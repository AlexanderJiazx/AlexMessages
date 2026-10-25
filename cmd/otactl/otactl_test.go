package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"alexmessage/internal/ota"
	"alexmessage/internal/ota/otatest"
	"alexmessage/internal/ota/sign"
)

const rt = "rt-android-1"

// harness runs otactl against a fixture store, with a fixed clock.
type harness struct {
	t     *testing.T
	f     *otatest.Fixture
	s     *ota.Store
	data  string
	env   map[string]string
	now   time.Time
	stdin string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	f := otatest.New(t)
	return &harness{t: t, f: f, s: &ota.Store{Dir: f.Dir}, data: t.TempDir(),
		env: map[string]string{"USER": "tester"}, now: f.Now.Add(time.Minute)}
}

func (h *harness) run(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	e := &env{stdin: strings.NewReader(h.stdin), stdout: &out, stderr: &errb,
		now: func() time.Time { return h.now }, getenv: func(k string) string { return h.env[k] }}
	full := append([]string{args[0], "--ota", h.f.Dir, "--data", h.data}, args[1:]...)
	code := run(full, e)
	return code, out.String(), errb.String()
}

func (h *harness) ok(args ...string) string {
	h.t.Helper()
	code, out, errs := h.run(args...)
	if code != 0 {
		h.t.Fatalf("otactl %s: exit %d\n%s%s", strings.Join(args, " "), code, out, errs)
	}
	return out
}

func (h *harness) fail(args ...string) string {
	h.t.Helper()
	code, out, errs := h.run(args...)
	if code == 0 {
		h.t.Fatalf("otactl %s succeeded:\n%s", strings.Join(args, " "), out)
	}
	return errs
}

func (h *harness) write(rel string, b []byte) {
	h.t.Helper()
	p := filepath.Join(h.f.Dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		h.t.Fatal(err)
	}
}

// stage writes updates (with their assets) into incoming/<publish>/.
func (h *harness) stage(publish string, ups ...*otatest.Update) {
	h.t.Helper()
	for _, u := range ups {
		h.write("incoming/"+publish+"/assets/"+u.AssetName, u.Asset)
		for name, b := range u.Files() {
			h.write("incoming/"+publish+"/updates/"+u.ID+"/"+name, b)
		}
	}
}

func (h *harness) pointer(channel, platform, runtime string) *ota.Pointer {
	h.t.Helper()
	p, err := h.s.ReadPointer(channel, platform, runtime)
	if err != nil {
		h.t.Fatal(err)
	}
	return p
}

// actions returns the action of every line of log/actions.log.
func (h *harness) actions() []map[string]any {
	h.t.Helper()
	f, err := os.Open(filepath.Join(h.f.Dir, "log", "actions.log"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		h.t.Fatal(err)
	}
	defer f.Close()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			h.t.Fatalf("actions.log line %q: %v", sc.Text(), err)
		}
		out = append(out, m)
	}
	return out
}

func (h *harness) lastAction() map[string]any {
	h.t.Helper()
	a := h.actions()
	if len(a) == 0 {
		h.t.Fatal("log/actions.log is empty")
	}
	return a[len(a)-1]
}

func pathExists(t *testing.T, p string) bool {
	t.Helper()
	_, err := os.Stat(p)
	return err == nil
}

func TestIngestInstallsAndRerunIsSafe(t *testing.T) {
	h := newHarness(t)
	u := h.f.BuildUpdate(t, ota.PlatformAndroid, rt, otatest.UpdateOptions{})
	h.stage("p1", u)
	h.write("incoming/p1/certs/leaf-202611.pem", h.f.LeafPEM)
	h.write("incoming/p1/native/android/AlexMessages-1.2.0-2.apk", []byte("apk"))
	out := h.ok("ingest", "p1", "--json")
	var plan ingestPlan
	if err := json.Unmarshal([]byte(out), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Updates) != 1 || plan.Updates[0] != u.ID || len(plan.Assets) != 1 || len(plan.Native) != 1 || plan.Unchanged != 1 {
		t.Fatalf("plan %+v", plan)
	}
	if _, err := h.s.VerifyUpdate(u.ID, h.f.Root, h.now); err != nil {
		t.Fatalf("stored update: %v", err)
	}
	if pathExists(t, filepath.Join(h.f.Dir, "incoming", "p1")) {
		t.Fatal("incoming/p1 was not removed")
	}
	if a := h.lastAction(); a["action"] != "ingest" || a["user"] != "tester" {
		t.Fatalf("log line %v", a)
	}
	// The same upload again changes nothing and succeeds.
	h.stage("p2", u)
	h.write("incoming/p2/native/android/AlexMessages-1.2.0-2.apk", []byte("apk"))
	out = h.ok("ingest", "p2", "--json")
	if err := json.Unmarshal([]byte(out), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Updates)+len(plan.Assets)+len(plan.Native) != 0 || plan.Unchanged != 3 {
		t.Fatalf("second plan %+v", plan)
	}
}

