// Package credclass is the shared limit-class vocabulary leaf: the
// quota / rate-limit classification tables plus the field-level
// classifiers over the four facts every credential failure carries —
// the HTTP status, the error-body type, the error-body code and the
// message text.
//
// LEAF LAW: this package imports the standard library ONLY — never any
// package under the module. It exists so two lanes that cannot share a
// package (one of them sits transitively under a subtree the other's
// transitive-closure import guard bans) still classify failures with
// ONE vocabulary instead of two drifting copies: each consumer
// extracts the four fields from its own error shape and calls IsQuota
// / IsRateLimit.
//
// Precedence contract (carried over unchanged from the original
// classifiers): quota is consulted BEFORE rate-limit by the CALLER —
// a 429 whose body carries quota vocabulary classifies as quota (the
// account is out of credit; the recovery is a TOP-UP, not a pause),
// while a plain 429 classifies as rate-limit. The leaf itself has no
// opinion on precedence; the callers resolve the overlap.
//
// MERGE NOTE (wi-4b8d21e7): the extraction froze the shipped v197-era
// quota table, but the quota-mint-scope commission NARROWED that table
// to the evidence-backed tokens (review S-1 / S-4). The narrowed table
// below is the one both lanes run, so the narrowing is fleet-wide by
// construction: the chat lane (through pkg/providers), the Anthropic
// lane (through pkg/providers.ClassifyQuotaBody) and the decision lane
// (through this package directly) all resolve the SAME tokens. The
// dropped spellings are documented below with their runtime
// consequence — they no longer classify as quota anywhere.
package credclass

import "strings"

// RateLimitStatus is the HTTP status that classifies as a rate-limit
// on its own: 429 Too Many Requests.
const RateLimitStatus = 429

// RateLimitTypeSubstring is the ONLY vocabulary check on the type
// field: a case-insensitive SUBSTRING match on "rate_limit" — covers
// `rate_limit`, `rate_limit_error`, `rate_limit_exceeded` and any
// longer underscore-delimited variant the upstream brand emits. The
// match is on the UNDERSCORE spelling: a dashed "rate-limit" does NOT
// match (pre-existing behaviour, pinned by this package's test).
const RateLimitTypeSubstring = "rate_limit"

// RateLimitCodeSet is the equality set for the error-body code field:
// {rate_limit, rate_limit_error, rate_limit_exceeded}.
var RateLimitCodeSet = []string{
	"rate_limit",
	"rate_limit_error",
	"rate_limit_exceeded",
}

// QuotaStatus is the HTTP status that classifies as quota on its own:
// 402 Payment Required.
const QuotaStatus = 402

