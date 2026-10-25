package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"alexmessage/internal/db"
	"alexmessage/internal/ota"
	"alexmessage/internal/webapp"
)

// The tests never reach the Pi, GitHub or npm. A temporary directory stands
// in for the Pi's home (alexmessage/bin/otactl, alexmessage/data/ota), and
// fake ssh, gh, npm and npx scripts come first on PATH: the fake ssh runs
// the remote command in that directory, so the SSH, rsync and tar paths run
// for real against it. An httptest server runs the real webapp engine over
// the same store, standing in for https://messages.alexanderjia.com.

// otactlBin is otactl built once for every test.
var otactlBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "otapublish-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	otactlBin = filepath.Join(dir, "otactl")
	if out, err := exec.Command("go", "build", "-o", otactlBin, "alexmessage/cmd/otactl").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building otactl: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

const fakeSSH = `#!/bin/sh
# Fake ssh: runs the remote command in $FAKE_PI_HOME instead of on the Pi.
while [ $# -gt 0 ]; do
  case "$1" in
    -p|-o|-i|-l) shift 2 ;;
    -*) shift ;;
    *) break ;;
  esac
done
shift
cd "$FAKE_PI_HOME" || exit 255
printf 'ssh %s\n' "$*" >> "$FAKE_LOG"
if [ -n "$FAKE_SSH_DOWN" ]; then exit 255; fi
if [ -n "$FAKE_NO_RSYNC" ] && [ "$*" = "command -v rsync >/dev/null" ]; then exit 1; fi
exec sh -c "$*"
`

const fakeGH = `#!/bin/sh
printf 'gh %s\n' "$*" >> "$FAKE_LOG"
printf '%s\n' "${FAKE_GH_RUNS:-[]}"
`

const fakeNPM = `#!/bin/sh
printf 'npm %s (in %s)\n' "$*" "$(basename "$PWD")" >> "$FAKE_LOG"
`

const fakeNPX = `#!/bin/sh
printf 'npx %s [profile=%s]\n' "$*" "$AM_OTA_PROFILE" >> "$FAKE_LOG"
[ "$1" = "--no" ] || { echo "fake npx: --no is missing" >&2; exit 1; }
shift
[ "$1" = "--" ] && shift
case "$1 $2" in
  "expo-updates runtimeversion:resolve")
    eval "rt=\${FAKE_RUNTIME_$4}"
    echo "Resolving the runtime of $4"
    printf '{"runtimeVersion":"%s","fingerprintSources":[],"workflow":"generic"}\n' "$rt" ;;
  "expo export")
    out=""
    while [ $# -gt 0 ]; do [ "$1" = "--output-dir" ] && out="$2"; shift; done
    mkdir -p "$out" && cp -R "$FAKE_EXPORT_DIR"/. "$out"/ ;;
  "expo config") cat "$FAKE_EXPO_CONFIG" ;;
  "expo prebuild")
    mkdir -p android ios && cp "$FAKE_GRADLEW" android/gradlew && chmod +x android/gradlew ;;
  "tsc --noEmit") ;;
  *) echo "fake npx: unexpected $*" >&2; exit 1 ;;
esac
`

type harness struct {
	t       *testing.T
	now     time.Time
	home    string
	archive string
	stick   string // the USB stick
	certOut string // keys init-root --cert-out
	repo    string // stands in for the clean worktree
	commit  string
	piHome  string
	app     string // the Pi's ~/alexmessage
	data    string
	store   string
	log     string
	stdin   string
	pass    map[string]string
	answers []string // passphrase answers used before pass, in order
	prompts []string
	procs   []process
	// extraEnv is added to what otapublish sees as its environment.
	extraEnv     []string
	removableErr error
	url          string
	// tamper may answer a request before the real server does.
	tamper func(w http.ResponseWriter, r *http.Request) bool
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, now: time.Now(), home: t.TempDir(), stick: t.TempDir(), piHome: t.TempDir(),
		pass: map[string]string{"root": "correct horse root", "leaf": "battery staple leaf"}}
	h.archive = filepath.Join(h.home, "AlexMessagesReleases")
	h.certOut = filepath.Join(t.TempDir(), "ota-root-2026.pem")
	h.app = filepath.Join(h.piHome, "alexmessage")
	h.data = filepath.Join(h.app, "data")
	h.store = filepath.Join(h.data, "ota")
	h.log = filepath.Join(h.piHome, "tools.log")
	if err := os.MkdirAll(filepath.Join(h.app, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A copy, not a symlink: otactl finds data/ next to the bin/ it runs from.
	h.copyFile(otactlBin, filepath.Join(h.app, "bin", "otactl"), 0o755)

	bin := t.TempDir()
	for name, script := range map[string]string{"ssh": fakeSSH, "gh": fakeGH, "npm": fakeNPM, "npx": fakeNPX} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_PI_HOME", h.piHome)
	t.Setenv("FAKE_LOG", h.log)
	t.Setenv("OTA_DIR", "")
	t.Setenv("OTA_DISABLED", "")
	// Dry runs keep their manifests in a temporary directory.
	t.Setenv("TMPDIR", t.TempDir())
	h.gitRepo()
	return h
}

func (h *harness) copyFile(src, dst string, perm os.FileMode) {
	h.t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, perm); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) git(dir string, args ...string) string {
	h.t.Helper()
	out, err := git(dir, args...)
	if err != nil {
		h.t.Fatal(err)
	}
	return out
}

