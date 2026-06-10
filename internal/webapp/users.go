package webapp

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/auth"
	"alexmessage/internal/db"
	"alexmessage/internal/httpx"
	"alexmessage/internal/runtime"
)

// registerUserRoutes wires the user directory + contacts (mirrors routes/users.py).
func registerUserRoutes(r *gin.Engine) {
	r.GET("/api/users", handleUsersIndex)
	r.GET("/api/users/lookup", handleUsersLookup)
	r.GET("/api/contacts", handleContactsGet)
	r.POST("/api/contacts/:contact_id", handleContactAdd)
	r.DELETE("/api/contacts/:contact_id", handleContactRemove)
}

func handleUsersIndex(c *gin.Context) {
	user, ok := requireUser(c)
	if !ok {
		return
	}
	visible, err := runtime.VisibleUserIDs(user.ID)
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	known, err := runtime.AllKnownUsers()
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	out := []runtime.PublicUser{}
	for uid := range visible {
		if u, ok := known[uid]; ok {
			out = append(out, u)
		}
	}
	c.JSON(http.StatusOK, gin.H{"users": out})
}

// handleUsersLookup resolves a username so a viewer can start a new chat — the
// only way a normal user discovers another account.
func handleUsersLookup(c *gin.Context) {
	user, ok := requireUser(c)
	if !ok {
		return
	}
	target, _ := db.GetUserByUsername(auth.NormalizeUsername(c.Query("username")))
	if target == nil || target.Status != "approved" {
		httpx.Error(c, http.StatusNotFound, "No user with that username")
		return
	}
	if target.ID == user.ID {
		httpx.Error(c, http.StatusBadRequest, "That's you")
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": runtime.UserPublic(target)})
}

func handleContactsGet(c *gin.Context) {
	user, ok := requireUser(c)
	if !ok {
		return
	}
	ids, err := db.ListContacts(user.ID)
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	known, err := runtime.AllKnownUsers()
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	out := []runtime.PublicUser{}
	for _, id := range ids {
		if u, ok := known[id]; ok {
			out = append(out, u)
		}
	}
	c.JSON(http.StatusOK, gin.H{"contacts": out})
}

func handleContactAdd(c *gin.Context) {
	user, ok := requireUser(c)
	if !ok {
		return
	}
	contactID, err := strconv.Atoi(c.Param("contact_id"))
	if err != nil {
		httpx.Error(c, http.StatusNotFound, "User not found")
		return
	}
	target, _ := db.GetUserByID(contactID)
	if target == nil || target.Status != "approved" {
		httpx.Error(c, http.StatusNotFound, "User not found")
		return
	}
	if err := db.AddContact(user.ID, contactID); err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func handleContactRemove(c *gin.Context) {
	user, ok := requireUser(c)
	if !ok {
		return
	}
	contactID, err := strconv.Atoi(c.Param("contact_id"))
	if err != nil {
		httpx.Error(c, http.StatusNotFound, "User not found")
		return
	}
	if err := db.RemoveContact(user.ID, contactID); err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