// ─────────────────────────────────────────────────────────────────────────────
// THE CANONICAL QUOTA-CLASS VOCABULARY (wi-4b8d21e7, review S-1 / S-4).
//
// ONE canonical vocabulary, three consumers:
//
//   - the CHAT lane's IsQuotaError (the *ProviderError classifier);
//   - the ANTHROPIC lane's decode-first classifier (it calls
//     pkg/providers.ClassifyQuotaBody — no local list);
//   - the MINT-SCOPE layer's subset invariant (pkg/modelcache's scope
//     vocabulary must cover every token here).
//
// WHY THE CLASS LIST IS TIGHT (the asymmetry that matters):
//
//	over-breadth in the SCOPE list resolves to the SAFE narrow scope
//	(a model mark is simply not minted);
//	over-breadth in the CLASS list benches a HEALTHY credential for
//	15m instead of 60s — the "cooldown trap" this commission exists to
//	close (a walled account that returns to the pool after a minute).
//
// Every token below is EVIDENCE-BACKED: a body in the corpus (a live
// upstream body or a test pin of one) carries it. Tokens without an
// observed instance were DROPPED (the shipped v197 list carried a few;
// the Anthropic lane's local list carried three more, one of them
// INVENTED — "insufficient_parameters" is a CLIENT error, not a quota).
//
// PROVENANCE per token (the observed instance):
//
//	"insufficient_credit"  — providers/interface_quota_test.go (type)
//	"insufficient_quota"   — providers/interface_quota_test.go (type/code);
//	                         pkg/proxy/sticky_scenario_test.go (live-shaped body)
//	"quota"                — providers/interface_quota_test.go (the
//	                         "quota_exceeded" type is matched by this token);
//	                         the 9router live body
//	"payment_required"     — the review's drift probe (code=payment_required
//	                         classified QUOTA in the race lane, NOT in the
//	                         Anthropic lane — the drift this list ends);
//	                         BOTH spellings are kept on purpose
//	"billing"              — providers/interface_quota_test.go (the
//	                         "billing_not_active" type/code — the TYPE
//	                         spelling only; the MESSAGE spelling is dropped)
//	"resource_exhausted"   — pkg/imggen/ninerouter.go:290 (the 9router
//	                         nested body: "status":"RESOURCE_EXHAUSTED")
//	"insufficient credit"  — providers/interface_quota_test.go (message)
//	"quota exceeded"       — providers/interface_quota_test.go (message)
//	"quota exhausted"      — pkg/proxy/race_reason_test.go (message)
//	"token plan usage limit" — the z.ai live bodies (codes 2056 / 2067)
//	"token plan"           — the z.ai live bodies (codes 2056 / 2067)
//	"payment required"     — providers/interface_quota_test.go (message)
//	"your quota will reset" — the 9router live body
//	"your limit will reset" — the z.ai 5-hour plan-window body
//	"exhausted your capacity" — the 9router live body
//
// DROPPED (no observed instance — the class list is the narrow, safe
// side; the SCOPE vocabulary in pkg/modelcache keeps the account-subject
// knowledge so the mint scope stays correct):
//
//	"insufficient_quota" / "insufficient_balance" (message spellings),
//	"insufficient_credits", "exceeded your quota", "credit exhausted",
//	"credits exhausted", "out of credit",
//	"billing" (message spelling; the TYPE/CODE token above stays),
//	"credit_exhausted", "insufficient_balance" (type spelling),
//	"insufficient_parameters" (INVENTED — a client-side parameter
//	complaint, never a quota), "credit_balance_too_low" and
//	"billing_error" (Anthropic-lane-only, no corpus instance).
//
// RUNTIME CONSEQUENCE of the drops (operator-facing — a body carrying a
// dropped spelling now falls through the classifier ladder instead of
// matching quota):
//
//	an HTTP 429 carrying one → RATE-LIMIT class (the body carries no
//	  quota vocabulary, so the fallback ladder lands on the 429
//	  status) → 60s / Retry-After cooldown, NOT the 15m quota
//	  cooldown;
//	any OTHER status carrying one (e.g. a 400) → not limit-class at
//	  all → NO rotation and NO cooldown (a client-attributable 4xx
//	  must never rotate or bench a credential — commission S-3);
//	cosmetic: "insufficient credits" (space, plural) is still MATCHED —
//	  the retained "insufficient credit" hits it via Contains. Only
//	  the underscore spelling "insufficient_credits" is truly gone.
//
// The retained set is PINNED by TestQuotaVocabulary_IsLocked
// (providers/interface_quota_test.go) — any future widen or narrow of
// the class vocabulary must update that pin in the same change, so the
// policy move is always deliberate.
//
// Observed live vocabulary (job 8d852270): HTTP 402 / code 2067 /
// "Token Plan usage limit" — the code arrives as a NUMBER on the wire
// (unparseable into the string-typed ErrorCode field), but the message
// text survives into the message field in BOTH wire shapes:
// OpenAI-shaped bodies decode the message directly; non-OpenAI-shaped
// bodies fall back to msg = raw body (openai.go:handleError), which
// still contains the message text. The message vocabulary is therefore
// the workhorse for numeric-code upstreams.
// ─────────────────────────────────────────────────────────────────────────────
var (
	QuotaTypeVocabulary = []string{
		"insufficient_credit",
		"insufficient_quota",
		"quota",
		"payment_required",
		"billing",
		"resource_exhausted",
	}
	QuotaCodeVocabulary    = QuotaTypeVocabulary // structured fields share the set
	QuotaMessageVocabulary = []string{
		"insufficient credit",
		"quota exceeded",
		"quota exhausted",
		// The specific observed live shape replaces the former bare
		// "usage limit" phrase — a PLAIN rate-limit body uses "usage
		// limit" for per-minute throttling ("you have reached your
		// usage limit for this minute"), which must classify rate_limit
		// (60s/Retry-After), NOT quota.
		"token plan usage limit",
		// "token plan" stays its own status-agnostic phrase BY DESIGN: a
		// token-plan body means the plan is exhausted at ANY status.
		"token plan",
		"payment required",
		// The account-subject shapes the SCOPE layer already knows
		// (review S-1): the z.ai plan window and the 9router account
		// quota are ACCOUNT walls — they must seed the QUOTA cooldown
		// (15m), not the flat rate-limit one (60s), or the dry account
		// re-enters the pool after a minute.
		"your quota will reset",
		"your limit will reset",
		"exhausted your capacity",
	}
)

