package webapp

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"alexmessage/internal/db"
	"alexmessage/internal/ota"
	"alexmessage/internal/ota/census"
	"alexmessage/internal/ota/otatest"
)

const (
	otaRuntime = "a1b2c3d4e5f6"
	// build2 is the X-AM-Client value of build 2 (OTAUpdatePlan.md, D8 item 2).
	build2 = "v=1;platform=android;version=1.2.0;build=2;runtime=a1b2c3d4e5f6;update=embedded;channel=production"
)

// otaEnv is a test database plus an update store at $OTA_DIR.
func otaEnv(t *testing.T) (*otatest.Fixture, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	initTranscribeTestDB(t)
	f := otatest.New(t)
	t.Setenv("OTA_DIR", f.Dir)
	t.Setenv("OTA_DISABLED", "")
	return f, NewEngine()
}

// manifestRequest is the update check of expo-updates 57 on a phone.
func manifestRequest(platform, current, embedded, client string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/ota/manifest", nil)
	req.Header.Set("Accept", "multipart/mixed,application/expo+json,application/json")
	req.Header.Set("Expo-Protocol-Version", "1")
	req.Header.Set("Expo-API-Version", "1")
	req.Header.Set("Expo-Platform", platform)
	req.Header.Set("Expo-Runtime-Version", otaRuntime)
	req.Header.Set("Expo-Channel-Name", "production")
	req.Header.Set("Expo-Expect-Signature", `sig, keyid="root-2026", alg="rsa-v1_5-sha256"`)
	if current != "" {
		req.Header.Set("Expo-Current-Update-ID", current)
	}
	if embedded != "" {
		req.Header.Set("Expo-Embedded-Update-ID", embedded)
	}
	if client != "" {
		req.Header.Set("EAS-Client-ID", client)
	}
	return req
}

