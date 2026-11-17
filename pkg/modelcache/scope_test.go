package modelcache

// ─────────────────────────────────────────────────────────────────────────────
// scope_test.go — the MINT-SCOPE pins (wi-4b8d21e7).
//
// The scope decision is the fix for the 2026-11-17 03:56:55Z incident:
// an ACCOUNT-scoped body (the Token-Plan class) minted a MODEL mark,
// and the same request's credential-failover attempt was suppressed at
// zero cost — one dry account hid the model's healthy sibling
// credential.
//
// The corpus pins below are the observed upstream bodies (Phase A
// audit, .agents/shared/planning/quota-mint-scope/phase-a-audit.md).
// Every one of them is ACCOUNT-scoped: the account ≈ the credential, so
// the model layer must never mint for them.
// ─────────────────────────────────────────────────────────────────────────────

import (
	"strings"
	"testing"
	"time"
)

// observedAccountBodies are the upstream limit bodies the corpus has
// actually observed. Each is ACCOUNT-scoped.
var observedAccountBodies = []string{
	// the observed Token-Plan class (z.ai, codes 2056 / 2067)
	"openai: Token Plan usage limit reached: Upgrade your Token Plan or purchase Credits for more usage. (2056)",
	"openai: The Token Plan usage limit has been reached. To avoid interruption, please upgrade your plan or buy credits and turn on auto consumption. (2067)",
	// the observed 9router account-quota body
	"You have exhausted your capacity on this model. Your quota will reset after 4h39m9s.",
	// the observed account plan-window wall ("your limit")
	"Usage limit reached for 5 hour. Your limit will reset at 2026-11-16 18:13:33",
	"Your weekly limit will reset at 2026-11-16 18:13:33",
	// the observed throttle + the bare reset banner
	"you have reached your usage limit for this minute",
	"rate limit exceeded, requests per minute",
	"Unavailable (reset after 62h 45m 6s)",
	// non-limit bodies
	"invalid api key",
	"",
}

// TestClassifyScope_ObservedBodiesAreAccountScoped pins the mint-scope
// of every OBSERVED upstream limit body: all of them are account-scoped.
//
// A regression here is the incident coming back: an account body
// classified as model-scoped would mint a model mark again and hide a
// healthy sibling credential.
func TestClassifyScope_ObservedBodiesAreAccountScoped(t *testing.T) {
	for _, body := range observedAccountBodies {
		if got := ClassifyScope(body); got != ScopeAccount {
			t.Errorf("ClassifyScope(%q) = %v, want ScopeAccount (an account body must never mint a model mark)", body, got)
		}
	}
}

// TestApplyScopedWall_AccountScopedBodiesNeverMark is the mint-scope
// GATE pin: an account-scoped body mints NOTHING in the model layer —
// not with the knob window, not with a parsed reset.
func TestApplyScopedWall_AccountScopedBodiesNeverMark(t *testing.T) {
	now := time.Now()
	for _, body := range observedAccountBodies {
		c := New()
		scope, marked := ApplyScopedWall(c, "m1", body, 30*time.Minute, now)
		if marked {
			t.Errorf("ApplyScopedWall(%q) marked the model layer, want no mark (account body)", body)
		}
		if scope != ScopeAccount {
			t.Errorf("ApplyScopedWall(%q) scope = %v, want ScopeAccount", body, scope)
		}
		if c.Marked("m1") {
			t.Errorf("ApplyScopedWall(%q) left a live model mark", body)
		}
	}
}

// TestApplyScopedWall_ModelScopedBodyMarks proves the model layer's
// machinery is INTACT (requirement 5: the knob cap, the clamp and the
// mark path all survive the scope change) — the production vocabulary
// is empty because no model-scoped body has been OBSERVED yet, so the
// branch is exercised through the package's test seam.
func TestApplyScopedWall_ModelScopedBodyMarks(t *testing.T) {
	const marker = "zz-synthetic-model-scope-marker-zz"
	restore := InjectModelScopeMarkerForTest(marker)
	defer restore()

	now := time.Now()
	c := New()
	scope, marked := ApplyScopedWall(c, "m1", "Usage limit reached for 5 hour "+marker, 30*time.Minute, now)
	if scope != ScopeModel {
		t.Fatalf("scope = %v, want ScopeModel", scope)
	}
	if !marked {
		t.Fatal("a model-scoped body must mint a model mark")
	}
	until, ok := c.Until("m1")
	if !ok {
		t.Fatal("expected a live model mark")
	}
	if d := until.Sub(now); d < 29*time.Minute || d > 31*time.Minute {
		t.Fatalf("mark window = %v, want ~30m (the configured knob)", d)
	}
}

// TestApplyScopedWall_ModelScopedBodyRespectsKnobCap is the v197 knob-cap
// pin THROUGH the scoped entry point: a parsed reset window longer than
// the configured knob is capped at the knob (never longer).
func TestApplyScopedWall_ModelScopedBodyRespectsKnobCap(t *testing.T) {
	const marker = "zz-synthetic-model-scope-marker-zz"
	restore := InjectModelScopeMarkerForTest(marker)
	defer restore()

	now := time.Now().UTC().Truncate(time.Second)
	c := New()
	body := "Usage limit reached for 5 hour " + marker + ". Reset at " + now.Add(5*time.Hour).Format("2006-01-02 15:04:05")
	scope, marked := ApplyScopedWall(c, "m1", body, 30*time.Minute, now)
	if scope != ScopeModel || !marked {
		t.Fatalf("scope = %v, marked = %v; want ScopeModel/true", scope, marked)
	}
	until, ok := c.Until("m1")
	if !ok {
		t.Fatal("expected a live model mark")
	}
	want := now.Add(30 * time.Minute)
	if !until.Equal(want) {
		t.Fatalf("until = %v, want %v (the knob caps the parsed reset)", until, want)
	}
}

// TestClassifyScope_MarkerContract guards the vocabulary invariants for
// the future: the ACCOUNT vocabulary is the broader subject ("your plan"
// covers a Token-Plan body AND a plan-window body) and therefore wins
// the tie, so no model marker may carry an account marker, and every
// model marker must classify as ScopeModel.
func TestClassifyScope_MarkerContract(t *testing.T) {
	for _, m := range modelScopeMarkers {
		if got := ClassifyScope(m); got != ScopeModel {
			t.Errorf("model marker %q classifies as %v, want ScopeModel", m, got)
		}
		for _, a := range accountScopeMarkers {
			if strings.Contains(strings.ToLower(m), a) {
				t.Errorf("model marker %q also carries the account marker %q — the account vocabulary must win the tie", m, a)
			}
		}
	}
}

// TestClassifyScope_AccountMarkersAreAccountScoped pins the account
// vocabulary itself: every marker classifies as account-scoped (the
// marker list IS the account-subject contract).
func TestClassifyScope_AccountMarkersAreAccountScoped(t *testing.T) {
	for _, a := range accountScopeMarkers {
		if got := ClassifyScope(a); got != ScopeAccount {
			t.Errorf("account marker %q classifies as %v, want ScopeAccount", a, got)
		}
	}
}
