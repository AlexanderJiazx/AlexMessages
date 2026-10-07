package matrix

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"alexmessage/internal/db"
	"alexmessage/internal/debuglog"
	"alexmessage/internal/dmpost"
	"alexmessage/internal/runtime"
)

// Bridge is the running appservice bridge. All outbound Matrix traffic is
// serialized through a single queue worker so per-room ordering is preserved
// and retries can't interleave.
type Bridge struct {
	cfg       Config
	hs        *hsClient
	queue     chan outboundOp
	retryBase time.Duration

	regOnce    sync.Map // puppet localpart -> struct{} (registered this process)
	avatarSeen sync.Map // mxid -> last synced avatar mxc
	stopping   atomic.Bool
}

// outboundOp is one queued homeserver interaction.
type outboundOp struct {
	name string
	run  func(ctx context.Context) error
}

var active atomic.Pointer[Bridge]

// Active returns the running bridge, or nil when the bridge is disabled.
func Active() *Bridge { return active.Load() }

// Enabled reports whether the MATRIX_* env vars configure the bridge.
func Enabled() bool {
	cfg, err := LoadConfig()
	return err == nil && cfg != nil
}

// Start reads env config and, when configured, registers the bot user and
// starts the outbound worker. Called once from cmd/server.
func Start() (*Bridge, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, nil // disabled
	}
	b := newBridge(*cfg)
	if err := b.ensurePuppet(context.Background(), cfg.BotLocalpart); err != nil {
		// Non-fatal: the HS might be unreachable at boot. The first outbound
		// op will retry registration via ensurePuppet anyway.
		debuglog.Emit("matrix", "warn", "bot_register_failed",
			"Could not register Matrix bot user at startup", map[string]any{"err": err.Error()})
	}
	go b.worker()
	active.Store(b)
	debuglog.Emit("matrix", "info", "bridge_started",
		"Matrix appservice bridge enabled", map[string]any{
			"homeserver": cfg.HomeserverURL, "server_name": cfg.ServerName,
			"user_prefix": cfg.UserPrefix, "bot": cfg.BotMXID(),
		})
	return b, nil
}

func newBridge(cfg Config) *Bridge {
	return &Bridge{
		cfg:       cfg,
		hs:        newHSClient(&cfg),
		queue:     make(chan outboundOp, 512),
		retryBase: time.Second,
	}
}

// randHex is secrets.token_hex(n) — n random bytes as 2n hex chars.
func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// ---------- outbound queue ----------

// enqueue schedules an op on the serialized worker. When the queue is nil
// (unit tests) the op runs inline.
func (b *Bridge) enqueue(name string, run func(ctx context.Context) error) {
	op := outboundOp{name: name, run: run}
	if b.queue == nil {
		b.withRetry(op)
		return
	}
	select {
	case b.queue <- op:
	default:
		debuglog.Emit("matrix", "error", "outbound_queue_full",
			"Matrix outbound queue is full; dropping op", map[string]any{"op": name})
	}
}

func (b *Bridge) worker() {
	for op := range b.queue {
		if b.stopping.Load() {
			return
		}
		b.withRetry(op)
	}
}

// withRetry runs an op with exponential backoff (1s → 30s cap, 6 attempts).
// Terminal HTTP errors (4xx other than 429) are not retried. Failures never
// escape into local delivery — they land in the debug log.
func (b *Bridge) withRetry(op outboundOp) {
	backoff := b.retryBase
	for attempt := 1; attempt <= 6; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		err := op.run(ctx)
		cancel()
		if err == nil {
			return
		}
		if he, ok := err.(*HTTPError); ok && !he.Retryable() {
			debuglog.Emit("matrix", "error", "hs_call_rejected",
				"Homeserver call rejected (not retrying)", map[string]any{
					"op": op.name, "err": err.Error(),
				})
			return
		}
		debuglog.Emit("matrix", "warn", "hs_call_failed",
			"Homeserver call failed; retrying", map[string]any{
				"op": op.name, "attempt": attempt, "err": err.Error(),
			})
		time.Sleep(backoff)
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
	debuglog.Emit("matrix", "error", "hs_call_gave_up",
		"Homeserver call gave up after retries", map[string]any{"op": op.name})
}

// ---------- outbound hooks (called by webapp on local activity) ----------

