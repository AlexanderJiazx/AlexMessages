package webapp

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/db"
	"alexmessage/internal/httpx"
)

// registerDMStateRoutes wires the per-user DM-state endpoints (routes/dm_state.py).
// These are personal flags — they only affect the calling user's view.
func registerDMStateRoutes(r *gin.Engine) {
	r.POST("/api/dm/:channel/pin", handlePinDM)
	r.POST("/api/dm/:channel/unpin", handleUnpinDM)
	r.POST("/api/dm/:channel/read", handleMarkRead)
	r.POST("/api/dm/:channel/unread", handleMarkUnread)
	r.DELETE("/api/dm/:channel", handleDeleteDM)
}

// validateDMChannel ensures the channel is a DM the user participates in.
func validateDMChannel(c *gin.Context, channel string, userID int) bool {
	a, b, ok := db.ParseDMChannel(channel)
	if !ok || (a != userID && b != userID) {
		httpx.Error(c, http.StatusNotFound, "Not a DM you can access")
		return false
	}
	return true
}

func handlePinDM(c *gin.Context) {
	user, ok := requireUser(c)
	if !ok {
		return
	}
	channel := c.Param("channel")
	if !validateDMChannel(c, channel, user.ID) {
		return
	}
	if err := db.SetDMPinned(user.ID, channel, true); err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "pinned": true})
}

func handleUnpinDM(c *gin.Context) {
	user, ok := requireUser(c)
	if !ok {
		return
	}
	channel := c.Param("channel")
	if !validateDMChannel(c, channel, user.ID) {
		return
	}
	if err := db.SetDMPinned(user.ID, channel, false); err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "pinned": false})
}

func handleMarkRead(c *gin.Context) {
	user, ok := requireUser(c)
	if !ok {
		return
	}
	channel := c.Param("channel")
	if !validateDMChannel(c, channel, user.ID) {
		return
	}
	latest, _ := db.ChannelLatestMessageTS(channel)
	cutoff := latest
	if now := db.NowTS(); now > cutoff {
		cutoff = now
	}
	if err := db.SetDMLastRead(user.ID, channel, cutoff); err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "last_read_at": cutoff})
}

func handleMarkUnread(c *gin.Context) {
	user, ok := requireUser(c)
	if !ok {
		return
	}
	channel := c.Param("channel")
	if !validateDMChannel(c, channel, user.ID) {
		return
	}
	if err := db.SetDMUnread(user.ID, channel); err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "last_read_at": 0})
}

// handleDeleteDM is the per-user clear: hide every existing message in this
// thread from this user. The other participant's view is untouched.
func handleDeleteDM(c *gin.Context) {
	user, ok := requireUser(c)
	if !ok {
		return
	}
	channel := c.Param("channel")
	if !validateDMChannel(c, channel, user.ID) {
		return
	}
	latest, _ := db.ChannelLatestMessageTS(channel)
	cutoff := latest
	if now := db.NowTS(); now > cutoff {
		cutoff = now
	}
	if err := db.ClearDMForUser(user.ID, channel, cutoff); err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "cleared_at": cutoff})
}