func TestIngestRefuses(t *testing.T) {
	h := newHarness(t)
	stored := h.f.AddUpdateWith(t, ota.PlatformAndroid, rt, otatest.UpdateOptions{})
	h.write("native/android/AlexMessages-1.2.0-2.apk", []byte("apk"))

	resignedSame := func() *otatest.Update {
		// The stored update's id with other notes, validly signed: published
		// bytes must still never change.
		u := *stored
		var m map[string]any
		json.Unmarshal(u.Manifest, &m)
		m["extra"].(map[string]any)["am"].(map[string]any)["notes"] = "changed"
		u.Manifest, _ = json.Marshal(m)
		u.ManifestSig, _ = sign.SignatureHeader(u.Manifest, h.f.LeafKey, ota.RootKeyID)
		u.Info = bytes.Replace(u.Info, []byte(`"notes":"test"`), []byte(`"notes":"changed"`), 1)
		return &u
	}
	cases := map[string]func(dir string){
		"non-empty metadata": func(dir string) {
			h.stage(filepath.Base(dir), h.f.BuildUpdate(t, ota.PlatformAndroid, rt, otatest.UpdateOptions{Metadata: map[string]any{"a": "b"}}))
		},
		"createdAt 10 minutes ahead": func(dir string) {
			h.stage(filepath.Base(dir), h.f.BuildUpdate(t, ota.PlatformAndroid, rt, otatest.UpdateOptions{CreatedAt: h.now.Add(10 * time.Minute)}))
		},
		"an asset that does not hash to its name": func(dir string) {
			u := h.f.BuildUpdate(t, ota.PlatformAndroid, rt, otatest.UpdateOptions{})
			h.stage(filepath.Base(dir), u)
			h.write("incoming/"+filepath.Base(dir)+"/assets/"+u.AssetName, []byte("tampered"))
		},
		"a missing asset": func(dir string) {
			u := h.f.BuildUpdate(t, ota.PlatformAndroid, rt, otatest.UpdateOptions{})
			h.stage(filepath.Base(dir), u)
			os.Remove(filepath.Join(dir, "assets", u.AssetName))
		},
		"a symbolic link": func(dir string) {
			h.stage(filepath.Base(dir), h.f.BuildUpdate(t, ota.PlatformAndroid, rt, otatest.UpdateOptions{}))
			os.Symlink("/etc/passwd", filepath.Join(dir, "assets", "x"))
		},
		"an unknown file": func(dir string) {
			h.stage(filepath.Base(dir), h.f.BuildUpdate(t, ota.PlatformAndroid, rt, otatest.UpdateOptions{}))
			h.write("incoming/"+filepath.Base(dir)+"/updates/run.sh", []byte("#!/bin/sh"))
		},
		"changed bytes of a published update": func(dir string) {
			h.stage(filepath.Base(dir), resignedSame())
		},
		"another root certificate": func(dir string) {
			der, err := sign.NewRoot(h.f.LeafKey, "other root", h.now)
			if err != nil {
				t.Fatal(err)
			}
			h.write("incoming/"+filepath.Base(dir)+"/certs/ota-root-2026.pem", sign.EncodePEM(der))
		},
		"a published native build with other bytes": func(dir string) {
			h.write("incoming/"+filepath.Base(dir)+"/native/android/AlexMessages-1.2.0-2.apk", []byte("other apk"))
		},
	}
	i := 0
	for name, setup := range cases {
		i++
		publish := "bad" + string(rune('a'+i))
		dir := filepath.Join(h.f.Dir, "incoming", publish)
		setup(dir)
		before, _ := h.s.ListUpdates()
		errs := h.fail("ingest", publish)
		if !strings.Contains(errs, "nothing was installed") && !strings.Contains(errs, "incoming/"+publish) {
			t.Errorf("%s: unexpected message %q", name, errs)
		}
		after, _ := h.s.ListUpdates()
		if len(after) != len(before) {
			t.Errorf("%s: an update was installed", name)
		}
		if !pathExists(t, dir) {
			t.Errorf("%s: the refused upload was removed", name)
		}
	}
	// 4 minutes ahead is within the 5 the Pi allows.
	h.stage("ahead", h.f.BuildUpdate(t, ota.PlatformAndroid, rt, otatest.UpdateOptions{CreatedAt: h.now.Add(4 * time.Minute)}))
	h.ok("ingest", "ahead")
	h.fail("ingest", "../x")
}

