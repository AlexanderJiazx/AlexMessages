package matrix

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/auth"
	"alexmessage/internal/db"
	"alexmessage/internal/dmpost"
)

// ---------- fake homeserver ----------

type sendCall struct {
	RoomID    string
	EventType string
	TxnID     string
	AsUser    string
	Content   map[string]any
}

type fakeHS struct {
	*httptest.Server
	mu sync.Mutex

	registers  []string
	createArgs []string // invite mxids
	rooms      int
	joins      []string
	leaves     []string
	sends      []sendCall
	receipts   []string // event ids
	uploads    []string // uploaded filenames
	uploadHits []string // upload endpoint paths hit
	dlHits     []string // download endpoint paths hit
	profiles   map[string]map[string]string
	media      map[string][]byte
	mediaMime  map[string]string
	members    map[string]map[string]string // roomID -> mxid -> membership

	failSends   int  // return 500 for the first N sends (retry test)
	failRegs    int  // return 500 for the first N registers
	failUploads int  // return 500 for the first N media uploads
	noV1Media   bool // make /_matrix/client/v1/media/* 404 (fallback test)
}

// setMember seeds a room's membership state (as if the user had joined/been
// invited before the test starts).
func (f *fakeHS) setMember(roomID, mxid, membership string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.members[roomID] == nil {
		f.members[roomID] = map[string]string{}
	}
	f.members[roomID][mxid] = membership
}

