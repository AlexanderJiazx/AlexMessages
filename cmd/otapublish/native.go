package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"alexmessage/internal/ota"
)

// `otapublish native` (D10): build the unsigned release APK and the unsigned
// IPA of a commit in a clean worktree, sign the APK with apksigner (the
// keystore password is typed at apksigner's own prompt), run the artifact
// checks, and upload, register and publish both builds. --no-upload stops
// after the checks (plan step 5.3); --upload-only uploads such a build once
// its commit is on origin (plan step 6.3).

const (
	// keystoreFile is the Android release keystore in the keys directory
	// (plan step 3.2).
	keystoreFile  = "android-release.p12"
	keystoreAlias = "alexmessages"
	// signerFile holds the SHA-256 of the release key's certificate
	// (committed, public).
	signerFile = "mobile/release/android-signer.sha256"
	// The Xcode workspace and scheme that expo prebuild generates for the
	// app name "Alex Messages".
	xcodeName = "AlexMessages"
)

func setupNative(fs *flag.FlagSet) func(a *app, args []string) error {
	notes := fs.String("notes", "", "what the build changes (the install page and Settings show it)")
	noUpload := fs.Bool("no-upload", false, "build, sign and check only; print the artifact directory (plan step 5.3)")
	allowLocal := fs.Bool("allow-local", false, "with --no-upload: accept a commit that is not on origin")
	uploadOnly := fs.String("upload-only", "", "upload the artifact directory of an earlier --no-upload build (plan step 6.3)")
	return func(a *app, args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		if a.drill() {
			return usagef("the drill builds its apps with expo run and registers them with otactl register-runtime (plan step 5.1); native makes release builds only")
		}
		if *allowLocal && !*noUpload {
			return usagef("--allow-local needs --no-upload: an upload needs a commit that is on origin (plan D3 item 4)")
		}
		if *uploadOnly != "" {
			if *noUpload || *notes != "" {
				return usagef("--upload-only takes the notes and the commit from the artifact directory's build.json")
			}
			dir, err := filepath.Abs(*uploadOnly)
			if err != nil {
				return err
			}
			b, err := readBuildJSON(dir)
			if err != nil {
				return err
			}
			return a.staged(stageOptions{ref: b.Commit, requireOrigin: true}, func() error { return a.uploadNative(dir, b) })
		}
		if err := validNotes(*notes, true); err != nil {
			return err
		}
		return a.staged(stageOptions{requireOrigin: !*allowLocal}, func() error { return a.buildNative(*notes, *noUpload) })
	}
}

// buildJSON is build.json in an artifact directory.
type buildJSON struct {
	Commit    string      `json:"commit"`
	Build     int         `json:"build"`
	Version   string      `json:"version"`
	Notes     string      `json:"notes"`
	CreatedAt string      `json:"created_at"`
	Artifacts []*artifact `json:"artifacts"`
}

var fullCommitRE = regexp.MustCompile(`^[0-9a-f]{40}$`)

func readBuildJSON(dir string) (*buildJSON, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "build.json"))
	if err != nil {
		return nil, fmt.Errorf("%s is not an artifact directory of otapublish native: %w", dir, err)
	}
	var b buildJSON
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, fmt.Errorf("%s/build.json: %w", dir, err)
	}
	if !fullCommitRE.MatchString(b.Commit) || b.Build < 2 || b.Version != fmt.Sprintf("1.%d.0", b.Build) || len(b.Notes) > ota.MaxNotes {
		return nil, fmt.Errorf("%s/build.json: commit %q, build %d, version %q: not a release build", dir, b.Commit, b.Build, b.Version)
	}
	seen := map[string]bool{}
	for _, art := range b.Artifacts {
		if !ota.ValidPlatform(art.Platform) || seen[art.Platform] || art.File != ota.NativeFileName(art.Platform, b.Version, b.Build) {
			return nil, fmt.Errorf("%s/build.json: artifact %+v", dir, *art)
		}
		seen[art.Platform] = true
	}
	if len(seen) != 2 {
		return nil, fmt.Errorf("%s/build.json lists %d platform(s), not android and ios", dir, len(seen))
	}
	sort.Slice(b.Artifacts, func(i, j int) bool { return b.Artifacts[i].Platform < b.Artifacts[j].Platform })
	return &b, nil
}

