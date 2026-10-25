package ota

import (
	"bufio"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// The update store under data/ota (OTAUpdatePlan.md, D2). Server deploys
// never touch it. The server only reads it; otactl (Phase 2, not built yet)
// will write it, replacing files with WriteFileAtomic.
//
//	FORMAT                                   "1"
//	certs/ota-root-2026.pem, leaf-*.pem      public certificates only
//	assets/<sha256-b64url>.<ext>             write-once
//	updates/<id>/manifest.json, .sig, .leaf  signed bytes, expo-signature value, leaf SHA-256
//	updates/<id>/rollback.json, .sig, .leaf  the update's rollBackToEmbedded directive
//	updates/<id>/info.json                   UpdateInfo
//	runtimes/<platform>/<runtime>.json       RuntimeInfo
//	channels/<channel>/<platform>/<runtime>.json  Pointer
//	revoked.txt                              one update id per line
//	native/policy.json, native/<platform>/*  Policy and the native builds
//	census.db                                the install census (package census)
//	incoming/, log/actions.log               otactl's upload staging and action log

// Pointer states.
const (
	StateUpdate   = "update"
	StateRollback = "rollback"
	StateHalted   = "halted"
)

// Pointer is channels/<channel>/<platform>/<runtime>.json. A halted pointer
// keeps the id of the update it halted.
type Pointer struct {
	State  string `json:"state"`
	Update string `json:"update,omitempty"`
	Since  string `json:"since"` // RFC 3339
}

// RuntimeBuild is one native build registered for a runtime.
type RuntimeBuild struct {
	Build            int    `json:"build"`
	Version          string `json:"version"`
	EmbeddedUpdateID string `json:"embedded_update_id"`
	CommitTime       string `json:"commit_time"` // the embedded app.manifest's commitTime
	Commit           string `json:"commit"`
	RegisteredAt     string `json:"registered_at"`
}

// RuntimeInfo is runtimes/<platform>/<runtime>.json: the native builds whose
// fingerprint is this runtime. Several builds can share one runtime.
type RuntimeInfo struct {
	Platform string         `json:"platform"`
	Runtime  string         `json:"runtime"`
	Builds   []RuntimeBuild `json:"builds"`
}

// UpdateInfo is updates/<id>/info.json.
type UpdateInfo struct {
	Platform  string `json:"platform"`
	Runtime   string `json:"runtime"`
	Label     string `json:"label"`
	Commit    string `json:"commit"`
	CreatedAt string `json:"created_at"`
	Notes     string `json:"notes"`
	Critical  bool   `json:"critical"`
}

// Signed is a stored signed body (manifest.json or rollback.json) with its
// expo-signature value and the certificate that made the signature.
type Signed struct {
	Body      []byte
	Signature string
	Leaf      *x509.Certificate
}

// Kinds of signed body stored for every update.
const (
	KindManifest = "manifest"
	KindRollback = "rollback"
)

var (
	// ErrNotInitialized means data/ota has no FORMAT file (`otactl init`
	// writes it): the store serves nothing.
	ErrNotInitialized = errors.New("ota store is not initialized")

	leafFileRE = regexp.MustCompile(`^leaf-[A-Za-z0-9_.-]{1,64}\.pem$`)
)

// Store reads the update store rooted at Dir.
type Store struct {
	Dir string
}

// DirFromEnv is the store directory: $OTA_DIR, or <dataDir>/ota.
func DirFromEnv(dataDir string) string {
	if d := os.Getenv("OTA_DIR"); d != "" {
		return d
	}
	return filepath.Join(dataDir, "ota")
}

func (s *Store) path(parts ...string) string {
	return filepath.Join(append([]string{s.Dir}, parts...)...)
}

// CheckFormat returns nil for an initialized store of this format,
// ErrNotInitialized when FORMAT is missing, and an error for any other
// format (a store written by a newer otactl).
func (s *Store) CheckFormat() error {
	b, err := os.ReadFile(s.path("FORMAT"))
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNotInitialized
	}
	if err != nil {
		return err
	}
	if v := strings.TrimSpace(string(b)); v != FormatVersion {
		return fmt.Errorf("ota store format %q is not supported (this binary reads %q)", v, FormatVersion)
	}
	return nil
}

// readJSON decodes a small JSON file; a missing file returns (false, nil).
func readJSON(path string, v any) (bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	return true, nil
}

