package voicecall

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"alexmessage/internal/auth"
	"alexmessage/internal/db"
	"alexmessage/internal/httpx"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func handleWS(c *gin.Context) {
	user := auth.ResolveSession(httpx.Cookie(c, auth.UserCookie), "user")

	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	if user == nil {
		_ = ws.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(4401, ""), time.Now().Add(time.Second))
		_ = ws.Close()
		return
	}
	defer ws.Close()

	cn := &conn{ws: ws}
	userID := user.ID
	presence.add(cn, userID)

	// Snapshot the user's existing active call (if any) so a freshly opened tab
	// can rejoin/observe.
	var snapshot map[string]any
	callMu.Lock()
	if existingID, ok := userToCall[userID]; ok {
		if call := calls[existingID]; call != nil {
			otherID := call.calleeID
			direction := "outgoing"
			if call.callerID != userID {
				otherID = call.callerID
				direction = "incoming"
			}
			other, _ := db.GetUserByID(otherID)
			var peer any
			if other != nil {
				peer = userPublic(other)
			}
			snapshot = map[string]any{
				"call_id":   call.id,
				"state":     call.state,
				"direction": direction,
				"peer":      peer,
			}
		}
	}
	callMu.Unlock()

	cn.send(map[string]any{
		"type":        "hello",
		"me":          userPublic(user),
		"active_call": snapshot,
	})

	defer onSocketGone(cn, userID)

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
			cn.send(map[string]any{"type": "pong"})
		case "call_start":
			handleCallStart(cn, user, data)
		case "call_accept":
			handleCallAccept(cn, user, data)
		case "call_decline":
			handleCallDecline(cn, user, data)
		case "call_cancel":
			handleCallCancel(cn, user, data)
		case "call_end":
			handleCallEnd(cn, user, data)
		case "call_signal":
			handleCallSignal(cn, user, data)
		}
		// Unknown types are silently ignored.
	}
}

// onSocketGone cleans up when a socket closes. If this was the user's last
// socket and they were in a call — or the call was bound to this specific
// socket — the call ends.
func onSocketGone(cn *conn, userID int) {
	presence.remove(cn)
	callMu.Lock()
	defer callMu.Unlock()
	callID, ok := userToCall[userID]
	if !ok {
		return
	}
	call := calls[callID]
	if call == nil {
		return
	}
	bound := cn == call.callerWS || cn == call.calleeWS
	anyLeft := presence.isOnline(userID)
	if !bound && anyLeft {
		return
	}
	if call.state == "ringing" {
		if call.callerID == userID {
			finalizeCall(call, "cancelled", "caller_left")
		} else {
			finalizeCall(call, "missed", "callee_left")
		}
	} else { // accepted
		finalizeCall(call, "completed", "peer_left")
	}
}

// ---------- command handlers ----------

func handleCallStart(cn *conn, user *db.User, data map[string]any) {
	username, _ := data["to_username"].(string)
	if trimSpace(username) == "" {
		cn.send(callError("Missing username."))
		return
	}
	target, _ := db.GetUserByUsername(auth.NormalizeUsername(username))
	if target == nil || target.Status != "approved" {
		cn.send(callError("No such user."))
		return
	}
	if target.ID == user.ID {
		cn.send(callError("You can't call yourself."))
		return
	}

	callMu.Lock()
	defer callMu.Unlock()

	if _, busy := userToCall[user.ID]; busy {
		cn.send(callError("You're already in a call."))
		return
	}
	if !presence.isOnline(target.ID) {
		// Persist an "unavailable" entry so the user sees it in history.
		ts := db.NowTS()
		caller, callee := user.ID, target.ID
		_, _ = db.InsertCall(&caller, &callee, ts, nil, ts, "unavailable")
		cn.send(map[string]any{"type": "call_unavailable", "reason": "offline", "peer": userPublic(target)})
		pushRecentCalls(user.ID)
		return
	}
	if _, targetBusy := userToCall[target.ID]; targetBusy {
		cn.send(map[string]any{"type": "call_unavailable", "reason": "busy", "peer": userPublic(target)})
		return
	}

	callID := allocCallID()
	call := &Call{
		id:        callID,
		callerID:  user.ID,
		calleeID:  target.ID,
		callerWS:  cn,
		state:     "ringing",
		startedAt: db.NowTS(),
	}
	calls[callID] = call
	userToCall[user.ID] = callID
	userToCall[target.ID] = callID

	// Caller tab gets the canonical "outgoing" view.
	cn.send(map[string]any{"type": "call_ringing", "call_id": callID, "peer": userPublic(target)})
	// Other caller tabs learn an outgoing call is in flight.
	sendToUser(user.ID, map[string]any{"type": "call_outgoing_elsewhere", "call_id": callID, "peer": userPublic(target)}, cn)
	// Every callee tab is rung.
	sendToUser(target.ID, map[string]any{"type": "incoming_call", "call_id": callID, "from": userPublic(user)}, nil)
}

