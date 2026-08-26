package providers

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestIsRateLimitError_Matrix (Phase 3 / Task 17 — Round 3b review #6
// pinned vocabulary table; Round 3c S1 + W2 rows) walks the 13-row
// unit-test matrix from technical-analysis.md §pkg/providers:
// status-429, type-substring, code-equality, 503-with-rate-limit-body,
// 200-out-of-scope, nil error, non-ProviderError, and wrapped errors.
func TestIsRateLimitError_Matrix(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		want       bool
		wantRetry  time.Duration
		checkRetry bool
	}{
		{
			name: "row1: 429 empty body",
			err:  &ProviderError{StatusCode: 429},
			want: true,
		},
		{
			name: "row2: 429 rate_limit type+code",
			err:  &ProviderError{StatusCode: 429, ErrorType: "rate_limit", ErrorCode: "rate_limit"},
			want: true,
		},
		{
			name: "row3: 429 rate_limit_error type+code",
			err:  &ProviderError{StatusCode: 429, ErrorType: "rate_limit_error", ErrorCode: "rate_limit_error"},
			want: true,
		},
		{
			name: "row4: 429 message-only body (no type/code) — status wins",
			err:  &ProviderError{StatusCode: 429, Message: "too many requests"},
			want: true,
		},
		{
			name: "row5: 500 server_error — not rate limit",
			err:  &ProviderError{StatusCode: 500, ErrorType: "server_error"},
			want: false,
		},
		{
			name: "row6: 401 empty body — not rate limit",
			err:  &ProviderError{StatusCode: 401},
			want: false,
		},
		{
			name:       "row7: 429 + Retry-After seconds header",
			err:        &ProviderError{StatusCode: 429, RetryAfter: 30 * time.Second},
			want:       true,
			wantRetry:  30 * time.Second,
			checkRetry: true,
		},
		{
			name: "row9: nil error",
			err:  nil,
			want: false,
		},
		{
			name: "row10: non-ProviderError",
			err:  fmt.Errorf("plain failure"),
			want: false,
		},
		{
			name: "row11: 503 with rate-limit body code (body check when status != 429)",
			err:  &ProviderError{StatusCode: 503, ErrorCode: "rate_limit"},
			want: true,
		},
		{
			name: "row12: 429 rate_limit_exceeded code (S1)",
			err:  &ProviderError{StatusCode: 429, ErrorType: "rate_limit", ErrorCode: "rate_limit_exceeded"},
			want: true,
		},
		{
			name: "row13: 503 rate_limit_error type + rate_limit_exceeded code (S1+W2)",
			err:  &ProviderError{StatusCode: 503, ErrorType: "rate_limit_error", ErrorCode: "rate_limit_exceeded"},
			want: true,
		},
		{
			name: "wrapped: fmt.Errorf-wrapped ProviderError still classifies (errors.As)",
			err:  fmt.Errorf("upstream call failed: %w", &ProviderError{StatusCode: 429}),
			want: true,
		},
		{
			name: "substring: type rate_limit_exceeded matched by type-substring",
			err:  &ProviderError{StatusCode: 500, ErrorType: "rate_limit_exceeded"},
			want: true,
		},
		{
			name: "negative: 500 with unrelated code",
			err:  &ProviderError{StatusCode: 500, ErrorType: "server_error", ErrorCode: "internal_error"},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsRateLimitError(tc.err)
			if got != tc.want {
				t.Fatalf("IsRateLimitError(%v) = %v, want %v", tc.err, got, tc.want)
			}
			if tc.checkRetry {
				pe := tc.err.(*ProviderError)
				if pe.RetryAfter != tc.wantRetry {
					t.Fatalf("RetryAfter = %v, want %v", pe.RetryAfter, tc.wantRetry)
				}
			}
		})
	}
}

// TestHandleError_WiresRetryAfterAndVocabulary (Phase 3 / Task 17 —
// R3-1 wiring) drives handleError through real HTTP response shapes:
// the dead parseRetryAfter must be wired on the 429 path, and the
// unmarshaled body Type/Code must land on ProviderError.ErrorType /
// .ErrorCode (Round 3c — W2) so the classifier can read them.
func TestHandleError_WiresRetryAfterAndVocabulary(t *testing.T) {
	p := &OpenAIProvider{}

	cases := []struct {
		name           string
		status         int
		body           string
		retryAfterHdr  string
		wantRateLimit  bool
		wantRetryAfter time.Duration
	}{
		{
			name:           "429 with Retry-After seconds",
			status:         429,
			body:           `{"error":{"message":"slow down","type":"rate_limit","code":"rate_limit"}}`,
			retryAfterHdr:  "30",
			wantRateLimit:  true,
			wantRetryAfter: 30 * time.Second,
		},
		{
			name:           "429 without Retry-After header — zero, caller applies 60s default",
			status:         429,
			body:           `{"error":{"message":"slow down"}}`,
			wantRateLimit:  true,
			wantRetryAfter: 0,
		},
		{
			name:           "503 with rate-limit body code — matrix row 11 via handleError",
			status:         503,
			body:           `{"error":{"message":"overloaded","code":"rate_limit"}}`,
			wantRateLimit:  true,
			wantRetryAfter: 0,
		},
		{
			name:           "RFC1123 Retry-After date (future)",
			status:         429,
			body:           `{"error":{"message":"slow down"}}`,
			retryAfterHdr:  time.Now().UTC().Add(2 * time.Second).Format(http.TimeFormat),
			wantRateLimit:  true,
			wantRetryAfter: 1 * time.Second, // ~2s until; allow slop below
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hdr := http.Header{}
			if tc.retryAfterHdr != "" {
				hdr.Set("Retry-After", tc.retryAfterHdr)
			}
			resp := &http.Response{
				StatusCode: tc.status,
				Header:     hdr,
				Body:       io.NopCloser(strings.NewReader(tc.body)),
			}
			perr := p.handleError(resp, nil)
			if perr == nil {
				t.Fatal("handleError returned nil")
			}
			if got := IsRateLimitError(perr); got != tc.wantRateLimit {
				t.Fatalf("IsRateLimitError = %v, want %v (ErrorType=%q ErrorCode=%q Status=%d)",
					got, tc.wantRateLimit, perr.ErrorType, perr.ErrorCode, perr.StatusCode)
			}
			if tc.wantRetryAfter > 0 {
				slop := 1500 * time.Millisecond
				if perr.RetryAfter <= 0 || perr.RetryAfter > tc.wantRetryAfter+slop {
					t.Fatalf("RetryAfter = %v, want ~%v (±slop)", perr.RetryAfter, tc.wantRetryAfter)
				}
			} else if perr.RetryAfter != 0 {
				t.Fatalf("RetryAfter = %v, want 0", perr.RetryAfter)
			}
		})
	}
}
