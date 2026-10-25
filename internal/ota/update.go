package ota

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// One update as otapublish builds it and otactl checks it (OTAUpdatePlan.md,
// D2 and D6 step 12): the manifest body, its rollBackToEmbedded directive,
// both signatures, and info.json. The publisher on the Mac and otactl on the
// Pi parse with these functions, so the two cannot apply different rules.

// TimeLayout is createdAt and commitTime: UTC with milliseconds.
const TimeLayout = "2006-01-02T15:04:05.000Z"

// FormatTime formats t as a manifest createdAt or directive commitTime.
func FormatTime(t time.Time) string {
	return t.UTC().Format(TimeLayout)
}

// ParseTime parses a stored time: TimeLayout, or any RFC 3339 time.
func ParseTime(s string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, s)
}

// UpdateFiles are the files of updates/<id>/, in the order they are listed.
var UpdateFiles = []string{
	"manifest.json", "manifest.sig", "manifest.leaf",
	"rollback.json", "rollback.sig", "rollback.leaf",
	"info.json",
}

// ManifestAsset is the launch asset or one asset of a manifest.
type ManifestAsset struct {
	Hash          string `json:"hash"` // SHA-256, unpadded base64url
	Key           string `json:"key"`  // MD5 hex: matches the asset embedded in the binary
	ContentType   string `json:"contentType"`
	FileExtension string `json:"fileExtension"`
	URL           string `json:"url"`
}

// ManifestAM is extra.am: what the app shows about the update.
type ManifestAM struct {
	Label    string `json:"label"`
	Commit   string `json:"commit"`
	Notes    string `json:"notes"`
	Critical bool   `json:"critical"`
}

// UpdateManifest is a manifest body (Expo Updates protocol v1), with its
// fields in the order otapublish writes them.
type UpdateManifest struct {
	ID             string          `json:"id"`
	CreatedAt      string          `json:"createdAt"`
	RuntimeVersion string          `json:"runtimeVersion"`
	LaunchAsset    ManifestAsset   `json:"launchAsset"`
	Assets         []ManifestAsset `json:"assets"`
	Metadata       json.RawMessage `json:"metadata"`
	Extra          json.RawMessage `json:"extra"`

	created time.Time
	am      ManifestAM
}

// Created returns createdAt.
func (m *UpdateManifest) Created() time.Time { return m.created }

// AM returns extra.am.
func (m *UpdateManifest) AM() ManifestAM { return m.am }

