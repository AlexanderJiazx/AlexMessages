package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"alexmessage/internal/ota"
)

// The artifact checks of D10 ("Checks on every native artifact"). They run
// on the APK and the IPA before anything is uploaded or registered, and
// again before an --upload-only upload. They read the settings the way
// expo-updates 57.0.26 reads them on the phone: a boolean must be a real
// boolean (Android's Bundle.getBoolean and the iOS plist reader both treat
// the string "true" as absent), and a runtime of "file:fingerprint" means the
// fingerprint file inside the binary.

const (
	appID = "com.alexanderjia.messages"
	// iosApp is the app inside an IPA (D10, check 3).
	iosApp = "Payload/AlexMessages.app/"
	// fingerprintSentinel is the runtime value that makes expo-updates read
	// the fingerprint file built into the binary (the fingerprint policy).
	fingerprintSentinel = "file:fingerprint"
	// maxEmbeddedAhead is how far an embedded update's commitTime may be in
	// the future (D10, check 4).
	maxEmbeddedAhead = 2 * time.Minute
	metaPrefix       = "expo.modules.updates."
)

// manifestURL is the update URL baked into every binary (D2).
const manifestURL = publicURL + "/api/ota/manifest"

// nativeExpect is what both artifacts of build N must contain.
type nativeExpect struct {
	build   int
	version string // 1.N.0
	cert    []byte // mobile/certs/ota-root-2026.pem, byte for byte
	// runtimes are the runtimes that runtimeversion:resolve printed for the
	// commit (or that build.json recorded); the fingerprint file in each
	// binary must equal them.
	runtimes map[string]string
	now      time.Time
}

// artifact is one checked native build file, as build.json records it.
type artifact struct {
	Platform         string `json:"platform"`
	File             string `json:"file"`
	SHA256           string `json:"sha256"`
	Size             int64  `json:"size"`
	Runtime          string `json:"runtime"`
	EmbeddedUpdateID string `json:"embedded_update_id"`
	CommitTime       string `json:"commit_time"` // the embedded app.manifest's commitTime
	Signer           string `json:"signer_sha256,omitempty"`
}

// checkFailed lists every check an artifact failed.
type checkFailed struct {
	file     string
	problems []string
}

func (c *checkFailed) Error() string {
	return fmt.Sprintf("%s failed %d artifact check(s) (plan D10):\n  - %s", c.file, len(c.problems), strings.Join(c.problems, "\n  - "))
}

func (c *checkFailed) addf(format string, args ...any) {
	c.problems = append(c.problems, fmt.Sprintf(format, args...))
}

func (c *checkFailed) err() error {
	if len(c.problems) == 0 {
		return nil
	}
	return c
}

// zipFiles reads the named members of a zip file; a missing member is
// absent from the result.
func zipFiles(r *zip.Reader, names ...string) (map[string][]byte, error) {
	const limit = 16 << 20
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	out := map[string][]byte{}
	for _, f := range r.File {
		if !want[f.Name] {
			continue
		}
		if _, dup := out[f.Name]; dup {
			return nil, fmt.Errorf("%s appears twice in the archive", f.Name)
		}
		if f.UncompressedSize64 > limit {
			return nil, fmt.Errorf("%s is larger than %d bytes", f.Name, limit)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Name, err)
		}
		b, err := io.ReadAll(io.LimitReader(rc, limit))
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Name, err)
		}
		out[f.Name] = b
	}
	return out, nil
}

// zipHas reports whether a zip file has a member.
func zipHas(r *zip.Reader, name string) bool {
	for _, f := range r.File {
		if f.Name == name {
			return true
		}
	}
	return false
}

