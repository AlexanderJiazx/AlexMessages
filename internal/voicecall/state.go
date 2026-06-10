// Package voicecall is the AlexMessage WebRTC signaling server (mirrors
// voicecall.py). It runs on its own port, shares the account database, and
// relays opaque SDP/ICE between two participants while policing the
// "one active call per user" invariant.
//
// Concurrency model: the original kept all call state under a single asyncio
// lock and `await`-ed sends while holding it. We reproduce that with callMu —
// a single mutex guarding `calls`, `userToCall`, and per-call fields — and
// hold it across the sends in each handler, exactly as the Python did. The one
// exception is signaling relay, which sends after releasing the lock. Because
// Go has true concurrency (unlike the single-threaded event loop), every
// socket additionally carries its own write mutex so concurrent fan-outs never
// interleave a frame on the same connection.
package voicecall

import (
	"sync"

	"github.com/gorilla/websocket"

	"alexmessage/internal/db"
)

// conn wraps one voice-call WebSocket with a write mutex.
type conn struct {
	ws *websocket.Conn
	mu sync.Mutex
}

// send is a best-effort write. Returns false if the socket appears dead.
func (c *conn) send(payload any) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ws.WriteJSON(payload) == nil
}

// Call is a live (ringing or accepted) call. Once it resolves it is removed
// from the registry and persisted into the calls table via finalizeCall.
type Call struct {
	id         int
	callerID   int
	calleeID   int
	callerWS   *conn
	calleeWS   *conn
	state      string // "ringing" | "accepted"
	startedAt  int64
	answeredAt *int64
}

// presence tracks all open voice-call sockets, indexed by user. It guards its
// own state with prMu, independent of callMu.
type presenceTracker struct {
	prMu    sync.Mutex
	sockets map[*conn]int
	byUser  map[int]map[*conn]struct{}
}

func newPresence() *presenceTracker {
	return &presenceTracker{
		sockets: map[*conn]int{},
		byUser:  map[int]map[*conn]struct{}{},
	}
}

func (p *presenceTracker) add(c *conn, userID int) {
	p.prMu.Lock()
	defer p.prMu.Unlock()
	p.sockets[c] = userID
	if p.byUser[userID] == nil {
		p.byUser[userID] = map[*conn]struct{}{}
	}
	p.byUser[userID][c] = struct{}{}
}

func (p *presenceTracker) remove(c *conn) (int, bool) {
	p.prMu.Lock()
	defer p.prMu.Unlock()
	uid, ok := p.sockets[c]
	if !ok {
		return 0, false
	}
	delete(p.sockets, c)
	if bucket := p.byUser[uid]; bucket != nil {
		delete(bucket, c)
		if len(bucket) == 0 {
			delete(p.byUser, uid)
		}
	}
	return uid, true
}

func (p *presenceTracker) socketsFor(userID int) []*conn {
	p.prMu.Lock()
	defer p.prMu.Unlock()
	var out []*conn
	for c := range p.byUser[userID] {
		out = append(out, c)
	}
	return out
}

func (p *presenceTracker) isOnline(userID int) bool {
	p.prMu.Lock()
	defer p.prMu.Unlock()
	return len(p.byUser[userID]) > 0
}

// ---------- process-wide state ----------

var (
	presence   = newPresence()
	calls      = map[int]*Call{}
	userToCall = map[int]int{}
	callSeq    int
	callMu     sync.Mutex // guards calls, userToCall, callSeq, and Call fields
)

// allocCallID returns the next call id. Caller MUST hold callMu.
func allocCallID() int {
	callSeq++
	return callSeq
}

// ---------- send helpers ----------

// sendToUser fans out to every open socket of a user, dropping any that fail.
func sendToUser(userID int, payload any, exclude *conn) {
	var dead []*conn
	for _, c := range presence.socketsFor(userID) {
		if c == exclude {
			continue
		}
		if !c.send(payload) {
			dead = append(dead, c)
		}
	}
	for _, c := range dead {
		presence.remove(c)
	}
}

// persistCall writes a finalized call into the calls table. Never panics.
func persistCall(call *Call, status string) {
	caller := call.callerID
	callee := call.calleeID
	_, _ = db.InsertCall(&caller, &callee, call.startedAt, call.answeredAt, db.NowTS(), status)
}

// pushRecentCalls tells a user's open tabs that their recent-calls list changed.
func pushRecentCalls(userID int) {
	if !presence.isOnline(userID) {
		return
	}
	sendToUser(userID, map[string]any{"type": "recent_changed"}, nil)
}

// finalizeCall removes a call from the registry, persists it, and notifies both
// sides. Caller MUST hold callMu.
func finalizeCall(call *Call, status, reason string) {
	if _, ok := calls[call.id]; !ok {
		return // already finalized
	}
	delete(calls, call.id)
	if userToCall[call.callerID] == call.id {
		delete(userToCall, call.callerID)
	}
	if userToCall[call.calleeID] == call.id {
		delete(userToCall, call.calleeID)
	}

	persistCall(call, status)

	if reason == "" {
		reason = status
	}
	endPayload := map[string]any{
		"type":    "call_ended",
		"call_id": call.id,
		"reason":  reason,
	}
	sendToUser(call.callerID, endPayload, nil)
	sendToUser(call.calleeID, endPayload, nil)
	pushRecentCalls(call.callerID)
	pushRecentCalls(call.calleeID)
}
