// Package debuglog is the ingestion side of the admin debug console.
//
// Both client-facing servers (Alex Messages and Alex Meet) expose a single
// POST /api/debug/report endpoint built from ReportHandler. Browsers stream
// real-time actions there; the events are written to the shared SQLite ring
// buffer (db.debug_events) and the admin panel — a separate process — tails
// them. Go code can also record events directly with Emit.
//
// The tunnel is deliberately defensive: a per-IP token bucket, a hard cap on
// body size and batch length, field-length limits, and a level whitelist keep a
// hostile or runaway client from flooding the buffer or the database.
package debuglog

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/auth"
	"alexmessage/internal/db"
)

// Ingestion limits.
const (
	maxBodyBytes   = 64 * 1024 // reject oversized report bodies outright
	maxBatch       = 50        // events accepted per request
	maxEventLen    = 64        // runes
	maxMessageLen  = 2000      // runes
	maxSessionLen  = 64        // runes
	maxContextSize = 4000      // bytes of serialized JSON context
)

// validLevels is the accepted set; anything else is coerced to "info".
var validLevels = map[string]bool{"debug": true, "info": true, "warn": true, "error": true}

// ---------- per-IP rate limiting ----------

// A token bucket: capacity events buffered, refilled at refillPerSec. A batch of
// N events costs N tokens; events beyond the available budget are dropped.
const (
	bucketCapacity   = 200
	bucketRefillRate = 20.0 // tokens per second
)

type bucket struct {
	tokens float64
	last   time.Time
}

var (
	limiterMu sync.Mutex
	limiters  = map[string]*bucket{}
)

// allow consumes up to `want` tokens for ip and returns how many were granted.
func allow(ip string, want int) int {
	limiterMu.Lock()
	defer limiterMu.Unlock()

	now := time.Now()
	b := limiters[ip]
	if b == nil {
		b = &bucket{tokens: bucketCapacity, last: now}
		limiters[ip] = b
		if len(limiters) > 4096 {
			pruneLimitersLocked(now)
		}
	}
	// Refill since last seen.
	b.tokens += now.Sub(b.last).Seconds() * bucketRefillRate
	if b.tokens > bucketCapacity {
		b.tokens = bucketCapacity
	}
	b.last = now

	granted := want
	if float64(granted) > b.tokens {
		granted = int(b.tokens)
	}
	b.tokens -= float64(granted)
	return granted
}

// pruneLimitersLocked drops buckets idle for over 10 minutes. Caller holds the lock.
func pruneLimitersLocked(now time.Time) {
	for ip, b := range limiters {
		if now.Sub(b.last) > 10*time.Minute {
			delete(limiters, ip)
		}
	}
}

// ---------- HTTP ingestion ----------

// reportBody is the wire shape clients POST. A single-event report omits Events
// and fills the top-level fields instead, so the same endpoint serves both.
type reportBody struct {
	Session string        `json:"session"`
	Events  []reportEvent `json:"events"`
	// single-event fallback
	Level   string          `json:"level"`
	Event   string          `json:"event"`
	Message string          `json:"message"`
	Context json.RawMessage `json:"context"`
}

type reportEvent struct {
	TsMs    int64           `json:"ts_ms"`
	Level   string          `json:"level"`
	Event   string          `json:"event"`
	Message string          `json:"message"`
	Context json.RawMessage `json:"context"`
}

// ReportHandler builds the POST /api/debug/report handler for one app
// ("messages" or "meet"). It always answers 200 {"ok":true} — even when events
// are dropped — so a probing client learns nothing about the limits.
func ReportHandler(app string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes)

		var body reportBody
		if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil {
			// Bad/oversized body: ack without recording.
			c.JSON(http.StatusOK, gin.H{"ok": true})
			return
		}

		ip := clientIP(c)
		events := body.Events
		if len(events) == 0 && (body.Message != "" || body.Event != "") {
			events = []reportEvent{{Level: body.Level, Event: body.Event, Message: body.Message, Context: body.Context}}
		}
		if len(events) > maxBatch {
			events = events[:maxBatch]
		}

		granted := allow(ip, len(events))
		if granted <= 0 {
			c.JSON(http.StatusOK, gin.H{"ok": true})
			return
		}
		events = events[:granted]

		// Identify the actor server-side from the session cookie; never trust a
		// client-supplied user id. Guests (no cookie) report with a null user.
		var userID *int
		username := "guest"
		if u := auth.ResolveSession(cookie(c, auth.UserCookie), "user"); u != nil {
			id := u.ID
			userID = &id
			username = u.Username
		}
		session := clamp(strings.TrimSpace(body.Session), maxSessionLen)
		now := db.NowTS() * 1000

		for _, e := range events {
			ts := e.TsMs
			if ts <= 0 {
				ts = now
			}
			_, _ = db.InsertDebugEvent(db.DebugEvent{
				TsMs:     ts,
				App:      app,
				Level:    normLevel(e.Level),
				UserID:   userID,
				Username: username,
				Session:  session,
				Event:    clamp(strings.TrimSpace(e.Event), maxEventLen),
				Message:  clamp(strings.TrimSpace(e.Message), maxMessageLen),
				Context:  clampContext(e.Context),
				IP:       ip,
			})
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

// Emit records a server-side event into the same console. Fire-and-forget:
// errors are swallowed so logging can never break a request path.
func Emit(app, level, event, message string, ctx map[string]any) {
	var raw string
	if ctx != nil {
		if b, err := json.Marshal(ctx); err == nil {
			raw = clampString(string(b), maxContextSize)
		}
	}
	_, _ = db.InsertDebugEvent(db.DebugEvent{
		TsMs:    db.NowTS() * 1000,
		App:     app,
		Level:   normLevel(level),
		Event:   clamp(event, maxEventLen),
		Message: clamp(message, maxMessageLen),
		Context: raw,
	})
}

// ---------- helpers ----------

func normLevel(level string) string {
	l := strings.ToLower(strings.TrimSpace(level))
	switch l {
	case "log", "": // browser console.log maps to debug
		return "debug"
	}
	if validLevels[l] {
		return l
	}
	return "info"
}

// clamp trims a string to at most n runes (multibyte-safe).
func clamp(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func clampString(s string, nBytes int) string {
	if len(s) <= nBytes {
		return s
	}
	return s[:nBytes]
}

// clampContext keeps a client context blob to a sane size. Empty/invalid JSON
// collapses to "".
func clampContext(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	if len(raw) > maxContextSize {
		return clampString(string(raw), maxContextSize)
	}
	if !json.Valid(raw) {
		return ""
	}
	return string(raw)
}

func cookie(c *gin.Context, name string) string {
	v, err := c.Cookie(name)
	if err != nil {
		return ""
	}
	return v
}

// clientIP returns the connecting IP, preferring Gin's parsed value but falling
// back to the raw remote address.
func clientIP(c *gin.Context) string {
	if ip := c.ClientIP(); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(c.Request.RemoteAddr)
	if err != nil {
		return c.Request.RemoteAddr
	}
	return host
}
