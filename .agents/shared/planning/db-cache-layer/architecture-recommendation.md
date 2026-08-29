# Architecture Recommendation — DB Caching / Resilience Layer (`db-cache-layer`)

**Date:** 2026-08-28
**Status:** ✅ Recommended — council consensus (2/2 councilors, HIGH confidence)
**Branch:** `feature/db-cache-layer` (from latest `68dbe63`)
**Decision method:** Architect-convened council (2 councilors, `trade-off-analysis` skill, read-only, models `agentic`+`coding`) over architect-verified code evidence; synthesis by architect. Council record: governor `4811e1a8-1f71-4367-adf6-8aecdc2841be`, disagreements D1–D3 surfaced and resolved on evidence (see §1.1, §2, §3).
**Goal (user verbatim intent):** survive a DB outage of **≥1 hour** with zero/near-zero request failures; **focus on caching models**; graceful degradation of costly extras is acceptable.
**Motivating incident:** 2026-08-27, 18s PG outage → 42 failed requests via silent internal→external misroute (`/Users/nguyenminhkha/All/Code/data-center-scripts/docs/llmproxy-external-misroute-db-outage-2026-08-27.md`).

---

## 0. Executive Summary

**Recommended: Option A+ — caching decorators at the composition-root seams, plus additive-only strict read methods in `store/database`, plus a ~2-site fail-fast touch in `pkg/proxy`, with the auth-token cache shipping in the SAME release as the models cache.**

- New package `pkg/modelscache` with two decorators:
  - `CachedModelsConfig` wrapping `models.ModelsConfigInterface`, wired at **`cmd/main.go:90`** — covers proxy + UI + ultimatemodel (verified: all call sites go through the interface, no concrete-type assertions downstream).
  - `CachedTokenStore` wrapping `auth.TokenStoreInterface`, wired at **`cmd/main.go:139`** before injection into both `proxy.NewHandler` (:156) and `ui.NewServer` (:149).
- Additive-only `store/database` methods: `GetModelStrict` / `GetCredentialStrict` (distinguish `sql.ErrNoRows` from infra errors — the conflated-nil fix) and a **resolver variant** of `ResolveInternalConfigWithAffinity` accepting the cached model + a credential-lookup closure, so the hottest internal path makes **zero DB reads** on a warm cache.
- **Fail-fast 503** (`config_store_unavailable`) at the two proxy boundary sites when a model is unknown AND the config store is unhealthy — **never silent external passthrough**.
- Failure model: HIT → serve cached (zero DB); STALE + DB-down → serve last-known-good (unbounded during outage, hard cap default 24h); MISS + DB-down for unknown model → 503; auth → **cached-token-degraded-allow** (never-seen tokens → 401).
- Invalidation: synchronous **write-through in the decorators** (primary), 60s TTL reconciler (safety net), store-side `EventCredentialsChanged` publishes on credential mutators (Phase 2 hygiene — the bus is drop-on-full and must never be load-bearing).
- Effort: **~7–10 dev-days** — Phase 0 MVP (4–5d) + Phase 1 auth (1–2d, **same release**) + Phase 2 nice-to-have (2–3d). No schema migrations, no signature changes.

---

## 1. Chosen Architecture

### 1.1 Decision: Option A+ (decorator at the seam — with three material corrections to "pure A")

The council proved that Option A **as originally framed** ("zero store/database changes, zero pkg/proxy changes") is not achievable. Three evidence-based corrections define "A+":

1. **No error returns on the interface.** `GetModel`/`GetCredential` in `models.ModelsConfigInterface` (pkg/models/config.go:177-208) return bare pointers — a decorator implementing that interface literally cannot propagate not-found vs DB-down (council disagreement D1, resolved on evidence). Fix: **additive strict methods** on the concrete `*ModelsManager` returning `(T, error)` with `errors.Is(err, sql.ErrNoRows)` distinguished; the decorator consumes them via a locally-defined `strictSource` interface. The public 18-method interface stays untouched — JSON-config impl and mocks unaffected.
2. **Resolver re-reads.** `ResolveInternalConfigWithAffinity` internally re-fetches the model and credentials fresh (store.go ~1818, ~1833/1851/1872) — a naive decorator pass-through leaves the *hottest internal path* DB-bound, defeating the cache for exactly the requests the incident was about. Fix: a resolver variant that accepts the cached `*ModelConfig` plus a credential-lookup closure supplied by the decorator.
3. **Fail-fast needs a signal.** The two proxy boundary sites must distinguish "nil because genuinely not found" (legitimate external passthrough — a real feature) from "nil because the store is unhealthy". Fix: a minimal health capability `ConfigStoreHealth{ Healthy() bool }` exposed by the decorator and type-asserted at those two sites.

