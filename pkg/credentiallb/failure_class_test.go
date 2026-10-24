package credentiallb

import (
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// ─────────────────────────────────────────────────────────────────────────────
// Per-class cooldown policy (layered credential fallback — Fix 1).
//
// Pins: rate-limit keeps the 60s/Retry-After shape byte-identical;
// quota seeds the LONG configurable per-class cooldown (default 15m),
// raised to an upstream Retry-After only when the upstream asked for
// LONGER; ExcludeAndReselectForClass seeds the class policy through the
// same B2/B3/C2 reselect machinery.
// ─────────────────────────────────────────────────────────────────────────────

func newClassPolicyEngine(t *testing.T) *Engine {
	t.Helper()
	e := NewEngine(time.Hour, time.Hour, 7, 60*time.Second)
	t.Cleanup(e.Stop)
	return e
}

func TestCooldownFor_RateLimitUnchanged(t *testing.T) {
	e := newClassPolicyEngine(t)

	// Upstream Retry-After present → used verbatim.
	if got := e.CooldownFor(FailureClassRateLimit, 7*time.Second); got != 7*time.Second {
		t.Errorf("rate-limit + Retry-After = %v, want 7s", got)
	}
	// Absent → engine default (constructor 60s).
	if got := e.CooldownFor(FailureClassRateLimit, 0); got != 60*time.Second {
		t.Errorf("rate-limit default = %v, want 60s", got)
	}
	// Nil engine → package default.
	var nilEngine *Engine
	if got := nilEngine.CooldownFor(FailureClassRateLimit, 0); got != DefaultCooldown {
		t.Errorf("nil-engine rate-limit default = %v, want %v", got, DefaultCooldown)
	}
}

func TestCooldownFor_QuotaPolicy(t *testing.T) {
	e := newClassPolicyEngine(t)
	e.SetQuotaCooldown(15 * time.Minute)

	// Absent Retry-After → the configured quota cooldown.
	if got := e.CooldownFor(FailureClassQuota, 0); got != 15*time.Minute {
		t.Errorf("quota default = %v, want 15m", got)
	}
	// Shorter upstream Retry-After does NOT undercut the quota floor.
	if got := e.CooldownFor(FailureClassQuota, 30*time.Second); got != 15*time.Minute {
		t.Errorf("quota + short Retry-After = %v, want 15m (configured value is the floor)", got)
	}
	// Longer upstream Retry-After is honored upward.
	if got := e.CooldownFor(FailureClassQuota, 2*time.Hour); got != 2*time.Hour {
		t.Errorf("quota + long Retry-After = %v, want 2h (upstream hint honored upward)", got)
	}
	// Nil engine → package default.
	var nilEngine *Engine
	if got := nilEngine.CooldownFor(FailureClassQuota, 0); got != DefaultQuotaCooldown {
		t.Errorf("nil-engine quota default = %v, want %v", got, DefaultQuotaCooldown)
	}
}

func TestSetQuotaCooldown_ResetSemantics(t *testing.T) {
	e := newClassPolicyEngine(t)

	// Unset → package default.
	if got := e.QuotaCooldown(); got != DefaultQuotaCooldown {
		t.Errorf("unset quota cooldown = %v, want %v", got, DefaultQuotaCooldown)
	}
	// Set → honored.
	e.SetQuotaCooldown(30 * time.Minute)
	if got := e.QuotaCooldown(); got != 30*time.Minute {
		t.Errorf("quota cooldown after set = %v, want 30m", got)
	}
	// Zero / negative reset to the package default (mis-wired config
	// degrades to sane behavior — same discipline as NewEngine).
	e.SetQuotaCooldown(0)
	if got := e.QuotaCooldown(); got != DefaultQuotaCooldown {
		t.Errorf("quota cooldown after zero-set = %v, want %v", got, DefaultQuotaCooldown)
	}
	e.SetQuotaCooldown(-1 * time.Minute)
	if got := e.QuotaCooldown(); got != DefaultQuotaCooldown {
		t.Errorf("quota cooldown after negative-set = %v, want %v", got, DefaultQuotaCooldown)
	}
	// Nil-safe.
	var nilEngine *Engine
	nilEngine.SetQuotaCooldown(time.Minute)
	if got := nilEngine.QuotaCooldown(); got != DefaultQuotaCooldown {
		t.Errorf("nil-engine QuotaCooldown = %v, want %v", got, DefaultQuotaCooldown)
	}
}

// TestSetQuotaCooldown_ClampBounds pins the defensive engine-seam
// clamp: the [1m, 24h] bounds are enforced by Validate() only on the
// config Save() path, so a value hand-edited into config.json / the DB
// config_json row (e.g. "30s") loads verbatim and would otherwise seed
// a sub-floor quota cooldown — silently below the documented 1m floor,
// in exactly the direction the feature cares about (quota cooldowns
// must be LONG, not short). The seam re-enforces the bounds: below 1m
// clamps UP to 1m, above 24h clamps DOWN to 24h, and 0/negative keep
// the "0 ⇒ engine default" semantics (unclamped, resets to the
// package default).
func TestSetQuotaCooldown_ClampBounds(t *testing.T) {
	e := newClassPolicyEngine(t)

	// Sub-floor hand-edited value → clamped UP to the 1m floor.
	e.SetQuotaCooldown(30 * time.Second)
	if got := e.QuotaCooldown(); got != time.Minute {
		t.Errorf("SetQuotaCooldown(30s) → QuotaCooldown = %v, want 1m (floor clamp)", got)
	}
	if got := e.CooldownFor(FailureClassQuota, 0); got < time.Minute {
		t.Errorf("CooldownFor(quota) after 30s set = %v, want ≥ 1m (the configured value is the floor)", got)
	}

	// Sub-floor by a hair → still clamped (boundary is inclusive 1m).
	e.SetQuotaCooldown(59 * time.Second)
	if got := e.QuotaCooldown(); got != time.Minute {
		t.Errorf("SetQuotaCooldown(59s) → QuotaCooldown = %v, want 1m (floor clamp)", got)
	}

	// The floor itself passes through unchanged.
	e.SetQuotaCooldown(time.Minute)
	if got := e.QuotaCooldown(); got != time.Minute {
		t.Errorf("SetQuotaCooldown(1m) → QuotaCooldown = %v, want 1m (exact floor honored)", got)
	}

	// Over-ceiling hand-edited value → clamped DOWN to 24h.
	e.SetQuotaCooldown(48 * time.Hour)
	if got := e.QuotaCooldown(); got != 24*time.Hour {
		t.Errorf("SetQuotaCooldown(48h) → QuotaCooldown = %v, want 24h (ceiling clamp)", got)
	}

	// In-bounds values still honored verbatim (clamp is defensive
	// only — it must not perturb legitimate tuning).
	e.SetQuotaCooldown(2 * time.Hour)
	if got := e.QuotaCooldown(); got != 2*time.Hour {
		t.Errorf("SetQuotaCooldown(2h) → QuotaCooldown = %v, want 2h (in-bounds verbatim)", got)
	}

	// "0 ⇒ engine default" semantics preserved (NOT clamped to 1m).
	e.SetQuotaCooldown(0)
	if got := e.QuotaCooldown(); got != DefaultQuotaCooldown {
		t.Errorf("SetQuotaCooldown(0) → QuotaCooldown = %v, want %v (zero ⇒ engine default)", got, DefaultQuotaCooldown)
	}
	e.SetQuotaCooldown(-1 * time.Second)
	if got := e.QuotaCooldown(); got != DefaultQuotaCooldown {
		t.Errorf("SetQuotaCooldown(-1s) → QuotaCooldown = %v, want %v (negative ⇒ engine default)", got, DefaultQuotaCooldown)
	}
}

// TestExcludeAndReselectForClass_QuotaSeedsLongCooldown pins the
// cooldown TRAP fix end-to-end at the engine: a quota-class exclusion
// seeds the QUOTA cooldown (not the 60s rate-limit default), so the
// exhausted credential stays out of the weighted pool for the class
// duration while a rate-limit exclusion under the same engine still
// seeds the short default.
func TestExcludeAndReselectForClass_QuotaSeedsLongCooldown(t *testing.T) {
	e := NewEngine(time.Hour, time.Hour, 11, 60*time.Second)
	t.Cleanup(e.Stop)
	e.SetQuotaCooldown(15 * time.Minute)
	ids := []string{"cred-A", "cred-B"}
	e.RebindFromStore("m1", models.TestRefs(ids...))

	// Quota-class exclusion of A: must mark A cooling for the QUOTA
	// duration and hand back the only healthy alternative.
	pick, mode := e.ExcludeAndReselectForClass("m1", "conv-quota-1", "cred-A", FailureClassQuota, 0)
	if mode != ReselectHealthy || pick != "cred-B" {
		t.Fatalf("quota reselect = (%q, %v), want (cred-B, ReselectHealthy)", pick, mode)
	}
	if until := e.cooldownUntil("m1", "cred-A"); time.Until(until) < 14*time.Minute {
		t.Errorf("quota exclusion seeded cooldown until %v — want ≈15m out (got %v remaining)", until, time.Until(until))
	}

	// Rate-limit exclusion under the SAME engine keeps the legacy
	// 60s/default policy.
	e2 := NewEngine(time.Hour, time.Hour, 11, 60*time.Second)
	t.Cleanup(e2.Stop)
	e2.RebindFromStore("m2", models.TestRefs(ids...))
	pick2, mode2 := e2.ExcludeAndReselect("m2", "conv-rl-1", "cred-A", 0)
	if mode2 != ReselectHealthy || pick2 != "cred-B" {
		t.Fatalf("rate-limit reselect = (%q, %v), want (cred-B, ReselectHealthy)", pick2, mode2)
	}
	if until := e2.cooldownUntil("m2", "cred-A"); time.Until(until) > DefaultCooldown {
		t.Errorf("rate-limit exclusion seeded cooldown until %v — want ≤ %v (legacy policy changed)", until, DefaultCooldown)
	}
}

// TestExcludeAndReselectForClass_QuotaCooldownExpiry pins the
// "topped-up credential recovers within the cooldown window" property:
// with a SHORT configured quota cooldown the excluded credential
// re-enters the pool after expiry (no permanent benching).
func TestExcludeAndReselectForClass_QuotaCooldownExpiry(t *testing.T) {
	e := NewEngine(time.Hour, time.Hour, 11, 50*time.Millisecond)
	t.Cleanup(e.Stop)
	// Seed a sub-floor cooldown directly on the engine (same-package
	// white-box write): the public seam clamps to the 1m floor since
	// the defensive-clamp fix, and this test exercises expiry/recovery
	// — not the seam — so it needs a fast window.
	e.mu.Lock()
	e.quotaCooldown = 80 * time.Millisecond
	e.mu.Unlock()
	ids := []string{"cred-A", "cred-B"}
	e.RebindFromStore("m1", models.TestRefs(ids...))

	// All cooling → soonest-expiry single attempt.
	pick, mode := e.ExcludeAndReselectForClass("m1", "", "cred-A", FailureClassQuota, 0)
	if mode != ReselectHealthy || pick != "cred-B" {
		t.Fatalf("first quota reselect = (%q, %v), want (cred-B, ReselectHealthy)", pick, mode)
	}
	// B is now the only healthy pick; excluding it puts the model
	// fully cooling (soonest-expiry path).
	_, mode2 := e.ExcludeAndReselectForClass("m1", "", "cred-B", FailureClassQuota, 0)
	if mode2 != ReselectSoonestExpiry {
		t.Fatalf("second quota reselect mode = %v, want ReselectSoonestExpiry (all cooling)", mode2)
	}
	// After the quota window elapses, both credentials are healthy
	// again — a fresh pick succeeds (recovery within the window).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if p, m := e.ExcludeAndReselectForClass("m1", "", "cred-A", FailureClassQuota, 0); m == ReselectHealthy && p != "" {
			return // recovered
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("credentials did not recover within the quota cooldown window")
}

// TestCooldownFor_NegativeRetryAfterClamped pins the defensive clamp:
// a negative Retry-After (mis-parsed header) is treated as absent.
func TestCooldownFor_NegativeRetryAfterClamped(t *testing.T) {
	e := newClassPolicyEngine(t)
	if got := e.CooldownFor(FailureClassRateLimit, -5*time.Second); got != 60*time.Second {
		t.Errorf("negative Retry-After (rate-limit) = %v, want 60s default", got)
	}
	if got := e.CooldownFor(FailureClassQuota, -5*time.Second); got != DefaultQuotaCooldown {
		t.Errorf("negative Retry-After (quota) = %v, want %v", got, DefaultQuotaCooldown)
	}
}
