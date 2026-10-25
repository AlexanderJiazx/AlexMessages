package ota

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// The X-AM-Client header (OTAUpdatePlan.md, D8 item 2) that build 2 and later
// send on every REST call, on the /ws upgrade as the client= query parameter,
// and from the Android ring service:
//
//	v=1;platform=android;version=1.2.0;build=2;runtime=<fp>;update=<uuid|embedded>;channel=production
//	v=1;platform=android;build=2;component=ring
//
// It is a claim of the client, used for the native-build policy and stored
// per session for the promote gate. The web client sends nothing.

// ClientInfo is a parsed X-AM-Client value. Raw is the value as sent (with
// surrounding space removed); it is what a session stores.
type ClientInfo struct {
	Raw       string
	Platform  string
	Version   string
	Build     int
	Runtime   string
	Update    string
	Channel   string
	Component string
}

const maxClientInfoLen = 512

var (
	versionRE     = regexp.MustCompile(`^\d{1,4}\.\d{1,4}\.\d{1,4}$`)
	buildRE       = regexp.MustCompile(`^[1-9]\d{0,5}$`)
	clientKeyRE   = regexp.MustCompile(`^[a-z_]{1,32}$`)
	clientValueRE = regexp.MustCompile(`^[A-Za-z0-9._+()-]{0,255}$`)
)

// ParseClientInfo parses an X-AM-Client value. It returns false for an empty
// or malformed value; such a client is treated exactly like one that sends no
// header (never gated, nothing stored). Unknown fields are allowed, so a later
// build can add one.
func ParseClientInfo(v string) (*ClientInfo, bool) {
	v = strings.TrimSpace(v)
	if v == "" || len(v) > maxClientInfoLen {
		return nil, false
	}
	fields := map[string]string{}
	for _, f := range strings.Split(v, ";") {
		key, value, ok := strings.Cut(strings.TrimSpace(f), "=")
		if !ok || !clientKeyRE.MatchString(key) || !clientValueRE.MatchString(value) {
			return nil, false
		}
		if _, dup := fields[key]; dup {
			return nil, false
		}
		fields[key] = value
	}
	if fields["v"] != "1" || !ValidPlatform(fields["platform"]) || !buildRE.MatchString(fields["build"]) {
		return nil, false
	}
	build, _ := strconv.Atoi(fields["build"])
	info := &ClientInfo{
		Platform:  fields["platform"],
		Build:     build,
		Version:   fields["version"],
		Runtime:   fields["runtime"],
		Update:    fields["update"],
		Channel:   fields["channel"],
		Component: fields["component"],
	}
	if info.Version != "" && !versionRE.MatchString(info.Version) {
		return nil, false
	}
	if info.Runtime != "" && !ValidRuntime(info.Runtime) {
		return nil, false
	}
	if info.Update != "" && info.Update != "embedded" {
		id, ok := NormalizeUUID(info.Update)
		if !ok {
			return nil, false
		}
		info.Update = id
	}
	if info.Channel != "" && info.Channel != ChannelProduction && info.Channel != ChannelBeta {
		return nil, false
	}
	if info.Component != "" && info.Component != "app" && info.Component != "ring" {
		return nil, false
	}
	info.Raw = v
	return info, true
}

// NativeBuild is a published native build (policy.json's latest).
type NativeBuild struct {
	Version string `json:"version"`
	Build   int    `json:"build"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
	Notes   string `json:"notes"`
}

// PlatformPolicy is the native-build policy of one platform. MinBuild 0 is
// off. HardGate turns a MinBuild violation from a blocking screen in the app
// (soft gate) into REST 426 and WebSocket close 4426; it is false unless an
// old native protocol is about to be removed (D10).
type PlatformPolicy struct {
	MinBuild int          `json:"min_build"`
	HardGate bool         `json:"hard_gate"`
	Latest   *NativeBuild `json:"latest"`
}

// Policy is native/policy.json.
type Policy struct {
	Android PlatformPolicy `json:"android"`
	IOS     PlatformPolicy `json:"ios"`
}

// Client policy states sent in the WS init (client_policy.state).
const (
	PolicyOK              = "ok"
	PolicyUpdateAvailable = "update_available"
	PolicyUpdateRequired  = "update_required"
)

// ClientPolicy is client_policy in the WS init.
type ClientPolicy struct {
	State  string       `json:"state"`
	Latest *NativeBuild `json:"latest"`
}

// For returns the policy of a platform.
func (p *Policy) For(platform string) *PlatformPolicy {
	if platform == PlatformIOS {
		return &p.IOS
	}
	return &p.Android
}

// Validate checks every field; a policy that fails is never applied.
func (p *Policy) Validate() error {
	for _, platform := range []string{PlatformAndroid, PlatformIOS} {
		pp := p.For(platform)
		if pp.MinBuild < 0 {
			return fmt.Errorf("%s: min_build %d is negative", platform, pp.MinBuild)
		}
		if b := pp.Latest; b != nil {
			if err := b.validate(platform); err != nil {
				return fmt.Errorf("%s: latest: %w", platform, err)
			}
		}
	}
	return nil
}

var sha256HexRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (b *NativeBuild) validate(platform string) error {
	if !versionRE.MatchString(b.Version) {
		return fmt.Errorf("version %q is not N.N.N", b.Version)
	}
	if b.Build < 1 {
		return fmt.Errorf("build %d is below 1", b.Build)
	}
	if !sha256HexRE.MatchString(b.SHA256) {
		return fmt.Errorf("sha256 %q is not lowercase hex", b.SHA256)
	}
	if b.Size <= 0 {
		return fmt.Errorf("size %d is not positive", b.Size)
	}
	if len(b.Notes) > 2000 {
		return fmt.Errorf("notes are longer than 2000 bytes")
	}
	u, err := url.Parse(b.URL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil ||
		u.RawQuery != "" || u.Fragment != "" ||
		u.Path != "/app/"+platform+"/"+NativeFileName(platform, b.Version, b.Build) {
		return fmt.Errorf("url %q is not /app/%s/%s on an http(s) host", b.URL, platform, NativeFileName(platform, b.Version, b.Build))
	}
	return nil
}

// ClientPolicy is the client_policy for a client that sent X-AM-Client.
func (p *Policy) ClientPolicy(c *ClientInfo) *ClientPolicy {
	pp := p.For(c.Platform)
	cp := &ClientPolicy{State: PolicyOK, Latest: pp.Latest}
	switch {
	case pp.MinBuild > 0 && c.Build < pp.MinBuild:
		cp.State = PolicyUpdateRequired
	case pp.Latest != nil && pp.Latest.Build > c.Build:
		cp.State = PolicyUpdateAvailable
	}
	return cp
}

// HardGated reports whether the hard gate refuses this client: REST 426 and
// WebSocket close 4426.
func (p *Policy) HardGated(c *ClientInfo) bool {
	pp := p.For(c.Platform)
	return pp.HardGate && pp.MinBuild > 0 && c.Build < pp.MinBuild
}
