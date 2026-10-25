package main

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

// Encoders for test artifacts: Android binary XML, a resource table, XML
// property lists, and the APK and IPA files around them. The parsers are
// also checked against files that aapt2, apksigner and plutil made
// (testdata/native) and, when this Mac has them, against real builds.

const androidNS = "http://schemas.android.com/apk/res/android"

// xmlAttrSpec is one attribute to encode. id 0 means no namespace.
type xmlAttrSpec struct {
	name  string
	id    uint32
	value any // string, bool, int, or resRef
}

// resRef is a reference to a resource id (@0x7f……).
type resRef uint32

type xmlElemSpec struct {
	name  string
	attrs []xmlAttrSpec
	kids  []xmlElemSpec
}

// stringPool encodes a ResStringPool chunk.
func stringPool(strs []string, utf8 bool) []byte {
	var data []byte
	offsets := make([]uint32, len(strs))
	for i, s := range strs {
		offsets[i] = uint32(len(data))
		if utf8 {
			data = appendLen8(data, len([]rune(s)))
			data = appendLen8(data, len(s))
			data = append(append(data, s...), 0)
		} else {
			units := utf16.Encode([]rune(s))
			if len(units) > 0x7fff {
				data = binary.LittleEndian.AppendUint16(data, uint16(len(units)>>16)|0x8000)
			}
			data = binary.LittleEndian.AppendUint16(data, uint16(len(units)))
			for _, u := range units {
				data = binary.LittleEndian.AppendUint16(data, u)
			}
			data = append(data, 0, 0)
		}
	}
	for len(data)%4 != 0 {
		data = append(data, 0)
	}
	hdr := 28
	start := hdr + 4*len(strs)
	b := make([]byte, start, start+len(data))
	binary.LittleEndian.PutUint16(b[0:], resStringPoolType)
	binary.LittleEndian.PutUint16(b[2:], uint16(hdr))
	binary.LittleEndian.PutUint32(b[4:], uint32(start+len(data)))
	binary.LittleEndian.PutUint32(b[8:], uint32(len(strs)))
	if utf8 {
		binary.LittleEndian.PutUint32(b[16:], stringPoolUTF8)
	}
	binary.LittleEndian.PutUint32(b[20:], uint32(start))
	for i, o := range offsets {
		binary.LittleEndian.PutUint32(b[hdr+4*i:], o)
	}
	return append(b, data...)
}

func appendLen8(b []byte, n int) []byte {
	if n > 0x7f {
		return append(b, byte(n>>8)|0x80, byte(n))
	}
	return append(b, byte(n))
}

// chunkBytes wraps a body in a chunk header.
func chunkBytes(typ uint16, headerSize int, header, body []byte) []byte {
	b := make([]byte, 8, 8+len(header)+len(body))
	binary.LittleEndian.PutUint16(b[0:], typ)
	binary.LittleEndian.PutUint16(b[2:], uint16(headerSize))
	binary.LittleEndian.PutUint32(b[4:], uint32(8+len(header)+len(body)))
	return append(append(b, header...), body...)
}

