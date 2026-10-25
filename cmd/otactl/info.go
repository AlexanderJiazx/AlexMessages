package main

import (
	"database/sql"
	"debug/buildinfo"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"alexmessage/internal/ota"
	"alexmessage/internal/ota/census"
)

// The read-only commands: status, census, runtimes, version, doctor and
// inventory. None of them takes the lock or writes anything.

// pointerStatus is a pointer as status --json prints it.
type pointerStatus struct {
	ota.PointerEntry
	Label     string `json:"label,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

func (a *app) pointerStatuses() ([]pointerStatus, error) {
	pointers, err := a.store.ListPointers()
	if err != nil {
		return nil, err
	}
	out := []pointerStatus{}
	for _, e := range pointers {
		ps := pointerStatus{PointerEntry: e}
		if e.Update != "" && e.State != ota.StateTestUnsigned {
			if info, err := a.store.ReadUpdateInfo(e.Update); err == nil {
				ps.Label, ps.CreatedAt = info.Label, info.CreatedAt
			}
		}
		out = append(out, ps)
	}
	return out, nil
}

// openCensus opens data/ota/census.db for reading; nil when it does not
// exist yet (no phone has checked for updates).
func (a *app) openCensus() (*census.DB, error) {
	return census.Shared(a.store.Path(census.FileName), false)
}

// buildKey names a registered native build by its embedded update id.
type buildKey struct {
	Platform string
	Runtime  string
	Build    int
	Version  string
}

func (a *app) buildsByEmbeddedID() (map[string]buildKey, error) {
	rts, err := a.store.ListRuntimes()
	if err != nil {
		return nil, err
	}
	m := map[string]buildKey{}
	for _, rt := range rts {
		for _, b := range rt.Builds {
			m[b.EmbeddedUpdateID] = buildKey{rt.Platform, rt.Runtime, b.Build, b.Version}
		}
	}
	return m, nil
}

// censusBuilds describes the native builds that the census shows, one line
// per build: what rollback-embedded prints before it asks for yes.
func (a *app) censusBuilds() ([]string, error) {
	cdb, err := a.openCensus()
	if err != nil {
		return nil, err
	}
	if cdb == nil {
		return []string{"the census is empty: no install has checked for updates yet"}, nil
	}
	installs, err := cdb.List()
	if err != nil {
		return nil, err
	}
	builds, err := a.buildsByEmbeddedID()
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, in := range installs {
		key := fmt.Sprintf("%s, embedded update %s (not a registered build), runtime %s", in.Platform, short(in.EmbeddedUpdateID), in.Runtime)
		if b, ok := builds[in.EmbeddedUpdateID]; ok {
			key = fmt.Sprintf("%s build %d (%s), runtime %s", b.Platform, b.Build, b.Version, b.Runtime)
		}
		counts[key]++
	}
	if len(counts) == 0 {
		return []string{"the census is empty: no install has checked for updates yet"}, nil
	}
	var lines []string
	for _, k := range sortedKeys(counts) {
		lines = append(lines, fmt.Sprintf("%s: %d install(s)", k, counts[k]))
	}
	return lines, nil
}

// runtimeSummary counts the census installs of one platform and runtime.
type runtimeSummary struct {
	Platform string         `json:"platform"`
	Runtime  string         `json:"runtime"`
	Installs int            `json:"installs"`
	Running  map[string]int `json:"running"` // label, "built-in", or an unknown update id
	Failed   int            `json:"failed"`  // installs that reported a failed update or a fatal error
}

func (a *app) censusSummary() ([]runtimeSummary, int, error) {
	cdb, err := a.openCensus()
	if err != nil || cdb == nil {
		return []runtimeSummary{}, 0, err
	}
	installs, err := cdb.List()
	if err != nil {
		return nil, 0, err
	}
	byKey := map[string]*runtimeSummary{}
	for _, in := range installs {
		k := in.Platform + "/" + in.Runtime
		s := byKey[k]
		if s == nil {
			s = &runtimeSummary{Platform: in.Platform, Runtime: in.Runtime, Running: map[string]int{}}
			byKey[k] = s
		}
		s.Installs++
		s.Running[a.runningName(in.CurrentUpdateID, in.EmbeddedUpdateID)]++
		if len(in.FailedUpdateIDs) > 0 || in.FatalError != "" {
			s.Failed++
		}
	}
	out := []runtimeSummary{}
	for _, k := range sortedKeys(byKey) {
		out = append(out, *byKey[k])
	}
	return out, len(installs), nil
}

// runningName names what an install runs: built-in, a label, or the id.
func (a *app) runningName(current, embedded string) string {
	if current == "" || current == embedded {
		return "built-in"
	}
	if label := a.labelOf(current); label != "" {
		return label + " (" + short(current) + ")"
	}
	return current
}

func setupStatus(fs *flag.FlagSet) func(a *app, args []string) error {
	return func(a *app, args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		return a.status()
	}
}

func (a *app) status() error {
	pointers, err := a.pointerStatuses()
	if err != nil {
		return err
	}
	revoked, err := a.store.Revoked()
	if err != nil {
		return err
	}
	summary, total, err := a.censusSummary()
	if err != nil {
		return err
	}
	var warnings []string
	now := a.now()
	for _, p := range pointers {
		if p.State == ota.StateTestUnsigned {
			warnings = append(warnings, fmt.Sprintf("%s/%s/%s is in state test_unsigned (until %s); clear it with otactl test-unsigned --clear", p.Channel, p.Platform, p.Runtime, p.Until))
		}
		if p.State == ota.StateUpdate && contains(revoked, p.Update) {
			warnings = append(warnings, fmt.Sprintf("%s/%s/%s serves revoked update %s: the server answers 204 there", p.Channel, p.Platform, p.Runtime, p.Update))
		}
	}
	if a.json {
		if revoked == nil {
			revoked = []string{}
		}
		if warnings == nil {
			warnings = []string{}
		}
		store, err := filepath.Abs(a.store.Dir)
		if err != nil {
			return err
		}
		// store: otapublish uploads into <store>/incoming/.
		return a.printJSON(map[string]any{
			"time": now.UTC().Format(time.RFC3339), "store": store, "pointers": pointers, "revoked": revoked,
			"census": map[string]any{"installs": total, "by_runtime": summary}, "warnings": warnings,
		})
	}
	a.printf("Pointers:\n")
	if len(pointers) == 0 {
		a.printf("  none: every phone gets 204 (no update)\n")
	}
	for _, p := range pointers {
		a.printf("  %s/%s/%s: %s, since %s\n", p.Channel, p.Platform, p.Runtime, a.describe(&p.Pointer), p.Since)
	}
	a.printf("Revoked updates: %d\n", len(revoked))
	for _, id := range revoked {
		a.printf("  %s\n", id)
	}
	a.printf("Census: %d install(s)\n", total)
	for _, s := range summary {
		a.printf("  %s/%s: %d install(s), %d with a failure report\n", s.Platform, s.Runtime, s.Installs, s.Failed)
		for _, name := range sortedKeys(s.Running) {
			a.printf("    %s: %d\n", name, s.Running[name])
		}
	}
	for _, w := range warnings {
		a.printf("WARNING: %s\n", w)
	}
	return nil
}

// censusInstall is a census row with what otactl knows about it.
type censusInstall struct {
	census.Install
	Build    int    `json:"build"` // 0: the embedded id is not a registered build
	Version  string `json:"version"`
	Running  string `json:"running"`
	Username string `json:"username"`
}

// userApps is what the census and the sessions table say about one approved
// user's phones (step 6.7: which users still lack an app from build 2 on).
type userApps struct {
	Username              string          `json:"username"`
	CensusInstalls        int             `json:"census_installs"`
	AppSessions           []sessionClient `json:"app_sessions"`
	SessionsWithoutClient int             `json:"sessions_without_client"`
	HasClientHeader       bool            `json:"has_x_am_client"`
}

type sessionClient struct {
	Client string `json:"client"`
	SeenAt *int64 `json:"seen_at"`
}

func setupCensus(fs *flag.FlagSet) func(a *app, args []string) error {
	return func(a *app, args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		installs := []censusInstall{}
		cdb, err := a.openCensus()
		if err != nil {
			return err
		}
		builds, err := a.buildsByEmbeddedID()
		if err != nil {
			return err
		}
		names, users, dbErr := a.readUsers()
		if cdb != nil {
			rows, err := cdb.List()
			if err != nil {
				return err
			}
			for _, in := range rows {
				ci := censusInstall{Install: in, Running: a.runningName(in.CurrentUpdateID, in.EmbeddedUpdateID)}
				if b, ok := builds[in.EmbeddedUpdateID]; ok {
					ci.Build, ci.Version = b.Build, b.Version
				}
				if in.UserID != nil {
					ci.Username = names[*in.UserID]
					for _, u := range users {
						if u.Username == ci.Username && ci.Username != "" {
							u.CensusInstalls++
						}
					}
				}
				installs = append(installs, ci)
			}
		}
		if a.json {
			out := map[string]any{"installs": installs, "users": users}
			if dbErr != nil {
				out["users_error"] = dbErr.Error()
			}
			return a.printJSON(out)
		}
		a.printf("Installs in the census: %d (every field is the phone's own claim)\n", len(installs))
		for _, in := range installs {
			build := "embedded update " + short(in.EmbeddedUpdateID) + " (not registered)"
			if in.Build > 0 {
				build = fmt.Sprintf("build %d (%s)", in.Build, in.Version)
			}
			user := "no user claimed"
			if in.Username != "" {
				user = "user " + in.Username
			} else if in.UserID != nil {
				user = fmt.Sprintf("user id %d (unknown)", *in.UserID)
			}
			a.printf("  %s %s, %s, running %s, channel %s, %s, last seen %s\n", in.Platform, short(in.ClientID), build,
				in.Running, in.Channel, user, time.Unix(in.LastSeen, 0).UTC().Format(time.RFC3339))
			if len(in.FailedUpdateIDs) > 0 {
				a.printf("    failed updates: %s\n", strings.Join(in.FailedUpdateIDs, ", "))
			}
			if in.FatalError != "" {
				a.printf("    fatal error: %q\n", in.FatalError)
			}
		}
		if dbErr != nil {
			a.printf("Users: not read (%v)\n", dbErr)
			return nil
		}
		a.printf("Approved users: %d\n", len(users))
		for _, u := range users {
			state := "NO app session with X-AM-Client (no app from build 2 on has signed in)"
			if u.HasClientHeader {
				state = "app from build 2 on"
			}
			a.printf("  %s: %s; %d census install(s); %d session(s) without X-AM-Client (web, or an app before build 2)\n",
				u.Username, state, u.CensusInstalls, u.SessionsWithoutClient)
			for _, s := range u.AppSessions {
				seen := "?"
				if s.SeenAt != nil {
					seen = time.Unix(*s.SeenAt, 0).UTC().Format(time.RFC3339)
				}
				a.printf("    %s (seen %s)\n", s.Client, seen)
			}
		}
		return nil
	}
}

// readUsers reads every approved user with their sessions' X-AM-Client
// values from alexmessage.db, read-only.
func (a *app) readUsers() (map[int64]string, []*userApps, error) {
	names := map[int64]string{}
	users := []*userApps{}
	if _, err := os.Stat(a.dbPath()); err != nil {
		return names, users, err
	}
	pool, err := sql.Open("sqlite", "file:"+a.dbPath()+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return names, users, err
	}
	defer pool.Close()
	rows, err := pool.Query(`SELECT id, username, status FROM users ORDER BY username`)
	if err != nil {
		return names, users, err
	}
	byID := map[int64]*userApps{}
	for rows.Next() {
		var id int64
		var username, status string
		if err := rows.Scan(&id, &username, &status); err != nil {
			rows.Close()
			return names, users, err
		}
		names[id] = username
		if status == "approved" {
			u := &userApps{Username: username, AppSessions: []sessionClient{}}
			byID[id] = u
			users = append(users, u)
		}
	}
	rows.Close()
	rows, err = pool.Query(`SELECT user_id, client, client_seen_at FROM sessions ORDER BY client_seen_at DESC`)
	if err != nil {
		return names, users, err
	}
	defer rows.Close()
	for rows.Next() {
		var uid int64
		var client string
		var seen sql.NullInt64
		if err := rows.Scan(&uid, &client, &seen); err != nil {
			return names, users, err
		}
		u := byID[uid]
		if u == nil {
			continue
		}
		if _, ok := ota.ParseClientInfo(client); !ok {
			u.SessionsWithoutClient++
			continue
		}
		sc := sessionClient{Client: client}
		if seen.Valid {
			sc.SeenAt = &seen.Int64
		}
		u.AppSessions = append(u.AppSessions, sc)
		u.HasClientHeader = true
	}
	return names, users, rows.Err()
}

func setupRuntimes(fs *flag.FlagSet) func(a *app, args []string) error {
	return func(a *app, args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		rts, err := a.store.ListRuntimes()
		if err != nil {
			return err
		}
		if a.json {
			if rts == nil {
				rts = []ota.RuntimeInfo{}
			}
			return a.printJSON(map[string]any{"runtimes": rts})
		}
		if len(rts) == 0 {
			a.printf("no runtime is registered\n")
		}
		for _, rt := range rts {
			a.printf("%s %s\n", rt.Platform, rt.Runtime)
			for _, b := range rt.Builds {
				a.printf("  build %d (%s), embedded update %s, built %s from %s\n", b.Build, b.Version, b.EmbeddedUpdateID, b.CommitTime, b.Commit)
			}
		}
		return nil
	}
}

// ldCommitRE finds -X main.commit=<sha> in the recorded -ldflags.
var ldCommitRE = regexp.MustCompile(`-X[= ]main\.commit=([0-9A-Za-z._-]+)`)

// binaryVersion is what version reports about one binary.
type binaryVersion struct {
	Commit      string `json:"commit"` // "unknown" when the build info names none
	VCSRevision string `json:"vcs_revision,omitempty"`
	VCSModified bool   `json:"vcs_modified,omitempty"` // built from a tree with uncommitted changes
	Go          string `json:"go,omitempty"`
	Error       string `json:"error,omitempty"`
}

func readBinaryVersion(path string) binaryVersion {
	bi, err := buildinfo.ReadFile(path)
	if err != nil {
		return binaryVersion{Commit: "unknown", Error: err.Error()}
	}
	return versionOf(bi)
}

// versionOf reads the commit out of a binary's build info. The Deploy
// workflow builds with -trimpath, and Go then leaves -ldflags out of the
// build info, so -X main.commit cannot be read back from a deployed binary;
// vcs.revision, which go build stamps from the git checkout, can. A recorded
// -X main.commit (a build without -trimpath) wins, because it is what the
// binary itself logs at start.
func versionOf(bi *debug.BuildInfo) binaryVersion {
	v := binaryVersion{Commit: "unknown", Go: bi.GoVersion}
	ld := ""
	for _, s := range bi.Settings {
		switch s.Key {
		case "-ldflags":
			if m := ldCommitRE.FindStringSubmatch(s.Value); m != nil {
				ld = m[1]
			}
		case "vcs.revision":
			v.VCSRevision = s.Value
		case "vcs.modified":
			v.VCSModified = s.Value == "true"
		}
	}
	switch {
	case ld != "":
		v.Commit = ld
	case v.VCSRevision != "":
		v.Commit = v.VCSRevision
	}
	return v
}

// selfVersion is this otactl's own version: main.commit, else its build info.
func selfVersion() binaryVersion {
	v := binaryVersion{Commit: commit}
	if bi, ok := debug.ReadBuildInfo(); ok {
		v = versionOf(bi)
		if commit != "unknown" {
			v.Commit = commit
		}
	}
	return v
}

func setupVersion(fs *flag.FlagSet) func(a *app, args []string) error {
	bin := fs.String("bin", "", "the directory of server, admin and meet (default: this executable's directory)")
	return func(a *app, args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		dir := *bin
		if dir == "" {
			dir = filepath.Dir(a.exe)
		}
		out := map[string]binaryVersion{"otactl": selfVersion()}
		for _, name := range []string{"server", "admin", "meet"} {
			out[name] = readBinaryVersion(filepath.Join(dir, name))
		}
		if a.json {
			return a.printJSON(out)
		}
		for _, name := range []string{"otactl", "server", "admin", "meet"} {
			v := out[name]
			if v.Error != "" {
				a.printf("%s: unreadable (%s)\n", name, v.Error)
				continue
			}
			modified := ""
			if v.VCSModified {
				modified = ", built from a tree with uncommitted changes"
			}
			a.printf("%s: commit %s%s\n", name, v.Commit, modified)
		}
		return nil
	}
}

// doctor checks the store and prints one line per finding; it fails when
// there is an error (OTAUpdatePlan.md, step 2.1).
type doctorReport struct {
	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`
	OK       []string `json:"ok"`
}