func serve(r http.Handler, req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func expectNoUpdate(t *testing.T, w *httptest.ResponseRecorder, what string) {
	t.Helper()
	if w.Code != http.StatusNoContent || w.Body.Len() != 0 {
		t.Fatalf("%s: %d %q, want 204", what, w.Code, w.Body.String())
	}
	if w.Header().Get("expo-protocol-version") != "1" || w.Header().Get("expo-sfv-version") != "0" ||
		w.Header().Get("Cache-Control") != "private, max-age=0, no-store" {
		t.Fatalf("%s: headers %v", what, w.Header())
	}
}

// otaCensus is the census database of the fixture's store.
func otaCensus(t *testing.T, f *otatest.Fixture) *census.DB {
	t.Helper()
	cdb, err := census.Shared(filepath.Join(f.Dir, census.FileName), false)
	if err != nil || cdb == nil {
		t.Fatalf("census: %v", err)
	}
	return cdb
}

func TestOTAManifestEndpoint(t *testing.T) {
	f, r := otaEnv(t)
	embedded := otatest.NewUUID()
	client := otatest.NewUUID()

	expectNoUpdate(t, serve(r, manifestRequest(ota.PlatformAndroid, embedded, embedded, client)), "no pointer")

	u := f.AddUpdate(t, ota.PlatformAndroid, otaRuntime)
	f.RegisterRuntime(t, ota.PlatformAndroid, otaRuntime, embedded, 2)
	f.Point(t, ota.ChannelProduction, ota.PlatformAndroid, otaRuntime, ota.StateUpdate, u.ID)

	w := serve(r, manifestRequest(ota.PlatformAndroid, embedded, embedded, client))
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("expo-protocol-version") != "1" || w.Header().Get("Cache-Control") != "private, max-age=0, no-store" {
		t.Fatalf("update headers %v", w.Header())
	}
	for _, h := range []string{"expo-manifest-filters", "expo-server-defined-headers"} {
		if w.Header().Get(h) != "" {
			t.Fatalf("sent %s", h)
		}
	}
	raw := w.Body.String()
	// iOS reads the part header case-sensitively.
	if !strings.Contains(raw, "\r\nexpo-signature: "+u.ManifestSig+"\r\n") {
		t.Fatalf("no lowercase expo-signature part header in:\n%s", raw)
	}
	mediaType, params, err := mime.ParseMediaType(w.Header().Get("Content-Type"))
	if err != nil || mediaType != "multipart/mixed" {
		t.Fatalf("content type %q", w.Header().Get("Content-Type"))
	}
	parts := map[string][]byte{}
	sigs := map[string]string{}
	mr := multipart.NewReader(strings.NewReader(raw), params["boundary"])
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(p)
		parts[p.FormName()] = b
		sigs[p.FormName()] = p.Header.Get("Expo-Signature")
	}
	if len(parts) != 2 || !bytes.Equal(parts["manifest"], u.Manifest) || !bytes.Equal(parts["certificate_chain"], f.LeafPEM) {
		t.Fatalf("parts %q", parts)
	}
	if _, err := ota.VerifySignature(parts["manifest"], sigs["manifest"], parts["certificate_chain"], f.Root, time.Now()); err != nil {
		t.Fatalf("the phone would reject the manifest: %v", err)
	}

	// The census has the install, because its embedded id is registered.
	cdb := otaCensus(t, f)
	rows, _ := cdb.List()
	if len(rows) != 1 || rows[0].ClientID != client || rows[0].EmbeddedUpdateID != embedded {
		t.Fatalf("census rows %+v", rows)
	}
	// An install whose embedded id belongs to no registered binary is not.
	serve(r, manifestRequest(ota.PlatformAndroid, "", otatest.NewUUID(), otatest.NewUUID()))
	if rows, _ := cdb.List(); len(rows) != 1 {
		t.Fatalf("unregistered install recorded: %+v", rows)
	}

	expectNoUpdate(t, serve(r, manifestRequest(ota.PlatformAndroid, u.ID, embedded, client)), "already on the update")
	expectNoUpdate(t, serve(r, manifestRequest(ota.PlatformIOS, embedded, embedded, client)), "other platform")

	// A failed update reaches the debug console once.
	failed := manifestRequest(ota.PlatformAndroid, embedded, embedded, client)
	failed.Header.Set("Expo-Recent-Failed-Update-IDs", `"`+u.ID+`"`)
	serve(r, failed)
	serve(r, failed)
	events, err := db.QueryDebugEvents(db.DebugFilter{Search: "ota.client_failure"})
	if err != nil || len(events) != 1 || !strings.Contains(events[0].Context, u.ID) {
		t.Fatalf("client_failure events %+v, %v", events, err)
	}

	// The rollback directive, and 204 once the phone runs its embedded JS.
	f.Revoke(t, u.ID)
	expectNoUpdate(t, serve(r, manifestRequest(ota.PlatformAndroid, embedded, embedded, client)), "revoked update")
	f.Point(t, ota.ChannelProduction, ota.PlatformAndroid, otaRuntime, ota.StateRollback, u.ID)
	w = serve(r, manifestRequest(ota.PlatformAndroid, u.ID, embedded, client))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `name="directive"`) ||
		!strings.Contains(w.Body.String(), string(u.Rollback)) ||
		!strings.Contains(w.Body.String(), "\r\nexpo-signature: "+u.RollbackSig+"\r\n") {
		t.Fatalf("rollback: %d %s", w.Code, w.Body.String())
	}
	expectNoUpdate(t, serve(r, manifestRequest(ota.PlatformAndroid, embedded, embedded, client)), "rollback on embedded")

	// The kill switch answers 204 to everything.
	f.Point(t, ota.ChannelProduction, ota.PlatformAndroid, otaRuntime, ota.StateUpdate, f.AddUpdate(t, ota.PlatformAndroid, otaRuntime).ID)
	t.Setenv("OTA_DISABLED", "1")
	expectNoUpdate(t, serve(r, manifestRequest(ota.PlatformAndroid, embedded, embedded, client)), "OTA_DISABLED")
	t.Setenv("OTA_DISABLED", "")

	// A damaged pointer is a 500 that no cache keeps.
	f.Write(t, "channels/production/android/"+otaRuntime+".json", []byte("{"))
	w = serve(r, manifestRequest(ota.PlatformAndroid, embedded, embedded, client))
	if w.Code != http.StatusInternalServerError || w.Header().Get("Cache-Control") != "private, max-age=0, no-store" {
		t.Fatalf("damaged pointer: %d %v", w.Code, w.Header())
	}
}

