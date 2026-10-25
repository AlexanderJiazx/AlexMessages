package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The checks of D6 that guard a publish: the environment (D3 item 6), a
// running Deploy (step 3), the clocks (step 4) and leftover processes
// (step 13).

// processEnvRE finds the variables a config file reads.
var processEnvRE = regexp.MustCompile(`process\.env(?:\.([A-Za-z_][A-Za-z0-9_]*)|\[\s*["']([A-Za-z_][A-Za-z0-9_]*)["']\s*\])`)

// envCheck refuses to run while a variable that changes the app's config is
// set (D3 item 6): AM_OTA_PROFILE, any EXPO_PUBLIC_* variable, and every
// variable mobile/app.config.js reads. The native build steps evaluate the
// config again to compute the fingerprint, so a stray variable would change
// the shipped binary.
func (a *app) envCheck() error {
	names := map[string]bool{"AM_OTA_PROFILE": true}
	if b, err := os.ReadFile(filepath.Join(a.repo, "mobile", "app.config.js")); err == nil {
		for _, m := range processEnvRE.FindAllStringSubmatch(string(b), -1) {
			names[m[1]+m[2]] = true
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	var set []string
	for _, kv := range a.environ() {
		k, _, _ := strings.Cut(kv, "=")
		if names[k] || strings.HasPrefix(k, "EXPO_PUBLIC_") {
			set = append(set, k)
		}
	}
	if len(set) > 0 {
		sort.Strings(set)
		return fmt.Errorf("unset %s first: app.config.js reads it, and the build would differ from the commit (plan D3 item 6)", strings.Join(set, ", "))
	}
	return nil
}

// childEnv is the environment of npm, npx, Gradle and xcodebuild.
func (a *app) childEnv() []string {
	env := withoutEnv(a.environ(), "EXPO_NO_DOTENV", "CI", "EXPO_NO_TELEMETRY")
	env = append(env, "EXPO_NO_DOTENV=1", "CI=1", "EXPO_NO_TELEMETRY=1")
	if a.drill() {
		env = append(env, "AM_OTA_PROFILE=drill")
	}
	return env
}

// command runs a program in dir with childEnv, its output going to stderr,
// and returns its standard output.
func (a *app) command(dir, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = a.childEnv()
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, a.stderr
	if err := cmd.Run(); err != nil {
		return out.Bytes(), fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return out.Bytes(), nil
}

// step runs a program in dir with its output shown.
func (a *app) step(dir, name string, args ...string) error {
	fmt.Fprintf(a.stderr, "$ (cd %s && %s %s)\n", filepath.Base(dir), name, strings.Join(args, " "))
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = a.childEnv()
	cmd.Stdout, cmd.Stderr = a.stderr, a.stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

// deployCheck refuses while a Deploy workflow run is in progress (D6 step 3).
func (a *app) deployCheck() error {
	out, err := a.command(a.repo, "gh", "run", "list", "--workflow", "Deploy", "--status", "in_progress", "--json", "databaseId")
	if err != nil {
		return fmt.Errorf("the deploy check (plan D6 step 3) needs the GitHub CLI signed in: %w", err)
	}
	var runs []json.RawMessage
	if err := json.Unmarshal(out, &runs); err != nil {
		return fmt.Errorf("gh run list printed %q: %w", out, err)
	}
	if len(runs) > 0 {
		return fmt.Errorf("a Deploy workflow run is in progress; publish after it has finished")
	}
	return nil
}

// maxSkew is the largest clock difference a publish accepts (D6 step 4).
const maxSkew = 2 * time.Minute

// clockCheck compares this Mac's clock with the Pi's and with the Date
// header of the public site (D6 step 4). A clock that runs ahead would stamp
// an update in the future, and phones would then ignore every correct
// update until that time.
func (a *app) clockCheck(p pi) error {
	t, ok, err := p.clock()
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if d := a.now().Sub(t); d > maxSkew || d < -maxSkew {
		return fmt.Errorf("this Mac's clock differs from the Pi's by %s; fix the clock first", d.Round(time.Second))
	}
	req, err := http.NewRequest(http.MethodHead, a.publicURL+"/", nil)
	if err != nil {
		return err
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return fmt.Errorf("the clock check reads the Date header of %s: %w", a.publicURL, err)
	}
	resp.Body.Close()
	site, err := http.ParseTime(resp.Header.Get("Date"))
	if err != nil {
		return fmt.Errorf("%s sent no usable Date header", a.publicURL)
	}
	if d := a.now().Sub(site); d > maxSkew || d < -maxSkew {
		return fmt.Errorf("this Mac's clock differs from %s by %s; fix the clock first", a.publicURL, d.Round(time.Second))
	}
	return nil
}

// process is one running process of this user.
type process struct {
	pid     int
	elapsed time.Duration
	name    string // the executable's base name
}

func listProcesses() ([]process, error) {
	out, err := exec.Command("ps", "-U", strconv.Itoa(os.Getuid()), "-o", "pid=,etime=,comm=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	return parseProcesses(string(out))
}

func parseProcesses(out string) ([]process, error) {
	var procs []process
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		pid, err := strconv.Atoi(f[0])
		if err != nil {
			return nil, fmt.Errorf("ps line %q", line)
		}
		el, err := parseEtime(f[1])
		if err != nil {
			return nil, fmt.Errorf("ps line %q: %w", line, err)
		}
		procs = append(procs, process{pid: pid, elapsed: el, name: filepath.Base(strings.Join(f[2:], " "))})
	}
	return procs, nil
}

// parseEtime parses ps's elapsed time, [[dd-]hh:]mm:ss.
func parseEtime(s string) (time.Duration, error) {
	days := 0
	if d, rest, ok := strings.Cut(s, "-"); ok {
		n, err := strconv.Atoi(d)
		if err != nil {
			return 0, fmt.Errorf("elapsed time %q", s)
		}
		days, s = n, rest
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, fmt.Errorf("elapsed time %q", s)
	}
	total := 0
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return 0, fmt.Errorf("elapsed time %q", s)
		}
		total = total*60 + n
	}
	return time.Duration(days)*24*time.Hour + time.Duration(total)*time.Second, nil
}

// watchedProcesses run npm package code or build tools (D6 step 13).
var watchedProcesses = map[string]bool{"node": true, "npm": true, "npx": true, "esbuild": true, "watchman": true, "java": true, "gradle": true}

// processCheck runs right before a passphrase prompt (D6 step 13, as fixed in
// Phase 2): a node, npm, npx, esbuild, watchman, java or gradle process that
// started after this publish began came from its own build steps and could
// still read what is typed, so the publish waits up to 10 s for it to exit
// and then refuses. Such processes that are older than the publish (an
// editor's language server, Claude Code itself) are listed, and the
// publisher must type yes.
func (a *app) processCheck() error {
	if a.drill() {
		return nil
	}
	for wait := 0; ; wait++ {
		procs, err := a.processes()
		if err != nil {
			return err
		}
		since := a.now().Sub(a.started)
		var fresh, old []string
		for _, p := range procs {
			if !watchedProcesses[p.name] || p.pid == os.Getpid() {
				continue
			}
			desc := fmt.Sprintf("%s (pid %d, running for %s)", p.name, p.pid, p.elapsed)
			if p.elapsed <= since {
				fresh = append(fresh, desc)
			} else {
				old = append(old, desc)
			}
		}
		if len(fresh) > 0 {
			if wait < 10 {
				a.sleep(time.Second)
				continue
			}
			return fmt.Errorf("these processes started during this publish and are still running: %s; stop them and publish again", strings.Join(fresh, ", "))
		}
		if len(old) == 0 {
			return nil
		}
		if !a.confirm(fmt.Sprintf("These processes were already running when this publish started:\n  %s\nAny of them could run npm package code. Close Metro, dev servers and Gradle daemons you do not need before you type the passphrase.", strings.Join(old, "\n  "))) {
			return fmt.Errorf("not confirmed; nothing was signed")
		}
		return nil
	}
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// tempDir makes a temporary directory that is removed when the command ends.
func (a *app) tempDir(kind string) (string, error) {
	dir, err := os.MkdirTemp("", "otapublish-"+kind+"-")
	if err != nil {
		return "", err
	}
	a.temps = append(a.temps, dir)
	return dir, nil
}

func (a *app) cleanup() {
	for _, d := range a.temps {
		os.RemoveAll(d)
	}
	a.temps = nil
}
