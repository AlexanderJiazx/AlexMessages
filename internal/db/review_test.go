package db

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSameSecondPagingAndCutoffs(t *testing.T) {
	initTestDB(t)
	a := mustCreateUser(t, "alice")
	b := mustCreateUser(t, "bob")
	ch := DMChannelID(a, b)
	ts := NowTS()
	for i := 0; i < 75; i++ {
		if _, err := InsertMessage(fmt.Sprintf("m%03d", i), ch, &b, "hi", nil, "message", &ts); err != nil {
			t.Fatal(err)
		}
	}
	first, err := FetchDMWindow(a, ch, 50, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := FetchDMWindow(a, ch, 50, &ts, first[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 50 || len(second) != 25 {
		t.Fatalf("pages=%d,%d", len(first), len(second))
	}
	if err := SetDMLastRead(a, ch, ts); err != nil {
		t.Fatal(err)
	}
	if _, err := InsertMessage("after-read", ch, &b, "new", nil, "message", &ts); err != nil {
		t.Fatal(err)
	}
	unread, err := CountUnread(a, ch, ts, 0)
	if err != nil || unread != 1 {
		t.Fatalf("unread=%d err=%v", unread, err)
	}
	if err := ClearDMForUser(a, ch, ts); err != nil {
		t.Fatal(err)
	}
	if _, err := InsertMessage("after-clear", ch, &b, "new", nil, "message", &ts); err != nil {
		t.Fatal(err)
	}
	visible, err := FetchDMWindow(a, ch, 50, nil, "")
	if err != nil || len(visible) != 1 || visible[0].ID != "after-clear" {
		t.Fatalf("visible=%+v err=%v", visible, err)
	}
	batched, err := FetchDMHistories(a, 50)
	if err != nil || len(batched[ch]) != 1 {
		t.Fatalf("batched=%+v err=%v", batched, err)
	}
	summaries, err := DMSummaries(a)
	if err != nil || summaries[ch].Unread != 1 {
		t.Fatalf("summaries=%+v err=%v", summaries, err)
	}
}

func TestConcurrentDMStateMutations(t *testing.T) {
	initTestDB(t)
	a := mustCreateUser(t, "alice")
	b := mustCreateUser(t, "bob")
	ch := DMChannelID(a, b)
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := SetDMPinned(a, ch, true); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := SetDMLastRead(a, ch, 100); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	st, err := GetDMState(a, ch)
	if err != nil || !st.Pinned || st.LastReadAt != 100 {
		t.Fatalf("state=%+v err=%v", st, err)
	}
}

func TestProtectLastApprovedAdminAtomically(t *testing.T) {
	initTestDB(t)
	a, err := CreateUser("admin", "hash", "Admin", "approved", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := UpdateUserStatus(a, "disabled"); !errors.Is(err, ErrLastAdministrator) {
		t.Fatal(err)
	}
	if err := UpdateUserStatus(a, "rejected"); !errors.Is(err, ErrLastAdministrator) {
		t.Fatal(err)
	}
	if err := SetAdmin(a, false); !errors.Is(err, ErrLastAdministrator) {
		t.Fatal(err)
	}
	if err := DeleteUser(a); !errors.Is(err, ErrLastAdministrator) {
		t.Fatal(err)
	}
	b, err := CreateUser("admin2", "hash", "Admin2", "approved", true)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, id := range []int{a, b} {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			err := UpdateUserStatus(id, "disabled")
			if err != nil && !errors.Is(err, ErrLastAdministrator) {
				t.Error(err)
			}
		}(id)
	}
	wg.Wait()
	if n, err := CountAdmins(); err != nil || n != 1 {
		t.Fatalf("admins=%d err=%v", n, err)
	}
}

func TestPasswordKeepsBearerAndLogoutRemovesPush(t *testing.T) {
	initTestDB(t)
	a := mustCreateUser(t, "alice")
	for _, token := range []string{"keep", "revoke"} {
		if err := CreateSession(token, a, NowTS()+3600, "user"); err != nil {
			t.Fatal(err)
		}
	}
	if err := ChangePasswordKeepingSession(a, "keep", "newhash"); err != nil {
		t.Fatal(err)
	}
	if s, _ := GetSession("keep"); s == nil {
		t.Fatal("current bearer revoked")
	}
	if s, _ := GetSession("revoke"); s != nil {
		t.Fatal("other bearer retained")
	}
	if _, err := pool.Exec("INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth, created_at) VALUES (?, 'endpoint', 'key', 'auth', ?)", a, NowTS()); err != nil {
		t.Fatal(err)
	}
	if err := LogoutUser(a); err != nil {
		t.Fatal(err)
	}
	if s, _ := GetSession("keep"); s != nil {
		t.Fatal("logout retained bearer")
	}
	if subs, _ := ListPushSubscriptions(a); len(subs) != 0 {
		t.Fatal("logout retained push")
	}
}

func TestUnusedUploadCleanupAndMissingAttachmentRollback(t *testing.T) {
	initTestDB(t)
	a := mustCreateUser(t, "alice")
	b := mustCreateUser(t, "bob")
	ch := DMChannelID(a, b)
	dir, _ := UserUploadDir(a)
	for _, name := range []string{"old.txt", "draft.txt", "referenced.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("file"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-25 * time.Hour)
	for _, name := range []string{"old.txt", "referenced.txt"} {
		if err := os.Chtimes(filepath.Join(dir, name), old, old); err != nil {
			t.Fatal(err)
		}
	}
	atts := []Attachment{{Name: "referenced.txt", URL: fmt.Sprintf("/uploads/%d/referenced.txt", a), Size: 4, Mime: "text/plain"}}
	if _, err := InsertMessageWithAttachments("good", ch, &a, "", nil, "message", "nonce", atts); err != nil {
		t.Fatal(err)
	}
	if _, err := InsertMessageWithAttachments("retry", ch, &a, "", nil, "message", "nonce", atts); err == nil {
		t.Fatal("duplicate nonce inserted")
	}
	if msg, err := MessageByClientID(a, ch, "nonce"); err != nil || msg == nil || msg.ID != "good" {
		t.Fatalf("nonce=%+v err=%v", msg, err)
	}
	if err := PruneUnusedUploads(time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "old.txt")); !os.IsNotExist(err) {
		t.Fatal("orphan retained")
	}
	for _, name := range []string{"draft.txt", "referenced.txt"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	atts[0].URL = fmt.Sprintf("/uploads/%d/missing.txt", a)
	if _, err := InsertMessageWithAttachments("bad", ch, &a, "", nil, "message", "badnonce", atts); err == nil {
		t.Fatal("missing attachment accepted")
	}
	if m, _ := GetMessageMeta("bad"); m != nil {
		t.Fatal("partial message persisted")
	}
}

func TestWALAndSequenceSurvivePurge(t *testing.T) {
	initTestDB(t)
	var mode string
	if err := pool.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal=%s err=%v", mode, err)
	}
	a := mustCreateUser(t, "alice")
	b := mustCreateUser(t, "bob")
	ch := DMChannelID(a, b)
	future := NowTS() + 100
	if _, err := InsertMessage("old", ch, &b, "old", nil, "message", &future); err != nil {
		t.Fatal(err)
	}
	if err := ClearDMForUser(a, ch, future); err != nil {
		t.Fatal(err)
	}
	if err := PurgeMessages(); err != nil {
		t.Fatal(err)
	}
	if _, err := InsertMessage("new", ch, &b, "new", nil, "message", nil); err != nil {
		t.Fatal(err)
	}
	// A rolled-back binary uses the original INSERT without a position column.
	// It must still advance the clock and remain visible after another upgrade.
	if _, err := pool.Exec("INSERT INTO messages (id, channel, user_id, type, text, created_at) VALUES ('legacy', ?, ?, 'message', 'legacy', ?)", ch, b, NowTS()); err != nil {
		t.Fatal(err)
	}
	visible, err := FetchDMWindow(a, ch, 50, nil, "")
	if err != nil || len(visible) != 2 || visible[1].ID != "legacy" || visible[1].Position <= visible[0].Position {
		t.Fatalf("visible=%+v err=%v", visible, err)
	}
}

func TestSequenceMigrationDoesNotReapplyEmptyReadCutoff(t *testing.T) {
	initTestDB(t)
	a := mustCreateUser(t, "alice")
	b := mustCreateUser(t, "bob")
	ch := DMChannelID(a, b)
	ts := NowTS()
	if err := SetDMLastRead(a, ch, ts); err != nil {
		t.Fatal(err)
	}
	if _, err := InsertMessage("new", ch, &b, "new", nil, "message", &ts); err != nil {
		t.Fatal(err)
	}
	oldPool := pool
	if err := InitDB(); err != nil {
		t.Fatal(err)
	}
	_ = oldPool.Close()
	n, err := CountUnread(a, ch, ts, 0)
	if err != nil || n != 1 {
		t.Fatalf("unread after restart=%d err=%v", n, err)
	}
}

// The migration transaction reads before it writes. When another process
// holds the write lock (a sibling binary's maintenance sweep at startup), it
// must wait out busy_timeout instead of failing at once with "database is
// locked", which made that binary exit.
func TestInitDBWaitsForAnotherWriter(t *testing.T) {
	initTestDB(t)
	writer := pool
	tx, err := writer.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("DELETE FROM attachments WHERE 0"); err != nil {
		t.Fatal(err)
	}
	released := make(chan error, 1)
	go func() {
		time.Sleep(300 * time.Millisecond)
		released <- tx.Commit()
	}()
	start := time.Now()
	err = InitDB()
	waited := time.Since(start)
	if commitErr := <-released; commitErr != nil {
		t.Fatal(commitErr)
	}
	_ = writer.Close()
	if err != nil {
		t.Fatalf("InitDB while another connection held the write lock: %v", err)
	}
	if waited < 200*time.Millisecond {
		t.Fatalf("InitDB finished after %v, before the write lock was released", waited)
	}
}
