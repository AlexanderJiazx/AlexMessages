package meet

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"alexmessage/internal/auth"
	"alexmessage/internal/db"
)

func meetDatabase(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	db.BaseDir = root
	db.DataDir = filepath.Join(root, "data")
	db.DBPath = filepath.Join(db.DataDir, "alexmessage.db")
	db.UploadRoot = filepath.Join(db.DataDir, "uploads")
	db.AvatarRoot = filepath.Join(db.DataDir, "avatars")
	if err := db.InitDB(); err != nil {
		t.Fatal(err)
	}
}

// A browser still holding a revoked am_session cookie (its user signed out in
// Messages, which ends every session) opens a guest-enabled meeting link. The
// page clears the dead cookie, so the page's socket opens anonymously and
// reaches the guest gate instead of being closed with 4401 and sent to /login.
func TestRevokedCookieReachesGuestGate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	meetDatabase(t)
	id, err := db.CreateUser("alice", "hash", "Alice", "approved", false)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := auth.IssueSession(id, "user")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteUserSessions(id); err != nil {
		t.Fatal(err)
	}
	code := "abc-defg-hjk"
	meetMu.Lock()
	rooms[code] = &room{code: code, mode: modeMesh, parts: map[int]*participant{}, createdAt: db.NowTS(), allowGuests: true}
	meetMu.Unlock()
	t.Cleanup(func() {
		meetMu.Lock()
		delete(rooms, code)
		meetMu.Unlock()
	})

	engine := NewEngine()
	handlers := make(chan struct{}, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { handlers <- struct{}{} }()
		engine.ServeHTTP(w, r)
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	jar, _ := cookiejar.New(nil)
	jar.SetCookies(base, []*http.Cookie{{Name: auth.UserCookie, Value: token, Path: "/"}})
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	resp, err := client.Get(server.URL + "/m/" + code)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	<-handlers
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("meeting page status=%d", resp.StatusCode)
	}
	for _, c := range jar.Cookies(base) {
		if c.Name == auth.UserCookie {
			t.Fatal("revoked session cookie kept")
		}
	}

	dialer := websocket.Dialer{Jar: jar}
	ws, _, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = ws.Close()
		select {
		case <-handlers:
		case <-time.After(5 * time.Second):
			t.Error("WebSocket handler did not stop")
		}
	}()
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	var hello map[string]any
	if err := ws.ReadJSON(&hello); err != nil {
		t.Fatalf("socket closed instead of greeting a guest: %v", err)
	}
	if hello["type"] != "hello" || hello["me"] != nil {
		t.Fatalf("hello=%v", hello)
	}
}

// Alex Meet's login shares the per-IP password budget with the other two
// servers: wrong passwords are refused with 429 after the burst, and a
// correct password does not use up the budget.
func TestLoginRateLimitedPerIP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	meetDatabase(t)
	auth.LoginLimiter.Reset()
	t.Cleanup(auth.LoginLimiter.Reset)
	hash, err := auth.HashPassword("password123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateUser("alice", hash, "Alice", "approved", false); err != nil {
		t.Fatal(err)
	}
	r := NewEngine()
	login := func(remote, password string) *httptest.ResponseRecorder {
		body := `{"username":"alice","password":"` + password + `"}`
		req := httptest.NewRequest("POST", "/api/login", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = remote
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	for i := 0; i < 15; i++ {
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
}

func TestMeetLoginShellHasCacheAndFrameHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	meetDatabase(t)
	w := httptest.NewRecorder()
	NewEngine().ServeHTTP(w, httptest.NewRequest("GET", "/login", nil))
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-cache" || w.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("status=%d cc=%q xfo=%q", w.Code, w.Header().Get("Cache-Control"), w.Header().Get("X-Frame-Options"))
	}
}
