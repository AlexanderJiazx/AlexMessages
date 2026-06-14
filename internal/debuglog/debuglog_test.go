package debuglog

import "testing"

func TestRateLimiterCapsBurst(t *testing.T) {
	limiterMu.Lock()
	limiters = map[string]*bucket{}
	limiterMu.Unlock()

	ip := "203.0.113.7"
	// A fresh bucket starts full; a burst larger than capacity is clamped.
	if got := allow(ip, bucketCapacity+50); got != bucketCapacity {
		t.Fatalf("first burst: want %d granted, got %d", bucketCapacity, got)
	}
	// The bucket is now drained, so an immediate follow-up is throttled.
	if got := allow(ip, 10); got != 0 {
		t.Fatalf("drained bucket should grant 0, got %d", got)
	}
	// A different IP has its own independent budget.
	if got := allow("198.51.100.1", 5); got != 5 {
		t.Fatalf("independent IP: want 5, got %d", got)
	}
}

func TestNormLevel(t *testing.T) {
	cases := map[string]string{
		"":       "debug",
		"log":    "debug",
		"LOG":    "debug",
		"info":   "info",
		"WARN":   "warn",
		" error": "error",
		"bogus":  "info",
	}
	for in, want := range cases {
		if got := normLevel(in); got != want {
			t.Errorf("normLevel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClampRunes(t *testing.T) {
	if got := clamp("héllo", 3); got != "hél" {
		t.Errorf("clamp multibyte: got %q", got)
	}
	if got := clamp("ok", 10); got != "ok" {
		t.Errorf("clamp short: got %q", got)
	}
}

func TestClampContextRejectsInvalidJSON(t *testing.T) {
	if got := clampContext([]byte("not json")); got != "" {
		t.Errorf("invalid JSON context should collapse to empty, got %q", got)
	}
	if got := clampContext([]byte(`{"a":1}`)); got != `{"a":1}` {
		t.Errorf("valid JSON should pass through, got %q", got)
	}
	if got := clampContext(nil); got != "" {
		t.Errorf("nil context should be empty, got %q", got)
	}
}
