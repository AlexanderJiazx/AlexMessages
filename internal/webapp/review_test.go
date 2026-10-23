package webapp

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"alexmessage/internal/auth"
	"alexmessage/internal/db"
	"alexmessage/internal/runtime"
)

func reviewUser(t *testing.T, name string) *db.User {
	t.Helper()
	hash, err := auth.HashPassword("password123")
	if err != nil {
		t.Fatal(err)
	}
	id, err := db.CreateUser(name, hash, name, "approved", false)
	if err != nil {
		t.Fatal(err)
	}
	u, err := db.GetUserByID(id)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestUploadAuthorizationAndActiveContent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	initTranscribeTestDB(t)
	a := reviewUser(t, "alice")
	b := reviewUser(t, "bob")
	stranger := reviewUser(t, "stranger")
	tokenA := sessionFor(t, a.ID)
	tokenB := sessionFor(t, b.ID)
	tokenC := sessionFor(t, stranger.ID)
	r := NewEngine()
	for _, name := range []string{"attack.html", "attack.svg", "attack.png"} {
		t.Run(name, func(t *testing.T) {
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			part, _ := writer.CreateFormFile("file", name)
			_, _ = io.WriteString(part, "<html><script>fetch('/api/me/export')</script></html>")
			_ = writer.Close()
			req := httptest.NewRequest("POST", "/api/upload", &body)
			req.Header.Set("Authorization", "Bearer "+tokenA)
			req.Header.Set("Content-Type", writer.FormDataContentType())
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != 200 {
				t.Fatalf("upload=%d %s", w.Code, w.Body.String())
			}
			var upload db.Attachment
			if err := json.Unmarshal(w.Body.Bytes(), &upload); err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				token string
				code  int
			}{{"", 401}, {tokenA, 200}, {tokenB, 404}, {tokenC, 404}} {
				get := httptest.NewRequest("GET", upload.URL, nil)
				if tc.token != "" {
					get.Header.Set("Authorization", "Bearer "+tc.token)
				}
				response := httptest.NewRecorder()
				r.ServeHTTP(response, get)
				if response.Code != tc.code {
					t.Fatalf("unsent access=%d want=%d", response.Code, tc.code)
				}
			}
			if _, err := db.InsertMessageWithAttachments("msg-"+name, db.DMChannelID(a.ID, b.ID), &a.ID, "file", nil, "message", name, []db.Attachment{upload}); err != nil {
				t.Fatal(err)
			}
			for _, method := range []string{"GET", "HEAD"} {
				get := httptest.NewRequest(method, upload.URL, nil)
				get.Header.Set("Authorization", "Bearer "+tokenB)
				response := httptest.NewRecorder()
				r.ServeHTTP(response, get)
				if response.Code != 200 || response.Header().Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(response.Header().Get("Content-Disposition"), "attachment;") || response.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(response.Header().Get("Content-Security-Policy"), "sandbox") {
					t.Fatalf("download=%d %v", response.Code, response.Header())
				}
			}
			get := httptest.NewRequest("GET", upload.URL+"?token="+tokenB, nil)
			response := httptest.NewRecorder()
			r.ServeHTTP(response, get)
			if response.Code != 401 {
				t.Fatal("REST query accepted session token")
			}
		})
	}
}

type generatedUpload struct{ remaining, consumed int64 }

func (r *generatedUpload) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := int64(len(p))
	if n > r.remaining {
		n = r.remaining
	}
	for i := int64(0); i < n; i++ {
		p[i] = 'a'
	}
	r.remaining -= n
	r.consumed += n
	return int(n), nil
}

func TestUploadStopsReadingOversizedMultipart(t *testing.T) {
	gin.SetMode(gin.TestMode)
	initTranscribeTestDB(t)
	u := reviewUser(t, "alice")
	token := sessionFor(t, u.ID)
	r := NewEngine()
	file := &generatedUpload{remaining: 100 * 1024 * 1024}
	content := io.MultiReader(strings.NewReader("--boundary\r\nContent-Disposition: form-data; name=\"file\"; filename=\"huge.bin\"\r\nContent-Type: application/octet-stream\r\n\r\n"), file, strings.NewReader("\r\n--boundary--\r\n"))
	req := httptest.NewRequest("POST", "/api/upload", content)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=boundary")
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 413 || file.consumed > runtime.MaxUploadBytes+1024*1024 {
		t.Fatalf("status=%d consumed=%d", w.Code, file.consumed)
	}
}

func TestBearerPasswordAndLogoutRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	initTranscribeTestDB(t)
	u := reviewUser(t, "alice")
	token := sessionFor(t, u.ID)
	other := sessionFor(t, u.ID)
	r := NewEngine()
	req := httptest.NewRequest("POST", "/api/me/password", strings.NewReader(`{"current_password":"password123","new_password":"newpassword456"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if auth.ResolveSession(token, "user") == nil || auth.ResolveSession(other, "user") != nil {
		t.Fatal("wrong sessions retained")
	}
	var result map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	if result["session_token"] != token {
		t.Fatal("current bearer changed")
	}
	logout := httptest.NewRequest("POST", "/api/logout", nil)
	logout.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, logout)
	if w.Code != 200 || auth.ResolveSession(token, "user") != nil {
		t.Fatal("bearer logout failed")
	}
}

func TestSocketRevokedWhileIdle(t *testing.T) {
	gin.SetMode(gin.TestMode)
	initTranscribeTestDB(t)
	u := reviewUser(t, "alice")
	token := sessionFor(t, u.ID)
	engine := NewEngine()
	handlerDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { defer close(handlerDone); engine.ServeHTTP(w, r) }))
	defer server.Close()
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/ws?token="+token, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = ws.Close()
		select {
		case <-handlerDone:
		case <-time.After(8 * time.Second):
			t.Error("WebSocket handler did not stop")
		}
	}()
	if err := ws.SetReadDeadline(time.Now().Add(8 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ws.ReadMessage(); err != nil {
		t.Fatal(err)
	} // init
	if _, _, err := ws.ReadMessage(); err != nil {
		t.Fatal(err)
	} // initial presence
	// Direct DB mutation exercises revocation written by another server process.
	if err := db.UpdateUserStatus(u.ID, "disabled"); err != nil {
		t.Fatal(err)
	}
	for {
		_, _, err = ws.ReadMessage()
		if err != nil {
			break
		}
	}
	if !websocket.IsCloseError(err, 4401) {
		t.Fatalf("close=%v", err)
	}
}

func TestFailedMessageIsNotBroadcastable(t *testing.T) {
	initTranscribeTestDB(t)
	missing := 999
	if _, err := makeMessagePayload(&missing, "dm:1:999", "ghost", nil, nil, "message", "nonce"); err == nil {
		t.Fatal("failed database insert reported success")
	}
	messages, err := db.FetchChannelWindow("dm:1:999", 50, nil, nil)
	if err != nil || len(messages) != 0 {
		t.Fatalf("messages=%+v err=%v", messages, err)
	}
}

func TestDebugReportAttributesBearerUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	initTranscribeTestDB(t)
	u := reviewUser(t, "alice")
	token := sessionFor(t, u.ID)
	req := httptest.NewRequest("POST", "/api/debug/report", strings.NewReader(`{"session":"native-test","events":[{"event":"native_event","message":"test","user_id":999}]}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	NewEngine().ServeHTTP(w, req)
	events, err := db.QueryDebugEvents(db.DebugFilter{Session: "native-test"})
	if w.Code != 200 || err != nil || len(events) != 1 || events[0].UserID == nil || *events[0].UserID != u.ID {
		t.Fatalf("events=%+v err=%v", events, err)
	}
}

func TestUploadDirectoryRequestsAndTraversal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	initTranscribeTestDB(t)
	u := reviewUser(t, "alice")
	token := sessionFor(t, u.ID)
	dir, _ := db.UserUploadDir(u.ID)
	_ = os.WriteFile(filepath.Join(dir, "safe.txt"), []byte("safe"), 0600)
	r := NewEngine()
	for _, path := range []string{fmt.Sprintf("/uploads/%d", u.ID), fmt.Sprintf("/uploads/%d/..", u.ID), fmt.Sprintf("/uploads/%d/%%2e%%2e%%2fsecret", u.ID)} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code == 200 {
			t.Fatalf("served %s", path)
		}
	}
}

