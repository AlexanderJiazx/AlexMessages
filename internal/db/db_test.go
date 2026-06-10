package db

import (
	"os"
	"path/filepath"
	"testing"
)

// initTestDB points the package at a throwaway directory and opens a fresh
// database there. Each call gets a brand-new schema.
func initTestDB(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	BaseDir = dir
	DataDir = filepath.Join(dir, "data")
	UploadRoot = filepath.Join(DataDir, "uploads")
	AvatarRoot = filepath.Join(DataDir, "avatars")
	DBPath = filepath.Join(DataDir, "alexmessage.db")
	if err := InitDB(); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() {
		if pool != nil {
			_ = pool.Close()
			pool = nil
		}
	})
}

func mustCreateUser(t *testing.T, username string) int {
	t.Helper()
	id, err := CreateUser(username, "hash", username, "approved", false)
	if err != nil {
		t.Fatalf("CreateUser(%s): %v", username, err)
	}
	return id
}

func TestInitDBIdempotentMigrations(t *testing.T) {
	initTestDB(t)
	// Re-running InitDB on an existing database must not fail (the column
	// back-fill has to detect existing columns).
	if err := InitDB(); err != nil {
		t.Fatalf("second InitDB: %v", err)
	}
}

func TestUserAvatarRoundTrip(t *testing.T) {
	initTestDB(t)
	id := mustCreateUser(t, "alice")

	u, err := GetUserByID(id)
	if err != nil || u == nil {
		t.Fatalf("GetUserByID: %v, %v", u, err)
	}
	if u.Avatar != "" {
		t.Fatalf("new user avatar = %q, want empty", u.Avatar)
	}

	if err := SetUserAvatar(id, "/avatars/1_abc.png"); err != nil {
		t.Fatalf("SetUserAvatar: %v", err)
	}
	u, _ = GetUserByID(id)
	if u.Avatar != "/avatars/1_abc.png" {
		t.Fatalf("avatar = %q, want /avatars/1_abc.png", u.Avatar)
	}

	if err := SetUserAvatar(id, ""); err != nil {
		t.Fatalf("clear avatar: %v", err)
	}
	u, _ = GetUserByID(id)
	if u.Avatar != "" {
		t.Fatalf("avatar after clear = %q, want empty", u.Avatar)
	}
}

func TestDeleteUserAvatarFiles(t *testing.T) {
	initTestDB(t)
	one := filepath.Join(AvatarRoot, "1_aaa.png")
	two := filepath.Join(AvatarRoot, "12_bbb.png")
	for _, p := range []string{one, two} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	DeleteUserAvatarFiles(1)
	if _, err := os.Stat(one); !os.IsNotExist(err) {
		t.Fatalf("user 1 avatar still exists")
	}
	// Prefix match must not catch user 12's file.
	if _, err := os.Stat(two); err != nil {
		t.Fatalf("user 12 avatar was wrongly removed: %v", err)
	}
}

func TestAttachmentDimensionsRoundTrip(t *testing.T) {
	initTestDB(t)
	a := mustCreateUser(t, "alice")
	b := mustCreateUser(t, "bob")
	ch := DMChannelID(a, b)

	if _, err := InsertMessage("m1", ch, &a, "photo", nil, "message", nil); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}
	if err := InsertAttachment("m1", a, "p.png", "1/p.png", 123, "image/png", 640, 480); err != nil {
		t.Fatalf("InsertAttachment: %v", err)
	}

	msgs, err := FetchChannelWindow(ch, 50, nil, nil)
	if err != nil {
		t.Fatalf("FetchChannelWindow: %v", err)
	}
	if len(msgs) != 1 || len(msgs[0].Attachments) != 1 {
		t.Fatalf("got %d msgs, want 1 with 1 attachment", len(msgs))
	}
	att := msgs[0].Attachments[0]
	if att.Width != 640 || att.Height != 480 {
		t.Fatalf("dimensions = %dx%d, want 640x480", att.Width, att.Height)
	}
	if att.URL != "/uploads/1/p.png" {
		t.Fatalf("url = %q", att.URL)
	}
}

