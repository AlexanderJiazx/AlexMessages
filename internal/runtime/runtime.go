// Package runtime holds the main app's shared in-memory state and helpers:
// the presence tracker, the visibility rules, and the cross-handler broadcast
// helpers. Anything reached by both the WebSocket handler and the REST routes
// lives here.
package runtime

import (
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"alexmessage/internal/auth"
	"alexmessage/internal/db"
)

const (
	// InitialHistoryPage is how many messages each DM thread ships on connect.
	InitialHistoryPage = 50
	// MaxUploadBytes caps a single upload at 20 MB.
	MaxUploadBytes = 20 * 1024 * 1024
)

// ---------- presence ----------

// Client wraps one live WebSocket connection. Gorilla connections are not safe
// for concurrent writes, so every send is serialized through mu.
type Client struct {
	Conn         *websocket.Conn
	UserID       int
	SessionToken string

	mu      sync.Mutex // serializes writes to Conn
	chmu    sync.Mutex
	channel string
	// hidden is set while the client reports its screen is not showing the
	// app; clients that never report count as visible.
	hidden atomic.Bool
}

// SetVisible records whether the client's screen is showing the app.
func (c *Client) SetVisible(visible bool) { c.hidden.Store(!visible) }

// Visible reports the client's last reported visibility.
func (c *Client) Visible() bool { return !c.hidden.Load() }

// Send writes a JSON payload to the client, serialized against other writers.
func (c *Client) Send(payload any) error {
	return c.send(payload, true)
}

func (c *Client) send(payload any, checkSession bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if checkSession && c.SessionToken != "" && auth.ResolveSession(c.SessionToken, "user") == nil {
		_ = c.Conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(4401, "Session revoked"), time.Now().Add(time.Second))
		_ = c.Conn.Close()
		return errors.New("session revoked")
	}
	_ = c.Conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	err := c.Conn.WriteJSON(payload)
	if err != nil {
		_ = c.Conn.Close()
	}
	return err
}

// SetChannel records the client's current channel (bookkeeping only; routing
// of DMs is by participant, and clients filter named channels locally).
func (c *Client) SetChannel(ch string) {
	c.chmu.Lock()
	c.channel = ch
	c.chmu.Unlock()
}

// Presence tracks live WebSocket connections per user.
type Presence struct {
	mu      sync.Mutex
	sockets map[*Client]struct{}
	byUser  map[int]map[*Client]struct{}
}

// presence is the process-wide singleton: the /ws sockets, which count as
// being online.
var presence = NewPresence()

// NewPresence returns an empty socket registry. Besides the singleton, the
// webapp keeps one for its call listener sockets, which don't count as being
// online.
func NewPresence() *Presence {
	return &Presence{
		sockets: map[*Client]struct{}{},
		byUser:  map[int]map[*Client]struct{}{},
	}
}

// PresenceTracker exposes the singleton.
func PresenceTracker() *Presence { return presence }

// Add registers a new client for a user.
func (p *Presence) Add(c *Client) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sockets[c] = struct{}{}
	if p.byUser[c.UserID] == nil {
		p.byUser[c.UserID] = map[*Client]struct{}{}
	}
	p.byUser[c.UserID][c] = struct{}{}
}

// Remove drops a client. Returns the user id it belonged to, or -1 if unknown.
func (p *Presence) Remove(c *Client) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.sockets[c]; !ok {
		return -1
	}
	delete(p.sockets, c)
	uid := c.UserID
	if bucket := p.byUser[uid]; bucket != nil {
		delete(bucket, c)
		if len(bucket) == 0 {
			delete(p.byUser, uid)
		}
	}
	return uid
}

// OnlineUserIDs returns the user ids with at least one live socket, sorted.
func (p *Presence) OnlineUserIDs() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]int, 0, len(p.byUser))
	for uid := range p.byUser {
		out = append(out, uid)
	}
	sort.Ints(out)
	return out
}

// SocketsFor returns every socket owned by any of the given user ids.
func (p *Presence) SocketsFor(userIDs map[int]struct{}) []*Client {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []*Client
	for uid := range userIDs {
		for c := range p.byUser[uid] {
			out = append(out, c)
		}
	}
	return out
}

// AllSockets returns a snapshot of every connected client.
func (p *Presence) AllSockets() []*Client {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*Client, 0, len(p.sockets))
	for c := range p.sockets {
		out = append(out, c)
	}
	return out
}

// ---------- low-level send helpers ----------

// SendJSON sends one payload, dropping the socket from presence on failure.
func SendJSON(c *Client, payload any) {
	if err := c.Send(payload); err != nil {
		presence.Remove(c)
	}
}

