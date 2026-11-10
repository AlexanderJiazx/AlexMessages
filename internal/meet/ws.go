package meet

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"alexmessage/internal/auth"
	"alexmessage/internal/db"
	"alexmessage/internal/debuglog"
	"alexmessage/internal/httpx"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: httpx.WebSocketOrigin,
}

// handleWS is the meeting control plane. The client protocol:
//
//	client → server: join {code, guest_name?, client_id?} · leave ·
//	                 signal {to, payload} · state {muted, cam_on, sharing} ·
//	                 host_mute {pid} · host_transfer {pid} · host_kick {pid} ·
//	                 set_guests {allowed} · volc_token · ping
//	server → client: hello {me} · joined {...} · peer_joined {participant} ·
//	                 peer_left {pid} · signal {from, payload} ·
//	                 peer_state {pid, ...} · host_changed {host_pid} ·
//	                 force_mute {by} · kicked {by} · replaced ·
//	                 guests_changed {allowed} · volc_token {volc} ·
//	                 error {message} · pong
//
// volc_token renews the VolcEngine room token (volcTokenTTL) and is answered
// only while the socket is on the roster of a volc meeting, so a participant
// who was kicked or left can't extend its access to the media room.
//
// client_id is a stable per-tab id: a join with an id already present in the
// room evicts the prior instance (so a reconnect/rejoin replaces its ghost and
// reclaims host) and the displaced socket receives a "replaced" message.
//
// Anonymous connections are allowed (hello carries me: null): a guest can
// join a room whose host enabled allow_guests by sending a guest_name.
func handleWS(c *gin.Context) {
	token := httpx.SessionToken(c, true)
	user := auth.ResolveSession(token, "user")

	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer ws.Close()
	if token != "" && user == nil {
		_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(4401, "Session revoked"), time.Now().Add(time.Second))
		return
	}
	ws.SetReadLimit(128 * 1024)
	if user != nil {
		stopped := make(chan struct{})
		watcherDone := make(chan struct{})
		defer func() { close(stopped); <-watcherDone }()
		go func() {
			defer close(watcherDone)
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-stopped:
					return
				case <-ticker.C:
					if auth.ResolveSession(token, "user") == nil {
						_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(4401, "Session revoked"), time.Now().Add(time.Second))
						_ = ws.Close()
						return
					}
				}
			}
		}()
	}

	cn := newConn(ws)
	defer cn.stop()
	var me any
	if user != nil {
		me = userPublic(user)
	}
	cn.send(gin.H{"type": "hello", "me": me})

	defer leaveRoom(cn)

	// Heartbeat: a socket that goes silent past pongWait is treated as dead so
	// its participant (and any host role it held) is reclaimed promptly. The
	// deadline is refreshed on every frame; the client's 20s ping keeps a
	// healthy connection alive.
	_ = ws.SetReadDeadline(time.Now().Add(pongWait))

	// Most frames fan out to the whole room, so each socket gets a budget:
	// a frame costs 1 + 1 per 2 KB, 400 at once, 100 back a second (a
	// newcomer's answers and ICE candidates to 8 peers cost about 200). A
	// frame over the budget is dropped, so a flood can't fill the other
	// participants' send queues (sendQueueSize) and get their sockets closed.
	budget := auth.NewAttemptLimiter(frameBurst, frameRefill, 1)
	for {
		_, raw, err := ws.ReadMessage()
		if err != nil {
			return
		}
		_ = ws.SetReadDeadline(time.Now().Add(pongWait))
		if !budget.AllowN("", 1+float64(len(raw))/2048) {
			continue
		}
		if user != nil && auth.ResolveSession(token, "user") == nil {
			_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(4401, "Session revoked"), time.Now().Add(time.Second))
			return
		}
		var data map[string]any
		if json.Unmarshal(raw, &data) != nil {
			continue
		}
		kind, ok := data["type"].(string)
		if !ok {
			continue
		}
		switch kind {
		case "ping":
			cn.send(gin.H{"type": "pong"})
		case "join":
			handleJoin(cn, user, data)
		case "leave":
			leaveRoom(cn)
		case "signal":
			handleSignal(cn, data)
		case "state":
			handleStateUpdate(cn, data)
		case "host_mute":
			handleHostMute(cn, data)
		case "host_transfer":
			handleHostTransfer(cn, data)
		case "host_kick":
			handleHostKick(cn, data)
		case "set_guests":
			handleSetGuests(cn, data)
		case "volc_token":
			handleVolcToken(cn)
		}
		// Unknown types are silently ignored.
	}
}

