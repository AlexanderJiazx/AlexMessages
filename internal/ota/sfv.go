package ota

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Structured Field Values (RFC 8941), the syntax of Expo-Extra-Params,
// Expo-Recent-Failed-Update-IDs, expo-expect-signature and expo-signature.
// Only parsing is needed: the server never writes a structured header of its
// own (expo-signature values are stored verbatim).

// sfvToken is a token bare item, kept apart from a string so that
// `am_channel=beta` (a token) is not mistaken for `am_channel="beta"`.
type sfvToken string

// sfvItem is a bare item or an inner list, with its parameters. value is a
// string, sfvToken, int64, float64, bool, []byte, or []sfvItem (inner list).
type sfvItem struct {
	value  any
	params []sfvParam
}

type sfvParam struct {
	key   string
	value any
}

type sfvMember struct {
	key  string
	item sfvItem
}

var errSFV = errors.New("malformed structured header")

type sfvParser struct {
	s string
	i int
}

// parseSFVDictionary parses an RFC 8941 dictionary. A key that appears twice
// keeps its first position and its last value, as the RFC says.
func parseSFVDictionary(s string) ([]sfvMember, error) {
	p := &sfvParser{s: s}
	if err := p.start(); err != nil {
		return nil, err
	}
	var members []sfvMember
	index := map[string]int{}
	for !p.done() {
		key, err := p.key()
		if err != nil {
			return nil, err
		}
		var item sfvItem
		if p.peek() == '=' {
			p.i++
			if item, err = p.itemOrInnerList(); err != nil {
				return nil, err
			}
		} else {
			params, err := p.parameters()
			if err != nil {
				return nil, err
			}
			item = sfvItem{value: true, params: params}
		}
		if at, ok := index[key]; ok {
			members[at].item = item
		} else {
			index[key] = len(members)
			members = append(members, sfvMember{key: key, item: item})
		}
		if err := p.next(); err != nil {
			return nil, err
		}
	}
	return members, nil
}

// parseSFVList parses an RFC 8941 list.
func parseSFVList(s string) ([]sfvItem, error) {
	p := &sfvParser{s: s}
	if err := p.start(); err != nil {
		return nil, err
	}
	var items []sfvItem
	for !p.done() {
		item, err := p.itemOrInnerList()
		if err != nil {
			return nil, err
		}
		items = append(items, item)
		if err := p.next(); err != nil {
			return nil, err
		}
	}
	return items, nil
}

// start rejects non-ASCII input and skips leading spaces.
func (p *sfvParser) start() error {
	for i := 0; i < len(p.s); i++ {
		if p.s[i] > 0x7e || (p.s[i] < 0x20 && p.s[i] != '\t') {
			return errSFV
		}
	}
	for p.i < len(p.s) && p.s[p.i] == ' ' {
		p.i++
	}
	// Trailing spaces are dropped here, so done() is true exactly at the end.
	p.s = strings.TrimRight(p.s, " ")
	return nil
}

func (p *sfvParser) done() bool { return p.i >= len(p.s) }

func (p *sfvParser) peek() byte {
	if p.done() {
		return 0
	}
	return p.s[p.i]
}

func (p *sfvParser) skipOWS() {
	for !p.done() && (p.s[p.i] == ' ' || p.s[p.i] == '\t') {
		p.i++
	}
}

// next consumes the comma between two members; a trailing comma is an error.
func (p *sfvParser) next() error {
	p.skipOWS()
	if p.done() {
		return nil
	}
	if p.s[p.i] != ',' {
		return errSFV
	}
	p.i++
	p.skipOWS()
	if p.done() {
		return errSFV
	}
	return nil
}

func (p *sfvParser) itemOrInnerList() (sfvItem, error) {
	if p.peek() != '(' {
		return p.item()
	}
	p.i++
	var list []sfvItem
	for !p.done() {
		for !p.done() && p.s[p.i] == ' ' {
			p.i++
		}
		if p.peek() == ')' {
			p.i++
			params, err := p.parameters()
			if err != nil {
				return sfvItem{}, err
			}
			return sfvItem{value: list, params: params}, nil
		}
		item, err := p.item()
		if err != nil {
			return sfvItem{}, err
		}
		list = append(list, item)
		if c := p.peek(); c != ' ' && c != ')' {
			return sfvItem{}, errSFV
		}
	}
	return sfvItem{}, errSFV
}

func (p *sfvParser) item() (sfvItem, error) {
	v, err := p.bareItem()
	if err != nil {
		return sfvItem{}, err
	}
	params, err := p.parameters()
	if err != nil {
		return sfvItem{}, err
	}
	return sfvItem{value: v, params: params}, nil
}

