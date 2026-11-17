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
// resolves to the NARROWER scope ("when unsure, hide less"): the
// credential.
//
// The 2026-11-17 03:56:55Z incident was the broad mark hiding a healthy
// credential: a Token-Plan (ACCOUNT) body minted a MODEL mark, and the
// same request's credential-failover attempt was suppressed before it
// ever reached the healthy sibling account.
// ─────────────────────────────────────────────────────────────────────────────

import "strings"

// Scope enumerates the MINT-SCOPE of a classified upstream body.
type Scope int

const (
	// ScopeAccount — the body is attributable to the ACCOUNT (the
	// credential's own plan / credit / quota / usage window). Such a
	// body must mint at the CREDENTIAL/UPSTREAM layer and NEVER at the
	// model layer.
	ScopeAccount Scope = iota
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
	default:
		return "unknown"
	}
}

// accountScopeMarkers identify a body whose SUBJECT is the ACCOUNT —
// the credential's own plan / credit / quota / usage window. Every
// marker below is backed by an OBSERVED body (wi-4b8d21e7 Phase A audit,
// .agents/shared/planning/quota-mint-scope/phase-a-audit.md):
//
//	"Token Plan usage limit reached: Upgrade your Token Plan or
//	 purchase Credits for more usage. (2056)"      — z.ai, code 2056
//	"The Token Plan usage limit has been reached. To avoid interruption,
//	 please upgrade your plan or buy credits and turn on auto
//	 consumption. (2067)"                          — z.ai, code 2067
//	"You have exhausted your capacity on this model. Your quota will
//	 reset after 4h39m9s."                         — 9router live body
//	"Usage limit reached for 5 hour. Your limit will reset at …" — the
//	 account's 5-hour usage window ("your limit")
//
// The list is deliberately BROAD: a false positive here only moves a
// body to the credential layer (the narrow, safe scope), while a false
// NEGATIVE would let a broad model mark hide a healthy sibling
// credential — the incident this file exists to prevent.
var accountScopeMarkers = []string{
	// ── the Token-Plan class (the observed 2056/2067 bodies) ──
	"token plan",
	"purchase credit",
	"buy credit",
	"credits for more usage",
	"auto consumption",
	"upgrade your",
	// ── the credit / balance / billing family ──
	"insufficient credit",
	"insufficient credits",
	"insufficient quota",
	"insufficient balance",
	"out of credit",
	"credit exhausted",
	"credits exhausted",
	"payment required",
	"billing",
	// ── the quota family ──
	"quota exceeded",
	"quota exhausted",
	"exceeded your quota",
	"quota will reset",
	"quota reset",
	// ── the "your <account subject>" family ──
	// The subject of a limit body that says "your …" is the ACCOUNT
	// holder, never the model: the 5-hour plan window, the account's
	// quota, the account's exhausted capacity. Rotating to a sibling
	// credential (a sibling ACCOUNT) escapes every one of them.
	"your quota",
	"your limit",
	"your limits",
	"your plan",
	"your usage",
	"your credit",
	"your credits",
	"your balance",
	"your capacity",
	"your account",
	"exhausted your capacity",
}

// modelScopeMarkers identify a body whose SUBJECT is the MODEL itself:
// the wall would apply NO MATTER which credential serves the request.
//
// EVIDENCE (wi-4b8d21e7, Phase A audit 2026-11-17): the observed corpus
// contains NO model-scoped body. Every limit/quota body we have observed
// names the ACCOUNT ("your quota", "your limit", "your capacity", "Token
// Plan", credits, balance) or names NO subject at all ("Usage limit
// reached for 5 hour" — whose subject is the account's usage window).
// A body with no model subject is exactly the "cannot distinguish" case
// the ruling resolves to the NARROWER scope.
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

// ClassifyScope returns the MINT-SCOPE of an upstream body.
//
// The order of the checks is the doctrine:
//  1. a body the classifier CANNOT DISTINGUISH (no account marker, no
//     model marker) resolves to the NARROWER scope — ScopeAccount;
//  2. positive ACCOUNT evidence → ScopeAccount;
//  3. positive MODEL evidence → ScopeModel.
//
// A positive model marker only wins when NO account marker is present:
// the account vocabulary is the broader subject ("your plan" covers a
// Token-Plan body AND a plan-window body), so it wins the tie.
//
// Empty body → ScopeAccount (never marks).
func ClassifyScope(msg string) Scope {
	if msg == "" {
		return ScopeAccount
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
	// Cannot distinguish → hide less: the narrower (credential) scope.
	return ScopeAccount
}
