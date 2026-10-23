package webapp

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"alexmessage/internal/db"
	"alexmessage/internal/runtime"
)

// callPushes records the call pushes the engine sends, as "<callee id>
// <kind>: <notification text>". The text is worked out when the engine sends
// the push, as the real pushCall does.
var callPushes struct {
	sync.Mutex
	sent []string
}

func init() {
	pushCall = func(call *liveCall, kind string) {
		body, _ := callPushPayload(*call, kind)["body"].(string)
		callPushes.Lock()
		defer callPushes.Unlock()
		callPushes.sent = append(callPushes.sent, fmt.Sprintf("%d %s: %s", call.calleeID, kind, body))
	}
}

func takeCallPushes() []string {
	callPushes.Lock()
	defer callPushes.Unlock()
	out := callPushes.sent
	callPushes.sent = nil
	return out
}

// callPeer is one test device on /ws.
type callPeer struct {
	t  *testing.T
	ws *websocket.Conn
}

func (p *callPeer) send(v gin.H) {
	p.t.Helper()
	if err := p.ws.WriteJSON(v); err != nil {
		p.t.Fatal(err)
	}
}

// next skips frames until one of type kind arrives.
func (p *callPeer) next(kind string) map[string]any {
	p.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	skipped := []string{}
	for {
		_ = p.ws.SetReadDeadline(deadline)
		var m map[string]any
		if err := p.ws.ReadJSON(&m); err != nil {
			p.t.Fatalf("waiting for %s (skipped %v): %v", kind, skipped, err)
		}
		if m["type"] == kind {
			return m
		}
		skipped = append(skipped, fmt.Sprint(m["type"]))
	}
}

// sync returns once the server has handled every frame p sent before it.
func (p *callPeer) sync() {
	p.t.Helper()
	p.send(gin.H{"type": "ping"})
	p.next("pong")
}

// drop loses the connection the way a network does: no close frame.
func (p *callPeer) drop() { _ = p.ws.UnderlyingConn().Close() }

// unload closes the socket the way a closing or reloading page does.
func (p *callPeer) unload() {
	_ = p.ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseGoingAway, ""), time.Now().Add(time.Second))
	_ = p.ws.Close()
}

func callOf(m map[string]any) map[string]any {
	c, _ := m["call"].(map[string]any)
	return c
}

func payloadOf(m map[string]any) map[string]any {
	p, _ := m["payload"].(map[string]any)
	return p
}

// callFixture is an engine with three approved users.
type callFixture struct {
	base             string
	alice, bob, cal  *db.User
	tokA, tokB, tokC string
}

// newCallFixture starts a server; ring and grace override callRingTimeout and
// callResumeGrace when non-zero.
func newCallFixture(t *testing.T, ring, grace time.Duration) *callFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	// User ids restart in every fresh database, so a socket another test left
	// open would ring for this test's user with the same id.
	waitUntil(t, "other tests' sockets to close", func() bool {
		return len(runtime.PresenceTracker().AllSockets()) == 0 && len(callListeners.AllSockets()) == 0
	})
	initTranscribeTestDB(t)
	takeCallPushes()

	// The timeouts are read under callsMu, so they are swapped under it.
	callsMu.Lock()
	defaultRing, defaultGrace := callRingTimeout, callResumeGrace
	if ring != 0 {
		callRingTimeout = ring
	}
	if grace != 0 {
		callResumeGrace = grace
	}
	callsMu.Unlock()
	t.Cleanup(func() {
		callsMu.Lock()
		defer callsMu.Unlock()
		callRingTimeout, callResumeGrace = defaultRing, defaultGrace
		// Drop calls a test left behind so their timers can't write into
		// the next test's database.
		for _, c := range liveCalls {
			if c.ringTimer != nil {
				c.ringTimer.Stop()
			}
			for _, g := range c.grace {
				g.Stop()
			}
		}
		liveCalls = map[string]*liveCall{}
		userCalls = map[int]*liveCall{}
	})

	f := &callFixture{alice: reviewUser(t, "alice"), bob: reviewUser(t, "bob"), cal: reviewUser(t, "cal")}
	f.tokA, f.tokB, f.tokC = sessionFor(t, f.alice.ID), sessionFor(t, f.bob.ID), sessionFor(t, f.cal.ID)

	// httptest stops tracking a connection once it is hijacked, so the /ws
	// handlers are counted here: each must be gone (its disconnect logged)
	// before the next test swaps the database out from under it.
	var handlers sync.WaitGroup
	engine := NewEngine()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlers.Add(1)
		defer handlers.Done()
		engine.ServeHTTP(w, r)
	}))
	t.Cleanup(func() {
		server.Close()
		done := make(chan struct{})
		go func() { handlers.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("a /ws handler outlived its test")
		}
	})
	f.base = server.URL
	return f
}

