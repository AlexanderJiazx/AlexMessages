package webapp

import (
	"encoding/json"
	"fmt"
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
	CheckOrigin: httpx.WebSocketOrigin,
}

// The /ws heartbeat. The server sends a WebSocket ping control frame every
// wsPingInterval; browsers and native WebSocket stacks answer it with a pong
// by themselves, so a client that never sends a frame of its own (the legacy
// static/app.js has no keepalive) still refreshes its read deadline. A socket
// that sends nothing, pong included, for wsReadTimeout is dropped as dead.
// The ping also keeps a reverse proxy from closing an idle socket (nginx's
// default proxy_read_timeout is 60 s).
var (
	// The tests shorten these two.
	wsPingInterval = 25 * time.Second
	wsReadTimeout  = 75 * time.Second
	// wsSessionCheck is how often the session is re-checked, so a logout
	// elsewhere closes the socket (close 4401) even while it is idle.
	wsSessionCheck = 5 * time.Second
	// wsWriteWait bounds a ping write, which waits for any frame in flight.
	wsWriteWait = 10 * time.Second
)

// registerWS wires the /ws WebSocket endpoint (mirrors routes/ws.py), and the
// call listener socket on /ws?mode=calls (call_listener.go).
func registerWS(r *gin.Engine) {
	r.GET("/ws", func(c *gin.Context) {
		if c.Query("mode") == "calls" {
			handleCallListener(c)
			return
		}
		handleWS(c)
	})
}

// ---------- wire payload shapes ----------

type dmThread struct {
	Channel string `json:"channel"`
	PeerID  int    `json:"peer_id"`
}

type dmStatePayload struct {
	Pinned               bool  `json:"pinned"`
	LastReadAt           int64 `json:"last_read_at"`
	ClearedAt            int64 `json:"cleared_at"`
	ForceUnread          bool  `json:"force_unread"`
	UnreadCount          int   `json:"unread_count"`
	PeerLastReadAt       int64 `json:"peer_last_read_at"`
	LastReadPosition     int64 `json:"last_read_position"`
	PeerLastReadPosition int64 `json:"peer_last_read_position"`
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
	Position    int64               `json:"position"`
	EditedAt    *int64              `json:"edited_at"`
	// ClientID echoes the sender's optimistic-message nonce so their client can
	// reconcile the locally rendered bubble with the persisted message. Other
	// recipients have no matching pending bubble and simply ignore it.
	ClientID string `json:"client_id,omitempty"`
	// Call is set on type="call" messages (the thread entry for a finished call).
	Call *db.CallRecord `json:"call,omitempty"`
}

// ---------- handler ----------

