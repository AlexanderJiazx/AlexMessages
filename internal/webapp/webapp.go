// Package webapp is the user-facing Alex Messages server: the Gin engine, static
// mounts, the cookie-authenticated request helpers, and every route module
// that the Python `routes/` package contained. It mirrors server.py.
package webapp

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/auth"
	"alexmessage/internal/db"
	"alexmessage/internal/debuglog"
	"alexmessage/internal/httpx"
)

// Server holds the loaded HTML shells and serves the engine.
type Server struct {
	indexHTML string
	loginHTML string
	// spaHTML is the built React client (web/dist/index.html). Empty when the
	// client hasn't been built — the legacy static frontend is served then.
	spaHTML string
}

// NewEngine builds the fully wired Gin engine for the user app.
func NewEngine() *gin.Engine {
	s := &Server{
		indexHTML: loadHTML("index.html"),
		loginHTML: loadHTML("login.html"),
		spaHTML:   loadHTMLOptional(filepath.Join("web", "dist", "index.html")),
	}

	r := gin.New()
	r.Use(gin.Recovery())

	// The service worker must be served from the root scope so it can intercept
	// push events for the whole app. We keep the source under static/.
	r.GET("/sw.js", func(c *gin.Context) {
		c.Header("Cache-Control", "no-cache")
		c.Header("Service-Worker-Allowed", "/")
		c.File(filepath.Join(db.BaseDir, "static", "sw.js"))
	})

	// Per-user upload dirs sit under data/uploads/<uid>/...; mount the root.
	mountStatic(r, "/uploads", db.UploadRoot)
	// Profile photos live flat under data/avatars/.
	mountStatic(r, "/avatars", db.AvatarRoot)
	if dir := filepath.Join(db.BaseDir, "fonts"); isDir(dir) {
		mountStatic(r, "/fonts", dir)
	}
	if dir := filepath.Join(db.BaseDir, "static"); isDir(dir) {
		mountStatic(r, "/static", dir)
	}
	// The built React client's hashed bundles (Vite emits web/dist/assets/).
	if s.spaHTML != "" {
		mountStatic(r, "/assets", filepath.Join(db.BaseDir, "web", "dist", "assets"))
	}

	// Route modules, grouped by concern (mirrors the Python routers).
	s.registerPages(r)
	registerAuthRoutes(r)
	registerMeRoutes(r)
	registerAccountRoutes(r)
	registerAvatarRoutes(r)
	registerUserRoutes(r)
	registerUploadRoutes(r)
	registerPushRoutes(r)
	registerDMStateRoutes(r)
	registerHistoryRoutes(r)
	registerLinkPreviewRoutes(r)
	registerTranscribeRoutes(r)
	registerWS(r)

	// Clients stream real-time actions to the centralized debug console (which
	// lives in the admin panel and reads the shared debug_events table).
	r.POST("/api/debug/report", debuglog.ReportHandler("messages"))

	return r
}

// requireUser resolves the user-scope session or aborts with 401. The browser
// client authenticates with the HttpOnly session cookie; the React Native
// client (which has no cookie jar) sends the same session token as
// `Authorization: Bearer <token>` — /api/login returns it as session_token.
func requireUser(c *gin.Context) (*db.User, bool) {
	user := auth.ResolveSession(requestSessionToken(c), "user")
	if user == nil {
		httpx.Error(c, http.StatusUnauthorized, "not authenticated")
		return nil, false
	}
	return user, true
}

// requestSessionToken extracts the session token from the cookie, an
// Authorization: Bearer header, or a ?token= query param (the last is how the
// native client authenticates the WebSocket upgrade, which can't set headers
// on every platform).
func requestSessionToken(c *gin.Context) string {
	if tok := httpx.Cookie(c, auth.UserCookie); tok != "" {
		return tok
	}
	if h := c.GetHeader("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(h[len("Bearer "):])
	}
	return c.Query("token")
}

// ---------- static helpers ----------

// noListFS wraps an http.FileSystem to 404 directory requests, matching
// FastAPI's StaticFiles (which never serves directory listings).
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

func loadHTML(name string) string {
	b, err := os.ReadFile(filepath.Join(db.BaseDir, name))
	if err != nil {
		return "<h1>" + name + " missing</h1>"
	}
	return string(b)
}

// loadHTMLOptional is loadHTML for assets that legitimately may not exist
// (the built React client): missing files yield "" rather than an error page.
func loadHTMLOptional(name string) string {
	b, err := os.ReadFile(filepath.Join(db.BaseDir, name))
	if err != nil {
		return ""
	}
	return string(b)
}
