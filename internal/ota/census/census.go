// Package census is the install census of OTA updates (OTAUpdatePlan.md,
// D8 item 1): one row per app install, upserted on every manifest request
// and keyed by the install's EAS-Client-ID. It lives in its own SQLite file,
// data/ota/census.db, so a flood of requests cannot fill alexmessage.db.
//
// Every field is an unauthenticated claim of whoever sent the request. The
// census is for reading only: no gate and no automatic action uses it.
package census

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const (
	// MaxRows caps the table. When it is full, existing rows are still
	// updated and new installs are not recorded.
	MaxRows = 5000
	// PruneAfter is how long a row survives without a manifest request.
	PruneAfter = 90 * 24 * time.Hour
	// FileName is the census file inside data/ota.
	FileName = "census.db"
)

const schema = `
CREATE TABLE IF NOT EXISTS installs (
    client_id       TEXT    PRIMARY KEY,   -- EAS-Client-ID, lowercase
    platform        TEXT    NOT NULL,
    runtime         TEXT    NOT NULL,
    current_update  TEXT    NOT NULL DEFAULT '',   -- Expo-Current-Update-ID
    embedded_update TEXT    NOT NULL,              -- Expo-Embedded-Update-ID
    channel         TEXT    NOT NULL,              -- am_channel: production|beta
    failed_updates  TEXT    NOT NULL DEFAULT '',   -- Expo-Recent-Failed-Update-IDs, comma-separated
    fatal_error     TEXT    NOT NULL DEFAULT '',   -- the last Expo-Fatal-Error
    fatal_error_at  INTEGER,
    am_uid          INTEGER,                       -- claimed user id (a hint only)
    first_seen      INTEGER NOT NULL,
    last_seen       INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_installs_am_uid ON installs(am_uid);
CREATE INDEX IF NOT EXISTS idx_installs_last_seen ON installs(last_seen);
`

// rowCap is MaxRows; tests lower it.
var rowCap = MaxRows

// DB is an open census file.
type DB struct {
	pool *sql.DB
}

func dsn(path string) string {
	// The same connection settings as alexmessage.db (internal/db).
	return fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_txlock=immediate", path)
}

// Open opens the census at path, creating the file and its table if needed.
func Open(path string) (*DB, error) {
	pool, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, err
	}
	if _, err := pool.Exec(schema); err != nil {
		pool.Close()
		return nil, err
	}
	return &DB{pool: pool}, nil
}

// Close closes the file.
func (d *DB) Close() error { return d.pool.Close() }

// Report is one manifest request.
type Report struct {
	ClientID         string
	Platform         string
	Runtime          string
	CurrentUpdateID  string
	EmbeddedUpdateID string
	Channel          string
	UserID           int64 // 0: none
	FailedUpdateIDs  []string
	FatalError       string
}

// Result says what Record did.
type Result struct {
	Created bool
	// Refused is true when a new install was not recorded: its embedded
	// update id is not one of a registered binary, or the table is full.
	Refused bool
	// Full is true when the refusal was the row cap.
	Full bool
	// NewFailedIDs are failed update ids this install did not report before.
	NewFailedIDs []string
	// NewFatalError is true when the request carried a fatal error that
	// differs from the one stored.
	NewFatalError bool
}

// Record upserts the row of r.ClientID. A new row is created only when
// registered() confirms that r.EmbeddedUpdateID belongs to a registered
// native build and the table has fewer than MaxRows rows; registered is not
// called for an install that already has a row.
func (d *DB) Record(r Report, registered func() (bool, error), now time.Time) (Result, error) {
	var res Result
	if r.ClientID == "" {
		return res, errors.New("census: no client id")
	}
	ts := now.Unix()
	uid := sql.NullInt64{Int64: r.UserID, Valid: r.UserID > 0}
	failed := strings.Join(r.FailedUpdateIDs, ",")
	fatalAt := sql.NullInt64{Int64: ts, Valid: r.FatalError != ""}

	var oldFailed, oldFatal string
	err := d.pool.QueryRow("SELECT failed_updates, fatal_error FROM installs WHERE client_id = ?", r.ClientID).
		Scan(&oldFailed, &oldFatal)
	switch {
	case err == nil:
		res.NewFailedIDs = newIDs(oldFailed, r.FailedUpdateIDs)
		res.NewFatalError = r.FatalError != "" && r.FatalError != oldFatal
		_, err = d.pool.Exec(`UPDATE installs SET platform = ?, runtime = ?, current_update = ?,
			embedded_update = ?, channel = ?, failed_updates = ?,
			fatal_error = CASE WHEN ? = '' THEN fatal_error ELSE ? END,
			fatal_error_at = CASE WHEN ? = '' THEN fatal_error_at ELSE ? END,
			am_uid = ?, last_seen = ?
			WHERE client_id = ?`,
			r.Platform, r.Runtime, r.CurrentUpdateID, r.EmbeddedUpdateID, r.Channel, failed,
			r.FatalError, r.FatalError, r.FatalError, fatalAt, uid, ts, r.ClientID)
		return res, err
	case !errors.Is(err, sql.ErrNoRows):
		return res, err
	}

	ok, err := registered()
	if err != nil {
		return res, err
	}
	if !ok {
		res.Refused = true
		return res, nil
	}
	var count int
	if err := d.pool.QueryRow("SELECT COUNT(*) FROM installs").Scan(&count); err != nil {
		return res, err
	}
	if count >= rowCap {
		res.Refused, res.Full = true, true
		return res, nil
	}
	res.Created = true
	res.NewFailedIDs = r.FailedUpdateIDs
	res.NewFatalError = r.FatalError != ""
	// ON CONFLICT covers a second request of the same new install that raced
	// this one past the SELECT.
	_, err = d.pool.Exec(`INSERT INTO installs (client_id, platform, runtime, current_update,
			embedded_update, channel, failed_updates, fatal_error, fatal_error_at, am_uid,
			first_seen, last_seen)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(client_id) DO UPDATE SET platform = excluded.platform,
			runtime = excluded.runtime, current_update = excluded.current_update,
			embedded_update = excluded.embedded_update, channel = excluded.channel,
			failed_updates = excluded.failed_updates, am_uid = excluded.am_uid,
			last_seen = excluded.last_seen`,
		r.ClientID, r.Platform, r.Runtime, r.CurrentUpdateID, r.EmbeddedUpdateID, r.Channel,
		failed, r.FatalError, fatalAt, uid, ts, ts)
	return res, err
}