// release is what the release commit says about its build (D10, check 6).
type release struct {
	build   int
	version string
	cert    []byte // mobile/certs/ota-root-2026.pem
	signer  string // mobile/release/android-signer.sha256, lowercase hex
}

// releaseInfo reads app.json, the root certificate and the signer hash of
// the worktree: version 1.N.0, ios.buildNumber "N" and android.versionCode
// N (section 6, "Ship a native change").
func (a *app) releaseInfo() (*release, error) {
	raw, err := os.ReadFile(filepath.Join(a.repo, "mobile", "app.json"))
	if err != nil {
		return nil, err
	}
	var cfg struct {
		Expo struct {
			Version string `json:"version"`
			IOS     struct {
				BuildNumber string `json:"buildNumber"`
			} `json:"ios"`
			Android struct {
				VersionCode int `json:"versionCode"`
			} `json:"android"`
		} `json:"expo"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("mobile/app.json: %w", err)
	}
	e := cfg.Expo
	n := e.Android.VersionCode
	if n < 2 || e.IOS.BuildNumber != strconv.Itoa(n) || e.Version != fmt.Sprintf("1.%d.0", n) {
		return nil, fmt.Errorf("mobile/app.json has version %q, ios.buildNumber %q and android.versionCode %d: a release commit has 1.N.0, \"N\" and N with N >= 2 (plan section 6)",
			e.Version, e.IOS.BuildNumber, n)
	}
	r := &release{build: n, version: e.Version}
	certPath := filepath.Join(a.repo, "mobile", "certs", ota.RootCertFile)
	root, b, err := ota.ReadRootFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("the root certificate every binary embeds: %w (plan step 3.1)", err)
	}
	if err := ota.CheckRoot(root, a.now()); err != nil {
		return nil, fmt.Errorf("mobile/certs/%s: %w", ota.RootCertFile, err)
	}
	r.cert = b
	s, err := os.ReadFile(filepath.Join(a.repo, filepath.FromSlash(signerFile)))
	if err != nil {
		return nil, fmt.Errorf("%s (the release key's certificate SHA-256, plan step 3.2): %w", signerFile, err)
	}
	if r.signer = normalizeSHA256(string(s)); r.signer == "" {
		return nil, fmt.Errorf("%s does not hold one SHA-256 (64 hex digits, colons allowed)", signerFile)
	}
	return r, nil
}

var hex64RE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// normalizeSHA256 accepts keytool's AB:CD:… form and apksigner's lowercase
// hex; it returns "" for anything else.
func normalizeSHA256(s string) string {
	s = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), ":", ""))
	if !hex64RE.MatchString(s) {
		return ""
	}
	return s
}

// androidTools are the Android SDK and the newest stable build-tools'
// zipalign and apksigner.
type androidTools struct {
	sdk, zipalign, apksigner string
}

func (a *app) androidTools() (*androidTools, error) {
	sdk := a.getenv("ANDROID_HOME")
	if sdk == "" {
		sdk = a.getenv("ANDROID_SDK_ROOT")
	}
	if sdk == "" {
		sdk = filepath.Join(a.home, "Library", "Android", "sdk")
	}
	entries, err := os.ReadDir(filepath.Join(sdk, "build-tools"))
	if err != nil {
		return nil, fmt.Errorf("the Android SDK's build-tools: %w; set ANDROID_HOME", err)
	}
	var best []int
	var bestName string
	for _, e := range entries {
		v := versionParts(e.Name())
		if v == nil || compareVersions(v, best) <= 0 {
			continue
		}
		dir := filepath.Join(sdk, "build-tools", e.Name())
		if ok, _ := pathExists(filepath.Join(dir, "apksigner")); !ok {
			continue
		}
		if ok, _ := pathExists(filepath.Join(dir, "zipalign")); !ok {
			continue
		}
		best, bestName = v, e.Name()
	}
	if bestName == "" {
		return nil, fmt.Errorf("%s has no build-tools with zipalign and apksigner", filepath.Join(sdk, "build-tools"))
	}
	dir := filepath.Join(sdk, "build-tools", bestName)
	return &androidTools{sdk: sdk, zipalign: filepath.Join(dir, "zipalign"), apksigner: filepath.Join(dir, "apksigner")}, nil
}

// versionParts parses a stable version such as 36.0.0; a release candidate
// (36.1.0-rc1) gives nil.
func versionParts(s string) []int {
	var out []int
	for _, p := range strings.Split(s, ".") {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		out = append(out, n)
	}
	return out
}

func compareVersions(a, b []int) int {
	for i := 0; i < len(a) || i < len(b); i++ {
		x, y := 0, 0
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// studioJDK is the JDK that Android Studio ships; tests replace it.
var studioJDK = "/Applications/Android Studio.app/Contents/jbr/Contents/Home"

// buildEnv is the environment of Gradle and apksigner: childEnv with
// ANDROID_HOME, and a JAVA_HOME that has bin/java (a JAVA_HOME left over
// from an uninstalled JDK makes gradlew stop).
func (a *app) buildEnv(t *androidTools) ([]string, error) {
	env := withoutEnv(a.childEnv(), "ANDROID_HOME", "JAVA_HOME")
	env = append(env, "ANDROID_HOME="+t.sdk)
	jdk := a.getenv("JAVA_HOME")
	if ok, _ := pathExists(filepath.Join(jdk, "bin", "java")); jdk == "" || !ok {
		studio, _ := pathExists(filepath.Join(studioJDK, "bin", "java"))
		switch {
		case studio:
			fmt.Fprintf(a.stderr, "otapublish: JAVA_HOME (%q) has no bin/java; using Android Studio's JDK\n", jdk)
			jdk = studioJDK
		case jdk != "":
			return nil, fmt.Errorf("JAVA_HOME (%s) has no bin/java: set it to a JDK 17 or 21", jdk)
		}
	}
	if jdk != "" {
		env = append(env, "JAVA_HOME="+jdk)
		for i, kv := range env {
			if strings.HasPrefix(kv, "PATH=") {
				env[i] = "PATH=" + filepath.Join(jdk, "bin") + string(os.PathListSeparator) + kv[len("PATH="):]
			}
		}
	}
	return env, nil
}

// run runs a build tool in dir with env, its output going to stderr.
func (a *app) runTool(dir string, env []string, name string, args ...string) error {
	fmt.Fprintf(a.stderr, "$ (cd %s && %s %s)\n", filepath.Base(dir), filepath.Base(name), strings.Join(args, " "))
	cmd := exec.Command(name, args...)
	cmd.Dir, cmd.Env = dir, env
	cmd.Stdout, cmd.Stderr = a.stderr, a.stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", filepath.Base(name), strings.Join(args, " "), err)
	}
	return nil
}

// buildNative builds, signs and checks build N, and uploads it unless
// noUpload.
func (a *app) buildNative(notes string, noUpload bool) error {
	commit := a.commit
	mobile := filepath.Join(a.repo, "mobile")
	rel, err := a.releaseInfo()
	if err != nil {
		return err
	}
	keystore := filepath.Join(a.keysDir, keystoreFile)
	if ok, err := pathExists(keystore); err != nil || !ok {
		return fmt.Errorf("the Android release keystore %s does not exist (plan step 3.2)", keystore)
	}
	tools, err := a.androidTools()
	if err != nil {
		return err
	}
	env, err := a.buildEnv(tools)
	if err != nil {
		return err
	}
	if err := a.envCheck(); err != nil {
		return err
	}
	p, err := a.pi()
	if err != nil {
		return err
	}
	if !noUpload {
		if err := a.localURLCheck(p); err != nil {
			return err
		}
	}
	if err := a.clockCheck(p); err != nil {
		return err
	}
	if _, err := a.checkBuildNumber(p, rel.build, nil); err != nil {
		return err
	}
	// D6 steps 5 and 6, then the runtimes of the clean tree (D3 item 1),
	// before prebuild writes ios/ and android/.
	if err := a.npmChecks(); err != nil {
		return err
	}
	runtimes := map[string]string{}
	for _, platform := range []string{ota.PlatformAndroid, ota.PlatformIOS} {
		if runtimes[platform], err = a.resolveRuntime(platform); err != nil {
			return err
		}
	}
	tmp, err := a.tempDir("native")
	if err != nil {
		return err
	}
	if err := a.step(mobile, "npx", "--no", "--", "expo", "prebuild", "--clean", "--no-install"); err != nil {
		return err
	}
	unsigned, err := a.gradleRelease(mobile, env)
	if err != nil {
		return err
	}
	ipa := filepath.Join(tmp, ota.NativeFileName(ota.PlatformIOS, rel.version, rel.build))
	if err := a.archiveIPA(mobile, tmp, ipa); err != nil {
		return err
	}
	// Every build process must be gone before the password prompt.
	if err := a.processCheck(); err != nil {
		return err
	}
	apk := filepath.Join(tmp, ota.NativeFileName(ota.PlatformAndroid, rel.version, rel.build))
	if err := a.signAPK(tools, env, unsigned, apk, keystore); err != nil {
		return err
	}
	x := nativeExpect{build: rel.build, version: rel.version, cert: rel.cert, runtimes: runtimes, now: a.now()}
	arts, err := a.checkArtifacts(tools, env, apk, ipa, x, rel.signer)
	if err != nil {
		return err
	}
	b := &buildJSON{Commit: commit, Build: rel.build, Version: rel.version, Notes: notes,
		CreatedAt: a.now().UTC().Format(time.RFC3339), Artifacts: arts}
	dir, err := a.saveArtifacts(b, map[string]string{ota.PlatformAndroid: apk, ota.PlatformIOS: ipa})
	if err != nil {
		return err
	}
	a.printf("All artifact checks passed. Artifact directory: %s\n", dir)
	for _, art := range arts {
		a.printf("  %s: %s, %d bytes, SHA-256 %s, runtime %s, embedded update %s\n", art.Platform, art.File, art.Size, art.SHA256, art.Runtime, art.EmbeddedUpdateID)
	}
	if noUpload {
		a.printf("Nothing was uploaded or registered. Once commit %s is on origin: go run ./cmd/otapublish native --upload-only %s\n", short(commit), dir)
		return nil
	}
	return a.uploadNative(dir, b)
}

// gradleRelease builds the unsigned release APK. The Kotlin compiler runs
// inside the single-use Gradle process, so no daemon outlives the build
// (the process check comes next).
func (a *app) gradleRelease(mobile string, env []string) (string, error) {
	android := filepath.Join(mobile, "android")
	if err := a.runTool(android, env, "./gradlew", "--no-daemon", "-Pkotlin.compiler.execution.strategy=in-process", ":app:assembleRelease"); err != nil {
		return "", err
	}
	outDir := filepath.Join(android, "app", "build", "outputs", "apk", "release")
	apk := filepath.Join(outDir, "app-release-unsigned.apk")
	if ok, err := pathExists(apk); err != nil || !ok {
		if signed, _ := pathExists(filepath.Join(outDir, "app-release.apk")); signed {
			return "", errors.New("Gradle signed the release APK (app-release.apk): mobile/plugins/withReleaseSigning.js is missing or broken (plan D10)")
		}
		return "", fmt.Errorf("Gradle wrote no %s", apk)
	}
	if signed, err := apkSigned(apk); err != nil || signed {
		if err == nil {
			err = errors.New("Gradle signed the release APK: mobile/plugins/withReleaseSigning.js is missing or broken (plan D10)")
		}
		return "", err
	}
	return apk, nil
}

// archiveIPA is the IPA recipe of D10: the Pods (prebuild --no-install
// leaves them out), an unsigned archive, its app copied into Payload/,
// zipped.
func (a *app) archiveIPA(mobile, tmp, ipa string) error {
	if err := a.step(filepath.Join(mobile, "ios"), "pod", "install"); err != nil {
		return err
	}
	archive := filepath.Join(tmp, "AM.xcarchive")
	if err := a.runTool(mobile, a.childEnv(), "xcodebuild", "-workspace", "ios/"+xcodeName+".xcworkspace", "-scheme", xcodeName,
		"-configuration", "Release", "-sdk", "iphoneos", "-destination", "generic/platform=iOS", "-archivePath", archive,
		"archive", "CODE_SIGNING_ALLOWED=NO", "CODE_SIGNING_REQUIRED=NO"); err != nil {
		return err
	}
	appDir := filepath.Join(archive, "Products", "Applications", xcodeName+".app")
	if ok, err := pathExists(appDir); err != nil || !ok {
		return fmt.Errorf("xcodebuild archive wrote no %s", appDir)
	}
	payload := filepath.Join(tmp, "ipa", "Payload")
	if err := os.MkdirAll(payload, 0o755); err != nil {
		return err
	}
	if err := a.runTool(tmp, a.childEnv(), "cp", "-pR", appDir, payload+"/"); err != nil {
		return err
	}
	return a.runTool(filepath.Join(tmp, "ipa"), a.childEnv(), "zip", "-qry", ipa, "Payload")
}

// signAPK aligns the unsigned APK and signs it with the release key. The
// keystore password is typed at apksigner's prompt and never passes
// through otapublish.
func (a *app) signAPK(t *androidTools, env []string, unsigned, out, keystore string) error {
	aligned := out + ".aligned"
	if err := a.runTool(filepath.Dir(out), env, t.zipalign, "-p", "-f", "4", unsigned, aligned); err != nil {
		return err
	}
	defer os.Remove(aligned)
	fmt.Fprintf(a.stderr, "apksigner asks for the password of %s (plan step 3.2).\n", keystore)
	cmd := exec.Command(t.apksigner, "sign", "--ks", keystore, "--ks-key-alias", keystoreAlias,
		"--v4-signing-enabled", "false", "--out", out, aligned)
	cmd.Dir, cmd.Env = filepath.Dir(out), env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = a.childStdin(), a.stderr, a.stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("apksigner sign: %w", err)
	}
	return nil
}

var (
	signerCountRE = regexp.MustCompile(`(?m)^Number of signers: (\d+)$`)
	signerRE      = regexp.MustCompile(`(?m)^Signer #1 certificate SHA-256 digest: ([0-9a-f]{64})$`)
	schemeRE      = regexp.MustCompile(`(?m)^Verified using v(2|3) scheme \(APK Signature Scheme v[23]\): true$`)
)

// apkSigner verifies an APK's signature and returns its signer's
// certificate SHA-256 (D10, check 5).
func (a *app) apkSigner(t *androidTools, env []string, apk string) (string, error) {
	cmd := exec.Command(t.apksigner, "verify", "--print-certs", "-v", apk)
	cmd.Env = env
	cmd.Stderr = a.stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("apksigner verify %s: %w\n%s", filepath.Base(apk), err, out)
	}
	if m := signerCountRE.FindSubmatch(out); m == nil || string(m[1]) != "1" {
		return "", fmt.Errorf("apksigner verify %s: not exactly one signer:\n%s", filepath.Base(apk), out)
	}
	if !schemeRE.Match(out) {
		return "", fmt.Errorf("apksigner verify %s: no APK Signature Scheme v2 or v3 signature:\n%s", filepath.Base(apk), out)
	}
	m := signerRE.FindSubmatch(out)
	if m == nil {
		return "", fmt.Errorf("apksigner verify %s printed no signer SHA-256:\n%s", filepath.Base(apk), out)
	}
	return string(m[1]), nil
}

