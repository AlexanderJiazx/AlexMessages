package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"alexmessage/internal/db"
	"alexmessage/internal/ota"
)

func (h *harness) label(k int) string { return fmt.Sprintf("%s.%d", h.now.Format("20060102"), k) }

// local are the flags of a run against the fake Pi's store as a local store.
func (h *harness) local() []string {
	return []string{"--pi-dir", h.store, "--otactl", filepath.Join(h.app, "bin", "otactl")}
}

func (h *harness) storedManifest(id string) *ota.UpdateManifest {
	h.t.Helper()
	m, err := ota.ParseUpdateManifest(readFile(h.t, filepath.Join(h.store, "updates", id, "manifest.json")))
	if err != nil {
		h.t.Fatal(err)
	}
	return m
}

func (h *harness) storedUpdates() []string {
	h.t.Helper()
	entries, err := os.ReadDir(filepath.Join(h.store, "updates"))
	if err != nil && !os.IsNotExist(err) {
		h.t.Fatal(err)
	}
	var ids []string
	for _, e := range entries {
		ids = append(ids, e.Name())
	}
	return ids
}

// signer is the leaf that signs now, read the way otapublish reads it. Its
// passphrase prompt is not recorded.
func (h *harness) signer() *signer {
	h.t.Helper()
	defer func(p []string) { h.prompts = p }(h.prompts)
	a := &app{env: h.env(&bytes.Buffer{}, &bytes.Buffer{}), options: options{archive: h.archive, keysDir: h.keysDir(), profile: "production"}}
	s, err := a.signingLeaf()
	if err != nil {
		h.t.Fatal(err)
	}
	return s
}

func extraAM(t *testing.T, m *ota.UpdateManifest) ota.ManifestAM {
	t.Helper()
	var x manifestExtra
	if err := json.Unmarshal(m.Extra, &x); err != nil {
		t.Fatal(err)
	}
	return x.AM
}

// TestDryRunFixture is the Phase 2 check of plan step 2.2: `ota --dry-run`
// against a fixture export completes.
func TestDryRunFixture(t *testing.T) {
	h := newHarness(t)
	h.setupStore()
	before := h.toolLog()
	out := h.ok(append([]string{"ota", "--dry-run", "--allow-local", "--fixture", "testdata/dry-run.json",
		"--notes", "Fixture dry run"}, h.local()...)...)
	mustContain(t, out, "android: runtime "+rtAndroid+", update ", "ios: runtime "+rtIOS+", update ",
		"Dry run of label "+h.label(1)+" for beta: steps 1–12 passed. Nothing was signed, uploaded or activated.")
	if after := h.toolLog(); after != before {
		t.Fatalf("the fixture dry run ran tools:\n%s", strings.TrimPrefix(after, before))
	}
	dir := strings.TrimSpace(out[strings.Index(out, "Unsigned manifests: ")+len("Unsigned manifests: "):])
	for platform, rt := range map[string]string{"ios": rtIOS, "android": rtAndroid} {
		m, err := ota.ParseUpdateManifest(readFile(t, filepath.Join(dir, platform+"-manifest.json")))
		if err != nil {
			t.Fatal(err)
		}
		if m.RuntimeVersion != rt || len(m.Assets) != 2 {
			t.Fatalf("%s manifest: runtime %s, %d assets", platform, m.RuntimeVersion, len(m.Assets))
		}
		if !strings.HasPrefix(m.LaunchAsset.URL, publicURL+"/ota/assets/") || !strings.HasSuffix(m.LaunchAsset.URL, ".js") {
			t.Fatalf("%s launch asset URL %s", platform, m.LaunchAsset.URL)
		}
		am := extraAM(t, m)
		if am.Label != h.label(1) || am.Commit != h.commit || am.Notes != "Fixture dry run" || am.Critical {
			t.Fatalf("%s extra.am %+v", platform, am)
		}
		for _, a := range m.Assets {
			if a.FileExtension != ".png" && a.FileExtension != ".ttf" {
				t.Fatalf("asset %+v", a)
			}
		}
	}
	if len(h.prompts) != 0 {
		t.Fatalf("a dry run asked for a passphrase: %q", h.prompts)
	}
	if ids := h.storedUpdates(); len(ids) != 0 {
		t.Fatalf("a dry run stored updates %v", ids)
	}
	if ok, _ := pathExists(filepath.Join(h.archive, "ota")); ok {
		t.Fatal("a dry run wrote into the archive")
	}
	if h.pointer("beta", "ios", rtIOS) != nil {
		t.Fatal("a dry run set a pointer")
	}
}

