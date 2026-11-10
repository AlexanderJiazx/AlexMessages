package push

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"alexmessage/internal/db"
)

// A push service that takes the request and never answers must not hold the
// sender forever: every push queued behind it would wait too.
func TestSendOneGivesUpOnStalledPushService(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-release
	}))
	defer srv.Close()
	defer close(release)

	vapid, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	deriveKeys(vapid)
	browser, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatal(err)
	}
	sub := db.PushSubscription{
		Endpoint: srv.URL + "/sub",
		P256dh:   base64.RawURLEncoding.EncodeToString(browser.PublicKey().Bytes()),
		Auth:     base64.RawURLEncoding.EncodeToString(auth),
	}

	// The real timeout is 30 s; a short one keeps the test fast.
	prev := pushHTTPClient
	pushHTTPClient = &http.Client{Timeout: 200 * time.Millisecond}
	defer func() { pushHTTPClient = prev }()

	done := make(chan bool, 1)
	go func() { done <- sendOne(sub, []byte(`{"title":"t"}`)) }()
	select {
	case delivered := <-done:
		if delivered {
			t.Fatal("a push that got no answer counted as delivered")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sendOne is still waiting on a push service that never answers")
	}
}

func TestValidEndpointAcceptsOnlyKnownPushServices(t *testing.T) {
	good := []string{
		"https://fcm.googleapis.com/fcm/send/abc:def",
		"https://fcm.googleapis.com/wp/abc",
		"https://updates.push.services.mozilla.com/wpush/v2/gAAA",
		"https://web.push.apple.com/QGx",
		"https://api.push.apple.com/3/device/x",
		"https://wns2-par02p.notify.windows.com/w/?token=x",
		"https://FCM.googleapis.com:443/fcm/send/x",
	}
	for _, e := range good {
		if !ValidEndpoint(e) {
			t.Errorf("ValidEndpoint(%q) = false, want true", e)
		}
	}
	bad := []string{
		"", "http://fcm.googleapis.com/fcm/send/x", "https://127.0.0.1/x",
		"https://localhost/x", "https://evil.example/x", "https://fcm.googleapis.com.evil.example/x",
		"https://evilpush.apple.com/x", "https://push.apple.com/x", "https://notify.windows.com/x",
		"https://fcm.googleapis.com:8443/x", "https://user:pw@fcm.googleapis.com/x",
		"https://169.254.169.254/latest/meta-data", "ftp://fcm.googleapis.com/x",
	}
	for _, e := range bad {
		if ValidEndpoint(e) {
			t.Errorf("ValidEndpoint(%q) = true, want false", e)
		}
	}
}

// The real push client refuses to dial a loopback endpoint, even one that
// was stored before endpoints were checked.
func TestPushClientRefusesInternalAddress(t *testing.T) {
	hit := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit <- struct{}{} }))
	defer srv.Close()
	resp, err := pushHTTPClient.Post(srv.URL+"/sub", "text/plain", nil)
	if err == nil {
		resp.Body.Close()
		t.Fatal("push client reached a loopback server")
	}
	select {
	case <-hit:
		t.Fatal("loopback server got the request")
	default:
	}
	if pushHTTPClient.CheckRedirect == nil {
		t.Fatal("push client follows redirects")
	}
}
