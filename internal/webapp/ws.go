package webapp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"alexmessage/internal/auth"
	"alexmessage/internal/db"
	"alexmessage/internal/debuglog"
	"alexmessage/internal/httpx"
	"alexmessage/internal/push"
	"alexmessage/internal/runtime"
)

var upgrader = websocket.Upgrader{
	// Starlette/FastAPI did not enforce WS origin; match that here.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// registerWS wires the /ws WebSocket endpoint (mirrors routes/ws.py).
func registerWS(r *gin.Engine) {
	r.GET("/ws", handleWS)
}

// ---------- wire payload shapes ----------

type dmThread struct {
	Channel string `json:"channel"`
	PeerID  int    `json:"peer_id"`
}

type dmStatePayload struct {
	Pinned         bool  `json:"pinned"`
	LastReadAt     int64 `json:"last_read_at"`
	ClearedAt      int64 `json:"cleared_at"`
	ForceUnread    bool  `json:"force_unread"`
	UnreadCount    int   `json:"unread_count"`
	PeerLastReadAt int64 `json:"peer_last_read_at"`
}

type initPayload struct {
	Type           string                         `json:"type"`
	Me             runtime.PublicUser             `json:"me"`
	Users          []runtime.PublicUser           `json:"users"`
	Contacts       []int                          `json:"contacts"`
	Online         []int                          `json:"online"`
	DMThreads      []dmThread                     `json:"dm_threads"`
	DMState        map[string]dmStatePayload      `json:"dm_state"`
	History        map[string][]db.HistoryMessage `json:"history"`
	HistoryHasMore map[string]bool                `json:"history_has_more"`
	PageSize       int                            `json:"page_size"`
	MaxUpload      int                            `json:"max_upload"`
}

// broadcastMessage is the live "message" shape — note it carries author, which
// the history/init message shape (db.HistoryMessage) deliberately omits.
type broadcastMessage struct {
	ID          string              `json:"id"`
	Type        string              `json:"type"`
	Channel     string              `json:"channel"`
	UserID      *int                `json:"user_id"`
	Author      *runtime.PublicUser `json:"author"`
	Text        string              `json:"text"`
	ReplyTo     *string             `json:"reply_to"`
	Attachments []db.Attachment     `json:"attachments"`
	CreatedAt   int64               `json:"created_at"`
	EditedAt    *int64              `json:"edited_at"`
	// ClientID echoes the sender's optimistic-message nonce so their client can
	// reconcile the locally rendered bubble with the persisted message. Other
	// recipients have no matching pending bubble and simply ignore it.
	ClientID string `json:"client_id,omitempty"`
}

// ---------- handler ----------

func handleWS(c *gin.Context) {
	// Cookie auth is read before the upgrade; we still complete the handshake
	// so the client receives close(4401) and redirects to /login.
	user := auth.ResolveSession(httpx.Cookie(c, auth.UserCookie), "user")

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	if user == nil {
		_ = conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(4401, ""), time.Now().Add(time.Second))
		_ = conn.Close()
		return
	}
	defer conn.Close()

	presence := runtime.PresenceTracker()
	client := &runtime.Client{Conn: conn, UserID: user.ID}
	presence.Add(client)
	debuglog.Emit("messages", "info", "ws_connect", "User connected", map[string]any{"user": user.Username})

	runtime.SendJSON(client, buildInitPayload(user))
	runtime.BroadcastPresence()

	defer func() {
		presence.Remove(client)
		runtime.BroadcastPresence()
		debuglog.Emit("messages", "info", "ws_disconnect", "User disconnected", map[string]any{"user": user.Username})
	}()

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var data map[string]any
		if json.Unmarshal(raw, &data) != nil {
			continue
		}
		kind, _ := data["type"].(string)
		switch kind {
		case "message":
			handleWSMessage(user.ID, data)
		case "edit":
			handleWSEdit(user.ID, data)
		case "switch":
			handleWSSwitch(client, data)
		case "open_dm":
			handleWSOpenDM(client, user.ID, data)
		case "ping":
			runtime.SendJSON(client, gin.H{"type": "pong"})
		}
	}
}