func TestOTAManifestRejectsBadHeaders(t *testing.T) {
	_, r := otaEnv(t)
	for name, edit := range map[string]func(*http.Request){
		"protocol 0":   func(q *http.Request) { q.Header.Set("Expo-Protocol-Version", "0") },
		"no platform":  func(q *http.Request) { q.Header.Del("Expo-Platform") },
		"bad runtime":  func(q *http.Request) { q.Header.Set("Expo-Runtime-Version", "../../x") },
		"web platform": func(q *http.Request) { q.Header.Set("Expo-Platform", "web") },
	} {
		req := manifestRequest(ota.PlatformAndroid, "", "", "")
		edit(req)
		w := serve(r, req)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") ||
			w.Header().Get("expo-protocol-version") != "1" {
			t.Errorf("%s: %d %v", name, w.Code, w.Header())
		}
	}
	// Without an initialized store every valid request gets 204.
	t.Setenv("OTA_DIR", t.TempDir())
	expectNoUpdate(t, serve(r, manifestRequest(ota.PlatformIOS, "", "", "")), "no store")
}

func TestOTAAssets(t *testing.T) {
	f, r := otaEnv(t)
	u := f.AddUpdate(t, ota.PlatformAndroid, otaRuntime)

	w := serve(r, httptest.NewRequest(http.MethodGet, "/ota/assets/"+u.AssetName, nil))
	if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), u.Asset) {
		t.Fatalf("asset: %d", w.Code)
	}
	want := map[string]string{
		"Content-Type":            "application/javascript",
		"Cache-Control":           "public, max-age=31536000, immutable",
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": "sandbox",
		"Content-Disposition":     `attachment; filename="` + u.AssetName + `"`,
	}
	for k, v := range want {
		if got := w.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}

	head := serve(r, httptest.NewRequest(http.MethodHead, "/ota/assets/"+u.AssetName, nil))
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") != strconv.Itoa(len(u.Asset)) {
		t.Fatalf("HEAD: %d %v", head.Code, head.Header())
	}
	rangeReq := httptest.NewRequest(http.MethodGet, "/ota/assets/"+u.AssetName, nil)
	rangeReq.Header.Set("Range", "bytes=0-6")
	if w := serve(r, rangeReq); w.Code != http.StatusPartialContent || w.Body.String() != string(u.Asset[:7]) {
		t.Fatalf("Range: %d %q", w.Code, w.Body.String())
	}

	missing := strings.Repeat("A", 43) + ".png"
	if w := serve(r, httptest.NewRequest(http.MethodGet, "/ota/assets/"+missing, nil)); w.Code != http.StatusNotFound ||
		w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("missing asset: %d %v", w.Code, w.Header())
	}
	hash := strings.TrimSuffix(u.AssetName, ".js")
	for _, bad := range []string{
		"/ota/assets/../FORMAT",
		"/ota/assets/..%2FFORMAT",
		"/ota/assets/" + strings.Repeat("A", 42) + ".js",
		"/ota/assets/" + hash + ".JS",
		"/ota/assets/" + hash,
		"/ota/assets/",
	} {
		w := serve(r, httptest.NewRequest(http.MethodGet, bad, nil))
		if w.Code != http.StatusBadRequest || w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: %d %v", bad, w.Code, w.Header())
		}
	}
}