func TestDMStateReadReceipts(t *testing.T) {
	initTestDB(t)
	a := mustCreateUser(t, "alice")
	b := mustCreateUser(t, "bob")
	ch := DMChannelID(a, b)

	// Alice sends two messages at t=100 and t=200.
	t100, t200 := int64(100), int64(200)
	if _, err := InsertMessage("m1", ch, &a, "hi", nil, "message", &t100); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}
	if _, err := InsertMessage("m2", ch, &a, "there", nil, "message", &t200); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}

	// Bob hasn't read anything: two unread, last_read_at zero.
	st, err := GetDMState(b, ch)
	if err != nil {
		t.Fatalf("GetDMState: %v", err)
	}
	if st.LastReadAt != 0 {
		t.Fatalf("fresh LastReadAt = %d, want 0", st.LastReadAt)
	}
	n, _ := CountUnread(b, ch, st.LastReadAt, st.ClearedAt)
	if n != 2 {
		t.Fatalf("unread = %d, want 2", n)
	}

	// Bob reads up to t=150: one left, and Alice can see Bob's last_read_at.
	if err := SetDMLastRead(b, ch, 150); err != nil {
		t.Fatalf("SetDMLastRead: %v", err)
	}
	st, _ = GetDMState(b, ch)
	if st.LastReadAt != 150 {
		t.Fatalf("LastReadAt = %d, want 150", st.LastReadAt)
	}
	n, _ = CountUnread(b, ch, st.LastReadAt, st.ClearedAt)
	if n != 1 {
		t.Fatalf("unread after partial read = %d, want 1", n)
	}

	// The reader's own messages never count as unread.
	n, _ = CountUnread(a, ch, 0, 0)
	if n != 0 {
		t.Fatalf("sender unread = %d, want 0", n)
	}

	// Mark-unread forces the sticky flag and zeroes last_read_at.
	if err := SetDMUnread(b, ch); err != nil {
		t.Fatalf("SetDMUnread: %v", err)
	}
	st, _ = GetDMState(b, ch)
	if !st.ForceUnread || st.LastReadAt != 0 {
		t.Fatalf("after SetDMUnread: %+v", st)
	}
	// Reading again clears the sticky flag.
	if err := SetDMLastRead(b, ch, 250); err != nil {
		t.Fatalf("SetDMLastRead: %v", err)
	}
	st, _ = GetDMState(b, ch)
	if st.ForceUnread || st.LastReadAt != 250 {
		t.Fatalf("after re-read: %+v", st)
	}
}

func TestDMChannelIDParseRoundTrip(t *testing.T) {
	if got := DMChannelID(7, 3); got != "dm:3:7" {
		t.Fatalf("DMChannelID(7,3) = %q, want dm:3:7", got)
	}
	a, b, ok := ParseDMChannel("dm:3:7")
	if !ok || a != 3 || b != 7 {
		t.Fatalf("ParseDMChannel = %d,%d,%v", a, b, ok)
	}
	for _, bad := range []string{"general", "dm:3", "dm:x:7", "dm:3:7:9", ""} {
		if _, _, ok := ParseDMChannel(bad); ok {
			t.Fatalf("ParseDMChannel(%q) unexpectedly ok", bad)
		}
	}
}

func TestFetchChannelWindowPaging(t *testing.T) {
	initTestDB(t)
	a := mustCreateUser(t, "alice")
	b := mustCreateUser(t, "bob")
	ch := DMChannelID(a, b)
	for i := int64(1); i <= 5; i++ {
		ts := i * 10
		id := string(rune('a' + i))
		if _, err := InsertMessage(id, ch, &a, "msg", nil, "message", &ts); err != nil {
			t.Fatalf("InsertMessage: %v", err)
		}
	}
	// Latest two, chronological.
	msgs, err := FetchChannelWindow(ch, 2, nil, nil)
	if err != nil {
		t.Fatalf("FetchChannelWindow: %v", err)
	}
	if len(msgs) != 2 || msgs[0].CreatedAt != 40 || msgs[1].CreatedAt != 50 {
		t.Fatalf("latest window wrong: %+v", msgs)
	}
	// Older-than-40 page.
	before := int64(40)
	msgs, _ = FetchChannelWindow(ch, 10, &before, nil)
	if len(msgs) != 3 || msgs[len(msgs)-1].CreatedAt != 30 {
		t.Fatalf("before window wrong: %+v", msgs)
	}
	// after (cleared_at) hides old history.
	after := int64(30)
	msgs, _ = FetchChannelWindow(ch, 10, nil, &after)
	if len(msgs) != 2 || msgs[0].CreatedAt != 40 {
		t.Fatalf("after window wrong: %+v", msgs)
	}
}
