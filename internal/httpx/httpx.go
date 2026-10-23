// Package httpx holds tiny HTTP helpers shared by all three Alex Messages
// servers: FastAPI-compatible error responses, session-cookie handling, and
// static file mounts without directory listings.
package httpx

import (
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

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
	c.SetCookie(name, token, auth.SessionTTLSeconds, "/", "", secureRequest(c.Request), true)
}

// ClearSessionCookie expires a session cookie.
func ClearSessionCookie(c *gin.Context, name string) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(name, "", -1, "/", "", secureRequest(c.Request), true)
}

// SessionToken accepts bearer credentials before the browser cookie. Query
// credentials are only accepted by callers that explicitly allow them.
func SessionToken(c *gin.Context, allowQuery bool) string {
	if h := c.GetHeader("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	if token := Cookie(c, auth.UserCookie); token != "" {
		return token
	}
	if allowQuery {
		return c.Query("token")
	}
	return ""
}

func secureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	// Production nginx terminates TLS and connects from loopback. Do not let
	// a public HTTP caller forge the proxy's HTTPS header.
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback() && r.Header.Get("X-Forwarded-Proto") == "https"
}

// WebSocketOrigin allows clients that send no Origin (the Android ring
// service's call listener), same-origin connections (browsers, and the React
// Native app, whose WebSocket sends its server URL's origin), and the exact
// origins listed in WS_ALLOWED_ORIGINS.
func WebSocketOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	scheme := "http"
	if secureRequest(r) {
		scheme = "https"
	}
	if strings.EqualFold(u.Host, r.Host) && u.Scheme == scheme {
		return true
	}
	for _, allowed := range strings.Split(os.Getenv("WS_ALLOWED_ORIGINS"), ",") {
		if origin == strings.TrimSpace(allowed) {
			return true
		}
	}
	return false
}

// Cookie returns the named cookie's value, or "" when absent.
func Cookie(c *gin.Context, name string) string {
	v, err := c.Cookie(name)
	if err != nil {
		return ""
	}
	return v
}

// NoListFS wraps an http.FileSystem to 404 directory requests, matching
// FastAPI's StaticFiles (which never serves directory listings). Every
// server's static mounts go through it, so no host lists its files.
type NoListFS struct{ FS http.FileSystem }

func (n NoListFS) Open(name string) (http.File, error) {
	f, err := n.FS.Open(name)
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
