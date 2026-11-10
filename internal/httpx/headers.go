package httpx

import (
	"net/http"
	"path"

	"github.com/gin-gonic/gin"
)

// SecurityHeaders sets the headers every response of the three servers
// carries. No page of this project is shown in a frame, so framing is denied.
// A handler that sets one of these later keeps its own value (c.Header replaces).
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Next()
	}
}

// Static serves the files under dir at prefix for GET and HEAD, never a
// directory listing. A found file carries Cache-Control cacheControl (none
// when it is ""); a 404 never carries one, so a proxy cannot cache the 404.
func Static(r gin.IRoutes, prefix, dir, cacheControl string) {
	fsys := NoListFS{FS: http.Dir(dir)}
	handler := func(c *gin.Context) {
		name := c.Param("filepath")
		f, err := fsys.Open(name)
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		if cacheControl != "" {
			c.Header("Cache-Control", cacheControl)
		}
		http.ServeContent(c.Writer, c.Request, path.Base(name), info.ModTime(), f)
	}
	r.GET(prefix+"/*filepath", handler)
	r.HEAD(prefix+"/*filepath", handler)
}
