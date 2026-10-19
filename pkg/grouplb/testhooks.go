package grouplb

import (
	"time"
)

// Test-only seams (W8 / Am14 — cloned 1:1 from credentiallb so race
// tests are deterministic). ALL test hooks live in this file;
// production engine code (engine.go / binding.go / selector.go)
// never references them — `grep -n "ForTest" engine.go binding.go
// selector.go` returns zero hits, keeping the production/test
// boundary grep-greppable.
//
// These hooks exist so tests can exercise cooldown timing and drive
// every ReselectMode branch deterministically WITHOUT real sleeps (a
// constructor-arg cooldown TTL plus these injection points cover the
// matrix).

// InjectAllCoolingForTest seeds EVERY configured member of groupID
// into cooldown EXCEPT those listed in excludeIDs. The cooldown
// duration is the engine's defaultCooldown (constructor 4th arg), so
// a test engine built with defaultCooldown=50ms auto-expires quickly
// while one built with 60s persists.
//
// PERSISTENT semantics (carried): injected cooldowns stay until their
// TTL elapses (or the janitor sweeps them, or an explicit
// forceCooldownExpiryForTest clears them) — exactly like a production
// failure-seeded cooldown. They are NOT transient and do not
// auto-clear on the next engine call; multi-step assertions stay
// deterministic.
//
// Lets tests drive both ReselectSoonestExpiry (exclude nothing) and
// the partial-cooling fresh-pick scenario (exclude the healthy
// candidate).
func (e *Engine) InjectAllCoolingForTest(groupID string, excludeIDs ...string) {
	if e == nil {
		return
	}
	skip := make(map[string]bool, len(excludeIDs))
	for _, id := range excludeIDs {
		skip[id] = true
	}
	now := time.Now()

	e.mu.RLock()
	st := e.groups[groupID]
	e.mu.RUnlock()
	if st == nil {
		return
	}

	st.mu.Lock()
	defer st.mu.Unlock()
	for _, ref := range st.members {
		if skip[ref.ModelID] {
			continue
		}
		st.cooldowns[ref.ModelID] = now.Add(e.defaultCooldown)
	}
}

// cooldownUntil is the cooldown-state reader hook: it returns the
// cooldownUntil deadline for (groupID, memberID), or the zero
// time.Time when no cooldown row exists (absent or already swept).
func (e *Engine) cooldownUntil(groupID, memberID string) time.Time {
	if e == nil {
		return time.Time{}
	}
	e.mu.RLock()
	st := e.groups[groupID]
	e.mu.RUnlock()
	if st == nil {
		return time.Time{}
	}
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.cooldowns[memberID] // zero value when absent
}

