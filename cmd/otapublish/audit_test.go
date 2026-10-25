package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"alexmessage/internal/ota"
)

// archiveRegisteredRuntimes copies the fake Pi's runtime records into the
// archive, as otapublish native does after it registers a build (the
// harness registers its builds with otactl directly).
func (h *harness) archiveRegisteredRuntimes() {
	h.t.Helper()
	for _, platform := range []string{"android", "ios"} {
		dir := filepath.Join(h.store, "runtimes", platform)
		entries, err := os.ReadDir(dir)
		if err != nil {
			h.t.Fatal(err)
		}
		for _, e := range entries {
			h.write(filepath.Join(h.archive, "ota", "runtimes", platform, e.Name()), string(readFile(h.t, filepath.Join(dir, e.Name()))))
		}
	}
}

// storeAsset is the name of one asset in the fake Pi's store.
func (h *harness) storeAsset() string {
	h.t.Helper()
	entries, err := os.ReadDir(filepath.Join(h.store, "assets"))
	if err != nil || len(entries) == 0 {
		h.t.Fatalf("the store has no assets (%v)", err)
	}
	return entries[0].Name()
}

func TestAudit(t *testing.T) {
	h := newHarness(t)
	h.setup()
	h.ok("ota", "--notes", "First")
	h.archiveRegisteredRuntimes()
	mustContain(t, h.ok("audit"), "No differences: the Pi matches the Mac archive.",
		"Checked 2 certificate(s), 4 asset(s), 2 update(s), 0+0 native build(s), 2 runtime(s), 2 pointer(s).")
	ios := h.pointer("beta", "ios", rtIOS)

	// An emergency halt on the Pi, an IPA replaced on the Pi, and a lost
	// asset.
	h.otactl("halt", "--all")
	h.write(filepath.Join(h.archive, "ota", "native", "ios", "AlexMessages-1.2.0-2.ipa"), "the IPA otapublish native built")
	h.write(filepath.Join(h.store, "native", "ios", "AlexMessages-1.2.0-2.ipa"), "a replaced IPA")
	lost := h.storeAsset()
	if err := os.Remove(filepath.Join(h.store, "assets", lost)); err != nil {
		t.Fatal(err)
	}
	out := h.fail("audit")
	mustContain(t, out,
		"DIFFERENCE: pointer beta/ios/"+rtIOS+": the Pi has halted "+ios.Update,
		"but the Mac set update "+ios.Update+" since "+ios.Since+": a halt the Mac did not set",
		"DIFFERENCE: native/ios/AlexMessages-1.2.0-2.ipa: the Pi's SHA-256 is "+sha256Hex([]byte("a replaced IPA")),
		"do NOT install this file from /app",
		"DIFFERENCE: assets/"+lost+": in the archive, missing on the Pi",
		"otapublish audit: 4 difference(s) between the Mac archive and the Pi")

	// --record stores the halt, after a yes; never the replaced IPA or the
	// lost asset.
	h.stdin = "no\n"
	mustContain(t, h.fail("audit", "--record"), "not confirmed; nothing was recorded")
	h.stdin = "yes\n"
	out = h.fail("audit", "--record")
	mustContain(t, out, "Recorded 2 pointer(s), 0 revoked id(s) and no policy change.", "Still different after recording",
		"otapublish audit: 2 difference(s)")
	log := h.pointersLog()
	if last := log[len(log)-1]; last.Action != "record" || last.New == nil || last.New.State != ota.StateHalted {
		t.Fatalf("pointers.log ends with %+v", last)
	}
	out = h.fail("audit")
	mustContain(t, out, "2 difference(s)")
	if strings.Contains(out, "DIFFERENCE: pointer") {
		t.Fatalf("the recorded halt is still a difference:\n%s", out)
	}

	// The replaced IPA is deleted on the Pi; restore-pi uploads the
	// archived IPA and the lost asset.
	if err := os.Remove(filepath.Join(h.store, "native", "ios", "AlexMessages-1.2.0-2.ipa")); err != nil {
		t.Fatal(err)
	}
	h.stdin = "yes\n"
	mustContain(t, h.ok("restore-pi"), "upload 2 file(s) the Pi lacks", "Restored.")
	mustContain(t, h.ok("audit"), "No differences")

	// Something the Mac never made, on the Pi.
	h.write(filepath.Join(h.store, "certs", "leaf-209901.pem"), "not from the Mac")
	mustContain(t, h.fail("audit"), "certs/leaf-209901.pem: on the Pi only; the Mac never issued it")
}

