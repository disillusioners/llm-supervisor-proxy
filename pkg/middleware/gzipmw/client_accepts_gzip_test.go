package gzipmw

// ─────────────────────────────────────────────────────────────────────────────
// Accept-Encoding q-value parser — RFC 9110 §12.5.3 / RFC 7231 §5.3.4
// ─────────────────────────────────────────────────────────────────────────────
//
// R5b — gzip Accept-Encoding q-value parsing fix (item 1 of RAM-residuals
// batch). These tests pin the new behavior of clientAcceptsGzip so that
// regressions on the literal-matching bug are caught in CI rather than at
// the next incident.
//
// The table is exhaustive over the categories the dispatcher asked for
// (RFC semantics, case/whitespace tolerance, malformed q, multi-coding
// lists, multi-header joining, wildcards/aliases). Each entry includes
// a short comment so future readers can see WHY the expected value is
// what it is without re-deriving the RFC.

import "testing"

// TestClientAcceptsGzip exercises clientAcceptsGzip against a table of
// Accept-Encoding values, asserting the new RFC-correct semantics.
//
// Coverage matrix (rows marked [CHANGED] would fail against the
// pre-fix literal parser; rows marked [POLICY] pin a malformed-input
// choice that is explicitly documented in the source).
func TestClientAcceptsGzip(t *testing.T) {
	cases := []struct {
		name    string
		header  string
		want    bool
		comment string
	}{
		// ── Empty / absent header ────────────────────────────────────
		{
			name:    "empty header",
			header:  "",
			want:    false,
			comment: "no header ⇒ no compression (RFC: absence = no coding accepted)",
		},

		// ── Plain gzip (positive cases) ──────────────────────────────
		{
			name:    "plain gzip",
			header:  "gzip",
			want:    true,
			comment: "canonical positive case",
		},
		{
			name:    "gzip with q=1",
			header:  "gzip;q=1",
			want:    true,
			comment: "explicit q=1 ⇒ compress",
		},
		{
			name:    "gzip with q=1.0",
			header:  "gzip;q=1.0",
			want:    true,
			comment: "q=1.0 (float form) ⇒ compress",
		},
		{
			name:    "gzip with fractional q",
			header:  "gzip;q=0.5",
			want:    true,
			comment: "non-zero fractional q ⇒ compress",
		},
		{
			name:    "gzip with tiny positive q",
			header:  "gzip;q=0.0001",
			want:    true,
			comment: "any q>0, however small ⇒ compress",
		},

		// ── q=0 explicit (the bug) ────────────────────────────────────
		{
			name:    "gzip;q=0",
			header:  "gzip;q=0",
			want:    false,
			comment: "[CHANGED] q=0 ⇒ MUST NOT compress",
		},
		{
			name:    "gzip;q=0.0",
			header:  "gzip;q=0.0",
			want:    false,
			comment: "[CHANGED] q=0.0 ⇒ MUST NOT compress",
		},
		{
			name:    "gzip;q=0.00",
			header:  "gzip;q=0.00",
			want:    false,
			comment: "[CHANGED] q=0.00 ⇒ MUST NOT compress (pre-fix only checked 0.0/0.00, missed 0.000)",
		},
		{
			name:    "gzip;q=0.000",
			header:  "gzip;q=0.000",
			want:    false,
			comment: "[CHANGED] q=0.000 ⇒ MUST NOT compress (pre-fix only checked 0/0.0/0.00)",
		},
		{
			name:    "gzip;q=0.0000",
			header:  "gzip;q=0.0000",
			want:    false,
			comment: "[CHANGED] q=0.0000 ⇒ MUST NOT compress (any extra-precision zero)",
		},

		// ── Case variation (tolerance) ───────────────────────────────
		{
			name:    "GZIP uppercase",
			header:  "GZIP",
			want:    true,
			comment: "[CHANGED] codec is case-insensitive (RFC 9110 §12.5.3)",
		},
		{
			name:    "GZip mixed case",
			header:  "GZip",
			want:    true,
			comment: "[CHANGED] codec is case-insensitive",
		},
		{
			name:    "gZip unusual case",
			header:  "gZip",
			want:    true,
			comment: "[CHANGED] codec is case-insensitive",
		},
		{
			name:    "GZIP;Q=0",
			header:  "GZIP;Q=0",
			want:    false,
			comment: "[CHANGED] uppercase Q parameter; pre-fix used params[2:] on original casing so Q=0 yielded qVal==\"=0\" and FAILED the zero check",
		},
		{
			name:    "GZIP; q=0.5",
			header:  "GZIP; q=0.5",
			want:    true,
			comment: "[CHANGED] case-insensitive codec + parameter",
		},

		// ── Whitespace tolerance ─────────────────────────────────────
		{
			name:    "gzip with leading/trailing space",
			header:  " gzip ",
			want:    true,
			comment: "outer trimSpace handles leading/trailing whitespace",
		},
		{
			name:    "gzip with space around semicolon and q",
			header:  " gzip ; q=0 ",
			want:    false,
			comment: "[CHANGED] whitespace around `;`, `=` and value all tolerated",
		},
		{
			name:    "gzip with space before q",
			header:  "gzip; q=0",
			want:    false,
			comment: "[CHANGED] pre-fix HasPrefix(lowered, \"q=\") rejected \" q=0\"",
		},
		{
			name:    "gzip with space before q (positive)",
			header:  "gzip; q=1",
			want:    true,
			comment: "whitespace around = tolerated on positive q too",
		},
		{
			name:    "gzip with space after =",
			header:  "gzip;q= 0",
			want:    false,
			comment: "whitespace after `=` tolerated",
		},
		{
			name:    "gzip with space inside value",
			header:  "gzip;q= 0 . 5 ",
			want:    true,
			comment: "trimSpace strips around value; inner spaces in number treated as malformed (no parse) ⇒ absent ⇒ q=1 ⇒ compress",
		},

		// ── Multiple codings in one header ───────────────────────────
		{
			name:    "deflate then gzip;q=0",
			header:  "deflate, gzip;q=0",
			want:    false,
			comment: "mixed list; gzip explicitly disabled ⇒ no compress",
		},
		{
			name:    "gzip;q=0 then deflate",
			header:  "gzip;q=0, deflate",
			want:    false,
			comment: "gzip first and disabled ⇒ no compress (no fallback to other codings)",
		},
		{
			name:    "deflate then gzip;q=1",
			header:  "deflate, gzip;q=1",
			want:    true,
			comment: "mixed list with gzip positive ⇒ compress",
		},
		{
			name:    "gzip;q=0.5 then br",
			header:  "gzip;q=0.5, br",
			want:    true,
			comment: "fractional q ⇒ compress",
		},
		{
			name:    "three codings, gzip;q=0 in middle",
			header:  "deflate, gzip;q=0, br",
			want:    false,
			comment: "gzip=q=0 disables even when bracketed by other codings",
		},
		{
			name:    "empty entries between commas",
			header:  ",, gzip ,,",
			want:    true,
			comment: "empty parts between commas are skipped",
		},

		// ── Malformed q-value (policy: treat as q=1) ────────────────
		{
			name:    "gzip;q=abc",
			header:  "gzip;q=abc",
			want:    true,
			comment: "[POLICY] malformed q (non-numeric) ⇒ treated as absent ⇒ q=1 ⇒ compress",
		},
		{
			name:    "gzip;q=2",
			header:  "gzip;q=2",
			want:    true,
			comment: "[POLICY] out-of-range q (>1) ⇒ treated as absent ⇒ q=1 ⇒ compress",
		},
		{
			name:    "gzip;q=-0.5",
			header:  "gzip;q=-0.5",
			want:    true,
			comment: "[POLICY] negative q ⇒ treated as absent ⇒ q=1 ⇒ compress",
		},
		{
			name:    "gzip;q=",
			header:  "gzip;q=",
			want:    true,
			comment: "[POLICY] empty q value ⇒ treated as absent ⇒ q=1 ⇒ compress",
		},
		{
			name:    "gzip;q",
			header:  "gzip;q",
			want:    true,
			comment: "[POLICY] bare q with no `=` ⇒ no value ⇒ treated as absent ⇒ q=1 ⇒ compress",
		},

		// ── Other q-form sanity (in-range forms) ────────────────────
		{
			name:    "gzip;q=0.7",
			header:  "gzip;q=0.7",
			want:    true,
			comment: "mid-range q ⇒ compress",
		},
		{
			name:    "gzip;q=1.000",
			header:  "gzip;q=1.000",
			want:    true,
			comment: "q=1.000 ⇒ compress",
		},

		// ── Other parameters alongside q ────────────────────────────
		{
			name:    "gzip with multiple params, q=0",
			header:  "gzip; level=9; q=0",
			want:    false,
			comment: "multiple params; q=0 still disables gzip",
		},
		{
			name:    "gzip with multiple params, q=1",
			header:  "gzip; level=9; q=1",
			want:    true,
			comment: "multiple params; positive q ⇒ compress",
		},
		{
			name:    "gzip with weird order",
			header:  "gzip; q=0; level=9",
			want:    false,
			comment: "parameter order does not matter; q=0 disables gzip",
		},

		// ── Non-gzip codings ─────────────────────────────────────────
		{
			name:    "identity only",
			header:  "identity",
			want:    false,
			comment: "identity is explicit no-coding ⇒ do not compress",
		},
		{
			name:    "identity;q=0",
			header:  "identity;q=0",
			want:    false,
			comment: "identity with q=0 — not relevant to gzip; identity is not gzip",
		},
		{
			name:    "br only",
			header:  "br",
			want:    false,
			comment: "client accepts only brotli — do not compress with gzip",
		},
		{
			name:    "br then gzip",
			header:  "br, gzip",
			want:    true,
			comment: "gzip appears positively in the list ⇒ compress",
		},
		{
			name:    "gzip then br;q=0",
			header:  "gzip, br;q=0",
			want:    true,
			comment: "gzip positive; br=q=0 is irrelevant",
		},

		// ── Wildcards and aliases (preserve pre-fix behavior) ───────
		{
			name:    "star wildcard",
			header:  "*",
			want:    false,
			comment: "current behavior: `*` is NOT honored (returns false); documented limitation",
		},
		{
			name:    "star wildcard with q=0",
			header:  "*;q=0",
			want:    false,
			comment: "current behavior: `*;q=0` returns false for the same reason",
		},
		{
			name:    "x-gzip alias",
			header:  "x-gzip",
			want:    false,
			comment: "current behavior: `x-gzip` alias NOT honored (only `gzip` recognized)",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := clientAcceptsGzip(tc.header)
			if got != tc.want {
				t.Errorf("clientAcceptsGzip(%q) = %v, want %v   // %s",
					tc.header, got, tc.want, tc.comment)
			}
		})
	}
}

