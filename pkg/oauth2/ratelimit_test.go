package oauth2

import (
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
