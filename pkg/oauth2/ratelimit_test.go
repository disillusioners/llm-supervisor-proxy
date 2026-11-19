package oauth2

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimit_PerIP429(t *testing.T) {
	rl := newRateLimiter(1, 1)
	if !rl.allow("1.2.3.4") {
		t.Fatal("first should allow")
	}
	if rl.allow("1.2.3.4") {
		t.Fatal("second should be limited")
	}
}

func TestRateLimit_PerIPAllowsBurst(t *testing.T) {
	rl := newRateLimiter(10, 5)
	for i := 0; i < 5; i++ {
		if !rl.allow("1.2.3.4") {
			t.Fatalf("burst %d rejected", i)
		}
	}
}

// S5: the per-IP bucket map is bounded — a flood of distinct client IPs
// cannot grow it without limit.
func TestRateLimit_BucketMapIsBounded(t *testing.T) {
	rl := newRateLimiter(10, 20)
	for i := 0; i < rateLimiterMaxBuckets+500; i++ {
		rl.allow(fmt.Sprintf("10.%d.%d.%d", (i/65536)%256, (i/256)%256, i%256))
	}
	rl.mu.Lock()
	n := len(rl.buckets)
	rl.mu.Unlock()
	if n > rateLimiterMaxBuckets {
		t.Fatalf("the bucket map grew unbounded: %d > %d", n, rateLimiterMaxBuckets)
	}
}

// S5: idle buckets are swept, so the per-IP map cannot be used as a memory
// sink by an attacker rotating client IPs.
func TestRateLimit_IdleBucketsArePruned(t *testing.T) {
	rl := newRateLimiter(10, 20)
	rl.maxBuckets = 8
	rl.idleTTL = time.Minute
	rl.allow("1.1.1.1")
	rl.mu.Lock()
	rl.buckets["1.1.1.1"].last = time.Now().Add(-2 * time.Minute)
	rl.mu.Unlock()
	rl.allow("2.2.2.2")
	rl.mu.Lock()
	_, stale := rl.buckets["1.1.1.1"]
	n := len(rl.buckets)
	rl.mu.Unlock()
	if stale {
		t.Fatal("the idle bucket was not pruned")
	}
	if n > 1 {
		t.Fatalf("unexpected bucket count: %d", n)
	}
}

func TestHostPart(t *testing.T) {
	cases := map[string]string{
		"1.2.3.4:5678": "1.2.3.4",
		"127.0.0.1:80": "127.0.0.1",
		"[::1]:80":     "::1",
		"localhost":    "localhost",
	}
	for in, want := range cases {
		if got := hostPart(in); got != want {
			t.Fatalf("hostPart(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRateLimit_HTTP429RetryAfter(t *testing.T) {
	h := newTestHandler(t)
	// exhaust burst
	for i := 0; i < 20; i++ {
		h.limiter.allow("127.0.0.1")
	}
	req := httptest.NewRequest(http.MethodGet, "/oauth2/start", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	h.HandleStart(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("code: %d", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("missing Retry-After")
	}
}