**Rejected alternatives** (full comparison in §6):
- **B — cache inside ModelsManager:** entangles cache policy with SQL/dialect code; leaves the auth seam and the credential hot path DB-bound. Appeal is code-line-count only.
- **C — full snapshot-store (generalized credentiallb engine pattern):** cold-start/boot-failure semantics re-introduce the misroute bug class (boot-time load failure = the exact bug being fixed, as a crash loop); schema-drift tax; biggest build cost.

### 1.2 Component layout

```
pkg/modelscache/                 (NEW package — no I/O, no SQL, no disk)
  models.go       CachedModelsConfig  — implements models.ModelsConfigInterface
  strict.go       strictSource        — local interface: ModelsConfigInterface
                                      + GetModelStrict / GetCredentialStrict
                                      + resolver variant
  tokens.go       CachedTokenStore    — implements auth.TokenStoreInterface (6 methods)
  health.go       ConfigStoreHealth{ Healthy() bool }
  copy.go         deep-copy helpers (cache isolation)
cmd/main.go                      2-line wiring change at :90 and :139 (+ env flag)
pkg/store/database/store.go      ADDITIVE: GetModelStrict, GetCredentialStrict,
                                 resolver variant; fix silent-[] in GetModels/GetEnabledModels
pkg/proxy/handler_functions.go:~87, handler_anthropic.go:~157
                                 fail-fast 503 on nil + !Healthy()  (the only pkg/proxy touch)
```

Wiring shape: `modelsConfig = modelscache.WrapModels(modelsMgr, opts)` at main.go:90; `tokenStore = modelscache.WrapTokens(auth.NewTokenStore(...))` at main.go:139. Downstream consumers (`proxy.Config` :132-136, `ui.NewServer` :149, `proxy.NewHandler` :156) receive wrapped values with no changes. **Rollback:** `CACHE_LAYER_ENABLED=false` env flag (instant, no rebuild) or revert the two wrap lines — both compatible; env flag recommended for operational ease.

### 1.3 Data structures & locking (ConfigManager precedent: load-once + RLock + copy-on-read)

One `sync.RWMutex` per cache.

**Model cache:**
- `models map[string]*models.ModelConfig` (keyed by ID)
- `nameIndex map[string]string` (Name → ID, serves `GetModelByName`)
- `enabledSnapshot []models.ModelConfig` (serves `GetModels`/`GetEnabledModels`)
- `negative map[string]negEntry{checkedAt}` — known-not-found IDs/names, TTL 60s (cleared by write-through `AddModel`; revalidated by reconciler)
- `lastRefresh time.Time`, `healthy bool` (DB reachability as of last refresh — powers `ConfigStoreHealth`)

**Credential cache:**
- `creds map[string]credEntry{ cred *models.CredentialConfig /*decrypted*/, decryptOK bool, refreshedAt time.Time }`

**Token cache:**
- `tokens map[[32]byte]tokenEntry{ auth *auth.AuthToken, expiresAt, cachedAt }` — keyed by **SHA-256 token hash; plaintext never stored**
- LRU cap ~10k; positive TTL 5m-or-token-expiry (tighten to 30s if revocation SLO demands — Q1); negative TTL 60s for invalid hashes

**Copy semantics: deep copy-on-read.** Clone `FallbackChain`, `TruncateParams`, `Credentials` slices on every read return so callers can never mutate cache entries (shallow-copy-immutability was considered and rejected as a fragile invariant — council ruling).