func TestIngestResignReplacesOnlySignatures(t *testing.T) {
	h := newHarness(t)
	u := h.f.AddUpdate(t, ota.PlatformAndroid, rt)
	newLeaf := h.f.AddLeaf(t, "202711", h.now.Add(-time.Hour))
	pemBytes, _ := os.ReadFile(filepath.Join(h.f.Dir, "certs", "leaf-202711.pem"))
	os.Remove(filepath.Join(h.f.Dir, "certs", "leaf-202711.pem"))

	re := *u
	re.LeafSHA = ota.LeafSHA256(newLeaf)
	re.ManifestSig, _ = sign.SignatureHeader(u.Manifest, h.f.LeafKey, ota.RootKeyID)
	re.RollbackSig, _ = sign.SignatureHeader(u.Rollback, h.f.LeafKey, ota.RootKeyID)
	for name, b := range re.Files() {
		h.write("incoming/rs/updates/"+u.ID+"/"+name, b)
	}
	h.write("incoming/rs/certs/leaf-202711.pem", pemBytes)
	out := h.ok("ingest", "rs", "--json")
	if !strings.Contains(out, `"resigned": [`+"\n"+`    "`+u.ID) {
		t.Fatalf("plan %s", out)
	}
	v, err := h.s.VerifyUpdate(u.ID, h.f.Root, h.now)
	if err != nil {
		t.Fatal(err)
	}
	if ota.LeafSHA256(v.ManifestLeaf) != ota.LeafSHA256(newLeaf) || ota.LeafSHA256(v.RollbackLeaf) != ota.LeafSHA256(newLeaf) {
		t.Fatal("the stored signatures were not replaced")
	}
	body, _ := os.ReadFile(filepath.Join(h.f.Dir, "updates", u.ID, "manifest.json"))
	if !bytes.Equal(body, u.Manifest) {
		t.Fatal("the manifest body changed")
	}
}

func TestIngestIntoAnEmptyStore(t *testing.T) {
	h := newHarness(t)
	rootPEM, _ := os.ReadFile(filepath.Join(h.f.Dir, "certs", "ota-root-2026.pem"))
	os.RemoveAll(filepath.Join(h.f.Dir, "certs"))
	u := h.f.BuildUpdate(t, ota.PlatformIOS, rt, otatest.UpdateOptions{})
	h.stage("first", u)
	h.fail("ingest", "first") // no root anywhere
	h.write("incoming/first/certs/ota-root-2026.pem", rootPEM)
	h.write("incoming/first/certs/leaf-202611.pem", h.f.LeafPEM)
	h.ok("ingest", "first")
	if _, err := h.s.VerifyUpdate(u.ID, h.f.Root, h.now); err != nil {
		t.Fatal(err)
	}
}