// checkArtifacts runs every artifact check of D10 on both files and
// reports every failure.
func (a *app) checkArtifacts(t *androidTools, env []string, apk, ipa string, x nativeExpect, signer string) ([]*artifact, error) {
	var errs []error
	aa, err := checkAPK(apk, x)
	if err != nil {
		errs = append(errs, err)
	}
	if got, err := a.apkSigner(t, env, apk); err != nil {
		errs = append(errs, err)
	} else if got != signer {
		errs = append(errs, fmt.Errorf("%s is signed by the certificate %s, but %s names %s: not the release key (plan D10)", filepath.Base(apk), got, signerFile, signer))
	} else if aa != nil {
		aa.Signer = got
	}
	ia, err := checkIPA(ipa, x)
	if err != nil {
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("the artifacts failed their checks; nothing was uploaded or registered:\n%w", err)
	}
	return []*artifact{aa, ia}, nil
}

// saveArtifacts copies the checked files and build.json into a new
// directory of the archive, <archive>/builds/native-N-<UTC time>.
func (a *app) saveArtifacts(b *buildJSON, files map[string]string) (string, error) {
	dir := filepath.Join(a.archive, "builds", fmt.Sprintf("native-%d-%s", b.Build, a.now().UTC().Format("20060102T150405Z")))
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}
	if err := os.Mkdir(dir, 0o755); err != nil {
		return "", err
	}
	for _, art := range b.Artifacts {
		if err := copyFile(files[art.Platform], filepath.Join(dir, art.File)); err != nil {
			return "", err
		}
	}
	if err := writeJSON(filepath.Join(dir, "build.json"), b); err != nil {
		return "", err
	}
	return dir, nil
}

