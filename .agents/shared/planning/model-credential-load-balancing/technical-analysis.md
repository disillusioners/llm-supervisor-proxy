# Technical Analysis: Model → Credentials Load Balancing

> **AMENDED 2026-08-21 (architect council / leader rulings):** This
> contract has been surgically amended per 12 leader-final rulings
> (A-1, A-2, M-1, E-1, E-2, E-3, W-1, W-2, W-3, M-1-test mandatory,
> E-4 recommended, M-1-tracked, W-1-tracked). Each amended section
> carries an `AMENDED 2026-08-21` blockquote. **Pin Amendment Changelog**
> (end of file) for downstream-worker verification — grep there for
> coverage of every cross-reference.
>
> **AMENDED 2026-08-25 (Round 3 — Rate-Limit Failover):** Surgical,
> append-only amendment per eight pre-pinned dispatcher rulings
> (R3-1..R3-8). Existing rulings are UNCHANGED. New API Contract
> additions under `### pkg/providers — error-classification additions`,
> new engine block under `### pkg/credentiallb — engine additions
> (Round 3)`, new top-level `## Rate-Limit Failover Precedence Tree`,
> new "Round-3 Cooldown Map Locking" sub-section in `## Concurrency
> Model`, new rows in `## Backward Compatibility Matrix`, and new
> `### Round 3 — Rate-Limit Failover (2026-08-25)` at the end. **Pin
> Amendment Changelog Round 3** for downstream-worker verification.
> Companion decision-log: `decisions.md` §F (new) and Round 3
> changelog.

Date: 2026-08-21 (original); 2026-08-21 (amended)
Author: planner[v2] via technical-analysis worker
Analysis depth: deep-dive (downstream plan-phase workers will implement against this contract)
Status: Draft — amended; ready for plan-phase workers
Companion: `decisions.md` (the why) -- this file is the what (the contract)

---

## Question

> **AMENDED 2026-08-21 (A-1, A-2):** The conversation identity in
> requirement (1) is now `sha256(model_id + "|" + token_id + "|" +
> first_user_message)` with canonical-JSON hashing for multimodal
> content.

How should the proxy balance requests for one model across 2..N upstream
credentials (same provider) so that:

1. Each **conversation** (defined by `(model_id, token_id,
   sha256(first_user_message))` where multimodal content is
   canonical-JSON-hashed per A-2) is pinned to **exactly one**
   credential for its lifetime (sticky affinity for upstream
   prompt-cache hits), AND
2. Across **all conversations** for a model, requests are distributed
   proportionally to the integer weights configured per credential
   (weighted random), AND
3. Single-credential models behave **byte-identically** to today (no LB,
   no map writes -- see E-3), AND
4. Credential set changes (add / remove / reweight) invalidate stale
   bindings safely, AND
5. Existing ultimate-model behavior is preserved (with LB applied
   independently on the ultimate side)?

---

## Context Summary

The proxy today resolves one credential per model via
`models.credential_id TEXT` (singleton FK-by-convention). All four
code paths that perform upstream auth (race-internal, race-external,
ultimate-internal, ultimate-external) read either that single column or
the global `cfg.UpstreamCredentialID` env var.

The store layer (`pkg/store/database/store.go`) maintains models +
credentials in SQLite or PostgreSQL via an embed-driven ordered
migration array (`pkg/store/database/migrate.go:24-52`, latest = 027).
House style for multi-valued fields is **JSON-array TEXT columns on the
parent row** (`fallback_chain_json`, `truncate_params_json`,
`auth_tokens.allowed_models`) — there are zero join tables anywhere.
Validation is app-level (no DB-enforced FKs); credential-deletion is
guarded by `ErrCredentialInUse` (`store.go:1289-1295`).

The proxy handler (`pkg/proxy/handler.go`) is constructed via DI from
`cmd/main.go:108`: `proxy.NewHandler(proxyConfig, bus, reqStore,
bufferStore, tokenStore, usageCounter)`. An event bus (`pkg/events/bus.go`)
exists and is already used for config-change invalidation
(`pkg/ultimatemodel/handler.go:150-160`).

Two new design decisions drive this feature:

- **Conversation key (AMENDED — A-1, A-2)**:
  `sha256(model_id + "|" + token_id + "|" + first_user_message)`,
  computed at the **post-auth wiring site** (`pkg/proxy/handler.go:401+`
  where `rc.tokenID` is populated; NOT in `initRequestContext` at
  `:353` where `tokenID` is unset), with canonical-JSON hashing for
  multimodal `[]interface{}` content (precedent at
  `pkg/ultimatemodel/hash_cache.go:172-186`). The existing
  `ultimatemodel.HashMessages` hashes the full role|content array and
  changes every turn, so it cannot be reused as a sticky key.
- **Persistence shape (AMENDED — M-1)**:
  `models.credentials_json TEXT NOT NULL DEFAULT '[]'` with
  `[{credential_id, weight, position}, ...]`. **We KEEP
  `models.credential_id` as a derived shadow** (same-statement UPDATE
  writes both). Migration 028 is ADD + backfill + shadow-write ONLY;
  DROP INDEX + DROP COLUMN is deferred to migration 029+ behind a
  release-note deprecation window. See `decisions.md` §B for the
  reversal rationale (old-binary-vs-migrated-DB total breakage,
  lossy down-migration, OSS external tooling).

---

## Architecture

### Current Patterns

- **Layered proxy**: HTTP entry → `initRequestContext` →
  `HandleChatCompletions` → race coordinator → per-attempt
  `executeInternalRequest` / `executeExternalRequest` →
  `ultimatemodel.Handler` (ultimate path). Pattern: layered with
  constructor-injected dependencies.
- **JSON-array columns on the parent row** for multi-valued fields (no
  join tables). See `pkg/store/database/migrations/sqlite/001_initial.up.sql:23-24`
  and migration 024 for the dialect-asymmetric backfill precedent.
- **RWMutex-protected maps** for in-memory caches (`pkg/models/credential.go:133`,
  `pkg/ultimatemodel/hash_cache.go:14`).
- **Immediate UPSERT** for per-request persistence (usage counters,
  `pkg/usage/counter.go:49-67`).
- **Event-bus-based config invalidation** (already used by ultimate model).

### New Patterns Introduced by This Feature

- **Sticky affinity engine** in a new package `pkg/credentiallb/`,
  injected into `proxy.NewHandler` (constructor DI).
- **Conversation-key field** on `requestContext`
  (`pkg/proxy/handler_helpers.go:23-108`) -- computed once at the
  **post-auth wiring site** (`handler.go:401+` where `rc.tokenID` is
  populated, NOT in `initRequestContext` at `:353` where the token is
  unset), reset on `rc.reset()`. The
  `firstUserMessage` extraction is cached separately on `rc` so the
  post-auth site can hash with the token.
- **Two-phase credential resolution**: `ResolveInternalConfig` (legacy,
  single-credential, unchanged) and `ResolveInternalConfigWithAffinity`
  (new, with `conversationKey` argument, calls the engine for
  multi-credential models).

### Module Boundaries

```
                 ┌─────────────────────────────┐
   HTTP ─────────┤ pkg/proxy/handler.go        │
                 │  • initRequestContext       │
                 │  • HandleChatCompletions    │
                 │  • (no credential logic)    │
                 └──────────┬──────────────────┘
                            │ uses
                            ▼
                 ┌─────────────────────────────┐
                 │ pkg/proxy/race_executor.go  │
                 │  • executeInternalRequest   │
                 │  • executeExternalRequest   │
                 │  • (calls into modelsMgr)   │
                 └──────────┬──────────────────┘
                            │ calls
                            ▼
   ┌─────────────────────────────────────────────────┐
   │ pkg/store/database/store.go                     │
   │  ModelsManager:                                 │
   │   • ResolveInternalConfig(modelID) [legacy]     │
   │   • ResolveInternalConfigWithAffinity          │
   │     (modelID, conversationKey) [NEW]           │
   │   • GetCredentials()                            │
   │   • AddModel / UpdateModel (now writes         │
   │     credentials_json)                          │
   └──────────┬──────────────────┬───────────────────┘
              │ uses             │ delegates
              ▼                  ▼
   ┌─────────────────────┐   ┌──────────────────────────────�
   │ pkg/models          │   │ pkg/credentiallb [NEW]       │
   │  • ModelConfig      │   │  • Engine                    │
   │  • Credentials []   │   │  • weightedSelector          │
   │    CredentialRef    │   │  • binding map (RWMutex)     │
   │  • CredentialConfig │   │  • TTL + janitor goroutine   │
   └─────────────────────┘   │  • OnModelChanged /          │
                             │    OnCredentialDeleted       │
                             └──────────────────────────────┘
                                        ▲
                                        │ uses
                            ┌───────────┴──────────────┐
                            │ pkg/events/bus.go       │
                            │  • Subscribe            │
                            │  (model.credentials     │
                            │   .changed)             │
                            └─────────────────────────┘
```

### Architecture Diagram (deep-dive)

```mermaid
flowchart LR
    client([HTTP Client])
    subgraph proxy [pkg/proxy]
        handler["HandleChatCompletions"]
        initRC["initRequestContext<br/>+ ExtractFirstUserMessage<br/>(rc.firstUserMessage)"]
        auth["auth.AuthToken<br/>(handler.go:401)<br/>rc.tokenID = ..."]
        postAuth["POST-AUTH wiring<br/>handler.go:401+<br/>rc.conversationKey =<br/>ComputeConversationKey<br/>(modelID, tokenID, firstUserMessage)"]
        raceExec["race_executor.go<br/>executeInternalRequest / ExternalRequest"]
        rc[("requestContext<br/>• resolvedModel<br/>• firstUserMessage<br/>• tokenID<br/>• conversationKey")]
        ultimate["ultimatemodel.Handler<br/>executeInternal"]
    end

    subgraph lb [pkg/credentiallb NEW]
        engine["Engine.GetOrSelect<br/>(modelID, convKey)<br/>-> (credentialID, newlyBound, err)"]
        selector["weightedSelector<br/>prefix-sum + binary search<br/>(per-model RNG, E-4)"]
        bindings["binding map<br/>sync.RWMutex<br/>(no writes on fast path, E-3)"]
        janitor["janitor goroutine<br/>5min sweep<br/>(outer RLock + per-model Lock, E-1)"]
        stats["Engine.Stats()<br/>map[string]EngineStats<br/>(Hits/Misses/Bindings, #5)"]
    end

    subgraph store [pkg/store/database]
        mm["ModelsManager"]
        models[("models<br/>credentials_json<br/>credential_id (shadow until 029)")]
        creds[("credentials")]
    end

    bus["events.Bus<br/>model.credentials.changed"]

    client --> handler
    handler --> initRC
    initRC --> rc
    handler --> auth
    auth --> postAuth
    postAuth --> rc
    handler --> raceExec
    raceExec --> mm
    ultimate --> mm
    mm --> engine
    engine --> selector
    engine --> bindings
    engine --> stats
    engine -. subscribes .-> bus
    bus -. publishes .-> mm
    mm --> models
    mm --> creds
    janitor -. sweeps .-> bindings
    raceExec -. uses .-> rc
    ultimate -. uses .-> rc
```

### Layering & Abstraction

- `pkg/credentiallb` exposes a minimal `Engine` API. It does NOT import
  `pkg/proxy`, `pkg/store`, or `pkg/events` — pure stdlib + `pkg/models`
  for the `CredentialRef` type.
- `ModelsManager` is the **only** place that bridges the engine to the
  store. It owns the engine instance, subscribes to the event bus on
  behalf of the engine, and surfaces `ResolveInternalConfigWithAffinity`
  to callers.
- The proxy package talks to `ModelsManager` only — never to the engine
  directly.

### Data Flow (race-internal, single credential)

1. HTTP request arrives → `initRequestContext` parses body,
   `rc.resolvedModel = ...`, `rc.originalMessages = snapshot`,
   `rc.firstUserMessage = ExtractFirstUserMessage(rc.originalMessages)`
   (per A-2: canonical-JSON hash for multimodal content; `""` for
   no-content).
2. Auth runs → `rc.tokenID` populated at `pkg/proxy/handler.go:401`.
3. **NEW (post-auth)**: at `handler.go:401+`,
   `rc.conversationKey = ComputeConversationKey(rc.resolvedModel.ID,
   rc.tokenID, rc.firstUserMessage)` -- salt prevents
   templated-agent-fleet skew (A-1).
4. `HandleChatCompletions` dispatches to race coordinator.
5. Race coordinator → `executeInternalRequest(req.modelID, ...)` →
   `cfg.ModelsConfig.ResolveInternalConfigWithAffinity(req.modelID,
   rc.conversationKey)`.
6. `ResolveInternalConfigWithAffinity` looks up the model:
   - If `model.Credentials` is empty → fallback to legacy
     `ResolveInternalConfig` (returns the single credential; identical to
     today).
   - If `model.Credentials` has 1 entry → fast path: return that
     entry's credential (no map writes per E-3, identical to today).
   - Else → `Engine.GetOrSelect(model.ID, convKey)` →
     `Engine.GetCredential(credentialID)` → returns the struct
     `ResolvedCredential` (provider, apiKey, baseURL, internalModel,
     credentialID, newlyBound) plus a trailing `ok bool` (REVISED
     2026-08-21 — #3, leader-ruled; struct form). The `CredentialID`
     field is the engine's selector output, useful for the
     `model_credential_selected` event payload and #16f prompt-cache
     observability. The `NewlyBound` field carries the W-1
     only-on-first-binding signal so the caller can publish
     `model_credential_selected` ONLY when `NewlyBound == true` — see
     the `ResolveInternalConfigWithAffinity` invariant block below.

### Data Flow (multi-credential, new conversation)

1. Same as above, but in step 6: `Engine.GetOrSelect` sees no binding
   for `(modelID, convKey)` → picks a credential via weighted random →
   stores the binding → returns `(credentialID, newlyBound=true, nil)`.
   The caller publishes the `model_credential_selected` event ONLY when
   `newlyBound == true` (per W-1). **NEW 2026-08-21 — #10 (sliding
   idle TTL)**: the stored binding's `boundAt` is set to `now`; on
   the next in-TTL hit, `boundAt` slides forward by another TTL window
   (24h of additional idle budget). The binding is eligible for
   expiry only after 24h of consecutive idle.

### Data Flow (multi-credential, follow-up request in same conversation)

1. Same as above; `Engine.GetOrSelect` finds the binding → returns
   `(credentialID, newlyBound=false, nil)` (per W-1). The caller does
   NOT publish the `model_credential_selected` event. **NEW 2026-08-21
   — #10 (sliding idle TTL)**: the engine's internal lazy-expiry
   check FIRST verifies `now - boundAt < ttl` (i.e., not idle past
   the 24h ceiling); if so, `boundAt` is REFRESHED to `now` (sliding
   semantics) and the binding is reused. If the binding is idle past
   the ceiling, it is dropped and a fresh credential is picked — the
   in-TTL reuse path is the dominant case for active conversations,
   which is the whole point of the feature.

---

## Integration Points

> **REVISED 2026-08-21 (reviewer pass — C1):** The Integration table
> below now lists five LB-participating paths (was four). The Anthropic
> `/v1/messages` internal path is the **PRIMARY client endpoint** for
> Claude Code and was **missing** from the original integration table
> — C1 adds it. Verification: `cmd/main.go:139` registers the route,
> `handler_anthropic.go:340` calls into `doAnthropicInternalRequest`,
> `handler_anthropic.go:447` is the function definition,
> `handler_anthropic.go:461` constructs `NewInternalHandler`,
> `internal_handler.go:67` is the `HandleRequest` signature, and
> `internal_handler.go:69` is the current 5-tuple `ResolveInternalConfig`
> call — the seam to swap. Thread:
> `anthropicRequestContext.conversationKey` (new field, per #16a C1+
> harmonization) → `InternalHandler.HandleRequest` extra arg →
> `ResolveInternalConfigWithAffinity`.

