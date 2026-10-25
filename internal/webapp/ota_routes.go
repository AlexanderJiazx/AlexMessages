package webapp

import (
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/db"
	"alexmessage/internal/debuglog"
	"alexmessage/internal/httpx"
	"alexmessage/internal/ota"
	"alexmessage/internal/ota/census"
)

// OTA updates of the mobile app (OTAUpdatePlan.md, D2): the Expo Updates
// manifest endpoint, the update assets, the native-build policy, and the /app
// install page with its downloads. None of these needs a session: neither
// expo-updates nor a browser downloading an APK sends one. The server only
// selects bytes that were signed on the publisher's Mac; it holds no key.
func registerOTARoutes(r *gin.Engine) {
	r.GET("/api/ota/manifest", handleOTAManifest)
	r.GET("/ota/assets/*name", handleOTAAsset)
	r.HEAD("/ota/assets/*name", handleOTAAsset)
	r.GET("/api/app/latest", handleAppLatest)
	r.GET("/app", handleAppPage)
	r.GET("/app/*file", handleAppFile)
	r.HEAD("/app/*file", handleAppFile)
}

// otaStore is the update store: $OTA_DIR, or data/ota.
func otaStore() *ota.Store {
	return &ota.Store{Dir: ota.DirFromEnv(db.DataDir)}
}

func otaCensusPath() string {
	return filepath.Join(ota.DirFromEnv(db.DataDir), census.FileName)
}

// PruneOTACensus deletes census rows not seen for 90 days. cmd/server adds
// it to the maintenance sweep.
func PruneOTACensus() {
	cdb, err := census.Shared(otaCensusPath(), false)
	if err == nil && cdb != nil {
		_, err = cdb.Prune(time.Now())
	}
	if err != nil {
		log.Printf("[maintenance] census prune: %v", err)
	}
}

// eventBudget allows at most n events per window, so forged census headers
// or a damaged store cannot flood the debug console.
type eventBudget struct {
	mu     sync.Mutex
	n      int
	window time.Duration
	start  time.Time
	used   int
}

func (b *eventBudget) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	if now.Sub(b.start) >= b.window {
		b.start, b.used = now, 0
	}
	if b.used >= b.n {
		return false
	}
	b.used++
	return true
}

var (
	otaErrorBudget   = &eventBudget{n: 1, window: 10 * time.Minute}
	censusFullBudget = &eventBudget{n: 1, window: time.Hour}
	failureBudget    = &eventBudget{n: 30, window: time.Hour}
)

// otaStoreError reports a damaged update store or census, at most once per
// 10 minutes.
func otaStoreError(err error) {
	if !otaErrorBudget.allow() {
		return
	}
	log.Printf("[ota] %v", err)
	debuglog.Emit("messages", "error", "ota.store_error", "The OTA update store could not be read", map[string]any{"err": err.Error()})
}

// otaRefuse answers an error that no cache may keep: Cloudflare caches 404s
// by extension, and an asset or APK uploaded later must not stay hidden.
func otaRefuse(c *gin.Context, status int, detail string) {
	c.Header("Cache-Control", "no-store")
	httpx.Error(c, status, detail)
}

// handleOTAManifest is the Expo Updates protocol v1 manifest endpoint, the
// URL baked into every binary from build 2 on.
func handleOTAManifest(c *gin.Context) {
	h := c.Writer.Header()
	h.Set("expo-protocol-version", "1")
	h.Set("expo-sfv-version", "0")
	h.Set("Cache-Control", "private, max-age=0, no-store")

	req, err := ota.ParseManifestRequest(c.Request.Header)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	// The kill switch (D9): every phone keeps what it runs.
	if os.Getenv("OTA_DISABLED") == "1" {
		c.Status(http.StatusNoContent)
		return
	}
	store := otaStore()
	sel, err := store.Select(req)
	if errors.Is(err, ota.ErrNotInitialized) {
		c.Status(http.StatusNoContent)
		return
	}
	if err != nil {
		otaStoreError(err)
		httpx.Error(c, http.StatusInternalServerError, "update store error")
		return
	}
	recordCensus(store, req)
	if sel.Parts == nil {
		c.Status(http.StatusNoContent)
		return
	}
	body, contentType, err := ota.EncodeMultipart(sel.Parts, ota.NewBoundary())
	if err != nil {
		otaStoreError(err)
		httpx.Error(c, http.StatusInternalServerError, "update store error")
		return
	}
	c.Data(http.StatusOK, contentType, body)
}

