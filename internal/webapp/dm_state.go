package webapp

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/db"
	"alexmessage/internal/httpx"
	"alexmessage/internal/runtime"
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

// handleMarkRead marks a DM read. An optional JSON body {"position": <int>}
// names the newest message position the client holds: the read position
// becomes the newest message at or below it, so the server never claims a
// message the client has not received. Without a position (no body, or a body
// without the field) the thread is read up to now, as before. The read
// position never moves backwards.
func handleMarkRead(c *gin.Context) {
	user, ok := requireUser(c)
	if !ok {
		return
	}
	channel := c.Param("channel")
	if !validateDMChannel(c, channel, user.ID) {
		return
	}
	position, hasPosition, valid := readPositionFromBody(c)
	if !valid {
		httpx.Error(c, http.StatusBadRequest, "position must be a whole number of 0 or more")
		return
	}
	var state db.DMState
	var err error
	if hasPosition {
		state, err = db.SetDMReadThrough(user.ID, channel, position)
	} else {
		latest, _ := db.ChannelLatestMessageTS(channel)
		cutoff := latest
		if now := db.NowTS(); now > cutoff {
			cutoff = now
		}
		if err = db.SetDMLastRead(user.ID, channel, cutoff); err == nil {
			state, err = db.GetDMState(user.ID, channel)
		}
	}
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	// Read receipt: tell both participants (the peer shows the "Read" remark;
	// the reader's other tabs clear their unread state).
	runtime.Broadcast(
		gin.H{"type": "dm_read", "channel": channel, "user_id": user.ID, "last_read_at": state.LastReadAt, "last_read_position": state.LastReadPosition},
		runtime.RecipientsForChannel(channel),
	)
	c.JSON(http.StatusOK, gin.H{"ok": true, "last_read_at": state.LastReadAt, "last_read_position": state.LastReadPosition})
}

// readPositionFromBody reads the optional {"position": n} body of mark-read.
// hasPosition is false for an empty body and for a JSON object without the
// field (or with null); valid is false for a body that is not a JSON object
// or a position that is not a whole number of 0 or more.
func readPositionFromBody(c *gin.Context) (position int64, hasPosition, valid bool) {
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 4096))
	if err != nil {
		return 0, false, false
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return 0, false, true
	}
	var body struct {
		Position *json.Number `json:"position"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&body); err != nil {
		return 0, false, false
	}
	if body.Position == nil {
		return 0, false, true
	}
	n, err := strconv.ParseInt(body.Position.String(), 10, 64)
	if err != nil || n < 0 {
		return 0, false, false
	}
	return n, true, true
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
	state, err := db.GetDMState(user.ID, channel)
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	// The read receipt is unchanged; only force_unread is set.
	c.JSON(http.StatusOK, gin.H{"ok": true, "force_unread": true, "last_read_at": state.LastReadAt, "last_read_position": state.LastReadPosition})
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
