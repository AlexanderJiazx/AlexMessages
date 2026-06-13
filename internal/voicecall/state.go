// Package voicecall is the Alex Meet server: Google Meet-style multi-party
// meetings on top of the shared AlexMessage account database. It serves the
// lobby/meeting pages and runs the WebSocket control plane that every meeting
// participant stays connected to, regardless of which media backend the
// meeting uses:
//
//   - "mesh"  — standard WebRTC full mesh; the control plane relays opaque
//     SDP/ICE between participant pairs.
//   - "volc"  — VolcEngine RTC; media flows through the VolcEngine SDK and the
//     server only mints room tokens and keeps the roster/host state.
//
// Concurrency model: one goroutine per WebSocket read loop. A single mutex
// (meetMu) guards the room registry and all room/participant fields; handlers
// mutate under the lock, snapshot the recipients, then send after releasing
// it. Each socket carries its own write mutex so concurrent fan-outs never
// interleave a frame on the same connection.
package voicecall

import (
	"crypto/rand"
	"math/big"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"alexmessage/internal/db"
)

const (
	// pongWait bounds how long a socket may go silent before the read loop
	// gives up on it. The client pings every 20s, so a healthy connection
	// (even a throttled background tab) refreshes the deadline well inside
	// this window; a truly-dead socket self-prunes within pongWait.
	pongWait = 75 * time.Second
	// writeWait bounds a single frame write so a half-open socket can't wedge
	// a fan-out goroutine while it holds the per-connection write mutex.
	writeWait = 10 * time.Second
)

// conn wraps one WebSocket with a write mutex.
type conn struct {
	ws *websocket.Conn
	mu sync.Mutex
}

// send is a best-effort write. Returns false if the socket appears dead.
func (c *conn) send(payload any) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.ws.SetWriteDeadline(time.Now().Add(writeWait))
	return c.ws.WriteJSON(payload) == nil
}

// participant is one connection inside a room. The same account joining from
// two tabs is two participants with distinct pids.
type participant struct {
	pid      int
	user     vcUser
	c        *conn
	clientID string // per-tab id from the client; identifies reconnects/guests
	joinedAt int64
	muted    bool
	camOn    bool
	sharing  bool
}

// room is a live meeting. Rooms are in-memory only: they are created from the
// lobby, survive briefly while empty (so a refresh doesn't kill a meeting),
// and are pruned afterwards.
type room struct {
	code        string
	mode        string // "mesh" | "volc"
	hostPid     int    // 0 while nobody has joined yet
	parts       map[int]*participant
	order       []int // join order; the head inherits the host role
	createdAt   int64
	emptySince  int64 // 0 while occupied
	allowGuests bool  // host-toggled: non-registered users may join by name
}

const (
	modeMesh = "mesh"
	modeVolc = "volc"

	// emptyRoomGraceSecs keeps an empty room joinable after the last
	// participant leaves (covers page refreshes of a solo participant).
	emptyRoomGraceSecs = 5 * 60
	// unusedRoomMaxAgeSecs prunes rooms that were created but never joined.
	unusedRoomMaxAgeSecs = 60 * 60
)

var (
	meetMu   sync.Mutex // guards rooms, pidSeq, connPart and room/participant fields
	rooms    = map[string]*room{}
	pidSeq   int
	connRoom = map[*conn]*room{}
	connPart = map[*conn]*participant{}
)

// allocPid returns the next participant id. Caller MUST hold meetMu.
func allocPid() int {
	pidSeq++
	return pidSeq
}

// pruneRoomsLocked drops stale rooms. Caller MUST hold meetMu.
func pruneRoomsLocked() {
	now := db.NowTS()
	for code, rm := range rooms {
		if len(rm.parts) > 0 {
			continue
		}
		if rm.emptySince != 0 && now-rm.emptySince > emptyRoomGraceSecs {
			delete(rooms, code)
		} else if rm.emptySince == 0 && now-rm.createdAt > unusedRoomMaxAgeSecs {
			delete(rooms, code)
		}
	}
}

// roomChars deliberately omits i/l/o to keep codes unambiguous when spoken.
const roomChars = "abcdefghjkmnpqrstuvwxyz"

func randLetters(n int) string {
	out := make([]byte, n)
	max := big.NewInt(int64(len(roomChars)))
	for i := range out {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			out[i] = roomChars[0]
			continue
		}
		out[i] = roomChars[idx.Int64()]
	}
	return string(out)
}

// newRoomCode returns an unused xxx-xxxx-xxx code. Caller MUST hold meetMu.
func newRoomCode() string {
	for {
		code := randLetters(3) + "-" + randLetters(4) + "-" + randLetters(3)
		if _, taken := rooms[code]; !taken {
			return code
		}
	}
}

// participantPayload is the wire shape of one roster entry.
func participantPayload(p *participant) map[string]any {
	return map[string]any{
		"pid":     p.pid,
		"user":    p.user,
		"muted":   p.muted,
		"cam_on":  p.camOn,
		"sharing": p.sharing,
	}
}
