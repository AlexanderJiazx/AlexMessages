// Package dmpost holds the "persist a DM message and fan it out" helpers
// shared by the WebSocket handler (webapp) and the Matrix bridge
// (internal/matrix). Both entry points must produce identical wire shapes and
// identical side effects (row + attachments + live broadcast + Web Push).
package dmpost

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/db"
	"alexmessage/internal/debuglog"
	"alexmessage/internal/push"
	"alexmessage/internal/runtime"
)

// Message is the live "message" broadcast shape — it carries the resolved
// author, which the history/init shape (db.HistoryMessage) deliberately omits.
type Message struct {
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

func newMessageID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		// rand.Read never fails on supported platforms; fall back to a
		// timestamp-derived id rather than aborting message delivery.
		return fmt.Sprintf("m%x", db.NowTS())
	}
	return hex.EncodeToString(b[:])
}

// Post persists a message + its attachments (and the Matrix event id it came
// from, when bridged) in one transaction and returns the live broadcast shape
// including the resolved author. Any persistence failure is propagated — the
// caller must never acknowledge, broadcast, or relay a message that was not
// stored. This is the shared insert path used by the WS "message" handler and
// by the Matrix bridge's inbound events. createdAt=0 means "now";
// eventID="" stores NULL.
func Post(userID *int, channel, text string, replyTo *string, attachments []db.Attachment, msgType string, createdAt int64, eventID string) (Message, error) {
	msgID := newMessageID()
	ts, err := db.InsertBridgedMessage(msgID, channel, userID, text, replyTo, msgType, createdAt, eventID, attachments)
	if err != nil {
		debuglog.Emit("messages", "error", "dm_insert_failed", "Message insert failed", map[string]any{"err": err.Error()})
		return Message{}, err
	}
	var author *runtime.PublicUser
	if userID != nil {
		if row, _ := db.GetUserByID(*userID); row != nil {
			a := runtime.UserPublic(row)
			author = &a
		}
	}
	return Message{
		ID:          msgID,
		Type:        msgType,
		Channel:     channel,
		UserID:      userID,
		Author:      author,
		Text:        text,
		ReplyTo:     replyTo,
		Attachments: attachments,
		CreatedAt:   ts,
	}, nil
}

// BroadcastNew fans a new message out to the channel participants.
func BroadcastNew(channel string, msg Message) {
	runtime.Broadcast(
		gin.H{"type": "message", "channel": channel, "message": msg},
		runtime.RecipientsForChannel(channel),
	)
}

// BroadcastEdited notifies both participants that a message body changed.
func BroadcastEdited(channel, msgID, text string, editedAt int64) {
	runtime.Broadcast(
		gin.H{"type": "message_edited", "channel": channel, "id": msgID, "text": text, "edited_at": editedAt},
		runtime.RecipientsForChannel(channel),
	)
}

// BroadcastRead notifies both participants of a read-receipt update for userID.
func BroadcastRead(userID int, channel string, lastReadAt int64) {
	runtime.Broadcast(
		gin.H{"type": "dm_read", "channel": channel, "user_id": userID, "last_read_at": lastReadAt},
		runtime.RecipientsForChannel(channel),
	)
}

// SendDMPush sends a Web Push to recipientID for a DM, when appropriate.
func SendDMPush(senderID, recipientID int, channel string, msg Message) {
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

func truncateForPush(text string, limit int) string {
	text = strings.TrimSpace(text)
	r := []rune(text)
	if len(r) <= limit {
		return text
	}
	return strings.TrimRight(string(r[:limit-1]), " \t\n\r") + "…"
}
