package webapp

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"alexmessage/internal/auth"
	"alexmessage/internal/db"
	"alexmessage/internal/runtime"
)

func postJSON(r http.Handler, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// Subscribing accepts only https endpoints on known push services, and
// unsubscribing removes only the caller's own subscription.
func TestPushSubscribeEndpointRulesAndOwnUnsubscribe(t *testing.T) {
	gin.SetMode(gin.TestMode)
	initTranscribeTestDB(t)
	a := reviewUser(t, "alice")
	b := reviewUser(t, "bob")
	tokenA, tokenB := sessionFor(t, a.ID), sessionFor(t, b.ID)
	r := NewEngine()
	sub := func(endpoint string) string {
		return fmt.Sprintf(`{"endpoint":%q,"keys":{"p256dh":"key","auth":"auth"}}`, endpoint)
	}
	for _, endpoint := range []string{"http://127.0.0.1:8001/api/debug/clear", "https://169.254.169.254/x", "https://attacker.example/x", "http://fcm.googleapis.com/fcm/send/x"} {
		if w := postJSON(r, "/api/push/subscribe", tokenA, sub(endpoint)); w.Code != 400 {
			t.Errorf("subscribe %s = %d, want 400", endpoint, w.Code)
		}
	}
	bobEndpoint := "https://fcm.googleapis.com/fcm/send/bob"
	if w := postJSON(r, "/api/push/subscribe", tokenB, sub(bobEndpoint)); w.Code != 200 {
		t.Fatalf("subscribe bob = %d %s", w.Code, w.Body.String())
	}
	if w := postJSON(r, "/api/push/unsubscribe", tokenA, fmt.Sprintf(`{"endpoint":%q}`, bobEndpoint)); w.Code != 200 {
		t.Fatalf("unsubscribe = %d", w.Code)
	}
	if subs, _ := db.ListPushSubscriptions(b.ID); len(subs) != 1 {
		t.Fatal("alice's unsubscribe removed bob's subscription")
	}
	if subs, _ := db.ListPushSubscriptions(a.ID); len(subs) != 0 {
		t.Fatalf("alice has %d subscriptions after refused subscribes", len(subs))
	}
}

// Forwarding headers set the client IP only on a request from loopback (the
// local nginx); CF-Connecting-IP wins over X-Forwarded-For.
func TestClientIPTrustsOnlyLoopbackProxy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	initTranscribeTestDB(t)
	r := NewEngine()
	r.GET("/test/client-ip", func(c *gin.Context) { c.String(200, c.ClientIP()) })
	for _, tc := range []struct {
		remote string
		header map[string]string
		want   string
	}{
		{"127.0.0.1:5555", map[string]string{"CF-Connecting-IP": "198.51.100.7", "X-Forwarded-For": "203.0.113.1"}, "198.51.100.7"},
		{"[::1]:5555", map[string]string{"X-Forwarded-For": "203.0.113.1"}, "203.0.113.1"},
		{"127.0.0.1:5555", map[string]string{"X-Real-IP": "203.0.113.2"}, "203.0.113.2"},
		{"127.0.0.1:5555", nil, "127.0.0.1"},
		{"203.0.113.9:5555", map[string]string{"CF-Connecting-IP": "198.51.100.7", "X-Forwarded-For": "198.51.100.8", "X-Real-IP": "198.51.100.9"}, "203.0.113.9"},
	} {
		req := httptest.NewRequest("GET", "/test/client-ip", nil)
		req.RemoteAddr = tc.remote
		for k, v := range tc.header {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if got := w.Body.String(); got != tc.want {
			t.Errorf("remote %s headers %v: ClientIP = %s, want %s", tc.remote, tc.header, got, tc.want)
		}
	}
}

// A message frame with more than runtime.MaxAttachments attachments is
// refused whole; init tells clients the limit.
func TestMessageWithTooManyAttachmentsIsRefused(t *testing.T) {
	initTranscribeTestDB(t)
	a := reviewUser(t, "alice")
	b := reviewUser(t, "bob")
	ch := db.DMChannelID(a.ID, b.ID)
	dir := filepath.Join(db.UploadRoot, strconv.Itoa(a.ID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	atts := func(n int) []any {
		out := []any{}
		for i := 0; i < n; i++ {
			name := fmt.Sprintf("tok%d_f%d.txt", i, i)
			if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			out = append(out, map[string]any{"url": fmt.Sprintf("/uploads/%d/%s", a.ID, name), "name": name, "size": 1.0, "mime": "text/plain"})
		}
		return out
	}
	handleWSMessage(a.ID, map[string]any{"channel": ch, "text": "too many", "attachments": atts(runtime.MaxAttachments + 1)})
	if msgs, _ := db.FetchChannelWindow(ch, 50, nil, nil); len(msgs) != 0 {
		t.Fatalf("a message with %d attachments was saved: %+v", runtime.MaxAttachments+1, msgs)
	}
	handleWSMessage(a.ID, map[string]any{"channel": ch, "text": "just right", "attachments": atts(runtime.MaxAttachments)})
	msgs, _ := db.FetchChannelWindow(ch, 50, nil, nil)
	if len(msgs) != 1 || len(msgs[0].Attachments) != runtime.MaxAttachments {
		t.Fatalf("message with %d attachments: %+v", runtime.MaxAttachments, msgs)
	}
	init, err := buildInitPayload(a)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(init)
	if !strings.Contains(string(raw), fmt.Sprintf(`"max_attachments":%d`, runtime.MaxAttachments)) {
		t.Fatalf("init payload has no max_attachments: %s", raw)
	}
}

// Wrong passwords from one IP are refused with 429 after the burst; a
// correct password does not use up the budget.
func TestLoginRateLimitedPerIP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	initTranscribeTestDB(t)
	auth.LoginLimiter.Reset()
	t.Cleanup(auth.LoginLimiter.Reset)
	reviewUser(t, "alice")
	r := NewEngine()
	login := func(remote, password string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/login", strings.NewReader(fmt.Sprintf(`{"username":"alice","password":%q}`, password)))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = remote
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	for i := 0; i < 20; i++ {
		if w := login("198.51.100.1:1", "password123"); w.Code != 200 {
			t.Fatalf("correct login %d = %d", i+1, w.Code)
		}
	}
	for i := 0; i < auth.LoginLimiter.Burst; i++ {
		if w := login("198.51.100.1:1", "wrong-password"); w.Code != 401 {
			t.Fatalf("wrong login %d = %d, want 401", i+1, w.Code)
		}
	}
	w := login("198.51.100.1:1", "password123")
	if w.Code != 429 || !strings.Contains(w.Body.String(), auth.TooManyAttemptsDetail) {
		t.Fatalf("login after the burst = %d %s, want 429", w.Code, w.Body.String())
	}
	if w := login("198.51.100.2:1", "password123"); w.Code != 200 {
		t.Fatalf("another IP = %d, want 200", w.Code)
	}
	reg := httptest.NewRequest("POST", "/api/register", strings.NewReader(`{"username":"newbie","password":"password123"}`))
	reg.Header.Set("Content-Type", "application/json")
	reg.RemoteAddr = "198.51.100.1:1"
	rw := httptest.NewRecorder()
	r.ServeHTTP(rw, reg)
	if rw.Code != 429 {
		t.Fatalf("register from a limited IP = %d, want 429", rw.Code)
	}
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Concurrent avatar changes leave the database naming a file that exists, and
// no other file of that user.
func TestConcurrentAvatarChangesKeepRecordedFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	initTranscribeTestDB(t)
	u := reviewUser(t, "alice")
	token := sessionFor(t, u.ID)
	r := NewEngine()
	photo := pngBytes(t)
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var req *http.Request
			if i%3 == 2 {
				req = httptest.NewRequest("DELETE", "/api/me/avatar", nil)
			} else {
				var body bytes.Buffer
				mw := multipart.NewWriter(&body)
				part, _ := mw.CreateFormFile("file", "a.png")
				_, _ = part.Write(photo)
				_ = mw.Close()
				req = httptest.NewRequest("POST", "/api/me/avatar", &body)
				req.Header.Set("Content-Type", mw.FormDataContentType())
			}
			req.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != 200 {
				t.Errorf("avatar change %d = %d %s", i, w.Code, w.Body.String())
			}
		}(i)
	}
	wg.Wait()
	fresh, _ := db.GetUserByID(u.ID)
	entries, _ := os.ReadDir(db.AvatarRoot)
	var files []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), strconv.Itoa(u.ID)+"_") {
			files = append(files, e.Name())
		}
	}
	if fresh.Avatar == "" {
		if len(files) != 0 {
			t.Fatalf("no avatar recorded but files remain: %v", files)
		}
		return
	}
	if len(files) != 1 || "/avatars/"+files[0] != fresh.Avatar {
		t.Fatalf("recorded %q, files on disk %v", fresh.Avatar, files)
	}
}

