package adminapp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/db"
	"alexmessage/internal/httpx"
)

// The admin debug console reads the shared debug_events ring buffer that the
// client-facing servers write to. Two read paths back the UI:
//
//   - GET /api/debug/events — a filtered snapshot/poll (also used to backfill).
//   - GET /api/debug/stream — Server-Sent Events tailing new rows for a live feed.
//
// Both accept the same filters (app, level, user, session, q). POST
// /api/debug/clear empties the buffer.

func registerDebugRoutes(r *gin.Engine) {
	r.GET("/api/debug/events", handleDebugEvents)
	r.GET("/api/debug/stream", handleDebugStream)
	r.POST("/api/debug/clear", handleDebugClear)
}

// debugFilterFromQuery reads the shared filter params off the request.
func debugFilterFromQuery(c *gin.Context) db.DebugFilter {
	f := db.DebugFilter{
		App:      c.Query("app"),
		Level:    c.Query("level"),
		Username: c.Query("user"),
		Session:  c.Query("session"),
		Search:   c.Query("q"),
	}
	if v, err := strconv.ParseInt(c.Query("since"), 10, 64); err == nil {
		f.SinceID = v
	}
	if v, err := strconv.Atoi(c.Query("limit")); err == nil {
		f.Limit = v
	}
	return f
}

func handleDebugEvents(c *gin.Context) {
	if _, ok := currentAdmin(c); !ok {
		return
	}
	events, err := db.QueryDebugEvents(debugFilterFromQuery(c))
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	latest := int64(0)
	if n := len(events); n > 0 {
		latest = events[n-1].ID
	}
	c.JSON(http.StatusOK, gin.H{"events": events, "latest_id": latest})
}

func handleDebugClear(c *gin.Context) {
	if _, ok := currentAdmin(c); !ok {
		return
	}
	if err := db.ClearDebugEvents(); err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// handleDebugStream tails the ring buffer over SSE. Since the writers are
// separate processes, the cross-process channel is the SQLite table itself, so
// this polls it on a short interval rather than subscribing to an in-process
// bus. The client passes ?since= (the latest id it already has) to avoid
// replaying old rows on (re)connect.
func handleDebugStream(c *gin.Context) {
	if _, ok := currentAdmin(c); !ok {
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no") // disable proxy buffering (nginx)

	filter := debugFilterFromQuery(c)
	cursor := filter.SinceID
	if cursor == 0 {
		// Start at the current tail so a fresh stream only shows new events; the
		// snapshot endpoint is responsible for any backfill.
		if latest, err := db.LatestDebugEventID(); err == nil {
			cursor = latest
		}
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	ctx := c.Request.Context()

	c.Stream(func(w io.Writer) bool {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			filter.SinceID = cursor
			filter.Limit = 200
			events, err := db.QueryDebugEvents(filter)
			if err != nil || len(events) == 0 {
				// Comment line doubles as a keepalive so idle proxies don't cut us.
				fmt.Fprint(w, ": ping\n\n")
				return true
			}
			for _, ev := range events {
				b, mErr := json.Marshal(ev)
				if mErr != nil {
					continue
				}
				fmt.Fprintf(w, "data: %s\n\n", b)
				cursor = ev.ID
			}
			return true
		}
	})
}