// The server pings every socket, so a client that never sends a data frame of
// its own (the legacy static/app.js) stays connected past the read deadline:
// its WebSocket stack answers each ping with a pong.
func TestSocketHeartbeatKeepsSilentClient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	waitUntil(t, "other tests' sockets to close", func() bool {
		return len(runtime.PresenceTracker().AllSockets()) == 0
	})
	initTranscribeTestDB(t)
	u := reviewUser(t, "alice")
	token := sessionFor(t, u.ID)
	defaultPing, defaultTimeout := wsPingInterval, wsReadTimeout
	wsPingInterval, wsReadTimeout = 50*time.Millisecond, 300*time.Millisecond
	engine := NewEngine()
	handlerDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { defer close(handlerDone); engine.ServeHTTP(w, r) }))
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/ws?token="+token, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = ws.Close()
		select {
		case <-handlerDone:
		case <-time.After(5 * time.Second):
			t.Error("WebSocket handler did not stop")
		}
		server.Close()
		wsPingInterval, wsReadTimeout = defaultPing, defaultTimeout
	}()
	frames := make(chan string, 16)
	readErr := make(chan error, 1)
	go func() {
		for {
			var m map[string]any
			if err := ws.ReadJSON(&m); err != nil {
				readErr <- err
				return
			}
			frames <- fmt.Sprint(m["type"])
		}
	}()
	select {
	case err := <-readErr:
		t.Fatalf("silent client was disconnected: %v", err)
	case <-time.After(5 * wsReadTimeout):
	}
	if err := ws.WriteJSON(gin.H{"type": "ping"}); err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case kind := <-frames:
			if kind == "pong" {
				return
			}
		case err := <-readErr:
			t.Fatalf("waiting for pong: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("no pong")
		}
	}
}

// An init that can't do one of its reads closes the socket with 1011, so the
// client retries instead of replacing its state with a partial init, and it
// leaves no ghost socket in presence. Each case breaks a table or column
// that only one read uses, so the case fails if that read's error is ignored
// (the history holds no call message, so only the unread counts read the
// calls table). Two reads have no case of their own: signing in reads the
// users table, and the thread-partners read uses only messages.channel,
// which the history read uses too.
func TestSocketInitFailureLeavesNoPresence(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct{ read, breakSQL string }{
		{"contacts", "ALTER TABLE contacts RENAME TO contacts_unreadable"},
		{"dm_state", "ALTER TABLE dm_state RENAME COLUMN force_unread TO force_unread_unreadable"},
		{"thread_history", "ALTER TABLE messages RENAME COLUMN client_id TO client_id_unreadable"},
		{"unread_counts", "ALTER TABLE calls RENAME TO calls_unreadable"},
	}
	for _, tc := range cases {
		t.Run(tc.read, func(t *testing.T) {
			waitUntil(t, "other tests' sockets to close", func() bool {
				return len(runtime.PresenceTracker().AllSockets()) == 0
			})
			initTranscribeTestDB(t)
			u := reviewUser(t, "alice")
			token := sessionFor(t, u.ID)
			// A contact gives init a thread to fill in from the history.
			if err := db.AddContact(u.ID, reviewUser(t, "bob").ID); err != nil {
				t.Fatal(err)
			}
			conn, err := sql.Open("sqlite", db.DBPath)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			// Direct DB mutation makes one read fail; sessions still resolve.
			if _, err := conn.Exec(tc.breakSQL); err != nil {
				t.Fatal(err)
			}
			engine := NewEngine()
			handlerDone := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { defer close(handlerDone); engine.ServeHTTP(w, r) }))
			defer server.Close()
			ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/ws?token="+token, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer ws.Close()
			if err := ws.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			_, _, err = ws.ReadMessage()
			if !websocket.IsCloseError(err, websocket.CloseInternalServerErr) {
				t.Errorf("close=%v", err)
			}
			select {
			case <-handlerDone:
			case <-time.After(5 * time.Second):
				t.Fatal("WebSocket handler did not stop")
			}
			if sockets := runtime.PresenceTracker().AllSockets(); len(sockets) != 0 {
				t.Fatalf("%d ghost socket(s) left in presence", len(sockets))
			}
		})
	}
}
