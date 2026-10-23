package db

import "fmt"

// FetchDMWindow applies the caller's sequence cutoff and an exact message
// cursor. The timestamp cursor remains supported for older clients.
func FetchDMWindow(userID int, channel string, limit int, beforeTS *int64, beforeID string) ([]HistoryMessage, error) {
	state, err := GetDMState(userID, channel)
	if err != nil {
		return nil, err
	}
	return FetchChannelWindowCursor(channel, limit, beforeTS, nil, beforeID, &state.ClearedPosition)
}

// FetchDMHistories uses one window query plus bounded attachment batches for every
// conversation. Init does not perform queries for each individual partner.
func FetchDMHistories(userID, limit int) (map[string][]HistoryMessage, error) {
	rows, err := fetchMessageRows(`SELECT id, channel, user_id, type, text, reply_to, created_at, edited_at, position, client_id FROM (
	 SELECT m.*, ROW_NUMBER() OVER (PARTITION BY m.channel ORDER BY m.position DESC) AS rank
	 FROM messages m LEFT JOIN dm_state s ON s.user_id = ? AND s.channel = m.channel
	 WHERE (m.channel LIKE ? OR m.channel LIKE ?) AND m.position > COALESCE(s.cleared_position, 0)
	) WHERE rank <= ? ORDER BY position DESC`, userID, fmt.Sprintf("dm:%d:%%", userID), fmt.Sprintf("dm:%%:%d", userID), limit)
	if err != nil {
		return nil, err
	}
	out := map[string][]HistoryMessage{}
	for _, m := range rows {
		out[m.Channel] = append(out[m.Channel], m)
	}
	return out, nil
}

type DMSummary struct {
	LatestPosition       int64
	Unread               int
	PeerLastReadAt       int64
	PeerLastReadPosition int64
}

// DMSummaries batches the remaining init metadata, including cleared threads.
func DMSummaries(userID int) (map[string]DMSummary, error) {
	out := map[string]DMSummary{}
	rows, err := pool.Query(`SELECT m.channel, MAX(m.position), SUM(CASE WHEN m.user_id IS NOT NULL AND m.user_id <> ? AND m.position > MAX(COALESCE(s.last_read_position,0),COALESCE(s.cleared_position,0)) AND `+quietCallFilter+` THEN 1 ELSE 0 END)
	FROM messages m LEFT JOIN dm_state s ON s.user_id = ? AND s.channel = m.channel
	WHERE m.channel LIKE ? OR m.channel LIKE ? GROUP BY m.channel`, userID, userID, fmt.Sprintf("dm:%d:%%", userID), fmt.Sprintf("dm:%%:%d", userID))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var ch string
		var s DMSummary
		if err := rows.Scan(&ch, &s.LatestPosition, &s.Unread); err != nil {
			rows.Close()
			return nil, err
		}
		out[ch] = s
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	rows, err = pool.Query("SELECT channel, last_read_at, last_read_position FROM dm_state WHERE user_id <> ? AND (channel LIKE ? OR channel LIKE ?)", userID, fmt.Sprintf("dm:%d:%%", userID), fmt.Sprintf("dm:%%:%d", userID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var ch string
		var ts, pos int64
		if err := rows.Scan(&ch, &ts, &pos); err != nil {
			return nil, err
		}
		s := out[ch]
		s.PeerLastReadAt = ts
		s.PeerLastReadPosition = pos
		out[ch] = s
	}
	return out, rows.Err()
}
