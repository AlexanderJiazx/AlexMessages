package ota

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"alexmessage/internal/ota/otatest"
	"alexmessage/internal/ota/sign"
)

func TestVerifyUpdateAcceptsAPublishedUpdate(t *testing.T) {
	f := otatest.New(t)
	s := &Store{Dir: f.Dir}
	u := f.AddUpdate(t, PlatformAndroid, testRuntime)
	v, err := s.VerifyUpdate(u.ID, f.Root, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if v.Info.Platform != PlatformAndroid || v.Manifest.RuntimeVersion != testRuntime ||
		!v.Created().Equal(u.CreatedAt) || v.ManifestLeaf.NotAfter != f.Leaf.NotAfter {
		t.Fatalf("verified update: %+v", v)
	}
	if got := v.Manifest.AssetNames(); len(got) != 1 || got[0] != u.AssetName {
		t.Fatalf("asset names %v, want [%s]", got, u.AssetName)
	}
	if err := s.CheckAssets(v.Manifest); err != nil {
		t.Fatal(err)
	}
	f.Write(t, "assets/"+u.AssetName, []byte("changed"))
	if err := s.CheckAssets(v.Manifest); err == nil {
		t.Fatal("a changed asset passed")
	}
}

// resign replaces a body in files and signs it again with the fixture's leaf.
func resign(t *testing.T, f *otatest.Fixture, files map[string][]byte, kind string, body []byte, keyID string) {
	t.Helper()
	sig, err := sign.SignatureHeader(body, f.LeafKey, keyID)
	if err != nil {
		t.Fatal(err)
	}
	files[kind+".json"] = body
	files[kind+".sig"] = []byte(sig + "\n")
}

func TestVerifyUpdateFilesRejects(t *testing.T) {
	f := otatest.New(t)
	now := time.Now()
	s := &Store{Dir: f.Dir}
	editManifest := func(files map[string][]byte, edit func(m map[string]any)) []byte {
		var m map[string]any
		if err := json.Unmarshal(files["manifest.json"], &m); err != nil {
			t.Fatal(err)
		}
		edit(m)
		b, _ := json.Marshal(m)
		return b
	}
	cases := map[string]func(files map[string][]byte, u *otatest.Update){
		"one byte of the manifest flipped": func(files map[string][]byte, u *otatest.Update) {
			b := bytes.Clone(files["manifest.json"])
			b[len(b)-2] ^= 1
			files["manifest.json"] = b
		},
		"one byte of the directive flipped": func(files map[string][]byte, u *otatest.Update) {
			b := bytes.Clone(files["rollback.json"])
			b[10] ^= 1
			files["rollback.json"] = b
		},
		"non-empty metadata, signed": func(files map[string][]byte, u *otatest.Update) {
			resign(t, f, files, KindManifest, editManifest(files, func(m map[string]any) {
				m["metadata"] = map[string]any{"branch": "x"}
			}), RootKeyID)
		},
		"keyid other than root-2026": func(files map[string][]byte, u *otatest.Update) {
			resign(t, f, files, KindManifest, files["manifest.json"], "root")
		},
		"directive commitTime is not createdAt + 1 s": func(files map[string][]byte, u *otatest.Update) {
			resign(t, f, files, KindRollback, NewRollbackDirective(u.CreatedAt.Add(time.Hour)), RootKeyID)
		},
		"manifest id differs from the directory": func(files map[string][]byte, u *otatest.Update) {
			resign(t, f, files, KindManifest, editManifest(files, func(m map[string]any) {
				m["id"] = otatest.NewUUID()
			}), RootKeyID)
		},
		"info.json label differs from extra.am": func(files map[string][]byte, u *otatest.Update) {
			files["info.json"] = bytes.Replace(files["info.json"], []byte(`"label":"20261105.1"`), []byte(`"label":"20261105.2"`), 1)
		},
		"info.json runtime differs": func(files map[string][]byte, u *otatest.Update) {
			files["info.json"] = bytes.Replace(files["info.json"], []byte(testRuntime), []byte("other"), 1)
		},
		"leaf file names an unknown certificate": func(files map[string][]byte, u *otatest.Update) {
			files["rollback.leaf"] = []byte(strings.Repeat("ab", 32) + "\n")
		},
		"a file is missing": func(files map[string][]byte, u *otatest.Update) {
			delete(files, "rollback.sig")
		},
		"a signature with a newline inside": func(files map[string][]byte, u *otatest.Update) {
			files["manifest.sig"] = []byte("sig=\"a\"\nkeyid=\"root-2026\"\n")
		},
	}
	for name, edit := range cases {
		u := f.BuildUpdate(t, PlatformAndroid, testRuntime, otatest.UpdateOptions{})
		files := u.Files()
		edit(files, u)
		if _, err := VerifyUpdateFiles(u.ID, files, s.FindLeaf, f.Root, now); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	u := f.BuildUpdate(t, PlatformAndroid, testRuntime, otatest.UpdateOptions{})
	if _, err := VerifyUpdateFiles(u.ID, u.Files(), s.FindLeaf, f.Root, now); err != nil {
		t.Fatalf("unchanged update: %v", err)
	}
	if _, err := VerifyUpdateFiles(u.ID, u.Files(), s.FindLeaf, f.Root, f.Leaf.NotAfter.Add(time.Minute)); err == nil {
		t.Fatal("accepted after the leaf expired")
	}
}

func TestParseUpdateManifestRejects(t *testing.T) {
	f := otatest.New(t)
	u := f.BuildUpdate(t, PlatformIOS, testRuntime, otatest.UpdateOptions{})
	if _, err := ParseUpdateManifest(u.Manifest); err != nil {
		t.Fatalf("fixture manifest: %v", err)
	}
	hash := strings.TrimSuffix(u.AssetName, ".js")
	for name, edit := range map[string]func(m map[string]any){
		"unknown field":           func(m map[string]any) { m["extensions"] = map[string]any{} },
		"createdAt without ms":    func(m map[string]any) { m["createdAt"] = "2026-11-05T10:00:00Z" },
		"uppercase id":            func(m map[string]any) { m["id"] = strings.ToUpper(m["id"].(string)) },
		"bad runtime":             func(m map[string]any) { m["runtimeVersion"] = "../x" },
		"no assets array":         func(m map[string]any) { delete(m, "assets") },
		"no extra.am":             func(m map[string]any) { m["extra"] = map[string]any{"expoClient": map[string]any{}} },
		"unknown extra member":    func(m map[string]any) { m["extra"].(map[string]any)["eas"] = map[string]any{} },
		"metadata not an object":  func(m map[string]any) { m["metadata"] = []any{} },
		"url with a query":        launchURL("https://messages.alexanderjia.com/ota/assets/" + hash + ".js?x=1"),
		"url with user info":      launchURL("https://u:p@messages.alexanderjia.com/ota/assets/" + hash + ".js"),
		"url with a fragment":     launchURL("https://messages.alexanderjia.com/ota/assets/" + hash + ".js#x"),
		"relative url":            launchURL("/ota/assets/" + hash + ".js"),
		"url outside /ota/assets": launchURL("https://messages.alexanderjia.com/assets/" + hash + ".js"),
		"url hash differs":        launchURL("https://messages.alexanderjia.com/ota/assets/" + strings.Repeat("A", 43) + ".js"),
		"launch asset not .js":    launchURL("https://messages.alexanderjia.com/ota/assets/" + hash + ".png"),
		"file url":                launchURL("file:///ota/assets/" + hash + ".js"),
		"bad key": func(m map[string]any) {
			m["launchAsset"].(map[string]any)["key"] = "XYZ"
		},
		"launch asset type": func(m map[string]any) {
			m["launchAsset"].(map[string]any)["contentType"] = "text/html"
		},
		"asset extension with a dot": func(m map[string]any) {
			m["assets"] = []any{map[string]any{"hash": hash, "key": strings.Repeat("a", 32), "contentType": "image/png",
				"fileExtension": ".png", "url": "https://messages.alexanderjia.com/ota/assets/" + hash + ".p.ng"}}
		},
	} {
		var m map[string]any
		if err := json.Unmarshal(u.Manifest, &m); err != nil {
			t.Fatal(err)
		}
		edit(m)
		b, _ := json.Marshal(m)
		if _, err := ParseUpdateManifest(b); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func launchURL(u string) func(m map[string]any) {
	return func(m map[string]any) { m["launchAsset"].(map[string]any)["url"] = u }
}

func TestRollbackDirectiveRoundTrip(t *testing.T) {
	created := time.Date(2026, 11, 5, 10, 0, 0, 123e6, time.UTC)
	body := NewRollbackDirective(created)
	if string(body) != `{"type":"rollBackToEmbedded","parameters":{"commitTime":"2026-11-05T10:00:01.123Z"}}` {
		t.Fatalf("directive %s", body)
	}
	got, err := ParseRollbackDirective(body)
	if err != nil || !got.Equal(created.Add(time.Second)) {
		t.Fatalf("parsed %v, %v", got, err)
	}
	for _, bad := range []string{
		`{"type":"noUpdateAvailable","parameters":{"commitTime":"2026-11-05T10:00:01.123Z"}}`,
		`{"type":"rollBackToEmbedded","parameters":{"commitTime":"yesterday"}}`,
		`{"type":"rollBackToEmbedded","parameters":{"commitTime":"2026-11-05T10:00:01.123Z"},"x":1}`,
	} {
		if _, err := ParseRollbackDirective([]byte(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestCheckRootAndLeaf(t *testing.T) {
	f := otatest.New(t)
	now := time.Now()
	if err := CheckRoot(f.Root, now); err != nil {
		t.Fatal(err)
	}
	if err := CheckLeaf(f.Leaf, f.Root, now); err != nil {
		t.Fatal(err)
	}
	if err := CheckRoot(f.Leaf, now); err == nil {
		t.Fatal("a leaf passed as root")
	}
	tmpl := rootTemplate(now)
	tmpl.MaxPathLenZero, tmpl.MaxPathLen = false, 1
	wide := makeCert(t, tmpl, &f.RootKey.PublicKey, nil, f.RootKey)
	if err := CheckRoot(wide, now); err == nil {
		t.Fatal("a root with pathlen 1 passed")
	}
	if err := CheckRoot(f.Root, f.Root.NotAfter.Add(time.Hour)); err == nil {
		t.Fatal("an expired root passed")
	}
}

func TestListPointersRuntimesRevoked(t *testing.T) {
	f := otatest.New(t)
	s := &Store{Dir: f.Dir}
	u := f.AddUpdate(t, PlatformIOS, "rt-ios")
	f.Point(t, ChannelProduction, PlatformIOS, "rt-ios", StateUpdate, u.ID)
	f.Point(t, ChannelBeta, PlatformAndroid, "rt-b", StateHalted, "")
	f.Point(t, ChannelBeta, PlatformAndroid, "rt-a", StateRollback, u.ID)
	f.Write(t, "channels/beta/android/.rt-c.json.tmp-1", []byte("{"))
	f.RegisterRuntime(t, PlatformIOS, "rt-ios", otatest.NewUUID(), 2)
	f.RegisterRuntime(t, PlatformAndroid, "rt-a", otatest.NewUUID(), 2)
	f.Revoke(t, strings.ToUpper(u.ID))

	ps, err := s.ListPointers()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range ps {
		got = append(got, p.Channel+"/"+p.Platform+"/"+p.Runtime+"="+p.State)
	}
	want := "beta/android/rt-a=rollback beta/android/rt-b=halted production/ios/rt-ios=update"
	if strings.Join(got, " ") != want {
		t.Fatalf("pointers %v, want %s", got, want)
	}
	rts, err := s.ListRuntimes()
	if err != nil || len(rts) != 2 || rts[0].Platform != PlatformAndroid || rts[1].Runtime != "rt-ios" {
		t.Fatalf("runtimes %+v, %v", rts, err)
	}
	ids, err := s.Revoked()
	if err != nil || len(ids) != 1 || ids[0] != u.ID {
		t.Fatalf("revoked %v, %v", ids, err)
	}
	if updates, err := s.ListUpdates(); err != nil || len(updates) != 1 || updates[0] != u.ID {
		t.Fatalf("updates %v, %v", updates, err)
	}
}

func TestTestUnsignedPointer(t *testing.T) {
	f := otatest.New(t)
	s := &Store{Dir: f.Dir}
	now := time.Now().UTC().Truncate(time.Second)
	embedded := otatest.NewUUID()
	f.RegisterRuntime(t, PlatformAndroid, testRuntime, embedded, 2)
	id := otatest.NewUUID()
	body := []byte(`{"id":"` + id + `"}`)
	f.Write(t, "test/"+id+"/manifest.json", body)
	f.Write(t, "test/"+id+"/info.json", []byte(`{"platform":"android","runtime":"`+testRuntime+`","label":"test-unsigned","created_at":"2026-11-05T10:00:00Z"}`))
	p := Pointer{State: StateTestUnsigned, Update: id, Since: now.Format(time.RFC3339), Until: now.Add(10 * time.Minute).Format(time.RFC3339)}

	if err := s.WritePointer(ChannelProduction, PlatformAndroid, testRuntime, p); err == nil {
		t.Fatal("a production pointer took state test_unsigned")
	}
	if err := (&Pointer{State: StateUpdate, Update: id, Until: p.Until}).Validate(ChannelBeta); err == nil {
		t.Fatal("until accepted on an update pointer")
	}
	if err := s.WritePointer(ChannelBeta, PlatformAndroid, testRuntime, p); err != nil {
		t.Fatal(err)
	}

	beta := phoneHeaders(embedded, embedded)
	beta.Set("Expo-Extra-Params", `am_channel="beta"`)
	sel, err := s.SelectAt(mustRequest(t, beta), now)
	if err != nil || sel.UpdateID != id || len(sel.Parts) != 1 || sel.Parts[0].Name != "manifest" ||
		sel.Parts[0].Signature != "" || !bytes.Equal(sel.Parts[0].Body, body) {
		t.Fatalf("test_unsigned: %+v, %v", sel, err)
	}
	for name, at := range map[string]time.Time{"at until": now.Add(10 * time.Minute), "after until": now.Add(time.Hour)} {
		if sel, err := s.SelectAt(mustRequest(t, beta), at); err != nil || sel.Parts != nil {
			t.Errorf("%s: %+v, %v", name, sel, err)
		}
	}
	// A production phone never sees it.
	if sel, err := s.SelectAt(mustRequest(t, phoneHeaders(embedded, embedded)), now); err != nil || sel.Parts != nil {
		t.Fatalf("production phone: %+v, %v", sel, err)
	}
	if err := s.RemovePointer(ChannelBeta, PlatformAndroid, testRuntime); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.Dir, "channels", "beta", "android", testRuntime+".json")); !os.IsNotExist(err) {
		t.Fatalf("pointer file still there: %v", err)
	}
}