func (r *doctorReport) errorf(f string, args ...any) {
	r.Errors = append(r.Errors, fmt.Sprintf(f, args...))
}

func (r *doctorReport) warnf(f string, args ...any) {
	r.Warnings = append(r.Warnings, fmt.Sprintf(f, args...))
}

func (r *doctorReport) okf(f string, args ...any) {
	r.OK = append(r.OK, fmt.Sprintf(f, args...))
}

// minFree: doctor warns below this much free space (D2, "Disk").
const minFree = 1 << 30

func setupDoctor(fs *flag.FlagSet) func(a *app, args []string) error {
	return func(a *app, args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		r := a.doctor()
		if a.json {
			if err := a.printJSON(r); err != nil {
				return err
			}
		} else {
			for _, s := range r.OK {
				a.printf("ok    %s\n", s)
			}
			for _, s := range r.Warnings {
				a.printf("WARN  %s\n", s)
			}
			for _, s := range r.Errors {
				a.printf("ERROR %s\n", s)
			}
		}
		if len(r.Errors) > 0 {
			return fmt.Errorf("%d error(s)", len(r.Errors))
		}
		return nil
	}
}

func (a *app) doctor() *doctorReport {
	r := &doctorReport{Errors: []string{}, Warnings: []string{}, OK: []string{}}
	now := a.now()
	r.okf("store %s, format %s", a.store.Dir, ota.FormatVersion)

	root, err := a.store.ReadRoot()
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			r.warnf("no root certificate yet (certs/%s); otapublish keys upload-certs installs it", ota.RootCertFile)
		} else {
			r.errorf("root certificate: %v", err)
		}
		root = nil
	} else if err := ota.CheckRoot(root, now); err != nil {
		r.errorf("%v", err)
		root = nil
	} else {
		r.okf("root certificate %q valid until %s", root.Subject.CommonName, root.NotAfter.UTC().Format(time.RFC3339))
	}

	if root != nil {
		entries, _ := os.ReadDir(a.store.Path("certs"))
		for _, e := range entries {
			if !ota.ValidLeafFileName(e.Name()) {
				continue
			}
			b, err := os.ReadFile(a.store.Path("certs", e.Name()))
			certs, perr := ota.ParseCertificatesPEM(b)
			switch {
			case err != nil:
				r.errorf("certs/%s: %v", e.Name(), err)
			case perr != nil || len(certs) != 1:
				r.errorf("certs/%s is not one certificate", e.Name())
			case now.After(certs[0].NotAfter):
				r.okf("certs/%s expired at %s (kept for the record)", e.Name(), certs[0].NotAfter.UTC().Format(time.RFC3339))
			default:
				if err := ota.CheckLeaf(certs[0], root, now); err != nil {
					r.errorf("certs/%s does not chain to the root: %v", e.Name(), err)
				}
			}
		}
	}

	pointers, err := a.store.ListPointers()
	if err != nil {
		r.errorf("pointers: %v", err)
	}
	revoked, err := a.store.Revoked()
	if err != nil {
		r.errorf("revoked.txt: %v", err)
	}
	for _, e := range pointers {
		where := e.Channel + "/" + e.Platform + "/" + e.Runtime
		if e.State == ota.StateTestUnsigned {
			r.warnf("%s is in state test_unsigned until %s; clear it with otactl test-unsigned --clear", where, e.Until)
			continue
		}
		if rt, err := a.store.ReadRuntime(e.Platform, e.Runtime); err != nil {
			r.errorf("%s: runtime file: %v", where, err)
		} else if rt == nil {
			r.warnf("%s: runtime %s is not registered, so the server answers 204 there", where, e.Runtime)
		}
		if e.Update == "" {
			r.okf("%s: %s", where, e.State)
			continue
		}
		if root == nil {
			r.errorf("%s points at update %s, but there is no usable root to check it", where, e.Update)
			continue
		}
		v, err := a.store.VerifyUpdate(e.Update, root, now)
		if err != nil {
			r.errorf("%s: %v", where, err)
			continue
		}
		if v.Info.Platform != e.Platform || v.Info.Runtime != e.Runtime {
			r.errorf("%s points at update %s, which is for %s/%s", where, e.Update, v.Info.Platform, v.Info.Runtime)
		}
		if err := a.store.CheckAssets(v.Manifest); err != nil && e.State == ota.StateUpdate {
			r.errorf("%s: %v", where, err)
		}
		for _, l := range []struct {
			name string
			exp  time.Time
		}{{"manifest", v.ManifestLeaf.NotAfter}, {"rollback directive", v.RollbackLeaf.NotAfter}} {
			if l.exp.Before(now.Add(leafMargin)) {
				r.errorf("%s: the %s of update %s is signed by a leaf that expires at %s, within 30 days; run otapublish resign-active",
					where, l.name, e.Update, l.exp.UTC().Format(time.RFC3339))
			}
		}
		if e.State == ota.StateUpdate && contains(revoked, e.Update) {
			r.warnf("%s serves revoked update %s, so the server answers 204 there", where, e.Update)
		}
		r.okf("%s: %s %s (%s), signatures verify, leaf valid until %s", where, e.State, e.Update, v.Info.Label,
			v.LeafNotAfter().UTC().Format(time.RFC3339))
	}

	if pol, err := a.store.ReadPolicy(); err != nil {
		r.errorf("native/policy.json: %v (the server gates nobody while it is invalid)", err)
	} else {
		for _, platform := range []string{ota.PlatformAndroid, ota.PlatformIOS} {
			pp := pol.For(platform)
			if pp.Latest == nil {
				continue
			}
			name := ota.NativeFileName(platform, pp.Latest.Version, pp.Latest.Build)
			p := a.store.Path("native", platform, name)
			st, err := os.Stat(p)
			if err != nil {
				r.errorf("policy %s latest: native/%s/%s: %v", platform, platform, name, err)
				continue
			}
			sum, err := fileSHA256(p)
			if err != nil || hex.EncodeToString(sum[:]) != pp.Latest.SHA256 || st.Size() != pp.Latest.Size {
				r.errorf("policy %s latest: native/%s/%s does not match the policy's sha256 and size", platform, platform, name)
				continue
			}
			r.okf("policy %s: latest build %d, min_build %d, hard_gate %v", platform, pp.Latest.Build, pp.MinBuild, pp.HardGate)
		}
	}

	if entries, err := os.ReadDir(a.store.Path("incoming")); err == nil && len(entries) > 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		r.warnf("incoming/ holds %s: an upload that was not ingested; ingest it or delete it", strings.Join(names, ", "))
	}
	if free, err := freeBytes(a.store.Dir); err != nil {
		r.warnf("free space: %v", err)
	} else if free < minFree {
		r.warnf("only %d MB free on the filesystem of %s; below 1 GB", free>>20, a.store.Dir)
	} else {
		r.okf("%d MB free", free>>20)
	}
	if cdb, err := a.openCensus(); err != nil {
		r.errorf("census: %v", err)
	} else if cdb != nil {
		if rows, err := cdb.List(); err != nil {
			r.errorf("census: %v", err)
		} else {
			r.okf("census: %d install(s) (limit %d)", len(rows), census.MaxRows)
		}
	}
	return r
}

