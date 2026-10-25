// Package otatest builds update stores (data/ota) for tests: a root and a
// leaf certificate, signed manifests and rollback directives, registered
// runtimes and channel pointers, laid out exactly as OTAUpdatePlan.md D2
// describes. It signs with internal/ota/sign, so only tests may import it.
package otatest

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"alexmessage/internal/ota/sign"
)

// Fixture is an initialized update store in a temporary directory.
type Fixture struct {
	Dir      string
	Now      time.Time
	Root     *x509.Certificate
	RootKey  *rsa.PrivateKey
	Leaf     *x509.Certificate
	LeafKey  *rsa.PrivateKey
	LeafPEM  []byte
	LeafSHA  string
	AssetURL string // the asset base URL written into manifests
}

// Update is an update written by AddUpdate.
type Update struct {
	ID          string
	CreatedAt   time.Time
	Manifest    []byte
	ManifestSig string
	Rollback    []byte
	RollbackSig string
	AssetName   string
	Asset       []byte
}

// RSA key generation is slow, so every fixture of a test binary shares two
// 2048-bit keys (the minimum the verifier accepts).
var (
	keysOnce         sync.Once
	rootKey, leafKey *rsa.PrivateKey
)

func keys() (*rsa.PrivateKey, *rsa.PrivateKey) {
	keysOnce.Do(func() {
		var err error
		if rootKey, err = rsa.GenerateKey(rand.Reader, 2048); err != nil {
			panic(err)
		}
		if leafKey, err = rsa.GenerateKey(rand.Reader, 2048); err != nil {
			panic(err)
		}
	})
	return rootKey, leafKey
}

// New creates an initialized store (FORMAT, the root and one leaf in certs/).
func New(t testing.TB) *Fixture {
	t.Helper()
	rk, lk := keys()
	f := &Fixture{
		Dir:      t.TempDir(),
		Now:      time.Now(),
		RootKey:  rk,
		LeafKey:  lk,
		AssetURL: "https://messages.alexanderjia.com/ota/assets/",
	}
	rootDER, err := sign.NewRoot(rk, "Alex Messages OTA test root", f.Now)
	if err != nil {
		t.Fatal(err)
	}
	if f.Root, err = x509.ParseCertificate(rootDER); err != nil {
		t.Fatal(err)
	}
	leafDER, err := sign.IssueLeaf(f.Root, rk, &lk.PublicKey, "Alex Messages OTA test leaf", f.Now)
	if err != nil {
		t.Fatal(err)
	}
	if f.Leaf, err = x509.ParseCertificate(leafDER); err != nil {
		t.Fatal(err)
	}
	f.LeafPEM = sign.EncodePEM(leafDER)
	sum := sha256.Sum256(leafDER)
	f.LeafSHA = hex.EncodeToString(sum[:])
	f.Write(t, "FORMAT", []byte("1\n"))
	f.Write(t, "certs/ota-root-2026.pem", sign.EncodePEM(rootDER))
	f.Write(t, "certs/leaf-202611.pem", f.LeafPEM)
	return f
}