// TestDryRunSteps runs a dry run without a fixture: every build step runs,
// through the fake npm, npx and gh.
func TestDryRunSteps(t *testing.T) {
	h := newHarness(t)
	h.setupStore()
	if err := os.Remove(h.log); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	out := h.ok(append([]string{"ota", "--dry-run", "--allow-local", "--notes", "All steps"}, h.local()...)...)
	mustContain(t, out, "steps 1–12 passed")
	want := []string{
		"gh run list --workflow Deploy --status in_progress --json databaseId",
		"npm ci --ignore-scripts (in shared)",
		"npm run typecheck (in shared)",
		"npm test (in shared)",
		"npm ci --ignore-scripts (in mobile)",
		"npx --no -- tsc --noEmit [profile=]",
		"npx --no -- expo-updates runtimeversion:resolve --platform android [profile=]",
		"npx --no -- expo-updates runtimeversion:resolve --platform ios [profile=]",
		"npx --no -- expo export --platform android --platform ios --output-dir ",
		"npx --no -- expo config --json --type public [profile=]",
	}
	var got []string
	for _, line := range strings.Split(strings.TrimSpace(h.toolLog()), "\n") {
		if !strings.HasPrefix(line, "ssh ") {
			got = append(got, line)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("tools ran:\n%s", strings.Join(got, "\n"))
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Fatalf("tool %d: %q, want %q…", i, got[i], want[i])
		}
	}
	mustContain(t, got[8], "--dump-assetmap --source-maps")
}