// androidPolicy is policy.json with an Android build (version 1.<build>.0)
// and no iPhone build.
func androidPolicy(build, minBuild int, hardGate bool, notes string) string {
	n, _ := json.Marshal(notes)
	return fmt.Sprintf(`{"android":{"min_build":%d,"hard_gate":%t,"latest":{"version":"1.%d.0","build":%d,`+
		`"url":"https://messages.alexanderjia.com/app/android/AlexMessages-1.%d.0-%d.apk","sha256":"%s","size":51234567,"notes":%s}},`+
		`"ios":{"min_build":0,"latest":null}}`, minBuild, hardGate, build, build, build, build, strings.Repeat("ab", 32), n)
}

func TestAppPageAndDownloads(t *testing.T) {
	f, r := otaEnv(t)

	w := serve(r, httptest.NewRequest(http.MethodGet, "/app", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "No Android build is published yet.") {
		t.Fatalf("/app without builds: %d", w.Code)
	}

	f.Write(t, "native/policy.json", []byte(androidPolicy(2, 0, false, `<script>alert(1)</script>`)))
	apk := []byte("PK\x03\x04 not really an apk")
	f.Write(t, "native/android/AlexMessages-1.2.0-2.apk", apk)

	w = serve(r, httptest.NewRequest(http.MethodGet, "/app", nil))
	body := w.Body.String()
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-cache" ||
		!strings.Contains(w.Header().Get("Content-Security-Policy"), "default-src 'none'") ||
		!strings.Contains(body, `href="/app/android/AlexMessages-1.2.0-2.apk"`) ||
		!strings.Contains(body, strings.Repeat("ab", 32)) || !strings.Contains(body, "No iPhone build is published yet.") {
		t.Fatalf("/app: %d %v\n%s", w.Code, w.Header(), body)
	}
	if strings.Contains(body, "<script>") || !strings.Contains(body, "&lt;script&gt;") {
		t.Fatal("notes were not escaped")
	}

	w = serve(r, httptest.NewRequest(http.MethodGet, "/app/android/AlexMessages-1.2.0-2.apk", nil))
	if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), apk) ||
		w.Header().Get("Content-Type") != "application/vnd.android.package-archive" ||
		w.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" ||
		w.Header().Get("Content-Disposition") != `attachment; filename="AlexMessages-1.2.0-2.apk"` ||
		w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Content-Security-Policy") != "sandbox" {
		t.Fatalf("apk: %d %v", w.Code, w.Header())
	}
	for _, bad := range []string{
		"/app/ios/AlexMessages-1.2.0-2.apk",
		"/app/android/AlexMessages-1.2.0-3.apk",
		"/app/android/../policy.json",
		"/app/android",
		"/app/",
	} {
		w := serve(r, httptest.NewRequest(http.MethodGet, bad, nil))
		if w.Code != http.StatusNotFound || w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: %d %v", bad, w.Code, w.Header())
		}
	}

	w = serve(r, httptest.NewRequest(http.MethodGet, "/api/app/latest", nil))
	var policy ota.Policy
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-cache" || json.Unmarshal(w.Body.Bytes(), &policy) != nil ||
		policy.Android.Latest == nil || policy.Android.Latest.Build != 2 || policy.IOS.Latest != nil {
		t.Fatalf("/api/app/latest: %d %s", w.Code, w.Body.String())
	}
}

