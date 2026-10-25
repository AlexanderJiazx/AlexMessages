package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestStage1 runs stage 1 for real: it checks out HEAD into a clean
// worktree, builds otapublish there with the commit stamped in, and runs that
// build. It works in a clone of this repository, so the checkout's own
// .git/worktrees never changes, and it builds the committed code, so it skips
// while HEAD has no cmd/otapublish.
func TestStage1(t *testing.T) {
	if testing.Short() {
		t.Skip("builds otapublish in a worktree")
	}
	top, err := git("", "rev-parse", "--show-toplevel")
	if err != nil {
		t.Skipf("not in a git checkout: %v", err)
	}
	if _, err := git(top, "cat-file", "-e", "HEAD:cmd/otapublish/worktree.go"); err != nil {
		t.Skip("HEAD has no cmd/otapublish yet; stage 1 builds the committed code")
	}
	tmp := t.TempDir()
	tmpdir := filepath.Join(tmp, "tmp")
	if err := os.Mkdir(tmpdir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", tmpdir)
	clone := filepath.Join(tmp, "clone")
	if _, err := git("", "clone", "--quiet", "--shared", "--no-checkout", top, clone); err != nil {
		t.Fatal(err)
	}
	head, err := git(clone, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(clone)

	// A drill key: its passphrase is fixed, so stage 2 asks for nothing.
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := encryptKey(key, []byte(drillPassphrase), 10)
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(tmp, "drill-root.key.age")
	if err := os.WriteFile(keyFile, enc, 0o600); err != nil {
		t.Fatal(err)
	}
	stage1 := func(args ...string) (string, string, int) {
		var out, errb bytes.Buffer
		e := &env{stdin: strings.NewReader(""), stdout: &out, stderr: &errb, now: time.Now, getenv: os.Getenv,
			environ: os.Environ, passphrase: func(string) ([]byte, error) { t.Fatal("stage 1 asked for a passphrase"); return nil, nil },
			workFactor: 10, rootBits: 2048, leafBits: 2048, commit: "unknown", sleep: func(time.Duration) {},
			http: &http.Client{Timeout: time.Second}, home: tmp, publicURL: publicURL}
		code := run(args, e)
		return out.String(), errb.String(), code
	}
	common := []string{"--profile", "drill", "--archive", filepath.Join(tmp, "archive"), "--keys-dir", filepath.Join(tmp, "keys")}

	out, errs, code := stage1(append([]string{"keys", "show", "--root", keyFile}, common...)...)
	if code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errs)
	}
	mustContain(t, errs, "otapublish: running the build of commit "+short(head)+" from a clean worktree")
	mustContain(t, out, keyFile+" decrypts: an RSA-2048 private key", "Public key SHA-256: "+spkiSHA256(&key.PublicKey))
	if list, err := git(clone, "worktree", "list", "--porcelain"); err != nil || strings.Count(list, "worktree ") != 1 {
		t.Fatalf("the worktree was not removed (%v):\n%s", err, list)
	}
	if entries, err := os.ReadDir(tmpdir); err != nil || len(entries) != 0 {
		t.Fatalf("stage 1 left %d entries in TMPDIR (%v)", len(entries), err)
	}

	// Stage 2's exit status passes through.
	_, errs, code = stage1(append([]string{"keys", "show", "--root", filepath.Join(tmp, "missing.key.age")}, common...)...)
	if code != 1 || !strings.Contains(errs, "missing.key.age") {
		t.Fatalf("exit %d, want 1\n%s", code, errs)
	}

	// A publish runs only a commit that is on origin (D3 item 4).
	local, err := git(clone, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit-tree", "-p", "HEAD", "-m", "local only", "HEAD^{tree}")
	if err != nil {
		t.Fatal(err)
	}
	_, errs, code = stage1("ota", "--dry-run", "--notes", "x", "--from", local)
	if code != 1 || !strings.Contains(errs, "is not contained in origin/react, origin/main or an origin/release/native-* branch: push it first") {
		t.Fatalf("exit %d\n%s", code, errs)
	}
	if branch, err := git(top, "rev-parse", "--abbrev-ref", "HEAD"); err == nil && allowedBranch("origin/"+branch) {
		if err := onOrigin(clone, head); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAllowedBranch(t *testing.T) {
	for ref, want := range map[string]bool{"origin/react": true, "origin/main": true, "origin/release/native-3": true,
		"origin/feature": false, "react": false, "upstream/main": false} {
		if allowedBranch(ref) != want {
			t.Errorf("allowedBranch(%q) = %v", ref, !want)
		}
	}
}
