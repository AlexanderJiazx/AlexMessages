package voicecall

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"alexmessage/internal/auth"
	"alexmessage/internal/db"
	"alexmessage/internal/httpx"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// handleWS is the meeting control plane. The client protocol:
//
//	client → server: join {code, guest_name?} · leave · signal {to, payload} ·
//	                 state {muted, cam_on, sharing} · host_mute {pid} ·
//	                 host_transfer {pid} · host_kick {pid} ·
//	                 set_guests {allowed} · ping
//	server → client: hello {me} · joined {...} · peer_joined {participant} ·
//	                 peer_left {pid} · signal {from, payload} ·
//	                 peer_state {pid, ...} · host_changed {host_pid} ·
//	                 force_mute {by} · kicked {by} · guests_changed {allowed} ·
//	                 error {message} · pong
//
// Anonymous connections are allowed (hello carries me: null): a guest can
// join a room whose host enabled allow_guests by sending a guest_name.
func handleWS(c *gin.Context) {
	user := auth.ResolveSession(httpx.Cookie(c, auth.UserCookie), "user")

	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer ws.Close()

	cn := &conn{ws: ws}
	var me any
	if user != nil {
		me = userPublic(user)
	}
	cn.send(gin.H{"type": "hello", "me": me})

	defer leaveRoom(cn)

	for {
		_, raw, err := ws.ReadMessage()
		if err != nil {
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
	guestName := strings.TrimSpace(strField(data, "guest_name"))
	if r := []rune(guestName); len(r) > 40 {
		guestName = string(r[:40])
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
	p := &participant{
		pid:      allocPid(),
		c:        cn,
		joinedAt: db.NowTS(),
		muted:    false,
		camOn:    true,
	}
	if user != nil {
		p.user = userPublic(user)
	} else {
		p.user = vcUser{ID: -p.pid, Username: "guest", DisplayName: guestName, Guest: true}
	}
	rm.parts[p.pid] = p
	rm.order = append(rm.order, p.pid)
	rm.emptySince = 0
	if rm.hostPid == 0 {
		rm.hostPid = p.pid // first joiner (in practice: the creator) hosts
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
	mode := rm.mode
	allowGuests := rm.allowGuests
	meetMu.Unlock()

	joined := gin.H{
		"type":         "joined",
		"code":         code,
		"mode":         mode,
		"self_pid":     p.pid,
		"host_pid":     hostPid,
		"participants": roster,
		"allow_guests": allowGuests,
	}
	if mode == modeVolc {
		joined["volc"] = volcJoinPayload(code, p.pid)
	}
	cn.send(joined)

	announce := gin.H{"type": "peer_joined", "participant": participantPayload(p)}
	for _, oc := range others {
		oc.send(announce)
	}
}

// detachLocked removes cn's participant from its room, reassigning the host
// role if needed. Caller MUST hold meetMu. Returns the connections still in
// the room, the departed pid (0 when cn was not in a room), and the resulting
// host pid. Notifying happens after the lock is released.
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
	notify, pid, hostPid, hostChanged := detachLocked(cn)
	meetMu.Unlock()
	if pid == 0 {
		return
	}
	notifyLeft(notify, pid, hostPid, hostChanged)
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
	var target *conn
	from := 0
	meetMu.Lock()
	if rm, p := connRoom[cn], connPart[cn]; rm != nil && p != nil {
		if t := rm.parts[to]; t != nil {
			target = t.c
			from = p.pid
		}
	}
	meetMu.Unlock()
	if target == nil {
		return
	}
	target.send(gin.H{"type": "signal", "from": from, "payload": payload})
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
	var notify []*conn
	for _, op := range rm.parts {
		if op.pid != p.pid {
			notify = append(notify, op.c)
		}
	}
	meetMu.Unlock()

	for _, oc := range notify {
		oc.send(payload)
	}
}

// ---------- host controls ----------

func handleHostMute(cn *conn, data map[string]any) {
	pid, ok := parsePid(data["pid"])
	if !ok {
		return
	}
	var (
		targetConn *conn
		notify     []*conn
		payload    gin.H
		byPid      int
	)
	meetMu.Lock()
	rm, p := connRoom[cn], connPart[cn]
	switch {
	case rm == nil || p == nil:
	case rm.hostPid != p.pid:
		meetMu.Unlock()
		cn.send(meetError("Only the host can mute others."))
		return
	default:
		if t := rm.parts[pid]; t != nil && t.pid != p.pid {
			t.muted = true
			targetConn = t.c
			byPid = p.pid
			payload = gin.H{"type": "peer_state", "pid": t.pid, "muted": true, "cam_on": t.camOn, "sharing": t.sharing}
			for _, op := range rm.parts {
				if op.pid != t.pid {
					notify = append(notify, op.c)
				}
			}
		}
	}
	meetMu.Unlock()

	if targetConn == nil {
		return
	}
	targetConn.send(gin.H{"type": "force_mute", "by": byPid})
	for _, oc := range notify {
		oc.send(payload)
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
	rm, p := connRoom[cn], connPart[cn]
	if rm == nil || p == nil {
		meetMu.Unlock()
		return
	}
	if rm.hostPid != p.pid {
		meetMu.Unlock()
		cn.send(meetError("Only the host can remove people."))
		return
	}
	t := rm.parts[pid]
	if t == nil || t.pid == p.pid {
		meetMu.Unlock()
		return
	}
	target := t.c
	notify, gone, hostPid, hostChanged := detachLocked(target)
	meetMu.Unlock()

	target.send(gin.H{"type": "kicked", "by": p.pid})
	notifyLeft(notify, gone, hostPid, hostChanged)
}

// handleSetGuests toggles whether non-registered users may join (host only).
func handleSetGuests(cn *conn, data map[string]any) {
	allowed, _ := data["allowed"].(bool)
	meetMu.Lock()
	rm, p := connRoom[cn], connPart[cn]
	if rm == nil || p == nil {
		meetMu.Unlock()
		return
	}
	if rm.hostPid != p.pid {
		meetMu.Unlock()
		cn.send(meetError("Only the host can change who may join."))
		return
	}
	rm.allowGuests = allowed
	var notify []*conn
	for _, op := range rm.parts {
		notify = append(notify, op.c)
	}
	meetMu.Unlock()

	payload := gin.H{"type": "guests_changed", "allowed": allowed}
	for _, oc := range notify {
		oc.send(payload)
	}
}

func handleHostTransfer(cn *conn, data map[string]any) {
	pid, ok := parsePid(data["pid"])
	if !ok {
		return
	}
	var notify []*conn
	meetMu.Lock()
	rm, p := connRoom[cn], connPart[cn]
	switch {
	case rm == nil || p == nil:
	case rm.hostPid != p.pid:
		meetMu.Unlock()
		cn.send(meetError("Only the host can transfer the host role."))
		return
	default:
		if t := rm.parts[pid]; t != nil {
			rm.hostPid = t.pid
			for _, op := range rm.parts {
				notify = append(notify, op.c)
			}
		}
	}
	hostPid := 0
	if rm != nil {
		hostPid = rm.hostPid
	}
	meetMu.Unlock()

	payload := gin.H{"type": "host_changed", "host_pid": hostPid}
	for _, oc := range notify {
		oc.send(payload)
	}
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
