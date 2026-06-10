package webapp

import (
	"bytes"
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

	"github.com/gin-gonic/gin"

	"alexmessage/internal/db"
	"alexmessage/internal/httpx"
	"alexmessage/internal/runtime"
)

// maxAvatarBytes caps a profile photo at 5 MB.
const maxAvatarBytes = 5 * 1024 * 1024

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
	fileHeader, err := c.FormFile("file")
	if err != nil {
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
	if err := os.WriteFile(filepath.Join(db.AvatarRoot, name), raw, 0o644); err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	// Replace, don't accumulate: record the new photo, then drop the previous
	// file(s). The token in the name doubles as a cache buster.
	if err := db.SetUserAvatar(user.ID, "/avatars/"+name); err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	removeAvatarFileExcept(user.ID, name)

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
	if err := db.SetUserAvatar(user.ID, ""); err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	db.DeleteUserAvatarFiles(user.ID)
	fresh, err := db.GetUserByID(user.ID)
	if err != nil || fresh == nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	profile := runtime.UserPublic(fresh)
	runtime.BroadcastProfileUpdate(profile)
	c.JSON(http.StatusOK, gin.H{"user": profile})
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