// ---------- init payload ----------

func buildInitPayload(user *db.User) initPayload {
	userID := user.ID
	contacts, _ := db.ListContacts(userID)
	contactSet := make(map[int]struct{}, len(contacts))
	for _, id := range contacts {
		contactSet[id] = struct{}{}
	}
	known, _ := runtime.AllKnownUsers()
	dmStates, _ := db.ListDMStates(userID)

	// DM threads to surface = contacts ∪ anyone we've messaged before.
	partnerIDs := map[int]struct{}{}
	for _, id := range contacts {
		partnerIDs[id] = struct{}{}
	}
	if partners, err := db.DMPartnerIDs(userID); err == nil {
		for id := range partners {
			partnerIDs[id] = struct{}{}
		}
	}

	history := map[string][]db.HistoryMessage{}
	historyHasMore := map[string]bool{}

	dmThreads := []dmThread{}
	dmStateOut := map[string]dmStatePayload{}
	for pid := range partnerIDs {
		ch := db.DMChannelID(userID, pid)
		state := dmStates[ch] // zero-value defaults when absent
		clearedAt := state.ClearedAt
		latestTS, _ := db.ChannelLatestMessageTS(ch)
		// Hide a deleted-for-me thread that has had no new activity since.
		_, isContact := contactSet[pid]
		if clearedAt != 0 && latestTS <= clearedAt && !isContact {
			continue
		}
		msgs, _ := db.FetchChannelWindow(ch, runtime.InitialHistoryPage, nil, afterPtr(clearedAt))
		history[ch] = msgs
		historyHasMore[ch] = len(msgs) == runtime.InitialHistoryPage
		unread, _ := db.CountUnread(userID, ch, state.LastReadAt, clearedAt)
		if state.ForceUnread && unread == 0 {
			unread = 1
		}
		peerState, _ := db.GetDMState(pid, ch)
		dmStateOut[ch] = dmStatePayload{
			Pinned:         state.Pinned,
			LastReadAt:     state.LastReadAt,
			ClearedAt:      clearedAt,
			ForceUnread:    state.ForceUnread,
			UnreadCount:    unread,
			PeerLastReadAt: peerState.LastReadAt,
		}
		dmThreads = append(dmThreads, dmThread{Channel: ch, PeerID: pid})
	}

	visible, _ := runtime.VisibleUserIDs(userID)
	visibleUsers := []runtime.PublicUser{}
	for uid := range visible {
		if u, ok := known[uid]; ok {
			visibleUsers = append(visibleUsers, u)
		}
	}
	visibleOnline := []int{}
	for _, uid := range runtime.PresenceTracker().OnlineUserIDs() {
		if _, ok := visible[uid]; ok {
			visibleOnline = append(visibleOnline, uid)
		}
	}

	if contacts == nil {
		contacts = []int{}
	}
	return initPayload{
		Type:           "init",
		Me:             runtime.UserPublic(user),
		Users:          visibleUsers,
		Contacts:       contacts,
		Online:         visibleOnline,
		DMThreads:      dmThreads,
		DMState:        dmStateOut,
		History:        history,
		HistoryHasMore: historyHasMore,
		PageSize:       runtime.InitialHistoryPage,
		MaxUpload:      runtime.MaxUploadBytes,
	}
}

// afterPtr maps `cleared_at or None` — 0 becomes nil (no lower bound).
func afterPtr(clearedAt int64) *int64 {
	if clearedAt == 0 {
		return nil
	}
	return &clearedAt
}

// ---------- message persistence ----------