func checkStoreKey(channel, platform, runtime string) error {
	if channel != ChannelProduction && channel != ChannelBeta {
		return fmt.Errorf("invalid channel %q", channel)
	}
	if !ValidPlatform(platform) {
		return fmt.Errorf("invalid platform %q", platform)
	}
	if !ValidRuntime(runtime) {
		return fmt.Errorf("invalid runtime %q", runtime)
	}
	return nil
}

func pointerPath(s *Store, channel, platform, runtime string) string {
	return s.path("channels", channel, platform, runtime+".json")
}

// ReadPointer returns the pointer of one channel, platform and runtime, or
// nil when there is none.
func (s *Store) ReadPointer(channel, platform, runtime string) (*Pointer, error) {
	if err := checkStoreKey(channel, platform, runtime); err != nil {
		return nil, err
	}
	var p Pointer
	path := pointerPath(s, channel, platform, runtime)
	found, err := readJSON(path, &p)
	if err != nil || !found {
		return nil, err
	}
	if err := p.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &p, nil
}

// WritePointer replaces a pointer atomically.
func (s *Store) WritePointer(channel, platform, runtime string, p Pointer) error {
	if err := checkStoreKey(channel, platform, runtime); err != nil {
		return err
	}
	if err := p.validate(); err != nil {
		return err
	}
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return WriteFileAtomic(pointerPath(s, channel, platform, runtime), append(b, '\n'), 0o644)
}

func (p *Pointer) validate() error {
	switch p.State {
	case StateUpdate, StateRollback:
		if id, ok := NormalizeUUID(p.Update); !ok || id != p.Update {
			return fmt.Errorf("pointer: update %q is not a lowercase UUID", p.Update)
		}
	case StateHalted:
		if p.Update != "" {
			if id, ok := NormalizeUUID(p.Update); !ok || id != p.Update {
				return fmt.Errorf("pointer: update %q is not a lowercase UUID", p.Update)
			}
		}
	default:
		return fmt.Errorf("pointer: unknown state %q", p.State)
	}
	if p.Since != "" {
		if _, err := time.Parse(time.RFC3339, p.Since); err != nil {
			return fmt.Errorf("pointer: since: %w", err)
		}
	}
	return nil
}

// ReadRuntime returns the native builds registered for a runtime, or nil when
// the runtime is not registered.
func (s *Store) ReadRuntime(platform, runtime string) (*RuntimeInfo, error) {
	if !ValidPlatform(platform) || !ValidRuntime(runtime) {
		return nil, fmt.Errorf("invalid platform %q or runtime %q", platform, runtime)
	}
	var info RuntimeInfo
	path := s.path("runtimes", platform, runtime+".json")
	found, err := readJSON(path, &info)
	if err != nil || !found {
		return nil, err
	}
	if info.Platform != platform || info.Runtime != runtime {
		return nil, fmt.Errorf("%s: names platform %q and runtime %q", path, info.Platform, info.Runtime)
	}
	if len(info.Builds) == 0 {
		return nil, nil
	}
	for _, b := range info.Builds {
		if id, ok := NormalizeUUID(b.EmbeddedUpdateID); !ok || id != b.EmbeddedUpdateID {
			return nil, fmt.Errorf("%s: embedded_update_id %q is not a lowercase UUID", path, b.EmbeddedUpdateID)
		}
	}
	return &info, nil
}

// IsRegisteredEmbedded reports whether embeddedID is the embedded update id
// of a native build registered for platform and runtime.
func (s *Store) IsRegisteredEmbedded(platform, runtime, embeddedID string) (bool, error) {
	if embeddedID == "" {
		return false, nil
	}
	rt, err := s.ReadRuntime(platform, runtime)
	if err != nil || rt == nil {
		return false, err
	}
	for _, b := range rt.Builds {
		if b.EmbeddedUpdateID == embeddedID {
			return true, nil
		}
	}
	return false, nil
}

// IsRevoked reports whether revoked.txt lists the update id.
func (s *Store) IsRevoked(id string) (bool, error) {
	f, err := os.Open(s.path("revoked.txt"))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.EqualFold(line, id) {
			return true, nil
		}
	}
	return false, sc.Err()
}

// ReadUpdateInfo returns updates/<id>/info.json.
func (s *Store) ReadUpdateInfo(id string) (*UpdateInfo, error) {
	if n, ok := NormalizeUUID(id); !ok || n != id {
		return nil, fmt.Errorf("invalid update id %q", id)
	}
	var info UpdateInfo
	path := s.path("updates", id, "info.json")
	found, err := readJSON(path, &info)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%s is missing", path)
	}
	return &info, nil
}

