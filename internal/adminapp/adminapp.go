// Package adminapp is the AlexMessage admin control panel (mirrors admin.py).
//
// It runs as a separate process on its own port, shares the same SQLite
// database and uploads as the user app, and is cookie-authenticated with admin
// scope (a distinct cookie name so an admin session never grants user-app
// access).
package adminapp

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/auth"
	"alexmessage/internal/db"
	"alexmessage/internal/httpx"
)

type server struct {
	adminHTML string
}

// NewEngine builds the fully wired Gin engine for the admin panel.
func NewEngine() *gin.Engine {
	s := &server{adminHTML: loadAdminHTML()}

	r := gin.New()
	r.Use(gin.Recovery())

	// The admin page shows a login form when no cookie; its JS handles both states.
	r.GET("/", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(s.adminHTML))
	})

	r.POST("/api/login", handleLogin)
	r.POST("/api/logout", handleLogout)
	r.GET("/api/me", handleMe)

	r.GET("/api/users", handleListUsers)
	r.POST("/api/users", handleCreateUser)
	r.POST("/api/users/:id/approve", handleApprove)
	r.POST("/api/users/:id/reject", handleReject)
	r.POST("/api/users/:id/disable", handleDisable)
	r.POST("/api/users/:id/enable", handleEnable)
	r.PATCH("/api/users/:id", handlePatchUser)
	r.POST("/api/users/:id/password", handleSetPassword)
	r.DELETE("/api/users/:id", handleDeleteUser)

	r.GET("/api/stats", handleStats)
	r.POST("/api/purge_messages", handlePurgeMessages)

	return r
}

// ---------- auth ----------

// currentAdmin resolves the admin-scope cookie or aborts with 401. (As in the
// Python original, resolve_session already rejects non-admins, so the explicit
// 403 branch is unreachable and collapses into this 401.)
func currentAdmin(c *gin.Context) (*db.User, bool) {
	user := auth.ResolveSession(httpx.Cookie(c, auth.AdminCookie), "admin")
	if user == nil {
		httpx.Error(c, http.StatusUnauthorized, "admin login required")
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
		httpx.Error(c, http.StatusUnauthorized, "Invalid credentials")
		return
	}
	if !user.IsAdmin {
		httpx.Error(c, http.StatusForbidden, "Not an administrator")
		return
	}
	if user.Status != "approved" {
		httpx.Error(c, http.StatusForbidden, "Account "+user.Status)
		return
	}
	token, _, err := auth.IssueSession(user.ID, "admin")
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	httpx.SetSessionCookie(c, auth.AdminCookie, token)
	c.JSON(http.StatusOK, gin.H{"ok": true, "user": gin.H{"id": user.ID, "username": user.Username}})
}

