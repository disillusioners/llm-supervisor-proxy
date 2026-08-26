package credentiallb

import (
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// TestEngine_Cooldown_BlocksUnrelatedFreshPick_PinLevel — Task 42
// (W7-1) pin-level companion to TestEngine_Cooldown_ExpiryRestores-
// Selectability (cooldown_test.go:17) and TestEngine_SkipCooling_
// RenormalizedDistribution (cooldown_test.go:662). Those pin the
// WHILE-COOLING half at scale (10k fresh keys never see cooling A)
// and the post-expiry half only via ExcludeAndReselect; the missing
// granularity is pinned HERE: POST-EXPIRY, fresh GetOrSelect picks
// from FRESH distinct keys re-include the formerly-cooling
// credential in the weighted distribution (>=1 of N picks).
func TestEngine_Cooldown_BlocksUnrelatedFreshPick_PinLevel(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour, 29, time.Hour)
	e.RebindFromStore("m", models.TestRefs("A", "B"))

	// Cool A via an UNRELATED conversation's failover (the "warming"
	// conversation; every pick key below is distinct from it).
	e.InjectPreconditionStateForTest("m", "warming", "A")
	e.ExcludeAndReselect("m", "warming", "A", 0)
	if !e.cooldownUntil("m", "A").After(time.Now()) {
		t.Fatal("precondition: A not cooling")
	}

	// Pre-expiry sanity: fresh distinct keys only ever get B.
	for i := 0; i < 50; i++ {
		if id, _, _ := e.GetOrSelect("m", "pre"+itoa(i)); id != "B" {
			t.Fatalf("cooling A returned to fresh pick %d: %s", i, id)
		}
	}

	// Post-expiry pin: A re-participates in fresh picks. Equal
	// weights [1,1] put zero-A probability at 2^-N over N picks;
	// the fixed seed makes the whole sequence deterministic.
	e.forceCooldownExpiryForTest("m", "A")
	const N = 200
	sawA := false
	for i := 0; i < N; i++ {
		id, _, err := e.GetOrSelect("m", "post"+itoa(i))
		if err != nil {
			t.Fatal(err)
		}
		switch id {
		case "A":
			sawA = true
		case "B":
		default:
			t.Fatalf("unexpected credential %q at post-expiry pick %d", id, i)
		}
	}
	if !sawA {
		t.Fatalf("formerly-cooling A never re-picked in %d fresh picks post-expiry", N)
	}
}

// TestEngine_BoundAtRefresh_OnInTTLHit — Task 44 (W7-3): the #10
// sliding-idle-TTL regression guard. (a) an in-TTL hit REFRESHES
// boundAt (strict increase) and ticks Hits; (b) total elapsed may
// pass the ORIGINAL TTL while the binding survives — expiry is
// measured from the LAST hit, not the first binding; (c) contrast —
// a binding with no interim hit expires after the full idle TTL and
// the next call freshly picks.
func TestEngine_BoundAtRefresh_OnInTTLHit(t *testing.T) {
	const ttl = time.Second
	e := newTestEngine(t, ttl, time.Hour, 31, time.Minute)
	e.RebindFromStore("m", models.TestRefs("A", "B"))

	// (a) Half the TTL elapsed -> in-TTL hit refreshes boundAt.
	e.InjectPreconditionStateForTest("m", "conv", "A")
	orig, ok := e.bindingBoundAtForTest("m", "conv")
	if !ok {
		t.Fatal("binding missing after inject")
	}
	time.Sleep(550 * time.Millisecond)
	if id, newly, err := e.GetOrSelect("m", "conv"); err != nil || newly || id != "A" {
		t.Fatalf("half-TTL hit: id=%s newly=%v err=%v want A/false/nil", id, newly, err)
	}
	if got := e.Stats()["m"].Hits; got != 1 {
		t.Fatalf("in-TTL hit did not tick Hits: %d", got)
	}
	refreshed, ok := e.bindingBoundAtForTest("m", "conv")
	if !ok {
		t.Fatal("binding vanished on in-TTL hit")
	}
	if !refreshed.After(orig) {
		t.Fatalf("boundAt not refreshed (sliding TTL broken): orig=%v refreshed=%v", orig, refreshed)
	}

	// (b) Another half-TTL: total elapsed > original TTL while
	// idle-since-last-hit < TTL -> STILL A.
	time.Sleep(550 * time.Millisecond)
	if id, newly, err := e.GetOrSelect("m", "conv"); err != nil || newly || id != "A" {
		t.Fatalf("post-original-TTL hit: id=%s newly=%v err=%v want A/false/nil (idle TTL must slide)", id, newly, err)
	}

	// (c) Contrast: a binding with NO interim hit expires after the
	// full idle TTL; the next call freshly picks (the stale
	// credential itself or the other one) instead of serving a hit.
	e.InjectPreconditionStateForTest("m", "idle", "B")
	time.Sleep(1100 * time.Millisecond) // > ttl, no interim hit
	id, newly, err := e.GetOrSelect("m", "idle")
	if err != nil || id == "" {
		t.Fatalf("idle-expiry pick: id=%q err=%v", id, err)
	}
	if !newly {
		t.Fatalf("idle-expired binding served as in-TTL hit: id=%s newly=%v (TTL did not fire)", id, newly)
	}
}
