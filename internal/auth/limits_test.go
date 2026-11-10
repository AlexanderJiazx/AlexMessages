package auth

import (
	"fmt"
	"testing"
	"time"
)

func TestAttemptLimiterBurstRefillRefund(t *testing.T) {
	now := time.Unix(1000, 0)
	l := NewAttemptLimiter(10, 6*time.Second, 100)
	l.now = func() time.Time { return now }
	for i := 0; i < 10; i++ {
		if !l.Allow("203.0.113.1") {
			t.Fatalf("attempt %d refused within the burst", i+1)
		}
	}
	if l.Allow("203.0.113.1") {
		t.Fatal("11th attempt allowed")
	}
	if !l.Allow("203.0.113.2") {
		t.Fatal("another IP shares the bucket")
	}
	now = now.Add(6 * time.Second)
	if !l.Allow("203.0.113.1") || l.Allow("203.0.113.1") {
		t.Fatal("6 s should give back exactly one attempt")
	}
	l.Refund("203.0.113.1")
	if !l.Allow("203.0.113.1") {
		t.Fatal("refund did not give the attempt back")
	}
}

func TestAttemptLimiterBoundsKeys(t *testing.T) {
	l := NewAttemptLimiter(10, 6*time.Second, 64)
	for i := 0; i < 1000; i++ {
		l.Allow(fmt.Sprintf("10.0.%d.%d", i/256, i%256))
		if n := len(l.buckets); n > 64 {
			t.Fatalf("limiter holds %d keys, cap 64", n)
		}
	}
}

// With every scrypt slot taken, a verification waits until one frees up.
func TestPasswordHashingWaitsForSlot(t *testing.T) {
	hash, err := HashPassword("password123")
	if err != nil {
		t.Fatal(err)
	}
	releases := make([]func(), 0, MaxConcurrentHashes)
	for i := 0; i < MaxConcurrentHashes; i++ {
		releases = append(releases, acquireHashSlot())
	}
	done := make(chan bool, 1)
	go func() { done <- VerifyPassword("password123", hash) }()
	select {
	case <-done:
		t.Fatal("verification ran while every slot was taken")
	case <-time.After(300 * time.Millisecond):
	}
	releases[0]()
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("verification failed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("verification did not run after a slot was freed")
	}
	for _, r := range releases[1:] {
		r()
	}
}