func TestActivateRefusals(t *testing.T) {
	h := newHarness(t)
	embedded := otatest.NewUUID()
	at := func(d time.Duration) otatest.UpdateOptions {
		return otatest.UpdateOptions{CreatedAt: h.f.Now.Add(d)}
	}
	u1 := h.f.AddUpdateWith(t, ota.PlatformAndroid, rt, at(2*time.Second))
	errs := h.fail("activate", "--channel", "beta", "--update", u1.ID)
	if !strings.Contains(errs, "no registered native build") {
		t.Fatalf("unregistered runtime: %s", errs)
	}
	h.f.RegisterRuntime(t, ota.PlatformAndroid, rt, embedded, 2)

	old := h.f.AddUpdateWith(t, ota.PlatformAndroid, rt, at(-time.Hour))
	if errs := h.fail("activate", "--channel", "beta", "--update", old.ID); !strings.Contains(errs, "not later than the build time") {
		t.Fatalf("older than the binary: %s", errs)
	}

	expiring := h.f.AddLeaf(t, "old", h.now.Add(-380*24*time.Hour))
	ue := h.f.AddUpdateWith(t, ota.PlatformAndroid, rt, otatest.UpdateOptions{CreatedAt: h.f.Now.Add(3 * time.Second), Leaf: expiring, LeafKey: h.f.LeafKey})
	if errs := h.fail("activate", "--channel", "beta", "--update", ue.ID); !strings.Contains(errs, "within 30 days") {
		t.Fatalf("expiring leaf: %s", errs)
	}

	h.ok("activate", "--channel", "beta", "--update", u1.ID)
	if p := h.pointer("beta", "android", rt); p.State != ota.StateUpdate || p.Update != u1.ID {
		t.Fatalf("pointer %+v", p)
	}
	if out := h.ok("activate", "--channel", "beta", "--update", u1.ID); !strings.Contains(out, "unchanged") {
		t.Fatalf("activating the same update again: %s", out)
	}

	older := h.f.AddUpdateWith(t, ota.PlatformAndroid, rt, at(time.Second))
	if errs := h.fail("activate", "--channel", "beta", "--update", older.ID); !strings.Contains(errs, "not later than the createdAt of the current update") {
		t.Fatalf("not newer than the current update: %s", errs)
	}

	revoked := h.f.AddUpdateWith(t, ota.PlatformAndroid, rt, at(5*time.Second))
	h.ok("revoke", revoked.ID)
	if errs := h.fail("activate", "--channel", "beta", "--update", revoked.ID); !strings.Contains(errs, "revoked") {
		t.Fatalf("revoked: %s", errs)
	}

	// A rollback pointer serves commitTime = createdAt + 1 s: an update
	// within that second is refused.
	h.f.Point(t, "beta", "android", rt, ota.StateRollback, u1.ID)
	within := h.f.AddUpdateWith(t, ota.PlatformAndroid, rt, at(2*time.Second+500*time.Millisecond))
	if errs := h.fail("activate", "--channel", "beta", "--update", within.ID); !strings.Contains(errs, "commitTime of the rollback directive") {
		t.Fatalf("within the directive's second: %s", errs)
	}
	later := h.f.AddUpdateWith(t, ota.PlatformAndroid, rt, at(4*time.Second))
	h.ok("activate", "--channel", "beta", "--update", later.ID)

	if errs := h.fail("activate", "--channel", "staging", "--update", later.ID); !strings.Contains(errs, "beta or production") {
		t.Fatalf("bad channel: %s", errs)
	}
	if n := len(h.actions()); n != 3 { // two activations and one revoke
		t.Fatalf("%d log lines, want 3", n)
	}
}

func TestHaltAllAndLabel(t *testing.T) {
	h := newHarness(t)
	a1 := h.f.AddUpdateWith(t, ota.PlatformAndroid, rt, otatest.UpdateOptions{Label: "20261105.1"})
	i1 := h.f.AddUpdateWith(t, ota.PlatformIOS, "rt-ios", otatest.UpdateOptions{Label: "20261105.1"})
	b1 := h.f.AddUpdateWith(t, ota.PlatformAndroid, "rt-old", otatest.UpdateOptions{Label: "20261101.1"})
	h.f.Point(t, "beta", "android", rt, ota.StateUpdate, a1.ID)
	h.f.Point(t, "production", "android", rt, ota.StateRollback, a1.ID)
	h.f.Point(t, "production", "ios", "rt-ios", ota.StateUpdate, i1.ID)
	h.f.Point(t, "production", "android", "rt-old", ota.StateUpdate, b1.ID)

	out := h.ok("halt", "--label", "20261105.1")
	if strings.Count(out, "-> halted") != 3 {
		t.Fatalf("halt --label output:\n%s", out)
	}
	for _, k := range [][3]string{{"beta", "android", rt}, {"production", "android", rt}, {"production", "ios", "rt-ios"}} {
		p := h.pointer(k[0], k[1], k[2])
		if p.State != ota.StateHalted || p.Update == "" {
			t.Fatalf("%v: %+v", k, p)
		}
	}
	if p := h.pointer("production", "android", "rt-old"); p.State != ota.StateUpdate {
		t.Fatalf("another label was halted: %+v", p)
	}
	h.fail("halt", "--label", "20990101.1")
	out = h.ok("halt", "--all")
	if strings.Count(out, "-> halted") != 1 || h.pointer("production", "android", "rt-old").State != ota.StateHalted {
		t.Fatalf("halt --all output:\n%s", out)
	}
	h.fail("halt")
	h.fail("halt", "--all", "--label", "x")
}

