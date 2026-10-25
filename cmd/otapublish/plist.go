package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

// parsePlist reads a property list, binary (bplist00, what Xcode writes into
// an app) or XML, into Go values: map[string]any, []any, string, bool,
// int64, float64, []byte and time.Time.
func parsePlist(b []byte) (any, error) {
	if bytes.HasPrefix(b, []byte("bplist00")) {
		return parseBinaryPlist(b)
	}
	return parseXMLPlist(b)
}

// plistEpoch is the reference date of plist dates.
var plistEpoch = time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)

type bplist struct {
	b       []byte
	offsets []uint64
	refSize int
}

func parseBinaryPlist(b []byte) (any, error) {
	if len(b) < 8+32 {
		return nil, errors.New("bplist: too short")
	}
	tr := b[len(b)-32:]
	offSize, refSize := int(tr[6]), int(tr[7])
	n := binary.BigEndian.Uint64(tr[8:])
	top := binary.BigEndian.Uint64(tr[16:])
	table := binary.BigEndian.Uint64(tr[24:])
	if offSize < 1 || offSize > 8 || refSize < 1 || refSize > 8 || n == 0 || top >= n ||
		table >= uint64(len(b)) || n > (uint64(len(b))-table)/uint64(offSize) {
		return nil, errors.New("bplist: bad trailer")
	}
	p := &bplist{b: b[:len(b)-32], refSize: refSize, offsets: make([]uint64, n)}
	for i := range p.offsets {
		p.offsets[i] = beUint(b[table+uint64(i*offSize):], offSize)
	}
	return p.object(top, 0)
}

func beUint(b []byte, n int) uint64 {
	var v uint64
	for i := 0; i < n; i++ {
		v = v<<8 | uint64(b[i])
	}
	return v
}

func (p *bplist) at(i uint64, n int) ([]byte, error) {
	if i > uint64(len(p.b)) || uint64(n) > uint64(len(p.b))-i || n < 0 {
		return nil, errors.New("bplist: object out of range")
	}
	return p.b[i : i+uint64(n)], nil
}

// count reads the length of an object whose marker is at off: the low
// nibble, or an int object that follows when the nibble is 0xF. It returns
// the length and where the object's data starts.
func (p *bplist) count(off uint64) (int, uint64, error) {
	m := p.b[off]
	if m&0x0f != 0x0f {
		return int(m & 0x0f), off + 1, nil
	}
	h, err := p.at(off+1, 1)
	if err != nil {
		return 0, 0, err
	}
	if h[0]&0xf0 != 0x10 || h[0]&0x0f > 3 {
		return 0, 0, errors.New("bplist: bad length")
	}
	size := 1 << (h[0] & 0x0f)
	d, err := p.at(off+2, size)
	if err != nil {
		return 0, 0, err
	}
	v := beUint(d, size)
	if v > uint64(len(p.b)) {
		return 0, 0, errors.New("bplist: length out of range")
	}
	return int(v), off + 2 + uint64(size), nil
}