func handleWS(c *gin.Context) {
	// Cookie or bearer auth is read before the upgrade; we still complete the
	// handshake so the client receives close(4401) and redirects to /login.
	token := requestSessionToken(c)
	user := auth.ResolveSession(token, "user")

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

	pingInterval, readTimeout := wsPingInterval, wsReadTimeout
	conn.SetReadLimit(128 * 1024)
	_ = conn.SetReadDeadline(time.Now().Add(readTimeout))
	// Pongs arrive inside ReadMessage, on this goroutine.
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(readTimeout))
	})
	stopped := make(chan struct{})
	watcherDone := make(chan struct{})
	defer func() { close(stopped); <-watcherDone }()
	go func() {
		defer close(watcherDone)
		session := time.NewTicker(wsSessionCheck)
		defer session.Stop()
		beat := time.NewTicker(pingInterval)
		defer beat.Stop()
		for {
			select {
			case <-stopped:
				return
			case <-beat.C:
				// WriteControl may run alongside the client's other writes.
				// A ping that can't be written means the socket is dead;
				// closing it ends the read loop at once.
				if conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(wsWriteWait)) != nil {
					_ = conn.Close()
					return
				}
			case <-session.C:
				if auth.ResolveSession(token, "user") == nil {
					_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(4401, "Session revoked"), time.Now().Add(time.Second))
					_ = conn.Close()
					return
				}
			}
		}
	}()

	presence := runtime.PresenceTracker()
	client := &runtime.Client{Conn: conn, UserID: user.ID, SessionToken: token}
	presence.Add(client)
	// Registered right after Add, so no early return or panic below can leave
	// the socket in presence (the user shown online with no socket).
	unloaded := false // the page closed the socket on its way out (1001)
	defer func() {
		presence.Remove(client)
		callSocketGone(client, unloaded)
		runtime.BroadcastPresence()
		debuglog.Emit("messages", "info", "ws_disconnect", "User disconnected", map[string]any{"user": user.Username})
	}()
	debuglog.Emit("messages", "info", "ws_connect", "User connected", map[string]any{"user": user.Username})

	payload, err := buildInitPayload(user)
	if err != nil {
		// A partial init would replace the client's state with missing threads.
		// Close 1011 (internal error) makes the client reconnect and retry.
		debuglog.Emit("messages", "error", "ws_init_failed", "Init payload could not be loaded", map[string]any{"user": user.Username, "err": err.Error()})
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "init failed"), time.Now().Add(time.Second))
		return
	}
	runtime.SendJSON(client, payload)
	runtime.BroadcastPresence()
	deliverRingingCalls(client)

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			unloaded = websocket.IsCloseError(err, websocket.CloseGoingAway)
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(readTimeout))
		if auth.ResolveSession(token, "user") == nil {
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(4401, "Session revoked"), time.Now().Add(time.Second))
			return
		}
		var data map[string]any
		if json.Unmarshal(raw, &data) != nil {
			continue
		}
		kind, _ := data["type"].(string)
		switch kind {
		case "message":
			handleWSMessage(user.ID, data, token)
		case "edit":
			handleWSEdit(user.ID, data, token)
		case "switch":
			handleWSSwitch(client, data)
		case "open_dm":
			handleWSOpenDM(client, user.ID, data)
		case "call_start", "call_accept", "call_hangup", "call_signal", "call_resume":
			handleCallMessage(client, user, kind, data)
		case "visibility":
			// Whether this device's screen is showing the app; incoming calls
			// ring through Web Push when no device of the callee is visible.
			visible, _ := data["visible"].(bool)
			client.SetVisible(visible)
		case "ping":
			runtime.SendJSON(client, gin.H{"type": "pong"})
		}
	}
}

// ---------- init payload ----------

// buildInitPayload returns an error when any of its reads fails; the client
// replaces its state with init, so it must not get a partial one.
func buildInitPayload(user *db.User) (initPayload, error) {
	userID := user.ID
	contacts, err := db.ListContacts(userID)
	if err != nil {
		return initPayload{}, err
	}
	contactSet := make(map[int]struct{}, len(contacts))
	for _, id := range contacts {
		contactSet[id] = struct{}{}
	}
	known, err := runtime.AllKnownUsers()
	if err != nil {
		return initPayload{}, err
	}
	dmStates, err := db.ListDMStates(userID)
	if err != nil {
		return initPayload{}, err
	}

	// DM threads to surface = contacts ∪ anyone we've messaged before.
	partnerIDs := map[int]struct{}{}
	for _, id := range contacts {
		partnerIDs[id] = struct{}{}
	}
	partners, err := db.DMPartnerIDs(userID)
	if err != nil {
		return initPayload{}, err
	}
	for id := range partners {
		partnerIDs[id] = struct{}{}
	}

	history, err := db.FetchDMHistories(userID, runtime.InitialHistoryPage)
	if err != nil {
		return initPayload{}, err
	}
	summaries, err := db.DMSummaries(userID)
	if err != nil {
		return initPayload{}, err
	}
	historyHasMore := map[string]bool{}

	dmThreads := []dmThread{}
	dmStateOut := map[string]dmStatePayload{}
	for pid := range partnerIDs {
		ch := db.DMChannelID(userID, pid)
		state := dmStates[ch] // zero-value defaults when absent
		clearedAt := state.ClearedAt
		summary := summaries[ch]
		// Hide a deleted-for-me thread that has had no new activity since.
		_, isContact := contactSet[pid]
		if clearedAt != 0 && summary.LatestPosition <= state.ClearedPosition && !isContact {
			delete(history, ch)
			continue
		}
		msgs := history[ch]
		if msgs == nil {
			msgs = []db.HistoryMessage{}
		}
		history[ch] = msgs
		historyHasMore[ch] = len(msgs) == runtime.InitialHistoryPage
		unread := summary.Unread
		if state.ForceUnread && unread == 0 {
			unread = 1
		}
		dmStateOut[ch] = dmStatePayload{
			Pinned:               state.Pinned,
			LastReadAt:           state.LastReadAt,
			ClearedAt:            clearedAt,
			ForceUnread:          state.ForceUnread,
			UnreadCount:          unread,
			PeerLastReadAt:       summary.PeerLastReadAt,
			LastReadPosition:     state.LastReadPosition,
			PeerLastReadPosition: summary.PeerLastReadPosition,
		}
		dmThreads = append(dmThreads, dmThread{Channel: ch, PeerID: pid})
	}

	visible := map[int]struct{}{userID: {}}
	for id := range partnerIDs {
		visible[id] = struct{}{}
	}
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
	}, nil
}