// makeMessagePayload persists a message (+ attachments) and returns the live
// broadcast shape, including the resolved author.
func makeMessagePayload(userID *int, channel, text string, replyTo *string, attachments []db.Attachment, msgType string) broadcastMessage {
	msgID, _ := tokenHex(6) // 12 hex chars, like uuid4().hex[:12]
	ts, _ := db.InsertMessage(msgID, channel, userID, text, replyTo, msgType, nil)
	for _, a := range attachments {
		rel := a.URL
		if strings.HasPrefix(a.URL, "/uploads/") {
			rel = a.URL[len("/uploads/"):]
		}
		owner := 0
		if userID != nil {
			owner = *userID
		}
		_ = db.InsertAttachment(msgID, owner, a.Name, rel, a.Size, a.Mime, a.Width, a.Height)
	}
	var author *runtime.PublicUser
	if userID != nil {
		if row, _ := db.GetUserByID(*userID); row != nil {
			a := runtime.UserPublic(row)
			author = &a
		}
	}
	return broadcastMessage{
		ID:          msgID,
		Type:        msgType,
		Channel:     channel,
		UserID:      userID,
		Author:      author,
		Text:        text,
		ReplyTo:     replyTo,
		Attachments: attachments,
		CreatedAt:   ts,
	}
}

// ---------- client → server handlers ----------

func handleWSMessage(userID int, data map[string]any) {
	channel, _ := data["channel"].(string)
	a, b, ok := db.ParseDMChannel(channel)
	if !ok || (a != userID && b != userID) {
		return
	}
	other := a
	if b != userID {
		other = b
	}
	target, _ := db.GetUserByID(other)
	if target == nil || target.Status != "approved" {
		return
	}

	text, _ := data["text"].(string)
	text = truncateRunes(trimSpace(text), 4000)
	attachmentsIn, _ := data["attachments"].([]any)
	if text == "" && len(attachmentsIn) == 0 {
		return
	}

	cleanAtts := []db.Attachment{}
	userPrefix := fmt.Sprintf("/uploads/%d/", userID)
	for i, raw := range attachmentsIn {
		if i >= 6 {
			break
		}
		am, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		url, ok := am["url"].(string)
		if !ok || !strings.HasPrefix(url, userPrefix) {
			continue
		}
		cleanAtts = append(cleanAtts, db.Attachment{
			Name:   truncateRunes(strOrDefault(am, "name", "file"), 120),
			URL:    url,
			Size:   floatToInt64(am["size"]),
			Mime:   truncateRunes(strOrDefault(am, "mime", "application/octet-stream"), 100),
			Width:  clampDimension(am["width"]),
			Height: clampDimension(am["height"]),
		})
	}

	var replyTo *string
	if v, ok := data["reply_to"].(string); ok {
		replyTo = &v
	}

	uid := userID
	msg := makeMessagePayload(&uid, channel, text, replyTo, cleanAtts, "message")
	if cid, ok := data["client_id"].(string); ok {
		msg.ClientID = truncateRunes(cid, 64)
	}
	runtime.Broadcast(
		gin.H{"type": "message", "channel": channel, "message": msg},
		runtime.RecipientsForChannel(channel),
	)

	// DM-only: poke Web Push for the other participant (fire-and-forget).
	if a, b, ok := db.ParseDMChannel(channel); ok {
		other := a
		if b != userID {
			other = b
		}
		go maybeSendDMPush(userID, other, channel, msg)
	}
}

// handleWSEdit rewrites the body of the user's own message and notifies both
// DM participants. Only plain "message" rows with a non-empty new text can be
// edited; attachments are untouched.
func handleWSEdit(userID int, data map[string]any) {
	msgID, _ := data["id"].(string)
	text, _ := data["text"].(string)
	text = truncateRunes(trimSpace(text), 4000)
	if msgID == "" || text == "" {
		return
	}
	meta, _ := db.GetMessageMeta(msgID)
	if meta == nil || meta.Type != "message" {
		return
	}
	if meta.UserID == nil || *meta.UserID != userID {
		return
	}
	a, b, ok := db.ParseDMChannel(meta.Channel)
	if !ok || (a != userID && b != userID) {
		return
	}
	ts := db.NowTS()
	if err := db.UpdateMessageText(msgID, text, ts); err != nil {
		return
	}
	runtime.Broadcast(
		gin.H{"type": "message_edited", "channel": meta.Channel, "id": msgID, "text": text, "edited_at": ts},
		runtime.RecipientsForChannel(meta.Channel),
	)
}

