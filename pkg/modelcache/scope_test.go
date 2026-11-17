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
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
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
	// the observed throttle (it names the account's usage window)
	"you have reached your usage limit for this minute",
}

// unknownBodies carry NO account-subject evidence: the classifier CANNOT
// DISTINGUISH them. They must resolve to ScopeUnknown — a scope that
// NEVER marks — and they must never be REPORTED as account-attributed.
var unknownBodies = []string{
	"",
	// the bare reset banner — no subject at all
	"Unavailable (reset after 62h 45m 6s)",
	// the plain per-minute throttle — no account subject
	"rate limit exceeded, requests per minute",
	// a non-limit body
	"invalid api key",
	"upstream connection reset by peer",
	"internal server error",
}

// TestClassifyScope_ObservedBodiesAreAccountScoped pins the mint-scope
// of every OBSERVED upstream limit body: all of them are account-scoped.
//
// A regression here is the incident coming back: an account body
// classified as model-scoped would mint a model mark again and hide a
// healthy sibling credential.
func TestClassifyScope_ObservedBodiesAreAccountScoped(t *testing.T) {
	for _, body := range observedAccountBodies {
		if got := classifyScope(body); got != ScopeAccount {
			t.Errorf("classifyScope(%q) = %v, want ScopeAccount (an account body must never mint a model mark)", body, got)
		}
	}
}

// TestClassifyScope_UndistinguishableBodiesAreUnknown pins the third
// scope: a body with NO account-subject evidence is NOT account-
// attributed — it is UNKNOWN, and unknown NEVER marks.
//
// The distinction is load-bearing for the operator-facing log: a body
// with no account evidence must never be reported as "account-scoped".
func TestClassifyScope_UndistinguishableBodiesAreUnknown(t *testing.T) {
	for _, body := range unknownBodies {
		if got := classifyScope(body); got != ScopeUnknown {
			t.Errorf("classifyScope(%q) = %v, want ScopeUnknown (no account evidence → cannot tell)", body, got)
		}
	}
	// The unknown scope must never mark — the gate refuses it.
	c := New()
	scope, modelMarked := ApplyScopedWall(c, "m1", "upstream connection reset by peer", 30*time.Minute, time.Now())
	if scope != ScopeUnknown {
		t.Fatalf("scope = %v, want ScopeUnknown", scope)
	}
	if modelMarked {
		t.Fatal("an unknown-scope body minted a model mark — unknown must never mark")
	}
	if c.Marked("m1") {
		t.Fatal("the model layer holds a mark for an unknown-scope body")
	}
}