// Avatar and transcription uploads stop reading an oversized body and answer
// 413, as /api/upload does.
func TestAvatarAndTranscribeCapRequestBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	initTranscribeTestDB(t)
	u := reviewUser(t, "alice")
	token := sessionFor(t, u.ID)
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("OPENROUTER_BASE_URL", "http://127.0.0.1:1")
	r := NewEngine()
	for _, tc := range []struct {
		path  string
		limit int64
	}{
		{"/api/me/avatar", maxAvatarBytes + 1024*1024},
		{"/api/transcribe", transcribeMaxBytes + 1024*1024},
	} {
		file := &generatedUpload{remaining: 100 * 1024 * 1024}
		content := io.MultiReader(strings.NewReader("--boundary\r\nContent-Disposition: form-data; name=\"file\"; filename=\"big.wav\"\r\nContent-Type: audio/wav\r\n\r\n"), file, strings.NewReader("\r\n--boundary--\r\n"))
		req := httptest.NewRequest("POST", tc.path, content)
		req.Header.Set("Content-Type", "multipart/form-data; boundary=boundary")
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 413 || file.consumed > tc.limit {
			t.Errorf("%s: status=%d consumed=%d (cap %d)", tc.path, w.Code, file.consumed, tc.limit)
		}
	}
}

