package webapp

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/db"
)

// writeUpdate publishes a fake `npx expo export` output as update <name> of
// runtime 1.0.0, with an Android bundle (and an iOS one when ios is true).
func writeUpdate(t *testing.T, name string, ios bool) {
	t.Helper()
	dir := filepath.Join(db.DataDir, "updates", "1.0.0", name)
	files := map[string]string{
		"_expo/static/js/android/entry-" + name + ".hbc": "android bundle " + name,
		"assets/0123abcd": "png bytes",
		"expoConfig.json": `{"name":"Alex Messages","extra":{"defaultServerUrl":"https://example.com"}}`,
	}
	meta := `{"version":0,"bundler":"metro","fileMetadata":{"android":{"bundle":"_expo/static/js/android/entry-` + name + `.hbc","assets":[{"path":"assets/0123abcd","ext":"png"}]}`
	if ios {
		files["_expo/static/js/ios/entry-"+name+".hbc"] = "ios bundle " + name
		meta += `,"ios":{"bundle":"_expo/static/js/ios/entry-` + name + `.hbc","assets":[]}`
	}
	files["metadata.json"] = meta + `}}`
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func otaTestEngine(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	initTranscribeTestDB(t)
	manifests.Clear()
	t.Cleanup(manifests.Clear)
	return NewEngine()
}

func manifestRequest(r *gin.Engine, platform, runtime string, edit func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/api/ota/manifest", nil)
	req.Header.Set("expo-protocol-version", "1")
	req.Header.Set("expo-platform", platform)
	req.Header.Set("expo-runtime-version", runtime)
	if edit != nil {
		edit(req)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// readManifestPart parses the multipart answer: exactly one part, "manifest".
func readManifestPart(t *testing.T, rec *httptest.ResponseRecorder) manifest {
	t.Helper()
	mediaType, params, err := mime.ParseMediaType(rec.Header().Get("Content-Type"))
	if err != nil || mediaType != "multipart/mixed" {
		t.Fatalf("Content-Type %q: %v", rec.Header().Get("Content-Type"), err)
	}
	mr := multipart.NewReader(rec.Body, params["boundary"])
	part, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if part.FormName() != "manifest" || part.Header.Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("part %q with Content-Type %q", part.FormName(), part.Header.Get("Content-Type"))
	}
	var m manifest
	if err := json.NewDecoder(part).Decode(&m); err != nil {
		t.Fatal(err)
	}
	if _, err := mr.NextPart(); err != io.EOF {
		t.Fatalf("a second part: %v", err)
	}
	return m
}

func b64sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func TestOTAManifestServesNewestUpdate(t *testing.T) {
	r := otaTestEngine(t)
	writeUpdate(t, "100", true)
	writeUpdate(t, "200", true)
	// An upload in progress and a stray directory are never served.
	writeUpdate(t, ".300.tmp", true)
	writeUpdate(t, "notes", true)

	rec := manifestRequest(r, "android", "1.0.0", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("expo-protocol-version") != "1" || rec.Header().Get("expo-sfv-version") != "0" {
		t.Fatalf("protocol headers: %v", rec.Header())
	}
	m := readManifestPart(t, rec)
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).MatchString(m.ID) {
		t.Fatalf("id %q is not UUID-shaped", m.ID)
	}
	if m.CreatedAt != "1970-01-01T00:03:20.000Z" || m.RuntimeVersion != "1.0.0" {
		t.Fatalf("createdAt %q, runtimeVersion %q", m.CreatedAt, m.RuntimeVersion)
	}
	launch := m.LaunchAsset
	if launch.URL != "http://example.com/ota/files/1.0.0/200/_expo/static/js/android/entry-200.hbc" ||
		launch.Hash != b64sha("android bundle 200") || launch.ContentType != "application/javascript" || launch.FileExtension != ".bundle" {
		t.Fatalf("launch asset %+v", launch)
	}
	if len(m.Assets) != 1 || m.Assets[0].URL != "http://example.com/ota/files/1.0.0/200/assets/0123abcd" ||
		m.Assets[0].Hash != b64sha("png bytes") || m.Assets[0].ContentType != "image/png" || m.Assets[0].FileExtension != ".png" {
		t.Fatalf("assets %+v", m.Assets)
	}
	var expoClient struct {
		Extra struct {
			DefaultServerURL string `json:"defaultServerUrl"`
		} `json:"extra"`
	}
	if err := json.Unmarshal(m.Extra.ExpoClient, &expoClient); err != nil || expoClient.Extra.DefaultServerURL != "https://example.com" {
		t.Fatalf("extra.expoClient %s: %v", m.Extra.ExpoClient, err)
	}

	// The URLs in the manifest serve the files whose hashes it lists.
	req := httptest.NewRequest(http.MethodGet, launch.URL, nil)
	got := httptest.NewRecorder()
	r.ServeHTTP(got, req)
	if got.Code != http.StatusOK || got.Body.String() != "android bundle 200" {
		t.Fatalf("launch asset: %d %q", got.Code, got.Body)
	}

	// The iOS manifest of the same update has the same id.
	ios := readManifestPart(t, manifestRequest(r, "ios", "1.0.0", nil))
	if ios.ID != m.ID || ios.LaunchAsset.Hash != b64sha("ios bundle 200") {
		t.Fatalf("ios manifest %+v", ios)
	}
}

func TestOTAManifestNoUpdate(t *testing.T) {
	r := otaTestEngine(t)
	if rec := manifestRequest(r, "android", "1.0.0", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("no store: status %d", rec.Code)
	}
	// The newest update has no iOS bundle: iPhones keep what they run.
	writeUpdate(t, "100", false)
	if rec := manifestRequest(r, "ios", "1.0.0", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("no ios bundle: status %d", rec.Code)
	}
	// Another app version gets nothing from this one's updates.
	if rec := manifestRequest(r, "android", "1.1.0", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("other runtime: status %d", rec.Code)
	}
}

func TestOTAManifestRejectsBadRequests(t *testing.T) {
	r := otaTestEngine(t)
	for _, c := range []struct{ platform, runtime string }{
		{"web", "1.0.0"}, {"", "1.0.0"}, {"android", ""}, {"android", ".."}, {"android", "../1.0.0"}, {"android", "1.0.0/100"},
	} {
		if rec := manifestRequest(r, c.platform, c.runtime, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("%q %q: status %d", c.platform, c.runtime, rec.Code)
		}
	}
}

func TestOTAManifestRefusesFilesOutsideTheUpdate(t *testing.T) {
	r := otaTestEngine(t)
	writeUpdate(t, "100", false)
	meta := `{"fileMetadata":{"android":{"bundle":"../../../alexmessage.db","assets":[]}}}`
	if err := os.WriteFile(filepath.Join(db.DataDir, "updates", "1.0.0", "100", "metadata.json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	if rec := manifestRequest(r, "android", "1.0.0", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
}

func TestOTAManifestURLsBehindTheHTTPSProxy(t *testing.T) {
	r := otaTestEngine(t)
	writeUpdate(t, "100", false)
	rec := manifestRequest(r, "android", "1.0.0", func(req *http.Request) {
		req.RemoteAddr = "127.0.0.1:41000" // nginx on the Pi
		req.Header.Set("X-Forwarded-Proto", "https")
	})
	m := readManifestPart(t, rec)
	if !strings.HasPrefix(m.LaunchAsset.URL, "https://example.com/ota/files/") {
		t.Fatalf("launch asset URL %q", m.LaunchAsset.URL)
	}
}

func TestAppPageAndDownloads(t *testing.T) {
	r := otaTestEngine(t)
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}
	if rec := get("/app"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "No Android build is published yet.") {
		t.Fatalf("empty /app: %d %s", rec.Code, rec.Body)
	}
	if err := os.MkdirAll(filepath.Join(db.DataDir, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	apk := filepath.Join(db.DataDir, "app", "AlexMessages.apk")
	if err := os.WriteFile(apk, []byte("PK apk"), 0o644); err != nil {
		t.Fatal(err)
	}
	built := time.Date(2026, 10, 28, 0, 45, 0, 0, time.UTC)
	if err := os.Chtimes(apk, built, built); err != nil {
		t.Fatal(err)
	}
	page := get("/app").Body.String()
	if !strings.Contains(page, `href="/app/AlexMessages.apk"`) || !strings.Contains(page, "No iPhone build is published yet.") ||
		!strings.Contains(page, "Version of 28 October 2026, 00:45 UTC") {
		t.Fatalf("/app: %s", page)
	}
	rec := get("/app/AlexMessages.apk")
	if rec.Code != http.StatusOK || rec.Body.String() != "PK apk" ||
		rec.Header().Get("Content-Type") != "application/vnd.android.package-archive" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("apk: %d %v %q", rec.Code, rec.Header(), rec.Body)
	}
	for _, path := range []string{"/app/AlexMessages.ipa", "/app/other.apk", "/app/..%2Falexmessage.db"} {
		if rec := get(path); rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status %d", path, rec.Code)
		}
	}
}