func handleWSOpenDM(client *runtime.Client, userID int, data map[string]any) {
	peerID, ok := parsePeerID(data["peer_id"])
	if !ok || peerID == userID {
		return
	}
	peer, _ := db.GetUserByID(peerID)
	if peer == nil || peer.Status != "approved" {
		return
	}
	ch := db.DMChannelID(userID, peerID)
	state, _ := db.GetDMState(userID, ch)
	peerState, _ := db.GetDMState(peerID, ch)
	hist, _ := db.FetchChannelWindow(ch, runtime.InitialHistoryPage, nil, afterPtr(state.ClearedAt))
	runtime.SendJSON(client, gin.H{
		"type":     "dm_opened",
		"channel":  ch,
		"peer_id":  peerID,
		"history":  hist,
		"has_more": len(hist) == runtime.InitialHistoryPage,
		"state": dmStatePayload{
			Pinned:         state.Pinned,
			LastReadAt:     state.LastReadAt,
			ClearedAt:      state.ClearedAt,
			ForceUnread:    state.ForceUnread,
			UnreadCount:    0,
			PeerLastReadAt: peerState.LastReadAt,
		},
	})
}

func handleWSSwitch(client *runtime.Client, data map[string]any) {
	channel, _ := data["channel"].(string)
	if _, _, isDM := db.ParseDMChannel(channel); isDM {
		client.SetChannel(channel)
	}
}

// ---------- Web Push for DMs ----------

func truncateForPush(text string, limit int) string {
	text = trimSpace(text)
	r := []rune(text)
	if len(r) <= limit {
		return text
	}
	return strings.TrimRight(string(r[:limit-1]), " \t\n\r") + "…"
}

// maybeSendDMPush sends a Web Push to recipientID for a DM, when appropriate.
func maybeSendDMPush(senderID, recipientID int, channel string, msg broadcastMessage) {
	if senderID == recipientID {
		return
	}
	if subs, _ := db.ListPushSubscriptions(recipientID); len(subs) == 0 {
		return
	}
	body := truncateForPush(msg.Text, 140)
	if body == "" && len(msg.Attachments) > 0 {
		if len(msg.Attachments) == 1 {
			body = "Sent a file"
		} else {
			body = fmt.Sprintf("Sent %d files", len(msg.Attachments))
		}
	}
	title := "New message"
	if msg.Author != nil && msg.Author.DisplayName != "" {
		title = msg.Author.DisplayName
	}
	if body == "" {
		body = "New message"
	}
	payload := map[string]any{
		"title":      title,
		"body":       body,
		"url":        "/",
		"channel":    channel,
		"sender_id":  senderID,
		"icon":       "/static/icons/favicon-192.png",
		"badge":      "/static/icons/favicon-32.png",
		"tag":        "dm-" + channel,
		"created_at": msg.CreatedAt,
	}
	push.SendToUser(recipientID, payload)
}

// ---------- small value helpers ----------

// strOrDefault mirrors str(m.get(key, default)) for the common string case.
func strOrDefault(m map[string]any, key, def string) string {
	v, ok := m[key]
	if !ok {
		return def
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// floatToInt64 mirrors int(x or 0) for a JSON number (decoded as float64).
func floatToInt64(v any) int64 {
	if f, ok := v.(float64); ok {
		return int64(f)
	}
	return 0
}

// clampDimension sanitizes a client-supplied pixel dimension: non-numeric,
// negative, or absurd values collapse to 0 (unknown).
func clampDimension(v any) int {
	f, ok := v.(float64)
	if !ok || f < 0 || f > 20000 {
		return 0
	}
	return int(f)
}

// parsePeerID mirrors int(data.get("peer_id")) with its TypeError/ValueError guard.
func parsePeerID(v any) (int, bool) {
	switch x := v.(type) {
	case float64:
		return int(x), true
	case string:
		n, err := strconv.Atoi(x)
		if err != nil {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}
