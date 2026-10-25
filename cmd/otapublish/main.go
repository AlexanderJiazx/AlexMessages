// Command otapublish publishes over-the-air updates and native builds of the
// mobile app from the publisher's Mac (OTAUpdatePlan.md, D4, D6, D9 and D10).
// It is the only program that ever holds a signing key, and it is never
// deployed: the Pi only verifies (cmd/otactl).
//
// Every command that touches a key, and every publish, runs in two stages.
// The copy started with `go run ./cmd/otapublish` (stage 1) only checks out
// the commit into a clean git worktree, builds otapublish there with that
// commit stamped in, and runs that build (stage 2) with --in-worktree. Stage 2
// refuses to run unless its stamped commit is the worktree's HEAD and the
// worktree is clean, so an edited checkout never touches a key (D6 steps 1
// and 2).
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// commit is the git commit this binary was built from: stage 1 builds stage
// 2 with -ldflags "-X main.commit=<HEAD>".
var commit = "unknown"

// The production server. Asset URLs in a manifest are absolute (D2).
const (
	publicURL = "https://messages.alexanderjia.com"
	defaultPi = "alexander@35.226.215.209"
)

// env is everything a run reads from the outside world; tests replace it.
type env struct {
	stdin   io.Reader
	stdout  io.Writer
	stderr  io.Writer
	now     func() time.Time
	getenv  func(string) string
	environ func() []string
	// passphrase reads a passphrase from the terminal without echo.
	passphrase func(prompt string) ([]byte, error)
	// workFactor is the scrypt work factor (log2 N) of new key files;
	// rootBits and leafBits are the RSA key sizes. Tests make them small.
	workFactor         int
	rootBits, leafBits int
	// removable checks that a directory is on an external volume (D4: the
	// root key never touches the Mac's disk).
	removable func(dir string) error
	commit    string // main.commit
	processes func() ([]process, error)
	sleep     func(time.Duration)
	http      *http.Client
	home      string
	// publicURL is the server that phones talk to; tests point it at an
	// httptest server.
	publicURL string
	// fixture replaces build steps in tests (the --fixture of a dry run).
	fixture *fixture
}

func main() {
	home, _ := os.UserHomeDir()
	e := &env{stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr, now: time.Now, getenv: os.Getenv,
		environ: os.Environ, passphrase: readPassphrase, workFactor: 18, rootBits: rootKeyBits, leafBits: leafKeyBits,
		removable: func(dir string) error { return checkRemovable(dir, home) }, commit: commit,
		processes: listProcesses, sleep: time.Sleep, http: &http.Client{Timeout: 2 * time.Minute}, home: home, publicURL: publicURL}
	os.Exit(run(os.Args[1:], e))
}

// options are the flags every command accepts.
type options struct {
	archive       string
	keysDir       string
	pi            string
	piPort        string
	piDir         string
	piData        string
	otactl        string
	profile       string
	inWorktree    string
	publicURLFlag string
}

// app is one run of a command.
type app struct {
	*env
	options
	args    []string // the command line after the program name, for stage 1
	repo    string   // the repository; the clean worktree in stage 2
	started time.Time
	piConn  pi
	temps   []string // removed when the command ends
	in      *bufio.Reader
}

type command struct {
	usage   string
	summary string
	setup   func(fs *flag.FlagSet) func(a *app, args []string) error
}

var commands map[string]*command

