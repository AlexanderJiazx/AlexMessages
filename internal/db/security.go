package db

import (
	"errors"
	"fmt"
	"strings"
)

// ActiveSessionTokens batches broadcast authorization for connected sockets.
func ActiveSessionTokens(tokens []string) (map[string]bool, error) {
	out := map[string]bool{}
	for start := 0; start < len(tokens); start += 500 {
		end := start + 500
		if end > len(tokens) {
			end = len(tokens)
		}
		args := []any{NowTS()}
		marks := []string{}
		for _, token := range tokens[start:end] {
			args = append(args, token)
			marks = append(marks, "?")
		}
		rows, err := pool.Query("SELECT s.token FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.scope = 'user' AND s.expires_at >= ? AND u.status = 'approved' AND s.token IN ("+strings.Join(marks, ",")+")", args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var token string
			if err := rows.Scan(&token); err != nil {
				rows.Close()
				return nil, err
			}
			out[token] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}

var ErrLastAdministrator = errors.New("refusing to remove the last approved administrator")

// CanAccessUpload checks persisted message membership rather than trusting a
// URL, filename, client attachment metadata, or the global contact list.
func CanAccessUpload(viewerID, ownerID int, rel string) (bool, error) {
	if viewerID == ownerID {
		return true, nil
	}
	rows, err := pool.Query("SELECT DISTINCT m.channel FROM attachments a JOIN messages m ON m.id = a.message_id WHERE a.rel_path = ? AND a.user_id = ?", rel, ownerID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var channel string
		if err := rows.Scan(&channel); err != nil {
			return false, err
		}
		a, b, ok := ParseDMChannel(channel)
		if ok && (viewerID == a || viewerID == b) {
			return true, nil
		}
	}
	return false, rows.Err()
}

// LogoutUser revokes sessions and push delivery in one database transaction.
func LogoutUser(userID int) error {
	tx, err := pool.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM sessions WHERE user_id = ?", userID); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM push_subscriptions WHERE user_id = ?", userID); err != nil {
		return err
	}
	return tx.Commit()
}

// ChangePasswordKeepingSession keeps the authenticated device's token valid;
// cookie and bearer clients keep using the same credential. Other tokens die.
func ChangePasswordKeepingSession(userID int, token, hash string) error {
	tx, err := pool.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec("UPDATE users SET password_hash = ? WHERE id = ? AND status = 'approved' AND EXISTS (SELECT 1 FROM sessions WHERE token = ? AND user_id = ? AND scope = 'user' AND expires_at >= ?)", hash, userID, token, userID, NowTS())
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("session was revoked")
	}
	if _, err := tx.Exec("DELETE FROM sessions WHERE user_id = ? AND token <> ?", userID, token); err != nil {
		return err
	}
	return tx.Commit()
}
