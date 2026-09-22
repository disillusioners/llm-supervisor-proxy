// Package memlimit resolves the process soft memory limit
// (runtime/debug.SetMemoryLimit) for non-k8s deployments — bare
// binaries, systemd units, and dev runs. k8s pods get GOMEMLIMIT from
// the helm chart (k8s/templates/deployment.yaml; goMemLimit default
// "1GiB" in k8s/values.yaml, ~50% of the 2Gi container limit — see
// docs/2026-09-22-fe-api-payload-ram-incident.md); everywhere else the
// limit used to be math.MaxInt64 (unbounded).
//
// Precedence contract (probe-verified against go1.26.0, see
// Resolve and TestParseByteCount):
//
//  1. GOMEMLIMIT (operator-set) — honored natively by the Go runtime
//     at startup, BEFORE main() runs. This package NEVER calls
//     SetMemoryLimit when GOMEMLIMIT is present, so the runtime stays
//     the sole owner and the limit is never double-applied. The
//     runtime grammar is ^[0-9]+(([KMGT]i)?B)?$ (decimal bytes,
//     optional suffix B/KiB/MiB/GiB/TiB — case-sensitive, no SI units,
//     no sign); "" and "off" both mean math.MaxInt64 (limit disabled);
//     any other malformed value is a FATAL runtime error before
//     main() ("fatal error: malformed GOMEMLIMIT", exit 2).
//  2. SOFT_MEMORY_LIMIT (project knob) — same grammar and "off" form
//     as GOMEMLIMIT. A malformed value is NOT fatal (a memory limit is
//     an optimization, not a liveness dependency): it is ignored with
//     a flagged Source line and the default below still applies.
//  3. DefaultLimit — 1 GiB, aligned with the k8s convention. This is
//     a SOFT limit: the GC paces toward it but the process may still
//     exceed it under real pressure, so a conservative default cannot
//     OOM a workload that would otherwise survive; it only asks the
//     GC to work harder as the heap approaches the limit (which also
//     improves GC pacing long before any OOM, per the go1.26
//     SetMemoryLimit doc).
package memlimit

import (
	"fmt"
	"strconv"
)

// EnvGOMEMLIMIT is the Go runtime's native soft-memory-limit variable.
const EnvGOMEMLIMIT = "GOMEMLIMIT"

// EnvSoftMemoryLimit is this project's override knob. It is only
// consulted when GOMEMLIMIT is unset (or empty), and accepts the same
// value grammar plus the "off" disable form.
const EnvSoftMemoryLimit = "SOFT_MEMORY_LIMIT"

// DefaultLimit is 1 GiB, aligned with the k8s chart convention
// (k8s/values.yaml goMemLimit: "1Gi").
const DefaultLimit int64 = 1 << 30

// offValue is the disable form shared by GOMEMLIMIT and
// SOFT_MEMORY_LIMIT. The runtime comparison is exact lowercase
// ("OFF" is malformed — verified go1.26.0).
const offValue = "off"

// Decision is the effective soft-memory-limit resolution.
type Decision struct {
	// Limit is the soft limit in bytes. Meaningful only when Apply
	// is true; callers must pass it to debug.SetMemoryLimit then.
	Limit int64
	// Source is a one-line, startup-log-ready description of the
	// effective decision (what won, what was skipped).
	Source string
	// Apply is true iff the caller must call debug.SetMemoryLimit.
	// False means someone else owns the limit (the Go runtime via
	// GOMEMLIMIT) or it is explicitly disabled ("off").
	Apply bool
}

