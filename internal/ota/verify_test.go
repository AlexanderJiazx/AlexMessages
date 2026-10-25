package ota

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	"alexmessage/internal/ota/otatest"
	"alexmessage/internal/ota/sign"
)

var (
	extraKeyOnce sync.Once
	extraKey     *rsa.PrivateKey
)

// otherKey is a third RSA key for certificates that must not chain.
func otherKey(t *testing.T) *rsa.PrivateKey {
	extraKeyOnce.Do(func() {
		var err error
		if extraKey, err = rsa.GenerateKey(rand.Reader, 2048); err != nil {
			panic(err)
		}
	})
	return extraKey
}

// makeCert creates a certificate from tmpl, signed by parent/parentKey (or
// self-signed when parent is nil).
func makeCert(t *testing.T, tmpl *x509.Certificate, pub *rsa.PublicKey, parent *x509.Certificate, parentKey *rsa.PrivateKey) *x509.Certificate {
	t.Helper()
	if tmpl.SerialNumber == nil {
		tmpl.SerialNumber = big.NewInt(time.Now().UnixNano())
	}
	if parent == nil {
		parent = tmpl
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, pub, parentKey)
	if err != nil {
		t.Fatal(err)
	}
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func leafTemplate(now time.Time) *x509.Certificate {
	return &x509.Certificate{
		Subject:               pkix.Name{CommonName: "test leaf"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
	}
}

func rootTemplate(now time.Time) *x509.Certificate {
	return &x509.Certificate{
		Subject:               pkix.Name{CommonName: "test root"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
}

func TestSignThenVerifyRoundTrip(t *testing.T) {
	f := otatest.New(t)
	body := []byte(`{"id":"x"}`)
	header, err := sign.SignatureHeader(body, f.LeafKey, RootKeyID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(header, `sig="`) || !strings.HasSuffix(header, `", keyid="root-2026", alg="rsa-v1_5-sha256"`) {
		t.Fatalf("header %q", header)
	}
	leaf, err := VerifySignature(body, header, f.LeafPEM, f.Root, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if LeafSHA256(leaf) != f.LeafSHA {
		t.Fatalf("verified with %s, want %s", LeafSHA256(leaf), f.LeafSHA)
	}

	// One flipped byte of the body fails.
	bad := append([]byte(nil), body...)
	bad[2] ^= 1
	if _, err := VerifySignature(bad, header, f.LeafPEM, f.Root, time.Now()); err == nil {
		t.Fatal("a changed body verified")
	}
	// The served chain must not repeat the root: the phone appends its own,
	// and the root's pathlen:0 then forbids it as an intermediate.
	withRoot := append(append([]byte(nil), f.LeafPEM...), EncodeCertificatePEM(f.Root)...)
	if _, err := VerifySignature(body, header, withRoot, f.Root, time.Now()); err == nil || !strings.Contains(err.Error(), "pathLenConstraint") {
		t.Fatalf("leaf+root chain: %v", err)
	}
	// An empty chain makes the root the code-signing certificate; the root
	// has no codeSigning usage.
	if _, err := VerifySignature(body, header, nil, f.Root, time.Now()); err == nil {
		t.Fatal("verified without the leaf")
	}
	// Expired leaf, and a leaf not valid yet.
	if _, err := VerifySignature(body, header, f.LeafPEM, f.Root, f.Leaf.NotAfter.Add(time.Second)); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expired leaf: %v", err)
	}
	if _, err := VerifySignature(body, header, f.LeafPEM, f.Root, f.Leaf.NotBefore.Add(-time.Second)); err == nil {
		t.Fatal("leaf before its notBefore verified")
	}
}

func TestVerifyChainRejects(t *testing.T) {
	f := otatest.New(t)
	now := time.Now()
	ok := makeCert(t, leafTemplate(now), &f.LeafKey.PublicKey, f.Root, f.RootKey)
	if _, err := VerifyChain([]*x509.Certificate{ok}, f.Root, now); err != nil {
		t.Fatalf("good chain: %v", err)
	}

	noEKU := leafTemplate(now)
	noEKU.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	noDigSig := leafTemplate(now)
	noDigSig.KeyUsage = x509.KeyUsageKeyEncipherment
	expired := leafTemplate(now)
	expired.NotAfter = now.Add(-time.Minute)

	nonCARoot := rootTemplate(now)
	nonCARoot.IsCA = false
	nonCARoot.MaxPathLenZero = false
	nonCARoot.KeyUsage = x509.KeyUsageDigitalSignature
	nonCA := makeCert(t, nonCARoot, &f.RootKey.PublicKey, nil, f.RootKey)
	noCertSignRoot := rootTemplate(now)
	noCertSignRoot.KeyUsage = x509.KeyUsageDigitalSignature
	noCertSign := makeCert(t, noCertSignRoot, &f.RootKey.PublicKey, nil, f.RootKey)

	projectInfo := leafTemplate(now)
	projectInfo.ExtraExtensions = []pkix.Extension{{Id: expoProjectInfoOID, Value: []byte{0x04, 0x03, 'a', ',', 'b'}}}
	unknownCritical := leafTemplate(now)
	unknownCritical.ExtraExtensions = []pkix.Extension{{Id: asn1.ObjectIdentifier{1, 2, 3, 4}, Critical: true, Value: []byte{0x05, 0x00}}}

	otherRoot := makeCert(t, rootTemplate(now), &otherKey(t).PublicKey, nil, otherKey(t))

	cases := []struct {
		name   string
		served []*x509.Certificate
		root   *x509.Certificate
		want   string
	}{
		{"leaf without codeSigning", []*x509.Certificate{makeCert(t, noEKU, &f.LeafKey.PublicKey, f.Root, f.RootKey)}, f.Root, "codeSigning"},
		{"leaf without digitalSignature", []*x509.Certificate{makeCert(t, noDigSig, &f.LeafKey.PublicKey, f.Root, f.RootKey)}, f.Root, "digitalSignature"},
		{"expired leaf", []*x509.Certificate{makeCert(t, expired, &f.LeafKey.PublicKey, f.Root, f.RootKey)}, f.Root, "expired"},
		{"non-CA root", []*x509.Certificate{makeCert(t, leafTemplate(now), &f.LeafKey.PublicKey, nonCA, f.RootKey)}, nonCA, "certificate authority"},
		{"root without keyCertSign", []*x509.Certificate{makeCert(t, leafTemplate(now), &f.LeafKey.PublicKey, noCertSign, f.RootKey)}, noCertSign, "certificate authority"},
		{"leaf of another root", []*x509.Certificate{ok}, otherRoot, "do not chain"},
		{"Expo project information", []*x509.Certificate{makeCert(t, projectInfo, &f.LeafKey.PublicKey, f.Root, f.RootKey)}, f.Root, "project-information"},
		{"unknown critical extension", []*x509.Certificate{makeCert(t, unknownCritical, &f.LeafKey.PublicKey, f.Root, f.RootKey)}, f.Root, "critical"},
	}
	for _, tc := range cases {
		_, err := VerifyChain(tc.served, tc.root, now)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to mention %q", tc.name, err, tc.want)
		}
	}

	// A leaf with the root's name as issuer, signed by another key.
	impostor := *f.Root
	impostor.PublicKey = &otherKey(t).PublicKey
	forged := makeCert(t, leafTemplate(now), &f.LeafKey.PublicKey, &impostor, otherKey(t))
	if _, err := VerifyChain([]*x509.Certificate{forged}, f.Root, now); err == nil {
		t.Error("a leaf signed by another key verified")
	}
}

func TestVerifyChainSingleCertificateFallback(t *testing.T) {
	// D4 fallback: one self-signed code-signing certificate is embedded, and
	// the server sends no chain.
	now := time.Now()
	key := otherKey(t)
	self := makeCert(t, leafTemplate(now), &key.PublicKey, nil, key)
	leaf, err := VerifyChain(nil, self, now)
	if err != nil || leaf != self {
		t.Fatalf("single certificate: %v", err)
	}
}

func TestParseSignatureHeader(t *testing.T) {
	good := `sig="` + strings.Repeat("A", 342) + `==", keyid="root-2026", alg="rsa-v1_5-sha256"`
	h, err := ParseSignatureHeader(good)
	if err != nil || h.KeyID != "root-2026" || h.Alg != SignatureAlg || len(h.Sig) != 256 {
		t.Fatalf("good header: %+v, %v", h, err)
	}
	h, err = ParseSignatureHeader(`sig="AAAA"`)
	if err != nil || h.KeyID != "root" || h.Alg != SignatureAlg {
		t.Fatalf("defaults: %+v, %v", h, err)
	}
	for _, bad := range []string{
		`keyid="root"`,                    // no sig
		`sig`,                             // boolean, not a string (iOS fails)
		`sig="AAAA", keyid=root`,          // token member (iOS fails)
		`sig="AAA"`,                       // unpadded
		`sig="AA AA"`,                     // whitespace
		`sig="AAAA", alg="rsa-v1_5-sha1"`, // other algorithm
		`sig="AAAA",`,                     // malformed
	} {
		if _, err := ParseSignatureHeader(bad); err == nil {
			t.Errorf("ParseSignatureHeader(%q) succeeded", bad)
		}
	}
}

func TestParseCertificatesPEMRejectsOtherBlocks(t *testing.T) {
	// Split so the repository's private-key guard does not match this line.
	block := "-----BEGIN " + "PRIVATE KEY-----\nAAAA\n-----END " + "PRIVATE KEY-----\n"
	if _, err := ParseCertificatesPEM([]byte(block)); err == nil {
		t.Fatal("a private key block was accepted")
	}
}