func TestPublishAndPromote(t *testing.T) {
	h := newHarness(t)
	h.setup()
	leaf := h.signer()
	out := h.ok("ota", "--notes", "Fixes the composer")
	label := h.label(1)
	mustContain(t, out, "ingested 2 update(s) and 4 new asset(s)", "Asset check passed: 4 asset URL(s) serve the right bytes.",
		"Live from now on (beta):", "Smoke test passed: "+h.url, "Tagged "+short(h.commit)+" as ota-"+label,
		"Promote it once it works on a beta phone of each platform: otapublish promote "+label)
	if len(h.prompts) != 1 || !strings.HasPrefix(h.prompts[0], "Leaf (ota-leaf-"+leaf.name+".key.age) passphrase") {
		t.Fatalf("prompts %q", h.prompts)
	}
	ptrs := map[string]*ota.Pointer{"ios": h.pointer("beta", "ios", rtIOS), "android": h.pointer("beta", "android", rtAndroid)}
	for platform, p := range ptrs {
		if p == nil || p.State != ota.StateUpdate {
			t.Fatalf("beta/%s pointer %+v", platform, p)
		}
		mustContain(t, out, fmt.Sprintf("beta/%s/%s: none -> update %s", platform, map[string]string{"ios": rtIOS, "android": rtAndroid}[platform], p.Update))
		for _, name := range ota.UpdateFiles {
			if !bytes.Equal(readFile(t, filepath.Join(h.archive, "ota", "updates", p.Update, name)), readFile(t, filepath.Join(h.store, "updates", p.Update, name))) {
				t.Fatalf("the archive's and the store's %s/%s differ", p.Update, name)
			}
		}
		m := h.storedManifest(p.Update)
		if am := extraAM(t, m); am.Label != label || am.Commit != h.commit || am.Notes != "Fixes the composer" {
			t.Fatalf("%s extra.am %+v", platform, am)
		}
		if !strings.HasPrefix(m.LaunchAsset.URL, h.url+"/ota/assets/") {
			t.Fatalf("%s launch asset URL %s", platform, m.LaunchAsset.URL)
		}
		if got := string(readFile(t, filepath.Join(h.store, "updates", p.Update, "manifest.leaf"))); got != ota.LeafSHA256(leaf.cert)+"\n" {
			t.Fatalf("%s manifest.leaf %q", platform, got)
		}
	}
	if h.pointer("production", "ios", rtIOS) != nil {
		t.Fatal("a beta publish set a production pointer")
	}
	log := h.pointersLog()
	if len(log) != 2 || log[0].Action != "activate" || log[0].Label != label || log[0].Commit != h.commit || log[0].Old != nil {
		t.Fatalf("pointers.log %+v", log)
	}
	if tag := h.git(h.repo, "rev-parse", "ota-"+label+"^{commit}"); tag != h.commit {
		t.Fatalf("tag ota-%s is %s", label, tag)
	}
	mustContain(t, h.ok("status"), "Pointers:", ptrs["ios"].Update)

	// promote needs an admin's report of each update.
	mustContain(t, h.fail("promote", label), "promote refused", "no approved admin's app has reported running update")
	uid, err := db.CreateUser("boss", "hash", "Boss", "approved", true)
	if err != nil {
		t.Fatal(err)
	}
	for platform, p := range ptrs {
		tok := "token-" + platform
		if err := db.CreateSession(tok, uid, h.now.Add(time.Hour).Unix(), "user"); err != nil {
			t.Fatal(err)
		}
		rt := map[string]string{"ios": rtIOS, "android": rtAndroid}[platform]
		client := "v=1;platform=" + platform + ";version=1.2.0;build=2;runtime=" + rt + ";update=" + p.Update + ";channel=beta"
		if err := db.RecordSessionClient(tok, client, h.now.Unix()); err != nil {
			t.Fatal(err)
		}
	}
	out = h.ok("promote", label)
	for platform, p := range ptrs {
		got := h.pointer("production", platform, map[string]string{"ios": rtIOS, "android": rtAndroid}[platform])
		if got == nil || got.State != ota.StateUpdate || got.Update != p.Update {
			t.Fatalf("production/%s pointer %+v", platform, got)
		}
	}
	mustContain(t, out, "production/ios/"+rtIOS+": none -> update "+ptrs["ios"].Update)
	if log := h.pointersLog(); len(log) != 4 || log[2].Action != "promote" || log[2].Channel != "production" {
		t.Fatalf("pointers.log %+v", log)
	}

	// The next publish of the day gets the next label.
	h.now = h.now.Add(time.Minute)
	mustContain(t, h.ok("ota", "--notes", "Second"), "Tagged "+short(h.commit)+" as ota-"+h.label(2))
}

func TestPublishSkipsUnregisteredRuntime(t *testing.T) {
	h := newHarness(t)
	h.setup()
	t.Setenv("FAKE_RUNTIME_android", "changed-android-runtime")
	out := h.ok("ota", "--notes", "iOS only")
	mustContain(t, out, "android: skipped. Native code changed since the last native build of android (runtime changed-android-runtime is not registered). Run `otapublish native`.")
	if h.pointer("beta", "ios", rtIOS) == nil || h.pointer("beta", "android", rtAndroid) != nil {
		t.Fatal("the pointers are not beta/ios only")
	}
	t.Setenv("FAKE_RUNTIME_ios", "changed-ios-runtime")
	h.now = h.now.Add(time.Minute)
	mustContain(t, h.fail("ota", "--notes", "Nothing"), "no platform has a registered runtime; nothing to publish")
}

func TestPublishAssetCheckFails(t *testing.T) {
	h := newHarness(t)
	h.setup()
	h.tamper = func(w http.ResponseWriter, r *http.Request) bool {
		if strings.HasPrefix(r.URL.Path, "/ota/assets/") {
			w.Write([]byte("an old copy in the cache"))
			return true
		}
		return false
	}
	mustContain(t, h.fail("ota", "--notes", "Cached"), "does not hash to its name; nothing was activated", "Custom Purge")
	if h.pointer("beta", "ios", rtIOS) != nil || h.pointer("beta", "android", rtAndroid) != nil {
		t.Fatal("a failed asset check activated a pointer")
	}
	if log := h.pointersLog(); len(log) != 0 {
		t.Fatalf("pointers.log %+v", log)
	}
}

