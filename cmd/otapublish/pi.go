package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// pi is the update store and its otactl: the Pi over SSH through the relay
// (D6: files reach the Pi over ssh -p 2222, bypassing Cloudflare), or a
// local store (--pi-dir) for dry runs, the drill and tests.
type pi interface {
	// otactl runs `otactl <args>` and returns its standard output. Its
	// standard error goes to ours.
	otactl(args ...string) ([]byte, error)
	// ready reports whether otactl and an initialized store exist.
	ready() (bool, error)
	// upload copies the files under dir into the store's
	// incoming/<publishID>/, a new directory.
	upload(dir, publishID string) error
	// clock is the Pi's time; ok is false for a local store, whose clock is
	// this Mac's.
	clock() (t time.Time, ok bool, err error)
	String() string
}

// piHome is the app directory on the Pi, relative to the SSH user's home.
const piHome = "alexmessage"

// safePathRE limits a path that goes into a remote shell command.
var safePathRE = regexp.MustCompile(`^/[A-Za-z0-9/._-]+$`)

func (a *app) pi() (pi, error) {
	if a.piConn != nil {
		return a.piConn, nil
	}
	if a.piDir != "" {
		store, err := filepath.Abs(a.piDir)
		if err != nil {
			return nil, err
		}
		data, err := filepath.Abs(a.piData)
		if err != nil {
			return nil, err
		}
		a.piConn = &localPi{a: a, store: store, data: data}
		return a.piConn, nil
	}
	if a.drill() {
		return nil, usagef("--profile drill needs --pi-dir: the drill never uses the Pi")
	}
	a.piConn = &sshPi{a: a, target: a.options.pi, port: a.piPort}
	return a.piConn, nil
}

// shellQuote quotes s for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func shellJoin(args []string) string {
	q := make([]string, len(args))
	for i, s := range args {
		q[i] = shellQuote(s)
	}
	return strings.Join(q, " ")
}

// sshPi is the Pi, reached with the publisher's own SSH key.
type sshPi struct {
	a      *app
	target string
	port   string
	store  string
}

func (p *sshPi) String() string { return "the Pi (" + p.target + ")" }

func (p *sshPi) sshCommand() []string {
	return []string{"ssh", "-p", p.port, "-o", "BatchMode=yes", "-o", "ConnectTimeout=20"}
}

// run runs a shell command on the Pi.
func (p *sshPi) run(remote string, stdin io.Reader) ([]byte, int, error) {
	args := append(p.sshCommand()[1:], p.target, remote)
	cmd := exec.Command("ssh", args...)
	cmd.Stdin = stdin
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, p.a.stderr
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ee.ExitCode() == 255 {
			return out.Bytes(), 255, fmt.Errorf("ssh -p %s %s failed (exit status 255): the Pi or the relay is not reachable", p.port, p.target)
		}
		return out.Bytes(), ee.ExitCode(), nil
	}
	return out.Bytes(), 0, err
}

func (p *sshPi) otactl(args ...string) ([]byte, error) {
	out, code, err := p.run("cd "+piHome+" && exec bin/otactl "+shellJoin(args), nil)
	if err == nil && code != 0 {
		err = fmt.Errorf("otactl %s on the Pi exited with status %d", args[0], code)
	}
	return out, err
}

func (p *sshPi) ready() (bool, error) {
	_, code, err := p.run("test -x "+piHome+"/bin/otactl", nil)
	if err != nil || code != 0 {
		return false, err
	}
	_, err = p.storeDir()
	return err == nil, nil
}

// storeDir is the store's absolute path, as otactl status reports it.
func (p *sshPi) storeDir() (string, error) {
	if p.store != "" {
		return p.store, nil
	}
	out, err := p.otactl("status", "--json")
	if err != nil {
		return "", err
	}
	var st struct {
		Store string `json:"store"`
	}
	if err := json.Unmarshal(out, &st); err != nil {
		return "", fmt.Errorf("otactl status --json: %w", err)
	}
	if !safePathRE.MatchString(st.Store) || strings.Contains(st.Store, "..") {
		return "", fmt.Errorf("otactl status names the store %q, which is not a plain absolute path", st.Store)
	}
	p.store = st.Store
	return p.store, nil
}

func (p *sshPi) upload(dir, publishID string) error {
	store, err := p.storeDir()
	if err != nil {
		return err
	}
	incoming := store + "/incoming"
	dest := incoming + "/" + publishID
	if _, code, err := p.run("mkdir -p "+shellQuote(incoming), nil); err != nil || code != 0 {
		return fmt.Errorf("mkdir %s on the Pi failed (exit status %d, %v)", incoming, code, err)
	}
	_, code, err := p.run("command -v rsync >/dev/null", nil)
	if err != nil {
		return err
	}
	if _, lerr := exec.LookPath("rsync"); code == 0 && lerr == nil {
		// --ignore-existing: a repeated upload of the same publishId never
		// changes a file that is already there.
		cmd := exec.Command("rsync", "-r", "--ignore-existing", "-e", strings.Join(p.sshCommand(), " "),
			dir+"/", p.target+":"+dest+"/")
		cmd.Stdout, cmd.Stderr = p.a.stderr, p.a.stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("rsync to %s: %w", dest, err)
		}
		return nil
	}
	// The tar fallback (D6 step 14): incoming/<publishId> is always a new
	// directory. COPYFILE_DISABLE and --no-mac-metadata keep macOS from
	// adding ._ files, which otactl ingest refuses.
	tar := exec.Command("tar", "--no-mac-metadata", "-C", dir, "-cf", "-", ".")
	tar.Env = append(p.a.environ(), "COPYFILE_DISABLE=1")
	tar.Stderr = p.a.stderr
	pipe, err := tar.StdoutPipe()
	if err != nil {
		return err
	}
	if err := tar.Start(); err != nil {
		return err
	}
	_, code, err = p.run("mkdir "+shellQuote(dest)+" && tar -C "+shellQuote(dest)+" -xf -", pipe)
	werr := tar.Wait()
	if err != nil {
		return err
	}
	if code != 0 || werr != nil {
		return fmt.Errorf("tar upload to %s failed (remote exit status %d, local %v)", dest, code, werr)
	}
	return nil
}

