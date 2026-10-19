package grouplb

import (
	"errors"
	"hash/fnv"
	"log"
	"sync"
	"time"
)

// ErrNoMembers is returned by GetOrSelect when the group has no
// members configured (no groupState, empty list, or an invalid ref
// list that failed the defensive selector build). Sentinel mirrors
// credentiallb.ErrNoCredentials exactly in shape.
var ErrNoMembers = errors.New("grouplb: group has no members")

// Production defaults (plan §8 / Q3 USER-DECIDED — cloned from
// credentiallb's decision defaults). The owning ModelsManager
// constructs the engine with all three; tests pass short values
// through the same constructor.
const (
	DefaultBindingTTL     = 24 * time.Hour
	DefaultSweepInterval  = 5 * time.Minute
	DefaultCooldown       = 60 * time.Second
	DefaultMemberSeed     = int64(0) // 0 = time-based seed
	DefaultMaxGroupMember = 16       // informational mirror of models.MaxGroupMembers
)

// MemberRef is the engine's view of one group member: a real proxy
// MODEL ID with a positive float weight and its position (slice
// order). Zero credential awareness (plan §3.5 / Am31): no
// credential IDs, no provider, no composite keys.
//
// group-backup-members (D1): BackupOnly marks the member as a
// recourse-tier candidate picked ONLY after every default member
// in the request's exclusion set has failed. The engine splits the
// group into two selectors (defaultSel + backupSel) and two binding
// maps (bindings + backupBindings); pool choice never crosses.
// Carries the per-member `backup_only` JSON field with
// default-false; existing groups (no flags) round-trip identically.
type MemberRef struct {
	ModelID    string
	Weight     float64
	Position   int
	BackupOnly bool
}

// retryAfterIf returns retryAfter when > 0, else the fallback —
// used by the single-backup guard so the mark-the-cooldown step
// honors whichever duration the caller passed (retryAfter from the
// race coordinator or the engine's defaultCooldown).
func retryAfterIf(retryAfter, fallback time.Duration) time.Duration {
	if retryAfter > 0 {
		return retryAfter
	}
	return fallback
}

// Engine maintains (groupID, conversationKey) → memberID bindings
// with sliding-idle-TTL eviction and weighted-random selection for
// new bindings, plus the per-(group, member) cooldown map driving
// group-level exclude-and-reselect (Am24).
//
// Mechanical clone of credentiallb.Engine reduced to the member axis:
// concurrency-safe, all exported methods thread-safe and nil-safe on
// a nil receiver.
//
// Lifecycle:
//
//	e := NewEngine(ttl, sweepInterval, rngSeed, defaultCooldown)
//	defer e.Stop()
//
// Per-group state has TWO axes — both non-lazy at boot, but only
// one is non-lazy at write-time:
//
//  1. PER-GROUP SELECTOR (cumulative prefix weights + member
//     refs): NOT lazy at boot. Every group Add/Update on the
//     owning ModelsManager calls notifyGroupEngineLocked →
//     OnGroupChanged immediately (P2.2 design — load-bearing
//     for the Am28 survivor filter on member edits; verified and
//     approved at P2 review). HasGroup returns true for any
//     group that has been AddModel'd, even before any request
//     has been served.
//
//  2. PER-CONVERSATION BINDINGS (sticky affinity): LAZY. Created
//     on the first GetOrSelect call per (groupID, conversationKey)
//     (W-1 / W-2) and TTL-evicted by the janitor. Empty-key
//     requests return NewlyBound=false and store NO binding
//     (C2 — Q15 model_member_selected surfaces newlyBound-only).
//
// Manager construction/boot performs NO rehydration walk — a
// freshly NewModelsManager'd grouplb.Engine has zero entries.
// The Am15 invariant is "no boot rebind", not "no eager seed":
// the per-group SELECTOR is eager at write-time (P2.2), the
// per-conversation BINDINGS are lazy at request-time (W-1).
type Engine struct {
	mu     sync.RWMutex // outer lock: guards the groups map
	groups map[string]*groupState

	ttl             time.Duration // sliding IDLE TTL per binding (#10)
	sweepInt        time.Duration // janitor cadence
	defaultCooldown time.Duration // fallback cooldown when retryAfter <= 0
	rngSeed         int64

	stopCh    chan struct{} // janitor shutdown
	sweepDone chan struct{} // janitor exit ack
	stopOnce  sync.Once

	// sweepHook is a nil-by-default janitor extension point — the
	// SANCTIONED test-seam exception cloned 1:1 from credentiallb
	// (E-4 panic-recovery). Production engine code (this file,
	// binding.go, selector.go) carries only the hook VARIABLE; no
	// symbol named with the test-helper suffix appears here and no
	// public method exposes the hook. See testhooks.go for the
	// consumer-side install function.
	sweepHookMu sync.Mutex
	sweepHook   func()
}

// NewEngine constructs an Engine. ttl is the sliding idle TTL on each
// binding (production 24h); sweepInterval the janitor cadence
// (production 5m); rngSeed seeds the per-group weighted-random RNGs
// (0 = time-based); defaultCooldown seeds cooldowns when
// ExcludeAndReselect's retryAfter <= 0 (production 60s).
//
// Non-positive arguments fall back to the production defaults so a
// mis-wired constructor degrades to sane behavior. Starts a
// background janitor goroutine; call Stop() to terminate it. After
// Stop, GetOrSelect still works (lazy expiry only).
func NewEngine(ttl, sweepInterval time.Duration, rngSeed int64, defaultCooldown time.Duration) *Engine {
	if ttl <= 0 {
		ttl = DefaultBindingTTL
	}
	if sweepInterval <= 0 {
		sweepInterval = DefaultSweepInterval
	}
	if defaultCooldown <= 0 {
		defaultCooldown = DefaultCooldown
	}
	e := &Engine{
		groups:          make(map[string]*groupState),
		ttl:             ttl,
		sweepInt:        sweepInterval,
		defaultCooldown: defaultCooldown,
		rngSeed:         rngSeed,
		stopCh:          make(chan struct{}),
		sweepDone:       make(chan struct{}),
	}
	go e.runJanitor()
	return e
}