// ---------- message persistence ----------

// makeMessagePayload persists a message (+ attachments) and returns the live
// broadcast shape, including the resolved author.
func makeMessagePayload(userID *int, channel, text string, replyTo *string, attachments []db.Attachment, msgType, clientID string, sessionToken ...string) (broadcastMessage, error) {
	msgID, err := tokenHex(6)
	if err != nil {
		return broadcastMessage{}, err
	} // 12 hex chars, like uuid4().hex[:12]
	ts, err := db.InsertMessageWithAttachments(msgID, channel, userID, text, replyTo, msgType, clientID, attachments, sessionToken...)
	if err != nil {
		return broadcastMessage{}, err
	}
	position, err := db.MessagePosition(msgID)
	if err != nil {
		return broadcastMessage{}, err
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
		Position:    position,
		ClientID:    clientID,
	}, nil
}

// ---------- client → server handlers ----------

func handleWSMessage(userID int, data map[string]any, sessionToken ...string) {
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
		if !ok || !strings.HasPrefix(url, userPrefix) || !validUploadFile(userID, strings.TrimPrefix(url, userPrefix)) {
			return // Do not acknowledge a message after silently dropping a file.
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

	if text == "" && len(cleanAtts) == 0 {
		return
	}
	var replyTo *string
	if v, ok := data["reply_to"].(string); ok {
		meta, _ := db.GetMessageMeta(v)
		if meta != nil && meta.Channel == channel {
			replyTo = &v
		}
	}

	uid := userID
	clientID := truncateRunes(strField(data, "client_id"), 64)
	previous, _ := db.MessageByClientID(userID, channel, clientID)
	var msg broadcastMessage
	if previous == nil {
		var err error
		msg, err = makeMessagePayload(&uid, channel, text, replyTo, cleanAtts, "message", clientID, sessionToken...)
		if err != nil {
			// A competing retry may have committed the same nonce first.
			previous, _ = db.MessageByClientID(userID, channel, clientID)
			if previous == nil {
				debuglog.Emit("messages", "error", "message_save_failed", "Message could not be saved", nil)
				return
			}
		}
	}
	if previous != nil {
		author, _ := db.GetUserByID(userID)
		if author == nil {
			return
		}
		pu := runtime.UserPublic(author)
		msg = broadcastMessage{ID: previous.ID, Type: previous.Type, Channel: previous.Channel, UserID: previous.UserID, Author: &pu, Text: previous.Text, ReplyTo: previous.ReplyTo, Attachments: previous.Attachments, CreatedAt: previous.CreatedAt, EditedAt: previous.EditedAt, Position: previous.Position, ClientID: clientID}
	}

	recipients := runtime.RecipientsForChannel(channel)
	if previous != nil {
		recipients = runtime.PresenceTracker().SocketsFor(map[int]struct{}{userID: {}})
	}
	runtime.Broadcast(gin.H{"type": "message", "channel": channel, "message": msg}, recipients)
	if previous != nil {
		return
	}

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
func handleWSEdit(userID int, data map[string]any, sessionToken string) {
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
	if err := db.EditMessageForSession(msgID, text, ts, userID, sessionToken); err != nil {
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
	hist, _ := db.FetchDMWindow(userID, ch, runtime.InitialHistoryPage, nil, "")
	runtime.SendJSON(client, gin.H{
		"type":     "dm_opened",
		"channel":  ch,
		"peer_id":  peerID,
		"history":  hist,
		"has_more": len(hist) == runtime.InitialHistoryPage,
		"state": dmStatePayload{
			Pinned:               state.Pinned,
			LastReadAt:           state.LastReadAt,
			ClearedAt:            state.ClearedAt,
			ForceUnread:          state.ForceUnread,
			UnreadCount:          0,
			PeerLastReadAt:       peerState.LastReadAt,
			LastReadPosition:     state.LastReadPosition,
			PeerLastReadPosition: peerState.LastReadPosition,
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
