package credclass

import (
	"fmt"
	"testing"
)

// The shared vocabulary table: ONE table, run by both lanes (the
// provider lane delegates to this package; the decision lane consumes
// it directly). Every exported vocabulary entry must classify its own
// class, and must NOT classify the other class.

// TestCredClass_IsQuota_VocabularyTable pins every quota vocabulary
// entry: each type/code/message entry classifies quota; a plain
// "usage limit" does NOT (the W1 removal — a plain per-minute
// throttling body must stay rate-limit); "token plan" is
// status-agnostic; HTTP 402 is quota on the status alone.
func TestCredClass_IsQuota_VocabularyTable(t *testing.T) {
	cases := []struct {
		name                      string
		status                    int
		errType, errCode, message string
		want                      bool
	}{
		// Status alone.
		{"402 empty body", QuotaStatus, "", "", "", true},
		{"200 empty body is not quota", 200, "", "", "", false},

		// Every type-vocabulary entry (substring, case-insensitive).
		{"type insufficient_quota", 400, "insufficient_quota", "", "", true},
		{"type quota_exceeded", 400, "quota_exceeded", "", "", true},
		{"type insufficient_credit mixed case", 400, "Insufficient_Credit", "", "", true},
		{"type billing", 400, "billing_not_active", "", "", true},
		{"type payment_required", 400, "payment_required", "", "", true},
		{"type credit_exhausted", 400, "credit_exhausted", "", "", true},
		{"type insufficient_balance", 400, "insufficient_balance", "", "", true},
		{"type quota", 400, "quota", "", "", true},

		// Every code-vocabulary entry (the code set shares the type set).
		{"code quota", 400, "", "quota", "", true},
		{"code insufficient_quota", 400, "", "insufficient_quota", "", true},
		{"code billing", 400, "", "billing_not_active", "", true},

		// Every message-vocabulary entry.
		{"message insufficient credit", 400, "", "", "you have insufficient credit", true},
		{"message insufficient credits", 400, "", "", "insufficient credits", true},
		{"message insufficient balance", 400, "", "", "insufficient balance", true},
		{"message insufficient quota", 400, "", "", "insufficient quota", true},
		{"message quota exceeded", 429, "", "", "quota exceeded", true},
		{"message quota exhausted", 429, "", "", "monthly quota exhausted", true},
		{"message exceeded your quota", 429, "", "", "you have exceeded your quota", true},
		{"message credit exhausted", 400, "", "", "credit exhausted", true},
		{"message credits exhausted", 400, "", "", "credits exhausted", true},
		{"message out of credit", 400, "", "", "you are out of credit", true},
		{"message token plan usage limit", 429, "", "", "Token Plan usage limit exceeded", true},
		{"message payment required", 400, "", "", "payment required", true},

		// The two live-shape cases the extraction must keep.
		{"plain usage limit stays NON-quota", 429, "", "", "you have reached your usage limit for this minute", false},
		{"token plan is status-agnostic (200)", 200, "", "", "token plan exhausted", true},
		{"token plan is status-agnostic (429)", 429, "", "", "token plan exhausted", true},

		// Rate-limit noise is not quota.
		{"plain 429 is not quota", 429, "", "", "slow down", false},
		{"rate_limit type is not quota", 429, "rate_limit_error", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsQuota(tc.status, tc.errType, tc.errCode, tc.message); got != tc.want {
				t.Fatalf("IsQuota(%d, %q, %q, %q) = %v, want %v", tc.status, tc.errType, tc.errCode, tc.message, got, tc.want)
			}
		})
	}

	// Table sweep: every exported vocabulary entry classifies quota
	// through its own field, with the other fields empty.
	for _, entry := range QuotaTypeVocabulary {
		t.Run("type-entry/"+entry, func(t *testing.T) {
			if !IsQuota(400, entry, "", "") {
				t.Fatalf("type vocabulary entry %q does not classify quota", entry)
			}
		})
	}
	for _, entry := range QuotaCodeVocabulary {
		t.Run("code-entry/"+entry, func(t *testing.T) {
			if !IsQuota(400, "", entry, "") {
				t.Fatalf("code vocabulary entry %q does not classify quota", entry)
			}
		})
	}
	for _, entry := range QuotaMessageVocabulary {
		t.Run("message-entry/"+entry, func(t *testing.T) {
			if !IsQuota(400, "", "", entry) {
				t.Fatalf("message vocabulary entry %q does not classify quota", entry)
			}
		})
	}
}