// OnLocalMessage relays a locally-sent DM to Matrix when the peer is remote.
// Must never block or break local delivery — it enqueues and returns.
func (b *Bridge) OnLocalMessage(senderID int, channel string, msg dmpost.Message) {
	peer := b.remotePeer(channel, senderID)
	if peer == nil {
		return
	}
	remoteID := peer.ID
	b.enqueue("send_message", func(ctx context.Context) error {
		sender, err := db.GetUserByID(senderID)
		if err != nil || sender == nil {
			return fmt.Errorf("sender %d gone", senderID)
		}
		remote, err := db.GetUserByID(remoteID)
		if err != nil || remote == nil || remote.MatrixID == nil {
			return nil // peer converted/gone — nothing to relay
		}
		roomID, err := b.ensureRoom(ctx, sender, remote)
		if err != nil {
			return err
		}
		if err := b.ensurePuppet(ctx, sender.Username); err != nil {
			return err
		}
		return b.sendMessage(ctx, roomID, b.cfg.PuppetMXID(sender.Username), msg)
	})
}

// OnLocalEdit relays an edit as an m.replace event for the linked event id.
func (b *Bridge) OnLocalEdit(userID int, msgID, channel, text string) {
	peer := b.remotePeer(channel, userID)
	if peer == nil {
		return
	}
	remoteID := peer.ID
	b.enqueue("send_edit", func(ctx context.Context) error {
		sender, err := db.GetUserByID(userID)
		if err != nil || sender == nil {
			return nil
		}
		remote, err := db.GetUserByID(remoteID)
		if err != nil || remote == nil || remote.MatrixID == nil {
			return nil
		}
		roomID, err := b.ensureRoom(ctx, sender, remote)
		if err != nil {
			return err
		}
		target, _ := db.MatrixEventIDForMessage(msgID)
		if target == "" {
			debuglog.Emit("matrix", "debug", "edit_no_event",
				"Edit has no Matrix event id; skipping relay", map[string]any{"message": msgID})
			return nil
		}
		puppet := b.cfg.PuppetMXID(sender.Username)
		content := map[string]any{
			"msgtype": "m.text",
			"body":    "* " + text,
			"m.new_content": map[string]any{
				"msgtype": "m.text",
				"body":    text,
			},
			"m.relates_to": map[string]any{
				"rel_type": "m.replace",
				"event_id": target,
			},
		}
		_, err = b.hs.sendEvent(ctx, roomID, "m.room.message", txnID(msgID, "edit"), puppet, content)
		return err
	})
}

// OnLocalRead sends an m.read receipt for the newest bridged event.
func (b *Bridge) OnLocalRead(userID int, channel string) {
	peer := b.remotePeer(channel, userID)
	if peer == nil {
		return
	}
	b.enqueue("send_receipt", func(ctx context.Context) error {
		sender, err := db.GetUserByID(userID)
		if err != nil || sender == nil {
			return nil
		}
		roomID, _ := db.MatrixRoomFor(channel)
		if roomID == "" {
			return nil
		}
		eventID, _, _ := db.LatestMatrixEventID(channel)
		if eventID == "" {
			return nil
		}
		return b.hs.sendReadReceipt(ctx, roomID, eventID, b.cfg.PuppetMXID(sender.Username))
	})
}

// remotePeer returns the remote (bridged) participant of a DM channel, or nil
// when the channel is not a DM or the peer is a normal local user.
func (b *Bridge) remotePeer(channel string, localID int) *db.User {
	a, c, ok := db.ParseDMChannel(channel)
	if !ok {
		return nil
	}
	other := a
	if c != localID {
		other = c
	}
	u, _ := db.GetUserByID(other)
	if u == nil || u.MatrixID == nil {
		return nil
	}
	return u
}

// ensurePuppet registers the puppet localpart once per process.
func (b *Bridge) ensurePuppet(ctx context.Context, username string) error {
	lp := b.cfg.UserPrefix + username
	if _, ok := b.regOnce.Load(lp); ok {
		return nil
	}
	if username == b.cfg.BotLocalpart {
		lp = b.cfg.BotLocalpart
	}
	if err := b.hs.register(ctx, lp); err != nil {
		return err
	}
	b.regOnce.Store(lp, struct{}{})
	return nil
}

// txnID derives a stable idempotency key for outbound sends.
func txnID(msgID, suffix string) string {
	if suffix == "" {
		return "am_" + msgID
	}
	return "am_" + msgID + "_" + suffix
}

