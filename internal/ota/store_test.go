package ota

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"alexmessage/internal/ota/otatest"
)

func TestWritePointerIsAtomicAndValidated(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	id := otatest.NewUUID()
	p := Pointer{State: StateUpdate, Update: id, Since: "2026-11-05T10:00:00Z"}
	if err := s.WritePointer(ChannelBeta, PlatformIOS, testRuntime, p); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadPointer(ChannelBeta, PlatformIOS, testRuntime)
	if err != nil || *got != p {
		t.Fatalf("read back %+v, %v", got, err)
	}
	if err := s.WritePointer(ChannelBeta, PlatformIOS, testRuntime, Pointer{State: StateHalted, Update: id}); err != nil {
		t.Fatal(err)
	}
	// Only the pointer itself is left in the directory: no temp files.
	entries, _ := os.ReadDir(filepath.Join(s.Dir, "channels", ChannelBeta, PlatformIOS))
	if len(entries) != 1 || entries[0].Name() != testRuntime+".json" {
		t.Fatalf("directory holds %v", entries)
	}

	for name, bad := range map[string]Pointer{
		"unknown state":      {State: "paused", Update: id},
		"update without id":  {State: StateUpdate},
		"uppercase id":       {State: StateRollback, Update: strings.ToUpper(id)},
		"since not RFC 3339": {State: StateHalted, Since: "yesterday"},
	} {
		if err := s.WritePointer(ChannelProduction, PlatformIOS, testRuntime, bad); err == nil {
			t.Errorf("%s: written", name)
		}
	}
	for _, key := range [][3]string{
		{"alpha", PlatformIOS, testRuntime},
		{ChannelBeta, "web", testRuntime},
		{ChannelBeta, PlatformIOS, "../../etc/passwd"},
	} {
		if err := s.WritePointer(key[0], key[1], key[2], p); err == nil {
			t.Errorf("pointer %v written", key)
		}
		if _, err := s.ReadPointer(key[0], key[1], key[2]); err == nil {
			t.Errorf("pointer %v read", key)
		}
	}
}

func TestRuntimesAndRevoked(t *testing.T) {
	f := otatest.New(t)
	s := &Store{Dir: f.Dir}
	embedded := otatest.NewUUID()
	if rt, err := s.ReadRuntime(PlatformAndroid, testRuntime); rt != nil || err != nil {
		t.Fatalf("unregistered runtime: %v, %v", rt, err)
	}
	f.RegisterRuntime(t, PlatformAndroid, testRuntime, embedded, 2)
	if ok, err := s.IsRegisteredEmbedded(PlatformAndroid, testRuntime, embedded); !ok || err != nil {
		t.Fatalf("registered embedded id: %v, %v", ok, err)
	}
	for _, c := range [][3]string{
		{PlatformAndroid, testRuntime, otatest.NewUUID()},
		{PlatformIOS, testRuntime, embedded},
		{PlatformAndroid, "other", embedded},
		{PlatformAndroid, testRuntime, ""},
	} {
		if ok, _ := s.IsRegisteredEmbedded(c[0], c[1], c[2]); ok {
			t.Errorf("%v counted as registered", c)
		}
	}
	// A runtimes file under the wrong name is an error.
	b, _ := os.ReadFile(filepath.Join(f.Dir, "runtimes", PlatformAndroid, testRuntime+".json"))
	f.Write(t, "runtimes/ios/"+testRuntime+".json", b)
	if _, err := s.ReadRuntime(PlatformIOS, testRuntime); err == nil {
		t.Error("an android runtimes file was read as ios")
	}

	id := otatest.NewUUID()
	if r, err := s.IsRevoked(id); r || err != nil {
		t.Fatalf("no revoked.txt: %v, %v", r, err)
	}
	f.Revoke(t, otatest.NewUUID(), strings.ToUpper(id))
	if r, err := s.IsRevoked(id); !r || err != nil {
		t.Fatalf("revoked id: %v, %v", r, err)
	}
	if r, _ := s.IsRevoked("# revoked update ids"); r {
		t.Fatal("a comment counted as an id")
	}
}

