package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// CallRecord is the finished-call summary carried by a type="call" message.
// Duration is in whole seconds and is 0 when the call was never answered.
type CallRecord struct {
	ID         int    `json:"id"`
	Media      string `json:"media"`
	Status     string `json:"status"`
	CallerID   *int   `json:"caller_id"`
	CalleeID   *int   `json:"callee_id"`
	StartedAt  int64  `json:"started_at"`
	AnsweredAt *int64 `json:"answered_at"`
	EndedAt    int64  `json:"ended_at"`
	Duration   int64  `json:"duration"`
}

// Call outcomes. The four unanswered outcomes all read as a missed call on
// the callee's side; the caller sees which one it was. A failed call was
// answered, but its media never connected.
const (
	CallCompleted   = "completed"
	CallDeclined    = "declined"
	CallFailed      = "failed"
	CallCancelled   = "cancelled"
	CallNoAnswer    = "no_answer"
	CallUnavailable = "unavailable"
	CallBusy        = "busy"
)

// CallMissed reports whether the callee still needs to hear about a call: it
// rang, or tried to, and was neither answered nor declined.
func CallMissed(status string) bool {
	return status != CallCompleted && status != CallDeclined && status != CallFailed
}

// quietCallFilter keeps calls the callee already knows about (answered or
// declined) out of unread counts; missed calls still count. `m` is messages.
const quietCallFilter = "NOT (m.type = 'call' AND EXISTS (SELECT 1 FROM calls qc WHERE qc.message_id = m.id AND qc.status IN ('completed', 'declined', 'failed')))"

// CallSummaryText is the plain-text body stored with a call message — what
// clients that don't render calls (the legacy web app, data exports) show.
func CallSummaryText(media, status string, duration int64) string {
	kind := "voice call"
	if media == "video" {
		kind = "video call"
	}
	switch status {
	case CallCompleted:
		return strings.ToUpper(kind[:1]) + kind[1:] + " · " + FormatCallDuration(duration)
	case CallDeclined:
		return "Declined " + kind
	case CallFailed:
		return strings.ToUpper(kind[:1]) + kind[1:] + " failed"
	default:
		return "Missed " + kind
	}
}

// FormatCallDuration renders seconds as m:ss, or h:mm:ss past an hour.
func FormatCallDuration(sec int64) string {
	if sec < 0 {
		sec = 0
	}
	h, m, s := sec/3600, sec%3600/60, sec%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// InsertCallMessage records a finished call — its calls row plus the
// type="call" thread entry, authored by the caller — in one transaction, so
// a call is never logged without its thread entry or vice versa. A
// participant whose account was deleted during the call (the admin panel is
// another process, so it can't end the call first) is stored as NULL, as ON
// DELETE SET NULL leaves the rows of earlier calls, and the returned record
// says the same.
func InsertCallMessage(msgID, channel string, callerID, calleeID int, media, status string, startedAt int64, answeredAt *int64, endedAt int64) (HistoryMessage, error) {
	tx, err := pool.Begin()
	if err != nil {
		return HistoryMessage{}, err
	}
	defer tx.Rollback()
	var position int64
	if err := tx.QueryRow("UPDATE message_clock SET position = MAX(position+1, ?) WHERE singleton = 1 RETURNING position", time.Now().UnixMicro()).Scan(&position); err != nil {
		return HistoryMessage{}, err
	}
	var duration int64
	if answeredAt != nil {
		duration = endedAt - *answeredAt
	}
	text := CallSummaryText(media, status, duration)
	var author sql.NullInt64
	if err := tx.QueryRow("INSERT INTO messages (id, channel, user_id, type, text, reply_to, created_at, position, client_id) VALUES (?, ?, (SELECT id FROM users WHERE id = ?), 'call', ?, NULL, ?, ?, '') RETURNING user_id", msgID, channel, callerID, text, endedAt, position).Scan(&author); err != nil {
		return HistoryMessage{}, err
	}
	var (
		id             int
		caller, callee sql.NullInt64
	)
	if err := tx.QueryRow("INSERT INTO calls (caller_id, callee_id, started_at, answered_at, ended_at, status, channel, media, message_id) VALUES ((SELECT id FROM users WHERE id = ?), (SELECT id FROM users WHERE id = ?), ?, ?, ?, ?, ?, ?, ?) RETURNING id, caller_id, callee_id", callerID, calleeID, startedAt, nullableInt64(answeredAt), endedAt, status, channel, media, msgID).Scan(&id, &caller, &callee); err != nil {
		return HistoryMessage{}, err
	}
	if err := tx.Commit(); err != nil {
		return HistoryMessage{}, err
	}
	return HistoryMessage{
		ID:          msgID,
		Position:    position,
		Type:        "call",
		Channel:     channel,
		UserID:      nullInt64ToPtr(author),
		Text:        text,
		CreatedAt:   endedAt,
		Attachments: []Attachment{},
		Call: &CallRecord{
			ID:         id,
			Media:      media,
			Status:     status,
			CallerID:   nullInt64ToPtr(caller),
			CalleeID:   nullInt64ToPtr(callee),
			StartedAt:  startedAt,
			AnsweredAt: answeredAt,
			EndedAt:    endedAt,
			Duration:   duration,
		},
	}, nil
}

// hydrateCalls attaches each call message's record, in bounded batches.
func hydrateCalls(msgs []HistoryMessage) error {
	index := map[string]int{}
	ids := []any{}
	for i, m := range msgs {
		if m.Type == "call" {
			index[m.ID] = i
			ids = append(ids, m.ID)
		}
	}
	for start := 0; start < len(ids); start += 500 {
		chunk := ids[start:min(start+500, len(ids))]
		marks := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		rows, err := pool.Query("SELECT message_id, id, caller_id, callee_id, media, status, started_at, answered_at, ended_at FROM calls WHERE message_id IN ("+marks+")", chunk...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var (
				msgID                    string
				rec                      CallRecord
				caller, callee, answered sql.NullInt64
			)
			if err := rows.Scan(&msgID, &rec.ID, &caller, &callee, &rec.Media, &rec.Status, &rec.StartedAt, &answered, &rec.EndedAt); err != nil {
				rows.Close()
				return err
			}
			rec.CallerID = nullInt64ToPtr(caller)
			rec.CalleeID = nullInt64ToPtr(callee)
			rec.AnsweredAt = nullInt64ToPtrI64(answered)
			if rec.AnsweredAt != nil {
				rec.Duration = rec.EndedAt - *rec.AnsweredAt
			}
			if i, ok := index[msgID]; ok {
				msgs[i].Call = &rec
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	return nil
}