// gitRepo makes the repository that stands in for the clean worktree.
func (h *harness) gitRepo() {
	h.t.Helper()
	h.repo = h.t.TempDir()
	h.write(filepath.Join(h.repo, ".gitignore"), "node_modules/\n")
	h.write(filepath.Join(h.repo, "mobile", "app.json"), `{"expo":{"name":"Alex Messages","version":"1.2.0","ios":{"buildNumber":"2"},"android":{"versionCode":2}}}`+"\n")
	h.write(filepath.Join(h.repo, "shared", "package.json"), "{}\n")
	h.git(h.repo, "init", "-q")
	h.git(h.repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "add", "-A")
	h.git(h.repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "init")
	h.commit = h.git(h.repo, "rev-parse", "HEAD")
	// Ignored, so the worktree stays clean: what npm ci would install.
	h.write(filepath.Join(h.repo, "mobile", "node_modules", "expo-updates", "package.json"), "{}\n")
}

func (h *harness) write(p, content string) {
	h.t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) passphrase(prompt string) ([]byte, error) {
	h.prompts = append(h.prompts, prompt)
	if len(h.answers) > 0 {
		a := h.answers[0]
		h.answers = h.answers[1:]
		return []byte(a), nil
	}
	if strings.Contains(strings.ToLower(prompt), "root") {
		return []byte(h.pass["root"]), nil
	}
	return []byte(h.pass["leaf"]), nil
}

// environ is the test process's environment without the variables that
// otapublish refuses (a developer's shell may have them).
func (h *harness) environ() []string {
	var out []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "EXPO_PUBLIC_") || strings.HasPrefix(kv, "AM_OTA_PROFILE=") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, h.extraEnv...)
}

func (h *harness) env(stdout, stderr io.Writer) *env {
	url := h.url
	if url == "" {
		url = publicURL
	}
	return &env{
		stdin: strings.NewReader(h.stdin), stdout: stdout, stderr: stderr,
		now: func() time.Time { return h.now }, getenv: os.Getenv, environ: h.environ,
		passphrase: h.passphrase, workFactor: 10, rootBits: 2048, leafBits: 2048,
		removable: func(string) error { return h.removableErr }, commit: h.commit,
		processes: func() ([]process, error) { return h.procs, nil }, sleep: func(time.Duration) {},
		http: &http.Client{Timeout: 30 * time.Second}, home: h.home, publicURL: url,
	}
}

// run runs otapublish as stage 2 in the test repository.
func (h *harness) run(args ...string) (string, string, int) {
	h.t.Helper()
	n := 1
	if args[0] == "keys" {
		n = 2
	}
	full := append(append(append([]string{}, args[:n]...), "--in-worktree", h.repo), args[n:]...)
	var out, errb bytes.Buffer
	code := run(full, h.env(&out, &errb))
	return out.String(), errb.String(), code
}

