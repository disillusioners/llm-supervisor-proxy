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
type MemberRef struct {
	ModelID  string
	Weight   float64
	Position int
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
//   1. PER-GROUP SELECTOR (cumulative prefix weights + member
//      refs): NOT lazy at boot. Every group Add/Update on the
//      owning ModelsManager calls notifyGroupEngineLocked →
//      OnGroupChanged immediately (P2.2 design — load-bearing
//      for the Am28 survivor filter on member edits; verified and
//      approved at P2 review). HasGroup returns true for any
//      group that has been AddModel'd, even before any request
//      has been served.
//
//   2. PER-CONVERSATION BINDINGS (sticky affinity): LAZY. Created
//      on the first GetOrSelect call per (groupID, conversationKey)
//      (W-1 / W-2) and TTL-evicted by the janitor. Empty-key
//      requests return NewlyBound=false and store NO binding
//      (C2 — Q15 model_member_selected surfaces newlyBound-only).
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
func (e *Engine) GetOrSelect(groupID, conversationKey string) (memberID string, newlyBound bool, err error) {
	if e == nil {
		return "", false, ErrNoMembers
	}
	now := time.Now()

	e.mu.RLock()
	st := e.groups[groupID]
	e.mu.RUnlock()

	if st == nil {
		return "", false, ErrNoMembers
	}
	n := st.memberCount()
	if n == 0 {
		return "", false, ErrNoMembers
	}

	// E-3 fast path: single member — read members[0] under the
	// per-group RLock (members is replaced wholesale under the write
	// lock) and return directly. NO binding-map writes.
	if n == 1 {
		st.mu.RLock()
		id := st.members[0].ModelID
		st.mu.RUnlock()
		return id, false, nil
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
				return hitMember, false, nil
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

	if conversationKey != "" {
		if b, ok := st.bindings[conversationKey]; ok {
			valid := !b.expired(e.ttl, now) &&
				memberInRefs(st.members, b.memberID) &&
				!st.isCoolingLocked(b.memberID, now)
			if valid {
				// Another goroutine bound this key between our
				// phases; treat as a hit (W-1: not newly bound by
				// THIS call).
				b.boundAt = now
				return b.memberID, false, nil
			}
			// Stale / idle-expired / orphaned / cooling: drop lazily.
			delete(st.bindings, conversationKey)
			st.bindingsCount--
		}
	}

	pick := st.pickHealthyLocked(now)
	if pick == "" {
		// All-cooling (F.4): soonest-expiring pick, selection-only.
		soonest, allCooling := st.soonestCooldownLocked(now)
		if !allCooling || soonest == "" {
			// Refs went invalid under us (race with OnGroupChanged).
			return "", false, ErrNoMembers
		}
		log.Printf("[LB-FAILOVER] group=%s all members cooling; picking soonest-expiring member=%s", groupID, soonest)
		return soonest, false, nil
	}

	if conversationKey == "" {
		// W-2: no binding stored for the empty key.
		return pick, false, nil
	}

	st.bindings[conversationKey] = &binding{memberID: pick, boundAt: now}
	st.bindingsCount++
	log.Printf("[grouplb] group=%s conv=%s→member=%s newlyBound=true", groupID, conversationKey, pick)
	return pick, true, nil
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
}

// applyRefsLocked replaces the group's member list + selector.
// Caller holds e.mu (write) and st.mu (write).
func (e *Engine) applyRefsLocked(st *groupState, refs []MemberRef) {
	st.members = append([]MemberRef(nil), refs...) // defensive copy
	st.selector = newWeightedSelector(st.members)
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
	if conversationKey != "" {
		if b, ok := st.bindings[conversationKey]; ok && b.memberID != excludedMemberID {
			// NO-OP unless the bound member is INDEPENDENTLY cooling
			// (Leader Item 2 semantics carried): availability beats
			// pure no-op.
			if !st.isCoolingLocked(b.memberID, now) {
				return b.memberID, ReselectHealthy
			}
			// Bound member independently cooling — fall through to
			// the healthy reselect.
		}
	}

	// (3) Weighted random among non-cooling (renormalized),
	// defensively skipping members absent from the configured list.
	pick := st.pickHealthyLocked(now)
	if pick == "" || pick == excludedMemberID {
		// All-cooling (or defensively the only healthy pick is the
		// just-excluded one) → soonest-expiry single-attempt path.
		soonest, allCooling := st.soonestCooldownLocked(now)
		if !allCooling || soonest == "" {
			return "", ReselectNone // genuinely no candidate
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
