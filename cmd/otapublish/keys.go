package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"filippo.io/age"
	"filippo.io/age/armor"
	"golang.org/x/term"

	"alexmessage/internal/ota"
	"alexmessage/internal/ota/sign"
)

// Key files (OTAUpdatePlan.md, D4). A private key exists on disk only as an
// age file: the key's PKCS#8 DER, encrypted with a passphrase (age's scrypt
// mode) and ASCII-armored, so it can be pasted into a password manager. The
// decrypted key lives only in this process's memory, and it is never PEM.
// Key and certificate files are created with O_EXCL: otapublish never
// overwrites one.

const (
	rootKeyBits   = 4096
	leafKeyBits   = 3072
	minPassphrase = 12
	// drillPassphrase protects the drill profile's throwaway keys (Phase 5).
	// It is not a secret: drill keys sign only for the drill build's own
	// root, so Claude can run the drill without a passphrase prompt.
	drillPassphrase = "alex-messages-drill-keys"
	ageArmorHeader  = "-----BEGIN AGE ENCRYPTED FILE-----"
)

// readPassphrase reads a passphrase from the terminal without echo. It reads
// /dev/tty, never standard input, so a piped command cannot supply it.
func readPassphrase(prompt string) ([]byte, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("a passphrase is read from the terminal, and this process has none (%v): run the command in a terminal", err)
	}
	defer tty.Close()
	fmt.Fprint(tty, prompt)
	p, err := term.ReadPassword(int(tty.Fd()))
	fmt.Fprintln(tty)
	return p, err
}

// askPassphrase asks for the passphrase of an existing key file.
func (a *app) askPassphrase(what string) ([]byte, error) {
	if a.drill() {
		return []byte(drillPassphrase), nil
	}
	return a.passphrase(what + " passphrase: ")
}

// newPassphrase asks twice for the passphrase of a new key file. It must be
// at least minPassphrase bytes and differ from other (D4: the root and the
// leaf have different passphrases).
func (a *app) newPassphrase(what string, other []byte) ([]byte, error) {
	if a.drill() {
		return []byte(drillPassphrase), nil
	}
	p1, err := a.passphrase(fmt.Sprintf("New %s passphrase (at least %d characters): ", what, minPassphrase))
	if err != nil {
		return nil, err
	}
	if len(p1) < minPassphrase {
		return nil, fmt.Errorf("the passphrase has %d characters; at least %d are needed", len(p1), minPassphrase)
	}
	if other != nil && bytes.Equal(p1, other) {
		return nil, fmt.Errorf("the %s passphrase must differ from the root passphrase", what)
	}
	p2, err := a.passphrase(fmt.Sprintf("Type the new %s passphrase again: ", what))
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(p1, p2) {
		return nil, errors.New("the two passphrases differ; nothing was written")
	}
	return p1, nil
}

// encryptKey returns the age file of key.
func encryptKey(key *rsa.PrivateKey, passphrase []byte, workFactor int) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	defer clear(der)
	r, err := age.NewScryptRecipient(string(passphrase))
	if err != nil {
		return nil, err
	}
	r.SetWorkFactor(workFactor)
	var buf bytes.Buffer
	aw := armor.NewWriter(&buf)
	w, err := age.Encrypt(aw, r)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(der); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	if err := aw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// decryptKey decrypts an age file (armored or binary) made by encryptKey.
func decryptKey(data, passphrase []byte) (*rsa.PrivateKey, error) {
	id, err := age.NewScryptIdentity(string(passphrase))
	if err != nil {
		return nil, err
	}
	var src io.Reader = bytes.NewReader(data)
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte(ageArmorHeader)) {
		src = armor.NewReader(bytes.NewReader(bytes.TrimSpace(data)))
	}
	r, err := age.Decrypt(src, id)
	if err != nil {
		var nomatch *age.NoIdentityMatchError
		if errors.As(err, &nomatch) {
			return nil, errors.New("wrong passphrase, or not a passphrase-encrypted key file")
		}
		return nil, err
	}
	der, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	defer clear(der)
	k, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("the decrypted file is not a PKCS#8 key: %w", err)
	}
	key, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("the key is a %T, not an RSA key", k)
	}
	return key, nil
}

// unlockKey reads and decrypts a key file, asking for its passphrase.
func (a *app) unlockKey(path, what string) (*rsa.PrivateKey, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	pass, err := a.askPassphrase(fmt.Sprintf("%s (%s)", what, filepath.Base(path)))
	if err != nil {
		return nil, nil, err
	}
	key, err := decryptKey(data, pass)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	return key, pass, nil
}

