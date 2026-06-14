package voicecall

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/auth"
	"alexmessage/internal/db"
	"alexmessage/internal/httpx"
)

const debugRingSize = 500

type debugEvent struct {
	ID        int64             `json:"id"`
	Time      int64             `json:"time"`
	Type      string            `json:"type"`
	Message   string            `json:"message"`
	Stack     string            `json:"stack,omitempty"`
	Context   map[string]any    `json:"context,omitempty"`
}

var (
	debugMu     sync.Mutex
	debugRing   = make([]debugEvent, 0, debugRingSize)
	debugSeq    int64
	debugSubs   = map[chan debugEvent]struct{}{}
)

func pushDebugEvent(ev debugEvent) {
	debugMu.Lock()
	defer debugMu.Unlock()
	debugSeq++
	ev.ID = debugSeq
	ev.Time = time.Now().Unix()
	if len(debugRing) >= debugRingSize {
		debugRing = append(debugRing[1:], ev)
	} else {
		debugRing = append(debugRing, ev)
	}
	for ch := range debugSubs {
		select {
		case ch <- ev:
		default:
		}
	}
}

func snapshotDebugEvents() []debugEvent {
	debugMu.Lock()
	defer debugMu.Unlock()
	out := make([]debugEvent, len(debugRing))
	copy(out, debugRing)
	return out
}

func subscribeDebugEvents() chan debugEvent {
	debugMu.Lock()
	defer debugMu.Unlock()
	ch := make(chan debugEvent, 16)
	debugSubs[ch] = struct{}{}
	return ch
}

func unsubscribeDebugEvents(ch chan debugEvent) {
	debugMu.Lock()
	defer debugMu.Unlock()
	delete(debugSubs, ch)
	close(ch)
}

func requireAdmin(c *gin.Context) (*db.User, bool) {
	user := auth.ResolveSession(httpx.Cookie(c, auth.AdminCookie), "admin")
	if user == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"detail": "admin login required"})
		return nil, false
	}
	return user, true
}

func handleDebugReport(c *gin.Context) {
	var req struct {
		Type    string         `json:"type"`
		Message string         `json:"message"`
		Stack   string         `json:"stack,omitempty"`
		Context map[string]any `json:"context,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid json"})
		return
	}
	pushDebugEvent(debugEvent{
		Type:    req.Type,
		Message: req.Message,
		Stack:   req.Stack,
		Context: req.Context,
	})
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func handleDebugSnapshot(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"events": snapshotDebugEvents()})
}

func handleDebugSSE(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	ch := subscribeDebugEvents()
	defer unsubscribeDebugEvents(ch)

	c.Stream(func(w io.Writer) bool {
		select {
		case ev, ok := <-ch:
			if !ok {
				return false
			}
			b, _ := json.Marshal(ev)
			fmt.Fprintf(w, "data: %s\n\n", b)
			return true
		case <-c.Request.Context().Done():
			return false
		}
	})
}

func handleDebugPage(s *vcServer) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := requireAdmin(c); !ok {
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(s.debugHTML))
	}
}
