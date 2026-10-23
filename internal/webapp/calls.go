package webapp

import (
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/db"
	"alexmessage/internal/debuglog"
	"alexmessage/internal/push"
	"alexmessage/internal/runtime"
)

// ---------- 1:1 DM calls ----------
//
// Calls are signaled over this /ws control plane; the audio and video flow
// peer to peer over WebRTC. The server owns the call state machine:
//
//	ringing ──accept──► active ──hangup──► ended (completed)
//	   └── cancel / decline / timeout / unavailable / busy ──► ended
//
// A user is in at most one call across all of their devices. A call binds one
// socket per side — the device that placed it and the device that answered —
// and only those two exchange signaling, so other open tabs never see
// duplicate offers or ICE candidates. Each binding also records the client's
// per-load device id, so a stale tab reconnecting cannot take the call over.
// When a bound socket drops, the call survives for callResumeGrace: the
// reconnecting client sends call_resume, receives the signals held for it,
// and the peer-to-peer media keeps flowing meanwhile.
//
// Every call that ends writes one type="call" message into the DM thread (and
// its calls row), so the outcome reaches every device, shows in the sidebar,
// and counts as unread for the callee only when it was missed.
//
// A phone whose app is closed rings through its call listener socket
// (call_listener.go): it hears call_incoming and call_ended, and can decline.

var (
	// callRingTimeout is how long an unanswered call rings before it ends as
	// no_answer.
	callRingTimeout = 45 * time.Second
	// callResumeGrace is how long a call outlives its dropped signaling
	// socket. ChatSocket retries every 1.5 s, so a network blip resumes well
	// inside it.
	callResumeGrace = 30 * time.Second
)

// callSignalBacklog caps the signals held for a side that is reconnecting.
const callSignalBacklog = 256

// pushCall sends one of a call's Web Pushes without holding up the engine;
// tests replace it with a recorder. Callers hold callsMu, so a call's pushes
// are queued in the order the engine decided on them, and each one waits for
// the one before it: the push that withdraws a ringing notification must not
// reach the push service ahead of the ringing push.
var pushCall = func(call *liveCall, kind string) {
	calleeID, payload, prev := call.calleeID, callPushPayload(*call, kind), call.pushQueue
	done := make(chan struct{})
	call.pushQueue = done
	go func() {
		defer close(done)
		if prev != nil {
			<-prev
		}
		push.SendToUser(calleeID, payload)
	}()
}

// callStartHook lets a test pause handleCallStart at a named point, with
// callsMu released, to replay a race there. It is read under callsMu.
var callStartHook func(point string)

// liveCall is a ringing or active call. Every field is guarded by callsMu.
type liveCall struct {
	id, channel, media string
	// nonce echoes the caller's client_call_id so its client can match the
	// call before it knows the server id.
	nonce              string
	callerID, calleeID int
	caller, callee     runtime.PublicUser
	active             bool
	startedAt          int64
	answeredAt         int64
	// callerConn placed the call and calleeConn answered it (nil while
	// ringing); either is nil while that side reconnects. The device ids
	// decide who may rebind after a reconnect.
	callerConn, calleeConn     *runtime.Client
	callerDevice, calleeDevice string
	reached                    bool // a callee device has been rung
	pushed                     bool // an incoming-call Web Push went out
	withdrawn                  bool // a call_over push replaced it
	// pushQueue closes once the last push queued for this call has gone out.
	pushQueue chan struct{}
	// status is the outcome, set when endCall ends the call.
	status string
	// lostAt is when the side whose loss ends the call dropped; the call
	// is logged as ending then, not when the resume grace ran out.
	lostAt    int64
	ringTimer *time.Timer
	grace     map[int]*time.Timer // user id → pending end
	backlog   map[int][]any       // user id → signals held for them
}

var (
	callsMu   sync.Mutex
	liveCalls = map[string]*liveCall{}
	userCalls = map[int]*liveCall{}
)

