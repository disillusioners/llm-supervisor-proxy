package oauth2

import (
	"net/http"
	"net/http/httptest"
	"testing"
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
