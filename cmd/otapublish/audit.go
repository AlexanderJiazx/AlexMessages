package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"alexmessage/internal/ota"
)

// `otapublish audit` and `otapublish restore-pi` (D2, "Which copy is
// authoritative" and "Backup and restore"; D10, "The IPA download is not
// authenticated").
//
// The Mac archive is authoritative for the write-once files: certificates,
// assets, updates, runtimes and native builds. The Pi is authoritative for
// the pointers, revoked.txt and policy.json. The Mac's record of those is
// pointers.log (every pointer change otapublish made), and revoked.txt and
// native/policy.json in the archive. A command run on the Pi itself (halt,
// rollback-embedded, revoke, policy, test-unsigned) changes the Pi without
// the Mac: audit reports it, and audit --record stores the Pi's pointers,
// revoked ids and policy as the Mac's record once you confirm that you made
// those changes, so that restore-pi rebuilds them.

// archiveState is what the Mac archive says the Pi holds.
type archiveState struct {
	certs map[string]string // certs/<name> → SHA-256 hex
	// expired are the leaves that expired; otactl ingest skips them.
	expired map[string]bool
	assets  map[string]int64             // assets/<name> → size
	updates map[string]map[string]string // id → file → SHA-256 hex
	// unverified are the updates whose signatures no longer verify (their
	// leaf expired): history that no phone is served again.
	unverified map[string]error
	native     map[string]map[string]string // platform → name → SHA-256 hex
	runtimes   map[string]ota.RuntimeInfo   // platform/runtime
	pointers   map[string]*ota.Pointer      // channel/platform/runtime → the last pointer pointers.log set (nil: removed)
	revoked    []string
	policy     *ota.Policy
	notes      []string // archive damage found while reading
}

func pointerKey(channel, platform, runtime string) string {
	return channel + "/" + platform + "/" + runtime
}

func splitPointerKey(k string) (channel, platform, runtime string) {
	parts := strings.SplitN(k, "/", 3)
	return parts[0], parts[1], parts[2]
}

