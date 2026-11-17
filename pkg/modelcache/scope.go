package modelcache

// ─────────────────────────────────────────────────────────────────────────────
// scope.go — the MINT-SCOPE decision (wi-4b8d21e7).
//
// A limit body mints a mark in ONE of three layers: MODEL, UPSTREAM or
// CREDENTIAL. This file owns the question "which of them?" for the
// MODEL-vs-CREDENTIAL choice; the layer that owns a mark never reads
// another layer's state (see the package contract in modelcache.go).
//
// THE RULE (user ruling 2026-11-17):
//
//	"a model need try all credential before declare quota limit reach.
//
// A model mark is the BROADEST mark in the system: it suppresses the
// model's ATTEMPT across ALL of its credentials. It is therefore earned
// ONLY by POSITIVE evidence that the wall's subject is the MODEL ITSELF
// — "a wall that would apply no matter which credential serves it".
//
// Everything else is CREDENTIAL-scoped: "the account ≈ the credential —
// a walled account is a walled credential, and the model's sibling
// credentials are unaffected". A body the classifier CANNOT DISTINGUISH
// resolves to ScopeUnknown — and ScopeUnknown NEVER marks (the narrower
// scope is the safe one: "when unsure, hide less"). A body with POSITIVE
// account evidence is ScopeAccount.
//
// The 2026-11-17 03:56:55Z incident was the broad mark hiding a healthy
// credential: a Token-Plan (ACCOUNT) body minted a MODEL mark, and the
// same request's credential-failover attempt was suppressed before it
// ever reached the healthy sibling account.
// ─────────────────────────────────────────────────────────────────────────────

import "strings"

// Scope enumerates the MINT-SCOPE of a classified upstream body.
//
// The ZERO value is ScopeUnknown — a zero-valued scope MUST NOT mark
// (the scope that hides least is the safe default).
type Scope int

const (
	// ScopeUnknown — the body carries NO account-subject evidence and NO
	// model-subject evidence: the classifier CANNOT DISTINGUISH it. It
	// resolves to the NARROWER scope (the credential) and NEVER marks
	// the model layer. It is distinct from ScopeAccount so the
	// operator-facing log never claims an attribution the classifier
	// did not make.
	ScopeUnknown Scope = iota
	// ScopeAccount — the body is attributable to the ACCOUNT (the
	// credential's own plan / credit / quota / usage window). Such a
	// body must mint at the CREDENTIAL/UPSTREAM layer and NEVER at the
	// model layer.
	ScopeAccount
	// ScopeModel — the body is attributable to the MODEL itself: the
	// wall applies no matter WHICH credential serves the request.
	ScopeModel
)

// String renders the scope for the operator-facing log lines. The
// values are stable vocabulary (the [MODEL-SKIP] log line carries them
// verbatim).
func (s Scope) String() string {
	switch s {
	case ScopeModel:
		return "model"
	case ScopeAccount:
		return "account"
	case ScopeUnknown:
		return "unknown"
	default:
		return "unknown"
	}
}