// sealKey encrypts key and checks that the result decrypts to the same key.
func (a *app) sealKey(key *rsa.PrivateKey, passphrase []byte) ([]byte, error) {
	enc, err := encryptKey(key, passphrase, a.workFactor)
	if err != nil {
		return nil, err
	}
	back, err := decryptKey(enc, passphrase)
	if err != nil {
		return nil, fmt.Errorf("the encrypted key does not decrypt: %w", err)
	}
	if !back.PublicKey.Equal(&key.PublicKey) {
		return nil, errors.New("the encrypted key decrypts to another key")
	}
	return enc, nil
}

// writeNew creates path with data; it never replaces an existing file.
func writeNew(path string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%s already exists; otapublish never overwrites a key or a certificate", path)
	}
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	return f.Close()
}

func pathExists(p string) (bool, error) {
	_, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// refuseExisting fails if any of the paths exists.
func refuseExisting(paths ...string) error {
	for _, p := range paths {
		ok, err := pathExists(p)
		if err != nil {
			return err
		}
		if ok {
			return fmt.Errorf("%s already exists; otapublish never overwrites a key or a certificate", p)
		}
	}
	return nil
}

// spkiSHA256 is the hex SHA-256 of a public key's SubjectPublicKeyInfo.
func spkiSHA256(pub *rsa.PublicKey) string {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "?"
	}
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// archivedRoot reads the root certificate from the archive. When the
// worktree holds mobile/certs/ota-root-2026.pem (the copy every binary
// embeds), the two must be the same certificate.
func (a *app) archivedRoot() (*x509.Certificate, []byte, error) {
	p := a.otaArchive("certs", ota.RootCertFile)
	root, b, err := ota.ReadRootFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, fmt.Errorf("the archive has no root certificate %s; otapublish keys init-root writes it", p)
	}
	if err != nil {
		return nil, nil, err
	}
	if a.repo != "" && !a.drill() {
		embedded := filepath.Join(a.repo, "mobile", "certs", ota.RootCertFile)
		if other, _, err := ota.ReadRootFile(embedded); err == nil {
			if !bytes.Equal(other.Raw, root.Raw) {
				return nil, nil, fmt.Errorf("%s and the archive's %s are different certificates", embedded, p)
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, nil, err
		}
	}
	if err := ota.CheckRoot(root, a.now()); err != nil {
		return nil, nil, err
	}
	return root, b, nil
}

func setupInitRoot(fs *flag.FlagSet) func(a *app, args []string) error {
	id := fs.String("id", ota.RootKeyID, "the root's key id: build 2 embeds root-2026")
	out := fs.String("out", "", "the directory for the encrypted root key: the USB stick")
	certOut := fs.String("cert-out", "", "where to write the root certificate: mobile/certs/ota-root-2026.pem")
	return func(a *app, args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		if *id != ota.RootKeyID {
			return usagef("--id must be %s: every binary and the server know the root by that id", ota.RootKeyID)
		}
		if *out == "" || *certOut == "" {
			return usagef("--out and --cert-out are required")
		}
		return a.staged(stageOptions{}, func() error { return a.initRoot(*out, *certOut) })
	}
}

func (a *app) initRoot(out, certOut string) error {
	st, err := os.Stat(out)
	if err != nil || !st.IsDir() {
		return fmt.Errorf("--out %s is not a directory: plug in the USB stick and name its folder", out)
	}
	if !a.drill() {
		// D4: the root key is never on the Mac's disk.
		if err := a.removable(out); err != nil {
			return err
		}
	}
	keyPath := filepath.Join(out, "ota-"+ota.RootKeyID+".key.age")
	archiveCert := a.otaArchive("certs", ota.RootCertFile)
	if err := refuseExisting(keyPath, certOut, archiveCert); err != nil {
		return err
	}
	pass, err := a.newPassphrase("root", nil)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stderr, "Generating an RSA-%d key…\n", a.rootBits)
	key, err := rsa.GenerateKey(rand.Reader, a.rootBits)
	if err != nil {
		return err
	}
	der, err := sign.NewRoot(key, "Alex Messages OTA "+ota.RootKeyID, a.now())
	if err != nil {
		return err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return err
	}
	if err := ota.CheckRoot(cert, a.now()); err != nil {
		return err
	}
	enc, err := a.sealKey(key, pass)
	if err != nil {
		return err
	}
	pemBytes := sign.EncodePEM(der)
	if err := writeNew(keyPath, enc, 0o600); err != nil {
		return err
	}
	for _, p := range []string{certOut, archiveCert} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := writeNew(p, pemBytes, 0o644); err != nil {
			return err
		}
	}
	a.printf("Root key (encrypted): %s\n", keyPath)
	a.printf("Root certificate: %s and %s\n", certOut, archiveCert)
	a.printf("Public key SHA-256: %s\n", spkiSHA256(&key.PublicKey))
	a.printf("Valid %s to %s\n", cert.NotBefore.UTC().Format(time.RFC3339), cert.NotAfter.UTC().Format(time.RFC3339))
	if !a.drill() {
		a.printf("Next (plan step 3.1): copy %s into your password manager as an attachment; write the root passphrase on paper and keep it with the USB stick; then run otapublish keys issue-leaf --root %s --out ~/.alexmessages/keys/\n", keyPath, keyPath)
	}
	return nil
}