func TestRollbackEmbedded(t *testing.T) {
	h := newHarness(t)
	h.f.RegisterRuntime(t, ota.PlatformAndroid, rt, otatest.NewUUID(), 2)
	bad := h.f.AddUpdateWith(t, ota.PlatformAndroid, rt, otatest.UpdateOptions{Label: "20261105.2"})
	other := h.f.AddUpdateWith(t, ota.PlatformAndroid, "rt-2", otatest.UpdateOptions{Label: "20261105.1"})
	h.f.Point(t, "beta", "android", rt, ota.StateUpdate, bad.ID)
	h.f.Point(t, "production", "android", rt, ota.StateHalted, bad.ID)
	h.f.Point(t, "production", "android", "rt-2", ota.StateUpdate, other.ID)

	h.stdin = "no\n"
	if errs := h.fail("rollback-embedded", "--label", "20261105.2"); !strings.Contains(errs, "not confirmed") {
		t.Fatalf("without yes: %s", errs)
	}
	if h.pointer("beta", "android", rt).State != ota.StateUpdate {
		t.Fatal("changed without yes")
	}
	h.stdin = "yes\n"
	out := h.ok("rollback-embedded", "--label", "20261105.2")
	if !strings.Contains(out, "the census is empty") || strings.Count(out, "-> rollback") != 2 {
		t.Fatalf("output:\n%s", out)
	}
	for _, ch := range []string{"beta", "production"} {
		if p := h.pointer(ch, "android", rt); p.State != ota.StateRollback || p.Update != bad.ID {
			t.Fatalf("%s: %+v", ch, p)
		}
	}
	if p := h.pointer("production", "android", "rt-2"); p.State != ota.StateUpdate {
		t.Fatalf("another label rolled back: %+v", p)
	}
	ids, _ := h.s.Revoked()
	if len(ids) != 1 || ids[0] != bad.ID {
		t.Fatalf("revoked %v", ids)
	}
	// The server serves the directive of the revoked update.
	sel, err := h.s.SelectAt(&ota.ManifestRequest{Platform: "android", Runtime: rt, Channel: "production",
		CurrentUpdateID: bad.ID, EmbeddedUpdateID: otatest.NewUUID()}, h.now)
	if err != nil || sel.Parts == nil || sel.Parts[0].Name != "directive" {
		t.Fatalf("select after rollback: %+v, %v", sel, err)
	}
	h.fail("rollback-embedded", "--label", "20261105.2") // nothing left in update or halted

	// A directive signed by a leaf that expires within 30 days is refused.
	expiring := h.f.AddLeaf(t, "old", h.now.Add(-380*24*time.Hour))
	ue := h.f.AddUpdateWith(t, ota.PlatformAndroid, "rt-3", otatest.UpdateOptions{Label: "20261105.3", Leaf: expiring, LeafKey: h.f.LeafKey})
	h.f.Point(t, "beta", "android", "rt-3", ota.StateUpdate, ue.ID)
	if errs := h.fail("rollback-embedded", "--label", "20261105.3"); !strings.Contains(errs, "within 30 days") {
		t.Fatalf("expiring directive leaf: %s", errs)
	}
}

