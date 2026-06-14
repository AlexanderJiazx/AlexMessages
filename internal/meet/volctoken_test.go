package meet

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"strings"
	"testing"
)

// TestVolcTokenGolden pins the serialized token against a value produced by an
// independent implementation of the reference algorithm (VolcEngineRTC's Java
// ByteBuf/AccessToken pair), so any packing/endianness regression fails loudly.
func TestVolcTokenGolden(t *testing.T) {
	tok := &volcToken{
		appID:      "6a2b39c655bc950177ce22c0",
		appKey:     "41353f9216a74e3fb1869164910dd5c6",
		roomID:     "abc-defg-hjk",
		userID:     "p42",
		issuedAt:   1750000000,
		nonce:      12345678,
		privileges: map[uint16]uint32{},
	}
	tok.expireTime(1750086400)
	tok.addPrivilege(volcPrivPublishStream, 1750086400)
	tok.addPrivilege(volcPrivSubscribeStream, 1750086400)

	const golden = "0016a2b39c655bc950177ce22c0PwBOYbwAgOFOaAAzUGgMAGFiYy1kZWZnLWhqawMAcDQyBQAAAAAzUGgBAAAzUGgCAAAzUGgDAAAzUGgEAAAzUGggAIkgziP0FrfKjp4xvrr7GYUB7sIBzYIabyjmRPE0PiZC"
	if got := tok.serialize(); got != golden {
		t.Fatalf("serialize mismatch:\n got %s\nwant %s", got, golden)
	}
}

// TestVolcTokenRoundTrip parses a freshly generated token back apart and
// verifies structure plus HMAC, mimicking the reference Parse/Verify pair.
func TestVolcTokenRoundTrip(t *testing.T) {
	appID := "6a2b39c655bc950177ce22c0"
	appKey := "41353f9216a74e3fb1869164910dd5c6"
	tok := newVolcToken(appID, appKey, "room-1", "p7")
	exp := tok.issuedAt + 86400
	tok.expireTime(exp)
	tok.addPrivilege(volcPrivPublishStream, exp)
	tok.addPrivilege(volcPrivSubscribeStream, exp)
	raw := tok.serialize()

	if !strings.HasPrefix(raw, volcTokenVersion+appID) {
		t.Fatalf("token missing version/appID prefix: %s", raw)
	}
	content, err := base64.StdEncoding.DecodeString(raw[len(volcTokenVersion)+len(appID):])
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}

	buf := bytes.NewReader(content)
	readBytes := func() []byte {
		var n uint16
		if err := binary.Read(buf, binary.LittleEndian, &n); err != nil {
			t.Fatalf("read length: %v", err)
		}
		b := make([]byte, n)
		if _, err := buf.Read(b); err != nil {
			t.Fatalf("read body: %v", err)
		}
		return b
	}
	msg := readBytes()
	sig := readBytes()
	if buf.Len() != 0 {
		t.Fatalf("trailing bytes after msg+signature: %d", buf.Len())
	}

	mac := hmac.New(sha256.New, []byte(appKey))
	mac.Write(msg)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		t.Fatal("HMAC signature does not verify")
	}

	// Parse msg fields back.
	mb := bytes.NewReader(msg)
	var nonce, issued, expire uint32
	_ = binary.Read(mb, binary.LittleEndian, &nonce)
	_ = binary.Read(mb, binary.LittleEndian, &issued)
	_ = binary.Read(mb, binary.LittleEndian, &expire)
	readStr := func() string {
		var n uint16
		_ = binary.Read(mb, binary.LittleEndian, &n)
		b := make([]byte, n)
		_, _ = mb.Read(b)
		return string(b)
	}
	if got := readStr(); got != "room-1" {
		t.Fatalf("roomID = %q", got)
	}
	if got := readStr(); got != "p7" {
		t.Fatalf("userID = %q", got)
	}
	var count uint16
	_ = binary.Read(mb, binary.LittleEndian, &count)
	if count != 5 { // 0..4: publish + its three sub-privileges + subscribe
		t.Fatalf("privilege count = %d, want 5", count)
	}
	prevKey := -1
	for i := 0; i < int(count); i++ {
		var k uint16
		var v uint32
		_ = binary.Read(mb, binary.LittleEndian, &k)
		_ = binary.Read(mb, binary.LittleEndian, &v)
		if int(k) <= prevKey {
			t.Fatalf("privilege keys not strictly ascending: %d after %d", k, prevKey)
		}
		prevKey = int(k)
		if v != exp {
			t.Fatalf("privilege %d expire = %d, want %d", k, v, exp)
		}
	}
	if mb.Len() != 0 {
		t.Fatalf("trailing bytes in msg: %d", mb.Len())
	}
	if expire != exp || issued != tok.issuedAt || nonce != tok.nonce {
		t.Fatalf("header fields mismatch: nonce=%d issued=%d expire=%d", nonce, issued, expire)
	}
}
