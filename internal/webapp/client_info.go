package webapp

import (
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/db"
	"alexmessage/internal/ota"
)

// The X-AM-Client header of the mobile app (OTAUpdatePlan.md, D8 item 2 and
// D10). Build 2 and later send it on every REST call, as the client= query
// parameter of the /ws upgrade, and from the Android ring service. A request
// without it, or with a malformed one (every web client and every build
// before 2), is never gated and stores nothing.

// sessionTokenKey is where requireUser leaves the token of an authenticated
// request, so the middleware can store the header on that session.
const sessionTokenKey = "am_session_token"

// sessionClientInterval is how often a session's client is rewritten when it
// has not changed.
const sessionClientInterval = 10 * time.Minute

// The hard gate never refuses these: the app must still read the native
// policy and its update, report errors, and sign out.
var hardGateExempt = []string{"/api/app/", "/api/ota/", "/api/debug/report", "/api/logout"}

// clientInfoMiddleware applies the hard gate to /api/ requests (REST 426,
// only while policy.json sets hard_gate for the platform) and, after an
// authenticated request, stores the header on its session.
func clientInfoMiddleware(c *gin.Context) {
	path := c.Request.URL.Path
	info, ok := ota.ParseClientInfo(c.GetHeader("X-AM-Client"))
	if !ok || !strings.HasPrefix(path, "/api/") {
		c.Next()
		return
	}
	if !isHardGateExempt(path) {
		if policy := currentPolicy(); policy != nil && policy.HardGated(info) {
			c.Header("Cache-Control", "no-store")
			c.AbortWithStatusJSON(http.StatusUpgradeRequired, gin.H{
				"detail":        "Update Alex Messages to keep messaging",
				"client_policy": policy.ClientPolicy(info),
			})
			return
		}
	}
	c.Next()
	if token := c.GetString(sessionTokenKey); token != "" {
		recordSessionClient(token, info)
	}
}

func isHardGateExempt(path string) bool {
	for _, p := range hardGateExempt {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// wsClientInfo reads the client of a WebSocket upgrade: the client= query
// parameter, or the X-AM-Client header (the Android ring service).
func wsClientInfo(c *gin.Context) (*ota.ClientInfo, bool) {
	if v := c.Query("client"); v != "" {
		return ota.ParseClientInfo(v)
	}
	return ota.ParseClientInfo(c.GetHeader("X-AM-Client"))
}

type sessionClientWrite struct {
	client string
	at     time.Time
}

var (
	sessionClientMu   sync.Mutex
	sessionClientLast = map[string]sessionClientWrite{}
)

// recordSessionClient stores the header on the session (sessions.client and
// client_seen_at): when it changed, else at most once per
// sessionClientInterval. The ring service's header is never stored: it
// shares the app's session and carries less.
func recordSessionClient(token string, info *ota.ClientInfo) {
	if info.Component == "ring" {
		return
	}
	now := time.Now()
	sessionClientMu.Lock()
	last, seen := sessionClientLast[token]
	if seen && last.client == info.Raw && now.Sub(last.at) < sessionClientInterval {
		sessionClientMu.Unlock()
		return
	}
	sessionClientLast[token] = sessionClientWrite{client: info.Raw, at: now}
	if len(sessionClientLast) > 4096 {
		for t, w := range sessionClientLast {
			if now.Sub(w.at) >= sessionClientInterval {
				delete(sessionClientLast, t)
			}
		}
	}
	sessionClientMu.Unlock()
	if err := db.RecordSessionClient(token, info.Raw, now.Unix()); err != nil {
		log.Printf("[ota] session client: %v", err)
	}
}