func handleLogout(c *gin.Context) {
	if token := httpx.Cookie(c, auth.AdminCookie); token != "" {
		_ = db.DeleteSession(token)
	}
	httpx.ClearSessionCookie(c, auth.AdminCookie)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func handleMe(c *gin.Context) {
	admin, ok := currentAdmin(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": db.UserToPublic(admin)})
}

// ---------- user management ----------

func handleListUsers(c *gin.Context) {
	if _, ok := currentAdmin(c); !ok {
		return
	}
	rows, err := db.ListUsers(c.Query("status"))
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	out := []map[string]any{}
	for _, r := range rows {
		out = append(out, db.UserToPublic(r))
	}
	c.JSON(http.StatusOK, gin.H{"users": out})
}

func handleCreateUser(c *gin.Context) {
	if _, ok := currentAdmin(c); !ok {
		return
	}
	m := bindJSON(c)
	username := auth.NormalizeUsername(strField(m, "username"))
	password := strField(m, "password")
	displayName := resolveDisplayName(strField(m, "display_name"), username)
	isAdmin := toBool(m["is_admin"])

	if !auth.ValidUsername(username) {
		httpx.Error(c, http.StatusBadRequest, "Invalid username")
		return
	}
	if !auth.ValidPassword(password) {
		httpx.Error(c, http.StatusBadRequest, "Password must be 8–128 chars")
		return
	}
	if existing, _ := db.GetUserByUsername(username); existing != nil {
		httpx.Error(c, http.StatusConflict, "Username already taken")
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	uid, err := db.CreateUser(username, hash, displayName, "approved", isAdmin)
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	fresh, _ := db.GetUserByID(uid)
	c.JSON(http.StatusOK, gin.H{"user": db.UserToPublic(fresh)})
}

func handleApprove(c *gin.Context) {
	admin, ok := currentAdmin(c)
	if !ok {
		return
	}
	_ = admin
	userID, ok := pathID(c)
	if !ok {
		return
	}
	user, _ := db.GetUserByID(userID)
	if user == nil {
		httpx.Error(c, http.StatusNotFound, "User not found")
		return
	}
	if err := db.UpdateUserStatus(userID, "approved"); err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	fresh, _ := db.GetUserByID(userID)
	c.JSON(http.StatusOK, gin.H{"user": db.UserToPublic(fresh)})
}

func handleReject(c *gin.Context) {
	if _, ok := currentAdmin(c); !ok {
		return
	}
	userID, ok := pathID(c)
	if !ok {
		return
	}
	user, _ := db.GetUserByID(userID)
	if user == nil {
		httpx.Error(c, http.StatusNotFound, "User not found")
		return
	}
	_ = db.UpdateUserStatus(userID, "rejected")
	_ = db.DeleteUserSessions(userID)
	fresh, _ := db.GetUserByID(userID)
	c.JSON(http.StatusOK, gin.H{"user": db.UserToPublic(fresh)})
}

func handleDisable(c *gin.Context) {
	if _, ok := currentAdmin(c); !ok {
		return
	}
	userID, ok := pathID(c)
	if !ok {
		return
	}
	_ = db.UpdateUserStatus(userID, "disabled")
	_ = db.DeleteUserSessions(userID)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func handleEnable(c *gin.Context) {
	if _, ok := currentAdmin(c); !ok {
		return
	}
	userID, ok := pathID(c)
	if !ok {
		return
	}
	_ = db.UpdateUserStatus(userID, "approved")
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func handlePatchUser(c *gin.Context) {
	admin, ok := currentAdmin(c)
	if !ok {
		return
	}
	userID, ok := pathID(c)
	if !ok {
		return
	}
	user, _ := db.GetUserByID(userID)
	if user == nil {
		httpx.Error(c, http.StatusNotFound, "User not found")
		return
	}
	m := bindJSON(c)

	var displayName *string
	if raw, isStr := isStr(m, "display_name"); isStr {
		v := truncateRunes(strings.TrimSpace(raw), 40)
		if v == "" {
			v = user.DisplayName
		}
		displayName = &v
	}
	var bio *string
	if raw, isStr := isStr(m, "bio"); isStr {
		v := truncateRunes(strings.TrimSpace(raw), 280)
		bio = &v
	}
	if err := db.UpdateUserProfile(userID, displayName, bio); err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}

	if _, present := m["is_admin"]; present {
		wants := toBool(m["is_admin"])
		// Don't let the last admin demote themselves.
		if !wants && admin.ID == userID {
			if n, _ := db.CountAdmins(); n <= 1 {
				httpx.Error(c, http.StatusBadRequest, "Refusing to demote the last administrator")
				return
			}
		}
		_ = db.SetAdmin(userID, wants)
	}
	fresh, _ := db.GetUserByID(userID)
	c.JSON(http.StatusOK, gin.H{"user": db.UserToPublic(fresh)})
}

func handleSetPassword(c *gin.Context) {
	if _, ok := currentAdmin(c); !ok {
		return
	}
	userID, ok := pathID(c)
	if !ok {
		return
	}
	m := bindJSON(c)
	newPassword := strField(m, "password")
	if !auth.ValidPassword(newPassword) {
		httpx.Error(c, http.StatusBadRequest, "Password must be 8–128 chars")
		return
	}
	user, _ := db.GetUserByID(userID)
	if user == nil {
		httpx.Error(c, http.StatusNotFound, "User not found")
		return
	}
	hash, err := auth.HashPassword(newPassword)
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	_ = db.SetPassword(userID, hash)
	_ = db.DeleteUserSessions(userID)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func handleDeleteUser(c *gin.Context) {
	if _, ok := currentAdmin(c); !ok {
		return
	}
	userID, ok := pathID(c)
	if !ok {
		return
	}
	user, _ := db.GetUserByID(userID)
	if user == nil {
		httpx.Error(c, http.StatusNotFound, "User not found")
		return
	}
	if user.IsAdmin {
		if n, _ := db.CountAdmins(); n <= 1 {
			httpx.Error(c, http.StatusBadRequest, "Refusing to delete the last administrator")
			return
		}
	}
	_ = db.DeleteUserSessions(userID)
	_ = db.DeleteUser(userID)         // cascades messages/attachments/contacts via FK
	db.DeleteUserUploads(userID)      // remove storage directory on disk
	db.DeleteUserAvatarFiles(userID)  // remove profile photo files on disk
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---------- global operations ----------

func handleStats(c *gin.Context) {
	if _, ok := currentAdmin(c); !ok {
		return
	}
	stats, err := db.Stats()
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	c.JSON(http.StatusOK, stats)
}

func handlePurgeMessages(c *gin.Context) {
	if _, ok := currentAdmin(c); !ok {
		return
	}
	if err := db.PurgeMessages(); err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---------- helpers ----------

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

func isStr(m map[string]any, key string) (string, bool) {
	v, ok := m[key].(string)
	return v, ok
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// resolveDisplayName mirrors `(payload.get("display_name") or username).strip()[:40] or username`.
func resolveDisplayName(raw, username string) string {
	dn := raw
	if dn == "" {
		dn = username
	}
	dn = truncateRunes(strings.TrimSpace(dn), 40)
	if dn == "" {
		dn = username
	}
	return dn
}

// toBool mirrors Python's bool(x) for the values that arrive over JSON.
func toBool(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		return x != ""
	case nil:
		return false
	default:
		return true
	}
}

// pathID parses the :id path parameter, aborting with 404 on a bad value.
func pathID(c *gin.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		httpx.Error(c, http.StatusNotFound, "User not found")
		return 0, false
	}
	return id, true
}

func loadAdminHTML() string {
	p := filepath.Join(db.BaseDir, "admin.html")
	b, err := os.ReadFile(p)
	if err != nil {
		return "<h1>admin.html missing</h1>"
	}
	return string(b)
}
