package meet

import (
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"alexmessage/internal/auth"
	"alexmessage/internal/db"
)

func TestSafeNext(t *testing.T) {
	cases := map[string]string{
		"":                       "/",
		"/":                      "/",
		"/m/abc-defg-hjk":        "/m/abc-defg-hjk",
		"/m/abc?x=1#y":           "/m/abc?x=1#y",
		"//evil.example":         "/",
		`/\evil.example`:         "/",
		`/\/evil.example`:        "/",
		`\/evil.example`:         "/",
		"/\t/evil.example":       "/",
		"/\n/evil.example":       "/",
		"/\r/evil.example":       "/",
		"/\x00x":                 "/",
		"/\x7fx":                 "/",
		"https://evil.example/":  "/",
		"evil.example":           "/",
		"javascript:alert(1)":    "/",
		"/%5Cevil.example":       "/%5Cevil.example", // stays a path: browsers don't decode %5C
		"/%2F%2Fevil.example":    "/%2F%2Fevil.example",
		"/m/%zz":                 "/", // does not parse
		"/ok/path/with:colon/ok": "/ok/path/with:colon/ok",
	}
	for in, want := range cases {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

// The login page redirects a signed-in user to next only when it is a
// same-site path; "/\evil.example" (which browsers read as "//evil.example")
// goes to "/".
func TestLoginRedirectRejectsBackslash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	meetDatabase(t)
	id, err := db.CreateUser("bob", "hash", "Bob", "approved", false)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := auth.IssueSession(id, "user")
	if err != nil {
		t.Fatal(err)
	}
	engine := NewEngine()
	for next, want := range map[string]string{`/\evil.example`: "/", "/m/abc-defg-hjk": "/m/abc-defg-hjk"} {
		req := httptest.NewRequest(http.MethodGet, "/login?next="+url.QueryEscape(next), nil)
		req.AddCookie(&http.Cookie{Name: auth.UserCookie, Value: token})
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusFound || w.Header().Get("Location") != want {
			t.Errorf("next=%q: status=%d Location=%q, want 302 %q", next, w.Code, w.Header().Get("Location"), want)
		}
	}
}

func clearRooms(t *testing.T) {
	t.Helper()
	meetMu.Lock()
	rooms = map[string]*room{}
	meetMu.Unlock()
	t.Cleanup(func() {
		meetMu.Lock()
		rooms = map[string]*room{}
		meetMu.Unlock()
	})
}

func TestRoomCreationCappedPerUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	meetDatabase(t)
	clearRooms(t)
	server := newMeetServer(t)
	client := func(name string) *http.Client {
		id, err := db.CreateUser(name, "hash", name, "approved", false)
		if err != nil {
			t.Fatal(err)
		}
		token, _, err := auth.IssueSession(id, "user")
		if err != nil {
			t.Fatal(err)
		}
		base, _ := url.Parse(server.URL)
		jar, _ := cookiejar.New(nil)
		jar.SetCookies(base, []*http.Cookie{{Name: auth.UserCookie, Value: token, Path: "/"}})
		return &http.Client{Jar: jar}
	}
	create := func(c *http.Client) int {
		resp, err := c.Post(server.URL+"/api/meetings", "application/json", strings.NewReader(`{"mode":"mesh"}`))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	carol := client("carol")
	for i := 0; i < maxLiveRoomsPerUser; i++ {
		if code := create(carol); code != http.StatusOK {
			t.Fatalf("room %d: status %d", i+1, code)
		}
	}
	if code := create(carol); code != http.StatusTooManyRequests {
		t.Fatalf("room over the cap: status %d, want 429", code)
	}
	// The cap is per account.
	if code := create(client("dave")); code != http.StatusOK {
		t.Fatalf("other user: status %d", code)
	}
}

// newMeetServer serves NewEngine and, at cleanup, waits until every handler
// (including WebSocket read loops) has returned, so no handler touches the
// database after the next test points db at a new directory.
func newMeetServer(t *testing.T) *httptest.Server {
	t.Helper()
	engine := NewEngine()
	var running sync.WaitGroup
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		running.Add(1)
		defer running.Done()
		engine.ServeHTTP(w, r)
	}))
	t.Cleanup(func() {
		server.Close()
		running.Wait()
	})
	return server
}

