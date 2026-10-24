package providers

import (
	"errors"
	"fmt"
	"testing"
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
