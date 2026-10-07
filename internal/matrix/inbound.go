package matrix

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"alexmessage/internal/db"
	"alexmessage/internal/debuglog"
	"alexmessage/internal/dmpost"
)

// hsEvent is one event in an appservice transaction body.
type hsEvent struct {
	Type           string         `json:"type"`
	Sender         string         `json:"sender"`
	RoomID         string         `json:"room_id"`
	EventID        string         `json:"event_id"`
	StateKey       string         `json:"state_key"`
	OriginServerTS int64          `json:"origin_server_ts"`
	Content        map[string]any `json:"content"`
}

// processTransaction applies one AS transaction. txnID idempotency is handled
// by the caller (TryRecordMatrixTxn).
func (b *Bridge) processTransaction(txnID string, events []hsEvent) {
	for _, ev := range events {
		if err := b.processEvent(ev); err != nil {
			debuglog.Emit("matrix", "warn", "event_failed",
				"Matrix event processing failed", map[string]any{
					"txn": txnID, "type": ev.Type, "event": ev.EventID, "err": err.Error(),
				})
		}
	}
}

func (b *Bridge) processEvent(ev hsEvent) error {
	if b.cfg.OwnMXID(ev.Sender) {
		return nil // our own puppet traffic echoed back — ignore
	}
	switch ev.Type {
	case "m.room.message":
		return b.handleRoomMessage(ev)
	case "m.room.member":
		return b.handleMember(ev)
	case "m.receipt":
		return b.handleReceipt(ev)
	default:
		return nil // typing/presence/etc. are out of scope
	}
}

// ---------- m.room.member (invites to virtual users) ----------

func (b *Bridge) handleMember(ev hsEvent) error {
	membership, _ := ev.Content["membership"].(string)
	if membership != "invite" || ev.StateKey == "" {
		return nil
	}
	target := ev.StateKey
	asUser := ""
	var local *db.User
	if target == b.cfg.BotMXID() {
		// A bot invite has no local user to attach to; the DM model needs a
		// real participant, so log and skip.
		debuglog.Emit("matrix", "info", "bot_invite_ignored",
			"Ignoring invite to the bridge bot (DMs need a user puppet)",
			map[string]any{"room": ev.RoomID, "sender": ev.Sender})
		return nil
	}
	username, ok := b.cfg.UsernameForPuppet(target)
	if !ok {
		return nil // invite for someone else's namespace
	}
	local, _ = db.GetUserByUsername(username)
	if local == nil {
		debuglog.Emit("matrix", "warn", "invite_unknown_puppet",
			"Invite for a puppet with no local user", map[string]any{"puppet": target})
		return nil
	}
	asUser = target
	// The puppet may never have sent anything yet — register before joining.
	if err := b.ensurePuppet(context.Background(), username); err != nil {
		return fmt.Errorf("register puppet %s: %w", username, err)
	}

	// The member event's own profile fields seed the remote row cheaply.
	remote, err := b.ensureRemoteUser(ev.Sender)
	if err != nil {
		return err
	}
	if name, _ := ev.Content["displayname"].(string); name != "" && remote.DisplayName != name {
		_ = db.UpdateUserProfile(remote.ID, &name, nil)
	}
	channel := db.DMChannelID(local.ID, remote.ID)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if rid, _ := db.MatrixRoomFor(channel); rid != ev.RoomID {
		if err := db.SetMatrixRoom(channel, ev.RoomID); err != nil {
			return err
		}
	}
	if err := b.hs.joinRoom(ctx, ev.RoomID, asUser); err != nil {
		return fmt.Errorf("join %s: %w", ev.RoomID, err)
	}
	// Best-effort profile fill-in (avatar etc.) in the background queue.
	b.enqueue("sync_profile", func(ctx context.Context) error {
		return b.syncRemoteProfile(ctx, ev.Sender)
	})
	return nil
}

// ---------- m.room.message ----------

