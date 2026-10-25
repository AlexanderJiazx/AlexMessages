package webapp

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/auth"
	"alexmessage/internal/db"
	"alexmessage/internal/debuglog"
	"alexmessage/internal/httpx"
	"alexmessage/internal/ota/census"
	"alexmessage/internal/runtime"
)

// registerAccountRoutes wires the self-service account endpoints: password
// change, account deletion (password-verified), and a personal data export.
func registerAccountRoutes(r *gin.Engine) {
	r.POST("/api/me/password", handleChangePassword)
	r.POST("/api/me/delete", handleDeleteAccount)
	r.GET("/api/me/export", handleExportData)
}

// handleChangePassword verifies the current password, stores a new hash, and
// rotates sessions: every other device is signed out, but this one stays in by
// retaining the authenticated cookie or bearer token.
func handleChangePassword(c *gin.Context) {
	user, ok := requireUser(c)
	if !ok {
		return
	}
	m := bindJSON(c)
	current := strField(m, "current_password")
	next := strField(m, "new_password")

	if !auth.VerifyPassword(current, user.PasswordHash) {
		httpx.Error(c, http.StatusForbidden, "Current password is incorrect")
		return
	}
	if !auth.ValidPassword(next) {
		httpx.Error(c, http.StatusBadRequest, "Password must be 8–128 chars")
		return
	}
	if current == next {
		httpx.Error(c, http.StatusBadRequest, "New password must differ from the current one")
		return
	}
	hash, err := auth.HashPassword(next)
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	token := requestSessionToken(c)
	if err := db.ChangePasswordKeepingSession(user.ID, token, hash); err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	httpx.SetSessionCookie(c, auth.UserCookie, token)
	c.JSON(http.StatusOK, gin.H{"ok": true, "session_token": token})
}

// handleDeleteAccount removes the calling user's account after verifying their
// password. The DB cascade drops messages/attachments/contacts; upload and
// avatar files are removed from disk. The last administrator can't self-delete.
// A call the user is on or ringing for ends first (endUserCall).
func handleDeleteAccount(c *gin.Context) {
	user, ok := requireUser(c)
	if !ok {
		return
	}
	m := bindJSON(c)
	if !auth.VerifyPassword(strField(m, "password"), user.PasswordHash) {
		httpx.Error(c, http.StatusForbidden, "Password is incorrect")
		return
	}
	if user.IsAdmin {
		if n, _ := db.CountAdmins(); n <= 1 {
			httpx.Error(c, http.StatusBadRequest, "Refusing to delete the last administrator")
			return
		}
	}
	endUserCall(user.ID)
	if err := db.DeleteUser(user.ID); err != nil {
		if errors.Is(err, db.ErrLastAdministrator) {
			httpx.Error(c, http.StatusBadRequest, err.Error())
		} else {
			httpx.Error(c, http.StatusInternalServerError, "internal error")
		}
		return
	}
	db.DeleteUserUploads(user.ID)     // remove storage directory on disk
	db.DeleteUserAvatarFiles(user.ID) // remove profile photo files on disk
	deleteCensusRows(user.ID)         // OTA install census rows that claim this user
	httpx.ClearSessionCookie(c, auth.UserCookie)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// deleteCensusRows removes the OTA census rows whose am_uid is the user
// (OTAUpdatePlan.md, D8 item 5). The account is already gone, so a failure
// is reported to the debug console instead of failing the request.
func deleteCensusRows(userID int) {
	cdb, err := census.Shared(otaCensusPath(), false)
	if err == nil && cdb != nil {
		err = cdb.DeleteUser(userID)
	}
	if err != nil {
		debuglog.Emit("messages", "error", "ota.census_delete_failed", "Census rows of a deleted account could not be removed", map[string]any{"user_id": userID, "err": err.Error()})
	}
}

// censusRowsOf returns the OTA census rows whose am_uid is the user, for the
// data export; an empty list when there is no census.
func censusRowsOf(userID int) []census.Install {
	cdb, err := census.Shared(otaCensusPath(), false)
	if err != nil || cdb == nil {
		return []census.Install{}
	}
	rows, err := cdb.ForUser(userID)
	if err != nil {
		return []census.Install{}
	}
	return rows
}

// exportConversation is one DM thread in the data export.
type exportConversation struct {
	Channel  string              `json:"channel"`
	Peer     *runtime.PublicUser `json:"peer"`
	Messages []db.HistoryMessage `json:"messages"`
}

// exportMaxMessages caps how many messages per conversation a single export
// pulls — generous enough to be a complete archive in practice.
const exportMaxMessages = 100000

// handleExportData streams a JSON archive of the caller's profile, contacts,
// and every DM conversation they participate in. Served as a download.
func handleExportData(c *gin.Context) {
	user, ok := requireUser(c)
	if !ok {
		return
	}
	known, _ := runtime.AllKnownUsers()

	// Contacts (resolved to public profiles where still known).
	contactIDs, _ := db.ListContacts(user.ID)
	contacts := []runtime.PublicUser{}
	for _, id := range contactIDs {
		if u, ok := known[id]; ok {
			contacts = append(contacts, u)
		}
	}

	// Partner set = contacts ∪ anyone we've ever messaged.
	partners := map[int]struct{}{}
	for _, id := range contactIDs {
		partners[id] = struct{}{}
	}
	if p, err := db.DMPartnerIDs(user.ID); err == nil {
		for id := range p {
			partners[id] = struct{}{}
		}
	}

	conversations := []exportConversation{}
	for pid := range partners {
		ch := db.DMChannelID(user.ID, pid)
		msgs, err := db.FetchChannelWindow(ch, exportMaxMessages, nil, nil)
		if err != nil {
			continue
		}
		if len(msgs) == 0 {
			continue
		}
		var peer *runtime.PublicUser
		if u, ok := known[pid]; ok {
			pu := u
			peer = &pu
		}
		conversations = append(conversations, exportConversation{
			Channel:  ch,
			Peer:     peer,
			Messages: msgs,
		})
	}

	profile := runtime.UserPublic(user)
	out := gin.H{
		"exported_at": time.Now().UTC().Format(time.RFC3339),
		"account": gin.H{
			"id":           user.ID,
			"username":     user.Username,
			"display_name": user.DisplayName,
			"bio":          user.Bio,
			"avatar":       profile.Avatar,
			"is_admin":     user.IsAdmin,
			"created_at":   user.CreatedAt,
			"approved_at":  user.ApprovedAt,
		},
		"contacts":      contacts,
		"conversations": conversations,
		// The OTA update census rows whose claimed user is this account.
		"ota_installs": censusRowsOf(user.ID),
	}

	filename := fmt.Sprintf("alex-messages-export-%s.json", user.Username)
	c.Header("Content-Disposition", `attachment; filename="`+filename+`"`)
	c.IndentedJSON(http.StatusOK, out)
}
