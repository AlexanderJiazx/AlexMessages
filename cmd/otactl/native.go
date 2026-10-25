package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"alexmessage/internal/ota"
)

var (
	versionRE = regexp.MustCompile(`^\d{1,4}\.\d{1,4}\.\d{1,4}$`)
	commitRE  = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
)

// highestBuild is the highest build registered for a platform; 0 when none.
func (a *app) highestBuild(platform string) (int, error) {
	b, _, err := a.newestBuild(platform)
	if err != nil || b == nil {
		return 0, err
	}
	return b.Build, nil
}

func setupRegisterRuntime(fs *flag.FlagSet) func(a *app, args []string) error {
	platform := fs.String("platform", "", "android or ios")
	runtime := fs.String("runtime", "", "the runtime (fingerprint) read out of the binary")
	build := fs.Int("build", 0, "the native build number")
	version := fs.String("version", "", "the app version, N.N.N")
	embedded := fs.String("embedded-update-id", "", "the id of the update embedded in the binary (its app.manifest)")
	commitTime := fs.String("commit-time", "", "the embedded update's commitTime")
	commitSHA := fs.String("commit", "", "the git commit the binary was built from")
	return func(a *app, args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		switch {
		case !ota.ValidPlatform(*platform):
			return usagef("--platform must be android or ios")
		case !ota.ValidRuntime(*runtime):
			return usagef("--runtime %q is not a valid runtime", *runtime)
		case *build < 1:
			return usagef("--build must be 1 or more")
		case !versionRE.MatchString(*version):
			return usagef("--version must be N.N.N")
		case !commitRE.MatchString(*commitSHA):
			return usagef("--commit must be a lowercase hex commit")
		}
		id, ok := ota.NormalizeUUID(*embedded)
		if !ok {
			return usagef("--embedded-update-id must be a UUID")
		}
		ct, err := ota.ParseTime(*commitTime)
		if err != nil {
			return usagef("--commit-time: %v", err)
		}
		entry := ota.RuntimeBuild{Build: *build, Version: *version, EmbeddedUpdateID: id,
			CommitTime: ota.FormatTime(ct), Commit: *commitSHA, RegisteredAt: a.now().UTC().Format(time.RFC3339)}

		info, err := a.store.ReadRuntime(*platform, *runtime)
		if err != nil {
			return err
		}
		if info != nil {
			for _, b := range info.Builds {
				if b.Build == entry.Build && b.EmbeddedUpdateID == entry.EmbeddedUpdateID && b.CommitTime == entry.CommitTime &&
					b.Version == entry.Version && b.Commit == entry.Commit {
					a.printf("%s build %d is already registered for runtime %s\n", *platform, *build, *runtime)
					return nil
				}
			}
		}
		highest, err := a.highestBuild(*platform)
		if err != nil {
			return err
		}
		if *build <= highest {
			return fmt.Errorf("build %d is not above the highest registered %s build, %d; a build number is never reused", *build, *platform, highest)
		}
		if info == nil {
			info = &ota.RuntimeInfo{Platform: *platform, Runtime: *runtime}
		}
		info.Builds = append(info.Builds, entry)
		b, err := json.MarshalIndent(info, "", "  ")
		if err != nil {
			return err
		}
		if err := ota.WriteFileAtomic(a.store.Path("runtimes", *platform, *runtime+".json"), append(b, '\n'), 0o644); err != nil {
			return err
		}
		a.printf("registered %s build %d (%s) for runtime %s, embedded update %s\n", *platform, *build, *version, *runtime, id)
		return a.logged("register-runtime", map[string]any{"platform": *platform, "runtime": *runtime, "build": entry})
	}
}

