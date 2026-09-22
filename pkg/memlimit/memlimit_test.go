package memlimit

import (
	"runtime/debug"
	"strings"
	"testing"
)

// TestResolve_GOMEMLIMITHasAbsolutePrecedence pins the core contract:
// whenever GOMEMLIMIT is present, Resolve NEVER returns Apply=true —
// the Go runtime honors GOMEMLIMIT natively at startup (before main),
// so an in-code SetMemoryLimit would double-apply or override the
// operator. This holds even when SOFT_MEMORY_LIMIT is ALSO set, and
// even for GOMEMLIMIT values the runtime itself rejects (malformed
// values are fatal at runtime startup — exit 2 before main runs — so
// the malformed branches below are contract pins for the resolver in
// isolation, not reachable production states).
func TestResolve_GOMEMLIMITHasAbsolutePrecedence(t *testing.T) {
	cases := []struct {
		name       string
		gomemlimit string
		note       string // substring the Source line must contain
	}{
		{"valid IEC value", "1GiB", "honored by Go runtime"},
		{"valid plain bytes", "1073741824", "honored by Go runtime"},
		{"zero is valid (runtime accepts 0)", "0", "honored by Go runtime"},
		{"off disables the limit", "off", "disabled"},
		{"malformed (fatal at runtime startup)", "bogus", "owned by Go runtime"},
		{"k8s quantity form is fatal at runtime startup", "1Gi", "owned by Go runtime"},
		{"uppercase off is malformed", "OFF", "owned by Go runtime"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := Resolve(tc.gomemlimit, "512MiB")
			if tc.gomemlimit != "" && d.Apply {
				t.Fatalf("GOMEMLIMIT=%q set but Apply=true — must never double-apply the runtime's limit", tc.gomemlimit)
			}
			if tc.gomemlimit != "" && d.Limit != 0 {
				t.Errorf("GOMEMLIMIT=%q set but Limit=%d — resolver must not produce a competing limit", tc.gomemlimit, d.Limit)
			}
			if tc.gomemlimit != "" && !strings.Contains(d.Source, tc.note) {
				t.Errorf("Source %q does not mention %q", d.Source, tc.note)
			}
			// gomemlimit == "" falls through to the SOFT_MEMORY_LIMIT
			// branch — covered by TestResolve_SoftMemoryLimit; the
			// both-unset default case is TestResolve_Default.
		})
	}
}

func TestResolve_SoftMemoryLimit(t *testing.T) {
	t.Run("valid values apply", func(t *testing.T) {
		cases := []struct {
			in   string
			want int64
		}{
			{"512MiB", 512 << 20},
			{"1GiB", 1 << 30},
			{"256MiB", 256 << 20},
			{"1000000", 1000000},
			{"64000000B", 64000000},
			{"0", 0}, // runtime-accepted pathological value, mirrored
		}
		for _, tc := range cases {
			d := Resolve("", tc.in)
			if !d.Apply {
				t.Errorf("SOFT_MEMORY_LIMIT=%q: Apply=false, want true (Source: %q)", tc.in, d.Source)
			}
			if d.Limit != tc.want {
				t.Errorf("SOFT_MEMORY_LIMIT=%q: Limit=%d, want %d", tc.in, d.Limit, tc.want)
			}
		}
	})

	t.Run("off disables", func(t *testing.T) {
		d := Resolve("", "off")
		if d.Apply {
			t.Errorf("SOFT_MEMORY_LIMIT=off: Apply=true, want false")
		}
		if !strings.Contains(d.Source, "disabled") {
			t.Errorf("Source %q does not say disabled", d.Source)
		}
	})

	t.Run("malformed falls back to default, never unbounded", func(t *testing.T) {
		for _, in := range []string{"1Gi", "bogus", "1mib", "1KB", "-5", " 1GiB", "8388608TiB"} {
			d := Resolve("", in)
			if !d.Apply {
				t.Errorf("SOFT_MEMORY_LIMIT=%q: Apply=false — malformed must fall back to default, not disable protection", in)
			}
			if d.Limit != DefaultLimit {
				t.Errorf("SOFT_MEMORY_LIMIT=%q: Limit=%d, want default %d", in, d.Limit, DefaultLimit)
			}
			if !strings.Contains(d.Source, "invalid") {
				t.Errorf("SOFT_MEMORY_LIMIT=%q: Source %q must flag the ignored value", in, d.Source)
			}
		}
	})
}

