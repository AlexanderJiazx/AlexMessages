// Package db is the SQLite layer for AlexMessage.
//
// The original Python used a connection-per-call pattern in autocommit mode.
// Go's database/sql gives us a connection pool with the same effect: each
// Exec/Query runs as its own implicit transaction. All write paths go through
// helpers in this package so the schema stays in one place.
package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Paths mirror the Python layout: data/ next to the working directory.
var (
	BaseDir    = mustBaseDir()
	DataDir    = filepath.Join(BaseDir, "data")
	UploadRoot = filepath.Join(DataDir, "uploads")
	AvatarRoot = filepath.Join(DataDir, "avatars")
	DBPath     = filepath.Join(DataDir, "alexmessage.db")
)

// pool is the shared connection pool. Opened lazily by InitDB.
var pool *sql.DB

func mustBaseDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

const schema = `
CREATE TABLE IF NOT EXISTS users (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    username        TEXT    UNIQUE NOT NULL,
    password_hash   TEXT    NOT NULL,
    display_name    TEXT    NOT NULL,
    bio             TEXT    NOT NULL DEFAULT '',
    avatar          TEXT    NOT NULL DEFAULT '',          -- /avatars/<file> or ''
    status          TEXT    NOT NULL DEFAULT 'pending',   -- pending|approved|rejected|disabled
    is_admin        INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL,
    approved_at     INTEGER
);

CREATE TABLE IF NOT EXISTS sessions (
    token       TEXT    PRIMARY KEY,
    user_id     INTEGER NOT NULL,
    scope       TEXT    NOT NULL DEFAULT 'user',   -- 'user' or 'admin'
    created_at  INTEGER NOT NULL,
    expires_at  INTEGER NOT NULL,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);

CREATE TABLE IF NOT EXISTS messages (
    id          TEXT    PRIMARY KEY,
    channel     TEXT    NOT NULL,   -- 'general'/'tech'/... or 'dm:<minId>:<maxId>'
    user_id     INTEGER,            -- null for system
    type        TEXT    NOT NULL DEFAULT 'message',
    text        TEXT    NOT NULL DEFAULT '',
    reply_to    TEXT,
    created_at  INTEGER NOT NULL,
    edited_at   INTEGER,            -- null until the author edits the message
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_msg_chan_time ON messages(channel, created_at);

CREATE TABLE IF NOT EXISTS attachments (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    message_id  TEXT    NOT NULL,
    user_id     INTEGER NOT NULL,
    name        TEXT    NOT NULL,
    rel_path    TEXT    NOT NULL,
    size        INTEGER NOT NULL,
    mime        TEXT    NOT NULL,
    width       INTEGER NOT NULL DEFAULT 0,   -- image pixel size, 0 when unknown
    height      INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL,
    FOREIGN KEY (message_id) REFERENCES messages(id) ON DELETE CASCADE,
    FOREIGN KEY (user_id)    REFERENCES users(id)    ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_att_msg ON attachments(message_id);

CREATE TABLE IF NOT EXISTS contacts (
    owner_id    INTEGER NOT NULL,
    contact_id  INTEGER NOT NULL,
    added_at    INTEGER NOT NULL,
    PRIMARY KEY (owner_id, contact_id),
    FOREIGN KEY (owner_id)   REFERENCES users(id) ON DELETE CASCADE,
    FOREIGN KEY (contact_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS calls (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    caller_id    INTEGER,            -- nullable so user deletion doesn't lose history
    callee_id    INTEGER,
    started_at   INTEGER NOT NULL,   -- when the call was initiated
    answered_at  INTEGER,            -- when callee picked up; null if never answered
    ended_at     INTEGER NOT NULL,   -- when the call entry was finalized
    status       TEXT    NOT NULL,   -- 'completed'|'missed'|'declined'|'cancelled'|'failed'
    FOREIGN KEY (caller_id) REFERENCES users(id) ON DELETE SET NULL,
    FOREIGN KEY (callee_id) REFERENCES users(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_calls_caller_time ON calls(caller_id, started_at);
CREATE INDEX IF NOT EXISTS idx_calls_callee_time ON calls(callee_id, started_at);

CREATE TABLE IF NOT EXISTS push_subscriptions (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL,
    endpoint    TEXT    NOT NULL UNIQUE,
    p256dh      TEXT    NOT NULL,
    auth        TEXT    NOT NULL,
    user_agent  TEXT    NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_push_user ON push_subscriptions(user_id);

CREATE TABLE IF NOT EXISTS dm_state (
    user_id      INTEGER NOT NULL,
    channel      TEXT    NOT NULL,
    pinned       INTEGER NOT NULL DEFAULT 0,
    last_read_at INTEGER NOT NULL DEFAULT 0,
    cleared_at   INTEGER NOT NULL DEFAULT 0,   -- delete-for-me cutoff
    force_unread INTEGER NOT NULL DEFAULT 0,   -- explicit "Mark as unread" sticky flag
    PRIMARY KEY (user_id, channel),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
`