// checkBuildNumber is the build-number half of D10 check 6: N is the
// highest build registered on either platform + 1 (2 when none is), and
// the Pi holds no file of build N. For an upload (arts set), a build N that
// is already registered for exactly these artifacts is a repeated upload,
// which passes: every later step is idempotent.
func (a *app) checkBuildNumber(p pi, n int, arts []*artifact) (bool, error) {
	ready, err := p.ready()
	if err != nil {
		return false, err
	}
	var registered []ota.RuntimeInfo
	inv := &inventoryJSON{}
	if ready {
		if registered, err = registeredRuntimes(p); err != nil {
			return false, err
		}
		if inv, err = piInventory(p); err != nil {
			return false, err
		}
	} else if arts != nil {
		return false, fmt.Errorf("%s has no update store yet: run bin/otactl init on the Pi first (plan step 6.3)", p)
	}
	highest := 0
	var atN []ota.RuntimeBuild
	var atNInfo []ota.RuntimeInfo
	for _, r := range registered {
		for _, b := range r.Builds {
			highest = max(highest, b.Build)
			if b.Build == n {
				atN = append(atN, b)
				atNInfo = append(atNInfo, r)
			}
		}
	}
	want := max(highest+1, 2)
	rerun := false
	switch {
	case n == want:
	case n == highest && arts != nil:
		for i, b := range atN {
			art := artifactFor(arts, atNInfo[i].Platform)
			if art == nil || atNInfo[i].Runtime != art.Runtime || b.EmbeddedUpdateID != art.EmbeddedUpdateID || b.CommitTime != art.CommitTime || b.Commit != a.commit {
				return false, fmt.Errorf("%s build %d is already registered for another binary (runtime %s, embedded update %s); a build number is never reused: make release commit %d",
					atNInfo[i].Platform, n, atNInfo[i].Runtime, b.EmbeddedUpdateID, n+1)
			}
		}
		rerun = true
	default:
		return false, fmt.Errorf("this is build %d, but the next build number is %d (the highest registered build is %d): make the release commit for build %d (plan section 6)", n, want, highest, want)
	}
	for _, platform := range []string{ota.PlatformAndroid, ota.PlatformIOS} {
		for name, sum := range inv.Native[platform] {
			_, b, ok := ota.ParseNativeName(platform, name)
			if !ok || b != n {
				continue
			}
			art := artifactFor(arts, platform)
			if art == nil || art.File != name || art.SHA256 != sum {
				return false, fmt.Errorf("%s already holds native/%s/%s with other bytes: a rebuilt binary takes the next build number (plan D2)", p, platform, name)
			}
		}
	}
	return rerun, nil
}