type wsClient struct {
	t  *testing.T
	ws *websocket.Conn
}

func dialMeet(t *testing.T, server *httptest.Server) *wsClient {
	t.Helper()
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	c := &wsClient{t: t, ws: ws}
	if m := c.read(); m["type"] != "hello" {
		t.Fatalf("first frame %v", m)
	}
	return c
}

func (c *wsClient) send(m map[string]any) {
	c.t.Helper()
	if err := c.ws.WriteJSON(m); err != nil {
		c.t.Fatal(err)
	}
}

func (c *wsClient) read() map[string]any {
	c.t.Helper()
	_ = c.ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	var m map[string]any
	if err := c.ws.ReadJSON(&m); err != nil {
		c.t.Fatalf("read: %v", err)
	}
	return m
}

// readType skips frames until one of the given type arrives.
func (c *wsClient) readType(kind string) map[string]any {
	c.t.Helper()
	for {
		if m := c.read(); m["type"] == kind {
			return m
		}
	}
}

// expectNo fails if a frame of the given type arrives within d.
func (c *wsClient) expectNo(kind string, d time.Duration) {
	c.t.Helper()
	deadline := time.Now().Add(d)
	for {
		_ = c.ws.SetReadDeadline(deadline)
		var m map[string]any
		if err := c.ws.ReadJSON(&m); err != nil {
			return
		}
		if m["type"] == kind {
			c.t.Fatalf("unexpected %s: %v", kind, m)
		}
	}
}

func addRoom(t *testing.T, code, mode string) {
	t.Helper()
	meetMu.Lock()
	rooms[code] = &room{code: code, mode: mode, parts: map[int]*participant{}, createdAt: db.NowTS(), allowGuests: true}
	meetMu.Unlock()
}

// A volc participant on the roster gets a fresh short-lived token on request;
// a kicked participant (still connected) gets none.
func TestVolcTokenRenewalOnlyForRoster(t *testing.T) {
	gin.SetMode(gin.TestMode)
	meetDatabase(t)
	clearRooms(t)
	t.Setenv("VOLC_RTC_APP_ID", "app")
	t.Setenv("VOLC_RTC_APP_KEY", "key")
	server := newMeetServer(t)
	addRoom(t, "vvv-vvvv-vvv", modeVolc)

	host := dialMeet(t, server)
	host.send(map[string]any{"type": "join", "code": "vvv-vvvv-vvv", "guest_name": "Host", "client_id": "h"})
	joined := host.readType("joined")
	volc, _ := joined["volc"].(map[string]any)
	exp, _ := volc["expires_at"].(float64)
	if left := time.Until(time.Unix(int64(exp), 0)); left <= 0 || left > volcTokenTTL+time.Minute {
		t.Fatalf("join token expires in %v, want about %v", left, volcTokenTTL)
	}

	guest := dialMeet(t, server)
	guest.send(map[string]any{"type": "join", "code": "vvv-vvvv-vvv", "guest_name": "Guest", "client_id": "g"})
	gj := guest.readType("joined")
	guestPid := gj["self_pid"]

	guest.send(map[string]any{"type": "volc_token"})
	renewed := guest.readType("volc_token")
	rv, _ := renewed["volc"].(map[string]any)
	if rv["user_id"] != fmt.Sprintf("p%v", guestPid) || rv["token"] == "" || rv["room_id"] != "vvv-vvvv-vvv" {
		t.Fatalf("renewed token %v", renewed)
	}

	host.send(map[string]any{"type": "host_kick", "pid": guestPid})
	guest.readType("kicked")
	guest.send(map[string]any{"type": "volc_token"})
	guest.expectNo("volc_token", 300*time.Millisecond)
}