func (b *Bridge) handleRoomMessage(ev hsEvent) error {
	channel, _ := db.MatrixChannelFor(ev.RoomID)
	if channel == "" {
		return nil // room we don't manage
	}
	remote, err := b.ensureRemoteUser(ev.Sender)
	if err != nil {
		return err
	}
	a, c, ok := db.ParseDMChannel(channel)
	if !ok || (remote.ID != a && remote.ID != c) {
		return fmt.Errorf("sender %s is not a participant of %s", ev.Sender, channel)
	}
	localID := a
	if remote.ID == a {
		localID = c
	}

	// Edits arrive as m.room.message with an m.replace relation.
	if rel, ok := ev.Content["m.relates_to"].(map[string]any); ok {
		if rt, _ := rel["rel_type"].(string); rt == "m.replace" {
			return b.handleEdit(ev, rel, remote)
		}
	}
	// Dedupe: a redelivered or double-routed event must not re-post.
	if prev, _ := db.GetMessageByMatrixEventID(ev.EventID); prev != nil {
		return nil
	}

	msgtype, _ := ev.Content["msgtype"].(string)
	body, _ := ev.Content["body"].(string)
	ts := ev.OriginServerTS / 1000
	if ts <= 0 {
		ts = db.NowTS()
	}

	var text string
	var atts []db.Attachment
	switch msgtype {
	case "m.text", "m.notice", "m.emote":
		text = body
	case "m.image", "m.video", "m.audio", "m.file":
		att, err := b.downloadAttachment(remote.ID, msgtype, ev.Content)
		if err != nil {
			return err
		}
		atts = []db.Attachment{att}
	default:
		// Unknown msgtype — keep the body if it looks like text.
		if body != "" {
			text = body
		} else {
			return nil
		}
	}
	uid := remote.ID
	msg := dmpost.Post(&uid, channel, text, nil, atts, "message", ts)
	_ = db.SetMessageMatrixEventID(msg.ID, ev.EventID)
	dmpost.BroadcastNew(channel, msg)
	go dmpost.SendDMPush(remote.ID, localID, channel, msg)
	return nil
}

// handleEdit applies an m.replace edit to the linked local message.
func (b *Bridge) handleEdit(ev hsEvent, rel map[string]any, remote *db.User) error {
	targetID, _ := rel["event_id"].(string)
	if targetID == "" {
		return nil
	}
	target, _ := db.GetMessageByMatrixEventID(targetID)
	if target == nil || target.Type != "message" {
		return nil
	}
	if target.UserID == nil || *target.UserID != remote.ID {
		return fmt.Errorf("edit from %s on another user's message", ev.Sender)
	}
	newContent, _ := ev.Content["m.new_content"].(map[string]any)
	body, _ := newContent["body"].(string)
	if mt, _ := newContent["msgtype"].(string); mt != "m.text" && mt != "" && mt != "m.notice" {
		return nil // media edits aren't meaningful locally
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return nil
	}
	if len([]rune(body)) > 4000 {
		r := []rune(body)
		body = string(r[:4000])
	}
	ts := db.NowTS()
	if err := db.UpdateMessageText(target.ID, body, ts); err != nil {
		return err
	}
	dmpost.BroadcastEdited(target.Channel, target.ID, body, ts)
	return nil
}

// downloadAttachment fetches an mxc:// attachment into the remote user's
// upload dir and returns the local db.Attachment shape.
func (b *Bridge) downloadAttachment(userID int, msgtype string, content map[string]any) (db.Attachment, error) {
	url, _ := content["url"].(string)
	fileObj, _ := content["file"].(map[string]any)
	if url == "" {
		url, _ = fileObj["url"].(string) // encrypted-room shape; ciphertext still lands as a file
	}
	if url == "" {
		return db.Attachment{}, fmt.Errorf("%s event has no url", msgtype)
	}
	raw, mimeType, err := b.hs.downloadMedia(context.Background(), url)
	if err != nil {
		return db.Attachment{}, err
	}
	info, _ := content["info"].(map[string]any)
	// The event's declared mimetype beats an absent or generic download
	// content-type (media repos often serve octet-stream; for encrypted
	// attachments it's the ciphertext's, not the file's).
	if mimeType == "" || mimeType == "application/octet-stream" {
		if m, _ := info["mimetype"].(string); m != "" {
			mimeType = m
		}
	}
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = http.DetectContentType(raw)
	}
	name := attachmentName(msgtype, content, mimeType)
	safe := sanitizeFileName(name)
	userDir, err := db.UserUploadDir(userID)
	if err != nil {
		return db.Attachment{}, err
	}
	stored := randHex(6) + "_" + safe
	if err := os.WriteFile(filepath.Join(userDir, stored), raw, 0o644); err != nil {
		return db.Attachment{}, err
	}
	width := intDim(info["w"])
	height := intDim(info["h"])
	if width == 0 && strings.HasPrefix(mimeType, "image/") {
		width, height = imageDimensions(raw)
	}
	size := int64(len(raw))
	if sz, ok := info["size"].(float64); ok && sz > 0 {
		size = int64(sz)
	}
	return db.Attachment{
		Name:   name,
		URL:    "/uploads/" + strconv.Itoa(userID) + "/" + stored,
		Size:   size,
		Mime:   mimeType,
		Width:  width,
		Height: height,
	}, nil
}