// inventory lists every stored file with its SHA-256 (assets: their size;
// the name is the hash), for otapublish audit, which compares it with the
// Mac archive.
type inventory struct {
	Certs    map[string]string            `json:"certs"`
	Assets   map[string]int64             `json:"assets"`
	Updates  map[string]map[string]string `json:"updates"`
	Native   map[string]map[string]string `json:"native"`
	Runtimes []ota.RuntimeInfo            `json:"runtimes"`
	Pointers []pointerStatus              `json:"pointers"`
	Revoked  []string                     `json:"revoked"`
	Policy   *ota.Policy                  `json:"policy"`
}

func hashFile(p string) (string, error) {
	sum, err := fileSHA256(p)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(sum[:]), nil
}

func setupInventory(fs *flag.FlagSet) func(a *app, args []string) error {
	return func(a *app, args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		inv := inventory{Certs: map[string]string{}, Assets: map[string]int64{}, Updates: map[string]map[string]string{},
			Native: map[string]map[string]string{}, Revoked: []string{}}
		if entries, err := os.ReadDir(a.store.Path("certs")); err == nil {
			for _, e := range entries {
				if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".pem") {
					if inv.Certs[e.Name()], err = hashFile(a.store.Path("certs", e.Name())); err != nil {
						return err
					}
				}
			}
		}
		if entries, err := os.ReadDir(a.store.Path("assets")); err == nil {
			for _, e := range entries {
				if _, _, ok := ota.ParseAssetName(e.Name()); ok {
					info, err := e.Info()
					if err != nil {
						return err
					}
					inv.Assets[e.Name()] = info.Size()
				}
			}
		}
		ids, err := a.store.ListUpdates()
		if err != nil {
			return err
		}
		for _, id := range ids {
			files := map[string]string{}
			for _, name := range ota.UpdateFiles {
				h, err := hashFile(a.store.Path("updates", id, name))
				if err != nil {
					h = "missing"
				}
				files[name] = h
			}
			inv.Updates[id] = files
		}
		for _, platform := range []string{ota.PlatformAndroid, ota.PlatformIOS} {
			inv.Native[platform] = map[string]string{}
			entries, _ := os.ReadDir(a.store.Path("native", platform))
			for _, e := range entries {
				if _, _, ok := ota.ParseNativeName(platform, e.Name()); ok {
					if inv.Native[platform][e.Name()], err = hashFile(a.store.Path("native", platform, e.Name())); err != nil {
						return err
					}
				}
			}
		}
		if inv.Runtimes, err = a.store.ListRuntimes(); err != nil {
			return err
		}
		if inv.Runtimes == nil {
			inv.Runtimes = []ota.RuntimeInfo{}
		}
		if inv.Pointers, err = a.pointerStatuses(); err != nil {
			return err
		}
		if revoked, err := a.store.Revoked(); err != nil {
			return err
		} else if revoked != nil {
			inv.Revoked = revoked
		}
		if _, err := os.Stat(a.store.Path("native", "policy.json")); err == nil {
			if inv.Policy, err = a.store.ReadPolicy(); err != nil {
				return err
			}
		}
		if a.json {
			return a.printJSON(inv)
		}
		a.printf("certs %d, assets %d, updates %d, native %d+%d, runtimes %d, pointers %d, revoked %d\n",
			len(inv.Certs), len(inv.Assets), len(inv.Updates), len(inv.Native["android"]), len(inv.Native["ios"]),
			len(inv.Runtimes), len(inv.Pointers), len(inv.Revoked))
		return nil
	}
}