// ReadSigned returns updates/<id>/<kind>.json with its stored expo-signature
// value and the leaf certificate named in <kind>.leaf. It checks that the
// signature value is well-formed and safe as a header; it does not verify
// the signature (otactl did that before the update could be activated).
func (s *Store) ReadSigned(id, kind string) (*Signed, error) {
	if n, ok := NormalizeUUID(id); !ok || n != id {
		return nil, fmt.Errorf("invalid update id %q", id)
	}
	if kind != KindManifest && kind != KindRollback {
		return nil, fmt.Errorf("invalid kind %q", kind)
	}
	dir := s.path("updates", id)
	body, err := os.ReadFile(filepath.Join(dir, kind+".json"))
	if err != nil {
		return nil, err
	}
	sig, err := os.ReadFile(filepath.Join(dir, kind+".sig"))
	if err != nil {
		return nil, err
	}
	signature := strings.TrimRight(string(sig), "\r\n")
	for i := 0; i < len(signature); i++ {
		if c := signature[i]; c < 0x20 || c > 0x7e {
			return nil, fmt.Errorf("%s.sig: character %q is not allowed in a header", kind, c)
		}
	}
	if _, err := ParseSignatureHeader(signature); err != nil {
		return nil, fmt.Errorf("%s.sig: %w", kind, err)
	}
	leafName, err := os.ReadFile(filepath.Join(dir, kind+".leaf"))
	if err != nil {
		return nil, err
	}
	leaf, err := s.FindLeaf(strings.TrimSpace(string(leafName)))
	if err != nil {
		return nil, fmt.Errorf("%s.leaf: %w", kind, err)
	}
	return &Signed{Body: body, Signature: signature, Leaf: leaf}, nil
}

// FindLeaf returns the certificate in certs/leaf-*.pem whose LeafSHA256 is
// sha.
func (s *Store) FindLeaf(sha string) (*x509.Certificate, error) {
	if !leafSHARE.MatchString(sha) {
		return nil, fmt.Errorf("%q is not a lowercase hex SHA-256", sha)
	}
	entries, err := os.ReadDir(s.path("certs"))
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.Type().IsRegular() || !leafFileRE.MatchString(e.Name()) {
			continue
		}
		b, err := os.ReadFile(s.path("certs", e.Name()))
		if err != nil {
			return nil, err
		}
		certs, err := ParseCertificatesPEM(b)
		if err != nil {
			return nil, fmt.Errorf("certs/%s: %w", e.Name(), err)
		}
		if len(certs) == 1 && LeafSHA256(certs[0]) == sha {
			return certs[0], nil
		}
	}
	return nil, fmt.Errorf("no certs/leaf-*.pem has SHA-256 %s", sha)
}

// ReadRoot returns the root certificate stored at certs/ota-root-2026.pem.
func (s *Store) ReadRoot() (*x509.Certificate, error) {
	b, err := os.ReadFile(s.path("certs", RootCertFile))
	if err != nil {
		return nil, err
	}
	certs, err := ParseCertificatesPEM(b)
	if err != nil {
		return nil, err
	}
	if len(certs) != 1 {
		return nil, fmt.Errorf("certs/%s holds %d certificates, want 1", RootCertFile, len(certs))
	}
	return certs[0], nil
}

// AssetFile returns the path of /ota/assets/<name>, or false when name is
// not a valid asset name. It does not check that the file exists.
func (s *Store) AssetFile(name string) (string, bool) {
	if _, _, ok := ParseAssetName(name); !ok {
		return "", false
	}
	return s.path("assets", name), true
}

// NativeFile returns the path of /app/<platform>/<name>, or false when the
// name is not a native build name of that platform.
func (s *Store) NativeFile(platform, name string) (string, bool) {
	if _, _, ok := ParseNativeName(platform, name); !ok {
		return "", false
	}
	return s.path("native", platform, name), true
}

// ReadPolicy returns native/policy.json; a missing file is the empty policy
// (no gate, no published build).
func (s *Store) ReadPolicy() (*Policy, error) {
	var p Policy
	path := s.path("native", "policy.json")
	if _, err := readJSON(path, &p); err != nil {
		return nil, err
	}
	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &p, nil
}

// WriteFileAtomic writes data to a temporary file in the same directory,
// syncs it, and renames it over path, so a reader sees the old or the new
// content and never a partial file. The parent directory is created if
// needed and synced after the rename.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // a no-op after the rename
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(perm); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
