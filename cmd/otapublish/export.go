package main

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"alexmessage/internal/ota"
)

// The build half of `otapublish ota` (D6 steps 5–12): checks, runtimes, the
// export, the public config and the manifests. Nothing here needs a key.

// fixture replaces steps of a dry run with prepared data (--fixture; tests
// of otapublish itself, and the plan's Phase 2 check "ota --dry-run against
// a fixture export completes"). Each field that is set replaces one step.
type fixture struct {
	// Runtimes replaces step 7's `expo-updates runtimeversion:resolve`.
	Runtimes map[string]string `json:"runtimes"`
	// ExportDir replaces step 10's `expo export` with its output directory.
	ExportDir string `json:"export_dir"`
	// ExpoConfig replaces step 11 with a file holding what `expo config
	// --json --type public` prints.
	ExpoConfig string `json:"expo_config"`
	// SkipNPM skips steps 5 and 6 (npm ci, typecheck, tests).
	SkipNPM bool `json:"skip_npm"`
	// SkipDeployCheck skips step 3's look at GitHub Actions.
	SkipDeployCheck bool `json:"skip_deploy_check"`
}

func readFixture(path string) (*fixture, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f fixture
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	dir := filepath.Dir(path)
	for _, p := range []*string{&f.ExportDir, &f.ExpoConfig} {
		if *p != "" && !filepath.IsAbs(*p) {
			*p = filepath.Join(dir, *p)
		}
	}
	return &f, nil
}

// npmChecks are D6 steps 5 and 6. --ignore-scripts: no npm package runs an
// install script.
func (a *app) npmChecks() error {
	shared := filepath.Join(a.repo, "shared")
	mobile := filepath.Join(a.repo, "mobile")
	for _, s := range []struct {
		dir  string
		args []string
	}{
		{shared, []string{"npm", "ci", "--ignore-scripts"}},
		{shared, []string{"npm", "run", "typecheck"}},
		{shared, []string{"npm", "test"}},
		{mobile, []string{"npm", "ci", "--ignore-scripts"}},
		{mobile, []string{"npx", "--no", "--", "tsc", "--noEmit"}},
	} {
		if err := a.step(s.dir, s.args[0], s.args[1:]...); err != nil {
			return err
		}
	}
	return nil
}

// runtimeVersionJSON is what `expo-updates runtimeversion:resolve` prints.
type runtimeVersionJSON struct {
	RuntimeVersion *string `json:"runtimeVersion"`
}

// resolveRuntime is D6 step 7's `npx expo-updates runtimeversion:resolve
// --platform <p>`: the fingerprint of this tree for one platform. The
// output format is UNVERIFIED until Phase 4 installs expo-updates: this
// reads the last line that is a JSON object with runtimeVersion.
func (a *app) resolveRuntime(platform string) (string, error) {
	mobile := filepath.Join(a.repo, "mobile")
	if ok, _ := pathExists(filepath.Join(mobile, "node_modules", "expo-updates")); !ok {
		return "", errors.New("mobile/node_modules has no expo-updates: the commit predates plan step 4.1")
	}
	out, err := a.command(mobile, "npx", "--no", "--", "expo-updates", "runtimeversion:resolve", "--platform", platform)
	if err != nil {
		return "", err
	}
	return parseRuntimeOutput(out)
}

func parseRuntimeOutput(out []byte) (string, error) {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		var v runtimeVersionJSON
		if strings.HasPrefix(line, "{") && json.Unmarshal([]byte(line), &v) == nil && v.RuntimeVersion != nil {
			if !ota.ValidRuntime(*v.RuntimeVersion) {
				return "", fmt.Errorf("runtimeversion:resolve printed runtime %q, which is not a valid runtime", *v.RuntimeVersion)
			}
			return *v.RuntimeVersion, nil
		}
	}
	return "", fmt.Errorf("runtimeversion:resolve printed no runtimeVersion: %q", out)
}

// target is one platform an update is published for.
type target struct {
	platform string
	runtime  string
	builds   []ota.RuntimeBuild // the registered builds of the runtime
}