func TestResolve_Default(t *testing.T) {
	d := Resolve("", "")
	if !d.Apply {
		t.Fatalf("both envs unset: Apply=false, want true (k8s-aligned default must apply)")
	}
	if d.Limit != DefaultLimit {
		t.Errorf("both envs unset: Limit=%d, want %d (1GiB, k8s/values.yaml goMemLimit convention)", d.Limit, DefaultLimit)
	}
	if !strings.Contains(d.Source, "default") {
		t.Errorf("Source %q does not say default", d.Source)
	}
}

// TestParseByteCount pins the go1.26 runtime GOMEMLIMIT grammar
// (^[0-9]+(([KMGT]i)?B)?$ — case-sensitive, no SI units, no sign).
// Boundaries cross-checked against the live runtime on go1.26.0:
// 8388607TiB parses (= 9223370937343148032), 8388608TiB is malformed.
func TestParseByteCount(t *testing.T) {
	cases := []struct {
		in    string
		want  int64
		valid bool
	}{
		{"", 0, false},
		{"0", 0, true},
		{"1000000", 1000000, true},
		{"9223372036854775807", 9223372036854775807, true},
		{"9223372036854775808", 0, false}, // plain overflow
		{"-5", 0, false},                  // no sign
		{"0B", 0, true},
		{"64000000B", 64000000, true},
		{"1KiB", 1 << 10, true},
		{"1MiB", 1 << 20, true},
		{"1GiB", 1 << 30, true},
		{"1TiB", 1 << 40, true},
		{"512MiB", 512 << 20, true},
		{"8388607TiB", 9223370937343148032, true}, // MaxInt64 boundary (probe-verified)
		{"8388608TiB", 0, false},                  // unit overflow (probe-verified fatal)
		{"1iB", 0, false},                         // truncated unit
		{"KiB", 0, false},                         // no digits
		{"1 KB", 0, false},                        // whitespace
		{" 1GiB", 0, false},
		{"1GiB ", 0, false},
		// Case-sensitivity: the runtime grammar is uppercase-only.
		{"1mib", 0, false},
		{"1MIB", 0, false},
		{"1kib", 0, false},
		{"1tib", 0, false},
		// No SI units (KB/MB/GB/TB without the 'i').
		{"1KB", 0, false},
		{"1MB", 0, false},
		{"1GB", 0, false},
		{"1TB", 0, false},
		// k8s quantity forms are NOT valid GOMEMLIMIT values.
		{"1Gi", 0, false},
		{"512Mi", 0, false},
		{"1800Mi", 0, false},
	}
	for _, tc := range cases {
		got, ok := parseByteCount(tc.in)
		if ok != tc.valid {
			t.Errorf("parseByteCount(%q) ok=%v, want %v", tc.in, ok, tc.valid)
			continue
		}
		if ok && got != tc.want {
			t.Errorf("parseByteCount(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0B"},
		{12345, "12345B"},
		{1 << 10, "1KiB"},
		{256 << 20, "256MiB"},
		{1 << 30, "1GiB"},
		{1 << 40, "1TiB"},
		{9223370937343148032, "8388607TiB"},
	}
	for _, tc := range cases {
		if got := formatBytes(tc.in); got != tc.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestApplyDoesNotLeak exercises the exact statement cmd/main.go runs
// (if d.Apply { debug.SetMemoryLimit(d.Limit) }) against the real
// runtime, restoring the prior limit deterministically via t.Cleanup
// so no mutation leaks into other tests or the test process.
func TestApplyDoesNotLeak(t *testing.T) {
	prev := debug.SetMemoryLimit(-1)                 // save current limit
	t.Cleanup(func() { debug.SetMemoryLimit(prev) }) // deterministic restore

	d := Resolve("", "256MiB")
	if !d.Apply {
		t.Fatalf("SOFT_MEMORY_LIMIT=256MiB: Apply=false, want true")
	}
	debug.SetMemoryLimit(d.Limit)
	if got := debug.SetMemoryLimit(-1); got != d.Limit {
		t.Errorf("after apply: limit=%d, want %d", got, d.Limit)
	}

	// GOMEMLIMIT-set decisions never touch the runtime.
	d2 := Resolve("1GiB", "")
	if d2.Apply {
		t.Fatalf("GOMEMLIMIT set: Apply=true, want false")
	}
	if got := debug.SetMemoryLimit(-1); got != d.Limit {
		t.Errorf("non-apply decision mutated the runtime limit: %d, want %d", got, d.Limit)
	}
}
