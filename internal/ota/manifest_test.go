package ota

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"alexmessage/internal/ota/otatest"
	"alexmessage/internal/ota/sign"
)

const testRuntime = "a1b2c3d4e5f6"

func phoneHeaders(current, embedded string) http.Header {
	h := http.Header{}
	h.Set("Expo-Protocol-Version", "1")
	h.Set("Expo-Platform", PlatformAndroid)
	h.Set("Expo-Runtime-Version", testRuntime)
	if current != "" {
		h.Set("Expo-Current-Update-ID", current)
	}
	if embedded != "" {
		h.Set("Expo-Embedded-Update-ID", embedded)
	}
	return h
}

func mustRequest(t *testing.T, h http.Header) *ManifestRequest {
	t.Helper()
	req, err := ParseManifestRequest(h)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestParseManifestRequestRejects(t *testing.T) {
	for name, edit := range map[string]func(http.Header){
		"protocol 0":       func(h http.Header) { h.Set("Expo-Protocol-Version", "0") },
		"no protocol":      func(h http.Header) { h.Del("Expo-Protocol-Version") },
		"platform web":     func(h http.Header) { h.Set("Expo-Platform", "web") },
		"no runtime":       func(h http.Header) { h.Del("Expo-Runtime-Version") },
		"runtime with ../": func(h http.Header) { h.Set("Expo-Runtime-Version", "../etc") },
		"runtime too long": func(h http.Header) { h.Set("Expo-Runtime-Version", strings.Repeat("a", 256)) },
	} {
		h := phoneHeaders("", "")
		edit(h)
		if _, err := ParseManifestRequest(h); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestParseManifestRequestCensusFields(t *testing.T) {
	h := phoneHeaders("0F8B8F4E-1F6E-4D2A-9A51-3C8E2F0D9B11", "not-a-uuid")
	h.Set("EAS-Client-ID", "4E1C7A52-0D35-4B39-9C55-0B3E1A2F6D10")
	h.Set("Expo-Extra-Params", `am_channel="beta", am_uid="42", other=?1`)
	h.Set("Expo-Recent-Failed-Update-IDs", `"11111111-2222-4333-8444-555555555555", "11111111-2222-4333-8444-555555555555", "junk", tok`)
	h.Set("Expo-Fatal-Error", strings.Repeat("é", MaxFatalErrorLen+10))
	req := mustRequest(t, h)
	if req.ClientID != "4e1c7a52-0d35-4b39-9c55-0b3e1a2f6d10" {
		t.Errorf("client id %q", req.ClientID)
	}
	if req.CurrentUpdateID != "0f8b8f4e-1f6e-4d2a-9a51-3c8e2f0d9b11" || req.EmbeddedUpdateID != "" {
		t.Errorf("update ids %q %q", req.CurrentUpdateID, req.EmbeddedUpdateID)
	}
	if req.Channel != ChannelBeta || req.UserID != 42 {
		t.Errorf("extra params: channel %q uid %d", req.Channel, req.UserID)
	}
	if len(req.FailedUpdateIDs) != 1 || req.FailedUpdateIDs[0] != "11111111-2222-4333-8444-555555555555" {
		t.Errorf("failed ids %v", req.FailedUpdateIDs)
	}
	if n := len([]rune(req.FatalError)); n != MaxFatalErrorLen {
		t.Errorf("fatal error has %d characters", n)
	}

	// A token is not a string: am_channel=beta does not select beta, and a
	// malformed Expo-Extra-Params is ignored instead of failing the check.
	h.Set("Expo-Extra-Params", `am_channel=beta, am_uid="0"`)
	if req := mustRequest(t, h); req.Channel != ChannelProduction || req.UserID != 0 {
		t.Errorf("token channel: %q %d", req.Channel, req.UserID)
	}
	h.Set("Expo-Extra-Params", `am_channel="beta",`)
	if req := mustRequest(t, h); req.Channel != ChannelProduction {
		t.Errorf("malformed params selected %q", req.Channel)
	}
}

// parts decodes a multipart answer with Go's own reader, as a client would.
func decodeParts(t *testing.T, body []byte, contentType string) map[string]*multipart.Part {
	t.Helper()
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatal(err)
	}
	r := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	out := map[string]*multipart.Part{}
	for {
		p, err := r.NextPart()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out[p.FormName()] = p
	}
}

func TestSelect(t *testing.T) {
	f := otatest.New(t)
	s := &Store{Dir: f.Dir}
	embedded := otatest.NewUUID()
	u1 := f.AddUpdate(t, PlatformAndroid, testRuntime)

	none := func(name string, req *ManifestRequest) {
		t.Helper()
		sel, err := s.Select(req)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if sel.Parts != nil {
			t.Fatalf("%s: served %s", name, sel.UpdateID)
		}
	}
	req := mustRequest(t, phoneHeaders(embedded, embedded))

	none("no pointer", req)
	f.Point(t, ChannelProduction, PlatformAndroid, testRuntime, StateUpdate, u1.ID)
	none("runtime not registered", req)
	f.RegisterRuntime(t, PlatformAndroid, testRuntime, embedded, 2)

	sel, err := s.Select(req)
	if err != nil || sel.UpdateID != u1.ID || sel.State != StateUpdate || sel.Channel != ChannelProduction {
		t.Fatalf("update: %+v, %v", sel, err)
	}
	if len(sel.Parts) != 2 || sel.Parts[0].Name != "manifest" || !bytes.Equal(sel.Parts[0].Body, u1.Manifest) ||
		sel.Parts[0].Signature != u1.ManifestSig || sel.Parts[1].Name != "certificate_chain" ||
		!bytes.Equal(sel.Parts[1].Body, f.LeafPEM) {
		t.Fatalf("update parts: %+v", sel.Parts)
	}
	// What the phone receives verifies against its embedded root.
	if _, err := VerifySignature(sel.Parts[0].Body, sel.Parts[0].Signature, sel.Parts[1].Body, f.Root, time.Now()); err != nil {
		t.Fatalf("served manifest does not verify: %v", err)
	}

	none("already on the update", mustRequest(t, phoneHeaders(u1.ID, embedded)))

	f.Revoke(t, u1.ID)
	none("revoked update", req)

	// rollback-embedded revokes the update and points at its directive: the
	// directive is still served.
	f.Point(t, ChannelProduction, PlatformAndroid, testRuntime, StateRollback, u1.ID)
	sel, err = s.Select(mustRequest(t, phoneHeaders(u1.ID, embedded)))
	if err != nil || sel.UpdateID != u1.ID || sel.Parts[0].Name != "directive" || !bytes.Equal(sel.Parts[0].Body, u1.Rollback) {
		t.Fatalf("rollback: %+v, %v", sel, err)
	}
	if _, err := VerifySignature(sel.Parts[0].Body, sel.Parts[0].Signature, sel.Parts[1].Body, f.Root, time.Now()); err != nil {
		t.Fatalf("served directive does not verify: %v", err)
	}
	none("rollback, already on the embedded bundle", req)

	f.Point(t, ChannelProduction, PlatformAndroid, testRuntime, StateHalted, u1.ID)
	none("halted", req)
}

func TestSelectBetaChannel(t *testing.T) {
	f := otatest.New(t)
	s := &Store{Dir: f.Dir}
	embedded := otatest.NewUUID()
	f.RegisterRuntime(t, PlatformAndroid, testRuntime, embedded, 2)
	prod := f.AddUpdate(t, PlatformAndroid, testRuntime)
	beta := f.AddUpdate(t, PlatformAndroid, testRuntime)
	f.Point(t, ChannelProduction, PlatformAndroid, testRuntime, StateUpdate, prod.ID)

	h := phoneHeaders(embedded, embedded)
	h.Set("Expo-Extra-Params", `am_channel="beta"`)
	betaReq := mustRequest(t, h)

	// Without a beta pointer, a beta phone gets production.
	if sel, err := s.Select(betaReq); err != nil || sel.UpdateID != prod.ID || sel.Channel != ChannelProduction {
		t.Fatalf("no beta pointer: %+v, %v", sel, err)
	}
	f.Point(t, ChannelBeta, PlatformAndroid, testRuntime, StateUpdate, beta.ID)
	if sel, err := s.Select(betaReq); err != nil || sel.UpdateID != beta.ID || sel.Channel != ChannelBeta {
		t.Fatalf("beta pointer: %+v, %v", sel, err)
	}
	// A production phone never sees beta.
	if sel, err := s.Select(mustRequest(t, phoneHeaders(embedded, embedded))); err != nil || sel.UpdateID != prod.ID {
		t.Fatalf("production phone: %+v, %v", sel, err)
	}
	// A halted beta pointer stops beta phones; they do not fall back to
	// production.
	f.Point(t, ChannelBeta, PlatformAndroid, testRuntime, StateHalted, beta.ID)
	if sel, err := s.Select(betaReq); err != nil || sel.Parts != nil || sel.Channel != ChannelBeta {
		t.Fatalf("halted beta: %+v, %v", sel, err)
	}
}

func TestSelectSendsTheLeafNamedInLeafFile(t *testing.T) {
	f := otatest.New(t)
	s := &Store{Dir: f.Dir}
	embedded := otatest.NewUUID()
	f.RegisterRuntime(t, PlatformAndroid, testRuntime, embedded, 2)
	u := f.AddUpdate(t, PlatformAndroid, testRuntime)
	f.Point(t, ChannelProduction, PlatformAndroid, testRuntime, StateUpdate, u.ID)

	// A second leaf issued later sits next to the first; the manifest still
	// names the first.
	der, err := sign.IssueLeaf(f.Root, f.RootKey, &f.RootKey.PublicKey, "second leaf", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	f.Write(t, "certs/leaf-202711.pem", sign.EncodePEM(der))
	sel, err := s.Select(mustRequest(t, phoneHeaders(embedded, embedded)))
	if err != nil || !bytes.Equal(sel.Parts[1].Body, f.LeafPEM) {
		t.Fatalf("sent another leaf: %v", err)
	}

	// A .leaf naming no certificate is a store error, not a silent 204.
	f.Write(t, "updates/"+u.ID+"/manifest.leaf", []byte(strings.Repeat("0", 64)))
	if _, err := s.Select(mustRequest(t, phoneHeaders(embedded, embedded))); err == nil {
		t.Fatal("unknown leaf served")
	}
}

func TestSelectStoreErrors(t *testing.T) {
	f := otatest.New(t)
	s := &Store{Dir: f.Dir}
	embedded := otatest.NewUUID()
	f.RegisterRuntime(t, PlatformAndroid, testRuntime, embedded, 2)
	u := f.AddUpdate(t, PlatformIOS, testRuntime) // an iOS update ...
	f.Point(t, ChannelProduction, PlatformAndroid, testRuntime, StateUpdate, u.ID)
	req := mustRequest(t, phoneHeaders(embedded, embedded))
	if _, err := s.Select(req); err == nil || !strings.Contains(err.Error(), "is for ios") {
		t.Fatalf("... behind an Android pointer: %v", err)
	}

	f.Write(t, "channels/production/android/"+testRuntime+".json", []byte(`{"state":"maybe"}`))
	if _, err := s.Select(req); err == nil {
		t.Fatal("unknown pointer state accepted")
	}
	f.Write(t, "channels/production/android/"+testRuntime+".json", []byte(`{"state":"update","update":"../x"}`))
	if _, err := s.Select(req); err == nil {
		t.Fatal("pointer to a non-UUID accepted")
	}

	// A header-unsafe signature file is refused.
	good := f.AddUpdate(t, PlatformAndroid, testRuntime)
	f.Point(t, ChannelProduction, PlatformAndroid, testRuntime, StateUpdate, good.ID)
	f.Write(t, "updates/"+good.ID+"/manifest.sig", []byte(good.ManifestSig+"\r\nx-injected: 1\n"))
	if _, err := s.Select(req); err == nil {
		t.Fatal("a signature with a line break was accepted")
	}

	// No FORMAT: not initialized. Another FORMAT: an error.
	os.Remove(filepath.Join(f.Dir, "FORMAT"))
	if _, err := s.Select(req); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("missing FORMAT: %v", err)
	}
	f.Write(t, "FORMAT", []byte("2\n"))
	if _, err := s.Select(req); err == nil || errors.Is(err, ErrNotInitialized) {
		t.Fatalf("FORMAT 2: %v", err)
	}
}

func TestEncodeMultipartExactBytes(t *testing.T) {
	parts := []Part{
		{Name: "manifest", ContentType: "application/json; charset=utf-8", Signature: `sig="AAAA", keyid="root-2026", alg="rsa-v1_5-sha256"`, Body: []byte(`{"id":"1"}`)},
		{Name: "certificate_chain", ContentType: "application/x-pem-file", Body: []byte("-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n")},
	}
	body, ct, err := EncodeMultipart(parts, "b0undary")
	if err != nil {
		t.Fatal(err)
	}
	want := "--b0undary\r\n" +
		"content-disposition: form-data; name=\"manifest\"\r\n" +
		"content-type: application/json; charset=utf-8\r\n" +
		"expo-signature: sig=\"AAAA\", keyid=\"root-2026\", alg=\"rsa-v1_5-sha256\"\r\n" +
		"\r\n" +
		"{\"id\":\"1\"}\r\n" +
		"--b0undary\r\n" +
		"content-disposition: form-data; name=\"certificate_chain\"\r\n" +
		"content-type: application/x-pem-file\r\n" +
		"\r\n" +
		"-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n\r\n" +
		"--b0undary--\r\n"
	if string(body) != want {
		t.Fatalf("body:\n%q\nwant:\n%q", body, want)
	}
	if ct != "multipart/mixed; boundary=b0undary" {
		t.Fatalf("content type %q", ct)
	}
	got := decodeParts(t, body, ct)
	m := got["manifest"]
	if m == nil || m.Header.Get("Expo-Signature") != parts[0].Signature {
		t.Fatalf("decoded manifest part: %+v", m)
	}

	if _, _, err := EncodeMultipart([]Part{{Name: "manifest", Body: []byte("x--b0undary")}}, "b0undary"); err == nil {
		t.Fatal("a body containing the boundary was encoded")
	}
	if b := NewBoundary(); len(b) != 48 || b == NewBoundary() {
		t.Fatalf("boundary %q", b)
	}
}