func (h *harness) ok(args ...string) string {
	h.t.Helper()
	out, errs, code := h.run(args...)
	if code != 0 {
		h.t.Fatalf("otapublish %s: exit %d\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), code, out, errs)
	}
	return out
}

func (h *harness) fail(args ...string) string {
	h.t.Helper()
	out, errs, code := h.run(args...)
	if code == 0 {
		h.t.Fatalf("otapublish %s succeeded, want a failure\nstdout:\n%s", strings.Join(args, " "), out)
	}
	return out + errs
}

// otactl runs the fake Pi's otactl directly.
func (h *harness) otactl(args ...string) string {
	h.t.Helper()
	out, err := exec.Command(filepath.Join(h.app, "bin", "otactl"), args...).CombinedOutput()
	if err != nil {
		h.t.Fatalf("otactl %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// serve starts the real server over the fake Pi's data directory.
func (h *harness) serve() {
	h.t.Helper()
	gin.SetMode(gin.TestMode)
	db.BaseDir = h.t.TempDir()
	db.DataDir = h.data
	db.UploadRoot = filepath.Join(h.data, "uploads")
	db.AvatarRoot = filepath.Join(h.data, "avatars")
	db.DBPath = filepath.Join(h.data, "alexmessage.db")
	if err := db.InitDB(); err != nil {
		h.t.Fatal(err)
	}
	eng := webapp.NewEngine()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.tamper != nil && h.tamper(w, r) {
			return
		}
		eng.ServeHTTP(w, r)
	}))
	h.t.Cleanup(srv.Close)
	h.url = srv.URL
}

const (
	rtIOS     = "fixture-ios-runtime"
	rtAndroid = "fixture-android-runtime"
)

// setupStore initializes the fake Pi's store, registers build 2 of both
// fixture runtimes (built from the test commit an hour ago), and points the
// fake tools at the testdata export.
func (h *harness) setupStore() {
	h.t.Helper()
	h.otactl("init")
	built := h.now.Add(-time.Hour).UTC()
	for p, rt := range map[string]string{"ios": rtIOS, "android": rtAndroid} {
		h.otactl("register-runtime", "--platform", p, "--runtime", rt, "--build", "2", "--version", "1.2.0",
			"--embedded-update-id", newUUID(), "--commit-time", ota.FormatTime(built), "--commit", h.commit)
	}
	wd, err := os.Getwd()
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Setenv("FAKE_EXPORT_DIR", filepath.Join(wd, "testdata", "export"))
	h.t.Setenv("FAKE_EXPO_CONFIG", filepath.Join(wd, "testdata", "expo-config.json"))
	h.t.Setenv("FAKE_RUNTIME_ios", rtIOS)
	h.t.Setenv("FAKE_RUNTIME_android", rtAndroid)
}

// setup makes what a publish to the fake Pi needs: a running server, the
// store, the root and a leaf (its certificates uploaded through the fake
// ssh).
func (h *harness) setup() {
	h.t.Helper()
	h.serve()
	h.setupStore()
	h.ok("keys", "init-root", "--out", h.stick, "--cert-out", h.certOut)
	h.ok("keys", "issue-leaf", "--root", filepath.Join(h.stick, "ota-root-2026.key.age"))
	// The deployed server's commit cannot be read on the fake Pi (it has no
	// bin/server), so step 8 asks for yes.
	h.stdin = "yes\n"
	h.prompts = nil
}

// toolLog is what the fake tools recorded.
func (h *harness) toolLog() string {
	b, _ := os.ReadFile(h.log)
	return string(b)
}

type statusOut struct {
	Pointers []pointerEntry `json:"pointers"`
}

func (h *harness) pointer(channel, platform, runtime string) *ota.Pointer {
	h.t.Helper()
	var st statusOut
	if err := json.Unmarshal([]byte(h.otactl("status", "--json")), &st); err != nil {
		h.t.Fatal(err)
	}
	for _, e := range st.Pointers {
		if e.Channel == channel && e.Platform == platform && e.Runtime == runtime {
			p := e.Pointer
			return &p
		}
	}
	return nil
}

func (h *harness) pointersLog() []pointerLogEntry {
	h.t.Helper()
	b, err := os.ReadFile(filepath.Join(h.archive, "ota", "pointers.log"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		h.t.Fatal(err)
	}
	var out []pointerLogEntry
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var e pointerLogEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			h.t.Fatalf("pointers.log line %q: %v", line, err)
		}
		out = append(out, e)
	}
	return out
}

func mustContain(t *testing.T, s string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(s, p) {
			t.Fatalf("output lacks %q:\n%s", p, s)
		}
	}
}
