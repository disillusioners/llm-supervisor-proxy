package credentiallb

import (
	"hash/fnv"
	"log"
	"sync"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// Production defaults (decisions.md §Default Configuration). The
// store constructs the engine with all three; tests pass short values
// through the same constructor (W8: constructor-arg cooldown TTL, not
// a setter).
const (
	DefaultBindingTTL     = 24 * time.Hour
	DefaultSweepInterval  = 5 * time.Minute
	DefaultCooldown       = 60 * time.Second
	DefaultCredentialSeed = int64(0) // 0 = time-based seed
)

// Engine maintains (modelID, conversationKey) → credentialID bindings
// with sliding-idle-TTL eviction and weighted-random selection for
// new bindings, plus the Round-3 per-(model, credential) cooldown map
// driving rate-limit failover.
//
// Concurrency: safe for concurrent use. All exported methods are
// thread-safe and nil-safe on a nil receiver.
//
// Lifecycle:
//
//	e := NewEngine(ttl, sweepInterval, rngSeed, defaultCooldown)
//	defer e.Stop()
type Engine struct {
	mu     sync.RWMutex // outer lock: guards the models map
	models map[string]*modelState

	ttl             time.Duration // sliding IDLE TTL per binding (#10)
	sweepInt        time.Duration // janitor cadence
	defaultCooldown time.Duration // fallback cooldown when retryAfter <= 0 (Round 3)
	rngSeed         int64

	stopCh    chan struct{} // janitor shutdown
	sweepDone chan struct{} // janitor exit ack
	stopOnce  sync.Once

	// sweepHook is a nil-by-default janitor extension point exercised
	// ONLY from the testhooks.go seam (W8): production engine code
	// never references any named test hook — see testhooks.go.
	sweepHookMu sync.Mutex
	sweepHook   func()
}

// NewEngine constructs an Engine. ttl is the sliding idle TTL on each
// binding (suggested 24h): boundAt is refreshed on every in-TTL
// GetOrSelect hit and the binding is eligible for expiry only after
// now-boundAt > ttl of consecutive idle. sweepInterval is the janitor
// cadence (suggested 5m); rngSeed seeds the per-model weighted-random
// RNGs (0 = time-based seed); defaultCooldown seeds cooldowns when
// ExcludeAndReselect's retryAfter is <= 0 (production passes
// DefaultCooldown = 60s).
//
// Non-positive arguments fall back to the production defaults so a
// mis-wired constructor degrades to sane behavior.
//
// Starts a background janitor goroutine. Call Stop() to terminate it;
// after Stop, GetOrSelect still works (lazy expiry only).
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
		models:          make(map[string]*modelState),
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

// modelRNGSeed derives the per-model RNG seed: engine seed XORed with
// the FNV-1a hash of the model ID (E-4). A zero (time-based) engine
// seed is mixed with the wall clock so concurrent engines in tests do
// not share sequences.
func modelRNGSeed(rngSeed int64, modelID string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(modelID))
	mix := int64(h.Sum64())
	if rngSeed == 0 {
		return mix ^ time.Now().UnixNano()
	}
	return rngSeed ^ mix
}

// runJanitor is the single background sweep goroutine. It sweeps BOTH
// expired cooldowns and idle-expired bindings in ONE pass (R3-4: no
// second goroutine, no second ticker), under the E-1 discipline:
// outer RLock for the whole walk + per-model write locks one at a
// time, so reads proceed concurrently with the sweep.
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

// sweepOnce performs one janitor pass. The whole body is wrapped in
// defer/recover + WARN (E-4, Risk #8 hardening): a panic no longer
// silently stops the janitor — the next tick runs another sweep.
//
// Sweep order within a model: cooldowns first (cheaper map), then
// bindings (sliding-TTL check) — per the Round-3b janitor note.
func (e *Engine) sweepOnce() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[WARN] [credentiallb] janitor sweep recovered from panic: %v", r)
		}
	}()
	if hook := e.getSweepHook(); hook != nil {
		hook()
	}
	now := time.Now()
	e.mu.RLock() // outer RLock for the WHOLE walk (E-1) — reads proceed concurrently
	for _, st := range e.models {
		st.mu.Lock() // per-model write lock, one at a time
		for credID, until := range st.cooldowns {
			if !until.After(now) { // expired iff cooldownUntil <= now
				delete(st.cooldowns, credID)
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
			log.Printf("[DEBUG] [credentiallb] janitor evicted %d idle bindings for model state", evicted)
		}
	}
	e.mu.RUnlock()
}