// ContainsAny reports whether the lowercased haystack contains any of
// the (already-lowercase) vocabulary entries.
func ContainsAny(lowered string, vocabulary []string) bool {
	for _, v := range vocabulary {
		if strings.Contains(lowered, v) {
			return true
		}
	}
	return false
}

// IsRateLimit classifies a failure's four facts as a 429-style
// rate-limit condition.
//
// True when ANY of the three match:
//   - status == 429, OR
//   - the type contains the substring "rate_limit" (case-insensitive;
//     the UNDERSCORE spelling — `rate_limit`, `rate_limit_error`,
//     `rate_limit_exceeded` and any longer underscore-delimited
//     variant the upstream brand emits), OR
//   - the code matches the equality set
//     {rate_limit, rate_limit_error, rate_limit_exceeded}.
//
// The message field is part of the uniform tuple but is NOT consulted:
// a plain rate-limit body's "usage limit for this minute" phrasing is
// message-side noise, and the quota vocabulary — not rate-limit — owns
// the message field. Caller precedence (quota before rate-limit) keeps
// a quota body from ever reaching this classifier as the deciding
// call.
//
// NOT touched by the quota-vocabulary narrowing (wi-4b8d21e7): the
// rate-limit table is a separate vocabulary and is unchanged.
func IsRateLimit(status int, errType, errCode, message string) bool {
	_ = message // uniform tuple; rate-limit never consults the message field
	if status == RateLimitStatus {
		return true
	}
	if strings.Contains(strings.ToLower(errType), RateLimitTypeSubstring) {
		return true
	}
	for _, code := range RateLimitCodeSet {
		if errCode == code {
			return true
		}
	}
	return false
}

// IsQuota classifies a failure's four facts as a QUOTA-class condition:
// the credential's credit / plan / balance is exhausted.
//
// True when ANY of the four match:
//   - status == 402 (Payment Required), OR
//   - the type contains the quota vocabulary substring
//     (case-insensitive), OR
//   - the code matches the quota vocabulary substring
//     (case-insensitive), OR
//   - the message carries one of the bounded quota phrases
//     (case-insensitive) — "insufficient credit", "quota exceeded",
//     "quota exhausted", "token plan usage limit", "token plan",
//     "payment required", "your quota will reset", "your limit will
//     reset", "exhausted your capacity", …
//
// (the dropped spellings — "insufficient balance", "out of credit", …
// no longer match — see the RUNTIME CONSEQUENCE note at the vocabulary)
//
// Why a message vocabulary at all: quota rejections arrive in
// non-OpenAI body shapes from gateway upstreams, and the quota code is
// often numeric (code 2067) — invisible to the string-typed code
// field. The message (or the raw-body fallback that lands in it) is
// the only carrier that survives every shape (see
// QuotaMessageVocabulary above).
//
// Status-agnostic on the vocabulary arms BY DESIGN: a "token plan"
// body means the plan is exhausted at ANY status. Auth-class failures
// (401/403 without vocabulary) carry none of the tables — the
// classifier is silent on them.
func IsQuota(status int, errType, errCode, message string) bool {
	if status == QuotaStatus {
		return true
	}
	if ContainsAny(strings.ToLower(errType), QuotaTypeVocabulary) {
		return true
	}
	if ContainsAny(strings.ToLower(errCode), QuotaCodeVocabulary) {
		return true
	}
	return ContainsAny(strings.ToLower(message), QuotaMessageVocabulary)
}