// sessionClient reads sessions.client and sessions.client_seen_at.
func sessionClient(t *testing.T, token string) (string, *int64) {
	t.Helper()
	conn, err := sql.Open("sqlite", db.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var client string
	var seen *int64
	if err := conn.QueryRow("SELECT client, client_seen_at FROM sessions WHERE token = ?", token).Scan(&client, &seen); err != nil {
		t.Fatal(err)
	}
	return client, seen
}

func TestClientHeaderGateAndSessionRecord(t *testing.T) {
	f, r := otaEnv(t)
	u := reviewUser(t, "alice")
	token := sessionFor(t, u.ID)

	me := func(client string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		if client != "" {
			req.Header.Set("X-AM-Client", client)
		}
		return serve(r, req)
	}

	// Soft gate (the default): REST works, and the header lands on the session.
	f.Write(t, "native/policy.json", []byte(androidPolicy(3, 3, false, "")))
	if w := me(build2); w.Code != http.StatusOK {
		t.Fatalf("soft gate: %d", w.Code)
	}
	if client, seen := sessionClient(t, token); client != build2 || seen == nil {
		t.Fatalf("session client %q %v", client, seen)
	}
	// The same value within 10 minutes is not written again.
	conn, err := sql.Open("sqlite", db.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec("UPDATE sessions SET client_seen_at = 1 WHERE token = ?", token); err != nil {
		t.Fatal(err)
	}
	me(build2)
	if _, seen := sessionClient(t, token); *seen != 1 {
		t.Fatalf("rewritten within the interval: %d", *seen)
	}
	// A changed value is written at once; the ring service's never.
	build2beta := strings.Replace(build2, "channel=production", "channel=beta", 1)
	me(build2beta)
	me("v=1;platform=android;build=2;component=ring")
	if client, _ := sessionClient(t, token); client != build2beta {
		t.Fatalf("session client %q", client)
	}
	// An unauthenticated request stores nothing (and still gets its 401).
	anon := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	anon.Header.Set("X-AM-Client", build2)
	if w := serve(r, anon); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", w.Code)
	}

	// Hard gate: 426 for the old build only, and never for exempt paths or
	// clients without the header.
	f.Write(t, "native/policy.json", []byte(androidPolicy(3, 3, true, "")))
	w := me(build2)
	var gated struct {
		Detail       string            `json:"detail"`
		ClientPolicy *ota.ClientPolicy `json:"client_policy"`
	}
	if w.Code != http.StatusUpgradeRequired || json.Unmarshal(w.Body.Bytes(), &gated) != nil || gated.Detail == "" ||
		gated.ClientPolicy == nil || gated.ClientPolicy.State != ota.PolicyUpdateRequired || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("hard gate: %d %s", w.Code, w.Body.String())
	}
	if w := me(""); w.Code != http.StatusOK {
		t.Fatalf("no header, hard gate: %d", w.Code)
	}
	if w := me("garbage"); w.Code != http.StatusOK {
		t.Fatalf("malformed header, hard gate: %d", w.Code)
	}
	if w := me(strings.Replace(build2, "build=2", "build=3", 1)); w.Code != http.StatusOK {
		t.Fatalf("build 3, hard gate: %d", w.Code)
	}
	for _, path := range []string{"/api/app/latest", "/api/ota/manifest"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-AM-Client", build2)
		if w := serve(r, req); w.Code == http.StatusUpgradeRequired {
			t.Fatalf("%s gated", path)
		}
	}
	// A policy that fails validation gates no one.
	f.Write(t, "native/policy.json", []byte(strings.Replace(androidPolicy(3, 3, true, ""), "https://", "javascript://", 1)))
	if w := me(build2); w.Code != http.StatusOK {
		t.Fatalf("invalid policy: %d", w.Code)
	}
}

// wsServer serves the engine and waits, at cleanup, until every WebSocket
// handler has returned.
func wsServer(t *testing.T, engine http.Handler) string {
	t.Helper()
	var handlers sync.WaitGroup
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlers.Add(1)
		defer handlers.Done()
		engine.ServeHTTP(w, r)
	}))
	t.Cleanup(func() {
		server.Close()
		done := make(chan struct{})
		go func() { handlers.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("a /ws handler outlived its test")
		}
	})
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

func readInit(t *testing.T, ws *websocket.Conn) map[string]json.RawMessage {
	t.Helper()
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, raw, err := ws.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var init map[string]json.RawMessage
	if err := json.Unmarshal(raw, &init); err != nil {
		t.Fatal(err)
	}
	if string(init["type"]) != `"init"` {
		t.Fatalf("first frame %s", raw)
	}
	return init
}

func expectClose(t *testing.T, ws *websocket.Conn, code int) {
	t.Helper()
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err := ws.ReadMessage()
	if !websocket.IsCloseError(err, code) {
		t.Fatalf("got %v, want close %d", err, code)
	}
}