func init() {
	commands = map[string]*command{
		"keys init-root":    {"keys init-root --id root-2026 --out <dir> --cert-out <file>", "create the root key (RSA-4096, encrypted) and its certificate", setupInitRoot},
		"keys issue-leaf":   {"keys issue-leaf --root <root key file> --out <dir> [--no-upload]", "create a signing leaf (RSA-3072, encrypted), certified by the root", setupIssueLeaf},
		"keys upload-certs": {"keys upload-certs", "copy the root and every leaf certificate to the Pi's data/ota/certs/", setupUploadCerts},
		"keys show":         {"keys show (--root <key file> | --leaf <key file>)", "decrypt a key file and print its public fingerprint and certificate", setupShow},
		"ota":               {"ota --notes <text> [--channel beta|production] [--critical] [--dry-run [--allow-local]] [--from <ref>] [--platform <p>]", "publish the JS of a commit as a signed update (D6)", setupOTA},
		"promote":           {"promote <label> [--force]", "point production at the beta update(s) of a label", setupPromote},
		"republish":         {"republish <label> [--channel production|beta] [--notes <text>]", "re-sign archived content as a new update (D9 step 5)", setupRepublish},
		"resign-active":     {"resign-active", "re-sign every update a pointer references, and the last 3 per runtime, with the newest leaf (D4)", setupResignActive},
		"status":            {"status", "the Pi's pointers, revoked updates and census summary", setupStatus},
		"native":            {"native (--notes <text> [--no-upload [--allow-local]] | --upload-only <artifact dir>)", "build, sign and check the APK and the IPA of a release commit, then upload, register and publish them (D10)", setupNative},
	}
}

// usageError makes run exit with status 2.
type usageError struct{ msg string }

func (u usageError) Error() string { return u.msg }

func usagef(format string, args ...any) error { return usageError{fmt.Sprintf(format, args...)} }

// exitCode is the exit status of stage 2, which stage 1 passes on.
type exitCode int

func (c exitCode) Error() string { return fmt.Sprintf("exit status %d", int(c)) }

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: otapublish <command> [flags]")
	names := make([]string, 0, len(commands))
	for n := range commands {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(w, "  %s\n      %s\n", commands[n].usage, commands[n].summary)
	}
}

func run(args []string, e *env) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printUsage(e.stderr)
		return 2
	}
	name, rest := args[0], args[1:]
	if name == "keys" && len(rest) > 0 {
		name, rest = "keys "+rest[0], rest[1:]
	}
	cmd, ok := commands[name]
	if !ok {
		fmt.Fprintf(e.stderr, "otapublish: unknown command %q\n", name)
		printUsage(e.stderr)
		return 2
	}
	err := runCommand(name, cmd, args, rest, e)
	var ue usageError
	var code exitCode
	switch {
	case err == nil:
		return 0
	case errors.As(err, &code):
		return int(code)
	case errors.Is(err, flag.ErrHelp):
		return 2
	case errors.As(err, &ue):
		fmt.Fprintf(e.stderr, "otapublish %s: %v\nusage: otapublish %s\n", name, err, cmd.usage)
		return 2
	default:
		fmt.Fprintf(e.stderr, "otapublish %s: %v\n", name, err)
		return 1
	}
}

func runCommand(name string, cmd *command, all, args []string, e *env) error {
	fs := flag.NewFlagSet("otapublish "+name, flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	var o options
	fs.StringVar(&o.archive, "archive", "", "the release archive (default: $AM_RELEASES_DIR, else ~/AlexMessagesReleases)")
	fs.StringVar(&o.keysDir, "keys-dir", "", "the encrypted signing keys (default: ~/.alexmessages/keys; drill: ~/.alexmessages/drill-keys)")
	fs.StringVar(&o.pi, "pi", defaultPi, "the Pi's SSH target")
	fs.StringVar(&o.piPort, "pi-port", "2222", "the Pi's SSH port (the relay)")
	fs.StringVar(&o.piDir, "pi-dir", "", "a local update store instead of the Pi (dry runs and the drill)")
	fs.StringVar(&o.piData, "pi-data", "", "with --pi-dir: the data directory of its server (default: the parent of --pi-dir)")
	fs.StringVar(&o.otactl, "otactl", "", "with --pi-dir: the otactl binary (default: built from the worktree)")
	fs.StringVar(&o.profile, "profile", "production", "production, or drill (Phase 5: throwaway keys, local store)")
	fs.StringVar(&o.inWorktree, "in-worktree", "", "internal: the worktree stage 2 runs in")
	fs.StringVar(&o.publicURLFlag, "public-url", "", "with --pi-dir: the server that phones talk to")
	body := cmd.setup(fs)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return usageError{err.Error()}
	}
	a := &app{env: e, options: o, args: all, started: e.now()}
	defer a.cleanup()
	if err := a.resolve(); err != nil {
		return err
	}
	return body(a, pos)
}

