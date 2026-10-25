package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"alexmessage/internal/ota"
)

func (h *harness) rootKey() string { return filepath.Join(h.stick, "ota-root-2026.key.age") }

func (h *harness) keysDir() string { return filepath.Join(h.home, ".alexmessages", "keys") }

func (h *harness) leafName(k int) string {
	name := h.now.Format("200601")
	if k > 1 {
		name += "-" + string(rune('0'+k))
	}
	return name
}

func readFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestInitRoot(t *testing.T) {
	h := newHarness(t)
	out := h.ok("keys", "init-root", "--out", h.stick, "--cert-out", h.certOut)
	mustContain(t, out, "Root key (encrypted): "+h.rootKey(), "Public key SHA-256: ")

	st, err := os.Stat(h.rootKey())
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("root key mode %v, want 0600", st.Mode().Perm())
	}
	enc := readFile(t, h.rootKey())
	if !bytes.HasPrefix(enc, []byte(ageArmorHeader)) || bytes.Contains(enc, []byte("PRIVATE KEY")) {
		t.Fatalf("the root key file is not an armored age file:\n%s", enc)
	}
	key, err := decryptKey(enc, []byte(h.pass["root"]))
	if err != nil {
		t.Fatal(err)
	}
	certPEM := readFile(t, h.certOut)
	if !bytes.Equal(certPEM, readFile(t, filepath.Join(h.archive, "ota", "certs", ota.RootCertFile))) {
		t.Fatal("--cert-out and the archive hold different root certificates")
	}
	certs, err := ota.ParseCertificatesPEM(certPEM)
	if err != nil || len(certs) != 1 || !key.PublicKey.Equal(certs[0].PublicKey) {
		t.Fatalf("the root certificate is not the key's (%v)", err)
	}
	if err := ota.CheckRoot(certs[0], h.now); err != nil {
		t.Fatal(err)
	}
	if len(h.prompts) != 2 || !strings.HasPrefix(h.prompts[0], "New root passphrase") || !strings.HasPrefix(h.prompts[1], "Type the new root passphrase again") {
		t.Fatalf("prompts %q", h.prompts)
	}

	// Never again over an existing key or certificate.
	before := readFile(t, h.rootKey())
	mustContain(t, h.fail("keys", "init-root", "--out", h.stick, "--cert-out", h.certOut), "already exists")
	if !bytes.Equal(before, readFile(t, h.rootKey())) {
		t.Fatal("a second init-root changed the root key")
	}
}

func TestInitRootRefusals(t *testing.T) {
	h := newHarness(t)
	if _, _, code := h.run("keys", "init-root", "--id", "root-2027", "--out", h.stick, "--cert-out", h.certOut); code != 2 {
		t.Fatalf("--id root-2027: exit %d, want 2", code)
	}
	if _, _, code := h.run("keys", "init-root", "--out", h.stick); code != 2 {
		t.Fatalf("no --cert-out: exit %d, want 2", code)
	}
	mustContain(t, h.fail("keys", "init-root", "--out", filepath.Join(h.stick, "missing"), "--cert-out", h.certOut), "is not a directory")

	h.removableErr = errors.New("not on a removable volume")
	mustContain(t, h.fail("keys", "init-root", "--out", h.stick, "--cert-out", h.certOut), "not on a removable volume")
	h.removableErr = nil

	h.answers = []string{"short"}
	mustContain(t, h.fail("keys", "init-root", "--out", h.stick, "--cert-out", h.certOut), "at least 12 are needed")
	h.answers = []string{"first passphrase", "second passphrase"}
	mustContain(t, h.fail("keys", "init-root", "--out", h.stick, "--cert-out", h.certOut), "the two passphrases differ")

	for _, p := range []string{h.rootKey(), h.certOut, filepath.Join(h.archive, "ota", "certs", ota.RootCertFile)} {
		if ok, _ := pathExists(p); ok {
			t.Fatalf("a refused init-root left %s", p)
		}
	}
}

func TestCheckRemovable(t *testing.T) {
	home := t.TempDir()
	if err := checkRemovable(home, home); err == nil {
		t.Fatal("a directory in the home directory counts as removable")
	}
	if err := checkRemovable(t.TempDir(), home); err == nil || !strings.Contains(err.Error(), "/Volumes/") {
		t.Fatalf("a temporary directory: %v", err)
	}
}