func (p *sshPi) clock() (time.Time, bool, error) {
	out, code, err := p.run("date +%s", nil)
	if err != nil {
		return time.Time{}, false, err
	}
	sec, perr := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if code != 0 || perr != nil {
		return time.Time{}, false, fmt.Errorf("date +%%s on the Pi printed %q (exit status %d)", out, code)
	}
	return time.Unix(sec, 0), true, nil
}

// localPi is a store on this machine with a local otactl.
type localPi struct {
	a      *app
	store  string
	data   string
	binary string
}

func (p *localPi) String() string { return "the local store " + p.store }

func (p *localPi) otactlBinary() (string, error) {
	if p.binary != "" {
		return p.binary, nil
	}
	if p.a.options.otactl != "" {
		p.binary = p.a.options.otactl
		return p.binary, nil
	}
	src := p.a.repo
	if src == "" {
		var err error
		if src, err = git("", "rev-parse", "--show-toplevel"); err != nil {
			return "", err
		}
	}
	dir, err := p.a.tempDir("otactl")
	if err != nil {
		return "", err
	}
	bin := filepath.Join(dir, "otactl")
	build := exec.Command("go", "build", "-o", bin, "./cmd/otactl")
	build.Dir = src
	build.Stdout, build.Stderr = p.a.stderr, p.a.stderr
	if err := build.Run(); err != nil {
		return "", fmt.Errorf("building otactl: %w", err)
	}
	p.binary = bin
	return bin, nil
}

func (p *localPi) otactl(args ...string) ([]byte, error) {
	bin, err := p.otactlBinary()
	if err != nil {
		return nil, err
	}
	full := append([]string{args[0], "--ota", p.store, "--data", p.data}, args[1:]...)
	cmd := exec.Command(bin, full...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, p.a.stderr
	cmd.Env = withoutEnv(p.a.environ(), "OTA_DIR")
	if err := cmd.Run(); err != nil {
		return out.Bytes(), fmt.Errorf("otactl %s: %w", args[0], err)
	}
	return out.Bytes(), nil
}

func (p *localPi) ready() (bool, error) {
	return pathExists(filepath.Join(p.store, "FORMAT"))
}

func (p *localPi) upload(dir, publishID string) error {
	incoming := filepath.Join(p.store, "incoming")
	if err := os.MkdirAll(incoming, 0o755); err != nil {
		return err
	}
	dest := filepath.Join(incoming, publishID)
	if err := os.Mkdir(dest, 0o755); err != nil {
		return err
	}
	return copyTree(dir, dest)
}

func (p *localPi) clock() (time.Time, bool, error) { return time.Time{}, false, nil }

// withoutEnv drops the named variables from an environment list.
func withoutEnv(env []string, names ...string) []string {
	var out []string
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		drop := false
		for _, n := range names {
			drop = drop || k == n
		}
		if !drop {
			out = append(out, kv)
		}
	}
	return out
}

// copyTree copies the regular files and directories under src into dst,
// which exists. It refuses anything else.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil || rel == "." {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case d.Type().IsRegular():
			return copyFile(p, target)
		}
		return fmt.Errorf("%s is not a regular file", p)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// writeFile writes a new file, creating its directory.
func writeFile(p string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

// ingestPlan is what otactl ingest --json reports.
type ingestPlan struct {
	Root          bool     `json:"root"`
	Leaves        []string `json:"leaves"`
	ExpiredLeaves []string `json:"expired_leaves"`
	Assets        []string `json:"assets"`
	Updates       []string `json:"updates"`
	Resigned      []string `json:"resigned"`
	Native        []string `json:"native"`
	Unchanged     int      `json:"unchanged"`
}

// ingest uploads dir as a new publish and ingests it (D6 steps 14 and 15).
func (a *app) ingest(p pi, dir, kind string) (*ingestPlan, error) {
	id := fmt.Sprintf("%s-%s-%s", kind, a.now().UTC().Format("20060102T150405"), randomHex(4))
	if err := p.upload(dir, id); err != nil {
		return nil, fmt.Errorf("upload: %w", err)
	}
	out, err := p.otactl("ingest", id, "--json")
	if err != nil {
		return nil, err
	}
	var plan ingestPlan
	if err := json.Unmarshal(out, &plan); err != nil {
		return nil, fmt.Errorf("otactl ingest --json: %w", err)
	}
	return &plan, nil
}