func setupIssueLeaf(fs *flag.FlagSet) func(a *app, args []string) error {
	root := fs.String("root", "", "the encrypted root key: <stick>/ota-root-2026.key.age")
	out := fs.String("out", "", "the directory for the encrypted leaf key (default: --keys-dir)")
	noUpload := fs.Bool("no-upload", false, "do not copy the certificates to the Pi")
	return func(a *app, args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		if *root == "" {
			return usagef("--root is required")
		}
		dir := *out
		if dir == "" {
			dir = a.keysDir
		}
		return a.staged(stageOptions{}, func() error { return a.issueLeaf(*root, dir, *noUpload) })
	}
}

// leafNames returns the first free leaf name for this month: 202611, then
// 202611-2, 202611-3…, free in the key directory and in the archive.
func (a *app) leafName(out string) (string, error) {
	base := a.now().Format("200601")
	for i := 1; i < 100; i++ {
		name := base
		if i > 1 {
			name = fmt.Sprintf("%s-%d", base, i)
		}
		used := false
		for _, p := range []string{leafKeyPath(out, name), leafCertPath(out, name), a.otaArchive("certs", "leaf-"+name+".pem")} {
			ok, err := pathExists(p)
			if err != nil {
				return "", err
			}
			used = used || ok
		}
		if !used {
			return name, nil
		}
	}
	return "", errors.New("no free leaf name this month")
}

func leafKeyPath(dir, name string) string  { return filepath.Join(dir, "ota-leaf-"+name+".key.age") }
func leafCertPath(dir, name string) string { return filepath.Join(dir, "ota-leaf-"+name+".pem") }

func (a *app) issueLeaf(rootPath, out string, noUpload bool) error {
	rootCert, _, err := a.archivedRoot()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o700); err != nil {
		return err
	}
	name, err := a.leafName(out)
	if err != nil {
		return err
	}
	rootKey, rootPass, err := a.unlockKey(rootPath, "Root")
	if err != nil {
		return err
	}
	if !rootKey.PublicKey.Equal(rootCert.PublicKey) {
		return fmt.Errorf("%s is not the key of the root certificate %s", rootPath, a.otaArchive("certs", ota.RootCertFile))
	}
	pass, err := a.newPassphrase("leaf", rootPass)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stderr, "Generating an RSA-%d key…\n", a.leafBits)
	key, err := rsa.GenerateKey(rand.Reader, a.leafBits)
	if err != nil {
		return err
	}
	der, err := sign.IssueLeaf(rootCert, rootKey, &key.PublicKey, "Alex Messages OTA leaf "+name, a.now())
	if err != nil {
		return err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return err
	}
	if err := ota.CheckLeaf(cert, rootCert, a.now()); err != nil {
		return err
	}
	enc, err := a.sealKey(key, pass)
	if err != nil {
		return err
	}
	pemBytes := sign.EncodePEM(der)
	keyPath, certPath, archiveCert := leafKeyPath(out, name), leafCertPath(out, name), a.otaArchive("certs", "leaf-"+name+".pem")
	if err := writeNew(keyPath, enc, 0o600); err != nil {
		return err
	}
	if err := writeNew(certPath, pemBytes, 0o644); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(archiveCert), 0o755); err != nil {
		return err
	}
	if err := writeNew(archiveCert, pemBytes, 0o644); err != nil {
		return err
	}
	a.printf("Leaf key (encrypted): %s\n", keyPath)
	a.printf("Leaf certificate: %s and %s\n", certPath, archiveCert)
	a.printf("Certificate SHA-256 (the .leaf files name it): %s\n", ota.LeafSHA256(cert))
	a.printf("Valid %s to %s\n", cert.NotBefore.UTC().Format(time.RFC3339), cert.NotAfter.UTC().Format(time.RFC3339))
	if noUpload {
		a.printf("Not uploaded (--no-upload). Copy the certificates to the Pi with: otapublish keys upload-certs\n")
	} else if err := a.uploadCertsIfPossible(); err != nil {
		return fmt.Errorf("the leaf was created, but the certificates were not uploaded: %w; run otapublish keys upload-certs once that is fixed", err)
	}
	a.printf("Signatures are made with the newest leaf from now on. Updates already published keep their old signatures until you run: otapublish resign-active\n")
	return nil
}