// groupRNGSeed derives the per-group RNG seed: engine seed XORed
// with the FNV-1a hash of the group ID (E-4 carried). A zero
// (time-based) engine seed is mixed with the wall clock so concurrent
// engines in tests do not share sequences.
func groupRNGSeed(rngSeed int64, groupID string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(groupID))
	mix := int64(h.Sum64())
	if rngSeed == 0 {
		return mix ^ time.Now().UnixNano()
	}
	return rngSeed ^ mix
}

// runJanitor is the single background sweep goroutine (R3-4 carried):
// sweeps BOTH expired cooldowns and idle-expired bindings in ONE
// pass, under the E-1 discipline: outer RLock for the whole walk +
// per-group write locks one at a time, so reads proceed concurrently
// with the sweep.
func (e *Engine) runJanitor() {
	defer close(e.sweepDone)
	ticker := time.NewTicker(e.sweepInt)
	defer ticker.Stop()
	for {
		select {
		case <-e.stopCh:
			return
		case <-ticker.C:
			e.sweepOnce()
		}
	}
}

// sweepOnce performs one janitor pass wrapped in defer/recover + WARN
// (E-4 hardening carried): a panic no longer silently stops the
// janitor — the next tick runs another sweep. Sweep order within a
// group: cooldowns first (cheaper map), then bindings (sliding-TTL
// check).
func (e *Engine) sweepOnce() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[WARN] [grouplb] janitor sweep recovered from panic: %v", r)
		}
	}()
	if hook := e.getSweepHook(); hook != nil {
		hook()
	}
	now := time.Now()
	e.mu.RLock() // outer RLock for the WHOLE walk (E-1)
	for _, st := range e.groups {
		st.mu.Lock() // per-group write lock, one at a time
		for memberID, until := range st.cooldowns {
			if !until.After(now) { // expired iff cooldownUntil <= now
				delete(st.cooldowns, memberID)
			}
		}
		evicted := 0
		for key, b := range st.bindings {
			if b.expired(e.ttl, now) {
				delete(st.bindings, key)
				st.bindingsCount--
				evicted++
			}
		}
		for key, b := range st.backupBindings {
			if b.expired(e.ttl, now) {
				delete(st.backupBindings, key)
				st.backupBindingsCount--
				evicted++
			}
		}
		st.mu.Unlock()
		if evicted > 0 {
			log.Printf("[DEBUG] [grouplb] janitor evicted %d idle bindings for group state", evicted)
		}
	}
	e.mu.RUnlock()
}

func (e *Engine) getSweepHook() func() {
	e.sweepHookMu.Lock()
	defer e.sweepHookMu.Unlock()
	return e.sweepHook
}