// callView is a live call's wire shape as one participant sees it.
type callView struct {
	ID         string             `json:"id"`
	Channel    string             `json:"channel"`
	CallerID   int                `json:"caller_id"`
	CalleeID   int                `json:"callee_id"`
	Media      string             `json:"media"`
	State      string             `json:"state"`
	StartedAt  int64              `json:"started_at"`
	AnsweredAt *int64             `json:"answered_at"`
	Peer       runtime.PublicUser `json:"peer"`
	ICEServers []iceServer        `json:"ice_servers"`
}

// viewFor snapshots the call for one participant. Caller holds callsMu.
func (c *liveCall) viewFor(viewerID int) callView {
	v := callView{
		ID:         c.id,
		Channel:    c.channel,
		CallerID:   c.callerID,
		CalleeID:   c.calleeID,
		Media:      c.media,
		State:      "ringing",
		StartedAt:  c.startedAt,
		Peer:       c.callee,
		ICEServers: iceServersFor(viewerID),
	}
	if viewerID == c.calleeID {
		v.Peer = c.caller
	}
	if c.active {
		v.State = "active"
		answered := c.answeredAt
		v.AnsweredAt = &answered
	}
	return v
}

// handleCallMessage routes the call_* frames of the /ws read loop.
func handleCallMessage(client *runtime.Client, user *db.User, kind string, data map[string]any) {
	callID := strField(data, "call_id")
	device := truncateRunes(strField(data, "device"), 64)
	switch kind {
	case "call_start":
		handleCallStart(client, user, device, data)
	case "call_accept":
		handleCallAccept(client, user.ID, callID, device, "")
	case "call_hangup":
		handleCallHangup(user.ID, callID, strField(data, "reason") == "failed")
	case "call_signal":
		handleCallSignal(client, callID, data)
	case "call_resume":
		handleCallResume(client, user.ID, callID, device)
	}
}

