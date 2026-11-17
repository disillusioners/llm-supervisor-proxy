package credentiallb

import (
	"math/rand"
	"sync"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// binding is a single (conversationKey → credentialID) pin with its
// absolute-lifetime anchor.
//
// createdAt is stamped ONCE at bind/rebind. It is NEVER refreshed
// on a GetOrSelect hit — a continuously-hit conversation must not pin
// a credential forever. A binding is expired (and DELETED from the map
// on read) once now-createdAt > ttl; after that the layer's default
// selection runs exactly as if no binding ever existed.
//
// A cooldown (R3-4) NEVER touches createdAt — the cooldown map on
// modelState is separate state.
type binding struct {
	credentialID string
	createdAt    time.Time
}

// expired reports whether the binding's absolute lifetime has elapsed
// as of now (lazy-expiry predicate; the janitor applies the same one).
func (b *binding) expired(ttl time.Duration, now time.Time) bool {
	return now.Sub(b.createdAt) > ttl
}

// modelState is the per-model engine state. Guarded by its own
// sync.RWMutex under the Engine's outer lock (E-1 discipline: outer
// lock before any modelState lock; never two modelState locks at
// once).
type modelState struct {
	mu sync.RWMutex

	// credentials is the current configured ref list (slice order ==
	// Position order). Replaced wholesale under mu.Lock by
	// OnModelChanged / RebindFromStore.
	credentials []models.CredentialRef

	// selector is the prebuilt prefix-sum selector over credentials.
	// nil when the ref list is empty/invalid (no valid credentials).
	selector *weightedSelector

	// bindings maps conversationKey → binding. Empty conversation
	// keys are NEVER stored here (W-2).
	bindings map[string]*binding

	// cooldowns maps credentialID → cooldownUntil (R3-4). Separate
	// from bindings; entries are swept by the janitor when
	// cooldownUntil <= now, and cleared by OnCredentialDeleted (S6).
	cooldowns map[string]time.Time

	// rng is the per-model RNG (E-4): seeded from the engine seed
	// XORed with the model-ID hash, removing first-selection
	// contention across models. Only used while holding mu (write
	// lock) or from paths that otherwise own exclusive access.
	rng *rand.Rand

	// Stats mirrors (#5 pin): bindingsCount mirrors len(bindings) so
	// Stats().Bindings is O(1) (no map walk under lock).
	hitsTotal      uint64
	missesTotal    uint64
	bindingsCount  uint64
	failoversTotal uint64 // R3-8: monotonic — ticks once per non-no-op failover pick/rebind
}

// newModelState builds a modelState with its per-model seeded RNG.
func newModelState(modelID string, rngSeed int64) *modelState {
	return &modelState{
		bindings:  make(map[string]*binding),
		cooldowns: make(map[string]time.Time),
		rng:       rand.New(rand.NewSource(modelRNGSeed(rngSeed, modelID))),
	}
}

// credCount returns len(credentials) under the per-model read lock.
// The credentials slice is replaced wholesale under the write lock
// (OnModelChanged / RebindFromStore), so every read — including a
// bare length check — must hold st.mu (read or write); an unlocked
// read is a data race against applyRefsLocked.
func (st *modelState) credCount() int {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return len(st.credentials)
}

// credentialInRefs reports whether credentialID appears in refs
// (defensive still-in-config check, design note #2).
func credentialInRefs(refs []models.CredentialRef, credentialID string) bool {
	for _, r := range refs {
		if r.CredentialID == credentialID {
			return true
		}
	}
	return false
}

// isCoolingLocked reports whether credentialID is under an active
// cooldown as of now. Caller holds mu (read or write).
func (st *modelState) isCoolingLocked(credentialID string, now time.Time) bool {
	until, ok := st.cooldowns[credentialID]
	return ok && until.After(now)
}

// pickHealthyLocked performs weighted random among non-cooling
// (renormalized) selection (R3-4 skip rule; Round 3c S3 canonical
// wording). Returns "" when every configured credential is cooling
// (the caller then applies the all-cooling soonest-expiry path) or
// the selector is invalid.
//
// Caller holds st.mu (WRITE lock — the per-model RNG is consumed).
func (st *modelState) pickHealthyLocked(now time.Time) string {
	if st.selector == nil || st.selector.totalWeight <= 0 {
		return ""
	}
	if len(st.cooldowns) == 0 {
		// Hot path: prebuilt prefix-sum selector, zero allocation.
		return st.selector.pick(st.rng.Intn(st.selector.totalWeight))
	}
	// Renormalized walk over the healthy (non-cooling) set.
	total := 0
	for _, ref := range st.credentials {
		if st.isCoolingLocked(ref.CredentialID, now) {
			continue
		}
		total += ref.Weight
	}
	if total == 0 {
		return "" // all cooling
	}
	r := st.rng.Intn(total)
	cum := 0
	for _, ref := range st.credentials {
		if st.isCoolingLocked(ref.CredentialID, now) {
			continue
		}
		cum += ref.Weight
		if r < cum {
			return ref.CredentialID
		}
	}
	// Unreachable given r < total; defensive last healthy ref.
	return ""
}

// soonestCooldownLocked returns the credential with the SOONEST
// cooldownUntil and whether EVERY configured credential is currently
// cooling (the 0-of-N-healthy pin; a single healthy credential means
// the all-cooling branch does NOT apply).
//
// Caller holds st.mu (read or write).
func (st *modelState) soonestCooldownLocked(now time.Time) (string, bool) {
	best := ""
	var bestUntil time.Time
	for _, ref := range st.credentials {
		until, ok := st.cooldowns[ref.CredentialID]
		if !ok || !until.After(now) {
			return "", false // at least one healthy → not all-cooling
		}
		if best == "" || until.Before(bestUntil) {
			best, bestUntil = ref.CredentialID, until
		}
	}
	return best, best != ""
}

// SetBindingTTL installs the engine's absolute-lifetime TTL for
// conversation bindings (createdAt + ttl). Nil-safe. The TTL lives
// in an atomic.Int64, so the setter needs no mutex — the atomic IS
// the guard, and the hot path reads it lock-free. Semantics:
//
//   - d <= 0: IGNORED — the current TTL is kept (a mis-wired or
//     hand-edited config degrades to the prior value, never to 0).
//   - d > BindingTTLCeiling (24h): clamped down to 24h.
//   - 0 < d < BindingTTLMinGranularity (1ms): meaningless noise —
//     RAISED to BindingTTLFloor (1m).
//   - BindingTTLMinGranularity <= d < BindingTTLFloor: honoured
//     VERBATIM — an operator may deliberately ask for a sub-minute
//     affinity window (short-lived sessions, tests).
//
// Safe for concurrent use. Boot-captured in production (cmd/main.go).
func (e *Engine) SetBindingTTL(d time.Duration) {
	if e == nil {
		return
	}
	if d <= 0 {
		return
	}
	if d > BindingTTLCeiling {
		d = BindingTTLCeiling
	}
	if d < BindingTTLMinGranularity {
		d = BindingTTLFloor
	}
	e.ttl.Store(int64(d))
}

// BindingTTL returns the engine's effective binding TTL. Nil-safe;
// the package default when unset (zero-value engine).
func (e *Engine) BindingTTL() time.Duration {
	if e == nil {
		return DefaultBindingTTL
	}
	v := e.ttl.Load()
	if v <= 0 {
		return DefaultBindingTTL
	}
	return time.Duration(v)
}
