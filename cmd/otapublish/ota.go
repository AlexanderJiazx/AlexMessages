package main

import (
	"bytes"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"alexmessage/internal/ota"
	"alexmessage/internal/ota/sign"
)

// `otapublish ota` (D6), and the commands that re-sign: republish (D9 step
// 5) and resign-active (D4).

type otaOptions struct {
	notes     string
	channel   string
	critical  bool
	dryRun    bool
	platforms []string
	fixture   *fixture
}

func platformsFlag(p string) ([]string, error) {
	if p == "" {
		return []string{ota.PlatformAndroid, ota.PlatformIOS}, nil
	}
	if !ota.ValidPlatform(p) {
		return nil, usagef("--platform must be android or ios")
	}
	return []string{p}, nil
}

func validChannel(c string) error {
	if c != ota.ChannelBeta && c != ota.ChannelProduction {
		return usagef("--channel must be beta or production, not %q", c)
	}
	return nil
}

func validNotes(notes string, required bool) error {
	if required && strings.TrimSpace(notes) == "" {
		return usagef("--notes is required: say what the update changes")
	}
	if len(notes) > ota.MaxNotes {
		return usagef("--notes is longer than %d bytes", ota.MaxNotes)
	}
	return nil
}

func setupOTA(fs *flag.FlagSet) func(a *app, args []string) error {
	notes := fs.String("notes", "", "what the update changes (Settings, Software Update shows it)")
	channel := fs.String("channel", ota.ChannelBeta, "beta or production")
	critical := fs.Bool("critical", false, "phones show the restart banner at once (D7)")
	dryRun := fs.Bool("dry-run", false, "run steps 1–12 only: no key, no upload")
	allowLocal := fs.Bool("allow-local", false, "with --dry-run: accept a commit that is not on origin")
	from := fs.String("from", "", "publish this branch or commit instead of HEAD (a backport: release/native-N)")
	platform := fs.String("platform", "", "android or ios (default: both)")
	fixturePath := fs.String("fixture", "", "with --dry-run: a JSON file that replaces steps with prepared data")
	return func(a *app, args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		if err := validNotes(*notes, true); err != nil {
			return err
		}
		if err := validChannel(*channel); err != nil {
			return err
		}
		platforms, err := platformsFlag(*platform)
		if err != nil {
			return err
		}
		if *allowLocal && !*dryRun {
			return usagef("--allow-local needs --dry-run: a publish runs only a commit that is on origin (plan D3 item 4)")
		}
		fx := a.fixture
		if *fixturePath != "" {
			if !*dryRun {
				return usagef("--fixture needs --dry-run")
			}
			if fx, err = readFixture(*fixturePath); err != nil {
				return err
			}
		}
		o := otaOptions{notes: *notes, channel: *channel, critical: *critical, dryRun: *dryRun, platforms: platforms, fixture: fx}
		return a.staged(stageOptions{ref: *from, requireOrigin: !*allowLocal && !a.drill()}, func() error { return a.publishOTA(o) })
	}
}

