package db

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// InsertMessageWithAttachments commits the entire message before a broadcast.
// A failed attachment or deleted author leaves no partial message behind.
func InsertMessageWithAttachments(id, channel string, userID *int, text string, reply *string, kind, clientID string, attachments []Attachment, sessionToken ...string) (int64, error) {
	tx, err := pool.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	ts := NowTS()
	var position int64
	if err := tx.QueryRow("UPDATE message_clock SET position = MAX(position+1, ?) WHERE singleton = 1 RETURNING position", time.Now().UnixMicro()).Scan(&position); err != nil {
		return 0, err
	}
	if userID != nil {
		var allowed bool
		if len(sessionToken) > 0 {
			err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.token = ? AND s.user_id = ? AND s.scope = 'user' AND s.expires_at >= ? AND u.status = 'approved')", sessionToken[0], *userID, NowTS()).Scan(&allowed)
		} else {
			err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM users WHERE id = ? AND status = 'approved')", *userID).Scan(&allowed)
		}
		if err != nil {
			return 0, err
		}
		if !allowed {
			return 0, fmt.Errorf("author session is invalid")
		}
	}
	if _, err := tx.Exec("INSERT INTO messages (id, channel, user_id, type, text, reply_to, created_at, position, client_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)", id, channel, nullableInt(userID), kind, text, nullableStr(reply), ts, position, clientID); err != nil {
		return 0, err
	}
	for _, a := range attachments {
		rel := strings.TrimPrefix(a.URL, "/uploads/")
		if userID == nil || !strings.HasPrefix(rel, fmt.Sprintf("%d/", *userID)) || filepath.Base(rel) == "." || filepath.Base(rel) == ".." || strings.Count(rel, "/") != 1 || strings.ContainsAny(rel, `\\?#`) {
			return 0, fmt.Errorf("invalid attachment path")
		}
		info, err := os.Lstat(filepath.Join(UploadRoot, filepath.FromSlash(rel)))
		if err != nil || !info.Mode().IsRegular() {
			return 0, fmt.Errorf("attachment file is missing")
		}
		if _, err := tx.Exec("INSERT INTO attachments (message_id, user_id, name, rel_path, size, mime, width, height, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)", id, nullableInt(userID), a.Name, strings.TrimPrefix(a.URL, "/uploads/"), a.Size, a.Mime, a.Width, a.Height, ts); err != nil {
			return 0, err
		}
	}
	return ts, tx.Commit()
}

func MessageByClientID(userID int, channel, clientID string) (*HistoryMessage, error) {
	if clientID == "" {
		return nil, nil
	}
	rows, err := fetchMessageRows("SELECT id, channel, user_id, type, text, reply_to, created_at, edited_at, position, client_id FROM messages WHERE user_id = ? AND channel = ? AND client_id = ?", userID, channel, clientID)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}

func MessagePosition(id string) (int64, error) {
	var pos int64
	err := pool.QueryRow("SELECT position FROM messages WHERE id = ?", id).Scan(&pos)
	return pos, err
}

// EditMessageForSession authorizes the mutation in the same statement, so an
// admin revocation cannot race a check in the WebSocket read loop.
func EditMessageForSession(id, text string, editedAt int64, userID int, token string) error {
	res, err := pool.Exec("UPDATE messages SET text = ?, edited_at = ? WHERE id = ? AND user_id = ? AND EXISTS (SELECT 1 FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.token = ? AND s.user_id = ? AND s.scope = 'user' AND s.expires_at >= ? AND u.status = 'approved')", text, editedAt, id, userID, token, userID, NowTS())
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("message or session is invalid")
	}
	return nil
}