func TestEmergencyAcceptsOnlyThreeWords(t *testing.T) {
	h := newHarness(t)
	u := h.f.AddUpdate(t, ota.PlatformAndroid, rt)
	h.f.Point(t, "production", "android", rt, ota.StateUpdate, u.ID)
	for _, cmd := range []string{"", "ls", "halt --label x", "status; rm -rf ~", "bash"} {
		h.env["SSH_ORIGINAL_COMMAND"] = cmd
		if errs := h.fail("emergency"); !strings.Contains(errs, "only one of: halt, rollback-embedded, status") {
			t.Fatalf("%q: %s", cmd, errs)
		}
		if a := h.lastAction(); a["action"] != "emergency-refused" || a["via"] != "emergency" {
			t.Fatalf("%q: log %v", cmd, a)
		}
	}
	if h.pointer("production", "android", rt).State != ota.StateUpdate {
		t.Fatal("a refused command changed a pointer")
	}
	h.env["SSH_ORIGINAL_COMMAND"] = "status"
	if out := h.ok("emergency"); !strings.Contains(out, "production/android/"+rt+": update "+u.ID) {
		t.Fatalf("status:\n%s", out)
	}
	h.env["SSH_ORIGINAL_COMMAND"] = "halt"
	h.ok("emergency")
	if p := h.pointer("production", "android", rt); p.State != ota.StateHalted || p.Update != u.ID {
		t.Fatalf("after halt: %+v", p)
	}
	if a := h.lastAction(); a["action"] != "halt" || a["via"] != "emergency" {
		t.Fatalf("log %v", a)
	}
	h.env["SSH_ORIGINAL_COMMAND"] = "rollback-embedded"
	h.stdin = "yes\n"
	h.ok("emergency")
	if p := h.pointer("production", "android", rt); p.State != ota.StateRollback {
		t.Fatalf("after rollback-embedded: %+v", p)
	}
}

func TestRegisterRuntimeAndPolicy(t *testing.T) {
	h := newHarness(t)
	reg := func(build, embedded string) (int, string, string) {
		return h.run("register-runtime", "--platform", "android", "--runtime", rt, "--build", build, "--version", "1."+build+".0",
			"--embedded-update-id", embedded, "--commit-time", "2026-11-01T10:00:00.000Z", "--commit", "abcdef1")
	}
	e2 := otatest.NewUUID()
	if code, out, errs := reg("2", e2); code != 0 {
		t.Fatalf("register: %s%s", out, errs)
	}
	if code, out, _ := reg("2", e2); code != 0 || !strings.Contains(out, "already registered") {
		t.Fatalf("same build again: %d %s", code, out)
	}
	if code, _, errs := reg("2", otatest.NewUUID()); code == 0 || !strings.Contains(errs, "never reused") {
		t.Fatalf("build 2 with another binary: %s", errs)
	}
	if code, _, _ := reg("1", otatest.NewUUID()); code == 0 {
		t.Fatal("a lower build was registered")
	}
	info, err := h.s.ReadRuntime("android", rt)
	if err != nil || len(info.Builds) != 1 || info.Builds[0].EmbeddedUpdateID != e2 {
		t.Fatalf("runtime %+v, %v", info, err)
	}

	if errs := h.fail("policy", "--platform", "android", "--min-build", "3", "--yes"); !strings.Contains(errs, "above the highest registered android build, 2") {
		t.Fatalf("min_build above the highest build: %s", errs)
	}
	if errs := h.fail("policy", "--platform", "android", "--latest-build", "2"); !strings.Contains(errs, "holds no build 2") {
		t.Fatalf("latest without a stored build: %s", errs)
	}
	h.write("native/android/AlexMessages-1.2.0-2.apk", []byte("apk bytes"))
	h.ok("policy", "--platform", "android", "--latest-build", "2", "--notes", "First")
	h.stdin = "no\n"
	h.fail("policy", "--platform", "android", "--min-build", "2", "--hard-gate")
	h.stdin = "yes\n"
	h.ok("policy", "--platform", "android", "--min-build", "2", "--hard-gate")
	pol, err := h.s.ReadPolicy()
	if err != nil {
		t.Fatal(err)
	}
	l := pol.Android.Latest
	if pol.Android.MinBuild != 2 || !pol.Android.HardGate || l == nil || l.Build != 2 || l.Size != 9 || l.Notes != "First" ||
		l.URL != "https://messages.alexanderjia.com/app/android/AlexMessages-1.2.0-2.apk" || pol.IOS.Latest != nil {
		t.Fatalf("policy %+v latest %+v", pol, l)
	}
	if out := h.ok("policy"); !strings.Contains(out, `"min_build": 2`) {
		t.Fatalf("policy shows:\n%s", out)
	}
}