// Mesh meetings never hand out VolcEngine tokens.
func TestVolcTokenNotForMesh(t *testing.T) {
	gin.SetMode(gin.TestMode)
	meetDatabase(t)
	clearRooms(t)
	server := newMeetServer(t)
	addRoom(t, "mmm-mmmm-mmm", modeMesh)
	c := dialMeet(t, server)
	c.send(map[string]any{"type": "join", "code": "mmm-mmmm-mmm", "guest_name": "A", "client_id": "a"})
	c.readType("joined")
	c.send(map[string]any{"type": "volc_token"})
	c.expectNo("volc_token", 300*time.Millisecond)
}

// Eight sockets join one room at the same moment. Each client applies
// joined / peer_joined / peer_left in arrival order; every client must end
// up with the full roster. Before the per-connection queue, a peer_joined
// could overtake the joined it depends on, and the client lost that peer.
func TestConcurrentJoinsKeepRosterOrder(t *testing.T) {
	gin.SetMode(gin.TestMode)
	meetDatabase(t)
	clearRooms(t)
	server := newMeetServer(t)
	for trial := 0; trial < 15; trial++ {
		code := fmt.Sprintf("ooo-oooo-%03d", trial)
		addRoom(t, code, modeMesh)
		const n = 8
		clients := make([]*wsClient, n)
		for i := range clients {
			clients[i] = dialMeet(t, server)
		}
		views := make([]map[int]bool, n)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				ws := clients[i].ws
				roster := map[int]bool{}
				<-start
				_ = ws.WriteJSON(map[string]any{"type": "join", "code": code, "guest_name": fmt.Sprint("g", i), "client_id": fmt.Sprint(trial, "-", i)})
				for {
					_ = ws.SetReadDeadline(time.Now().Add(400 * time.Millisecond))
					var m map[string]any
					if ws.ReadJSON(&m) != nil {
						break
					}
					switch m["type"] {
					case "joined":
						roster = map[int]bool{}
						for _, p := range m["participants"].([]any) {
							roster[int(p.(map[string]any)["pid"].(float64))] = true
						}
					case "peer_joined":
						roster[int(m["participant"].(map[string]any)["pid"].(float64))] = true
					case "peer_left":
						delete(roster, int(m["pid"].(float64)))
					}
				}
				views[i] = roster
			}(i)
		}
		close(start)
		wg.Wait()
		for i, v := range views {
			if len(v) != n {
				t.Errorf("trial %d: client %d sees %d of %d participants", trial, i, len(v), n)
			}
		}
		for _, c := range clients {
			_ = c.ws.Close()
		}
	}
}

// A socket whose outbound queue overflows is closed instead of blocking the
// sender, and later sends to it are dropped.
func TestSendQueueOverflowClosesSocket(t *testing.T) {
	upgraded := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		upgraded <- ws
	}))
	defer server.Close()
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ws := <-upgraded
	cn := &conn{ws: ws, out: make(chan outFrame, sendQueueSize), done: make(chan struct{})}
	// No writer goroutine: the queue fills up exactly as with a stalled client.
	for i := 0; i < sendQueueSize; i++ {
		if !cn.send(i) {
			t.Fatalf("send %d refused before the queue was full", i)
		}
	}
	if cn.send("overflow") {
		t.Fatal("send past a full queue reported success")
	}
	if cn.send("after") {
		t.Fatal("send to a closed socket reported success")
	}
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := client.ReadMessage(); err == nil {
		t.Fatal("socket still open after the queue overflowed")
	}
}