func TestAssetAndNativeNames(t *testing.T) {
	s := &Store{Dir: "/srv/ota"}
	good := strings.Repeat("A", 42) + "Q.js" // 43 characters of unpadded base64url = 32 bytes
	if p, ok := s.AssetFile(good); !ok || p != "/srv/ota/assets/"+good {
		t.Fatalf("good asset name: %q %v", p, ok)
	}
	if _, ext, ok := ParseAssetName(good); !ok || ext != "js" || AssetContentType(ext) != "application/javascript" {
		t.Fatalf("asset ext %q", ext)
	}
	for _, bad := range []string{
		"../" + good,
		strings.Repeat("A", 42) + ".js",           // 42 characters
		strings.Repeat("A", 44) + ".js",           // 44 characters
		strings.Repeat("A", 42) + "B.js",          // non-zero padding bits: not canonical
		strings.Repeat("A", 42) + "Q.JS",          // uppercase extension
		strings.Repeat("A", 42) + "Q.toolongexts", // 11 characters
		strings.Repeat("A", 42) + "Q",             // no extension
		strings.Repeat("A", 42) + "Q.j/s",
		strings.Repeat("+", 42) + "Q.js", // standard, not URL-safe, base64
	} {
		if _, ok := s.AssetFile(bad); ok {
			t.Errorf("asset name %q accepted", bad)
		}
	}
	if p, ok := s.NativeFile(PlatformAndroid, "AlexMessages-1.2.0-2.apk"); !ok || p != "/srv/ota/native/android/AlexMessages-1.2.0-2.apk" {
		t.Fatalf("apk: %q %v", p, ok)
	}
	if _, ok := s.NativeFile(PlatformIOS, "AlexMessages-1.2.0-2.ipa"); !ok {
		t.Fatal("ipa refused")
	}
	for _, c := range [][2]string{
		{PlatformAndroid, "AlexMessages-1.2.0-2.ipa"},
		{PlatformIOS, "AlexMessages-1.2.0-2.apk"},
		{PlatformAndroid, "../AlexMessages-1.2.0-2.apk"},
		{PlatformAndroid, "AlexMessages-1.2-2.apk"},
		{"web", "AlexMessages-1.2.0-2.apk"},
	} {
		if _, ok := s.NativeFile(c[0], c[1]); ok {
			t.Errorf("native %v accepted", c)
		}
	}
	if NativeFileName(PlatformIOS, "1.3.0", 3) != "AlexMessages-1.3.0-3.ipa" {
		t.Fatal("NativeFileName")
	}
}

func TestReadPolicy(t *testing.T) {
	f := otatest.New(t)
	s := &Store{Dir: f.Dir}
	p, err := s.ReadPolicy()
	if err != nil || p.Android.MinBuild != 0 || p.Android.Latest != nil || p.IOS.HardGate {
		t.Fatalf("missing policy: %+v, %v", p, err)
	}
	f.Write(t, "native/policy.json", []byte(`{"android":{"min_build":2,"hard_gate":true,"latest":{"version":"1.3.0","build":3,
		"url":"https://messages.alexanderjia.com/app/android/AlexMessages-1.3.0-3.apk","sha256":"`+strings.Repeat("ab", 32)+`","size":51000000,"notes":"Calls"}},
		"ios":{"min_build":0,"latest":null}}`))
	p, err = s.ReadPolicy()
	if err != nil || p.Android.MinBuild != 2 || !p.Android.HardGate || p.Android.Latest.Build != 3 {
		t.Fatalf("policy: %+v, %v", p, err)
	}
	for name, latest := range map[string]string{
		"url of another file": `"version":"1.3.0","build":3,"url":"https://h/app/android/AlexMessages-1.3.0-4.apk","sha256":"` + strings.Repeat("ab", 32) + `","size":1`,
		"url with a query":    `"version":"1.3.0","build":3,"url":"https://h/app/android/AlexMessages-1.3.0-3.apk?x=1","sha256":"` + strings.Repeat("ab", 32) + `","size":1`,
		"javascript url":      `"version":"1.3.0","build":3,"url":"javascript:alert(1)","sha256":"` + strings.Repeat("ab", 32) + `","size":1`,
		"uppercase sha256":    `"version":"1.3.0","build":3,"url":"https://h/app/android/AlexMessages-1.3.0-3.apk","sha256":"` + strings.Repeat("AB", 32) + `","size":1`,
		"zero size":           `"version":"1.3.0","build":3,"url":"https://h/app/android/AlexMessages-1.3.0-3.apk","sha256":"` + strings.Repeat("ab", 32) + `","size":0`,
		"version not N.N.N":   `"version":"1.3","build":3,"url":"https://h/app/android/AlexMessages-1.3-3.apk","sha256":"` + strings.Repeat("ab", 32) + `","size":1`,
	} {
		f.Write(t, "native/policy.json", []byte(`{"android":{"latest":{`+latest+`}}}`))
		if _, err := s.ReadPolicy(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	f.Write(t, "native/policy.json", []byte(`{"ios":{"min_build":-1}}`))
	if _, err := s.ReadPolicy(); err == nil {
		t.Error("negative min_build accepted")
	}
}
