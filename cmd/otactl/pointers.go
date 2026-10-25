package main

import (
	"archive/zip"
	"crypto/md5"
	"crypto/rand"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"alexmessage/internal/ota"
)

// leafMargin: activate, promote and rollback-embedded refuse a signature
// whose leaf expires sooner than this (OTAUpdatePlan.md, D4).
const leafMargin = 30 * 24 * time.Hour

// pointerChange is one pointer that a command changed (or left as it was).
type pointerChange struct {
	Channel   string       `json:"channel"`
	Platform  string       `json:"platform"`
	Runtime   string       `json:"runtime"`
	Old       *ota.Pointer `json:"old"`
	New       *ota.Pointer `json:"new"`
	Label     string       `json:"label,omitempty"`
	Unchanged bool         `json:"unchanged,omitempty"`
}

func (a *app) describe(p *ota.Pointer) string {
	if p == nil {
		return "none"
	}
	s := p.State
	if p.Update != "" {
		s += " " + p.Update
		if label := a.labelOf(p.Update); label != "" {
			s += " (" + label + ")"
		}
	}
	if p.Until != "" {
		s += " until " + p.Until
	}
	return s
}

// labelOf is an update's label, or "" when its info.json cannot be read.
func (a *app) labelOf(id string) string {
	if info, err := a.store.ReadUpdateInfo(id); err == nil {
		return info.Label
	}
	return ""
}

func (a *app) printChanges(changes []pointerChange) error {
	if a.json {
		if changes == nil {
			changes = []pointerChange{}
		}
		return a.printJSON(map[string]any{"changes": changes})
	}
	if len(changes) == 0 {
		a.printf("no pointer changed\n")
	}
	for _, c := range changes {
		if c.Unchanged {
			a.printf("%s/%s/%s: unchanged, %s\n", c.Channel, c.Platform, c.Runtime, a.describe(c.New))
			continue
		}
		a.printf("%s/%s/%s: %s -> %s\n", c.Channel, c.Platform, c.Runtime, a.describe(c.Old), a.describe(c.New))
	}
	return nil
}

func (a *app) root() (*x509.Certificate, error) {
	root, err := a.store.ReadRoot()
	if err != nil {
		return nil, fmt.Errorf("the store has no usable root certificate (certs/%s): %w", ota.RootCertFile, err)
	}
	return root, nil
}

// updateTime is an update's createdAt from its info.json.
func (a *app) updateTime(id string) (time.Time, error) {
	info, err := a.store.ReadUpdateInfo(id)
	if err != nil {
		return time.Time{}, err
	}
	return ota.ParseTime(info.CreatedAt)
}

// floor is the time a new update for a pointer must be later than: the
// current update's createdAt (one second later for a rollback, whose
// directive moved phones to that commitTime) and the build time of every
// registered binary of the runtime. Phones ignore an update that is not
// newer than what they run (plan section 1, fact 14).
func (a *app) floor(cur *ota.Pointer, builds []ota.RuntimeBuild) (time.Time, string, error) {
	var at time.Time
	var what string
	for _, b := range builds {
		t, err := ota.ParseTime(b.CommitTime)
		if err != nil {
			return at, "", fmt.Errorf("build %d: commit_time %q: %w", b.Build, b.CommitTime, err)
		}
		if t.After(at) {
			at, what = t, fmt.Sprintf("the build time of native build %d", b.Build)
		}
	}
	if cur != nil && cur.State == ota.StateTestUnsigned {
		cur = cur.Previous
	}
	if cur != nil && cur.Update != "" {
		t, err := a.updateTime(cur.Update)
		if err != nil {
			return at, "", fmt.Errorf("the current pointer's update %s: %w", cur.Update, err)
		}
		what2 := "the createdAt of the current update " + cur.Update
		if cur.State == ota.StateRollback {
			t = t.Add(time.Second)
			what2 = "the commitTime of the rollback directive the pointer serves"
		}
		if t.After(at) {
			at, what = t, what2
		}
	}
	return at, what, nil
}

