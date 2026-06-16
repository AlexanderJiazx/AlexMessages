package webapp

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/auth"
	"alexmessage/internal/db"
	"alexmessage/internal/httpx"
	"alexmessage/internal/runtime"
)

// registerAuthRoutes wires register/login/logout (mirrors routes/auth_routes.py).
func registerAuthRoutes(r *gin.Engine) {
	r.POST("/api/register", handleRegister)
	r.POST("/api/login", handleLogin)
	r.POST("/api/logout", handleLogout)
}

// resolveDisplayName mirrors `(payload.get("display_name") or username).strip()[:40] or username`.
func resolveDisplayName(raw, username string) string {
	dn := firstNonEmpty(raw, username)
	dn = truncateRunes(trimSpace(dn), 40)
	return firstNonEmpty(dn, username)
}

func handleRegister(c *gin.Context) {
	m := bindJSON(c)
	username := auth.NormalizeUsername(strField(m, "username"))
	password := strField(m, "password")
	displayName := resolveDisplayName(strField(m, "display_name"), username)

	if !auth.ValidUsername(username) {
		httpx.Error(c, http.StatusBadRequest, "Username must be 3–32 chars (letters, digits, . _ -)")
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
	uid, err := db.CreateUser(username, hash, displayName, "pending", false)
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"id":      uid,
		"status":  "pending",
		"message": "Registration submitted. An admin must approve your account.",
	})
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
	c.JSON(http.StatusOK, gin.H{"ok": true, "user": runtime.UserPublic(user)})
}

func handleLogout(c *gin.Context) {
	token := httpx.Cookie(c, auth.UserCookie)
	// Sign out everywhere: a valid session drops all of the user's sessions
	// (matching the "log out from all of your devices" action); fall back to
	// dropping just this token when the session can't be resolved.
	if user := auth.ResolveSession(token, "user"); user != nil {
		_ = db.DeleteUserSessions(user.ID)
	} else if token != "" {
		_ = db.DeleteSession(token)
	}
	httpx.ClearSessionCookie(c, auth.UserCookie)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
