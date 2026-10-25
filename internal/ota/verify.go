package ota

import (
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"time"
)

// Code-signing checks, mirroring what expo-updates 57.0.26 does on the phone
// in chain mode (CertificateChain.kt, CodeSigningConfiguration.kt,
// SignatureHeaderInfo.swift and the iOS SecTrust basic X.509 policy). Where
// the Android and iOS clients differ, the stricter rule applies. A few checks
// go further than either client, because every certificate this project
// issues passes them: RSA keys of at least 2048 bits, RSA signatures with
// SHA-256/384/512 only, and no Expo project-information extension.
//
// otactl and otapublish run these checks before anything is served, so that a
// signature the phones would reject never reaches them.

// SignatureHeader is a parsed expo-signature header value.
type SignatureHeader struct {
	Sig   []byte
	KeyID string
	Alg   string
}

// expoProjectInfoOID marks a certificate as valid for one EAS project only.
var expoProjectInfoOID = asn1.ObjectIdentifier{1, 2, 840, 113556, 1, 8000, 2554, 43437, 254, 128, 102, 157, 7894389, 20439, 2, 1}

// iOS decodes the signature with Data(base64Encoded:), which needs padding and
// rejects whitespace.
var strictBase64RE = regexp.MustCompile(`^[A-Za-z0-9+/]*={0,2}$`)

// ParseSignatureHeader parses an expo-signature value the way both clients
// do: an RFC 8941 dictionary whose members are all strings (iOS fails on any
// other member), a required sig in padded base64, keyid defaulting to "root",
// and alg absent or rsa-v1_5-sha256.
func ParseSignatureHeader(value string) (*SignatureHeader, error) {
	members, err := parseSFVDictionary(value)
	if err != nil {
		return nil, fmt.Errorf("expo-signature: %w", err)
	}
	fields, other := sfvStrings(members)
	if len(other) > 0 {
		return nil, fmt.Errorf("expo-signature: member %q is not a string", other[0])
	}
	sig, ok := fields["sig"]
	if !ok {
		return nil, errors.New("expo-signature: sig is missing")
	}
	if sig == "" || len(sig)%4 != 0 || !strictBase64RE.MatchString(sig) {
		return nil, errors.New("expo-signature: sig is not padded base64")
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(sig)
	if err != nil {
		return nil, errors.New("expo-signature: sig is not padded base64")
	}
	h := &SignatureHeader{Sig: raw, KeyID: "root", Alg: SignatureAlg}
	if k, ok := fields["keyid"]; ok {
		h.KeyID = k
	}
	if a, ok := fields["alg"]; ok {
		if a != SignatureAlg {
			return nil, fmt.Errorf("expo-signature: unsupported alg %q", a)
		}
		h.Alg = a
	}
	return h, nil
}

// ParseCertificatesPEM returns every CERTIFICATE block in data, in order. Any
// other PEM block is an error. Text around the blocks is ignored, as the
// clients ignore it.
func ParseCertificatesPEM(data []byte) ([]*x509.Certificate, error) {
	var certs []*x509.Certificate
	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("unexpected PEM block %q", block.Type)
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		certs = append(certs, cert)
	}
	return certs, nil
}

// LeafSHA256 is the name a .leaf file gives a certificate: the lowercase hex
// SHA-256 of its DER bytes.
func LeafSHA256(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

// EncodeCertificatePEM is the certificate_chain part for one certificate.
func EncodeCertificatePEM(cert *x509.Certificate) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
}

