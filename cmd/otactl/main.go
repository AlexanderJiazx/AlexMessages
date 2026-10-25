// Command otactl manages the OTA update store data/ota on the Raspberry Pi
// (OTAUpdatePlan.md, D2, D3, D9, D10 and step 2.1). It never signs anything
// and holds no key: it checks the signatures made on the publisher's Mac,
// moves uploaded files into place, and switches the channel pointers that
// GET /api/ota/manifest reads.
//
// It finds the store without help from the shell: --ota, else $OTA_DIR, else
// <data>/ota, where <data> is --data, else ../data next to the executable
// when it sits in a bin/ directory (the Pi's ~/alexmessage/bin/otactl, also
// when the emergency key's forced command starts it in the home directory),
// else ./data.
//
// Every command that changes the store holds the store's lock (data/ota/.lock)
// and appends one JSON line to data/ota/log/actions.log.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"alexmessage/internal/ota"
)

// commit is the git commit this binary was built from. deploy.yml stamps it
// with -ldflags "-X main.commit=<sha>"; a local build says "unknown".
var commit = "unknown"

// env is what a run reads from the outside world; tests replace it.
type env struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	now    func() time.Time
	getenv func(string) string
	exe    string // this executable's path, symlinks resolved
}

func main() {
	exe, _ := os.Executable()
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	e := &env{stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr, now: time.Now, getenv: os.Getenv, exe: exe}
	os.Exit(run(os.Args[1:], e))
}

// options are the flags every command accepts.
type options struct {
	data string
	ota  string
	json bool
	yes  bool
}

// app is one run of a command.
type app struct {
	*env
	options
	store   *ota.Store
	dataDir string
	via     string // "emergency" under the emergency key's forced command
}

type command struct {
	usage   string
	summary string
	mutates bool // takes the lock; needs an initialized store unless it is init
	setup   func(fs *flag.FlagSet) func(a *app, args []string) error
}

var commands map[string]*command

func init() {
	commands = map[string]*command{
		"init":              {"init", "create data/ota (FORMAT last); safe to run again", true, setupInit},
		"version":           {"version [--bin <dir>] [--json]", "the commit of otactl and of bin/server, bin/admin and bin/meet", false, setupVersion},
		"ingest":            {"ingest <publishId> [--json]", "verify data/ota/incoming/<publishId>/ and move it into the store", true, setupIngest},
		"activate":          {"activate --channel <c> --update <id> [--update <id>]… [--json]", "point a channel at signed updates", true, setupActivate},
		"promote":           {"promote --label <label> [--force] [--json]", "point production at the beta update(s) of a label", true, setupPromote},
		"restore-pointer":   {"restore-pointer --channel <c> --platform <p> --runtime <r> (--pointer <json> | --none)", "write one exact pointer back", true, setupRestorePointer},
		"halt":              {"halt (--all | --label <label>) [--json]", "stop serving: pointers go to state halted", true, setupHalt},
		"rollback-embedded": {"rollback-embedded (--label <label> | --all-active) [--yes] [--json]", "send phones back to the JS built into their binary; revokes the updates", true, setupRollback},
		"revoke":            {"revoke <id>… [--note <text>]", "never activate or serve these updates again", true, setupRevoke},
		"status":            {"status [--json]", "pointers, revoked ids and a census summary", false, setupStatus},
		"census":            {"census [--json]", "every install in the census, and every approved user's app sessions", false, setupCensus},
		"runtimes":          {"runtimes [--json]", "registered native builds per runtime", false, setupRuntimes},
		"register-runtime":  {"register-runtime --platform <p> --runtime <r> --build <n> --version <v> --embedded-update-id <id> --commit-time <t> --commit <sha>", "register a native build", true, setupRegisterRuntime},
		"policy":            {"policy [--platform <p> [--min-build <n>] [--hard-gate=<bool>] [--latest-build <n>] [--notes <text>]] [--yes]", "show or change native/policy.json", true, setupPolicy},
		"doctor":            {"doctor [--json]", "check the store; exits 1 on any error", false, setupDoctor},
		"emergency":         {"emergency", "the emergency key's forced command: one word from $SSH_ORIGINAL_COMMAND", true, setupEmergency},
		"repair-asset":      {"repair-asset --asset <name> < file", "replace assets/<name> with bytes that hash to <name>", true, setupRepairAsset},
		"test-unsigned":     {"test-unsigned --channel beta [--minutes 10] [--platform <p>] | test-unsigned --clear", "serve beta phones an unsigned copy of the built-in update (plan step 6.4)", true, setupTestUnsigned},
		"inventory":         {"inventory [--json]", "every stored file with its SHA-256, for otapublish audit", false, setupInventory},
	}
}