func zipWith(t *testing.T, p, name string, content []byte) {
	t.Helper()
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("AndroidManifest.xml")
	w.Write([]byte("x"))
	w, _ = zw.Create(name)
	w.Write(content)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func TestTestUnsigned(t *testing.T) {
	h := newHarness(t)
	embedded := otatest.NewUUID()
	h.f.RegisterRuntime(t, ota.PlatformAndroid, rt, embedded, 2)
	h.f.RegisterRuntime(t, ota.PlatformIOS, "rt-ios", otatest.NewUUID(), 2)
	bundle := []byte("hermes bytecode of the built-in update")
	os.MkdirAll(filepath.Join(h.f.Dir, "native", "android"), 0o755)
	os.MkdirAll(filepath.Join(h.f.Dir, "native", "ios"), 0o755)
	zipWith(t, filepath.Join(h.f.Dir, "native", "android", "AlexMessages-1.2.0-2.apk"), "assets/index.android.bundle", bundle)
	zipWith(t, filepath.Join(h.f.Dir, "native", "ios", "AlexMessages-1.2.0-2.ipa"), "Payload/AlexMessages.app/main.jsbundle", bundle)
	u := h.f.AddUpdate(t, ota.PlatformAndroid, rt)
	h.f.Point(t, "beta", "android", rt, ota.StateUpdate, u.ID)

	h.fail("test-unsigned", "--channel", "production")
	h.ok("test-unsigned", "--channel", "beta", "--minutes", "10")
	p := h.pointer("beta", "android", rt)
	if p.State != ota.StateTestUnsigned || p.Previous == nil || p.Previous.Update != u.ID ||
		p.Until != h.now.Add(10*time.Minute).UTC().Format(time.RFC3339) {
		t.Fatalf("pointer %+v", p)
	}
	if ip := h.pointer("beta", "ios", "rt-ios"); ip == nil || ip.State != ota.StateTestUnsigned || ip.Previous != nil {
		t.Fatalf("ios pointer %+v", ip)
	}
	req := &ota.ManifestRequest{Platform: "android", Runtime: rt, Channel: "beta", CurrentUpdateID: embedded, EmbeddedUpdateID: embedded}
	sel, err := h.s.SelectAt(req, h.now)
	if err != nil || len(sel.Parts) != 1 || sel.Parts[0].Signature != "" {
		t.Fatalf("select %+v, %v", sel, err)
	}
	m, err := ota.ParseUpdateManifest(sel.Parts[0].Body)
	if err != nil {
		t.Fatal(err)
	}
	if m.LaunchAsset.Hash != ota.AssetHash(bundle) || !strings.HasPrefix(m.LaunchAsset.URL, "https://messages.alexanderjia.com/ota/assets/") {
		t.Fatalf("manifest %s", sel.Parts[0].Body)
	}
	if err := h.s.CheckAssets(m); err != nil {
		t.Fatal(err)
	}
	h.ok("test-unsigned", "--clear")
	if p := h.pointer("beta", "android", rt); p.State != ota.StateUpdate || p.Update != u.ID {
		t.Fatalf("after clear: %+v", p)
	}
	if p := h.pointer("beta", "ios", "rt-ios"); p != nil {
		t.Fatalf("ios pointer after clear: %+v", p)
	}
}

func TestRepairAssetAndRestorePointer(t *testing.T) {
	h := newHarness(t)
	u := h.f.AddUpdate(t, ota.PlatformAndroid, rt)
	h.write("assets/"+u.AssetName, []byte("damaged"))
	h.stdin = "wrong bytes"
	h.fail("repair-asset", "--asset", u.AssetName)
	h.stdin = string(u.Asset)
	if out := h.ok("repair-asset", "--asset", u.AssetName); !strings.Contains(out, "was damaged and is now intact") {
		t.Fatalf("repair: %s", out)
	}
	if err := ota.CheckAssetFile(filepath.Join(h.f.Dir, "assets", u.AssetName), u.AssetName); err != nil {
		t.Fatal(err)
	}

	pj := `{"state":"update","update":"` + u.ID + `","since":"2026-11-05T10:00:00Z"}`
	h.ok("restore-pointer", "--channel", "production", "--platform", "android", "--runtime", rt, "--pointer", pj)
	if p := h.pointer("production", "android", rt); p.Update != u.ID || p.Since != "2026-11-05T10:00:00Z" {
		t.Fatalf("restored %+v", p)
	}
	h.fail("restore-pointer", "--channel", "production", "--platform", "ios", "--runtime", rt, "--pointer", pj)
	h.ok("revoke", u.ID)
	h.fail("restore-pointer", "--channel", "beta", "--platform", "android", "--runtime", rt, "--pointer", pj)
	h.ok("restore-pointer", "--channel", "production", "--platform", "android", "--runtime", rt, "--none")
	if p := h.pointer("production", "android", rt); p != nil {
		t.Fatalf("pointer still there: %+v", p)
	}
}

func TestParseInterleaved(t *testing.T) {
	for _, c := range []struct {
		args       []string
		pos        []string
		note       string
		yes        bool
		wantErrSub string
	}{
		{args: []string{"a", "--note", "x", "b", "--yes"}, pos: []string{"a", "b"}, note: "x", yes: true},
		{args: []string{"--note=-n", "--", "-a", "--yes"}, pos: []string{"-a", "--yes"}, note: "-n"},
		{args: []string{"--note", "--", "a", "--yes"}, pos: []string{"a"}, note: "--", yes: true},
		{args: []string{"--yes", "--", "--"}, pos: []string{"--"}, yes: true},
		{args: []string{"-SphGWz.js"}, wantErrSub: "not defined: -SphGWz.js"},
	} {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		note := fs.String("note", "", "")
		yes := fs.Bool("yes", false, "")
		pos, err := parseInterleaved(fs, c.args)
		if c.wantErrSub != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErrSub) {
				t.Errorf("%q: error %v", c.args, err)
			}
			continue
		}
		if err != nil || strings.Join(pos, "|") != strings.Join(c.pos, "|") || *note != c.note || *yes != c.yes {
			t.Errorf("%q: pos %q note %q yes %v err %v", c.args, pos, *note, *yes, err)
		}
	}
}