// ensureRoom returns the Matrix room for the local<->remote DM, creating it
// (as the local user's puppet, inviting the remote user) on first use.
func (b *Bridge) ensureRoom(ctx context.Context, local, remote *db.User) (string, error) {
	channel := db.DMChannelID(local.ID, remote.ID)
	if rid, _ := db.MatrixRoomFor(channel); rid != "" {
		return rid, nil
	}
	if remote.MatrixID == nil {
		return "", fmt.Errorf("user %d is not a matrix user", remote.ID)
	}
	if err := b.ensurePuppet(ctx, local.Username); err != nil {
		return "", err
	}
	puppet := b.cfg.PuppetMXID(local.Username)
	roomID, err := b.hs.createRoom(ctx, puppet, *remote.MatrixID)
	if err != nil {
		return "", err
	}
	if rid, _ := db.MatrixRoomFor(channel); rid != "" {
		return rid, nil // created meanwhile (serial worker makes this rare)
	}
	if err := db.SetMatrixRoom(channel, roomID); err != nil {
		return "", err
	}
	debuglog.Emit("matrix", "info", "room_created",
		"Created Matrix DM room", map[string]any{"channel": channel, "room_id": roomID, "peer": *remote.MatrixID})
	return roomID, nil
}

// ---------- outbound message building ----------

// sendMessage relays one local message (text + attachments) into the room.
// The first event id produced is stored on the message row so later edits and
// read receipts have an anchor.
func (b *Bridge) sendMessage(ctx context.Context, roomID, puppet string, msg dmpost.Message) error {
	firstEvent := ""
	remember := func(eventID string) {
		if firstEvent == "" && eventID != "" {
			firstEvent = eventID
			_ = db.SetMessageMatrixEventID(msg.ID, eventID)
		}
	}
	if msg.Text != "" {
		ev, err := b.hs.sendEvent(ctx, roomID, "m.room.message", txnID(msg.ID, "t"), puppet,
			map[string]any{"msgtype": "m.text", "body": msg.Text})
		if err != nil {
			return err
		}
		remember(ev)
	}
	for i, att := range msg.Attachments {
		content, err := b.buildMediaContent(ctx, att)
		if err != nil {
			debuglog.Emit("matrix", "warn", "attachment_relay_failed",
				"Attachment upload to Matrix failed", map[string]any{
					"message": msg.ID, "name": att.Name, "err": err.Error(),
				})
			continue // one bad attachment must not drop the message
		}
		ev, err := b.hs.sendEvent(ctx, roomID, "m.room.message",
			txnID(msg.ID, fmt.Sprintf("a%d", i)), puppet, content)
		if err != nil {
			return err
		}
		remember(ev)
	}
	return nil
}

// buildMediaContent uploads a local attachment file to the homeserver's media
// repo and returns the m.room.message content for it.
func (b *Bridge) buildMediaContent(ctx context.Context, att db.Attachment) (map[string]any, error) {
	rel := strings.TrimPrefix(att.URL, "/uploads/")
	raw, err := os.ReadFile(filepath.Join(db.UploadRoot, filepath.FromSlash(rel)))
	if err != nil {
		return nil, err
	}
	mxc, err := b.hs.upload(ctx, att.Name, att.Mime, raw)
	if err != nil {
		return nil, err
	}
	info := map[string]any{"mimetype": att.Mime, "size": att.Size}
	if att.Width > 0 && att.Height > 0 {
		info["w"] = att.Width
		info["h"] = att.Height
	}
	msgtype := "m.file"
	switch {
	case strings.HasPrefix(att.Mime, "image/"):
		msgtype = "m.image"
	case strings.HasPrefix(att.Mime, "video/"):
		msgtype = "m.video"
	case strings.HasPrefix(att.Mime, "audio/"):
		msgtype = "m.audio"
	}
	content := map[string]any{
		"msgtype":  msgtype,
		"body":     att.Name,
		"url":      mxc,
		"filename": att.Name,
		"info":     info,
	}
	if isVoiceAttachment(att) {
		// MSC3245 voice bubble + MSC1767 audio block markup so Matrix clients
		// render it as a voice message rather than a generic audio file.
		content["org.matrix.msc3245.voice"] = map[string]any{}
		content["org.matrix.msc1767.audio"] = map[string]any{}
	}
	return content, nil
}

// isVoiceAttachment matches shared/src/format.ts: a voice message is an
// audio/* attachment named voice-message.<ext>.
func isVoiceAttachment(att db.Attachment) bool {
	if !strings.HasPrefix(att.Mime, "audio/") {
		return false
	}
	base := att.Name
	if i := strings.LastIndex(base, "."); i > 0 {
		base = base[:i]
	}
	return base == "voice-message" || strings.HasSuffix(base, "_voice-message")
}

// ---------- remote user provisioning ----------

