package ota

import (
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// The update store under data/ota (OTAUpdatePlan.md, D2). Server deploys
// never touch it. The server only reads it; cmd/otactl writes it, replacing
// files with WriteFileAtomic or renaming whole files and directories into
// place.
//
//	FORMAT                                   "1"
//	certs/ota-root-2026.pem, leaf-*.pem      public certificates only
//	assets/<sha256-b64url>.<ext>             write-once
//	updates/<id>/manifest.json, .sig, .leaf  signed bytes, expo-signature value, leaf SHA-256
//	updates/<id>/rollback.json, .sig, .leaf  the update's rollBackToEmbedded directive
//	updates/<id>/info.json                   UpdateInfo
//	test/<id>/manifest.json, info.json       otactl test-unsigned's unsigned manifest
//	runtimes/<platform>/<runtime>.json       RuntimeInfo
//	channels/<channel>/<platform>/<runtime>.json  Pointer
//	revoked.txt                              one update id per line
//	native/policy.json, native/<platform>/*  Policy and the native builds
//	census.db                                the install census (package census)
//	incoming/, log/actions.log, .lock        otactl's upload staging, action log and lock

// Pointer states.
const (
	StateUpdate   = "update"
	StateRollback = "rollback"
	StateHalted   = "halted"
	// StateTestUnsigned serves the unsigned manifest test/<id>/manifest.json
	// until Until, then nothing (otactl test-unsigned, plan step 6.4). Only a
	// beta pointer can be in this state. Previous is the pointer it replaced,
	// which `otactl test-unsigned --clear` restores.
	StateTestUnsigned = "test_unsigned"
)

// Pointer is channels/<channel>/<platform>/<runtime>.json. A halted pointer
// keeps the id of the update it halted.
type Pointer struct {
	State    string   `json:"state"`
	Update   string   `json:"update,omitempty"`
	Since    string   `json:"since"`              // RFC 3339
	Until    string   `json:"until,omitempty"`    // RFC 3339; test_unsigned only
	Previous *Pointer `json:"previous,omitempty"` // test_unsigned only
}

// PointerEntry is a pointer with the channel, platform and runtime it
// belongs to.
type PointerEntry struct {
	Channel  string `json:"channel"`
	Platform string `json:"platform"`
	Runtime  string `json:"runtime"`
	Pointer
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
	if err := p.Validate(channel); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &p, nil
}

// WritePointer replaces a pointer atomically.
func (s *Store) WritePointer(channel, platform, runtime string, p Pointer) error {
	if err := checkStoreKey(channel, platform, runtime); err != nil {
		return err
	}
	if err := p.Validate(channel); err != nil {
		return err
	}
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return WriteFileAtomic(pointerPath(s, channel, platform, runtime), append(b, '\n'), 0o644)
}

// RemovePointer deletes a pointer; phones of that channel, platform and
// runtime then get nothing (beta phones fall back to production).
func (s *Store) RemovePointer(channel, platform, runtime string) error {
	if err := checkStoreKey(channel, platform, runtime); err != nil {
		return err
	}
	err := os.Remove(pointerPath(s, channel, platform, runtime))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// Validate checks a pointer of channel.
func (p *Pointer) Validate(channel string) error {
	validID := func() error {
		if id, ok := NormalizeUUID(p.Update); !ok || id != p.Update {
			return fmt.Errorf("pointer: update %q is not a lowercase UUID", p.Update)
		}
		return nil
	}
	switch p.State {
	case StateUpdate, StateRollback:
		if err := validID(); err != nil {
			return err
		}
	case StateHalted:
		if p.Update != "" {
			if err := validID(); err != nil {
				return err
			}
		}
	case StateTestUnsigned:
		if channel != ChannelBeta {
			return errors.New("pointer: only a beta pointer can be in state test_unsigned")
		}
		if err := validID(); err != nil {
			return err
		}
		if _, err := time.Parse(time.RFC3339, p.Until); err != nil {
			return fmt.Errorf("pointer: until: %w", err)
		}
		if p.Previous != nil {
			if p.Previous.State == StateTestUnsigned {
				return errors.New("pointer: previous is in state test_unsigned")
			}
			if err := p.Previous.Validate(channel); err != nil {
				return fmt.Errorf("previous: %w", err)
			}
		}
	default:
		return fmt.Errorf("pointer: unknown state %q", p.State)
	}
	if p.State != StateTestUnsigned && (p.Until != "" || p.Previous != nil) {
		return fmt.Errorf("pointer: until and previous belong to state test_unsigned, not %s", p.State)
	}
	if p.Since != "" {
		if _, err := time.Parse(time.RFC3339, p.Since); err != nil {
			return fmt.Errorf("pointer: since: %w", err)
		}
	}
	return nil
}

// ListPointers returns every pointer, sorted by channel, platform and
// runtime.
func (s *Store) ListPointers() ([]PointerEntry, error) {
	var out []PointerEntry
	for _, channel := range []string{ChannelBeta, ChannelProduction} {
		for _, platform := range []string{PlatformAndroid, PlatformIOS} {
			runtimes, err := s.listJSON("channels", channel, platform)
			if err != nil {
				return nil, err
			}
			for _, rt := range runtimes {
				p, err := s.ReadPointer(channel, platform, rt)
				if err != nil {
					return nil, err
				}
				if p != nil {
					out = append(out, PointerEntry{Channel: channel, Platform: platform, Runtime: rt, Pointer: *p})
				}
			}
		}
	}
	return out, nil
}

// ListRuntimes returns every registered runtime, Android first.
func (s *Store) ListRuntimes() ([]RuntimeInfo, error) {
	var out []RuntimeInfo
	for _, platform := range []string{PlatformAndroid, PlatformIOS} {
		runtimes, err := s.listJSON("runtimes", platform)
		if err != nil {
			return nil, err
		}
		for _, rt := range runtimes {
			info, err := s.ReadRuntime(platform, rt)
			if err != nil {
				return nil, err
			}
			if info != nil {
				out = append(out, *info)
			}
		}
	}
	return out, nil
}

// listJSON returns the runtimes named by the <runtime>.json files of a
// directory, sorted; a missing directory has none. Temporary files of
// WriteFileAtomic start with a dot and are skipped.
func (s *Store) listJSON(parts ...string) ([]string, error) {
	entries, err := os.ReadDir(s.path(parts...))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		rt, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || !e.Type().IsRegular() || !ValidRuntime(rt) {
			continue
		}
		names = append(names, rt)
	}
	return names, nil // os.ReadDir sorts by name
}

// Revoked returns the ids in revoked.txt, in file order, lowercase.
func (s *Store) Revoked() ([]string, error) {
	b, err := os.ReadFile(s.path("revoked.txt"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		ids = append(ids, strings.ToLower(line))
	}
	return ids, nil
}

// ListUpdates returns the ids of every stored update, sorted.
func (s *Store) ListUpdates() ([]string, error) {
	entries, err := os.ReadDir(s.path("updates"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if id, ok := NormalizeUUID(e.Name()); ok && id == e.Name() && e.IsDir() {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// ReadTestManifest returns test/<id>/manifest.json and its info.json: the
// unsigned manifest of otactl test-unsigned.
func (s *Store) ReadTestManifest(id string) ([]byte, *UpdateInfo, error) {
	if n, ok := NormalizeUUID(id); !ok || n != id {
		return nil, nil, fmt.Errorf("invalid update id %q", id)
	}
	body, err := os.ReadFile(s.path("test", id, "manifest.json"))
	if err != nil {
		return nil, nil, err
	}
	var info UpdateInfo
	found, err := readJSON(s.path("test", id, "info.json"), &info)
	if err != nil {
		return nil, nil, err
	}
	if !found {
		return nil, nil, fmt.Errorf("test/%s/info.json is missing", id)
	}
	return body, &info, nil
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
	ids, err := s.Revoked()
	if err != nil {
		return false, err
	}
	return slices.Contains(ids, strings.ToLower(id)), nil
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

// ValidLeafFileName reports whether name is a leaf certificate file name of
// certs/: leaf-<name>.pem.
func ValidLeafFileName(name string) bool { return leafFileRE.MatchString(name) }

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
	root, _, err := ReadRootFile(s.path("certs", RootCertFile))
	return root, err
}

// Path returns the path of a file or directory inside the store.
func (s *Store) Path(parts ...string) string {
	return s.path(parts...)
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