func (a *app) publishOTA(o otaOptions) error {
	commit := a.commit
	p, err := a.pi()
	if err != nil {
		return err
	}
	_, local := p.(*localPi)
	if !o.dryRun {
		if err := a.localURLCheck(p); err != nil {
			return err
		}
	}
	fx := o.fixture

	// Step 3: the environment and a running Deploy.
	if err := a.envCheck(); err != nil {
		return err
	}
	if !a.drill() && (fx == nil || !fx.SkipDeployCheck) {
		if err := a.deployCheck(); err != nil {
			return err
		}
	}
	// Step 4: clocks.
	if !a.drill() {
		if err := a.clockCheck(p); err != nil {
			return err
		}
	}
	// Steps 5 and 6: typecheck and tests.
	if fx == nil || !fx.SkipNPM {
		if err := a.npmChecks(); err != nil {
			return err
		}
	}
	// Step 7: runtimes.
	targets, err := a.targets(p, o.platforms, fx)
	if err != nil {
		return err
	}
	// Step 8: the deployed server. A local store has no deployed server.
	if !a.drill() && !local {
		if err := a.serverCheck(p, commit, o.dryRun); err != nil {
			return err
		}
	}
	// Step 9: the native contract.
	if err := a.nativeContractCheck(targets, commit, o.dryRun); err != nil {
		return err
	}
	// Step 10: export.
	exportDir := ""
	if fx != nil && fx.ExportDir != "" {
		exportDir = fx.ExportDir
	} else if exportDir, err = a.exportJS(targets); err != nil {
		return err
	}
	exp, err := readExport(exportDir)
	if err != nil {
		return err
	}
	// Step 11: the public config.
	expoClient, err := a.expoConfig(fx)
	if err != nil {
		return err
	}
	// Step 12: manifests.
	st, err := piStatus(p)
	if err != nil {
		return err
	}
	label, err := a.nextLabel(st)
	if err != nil {
		return err
	}
	createdAt := a.now().UTC().Truncate(time.Millisecond)
	am := ota.ManifestAM{Label: label, Commit: commit, Notes: o.notes, Critical: o.critical}
	var builds []*built
	for _, t := range targets {
		e, ok := exp[t.platform]
		if !ok {
			return fmt.Errorf("the export has no %s bundle", t.platform)
		}
		if err := a.checkFloor(st, o.channel, t, createdAt); err != nil {
			return err
		}
		b, err := buildManifest(t, e, exportDir, manifestBuild{id: newUUID(), createdAt: createdAt,
			baseURL: a.baseURL(t.platform), expoClient: expoClient, am: am})
		if err != nil {
			return err
		}
		builds = append(builds, b)
		a.printf("%s: runtime %s, update %s, bundle %d bytes, %d other assets\n", t.platform, t.runtime, b.id, b.bundle, len(b.assets)-1)
	}
	if o.dryRun {
		dir, err := os.MkdirTemp("", "otapublish-dry-run-")
		if err != nil {
			return err
		}
		for _, b := range builds {
			if err := os.WriteFile(filepath.Join(dir, b.platform+"-manifest.json"), b.manifest, 0o644); err != nil {
				return err
			}
		}
		a.printf("Dry run of label %s for %s: steps 1–12 passed. Nothing was signed, uploaded or activated. Unsigned manifests: %s\n", label, o.channel, dir)
		return nil
	}

	// Step 13: the process check, the passphrase, signatures.
	if err := a.processCheck(); err != nil {
		return err
	}
	s, err := a.signingLeaf()
	if err != nil {
		return err
	}
	var ups []*update
	for _, b := range builds {
		info := ota.UpdateInfo{Platform: b.platform, Runtime: b.runtime, Label: label, Commit: commit,
			CreatedAt: ota.FormatTime(createdAt), Notes: o.notes, Critical: o.critical}
		u, err := a.signUpdate(s, b.id, b.manifest, info, b.assets)
		if err != nil {
			return err
		}
		ups = append(ups, u)
	}
	if err := a.archiveSourceMaps(exportDir, label); err != nil {
		return err
	}
	// Steps 14–17.
	if err := a.publish(p, s, ups, o.channel, label); err != nil {
		return err
	}
	// Step 18.
	a.finish(o.channel, label, commit, ups)
	return nil
}

// localURLCheck refuses to check a local store's updates through the
// production server: a publish into --pi-dir needs --public-url (the server
// that serves that store), or the drill, which skips those checks.
func (a *app) localURLCheck(p pi) error {
	if _, ok := p.(*localPi); ok && !a.drill() && a.publicURLFlag == "" {
		return usagef("a publish into --pi-dir needs --public-url (the server that serves that store), or --profile drill")
	}
	return nil
}

// checkFloor refuses a createdAt that phones would ignore.
func (a *app) checkFloor(st *statusJSON, channel string, t target, createdAt time.Time) error {
	fl, what, err := a.floor(st, channel, t)
	if err != nil {
		return err
	}
	if !createdAt.After(fl) {
		return fmt.Errorf("%s: createdAt %s is not later than %s, %s: phones would ignore this update; check this Mac's clock",
			t.platform, ota.FormatTime(createdAt), what, ota.FormatTime(fl))
	}
	return nil
}

func newUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// update is one signed update: the seven files of updates/<id>/ and the
// assets its manifest names.
type update struct {
	id     string
	info   ota.UpdateInfo
	files  map[string][]byte
	assets []assetFile
}

func (s *signer) findLeaf(sha string) (*x509.Certificate, error) {
	if sha == ota.LeafSHA256(s.cert) {
		return s.cert, nil
	}
	return nil, fmt.Errorf("leaf %s is not the signing leaf", sha)
}