// recordCensus upserts the census row of the requesting install (D8 item 1).
// A failure is logged and never fails the update check.
func recordCensus(store *ota.Store, req *ota.ManifestRequest) {
	if req.ClientID == "" || req.EmbeddedUpdateID == "" {
		return
	}
	cdb, err := census.Shared(filepath.Join(store.Dir, census.FileName), true)
	if err != nil {
		otaStoreError(err)
		return
	}
	res, err := cdb.Record(census.Report{
		ClientID:         req.ClientID,
		Platform:         req.Platform,
		Runtime:          req.Runtime,
		CurrentUpdateID:  req.CurrentUpdateID,
		EmbeddedUpdateID: req.EmbeddedUpdateID,
		Channel:          req.Channel,
		UserID:           req.UserID,
		FailedUpdateIDs:  req.FailedUpdateIDs,
		FatalError:       req.FatalError,
	}, func() (bool, error) {
		return store.IsRegisteredEmbedded(req.Platform, req.Runtime, req.EmbeddedUpdateID)
	}, time.Now())
	if err != nil {
		otaStoreError(err)
		return
	}
	if res.Full && censusFullBudget.allow() {
		debuglog.Emit("messages", "warn", "ota.census_full", fmt.Sprintf("The install census holds %d rows; new installs are not recorded", census.MaxRows), nil)
	}
	if (len(res.NewFailedIDs) > 0 || res.NewFatalError) && failureBudget.allow() {
		ctx := map[string]any{
			"client_id":      req.ClientID,
			"platform":       req.Platform,
			"runtime":        req.Runtime,
			"current_update": req.CurrentUpdateID,
			"failed_updates": res.NewFailedIDs,
			"am_uid":         req.UserID,
		}
		if res.NewFatalError {
			ctx["fatal_error"] = req.FatalError
		}
		debuglog.Emit("messages", "error", "ota.client_failure", "An install reported a failed update (unauthenticated census data)", ctx)
	}
}

// serveOTAFile serves an immutable download. The headers stop an SVG, HTML
// or XML file from rendering on this origin; expo-updates ignores them.
func serveOTAFile(c *gin.Context, path, name, contentType string) {
	f, err := os.Open(path)
	if err != nil {
		otaRefuse(c, http.StatusNotFound, "not found")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		otaRefuse(c, http.StatusNotFound, "not found")
		return
	}
	h := c.Writer.Header()
	h.Set("Content-Type", contentType)
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox")
	h.Set("Content-Disposition", `attachment; filename="`+name+`"`)
	http.ServeContent(c.Writer, c.Request, name, info.ModTime(), f)
}

// handleOTAAsset serves /ota/assets/<sha256-b64url>.<ext>.
func handleOTAAsset(c *gin.Context) {
	name := strings.TrimPrefix(c.Param("name"), "/")
	path, ok := otaStore().AssetFile(name)
	if !ok {
		otaRefuse(c, http.StatusBadRequest, "invalid asset name")
		return
	}
	_, ext, _ := ota.ParseAssetName(name)
	serveOTAFile(c, path, name, ota.AssetContentType(ext))
}

// handleAppFile serves /app/android/AlexMessages-<version>-<build>.apk and
// /app/ios/AlexMessages-<version>-<build>.ipa.
func handleAppFile(c *gin.Context) {
	platform, name, _ := strings.Cut(strings.TrimPrefix(c.Param("file"), "/"), "/")
	path, ok := otaStore().NativeFile(platform, name)
	if !ok {
		otaRefuse(c, http.StatusNotFound, "not found")
		return
	}
	serveOTAFile(c, path, name, ota.NativeContentType(platform))
}

// handleAppLatest is the native-build policy of both platforms (D10).
func handleAppLatest(c *gin.Context) {
	policy, err := otaStore().ReadPolicy()
	if err != nil {
		otaStoreError(err)
		otaRefuse(c, http.StatusInternalServerError, "policy error")
		return
	}
	c.Header("Cache-Control", "no-cache")
	c.JSON(http.StatusOK, policy)
}

// currentPolicy is the native-build policy, or nil when policy.json cannot
// be read or is invalid. A broken policy gates no one.
func currentPolicy() *ota.Policy {
	policy, err := otaStore().ReadPolicy()
	if err != nil {
		otaStoreError(err)
		return nil
	}
	return policy
}

// appPageBuild is one platform's download on the /app page.
type appPageBuild struct {
	Version string
	Build   int
	Path    string
	File    string
	SHA256  string
	SizeMB  string
	Notes   string
}