// AssetNames returns the store file name of the launch asset and of every
// asset, launch asset first, without duplicates.
func (m *UpdateManifest) AssetNames() []string {
	seen := map[string]bool{}
	var names []string
	for _, a := range append([]ManifestAsset{m.LaunchAsset}, m.Assets...) {
		n := path.Base(a.URL) // checked by ParseUpdateManifest
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	return names
}

var (
	md5HexRE  = regexp.MustCompile(`^[0-9a-f]{32}$`)
	fileExtRE = regexp.MustCompile(`^\.[A-Za-z0-9]{1,10}$`)
	labelRE   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	commitRE  = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
)

const (
	// MaxNotes is the longest notes text of an update or a native build.
	MaxNotes   = 2000
	maxContent = 100
)

// ValidLabel reports whether s is a usable update label (otapublish writes
// YYYYMMDD.k).
func ValidLabel(s string) bool { return labelRE.MatchString(s) }

// ParseUpdateManifest parses and checks a manifest body. It is strict on
// purpose: only otapublish writes manifests, so anything else is a sign of
// tampering or a bug.
//   - id is a lowercase UUID, createdAt is TimeLayout, runtimeVersion valid;
//   - metadata is exactly {}: the client filters on metadata with the
//     unsigned expo-manifest-filters header, which must never select;
//   - extra holds only expoClient and am;
//   - every asset URL is absolute http(s), without user, query or fragment,
//     and its path ends in /ota/assets/<hash>.<ext>, the store file name; the
//     launch asset's extension is js.
func ParseUpdateManifest(body []byte) (*UpdateManifest, error) {
	var m UpdateManifest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	if dec.More() {
		return nil, errors.New("manifest: trailing data")
	}
	if id, ok := NormalizeUUID(m.ID); !ok || id != m.ID {
		return nil, fmt.Errorf("manifest: id %q is not a lowercase UUID", m.ID)
	}
	created, err := time.Parse(TimeLayout, m.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("manifest: createdAt %q is not %s", m.CreatedAt, TimeLayout)
	}
	m.created = created
	if !ValidRuntime(m.RuntimeVersion) {
		return nil, fmt.Errorf("manifest: runtimeVersion %q is not valid", m.RuntimeVersion)
	}
	var meta bytes.Buffer
	if err := json.Compact(&meta, m.Metadata); err != nil || meta.String() != "{}" {
		return nil, errors.New("manifest: metadata must be {}")
	}
	var extra struct {
		ExpoClient json.RawMessage `json:"expoClient"`
		AM         *ManifestAM     `json:"am"`
	}
	ed := json.NewDecoder(bytes.NewReader(m.Extra))
	ed.DisallowUnknownFields()
	if err := ed.Decode(&extra); err != nil {
		return nil, fmt.Errorf("manifest: extra: %w", err)
	}
	if extra.AM == nil {
		return nil, errors.New("manifest: extra.am is missing")
	}
	if len(extra.ExpoClient) > 0 && extra.ExpoClient[0] != '{' {
		return nil, errors.New("manifest: extra.expoClient is not an object")
	}
	m.am = *extra.AM
	if err := checkAsset(m.LaunchAsset, true); err != nil {
		return nil, fmt.Errorf("manifest: launchAsset: %w", err)
	}
	if m.Assets == nil {
		return nil, errors.New("manifest: assets is missing")
	}
	for i, a := range m.Assets {
		if err := checkAsset(a, false); err != nil {
			return nil, fmt.Errorf("manifest: assets[%d]: %w", i, err)
		}
	}
	return &m, nil
}

func checkAsset(a ManifestAsset, launch bool) error {
	if raw, err := base64.RawURLEncoding.Strict().DecodeString(a.Hash); err != nil || len(raw) != sha256.Size {
		return fmt.Errorf("hash %q is not an unpadded base64url SHA-256", a.Hash)
	}
	if !md5HexRE.MatchString(a.Key) {
		return fmt.Errorf("key %q is not a lowercase MD5 hex", a.Key)
	}
	if a.ContentType == "" || len(a.ContentType) > maxContent || strings.ContainsAny(a.ContentType, "\r\n") {
		return fmt.Errorf("contentType %q is not valid", a.ContentType)
	}
	if launch && (a.FileExtension != ".bundle" || a.ContentType != "application/javascript") {
		return fmt.Errorf("the launch asset needs fileExtension .bundle and contentType application/javascript, not %q and %q", a.FileExtension, a.ContentType)
	}
	if !fileExtRE.MatchString(a.FileExtension) {
		return fmt.Errorf("fileExtension %q is not valid", a.FileExtension)
	}
	u, err := url.Parse(a.URL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return fmt.Errorf("url %q is not an absolute http(s) URL without user, query or fragment", a.URL)
	}
	dir, name := path.Split(u.EscapedPath())
	hash, ext, ok := ParseAssetName(name)
	if !ok || !strings.HasSuffix(dir, "/ota/assets/") || hash != a.Hash {
		return fmt.Errorf("url %q does not end in /ota/assets/%s.<ext>", a.URL, a.Hash)
	}
	if launch && ext != "js" {
		return fmt.Errorf("the launch asset url must end in .js, not .%s", ext)
	}
	return nil
}

// RollbackDirective is a rollBackToEmbedded directive body.
type RollbackDirective struct {
	Type       string `json:"type"`
	Parameters struct {
		CommitTime string `json:"commitTime"`
	} `json:"parameters"`
}

// NewRollbackDirective returns the directive body for an update created at
// createdAt: its commitTime is one second later, so it applies to that update
// and to every older one.
func NewRollbackDirective(createdAt time.Time) []byte {
	var d RollbackDirective
	d.Type = "rollBackToEmbedded"
	d.Parameters.CommitTime = FormatTime(createdAt.Add(time.Second))
	b, _ := json.Marshal(d)
	return b
}

// ParseRollbackDirective parses a directive body and returns its commitTime.
func ParseRollbackDirective(body []byte) (time.Time, error) {
	var d RollbackDirective
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return time.Time{}, fmt.Errorf("directive: %w", err)
	}
	if dec.More() {
		return time.Time{}, errors.New("directive: trailing data")
	}
	if d.Type != "rollBackToEmbedded" {
		return time.Time{}, fmt.Errorf("directive: type %q is not rollBackToEmbedded", d.Type)
	}
	t, err := time.Parse(TimeLayout, d.Parameters.CommitTime)
	if err != nil {
		return time.Time{}, fmt.Errorf("directive: commitTime %q is not %s", d.Parameters.CommitTime, TimeLayout)
	}
	return t, nil
}

