package ota

import (
	"strings"
	"testing"
)

func TestParseClientInfo(t *testing.T) {
	c, ok := ParseClientInfo(" v=1;platform=android;version=1.2.0;build=2;runtime=a1b2c3;update=0F8B8F4E-1F6E-4D2A-9A51-3C8E2F0D9B11;channel=beta ")
	if !ok {
		t.Fatal("app header refused")
	}
	if c.Platform != PlatformAndroid || c.Version != "1.2.0" || c.Build != 2 || c.Runtime != "a1b2c3" ||
		c.Update != "0f8b8f4e-1f6e-4d2a-9a51-3c8e2f0d9b11" || c.Channel != ChannelBeta || c.Component != "" ||
		strings.HasPrefix(c.Raw, " ") {
		t.Fatalf("parsed %+v", c)
	}
	ring, ok := ParseClientInfo("v=1;platform=android;build=2;component=ring")
	if !ok || ring.Component != "ring" || ring.Build != 2 {
		t.Fatalf("ring header: %+v %v", ring, ok)
	}
	if c, ok := ParseClientInfo("v=1;platform=ios;build=3;update=embedded;future_field=x"); !ok || c.Update != "embedded" {
		t.Fatalf("unknown field: %+v %v", c, ok)
	}
	for _, bad := range []string{
		"",
		"v=2;platform=android;build=2",
		"platform=android;build=2",
		"v=1;platform=web;build=2",
		"v=1;platform=android",
		"v=1;platform=android;build=0",
		"v=1;platform=android;build=02",
		"v=1;platform=android;build=1234567",
		"v=1;platform=android;build=2;build=3",
		"v=1;platform=android;build=2;version=1.2",
		"v=1;platform=android;build=2;update=latest",
		"v=1;platform=android;build=2;channel=alpha",
		"v=1;platform=android;build=2;component=widget",
		"v=1;platform=android;build=2;runtime=../x",
		"v=1;platform=android;build=2;note=a b",
		"v=1;platform=android;build=2;" + strings.Repeat("x=1;", 130),
	} {
		if c, ok := ParseClientInfo(bad); ok {
			t.Errorf("ParseClientInfo(%q) = %+v", bad, c)
		}
	}
}

func TestClientPolicyStates(t *testing.T) {
	latest := &NativeBuild{Version: "1.3.0", Build: 3}
	p := &Policy{Android: PlatformPolicy{Latest: latest}}
	build := func(n int) *ClientInfo { return &ClientInfo{Platform: PlatformAndroid, Build: n} }

	if cp := p.ClientPolicy(build(3)); cp.State != PolicyOK || cp.Latest != latest {
		t.Fatalf("current build: %+v", cp)
	}
	if cp := p.ClientPolicy(build(2)); cp.State != PolicyUpdateAvailable {
		t.Fatalf("older build: %+v", cp)
	}
	p.Android.MinBuild = 3
	if cp := p.ClientPolicy(build(2)); cp.State != PolicyUpdateRequired {
		t.Fatalf("below min_build: %+v", cp)
	}
	// The soft gate is the default: below min_build is not hard-gated
	// unless hard_gate is set.
	if p.HardGated(build(2)) {
		t.Fatal("hard gate without hard_gate")
	}
	p.Android.HardGate = true
	if !p.HardGated(build(2)) || p.HardGated(build(3)) {
		t.Fatal("hard gate")
	}
	// The other platform is untouched.
	if cp := p.ClientPolicy(&ClientInfo{Platform: PlatformIOS, Build: 1}); cp.State != PolicyOK || cp.Latest != nil ||
		p.HardGated(&ClientInfo{Platform: PlatformIOS, Build: 1}) {
		t.Fatalf("ios: %+v", cp)
	}
	// min_build 0 is off.
	p.Android.MinBuild = 0
	if p.HardGated(build(1)) {
		t.Fatal("min_build 0 gated")
	}
}
