package auth

import (
	"strings"
	"unicode"
)

// CleanDisplayName makes a display name safe to show on one line: control
// characters and line breaks become spaces, bidi embeddings and overrides
// (which would reverse the text around the name) are dropped, runs of
// whitespace collapse to one space, and the result is capped at 40 runes. A
// name with nothing visible left (only spaces or zero-width characters)
// comes back empty, so the caller falls back to another name.
func CleanDisplayName(raw string) string {
	var b strings.Builder
	space := false
	for _, r := range raw {
		switch {
		case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069:
			continue
		case unicode.IsSpace(r) || unicode.IsControl(r):
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	name := b.String()
	if r := []rune(name); len(r) > 40 {
		name = string(r[:40])
	}
	name = strings.TrimSpace(name)
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) || unicode.IsMark(r) {
			return name
		}
	}
	return ""
}