// Mark-read with a position reads only up to the newest message at or below
// it, and the dm_read broadcast carries that position. Without a body the
// thread is read through its newest message.
func TestMarkReadWithPosition(t *testing.T) {
	gin.SetMode(gin.TestMode)
	waitUntil(t, "other tests' sockets to close", func() bool {
		return len(runtime.PresenceTracker().AllSockets()) == 0
	})
	initTranscribeTestDB(t)
	a := reviewUser(t, "alice")
	b := reviewUser(t, "bob")
	tokenA, tokenB := sessionFor(t, a.ID), sessionFor(t, b.ID)
	ch := db.DMChannelID(a.ID, b.ID)
	for i := 0; i < 3; i++ {
		if _, err := db.InsertMessage(fmt.Sprintf("m%d", i), ch, &a.ID, "hi", nil, "message", nil); err != nil {
			t.Fatal(err)
		}
	}
	msgs, _ := db.FetchDMWindow(b.ID, ch, 50, nil, "")
	pos := map[string]int64{}
	for _, m := range msgs {
		pos[m.ID] = m.Position
	}

	engine := NewEngine()
	handlerDone := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ws" {
			defer once.Do(func() { close(handlerDone) })
		}
		engine.ServeHTTP(w, r)
	}))
	defer server.Close()
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/ws?token="+tokenA, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Wait for the socket's handler to finish, so it can't touch the next
	// test's database.
	defer func() {
		_ = ws.Close()
		select {
		case <-handlerDone:
		case <-time.After(8 * time.Second):
			t.Error("WebSocket handler did not stop")
		}
	}()
	next := func(kind string) map[string]any {
		t.Helper()
		_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
		for {
			var frame map[string]any
			if err := ws.ReadJSON(&frame); err != nil {
				t.Fatalf("no %s frame: %v", kind, err)
			}
			if frame["type"] == kind {
				return frame
			}
		}
	}
	nextDMRead := func() map[string]any { return next("dm_read") }
	next("init") // alice's socket is registered once init arrives

	w := postJSON(engine, "/api/dm/"+ch+"/read", tokenB, fmt.Sprintf(`{"position":%d}`, pos["m1"]))
	if w.Code != 200 {
		t.Fatalf("mark read = %d %s", w.Code, w.Body.String())
	}
	if got := int64(nextDMRead()["last_read_position"].(float64)); got != pos["m1"] {
		t.Fatalf("dm_read position = %d, want %d", got, pos["m1"])
	}
	if n, _ := db.CountUnread(b.ID, ch, 0, 0); n != 1 {
		t.Fatalf("bob unread = %d, want 1", n)
	}
	// An older position never moves the read position back.
	_ = postJSON(engine, "/api/dm/"+ch+"/read", tokenB, fmt.Sprintf(`{"position":%d}`, pos["m0"]))
	if got := int64(nextDMRead()["last_read_position"].(float64)); got != pos["m1"] {
		t.Fatalf("dm_read position after an older position = %d, want %d", got, pos["m1"])
	}
	for _, bad := range []string{`{"position":-1}`, `{"position":1.5}`, `{"position":"x"}`, `[1]`} {
		if w := postJSON(engine, "/api/dm/"+ch+"/read", tokenB, bad); w.Code != 400 {
			t.Errorf("body %s = %d, want 400", bad, w.Code)
		}
	}
	// No body: read through the newest message.
	_ = postJSON(engine, "/api/dm/"+ch+"/read", tokenB, "")
	if got := int64(nextDMRead()["last_read_position"].(float64)); got != pos["m2"] {
		t.Fatalf("dm_read position without a body = %d, want %d", got, pos["m2"])
	}
}