// signFiles signs files["manifest.json"] and files["rollback.json"] with the
// signer's leaf and checks the result with the Pi's rules (D4: verify both
// signatures against the root before uploading).
func (a *app) signFiles(s *signer, id string, files map[string][]byte) error {
	leaf := []byte(ota.LeafSHA256(s.cert) + "\n")
	for _, kind := range []string{ota.KindManifest, ota.KindRollback} {
		sig, err := sign.SignatureHeader(files[kind+".json"], s.key, ota.RootKeyID)
		if err != nil {
			return err
		}
		files[kind+".sig"] = []byte(sig + "\n")
		files[kind+".leaf"] = leaf
	}
	if _, err := ota.VerifyUpdateFiles(id, files, s.findLeaf, s.root, a.now()); err != nil {
		return fmt.Errorf("the signed update does not verify: %w", err)
	}
	return nil
}

// signUpdate makes the seven files of a new update.
func (a *app) signUpdate(s *signer, id string, manifest []byte, info ota.UpdateInfo, assets []assetFile) (*update, error) {
	created, err := time.Parse(ota.TimeLayout, info.CreatedAt)
	if err != nil {
		return nil, err
	}
	infoJSON, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{
		"manifest.json": manifest,
		"rollback.json": ota.NewRollbackDirective(created),
		"info.json":     append(infoJSON, '\n'),
	}
	if err := a.signFiles(s, id, files); err != nil {
		return nil, err
	}
	return &update{id: id, info: info, files: files, assets: assets}, nil
}

// publish is D6 steps 14–17 for signed updates: the archive, the upload,
// ingest, the asset check through the public URL, activation, and the smoke
// test, which puts the old pointers back when it fails.
func (a *app) publish(p pi, s *signer, ups []*update, channel, label string) error {
	if err := a.archiveUpdates(ups); err != nil {
		return err
	}
	dir, err := a.uploadDir(p, s, ups, true)
	if err != nil {
		return err
	}
	plan, err := a.ingest(p, dir, label)
	if err != nil {
		return err
	}
	a.printf("%s: ingested %d update(s) and %d new asset(s)\n", p, len(plan.Updates), len(plan.Assets))
	if !a.drill() {
		if err := a.checkAssets(ups); err != nil {
			return err
		}
	}
	ids := make([]string, len(ups))
	for i, u := range ups {
		ids[i] = u.id
	}
	changes, err := a.activate(p, channel, ids, label)
	if err != nil {
		return err
	}
	a.printf("Live from now on (%s):\n", channel)
	a.printChanges(changes)
	if a.drill() {
		return nil
	}
	if err := a.smokeTest(s.root, channel, ups); err != nil {
		if rerr := a.restorePointers(p, changes, label); rerr != nil {
			return fmt.Errorf("the smoke test failed (%v), and putting the previous pointers back ALSO failed: %v; run otactl halt --label %s on the Pi now", err, rerr, label)
		}
		return fmt.Errorf("the smoke test failed, and the previous pointers are back: %w", err)
	}
	a.printf("Smoke test passed: %s serves the signed manifest of every platform.\n", a.publicURL)
	return nil
}

// archiveUpdates writes the updates and their assets into the Mac archive,
// which D2 makes authoritative for these write-once files.
func (a *app) archiveUpdates(ups []*update) error {
	for _, u := range ups {
		dir := a.otaArchive("updates", u.id)
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			return err
		}
		if err := os.Mkdir(dir, 0o755); err != nil {
			return fmt.Errorf("archive: %w", err)
		}
		for _, name := range ota.UpdateFiles {
			if err := writeNew(filepath.Join(dir, name), u.files[name], 0o644); err != nil {
				return err
			}
		}
		for _, f := range u.assets {
			if err := a.archiveAsset(f); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *app) archiveAsset(f assetFile) error {
	p := a.otaArchive("assets", f.name)
	old, err := os.ReadFile(p)
	if err == nil {
		if !bytes.Equal(old, f.data) {
			return fmt.Errorf("the archive's %s differs from the asset of that hash; the archive is damaged", p)
		}
		return nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return writeNew(p, f.data, 0o644)
}

// archiveSourceMaps keeps the export's source maps, which turn a stack trace
// from a phone back into source lines. They are never uploaded.
func (a *app) archiveSourceMaps(exportDir, label string) error {
	dst := a.otaArchive("sourcemaps", label)
	return filepath.WalkDir(exportDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".map") {
			return err
		}
		rel, err := filepath.Rel(exportDir, p)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return writeNew(target, b, 0o644)
	})
}