// Broadcast sends a payload to each recipient, dropping any that fail.
func Broadcast(payload any, recipients []*Client) {
	recipients = authenticatedClients(recipients)
	var wg sync.WaitGroup
	for _, c := range recipients {
		wg.Add(1)
		go func(c *Client) {
			defer wg.Done()
			if err := c.send(payload, false); err != nil {
				presence.Remove(c)
			}
		}(c)
	}
	wg.Wait()
}

func authenticatedClients(clients []*Client) []*Client {
	tokens := []string{}
	seen := map[string]bool{}
	for _, c := range clients {
		if c.SessionToken != "" && !seen[c.SessionToken] {
			seen[c.SessionToken] = true
			tokens = append(tokens, c.SessionToken)
		}
	}
	active, err := db.ActiveSessionTokens(tokens)
	if err != nil {
		return nil
	}
	out := []*Client{}
	for _, c := range clients {
		if c.SessionToken == "" || active[c.SessionToken] {
			out = append(out, c)
			continue
		}
		_ = c.Conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(4401, "Session revoked"), time.Now().Add(time.Second))
		_ = c.Conn.Close()
		presence.Remove(c)
	}
	return out
}

// ---------- public user views ----------

// PublicUser is the public-facing user record (no password hash, no internals).
type PublicUser struct {
	ID          int    `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Bio         string `json:"bio"`
	Avatar      string `json:"avatar"`
}

// UserPublic projects a db.User to its public view.
func UserPublic(u *db.User) PublicUser {
	return PublicUser{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Bio: u.Bio, Avatar: u.Avatar}
}

// AllKnownUsers returns every approved user keyed by id, so the client can
// resolve any message author.
func AllKnownUsers() (map[int]PublicUser, error) {
	rows, err := db.ListUsers("approved")
	if err != nil {
		return nil, err
	}
	out := make(map[int]PublicUser, len(rows))
	for _, r := range rows {
		out[r.ID] = UserPublic(r)
	}
	return out, nil
}

// VisibleUserIDs returns the user ids a viewer is allowed to know about: self,
// contacts, and DM partners. The roster is intentionally private — this
// prevents leaking the full membership.
func VisibleUserIDs(viewerID int) (map[int]struct{}, error) {
	ids := map[int]struct{}{viewerID: {}}

	contacts, err := db.ListContacts(viewerID)
	if err != nil {
		return nil, err
	}
	for _, id := range contacts {
		ids[id] = struct{}{}
	}

	partners, err := db.DMPartnerIDs(viewerID)
	if err != nil {
		return nil, err
	}
	for id := range partners {
		ids[id] = struct{}{}
	}
	return ids, nil
}

// ---------- broadcast helpers (visibility-aware) ----------

// BroadcastProfileUpdate sends a profile_update only to sockets that can
// already see this user.
func BroadcastProfileUpdate(profile PublicUser) {
	sockets := presence.AllSockets()
	viewers := presence.OnlineUserIDs()
	visible, err := db.VisibilityForUsers(viewers)
	if err != nil {
		return
	}
	recipients := []*Client{}
	for _, c := range sockets {
		if _, ok := visible[c.UserID][profile.ID]; ok {
			recipients = append(recipients, c)
		}
	}
	Broadcast(map[string]any{"type": "profile_update", "profile": profile}, recipients)
}

// BroadcastPresence shares visibility work across tabs and does not let a
// blocked TCP connection postpone delivery to healthy recipients.
func BroadcastPresence() {
	sockets := authenticatedClients(presence.AllSockets())
	online := presence.OnlineUserIDs()
	visible, err := db.VisibilityForUsers(online)
	if err != nil {
		return
	}
	var wg sync.WaitGroup
	for _, c := range sockets {
		filtered := []int{}
		for _, id := range online {
			if _, ok := visible[c.UserID][id]; ok {
				filtered = append(filtered, id)
			}
		}
		wg.Add(1)
		go func(c *Client, ids []int) {
			defer wg.Done()
			if err := c.send(map[string]any{"type": "presence", "online": ids}, false); err != nil {
				presence.Remove(c)
			}
		}(c, filtered)
	}
	wg.Wait()
}

// RecipientsForChannel returns the sockets a DM channel's messages go to: the
// two participants. Non-DM channels no longer exist, so they get no recipients.
func RecipientsForChannel(channel string) []*Client {
	if a, b, ok := db.ParseDMChannel(channel); ok {
		return presence.SocketsFor(map[int]struct{}{a: {}, b: {}})
	}
	return nil
}