// A participant the host removed can't join that room again: not from a new
// socket of the same tab (client id), and not from another device of the
// same account. Other people still can.
func TestRemovedParticipantCannotRejoin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	meetDatabase(t)
	clearRooms(t)
	server := newMeetServer(t)
	const code = "kkk-kkkk-kkk"
	addRoom(t, code, modeMesh)

	dialAs := func(name string) *wsClient {
		id, err := db.CreateUser(name, "hash", name, "approved", false)
		if err != nil {
			t.Fatal(err)
		}
		token, _, err := auth.IssueSession(id, "user")
		if err != nil {
			t.Fatal(err)
		}
		header := http.Header{"Cookie": {auth.UserCookie + "=" + token}}
		ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/ws", header)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ws.Close() })
		c := &wsClient{t: t, ws: ws}
		c.readType("hello")
		return c
	}

	host := dialMeet(t, server)
	host.send(map[string]any{"type": "join", "code": code, "guest_name": "Host", "client_id": "host"})
	host.readType("joined")

	guest := dialMeet(t, server)
	guest.send(map[string]any{"type": "join", "code": code, "guest_name": "Troll", "client_id": "troll"})
	guestPid := guest.readType("joined")["self_pid"]
	host.readType("peer_joined")
	host.send(map[string]any{"type": "host_kick", "pid": guestPid})
	guest.readType("kicked")
	host.readType("peer_left")

	// The same socket, and a new socket of the same tab, are refused.
	guest.send(map[string]any{"type": "join", "code": code, "guest_name": "Troll", "client_id": "troll"})
	if m := guest.readType("error"); m["message"] != "The host removed you from this meeting." {
		t.Fatalf("rejoin on the kicked socket: %v", m)
	}
	again := dialMeet(t, server)
	again.send(map[string]any{"type": "join", "code": code, "guest_name": "Troll", "client_id": "troll"})
	again.readType("error")
	host.expectNo("peer_joined", 200*time.Millisecond)

	// A registered account is refused on every device.
	erin := dialAs("erin")
	erin.send(map[string]any{"type": "join", "code": code, "client_id": "erin-phone"})
	erinPid := erin.readType("joined")["self_pid"]
	host.send(map[string]any{"type": "host_kick", "pid": erinPid})
	erin.readType("kicked")
	laptop := &wsClient{t: t, ws: erin.ws}
	laptop.send(map[string]any{"type": "join", "code": code, "client_id": "erin-laptop"})
	laptop.readType("error")

	// Someone else still gets in.
	friend := dialMeet(t, server)
	friend.send(map[string]any{"type": "join", "code": code, "guest_name": "Friend", "client_id": "friend"})
	friend.readType("joined")
}

// A participant that floods frames gets only its budget through: the others
// receive a bounded number of fan-outs and keep their sockets.
func TestFrameFloodIsDropped(t *testing.T) {
	gin.SetMode(gin.TestMode)
	meetDatabase(t)
	clearRooms(t)
	burst, refill := frameBurst, frameRefill
	frameBurst, frameRefill = 20, time.Hour
	t.Cleanup(func() { frameBurst, frameRefill = burst, refill })
	server := newMeetServer(t)
	const code = "fff-ffff-fff"
	addRoom(t, code, modeMesh)

	victim := dialMeet(t, server)
	victim.send(map[string]any{"type": "join", "code": code, "guest_name": "Victim", "client_id": "v"})
	victim.readType("joined")
	attacker := dialMeet(t, server)
	attacker.send(map[string]any{"type": "join", "code": code, "guest_name": "Flood", "client_id": "f"})
	attacker.readType("joined")
	victim.readType("peer_joined")

	for i := 0; i < 2000; i++ {
		attacker.send(map[string]any{"type": "state", "muted": i%2 == 0, "cam_on": true, "sharing": false})
	}
	// The server reads the whole flood within the pause; the victim's pong
	// then queues behind every peer_state that got through.
	time.Sleep(300 * time.Millisecond)
	victim.send(map[string]any{"type": "ping"})
	got := 0
	for m := victim.read(); m["type"] != "pong"; m = victim.read() {
		if m["type"] == "peer_state" {
			got++
		}
	}
	// 20 units: one went to the attacker's join, so at most 19 states.
	if got == 0 || got > 19 {
		t.Fatalf("victim received %d peer_state frames, want 1..19", got)
	}
}