// usageError makes run exit with status 2.
type usageError struct{ msg string }

func (u usageError) Error() string { return u.msg }

func usagef(format string, args ...any) error { return usageError{fmt.Sprintf(format, args...)} }

// exitNoStore is the exit status of every command except init and version
// while the store has no FORMAT file. otapublish reads it as "no update
// store yet" (before plan step 6.3) and every other failure as an error.
const exitNoStore = 3

// noStoreError makes run exit with status exitNoStore.
type noStoreError struct{ dir string }

func (e noStoreError) Error() string {
	return e.dir + " is not an update store (no FORMAT file); run otactl init"
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: otactl <command> [flags]   (every command also takes --data <dir>, --ota <dir>)")
	fmt.Fprintf(w, "every command except init and version exits %d while the store has no FORMAT file\n", exitNoStore)
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
	cmd, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(e.stderr, "otactl: unknown command %q\n", args[0])
		printUsage(e.stderr)
		return 2
	}
	err := runCommand(args[0], cmd, args[1:], e, "")
	var ue usageError
	var ns noStoreError
	switch {
	case err == nil:
		return 0
	case errors.Is(err, flag.ErrHelp):
		return 2
	case errors.As(err, &ue):
		fmt.Fprintf(e.stderr, "otactl %s: %v\nusage: otactl %s\n", args[0], err, cmd.usage)
		return 2
	case errors.As(err, &ns):
		fmt.Fprintf(e.stderr, "otactl %s: %v\n", args[0], err)
		return exitNoStore
	default:
		fmt.Fprintf(e.stderr, "otactl %s: %v\n", args[0], err)
		return 1
	}
}

func runCommand(name string, cmd *command, args []string, e *env, via string) error {
	fs := flag.NewFlagSet("otactl "+name, flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	var o options
	fs.StringVar(&o.data, "data", "", "the data directory (default: ../data next to a bin/otactl, else ./data)")
	fs.StringVar(&o.ota, "ota", "", "the update store (default: $OTA_DIR, else <data>/ota)")
	fs.BoolVar(&o.json, "json", false, "print JSON")
	fs.BoolVar(&o.yes, "yes", false, "answer yes to the confirmation")
	body := cmd.setup(fs)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return usageError{err.Error()}
	}
	a := newApp(e, o)
	a.via = via
	if name != "init" && name != "version" {
		if err := a.store.CheckFormat(); errors.Is(err, ota.ErrNotInitialized) {
			return noStoreError{a.store.Dir}
		} else if err != nil {
			return err
		}
	}
	if cmd.mutates {
		if name == "init" {
			if err := os.MkdirAll(a.store.Dir, 0o755); err != nil {
				return err
			}
		}
		unlock, err := lockStore(a.store.Path(".lock"), e.stderr)
		if err != nil {
			return err
		}
		defer unlock()
	}
	return body(a, pos)
}

// parseInterleaved parses flags that come before, between or after the
// positional arguments, which it returns in order. A "--" ends the flags:
// every argument after it is positional, even one that starts with "-".
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
// "--" that is the value of a flag ("--notes --") does not end them.
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
				i++ // the next argument is this flag's value
			}
		}
	}
	return -1
}