func TestPublishSmokeTestFailureRestoresPointers(t *testing.T) {
	h := newHarness(t)
	h.setup()
	noUpdate := func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/api/ota/manifest" {
			w.WriteHeader(http.StatusNoContent)
			return true
		}
		return false
	}

	// The first publish of a runtime: the pointers are removed again.
	h.tamper = noUpdate
	mustContain(t, h.fail("ota", "--notes", "First"), "the smoke test failed, and the previous pointers are back", "answered 204 No Content")
	if h.pointer("beta", "ios", rtIOS) != nil || h.pointer("beta", "android", rtAndroid) != nil {
		t.Fatal("the pointers were not removed")
	}
	if log := h.pointersLog(); len(log) != 4 || log[2].Action != "restore" || log[2].New != nil {
		t.Fatalf("pointers.log %+v", log)
	}

	// A later publish: the previous update is back.
	h.tamper = nil
	h.now = h.now.Add(time.Minute)
	h.ok("ota", "--notes", "Second")
	prev := h.pointer("beta", "ios", rtIOS)
	h.now = h.now.Add(time.Minute)
	h.tamper = noUpdate
	mustContain(t, h.fail("ota", "--notes", "Third"), "the previous pointers are back")
	if p := h.pointer("beta", "ios", rtIOS); p == nil || p.Update != prev.Update || p.State != ota.StateUpdate {
		t.Fatalf("beta/ios pointer %+v, want update %s", p, prev.Update)
	}
	log := h.pointersLog()
	last := log[len(log)-1]
	if last.Action != "restore" || last.New == nil || last.Old == nil || last.New.Update == last.Old.Update {
		t.Fatalf("the last pointers.log entry %+v", last)
	}
	if _, err := git(h.repo, "rev-parse", "ota-"+h.label(3)); err == nil {
		t.Fatal("a failed publish was tagged")
	}
}

func TestPublishRefusals(t *testing.T) {
	h := newHarness(t)
	h.setup()
	refuse := func(want string, args ...string) {
		t.Helper()
		mustContain(t, h.fail(append([]string{"ota", "--notes", "Refused"}, args...)...), want)
		if len(h.prompts) != 0 {
			t.Fatalf("a refused publish asked for a passphrase: %q", h.prompts)
		}
		if ids := h.storedUpdates(); len(ids) != 0 {
			t.Fatalf("a refused publish stored %v", ids)
		}
	}
	for _, args := range [][]string{
		{"ota"},
		{"ota", "--notes", "x", "--channel", "alpha"},
		{"ota", "--notes", "x", "--allow-local"},
		{"ota", "--notes", "x", "--fixture", "testdata/dry-run.json"},
		{"ota", "--notes", "x", "--platform", "windows"},
		{"ota", "--notes", "x", "--profile", "drill"},
	} {
		if _, errs, code := h.run(args...); code != 2 {
			t.Fatalf("%q: exit %d, want 2\n%s", args, code, errs)
		}
	}
	refuse("a publish into --pi-dir needs --public-url", h.local()...)

	h.extraEnv = []string{"EXPO_PUBLIC_SERVER=https://example.com"}
	refuse("unset EXPO_PUBLIC_SERVER first")
	h.extraEnv = nil

	t.Setenv("FAKE_GH_RUNS", `[{"databaseId":7}]`)
	refuse("a Deploy workflow run is in progress")
	t.Setenv("FAKE_GH_RUNS", "")

	real := h.now
	h.now = real.Add(5 * time.Minute)
	refuse("this Mac's clock differs from the Pi's by")
	h.now = real

	h.stdin = "no\n"
	refuse("not confirmed; nothing was published")
	h.stdin = "yes\n"

	h.procs = []process{{pid: 4242, elapsed: 0, name: "node"}}
	refuse("these processes started during this publish and are still running: node (pid 4242")
	h.procs = []process{{pid: 4343, elapsed: time.Hour, name: "java"}}
	h.stdin = "yes\nno\n"
	refuse("not confirmed; nothing was signed")
	h.stdin = "yes\nyes\n"
	h.procs = nil

	// A native build made after now: phones would ignore the update.
	h.otactl("register-runtime", "--platform", "ios", "--runtime", rtIOS, "--build", "3", "--version", "1.2.1",
		"--embedded-update-id", newUUID(), "--commit-time", ota.FormatTime(h.now.Add(time.Hour)), "--commit", h.commit)
	refuse("is not later than the build time of ios native build 3")
}