func handleCallAccept(cn *conn, user *db.User, data map[string]any) {
	cid, ok := parseCallID(data["call_id"])
	if !ok {
		return
	}
	callMu.Lock()
	defer callMu.Unlock()
	call := calls[cid]
	if call == nil || call.calleeID != user.ID {
		cn.send(callError("Call not found."))
		return
	}
	if call.state != "ringing" {
		cn.send(callError("Call already resolved."))
		return
	}
	call.state = "accepted"
	call.calleeWS = cn
	now := db.NowTS()
	call.answeredAt = &now

	// The caller side starts negotiation (creates the SDP offer).
	call.callerWS.send(map[string]any{"type": "call_accepted", "call_id": call.id, "peer": userPublic(user)})
	// Confirm to the accepting tab.
	var callerPeer any
	if caller, _ := db.GetUserByID(call.callerID); caller != nil {
		callerPeer = userPublic(caller)
	}
	cn.send(map[string]any{"type": "call_connected", "call_id": call.id, "peer": callerPeer})
	// Tell other callee tabs the banner is no longer relevant.
	sendToUser(call.calleeID, map[string]any{"type": "call_taken", "call_id": call.id}, cn)
}

func handleCallDecline(cn *conn, user *db.User, data map[string]any) {
	cid, ok := parseCallID(data["call_id"])
	if !ok {
		return
	}
	callMu.Lock()
	defer callMu.Unlock()
	call := calls[cid]
	if call == nil || call.calleeID != user.ID {
		return
	}
	if call.state != "ringing" {
		return
	}
	finalizeCall(call, "declined", "declined")
}

func handleCallCancel(cn *conn, user *db.User, data map[string]any) {
	cid, ok := parseCallID(data["call_id"])
	if !ok {
		return
	}
	callMu.Lock()
	defer callMu.Unlock()
	call := calls[cid]
	if call == nil || call.callerID != user.ID {
		return
	}
	if call.state != "ringing" {
		return
	}
	finalizeCall(call, "cancelled", "cancelled")
}

func handleCallEnd(cn *conn, user *db.User, data map[string]any) {
	cid, ok := parseCallID(data["call_id"])
	if !ok {
		return
	}
	callMu.Lock()
	defer callMu.Unlock()
	call := calls[cid]
	if call == nil {
		return
	}
	if user.ID != call.callerID && user.ID != call.calleeID {
		return
	}
	if call.state == "ringing" {
		// Treat as cancel/decline depending on who hung up.
		if call.callerID == user.ID {
			finalizeCall(call, "cancelled", "cancelled")
		} else {
			finalizeCall(call, "declined", "declined")
		}
		return
	}
	finalizeCall(call, "completed", "hangup")
}

// handleCallSignal forwards an opaque SDP/ICE payload to the other participant.
// Only the two bound sockets may exchange signaling, so multi-tab fan-out never
// duplicates ICE candidates. The send happens after releasing callMu.
func handleCallSignal(cn *conn, user *db.User, data map[string]any) {
	cid, ok := parseCallID(data["call_id"])
	if !ok {
		return
	}
	payload, present := data["payload"]
	if !present || payload == nil {
		return
	}
	var target *conn
	callMu.Lock()
	call := calls[cid]
	if call != nil && call.state == "accepted" {
		if cn == call.callerWS && call.calleeWS != nil {
			target = call.calleeWS
		} else if cn == call.calleeWS && call.callerWS != nil {
			target = call.callerWS
		}
	}
	callMu.Unlock()
	if target == nil {
		return
	}
	target.send(map[string]any{"type": "call_signal", "call_id": cid, "payload": payload})
}

// ---------- small helpers ----------

func callError(message string) map[string]any {
	return map[string]any{"type": "call_error", "message": message}
}

// parseCallID mirrors int(data.get("call_id")) with its TypeError/ValueError guard.
func parseCallID(v any) (int, bool) {
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
