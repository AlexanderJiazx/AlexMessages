package webapp

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/db"
	"alexmessage/internal/httpx"
	"alexmessage/internal/runtime"
)

// registerMeRoutes wires GET/PATCH /api/me (mirrors routes/me.py).
func registerMeRoutes(r *gin.Engine) {
	r.GET("/api/me", handleGetMe)
	r.PATCH("/api/me", handlePatchMe)
}

func handleGetMe(c *gin.Context) {
	user, ok := requireUser(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": runtime.UserPublic(user), "is_admin": user.IsAdmin})
}

func handlePatchMe(c *gin.Context) {
	user, ok := requireUser(c)
	if !ok {
		return
	}
	m := bindJSON(c)

	// display_name: trim + cap 40; empty falls back to the existing name.
	var displayName *string
	if raw, isStr := isStr(m, "display_name"); isStr {
		v := truncateRunes(trimSpace(raw), 40)
		if v == "" {
			v = user.DisplayName
		}
		displayName = &v
	}
	// bio: trim + cap 280; empty is allowed.
	var bio *string
	if raw, isStr := isStr(m, "bio"); isStr {
		v := truncateRunes(trimSpace(raw), 280)
		bio = &v
	}

	if err := db.UpdateUserProfile(user.ID, displayName, bio); err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	fresh, err := db.GetUserByID(user.ID)
	if err != nil || fresh == nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	profile := runtime.UserPublic(fresh)
	runtime.BroadcastProfileUpdate(profile)
	c.JSON(http.StatusOK, gin.H{"user": profile})
}
