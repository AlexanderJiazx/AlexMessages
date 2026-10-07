package matrix

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/db"
	"alexmessage/internal/debuglog"
)

// RegisterRoutes mounts the Application Service API on the main engine. The
// namespaced form (/_matrix/app/v1/*) is what current Synapse/Conduit use; the
// unprefixed form is kept for older homeservers whose registration file points
// at the bare base URL.
func (b *Bridge) RegisterRoutes(r *gin.Engine) {
	v1 := r.Group("/_matrix/app/v1", b.requireHS)
	v1.PUT("/transactions/:txnID", b.putTransaction)
	v1.GET("/users/:userID", b.queryUser)
	v1.GET("/rooms/:roomAlias", b.queryRoom)

	legacy := r.Group("/", b.requireHS)
	legacy.PUT("/transactions/:txnID", b.putTransaction)
	legacy.GET("/users/:userID", b.queryUser)
	legacy.GET("/rooms/:roomAlias", b.queryRoom)
}

// requireHS enforces the hs_token shared secret on every AS endpoint. Both
// transports are accepted: ?access_token= (historic) and Authorization: Bearer
// (current client-server convention used by Synapse).
func (b *Bridge) requireHS(c *gin.Context) {
	tok := c.Query("access_token")
	if tok == "" {
		if h := c.GetHeader("Authorization"); len(h) > 7 && h[:7] == "Bearer " {
			tok = h[7:]
		}
	}
	if subtle.ConstantTimeCompare([]byte(tok), []byte(b.cfg.HSToken)) != 1 {
		c.AbortWithStatusJSON(http.StatusForbidden,
			gin.H{"errcode": "M_FORBIDDEN", "error": "bad hs_token"})
		return
	}
	c.Next()
}

// putTransaction handles PUT /_matrix/app/v1/transactions/{txnId}.
// The txn id is recorded first so redeliveries are cheap no-ops.
func (b *Bridge) putTransaction(c *gin.Context) {
	txnID := c.Param("txnID")
	var body struct {
		Events []hsEvent `json:"events"`
	}
	raw, err := c.GetRawData()
	if err != nil || json.Unmarshal(raw, &body) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"errcode": "M_BAD_JSON", "error": "could not parse transaction"})
		return
	}
	fresh, err := db.TryRecordMatrixTxn(txnID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"errcode": "M_UNKNOWN", "error": "internal error"})
		return
	}
	if !fresh {
		c.JSON(http.StatusOK, gin.H{}) // already applied
		return
	}
	b.processTransaction(txnID, body.Events)
	c.JSON(http.StatusOK, gin.H{})
}

// queryUser implements GET /_matrix/app/v1/users/{userId}: 200 claims the
// user id into our exclusive namespace (the HS then lets the AS control it),
// 404 declines. We claim exactly the puppets of existing local users.
func (b *Bridge) queryUser(c *gin.Context) {
	mxid := c.Param("userID")
	if mxid == b.cfg.BotMXID() {
		c.JSON(http.StatusOK, gin.H{})
		return
	}
	username, ok := b.cfg.UsernameForPuppet(mxid)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"errcode": "M_NOT_FOUND", "error": "not our namespace"})
		return
	}
	u, _ := db.GetUserByUsername(username)
	if u == nil || u.MatrixID != nil || u.Status != "approved" {
		c.JSON(http.StatusNotFound, gin.H{"errcode": "M_NOT_FOUND", "error": "no such local user"})
		return
	}
	debuglog.Emit("matrix", "debug", "user_queried",
		"Homeserver queried a puppet user", map[string]any{"mxid": mxid})
	c.JSON(http.StatusOK, gin.H{})
}

// queryRoom implements GET /_matrix/app/v1/rooms/{roomAlias}. The bridge never
// creates or responds to room aliases, so this is always 404.
func (b *Bridge) queryRoom(c *gin.Context) {
	c.JSON(http.StatusNotFound, gin.H{"errcode": "M_NOT_FOUND", "error": "aliases not supported"})
}