// ensureRemoteUser returns the local row for a remote Matrix user, creating a
// non-login 'approved' row on first sight. Display name defaults to the
// localpart; a profile sync refines it asynchronously.
func (b *Bridge) ensureRemoteUser(mxid string) (*db.User, error) {
	if b.cfg.OwnMXID(mxid) {
		return nil, fmt.Errorf("%s is a bridged Alex Messages user", mxid)
	}
	lp, server, ok := ParseMXID(mxid)
	if !ok {
		return nil, fmt.Errorf("bad matrix id %q", mxid)
	}
	if u, err := db.GetUserByMatrixID(mxid); err != nil {
		return nil, err
	} else if u != nil {
		return u, nil
	}
	// Remote usernames embed the full mxid minus '@' — locals can't contain
	// ':' so there is never a collision with a real account.
	username := strings.ToLower(lp + ":" + server)
	id, err := db.CreateRemoteUser(username, lp, mxid)
	if err != nil {
		// Lost a create race — the row exists now; re-read it.
		if u, _ := db.GetUserByMatrixID(mxid); u != nil {
			return u, nil
		}
		return nil, err
	}
	debuglog.Emit("matrix", "info", "remote_user_created",
		"Provisioned remote Matrix user", map[string]any{"mxid": mxid, "id": id})
	return db.GetUserByID(id)
}

// syncRemoteProfile pulls displayname + avatar from the homeserver and applies
// them locally (avatar bytes are copied into data/avatars/).
func (b *Bridge) syncRemoteProfile(ctx context.Context, mxid string) error {
	u, err := db.GetUserByMatrixID(mxid)
	if err != nil || u == nil {
		return err
	}
	name, avatarMXC, err := b.hs.profile(ctx, mxid)
	if err != nil {
		return err
	}
	if name != "" && name != u.DisplayName {
		if err := db.UpdateUserProfile(u.ID, &name, nil); err != nil {
			return err
		}
	}
	if avatarMXC != "" {
		if prev, _ := b.avatarSeen.Load(mxid); prev != avatarMXC {
			if err := b.copyAvatar(ctx, u.ID, avatarMXC); err != nil {
				debuglog.Emit("matrix", "warn", "avatar_copy_failed",
					"Could not copy Matrix avatar", map[string]any{"mxid": mxid, "err": err.Error()})
			} else {
				b.avatarSeen.Store(mxid, avatarMXC)
			}
		}
	}
	if fresh, _ := db.GetUserByID(u.ID); fresh != nil {
		runtime.BroadcastProfileUpdate(runtime.UserPublic(fresh))
	}
	return nil
}

// copyAvatar downloads a Matrix avatar and stores it like a local avatar upload.
func (b *Bridge) copyAvatar(ctx context.Context, userID int, mxc string) error {
	raw, mime, err := b.hs.downloadMedia(ctx, mxc)
	if err != nil {
		return err
	}
	if mime == "" {
		mime = http.DetectContentType(raw)
	}
	ext, ok := avatarExt[mime]
	if !ok {
		return fmt.Errorf("unsupported avatar mime %q", mime)
	}
	name := fmt.Sprintf("%d_%s.%s", userID, randHex(6), ext)
	if err := os.WriteFile(filepath.Join(db.AvatarRoot, name), raw, 0o644); err != nil {
		return err
	}
	return db.SetUserAvatar(userID, "/avatars/"+name)
}

var avatarExt = map[string]string{
	"image/png":  "png",
	"image/jpeg": "jpg",
	"image/gif":  "gif",
	"image/webp": "webp",
}

// OpenDMByMXID provisions (or returns) the remote user for a full Matrix ID and
// kicks off room creation + profile sync. Called from the user-lookup path so
// "New Message → @user:server" just works.
func (b *Bridge) OpenDMByMXID(localUserID int, mxid string) (*db.User, error) {
	if _, ok := b.cfg.UsernameForPuppet(mxid); ok || mxid == b.cfg.BotMXID() {
		return nil, fmt.Errorf("that's an Alex Messages user — open the DM by username instead")
	}
	lp, server, ok := ParseMXID(mxid)
	if !ok || lp == "" || server == "" {
		return nil, fmt.Errorf("not a Matrix user id")
	}
	remote, err := b.ensureRemoteUser(mxid)
	if err != nil {
		return nil, err
	}
	b.enqueue("open_dm_setup", func(ctx context.Context) error {
		local, err := db.GetUserByID(localUserID)
		if err != nil || local == nil {
			return nil
		}
		if err := b.ensurePuppet(ctx, local.Username); err != nil {
			return err
		}
		if _, err := b.ensureRoom(ctx, local, remote); err != nil {
			return err
		}
		return nil
	})
	b.enqueue("sync_profile", func(ctx context.Context) error {
		return b.syncRemoteProfile(ctx, mxid)
	})
	return remote, nil
}