func newFakeHS() *fakeHS {
	f := &fakeHS{
		profiles:  map[string]map[string]string{},
		media:     map[string][]byte{},
		mediaMime: map[string]string{},
		members:   map[string]map[string]string{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /_matrix/client/v3/register", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Username string `json:"username"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.failRegs > 0 {
			f.failRegs--
			w.WriteHeader(500)
			return
		}
		f.registers = append(f.registers, body.Username)
		fmt.Fprint(w, `{"user_id":"@`+body.Username+`:hs.local"}`)
	})
	mux.HandleFunc("POST /_matrix/client/v3/createRoom", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Invite []string `json:"invite"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.rooms++
		f.createArgs = append(f.createArgs, strings.Join(body.Invite, ","))
		rid := fmt.Sprintf("!room%d:hs.local", f.rooms)
		f.members[rid] = map[string]string{r.URL.Query().Get("user_id"): "join"}
		for _, mxid := range body.Invite {
			f.members[rid][mxid] = "invite"
		}
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]string{"room_id": rid})
	})
	mux.HandleFunc("GET /_matrix/client/v3/profile/", func(w http.ResponseWriter, r *http.Request) {
		mxid, _ := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/_matrix/client/v3/profile/"))
		f.mu.Lock()
		p := f.profiles[mxid]
		f.mu.Unlock()
		if p == nil {
			w.WriteHeader(404)
			fmt.Fprint(w, `{"errcode":"M_NOT_FOUND"}`)
			return
		}
		json.NewEncoder(w).Encode(p)
	})
	uploadHandler := func(path string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.noV1Media && (strings.HasPrefix(path, "/_matrix/client/v1/") || strings.HasPrefix(path, "/_matrix/media/v1/")) {
				w.WriteHeader(404)
				fmt.Fprint(w, `{"errcode":"M_UNRECOGNIZED"}`)
				return
			}
			f.uploadHits = append(f.uploadHits, path)
			if f.failUploads > 0 {
				f.failUploads--
				w.WriteHeader(500)
				return
			}
			f.uploads = append(f.uploads, r.URL.Query().Get("filename"))
			n := len(f.media)
			uri := fmt.Sprintf("mxc://hs.local/media%d", n)
			data, _ := io.ReadAll(r.Body)
			f.media[uri] = data
			f.mediaMime[uri] = r.Header.Get("Content-Type")
			json.NewEncoder(w).Encode(map[string]string{"content_uri": uri})
		}
	}
	mux.HandleFunc("POST /_matrix/media/v1/upload", uploadHandler("/_matrix/media/v1/upload"))
	mux.HandleFunc("POST /_matrix/media/v3/upload", uploadHandler("/_matrix/media/v3/upload"))
	downloadHandler := func(prefix string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, prefix), "/")
			mxc := "mxc://" + parts[0] + "/" + parts[1]
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.noV1Media && strings.HasPrefix(prefix, "/_matrix/client/v1/") {
				w.WriteHeader(404)
				fmt.Fprint(w, `{"errcode":"M_UNRECOGNIZED"}`)
				return
			}
			f.dlHits = append(f.dlHits, prefix)
			data, ok := f.media[mxc]
			mime := f.mediaMime[mxc]
			if !ok {
				w.WriteHeader(404)
				return
			}
			w.Header().Set("Content-Type", mime)
			_, _ = w.Write(data)
		}
	}
	mux.HandleFunc("GET /_matrix/client/v1/media/download/", downloadHandler("/_matrix/client/v1/media/download/"))
	mux.HandleFunc("GET /_matrix/media/v3/download/", downloadHandler("/_matrix/media/v3/download/"))
	mux.HandleFunc("/_matrix/client/v3/rooms/", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/_matrix/client/v3/rooms/")
		segs := strings.SplitN(rest, "/", 4)
		roomID, _ := url.PathUnescape(segs[0])
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case len(segs) >= 2 && segs[1] == "join" && r.Method == http.MethodPost:
			who := r.URL.Query().Get("user_id")
			f.joins = append(f.joins, roomID+" as "+who)
			if f.members[roomID] == nil {
				f.members[roomID] = map[string]string{}
			}
			f.members[roomID][who] = "join"
			json.NewEncoder(w).Encode(map[string]string{"room_id": roomID})
		case len(segs) >= 2 && segs[1] == "leave" && r.Method == http.MethodPost:
			who := r.URL.Query().Get("user_id")
			f.leaves = append(f.leaves, roomID+" as "+who)
			if f.members[roomID] != nil {
				f.members[roomID][who] = "leave"
			}
			fmt.Fprint(w, `{}`)
		case len(segs) >= 2 && segs[1] == "members" && r.Method == http.MethodGet:
			chunk := []map[string]any{}
			for mxid, membership := range f.members[roomID] {
				chunk = append(chunk, map[string]any{
					"type":      "m.room.member",
					"room_id":   roomID,
					"sender":    mxid,
					"state_key": mxid,
					"content":   map[string]any{"membership": membership},
				})
			}
			json.NewEncoder(w).Encode(map[string]any{"chunk": chunk})
		case len(segs) >= 3 && segs[1] == "send":
			if f.failSends > 0 {
				f.failSends--
				w.WriteHeader(500)
				return
			}
			var content map[string]any
			_ = json.NewDecoder(r.Body).Decode(&content)
			f.sends = append(f.sends, sendCall{
				RoomID:    roomID,
				EventType: segs[2],
				TxnID:     segs[3],
				AsUser:    r.URL.Query().Get("user_id"),
				Content:   content,
			})
			json.NewEncoder(w).Encode(map[string]string{
				"event_id": fmt.Sprintf("$evt%d", len(f.sends)),
			})
		case len(segs) >= 4 && segs[1] == "receipt" && segs[2] == "m.read":
			evID, _ := url.PathUnescape(segs[3])
			f.receipts = append(f.receipts, roomID+" "+evID)
			fmt.Fprint(w, `{}`)
		default:
			w.WriteHeader(404)
		}
	})
	f.Server = httptest.NewServer(mux)
	return f
}

func (f *fakeHS) lastSend() sendCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sends[len(f.sends)-1]
}

// ---------- test plumbing ----------

func testConfig(hs *fakeHS) Config {
	return Config{
		HomeserverURL: hs.URL,
		ServerName:    "hs.local",
		ASToken:       "as_tok",
		HSToken:       "hs_tok",
		UserPrefix:    "am_",
		BotLocalpart:  "alexmessages",
	}
}

// newTestBridge runs ops inline (no worker goroutine) with instant retries.
func newTestBridge(hs *fakeHS) *Bridge {
	b := newBridge(testConfig(hs))
	b.queue = nil
	b.retryBase = time.Millisecond
	return b
}

func initDB(t *testing.T) {
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
}

func mustLocalUser(t *testing.T, username string) int {
	t.Helper()
	id, err := db.CreateUser(username, "hash", username, "approved", false)
	if err != nil {
		t.Fatalf("CreateUser(%s): %v", username, err)
	}
	return id
}

func asRouter(b *Bridge) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	b.RegisterRoutes(r)
	return r
}

func putTxn(t *testing.T, r *gin.Engine, b *Bridge, txnID string, body map[string]any, token string) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("PUT", "/_matrix/app/v1/transactions/"+txnID, bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// ---------- config / MXID ----------

func TestParseMXID(t *testing.T) {
	cases := []struct {
		in      string
		lp, srv string
		ok      bool
	}{
		{"@alice:matrix.org", "alice", "matrix.org", true},
		{"@am_bob:hs.local", "am_bob", "hs.local", true},
		{"@a:b:8448", "a", "b:8448", true},
		{"alice:matrix.org", "", "", false},
		{"@:server", "", "", false},
		{"@local:", "", "", false},
		{"@", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		lp, srv, ok := ParseMXID(c.in)
		if ok != c.ok || lp != c.lp || srv != c.srv {
			t.Errorf("ParseMXID(%q) = %q,%q,%v want %q,%q,%v", c.in, lp, srv, ok, c.lp, c.srv, c.ok)
		}
	}
}

func TestConfigIdentity(t *testing.T) {
	cfg := Config{ServerName: "hs.local", UserPrefix: "am_", BotLocalpart: "alexmessages"}
	if got := cfg.PuppetMXID("bob"); got != "@am_bob:hs.local" {
		t.Fatalf("PuppetMXID = %q", got)
	}
	name, ok := cfg.UsernameForPuppet("@am_bob:hs.local")
	if !ok || name != "bob" {
		t.Fatalf("UsernameForPuppet = %q,%v", name, ok)
	}
	if _, ok := cfg.UsernameForPuppet("@alice:hs.local"); ok {
		t.Fatal("claimed a non-puppet localpart")
	}
	if _, ok := cfg.UsernameForPuppet("@am_bob:other.org"); ok {
		t.Fatal("claimed a puppet on another server")
	}
	if !cfg.OwnMXID("@am_bob:hs.local") || !cfg.OwnMXID("@alexmessages:hs.local") {
		t.Fatal("OwnMXID failed for puppet/bot")
	}
	if cfg.OwnMXID("@alice:hs.local") {
		t.Fatal("OwnMXID claimed remote user")
	}
}

func TestLoadConfigDisabled(t *testing.T) {
	for _, k := range []string{"MATRIX_HOMESERVER_URL", "MATRIX_SERVER_NAME", "MATRIX_AS_TOKEN", "MATRIX_HS_TOKEN"} {
		t.Setenv(k, "")
	}
	cfg, err := LoadConfig()
	if err != nil || cfg != nil {
		t.Fatalf("LoadConfig = %v,%v want nil,nil", cfg, err)
	}
	t.Setenv("MATRIX_HOMESERVER_URL", "http://hs")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("partial config should error")
	}
}

// ---------- AS auth / transactions ----------

func TestHSTokenRejected(t *testing.T) {
	initDB(t)
	hs := newFakeHS()
	defer hs.Close()
	b := newTestBridge(hs)
	r := asRouter(b)

	for _, token := range []string{"", "wrong"} {
		w := putTxn(t, r, b, "t1", map[string]any{"events": []any{}}, token)
		if w.Code != 403 {
			t.Fatalf("token %q: got %d want 403", token, w.Code)
		}
	}
	if w := putTxn(t, r, b, "t1", map[string]any{"events": []any{}}, "hs_tok"); w.Code != 200 {
		t.Fatalf("valid token: got %d", w.Code)
	}
}

func TestTransactionIdempotent(t *testing.T) {
	initDB(t)
	hs := newFakeHS()
	defer hs.Close()
	b := newTestBridge(hs)
	r := asRouter(b)

	localID := mustLocalUser(t, "bob")
	setupRoom(t, b, localID, "@alice:hs.local", "!r1:hs.local")

	body := map[string]any{"events": []any{
		map[string]any{
			"type": "m.room.message", "sender": "@alice:hs.local",
			"room_id": "!r1:hs.local", "event_id": "$e1",
			"origin_server_ts": 1700000000000,
			"content":          map[string]any{"msgtype": "m.text", "body": "hi bob"},
		},
	}}
	if w := putTxn(t, r, b, "txn1", body, "hs_tok"); w.Code != 200 {
		t.Fatalf("txn1 %d", w.Code)
	}
	if w := putTxn(t, r, b, "txn1", body, "hs_tok"); w.Code != 200 {
		t.Fatalf("redelivery %d", w.Code)
	}
	// Same event id in a new txn also dedupes via matrix_event_id.
	if w := putTxn(t, r, b, "txn2", body, "hs_tok"); w.Code != 200 {
		t.Fatalf("txn2 %d", w.Code)
	}
	msgs, _ := db.FetchChannelWindow(channelFor(t, localID, "@alice:hs.local"), 50, nil, nil)
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	if msgs[0].Text != "hi bob" {
		t.Fatalf("text = %q", msgs[0].Text)
	}
}

// setupRoom registers a remote user and room mapping the way an invite does.
func setupRoom(t *testing.T, b *Bridge, localID int, mxid, roomID string) {
	t.Helper()
	remote, err := b.ensureRemoteUser(mxid)
	if err != nil {
		t.Fatalf("ensureRemoteUser: %v", err)
	}
	if err := db.SetMatrixRoom(db.DMChannelID(localID, remote.ID), roomID); err != nil {
		t.Fatalf("SetMatrixRoom: %v", err)
	}
}

func channelFor(t *testing.T, localID int, mxid string) string {
	t.Helper()
	u, err := db.GetUserByMatrixID(mxid)
	if err != nil || u == nil {
		t.Fatalf("no remote user %s", mxid)
	}
	return db.DMChannelID(localID, u.ID)
}

// ---------- inbound ----------

func TestInboundText(t *testing.T) {
	initDB(t)
	hs := newFakeHS()
	defer hs.Close()
	b := newTestBridge(hs)
	r := asRouter(b)
	localID := mustLocalUser(t, "bob")
	setupRoom(t, b, localID, "@alice:hs.local", "!r1:hs.local")

	putTxn(t, r, b, "txn1", map[string]any{"events": []any{
		map[string]any{
			"type": "m.room.message", "sender": "@alice:hs.local",
			"room_id": "!r1:hs.local", "event_id": "$e1",
			"origin_server_ts": 1700000000000,
			"content":          map[string]any{"msgtype": "m.text", "body": "hello from matrix"},
		},
	}}, "hs_tok")

	ch := channelFor(t, localID, "@alice:hs.local")
	msgs, _ := db.FetchChannelWindow(ch, 50, nil, nil)
	if len(msgs) != 1 || msgs[0].Text != "hello from matrix" {
		t.Fatalf("msgs = %+v", msgs)
	}
	if ev, _ := db.MatrixEventIDForMessage(msgs[0].ID); ev != "$e1" {
		t.Fatalf("matrix_event_id = %q", ev)
	}
	if msgs[0].CreatedAt != 1700000000 {
		t.Fatalf("created_at = %d want origin_server_ts/1000", msgs[0].CreatedAt)
	}
}

func TestInboundIgnoresOwnPuppet(t *testing.T) {
	initDB(t)
	hs := newFakeHS()
	defer hs.Close()
	b := newTestBridge(hs)
	r := asRouter(b)
	localID := mustLocalUser(t, "bob")
	setupRoom(t, b, localID, "@alice:hs.local", "!r1:hs.local")

	putTxn(t, r, b, "txn1", map[string]any{"events": []any{
		map[string]any{
			"type": "m.room.message", "sender": "@am_bob:hs.local",
			"room_id": "!r1:hs.local", "event_id": "$echo",
			"content": map[string]any{"msgtype": "m.text", "body": "echo"},
		},
	}}, "hs_tok")
	msgs, _ := db.FetchChannelWindow(channelFor(t, localID, "@alice:hs.local"), 50, nil, nil)
	if len(msgs) != 0 {
		t.Fatalf("puppet echo inserted %d messages", len(msgs))
	}
}

func TestInboundEdit(t *testing.T) {
	initDB(t)
	hs := newFakeHS()
	defer hs.Close()
	b := newTestBridge(hs)
	r := asRouter(b)
	localID := mustLocalUser(t, "bob")
	setupRoom(t, b, localID, "@alice:hs.local", "!r1:hs.local")

	putTxn(t, r, b, "txn1", map[string]any{"events": []any{
		map[string]any{
			"type": "m.room.message", "sender": "@alice:hs.local",
			"room_id": "!r1:hs.local", "event_id": "$orig",
			"content": map[string]any{"msgtype": "m.text", "body": "before"},
		},
	}}, "hs_tok")
	putTxn(t, r, b, "txn2", map[string]any{"events": []any{
		map[string]any{
			"type": "m.room.message", "sender": "@alice:hs.local",
			"room_id": "!r1:hs.local", "event_id": "$edit",
			"content": map[string]any{
				"msgtype": "m.text", "body": "* after",
				"m.new_content": map[string]any{"msgtype": "m.text", "body": "after"},
				"m.relates_to":  map[string]any{"rel_type": "m.replace", "event_id": "$orig"},
			},
		},
	}}, "hs_tok")

	msgs, _ := db.FetchChannelWindow(channelFor(t, localID, "@alice:hs.local"), 50, nil, nil)
	if len(msgs) != 1 || msgs[0].Text != "after" {
		t.Fatalf("msgs = %+v", msgs)
	}
	if msgs[0].EditedAt == nil {
		t.Fatal("edited_at not set")
	}
}

func TestInboundReceipt(t *testing.T) {
	initDB(t)
	hs := newFakeHS()
	defer hs.Close()
	b := newTestBridge(hs)
	r := asRouter(b)
	localID := mustLocalUser(t, "bob")
	setupRoom(t, b, localID, "@alice:hs.local", "!r1:hs.local")
	ch := channelFor(t, localID, "@alice:hs.local")

	putTxn(t, r, b, "txn1", map[string]any{"events": []any{
		map[string]any{
			"type": "m.room.message", "sender": "@alice:hs.local",
			"room_id": "!r1:hs.local", "event_id": "$m1",
			"origin_server_ts": 1700000000000,
			"content":          map[string]any{"msgtype": "m.text", "body": "m1"},
		},
		map[string]any{
			"type": "m.receipt", "sender": "@alice:hs.local", "room_id": "!r1:hs.local",
			"content": map[string]any{
				"$m1": map[string]any{"m.read": map[string]any{
					"@alice:hs.local": map[string]any{"ts": 1700000005000},
				}},
			},
		},
	}}, "hs_tok")

	st, err := db.GetDMState(remoteID(t, "@alice:hs.local"), ch)
	if err != nil || st.LastReadAt == 0 {
		t.Fatalf("last_read_at = %+v, %v", st, err)
	}
}

func remoteID(t *testing.T, mxid string) int {
	t.Helper()
	u, _ := db.GetUserByMatrixID(mxid)
	if u == nil {
		t.Fatalf("no remote user %s", mxid)
	}
	return u.ID
}

func TestInboundImage(t *testing.T) {
	initDB(t)
	hs := newFakeHS()
	defer hs.Close()
	b := newTestBridge(hs)
	r := asRouter(b)
	localID := mustLocalUser(t, "bob")
	setupRoom(t, b, localID, "@alice:hs.local", "!r1:hs.local")

	// 1x1 red PNG
	png := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0x0D,
		0x49, 0x48, 0x44, 0x52, 0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0,
		0x1F, 0x15, 0xC4, 0x89, 0, 0, 0, 0x0D, 0x49, 0x44, 0x41, 0x54,
		0x78, 0x9C, 0x62, 0xFA, 0xCF, 0xC0, 0xF0, 0x1F, 0, 5, 2, 1, 0x31,
		0x1C, 0x5D, 0x1D, 0, 0, 0, 0, 0x49, 0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82}
	hs.media["mxc://hs.local/pic1"] = png
	hs.mediaMime["mxc://hs.local/pic1"] = "image/png"

	putTxn(t, r, b, "txn1", map[string]any{"events": []any{
		map[string]any{
			"type": "m.room.message", "sender": "@alice:hs.local",
			"room_id": "!r1:hs.local", "event_id": "$img",
			"content": map[string]any{
				"msgtype": "m.image", "body": "pic.png", "url": "mxc://hs.local/pic1",
				"info": map[string]any{"mimetype": "image/png", "w": 1, "h": 1, "size": len(png)},
			},
		},
	}}, "hs_tok")

	msgs, _ := db.FetchChannelWindow(channelFor(t, localID, "@alice:hs.local"), 50, nil, nil)
	if len(msgs) != 1 || len(msgs[0].Attachments) != 1 {
		t.Fatalf("msgs = %+v", msgs)
	}
	att := msgs[0].Attachments[0]
	if att.Mime != "image/png" || att.Width != 1 || att.Height != 1 {
		t.Fatalf("att = %+v", att)
	}
	if !strings.HasPrefix(att.URL, "/uploads/") {
		t.Fatalf("url = %q", att.URL)
	}
	rel := strings.TrimPrefix(att.URL, "/uploads/")
	if _, err := os.Stat(filepath.Join(db.UploadRoot, rel)); err != nil {
		t.Fatalf("file not on disk: %v", err)
	}
}

func TestInboundVoiceFlag(t *testing.T) {
	initDB(t)
	hs := newFakeHS()
	defer hs.Close()
	b := newTestBridge(hs)
	r := asRouter(b)
	localID := mustLocalUser(t, "bob")
	setupRoom(t, b, localID, "@alice:hs.local", "!r1:hs.local")
	hs.media["mxc://hs.local/voice1"] = []byte("fake-ogg")
	hs.mediaMime["mxc://hs.local/voice1"] = "audio/ogg"

	putTxn(t, r, b, "txn1", map[string]any{"events": []any{
		map[string]any{
			"type": "m.room.message", "sender": "@alice:hs.local",
			"room_id": "!r1:hs.local", "event_id": "$v",
			"content": map[string]any{
				"msgtype": "m.audio", "body": "voice.ogg", "url": "mxc://hs.local/voice1",
				"org.matrix.msc3245.voice": map[string]any{},
				"org.matrix.msc1767.audio": map[string]any{},
				"info":                     map[string]any{"mimetype": "audio/ogg"},
			},
		},
	}}, "hs_tok")

	msgs, _ := db.FetchChannelWindow(channelFor(t, localID, "@alice:hs.local"), 50, nil, nil)
	if len(msgs) != 1 || len(msgs[0].Attachments) != 1 {
		t.Fatalf("msgs = %+v", msgs)
	}
	if name := msgs[0].Attachments[0].Name; name != "voice-message.ogg" {
		t.Fatalf("name = %q, want voice-message.ogg", name)
	}
}

func TestInviteAutoJoin(t *testing.T) {
	initDB(t)
	hs := newFakeHS()
	defer hs.Close()
	b := newTestBridge(hs)
	r := asRouter(b)
	mustLocalUser(t, "bob")
	// A genuine 1:1: only carol is in the room before the invite.
	hs.setMember("!inv1:hs.local", "@carol:hs.local", "join")

	putTxn(t, r, b, "txn1", map[string]any{"events": []any{
		map[string]any{
			"type": "m.room.member", "sender": "@carol:hs.local",
			"room_id": "!inv1:hs.local", "state_key": "@am_bob:hs.local",
			"event_id": "$inv",
			"content":  map[string]any{"membership": "invite", "displayname": "Carol"},
		},
	}}, "hs_tok")

	u, _ := db.GetUserByMatrixID("@carol:hs.local")
	if u == nil {
		t.Fatal("remote user not provisioned")
	}
	if u.DisplayName != "Carol" {
		t.Fatalf("displayname = %q", u.DisplayName)
	}
	if room, _ := db.MatrixRoomFor(db.DMChannelID(1, u.ID)); room != "!inv1:hs.local" {
		t.Fatalf("room mapping = %q", room)
	}
	if len(hs.joins) != 1 || !strings.Contains(hs.joins[0], "!inv1:hs.local") {
		t.Fatalf("joins = %v", hs.joins)
	}
	if !strings.Contains(hs.joins[0], "@am_bob:hs.local") {
		t.Fatalf("joined as wrong user: %v", hs.joins)
	}
}

// ---------- query endpoints ----------

func TestQueryUser(t *testing.T) {
	initDB(t)
	hs := newFakeHS()
	defer hs.Close()
	b := newTestBridge(hs)
	r := asRouter(b)
	mustLocalUser(t, "bob")

	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", "Bearer hs_tok")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	if w := get("/_matrix/app/v1/users/@am_bob:hs.local"); w.Code != 200 {
		t.Fatalf("puppet user: %d", w.Code)
	}
	if w := get("/_matrix/app/v1/users/@am_nobody:hs.local"); w.Code != 404 {
		t.Fatalf("unknown puppet: %d", w.Code)
	}
	if w := get("/_matrix/app/v1/users/@am_bob:other.org"); w.Code != 404 {
		t.Fatalf("other server: %d", w.Code)
	}
	if w := get("/_matrix/app/v1/users/@alexmessages:hs.local"); w.Code != 200 {
		t.Fatalf("bot: %d", w.Code)
	}
	if w := get("/_matrix/app/v1/rooms/%23x:hs.local"); w.Code != 404 {
		t.Fatalf("alias: %d", w.Code)
	}
}

// ---------- outbound ----------

func TestOutboundSend(t *testing.T) {
	initDB(t)
	hs := newFakeHS()
	defer hs.Close()
	b := newTestBridge(hs)
	localID := mustLocalUser(t, "bob")
	remoteID := remoteIDAfterSetup(t, b, localID, "@alice:hs.local")

	msg := dmpost.Post(&localID, db.DMChannelID(localID, remoteID), "hi alice", nil, nil, "message", 0)
	b.OnLocalMessage(localID, db.DMChannelID(localID, remoteID), msg)

	if len(hs.sends) != 1 {
		t.Fatalf("sends = %v", hs.sends)
	}
	s := hs.sends[0]
	if s.AsUser != "@am_bob:hs.local" {
		t.Fatalf("masquerade user_id = %q", s.AsUser)
	}
	if s.TxnID != "am_"+msg.ID+"_t" {
		t.Fatalf("txn = %q", s.TxnID)
	}
	if s.Content["body"] != "hi alice" || s.Content["msgtype"] != "m.text" {
		t.Fatalf("content = %+v", s.Content)
	}
	if len(hs.createArgs) != 1 || hs.createArgs[0] != "@alice:hs.local" {
		t.Fatalf("createRoom invites = %v", hs.createArgs)
	}
	if !contains(hs.registers, "am_bob") {
		t.Fatalf("registers = %v", hs.registers)
	}
	if ev, _ := db.MatrixEventIDForMessage(msg.ID); ev == "" {
		t.Fatal("matrix_event_id not stored")
	}
}

func remoteIDAfterSetup(t *testing.T, b *Bridge, localID int, mxid string) int {
	t.Helper()
	u, err := b.ensureRemoteUser(mxid)
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func TestOutboundVoiceAttachment(t *testing.T) {
	initDB(t)
	hs := newFakeHS()
	defer hs.Close()
	b := newTestBridge(hs)
	localID := mustLocalUser(t, "bob")
	remoteID := remoteIDAfterSetup(t, b, localID, "@alice:hs.local")

	dir, _ := db.UserUploadDir(localID)
	os.WriteFile(filepath.Join(dir, "abc123_voice-message.m4a"), []byte("audio-data"), 0o644)
	att := db.Attachment{
		Name: "voice-message.m4a",
		URL:  "/uploads/" + fmt.Sprint(localID) + "/abc123_voice-message.m4a",
		Mime: "audio/mp4", Size: 10,
	}
	msg := dmpost.Post(&localID, db.DMChannelID(localID, remoteID), "", nil, []db.Attachment{att}, "message", 0)
	b.OnLocalMessage(localID, db.DMChannelID(localID, remoteID), msg)

	if len(hs.sends) != 1 {
		t.Fatalf("sends = %v", hs.sends)
	}
	c := hs.sends[0].Content
	if c["msgtype"] != "m.audio" {
		t.Fatalf("msgtype = %v", c["msgtype"])
	}
	if _, ok := c["org.matrix.msc3245.voice"]; !ok {
		t.Fatalf("missing msc3245 flag: %+v", c)
	}
	if _, ok := c["org.matrix.msc1767.audio"]; !ok {
		t.Fatalf("missing msc1767 flag: %+v", c)
	}
	if !strings.HasPrefix(c["url"].(string), "mxc://hs.local/") {
		t.Fatalf("url = %v", c["url"])
	}
	if len(hs.uploads) != 1 || hs.uploads[0] != "voice-message.m4a" {
		t.Fatalf("uploads = %v", hs.uploads)
	}
}

func TestOutboundEditAndReceipt(t *testing.T) {
	initDB(t)
	hs := newFakeHS()
	defer hs.Close()
	b := newTestBridge(hs)
	localID := mustLocalUser(t, "bob")
	remoteID := remoteIDAfterSetup(t, b, localID, "@alice:hs.local")
	ch := db.DMChannelID(localID, remoteID)

	msg := dmpost.Post(&localID, ch, "draft", nil, nil, "message", 0)
	b.OnLocalMessage(localID, ch, msg)
	b.OnLocalEdit(localID, msg.ID, ch, "final")

	if len(hs.sends) != 2 {
		t.Fatalf("sends = %v", hs.sends)
	}
	edit := hs.sends[1]
	rel, _ := edit.Content["m.relates_to"].(map[string]any)
	if rel["rel_type"] != "m.replace" {
		t.Fatalf("edit content = %+v", edit.Content)
	}
	nc, _ := edit.Content["m.new_content"].(map[string]any)
	if nc["body"] != "final" {
		t.Fatalf("new_content = %+v", nc)
	}

	b.OnLocalRead(localID, ch)
	if len(hs.receipts) != 1 {
		t.Fatalf("receipts = %v", hs.receipts)
	}
}

func TestOutboundRetryOn5xx(t *testing.T) {
	initDB(t)
	hs := newFakeHS()
	defer hs.Close()
	b := newTestBridge(hs)
	localID := mustLocalUser(t, "bob")
	remoteID := remoteIDAfterSetup(t, b, localID, "@alice:hs.local")
	ch := db.DMChannelID(localID, remoteID)
	// Room must exist before the send or ensureRoom runs (also a send path) —
	// let it create the room first, then fail the message send once.
	msg := dmpost.Post(&localID, ch, "retry me", nil, nil, "message", 0)
	hs.failSends = 1
	b.OnLocalMessage(localID, ch, msg)
	// First send attempt failed once then retried: exactly one successful send.
	if len(hs.sends) != 1 {
		t.Fatalf("sends = %v", hs.sends)
	}
}

// ---------- lookup path ----------

func TestOpenDMByMXID(t *testing.T) {
	initDB(t)
	hs := newFakeHS()
	defer hs.Close()
	hs.profiles["@alice:hs.local"] = map[string]string{"displayname": "Alice Cooper"}
	b := newTestBridge(hs)
	localID := mustLocalUser(t, "bob")

	u, err := b.OpenDMByMXID(localID, "@alice:hs.local")
	if err != nil {
		t.Fatal(err)
	}
	if u.MatrixID == nil || *u.MatrixID != "@alice:hs.local" {
		t.Fatalf("matrix_id = %v", u.MatrixID)
	}
	if u.Username != "alice:hs.local" {
		t.Fatalf("username = %q", u.Username)
	}
	if len(hs.createArgs) != 1 {
		t.Fatalf("no room created: %v", hs.createArgs)
	}
	// Profile sync ran inline (test bridge) — display name resolved.
	fresh, _ := db.GetUserByMatrixID("@alice:hs.local")
	if fresh.DisplayName != "Alice Cooper" {
		t.Fatalf("displayname = %q", fresh.DisplayName)
	}
	// Room mapping recorded for the DM channel.
	if room, _ := db.MatrixRoomFor(db.DMChannelID(localID, u.ID)); room != "!room1:hs.local" {
		t.Fatalf("room = %q", room)
	}
}

func TestOpenDMRejectsOwnNamespace(t *testing.T) {
	initDB(t)
	hs := newFakeHS()
	defer hs.Close()
	b := newTestBridge(hs)
	mustLocalUser(t, "bob")
	if _, err := b.OpenDMByMXID(1, "@am_bob:hs.local"); err == nil {
		t.Fatal("expected rejection of our own puppet")
	}
	if _, err := b.OpenDMByMXID(1, "not-an-mxid"); err == nil {
		t.Fatal("expected rejection of non-mxid")
	}
}

func TestRemoteUserCannotLogin(t *testing.T) {
	initDB(t)
	hs := newFakeHS()
	defer hs.Close()
	b := newTestBridge(hs)
	u, err := b.ensureRemoteUser("@alice:hs.local")
	if err != nil {
		t.Fatal(err)
	}
	if auth.VerifyPassword("anything", u.PasswordHash) {
		t.Fatal("remote user authenticated")
	}
	// Status is 'approved' so the DM machinery works, but they must not appear
	// in the admin approval queue (which lists 'pending').
	pending, _ := db.ListUsers("pending")
	for _, p := range pending {
		if p.ID == u.ID {
			t.Fatal("remote user in pending queue")
		}
	}
}

func TestRegistrationYAML(t *testing.T) {
	cfg := &Config{ServerName: "example.org", UserPrefix: "am_", BotLocalpart: "alexmessages"}
	y := RegistrationYAML(cfg, "tok_as", "tok_hs", "https://am.example.org")
	for _, want := range []string{
		"id: alex-messages",
		"url: https://am.example.org",
		"as_token: tok_as",
		"hs_token: tok_hs",
		"sender_localpart: alexmessages",
		"exclusive: true",
		"de.sorunome.msc2409.push_ephemeral: true",
		"receive_ephemeral: true",
		"'@am_.*:example\\.org'",
	} {
		if !strings.Contains(y, want) {
			t.Fatalf("registration missing %q:\n%s", want, y)
		}
	}
}

// ---------- review-fix regression tests ----------

// The invite handler must refuse to map a room with more than the two DM
// participants — otherwise a group invite turns a private thread into a leak.
func TestGroupInviteDeclined(t *testing.T) {
	initDB(t)
	hs := newFakeHS()
	defer hs.Close()
	b := newTestBridge(hs)
	r := asRouter(b)
	mustLocalUser(t, "bob")
	// !g1 already contains carol AND a third participant.
	hs.setMember("!g1:hs.local", "@carol:hs.local", "join")
	hs.setMember("!g1:hs.local", "@mallory:hs.local", "join")

	w := putTxn(t, r, b, "txn1", map[string]any{"events": []any{
		map[string]any{
			"type": "m.room.member", "sender": "@carol:hs.local",
			"room_id": "!g1:hs.local", "state_key": "@am_bob:hs.local",
			"event_id": "$ginv",
			"content":  map[string]any{"membership": "invite"},
		},
	}}, "hs_tok")
	if w.Code != 200 {
		t.Fatalf("declined invite should still commit the txn, got %d", w.Code)
	}
	u, _ := db.GetUserByMatrixID("@carol:hs.local")
	if u == nil {
		t.Fatal("remote user not provisioned")
	}
	if room, _ := db.MatrixRoomFor(db.DMChannelID(1, u.ID)); room != "" {
		t.Fatalf("group room mapped to a DM: %q", room)
	}
	if len(hs.leaves) != 1 || !strings.Contains(hs.leaves[0], "!g1:hs.local") {
		t.Fatalf("puppet did not leave the group room: %v", hs.leaves)
	}
	if !strings.Contains(hs.leaves[0], "@am_bob:hs.local") {
		t.Fatalf("leave as wrong user: %v", hs.leaves)
	}
}

// A room where the only pending third presence is an *invited* user also
// fails — the invite could be accepted later.
func TestGroupInviteDeclinedOnPendingInvite(t *testing.T) {
	initDB(t)
	hs := newFakeHS()
	defer hs.Close()
	b := newTestBridge(hs)
	r := asRouter(b)
	mustLocalUser(t, "bob")
	hs.setMember("!g2:hs.local", "@carol:hs.local", "join")
	hs.setMember("!g2:hs.local", "@mallory:hs.local", "invite")

	putTxn(t, r, b, "txn1", map[string]any{"events": []any{
		map[string]any{
			"type": "m.room.member", "sender": "@carol:hs.local",
			"room_id": "!g2:hs.local", "state_key": "@am_bob:hs.local",
			"event_id": "$ginv2",
			"content":  map[string]any{"membership": "invite"},
		},
	}}, "hs_tok")

	u, _ := db.GetUserByMatrixID("@carol:hs.local")
	if room, _ := db.MatrixRoomFor(db.DMChannelID(1, u.ID)); room != "" {
		t.Fatalf("room with pending third invite mapped: %q", room)
	}
	if len(hs.leaves) != 1 {
		t.Fatalf("leaves = %v", hs.leaves)
	}
}

// A failed transaction must NOT be recorded: the HS retry redelivers it.
func TestTxnRetriedAfterProcessingFailure(t *testing.T) {
	initDB(t)
	hs := newFakeHS()
	defer hs.Close()
	b := newTestBridge(hs)
	r := asRouter(b)
	mustLocalUser(t, "bob")
	hs.setMember("!invr:hs.local", "@carol:hs.local", "join")
	hs.failRegs = 1 // first register (ensurePuppet) 500s → txn fails

	invite := map[string]any{"events": []any{
		map[string]any{
			"type": "m.room.member", "sender": "@carol:hs.local",
			"room_id": "!invr:hs.local", "state_key": "@am_bob:hs.local",
			"event_id": "$invr",
			"content":  map[string]any{"membership": "invite"},
		},
	}}
	if w := putTxn(t, r, b, "txnX", invite, "hs_tok"); w.Code != 500 {
		t.Fatalf("failed txn should 500 so the HS retries, got %d", w.Code)
	}
	if u, _ := db.GetUserByMatrixID("@carol:hs.local"); u != nil {
		t.Fatal("failed txn left remote user/room state behind")
	}
	if len(hs.joins) != 0 {
		t.Fatalf("join ran %d times on a failed txn", len(hs.joins))
	}
	if w := putTxn(t, r, b, "txnX", invite, "hs_tok"); w.Code != 200 {
		t.Fatalf("redelivery got %d", w.Code)
	}
	if w := putTxn(t, r, b, "txnX", invite, "hs_tok"); w.Code != 200 {
		t.Fatalf("second redelivery got %d", w.Code)
	}
	if len(hs.joins) != 1 {
		t.Fatalf("join ran %d times, want 1", len(hs.joins))
	}
}

// A homeserver restart resets its transaction counter, so a brand-new txn
// can arrive under an id we recorded before — it must still be processed.
// Conversely a genuine redelivery must not double-insert the message.
func TestTxnIDReuseAfterRestart(t *testing.T) {
	initDB(t)
	hs := newFakeHS()
	defer hs.Close()
	b := newTestBridge(hs)
	r := asRouter(b)
	localID := mustLocalUser(t, "bob")
	setupRoom(t, b, localID, "@alice:hs.local", "!r2:hs.local")
	ch := channelFor(t, localID, "@alice:hs.local")

	msg := func(eventID string) map[string]any {
		return map[string]any{"events": []any{
			map[string]any{
				"type": "m.room.message", "sender": "@alice:hs.local",
				"room_id": "!r2:hs.local", "event_id": eventID,
				"origin_server_ts": 1700000000000,
				"content":          map[string]any{"msgtype": "m.text", "body": "hi"},
			},
		}}
	}
	putTxn(t, r, b, "1", msg("$m1"), "hs_tok")
	putTxn(t, r, b, "1", msg("$m1"), "hs_tok")   // redelivery: deduped by event id
	putTxn(t, r, b, "1", msg("$m2"), "hs_tok")   // reused id, NEW event — must land
	putTxn(t, r, b, "999", msg("$m1"), "hs_tok") // same event, new txn id — deduped
	msgs, err := db.FetchChannelWindow(ch, 50, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2", len(msgs))
	}
}

// Receipts arrive in the transaction's `ephemeral` array (MSC2409), not in
// `events` — they must still apply.
func TestEphemeralReceipt(t *testing.T) {
	initDB(t)
	hs := newFakeHS()
	defer hs.Close()
	b := newTestBridge(hs)
	r := asRouter(b)
	localID := mustLocalUser(t, "bob")
	setupRoom(t, b, localID, "@alice:hs.local", "!r1:hs.local")
	ch := channelFor(t, localID, "@alice:hs.local")

	w := putTxn(t, r, b, "txn1", map[string]any{
		"events": []any{
			map[string]any{
				"type": "m.room.message", "sender": "@alice:hs.local",
				"room_id": "!r1:hs.local", "event_id": "$m1",
				"origin_server_ts": 1700000000000,
				"content":          map[string]any{"msgtype": "m.text", "body": "m1"},
			},
		},
		"ephemeral": []any{
			map[string]any{
				"type": "m.receipt", "room_id": "!r1:hs.local",
				"content": map[string]any{
					"$m1": map[string]any{"m.read": map[string]any{
						"@alice:hs.local": map[string]any{"ts": 1700000005000},
					}},
				},
			},
		},
	}, "hs_tok")
	if w.Code != 200 {
		t.Fatalf("ephemeral txn got %d", w.Code)
	}
	st, err := db.GetDMState(remoteID(t, "@alice:hs.local"), ch)
	if err != nil || st.LastReadAt == 0 {
		t.Fatalf("ephemeral receipt ignored: %+v, %v", st, err)
	}
}

// Each edit needs its own txn id — the HS dedupes on it, so a constant
// "<msg>_edit" silently dropped every edit after the first.
func TestEditTxnIDsUnique(t *testing.T) {
	initDB(t)
	hs := newFakeHS()
	defer hs.Close()
	b := newTestBridge(hs)
	localID := mustLocalUser(t, "bob")
	remoteID := remoteIDAfterSetup(t, b, localID, "@alice:hs.local")
	ch := db.DMChannelID(localID, remoteID)

	msg := dmpost.Post(&localID, ch, "v1", nil, nil, "message", 0)
	b.OnLocalMessage(localID, ch, msg)
	b.OnLocalEdit(localID, msg.ID, ch, "v2")
	b.OnLocalEdit(localID, msg.ID, ch, "v3")

	if len(hs.sends) != 3 {
		t.Fatalf("sends = %v", hs.sends)
	}
	t1, t2 := hs.sends[1].TxnID, hs.sends[2].TxnID
	if t1 == t2 {
		t.Fatalf("two edits reused txn id %q", t1)
	}
	for _, id := range []string{t1, t2} {
		if !strings.HasPrefix(id, "am_"+msg.ID+"_edit_") {
			t.Fatalf("edit txn id %q has wrong shape", id)
		}
	}
}

// Media downloads must hit the authenticated MSC3916 endpoint first; on a
// legacy-only HS the media/v3 fallback keeps working.
func TestMediaDownloadPrefersV1(t *testing.T) {
	initDB(t)
	hs := newFakeHS()
	defer hs.Close()
	b := newTestBridge(hs)
	r := asRouter(b)
	localID := mustLocalUser(t, "bob")
	setupRoom(t, b, localID, "@alice:hs.local", "!r1:hs.local")
	hs.media["mxc://hs.local/a1"] = []byte("data")
	hs.mediaMime["mxc://hs.local/a1"] = "text/plain"

	putTxn(t, r, b, "txn1", map[string]any{"events": []any{
		map[string]any{
			"type": "m.room.message", "sender": "@alice:hs.local",
			"room_id": "!r1:hs.local", "event_id": "$f1",
			"content": map[string]any{
				"msgtype": "m.file", "body": "a.txt", "url": "mxc://hs.local/a1",
				"info": map[string]any{"mimetype": "text/plain"},
			},
		},
	}}, "hs_tok")

	if len(hs.dlHits) == 0 || !strings.HasPrefix(hs.dlHits[0], "/_matrix/client/v1/media/") {
		t.Fatalf("download hit %v, want client/v1 first", hs.dlHits)
	}
	msgs, _ := db.FetchChannelWindow(channelFor(t, localID, "@alice:hs.local"), 50, nil, nil)
	if len(msgs) != 1 || len(msgs[0].Attachments) != 1 {
		t.Fatalf("msgs = %+v", msgs)
	}
}

func TestMediaDownloadLegacyFallback(t *testing.T) {
	initDB(t)
	hs := newFakeHS()
	defer hs.Close()
	hs.noV1Media = true
	b := newTestBridge(hs)
	r := asRouter(b)
	localID := mustLocalUser(t, "bob")
	setupRoom(t, b, localID, "@alice:hs.local", "!r1:hs.local")
	hs.media["mxc://hs.local/a2"] = []byte("data")
	hs.mediaMime["mxc://hs.local/a2"] = "text/plain"

	putTxn(t, r, b, "txn1", map[string]any{"events": []any{
		map[string]any{
			"type": "m.room.message", "sender": "@alice:hs.local",
			"room_id": "!r1:hs.local", "event_id": "$f2",
			"content": map[string]any{
				"msgtype": "m.file", "body": "a.txt", "url": "mxc://hs.local/a2",
				"info": map[string]any{"mimetype": "text/plain"},
			},
		},
	}}, "hs_tok")

	if !contains(hs.dlHits, "/_matrix/media/v3/download/") {
		t.Fatalf("legacy fallback never hit: %v", hs.dlHits)
	}
	msgs, _ := db.FetchChannelWindow(channelFor(t, localID, "@alice:hs.local"), 50, nil, nil)
	if len(msgs) != 1 || len(msgs[0].Attachments) != 1 {
		t.Fatalf("attachment lost despite working legacy path: %+v", msgs)
	}
}

// A transient upload failure must propagate so the op retries — not get
// swallowed while the message silently loses its file.
func TestOutboundAttachmentUploadRetry(t *testing.T) {
	initDB(t)
	hs := newFakeHS()
	defer hs.Close()
	b := newTestBridge(hs)
	localID := mustLocalUser(t, "bob")
	remoteID := remoteIDAfterSetup(t, b, localID, "@alice:hs.local")
	ch := db.DMChannelID(localID, remoteID)

	dir, _ := db.UserUploadDir(localID)
	os.WriteFile(filepath.Join(dir, "x_file.txt"), []byte("payload"), 0o644)
	att := db.Attachment{
		Name: "file.txt",
		URL:  "/uploads/" + fmt.Sprint(localID) + "/x_file.txt",
		Mime: "text/plain", Size: 7,
	}
	// Fail the first upload attempt; the retry must succeed — previously the
	// failure was logged and the attachment silently dropped.
	hs.failUploads = 1
	msg := dmpost.Post(&localID, ch, "", nil, []db.Attachment{att}, "message", 0)
	b.OnLocalMessage(localID, ch, msg)
	if len(hs.uploads) != 1 {
		t.Fatalf("uploads = %v — retry did not happen", hs.uploads)
	}
	if len(hs.sends) != 1 {
		t.Fatalf("sends = %v", hs.sends)
	}
}