func handleCallStart(client *runtime.Client, user *db.User, device string, data map[string]any) {
	nonce := truncateRunes(strField(data, "client_call_id"), 64)
	media := "video"
	if strField(data, "media") == "audio" {
		media = "audio"
	}
	reject := func(reason string) {
		runtime.SendJSON(client, gin.H{"type": "call_error", "client_call_id": nonce, "error": reason})
	}
	peerID, ok := parsePeerID(data["peer_id"])
	if !ok || peerID == user.ID {
		reject("invalid")
		return
	}
	peer, _ := db.GetUserByID(peerID)
	if peer == nil || peer.Status != "approved" {
		reject("invalid")
		return
	}

	callsMu.Lock()
	if mine := userCalls[user.ID]; mine != nil {
		// Both sides dialed at once: the call in the way is the peer ringing
		// us, so answering it connects them.
		if !mine.active && mine.callerID == peerID {
			id := mine.id
			callsMu.Unlock()
			handleCallAccept(client, user.ID, id, device, nonce)
			return
		}
		callsMu.Unlock()
		reject("busy_self")
		return
	}
	id, err := tokenHex(8)
	if err != nil {
		callsMu.Unlock()
		reject("invalid")
		return
	}
	call := &liveCall{
		id:           id,
		channel:      db.DMChannelID(user.ID, peerID),
		media:        media,
		nonce:        nonce,
		callerID:     user.ID,
		calleeID:     peerID,
		caller:       runtime.UserPublic(user),
		callee:       runtime.UserPublic(peer),
		startedAt:    db.NowTS(),
		callerConn:   client,
		callerDevice: device,
		grace:        map[int]*time.Timer{},
		backlog:      map[int][]any{},
	}
	busy := userCalls[peerID] != nil
	liveCalls[id] = call
	userCalls[user.ID] = call
	if !busy {
		userCalls[peerID] = call
	}
	outgoing := call.viewFor(user.ID)
	hook := callStartHook
	callsMu.Unlock()

	debuglog.Emit("messages", "info", "call_start", "Call placed", map[string]any{"call": id, "media": media, "user": user.Username})
	runtime.SendJSON(client, gin.H{"type": "call_outgoing", "client_call_id": nonce, "call": outgoing})
	if busy {
		endCall(id, unconditionally(db.CallBusy))
		return
	}
	sockets := runtime.PresenceTracker().SocketsFor(map[int]struct{}{peerID: {}})
	listeners := callListeners.SocketsFor(map[int]struct{}{peerID: {}})
	subs, _ := db.ListPushSubscriptions(peerID)
	if len(sockets) == 0 && len(listeners) == 0 && len(subs) == 0 {
		endCall(id, unconditionally(db.CallUnavailable))
		return
	}
	if hook != nil {
		hook("looked_up")
	}

	callsMu.Lock()
	// The callee's own call_start may have crossed this one and answered it
	// (the glare path above), which already told the callee's other devices.
	if liveCalls[id] != call || call.active {
		callsMu.Unlock()
		return // ended or answered while we looked the callee up
	}
	call.ringTimer = time.AfterFunc(callRingTimeout, func() { endCall(id, whileRinging(db.CallNoAnswer)) })
	incoming := call.viewFor(peerID)
	callsMu.Unlock()
	if hook != nil {
		hook("ringing")
	}

	seen := false
	rung := []*runtime.Client{}
	for _, s := range sockets {
		if err := s.Send(gin.H{"type": "call_incoming", "call": incoming}); err != nil {
			runtime.PresenceTracker().Remove(s)
			continue
		}
		rung = append(rung, s)
		seen = seen || s.Visible()
	}
	// A listener rings a phone whose app is closed; no screen shows the call.
	for _, s := range listeners {
		if err := s.Send(gin.H{"type": "call_incoming", "call": incoming}); err != nil {
			callListeners.Remove(s)
			continue
		}
		rung = append(rung, s)
	}
	callsMu.Lock()
	if liveCalls[id] != call || call.active {
		// The call was answered or ended while we rang. The call_ended that
		// said so may have reached a device ahead of its call_incoming, so the
		// devices just rung hear it again, after the ring. While the call is
		// still on, the device that answered it is left out.
		reason, keep := call.status, (*runtime.Client)(nil)
		if liveCalls[id] == call {
			reason, keep = "answered_elsewhere", call.calleeConn
		}
		callsMu.Unlock()
		stop := []*runtime.Client{}
		for _, s := range rung {
			if s != keep {
				stop = append(stop, s)
			}
		}
		runtime.Broadcast(gin.H{"type": "call_ended", "call_id": id, "reason": reason}, stop)
		return
	}
	reached := len(rung) > 0
	call.reached = call.reached || reached
	// Ring through Web Push unless a callee screen is showing the call: that
	// covers closed tabs, background tabs, and suspended phone browsers.
	if !seen && len(subs) > 0 {
		call.pushed = true
		pushCall(call, "call")
	}
	callsMu.Unlock()
	if reached {
		runtime.SendJSON(client, gin.H{"type": "call_ringing", "call_id": id})
	}
}

