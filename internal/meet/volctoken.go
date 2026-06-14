package meet

// VolcEngine RTC AccessToken generation, ported from the reference
// implementations shipped in github.com/volcengine/VolcEngineRTC (the Java
// ByteBuf/AccessToken pair). Wire format:
//
//	token   = "001" + appID + base64std( pack(msg) + pack(signature) )
//	msg     = u32(nonce) u32(issuedAt) u32(expireAt) str(roomID) str(userID) privMap
//	str     = u16(len) bytes
//	privMap = u16(count) then (u16 key, u32 value) pairs in ascending key order
//	signature = HMAC-SHA256(appKey, msg), length-prefixed like str
//
// All integers are little-endian. The reference stores privileges in a
// TreeMap, so the sorted key order is part of the signed bytes.

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"sort"
	"time"
)

const volcTokenVersion = "001"

// Privilege ids, mirroring the reference enum. PrivPublishStream expands into
// the three sub-privileges when granted.
const (
	volcPrivPublishStream      uint16 = 0
	volcPrivPublishAudioStream uint16 = 1
	volcPrivPublishVideoStream uint16 = 2
	volcPrivPublishDataStream  uint16 = 3
	volcPrivSubscribeStream    uint16 = 4
)

type volcToken struct {
	appID      string
	appKey     string
	roomID     string
	userID     string
	issuedAt   uint32
	expireAt   uint32
	nonce      uint32
	privileges map[uint16]uint32
}

func newVolcToken(appID, appKey, roomID, userID string) *volcToken {
	return &volcToken{
		appID:      appID,
		appKey:     appKey,
		roomID:     roomID,
		userID:     userID,
		issuedAt:   uint32(time.Now().Unix()),
		nonce:      randomNonce(),
		privileges: map[uint16]uint32{},
	}
}

func randomNonce() uint32 {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return uint32(time.Now().UnixNano())
	}
	return binary.LittleEndian.Uint32(b[:])
}

// addPrivilege grants a privilege until expireTS (Unix seconds).
func (t *volcToken) addPrivilege(priv uint16, expireTS uint32) {
	t.privileges[priv] = expireTS
	if priv == volcPrivPublishStream {
		t.privileges[volcPrivPublishAudioStream] = expireTS
		t.privileges[volcPrivPublishVideoStream] = expireTS
		t.privileges[volcPrivPublishDataStream] = expireTS
	}
}

// expireTime sets the whole-token expiry (Unix seconds; 0 = never).
func (t *volcToken) expireTime(ts uint32) { t.expireAt = ts }

func (t *volcToken) packMsg() []byte {
	buf := new(bytes.Buffer)
	packUint32(buf, t.nonce)
	packUint32(buf, t.issuedAt)
	packUint32(buf, t.expireAt)
	packString(buf, t.roomID)
	packString(buf, t.userID)
	packPrivileges(buf, t.privileges)
	return buf.Bytes()
}

// serialize produces the final token string handed to the client SDK.
func (t *volcToken) serialize() string {
	msg := t.packMsg()
	mac := hmac.New(sha256.New, []byte(t.appKey))
	mac.Write(msg)
	sig := mac.Sum(nil)

	buf := new(bytes.Buffer)
	packBytes(buf, msg)
	packBytes(buf, sig)
	return volcTokenVersion + t.appID + base64.StdEncoding.EncodeToString(buf.Bytes())
}

// ---------- little-endian packing helpers ----------

func packUint16(buf *bytes.Buffer, v uint16) { _ = binary.Write(buf, binary.LittleEndian, v) }
func packUint32(buf *bytes.Buffer, v uint32) { _ = binary.Write(buf, binary.LittleEndian, v) }

func packBytes(buf *bytes.Buffer, b []byte) {
	packUint16(buf, uint16(len(b)))
	buf.Write(b)
}

func packString(buf *bytes.Buffer, s string) { packBytes(buf, []byte(s)) }

func packPrivileges(buf *bytes.Buffer, m map[uint16]uint32) {
	packUint16(buf, uint16(len(m)))
	keys := make([]uint16, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, k := range keys {
		packUint16(buf, k)
		packUint32(buf, m[k])
	}
}
