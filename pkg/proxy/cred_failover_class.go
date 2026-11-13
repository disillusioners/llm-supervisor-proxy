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
	"errors"
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
// 401/auth errors match neither classifier — terminal for the LIMIT
// vocabulary. Part 2 (wi-7769a812) keeps this function as the
// LIMIT-CLASS classifier ONLY — see rotationEligible / the predicate
// split below for what changed about who consumes it.
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

// ─────────────────────────────────────────────────────────────────────────────
// PART-2 PREDICATE SPLIT (wi-7769a812 part 2 — spec items 5 + 6).
//
// classifyCredFailure used to drive BOTH decisions downstream:
//
//	(a) credential-rotation eligibility (credFailoverEligibleLocked —
//	    the Layer-1 ExcludeAndReselect branch), and
//	(b) the admission gate of the whole Case-1 site — so a failure
//	    outside the limit classes closed the gate entirely and a
//	    chain-end failure never reached the exhaust arm, while any
//	    client-attributable 4xx freely thrashed the pool.
//
// Part 2 splits the ONE implicit predicate into TWO explicit ones:
//
//	rotationEligible(err)      — P1 "is a credential worth rotating?"
//	    limit classes (unchanged) + upstream 401/403 (USER RULING #6:
//	    a pooled-credential rejection upstream IS a credential
//	    fault — rotate AND advance; the client-boundary 401/403
//	    never reaches this layer, it is answered at handler entry).
//	memberAdvanceBlocked(fr)   — P2 "must we STOP advancing past
//	    this member?" — the client-attributable fail-fast: 400 /
//	    413 / 415 / 422 / client-cancelled context. Everything else
//	    (5xx, timeouts, connection errors, limit classes, upstream
//	    auth) ADVANCES — including 5xx/timeout, which stay
//	    rotation-INELIGIBLE (P1 false) yet advance-worthy (P2 false):
//	    the two predicates are deliberately NOT complements.
// ─────────────────────────────────────────────────────────────────────────────

// upstreamAuthRejected reports whether the error carries an upstream
// 401/403 — the upstream rejected the CREDENTIAL we sent (pooled-
// credential rejection). Resolution: attempt HTTP status first (the
// executor stores it), then the ProviderError status. Pure function.
func upstreamAuthRejected(err error, storedStatus int) bool {
	if err == nil {
		return false
	}
	status := storedStatus
	if status == 0 {
		var pe *providers.ProviderError
		if errors.As(err, &pe) && pe != nil {
			status = pe.StatusCode
		}
	}
	return status == 401 || status == 403
}

// rotationEligible is P1 — the Layer-1 credential-rotation gate. The
// limit classes keep their exact pre-part-2 meaning (quota beats
// rate-limit; nothing else in the limit vocabulary). Upstream
// 401/403 is the ONE widening (user ruling #6): the failed
// credential is benched with the rate-limit cooldown policy (60s /
// Retry-After) via toEngineClass's non-quota default.
func rotationEligible(err error, storedStatus int) bool {
	return classifyCredFailure(err) != credFailoverNone || upstreamAuthRejected(err, storedStatus)
}

// clientAttributableStatuses — the 4xx family the CLIENT caused:
// malformed request (400), payload too large (413), unsupported media
// type (415), unprocessable content (422). Retrying the same bytes on
// another member is guaranteed thrash: the request body does not
// change between members, so the failure is attributable to the
// CLIENT payload, not the member's health. (401/403 deliberately
// ABSENT — upstream auth failures rotate+advance per ruling #6; 429
// is limit-class and feeds the skip cache instead.)
var clientAttributableStatuses = map[int]bool{
	400: true,
	413: true,
	415: true,
	422: true,
}

// memberAdvanceBlocked is P2 — "is this failure client-attributable,
// so advancing past the member (chain OR group pool) would be
// dishonest thrash?". Also true for a CLIENT-cancelled context (the
// request is gone; no member can serve it). 5xx, timeouts, limit
// classes, connection errors, proxy-internal failures, and upstream
// 401/403 all return FALSE — advance.
//
// fr is the classified failureReason (race_reason.go vocabulary);
// Status carries the upstream HTTP status when one is known.
func memberAdvanceBlocked(fr failureReason) bool {
	if fr.Class == reasonClassCancelled {
		return true
	}
	return clientAttributableStatuses[fr.Status]
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
