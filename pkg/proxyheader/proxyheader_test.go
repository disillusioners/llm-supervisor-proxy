package proxyheader

import (
	"net/http"
	"testing"
)

func TestParseInterleavedThinkingHeaderValue(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		// accepted (case-insensitive via strconv.ParseBool)
		{"true", true},
		{"True", true},
		{"TRUE", true},
		{"t", true},
		{"T", true},
		{"1", true},
		// whitespace tolerated around the canonical forms (trim before parse)
		{" true", true},
		{"true ", true},
		{"  TRUE  ", true},
		// rejected — anything outside strconv.ParseBool's whitelist
		{"yes", false},
		{"YES", false},
		{"on", false},
		{"enabled", false},
		{"false", false},
		{"False", false},
		{"FALSE", false},
		{"f", false},
		{"F", false},
		{"0", false},
		{"", false},
		{"   ", false}, // pure whitespace after trim → empty → false
		{"truee", false},
		{"11", false},
		{"truthy", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := ParseInterleavedThinkingHeaderValue(tc.in); got != tc.want {
				t.Errorf("ParseInterleavedThinkingHeaderValue(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseInterleavedThinkingHeader(t *testing.T) {
	cases := []struct {
		name  string
		setup func(r *http.Request)
		want  bool
	}{
		{
			name:  "canonical case set to true",
			setup: func(r *http.Request) { r.Header.Set(InterleavedThinkingHeader, "true") },
			want:  true,
		},
		{
			name:  "canonical case set to 1",
			setup: func(r *http.Request) { r.Header.Set(InterleavedThinkingHeader, "1") },
			want:  true,
		},
		{
			name:  "lowercase variant accepted (canonical case folding of NAME)",
			setup: func(r *http.Request) { r.Header.Set("x-proxy-interleaved-thinking", "true") },
			want:  true,
		},
		{
			name:  "uppercase variant accepted (canonical case folding of NAME)",
			setup: func(r *http.Request) { r.Header.Set("X-PROXY-INTERLEAVED-THINKING", "true") },
			want:  true,
		},
		{
			name:  "mixed-case variant accepted (canonical case folding of NAME)",
			setup: func(r *http.Request) { r.Header.Set("X-proxy-INTERLEAVED-thinking", "true") },
			want:  true,
		},
		{
			// NAME case folding is stdlib (Go's http.Header.Get); we
			// verify it works here so future refactors that replace
			// the lookup don't silently regress.
			name:  "True value accepted (case-insensitive VALUE)",
			setup: func(r *http.Request) { r.Header.Set(InterleavedThinkingHeader, "True") },
			want:  true,
		},
		{
			name:  "TRUE value accepted (case-insensitive VALUE)",
			setup: func(r *http.Request) { r.Header.Set(InterleavedThinkingHeader, "TRUE") },
			want:  true,
		},
		{
			name:  "yes value still rejected (out of strconv.ParseBool whitelist)",
			setup: func(r *http.Request) { r.Header.Set(InterleavedThinkingHeader, "yes") },
			want:  false,
		},
		{
			name:  "missing header",
			setup:  func(r *http.Request) {},
			want:  false,
		},
		{
			name:  "empty value rejected",
			setup: func(r *http.Request) { r.Header.Set(InterleavedThinkingHeader, "") },
			want:  false,
		},
		{
			name:  "nil request",
			setup:  func(r *http.Request) {},
			want:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var r *http.Request
			if tc.name != "nil request" {
				r, _ = http.NewRequest("POST", "/v1/chat/completions", nil)
				tc.setup(r)
			}
			if got := ParseInterleavedThinkingHeader(r); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}