func TestIssueLeaf(t *testing.T) {
	h := newHarness(t)
	h.ok("keys", "init-root", "--out", h.stick, "--cert-out", h.certOut)
	h.prompts = nil

	// No store on the Pi yet: the certificates wait for upload-certs.
	out := h.ok("keys", "issue-leaf", "--root", h.rootKey())
	name := h.leafName(1)
	mustContain(t, out, "Leaf key (encrypted): "+leafKeyPath(h.keysDir(), name), "Not uploaded: the Pi", "has no update store yet")
	if len(h.prompts) != 3 || !strings.HasPrefix(h.prompts[0], "Root (ota-root-2026.key.age) passphrase") ||
		!strings.HasPrefix(h.prompts[1], "New leaf passphrase") {
		t.Fatalf("prompts %q", h.prompts)
	}
	enc := readFile(t, leafKeyPath(h.keysDir(), name))
	key, err := decryptKey(enc, []byte(h.pass["leaf"]))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decryptKey(enc, []byte(h.pass["root"])); err == nil {
		t.Fatal("the leaf key decrypts with the root passphrase")
	}
	certPEM := readFile(t, leafCertPath(h.keysDir(), name))
	if !bytes.Equal(certPEM, readFile(t, filepath.Join(h.archive, "ota", "certs", "leaf-"+name+".pem"))) {
		t.Fatal("the key directory and the archive hold different leaf certificates")
	}
	root, _, err := ota.ReadRootFile(h.certOut)
	if err != nil {
		t.Fatal(err)
	}
	certs, err := ota.ParseCertificatesPEM(certPEM)
	if err != nil || len(certs) != 1 || !key.PublicKey.Equal(certs[0].PublicKey) {
		t.Fatalf("the leaf certificate is not the key's (%v)", err)
	}
	if err := ota.CheckLeaf(certs[0], root, h.now); err != nil {
		t.Fatal(err)
	}

	// The next leaf of the month gets the next name, and it signs from now on.
	h.ok("keys", "issue-leaf", "--root", h.rootKey(), "--no-upload")
	a := &app{env: h.env(&bytes.Buffer{}, &bytes.Buffer{}), options: options{archive: h.archive, keysDir: h.keysDir(), profile: "production"}}
	s, err := a.signingLeaf()
	if err != nil {
		t.Fatal(err)
	}
	if s.name != h.leafName(2) {
		t.Fatalf("signing leaf %s, want %s", s.name, h.leafName(2))
	}

	// The root passphrase never protects a leaf.
	h.answers = []string{h.pass["root"], h.pass["root"]}
	mustContain(t, h.fail("keys", "issue-leaf", "--root", h.rootKey(), "--no-upload"), "must differ from the root passphrase")
	h.answers = []string{"not the root passphrase"}
	mustContain(t, h.fail("keys", "issue-leaf", "--root", h.rootKey(), "--no-upload"), "wrong passphrase")
	if ok, _ := pathExists(leafKeyPath(h.keysDir(), h.leafName(3))); ok {
		t.Fatal("a refused issue-leaf wrote a key")
	}

	// A root key that is not the archive's root certificate's key.
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	otherEnc, err := encryptKey(other, []byte(h.pass["root"]), 10)
	if err != nil {
		t.Fatal(err)
	}
	otherPath := filepath.Join(h.stick, "other.key.age")
	if err := os.WriteFile(otherPath, otherEnc, 0o600); err != nil {
		t.Fatal(err)
	}
	mustContain(t, h.fail("keys", "issue-leaf", "--root", otherPath, "--no-upload"), "is not the key of the root certificate")
}

func TestSigningLeafSkipsExpiringLeaves(t *testing.T) {
	h := newHarness(t)
	h.ok("keys", "init-root", "--out", h.stick, "--cert-out", h.certOut)
	h.ok("keys", "issue-leaf", "--root", h.rootKey(), "--no-upload")
	cert, err := ota.ParseCertificatesPEM(readFile(t, leafCertPath(h.keysDir(), h.leafName(1))))
	if err != nil {
		t.Fatal(err)
	}
	// 29 days before the leaf expires, it no longer signs.
	h.now = cert[0].NotAfter.Add(-29 * 24 * time.Hour)
	a := &app{env: h.env(&bytes.Buffer{}, &bytes.Buffer{}), options: options{archive: h.archive, keysDir: h.keysDir(), profile: "production"}}
	if _, err := a.signingLeaf(); err == nil || !strings.Contains(err.Error(), "within 30 days") {
		t.Fatalf("signingLeaf: %v", err)
	}
	// Without its key file, a leaf cannot sign either.
	h.now = time.Now()
	if err := os.Remove(leafKeyPath(h.keysDir(), h.leafName(1))); err != nil {
		t.Fatal(err)
	}
	if _, err := a.signingLeaf(); err == nil || !strings.Contains(err.Error(), "is missing") {
		t.Fatalf("signingLeaf: %v", err)
	}
}