func TestInitAndStoreLocation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ota")
	var out, errb bytes.Buffer
	e := &env{stdin: strings.NewReader(""), stdout: &out, stderr: &errb, now: time.Now, getenv: func(string) string { return "" }}
	// Before init every command but init and version exits 3, which
	// otapublish (otactlNoStore) tells apart from every other failure.
	for _, cmd := range []string{"status", "doctor", "runtimes"} {
		errb.Reset()
		if code := run([]string{cmd, "--ota", dir}, e); code != 3 || !strings.Contains(errb.String(), "run otactl init") {
			t.Fatalf("%s before init: %d %s", cmd, code, errb.String())
		}
	}
	if code := run([]string{"version", "--ota", dir, "--bin", t.TempDir()}, e); code != 0 {
		t.Fatalf("version before init: %d %s", code, errb.String())
	}
	if code := run([]string{"init", "--ota", dir}, e); code != 0 {
		t.Fatalf("init: %s", errb.String())
	}
	for _, d := range []string{"FORMAT", "certs", "assets", "updates", "runtimes", "channels", "native/android", "incoming", "log/actions.log"} {
		if !pathExists(t, filepath.Join(dir, d)) {
			t.Fatalf("%s missing after init", d)
		}
	}
	if code := run([]string{"init", "--ota", dir}, e); code != 0 || !strings.Contains(out.String(), "already an update store") {
		t.Fatalf("init again: %s", out.String())
	}
	// A store this binary cannot read is an error, not a missing store.
	if err := os.WriteFile(filepath.Join(dir, "FORMAT"), []byte("99\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	errb.Reset()
	if code := run([]string{"status", "--ota", dir}, e); code != 1 || !strings.Contains(errb.String(), `format "99" is not supported`) {
		t.Fatalf("status of an unreadable store: %d %s", code, errb.String())
	}

	for _, c := range []struct{ exe, otaEnv, want string }{
		{"/home/alexander/alexmessage/bin/otactl", "", "/home/alexander/alexmessage/data/ota"},
		{"/home/alexander/alexmessage/bin/otactl", "/srv/ota", "/srv/ota"},
		{"/tmp/go-build123/exe/otactl", "", "data/ota"},
	} {
		a := newApp(&env{exe: c.exe, getenv: func(k string) string {
			if k == "OTA_DIR" {
				return c.otaEnv
			}
			return ""
		}}, options{})
		if a.store.Dir != c.want {
			t.Errorf("exe %s, OTA_DIR %q: store %s, want %s", c.exe, c.otaEnv, a.store.Dir, c.want)
		}
	}
}