func (e *Engine) getSweepHook() func() {
	e.sweepHookMu.Lock()
	defer e.sweepHookMu.Unlock()
	return e.sweepHook
}

// GetOrSelect returns the credential pinned to (modelID,
// conversationKey) if a fresh binding exists; otherwise picks a new
// credential via weighted random (among non-cooling, renormalized),
// persists the binding, and returns it.
//
// W-1: returns (credentialID, newlyBound, err) where
// newlyBound=true ⇔ a fresh binding was created by THIS call;
// newlyBound=false ⇔ an existing in-TTL binding was reused, OR the
// single-credential fast path returned directly, OR an empty-key call
// returned a fresh pick (C2: newlyBound is bound to the binding-store
// side effect, NOT the pick side effect).
//
// Invariants:
//   - Returns ("", false, ErrNoCredentials) when modelID has no
//     credentials configured (no modelState, empty list, or an
//     invalid ref list that failed the defensive selector build).
//   - Single-credential fast path (E-3): returns credentials[0]
//     directly with NO map writes and NO stats ticks — byte-identical
//     to pre-LB behavior, zero engine overhead. The fast path ignores
//     cooldowns (single-credential models never enter the failover
//     path; ExcludeAndReselect returns ReselectNone for them).
//   - Binding lookup applies the #10 sliding-idle check
//     (now-boundAt > ttl ⇒ drop + re-pick), the defensive
//     still-in-config check (design note #2), and the skip-cooling
//     check (a bound credential that went cooling is dropped and
//     re-picked — selection NEVER returns a cooling credential except
//     the explicit all-cooling soonest-expiry path below).
//   - Empty conversationKey (W-2): NO binding stored, fresh weighted
//     pick per call, newlyBound=false, Misses ticks. The ""-as-own-
//     bucket reading is REMOVED (silent 24h hotspot).
//   - All-cooling (F.4 availability ruling): when every configured
//     credential is cooling, returns the SOONEST-expiring credential
//     with newlyBound=false and NO binding stored, emitting the
//     [LB-FAILOVER] WARN. Availability beats strict cooldown.
//   - Nil receiver: returns ErrNoCredentials (defensive; the store
//     layer guards the nil-engine path before calling).
func (e *Engine) GetOrSelect(modelID, conversationKey string) (credentialID string, newlyBound bool, err error) {
	if e == nil {
		return "", false, ErrNoCredentials
	}
	now := time.Now()

	e.mu.RLock()
	st := e.models[modelID]
	e.mu.RUnlock()

	if st == nil {
		return "", false, ErrNoCredentials
	}
	n := st.credCount()
	if n == 0 {
		return "", false, ErrNoCredentials
	}

	// E-3 fast path: single credential — read credentials[0] under the
	// per-model RLock (credentials is replaced wholesale under the
	// write lock) and return directly. NO binding-map writes, NO
	// stats ticks.
	if n == 1 {
		st.mu.RLock()
		id := st.credentials[0].CredentialID
		st.mu.RUnlock()
		return id, false, nil
	}

	// Happy path (99%): optimistic read under modelState RLock.
	if conversationKey != "" {
		st.mu.RLock()
		hitCred, hit := "", false
		if b, ok := st.bindings[conversationKey]; ok &&
			!b.expired(e.ttl, now) &&
			credentialInRefs(st.credentials, b.credentialID) &&
			!st.isCoolingLocked(b.credentialID, now) {
			hitCred, hit = b.credentialID, true
		}
		st.mu.RUnlock()
		if hit {
			// Upgrade to the write lock for the #10 boundAt refresh
			// (a value write cannot race under RLock), re-verifying
			// the binding survived the lock transition.
			st.mu.Lock()
			if b, ok := st.bindings[conversationKey]; ok &&
				b.credentialID == hitCred &&
				!b.expired(e.ttl, now) {
				b.boundAt = now
				st.hitsTotal++
				st.mu.Unlock()
				return hitCred, false, nil
			}
			st.mu.Unlock()
			// Raced out between the locks (binding rebound/evicted by
			// a concurrent writer) — fall through to the full path.
		}
	}

	// Full path under the per-model write lock: miss, lazy expiry,
	// stale/cooling eviction, first selection, empty-key pick.
	st.mu.Lock()
	defer st.mu.Unlock()

	if conversationKey != "" {
		if b, ok := st.bindings[conversationKey]; ok {
			valid := !b.expired(e.ttl, now) &&
				credentialInRefs(st.credentials, b.credentialID) &&
				!st.isCoolingLocked(b.credentialID, now)
			if valid {
				// Another goroutine bound this key between our phases;
				// treat as a hit (W-1: not newly bound by THIS call).
				b.boundAt = now
				st.hitsTotal++
				return b.credentialID, false, nil
			}
			// Stale / idle-expired / orphaned / cooling: drop lazily.
			delete(st.bindings, conversationKey)
			st.bindingsCount--
		}
	}

	st.missesTotal++ // a fresh pick happens from here on (C2: ticks for empty-key picks too)

	pick := st.pickHealthyLocked(now)
	if pick == "" {
		// All-cooling (F.4): soonest-expiring pick, selection-only.
		soonest, allCooling := st.soonestCooldownLocked(now)
		if !allCooling || soonest == "" {
			// Refs went invalid under us (race with OnModelChanged).
			return "", false, ErrNoCredentials
		}
		log.Printf("[LB-FAILOVER] model=%s all credentials cooling; picking soonest-expiring cred=%s", modelID, soonest)
		return soonest, false, nil
	}

	if conversationKey == "" {
		// W-2: no binding stored for the empty key.
		return pick, false, nil
	}

	st.bindings[conversationKey] = &binding{credentialID: pick, boundAt: now}
	st.bindingsCount++
	log.Printf("[credentiallb] model=%s conv=%s→cred=%s newlyBound=true", modelID, conversationKey, pick)
	return pick, true, nil
}