// dial opens a device and waits for its init payload. Devices close before
// the server's cleanup runs (t.Cleanup is last in, first out).
func (f *callFixture) dial(t *testing.T, token string) *callPeer {
	t.Helper()
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(f.base, "http")+"/ws?token="+token, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	p := &callPeer{t: t, ws: ws}
	p.next("init")
	return p
}

// listen opens a call listener socket (a phone whose app is closed).
func (f *callFixture) listen(t *testing.T, token string) *callPeer {
	t.Helper()
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(f.base, "http")+"/ws?mode=calls&token="+token, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	return &callPeer{t: t, ws: ws}
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !cond(); {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// liveCallState reads a live call under callsMu.
func liveCallState[T any](id string, read func(*liveCall) T) (T, bool) {
	callsMu.Lock()
	defer callsMu.Unlock()
	var zero T
	c := liveCalls[id]
	if c == nil {
		return zero, false
	}
	return read(c), true
}

// ring places a video call from a to the user owning b's socket and returns
// the call id once a has heard it ring.
func ring(t *testing.T, a, b *callPeer, calleeID int) string {
	t.Helper()
	a.send(gin.H{"type": "call_start", "peer_id": calleeID, "media": "video", "client_call_id": "n1", "device": "dev-a"})
	out := a.next("call_outgoing")
	if out["client_call_id"] != "n1" || callOf(out)["state"] != "ringing" {
		t.Fatalf("call_outgoing = %v", out)
	}
	id, _ := callOf(out)["id"].(string)
	in := b.next("call_incoming")
	if callOf(in)["id"] != id || callOf(in)["media"] != "video" {
		t.Fatalf("call_incoming = %v", in)
	}
	if r := a.next("call_ringing"); r["call_id"] != id {
		t.Fatalf("call_ringing = %v", r)
	}
	return id
}

// answer accepts a ringing call from b and waits until both sides know.
func answer(t *testing.T, a, b *callPeer, id string) {
	t.Helper()
	b.send(gin.H{"type": "call_accept", "call_id": id, "device": "dev-b"})
	if acc := b.next("call_accepted"); callOf(acc)["state"] != "active" {
		t.Fatalf("callee call_accepted = %v", acc)
	}
	acc := a.next("call_accepted")
	if acc["client_call_id"] != "n1" || callOf(acc)["state"] != "active" || callOf(acc)["answered_at"] == nil {
		t.Fatalf("caller call_accepted = %v", acc)
	}
}

// expectEnd waits for a call's end and then the thread entry it writes — the
// engine's last act for a call — and returns the entry.
func expectEnd(t *testing.T, p *callPeer, id, reason string) map[string]any {
	t.Helper()
	if e := p.next("call_ended"); e["call_id"] != id || e["reason"] != reason {
		t.Fatalf("call_ended = %v, want %s for %s", e, reason, id)
	}
	msg, _ := p.next("message")["message"].(map[string]any)
	if msg["type"] != "call" || callOf(msg)["status"] != reason {
		t.Fatalf("call entry = %v, want %s", msg, reason)
	}
	return msg
}

// expectNotice waits for a call_ended sent to one device the call goes on
// without.
func expectNotice(t *testing.T, p *callPeer, id, reason string) {
	t.Helper()
	if e := p.next("call_ended"); e["call_id"] != id || e["reason"] != reason {
		t.Fatalf("call_ended = %v, want %s for %s", e, reason, id)
	}
}

// expectText checks an entry's body; a test call can straddle a second
// boundary, so durations are matched by prefix.
func expectText(t *testing.T, entry map[string]any, prefix string) {
	t.Helper()
	if body, _ := entry["text"].(string); !strings.HasPrefix(body, prefix) {
		t.Fatalf("entry text = %q, want %q…", body, prefix)
	}
}

func TestCallAnswerRelayAndHangup(t *testing.T) {
	f := newCallFixture(t, 0, 0)
	a := f.dial(t, f.tokA)
	b := f.dial(t, f.tokB)
	b2 := f.dial(t, f.tokB) // Bob's second device rings too
	id := ring(t, a, b, f.bob.ID)
	in := b2.next("call_incoming")
	if peer, _ := callOf(in)["peer"].(map[string]any); callOf(in)["id"] != id || peer["id"] != float64(f.alice.ID) {
		t.Fatalf("second device call_incoming = %v", in)
	}
	if ice, _ := callOf(in)["ice_servers"].([]any); len(ice) == 0 {
		t.Fatalf("call_incoming carries no ICE servers: %v", in)
	}

	answer(t, a, b, id)
	expectNotice(t, b2, id, "answered_elsewhere")

	// Signaling flows only between the device that placed the call and the
	// one that answered it.
	a.send(gin.H{"type": "call_signal", "call_id": id, "payload": gin.H{"kind": "offer", "sdp": "v=0"}})
	if s := b.next("call_signal"); payloadOf(s)["sdp"] != "v=0" {
		t.Fatalf("callee got %v", s)
	}
	b2.send(gin.H{"type": "call_signal", "call_id": id, "payload": gin.H{"kind": "answer", "sdp": "stale"}})
	b2.sync()
	b.send(gin.H{"type": "call_signal", "call_id": id, "payload": gin.H{"kind": "answer", "sdp": "v=1"}})
	if s := a.next("call_signal"); payloadOf(s)["sdp"] != "v=1" {
		t.Fatalf("caller got %v; the unbound device's signal leaked through", s)
	}

	a.send(gin.H{"type": "call_hangup", "call_id": id})
	entry := expectEnd(t, a, id, "completed")
	expectText(t, entry, "Video call · 0:0")
	if callOf(entry)["media"] != "video" || callOf(entry)["caller_id"] != float64(f.alice.ID) || entry["author"] == nil {
		t.Fatalf("call entry = %v", entry)
	}
	expectEnd(t, b, id, "completed")
	expectEnd(t, b2, id, "completed")

	// The thread keeps the entry, and an answered call is not unread.
	ch := db.DMChannelID(f.alice.ID, f.bob.ID)
	hist, err := db.FetchDMHistories(f.bob.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if got := hist[ch]; len(got) != 1 || got[0].Call == nil || got[0].Call.Status != db.CallCompleted || got[0].Call.Media != "video" {
		t.Fatalf("history = %+v", got)
	}
	sums, err := db.DMSummaries(f.bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sums[ch].Unread != 0 {
		t.Fatalf("answered call counted as unread: %d", sums[ch].Unread)
	}
	if pushes := takeCallPushes(); len(pushes) != 0 {
		t.Fatalf("pushes = %v, want none (Bob has no subscription)", pushes)
	}
}

func TestCallDeclinedAndCancelled(t *testing.T) {
	f := newCallFixture(t, 0, 0)
	a := f.dial(t, f.tokA)
	b := f.dial(t, f.tokB)
	ch := db.DMChannelID(f.alice.ID, f.bob.ID)

	id := ring(t, a, b, f.bob.ID)
	b.send(gin.H{"type": "call_hangup", "call_id": id})
	expectText(t, expectEnd(t, a, id, "declined"), "Declined video call")
	expectEnd(t, b, id, "declined")

	id = ring(t, a, b, f.bob.ID)
	a.send(gin.H{"type": "call_hangup", "call_id": id})
	expectEnd(t, a, id, "cancelled")
	expectText(t, expectEnd(t, b, id, "cancelled"), "Missed video call")

	// Only the call Bob missed is unread for him; Alice's own calls never are.
	sums, _ := db.DMSummaries(f.bob.ID)
	if sums[ch].Unread != 1 {
		t.Fatalf("bob unread = %d, want 1", sums[ch].Unread)
	}
	sums, _ = db.DMSummaries(f.alice.ID)
	if sums[ch].Unread != 0 {
		t.Fatalf("alice unread = %d, want 0", sums[ch].Unread)
	}
	if n, _ := db.CountUnread(f.bob.ID, ch, 0, 0); n != 1 {
		t.Fatalf("CountUnread = %d, want 1", n)
	}
}

func TestCallFailedToConnect(t *testing.T) {
	f := newCallFixture(t, 0, 0)
	a := f.dial(t, f.tokA)
	b := f.dial(t, f.tokB)
	id := ring(t, a, b, f.bob.ID)
	answer(t, a, b, id)
	// The media never connected, so Bob's client gives up.
	b.send(gin.H{"type": "call_hangup", "call_id": id, "reason": "failed"})
	expectText(t, expectEnd(t, a, id, "failed"), "Video call failed")
	expectEnd(t, b, id, "failed")
	// Bob answered it: a failed call isn't a missed one.
	sums, _ := db.DMSummaries(f.bob.ID)
	if n := sums[db.DMChannelID(f.alice.ID, f.bob.ID)].Unread; n != 0 {
		t.Fatalf("bob unread = %d, want 0", n)
	}
}

func TestCallNoAnswer(t *testing.T) {
	f := newCallFixture(t, 300*time.Millisecond, 0)
	a := f.dial(t, f.tokA)
	b := f.dial(t, f.tokB)
	id := ring(t, a, b, f.bob.ID)
	expectEnd(t, a, id, "no_answer")
	expectText(t, expectEnd(t, b, id, "no_answer"), "Missed video call")
}

func TestCallUnavailableBusyInvalid(t *testing.T) {
	f := newCallFixture(t, 0, 0)
	a := f.dial(t, f.tokA)

	// Cal has no open device and no push subscription: nothing can ring.
	a.send(gin.H{"type": "call_start", "peer_id": f.cal.ID, "media": "audio", "client_call_id": "u1", "device": "dev-a"})
	out := a.next("call_outgoing")
	if callOf(out)["media"] != "audio" {
		t.Fatalf("call_outgoing = %v", out)
	}
	id, _ := callOf(out)["id"].(string)
	expectText(t, expectEnd(t, a, id, "unavailable"), "Missed voice call")

	for _, peer := range []any{f.alice.ID, 9999, "nope"} {
		a.send(gin.H{"type": "call_start", "peer_id": peer, "client_call_id": "x"})
		if e := a.next("call_error"); e["error"] != "invalid" || e["client_call_id"] != "x" {
			t.Fatalf("call to %v: %v", peer, e)
		}
	}

	// While Bob rings for Alice, Cal's call to him ends busy, Alice can't
	// start a second call, and Alice's call is untouched.
	b := f.dial(t, f.tokB)
	c := f.dial(t, f.tokC)
	id = ring(t, a, b, f.bob.ID)
	c.send(gin.H{"type": "call_start", "peer_id": f.bob.ID, "client_call_id": "c1", "device": "dev-c"})
	busy, _ := callOf(c.next("call_outgoing"))["id"].(string)
	expectText(t, expectEnd(t, c, busy, "busy"), "Missed video call")
	a.send(gin.H{"type": "call_start", "peer_id": f.cal.ID, "client_call_id": "n2"})
	if e := a.next("call_error"); e["error"] != "busy_self" || e["client_call_id"] != "n2" {
		t.Fatalf("second call: %v", e)
	}
	answer(t, a, b, id)
	b.send(gin.H{"type": "call_hangup", "call_id": id})
	expectEnd(t, a, id, "completed")
}

func TestCallGlare(t *testing.T) {
	f := newCallFixture(t, 0, 0)
	a := f.dial(t, f.tokA)
	b := f.dial(t, f.tokB)
	id := ring(t, a, b, f.bob.ID)

	// Bob dials Alice before his client showed her call: the two connect on
	// Alice's call, with Bob's device as the bound callee.
	b.send(gin.H{"type": "call_start", "peer_id": f.alice.ID, "media": "audio", "client_call_id": "n2", "device": "dev-b"})
	acc := b.next("call_accepted")
	if acc["client_call_id"] != "n2" || callOf(acc)["id"] != id || callOf(acc)["caller_id"] != float64(f.alice.ID) {
		t.Fatalf("callee call_accepted = %v", acc)
	}
	if acc := a.next("call_accepted"); acc["client_call_id"] != "n1" || callOf(acc)["id"] != id {
		t.Fatalf("caller call_accepted = %v", acc)
	}
	a.send(gin.H{"type": "call_signal", "call_id": id, "payload": gin.H{"kind": "offer"}})
	b.next("call_signal")
	a.send(gin.H{"type": "call_hangup", "call_id": id})
	expectEnd(t, b, id, "completed")
}

// callFramesUntilPong pings p and returns the call frames it receives before
// the pong ("<type>", or "<type> <reason>" for call_ended): everything sent
// to p by work that finished before the ping was sent.
func callFramesUntilPong(p *callPeer) []string {
	p.t.Helper()
	p.send(gin.H{"type": "ping"})
	frames := []string{}
	for {
		_ = p.ws.SetReadDeadline(time.Now().Add(5 * time.Second))
		var m map[string]any
		if err := p.ws.ReadJSON(&m); err != nil {
			p.t.Fatalf("waiting for pong (got %v): %v", frames, err)
		}
		kind, _ := m["type"].(string)
		switch {
		case kind == "pong":
			return frames
		case kind == "call_ended":
			frames = append(frames, fmt.Sprintf("%s %v", kind, m["reason"]))
		case strings.HasPrefix(kind, "call_"):
			frames = append(frames, kind)
		}
	}
}

// Bob's own call to Alice crosses hers while the server is still ringing his
// devices for it (TestCallGlare covers the crossing after the ring). The
// answer must leave none of Bob's other devices ringing, and Alice must not
// hear a ring for a call already answered.
func TestCallGlareWhileRinging(t *testing.T) {
	cases := []struct {
		point string
		// rung is what Bob's other devices hear after the answer.
		rung, bound []string
	}{
		// Before the ring: nothing more reaches them.
		{"looked_up", []string{}, []string{}},
		// While it rings: the ring is followed by its end.
		{"ringing", []string{"call_incoming", "call_ended answered_elsewhere"}, []string{"call_incoming"}},
	}
	for _, tc := range cases {
		t.Run(tc.point, func(t *testing.T) {
			f := newCallFixture(t, 0, 0)
			a := f.dial(t, f.tokA)
			b := f.dial(t, f.tokB)
			b2 := f.dial(t, f.tokB)
			l := f.listen(t, f.tokB)
			l.sync()

			// Alice's call_start waits at tc.point until resumed; a failed
			// test resumes it too, so its /ws handler can finish.
			paused, resume := make(chan struct{}, 1), make(chan struct{})
			release := sync.OnceFunc(func() { close(resume) })
			callsMu.Lock()
			callStartHook = func(point string) {
				if point == tc.point {
					paused <- struct{}{}
					<-resume
				}
			}
			callsMu.Unlock()
			t.Cleanup(func() {
				release()
				callsMu.Lock()
				defer callsMu.Unlock()
				callStartHook = nil
			})

			a.send(gin.H{"type": "call_start", "peer_id": f.bob.ID, "media": "video", "client_call_id": "n1", "device": "dev-a"})
			id, _ := callOf(a.next("call_outgoing"))["id"].(string)
			select {
			case <-paused:
			case <-time.After(5 * time.Second):
				t.Fatalf("Alice's call_start never reached %s", tc.point)
			}
			b.send(gin.H{"type": "call_start", "peer_id": f.alice.ID, "media": "audio", "client_call_id": "n2", "device": "dev-b"})
			if acc := b.next("call_accepted"); callOf(acc)["id"] != id {
				t.Fatalf("callee call_accepted = %v", acc)
			}
			expectNotice(t, b2, id, "answered_elsewhere")
			expectNotice(t, l, id, "answered_elsewhere")
			release()

			// Alice's read loop handles the ping only once her call_start is
			// done, so these hold everything it sent.
			if got := callFramesUntilPong(a); !slices.Equal(got, []string{"call_accepted"}) {
				t.Fatalf("caller frames = %v, want only call_accepted", got)
			}
			for name, p := range map[string]*callPeer{"other device": b2, "listener": l} {
				if got := callFramesUntilPong(p); !slices.Equal(got, tc.rung) {
					t.Fatalf("%s frames = %v, want %v", name, got, tc.rung)
				}
			}
			if got := callFramesUntilPong(b); !slices.Equal(got, tc.bound) {
				t.Fatalf("answering device frames = %v, want %v", got, tc.bound)
			}

			a.send(gin.H{"type": "call_hangup", "call_id": id})
			expectEnd(t, b, id, "completed")
		})
	}
}

func TestCallResumeAfterDrop(t *testing.T) {
	f := newCallFixture(t, 0, 0)
	a := f.dial(t, f.tokA)
	b := f.dial(t, f.tokB)
	id := ring(t, a, b, f.bob.ID)
	answer(t, a, b, id)

	// Alice's network drops. Bob's signals meanwhile wait for her.
	a.drop()
	waitUntil(t, "the server to notice the drop", func() bool {
		gone, _ := liveCallState(id, func(c *liveCall) bool { return c.callerConn == nil })
		return gone
	})
	for n := 1; n <= 2; n++ {
		b.send(gin.H{"type": "call_signal", "call_id": id, "payload": gin.H{"kind": "candidate", "n": n}})
	}
	waitUntil(t, "the signals to be held", func() bool {
		held, _ := liveCallState(id, func(c *liveCall) int { return len(c.backlog[f.alice.ID]) })
		return held == 2
	})

	// A tab of Alice's that isn't the device on the call can't take it over.
	stale := f.dial(t, f.tokA)
	stale.send(gin.H{"type": "call_resume", "call_id": id, "device": "dev-other"})
	expectNotice(t, stale, id, "gone")

	a2 := f.dial(t, f.tokA)
	a2.send(gin.H{"type": "call_resume", "call_id": id, "device": "dev-a"})
	if r := a2.next("call_resumed"); callOf(r)["state"] != "active" || callOf(r)["id"] != id {
		t.Fatalf("call_resumed = %v", r)
	}
	for n := 1; n <= 2; n++ {
		if s := a2.next("call_signal"); payloadOf(s)["n"] != float64(n) {
			t.Fatalf("held signal %d = %v", n, s)
		}
	}
	a2.send(gin.H{"type": "call_signal", "call_id": id, "payload": gin.H{"kind": "offer", "ice_restart": true}})
	if s := b.next("call_signal"); payloadOf(s)["ice_restart"] != true {
		t.Fatalf("callee got %v", s)
	}

	// Bob's side of the call resumes only from the device that answered.
	b2 := f.dial(t, f.tokB)
	b2.send(gin.H{"type": "call_resume", "call_id": id, "device": "dev-b2"})
	expectNotice(t, b2, id, "answered_elsewhere")

	a2.send(gin.H{"type": "call_hangup", "call_id": id})
	expectEnd(t, b, id, "completed")
}

func TestCallGraceExpiry(t *testing.T) {
	f := newCallFixture(t, 0, 300*time.Millisecond)
	a := f.dial(t, f.tokA)
	b := f.dial(t, f.tokB)
	id := ring(t, a, b, f.bob.ID)
	answer(t, a, b, id)
	b.drop() // and never comes back
	expectText(t, expectEnd(t, a, id, "completed"), "Video call · 0:0")

	// A caller that vanishes while ringing cancels the call.
	b = f.dial(t, f.tokB)
	id = ring(t, a, b, f.bob.ID)
	a.drop()
	expectEnd(t, b, id, "cancelled")
}

func TestCallEndsWhenPageUnloads(t *testing.T) {
	// The default 30 s grace: an unload must not wait for it.
	f := newCallFixture(t, 0, 0)
	a := f.dial(t, f.tokA)
	b := f.dial(t, f.tokB)
	id := ring(t, a, b, f.bob.ID)
	answer(t, a, b, id)
	b.unload()
	expectEnd(t, a, id, "completed")

	b = f.dial(t, f.tokB)
	id = ring(t, a, b, f.bob.ID)
	a.unload()
	expectEnd(t, b, id, "cancelled")
}

// deleteAccount deletes token's account through the API, as its owner does.
func (f *callFixture) deleteAccount(t *testing.T, token string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, f.base+"/api/me/delete", strings.NewReader(`{"password":"password123"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("delete account: %s", res.Status)
	}
}

// Deleting your account ends your call first, as hanging up would: the peer
// hears at once, and the call is logged while both accounts exist.
func TestCallEndsWhenAccountDeleted(t *testing.T) {
	f := newCallFixture(t, 0, 0)
	a := f.dial(t, f.tokA)
	b := f.dial(t, f.tokB)
	c := f.dial(t, f.tokC)

	// On the call.
	id := ring(t, a, b, f.bob.ID)
	answer(t, a, b, id)
	f.deleteAccount(t, f.tokB)
	entry := expectEnd(t, a, id, "completed")
	if callOf(entry)["callee_id"] != float64(f.bob.ID) || entry["author"] == nil {
		t.Fatalf("call entry = %v", entry)
	}

	// Ringing for it: declined.
	id = ring(t, a, c, f.cal.ID)
	f.deleteAccount(t, f.tokC)
	expectText(t, expectEnd(t, a, id, "declined"), "Declined video call")

	// Ringing out: cancelled.
	dan := reviewUser(t, "dan")
	tokD := sessionFor(t, dan.ID)
	d := f.dial(t, tokD)
	id = ring(t, d, a, f.alice.ID)
	f.deleteAccount(t, tokD)
	expectText(t, expectEnd(t, a, id, "cancelled"), "Missed video call")
}

// Logging out drops every session of the user, so their call ends at once,
// as hanging up would, instead of waiting out the resume grace.
func TestCallEndsOnLogout(t *testing.T) {
	f := newCallFixture(t, 0, 0)
	a := f.dial(t, f.tokA)
	b := f.dial(t, f.tokB)

	id := ring(t, a, b, f.bob.ID)
	answer(t, a, b, id)
	req, err := http.NewRequest(http.MethodPost, f.base+"/api/logout", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+f.tokB)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("logout: %s", res.Status)
	}
	expectEnd(t, a, id, "completed")
}

// The admin panel (another process) can delete a user mid-call without
// ending the call. The call is still logged when it ends, with no id for
// the deleted user.
func TestCallLoggedAfterAdminDeletesParticipant(t *testing.T) {
	f := newCallFixture(t, 0, 0)
	a := f.dial(t, f.tokA)
	b := f.dial(t, f.tokB)
	c := f.dial(t, f.tokC)

	id := ring(t, a, b, f.bob.ID)
	answer(t, a, b, id)
	if err := db.DeleteUser(f.bob.ID); err != nil {
		t.Fatal(err)
	}
	a.send(gin.H{"type": "call_hangup", "call_id": id})
	entry := expectEnd(t, a, id, "completed")
	if call := callOf(entry); call["caller_id"] != float64(f.alice.ID) || call["callee_id"] != nil || entry["user_id"] != float64(f.alice.ID) {
		t.Fatalf("callee deleted: call entry = %v", entry)
	}

	// The caller authors the entry; without them it has no author.
	id = ring(t, a, c, f.cal.ID)
	answer(t, a, c, id)
	if err := db.DeleteUser(f.alice.ID); err != nil {
		t.Fatal(err)
	}
	c.send(gin.H{"type": "call_hangup", "call_id": id})
	entry = expectEnd(t, c, id, "completed")
	if call := callOf(entry); call["caller_id"] != nil || call["callee_id"] != float64(f.cal.ID) || entry["user_id"] != nil || entry["author"] != nil {
		t.Fatalf("caller deleted: call entry = %v", entry)
	}
}

func TestCallRingsDeviceThatConnectsLate(t *testing.T) {
	f := newCallFixture(t, 0, 0)
	// Bob has no open device but a Web Push subscription, so the call rings
	// through the push.
	if err := db.UpsertPushSubscription(f.bob.ID, "https://push.invalid/sub", "key", "auth", "test"); err != nil {
		t.Fatal(err)
	}
	a := f.dial(t, f.tokA)
	a.send(gin.H{"type": "call_start", "peer_id": f.bob.ID, "client_call_id": "n1", "device": "dev-a"})
	id, _ := callOf(a.next("call_outgoing"))["id"].(string)
	a.sync()
	if reached, _ := liveCallState(id, func(c *liveCall) bool { return c.reached }); reached {
		t.Fatal("no device of Bob's was open, yet the call reached one")
	}
	expectPushes(t, f.bob.ID, "call: Incoming video call")

	// Bob opens the app from the notification: his new device rings, and
	// Alice hears it ring.
	b := f.dial(t, f.tokB)
	if in := b.next("call_incoming"); callOf(in)["id"] != id {
		t.Fatalf("call_incoming = %v", in)
	}
	if r := a.next("call_ringing"); r["call_id"] != id {
		t.Fatalf("call_ringing = %v", r)
	}
	// Answering withdraws the ringing notification, and only that.
	answer(t, a, b, id)
	a.send(gin.H{"type": "call_hangup", "call_id": id})
	expectEnd(t, b, id, "completed")
	expectPushes(t, f.bob.ID, "call_over: Answered video call")
}

// expectPushes checks the call pushes sent to calleeID since the last check,
// each given as "<kind>: <notification text>".
func expectPushes(t *testing.T, calleeID int, pushes ...string) {
	t.Helper()
	expected := []string{}
	for _, p := range pushes {
		expected = append(expected, fmt.Sprintf("%d %s", calleeID, p))
	}
	if got := takeCallPushes(); !slices.Equal(got, expected) {
		t.Fatalf("pushes = %v, want %v", got, expected)
	}
}

func TestCallPushesOnlyWithoutVisibleDevice(t *testing.T) {
	f := newCallFixture(t, 0, 0)
	if err := db.UpsertPushSubscription(f.bob.ID, "https://push.invalid/sub", "key", "auth", "test"); err != nil {
		t.Fatal(err)
	}
	a := f.dial(t, f.tokA)
	b := f.dial(t, f.tokB)

	// Bob's only device is in the background: it rings, and so does the push;
	// the missed call follows as a push too.
	b.send(gin.H{"type": "visibility", "visible": false})
	b.sync()
	id := ring(t, a, b, f.bob.ID)
	expectPushes(t, f.bob.ID, "call: Incoming video call")
	a.send(gin.H{"type": "call_hangup", "call_id": id})
	expectEnd(t, b, id, "cancelled")
	expectPushes(t, f.bob.ID, "missed_call: Missed video call")

	// With his screen on the app, the device alone rings.
	b.send(gin.H{"type": "visibility", "visible": true})
	b.sync()
	id = ring(t, a, b, f.bob.ID)
	a.send(gin.H{"type": "call_hangup", "call_id": id})
	expectEnd(t, b, id, "cancelled")
	expectPushes(t, f.bob.ID)
}

// A ringing push stays on screen until another push with its tag replaces
// it, so every pushed call that stops ringing sends exactly one more.
func TestCallPushWithdrawnWhenRingingStops(t *testing.T) {
	f := newCallFixture(t, 0, 0)
	if err := db.UpsertPushSubscription(f.bob.ID, "https://push.invalid/sub", "key", "auth", "test"); err != nil {
		t.Fatal(err)
	}
	a := f.dial(t, f.tokA)
	b := f.dial(t, f.tokB)
	b.send(gin.H{"type": "visibility", "visible": false})
	b.sync()

	// Answered: the answer withdraws it, and the hangup sends nothing more.
	id := ring(t, a, b, f.bob.ID)
	expectPushes(t, f.bob.ID, "call: Incoming video call")
	answer(t, a, b, id)
	expectPushes(t, f.bob.ID, "call_over: Answered video call")
	a.send(gin.H{"type": "call_hangup", "call_id": id})
	expectEnd(t, b, id, "completed")
	expectPushes(t, f.bob.ID)

	// Declined.
	id = ring(t, a, b, f.bob.ID)
	expectPushes(t, f.bob.ID, "call: Incoming video call")
	b.send(gin.H{"type": "call_hangup", "call_id": id})
	expectEnd(t, a, id, "declined")
	expectPushes(t, f.bob.ID, "call_over: Declined video call")

	// Missed: the missed-call notice replaces it instead.
	id = ring(t, a, b, f.bob.ID)
	expectPushes(t, f.bob.ID, "call: Incoming video call")
	a.send(gin.H{"type": "call_hangup", "call_id": id})
	expectEnd(t, b, id, "cancelled")
	expectPushes(t, f.bob.ID, "missed_call: Missed video call")
}

// Every push of a call carries the call's tag, so each one replaces the
// last; only the ringing one stays on screen until it is dealt with.
func TestCallPushPayload(t *testing.T) {
	call := liveCall{
		id: "c1", channel: "dm:1:2", media: "audio",
		callerID: 1, calleeID: 2,
		caller: runtime.PublicUser{ID: 1, Username: "alice"},
	}
	video := call
	video.media = "video"
	video.caller.DisplayName = "Alice"
	answered := video
	answered.active = true
	for _, tc := range []struct {
		call               liveCall
		kind, title, body  string
		requireInteraction bool
	}{
		{call, "call", "alice", "Incoming voice call", true},
		{video, "call", "Alice", "Incoming video call", true},
		{call, "missed_call", "alice", "Missed voice call", false},
		{answered, "call_over", "Alice", "Answered video call", false},
		{video, "call_over", "Alice", "Declined video call", false},
	} {
		p := callPushPayload(tc.call, tc.kind)
		if p["kind"] != tc.kind || p["title"] != tc.title || p["body"] != tc.body ||
			p["require_interaction"] != tc.requireInteraction ||
			p["tag"] != "call-c1" || p["call_id"] != "c1" || p["channel"] != "dm:1:2" {
			t.Errorf("%s (%s, active %v) = %v", tc.kind, tc.call.media, tc.call.active, p)
		}
	}
}

func TestTURNRESTCredential(t *testing.T) {
	user, pass := turnRESTCredential("north-secret", 42, time.Unix(1700000000, 0))
	if user != "1700000000:42" || pass != "1azYcwkgPAnmyb6GOHbBMj8gPoQ=" {
		t.Fatalf("credential = %q / %q", user, pass)
	}
}

func TestICEServersFromEnv(t *testing.T) {
	for _, k := range []string{"STUN_URLS", "TURN_URLS", "TURN_SECRET", "TURN_USERNAME", "TURN_CREDENTIAL"} {
		t.Setenv(k, "")
	}
	if got := iceServersFor(7); len(got) != 1 || got[0].URLs[0] != "stun:stun.l.google.com:19302" || got[0].Username != "" {
		t.Fatalf("default = %+v", got)
	}

	t.Setenv("STUN_URLS", "stun:a.example:3478, stun:b.example:3478")
	t.Setenv("TURN_URLS", "turn:relay.example:3478?transport=udp,turns:relay.example:5349")
	t.Setenv("TURN_SECRET", "s3")
	got := iceServersFor(7)
	if len(got) != 2 || len(got[0].URLs) != 2 || got[0].URLs[1] != "stun:b.example:3478" {
		t.Fatalf("stun = %+v", got)
	}
	if got[1].URLs[1] != "turns:relay.example:5349" || !strings.HasSuffix(got[1].Username, ":7") || got[1].Credential == "" {
		t.Fatalf("turn = %+v", got[1])
	}

	t.Setenv("TURN_SECRET", "")
	t.Setenv("TURN_USERNAME", "u")
	t.Setenv("TURN_CREDENTIAL", "p")
	if got := iceServersFor(7); got[1].Username != "u" || got[1].Credential != "p" {
		t.Fatalf("static turn = %+v", got[1])
	}
}

func TestCallSummaryText(t *testing.T) {
	cases := []struct {
		media, status string
		duration      int64
		want          string
	}{
		{"video", db.CallCompleted, 272, "Video call · 4:32"},
		{"audio", db.CallCompleted, 3725, "Voice call · 1:02:05"},
		{"audio", db.CallDeclined, 0, "Declined voice call"},
		{"video", db.CallFailed, 3, "Video call failed"},
		{"video", db.CallNoAnswer, 0, "Missed video call"},
		{"video", db.CallBusy, 0, "Missed video call"},
	}
	for _, c := range cases {
		if got := db.CallSummaryText(c.media, c.status, c.duration); got != c.want {
			t.Errorf("CallSummaryText(%s, %s, %d) = %q, want %q", c.media, c.status, c.duration, got, c.want)
		}
	}
}