// Validate checks info.json on its own.
func (i *UpdateInfo) Validate() error {
	if !ValidPlatform(i.Platform) {
		return fmt.Errorf("info: platform %q is not valid", i.Platform)
	}
	if !ValidRuntime(i.Runtime) {
		return fmt.Errorf("info: runtime %q is not valid", i.Runtime)
	}
	if !ValidLabel(i.Label) {
		return fmt.Errorf("info: label %q is not valid", i.Label)
	}
	if i.Commit != "" && !commitRE.MatchString(i.Commit) {
		return fmt.Errorf("info: commit %q is not a lowercase hex commit", i.Commit)
	}
	if len(i.Notes) > MaxNotes {
		return fmt.Errorf("info: notes are longer than %d bytes", MaxNotes)
	}
	if _, err := ParseTime(i.CreatedAt); err != nil {
		return fmt.Errorf("info: created_at %q: %w", i.CreatedAt, err)
	}
	return nil
}

// VerifiedUpdate is an update whose files passed VerifyUpdateFiles.
type VerifiedUpdate struct {
	ID           string
	Info         UpdateInfo
	Manifest     *UpdateManifest
	ManifestLeaf *x509.Certificate
	RollbackLeaf *x509.Certificate
}

// Created returns the update's createdAt.
func (v *VerifiedUpdate) Created() time.Time { return v.Manifest.Created() }

// LeafNotAfter is the earlier expiry of the two signing leaves.
func (v *VerifiedUpdate) LeafNotAfter() time.Time {
	if v.RollbackLeaf.NotAfter.Before(v.ManifestLeaf.NotAfter) {
		return v.RollbackLeaf.NotAfter
	}
	return v.ManifestLeaf.NotAfter
}

// VerifyUpdateFiles checks the seven files of one update (UpdateFiles, keyed
// by name): info.json is valid and agrees with the manifest (platform aside,
// which only info.json names); the manifest passes ParseUpdateManifest and
// its id is id; the directive's commitTime is createdAt + 1 s; and both
// signatures verify with the client's chain rules against root, made by the
// leaf that the .leaf file names (found with findLeaf), with keyid
// root-2026. It does not look at asset files.
func VerifyUpdateFiles(id string, files map[string][]byte, findLeaf func(sha string) (*x509.Certificate, error), root *x509.Certificate, now time.Time) (*VerifiedUpdate, error) {
	for _, name := range UpdateFiles {
		if _, ok := files[name]; !ok {
			return nil, fmt.Errorf("update %s: %s is missing", id, name)
		}
	}
	v := &VerifiedUpdate{ID: id}
	if err := json.Unmarshal(files["info.json"], &v.Info); err != nil {
		return nil, fmt.Errorf("update %s: info.json: %w", id, err)
	}
	if err := v.Info.Validate(); err != nil {
		return nil, fmt.Errorf("update %s: %w", id, err)
	}
	m, err := ParseUpdateManifest(files["manifest.json"])
	if err != nil {
		return nil, fmt.Errorf("update %s: %w", id, err)
	}
	v.Manifest = m
	if m.ID != id {
		return nil, fmt.Errorf("update %s: the manifest's id is %s", id, m.ID)
	}
	infoCreated, _ := ParseTime(v.Info.CreatedAt)
	am := m.AM()
	switch {
	case m.RuntimeVersion != v.Info.Runtime:
		return nil, fmt.Errorf("update %s: the manifest's runtime %q differs from info.json's %q", id, m.RuntimeVersion, v.Info.Runtime)
	case !infoCreated.Equal(m.Created()):
		return nil, fmt.Errorf("update %s: info.json's created_at %s differs from createdAt %s", id, v.Info.CreatedAt, m.CreatedAt)
	case am != (ManifestAM{Label: v.Info.Label, Commit: v.Info.Commit, Notes: v.Info.Notes, Critical: v.Info.Critical}):
		return nil, fmt.Errorf("update %s: extra.am differs from info.json", id)
	}
	commitTime, err := ParseRollbackDirective(files["rollback.json"])
	if err != nil {
		return nil, fmt.Errorf("update %s: %w", id, err)
	}
	if !commitTime.Equal(m.Created().Add(time.Second)) {
		return nil, fmt.Errorf("update %s: the directive's commitTime is not createdAt + 1 s", id)
	}
	for _, kind := range []string{KindManifest, KindRollback} {
		leaf, err := verifyStoredSignature(files[kind+".json"], files[kind+".sig"], files[kind+".leaf"], findLeaf, root, now)
		if err != nil {
			return nil, fmt.Errorf("update %s: %s: %w", id, kind, err)
		}
		if kind == KindManifest {
			v.ManifestLeaf = leaf
		} else {
			v.RollbackLeaf = leaf
		}
	}
	return v, nil
}

