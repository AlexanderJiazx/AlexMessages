package webapp

import (
	"bytes"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/db"
	"alexmessage/internal/httpx"
)

// Over-the-air updates of the mobile app (expo-updates) and the /app install
// page. mobile/scripts/publish-update.sh uploads the output of `npx expo
// export` to data/updates/<runtime version>/<unix seconds>/, and every app
// start asks /api/ota/manifest for the newest update of its platform and
// runtime version (the app version). Updates are not signed: the app trusts
// this server, as most Expo apps trust theirs. No route here needs a
// session; expo-updates sends none.
func registerOTARoutes(r *gin.Engine) {
	r.GET("/api/ota/manifest", handleOTAManifest)
	mountStatic(r, "/ota/files", updatesDir())
	r.GET("/app", handleAppPage)
	r.GET("/app/:file", handleAppFile)
	r.HEAD("/app/:file", handleAppFile)
}

func updatesDir() string  { return filepath.Join(db.DataDir, "updates") }
func appFilesDir() string { return filepath.Join(db.DataDir, "app") }

// runtimeVersionRE keeps the runtime version a single plain directory name.
var runtimeVersionRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// manifestAsset is one file of an update in the Expo Updates protocol.
type manifestAsset struct {
	Hash          string `json:"hash"` // base64url SHA-256, checked by the app
	Key           string `json:"key"`
	ContentType   string `json:"contentType"`
	FileExtension string `json:"fileExtension"`
	URL           string `json:"url"`
}

type manifest struct {
	ID             string          `json:"id"`
	CreatedAt      string          `json:"createdAt"`
	RuntimeVersion string          `json:"runtimeVersion"`
	LaunchAsset    manifestAsset   `json:"launchAsset"`
	Assets         []manifestAsset `json:"assets"`
	Metadata       struct{}        `json:"metadata"`
	Extra          struct {
		// The public app config, which expo-constants serves as
		// Constants.expoConfig while the update runs.
		ExpoClient json.RawMessage `json:"expoClient"`
	} `json:"extra"`
}

// manifests caches each update's manifest per platform, with URLs relative
// to the server: a published update never changes.
var manifests sync.Map // "<runtime>/<update>/<platform>" → *manifest

func handleOTAManifest(c *gin.Context) {
	h := c.Writer.Header()
	h.Set("expo-protocol-version", "1")
	h.Set("expo-sfv-version", "0")
	h.Set("Cache-Control", "private, max-age=0, no-store")

	platform := c.GetHeader("expo-platform")
	runtimeVersion := c.GetHeader("expo-runtime-version")
	if platform != "ios" && platform != "android" {
		httpx.Error(c, http.StatusBadRequest, "expo-platform must be ios or android")
		return
	}
	if !runtimeVersionRE.MatchString(runtimeVersion) {
		httpx.Error(c, http.StatusBadRequest, "invalid expo-runtime-version")
		return
	}
	m, err := newestManifest(runtimeVersion, platform)
	if err != nil {
		log.Printf("[ota] %v", err)
		httpx.Error(c, http.StatusInternalServerError, "update store error")
		return
	}
	if m == nil {
		c.Status(http.StatusNoContent) // no update: the app keeps what it runs
		return
	}

	// Asset URLs must be absolute; they point back at this server.
	out := *m
	base := httpx.Scheme(c.Request) + "://" + c.Request.Host
	out.LaunchAsset.URL = base + m.LaunchAsset.URL
	out.Assets = make([]manifestAsset, len(m.Assets))
	for i, a := range m.Assets {
		a.URL = base + a.URL
		out.Assets[i] = a
	}
	body, err := json.Marshal(out)
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "update store error")
		return
	}
	boundary := randomBoundary()
	var b bytes.Buffer
	fmt.Fprintf(&b, "--%s\r\ncontent-disposition: form-data; name=\"manifest\"\r\ncontent-type: application/json; charset=utf-8\r\n\r\n", boundary)
	b.Write(body)
	fmt.Fprintf(&b, "\r\n--%s--\r\n", boundary)
	c.Data(http.StatusOK, "multipart/mixed; boundary="+boundary, b.Bytes())
}

