package credentiallb

import (
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// Test-only seams (W8). ALL test hooks live in this file; production
// engine code (engine.go / binding.go / selector.go) never references
// them — `grep -n "ForTest" engine.go binding.go selector.go` returns
// zero hits, keeping the production/test boundary grep-greppable.
//
// These hooks exist so Phase 5 and this phase's tests can exercise
// cooldown timing and drive every ReselectMode branch deterministically
// WITHOUT real sleeps (a constructor-arg cooldown TTL plus these
// injection points cover the matrix).

// InjectAllCoolingForTest seeds EVERY configured credential of modelID
// into cooldown EXCEPT those listed in excludeIDs (the variadic
// exclude list supersedes the Round-3b single-arg form). The cooldown
// duration is the engine's defaultCooldown (constructor 4th arg — W8),
// so a test engine built with defaultCooldown=50ms auto-expires
// quickly while one built with 60s persists.
//
// PERSISTENT semantics (Round 3c pin): injected cooldowns stay until
// their TTL elapses (or the janitor sweeps them, or an explicit
// forceCooldownExpiryForTest clears them) — exactly like a production
// 429-seeded cooldown. They are NOT transient and do not auto-clear on
// the next engine call; multi-step assertions stay deterministic.
//
// Lets tests drive both ReselectSoonestExpiry (exclude nothing) and
// the partial-cooling fresh-pick scenario (exclude the healthy
// candidate) without hand-crafting per-credential expirations.
func (e *Engine) InjectAllCoolingForTest(modelID string, excludeIDs ...string) {
	if e == nil {
		return
	}
	skip := make(map[string]bool, len(excludeIDs))
	for _, id := range excludeIDs {
		skip[id] = true
	}
	now := time.Now()

	e.mu.RLock()
	st := e.models[modelID]
	e.mu.RUnlock()
	if st == nil {
		return
	}

	st.mu.Lock()
	defer st.mu.Unlock()
	for _, ref := range st.credentials {
		if skip[ref.CredentialID] {
			continue
		}
		st.cooldowns[ref.CredentialID] = now.Add(e.defaultCooldown)
	}
}

// cooldownUntil is the cooldown-state reader hook: it returns the
// cooldownUntil deadline for (modelID, credID), or the zero time.Time
// when no cooldown row exists (absent or already swept).
func (e *Engine) cooldownUntil(modelID, credID string) time.Time {
	if e == nil {
		return time.Time{}
	}
	e.mu.RLock()
	st := e.models[modelID]
	e.mu.RUnlock()
	if st == nil {
		return time.Time{}
	}
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.cooldowns[credID] // zero value when absent
}

// bindingBoundAtForTest is the binding-state reader hook — the #10
// sliding-idle-TTL counterpart of cooldownUntil above. It returns
// the binding's boundAt anchor for (modelID, convKey) and whether a
// binding row exists (absent, or lazily expired / janitor-swept).
// Phase 5 Task 44 (W7-3) uses it to pin that an in-TTL GetOrSelect
// hit REFRESHES boundAt (strict increase); it is read-only, like
// cooldownUntil, and referenced only from tests.
func (e *Engine) bindingBoundAtForTest(modelID, convKey string) (time.Time, bool) {
	if e == nil {
		return time.Time{}, false
	}
	e.mu.RLock()
	st := e.models[modelID]
	e.mu.RUnlock()
	if st == nil {
		return time.Time{}, false
	}
	st.mu.RLock()
	defer st.mu.RUnlock()
	b, ok := st.bindings[convKey]
	if !ok {
		return time.Time{}, false
	}
	return b.boundAt, true
}

// forceCooldownExpiryForTest fast-forwards the cooldown row for
// (modelID, credID) to the past so the read-time check treats it as
// expired immediately and the next janitor sweep removes the row
// (mirrors the E-4 panic-injection hook pattern; Phase 5 uses it for
// the inert-lingering test).
func (e *Engine) forceCooldownExpiryForTest(modelID, credID string) {
	if e == nil {
		return
	}
	e.mu.RLock()
	st := e.models[modelID]
	e.mu.RUnlock()
	if st == nil {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if _, ok := st.cooldowns[credID]; ok {
		st.cooldowns[credID] = time.Now().Add(-time.Hour)
	}
}

// InjectPreconditionStateForTest primes the binding map so that
// bindings[convKey] = credentialID (boundAt = now). Used to drive the
// B2 precondition-WIN path (binding == excludedCredID, rebind
// proceeds) AND the precondition-LOSE no-op path (binding !=
// excludedCredID, ExcludeAndReselect returns the unchanged binding)
// deterministically, without racing two real requests.
func (e *Engine) InjectPreconditionStateForTest(modelID, convKey, credentialID string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	st := e.models[modelID]
	if st == nil {
		st = newModelState(modelID, e.rngSeed)
		e.models[modelID] = st
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if _, exists := st.bindings[convKey]; !exists {
		st.bindingsCount++
	}
	st.bindings[convKey] = &binding{credentialID: credentialID, boundAt: time.Now()}
}

// InjectSingleCredExclusionForTest truncates the model's engine state
// to its FIRST configured credential (selector rebuilt, orphan
// bindings filtered like OnModelChanged), priming the
// single-credential exclusion case: a subsequent
// ExcludeAndReselect(modelID, ..., thatCredID, ...) returns
// ReselectNone (no alternative exists).
func (e *Engine) InjectSingleCredExclusionForTest(modelID string) {
	if e == nil {
		return
	}
	e.mu.RLock()
	st := e.models[modelID]
	e.mu.RUnlock()
	if st == nil {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.credentials) == 0 {
		return
	}
	only := st.credentials[0].CredentialID
	st.credentials = append([]models.CredentialRef(nil), st.credentials[:1]...)
	st.selector = newWeightedSelector(st.credentials)
	for key, b := range st.bindings {
		if b.credentialID != only {
			delete(st.bindings, key)
			st.bindingsCount--
		}
	}
}

// InjectSweepPanicForTest installs fn as the janitor's sweep hook; fn
// is invoked at the start of every sweep INSIDE the defer/recover
// scope, so a panicking fn exercises the E-4 panic-recovery path
// (WARN + next sweep still runs). Pass nil to clear the hook.
func (e *Engine) InjectSweepPanicForTest(fn func()) {
	if e == nil {
		return
	}
	e.sweepHookMu.Lock()
	e.sweepHook = fn
	e.sweepHookMu.Unlock()
}