// handleCallAccept binds the answering device and starts negotiation: the
// caller, on call_accepted, sends the offer. nonce is set when the accept is
// a call_start that crossed the peer's own call (handleCallStart).
func handleCallAccept(client *runtime.Client, userID int, callID, device, nonce string) {
	callsMu.Lock()
	call := liveCalls[callID]
	if call == nil || call.calleeID != userID || call.active || userCalls[userID] != call {
		callsMu.Unlock()
		runtime.SendJSON(client, gin.H{"type": "call_ended", "call_id": callID, "client_call_id": nonce, "reason": "gone"})
		return
	}
	call.active = true
	call.answeredAt = db.NowTS()
	call.calleeConn = client
	call.calleeDevice = device
	if call.ringTimer != nil {
		call.ringTimer.Stop()
	}
	// The ringing Web Push stays on screen until something replaces it, and
	// the answer may have come from another device.
	if call.pushed && !call.withdrawn {
		call.withdrawn = true
		pushCall(call, "call_over")
	}
	callerConn := call.callerConn
	callerNonce := call.nonce
	callerView, calleeView := call.viewFor(call.callerID), call.viewFor(userID)
	callsMu.Unlock()

	debuglog.Emit("messages", "info", "call_accept", "Call answered", map[string]any{"call": callID})
	runtime.SendJSON(client, gin.H{"type": "call_accepted", "client_call_id": nonce, "call": calleeView})
	if callerConn != nil {
		runtime.SendJSON(callerConn, gin.H{"type": "call_accepted", "client_call_id": callerNonce, "call": callerView})
	}
	callee := map[int]struct{}{userID: {}}
	others := []*runtime.Client{}
	for _, s := range runtime.PresenceTracker().SocketsFor(callee) {
		if s != client {
			others = append(others, s)
		}
	}
	// The callee's listeners stop ringing too.
	others = append(others, callListeners.SocketsFor(callee)...)
	runtime.Broadcast(gin.H{"type": "call_ended", "call_id": callID, "reason": "answered_elsewhere"}, others)
}

// handleCallHangup ends the call from either side: hanging up a ringing call
// cancels it (caller) or declines it (callee). failed marks an answered call
// whose media never connected.
func handleCallHangup(userID int, callID string, failed bool) {
	endCall(callID, func(c *liveCall) (string, bool) {
		switch {
		case userID != c.callerID && userID != c.calleeID:
			return "", false
		case c.active && failed:
			return db.CallFailed, true
		case c.active:
			return db.CallCompleted, true
		case userID == c.callerID:
			return db.CallCancelled, true
		default:
			return db.CallDeclined, true
		}
	})
}

// endUserCall ends userID's live call as if they had hung up. Logout and
// account deletion run it first, so the peer hears at once (and, for a
// deletion, the thread entry is written while both users still exist).
func endUserCall(userID int) {
	callsMu.Lock()
	call := userCalls[userID]
	if call == nil {
		callsMu.Unlock()
		return
	}
	id := call.id
	callsMu.Unlock()
	handleCallHangup(userID, id, false)
}

// handleCallSignal relays an opaque SDP/ICE/state payload between the two
// bound sockets, holding it while the receiving side reconnects.
func handleCallSignal(client *runtime.Client, callID string, data map[string]any) {
	payload, ok := data["payload"].(map[string]any)
	if !ok {
		return
	}
	callsMu.Lock()
	call := liveCalls[callID]
	if call == nil || !call.active {
		callsMu.Unlock()
		return
	}
	var target *runtime.Client
	var targetID int
	switch client {
	case call.callerConn:
		target, targetID = call.calleeConn, call.calleeID
	case call.calleeConn:
		target, targetID = call.callerConn, call.callerID
	default:
		callsMu.Unlock()
		return
	}
	if target == nil {
		if len(call.backlog[targetID]) < callSignalBacklog {
			call.backlog[targetID] = append(call.backlog[targetID], payload)
		}
		callsMu.Unlock()
		return
	}
	callsMu.Unlock()
	runtime.SendJSON(target, gin.H{"type": "call_signal", "call_id": callID, "payload": payload})
}

