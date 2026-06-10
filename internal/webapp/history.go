package webapp

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/db"
	"alexmessage/internal/httpx"
)

const (
	historyPageLimit = 50
	historyMaxLimit  = 200
)

// registerHistoryRoutes wires the lazy-history fetch (mirrors routes/history.py).
func registerHistoryRoutes(r *gin.Engine) {
	r.GET("/api/history/:channel", handleHistory)
}

func handleHistory(c *gin.Context) {
	user, ok := requireUser(c)
	if !ok {
		return
	}
	channel := c.Param("channel")

	a, b, okDM := db.ParseDMChannel(channel)
	if !okDM || (a != user.ID && b != user.ID) {
		httpx.Error(c, http.StatusNotFound, "Channel not found")
		return
	}

	limit := historyPageLimit
	if raw := c.Query("limit"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			limit = v
		}
	}
	if limit <= 0 {
		limit = historyPageLimit
	}
	if limit > historyMaxLimit {
		limit = historyMaxLimit
	}

	var before *int64
	if raw := c.Query("before"); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil {
			before = &v
		}
	}

	var afterTS *int64
	state, _ := db.GetDMState(user.ID, channel)
	if state.ClearedAt != 0 {
		cleared := state.ClearedAt
		afterTS = &cleared
	}

	messages, err := db.FetchChannelWindow(channel, limit, before, afterTS)
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"channel":  channel,
		"messages": messages,
		"has_more": len(messages) == limit,
	})
}
