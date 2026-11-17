package providers

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/credclass"
)

// ─────────────────────────────────────────────────────────────────────────────
// IsQuotaError (layered credential fallback — Fix 1) classifier matrix.
//
// Pinned vocabulary: HTTP 402, quota/insufficient-credit type+code
// substrings, and the bounded message phrases — including the observed
// live shape (code 2067 numeric + "Token Plan usage limit"), whose
// numeric code cannot land in the string-typed ErrorCode field so the
// message carries the class.
// ─────────────────────────────────────────────────────────────────────────────

func TestIsQuotaError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"non-provider error", errors.New("boom"), false},

		// Status-based
		{"402 payment required", &ProviderError{StatusCode: 402, Message: "payment required"}, true},
		{"402 empty body", &ProviderError{StatusCode: 402}, true},

		// Type vocabulary
		{"type insufficient_quota", &ProviderError{StatusCode: 400, ErrorType: "insufficient_quota"}, true},
		{"type quota_exceeded", &ProviderError{StatusCode: 400, ErrorType: "quota_exceeded"}, true},
		{"type insufficient_credit mixed case", &ProviderError{StatusCode: 400, ErrorType: "Insufficient_Credit"}, true},
		{"type billing", &ProviderError{StatusCode: 400, ErrorType: "billing_not_active"}, true},

		// Code vocabulary
		{"code insufficient_quota", &ProviderError{StatusCode: 400, ErrorCode: "insufficient_quota"}, true},
		{"code quota", &ProviderError{StatusCode: 400, ErrorCode: "QUOTA"}, true},

		// Message vocabulary (the numeric-code upstreams' carrier)
		{"message token plan usage limit (observed live)", &ProviderError{StatusCode: 400, Message: "Token Plan usage limit"}, true},
		{"message insufficient credit", &ProviderError{StatusCode: 400, Message: "You have insufficient credit to serve the request"}, true},
		{"message quota exceeded", &ProviderError{StatusCode: 400, Message: "quota exceeded for project"}, true},
		{"message payment required", &ProviderError{StatusCode: 400, Message: "Payment required for this organization"}, true},

		// W1 (pre-merge review) — the bare "usage limit" phrase was
		// removed: a PLAIN rate-limit body uses it for per-minute
		// throttling and must NOT classify as quota (it would bench the
		// credential for the quota cooldown instead of a 60s pause).
		{"429 plain per-minute usage limit body", &ProviderError{StatusCode: 429, ErrorType: "rate_limit", Message: "you have reached your usage limit for this minute"}, false},
		// …while "token plan" remains its own status-agnostic phrase BY
		// DESIGN: the plan is exhausted at ANY status, 429 included.
		{"429 token plan body stays quota (status-agnostic by design)", &ProviderError{StatusCode: 429, Message: "Token Plan usage limit"}, true},
		// W1 (ii): 402 + the token-plan body ⇒ quota (status path and
		// message path agree).
		{"402 token plan usage limit", &ProviderError{StatusCode: 402, Message: "Token Plan usage limit"}, true},

		// Raw-body fallback shape: a non-OpenAI body lands WHOLESALE in
		// Message (openai.go:handleError msg = string(body)) — the quota
		// phrase inside it still classifies.
		{"raw body fallback with numeric code", &ProviderError{StatusCode: 400, Message: `{"code":2067,"message":"Token Plan usage limit"}`}, true},

		// Wrapped *ProviderError must classify through the wrapping.
		{"wrapped 402", fmt.Errorf("upstream: %w", &ProviderError{StatusCode: 402, Message: "no credit"}), true},
		{"wrapped token plan", fmt.Errorf("layer: %w", &ProviderError{StatusCode: 400, Message: "Token Plan usage limit"}), true},

		// Ineligible classes stay false
		{"429 rate limit", &ProviderError{StatusCode: 429, ErrorType: "rate_limit", Message: "rate limited"}, false},
		{"429 rate limit code", &ProviderError{StatusCode: 429, ErrorCode: "rate_limit_error"}, false},
		{"401 unauthorized", &ProviderError{StatusCode: 401, Message: "invalid api key"}, false},
		{"403 forbidden", &ProviderError{StatusCode: 403, Message: "forbidden"}, false},
		{"500 server error", &ProviderError{StatusCode: 500, Message: "internal error"}, false},
		{"503 unavailable", &ProviderError{StatusCode: 503, Message: "unavailable"}, false},
		{"idle timeout shape", &ProviderError{StatusCode: 0, Message: "context deadline exceeded"}, false},

		// Vocabulary boundaries — near-miss words must NOT classify.
		{"message mentions rate limit only", &ProviderError{StatusCode: 400, Message: "rate limit exceeded"}, false},
		{"nil *ProviderError inside interface", (*ProviderError)(nil), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsQuotaError(tc.err); got != tc.want {
				t.Errorf("IsQuotaError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestIsQuotaError_DoesNotShadowRateLimit pins the class boundary the
// proxy classifier relies on: a PLAIN 429 (rate-limit vocabulary only)
// is not quota, so the 60s/Retry-After rate-limit policy is untouched.
// A 429 that EXPLICITLY carries quota vocabulary (OpenAI's real shape
// for credit exhaustion) IS quota — the account needs a top-up, not a
// pause.
func TestIsQuotaError_DoesNotShadowRateLimit(t *testing.T) {
	plain := &ProviderError{StatusCode: 429, ErrorType: "rate_limit", ErrorCode: "rate_limit", Message: "Too many requests, slow down"}
	if IsQuotaError(plain) {
		t.Errorf("plain 429 classified as quota — rate-limit 60s/Retry-After policy would be shadowed")
	}
	if !IsRateLimitError(plain) {
		t.Errorf("plain 429 not classified as rate-limit")
	}

	openAiQuota := &ProviderError{StatusCode: 429, ErrorType: "insufficient_quota", ErrorCode: "insufficient_quota", Message: "You exceeded your current quota"}
	if !IsQuotaError(openAiQuota) {
		t.Errorf("OpenAI insufficient_quota 429 not classified as quota")
	}
	if !IsRateLimitError(openAiQuota) {
		// IsRateLimitError is status-429-first — both classifiers fire;
		// the proxy resolves the overlap quota-first.
		t.Errorf("429 status must still classify as rate-limit for the fallback classifier")
	}
}

// TestHandleError_Quota402NumericCodeLeavesCodeEmpty pins the PARSE
// behavior the layered-fallback classifier relies on at the provider
// seam: the observed live 402 body carries its code as a JSON NUMBER
// (2067), which cannot unmarshal into the string-typed
// ProviderError.ErrorCode field — the field stays "" and the MESSAGE
// carries the class. Quota classification must therefore succeed from
// the 402 status + message text ALONE (mirrored at the coordinator
// seam by cred_failover_layered_test.go's quotaScriptedProvider, whose
// ErrorCode is deliberately wire-faithful-empty).
func TestHandleError_Quota402NumericCodeLeavesCodeEmpty(t *testing.T) {
	p := &OpenAIProvider{}
	resp := &http.Response{
		StatusCode: http.StatusPaymentRequired,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"Token Plan usage limit","code":2067}}`)),
	}
	perr := p.handleError(resp, nil)
	if perr == nil {
		t.Fatal("handleError returned nil")
	}
	if perr.ErrorCode != "" {
		t.Errorf("ErrorCode = %q, want \"\" — the numeric 2067 cannot unmarshal into the string-typed field", perr.ErrorCode)
	}
	if perr.ErrorType != "" {
		t.Errorf("ErrorType = %q, want \"\" (the live body carries no type)", perr.ErrorType)
	}
	if perr.Message != "Token Plan usage limit" {
		t.Errorf("Message = %q, want \"Token Plan usage limit\" (the message survives the partial decode and carries the class)", perr.Message)
	}
	if perr.StatusCode != http.StatusPaymentRequired {
		t.Errorf("StatusCode = %d, want 402", perr.StatusCode)
	}
	if !IsQuotaError(perr) {
		t.Error("IsQuotaError = false — classification must succeed from the 402 status + message alone")
	}
	if IsRateLimitError(perr) {
		t.Error("IsRateLimitError = true on a 402 — the quota class must not shadow into rate-limit")
	}
	if perr.RetryAfter != 0 {
		t.Errorf("RetryAfter = %v, want 0 (Retry-After parsing is 429-only)", perr.RetryAfter)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestQuotaVocabulary_IsLocked — the vocabulary PIN (wi-4b8d21e7, closure
// OBS-1).
//
// Round 2 NARROWED the quota-CLASS vocabulary to the evidence-backed
// tokens (every dropped spelling lacked an observed corpus instance).
// That narrowing is a DELIBERATE policy change with an operator-facing
// consequence, not a no-op:
//
//   - an HTTP 429 carrying a dropped spelling ("insufficient balance",
//     "credit exhausted", "out of credit", …) now classifies RATE-LIMIT
//     (60s / Retry-After cooldown), NOT quota (15m) — the body carries
//     no quota vocabulary, so the fallback ladder lands on the 429
//     status;
//   - any NON-429 carrying a dropped spelling is not limit-class at
//     all → no rotation, no cooldown (a client-attributable 4xx must
//     never rotate or bench a credential — commission S-3);
//   - "insufficient credits" (space, plural) is still MATCHED via the
//     retained "insufficient credit" (Contains) — only the underscore
//     spelling "insufficient_credits" is gone.
//
// This pin locks the CURRENT set element-for-element so any future
// widen or narrow is a conscious decision that updates this test in
// the same change. The SCOPE vocabulary in pkg/modelcache is a
// separate, deliberately broader list (class ⊂ scope) and is pinned
// there, not here.
//
// WHERE THE TABLES LIVE (merge note — wi-4b8d21e7): the upstream
// credclass extraction moved the vocabulary tables to the leaf
// package pkg/credclass, so this pin reads them THERE — the lock
// covers the ONE table every lane runs, not a local copy of it.
// ─────────────────────────────────────────────────────────────────────────────
func TestQuotaVocabulary_IsLocked(t *testing.T) {
	wantStructured := []string{
		"insufficient_credit",
		"insufficient_quota",
		"quota",
		"payment_required",
		"billing",
		"resource_exhausted",
	}
	if !slices.Equal(credclass.QuotaTypeVocabulary, wantStructured) {
		t.Errorf("credclass.QuotaTypeVocabulary = %q\nwant (locked) %q\n—the quota-CLASS vocabulary changed. Widening benches a healthy credential for 15m; narrowing re-classifies live bodies. Make the change deliberate and update this pin.", credclass.QuotaTypeVocabulary, wantStructured)
	}
	if !slices.Equal(credclass.QuotaCodeVocabulary, wantStructured) {
		t.Errorf("credclass.QuotaCodeVocabulary = %q\nwant it to share the locked structured set %q\n—type and code fields must match the SAME vocabulary.", credclass.QuotaCodeVocabulary, wantStructured)
	}

	wantMessage := []string{
		"insufficient credit",
		"quota exceeded",
		"quota exhausted",
		"token plan usage limit",
		"token plan",
		"payment required",
		"your quota will reset",
		"your limit will reset",
		"exhausted your capacity",
	}
	if !slices.Equal(credclass.QuotaMessageVocabulary, wantMessage) {
		t.Errorf("credclass.QuotaMessageVocabulary = %q\nwant (locked) %q\n—the quota-CLASS vocabulary changed. Widening benches a healthy credential for 15m; narrowing re-classifies live bodies. Make the change deliberate and update this pin.", credclass.QuotaMessageVocabulary, wantMessage)
	}

	// The dropped spellings stay ABSENT from the list each used to live
	// in (round-2 policy: no class token without an observed corpus
	// instance).
	for _, tok := range []string{
		"insufficient balance",
		"insufficient quota", // space spelling; the underscore TYPE token above stays
		"exceeded your quota",
		"credit exhausted",
		"credits exhausted",
		"out of credit",
		"billing", // message spelling; the TYPE/CODE token above stays
	} {
		if slices.Contains(credclass.QuotaMessageVocabulary, tok) {
			t.Errorf("credclass.QuotaMessageVocabulary re-added dropped spelling %q — a re-added class token benches a healthy credential for 15m on any body that carries it; it needs corpus evidence AND this pin updated", tok)
		}
	}
	for _, tok := range []string{
		"credit_exhausted",
	} {
		if slices.Contains(credclass.QuotaTypeVocabulary, tok) {
			t.Errorf("credclass.QuotaTypeVocabulary re-added dropped spelling %q — a re-added class token benches a healthy credential for 15m on any body that carries it; it needs corpus evidence AND this pin updated", tok)
		}
	}

	// The exported aggregate must be exactly the locked union (15
	// tokens) — pkg/modelcache's scope-subset invariant reads it.
	want := append(append([]string{}, wantStructured...), wantMessage...)
	got := QuotaVocabularyTokens()
	if !slices.Equal(got, want) {
		t.Errorf("QuotaVocabularyTokens() = %q (len %d)\nwant the locked 15-token union %q", got, len(got), want)
	}

	// The consequence, pinned EXECUTABLE (the header comment's ladder):
	// a dropped spelling on a live body classifies by status fallback,
	// never by the dropped text.
	dropped429 := &ProviderError{StatusCode: 429, Message: "insufficient balance, please top up"}
	if IsQuotaError(dropped429) {
		t.Error("429 carrying the dropped spelling \"insufficient balance\" classified QUOTA — the round-2 narrowing regressed")
	}
	if !IsRateLimitError(dropped429) {
		t.Error("429 carrying a dropped spelling must still classify RATE-LIMIT (60s/Retry-After), not nothing")
	}
	dropped400 := &ProviderError{StatusCode: 400, Message: "insufficient balance, please top up"}
	if IsQuotaError(dropped400) || IsRateLimitError(dropped400) {
		t.Error("400 carrying a dropped spelling must be limit-class NOWHERE — no rotation, no cooldown (commission S-3)")
	}
}
