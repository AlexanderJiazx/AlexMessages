package auth

import (
	"sync"
	"time"
)

// ---------- scrypt concurrency ----------

// MaxConcurrentHashes is how many scrypt computations (HashPassword and
// VerifyPassword) run at once in this process. Each takes about 16 MB of
// memory and tens of milliseconds of CPU; further calls wait for a slot, so a
// flood of logins can't exhaust the machine's memory or CPU.
const MaxConcurrentHashes = 4

var hashSlots = make(chan struct{}, MaxConcurrentHashes)

// acquireHashSlot blocks until a scrypt slot is free and returns its release.
func acquireHashSlot() func() {
	hashSlots <- struct{}{}
	return func() { <-hashSlots }
}

// ---------- per-IP attempt limiter ----------

// TooManyAttemptsDetail is the 429 body detail for a refused attempt.
const TooManyAttemptsDetail = "Too many attempts. Try again in a minute."

// AttemptLimiter is a per-key token bucket: each key (a client IP) holds up
// to Burst tokens, one token comes back every Refill, and each attempt takes
// one token. At most maxKeys buckets are kept in memory.
type AttemptLimiter struct {
	Burst  int
	Refill time.Duration

	mu      sync.Mutex
	buckets map[string]*attemptBucket
	maxKeys int
	now     func() time.Time // replaced in tests
}

type attemptBucket struct {
	tokens float64
	last   time.Time
}

// NewAttemptLimiter returns a limiter with burst tokens per key, one token
// back every refill, and at most maxKeys keys kept in memory.
func NewAttemptLimiter(burst int, refill time.Duration, maxKeys int) *AttemptLimiter {
	return &AttemptLimiter{Burst: burst, Refill: refill, maxKeys: maxKeys, buckets: map[string]*attemptBucket{}, now: time.Now}
}

// LoginLimiter limits password attempts per client IP: POST /api/login and
// POST /api/register on the user app, and POST /api/login on the admin panel
// (each process has its own). 10 attempts at once, then one more every 6
// seconds. A login whose password is correct gives its token back (Refund),
// so only wrong passwords and registrations use up the budget.
var LoginLimiter = NewAttemptLimiter(10, 6*time.Second, 4096)

// Allow takes one token for key and reports whether one was available.
func (l *AttemptLimiter) Allow(key string) bool {
	return l.AllowN(key, 1)
}

// AllowN takes n tokens for key and reports whether n were available; it
// takes none when they were not.
func (l *AttemptLimiter) AllowN(key string, n float64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.refillLocked(key)
	if b.tokens < n {
		return false
	}
	b.tokens -= n
	return true
}

// Refund gives one token back to key (never above Burst).
func (l *AttemptLimiter) Refund(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.refillLocked(key)
	if b.tokens += 1; b.tokens > float64(l.Burst) {
		b.tokens = float64(l.Burst)
	}
}

// Reset forgets every key (all buckets full again).
func (l *AttemptLimiter) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buckets = map[string]*attemptBucket{}
}

// refillLocked returns key's bucket, created full when absent, with the
// tokens earned since it was last touched added. Caller holds l.mu.
func (l *AttemptLimiter) refillLocked(key string) *attemptBucket {
	now := l.now()
	b := l.buckets[key]
	if b == nil {
		if len(l.buckets) >= l.maxKeys {
			l.pruneLocked(now)
		}
		b = &attemptBucket{tokens: float64(l.Burst), last: now}
		l.buckets[key] = b
		return b
	}
	if l.Refill > 0 {
		b.tokens += float64(now.Sub(b.last)) / float64(l.Refill)
	}
	if b.tokens > float64(l.Burst) {
		b.tokens = float64(l.Burst)
	}
	b.last = now
	return b
}

// pruneLocked drops every bucket that has refilled completely (a new bucket
// for that key would be identical); if more than maxKeys/2 remain, it drops
// arbitrary buckets until maxKeys/2 remain. Caller holds l.mu.
func (l *AttemptLimiter) pruneLocked(now time.Time) {
	full := time.Duration(l.Burst) * l.Refill
	for k, b := range l.buckets {
		if now.Sub(b.last) >= full {
			delete(l.buckets, k)
		}
	}
	for k := range l.buckets {
		if len(l.buckets) <= l.maxKeys/2 {
			break
		}
		delete(l.buckets, k)
	}
}