// An account that floods message frames gets only its budget saved (30 at
// once), over all of its sockets; the rest are dropped unsaved.
func TestMessageFloodIsLimited(t *testing.T) {
	f := newCallFixture(t, 0, 0)
	messageLimiter.Reset()
	t.Cleanup(messageLimiter.Reset)
	phone, laptop := f.dial(t, f.tokA), f.dial(t, f.tokA)
	ch := db.DMChannelID(f.alice.ID, f.bob.ID)
	for i := 0; i < 50; i++ {
		dev := phone
		if i%2 == 1 {
			dev = laptop
		}
		dev.send(gin.H{"type": "message", "channel": ch, "text": fmt.Sprint("spam ", i), "client_id": fmt.Sprint("c", i)})
	}
	phone.sync()
	laptop.sync()
	msgs, err := db.FetchChannelWindow(ch, 100, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The budget refills 2 a second, so a slow run may save one or two more.
	if len(msgs) < 30 || len(msgs) > 33 {
		t.Fatalf("%d of 50 flooded messages were saved, want about 30", len(msgs))
	}
}

func TestLoginShellHasCacheAndSecurityHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	initTranscribeTestDB(t)
	w := httptest.NewRecorder()
	NewEngine().ServeHTTP(w, httptest.NewRequest("GET", "/login", nil))
	if w.Code != 200 {
		t.Fatalf("status=%d", w.Code)
	}
	for h, v := range map[string]string{
		"Cache-Control":          "no-cache",
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	} {
		if got := w.Header().Get(h); got != v {
			t.Fatalf("%s=%q want %q", h, got, v)
		}
	}
}

// An open_dm whose reads fail closes the socket with 1011 and sends no
// dm_opened, so the client does not replace the thread's history with nothing.
func TestOpenDMFailureClosesSocket(t *testing.T) {
	gin.SetMode(gin.TestMode)
	waitUntil(t, "other tests' sockets to close", func() bool {
		return len(runtime.PresenceTracker().AllSockets()) == 0
	})
	initTranscribeTestDB(t)
	alice := reviewUser(t, "alice")
	bob := reviewUser(t, "bob")
	token := sessionFor(t, alice.ID)
	engine := NewEngine()
	server := httptest.NewServer(engine)
	defer server.Close()
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/ws?token="+token, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	var first struct {
		Type string `json:"type"`
	}
	if err := ws.ReadJSON(&first); err != nil || first.Type != "init" {
		t.Fatalf("first frame=%q err=%v", first.Type, err)
	}
	conn, err := sql.Open("sqlite", db.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec("ALTER TABLE dm_state RENAME COLUMN force_unread TO force_unread_unreadable"); err != nil {
		t.Fatal(err)
	}
	if err := ws.WriteJSON(gin.H{"type": "open_dm", "peer_id": bob.ID}); err != nil {
		t.Fatal(err)
	}
	for {
		var f struct {
			Type string `json:"type"`
		}
		err := ws.ReadJSON(&f)
		if err == nil {
			if f.Type == "dm_opened" {
				t.Fatal("dm_opened was sent")
			}
			continue
		}
		if !websocket.IsCloseError(err, websocket.CloseInternalServerErr) {
			t.Fatalf("close=%v", err)
		}
		break
	}
	// The server side of this socket still runs after the close frame: its
	// last database access writes the ws_disconnect debug event. Wait for
	// it, so the next test does not open a new database underneath it.
	waitUntil(t, "the server to finish the closed socket", func() bool {
		events, err := db.QueryDebugEvents(db.DebugFilter{App: "messages", Search: "ws_disconnect"})
		return err == nil && len(events) > 0
	})
}

func TestExportAnswers500WhenAReadFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	initTranscribeTestDB(t)
	alice := reviewUser(t, "alice")
	bob := reviewUser(t, "bob")
	token := sessionFor(t, alice.ID)
	ch := db.DMChannelID(alice.ID, bob.ID)
	if _, err := db.InsertMessage("m1", ch, &alice.ID, "hello", nil, "message", nil); err != nil {
		t.Fatal(err)
	}
	engine := NewEngine()
	get := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/api/me/export", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w
	}
	w := get()
	if w.Code != 200 || !strings.Contains(w.Body.String(), "hello") {
		t.Fatalf("before: status=%d body=%s", w.Code, w.Body.String())
	}
	conn, err := sql.Open("sqlite", db.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec("ALTER TABLE messages RENAME COLUMN client_id TO client_id_unreadable"); err != nil {
		t.Fatal(err)
	}
	w = get()
	if w.Code != 500 || w.Header().Get("Content-Disposition") != "" {
		t.Fatalf("after: status=%d disposition=%q", w.Code, w.Header().Get("Content-Disposition"))
	}
}