// encodeAXML encodes a document the way aapt2 lays it out, with UTF-8
// strings (aapt2 writes UTF-16; the aapt2 fixture covers that).
func encodeAXML(root xmlElemSpec) []byte {
	// Attribute names with a resource id come first, in the resource map's
	// order; every other string follows.
	var ids []uint32
	var strs []string
	index := map[string]uint32{}
	var collect func(e xmlElemSpec, withID bool)
	add := func(s string) {
		if _, ok := index[s]; !ok {
			index[s] = uint32(len(strs))
			strs = append(strs, s)
		}
	}
	collect = func(e xmlElemSpec, withID bool) {
		for _, a := range e.attrs {
			if withID && a.id != 0 {
				if _, ok := index[a.name]; !ok {
					add(a.name)
					ids = append(ids, a.id)
				}
			}
			if !withID {
				add(a.name)
				if s, ok := a.value.(string); ok {
					add(s)
				}
			}
		}
		if !withID {
			add(e.name)
		}
		for _, k := range e.kids {
			collect(k, withID)
		}
	}
	collect(root, true)
	add("android")
	add(androidNS)
	collect(root, false)

	var body []byte
	body = append(body, stringPool(strs, true)...)
	var resMap []byte
	for _, id := range ids {
		resMap = binary.LittleEndian.AppendUint32(resMap, id)
	}
	body = append(body, chunkBytes(resXMLResourceMap, 8, nil, resMap)...)
	node := func(extra ...uint32) []byte {
		h := []byte{1, 0, 0, 0, 0xff, 0xff, 0xff, 0xff} // line 1, no comment
		for _, x := range extra {
			h = binary.LittleEndian.AppendUint32(h, x)
		}
		return h
	}
	body = append(body, chunkBytes(0x0100, 16, node(index["android"], index[androidNS]), nil)...)
	var elem func(e xmlElemSpec)
	elem = func(e xmlElemSpec) {
		ext := binary.LittleEndian.AppendUint32(nil, noIndex)
		ext = binary.LittleEndian.AppendUint32(ext, index[e.name])
		ext = binary.LittleEndian.AppendUint16(ext, 20) // attributeStart
		ext = binary.LittleEndian.AppendUint16(ext, 20) // attributeSize
		ext = binary.LittleEndian.AppendUint16(ext, uint16(len(e.attrs)))
		ext = append(ext, 0, 0, 0, 0, 0, 0)
		for _, a := range e.attrs {
			ns := uint32(noIndex)
			if a.id != 0 {
				ns = index[androidNS]
			}
			raw, typ, data := uint32(noIndex), uint8(0), uint32(0)
			switch v := a.value.(type) {
			case string:
				raw, typ, data = index[v], typeString, index[v]
			case bool:
				typ = typeBoolean
				if v {
					data = 0xffffffff
				}
			case int:
				typ, data = typeIntDec, uint32(int32(v))
			case resRef:
				typ, data = typeReference, uint32(v)
			}
			ext = binary.LittleEndian.AppendUint32(ext, ns)
			ext = binary.LittleEndian.AppendUint32(ext, index[a.name])
			ext = binary.LittleEndian.AppendUint32(ext, raw)
			ext = append(ext, 8, 0, 0, typ)
			ext = binary.LittleEndian.AppendUint32(ext, data)
		}
		body = append(body, chunkBytes(resXMLStartElement, 16, node(), ext)...)
		for _, k := range e.kids {
			elem(k)
		}
		end := binary.LittleEndian.AppendUint32(nil, noIndex)
		end = binary.LittleEndian.AppendUint32(end, index[e.name])
		body = append(body, chunkBytes(resXMLStartElement+1, 16, node(), end)...)
	}
	elem(root)
	body = append(body, chunkBytes(0x0101, 16, node(index["android"], index[androidNS]), nil)...)
	return chunkBytes(resXMLType, 8, nil, body)
}

// encodeARSC encodes a resource table with one package (0x7f) and one type
// (string, id 1) whose entries are the given strings, in order, as
// 0x7f010000, 0x7f010001, … (UTF-16 pools; aapt2 writes UTF-8 values, which
// the aapt2 fixture covers).
func encodeARSC(names, values []string) []byte {
	pkgHeader := make([]byte, 288-8)
	binary.LittleEndian.PutUint32(pkgHeader[0:], 0x7f)
	for i, u := range utf16.Encode([]rune(appID)) {
		binary.LittleEndian.PutUint16(pkgHeader[4+2*i:], u)
	}
	typeStrings := stringPool([]string{"string"}, false)
	keyStrings := stringPool(names, false)
	binary.LittleEndian.PutUint32(pkgHeader[260:], 288)
	binary.LittleEndian.PutUint32(pkgHeader[268:], uint32(288+len(typeStrings)))

	// A typeSpec chunk, which the reader skips.
	spec := []byte{1, 0, 0, 0}
	spec = binary.LittleEndian.AppendUint32(spec, uint32(len(names)))
	specBody := make([]byte, 4*len(names))
	// A type chunk with the default configuration (64 bytes, all zero).
	cfg := make([]byte, 64)
	binary.LittleEndian.PutUint32(cfg, 64)
	th := []byte{1, 0, 0, 0}
	th = binary.LittleEndian.AppendUint32(th, uint32(len(names)))
	th = binary.LittleEndian.AppendUint32(th, uint32(8+12+64+4*len(names)))
	th = append(th, cfg...)
	var offsets, entries []byte
	for i := range names {
		offsets = binary.LittleEndian.AppendUint32(offsets, uint32(len(entries)))
		e := []byte{8, 0, 0, 0}
		e = binary.LittleEndian.AppendUint32(e, uint32(i))
		e = append(e, 8, 0, 0, typeString)
		e = binary.LittleEndian.AppendUint32(e, uint32(i))
		entries = append(entries, e...)
	}
	pkgBody := append(append(append([]byte{}, typeStrings...), keyStrings...), chunkBytes(0x0202, 16, spec, specBody)...)
	pkgBody = append(pkgBody, chunkBytes(resTableTypeType, 8+len(th), th, append(offsets, entries...))...)
	body := append(stringPool(values, false), chunkBytes(resTablePackageType, 288, pkgHeader, pkgBody)...)
	return chunkBytes(resTableType, 12, binary.LittleEndian.AppendUint32(nil, 1), body)
}