// TestApplyScopedWall_AccountScopedBodiesNeverMark is the mint-scope
// GATE pin: an account-scoped body mints NOTHING in the model layer —
// not with the knob window, not with a parsed reset.
func TestApplyScopedWall_AccountScopedBodiesNeverMark(t *testing.T) {
	now := time.Now()
	for _, body := range observedAccountBodies {
		c := New()
		scope, modelMarked := ApplyScopedWall(c, "m1", body, 30*time.Minute, now)
		if modelMarked {
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
	scope, modelMarked := ApplyScopedWall(c, "m1", "Usage limit reached for 5 hour "+marker, 30*time.Minute, now)
	if scope != ScopeModel {
		t.Fatalf("scope = %v, want ScopeModel", scope)
	}
	if !modelMarked {
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
	scope, modelMarked := ApplyScopedWall(c, "m1", body, 30*time.Minute, now)
	if scope != ScopeModel || !modelMarked {
		t.Fatalf("scope = %v, modelMarked = %v; want ScopeModel/true", scope, modelMarked)
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

// TestApplyScopedWall_ReportsTheRealPostState is the S-9 contract: the
// second return value is the MODEL LAYER's REAL post-state (is a model
// mark live for the key after the call) — NOT "did this call write a
// mark". A body the scope gate refuses must report the cache's actual
// state, and a pre-existing mark must be reported as live.
func TestApplyScopedWall_ReportsTheRealPostState(t *testing.T) {
	now := time.Now()
	// (a) refused body, no mark live → false.
	c := New()
	if _, modelMarked := ApplyScopedWall(c, "m1", observedAccountBodies[0], 30*time.Minute, now); modelMarked {
		t.Fatal("modelMarked = true with no mark live")
	}
	// (b) refused body, a mark ALREADY live → the real post-state is
	// true (the cache holds a model mark for the key).
	c.Mark("m1", 10*time.Minute)
	if _, modelMarked := ApplyScopedWall(c, "m1", observedAccountBodies[0], 30*time.Minute, now); !modelMarked {
		t.Fatal("modelMarked = false while a model mark is live — the return must be the cache's real post-state")
	}
}

// TestClassifyScope_MarkerContract guards the vocabulary invariants for
// the future: the ACCOUNT vocabulary is the broader subject ("your plan"
// covers a Token-Plan body AND a plan-window body) and therefore wins
// the tie, so no model marker may carry an account marker, and every
// model marker must classify as ScopeModel.
func TestClassifyScope_MarkerContract(t *testing.T) {
	if len(modelScopeMarkers) == 0 {
		t.Logf("modelScopeMarkers is EMPTY — the model layer mints nothing for the observed corpus (no observed model-scoped body). The ScopeModel branch below is exercised through the test seam only.")
	}
	for _, m := range modelScopeMarkers {
		if got := classifyScope(m); got != ScopeModel {
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
// vocabulary itself: every marker must classify a REALISTIC sentence
// that carries it as account-scoped.
//
// (The pre-review version matched the marker against ITSELF — a
// tautology that proved nothing about a real body.)
func TestClassifyScope_AccountMarkersAreAccountScoped(t *testing.T) {
	for _, a := range accountScopeMarkers {
		sentence := "The upstream rejected the request: " + a + " (see the provider dashboard)"
		if got := classifyScope(sentence); got != ScopeAccount {
			t.Errorf("classifyScope(%q) = %v, want ScopeAccount (the marker %q must carry the account scope)", sentence, got, a)
		}
	}
}

// TestScope_ClassVocabularyIsSubsetOfScopeVocabulary is the SUBSET
// INVARIANT (review S-1 / S-4):
//
//	the CLASS vocabulary decides the cooldown POLICY (60s vs 15m);
//	the SCOPE vocabulary decides whether a MODEL mark is minted.
//
// Every class token must be covered by the scope vocabulary — a body
// the class layer treats as an account quota must never be scoped as
// model-scoped (that would mint a model mark and hide a healthy sibling
// credential — the 2026-11-17 03:56:55Z incident).
//
// "Covered" means: a scope marker CONTAINS the class token, or the
// class token CONTAINS a scope marker (the vocabulary is matched by
// substring on the error text, so either direction covers the body).
func TestScope_ClassVocabularyIsSubsetOfScopeVocabulary(t *testing.T) {
	tokens := providers.QuotaVocabularyTokens()
	if len(tokens) == 0 {
		t.Fatal("the canonical quota-class vocabulary is empty — the classifier would never fire")
	}
	for _, token := range tokens {
		covered := false
		for _, marker := range accountScopeMarkers {
			if strings.Contains(token, marker) || strings.Contains(marker, token) {
				covered = true
				break
			}
		}
		if !covered {
			t.Errorf("class token %q is NOT covered by the scope vocabulary — a body carrying it could be scoped as model-scoped", token)
		}
		// The MEANINGFUL form of the same invariant: a body carrying
		// the class token must classify as account-scoped.
		body := "upstream error body: " + token + " (upstream provider said so)"
		if got := classifyScope(body); got != ScopeAccount {
			t.Errorf("classifyScope(%q) = %v, want ScopeAccount (the class token %q must carry the account scope)", body, got, token)
		}
	}
}

// TestApplyWall_NotExportedOutsidePackage is the S-7 TRIPWIRE: the
// wall-shape primitive (applyWall) is unexported, and NO file outside
// pkg/modelcache may call it (or the former exported name).
//
// The compiler already enforces the unexported call; this test pins
// the OTHER half — that the exported name is gone from the tree, so a
// future "just export it again" (which would re-open the 03:56:55Z
// bypass) fails a gate instead of shipping.
func TestApplyWall_NotExportedOutsidePackage(t *testing.T) {
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		if strings.HasPrefix(rel, filepath.Join("pkg", "modelcache")+string(filepath.Separator)) {
			return nil // the package itself owns the primitive
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		text := string(b)
		if strings.Contains(text, "modelcache.ApplyWall") {
			t.Errorf("%s calls the exported modelcache.ApplyWall — the mint-scope gate must be the only entry point", path)
		}
		if strings.Contains(text, "applyWall(") {
			t.Errorf("%s calls the wall-shape primitive applyWall( — it is package-private to pkg/modelcache", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
}

// TestScope_PlainPerMinuteBodyMatchesNoClassToken is the class-list
// precision pin (review S-1, PROBE7): the PLAIN per-minute throttle
// body must match NO class token — it is a rate limit (60s), never a
// quota (15m). The bare "usage limit" phrase was removed from the
// class vocabulary for exactly this reason.
func TestScope_PlainPerMinuteBodyMatchesNoClassToken(t *testing.T) {
	const perMinute = "you have reached your usage limit for this minute"
	if providers.IsQuotaBody("", "", perMinute) {
		t.Fatal("the plain per-minute body matched the quota class — it must stay a rate limit (60s)")
	}
	// It is also NOT an account-subject body: the scope layer cannot
	// attribute it to the account either.
	if got := classifyScope(perMinute); got == ScopeModel {
		t.Fatal("the plain per-minute body classified as model-scoped")
	}
}
