package main

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// readZipMember reads one member of a zip file.
func readZipMember(t *testing.T, path, name string) []byte {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer rc.Close()
			b, err := io.ReadAll(rc)
			if err != nil {
				t.Fatal(err)
			}
			return b
		}
	}
	t.Fatalf("%s has no %s", path, name)
	return nil
}

// fixtureCert is the certificate text in testdata/native/aapt2.apk, written
// by aapt2 from &#xA; escapes as Expo's config plugin writes them.
const fixtureCert = "-----BEGIN CERTIFICATE-----\nMIIBfixture+certificate/bytes==\n-----END CERTIFICATE-----\n"

// TestAAPT2Fixture reads an APK that aapt2 built (UTF-16 manifest strings,
// UTF-8 resource strings), as an expo-updates release build lays out its
// manifest: see testdata/native/README.md.
func TestAAPT2Fixture(t *testing.T) {
	apk := filepath.Join("testdata", "native", "aapt2.apk")
	elems, err := parseAXML(readZipMember(t, apk, "AndroidManifest.xml"))
	if err != nil {
		t.Fatal(err)
	}
	table, err := parseARSC(readZipMember(t, apk, "resources.arsc"))
	if err != nil {
		t.Fatal(err)
	}
	if elems[0].path != "manifest" || elems[0].attr(0, "package").str != appID {
		t.Fatalf("root %+v", elems[0])
	}
	if a := elems[0].attr(attrVersionCode, ""); a.value.typ != typeIntDec || a.value.data != 2 {
		t.Fatalf("versionCode %+v", a)
	}
	if a := elems[0].attr(attrVersionName, ""); a.str != "1.2.0" {
		t.Fatalf("versionName %+v", a)
	}
	meta := map[string]any{}
	for _, e := range elems {
		if e.path == "manifest/application" {
			v, err := table.metaValue(e.attr(0x01010001, ""))
			if err != nil || v != "Alex Messages" {
				t.Fatalf("label %v %v", v, err)
			}
		}
		if e.path == "manifest/application/meta-data" {
			v, err := table.metaValue(e.attr(attrValue, ""))
			if err != nil {
				t.Fatal(err)
			}
			meta[e.attr(attrName, "").str] = v
		}
	}
	want := map[string]any{
		androidKeys.enabled:                         true,
		androidKeys.url:                             manifestURL,
		androidKeys.headers:                         `{"expo-channel-name":"production"}`,
		androidKeys.cert:                            fixtureCert,
		androidKeys.metadata:                        `{"keyid":"root-2026","alg":"rsa-v1_5-sha256"}`,
		androidKeys.chain:                           true,
		androidKeys.runtime:                         fingerprintSentinel,
		metaPrefix + "EXPO_UPDATES_CHECK_ON_LAUNCH": "ALWAYS",
		metaPrefix + "EXPO_UPDATES_LAUNCH_WAIT_MS":  int64(0),
	}
	if !reflect.DeepEqual(meta, want) {
		t.Fatalf("meta-data\n got %#v\nwant %#v", meta, want)
	}

	// The whole check, with the fixture's values. Its embedded commitTime
	// is 2026-10-25T03:46:40Z.
	x := nativeExpect{build: 2, version: "1.2.0", cert: []byte(fixtureCert),
		runtimes: map[string]string{"android": "aapt2fixtureruntime0123456789abcdef01234"}, now: time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)}
	art, err := checkAPK(apk, x)
	if err != nil {
		t.Fatal(err)
	}
	if art.Runtime != x.runtimes["android"] || art.EmbeddedUpdateID != "0f7cc3a4-8b5a-4c84-9a3e-6a2f0d6f1c11" || art.CommitTime != "2026-10-25T03:46:40.000Z" {
		t.Fatalf("%+v", art)
	}
	// A clock 3 minutes behind the embedded commitTime fails check 4.
	x.now = time.Date(2026, 10, 25, 3, 43, 0, 0, time.UTC)
	if _, err := checkAPK(apk, x); err == nil || !strings.Contains(err.Error(), "ahead of this Mac's clock") {
		t.Fatalf("got %v", err)
	}
}