func meetError(message string) gin.H {
	return gin.H{"type": "error", "message": message}
}

// ---------- join / leave ----------

func handleJoin(cn *conn, user *db.User, data map[string]any) {
	code, _ := data["code"].(string)
	code = strings.ToLower(strings.TrimSpace(code))
	if code == "" {
		cn.send(meetError("Missing meeting code."))
		return
	}
	guestName := auth.CleanDisplayName(strField(data, "guest_name"))
	clientID := strings.TrimSpace(strField(data, "client_id"))
	if r := []rune(clientID); len(r) > 64 {
		clientID = string(r[:64])
	}

	// A connection joins at most one room; joining again moves it.
	leaveRoom(cn)

	meetMu.Lock()
	pruneRoomsLocked()
	rm := rooms[code]
	if rm == nil {
		meetMu.Unlock()
		cn.send(meetError("Meeting not found. It may have ended."))
		return
	}
	if user == nil && !rm.allowGuests {
		meetMu.Unlock()
		cn.send(meetError("This meeting requires you to sign in."))
		return
	}
	if user == nil && guestName == "" {
		meetMu.Unlock()
		cn.send(meetError("Enter your name to join."))
		return
	}
	if (user != nil && rm.removedUsers[user.ID]) || (clientID != "" && rm.removedClients[clientID]) {
		meetMu.Unlock()
		cn.send(meetError("The host removed you from this meeting."))
		return
	}

	// A single account (or a reconnecting tab) may only hold one slot: evict
	// any prior same-identity participant so an abnormal disconnect + rejoin
	// can't leave a ghost behind. Identity is the registered user id, or the
	// per-tab client id (which also covers guests and reconnects).
	var (
		evicted        []*participant
		hostWasEvicted bool
	)
	for _, pid := range rm.order {
		op := rm.parts[pid]
		if op == nil {
			continue
		}
		sameUser := user != nil && !op.user.Guest && op.user.ID == user.ID
		sameClient := clientID != "" && op.clientID == clientID
		if sameUser || sameClient {
			evicted = append(evicted, op)
		}
	}
	var evictedConns []*conn
	var evictedPids []int
	for _, op := range evicted {
		if rm.hostPid == op.pid {
			hostWasEvicted = true
		}
		delete(rm.parts, op.pid)
		for i, q := range rm.order {
			if q == op.pid {
				rm.order = append(rm.order[:i], rm.order[i+1:]...)
				break
			}
		}
		delete(connRoom, op.c)
		delete(connPart, op.c)
		evictedConns = append(evictedConns, op.c)
		evictedPids = append(evictedPids, op.pid)
	}

	p := &participant{
		pid:      allocPid(),
		c:        cn,
		clientID: clientID,
		joinedAt: db.NowTS(),
		muted:    false,
		camOn:    true,
	}
	if user != nil {
		p.user = userPublic(user)
	} else {
		p.user = meetUser{ID: -p.pid, Username: "guest", DisplayName: guestName, Guest: true}
	}
	rm.parts[p.pid] = p
	rm.order = append(rm.order, p.pid)
	rm.emptySince = 0
	// First joiner hosts; a rejoiner also reclaims host from its evicted ghost.
	if rm.hostPid == 0 || hostWasEvicted {
		rm.hostPid = p.pid
	}
	connRoom[cn] = rm
	connPart[cn] = p

	roster := make([]map[string]any, 0, len(rm.parts))
	var others []*conn
	for _, pid := range rm.order {
		op := rm.parts[pid]
		if op == nil {
			continue
		}
		roster = append(roster, participantPayload(op))
		if op.pid != p.pid {
			others = append(others, op.c)
		}
	}
	hostPid := rm.hostPid
	hostChanged := hostWasEvicted

	// Every message is queued while meetMu is held, so each recipient sees
	// this join in the same order as every other roster change.
	joined := gin.H{
		"type":         "joined",
		"code":         code,
		"mode":         rm.mode,
		"self_pid":     p.pid,
		"host_pid":     hostPid,
		"participants": roster,
		"allow_guests": rm.allowGuests,
	}
	if rm.mode == modeVolc {
		joined["volc"] = volcJoinPayload(code, p.pid)
	}
	cn.send(joined)

	// Tell the evicted instance it was taken over, then close it so its read
	// loop exits (its deferred leaveRoom is now a no-op — maps already cleared).
	for _, ec := range evictedConns {
		ec.send(gin.H{"type": "replaced"})
		ec.closeAfterFlush()
	}

	// Others see the ghost leave (+ any host handover) before the newcomer.
	announce := gin.H{"type": "peer_joined", "participant": participantPayload(p)}
	for _, oc := range others {
		for _, gone := range evictedPids {
			oc.send(gin.H{"type": "peer_left", "pid": gone})
		}
		if hostChanged {
			oc.send(gin.H{"type": "host_changed", "host_pid": hostPid})
		}
		oc.send(announce)
	}
	meetMu.Unlock()

	debuglog.Emit("meet", "info", "join", "Participant joined", map[string]any{
		"code": code, "pid": p.pid, "user": p.user.DisplayName, "guest": p.user.Guest, "host_pid": hostPid,
	})
}