func fileSHA256(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// embeddedManifest checks the app.manifest that expo-updates builds into
// the binary: its id is the embedded update id that otactl registers, and
// its commitTime may not be more than 2 minutes in the future.
func embeddedManifest(c *checkFailed, b []byte, now time.Time) (string, time.Time) {
	var m struct {
		ID         string      `json:"id"`
		CommitTime json.Number `json:"commitTime"`
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		c.addf("app.manifest is not JSON: %v", err)
		return "", time.Time{}
	}
	id, ok := ota.NormalizeUUID(m.ID)
	if !ok {
		c.addf("app.manifest has the id %q, which is not a UUID", m.ID)
	}
	ms, err := m.CommitTime.Int64()
	if err != nil || ms <= 0 {
		c.addf("app.manifest has the commitTime %q, which is not a time in milliseconds", m.CommitTime)
		return id, time.Time{}
	}
	t := time.UnixMilli(ms).UTC()
	if t.After(now.Add(maxEmbeddedAhead)) {
		c.addf("app.manifest's commitTime %s is more than %s ahead of this Mac's clock (%s)", ota.FormatTime(t), maxEmbeddedAhead, ota.FormatTime(now.UTC()))
	}
	return id, t
}

// runtimeOf is the runtime expo-updates uses: the fingerprint file when the
// configured value is the fingerprint sentinel, else the value itself.
func runtimeOf(c *checkFailed, platform, key string, value any, fingerprint []byte, haveFile bool, fileName string, want string) string {
	s, ok := value.(string)
	if !ok {
		c.addf("%s is %s, not a string", key, describeValue(value))
		return ""
	}
	if platform == ota.PlatformAndroid {
		s = strings.TrimPrefix(s, "string:")
	}
	rt := s
	if s == fingerprintSentinel {
		if !haveFile {
			c.addf("the runtime is %q, and %s is missing", fingerprintSentinel, fileName)
			return ""
		}
		rt = string(fingerprint)
	}
	if !ota.ValidRuntime(rt) {
		c.addf("the runtime %q (from %s) is not a valid runtime version", rt, fileName)
		return ""
	}
	if want != "" && rt != want {
		c.addf("the runtime built into the binary is %s, but runtimeversion:resolve printed %s for this commit (plan D3 item 1)", rt, want)
	}
	return rt
}

func describeValue(v any) string {
	if v == nil {
		return "absent"
	}
	switch t := v.(type) {
	case string:
		return strconv.Quote(t)
	case bool:
		return strconv.FormatBool(t)
	case int64:
		return fmt.Sprintf("the number %d", t)
	}
	return fmt.Sprintf("%v (%T)", v, v)
}

// checkUpdatesConfig is check 1 and 2 of D10 for the settings both
// platforms share. get returns a setting, or nil when it is absent; headers
// and metadata are a JSON string on Android and a dictionary on iOS.
func checkUpdatesConfig(c *checkFailed, x nativeExpect, keys updatesKeys, get func(string) any) {
	if v := get(keys.enabled); v != nil && v != true {
		c.addf("%s is %s: updates must be enabled (true or absent)", keys.enabled, describeValue(v))
	}
	if v := get(keys.url); v != manifestURL {
		c.addf("%s is %s, not %q", keys.url, describeValue(v), manifestURL)
	}
	headers, err := stringMap(get(keys.headers))
	if err != nil || !reflect.DeepEqual(headers, map[string]string{"expo-channel-name": ota.ChannelProduction}) {
		c.addf("%s is %s, not {\"expo-channel-name\": \"production\"} (plan D5)", keys.headers, describeValue(get(keys.headers)))
	}
	if v, ok := get(keys.cert).(string); !ok || v != string(x.cert) {
		c.addf("%s is not byte-equal to mobile/certs/%s%s", keys.cert, ota.RootCertFile, certHint(get(keys.cert), x.cert))
	}
	md, err := stringMap(get(keys.metadata))
	if err != nil || md["keyid"] != ota.RootKeyID || (md["alg"] != "" && md["alg"] != ota.SignatureAlg) {
		c.addf("%s is %s: keyid must be %s and alg %s", keys.metadata, describeValue(get(keys.metadata)), ota.RootKeyID, ota.SignatureAlg)
	}
	if v := get(keys.chain); v != true {
		c.addf("%s is %s, not true (plan D4: chain mode)", keys.chain, describeValue(v))
	}
	if v := get(keys.allowUnsigned); v != nil && v != false {
		c.addf("%s is %s: it must be absent or false", keys.allowUnsigned, describeValue(v))
	}
}

// certHint says whether a certificate that is not byte-equal is at least
// the same certificate, which points at a newline or encoding change.
func certHint(v any, want []byte) string {
	s, ok := v.(string)
	if !ok {
		return " (it is " + describeValue(v) + ")"
	}
	got, err1 := ota.ParseCertificatesPEM([]byte(s))
	exp, err2 := ota.ParseCertificatesPEM(want)
	if err1 == nil && err2 == nil && len(got) == 1 && len(exp) == 1 && bytes.Equal(got[0].Raw, exp[0].Raw) {
		return " (it is the same certificate with other bytes around it)"
	}
	return " (it is another certificate, or not a certificate)"
}

// stringMap reads a map of strings from a JSON object string (Android) or a
// plist dictionary (iOS).
func stringMap(v any) (map[string]string, error) {
	switch t := v.(type) {
	case string:
		var m map[string]string
		if err := json.Unmarshal([]byte(t), &m); err != nil {
			return nil, err
		}
		return m, nil
	case map[string]any:
		m := map[string]string{}
		for k, e := range t {
			s, ok := e.(string)
			if !ok {
				return nil, fmt.Errorf("%s is not a string", k)
			}
			m[k] = s
		}
		return m, nil
	}
	return nil, errors.New("not a map")
}

// updatesKeys are the names of the expo-updates settings on one platform.
type updatesKeys struct {
	enabled, url, headers, cert, metadata, chain, allowUnsigned, runtime string
}

var androidKeys = updatesKeys{
	enabled:       metaPrefix + "ENABLED",
	url:           metaPrefix + "EXPO_UPDATE_URL",
	headers:       metaPrefix + "UPDATES_CONFIGURATION_REQUEST_HEADERS_KEY",
	cert:          metaPrefix + "CODE_SIGNING_CERTIFICATE",
	metadata:      metaPrefix + "CODE_SIGNING_METADATA",
	chain:         metaPrefix + "CODE_SIGNING_INCLUDE_MANIFEST_RESPONSE_CERTIFICATE_CHAIN",
	allowUnsigned: metaPrefix + "CODE_SIGNING_ALLOW_UNSIGNED_MANIFESTS",
	runtime:       metaPrefix + "EXPO_RUNTIME_VERSION",
}

var iosKeys = updatesKeys{
	enabled:       "EXUpdatesEnabled",
	url:           "EXUpdatesURL",
	headers:       "EXUpdatesRequestHeaders",
	cert:          "EXUpdatesCodeSigningCertificate",
	metadata:      "EXUpdatesCodeSigningMetadata",
	chain:         "EXUpdatesCodeSigningIncludeManifestResponseCertificateChain",
	allowUnsigned: "EXUpdatesConfigCodeSigningAllowUnsignedManifests",
	runtime:       "EXUpdatesRuntimeVersion",
}

// checkAPK runs the checks of D10 on an APK.
func checkAPK(path string, x nativeExpect) (*artifact, error) {
	c := &checkFailed{file: filepath.Base(path)}
	art := &artifact{Platform: ota.PlatformAndroid, File: filepath.Base(path)}
	var err error
	if art.SHA256, art.Size, err = fileSHA256(path); err != nil {
		return nil, err
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	defer zr.Close()
	const (
		manifest = "AndroidManifest.xml"
		arsc     = "resources.arsc"
		bundle   = "assets/index.android.bundle"
		embedded = "assets/app.manifest"
		fp       = "assets/fingerprint"
	)
	files, err := zipFiles(&zr.Reader, manifest, arsc, embedded, fp)
	if err != nil {
		return nil, err
	}
	for _, n := range []string{manifest, arsc} {
		if _, ok := files[n]; !ok {
			return nil, fmt.Errorf("%s has no %s: it is not an APK", path, n)
		}
	}
	for _, n := range []string{bundle, embedded} {
		if !zipHas(&zr.Reader, n) {
			c.addf("the APK has no %s (plan D10, check 3)", n)
		}
	}
	elems, err := parseAXML(files[manifest])
	if err != nil {
		return nil, fmt.Errorf("%s: %s: %w", path, manifest, err)
	}
	table, err := parseARSC(files[arsc])
	if err != nil {
		return nil, fmt.Errorf("%s: %s: %w", path, arsc, err)
	}
	var root *axmlElement
	meta := map[string]any{}
	for i := range elems {
		e := &elems[i]
		switch e.path {
		case "manifest":
			root = e
		case "manifest/application/meta-data":
			name := e.attr(attrName, "")
			value := e.attr(attrValue, "")
			if name == nil || value == nil || name.value.typ != typeString {
				continue
			}
			if _, dup := meta[name.str]; dup {
				c.addf("the meta-data %s appears twice", name.str)
			}
			v, err := table.metaValue(value)
			if err != nil {
				c.addf("the meta-data %s: %v", name.str, err)
				continue
			}
			meta[name.str] = v
		}
	}
	if root == nil {
		return nil, fmt.Errorf("%s: the manifest has no <manifest> element", path)
	}
	if a := root.attr(0, "package"); a == nil || a.str != appID {
		c.addf("the package is %s, not %s", attrText(a), appID)
	}
	if a := root.attr(attrVersionCode, ""); a == nil || (a.value.typ != typeIntDec && a.value.typ != typeIntHex) || int64(int32(a.value.data)) != int64(x.build) {
		c.addf("android:versionCode is %s, not %d", attrText(a), x.build)
	}
	if a := root.attr(attrVersionName, ""); a == nil || a.str != x.version {
		c.addf("android:versionName is %s, not %q", attrText(a), x.version)
	}
	get := func(k string) any { return meta[k] }
	checkUpdatesConfig(c, x, androidKeys, get)
	fpBytes, haveFP := files[fp]
	art.Runtime = runtimeOf(c, art.Platform, androidKeys.runtime, get(androidKeys.runtime), fpBytes, haveFP, fp, x.runtimes[art.Platform])
	if b := files[embedded]; b != nil {
		id, t := embeddedManifest(c, b, x.now)
		art.EmbeddedUpdateID, art.CommitTime = id, ota.FormatTime(t)
	}
	return art, c.err()
}

// attrText describes an attribute value for an error message.
func attrText(a *axmlAttr) string {
	switch {
	case a == nil:
		return "absent"
	case a.value.typ == typeString:
		return strconv.Quote(a.str)
	case a.value.typ == typeIntDec || a.value.typ == typeIntHex:
		return strconv.FormatInt(int64(int32(a.value.data)), 10)
	}
	return fmt.Sprintf("a value of type 0x%02x", a.value.typ)
}

// checkIPA runs the checks of D10 on an IPA.
func checkIPA(path string, x nativeExpect) (*artifact, error) {
	c := &checkFailed{file: filepath.Base(path)}
	art := &artifact{Platform: ota.PlatformIOS, File: filepath.Base(path)}
	var err error
	if art.SHA256, art.Size, err = fileSHA256(path); err != nil {
		return nil, err
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	defer zr.Close()
	const (
		info     = iosApp + "Info.plist"
		expo     = iosApp + "Expo.plist"
		bundle   = iosApp + "main.jsbundle"
		embedded = iosApp + "EXUpdates.bundle/app.manifest"
		fp       = iosApp + "EXUpdates.bundle/fingerprint"
	)
	files, err := zipFiles(&zr.Reader, info, expo, embedded, fp)
	if err != nil {
		return nil, err
	}
	for _, n := range []string{info, expo, bundle, embedded} {
		if !zipHas(&zr.Reader, n) {
			c.addf("the IPA has no %s (plan D10, check 3)", n)
		}
	}
	plist := func(name string) map[string]any {
		b, ok := files[name]
		if !ok {
			return map[string]any{}
		}
		m, err := plistDict(bytes.NewReader(b))
		if err != nil {
			c.addf("%s: %v", name, err)
			return map[string]any{}
		}
		return m
	}
	in := plist(info)
	if v := in["CFBundleIdentifier"]; v != appID {
		c.addf("CFBundleIdentifier is %s, not %s", describeValue(v), appID)
	}
	if v := in["CFBundleShortVersionString"]; v != x.version {
		c.addf("CFBundleShortVersionString is %s, not %q", describeValue(v), x.version)
	}
	if v := in["CFBundleVersion"]; v != strconv.Itoa(x.build) {
		c.addf("CFBundleVersion is %s, not \"%d\"", describeValue(v), x.build)
	}
	ex := plist(expo)
	get := func(k string) any { return ex[k] }
	checkUpdatesConfig(c, x, iosKeys, get)
	fpBytes, haveFP := files[fp]
	art.Runtime = runtimeOf(c, art.Platform, iosKeys.runtime, get(iosKeys.runtime), fpBytes, haveFP, fp, x.runtimes[art.Platform])
	if b := files[embedded]; b != nil {
		id, t := embeddedManifest(c, b, x.now)
		art.EmbeddedUpdateID, art.CommitTime = id, ota.FormatTime(t)
	}
	return art, c.err()
}

// apkSigned reports whether an APK carries a signature: a JAR signature
// (META-INF/*.SF, *.RSA, *.DSA, *.EC) or an APK Signing Block, which sits
// right before the central directory.
func apkSigned(path string) (bool, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return false, err
	}
	defer zr.Close()
	for _, f := range zr.File {
		up := strings.ToUpper(f.Name)
		if strings.HasPrefix(up, "META-INF/") {
			switch filepath.Ext(up) {
			case ".SF", ".RSA", ".DSA", ".EC":
				return true, nil
			}
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	// The end of central directory record: its signature, and the offset of
	// the central directory 16 bytes after it.
	for i := len(b) - 22; i >= 0 && i >= len(b)-22-0xffff; i-- {
		if binary.LittleEndian.Uint32(b[i:]) != 0x06054b50 {
			continue
		}
		cd := int(binary.LittleEndian.Uint32(b[i+16:]))
		return cd >= 16 && cd <= len(b) && string(b[cd-16:cd]) == "APK Sig Block 42", nil
	}
	return false, errors.New(path + ": no end of central directory record")
}