// VerifyChain checks served (the certificate_chain part, leaf first) plus the
// embedded root the way a phone does, and returns the code-signing
// certificate. The phone appends its root to the served certificates, so a
// served chain that repeats the root fails the root's pathlen:0.
func VerifyChain(served []*x509.Certificate, root *x509.Certificate, now time.Time) (*x509.Certificate, error) {
	if root == nil {
		return nil, errors.New("no root certificate")
	}
	chain := append(slices.Clone(served), root)
	for i, c := range chain {
		if err := checkCertificate(c, now); err != nil {
			return nil, fmt.Errorf("certificate %d (%s): %w", i, c.Subject, err)
		}
	}
	for i := 0; i < len(chain)-1; i++ {
		child, parent := chain[i], chain[i+1]
		if !bytes.Equal(child.RawIssuer, parent.RawSubject) {
			return nil, fmt.Errorf("certificate %d is not issued by certificate %d: certificates do not chain", i, i+1)
		}
		if len(child.AuthorityKeyId) > 0 && len(parent.SubjectKeyId) > 0 && !bytes.Equal(child.AuthorityKeyId, parent.SubjectKeyId) {
			return nil, fmt.Errorf("certificate %d: authority key id does not match its issuer", i)
		}
		if err := parent.CheckSignature(child.SignatureAlgorithm, child.RawTBSCertificate, child.Signature); err != nil {
			return nil, fmt.Errorf("certificate %d: bad signature from its issuer: %w", i, err)
		}
	}
	if !bytes.Equal(root.RawIssuer, root.RawSubject) {
		return nil, errors.New("root certificate is not self-signed")
	}
	if err := root.CheckSignature(root.SignatureAlgorithm, root.RawTBSCertificate, root.Signature); err != nil {
		return nil, fmt.Errorf("root certificate: bad self-signature: %w", err)
	}
	if len(chain) > 1 {
		if !isCA(root) {
			return nil, errors.New("root certificate is not a certificate authority (needs CA:true and keyCertSign)")
		}
		maxPath := pathLen(root)
		for i := len(chain) - 2; i >= 1; i-- {
			if !isCA(chain[i]) {
				return nil, fmt.Errorf("certificate %d is not a certificate authority", i)
			}
			if maxPath <= 0 {
				return nil, fmt.Errorf("certificate %d violates the pathLenConstraint", i)
			}
			maxPath = min(maxPath-1, pathLen(chain[i]))
		}
	}
	leaf := chain[0]
	if leaf.KeyUsage&x509.KeyUsageDigitalSignature == 0 || !slices.Contains(leaf.ExtKeyUsage, x509.ExtKeyUsageCodeSigning) {
		return nil, errors.New("code-signing certificate needs keyUsage digitalSignature and extendedKeyUsage codeSigning")
	}
	return leaf, nil
}

// VerifySignature checks an expo-signature value over body with the
// certificates of a certificate_chain part (chainPEM) and the embedded root,
// and returns the certificate that signed it.
func VerifySignature(body []byte, signature string, chainPEM []byte, root *x509.Certificate, now time.Time) (*x509.Certificate, error) {
	h, err := ParseSignatureHeader(signature)
	if err != nil {
		return nil, err
	}
	served, err := ParseCertificatesPEM(chainPEM)
	if err != nil {
		return nil, fmt.Errorf("certificate_chain: %w", err)
	}
	leaf, err := VerifyChain(served, root, now)
	if err != nil {
		return nil, err
	}
	pub, ok := leaf.PublicKey.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("code-signing key is not RSA")
	}
	digest := sha256.Sum256(body)
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], h.Sig); err != nil {
		return nil, errors.New("signature does not match the body")
	}
	return leaf, nil
}

func checkCertificate(c *x509.Certificate, now time.Time) error {
	if now.Before(c.NotBefore) {
		return fmt.Errorf("not valid before %s", c.NotBefore.UTC().Format(time.RFC3339))
	}
	if now.After(c.NotAfter) {
		return fmt.Errorf("expired at %s", c.NotAfter.UTC().Format(time.RFC3339))
	}
	pub, ok := c.PublicKey.(*rsa.PublicKey)
	if !ok || pub.N.BitLen() < 2048 {
		return errors.New("key is not RSA of at least 2048 bits")
	}
	switch c.SignatureAlgorithm {
	case x509.SHA256WithRSA, x509.SHA384WithRSA, x509.SHA512WithRSA:
	default:
		return fmt.Errorf("signature algorithm %s is not allowed", c.SignatureAlgorithm)
	}
	if len(c.Subject.Names) == 0 {
		return errors.New("empty subject")
	}
	if len(c.UnhandledCriticalExtensions) > 0 {
		return fmt.Errorf("unknown critical extension %v", c.UnhandledCriticalExtensions[0])
	}
	for _, e := range c.Extensions {
		if e.Id.Equal(expoProjectInfoOID) {
			return errors.New("has the Expo project-information extension")
		}
	}
	return nil
}

func isCA(c *x509.Certificate) bool {
	return c.BasicConstraintsValid && c.IsCA && c.KeyUsage&x509.KeyUsageCertSign != 0
}

// pathLen is Java's getBasicConstraints() for a CA: the pathLenConstraint, or
// unlimited when it is absent.
func pathLen(c *x509.Certificate) int {
	if c.MaxPathLen < 0 || (c.MaxPathLen == 0 && !c.MaxPathLenZero) {
		return math.MaxInt
	}
	return c.MaxPathLen
}
