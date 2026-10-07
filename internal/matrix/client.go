package matrix

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HTTPError is a non-2xx Matrix API response. Status/Errcode drive the
// retry classifier in bridge.go.
type HTTPError struct {
	Status  int
	Errcode string
	Body    string
}

func (e *HTTPError) Error() string {
	if e.Errcode != "" {
		return fmt.Sprintf("matrix http %d: %s", e.Status, e.Errcode)
	}
	return fmt.Sprintf("matrix http %d: %s", e.Status, e.Body)
}

// Retryable reports whether the call is worth retrying: network failures,
// 5xx and rate limits (429) are transient; other 4xx are terminal.
func (e *HTTPError) Retryable() bool {
	return e.Status >= 500 || e.Status == http.StatusTooManyRequests
}

// hsClient is a thin client for the small slice of the Client-Server and
// Media APIs an appservice needs. All requests carry the as_token; puppet
// operations pass ?user_id= to masquerade as that user.
type hsClient struct {
	cfg  *Config
	http *http.Client
}

func newHSClient(cfg *Config) *hsClient {
	return &hsClient{cfg: cfg, http: &http.Client{Timeout: 30 * time.Second}}
}

// do issues one authenticated request. asUser, when non-empty, is the
// masqueraded MXID (appservice user_id query parameter).
func (h *hsClient) do(ctx context.Context, method, path string, asUser string, query url.Values, body any, out any) error {
	u := h.cfg.HomeserverURL + path
	if query == nil {
		query = url.Values{}
	}
	if asUser != "" {
		query.Set("user_id", asUser)
	}
	if enc := query.Encode(); enc != "" {
		u += "?" + enc
	}
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+h.cfg.ASToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode/100 != 2 {
		he := &HTTPError{Status: resp.StatusCode, Body: string(raw)}
		var em map[string]any
		if json.Unmarshal(raw, &em) == nil {
			if ec, _ := em["errcode"].(string); ec != "" {
				he.Errcode = ec
			}
		}
		return he
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// register creates the puppet user (or returns M_USER_IN_USE, which is fine).
func (h *hsClient) register(ctx context.Context, localpart string) error {
	err := h.do(ctx, "POST", "/_matrix/client/v3/register", "", nil,
		map[string]any{"type": "m.login.application_service", "username": localpart}, nil)
	if he, ok := err.(*HTTPError); ok && he.Errcode == "M_USER_IN_USE" {
		return nil
	}
	return err
}

// createRoom makes a DM room as asUser and invites the remote user.
// trusted_private_chat gives both sides op-level permissions (needed for
// edits/redaction parity with real DM clients).
func (h *hsClient) createRoom(ctx context.Context, asUser, inviteMXID string) (string, error) {
	var out struct {
		RoomID string `json:"room_id"`
	}
	err := h.do(ctx, "POST", "/_matrix/client/v3/createRoom", asUser, nil,
		map[string]any{
			"is_direct": true,
			"invite":    []string{inviteMXID},
			"preset":    "trusted_private_chat",
		}, &out)
	return out.RoomID, err
}

// joinRoom joins a room the puppet was invited to.
func (h *hsClient) joinRoom(ctx context.Context, roomID, asUser string) error {
	var out struct {
		RoomID string `json:"room_id"`
	}
	return h.do(ctx, "POST",
		"/_matrix/client/v3/rooms/"+url.PathEscape(roomID)+"/join",
		asUser, nil, map[string]any{}, &out)
}

// sendEvent sends a timeline event and returns its event id. txnID makes the
// send idempotent on the homeserver side.
func (h *hsClient) sendEvent(ctx context.Context, roomID, eventType, txnID, asUser string, content any) (string, error) {
	var out struct {
		EventID string `json:"event_id"`
	}
	path := "/_matrix/client/v3/rooms/" + url.PathEscape(roomID) +
		"/send/" + url.PathEscape(eventType) + "/" + url.PathEscape(txnID)
	err := h.do(ctx, "PUT", path, asUser, nil, content, &out)
	return out.EventID, err
}

// sendReadReceipt marks the room as read up to eventID for the puppet.
func (h *hsClient) sendReadReceipt(ctx context.Context, roomID, eventID, asUser string) error {
	path := "/_matrix/client/v3/rooms/" + url.PathEscape(roomID) +
		"/receipt/m.read/" + url.PathEscape(eventID)
	return h.do(ctx, "POST", path, asUser, nil, map[string]any{}, nil)
}

// upload pushes bytes to the homeserver's media repo and returns the mxc:// URI.
func (h *hsClient) upload(ctx context.Context, filename, mime string, data []byte) (string, error) {
	var out struct {
		ContentURI string `json:"content_uri"`
	}
	q := url.Values{"filename": {filename}}
	req, err := http.NewRequestWithContext(ctx, "POST",
		h.cfg.HomeserverURL+"/_matrix/media/v3/upload?"+q.Encode(), bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+h.cfg.ASToken)
	req.Header.Set("Content-Type", mime)
	resp, err := h.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return "", &HTTPError{Status: resp.StatusCode, Body: string(raw)}
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	return out.ContentURI, nil
}

// doRaw is do() for binary bodies — media download returns raw bytes, not JSON.
func (h *hsClient) doRaw(ctx context.Context, path string, query url.Values, out *bytes.Buffer) (mime string, err error) {
	u := h.cfg.HomeserverURL + path
	if enc := query.Encode(); enc != "" {
		u += "?" + enc
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+h.cfg.ASToken)
	resp, err := h.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return "", &HTTPError{Status: resp.StatusCode, Body: string(raw)}
	}
	if out != nil {
		if _, err := io.Copy(out, io.LimitReader(resp.Body, 64<<20)); err != nil {
			return "", err
		}
	}
	return resp.Header.Get("Content-Type"), nil
}

// ParseMXC splits an mxc://server/mediaId URI.
func ParseMXC(mxc string) (server, mediaID string, ok bool) {
	if !strings.HasPrefix(mxc, "mxc://") {
		return "", "", false
	}
	rest := mxc[len("mxc://"):]
	i := strings.Index(rest, "/")
	if i <= 0 || i == len(rest)-1 {
		return "", "", false
	}
	return rest[:i], rest[i+1:], true
}

// downloadMedia is download() with the content type returned.
func (h *hsClient) downloadMedia(ctx context.Context, mxc string) (data []byte, mime string, err error) {
	server, mediaID, ok := ParseMXC(mxc)
	if !ok {
		return nil, "", fmt.Errorf("bad mxc uri %q", mxc)
	}
	var buf bytes.Buffer
	mime, err = h.doRaw(ctx,
		"/_matrix/media/v3/download/"+url.PathEscape(server)+"/"+url.PathEscape(mediaID),
		url.Values{"allow_remote": {"true"}, "timeout_ms": {"20000"}}, &buf)
	if err != nil {
		return nil, "", err
	}
	return buf.Bytes(), mime, nil
}

// profile fetches a Matrix user's displayname/avatar_url.
func (h *hsClient) profile(ctx context.Context, mxid string) (displayname, avatarURL string, err error) {
	var out struct {
		DisplayName string `json:"displayname"`
		AvatarURL   string `json:"avatar_url"`
	}
	err = h.do(ctx, "GET", "/_matrix/client/v3/profile/"+url.PathEscape(mxid), "", nil, nil, &out)
	return out.DisplayName, out.AvatarURL, err
}
