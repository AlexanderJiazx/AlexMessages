package webapp

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestSafeUploadNameKeepsExtension(t *testing.T) {
	for in, want := range map[string]string{
		"报告.pdf":                          "file.pdf",
		"photo 1.JPG":                     "photo_1.JPG",
		"..pdf":                           "file.pdf",
		"noext":                           "noext",
		".bashrc":                         "file.bashrc",
		"a.tar.gz":                        "a.tar.gz",
		"x." + strings.Repeat("e", 20):    "x",
		strings.Repeat("a", 300) + ".pdf": strings.Repeat("a", 80) + ".pdf",
	} {
		if got := safeUploadName(in); got != want {
			t.Fatalf("safeUploadName(%q) = %q, want %q", in, got, want)
		}
	}
}

// exifJPEG returns a w×h JPEG with an EXIF APP1 segment that sets the
// given orientation, in the given byte order ("II" or "MM").
func exifJPEG(t *testing.T, w, h, orientation int, order string) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h)), nil); err != nil {
		t.Fatal(err)
	}
	var bo binary.ByteOrder = binary.LittleEndian
	if order == "MM" {
		bo = binary.BigEndian
	}
	tiff := make([]byte, 8+2+12+4)
	copy(tiff, order)
	bo.PutUint16(tiff[2:], 42)
	bo.PutUint32(tiff[4:], 8)
	bo.PutUint16(tiff[8:], 1)                    // one IFD entry
	bo.PutUint16(tiff[10:], 0x0112)              // Orientation
	bo.PutUint16(tiff[12:], 3)                   // SHORT
	bo.PutUint32(tiff[14:], 1)                   // count
	bo.PutUint16(tiff[18:], uint16(orientation)) // value
	seg := append([]byte("Exif\x00\x00"), tiff...)
	app1 := []byte{0xFF, 0xE1, byte((len(seg) + 2) >> 8), byte(len(seg) + 2)}
	raw := buf.Bytes()
	out := append([]byte{}, raw[:2]...)
	out = append(out, app1...)
	out = append(out, seg...)
	return append(out, raw[2:]...)
}

func TestImageDimensionsHonorExifOrientation(t *testing.T) {
	for _, order := range []string{"II", "MM"} {
		if w, h := imageDimensions(exifJPEG(t, 40, 30, 6, order), "image/jpeg"); w != 30 || h != 40 {
			t.Fatalf("%s orientation 6: got %dx%d, want 30x40", order, w, h)
		}
		if w, h := imageDimensions(exifJPEG(t, 40, 30, 1, order), "image/jpeg"); w != 40 || h != 30 {
			t.Fatalf("%s orientation 1: got %dx%d, want 40x30", order, w, h)
		}
	}
	// Truncated or garbage EXIF must not panic.
	raw := exifJPEG(t, 40, 30, 6, "II")
	for n := 0; n < 60; n++ {
		_ = jpegOrientation(raw[:n])
	}
}