**Refresh:** background reconciler every **60s** rebuilds under write lock and atomically swaps maps; revalidates negative entries; updates `healthy`. **Boot priming:** synchronous fill after `database.InitializeAll` (mirrors the engine's `RebindFromStore`). DB-down-at-boot keeps today's `log.Fatalf` fail-fast posture (correct; bounded boot-retry is a planner option — Q4).

**Engine note:** the credentiallb engine needs no change — it is already zero-DB-per-request and stays fresh automatically because mutations pass through the decorator → store mutators → existing `OnModelChanged` hooks, plus boot `RebindFromStore` (store.go:654-656).

### 1.4 What is cached — and what is not

| Data | Cached? | Form | Rationale |
|---|---|---|---|
| Models — full `ModelConfig` (fallback chain, truncate params, credential refs, peak hours, ultimate/secondary flags) | ✅ | deep-copied structs | Rarely change; API/UI is sole mutation source; this is the misroute crux |
| Credentials | ✅ | **decrypted plaintext** in memory | Plaintext already flows per-request today; caching ciphertext + decrypt-on-use keeps the ciphertext-as-APIKey hazard armed on every read (store.go:1533-1537). The cache **negative-caches decrypt failures and never serves ciphertext** — this fixes the hazard rather than inheriting it |
| Auth tokens | ✅ **same release** | keyed by hash; `AuthToken` value | Council D2 proof: with the models cache fixed, internal models resolve correctly → `requiresInternalAuth` = true → `authenticate` (handler.go:301-303) conflates the DB error into "invalid" → **~100% of authenticated internal requests 401 during an outage** without token caching |
| ConfigManager settings | 🟢 already cached (load-once + RLock, store.go:46-78/233-237) | — | Outage-immune; the sanctioned precedent |
| credentiallb engine snapshot | 🟢 already zero-DB (refs-only + `OnModelChanged` + boot `RebindFromStore`) | — | No change needed |
| Request/event store | ❌ | — | Already in-memory |
| events.Bus | ❌ | — | Ephemeral telemetry |
| MCP store | ❌ | — | T3 degradable |
| bufferstore | ❌ | — | Disk-backed already |
| Usage counter | ❌ (T3) | — | Accept log-loss in MVP; bounded replay ring Phase 2 |

---

## 2. Failure-Semantics Matrix

| # | Cache state | DB state | Decorator behavior | Client outcome |
|---|---|---|---|---|
| 1 | HIT | OK or DOWN | serve cached copy, zero DB | ✅ success — the outage goal |
| 2 | miss | OK | strict-fill via `GetModelStrict`/`GetCredentialStrict`, serve | ✅ success |
| 3 | not-found (negative) | OK | negative-cache (60s TTL), return nil | ✅ **legitimate external passthrough unchanged** (a real feature, preserved) |
| 4 | miss, unknown model | DOWN | `(nil, ErrConfigUnavailable)` → boundary site sees nil + `!Healthy()` | 🔴 **503 `config_store_unavailable`** — NEVER silent external passthrough |
| 5 | HIT but referenced credential missing/undecryptable | any | `ErrCredentialMissing` | 🔴 503 — never a dangling reference, never ciphertext |
| 6 | stale | DOWN | serve last-known-good | ✅ success on stale config |
| A | auth: known token | any | cached validation | ✅ success (degraded-allow) |
| B | auth: invalid token | any | negative cache rejects | 401 |
| C | auth: never-seen token | DOWN | cannot validate | 401 (admitting unvalidatable tokens is a security hole; affected population ≈ 0) |

**Stale window — unbounded during outage, with a configurable hard cap, default 24h.** Justification against the ≥1h goal: real outages don't schedule themselves; model settings change ~weekly, so 24h-stale config is almost certainly more correct than 503ing working traffic; the cap exists only to prevent silent permanent divergence. Both councilors independently converged on 24h.

**Conflated-nil fix (root cause).** store.go:862-863 — `if err != nil { return nil }` inside `GetModel` makes `sql.ErrNoRows` ≡ connection-refused. Fixed by the strict methods (not-found → legitimate nil + negative cache; infra error → mark `healthy=false`). Applied at exactly two boundary sites — `handler_functions.go:87-103` and `handler_anthropic.go:157-170` — with logic: `nil && !healthy` → 503; `nil && healthy` → existing external passthrough. **Also fixed for free:** `GetModels`/`GetEnabledModels` silently returning `[]` on DB error (store.go ~978-1005 — second silent-degradation site, found during council verification).

**Auth posture — cached-token-degraded-allow** (both councilors; full fail-closed explicitly rejected on lockout math — a fail-closed proxy during a DB outage converts it into a total outage). Note: today's "fail-open" is really the nil-model bug defeating `requiresInternalAuth` — the incident's exact chain — and this design closes it at both layers.

---

## 3. Invalidation Design

**Primary: synchronous write-through in the decorators.** Mutators delegate to the inner store; on success, mutate/invalidate the cache under the write lock. This closes the `UpdateCredential`/`AddCredential` publish gap without touching store semantics, and covers UI mutations automatically (the UI mutates through the same decorator instance).

- Model mutators (`AddModel`/`UpdateModel`/`RemoveModel`): write-through with the authoritative payload (what was just written). `AddModel` also clears any negative entry for that ID/name.
- Credential mutators (`AddCredential`/`UpdateCredential`/`RemoveCredential`): **invalidate-only** (lazy refill on next read) — avoids empty-key / keep-existing payload ambiguity in update requests.
- Token mutators (create/delete/permission-update): write-through; delete clears both positive and negative entries.

**Safety net: 60s TTL reconciler** re-reading via the strict methods — self-heals out-of-band SQL edits, dropped events, and replay races; revalidates negative entries.

**Secondary hygiene (Phase 2):** add `publishCredentialsChanged` to `AddCredential`/`UpdateCredential` store-side, for future subscribers and observability. Verified invalidation-gap table:

| Mutator | Publishes today | Engine hook today |
|---|---|---|
| AddModel / UpdateModel | ✅ | ✅ `OnModelChanged` |
| AddCredential | ❌ | ❌ |
| UpdateCredential | ❌ | ❌ |
| RemoveCredential | ⚠️ partial (`OnCredentialDeleted` only) | partial |

**Event bus vs direct call:** `events.Bus.Publish` is non-blocking **drop-on-full** with only a WARN (bus.go:131-148, 100-buffer channels) — under UI/SSE backpressure, invalidation events can be silently dropped. Ruling (council D3): the bus is nicely decoupled but must **never be load-bearing for correctness**; synchronous write-through is the primary mechanism.

---

## 4. Degradation Tiers

| Tier | Subsystem | Behavior during DB outage |
|---|---|---|
| **T1 REQUIRED** | Model resolution; credential resolution (incl. API key); ultimate-model (rides the models cache); **auth tokens (same release as MVP)** | Served from cache — zero failures for known entities |
| **T2 IMPORTANT** | Config/settings (ConfigManager — already boot-loaded) | Outage-immune already |
| **T3 DEGRADABLE** | Usage counter — **accept log-loss in MVP** (incident precedent: 54 lost increments, non-fatal; clients unaffected); bounded replay ring ~10k entries in Phase 2 (UPSERT is idempotent-shaped). Request/event store (already in-memory). MCP store. | Degrade gracefully; no client impact |

- **UI:** reads serve cached data; **writes return a clear 503 — never silent divergence.**
- **Boot tripwire (bonus):** loud WARN when `UpstreamURL` is still the `http://localhost:4001` default while internal models exist — complements incident rec 3; dead-default removal itself stays a separate workstream (Q7).
- **Deliberately NOT cached:** request/event store, events bus, MCP store, bufferstore, usage counter, ConfigManager (already cached).

---

## 5. Security Review

**Decrypted API keys in memory:**
- Lifetime extends from per-request to **process-lifetime — accepted residual**: plaintext already exists in memory per-request today; the delta is residency duration, not presence. Both councilors reached this independently.
- **Never logged** — cache logs credential IDs only (matches the existing store.go:1534 contract). **Never metric labels.** **Never written to disk** — the cache package performs no I/O at all (no SQL, no files, no bufferstore).
- `pprof` routes: none exist (verified). Container core-dump policy: unverified — flagged as an ops note.
- Zeroing: best-effort only — Go string immutability makes reliable wipe impossible; optional `[]byte`-with-explicit-wipe hardening in Phase 2, not MVP-blocking.

**Decrypt-failure hazard — fixed, not inherited:** current `GetCredential` WARNs and serves **ciphertext as the APIKey** (store.go:1530-1538). The cache negative-caches decrypt failures and never serves ciphertext (matrix row 5 → 503).

**Tokens:** keyed by SHA-256 hash; plaintext never stored; negative TTL bounds post-delete/recreate lockout windows.

**Stale-serving is not a security regression:** it serves previously-valid config — worst case is routing/keys the deployment itself configured within the ≤24h cap. The 503 rule *closes* the fail-open misroute; nothing new opens.

---

## 6. Trade-Off Matrix (5-axis)

Axis weights used by both councilors: Complexity 20% / Scalability 20% / Maintainability 25% / Risk 20% / Cost 15%. Cell scores 1–5 (5 = best).

| Approach | Complexity | Scalability | Maintainability | Risk | Cost | Weighted (agentic / coding) | Verdict |
|---|---|---|---|---|---|---|---|
| **A+: Decorator at seam** | 4 — one new package, 2-line wiring, additive store reads | 5 — zero DB on warm hot path | 5 — policy isolated, compile-checked interface, 2-line revert | 4 — env-flag rollback + contract tests | 4 — no new infra | **4.20 / 3.55** | ✅ **RECOMMENDED (unanimous)** |
| B: Cache inside ModelsManager | 3 | 2 — credential hot path AND auth seam stay DB-bound | 2 — cache policy entangled with SQL/dialect code | 3 | 3 | 2.75 / 2.55 | ❌ Rejected |
| C: Full snapshot-store | 2 — cold-start/boot-failure semantics re-introduce the misroute bug class | 5 — ties A on raw perf | 2 — schema-drift tax | 2 — boot failure = crash loop of the very bug being fixed | 2 — biggest build cost | 2.60 / 2.60 | ❌ Rejected |

- A dominates by ~1.0–1.5 weighted points under both scorings; its strongest axes (Maintainability 25% + Complexity 20% = 45% combined weight) are the ones the codebase values most.
- B's appeal is code-line-count only; it leaves both the credential hot path and the auth seam uncovered.
- C ties A on scalability but pays in cold-start semantics — a boot-time load failure would re-introduce the exact bug this layer exists to fix.
- **Flip conditions (would legitimately reopen this decision):** (1) multi-replica deployment — bus events don't cross processes, so external invalidation becomes necessary for *all* options; (2) multi-thousand-model scale, where C becomes attractive. Neither is true today.
- Immaterial divergence: B-vs-C ordering flips between councilors (agentic B>C, coding C>B) — both reject B and C regardless.

---

## 7. Phased Implementation Outline

### Phase 0 — MVP (4–5 dev-days)
1. `store/database` additive: `GetModelStrict`, `GetCredentialStrict` (`errors.Is(err, sql.ErrNoRows)` distinguished from infra errors); resolver variant of `ResolveInternalConfigWithAffinity` accepting a cached `*ModelConfig` + credential-lookup closure; fix silent-`[]` in `GetModels`/`GetEnabledModels`.
2. `pkg/modelscache`: `CachedModelsConfig` — models + decrypted credentials, deep copy-on-read, negative caching, 60s reconciler, synchronous write-through, boot priming, `ConfigStoreHealth`.
3. Fail-fast 503 (`config_store_unavailable`) at the two proxy boundary sites (`handler_functions.go:87-103`, `handler_anthropic.go:157-170`).
4. Dead-default boot WARN tripwire.
5. `CACHE_LAYER_ENABLED` env flag. Recommendation: **default ON at merge** (active incident pain — an off-by-default fix nobody enables is worthless; the flag is the instant rollback). Conservative alternative per team risk appetite: default-off for one soak week, then flip.

### Phase 1 — Auth cache (1–2 dev-days) — **MUST ship in the same release as Phase 0**
Target: land before the PG 17→18 upgrade window (incident rec 7). `CachedTokenStore` + negative cache + write-through, wired at main.go:139 into BOTH `proxy.NewHandler` and `ui.NewServer`. (Council D2: without this, ~100% of authenticated internal requests 401 during an outage — the ≥1h-zero-failure goal is unmet.)

### Phase 2 — Nice-to-have (2–3 dev-days)
Usage replay ring (~10k entries); cache metrics (hit/miss/stale/negative counters via events bus or `/fe/api`); optional `[]byte` key-wipe hardening; store-side credential-mutator publishes (gap table §3); env-configurable TTLs.

**Total: ~7–10 dev-days.** Regression posture: no migrations, no signature changes, dual-dialect store tests untouched; the `pkg/proxy` touch is ~2 sites.

### Test strategy
- **Unit:** fake-DB error injection distinguishing `sql.ErrNoRows` / connection-refused / timeout; a failure-mode contract test covering the full (cache_state × db_state) matrix of §2; deep-copy aliasing test (mutate a returned slice, assert cache unaffected); negative-TTL expiry; write-through invalidation on every mutator.
- **Integration (outage simulation):** `docker stop postgres` (or sqlite file swap) — pre-warm cache, run 100 requests across `quick`/`agentic`/`coding`, assert **zero misroutes to `localhost:4001`**, 503 only for genuinely-unknown models, cached tokens accepted, recovery convergence ≤60s after DB returns.
- **Concurrency:** `go test -race` over concurrent read/write/refresh paths.

---

## 8. Risks & Open Questions

### Planner corrections (material — budget these)
1. **Auth cache re-sequenced** from "hardening" to **same-release-as-MVP** (D2 proof above).
2. **"Zero store changes" is false as stated** — the additive strict methods are required (the interface has no error returns).
3. **The resolver variant is required** — without it the hottest internal path stays DB-bound.
4. **~2-site `pkg/proxy` touch** for fail-fast 503 is required.

### Risks
- 🟡 Negative-cache can mask models added during an outage — TTL-bounded (60s), cleared by write-through on API adds; low likelihood (the API is down during a DB outage anyway).
- 🟡 The decorator is a shared SPOF for proxy + UI + ultimatemodel — mitigated by env-flag rollback + contract tests.
- 🟡 Plaintext key residency (§5) — accepted residual, documented.
- 🟢 Bus drop-on-full — mitigated by write-through-primary design.
- 🟢 Multi-replica future invalidates the in-process invalidation model (flip condition; not true today).

### Open questions
- **Q1** Revocation-latency SLO → auth positive TTL 30s vs 5m (default 5m-or-expiry).
- **Q2** TTL configurability — recommend hardcoded first, env in Phase 2.
- **Q3** Store-side credential-mutator publish gap — both councilors recommend defer-to-Phase-2 (cheap).
- **Q4** Boot behavior with DB down at start — keep `log.Fatalf` fail-fast (recommended) vs bounded retry.
- **Q5** Production token-table cardinality (assumed small; LRU cap 10k) — unverified.
- **Q6** Out-of-band SQL config writes — assumed none; the 60s reconciler bounds drift if the assumption is wrong.
- **Q7** Dead-default (`localhost:4001` / `external` credential) removal — separate workstream (incident rec 3); this design adds the boot tripwire only.

### Out of scope (noted only)
Incident doc recommendations 4–9: APST disable, PG hot standby, alerting, pod-recreation alerts, PG 17→18 upgrade ops, pve3 NVMe remediation — DevOps follow-ups that complement but do not substitute the proxy-side fix.

---

## Appendix A — Verified evidence anchors

| Claim | Anchor | Verified by |
|---|---|---|
| Conflated-nil (misroute crux) | store.go:862-863 `if err != nil { return nil }` | architect + 2/2 councilors |
| nil ⇒ "external/unknown, raw name" | handler_functions.go:87-103 | architect + council |
| Auth fail-open chain | handler.go:301-303, ~411-413; handler_anthropic.go:157-170 | council |
| Interface has NO error returns (D1) | models/config.go:177-208 | architect + council |
| Seam is interface-typed; no concrete-type assertions downstream | main.go:90, :132-136, :149, :156 | architect + 2/2 councilors |
| `UpdateCredential` publishes nothing | store.go:1615-1653 | architect + council |
| `AddCredential` publishes nothing | store.go:1584-1612 | architect + council |
| `RemoveCredential` partial invalidation | store.go:1663+ | council |
| Decrypt-failure ⇒ ciphertext served as APIKey | store.go:1530-1538 | architect + council |
| Bus `Publish` is drop-on-full | bus.go:131-148 (100-buffer channels) | council |
| ConfigManager cache precedent | store.go:46-78, 233-237 | architect |
| Engine zero-DB precedent | store.go:654-656 (boot `RebindFromStore`) | architect |
| Silent-`[]` on `GetModels`/`GetEnabledModels` | store.go:978-1005 | council |
| No pprof routes | cmd/main.go routing | council |
| Token store distinguishes errors but caller conflates | auth/store.go:101-128 vs handler.go:301-303 | architect + council |
