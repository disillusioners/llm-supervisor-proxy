# Plan Overview: DB Caching / Resilience Layer — MVP Release

Date: 2026-08-28
Author: planner[v2] via plan-creation worker
Status: Ready for Review
Branch: `feature/db-cache-layer` @ `68dbe63`
Builds on: `.agents/shared/planning/db-cache-layer/architecture-recommendation.md` (council consensus, HIGH confidence; Option A+, 2/2 unanimity)

---

## Objective

Survive a ≥1-hour database outage with **zero/near-zero request failures** for known models/credentials/tokens by introducing a transparent in-process caching layer at the composition-root seams, with explicit fail-fast (HTTP 503 `config_store_unavailable`) for genuinely-unknown models instead of silent external-passthrough misroutes, while preserving today's wire-format, boot posture, and dual-dialect behavior with no schema migrations and no signature changes outside `pkg/proxy/handler.go` (the 2 fail-fast sites) and `pkg/store/database/store.go` (the additive strict methods + the silent-`[]` fix).

**Single-sentence completion test:** A simulated 1-hour database outage while serving traffic for previously-warmed models, credentials, and tokens produces **zero misroutes to `localhost:4001`**, **zero silent 401s for valid tokens**, and **zero silent-empty `[]` model lists** — exactly the failures that motivated the 2026-08-27 incident.

---

## Scope

### In Scope (MVP = Phase 0 + Phase 1, one release)

- New package `pkg/modelscache` with two decorators:
  - `CachedModelsConfig` wrapping `models.ModelsConfigInterface` (covers proxy + UI + ultimatemodel; verified: no concrete-type assertions downstream).
  - `CachedTokenStore` wrapping `auth.TokenStoreInterface` (covers proxy + UI).
- Additive-only `pkg/store/database/store.go` methods:
  - `GetModelStrict(ctx, modelID) (*models.ModelConfig, error)` — distinguishes `errors.Is(err, sql.ErrNoRows)` from infra errors.
  - `GetCredentialStrict(ctx, id) (*models.CredentialConfig, error)` — same error discrimination; **never serves ciphertext** (fixes store.go:1530-1538 hazard).
  - `ResolveInternalConfigWithAffinityCached(cachedModel, conversationKey, credLookup)` — resolver variant (council correction §8.3) so the hottest internal path makes **zero DB reads** on a warm cache.
  - `GetModelsStrict(ctx)`, `GetEnabledModelsStrict(ctx)`, `GetCredentialsStrict(ctx)` — **NEW** error-propagating list scans (additive only; no signature changes to the existing 19-method interface per [PLANNER-C]-amended). Combined with the strict single-row reads, the decorator can boot-prime + reconcile with full error visibility without ever touching the legacy silent-`[]` paths.
- Fail-fast 503 at the **two boundary sites only** (council correction §8.4):
  - `pkg/proxy/handler_functions.go:87-103` (chat-completions / OpenAI)
  - `pkg/proxy/handler_anthropic.go:154-170` (Anthropic messages)
  - Contract: `resolvedModel == nil && !ConfigStoreHealth.Healthy()` → 503 `config_store_unavailable`; `nil && healthy` → today's legit external passthrough (unchanged).
