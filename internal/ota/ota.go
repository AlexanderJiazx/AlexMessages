// Package ota is the server side of over-the-air JS updates for the Alex
// Messages mobile app (OTAUpdatePlan.md, D2, D4, D8 and D10): the Expo
// Updates protocol v1 as expo-updates 57.0.26 speaks it, the update store
// under data/ota, the certificate-chain checks the phones apply, the install
// census, the X-AM-Client header and the native-build policy.
//
// Nothing here signs. Signing lives in internal/ota/sign, which the server
// binaries never import: the server only selects bytes that were signed on
// the publisher's Mac.
package ota

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	// FormatVersion is the content of data/ota/FORMAT. A store without that
	// file is not initialized (`otactl init` writes it) and serves nothing.
	FormatVersion = "1"

	// RootKeyID is the keyid of codeSigningMetadata in every binary
	// (app.json). Every signature carries it: phones in chain mode ignore a
	// signature's keyid, and the single-certificate fallback (D4) requires it.
	RootKeyID = "root-2026"
	// RootCertFile is the root certificate's copy under data/ota/certs.
	RootCertFile = "ota-root-2026.pem"
	// SignatureAlg is the only algorithm expo-updates verifies.
	SignatureAlg = "rsa-v1_5-sha256"

	ChannelProduction = "production"
	ChannelBeta       = "beta"

	PlatformAndroid = "android"
	PlatformIOS     = "ios"
)

var (
	// runtimeRE is the runtime-version syntax expo-updates accepts.
	runtimeRE = regexp.MustCompile(`^[a-zA-Z\d][a-zA-Z\d._+()-]{0,254}$`)
	uuidRE    = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	// An asset file is <SHA-256 as unpadded base64url>.<extension>.
	assetNameRE  = regexp.MustCompile(`^([A-Za-z0-9_-]{43})\.([a-z0-9]{1,10})$`)
	nativeNameRE = regexp.MustCompile(`^AlexMessages-(\d{1,4}\.\d{1,4}\.\d{1,4})-(\d{1,6})\.(apk|ipa)$`)
	// A leaf file names the SHA-256 of the certificate's DER bytes in hex.
	leafSHARE = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// ValidPlatform reports whether p is one of the two platforms expo-updates
// sends in expo-platform.
func ValidPlatform(p string) bool {
	return p == PlatformAndroid || p == PlatformIOS
}

// ValidRuntime reports whether rt is a runtime version expo-updates accepts.
// Such a value never contains a path separator and never starts with a dot,
// so it is safe as a file name.
func ValidRuntime(rt string) bool {
	return runtimeRE.MatchString(rt)
}

// NormalizeUUID returns s in lowercase canonical form, or false when s is not
// a UUID. The clients send update ids in lowercase, and iOS sends its
// EAS-Client-ID in uppercase.
func NormalizeUUID(s string) (string, bool) {
	if !uuidRE.MatchString(s) {
		return "", false
	}
	return strings.ToLower(s), true
}

// ParseAssetName splits an /ota/assets/ file name into its hash and
// extension. The hash must be the unpadded base64url encoding of exactly 32
// bytes, so every asset has one name.
func ParseAssetName(name string) (hash, ext string, ok bool) {
	m := assetNameRE.FindStringSubmatch(name)
	if m == nil {
		return "", "", false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(m[1])
	if err != nil || len(raw) != 32 {
		return "", "", false
	}
	return m[1], m[2], true
}

// AssetContentType is the Content-Type an asset is served with: the launch
// bundle is always stored as .js. Unknown extensions are served as bytes;
// expo-updates checks every asset's hash, not its type.
func AssetContentType(ext string) string {
	switch ext {
	case "js":
		return "application/javascript"
	case "json":
		return "application/json"
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	case "svg":
		return "image/svg+xml"
	case "ttf":
		return "font/ttf"
	case "otf":
		return "font/otf"
	case "woff":
		return "font/woff"
	case "woff2":
		return "font/woff2"
	case "mp3":
		return "audio/mpeg"
	case "wav":
		return "audio/wav"
	case "mp4":
		return "video/mp4"
	}
	return "application/octet-stream"
}

// NativeFileName is the published file name of a native build:
// AlexMessages-<version>-<build>.apk on Android, .ipa on iOS.
func NativeFileName(platform, version string, build int) string {
	ext := "apk"
	if platform == PlatformIOS {
		ext = "ipa"
	}
	return fmt.Sprintf("AlexMessages-%s-%d.%s", version, build, ext)
}

// ParseNativeName checks a /app/<platform>/ file name and returns its version
// and build. The extension must match the platform.
func ParseNativeName(platform, name string) (version string, build int, ok bool) {
	m := nativeNameRE.FindStringSubmatch(name)
	if m == nil {
		return "", 0, false
	}
	if (platform == PlatformAndroid) != (m[3] == "apk") || !ValidPlatform(platform) {
		return "", 0, false
	}
	build, err := strconv.Atoi(m[2])
	if err != nil {
		return "", 0, false
	}
	return m[1], build, true
}

// NativeContentType is the Content-Type of a native build download. Android
// offers to install a file served as a package archive.
func NativeContentType(platform string) string {
	if platform == PlatformAndroid {
		return "application/vnd.android.package-archive"
	}
	return "application/octet-stream"
}
