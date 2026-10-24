package proxy

// ─────────────────────────────────────────────────────────────────────────────
// cred_failover_class.go — the credential-failover CLASS layer.
//
// The layered credential fallback (Fix 1) recognizes TWO eligible error
// classes:
//
//	rate_limit      — 429-style "slow down" (providers.IsRateLimitError).
//	                  Existing behavior; cooldown 60s / upstream Retry-After.
//	quota_exceeded  — credit/plan exhaustion (providers.IsQuotaError):
//	                  HTTP 402, code 2067, "Token Plan usage limit",
//	                  "insufficient credit", … . Cooldown is the LONG,
//	                  config-driven quota cooldown (default 15m) — a
//	                  quota-exhausted account recovers on a TOP-UP, not
//	                  on a short timer.
//
// Everything else (401/auth, 5xx, timeouts) stays INELIGIBLE for
// credential failover — eligibility is never widened beyond these two
// classes (investigation invariant).
// ─────────────────────────────────────────────────────────────────────────────

import (
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/credentiallb"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
)

// credFailoverClass classifies a failed upstream attempt into the
// credential-failover class vocabulary. The zero value is "none"
// (ineligible), so a zero-value struct field means "unclassified" —
// never "rate limit" by accident.
type credFailoverClass int

const (
	credFailoverNone credFailoverClass = iota
	credFailoverRateLimit
	credFailoverQuota
)

// String returns the event/log vocabulary for the class. The values are
// part of the operator-facing surface: model_credential_failover's
// `reason` payload key, the race_spawn `trigger` key, and the
// [LB-FAILOVER] server-log lines all carry them verbatim (the FE
// EventLog renders both).
func (c credFailoverClass) String() string {
	switch c {
	case credFailoverRateLimit:
		return "rate_limit"
	case credFailoverQuota:
		return "quota_exceeded"
	default:
		return ""
	}
}

// toEngineClass maps the proxy-side class onto the credentiallb engine's
// cooldown-policy class. The zero value maps to the rate-limit policy —
// the engine call sites only consult this after the class check has
// already narrowed to an eligible class, so the mapping is total on the
// reachable domain.
func (c credFailoverClass) toEngineClass() credentiallb.FailureClass {
	if c == credFailoverQuota {
		return credentiallb.FailureClassQuota
	}
	return credentiallb.FailureClassRateLimit
}

// readableClass renders the human-readable class label used by the
// [LB-FAILOVER] log lines (the machine vocabulary lives in String()).
func (c credFailoverClass) readable() string {
	switch c {
	case credFailoverRateLimit:
		return "rate-limited"
	case credFailoverQuota:
		return "quota-exceeded"
	default:
		return "failed"
	}
}

// classifyCredFailure maps a failed attempt's error onto the
// credential-failover class. QUOTA is checked BEFORE rate-limit: the
// quota body is the more specific signal, and an upstream that reports
// credit exhaustion (whatever the status code) recovers on a top-up —
// classifying it rate-limit would put a dead credential back into the
// pool after 60s (the cooldown trap this fix removes). A PLAIN 429 (no
// quota vocabulary anywhere in type/code/message) still classifies as
// rate_limit and keeps the existing 60s/Retry-After behavior.
//
// 401/auth errors match neither classifier — terminal, as required.
func classifyCredFailure(err error) credFailoverClass {
	if err == nil {
		return credFailoverNone
	}
	if providers.IsQuotaError(err) {
		return credFailoverQuota
	}
	if providers.IsRateLimitError(err) {
		return credFailoverRateLimit
	}
	return credFailoverNone
}

// effectiveCooldown returns the cooldown duration the engine will seed
// for the class + upstream Retry-After pair. It is a thin delegate to
// the nil-safe Engine.CooldownFor (pkg/credentiallb/failure_class.go —
// the single source of the per-class policy, pinned by
// TestCooldownFor_*):
//
//	rate_limit  — Retry-After when > 0, else the engine's default (60s).
//	quota       — the configured quota cooldown, raised to Retry-After
//	              when the upstream asked for LONGER (the configured
//	              value is the floor; an explicit upstream hint is
//	              honored upward only) — with the hint CLAMPED to the
//	              24h quota ceiling (credentiallb.QuotaCooldownCeiling),
//	              so a hostile/typo'd "Retry-After: 172800" cannot bench
//	              a credential past the documented [1m, 24h] window.
//
// Both production call sites wire a non-nil engine and bail on nil
// BEFORE reaching this helper (internal_handler.go: "err == nil ||
// h.credEngine == nil → return err"; race_coordinator.go:
// credFailoverEligibleLocked → "c.engine == nil → false"), so the
// delegation is unconditional — a nil engine still behaves sanely via
// CooldownFor's own nil-safe package defaults.
func effectiveCooldown(engine *credentiallb.Engine, class credFailoverClass, retryAfter time.Duration) time.Duration {
	return engine.CooldownFor(class.toEngineClass(), retryAfter)
}