// accountScopeMarkers identify a body whose SUBJECT is the ACCOUNT —
// the credential's own plan / credit / quota / usage window.
//
// PROVENANCE (per marker, wi-4b8d21e7 review S-10):
//
//	observed — the marker is carried by a body in the corpus (a live
//	           upstream body or a test pin of one);
//	inferred — the marker is the ACCOUNT-SUBJECT reasoning applied to a
//	           family the corpus does not pin (e.g. every "your <x>"
//	           phrasing, where <x> is an account attribute).
//
// The list is deliberately BROAD: a false positive here only moves a
// body to the credential layer (the narrow, safe scope), while a false
// NEGATIVE would let a broad model mark hide a healthy sibling
// credential — the incident this file exists to prevent.
//
// SUBSET INVARIANT: every token of the CLASS vocabulary
// (providers.QuotaVocabularyTokens) must be covered by a marker here.
// The class vocabulary decides the COOLDOWN POLICY (60s vs 15m) and
// over-breadth there benches a healthy credential 15x; over-breadth here
// only narrows the scope. The invariant is pinned by
// TestScope_ClassVocabularyIsSubsetOfScopeVocabulary.
var accountScopeMarkers = []string{
	// ── the Token-Plan class (z.ai, codes 2056 / 2067) ──
	"token plan",             // observed: the 2056 / 2067 live bodies
	"purchase credit",        // observed: the 2056 live body
	"buy credit",             // observed: the 2067 live body
	"credits for more usage", // observed: the 2056 live body
	"auto consumption",       // observed: the 2067 live body
	"upgrade your",           // observed: both live bodies

	// ── the credit / balance / billing family ──
	"insufficient credit",  // observed: providers/interface_quota_test.go
	"insufficient credits", // inferred: the plural of the observed shape
	"insufficient quota",   // inferred: the plural/underscore family
	"insufficient balance", // inferred: the balance family
	"out of credit",        // inferred: the credit family
	"credit exhausted",     // inferred: the credit family
	"credits exhausted",    // inferred: the plural of the credit family
	"payment required",     // observed: providers/interface_quota_test.go
	"billing",              // observed: the OpenAI-shaped quota body
	// the STRUCTURED (type/code) spellings of the same families — the
	// scope layer matches the raw error text, which carries the
	// underscored spellings too.
	"insufficient_credit",  // observed: providers/interface_quota_test.go
	"insufficient_quota",   // observed: providers/interface_quota_test.go
	"insufficient_balance", // inferred: the balance family
	"payment_required",     // observed: the reviewer's drift probe
	"credit_exhausted",     // inferred: the credit family

	// ── the quota family ──
	"quota exceeded",      // observed: providers/interface_quota_test.go
	"quota exhausted",     // observed: pkg/proxy/race_reason_test.go
	"exceeded your quota", // inferred: the quota family
	"quota will reset",    // observed: the 9router live body
	"quota reset",         // inferred: the quota family
	"quota",               // observed: the 9router live body ("your quota")

	// ── the "your <account subject>" family ──
	// The subject of a limit body that says "your …" is the ACCOUNT
	// holder, never the model: the 5-hour plan window, the account's
	// quota, the account's exhausted capacity. Rotating to a sibling
	// credential (a sibling ACCOUNT) escapes every one of them.
	"your quota",              // observed: the 9router live body
	"your limit",              // observed: the z.ai 5-hour plan-window body
	"your limits",             // inferred: the plural of the observed shape
	"your plan",               // observed: the 2067 live body
	"your usage",              // inferred: the usage-window family
	"your credit",             // inferred: the credit family
	"your credits",            // inferred: the plural of the credit family
	"your balance",            // inferred: the balance family
	"your capacity",           // observed: the 9router live body
	"your account",            // inferred: the account family
	"exhausted your capacity", // observed: the 9router live body

	// ── the plan-window family (the account's own usage windows) ──
	// The v197 wall vocabulary carried this family as wall SHAPES; as a
	// SCOPE they are account subjects (the window belongs to the plan,
	// i.e. to the account that holds it), so they resolve to the
	// narrow scope here.
	"daily limit",   // inferred: the plan-window family
	"session limit", // inferred: the plan-window family
	"plan limit",    // inferred: the plan-window family
	"weekly limit",  // inferred: the plan-window family
	"week limit",    // inferred: the plan-window family

	// ── the 9router nested resource wall ──
	"resource_exhausted", // observed: pkg/imggen/ninerouter.go (the nested body)
}

// modelScopeMarkers identify a body whose SUBJECT is the MODEL itself:
// the wall would apply NO MATTER which credential serves the request.
//
// EVIDENCE (wi-4b8d21e7, Phase A audit 2026-11-17 + review S-10): the
// observed corpus contains NO model-scoped body. Every limit/quota body
// we have observed names the ACCOUNT ("your quota", "your limit", "your
// capacity", "Token Plan", credits, balance) or names NO subject at all
// ("Usage limit reached for 5 hour" — whose subject is the account's
// usage window). A body with no model subject is exactly the "cannot
// distinguish" case the ruling resolves to the NARROWER scope.
//
// ONE CANDIDATE, DELIBERATELY EXCLUDED: the translator's
// overloaded_error / HTTP 529 shape (pkg/proxy/translator/errors.go) —
// the only observed shape whose subject is the MODEL rather than the
// account. It is EXCLUDED on purpose: it is a TRANSLATOR-side
// synthesis (the proxy's own wording for an upstream overload), not an
// upstream account wall, and the upstream-layer demotion already covers
// it. Admitting it would put a model mark back on a body the corpus
// does not show to be model-scoped.
//
// The list is therefore EMPTY: the model layer mints nothing for the
// observed corpus. The layer itself stays wired — the TTL knob
// (model_skip_cache_ttl, default 30m), the [1m,24h] clamp, the
// never-shorten rule, the win-clear seam and both attempt-suppression
// read sites are untouched — so an OBSERVED model-scoped body can be
// added here without touching a mint site.
//
// This is the SAFE state. The model mark is the broadest mark in the
// system (it suppresses a model's attempt across ALL of its
// credentials), and the 2026-11-17 03:56:55Z incident was exactly that
// broad mark hiding a healthy sibling account.
var modelScopeMarkers = []string{}

// classifyScope returns the MINT-SCOPE of an upstream body.
//
// The order of the checks is the doctrine:
//  1. a body with POSITIVE ACCOUNT evidence classifies as ScopeAccount —
//     the account vocabulary is the broader subject ("your plan" covers
//     a Token-Plan body AND a plan-window body), so it wins the tie;
//  2. a body with POSITIVE MODEL evidence and no account evidence
//     classifies as ScopeModel;
//  3. everything else — the classifier CANNOT DISTINGUISH it — is
//     ScopeUnknown, which NEVER marks.
//
// Empty body → ScopeUnknown (never marks).
func classifyScope(msg string) Scope {
	if msg == "" {
		return ScopeUnknown
	}
	lower := strings.ToLower(msg)
	for _, a := range accountScopeMarkers {
		if strings.Contains(lower, a) {
			return ScopeAccount
		}
	}
	for _, m := range modelScopeMarkers {
		if strings.Contains(lower, m) {
			return ScopeModel
		}
	}
	// Cannot distinguish → hide less: ScopeUnknown never marks.
	return ScopeUnknown
}