func artifactFor(arts []*artifact, platform string) *artifact {
	for _, art := range arts {
		if art.Platform == platform {
			return art
		}
	}
	return nil
}

// uploadNative checks the artifacts of dir again, uploads them, registers
// their runtimes, publishes them in policy.json and tags native-N.
func (a *app) uploadNative(dir string, b *buildJSON) error {
	if b.Commit != a.commit {
		return fmt.Errorf("build.json names commit %s, and this otapublish runs %s", short(b.Commit), short(a.commit))
	}
	rel, err := a.releaseInfo()
	if err != nil {
		return err
	}
	if rel.build != b.Build || rel.version != b.Version {
		return fmt.Errorf("build.json says build %d (%s), and mobile/app.json at %s says build %d (%s)", b.Build, b.Version, short(b.Commit), rel.build, rel.version)
	}
	tools, err := a.androidTools()
	if err != nil {
		return err
	}
	env, err := a.buildEnv(tools)
	if err != nil {
		return err
	}
	p, err := a.pi()
	if err != nil {
		return err
	}
	if err := a.localURLCheck(p); err != nil {
		return err
	}
	if err := a.clockCheck(p); err != nil {
		return err
	}
	// The same files that passed at build time, checked again.
	paths := map[string]string{}
	runtimes := map[string]string{}
	for _, art := range b.Artifacts {
		paths[art.Platform] = filepath.Join(dir, art.File)
		sum, size, err := fileSHA256(paths[art.Platform])
		if err != nil {
			return err
		}
		if sum != art.SHA256 || size != art.Size {
			return fmt.Errorf("%s is not the file that build.json describes (SHA-256 %s, %d bytes)", art.File, sum, size)
		}
		runtimes[art.Platform] = art.Runtime
	}
	x := nativeExpect{build: b.Build, version: b.Version, cert: rel.cert, runtimes: runtimes, now: a.now()}
	arts, err := a.checkArtifacts(tools, env, paths[ota.PlatformAndroid], paths[ota.PlatformIOS], x, rel.signer)
	if err != nil {
		return err
	}
	for _, art := range arts {
		if old := artifactFor(b.Artifacts, art.Platform); *old != *art {
			return fmt.Errorf("%s: the checks now read %+v, and build.json recorded %+v", art.File, *art, *old)
		}
	}
	rerun, err := a.checkBuildNumber(p, b.Build, arts)
	if err != nil {
		return err
	}
	if rerun {
		a.printf("Build %d is already registered for these artifacts; finishing the earlier upload.\n", b.Build)
	}

	// The archive first (D2: authoritative for native builds), then the Pi.
	up, err := a.tempDir("upload")
	if err != nil {
		return err
	}
	for _, art := range arts {
		if err := a.archiveNative(art, paths[art.Platform]); err != nil {
			return err
		}
		dst := filepath.Join(up, "native", art.Platform, art.File)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := copyFile(paths[art.Platform], dst); err != nil {
			return err
		}
	}
	plan, err := a.ingest(p, up, fmt.Sprintf("native-%d", b.Build))
	if err != nil {
		return err
	}
	a.printf("%s: ingested %d native build file(s)\n", p, len(plan.Native))
	for _, art := range arts {
		if _, err := p.otactl("register-runtime", "--platform", art.Platform, "--runtime", art.Runtime, "--build", strconv.Itoa(b.Build),
			"--version", b.Version, "--embedded-update-id", art.EmbeddedUpdateID, "--commit-time", art.CommitTime, "--commit", b.Commit); err != nil {
			return err
		}
	}
	if err := a.archiveRuntimes(p, arts); err != nil {
		return err
	}
	var policy []byte
	for _, art := range arts {
		if policy, err = p.otactl("policy", "--platform", art.Platform, "--latest-build", strconv.Itoa(b.Build),
			"--notes", b.Notes, "--base-url", a.publicURL); err != nil {
			return err
		}
	}
	if err := ota.WriteFileAtomic(a.otaArchive("native", "policy.json"), policy, 0o644); err != nil {
		return err
	}
	if err := a.checkDownloads(b.Build, arts); err != nil {
		return err
	}
	tag := fmt.Sprintf("native-%d", b.Build)
	if old, err := git(a.repo, "rev-parse", "--verify", "--quiet", "refs/tags/"+tag+"^{commit}"); err == nil && old != "" {
		if old != b.Commit {
			a.printf("WARNING: the tag %s already names commit %s, not %s; it was left as it is.\n", tag, short(old), short(b.Commit))
		}
	} else if _, err := git(a.repo, "tag", tag, b.Commit); err != nil {
		a.printf("WARNING: the git tag %s was not created: %v\n", tag, err)
	} else {
		a.printf("Tagged %s as %s (local tag).\n", short(b.Commit), tag)
	}
	a.printf("Build %d (%s) is published on %s/app. Android phones offer it in the app; on the iPhone, Settings, Software Update links to /app.\n", b.Build, b.Version, a.publicURL)
	return nil
}