// OnModelChanged rebuilds the per-model selector (cumulative prefix
// sums over the new weights) and FILTERS existing bindings (E-2):
// preserves any binding whose credentialID still appears in refs —
// keeping its boundAt untouched (the sliding idle window survives a
// weight nudge); drops only orphan bindings (whose credential was
// removed). This is NOT clear-all: routine weight changes preserve
// cache affinity for every surviving conversation.
//
// Cooldowns are deliberately NOT touched here (contract locking
// table): orphan cooldown rows linger inertly until the janitor
// sweeps their expiry or OnCredentialDeleted clears them (S6).
//
// refs == nil (or empty) means the model was removed: the model's
// state is deleted outright (RemoveModel wires this).
//
// Nil receiver: no-op.
func (e *Engine) OnModelChanged(modelID string, refs []models.CredentialRef) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(refs) == 0 {
		delete(e.models, modelID)
		return
	}
	st := e.models[modelID]
	if st == nil {
		st = newModelState(modelID, e.rngSeed)
		e.models[modelID] = st
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	e.applyRefsLocked(st, refs)
	surviving := make(map[string]bool, len(refs))
	for _, r := range refs {
		surviving[r.CredentialID] = true
	}
	for key, b := range st.bindings {
		if !surviving[b.credentialID] {
			delete(st.bindings, key)
			st.bindingsCount--
			log.Printf("[credentiallb] model=%s dropped orphan binding conv=%s→cred=%s (credential no longer configured)", modelID, key, b.credentialID)
		}
	}
}

// RebindFromStore is the bulk-load counterpart of OnModelChanged for
// startup: given a model's configured credentials, set up the
// selector WITHOUT dropping any bindings (none exist at startup —
// "guaranteed not to drop"). Safe to call multiple times for the same
// model; the last call wins. Nil receiver: no-op.
func (e *Engine) RebindFromStore(modelID string, refs []models.CredentialRef) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(refs) == 0 {
		delete(e.models, modelID)
		return
	}
	st := e.models[modelID]
	if st == nil {
		st = newModelState(modelID, e.rngSeed)
		e.models[modelID] = st
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	e.applyRefsLocked(st, refs)
}

// applyRefsLocked replaces the model's credential list + selector.
// Caller holds e.mu (write) and st.mu (write).
func (e *Engine) applyRefsLocked(st *modelState, refs []models.CredentialRef) {
	st.credentials = append([]models.CredentialRef(nil), refs...) // defensive copy
	st.selector = newWeightedSelector(st.credentials)
}

