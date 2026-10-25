package webapp

import (
	"encoding/json"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"alexmessage/internal/auth"
	"alexmessage/internal/db"
	"alexmessage/internal/debuglog"
	"alexmessage/internal/ota"
	"alexmessage/internal/runtime"
)

// ---------- the call listener socket (/ws?mode=calls) ----------
//
// A phone keeps this second, small socket open while the app itself is
// closed, so an incoming call still rings it: the Android app's ring service
// holds it (phones without Google Play get no push). It carries only
// ringing — call_incoming when a call rings for its user, call_ended when
// that call stops — and takes call_decline, so the notification's Decline
// works without opening the app. Answering opens the app, whose /ws socket
// accepts. A listener counts toward reaching the callee, but not toward
// being online, and is never bound to a call. It rides the /ws path (a query
// parameter picks it) so a proxy's /ws rule covers it.
//
// The phone sleeps between packets, so the server sends the heartbeat: a
// ping every callListenerPing, which the phone answers with pong. A listener
// silent for callListenerTimeout is dropped. The ping also keeps a reverse
// proxy from closing the socket: nginx drops a proxied WebSocket after 60 s
// without data from the server (proxy_read_timeout's default), so the
// heartbeat stays well inside that.

var (
	callListenerPing    = 30 * time.Second
	callListenerTimeout = 150 * time.Second
	// callListenerCheck is how often a listener's session is re-checked, so a
	// logout elsewhere stops the ringing (close 4401).
	callListenerCheck = 5 * time.Second
)

// callListeners holds the listener sockets per user.
var callListeners = runtime.NewPresence()

func handleCallListener(c *gin.Context) {
	token := requestSessionToken(c)
	user := auth.ResolveSession(token, "user")
	clientInfo, hasClient := wsClientInfo(c)
	var policy *ota.Policy
	if hasClient {
		policy = currentPolicy()
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	// The hard gate (OTAUpdatePlan.md, D10): the ring service keeps running
	// and retries every 30 minutes, so a corrected policy heals it.
	if policy != nil && policy.HardGated(clientInfo) {
		_ = conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(4426, "update required"), time.Now().Add(time.Second))
		_ = conn.Close()
		return
	}
	if user == nil {
		_ = conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(4401, ""), time.Now().Add(time.Second))
		_ = conn.Close()
		return
	}
	defer conn.Close()

	// The tests shorten these; they swap them under callsMu.
	callsMu.Lock()
	ping, timeout, check := callListenerPing, callListenerTimeout, callListenerCheck
	callsMu.Unlock()

	client := &runtime.Client{Conn: conn, UserID: user.ID, SessionToken: token}
	conn.SetReadLimit(4 * 1024)
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	callListeners.Add(client)
	debuglog.Emit("messages", "info", "call_listener_connect", "Call listener connected", map[string]any{"user": user.Username})
	defer func() {
		callListeners.Remove(client)
		debuglog.Emit("messages", "info", "call_listener_disconnect", "Call listener disconnected", map[string]any{"user": user.Username})
	}()

	stopped := make(chan struct{})
	beatDone := make(chan struct{})
	defer func() { close(stopped); <-beatDone }()
	go func() {
		defer close(beatDone)
		beat := time.NewTicker(ping)
		defer beat.Stop()
		session := time.NewTicker(check)
		defer session.Stop()
		for {
			select {
			case <-stopped:
				return
			case <-beat.C:
				if client.Send(gin.H{"type": "ping"}) != nil {
					return
				}
			case <-session.C:
				if auth.ResolveSession(token, "user") == nil {
					_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(4401, "Session revoked"), time.Now().Add(time.Second))
					_ = conn.Close()
					return
				}
			}
		}
	}()

	deliverRingingCalls(client)

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(timeout))
		var data map[string]any
		if json.Unmarshal(raw, &data) != nil {
			continue
		}
		switch data["type"] {
		case "call_decline":
			declineRinging(user.ID, strField(data, "call_id"))
		case "ping":
			runtime.SendJSON(client, gin.H{"type": "pong"})
		}
	}
}

// declineRinging declines a call still ringing for userID. A stale Decline
// in a notification can't end a call another device already answered.
func declineRinging(userID int, callID string) {
	endCall(callID, func(c *liveCall) (string, bool) {
		return db.CallDeclined, c.calleeID == userID && !c.active
	})
}