// detachLocked removes cn's participant from its room, reassigning the host
// role if needed. Caller MUST hold meetMu. Returns the connections still in
// the room, the departed pid (0 when cn was not in a room), and the resulting
// host pid. The caller notifies (notifyLeft) before releasing meetMu.
func detachLocked(cn *conn) (notify []*conn, pid, hostPid int, hostChanged bool) {
	rm := connRoom[cn]
	p := connPart[cn]
	delete(connRoom, cn)
	delete(connPart, cn)
	if rm == nil || p == nil {
		return nil, 0, 0, false
	}
	delete(rm.parts, p.pid)
	for i, q := range rm.order {
		if q == p.pid {
			rm.order = append(rm.order[:i], rm.order[i+1:]...)
			break
		}
	}
	if rm.hostPid == p.pid {
		rm.hostPid = 0
		if len(rm.order) > 0 {
			rm.hostPid = rm.order[0] // longest-present participant inherits
			hostChanged = true
		}
	}
	if len(rm.parts) == 0 {
		rm.emptySince = db.NowTS()
	}
	for _, op := range rm.parts {
		notify = append(notify, op.c)
	}
	return notify, p.pid, rm.hostPid, hostChanged
}

func notifyLeft(notify []*conn, pid, hostPid int, hostChanged bool) {
	for _, oc := range notify {
		oc.send(gin.H{"type": "peer_left", "pid": pid})
		if hostChanged {
			oc.send(gin.H{"type": "host_changed", "host_pid": hostPid})
		}
	}
}

// leaveRoom detaches a connection from its room (no-op when not in one) and
// notifies the remaining participants, reassigning the host role if needed.
func leaveRoom(cn *conn) {
	meetMu.Lock()
	code := ""
	if rm := connRoom[cn]; rm != nil {
		code = rm.code
	}
	notify, pid, hostPid, hostChanged := detachLocked(cn)
	notifyLeft(notify, pid, hostPid, hostChanged)
	meetMu.Unlock()
	if pid == 0 {
		return
	}
	debuglog.Emit("meet", "info", "leave", "Participant left", map[string]any{"code": code, "pid": pid, "host_changed": hostChanged})
}

// ---------- mesh signaling relay ----------

// handleSignal forwards an opaque WebRTC payload (SDP description or ICE
// candidate) to one other participant in the same room.
func handleSignal(cn *conn, data map[string]any) {
	to, ok := parsePid(data["to"])
	if !ok {
		return
	}
	payload, present := data["payload"]
	if !present || payload == nil {
		return
	}
	meetMu.Lock()
	defer meetMu.Unlock()
	if rm, p := connRoom[cn], connPart[cn]; rm != nil && p != nil {
		if t := rm.parts[to]; t != nil {
			t.c.send(gin.H{"type": "signal", "from": p.pid, "payload": payload})
		}
	}
}

// ---------- AV state fan-out ----------

func handleStateUpdate(cn *conn, data map[string]any) {
	muted, _ := data["muted"].(bool)
	camOn, _ := data["cam_on"].(bool)
	sharing, _ := data["sharing"].(bool)

	meetMu.Lock()
	rm, p := connRoom[cn], connPart[cn]
	if rm == nil || p == nil {
		meetMu.Unlock()
		return
	}
	p.muted = muted
	p.camOn = camOn
	p.sharing = sharing
	payload := gin.H{"type": "peer_state", "pid": p.pid, "muted": muted, "cam_on": camOn, "sharing": sharing}
	for _, op := range rm.parts {
		if op.pid != p.pid {
			op.c.send(payload)
		}
	}
	meetMu.Unlock()
}

