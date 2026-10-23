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