// handleCallResume rebinds a reconnected device to its call and flushes the
// signals held for it. A device that is not part of the call is told so.
func handleCallResume(client *runtime.Client, userID int, callID, device string) {
	callsMu.Lock()
	call := liveCalls[callID]
	reason := "gone"
	bound := false
	if call != nil && userCalls[userID] == call {
		switch {
		case userID == call.callerID && device == call.callerDevice:
			call.callerConn, bound = client, true
		case userID == call.calleeID && !call.active:
			bound = true // still ringing: nothing to rebind
		case userID == call.calleeID && device == call.calleeDevice:
			call.calleeConn, bound = client, true
		case userID == call.calleeID:
			reason = "answered_elsewhere"
		}
	}
	if !bound {
		callsMu.Unlock()
		runtime.SendJSON(client, gin.H{"type": "call_ended", "call_id": callID, "reason": reason})
		return
	}
	if t := call.grace[userID]; t != nil {
		t.Stop()
		delete(call.grace, userID)
	}
	backlog := call.backlog[userID]
	delete(call.backlog, userID)
	view := call.viewFor(userID)
	callsMu.Unlock()
	runtime.SendJSON(client, gin.H{"type": "call_resumed", "call": view})
	for _, p := range backlog {
		runtime.SendJSON(client, gin.H{"type": "call_signal", "call_id": callID, "payload": p})
	}
}

// deliverRingingCalls rings a freshly connected device (opened from a push,
// or reconnected) or call listener when a call to its user is still ringing.
func deliverRingingCalls(client *runtime.Client) {
	callsMu.Lock()
	call := userCalls[client.UserID]
	if call == nil || call.active || call.calleeID != client.UserID {
		callsMu.Unlock()
		return
	}
	view := call.viewFor(client.UserID)
	first := !call.reached
	call.reached = true
	callerConn, id := call.callerConn, call.id
	callsMu.Unlock()
	runtime.SendJSON(client, gin.H{"type": "call_incoming", "call": view})
	if first && callerConn != nil {
		runtime.SendJSON(callerConn, gin.H{"type": "call_ringing", "call_id": id})
	}
}

// callSocketGone handles a call's bound socket closing. A page unload (close
// code 1001, Going Away) ends the call at once, since that page's media is
// gone with it; any other drop starts the resume grace.
func callSocketGone(client *runtime.Client, unloaded bool) {
	callsMu.Lock()
	call := userCalls[client.UserID]
	if call == nil {
		callsMu.Unlock()
		return
	}
	uid := client.UserID
	switch {
	case uid == call.callerID && call.callerConn == client:
		call.callerConn = nil
	case uid == call.calleeID && call.calleeConn == client:
		call.calleeConn = nil
	default:
		callsMu.Unlock()
		return
	}
	if t := call.grace[uid]; t != nil {
		t.Stop()
	}
	id, lostAt := call.id, db.NowTS()
	end := func() {
		endCall(id, func(c *liveCall) (string, bool) {
			gone := (uid == c.callerID && c.callerConn == nil) || (uid == c.calleeID && c.calleeConn == nil)
			if !gone {
				return "", false
			}
			c.lostAt = lostAt
			if c.active {
				return db.CallCompleted, true
			}
			return db.CallCancelled, true
		})
	}
	if unloaded {
		callsMu.Unlock()
		end()
		return
	}
	call.grace[uid] = time.AfterFunc(callResumeGrace, end)
	callsMu.Unlock()
}

func unconditionally(status string) func(*liveCall) (string, bool) {
	return func(*liveCall) (string, bool) { return status, true }
}

func whileRinging(status string) func(*liveCall) (string, bool) {
	return func(c *liveCall) (string, bool) { return status, !c.active }
}