// newestBuild is the registered build with the highest number.
func (t target) newestBuild() ota.RuntimeBuild {
	var n ota.RuntimeBuild
	for _, b := range t.builds {
		if b.Build > n.Build {
			n = b
		}
	}
	return n
}

// registeredRuntimes asks otactl which runtimes have a native build.
func registeredRuntimes(p pi) ([]ota.RuntimeInfo, error) {
	out, err := p.otactl("runtimes", "--json")
	if err != nil {
		return nil, err
	}
	var r struct {
		Runtimes []ota.RuntimeInfo `json:"runtimes"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, fmt.Errorf("otactl runtimes --json: %w", err)
	}
	return r.Runtimes, nil
}

// targets is D6 step 7: the platforms whose resolved runtime is registered.
func (a *app) targets(p pi, platforms []string, fx *fixture) ([]target, error) {
	registered, err := registeredRuntimes(p)
	if err != nil {
		return nil, err
	}
	var out []target
	for _, platform := range platforms {
		var rt string
		if fx != nil && fx.Runtimes != nil {
			if rt = fx.Runtimes[platform]; rt == "" {
				return nil, fmt.Errorf("the fixture names no runtime for %s", platform)
			}
		} else if rt, err = a.resolveRuntime(platform); err != nil {
			return nil, err
		}
		var builds []ota.RuntimeBuild
		for _, r := range registered {
			if r.Platform == platform && r.Runtime == rt {
				builds = r.Builds
			}
		}
		if len(builds) == 0 {
			a.printf("%s: skipped. Native code changed since the last native build of %s (runtime %s is not registered). Run `otapublish native`.\n", platform, platform, rt)
			continue
		}
		out = append(out, target{platform: platform, runtime: rt, builds: builds})
	}
	if len(out) == 0 {
		return nil, errors.New("no platform has a registered runtime; nothing to publish")
	}
	return out, nil
}

// warnOrConfirm prints a warning. A real publish then needs the word yes; a
// dry run only reports that it would ask.
func (a *app) warnOrConfirm(dryRun bool, text string) error {
	if dryRun {
		a.printf("WARNING (a real publish asks for yes here): %s\n", text)
		return nil
	}
	if !a.confirm(text) {
		return errors.New("not confirmed; nothing was published")
	}
	return nil
}

// versionJSON is otactl version --json.
type versionJSON map[string]struct {
	Commit string `json:"commit"`
	Error  string `json:"error"`
}

// serverCheck is D6 step 8: the server must serve phones that run this
// commit's JS. It compares the deployed server's commit with this one.
func (a *app) serverCheck(p pi, commit string, dryRun bool) error {
	out, err := p.otactl("version", "--json")
	if err != nil {
		return err
	}
	var v versionJSON
	if err := json.Unmarshal(out, &v); err != nil {
		return fmt.Errorf("otactl version --json: %w", err)
	}
	server := v["server"].Commit
	if server == "" || server == "unknown" {
		return a.warnOrConfirm(dryRun, "The commit of the deployed server cannot be read (otactl version). Publish only if the deployed server works with this commit's JS.")
	}
	if _, err := git(a.repo, "cat-file", "-e", server+"^{commit}"); err != nil {
		return a.warnOrConfirm(dryRun, fmt.Sprintf("The deployed server was built from commit %s, which this repository does not have (git fetch?).", short(server)))
	}
	if server == commit {
		return nil
	}
	diff, err := git(a.repo, "diff", "--stat", server, commit, "--", "internal/", "cmd/server")
	if err != nil {
		return err
	}
	if diff == "" {
		return nil
	}
	return a.warnOrConfirm(dryRun, fmt.Sprintf("The server code differs between the deployed server (%s) and this commit (%s):\n%s\nIf this update needs the newer server, deploy the server first (plan section 6).", short(server), short(commit), diff))
}

// nativeContractPaths are the files whose change since a native build can
// break the JS-to-native contract of that build (D6 step 9).
var nativeContractPaths = []string{"mobile/src/call", "mobile/modules", "mobile/src/notify.ts", "mobile/src/session.tsx", "mobile/app.json"}

// nativeContractCheck is D6 step 9.
func (a *app) nativeContractCheck(targets []target, commit string, dryRun bool) error {
	seen := map[string]bool{}
	for _, t := range targets {
		base := t.newestBuild().Commit
		if seen[base] {
			continue
		}
		seen[base] = true
		if _, err := git(a.repo, "cat-file", "-e", base+"^{commit}"); err != nil {
			if err := a.warnOrConfirm(dryRun, fmt.Sprintf("%s build %d was built from commit %s, which this repository does not have: the native-contract check cannot run.", t.platform, t.newestBuild().Build, base)); err != nil {
				return err
			}
			continue
		}
		diff, err := git(a.repo, append([]string{"diff", "--stat", base, commit, "--"}, nativeContractPaths...)...)
		if err != nil {
			return err
		}
		if diff != "" {
			if err := a.warnOrConfirm(dryRun, fmt.Sprintf("Since native build %d (commit %s), these files changed; the JS must still work with that build's native code:\n%s", t.newestBuild().Build, short(base), diff)); err != nil {
				return err
			}
		}
	}
	return nil
}

// exportJS is D6 step 10: `expo export` into a temporary directory (never
// mobile/dist).
func (a *app) exportJS(targets []target) (string, error) {
	tmp, err := a.tempDir("export")
	if err != nil {
		return "", err
	}
	out := filepath.Join(tmp, "export")
	args := []string{"--no", "--", "expo", "export"}
	for _, t := range targets {
		args = append(args, "--platform", t.platform)
	}
	args = append(args, "--output-dir", out, "--dump-assetmap", "--source-maps")
	if err := a.step(filepath.Join(a.repo, "mobile"), "npx", args...); err != nil {
		return "", err
	}
	return out, nil
}

// expoConfig is D6 step 11: the public config, which becomes
// extra.expoClient.
func (a *app) expoConfig(fx *fixture) (json.RawMessage, error) {
	var out []byte
	var err error
	if fx != nil && fx.ExpoConfig != "" {
		out, err = os.ReadFile(fx.ExpoConfig)
	} else {
		out, err = a.command(filepath.Join(a.repo, "mobile"), "npx", "--no", "--", "expo", "config", "--json", "--type", "public")
	}
	if err != nil {
		return nil, err
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(out, &obj); err != nil {
		return nil, fmt.Errorf("expo config printed no JSON object: %w", err)
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// exportedPlatform is one platform of an expo export's metadata.json.
type exportedPlatform struct {
	Bundle string `json:"bundle"`
	Assets []struct {
		Path string `json:"path"`
		Ext  string `json:"ext"`
	} `json:"assets"`
}

// readExport reads metadata.json of an expo export.
func readExport(dir string) (map[string]exportedPlatform, error) {
	b, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if err != nil {
		return nil, err
	}
	var md struct {
		FileMetadata map[string]exportedPlatform `json:"fileMetadata"`
	}
	if err := json.Unmarshal(b, &md); err != nil {
		return nil, fmt.Errorf("metadata.json: %w", err)
	}
	return md.FileMetadata, nil
}

// exportFile reads a file the export's metadata names. The path must stay
// inside the export.
func exportFile(dir, rel string) ([]byte, error) {
	clean := filepath.Clean(filepath.FromSlash(rel))
	if rel == "" || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("metadata.json names the path %q, which is outside the export", rel)
	}
	p := filepath.Join(dir, clean)
	st, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", rel)
	}
	return os.ReadFile(p)
}

var assetExtRE = regexp.MustCompile(`^[a-z0-9]{1,10}$`)

// assetFile is one file of an update: its store name and its bytes.
type assetFile struct {
	name string // <sha256-b64url>.<ext>
	data []byte
}

// built is one platform's unsigned update.
type built struct {
	target
	id       string
	manifest []byte
	assets   []assetFile // the launch asset first
	bundle   int         // the launch bundle's size
}

// manifestBuild is what one manifest is made of.
type manifestBuild struct {
	id         string
	createdAt  time.Time
	baseURL    string
	expoClient json.RawMessage
	am         ota.ManifestAM
}

// buildManifest is D6 step 12 for one platform.
func buildManifest(t target, exp exportedPlatform, exportDir string, mb manifestBuild) (*built, error) {
	bundle, err := exportFile(exportDir, exp.Bundle)
	if err != nil {
		return nil, fmt.Errorf("%s bundle: %w", t.platform, err)
	}
	b := &built{target: t, id: mb.id, bundle: len(bundle)}
	launch, file := manifestAsset(bundle, "js", mb.baseURL)
	launch.ContentType = "application/javascript"
	launch.FileExtension = ".bundle"
	b.assets = append(b.assets, file)
	assets := []ota.ManifestAsset{}
	for _, ea := range exp.Assets {
		data, err := exportFile(exportDir, ea.Path)
		if err != nil {
			return nil, fmt.Errorf("%s asset: %w", t.platform, err)
		}
		ext := strings.ToLower(ea.Ext)
		if !assetExtRE.MatchString(ext) {
			return nil, fmt.Errorf("%s asset %s: extension %q must match %s (plan D2)", t.platform, ea.Path, ea.Ext, assetExtRE)
		}
		ma, file := manifestAsset(data, ext, mb.baseURL)
		// The key is the MD5 that expo export named the file by; phones skip
		// a download when an asset with that key is built into the binary.
		if filepath.Base(ea.Path) != ma.Key {
			return nil, fmt.Errorf("%s asset %s: its MD5 is %s, but expo export names assets by their MD5", t.platform, ea.Path, ma.Key)
		}
		ma.FileExtension = "." + ea.Ext
		assets = append(assets, ma)
		b.assets = append(b.assets, file)
	}
	extra, err := marshalNoEscape(struct {
		ExpoClient json.RawMessage `json:"expoClient"`
		AM         ota.ManifestAM  `json:"am"`
	}{mb.expoClient, mb.am})
	if err != nil {
		return nil, err
	}
	m := ota.UpdateManifest{
		ID: mb.id, CreatedAt: ota.FormatTime(mb.createdAt), RuntimeVersion: t.runtime,
		LaunchAsset: launch, Assets: assets, Metadata: json.RawMessage(`{}`), Extra: extra,
	}
	if b.manifest, err = marshalNoEscape(m); err != nil {
		return nil, err
	}
	// The Pi checks every manifest with the same parser.
	if _, err := ota.ParseUpdateManifest(b.manifest); err != nil {
		return nil, err
	}
	return b, nil
}

func manifestAsset(data []byte, ext, baseURL string) (ota.ManifestAsset, assetFile) {
	hash := ota.AssetHash(data)
	sum := md5.Sum(data)
	name := hash + "." + ext
	return ota.ManifestAsset{
		Hash: hash, Key: hex.EncodeToString(sum[:]), ContentType: ota.AssetContentType(ext),
		FileExtension: "." + ext, URL: baseURL + "/ota/assets/" + name,
	}, assetFile{name: name, data: data}
}

// marshalNoEscape marshals v without HTML escaping and without a newline.
func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// baseURL is where phones download a platform's assets: the public server,
// or in the drill the scratch server as each emulator reaches it (D6).
func (a *app) baseURL(platform string) string {
	if a.drill() {
		if platform == ota.PlatformAndroid {
			return "http://10.0.2.2:8799"
		}
		return "http://localhost:8799"
	}
	return a.publicURL
}

// statusJSON is the part of otactl status --json that otapublish reads.
type statusJSON struct {
	Store    string         `json:"store"`
	Pointers []pointerEntry `json:"pointers"`
	Revoked  []string       `json:"revoked"`
}

type pointerEntry struct {
	ota.PointerEntry
	Label     string `json:"label"`
	CreatedAt string `json:"created_at"`
}

func piStatus(p pi) (*statusJSON, error) {
	out, err := p.otactl("status", "--json")
	if err != nil {
		return nil, err
	}
	var s statusJSON
	if err := json.Unmarshal(out, &s); err != nil {
		return nil, fmt.Errorf("otactl status --json: %w", err)
	}
	return &s, nil
}

func (s *statusJSON) pointer(channel, platform, runtime string) *pointerEntry {
	for i := range s.Pointers {
		e := &s.Pointers[i]
		if e.Channel == channel && e.Platform == platform && e.Runtime == runtime {
			return e
		}
	}
	return nil
}

// labelRE is the label otapublish gives an update: YYYYMMDD.k.
var labelRE = regexp.MustCompile(`^(\d{8})\.(\d+)$`)

// nextLabel is today's next label (local date): one more than the highest k
// of today's labels in the archive, on the Pi and in the git tags.
func (a *app) nextLabel(st *statusJSON) (string, error) {
	day := a.now().Format("20060102")
	used := map[string]bool{}
	infos, err := a.archivedInfos()
	if err != nil {
		return "", err
	}
	for _, in := range infos {
		used[in.Label] = true
	}
	for _, p := range st.Pointers {
		used[p.Label] = true
	}
	if tags, err := git(a.repo, "tag", "--list", "ota-"+day+".*"); err == nil {
		for _, t := range strings.Fields(tags) {
			used[strings.TrimPrefix(t, "ota-")] = true
		}
	}
	k := 0
	for l := range used {
		if m := labelRE.FindStringSubmatch(l); m != nil && m[1] == day {
			var n int
			fmt.Sscanf(m[2], "%d", &n)
			k = max(k, n)
		}
	}
	return fmt.Sprintf("%s.%d", day, k+1), nil
}

// archivedUpdate is one update in the archive.
type archivedUpdate struct {
	id string
	ota.UpdateInfo
}

// archivedInfos lists the archive's updates, oldest first.
func (a *app) archivedInfos() ([]archivedUpdate, error) {
	entries, err := os.ReadDir(a.otaArchive("updates"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []archivedUpdate
	for _, e := range entries {
		id, ok := ota.NormalizeUUID(e.Name())
		if !ok || id != e.Name() {
			continue
		}
		var info ota.UpdateInfo
		b, err := os.ReadFile(a.otaArchive("updates", id, "info.json"))
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, &info); err != nil {
			return nil, fmt.Errorf("archive update %s: info.json: %w", id, err)
		}
		out = append(out, archivedUpdate{id: id, UpdateInfo: info})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out, nil
}

// floor is the time an update for t on channel must be later than: the
// pointer's current update (one second more for a rollback directive) and
// the build time of every registered binary of the runtime (D6 step 12).
func (a *app) floor(st *statusJSON, channel string, t target) (time.Time, string, error) {
	var at time.Time
	var what string
	for _, b := range t.builds {
		bt, err := ota.ParseTime(b.CommitTime)
		if err != nil {
			return at, "", fmt.Errorf("%s build %d: commit_time %q: %w", t.platform, b.Build, b.CommitTime, err)
		}
		if bt.After(at) {
			at, what = bt, fmt.Sprintf("the build time of %s native build %d", t.platform, b.Build)
		}
	}
	e := st.pointer(channel, t.platform, t.runtime)
	if e == nil {
		return at, what, nil
	}
	ptr := &e.Pointer
	if ptr.State == ota.StateTestUnsigned {
		ptr = ptr.Previous
	}
	if ptr == nil || ptr.Update == "" {
		return at, what, nil
	}
	created, err := a.updateCreated(ptr.Update, e)
	if err != nil {
		return at, "", err
	}
	w := "the createdAt of the update the " + channel + " pointer serves"
	if ptr.State == ota.StateRollback {
		created, w = created.Add(time.Second), "the commitTime of the rollback directive the "+channel+" pointer serves"
	}
	if created.After(at) {
		at, what = created, w
	}
	return at, what, nil
}

// updateCreated finds an update's createdAt in the archive, else in the
// status entry.
func (a *app) updateCreated(id string, e *pointerEntry) (time.Time, error) {
	b, err := os.ReadFile(a.otaArchive("updates", id, "info.json"))
	if err == nil {
		var info ota.UpdateInfo
		if err := json.Unmarshal(b, &info); err != nil {
			return time.Time{}, err
		}
		return ota.ParseTime(info.CreatedAt)
	}
	if e != nil && e.Update == id && e.CreatedAt != "" {
		return ota.ParseTime(e.CreatedAt)
	}
	return time.Time{}, fmt.Errorf("update %s is neither in the archive nor described by the Pi", id)
}
