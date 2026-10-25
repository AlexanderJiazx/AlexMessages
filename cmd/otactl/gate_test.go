package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"alexmessage/internal/db"
	"alexmessage/internal/ota"
	"alexmessage/internal/ota/census"
	"alexmessage/internal/ota/otatest"
)

// initDB creates the main app's database in the harness's data directory,
// with the server's own schema.
func (h *harness) initDB() {
	h.t.Helper()
	db.BaseDir = h.data
	db.DataDir = h.data
	db.UploadRoot = filepath.Join(h.data, "uploads")
	db.AvatarRoot = filepath.Join(h.data, "avatars")
	db.DBPath = filepath.Join(h.data, "alexmessage.db")
	if err := db.InitDB(); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) user(name string, status string, admin bool) int {
	h.t.Helper()
	id, err := db.CreateUser(name, "hash", name, status, admin)
	if err != nil {
		h.t.Fatal(err)
	}
	return id
}

var sessions int

// session gives user uid a session whose X-AM-Client is client ("" for a
// web session).
func (h *harness) session(uid int, client string) {
	h.t.Helper()
	sessions++
	tok := fmt.Sprintf("token-%d", sessions)
	if err := db.CreateSession(tok, uid, h.now.Add(time.Hour).Unix(), "user"); err != nil {
		h.t.Fatal(err)
	}
	if client != "" {
		if err := db.RecordSessionClient(tok, client, h.now.Unix()); err != nil {
			h.t.Fatal(err)
		}
	}
}

func (h *harness) report(uid *int, username, event, updateID string) {
	h.t.Helper()
	_, err := db.InsertDebugEvent(db.DebugEvent{TsMs: h.now.UnixMilli(), App: "messages", Level: "error", UserID: uid,
		Username: username, Event: event, Context: `{"updateId":"` + updateID + `"}`})
	if err != nil {
		h.t.Fatal(err)
	}
}

func appClient(platform, runtime, update string) string {
	return "v=1;platform=" + platform + ";version=1.2.0;build=2;runtime=" + runtime + ";update=" + update + ";channel=beta"
}