| # | Integration | Type | Contract | Auth | Failure Mode | File:Line |
|---|-------------|------|----------|------|--------------|-----------|
| 1 | race-internal → LB engine | sync | `ResolveInternalConfigWithAffinity(modelID, convKey)` returns `(ResolvedCredential, bool)` (REVISED 2026-08-21 — #3, leader-ruled struct form: `ResolvedCredential{Provider, APIKey, BaseURL, InternalModel, CredentialID, NewlyBound}` + trailing `ok bool`) | n/a | Engine error → call site returns the same error class as today's `ResolveInternalConfig` failure (`fmt.Errorf("failed to resolve internal config for model %s", ...)`) | `pkg/proxy/race_executor.go:137` (call site); `pkg/store/database/store.go:1316-1350` (legacy impl); NEW signature at `pkg/store/database/store.go` (see §API Contract below) |
| 2 | ultimate-internal → LB engine | sync | Same as #1 | n/a | Same as #1 | `pkg/ultimatemodel/handler_internal.go:46` |
| 3 | **Anthropic `/v1/messages` internal → LB engine** **(NEW 2026-08-21 — C1, leader-ruled)** | sync | `ResolveInternalConfigWithAffinity(modelID, convKey)` returns `(ResolvedCredential, bool)` (REVISED 2026-08-21 — #3, leader-ruled struct form) | n/a | Same as #1 | `cmd/main.go:139` (route registration) → `pkg/proxy/handler_anthropic.go:340` (call into `doAnthropicInternalRequest`) → `handler_anthropic.go:447` (function def) → `handler_anthropic.go:461` (`NewInternalHandler` construction) → `pkg/proxy/internal_handler.go:67` (HandleRequest signature) → `internal_handler.go:69` (current 5-tuple `ResolveInternalConfig` seam). **PRIMARY client endpoint** for Claude Code. |
| 4 | ModelsManager → events.Bus | pub/sub | `Subscribe()` reads `model.credentials.changed` events with `Data: {model_id: string}` | n/a | If bus is nil at startup, engine works without invalidation (acceptable for the v1 deployment) | NEW subscription in `pkg/store/database/store.go` constructor |
| 5 | UI → ModelsManager | sync HTTP | `POST /fe/api/models` with `credentials: [{credential_id, weight, position}]` | session cookie (existing) | Validation error → HTTP 400 with `error.message` | `pkg/ui/server.go:380-460` (existing handler) — payload shape changes |
| 6 | Engine → ModelsManager.GetCredential | sync | `GetCredential(id)` returns `*CredentialConfig` with decrypted `APIKey` | n/a | `nil` if credential deleted mid-flight → engine drops binding, returns error | `pkg/store/database/store.go:1130-1170` |
| 7 | cmd/main.go → proxy.NewHandler | sync DI | `proxy.NewHandler` gains a 7th parameter `credEngine *credentiallb.Engine` (named `credEngine` per the #16a convention; the local variable in `cmd/main.go` is `credLB := credentiallb.NewEngine(...)` — see the convention blockquote in decisions.md §C) | n/a | nil engine → constructor skips wiring; legacy paths use `ResolveInternalConfig` only | `pkg/proxy/handler.go:108` (constructor) |

### Integration Details

**Integration 1+2+3: Race-internal, ultimate-internal, and Anthropic `/v1/messages` internal call sites (NEW 2026-08-21 — C1)**

- **Protocol**: in-process function call.
- **Data format** (REVISED 2026-08-21 — #3, leader-ruled struct form): `(string, string) → (ResolvedCredential, bool)` where `ResolvedCredential{Provider, APIKey, BaseURL, InternalModel, CredentialID, NewlyBound}` plus a trailing `ok bool`. See §API Contract / `ResolveInternalConfigWithAffinity` for the pin and rationale.
- **Authentication**: n/a (in-process).
- **Error handling**: caller returns the standard 500-shaped error
  (`failed to resolve internal config for model %s`); engine never panics.
- **Observability**: the existing per-request log lines (e.g.,
  `[DEBUG] Race attempt %d calling internal provider: %s ...` at
  `pkg/proxy/race_executor.go:151`) gain no new fields — the credential
  ID picked by the engine is logged via a new `INFO` line at engine
  entry, NOT at every call site. The Anthropic `/v1/messages` internal
  path (NEW — C1) reuses the same `proxy.publishEvent` hook for the
  `model_credential_selected` event so the UI sees the same event
  regardless of which endpoint the client used.
- **Known issues**: none. The legacy `ResolveInternalConfig` already
  returns `ok=false` for all known failure modes (model not found, not
  internal, credential missing); we re-use the same failure shape.

**Integration 3: Event-bus invalidation**

- **Protocol**: in-process pub/sub (`pkg/events/bus.go`).
- **Data format**: `Event{Type: "model.credentials.changed", Data:
  map[string]interface{}{"model_id": "..."}}`.
- **Authentication**: n/a.
- **Error handling**: subscriber is non-blocking; slow handlers drop
  events. We treat the engine's subscription as fire-and-forget; missed
  events result in stale bindings that the engine evicts on next lookup
  (the lookup checks `credentialID` is still in the model's configured
  list — see `GetOrSelect` invariant below).
- **Observability**: same as integration 1+2.
- **Known issues**: existing `pkg/config/config.go:518` publishes
  `config.updated` with the whole config object; the subscriber at
  `pkg/ultimatemodel/handler.go:155` checks for a `field` key that is
  never set — so that subscriber may be silently broken. **We do not
  fix it here**. Our new event has a typed shape that does not depend on
  that contract.

**Integration 4: UI payload**

- **Protocol**: HTTP/JSON (existing pattern).
- **Data format**: model POST body gains `credentials`:
  ```json
  {
    "id": "gpt-4o",
    "name": "gpt-4o",
    "enabled": true,
    "internal": true,
    "internal_model": "gpt-4o-2024-08-06",
    "credentials": [
      {"credential_id": "cred-A", "weight": 1, "position": 0},
      {"credential_id": "cred-B", "weight": 2, "position": 1}
    ]
  }
  ```
- **Authentication**: existing UI session auth.
- **Error handling**: server returns 400 with provider mismatch or
  unknown-credential errors. UI displays the error in the toast.
- **Observability**: standard HTTP access log.

---

## API Contract (Go Function Signatures — copy-pasteable)

### `pkg/models` — model types

```go
// pkg/models/config.go (additions)

// CredentialRef is a single (credential, weight, position) entry in a
// model's ordered, weighted credential list.
//
//   - CredentialID: references credentials.id (app-level FK).
//   - Weight: positive integer; the higher, the more often picked.
//     Default 1. Validation: must be > 0.
//   - Position: 0-based deterministic ordering. Used to break weight ties
//     (lower position wins) and to define the "primary" credential
//     (Credentials[0]).
type CredentialRef struct {
    CredentialID string `json:"credential_id"`
    Weight       int    `json:"weight"`
    Position     int    `json:"position"`
}

// ModelConfig (modified — credential_id REMOVED, credentials ADDED):
type ModelConfig struct {
    ID             string   `json:"id"`
    Name           string   `json:"name"`
    Enabled        bool     `json:"enabled"`
    FallbackChain  []string `json:"fallback_chain,omitempty"`
    TruncateParams []string `json:"truncate_params,omitempty"`

    Internal        bool           `json:"internal,omitempty"`
    // CredentialID REMOVED. Use Credentials[0] as the primary.
    InternalBaseURL string         `json:"internal_base_url,omitempty"`
    InternalModel   string         `json:"internal_model,omitempty"`

    // NEW: ordered, weighted credential list.
    // Empty for external/non-internal models.
    // Validation: if non-empty, every entry's CredentialID must exist in
    // the credentials table; every entry's weight must be > 0.
    Credentials []CredentialRef `json:"credentials,omitempty"`

    ReleaseStreamChunkDeadline Duration `json:"release_stream_chunk_deadline,omitempty"`

    PeakHourEnabled  bool   `json:"peak_hour_enabled,omitempty"`
    PeakHourStart    string `json:"peak_hour_start,omitempty"`
    PeakHourEnd      string `json:"peak_hour_end,omitempty"`
    PeakHourTimezone string `json:"peak_hour_timezone,omitempty"`
    PeakHourModel    string `json:"peak_hour_model,omitempty"`

    SecondaryUpstreamModel     string `json:"secondary_upstream_model,omitempty"`
    ExcludeFromUltimateSwitching bool `json:"exclude_from_ultimate_switching,omitempty"`
}
```

### `pkg/credentiallb` — new package

```go
// pkg/credentiallb/engine.go

// Engine maintains (modelID, conversationKey) → credentialID bindings
// with TTL-based eviction and weighted-random selection for new bindings.
//
// Concurrency: safe for concurrent use. All public methods are
// thread-safe.
//
// Lifecycle:
//
//   e := NewEngine(ttl, sweepInterval, rngSeed)
//   defer e.Stop()
//
//   e.RebindFromStore(modelID, refs)        // once at startup, per model
//   e.OnModelChanged(modelID, newRefs)      // on credentials change
//   e.OnCredentialDeleted(credentialID)     // on credential deletion
//
//   credentialID, newlyBound, err := e.GetOrSelect(modelID, conversationKey)  // AMENDED 2026-08-21 (#6, leader-ruled)
type Engine struct {
    // unexported fields; see engine.go internals
}

// NewEngine constructs an Engine. ttl is the **sliding idle TTL** on
// each binding (suggested 24h): the binding's `boundAt` is refreshed
// on every in-TTL `GetOrSelect` hit, and the binding is eligible for
// expiry only after `now - boundAt > ttl` of consecutive idle.
// sweepInterval is the janitor cadence (suggested 5m); rngSeed seeds
// the weighted-random RNG (0 = time-based seed).
//
// Starts a background janitor goroutine. Call Stop() to terminate it.
func NewEngine(ttl time.Duration, sweepInterval time.Duration, rngSeed int64) *Engine

// GetOrSelect returns the credential pinned to (modelID, conversationKey)
// if a fresh binding exists; otherwise picks a new credential via
// weighted random, persists the binding, and returns it.
// **AMENDED 2026-08-21 (reviewer pass — #6, #10, C2):** the doc-comment
// example signature now reads `credentialID, newlyBound, err := e.GetOrSelect(...)`
// (the 3-tuple, matching W-1); the W-1 block below is unchanged in
// semantics, but the empty-key invariant is tightened (C2): with
// `newlyBound ⇔ a binding was stored on this call`, empty-key picks
// return `newlyBound = false` because NO binding is stored (W-2 + C2).
// The TTL is **sliding on idle** (NEW 2026-08-21 — #10): `boundAt` is
// refreshed on every in-TTL hit, so a binding survives as long as the
// conversation is active; the binding is eligible for expiry only after
// 24h of consecutive idle.
//
// **AMENDED 2026-08-21 (W-1)**: signature gains `newlyBound bool`.
// The bool is the only signal that lets the caller publish the
// `model_credential_selected` event **only on first binding** (per
// request), and also enables `Engine.Stats()` (E-4 recommended).
// `newlyBound = true` ⇔ a fresh binding was created for this call;
// `newlyBound = false` ⇔ an existing (in-TTL) binding was reused.
//
// **AMENDED 2026-08-21 (W-2)**: when `conversationKey == ""`, the
// engine performs a **fresh weighted pick per call** with **NO
// binding stored**. The ""-as-own-bucket reading is REMOVED. **NEW
// 2026-08-21 (C2, leader-ruled)**: with this no-binding-stored
// invariant, **empty-key picks return `newlyBound = false`** — even
// though a fresh weighted pick happens in the call, **no binding is
// stored**, so the call is not a "new binding" in the W-1 sense.
// `newlyBound` is bound to the binding-store side effect, NOT to the
// pick side effect. The invariant is `newlyBound ⇔ a binding was
// stored on this call`. Empty-key picks therefore never publish
// `model_credential_selected` (the binding-store event) — instead the
// caller emits a DEBUG log (owned by phase3, not the engine) per the
// `#16f` prompt-cache observability note ("Hot path: cheap
// selection per call").
//
// Invariants:
//   - Returns ("", false, ErrNoCredentials) if modelID has no credentials
//     configured (callers must treat this as a 500).
//   - If a binding exists but the credentialID it points to is no longer
//     in modelID's configured list (e.g., the credential was deleted),
//     the stale binding is dropped and a new credential is picked.
//   - If a binding exists but its boundAt + ttl < now (i.e., it has
//     been idle longer than the TTL), the binding is dropped and a new
//     credential is picked (lazy expiry). **NEW 2026-08-21 — #10**:
//     `boundAt` is REFRESHED on every in-TTL hit, so a binding stays
//     alive as long as the conversation is active; the binding is
//     eligible for expiry only after 24h of consecutive idle.
//   - Single-credential fast path: NO map writes (E-3); always returns
//     `newlyBound = false` (no binding is stored).
//   - **Empty-key (NEW 2026-08-21 — C2)**: `conversationKey == ""` ⇒
//     fresh weighted pick per call, **NO binding stored**, returns
//     `newlyBound = false`. Pinned invariant;
//     `decisions.md §E` test #8 enforces it.
func (e *Engine) GetOrSelect(modelID, conversationKey string) (credentialID string, newlyBound bool, err error)

// OnModelChanged rebuilds the per-model selector (cumulative-sum prefix
// over the new weights) and **filters** existing bindings: preserves
// any binding whose credentialID still appears in `refs`; drops only
// orphan bindings (whose credential was removed).
//
// **AMENDED 2026-08-21 (E-2)**: was "drops ALL existing bindings for
// modelID" — REVERSED. The original clear-all semantics flushed cache
// affinity for every active conversation on routine weight nudges (e.g.,
// weight 1→2). Filter-survivors is strictly better and no harder to
// implement.
//
// Callers MUST invoke this after every change to modelID's
// credentials_json (add, remove, reweight).
func (e *Engine) OnModelChanged(modelID string, refs []models.CredentialRef)

// OnCredentialDeleted drops any binding whose credentialID == id
// across all models. **Round 3c — S6:** ALSO clears all cooldown
// entries for that credential (`cooldowns[modelID][credentialID]`
// across all modelIDs where it appears) — hygiene against
// unbounded map growth + stale `EngineStats.Cooldowns` gauge
// counts. Idempotent. Safe to call even if no bindings exist.
func (e *Engine) OnCredentialDeleted(credentialID string)

// RebindFromStore is the bulk-load counterpart of OnModelChanged for
// startup: given a model's configured credentials, set up the selector
// WITHOUT dropping existing bindings (none exist at startup). It is
// safe to call this multiple times for the same model — the last call
// wins.
func (e *Engine) RebindFromStore(modelID string, refs []models.CredentialRef)

// Stop terminates the janitor goroutine. Idempotent. After Stop(),
// GetOrSelect still works (lazy expiry only) but the janitor no
// longer sweeps.
func (e *Engine) Stop()
```

```go
// pkg/credentiallb/key.go

// ComputeConversationKey returns a 64-char hex SHA256 of
// (modelID + "|" + tokenID + "|" + firstUserMessage).
//
// **AMENDED 2026-08-21 (A-1)**: signature gains `tokenID string`.
// Salt is mandatory (post-auth source: `rc.tokenID` at
// `pkg/proxy/handler.go:401`); salt prevents templated-agent-fleet
// skew (see decisions.md §A).
//
// **AMENDED 2026-08-21 (W-2)**: if `firstUserMessage == ""`, the
// caller MUST skip the engine entirely and use weighted-random-without-
// affinity for that request (the engine itself also enforces this:
// `GetOrSelect(..., "")` returns a fresh pick with no binding stored).
//
// **AMENDED 2026-08-21 (A-2)**: when the first user message's content
// is multimodal `[]interface{}`, `ExtractFirstUserMessage` returns
// the canonical JSON of the content (sorted keys, no whitespace), not
// the raw array. Precedent at `pkg/ultimatemodel/hash_cache.go:172-186`.
func ComputeConversationKey(modelID, tokenID, firstUserMessage string) string

// ExtractFirstUserMessage returns the canonical content of the first
// message with role=="user":
//   - string content → return the string as-is
//   - []interface{} content (multimodal) → return canonical JSON
//     (sorted keys, no whitespace); per A-2
//   - missing/empty/null content or no user-role message → return ""
//
// **AMENDED 2026-08-21 (A-2)**: the multimodal-as-"" fallback is
// REMOVED. Canonical JSON hashing is the v1 contract.
//
// messages is the snapshot from rc.originalMessages
// ([]interface{} of map[string]interface{}).
func ExtractFirstUserMessage(messages []interface{}) string

// ErrNoCredentials is returned by GetOrSelect when the model has no
// credentials configured.
var ErrNoCredentials = errors.New("credentiallb: model has no credentials")
```

### `pkg/store/database` — store-layer changes

```go
// pkg/store/database/store.go (additions to ModelsManager)

// Engine returns the underlying *credentiallb.Engine. The handler
// constructor calls this only to pass the engine to
// proxy.NewHandler.
//
// Returns nil if the ModelsManager was constructed without an engine
// (legacy / tests). Callers must handle the nil case.
func (m *ModelsManager) Engine() *credentiallb.Engine

// ResolvedCredential is the return shape of ResolveInternalConfigWithAffinity.
// (REVISED 2026-08-21 — reviewer pass #3, leader-ruled: struct form; carries newlyBound.)
type ResolvedCredential struct {
    Provider      string
    APIKey        string
    BaseURL       string
    InternalModel string
    CredentialID  string // which credential in the model's list was selected (logging/events/UI)
    NewlyBound    bool   // true ⇔ this call stored a new binding (W-1 event signal)
}

// ResolveInternalConfigWithAffinity resolves a credential for modelID,
// applying the LB engine if the model has 2+ credentials.
//
// **REVISED 2026-08-21 (#3, leader-ruled) — return shape PINNED**:
// **struct `ResolvedCredential` + trailing `ok bool`** (the leader-ruled
// struct branch of the 6-tuple/struct ruling; struct carries both
// `credentialID` AND the ruled `newlyBound` — tuple form could not carry
// both without a 7th element, so the struct is the minimum-shape path
// that satisfies the ruling). The trailing `ok bool` is **kept** for
// parity with the legacy 5-tuple `ResolveInternalConfig` (`pkg/models/config.go:146`)
// and is the only failure signal — `ok = false` means callers must
// return the existing 500-shaped error. The struct fields map as
// follows vs. the legacy 5-tuple:
//
//   ResolvedCredential.Provider      ↔  legacy 5-tuple `provider`
//   ResolvedCredential.APIKey        ↔  legacy 5-tuple `apiKey`
//   ResolvedCredential.BaseURL       ↔  legacy 5-tuple `baseURL`
//   ResolvedCredential.InternalModel ↔  legacy 5-tuple `internalModel`
//   (ok bool)                         ↔  legacy 5-tuple `ok`
//
// `CredentialID` is the **new** field (per #16f prompt-cache
// observability and `model_credential_selected` event payload);
// `NewlyBound` is the **new** field (per W-1 only-on-first-binding:
// the rounded struct is the ONLY way `newlyBound` flows from the
// engine-binding-store side effect through to the proxy/ultimatemodel
// handlers — those call sites receive the resolution result through
// this seam, and if `newlyBound` doesn't flow through here, the W-1
// only-on-first-binding event is unimplementable at the call sites).
// Per C2: empty-key picks return `NewlyBound = false` because no
// binding is stored.
//
// **AMENDED 2026-08-21 (W-2, E-3)**: the conversationKey may be empty
// (no first user message found); the engine performs a fresh weighted
// pick per call with no binding stored. The single-credential fast
// path is a no-op engine call (length check) with **NO map writes**
// (E-3) — behavior is byte-identical to today.
//
// Behavior:
//   - If model.Credentials is empty → fall back to ResolveInternalConfig
//     (single-credential / legacy behavior; identical to today). Returns
//     `ResolvedCredential{}` with `ok = false` on failure.
//   - If model.Credentials has 1 entry → return that entry's credential
//     (no engine call needed; behavior identical to today, no map
//     writes per E-3). `CredentialID` is the singleton, `NewlyBound` is
//     `false` (single-credential fast path never stores a binding),
//     `ok = true`.
//   - If model.Credentials has 2+ entries → call engine.GetOrSelect
//     with conversationKey, then resolve the returned credential via
//     GetCredential(credentialID). `CredentialID` in the returned struct
//     is the SAME string passed to `GetCredential` (the engine-selector
//     output), useful for `model_credential_selected` event payloads
//     and #16f prompt-cache observability. `NewlyBound` is the engine's
//     own binding-store side-effect signal (W-1 invariant) — propagates
//     here so the caller can publish `model_credential_selected` ONLY
//     when `NewlyBound == true`.
//
// conversationKey may be empty (no first user message found) for
// single-credential fast path; the engine handles empty keys the same
// as non-empty keys (affinity is best-effort -- empty-key requests do
// NOT store a binding, per W-2, AND return `NewlyBound = false` per
// C2; the binding-store side effect is what `NewlyBound` reports).
func (m *ModelsManager) ResolveInternalConfigWithAffinity(
    modelID, conversationKey string,
) (ResolvedCredential, bool)

// AddModel / UpdateModel (modified):
//   - Accept ModelConfig.Credentials in the new shape.
//   - Validate: if Credentials is non-empty and the model is Internal,
//     every entry's CredentialID must exist in credentials; every
//     entry's weight must be > 0; every entry's provider must match
//     Credentials[0].CredentialID's provider (provider-match invariant).
//   - On success, marshal []CredentialRef to JSON and write to
//     credentials_json.
//   - After successful Add/Update, call
//     m.engine.OnModelChanged(modelID, model.Credentials).
//   - The legacy CredentialID field is gone — see migration 028.
//
// RemoveModel (modified):
//   - After successful delete, call m.engine.OnModelChanged(modelID, nil)
//     to drop any lingering bindings.
//
// RemoveCredential (modified):
//   - After the in-use guard check, call m.engine.OnCredentialDeleted(id)
//     for every model that references the credential via
//     Credentials[i].CredentialID == id.
```

### `pkg/proxy` — handler wiring

```go
// pkg/proxy/handler.go (additions)

// Config (modified — REMOVED CredEngine field, AMENDED 2026-08-21 W-3):
// Constructor-only injection is the law. There is ONE injection path
// (the 7th constructor arg), not two. The original contract's
// `Config.CredEngine` field is REMOVED; phase3's constructor-only
// rule wins.
type Config struct {
    ConfigMgr    config.ManagerInterface
    ModelsConfig models.ModelsConfigInterface
    EventBus     *events.Bus
    // CredEngine   *credentiallb.Engine  // REMOVED by W-3
}

// Handler (modified — add credEngine field):
type Handler struct {
    // ... existing fields ...
    credEngine *credentiallb.Engine  // NEW; may be nil
}

// NewHandler (modified signature):
//
//   func NewHandler(
//       config *Config,
//       bus *events.Bus,
//       store *store.RequestStore,
//       bufferStore *bufferstore.BufferStore,
//       tokenStore auth.TokenStoreInterface,
//       counter *usage.Counter,
//       credEngine *credentiallb.Engine,  // NEW (constructor-only, per W-3)
//   ) *Handler
//
// If credEngine is nil, legacy single-credential path is used
// everywhere (backward compat for unit tests that don't wire the
// engine).
```

```go
// pkg/proxy/handler_helpers.go (additions)

// requestContext (modified — add conversationKey + firstUserMessage):
//
// **AMENDED 2026-08-21 (A-1 wiring)**: conversationKey is computed
// POST-AUTH (handler.go:401+), NOT in initRequestContext (handler.go:353).
// The firstUserMessage extraction IS done in initRequestContext (cheap
// map walk) and cached so the post-auth site can hash with tokenID.
type requestContext struct {
    // ... existing fields ...
    firstUserMessage string  // NEW; set in initRequestContext
    conversationKey  string  // NEW; sha256(model_id + "|" + token_id + "|" + first_user_msg); set POST-AUTH
}

// reset (modified — clear conversationKey + firstUserMessage):
func (rc *requestContext) reset() {
    // ... existing resets ...
    rc.firstUserMessage = ""  // NEW
    rc.conversationKey  = ""  // NEW
}
```

```go
// pkg/proxy/handler_functions.go (modified initRequestContext)
//
// **AMENDED 2026-08-21 (A-1 wiring)**: initRequestContext sets
// `rc.firstUserMessage` ONLY (cheap map walk). The conversationKey
// (with tokenID salt) is computed at the post-auth wiring site
// (handler.go:401+); see the comment block below.

// In initRequestContext, AFTER rc.resolvedModel and rc.originalMessages
// are set, cache the first-user-message extraction:
//   if rc.resolvedModel != nil && rc.resolvedModel.Internal {
//       rc.firstUserMessage = credentiallb.ExtractFirstUserMessage(rc.originalMessages)
//   }
//
// If rc.resolvedModel is nil (external/unknown model), firstUserMessage
// stays "" and the engine path is skipped at every call site.

// POST-AUTH wiring site (handler.go:401+), AFTER rc.tokenID is set:
//   if rc.resolvedModel != nil && rc.resolvedModel.Internal {
//       rc.conversationKey = credentiallb.ComputeConversationKey(
//           rc.resolvedModel.ID,
//           rc.tokenID,
//           rc.firstUserMessage,
//       )
//   }
// NOTE: do NOT compute conversationKey inside initRequestContext; the
// tokenID is unset at that point (handler.go:353 vs handler.go:401).
```

### `pkg/providers` — error-classification additions (Round 3 — 2026-08-25)

> **AMENDED 2026-08-25 (Round 3 — R3-1)**: Rate-limit classifier
> additions live in `pkg/providers` (the layer that already owns
> `ProviderError` and `IsRetryable`). This is a NEW Round-3 subsection;
> existing `pkg/models` / `pkg/credentiallb` / `pkg/store/database` /
> `pkg/proxy` subsections are UNCHANGED.

```go
// pkg/providers/interface.go (additions — Round 3 + Round 3c — W2)

// ProviderError (extended — RetryAfter + ErrorType + ErrorCode fields):
//
// Existing struct (pkg/providers/interface.go:155-162) is extended
// ADDITIVELY. No existing field is renamed, removed, or reordered.
// **Round 3c — W2**: ErrorType + ErrorCode are NEW ADDITIVE fields,
// captured in `handleError` from the already-unmarshaled anonymous-
// local `{Error:{Message,Type,Code}}` struct (openai.go:238-246 today
// — the caller unmarshals the body, then discards the anonymous-
// local shape when constructing ProviderError). Populating these
// fields makes the Round-3b classifier matrix row 11 (503 +
// rate-limit body) implementable: row 11 requires reading the
// body's `code` field even when StatusCode ≠ 429, which the
// current shape cannot do.
type ProviderError struct {
    Provider   string
    StatusCode int
    Message    string
    Retryable  bool
    BufferID   string

    // NEW (R3-1): captured from the Retry-After response header when
    // StatusCode == 429 (or any retryable response that includes it).
    // Zero when the header is absent or unparseable. Used by R3-2's
    // ExcludeAndReselect to seed the per-credential cooldown.
    // Default-zero preserves existing error-comparison code paths
    // (tests assert Retryable: true without setting RetryAfter).
    RetryAfter time.Duration

    // NEW (Round 3c — W2): captured from the unmarshaled error body's
    // `type` and `code` fields. Empty string when the body is absent
    // or unparseable. Used by IsRateLimitError to support the matrix
    // row 11 case (503 + rate-limit body) AND by the /v1/messages
    // anthropic-passthrough classifier (handler_anthropic.go:297-345
    // → doAnthropicRequest — see W1), which classifies on
    // `arc.lastStatusCode == 429` OR response-body `type` substring
    // `rate_limit` and reads the captured values from these fields.
    // Default-zero (empty string) preserves existing error-comparison
    // code paths.
    ErrorType string
    ErrorCode string
}

// IsRateLimitError classifies an error as a 429-style rate-limit
// condition. Returns true for:
//
//   - HTTP 429 (StatusCode == 429 in a ProviderError)
//   - An unmarshaled OpenAI-compatible error body whose code/type
//     matches the rate-limit vocabulary (Round 3b — review amendment 6
//     — pinned ONE vocabulary table; see "Vocabulary table" below;
//     Round 3c — S1 adds `rate_limit_exceeded` to the code-equality
//     set). MiniMax emits the standard shape `{"error":{message,type,
//     code}}` and flows through the existing unmarshal at
//     pkg/providers/openai.go:238-245, so no new provider integration
//     is required — the classifier just reads the already-decoded
//     fields (Round 3c — W2: ErrorType + ErrorCode are read from the
//     extended ProviderError struct; the unmarshal target is no longer
//     discarded).
//
// Returns false for nil errors and non-ProviderError errors. Pure
// function; no I/O. Safe to call from any goroutine.
func IsRateLimitError(err error) bool
```

**Vocabulary table (Round 3b — review amendment 6, ONE pinned
table; Round 3c — S1 + W2 — `rate_limit_exceeded` added to the
code-equality set; covers `rate_limit` / `rate_limit_error` /
`rate_limit_exceeded` vocabulary in use across providers):**

| Source | Field | Value | Match type |
|---|---|---|---|
| `pkg/models/errors.go:7` | `ErrorTypeRateLimit` constant | `rate_limit` | equality |
| `pkg/models/errors.go:17` | `ErrorCodeRateLimit` constant | `rate_limit` | equality |
| `pkg/proxy/translator/errors.go:13` | translator error type | `rate_limit_error` | equality |
| `pkg/proxy/translator/errors.go:70` | translator error code | `rate_limit_error` | equality |
| `pkg/proxy/translator/errors.go:129` | translator error type | `rate_limit_error` | equality |
| Fixture: `pkg/proxy/translator/translator_test.go:603-636` | test corpus | mixed `rate_limit` / `rate_limit_error` | (test fixture) |
| **Round 3c — S1** | provider-emitted code | **`rate_limit_exceeded`** | **equality (NEW — added to code-equality set)** |

**Match semantics (Round 3b — pinned; Round 3c — S1 extends the
code-equality set):**

- **`type` field**: case-insensitive **substring** match on
  `rate_limit` (covers both `rate_limit` and `rate_limit_error` in a
  single comparison; also matches `rate-limit` / `ratelimit` if a
  brand uses a dash or omits the underscore; the
  `rate_limit_exceeded` literal S1 is matched by the SUBSTRING
  check on the `type` field per this row, since it begins with
  `rate_limit`). The substring is the ONLY vocabulary check on
  `type`.
- **`code` field**: **equality** match against either `rate_limit`,
  `rate_limit_error`, OR **`rate_limit_exceeded`** (Round 3c — S1;
  the third literal covers the OpenAI-style "rate_limit_exceeded"
  code that some upstream proxies emit). The code-equality set is
  `{rate_limit, rate_limit_error, rate_limit_exceeded}`.

This dual-vocabulary table replaces the Round-3 vague "rate-limit
code/type" wording — the classifier MUST match `rate_limit`,
`rate_limit_error`, and `rate_limit_exceeded` on the `code` field
via equality, and `rate_limit` as a substring on the `type` field.
The classifier itself does NOT default — caller treats zero
`RetryAfter` as "use 60s default" (separation of concerns).

**Retry-After absent flow (Round 3b — review amendment 6, amendment iii):**
when the `Retry-After` header is absent (common for many providers
that omit it on 429), `parseRetryAfter` returns `0` →
`ProviderError.RetryAfter` is zero → the F.2 caller
(`ExcludeAndReselect`) treats zero as "use 60s default" cooldown
seed. The classifier itself does NOT apply the default — separation
of concerns (classifier reads what's there; caller decides the
fallback).

**Out of scope (Round 3b — review amendment 6, amendment iv):**
**HTTP-200-with-embedded-error** is OUT OF SCOPE for R3-1. A 200
response whose body contains an embedded error envelope (mid-stream
parsing) would require parsing the SSE stream for `error:` /
`data:` event types — non-trivial and not requested. Marked as
**future work, not v1**. The R3-1 classifier checks StatusCode == 429
OR the body's `type`/`code` vocabulary; anything else is not a
rate-limit for v1 purposes.

```go
// pkg/providers/openai.go (additions — Round 3)

// In handleError (pkg/providers/openai.go:234-279), extend the
// ProviderError construction (currently at line 261-266) to capture
// the Retry-After header:
//
//   providerErr := &ProviderError{
//       Provider:   p.Name(),
//       StatusCode: resp.StatusCode,
//       Message:    msg,
//       Retryable:  retryable,
//       RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")), // NEW (R3-1)
//   }
//
// parseRetryAfter already exists at pkg/providers/openai.go:592-609
// (handles seconds + RFC1123 dates) but is DEAD CODE today
// (verified: grep -rn parseRetryAfter pkg/ --include="*.go" returns
// only the declaration site). Wiring it inside handleError is
// conflict-free: zero existing callers to update. Conflict-free
// import: parseRetryAfter is in the same package, no import change.
//
// Seed behavior: when the Retry-After header is absent (common for
// many providers that omit it on 429), RetryAfter is zero. The
// R3-2 caller treats zero as "use 60s default" so the classifier
// itself does NOT default — separation of concerns (classifier
// reads what's there; caller decides the fallback).
```

```go
// pkg/providers/openai.go (existing — wired by R3-1)

// parseRetryAfter parses the Retry-After header (existing at
// pkg/providers/openai.go:592-609; DEAD until R3-1):
//
//   - Empty header → returns 0
//   - All-digits string → returns time.Duration(seconds) * time.Second
//   - RFC1123 date string → returns time.Until(t)
//   - Unparseable → returns 0
//
// Wiring site: handleError (line 261-266, see above).
// Caller (R3-2 ExcludeAndReselect) treats zero as "use 60s default".
```

**Backward-compat note (R3-1).** The `RetryAfter time.Duration` field is
appended to the existing `ProviderError` struct at the tail. **All
existing error-comparison sites continue to work** because (a) the
field is a value type (not a pointer), so the default-zero value is
indistinguishable from "not set"; (b) tests asserting
`&ProviderError{Retryable: true}` continue to compile because the
field is optional; (c) `errors.As(err, &providerErr)` continues to
match the existing struct shape.

**Unit-test matrix (Phase 5, sibling worker, Round 3b — review
amendment 6, amendment ii — 503 row added; Round 3c — S1
`rate_limit_exceeded` row added):**

| Case | StatusCode | Error JSON | Expected `IsRateLimitError` | Expected `RetryAfter` |
|---|---|---|---|---|
| 1 | 429 | (empty body) | true | (header value or 0) |
| 2 | 429 | `{"error":{"type":"rate_limit","code":"rate_limit"}}` | true | (header value or 0) |
| 3 | 429 | `{"error":{"type":"rate_limit_error","code":"rate_limit_error"}}` | true | (header value or 0) |
| 4 | 429 | `{"error":{"message":"too many requests"}}` (no type/code) | true (StatusCode wins) | (header value or 0) |
| 5 | 500 | `{"error":{"type":"server_error"}}` | false | 0 |
| 6 | 401 | (empty body) | false | 0 |
| 7 | 429 | `{"error":{"type":"rate_limit","code":"rate_limit"}}` + `Retry-After: 30` | true | 30s |
| 8 | 429 | `{"error":{"type":"rate_limit","code":"rate_limit"}}` + `Retry-After: Wed, 21 Oct 2026 07:28:00 GMT` (future) | true | time.Until(...) |
| 9 | nil error | n/a | false | n/a |
| 10 | non-ProviderError | n/a | false | n/a |
| **11 (Round 3b — review amendment 6, amendment ii)** | **503** | `{"error":{"code":"rate_limit"}}` | **true** (body-code check when status ≠ 429) | (header value or 0) |
| **12 (Round 3c — S1)** | **429** | `{"error":{"type":"rate_limit","code":"rate_limit_exceeded"}}` | **true** (code-equality against `rate_limit_exceeded`) | (header value or 0) |
| **13 (Round 3c — S1 + W2)** | **503** | `{"error":{"type":"rate_limit_error","code":"rate_limit_exceeded"}}` | **true** (type substring AND code equality against `rate_limit_exceeded`) | (header value or 0) |

### `pkg/credentiallb` — engine additions (Round 3 — 2026-08-25)

> **AMENDED 2026-08-25 (Round 3 — R3-2, R3-4, R3-8)**: Engine gains
> `ExcludeAndReselect` (R3-2), the cooldown map (R3-4), and additive
> `EngineStats` fields (R3-8). Existing `GetOrSelect` / `OnModelChanged` /
> `OnCredentialDeleted` / `RebindFromStore` / `Stop` API is UNCHANGED.

```go
// pkg/credentiallb/engine.go (additions — Round 3b — B3 SUPERSEDES Round-3 signature;
// Round 3c — C2 unifies the ReselectMode semantics across all four
// surfaces — this enum, the return-bullets below, the precedence-tree
// pseudocode in technical-analysis.md, and the failure-modes table).

// ReselectMode describes the outcome of ExcludeAndReselect. The
// engine picks the mode; the caller enforces the F.4 fall-through
// contract based on it. Leader ruling (Round 3b) accepted the
// B3-recommended single-call enum shape over the two-call
// `ok bool` + `PickSoonestExpiring` alternative (the two-call shape
// risks an extra all-cooling pick looping the fallback chain to the
// terminal error incorrectly — the engine has no tried-set awareness).
//
// **Round 3c — C2 unified semantics (all four surfaces agree):**
//
//   - mode == ReselectHealthy: a credential is available for THIS
//     call. The engine returns a credentialID the caller should
//     spawn against (or, in the empty-key / B2 no-op sub-cases, the
//     current/freshly-picked credential). Caller MUST proceed with
//     credential failover — NOT fall through to model-fallback.
//   - mode == ReselectSoonestExpiry: ALL credentials are cooling
//     (0-of-N healthy); the engine picked the soonest-expiring
//     credential and emits the WARN. Caller MUST treat this as a
//     SINGLE attempt only — if that attempt also 429s, the cooldown
//     extends and the caller MUST fall through to model-fallback /
//     terminal error (no second call to ExcludeAndReselect, no loop).
//   - mode == ReselectNone: NO credential is available — either the
//     model has a single credential (no alternative exists) or
//     genuinely 0 credentials are valid. Caller falls through to
//     model-fallback.
type ReselectMode int

const (
    // ReselectHealthy: a credential is available and the caller MUST
    // proceed with credential failover. Sub-cases (Round 3c — C2):
    //   (a) Weighted-random among non-cooling (renormalized) picked a
    //       fresh healthy credential (the typical happy path).
    //   (b) B2 no-op (current binding already points at a credential
    //       OTHER than `excludedCredID`, because a concurrent same-
    //       conversation request already rebinded away from it) →
    //       return `(currentBinding.credentialID, ReselectHealthy)`.
    //       The unchanged binding is the rebind the concurrent
    //       request already committed; the caller continues with it.
    //       NOT an error, NOT ReselectNone.
    //   (c) Empty conversationKey (Round 1 W-2 semantics — no binding
    //       stored) → fresh NON-COOLING weighted-random (renormalized)
    //       pick, NO map write, `ReselectHealthy`. Mirrors the
    //       Round-1 C2 empty-key no-binding fast-path.
    // Caller spawns the new attempt against this credential; the
    // standard healthy-skip path applies on subsequent calls.
    ReselectHealthy ReselectMode = iota

    // ReselectSoonestExpiry: ALL credentials are cooling (0-of-N
    // healthy — Round 3b 0-of-N pin); the engine picked the
    // soonest-expiring credential and emits the WARN. Caller MUST
    // treat this as a SINGLE attempt only — if that attempt also
    // 429s, the cooldown extends and the caller MUST fall through to
    // model-fallback / terminal error (no second call to
    // ExcludeAndReselect, no loop). This is the F.4 contract.
    ReselectSoonestExpiry

    // ReselectNone: NO credential is available. Either (a) the model
    // has a single credential (no alternative exists); or (b) zero
    // credentials are valid (e.g., all credentials are excluded by
    // the caller via some out-of-band mechanism, or the model was
    // reconfigured to zero credentials between lookup and reselect).
    // Caller falls through to model-fallback.
    //
    // Round 3c — C2 narrowing: prior readings that returned
    // ReselectNone for (i) B2 no-op or (ii) empty conversationKey
    // are REJECTED — both now return ReselectHealthy. ReselectNone
    // is reserved for "no credential exists / no credential valid".
    ReselectNone
)

// ExcludeAndReselect marks `excludedCredID` as cooling for `modelID`
// (cooldown seeded from `retryAfter`, default 60s if zero), then
// REBINDS the existing (modelID, conversationKey) entry to the next
// healthy credential chosen by weighted random among non-cooling
// (renormalized), skipping cooling credentials.
//
// **AMENDED 2026-08-25 (Round 3b — R3-2 + B2 + B3 — supersedes
// Round-3 `(credentialID, ok)` signature; Round 3c — C2 unifies the
// mode mapping across all four contract surfaces)**: the Round 3b
// signature is `(credentialID string, mode ReselectMode)`. The
// `ReselectMode` return value carries the F.4 contract —
// caller-enforceable single-attempt-then-fall-through
// (ReselectSoonestExpiry) vs standard healthy-skip
// (ReselectHealthy) vs no-rebind (ReselectNone). The two-call
// `ok bool` + `PickSoonestExpiring` alternative was REJECTED: a
// second `ok=false` call after a soonest-expiry pick would return
// yet another all-cooling pick and loop the fallback chain to the
// terminal error incorrectly.
//
// **Round 3b — B2 PRECONDITION (concurrent double-429 idempotency;
// Round 3c — C2 returns ReselectHealthy, NOT ReselectNone)**: the
// rebind happens ONLY if
// `bindings[convKey].credentialID == excludedCredID`. If the current
// binding already points at a different credential (because a
// concurrent request on the same conversation already rebinded away
// from `excludedCredID`), this call is a NO-OP and returns
// `(currentCredentialID, ReselectHealthy)` — the unchanged binding is
// returned as `credentialID` with mode `ReselectHealthy` (Round 3c —
// C2: was `ReselectNone` in Round 3b; superseded in-place; the prior
// reading is preserved as audit trail in the changelog). This
// prevents the binding-flap that two concurrent double-429 requests
// would otherwise cause (request 1 rebinds A→B mid-attempt on B while
// request 2 re-marks A cooling and re-selects C). The unchanged
// binding is healthy — the caller proceeds with credential failover
// on the unchanged binding (R5 reviewer-5 invariant: do not
// model-switch while a healthy credential exists).
//
// Locking: outer Engine.mu Lock → per-model write Lock (one at a
// time). Reuses the E-1 discipline from Round 1. Single map write
// to `bindings[conversationKey]` (REBIND, not delete-then-create).
//
// **Invariants (Round 3b + Round 3c — C2 unification)**:
//   - Per-conversation retry accounting (which credentials were
//     already tried this request) lives in the REQUEST context
//     (requestContext / anthropicRequestContext / upstreamRequest),
//     NOT in the engine — the engine stays per-conversation-key
//     stateless about retries. The tried-set is mutated ONLY from
//     the `manage()` loop (single-goroutine, serialized by the
//     coordinator mutex — see Concurrency Model §Round-3b tried-set
//     invariant). **Round 3c — S2 pin:** the per-request tried-set
//     lives in `requestContext` (per the Round-3b single-goroutine
//     invariant), NOT in engine state.
//   - The cooldown map (`cooldowns[modelID][excludedCredID]`) is
//     SEPARATE from the binding map. A cooldown does NOT touch the
//     binding's `boundAt` (#10 sliding-TTL anchor); a rebind via
//     ExcludeAndReselect DOES refresh `boundAt` for the new
//     credential (per #10 semantics).
//   - Empty conversationKey (Round 3c — C2: was
//     `(currentOrEmptyCredentialID, ReselectNone)` in Round 3b;
//     superseded in-place): returns `(freshPickedCredID,
//     ReselectHealthy)` — fresh weighted random among non-cooling
//     (renormalized), NO map write. Mirrors the Round-1 C2 empty-
//     key no-binding fast-path. **Rationale:** the caller must
//     proceed with credential failover (the empty-key request
//     needs to be served by SOME credential); returning
//     ReselectNone would force the caller to model-switch while
//     a healthy credential exists — violating R5 reviewer-5.
//   - Single-credential model: returns `("", ReselectNone)` (no
//     other credential to fail over to). Caller falls through to
//     model-fallback unchanged.
//   - B2 precondition: only rebind if current binding matches
//     `excludedCredID`; otherwise no-op returning
//     `(currentCredentialID, ReselectHealthy)` — the current
//     unchanged binding.
//   - Genuinely no candidate (0 valid credentials — e.g., model
//     was reconfigured to zero credentials between lookup and
//     reselect, or all credentials are excluded via out-of-band):
//     returns `("", ReselectNone)`. Caller falls through to
//     model-fallback.
//   - Round 3c — S6: `OnCredentialDeleted(credentialID)` clears
//     any cooldown entries for that credential in addition to
//     dropping bindings (hygiene against unbounded map growth +
//     stale `EngineStats.Cooldowns` gauge counts). See
//     technical-analysis.md §Invalidation Semantics for the table
//     update.
func (e *Engine) ExcludeAndReselect(
    modelID, conversationKey, excludedCredID string,
    retryAfter time.Duration,
) (credentialID string, mode ReselectMode)
```

```go
// pkg/credentiallb/engine.go (engineState additions — Round 3b — gauge semantics for Cooldowns)

type engineState struct {
    mu          sync.RWMutex
    credentials []models.CredentialRef
    prefixSum   []int
    totalWeight int
    bindings    map[string]*binding   // existing — keyed by conversationKey
    rng         *rand.Rand

    // NEW (R3-4): cooldown map. Keyed by modelID → credentialID →
    // cooldownUntil time.Time. Guarded by the same outer+per-model
    // locking discipline (E-1): outer RLock for reads + janitor
    // sweep, per-model write Lock for cooldown updates. The cooldown
    // map is SEPARATE from the binding map — a cooldown does NOT
    // touch the binding's boundAt (#10 sliding-TTL anchor).
    cooldowns   map[string]map[string]time.Time

    hitsTotal     uint64
    missesTotal   uint64
    bindingsCount uint64
    // NEW (R3-8): additive stats counters.
    // Round 3b — amendment 7: Failovers is a monotonic counter;
    // Cooldowns is a GAUGE (current cooldown-map size recomputed at
    // each janitor sweep or read live on Stats()).
    failoversTotal  uint64  // total ExcludeAndReselect calls that returned mode=ReselectHealthy (monotonic counter)
    cooldownsTotal  uint64  // GAUGE (Round 3b): current cooldown-map size across all models; recomputed at each janitor sweep or live read on Stats()
}
```

```go
// pkg/credentiallb/engine.go (EngineStats extension — Round 3b — gauge semantics for Cooldowns)

// Existing EngineStats (Round 1 — pinned in #5):
//   type EngineStats struct {
//       Hits     uint64
//       Misses   uint64
//       Bindings uint64
//   }
//
// EXTENDED ADDITIVELY (Round 3 — R3-8). No existing field is
// renamed, removed, or reordered. The pinned #5 field-name
// discipline applies to the new fields too: `Failovers`, `Cooldowns`
// (no aliases). Round 3b — amendment 7: `Cooldowns` is a GAUGE
// (current map size), NOT a monotonic counter.
type EngineStats struct {
    Hits      uint64  // existing — in-TTL lookup hits
    Misses    uint64  // existing — lookups that selected a fresh credential
    Bindings  uint64  // existing — current binding-map size
    Failovers uint64  // NEW (R3-8) — monotonic counter; total credential failovers (ExcludeAndReselect calls that returned mode=ReselectHealthy)
    Cooldowns uint64  // NEW (R3-8, Round 3b — GAUGE) — current cooldown-map size across all models; recomputed at each janitor sweep or read live on Stats(). NOT a monotonic counter.
}
```

```go
// pkg/credentiallb/engine.go (weightedSelector modification —
// Round 3 + Round 3c — S3 wording standardization)

// weightedSelector now skips credentials with active cooldowns
// (the selection-after-cooldown invariant). The selection is
// **weighted random among non-cooling (renormalized)** (Round 3c
// — S3: this is the canonical wording; replaces the Round 3
// "weighted order skipping cooling" / "weighted selection"
// variants wherever selection-after-cooldown is described in
// active spec text — audit-trail blocks untouched):
//   for i, ref := range state.credentials {
//       if cooldown, ok := state.cooldowns[modelID][ref.CredentialID]; ok && cooldown.After(now) {
//           continue  // skip cooling credential
//       }
//       // ... existing weight accumulation ...
//   }
// Weights are renormalized across the healthy set (sum of
// healthy weights becomes the new total).
//
// When ALL credentials are cooling (the all-cooling case per R3-4),
// the selector returns an empty slice — caller (race coordinator or
// ultimate handler) logs WARN, picks the SOONEST-EXPIRING credential
// for one last attempt, and if that attempt 429s again, the cooldown
// extends and the flow falls through to model fallback / terminal
// error. The all-cooling branch is owned by the CALLER, not the
// selector, because it requires the request context (attempt_index
// for R3-5 budget, event publishing for R3-8).
```

```go
// pkg/credentiallb/events.go (additions — Round 3)

const (
    EventBindingDropped     = "model_credential_binding_dropped"  // existing
    EventCredentialsChanged = "model.credentials.changed"         // existing
    EventCredentialFailover = "model_credential_failover"         // NEW (R3-8)
)

// Payload (published via events.Bus; the engine stays decoupled
// from events.Bus — caller publishes, NOT the engine):
//
//   {
//     "model_id":           string,
//     "from_credential_id": string,        // the credential that 429'd
//     "to_credential_id":   string,        // the next credential selected
//     "reason":             "rate_limit",  // constant for v1
//     "retry_after_ms":     int64,         // upstream-supplied; 0 if absent
//     "cooldown_ms":        int64,         // effective cooldown applied (> retry_after_ms for fallback default)
//     "attempt_index":      int            // 1..N-1 for this request (per R3-5 budget)
//   }
//
// Caller-side hook: ExcludeAndReselect returns (newCredID, ok=true);
// caller publishes the event AFTER the rebind succeeds. Sequence:
//   1. ExcludeAndReselect returns newCredID
//   2. Caller publishes model_credential_failover
//   3. Caller spawns the new upstream attempt (R3-3)
//   4. (eventually) caller spawn completes; if 429 again, repeat
//      from step 1 with the new "from" credential
```

**Janitor interaction (R3-4, Round 3b — gauge semantics).** The
existing janitor (5-minute sweep, outer RLock + per-model write Lock
one-at-a-time per E-1) sweeps expired cooldowns alongside idle-TTL
bindings in the SAME pass. A cooldown is expired iff
`cooldownUntil <= now`. **Round 3b — amendment 7 (gauge
semantics):** the janitor RECOMPUTES `EngineStats.Cooldowns` to the
current size of the cooldown map at each sweep (the field is a
**GAUGE**, NOT a monotonic counter — the Round-3 increment-per-
removal prose is REPLACED). Sweep order within a model: cooldowns
first (cheaper map), then bindings (more expensive because of the
sliding-TTL check). No new janitor is introduced. `Failovers`
remains a monotonic counter (incremented once per successful
`ExcludeAndReselect` call that returned `mode == ReselectHealthy`).

**Sliding-TTL interplay (R3-4 cross-ref to #10).** The cooldown map
is orthogonal to the binding map:

| Operation | Cooldown map touched? | Binding map touched? | `boundAt` refreshed? |
|---|---|---|---|
| `GetOrSelect` happy path (in-TTL hit) | NO | NO (read-only) | YES (#10 sliding-TTL semantics) |
| `GetOrSelect` first selection | NO | YES (new entry) | YES (new entry has `boundAt = now`) |
| `GetOrSelect` lazy expiry eviction | NO | YES (drop+rebind) | YES (new entry has `boundAt = now`) |
| `ExcludeAndReselect` (R3-2) | YES (set `cooldowns[modelID][excludedCredID]`) | YES (single rebind write) | YES (rebind refreshes `boundAt` per #10) |
| Janitor sweep | YES (drop expired cooldowns) | YES (drop idle-TTL bindings) | NO (drop is destructive, no refresh) |

The cooldown map's lifecycle is bounded by `max(retryAfter, 60s) +
clock-skew-tolerance` (typically < 5min in practice). The binding
map's lifecycle is bounded by the sliding-idle TTL (24h default). The
two maps are independent — a cooldown does not promote or demote a
binding's `boundAt`.

### Migration SQL (028, AMENDED 2026-08-21 — M-1)

> **AMENDED 2026-08-21 (M-1, M-1-test)**: 028 is now ADD +
> backfill + same-statement **derived shadow write** to
> `credential_id`. **NO DROP COLUMN.** The DROP INDEX + DROP COLUMN
> are deferred to migration **029+** behind a release-note
> deprecation window. The PG-gated 028 transaction test is
> **MANDATORY** (this is the repo's first file-level transactional
> migration; see the testing section). The `DROP INDEX IF EXISTS
> idx_models_credential_id` statement is **NOT** included in 028
> because 028 keeps the column -- it would be a no-op and the index
> stays until 029.

**File: `pkg/store/database/migrations/sqlite/028_add_model_credentials.up.sql`**

```sql
-- Migration 028: Add models.credentials_json (ordered, weighted list)
-- and backfill from existing models.credential_id rows.
--
-- **AMENDED 2026-08-21 (M-1)**: this migration does NOT drop
-- models.credential_id. The column is kept as a DERIVED SHADOW --
-- the same UPDATE statement that writes credentials_json also
-- writes credential_id (= credentials_json[0].credential_id) so
-- that legacy binaries and external tooling keep reading a
-- consistent value. DROP INDEX + DROP COLUMN are deferred to
-- migration 029+ behind a release-note deprecation window.
--
-- The transaction is the repo's FIRST file-level BEGIN...COMMIT
-- pair through the SQLite/modernc.org/sqlite v1.46.1 driver +
-- pgx/v5 ExecContext pairing. A MANDATORY PG-gated test must
-- PROVE the transaction commits on both dialects (see testing
-- section); do not ship without it.

BEGIN TRANSACTION;

-- 1. Add the new column.
ALTER TABLE models ADD COLUMN credentials_json TEXT NOT NULL DEFAULT '[]';

-- 2. Backfill + shadow write (same statement).
--    For every row with a non-empty credential_id:
--      credentials_json = [{"credential_id", "weight":1, "position":0}]
--      credential_id   = credential_id   (shadow, no-op)
--    The shadow write is a tautology; it lives in the same
--    UPDATE so that any future writer that touches only
--    credentials_json is forced (by code review) to also touch
--    credential_id. Rows with credential_id = '' or NULL stay
--    with credentials_json = '[]' and shadow = ''.
UPDATE models
SET credentials_json = '[{"credential_id":"' || credential_id ||
                       '","weight":1,"position":0}]',
    credential_id   = credential_id  -- shadow (no-op today; load-bearing for 029)
WHERE credential_id IS NOT NULL
  AND credential_id != '';

-- 3. NO DROP COLUMN. credential_id stays; DROP moves to 029+.

COMMIT;
```

**File: `pkg/store/database/migrations/postgres/028_add_model_credentials.up.sql`**

```sql
-- Migration 028 (Postgres, AMENDED 2026-08-21 M-1): ADD + backfill +
-- same-statement shadow write to credential_id. NO DROP COLUMN
-- (deferred to 029+).
--
-- The transaction is the repo's FIRST file-level BEGIN...COMMIT
-- through pgx/v5 ExecContext; MANDATORY PG-gated test required.

BEGIN;

-- 1. Add the new column.
ALTER TABLE models
    ADD COLUMN credentials_json TEXT NOT NULL DEFAULT '[]';

-- 2. Backfill + shadow write (same statement, JSONB-array form).
UPDATE models
SET credentials_json = (
        jsonb_build_array(
            jsonb_build_object(
                'credential_id', credential_id,
                'weight',        1,
                'position',      0
            )
        )::text
    ),
    credential_id = credential_id  -- shadow (no-op today; load-bearing for 029)
WHERE credential_id IS NOT NULL
  AND credential_id != '';

-- 3. NO DROP COLUMN. credential_id stays; DROP moves to 029+.

COMMIT;
```

**File: `pkg/store/database/migrations/sqlite/028_add_model_credentials.down.sql`**

```sql
-- Down migration (SQLite, AMENDED 2026-08-21 M-1, LOSSLESS).
--
-- Pulls credential_id back from credentials_json[0] (re-adding the
-- column if a future state has dropped it -- e.g., after 029 has
-- landed and someone down-migrates past it). The credentials_json
-- column itself is PRESERVED (a re-up restores the full list).
--
-- If credential_id is already present (typical case today, since
-- 028 does not drop it), this is a no-op for the column add and a
-- defensive re-write of the shadow from credentials_json[0].

BEGIN TRANSACTION;

-- Re-add the column only if missing (idempotent for the
-- pre-029-deprecation case). The IF NOT EXISTS form is not
-- supported on older PG; for SQLite we use the pragma check.
-- For now, assume the down-migration runs in a state where
-- credential_id EXISTS (pre-029 typical), so we skip the ADD.
-- If the column is absent, the operator must run a manual ALTER
-- TABLE first.

UPDATE models
SET credential_id = COALESCE(
        json_extract(credentials_json, '$[0].credential_id'),
        credential_id
    )
WHERE credentials_json IS NOT NULL
  AND credentials_json != '[]'
  AND json_array_length(credentials_json) > 0
  AND json_extract(credentials_json, '$[0].credential_id') IS NOT NULL;

-- NOTE: we do NOT DROP credentials_json -- it is preserved so a
-- re-up restores the full multi-credential list.

COMMIT;
```

**File: `pkg/store/database/migrations/postgres/028_add_model_credentials.down.sql`**

```sql
-- Down migration (Postgres, AMENDED 2026-08-21 M-1, LOSSLESS).
-- Preserves credentials_json so a re-up restores the full list.

BEGIN;

UPDATE models
SET credential_id = COALESCE(
        (credentials_json::JSONB -> 0 ->> 'credential_id'),
        credential_id
    )
WHERE credentials_json IS NOT NULL
  AND credentials_json != '[]'
  AND jsonb_array_length(credentials_json::JSONB) > 0;

-- NOTE: we do NOT DROP credentials_json.

COMMIT;
```

**File: `pkg/store/database/migrations/sqlite/029_drop_credential_id.up.sql` (DEFERRED — sketch only)**

```sql
-- Migration 029 (DEFERRED, AMENDED 2026-08-21 M-1): DROP INDEX +
-- DROP COLUMN credential_id from models. Gated on a release-note
-- deprecation window ("credential_id is derived; will be removed
-- in the next minor"). This migration is a TRACKED ISSUE at merge
-- time -- load-bearing for M-1.
--
-- File created at the same time as 028 but NOT added to the
-- migrations[] array. Becomes runnable when the deprecation window
-- closes (typically the next minor release).
--
-- SQLite DROP COLUMN requires the SQLite ≥ 3.35 column-add-without-
-- rewrite; modernc.org/sqlite v1.46.1 bundles ≥ 3.35, so this
-- works. SQLite also requires dropping dependent indexes first;
-- the index on credential_id is dropped in step 1.

BEGIN TRANSACTION;

DROP INDEX IF EXISTS idx_models_credential_id;  -- SQLite needs index drop before DROP COLUMN
ALTER TABLE models DROP COLUMN credential_id;

COMMIT;
```

**File: `pkg/store/database/migrations/postgres/029_drop_credential_id.up.sql` (DEFERRED — sketch only)**

```sql
-- Migration 029 (Postgres, DEFERRED). pgx/v5 handles DROP COLUMN
-- in a single statement; the index is dropped in the same
-- transaction for symmetry with the SQLite form.

BEGIN;

DROP INDEX IF EXISTS idx_models_credential_id;
ALTER TABLE models DROP COLUMN credential_id;

COMMIT;
```

**File: `pkg/store/database/migrate.go`** (add to the `migrations` array at line 24-52, after `027`):

```go
var migrations = []migration{
    // ... existing 001..027 ...
    {"027", "027_add_exclude_from_ultimate_switching.up"},
    {"028", "028_add_model_credentials.up"},  // NEW (amended)
    // {"029", "029_drop_credential_id.up"},  // DEFERRED, see tracked issue
}
```

**File: `pkg/store/database/store.go` (write-path shadow maintenance, ~3 lines per AMENDED — M-1)**

```go
// AddModel / UpdateModel (modified, AMENDED 2026-08-21 M-1):
//   - Accept ModelConfig.Credentials in the new shape.
//   - Validate: if Credentials is non-empty and the model is Internal,
//     every entry's CredentialID must exist in credentials; every
//     entry's weight must be > 0; every entry's provider must match
//     Credentials[0].CredentialID's provider (provider-match invariant).
//   - On success, marshal []CredentialRef to JSON and write to
//     credentials_json.
//   - **NEW (~3 lines, M-1)**: in the same UPDATE, write
//     credential_id = credentials_json[0].credential_id (the
//     derived shadow). Until 029 lands, the shadow is required;
//     after 029, this write is dropped.
//   - After successful Add/Update, call
//     m.engine.OnModelChanged(modelID, model.Credentials)
//     (E-2: filter-survivors, not clear-all).
//   - The CredentialID field on ModelConfig is GONE (the column
//     stays; the field is removed from the struct).
//
// RemoveModel (modified):
//   - After successful delete, call m.engine.OnModelChanged(modelID, nil)
//     to drop any lingering bindings.
//
// RemoveCredential (modified):
//   - After the in-use guard check, call m.engine.OnCredentialDeleted(id)
//     for every model that references the credential via
//     Credentials[i].CredentialID == id.
```

---

## Concurrency Model

> **AMENDED 2026-08-21 (E-1, E-2, E-3, E-4)**: the locking discipline
> table below corrects the original self-contradiction (janitor
> stalling all reads) and the `OnModelChanged` semantics
> (filter-survivors, not clear-all). The single-credential fast path
> does no map writes (E-3). E-4 recommends per-model RNG / Stats() /
> janitor panic-recovery.

### Engine Internals

```go
type Engine struct {
    mu        sync.RWMutex                  // outer lock
    models    map[string]*modelState        // modelID -> state
    ttl       time.Duration
    sweepInt  time.Duration
    // **AMENDED 2026-08-21 (E-4)**: per-model RNG (one per modelState),
    // NOT a single shared rand.Rand. Removes first-selection contention
    // across models. See modelState below.
    stopCh    chan struct{}                 // janitor shutdown
    sweepDone chan struct{}                 // janitor exit ack
}

type modelState struct {
    mu         sync.RWMutex                 // per-model lock
    credentials []models.CredentialRef      // current config
    prefixSum  []int                        // cumulative weights
    totalWeight int
    bindings   map[string]*binding          // conversationKey -> binding
    // **AMENDED 2026-08-21 (E-4)**: per-model RNG.
    rng        *rand.Rand
    // **AMENDED 2026-08-21 (E-4)**: per-model stats counters for Stats().
    // **NEW 2026-08-21 (#5, leader-ruled) — Stats() field names PINNED**:
    // the public Stats() shape is
    //      Engine.Stats() map[string]EngineStats
    //   where `EngineStats` is the per-model struct:
    //      type EngineStats struct {
    //          Hits     uint64  // in-TTL lookup hits (sliding-TTL refresh)
    //          Misses   uint64  // lookups that selected a fresh credential
    //          Bindings uint64  // current binding-map size for this model
    //      }
    // The per-model binding-count field is named `Bindings` (no other
    // alias — `binding_total`, `count`, `len(bindings)` are all
    // rejected). `EngineStats` is exported so callers can range over
    // `Engine.Stats()` and read fields by name; the field-name choice
    // (`Bindings`) is locked across all operator-facing surfaces
    // (logs, expose, debug). Empty-key requests update `Misses` (a
    // fresh pick happened) but do NOT update `Bindings` (no store),
    // which falls out for free from the C2 invariant.
    hitsTotal   uint64
    missesTotal uint64
    bindingsCount uint64  // mirrors len(bindings) for O(1) Stats()
}

type binding struct {
    credentialID string
    boundAt      time.Time
}

// EngineStats is the per-model stats shape returned by Engine.Stats().
// **PINNED 2026-08-21 (#5)**: field names are `Hits`, `Misses`,
// `Bindings` (no aliases). See the modelState doc comment above.
type EngineStats struct {
    Hits     uint64
    Misses   uint64
    Bindings uint64
}

// Stats returns a snapshot of per-model engine stats for operator
// visibility. Reads each model's snapshot under its own RLock; the
// resulting map is a value copy (callers may mutate it freely).
//
// **PINNED 2026-08-21 (#5)**: the per-model field names are
// `EngineStats.Hits`, `EngineStats.Misses`, `EngineStats.Bindings`.
// Decisions §E test #12 will assert the shape.
func (e *Engine) Stats() map[string]EngineStats
```

### Locking Discipline

> **AMENDED 2026-08-21 (E-1)**: the janitor takes the outer **RLock**
> (so reads proceed concurrently) and acquires each per-model write
> lock one at a time. The original contract's "janitor takes outer
> write lock" stalled ALL reads each sweep — contradicted the §Locking
> Edge Cases claim that the sweep "never blocks reads for more than
> one model." The amended variant preserves the claimed behavior.

| Operation | Locks acquired | Notes |
|-----------|---------------|-------|
| `GetOrSelect(modelID, convKey)` happy path | outer RLock → modelState RLock | 99% of requests |
| `GetOrSelect` first selection (no binding, convKey != "") | outer RLock → upgrade to modelState Lock | writes one binding entry |
| `GetOrSelect` empty conversationKey (W-2) | outer RLock → modelState RLock → upgrade to call weightedSelector | NO binding written |
| `GetOrSelect` lazy expiry eviction | outer RLock → upgrade to modelState Lock | drops binding, picks new |
| `OnModelChanged(modelID, refs)` **(AMENDED — E-2)** | outer Lock → modelState Lock | **filters** bindings: keeps those whose credential survives in `refs`; drops orphans. NOT clear-all. |
| `OnCredentialDeleted(credentialID)` | outer Lock → per affected modelState Lock | scans all models |
| `RebindFromStore(modelID, refs)` | outer Lock → modelState Lock | startup-time; not on hot path |
| Janitor sweep **(AMENDED — E-1)** | **outer RLock** → per modelState Lock (one at a time) | background; **reads proceed concurrently**; sweep is bounded by per-model scan time |

### Why RWMutex, Not sync.Map

House style (`pkg/models/credential.go:133`,
`pkg/ultimatemodel/hash_cache.go:14`) is explicit RWMutex. `sync.Map` is
optimized for write-once-read-many, which is the OPPOSITE of our access
pattern (hot reads; occasional config-change writes; frequent per-model
binding updates on first selection).

### Locking Edge Cases

- **Deadlock avoidance**: the engine never holds two modelState locks
  simultaneously. The outer map lock is acquired BEFORE any modelState
  lock, and released BEFORE acquiring another. The order is:
  outer → modelState → release outer → next modelState.
- **Janitor concurrency (AMENDED — E-1)**: the janitor acquires the
  outer **RLock**, which lets all `GetOrSelect` calls proceed
  concurrently. The sweep walks models one at a time, taking each
  model's write lock briefly to evict expired bindings. For typical
  100 models × 1000 active conversations, the per-model scan is
  sub-millisecond; the outer RLock is held for the duration of the
  walk but never blocks reads.

### Race-Detector Validation

The unit tests under `pkg/credentiallb/*_test.go` MUST pass with
`go test -race ./pkg/credentiallb/...`. See `decisions.md` §E test #5.

### Round-3 Cooldown Map Locking (R3-4)

> **AMENDED 2026-08-25 (Round 3 — R3-4)**: New cooldown map follows the
> existing E-1 outer-RLock + per-model write-lock discipline. No new
> lock primitives introduced.

| Operation | Locks acquired | Cooldown map touched? | Notes |
|-----------|---------------|------------------------|-------|
| `GetOrSelect` happy path | outer RLock → modelState RLock | NO (cooldown map untouched) | 99% of requests |
| `GetOrSelect` first selection | outer RLock → upgrade to modelState Lock | NO (cooldown untouched; bindings write only) | writes one binding entry |
| `GetOrSelect` lazy expiry eviction | outer RLock → upgrade to modelState Lock | NO (cooldown untouched; binding drop+write only) | drops binding, picks new |
| **`ExcludeAndReselect` (NEW R3-2)** | outer Lock → modelState Lock | **YES** — writes `cooldowns[modelID][excludedCredID]` AND rebinds `bindings[conversationKey]` | single map write each (rebind, not delete-then-create); bounded by `len(credentials)` retries per request (R3-5) |
| `OnModelChanged` (E-2) | outer Lock → modelState Lock | NO (cooldown untouched; filter-survivors only affects bindings) | preserved unchanged |
| **`OnCredentialDeleted` (Round 3c — S6)** | outer Lock → per affected modelState Lock | **YES — clears `cooldowns[modelID][credentialID]` across all models** | drops bindings AND clears cooldowns for the deleted credential (S6 hygiene against unbounded map growth + stale `EngineStats.Cooldowns` gauge counts; gauge recomputed on next sweep or live read via `Stats()`) |
| **Janitor sweep (extended R3-4)** | outer RLock → per modelState Lock (one at a time) | **YES** — drops expired cooldowns alongside idle-TTL bindings in same pass | sweep order: cooldowns first (cheaper), then bindings (sliding-TTL check). Outer RLock preserved from E-1 — reads proceed concurrently. |

**Lock ordering invariant (preserved from E-1).** The outer map lock is
acquired BEFORE any modelState lock, and released BEFORE acquiring
another. The engine never holds two modelState locks simultaneously
(deadlock avoidance).

**Cooldown map lifecycle.** `cooldowns[modelID][credID]` is bounded by
`max(retryAfter, 60s)` plus clock-skew tolerance (typically < 5min in
practice). A cooldown outlives the upstream 429 response by the
retry-after duration; once expired, the credential is eligible again.
The map size scales with `(active models × 429-during-window)`,
typically << 100 entries.

**`ExcludeAndReselect` deadlock-free path (R3-2 critical).** The
operation takes outer Lock + per-model write Lock — same locks used
by `OnModelChanged` and `OnCredentialDeleted`. Since the engine never
holds two modelState locks simultaneously (E-1), `ExcludeAndReselect`
cannot deadlock with config-change operations. Per-request retry
budget (R3-5) bounds total `ExcludeAndReselect` calls per request to
`len(credentials) - 1`, so the lock contention is bounded by the
credential count, not by request count.

**Cooldown map vs. binding map — concurrent reads.** `GetOrSelect`
takes outer RLock + modelState RLock; both maps are read under the
same RLock, so an `ExcludeAndReselect` mid-lookup will either happen
fully before the lookup (RLock acquired after the write completes) or
fully after (the lookup completes first). No torn reads: the cooldown
map and the binding map are separate Go maps, but they're guarded by
the same per-model write lock during writes, so a reader either sees
the pre-`ExcludeAndReselect` state or the post-`ExcludeAndReselect`
state, never a half-applied state.

**Backward-compat with Round 1 E-1 janitor.** The janitor (5-minute
sweep) now sweeps two maps in one pass instead of one. The sweep
order is cooldowns first, then bindings — cooldowns are cheaper to
evaluate (single `time.Time` comparison per entry vs. the binding's
sliding-TTL check). Per-sweep CPU increases by at most the size of
the cooldown map; in practice, the cooldown map is much smaller than
the binding map because cooldowns are bounded to the 429-window
whereas bindings persist for the 24h idle TTL.

### Round-3b — Tried-Set Single-Goroutine Mutation Invariant

> **AMENDED 2026-08-25 (Round 3b — review §2 pin):** the
> `requestContext` fields `triedSet` (the tried-this-request set) and
> `failoverAttempts` (the per-request attempt counter) are mutated
> **ONLY** from the `manage()` loop in `race_coordinator.go` (function
> def `race_coordinator.go:252`) — single-goroutine, serialized by
> the coordinator mutex. Concurrent attempts (`RaceMaxParallel > 1`)
> racing the map/int are an explicit invariant violation. The fields
> MUST carry a code comment to that effect at the declaration site;
> Phase 5 tests MUST assert the invariant under `go test -race`.

```go
// pkg/proxy/handler_helpers.go (additions — Round 3b tried-set invariant)

// triedSet records which credentials have been attempted on THIS
// request (R3-5 budget enforcement). Added to the same set when a
// spawn uses a credential; refusal to spawn another attempt whose
// credential is already in the set (R3-5 cap).
//
// Round 3b — SINGLE-GOROUTINE INVARIANT: this field is mutated ONLY
// from the race coordinator's manage() loop (race_coordinator.go:252),
// serialized by the coordinator mutex. Concurrent attempts
// (RaceMaxParallel > 1) racing the map are an explicit invariant
// violation — the race coordinator is the single writer. The
// mutex is the existing raceCoordinator.mu.
type triedSet map[string]struct{}

// failoverAttempts counts how many credential-failover attempts have
// run for THIS request. Read and reset ONLY from the manage() loop
// (serialized by raceCoordinator.mu). Used for R3-5 budget cap.
type failoverAttempts int

type requestContext struct {
    // ... existing fields ...
    firstUserMessage string  // existing
    conversationKey  string  // existing
    triedSet         triedSet         // NEW (Round 3) — R3-5 budget tracking
    failoverAttempts failoverAttempts // NEW (Round 3) — R3-5 budget tracking
}

// reset (modified — clear Round-3 fields):
func (rc *requestContext) reset() {
    // ... existing resets ...
    rc.firstUserMessage = ""
    rc.conversationKey  = ""
    rc.triedSet = nil          // NEW (Round 3) — cleared per-request
    rc.failoverAttempts = 0    // NEW (Round 3) — cleared per-request
}
```

The same invariant applies to the `requestContext` mirror fields
in `pkg/proxy/handler_anthropic.go:697` (`anthropicRequestContext`):
`arc.triedSet` and `arc.failoverAttempts` are mutated only from the
`/v1/messages` internal call sites (the `doAnthropicInternalRequest`
function and the anthropic-passthrough branch — Round 3b leader
ruling i). All such call sites are serialized by the request handler
lifecycle (single goroutine per request); the engine API does not
expose any concurrency that would race these fields.

**Unit-test coverage (Phase 5, sibling worker):** Round-3 tests must
exercise (a) concurrent `ExcludeAndReselect` + `GetOrSelect` (race
detector must not flag), (b) janitor sweep removes expired cooldowns
and updates `EngineStats.Cooldowns` (Round 3b — gauge semantics:
recomputed at sweep, NOT increment-per-removal), (c) cooldown map
does NOT touch `boundAt` on the binding it shadows, (d)
`ExcludeAndReselect` returns `mode == ReselectSoonestExpiry` when
all credentials are cooling (Round 3b — was `ok=false`; **Round 3c
— W3:** caller also sets `soonestExpiryAttempted = true` and
`continue`s; no second `modelTypeCredFailover` spawn), (e) empty
conversationKey returns `mode == ReselectHealthy` with a fresh
weighted random among non-cooling (renormalized) pick and NO map
write (**Round 3c — C2 supersedes the Round 3b "ReselectNone"
reading** — the sibling worker must update the phase-2 Task-12
test expectation to match), (f) concurrent same-conversation
double-429 (Round 3b — B2) — the second call is a no-op returning
the current unchanged binding with `mode == ReselectHealthy`
(**Round 3c — C2** — was `ReselectNone` in Round 3b), (g)
tried-set single-goroutine invariant under `go test -race` with
`RaceMaxParallel > 1` attempts.

---

## Rate-Limit Failover Precedence Tree (Round 3 — 2026-08-25)

> **AMENDED 2026-08-25 (Round 3 — R3-3, R3-5, R3-6)**: New
> top-level section pinning the full decision tree from upstream
> error → classify → (internal? multi-cred? pre-first-byte? budget?)
> → `ExcludeAndReselect` → respawn vs fall-through-to-model-fallback.
> Covers all LB'd internal paths (Round 3b — leader ruling i: now
> four paths including the `/v1/messages` anthropic-passthrough
> branch). Cross-ref decisions.md §F.3.
>
> **AMENDED 2026-08-25 (Round 3b — B1 + leader ruling iii + real
> guards + canonical naming + B3 mode-based fall-through):** the
> tree below supersedes the Round-3 tree with: B1 spawn-window gate
> exemption for `modelTypeCredFailover`; Case-2 uniform classification
> (rate-limit ⇒ credential failover first, on EVERY spawn path); real
> pre-first-byte guards (`c.winner == nil` for race; initial-call
> `*ProviderError` for ultimate; `arc.headersSent` per
> `handler_anthropic.go:648-654` for /v1/messages); canonical Go
> identifier `modelTypeCredFailover`; B3 mode-aware `ExcludeAndReselect`
> return (Healthy/SoonestExpiry/None); 0-of-N pin for "all-cooling".

### Three-Path Coverage (Round 3b — four paths, leader ruling i)

The decision tree applies uniformly to:

1. **Race-internal path** — `pkg/proxy/race_executor.go:137`
   `ResolveInternalConfig(req.modelID)` (to be replaced by
   `ResolveInternalConfigWithAffinity` per Round 1). Interception point
   for the failover spawn: `pkg/proxy/race_coordinator.go:350-364`
   `manage()` "Case 1" (function def `race_coordinator.go:252`) —
   the existing decision tree there is replaced by the precedence
   tree below.
2. **Ultimate-internal path** — `pkg/ultimatemodel/handler_internal.go:46`
   `executeInternal` calls `ResolveInternalConfig(modelCfg.ID)`.
   Round 3 hook is added: classify the returned `ProviderError`,
   check pre-first-byte (initial-call `*ProviderError` type
   assertion — `headersSent` is UNUSABLE because
   `ultimatemodel/handler.go:608-609` pre-sets it before
   `executeInternal`), call `ExcludeAndReselect`, re-attempt.
3. **`/v1/messages` internal — `doAnthropicInternalRequest` branch** —
   `pkg/proxy/internal_handler.go:111` `HandleRequest` calls
   `ResolveInternalConfig(h.config.ID)`. Same shape as the
   ultimate-internal hook (classify, check, rebind, re-attempt).
4. **`/v1/messages` internal — anthropic-passthrough branch (Round 3b
   — leader ruling i, NEW IN SCOPE; Round 3c — W1 classification
   contract)** — `pkg/proxy/handler_anthropic.go:297-345` for
   internal models whose credential provider is `"anthropic"`.
   This branch BYPASSES `doAnthropicInternalRequest` entirely
   (reads the single-cred `modelConfig.CredentialID` +
   `cred.ResolveAPIKey()` and calls `h.doAnthropicRequest(w, arc)`).
   MUST be swapped to the affinity resolver; the same R3-1 /
   R3-2 / R3-3 / R3-6 pipeline applies. Pre-first-byte guard:
   recorder's `arc.Code` + `arc.headersSent` per
   `handler_anthropic.go:648-654` (NOT
   `adapter_anthropic.go:170-176` — that is `SetStreamHeaders`).

   **Round 3c — W1 classification source contract:** this branch
   does NOT flow through `pkg/providers.OpenAIProvider.handleError`
   — it produces NO `*ProviderError`. The classifier for this
   branch is therefore separate from the Round 3 R3-1
   `IsRateLimitError`: classify on `arc.lastStatusCode == 429`
   OR response-body `type` substring `rate_limit`. The
   `Retry-After` header is captured via the NEW additive
   `arc.retryAfter time.Duration` field on
   `anthropicRequestContext` — captured at the point where
   `arc.lastStatusCode` is set (`handler_anthropic.go:423`).
   Today `doAnthropicRequest` does NOT touch `resp.Header`;
   the Retry-After capture is genuinely additive (no existing
   read site is disturbed). The classification reads `arc.lastStatusCode`
   + `arc.retryAfter` (the new additive W1 field) + the
   captured `ErrorType`/`ErrorCode` from the response body's
   `type` substring (`Round 3c — W2 ProviderError extension`).

### Decision Tree (ASCII flowchart) — Round 3b amended

```
                    ┌─────────────────────────────┐
                    │ Upstream returns Provider- │
                    │ Error from attempt #N      │
                    └─────────────┬───────────────┘
                                  │
                                  ▼
                    ┌─────────────────────────────┐
                    │ IsRateLimitError(err)?      │  (R3-1)
                    │ (status==429 OR JSON body   │
                    │  has rate-limit code/type)  │
                    └────┬───────────────┬────────┘
                         │               │
                      NO │               │ YES
                         ▼               ▼
            ┌─────────────────────┐  ┌────────────────────────────────────┐
            │ Fall through to     │  │ Pre-check #1: failed model is     │
            │ existing model      │  │ internal AND has >1 credential?   │
            │ fallback. STOP.     │  └────┬─────────────────────┬────────┘
            │ (Round 1 path —     │       │                     │
            │  UNCHANGED)         │    NO │                  YES│
            └─────────────────────┘       ▼                     ▼
                          ┌─────────────────────┐  ┌──────────────────────────────┐
                          │ Fall through to     │  │ Pre-check #2: no CONTENT     │
                          │ existing model      │  │ flushed to client yet?       │
                          │ fallback. STOP.     │  │ race: c.winner == nil        │
                          │ (single-credential  │  │ ultimate: initial-call       │
                          │  fast path)         │  │  *ProviderError assertion    │
                          └─────────────────────┘  │ /v1/messages: arc.headersSent │
                                                    │  per handler_anthropic.go:   │
                                                    │  648-654 (NOT adapter 170-176)│
                                                    │  + arc.Code == http.StatusOK │
                                                    └────┬─────────────────┬───────┘
                                                         │                 │
                                                      NO │              YES│
                                                         ▼                 ▼
                                          ┌──────────────────────┐ ┌───────────────────────┐
                                          │ Fall through to      │ │ Pre-check #3:         │
                                          │ existing model       │ │ remaining non-cooling │
                                          │ fallback. STOP.      │ │ credentials exist?    │
                                          │ (post-first-byte —   │ │ (GetOrSelect with     │
                                          │  can't swap mid-     │ │  cooldown skip)       │
                                          │  stream)             │ └────┬───────────┬──────┘
                                          └──────────────────────┘      │           │
                                                                     NO│         YES│
                                                                      ▼            ▼
                                                  ┌──────────────────────┐ ┌────────────────────┐
                                                  │ Pre-check #4:        │ │ Pre-check #4:      │
                                                  │ all-cooling case     │ │ retry budget not   │
                                                  │ (0-of-N ONLY) —      │ │ exhausted?         │
                                                  │ pick SOONEST-        │ │ (tried-this-req    │
                                                  │ EXPIRING credential  │ │  set, len <        │
                                                  │ (engine picks,       │ │  len(creds)-1)     │
                                                  │ emits WARN), single  │ └────┬─────────┬────┘
                                                  │ attempt by caller    │      │         │
                                                  │ via B3 mode           │   NO │       YES│
                                                  │ ReselectSoonestExpiry │      ▼         ▼
                                                  │ contract. If 429     │ ┌─────────┐ ┌──────────────────┐
                                                  │ again, cooldown      │ │ Budget  │ │ ExcludeAndReselect│
                                                  │ extends, fall        │ │ exhaust.│ │ (B3 mode-aware):  │
                                                  │ through to model     │ │ — fall  │ │ 1. Set cooldown   │
                                                  │ fallback / terminal. │ │ through│ │    on excluded    │
                                                  │ (R3-4 + B3 + 0-of-N)  │ │ to     │ │    credential     │
                                                  └──────────────────────┘ │ model  │ │    (seed=retryAft,│
                                                                          │ fall-  │ │     default 60s)  │
                                                                          │ back.  │ │ 2. B2 precondition│
                                                                          │ STOP.  │ │    rebind only if │
                                                                          │        │ │    binding.credID │
                                                                          │ (R3-5) │ │    == excludedID │
                                                                          └────────┘ │ 3. Rebind to next │
                                                                                    │    healthy (Resel-│
                                                                                    │    ectHealthy)    │
                                                                                    │    OR no-op (Res- │
                                                                                    │    electNone on   │
                                                                                    │    B2 fail)       │
                                                                                    │    OR soonest-exp │
                                                                                    │    (ReselectSoon- │
                                                                                    │    estExpiry)     │
                                                                                    │ 4. Caller publishes│
                                                                                    │    model_credential│
                                                                                    │    _failover event │
                                                                                    │ 5. Caller spawns  │
                                                                                    │    new upstream   │
                                                                                    │    attempt        │
                                                                                    │ 6. New attempt →  │
                                                                                    │    loop back to   │
                                                                                    │    "Upstream returns│
                                                                                    │    ProviderError" │
                                                                                    └──────────────────┘
```

**B1 gate exemption (Round 3b) + Round 3c — C1 hoisted form:**
the `modelTypeCredFailover` attempt satisfied by the spawned attempt
above does NOT consume the spawn-window slot at
`race_coordinator.go:338` (`len(c.requests) < len(c.models)`) and
does NOT count against the all-failed accounting at
`race_coordinator.go:420-421` (`len(c.requests) >= len(c.models)`).
Mechanism: maintain a SEPARATE accounting of model-attempts vs
credential-attempts (recommended — count only `modelType ∈ {main,
second, fallback}` against the window; `modelTypeCredFailover`
attempts track independently against the per-model credential
budget). The Round 3c — C1 HOISTED admission gate
`gate := modelAttempts < len(models) ||
credFailoverEligibleWithBudget()` is the explicit expression of
this separation (see Pseudocode below — the gate is evaluated
BEFORE the model-fallback spawn at `:338`-equivalent). After
credential exhaustion, the flow FALLS THROUGH to the existing
model-fallback spawn (`c.models[1]`) when it exists. This
rescues the user's core scenario: 3-credential single-model (no
fallback chain) gets the FULL failover chain
(`len(modelCfg.Credentials)−1` attempts before the terminal
message).

### Pseudocode (race-internal, the canonical site) — Round 3b amended + Round 3c (C1 hoisted form, C3 wiring, W3 soonestExpiryAttempted, C2 four-surface sync)

```go
// Inside race_coordinator.go manage() "Case 1"
// (function def race_coordinator.go:252; current lines 350-364 —
// body replaced by Round 3b + Round 3c — C1 hoisted form, C3 wiring,
// W3 soonestExpiryAttempted, C2 four-surface sync).
//
// Round 3c — C3(3) constructor wiring: the coordinator was constructed
// with (ctx, cfg, req, rawBody, models, eventBus, requestID,
// interleaved, engine, conversationKey) at handler.go:830 — the
// engine and conversationKey arrive via constructor args, NOT via
// Config.CredEngine. c.credEngine and c.conversationKey are the
// the per-coordinator references used below.

if running < c.cfg.RaceMaxParallel {
    latestReq := c.requests[len(c.requests)-1]  // Round 3b: latest-only inspection (Case-1)
    if latestReq.IsDone() && latestReq.GetError() != nil {
        err := latestReq.GetError()

        // ── HOISTED CRED-FAILOVER PRE-CHECKS (Round 3c — C1) ──
        // These run ABOVE the spawn-window gate (the equivalent of
        // race_coordinator.go:338) and ABOVE the all-failed check
        // (:420-421). The hoisted form is the only way the user's
        // core scenario (3 creds / 1 model / no fallback chain)
        // gets any failover at all.
        // ── Pre-check #1: rate-limit classifier (R3-1) ──
        if providers.IsRateLimitError(err) {
            // Extract retry-after from ProviderError (or from the
            // anthropic-passthrough's NEW additive arc.retryAfter
            // field — Round 3c — W1 — see OpenAIProvider /
            // anthropicRequestContext reads below).
            var retryAfter time.Duration
            var providerErr *providers.ProviderError
            if errors.As(err, &providerErr) {
                retryAfter = providerErr.RetryAfter
            }

            // ── Pre-check #2: failed model is internal AND >1 credential ──
            // (race coordinator already knows modelID; consult ModelsManager)
            modelCfg, _ := c.cfg.ModelsConfig.GetModel(latestReq.modelID)
            if modelCfg != nil && modelCfg.Internal && len(modelCfg.Credentials) > 1 {

                // ── Pre-check #3: no CONTENT flushed to client yet (Round 3b) ──
                // Race path: c.winner == nil is the expressible guard;
                // SSE framing headers + ": connected\n\n" + heartbeat DO
                // precede coordinator.Start() (handler.go:783-797) but carry
                // NO model content — those bytes are exempted as no-content.
                if c.winner == nil {

                    // ── Pre-check #4a: caller-side tried-set, retry budget (R3-5) ──
                    currentAttempt := c.attemptIndexFor(latestReq)
                    if currentAttempt < len(modelCfg.Credentials)-1 {

                        // ── Pre-check #4b: remaining non-cooling credentials ──
                        // Round 3b — B3 + Round 3c — C2 unified semantics:
                        // Healthy = proceed with failover (fresh pick OR
                        // B2 no-op OR empty-key fresh pick); SoonestExpiry
                        // = 0-of-N, single attempt; None = NO credential
                        // available (single-cred model or 0 valid creds).
                        nextCred, mode := c.credEngine.ExcludeAndReselect(
                            latestReq.modelID,
                            c.conversationKey,           // Round 3c — C3(3): injected via constructor
                            latestReq.credentialID,      // the credential that 429'd
                            retryAfter,
                        )
                        if mode == credentiallb.ReselectHealthy {
                            // Healthy (non-cooling) credential picked
                            // (or B2 no-op with current binding, or
                            // empty-key fresh pick — Round 3c — C2);
                            // caller spawns a new attempt below.

                            // Publish event (R3-8)
                            c.publishEvent("model_credential_failover", map[string]interface{}{
                                "model_id":           latestReq.modelID,
                                "from_credential_id": latestReq.credentialID,
                                "to_credential_id":   nextCred,
                                "reason":             "rate_limit",
                                "retry_after_ms":     retryAfter.Milliseconds(),
                                "cooldown_ms":        effectiveCooldown(retryAfter).Milliseconds(),
                                "attempt_index":      currentAttempt + 1,
                            })

                            // Spawn a SAME-MODEL, NEW-CREDENTIAL attempt (R3-3)
                            // Round 3c — C3(1): spawnTriggerInfo additively
                            // gains `credentialID` — the failed credential's
                            // replacement rides the trigger info from
                            // classification to spawn()/executor (verified
                            // absent at race_coordinator.go:79 today).
                            // Round 3c — C3(2): spawn() switch (currently
                            // ~196-218) gains `case modelTypeCredFailover:
                            // modelID = c.models[0]` — same model,
                            // credential re-selected.
                            // Round 3b — B1: modelTypeCredFailover does NOT
                            // consume the spawn-window slot at :338 and does
                            // NOT count against the all-failed accounting at
                            // :420-421 (separate accounting).
                            c.spawn(modelTypeCredFailover, spawnTriggerInfo{
                                trigger:       triggerRateLimit,
                                errorMessage:  err.Error(),
                                failedRequest: latestReq.id,
                                credentialID:  nextCred, // pre-resolved; spawn reads from this, not from the engine
                            })

                            // Add the new credential to caller-side tried-set
                            // (Round 3b — single-goroutine invariant: tried-set
                            // mutations are serialized by the coordinator mutex
                            // from the manage() loop only).
                            c.addToTriedSet(latestReq.modelID, nextCred)
                            continue // skip the existing model-fallback spawn below
                        }
                        if mode == credentiallb.ReselectSoonestExpiry {
                            // 0-of-N cooling (Round 3b — 0-of-N pin): the engine
                            // already emitted the WARN; caller MUST spawn a
                            // SINGLE attempt only, and if it 429s, fall through
                            // to model-fallback (no second ExcludeAndReselect).
                            // Single-attempt-then-fall-through is caller-enforced.
                            c.spawn(modelTypeCredFailover, spawnTriggerInfo{
                                trigger:       triggerRateLimit,
                                errorMessage:  err.Error(),
                                failedRequest: latestReq.id,
                                credentialID:  nextCred, // soonest-expiring
                            })
                            c.addToTriedSet(latestReq.modelID, nextCred)
                            // Round 3c — W3 single-shot guard: set the
                            // soonestExpiryAttempted flag and `continue` —
                            // the NEXT 429 (if any) routes straight to
                            // model-fallback. No second soonest-expiry
                            // spawn; no double-spawn loop.
                            c.soonestExpiryAttempted = true
                            continue
                        }
                        // mode == ReselectNone (Round 3c — C2 narrowing):
                        // NO credential is available — either single-
                        // credential model OR 0 valid credentials. Caller
                        // falls through to existing model-fallback below.
                        // (B2 no-op and empty-`conversationKey` are NOT
                        // ReselectNone; they are ReselectHealthy per the
                        // C2 unified enum.)
                    }
                }
            }
        }

        // ── C1 HOISTED ADMISSION GATE (Round 3c — replaces the
        // Round-3b "B1 gate exemption" paragraph; the pseudocode
        // equivalent of race_coordinator.go:338 + :420-421 with the
        // credential-failover accounting OR'd in).
        //
        // gate := modelAttempts < len(models) ||
        //         credFailoverEligibleWithBudget()
        //
        // modelAttempts counts only modelType ∈ {main, second,
        // fallback} (separate accounting; modelTypeCredFailover
        // attempts do NOT count here).
        // credFailoverEligibleWithBudget() = rate-limit classified
        // AND internal AND >1 creds AND c.winner == nil AND budget
        // remaining (the hoisted pre-checks above).
        //
        // When gate is true → existing model-fallback spawn below.
        // When gate is false → terminal "All models rate limited"
        // at race_coordinator.go:860-868 (UNCHANGED).
        modelAttempts := c.modelAttemptCount() // separate accounting
        credEligible := credFailoverEligibleWithBudget(c)
        gate := modelAttempts < len(c.models) || credEligible
        if !gate {
            // terminal — never reached when the hoisted pre-checks
            // would admit a credFailover attempt
            c.terminalAllModelsRateLimited()
            continue
        }

        // Existing model-fallback spawn path (UNCHANGED for non-rate-limit
        // errors, OR when any pre-check fails, OR when credential budget
        // is exhausted and a fallback model exists).
        errMsg := err.Error()
        c.spawn(modelTypeFallback, spawnTriggerInfo{
            trigger:       triggerMainError,
            errorMessage:  errMsg,
            failedRequest: latestReq.id,
        })
    }
}
```

### Caller-Side Tried-Set (R3-5 invariant) + Round 3b single-goroutine pin

The race coordinator owns the "tried-this-request" set. Per request:
the set starts empty; every credential used in an attempt is added to
the set; an attempt is refused if its credential is already in the
set (R3-5 budget cap). The set is cleared when the request completes
or fails terminally. Storage: `map[string]struct{}` on the
`raceCoordinator` struct (existing `mu sync.RWMutex` already guards
`requests`, `models`, etc.).

**Round 3b — Concurrency invariant (mandatory code comment at the
tried-set field declaration):** the tried-set and `failoverAttempts`
fields on `requestContext` / `raceCoordinator` are mutated ONLY from
the `manage()` loop (single-goroutine, serialized by the coordinator
mutex). Concurrent attempts (`RaceMaxParallel > 1`) racing the map
are an explicit invariant violation — the code MUST carry a comment
to that effect at the field declarations, and Phase 5 tests MUST
assert the invariant under `go test -race`. The coordinator mutex
guards all reads/writes of the tried-set and `failoverAttempts`,
serializing them against any spawned-upstream-attempt goroutines.

**Why the engine doesn't own this.** The engine is per-conversation-key
stateless about retries — its responsibility is "given a conversation,
which credential should it use?". The tried-set is a request-scoped
budget concern. Mixing them would force the engine to learn about
request lifetimes (which it doesn't), introduce cleanup-on-completion
coupling, and duplicate state that the request context already tracks.

**Case-2 idle-trigger classification (Round 3b — leader ruling iii,
SUPERSEDES the Round-3 non-goal reading):** the Case-2 idle-timeout
spawn path (`race_coordinator.go:364-395` → spawns `modelTypeSecond`
+ `modelTypeFallback` together at `:399-405`) ALSO classifies
rate-limit before spawning. The uniform precedence rule applies: a
rate-limit error ⇒ credential failover first, on EVERY spawn path.
Implementation: at the Case-2 spawn site, classify the err that
triggered the idle (if any) via `providers.IsRateLimitError`; on
rate-limit, route to credential failover instead of straight-to-
second/fallback spawn. The original Round-3 "non-goal" reading is
preserved in the decisions.md §F.3 audit trail.

**`modelTypeSecond` re-key (Round 3b — specified behavior):**
secondary attempts share `c.models[0]`'s modelID but resolve the
PRIMARY credential (verified at `race_executor.go:112`). A 429 on
a `modelTypeSecond` attempt re-keys failover to the primary
credential of `c.models[0]` (the secondary resolves to the primary
credential at the executor layer; the engine rebind writes the
primary back). This is **specified behavior, not an accident** —
it preserves provider-pool consistency within the same model.

### Pre-First-Byte Guard (R3-6, Round 3b — real guards)

The "no CONTENT sent to client" check has three (Round 3b — now
four with the passthrough branch) path-specific implementations,
all consistent with the existing architecture:

| Path | Pre-first-byte guard (Round 3b — real implementations) | Post-first-byte behavior |
|---|---|---|
| Race-internal | **`c.winner == nil`** (non-stream case is implicit — nothing written pre-winner). For streaming, the TRUE invariant is "no CONTENT flushes pre-winner" — SSE framing headers + `": connected\n\n"` comment DO precede `coordinator.Start()` at `handler.go:783-797` (verified) but carry NO model content. Round-3 invented `chunksFlushedToClient` / `bytesFlushedToClient` and the vacuous `status != statusStreaming` are DELETED (do not exist at HEAD). | Error propagates UNCHANGED. Race coordinator's `streamResult` (`handler.go:852`, def `:960`) and `handleNonStreamResult` (`handler.go:879`, def `:1327`) write the SSE error to the client. |
| Ultimate-internal | **Initial-call `*ProviderError` type assertion.** The Round-3 `headersSent` flag is UNUSABLE because `ultimatemodel/handler.go:608-609` sets `*headersSent = true` and `:643` starts the SSE heartbeat (`": heartbeat\n\n"`) BEFORE `executeInternal` runs. Initial-call errors return before any CONTENT write (`handler_internal.go:155-157` non-stream, `:253-255` stream). Mid-stream failures surface as SSE error events. | Error propagates UNCHANGED. `handleNonStreamResult` / `handleStream` already write the response. |
| `/v1/messages` internal — `doAnthropicInternalRequest` branch | **recorder's `arc.Code == http.StatusOK`** AND **`!arc.headersSent`** (captured in `anthropicRequestContext`). The header-write cite is `handler_anthropic.go:648-654` (`arc.headersSent`) — NOT `adapter_anthropic.go:170-176` (that is `SetStreamHeaders`, Round 3b cite fix). | Error propagates UNCHANGED. `arc.lastError` / `arc.lastStatusCode` already capture the failure for `sendAnthropicError`. |
| **`/v1/messages` anthropic-passthrough branch (Round 3b — leader ruling i, NEW)** | **Same `arc.headersSent` + `arc.Code` guards as the `doAnthropicInternalRequest` branch** — recorder decouples the actual write from the captured flag. The branch's pre-existing single-cred `modelConfig.CredentialID` is replaced by the affinity resolver at this point. | Error propagates UNCHANGED. The branch's downstream `h.doAnthropicRequest` already writes the response. |

### Failure Modes (documented, Round 3b — mode-aware; Round 3c — C2 four-surface sync)

| Failure | What happens | Recovery |
|---|---|---|
| `IsRateLimitError` returns false on a true 429 | Falls through to existing model fallback (correct behavior — the model fallback handles non-rate-limit errors too) | None needed |
| `ExcludeAndReselect` returns `ReselectHealthy` (fresh weighted-random pick among non-cooling (renormalized), or B2 no-op returning current binding, or empty-`conversationKey` fresh pick) | Caller spawns against the returned credential. Standard healthy-skip path. (Round 3c — C2 unified semantics: was multiple disjoint rows in Round 3b; folded into one row per the C2 narrowing — sub-cases enumerated inline.) | None needed; standard path |
| `ExcludeAndReselect` returns `ReselectSoonestExpiry` (0-of-N healthy — Round 3b 0-of-N pin) | Round 3b — caller enforces single-attempt-then-fall-through via the B3 mode. Caller spawns ONE attempt; if it 429s, cooldown extends, the caller MUST fall through to model-fallback (no second `ExcludeAndReselect` call). **Round 3c — W3:** the caller sets `soonestExpiryAttempted = true` and `continue`s after the soonest-expiry spawn — the NEXT 429 routes straight to model-fallback (no double-spawn of soonest-expiry picks; sequential progression per F.4 single-attempt-then-fall-through). | The terminal "All models rate limited" error (`pkg/proxy/race_coordinator.go:860-868`) remains the client-facing shape |
| `ExcludeAndReselect` returns `ReselectNone` (Round 3c — C2 narrowing: NO credential is available — single-credential model, OR 0 valid credentials; B2 no-op and empty-convKey were here in Round 3b and are now `ReselectHealthy`) | Caller falls through to existing model-fallback | Same as above |
| Caller-side tried-set cap reached (R3-5) | Falls through to existing model fallback. The terminal "All models rate limited" remains. | Same as above |
| Retry budget exhausted mid-spawn (race condition) | The new attempt is spawned; if it 429s, the next iteration hits the cap and falls through | Same as above |
| Pre-first-byte check fails (bytes already sent) | Falls through to existing model fallback — but model fallback is also post-first-byte blocked. The existing `streamResult` / `handleStream` error path takes over. | Client sees the partial stream + final error (existing behavior) |

---

## Invalidation Semantics

> **AMENDED 2026-08-21 (E-2)**: `OnModelChanged` now **filters**
> bindings (preserve survivors; drop orphans), NOT clear-all. The
> original clear-all semantics flushed cache affinity for every active
> conversation on routine weight nudges (e.g., weight 1→2).

### When Does the Engine Forget a Binding?

| Trigger | What the engine does | Caller invariant |
|---------|---------------------|------------------|
| TTL expiry (now > boundAt + ttl, **i.e., > 24h idle — NEW 2026-08-21 #10**) | Drop binding; next `GetOrSelect` picks a fresh credential | Lazy: dropped at next lookup, not proactively. **`boundAt` is REFRESHED on every in-TTL hit** (sliding semantics) — a binding stays alive as long as the conversation is active. |
| Credential deleted (via `OnCredentialDeleted`) | Drop ALL bindings pointing to that credential across all models. **Round 3c — S6:** ALSO clear all cooldown entries for that credential in the cooldown map (`cooldowns[modelID][credentialID]` across all modelIDs where it appears). Hygiene against unbounded map growth in the pathological "many credentials deleted under load" case AND against stale `EngineStats.Cooldowns` gauge counts (Round 3b — amendment 7 gauge semantics: the gauge includes the dead entries until the next janitor sweep or live read). The clear is a single-pass loop across all models that have the credential in their cooldowns map, performed under the same outer Lock + per-model write Lock discipline (E-1) already used by the binding drop. The `EngineStats.Cooldowns` gauge is NOT mutated directly — it is recomputed on the next sweep (or live read via `Stats()`). | Proactive: called by `ModelsManager.RemoveCredential` after the in-use guard |
| Model's credentials changed (via `OnModelChanged`) **(AMENDED — E-2)** | **Filter**: keep bindings whose credential survives in `newCredentials`; drop only orphans (whose credential was removed). | Proactive: called by `ModelsManager.AddModel`/`UpdateModel` after the DB write succeeds |
| Binding's credentialID is no longer in the model's configured list (defensive check at lookup) | Drop binding; pick a fresh credential | Catches missed invalidation events; idempotent |
| `Engine.Stop()` called | Drop nothing — janitor stops but bindings remain | Caller-side lifecycle; safe to restart by calling `RebindFromStore` for every model. **NEW 2026-08-21 — #10**: surviving bindings keep their `boundAt` (no expiry on stop); the next `GetOrSelect` after a Stop/Restart cycle applies the sliding-TTL check against the preserved `boundAt`. |
| Empty conversationKey **(AMENDED — W-2)** | **No binding stored**; fresh weighted pick per request. The ""-as-own-bucket reading is REMOVED. | Caller MUST NOT expect affinity for empty-key requests |

### Defensive Lookup Check

`GetOrSelect` performs a "is the binding's credential still valid for this
model?" check on every call:

```go
// pseudocode inside GetOrSelect
b, ok := state.bindings[convKey]
if ok && b.boundAt.Add(e.ttl).After(now) {
    if credentialStillInConfig(state.credentials, b.credentialID) {
        return b.credentialID, nil  // happy path
    }
    // Stale — drop and fall through to weighted random.
    delete(state.bindings, convKey)
}
// Pick a new credential via weighted random.
```

This means even if every invalidation event is missed (bus disconnected,
panic mid-publish), the engine self-corrects on the next request.

### Race Between Invalidation and Lookup

There is a benign race: a lookup is in flight (read lock held, credential
resolved) while an `OnModelChanged` is queued (write lock pending). The
write lock will wait for the in-flight read to finish; subsequent
lookups see the new state. No correctness issue — only an at-most-one
extra request going to the soon-to-be-deleted credential. Acceptable.

---

## Config-Change Propagation

### Wiring Path

```
   UI POST /fe/api/models
        │
        ▼
   pkg/ui/server.go: handleModelUpdate
        │
        ▼
   ModelsManager.UpdateModel(modelID, model)
        │
        ├─► Validate (provider-match, weight>0, creds exist)
        ├─► DB write (UPDATE models SET credentials_json = ...)
        ├─► Engine.OnModelChanged(modelID, model.Credentials)
        │
        ▼
   Engine (drops bindings for modelID, rebuilds prefix sums)
        │
        └─► Publishes events.Event{Type:"model.credentials.changed",
              Data: {model_id: modelID}} via the bus
                    │
                    ▼
              Other subscribers (none for v1; future-proofed hook)
```

### Why We Invalidate After the DB Write, Not Before

If we invalidate BEFORE the DB write succeeds, a concurrent request
could:

1. Read `old credentials_json` from a cached snapshot → pick credential
   `X` from the old set.
2. Cache binding → returns `X` for a few requests.
3. We commit the new credentials_json.
4. Future lookups see the new state, drop the stale binding, pick from
   the new set.

That's the same outcome as "invalidate after," but with the window of
incorrectness shifted by one step. The "after" pattern is simpler
because the engine's state always reflects what's in the DB (no
transitional window).

### Edge: Failed DB Write

If the DB write fails, we do NOT call `Engine.OnModelChanged`. The
engine keeps its current state. The user retries; if the retry
succeeds, the engine updates. If the user abandons, the engine's state
is unchanged (correct).

### Edge: ModelsManager Constructed Without an Engine

Unit tests for the proxy that don't care about LB pass `credEngine: nil`
to `NewHandler`. The handler MUST tolerate `credEngine == nil` by
falling back to `ResolveInternalConfig` (legacy, single-credential) at
every call site. Implementation: `ResolveInternalConfigWithAffinity`
checks `m.engine == nil` and short-circuits to the legacy path.

---

## Backward Compatibility Matrix

| Scenario | Pre-LB behavior | Post-LB behavior | Status |
|----------|-----------------|-------------------|--------|
| Model with `credential_id = 'X'` (existing) | All requests use credential X | Migration backfills to `Credentials = [{X, 1, 0}]` AND keeps `credential_id = 'X'` as derived shadow (M-1); engine sees single credential → returns X (no map writes, E-3) | **Identical** |
| Model with no credential (external/unknown) | `ResolveInternalConfig` returns `ok=false` | `ResolveInternalConfigWithAffinity` returns `ok=false` (empty `Credentials` → falls back to legacy path) | **Identical** |
| `cfg.UpstreamCredentialID = 'Y'` (env-only race-external) | Race-external uses credential Y | Race-external untouched | **Identical** |
| Ultimate-external request (any model) | Provider probe via `modelCfg.CredentialID`; client auth passes through | Provider probe via `getEffectivePrimaryCredentialID(modelCfg)` (= Credentials[0].CredentialID or ""); client auth passes through | **Identical** (line 287-293 of `pkg/ultimatemodel/handler.go` reads `modelCfg.CredentialID != ""` → becomes `getEffectivePrimaryCredentialID(modelCfg) != ""`) |
| Model with multi-credential set + new conversation | n/a (no feature today) | Engine picks credential via weighted random, pins for conversation; returns `newlyBound = true` (W-1) so caller publishes `model_credential_selected` event | **NEW** |
| Model with multi-credential set + repeat conversation (same first-user-msg) | n/a | Engine reuses pinned credential; returns `newlyBound = false` (W-1) so caller does NOT publish the event | **NEW** |
| Model credentials reweighted via UI | n/a | **AMENDED — E-2**: engine **filters** bindings — keeps conversations whose credential survives, drops only orphans. Cache affinity preserved across weight nudges. | **NEW (intentional, cache-preserving invalidation)** |
| Old binary running against a 028-migrated DB **(AMENDED — M-1)** | (today) reads `credential_id` directly | Shadow is populated; old binary keeps working but loses LB for multi-credential models. No crash. | **Identical (no crash), partial LB loss for multi-credential models** |
| External tooling (backup scripts, third-party SELECTs) querying `models.credential_id` **(AMENDED — M-1)** | (today) reads `credential_id` | Shadow is populated; tooling keeps working during deprecation window. 029+ removes the column with release-note notice. | **Identical until 029** |
| Empty body / no first user message | n/a | **AMENDED — W-2**: engine treats as no-affinity for this request; **fresh weighted pick per request** with **no binding stored**. The ""-as-own-bucket reading is REMOVED. | **NEW (no hotspot)** |
| Credential deleted while model references it | `ErrCredentialInUse` blocks deletion | Same guard updated to scan `Credentials` JSON; engine drops bindings for that credential on success | **Identical behavior, JSON-scanned guard** |
| Empty body / no first user message | n/a | Engine treats as no-affinity for that request; weighted random without persistence | **NEW (degrade gracefully)** |
| Race between `OnModelChanged` and in-flight `GetOrSelect` | n/a | At-most-one extra request to a soon-to-be-removed credential; subsequent requests use new state | **NEW (benign race)** |
| **(Round 3 — R3-7)** Single-credential model — rate-limit hit on the only credential | All requests fail with 429 → existing model fallback / terminal "All models rate limited" error | Same behavior. R3-3's `len(modelCfg.Credentials) > 1` pre-check fails immediately, so the Round 3 tree falls through to existing model fallback. No cooldown map writes (cooldown map is keyed by credential, but the `len > 1` pre-check rejects single-credential models before `ExcludeAndReselect` is called). | **Identical (byte-identical)** |
| **(Round 3 — R3-7)** Race-external env path (`cfg.UpstreamCredentialID`) — rate-limit hit | All requests fail with 429 → terminal 429 error | UNCHANGED per R3-7. Single env credential = no LB domain. `IsRateLimitError` and `ExcludeAndReselect` are not called on this path; existing race-executor error handling applies. | **Identical (byte-identical)** |
| **(Round 3 — R3-7)** Ultimate-external passthrough — rate-limit hit | Upstream returns 429 → existing error propagation | UNCHANGED per R3-7. The provider probe at `pkg/ultimatemodel/handler.go:625-635` (Round 3 cite) only inspects `modelCfg.CredentialID` for provider detection; no credential injection. R3-3 hook is only on ultimate-internal (`executeInternal`). | **Identical (byte-identical)** |
| **(Round 3 — R3-2)** Multi-credential internal model — credential A 429s | All requests use credential A (Round 1 single-credential behavior) | R3-3 tree: pre-check #1 (rate-limit) passes, pre-check #2 (internal + >1 creds) passes, pre-check #3 (no bytes sent) passes, pre-check #4a (budget) passes, pre-check #4b (next non-cooling cred exists) passes → spawn same-model-credential-B attempt. R3-2 `ExcludeAndReselect` sets A's cooldown, rebinds conversation to B. Publishes `model_credential_failover` event. | **NEW (rate-limit failover)** |
| **(Round 3 — R3-4)** All credentials cooling for a model | n/a (no feature today) | R3-4 all-cooling branch: pick SOONEST-EXPIRING credential, log `[LB-FAILOVER] WARN`, single attempt only (no loop). If that attempt 429s again, cooldown extends and the flow falls through to existing model fallback / terminal "All models rate limited" (`pkg/proxy/race_coordinator.go:860-868`). | **NEW (degrade gracefully — never blocks)** |
| **(Round 3 — R3-6)** Credential failover attempt during streaming | n/a | R3-6 pre-first-byte check fails → fall through to existing model fallback. Streaming continues unchanged. Once `streamResult` / `handleStream` / `arc.headersSent` write paths start, errors propagate as-is; mid-stream credential swap is fundamentally unsafe (corrupts the SSE protocol). | **Identical (no mid-stream swap)** |
| **(Round 3 — R3-5)** Retry budget exhausted | n/a | Caller-side tried-set reaches `len(credentials) - 1` → falls through to existing model fallback. The terminal "All models rate limited" remains the client-facing error shape. Bounded latency: worst case `(N-1) × 429-RTT`. | **NEW (bounded retry)** |
| **(Round 3 — R3-8)** Engine stats shape (Hits / Misses / Bindings) | n/a | UNCHANGED. New `Failovers` + `Cooldowns` fields appended at the struct tail (additive, no field rename). The pinned #5 field-name discipline (`Hits`, `Misses`, `Bindings`) is preserved. Existing operator dashboards reading the three base fields continue to work. **Round 3b — amendment 7 (gauge semantics):** `Cooldowns` is a GAUGE (current cooldown-map size, recomputed at each janitor sweep or read live on `Stats()`), NOT a monotonic counter; `Failovers` remains a monotonic counter. | **NEW (additive)** |
| **(Round 3b — leader ruling i)** `/v1/messages` internal — anthropic-passthrough branch (`handler_anthropic.go:297-345`) — rate-limit hit | n/a (this sub-branch existed pre-feature; it read the single-cred `modelConfig.CredentialID` directly and bypassed `doAnthropicInternalRequest`) | Round 3b — the branch is IN SCOPE for R3-3 / R3-6 / R3-7 / F.7. The sibling phase worker swaps the single-cred `modelConfig.CredentialID` + `cred.ResolveAPIKey()` reads to the affinity resolver (same shape as the `doAnthropicInternalRequest` branch). Pre-first-byte guard: recorder's `arc.Code` + `arc.headersSent` per `handler_anthropic.go:648-654` (Round 3b cite fix; NOT `adapter_anthropic.go:170-176`). The R3-3 tree applies uniformly. **Behavior change:** rate-limit failover NOW fires on this branch (was: terminal error propagation). | **NEW (rate-limit failover)** |
| **(Round 3b — B1)** Multi-credential internal model with NO fallback chain (`len(c.models) == 1`) — rate-limit hit | Round 3 (pre-amendment): the spawn-window gate at `race_coordinator.go:338` (`len(c.requests) < len(c.models)`) caps credential failover at ONE attempt and the all-failed check at `:420-421` (`len(c.requests) >= len(c.models)`) fires immediately → terminal "All models rate limited" | Round 3b — the gate EXEMPTS `modelTypeCredFailover` attempts: separate accounting for model-attempts (counted at the window) vs credential-attempts (tracked independently against the per-model credential budget). The user's core 3-credential single-model scenario now gets the FULL `len(creds)−1` failover chain. After credential exhaustion, falls through to the existing model-fallback spawn (`c.models[1]`) when it exists. **The terminal message at `:860-868` MUST NOT fire while an untried fallback model OR an untried credential remains** (amended all-failed condition). | **NEW (rate-limit failover — fixes silent no-op regression)** |

---

## Risks

| # | Risk | Impact | Severity | Mitigation |
|---|------|--------|----------|------------|
| 1 | Migration 028 fails mid-transaction (e.g., DB disk full) | DB left in inconsistent state (new column added, backfill incomplete) | **High** if transaction is not atomic | Wrapped in file-level `BEGIN` / `COMMIT` (the repo's FIRST file-level transactional migration through `modernc.org/sqlite` v1.46.1 + pgx/v5 `ExecContext`); if any step fails, the migration runner (`pkg/store/database/migrate.go:71-97`) marks it as not applied and the next startup retries from scratch. **Mandatory PG-gated test proves the transaction works on both dialects.** |
| 2 | External tool (e.g., backup script) queries `models.credential_id` and breaks after the (future) 029 column drop | Tool returns NULL or errors | Medium **(AMENDED — M-1)** | 028 does NOT drop the column; it stays as a derived shadow. 029+ removal is gated on a release-note deprecation window. The proxy's public API surface (HTTP) is unchanged. |
| 3 | Engine consumes unbounded memory for short conversations | Slow leak in long-running deployments | Medium | Hybrid sliding-idle TTL + janitor (sweep every 5m); default TTL 24h bounds idle conversations. **NEW 2026-08-21 — #10**: the TTL is sliding-on-idle (refreshed on every in-TTL hit), so active conversations do NOT expire mid-flight; the TTL is the IDLE ceiling, not the lifetime ceiling. Test #3 in `decisions.md` §E covers lazy + janitor eviction. |
| 4 | Weighted random skews toward one credential due to small N | User-visible imbalance | Low | Documented tolerance bands in test #1 of `decisions.md` §E (±5% at N=10k); per-credential weight validation rejects zero or negative weights. |
| 5 | `OnModelChanged` event published on bus but no subscriber (engine missing) | Bindings not invalidated → stale credential used | Low | Defensive lookup check (`GetOrSelect` verifies credential is still in config) self-corrects within one request. |
| 6 | `firstUserMessage` extraction returns wrong message (multimodal content) | Conversation key changes mid-conversation → affinity lost | Low **(AMENDED — A-2)** | Multimodal content is now canonical-JSON-hashed (no-affinity fallback removed). Precedent at `hash_cache.go:172-186`. |
| 7 | First user message truncated by client (context overflow) | Conversation key changes → affinity lost | Low | Same as #6 — accepted behavior. Future enhancement: fallback to last user message hash. |
| 8 | Janitor goroutine panics and silently stops | Memory leak (no more eviction) | Medium | **AMENDED — E-4**: engine wraps the sweep in `defer recover()` and logs the panic; continues on the next sweep interval. Mandatory test #11 in `decisions.md` §E forces a panic via injected failure and verifies recovery. |
| 9 | `ResolveInternalConfigWithAffinity` call site accidentally uses `ResolveInternalConfig` (legacy) for a multi-credential model | LB is silently skipped; all requests go to Credentials[0] | Medium | Code review checklist item + linter rule suggestion: any new call to `ResolveInternalConfig` after this feature lands must justify why affinity is not needed. |
| 10 | Credential provider mismatch (different provider in Credentials[1] vs Credentials[0]) silently routes to wrong API | All requests fail at the upstream | Medium | `ModelsManager.Validate()` enforces same-provider invariant across all `Credentials` entries; UI blocks mixed-provider saves. Test #5 of `decisions.md` §E covers the validation. |
| 11 | **Templated-agent-fleet skew (NEW, A-1)** — same-token-same-first-message conversations pin to one credential → weighted LB silently broken for scripted/agent workloads; E2E test cannot detect it | High for affected deployments | High | **A-1 mitigation**: token salt in conversation key. Anonymous requests degrade to unsalted key (acceptable). |
| 12 | **Destructive DROP COLUMN in 028 (REVERSED — M-1)** — lossy rollback for multi-credential models; old-binary-vs-migrated-DB total breakage; OSS tooling/backups break silently | High | High | **M-1 mitigation**: 028 keeps `credential_id` as derived shadow; DROP INDEX + DROP COLUMN deferred to 029+ behind release-note deprecation window. |
| 13 | **Janitor locking self-contradiction (NEW, E-1)** — original contract's "janitor takes outer write lock" stalled ALL reads each sweep | High | High | **E-1 mitigation**: janitor takes outer **RLock** + per-model write locks (one at a time). Reads proceed concurrently. |
| 14 | **`OnModelChanged` clear-all (NEW, E-2)** — fleet-wide cache flush on routine weight nudges | Medium | Medium | **E-2 mitigation**: filter-survivors. Preserves affinity for conversations whose credential still exists. |
| 15 | **`model_credential_selected` only-on-first-binding unimplementable with original signature (NEW, W-1)** — phase3 cannot detect first-binding-within-request | High | High | **W-1 mitigation**: `GetOrSelect` returns `(credentialID, newlyBound bool, err)`; `newlyBound = true` � first binding. |
| 16 | **Empty-key three-way ambiguity (NEW, W-2)** — three docs specified empty `conversationKey` three different ways; ""-as-own-bucket reading creates a silent 24h hotspot | High | High | **W-2 mitigation**: pinned to one reading — empty key ⇔ NO binding stored, fresh pick per request. Phase 2 test asserts it. **NEW 2026-08-21 — C2**: with `newlyBound ⇔ a binding was stored on this call`, empty-key picks return `newlyBound = false` (cleanly observable and aligned with the no-binding-stored invariant). |
| 17 | **Two injection paths (NEW, W-3)** — `Config.CredEngine` field AND 7th constructor param | Medium | Medium | **W-3 mitigation**: constructor-only. `Config.CredEngine` REMOVED from the contract. |
| 18 | **Dual-source debt returns if 029 never lands (NEW, M-1-tracked)** | High | High | **M-1 mitigation**: tracked issue filed at merge time; load-bearing for M-1. |

---

## Scalability

### Growth Assumptions

- **Models**: 10–100 active models per deployment (matches the
  credentials-table cardinality; no evidence of higher numbers in the
  codebase).
- **Credentials**: 5–50 per deployment (UI lists all credentials on
  `/fe/api/credentials`; no per-model credential count expected to
  exceed 16 — that's our validation ceiling).
- **Concurrent active conversations**: 1k–10k per proxy instance (one
  binding each; map size bounded by TTL × request rate).
- **Request rate**: 100 req/s sustained per model; 1k req/s peak across
  all models.

### Current Bottlenecks

| # | Bottleneck | Threshold | File:Line | Impact |
|---|------------|-----------|-----------|--------|
| 1 | Weighted random selector rebuild on every `OnModelChanged` | O(k) per model; k≤16 | `pkg/credentiallb/engine.go` (NEW) | Negligible — k is small |
| 2 | Binding map lookup in `GetOrSelect` | O(1) hash lookup + O(1) per-model RLock acquire | Same | Negligible at 10k conversations |
| 3 | Janitor sweep over all bindings | O(N models × M bindings per model) every 5 min | Same | Sub-ms at 100×1000; scales linearly |
| 4 | `credentials_json` JSON parse on every model read | O(refs) per parse; refs≤16 | `pkg/store/database/store.go:554-614` (`scanModels`) | Negligible; same cost as `fallback_chain_json` parse today |

### Scaling Characteristics

- **Vertical vs horizontal**: vertical (single-process engine per proxy
  instance; no shared state). For multi-instance deployments, each
  instance maintains its own bindings — affinity is per-instance, which
  is acceptable per the "few % misses" constraint. **Future enhancement
  (v2)**: shared Redis-backed bindings for cross-instance stickiness;
  out of scope for v1.
- **Stateless vs stateful**: the engine is stateful (in-memory map), but
  state is recoverable from the DB on restart (rebind per model).
- **Sync vs async**: all engine operations are synchronous (lookup on
  the request hot path). The janitor is the only async component.
- **Scaling cliffs**: the first cliff is when active conversations
  exceed ~50k per instance (map operations stay O(1), but per-request
  GC pressure from map growth becomes measurable). Mitigation: shrink
  TTL or add Redis. The second cliff is when weighted-random selection
  latency exceeds 1µs (k grows past 256, prefix-sum binary search
  regresses to linear scan). Mitigation: cap k at 256 in validation.

### Capacity Planning

- **Per-instance memory**: 50k active conversations × 64-char key +
  32-char credential ID + 24-byte binding header ≈ 8 MB. Plus per-model
  state: 100 models × 256 bytes ≈ 25 KB. **Total: <10 MB** at the upper
  end of assumptions.
- **Janitor CPU**: O(N×M) every 5 min = sub-ms at upper bounds.
  **AMENDED — E-1**: janitor holds outer RLock for the sweep duration;
  reads proceed concurrently; per-model scan takes the model's write
  lock briefly.
- **Per-model RNG (AMENDED — E-4)**: each `modelState` owns a private
  `*rand.Rand` (or `math/rand/v2` source). First-selection cost is
  bounded to a single model's locked region; no cross-model contention.
- **No new disk I/O**: in-memory only (per Decision C, persistence
  deferred).

---

## Technical Debt Affecting This Analysis

| # | Debt Item | Impact on Recommendation | Severity | File:Line |
|---|-----------|--------------------------|----------|-----------|
| 1 | `pkg/config/config.go:518` publishes `config.updated` with the WHOLE config object; `pkg/ultimatemodel/handler.go:155` checks for a `field` key that is NEVER set | The existing ultimate-model reset-on-config-change path may be silently broken. **Tracked issue filed at merge time** (out of feature scope). Our new event has a typed shape that does not depend on that contract. | Medium (pre-existing; tracked) | `pkg/config/config.go:518`; `pkg/ultimatemodel/handler.go:150-160` |
| 2 | No janitor / background-sweep pattern exists in the codebase | We introduce one. Acceptable because the engine's lifecycle is self-contained (`NewEngine` returns; `Stop()` is the explicit destructor — matches `usage.Counter` precedent of goroutines per long-lived subsystem). **AMENDED — E-1**: janitor uses outer RLock + per-model write locks (does not block reads). | Low | n/a |
| 3 | House style has zero join tables; we adopt JSON columns | Aligns with house style; reduces migration complexity. | None | n/a |
| 4 | `ResolveInternalConfig` returns `(provider, apiKey, baseURL, internalModel, ok)` — five return values, awkward in Go | We add a parallel `ResolveInternalConfigWithAffinity` with the same signature for symmetry. Future refactor could consolidate into a struct return — out of scope here. | Low | `pkg/store/database/store.go:1316-1350` |
| 5 | `cmd/main.go:108` `proxy.NewHandler` already takes 6 args; adding a 7th makes the call site noisier | We accept this; alternative (a builder pattern) is a wider refactor. **AMENDED — W-3**: constructor-only injection; `Config.CredEngine` field REMOVED so there is exactly ONE injection path. | Low | `cmd/main.go:108` |
| 6 | **Dual-source-of-truth debt during deprecation window (NEW, M-1)** — `models.credential_id` is a derived shadow until 029 lands; divergence requires an out-of-band writer | Reduced from "perpetual bug source" to "deprecation-window discipline until 029 lands". **029 is a tracked issue.** | Medium (load-bearing for M-1) | n/a |

### Items NOT Affecting This Analysis

- The `loopdetection` package's goroutine lifecycle (independent subsystem).
- The MCP server split (separate feature branch).
- The MiniMax reasoning_details translation (independent feature; the
  `ultimatemodel/handler_internal.go:46` call site is unaffected except
  for the credential resolution, which becomes LB-aware but otherwise
  transparent).

### Recommended Paydown (before / alongside this feature)

1. **(Tracked issue, ~30m)** File the `config.updated` event payload
   mismatch (`config.go:518` vs `handler.go:155`) as a tracked issue at
   merge time. Non-blocking — out of scope for this feature; flag for
   follow-up.
2. **(Mandatory test, M-1-test)** Add a PG-gated 028 transaction test
   that **PROVES** the file-level BEGIN...COMMIT pairs through pgx/v5
   `ExecContext` AND that the modernc.org/sqlite v1.46.1 bundled
   engine version supports the transaction. Without this test, the
   migration is NOT approved.
3. **(Optional, ~30m)** Add a `credentiallb` integration test that
   verifies the engine recovers from a panic in the janitor (force a
   panic via injected failure). Recommended per E-4.

---

## Open Questions

None blocking. For the dispatcher / plan-phase workers to confirm:

1. **Engine lifetime**: should `credentiallb.Engine.Stop()` be called
   explicitly at proxy shutdown, or piggyback on a `context.Context`
   cancel? Current decision: explicit `Stop()` in the proxy's shutdown
   path (matches `usage.Counter` precedent of explicit lifecycle
   management).
2. **Janitor logging verbosity**: INFO at sweep start/end, DEBUG per
   eviction? Current decision: DEBUG per-eviction-count; INFO on sweep
   start/end (so operators can see "janitor swept N bindings" in logs).
3. **(Resolved by A-2)** Multimodal first-user-message handling: now
   hashes canonical JSON of the multimodal content array (A-2
   amendment). Removed from open questions.
4. **(AMENDED — M-1-test)** Mandatory PG-gated 028 transaction test:
   the test framework must support a PostgreSQL test container; if
   the current CI does not, the migration is **NOT approved** until
   one is added. Coordinate with infra/CI.

---

## References

- `pkg/proxy/handler.go:108` — `proxy.NewHandler` constructor (modified)
- `pkg/proxy/handler.go:345-846` — `HandleChatCompletions` race path
- `pkg/proxy/handler_functions.go:23-126` — `initRequestContext` (modified)
- `pkg/proxy/handler_helpers.go:23-108` — `requestContext` (modified)
- `pkg/proxy/handler_helpers.go:113-132` — `requestContext.reset` (modified)
- `pkg/proxy/race_executor.go:137` — race-internal credential resolution
- `pkg/proxy/race_executor.go:279-300` — race-external UpstreamCredentialID (unchanged)
- `pkg/proxy/race_executor.go:763` — `convertToProviderRequest` (unchanged)
- `pkg/proxy/handler_anthropic.go:139` — `/v1/messages` route handler (NEW 2026-08-21 — C1)
- `pkg/proxy/handler_anthropic.go:340` — call into `doAnthropicInternalRequest` (NEW 2026-08-21 — C1)
- `pkg/proxy/handler_anthropic.go:447` — `doAnthropicInternalRequest` function def (NEW 2026-08-21 — C1)
- `pkg/proxy/handler_anthropic.go:461` — `NewInternalHandler` construction (NEW 2026-08-21 — C1)
- `pkg/proxy/handler_anthropic.go:697` — `anthropicRequestContext` struct (gains `conversationKey` field per #16a C1+; NEW 2026-08-21 — C1)
- `pkg/proxy/internal_handler.go:38` — `NewInternalHandler` signature (gains `conversationKey` arg per #16a C1+; NEW 2026-08-21 — C1)
- `pkg/proxy/internal_handler.go:67` — `HandleRequest` signature (gains `conversationKey` arg per #16a C1+; NEW 2026-08-21 — C1)
- `pkg/proxy/internal_handler.go:69` — current 5-tuple `ResolveInternalConfig` seam (replaced by `ResolveInternalConfigWithAffinity` when engine is wired; NEW 2026-08-21 — C1)
- `pkg/ultimatemodel/handler.go:150-160` — `OnConfigChange` precedent
- `pkg/ultimatemodel/handler.go:287-293` — ultimate-external provider probe (modified shim)
- `pkg/ultimatemodel/handler_internal.go:46` — ultimate-internal resolution (modified)
- `pkg/ultimatemodel/hash_cache.go:158-193` — `HashMessages` (NOT reused)
- `pkg/store/database/store.go:554-614` — `scanModels` (modified for Credentials JSON parse)
- `pkg/store/database/store.go:905-952` — `AddModel` (modified)
- `pkg/store/database/store.go:954-1004` — `UpdateModel` (modified)
- `pkg/store/database/store.go:1007-1023` — `RemoveModel` (modified)
- `pkg/store/database/store.go:1026-1125` — `Validate` (extended for provider-match + weight>0)
- `pkg/store/database/store.go:1130-1170` — `GetCredential` (unchanged)
- `pkg/store/database/store.go:1289-1295` — credential-in-use guard (rewritten to scan Credentials JSON)
- `pkg/store/database/store.go:1316-1350` — `ResolveInternalConfig` (legacy, unchanged)
- `pkg/store/database/store.go` — NEW `ResolveInternalConfigWithAffinity`
- `pkg/store/database/migrate.go:24-52` — migration array (add `028`)
- `pkg/store/database/migrate.go:62-67` — `RunMigrations` loop (reads only `.up` names from the array; **NEW 2026-08-21 — #16b, #16c**: `.down.sql` files are NOT auto-run by the runner — they are manual rollback artifacts)
- `pkg/store/database/migrations/sqlite/001_initial.up.sql` — DDL baseline
- `pkg/store/database/migrations/sqlite/023_add_ultimate_model.down.sql` — **NEW 2026-08-21 — #16b precedent**: `.down.sql` sibling that the runner ignores; operator-applied for rollback
- `pkg/store/database/migrations/sqlite/024_convert_allowed_models_to_ids.down.sql` — **NEW 2026-08-21 — #16b precedent**: same shape as 023
- `pkg/store/database/migrations/sqlite/024_convert_allowed_models_to_ids.up.sql`
  — JSON-array data migration precedent
- `pkg/store/database/migrations/sqlite/027_add_exclude_from_ultimate_switching.up.sql`
  — most recent column-add precedent
- NEW `pkg/store/database/migrations/sqlite/028_add_model_credentials.up.sql` (and `.down.sql`) **(AMENDED — M-1, NO DROP COLUMN)** — `.down.sql` is **NEW 2026-08-21 — #16c** a manual rollback artifact (not auto-run)
- NEW `pkg/store/database/migrations/postgres/028_add_model_credentials.up.sql` (and `.down.sql`) **(AMENDED — M-1, NO DROP COLUMN)**
- NEW (DEFERRED, tracked issue) `pkg/store/database/migrations/sqlite/029_drop_credential_id.up.sql`
- NEW (DEFERRED, tracked issue) `pkg/store/database/migrations/postgres/029_drop_credential_id.up.sql`
- `pkg/credentiallb/key.go` — **AMENDED — A-1, A-2**: signature gains `tokenID`; multimodal canonical-JSON hashing
- `pkg/credentiallb/engine.go` — **AMENDED — E-1, E-2, E-3, W-1, W-2, E-4, #3, #5, #6, #10**; the public `EngineStats` struct (NEW 2026-08-21 — #5) exposes `Hits`, `Misses`, `Bindings` per model; the public `Engine.Stats()` returns `map[string]EngineStats`
- `pkg/models/credential.go:131-166` — RWMutex map pattern
- `pkg/models/config.go:80-111` — `ModelConfig` (modified)
- `pkg/usage/counter.go:49-67` — immediate UPSERT precedent
- `pkg/events/bus.go` — pub/sub bus
- `pkg/ui/server.go:380-460` — model POST/PUT handlers (modified payload)
- `pkg/ui/frontend/src/components/config/ModelForm.tsx:709-725` — per-model checkbox precedent (modeled for credentials multi-select)
- `cmd/main.go:108` — `proxy.NewHandler` call site (modified, 7th constructor arg per W-3; no `Config.CredEngine`)
- `.agents/shared/planning/exclude-ultimate-switching/plan.md` — sibling feature plan

---

## Amendment Changelog (2026-08-21 — leader-final)

> **For downstream-worker verification**: every cross-reference to
> `GetOrSelect`, `ComputeConversationKey`, empty-key semantics, janitor
> locking, `OnModelChanged` invalidation, `Config.CredEngine`,
> migration 028/029 SQL, and the PG-gated transaction test MUST be
> pinned to the AMENDED contract below. Grep this section to verify
> coverage.

| # | Ruling | Section(s) updated | One-line summary |
|---|--------|---------------------|------------------|
| **A-1** | Token-salted key | Question; Context Summary; Architecture Diagram; New Patterns; Data Flow; API Contract (`ComputeConversationKey`, `ExtractFirstUserMessage`); Risks | `ComputeConversationKey(modelID, tokenID, firstUserMessage)`. Anonymous degrades to unsalted. Residual within-principal skew accepted (documented). |
| **A-1-wiring** | Post-auth key wiring | New Patterns; Data Flow; API Contract (`requestContext`, `initRequestContext`); Order of Operations in decisions.md | `rc.firstUserMessage` cached in `initRequestContext` (handler.go:353); `rc.conversationKey` computed POST-AUTH (handler.go:401+). `rc.tokenID` is unset at `:353`. |
| **A-2** | Multimodal hashing | Question; Context Summary; API Contract (`ExtractFirstUserMessage`); Decisions §A Secondary Decisions Tied to A | All requests get affinity; canonical JSON of multimodal `[]interface{}` content is hashed (precedent `hash_cache.go:172-186`). |
| **M-1** | Non-destructive migration | Context Summary; API Contract; Migration SQL; Backward Compatibility Matrix; Risks; Technical Debt; Migration Numbering in decisions.md | 028 = ADD + backfill + same-statement derived shadow write to `credential_id`. **NO DROP COLUMN.** DROP INDEX + DROP COLUMN deferred to 029+ behind release-note deprecation window. Down-migration is lossless. |
| **M-1-test** | Mandatory PG-gated 028 transaction test | Decisions §E Migration Tests; Technical Debt Recommended Paydown; Risks #1; Open Questions | This is the repo's FIRST file-level transactional migration (PG 024 uses BEGIN only inside PL/pgSQL). The test MUST prove the transaction commits on both dialects. Without it, 028 is NOT approved. |
| **E-1** | Janitor outer RLock + per-model write locks | Concurrency Model (Locking Discipline + Locking Edge Cases); Risks #13 | Janitor takes outer **RLock** (reads proceed concurrently) and acquires each per-model write lock one at a time. Original "janitor outer write lock" self-contradiction resolved. |
| **E-2** | `OnModelChanged` filter-survivors | API Contract (`OnModelChanged`); Invalidation Semantics; Risks #14 | Preserve bindings whose credential still appears in `newCredentials`; drop only orphans. NOT clear-all. Routine weight nudges preserve cache affinity. |
| **E-3** | Single-credential fast path no map writes | API Contract (`GetOrSelect` invariants); Decisions §E test #7; Risks #8 (cross-ref) | Fast path is byte-identical to today with **NO binding map writes**. Rule in favor of decisions.md §E #7 / success criterion #5 over phase2 Task 4. |
| **W-1** | `GetOrSelect` returns `newlyBound bool` | API Contract (Engine); Public API Naming Convention in decisions.md; Data Flow; Decisions §E test #9 | `GetOrSelect(modelID, conversationKey) (credentialID string, newlyBound bool, err error)`. `newlyBound=true` ⇔ a binding was stored on this call (per the C2 invariant pinning). Enables `model_credential_selected` only-on-first and E-4 stats. |
| **W-2** | Empty-key semantics pinned | API Contract (Engine invariants, `ComputeConversationKey`, `ResolveInternalConfigWithAffinity`); Invalidation Semantics; Empty Conversation Key in decisions.md; Decisions §E test #8 | `conversationKey == ""` ⇔ NO binding stored, fresh weighted pick per request. The ""-as-own-bucket reading is REMOVED. Phase 2 test asserts it. |
| **W-3** | Constructor-only engine injection | API Contract (`pkg/proxy` `Config`); Technical Debt #5; Risks #17 | `Config.CredEngine` REMOVED from the contract. Only the 7th `NewHandler` constructor arg wires the engine. |
| **E-4** (recommended) | Per-model RNG, `Engine.Stats()`, janitor panic-recovery | Concurrency Model (Engine Internals); Risks #8; Decisions §E tests #11, #12; Default Configuration table | `math/rand/v2` or per-model `*rand.Rand`; `Engine.Stats()` returns `map[string]EngineStats` with **pinned field names `Hits`, `Misses`, `Bindings`** (NEW 2026-08-21 — #5); janitor panic-recovery test mandatory. Non-blocking recommendations. |
| **M-1-tracked** | Tracked issue for migration 029 | Migration SQL (sketch 029 files); Risks #18; Decisions §B Migration Numbering; Open Questions | Migration 029 (`DROP INDEX + DROP COLUMN credential_id`) is a tracked issue filed at merge time. Load-bearing for M-1. |
| **W-1-tracked** | Tracked issue for `config.updated` payload mismatch | Risks #1; Technical Debt #1 | Pre-existing dead code in ultimate hash-cache reset path (`config.go:518` vs `handler.go:155`). Filed separately; out of feature scope. |

### Round 2 — Reviewer Punch-List (2026-08-21)

> **REVISED 2026-08-21 (reviewer pass):** The seven items below are
> the contract-pin amendments from the reviewer punch-list. Each row
> ties to a specific section head above that now carries a
> `> **REVISED 2026-08-21 (reviewer pass — …):**` blockquote. Pin
> Amendment Changelog for downstream-worker verification. **Contract
> wins**: the pins below are downstream law — phase2 Task 9, phase3
> Tasks 4 / 6, all mocks, and the operator-facing surfaces MUST mirror
> the shapes pinned here.

| # | Item | Section(s) updated | One-line summary |
|---|------|---------------------|------------------|
| **C1** | `/v1/messages` internal path in scope | Integration table row 3; Data Flow; API Contract `ResolveInternalConfigWithAffinity` block; References wiring-sites list | `/v1/messages` is the PRIMARY client endpoint (Claude Code). Verified code reality: `cmd/main.go:139` (route) → `handler_anthropic.go:340` (call into `doAnthropicInternalRequest`) → `handler_anthropic.go:447` (function def) → `handler_anthropic.go:461` (`NewInternalHandler` construction) → `internal_handler.go:67` (`HandleRequest` signature) → `internal_handler.go:69` (current 5-tuple `ResolveInternalConfig` seam to replace). Thread: `anthropicRequestContext.conversationKey` (new field) → `InternalHandler.HandleRequest` extra arg → `ResolveInternalConfigWithAffinity`. |
| **C2** | Empty-key `newlyBound=false` invariant pinned | API Contract `GetOrSelect` invariants block (NEW 2026-08-21 invariant: `newlyBound ⇔ a binding was stored on this call`); Decisions §E test #8 (already asserts) | `conversationKey == ""` ⇒ fresh weighted pick per call, **NO binding stored**, returns `newlyBound = false`. The caller does NOT publish `model_credential_selected` (the binding-store event). Phase 3 owns a DEBUG log for empty-key fresh-pick observability (cheap, per-request). |
| **#3** | `ResolveInternalConfigWithAffinity` return shape pinned (struct form) | API Contract `ResolveInternalConfigWithAffinity` block; Data Flow; Integration table contract column | **Struct `ResolvedCredential` + trailing `ok bool`** (REVISED 2026-08-21 — leader-ruled struct branch of the 6-tuple/struct ruling; struct carries BOTH `credentialID` AND `newlyBound` — tuple form could not carry both without a 7th element). Field mapping vs. legacy 5-tuple (`pkg/models/config.go:146` / `pkg/models/config.go:600` / `pkg/proxy/internal_handler.go:69`): `ResolvedCredential.Provider/APIKey/BaseURL/InternalModel` ↔ legacy 5-tuple; **NEW** `CredentialID` (for `#16f` cache-affinity observability + `model_credential_selected` event payload); **NEW** `NewlyBound` (for W-1 only-on-first-binding — the struct is the ONLY way `newlyBound` flows from the engine-binding-store side effect through to the proxy/ultimatemodel handlers, since those call sites receive the resolution result through this seam). The trailing `ok bool` is kept for parity with the legacy 5-tuple. Per C2: empty-key picks return `NewlyBound = false` (no binding stored). |
| **#5** | `Engine.Stats()` `Bindings` field name pinned | API Contract `EngineStats` struct definition; `Engine.Stats()` signature | Pinned shape: `Engine.Stats() map[string]EngineStats` where `EngineStats{Hits uint64, Misses uint64, Bindings uint64}`. The per-model binding-count field is named `Bindings` (NO aliases: `binding_total`, `count`, `len(bindings)` are all rejected). Empty-key requests update `Misses` (a fresh pick happened) but do NOT update `Bindings` (no store), which falls out for free from the C2 invariant. |
| **#6** | Stale 2-tuple doc-comment fixed | API Contract `Engine` package doc-comment (line ~429) | `credentialID, err := e.GetOrSelect(...)` → `credentialID, newlyBound, err := e.GetOrSelect(...)` (the **3-tuple** that matches the W-1 amended `GetOrSelect` signature). |
| **#10** | Sliding idle TTL (NOT fixed-lifetime) | API Contract `NewEngine` doc (`ttl` is now sliding-idle); `GetOrSelect` invariants block; Data Flow (multi-credential, follow-up); Invalidation `When does the engine forget?` table; `Engine.Stop()` row; Risks #3 | `boundAt` is REFRESHED on every in-TTL hit. The binding is eligible for expiry only after `now - boundAt > ttl` (24h of consecutive idle). Janitor sweeps idle-expired bindings; lazy expiry checks idle time on lookup. E-2 filter-survivors preserves `boundAt` on surviving bindings. The 24h ceiling is an /idle/ ceiling, not a /lifetime/ ceiling. |
| **#16a** | Naming convention (handler field vs main.go local) | Integration table row 7; cross-ref to decisions.md §C convention block | `Handler.credEngine *credentiallb.Engine` (unexported field, `pkg/proxy/handler.go`); `credLB := credentiallb.NewEngine(...)` (local in `cmd/main.go`). Convention stated once in decisions.md §C, applies throughout. |
| **#16b** | 023/024 cited as the precedent for `.down.sql` files existing | References (migration files section) | Precedent files: `pkg/store/database/migrations/sqlite/023_add_ultimate_model.down.sql` and `pkg/store/database/migrations/sqlite/024_convert_allowed_models_to_ids.down.sql` both exist on disk for operator rollback reference. |
| **#16c** | `.down.sql` files are NOT auto-run by the runner | References (`pkg/store/database/migrate.go:62-67`); Down-migration blockquote | Verified by `migrate.go:24-52` (the `migrations` array lists only `.up` names) and `migrate.go:62-67` (the `RunMigrations` loop iterates `migrations` only). `.down.sql` files are manual rollback artifacts — operator-applied via `psql`/`sqlite3`, not runner-driven. |
| **#16d** | 16-cap rationale — bounded memory + sanity guard, tunable | Cross-ref to decisions.md §C Default Configuration table | Original rationale ("matches house ceiling on `fallback_chain_json` length") is WRONG — the actual house ceiling is 1 fallback (`pkg/models/config.go:64-66` deprecated; `pkg/store/database/store.go:1047`). The 16-cap is a fresh sanity guard for this feature, tunable. |
| **#16f** | Credential-scoped upstream prompt-cache note | Cross-ref to decisions.md §A "Problem" blockquote | Upstream provider prompt caches are keyed per-credential. Conversation-sticky affinity is required to keep caches hot — weighted-without-affinity silently wastes provider cache budget on every conversation that crosses credential boundaries. |

### Round 3 — Rate-Limit Failover (2026-08-25)

> **AMENDED 2026-08-25 (Round 3 — Rate-Limit Failover):** Eight
> pre-pinned dispatcher rulings (R3-1..R3-8) applied as a SURGICAL
> AMENDMENT to the existing contract. Existing rulings (A-1, A-2, M-1,
> C1, C2, W-1..W-3, E-1..E-4, #3, #5, #6, #10, #16a-d, #16f) are
> UNCHANGED. Phase-1 schema targets (`pkg/models/`, `pkg/store/`,
> `migrations/`) are UNTOUCHED. `race_executor.go` source is
> UNTOUCHED. A sibling worker amends the phase files against the same
> rulings; the **contract wins** rule (per the plan header) governs.

| # | Ruling | Section(s) added | One-line summary |
|---|--------|---------------------|------------------|
| **R3-1** | Rate-limit classifier + retry-after extraction | New `### pkg/providers — error-classification additions (Round 3)` block | `IsRateLimitError(err error) bool` + `ProviderError.RetryAfter time.Duration` added to `pkg/providers`. Wires the existing-but-dead `parseRetryAfter` (`pkg/providers/openai.go:592-609`) inside `handleError` (`pkg/providers/openai.go:234-279`). Classifier reads the already-unmarshaled `{"error":{message,type,code}}` body — no new provider integration required. |
| **R3-2** | Affinity break + `ExcludeAndReselect` | New `### pkg/credentiallb — engine additions (Round 3)` block | Engine gains `ExcludeAndReselect(modelID, conversationKey, excludedCredID, retryAfter) (credentialID, mode ReselectMode)` (**Round 3b — SUPERSEDES Round-3 `(credentialID, ok)` signature, B3 mode-aware**; `ReselectHealthy`/`ReselectSoonestExpiry`/`ReselectNone` enum shape; leader accepted the B3-recommended single-call enum over the two-call `ok bool`+`PickSoonestExpiring` alternative). Marks `excludedCredID` cooling (cooldown seeded from `retryAfter`, else 60s default), REBINDS the existing `(modelID, conversationKey)` entry to next healthy credential in weighted order, skipping cooling credentials. Single map write under existing locking discipline. **Round 3b — B2 idempotent precondition:** rebind only if `bindings[convKey].credentialID == excludedCredID`; otherwise no-op returning the current unchanged binding (mode=ReselectNone). Per-conversation retry accounting (tried-this-request set) lives in the **request context** (not the engine). |
| **R3-3** | Precedence ordering — credential failover BEFORE model fallback | New `## Rate-Limit Failover Precedence Tree` section + integration table cross-ref | Interception point: `pkg/proxy/race_coordinator.go` `manage()` (function def `:252`) "Case 1" (current lines 350-364). Decision tree: if failed model internal AND >1 creds AND error classifies rate-limit AND no CONTENT flushes pre-winner (race: `c.winner == nil`; ultimate: initial-call `*ProviderError` type assertion; /v1/messages: `arc.headersSent` per `handler_anthropic.go:648-654`) AND remaining non-cooling creds exist AND budget not exhausted → spawn same-model-next-credential attempt (`modelTypeCredFailover` / `triggerRateLimit`); else fall through to existing model fallback. Ultimate-internal (`pkg/ultimatemodel/handler_internal.go:36-46`), `/v1/messages` internal (`pkg/proxy/internal_handler.go:109-111`), and the `/v1/messages` anthropic-passthrough branch (`handler_anthropic.go:297-345`, Round 3b leader ruling i — in scope) get equivalent hooks. **Round 3b:** B1 gate exemption for `modelTypeCredFailover` attempts (separate spawn-window accounting vs model attempts); Case-2 uniform classification (leader ruling iii); Case-1 latest-only inspection (accepted v1 behavior); `modelTypeSecond` re-key (specified); canonical naming `modelTypeCredFailover`. |
| **R3-4** | Per-credential cooldown state + soonest-expiry all-cooling fallback | New "Round-3 Cooldown Map Locking" sub-section in Concurrency Model | In-memory `cooldowns map[modelID]map[credID]time.Time` guarded by engine's existing outer+per-model locking discipline (E-1). Janitor sweeps expired cooldowns alongside idle-TTL bindings in same pass. Weighted selection + `ExcludeAndReselect` skip cooling credentials. **All-cooling fallback** (Round 3b — 0-of-N ONLY pin): pick SOONEST-EXPIRING credential (availability beats strict cooldown), log WARN, **single attempt only (no loop)** — caller enforces via B3 mode-aware `ReselectSoonestExpiry` (engine emits WARN; caller does single-attempt-then-fall-through, no second `ExcludeAndReselect` call). 1-of-N healthy takes the normal healthy-skip path; F.4 prose is clarified. `pkg/proxy/race_coordinator.go:860-868` "All models rate limited" remains terminal. **Round 3b — amendment 7 (gauge semantics):** `EngineStats.Cooldowns` is a GAUGE (current cooldown-map size, recomputed at each janitor sweep or read live on `Stats()`), NOT a monotonic counter; the Round-3 increment-per-removal prose at `decisions.md:1291-1293` is REPLACED. `Failovers` remains a monotonic counter. |
| **R3-5** | Retry budget | Rate-Limit Failover Precedence Tree §Caller-Side Tried-Set | Each remaining credential tried AT MOST ONCE per request. Total credential-failover attempts bounded by `len(credentials) - 1`. Caller-side (request context / `raceCoordinator` map) tried-set enforces the cap. Worst-case added latency: `(N-1) × 429-RTT` (429s typically fast, no body wait). |
| **R3-6** | Streaming constraint | Rate-Limit Failover Precedence Tree §Pre-First-Byte Guard | Failover applies ONLY before first CONTENT byte sent to client (Round 3b — invented SSE framing bytes exempted: `handler.go:783-797` writes headers + `": connected\n\n"` comment BEFORE `coordinator.Start()`, but these carry no model content). Race path: `c.winner == nil` (Round 3b — invented `chunksFlushedToClient`/`bytesFlushedToClient` PURGED; `status != statusStreaming` vacuous and PURGED). Ultimate-internal: initial-call `*ProviderError` type assertion (Round 3b — `headersSent` UNUSABLE because `ultimatemodel/handler.go:608-609` pre-sets it before `executeInternal`). `/v1/messages`: recorder/`arc.headersSent` per `handler_anthropic.go:648-654` (Round 3b — cite fixed from `adapter_anthropic.go:170-176` which is `SetStreamHeaders`). Three path-specific implementations documented in the precedence tree section. **Round 3b — leader ruling i:** the `/v1/messages` anthropic-passthrough branch (`handler_anthropic.go:297-345`) is in scope and uses the same `arc.headersSent` guard. |
| **R3-7** | Path scope | Backward Compatibility Matrix — four new rows (Round 3b — leader ruling i) | **Four** LB'd internal paths participate (Round 3b — leader ruling i added the anthropic-passthrough fourth row): race-internal (`executeInternalRequest`), ultimate-internal (`handler_internal.go executeInternal`), `/v1/messages` internal (`internal_handler.go HandleRequest`), and `/v1/messages` anthropic-passthrough branch (`handler_anthropic.go:297-345` — internal model + anthropic-provider credential → `doAnthropicRequest`, bypasses `doAnthropicInternalRequest`; MUST be swapped from single-cred `modelConfig.CredentialID` to the affinity resolver). **UNCHANGED**: race-external env path (`cfg.UpstreamCredentialID`, `race_executor.go:279-300`), ultimate-external passthrough. Single env credential = no LB domain. The four UNCHANGED rows in the Backward Compatibility Matrix prove byte-identity for these paths. |
| **R3-8** | Observability | Engine additions block — event payload + `EngineStats` extension + log prefix | New event `model_credential_failover` {model_id, from_credential_id, to_credential_id, reason:"rate_limit", retry_after_ms, cooldown_ms, attempt_index} published via existing `events.Bus`. `EngineStats` EXTENDED additively with `Failovers uint64` (monotonic counter — total successful `ExcludeAndReselect`) + `Cooldowns uint64` (**Round 3b — amendment 7 GAUGE**, not a counter; current cooldown-map size recomputed at each janitor sweep; the Round-3 increment-per-removal prose at `decisions.md:1291-1293` is REPLACED). Pinned #5 field names `Hits`, `Misses`, `Bindings` unchanged. New log prefix `[LB-FAILOVER]`. |
| **R3-drift** | Base-drift re-verification | (no edits to this contract; cross-ref decisions.md Round 3 Base-Drift Re-Verification) | Eleven commits landed between fea5874 and 8f67bdf. Drift table covers 19 sites (most exact, several line-drifts, one MAJOR drift +337 on `pkg/ultimatemodel/handler.go:288-289 → 625-635`). **Phase-1 schema targets untouched**. `race_executor.go` source untouched. New code since fea5874 (`SetThinkingSink`, bare-JSON capture fallback, D3 probe relocation, `extractNonStreamContent`, SSE no-transform) does NOT conflict with R3-1..R3-8. |

**Contract wins (Round 3 cross-reference matrix — for sibling worker verification).**

| Worker concern | Round 3 ruling | Decisions.md section | technical-analysis.md section |
|---|---|---|---|
| Provider error type extension | R3-1 | §F.1 | `### pkg/providers — error-classification additions (Round 3)` |
| Engine API surface | R3-2 | §F.2 | `### pkg/credentiallb — engine additions (Round 3)` |
| Precedence tree wiring | R3-3 | §F.3 | `## Rate-Limit Failover Precedence Tree` |
| Cooldown map + locking | R3-4 | §F.4 | `## Concurrency Model` — "Round-3 Cooldown Map Locking" sub-section |
| Retry budget invariant | R3-5 | §F.5 | "Rate-Limit Failover Precedence Tree" §Caller-Side Tried-Set |
| Pre-first-byte guard | R3-6 | §F.6 | "Rate-Limit Failover Precedence Tree" §Pre-First-Byte Guard |
| Path scope | R3-7 | §F.7 | Backward Compatibility Matrix — three "UNCHANGED" rows |
| Event/stats/log schema | R3-8 | §F.8 | `### pkg/credentiallb — engine additions (Round 3)` |
| Drift verification | R3-drift | "Round 3 Base-Drift Re-Verification" | (no edits here; decisions.md owns the table) |

**Cross-document invariants preserved (Round 3 does not relax any).**

- **#10 (sliding idle TTL, NOT fixed-lifetime)**: A cooldown does NOT touch `boundAt` (cooldown is a separate map, document in the Sliding-TTL interplay table under `### pkg/credentiallb — engine additions (Round 3)`). A rebind via `ExcludeAndReselect` DOES refresh `boundAt` for the new credential (same as a fresh `GetOrSelect` would). Pinned explicitly in the engine additions block and the Round-3 Cooldown Map Locking sub-section.
- **#5 (`EngineStats` field names locked)**: New `Failovers` + `Cooldowns` fields are appended at the struct tail; `Hits`, `Misses`, `Bindings` unchanged. Existing operator dashboards reading the three base fields continue to work. New field names follow the same locked discipline.
- **C1 (`/v1/messages` internal path in scope)**: Round 3 R3-3 + R3-7 confirms the path participates; the Round 1 conversation-key wiring (`anthropicRequestContext.conversationKey` → `InternalHandler.HandleRequest` extra arg → `ResolveInternalConfigWithAffinity`) is the entry point that the Round 3 `ExcludeAndReselect` is called from.
- **W-2 (empty-key ⇔ NO binding stored)**: Empty-key requests cannot trigger `ExcludeAndReselect` because they have no binding to rebind. The request context's tried-set is also empty-key-unaware (no affinity = no per-conversation retry accounting needed). Empty-key 429s fall through directly to model fallback (R3-3's else branch). The `ExcludeAndReselect` contract pins `ok=false` on empty conversationKey explicitly.
- **E-1 (janitor outer RLock + per-model write locks)**: The Round 3 cooldown sweep uses the SAME locking discipline as the existing binding sweep. The Round-3 Cooldown Map Locking sub-section proves: outer RLock preserved, per-model write Lock one at a time, no deadlock. Janitor sweep order: cooldowns first (cheaper), then bindings (sliding-TTL check). No new lock primitives introduced.
---

### Round 3b — Architect Review Amendments (2026-08-25)

> **AMENDED 2026-08-25 (Round 3b — Architect Stress-Test):**
> Round 3 was architect stress-tested against HEAD @ 8f67bdf; the
> review (`architecture-review-round3.md`) returned
> **AMEND-AND-ADOPT** with eight consolidated amendments + three
> leader rulings. **R3-1..R3-8 SEMANTICS STAND** — these are
> specification-level fixes, not re-decisions. **This contract**
> (technical-analysis.md) was amended in place alongside the
> decisions.md Round 3b changelog (sibling contract). The
> cross-reference matrix below documents the Round 3b changes here
> for downstream-worker verification.

### Round 3b amendment→section mapping (this file)

| # | Source | Title | Section amended in this file |
|---|--------|-------|------------------------------|
| **1 (B1)** | Review blocking finding | **Spawn-window gate exemption for `modelTypeCredFailover`** | Rate-Limit Failover Precedence Tree (B1 gate exemption paragraph + Decision Tree update) + Backward Compatibility Matrix (gate-exemption row) |
| **2 (B2)** | Review blocking finding | **`ExcludeAndReselect` idempotent precondition** | engine-additions `ExcludeAndReselect` invariants block (B2 paragraph) |
| **3 (B3)** | Review blocking finding | **Mode-aware `ExcludeAndReselect` return** (single-call enum shape, leader ruling ii) | engine-additions `ExcludeAndReselect` (B3 supersession + `ReselectMode` enum) + precedence tree (mode-based fall-through pseudocode) + Failure Modes table |
| **4** | Review amendment | **Real streaming guards** (purge invented state) | Precedence Tree §Pre-First-Byte Guard table (real guards) + decision tree (Pre-check #2 updated) + pseudocode (c.winner == nil) |
| **5** | Review amendment + **leader ruling i** | **Anthropic-passthrough internal branch in scope** | Three-Path Coverage (fourth path) + Pre-First-Byte Guard table (fourth row) + Backward Compatibility Matrix (passthrough-branch row) |
| **6** | Review amendment | **Classifier vocabulary table + 503 row + absent-`Retry-After` flow + HTTP-200 out-of-scope** | `IsRateLimitError` doc comment (Vocabulary table + Match semantics + Retry-After absent flow + Out-of-scope note) + Unit-test matrix (503 row added) |
| **7** | Review amendment | **`EngineStats.Cooldowns` gauge semantics** | engine additions block (engineState + EngineStats — gauge comments) + Janitor interaction (REPLACE increment-per-removal prose) + Unit-test coverage (Round 3b — gauge semantics noted) + Backward Compatibility Matrix (R3-8 row updated) |
| **8** | Review amendment + leader ruling | **E2E premise correction** (no `--hit-count-file` precedent) | (no edits at this contract level — sibling phase worker owns phase5 Task 23 wiring) |
| **Leader ruling ii** | Leader ruling | **Single-call enum shape for `ExcludeAndReselect` (B3)** | engine-additions `ExcludeAndReselect` (B3 paragraph, one-line rationale pinned) |
| **Leader ruling iii** | Leader ruling | **Case-2 uniform classification** | Precedence Tree §Caller-Side Tried-Set (Case-2 paragraph + audit trail reference) + Decision Tree (Pre-check #1 covers Case-2 path uniformly) |
| **Symbol fixes** | Review §1 citation nits | **`monitorLoop` → `manage()` (`:252`), `spawnAttempt` → `spawn()` (`:189`); canonical Go identifier `modelTypeCredFailover`** (purge `modelTypeCredentialFailover`/`credential_failover` from prose where they name the Go constant) | Precedence Tree §Three-Path Coverage (monitorLoop → manage()) + Pseudocode (monitorLoop → manage()) + Round 3 R3-2 / R3-3 / R3-6 / R3-7 / R3-8 changelog entries |
| **Pre-first-byte guard operator docs** | Review §1 sub-finding | **Case-1 latest-only inspection (accepted v1 behavior), `modelTypeSecond` re-key (specified behavior)** | Precedence Tree §Caller-Side Tried-Set (both pins — `latestReq := c.requests[len(c.requests)-1]` accepted; `modelTypeSecond` re-key to primary specified) |
| **Operator note** | Review §3 pin | **1-of-N healthy load concentration + gradual re-distribution (no switch-back thundering)** | (operator-doc only — decisions.md §F.3 carries it; this file references it via cross-doc invariants) |
| **Tried-set single-goroutine invariant** | Review §2 pin | **`triedSet` / `failoverAttempts` mutated ONLY from `manage()` loop** | Round-3b — Tried-Set Single-Goroutine Mutation Invariant (new sub-section in Concurrency Model with `requestContext` field declarations + reset list) |

### Cross-reference matrix (Round 3b — for sibling worker verification)

| Worker concern | Round 3b amendment | technical-analysis.md section | decisions.md section |
|---|---|---|---|
| Spawn-window gate exemption | B1 | Rate-Limit Failover Precedence Tree (gate exemption) + Backward Compatibility Matrix (gate-exemption row) | §F.3 (B1 gate exemption) |
| `ExcludeAndReselect` precondition | B2 | engine-additions ExcludeAndReselect (B2 invariants) | §F.2 (B2 precondition) |
| Mode-aware return | B3 (leader ruling ii) | engine-additions ExcludeAndReselect (B3 supersession + ReselectMode) + Precedence Tree (mode-based fall-through) + Failure Modes table | §F.2 (B3 signature) + §F.4 (mode semantics) |
| Real streaming guards | 4 | Precedence Tree §Pre-First-Byte Guard (real guards) + Decision Tree (Pre-check #2) + Pseudocode (c.winner == nil) | §F.6 (real guards) |
| Passthrough branch in scope | 5 + leader ruling i | Three-Path Coverage (fourth path) + Pre-First-Byte Guard (fourth row) + Backward Compatibility Matrix (passthrough row) | §F.7 (fourth row) + §F.3 (third hook bullet) |
| Classifier vocabulary table + 503 row + out-of-scope | 6 | IsRateLimitError (Vocabulary table + Match semantics + Retry-After absent flow + Out-of-scope) + Unit-test matrix (503 row) | (decision-level §F.1 unchanged — pinned here) |
| Gauge semantics | 7 | engine additions block (engineState + EngineStats gauge comments) + Janitor interaction (REPLACE increment-per-removal) | §F.8 (EngineStats extension) + §F.4 (Janitor interaction REPLACE) |
| Case-2 uniform classification | leader ruling iii | Precedence Tree §Caller-Side Tried-Set (Case-2 paragraph) | §F.3 (Case-2 paragraph + audit trail) |
| Case-1 latest-only / modelTypeSecond re-key | sub-finding pins | Precedence Tree pseudocode + §Caller-Side Tried-Set (both pins) | §F.3 (both pins) |
| Tried-set single-goroutine mutation invariant | sub-finding pin | Round-3b — Tried-Set Single-Goroutine Mutation Invariant (new sub-section in Concurrency Model) | (decision-level §F.2 + §F.3 cross-ref; this file is the authoritative contract for the invariant) |
| Operator docs (1-of-N load + gradual re-distribution) | sub-finding pin | (operator-doc only — decisions.md §F.3 carries it) | §F.3 operator note |

### Cross-document invariants preserved (Round 3b does NOT relax any)

- **#10 (sliding idle TTL, NOT fixed-lifetime)**: A cooldown does NOT touch `boundAt` (cooldown is a separate map, document in the Sliding-TTL interplay table under `### pkg/credentiallb — engine additions (Round 3)`). A rebind via `ExcludeAndReselect` DOES refresh `boundAt` for the new credential (same as a fresh `GetOrSelect` would). Pinned explicitly in the engine additions block and the Round-3 Cooldown Map Locking sub-section. Round 3b does NOT change this.
- **#5 (`EngineStats` field names locked)**: New `Failovers` + `Cooldowns` fields are appended at the struct tail; `Hits`, `Misses`, `Bindings` unchanged. Round 3b only changes `Cooldowns` semantics (counter → gauge), NOT its field name or position. Add-only rule preserved.
- **C1 (`/v1/messages` internal path in scope)**: Round 3 + Round 3b confirm the path participates. Round 3b (leader ruling i) extends coverage to the anthropic-passthrough sub-branch — same family, same LB coverage.
- **W-2 (empty-key ⇔ NO binding stored)**: Empty-key requests cannot trigger `ExcludeAndReselect` because they have no binding to rebind. With B3 mode-aware return, empty-key returns `ReselectNone` (caller falls through to model-fallback); Round 3b pins this explicitly in the engine-additions `ExcludeAndReselect` invariants block (replaces Round-3 "REJECTED with ok=false" wording).
- **E-1 (janitor outer RLock + per-model write locks)**: The Round 3 cooldown sweep uses the SAME locking discipline as the existing binding sweep. Round 3b only changes the `Cooldowns` field semantics (counter → gauge recomputation), NOT the lock ordering. Pinned in the Round-3 Cooldown Map Locking sub-section + the Janitor interaction paragraph.
- **R3-1..R3-8 SEMANTICS STAND**: every ruling's behavior is unchanged; the eight amendments + three leader rulings are specification-level fixes (gate exemption, idempotent precondition, mode-aware return, real guards, path-scope row, classifier vocabulary, gauge semantics, E2E premise correction, single-call enum shape, Case-2 uniform classification).
- **Round 3b — New invariant: tried-set single-goroutine mutation** — `requestContext.triedSet` and `requestContext.failoverAttempts` are mutated ONLY from the `manage()` loop in `race_coordinator.go` (function def `:252`), serialized by the coordinator mutex. The Phase 5 tests MUST assert the invariant under `go test -race`. This is the only NEW invariant introduced by Round 3b; all other invariants are preserved.

## Round 3c — Reviewer Findings (2026-08-25)

> **AMENDED 2026-08-25 (Round 3c — Reviewer Findings):** A reviewer
> pass against Round 3b returned APPROVED-WITH-NOTES gated on one
> mandatory revision: 3 critical pins (C1, C2, C3), 4 warnings
> (W1, W2, W3, S6 — leader ruled S6 YES), 3 suggestions (S1,
> S2, S3) — all pin-level. The leader pre-ruled every open
> decision point (folded below). Amendments applied in place per
> section; this changelog documents the mapping for sibling-worker
> verification. The contract wins rule (per the plan header)
> governs — sibling phase workers amend against the SAME rulings.
> Cross-ref: decisions.md Round 3c changelog (sibling contract).
> The contract wins rule (per the plan header) governs.

### Item → Section Mapping (this file)

| # | Source | Title | Sections amended in this file | Sections amended in decisions.md |
|---|--------|-------|---------------------------------|------------------------------------|
| **C1** | Reviewer critical | **Hoisted credFailover pre-checks (C1) — admission condition** | "Rate-Limit Failover Precedence Tree" §Pseudocode (Race-internal) — hoisted form block + admission gate expression `gate := modelAttempts < len(models) \|\| credFailoverEligibleWithBudget()`; §B1 gate exemption paragraph (REPLACED with C1 hoisted form) | §F.3 (decision tree REPLACED with hoisted form + C1 semantic-equivalence note) |
| **C2** | Reviewer critical | **`ReselectMode` semantics unified to phase2's reading (C2)** — FOUR-SURFACE SYNC | engine-additions `ReselectMode` enum comments; engine-additions `ExcludeAndReselect` doc-comment (return-bullets + invariants block); Precedence Tree §Pseudocode (mode handling); Failure Modes table — all four surfaces agree on the unified mapping | §F.2 (enum comments + return-bullets REPLACED with C2 mapping); §F.4 (B3 mode-aware bullets updated for C2 narrowing) |
| **C3** | Reviewer critical | **Three nonexistent references (C3)** | Precedence Tree §Pseudocode (C3(1) `spawnTriggerInfo.credentialID` + C3(2) `case modelTypeCredFailover` inline comments; C3(3) constructor wiring at top of pseudocode block) | §F.3 (decision tree 1-2: C3(1) + C3(2) inline); new "Race coordinator constructor wiring (Round 3c — C3(3))" sub-section |
| **W1** | Reviewer warning | **Passthrough classification source (W1)** | Three-Path Coverage row 4 (W1 classification contract appended); Precedence Tree §Pre-First-Byte Guard (passthrough row cross-ref W1) | §F.6 (AMENDED blockquote adds W1 passthrough classification contract); §F.7 path-scope table row 4 |
| **W2** | Reviewer warning | **`ProviderError` extension (W2)** | `pkg/providers — error-classification additions` (ProviderError struct + IsRateLimitError comment updated for ErrorType + ErrorCode fields); vocabulary table cross-ref | §F.1 (ProviderError struct extended additively; Round 3c — W2 wiring paragraph) |
| **W3** | Reviewer warning | **SoonestExpiry single-shot pseudocode (W3)** | Precedence Tree §Pseudocode (soonestExpiryAttempted flag set + continue after ReselectSoonestExpiry spawn); Failure Modes table (W3 cross-ref in the SoonestExpiry row) | §F.4 (Round 3c — W3 paragraph under Latency note); §F.4 mode-aware bullets (W3 cross-ref) |
| **S1** | Suggestion | **`rate_limit_exceeded` literal in code-equality set (S1)** | `pkg/providers` vocabulary table row added (rate_limit_exceeded / equality); Match semantics paragraph extended (code-equality set is `{rate_limit, rate_limit_error, rate_limit_exceeded}`); Unit-test matrix rows 12 + 13 added | §F.1 (Classifier vocabulary paragraph cross-refs S1) |
| **S2** | Suggestion | **Tried-set home in requestContext, not engine (S2)** | engine-additions ReselectMode invariants block (S2 pin added); Round-3b Tried-Set Single-Goroutine Mutation Invariant cross-ref | §F.2 (one sentence added to ExcludeAndReselect doc-comment) |
| **S3** | Suggestion | **Wording standardization (S3)** — "weighted random among non-cooling (renormalized)" | engine-additions weightedSelector pseudocode (S3 wording inserted with audit-trail-untouched note); §Concurrency Model cross-ref (the F.4 Selection skip rule standardization lives in decisions.md) | §F.4 (Selection skip rule paragraph REPLACED with S3 standardized wording) |
| **S6** | Suggestion (leader ruled YES) | **`OnCredentialDeleted` clears cooldowns (S6)** | Invalidation Semantics table (Credential-deleted row extended with S6 cooldown clear); engine additions invariants block (S6 bullet) | §F.4 (new AMENDED blockquote: S6 cooldown hygiene); §F.8 cross-ref |

### C2 Four-Surface Consistency Statement

After Round 3c, all four surfaces of the `ReselectMode` semantics
agree on the unified mapping:

1. **Engine-additions enum comments** (the canonical source):
   - `ReselectHealthy` — a credential is available and the caller MUST
     proceed with credential failover. Sub-cases: (a) weighted-random
     among non-cooling (renormalized) fresh pick, (b) B2 no-op
     returning `(currentBinding.credentialID, ReselectHealthy)`,
     (c) empty-`conversationKey` fresh non-cooling weighted-random
     pick, NO map write.
   - `ReselectSoonestExpiry` — ALL credentials are cooling (0-of-N);
     engine picks soonest-expiring; caller MUST treat as a single
     attempt only.
   - `ReselectNone` — NO credential is available. Either (a) the
     model has a single credential, or (b) zero credentials are
     valid. Reserved for "no credential exists / no credential
     valid"; B2 no-op and empty-`conversationKey` are NOT here.

2. **Engine-additions return-bullets** (in the ExcludeAndReselect
   doc-comment): same mapping, restated as the three return
   bullets with `mode == ReselectHealthy` = proceed with failover
   (fresh pick OR B2 no-op OR empty-key fresh pick), `mode ==
   ReselectSoonestExpiry` = single attempt, `mode == ReselectNone`
   = NO credential is available (single-credential model or 0
   valid credentials).

3. **Precedence Tree Pseudocode** (race-internal, the canonical
   site): the `if mode == credentiallb.ReselectHealthy { ... }`
   branch handles fresh weighted pick, B2 no-op, and empty-key
   fresh pick uniformly — caller spawns against `credentialID`,
   publishes event, adds to tried-set, and `continue`s. The
   `if mode == credentiallb.ReselectSoonestExpiry { ... }` branch
   spawns ONE attempt, sets `soonestExpiryAttempted = true`, and
   `continue`s (no double-spawn; next 429 routes to model-fallback
   per W3). The `ReselectNone` comment in the pseudocode reads
   "NO credential is available — either single-credential model
   OR 0 valid credentials" — matching the enum.

4. **Failure Modes table** (the operational reference): the
   `ReselectHealthy` row lists all three sub-cases; the
   `ReselectSoonestExpiry` row references W3 single-shot guard;
   the `ReselectNone` row is narrowed to single-credential model
   OR 0 valid credentials.

**Rationale (the non-obvious narrowing):** the prior Round 3b
reading that returned `ReselectNone` for both B2 no-op AND
empty-`conversationKey` was REJECTED because it would force the
caller to model-switch while a healthy credential exists,
violating R5 (Round-1 reviewer-5 — "do not model-switch when a
healthy credential is available"). The narrow `ReselectNone` to
"no credential exists / no credential valid" restores the R5
invariant.

### C1 Semantic-Equivalence Note

The hoisted admission form
`gate := modelAttempts < len(models) ||
credFailoverEligibleWithBudget()` is SEMANTICALLY EQUIVALENT to
the rejected cap-extension alternative (`cap += len(credentials)−1`).
Both admit `len(credentials) − 1` additional attempts before the
terminal. Cap-extension was REJECTED purely as an IMPLEMENTATION
(mutation of len-dependent invariants elsewhere — the existing
`race_coordinator.go:338` / `:420-421` reads of `len(c.models)`
would need synchronized widening across multiple sites); the
hoisted form is the same semantic admission expressed via a
separate accounting + OR-clause, which keeps the existing `:338`
/ `:420-421` invariants intact.

### Cross-document invariants preserved (Round 3c does NOT relax any)

- **#10 (sliding idle TTL, NOT fixed-lifetime)**: Unchanged. Round 3c
  S6's `OnCredentialDeleted` cooldown clear does NOT touch any
  binding's `boundAt`.
- **#5 (`EngineStats` field names locked)**: Unchanged. Round 3c
  W2's `ErrorType` + `ErrorCode` are appended to `ProviderError`
  (a different struct than `EngineStats`); W1's `arc.retryAfter`
  is appended to `anthropicRequestContext`. Neither alters the
  pinned `EngineStats` shape.
- **C1 (`/v1/messages` internal path in scope)**: Unchanged.
- **E-1 (janitor outer RLock + per-model write locks)**: Unchanged.
  Round 3c S6's cooldown clear uses the SAME outer Lock +
  per-model write Lock discipline.
- **W-3 (constructor-only injection)** — **NEW invariant:**
  the Round 3c C3(3) constructor gains `engine` +
  `conversationKey` params but NO `Config.CredEngine` field is
  exposed. The config stays flat; engine + key arrive via
  constructor args exactly as `eventBus` and `requestID` already
  do today. The engine reference is local to the coordinator
  (lifetime = request scope).
- **Round 3c — C2 four-surface invariant:** the four surfaces
  (enum comments, return-bullets, pseudocode, failure-modes
  table) MUST agree on the ReselectMode mapping. Drift is a
  contract regression; sibling workers who touch the
  precedence tree must verify all four surfaces after their
  edits.
- **R3-1..R3-8 SEMANTICS STAND**: every ruling's behavior is
  unchanged; the ten Round 3c items are specification-level
  fixes (gate hoisting, ReselectMode unification, three
  nonexistent references, passthrough classification source,
  ProviderError extension, soonest-expiry single-shot,
  `rate_limit_exceeded` literal, tried-set home pin, S3 wording
  standardization, OnCredentialDeleted cooldown hygiene). No
  R3-1..R3-8 ruling is overridden.