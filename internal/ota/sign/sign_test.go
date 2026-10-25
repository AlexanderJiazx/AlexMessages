package sign

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"errors"
	"os/exec"
	"path"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestRootAndLeafCertificates(t *testing.T) {
	rootKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	rootDER, err := NewRoot(rootKey, "Alex Messages OTA root 2026", now)
	if err != nil {
		t.Fatal(err)
	}
	root, err := x509.ParseCertificate(rootDER)
	if err != nil {
		t.Fatal(err)
	}
	if !root.IsCA || !root.BasicConstraintsValid || root.MaxPathLen != 0 || !root.MaxPathLenZero ||
		root.KeyUsage != x509.KeyUsageCertSign || len(root.SubjectKeyId) == 0 {
		t.Fatalf("root constraints: %+v", root)
	}
	if !root.NotBefore.Equal(now.Add(-24*time.Hour)) || !root.NotAfter.Equal(root.NotBefore.Add(RootValidity)) {
		t.Fatalf("root validity %s – %s", root.NotBefore, root.NotAfter)
	}

	leafDER, err := IssueLeaf(root, rootKey, &leafKey.PublicKey, "Alex Messages OTA leaf 202611", now)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(leafDER)
	if err != nil {
		t.Fatal(err)
	}
	if leaf.IsCA || leaf.KeyUsage != x509.KeyUsageDigitalSignature ||
		!slices.Equal(leaf.ExtKeyUsage, []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning}) ||
		string(leaf.AuthorityKeyId) != string(root.SubjectKeyId) {
		t.Fatalf("leaf usage: %+v", leaf)
	}
	if !leaf.NotBefore.Equal(now.Add(-24*time.Hour)) || !leaf.NotAfter.Equal(leaf.NotBefore.Add(LeafValidity)) {
		t.Fatalf("leaf validity %s – %s", leaf.NotBefore, leaf.NotAfter)
	}
	if err := leaf.CheckSignatureFrom(root); err != nil {
		t.Fatalf("leaf not signed by root: %v", err)
	}
	// A leaf never outlives its root.
	late, err := IssueLeaf(root, rootKey, &leafKey.PublicKey, "late leaf", root.NotAfter.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := x509.ParseCertificate(late); !c.NotAfter.Equal(root.NotAfter) {
		t.Fatalf("late leaf ends %s, root %s", c.NotAfter, root.NotAfter)
	}
	// A leaf cannot be issued by a leaf.
	if _, err := IssueLeaf(leaf, leafKey, &leafKey.PublicKey, "x", now); err == nil {
		t.Fatal("a leaf issued a leaf")
	}
}

func TestRefusesSmallKeysAndBadKeyIDs(t *testing.T) {
	small, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewRoot(small, "x", time.Now()); err == nil {
		t.Fatal("1024-bit root accepted")
	}
	if _, err := SignatureHeader([]byte("x"), small, `root", alg="none`); err == nil {
		t.Fatal("a keyid that breaks the header was accepted")
	}
	h, err := SignatureHeader([]byte("x"), small, "root-2026")
	if err != nil || !strings.HasSuffix(h, `", keyid="root-2026", alg="rsa-v1_5-sha256"`) {
		t.Fatalf("header %q, %v", h, err)
	}
}

// TestServerBinariesNeverImportSigning is the import guard of D4: signing
// code and the key-file encryption are reachable only from the publisher's
// tools, never from the binaries deployed to the Pi (otactl included).
func TestServerBinariesNeverImportSigning(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "alexmessage/cmd/server", "alexmessage/cmd/admin",
		"alexmessage/cmd/meet", "alexmessage/cmd/otactl").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	deps := strings.Fields(string(out))
	for _, want := range []string{"alexmessage/internal/webapp", "alexmessage/cmd/otactl"} {
		if !slices.Contains(deps, want) {
			t.Fatalf("go list printed no %s:\n%s", want, out)
		}
	}
	for _, dep := range deps {
		if dep == "alexmessage/internal/ota/sign" || dep == "alexmessage/internal/ota/otatest" ||
			dep == "filippo.io/age" || strings.HasPrefix(dep, "filippo.io/age/") {
			t.Errorf("a binary deployed to the Pi imports %s", dep)
		}
	}
}

// privateKeyRE matches the first line of a PEM private key of any type
// (PKCS#8, RSA, EC, OPENSSH, ENCRYPTED).
const privateKeyRE = `-----BEGIN ([A-Z0-9]+ )*PRIVATE KEY-----`

// TestNoPrivateKeysTracked is the private-key guard of D4: no tracked file
// holds a PEM private key or has a key file's extension. The publisher's key
// files are age-encrypted PKCS#8 DER and never contain the PEM header, but a
// decrypted or exported copy would.
func TestNoPrivateKeysTracked(t *testing.T) {
	top, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Skip("not in a git checkout")
	}
	root := strings.TrimSpace(string(top))
	files, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	for _, f := range strings.Split(string(files), "\x00") {
		switch strings.ToLower(path.Ext(f)) {
		case ".key", ".p12", ".pfx", ".jks", ".keystore":
			t.Errorf("tracked key file %s", f)
		}
	}
	// git grep searches the working-tree copy of every tracked file, binary
	// files too; it exits 1 when nothing matches.
	out, err := exec.Command("git", "-C", root, "grep", "-l", "-E", "-e", privateKeyRE).Output()
	var exit *exec.ExitError
	switch {
	case err == nil:
		t.Errorf("tracked files hold a PEM private key:\n%s", out)
	case errors.As(err, &exit) && exit.ExitCode() == 1:
	default:
		t.Fatalf("git grep: %v", err)
	}
}