// TestClientAcceptsGzip_ExtractQValue exercises the small extractQValue
// helper directly. It is a pure function (string → (float64, bool)) so
// a focused table is cheap and gives us per-param isolation when a
// regression lands.
func TestClientAcceptsGzip_ExtractQValue(t *testing.T) {
	cases := []struct {
		name     string
		params   string
		wantQ    float64
		wantHasQ bool
	}{
		{"empty params", "", 1.0, false},
		{"no q parameter", "level=9", 1.0, false},
		{"q=0", "q=0", 0.0, true},
		{"q=0.0", "q=0.0", 0.0, true},
		{"q=0.000", "q=0.000", 0.0, true},
		{"q=0.5", "q=0.5", 0.5, true},
		{"q=1", "q=1", 1.0, true},
		{"q=1.0", "q=1.0", 1.0, true},
		{"Q=0.5 case-insensitive", "Q=0.5", 0.5, true},
		{"Q = 0.5 with whitespace", "Q = 0.5", 0.5, true},
		{"q=abc malformed", "q=abc", 1.0, false}, // treated as absent
		{"q=2 out of range", "q=2", 1.0, false},  // treated as absent
		{"q=-0.5 negative", "q=-0.5", 1.0, false},
		{"q= empty value", "q=", 1.0, false},
		{"q alone no value", "q", 1.0, false},
		{"multiple params no q", "level=9; foo=bar", 1.0, false},
		{"multiple params with q=0", "level=9; q=0", 0.0, true},
		{"reversed param order", "q=0.7; level=9", 0.7, true},
		{"extra whitespace", "  q = 0.25  ", 0.25, true},
		{"trailing semicolons", "q=0.5;;", 0.5, true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			gotQ, gotHasQ := extractQValue(tc.params)
			if gotHasQ != tc.wantHasQ {
				t.Errorf("extractQValue(%q) hasQ = %v, want %v", tc.params, gotHasQ, tc.wantHasQ)
				return
			}
			// Compare q loosely — equal weights must compare equal.
			// 0.0 and 1.0 must compare exactly; fractional values are
			// produced from decimal ParseFloat so they compare exactly.
			if gotQ != tc.wantQ {
				t.Errorf("extractQValue(%q) q = %v, want %v", tc.params, gotQ, tc.wantQ)
			}
		})
	}
}