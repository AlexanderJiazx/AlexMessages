// Package runtime holds the main app's shared in-memory state and helpers:
// the presence tracker, the channel catalog, the visibility rules, and the
// cross-handler broadcast helpers. Anything reached by both the WebSocket
// handler and the REST routes lives here.
package runtime

import (
	"sort"
	"sync"

	"github.com/gorilla/websocket"

	"alexmessage/internal/db"
)

const (
	// InitialHistoryPage is how many messages each channel ships on connect.
	InitialHistoryPage = 50
	// MaxUploadBytes caps a single upload at 20 MB.
	MaxUploadBytes = 20 * 1024 * 1024
	// DefaultChannel is where system join/leave broadcasts land.
	DefaultChannel = "general"
)

// Channel is one entry of the hardcoded channel catalog.
type Channel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Desc string `json:"desc"`
}

// Channels is the catalog. general is default.
var Channels = []Channel{
	{ID: "general", Name: "general", Desc: "The main room — anything goes."},
	{ID: "sports", Name: "sports", Desc: "Games, scores, takes."},
	{ID: "tech", Name: "tech", Desc: "Code, hardware, software."},
	{ID: "news", Name: "news", Desc: "What's happening out there."},
	{ID: "music", Name: "music", Desc: "Now playing."},
	{ID: "study", Name: "study", Desc: "Heads-down focus and notes."},
}

// ChannelIDs is the set of named channel ids for O(1) membership checks.
var ChannelIDs = func() map[string]struct{} {
	m := make(map[string]struct{}, len(Channels))
	for _, c := range Channels {
		m[c.ID] = struct{}{}
	}
	return m
}()

// channelIDList is the catalog ids as a slice (for DB IN-queries).
var channelIDList = func() []string {
	out := make([]string, len(Channels))
	for i, c := range Channels {
		out[i] = c.ID
	}
	return out
}()

// ---------- presence ----------

// Client wraps one live WebSocket connection. Gorilla connections are not safe
// for concurrent writes, so every send is serialized through mu.
type Client struct {
	Conn   *websocket.Conn
	UserID int

	mu      sync.Mutex // serializes writes to Conn
	chmu    sync.Mutex
	channel string
}

// Send writes a JSON payload to the client, serialized against other writers.
func (c *Client) Send(payload any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Conn.WriteJSON(payload)
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

// presence is the process-wide singleton.
var presence = &Presence{
	sockets: map[*Client]struct{}{},
	byUser:  map[int]map[*Client]struct{}{},
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
	for _, c := range recipients {
		if err := c.Send(payload); err != nil {
			presence.Remove(c)
		}
	}
}

// ---------- public user views ----------

// PublicUser is the public-facing user record (no password hash, no internals).
type PublicUser struct {
	ID          int    `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Bio         string `json:"bio"`
}

// UserPublic projects a db.User to its public view.
func UserPublic(u *db.User) PublicUser {
	return PublicUser{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Bio: u.Bio}
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
// contacts, DM partners, and authors of messages in the named channels. The
// roster is intentionally private — this prevents leaking the full membership.
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

	authors, err := db.ChannelAuthors(channelIDList)
	if err != nil {
		return nil, err
	}
	for id := range authors {
		ids[id] = struct{}{}
	}
	return ids, nil
}

// ---------- broadcast helpers (visibility-aware) ----------

// BroadcastProfileUpdate sends a profile_update only to sockets that can
// already see this user.
func BroadcastProfileUpdate(profile PublicUser) {
	uid := profile.ID
	payload := map[string]any{"type": "profile_update", "profile": profile}
	for _, c := range presence.AllSockets() {
		viewer := c.UserID
		if uid != viewer {
			visible, err := VisibleUserIDs(viewer)
			if err != nil {
				continue
			}
			if _, ok := visible[uid]; !ok {
				continue
			}
		}
		if err := c.Send(payload); err != nil {
			presence.Remove(c)
		}
	}
}

// BroadcastPresence sends each socket only the online users it may know about,
// so the full online roster never leaks.
func BroadcastPresence() {
	onlineList := presence.OnlineUserIDs()
	online := make(map[int]struct{}, len(onlineList))
	for _, id := range onlineList {
		online[id] = struct{}{}
	}
	for _, c := range presence.AllSockets() {
		visible, err := VisibleUserIDs(c.UserID)
		if err != nil {
			continue
		}
		filtered := make([]int, 0)
		for id := range online {
			if _, ok := visible[id]; ok {
				filtered = append(filtered, id)
			}
		}
		sort.Ints(filtered)
		if err := c.Send(map[string]any{"type": "presence", "online": filtered}); err != nil {
			presence.Remove(c)
		}
	}
}

// RecipientsForChannel returns the sockets a channel's messages go to: the two
// participants for a DM, or everyone online for a named channel.
func RecipientsForChannel(channel string) []*Client {
	if a, b, ok := db.ParseDMChannel(channel); ok {
		return presence.SocketsFor(map[int]struct{}{a: {}, b: {}})
	}
	return presence.AllSockets()
}