// OnCredentialDeleted drops any binding whose credentialID == id
// across all models AND clears all cooldown entries for that
// credential in the SAME per-model write-lock pass (S6: hygiene
// against unbounded map growth and stale EngineStats.Cooldowns gauge
// counts; a deleted credential can never be selected again). WARN
// when bindings were dropped; idempotent; nil receiver: no-op.
func (e *Engine) OnCredentialDeleted(credentialID string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for modelID, st := range e.models {
		st.mu.Lock()
		dropped := 0
		for key, b := range st.bindings {
			if b.credentialID == credentialID {
				delete(st.bindings, key)
				st.bindingsCount--
				dropped++
			}
		}
		delete(st.cooldowns, credentialID)
		st.mu.Unlock()
		if dropped > 0 {
			log.Printf("[WARN] [credentiallb] model=%s dropped %d binding(s) for deleted credential %s", modelID, dropped, credentialID)
		}
	}
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

// EngineStats is the per-model stats shape returned by Engine.Stats().
// Field names pinned (#5 + R3-8): Hits, Misses, Bindings, Failovers,
// Cooldowns — no aliases.
type EngineStats struct {
	Hits      uint64 // in-TTL lookup hits (sliding-TTL refreshes)
	Misses    uint64 // lookups that selected a fresh credential (incl. empty-key picks; excludes the E-3 fast path)
	Bindings  uint64 // current binding-map size for this model (O(1) via the bindingsCount mirror)
	Failovers uint64 // R3-8 monotonic counter — ticks once per NON-no-op failover (actual rebind or empty-key healthy pick; B2 no-ops and SoonestExpiry picks do NOT tick)
	Cooldowns uint64 // R3-8 GAUGE (Round 3b amendment 7) — current cooldown-map size for this model, read live on Stats(); NOT a counter
}

// Stats returns a snapshot of per-model engine stats for operator
// visibility. Reads each model's snapshot under its own RLock; the
// returned map is a value copy. Nil receiver: empty map.
func (e *Engine) Stats() map[string]EngineStats {
	if e == nil {
		return map[string]EngineStats{}
	}
	e.mu.RLock()
	type entry struct {
		id string
		st *modelState
	}
	entries := make([]entry, 0, len(e.models))
	for id, st := range e.models {
		entries = append(entries, entry{id, st})
	}
	e.mu.RUnlock()

	out := make(map[string]EngineStats, len(entries))
	for _, en := range entries {
		en.st.mu.RLock()
		out[en.id] = EngineStats{
			Hits:      en.st.hitsTotal,
			Misses:    en.st.missesTotal,
			Bindings:  en.st.bindingsCount,
			Failovers: en.st.failoversTotal,
			Cooldowns: uint64(len(en.st.cooldowns)), // gauge, read live
		}
		en.st.mu.RUnlock()
	}
	return out
}

// ReselectMode describes the outcome of ExcludeAndReselect. The
// engine picks the mode; the caller enforces the F.4 fall-through
// contract based on it (Round 3b B3 — one call, mode in return; the
// two-call ok+PickSoonestExpiring alternative was REJECTED because
// the engine has no tried-set awareness).
//
// C2 unified semantics (Round 3c):
//
//   - ReselectHealthy: a credential is available for THIS call (the
//     fresh weighted-random-among-non-cooling pick, OR the B2 no-op
//     sub-case returning the current unchanged binding, OR the
//     empty-key fresh pick). Caller MUST proceed with credential
//     failover — NOT fall through to model-fallback.
//   - ReselectSoonestExpiry: ALL credentials are cooling (0-of-N
//     healthy); the engine picked the soonest-expiring credential and
//     emitted the WARN. Caller MUST treat this as a SINGLE attempt
//     only — on re-429, fall through to model-fallback / terminal
//     error (no second ExcludeAndReselect call, no loop).
//   - ReselectNone: NO credential is available — single-credential
//     model (no alternative exists) or genuinely 0 valid candidates.
//     Caller falls through to model-fallback. Reserved for "no
//     credential exists / no credential valid": the B2 no-op and
//     empty-key paths return ReselectHealthy, NEVER ReselectNone.
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

// ExcludeAndReselect marks excludedCredID as cooling for modelID
// (cooldown seeded from retryAfter when > 0, else the engine's
// defaultCooldown — 60s in production), then REBINDS the
// (modelID, conversationKey) binding to the next healthy credential
// chosen by weighted random among non-cooling (renormalized),
// skipping cooling credentials and any credential defensively absent
// from the configured list.
//
// B2 PRECONDITION (concurrent double-429 idempotency): the rebind
// happens ONLY if the current binding's credentialID ==
// excludedCredID. If the current binding already points at a
// different credential (a concurrent same-conversation request
// already rebinded away), this call is a NO-OP returning
// (currentCredentialID, ReselectHealthy) — the unchanged binding IS
// the rebind the concurrent request committed. The excluded
// credential is still marked cooling (it genuinely 429'd); Failovers
// does NOT tick.
//
// Mode semantics (B3 / C2 unified mapping):
//   - ReselectHealthy — fresh healthy pick (typical), B2 no-op
//     (current binding returned unchanged), or empty-key fresh pick.
//     Failovers ticks on the non-no-op sub-cases.
//   - ReselectSoonestExpiry — every credential cooling: the engine
//     returns the credential with the SOONEST cooldownUntil and emits
//     "[LB-FAILOVER] model=%s all credentials cooling; picking
//     soonest-expiring cred=%s" (WARN). Selection-only: NO binding
//     write, NO loop, NO sleep (the F.4 single-attempt contract is
//     caller-enforced via the mode).
//   - ReselectNone — single-credential model (returns ("",
//     ReselectNone): no cooldown write, the E-3 fast path never
//     enters the failover path) or genuinely no candidate (zero
//     credentials valid). Caller falls through to model-fallback.
//
// Empty conversationKey (W-2 analog): no binding is stored — the call
// returns a fresh non-cooling weighted pick with
// mode=ReselectHealthy (ReselectSoonestExpiry when all cooling) and
// performs NO map write on the binding map.
//
// Locking: outer Engine.mu RLock for the state lookup, then the
// per-model write Lock (same E-1 discipline as OnModelChanged /
// OnCredentialDeleted — deadlock-free by the never-two-modelState-
// locks invariant). Single map write to bindings[conversationKey]
// (REBIND, not delete-then-create); the cooldown mark rides the same
// per-model write-lock section.
//
// Per-conversation retry accounting (which credentials were tried
// THIS request) lives in the request context — NOT in the engine
// (R3-5; the engine stays per-conversation-key stateless about
// retries and never becomes a stateful retry counter).
//
// Nil receiver: ("", ReselectNone) no-op.
func (e *Engine) ExcludeAndReselect(
	modelID, conversationKey, excludedCredID string,
	retryAfter time.Duration,
) (credentialID string, mode ReselectMode) {
	if e == nil {
		return "", ReselectNone
	}
	now := time.Now()

	e.mu.RLock()
	st := e.models[modelID]
	e.mu.RUnlock()

	if st == nil {
		return "", ReselectNone
	}
	n := st.credCount()
	if n == 0 {
		return "", ReselectNone
	}

	// Single-credential model: no alternative exists. No cooldown
	// write (R3-7: the len(Credentials) > 1 pre-check lives in the
	// Phase 3 handler; this is the engine-side backstop).
	if n == 1 {
		return "", ReselectNone
	}

	cooldown := retryAfter
	if cooldown <= 0 {
		cooldown = e.defaultCooldown
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	// (1) Mark the excluded credential cooling. The B2 no-op below
	// still applies this mark (the credential genuinely 429'd);
	// re-exclusion while cooling extends the row (idempotent shape,
	// GAUGE count unchanged — it was already counted).
	if st.cooldowns == nil {
		st.cooldowns = make(map[string]time.Time)
	}
	st.cooldowns[excludedCredID] = now.Add(cooldown)

	// (2) B2 precondition — re-read the binding under the write lock.
	if conversationKey != "" {
		if b, ok := st.bindings[conversationKey]; ok && b.credentialID != excludedCredID {
			// NO-OP: a concurrent same-conversation request already
			// rebinded away from excludedCredID. Return the unchanged
			// binding; Failovers does NOT tick.
			return b.credentialID, ReselectHealthy
		}
	}

	// (3) Weighted random among non-cooling (renormalized), also
	// defensively skipping credentials absent from the configured
	// list (pickHealthyLocked only picks from st.credentials).
	pick := st.pickHealthyLocked(now)
	if pick == "" || pick == excludedCredID {
		// All-cooling (or the only healthy pick is the excluded one,
		// which cannot happen when it was just marked cooling —
		// defensive) → soonest-expiry single-attempt path.
		soonest, allCooling := st.soonestCooldownLocked(now)
		if !allCooling || soonest == "" {
			return "", ReselectNone // genuinely no candidate
		}
		log.Printf("[LB-FAILOVER] model=%s all credentials cooling; picking soonest-expiring cred=%s", modelID, soonest)
		return soonest, ReselectSoonestExpiry
	}

	// Non-no-op failover: tick the monotonic counter (B2 no-ops and
	// SoonestExpiry picks do not reach here).
	st.failoversTotal++

	if conversationKey == "" {
		// W-2 analog: fresh pick, NO binding-map write.
		return pick, ReselectHealthy
	}

	// (4)+(5) Single rebind map write; the rebind REFRESHES boundAt
	// (#10 — a rebind is a binding write; the sliding idle window
	// restarts for the NEW credential).
	if _, exists := st.bindings[conversationKey]; !exists {
		st.bindingsCount++
	}
	st.bindings[conversationKey] = &binding{credentialID: pick, boundAt: now}
	return pick, ReselectHealthy
}
