package main

import (
	"archive/zip"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"alexmessage/internal/ota"
	"alexmessage/internal/ota/sign"
)

// The native tests run `otapublish native` against fake build tools: the
// fake npx's `expo prebuild` writes a fake gradlew that copies $FAKE_APK
// (an APK made by the test encoders) into Gradle's output directory, the
// fake xcodebuild copies $FAKE_APP_DIR into the archive, and the fake
// Android SDK's zipalign copies while its apksigner checks the keystore
// password on stdin and prints $FAKE_SIGNER as the signer. cp and zip are
// the real tools, so the IPA is made exactly as the recipe says.

const fakeGradlew = `#!/bin/sh
printf 'gradlew %s [JAVA_HOME=%s ANDROID_HOME=%s]\n' "$*" "$JAVA_HOME" "$ANDROID_HOME" >> "$FAKE_LOG"
out=app/build/outputs/apk/release
mkdir -p "$out" && cp "$FAKE_APK" "$out/${FAKE_APK_NAME:-app-release-unsigned.apk}"
`

const fakePod = `#!/bin/sh
printf 'pod %s (in %s)\n' "$*" "$(basename "$PWD")" >> "$FAKE_LOG"
`

const fakeXcodebuild = `#!/bin/sh
printf 'xcodebuild %s\n' "$*" >> "$FAKE_LOG"
archive=""
while [ $# -gt 0 ]; do [ "$1" = "-archivePath" ] && archive="$2"; shift; done
mkdir -p "$archive/Products/Applications" && cp -R "$FAKE_APP_DIR"/. "$archive/Products/Applications/"
`

const fakeZipalign = `#!/bin/sh
printf 'zipalign(%s) %s\n' "$(basename "$(dirname "$0")")" "$*" >> "$FAKE_LOG"
[ "$1 $2 $3" = "-p -f 4" ] || { echo "fake zipalign: unexpected $*" >&2; exit 1; }
cp "$4" "$5"
`

const fakeApksigner = `#!/bin/sh
printf 'apksigner(%s) %s\n' "$(basename "$(dirname "$0")")" "$*" >> "$FAKE_LOG"
case "$1" in
sign)
  shift
  out=""
  while [ $# -gt 1 ]; do [ "$1" = "--out" ] && out="$2"; shift; done
  printf 'Keystore password for signer #1: ' >&2
  read -r pw || { echo "apksigner: no password" >&2; exit 1; }
  if [ "$pw" != "$FAKE_KS_PASSWORD" ]; then echo "Failed to load signer \"signer #1\"" >&2; exit 1; fi
  echo "password accepted" >> "$FAKE_LOG"
  cp "$1" "$out" ;;
verify)
  echo "Verifies"
  echo "Verified using v1 scheme (JAR signing): false"
  echo "Verified using v2 scheme (APK Signature Scheme v2): ${FAKE_V2:-true}"
  echo "Verified using v3 scheme (APK Signature Scheme v3): ${FAKE_V2:-true}"
  echo "Verified using v4 scheme (APK Signature Scheme v4): false"
  echo "Verified for SourceStamp: false"
  echo "Number of signers: ${FAKE_SIGNERS:-1}"
  echo "Signer #1 certificate DN: CN=Fake"
  echo "Signer #1 certificate SHA-256 digest: $FAKE_SIGNER"
  echo "Signer #1 certificate SHA-1 digest: 0000000000000000000000000000000000000000" ;;
*) echo "fake apksigner: unexpected $*" >&2; exit 1 ;;
esac
`

const (
	fakeSigner     = "4a1f0c3be2d9a87e65b4c3d2e1f0a9b8c7d6e5f4a3b2c1d0e9f8a7b6c5d4e3f2"
	fakeKSPassword = "keystore password"
)

var (
	testRootOnce sync.Once
	testRootPEM  []byte
)

// testRoot is a root certificate for the release commit's
// mobile/certs/ota-root-2026.pem.
func testRoot(t *testing.T) []byte {
	testRootOnce.Do(func() {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		der, err := sign.NewRoot(key, "Alex Messages OTA test root", time.Now())
		if err != nil {
			panic(err)
		}
		testRootPEM = sign.EncodePEM(der)
	})
	return testRootPEM
}