func verifyStoredSignature(body, sigFile, leafFile []byte, findLeaf func(string) (*x509.Certificate, error), root *x509.Certificate, now time.Time) (*x509.Certificate, error) {
	signature := strings.TrimRight(string(sigFile), "\r\n")
	for i := 0; i < len(signature); i++ {
		if c := signature[i]; c < 0x20 || c > 0x7e {
			return nil, fmt.Errorf("the signature holds character %q, which a header cannot carry", c)
		}
	}
	h, err := ParseSignatureHeader(signature)
	if err != nil {
		return nil, err
	}
	if h.KeyID != RootKeyID {
		return nil, fmt.Errorf("keyid %q is not %s", h.KeyID, RootKeyID)
	}
	leaf, err := findLeaf(strings.TrimSpace(string(leafFile)))
	if err != nil {
		return nil, err
	}
	return VerifySignature(body, signature, EncodeCertificatePEM(leaf), root, now)
}

// ReadUpdateFiles reads the files of updates/<id>/.
func (s *Store) ReadUpdateFiles(id string) (map[string][]byte, error) {
	if n, ok := NormalizeUUID(id); !ok || n != id {
		return nil, fmt.Errorf("invalid update id %q", id)
	}
	files := map[string][]byte{}
	for _, name := range UpdateFiles {
		b, err := os.ReadFile(s.path("updates", id, name))
		if err != nil {
			return nil, err
		}
		files[name] = b
	}
	return files, nil
}

// VerifyUpdate runs VerifyUpdateFiles on a stored update, against the
// store's root and leaves.
func (s *Store) VerifyUpdate(id string, root *x509.Certificate, now time.Time) (*VerifiedUpdate, error) {
	files, err := s.ReadUpdateFiles(id)
	if err != nil {
		return nil, err
	}
	return VerifyUpdateFiles(id, files, s.FindLeaf, root, now)
}

// CheckAssets checks that every asset of m is stored and hashes to its name.
func (s *Store) CheckAssets(m *UpdateManifest) error {
	for _, name := range m.AssetNames() {
		if err := CheckAssetFile(s.path("assets", name), name); err != nil {
			return err
		}
	}
	return nil
}

// CheckAssetFile checks that the file at p hashes to the asset name.
func CheckAssetFile(p, name string) error {
	hash, _, ok := ParseAssetName(name)
	if !ok {
		return fmt.Errorf("%q is not an asset name", name)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	if AssetHash(b) != hash {
		return fmt.Errorf("assets/%s does not hash to its name", name)
	}
	return nil
}

// AssetHash is an asset's hash: SHA-256 as unpadded base64url.
func AssetHash(b []byte) string {
	sum := sha256.Sum256(b)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// CheckRoot checks a root certificate on its own: valid at now, an allowed
// RSA key and signature algorithm, self-signed, and a certificate authority
// with keyCertSign and pathlen:0 (D4), so no intermediate can sit between it
// and a leaf.
func CheckRoot(root *x509.Certificate, now time.Time) error {
	if err := checkCertificate(root, now); err != nil {
		return fmt.Errorf("root certificate: %w", err)
	}
	if !bytes.Equal(root.RawIssuer, root.RawSubject) {
		return errors.New("root certificate is not self-signed")
	}
	if err := root.CheckSignature(root.SignatureAlgorithm, root.RawTBSCertificate, root.Signature); err != nil {
		return fmt.Errorf("root certificate: bad self-signature: %w", err)
	}
	if !isCA(root) || pathLen(root) != 0 {
		return errors.New("root certificate is not a certificate authority with keyCertSign and pathlen:0")
	}
	return nil
}

// CheckLeaf checks a leaf certificate against the root the way a phone does,
// as the only certificate of a certificate_chain part.
func CheckLeaf(leaf, root *x509.Certificate, now time.Time) error {
	_, err := VerifyChain([]*x509.Certificate{leaf}, root, now)
	return err
}

// ReadRootFile reads one root certificate from a PEM file.
func ReadRootFile(p string) (*x509.Certificate, []byte, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, nil, err
	}
	certs, err := ParseCertificatesPEM(b)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", filepath.Base(p), err)
	}
	if len(certs) != 1 {
		return nil, nil, fmt.Errorf("%s holds %d certificates, want 1", filepath.Base(p), len(certs))
	}
	return certs[0], b, nil
}
