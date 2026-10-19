package grouplb

import (
	"math/rand"
	"sync"
	"time"
)

// binding is a single (conversationKey → memberID) pin with its
// sliding-idle-TTL anchor (clone of credentiallb/binding.go, keyed by
// member MODEL ID instead of credential ID).
//
// #10 (leader-ruled, carried): boundAt is REFRESHED on every in-TTL
// GetOrSelect hit and by every ExcludeAndReselect rebind; the binding
// is eligible for expiry only after now-boundAt > ttl of CONSECUTIVE
// IDLE. The TTL is an /idle/ ceiling, not a /lifetime/ ceiling.
//
// A cooldown NEVER touches boundAt — the cooldown map on groupState
// is separate state.
type binding struct {
	memberID string
	boundAt  time.Time
}

// expired reports whether the binding has been idle longer than ttl
// as of now (lazy-expiry predicate; the janitor applies the same one).
func (b *binding) expired(ttl time.Duration, now time.Time) bool {
	return now.Sub(b.boundAt) > ttl
}

// groupState is the per-group engine state. Guarded by its own
// sync.RWMutex under the Engine's outer lock (E-1 discipline: outer
// lock before any groupState lock; never two groupState locks at
// once).
//
// group-backup-members / D1: members split into two selectors and
// two binding maps. Defaults populate `members` + `selector` +
// `bindings`; backups populate a parallel slice/selector/map. They
// never cross on the read or write paths. Cooldowns are SHARED
// across both pools (a member that failed in the default pool stays
// cooling in the backup pool too — a "this member is bad right now"
// gate; matches the existing cross-call exclusion semantics).
type groupState struct {
	mu sync.RWMutex

	// members holds the group's CURRENT configured refs (slice order
	// == Position order); replaced wholesale under mu.Lock by
	// OnGroupChanged. It spans BOTH pools (default ∪ backup) and is
	// the D8 existence source for the selection-time repair.
	members []MemberRef

	// selector is the prebuilt prefix-sum selector over the DEFAULT
	// members (those with BackupOnly=false). nil when the default
	// ref list is empty/invalid (no valid default member).
	selector *weightedSelector

	// defaultMembers is the DEFAULT-pool refs (the slice `selector`
	// was built over — the cooldown walk iterates it directly per
	// R3-4). nil when no default member is configured.
	defaultMembers []MemberRef

	// backupMembers / backupSelector mirror the default pair for
	// the BACKUP sub-pool (BackupOnly=true). Both are REBUILT on
	// every applyRefsLocked (NOT lazily allocated); the selector is
	// nil when the group has no backup member (a backup-less
	// group's PickBackupMember returns ErrNoMembers — clean, never
	// a silent default-pool passthrough).
	backupMembers  []MemberRef
	backupSelector *weightedSelector

	// bindings maps conversationKey → binding for the DEFAULT
	// pool. Empty conversation keys are NEVER stored here (W-2
	// carried).
	bindings map[string]*binding

	// backupBindings maps conversationKey → binding for the BACKUP
	// pool. Separate from `bindings`; never cross-referenced (a
	// conversation that never fell back never sees this map; one
	// that did keeps its backup binding per-pool, refreshed on
	// every in-TTL hit, same sliding-idle-TTL semantics).
	backupBindings map[string]*binding

	// cooldowns maps memberID → cooldownUntil. Separate from
	// bindings; entries are swept by the janitor when
	// cooldownUntil <= now. SHARED across pools (see D5 note
	// above).
	cooldowns map[string]time.Time

	// rng is the per-group RNG (E-4 carried): seeded from the engine
	// seed XORed with the group-ID hash, removing first-selection
	// contention across groups. Only used while holding mu (write
	// lock) or from paths that otherwise own exclusive access. SHARED
	// across both pools — one RNG per group, sequentially consumed.
	rng *rand.Rand

	// bindingsCount / backupBindingsCount mirror len(bindings) /
	// len(backupBindings) (O(1) stats without a map walk under lock).
	bindingsCount       uint64
	backupBindingsCount uint64
}

