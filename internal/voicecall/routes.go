package voicecall

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/auth"
	"alexmessage/internal/db"
	"alexmessage/internal/httpx"
)

// vcUser is the voice-call public user view (no bio, unlike the chat app).
type vcUser struct {
	ID          int    `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

func userPublic(u *db.User) vcUser {
	return vcUser{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName}
}

type vcServer struct {
	indexHTML string
	loginHTML string
}

// NewEngine builds the fully wired Gin engine for the voice-call server.
func NewEngine() *gin.Engine {
	s := &vcServer{
		indexHTML: loadHTML("voicecall.html"),
		loginHTML: loadHTML("voicecall_login.html"),
	}

	r := gin.New()
	r.Use(gin.Recovery())

	if dir := filepath.Join(db.BaseDir, "fonts"); isDir(dir) {
		mountStatic(r, "/fonts", dir)
	}
	if dir := filepath.Join(db.BaseDir, "sound"); isDir(dir) {
		mountStatic(r, "/sound", dir)
	}

	r.GET("/", s.handleRoot)
	r.GET("/login", s.handleLoginPage)
	r.POST("/api/login", handleLogin)
	r.POST("/api/logout", handleLogout)
	r.GET("/api/me", handleMe)
	r.GET("/api/users/lookup", handleUsersLookup)
	r.GET("/api/calls/recent", handleRecentCalls)
	r.GET("/ws", handleWS)

	return r
}

// ---------- HTML routes ----------

func (s *vcServer) handleRoot(c *gin.Context) {
	user := auth.ResolveSession(httpx.Cookie(c, auth.UserCookie), "user")
	if user == nil {
		c.Redirect(http.StatusFound, "/login")
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(s.indexHTML))
}

func (s *vcServer) handleLoginPage(c *gin.Context) {
	user := auth.ResolveSession(httpx.Cookie(c, auth.UserCookie), "user")
	if user != nil {
		c.Redirect(http.StatusFound, "/")
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(s.loginHTML))
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

func handleUsersLookup(c *gin.Context) {
	user, ok := requireUser(c)
	if !ok {
		return
	}
	target, _ := db.GetUserByUsername(auth.NormalizeUsername(c.Query("username")))
	if target == nil || target.Status != "approved" {
		httpx.Error(c, http.StatusNotFound, "No user with that username")
		return
	}
	if target.ID == user.ID {
		httpx.Error(c, http.StatusBadRequest, "That's you")
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": userPublic(target)})
}

// recentCall embeds db.Call and adds the resolved "other" party.
type recentCall struct {
	db.Call
	Peer *vcUser `json:"peer"`
}

func handleRecentCalls(c *gin.Context) {
	user, ok := requireUser(c)
	if !ok {
		return
	}
	rows, err := db.ListRecentCalls(user.ID, 50)
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}

	// otherID for a row: the callee for outgoing calls, else the caller.
	otherOf := func(r db.Call) *int {
		if r.Direction == "outgoing" {
			return r.CalleeID
		}
		return r.CallerID
	}

	others := map[int]vcUser{}
	for _, r := range rows {
		oid := otherOf(r)
		if oid == nil {
			continue
		}
		if _, seen := others[*oid]; seen {
			continue
		}
		if u, _ := db.GetUserByID(*oid); u != nil {
			others[*oid] = userPublic(u)
		}
	}

	enriched := make([]recentCall, 0, len(rows))
	for _, r := range rows {
		var peer *vcUser
		if oid := otherOf(r); oid != nil {
			if u, found := others[*oid]; found {
				p := u
				peer = &p
			}
		}
		enriched = append(enriched, recentCall{Call: r, Peer: peer})
	}
	c.JSON(http.StatusOK, gin.H{"calls": enriched})
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

func trimSpace(s string) string { return strings.TrimSpace(s) }

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