func newApp(e *env, o options) *app {
	data := o.data
	if data == "" {
		data = "data"
		if dir := filepath.Dir(e.exe); e.exe != "" && filepath.Base(dir) == "bin" {
			data = filepath.Join(filepath.Dir(dir), "data")
		}
	}
	dir := o.ota
	if dir == "" {
		dir = e.getenv("OTA_DIR")
	}
	if dir == "" {
		dir = filepath.Join(data, "ota")
	}
	return &app{env: e, options: o, store: &ota.Store{Dir: dir}, dataDir: data}
}

// dbPath is the main app's database, read for the census users and the
// promote gate.
func (a *app) dbPath() string { return filepath.Join(a.dataDir, "alexmessage.db") }

// logAction appends one line to log/actions.log.
func (a *app) logAction(action string, details map[string]any) error {
	rec := map[string]any{"time": a.now().UTC().Format(time.RFC3339), "action": action}
	user := a.getenv("USER")
	if user == "" {
		user = strconv.Itoa(os.Getuid())
	}
	rec["user"] = user
	if a.via != "" {
		rec["via"] = a.via
	}
	if c := strings.Fields(a.getenv("SSH_CONNECTION")); len(c) > 0 {
		rec["ssh_from"] = c[0]
	}
	for k, v := range details {
		rec[k] = v
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	path := a.store.Path("log", "actions.log")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// logged logs an action that already changed the store; a log failure is
// reported as such, so nobody retries a change that was made.
func (a *app) logged(action string, details map[string]any) error {
	if err := a.logAction(action, details); err != nil {
		return fmt.Errorf("the change was made, but log/actions.log could not be written: %w", err)
	}
	return nil
}

// confirm asks for the word yes on stdin, unless --yes was given.
func (a *app) confirm(question string) bool {
	if a.yes {
		return true
	}
	fmt.Fprintf(a.stderr, "%s\nType yes to continue: ", question)
	line, _ := bufio.NewReader(a.stdin).ReadString('\n')
	return strings.TrimSpace(line) == "yes"
}

func (a *app) printJSON(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = a.stdout.Write(append(b, '\n'))
	return err
}

func (a *app) printf(format string, args ...any) {
	fmt.Fprintf(a.stdout, format, args...)
}

// noArgs is the body check of a command without positional arguments.
func noArgs(args []string) error {
	if len(args) > 0 {
		return usagef("unexpected argument %q", args[0])
	}
	return nil
}

// stringList is a repeatable string flag.
type stringList []string

func (l *stringList) String() string     { return strings.Join(*l, ",") }
func (l *stringList) Set(v string) error { *l = append(*l, v); return nil }

// flagWasSet reports whether the flag name was given on the command line.
func flagWasSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func setupInit(fs *flag.FlagSet) func(a *app, args []string) error {
	return func(a *app, args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		switch err := a.store.CheckFormat(); {
		case err == nil:
			a.printf("%s is already an update store (format %s)\n", a.store.Dir, ota.FormatVersion)
			return nil
		case !errors.Is(err, ota.ErrNotInitialized):
			return err
		}
		for _, d := range []string{"certs", "assets", "updates", "test", "runtimes", "channels", "native/android", "native/ios", "incoming", "log"} {
			if err := os.MkdirAll(a.store.Path(filepath.FromSlash(d)), 0o755); err != nil {
				return err
			}
		}
		// FORMAT last: until it exists the server treats the store as absent.
		if err := ota.WriteFileAtomic(a.store.Path("FORMAT"), []byte(ota.FormatVersion+"\n"), 0o644); err != nil {
			return err
		}
		a.printf("initialized %s (format %s)\n", a.store.Dir, ota.FormatVersion)
		return a.logged("init", map[string]any{"dir": a.store.Dir})
	}
}