// resolve fills in the defaults of the common flags.
func (a *app) resolve() error {
	switch a.profile {
	case "production", "drill":
	default:
		return usagef("--profile must be production or drill")
	}
	if a.publicURLFlag != "" {
		if a.piDir == "" {
			return usagef("--public-url needs --pi-dir: a publish to the Pi always uses %s", publicURL)
		}
		a.env.publicURL = strings.TrimRight(a.publicURLFlag, "/")
	}
	if a.archive == "" {
		a.archive = a.getenv("AM_RELEASES_DIR")
	}
	if a.archive == "" {
		a.archive = filepath.Join(a.home, "AlexMessagesReleases")
	}
	if a.drill() {
		// Drill updates and builds never mix with the real ones.
		a.archive = filepath.Join(a.archive, "drill")
	}
	if a.keysDir == "" {
		a.keysDir = filepath.Join(a.home, ".alexmessages", "keys")
		if a.drill() {
			a.keysDir = filepath.Join(a.home, ".alexmessages", "drill-keys")
		}
	}
	if a.piDir != "" && a.piData == "" {
		a.piData = filepath.Dir(a.piDir)
	}
	return nil
}

func (a *app) drill() bool { return a.profile == "drill" }

// otaArchive is the archive's mirror of data/ota (D2: authoritative for the
// write-once files).
func (a *app) otaArchive(parts ...string) string {
	return filepath.Join(append([]string{a.archive, "ota"}, parts...)...)
}

// parseInterleaved parses flags that come before, between or after the
// positional arguments, which it returns in order. A "--" ends the flags.
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var tail []string
	if i := endOfFlags(fs, args); i >= 0 {
		args, tail = args[:i], args[i+1:]
	}
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return append(pos, tail...), nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// endOfFlags is the index of the "--" that ends the flags in args, or -1. A
// "--" that is the value of a flag does not end them.
func endOfFlags(fs *flag.FlagSet, args []string) int {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return i
		}
		name := strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-")
		if name == arg || name == "" || strings.Contains(name, "=") {
			continue
		}
		if f := fs.Lookup(name); f != nil {
			if b, ok := f.Value.(interface{ IsBoolFlag() bool }); !ok || !b.IsBoolFlag() {
				i++
			}
		}
	}
	return -1
}

func noArgs(args []string) error {
	if len(args) > 0 {
		return usagef("unexpected argument %q", args[0])
	}
	return nil
}

func (a *app) printf(format string, args ...any) {
	fmt.Fprintf(a.stdout, format, args...)
}

// confirm asks for the word yes on stdin. There is no flag that answers it:
// every question here guards a step that the plan says needs a person.
func (a *app) confirm(question string) bool {
	fmt.Fprintf(a.stderr, "%s\nType yes to continue: ", question)
	if a.in == nil {
		a.in = bufio.NewReader(a.stdin)
	}
	line, _ := a.in.ReadString('\n')
	return strings.TrimSpace(line) == "yes"
}

// childStdin is the standard input of a program that reads the terminal
// itself (apksigner's password prompt). It is the terminal, so the program
// can turn echo off, unless confirm read input ahead (stdin is not a
// terminal), which then comes first.
func (a *app) childStdin() io.Reader {
	if a.in == nil || a.in.Buffered() == 0 {
		return a.stdin
	}
	ahead, _ := a.in.Peek(a.in.Buffered())
	return io.MultiReader(bytes.NewReader(bytes.Clone(ahead)), a.stdin)
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