func TestProcessCheckWaits(t *testing.T) {
	h := newHarness(t)
	calls, slept := 0, 0
	e := h.env(&bytes.Buffer{}, &bytes.Buffer{})
	e.processes = func() ([]process, error) {
		calls++
		if calls < 4 {
			return []process{{pid: 1, elapsed: 0, name: "esbuild"}, {pid: 2, elapsed: 0, name: "zsh"}}, nil
		}
		return nil, nil
	}
	e.sleep = func(time.Duration) { slept++ }
	a := &app{env: e, options: options{profile: "production"}, started: h.now}
	if err := a.processCheck(); err != nil || slept != 3 {
		t.Fatalf("processCheck: %v after %d waits", err, slept)
	}
}

func TestParseProcesses(t *testing.T) {
	procs, err := parseProcesses("  101 01:02 /usr/local/bin/node\n 202 2-03:04:05 /Applications/Visual Studio Code.app/Contents/MacOS/Electron\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(procs) != 2 || procs[0].pid != 101 || procs[0].elapsed != 62*time.Second || procs[0].name != "node" ||
		procs[1].elapsed != 2*24*time.Hour+3*time.Hour+4*time.Minute+5*time.Second || procs[1].name != "Electron" {
		t.Fatalf("%+v", procs)
	}
	if _, err := parseEtime("1:2:3:4"); err == nil {
		t.Fatal("parseEtime accepted 1:2:3:4")
	}
}

func TestRepublish(t *testing.T) {
	h := newHarness(t)
	h.setup()
	h.ok("ota", "--notes", "First")
	old := h.pointer("beta", "ios", rtIOS)
	h.now = h.now.Add(time.Minute)
	out := h.ok("republish", h.label(1))
	mustContain(t, out, "ios: republishing "+h.label(1)+" (update "+old.Update+") as "+h.label(2),
		"ingested 2 update(s) and 0 new asset(s)", "Live from now on (production):", "Smoke test passed",
		"Tagged "+short(h.commit)+" as ota-"+h.label(2))
	p := h.pointer("production", "ios", rtIOS)
	if p == nil || p.Update == old.Update {
		t.Fatalf("production/ios pointer %+v", p)
	}
	was, now := h.storedManifest(old.Update), h.storedManifest(p.Update)
	if now.LaunchAsset != was.LaunchAsset || len(now.Assets) != len(was.Assets) {
		t.Fatal("the republished update has other assets")
	}
	if am := extraAM(t, now); am.Label != h.label(2) || am.Notes != "Republish of "+h.label(1) || am.Commit != h.commit {
		t.Fatalf("extra.am %+v", am)
	}
	if h.pointer("beta", "ios", rtIOS).Update != old.Update {
		t.Fatal("republish to production changed the beta pointer")
	}
	mustContain(t, h.fail("republish", "20000101.1"), "has no update with label 20000101.1")
}

func TestResignActive(t *testing.T) {
	h := newHarness(t)
	h.setup()
	h.ok("ota", "--notes", "First")
	ios := h.pointer("beta", "ios", rtIOS).Update
	manifest := readFile(t, filepath.Join(h.store, "updates", ios, "manifest.json"))
	oldSig := readFile(t, filepath.Join(h.store, "updates", ios, "manifest.sig"))

	h.now = h.now.Add(time.Minute)
	h.ok("keys", "issue-leaf", "--root", h.rootKey())
	leaf := h.signer()
	out := h.ok("resign-active")
	mustContain(t, out, "re-signed "+ios, "2 update(s) now carry the signatures of leaf "+leaf.name, "Smoke test passed for 2 live pointer(s).")
	for _, dir := range []string{h.store, filepath.Join(h.archive, "ota")} {
		u := filepath.Join(dir, "updates", ios)
		if got := string(readFile(t, filepath.Join(u, "manifest.leaf"))); got != ota.LeafSHA256(leaf.cert)+"\n" {
			t.Fatalf("%s manifest.leaf %q", u, got)
		}
		if bytes.Equal(readFile(t, filepath.Join(u, "manifest.sig")), oldSig) {
			t.Fatalf("%s keeps the old signature", u)
		}
		if !bytes.Equal(readFile(t, filepath.Join(u, "manifest.json")), manifest) {
			t.Fatalf("%s manifest.json changed", u)
		}
	}
	// The signatures are deterministic: a second run changes nothing.
	mustContain(t, h.ok("resign-active"), "0 update(s) now carry the signatures of leaf "+leaf.name+"; 2 were already signed by it")
}

func TestDrill(t *testing.T) {
	h := newHarness(t)
	h.setupStore()
	drill := append([]string{"--profile", "drill"}, h.local()...)
	if _, _, code := h.run("ota", "--notes", "x", "--profile", "drill"); code != 2 {
		t.Fatalf("a drill without --pi-dir: exit %d, want 2", code)
	}
	h.ok(append([]string{"keys", "init-root", "--out", h.stick, "--cert-out", h.certOut}, drill...)...)
	out := h.ok(append([]string{"keys", "issue-leaf", "--root", h.rootKey()}, drill...)...)
	mustContain(t, out, "root certificate installed, 1 new leaf certificate(s)")
	if ok, _ := pathExists(filepath.Join(h.home, ".alexmessages", "drill-keys", "ota-leaf-"+h.leafName(1)+".key.age")); !ok {
		t.Fatal("the drill leaf is not in ~/.alexmessages/drill-keys")
	}
	out = h.ok(append([]string{"ota", "--notes", "Drill"}, drill...)...)
	mustContain(t, out, "Live from now on (beta):")
	if strings.Contains(out, "Smoke test") || strings.Contains(out, "Asset check") || strings.Contains(out, "Tagged") {
		t.Fatalf("the drill ran a production-only step:\n%s", out)
	}
	if len(h.prompts) != 0 {
		t.Fatalf("the drill asked for a passphrase: %q", h.prompts)
	}
	for platform, base := range map[string]string{"android": "http://10.0.2.2:8799", "ios": "http://localhost:8799"} {
		p := h.pointer("beta", platform, map[string]string{"ios": rtIOS, "android": rtAndroid}[platform])
		if p == nil {
			t.Fatalf("no beta/%s pointer", platform)
		}
		if u := h.storedManifest(p.Update).LaunchAsset.URL; !strings.HasPrefix(u, base+"/ota/assets/") {
			t.Fatalf("%s launch asset URL %s", platform, u)
		}
		if ok, _ := pathExists(filepath.Join(h.archive, "drill", "ota", "updates", p.Update, "manifest.json")); !ok {
			t.Fatalf("the drill update %s is not in the drill archive", p.Update)
		}
	}
	if ok, _ := pathExists(filepath.Join(h.archive, "ota")); ok {
		t.Fatal("the drill wrote into the real archive")
	}
	if tags := h.git(h.repo, "tag", "--list", "ota-*"); tags != "" {
		t.Fatalf("the drill tagged %s", tags)
	}
	mustContain(t, h.toolLog(), "expo export --platform android --platform ios", "[profile=drill]")
	if strings.Contains(h.toolLog(), "gh run list") {
		t.Fatal("the drill looked at GitHub Actions")
	}
}
