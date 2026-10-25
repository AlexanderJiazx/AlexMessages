package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
)

// The two stages (D6 steps 1 and 2). Stage 1 is the copy of otapublish that
// `go run ./cmd/otapublish` built from the working tree, which may hold
// uncommitted or tampered code: it only checks the commit, checks it out
// into a clean git worktree, builds otapublish there with the commit stamped
// in, and runs that build with --in-worktree. Stage 2 runs everything else,
// and only if its stamped commit is the worktree's HEAD and the worktree is
// clean.

// stageOptions say which commit a command runs on.
type stageOptions struct {
	ref string // the commit to check out; default HEAD
	// requireOrigin: the commit must be contained in origin/react,
	// origin/main or an origin/release/native-* branch (D3 item 4).
	requireOrigin bool
}

// staged runs body in stage 2. In stage 1 it starts stage 2 and returns its
// exit status.
func (a *app) staged(o stageOptions, body func() error) error {
	if a.inWorktree == "" {
		return a.stage1(o)
	}
	if err := a.stage2(); err != nil {
		return err
	}
	return body()
}

// git runs git in dir and returns its trimmed standard output.
func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// allowedBranch reports whether a remote branch may publish (D3 item 4).
func allowedBranch(ref string) bool {
	return ref == "origin/react" || ref == "origin/main" || strings.HasPrefix(ref, "origin/release/native-")
}

// onOrigin checks that commit is contained in an allowed origin branch,
// after fetching origin.
func onOrigin(repo, commit string) error {
	if _, err := git(repo, "fetch", "--quiet", "origin"); err != nil {
		return err
	}
	out, err := git(repo, "branch", "-r", "--contains", commit, "--format=%(refname:short)")
	if err != nil {
		return err
	}
	for _, b := range strings.Fields(out) {
		if allowedBranch(b) {
			return nil
		}
	}
	return fmt.Errorf("commit %s is not contained in origin/react, origin/main or an origin/release/native-* branch: push it first", short(commit))
}

func (a *app) stage1(o stageOptions) error {
	repo, err := git("", "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("otapublish runs inside the Alex Messages repository: %w", err)
	}
	ref := o.ref
	if ref == "" {
		ref = "HEAD"
	}
	commit, err := git(repo, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return err
	}
	if o.requireOrigin {
		if err := onOrigin(repo, commit); err != nil {
			return err
		}
	}
	tmp, err := os.MkdirTemp("", "otapublish-")
	if err != nil {
		return err
	}
	wt := filepath.Join(tmp, "src")
	defer func() {
		if _, err := git(repo, "worktree", "remove", "--force", wt); err != nil {
			fmt.Fprintf(a.stderr, "otapublish: %v\n", err)
		}
		os.RemoveAll(tmp)
	}()
	if _, err := git(repo, "worktree", "add", "--quiet", "--detach", wt, commit); err != nil {
		return err
	}
	bin := filepath.Join(tmp, "otapublish")
	build := exec.Command("go", "build", "-o", bin, "-ldflags", "-X main.commit="+commit, "./cmd/otapublish")
	build.Dir = wt
	build.Stdout, build.Stderr = a.stderr, a.stderr
	if err := build.Run(); err != nil {
		return fmt.Errorf("building otapublish at %s: %w", short(commit), err)
	}
	fmt.Fprintf(a.stderr, "otapublish: running the build of commit %s from a clean worktree\n", short(commit))

	// The child reads the terminal and gets the terminal's interrupt itself;
	// this process waits for it and then removes the worktree.
	signal.Ignore(os.Interrupt)
	defer signal.Reset(os.Interrupt)
	child := exec.Command(bin, a.stage2Args(wt)...)
	child.Stdin, child.Stdout, child.Stderr = a.stdin, a.stdout, a.stderr
	child.Env = a.environ()
	err = child.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return exitCode(ee.ExitCode())
	}
	return err
}

// stage2Args is this command line with --in-worktree added after the
// command name, before any "--".
func (a *app) stage2Args(wt string) []string {
	n := 1
	if len(a.args) > 1 && a.args[0] == "keys" {
		n = 2
	}
	out := append([]string{}, a.args[:n]...)
	out = append(out, "--in-worktree", wt)
	return append(out, a.args[n:]...)
}

func (a *app) stage2() error {
	wt := a.inWorktree
	head, err := git(wt, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if a.commit == "unknown" || a.commit != head {
		return fmt.Errorf("this otapublish was built from commit %s, and %s is at %s: start it with go run ./cmd/otapublish", a.commit, wt, head)
	}
	status, err := git(wt, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return err
	}
	if status != "" {
		return fmt.Errorf("the worktree %s is not clean:\n%s", wt, status)
	}
	a.repo = wt
	return nil
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
