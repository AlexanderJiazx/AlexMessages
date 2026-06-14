package db

import (
	"database/sql"
	"strings"
)

// DebugCap is the maximum number of debug rows retained. Older rows are pruned
// so the table behaves as a fixed-size ring buffer rather than durable history.
const DebugCap = 5000

// DebugEvent is one row of the live debug-console ring buffer. The JSON tags are
// the wire shape the admin console consumes (both the snapshot and SSE stream).
type DebugEvent struct {
	ID       int64  `json:"id"`
	TsMs     int64  `json:"ts_ms"`
	App      string `json:"app"`
	Level    string `json:"level"`
	UserID   *int   `json:"user_id"`
	Username string `json:"username"`
	Session  string `json:"session"`
	Event    string `json:"event"`
	Message  string `json:"message"`
	Context  string `json:"context"`
	IP       string `json:"ip"`
}

// DebugFilter narrows a debug-event query. Empty/zero fields are ignored, so the
// zero value returns the most recent events unfiltered. All text matches are
// case-insensitive.
type DebugFilter struct {
	SinceID  int64  // only events with id > SinceID
	App      string // exact match when set ("messages" | "meet" | "server")
	Level    string // exact match when set
	Username string // substring match when set
	Session  string // substring match when set
	Search   string // substring over event/message when set
	Limit    int    // capped to 1000; defaults to 500
}

// InsertDebugEvent appends one event and returns its assigned id.
func InsertDebugEvent(ev DebugEvent) (int64, error) {
	res, err := pool.Exec(
		"INSERT INTO debug_events (ts_ms, app, level, user_id, username, session, event, message, context, ip) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		ev.TsMs, ev.App, ev.Level, nullableInt(ev.UserID), ev.Username,
		ev.Session, ev.Event, ev.Message, ev.Context, ev.IP,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// QueryDebugEvents returns matching events in ascending id order (oldest first),
// which is the order a console renders them.
func QueryDebugEvents(f DebugFilter) ([]DebugEvent, error) {
	where := []string{"id > ?"}
	args := []any{f.SinceID}
	if f.App != "" {
		where = append(where, "app = ?")
		args = append(args, f.App)
	}
	if f.Level != "" {
		where = append(where, "level = ?")
		args = append(args, f.Level)
	}
	if f.Username != "" {
		where = append(where, "instr(lower(username), lower(?)) > 0")
		args = append(args, f.Username)
	}
	if f.Session != "" {
		where = append(where, "instr(lower(session), lower(?)) > 0")
		args = append(args, f.Session)
	}
	if f.Search != "" {
		where = append(where, "(instr(lower(event), lower(?)) > 0 OR instr(lower(message), lower(?)) > 0)")
		args = append(args, f.Search, f.Search)
	}
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	args = append(args, limit)

	// Take the newest `limit` rows that match, then flip to ascending so the
	// caller renders oldest-first without losing the most recent events.
	q := "SELECT id, ts_ms, app, level, user_id, username, session, event, message, context, ip " +
		"FROM debug_events WHERE " + strings.Join(where, " AND ") + " ORDER BY id DESC LIMIT ?"
	rows, err := pool.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DebugEvent
	for rows.Next() {
		var (
			ev     DebugEvent
			userID sql.NullInt64
		)
		if err := rows.Scan(&ev.ID, &ev.TsMs, &ev.App, &ev.Level, &userID,
			&ev.Username, &ev.Session, &ev.Event, &ev.Message, &ev.Context, &ev.IP); err != nil {
			return nil, err
		}
		ev.UserID = nullInt64ToPtr(userID)
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Reverse to ascending id order.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if out == nil {
		out = []DebugEvent{}
	}
	return out, nil
}

// LatestDebugEventID returns the highest event id, or 0 when the table is empty.
// The SSE stream uses it as a starting cursor so it only tails new events.
func LatestDebugEventID() (int64, error) {
	var id sql.NullInt64
	if err := pool.QueryRow("SELECT MAX(id) FROM debug_events").Scan(&id); err != nil {
		return 0, err
	}
	if id.Valid {
		return id.Int64, nil
	}
	return 0, nil
}

// PruneDebugEvents trims the table to its newest `keep` rows.
func PruneDebugEvents(keep int) error {
	if keep < 0 {
		keep = 0
	}
	_, err := pool.Exec(
		"DELETE FROM debug_events WHERE id <= (SELECT COALESCE(MAX(id), 0) FROM debug_events) - ?",
		keep,
	)
	return err
}

// ClearDebugEvents empties the ring buffer (admin "Clear" action).
func ClearDebugEvents() error {
	_, err := pool.Exec("DELETE FROM debug_events")
	return err
}
