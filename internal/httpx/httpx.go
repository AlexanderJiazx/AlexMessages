// Package httpx holds tiny HTTP helpers shared by all three Alex Messages
// servers: FastAPI-compatible error responses and session-cookie handling.
package httpx

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/auth"
)

// Error aborts the request with the FastAPI-compatible {"detail": msg} body so
// the existing frontend's `data.detail` reads keep working unchanged.
func Error(c *gin.Context, status int, detail string) {
	c.AbortWithStatusJSON(status, gin.H{"detail": detail})
}

// SetSessionCookie writes a session cookie (HttpOnly, SameSite=Lax, path=/).
func SetSessionCookie(c *gin.Context, name, token string) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(name, token, auth.SessionTTLSeconds, "/", "", false, true)
}

// ClearSessionCookie expires a session cookie.
func ClearSessionCookie(c *gin.Context, name string) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(name, "", -1, "/", "", false, true)
}

// Cookie returns the named cookie's value, or "" when absent.
func Cookie(c *gin.Context, name string) string {
	v, err := c.Cookie(name)
	if err != nil {
		return ""
	}
	return v
}