// HasGroup reports whether the engine currently holds per-group
// SELECTOR state for groupID. The two-axis state model
// distinguishes:
//
//   - HAS-GROUP (= true) means the per-group SELECTOR (cumulative
//     prefix weights + member refs) is registered. This is seeded
//     by every group Add/Update write via notifyGroupEngineLocked →
//     OnGroupChanged (P2.2) and is NOT lazy at boot — HasGroup
//     returns true for any group that has been AddModel'd.
//
//   - BINDINGS COUNT (= 0 means none yet) is the per-conversation
//     sticky-affinity state, lazily created on first GetOrSelect.
//     See bindingsCountForTest for the reader.
//
// The Am15 boot-lazy invariant is specifically: NewModelsManager
// performs NO rehydration walk — HasGroup returns false for every
// persisted group row until a write-path notification (or the
// request-path lazy seed in pickServableGroupMember) registers it.
// load-balance-groups / P5 / am29 case-(e) boot-lazy pin asserts
// exactly this invariant: after a fresh NewModelsManager over a
// file DSN that holds persisted group rows, HasGroup returns false
// for the persisted group IDs.
func (e *Engine) HasGroup(groupID string) bool {
	if e == nil {
		return false
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	_, ok := e.groups[groupID]
	return ok
}

// HasBackupPool reports whether the configured group has any
// BACKUP-pool members (group-backup-members / D1). False for a
// pure-default group; true once one or more members carry
// BackupOnly=true. Nil receiver / unknown group ⇒ false.
func (e *Engine) HasBackupPool(groupID string) bool {
	if e == nil {
		return false
	}
	e.mu.RLock()
	st := e.groups[groupID]
	e.mu.RUnlock()
	if st == nil {
		return false
	}
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.backupSelector != nil && st.backupSelector.totalWeight > 0
}

// DefaultPoolHealthy reports whether the group's DEFAULT pool has at
// least one non-cooling member as of now (group-backup-members / C1).
//
// D1 says backups engage when every default member has failed; a
// default under cooldown is exactly that. The manager's two-pool
// picker consults this ONLY when HasBackupPool is true — a
// backup-less group keeps the v1 all-cooling availability ruling
// (serve the soonest-expiring default) byte-identically (D7).
//
// Nil receiver / unknown group / empty default pool ⇒ false.
func (e *Engine) DefaultPoolHealthy(groupID string) bool {
	if e == nil {
		return false
	}
	e.mu.RLock()
	st := e.groups[groupID]
	e.mu.RUnlock()
	if st == nil {
		return false
	}
	now := time.Now()
	st.mu.RLock()
	defer st.mu.RUnlock()
	// C2: selector/ref reads hold st.mu (applyRefsLocked replaces
	// both under the write lock).
	if st.selector == nil || st.selector.totalWeight <= 0 {
		return false // empty default pool
	}
	for _, ref := range st.defaultMembers {
		if !st.isCoolingLocked(ref.ModelID, now) {
			return true
		}
	}
	return false
}

// GetOrSelect returns the member pinned to (groupID, conversationKey)
// if a fresh binding exists; otherwise picks a new member via
// weighted random (among non-cooling, renormalized), persists the
// binding, and returns it.
//
// W-1 carried: returns (memberID, newlyBound, err) where
// newlyBound=true ⇔ a fresh binding was created by THIS call;
// newlyBound=false ⇔ an existing in-TTL binding was reused, OR the
// single-member fast path returned directly, OR an empty-key call
// returned a fresh pick (C2: newlyBound is bound to the binding-store
// side effect, NOT the pick side effect).
//
// Invariants (all carried verbatim from the credentiallb contract,
// member axis):
//   - Returns ("", false, ErrNoMembers) when the group has no members
//     configured (no state, empty list, or invalid refs).
//   - Single-member fast path (E-3): returns members[0] directly with
//     NO map writes — byte-identical to pre-group behavior, zero
//     engine overhead. The fast path ignores cooldowns (a
//     single-member group never has an alternative;
//     ExcludeAndReselect returns ReselectNone for it).
//   - Binding lookup applies the #10 sliding-idle check, the
//     defensive still-in-config check, and the skip-cooling check (a
//     bound member that went cooling is dropped and re-picked —
//     selection NEVER returns a cooling member except the explicit
//     all-cooling soonest-expiry path below).
//   - Empty conversationKey (W-2): NO binding stored, fresh weighted
//     pick per call, newlyBound=false.
//   - All-cooling (F.4 availability ruling): when every configured
//     member is cooling, returns the SOONEST-expiring member with
//     newlyBound=false and NO binding stored, emitting the
//     [LB-FAILOVER] WARN. Availability beats strict cooldown.
//   - Nil receiver: returns ErrNoMembers (defensive; the manager
//     layer guards the nil-engine path before calling).
//
// group-backup-members / D8: the bound member is resolved against
// the CURRENT membership — the UNION of both pools (default ∪
// backup), not the pool it was bound in. A member that merely
// CHANGED POOLS (the backup_only flag flipped) still exists, so
// its binding is grandfathered (continuity); a member that LEFT
// the group does not, so the binding is discarded and the pick is
// re-made (the `binding=stale` token is emitted). The detailed
// variant GetOrSelectWithRepair reports that repair to the caller;
// GetOrSelect keeps its original 3-tuple signature.
func (e *Engine) GetOrSelect(groupID, conversationKey string) (memberID string, newlyBound bool, err error) {
	out := e.getOrSelect(groupID, conversationKey)
	return out.memberID, out.newlyBound, out.err
}

// GetOrSelectWithRepair is the repair-aware variant of GetOrSelect:
// it additionally reports whether a STALE binding (one whose member
// left the group) was discarded during this call (group-backup-
// members / D8). The member / newlyBound / error triple is
// identical to GetOrSelect's.
//
// staleRepaired=true ⇔ this call found a binding for conversationKey
// whose member is no longer in the group's membership (the UNION of
// both pools) and re-picked a live member.
func (e *Engine) GetOrSelectWithRepair(groupID, conversationKey string) (memberID string, newlyBound bool, staleRepaired bool, err error) {
	out := e.getOrSelect(groupID, conversationKey)
	return out.memberID, out.newlyBound, out.staleRepaired, out.err
}

// pickOutcome is the internal rich result of one pool selection.
// It exists so the public GetOrSelect / GetOrSelectWithRepair
// pair can share ONE implementation (the D8 repair signal is
// additive — the legacy 3-tuple signature must not change).
type pickOutcome struct {
	memberID   string
	newlyBound bool
	// staleRepaired is set when a binding whose member left the
	// group was discarded during this call (D8).
	staleRepaired bool
	err           error
}

// getOrSelect is the shared implementation behind GetOrSelect and
// GetOrSelectWithRepair. See GetOrSelect's doc comment for the
// full contract.
func (e *Engine) getOrSelect(groupID, conversationKey string) pickOutcome {
	if e == nil {
		return pickOutcome{err: ErrNoMembers}
	}
	now := time.Now()

	e.mu.RLock()
	st := e.groups[groupID]
	e.mu.RUnlock()

	if st == nil {
		return pickOutcome{err: ErrNoMembers}
	}
	n := st.memberCount()
	if n == 0 {
		return pickOutcome{err: ErrNoMembers}
	}

	// E-3 fast path: single total-member group → return members[0]
	// without engaging cooldowns. The D2 ≥1-default validation lives
	// at the write path, so a single-member group normally holds a
	// default member; backups live separately in backupMembers and
	// are never returned through GetOrSelect.
	//
	// D8 (defensive): the fast path is DEFAULT-POOL ONLY. A group
	// whose single member is backup-flagged has an EMPTY default
	// pool — the picker must report "the default pool is exhausted"
	// (ErrNoMembers) so the manager falls through to the backup
	// pool, never serve a backup from the default entry point. The
	// state is unreachable via the API (D2 blocks zero-default
	// writes) but the engine handles it defensively.
	if n == 1 {
		st.mu.RLock()
		id := st.members[0].ModelID
		isBackup := st.members[0].BackupOnly
		st.mu.RUnlock()
		if !isBackup {
			return pickOutcome{memberID: id}
		}
		// Single BACKUP-only member — fall through to the full path
		// (which reports the default pool as exhausted).
	}

	// Happy path (99%): optimistic read under groupState RLock.
	if conversationKey != "" {
		st.mu.RLock()
		hitMember, hit := "", false
		if b, ok := st.bindings[conversationKey]; ok &&
			!b.expired(e.ttl, now) &&
			memberInRefs(st.members, b.memberID) &&
			!st.isCoolingLocked(b.memberID, now) {
			hitMember, hit = b.memberID, true
		}
		st.mu.RUnlock()
		if hit {
			// Upgrade to the write lock for the #10 boundAt refresh,
			// re-verifying the binding survived the lock transition
			// AND has not been marked cooling in the gap (Item 1
			// TOCTOU carried).
			st.mu.Lock()
			if b, ok := st.bindings[conversationKey]; ok &&
				b.memberID == hitMember &&
				!b.expired(e.ttl, now) &&
				!st.isCoolingLocked(b.memberID, now) {
				b.boundAt = now
				st.mu.Unlock()
				return pickOutcome{memberID: hitMember}
			}
			st.mu.Unlock()
			// Raced out between the locks — fall through to the full
			// path.
		}
	}

	// Full path under the per-group write lock: miss, lazy expiry,
	// stale/cooling eviction, first selection, empty-key pick.
	st.mu.Lock()
	defer st.mu.Unlock()

	staleRepaired := false
	if conversationKey != "" {
		if b, ok := st.bindings[conversationKey]; ok {
			// D8: the existence test is UNION-scoped (st.members
			// holds default ∪ backup) — a member that merely
			// changed pools still exists, so its binding is
			// grandfathered; a member that LEFT the group does
			// not, so the binding is discarded and re-picked.
			exists := memberInRefs(st.members, b.memberID)
			valid := !b.expired(e.ttl, now) &&
				exists &&
				!st.isCoolingLocked(b.memberID, now)
			if valid {
				// Another goroutine bound this key between our
				// phases; treat as a hit (W-1: not newly bound by
				// THIS call).
				b.boundAt = now
				return pickOutcome{memberID: b.memberID}
			}
			if !exists {
				// D8: the bound member is GONE (removed from the
				// group) — the binding cannot be honoured. The
				// token mirrors the D5 `pool=backup` idiom; it is
				// bounded + redacted by construction (three
				// internal identifiers, no free text).
				staleRepaired = true
				log.Printf("[grouplb] group=%s conv=%s binding=stale member=%s (member no longer configured) — re-picking",
					groupID, conversationKey, b.memberID)
			}
			// Stale / idle-expired / orphaned / cooling: drop lazily.
			// Guarded by the row-presence check ⇒ the count
			// decrement happens at most once per row, so the
			// OnGroupChanged push-layer drop and this pull-layer
			// drop can never double-drop (D8 composition rule).
			delete(st.bindings, conversationKey)
			st.bindingsCount--
		}
	}

	pick := st.pickHealthyLocked(now, st.selector, st.defaultMembers)
	if pick == "" {
		// All-cooling (F.4): soonest-expiring pick, selection-only.
		soonest, allCooling := st.soonestCooldownLocked(now, st.defaultMembers)
		if !allCooling || soonest == "" {
			// Refs went invalid under us (race with OnGroupChanged).
			// D8: an empty DEFAULT pool is "the default pool is
			// exhausted" — the caller (the manager's two-pool
			// picker) falls through to the backup pool.
			return pickOutcome{staleRepaired: staleRepaired, err: ErrNoMembers}
		}
		log.Printf("[LB-FAILOVER] group=%s all members cooling; picking soonest-expiring member=%s", groupID, soonest)
		return pickOutcome{memberID: soonest, staleRepaired: staleRepaired}
	}

	if conversationKey == "" {
		// W-2: no binding stored for the empty key.
		return pickOutcome{memberID: pick, staleRepaired: staleRepaired}
	}

	st.bindings[conversationKey] = &binding{memberID: pick, boundAt: now}
	st.bindingsCount++
	log.Printf("[grouplb] group=%s conv=%s→member=%s newlyBound=true", groupID, conversationKey, pick)
	return pickOutcome{memberID: pick, newlyBound: true, staleRepaired: staleRepaired}
}

// OnGroupChanged rebuilds the per-group selector (cumulative prefix
// sums over the new weights) and FILTERS existing bindings (E-2
// survivor filter): preserves any binding whose memberID still
// appears in refs — keeping its boundAt untouched (the sliding idle
// window survives a weight nudge); drops only orphan bindings (whose
// member was removed). This is NOT clear-all: routine weight changes
// preserve affinity for every surviving conversation.
//
// Cooldowns are deliberately NOT touched here (contract carried):
// orphan cooldown rows linger inertly until the janitor sweeps their
// expiry.
//
// refs == nil (or empty) means the group was removed (or has no
// members): the group's state is deleted outright (RemoveModel and
// member-empty writes wire this).
//
// Nil receiver: no-op.
func (e *Engine) OnGroupChanged(groupID string, refs []MemberRef) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(refs) == 0 {
		delete(e.groups, groupID)
		return
	}
	st := e.groups[groupID]
	if st == nil {
		st = newGroupState(groupID, e.rngSeed)
		e.groups[groupID] = st
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	e.applyRefsLocked(st, refs)
	// group-backup-members / D1: the survivor filter runs against
	// the FULL surviving set (default + backup) so an
	// editor-driven change that removes a backup member drops its
	// binding the same way a removed default does (no orphan
	// bindings linger on either side).
	surviving := make(map[string]bool, len(refs))
	for _, r := range refs {
		surviving[r.ModelID] = true
	}
	for key, b := range st.bindings {
		if !surviving[b.memberID] {
			delete(st.bindings, key)
			st.bindingsCount--
			log.Printf("[grouplb] group=%s dropped orphan binding conv=%s→member=%s (member no longer configured)", groupID, key, b.memberID)
		}
	}
	for key, b := range st.backupBindings {
		if !surviving[b.memberID] {
			delete(st.backupBindings, key)
			st.backupBindingsCount--
			log.Printf("[grouplb] group=%s dropped orphan backup binding conv=%s→member=%s (member no longer configured)", groupID, key, b.memberID)
		}
	}
}

// applyRefsLocked replaces the group's member list + selectors per
// the partition (default + backup). Caller holds e.mu (write) and
// st.mu (write).
//
// Partition discipline (group-backup-members / D1): a ref with
// BackupOnly=false goes into the default slice (members + selector);
// a ref with BackupOnly=true goes into the backup slice
// (backupMembers + backupSelector). Each pool's selector is built
// independently with its own weighted prefix sums — so a request
// that consumes the entire default pool falls to a backup pool whose
// weights balance among themselves.
func (e *Engine) applyRefsLocked(st *groupState, refs []MemberRef) {
	defaults := make([]MemberRef, 0, len(refs))
	backups := make([]MemberRef, 0, len(refs))
	for _, r := range refs {
		if r.BackupOnly {
			backups = append(backups, r)
		} else {
			defaults = append(defaults, r)
		}
	}
	// Defensive copy of the FULL slice (diagnostic / survivor filter
	// only; the partition's own slices are the read-paths' source).
	st.members = append([]MemberRef(nil), refs...)
	st.defaultMembers = append([]MemberRef(nil), defaults...)
	st.selector = newWeightedSelector(defaults)
	st.backupMembers = append([]MemberRef(nil), backups...)
	st.backupSelector = newWeightedSelector(backups)
}

// Stop terminates the janitor goroutine. Idempotent (sync.Once +
// closed-channel ack). After Stop, GetOrSelect still works (lazy
// expiry only) but the janitor no longer sweeps. Nil receiver: no-op.
func (e *Engine) Stop() {
	if e == nil {
		return
	}
	e.stopOnce.Do(func() {
		close(e.stopCh)
	})
	<-e.sweepDone // ack (immediate on repeat calls: channel closed)
}

// ReselectMode describes the outcome of ExcludeAndReselect (clone of
// the credentiallb B3/C2 contract, member axis). The engine picks the
// mode; the caller enforces the fall-through contract based on it.
//
//   - ReselectHealthy: a member is available for THIS call (fresh
//     weighted pick among non-cooling, the B2 no-op sub-case
//     returning the current unchanged binding, or the empty-key
//     fresh pick). Caller MUST proceed.
//   - ReselectSoonestExpiry: ALL members are cooling; the engine
//     picked the soonest-expiring member and emitted the WARN.
//     Caller MUST treat this as a SINGLE attempt only.
//   - ReselectNone: NO member is available — single-member group (no
//     alternative exists) or genuinely 0 valid candidates. Caller
//     falls through to the real failure path.
type ReselectMode int

const (
	// ReselectHealthy: see the ReselectMode doc comment.
	ReselectHealthy ReselectMode = iota
	// ReselectSoonestExpiry: see the ReselectMode doc comment.
	ReselectSoonestExpiry
	// ReselectNone: see the ReselectMode doc comment.
	ReselectNone
)

// String renders the mode for logs and test failure messages.
func (m ReselectMode) String() string {
	switch m {
	case ReselectHealthy:
		return "ReselectHealthy"
	case ReselectSoonestExpiry:
		return "ReselectSoonestExpiry"
	case ReselectNone:
		return "ReselectNone"
	default:
		return "ReselectUnknown"
	}
}

// ExcludeAndReselect marks excludedMemberID as cooling for groupID
// (cooldown seeded from retryAfter when > 0, else the engine's
// defaultCooldown — 60s in production), then REBINDS the
// (groupID, conversationKey) binding to the next healthy member
// chosen by weighted random among non-cooling (renormalized).
//
// B2 PRECONDITION (concurrent double-failure idempotency, carried):
// the rebind happens ONLY if the current binding's memberID ==
// excludedMemberID. If the current binding already points at a
// different member (a concurrent same-conversation request already
// rebinded away), this call is a NO-OP returning
// (currentMemberID, ReselectHealthy). The excluded member is still
// marked cooling (it genuinely failed).
//
// Mode semantics (B3 / C2 carried): ReselectHealthy on fresh pick /
// B2 no-op / empty-key fresh pick; ReselectSoonestExpiry when every
// member is cooling (soonest-expiring returned, WARN emitted,
// selection-only); ReselectNone for single-member groups (no
// cooldown write — the E-3 fast path never enters the failover path)
// or genuinely no candidate.
//
// Am24 usage: the proxy fires this ONLY on terminal member-pipeline
// failure (winner==nil), max ONE re-entry per request; re-entry does
// not chain. Member-internal 429s are absorbed by the member's own
// credential LB and never reach this method.
//
// Nil receiver: ("", ReselectNone) no-op.
func (e *Engine) ExcludeAndReselect(
	groupID, conversationKey, excludedMemberID string,
	retryAfter time.Duration,
) (memberID string, mode ReselectMode) {
	if e == nil {
		return "", ReselectNone
	}
	now := time.Now()

	e.mu.RLock()
	st := e.groups[groupID]
	e.mu.RUnlock()

	if st == nil {
		return "", ReselectNone
	}
	n := st.memberCount()
	if n == 0 {
		return "", ReselectNone
	}

	// Single-member group: no alternative exists. No cooldown write
	// (engine-side backstop; the caller pre-checks too).
	if n == 1 {
		return "", ReselectNone
	}

	cooldown := retryAfter
	if cooldown <= 0 {
		cooldown = e.defaultCooldown
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	// (1) Mark the excluded member cooling. The B2 no-op below still
	// applies this mark (the member genuinely failed); re-exclusion
	// while cooling extends the row (idempotent shape).
	if st.cooldowns == nil {
		st.cooldowns = make(map[string]time.Time)
	}
	st.cooldowns[excludedMemberID] = now.Add(cooldown)

	// (2) B2 precondition — re-read the binding under the write lock.
	// D8: the bound member must STILL EXIST in the group's current
	// membership (the UNION of both pools) — a member that left the
	// group can never be handed back as the replacement (the
	// selection-time repair owns that case; here we simply refuse
	// to resurrect a removed member).
	if conversationKey != "" {
		if b, ok := st.bindings[conversationKey]; ok && b.memberID != excludedMemberID {
			// NO-OP unless the bound member is INDEPENDENTLY cooling
			// (Leader Item 2 semantics carried): availability beats
			// pure no-op.
			if !st.isCoolingLocked(b.memberID, now) && memberInRefs(st.members, b.memberID) {
				return b.memberID, ReselectHealthy
			}
			// Bound member independently cooling (or gone — D8) —
			// fall through to the healthy reselect.
		}
	}

	// (3) Weighted random among non-cooling (renormalized),
	// defensively skipping members absent from the configured list.
	// group-backup-members / D1: pool-scoped to the DEFAULT pool
	// (the picker operates on the same pool as the original pick).
	pick := st.pickHealthyLocked(now, st.selector, st.defaultMembers)
	if pick == "" || pick == excludedMemberID {
		// All-cooling (or defensively the only healthy pick is the
		// just-excluded one) → soonest-expiry single-attempt path.
		soonest, allCooling := st.soonestCooldownLocked(now, st.defaultMembers)
		if !allCooling || soonest == "" {
			return "", ReselectNone // genuinely no candidate in the default pool
		}
		log.Printf("[LB-FAILOVER] group=%s all members cooling; picking soonest-expiring member=%s", groupID, soonest)
		return soonest, ReselectSoonestExpiry
	}

	if conversationKey == "" {
		// W-2 analog: fresh pick, NO binding-map write.
		return pick, ReselectHealthy
	}

	// (4) Single rebind map write; the rebind REFRESHES boundAt (#10
	// — a rebind is a binding write; the sliding idle window restarts
	// for the NEW member).
	if _, exists := st.bindings[conversationKey]; !exists {
		st.bindingsCount++
	}
	st.bindings[conversationKey] = &binding{memberID: pick, boundAt: now}
	return pick, ReselectHealthy
}

// PickBackupMember returns the sticky-picked BACKUP-pool member for
// (groupID, conversationKey) — the fallback tier reached ONLY after
// every default member in the request's exclusion set has failed
// (group-backup-members / D1).
//
// Mechanical twin of GetOrSelect, restricted to the BACKUP pool:
//   - Returns ("", false, ErrNoMembers) when the group has no
//     configured backup members (clean, never a default-pool
//     passthrough; the proxy surfaces the normal group_unservable
//     envelope).
//   - Weighted random among non-cooling, renormalized, uses the
//     per-group RNG.
//   - Sticky binding map is `backupBindings` — SEPARATE from the
//     default pool's `bindings` map (no cross-pool lookup; a
//     conversation that fell back keeps its backup binding per-pool
//     but never sees its default binding on the backup path).
//   - Same empty-key fresh-pick / binding-write semantics.
//   - Cooldowns are SHARED (see D1 note in `groupState`).
//   - All-cooling ⇒ soonest-expiring pick, selection-only, no
//     binding (mirrors GetOrSelect).
//   - Nil receiver: ErrNoMembers.
//
// group-backup-members / D8: the bound member is resolved against
// the CURRENT membership — the UNION of both pools (default ∪
// backup), not the backup pool alone. A backup member whose
// backup_only flag was flipped OFF still exists, so its binding is
// grandfathered (continuity, symmetric with the default→backup
// case); a member that LEFT the group does not, so the binding is
// discarded and the pick is re-made among the remaining backups
// (the `binding=stale` token is emitted).
func (e *Engine) PickBackupMember(groupID, conversationKey string) (memberID string, newlyBound bool, err error) {
	out := e.pickBackupMember(groupID, conversationKey)
	return out.memberID, out.newlyBound, out.err
}

// PickBackupMemberWithRepair is the repair-aware variant of
// PickBackupMember: it additionally reports whether a STALE binding
// (one whose member left the group) was discarded during this call
// (group-backup-members / D8). The member / newlyBound / error
// triple is identical to PickBackupMember's.
func (e *Engine) PickBackupMemberWithRepair(groupID, conversationKey string) (memberID string, newlyBound bool, staleRepaired bool, err error) {
	out := e.pickBackupMember(groupID, conversationKey)
	return out.memberID, out.newlyBound, out.staleRepaired, out.err
}

// pickBackupMember is the shared implementation behind
// PickBackupMember and PickBackupMemberWithRepair. See
// PickBackupMember's doc comment for the full contract.
func (e *Engine) pickBackupMember(groupID, conversationKey string) pickOutcome {
	if e == nil {
		return pickOutcome{err: ErrNoMembers}
	}
	now := time.Now()

	e.mu.RLock()
	st := e.groups[groupID]
	e.mu.RUnlock()

	if st == nil {
		return pickOutcome{err: ErrNoMembers}
	}

	// C2: every read of the per-group selector / ref slices must hold
	// st.mu — applyRefsLocked replaces them wholesale under the write
	// lock, so even a bare selector or length check is a read (the
	// binding.go memberCount pattern). One locked snapshot here feeds
	// BOTH the entry guard and the fast-path eligibility decision, so
	// the fast path no longer needs a second read-lock acquisition.
	st.mu.RLock()
	hasBackups := st.backupSelector != nil && st.backupSelector.totalWeight > 0
	n := len(st.backupMembers)
	fastEligible := hasBackups && n == 1 && len(st.cooldowns) == 0
	fastID := ""
	if fastEligible {
		fastID = st.backupMembers[0].ModelID
	}
	st.mu.RUnlock()

	if !hasBackups || n == 0 {
		return pickOutcome{err: ErrNoMembers}
	}

	// staleRepaired is function-scoped: the fast path may detect the
	// stale binding and then fall through to the full path (the
	// membership changed under it) — the signal must survive the
	// hand-off (D8).
	staleRepaired := false

	// Fast path (single backup member): no cooldowns, no other
	// backup to choose from — return directly. Mirrors the default
	// pool's E-3 fast-path semantics.
	if fastEligible {
		if conversationKey == "" {
			return pickOutcome{memberID: fastID}
		}
		st.mu.Lock()
		// D8: an EXISTING binding for this conversation wins when it
		// is still valid — the member may have been RECLASSIFIED
		// into the default pool but it still EXISTS, so the binding
		// is grandfathered (continuity). Without this check the fast
		// path would silently rebind the conversation to the pool's
		// only member, breaking the grandfathering ruling.
		if b, ok := st.backupBindings[conversationKey]; ok {
			exists := memberInRefs(st.members, b.memberID)
			if exists && !b.expired(e.ttl, now) {
				b.boundAt = now
				st.mu.Unlock()
				return pickOutcome{memberID: b.memberID}
			}
			if !exists {
				// D8: the bound backup member is GONE — same
				// bounded + redacted token idiom as the full path.
				staleRepaired = true
				log.Printf("[grouplb] group=%s pool=backup conv=%s binding=stale member=%s (member no longer configured) — re-picking",
					groupID, conversationKey, b.memberID)
			}
			// Guarded by the row-presence check ⇒ at most one count
			// decrement per row (D8 composition rule: no double-drop
			// against the OnGroupChanged push layer).
			delete(st.backupBindings, conversationKey)
			st.backupBindingsCount--
		}
		// Re-check under write lock: still single, still no
		// cooldowns, AND the member ID read under RLock is still
		// the pool's only configured member (OnGroupChanged can
		// swap backupMembers between the two lock sections —
		// persisting the stale id would orphan the binding).
		if st.backupSelector != nil && len(st.backupMembers) == 1 &&
			len(st.cooldowns) == 0 && st.backupMembers[0].ModelID == fastID {
			// C4: the counter increment is guarded by map-existence
			// (the row was either just deleted above or never
			// existed) so a future write path cannot drift the O(1)
			// mirror.
			if _, exists := st.backupBindings[conversationKey]; !exists {
				st.backupBindingsCount++
			}
			st.backupBindings[conversationKey] = &binding{memberID: fastID, boundAt: now}
			st.mu.Unlock()
			log.Printf("[grouplb] group=%s pool=backup conv=%s→member=%s newlyBound=true", groupID, conversationKey, fastID)
			return pickOutcome{memberID: fastID, newlyBound: true, staleRepaired: staleRepaired}
		}
		st.mu.Unlock()
		// fall through to full path
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	// Re-check selector under write lock (race with OnGroupChanged).
	if st.backupSelector == nil || st.backupSelector.totalWeight <= 0 {
		return pickOutcome{staleRepaired: staleRepaired, err: ErrNoMembers}
	}

	if conversationKey != "" {
		if b, ok := st.backupBindings[conversationKey]; ok {
			// D8: the existence test is UNION-scoped (st.members
			// holds default ∪ backup) — a backup member that was
			// RE-CLASSIFIED to default still exists, so its
			// binding is grandfathered; a member that LEFT the
			// group does not, so the binding is discarded and
			// re-picked among the remaining backups.
			exists := memberInRefs(st.members, b.memberID)
			valid := !b.expired(e.ttl, now) &&
				exists &&
				!st.isCoolingLocked(b.memberID, now)
			if valid {
				b.boundAt = now
				return pickOutcome{memberID: b.memberID}
			}
			if !exists {
				// D8: the bound backup member is GONE — the
				// binding cannot be honoured. Same bounded +
				// redacted token idiom as the default pool
				// (D5's `pool=backup` prefix kept for grep
				// symmetry).
				staleRepaired = true
				log.Printf("[grouplb] group=%s pool=backup conv=%s binding=stale member=%s (member no longer configured) — re-picking",
					groupID, conversationKey, b.memberID)
			}
			// Guarded by the row-presence check ⇒ at most one
			// count decrement per row (D8 composition rule: no
			// double-drop against the OnGroupChanged push layer).
			delete(st.backupBindings, conversationKey)
			st.backupBindingsCount--
		}
	}

	pick := st.pickHealthyLocked(now, st.backupSelector, st.backupMembers)
	if pick == "" {
		soonest, allCooling := st.soonestCooldownLocked(now, st.backupMembers)
		if !allCooling || soonest == "" {
			return pickOutcome{staleRepaired: staleRepaired, err: ErrNoMembers}
		}
		log.Printf("[LB-FAILOVER] group=%s pool=backup all backups cooling; picking soonest-expiring member=%s", groupID, soonest)
		return pickOutcome{memberID: soonest, staleRepaired: staleRepaired}
	}

	if conversationKey == "" {
		return pickOutcome{memberID: pick, staleRepaired: staleRepaired}
	}

	// C4: guard the O(1) mirror increment by map-existence (the row
	// was dropped above if it existed), so no future write path can
	// drift the counter.
	if _, exists := st.backupBindings[conversationKey]; !exists {
		st.backupBindingsCount++
	}
	st.backupBindings[conversationKey] = &binding{memberID: pick, boundAt: now}
	log.Printf("[grouplb] group=%s pool=backup conv=%s→member=%s newlyBound=true", groupID, conversationKey, pick)
	return pickOutcome{memberID: pick, newlyBound: true, staleRepaired: staleRepaired}
}

// ExcludeAndBackupReselect is the BACKUP-pool twin of
// ExcludeAndReselect: marks excludedMemberID as cooling AND
// rebinds (groupID, conversationKey) to the next healthy BACKUP
// member. Same shared-cooldown semantics (a member in either pool's
// exclusion set is cooling in both).
//
// Returns the ReselectMode for caller observability:
//   - ReselectHealthy: a backup member is available for this call.
//   - ReselectSoonestExpiry: all backups are cooling; soonest picked.
//   - ReselectNone: backup pool exhausted — caller surfaces real
//     failure (no fallback beyond backups in v1).
//
// Single-backup group ⇒ ReselectNone (no alternative exists within
// the backup pool). Nil receiver ⇒ ("", ReselectNone).
func (e *Engine) ExcludeAndBackupReselect(
	groupID, conversationKey, excludedMemberID string,
	retryAfter time.Duration,
) (memberID string, mode ReselectMode) {
	if e == nil {
		return "", ReselectNone
	}
	now := time.Now()

	e.mu.RLock()
	st := e.groups[groupID]
	e.mu.RUnlock()

	if st == nil {
		return "", ReselectNone
	}

	// C2: selector / length reads must hold st.mu (applyRefsLocked
	// replaces both under the write lock — the binding.go memberCount
	// pattern). Snapshot once, then re-validate anything the decision
	// depends on under the write lock below.
	st.mu.RLock()
	hasBackups := st.backupSelector != nil && st.backupSelector.totalWeight > 0
	backupCount := len(st.backupMembers)
	st.mu.RUnlock()
	if !hasBackups || backupCount == 0 {
		return "", ReselectNone
	}

	// Single-backup group: no alternative exists within the backup
	// pool. Mark the cooldown so the member is gated on a follow-up
	// attempt, but do not try to pick (mirrors the default pool's
	// E-3 fast-path guard).
	//
	// C2: the single-backup count is RE-VALIDATED under the write
	// lock — OnGroupChanged can add or remove a backup between the
	// read snapshot above and this acquisition (the 34b3f51
	// fast-path pattern). On a change we fall through to the full
	// path, which re-reads everything under its own write lock.
	if backupCount == 1 {
		now0 := time.Now()
		st.mu.Lock()
		if st.backupSelector != nil && len(st.backupMembers) == 1 {
			if st.cooldowns == nil {
				st.cooldowns = make(map[string]time.Time)
			}
			st.cooldowns[excludedMemberID] = now0.Add(retryAfterIf(retryAfter, e.defaultCooldown))
			st.mu.Unlock()
			return "", ReselectNone
		}
		st.mu.Unlock()
		// Membership changed under us — fall through.
	}

	cooldown := retryAfter
	if cooldown <= 0 {
		cooldown = e.defaultCooldown
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	// (1) Mark the excluded member cooling (shared with the default
	// pool by D1 — the cooldown map is the cross-pool "bad right
	// now" gate).
	if st.cooldowns == nil {
		st.cooldowns = make(map[string]time.Time)
	}
	st.cooldowns[excludedMemberID] = now.Add(cooldown)

	// (2) B2 precondition — re-read the binding under the write lock.
	// D8: the bound member must STILL EXIST in the group's current
	// membership (the UNION of both pools) — a member that left the
	// group can never be handed back as the replacement.
	if conversationKey != "" {
		if b, ok := st.backupBindings[conversationKey]; ok && b.memberID != excludedMemberID {
			if !st.isCoolingLocked(b.memberID, now) && memberInRefs(st.members, b.memberID) {
				return b.memberID, ReselectHealthy
			}
		}
	}

	// (3) Weighted random among non-cooling (renormalized) in the
	// backup pool.
	pick := st.pickHealthyLocked(now, st.backupSelector, st.backupMembers)
	if pick == "" || pick == excludedMemberID {
		soonest, allCooling := st.soonestCooldownLocked(now, st.backupMembers)
		if !allCooling || soonest == "" {
			return "", ReselectNone
		}
		log.Printf("[LB-FAILOVER] group=%s pool=backup all backups cooling; picking soonest-expiring member=%s", groupID, soonest)
		return soonest, ReselectSoonestExpiry
	}

	if conversationKey == "" {
		return pick, ReselectHealthy
	}

	if _, exists := st.backupBindings[conversationKey]; !exists {
		st.backupBindingsCount++
	}
	st.backupBindings[conversationKey] = &binding{memberID: pick, boundAt: now}
	return pick, ReselectHealthy
}
