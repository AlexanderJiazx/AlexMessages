package httpx

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSecurityHeadersOn200And404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(SecurityHeaders())
	r.GET("/ok", func(c *gin.Context) { c.String(200, "ok") })
	for path, want := range map[string]int{"/ok": 200, "/missing": 404} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != want {
			t.Fatalf("%s status=%d want %d", path, w.Code, want)
		}
		for h, v := range map[string]string{
			"X-Content-Type-Options": "nosniff",
			"X-Frame-Options":        "DENY",
			"Referrer-Policy":        "strict-origin-when-cross-origin",
		} {
			if got := w.Header().Get(h); got != v {
				t.Fatalf("%s %s=%q want %q", path, h, got, v)
			}
		}
	}
}

func staticFixture(t *testing.T, cacheControl string) (*gin.Engine, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	outer := t.TempDir()
	dir := filepath.Join(outer, "pub")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("0123456789"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outer, "secret.txt"), []byte("TOPSECRET"), 0644); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	Static(r, "/s", dir, cacheControl)
	return r, dir
}

func do(r http.Handler, method, path string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestStaticServesWithCacheControl(t *testing.T) {
	r, _ := staticFixture(t, "public, max-age=60")
	w := do(r, "GET", "/s/a.txt", nil)
	if w.Code != 200 || w.Body.String() != "0123456789" || w.Header().Get("Cache-Control") != "public, max-age=60" {
		t.Fatalf("GET status=%d body=%q cc=%q", w.Code, w.Body.String(), w.Header().Get("Cache-Control"))
	}
	if w := do(r, "HEAD", "/s/a.txt", nil); w.Code != 200 {
		t.Fatalf("HEAD status=%d", w.Code)
	}
	w = do(r, "GET", "/s/a.txt", map[string]string{"Range": "bytes=2-4"})
	if w.Code != 206 || w.Body.String() != "234" {
		t.Fatalf("range status=%d body=%q", w.Code, w.Body.String())
	}
}

func TestStaticNotFoundHasNoCacheControl(t *testing.T) {
	r, _ := staticFixture(t, "public, max-age=60")
	for _, path := range []string{"/s/missing.txt", "/s/sub", "/s/sub/", "/s/"} {
		w := do(r, "GET", path, nil)
		if w.Code != 404 || w.Header().Get("Cache-Control") != "" {
			t.Fatalf("%s status=%d cc=%q", path, w.Code, w.Header().Get("Cache-Control"))
		}
	}
}

func TestStaticNeverServesOutsideDirectory(t *testing.T) {
	r, _ := staticFixture(t, "no-cache")
	for _, path := range []string{"/s/../secret.txt", "/s/%2e%2e/secret.txt", "/s/sub/../../secret.txt", "/s/..%2fsecret.txt"} {
		w := do(r, "GET", path, nil)
		if w.Code == 200 || w.Body.String() == "TOPSECRET" {
			t.Fatalf("%s served outside file: status=%d", path, w.Code)
		}
	}
}

func TestStaticEmptyCacheControlSendsNoHeader(t *testing.T) {
	r, _ := staticFixture(t, "")
	w := do(r, "GET", "/s/a.txt", nil)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "" {
		t.Fatalf("status=%d cc=%q", w.Code, w.Header().Get("Cache-Control"))
	}
}