func randomBoundary() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// newestManifest is the manifest of the newest update published for the
// runtime version, or nil when there is none or it has no bundle for the
// platform. Updates are directories named by their publish time in unix
// seconds; an upload in progress has a dot-prefixed name and is skipped.
func newestManifest(runtimeVersion, platform string) (*manifest, error) {
	entries, err := os.ReadDir(filepath.Join(updatesDir(), runtimeVersion))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	newest := int64(-1)
	for _, e := range entries {
		n, err := strconv.ParseInt(e.Name(), 10, 64)
		if e.IsDir() && err == nil && n > newest && strconv.FormatInt(n, 10) == e.Name() {
			newest = n
		}
	}
	if newest < 0 {
		return nil, nil
	}
	update := strconv.FormatInt(newest, 10)
	key := runtimeVersion + "/" + update + "/" + platform
	if m, ok := manifests.Load(key); ok {
		return m.(*manifest), nil
	}
	m, err := readManifest(runtimeVersion, update, time.Unix(newest, 0), platform)
	if err != nil {
		return nil, fmt.Errorf("update %s/%s: %w", runtimeVersion, update, err)
	}
	if m != nil {
		manifests.Store(key, m)
	}
	return m, nil
}

// readManifest builds a manifest from an update's metadata.json and
// expoConfig.json, hashing each file it lists.
func readManifest(runtimeVersion, update string, createdAt time.Time, platform string) (*manifest, error) {
	dir := filepath.Join(updatesDir(), runtimeVersion, update)
	metadata, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if err != nil {
		return nil, err
	}
	var meta struct {
		FileMetadata map[string]struct {
			Bundle string `json:"bundle"`
			Assets []struct {
				Path string `json:"path"`
				Ext  string `json:"ext"`
			} `json:"assets"`
		} `json:"fileMetadata"`
	}
	if err := json.Unmarshal(metadata, &meta); err != nil {
		return nil, fmt.Errorf("metadata.json: %w", err)
	}
	files, ok := meta.FileMetadata[platform]
	if !ok || files.Bundle == "" {
		return nil, nil
	}
	expoConfig, err := os.ReadFile(filepath.Join(dir, "expoConfig.json"))
	if err != nil {
		return nil, err
	}
	if !json.Valid(expoConfig) {
		return nil, errors.New("expoConfig.json is not JSON")
	}

	describe := func(path, ext, contentType string) (manifestAsset, error) {
		if !filepath.IsLocal(filepath.FromSlash(path)) {
			return manifestAsset{}, fmt.Errorf("metadata.json names %q, outside the update", path)
		}
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(path)))
		if err != nil {
			return manifestAsset{}, err
		}
		sha := sha256.Sum256(b)
		sum := md5.Sum(b)
		return manifestAsset{
			Hash:          base64.RawURLEncoding.EncodeToString(sha[:]),
			Key:           hex.EncodeToString(sum[:]),
			ContentType:   contentType,
			FileExtension: ext,
			URL:           "/ota/files/" + runtimeVersion + "/" + update + "/" + path,
		}, nil
	}

	m := &manifest{
		CreatedAt:      createdAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		RuntimeVersion: runtimeVersion,
	}
	if m.LaunchAsset, err = describe(files.Bundle, ".bundle", "application/javascript"); err != nil {
		return nil, err
	}
	m.Assets = []manifestAsset{}
	for _, a := range files.Assets {
		contentType := mime.TypeByExtension("." + a.Ext)
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		asset, err := describe(a.Path, "."+a.Ext, contentType)
		if err != nil {
			return nil, err
		}
		m.Assets = append(m.Assets, asset)
	}
	m.Extra.ExpoClient = expoConfig
	// The id is a UUID-shaped hash of the update's content, as in Expo's
	// example server: the same export always gets the same id.
	id := sha256.Sum256(append(metadata, expoConfig...))
	x := hex.EncodeToString(id[:16])
	m.ID = x[0:8] + "-" + x[8:12] + "-" + x[12:16] + "-" + x[16:20] + "-" + x[20:32]
	return m, nil
}

