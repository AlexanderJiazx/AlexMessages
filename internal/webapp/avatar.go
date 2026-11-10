package webapp

import (
	"bytes"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/db"
	"alexmessage/internal/httpx"
	"alexmessage/internal/runtime"
)

// maxAvatarBytes caps a profile photo at 5 MB.
const maxAvatarBytes = 5 * 1024 * 1024

// avatarLocks serializes the avatar changes of each user (key: user id,
// value: *sync.Mutex). Without it, two concurrent changes could each delete
// the file the other one just recorded in the database.
var avatarLocks sync.Map

func lockAvatar(userID int) func() {
	m, _ := avatarLocks.LoadOrStore(userID, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// avatarExtByMime maps the sniffed content type to the stored extension.
var avatarExtByMime = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// registerAvatarRoutes wires the profile-photo endpoints.
func registerAvatarRoutes(r *gin.Engine) {
	r.POST("/api/me/avatar", handleSetAvatar)
	r.DELETE("/api/me/avatar", handleDeleteAvatar)
}

func handleSetAvatar(c *gin.Context) {
	user, ok := requireUser(c)
	if !ok {
		return
	}
	// The photo plus 1 MB of multipart overhead; a larger body stops being
	// read at that point.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxAvatarBytes+1024*1024)
	defer func() {
		if c.Request.MultipartForm != nil {
			_ = c.Request.MultipartForm.RemoveAll()
		}
	}()
	fileHeader, err := c.FormFile("file")
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpx.Error(c, http.StatusRequestEntityTooLarge, "Photo exceeds 5MB limit")
			return
		}
		httpx.Error(c, http.StatusBadRequest, "Empty file")
		return
	}
	f, err := fileHeader.Open()
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "Empty file")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxAvatarBytes+1))
	_ = f.Close()
	if err != nil || len(raw) == 0 {
		httpx.Error(c, http.StatusBadRequest, "Empty file")
		return
	}
	if len(raw) > maxAvatarBytes {
		httpx.Error(c, http.StatusRequestEntityTooLarge, "Photo exceeds 5MB limit")
		return
	}

	// Trust the bytes, not the declared content type.
	mimeType := http.DetectContentType(raw)
	ext, supported := avatarExtByMime[mimeType]
	if !supported {
		httpx.Error(c, http.StatusBadRequest, "Use a PNG, JPEG, GIF, or WebP image")
		return
	}
	// For formats the stdlib can decode, reject files that don't actually parse.
	if mimeType != "image/webp" {
		if _, _, err := image.DecodeConfig(bytes.NewReader(raw)); err != nil {
			httpx.Error(c, http.StatusBadRequest, "That image can't be read")
			return
		}
	}

	token, err := tokenHex(6)
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	name := strconv.Itoa(user.ID) + "_" + token + ext
	unlock := lockAvatar(user.ID)
	defer unlock()
	if err := os.WriteFile(filepath.Join(db.AvatarRoot, name), raw, 0o644); err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	// Replace, don't accumulate: record the new photo, then drop the previous
	// file(s). The token in the name doubles as a cache buster.
	if err := db.SetUserAvatar(user.ID, "/avatars/"+name); err != nil {
		_ = os.Remove(filepath.Join(db.AvatarRoot, name))
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	pruneAvatarFiles(user.ID)

	fresh, err := db.GetUserByID(user.ID)
	if err != nil || fresh == nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	profile := runtime.UserPublic(fresh)
	runtime.BroadcastProfileUpdate(profile)
	c.JSON(http.StatusOK, gin.H{"user": profile})
}

func handleDeleteAvatar(c *gin.Context) {
	user, ok := requireUser(c)
	if !ok {
		return
	}
	unlock := lockAvatar(user.ID)
	defer unlock()
	if err := db.SetUserAvatar(user.ID, ""); err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	pruneAvatarFiles(user.ID)
	fresh, err := db.GetUserByID(user.ID)
	if err != nil || fresh == nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	profile := runtime.UserPublic(fresh)
	runtime.BroadcastProfileUpdate(profile)
	c.JSON(http.StatusOK, gin.H{"user": profile})
}

// pruneAvatarFiles deletes every avatar file of the user except the one the
// database row names now. It deletes nothing when that row can't be read.
// The caller holds lockAvatar(userID).
func pruneAvatarFiles(userID int) {
	u, err := db.GetUserByID(userID)
	if err != nil || u == nil {
		return
	}
	removeAvatarFileExcept(userID, strings.TrimPrefix(u.Avatar, "/avatars/"))
}

// removeAvatarFileExcept deletes every avatar file for the user except keep.
func removeAvatarFileExcept(userID int, keep string) {
	entries, err := os.ReadDir(db.AvatarRoot)
	if err != nil {
		return
	}
	prefix := strconv.Itoa(userID) + "_"
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), prefix) && e.Name() != keep {
			_ = os.Remove(filepath.Join(db.AvatarRoot, e.Name()))
		}
	}
}