func TestPromoteGate(t *testing.T) {
	h := newHarness(t)
	h.initDB()
	h.f.RegisterRuntime(t, ota.PlatformAndroid, rt, otatest.NewUUID(), 2)
	h.f.RegisterRuntime(t, ota.PlatformIOS, "rt-ios", otatest.NewUUID(), 2)
	o := otatest.UpdateOptions{Label: "20261105.1", CreatedAt: h.f.Now.Add(10 * time.Second)}
	ua := h.f.AddUpdateWith(t, ota.PlatformAndroid, rt, o)
	ui := h.f.AddUpdateWith(t, ota.PlatformIOS, "rt-ios", o)
	h.ok("activate", "--channel", "beta", "--update", ua.ID, "--update", ui.ID)

	noEvidence := func(errs string) {
		t.Helper()
		for _, p := range []string{"android", "ios"} {
			if !strings.Contains(errs, p+": no approved admin's app has reported running update") {
				t.Fatalf("%s evidence was accepted:\n%s", p, errs)
			}
		}
	}
	noEvidence(h.fail("promote", "--label", "20261105.1"))

	admin := h.user("boss", "approved", true)
	pal := h.user("pal", "approved", false)
	pending := h.user("newbie", "pending", true)
	h.session(pal, appClient("android", rt, ua.ID))                  // not an admin
	h.session(pending, appClient("android", rt, ua.ID))              // not approved
	h.session(admin, "v=1;platform=android;build=2;component=ring")  // the ring service
	h.session(admin, appClient("ios", "rt-ios", ua.ID))              // the other platform
	h.session(admin, "v=1;platform=android;build=2;update=not-a-id") // malformed
	h.session(admin, "")                                             // the web client
	noEvidence(h.fail("promote", "--label", "20261105.1"))
	if p := h.pointer("production", "android", rt); p != nil {
		t.Fatalf("promoted without evidence: %+v", p)
	}

	h.session(admin, appClient("android", rt, ua.ID))
	h.session(admin, appClient("ios", "rt-ios", ui.ID))
	h.report(nil, "", "ota.crash", ua.ID)         // unauthenticated: not counted
	h.report(&pal, "pal", "ota.error", ui.ID+"x") // another update
	h.report(&pal, "pal", "ota.warning", ua.ID)   // another event
	h.ok("promote", "--label", "20261105.1")
	if p := h.pointer("production", "android", rt); p == nil || p.Update != ua.ID {
		t.Fatalf("android production %+v", p)
	}
	if p := h.pointer("production", "ios", "rt-ios"); p == nil || p.Update != ui.ID {
		t.Fatalf("ios production %+v", p)
	}
	a := h.lastAction()
	ev, _ := json.Marshal(a["evidence"])
	if a["action"] != "promote" || !strings.Contains(string(ev), `"admins":["boss"]`) {
		t.Fatalf("log %v", a)
	}

	o2 := otatest.UpdateOptions{Label: "20261105.2", CreatedAt: h.f.Now.Add(20 * time.Second)}
	ua2 := h.f.AddUpdateWith(t, ota.PlatformAndroid, rt, o2)
	h.ok("activate", "--channel", "beta", "--update", ua2.ID)
	h.session(admin, appClient("android", rt, ua2.ID))
	h.report(&pal, "pal", "ota.crash", strings.ToUpper(ua2.ID))
	if errs := h.fail("promote", "--label", "20261105.2"); !strings.Contains(errs, "failure report ota.crash by pal") {
		t.Fatalf("a crash report did not block:\n%s", errs)
	}
	if p := h.pointer("production", "android", rt); p.Update != ua.ID {
		t.Fatalf("production changed: %+v", p)
	}
	h.ok("promote", "--label", "20261105.2", "--force")
	if p := h.pointer("production", "android", rt); p.Update != ua2.ID {
		t.Fatalf("--force did not promote: %+v", p)
	}
	if errs := h.fail("promote", "--label", "20261105.9"); !strings.Contains(errs, "no beta pointer") {
		t.Fatalf("unknown label: %s", errs)
	}
}

func TestPromoteWithoutDatabase(t *testing.T) {
	h := newHarness(t)
	h.f.RegisterRuntime(t, ota.PlatformAndroid, rt, otatest.NewUUID(), 2)
	u := h.f.AddUpdateWith(t, ota.PlatformAndroid, rt, otatest.UpdateOptions{Label: "20261105.1", CreatedAt: h.f.Now.Add(time.Second)})
	h.ok("activate", "--channel", "beta", "--update", u.ID)
	if errs := h.fail("promote", "--label", "20261105.1"); !strings.Contains(errs, "alexmessage.db") {
		t.Fatalf("a missing database: %s", errs)
	}
	if p := h.pointer("production", "android", rt); p != nil {
		t.Fatalf("promoted: %+v", p)
	}
}