// uploadCertsIfPossible uploads the certificates when otactl exists on the
// Pi (plan step 2.2: issue-leaf runs upload-certs once otactl is there).
func (a *app) uploadCertsIfPossible() error {
	p, err := a.pi()
	if err != nil {
		return err
	}
	ready, err := p.ready()
	if err != nil {
		return err
	}
	if !ready {
		a.printf("Not uploaded: %s has no update store yet (it arrives with plan step 6.2 and otactl init in 6.3). Upload the certificates then with: otapublish keys upload-certs\n", p)
		return nil
	}
	return a.uploadCerts(p)
}

func setupUploadCerts(fs *flag.FlagSet) func(a *app, args []string) error {
	return func(a *app, args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		p, err := a.pi()
		if err != nil {
			return err
		}
		return a.uploadCerts(p)
	}
}

// uploadCerts copies the root and every unexpired leaf certificate from the
// archive to the store's certs/ through otactl ingest.
func (a *app) uploadCerts(p pi) error {
	root, rootPEM, err := a.archivedRoot()
	if err != nil {
		return err
	}
	dir, err := a.tempDir("certs")
	if err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, "certs", ota.RootCertFile), rootPEM); err != nil {
		return err
	}
	leaves, err := a.archivedLeaves(root)
	if err != nil {
		return err
	}
	for _, l := range leaves {
		if l.err != nil {
			a.printf("not uploaded: certs/%s (%v)\n", l.file, l.err)
			continue
		}
		if err := writeFile(filepath.Join(dir, "certs", l.file), l.pem); err != nil {
			return err
		}
	}
	plan, err := a.ingest(p, dir, "certs")
	if err != nil {
		return err
	}
	a.printf("%s: root certificate %s, %d new leaf certificate(s), %d already stored\n", p,
		map[bool]string{true: "installed", false: "already stored"}[plan.Root], len(plan.Leaves), plan.Unchanged-boolInt(!plan.Root))
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// archivedLeaf is one certs/leaf-*.pem of the archive.
type archivedLeaf struct {
	file string // leaf-<name>.pem
	name string // <name>
	cert *x509.Certificate
	pem  []byte
	err  error // why it cannot sign: expired, or does not chain to the root
}

// archivedLeaves lists the archive's leaf certificates, newest first.
func (a *app) archivedLeaves(root *x509.Certificate) ([]archivedLeaf, error) {
	entries, err := os.ReadDir(a.otaArchive("certs"))
	if err != nil {
		return nil, err
	}
	var out []archivedLeaf
	for _, e := range entries {
		if !e.Type().IsRegular() || !ota.ValidLeafFileName(e.Name()) {
			continue
		}
		b, err := os.ReadFile(a.otaArchive("certs", e.Name()))
		if err != nil {
			return nil, err
		}
		l := archivedLeaf{file: e.Name(), name: strings.TrimSuffix(strings.TrimPrefix(e.Name(), "leaf-"), ".pem"), pem: b}
		certs, err := ota.ParseCertificatesPEM(b)
		if err != nil || len(certs) != 1 {
			return nil, fmt.Errorf("%s is not one certificate (%v)", a.otaArchive("certs", e.Name()), err)
		}
		l.cert = certs[0]
		l.err = ota.CheckLeaf(l.cert, root, a.now())
		out = append(out, l)
	}
	sortLeaves(out)
	return out, nil
}

// sortLeaves puts the newest leaf first: the latest NotBefore, and for two
// leaves issued in the same second the later name (202611-2 after 202611).
func sortLeaves(l []archivedLeaf) {
	sort.Slice(l, func(i, j int) bool {
		if !l[i].cert.NotBefore.Equal(l[j].cert.NotBefore) {
			return l[i].cert.NotBefore.After(l[j].cert.NotBefore)
		}
		if len(l[i].name) != len(l[j].name) {
			return len(l[i].name) > len(l[j].name)
		}
		return l[i].name > l[j].name
	})
}

// signer is the leaf that signs: its decrypted key and its certificate.
type signer struct {
	name string
	key  *rsa.PrivateKey
	cert *x509.Certificate
	pem  []byte
	root *x509.Certificate
}