// ---------- host controls ----------

func handleHostMute(cn *conn, data map[string]any) {
	pid, ok := parsePid(data["pid"])
	if !ok {
		return
	}
	meetMu.Lock()
	defer meetMu.Unlock()
	rm, p := connRoom[cn], connPart[cn]
	switch {
	case rm == nil || p == nil:
	case rm.hostPid != p.pid:
		cn.send(meetError("Only the host can mute others."))
	default:
		if t := rm.parts[pid]; t != nil && t.pid != p.pid {
			t.muted = true
			t.c.send(gin.H{"type": "force_mute", "by": p.pid})
			payload := gin.H{"type": "peer_state", "pid": t.pid, "muted": true, "cam_on": t.camOn, "sharing": t.sharing}
			for _, op := range rm.parts {
				if op.pid != t.pid {
					op.c.send(payload)
				}
			}
		}
	}
}

// handleHostKick removes a participant from the room (host only). The kicked
// connection stays open but is detached; the client shows a "removed" screen.
func handleHostKick(cn *conn, data map[string]any) {
	pid, ok := parsePid(data["pid"])
	if !ok {
		return
	}
	meetMu.Lock()
	defer meetMu.Unlock()
	rm, p := connRoom[cn], connPart[cn]
	if rm == nil || p == nil {
		return
	}
	if rm.hostPid != p.pid {
		cn.send(meetError("Only the host can remove people."))
		return
	}
	t := rm.parts[pid]
	if t == nil || t.pid == p.pid {
		return
	}
	if !t.user.Guest {
		if rm.removedUsers == nil {
			rm.removedUsers = map[int]bool{}
		}
		rm.removedUsers[t.user.ID] = true
	}
	if t.clientID != "" {
		if rm.removedClients == nil {
			rm.removedClients = map[string]bool{}
		}
		rm.removedClients[t.clientID] = true
	}
	target := t.c
	notify, gone, hostPid, hostChanged := detachLocked(target)
	target.send(gin.H{"type": "kicked", "by": p.pid})
	notifyLeft(notify, gone, hostPid, hostChanged)
}

// handleSetGuests toggles whether non-registered users may join (host only).
func handleSetGuests(cn *conn, data map[string]any) {
	allowed, _ := data["allowed"].(bool)
	meetMu.Lock()
	defer meetMu.Unlock()
	rm, p := connRoom[cn], connPart[cn]
	if rm == nil || p == nil {
		return
	}
	if rm.hostPid != p.pid {
		cn.send(meetError("Only the host can change who may join."))
		return
	}
	rm.allowGuests = allowed
	payload := gin.H{"type": "guests_changed", "allowed": allowed}
	for _, op := range rm.parts {
		op.c.send(payload)
	}
}

func handleHostTransfer(cn *conn, data map[string]any) {
	pid, ok := parsePid(data["pid"])
	if !ok {
		return
	}
	meetMu.Lock()
	defer meetMu.Unlock()
	rm, p := connRoom[cn], connPart[cn]
	switch {
	case rm == nil || p == nil:
	case rm.hostPid != p.pid:
		cn.send(meetError("Only the host can transfer the host role."))
	default:
		if t := rm.parts[pid]; t != nil {
			rm.hostPid = t.pid
			payload := gin.H{"type": "host_changed", "host_pid": rm.hostPid}
			for _, op := range rm.parts {
				op.c.send(payload)
			}
		}
	}
}

// handleVolcToken answers a volc_token request with a fresh VolcEngine token
// for the caller's current pid. Only a socket that is on the roster of a volc
// meeting gets one; anyone else (kicked, left, never joined, a mesh meeting)
// gets no answer, so its token runs out within volcTokenTTL.
func handleVolcToken(cn *conn) {
	meetMu.Lock()
	defer meetMu.Unlock()
	rm, p := connRoom[cn], connPart[cn]
	if rm == nil || p == nil || rm.mode != modeVolc || rm.parts[p.pid] != p {
		return
	}
	cn.send(gin.H{"type": "volc_token", "volc": volcJoinPayload(rm.code, p.pid)})
}

// ---------- small helpers ----------

// parsePid mirrors int(data.get(...)) with its TypeError/ValueError guard.
func parsePid(v any) (int, bool) {
	switch x := v.(type) {
	case float64:
		return int(x), true
	case string:
		n, err := strconv.Atoi(x)
		if err != nil {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}