- Boot priming of the cache synchronously after `database.InitializeAll` (DB-down at boot keeps today's `log.Fatalf` fail-fast).
- Background reconciler at **60s** TTL rebuilds under write lock; clears stale entries; updates `healthy`.
- Synchronous write-through in the decorators for every mutator:
  - Model mutators (`AddModel`/`UpdateModel`/`RemoveModel`): write-through with authoritative payload; `AddModel` clears negative entries for that ID/name.
  - Credential mutators (`AddCredential`/`UpdateCredential`/`RemoveCredential`): **invalidate-only** (lazy refill — avoids empty-key ambiguity in updates).
  - Token mutators (`CreateToken`/`DeleteToken`/`UpdateTokenPermission`): write-through; `DeleteToken` clears both positive and negative entries.
- Deep copy-on-read of `FallbackChain`, `TruncateParams`, `Credentials` slices, plus `PeakHour*` fields where callers mutate, so cache entries can never be mutated through returned values.
- Negative caches:
  - Models: 60s TTL per ID/name; cleared by write-through on `AddModel` and revalidated by reconciler.
  - Tokens: 60s TTL for invalid hashes.
- Decryption-cached credentials: cache stores **decrypted plaintext** (residual accepted per arch §5 — plaintext already flows per request today); negative-cache decrypt failures; never serve ciphertext.
- Token cache: SHA-256-hashed key, plaintext never stored, LRU cap 10k entries, positive TTL **60s** or token expiry (whichever shorter, per leader decision 3), negative TTL 60s.
- Dead-default boot WARN tripwire: log loud warning when any enabled model's `UpstreamURL` is still the `http://localhost:4001` default (incident rec 3; informational only — dead-default removal itself stays a separate workstream).
- `CACHE_LAYER_ENABLED` env flag (default ON per leader decision 5) — instant rollback lever without rebuild.
- README/env docs updated to describe the new flag, TTLs, and outage behavior.

### Out of Scope (deferred to Phase 2 — explicitly NOT in this release)

- Usage replay ring (~10k bounded UPSERT) — T3 degradable in MVP (54 lost increments in the incident, non-fatal).
- Cache metrics (hit/miss/stale/negative counters via events bus or `/fe/api`).
- `[]byte`-with-explicit-wipe hardening of credential API keys (Go string immutability bounds MVP).
- Store-side publish-on-mutator for credential events (`AddCredential`/`UpdateCredential`); the decorator's write-through invalidation already covers correctness.
- Multi-replica cross-process invalidation (architecture flip condition; single-binary deployment today).
- Removal of the dead-default `localhost:4001` literal itself (separate workstream; tripwire warning is the MVP touch).
- Env-configurable TTLs (defaults are hardcoded first; env knobs in Phase 2).
- Reconciler backoff / bounded-retry on DB-down (today is straight-line 60s reconcile).
- pprof endpoint hardening for credential residency (verified: no pprof routes exist today; flagged as ops note).
- APST disable / PG hot standby / alerting / pod-recreation alerts / PG 17→18 upgrade ops / pve3 NVMe remediation (DevOps incident follow-ups, not proxy-side).

**Justification for boundary:** Phase 2 items either (a) have non-fatal degradation already acceptable (usage counter), (b) are hygiene that does not affect correctness given decorator write-through, or (c) are operational/deployment concerns out of band.

---

## Phases

| Phase | Name | Objective | Tasks | Coupling | Status |
|-------|------|-----------|-------|----------|--------|
| 1 | DB strict-method additions | Additive `GetModelStrict` / `GetModelByNameStrict` / `GetCredentialStrict` / `ResolveInternalConfigWithAffinityCached` / **`GetModelsStrict` / `GetEnabledModelsStrict` / `GetCredentialsStrict`** (the last three per C1 — no signature changes to existing methods) in `pkg/store/database/store.go`; required for D1+D3 corrections | 6 | tight with 2-3 (decorator consumes them); independent of 4, 5 | pending |
| 2 | `pkg/modelscache` core | `CachedModelsConfig` + `CachedTokenStore` decorators with deep-copy, negative cache, write-through, reconciler, boot priming, `ConfigStoreHealth` | 9 | tight with 1 (consumes strict methods); tight with 3 (wiring seam); loose with 4 (proxy uses `Healthy()`) | pending |
| 3 | Composition-root wiring | 2-line wrap at `cmd/main.go:90` and `:139`; env flag parse; boot priming after `database.InitializeAll`; deferred teardown ordering | 4 | tight with 2 (instantiates wrappers); independent of 4, 5 | pending |
| 4 | Proxy fail-fast 503 | Two-site gate at `handler_functions.go:87-103` and `handler_anthropic.go:154-170` — fail-fast only when nil + `!healthy`, never otherwise | 3 | loose with 2 (uses `ConfigStoreHealth` interface); independent of 3, 5 | pending |
| 5 | Hardening + tests + docs | Dead-default WARN, deep-copy assertions, outage simulation, contract tests, `go test -race` clean, env-doc updates | 8 | loose with 2 (tripwire reads cache), 3 (env flag), 4 (boundary test) | pending |

The release unit is **all five sub-stages together as ONE release** (leader decision 1 / arch §8.1).

> **Naming note (implementation mapping):** the phases in this table map 1:1 onto the implementation sub-stages used in `phase1-plan.md`: Phase 1 ↔ Sub-Stage **1A** (store strict methods), Phase 2 ↔ **1B** (`pkg/modelscache`), Phase 3 ↔ **1C** (composition-root wiring), Phase 4 ↔ **1D** (proxy fail-fast 503), Phase 5 ↔ **1E** (hardening + tests + docs). The implementation ran in the order **1A → 1B → 1D → 1C → 1E** (1D is interface-driven and lands before the wiring).

---

## Coupling Map

| | Phase 1 | Phase 2 | Phase 3 | Phase 4 | Phase 5 |
|---|---|---|---|---|---|
| Phase 1 | — | tight (decorator consumes strict methods + variant) | independent | independent | tight (silent-`[]` test) |
| Phase 2 | tight | — | tight (wrappers instantiated at main.go) | tight (`ConfigStoreHealth` consumed) | tight (decorator + outage sim) |
| Phase 3 | independent | tight | — | independent | loose (env flag test) |
| Phase 4 | independent | tight | independent | — | tight (boundary test) |
| Phase 5 | tight | tight | loose | tight | — |

**Critical shared contract — single source of truth:**
- `pkg/modelscache.ConfigStoreHealth{ Healthy() bool }` interface, implemented by `*CachedModelsConfig`. Consumed by Phase 4 boundary sites AND future observability code. Interface lives in `pkg/modelscache`; the proxy package imports `pkg/modelscache` for the interface only (no concrete type assertions — verified in arch §0, Appendix A).
- `ErrConfigUnavailable` and `ErrCredentialMissing` exported from `pkg/modelscache`; consumed by Phase 4 boundary sites for fail-fast, and by Phase 5 contract tests.

**Cross-phase risk surfaced:**
- **C1 amendment (eliminated):** the original 1.A.5 proposed changing the existing `*ModelsManager.GetModels()`/`GetEnabledModels()` signatures, which would break interface satisfaction at `main.go:90` AND at ~12 concrete-type call sites in `pkg/store/database/database_test.go` (verified at HEAD `68dbe63`: lines 215, 233, 265, 276, 305, ...). **Replaced by additive `GetModelsStrict` / `GetEnabledModelsStrict` / `GetCredentialsStrict` (per C1)** — legacy signatures stay byte-identical; the decorator consumes the strict variants for boot priming + reconciler; the silent-`[]` legacy behavior is unreachable in prod because the prod callers (`pkg/ui/server.go:508/:807`, `pkg/proxy/handler.go:242`) reach the decorator via the wrapped interface and the decorator's own `GetModels`/`GetEnabledModels` overrides return cached data unconditionally. Documented in phase 1 plan as amended 1.A.5/1.A.6.

---

## Risks

| # | Risk | Impact | Likelihood | Mitigation |
|---|------|--------|------------|------------|
| 1 | Negative-cache masks a model added during an outage | Medium | Low | TTL-bounded (60s); cleared by write-through on `AddModel`; UI/API is down during a DB outage anyway; reverified by reconciler once DB returns. |
| 2 | Shared decorator SPOF for proxy + UI + ultimatemodel | High | Low | `CACHE_LAYER_ENABLED=false` env flag for instant rollback; contract tests cover §2 matrix; revertable via two `Wrap*` lines if needed. |
| 3 | Plaintext API-key residency extended to process lifetime | Medium (security) | Certain (residual) | Accepted per arch §5 — plaintext already flows per-request today; delta is duration, not presence. Never logged (credential IDs only), never metric-labeled, never written to disk. Container core-dump policy flagged as ops note (not blocking MVP). |
| 4 | Decrypt-failure silently serves ciphertext as the API key (current `GetCredential` behavior) | High (auth/credentials) | Confirmed at store.go:1530-1538 | Cache **negative-caches decrypt failures** and never serves ciphertext (matrix row 5 → 503). `GetCredentialStrict` returns `(nil, ErrDecryptionFailed)` and the cache propagates `ErrCredentialMissing` to the boundary. |
| 5 | `events.Bus` drop-on-full silently drops invalidation events (bus.go:131-148, 100-buffer channels) | Medium | Medium | Bus is **never load-bearing for correctness** — synchronous write-through in the decorator is the primary invalidation path; the reconciler is the self-healing safety net. |
| 6 | Multi-replica deployment today would invalidate the in-process invalidation model | High | Not applicable today | Listed as the architecture flip condition; not true today (single binary deployment per blueprint); document as a known limitation. |
| 7 | Boot retry loop absent — DB-down at start crashes the process (today's behavior preserved) | Medium (operational) | Low | Mirrors today's `log.Fatalf` posture (leader decision 4); bounded boot-retry is a Phase 2 planner option. Document in README. |
| 8 | (C1 — eliminated by amendment) Original `GetModels`/`GetEnabledModels` signature change would have broken interface satisfaction + 12+ test sites | — | — | ~~Mitigation:~~ **Eliminated by design** — additive `GetModelsStrict`/`GetEnabledModelsStrict`/`GetCredentialsStrict` per C1; legacy signatures remain byte-identical. |
| 8a | (C3 — new) Transient credential-empty scan swaps last-known-good creds with empty `credsByID`, re-arming the misroute bug class | High | Medium | Reconciler ABORTS any swap when `GetCredentialsStrict` returns non-nil error OR a successful empty when previous snapshot was non-empty (per `phase1-plan.md` 1.B.6/1.B.7 + `[PLANNER-J]` in decisions.md). |
| 8b | (C2 — new) 60s token positive TTL → universal 401 at t≈61s into an outage | High | Certain | Three-tier state machine (per `phase1-plan.md` 1.B.8 + `[PLANNER-I]` in decisions.md): expired positives move to a `stale-positive` tier served ONLY on inner infra-class error (connection-refused/timeout/whitelisted by `errors.As`); verdict-class errors (`ErrTokenNotFound`, `ErrInvalidTokenFormat`) never fall back to stale → fail-closed preserved. `staleCap = 24h` default. |
| 9 | Reconciler self-heals unbounded during outage but stale window could exceed 24h hard cap → silent permanent divergence | Medium | Low | Hard cap default 24h enforced in reconciler; cap configurable. Both councilors converged on 24h; weekly cadence makes 24h-stale almost certainly more correct than 503. |
| 10 | `CACHE_LAYER_ENABLED` flag forgotten/misread in production rollback playbook | Medium | Medium | Document flag in README + env-vars table; PRD env-doc says: "set false + restart = pre-cache behavior". |
| 11 | LRU cap 10k tokens may be insufficient for high-cardinality deployments | Low | Unknown (Q5 unverified) | Cap is configurable constant; out-of-band eviction follows LRU; negative-cache keeps memory bounded for invalid hashes; Q5 remains open for ops measurement. |
| 12 | Auth TTL 60s delays token revocation visibility | Low (security) | Low | 60s = acceptable revocation SLO per leader decision 3; phase-2 env-configurable to 30s if needed. |

---

## Success Criteria

| # | Criterion | How to Measure | Threshold |
|---|-----------|----------------|-----------|
| A | Proxy serves 100% of requests for **a mix of internal credential-backed, multi-credential affinity, credFailover, and external models** during a simulated 1h DB outage using last-known-good config; zero failures overall; 503 only for the never-seen model | `pkg/modelscache/outage_test.go` integration test (per W7): pre-warm cache exercising `ResolveInternalConfigWithAffinity`, legacy `ResolveInternalConfig` via `DetectProvider` (race_executor.go:393, :559), and the `credFailover` spawn path. Simulate DB error injection for ≥1h of simulated time (advance clock; per W8 the worst-case refresh is ≤120s). After +10min past token `positivesTTL`, request tokens are STILL authorized via the stale-tier (per C2). | 100% served-or-503-never; zero misroutes to `localhost:4001`; zero silent 401s; recovery ≤120s |
| B | Unknown model + DB down → 503 `config_store_unavailable`, never external passthrough | Outage simulation test: cache warm, send request for never-seen model, DB-down → 503 with error_type=`config_store_unavailable` | 100% 503 — zero external passthroughs to `localhost:4001` |
| C | Known token remains authorized **at t>60s into the outage** (per C2 — via stale-tier served ONLY on inner infra errors; verdict-class errors remain fail-closed) | Outage simulation test: warm token cache, advance clock past `positivesTTL`, block DB with infra-class error, send authenticated request for internal model | 100% pass with cached/stale token; `ErrTokenNotFound`/`ErrInvalidTokenFormat` still return 401 |
| D | Unknown token + DB down → 401 (fail-closed security posture) | Outage simulation test: never-seen token, DB down, internal model | 100% 401 — zero bypass |
| E | API model/credential/token mutators invalidate cache correctly | Write-through invalidation contract test: every mutator path (model/credential/token) updates cache synchronously and a follow-up read returns the new state | 100% — covers all 9 mutators (`AddModel`/`UpdateModel`/`RemoveModel` × `AddCredential`/`UpdateCredential`/`RemoveCredential` × `CreateToken`/`DeleteToken`/`UpdateTokenPermission`) |
| F | `CACHE_LAYER_ENABLED=false` cleanly disables the whole layer | Wiring test: env=false → wrappers are pass-through → DB calls resume per existing behavior | identical to pre-MVP (verified by contract test reusing existing test suite) |
| G | No behavior change when DB healthy | Default-path contract test: every existing `pkg/proxy/*_test.go` and `pkg/ui/*_test.go` continues to pass with `CACHE_LAYER_ENABLED=true` | zero regressions (`go test ./...` clean) |
| H | Recovery convergence ≤120s worst case after DB returns (per W8 — 60s reconciler tick + 5s strict-fill timeout per W9) | Outage simulation test: bring DB back, advance clock to the next tick, assert cache refresh has occurred and a follow-up read returns the post-recovery state | ≤120s for refresh; first request after refresh sees new state |
| I | Deep-copy isolation — mutating returned slice never mutates cache | Unit test: read returned `FallbackChain`, append an entry, re-read → cache entry unchanged | 100% across all read methods that return slices |
| J | Race-free under concurrent reads + reconciler writes | `go test -race -count=10` over concurrent reads, mutator-triggered write-through, and reconciler tick | zero races reported |
| K | Dead-default WARN tripwire fires when `localhost:4001` is still in use | Boot log capture test: default config + at least one enabled model → WARN log present at boot | 100% |
| L | `go test -race ./...` clean across touched packages | Standard Go test invocation | zero failures, zero `-race` warnings |
| M | `ErrConfigUnavailable` exposed and consumed at boundary sites | Compile-time check via `*CachedModelsConfig` implementing `pkg/modelscache.ConfigStoreHealth`; runtime check via integration test | compile + runtime |
| N | Decrypt-failed credentials never served as ciphertext | Inject a fake credential whose stored `APIKey` cannot be decrypted; request internal model referencing it during a DB outage | 503 with `ErrCredentialMissing`, never 200 with ciphertext-as-key |
| O | Write-through adds zero DB drift between cache and store | After mutator, query DB directly and compare to cache snapshot | zero divergence |

---

## Research Insights

**Driver — 2026-08-27 incident:** 18-second PG outage → 42 failed requests via silent internal→external misroute (data-center-scripts incident doc). Root cause conflated nil in `store.go:862-863` (`if err != nil { return nil }`).

**Council evidence anchors (verified at HEAD `68dbe63`):**
- `cmd/main.go` — verified seams at `:90` (modelsConfig), `:132-136` (proxyConfig), `:139` (tokenStore), `:149` (ui.NewServer), `:156` (proxy.NewHandler); all interface-typed; no concrete-type assertions downstream.
- `pkg/models/config.go:177-208` — `ModelsConfigInterface` 19-method surface with **no error returns** (D1 proof).
- `pkg/store/database/store.go`:
  - `:862-863` GetModel conflated nil (incident crux).
  - `:942-944` GetModelByName same shape (same defect — silent nil on err).
  - `:978-987` `GetModels` silent-`[]` on err.
  - `:990-999` `GetEnabledModels` silent-`[]` on err.
  - `:1530-1538` decrypt-failure → WARN + serve ciphertext.
  - `:1584-1612` `AddCredential` publishes nothing.
  - `:1614-1653` `UpdateCredential` publishes nothing.
  - `:1663+` `RemoveCredential` partial invalidation.
  - `:1818-1915` `ResolveInternalConfigWithAffinity` internally re-fetches the model and credentials at lines 1819, 1833, 1851, 1872 — D3 proof that decorator pass-through leaves the hot path DB-bound.
- `pkg/auth/store.go:101-128` — `ValidateToken` distinguishes `sql.ErrNoRows` (`ErrTokenNotFound`) vs infra errors; caller at handler.go:301-303 conflates both into `false`.
- `pkg/proxy/handler_functions.go:87-103` — boundary site: nil from `GetModel` → modelList := []string{originalModel} (external passthrough).
- `pkg/proxy/handler_anthropic.go:154-170` — boundary site: same logic for Anthropic.
- `pkg/proxy/handler.go:301-303` — `authenticate` returns `(nil, false)` on both invalid-token and DB-down (D2 proof).
- `pkg/proxy/handler.go:411-413` — `if authToken == nil && requiresAuth` → 401 (fail-closed chain).
- `pkg/events/bus.go:131-148` — `Publish` drop-on-full (100-buffer channel; D3 evidence — bus must never be load-bearing).

**Council rejected alternatives:**
- **B — cache inside `ModelsManager`:** entangles cache policy with SQL/dialect code; leaves the auth seam and credential hot path DB-bound.
- **C — full snapshot-store:** cold-start/boot-failure semantics re-introduce the misroute bug class (boot-time load failure becomes the very bug being fixed as a crash loop).

**Council scoring (5-axis, weights Complexity 20% / Scalability 20% / Maintainability 25% / Risk 20% / Cost 15%):**
- A+: 4.20 (agentic) / 3.55 (coding) — unanimous.
- B: rejected (2.75 / 2.55).
- C: rejected (2.60 / 2.60).

**Planner corrections (arch §8) — material, all four MUST be reflected in this plan:**
1. Auth cache re-sequenced from "hardening" to **same-release-as-MVP** (D2).
2. "Zero store changes" is false — additive strict methods required.
3. Resolver variant required — without it the hottest internal path stays DB-bound.
4. ~2-site `pkg/proxy` touch required for fail-fast 503.

---

## Open Questions

| # | Question | Disposition in MVP |
|---|----------|---------------------|
| Q1 | Auth positive TTL — 30s vs 5m | **Resolved by leader:** 60s (revocation SLO ≤60s acceptable). |
| Q2 | TTL configurability via env | **Deferred** — hardcoded defaults; Phase 2. |
| Q3 | Store-side credential-mutator publishes | **Deferred** — Phase 2 (decorator write-through covers correctness). |
| Q4 | Boot behavior with DB down at start | **Resolved by leader:** keep today's `log.Fatalf` fail-fast. |
| Q5 | Production token-table cardinality | Open; assumed small (LRU 10k). Ops measurement in Phase 2. |
| Q6 | Out-of-band SQL config writes | Assumed none (60s reconciler bounds drift). |
| Q7 | Dead-default `localhost:4001` literal removal | **Out of scope** — boot WARN tripwire only. |

---

## Effort Estimate Summary (re-budgeted after amendment pass)

| Component | Effort | Notes |
|-----------|--------|-------|
| Phase 1: `pkg/store/database` strict additions (single + list, **+GetModelsStrict +GetEnabledModelsStrict +GetCredentialsStrict** per C1/C3) + resolver variant | 2.0–2.5 dev-days | Additive only across 7 new methods; no signature breakage on the existing 19-method interface; 12+ concrete-call sites in `database_test.go` keep their shape; `cmd/main.go:90` compilation unaffected. |
| Phase 2: `pkg/modelscache` package (models + tokens decorators, **+3-tier token state machine with stale-tier per C2**, **+abort-on-error reconciler swap per C3**, **+legacy `ResolveInternalConfig` cache override per W1**, **+no-op `Stop()` on `CachedTokenStore` per W2**, **+Stop cancels reconciler ctx per W3**, **+`ListTokens`/`GetTokenByID` pass-through per W5**) | 3.0–3.5 dev-days | Includes deep-copy helpers, negative caches, write-through logic, LRU, infra-error classifier helper. |
| Phase 3: `cmd/main.go` wiring (**+wrapped vars declared + nil-guarded Stops per W2**) | 0.75 dev-day | 2-line wrap at `:90` and `:139`; env-flag parse; teardown LIFO order `srv → StopModelsCache → StopTokensCache → credLB → modelsMgr → dbStore` per W2. |
| Phase 4: Proxy fail-fast 503 | 0.5 dev-day | 2-site gate. |
| Phase 5: Tests + docs + dead-default WARN (**+stale-tier token rows in contract_test, +DetectProvider-under-DB-down test per W1, +abort-on-error reconciler test per C3, +recovery ≤120s per W8**) | 2.0–2.5 dev-days | Includes outage simulation test (mix per W7), contract matrix test, deep-copy aliasing, write-through invalidation, race-clean run, env-doc updates. |
| **Total** | **8.25–9.75 dev-days** | Aligns with re-budgeted architecture target (8–10 dev-days, per amendment); ~1.5 days net addition over the pre-amendment baseline absorbed by the stale-tier, the abort-on-error swap logic, and the legacy-resolver override. |

**Single-developer sequence (natural order; parallelizable in pairs only via explicit task boundaries):**
1. Phase 1.1-1.4 (store strict single-row + variant).
2. Phase 1.5-1.6 (store strict list reads — additive only, no test breakage).
3. Phase 2.1 (modelscache skeleton: types + locking + `ConfigStoreHealth`).
4. Phase 4 (proxy fail-fast 503) — parallelizable with Phase 2 onward (interface boundary already defined).
5. Phase 2.2 (`CachedModelsConfig` with legacy-resolver override per W1).
6. Phase 2.3 (`CachedTokenStore` with stale-tier + abort-on-error per C2).
7. Phase 2.4 (reconciler with abort-on-swap per C3 + Stop cancellation per W3).
8. Phase 3 (wiring + teardown ordering per W2).
9. Phase 5 (tests + docs + WARN).

Tasks 2.1+4 and 4 alone are independently parallelizable once the `ConfigStoreHealth` interface is declared (first commit of Phase 2).

---

## Files Touched (Change Inventory)

| Path | Nature | Phase | Notes |
|------|--------|-------|-------|
| `pkg/store/database/store.go` | **modified** | 1 | Additive only: `GetModelStrict`, `GetModelByNameStrict`, `GetCredentialStrict`, `ResolveInternalConfigWithAffinityCached`, `GetModelsStrict`, `GetEnabledModelsStrict`, `GetCredentialsStrict` (per C1/C3 — **no signature changes to existing methods**; the 12+ concrete-type call sites in `pkg/store/database/database_test.go` and the 4 internal helpers in `store.go:1300/:1309/:1337/:1490` all keep their shape). Legacy `GetModels`/`GetEnabledModels`/`GetCredentials`/`ResolveInternalConfig` retain their no-error return shape and the silent-`[]` legacy behavior is unreachable in prod once the decorator is wired at `cmd/main.go:90`. |
| `pkg/modelscache/models.go` | **new** | 2 | `CachedModelsConfig` + `WrapModels(...)` + state, write-through, reconciler, boot priming. |
| `pkg/modelscache/strict.go` | **new** | 2 | Local interface `strictSource` (consumed by CachedModelsConfig) declaring the strict methods and the resolver variant. |
| `pkg/modelscache/tokens.go` | **new** | 2 | `CachedTokenStore` + `WrapTokens(...)` + LRU + write-through. |
| `pkg/modelscache/health.go` | **new** | 2 | `ConfigStoreHealth{ Healthy() bool }` interface; `ErrConfigUnavailable`, `ErrCredentialMissing` exports. |
| `pkg/modelscache/copy.go` | **new** | 2 | Deep-copy helpers for `FallbackChain`, `TruncateParams`, `Credentials`, `PeakHour*`. |
| `pkg/modelscache/lru.go` | **new** | 2 | Tiny bounded-LRU for tokens (or `container/list` from stdlib — pick stdlib for zero deps). |
| `pkg/proxy/handler_functions.go` | **modified** | 4 | Add `ConfigStoreHealth` type-assertion at the `:87-103` site; 503 `config_store_unavailable` when `nil && !Healthy()`. |
| `pkg/proxy/handler_anthropic.go` | **modified** | 4 | Same pattern at `:154-170`. |
| `cmd/main.go` | **modified** | 3 | 2-line wrap at `:90` (models) and `:139` (tokens); env-flag parse near top of `main()`; boot priming after `database.InitializeAll`; deferred `cache.Stop()` registration. |
| `pkg/proxy/handler.go` | (no change) | — | The fail-fast gates rely on the decorator exposing `Healthy()`; no proxy-config field change. |
| `pkg/proxy/internal_handler.go` | (no change) | — | Resolver variant is consumed via the `CachedModelsConfig` decorator's own `ResolveInternalConfigWithAffinity` (no override needed — the decorator overrides the existing method). |
| `pkg/ui/server.go` | (no change) | — | UI mutators receive the wrapped `ModelsConfig` and the wrapped `TokenStore` via `NewServer(...)`'s existing parameters — same compile. |
| `pkg/ultimatemodel/handler.go` | (no change) | — | ultimatemodel receives the wrapped `ModelsConfig` via `NewHandler(...)`'s `modelsMgr models.ModelsConfigInterface` argument — same compile. |
| `pkg/proxy/race_executor.go` | (no change) | — | Consumes the decorator via the interface seam (already verified); the resolver variant is reached via the decorator's `ResolveInternalConfigWithAffinity`. |
| `README.md` (or `docs/`) | **modified** | 5 | Document `CACHE_LAYER_ENABLED`, TTLs, outage behavior, dead-default WARN. |
| `pkg/modelscache/outage_test.go` | **new** | 5 | Outage simulation — 1h simulated, 100 requests, zero misroutes. |
| `pkg/modelscache/contract_test.go` | **new** | 5 | Full §2 (cache_state × db_state) matrix contract. |
| `pkg/modelscache/models_test.go` | **new** | 5 | Deep-copy aliasing, write-through, negative-TTL, reconciler, boot priming. |
| `pkg/modelscache/tokens_test.go` | **new** | 5 | Auth cache: SHA-256 keying, LRU, negative-TTL, write-through mutators. |
| `pkg/modelscache/proxy_integration_test.go` | **new** | 5 | Boundary-site tests for `handler_functions.go` and `handler_anthropic.go`. |
| `pkg/proxy/handler_functions_failsafe_test.go` | **new** | 5 | Boundary-site 503 behavior at the two sites. |
| `pkg/proxy/handler_anthropic_failsafe_test.go` | **new** | 5 | Same. |
| `pkg/store/database/store_strict_test.go` | **new** | 5 | Strict-method contract — `errors.Is(err, sql.ErrNoRows)` vs infra error returns. |

**Net change count:** ~16 files (5 modified, 11 new).
**Interface expansion risk:** zero — both `ModelsConfigInterface` (19 methods, no errors) and `TokenStoreInterface` (6 methods) remain untouched. The decorator implements them transparently.
