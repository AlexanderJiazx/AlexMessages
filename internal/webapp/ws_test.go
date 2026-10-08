package webapp

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"alexmessage/internal/auth"
	"alexmessage/internal/db"
)

func initWSTestDB(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	db.BaseDir = dir
	db.DataDir = filepath.Join(dir, "data")
	db.UploadRoot = filepath.Join(db.DataDir, "uploads")
	db.AvatarRoot = filepath.Join(db.DataDir, "avatars")
	db.DBPath = filepath.Join(db.DataDir, "alexmessage.db")
	if err := db.InitDB(); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	// Close the pool before t.TempDir cleanup so the dir empties on Linux CI.
	t.Cleanup(func() {
		if err := db.CloseDB(); err != nil {
			t.Errorf("CloseDB: %v", err)
		}
	})
}

func mustWSUser(t *testing.T, username string) int {
	t.Helper()
	id, err := db.CreateUser(username, "hash", username, "approved", false)
	if err != nil {
		t.Fatalf("CreateUser(%s): %v", username, err)
	}
	return id
}

// readWSMessage polls the socket until an event of the wanted type arrives
// (init/presence chatter is skipped) or the deadline passes.
func readWSMessage(t *testing.T, conn *websocket.Conn, wantType string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		_, raw, err := conn.ReadMessage()
		if err != nil {
			continue
		}
		var ev map[string]any
		if json.Unmarshal(raw, &ev) != nil {
			continue
		}
		if ev["type"] == wantType {
			return ev
		}
	}
	t.Fatalf("no %q event received", wantType)
	return nil
}

// Regression test for the review finding: the authenticated WS handler used
// to accept any attachment URL under /uploads/<uid>/, including "../.."
// escapes that let the Matrix bridge read files outside data/uploads (the
// reviewer uploaded a planted marker file through this path). Traversal
// attachments must be stripped; a message made of nothing else must not post.
func TestWSAttachmentTraversalRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	initWSTestDB(t)
	alice := mustWSUser(t, "alice")
	carol := mustWSUser(t, "carol")
	channel := db.DMChannelID(alice, carol)
	token, _, err := auth.IssueSession(alice, "user")
	if err != nil {
		t.Fatalf("IssueSession: %v", err)
	}

	// The file the attack targets: inside data/, outside data/uploads.
	marker := filepath.Join(db.DataDir, "review-marker.txt")
	if err := os.WriteFile(marker, []byte("must-never-reach-matrix"), 0o644); err != nil {
		t.Fatal(err)
	}
	uploadDir, err := db.UserUploadDir(alice)
	if err != nil {
		t.Fatal(err)
	}

	r := gin.New()
	registerWS(r)
	srv := httptest.NewServer(r)
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws?token=" + token
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	readWSMessage(t, conn, "init")

	// A real uploaded file in the sender's dir, used for the control case.
	if err := os.WriteFile(filepath.Join(uploadDir, "ok.txt"), []byte("fine"), 0o644); err != nil {
		t.Fatal(err)
	}

	send := func(text string, atts []map[string]any) {
		t.Helper()
		payload := map[string]any{
			"type": "message", "channel": channel, "text": text,
			"attachments": atts, "client_id": "t",
		}
		if err := conn.WriteJSON(payload); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	// Message whose only attachment is a traversal escape: stripped → posted
	// as plain text, marker file never referenced.
	send("hi", []map[string]any{{
		"url":  fmt.Sprintf("/uploads/%d/../../review-marker.txt", alice),
		"name": "review-marker.txt", "mime": "text/plain", "size": 38,
	}})
	ev := readWSMessage(t, conn, "message")
	msg, _ := ev["message"].(map[string]any)
	if atts, _ := msg["attachments"].([]any); len(atts) != 0 {
		t.Fatalf("traversal attachment survived filtering: %v", atts)
	}

	// A message carrying ONLY a traversal attachment must not post at all.
	send("", []map[string]any{{
		"url":  fmt.Sprintf("/uploads/%d/../../review-marker.txt", alice),
		"name": "review-marker.txt", "mime": "text/plain", "size": 38,
	}})
	// Control: a legit attachment still posts.
	send("", []map[string]any{{
		"url":  fmt.Sprintf("/uploads/%d/ok.txt", alice),
		"name": "ok.txt", "mime": "text/plain", "size": 4,
	}})
	ev = readWSMessage(t, conn, "message")
	msg, _ = ev["message"].(map[string]any)
	if atts, _ := msg["attachments"].([]any); len(atts) != 1 {
		t.Fatalf("legit attachment dropped: %v", atts)
	}
	// Give the (silently dropped) traversal-only message a moment, then count:
	// exactly 2 messages — the text one and the legit-attachment one.
	time.Sleep(300 * time.Millisecond)
	msgs, err := db.FetchChannelWindow(channel, 50, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2 (traversal-only message must not post)", len(msgs))
	}
	for _, m := range msgs {
		for _, a := range m.Attachments {
			if strings.Contains(a.URL, "..") || !strings.HasPrefix(a.URL, fmt.Sprintf("/uploads/%d/", alice)) {
				t.Fatalf("bad attachment persisted: %+v", a)
			}
		}
	}
}
