package webapp

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/auth"
	"alexmessage/internal/httpx"
)

// registerPages serves the HTML shells for / and /login (mirrors routes/pages.py).
func (s *Server) registerPages(r *gin.Engine) {
	r.GET("/", func(c *gin.Context) {
		user := auth.ResolveSession(httpx.Cookie(c, auth.UserCookie), "user")
		if user == nil {
			c.Redirect(http.StatusFound, "/login")
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(s.indexHTML))
	})

	r.GET("/login", func(c *gin.Context) {
		user := auth.ResolveSession(httpx.Cookie(c, auth.UserCookie), "user")
		if user != nil {
			c.Redirect(http.StatusFound, "/")
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(s.loginHTML))
	})
}
