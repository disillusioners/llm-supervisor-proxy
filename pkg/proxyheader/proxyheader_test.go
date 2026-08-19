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
		// accepted (per handler.go:465 precedent — case-sensitive lowercase)
		{"true", true},
		{"1", true},
		// rejected
		{"True", false},
		{"TRUE", false},
		{"yes", false},
		{"YES", false},
		{"false", false},
		{"0", false},
		{"on", false},
		{"", false},
		{" true", false},
		{"true ", false},
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
			name:  "lowercase variant accepted (canonical case folding)",
			setup: func(r *http.Request) { r.Header.Set("x-proxy-interleaved-thinking", "true") },
			want:  true,
		},
		{
			name:  "missing header",
			setup:  func(r *http.Request) {},
			want:  false,
		},
		{
			name:  "True rejected",
			setup: func(r *http.Request) { r.Header.Set(InterleavedThinkingHeader, "True") },
			want:  false,
		},
		{
			name:  "yes rejected",
			setup: func(r *http.Request) { r.Header.Set(InterleavedThinkingHeader, "yes") },
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