// inventoryJSON is the part of otactl inventory --json that otapublish reads.
type inventoryJSON struct {
	Certs    map[string]string            `json:"certs"`
	Assets   map[string]int64             `json:"assets"`
	Updates  map[string]map[string]string `json:"updates"`
	Native   map[string]map[string]string `json:"native"`
	Runtimes []ota.RuntimeInfo            `json:"runtimes"`
	Pointers []pointerEntry               `json:"pointers"`
	Revoked  []string                     `json:"revoked"`
	Policy   *ota.Policy                  `json:"policy"`
}

func piInventory(p pi) (*inventoryJSON, error) {
	out, err := p.otactl("inventory", "--json")
	if err != nil {
		return nil, err
	}
	var inv inventoryJSON
	if err := json.Unmarshal(out, &inv); err != nil {
		return nil, fmt.Errorf("otactl inventory --json: %w", err)
	}
	return &inv, nil
}

// uploadDir lays out an upload as otactl ingest expects it: the root and
// signing leaf certificates, the updates, and (withAssets) every asset the
// store does not hold yet.
func (a *app) uploadDir(p pi, s *signer, ups []*update, withAssets bool) (string, error) {
	_, rootPEM, err := a.archivedRoot()
	if err != nil {
		return "", err
	}
	stored := map[string]int64{}
	if withAssets {
		inv, err := piInventory(p)
		if err != nil {
			return "", err
		}
		stored = inv.Assets
	}
	dir, err := a.tempDir("upload")
	if err != nil {
		return "", err
	}
	if err := writeFile(filepath.Join(dir, "certs", ota.RootCertFile), rootPEM); err != nil {
		return "", err
	}
	if err := writeFile(filepath.Join(dir, "certs", "leaf-"+s.name+".pem"), s.pem); err != nil {
		return "", err
	}
	written := map[string]bool{}
	for _, u := range ups {
		for _, name := range ota.UpdateFiles {
			if err := writeFile(filepath.Join(dir, "updates", u.id, name), u.files[name]); err != nil {
				return "", err
			}
		}
		if !withAssets {
			continue
		}
		for _, f := range u.assets {
			if _, ok := stored[f.name]; ok || written[f.name] {
				continue
			}
			written[f.name] = true
			if err := writeFile(filepath.Join(dir, "assets", f.name), f.data); err != nil {
				return "", err
			}
		}
	}
	return dir, nil
}

// checkAssets is D6 step 16: download every asset from the URL in the
// manifest (through Cloudflare in production) and check its SHA-256 after
// decoding. Go's transport asks only for gzip and decodes it.
func (a *app) checkAssets(ups []*update) error {
	seen := map[string]bool{}
	for _, u := range ups {
		m, err := ota.ParseUpdateManifest(u.files["manifest.json"])
		if err != nil {
			return err
		}
		for _, ma := range append([]ota.ManifestAsset{m.LaunchAsset}, m.Assets...) {
			if seen[ma.URL] {
				continue
			}
			seen[ma.URL] = true
			resp, err := a.http.Get(ma.URL)
			if err != nil {
				return fmt.Errorf("asset check: %w", err)
			}
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				return fmt.Errorf("asset check: %s: %w", ma.URL, err)
			}
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("asset check: %s answered %s; nothing was activated", ma.URL, resp.Status)
			}
			if ota.AssetHash(body) != ma.Hash {
				return fmt.Errorf("asset check: %s does not hash to its name; nothing was activated. Purge exactly this URL in Cloudflare (dashboard, Caching, Configuration, Custom Purge), then publish again", ma.URL)
			}
		}
	}
	a.printf("Asset check passed: %d asset URL(s) serve the right bytes.\n", len(seen))
	return nil
}

// pointerChange is one pointer that otactl changed, as its --json prints it.
type pointerChange struct {
	Channel   string       `json:"channel"`
	Platform  string       `json:"platform"`
	Runtime   string       `json:"runtime"`
	Old       *ota.Pointer `json:"old"`
	New       *ota.Pointer `json:"new"`
	Label     string       `json:"label,omitempty"`
	Unchanged bool         `json:"unchanged,omitempty"`
}

