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
type groupState struct {
	mu sync.RWMutex

	// members is the current configured ref list (slice order ==
	// Position order). Replaced wholesale under mu.Lock by
	// OnGroupChanged.
	members []MemberRef

	// selector is the prebuilt prefix-sum selector over members.
	// nil when the ref list is empty/invalid (no valid members).
	selector *weightedSelector

	// bindings maps conversationKey → binding. Empty conversation
	// keys are NEVER stored here (W-2 carried).
	bindings map[string]*binding

	// cooldowns maps memberID → cooldownUntil. Separate from
	// bindings; entries are swept by the janitor when
	// cooldownUntil <= now.
	cooldowns map[string]time.Time

	// rng is the per-group RNG (E-4 carried): seeded from the engine
	// seed XORed with the group-ID hash, removing first-selection
	// contention across groups. Only used while holding mu (write
	// lock) or from paths that otherwise own exclusive access.
	rng *rand.Rand

	// bindingsCount mirrors len(bindings) (O(1) stats without a map
	// walk under lock).
	bindingsCount uint64
}

// newGroupState builds a groupState with its per-group seeded RNG.
func newGroupState(groupID string, rngSeed int64) *groupState {
	return &groupState{
		bindings:  make(map[string]*binding),
		cooldowns: make(map[string]time.Time),
		rng:       rand.New(rand.NewSource(groupRNGSeed(rngSeed, groupID))),
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
// Caller holds st.mu (WRITE lock — the per-group RNG is consumed).
func (st *groupState) pickHealthyLocked(now time.Time) string {
	if st.selector == nil || st.selector.totalWeight <= 0 {
		return ""
	}
	if len(st.cooldowns) == 0 {
		// Hot path: prebuilt prefix-sum selector, zero allocation.
		return st.selector.pick(st.rng.Float64() * st.selector.totalWeight)
	}
	// Renormalized walk over the healthy (non-cooling) set.
	total := 0.0
	for _, ref := range st.members {
		if st.isCoolingLocked(ref.ModelID, now) {
			continue
		}
		total += ref.Weight
	}
	if total <= 0 {
		return "" // all cooling
	}
	r := st.rng.Float64() * total
	cum := 0.0
	for _, ref := range st.members {
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
// cooldownUntil and whether EVERY configured member is currently
// cooling (the 0-of-N-healthy pin carried).
//
// Caller holds st.mu (read or write).
func (st *groupState) soonestCooldownLocked(now time.Time) (string, bool) {
	best := ""
	var bestUntil time.Time
	for _, ref := range st.members {
		until, ok := st.cooldowns[ref.ModelID]
		if !ok || !until.After(now) {
			return "", false // at least one healthy → not all-cooling
		}
		if best == "" || until.Before(bestUntil) {
			best, bestUntil = ref.ModelID, until
		}
	}
	return best, best != ""
}