// bindingBoundAtForTest is the binding-state reader hook — the #10
// sliding-idle-TTL counterpart of cooldownUntil above. It returns the
// binding's boundAt anchor for (groupID, convKey) and whether a
// binding row exists (absent, or lazily expired / janitor-swept).
// Read-only, referenced only from tests.
func (e *Engine) bindingBoundAtForTest(groupID, convKey string) (time.Time, bool) {
	if e == nil {
		return time.Time{}, false
	}
	e.mu.RLock()
	st := e.groups[groupID]
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
// (groupID, memberID) to the past so the read-time check treats it as
// expired immediately and the next janitor sweep removes the row
// (mirrors the E-4 panic-injection hook pattern).
func (e *Engine) forceCooldownExpiryForTest(groupID, memberID string) {
	if e == nil {
		return
	}
	e.mu.RLock()
	st := e.groups[groupID]
	e.mu.RUnlock()
	if st == nil {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if _, ok := st.cooldowns[memberID]; ok {
		st.cooldowns[memberID] = time.Now().Add(-time.Hour)
	}
}

// InjectPreconditionStateForTest primes the binding map so that
// bindings[convKey] = memberID (boundAt = now). Used to drive the B2
// precondition-WIN path (binding == excludedMemberID, rebind
// proceeds) AND the precondition-LOSE no-op path (binding !=
// excludedMemberID, ExcludeAndReselect returns the unchanged binding)
// deterministically, without racing two real requests.
func (e *Engine) InjectPreconditionStateForTest(groupID, convKey, memberID string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	st := e.groups[groupID]
	if st == nil {
		st = newGroupState(groupID, e.rngSeed)
		e.groups[groupID] = st
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if _, exists := st.bindings[convKey]; !exists {
		st.bindingsCount++
	}
	st.bindings[convKey] = &binding{memberID: memberID, boundAt: time.Now()}
}

// InjectBackupPreconditionStateForTest primes the BACKUP-pool
// binding map (backupBindings) so that backupBindings[convKey] =
// memberID (boundAt = now) — the backup-pool twin of
// InjectPreconditionStateForTest.
//
// group-backup-members / D8: the D8 tests need to place a binding
// in the BACKUP map to exercise the backup pool's selection-time
// existence check (a stale BACKUP binding whose member left the
// group). It also models the config-change→selection race window
// precisely: the membership is updated first (OnGroupChanged), THEN
// the in-flight request writes its binding against the old member —
// the push layer cannot see a row that does not exist yet.
func (e *Engine) InjectBackupPreconditionStateForTest(groupID, convKey, memberID string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	st := e.groups[groupID]
	if st == nil {
		st = newGroupState(groupID, e.rngSeed)
		e.groups[groupID] = st
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if _, exists := st.backupBindings[convKey]; !exists {
		st.backupBindingsCount++
	}
	st.backupBindings[convKey] = &binding{memberID: memberID, boundAt: time.Now()}
}

// InjectSingleMemberExclusionForTest truncates the group's engine
// state to its FIRST configured member (selector rebuilt, orphan
// bindings filtered like OnGroupChanged), priming the single-member
// exclusion case: a subsequent ExcludeAndReselect(groupID, ...,
// thatMemberID, ...) returns ReselectNone (no alternative exists).
// (Credentiallb twin: InjectSingleCredExclusionForTest — renamed for
// the member axis; the seam count and shape are identical.)
func (e *Engine) InjectSingleMemberExclusionForTest(groupID string) {
	if e == nil {
		return
	}
	e.mu.RLock()
	st := e.groups[groupID]
	e.mu.RUnlock()
	if st == nil {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.members) == 0 {
		return
	}
	only := st.members[0].ModelID
	st.members = append([]MemberRef(nil), st.members[:1]...)
	st.selector = newWeightedSelector(st.members)
	for key, b := range st.bindings {
		if b.memberID != only {
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

// sweepOnceForTest exposes one janitor pass synchronously (the
// production janitor is ticker-driven; tests must not sleep to
// observe TTL sweeps). Deterministic janitor coverage without
// waiting a real sweep interval.
func (e *Engine) sweepOnceForTest() {
	if e == nil {
		return
	}
	e.sweepOnce()
}

// bindingsCountForTest is the binding-map size reader for (groupID)
// — the O(1) mirror — used by survivor-filter and TTL-sweep
// assertions.
func (e *Engine) bindingsCountForTest(groupID string) int {
	if e == nil {
		return 0
	}
	e.mu.RLock()
	st := e.groups[groupID]
	e.mu.RUnlock()
	if st == nil {
		return 0
	}
	st.mu.RLock()
	defer st.mu.RUnlock()
	return int(st.bindingsCount)
}

// backupBindingsCountForTest is the BACKUP-pool twin of
// bindingsCountForTest — the O(1) mirror for the per-pool
// backup-bindings map (group-backup-members / D1). Used by the
// orphan-filter and TTL-sweep assertions on the backup path.
func (e *Engine) backupBindingsCountForTest(groupID string) int {
	if e == nil {
		return 0
	}
	e.mu.RLock()
	st := e.groups[groupID]
	e.mu.RUnlock()
	if st == nil {
		return 0
	}
	st.mu.RLock()
	defer st.mu.RUnlock()
	return int(st.backupBindingsCount)
}