func (a *app) printChanges(changes []pointerChange) {
	for _, c := range changes {
		if c.Unchanged {
			a.printf("  %s/%s/%s: unchanged, %s\n", c.Channel, c.Platform, c.Runtime, describe(c.New))
			continue
		}
		a.printf("  %s/%s/%s: %s -> %s\n", c.Channel, c.Platform, c.Runtime, describe(c.Old), describe(c.New))
	}
}

func describe(p *ota.Pointer) string {
	if p == nil {
		return "none"
	}
	if p.Update == "" {
		return p.State
	}
	return p.State + " " + p.Update
}

// pointerChanges runs an otactl command that prints {"changes": […]} and
// records every changed pointer in the archive's pointers.log (D2), also
// when otactl failed part way.
func (a *app) pointerChanges(p pi, action, label string, args ...string) ([]pointerChange, error) {
	out, err := p.otactl(args...)
	var r struct {
		Changes []pointerChange `json:"changes"`
	}
	if jerr := json.Unmarshal(out, &r); jerr != nil && err == nil {
		err = fmt.Errorf("otactl %s --json: %w", args[0], jerr)
	}
	if lerr := a.logPointers(action, label, r.Changes); lerr != nil && err == nil {
		err = lerr
	}
	return r.Changes, err
}

func (a *app) activate(p pi, channel string, ids []string, label string) ([]pointerChange, error) {
	args := []string{"activate", "--channel", channel, "--json"}
	for _, id := range ids {
		args = append(args, "--update", id)
	}
	return a.pointerChanges(p, "activate", label, args...)
}

// pointerLogEntry is one line of <archive>/ota/pointers.log.
type pointerLogEntry struct {
	Time     string       `json:"time"`
	Action   string       `json:"action"`
	Channel  string       `json:"channel"`
	Platform string       `json:"platform"`
	Runtime  string       `json:"runtime"`
	Old      *ota.Pointer `json:"old"`
	New      *ota.Pointer `json:"new"`
	Label    string       `json:"label,omitempty"`
	Commit   string       `json:"commit,omitempty"`
}