func (p *sfvParser) parameters() ([]sfvParam, error) {
	var params []sfvParam
	for p.peek() == ';' {
		p.i++
		for !p.done() && p.s[p.i] == ' ' {
			p.i++
		}
		key, err := p.key()
		if err != nil {
			return nil, err
		}
		var v any = true
		if p.peek() == '=' {
			p.i++
			if v, err = p.bareItem(); err != nil {
				return nil, err
			}
		}
		replaced := false
		for i := range params {
			if params[i].key == key {
				params[i].value, replaced = v, true
			}
		}
		if !replaced {
			params = append(params, sfvParam{key: key, value: v})
		}
	}
	return params, nil
}

func (p *sfvParser) key() (string, error) {
	start := p.i
	c := p.peek()
	if !(c >= 'a' && c <= 'z') && c != '*' {
		return "", errSFV
	}
	for !p.done() {
		c := p.s[p.i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-' || c == '.' || c == '*' {
			p.i++
			continue
		}
		break
	}
	return p.s[start:p.i], nil
}

func (p *sfvParser) bareItem() (any, error) {
	c := p.peek()
	switch {
	case c == '-' || (c >= '0' && c <= '9'):
		return p.number()
	case c == '"':
		return p.str()
	case c == '*' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
		return p.token(), nil
	case c == ':':
		return p.bytes()
	case c == '?':
		if p.i+1 < len(p.s) && (p.s[p.i+1] == '0' || p.s[p.i+1] == '1') {
			v := p.s[p.i+1] == '1'
			p.i += 2
			return v, nil
		}
	}
	return nil, errSFV
}

func (p *sfvParser) number() (any, error) {
	start := p.i
	if p.peek() == '-' {
		p.i++
	}
	if c := p.peek(); c < '0' || c > '9' {
		return nil, errSFV
	}
	digits, dot := 0, -1
	for !p.done() {
		c := p.s[p.i]
		if c >= '0' && c <= '9' {
			digits++
			p.i++
		} else if c == '.' && dot < 0 {
			if digits > 12 {
				return nil, errSFV
			}
			dot = digits
			p.i++
		} else {
			break
		}
		// 15 digits at most: an integer of 15, or a decimal of 16 with its dot.
		if digits > 15 {
			return nil, errSFV
		}
	}
	text := p.s[start:p.i]
	if dot < 0 {
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return nil, errSFV
		}
		return n, nil
	}
	if frac := digits - dot; frac < 1 || frac > 3 {
		return nil, errSFV
	}
	f, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return nil, errSFV
	}
	return f, nil
}

func (p *sfvParser) str() (string, error) {
	p.i++ // opening quote
	var b strings.Builder
	for !p.done() {
		c := p.s[p.i]
		p.i++
		switch {
		case c == '\\':
			if p.done() || (p.s[p.i] != '"' && p.s[p.i] != '\\') {
				return "", errSFV
			}
			b.WriteByte(p.s[p.i])
			p.i++
		case c == '"':
			return b.String(), nil
		case c < 0x20 || c > 0x7e:
			return "", errSFV
		default:
			b.WriteByte(c)
		}
	}
	return "", errSFV
}

func (p *sfvParser) token() sfvToken {
	start := p.i
	p.i++
	for !p.done() {
		c := p.s[p.i]
		if isTChar(c) || c == ':' || c == '/' {
			p.i++
			continue
		}
		break
	}
	return sfvToken(p.s[start:p.i])
}

func isTChar(c byte) bool {
	if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
		return true
	}
	return strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0
}

func (p *sfvParser) bytes() ([]byte, error) {
	p.i++ // opening colon
	end := strings.IndexByte(p.s[p.i:], ':')
	if end < 0 {
		return nil, errSFV
	}
	text := p.s[p.i : p.i+end]
	p.i += end + 1
	for i := 0; i < len(text); i++ {
		c := text[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '+' || c == '/' || c == '=') {
			return nil, errSFV
		}
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(text, "="))
	if err != nil {
		return nil, errSFV
	}
	return raw, nil
}

// sfvStrings returns the string-valued members of a dictionary, ignoring
// parameters. A member whose value is not a string is reported in other.
func sfvStrings(members []sfvMember) (strs map[string]string, other []string) {
	strs = map[string]string{}
	for _, m := range members {
		if s, ok := m.item.value.(string); ok {
			strs[m.key] = s
		} else {
			other = append(other, m.key)
		}
	}
	return strs, other
}

// QuoteSFVString serializes s as an RFC 8941 string. It fails for characters
// outside printable ASCII, which a structured string cannot hold.
func QuoteSFVString(s string) (string, error) {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c > 0x7e {
			return "", fmt.Errorf("character %q cannot appear in a structured string", c)
		}
		if c == '"' || c == '\\' {
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	b.WriteByte('"')
	return b.String(), nil
}
