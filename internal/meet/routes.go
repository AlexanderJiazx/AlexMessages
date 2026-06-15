package meet

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/auth"
	"alexmessage/internal/db"
	"alexmessage/internal/debuglog"
	"alexmessage/internal/httpx"
)

// meetUser is the Alex Meet public user view (no bio, unlike the chat app, but
// with the avatar URL so meeting tiles can show profile photos). Guests carry
// a synthetic identity: a negative id (unique per participant, so avatar
// colors stay distinct) and guest=true.
type meetUser struct {
	ID          int    `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Avatar      string `json:"avatar"`
	Guest       bool   `json:"guest,omitempty"`
}

func userPublic(u *db.User) meetUser {
	return meetUser{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Avatar: u.Avatar}
}

type meetServer struct {
	meetHTML  string
	loginHTML string
}

// NewEngine builds the fully wired Gin engine for the Alex Meet server.
func NewEngine() *gin.Engine {
	s := &meetServer{
		meetHTML:  loadHTML("meet.html"),
		loginHTML: loadHTML("meet_login.html"),
	}

	r := gin.New()
	r.Use(gin.Recovery())

	if dir := filepath.Join(db.BaseDir, "fonts"); isDir(dir) {
		mountStatic(r, "/fonts", dir)
	}
	if dir := filepath.Join(db.BaseDir, "sound"); isDir(dir) {
		mountStatic(r, "/sound", dir)
	}
	if dir := filepath.Join(db.BaseDir, "static"); isDir(dir) {
		mountStatic(r, "/static", dir)
	}
	// Profile photos are written by the main app; serve them here too so
	// meeting tiles can use the same /avatars/... URLs.
	mountStatic(r, "/avatars", db.AvatarRoot)

	r.GET("/", s.handleRoot)
	r.GET("/m/:code", s.handleMeetingPage)
	r.GET("/login", s.handleLoginPage)
	r.POST("/api/login", handleLogin)
	r.POST("/api/logout", handleLogout)
	r.GET("/api/me", handleMe)
	r.POST("/api/meetings", handleCreateMeeting)
	r.GET("/api/meetings/:code", handleMeetingInfo)
	r.GET("/ws", handleWS)

	// Clients stream real-time actions here; the centralized debug console
	// lives in the admin panel (reads the shared debug_events table).
	r.POST("/api/debug/report", debuglog.ReportHandler("meet"))

	return r
}

// ---------- VolcEngine RTC configuration ----------

// Credentials are supplied at runtime via the VOLC_RTC_APP_ID / VOLC_RTC_APP_KEY
// environment variables — no defaults are baked into the source. The app key is
// an HMAC signing secret and must never be committed. When unset, the volc
// backend simply can't mint join tokens (the mesh backend is unaffected).
func volcAppID() string  { return envOr("VOLC_RTC_APP_ID", "") }
func volcAppKey() string { return envOr("VOLC_RTC_APP_KEY", "") }

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// volcJoinPayload mints the per-participant join credentials for a VolcEngine
// meeting. The VolcEngine user id is "p<pid>" so clients can map media streams
// back to roster entries.
func volcJoinPayload(roomID string, pid int) gin.H {
	uid := "p" + strconv.Itoa(pid)
	exp := uint32(time.Now().Add(24 * time.Hour).Unix())
	t := newVolcToken(volcAppID(), volcAppKey(), roomID, uid)
	t.expireTime(exp)
	t.addPrivilege(volcPrivPublishStream, exp)
	t.addPrivilege(volcPrivSubscribeStream, exp)
	return gin.H{
		"app_id":  volcAppID(),
		"room_id": roomID,
		"user_id": uid,
		"token":   t.serialize(),
	}
}

// ---------- HTML routes ----------

func (s *meetServer) handleRoot(c *gin.Context) {
	user := auth.ResolveSession(httpx.Cookie(c, auth.UserCookie), "user")
	if user == nil {
		c.Redirect(http.StatusFound, "/login")
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(s.meetHTML))
}

func (s *meetServer) handleMeetingPage(c *gin.Context) {
	user := auth.ResolveSession(httpx.Cookie(c, auth.UserCookie), "user")
	if user == nil {
		// Guests may load the page when the meeting allows them; everyone
		// else is sent back to this meeting after signing in.
		code := strings.ToLower(strings.TrimSpace(c.Param("code")))
		meetMu.Lock()
		rm := rooms[code]
		allowGuests := rm != nil && rm.allowGuests
		meetMu.Unlock()
		if !allowGuests {
			c.Redirect(http.StatusFound, "/login?next="+url.QueryEscape(c.Request.URL.Path))
			return
		}
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(s.meetHTML))
}

func (s *meetServer) handleLoginPage(c *gin.Context) {
	user := auth.ResolveSession(httpx.Cookie(c, auth.UserCookie), "user")
	if user != nil {
		c.Redirect(http.StatusFound, safeNext(c.Query("next")))
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(s.loginHTML))
}

// safeNext only allows same-site paths so /login?next= can't open-redirect.
func safeNext(next string) string {
	if strings.HasPrefix(next, "/") && !strings.HasPrefix(next, "//") {
		return next
	}
	return "/"
}

// ---------- auth ----------

func requireUser(c *gin.Context) (*db.User, bool) {
	user := auth.ResolveSession(httpx.Cookie(c, auth.UserCookie), "user")
	if user == nil {
		httpx.Error(c, http.StatusUnauthorized, "not authenticated")
		return nil, false
	}
	return user, true
}

func handleLogin(c *gin.Context) {
	m := bindJSON(c)
	username := auth.NormalizeUsername(strField(m, "username"))
	password := strField(m, "password")

	user, _ := db.GetUserByUsername(username)
	if user == nil || !auth.VerifyPassword(password, user.PasswordHash) {
		httpx.Error(c, http.StatusUnauthorized, "Invalid username or password")
		return
	}
	if user.Status == "pending" {
		httpx.Error(c, http.StatusForbidden, "Your account is awaiting admin approval")
		return
	}
	if user.Status != "approved" {
		httpx.Error(c, http.StatusForbidden, "Account "+user.Status)
		return
	}
	token, _, err := auth.IssueSession(user.ID, "user")
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	httpx.SetSessionCookie(c, auth.UserCookie, token)
	c.JSON(http.StatusOK, gin.H{"ok": true, "user": userPublic(user)})
}

func handleLogout(c *gin.Context) {
	if token := httpx.Cookie(c, auth.UserCookie); token != "" {
		_ = db.DeleteSession(token)
	}
	httpx.ClearSessionCookie(c, auth.UserCookie)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func handleMe(c *gin.Context) {
	user, ok := requireUser(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": userPublic(user), "is_admin": user.IsAdmin})
}

// ---------- meetings ----------

func handleCreateMeeting(c *gin.Context) {
	if _, ok := requireUser(c); !ok {
		return
	}
	m := bindJSON(c)
	mode := strField(m, "mode")
	if mode != modeVolc {
		mode = modeMesh
	}
	meetMu.Lock()
	pruneRoomsLocked()
	code := newRoomCode()
	rooms[code] = &room{
		code:      code,
		mode:      mode,
		parts:     map[int]*participant{},
		createdAt: db.NowTS(),
	}
	meetMu.Unlock()
	debuglog.Emit("meet", "info", "meeting_created", "Meeting created", map[string]any{"code": code, "mode": mode})
	c.JSON(http.StatusOK, gin.H{"code": code, "mode": mode})
}

// handleMeetingInfo is deliberately public: the meeting page must learn
// allow_guests before deciding between the guest gate and the login redirect.
// It only reveals that a room code exists, its backend, and a head count.
func handleMeetingInfo(c *gin.Context) {
	code := strings.ToLower(strings.TrimSpace(c.Param("code")))
	meetMu.Lock()
	pruneRoomsLocked()
	rm := rooms[code]
	var (
		mode        string
		count       int
		allowGuests bool
	)
	if rm != nil {
		mode = rm.mode
		count = len(rm.parts)
		allowGuests = rm.allowGuests
	}
	meetMu.Unlock()
	if rm == nil {
		httpx.Error(c, http.StatusNotFound, "Meeting not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": code, "mode": mode, "participants": count, "allow_guests": allowGuests})
}

// ---------- shared small helpers ----------

func bindJSON(c *gin.Context) map[string]any {
	var m map[string]any
	_ = c.ShouldBindJSON(&m)
	if m == nil {
		m = map[string]any{}
	}
	return m
}

func strField(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func loadHTML(name string) string {
	b, err := os.ReadFile(filepath.Join(db.BaseDir, name))
	if err != nil {
		return "<h1>" + name + " missing</h1>"
	}
	return string(b)
}

type noListFS struct{ fs http.FileSystem }

func (n noListFS) Open(name string) (http.File, error) {
	f, err := n.fs.Open(name)
	if err != nil {
		return nil, err
	}
	stat, err := f.Stat()
	if err == nil && stat.IsDir() {
		_ = f.Close()
		return nil, os.ErrNotExist
	}
	return f, nil
}

func mountStatic(r *gin.Engine, prefix, dir string) {
	r.StaticFS(prefix, noListFS{http.Dir(dir)})
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
