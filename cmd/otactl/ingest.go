package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"alexmessage/internal/ota"
)

// ingest moves one upload from data/ota/incoming/<publishId>/ into the store
// (OTAUpdatePlan.md, D6 steps 14 and 15). The upload mirrors the store:
//
//	certs/ota-root-2026.pem, certs/leaf-*.pem
//	assets/<sha256-b64url>.<ext>
//	updates/<id>/            the seven files of ota.UpdateFiles
//	native/<platform>/AlexMessages-<version>-<build>.<apk|ipa>
//
// It checks the whole upload before it moves anything: no symbolic links or
// unknown files; every asset hashes to its name; the root matches the stored
// root (or, in an empty store, is a valid root); every leaf chains to the
// root; every update passes ota.VerifyUpdateFiles (both signatures, metadata
// {}, the directive's commitTime), its createdAt is at most 5 minutes ahead of
// this clock, and every asset it names is uploaded or stored. Published bytes
// are never overwritten: a file that exists must be identical, except an
// update's .sig and .leaf files, which resign-active replaces once the new
// signature verifies. Running it again after a failure is safe.

var publishIDRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// maxAhead is how far a manifest's createdAt may be ahead of the Pi's clock.
const maxAhead = 5 * time.Minute

type upload struct {
	dir     string
	root    []byte                       // certs/ota-root-2026.pem; nil when absent
	leaves  map[string][]byte            // certs/leaf-*.pem by file name
	assets  map[string]bool              // asset file names
	updates map[string]map[string][]byte // update id → file name → bytes
	native  map[string][]string          // platform → file names
}

func scanUpload(dir string) (*upload, error) {
	st, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", dir)
	}
	u := &upload{dir: dir, leaves: map[string][]byte{}, assets: map[string]bool{},
		updates: map[string]map[string][]byte{}, native: map[string][]string{}}
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symbolic link", rel)
		}
		parts := strings.Split(rel, "/")
		if d.IsDir() {
			if !uploadDir(parts) {
				return fmt.Errorf("unexpected directory %s", rel)
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", rel)
		}
		switch {
		case len(parts) == 2 && parts[0] == "certs" && parts[1] == ota.RootCertFile:
			u.root, err = os.ReadFile(p)
			return err
		case len(parts) == 2 && parts[0] == "certs" && ota.ValidLeafFileName(parts[1]):
			u.leaves[parts[1]], err = os.ReadFile(p)
			return err
		case len(parts) == 2 && parts[0] == "assets":
			if _, _, ok := ota.ParseAssetName(parts[1]); ok {
				u.assets[parts[1]] = true
				return nil
			}
		case len(parts) == 3 && parts[0] == "updates" && isUpdateFile(parts[2]):
			files := u.updates[parts[1]]
			if files == nil {
				files = map[string][]byte{}
				u.updates[parts[1]] = files
			}
			files[parts[2]], err = os.ReadFile(p)
			return err
		case len(parts) == 3 && parts[0] == "native":
			if _, _, ok := ota.ParseNativeName(parts[1], parts[2]); ok {
				u.native[parts[1]] = append(u.native[parts[1]], parts[2])
				return nil
			}
		}
		return fmt.Errorf("unexpected file %s", rel)
	})
	if err != nil {
		return nil, err
	}
	return u, nil
}

func uploadDir(parts []string) bool {
	switch len(parts) {
	case 1:
		return parts[0] == "certs" || parts[0] == "assets" || parts[0] == "updates" || parts[0] == "native"
	case 2:
		if parts[0] == "updates" {
			id, ok := ota.NormalizeUUID(parts[1])
			return ok && id == parts[1]
		}
		return parts[0] == "native" && ota.ValidPlatform(parts[1])
	}
	return false
}

func isUpdateFile(name string) bool {
	for _, f := range ota.UpdateFiles {
		if f == name {
			return true
		}
	}
	return false
}

// ingestPlan is what a checked upload installs.
type ingestPlan struct {
	Root          bool     `json:"root"`
	Leaves        []string `json:"leaves"`
	ExpiredLeaves []string `json:"expired_leaves"`
	Assets        []string `json:"assets"`
	Updates       []string `json:"updates"`
	Resigned      []string `json:"resigned"`
	Native        []string `json:"native"`
	Unchanged     int      `json:"unchanged"`

	resign map[string]map[string][]byte // id → .sig/.leaf files to replace
}