func (a *app) logPointers(action, label string, changes []pointerChange) error {
	var lines []byte
	for _, c := range changes {
		if c.Unchanged {
			continue
		}
		e := pointerLogEntry{Time: a.now().UTC().Format(time.RFC3339), Action: action, Channel: c.Channel,
			Platform: c.Platform, Runtime: c.Runtime, Old: c.Old, New: c.New, Label: label, Commit: a.commitOf(c.New)}
		b, err := json.Marshal(e)
		if err != nil {
			return err
		}
		lines = append(append(lines, b...), '\n')
	}
	if len(lines) == 0 {
		return nil
	}
	path := a.otaArchive("pointers.log")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(lines); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// commitOf is the commit of the update a pointer serves, from the archive.
func (a *app) commitOf(p *ota.Pointer) string {
	if p == nil || p.Update == "" {
		return ""
	}
	b, err := os.ReadFile(a.otaArchive("updates", p.Update, "info.json"))
	if err != nil {
		return ""
	}
	var info ota.UpdateInfo
	if json.Unmarshal(b, &info) != nil {
		return ""
	}
	return info.Commit
}

// restorePointers puts back the pointers that changes replaced.
func (a *app) restorePointers(p pi, changes []pointerChange, label string) error {
	var errs []error
	for i := len(changes) - 1; i >= 0; i-- {
		c := changes[i]
		if c.Unchanged {
			continue
		}
		args := []string{"restore-pointer", "--channel", c.Channel, "--platform", c.Platform, "--runtime", c.Runtime, "--json"}
		if c.Old == nil {
			args = append(args, "--none")
		} else {
			b, err := json.Marshal(c.Old)
			if err != nil {
				return err
			}
			args = append(args, "--pointer", string(b))
		}
		restored, err := a.pointerChanges(p, "restore", label, args...)
		a.printChanges(restored)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// smokeTest is D6 step 17: ask the manifest endpoint what a phone on each
// platform and runtime gets now, and check that it is this update, signed.
func (a *app) smokeTest(root *x509.Certificate, channel string, ups []*update) error {
	for _, u := range ups {
		parts, err := a.manifestParts(channel, u.info.Platform, u.info.Runtime)
		if err != nil {
			return err
		}
		m := parts["manifest"]
		if m == nil || !bytes.Equal(m.body, u.files["manifest.json"]) {
			return fmt.Errorf("%s: the manifest endpoint does not serve update %s", u.info.Platform, u.id)
		}
		chain := parts["certificate_chain"]
		if chain == nil {
			return fmt.Errorf("%s: the answer has no certificate_chain part", u.info.Platform)
		}
		if _, err := ota.VerifySignature(m.body, m.signature, chain.body, root, a.now()); err != nil {
			return fmt.Errorf("%s: the served signature does not verify: %w", u.info.Platform, err)
		}
	}
	return nil
}

type part struct {
	body      []byte
	signature string
}

// manifestParts makes the update check of a phone on the production URL
// (with am_channel="beta" for beta) and returns the multipart answer's parts
// by name.
func (a *app) manifestParts(channel, platform, runtime string) (map[string]*part, error) {
	req, err := http.NewRequest(http.MethodGet, a.publicURL+"/api/ota/manifest", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "multipart/mixed,application/expo+json,application/json")
	req.Header.Set("Expo-Protocol-Version", "1")
	req.Header.Set("Expo-Platform", platform)
	req.Header.Set("Expo-Runtime-Version", runtime)
	req.Header.Set("Expo-Channel-Name", ota.ChannelProduction)
	req.Header.Set("Expo-Expect-Signature", `sig, keyid="`+ota.RootKeyID+`", alg="`+ota.SignatureAlg+`"`)
	if channel == ota.ChannelBeta {
		req.Header.Set("Expo-Extra-Params", `am_channel="beta"`)
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("smoke test: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("smoke test: %s %s/%s answered %s, not an update", platform, channel, runtime, resp.Status)
	}
	mt, params, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/mixed" {
		return nil, fmt.Errorf("smoke test: Content-Type %q is not multipart/mixed", resp.Header.Get("Content-Type"))
	}
	r := multipart.NewReader(resp.Body, params["boundary"])
	parts := map[string]*part{}
	for {
		pt, err := r.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("smoke test: %w", err)
		}
		body, err := io.ReadAll(pt)
		if err != nil {
			return nil, err
		}
		parts[pt.FormName()] = &part{body: body, signature: pt.Header.Get("expo-signature")}
	}
	return parts, nil
}

// finish is D6 step 18: the git tag, and who gets the update when.
func (a *app) finish(channel, label, commit string, ups []*update) {
	if !a.drill() {
		if _, err := git(a.repo, "tag", "ota-"+label, commit); err != nil {
			a.printf("WARNING: the git tag ota-%s was not created: %v\n", label, err)
		} else {
			a.printf("Tagged %s as ota-%s (local tag).\n", short(commit), label)
		}
	}
	who := "Every phone"
	if channel == ota.ChannelBeta {
		who = "Phones with Beta updates on"
	}
	for _, u := range ups {
		a.printf("%s: %s on runtime %s download update %s (%s) at their next check: a cold start, or a return to the app after 30 minutes. They run it after that download, at their next restart.\n",
			u.info.Platform, who, u.info.Runtime, label, u.id)
	}
	if channel == ota.ChannelBeta {
		a.printf("Promote it once it works on a beta phone of each platform: otapublish promote %s\n", label)
	}
}

func setupPromote(fs *flag.FlagSet) func(a *app, args []string) error {
	force := fs.Bool("force", false, "skip the evidence check (an urgent fix)")
	return func(a *app, args []string) error {
		if len(args) != 1 || !ota.ValidLabel(args[0]) {
			return usagef("promote takes one label")
		}
		p, err := a.pi()
		if err != nil {
			return err
		}
		cmd := []string{"promote", "--label", args[0], "--json"}
		if *force {
			cmd = append(cmd, "--force")
		}
		changes, err := a.pointerChanges(p, "promote", args[0], cmd...)
		a.printChanges(changes)
		return err
	}
}

func setupStatus(fs *flag.FlagSet) func(a *app, args []string) error {
	return func(a *app, args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		p, err := a.pi()
		if err != nil {
			return err
		}
		out, err := p.otactl("status")
		a.stdout.Write(out)
		return err
	}
}

func setupRepublish(fs *flag.FlagSet) func(a *app, args []string) error {
	channel := fs.String("channel", ota.ChannelProduction, "beta or production")
	notes := fs.String("notes", "", "the new update's notes (default: Republish of <label>)")
	return func(a *app, args []string) error {
		if len(args) != 1 || !ota.ValidLabel(args[0]) {
			return usagef("republish takes one label")
		}
		if err := validChannel(*channel); err != nil {
			return err
		}
		if err := validNotes(*notes, false); err != nil {
			return err
		}
		return a.staged(stageOptions{}, func() error { return a.republish(args[0], *channel, *notes) })
	}
}

// archivedFiles reads the seven files of an archived update.
func (a *app) archivedFiles(id string) (map[string][]byte, error) {
	files := map[string][]byte{}
	for _, name := range ota.UpdateFiles {
		b, err := os.ReadFile(a.otaArchive("updates", id, name))
		if err != nil {
			return nil, fmt.Errorf("archive: %w", err)
		}
		files[name] = b
	}
	return files, nil
}

// archivedAssets reads the assets a manifest names from the archive.
func (a *app) archivedAssets(m *ota.UpdateManifest) ([]assetFile, error) {
	var out []assetFile
	for _, name := range m.AssetNames() {
		p := a.otaArchive("assets", name)
		if err := ota.CheckAssetFile(p, name); err != nil {
			return nil, fmt.Errorf("archive: %w", err)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		out = append(out, assetFile{name: name, data: b})
	}
	return out, nil
}

// manifestExtra is a manifest's extra object.
type manifestExtra struct {
	ExpoClient json.RawMessage `json:"expoClient"`
	AM         ota.ManifestAM  `json:"am"`
}

// republish re-signs the content of an archived label as a new update with a
// new id and createdAt (D9 step 5). Phones download only the manifest: the
// assets keep their hashes.
func (a *app) republish(label, channel, notes string) error {
	p, err := a.pi()
	if err != nil {
		return err
	}
	if err := a.localURLCheck(p); err != nil {
		return err
	}
	if !a.drill() {
		if err := a.clockCheck(p); err != nil {
			return err
		}
	}
	infos, err := a.archivedInfos()
	if err != nil {
		return err
	}
	var olds []archivedUpdate
	for _, in := range infos {
		if in.Label == label {
			olds = append(olds, in)
		}
	}
	if len(olds) == 0 {
		return fmt.Errorf("the archive %s has no update with label %s", a.otaArchive("updates"), label)
	}
	if notes == "" {
		notes = "Republish of " + label
	}
	st, err := piStatus(p)
	if err != nil {
		return err
	}
	registered, err := registeredRuntimes(p)
	if err != nil {
		return err
	}
	newLabel, err := a.nextLabel(st)
	if err != nil {
		return err
	}
	createdAt := a.now().UTC().Truncate(time.Millisecond)
	type pending struct {
		id       string
		manifest []byte
		info     ota.UpdateInfo
		assets   []assetFile
	}
	var todo []pending
	for _, old := range olds {
		t := target{platform: old.Platform, runtime: old.Runtime}
		for _, r := range registered {
			if r.Platform == t.platform && r.Runtime == t.runtime {
				t.builds = r.Builds
			}
		}
		if len(t.builds) == 0 {
			return fmt.Errorf("%s runtime %s of %s is not registered on %s", t.platform, t.runtime, label, p)
		}
		if err := a.checkFloor(st, channel, t, createdAt); err != nil {
			return err
		}
		files, err := a.archivedFiles(old.id)
		if err != nil {
			return err
		}
		m, err := ota.ParseUpdateManifest(files["manifest.json"])
		if err != nil {
			return err
		}
		var extra manifestExtra
		if err := json.Unmarshal(m.Extra, &extra); err != nil {
			return err
		}
		am := ota.ManifestAM{Label: newLabel, Commit: extra.AM.Commit, Notes: notes, Critical: extra.AM.Critical}
		if m.Extra, err = marshalNoEscape(manifestExtra{ExpoClient: extra.ExpoClient, AM: am}); err != nil {
			return err
		}
		m.ID, m.CreatedAt = newUUID(), ota.FormatTime(createdAt)
		body, err := marshalNoEscape(m)
		if err != nil {
			return err
		}
		if _, err := ota.ParseUpdateManifest(body); err != nil {
			return err
		}
		assets, err := a.archivedAssets(m)
		if err != nil {
			return err
		}
		todo = append(todo, pending{id: m.ID, manifest: body, assets: assets, info: ota.UpdateInfo{Platform: old.Platform,
			Runtime: old.Runtime, Label: newLabel, Commit: am.Commit, CreatedAt: m.CreatedAt, Notes: notes, Critical: am.Critical}})
		a.printf("%s: republishing %s (update %s) as %s, update %s\n", old.Platform, label, old.id, newLabel, m.ID)
	}
	if err := a.processCheck(); err != nil {
		return err
	}
	s, err := a.signingLeaf()
	if err != nil {
		return err
	}
	var ups []*update
	for _, t := range todo {
		u, err := a.signUpdate(s, t.id, t.manifest, t.info, t.assets)
		if err != nil {
			return err
		}
		ups = append(ups, u)
	}
	if err := a.publish(p, s, ups, channel, newLabel); err != nil {
		return err
	}
	a.finish(channel, newLabel, ups[0].info.Commit, ups)
	return nil
}

func setupResignActive(fs *flag.FlagSet) func(a *app, args []string) error {
	return func(a *app, args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		return a.staged(stageOptions{}, a.resignActive)
	}
}

// resignActive re-signs, with the newest leaf, every update a pointer
// references and the last 3 updates of every platform and runtime (D4: leaf
// rotation). The signed bodies stay byte-identical; only the .sig and .leaf
// files change. RSA PKCS#1 v1.5 signatures are deterministic, so running it
// again uploads the same bytes.
func (a *app) resignActive() error {
	p, err := a.pi()
	if err != nil {
		return err
	}
	if err := a.localURLCheck(p); err != nil {
		return err
	}
	st, err := piStatus(p)
	if err != nil {
		return err
	}
	infos, err := a.archivedInfos()
	if err != nil {
		return err
	}
	want := map[string]string{} // id → why
	for _, e := range st.Pointers {
		ptr := &e.Pointer
		if ptr.State == ota.StateTestUnsigned {
			ptr = ptr.Previous
		}
		if ptr != nil && ptr.Update != "" {
			want[ptr.Update] = fmt.Sprintf("the %s/%s/%s pointer", e.Channel, e.Platform, e.Runtime)
		}
	}
	perRuntime := map[string][]archivedUpdate{}
	for _, in := range infos {
		k := in.Platform + "/" + in.Runtime
		perRuntime[k] = append(perRuntime[k], in)
	}
	for k, list := range perRuntime {
		for i := max(0, len(list)-3); i < len(list); i++ {
			if _, ok := want[list[i].id]; !ok {
				want[list[i].id] = "one of the last 3 updates of " + k
			}
		}
	}
	if len(want) == 0 {
		a.printf("No update to re-sign.\n")
		return nil
	}
	ids := make([]string, 0, len(want))
	for id := range want {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	files := map[string]map[string][]byte{}
	for _, id := range ids {
		f, err := a.archivedFiles(id)
		if err != nil {
			return fmt.Errorf("update %s (%s): %w", id, want[id], err)
		}
		files[id] = f
	}
	if err := a.processCheck(); err != nil {
		return err
	}
	s, err := a.signingLeaf()
	if err != nil {
		return err
	}
	var ups []*update
	for _, id := range ids {
		f := files[id]
		if err := a.signFiles(s, id, f); err != nil {
			return fmt.Errorf("update %s: %w", id, err)
		}
		var info ota.UpdateInfo
		if err := json.Unmarshal(f["info.json"], &info); err != nil {
			return err
		}
		for _, name := range []string{"manifest.sig", "manifest.leaf", "rollback.sig", "rollback.leaf"} {
			if err := ota.WriteFileAtomic(a.otaArchive("updates", id, name), f[name], 0o644); err != nil {
				return err
			}
		}
		ups = append(ups, &update{id: id, info: info, files: f})
		a.printf("re-signed %s (%s, %s)\n", id, info.Label, want[id])
	}
	dir, err := a.uploadDir(p, s, ups, false)
	if err != nil {
		return err
	}
	plan, err := a.ingest(p, dir, "resign")
	if err != nil {
		return err
	}
	a.printf("%s: %d update(s) now carry the signatures of leaf %s; %d were already signed by it\n", p, len(plan.Resigned), s.name, len(ups)-len(plan.Resigned))
	if a.drill() {
		return nil
	}
	// Every pointer in state update must now serve the new signature.
	var live []*update
	for _, e := range st.Pointers {
		if e.State != ota.StateUpdate {
			continue
		}
		for _, u := range ups {
			if u.id == e.Update {
				if err := a.smokeTest(s.root, e.Channel, []*update{u}); err != nil {
					return err
				}
				live = append(live, u)
			}
		}
	}
	a.printf("Smoke test passed for %d live pointer(s).\n", len(live))
	return nil
}
