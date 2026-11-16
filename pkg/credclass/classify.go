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

// QuotaTypeVocabulary / QuotaCodeVocabulary / QuotaMessageVocabulary
// are the quota-class (credit/plan exhaustion) vocabularies consumed
// by IsQuota. Split by FIELD so each field's precision can differ:
// type/code are structured (low noise ⇒ substring-safe), the message
// is free text (higher noise ⇒ bounded phrases only).
//
// Observed live vocabulary (job 8d852270): HTTP 402 / code 2067 /
// "Token Plan usage limit" — the code arrives as a NUMBER on the wire
// (unparseable into the string-typed ErrorCode field), but the message
// text survives into the message field in BOTH wire shapes:
// OpenAI-shaped bodies decode the message directly; non-OpenAI-shaped
// bodies fall back to msg = raw body, which still contains the message
// text. The message vocabulary is therefore the workhorse for
// numeric-code upstreams.
var (
	QuotaTypeVocabulary = []string{
		"quota",
		"insufficient_credit",
		"insufficient_quota",
		"insufficient_balance",
		"payment_required",
		"credit_exhausted",
		"billing",
	}
	QuotaCodeVocabulary    = QuotaTypeVocabulary // structured fields share the set
	QuotaMessageVocabulary = []string{
		"insufficient credit",
		"insufficient credits",
		"insufficient balance",
		"insufficient quota",
		"quota exceeded",
		"quota exhausted",
		"exceeded your quota",
		"credit exhausted",
		"credits exhausted",
		"out of credit",
		// W1 (pre-merge review): the former bare "usage limit" phrase was
		// removed — a PLAIN rate-limit body uses it for per-minute
		// throttling ("you have reached your usage limit for this
		// minute"), which must classify rate_limit (60s/Retry-After),
		// NOT quota. The specific observed live shape replaces it.
		"token plan usage limit",
		// "token plan" stays its own status-agnostic phrase BY DESIGN: a
		// token-plan body means the plan is exhausted at ANY status.
		"token plan",
		"payment required",
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
//     (case-insensitive) — "insufficient credit", "insufficient
//     balance", "quota exceeded", "token plan usage limit", "token
//     plan", "payment required", …
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
