package modelcache

import (
	"testing"
	"time"
)

func TestCache_MarkMarkedClear(t *testing.T) {
	c := New()
	if c.Marked("m") {
		t.Fatal("fresh cache must be unmarked")
	}
	c.Mark("m", time.Minute)
	if !c.Marked("m") {
		t.Fatal("marked model must report Marked")
	}
	if _, ok := c.Until("m"); !ok {
		t.Fatal("Until must report the live expiry")
	}
	c.Clear("m")
	if c.Marked("m") {
		t.Fatal("Clear must remove the mark")
	}
}

func TestCache_NilAndZeroNoOps(t *testing.T) {
	var c *Cache
	if c.Marked("m") {
		t.Fatal("nil cache must be unmarked")
	}
	c.Mark("m", time.Minute) // nil receiver: no-op, must not panic
	c.Clear("m")
	if _, ok := c.Until("m"); ok {
		t.Fatal("nil cache Until must be false")
	}
	real := New()
	real.Mark("", time.Minute)
	real.Mark("m", 0)
	if _, ok := real.Until("m"); ok {
		t.Fatal("zero ttl must be a no-op")
	}
}

func TestCache_MarkUntilExtendsNotShortens(t *testing.T) {
	c := New()
	long := time.Now().Add(time.Hour)
	c.MarkUntil("m", long)
	c.MarkUntil("m", time.Now().Add(time.Minute)) // shorter — must not shorten
	until, ok := c.Until("m")
	if !ok || until.Before(long.Add(-time.Second)) {
		t.Fatalf("shorter MarkUntil shortened a live mark: until=%v want≈%v", until, long)
	}
}

func TestParseReset_Absolute(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	until, ok := ParseReset("Usage limit reached for 5 hour. Your limit will reset at 2026-11-16 18:13:33", now)
	if !ok {
		t.Fatal("absolute reset must parse")
	}
	want := time.Date(2026, 11, 16, 18, 13, 33, 0, time.UTC)
	if !until.Equal(want) {
		t.Fatalf("until = %v, want %v", until, want)
	}
}

func TestParseReset_Relative(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	until, ok := ParseReset("Unavailable (reset after 62h 45m 6s)", now)
	if !ok {
		t.Fatal("relative reset must parse")
	}
	want := now.Add(62*time.Hour + 45*time.Minute + 6*time.Second)
	if !until.Equal(want) {
		t.Fatalf("until = %v, want %v", until, want)
	}
	// Real 9router wire shape (pkg/imggen/ninerouter.go): no spaces.
	until2, ok2 := ParseReset("You have exhausted your capacity on this model. Your quota will reset after 4h39m9s.", now)
	if !ok2 {
		t.Fatal("compact relative reset (4h39m9s) must parse")
	}
	want2 := now.Add(4*time.Hour + 39*time.Minute + 9*time.Second)
	if !until2.Equal(want2) {
		t.Fatalf("until2 = %v, want %v", until2, want2)
	}
}

func TestParseReset_NoParse(t *testing.T) {
	now := time.Now()
	for _, msg := range []string{"plain rate limit", "reset at not-a-date", "", "reset after"} {
		if _, ok := ParseReset(msg, now); ok {
			t.Fatalf("ParseReset(%q) = ok, want false", msg)
		}
	}
}

func TestModelWall(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		msg          string
		attributable bool
		hasReset     bool
	}{
		{"Usage limit reached for 5 hour. Your limit will reset at 2026-11-16 18:13:33", true, true},
		{"Your weekly limit will reset at 2026-11-16 18:13:33", true, true},
		{"Token Plan usage limit", true, false},
		// D3: a bare account-quota reset is ACCOUNT-wide — it must NOT
		// mint a model mark (it stays credential-scoped upstream).
		{"You have exhausted your capacity on this model. Your quota will reset after 4h39m9s.", false, false},
		{"Unavailable (reset after 62h 45m 6s)", false, false},
		// fix round #1 / #4: MIXED bodies — a wall phrase plus account-
		// quota vocabulary is an account wall wearing a plan phrase; the
		// quota veto wins.
		{"Usage limit reached for 5 hour. Your quota will reset after 4h39m9s.", false, false},
		{"usage limit reached; quota exceeded; resets at 2026-11-16 18:13:33", false, false},
		{"you have reached your usage limit for this minute", false, false}, // throttle
		{"rate limit exceeded, requests per minute", false, false},          // throttle
		{"invalid api key", false, false},
		{"", false, false},
	}
	for _, tc := range cases {
		attr, until, hasReset := ModelWall(tc.msg, now)
		if attr != tc.attributable {
			t.Errorf("ModelWall(%q) attributable = %v, want %v", tc.msg, attr, tc.attributable)
		}
		if hasReset != tc.hasReset {
			t.Errorf("ModelWall(%q) hasReset = %v, want %v", tc.msg, hasReset, tc.hasReset)
		}
		if hasReset && !until.After(now) {
			t.Errorf("ModelWall(%q) reset %v is not in the future (now %v)", tc.msg, until, now)
		}
	}
}

// TestModelWall_AccountQuotaNotModelMarked is the D3 pin: the real
// 9router account-wide quota body and a bare reset banner must never
// classify as a model-attributable wall, while the genuine 5-hour plan
// wall must.
func TestModelWall_AccountQuotaNotModelMarked(t *testing.T) {
	now := time.Now()
	for _, quota := range []string{
		"You have exhausted your capacity on this model. Your quota will reset after 4h39m9s.",
		"Unavailable (reset after 62h 45m 6s)",
		"quota exceeded; resets at 2026-11-16 18:13:33",
		// fix round #1 / #4: mixed wall-phrase + quota-vocabulary bodies.
		"Usage limit reached for 5 hour. Your quota will reset after 4h39m9s.",
		"usage limit reached; quota exceeded; resets at 2026-11-16 18:13:33",
	} {
		if attr, _, _ := ModelWall(quota, now); attr {
			t.Fatalf("account-quota body %q was classified model-attributable (D3 over-breadth)", quota)
		}
	}
	if attr, _, _ := ModelWall("Usage limit reached for 5 hour. Your limit will reset at 2026-11-16 18:13:33", now); !attr {
		t.Fatal("genuine 5-hour usage wall must be model-attributable")
	}
}