// archiveNative copies an uploaded build into <archive>/ota/native, the
// archive's write-once mirror of data/ota/native.
func (a *app) archiveNative(art *artifact, src string) error {
	dst := a.otaArchive("native", art.Platform, art.File)
	if sum, _, err := fileSHA256(dst); err == nil {
		if sum != art.SHA256 {
			return fmt.Errorf("the archive already holds another %s; a build number is never reused", dst)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return copyFile(src, dst)
}

// archiveRuntimes mirrors the Pi's runtime files of the uploaded builds
// into <archive>/ota/runtimes (D2: the archive is authoritative for them;
// restore-pi registers them again from there).
func (a *app) archiveRuntimes(p pi, arts []*artifact) error {
	registered, err := registeredRuntimes(p)
	if err != nil {
		return err
	}
	for _, art := range arts {
		for _, r := range registered {
			if r.Platform != art.Platform || r.Runtime != art.Runtime {
				continue
			}
			b, err := json.MarshalIndent(r, "", "  ")
			if err != nil {
				return err
			}
			if err := os.MkdirAll(a.otaArchive("runtimes", r.Platform), 0o755); err != nil {
				return err
			}
			if err := ota.WriteFileAtomic(a.otaArchive("runtimes", r.Platform, r.Runtime+".json"), append(b, '\n'), 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkDownloads reads /api/app/latest and the download URLs through the
// public server, as phones and the install page do.
func (a *app) checkDownloads(build int, arts []*artifact) error {
	resp, err := a.http.Get(a.publicURL + "/api/app/latest")
	if err != nil {
		return fmt.Errorf("download check: %w", err)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if err != nil {
		return err
	}
	var policy ota.Policy
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &policy) != nil {
		return fmt.Errorf("download check: %s/api/app/latest answered %s: %s", a.publicURL, resp.Status, body)
	}
	for _, art := range arts {
		l := policy.For(art.Platform).Latest
		if l == nil || l.Build != build || l.SHA256 != art.SHA256 || l.Size != art.Size {
			return fmt.Errorf("download check: /api/app/latest shows %s build %+v, not build %d with SHA-256 %s", art.Platform, l, build, art.SHA256)
		}
		req, err := http.NewRequest(http.MethodHead, l.URL, nil)
		if err != nil {
			return err
		}
		resp, err := a.http.Do(req)
		if err != nil {
			return fmt.Errorf("download check: %w", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || resp.ContentLength != art.Size {
			return fmt.Errorf("download check: HEAD %s answered %s with %d bytes, not %d", l.URL, resp.Status, resp.ContentLength, art.Size)
		}
	}
	a.printf("Download check passed: /api/app/latest shows build %d, and both files answer with their size.\n", build)
	return nil
}