// policy changes native/policy.json (D10, "Minimum-client gate").
func setupPolicy(fs *flag.FlagSet) func(a *app, args []string) error {
	platform := fs.String("platform", "", "android or ios")
	minBuild := fs.Int("min-build", 0, "the lowest build that may connect (0 = off)")
	hardGate := fs.Bool("hard-gate", false, "refuse builds below min_build with 426 and 4426 (only before removing an old native protocol)")
	latestBuild := fs.Int("latest-build", 0, "publish this stored build as the latest one")
	notes := fs.String("notes", "", "the release notes of --latest-build")
	baseURL := fs.String("base-url", "https://messages.alexanderjia.com", "the public origin of the download URL")
	return func(a *app, args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		cur, err := a.store.ReadPolicy()
		if err != nil {
			fmt.Fprintf(a.stderr, "native/policy.json is invalid (%v); starting from an empty policy\n", err)
			cur = &ota.Policy{}
		}
		changing := flagWasSet(fs, "min-build") || flagWasSet(fs, "hard-gate") || flagWasSet(fs, "latest-build") || flagWasSet(fs, "notes")
		if !changing {
			return a.printJSON(cur)
		}
		if !ota.ValidPlatform(*platform) {
			return usagef("--platform is required to change the policy")
		}
		next := *cur
		pp := next.For(*platform)
		before := *pp
		if flagWasSet(fs, "latest-build") {
			latest, err := a.latestFor(*platform, *latestBuild, *notes, strings.TrimRight(*baseURL, "/"))
			if err != nil {
				return err
			}
			pp.Latest = latest
		} else if flagWasSet(fs, "notes") {
			if pp.Latest == nil {
				return usagef("--notes needs a latest build")
			}
			l := *pp.Latest
			l.Notes = *notes
			pp.Latest = &l
		}
		if flagWasSet(fs, "min-build") {
			if *minBuild < 0 {
				return usagef("--min-build must be 0 or more")
			}
			highest, err := a.highestBuild(*platform)
			if err != nil {
				return err
			}
			if *minBuild > highest {
				return fmt.Errorf("min_build %d is above the highest registered %s build, %d: no app could connect", *minBuild, *platform, highest)
			}
			pp.MinBuild = *minBuild
		}
		if flagWasSet(fs, "hard-gate") {
			pp.HardGate = *hardGate
		}
		if err := next.Validate(); err != nil {
			return err
		}
		if pp.MinBuild != before.MinBuild || pp.HardGate != before.HardGate {
			lines, err := a.gatedInstalls(*platform, pp.MinBuild)
			if err != nil {
				return err
			}
			a.printf("%s: min_build %d -> %d, hard_gate %v -> %v\n", *platform, before.MinBuild, pp.MinBuild, before.HardGate, pp.HardGate)
			for _, l := range lines {
				a.printf("  %s\n", l)
			}
			if !a.confirm("Change the gate?") {
				return errors.New("not confirmed; nothing changed")
			}
		}
		b, err := json.MarshalIndent(next, "", "  ")
		if err != nil {
			return err
		}
		if err := ota.WriteFileAtomic(a.store.Path("native", "policy.json"), append(b, '\n'), 0o644); err != nil {
			return err
		}
		if err := a.printJSON(next); err != nil {
			return err
		}
		return a.logged("policy", map[string]any{"platform": *platform, "old": before, "new": *pp})
	}
}

// latestFor builds policy.json's latest entry from a stored native build.
func (a *app) latestFor(platform string, build int, notes, baseURL string) (*ota.NativeBuild, error) {
	if len(notes) > ota.MaxNotes {
		return nil, usagef("--notes is longer than %d bytes", ota.MaxNotes)
	}
	entries, err := os.ReadDir(a.store.Path("native", platform))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, e := range entries {
		version, b, ok := ota.ParseNativeName(platform, e.Name())
		if !ok || b != build {
			continue
		}
		p := a.store.Path("native", platform, e.Name())
		st, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		sum, err := fileSHA256(p)
		if err != nil {
			return nil, err
		}
		return &ota.NativeBuild{Version: version, Build: build, URL: baseURL + "/app/" + platform + "/" + e.Name(),
			SHA256: hex.EncodeToString(sum[:]), Size: st.Size(), Notes: notes}, nil
	}
	return nil, fmt.Errorf("native/%s/ holds no build %d; upload it first", platform, build)
}

// gatedInstalls says which census installs a min_build would gate. The gate
// itself reads X-AM-Client, which builds before 2 do not send; the census
// only approximates it.
func (a *app) gatedInstalls(platform string, minBuild int) ([]string, error) {
	cdb, err := a.openCensus()
	if err != nil {
		return nil, err
	}
	if cdb == nil {
		return []string{"the census is empty"}, nil
	}
	installs, err := cdb.List()
	if err != nil {
		return nil, err
	}
	builds, err := a.buildsByEmbeddedID()
	if err != nil {
		return nil, err
	}
	gated, unknown, total := 0, 0, 0
	for _, in := range installs {
		if in.Platform != platform {
			continue
		}
		total++
		b, ok := builds[in.EmbeddedUpdateID]
		switch {
		case !ok:
			unknown++
		case minBuild > 0 && b.Build < minBuild:
			gated++
		}
	}
	return []string{fmt.Sprintf("%d of %d %s census install(s) are below build %d; %d run a build that is not registered",
		gated, total, platform, minBuild, unknown)}, nil
}

// repair-asset replaces a stored asset with bytes that hash to its name, for
// an asset whose stored copy is damaged (D6 step 16 names the asset). The
// name is a flag value because a base64url hash may start with "-", which
// would read as a flag in a positional argument.
func setupRepairAsset(fs *flag.FlagSet) func(a *app, args []string) error {
	asset := fs.String("asset", "", "the asset's name, <hash>.<ext>")
	return func(a *app, args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		name := *asset
		hash, _, ok := ota.ParseAssetName(name)
		if !ok {
			return usagef("%q is not an asset name", name)
		}
		b, err := io.ReadAll(io.LimitReader(a.stdin, 512<<20))
		if err != nil {
			return err
		}
		if ota.AssetHash(b) != hash {
			return fmt.Errorf("the bytes on stdin do not hash to %s; nothing changed", name)
		}
		p := a.store.Path("assets", name)
		old, err := os.ReadFile(p)
		state := "missing"
		switch {
		case err == nil && bytes.Equal(old, b):
			a.printf("assets/%s is already intact\n", name)
			return nil
		case err == nil:
			state = "damaged"
		case !os.IsNotExist(err):
			return err
		}
		if err := ota.WriteFileAtomic(p, b, 0o644); err != nil {
			return err
		}
		a.printf("assets/%s was %s and is now intact (%d bytes)\n", name, state, len(b))
		return a.logged("repair-asset", map[string]any{"asset": name, "was": state, "size": len(b), "dir": filepath.Base(a.store.Dir)})
	}
}
