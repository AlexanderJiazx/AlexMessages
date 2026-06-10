package webapp

import (
	"bytes"
	"image"
	"image/png"
	"testing"
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
