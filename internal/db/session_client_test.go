package db

import (
	"database/sql"
	"testing"
)

// A database from before OTA updates has no sessions.client or
// client_seen_at. InitDB adds both and keeps every session.
func TestSessionClientColumnsMigrate(t *testing.T) {
	initTestDB(t)
	uid := mustCreateUser(t, "alice")
	if err := CreateSession("old-token", uid, NowTS()+3600, "user"); err != nil {
		t.Fatal(err)
	}
	for _, col := range []string{"client", "client_seen_at"} {
		if _, err := pool.Exec("ALTER TABLE sessions DROP COLUMN " + col); err != nil {
			t.Fatal(err)
		}
	}
	oldPool := pool
	if err := InitDB(); err != nil {
		t.Fatalf("InitDB on the old schema: %v", err)
	}
	_ = oldPool.Close()

	s, err := GetSession("old-token")
	if err != nil || s == nil || s.UserID != uid {
		t.Fatalf("session after migration %+v, %v", s, err)
	}
	var client string
	var seen sql.NullInt64
	read := func() {
		t.Helper()
		if err := pool.QueryRow("SELECT client, client_seen_at FROM sessions WHERE token = 'old-token'").Scan(&client, &seen); err != nil {
			t.Fatal(err)
		}
	}
	read()
	if client != "" || seen.Valid {
		t.Fatalf("migrated defaults %q %v", client, seen)
	}
	if err := RecordSessionClient("old-token", "v=1;platform=ios;build=2", 1700000000); err != nil {
		t.Fatal(err)
	}
	read()
	if client != "v=1;platform=ios;build=2" || seen.Int64 != 1700000000 {
		t.Fatalf("recorded %q %v", client, seen)
	}
}

func TestAddMaintenanceRunsInEverySweep(t *testing.T) {
	initTestDB(t)
	t.Cleanup(func() {
		extraMu.Lock()
		extraTasks = nil
		extraMu.Unlock()
	})
	runs := 0
	AddMaintenance(func() { runs++ })
	runMaintenance()
	runMaintenance()
	if runs != 2 {
		t.Fatalf("task ran %d times in 2 sweeps", runs)
	}
}
