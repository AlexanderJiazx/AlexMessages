package httpx

import (
	"crypto/tls"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSessionCookieTransport(t *testing.T) {
	for _, tc := range []struct {
		name       string
		tls, proxy bool
		remote     string
		secure     bool
	}{
		{"localhost", false, false, "127.0.0.1:1234", false},
		{"https", true, false, "192.0.2.1:1234", true},
		{"nginx", false, true, "127.0.0.1:1234", true},
		{"spoofed proxy", false, true, "192.0.2.1:1234", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "http://messages.example/api/login", nil)
			c.Request.RemoteAddr = tc.remote
			if tc.tls {
				c.Request.TLS = &tls.ConnectionState{}
			}
			if tc.proxy {
				c.Request.Header.Set("X-Forwarded-Proto", "https")
			}
			SetSessionCookie(c, "am_session", "secret")
			cookie := w.Result().Cookies()[0]
			if cookie.Secure != tc.secure || !cookie.HttpOnly {
				t.Fatalf("cookie=%+v", cookie)
			}
		})
	}
}

func TestWebSocketOrigin(t *testing.T) {
	t.Setenv("WS_ALLOWED_ORIGINS", "http://localhost:5173")
	for _, tc := range []struct {
		origin  string
		allowed bool
	}{
		{"", true}, {"http://messages.example", true}, {"https://messages.example", false},
		{"http://evil.example", false}, {"null", false}, {"http://messages.example/path", false},
		{"http://messages.example.evil.example", false}, {"http://localhost:5173", true},
	} {
		r := httptest.NewRequest("GET", "http://messages.example/ws", nil)
		r.Header.Set("Origin", tc.origin)
		if got := WebSocketOrigin(r); got != tc.allowed {
			t.Errorf("%q: got %v", tc.origin, got)
		}
	}
}

func TestWebSocketOriginBehindTLSProxy(t *testing.T) {
	t.Setenv("WS_ALLOWED_ORIGINS", "")
	r := httptest.NewRequest("GET", "http://messages.alexanderjia.com/ws", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("Origin", "https://messages.alexanderjia.com")
	if !WebSocketOrigin(r) {
		t.Fatal("nginx HTTPS origin was rejected")
	}
	r.Header.Set("Origin", "https://other.alexanderjia.com")
	if WebSocketOrigin(r) {
		t.Fatal("foreign same-site origin was allowed")
	}
	r.Header.Set("Origin", "https://messages.alexanderjia.com")
	r.RemoteAddr = "192.0.2.1:1234"
	if WebSocketOrigin(r) {
		t.Fatal("untrusted caller forged HTTPS forwarding")
	}
}
