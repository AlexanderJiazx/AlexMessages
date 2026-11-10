// Package voicecall is the Alex Meet server: Google Meet-style multi-party
// meetings on top of the shared Alex Messages account database. It serves the
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
// mutate under the lock and enqueue their messages while still holding it.
// Each socket has an ordered outbound queue drained by one writer goroutine,
// so every recipient sees messages in the order the room state changed, and
// a slow socket never blocks a handler (a full queue closes that socket).
package meet

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

// sendQueueSize bounds the frames waiting for one socket. A socket that falls
// this far behind is closed (its read loop then leaves the room), so a stalled
// client can never hold up a fan-out or grow memory without bound.
const sendQueueSize = 256

// The inbound frame budget of one socket (handleWS): frameBurst cost units
// at once, one more back every frameRefill. Variables so tests can shrink it.
var (
	frameBurst  = 400
	frameRefill = 10 * time.Millisecond
)

// conn wraps one WebSocket with an ordered outbound queue. send only enqueues
// (it never blocks, so callers may hold meetMu); one writer goroutine writes
// the frames in queue order, each under the writeWait deadline.
type conn struct {
	ws   *websocket.Conn
	out  chan outFrame
	done chan struct{} // closed by stop: the writer exits
	once sync.Once
	mu   sync.Mutex // guards dead
	dead bool       // the queue overflowed or a write failed: drop new frames
}

// outFrame is one queued payload. closeAfter closes the socket once every
// frame queued before it has been written.
type outFrame struct {
	payload    any
	closeAfter bool
}

func newConn(ws *websocket.Conn) *conn {
	c := &conn{ws: ws, out: make(chan outFrame, sendQueueSize), done: make(chan struct{})}
	go c.writeLoop()
	return c
}

func (c *conn) writeLoop() {
	for {
		select {
		case <-c.done:
			return
		case f := <-c.out:
			if f.closeAfter {
				c.kill()
				return
			}
			_ = c.ws.SetWriteDeadline(time.Now().Add(writeWait))
			if c.ws.WriteJSON(f.payload) != nil {
				c.kill()
				return
			}
		}
	}
}

// kill marks the socket dead and closes it, which ends its read loop.
func (c *conn) kill() {
	c.mu.Lock()
	c.dead = true
	c.mu.Unlock()
	_ = c.ws.Close()
}

func (c *conn) enqueue(f outFrame) bool {
	c.mu.Lock()
	if c.dead {
		c.mu.Unlock()
		return false
	}
	select {
	case c.out <- f:
		c.mu.Unlock()
		return true
	default:
		c.mu.Unlock()
		c.kill() // queue full: the client is too far behind to catch up
		return false
	}
}

// send queues one frame. Returns false if the socket is dead or its queue
// overflowed (the socket is then closed).
func (c *conn) send(payload any) bool {
	return c.enqueue(outFrame{payload: payload})
}

// closeAfterFlush closes the socket once the frames queued so far are written.
func (c *conn) closeAfterFlush() {
	if !c.enqueue(outFrame{closeAfter: true}) {
		_ = c.ws.Close()
	}
}

// stop ends the writer goroutine. Frames still queued are dropped.
func (c *conn) stop() {
	c.once.Do(func() { close(c.done) })
}

// participant is one connection inside a room. The same account joining from
// two tabs is two participants with distinct pids.
type participant struct {
	pid      int
	user     meetUser
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
	creatorID   int   // the account that created the room (caps live rooms per user)
	// removed holds the identities the host removed (host_kick): registered
	// user ids and per-tab client ids. A join by one of them is refused for
	// the rest of the room's life, so a removal holds across a reload or a
	// second device. A guest who also drops its tab id can come back; the
	// host's guest switch shuts guests out.
	removedUsers   map[int]bool
	removedClients map[string]bool
}

const (
	modeMesh = "mesh"
	modeVolc = "volc"

	// emptyRoomGraceSecs keeps an empty room joinable after the last
	// participant leaves (covers page refreshes of a solo participant).
	emptyRoomGraceSecs = 5 * 60
	// unusedRoomMaxAgeSecs prunes rooms that were created but never joined.
	unusedRoomMaxAgeSecs = 60 * 60
	// maxLiveRoomsPerUser caps the rooms one account has created that still
	// exist (occupied, or inside their grace period), so a client can't fill
	// memory by creating rooms in a loop.
	maxLiveRoomsPerUser = 20
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
