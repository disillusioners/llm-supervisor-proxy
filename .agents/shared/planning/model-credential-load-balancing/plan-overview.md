# Plan Overview: Model → Credentials Load Balancing

Date: 2026-08-21
Author: planner[v2] via plan-creation worker
Status: **Amended — Ready for Implementation** (architect council rulings incorporated 2026-08-21)
Branch: `feature/model-credential-load-balancing` (base `latest @ fea5874`)

> **AMENDED 2026-08-21 (leader rulings — architect council stress-test):** This overview has been
> updated for rulings **A-1** (token-salted conversation key, computed post-auth), **M-1**
> (non-destructive migration 028: `credential_id` retained as derived shadow; drop deferred to
> 029+), **A-2** (multimodal content hashing in v1), **W-1/W-2/W-3 + E-1/E-2/E-3/E-4** (engine
> contract reconciliations), and the **mandatory PG-gated 028 transaction test**. Amendment
> sources: `architecture-recommendation.md` (§7 consolidated list) → amended contract
> (`technical-analysis.md` Amendment Changelog, lines 1349-1373) → amended phase files (each ends
> with its own Amendment Changelog). See "Amendment Record" at the end of this file for the
> overview-level change list.

**Authoritative references — this plan translates, it does not re-decide:**

- `.agents/shared/planning/model-credential-load-balancing/decisions.md` — decision log A–E (conversation key, persistence schema, engine, rollout, testing)
- `.agents/shared/planning/model-credential-load-balancing/technical-analysis.md` — **the contract**: Go API signatures, migration SQL, concurrency model, invalidation semantics, backward-compat matrix, risks

Where this plan and the contract disagree on a name or signature, **the contract wins**. Two contract
*bugs* found during code verification are corrected here and flagged inline (see Risks #11–#12 and
Open Questions): both are additive fixes that preserve the contract's semantics.

---

## Objective

