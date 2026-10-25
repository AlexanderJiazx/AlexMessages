// Package sign makes the OTA code-signing certificates and signatures
// (OTAUpdatePlan.md, D4): a self-signed root that is a certificate authority
// with pathlen:0, leaves for code signing issued by it, and expo-signature
// values over manifest and directive bytes.
//
// Only cmd/otapublish, which runs on the publisher's Mac, may import this
// package. The server binaries never sign: they serve bytes signed on the
// Mac, and a test fails if cmd/server, cmd/admin or cmd/meet import it.
// Verification lives in internal/ota.
package sign

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"time"
)

const (
	// Alg is the only signature algorithm expo-updates verifies.
	Alg = "rsa-v1_5-sha256"
	// RootValidity and LeafValidity count from a notBefore 24 hours in the
	// past, so a phone whose clock runs a little behind still accepts a new
	// certificate.
	RootValidity = 20 * 365 * 24 * time.Hour
	LeafValidity = 400 * 24 * time.Hour
	backdate     = 24 * time.Hour
	minKeyBits   = 2048
)

var keyIDRE = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// SignatureHeader signs body (RSA PKCS#1 v1.5 over its SHA-256) and returns
// the expo-signature value `sig="…", keyid="…", alg="rsa-v1_5-sha256"`. Every
// member is a string because the iOS client rejects any other member type.
func SignatureHeader(body []byte, key *rsa.PrivateKey, keyID string) (string, error) {
	if !keyIDRE.MatchString(keyID) {
		return "", fmt.Errorf("keyid %q must match %s", keyID, keyIDRE)
	}
	digest := sha256.Sum256(body)
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`sig="%s", keyid="%s", alg="%s"`, base64.StdEncoding.EncodeToString(sig), keyID, Alg), nil
}

// NewRoot returns the DER of a self-signed root certificate for key: a
// certificate authority with pathlen:0 and keyUsage keyCertSign, valid for
// RootValidity.
func NewRoot(key *rsa.PrivateKey, commonName string, now time.Time) ([]byte, error) {
	if err := checkKey(&key.PublicKey); err != nil {
		return nil, err
	}
	tmpl, err := template(&key.PublicKey, commonName, now, RootValidity)
	if err != nil {
		return nil, err
	}
	tmpl.IsCA = true
	tmpl.MaxPathLen = 0
	tmpl.MaxPathLenZero = true
	tmpl.KeyUsage = x509.KeyUsageCertSign
	return x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
}

// IssueLeaf returns the DER of a code-signing certificate for leafKey issued
// by root: keyUsage digitalSignature, extendedKeyUsage codeSigning, not a
// certificate authority, valid for LeafValidity (and never past the root).
func IssueLeaf(root *x509.Certificate, rootKey *rsa.PrivateKey, leafKey *rsa.PublicKey, commonName string, now time.Time) ([]byte, error) {
	if err := checkKey(leafKey); err != nil {
		return nil, err
	}
	if !root.IsCA {
		return nil, errors.New("the issuer is not a certificate authority")
	}
	tmpl, err := template(leafKey, commonName, now, LeafValidity)
	if err != nil {
		return nil, err
	}
	if tmpl.NotAfter.After(root.NotAfter) {
		tmpl.NotAfter = root.NotAfter
	}
	tmpl.KeyUsage = x509.KeyUsageDigitalSignature
	tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning}
	return x509.CreateCertificate(rand.Reader, tmpl, root, leafKey, rootKey)
}

// EncodePEM wraps certificate DER in a PEM block.
func EncodePEM(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func checkKey(pub *rsa.PublicKey) error {
	if pub.N.BitLen() < minKeyBits {
		return fmt.Errorf("RSA key of %d bits; at least %d are needed", pub.N.BitLen(), minKeyBits)
	}
	return nil
}

func template(pub *rsa.PublicKey, commonName string, now time.Time, validity time.Duration) (*x509.Certificate, error) {
	if commonName == "" {
		return nil, errors.New("empty common name")
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, err
	}
	// Subject key identifier: SHA-1 of the subjectPublicKey bits (RFC 5280
	// 4.2.1.2, method 1); for RSA those bits are the PKCS#1 public key.
	ski := sha1.Sum(x509.MarshalPKCS1PublicKey(pub))
	notBefore := now.Add(-backdate).UTC().Truncate(time.Second)
	return &x509.Certificate{
		SerialNumber:          serial.Add(serial, big.NewInt(1)),
		Subject:               pkix.Name{CommonName: commonName, Organization: []string{"Alex Messages"}},
		NotBefore:             notBefore,
		NotAfter:              notBefore.Add(validity),
		SubjectKeyId:          ski[:],
		BasicConstraintsValid: true,
		SignatureAlgorithm:    x509.SHA256WithRSA,
	}, nil
}