// NowTS returns the current Unix time in whole seconds, matching int(time.time()).
func NowTS() int64 {
	return time.Now().Unix()
}

// InitDB opens the pool, applies the schema, and back-fills any missing columns.
func InitDB() error {
	if err := os.MkdirAll(DataDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(UploadRoot, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(AvatarRoot, 0o755); err != nil {
		return err
	}
	// foreign_keys + busy_timeout are applied on every pooled connection.
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)", DBPath)
	p, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	pool = p
	if _, err := pool.Exec(schema); err != nil {
		return err
	}
	// Back-fill columns added after the original schema shipped.
	migrations := []struct{ table, column, ddl string }{
		{"dm_state", "force_unread", "ALTER TABLE dm_state ADD COLUMN force_unread INTEGER NOT NULL DEFAULT 0"},
		{"users", "avatar", "ALTER TABLE users ADD COLUMN avatar TEXT NOT NULL DEFAULT ''"},
		{"attachments", "width", "ALTER TABLE attachments ADD COLUMN width INTEGER NOT NULL DEFAULT 0"},
		{"attachments", "height", "ALTER TABLE attachments ADD COLUMN height INTEGER NOT NULL DEFAULT 0"},
		{"messages", "edited_at", "ALTER TABLE messages ADD COLUMN edited_at INTEGER"},
	}
	for _, m := range migrations {
		if err := ensureColumn(m.table, m.column, m.ddl); err != nil {
			return err
		}
	}
	return nil
}

func ensureColumn(table, column, ddl string) error {
	rows, err := pool.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid       int
			name, typ string
			notnull   int
			dflt      sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = pool.Exec(ddl)
	// All three server binaries run InitDB at startup; if another process adds
	// the column between our PRAGMA check and the ALTER, treat it as done.
	if err != nil && strings.Contains(err.Error(), "duplicate column name") {
		return nil
	}
	return err
}

// ---------- users ----------

// User mirrors a row of the users table.
type User struct {
	ID           int
	Username     string
	PasswordHash string
	DisplayName  string
	Bio          string
	Avatar       string
	Status       string
	IsAdmin      bool
	CreatedAt    int64
	ApprovedAt   *int64
}

func scanUser(s interface{ Scan(...any) error }) (*User, error) {
	var (
		u          User
		isAdmin    int
		approvedAt sql.NullInt64
	)
	err := s.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.DisplayName,
		&u.Bio, &u.Avatar, &u.Status, &isAdmin, &u.CreatedAt, &approvedAt)
	if err != nil {
		return nil, err
	}
	u.IsAdmin = isAdmin != 0
	if approvedAt.Valid {
		v := approvedAt.Int64
		u.ApprovedAt = &v
	}
	return &u, nil
}

const userCols = "id, username, password_hash, display_name, bio, avatar, status, is_admin, created_at, approved_at"

