package webapp

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"os"
	"strconv"
	"strings"
	"time"
)

// ICE servers for DM calls. STUN lets each device learn its public address,
// which is enough for most calls to connect peer to peer; TURN relays the
// media when both sides sit behind NATs that block a direct path. Settings:
//
//	STUN_URLS        comma-separated stun: URLs (default: Google's public STUN)
//	TURN_URLS        comma-separated turn:/turns: URLs; no TURN when empty
//	TURN_SECRET      shared secret (coturn `static-auth-secret`): each call gets
//	                 credentials that expire after turnCredentialTTL, per the
//	                 TURN REST API scheme coturn and eturnal implement
//	TURN_USERNAME / TURN_CREDENTIAL
//	                 static credentials, used only when TURN_SECRET is unset
type iceServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// turnCredentialTTL outlives any realistic call: coturn checks the expiry on
// every allocation refresh, so an expired credential drops the relay mid-call.
const turnCredentialTTL = 24 * time.Hour

var defaultSTUNURLs = []string{"stun:stun.l.google.com:19302", "stun:stun1.l.google.com:19302"}

// iceServersFor returns the ICE configuration handed to userID's device for
// one call. TURN credentials are minted per user so a leaked pair names its
// owner and expires on its own.
func iceServersFor(userID int) []iceServer {
	stun := splitURLList(os.Getenv("STUN_URLS"))
	if len(stun) == 0 {
		stun = defaultSTUNURLs
	}
	servers := []iceServer{{URLs: stun}}
	turn := splitURLList(os.Getenv("TURN_URLS"))
	if len(turn) == 0 {
		return servers
	}
	relay := iceServer{URLs: turn}
	if secret := os.Getenv("TURN_SECRET"); secret != "" {
		relay.Username, relay.Credential = turnRESTCredential(secret, userID, time.Now().Add(turnCredentialTTL))
	} else {
		relay.Username, relay.Credential = os.Getenv("TURN_USERNAME"), os.Getenv("TURN_CREDENTIAL")
	}
	return append(servers, relay)
}

// turnRESTCredential implements the TURN REST API credential: the username is
// "<expiry unix seconds>:<user id>" and the password is
// base64(HMAC-SHA1(secret, username)), which the TURN server recomputes.
func turnRESTCredential(secret string, userID int, expires time.Time) (string, string) {
	username := strconv.FormatInt(expires.Unix(), 10) + ":" + strconv.Itoa(userID)
	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write([]byte(username))
	return username, base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func splitURLList(raw string) []string {
	out := []string{}
	for _, part := range strings.Split(raw, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