// newGroupState builds a groupState with its per-group seeded RNG.
func newGroupState(groupID string, rngSeed int64) *groupState {
	return &groupState{
		bindings:       make(map[string]*binding),
		backupBindings: make(map[string]*binding),
		cooldowns:      make(map[string]time.Time),
		rng:            rand.New(rand.NewSource(groupRNGSeed(rngSeed, groupID))),
	}
}

// memberCount returns len(members) under the per-group read lock.
// The members slice is replaced wholesale under the write lock
// (OnGroupChanged), so every read — including a bare length check —
// must hold st.mu (read or write); an unlocked read is a data race.
func (st *groupState) memberCount() int {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return len(st.members)
}

// memberInRefs reports whether memberID appears in refs (defensive
// still-in-config check, design note #2 carried).
func memberInRefs(refs []MemberRef, memberID string) bool {
	for _, r := range refs {
		if r.ModelID == memberID {
			return true
		}
	}
	return false
}

// isCoolingLocked reports whether memberID is under an active
// cooldown as of now. Caller holds mu (read or write).
func (st *groupState) isCoolingLocked(memberID string, now time.Time) bool {
	until, ok := st.cooldowns[memberID]
	return ok && until.After(now)
}

// pickHealthyLocked performs weighted random among non-cooling
// (renormalized) selection (R3-4 skip rule carried). Returns "" when
// every configured member is cooling (the caller then applies the
// all-cooling soonest-expiry path) or the selector is invalid.
//
// group-backup-members / D1: takes the pool's selector +
// pre-screened refs list. The hot path uses the per-pool
// prebuilt prefix-sum selector (zero allocation, no cooldown
// walk); the renormalized walk iterates ONLY the pool's refs (not
// the cross-pool `members` slice) so an all-cooling-defaults run
// does not silently pick a backup member.
//
// Caller holds st.mu (WRITE lock — the per-group RNG is consumed).
func (st *groupState) pickHealthyLocked(now time.Time, sel *weightedSelector, refs []MemberRef) string {
	if sel == nil || sel.totalWeight <= 0 {
		return ""
	}
	if len(st.cooldowns) == 0 {
		// Hot path: prebuilt prefix-sum selector, zero allocation.
		return sel.pick(st.rng.Float64() * sel.totalWeight)
	}
	// Renormalized walk over the healthy (non-cooling) set — pool-scoped.
	total := 0.0
	for _, ref := range refs {
		if st.isCoolingLocked(ref.ModelID, now) {
			continue
		}
		total += ref.Weight
	}
	if total <= 0 {
		return "" // all cooling in this pool
	}
	r := st.rng.Float64() * total
	cum := 0.0
	for _, ref := range refs {
		if st.isCoolingLocked(ref.ModelID, now) {
			continue
		}
		cum += ref.Weight
		if r < cum {
			return ref.ModelID
		}
	}
	// Unreachable given r < total; defensive.
	return ""
}

// soonestCooldownLocked returns the member with the SOONEST
// cooldownUntil and whether EVERY member of the supplied pool is
// currently cooling (the 0-of-N-healthy pin carried —
// pool-scoped per D1; the all-cooling windows are independent
// across the default and backup pools).
//
// Caller holds st.mu (read or write).
func (st *groupState) soonestCooldownLocked(now time.Time, refs []MemberRef) (string, bool) {
	best := ""
	var bestUntil time.Time
	for _, ref := range refs {
		until, ok := st.cooldowns[ref.ModelID]
		if !ok || !until.After(now) {
			return "", false // at least one healthy in this pool
		}
		if best == "" || until.Before(bestUntil) {
			best, bestUntil = ref.ModelID, until
		}
	}
	return best, best != ""
}