Operators can configure 2..N same-provider upstream credentials per model with integer weights, and
the proxy distributes requests for that model across those credentials proportionally to weight,
while pinning every conversation (identified by the token-salted key
`sha256(model_id + "|" + token_id + "|" + first_user_message)` per A-1) to exactly one credential
**while the conversation is active** — bindings use a **sliding idle TTL** (default 24h):
`boundAt` refreshes on every in-TTL hit, so an active conversation stays bound indefinitely and a
binding expires only after 24h of consecutive idle (#10). Models with a single credential behave
byte-identically to today, and existing single-credential deployments migrate losslessly via one
atomic DB migration that keeps `credential_id` as a derived shadow column (M-1).

## Scope

### In Scope

- **DB schema swap**: `models.credential_id` (single) → `models.credentials_json` (ordered weighted
  JSON array `[{credential_id, weight, position}]`), migration 028 both dialects. **M-1
  (amended):** the `credential_id` COLUMN IS RETAINED as a derived shadow
  (`= credentials[0].credential_id`, same-statement write) for legacy/external readers; DROP
  deferred to migration 029+ behind a tracked issue + deprecation window (decisions.md §B as
  amended).
- **Types & store layer**: `ModelConfig.Credentials []CredentialRef` (field `CredentialID` removed),
  `ModelsManager` CRUD/validation/in-use-guard/scan rewritten against `credentials_json`; legacy
  `ResolveInternalConfig` preserved (reads `Credentials[0]`).
- **Selection & affinity engine**: new `pkg/credentiallb` — `Engine.GetOrSelect(modelID,
  conversationKey) (credentialID, newlyBound, err)` (W-1; C2: empty-key ⇒ no binding ⇒
  `newlyBound=false`), cumulative-prefix-sum weighted random, `(modelID, conversationKey)` binding
  map with **sliding idle TTL** (default 24h, refreshed on every in-TTL hit — #10) + 5min janitor
  (outer RLock, E-1), filter-survivors invalidation (E-2), no-map-writes single-credential fast
  path (E-3), per-model RNG + `Engine.Stats()` → `map[string]EngineStats{Hits, Misses, Bindings}`
  (E-4/#5), invalidation via direct hooks + `model.credentials.changed` event.
  `ModelsManager.ResolveInternalConfigWithAffinity` returns **`(ResolvedCredential, bool)`** —
  struct `ResolvedCredential{Provider, APIKey, BaseURL, InternalModel, CredentialID, NewlyBound}`
  + trailing `ok` (#3, leader-ruled struct form).
- **Handler wiring** (Phase 3, sibling): `ResolveInternalConfigWithAffinity` call sites in race +
  ultimate paths, **and the Anthropic `/v1/messages` internal path (C1: `handler_anthropic.go:340,461`
  → `internal_handler.go:69` — the primary Claude Code endpoint)**, `requestContext.conversationKey`
  + `anthropicRequestContext.conversationKey`, `proxy.NewHandler` 7th param (nil-safe).
- **UI** (Phase 4, sibling): multi-credential editor in ModelForm.tsx, `/fe/api/models` DTO change.
- **Tests/E2E** (Phase 5, sibling): handler integration tests + `test/test_mock_credential_lb.sh`.
- **Validation additions**: provider-match across a model's credentials, `weight > 0`, max 16
  credentials, duplicate-credential rejection.

### Out of Scope

- **Join table `model_credentials`** — evaluated and overruled in decisions.md §B (house style has
  zero join tables; JSON columns are the established pattern).
- **Client-supplied conversation header** (`x-conversation-id`) — rejected in decisions.md §A.
- **Persistent (cross-restart) bindings** — decisions.md §C "Restart Stickiness": in-memory + TTL
  for v1; DB-backed bindings deferred to a follow-up.
- **`cfg.UpstreamCredentialID` race-external path** — env-only global, explicitly unchanged
  (decisions.md §D table).
- **Ultimate-external credential injection** — provider detection only; passes client auth through.
  Only the detection read is shimmed (`PrimaryCredentialID()`).
- **Fixing the pre-existing broken `config.updated` event** (`pkg/config/config.go:518` vs
  `pkg/ultimatemodel/handler.go:155` `field`-key mismatch) — documented tech debt, referenced but
  NOT fixed (decisions.md §C, technical-analysis.md debt #1).
- **Multimodal first-user-message hashing** — ~~multimodal content ⇒ no affinity for v1~~
  **REVERSED by A-2 (amended)**: multimodal content is hashed via canonical JSON
  (precedent `hash_cache.go:172-186`); ALL requests get affinity in v1.
- **Cross-instance (Redis) affinity sharing** — v2 (technical-analysis Scalability).
- **Changing the 5-value return tuple of `ResolveInternalConfig`** — kept for symmetry; struct-return
  refactor explicitly out of scope (technical-analysis debt #4).

## Phases

| Phase | Name | Objective | Tasks | Coupling | Status | Owner |
|-------|------|-----------|-------|----------|--------|-------|
| 1 | DB schema + store + model types (migration 028) | `credentials_json` replaces `credential_id` end-to-end in the persistence + type layer; system compiles and behaves identically for single-credential models; old UI still functions via shim | 10 | tight with 2 (shared `CredentialRef`/`Credentials` type); compile-shim touchpoints handed to 3/4 | **detailed here** | this worker |
| 2 | Selection & affinity engine (`pkg/credentiallb`) + store integration seams | Engine implements the contract API with weighted random, TTL bindings, invalidation; `ModelsManager` owns the engine and exposes `ResolveInternalConfigWithAffinity` / `Engine()` | 9 | tight with 1 (consumes `models.CredentialRef`, store reads); exposes seams consumed by 3 | **detailed here** | this worker |
| 3 | Handler wiring (proxy + ultimate + main) | Both internal resolution call sites use `ResolveInternalConfigWithAffinity(modelID, rc.conversationKey)`; `conversationKey` computed once per request; `NewHandler` gains nil-safe 7th param; interface + mocks updated | ~12 | tight with 1 (compile shims), 2 (engine API) | summary; sibling | sibling worker |
| 4 | UI multi-credential editor | ModelForm.tsx multi-select + weights, types.ts, `/fe/api/models` DTO with `credentials` array; same-provider enforcement client-side | ~8 | loose with 1 (DTO shape via technical-analysis integration #4) | summary; sibling | sibling worker |
| 5 | Integration + E2E tests | `test/test_mock_credential_lb.sh` (affinity, distribution, provider check, backward compat) + handler integration tests | ~6 | loose with 3/4 | summary; sibling | sibling worker |

### Phase summaries 3–5 (detailed plans by sibling worker)

**Phase 3 — Handler wiring.** Extend `requestContext` with `conversationKey string` (reset in
`rc.reset()`). **A-1 (amended): the key is computed POST-AUTH — after `rc.tokenID` is set at
`handler.go:401`, NOT in `initRequestContext` at `:353`** (token salt unavailable there);
`initRequestContext` caches only `rc.firstUserMessage`. Key =
`credentiallb.ComputeConversationKey(modelID, tokenID, firstUserMessage)`; anonymous requests
degrade to the unsalted key. Replace `ResolveInternalConfig` with
`ResolveInternalConfigWithAffinity(modelID, conversationKey)` at `pkg/proxy/race_executor.go:112,137`
and `pkg/ultimatemodel/handler_internal.go:46`; thread `conversationKey` + `newlyBound` through
`upstreamRequest`/`executeRequest`/`ultimatemodel.Execute` (W-1); publish
`model_credential_selected` only when `newlyBound == true`. Add `credEngine *credentiallb.Engine`
to `proxy.Handler` + 7th constructor param (nil ⇒ legacy path everywhere); **W-3: constructor
injection ONLY — no `Config.CredEngine` field**; `cmd/main.go:108` passes the engine obtained from
`modelsMgr.Engine()` (Dispatcher Ruling #2). Add `ResolveInternalConfigWithAffinity` to
`models.ModelsConfigInterface` and update all test mocks (pkg/proxy, pkg/ui, pkg/ultimatemodel) to
the `Credentials` shape (Dispatcher Ruling #1).

**Phase 4 — UI.** `ModelForm.tsx` gains a weighted multi-credential picker (same-provider filtered),
`types.ts` + API payload gain `credentials: [{credential_id, weight, position}]`; the existing
single-credential dropdown remains as the "primary credential" shortcut mapping to `credentials[0]`
(decisions.md §D rollout). `/fe/api/models` DTO (`pkg/ui/server.go`) swaps `credential_id` for the
`credentials` array; validation errors (provider mismatch, weight ≤ 0) surface as HTTP 400 + toast.

**Phase 5 — Tests/E2E.** `test/test_mock_credential_lb.sh` mirroring `test/test_mock_ultimate_model.sh`:
two mock upstreams (4001/4002) asserting upstream API-key headers; **12-scenario matrix (Round-2
revised: was 11)** — token-salted affinity, unique-first distribution, **templated-first-message
distribution with rotating tokens (leader-mandated)**, **anonymous distribution**, **empty-key
distribution with ZERO events (W-2+C2)**, **multimodal affinity (A-2)**, provider-mismatch save
blocked, single-credential backward-compat byte comparison, **event-once-per-first-binding (W-1)**,
**token-salted affinity independence**, **`/v1/messages` Anthropic affinity + distribution (C1 —
Test 8)**, and **shutdown-order verification (`srv.Shutdown` → `credLB.Stop` → `dbStore.Close`,
#11)**. **The E2E script's full output pasted into the PR description is a Phase 5 exit
precondition (#14 — merge gate, not a nicety).** Plus `pkg/proxy` handler integration tests per
decisions.md §E (as amended), the **mandatory PG-gated 028 transaction test** gating Phase 1 exit,
and engine tests on the corrected substrate (DB-backed `setupIntegrationDB` or test-local
`ModelsConfigInterface` impl — JSON-backed `ModelsConfig` never owns an engine, #4).

## Coupling Map

### Phase → package ownership

| Package | Phase 1 | Phase 2 | Phase 3 | Phase 4 | Phase 5 |
|---------|---------|---------|---------|---------|---------|
| `pkg/store/database/migrations/{sqlite,postgres}/` (028 files) | **owns (new)** | — | — | — | verifies |
| `pkg/store/database/migrate.go`, `sqlc/`, `db/` (regen) | **owns** | — | — | — | — |
| `pkg/store/database/store.go`, `querybuilder.go`, `init.go` | **owns (schema/CRUD)** | **owns (engine integration)** | reads API | — | tests |
| `pkg/models/` (config.go, credential.go) | **owns (types/validate/legacy resolve)** | adds JSON-backed affinity method | adds interface method | — | — |
| `pkg/credentiallb/` (new package) | — | **owns** | consumes | — | tests |
| `pkg/proxy/` | compile-shim only (3 sites + fixtures) | **must not touch** | **owns** | — | tests |
| `pkg/ultimatemodel/` | compile-shim only (1 site + fixtures) | **must not touch** | **owns** | — | tests |
| `pkg/ui/` (server.go Go side) | compile-shim only (DTO keep-alive) | — | — | **owns** | — |
| `pkg/ui/frontend/` | — | — | — | **owns** | — |
| `cmd/main.go` | — | — | **owns** | — | — |
| `test/` (shell E2E) | — | — | — | — | **owns** |

### Phase ↔ phase

| | Phase 1 | Phase 2 | Phase 3 | Phase 4 | Phase 5 |
|---|---|---|---|---|---|
| Phase 1 | — | tight (shared `CredentialRef`, `Credentials` field; store writes what engine reads) | tight (field drop forces Phase 3 compile fixes; shims listed in phase1-plan §Compile sweep) | tight (DTO swap on `pkg/ui/server.go`; Phase 1 installs transitional shim) | loose |
| Phase 2 | tight | — | tight (engine API + `Engine()` + `ResolveInternalConfigWithAffinity` are Phase 3's only entry points) | loose (no direct dep) | loose |
| Phase 3 | tight | tight | — | loose (interface stable before UI) | tight (E2E exercises wiring) |
| Phase 4 | tight | loose | loose | — | loose |
| Phase 5 | loose | loose | tight | loose | — |

**Key seam discipline** (from the dispatch boundary contract): Phase 2 does **not** modify
`pkg/proxy/**` or `pkg/ultimatemodel/**`. Phase 1's touches there are mechanical compile shims
(behavior-preserving `PrimaryCredentialID()` reads), enumerated file:line in phase1-plan.md so the
Phase 3 sibling does not double-edit. `models.ModelsConfigInterface` gains
`ResolveInternalConfigWithAffinity` in **Phase 3** (not Phase 2) so Phase 2 never has to edit
pkg/proxy/pkg-ui test mocks to satisfy the interface.

## Risks

| # | Risk | Impact | Likelihood | Mitigation |
|---|------|--------|------------|------------|
| 1 | Migration 028 fails mid-way → partial state (column added, backfill incomplete) | High | Low | Single migration (add+backfill atomically, decisions.md §B "Migration Numbering" as amended); file-level `BEGIN TRANSACTION`/`COMMIT` in the SQL; runner records applied only on success (`migrate.go:71-97`) and retries next startup. **Amended: the file-level transaction itself is unproven through this runner+driver pairing (028 is the repo's first — see Research Insights correction); proven by the MANDATORY PG-gated 028 test (SQLite 4a + Postgres 4b) gating Phase 1 exit** |
| 2 | ~~Contract bug: `idx_models_credential_id` blocks SQLite `DROP COLUMN`~~ **SUPERSEDED by M-1** — 028 no longer drops the column; the index-blocks-DROP hazard moves to migration 029 (tracked issue, which must include `DROP INDEX IF EXISTS idx_models_credential_id` before its `DROP COLUMN` on both dialects) | — (deferred to 029) | — | Risk consciously transferred to the 029 tracked issue; 029's SQL sketch (technical-analysis.md Migration SQL, deferred section) already contains the DROP INDEX step |
| 3 | Dropping `ModelConfig.CredentialID` breaks compilation of `pkg/proxy/handler_anthropic.go:294-295`, `pkg/proxy/race_executor.go:55-58`, `pkg/ultimatemodel/handler.go:288-289`, `pkg/ui/server.go` + ~12 test files | Medium (compile break across sibling-owned packages) | Certain if unhandled | Phase 1 Task 9 is an explicit **mechanical compile-shim sweep** (behavior-preserving `PrimaryCredentialID()` substitution + `credential_id`→`credentials[0]` write translation in the UI server); file:line list in phase1-plan.md; semantic changes stay in Phases 3–4 |
| 4 | Postgres backfill JSON not byte-identical to SQLite (`jsonb` reorders keys + adds spaces) → cross-dialect test assertions diverge | Low | Medium if contract SQL copied verbatim | Phase 1 Task 1 uses the same string-concatenation form in both dialects (byte-identical output); contract's `jsonb_build_array` variant documented as acceptable alternative with semantic (not byte) assertions |
| 5 | Engine memory growth from abandoned conversations | Medium | Medium | Hybrid TTL (24h default) + 5-min janitor (decisions.md §C); tests cover lazy + janitor eviction |
| 6 | Stale bindings after missed invalidation events (bus drop, panic mid-publish) | Medium | Low | Defensive lookup check in `GetOrSelect` (binding's credential must still be in the model's configured list) self-corrects within one request (technical-analysis §Invalidation) |
| 7 | Call site accidentally keeps legacy `ResolveInternalConfig` for a multi-credential model → LB silently skipped | Medium | Medium | Phase 3 code-review checklist + contract risk #9; Phase 1 keeps legacy method but its doc comment marks it single-credential-only |
| 8 | Provider mismatch inside a credential list routes to wrong API | Medium | Medium | `Validate()` same-provider invariant (Phase 1 Task 6) + UI enforcement (Phase 4) + E2E check (Phase 5) |
| 9 | Weighted random skews at small N | Low | Low | ±5% @ N=10k fixed-seed tolerance bands (decisions.md §E #1) |
| 10 | **Templated-agent fleets** sharing a byte-identical first message pin to one credential — silent LB defeat for scripted/agent workloads (council-found; the 100-unique-message distribution test cannot detect it) | High (silent) | Medium | **A-1 token-salted key** (modelID+tokenID+firstUserMessage) bounds skew to within-principal; **leader-mandated templated-first-message tests at 3 layers** (engine unit, handler integration, E2E Test 2b) prove the rotation distributes; residual within-principal risk accepted + documented (decisions.md §A as amended) |
| 11 | **Empty-key 24h hotspot** — if `""` were treated as its own bucket, all key-less requests pin to one credential for 24h with zero cache benefit | Medium | Low (pinned semantics) | **W-2 pinned everywhere**: empty conversationKey ⇒ NO binding stored, fresh weighted pick per request; phase2 + phase5 tests assert binding-map size == 0 |
| 12 | Phase 2/3 seam drift (mock seeding, engine ownership) | Medium | Medium (now resolved) | **Resolved by Dispatcher Rulings #1–#2** (contract-variant engine ownership; `Credentials[]` mock seeding) and re-verified by the amendment pass — see Amendment Record |

## Success Criteria

| # | Criterion | How to Measure | Threshold |
|---|-----------|----------------|-----------|
| 1 | Migration 028 backfills losslessly | SQLite temp DB pre-populated with models rows (`credential_id` set/empty), run migrations, inspect `credentials_json` + `PRAGMA table_info` | Every set row ⇒ `[{"credential_id":"X","weight":1,"position":0}]` byte-exact; empty rows stay `'[]'`; **both columns present post-028 (M-1: `credential_id` retained as derived shadow, byte-exact = `credentials_json[0].credential_id`)**; PG equivalent via `TEST_DATABASE_URL` (semantic compare) + **mandatory 028 transaction test 4a/4b** |
| 2 | Single-credential models behave identically | Golden-tuple test of legacy `ResolveInternalConfig` before/after Phase 1; Phase 5 wire-level byte comparison | Identical tuple (provider, apiKey, baseURL, internalModel, ok) incl. peak-hour substitution |
| 3 | Weighted distribution respects weights | `engine_test.go`: weights [1,1,2], 10,000 unique conversation keys, fixed seed | Per-credential counts within ±5% of [2500, 2500, 5000] |
| 4 | Conversation affinity | Same `(modelID, convKey)` × 100 calls | 100/100 same credential |
| 5 | TTL + invalidation correctness | Lazy expiry at ttl=100ms; `OnModelChanged`/`OnCredentialDeleted` drop bindings | Expired/invalidated bindings re-select from the new set; single-credential fast path writes no bindings |
| 6 | Concurrency safety | `go test -race ./pkg/credentiallb/...` (100 goroutines × 1000 calls) | Clean run, no race reports, consistent per-key result |
| 7 | No regressions in owned packages | `go build ./... && go test ./...` after each phase | Green (with the per-phase compile-shim sweep keeping unrelated packages green) |
| 8 | Old UI still saves models between Phase 1 and Phase 4 | Manual: create/edit a single-credential model via existing UI post-Phase-1 | Write round-trips to `credentials_json` `[{"credential_id": X, "weight":1, "position":0}]` |
| 9 | E2E (Phase 5) | `test/test_mock_credential_lb.sh` | Affinity: single upstream per conversation; distribution within tolerance; provider mismatch blocked |

## Rollout / Rollback Notes

> **AMENDED 2026-08-21 (leader rulings — M-1):** the destructive DROP is removed from 028.

- **Rollout**: single binary, no feature flag (decisions.md §D). Migration 028 runs at startup via
  the existing ordered runner; all existing models backfilled; **`credential_id` retained as a
  derived shadow (M-1) — old binaries and external tooling keep working during the deprecation
  window; a migrated DB no longer bricks a rolled-back binary**. Engine starts empty; first
  request per conversation binds. DB backup (sqlite file copy / `pg_dump`) still recommended
  before first deploy of this branch.
- **Rollback**: `028_add_model_credentials.down.sql` is now **lossless** (M-1): it restores
  nothing — the shadow column was never dropped — it simply removes `credentials_json`; the
  shadow `credential_id` was maintained in the same statement on every write, so rolling back the
  binary alone also works (old binary reads a still-correct `credential_id`). Take the DB backup
  advisory as covering the pre-028 state regardless.
- **Deprecation window (load-bearing for M-1)**: release notes must state that `credential_id`
  is now derived and will be removed in a future minor; migration 029 (`DROP INDEX
  idx_models_credential_id` + `DROP COLUMN credential_id`, both dialects) is **tracked as an
  issue to be filed at merge time** — a two-week-ish window is the target; the issue is
  load-bearing, not a footnote (architecture-recommendation.md §3).
- **Compatibility matrix**: pre-LB `credential_id='X'` model ⇒ `[{"X",1,0}]` ⇒ engine fast-path ⇒
  identical routing; `UpstreamCredentialID` env path untouched; ultimate-external provider probe
  shims to `PrimaryCredentialID()` (technical-analysis §Backward Compatibility Matrix is
  authoritative).

## Research Insights

Verified against the working tree during planning (beyond the contract's citations):

- `migrate.go:24-52` — ordered array confirmed; 027 is last; runner `ExecContext`es whole files
  (multi-statement SQL is proven by 001–027). **CORRECTION (council-verified):** Postgres
  migration 024 does **not** use file-level `BEGIN`/`COMMIT` — its only `BEGIN` is inside
  PL/pgSQL. **Migration 028 is therefore the repo's FIRST file-level transactional migration**;
  the runner+driver pairing (bundled SQLite in `modernc.org/sqlite` v1.46.1, `pgx/v5`
  ExecContext) must be **proven by the mandatory PG-gated 028 transaction test** (phase1-plan
  Task 11 test 4a/4b; phase5-plan Exit Criterion #5), not assumed.
- **`migrations/{sqlite,postgres}/005_add_credentials.up.sql` both create
  `idx_models_credential_id`** — not mentioned anywhere in the contract; **superseded context:**
  this only mattered for the pre-M-1 DROP COLUMN variant (old Risk #2). Under M-1 the column and
  index both survive 028 and are removed together in migration 029 (tracked issue).
- `models.ModelConfig.CredentialID` has **more readers than the contract's file list**:
  `pkg/proxy/handler_anthropic.go:294-295` (Anthropic passthrough provider probe),
  `pkg/proxy/race_executor.go:55-58` (`raceIntProviderIsMiniMax`), `pkg/models/config.go`
  (JSON-backed `Validate`, `GetInternalConfig:594`, `ResolveInternalConfig:629`, JSON
  `RemoveCredential:792`), `pkg/ui/server.go:35,390,460,558` (DTO) — plus ~12 test files. Drives
  Phase 1 Task 9.
- `AddModel`/`UpdateModel`/`RemoveModel` use a 19-column dummy scan for existence checks
  (`store.go:920,973,1016`); the credential_id→credentials_json swap keeps arity at 19 (content
  changes, count does not).
- `QueryBuilder` (`querybuilder.go:81-261`) centralizes all 6 model statements — single-file edit
  point for the column swap.
- `sqlc.yaml` regenerates `pkg/store/database/db` from `sqlc/schema.sql`; the generated package is
  currently imported nowhere (orphan) but is compiled by `go build ./...`; `db/models.go:60`
  carries `CredentialID` from `SELECT *` — needs schema.sql update + regen.
- `NewModelsManager(store)` (`store.go:536`) currently takes no bus; `InitializeManagers(store,
  eventBus)` (`init.go:28-41`) already receives the bus and is the natural wiring point for the
  engine (Phase 2).
- `pkg/events/bus.go` — `Subscribe()` returns a buffered channel (100) with non-blocking publish
  (slow subscribers drop events) — consistent with the contract's "missed events self-heal via
  defensive lookup" stance.
- `database_test.go` header documents the PG gating pattern
  (`TEST_DATABASE_URL="postgres://..." go test -run TestPostgreSQL`) and the per-test
  `newSQLiteConnectionAtPath(t.TempDir())` + `RunMigrations` recipe — reusable for the 028 backfill
  test.
- Go 1.26 / `modernc.org/sqlite` v1.46.1 (bundled SQLite ≥ 3.35 ⇒ `DROP COLUMN` supported) /
  `pgx/v5` stdlib — driver prerequisites for migration 028 are met.

## Open Questions

1. **Phase 3 mock seeding divergence.** The sibling's phase3-plan.md (already written) says test
   mocks "continue to set `CredentialID: "cred-1"`" — impossible after Phase 1 removes the field.
   Resolution proposed here: all fixtures switch to `Credentials: []models.CredentialRef{{...}}` in
   Phase 1's compile sweep; Phase 3 only adds the new interface method to mocks. Dispatcher should
   confirm the sibling aligns.
2. **Engine ownership in `cmd/main.go`.** Contract: `ModelsManager` owns the engine; `main.go`
   passes `modelsMgr.Engine()` to `proxy.NewHandler` (integration #6). phase3-plan.md currently has
   main.go constructing the engine itself. Both satisfy the `NewHandler` 7th-param seam; Phase 2
   delivers `Engine()` + a `SetEngine`-free constructor-owned engine either way. Dispatcher should
   pick one (contract's variant recommended) so Phase 3 lands consistently.
3. **Max credentials per model = 16** and **TTL = 24h** — carried from decisions.md open questions;
   both tunable later without schema impact. Confirm during review.

## Dispatcher Rulings (aggregator, post fan-in — read before implementing)

These rulings resolve the Open Questions above in favor of the contract (`technical-analysis.md`
is the tiebreaker on any phase-file conflict). No phase-file content is invalidated; the rulings
pin the interpretation the implementer must follow.

1. **Q1 ruled (mock seeding):** Fixtures switch to `Credentials: []models.CredentialRef{...}`
   seeding in Phase 1's compile sweep. Phase 3's note that mocks "continue to set `CredentialID`"
   (phase3-plan.md:88) is superseded — read it as "seed a single-entry `Credentials` slice; where
   an ID is needed, use `PrimaryCredentialID()`". `ResolveInternalConfigWithAffinity` on mocks
   returns the same tuple as `ResolveInternalConfig` (single credential → no LB behavior).
2. **Q2 ruled (engine ownership):** Contract variant wins — `ModelsManager` owns the engine
   (`technical-analysis.md:471`, `phase2-plan.md:85`): `NewModelsManager`/`InitializeManagers`
   constructs it; `cmd/main.go` only obtains it via `modelsMgr.Engine()` and passes it as
   `proxy.NewHandler`'s 7th param. Phase 3's `NewEngine(...)` line in `cmd/main.go`
   (phase3-plan.md:37,105) is superseded — main.go must NOT construct a second engine; two
   independent binding stores would split-brain conversation affinity.
3. **Q3 left to review:** cap=16 and TTL=24h stand as defaults; they are config-time constants,
   not schema — adjustable during implementation review without plan impact.
4. **Seam invariant (applies to all phases):** `pkg/credentiallb` implements exactly the
   `technical-analysis.md §API Contract` signatures. Any drift between phase files is resolved
   in favor of the contract; discrepancies get logged in the PR description, not silently
   resolved by the implementer.
5. **Operator-visible behavior note:** ~~`models.credential_id` is DROPPED by migration 028~~
   **SUPERSEDED by M-1 (leader ruling):** migration 028 KEEPS `credential_id` as a derived shadow
   (`= credentials[0].credential_id`, same-statement write); the column drop moved to migration
   029+ behind a deprecation window and a tracked issue filed at merge time. The API/UI wire
   drops the field in Phase 4 regardless (the shadow is DB-level, for legacy/external readers
   only). See decisions.md §B as amended + technical-analysis.md Migration SQL (029 sketch).
   External tooling querying that column keeps working during the deprecation window; release
   notes flag the eventual 029 removal.

---

## Amendment Record (2026-08-21 — architect council / leader rulings)

Sources: `architecture-recommendation.md` (stress-test, §7 consolidated list) → amended contract
(`decisions.md` §A/§B amendment log entries; `technical-analysis.md` Amendment Changelog,
lines 1349-1373) → amended phase files (each carries per-ruling Amendment Changelogs).

| Ruling | Change | Where it landed |
|--------|--------|-----------------|
| **A-1** | Token-salted key `sha256(modelID\|tokenID\|firstUserMessage)`; **computed post-auth** (`handler.go:401+`, NOT `initRequestContext:353` — original location unimplementable, `rc.tokenID` unset there); anonymous ⇒ unsalted | decisions.md §A; technical-analysis API Contract + Data Flow; phase2 Task 1; phase3 Task 2a (new post-auth wiring site + grep-guard exit criterion); phase5 token-salted/anonymous/templated tests |
| **A-2** | Multimodal content hashed via canonical JSON (precedent `hash_cache.go:172-186`); no-affinity fallback REMOVED — all requests get affinity | decisions.md §A; technical-analysis `ExtractFirstUserMessage`; phase2 Task 1; phase3 context; phase5 multimodal affinity tests + E2E Test 2e |
| **M-1** | 028 non-destructive: ADD + backfill + keep `credential_id` as derived shadow (same-statement write, ~3 lines, Phase 1 Task 13 contract); DROP INDEX + DROP COLUMN deferred to 029+ | decisions.md §B (objection answered: one source + one derived shadow); technical-analysis Migration SQL (028 rewritten, 029 sketch); phase1 Tasks 1/5/11/13; phase4 narrative (wire vs DB); this overview (objective, scope, risks, rollout, ruling #5) |
| **M-1-test** | PG-gated 028 transaction test MANDATORY (SQLite 4a + Postgres 4b) — 028 is the repo's FIRST file-level transactional migration (024's only BEGIN is inside PL/pgSQL); gates Phase 1 exit | decisions.md §E; technical-analysis Risks; phase1 Task 11(4a/4b) + Exit Criterion; phase5 Exit Criterion #5 |
| **W-1** | `GetOrSelect` → `(credentialID, newlyBound, err)`; `model_credential_selected` fires ONLY on `newlyBound==true`; per-request bool heuristic removed | technical-analysis API Contract; phase2 Task 4/9 + design note #9; phase3 Scope #4/#9; phase5 event tests |
| **W-2** | Empty conversationKey ⇒ NO binding stored, fresh weighted pick per request (""-as-bucket reading removed everywhere) | decisions.md §E #8; technical-analysis GetOrSelect invariants; phase2 Tasks 1/4/9; phase3 logging; phase5 empty-key tests + E2E Test 2d |
| **W-3** | Constructor-only engine injection; `Config.CredEngine` removed from contract | technical-analysis API Contract + debt #5; phase3 Task 3 + grep-guard |
| **E-1** | Janitor: outer RLock + per-model write locks (one at a time) — not outer write lock | technical-analysis Locking Discipline/Edge Cases; phase2 Task 5 + sweep-concurrency test |
| **E-2** | `OnModelChanged` filters — preserves surviving bindings, drops orphans (no fleet-wide flush on reweight) | technical-analysis API Contract + Invalidation; phase2 Task 6 + filter-survivors test |
| **E-3** | Single-credential fast path: NO map writes (reverses phase2's "write-once binding"; binding-map size == 0 assertable) | decisions.md §E #7; technical-analysis; phase2 Task 4; phase3 exit criterion #8; phase5 fast-path tests |
| **E-4** | Per-model RNG; `Engine.Stats()`; janitor panic-recovery (recommended, in Phase 2 scope) | technical-analysis Engine Internals; phase2 Tasks 4/5 + Stats/panic tests |
| **Tracked issues** | (1) migration 029 cleanup — load-bearing for M-1, file at merge time; (2) pre-existing `config.updated` payload mismatch (`config.go:518` vs `handler.go:155`) — separate issue, not this feature | decisions.md Tracked Issues section; this overview Rollout notes |

**Internal consistency statement:** all six plan documents (overview, decisions, technical-analysis,
phase1–phase5) now carry matching amendment semantics. Verified seams: engine ownership
(ModelsManager owns; main.go passes `Engine()`), key computation site (post-auth), GetOrSelect
3-tuple, empty-key no-binding, non-destructive 028 with derived shadow, mandatory PG-gated
transaction test, E-3 fast-path no-writes. The contract (`technical-analysis.md`) remains the
tiebreaker on any residual drift.

---

## Revision Record — Round 2 (2026-08-21, reviewer punch-list, leader-ruled)

Reviewer verdict APPROVED-WITH-NOTES (2 critical, 12 warnings, 33 suggestions) → leader rulings
applied in one pass. Sources: reviewer findings → leader rulings → contract Round-2 pass
(`technical-analysis.md` / `decisions.md` "Round 2 — Reviewer Punch-List" changelogs) → phase-file
Round-2 app sections (each phase file's "Round 2" appendix). Do NOT restructure phases.

| # | Ruling | What changed | Where |
|---|--------|--------------|-------|
| **C1** | `/v1/messages` internal path IN Phase 3 scope — thread `anthropicRequestContext.conversationKey` → `InternalHandler.HandleRequest` → `ResolveInternalConfigWithAffinity` (`cmd/main.go:139` → `handler_anthropic.go:340/447/461` → `internal_handler.go:67/69`) | decisions.md §D path table + technical-analysis Integration row 3 + References wiring list; phase3 Scope #11 / Task #16 / Files rows / grep-guard Exit #10; phase5 `TestHandler_AnthropicMessagesAffinity` + E2E Test 8 (matrix → 12 scenarios) | all layers |
| **C2** | Empty-key ⇒ `newlyBound=false` pinned (matches W-1: newlyBound ⇔ binding stored); fresh-pick observability = handler-side DEBUG log (phase3 Scope #8); engine publishes nothing for empty-key | technical-analysis GetOrSelect invariants + Risks #16; decisions.md §E; phase2 design note #13 + Task 7 doc.go acceptance; phase3 Task #6 reversed (empty-key emits ZERO events); phase5 test renamed `_NeverFires`, assertion reversed (5 → 0), 5 sites flipped | all layers |
| **#3** | `ResolveInternalConfigWithAffinity` → struct `ResolvedCredential{Provider, APIKey, BaseURL, InternalModel, CredentialID, NewlyBound}` + trailing `ok bool` (leader-ruled struct form; carries BOTH credentialID and newlyBound — tuple needed 7 elements) | technical-analysis API Contract + Data Flow + Integration rows; decisions.md §C; phase2 Task 9 (field-by-field branch table, 9-case test); phase3 Tasks #4/#6 + mock guidance (`SetNewlyBoundForTest`) | all layers |
| **#4** | Test substrate corrected: JSON-backed `ModelsConfig` never owns an engine — DB-backed `setupIntegrationDB` or test-local `ModelsConfigInterface` impl | phase5 Task #5 acceptance + Files substrate split + Task #21 helper + Exit #10 | phase5 |
| **#5** | `Stats()` pinned: `map[string]EngineStats{Hits, Misses, Bindings}` + O(1) `bindingsCount` mirror on modelState | technical-analysis (#5 pin + diagram); decisions.md §C naming; phase2 Task 4 acceptance (alias names rejected) | all layers |
| **#6** | Stale 2-tuple doc-comment at technical-analysis:429 → 3-tuple | technical-analysis | contract |
| **#7** | main.go does NOT construct the engine — `credLB := modelsMgr.Engine()` only; rebind inside ModelsManager (Ruling #2 enforcement) | phase3 Task #10 + Files row + Risk #15 + grep-guards Exit #11 (`NewEngine`/`RebindFromStore` in main.go → 0 hits) | phase3 |
| **#8** | Mock seeding via `TestRefs` fixture factory (CredentialID field gone post-Phase-1) | phase3 Files rows + Exit #12 grep-guard; phase1 delivers the factory (`pkg/models/testrefs_test.go`) | phase1+3 |
| **#9** | Phase-4 exit grep scoped to Model DTO/CRUD responses; `TestModelRequest` (ui/server.go:673) exempted | phase4 Exit #5 (exact grep + exemptions + expected ZERO) | phase4 |
| **#10** | Sliding idle TTL: `boundAt` refreshed on in-TTL hits; expires after 24h idle, not 24h from selection | technical-analysis NewEngine doc + invariants + Data Flow + invalidation table + Risks #3; decisions.md §C sliding-TTL block + §D; phase2 Tasks 4/5 (3 explicit TTL test cases + E-2 sliding-survivors); this overview Objective + In-Scope | all layers |
| **#11** | Shutdown order pinned: `srv.Shutdown(ctx)` → `credLB.Stop()` → `dbStore.Close()` (LIFO defer discipline) | phase3 Task #13 + Risk #6 + Files row; phase5 `TestHandler_ShutdownOrder` (Task #20, Exit #11) | phase3+5 |
| **#12** | UP-path shadow write = Go-computed `model.Credentials[0].CredentialID` bound in the same INSERT/UPDATE; `json_extract` form down-migration-only | phase1 Tasks 5 + 13 (+ Round-2 conflict note superseding Round-1 SQL-side wording) | phase1 |
| **#13** | Sweep kept in-branch; 122+ literals / 30+ files acknowledged; `TestRefs` fixture factory routes all test seeding; effort re-estimated | phase1 Task 9 + Files row #17 | phase1 |
| **#14** | E2E output paste in PR description = Phase 5 exit precondition (merge gate) | phase5 Task #16/#22 + Exit #3/#12 | phase5 |
| **#15** | `GetModel` (store.go:628-647) + `GetModelByName` (store.go:707-727) inline SQL added to the swap scope (update inline; no QueryBuilder refactor) | phase1 Task 4 + round-trip acceptance test | phase1 |
| **#16** | Cheap suggestions folded: (a) naming convention `Handler.credEngine`/`credLB`; (b) 023/024 down-file precedent re-cited; (c) .down not auto-run note; (d) 16-cap rationale rewritten (fresh cap, prior "house ceiling" claim WRONG — MaxFallbackDepth deprecated); (e) off-by-one cites re-verified; (f) credential-scoped prompt-cache rationale added to §A. None rejected. | decisions.md + technical-analysis (per-item cites in their Round-2 changelogs) | contract |

**Round-2 consistency audit (dispatcher, serial edits):** stale 6-tuple → 0 hits; empty-key
event-emit text → flipped at all 5 sites; main.go engine construction → grep-guarded to 0;
mock `CredentialID` seeding → grep-guarded to 0; sliding-TTL language synchronized across
contract + phase2 + overview. Phase structure unchanged (5 phases). The contract
(`technical-analysis.md`) remains the tiebreaker.