// checkActivation checks that channel may point at update id now. unchanged
// is true when the pointer already serves it.
func (a *app) checkActivation(channel, id string, now time.Time) (v *ota.VerifiedUpdate, cur *ota.Pointer, unchanged bool, err error) {
	root, err := a.root()
	if err != nil {
		return nil, nil, false, err
	}
	if v, err = a.store.VerifyUpdate(id, root, now); err != nil {
		return nil, nil, false, err
	}
	if err := a.store.CheckAssets(v.Manifest); err != nil {
		return nil, nil, false, fmt.Errorf("update %s: %w", id, err)
	}
	platform, runtime := v.Info.Platform, v.Info.Runtime
	if revoked, err := a.store.IsRevoked(id); err != nil || revoked {
		if err == nil {
			err = fmt.Errorf("update %s is in revoked.txt and is never activated again", id)
		}
		return nil, nil, false, err
	}
	rt, err := a.store.ReadRuntime(platform, runtime)
	if err != nil {
		return nil, nil, false, err
	}
	if rt == nil {
		return nil, nil, false, fmt.Errorf("update %s is for runtime %s of %s, which no registered native build has; run otapublish native", id, runtime, platform)
	}
	if exp := v.LeafNotAfter(); exp.Before(now.Add(leafMargin)) {
		return nil, nil, false, fmt.Errorf("update %s is signed by a leaf that expires at %s, within 30 days; issue a new leaf and run otapublish resign-active",
			id, exp.UTC().Format(time.RFC3339))
	}
	if cur, err = a.store.ReadPointer(channel, platform, runtime); err != nil {
		return nil, nil, false, err
	}
	if cur != nil && cur.State == ota.StateUpdate && cur.Update == id {
		return v, cur, true, nil
	}
	floor, what, err := a.floor(cur, rt.Builds)
	if err != nil {
		return nil, nil, false, err
	}
	if !v.Created().After(floor) {
		return nil, nil, false, fmt.Errorf("update %s: its createdAt %s is not later than %s (%s); phones would ignore it",
			id, v.Manifest.CreatedAt, what, ota.FormatTime(floor))
	}
	return v, cur, false, nil
}

// activate points channel at every update in ids, one per platform and
// runtime, after all of them passed checkActivation.
func (a *app) activate(channel string, ids []string) ([]pointerChange, error) {
	now := a.now()
	type target struct {
		v         *ota.VerifiedUpdate
		cur       *ota.Pointer
		unchanged bool
	}
	var targets []target
	seen := map[string]bool{}
	for _, raw := range ids {
		id, ok := ota.NormalizeUUID(raw)
		if !ok {
			return nil, usagef("%q is not an update id", raw)
		}
		v, cur, unchanged, err := a.checkActivation(channel, id, now)
		if err != nil {
			return nil, err
		}
		key := v.Info.Platform + "/" + v.Info.Runtime
		if seen[key] {
			return nil, fmt.Errorf("two updates for %s/%s", channel, key)
		}
		seen[key] = true
		targets = append(targets, target{v, cur, unchanged})
	}
	since := now.UTC().Format(time.RFC3339)
	var changes []pointerChange
	for _, t := range targets {
		c := pointerChange{Channel: channel, Platform: t.v.Info.Platform, Runtime: t.v.Info.Runtime, Old: t.cur, Label: t.v.Info.Label}
		if t.unchanged {
			c.New, c.Unchanged = t.cur, true
			changes = append(changes, c)
			continue
		}
		np := &ota.Pointer{State: ota.StateUpdate, Update: t.v.ID, Since: since}
		if err := a.store.WritePointer(channel, c.Platform, c.Runtime, *np); err != nil {
			return changes, err
		}
		c.New = np
		changes = append(changes, c)
	}
	return changes, nil
}

// anyChanged says whether a pointer was written; an activation of the update
// a pointer already serves changes nothing and is not logged.
func anyChanged(changes []pointerChange) bool {
	for _, c := range changes {
		if !c.Unchanged {
			return true
		}
	}
	return false
}

func validChannel(c string) error {
	if c != ota.ChannelBeta && c != ota.ChannelProduction {
		return usagef("--channel must be beta or production, not %q", c)
	}
	return nil
}