// endCall finishes a live call when decide — run under callsMu so it sees
// the state it decides on — returns a status: the call is unregistered, both
// users' devices are told, and the outcome is written into the thread.
func endCall(callID string, decide func(*liveCall) (string, bool)) {
	callsMu.Lock()
	call := liveCalls[callID]
	if call == nil {
		callsMu.Unlock()
		return
	}
	status, ok := decide(call)
	if !ok {
		callsMu.Unlock()
		return
	}
	call.status = status
	delete(liveCalls, callID)
	for _, uid := range []int{call.callerID, call.calleeID} {
		if userCalls[uid] == call {
			delete(userCalls, uid)
		}
	}
	if call.ringTimer != nil {
		call.ringTimer.Stop()
	}
	for _, t := range call.grace {
		t.Stop()
	}
	// The call's last push replaces the ringing one (same tag): a missed call
	// says so, and any other outcome withdraws it quietly, unless the answer
	// already did.
	if call.pushed {
		switch {
		case db.CallMissed(status):
			pushCall(call, "missed_call")
		case !call.withdrawn:
			call.withdrawn = true
			pushCall(call, "call_over")
		}
	}
	snap := *call
	callsMu.Unlock()

	endedAt := db.NowTS()
	if snap.lostAt != 0 {
		endedAt = max(snap.lostAt, snap.answeredAt)
	}
	participants := map[int]struct{}{snap.callerID: {}, snap.calleeID: {}}
	recipients := append(runtime.PresenceTracker().SocketsFor(participants), callListeners.SocketsFor(participants)...)
	runtime.Broadcast(gin.H{"type": "call_ended", "call_id": snap.id, "client_call_id": snap.nonce, "reason": status}, recipients)

	var answeredAt *int64
	if snap.active {
		answeredAt = &snap.answeredAt
	}
	msgID, err := tokenHex(6)
	var rec db.HistoryMessage
	if err == nil {
		rec, err = db.InsertCallMessage(msgID, snap.channel, snap.callerID, snap.calleeID, snap.media, status, snap.startedAt, answeredAt, endedAt)
	}
	var duration int64
	if answeredAt != nil {
		duration = endedAt - *answeredAt
	}
	debuglog.Emit("messages", "info", "call_end", "Call ended", map[string]any{"call": snap.id, "status": status, "duration": duration})
	if err != nil {
		debuglog.Emit("messages", "error", "call_record_failed", "Call outcome could not be saved", map[string]any{"call": snap.id, "status": status})
		return
	}
	// The thread entry goes out last: once a client has it, the call is over
	// on the server too. A caller whose account was deleted mid-call has no
	// author, as in history.
	var author *runtime.PublicUser
	if rec.UserID != nil {
		author = &snap.caller
	}
	msg := broadcastMessage{
		ID:          rec.ID,
		Type:        rec.Type,
		Channel:     rec.Channel,
		UserID:      rec.UserID,
		Author:      author,
		Text:        rec.Text,
		Attachments: rec.Attachments,
		CreatedAt:   rec.CreatedAt,
		Position:    rec.Position,
		Call:        rec.Call,
	}
	runtime.Broadcast(gin.H{"type": "message", "channel": snap.channel, "message": msg}, runtime.RecipientsForChannel(snap.channel))
}

// callPushPayload is the Web Push sent to the callee that rings (kind
// "call"), reports a missed call (kind "missed_call"), or says the ringing
// stopped because the call was answered or declined (kind "call_over"). All
// three share one tag per call, so each later one replaces the ringing one.
func callPushPayload(call liveCall, kind string) map[string]any {
	title := call.caller.DisplayName
	if title == "" {
		title = call.caller.Username
	}
	what := "voice call"
	if call.media == "video" {
		what = "video call"
	}
	body := "Incoming " + what
	switch {
	case kind == "missed_call":
		body = "Missed " + what
	case kind == "call_over" && call.active:
		body = "Answered " + what
	case kind == "call_over":
		// A call that stopped ringing without an answer and wasn't missed
		// was declined (db.CallMissed).
		body = "Declined " + what
	}
	return map[string]any{
		"title":               title,
		"body":                body,
		"url":                 "/",
		"channel":             call.channel,
		"sender_id":           call.callerID,
		"icon":                "/static/icons/favicon-192.png",
		"badge":               "/static/icons/favicon-32.png",
		"tag":                 "call-" + call.id,
		"kind":                kind,
		"call_id":             call.id,
		"require_interaction": kind == "call",
		"created_at":          call.startedAt,
	}
}