func TestCensusAndStatus(t *testing.T) {
	h := newHarness(t)
	h.initDB()
	emb := otatest.NewUUID()
	h.f.RegisterRuntime(t, ota.PlatformAndroid, rt, emb, 2)
	u := h.f.AddUpdateWith(t, ota.PlatformAndroid, rt, otatest.UpdateOptions{Label: "20261105.1"})
	h.f.Point(t, "production", "android", rt, ota.StateUpdate, u.ID)
	pal := h.user("pal", "approved", false)
	h.user("quiet", "approved", false)
	h.session(pal, appClient("android", rt, u.ID))
	h.session(pal, "")

	if out := h.ok("status"); !strings.Contains(out, "Census: 0 install(s)") {
		t.Fatalf("status without a census:\n%s", out)
	}
	cdb, err := census.Shared(filepath.Join(h.f.Dir, census.FileName), true)
	if err != nil {
		t.Fatal(err)
	}
	yes := func() (bool, error) { return true, nil }
	for _, r := range []census.Report{
		{ClientID: "client-1", Platform: "android", Runtime: rt, CurrentUpdateID: u.ID, EmbeddedUpdateID: emb, Channel: "production", UserID: int64(pal)},
		{ClientID: "client-2", Platform: "android", Runtime: rt, CurrentUpdateID: emb, EmbeddedUpdateID: emb, Channel: "production", FailedUpdateIDs: []string{u.ID}},
	} {
		if _, err := cdb.Record(r, yes, h.now); err != nil {
			t.Fatal(err)
		}
	}

	var c struct {
		Installs []censusInstall `json:"installs"`
		Users    []userApps      `json:"users"`
	}
	if err := json.Unmarshal([]byte(h.ok("census", "--json")), &c); err != nil {
		t.Fatal(err)
	}
	if len(c.Installs) != 2 || len(c.Users) != 2 {
		t.Fatalf("census %+v", c)
	}
	for _, in := range c.Installs {
		want := map[string]string{"client-1": "20261105.1 (" + u.ID[:8] + ")", "client-2": "built-in"}[in.ClientID]
		if in.Running != want || in.Build != 2 || in.Version != "1.2.0" {
			t.Fatalf("install %+v", in)
		}
		if (in.ClientID == "client-1") != (in.Username == "pal") {
			t.Fatalf("install %s user %q", in.ClientID, in.Username)
		}
	}
	for _, us := range c.Users {
		switch us.Username {
		case "pal":
			if !us.HasClientHeader || us.CensusInstalls != 1 || us.SessionsWithoutClient != 1 || len(us.AppSessions) != 1 {
				t.Fatalf("pal %+v", us)
			}
		case "quiet":
			if us.HasClientHeader || us.CensusInstalls != 0 {
				t.Fatalf("quiet %+v", us)
			}
		}
	}
	if out := h.ok("census"); !strings.Contains(out, "quiet: NO app session with X-AM-Client") {
		t.Fatalf("census text:\n%s", out)
	}

	h.ok("revoke", u.ID)
	var st struct {
		Census struct {
			Installs  int              `json:"installs"`
			ByRuntime []runtimeSummary `json:"by_runtime"`
		} `json:"census"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(h.ok("status", "--json")), &st); err != nil {
		t.Fatal(err)
	}
	if st.Census.Installs != 2 || len(st.Census.ByRuntime) != 1 || st.Census.ByRuntime[0].Failed != 1 ||
		st.Census.ByRuntime[0].Running["built-in"] != 1 || len(st.Warnings) != 1 || !strings.Contains(st.Warnings[0], "serves revoked update") {
		t.Fatalf("status %+v", st)
	}
	if out := h.ok("runtimes"); !strings.Contains(out, "build 2 (1.2.0), embedded update "+emb) {
		t.Fatalf("runtimes:\n%s", out)
	}
}

func TestDoctor(t *testing.T) {
	h := newHarness(t)
	h.f.RegisterRuntime(t, ota.PlatformAndroid, rt, otatest.NewUUID(), 2)
	u := h.f.AddUpdate(t, ota.PlatformAndroid, rt)
	h.f.Point(t, "production", "android", rt, ota.StateUpdate, u.ID)
	report := func(wantCode int) doctorReport {
		t.Helper()
		code, out, errs := h.run("doctor", "--json")
		if code != wantCode {
			t.Fatalf("doctor exit %d, want %d:\n%s%s", code, wantCode, out, errs)
		}
		var r doctorReport
		if err := json.Unmarshal([]byte(out), &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	has := func(list []string, sub string) bool {
		for _, s := range list {
			if strings.Contains(s, sub) {
				return true
			}
		}
		return false
	}
	r := report(0)
	if len(r.Errors) != 0 || len(r.Warnings) != 0 || !has(r.OK, "signatures verify") {
		t.Fatalf("a healthy store: %+v", r)
	}

	h.write("incoming/left-over/assets/x", []byte("x"))
	h.f.Point(t, "beta", "android", "rt-unregistered", ota.StateHalted, "")
	r = report(0)
	if !has(r.Warnings, "incoming/ holds left-over") || !has(r.Warnings, "rt-unregistered is not registered") {
		t.Fatalf("warnings %+v", r)
	}

	expiring := h.f.AddLeaf(t, "old", h.now.Add(-380*24*time.Hour))
	ue := h.f.AddUpdateWith(t, ota.PlatformAndroid, "rt-2", otatest.UpdateOptions{Leaf: expiring, LeafKey: h.f.LeafKey})
	h.f.RegisterRuntime(t, ota.PlatformAndroid, "rt-2", otatest.NewUUID(), 3)
	h.f.Point(t, "production", "android", "rt-2", ota.StateUpdate, ue.ID)
	h.write("assets/"+u.AssetName, []byte("damaged"))
	h.write("native/policy.json", []byte(`{"android":{"min_build":0,"hard_gate":false,"latest":{"version":"1.2.0","build":2,`+
		`"url":"https://messages.alexanderjia.com/app/android/AlexMessages-1.2.0-2.apk","sha256":"`+strings.Repeat("0", 64)+`","size":3,"notes":""}}}`))
	r = report(1)
	for _, want := range []string{"expires at", "does not hash to its name", "AlexMessages-1.2.0-2.apk"} {
		if !has(r.Errors, want) {
			t.Fatalf("no error with %q: %+v", want, r.Errors)
		}
	}
}

func TestInventory(t *testing.T) {
	h := newHarness(t)
	u := h.f.AddUpdate(t, ota.PlatformIOS, rt)
	h.f.Point(t, "beta", "ios", rt, ota.StateUpdate, u.ID)
	var inv inventory
	if err := json.Unmarshal([]byte(h.ok("inventory", "--json")), &inv); err != nil {
		t.Fatal(err)
	}
	sum, _ := hashFile(filepath.Join(h.f.Dir, "updates", u.ID, "manifest.json"))
	if inv.Updates[u.ID]["manifest.json"] != sum || inv.Assets[u.AssetName] != int64(len(u.Asset)) ||
		len(inv.Pointers) != 1 || inv.Certs["ota-root-2026.pem"] == "" {
		t.Fatalf("inventory %+v", inv)
	}
}

func TestVersionReadsTheCommit(t *testing.T) {
	rev := "c4600167ac26c68c8e4a7073c7584be9deaf8c6f"
	for _, c := range []struct {
		settings []debug.BuildSetting
		want     string
	}{
		// -trimpath: Go records no -ldflags, only the VCS stamp.
		{[]debug.BuildSetting{{Key: "-trimpath", Value: "true"}, {Key: "vcs.revision", Value: rev}}, rev},
		{[]debug.BuildSetting{{Key: "-ldflags", Value: "-s -w -X main.commit=abc1234"}, {Key: "vcs.revision", Value: rev}}, "abc1234"},
		{[]debug.BuildSetting{{Key: "-ldflags", Value: "-X=main.commit=abc1234"}}, "abc1234"},
		{nil, "unknown"},
	} {
		if v := versionOf(&debug.BuildInfo{Settings: c.settings}); v.Commit != c.want {
			t.Errorf("%v: commit %s, want %s", c.settings, v.Commit, c.want)
		}
	}

	if testing.Short() {
		t.Skip("builds a binary")
	}
	head, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		t.Skip("not in a git checkout")
	}
	sha := strings.TrimSpace(string(head))
	dir := t.TempDir()
	// The Deploy workflow's flags, for the host platform.
	build := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w -X main.commit="+sha, "-o", filepath.Join(dir, "server"), ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	v := readBinaryVersion(filepath.Join(dir, "server"))
	if v.VCSRevision == "" {
		t.Skip("go build stamped no vcs.revision here")
	}
	if v.Commit != sha {
		t.Fatalf("commit %s, want %s", v.Commit, sha)
	}
	h := newHarness(t)
	out := h.ok("version", "--bin", dir, "--json")
	var vs map[string]binaryVersion
	if err := json.Unmarshal([]byte(out), &vs); err != nil {
		t.Fatal(err)
	}
	if vs["server"].Commit != sha || vs["admin"].Error == "" || vs["otactl"].Commit == "" {
		t.Fatalf("version %s", out)
	}
}