// TestCredClass_IsRateLimit_VocabularyTable pins the rate-limit
// classification: 429 on the status; the type-substring rule; the code
// equality set; and a quota body is NOT rate-limit (caller precedence:
// quota is consulted first, but the classifier itself must also be
// silent on quota-only bodies).
func TestCredClass_IsRateLimit_VocabularyTable(t *testing.T) {
	cases := []struct {
		name                      string
		status                    int
		errType, errCode, message string
		want                      bool
	}{
		{"429 on the status alone", RateLimitStatus, "", "", "", true},
		{"402 alone is not rate-limit", QuotaStatus, "", "", "", false},
		{"type substring rate_limit", 400, "rate_limit", "", "", true},
		{"type substring rate_limit_error", 400, "rate_limit_error", "", "", true},
		{"type substring rate_limit_exceeded", 400, "rate_limit_exceeded", "", "", true},
		{"type substring mixed case", 400, "Rate_Limit", "", "", true},
		// The type rule is an UNDERSCORE substring match: the dashed
		// spelling does NOT match (pre-existing behaviour, pinned here
		// so the extraction cannot silently change it).
		{"type dashed variant does not match", 400, "rate-limit", "", "", false},
		{"type non-rate-limit", 400, "insufficient_quota", "", "", false},
		{"code equality set entry 1", 400, "", "rate_limit", "", true},
		{"code equality set entry 2", 400, "", "rate_limit_error", "", true},
		{"code equality set entry 3", 400, "", "rate_limit_exceeded", "", true},
		{"code non-set entry", 400, "", "usage_limit", "", false},
		// A quota body on a NON-429 status is NOT rate-limit. (A quota
		// body ON a 429 IS rate-limit at leaf level — the caller's
		// quota-first precedence is what resolves the overlap; see the
		// next case.)
		{"a quota body on 400 is NOT rate-limit", 400, "", "", "quota exceeded", false},
		{"a quota type is NOT rate-limit", 402, "insufficient_quota", "", "", false},
		// Leaf-level status precedence: 429 wins regardless of body —
		// the CALLER consults quota BEFORE rate-limit (that ordering is
		// pinned in the callers, not here).
		{"429 with a quota body still classifies rate-limit at leaf level", RateLimitStatus, "", "", "quota exceeded", true},
		{"the message field is not consulted", 400, "", "", "rate_limit-ish text", false},
		{"plain 400 is nothing", 400, "", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsRateLimit(tc.status, tc.errType, tc.errCode, tc.message)
			if got != tc.want {
				t.Fatalf("IsRateLimit(%d, %q, %q, %q) = %v, want %v", tc.status, tc.errType, tc.errCode, tc.message, got, tc.want)
			}
		})
	}

	// Table sweep: every exported code-set entry classifies via the
	// code field alone.
	for _, entry := range RateLimitCodeSet {
		t.Run("code-entry/"+entry, func(t *testing.T) {
			if !IsRateLimit(400, "", entry, "") {
				t.Fatalf("code set entry %q does not classify rate-limit", entry)
			}
		})
	}
	if RateLimitTypeSubstring == "" {
		t.Fatal("RateLimitTypeSubstring must be non-empty")
	}
	if !IsRateLimit(400, fmt.Sprintf("prefix_%s", RateLimitTypeSubstring), "", "") {
		t.Fatal("the type-substring rule must match the exported substring")
	}
}