// readArchive reads the archive's mirror of data/ota and verifies every
// archived update against the archived root and leaves.
func (a *app) readArchive() (*archiveState, error) {
	s := &archiveState{certs: map[string]string{}, expired: map[string]bool{}, assets: map[string]int64{},
		updates: map[string]map[string]string{}, unverified: map[string]error{},
		native: map[string]map[string]string{}, runtimes: map[string]ota.RuntimeInfo{}, pointers: map[string]*ota.Pointer{}}
	now := a.now()

	// Certificates.
	var root *x509.Certificate
	leaves := map[string]*x509.Certificate{}
	entries, err := readDirIfExists(a.otaArchive("certs"))
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".pem") {
			continue
		}
		p := a.otaArchive("certs", e.Name())
		sum, _, err := fileSHA256(p)
		if err != nil {
			return nil, err
		}
		s.certs[e.Name()] = sum
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		certs, err := ota.ParseCertificatesPEM(b)
		if err != nil || len(certs) != 1 {
			s.notes = append(s.notes, fmt.Sprintf("the archive's certs/%s is not one certificate", e.Name()))
			continue
		}
		if e.Name() == ota.RootCertFile {
			root = certs[0]
			continue
		}
		leaves[ota.LeafSHA256(certs[0])] = certs[0]
		if now.After(certs[0].NotAfter) {
			s.expired[e.Name()] = true
		}
	}

	// Assets: the name is the content's hash.
	if entries, err = readDirIfExists(a.otaArchive("assets")); err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p := a.otaArchive("assets", e.Name())
		if err := ota.CheckAssetFile(p, e.Name()); err != nil {
			s.notes = append(s.notes, fmt.Sprintf("the archive's assets/%s is damaged: %v", e.Name(), err))
		}
		st, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		s.assets[e.Name()] = st.Size()
	}

	// Updates.
	findLeaf := func(sha string) (*x509.Certificate, error) {
		if c := leaves[sha]; c != nil {
			return c, nil
		}
		return nil, fmt.Errorf("the archive has no leaf certificate %s", sha)
	}
	if entries, err = readDirIfExists(a.otaArchive("updates")); err != nil {
		return nil, err
	}
	for _, e := range entries {
		id, ok := ota.NormalizeUUID(e.Name())
		if !ok || id != e.Name() || !e.IsDir() {
			continue
		}
		files := map[string]string{}
		contents := map[string][]byte{}
		for _, name := range ota.UpdateFiles {
			b, err := os.ReadFile(a.otaArchive("updates", id, name))
			if errors.Is(err, fs.ErrNotExist) {
				files[name] = "missing"
				continue
			}
			if err != nil {
				return nil, err
			}
			contents[name] = b
			files[name] = sha256Hex(b)
		}
		s.updates[id] = files
		if root == nil {
			s.unverified[id] = errors.New("the archive has no root certificate")
		} else if _, err := ota.VerifyUpdateFiles(id, contents, findLeaf, root, now); err != nil {
			s.unverified[id] = err
		}
	}

	// Native builds.
	for _, platform := range []string{ota.PlatformAndroid, ota.PlatformIOS} {
		s.native[platform] = map[string]string{}
		if entries, err = readDirIfExists(a.otaArchive("native", platform)); err != nil {
			return nil, err
		}
		for _, e := range entries {
			if _, _, ok := ota.ParseNativeName(platform, e.Name()); !ok {
				continue
			}
			sum, _, err := fileSHA256(a.otaArchive("native", platform, e.Name()))
			if err != nil {
				return nil, err
			}
			s.native[platform][e.Name()] = sum
		}
	}

	// Runtimes.
	for _, platform := range []string{ota.PlatformAndroid, ota.PlatformIOS} {
		if entries, err = readDirIfExists(a.otaArchive("runtimes", platform)); err != nil {
			return nil, err
		}
		for _, e := range entries {
			rt, ok := strings.CutSuffix(e.Name(), ".json")
			if !ok || !ota.ValidRuntime(rt) {
				continue
			}
			b, err := os.ReadFile(a.otaArchive("runtimes", platform, e.Name()))
			if err != nil {
				return nil, err
			}
			var info ota.RuntimeInfo
			if err := json.Unmarshal(b, &info); err != nil || info.Platform != platform || info.Runtime != rt {
				return nil, fmt.Errorf("the archive's runtimes/%s/%s is not the record of that runtime (%v)", platform, e.Name(), err)
			}
			s.runtimes[platform+"/"+rt] = info
		}
	}

	// pointers.log, oldest first: the last entry of each pointer wins.
	log, err := a.readPointersLog()
	if err != nil {
		return nil, err
	}
	for _, e := range log {
		s.pointers[pointerKey(e.Channel, e.Platform, e.Runtime)] = e.New
	}

	// revoked.txt and policy.json, as audit --record and native stored them.
	if b, err := os.ReadFile(a.otaArchive("revoked.txt")); err == nil {
		s.revoked = parseRevoked(b)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if b, err := os.ReadFile(a.otaArchive("native", "policy.json")); err == nil {
		var p ota.Policy
		if err := json.Unmarshal(b, &p); err != nil {
			return nil, fmt.Errorf("the archive's native/policy.json: %w", err)
		}
		s.policy = &p
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return s, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func readDirIfExists(dir string) ([]os.DirEntry, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return entries, err
}

func (a *app) readPointersLog() ([]pointerLogEntry, error) {
	f, err := os.Open(a.otaArchive("pointers.log"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []pointerLogEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for n := 1; sc.Scan(); n++ {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var e pointerLogEntry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return nil, fmt.Errorf("pointers.log line %d: %w", n, err)
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

// parseRevoked reads revoked.txt: one id per line, "#" starts a comment.
func parseRevoked(b []byte) []string {
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		line, _, _ = strings.Cut(line, "#")
		if id, ok := ota.NormalizeUUID(strings.TrimSpace(line)); ok {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// buildKeys are a runtime's builds without registered_at, which says when
// a store learned of the build, not what the build is.
func buildKeys(builds []ota.RuntimeBuild) []ota.RuntimeBuild {
	out := make([]ota.RuntimeBuild, len(builds))
	for i, b := range builds {
		b.RegisteredAt = ""
		out[i] = b
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Build < out[j].Build })
	return out
}

// auditReport is every difference between the archive and the Pi.
type auditReport struct {
	diffs []string
	notes []string
	// What audit --record may store, and restore-pi may restore.
	piPointers map[string]*ota.Pointer // the Pi's pointers
	piRevoked  []string
	piPolicy   *ota.Policy
}

func (r *auditReport) difff(format string, args ...any) {
	r.diffs = append(r.diffs, fmt.Sprintf(format, args...))
}

// compare lists every difference between the archive and the Pi's
// inventory.
func compare(s *archiveState, inv *inventoryJSON) *auditReport {
	r := &auditReport{notes: append([]string(nil), s.notes...), piPointers: map[string]*ota.Pointer{},
		piRevoked: append([]string(nil), inv.Revoked...), piPolicy: inv.Policy}
	sort.Strings(r.piRevoked)

	for _, name := range sortedKeys(s.certs) {
		switch pi, ok := inv.Certs[name]; {
		case !ok && s.expired[name]:
			r.notes = append(r.notes, fmt.Sprintf("certs/%s: an expired leaf, not on the Pi (otactl ingest skips expired leaves)", name))
		case !ok:
			r.difff("certs/%s: in the archive, missing on the Pi", name)
		case pi != s.certs[name]:
			r.difff("certs/%s: the Pi's file differs from the archive's", name)
		}
	}
	for _, name := range sortedKeys(inv.Certs) {
		if _, ok := s.certs[name]; !ok {
			r.difff("certs/%s: on the Pi only; the Mac never issued it", name)
		}
	}

	for _, name := range sortedKeys(s.assets) {
		switch size, ok := inv.Assets[name]; {
		case !ok:
			r.difff("assets/%s: in the archive, missing on the Pi", name)
		case size != s.assets[name]:
			r.difff("assets/%s: %d bytes on the Pi, %d in the archive", name, size, s.assets[name])
		}
	}
	for _, name := range sortedKeys(inv.Assets) {
		if _, ok := s.assets[name]; !ok {
			r.difff("assets/%s: on the Pi only", name)
		}
	}

	for _, id := range sortedKeys(s.updates) {
		pi, ok := inv.Updates[id]
		if !ok {
			if err := s.unverified[id]; err != nil {
				r.notes = append(r.notes, fmt.Sprintf("updates/%s: not on the Pi, and its signatures no longer verify (%v); it is history only", id, err))
			} else {
				r.difff("updates/%s: in the archive, missing on the Pi", id)
			}
			continue
		}
		for _, name := range ota.UpdateFiles {
			if pi[name] != s.updates[id][name] {
				r.difff("updates/%s/%s: the Pi's file differs from the archive's", id, name)
			}
		}
	}
	for _, id := range sortedKeys(inv.Updates) {
		if _, ok := s.updates[id]; !ok {
			r.difff("updates/%s: on the Pi only; the Mac never published it", id)
		}
	}

	for _, platform := range []string{ota.PlatformAndroid, ota.PlatformIOS} {
		mine, theirs := s.native[platform], inv.Native[platform]
		for _, name := range sortedKeys(mine) {
			switch sum, ok := theirs[name]; {
			case !ok:
				r.difff("native/%s/%s: in the archive, missing on the Pi", platform, name)
			case sum != mine[name]:
				r.difff("native/%s/%s: the Pi's SHA-256 is %s, the archive's is %s: do NOT install this file from /app", platform, name, sum, mine[name])
			}
		}
		for _, name := range sortedKeys(theirs) {
			if _, ok := mine[name]; !ok {
				r.difff("native/%s/%s: on the Pi only; the Mac never built it: do NOT install this file from /app", platform, name)
			}
		}
	}

	piRuntimes := map[string]ota.RuntimeInfo{}
	for _, info := range inv.Runtimes {
		piRuntimes[info.Platform+"/"+info.Runtime] = info
	}
	for _, k := range sortedKeys(s.runtimes) {
		switch pi, ok := piRuntimes[k]; {
		case !ok:
			r.difff("runtimes/%s: in the archive, not registered on the Pi", k)
		case !reflect.DeepEqual(buildKeys(pi.Builds), buildKeys(s.runtimes[k].Builds)):
			r.difff("runtimes/%s: the Pi registers builds %s, the archive %s", k, describeBuilds(pi.Builds), describeBuilds(s.runtimes[k].Builds))
		}
	}
	for _, k := range sortedKeys(piRuntimes) {
		if _, ok := s.runtimes[k]; !ok {
			r.difff("runtimes/%s: registered on the Pi only (builds %s); otapublish native never registered it", k, describeBuilds(piRuntimes[k].Builds))
		}
	}

	for _, e := range inv.Pointers {
		p := e.Pointer
		r.piPointers[pointerKey(e.Channel, e.Platform, e.Runtime)] = &p
	}
	for _, k := range sortedKeys(s.pointers) {
		mine, theirs := s.pointers[k], r.piPointers[k]
		switch {
		case mine == nil && theirs == nil:
		case theirs == nil:
			r.difff("pointer %s: the Mac set %s, and the Pi has no pointer", k, describePointer(mine))
		case !reflect.DeepEqual(mine, theirs):
			r.difff("pointer %s: the Pi has %s, but the Mac set %s: %s", k, describePointer(theirs), describePointer(mine), notSetByMac(theirs))
		}
	}
	for _, k := range sortedKeys(r.piPointers) {
		if _, ok := s.pointers[k]; !ok {
			r.difff("pointer %s: the Pi has %s, which the Mac never set: %s", k, describePointer(r.piPointers[k]), notSetByMac(r.piPointers[k]))
		}
	}

	archived := map[string]bool{}
	for _, id := range s.revoked {
		archived[id] = true
	}
	onPi := map[string]bool{}
	for _, id := range r.piRevoked {
		onPi[id] = true
		if !archived[id] {
			r.difff("revoked.txt: the Pi revokes %s, which the Mac has not recorded", id)
		}
	}
	for _, id := range s.revoked {
		if !onPi[id] {
			r.difff("revoked.txt: the Mac recorded %s as revoked, and the Pi does not revoke it", id)
		}
	}

	switch {
	case s.policy == nil && inv.Policy == nil:
	case inv.Policy == nil:
		r.difff("native/policy.json: in the archive, missing on the Pi")
	case s.policy == nil:
		r.difff("native/policy.json: on the Pi only")
	default:
		for _, platform := range []string{ota.PlatformAndroid, ota.PlatformIOS} {
			mine, theirs := s.policy.For(platform), inv.Policy.For(platform)
			if !reflect.DeepEqual(mine, theirs) {
				r.difff("native/policy.json %s: the Pi has %s, the archive %s", platform, describePolicy(theirs), describePolicy(mine))
			}
		}
	}
	return r
}

func describeBuilds(builds []ota.RuntimeBuild) string {
	var parts []string
	for _, b := range buildKeys(builds) {
		parts = append(parts, fmt.Sprintf("%d (embedded %s, commit %s)", b.Build, b.EmbeddedUpdateID, short(b.Commit)))
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

func describePointer(p *ota.Pointer) string {
	if p == nil {
		return "no pointer"
	}
	s := describe(p) + " since " + p.Since
	if p.State == ota.StateTestUnsigned {
		s += " until " + p.Until + " (previous: " + describePointer(p.Previous) + ")"
	}
	return s
}

// notSetByMac explains a pointer that pointers.log does not explain.
func notSetByMac(p *ota.Pointer) string {
	switch p.State {
	case ota.StateRollback:
		return "a rollback the Mac did not set (otactl rollback-embedded on the Pi, or someone with access to it)"
	case ota.StateHalted:
		return "a halt the Mac did not set (otactl halt on the Pi, or someone with access to it)"
	case ota.StateTestUnsigned:
		return "otactl test-unsigned is running (plan step 6.4); otactl test-unsigned --clear ends it"
	}
	return "a change the Mac did not make"
}

func describePolicy(p *ota.PlatformPolicy) string {
	s := fmt.Sprintf("min_build %d, hard_gate %v", p.MinBuild, p.HardGate)
	if p.Latest == nil {
		return s + ", no latest build"
	}
	return s + fmt.Sprintf(", latest build %d (%s, SHA-256 %s)", p.Latest.Build, p.Latest.URL, p.Latest.SHA256)
}

// auditFailed is audit's result when the copies differ: exit status 1.
type auditFailed int

func (n auditFailed) Error() string {
	return fmt.Sprintf("%d difference(s) between the Mac archive and the Pi", int(n))
}

func setupAudit(fs *flag.FlagSet) func(a *app, args []string) error {
	record := fs.Bool("record", false, "after you confirm, store the Pi's pointers, revoked ids and policy as the Mac's record")
	return func(a *app, args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		return a.audit(*record)
	}
}

func (a *app) audit(record bool) error {
	p, err := a.pi()
	if err != nil {
		return err
	}
	if ok, err := p.ready(); err != nil || !ok {
		if err == nil {
			err = fmt.Errorf("%s has no update store: nothing to audit", p)
		}
		return err
	}
	s, err := a.readArchive()
	if err != nil {
		return err
	}
	inv, err := piInventory(p)
	if err != nil {
		return err
	}
	r := compare(s, inv)
	a.printf("Audit of %s against the archive %s:\n", p, a.otaArchive())
	for _, n := range r.notes {
		a.printf("  note: %s\n", n)
	}
	for _, d := range r.diffs {
		a.printf("  DIFFERENCE: %s\n", d)
	}
	a.printf("Checked %d certificate(s), %d asset(s), %d update(s), %d+%d native build(s), %d runtime(s), %d pointer(s).\n",
		len(s.certs), len(s.assets), len(s.updates), len(s.native[ota.PlatformAndroid]), len(s.native[ota.PlatformIOS]),
		len(s.runtimes), len(r.piPointers))
	if record {
		if err := a.record(s, r); err != nil {
			return err
		}
		if s, err = a.readArchive(); err != nil {
			return err
		}
		if r = compare(s, inv); len(r.diffs) > 0 {
			a.printf("Still different after recording (the archive stays authoritative for the write-once files, and a loss on the Pi is never recorded: restore-pi puts it back):\n")
			for _, d := range r.diffs {
				a.printf("  DIFFERENCE: %s\n", d)
			}
		}
	}
	if len(r.diffs) > 0 {
		return auditFailed(len(r.diffs))
	}
	a.printf("No differences: the Pi matches the Mac archive.\n")
	return nil
}

// record stores the Pi's pointers, revoked ids and policy as the Mac's
// record. It never records a loss (a pointer, revoked id or policy the Pi
// no longer has: restore-pi puts those back) and never touches the
// write-once files, for which the archive stays authoritative.
func (a *app) record(s *archiveState, r *auditReport) error {
	var changes []pointerChange
	for _, k := range sortedKeys(r.piPointers) {
		theirs := r.piPointers[k]
		if mine, ok := s.pointers[k]; ok && reflect.DeepEqual(mine, theirs) {
			continue
		}
		c, pl, rt := splitPointerKey(k)
		changes = append(changes, pointerChange{Channel: c, Platform: pl, Runtime: rt, Old: s.pointers[k], New: theirs})
	}
	revoked := map[string]bool{}
	for _, id := range s.revoked {
		revoked[id] = true
	}
	var newRevoked []string
	for _, id := range r.piRevoked {
		if !revoked[id] {
			newRevoked = append(newRevoked, id)
		}
	}
	recordPolicy := r.piPolicy != nil && (s.policy == nil || !reflect.DeepEqual(*s.policy, *r.piPolicy))
	if len(changes) == 0 && len(newRevoked) == 0 && !recordPolicy {
		a.printf("Nothing to record: the Mac's record of the pointers, revoked ids and policy matches the Pi.\n")
		return nil
	}
	var lines []string
	for _, c := range changes {
		lines = append(lines, fmt.Sprintf("pointer %s: %s (the Mac's record: %s)", pointerKey(c.Channel, c.Platform, c.Runtime), describePointer(c.New), describePointer(c.Old)))
	}
	for _, id := range newRevoked {
		lines = append(lines, "revoked: "+id)
	}
	if recordPolicy {
		for _, platform := range []string{ota.PlatformAndroid, ota.PlatformIOS} {
			lines = append(lines, fmt.Sprintf("policy %s: %s", platform, describePolicy(r.piPolicy.For(platform))))
		}
	}
	if !a.confirm("Record this state of the Pi as the Mac's:\n  " + strings.Join(lines, "\n  ") +
		"\nRecord it only if you made these changes yourself (an emergency command or otactl policy on the Pi): restore-pi rebuilds the Pi from this record.") {
		return errors.New("not confirmed; nothing was recorded")
	}
	if err := a.logPointers("record", "", changes); err != nil {
		return err
	}
	if len(newRevoked) > 0 {
		all := append(append([]string(nil), s.revoked...), newRevoked...)
		sort.Strings(all)
		body := "# Revoked on the Pi; recorded by otapublish audit --record\n" + strings.Join(all, "\n") + "\n"
		if err := ota.WriteFileAtomic(a.otaArchive("revoked.txt"), []byte(body), 0o644); err != nil {
			return err
		}
	}
	if recordPolicy {
		b, err := json.MarshalIndent(r.piPolicy, "", "  ")
		if err != nil {
			return err
		}
		if err := os.MkdirAll(a.otaArchive("native"), 0o755); err != nil {
			return err
		}
		if err := ota.WriteFileAtomic(a.otaArchive("native", "policy.json"), append(b, '\n'), 0o644); err != nil {
			return err
		}
	}
	policyNote := "no policy change"
	if recordPolicy {
		policyNote = "the policy"
	}
	a.printf("Recorded %d pointer(s), %d revoked id(s) and %s.\n", len(changes), len(newRevoked), policyNote)
	return nil
}

func setupRestorePi(fs *flag.FlagSet) func(a *app, args []string) error {
	return func(a *app, args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		return a.restorePi()
	}
}

// restorePi rebuilds the Pi's store from the Mac archive after a lost SD
// card: it uploads every write-once file the Pi lacks, registers the
// archived native builds, adds the recorded revoked ids, writes the policy
// when the Pi has none, and writes every pointer that pointers.log ends
// with and the Pi lacks. It never replaces anything the Pi has: the Pi
// stays authoritative for the pointers, revoked.txt and policy.json.
func (a *app) restorePi() error {
	p, err := a.pi()
	if err != nil {
		return err
	}
	if ok, err := p.ready(); err != nil || !ok {
		if err == nil {
			err = fmt.Errorf("%s has no update store: run bin/otactl init on it first", p)
		}
		return err
	}
	s, err := a.readArchive()
	if err != nil {
		return err
	}
	inv, err := piInventory(p)
	if err != nil {
		return err
	}
	r := compare(s, inv)

	// The write-once files the Pi lacks. A file the Pi holds with other
	// bytes stops the restore: that is damage or tampering to look at with
	// audit, not something to paper over.
	up, err := a.tempDir("restore")
	if err != nil {
		return err
	}
	var conflicts []string
	upload := 0
	add := func(rel string) error {
		upload++
		dst := filepath.Join(up, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return copyFile(a.otaArchive(filepath.FromSlash(rel)), dst)
	}
	for _, name := range sortedKeys(s.certs) {
		if sum, ok := inv.Certs[name]; ok && sum != s.certs[name] {
			conflicts = append(conflicts, "certs/"+name)
		} else if !ok && !s.expired[name] {
			if err := add("certs/" + name); err != nil {
				return err
			}
		}
	}
	for _, name := range sortedKeys(s.assets) {
		if size, ok := inv.Assets[name]; ok && size != s.assets[name] {
			conflicts = append(conflicts, "assets/"+name)
		} else if !ok {
			if err := add("assets/" + name); err != nil {
				return err
			}
		}
	}
	restorable := map[string]bool{}
	for _, id := range sortedKeys(s.updates) {
		if pi, ok := inv.Updates[id]; ok {
			restorable[id] = true
			for _, name := range ota.UpdateFiles {
				if pi[name] != s.updates[id][name] {
					conflicts = append(conflicts, "updates/"+id+"/"+name)
				}
			}
			continue
		}
		if s.unverified[id] != nil {
			continue
		}
		restorable[id] = true
		for _, name := range ota.UpdateFiles {
			if err := add("updates/" + id + "/" + name); err != nil {
				return err
			}
		}
	}
	for _, platform := range []string{ota.PlatformAndroid, ota.PlatformIOS} {
		for _, name := range sortedKeys(s.native[platform]) {
			if sum, ok := inv.Native[platform][name]; ok && sum != s.native[platform][name] {
				conflicts = append(conflicts, "native/"+platform+"/"+name)
			} else if !ok {
				if err := add("native/" + platform + "/" + name); err != nil {
					return err
				}
			}
		}
	}
	if len(conflicts) > 0 {
		return fmt.Errorf("%s holds files that differ from the archive: %s; run otapublish audit and find out why before restoring", p, strings.Join(conflicts, ", "))
	}

	// The native builds to register, oldest first on each platform:
	// otactl registers a build only above the highest one.
	type reg struct {
		platform, runtime string
		b                 ota.RuntimeBuild
	}
	var regs []reg
	piRuntimes := map[string]ota.RuntimeInfo{}
	for _, info := range inv.Runtimes {
		piRuntimes[info.Platform+"/"+info.Runtime] = info
	}
	for _, k := range sortedKeys(s.runtimes) {
		info := s.runtimes[k]
		have := map[ota.RuntimeBuild]bool{}
		for _, b := range buildKeys(piRuntimes[k].Builds) {
			have[b] = true
		}
		for _, b := range buildKeys(info.Builds) {
			if !have[b] {
				regs = append(regs, reg{info.Platform, info.Runtime, b})
			}
		}
	}
	sort.SliceStable(regs, func(i, j int) bool {
		if regs[i].platform != regs[j].platform {
			return regs[i].platform < regs[j].platform
		}
		return regs[i].b.Build < regs[j].b.Build
	})

	// The revoked ids, the policy and the pointers the Pi lacks.
	onPi := map[string]bool{}
	for _, id := range r.piRevoked {
		onPi[id] = true
	}
	var revoke []string
	for _, id := range s.revoked {
		if !onPi[id] {
			revoke = append(revoke, id)
		}
	}
	restorePolicy := s.policy != nil && inv.Policy == nil
	type ptr struct {
		key string
		p   *ota.Pointer
	}
	var pointers []ptr
	var skipped []string
	for _, k := range sortedKeys(s.pointers) {
		want := s.pointers[k]
		if want == nil {
			continue
		}
		if have := r.piPointers[k]; have != nil {
			if !reflect.DeepEqual(have, want) {
				skipped = append(skipped, fmt.Sprintf("pointer %s: the Pi has %s; left as it is (the Mac set %s)", k, describePointer(have), describePointer(want)))
			}
			continue
		}
		if want.State == ota.StateTestUnsigned {
			// The unsigned test copy is not archived; put back what the
			// test replaced.
			if want = want.Previous; want == nil {
				continue
			}
		}
		if want.Update != "" && !restorable[want.Update] {
			skipped = append(skipped, fmt.Sprintf("pointer %s: not restored; its update %s cannot be restored (%v): publish or republish instead", k, want.Update, s.unverified[want.Update]))
			continue
		}
		pointers = append(pointers, ptr{k, want})
	}

	a.printf("Restore of %s from the archive %s:\n", p, a.otaArchive())
	a.printf("  upload %d file(s) the Pi lacks\n", upload)
	for _, g := range regs {
		a.printf("  register %s build %d for runtime %s\n", g.platform, g.b.Build, g.runtime)
	}
	if len(revoke) > 0 {
		a.printf("  revoke %s\n", strings.Join(revoke, ", "))
	}
	if restorePolicy {
		for _, platform := range []string{ota.PlatformAndroid, ota.PlatformIOS} {
			a.printf("  policy %s: %s\n", platform, describePolicy(s.policy.For(platform)))
		}
	}
	for _, x := range pointers {
		a.printf("  pointer %s: %s\n", x.key, describePointer(x.p))
	}
	for _, line := range skipped {
		a.printf("  %s\n", line)
	}
	if upload == 0 && len(regs) == 0 && len(revoke) == 0 && !restorePolicy && len(pointers) == 0 {
		a.printf("Nothing to restore: the Pi lacks nothing that the archive has.\n")
		return nil
	}
	if !a.confirm("The pointers come from pointers.log, which has every pointer change otapublish made and the states that audit --record stored.\n" +
		"It does not have a halt or rollback-embedded that was run on the Pi after the last audit --record: restoring would serve that update again.\n" +
		"Restore the Pi as listed?") {
		return errors.New("not confirmed; nothing was restored")
	}

	if upload > 0 {
		plan, err := a.ingest(p, up, "restore")
		if err != nil {
			return err
		}
		a.printf("%s: ingested %d certificate(s), %d asset(s), %d update(s), %d native build(s); %d unchanged\n",
			p, len(plan.Leaves)+boolInt(plan.Root), len(plan.Assets), len(plan.Updates), len(plan.Native), plan.Unchanged)
	}
	for _, g := range regs {
		if _, err := p.otactl("register-runtime", "--platform", g.platform, "--runtime", g.runtime, "--build", strconv.Itoa(g.b.Build),
			"--version", g.b.Version, "--embedded-update-id", g.b.EmbeddedUpdateID, "--commit-time", g.b.CommitTime, "--commit", g.b.Commit); err != nil {
			return err
		}
	}
	if len(revoke) > 0 {
		if _, err := p.otactl(append(append([]string{"revoke"}, revoke...), "--note", "restored from the Mac archive by otapublish restore-pi")...); err != nil {
			return err
		}
	}
	if restorePolicy {
		if err := a.restorePolicy(p, s.policy); err != nil {
			return err
		}
	}
	var errs []error
	for _, x := range pointers {
		c, pl, rt := splitPointerKey(x.key)
		b, err := json.Marshal(x.p)
		if err != nil {
			return err
		}
		changes, err := a.pointerChanges(p, "restore-pi", "", "restore-pointer", "--channel", c, "--platform", pl, "--runtime", rt, "--pointer", string(b), "--json")
		a.printChanges(changes)
		if err != nil {
			errs = append(errs, err)
		}
	}
	out, derr := p.otactl("doctor")
	a.stdout.Write(out)
	if derr != nil {
		errs = append(errs, fmt.Errorf("otactl doctor found problems after the restore: %w", derr))
	}
	if len(skipped) > 0 {
		errs = append(errs, fmt.Errorf("%d pointer(s) were not restored; see the list above", len(skipped)))
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	a.printf("Restored. Run otapublish audit to compare the Pi with the archive.\n")
	return nil
}

// restorePolicy writes the archived policy onto a Pi that has none: the
// latest build of each platform (its URL keeps the archived origin), then
// min_build and hard_gate.
func (a *app) restorePolicy(p pi, policy *ota.Policy) error {
	for _, platform := range []string{ota.PlatformAndroid, ota.PlatformIOS} {
		pp := policy.For(platform)
		if l := pp.Latest; l != nil {
			u, err := url.Parse(l.URL)
			if err != nil || u.Scheme == "" || u.Host == "" {
				return fmt.Errorf("the archived policy's %s latest URL %q has no origin", platform, l.URL)
			}
			if _, err := p.otactl("policy", "--platform", platform, "--latest-build", strconv.Itoa(l.Build), "--notes", l.Notes,
				"--base-url", u.Scheme+"://"+u.Host); err != nil {
				return err
			}
		}
		if pp.MinBuild != 0 || pp.HardGate {
			if _, err := p.otactl("policy", "--platform", platform, "--min-build", strconv.Itoa(pp.MinBuild),
				"--hard-gate="+strconv.FormatBool(pp.HardGate), "--yes"); err != nil {
				return err
			}
		}
	}
	return nil
}

// sha256Hex is the lowercase hex SHA-256 of b.
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
