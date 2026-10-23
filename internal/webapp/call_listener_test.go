package webapp

import (
	"slices"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"alexmessage/internal/db"
	"alexmessage/internal/runtime"
)

func TestCallListenerRingsClosedApp(t *testing.T) {
	f := newCallFixture(t, 0, 0)
	a := f.dial(t, f.tokA)
	// Bob's phone: the app is closed, its ring service listens.
	l := f.listen(t, f.tokB)
	l.sync()
	if slices.Contains(runtime.PresenceTracker().OnlineUserIDs(), f.bob.ID) {
		t.Fatal("a call listener put Bob online")
	}

	// The listener reaches Bob: the call rings instead of ending unavailable,
	// and Alice hears it ring.
	id := ring(t, a, l, f.bob.ID)
	l.send(gin.H{"type": "call_decline", "call_id": id})
	expectText(t, expectEnd(t, a, id, "declined"), "Declined video call")
	expectNotice(t, l, id, "declined")
}

func TestCallListenerStopsWhenAnswered(t *testing.T) {
	f := newCallFixture(t, 0, 0)
	a := f.dial(t, f.tokA)
	b := f.dial(t, f.tokB)
	l := f.listen(t, f.tokB)
	l.sync()
	id := ring(t, a, b, f.bob.ID)
	if in := l.next("call_incoming"); callOf(in)["id"] != id {
		t.Fatalf("listener call_incoming = %v", in)
	}

	answer(t, a, b, id)
	expectNotice(t, l, id, "answered_elsewhere")
	// A stale Decline in the notification can't end the answered call.
	l.send(gin.H{"type": "call_decline", "call_id": id})
	l.sync()
	if active, ok := liveCallState(id, func(c *liveCall) bool { return c.active }); !ok || !active {
		t.Fatal("a stale decline ended the answered call")
	}

	a.send(gin.H{"type": "call_hangup", "call_id": id})
	expectEnd(t, b, id, "completed")
	expectNotice(t, l, id, "completed")
}

func TestCallListenerConnectsWhileRinging(t *testing.T) {
	f := newCallFixture(t, 0, 0)
	// A Web Push subscription keeps the call ringing while Bob has no socket.
	if err := db.UpsertPushSubscription(f.bob.ID, "https://push.invalid/sub", "key", "auth", "test"); err != nil {
		t.Fatal(err)
	}
	a := f.dial(t, f.tokA)
	a.send(gin.H{"type": "call_start", "peer_id": f.bob.ID, "client_call_id": "n1", "device": "dev-a"})
	id, _ := callOf(a.next("call_outgoing"))["id"].(string)
	a.sync()

	// Bob's ring service (re)connects while the call rings.
	l := f.listen(t, f.tokB)
	if in := l.next("call_incoming"); callOf(in)["id"] != id {
		t.Fatalf("listener call_incoming = %v", in)
	}
	if r := a.next("call_ringing"); r["call_id"] != id {
		t.Fatalf("call_ringing = %v", r)
	}
	a.send(gin.H{"type": "call_hangup", "call_id": id})
	expectEnd(t, a, id, "cancelled")
	expectNotice(t, l, id, "cancelled")
}

func TestCallListenerHeartbeatAndAuth(t *testing.T) {
	f := newCallFixture(t, 0, 0)
	callsMu.Lock()
	defaultPing, defaultCheck := callListenerPing, callListenerCheck
	callListenerPing, callListenerCheck = 50*time.Millisecond, 50*time.Millisecond
	callsMu.Unlock()
	t.Cleanup(func() {
		callsMu.Lock()
		defer callsMu.Unlock()
		callListenerPing, callListenerCheck = defaultPing, defaultCheck
	})

	// The server keeps a sleeping phone's connection alive.
	l := f.listen(t, f.tokB)
	l.next("ping")
	l.send(gin.H{"type": "pong"})

	expectClose4401 := func(p *callPeer) {
		t.Helper()
		_ = p.ws.SetReadDeadline(time.Now().Add(5 * time.Second))
		for {
			_, _, err := p.ws.ReadMessage()
			if err == nil {
				continue
			}
			if !websocket.IsCloseError(err, 4401) {
				t.Fatalf("closed with %v, want 4401", err)
			}
			return
		}
	}
	expectClose4401(f.listen(t, "not-a-session"))

	// Logging out everywhere stops the ringing too.
	if err := db.DeleteUserSessions(f.bob.ID); err != nil {
		t.Fatal(err)
	}
	expectClose4401(l)
}
