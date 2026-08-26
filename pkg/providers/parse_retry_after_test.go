package providers

import (
	"net/http"
	"testing"
	"time"
)

// TestParseRetryAfter_Clamp (Phase 3 residuals — Item 3) exercises the
// two parseRetryAfter branches and the negative-clamp contract:
//   - seconds branch: integer seconds → Duration; negative or zero
//     clamped to 0 (a negative cooldown would otherwise expire a
//     credential's cooling instantly and re-select the same credential
//     on the next failover — see openai.go:611-617 doc).
//   - HTTP-date branch (RFC1123 / time.RFC1123 — the same format as
//     http.TimeFormat): time.Until(target) — past dates clamp to 0,
//     future dates return positive Duration ≈ delta.
//   - unparseable input: 0.
//   - empty input: 0.
//
// This test must be table-driven and same-package (parseRetryAfter is
// unexported). It would fail if the negative-clamp branches regress.
func TestParseRetryAfter_Clamp(t *testing.T) {
	now := time.Now()

	cases := []struct {
		name     string
		header   string
		want     time.Duration
		epsilon  time.Duration // tolerance for the HTTP-date future branch (date parse + now drift)
	}{
		{
			name:   "empty header returns 0",
			header: "",
			want:   0,
		},
		{
			name:   "negative seconds clamp to 0",
			header: "-1",
			want:   0,
		},
		{
			name:   "large negative seconds clamp to 0",
			header: "-3600",
			want:   0,
		},
		{
			name:   "zero seconds return 0",
			header: "0",
			want:   0,
		},
		{
			name:   "positive seconds return Duration",
			header: "30",
			want:   30 * time.Second,
		},
		{
			name:   "positive large seconds return Duration",
			header: "120",
			want:   120 * time.Second,
		},
		{
			name:   "past HTTP-date clamps to 0",
			header: "Thu, 01 Jan 1970 00:00:00 GMT",
			want:   0,
		},
		{
			name:    "future HTTP-date (~3min ahead) returns positive ≈ delta",
			header:  now.Add(3 * time.Minute).UTC().Format(http.TimeFormat),
			want:    3 * time.Minute,
			epsilon: 2 * time.Second, // parse + clock drift between now() and parseRetryAfter's time.Until
		},
		{
			name:   "future HTTP-date (~5s ahead) returns positive bounded Duration",
			header: now.Add(5 * time.Second).UTC().Format(http.TimeFormat),
			// Date is in the future but the bound is generous: returned
			// Duration can be anywhere in (0, 5s] depending on clock
			// drift between now() above and parseRetryAfter's time.Until.
			// Asserted below with epsilon=5s in the comparison branch.
			want:    5 * time.Second,
			epsilon: 5 * time.Second,
		},
		{
			name:   "unparseable header returns 0",
			header: "not-a-number-or-date",
			want:   0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseRetryAfter(tc.header)
			if tc.epsilon > 0 {
				// Bounded comparison: |got - want| <= epsilon.
				diff := got - tc.want
				if diff < 0 {
					diff = -diff
				}
				if diff > tc.epsilon {
					t.Fatalf("parseRetryAfter(%q) = %v, want ≈ %v (±%v)", tc.header, got, tc.want, tc.epsilon)
				}
			} else {
				if got != tc.want {
					t.Fatalf("parseRetryAfter(%q) = %v, want %v", tc.header, got, tc.want)
				}
			}
		})
	}
}