func exists(p string) (bool, error) {
	_, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func fileSHA256(p string) ([32]byte, error) {
	var sum [32]byte
	f, err := os.Open(p)
	if err != nil {
		return sum, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return sum, err
	}
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

// checkUpload checks an upload against the store and returns what it would
// install. It changes nothing.
func (a *app) checkUpload(u *upload, now time.Time) (*ingestPlan, error) {
	plan := &ingestPlan{Leaves: []string{}, ExpiredLeaves: []string{}, Assets: []string{}, Updates: []string{},
		Resigned: []string{}, Native: []string{}, resign: map[string]map[string][]byte{}}
	s := a.store

	// The root.
	var root *x509.Certificate
	rootPath := s.Path("certs", ota.RootCertFile)
	if ok, err := exists(rootPath); err != nil {
		return nil, err
	} else if ok {
		stored, storedBytes, err := ota.ReadRootFile(rootPath)
		if err != nil {
			return nil, err
		}
		if u.root != nil && !bytes.Equal(u.root, storedBytes) {
			return nil, errors.New("certs/" + ota.RootCertFile + " differs from the stored root certificate")
		}
		if u.root != nil {
			plan.Unchanged++
		}
		root = stored
	} else if u.root != nil {
		certs, err := ota.ParseCertificatesPEM(u.root)
		if err != nil || len(certs) != 1 {
			return nil, fmt.Errorf("certs/%s is not one certificate (%v)", ota.RootCertFile, err)
		}
		if err := ota.CheckRoot(certs[0], now); err != nil {
			return nil, err
		}
		root, plan.Root = certs[0], true
	}
	if root == nil && (len(u.leaves) > 0 || len(u.updates) > 0) {
		return nil, errors.New("neither the store nor the upload has the root certificate certs/" + ota.RootCertFile)
	}

	// Leaves.
	uploaded := map[string]*x509.Certificate{}
	for _, name := range sortedKeys(u.leaves) {
		b := u.leaves[name]
		certs, err := ota.ParseCertificatesPEM(b)
		if err != nil || len(certs) != 1 {
			return nil, fmt.Errorf("certs/%s is not one certificate (%v)", name, err)
		}
		cert := certs[0]
		if stored, err := os.ReadFile(s.Path("certs", name)); err == nil {
			if !bytes.Equal(stored, b) {
				return nil, fmt.Errorf("certs/%s differs from the stored file of that name", name)
			}
			uploaded[ota.LeafSHA256(cert)] = cert
			plan.Unchanged++
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		if err := ota.CheckLeaf(cert, root, now); err != nil {
			if now.After(cert.NotAfter) {
				plan.ExpiredLeaves = append(plan.ExpiredLeaves, name)
				continue
			}
			return nil, fmt.Errorf("certs/%s: %w", name, err)
		}
		uploaded[ota.LeafSHA256(cert)] = cert
		plan.Leaves = append(plan.Leaves, name)
	}
	findLeaf := func(sha string) (*x509.Certificate, error) {
		if c := uploaded[sha]; c != nil {
			return c, nil
		}
		return s.FindLeaf(sha)
	}

	// Assets.
	for _, name := range sortedKeys(u.assets) {
		if err := ota.CheckAssetFile(filepath.Join(u.dir, "assets", name), name); err != nil {
			return nil, fmt.Errorf("upload: %w", err)
		}
		stored := s.Path("assets", name)
		ok, err := exists(stored)
		if err != nil {
			return nil, err
		}
		if !ok {
			plan.Assets = append(plan.Assets, name)
			continue
		}
		if err := ota.CheckAssetFile(stored, name); err != nil {
			return nil, fmt.Errorf("stored %v; replace it with otactl repair-asset --asset %s", err, name)
		}
		plan.Unchanged++
	}

	// Updates.
	for _, id := range sortedKeys(u.updates) {
		files := u.updates[id]
		v, err := ota.VerifyUpdateFiles(id, files, findLeaf, root, now)
		if err != nil {
			return nil, err
		}
		if v.Created().After(now.Add(maxAhead)) {
			return nil, fmt.Errorf("update %s: createdAt %s is more than %s ahead of this machine's clock (%s)",
				id, v.Manifest.CreatedAt, maxAhead, now.UTC().Format(time.RFC3339))
		}
		for _, name := range v.Manifest.AssetNames() {
			if u.assets[name] {
				continue
			}
			if err := ota.CheckAssetFile(s.Path("assets", name), name); err != nil {
				return nil, fmt.Errorf("update %s needs asset %s, which is neither uploaded nor stored intact: %v", id, name, err)
			}
		}
		dir := s.Path("updates", id)
		ok, err := exists(dir)
		if err != nil {
			return nil, err
		}
		if !ok {
			plan.Updates = append(plan.Updates, id)
			continue
		}
		stored, err := s.ReadUpdateFiles(id)
		if err != nil {
			return nil, fmt.Errorf("update %s is stored but unreadable: %w", id, err)
		}
		changed := map[string][]byte{}
		for _, name := range ota.UpdateFiles {
			if bytes.Equal(stored[name], files[name]) {
				continue
			}
			if strings.HasSuffix(name, ".json") {
				return nil, fmt.Errorf("update %s: %s differs from the published file; published bytes are never replaced", id, name)
			}
			changed[name] = files[name]
		}
		if len(changed) == 0 {
			plan.Unchanged++
			continue
		}
		plan.Resigned = append(plan.Resigned, id)
		plan.resign[id] = changed
	}

	// Native builds.
	for _, platform := range sortedKeys(u.native) {
		for _, name := range u.native[platform] {
			src := filepath.Join(u.dir, "native", platform, name)
			dst := s.Path("native", platform, name)
			ok, err := exists(dst)
			if err != nil {
				return nil, err
			}
			if !ok {
				plan.Native = append(plan.Native, platform+"/"+name)
				continue
			}
			a1, err := fileSHA256(src)
			if err != nil {
				return nil, err
			}
			a2, err := fileSHA256(dst)
			if err != nil {
				return nil, err
			}
			if a1 != a2 {
				return nil, fmt.Errorf("native/%s/%s is already published with other bytes; a rebuilt binary takes the next build number", platform, name)
			}
			plan.Unchanged++
		}
	}
	return plan, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// moveInto renames src to dst, which must not exist, with mode perm.
func moveInto(src, dst string, perm os.FileMode, dirs map[string]bool) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Chmod(src, perm); err != nil {
		return err
	}
	if ok, err := exists(dst); err != nil || ok {
		if err == nil {
			err = fmt.Errorf("%s appeared while ingesting", dst)
		}
		return err
	}
	if err := os.Rename(src, dst); err != nil {
		return err
	}
	dirs[filepath.Dir(dst)] = true
	return nil
}

func syncDirs(dirs map[string]bool) error {
	for d := range dirs {
		f, err := os.Open(d)
		if err != nil {
			return err
		}
		err = f.Sync()
		f.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// install moves a checked upload into the store: certificates first, then
// assets, then updates, then native builds, so nothing is in place before
// what it needs.
func (a *app) install(u *upload, plan *ingestPlan) error {
	s := a.store
	dirs := map[string]bool{}
	if plan.Root {
		if err := moveInto(filepath.Join(u.dir, "certs", ota.RootCertFile), s.Path("certs", ota.RootCertFile), 0o644, dirs); err != nil {
			return err
		}
	}
	for _, name := range plan.Leaves {
		if err := moveInto(filepath.Join(u.dir, "certs", name), s.Path("certs", name), 0o644, dirs); err != nil {
			return err
		}
	}
	for _, name := range plan.Assets {
		if err := moveInto(filepath.Join(u.dir, "assets", name), s.Path("assets", name), 0o644, dirs); err != nil {
			return err
		}
	}
	if err := syncDirs(dirs); err != nil {
		return err
	}
	for _, id := range plan.Updates {
		src := filepath.Join(u.dir, "updates", id)
		for _, name := range ota.UpdateFiles {
			if err := os.Chmod(filepath.Join(src, name), 0o644); err != nil {
				return err
			}
		}
		if err := moveInto(src, s.Path("updates", id), 0o755, dirs); err != nil {
			return err
		}
	}
	for _, id := range plan.Resigned {
		// The .leaf and .sig files are replaced one after the other: for a
		// moment a reader can see a new signature with the old leaf, and that
		// one answer fails on the phone, which checks again later.
		changed := plan.resign[id]
		for _, name := range []string{"manifest.leaf", "manifest.sig", "rollback.leaf", "rollback.sig"} {
			if b, ok := changed[name]; ok {
				if err := ota.WriteFileAtomic(s.Path("updates", id, name), b, 0o644); err != nil {
					return err
				}
			}
		}
	}
	for _, pn := range plan.Native {
		platform, name, _ := strings.Cut(pn, "/")
		if err := moveInto(filepath.Join(u.dir, "native", platform, name), s.Path("native", platform, name), 0o644, dirs); err != nil {
			return err
		}
	}
	return syncDirs(dirs)
}

func setupIngest(fs *flag.FlagSet) func(a *app, args []string) error {
	return func(a *app, args []string) error {
		if len(args) != 1 {
			return usagef("ingest takes one publishId")
		}
		id := args[0]
		if !publishIDRE.MatchString(id) {
			return usagef("publishId %q must match %s", id, publishIDRE)
		}
		dir := a.store.Path("incoming", id)
		u, err := scanUpload(dir)
		if err != nil {
			return fmt.Errorf("incoming/%s: %w", id, err)
		}
		plan, err := a.checkUpload(u, a.now())
		if err != nil {
			return fmt.Errorf("incoming/%s refused, nothing was installed: %w", id, err)
		}
		if err := a.install(u, plan); err != nil {
			return fmt.Errorf("incoming/%s: installing failed part way; run the same ingest again: %w", id, err)
		}
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
		if a.json {
			if err := a.printJSON(plan); err != nil {
				return err
			}
		} else {
			a.printf("ingested %s: root %v, %d leaves, %d assets, %d new updates, %d re-signed updates, %d native builds, %d files already stored\n",
				id, plan.Root, len(plan.Leaves), len(plan.Assets), len(plan.Updates), len(plan.Resigned), len(plan.Native), plan.Unchanged)
			for _, name := range plan.ExpiredLeaves {
				a.printf("not installed: certs/%s has expired\n", name)
			}
		}
		return a.logged("ingest", map[string]any{
			"publish_id": id, "root": plan.Root, "leaves": plan.Leaves, "assets": len(plan.Assets),
			"updates": plan.Updates, "resigned": plan.Resigned, "native": plan.Native,
		})
	}
}