func (p *bplist) object(ref uint64, depth int) (any, error) {
	if depth > 32 {
		return nil, errors.New("bplist: nested too deeply")
	}
	if ref >= uint64(len(p.offsets)) || p.offsets[ref] >= uint64(len(p.b)) {
		return nil, errors.New("bplist: bad object reference")
	}
	off := p.offsets[ref]
	m := p.b[off]
	switch m >> 4 {
	case 0x0:
		switch m {
		case 0x08:
			return false, nil
		case 0x09:
			return true, nil
		}
		return nil, nil
	case 0x1:
		size := 1 << (m & 0x0f)
		d, err := p.at(off+1, size)
		if err != nil {
			return nil, err
		}
		if size > 8 { // a 128-bit integer: its low 64 bits
			d = d[size-8:]
		}
		// 1-, 2- and 4-byte integers are unsigned; 8-byte ones are signed.
		return int64(beUint(d, len(d))), nil
	case 0x2:
		size := 1 << (m & 0x0f)
		d, err := p.at(off+1, size)
		if err != nil {
			return nil, err
		}
		switch size {
		case 4:
			return float64(math.Float32frombits(binary.BigEndian.Uint32(d))), nil
		case 8:
			return math.Float64frombits(binary.BigEndian.Uint64(d)), nil
		}
		return nil, errors.New("bplist: bad real")
	case 0x3:
		d, err := p.at(off+1, 8)
		if err != nil {
			return nil, err
		}
		sec := math.Float64frombits(binary.BigEndian.Uint64(d))
		return plistEpoch.Add(time.Duration(sec * float64(time.Second))), nil
	case 0x4, 0x5, 0x6:
		n, start, err := p.count(off)
		if err != nil {
			return nil, err
		}
		if m>>4 == 0x6 {
			d, err := p.at(start, 2*n)
			if err != nil {
				return nil, err
			}
			units := make([]uint16, n)
			for i := range units {
				units[i] = binary.BigEndian.Uint16(d[2*i:])
			}
			return string(utf16.Decode(units)), nil
		}
		d, err := p.at(start, n)
		if err != nil {
			return nil, err
		}
		if m>>4 == 0x4 {
			return append([]byte(nil), d...), nil
		}
		return string(d), nil
	case 0x8:
		d, err := p.at(off+1, int(m&0x0f)+1)
		if err != nil {
			return nil, err
		}
		return int64(beUint(d, len(d))), nil
	case 0xA, 0xC:
		n, start, err := p.count(off)
		if err != nil {
			return nil, err
		}
		refs, err := p.refs(start, n)
		if err != nil {
			return nil, err
		}
		out := make([]any, n)
		for i, r := range refs {
			if out[i], err = p.object(r, depth+1); err != nil {
				return nil, err
			}
		}
		return out, nil
	case 0xD:
		n, start, err := p.count(off)
		if err != nil {
			return nil, err
		}
		refs, err := p.refs(start, 2*n)
		if err != nil {
			return nil, err
		}
		out := make(map[string]any, n)
		for i := 0; i < n; i++ {
			k, err := p.object(refs[i], depth+1)
			if err != nil {
				return nil, err
			}
			key, ok := k.(string)
			if !ok {
				return nil, errors.New("bplist: a dictionary key is not a string")
			}
			if out[key], err = p.object(refs[n+i], depth+1); err != nil {
				return nil, err
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("bplist: unknown marker 0x%02x", m)
}

func (p *bplist) refs(start uint64, n int) ([]uint64, error) {
	if n > len(p.b) {
		return nil, errors.New("bplist: count out of range")
	}
	d, err := p.at(start, n*p.refSize)
	if err != nil {
		return nil, err
	}
	out := make([]uint64, n)
	for i := range out {
		out[i] = beUint(d[i*p.refSize:], p.refSize)
	}
	return out, nil
}

func parseXMLPlist(b []byte) (any, error) {
	d := xml.NewDecoder(bytes.NewReader(b))
	for {
		tok, err := d.Token()
		if err != nil {
			return nil, fmt.Errorf("plist: %w", err)
		}
		if se, ok := tok.(xml.StartElement); ok {
			if se.Name.Local != "plist" {
				return nil, fmt.Errorf("plist: root element <%s>", se.Name.Local)
			}
			v, end, err := xmlPlistValue(d, 0)
			if err != nil {
				return nil, err
			}
			if _, isKey := v.(xmlKey); end || isKey {
				return nil, errors.New("plist: <plist> holds no value")
			}
			return v, nil
		}
	}
}

// xmlPlistValue reads the next value element. end is true when the parent
// element ended instead.
func xmlPlistValue(d *xml.Decoder, depth int) (v any, end bool, err error) {
	if depth > 32 {
		return nil, false, errors.New("plist: nested too deeply")
	}
	for {
		tok, err := d.Token()
		if err != nil {
			return nil, false, fmt.Errorf("plist: %w", err)
		}
		switch t := tok.(type) {
		case xml.EndElement:
			return nil, true, nil
		case xml.StartElement:
			v, err := xmlPlistElement(d, t.Name.Local, depth)
			return v, false, err
		}
	}
}

func xmlPlistElement(d *xml.Decoder, name string, depth int) (any, error) {
	switch name {
	case "dict":
		out := map[string]any{}
		for {
			k, end, err := xmlPlistValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			if end {
				return out, nil
			}
			key, ok := k.(xmlKey)
			if !ok {
				return nil, errors.New("plist: a <dict> entry does not start with <key>")
			}
			v, end, err := xmlPlistValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			if _, isKey := v.(xmlKey); end || isKey {
				return nil, fmt.Errorf("plist: key %q has no value", string(key))
			}
			out[string(key)] = v
		}
	case "array":
		out := []any{}
		for {
			v, end, err := xmlPlistValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			if end {
				return out, nil
			}
			if _, isKey := v.(xmlKey); isKey {
				return nil, errors.New("plist: a <key> outside a <dict>")
			}
			out = append(out, v)
		}
	case "true", "false":
		if err := d.Skip(); err != nil {
			return nil, err
		}
		return name == "true", nil
	}
	var text string
	if err := d.DecodeElement(&text, &xml.StartElement{Name: xml.Name{Local: name}}); err != nil {
		return nil, fmt.Errorf("plist: <%s>: %w", name, err)
	}
	switch name {
	case "key":
		return xmlKey(text), nil
	case "string":
		return text, nil
	case "integer":
		return strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	case "real":
		return strconv.ParseFloat(strings.TrimSpace(text), 64)
	case "date":
		return time.Parse(time.RFC3339, strings.TrimSpace(text))
	case "data":
		return base64.StdEncoding.DecodeString(strings.Join(strings.Fields(text), ""))
	}
	return nil, fmt.Errorf("plist: unknown element <%s>", name)
}

// xmlKey is a <key> of an XML plist; it is never a value.
type xmlKey string

// plistDict reads a property list whose top object is a dictionary.
func plistDict(r io.Reader) (map[string]any, error) {
	b, err := io.ReadAll(io.LimitReader(r, 4<<20))
	if err != nil {
		return nil, err
	}
	v, err := parsePlist(b)
	if err != nil {
		return nil, err
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("plist: the top object is not a dictionary")
	}
	return m, nil
}
