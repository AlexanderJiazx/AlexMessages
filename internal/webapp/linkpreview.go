package webapp

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/net/html"

	"alexmessage/internal/httpx"
)

// Link previews: GET /api/link-preview?url=… fetches a page server-side (the
// browser can't, cross-origin) and returns its OpenGraph/title metadata. The
// fetcher is locked down because it makes requests on behalf of users: only
// http/https, only public addresses (checked at dial time so DNS games and
// redirects can't bypass it), small response cap, short timeout.

const (
	linkPreviewMaxBody  = 512 * 1024
	linkPreviewTimeout  = 8 * time.Second
	linkPreviewCacheTTL = 10 * time.Minute
	linkPreviewCacheMax = 512
)

// LinkPreview is the wire shape returned to the client.
type LinkPreview struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Image       string `json:"image"`
	SiteName    string `json:"site_name"`
}

// ---------- cache ----------

type previewCacheEntry struct {
	preview LinkPreview
	expires time.Time
}

var (
	previewCacheMu sync.Mutex
	previewCache   = map[string]previewCacheEntry{}
)

func previewCacheGet(key string) (LinkPreview, bool) {
	previewCacheMu.Lock()
	defer previewCacheMu.Unlock()
	e, ok := previewCache[key]
	if !ok || time.Now().After(e.expires) {
		return LinkPreview{}, false
	}
	return e.preview, true
}

func previewCachePut(key string, p LinkPreview) {
	previewCacheMu.Lock()
	defer previewCacheMu.Unlock()
	if len(previewCache) >= linkPreviewCacheMax {
		// Simple pressure valve: drop expired entries; if still full, reset.
		now := time.Now()
		for k, e := range previewCache {
			if now.After(e.expires) {
				delete(previewCache, k)
			}
		}
		if len(previewCache) >= linkPreviewCacheMax {
			previewCache = map[string]previewCacheEntry{}
		}
	}
	previewCache[key] = previewCacheEntry{preview: p, expires: time.Now().Add(linkPreviewCacheTTL)}
}

// ---------- SSRF-guarded HTTP client ----------

// isPublicIP rejects loopback, private, link-local, multicast, and unspecified
// addresses — anything that could reach internal services.
func isPublicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified())
}

// guardedDialContext resolves the host itself and only dials public IPs, so
// the check can't be bypassed via DNS tricks or redirects (the transport is
// reused for every hop).
func guardedDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	for _, ip := range ips {
		if !isPublicIP(ip) {
			continue
		}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
	}
	return nil, fmt.Errorf("no public address for %s", host)
}

var linkPreviewClient = &http.Client{
	Timeout: linkPreviewTimeout,
	Transport: &http.Transport{
		DialContext:       guardedDialContext,
		MaxIdleConns:      4,
		DisableKeepAlives: true,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects")
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return fmt.Errorf("redirect to unsupported scheme")
		}
		return nil
	},
}

// ---------- route ----------

// registerLinkPreviewRoutes wires GET /api/link-preview.
func registerLinkPreviewRoutes(r *gin.Engine) {
	r.GET("/api/link-preview", handleLinkPreview)
}

func handleLinkPreview(c *gin.Context) {
	if _, ok := requireUser(c); !ok {
		return
	}
	raw := strings.TrimSpace(c.Query("url"))
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		httpx.Error(c, http.StatusBadRequest, "Invalid URL")
		return
	}

	if p, ok := previewCacheGet(raw); ok {
		c.JSON(http.StatusOK, p)
		return
	}

	preview := fetchLinkPreview(c.Request.Context(), u)
	previewCachePut(raw, preview)
	c.JSON(http.StatusOK, preview)
}

// fetchLinkPreview fetches the URL and extracts metadata. Failures degrade to
// an empty preview (the client just doesn't render a card) — never an error.
func fetchLinkPreview(ctx context.Context, u *url.URL) LinkPreview {
	empty := LinkPreview{URL: u.String()}
	ctx, cancel := context.WithTimeout(ctx, linkPreviewTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return empty
	}
	req.Header.Set("User-Agent", "AlexMessage-LinkPreview/1.0")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	res, err := linkPreviewClient.Do(req)
	if err != nil {
		return empty
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return empty
	}
	ctype := res.Header.Get("Content-Type")
	if i := strings.IndexByte(ctype, ';'); i >= 0 {
		ctype = ctype[:i]
	}
	ctype = strings.TrimSpace(strings.ToLower(ctype))
	// A direct link to an image previews as the image itself.
	if strings.HasPrefix(ctype, "image/") {
		p := empty
		p.Image = res.Request.URL.String()
		return p
	}
	if ctype != "text/html" && ctype != "application/xhtml+xml" {
		return empty
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, linkPreviewMaxBody))
	if err != nil && len(body) == 0 {
		return empty
	}
	return parseLinkPreview(body, res.Request.URL)
}

// parseLinkPreview pulls OpenGraph (and fallback) metadata out of an HTML
// document. base resolves relative image URLs.
func parseLinkPreview(body []byte, base *url.URL) LinkPreview {
	p := LinkPreview{URL: base.String()}
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return p
	}

	var fallbackTitle, fallbackDesc, twitterImage string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "title":
				if fallbackTitle == "" && n.FirstChild != nil && n.FirstChild.Type == html.TextNode {
					fallbackTitle = strings.TrimSpace(n.FirstChild.Data)
				}
			case "meta":
				var key, content string
				for _, a := range n.Attr {
					switch strings.ToLower(a.Key) {
					case "property", "name":
						if key == "" {
							key = strings.ToLower(strings.TrimSpace(a.Val))
						}
					case "content":
						content = strings.TrimSpace(a.Val)
					}
				}
				if content == "" {
					break
				}
				switch key {
				case "og:title":
					if p.Title == "" {
						p.Title = content
					}
				case "og:description":
					if p.Description == "" {
						p.Description = content
					}
				case "og:image", "og:image:url", "og:image:secure_url":
					if p.Image == "" {
						p.Image = content
					}
				case "og:site_name":
					if p.SiteName == "" {
						p.SiteName = content
					}
				case "twitter:image", "twitter:image:src":
					if twitterImage == "" {
						twitterImage = content
					}
				case "description":
					if fallbackDesc == "" {
						fallbackDesc = content
					}
				}
			}
			// Metadata lives in <head>; don't walk the whole body.
			if n.Data == "body" {
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	if p.Title == "" {
		p.Title = fallbackTitle
	}
	if p.Description == "" {
		p.Description = fallbackDesc
	}
	if p.Image == "" {
		p.Image = twitterImage
	}
	p.Title = truncateRunes(p.Title, 200)
	p.Description = truncateRunes(p.Description, 300)
	p.SiteName = truncateRunes(p.SiteName, 100)
	// Resolve a relative image and refuse non-http(s) schemes.
	if p.Image != "" {
		if img, err := base.Parse(p.Image); err == nil && (img.Scheme == "http" || img.Scheme == "https") {
			p.Image = img.String()
		} else {
			p.Image = ""
		}
	}
	return p
}