func setupActivate(fs *flag.FlagSet) func(a *app, args []string) error {
	channel := fs.String("channel", "", "beta or production")
	var ids stringList
	fs.Var(&ids, "update", "an update id; repeat for each platform")
	return func(a *app, args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		if err := validChannel(*channel); err != nil {
			return err
		}
		if len(ids) == 0 {
			return usagef("--update is required")
		}
		changes, err := a.activate(*channel, ids)
		perr := a.printChanges(changes)
		if anyChanged(changes) {
			if lerr := a.logged("activate", map[string]any{"channel": *channel, "changes": changes}); lerr != nil && err == nil {
				err = lerr
			}
		}
		if err != nil {
			return err
		}
		return perr
	}
}

// promoteEvidence is what the promote gate found for one beta update.
type promoteEvidence struct {
	Platform string   `json:"platform"`
	Runtime  string   `json:"runtime"`
	Update   string   `json:"update"`
	Admins   []string `json:"admins"`   // approved admins whose app reported running it
	Failures []string `json:"failures"` // authenticated ota.crash / ota.error reports for it
}

// promoteGate looks in alexmessage.db for the evidence D5 asks for: an
// approved admin's session whose X-AM-Client named the update, and no
// authenticated failure report for it. X-AM-Client is stored only for the
// mobile app (the web client sends none).
func (a *app) promoteGate(ev []*promoteEvidence) error {
	pool, err := sql.Open("sqlite", "file:"+a.dbPath()+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer pool.Close()
	rows, err := pool.Query(`SELECT u.username, s.client FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE u.status = 'approved' AND u.is_admin = 1 AND s.client != ''`)
	if err != nil {
		return fmt.Errorf("%s: %w", a.dbPath(), err)
	}
	for rows.Next() {
		var username, client string
		if err := rows.Scan(&username, &client); err != nil {
			rows.Close()
			return err
		}
		info, ok := ota.ParseClientInfo(client)
		if !ok || info.Component == "ring" {
			continue
		}
		for _, e := range ev {
			if info.Platform == e.Platform && info.Update == e.Update && !contains(e.Admins, username) {
				e.Admins = append(e.Admins, username)
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	rows, err = pool.Query(`SELECT id, username, event, context FROM debug_events
		WHERE user_id IS NOT NULL AND event IN ('ota.crash', 'ota.error')`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var username, event, ctx string
		if err := rows.Scan(&id, &username, &event, &ctx); err != nil {
			return err
		}
		var c struct {
			UpdateID string `json:"updateId"`
		}
		if json.Unmarshal([]byte(ctx), &c) != nil {
			continue
		}
		for _, e := range ev {
			if strings.EqualFold(c.UpdateID, e.Update) {
				e.Failures = append(e.Failures, fmt.Sprintf("%s by %s (debug event %d)", event, username, id))
			}
		}
	}
	return rows.Err()
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func setupPromote(fs *flag.FlagSet) func(a *app, args []string) error {
	label := fs.String("label", "", "the label of the beta update(s) to promote")
	force := fs.Bool("force", false, "skip the evidence check (an urgent fix)")
	return func(a *app, args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		if !ota.ValidLabel(*label) {
			return usagef("--label is required")
		}
		pointers, err := a.store.ListPointers()
		if err != nil {
			return err
		}
		var ev []*promoteEvidence
		for _, e := range pointers {
			if e.Channel == ota.ChannelBeta && e.State == ota.StateUpdate && a.labelOf(e.Update) == *label {
				ev = append(ev, &promoteEvidence{Platform: e.Platform, Runtime: e.Runtime, Update: e.Update,
					Admins: []string{}, Failures: []string{}})
			}
		}
		if len(ev) == 0 {
			return fmt.Errorf("no beta pointer in state update serves label %s", *label)
		}
		if !*force {
			if err := a.promoteGate(ev); err != nil {
				return err
			}
			var problems []string
			for _, e := range ev {
				if len(e.Admins) == 0 {
					problems = append(problems, fmt.Sprintf("%s: no approved admin's app has reported running update %s; on an admin's %s phone open Settings, Software Update, Check for Updates", e.Platform, e.Update, e.Platform))
				}
				for _, f := range e.Failures {
					problems = append(problems, fmt.Sprintf("%s: failure report %s", e.Platform, f))
				}
			}
			if len(problems) > 0 {
				return fmt.Errorf("promote refused (--force skips this check):\n  %s", strings.Join(problems, "\n  "))
			}
		}
		ids := make([]string, len(ev))
		for i, e := range ev {
			ids[i] = e.Update
		}
		changes, err := a.activate(ota.ChannelProduction, ids)
		perr := a.printChanges(changes)
		if anyChanged(changes) {
			if lerr := a.logged("promote", map[string]any{"label": *label, "force": *force, "evidence": ev, "changes": changes}); lerr != nil && err == nil {
				err = lerr
			}
		}
		if err != nil {
			return err
		}
		return perr
	}
}

func setupRestorePointer(fs *flag.FlagSet) func(a *app, args []string) error {
	channel := fs.String("channel", "", "beta or production")
	platform := fs.String("platform", "", "android or ios")
	runtime := fs.String("runtime", "", "the runtime version")
	pointer := fs.String("pointer", "", "the pointer as JSON")
	none := fs.Bool("none", false, "remove the pointer")
	return func(a *app, args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		if err := validChannel(*channel); err != nil {
			return err
		}
		if !ota.ValidPlatform(*platform) || !ota.ValidRuntime(*runtime) {
			return usagef("--platform and --runtime are required")
		}
		if (*pointer == "") == !*none {
			return usagef("give exactly one of --pointer and --none")
		}
		old, err := a.store.ReadPointer(*channel, *platform, *runtime)
		if err != nil {
			return err
		}
		c := pointerChange{Channel: *channel, Platform: *platform, Runtime: *runtime, Old: old}
		if *none {
			if err := a.store.RemovePointer(*channel, *platform, *runtime); err != nil {
				return err
			}
		} else {
			var p ota.Pointer
			dec := json.NewDecoder(strings.NewReader(*pointer))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&p); err != nil {
				return usagef("--pointer: %v", err)
			}
			if err := a.checkRestore(*channel, *platform, *runtime, &p); err != nil {
				return err
			}
			if err := a.store.WritePointer(*channel, *platform, *runtime, p); err != nil {
				return err
			}
			c.New = &p
		}
		if err := a.printChanges([]pointerChange{c}); err != nil {
			return err
		}
		return a.logged("restore-pointer", map[string]any{"change": c})
	}
}

// checkRestore checks a pointer that restore-pointer writes back: its update
// exists for that platform and runtime, and a pointer in state update does
// not serve a revoked update. Unlike activate, it accepts an older update:
// it puts back what was there.
func (a *app) checkRestore(channel, platform, runtime string, p *ota.Pointer) error {
	if err := p.Validate(channel); err != nil {
		return err
	}
	check := func(p *ota.Pointer) error {
		if p.Update == "" {
			return nil
		}
		var info *ota.UpdateInfo
		var err error
		if p.State == ota.StateTestUnsigned {
			_, info, err = a.store.ReadTestManifest(p.Update)
		} else {
			info, err = a.store.ReadUpdateInfo(p.Update)
		}
		if err != nil {
			return err
		}
		if info.Platform != platform || info.Runtime != runtime {
			return fmt.Errorf("update %s is for %s/%s, not %s/%s", p.Update, info.Platform, info.Runtime, platform, runtime)
		}
		if p.State == ota.StateUpdate {
			if revoked, err := a.store.IsRevoked(p.Update); err != nil || revoked {
				if err == nil {
					err = fmt.Errorf("update %s is revoked; a pointer in state update would serve nothing", p.Update)
				}
				return err
			}
		}
		return nil
	}
	if err := check(p); err != nil {
		return err
	}
	if p.Previous != nil {
		return check(p.Previous)
	}
	return nil
}

// halt sets every matching pointer that serves something to halted. A
// halted pointer keeps the id of the update it halted.
func (a *app) halt(match func(e ota.PointerEntry) (bool, error)) ([]pointerChange, error) {
	pointers, err := a.store.ListPointers()
	if err != nil {
		return nil, err
	}
	since := a.now().UTC().Format(time.RFC3339)
	var changes []pointerChange
	for _, e := range pointers {
		if e.State == ota.StateHalted {
			continue
		}
		ok, err := match(e)
		if err != nil {
			return changes, err
		}
		if !ok {
			continue
		}
		old := e.Pointer
		np := &ota.Pointer{State: ota.StateHalted, Update: e.Update, Since: since}
		if e.State == ota.StateTestUnsigned {
			np.Update = ""
			if e.Previous != nil {
				np.Update = e.Previous.Update
			}
		}
		if err := a.store.WritePointer(e.Channel, e.Platform, e.Runtime, *np); err != nil {
			return changes, err
		}
		changes = append(changes, pointerChange{Channel: e.Channel, Platform: e.Platform, Runtime: e.Runtime,
			Old: &old, New: np, Label: a.labelOf(e.Update)})
	}
	return changes, nil
}

// matchLabel matches pointers whose update has the label; a pointer whose
// info.json cannot be read is an error, because it might match.
func (a *app) matchLabel(label string) func(e ota.PointerEntry) (bool, error) {
	return func(e ota.PointerEntry) (bool, error) {
		if e.Update == "" || e.State == ota.StateTestUnsigned {
			return false, nil
		}
		info, err := a.store.ReadUpdateInfo(e.Update)
		if err != nil {
			return false, fmt.Errorf("%s/%s/%s: cannot read the label of update %s: %w", e.Channel, e.Platform, e.Runtime, e.Update, err)
		}
		return info.Label == label, nil
	}
}

func matchAll(ota.PointerEntry) (bool, error) { return true, nil }

func setupHalt(fs *flag.FlagSet) func(a *app, args []string) error {
	all := fs.Bool("all", false, "halt every pointer (every channel, platform and runtime)")
	label := fs.String("label", "", "halt every pointer that serves this label's updates")
	return func(a *app, args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		if *all == (*label != "") {
			return usagef("give exactly one of --all and --label")
		}
		match := matchAll
		if !*all {
			match = a.matchLabel(*label)
		}
		return a.runHalt(match, *label)
	}
}

func (a *app) runHalt(match func(e ota.PointerEntry) (bool, error), label string) error {
	changes, err := a.halt(match)
	perr := a.printChanges(changes)
	if len(changes) > 0 {
		if lerr := a.logged("halt", map[string]any{"label": label, "changes": changes}); lerr != nil && err == nil {
			err = lerr
		}
	}
	if err != nil {
		return err
	}
	if label != "" && len(changes) == 0 {
		return fmt.Errorf("no pointer serves label %s (already halted ones are left as they are)", label)
	}
	return perr
}

// rollbackEmbedded switches every matching pointer in state update or
// halted to its update's pre-signed rollBackToEmbedded directive, after the
// operator saw which native builds the census shows and typed yes, and puts
// the updates into revoked.txt (OTAUpdatePlan.md, D9 step 2).
func (a *app) rollbackEmbedded(match func(e ota.PointerEntry) (bool, error), what string) ([]pointerChange, error) {
	pointers, err := a.store.ListPointers()
	if err != nil {
		return nil, err
	}
	root, err := a.root()
	if err != nil {
		return nil, err
	}
	now := a.now()
	var targets []ota.PointerEntry
	for _, e := range pointers {
		if (e.State != ota.StateUpdate && e.State != ota.StateHalted) || e.Update == "" {
			continue
		}
		ok, err := match(e)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		v, err := a.store.VerifyUpdate(e.Update, root, now)
		if err != nil {
			return nil, fmt.Errorf("%s/%s/%s: %w", e.Channel, e.Platform, e.Runtime, err)
		}
		if exp := v.RollbackLeaf.NotAfter; exp.Before(now.Add(leafMargin)) {
			return nil, fmt.Errorf("%s/%s/%s: the directive of update %s is signed by a leaf that expires at %s, within 30 days; run otapublish resign-active first",
				e.Channel, e.Platform, e.Runtime, e.Update, exp.UTC().Format(time.RFC3339))
		}
		targets = append(targets, e)
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("no pointer in state update or halted serves %s", what)
	}
	a.printf("Phones go back to the JS built into their binary. That is allowed only while the server still works with the built-in JS of every native build below.\n")
	lines, err := a.censusBuilds()
	if err != nil {
		return nil, err
	}
	for _, l := range lines {
		a.printf("  %s\n", l)
	}
	for _, e := range targets {
		a.printf("will roll back %s/%s/%s, update %s (%s)\n", e.Channel, e.Platform, e.Runtime, e.Update, a.labelOf(e.Update))
	}
	if !a.confirm("Roll these pointers back to the built-in JS?") {
		return nil, errors.New("not confirmed; nothing changed")
	}
	var ids []string
	for _, e := range targets {
		if !contains(ids, e.Update) {
			ids = append(ids, e.Update)
		}
	}
	if _, err := a.appendRevoked(ids, "rollback-embedded "+what); err != nil {
		return nil, err
	}
	since := now.UTC().Format(time.RFC3339)
	var changes []pointerChange
	for _, e := range targets {
		old := e.Pointer
		np := &ota.Pointer{State: ota.StateRollback, Update: e.Update, Since: since}
		if err := a.store.WritePointer(e.Channel, e.Platform, e.Runtime, *np); err != nil {
			return changes, err
		}
		changes = append(changes, pointerChange{Channel: e.Channel, Platform: e.Platform, Runtime: e.Runtime,
			Old: &old, New: np, Label: a.labelOf(e.Update)})
	}
	return changes, nil
}

func setupRollback(fs *flag.FlagSet) func(a *app, args []string) error {
	label := fs.String("label", "", "roll back every pointer that serves this label's updates")
	allActive := fs.Bool("all-active", false, "roll back every pointer in state update or halted")
	return func(a *app, args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		if *allActive == (*label != "") {
			return usagef("give exactly one of --label and --all-active")
		}
		match, what := matchAll, "every active update"
		if !*allActive {
			match, what = a.matchLabel(*label), "label "+*label
		}
		return a.runRollback(match, what)
	}
}

func (a *app) runRollback(match func(e ota.PointerEntry) (bool, error), what string) error {
	changes, err := a.rollbackEmbedded(match, what)
	perr := a.printChanges(changes)
	if len(changes) > 0 {
		if lerr := a.logged("rollback-embedded", map[string]any{"what": what, "changes": changes}); lerr != nil && err == nil {
			err = lerr
		}
	}
	if err != nil {
		return err
	}
	return perr
}

// appendRevoked adds the ids that revoked.txt does not list yet, under a
// comment line, and returns them.
func (a *app) appendRevoked(ids []string, note string) ([]string, error) {
	have, err := a.store.Revoked()
	if err != nil {
		return nil, err
	}
	var add []string
	for _, id := range ids {
		if !contains(have, id) && !contains(add, id) {
			add = append(add, id)
		}
	}
	if len(add) == 0 {
		return nil, nil
	}
	path := a.store.Path("revoked.txt")
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	b := append([]byte(nil), old...)
	if len(b) > 0 && b[len(b)-1] != '\n' {
		b = append(b, '\n')
	}
	note = strings.Map(func(r rune) rune {
		if r < 0x20 {
			return ' '
		}
		return r
	}, note)
	b = append(b, fmt.Sprintf("# %s %s\n", a.now().UTC().Format(time.RFC3339), note)...)
	for _, id := range add {
		b = append(b, id+"\n"...)
	}
	return add, ota.WriteFileAtomic(path, b, 0o644)
}

func setupRevoke(fs *flag.FlagSet) func(a *app, args []string) error {
	note := fs.String("note", "revoked by hand", "the comment written above the ids")
	return func(a *app, args []string) error {
		if len(args) == 0 {
			return usagef("give one or more update ids")
		}
		var ids []string
		for _, raw := range args {
			id, ok := ota.NormalizeUUID(raw)
			if !ok {
				return usagef("%q is not an update id", raw)
			}
			ids = append(ids, id)
		}
		added, err := a.appendRevoked(ids, *note)
		if err != nil {
			return err
		}
		a.printf("revoked %d update(s); %d already listed\n", len(added), len(ids)-len(added))
		pointers, err := a.store.ListPointers()
		if err != nil {
			return err
		}
		for _, e := range pointers {
			if e.State == ota.StateUpdate && contains(ids, e.Update) {
				a.printf("%s/%s/%s serves %s: the server now answers 204 there, as if halted\n", e.Channel, e.Platform, e.Runtime, e.Update)
			}
		}
		return a.logged("revoke", map[string]any{"ids": added, "note": *note})
	}
}

// test-unsigned (plan step 6.4): beta phones get an unsigned copy of the JS
// built into the newest registered native build, for a few minutes. A phone
// whose binary checks signatures rejects it and keeps running its built-in
// JS; one that accepts it runs the same JS it already has.

var appBundleRE = regexp.MustCompile(`^Payload/[^/]+\.app/main\.jsbundle$`)

// embeddedBundle reads the JS bundle built into a stored APK or IPA.
func embeddedBundle(path, platform string) ([]byte, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if (platform == ota.PlatformAndroid && f.Name == "assets/index.android.bundle") ||
			(platform == ota.PlatformIOS && appBundleRE.MatchString(f.Name)) {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(io.LimitReader(rc, 256<<20))
		}
	}
	return nil, fmt.Errorf("%s has no embedded JS bundle", path)
}

// newestBuild is the registered native build of a platform with the highest
// build number, and its runtime.
func (a *app) newestBuild(platform string) (*ota.RuntimeBuild, string, error) {
	rts, err := a.store.ListRuntimes()
	if err != nil {
		return nil, "", err
	}
	var best *ota.RuntimeBuild
	var runtime string
	for _, rt := range rts {
		if rt.Platform != platform {
			continue
		}
		for i := range rt.Builds {
			if best == nil || rt.Builds[i].Build > best.Build {
				best, runtime = &rt.Builds[i], rt.Runtime
			}
		}
	}
	return best, runtime, nil
}

func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func setupTestUnsigned(fs *flag.FlagSet) func(a *app, args []string) error {
	channel := fs.String("channel", "", "must be beta")
	minutes := fs.Int("minutes", 10, "how long the unsigned manifest is served (1 to 60)")
	platform := fs.String("platform", "", "android or ios (default: both)")
	baseURL := fs.String("base-url", "https://messages.alexanderjia.com", "the public origin written into the manifest")
	clear := fs.Bool("clear", false, "put back the beta pointers that test-unsigned replaced")
	return func(a *app, args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		if *clear {
			return a.clearTestUnsigned()
		}
		if *channel != ota.ChannelBeta {
			return usagef("--channel beta is required: only beta phones may receive the unsigned test")
		}
		if *minutes < 1 || *minutes > 60 {
			return usagef("--minutes must be 1 to 60")
		}
		platforms := []string{ota.PlatformAndroid, ota.PlatformIOS}
		if *platform != "" {
			if !ota.ValidPlatform(*platform) {
				return usagef("--platform must be android or ios")
			}
			platforms = []string{*platform}
		}
		now := a.now()
		var changes []pointerChange
		for _, p := range platforms {
			c, err := a.startTestUnsigned(p, strings.TrimRight(*baseURL, "/"), now, time.Duration(*minutes)*time.Minute)
			if err != nil {
				if *platform == "" && errors.Is(err, errNoBuild) {
					a.printf("%s: skipped, %v\n", p, err)
					continue
				}
				return err
			}
			changes = append(changes, *c)
		}
		if len(changes) == 0 {
			return errors.New("no platform has a registered and stored native build")
		}
		if err := a.printChanges(changes); err != nil {
			return err
		}
		return a.logged("test-unsigned", map[string]any{"minutes": *minutes, "changes": changes})
	}
}

var errNoBuild = errors.New("no registered native build with a stored binary")

func (a *app) startTestUnsigned(platform, baseURL string, now time.Time, d time.Duration) (*pointerChange, error) {
	build, runtime, err := a.newestBuild(platform)
	if err != nil {
		return nil, err
	}
	if build == nil {
		return nil, errNoBuild
	}
	binary := a.store.Path("native", platform, ota.NativeFileName(platform, build.Version, build.Build))
	if ok, err := exists(binary); err != nil || !ok {
		if err == nil {
			err = fmt.Errorf("%w (%s is missing)", errNoBuild, binary)
		}
		return nil, err
	}
	bundle, err := embeddedBundle(binary, platform)
	if err != nil {
		return nil, err
	}
	hash := ota.AssetHash(bundle)
	assetName := hash + ".js"
	if ok, err := exists(a.store.Path("assets", assetName)); err != nil {
		return nil, err
	} else if !ok {
		if err := ota.WriteFileAtomic(a.store.Path("assets", assetName), bundle, 0o644); err != nil {
			return nil, err
		}
	}
	key := md5.Sum(bundle)
	id := newUUID()
	created := ota.FormatTime(now)
	am := ota.ManifestAM{Label: "test-unsigned", Notes: "Unsigned test of the code-signing check (plan step 6.4)"}
	extra, _ := json.Marshal(map[string]any{"am": am})
	m := ota.UpdateManifest{
		ID: id, CreatedAt: created, RuntimeVersion: runtime,
		LaunchAsset: ota.ManifestAsset{Hash: hash, Key: hex.EncodeToString(key[:]), ContentType: "application/javascript",
			FileExtension: ".bundle", URL: baseURL + "/ota/assets/" + assetName},
		Assets: []ota.ManifestAsset{}, Metadata: json.RawMessage("{}"), Extra: extra,
	}
	body, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	if _, err := ota.ParseUpdateManifest(body); err != nil {
		return nil, err
	}
	info, _ := json.Marshal(ota.UpdateInfo{Platform: platform, Runtime: runtime, Label: am.Label, CreatedAt: created, Notes: am.Notes})
	if err := ota.WriteFileAtomic(a.store.Path("test", id, "manifest.json"), body, 0o644); err != nil {
		return nil, err
	}
	if err := ota.WriteFileAtomic(a.store.Path("test", id, "info.json"), info, 0o644); err != nil {
		return nil, err
	}
	cur, err := a.store.ReadPointer(ota.ChannelBeta, platform, runtime)
	if err != nil {
		return nil, err
	}
	prev := cur
	if cur != nil && cur.State == ota.StateTestUnsigned {
		prev = cur.Previous
	}
	np := &ota.Pointer{State: ota.StateTestUnsigned, Update: id, Since: now.UTC().Format(time.RFC3339),
		Until: now.Add(d).UTC().Format(time.RFC3339), Previous: prev}
	if err := a.store.WritePointer(ota.ChannelBeta, platform, runtime, *np); err != nil {
		return nil, err
	}
	return &pointerChange{Channel: ota.ChannelBeta, Platform: platform, Runtime: runtime, Old: cur, New: np, Label: am.Label}, nil
}

func (a *app) clearTestUnsigned() error {
	pointers, err := a.store.ListPointers()
	if err != nil {
		return err
	}
	var changes []pointerChange
	for _, e := range pointers {
		if e.State != ota.StateTestUnsigned {
			continue
		}
		old := e.Pointer
		c := pointerChange{Channel: e.Channel, Platform: e.Platform, Runtime: e.Runtime, Old: &old, New: e.Previous}
		if e.Previous == nil {
			err = a.store.RemovePointer(e.Channel, e.Platform, e.Runtime)
		} else {
			err = a.store.WritePointer(e.Channel, e.Platform, e.Runtime, *e.Previous)
		}
		if err != nil {
			return err
		}
		changes = append(changes, c)
	}
	if err := a.printChanges(changes); err != nil {
		return err
	}
	if len(changes) == 0 {
		return nil
	}
	return a.logged("test-unsigned-clear", map[string]any{"changes": changes})
}