// CreateUser inserts a new account and returns its id.
func CreateUser(username, passwordHash, displayName, status string, isAdmin bool) (int, error) {
	ts := NowTS()
	var approvedAt any
	if status == "approved" {
		approvedAt = ts
	}
	res, err := pool.Exec(
		"INSERT INTO users (username, password_hash, display_name, status, is_admin, created_at, approved_at) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?)",
		username, passwordHash, displayName, status, boolToInt(isAdmin), ts, approvedAt,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}

func GetUserByID(userID int) (*User, error) {
	row := pool.QueryRow("SELECT "+userCols+" FROM users WHERE id = ?", userID)
	u, err := scanUser(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func GetUserByUsername(username string) (*User, error) {
	row := pool.QueryRow("SELECT "+userCols+" FROM users WHERE username = ?", username)
	u, err := scanUser(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

// ListUsers returns every user (newest first), optionally filtered by status.
func ListUsers(status string) ([]*User, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if status != "" {
		rows, err = pool.Query("SELECT "+userCols+" FROM users WHERE status = ? ORDER BY created_at DESC", status)
	} else {
		rows, err = pool.Query("SELECT " + userCols + " FROM users ORDER BY created_at DESC")
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func UpdateUserStatus(userID int, status string) error {
	var approvedAt any
	if status == "approved" {
		approvedAt = NowTS()
	}
	// COALESCE keeps the original approved_at when the new status isn't 'approved'.
	_, err := pool.Exec(
		"UPDATE users SET status = ?, approved_at = COALESCE(?, approved_at) WHERE id = ?",
		status, approvedAt, userID,
	)
	return err
}

// UpdateUserProfile updates display_name and/or bio. Nil pointers are skipped.
func UpdateUserProfile(userID int, displayName, bio *string) error {
	var sets []string
	var vals []any
	if displayName != nil {
		sets = append(sets, "display_name = ?")
		vals = append(vals, *displayName)
	}
	if bio != nil {
		sets = append(sets, "bio = ?")
		vals = append(vals, *bio)
	}
	if len(sets) == 0 {
		return nil
	}
	vals = append(vals, userID)
	_, err := pool.Exec("UPDATE users SET "+strings.Join(sets, ", ")+" WHERE id = ?", vals...)
	return err
}

// SetUserAvatar stores the public avatar URL ('' clears it).
func SetUserAvatar(userID int, avatar string) error {
	_, err := pool.Exec("UPDATE users SET avatar = ? WHERE id = ?", avatar, userID)
	return err
}

func SetPassword(userID int, passwordHash string) error {
	_, err := pool.Exec("UPDATE users SET password_hash = ? WHERE id = ?", passwordHash, userID)
	return err
}

func SetAdmin(userID int, isAdmin bool) error {
	_, err := pool.Exec("UPDATE users SET is_admin = ? WHERE id = ?", boolToInt(isAdmin), userID)
	return err
}

func DeleteUser(userID int) error {
	_, err := pool.Exec("DELETE FROM users WHERE id = ?", userID)
	return err
}

// CountAdmins returns the number of accounts with the admin flag set.
func CountAdmins() (int, error) {
	var n int
	err := pool.QueryRow("SELECT COUNT(*) FROM users WHERE is_admin = 1").Scan(&n)
	return n, err
}

// UserToPublic is the admin-facing user view (db.user_to_public in Python).
func UserToPublic(u *User) map[string]any {
	return map[string]any{
		"id":           u.ID,
		"username":     u.Username,
		"display_name": u.DisplayName,
		"bio":          u.Bio,
		"status":       u.Status,
		"is_admin":     u.IsAdmin,
		"created_at":   u.CreatedAt,
	}
}

// ---------- sessions ----------

// Session mirrors a row of the sessions table.
type Session struct {
	Token     string
	UserID    int
	Scope     string
	CreatedAt int64
	ExpiresAt int64
}

func CreateSession(token string, userID int, expiresAt int64, scope string) error {
	_, err := pool.Exec(
		"INSERT INTO sessions (token, user_id, scope, created_at, expires_at) VALUES (?, ?, ?, ?, ?)",
		token, userID, scope, NowTS(), expiresAt,
	)
	return err
}

// GetSession returns the session for a token, deleting and returning nil if expired.
func GetSession(token string) (*Session, error) {
	row := pool.QueryRow(
		"SELECT token, user_id, scope, created_at, expires_at FROM sessions WHERE token = ?", token)
	var s Session
	err := row.Scan(&s.Token, &s.UserID, &s.Scope, &s.CreatedAt, &s.ExpiresAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if s.ExpiresAt < NowTS() {
		_, _ = pool.Exec("DELETE FROM sessions WHERE token = ?", token)
		return nil, nil
	}
	return &s, nil
}

func DeleteSession(token string) error {
	_, err := pool.Exec("DELETE FROM sessions WHERE token = ?", token)
	return err
}

func DeleteUserSessions(userID int) error {
	_, err := pool.Exec("DELETE FROM sessions WHERE user_id = ?", userID)
	return err
}

// ---------- messages ----------

// InsertMessage writes a message row and returns its created_at timestamp.
func InsertMessage(msgID, channel string, userID *int, text string, replyTo *string, msgType string, createdAt *int64) (int64, error) {
	ts := NowTS()
	if createdAt != nil {
		ts = *createdAt
	}
	_, err := pool.Exec(
		"INSERT INTO messages (id, channel, user_id, type, text, reply_to, created_at) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?)",
		msgID, channel, nullableInt(userID), msgType, text, nullableStr(replyTo), ts,
	)
	return ts, err
}

// MessageMeta is the slice of a message row the edit path needs to authorize
// and route a change.
type MessageMeta struct {
	ID      string
	Channel string
	UserID  *int
	Type    string
}

// GetMessageMeta returns id/channel/author/type for a message, or nil if absent.
func GetMessageMeta(msgID string) (*MessageMeta, error) {
	row := pool.QueryRow("SELECT id, channel, user_id, type FROM messages WHERE id = ?", msgID)
	var (
		m      MessageMeta
		userID sql.NullInt64
	)
	err := row.Scan(&m.ID, &m.Channel, &userID, &m.Type)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	m.UserID = nullInt64ToPtr(userID)
	return &m, nil
}

// UpdateMessageText rewrites a message body and stamps edited_at.
func UpdateMessageText(msgID, text string, editedAt int64) error {
	_, err := pool.Exec("UPDATE messages SET text = ?, edited_at = ? WHERE id = ?", text, editedAt, msgID)
	return err
}

func InsertAttachment(messageID string, userID int, name, relPath string, size int64, mime string, width, height int) error {
	_, err := pool.Exec(
		"INSERT INTO attachments (message_id, user_id, name, rel_path, size, mime, width, height, created_at) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		messageID, userID, name, relPath, size, mime, width, height, NowTS(),
	)
	return err
}

// ---------- contacts ----------

func AddContact(ownerID, contactID int) error {
	if ownerID == contactID {
		return nil
	}
	_, err := pool.Exec(
		"INSERT OR IGNORE INTO contacts (owner_id, contact_id, added_at) VALUES (?, ?, ?)",
		ownerID, contactID, NowTS(),
	)
	return err
}

func RemoveContact(ownerID, contactID int) error {
	_, err := pool.Exec("DELETE FROM contacts WHERE owner_id = ? AND contact_id = ?", ownerID, contactID)
	return err
}

func ListContacts(ownerID int) ([]int, error) {
	rows, err := pool.Query("SELECT contact_id FROM contacts WHERE owner_id = ?", ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int{}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ---------- helpers ----------

// DMChannelID builds the synthetic channel id for a DM thread, smaller id first.
func DMChannelID(a, b int) string {
	lo, hi := a, b
	if hi < lo {
		lo, hi = hi, lo
	}
	return fmt.Sprintf("dm:%d:%d", lo, hi)
}

// ParseDMChannel reverses DMChannelID. ok is false for non-DM/malformed channels.
// The two ids are returned in the order they appear in the string (not sorted).
func ParseDMChannel(channel string) (a, b int, ok bool) {
	if !strings.HasPrefix(channel, "dm:") {
		return 0, 0, false
	}
	parts := strings.Split(channel, ":")
	if len(parts) != 3 {
		return 0, 0, false
	}
	var err1, err2 error
	a, err1 = strconv.Atoi(parts[1])
	b, err2 = strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return a, b, true
}

// UserUploadDir returns (and creates) the per-user upload directory.
func UserUploadDir(userID int) (string, error) {
	p := filepath.Join(UploadRoot, strconv.Itoa(userID))
	if err := os.MkdirAll(p, 0o755); err != nil {
		return "", err
	}
	return p, nil
}

// DeleteUserUploads best-effort removes a user's upload directory.
func DeleteUserUploads(userID int) {
	_ = os.RemoveAll(filepath.Join(UploadRoot, strconv.Itoa(userID)))
}

// DeleteUserAvatarFiles best-effort removes a user's avatar files on disk.
// Avatars are stored as <uid>_<token>.<ext> so the prefix is unambiguous.
func DeleteUserAvatarFiles(userID int) {
	entries, err := os.ReadDir(AvatarRoot)
	if err != nil {
		return
	}
	prefix := strconv.Itoa(userID) + "_"
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
			_ = os.Remove(filepath.Join(AvatarRoot, e.Name()))
		}
	}
}

// ---------- calls ----------

// Call mirrors a row of the calls table, with a direction relative to a viewer.
type Call struct {
	ID         int    `json:"id"`
	CallerID   *int   `json:"caller_id"`
	CalleeID   *int   `json:"callee_id"`
	StartedAt  int64  `json:"started_at"`
	AnsweredAt *int64 `json:"answered_at"`
	EndedAt    int64  `json:"ended_at"`
	Status     string `json:"status"`
	Direction  string `json:"direction"`
}

func InsertCall(callerID, calleeID *int, startedAt int64, answeredAt *int64, endedAt int64, status string) (int, error) {
	res, err := pool.Exec(
		"INSERT INTO calls (caller_id, callee_id, started_at, answered_at, ended_at, status) "+
			"VALUES (?, ?, ?, ?, ?, ?)",
		nullableInt(callerID), nullableInt(calleeID), startedAt, nullableInt64(answeredAt), endedAt, status,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}

// ListRecentCalls returns calls where the user is caller or callee, newest first.
func ListRecentCalls(userID, limit int) ([]Call, error) {
	rows, err := pool.Query(
		"SELECT id, caller_id, callee_id, started_at, answered_at, ended_at, status FROM calls "+
			"WHERE caller_id = ? OR callee_id = ? ORDER BY started_at DESC LIMIT ?",
		userID, userID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Call{}
	for rows.Next() {
		var (
			c                              Call
			caller, callee, answered       sql.NullInt64
		)
		if err := rows.Scan(&c.ID, &caller, &callee, &c.StartedAt, &answered, &c.EndedAt, &c.Status); err != nil {
			return nil, err
		}
		c.CallerID = nullInt64ToPtr(caller)
		c.CalleeID = nullInt64ToPtr(callee)
		c.AnsweredAt = nullInt64ToPtrI64(answered)
		c.Direction = "incoming"
		if c.CallerID != nil && *c.CallerID == userID {
			c.Direction = "outgoing"
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---------- push subscriptions ----------

// PushSubscription is a stored browser push endpoint with its encryption keys.
type PushSubscription struct {
	Endpoint string
	P256dh   string
	Auth     string
}

// UpsertPushSubscription inserts or refreshes a subscription keyed by endpoint.
// If another user owned this endpoint, the row is reassigned to user_id.
func UpsertPushSubscription(userID int, endpoint, p256dh, authToken, userAgent string) error {
	_, err := pool.Exec(
		"INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth, user_agent, created_at) "+
			"VALUES (?, ?, ?, ?, ?, ?) "+
			"ON CONFLICT(endpoint) DO UPDATE SET "+
			"user_id = excluded.user_id, "+
			"p256dh = excluded.p256dh, "+
			"auth = excluded.auth, "+
			"user_agent = excluded.user_agent",
		userID, endpoint, p256dh, authToken, userAgent, NowTS(),
	)
	return err
}

func RemovePushSubscription(endpoint string) error {
	_, err := pool.Exec("DELETE FROM push_subscriptions WHERE endpoint = ?", endpoint)
	return err
}

func ListPushSubscriptions(userID int) ([]PushSubscription, error) {
	rows, err := pool.Query(
		"SELECT endpoint, p256dh, auth FROM push_subscriptions WHERE user_id = ?", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PushSubscription{}
	for rows.Next() {
		var s PushSubscription
		if err := rows.Scan(&s.Endpoint, &s.P256dh, &s.Auth); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ---------- per-user DM state (pin / unread / delete-for-me) ----------

// DMState carries the four per-user flags for a DM thread.
type DMState struct {
	Pinned     bool
	LastReadAt int64
	ClearedAt  int64
	ForceUnread bool
}

// GetDMState returns the state row, or zero-value defaults when absent.
func GetDMState(userID int, channel string) (DMState, error) {
	row := pool.QueryRow(
		"SELECT pinned, last_read_at, cleared_at, force_unread FROM dm_state WHERE user_id = ? AND channel = ?",
		userID, channel,
	)
	var (
		st                               DMState
		pinned, forceUnread              int
	)
	err := row.Scan(&pinned, &st.LastReadAt, &st.ClearedAt, &forceUnread)
	if err == sql.ErrNoRows {
		return DMState{}, nil
	}
	if err != nil {
		return DMState{}, err
	}
	st.Pinned = pinned != 0
	st.ForceUnread = forceUnread != 0
	return st, nil
}

// ListDMStates returns every DM-state row for a user, keyed by channel.
func ListDMStates(userID int) (map[string]DMState, error) {
	rows, err := pool.Query(
		"SELECT channel, pinned, last_read_at, cleared_at, force_unread FROM dm_state WHERE user_id = ?",
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]DMState{}
	for rows.Next() {
		var (
			ch                  string
			st                  DMState
			pinned, forceUnread int
		)
		if err := rows.Scan(&ch, &pinned, &st.LastReadAt, &st.ClearedAt, &forceUnread); err != nil {
			return nil, err
		}
		st.Pinned = pinned != 0
		st.ForceUnread = forceUnread != 0
		out[ch] = st
	}
	return out, rows.Err()
}

// writeDMState upserts a full DM-state row.
func writeDMState(userID int, channel string, st DMState) error {
	_, err := pool.Exec(
		"INSERT INTO dm_state (user_id, channel, pinned, last_read_at, cleared_at, force_unread) "+
			"VALUES (?, ?, ?, ?, ?, ?) "+
			"ON CONFLICT(user_id, channel) DO UPDATE SET "+
			"pinned = excluded.pinned, "+
			"last_read_at = excluded.last_read_at, "+
			"cleared_at = excluded.cleared_at, "+
			"force_unread = excluded.force_unread",
		userID, channel, boolToInt(st.Pinned), st.LastReadAt, st.ClearedAt, boolToInt(st.ForceUnread),
	)
	return err
}

// SetDMPinned merges a new pinned flag into the (user, channel) row.
func SetDMPinned(userID int, channel string, pinned bool) error {
	st, err := GetDMState(userID, channel)
	if err != nil {
		return err
	}
	st.Pinned = pinned
	return writeDMState(userID, channel, st)
}

// SetDMLastRead bumps last_read_at and clears any explicit-unread flag.
func SetDMLastRead(userID int, channel string, ts int64) error {
	st, err := GetDMState(userID, channel)
	if err != nil {
		return err
	}
	st.LastReadAt = ts
	st.ForceUnread = false
	return writeDMState(userID, channel, st)
}

// SetDMUnread forces an unread state — zero last_read_at and flag it sticky.
func SetDMUnread(userID int, channel string) error {
	st, err := GetDMState(userID, channel)
	if err != nil {
		return err
	}
	st.LastReadAt = 0
	st.ForceUnread = true
	return writeDMState(userID, channel, st)
}

// ClearDMForUser is the per-user delete: hide existing messages and reset flags.
func ClearDMForUser(userID int, channel string, ts int64) error {
	st, err := GetDMState(userID, channel)
	if err != nil {
		return err
	}
	st.ClearedAt = ts
	st.LastReadAt = ts
	st.ForceUnread = false
	st.Pinned = false
	return writeDMState(userID, channel, st)
}

// ChannelLatestMessageTS returns the newest message timestamp in a channel, or 0.
func ChannelLatestMessageTS(channel string) (int64, error) {
	var ts sql.NullInt64
	err := pool.QueryRow(
		"SELECT created_at FROM messages WHERE channel = ? ORDER BY created_at DESC LIMIT 1", channel,
	).Scan(&ts)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if ts.Valid {
		return ts.Int64, nil
	}
	return 0, nil
}

// CountUnread counts messages newer than max(last_read_at, cleared_at) not by user.
func CountUnread(userID int, channel string, lastReadAt, clearedAt int64) (int, error) {
	cutoff := lastReadAt
	if clearedAt > cutoff {
		cutoff = clearedAt
	}
	var n int
	err := pool.QueryRow(
		"SELECT COUNT(*) FROM messages WHERE channel = ? AND created_at > ? "+
			"AND user_id IS NOT NULL AND user_id <> ?",
		channel, cutoff, userID,
	).Scan(&n)
	return n, err
}

// Attachment is the embedded attachment view returned with history messages.
// Width/Height are the pixel dimensions for images (0 when unknown) so the
// client can reserve a correctly sized placeholder while the image loads.
type Attachment struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Size   int64  `json:"size"`
	Mime   string `json:"mime"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// HistoryMessage is the message shape used by history/init payloads (no author).
type HistoryMessage struct {
	ID          string       `json:"id"`
	Type        string       `json:"type"`
	Channel     string       `json:"channel"`
	UserID      *int         `json:"user_id"`
	Text        string       `json:"text"`
	ReplyTo     *string      `json:"reply_to"`
	CreatedAt   int64        `json:"created_at"`
	EditedAt    *int64       `json:"edited_at"`
	Attachments []Attachment `json:"attachments"`
}

// FetchChannelWindow returns a chronological page of messages with attachments.
// beforeTS (when non-nil) is exclusive (older than); afterTS (when non-nil) is
// exclusive (newer than).
func FetchChannelWindow(channel string, limit int, beforeTS, afterTS *int64) ([]HistoryMessage, error) {
	where := []string{"channel = ?"}
	args := []any{channel}
	if beforeTS != nil {
		where = append(where, "created_at < ?")
		args = append(args, *beforeTS)
	}
	if afterTS != nil {
		where = append(where, "created_at > ?")
		args = append(args, *afterTS)
	}
	args = append(args, limit)
	query := "SELECT id, channel, user_id, type, text, reply_to, created_at, edited_at FROM messages WHERE " +
		strings.Join(where, " AND ") + " ORDER BY created_at DESC LIMIT ?"

	rows, err := pool.Query(query, args...)
	if err != nil {
		return nil, err
	}
	type rawMsg struct {
		id, channel, typ, text string
		userID                 *int
		replyTo                *string
		createdAt              int64
		editedAt               *int64
	}
	var msgs []rawMsg
	for rows.Next() {
		var (
			m        rawMsg
			userID   sql.NullInt64
			replyTo  sql.NullString
			editedAt sql.NullInt64
		)
		if err := rows.Scan(&m.id, &m.channel, &userID, &m.typ, &m.text, &replyTo, &m.createdAt, &editedAt); err != nil {
			rows.Close()
			return nil, err
		}
		if userID.Valid {
			v := int(userID.Int64)
			m.userID = &v
		}
		if replyTo.Valid {
			v := replyTo.String
			m.replyTo = &v
		}
		m.editedAt = nullInt64ToPtrI64(editedAt)
		msgs = append(msgs, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Reverse to chronological order (query fetched newest-first).
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	if len(msgs) == 0 {
		return []HistoryMessage{}, nil
	}

	// Fetch attachments for the page in one query.
	ids := make([]any, len(msgs))
	placeholders := make([]string, len(msgs))
	for i, m := range msgs {
		ids[i] = m.id
		placeholders[i] = "?"
	}
	byMsg := map[string][]Attachment{}
	attRows, err := pool.Query(
		"SELECT message_id, name, rel_path, size, mime, width, height FROM attachments WHERE message_id IN ("+
			strings.Join(placeholders, ",")+")", ids...,
	)
	if err != nil {
		return nil, err
	}
	for attRows.Next() {
		var (
			msgID, name, relPath, mime string
			size                       int64
			width, height              int
		)
		if err := attRows.Scan(&msgID, &name, &relPath, &size, &mime, &width, &height); err != nil {
			attRows.Close()
			return nil, err
		}
		byMsg[msgID] = append(byMsg[msgID], Attachment{
			Name:   name,
			URL:    "/uploads/" + relPath,
			Size:   size,
			Mime:   mime,
			Width:  width,
			Height: height,
		})
	}
	attRows.Close()
	if err := attRows.Err(); err != nil {
		return nil, err
	}

	out := make([]HistoryMessage, 0, len(msgs))
	for _, m := range msgs {
		atts := byMsg[m.id]
		if atts == nil {
			atts = []Attachment{}
		}
		out = append(out, HistoryMessage{
			ID:          m.id,
			Type:        m.typ,
			Channel:     m.channel,
			UserID:      m.userID,
			Text:        m.text,
			ReplyTo:     m.replyTo,
			CreatedAt:   m.createdAt,
			EditedAt:    m.editedAt,
			Attachments: atts,
		})
	}
	return out, nil
}

// DMPartnerIDs returns ids the viewer has an existing DM thread with.
// (Shared by runtime.visible_user_ids and the WS init builder.)
func DMPartnerIDs(viewerID int) (map[int]struct{}, error) {
	rows, err := pool.Query(
		"SELECT DISTINCT channel FROM messages WHERE channel LIKE 'dm:%' AND "+
			"(channel LIKE ? OR channel LIKE ?)",
		fmt.Sprintf("dm:%d:%%", viewerID), fmt.Sprintf("dm:%%:%d", viewerID),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := map[int]struct{}{}
	for rows.Next() {
		var ch string
		if err := rows.Scan(&ch); err != nil {
			return nil, err
		}
		if a, b, ok := ParseDMChannel(ch); ok {
			other := a
			if b != viewerID {
				other = b
			}
			ids[other] = struct{}{}
		}
	}
	return ids, rows.Err()
}

// ---------- admin stats / moderation ----------

// Stats returns the counts shown on the admin dashboard.
func Stats() (map[string]any, error) {
	count := func(q string) (int, error) {
		var n int
		err := pool.QueryRow(q).Scan(&n)
		return n, err
	}
	usersTotal, err := count("SELECT COUNT(*) FROM users")
	if err != nil {
		return nil, err
	}
	pending, err := count("SELECT COUNT(*) FROM users WHERE status = 'pending'")
	if err != nil {
		return nil, err
	}
	approved, err := count("SELECT COUNT(*) FROM users WHERE status = 'approved'")
	if err != nil {
		return nil, err
	}
	messagesTotal, err := count("SELECT COUNT(*) FROM messages")
	if err != nil {
		return nil, err
	}
	attachmentsTotal, err := count("SELECT COUNT(*) FROM attachments")
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"users_total":       usersTotal,
		"pending":           pending,
		"approved":          approved,
		"messages_total":    messagesTotal,
		"attachments_total": attachmentsTotal,
	}, nil
}

// PurgeMessages wipes all message history (channels and DMs).
func PurgeMessages() error {
	if _, err := pool.Exec("DELETE FROM messages"); err != nil {
		return err
	}
	_, err := pool.Exec("DELETE FROM attachments")
	return err
}

// ---------- small scalar helpers ----------

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullableInt(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullableInt64(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullableStr(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullInt64ToPtr(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
}

func nullInt64ToPtrI64(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

// SortedKeys returns the integer keys of a set in ascending order (helper used
// by callers that need deterministic ordering, e.g. presence rosters).
func SortedKeys(set map[int]struct{}) []int {
	out := make([]int, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}