// Resolve decides the effective soft memory limit from the raw
// GOMEMLIMIT and SOFT_MEMORY_LIMIT environment values ("" = unset).
// It is pure: no environment access, no runtime mutation.
func Resolve(gomemlimit, softlimit string) Decision {
	// GOMEMLIMIT present → the Go runtime already applied (or
	// fataled on) it at startup; never override or double-apply.
	if gomemlimit != "" {
		if gomemlimit == offValue {
			return Decision{
				Source: fmt.Sprintf("%s=off honored (soft memory limit disabled); in-code default skipped", EnvGOMEMLIMIT),
			}
		}
		if n, ok := parseByteCount(gomemlimit); ok {
			return Decision{
				Source: fmt.Sprintf("%s=%s honored by Go runtime (%s); in-code default skipped", EnvGOMEMLIMIT, gomemlimit, formatBytes(n)),
			}
		}
		// Unreachable in a live process — the runtime throws
		// "malformed GOMEMLIMIT" (fatal, exit 2) before main().
		// Kept so the never-override rule has no exception path.
		return Decision{
			Source: fmt.Sprintf("%s=%q present (owned by Go runtime; malformed values are fatal at runtime startup); in-code default skipped", EnvGOMEMLIMIT, gomemlimit),
		}
	}

	// GOMEMLIMIT unset → project knob.
	if softlimit != "" {
		if softlimit == offValue {
			return Decision{
				Source: fmt.Sprintf("%s=off: soft memory limit disabled", EnvSoftMemoryLimit),
			}
		}
		if n, ok := parseByteCount(softlimit); ok {
			return Decision{
				Limit:  n,
				Apply:  true,
				Source: fmt.Sprintf("soft memory limit %s (%s=%s)", formatBytes(n), EnvSoftMemoryLimit, softlimit),
			}
		}
		// Malformed project knob: not fatal (unlike GOMEMLIMIT),
		// but never silently unbounded either — fall through to the
		// default and flag the ignored value in the log line.
		return Decision{
			Limit: DefaultLimit,
			Apply: true,
			Source: fmt.Sprintf("invalid %s=%q ignored (expected e.g. 512MiB, 1GiB, 1000000, or \"off\"); soft memory limit %s (default)",
				EnvSoftMemoryLimit, softlimit, formatBytes(DefaultLimit)),
		}
	}

	// Neither set → k8s-aligned default.
	return Decision{
		Limit: DefaultLimit,
		Apply: true,
		Source: fmt.Sprintf("soft memory limit %s (default; set %s or %s to override, \"off\" to disable)",
			formatBytes(DefaultLimit), EnvSoftMemoryLimit, EnvGOMEMLIMIT),
	}
}

// parseByteCount mirrors the Go runtime's GOMEMLIMIT grammar
// (runtime/string.go parseByteCount, go1.26.0):
//
//	^[0-9]+(([KMGT]i)?B)?$
//
// decimal byte count with optional suffix B / KiB / MiB / GiB / TiB.
// Case-sensitive (no "1mib", no SI "1KB"), no sign, no whitespace.
// "0" is valid (limit of zero bytes — the runtime accepts it; GC runs
// nearly continuously, per the SetMemoryLimit doc).
func parseByteCount(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	// No suffix: plain decimal byte count.
	last := s[len(s)-1]
	if last >= '0' && last <= '9' {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < 0 {
			return 0, false
		}
		return n, true
	}
	// Otherwise the value must end in 'B' (bytes or binary unit).
	if last != 'B' || len(s) < 2 {
		return 0, false
	}
	// "...<digits>B" — plain byte count with B suffix.
	if c := s[len(s)-2]; c >= '0' && c <= '9' {
		n, err := strconv.ParseInt(s[:len(s)-1], 10, 64)
		if err != nil || n < 0 {
			return 0, false
		}
		return n, true
	} else if c != 'i' {
		return 0, false
	}
	// "...<K|M|G|T>iB" — needs at least 4 chars for unit + 1 digit.
	if len(s) < 4 {
		return 0, false
	}
	var power uint
	switch s[len(s)-3] {
	case 'K':
		power = 10
	case 'M':
		power = 20
	case 'G':
		power = 30
	case 'T':
		power = 40
	default:
		return 0, false
	}
	n, err := strconv.ParseInt(s[:len(s)-3], 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	if power > 0 && n > int64(^uint64(0)>>1)>>power { // n<<power must fit int64
		return 0, false
	}
	return n << power, true
}

// formatBytes renders a byte count with the largest exact binary
// suffix (TiB/GiB/MiB/KiB), falling back to a raw "B" count — e.g.
// 1073741824 → "1GiB", 268435456 → "256MiB", 12345 → "12345B".
func formatBytes(n int64) string {
	for _, u := range []struct {
		div  int64
		name string
	}{
		{1 << 40, "TiB"},
		{1 << 30, "GiB"},
		{1 << 20, "MiB"},
		{1 << 10, "KiB"},
	} {
		if n > 0 && n%u.div == 0 {
			return fmt.Sprintf("%d%s", n/u.div, u.name)
		}
	}
	return fmt.Sprintf("%dB", n)
}
