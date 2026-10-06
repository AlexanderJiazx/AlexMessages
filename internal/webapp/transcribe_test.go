package webapp

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/auth"
	"alexmessage/internal/db"
)

func TestAudioFormatFor(t *testing.T) {
	cases := []struct {
		name, ct, want string
	}{
		{"clip.wav", "audio/wav", "wav"},
		{"clip.mp3", "audio/mpeg", "mp3"},
		{"clip.m4a", "audio/mp4", "m4a"},
		{"clip.webm", "audio/webm", "webm"},
		{"clip.ogg", "audio/ogg", "ogg"},
		{"clip.flac", "", "flac"},
		{"clip.bin", "application/octet-stream", ""},
		{"noext", "", ""},
	}
	for _, tc := range cases {
		if got := audioFormatFor(tc.name, tc.ct); got != tc.want {
			t.Errorf("audioFormatFor(%q,%q) = %q, want %q", tc.name, tc.ct, got, tc.want)
		}
	}
}

func TestTranscribeModel(t *testing.T) {
	t.Setenv("OPENROUTER_TRANSCRIBE_MODEL", "")
	if m := transcribeModel("wav"); m != "mistralai/voxtral-small-24b-2507" {
		t.Errorf("wav should route to voxtral, got %s", m)
	}
	if m := transcribeModel("m4a"); m != "google/gemini-3.1-flash-lite" {
		t.Errorf("m4a should route to gemini fallback, got %s", m)
	}
	t.Setenv("OPENROUTER_TRANSCRIBE_MODEL", "x/y")
	if m := transcribeModel("wav"); m != "x/y" {
		t.Errorf("env override ignored, got %s", m)
	}
}

func TestHandleTranscribe(t *testing.T) {
	gin.SetMode(gin.TestMode)
	initTranscribeTestDB(t)
	testUser := createTranscribeTestUser(t)
	token := sessionFor(t, testUser.ID)

	var gotFormat, gotModel, gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var req orRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotModel = req.Model
		for _, p := range req.Messages[0].Content {
			if p.InputAudio != nil {
				gotFormat = p.InputAudio.Format
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hello world"}}]}`))
	}))
	defer upstream.Close()

	// openRouterURL appends /chat/completions — give it a server that accepts any path.
	t.Setenv("OPENROUTER_BASE_URL", upstream.URL)
	t.Setenv("OPENROUTER_API_KEY", "test-key-123")

	r := gin.New()
	registerTranscribeRoutes(r)

	call := func(ct, filename string, body []byte) *httptest.ResponseRecorder {
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		h := make(map[string][]string)
		h["Content-Disposition"] = []string{`form-data; name="file"; filename="` + filename + `"`}
		h["Content-Type"] = []string{ct}
		part, _ := w.CreatePart(h)
		_, _ = part.Write(body)
		_ = w.Close()
		req := httptest.NewRequest(http.MethodPost, "/api/transcribe", &buf)
		req.Header.Set("Content-Type", w.FormDataContentType())
		req.AddCookie(&http.Cookie{Name: auth.UserCookie, Value: token})
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	// Unauthenticated request is rejected.
	req := httptest.NewRequest(http.MethodPost, "/api/transcribe", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: got %d, want 401", rec.Code)
	}

	// Happy path: WAV → voxtral → transcript returned.
	rec = call("audio/wav", "clip.wav", []byte("RIFF....WAVEfmt fake-pcm"))
	if rec.Code != http.StatusOK {
		t.Fatalf("wav: got %d body %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Text != "hello world" {
		t.Fatalf("wav: bad body %s err %v", rec.Body.String(), err)
	}
	if gotFormat != "wav" || gotModel != "mistralai/voxtral-small-24b-2507" {
		t.Fatalf("upstream got format=%s model=%s", gotFormat, gotModel)
	}
	if gotAuth != "Bearer test-key-123" {
		t.Fatalf("api key not forwarded correctly: %q", gotAuth)
	}
	// m4a routes to the Gemini fallback model.
	rec = call("audio/mp4", "clip.m4a", []byte("....ftypM4A"))
	if rec.Code != http.StatusOK {
		t.Fatalf("m4a: got %d body %s", rec.Code, rec.Body.String())
	}
	if gotModel != "google/gemini-3.1-flash-lite" {
		t.Fatalf("m4a routed to %s, want gemini fallback", gotModel)
	}

	// Unsupported format rejected before any upstream call.
	rec = call("application/octet-stream", "clip.bin", []byte{0, 1, 2})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bin: got %d, want 400", rec.Code)
	}

	// Empty body rejected.
	rec = call("audio/wav", "clip.wav", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty: got %d, want 400", rec.Code)
	}
}

// initTranscribeTestDB points db at a throwaway dir (mirrors db_test.go).
func initTranscribeTestDB(t *testing.T) {
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

// createTranscribeTestUser inserts an approved user; sessionFor mints a session.
func createTranscribeTestUser(t *testing.T) *db.User {
	t.Helper()
	id, err := db.CreateUser("transcribe_user", "hash", "Transcribe User", "approved", false)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	u, _ := db.GetUserByID(id)
	return u
}

func sessionFor(t *testing.T, userID int) string {
	t.Helper()
	tok, _, err := auth.IssueSession(userID, "user")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return tok
}

func TestTranscribeMissingKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	initTranscribeTestDB(t)
	testUser := createTranscribeTestUser(t)
	token := sessionFor(t, testUser.ID)
	t.Setenv("OPENROUTER_API_KEY", "")

	r := gin.New()
	registerTranscribeRoutes(r)

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, _ := w.CreateFormFile("file", "clip.wav")
	_, _ = part.Write([]byte("RIFF"))
	_ = w.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/transcribe", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: auth.UserCookie, Value: token})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no key: got %d, want 503", rec.Code)
	}
}