type nativeHarness struct {
	*harness
	spec     *buildSpec
	sdk      string
	keystore string
	bin      string // the fake tools
}

func newNativeHarness(t *testing.T) *nativeHarness {
	t.Helper()
	if _, err := exec.LookPath("zip"); err != nil {
		t.Skip("the IPA recipe needs zip")
	}
	h := &nativeHarness{harness: newHarness(t)}
	// The clock check and the download check go to this server.
	h.serve()
	cert := testRoot(t)
	h.spec = newBuildSpec(cert, 2)
	h.spec.commit = h.now.Add(-time.Minute)

	// The release commit: app.json (written by newHarness), the root
	// certificate and the signer in keytool's form.
	h.write(filepath.Join(h.repo, "mobile", ".gitignore"), "/ios\n/android\n")
	h.write(filepath.Join(h.repo, "mobile", "certs", ota.RootCertFile), string(cert))
	h.write(filepath.Join(h.repo, filepath.FromSlash(signerFile)), keytoolForm(fakeSigner)+"\n")
	h.git(h.repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "add", "-A")
	h.git(h.repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "release: native 2")
	h.commit = h.git(h.repo, "rev-parse", "HEAD")

	h.keystore = filepath.Join(h.home, ".alexmessages", "keys", keystoreFile)
	h.write(h.keystore, "not a real keystore\n")

	h.bin = t.TempDir()
	for name, script := range map[string]string{"pod": fakePod, "xcodebuild": fakeXcodebuild, "gradlew": fakeGradlew} {
		if err := os.WriteFile(filepath.Join(h.bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", h.bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_GRADLEW", filepath.Join(h.bin, "gradlew"))

	// An SDK whose newest stable build-tools is 36.0.0.
	h.sdk = t.TempDir()
	for _, v := range []string{"35.0.0", "36.0.0", "36.1.0-rc1"} {
		dir := filepath.Join(h.sdk, "build-tools", v)
		h.write(filepath.Join(dir, "zipalign"), fakeZipalign)
		h.write(filepath.Join(dir, "apksigner"), fakeApksigner)
		for _, n := range []string{"zipalign", "apksigner"} {
			if err := os.Chmod(filepath.Join(dir, n), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Setenv("ANDROID_HOME", h.sdk)
	t.Setenv("ANDROID_SDK_ROOT", "")
	jdk := t.TempDir()
	h.write(filepath.Join(jdk, "bin", "java"), "#!/bin/sh\n")
	t.Setenv("JAVA_HOME", jdk)

	t.Setenv("FAKE_SIGNER", fakeSigner)
	t.Setenv("FAKE_KS_PASSWORD", fakeKSPassword)
	t.Setenv("FAKE_RUNTIME_android", h.spec.runtimes["android"])
	t.Setenv("FAKE_RUNTIME_ios", h.spec.runtimes["ios"])
	h.stdin = fakeKSPassword + "\n"
	h.writeBuild()
	return h
}

// writeBuild writes what the fake Gradle and xcodebuild produce from spec.
func (h *nativeHarness) writeBuild() {
	h.t.Helper()
	dir := h.t.TempDir()
	h.spec.writeAPK(h.t, filepath.Join(dir, "unsigned.apk"))
	h.spec.writeAppDir(h.t, filepath.Join(dir, "app"))
	h.t.Setenv("FAKE_APK", filepath.Join(dir, "unsigned.apk"))
	h.t.Setenv("FAKE_APP_DIR", filepath.Join(dir, "app"))
}

// keytoolForm writes a SHA-256 as keytool prints it, AB:CD:….
func keytoolForm(hexSum string) string {
	var parts []string
	for i := 0; i < len(hexSum); i += 2 {
		parts = append(parts, strings.ToUpper(hexSum[i:i+2]))
	}
	return strings.Join(parts, ":")
}

// artifactDir is the only artifact directory in the archive.
func (h *nativeHarness) artifactDir() string {
	h.t.Helper()
	dirs, _ := filepath.Glob(filepath.Join(h.archive, "builds", "native-2-*"))
	if len(dirs) != 1 {
		h.t.Fatalf("artifact directories: %v", dirs)
	}
	return dirs[0]
}

func (h *nativeHarness) registered() []ota.RuntimeInfo {
	h.t.Helper()
	var r struct {
		Runtimes []ota.RuntimeInfo `json:"runtimes"`
	}
	if err := json.Unmarshal([]byte(h.otactl("runtimes", "--json")), &r); err != nil {
		h.t.Fatal(err)
	}
	sort.Slice(r.Runtimes, func(i, j int) bool { return r.Runtimes[i].Platform < r.Runtimes[j].Platform })
	return r.Runtimes
}

// toolLines are the lines of the tool log that start with one of prefixes.
func (h *nativeHarness) toolLines(prefixes ...string) []string {
	var out []string
	for _, l := range strings.Split(h.toolLog(), "\n") {
		for _, p := range prefixes {
			if strings.HasPrefix(l, p) {
				out = append(out, l)
				break
			}
		}
	}
	return out
}

func zipNames(t *testing.T, path string) map[string]bool {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	out := map[string]bool{}
	for _, f := range zr.File {
		out[f.Name] = true
	}
	return out
}

func TestNativeBuildThenUpload(t *testing.T) {
	h := newNativeHarness(t)
	out := h.ok("native", "--notes", "Private release key; in-app updates", "--no-upload", "--allow-local")
	mustContain(t, out, "All artifact checks passed. Artifact directory: ", "Nothing was uploaded or registered",
		"--upload-only", "android: AlexMessages-1.2.0-2.apk", "ios: AlexMessages-1.2.0-2.ipa")

	// The steps, in order, with the exact commands of D10.
	steps := h.toolLines("npm ", "npx ", "gradlew ", "pod ", "xcodebuild ", "zipalign", "apksigner", "password")
	want := []string{
		"npm ci --ignore-scripts (in shared)",
		"npm run typecheck (in shared)",
		"npm test (in shared)",
		"npm ci --ignore-scripts (in mobile)",
		"npx --no -- tsc --noEmit [profile=]",
		"npx --no -- expo-updates runtimeversion:resolve --platform android [profile=]",
		"npx --no -- expo-updates runtimeversion:resolve --platform ios [profile=]",
		"npx --no -- expo prebuild --clean --no-install [profile=]",
		"gradlew --no-daemon -Pkotlin.compiler.execution.strategy=in-process :app:assembleRelease [JAVA_HOME=" + os.Getenv("JAVA_HOME") + " ANDROID_HOME=" + h.sdk + "]",
		"pod install (in ios)",
		"xcodebuild -workspace ios/AlexMessages.xcworkspace -scheme AlexMessages -configuration Release -sdk iphoneos -destination generic/platform=iOS -archivePath ",
		"zipalign(36.0.0) -p -f 4 ",
		"apksigner(36.0.0) sign --ks " + h.keystore + " --ks-key-alias alexmessages --v4-signing-enabled false --out ",
		"password accepted",
		"apksigner(36.0.0) verify --print-certs -v ",
	}
	if len(steps) != len(want) {
		t.Fatalf("steps:\n%s", strings.Join(steps, "\n"))
	}
	for i, w := range want {
		if !strings.HasPrefix(steps[i], w) {
			t.Fatalf("step %d is %q, want %q…\nall steps:\n%s", i+1, steps[i], w, strings.Join(steps, "\n"))
		}
	}
	mustContain(t, steps[10], "archive CODE_SIGNING_ALLOWED=NO CODE_SIGNING_REQUIRED=NO")

	dir := h.artifactDir()
	b, err := readBuildJSON(dir)
	if err != nil {
		t.Fatal(err)
	}
	if b.Commit != h.commit || b.Build != 2 || b.Version != "1.2.0" || b.Notes != "Private release key; in-app updates" {
		t.Fatalf("build.json: %+v", b)
	}
	for _, art := range b.Artifacts {
		if art.Runtime != h.spec.runtimes[art.Platform] || art.EmbeddedUpdateID != h.spec.embedded[art.Platform] ||
			art.CommitTime != ota.FormatTime(time.UnixMilli(h.spec.commit.UnixMilli())) {
			t.Fatalf("artifact %+v", *art)
		}
		if sum, size, _ := fileSHA256(filepath.Join(dir, art.File)); sum != art.SHA256 || size != art.Size {
			t.Fatalf("%s: SHA-256 %s, %d bytes; build.json says %s, %d", art.File, sum, size, art.SHA256, art.Size)
		}
	}
	if b.Artifacts[0].Signer != fakeSigner || b.Artifacts[1].Signer != "" {
		t.Fatalf("signers: %q, %q", b.Artifacts[0].Signer, b.Artifacts[1].Signer)
	}
	names := zipNames(t, filepath.Join(dir, "AlexMessages-1.2.0-2.ipa"))
	for _, n := range []string{"Payload/", "Payload/AlexMessages.app/", "Payload/AlexMessages.app/Expo.plist", "Payload/AlexMessages.app/EXUpdates.bundle/app.manifest"} {
		if !names[n] {
			t.Fatalf("the IPA has no %s: %v", n, names)
		}
	}
	if strings.Contains(h.toolLog(), "ssh cd alexmessage && exec bin/otactl 'register-runtime'") {
		t.Fatal("--no-upload registered a runtime")
	}

	// The upload, once the commit is on origin (plan step 6.3).
	h.otactl("init")
	h.stdin = fakeKSPassword + "\n"
	out = h.ok("native", "--upload-only", dir)
	mustContain(t, out, "ingested 2 native build file(s)", "Download check passed", "Tagged "+short(h.commit)+" as native-2",
		"Build 2 (1.2.0) is published on "+h.url+"/app")
	if strings.Contains(h.toolLog()[strings.LastIndex(h.toolLog(), "ssh date"):], "password accepted") {
		t.Fatal("--upload-only asked for the keystore password")
	}
	rts := h.registered()
	if len(rts) != 2 {
		t.Fatalf("runtimes: %+v", rts)
	}
	for i, platform := range []string{"android", "ios"} {
		r := rts[i]
		if r.Platform != platform || r.Runtime != h.spec.runtimes[platform] || len(r.Builds) != 1 ||
			r.Builds[0].Build != 2 || r.Builds[0].EmbeddedUpdateID != h.spec.embedded[platform] || r.Builds[0].Commit != h.commit {
			t.Fatalf("%s runtime: %+v", platform, r)
		}
		if _, err := os.Stat(filepath.Join(h.archive, "ota", "runtimes", platform, r.Runtime+".json")); err != nil {
			t.Fatal(err)
		}
	}
	var policy ota.Policy
	pb, err := os.ReadFile(filepath.Join(h.store, "native", "policy.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(pb, &policy); err != nil {
		t.Fatal(err)
	}
	archived, err := os.ReadFile(filepath.Join(h.archive, "ota", "native", "policy.json"))
	if err != nil || string(archived) != string(pb) {
		t.Fatalf("the archived policy.json differs from the Pi's (%v)", err)
	}
	for _, art := range b.Artifacts {
		l := policy.For(art.Platform).Latest
		if l == nil || l.Build != 2 || l.SHA256 != art.SHA256 || l.URL != h.url+"/app/"+art.Platform+"/"+art.File || l.Notes != b.Notes {
			t.Fatalf("%s latest: %+v", art.Platform, l)
		}
		for _, p := range []string{filepath.Join(h.store, "native", art.Platform, art.File), filepath.Join(h.archive, "ota", "native", art.Platform, art.File)} {
			if sum, _, err := fileSHA256(p); err != nil || sum != art.SHA256 {
				t.Fatalf("%s: %s, %v", p, sum, err)
			}
		}
	}
	if tag := h.git(h.repo, "rev-parse", "native-2^{commit}"); tag != h.commit {
		t.Fatalf("native-2 names %s", tag)
	}

	// A repeated upload finishes nothing new and fails nothing.
	out = h.ok("native", "--upload-only", dir)
	mustContain(t, out, "already registered for these artifacts", "Download check passed")
	if rts := h.registered(); len(rts[0].Builds) != 1 || len(rts[1].Builds) != 1 {
		t.Fatalf("runtimes after the repeat: %+v", rts)
	}
}

func TestNativeBuildAndUpload(t *testing.T) {
	h := newNativeHarness(t)
	h.otactl("init")
	// Build 2 is registered: this commit must be build 3.
	h.otactl("register-runtime", "--platform", "android", "--runtime", "old-android-runtime", "--build", "2", "--version", "1.2.0",
		"--embedded-update-id", newUUID(), "--commit-time", ota.FormatTime(h.now.Add(-48*time.Hour)), "--commit", h.commit)
	mustContain(t, h.fail("native", "--notes", "x"), "this is build 2, but the next build number is 3 (the highest registered build is 2)")
	if strings.Contains(h.toolLog(), "npm ci") {
		t.Fatal("the build ran although the build number is taken")
	}

	h.write(filepath.Join(h.repo, "mobile", "app.json"), `{"expo":{"name":"Alex Messages","version":"1.3.0","ios":{"buildNumber":"3"},"android":{"versionCode":3}}}`+"\n")
	h.git(h.repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-am", "release: native 3")
	h.commit = h.git(h.repo, "rev-parse", "HEAD")
	spec := newBuildSpec(testRoot(t), 3)
	spec.commit = h.now.Add(-time.Minute)
	h.spec = spec
	t.Setenv("FAKE_RUNTIME_android", spec.runtimes["android"])
	t.Setenv("FAKE_RUNTIME_ios", spec.runtimes["ios"])
	h.writeBuild()
	h.stdin = fakeKSPassword + "\n"
	out := h.ok("native", "--notes", "Build three")
	mustContain(t, out, "All artifact checks passed", "ingested 2 native build file(s)", "Tagged "+short(h.commit)+" as native-3",
		"Build 3 (1.3.0) is published")
	var android ota.RuntimeInfo
	for _, r := range h.registered() {
		if r.Platform == "android" && r.Runtime == spec.runtimes["android"] {
			android = r
		}
	}
	if len(android.Builds) != 1 || android.Builds[0].Build != 3 {
		t.Fatalf("android runtimes: %+v", h.registered())
	}
}

func TestNativeRefusals(t *testing.T) {
	h := newNativeHarness(t)
	mustContain(t, h.fail("native", "--notes", "x", "--profile", "drill", "--pi-dir", t.TempDir()), "native makes release builds only")
	mustContain(t, h.fail("native", "--notes", "x", "--allow-local"), "--allow-local needs --no-upload")
	mustContain(t, h.fail("native", "--no-upload"), "notes")
	mustContain(t, h.fail("native", "--upload-only", t.TempDir()), "is not an artifact directory of otapublish native")
	mustContain(t, h.fail("native", "--upload-only", t.TempDir(), "--notes", "x"), "--upload-only takes the notes")

	// app.json whose three numbers disagree.
	h.write(filepath.Join(h.repo, "mobile", "app.json"), `{"expo":{"version":"1.3.0","ios":{"buildNumber":"2"},"android":{"versionCode":2}}}`+"\n")
	h.git(h.repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-am", "bad versions")
	h.commit = h.git(h.repo, "rev-parse", "HEAD")
	mustContain(t, h.fail("native", "--notes", "x", "--no-upload"), `version "1.3.0", ios.buildNumber "2" and android.versionCode 2`)
	h.git(h.repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "revert", "--no-edit", "HEAD")
	h.commit = h.git(h.repo, "rev-parse", "HEAD")

	// No keystore.
	if err := os.Rename(h.keystore, h.keystore+".moved"); err != nil {
		t.Fatal(err)
	}
	mustContain(t, h.fail("native", "--notes", "x", "--no-upload"), "the Android release keystore "+h.keystore+" does not exist")
	if err := os.Rename(h.keystore+".moved", h.keystore); err != nil {
		t.Fatal(err)
	}

	// A JAVA_HOME without bin/java, and no Android Studio JDK either.
	t.Setenv("JAVA_HOME", filepath.Join(t.TempDir(), "jdk-23.jdk"))
	old := studioJDK
	defer func() { studioJDK = old }()
	studioJDK = filepath.Join(t.TempDir(), "no-studio")
	mustContain(t, h.fail("native", "--notes", "x", "--no-upload"), "has no bin/java: set it to a JDK 17 or 21")

	if strings.Contains(h.toolLog(), "npm ci") {
		t.Fatalf("a refused build ran a build step:\n%s", h.toolLog())
	}
}

func TestNativeStudioJDK(t *testing.T) {
	h := newNativeHarness(t)
	t.Setenv("JAVA_HOME", filepath.Join(t.TempDir(), "jdk-23.jdk"))
	old := studioJDK
	defer func() { studioJDK = old }()
	studioJDK = t.TempDir()
	h.write(filepath.Join(studioJDK, "bin", "java"), "#!/bin/sh\n")
	_, errs, code := h.run("native", "--notes", "x", "--no-upload")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, errs)
	}
	mustContain(t, errs, "has no bin/java; using Android Studio's JDK")
	mustContain(t, h.toolLog(), "[JAVA_HOME="+studioJDK+" ")
}

// TestNativeChecksFail breaks the build in ways each of which must stop it
// before anything is saved, uploaded or registered.
func TestNativeChecksFail(t *testing.T) {
	cases := []struct {
		name  string
		setup func(h *nativeHarness)
		want  []string
		// signed: the failure comes after apksigner sign.
		signed bool
	}{
		{"gradle signed the APK", func(h *nativeHarness) { h.t.Setenv("FAKE_APK_NAME", "app-release.apk") },
			[]string{"Gradle signed the release APK (app-release.apk): mobile/plugins/withReleaseSigning.js"}, false},
		{"gradle signed the unsigned APK", func(h *nativeHarness) {
			h.t.Setenv("FAKE_APK", filepath.Join(mustAbs(h.t, "testdata"), "native", "aapt2-signed.apk"))
		}, []string{"Gradle signed the release APK: mobile/plugins/withReleaseSigning.js"}, false},
		{"wrong update URL", func(h *nativeHarness) {
			h.spec.android = map[string]any{androidKeys.url: "https://evil.example/api/ota/manifest"}
			h.spec.ios = map[string]any{iosKeys.allowUnsigned: true}
			h.writeBuild()
		}, []string{"the artifacts failed their checks; nothing was uploaded or registered",
			"AlexMessages-1.2.0-2.apk failed 1 artifact check(s)", "EXPO_UPDATE_URL is \"https://evil.example/api/ota/manifest\"",
			"AlexMessages-1.2.0-2.ipa failed 1 artifact check(s)", "EXUpdatesConfigCodeSigningAllowUnsignedManifests is true"}, true},
		{"fingerprint differs from the resolved runtime", func(h *nativeHarness) {
			h.t.Setenv("FAKE_RUNTIME_ios", "another-ios-runtime")
		}, []string{"the runtime built into the binary is " + "fixture-ios-runtime-2" + ", but runtimeversion:resolve printed another-ios-runtime"}, true},
		{"signed by another key", func(h *nativeHarness) { h.t.Setenv("FAKE_SIGNER", strings.Repeat("ab", 32)) },
			[]string{"is signed by the certificate " + strings.Repeat("ab", 32) + ", but " + signerFile + " names " + fakeSigner}, true},
		{"two signers", func(h *nativeHarness) { h.t.Setenv("FAKE_SIGNERS", "2") }, []string{"not exactly one signer"}, true},
		{"only a v1 signature", func(h *nativeHarness) { h.t.Setenv("FAKE_V2", "false") }, []string{"no APK Signature Scheme v2 or v3 signature"}, true},
		{"wrong keystore password", func(h *nativeHarness) { h.stdin = "wrong\n" }, []string{"apksigner sign: exit status 1"}, false},
		{"a build process still runs", func(h *nativeHarness) {
			h.procs = []process{{pid: 4242, elapsed: 0, name: "java"}}
		}, []string{"java (pid 4242", "started during this publish and are still running"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newNativeHarness(t)
			tc.setup(h)
			mustContain(t, h.fail("native", "--notes", "x", "--no-upload"), tc.want...)
			if got := strings.Contains(h.toolLog(), "password accepted"); got != tc.signed {
				t.Fatalf("signed = %v, want %v\n%s", got, tc.signed, h.toolLog())
			}
			if dirs, _ := filepath.Glob(filepath.Join(h.archive, "builds", "*")); len(dirs) > 0 {
				t.Fatalf("a failed build saved %v", dirs)
			}
		})
	}
}

// TestNativeConfirmBeforePassword: processes older than the build are
// listed and need yes; the keystore password typed after it still reaches
// apksigner.
func TestNativeConfirmBeforePassword(t *testing.T) {
	h := newNativeHarness(t)
	h.procs = []process{{pid: 77, elapsed: 30 * 24 * time.Hour, name: "node"}}
	h.stdin = "yes\n" + fakeKSPassword + "\n"
	_, errs, code := h.run("native", "--notes", "x", "--no-upload")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, errs)
	}
	mustContain(t, errs, "node (pid 77", "Type yes to continue", "apksigner asks for the password of "+h.keystore)
	mustContain(t, h.toolLog(), "password accepted")
}

func TestNativeUploadRefusals(t *testing.T) {
	h := newNativeHarness(t)
	h.ok("native", "--notes", "x", "--no-upload")
	dir := h.artifactDir()

	mustContain(t, h.fail("native", "--upload-only", dir), "has no update store yet: run bin/otactl init")
	h.otactl("init")

	// A file that is not the one build.json describes.
	apk := filepath.Join(dir, "AlexMessages-1.2.0-2.apk")
	orig, err := os.ReadFile(apk)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(apk, append(append([]byte(nil), orig...), 0), 0o644); err != nil {
		t.Fatal(err)
	}
	mustContain(t, h.fail("native", "--upload-only", dir), "AlexMessages-1.2.0-2.apk is not the file that build.json describes")
	if err := os.WriteFile(apk, orig, 0o644); err != nil {
		t.Fatal(err)
	}

	// build.json from another commit.
	b, err := readBuildJSON(dir)
	if err != nil {
		t.Fatal(err)
	}
	other := *b
	other.Commit = strings.Repeat("1", 40)
	otherDir := t.TempDir()
	for _, art := range b.Artifacts {
		h.copyFile(filepath.Join(dir, art.File), filepath.Join(otherDir, art.File), 0o644)
	}
	if err := writeJSON(filepath.Join(otherDir, "build.json"), &other); err != nil {
		t.Fatal(err)
	}
	mustContain(t, h.fail("native", "--upload-only", otherDir), "build.json names commit 11111111")

	// The Pi already holds another build 2.
	h.write(filepath.Join(h.store, "native", "ios", "AlexMessages-1.2.0-2.ipa"), "other bytes")
	mustContain(t, h.fail("native", "--upload-only", dir), "already holds native/ios/AlexMessages-1.2.0-2.ipa with other bytes")
	if rts := h.registered(); len(rts) != 0 {
		t.Fatalf("a refused upload registered %+v", rts)
	}
}

func TestAndroidTools(t *testing.T) {
	sdk := t.TempDir()
	for v, tools := range map[string][]string{
		"34.0.0": {"zipalign", "apksigner"}, "36.0.0": {"zipalign", "apksigner"},
		"36.1.0-rc1": {"zipalign", "apksigner"}, "37.0.0": {"zipalign"},
	} {
		for _, n := range tools {
			p := filepath.Join(sdk, "build-tools", v, n)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, nil, 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	a := &app{env: &env{getenv: func(k string) string {
		if k == "ANDROID_HOME" {
			return sdk
		}
		return ""
	}}}
	tools, err := a.androidTools()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(sdk, "build-tools", "36.0.0", "apksigner"); tools.apksigner != want {
		t.Fatalf("apksigner = %s, want %s", tools.apksigner, want)
	}
	a.env.getenv = func(string) string { return "" }
	a.env.home = t.TempDir()
	if _, err := a.androidTools(); err == nil || !strings.Contains(err.Error(), "set ANDROID_HOME") {
		t.Fatalf("no SDK: %v", err)
	}
}

func TestNormalizeSHA256(t *testing.T) {
	for in, want := range map[string]string{
		keytoolForm(fakeSigner) + "\n": fakeSigner,
		"  " + fakeSigner + "  ":       fakeSigner,
		strings.ToUpper(fakeSigner):    fakeSigner,
		fakeSigner[:62]:                "",
		fakeSigner + "00":              "",
		"SHA256: " + fakeSigner:        "",
	} {
		if got := normalizeSHA256(in); got != want {
			t.Errorf("normalizeSHA256(%q) = %q, want %q", in, got, want)
		}
	}
}

func mustAbs(t *testing.T, p string) string {
	t.Helper()
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}
