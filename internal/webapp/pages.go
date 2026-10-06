package webapp

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/auth"
	"alexmessage/internal/httpx"
)

// registerPages serves the app shell for / and /login (mirrors routes/pages.py).
// When the React client has been built (web/dist/index.html present) it is
// served for both paths — the SPA renders the login screen or the chat app and
// enforces auth client-side as well. Otherwise the legacy static shells are
// served, preserving the pre-React frontend as a fallback.
func (s *Server) registerPages(r *gin.Engine) {
	r.GET("/", func(c *gin.Context) {
		user := auth.ResolveSession(httpx.Cookie(c, auth.UserCookie), "user")
		if user == nil {
			c.Redirect(http.StatusFound, "/login")
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(s.shellHTML()))
	})

	r.GET("/login", func(c *gin.Context) {
		user := auth.ResolveSession(httpx.Cookie(c, auth.UserCookie), "user")
		if user != nil {
			c.Redirect(http.StatusFound, "/")
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(s.loginHTMLFor(c)))
	})
}

// shellHTML is the authenticated app shell — the React SPA when built.
func (s *Server) shellHTML() string {
	if s.spaHTML != "" {
		return s.spaHTML
	}
	return s.indexHTML
}

// loginHTMLFor is the anonymous shell — the React SPA handles both routes, so
// it wins when present; the legacy login page is the fallback.
func (s *Server) loginHTMLFor(c *gin.Context) string {
	if s.spaHTML != "" {
		return s.spaHTML
	}
	return s.loginHTML
}
