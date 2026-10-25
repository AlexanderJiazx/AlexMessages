package ota

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// Part is one part of a multipart/mixed manifest response.
type Part struct {
	Name        string // manifest, directive or certificate_chain
	ContentType string
	Signature   string // the expo-signature part header; "" for none
	Body        []byte
}

// NewBoundary returns a random multipart boundary.
func NewBoundary() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return hex.EncodeToString(b)
}

// EncodeMultipart encodes parts as a multipart/mixed body (RFC 2046) and
// returns it with its Content-Type. Part header names are written in lower
// case on purpose: the iOS client looks up "expo-signature" case-sensitively
// (FileDownloader.swift), so Go's canonical "Expo-Signature" would read as an
// unsigned part there. It fails if a body contains the boundary.
func EncodeMultipart(parts []Part, boundary string) ([]byte, string, error) {
	delim := []byte("--" + boundary)
	var b bytes.Buffer
	for i, p := range parts {
		if bytes.Contains(p.Body, delim) {
			return nil, "", fmt.Errorf("part %q contains the boundary", p.Name)
		}
		if i > 0 {
			b.WriteString("\r\n")
		}
		b.Write(delim)
		b.WriteString("\r\n")
		fmt.Fprintf(&b, "content-disposition: form-data; name=%q\r\n", p.Name)
		fmt.Fprintf(&b, "content-type: %s\r\n", p.ContentType)
		if p.Signature != "" {
			fmt.Fprintf(&b, "expo-signature: %s\r\n", p.Signature)
		}
		b.WriteString("\r\n")
		b.Write(p.Body)
	}
	b.WriteString("\r\n")
	b.Write(delim)
	b.WriteString("--\r\n")
	return b.Bytes(), "multipart/mixed; boundary=" + boundary, nil
}