// xmlPlist encodes a dictionary as an XML property list.
func xmlPlist(m map[string]any) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n" + `<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n<plist version=\"1.0\">\n")
	var value func(v any)
	value = func(v any) {
		switch t := v.(type) {
		case string:
			b.WriteString("<string>" + xmlEscape(t) + "</string>")
		case bool:
			b.WriteString(fmt.Sprintf("<%v/>", t))
		case int:
			b.WriteString(fmt.Sprintf("<integer>%d</integer>", t))
		case map[string]any:
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			b.WriteString("<dict>")
			for _, k := range keys {
				b.WriteString("<key>" + xmlEscape(k) + "</key>")
				value(t[k])
			}
			b.WriteString("</dict>")
		default:
			panic(fmt.Sprintf("xmlPlist: %T", v))
		}
	}
	value(m)
	b.WriteString("\n</plist>\n")
	return []byte(b.String())
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	if err := xml.EscapeText(&b, []byte(s)); err != nil {
		panic(err)
	}
	return b.String()
}

// writeZip writes a zip file with the given members, in name order.
func writeZip(t *testing.T, path string, files map[string][]byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(files[n]); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// buildSpec describes the test artifacts of one native build: the
// settings that a correct release build has, which a test then breaks.
type buildSpec struct {
	build     int
	version   string
	pkg       string
	cert      []byte
	runtimes  map[string]string
	commit    time.Time
	embedded  map[string]string // platform → embedded update id
	android   map[string]any    // meta-data overrides; nil removes one
	ios       map[string]any    // Expo.plist overrides; nil removes one
	iosInfo   map[string]any    // Info.plist overrides
	omit      []string          // zip members to leave out
	runtimeAt map[string]string // the runtime setting, when not the sentinel
}

func newBuildSpec(cert []byte, build int) *buildSpec {
	return &buildSpec{build: build, version: fmt.Sprintf("1.%d.0", build), pkg: appID, cert: cert,
		runtimes: map[string]string{"android": "fixture-android-runtime-" + fmt.Sprint(build), "ios": "fixture-ios-runtime-" + fmt.Sprint(build)},
		commit:   time.Now().Add(-time.Minute), embedded: map[string]string{"android": newUUID(), "ios": newUUID()}}
}

func (s *buildSpec) embeddedManifest(platform string) []byte {
	return []byte(fmt.Sprintf(`{"id":"%s","commitTime":%d,"assets":[]}`, s.embedded[platform], s.commit.UnixMilli()))
}

func omitted(omit []string, name string) bool {
	for _, o := range omit {
		if o == name {
			return true
		}
	}
	return false
}

// writeAPK writes the APK of a buildSpec.
func (s *buildSpec) writeAPK(t *testing.T, path string) {
	t.Helper()
	runtime := any(resRef(0x7f010001))
	if v, ok := s.runtimeAt["android"]; ok {
		runtime = v
	}
	meta := map[string]any{
		androidKeys.enabled:                         true,
		androidKeys.url:                             manifestURL,
		androidKeys.headers:                         `{"expo-channel-name":"production"}`,
		androidKeys.cert:                            string(s.cert),
		androidKeys.metadata:                        `{"keyid":"root-2026","alg":"rsa-v1_5-sha256"}`,
		androidKeys.chain:                           true,
		androidKeys.runtime:                         runtime,
		metaPrefix + "EXPO_UPDATES_CHECK_ON_LAUNCH": "ALWAYS",
		metaPrefix + "EXPO_UPDATES_LAUNCH_WAIT_MS":  0,
	}
	for k, v := range s.android {
		if v == nil {
			delete(meta, k)
		} else {
			meta[k] = v
		}
	}
	keys := make([]string, 0, len(meta))
	for k := range meta {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	app := xmlElemSpec{name: "application", attrs: []xmlAttrSpec{{"label", 0x01010001, resRef(0x7f010000)}}}
	for _, k := range keys {
		app.kids = append(app.kids, xmlElemSpec{name: "meta-data", attrs: []xmlAttrSpec{{"name", attrName, k}, {"value", attrValue, meta[k]}}})
	}
	root := xmlElemSpec{name: "manifest", attrs: []xmlAttrSpec{
		{"versionCode", attrVersionCode, s.build}, {"versionName", attrVersionName, s.version}, {"package", 0, s.pkg},
	}, kids: []xmlElemSpec{{name: "uses-sdk", attrs: []xmlAttrSpec{{"minSdkVersion", 0x0101020c, 24}}}, app}}
	files := map[string][]byte{
		"AndroidManifest.xml":         encodeAXML(root),
		"resources.arsc":              encodeARSC([]string{"app_name", "expo_runtime_version"}, []string{"Alex Messages", fingerprintSentinel}),
		"assets/index.android.bundle": []byte("var bundle = 1;\n"),
		"assets/app.manifest":         s.embeddedManifest("android"),
		"assets/fingerprint":          []byte(s.runtimes["android"]),
		"classes.dex":                 []byte("dex\n035\x00"),
	}
	for _, o := range s.omit {
		delete(files, o)
	}
	writeZip(t, path, files)
}

// appFiles are the files of the iOS app (what xcodebuild archive puts into
// AlexMessages.app), by path inside the app.
func (s *buildSpec) appFiles() map[string][]byte {
	expo := map[string]any{
		iosKeys.enabled:          true,
		iosKeys.url:              manifestURL,
		iosKeys.headers:          map[string]any{"expo-channel-name": "production"},
		iosKeys.cert:             string(s.cert),
		iosKeys.metadata:         map[string]any{"keyid": "root-2026", "alg": "rsa-v1_5-sha256"},
		iosKeys.chain:            true,
		iosKeys.runtime:          fingerprintSentinel,
		"EXUpdatesCheckOnLaunch": "ALWAYS",
		"EXUpdatesLaunchWaitMs":  0,
	}
	if v, ok := s.runtimeAt["ios"]; ok {
		expo[iosKeys.runtime] = v
	}
	for k, v := range s.ios {
		if v == nil {
			delete(expo, k)
		} else {
			expo[k] = v
		}
	}
	info := map[string]any{"CFBundleIdentifier": s.pkg, "CFBundleShortVersionString": s.version,
		"CFBundleVersion": fmt.Sprint(s.build), "CFBundleExecutable": "AlexMessages"}
	for k, v := range s.iosInfo {
		if v == nil {
			delete(info, k)
		} else {
			info[k] = v
		}
	}
	files := map[string][]byte{
		"Info.plist":                    xmlPlist(info),
		"Expo.plist":                    xmlPlist(expo),
		"main.jsbundle":                 []byte("var bundle = 1;\n"),
		"EXUpdates.bundle/app.manifest": s.embeddedManifest("ios"),
		"EXUpdates.bundle/fingerprint":  []byte(s.runtimes["ios"]),
		"AlexMessages":                  []byte("\xcf\xfa\xed\xfe fake Mach-O"),
	}
	for name := range files {
		if omitted(s.omit, iosApp+name) {
			delete(files, name)
		}
	}
	return files
}

// writeIPA writes the IPA of a buildSpec.
func (s *buildSpec) writeIPA(t *testing.T, path string) {
	t.Helper()
	files := map[string][]byte{}
	for name, b := range s.appFiles() {
		files[iosApp+name] = b
	}
	writeZip(t, path, files)
}

// writeAppDir writes the app's files into dir/AlexMessages.app.
func (s *buildSpec) writeAppDir(t *testing.T, dir string) {
	t.Helper()
	for name, b := range s.appFiles() {
		p := filepath.Join(dir, "AlexMessages.app", filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func (s *buildSpec) expect() nativeExpect {
	return nativeExpect{build: s.build, version: s.version, cert: s.cert, runtimes: s.runtimes, now: time.Now()}
}