// signingLeaf picks the newest archived leaf that is valid for at least 30
// more days and whose encrypted key is in the key directory, and decrypts it
// (asking for its passphrase).
func (a *app) signingLeaf() (*signer, error) {
	root, _, err := a.archivedRoot()
	if err != nil {
		return nil, err
	}
	leaves, err := a.archivedLeaves(root)
	if err != nil {
		return nil, err
	}
	var reasons []string
	for _, l := range leaves {
		keyPath := leafKeyPath(a.keysDir, l.name)
		switch ok, err := pathExists(keyPath); {
		case err != nil:
			return nil, err
		case l.err != nil:
			reasons = append(reasons, fmt.Sprintf("%s: %v", l.file, l.err))
			continue
		case l.cert.NotAfter.Before(a.now().Add(leafMargin)):
			reasons = append(reasons, fmt.Sprintf("%s expires %s, within 30 days (otactl activate would refuse it)", l.file, l.cert.NotAfter.UTC().Format(time.RFC3339)))
			continue
		case !ok:
			reasons = append(reasons, fmt.Sprintf("%s: its key %s is missing", l.file, keyPath))
			continue
		}
		key, _, err := a.unlockKey(keyPath, "Leaf")
		if err != nil {
			return nil, err
		}
		if !key.PublicKey.Equal(l.cert.PublicKey) {
			return nil, fmt.Errorf("%s is not the key of %s", keyPath, a.otaArchive("certs", l.file))
		}
		return &signer{name: l.name, key: key, cert: l.cert, pem: l.pem, root: root}, nil
	}
	if len(reasons) == 0 {
		reasons = []string{"the archive has no leaf certificate"}
	}
	return nil, fmt.Errorf("no leaf can sign; issue one with otapublish keys issue-leaf (%s)", strings.Join(reasons, "; "))
}

// leafMargin: otactl activate refuses a leaf that expires sooner (D4).
const leafMargin = 30 * 24 * time.Hour

func setupShow(fs *flag.FlagSet) func(a *app, args []string) error {
	root := fs.String("root", "", "an encrypted root key file")
	leaf := fs.String("leaf", "", "an encrypted leaf key file")
	return func(a *app, args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		if (*root == "") == (*leaf == "") {
			return usagef("give exactly one of --root and --leaf")
		}
		return a.staged(stageOptions{}, func() error { return a.show(*root, *leaf) })
	}
}

// show decrypts a key file and compares it with every certificate it should
// belong to. It prints only public material.
func (a *app) show(rootPath, leafPath string) error {
	path, what := rootPath, "Root"
	var certs []string
	if rootPath != "" {
		if a.repo != "" {
			certs = append(certs, filepath.Join(a.repo, "mobile", "certs", ota.RootCertFile))
		}
		certs = append(certs, a.otaArchive("certs", ota.RootCertFile))
	} else {
		path, what = leafPath, "Leaf"
		base := strings.TrimSuffix(filepath.Base(leafPath), ".key.age")
		name := strings.TrimPrefix(base, "ota-leaf-")
		certs = append(certs, filepath.Join(filepath.Dir(leafPath), base+".pem"), a.otaArchive("certs", "leaf-"+name+".pem"))
	}
	key, _, err := a.unlockKey(path, what)
	if err != nil {
		return err
	}
	a.printf("%s decrypts: an RSA-%d private key\n", path, key.N.BitLen())
	a.printf("Public key SHA-256: %s\n", spkiSHA256(&key.PublicKey))
	found, mismatch := 0, 0
	for _, p := range certs {
		b, err := os.ReadFile(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		parsed, err := ota.ParseCertificatesPEM(b)
		if err != nil || len(parsed) != 1 {
			return fmt.Errorf("%s is not one certificate (%v)", p, err)
		}
		c := parsed[0]
		found++
		match := "the key MATCHES this certificate"
		if !key.PublicKey.Equal(c.PublicKey) {
			match, mismatch = "the key DOES NOT MATCH this certificate", mismatch+1
		}
		a.printf("\n%s\n  subject %s, valid %s to %s\n  certificate SHA-256 %s\n  %s\n%s", p, c.Subject.CommonName,
			c.NotBefore.UTC().Format(time.RFC3339), c.NotAfter.UTC().Format(time.RFC3339), ota.LeafSHA256(c), match, b)
	}
	if found == 0 {
		a.printf("No certificate to compare with was found (looked for %s).\n", strings.Join(certs, ", "))
	}
	if mismatch > 0 {
		return fmt.Errorf("%d certificate(s) do not belong to this key", mismatch)
	}
	return nil
}
