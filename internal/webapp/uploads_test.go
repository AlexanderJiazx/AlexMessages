package webapp

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/db"
)

func TestImageDimensions(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 31, 17))); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	w, h := imageDimensions(buf.Bytes(), "image/png")
	if w != 31 || h != 17 {
		t.Fatalf("dimensions = %dx%d, want 31x17", w, h)
	}

	// Non-image mime: skipped without decoding.
	if w, h := imageDimensions(buf.Bytes(), "application/pdf"); w != 0 || h != 0 {
		t.Fatalf("non-image dims = %dx%d, want 0x0", w, h)
	}
	// Image mime but undecodable bytes: degrade to unknown.
	if w, h := imageDimensions([]byte("junk"), "image/png"); w != 0 || h != 0 {
		t.Fatalf("junk dims = %dx%d, want 0x0", w, h)
	}
}

func TestClampDimension(t *testing.T) {
	cases := []struct {
		in   any
		want int
	}{
		{float64(640), 640},
		{float64(0), 0},
		{float64(-5), 0},
		{float64(99999), 0},
		{"640", 0},
		{nil, 0},
	}
	for _, c := range cases {
		if got := clampDimension(c.in); got != c.want {
			t.Errorf("clampDimension(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

// Go's sniffer names Ogg files application/ogg, never audio/ogg. An Ogg voice
// message must still be served inline, with an audio type players accept.
func TestOggUploadServedInlineAsAudio(t *testing.T) {
	gin.SetMode(gin.TestMode)
	initTranscribeTestDB(t)
	a := reviewUser(t, "alice")
	b := reviewUser(t, "bob")
	dir, err := db.UserUploadDir(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	name := "abc_voice-message.ogg"
	body := append([]byte("OggS\x00\x02"), make([]byte, 64)...)
	if err := os.WriteFile(filepath.Join(dir, name), body, 0600); err != nil {
		t.Fatal(err)
	}
	url := fmt.Sprintf("/uploads/%d/%s", a.ID, name)
	att := db.Attachment{Name: "voice-message.ogg", URL: url, Size: int64(len(body)), Mime: "audio/ogg"}
	if _, err := db.InsertMessageWithAttachments("ogg", db.DMChannelID(a.ID, b.ID), &a.ID, "", nil, "message", "ogg", []db.Attachment{att}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+sessionFor(t, b.ID))
	w := httptest.NewRecorder()
	NewEngine().ServeHTTP(w, req)
	if w.Code != 200 || w.Header().Get("Content-Type") != "audio/ogg" || w.Header().Get("Content-Disposition") != "" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("status=%d headers=%v", w.Code, w.Header())
	}
}