// appFiles are the downloads of the /app page, which
// mobile/scripts/publish-app.sh copies into data/app/.
var appFiles = map[string]string{
	"AlexMessages.apk": "application/vnd.android.package-archive",
	"AlexMessages.ipa": "application/octet-stream",
}

// handleAppFile serves /app/AlexMessages.apk and /app/AlexMessages.ipa. The
// names never change, so no browser or Cloudflare cache may keep a copy.
func handleAppFile(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	name := c.Param("file")
	contentType, ok := appFiles[name]
	if !ok {
		httpx.Error(c, http.StatusNotFound, "not found")
		return
	}
	f, err := os.Open(filepath.Join(appFilesDir(), name))
	if err != nil {
		httpx.Error(c, http.StatusNotFound, "not found")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		httpx.Error(c, http.StatusNotFound, "not found")
		return
	}
	c.Header("Content-Type", contentType)
	c.Header("Content-Disposition", `attachment; filename="`+name+`"`)
	http.ServeContent(c.Writer, c.Request, name, info.ModTime(), f)
}

// appDownload is one platform's download on the /app page.
type appDownload struct {
	Path, File, Size, Date string
}

func appDownloadFor(name string) *appDownload {
	info, err := os.Stat(filepath.Join(appFilesDir(), name))
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	return &appDownload{
		Path: "/app/" + name,
		File: name,
		Size: fmt.Sprintf("%.1f MB", float64(info.Size())/1e6),
		Date: info.ModTime().UTC().Format("2 January 2006"),
	}
}

// handleAppPage is the install page for both platforms.
func handleAppPage(c *gin.Context) {
	data := struct{ Android, IOS *appDownload }{
		Android: appDownloadFor("AlexMessages.apk"),
		IOS:     appDownloadFor("AlexMessages.ipa"),
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
  .muted { color: #5C6660; font-size: 14px; }
</style>
</head>
<body>
<main>
<h1>Install Alex Messages</h1>
<p class="muted">Messages, voice and video calls. Small updates reach the app by themselves: you need this page only to install the app, or when you are told that a new version needs installing.</p>

<section>
<h2>Android</h2>
{{with .Android}}
<p>Version of {{.Date}}, {{.Size}}</p>
<p><a class="button" href="{{.Path}}" download="{{.File}}">Download for Android</a></p>
<ol>
  <li>Tap <b>Download for Android</b> and wait until the download has finished.</li>
  <li>Open the downloaded file. When Android asks, allow <b>Install unknown apps</b> for the app that opened it (your browser or file manager), go back, and tap <b>Install</b>. It installs over the old app and keeps your sign-in.</li>
  <li>Open Alex Messages. On a first install, sign in.</li>
  <li>Allow notifications, full-screen calls and background use when the app asks. Settings → Notifications in the app shows what is still missing.</li>
</ol>
{{else}}
<p>No Android build is published yet.</p>
{{end}}
</section>

<section>
<h2>iPhone</h2>
{{with .IOS}}
<p>Version of {{.Date}}, {{.Size}}</p>
<p><a class="button" href="{{.Path}}" download="{{.File}}">Download for iPhone</a></p>
<ol>
  <li>Open this page in Safari on the iPhone and tap <b>Download for iPhone</b>.</li>
  <li>Open the downloaded file in 万能签, which signs it and installs it over the old app.</li>
  <li>Open Alex Messages.</li>
</ol>
{{else}}
<p>No iPhone build is published yet.</p>
{{end}}
</section>
</main>
</body>
</html>
`))
