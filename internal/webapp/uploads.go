package webapp

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/db"
	"alexmessage/internal/httpx"
	"alexmessage/internal/runtime"
)

var safeNameRE = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// registerUploadRoutes wires POST /api/upload (mirrors routes/uploads.py).
func registerUploadRoutes(r *gin.Engine) {
	r.POST("/api/upload", handleUpload)
}

func handleUpload(c *gin.Context) {
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
	raw, err := io.ReadAll(f)
	_ = f.Close()
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "Empty file")
		return
	}
	if len(raw) > runtime.MaxUploadBytes {
		httpx.Error(c, http.StatusRequestEntityTooLarge, "File exceeds 20MB limit")
		return
	}
	if len(raw) == 0 {
		httpx.Error(c, http.StatusBadRequest, "Empty file")
		return
	}

	original := fileHeader.Filename
	if original == "" {
		original = "file"
	}
	safe := strings.Trim(safeNameRE.ReplaceAllString(original, "_"), "._")
	if safe == "" {
		safe = "file"
	}
	token, err := tokenHex(6)
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	storedName := token + "_" + safe

	userDir, err := db.UserUploadDir(user.ID)
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	if err := os.WriteFile(filepath.Join(userDir, storedName), raw, 0o644); err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}

	contentType := fileHeader.Header.Get("Content-Type")
	if contentType == "" {
		contentType = mime.TypeByExtension(filepath.Ext(original))
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	relPath := strconv.Itoa(user.ID) + "/" + storedName
	c.JSON(http.StatusOK, gin.H{
		"name": original,
		"url":  "/uploads/" + relPath,
		"size": len(raw),
		"mime": contentType,
	})
}

// tokenHex mirrors secrets.token_hex(n): n random bytes as 2n hex chars.
func tokenHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
