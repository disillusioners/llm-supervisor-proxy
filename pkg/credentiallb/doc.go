// Package credentiallb implements the model-credential load-balancing
// selection and affinity engine (Phase 2 of the model-credential load
// balancing plan).
//
// # Purpose
//
// The engine pins each conversation (identified by a stable
// (modelID, conversationKey) pair) to one of a model's configured
// credentials so that upstream provider prompt caches (which are keyed
// per-credential) stay hot for the whole conversation, while NEW
// conversations are spread across credentials by weighted random
// selection (cumulative prefix-sum, O(k) build / O(log k) pick).
//
// It additionally owns the Round-3 rate-limit failover state: a
// per-(model, credential) cooldown map consulted by every selection
// path, and ExcludeAndReselect — the single call that marks a 429'd
// credential cooling and rebinds the conversation to the next healthy
// credential.
//
// # Lifecycle
//
//	e := NewEngine(DefaultBindingTTL, DefaultSweepInterval, 0, DefaultCooldown)
//	defer e.Stop()
//
//	e.RebindFromStore(modelID, refs)     // once at startup, per model
//	e.OnModelChanged(modelID, newRefs)   // on credentials change
//	e.OnCredentialDeleted(credentialID)  // on credential deletion
//
//	credentialID, newlyBound, err := e.GetOrSelect(modelID, conversationKey)
//
// NewEngine starts a background janitor goroutine (sliding-idle TTL
// sweep + cooldown expiry sweep in ONE pass). Stop terminates it;
// Stop is idempotent and acknowledges exit via an internal done
// channel. After Stop, GetOrSelect still works (lazy expiry only).
//
// # Concurrency guarantees
//
// All exported methods are safe for concurrent use and nil-safe on a
// nil receiver (store call sites never need nil checks). The locking
// discipline is the contract's E-1 amendment:
//
//   - Engine.mu (outer RWMutex) guards the models map. Hot paths take
//     the outer RLock; only model-set mutations (OnModelChanged with
//     nil/absent state, OnCredentialDeleted scans) take the outer Lock.
//   - Each modelState owns its own RWMutex. GetOrSelect's happy path
//     reads under modelState RLock and upgrades to the write Lock for
//     the sliding-TTL boundAt refresh (#10) and for first selection.
//   - The janitor takes the OUTER RLock for the whole walk and each
//     per-model write lock one at a time — reads proceed concurrently
//     with the sweep (E-1; an outer write lock would stall all reads
//     every sweep).
//   - The engine never holds two modelState locks simultaneously; the
//     outer lock is always acquired before any modelState lock.
//   - No sync.Map — explicit RWMutex is the house style (opposite
//     access pattern: hot reads, occasional config writes).
//
// # Deliberate non-imports (layering contract)
//
// This package imports ONLY the standard library and pkg/models (for
// models.CredentialRef). It must NEVER import pkg/proxy, pkg/store, or
// pkg/events: invalidation reaches the engine via method calls, and
// the model.credentials.changed subscription lives in ModelsManager
// (pkg/store/database), which subscribes on the engine's behalf and
// forwards to OnModelChanged/OnCredentialDeleted.
//
// # C2 boundary — empty-key observability (contract, verbatim)
//
// The engine does NOT publish a model_credential_selected event for
// empty-key fresh picks, NOR a DEBUG log for them — DEBUG-log
// observability for empty-key fresh picks is phase3's responsibility
// (handler-side, cheap per-request log). The engine's only knowledge
// of the binding-store side effect is the W-1 NewlyBound signal in
// the GetOrSelect return; phase3 reads that signal and decides
// whether to publish model_credential_selected. Empty-key fresh picks
// are silent at the engine layer (per C2 invariant
// "newlyBound ⇔ a binding was stored on this call"; no binding stored
// ⇒ no signal). This boundary is part of the contract — the engine
// MUST NOT introduce an event/log for empty-key fresh picks in a
// future revision; doing so would re-introduce the silent hotspot
// risk P2-7 calls out.
//
// # Test-hook boundary (W8)
//
// ALL test hooks live in testhooks.go (one file, clearly test-scoped).
// Production engine code in engine.go / binding.go / selector.go
// never references them: `grep -n "ForTest" engine.go binding.go
// selector.go` returns zero hits, keeping the production/test boundary
// grep-greppable.
package credentiallb
