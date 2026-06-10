package webapp

import (
	"net"
	"net/url"
	"testing"
)

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return u
}

func TestParseLinkPreviewOpenGraph(t *testing.T) {
	body := []byte(`<!doctype html><html><head>
		<title>Fallback title</title>
		<meta property="og:title" content="OG Title"/>
		<meta property="og:description" content="OG Description"/>
		<meta property="og:image" content="/img/cover.png"/>
		<meta property="og:site_name" content="Example"/>
	</head><body><p>hi</p></body></html>`)
	p := parseLinkPreview(body, mustURL(t, "https://example.com/post/1"))
	if p.Title != "OG Title" {
		t.Fatalf("title = %q", p.Title)
	}
	if p.Description != "OG Description" {
		t.Fatalf("description = %q", p.Description)
	}
	if p.SiteName != "Example" {
		t.Fatalf("site_name = %q", p.SiteName)
	}
	// Relative og:image resolves against the page URL.
	if p.Image != "https://example.com/img/cover.png" {
		t.Fatalf("image = %q", p.Image)
	}
}

func TestParseLinkPreviewFallbacks(t *testing.T) {
	body := []byte(`<html><head>
		<title>  Plain Title  </title>
		<meta name="description" content="Meta description">
		<meta name="twitter:image" content="https://cdn.example.com/t.jpg">
	</head><body></body></html>`)
	p := parseLinkPreview(body, mustURL(t, "https://example.com/"))
	if p.Title != "Plain Title" {
		t.Fatalf("title = %q", p.Title)
	}
	if p.Description != "Meta description" {
		t.Fatalf("description = %q", p.Description)
	}
	if p.Image != "https://cdn.example.com/t.jpg" {
		t.Fatalf("image = %q", p.Image)
	}
}

func TestParseLinkPreviewRejectsBadImageScheme(t *testing.T) {
	body := []byte(`<html><head>
		<meta property="og:title" content="T"/>
		<meta property="og:image" content="javascript:alert(1)"/>
	</head></html>`)
	p := parseLinkPreview(body, mustURL(t, "https://example.com/"))
	if p.Image != "" {
		t.Fatalf("image = %q, want empty for javascript: scheme", p.Image)
	}
}

func TestParseLinkPreviewEmptyDocument(t *testing.T) {
	p := parseLinkPreview([]byte("not really html"), mustURL(t, "https://example.com/x"))
	if p.Title != "" || p.Image != "" || p.Description != "" {
		t.Fatalf("expected empty preview, got %+v", p)
	}
	if p.URL != "https://example.com/x" {
		t.Fatalf("url = %q", p.URL)
	}
}

func TestIsPublicIP(t *testing.T) {
	blocked := []string{"127.0.0.1", "10.0.0.8", "192.168.1.1", "172.16.5.5", "169.254.1.1", "0.0.0.0", "::1", "fe80::1", "224.0.0.1"}
	for _, s := range blocked {
		if isPublicIP(net.ParseIP(s)) {
			t.Errorf("isPublicIP(%s) = true, want false", s)
		}
	}
	allowed := []string{"93.184.216.34", "1.1.1.1", "2606:4700:4700::1111"}
	for _, s := range allowed {
		if !isPublicIP(net.ParseIP(s)) {
			t.Errorf("isPublicIP(%s) = false, want true", s)
		}
	}
}

func TestPreviewCache(t *testing.T) {
	p := LinkPreview{URL: "https://example.com", Title: "T"}
	previewCachePut("k", p)
	got, ok := previewCacheGet("k")
	if !ok || got.Title != "T" {
		t.Fatalf("cache miss: %+v %v", got, ok)
	}
	if _, ok := previewCacheGet("absent"); ok {
		t.Fatalf("unexpected cache hit")
	}
}
