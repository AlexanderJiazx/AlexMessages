package ota

import (
	"reflect"
	"testing"
)

func TestParseSFVDictionary(t *testing.T) {
	members, err := parseSFVDictionary(`am_channel="beta", am_uid="42";p=1, flag, tok=beta, n=-12, d=1.5, b=:aGk=:, f=?0, il=("a" b);q`)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]any{}
	for _, m := range members {
		got[m.key] = m.item.value
	}
	want := map[string]any{
		"am_channel": "beta",
		"am_uid":     "42",
		"flag":       true,
		"tok":        sfvToken("beta"),
		"n":          int64(-12),
		"d":          1.5,
		"b":          []byte("hi"),
		"f":          false,
		"il":         []sfvItem{{value: "a"}, {value: sfvToken("b")}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v\nwant %#v", got, want)
	}
	if p := members[1].item.params; len(p) != 1 || p[0].key != "p" || p[0].value != int64(1) {
		t.Fatalf("params of am_uid = %#v", p)
	}
	strs, other := sfvStrings(members)
	if strs["am_channel"] != "beta" || strs["am_uid"] != "42" || len(other) != 7 {
		t.Fatalf("sfvStrings = %v, %v", strs, other)
	}
}

func TestParseSFVDictionaryDuplicateKeepsLastValue(t *testing.T) {
	members, err := parseSFVDictionary(`a="1", b="2", a="3"`)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 || members[0].key != "a" || members[0].item.value != "3" {
		t.Fatalf("got %#v", members)
	}
}

func TestParseSFVStringEscapes(t *testing.T) {
	members, err := parseSFVDictionary(`s="a\"b\\c"`)
	if err != nil {
		t.Fatal(err)
	}
	if members[0].item.value != `a"b\c` {
		t.Fatalf("got %q", members[0].item.value)
	}
}

func TestParseSFVList(t *testing.T) {
	items, err := parseSFVList(`"0f8b8f4e-1f6e-4d2a-9a51-3c8e2f0d9b11", "x",tok;a=1  `)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 || items[0].value != "0f8b8f4e-1f6e-4d2a-9a51-3c8e2f0d9b11" || items[2].value != sfvToken("tok") {
		t.Fatalf("got %#v", items)
	}
	if items, err := parseSFVList(""); err != nil || len(items) != 0 {
		t.Fatalf("empty list: %v %v", items, err)
	}
	// Parameters follow their item with no space.
	if _, err := parseSFVList(`tok ;a=1`); err == nil {
		t.Fatal("a space before a parameter was accepted")
	}
}

func TestParseSFVRejects(t *testing.T) {
	for _, in := range []string{
		`a="1",`,          // trailing comma
		`a="1" b="2"`,     // missing comma
		`a="unterminated`, // unterminated string
		`a="bad\escape"`,  // only \" and \\ escape
		`a="é"`,           // non-ASCII
		`A="1"`,           // keys are lowercase
		`a=1.2345`,        // at most 3 fractional digits
		`a=1234567890123456`,
		`a=?2`,
		`a=:not base64!:`,
		`a=("x"`,      // unterminated inner list
		`a=@`,         // not a bare item
		"a=\"x\x01\"", // control character
	} {
		if _, err := parseSFVDictionary(in); err == nil {
			t.Errorf("parseSFVDictionary(%q) succeeded", in)
		}
	}
}

func TestQuoteSFVString(t *testing.T) {
	q, err := QuoteSFVString(`a"b\c`)
	if err != nil || q != `"a\"b\\c"` {
		t.Fatalf("got %q, %v", q, err)
	}
	members, err := parseSFVDictionary("k=" + q)
	if err != nil || members[0].item.value != `a"b\c` {
		t.Fatalf("round trip: %#v %v", members, err)
	}
	if _, err := QuoteSFVString("line\nbreak"); err == nil {
		t.Fatal("newline accepted")
	}
}