func TestKeysShow(t *testing.T) {
	h := newHarness(t)
	h.ok("keys", "init-root", "--out", h.stick, "--cert-out", h.certOut)
	h.ok("keys", "issue-leaf", "--root", h.rootKey(), "--no-upload")

	out := h.ok("keys", "show", "--root", h.rootKey())
	mustContain(t, out, "decrypts: an RSA-2048 private key", "the key MATCHES this certificate", "-----BEGIN CERTIFICATE-----")
	leaf := leafKeyPath(h.keysDir(), h.leafName(1))
	out = h.ok("keys", "show", "--leaf", leaf)
	if n := strings.Count(out, "the key MATCHES this certificate"); n != 2 {
		t.Fatalf("show --leaf matched %d certificates, want 2 (key directory and archive):\n%s", n, out)
	}
	if strings.Contains(out, "PRIVATE KEY") {
		t.Fatalf("show printed a private key:\n%s", out)
	}

	// A leaf certificate that is not the key's.
	archived := filepath.Join(h.archive, "ota", "certs", "leaf-"+h.leafName(1)+".pem")
	if err := os.Chmod(archived, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archived, readFile(t, h.certOut), 0o644); err != nil {
		t.Fatal(err)
	}
	mustContain(t, h.fail("keys", "show", "--leaf", leaf), "the key DOES NOT MATCH this certificate", "1 certificate(s) do not belong to this key")

	if _, _, code := h.run("keys", "show", "--root", h.rootKey(), "--leaf", leaf); code != 2 {
		t.Fatalf("--root and --leaf: exit %d, want 2", code)
	}
	h.answers = []string{"wrong passphrase"}
	mustContain(t, h.fail("keys", "show", "--root", h.rootKey()), "wrong passphrase")
}

// TestUploadCerts runs issue-leaf and upload-certs through the fake ssh:
// rsync, then the tar fallback.
func TestUploadCerts(t *testing.T) {
	h := newHarness(t)
	h.otactl("init")
	h.ok("keys", "init-root", "--out", h.stick, "--cert-out", h.certOut)
	out := h.ok("keys", "issue-leaf", "--root", h.rootKey())
	mustContain(t, out, "root certificate installed, 1 new leaf certificate(s)")
	certs := filepath.Join(h.store, "certs")
	if !bytes.Equal(readFile(t, filepath.Join(certs, ota.RootCertFile)), readFile(t, h.certOut)) {
		t.Fatal("the store's root certificate differs")
	}
	name := h.leafName(1)
	if !bytes.Equal(readFile(t, filepath.Join(certs, "leaf-"+name+".pem")), readFile(t, leafCertPath(h.keysDir(), name))) {
		t.Fatal("the store's leaf certificate differs")
	}
	log := h.toolLog()
	mustContain(t, log, "ssh rsync --server")

	// Without rsync on the Pi, the upload goes through tar.
	t.Setenv("FAKE_NO_RSYNC", "1")
	if err := os.Remove(h.log); err != nil {
		t.Fatal(err)
	}
	out = h.ok("keys", "issue-leaf", "--root", h.rootKey())
	mustContain(t, out, "root certificate already stored, 1 new leaf certificate(s)")
	if _, err := os.Stat(filepath.Join(certs, "leaf-"+h.leafName(2)+".pem")); err != nil {
		t.Fatal(err)
	}
	log = h.toolLog()
	mustContain(t, log, "-xf -")
	if strings.Contains(log, "rsync --server") {
		t.Fatalf("rsync ran although the Pi has none:\n%s", log)
	}

	out = h.ok("keys", "upload-certs")
	mustContain(t, out, "root certificate already stored, 0 new leaf certificate(s), 2 already stored")
	entries, err := os.ReadDir(filepath.Join(h.store, "incoming"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("incoming/ keeps %d upload(s) after ingest", len(entries))
	}

	t.Setenv("FAKE_SSH_DOWN", "1")
	mustContain(t, h.fail("keys", "upload-certs"), "the Pi or the relay is not reachable")
}

func TestStage2Refusals(t *testing.T) {
	h := newHarness(t)
	commit := h.commit
	for _, c := range []string{"unknown", strings.Repeat("0", 40)} {
		h.commit = c
		mustContain(t, h.fail("keys", "init-root", "--out", h.stick, "--cert-out", h.certOut), "start it with go run ./cmd/otapublish")
	}
	h.commit = commit
	h.write(filepath.Join(h.repo, "mobile", "src", "edited.ts"), "export {}\n")
	mustContain(t, h.fail("keys", "init-root", "--out", h.stick, "--cert-out", h.certOut), "is not clean", "mobile/src/edited.ts")
	if len(h.prompts) != 0 {
		t.Fatalf("a refused stage 2 asked for a passphrase: %q", h.prompts)
	}
	if ok, _ := pathExists(h.rootKey()); ok {
		t.Fatal("a refused stage 2 wrote a key")
	}
}
