// Package push implements the application-server side of Web Push (VAPID).
//
// It bootstraps a VAPID key pair (persisted under data/ as a PKCS8 PEM plus a
// base64url public-key mirror, exactly like the Python original), delivers
// encrypted payloads to a user's stored subscriptions via webpush-go, and
// prunes any subscription the push service has invalidated (404/410).
package push

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	webpush "github.com/SherClockHolmes/webpush-go"

	"alexmessage/internal/db"
)

var (
	vapidPrivatePath = filepath.Join(db.DataDir, "vapid_private.pem")
	vapidPublicPath  = filepath.Join(db.DataDir, "vapid_public.txt")
)

// Per RFC 8030: 24h gives a reasonable buffer for an offline recipient. The
// payload is small so push-service storage is not a concern.
const (
	pushTTLSeconds = 24 * 60 * 60
	pushUrgency    = webpush.UrgencyNormal // DM "ping" is normal priority.
)

// In-memory base64url keys derived from the on-disk PEM. The public key is the
// uncompressed EC point; the private key is the raw 32-byte scalar — the two
// forms webpush-go expects.
var (
	vapidPublicB64  string
	vapidPrivateB64 string
)

// vapidSubject is the contact URI the push service can use to reach an admin
// (RFC 8292). Defaults to a mailto: URI.
func vapidSubject() string {
	s := strings.TrimSpace(os.Getenv("VAPID_SUBJECT"))
	if s == "" {
		s = "mailto:admin@alexanderjia.com"
	}
	return s
}

// BootstrapVAPID generates VAPID keys on first run; on subsequent runs it loads
// the PEM and re-derives the public key so the in-memory key, the .txt mirror,
// and what we sign with cannot drift apart.
func BootstrapVAPID() error {
	if _, err := os.Stat(vapidPrivatePath); err == nil {
		pemBytes, rerr := os.ReadFile(vapidPrivatePath)
		if rerr == nil {
			if priv, perr := parsePKCS8EC(pemBytes); perr == nil {
				deriveKeys(priv)
				return os.WriteFile(vapidPublicPath, []byte(vapidPublicB64), 0o644)
			}
		}
		fmt.Fprintln(os.Stderr, "[alexmessage] existing VAPID key unreadable; regenerating")
	}

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(vapidPrivatePath, pemBytes, 0o600); err != nil {
		return err
	}
	deriveKeys(priv)
	if err := os.WriteFile(vapidPublicPath, []byte(vapidPublicB64), 0o644); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "[alexmessage] generated new VAPID key pair")
	return nil
}

func parsePKCS8EC(pemBytes []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("no PEM block")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	ec, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("not an EC private key")
	}
	return ec, nil
}

func deriveKeys(priv *ecdsa.PrivateKey) {
	// VAPID applicationServerKey = base64url(uncompressed-point), no padding.
	pubRaw := elliptic.Marshal(priv.Curve, priv.X, priv.Y) //nolint:staticcheck // matches Python's X9.62 encoding
	vapidPublicB64 = base64.RawURLEncoding.EncodeToString(pubRaw)

	// webpush-go wants the private key as the raw 32-byte scalar, base64url.
	d := priv.D.FillBytes(make([]byte, 32))
	vapidPrivateB64 = base64.RawURLEncoding.EncodeToString(d)
}

// PublicKey returns the base64url VAPID public key served to clients.
func PublicKey() string {
	if vapidPublicB64 == "" {
		_ = BootstrapVAPID()
	}
	return vapidPublicB64
}

// sendOne encrypts + signs + POSTs one push. It removes the row on 404/410.
// Returns whether the push was delivered (2xx).
func sendOne(sub db.PushSubscription, data []byte) bool {
	subscriber := strings.TrimPrefix(vapidSubject(), "mailto:")
	resp, err := webpush.SendNotification(data, &webpush.Subscription{
		Endpoint: sub.Endpoint,
		Keys:     webpush.Keys{P256dh: sub.P256dh, Auth: sub.Auth},
	}, &webpush.Options{
		Subscriber:      subscriber,
		VAPIDPublicKey:  vapidPublicB64,
		VAPIDPrivateKey: vapidPrivateB64,
		TTL:             pushTTLSeconds,
		Urgency:         pushUrgency,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "[alexmessage] push send error: %v\n", err)
		return false
	}
	defer resp.Body.Close()
	status := resp.StatusCode
	if status >= 200 && status < 300 {
		return true
	}
	if status == 404 || status == 410 {
		_ = db.RemovePushSubscription(sub.Endpoint)
	} else {
		fmt.Fprintf(os.Stderr, "[alexmessage] push send failed (%d)\n", status)
	}
	return false
}

// SendToUser delivers a payload to every subscription owned by userID and
// returns the count that accepted it. Never returns an error; failures are
// logged and skipped. (The Python async wrapper is unnecessary here — callers
// invoke this from a goroutine when they want fire-and-forget delivery.)
func SendToUser(userID int, payload map[string]any) int {
	subs, err := db.ListPushSubscriptions(userID)
	if err != nil || len(subs) == 0 {
		return 0
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return 0
	}
	sent := 0
	for _, sub := range subs {
		if sendOne(sub, data) {
			sent++
		}
	}
	return sent
}