func TestWebSocketClientPolicyAndHardGate(t *testing.T) {
	f, engine := otaEnv(t)
	u := reviewUser(t, "alice")
	token := sessionFor(t, u.ID)
	base := wsServer(t, engine)
	dial := func(query string, header http.Header) *websocket.Conn {
		t.Helper()
		ws, _, err := websocket.DefaultDialer.Dial(base+"/ws?"+query, header)
		if err != nil {
			t.Fatal(err)
		}
		return ws
	}
	f.Write(t, "native/policy.json", []byte(androidPolicy(3, 3, false, "")))

	// The web client sends no client=: no client_policy.
	ws := dial("token="+token, nil)
	if _, ok := readInit(t, ws)["client_policy"]; ok {
		t.Fatal("client_policy sent to a client without X-AM-Client")
	}
	ws.Close()

	// Build 2 below min_build, soft gate: init says update_required.
	ws = dial("token="+token+"&client="+url.QueryEscape(build2), nil)
	var cp ota.ClientPolicy
	if err := json.Unmarshal(readInit(t, ws)["client_policy"], &cp); err != nil || cp.State != ota.PolicyUpdateRequired ||
		cp.Latest == nil || cp.Latest.Build != 3 {
		t.Fatalf("client_policy %+v, %v", cp, err)
	}
	ws.Close()
	if client, _ := sessionClient(t, token); client != build2 {
		t.Fatalf("/ws did not record the client: %q", client)
	}

	// Hard gate: close 4426 on /ws and on the call listener (which sends the
	// header), before authentication.
	f.Write(t, "native/policy.json", []byte(androidPolicy(3, 3, true, "")))
	ws = dial("token="+token+"&client="+url.QueryEscape(build2), nil)
	expectClose(t, ws, 4426)
	ws.Close()
	ws = dial("client="+url.QueryEscape(build2), nil)
	expectClose(t, ws, 4426)
	ws.Close()
	ws = dial("mode=calls&token="+token, http.Header{"X-AM-Client": {"v=1;platform=android;build=2;component=ring"}})
	expectClose(t, ws, 4426)
	ws.Close()
	// A client without the header is never gated.
	ws = dial("token="+token, nil)
	readInit(t, ws)
	ws.Close()
}

func TestAccountDeletionAndExportCoverCensus(t *testing.T) {
	f, r := otaEnv(t)
	alice := reviewUser(t, "alice")
	bob := reviewUser(t, "bob")
	token := sessionFor(t, alice.ID)
	embedded := otatest.NewUUID()
	f.RegisterRuntime(t, ota.PlatformAndroid, otaRuntime, embedded, 2)
	for _, uid := range []int{alice.ID, alice.ID, bob.ID} {
		req := manifestRequest(ota.PlatformAndroid, embedded, embedded, otatest.NewUUID())
		req.Header.Set("Expo-Extra-Params", `am_uid="`+strconv.Itoa(uid)+`"`)
		serve(r, req)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/me/export", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := serve(r, req)
	var export struct {
		Installs []census.Install `json:"ota_installs"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &export) != nil || len(export.Installs) != 2 ||
		export.Installs[0].UserID == nil || *export.Installs[0].UserID != int64(alice.ID) {
		t.Fatalf("export: %d %s", w.Code, w.Body.String())
	}

	del := httptest.NewRequest(http.MethodPost, "/api/me/delete", strings.NewReader(`{"password":"password123"}`))
	del.Header.Set("Authorization", "Bearer "+token)
	del.Header.Set("Content-Type", "application/json")
	if w := serve(r, del); w.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	rows, _ := otaCensus(t, f).List()
	if len(rows) != 1 || rows[0].UserID == nil || *rows[0].UserID != int64(bob.ID) {
		t.Fatalf("left %+v", rows)
	}
}

func TestPruneOTACensusWithoutStore(t *testing.T) {
	otaEnv(t)
	dir := filepath.Join(t.TempDir(), "absent")
	t.Setenv("OTA_DIR", dir)
	PruneOTACensus()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("PruneOTACensus created the store: %v", err)
	}
}