func pageBuild(platform string, b *ota.NativeBuild) *appPageBuild {
	if b == nil {
		return nil
	}
	file := ota.NativeFileName(platform, b.Version, b.Build)
	return &appPageBuild{
		Version: b.Version,
		Build:   b.Build,
		Path:    "/app/" + platform + "/" + file,
		File:    file,
		SHA256:  b.SHA256,
		SizeMB:  fmt.Sprintf("%.1f MB", float64(b.Size)/1e6),
		Notes:   b.Notes,
	}
}

// handleAppPage is the install page for both platforms.
func handleAppPage(c *gin.Context) {
	policy, err := otaStore().ReadPolicy()
	if err != nil {
		otaStoreError(err)
		otaRefuse(c, http.StatusInternalServerError, "policy error")
		return
	}
	data := struct{ Android, IOS *appPageBuild }{
		Android: pageBuild(ota.PlatformAndroid, policy.Android.Latest),
		IOS:     pageBuild(ota.PlatformIOS, policy.IOS.Latest),
	}
	h := c.Writer.Header()
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	h.Set("Content-Type", "text/html; charset=utf-8")
	c.Status(http.StatusOK)
	if err := appPage.Execute(c.Writer, data); err != nil {
		log.Printf("[ota] /app page: %v", err)
	}
}

var appPage = template.Must(template.New("app").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Install Alex Messages</title>
<style>
  :root { color-scheme: light; }
  body { margin: 0; background: #F6F4EE; color: #1F2A24; font: 16px/1.5 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; }
  main { max-width: 640px; margin: 0 auto; padding: 24px 16px 48px; }
  h1 { font-size: 28px; margin: 8px 0 4px; }
  h2 { font-size: 20px; margin: 0 0 8px; }
  section { background: #FAF8F1; border: 1px solid #E2DFD3; border-radius: 14px; padding: 16px; margin-top: 20px; }
  a.button { display: inline-block; background: #4F7A5E; color: #fff; text-decoration: none; padding: 10px 18px; border-radius: 999px; font-weight: 600; }
  a.button:active { background: #355040; }
  ol { padding-left: 22px; margin: 12px 0 0; }
  li { margin: 6px 0; }
  code { font: 12px/1.4 ui-monospace, Menlo, monospace; word-break: break-all; }
  .muted { color: #5C6660; font-size: 14px; }
  .notes { white-space: pre-wrap; }
</style>
</head>
<body>
<main>
<h1>Install Alex Messages</h1>
<p class="muted">Messages, voice and video calls. Your messages are kept on the server, so reinstalling the app loses none of them.</p>

<section>
<h2>Android</h2>
{{with .Android}}
<p>Alex Messages {{.Version}} (build {{.Build}}), {{.SizeMB}}</p>
{{if .Notes}}<p class="notes">{{.Notes}}</p>{{end}}
<p><a class="button" href="{{.Path}}" download="{{.File}}">Download for Android</a></p>
<ol>
  <li>Tap <b>Download for Android</b> and wait until the download has finished: your Files app shows the file <code>{{.File}}</code> with its full size.</li>
  <li>If your Alex Messages is older than version 1.2.0, uninstall it now (Settings → Apps → Alex Messages → Uninstall). Android refuses to install the new version over it, because the new version is signed with a new key.</li>
  <li>Open the downloaded file. When Android asks, allow <b>Install unknown apps</b> for the app that opened it (your browser or file manager), go back, and tap <b>Install</b>.</li>
  <li>Open Alex Messages and sign in.</li>
  <li>Allow notifications, full-screen calls and background use when the app asks. Settings → Notifications in the app shows what is still missing.</li>
</ol>
<p class="muted">SHA-256 of {{.File}}:<br><code>{{.SHA256}}</code></p>
{{else}}
<p>No Android build is published yet.</p>
{{end}}
</section>

<section>
<h2>iPhone</h2>
{{with .IOS}}
<p>Alex Messages {{.Version}} (build {{.Build}}), {{.SizeMB}}</p>
{{if .Notes}}<p class="notes">{{.Notes}}</p>{{end}}
<p><a class="button" href="{{.Path}}" download="{{.File}}">Download for iPhone</a></p>
<ol>
  <li>Open this page in Safari on the iPhone and tap <b>Download for iPhone</b>.</li>
  <li>Open the downloaded file <code>{{.File}}</code> in 万能签, which signs it and installs it over the old app.</li>
  <li>Open Alex Messages and sign in if it asks.</li>
</ol>
<p class="muted">SHA-256 of {{.File}}:<br><code>{{.SHA256}}</code></p>
{{else}}
<p>No iPhone build is published yet.</p>
{{end}}
</section>

<p class="muted">To check a download on a computer, run <code>shasum -a 256 &lt;file&gt;</code> and compare the result with the SHA-256 above.</p>
</main>
</body>
</html>
`))