func newIDs(stored string, ids []string) []string {
	old := map[string]bool{}
	for _, id := range strings.Split(stored, ",") {
		old[id] = true
	}
	var fresh []string
	for _, id := range ids {
		if !old[id] {
			fresh = append(fresh, id)
		}
	}
	return fresh
}

// Install is one census row, as GET /api/ota/installs and the data export
// return it. Times are Unix seconds.
type Install struct {
	ClientID         string   `json:"client_id"`
	Platform         string   `json:"platform"`
	Runtime          string   `json:"runtime"`
	CurrentUpdateID  string   `json:"current_update_id"`
	EmbeddedUpdateID string   `json:"embedded_update_id"`
	Channel          string   `json:"channel"`
	FailedUpdateIDs  []string `json:"failed_update_ids"`
	FatalError       string   `json:"fatal_error"`
	FatalErrorAt     *int64   `json:"fatal_error_at"`
	UserID           *int64   `json:"am_uid"`
	FirstSeen        int64    `json:"first_seen"`
	LastSeen         int64    `json:"last_seen"`
}

const selectInstalls = `SELECT client_id, platform, runtime, current_update, embedded_update,
	channel, failed_updates, fatal_error, fatal_error_at, am_uid, first_seen, last_seen FROM installs`

// List returns every row, most recently seen first.
func (d *DB) List() ([]Install, error) {
	return d.query(selectInstalls + " ORDER BY last_seen DESC, client_id")
}

// ForUser returns the rows that claim user id uid.
func (d *DB) ForUser(uid int) ([]Install, error) {
	return d.query(selectInstalls+" WHERE am_uid = ? ORDER BY last_seen DESC, client_id", uid)
}

func (d *DB) query(q string, args ...any) ([]Install, error) {
	rows, err := d.pool.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Install{}
	for rows.Next() {
		var (
			in             Install
			failed         string
			fatalAt, amUID sql.NullInt64
		)
		if err := rows.Scan(&in.ClientID, &in.Platform, &in.Runtime, &in.CurrentUpdateID,
			&in.EmbeddedUpdateID, &in.Channel, &failed, &in.FatalError, &fatalAt, &amUID,
			&in.FirstSeen, &in.LastSeen); err != nil {
			return nil, err
		}
		in.FailedUpdateIDs = []string{}
		if failed != "" {
			in.FailedUpdateIDs = strings.Split(failed, ",")
		}
		if fatalAt.Valid {
			in.FatalErrorAt = &fatalAt.Int64
		}
		if amUID.Valid {
			in.UserID = &amUID.Int64
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// DeleteUser removes the rows that claim user id uid (account deletion).
func (d *DB) DeleteUser(uid int) error {
	_, err := d.pool.Exec("DELETE FROM installs WHERE am_uid = ?", uid)
	return err
}

// Prune deletes rows not seen for PruneAfter.
func (d *DB) Prune(now time.Time) (int64, error) {
	res, err := d.pool.Exec("DELETE FROM installs WHERE last_seen < ?", now.Add(-PruneAfter).Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// openFiles keeps one handle per census path for the process, so the
// manifest endpoint does not reopen the file on every request.
var (
	openMu    sync.Mutex
	openFiles = map[string]*DB{}
)

// Shared returns this process's handle on the census at path, opened on
// first use and never closed. With create false it returns (nil, nil) when
// the file does not exist: readers and account deletion never create a
// census on a server that has no update store.
func Shared(path string, create bool) (*DB, error) {
	openMu.Lock()
	defer openMu.Unlock()
	if d := openFiles[path]; d != nil {
		return d, nil
	}
	if !create {
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		} else if err != nil {
			return nil, err
		}
	}
	d, err := Open(path)
	if err != nil {
		return nil, err
	}
	openFiles[path] = d
	return d, nil
}
