package credentiallb

import "time"

// ─────────────────────────────────────────────────────────────────────────────
// failure_class.go — the per-class cooldown policy consumed by the
// layered credential fallback.
//
// The engine owns the cooldown POLICY (it already owns defaultCooldown,
// the 60s rate-limit fallback); the callers own the CLASSIFICATION
// (pkg/providers.IsRateLimitError / IsQuotaError — pkg/credentiallb
// stays free of pkg/providers). ExcludeAndReselect keeps its
// rate-limit signature; the quota class flows through
// ExcludeAndReselectForClass / CooldownFor.
// ─────────────────────────────────────────────────────────────────────────────

// FailureClass selects the cooldown policy a failed credential is
// seeded with. The zero value is the rate-limit policy (the legacy
// default), so existing call sites and stored zero-values keep today's
// behavior byte-identical.
type FailureClass int

const (
	// FailureClassRateLimit is the 429-style "slow down" class:
	// Retry-After when the upstream supplied one, else the engine's
	// defaultCooldown (60s in production).
	FailureClassRateLimit FailureClass = iota
	// FailureClassQuota is the credit/plan-exhaustion class: the
	// engine's quotaCooldown (config-driven; default
	// DefaultQuotaCooldown = 15m), raised to the upstream Retry-After
	// when the upstream asked for LONGER. Rationale: a quota-exhausted
	// account recovers on a TOP-UP (hours/days), not on a short timer
	// — seeding 60s put the dead credential straight back into the
	// weighted pool (the cooldown trap).
	FailureClassQuota
)

// DefaultQuotaCooldown is the package-default cooldown for the quota
// class. 15 minutes: long enough to stop a per-request sticky binding
// from re-hitting an exhausted credential, short enough that a topped-up
// credential re-enters the pool within the same cooldown window. The
// config knob (config.json credential_quota_cooldown / env
// CREDENTIAL_QUOTA_COOLDOWN) overrides it per deployment via
// Engine.SetQuotaCooldown.
const DefaultQuotaCooldown = 15 * time.Minute

// Quota-cooldown clamp bounds, mirroring the config-side Validate()
// rules for credential_quota_cooldown ([1m, 24h] when set). The config
// clamp only fires on the Save() path — a hand-edited config.json / DB
// config_json row loads verbatim — so the engine seam re-enforces the
// same bounds defensively. The floor matters in exactly the direction
// the feature cares about: quota cooldowns must be LONG, not short.
//
// Exported so pkg/config's Validate() references this single source of
// truth instead of duplicating the literals.
const (
	// QuotaCooldownFloor is the minimum quota-class cooldown (1m).
	QuotaCooldownFloor = time.Minute
	// QuotaCooldownCeiling is the maximum quota-class cooldown (24h) —
	// also the ceiling an upstream Retry-After cannot exceed on the
	// quota branch.
	QuotaCooldownCeiling = 24 * time.Hour
)

// SetQuotaCooldown installs the engine's quota-class cooldown. Nil-safe.
// Values <= 0 reset to DefaultQuotaCooldown (a mis-wired or hand-edited
// config degrades to sane behavior — same discipline as NewEngine).
// Non-zero values are clamped to [QuotaCooldownFloor,
// QuotaCooldownCeiling] so a verbatim hand-edited config value (the
// Save()-only Validate() clamp never saw it) cannot seed a cooldown
// below the documented 1m floor or above 24h. Safe for concurrent use.
func (e *Engine) SetQuotaCooldown(d time.Duration) {
	if e == nil {
		return
	}
	if d <= 0 {
		d = DefaultQuotaCooldown
	}
	if d < QuotaCooldownFloor {
		d = QuotaCooldownFloor
	}
	if d > QuotaCooldownCeiling {
		d = QuotaCooldownCeiling
	}
	e.mu.Lock()
	e.quotaCooldown = d
	e.mu.Unlock()
}

// QuotaCooldown returns the engine's effective quota-class cooldown.
// Nil-safe; the package default when never set.
func (e *Engine) QuotaCooldown() time.Duration {
	if e == nil {
		return DefaultQuotaCooldown
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.quotaCooldown <= 0 {
		return DefaultQuotaCooldown
	}
	return e.quotaCooldown
}

// CooldownFor returns the cooldown the engine seeds for a credential
// that failed with the given class and upstream Retry-After:
//
//	rate-limit  — retryAfter when > 0, else defaultCooldown. An
//	              upstream instruction is honored VERBATIM (no ceiling —
//	              upstream said wait; only the quota class is clamped).
//	quota       — quotaCooldown, raised to retryAfter when the upstream
//	              asked for LONGER (max()). retryAfter is first clamped
//	              to QuotaCooldownCeiling so a hostile/typo'd quota
//	              Retry-After cannot bench a credential past the
//	              documented [QuotaCooldownFloor, QuotaCooldownCeiling]
//	              window.
//
// Pure function of (class, retryAfter, engine config) — no state
// mutation, no lock side effects beyond the config read. Nil-safe.
func (e *Engine) CooldownFor(class FailureClass, retryAfter time.Duration) time.Duration {
	if retryAfter < 0 {
		retryAfter = 0
	}
	if class == FailureClassQuota {
		if retryAfter > QuotaCooldownCeiling {
			retryAfter = QuotaCooldownCeiling
		}
		q := e.QuotaCooldown()
		if retryAfter > q {
			return retryAfter
		}
		return q
	}
	if retryAfter > 0 {
		return retryAfter
	}
	if e == nil {
		return DefaultCooldown
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.defaultCooldown <= 0 {
		return DefaultCooldown
	}
	return e.defaultCooldown
}