// Write writes a file under the store, creating its directories.
func (f *Fixture) Write(t testing.TB, rel string, data []byte) {
	t.Helper()
	path := filepath.Join(f.Dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *Fixture) writeJSON(t testing.TB, rel string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	f.Write(t, rel, b)
}

// NewUUID returns a random version-4 UUID in lowercase.
func NewUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// AddUpdate writes a signed update for platform and runtime: its launch
// asset, manifest, rollback directive and info.json.
func (f *Fixture) AddUpdate(t testing.TB, platform, runtime string) *Update {
	t.Helper()
	u := &Update{ID: NewUUID(), CreatedAt: f.Now.UTC().Truncate(time.Millisecond)}
	u.Asset = []byte("console.log(" + `"` + u.ID + `"` + ");\n")
	sum := sha256.Sum256(u.Asset)
	hash := base64.RawURLEncoding.EncodeToString(sum[:])
	u.AssetName = hash + ".js"
	md5sum := md5.Sum(u.Asset)
	manifest := map[string]any{
		"id":             u.ID,
		"createdAt":      u.CreatedAt.Format("2006-01-02T15:04:05.000Z"),
		"runtimeVersion": runtime,
		"launchAsset": map[string]any{
			"hash":          hash,
			"key":           hex.EncodeToString(md5sum[:]),
			"contentType":   "application/javascript",
			"fileExtension": ".bundle",
			"url":           f.AssetURL + u.AssetName,
		},
		"assets":   []any{},
		"metadata": map[string]any{},
		"extra": map[string]any{
			"expoClient": map[string]any{"name": "Alex Messages"},
			"am":         map[string]any{"label": "20261105.1", "commit": "0000000", "notes": "test", "critical": false},
		},
	}
	var err error
	if u.Manifest, err = json.Marshal(manifest); err != nil {
		t.Fatal(err)
	}
	directive := map[string]any{
		"type":       "rollBackToEmbedded",
		"parameters": map[string]any{"commitTime": u.CreatedAt.Add(time.Second).Format("2006-01-02T15:04:05.000Z")},
	}
	if u.Rollback, err = json.Marshal(directive); err != nil {
		t.Fatal(err)
	}
	if u.ManifestSig, err = sign.SignatureHeader(u.Manifest, f.LeafKey, "root-2026"); err != nil {
		t.Fatal(err)
	}
	if u.RollbackSig, err = sign.SignatureHeader(u.Rollback, f.LeafKey, "root-2026"); err != nil {
		t.Fatal(err)
	}
	dir := "updates/" + u.ID + "/"
	f.Write(t, "assets/"+u.AssetName, u.Asset)
	f.Write(t, dir+"manifest.json", u.Manifest)
	f.Write(t, dir+"manifest.sig", []byte(u.ManifestSig+"\n"))
	f.Write(t, dir+"manifest.leaf", []byte(f.LeafSHA+"\n"))
	f.Write(t, dir+"rollback.json", u.Rollback)
	f.Write(t, dir+"rollback.sig", []byte(u.RollbackSig+"\n"))
	f.Write(t, dir+"rollback.leaf", []byte(f.LeafSHA+"\n"))
	f.writeJSON(t, dir+"info.json", map[string]any{
		"platform": platform, "runtime": runtime, "label": "20261105.1", "commit": "0000000",
		"created_at": u.CreatedAt.Format(time.RFC3339), "notes": "test", "critical": false,
	})
	return u
}

// RegisterRuntime registers one native build of platform with runtime and
// the given embedded update id.
func (f *Fixture) RegisterRuntime(t testing.TB, platform, runtime, embeddedID string, build int) {
	t.Helper()
	f.writeJSON(t, "runtimes/"+platform+"/"+runtime+".json", map[string]any{
		"platform": platform,
		"runtime":  runtime,
		"builds": []map[string]any{{
			"build": build, "version": fmt.Sprintf("1.%d.0", build), "embedded_update_id": embeddedID,
			"commit_time": f.Now.UTC().Format(time.RFC3339), "commit": "0000000",
			"registered_at": f.Now.UTC().Format(time.RFC3339),
		}},
	})
}

// Point writes the pointer of channel, platform and runtime.
func (f *Fixture) Point(t testing.TB, channel, platform, runtime, state, updateID string) {
	t.Helper()
	p := map[string]any{"state": state, "since": f.Now.UTC().Format(time.RFC3339)}
	if updateID != "" {
		p["update"] = updateID
	}
	f.writeJSON(t, "channels/"+channel+"/"+platform+"/"+runtime+".json", p)
}

// Revoke writes revoked.txt with ids.
func (f *Fixture) Revoke(t testing.TB, ids ...string) {
	t.Helper()
	var b []byte
	b = append(b, "# revoked update ids\n"...)
	for _, id := range ids {
		b = append(b, id+"\n"...)
	}
	f.Write(t, "revoked.txt", b)
}