// attachmentName derives the display name; voice-flagged audio keeps the
// voice-message.<ext> contract so clients render it as a voice bubble.
func attachmentName(msgtype string, content map[string]any, mimeType string) string {
	if hasVoiceFlag(content) {
		return "voice-message." + extForMime(mimeType)
	}
	if fn, _ := content["filename"].(string); fn != "" {
		return fn
	}
	if body, _ := content["body"].(string); body != "" {
		// Strip a leading "name: " or path separators from odd bodies.
		if i := strings.LastIndex(body, "/"); i >= 0 {
			body = body[i+1:]
		}
		return body
	}
	return "file" + extForMime(mimeType)
}

func hasVoiceFlag(content map[string]any) bool {
	_, v := content["org.matrix.msc3245.voice"]
	return v
}

// extForMime maps common media mimes to extensions for stored filenames.
func extForMime(mimeType string) string {
	switch mimeType {
	case "audio/ogg", "audio/opus":
		return "ogg"
	case "audio/mp4", "audio/x-m4a":
		return "m4a"
	case "audio/mpeg":
		return "mp3"
	case "audio/wav", "audio/x-wav":
		return "wav"
	case "audio/webm":
		return "webm"
	case "audio/aac":
		return "aac"
	case "audio/flac":
		return "flac"
	case "image/png":
		return "png"
	case "image/jpeg":
		return "jpg"
	case "image/gif":
		return "gif"
	case "image/webp":
		return "webp"
	case "video/mp4":
		return "mp4"
	case "video/webm":
		return "webm"
	case "video/quicktime":
		return "mov"
	case "application/pdf":
		return "pdf"
	}
	if exts, err := mime.ExtensionsByType(mimeType); err == nil && len(exts) > 0 {
		return strings.TrimPrefix(exts[0], ".")
	}
	return "bin"
}

var safeChars = strings.NewReplacer(
	"\\", "_", "/", "_", ":", "_", "*", "_", "?", "_",
	"\"", "_", "<", "_", ">", "_", "|", "_", " ", "_",
)

func sanitizeFileName(name string) string {
	name = safeChars.Replace(name)
	name = strings.Trim(name, "._")
	if name == "" {
		return "file"
	}
	if len(name) > 120 {
		name = name[len(name)-120:]
	}
	return name
}

func intDim(v any) int {
	if f, ok := v.(float64); ok && f > 0 && f < 20000 {
		return int(f)
	}
	return 0
}

// imageDimensions decodes image pixel size for inbound media (mirrors the
// upload path's helper in webapp/uploads.go).
func imageDimensions(raw []byte) (int, int) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

// ---------- m.receipt ----------

// handleReceipt applies remote m.read markers to our dm_state and broadcasts
// dm_read so the local peer's UI shows the read position.
func (b *Bridge) handleReceipt(ev hsEvent) error {
	channel, _ := db.MatrixChannelFor(ev.RoomID)
	if channel == "" {
		return nil
	}
	// content: {eventID: {"m.read": {mxid: {"ts": ms}}}}
	for eventID, byType := range ev.Content {
		typeMap, _ := byType.(map[string]any)
		readMap, _ := typeMap["m.read"].(map[string]any)
		for mxid, raw := range readMap {
			if b.cfg.OwnMXID(mxid) {
				continue
			}
			remote, err := b.ensureRemoteUser(mxid)
			if err != nil {
				continue
			}
			// Anchor the read marker at the referenced event's local timestamp,
			// falling back to the receipt's own ts.
			var cutoff int64
			if msg, _ := db.GetMessageByMatrixEventID(eventID); msg != nil {
				cutoff = msg.CreatedAt
			}
			if meta, _ := raw.(map[string]any); meta != nil {
				if t, ok := meta["ts"].(float64); ok && int64(t/1000) > cutoff {
					cutoff = int64(t / 1000)
				}
			}
			if cutoff <= 0 {
				cutoff = db.NowTS()
			}
			if err := db.SetDMLastRead(remote.ID, channel, cutoff); err != nil {
				continue
			}
			dmpost.BroadcastRead(remote.ID, channel, cutoff)
		}
	}
	return nil
}
