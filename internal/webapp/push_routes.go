package webapp

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/db"
	"alexmessage/internal/httpx"
	"alexmessage/internal/push"
)

// registerPushRoutes wires the Web Push subscription endpoints (routes/push.py).
func registerPushRoutes(r *gin.Engine) {
	r.GET("/api/push/public-key", handlePublicKey)
	r.POST("/api/push/subscribe", handleSubscribe)
	r.POST("/api/push/unsubscribe", handleUnsubscribe)
}

func handlePublicKey(c *gin.Context) {
	if _, ok := requireUser(c); !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"public_key": push.PublicKey()})
}

func handleSubscribe(c *gin.Context) {
	user, ok := requireUser(c)
	if !ok {
		return
	}
	m := bindJSON(c)
	endpoint := trimSpace(strField(m, "endpoint"))
	var p256dh, authToken string
	if keys, ok := m["keys"].(map[string]any); ok {
		p256dh = trimSpace(strField(keys, "p256dh"))
		authToken = trimSpace(strField(keys, "auth"))
	}
	userAgent := truncateRunes(strField(m, "user_agent"), 300)
	if endpoint == "" || p256dh == "" || authToken == "" {
		httpx.Error(c, http.StatusBadRequest, "Subscription is missing required fields")
		return
	}
	if err := db.UpsertPushSubscription(user.ID, endpoint, p256dh, authToken, userAgent); err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func handleUnsubscribe(c *gin.Context) {
	if _, ok := requireUser(c); !ok {
		return
	}
	m := bindJSON(c)
	endpoint := strings.TrimSpace(strField(m, "endpoint"))
	if endpoint == "" {
		httpx.Error(c, http.StatusBadRequest, "Missing endpoint")
		return
	}
	if err := db.RemovePushSubscription(endpoint); err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
