package webapp

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
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

func validUploadFile(userID int, name string) bool {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\\?#`) {
		return false
	}
	info, err := os.Lstat(filepath.Join(db.UploadRoot, strconv.Itoa(userID), name))
	return err == nil && info.Mode().IsRegular()
}

// registerUploadRoutes wires POST /api/upload (mirrors routes/uploads.py).
func registerUploadRoutes(r *gin.Engine) {
	r.POST("/api/upload", handleUpload)
	r.GET("/uploads/:uid/:name", handleGetUpload)
	r.HEAD("/uploads/:uid/:name", handleGetUpload)
}

func handleUpload(c *gin.Context) {
	user, ok := requireUser(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, runtime.MaxUploadBytes+1024*1024)
	defer func() {
		if c.Request.MultipartForm != nil {
			_ = c.Request.MultipartForm.RemoveAll()
		}
	}()
	fileHeader, err := c.FormFile("file")
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpx.Error(c, http.StatusRequestEntityTooLarge, "File exceeds 20MB limit")
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
	raw, err := io.ReadAll(io.LimitReader(f, runtime.MaxUploadBytes+1))
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

	width, height := imageDimensions(raw, contentType)

	relPath := strconv.Itoa(user.ID) + "/" + storedName
	c.JSON(http.StatusOK, gin.H{
		"name":   original,
		"url":    "/uploads/" + relPath,
		"size":   len(raw),
		"mime":   contentType,
		"width":  width,
		"height": height,
	})
}

// handleGetUpload only serves the uploader and participants of a message that
// references this exact file. Unsent uploads are private to their uploader.
func handleGetUpload(c *gin.Context) {
	user, ok := requireUser(c)
	if !ok {
		return
	}
	uid, err := strconv.Atoi(c.Param("uid"))
	name := c.Param("name")
	if err != nil || uid <= 0 || name == "." || name == ".." || strings.ContainsAny(name, `/\\`) {
		httpx.Error(c, http.StatusNotFound, "File not found")
		return
	}
	rel := strconv.Itoa(uid) + "/" + name
	allowed, err := db.CanAccessUpload(user.ID, uid, rel)
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, "internal error")
		return
	}
	if !allowed {
		httpx.Error(c, http.StatusNotFound, "File not found")
		return
	}
	f, err := os.Open(filepath.Join(db.UploadRoot, strconv.Itoa(uid), name))
	if err != nil {
		httpx.Error(c, http.StatusNotFound, "File not found")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		httpx.Error(c, http.StatusNotFound, "File not found")
		return
	}
	var sample [512]byte
	n, _ := f.Read(sample[:])
	_, _ = f.Seek(0, io.SeekStart)
	contentType := http.DetectContentType(sample[:n])
	// Only inert, browser-decodable media may be inline. Everything else is a
	// download, even when its extension or claimed MIME says it is an image.
	inline := contentType == "image/png" || contentType == "image/jpeg" || contentType == "image/gif" || contentType == "image/webp" ||
		contentType == "video/mp4" || contentType == "video/webm" || contentType == "audio/mpeg" || contentType == "audio/wave" || contentType == "application/ogg"
	if contentType == "application/ogg" {
		// The sniffer names every Ogg file application/ogg; audio players
		// expect the audio type (Ogg/Opus voice messages).
		contentType = "audio/ogg"
	}
	if !inline {
		contentType = "application/octet-stream"
		c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	}
	c.Header("Content-Type", contentType)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "sandbox; default-src 'none'")
	c.Header("Cache-Control", "private, no-store")
	c.Header("Referrer-Policy", "no-referrer")
	http.ServeContent(c.Writer, c.Request, name, info.ModTime(), f)
}

// imageDimensions returns the pixel size of an image upload, or (0, 0) when
// the file isn't an image or the format isn't decodable (e.g. webp). The
// client uses these to render a placeholder with the final layout size.
func imageDimensions(raw []byte, contentType string) (int, int) {
	if !strings.HasPrefix(contentType, "image/") {
		return 0, 0
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

// tokenHex mirrors secrets.token_hex(n): n random bytes as 2n hex chars.
func tokenHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