// TestSampleBuilds reads the real builds this Mac has (build 1, made before
// expo-updates), when they are there: Gradle's resource table and Xcode's
// binary plists.
func TestSampleBuilds(t *testing.T) {
	apk := filepath.Join("..", "..", "mobile", "dist", "AlexMessages-1.0.0-unsigned.apk")
	if _, err := os.Stat(apk); err != nil {
		t.Skip("no sample APK in mobile/dist")
	}
	elems, err := parseAXML(readZipMember(t, apk, "AndroidManifest.xml"))
	if err != nil {
		t.Fatal(err)
	}
	table, err := parseARSC(readZipMember(t, apk, "resources.arsc"))
	if err != nil {
		t.Fatal(err)
	}
	if elems[0].attr(0, "package").str != appID || elems[0].attr(attrVersionCode, "").value.data != 1 {
		t.Fatalf("%+v", elems[0])
	}
	found := false
	for _, e := range elems {
		if e.path == "manifest/application" {
			v, err := table.metaValue(e.attr(0x01010001, ""))
			if err != nil || v != "Alex Messages" {
				t.Fatalf("label %v %v", v, err)
			}
		}
		if e.path == "manifest/application/meta-data" && e.attr(attrName, "").str == androidKeys.enabled {
			v, err := table.metaValue(e.attr(attrValue, ""))
			found = err == nil && v == false
		}
	}
	if !found {
		t.Fatal("expo.modules.updates.ENABLED=false not found")
	}
	// Build 1 fails every expo-updates check, and the checks say why.
	_, err = checkAPK(apk, nativeExpect{build: 1, version: "1.0.0", cert: []byte(fixtureCert), now: time.Now()})
	mustContain(t, errString(err), "ENABLED is false: updates must be enabled", "has no assets/app.manifest",
		"EXPO_UPDATE_URL is absent", "CODE_SIGNING_CERTIFICATE is not byte-equal")

	ipa := filepath.Join("..", "..", "mobile", "build", "AlexMessages-previous.ipa")
	if _, err := os.Stat(ipa); err != nil {
		t.Skip("no sample IPA in mobile/build")
	}
	for _, name := range []string{"Info.plist", "Expo.plist"} {
		b := readZipMember(t, ipa, iosApp+name)
		if !bytes.HasPrefix(b, []byte("bplist00")) {
			t.Fatalf("%s is not a binary plist", name)
		}
		m, err := plistDict(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		switch name {
		case "Info.plist":
			if m["CFBundleIdentifier"] != appID || m["CFBundleShortVersionString"] != "1.0.0" || m["CFBundleVersion"] != "1" {
				t.Fatalf("Info.plist %v", m)
			}
		case "Expo.plist":
			if m["EXUpdatesEnabled"] != false || m["EXUpdatesCheckOnLaunch"] != "ALWAYS" || m["EXUpdatesLaunchWaitMs"] != int64(0) {
				t.Fatalf("Expo.plist %v", m)
			}
		}
	}
	_, err = checkIPA(ipa, nativeExpect{build: 1, version: "1.0.0", cert: []byte(fixtureCert), now: time.Now()})
	mustContain(t, errString(err), "EXUpdatesEnabled is false", "has no "+iosApp+"EXUpdates.bundle/app.manifest")
}

func errString(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

// TestPlistFixtures reads one dictionary written by plutil as XML and as a
// binary plist (testdata/native/fixture.plist and fixture.bplist).
func TestPlistFixtures(t *testing.T) {
	want := map[string]any{
		"EXUpdatesEnabled": true,
		"EXUpdatesConfigCodeSigningAllowUnsignedManifests": false,
		"EXUpdatesURL":                    manifestURL,
		"EXUpdatesRequestHeaders":         map[string]any{"expo-channel-name": "production"},
		"EXUpdatesCodeSigningCertificate": fixtureCert,
		"Unicode":                         "万能签 ✓ café",
		"Small":                           int64(7),
		"Big":                             int64(8589934592),
		"Negative":                        int64(-42),
		"Real":                            2.5,
		"Date":                            time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC),
		"Data":                            []byte{0, 1, 2, 0xff},
		"Array":                           []any{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", int64(1)},
	}
	for _, name := range []string{"fixture.plist", "fixture.bplist"} {
		b, err := os.ReadFile(filepath.Join("testdata", "native", name))
		if err != nil {
			t.Fatal(err)
		}
		got, err := plistDict(bytes.NewReader(b))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if d, ok := got["Date"].(time.Time); ok {
			got["Date"] = d.UTC()
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s\n got %#v\nwant %#v", name, got, want)
		}
	}
	for _, bad := range []string{"", "bplist00", "<plist><dict><key>a</key></dict></plist>", "<plist><array><key>a</key></array></plist>", "<x/>"} {
		if _, err := parsePlist([]byte(bad)); err == nil {
			t.Errorf("parsePlist(%q) succeeded", bad)
		}
	}
}

// TestCheckArtifacts builds a correct APK and IPA with the test encoders,
// then breaks one setting at a time.
func TestCheckArtifacts(t *testing.T) {
	dir := t.TempDir()
	cert := []byte(fixtureCert)
	s := newBuildSpec(cert, 2)
	apk, ipa := filepath.Join(dir, "ok.apk"), filepath.Join(dir, "ok.ipa")
	s.writeAPK(t, apk)
	s.writeIPA(t, ipa)
	for path, f := range map[string]func(string, nativeExpect) (*artifact, error){apk: checkAPK, ipa: checkIPA} {
		art, err := f(path, s.expect())
		if err != nil {
			t.Fatal(err)
		}
		if art.Runtime != s.runtimes[art.Platform] || art.EmbeddedUpdateID != s.embedded[art.Platform] ||
			art.CommitTime != formatMillis(s.commit) || len(art.SHA256) != 64 || art.Size == 0 {
			t.Fatalf("%+v", art)
		}
	}

	cases := []struct {
		name     string
		edit     func(s *buildSpec)
		expect   func(x *nativeExpect)
		apk, ipa string // a part of the error; "" means the check passes
	}{
		{name: "updates disabled", edit: func(s *buildSpec) {
			s.android = map[string]any{androidKeys.enabled: false}
			s.ios = map[string]any{iosKeys.enabled: false}
		}, apk: "ENABLED is false", ipa: "EXUpdatesEnabled is false"},
		{name: "enabled as a string", edit: func(s *buildSpec) {
			s.android = map[string]any{androidKeys.enabled: "true"}
			s.ios = map[string]any{iosKeys.enabled: "true"}
		}, apk: `ENABLED is "true": updates must be enabled`, ipa: `EXUpdatesEnabled is "true"`},
		{name: "enabled absent", edit: func(s *buildSpec) {
			s.android = map[string]any{androidKeys.enabled: nil}
			s.ios = map[string]any{iosKeys.enabled: nil}
		}},
		{name: "test URL", edit: func(s *buildSpec) {
			s.android = map[string]any{androidKeys.url: "http://10.0.2.2:8799/api/ota/manifest"}
			s.ios = map[string]any{iosKeys.url: "http://localhost:8799/api/ota/manifest"}
		}, apk: `EXPO_UPDATE_URL is "http://10.0.2.2:8799`, ipa: `EXUpdatesURL is "http://localhost:8799`},
		{name: "headers", edit: func(s *buildSpec) {
			s.android = map[string]any{androidKeys.headers: `{"expo-channel-name":"beta"}`}
			s.ios = map[string]any{iosKeys.headers: map[string]any{"expo-channel-name": "production", "x": "y"}}
		}, apk: "REQUEST_HEADERS_KEY is", ipa: "EXUpdatesRequestHeaders is"},
		{name: "certificate", edit: func(s *buildSpec) {
			s.android = map[string]any{androidKeys.cert: strings.ReplaceAll(fixtureCert, "\n", " ")}
			s.ios = map[string]any{iosKeys.cert: nil}
		}, apk: "CODE_SIGNING_CERTIFICATE is not byte-equal", ipa: "EXUpdatesCodeSigningCertificate is not byte-equal to mobile/certs/ota-root-2026.pem (it is absent)"},
		{name: "metadata", edit: func(s *buildSpec) {
			s.android = map[string]any{androidKeys.metadata: `{"keyid":"main","alg":"rsa-v1_5-sha256"}`}
			s.ios = map[string]any{iosKeys.metadata: map[string]any{"keyid": "root-2026", "alg": "ecdsa"}}
		}, apk: "CODE_SIGNING_METADATA is", ipa: "EXUpdatesCodeSigningMetadata is"},
		{name: "no chain", edit: func(s *buildSpec) {
			s.android = map[string]any{androidKeys.chain: nil}
			s.ios = map[string]any{iosKeys.chain: false}
		}, apk: "CERTIFICATE_CHAIN is absent, not true", ipa: "CertificateChain is false, not true"},
		{name: "unsigned allowed", edit: func(s *buildSpec) {
			s.android = map[string]any{androidKeys.allowUnsigned: true}
			s.ios = map[string]any{iosKeys.allowUnsigned: true}
		}, apk: "ALLOW_UNSIGNED_MANIFESTS is true", ipa: "AllowUnsignedManifests is true"},
		{name: "unsigned explicitly off", edit: func(s *buildSpec) {
			s.android = map[string]any{androidKeys.allowUnsigned: false}
			s.ios = map[string]any{iosKeys.allowUnsigned: false}
		}},
		{name: "other package", edit: func(s *buildSpec) { s.pkg = appID + ".drill" },
			apk: `the package is "com.alexanderjia.messages.drill"`, ipa: `CFBundleIdentifier is "com.alexanderjia.messages.drill"`},
		{name: "version", expect: func(x *nativeExpect) { x.version = "1.2.1" },
			apk: `versionName is "1.2.0", not "1.2.1"`, ipa: `CFBundleShortVersionString is "1.2.0", not "1.2.1"`},
		{name: "build", expect: func(x *nativeExpect) { x.build = 3 },
			apk: "versionCode is 2, not 3", ipa: `CFBundleVersion is "2", not "3"`},
		{name: "runtime differs", expect: func(x *nativeExpect) {
			x.runtimes = map[string]string{"android": "resolved-android", "ios": "resolved-ios"}
		}, apk: "is fixture-android-runtime-2, but runtimeversion:resolve printed resolved-android",
			ipa: "is fixture-ios-runtime-2, but runtimeversion:resolve printed resolved-ios"},
		{name: "literal runtime", edit: func(s *buildSpec) { s.runtimeAt = map[string]string{"android": "1.2.0", "ios": "string:abc"} },
			apk: "the runtime built into the binary is 1.2.0", ipa: `the runtime "string:abc"`},
		{name: "missing files", edit: func(s *buildSpec) {
			s.omit = []string{"assets/index.android.bundle", "assets/fingerprint", iosApp + "main.jsbundle", iosApp + "EXUpdates.bundle/app.manifest"}
		}, apk: "has no assets/index.android.bundle", ipa: "has no " + iosApp + "main.jsbundle"},
		{name: "future commit", edit: func(s *buildSpec) { s.commit = time.Now().Add(10 * time.Minute) },
			apk: "ahead of this Mac's clock", ipa: "ahead of this Mac's clock"},
	}
	for _, tc := range cases {
		s := newBuildSpec(cert, 2)
		if tc.edit != nil {
			tc.edit(s)
		}
		x := s.expect()
		if tc.expect != nil {
			tc.expect(&x)
		}
		apk, ipa := filepath.Join(dir, "c.apk"), filepath.Join(dir, "c.ipa")
		s.writeAPK(t, apk)
		s.writeIPA(t, ipa)
		_, errA := checkAPK(apk, x)
		_, errI := checkIPA(ipa, x)
		for _, r := range []struct {
			got  error
			want string
		}{{errA, tc.apk}, {errI, tc.ipa}} {
			if r.want == "" && r.got != nil {
				t.Errorf("%s: %v", tc.name, r.got)
			}
			if r.want != "" && (r.got == nil || !strings.Contains(r.got.Error(), r.want)) {
				t.Errorf("%s: want %q, got %v", tc.name, r.want, r.got)
			}
		}
	}
}

func formatMillis(t time.Time) string {
	return time.UnixMilli(t.UnixMilli()).UTC().Format("2006-01-02T15:04:05.000Z")
}

// TestAPKSigned tells the Gradle output (unsigned) from an APK that
// apksigner signed (testdata/native/aapt2-signed.apk: v2 and v3, a
// throwaway key) and from a JAR-signed one.
func TestAPKSigned(t *testing.T) {
	for name, want := range map[string]bool{"aapt2.apk": false, "aapt2-signed.apk": true} {
		got, err := apkSigned(filepath.Join("testdata", "native", name))
		if err != nil || got != want {
			t.Errorf("%s: %v %v", name, got, err)
		}
	}
	jar := filepath.Join(t.TempDir(), "jar.apk")
	writeZip(t, jar, map[string][]byte{"AndroidManifest.xml": {1}, "META-INF/CERT.RSA": {2}})
	if got, err := apkSigned(jar); err != nil || !got {
		t.Errorf("JAR-signed: %v %v", got, err)
	}
}