func TestRestorePi(t *testing.T) {
	h := newHarness(t)
	h.setup()
	h.ok("ota", "--notes", "First")
	first := h.pointer("beta", "ios", rtIOS)
	h.now = h.now.Add(time.Minute)
	h.ok("ota", "--notes", "Second")
	h.archiveRegisteredRuntimes()

	// A native build, its policy and a revoked update, set on the Pi and
	// recorded on the Mac.
	apk := filepath.Join("native", "android", "AlexMessages-1.2.0-2.apk")
	h.write(filepath.Join(h.store, apk), "the APK")
	h.write(filepath.Join(h.archive, "ota", apk), "the APK")
	h.otactl("policy", "--platform", "android", "--latest-build", "2", "--notes", "Build two", "--base-url", h.url)
	h.otactl("policy", "--platform", "android", "--min-build", "2", "--yes")
	h.otactl("revoke", first.Update)
	h.stdin = "yes\n"
	mustContain(t, h.ok("audit", "--record"), "Recorded 0 pointer(s), 1 revoked id(s) and the policy.", "No differences")
	policy := readFile(t, filepath.Join(h.store, "native", "policy.json"))
	before := map[string]*ota.Pointer{}
	for _, platform := range []string{"ios", "android"} {
		rt := map[string]string{"ios": rtIOS, "android": rtAndroid}[platform]
		before[platform] = h.pointer("beta", platform, rt)
	}

	// The SD card is lost.
	if err := os.RemoveAll(h.store); err != nil {
		t.Fatal(err)
	}
	mustContain(t, h.fail("restore-pi"), "has no update store: run bin/otactl init on it first")
	h.otactl("init")
	h.stdin = "no\n"
	mustContain(t, h.fail("restore-pi"), "not confirmed; nothing was restored")
	if entries, _ := os.ReadDir(filepath.Join(h.store, "updates")); len(entries) != 0 {
		t.Fatal("a refused restore uploaded updates")
	}
	h.stdin = "yes\n"
	out := h.ok("restore-pi")
	mustContain(t, out, "register android build 2 for runtime "+rtAndroid, "register ios build 2 for runtime "+rtIOS,
		"revoke "+first.Update, "policy android: min_build 2, hard_gate false, latest build 2",
		"pointer beta/ios/"+rtIOS+": update "+before["ios"].Update, "ingested 2 certificate(s), 4 asset(s), 4 update(s), 1 native build(s)", "Restored.")
	for platform, want := range before {
		rt := map[string]string{"ios": rtIOS, "android": rtAndroid}[platform]
		if got := h.pointer("beta", platform, rt); !reflect.DeepEqual(got, want) {
			t.Fatalf("beta/%s pointer %+v, want %+v", platform, got, want)
		}
	}
	if got := readFile(t, filepath.Join(h.store, "native", "policy.json")); string(got) != string(policy) {
		t.Fatalf("policy.json:\n%s\nwant:\n%s", got, policy)
	}
	if revoked := readFile(t, filepath.Join(h.store, "revoked.txt")); !strings.Contains(string(revoked), first.Update) {
		t.Fatalf("revoked.txt:\n%s", revoked)
	}
	mustContain(t, h.ok("audit"), "No differences")
	mustContain(t, h.ok("restore-pi"), "Nothing to restore: the Pi lacks nothing that the archive has.")

	// A pointer the Pi changed is left as it is.
	h.otactl("halt", "--all")
	out = h.ok("restore-pi")
	mustContain(t, out, "pointer beta/ios/"+rtIOS+": the Pi has halted", "left as it is", "Nothing to restore")

	// A file the Pi holds with other bytes stops the restore.
	h.write(filepath.Join(h.store, apk), "another APK")
	mustContain(t, h.fail("restore-pi"), "holds files that differ from the archive: native/android/AlexMessages-1.2.0-2.apk")
}

// TestRestorePiExpiredLeaf: an update whose leaf expired is history; its
// pointer is not restored, and the restore says so.
func TestRestorePiExpiredLeaf(t *testing.T) {
	h := newHarness(t)
	h.setup()
	h.ok("ota", "--notes", "First")
	h.archiveRegisteredRuntimes()
	if err := os.RemoveAll(h.store); err != nil {
		t.Fatal(err)
	}
	h.otactl("init")
	h.now = h.now.Add(500 * 24 * time.Hour)
	h.stdin = "yes\n"
	out := h.fail("restore-pi")
	mustContain(t, out, "pointer beta/ios/"+rtIOS+": not restored; its update", "cannot be restored", "publish or republish instead",
		"2 pointer(s) were not restored")
	out = h.fail("audit")
	mustContain(t, out, "an expired leaf, not on the Pi", "its signatures no longer verify", "it is history only")
}

func TestCompareRuntimesIgnoreRegisteredAt(t *testing.T) {
	b := ota.RuntimeBuild{Build: 2, Version: "1.2.0", EmbeddedUpdateID: newUUID(), CommitTime: "2026-10-25T00:00:00.000Z", Commit: strings.Repeat("a", 40)}
	mine, theirs := b, b
	mine.RegisteredAt, theirs.RegisteredAt = "2026-10-25T01:00:00Z", "2027-01-01T00:00:00Z"
	s := &archiveState{runtimes: map[string]ota.RuntimeInfo{"android/rt": {Platform: "android", Runtime: "rt", Builds: []ota.RuntimeBuild{mine}}},
		pointers: map[string]*ota.Pointer{}}
	inv := &inventoryJSON{Runtimes: []ota.RuntimeInfo{{Platform: "android", Runtime: "rt", Builds: []ota.RuntimeBuild{theirs}}}}
	if r := compare(s, inv); len(r.diffs) != 0 {
		t.Fatalf("diffs: %q", r.diffs)
	}
	theirs.Commit = strings.Repeat("b", 40)
	inv.Runtimes[0].Builds[0] = theirs
	if r := compare(s, inv); len(r.diffs) != 1 || !strings.Contains(r.diffs[0], "runtimes/android/rt: the Pi registers builds 2") {
		t.Fatalf("diffs: %q", r.diffs)
	}
}

func TestParseRevoked(t *testing.T) {
	a, b := newUUID(), newUUID()
	got := parseRevoked([]byte("# a comment\n" + strings.ToUpper(b) + "  # rollback\n\n" + a + "\nnot-an-id\n"))
	want := []string{a, b}
	if want[0] > want[1] {
		want[0], want[1] = want[1], want[0]
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseRevoked = %q, want %q", got, want)
	}
	if _, err := json.Marshal(got); err != nil {
		t.Fatal(err)
	}
}
