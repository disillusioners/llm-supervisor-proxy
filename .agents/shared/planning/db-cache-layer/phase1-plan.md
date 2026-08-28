# Phase 1: DB Cache MVP (Architecture Phase 0 + Phase 1, single release)

> **Scope note:** this phase covers architecture §7 Phase 0 + Phase 1 combined (per leader decision 1). Phase 2 (usage replay ring, cache metrics, `[]byte` key-wipe hardening, store-side credential-mutator publishes, env-configurable TTLs) is **explicitly out of this release** — see `.agents/shared/planning/db-cache-layer/decisions.md`.

**Phase objective:** Deliver the architecture's Option A+ in five internal sub-stages as a single release. After Phase 1, `go test -race ./...` must be green across all touched packages AND the **outage simulation test must demonstrate ≥1h simulated DB outage with zero misroutes to `localhost:4001` and zero silent 401s for valid tokens** (success criteria A–D from `plan-overview.md`).

**Estimated effort:** **8–10 dev-days** single-developer (re-budgeted after amendment pass); up to ~4.5 dev-days parallelizable if two engineers accept the interface-first contract below.

---

## Sub-Stage 1A — `pkg/store/database` strict-method additions

**Why first:** the decorator cannot consume strict methods that don't exist (council D1). All methods are strictly additive per [PLANNER-C] (amendment C1): single-row strict reads plus a list-strict trio (`GetModelsStrict`, `GetEnabledModelsStrict`, `GetCredentialsStrict`) — no existing signatures change.

**Files touched (this sub-stage):**
- `pkg/store/database/store.go` — modified (additive only).
- `pkg/store/database/store_strict_test.go` — new.

### Tasks

| # | Task | Depends On | Acceptance |
|---|------|------------|------------|
| 1.A.1 | Add `GetModelStrict(ctx context.Context, modelID string) (*models.ModelConfig, error)` to `*ModelsManager` — clone of `GetModel` (lines :851-895) but propagating `errors.Is(err, sql.ErrNoRows)` AS `ErrModelNotFound` and infra errors as-is. | none | compile clean; `errors.Is(err, sql.ErrNoRows)` ⇒ true; non-row SQL errors propagate unchanged. |
| 1.A.2 | Add `GetModelByNameStrict(ctx context.Context, modelName string) (*models.ModelConfig, error)` — same pattern against `:898-955`. | 1.A.1 | same as above; name lookup returns the same error discrimination. |
| 1.A.3 | Add `GetCredentialStrict(ctx context.Context, id string) (*models.CredentialConfig, error)` — clone of `GetCredential` (in the same range) but: **(a)** propagate SQL errors distinctly, **(b)** if `crypto.Decrypt` fails, return `(nil, ErrDecryptionFailed)` rather than WARN-and-serve-ciphertext (fixes store.go:1530-1538 hazard; arch §5). | 1.A.1 | `errors.Is(err, sql.ErrNoRows)` ⇒ true; inject a malformed ciphertext → returns `ErrDecryptionFailed`, never returns a credential whose `APIKey` is the raw ciphertext. |
| 1.A.4 | Add `ResolveInternalConfigWithAffinityCached(cached *models.ModelConfig, conversationKey string, credLookup func(credentialID string) (*models.CredentialConfig, bool)) (ResolvedCredential, bool)` on `*ModelsManager`. Body: same logic as `ResolveInternalConfigWithAffinity` (`:1818-1915`) but (a) skips the `m.GetModel(modelID)` re-read at line 1819 (uses supplied `cached`), (b) substitutes every `m.GetCredential(...)` (lines 1833, 1851, 1872) with `credLookup(id)`. The 2+-credentials engine path is preserved as-is and still calls `m.engine.GetOrSelect(...)`. The dangling-ref defensive heal at :1900-1902 stays. | 1.A.1, 1.A.3 | warm cache + happy DB ⇒ zero DB calls during the engine path (assert via a countingSource that increments on every `GetModel`/`GetCredential` call; assert counter == 0). |
| 1.A.5 | Add `GetModelsStrict(ctx context.Context) ([]models.ModelConfig, error)` and `GetEnabledModelsStrict(ctx context.Context) ([]models.ModelConfig, error)` as **NEW** methods on `*ModelsManager` — clones of the inner `scanModels` result that propagate errors via `return`, **additive only**. The legacy `GetModels()` / `GetEnabledModels()` signatures on `*ModelsManager` are **unchanged** — interface compilation against `models.ModelsConfigInterface` (which declares `GetModels() []ModelConfig` with no error) remains green at `cmd/main.go:90` and at ~12 concrete-type call sites in `pkg/store/database/database_test.go` (verified at HEAD `68dbe63`: line 215, 233, 265, 276, 305, etc.). The decorator consumes the strict variants for its boot-priming + reconciler snapshots; the legacy methods stay byte-identical and are not even mentioned in the decorator. **Silent-`[]` on legacy methods is therefore a non-issue in prod** — the only prod callers are `pkg/ui/server.go:508/:807`, `pkg/proxy/handler.go:242`, all reached via the wrapped interface, and the decorator's `GetModels`/`GetEnabledModels` overrides return cached data unconditionally. **Decrypt-failure handling for credential scan:** per-entry `crypto.Decrypt` failures cause the **whole scan to abort** (skip-and-flag would risk a partial-swap that masks configuration corruption); the method returns `(partial, ErrDecryptionFailureInScan)` and the reconciler/boot priming treat any non-nil error as authoritative "no-swap" — see [PLANNER-J] in `decisions.md`. | 1.A.1 | `errors.Is(err, sql.ErrNoRows)` undefined for list scans (they're bulk — not row-keyed); consumers treat any non-nil error as "snapshot unusable". Transient infra error ⇒ `(nil, err)`; legitimate empty result ⇒ `([], nil)`. Inject a row with undecryptable ciphertext during a credential scan ⇒ `(partial, ErrDecryptionFailureInScan)`; the row with raw ciphertext MUST NOT appear in `partial`. All existing `m.GetModels()` call sites compile unchanged. |
| 1.A.6 | Add `GetCredentialsStrict(ctx context.Context) ([]models.CredentialConfig, error)` as a NEW method on `*ModelsManager` — same intent as `GetModelsStrict` for credentials: error-propagating + decrypt-failure-discriminating + never-serves-ciphertext. **Required for C3:** the credential-list reconciler path must NEVER treat a transient infra-empty scan as authoritative; without this method the boot priming/reconciler must fall back to the silent-`[]` `GetCredentials()` which would swap an empty list into `credsByID` and destroy last-known-good credentials — re-arming the bug class this layer exists to fix. | 1.A.5 | Boot-prime/reconciler stubs `err=connection-refused` ⇒ `(nil, err)`; reconciler ABORTS swap, preserves last snapshot. Stubs 5 rows with row[2] ciphertext failure ⇒ `(partial-with-2-rows, ErrDecryptionFailureInScan)`; reconciler ABORTS. Stubs empty-but-successful scan ⇒ `([], nil)`; reconciler SWAPS (genuine empty state). |

### Test plan (1A) — amended

- `pkg/store/database/store_strict_test.go`:
  - `TestGetModelStrict_SqlNoRowsVsConnRefused` — fake DB returning `sql.ErrNoRows` vs a wrapping `*net.OpError`; assert error discrimination.
  - `TestGetModelByNameStrict_*` — same as above.
  - `TestGetCredentialStrict_CiphertextHardened` — inject credential row with `APIKey` = literal `"not-a-ciphertext-ENOENT-style"`; assert `(nil, ErrDecryptionFailed)`, never silent fallback.
  - `TestResolveInternalConfigWithAffinityCached_ZeroDBOnWarmCache` — wrap with a `countingSource` that increments a counter on every `GetModel`/`GetCredential` call; resolver via the variant + supplied closure; assert counter == 0.
  - `TestGetModelsStrict_*` (replaces `TestGetModels_SilentSliceFixed` — strict variant, no signature change to legacy method):
    - `TestGetModelsStrict_TransientErrorReturnsNilSlice` — fake DB returning `connection refused` → `(nil, err)` where `err` matches infra-class (see PLANNER-I).
    - `TestGetModelsStrict_EmptyResultLegit` — fake DB returning `[]` rows → `([], nil)` — distinct from infra error, consumers preserve last snapshot.
    - `TestGetModelsStrict_DecryptFailureAbortsScan` — fake DB returning 5 rows where `row[2].Credentials[0].APIKey` is raw ciphertext → `(partial-with-2-rows, ErrDecryptionFailureInScan)`; the offending row MUST NOT appear in `partial`.
  - `TestGetEnabledModelsStrict_*` — same three tests on the enabled variant.
  - `TestGetCredentialsStrict_*` — credential-equivalents of all three tests above.

### Coupling
- **Tight with:** 1B (decorator consumes strict methods + variant + strict list reads).
- **Independent of:** 1C, 1D, 1E.

### Risks
- ✅ ~~The legacy `GetModels`/`GetEnabledModels` signature change on `*ModelsManager` is technically observable to callers using the concrete type.~~ — **eliminated by C1 design**: the strict methods are additive-only; existing signatures stay byte-identical. Compile-satisfaction rationale: `models.ModelsConfigInterface` keeps `GetModels() []ModelConfig` so `*ModelsManager` continues to satisfy it; the decorator's own `GetModels` overrides the interface method (cache-served), so the silent-`[]` legacy path is unreachable in prod once wrapped at `main.go:90`.
- 🟡 The variant method name `ResolveInternalConfigWithAffinityCached` is verbose; aliasing could be considered but flagged as over-engineering for MVP.
- 🟡 The `GetCredentialsStrict` decrypt-failure-on-scan abort policy ([PLANNER-J]) is conservative — partial swaps would be more available but risk masking corruption. Documented in decisions.md.

### Exit criterion
`pkg/store/database/store.go` compiles clean with the 7 added methods (GetModelStrict, GetModelByNameStrict, GetCredentialStrict, ResolveInternalConfigWithAffinityCached, GetModelsStrict, GetEnabledModelsStrict, GetCredentialsStrict); **zero signatures changed**; `go test -race ./pkg/store/database/...` passes; `store_strict_test.go` is in place; the ~12 `m.GetModels()` test sites at HEAD `68dbe63` continue to compile.

---

## Sub-Stage 1B — `pkg/modelscache` package

**Why this sub-stage:** the architecture mandates a single-purpose, dependency-light new package. Sub-Stage 1B owns (a) type/locking skeleton, (b) `CachedModelsConfig`, (c) `CachedTokenStore`, (d) deep-copy helpers, (e) reconciler + boot priming, (f) LRU for tokens, (g) the `ConfigStoreHealth` interface, (h) tests.

**Files touched (this sub-stage):**
- `pkg/modelscache/health.go` — new.
- `pkg/modelscache/strict.go` — new.
- `pkg/modelscache/copy.go` — new.
- `pkg/modelscache/lru.go` — new.
- `pkg/modelscache/models.go` — new.
- `pkg/modelscache/tokens.go` — new.
- `pkg/modelscache/warn.go` — new.
- `pkg/modelscache/models_test.go` — new.
- `pkg/modelscache/tokens_test.go` — new.
- `pkg/modelscache/contract_test.go` — new.
- `pkg/modelscache/outage_test.go` — new.

> Convention: the package deliberately imports `pkg/store/database` ONLY for the `database.Dialect` const (already used elsewhere — no import cycle because `pkg/modelscache` is a leaf). It does NOT depend on `pkg/proxy` or `pkg/ui` — they import it (one-way edge).

### Tasks

| # | Task | Depends On | Acceptance |
|---|------|------------|------------|
| 1.B.1 | Declare `pkg/modelscache/health.go`: interface `ConfigStoreHealth { Healthy() bool }`; exported errors `ErrConfigUnavailable`, `ErrCredentialMissing`, `ErrDecryptionFailed` (alias of database-level) with `errors.Is` support and formatted message strings. | none | single source-of-truth contract; consumed by 1D. |
| 1.B.2 | Declare `pkg/modelscache/strict.go`: local interface `strictSource` — embeds `models.ModelsConfigInterface` and adds `GetModelStrict`, `GetModelByNameStrict`, `GetCredentialStrict`, and `ResolveInternalConfigWithAffinityCached`. The decorator accepts any `strictSource` (compile-time check: the decorator is the consumer, not the implementer; concrete `*ModelsManager` is). | 1.B.1 | compile-only check; `strictSource` is package-private. |
| 1.B.3 | Implement `pkg/modelscache/copy.go` — `deepCopyModelConfig(*models.ModelConfig) *models.ModelConfig` cloning `FallbackChain`, `TruncateParams`, `Credentials` slices + credentials' nested pointers; `deepCopyCredentialConfig(*models.CredentialConfig) *models.CredentialConfig` cloning internal maps; `deepCopyModelConfigs([]models.ModelConfig) []models.ModelConfig`. | none | unit test mutates the returned slice/credential and verifies the underlying cache is unaffected. |
| 1.B.4 | Implement `pkg/modelscache/lru.go` — a tiny bounded-LRU keyed by `[32]byte` using `container/list` from stdlib. Methods: `Get(key) (entry, ok)`, `Put(key, entry)`, `Delete(key)`. Capacity constant `defaultTokenCacheCap = 10000`. Public surface: `type lru struct{...}` package-private; constructor `newLRU(cap int) *lru`. | none | unit test: insert N+5 entries with cap N → oldest N evicted; Delete removes both map and list nodes. |
| 1.B.5 | Implement `pkg/modelscache/models.go` — type `CachedModelsConfig struct { inner strictSource; mu sync.RWMutex; modelsByID map[string]*models.ModelConfig; modelsByName map[string]string; credsByID map[string]credEntry; negByID map[string]negEntry; negByName map[string]negEntry; enabledSnapshot []models.ModelConfig; lastRefresh time.Time; healthy bool; ttl time.Duration; stalenessCap time.Duration; stopCh chan struct{}}; ...}; func WrapModels(inner strictSource, opts Options) (*CachedModelsConfig, error)`. Methods (full surface required by `models.ModelsConfigInterface` + `ConfigStoreHealth`): all 19 interface methods, plus `Healthy() bool`, plus `Stop()` for the deferred teardown. **Two resolver overrides are MANDATORY (per W1):** the decorator must implement (a) `ResolveInternalConfigWithAffinity(modelID, conversationKey) (ResolvedCredential, bool)` using the cache maps + the variant from 1.A.4 (zero-DB hot path), AND (b) legacy `ResolveInternalConfig(modelID) (provider, apiKey, baseURL, model string, ok bool)` ALSO from cache (single-credential fall-through: `cachedModel.PrimaryCredentialID()` → `credsByID` lookup via cached closure). The legacy override is required because `normalizers.DetectProvider` (pkg/proxy/normalizers/config.go:20) calls `cfg.ResolveInternalConfig(modelID)` for every non-stream and stream path at `pkg/proxy/race_executor.go:393` and `:559` — without (b), a DB-down scenario would return `"external"` even for known internal models, partially re-arming the misroute bug class. | 1.A.1, 1.A.4, 1.B.1, 1.B.2, 1.B.3 | every interface method implemented; write-through `AddModel` clears negative entries for ID+name; delete credential mutators drop creds by ID; hot-path resolver via `ResolveInternalConfigWithAffinityCached` reads cache maps only; legacy `ResolveInternalConfig` is also cache-served; `DetectProvider` against a known-internal model + zero-DB returns the actual credential provider, not `"external"`. |
| 1.B.6 | Implement the **boot priming** step inside `WrapModels(...)`: synchronously call `inner.GetModelsStrict(ctx)` + `inner.GetEnabledModelsStrict(ctx)` + `inner.GetCredentialsStrict(ctx)` (per Sub-Stage 1A.5+1A.6). On any of those returning a non-nil error → set `healthy=false`, **return an error from the constructor** (no partial state — abort priming entirely). On success → fill the maps under write lock, set `healthy=true`. **Database-down at boot must surface an error (caller decides to fatal)** — the `WrapModels` constructor returns `(*CachedModelsConfig, error)`; `cmd/main.go` `log.Fatalf`s per leader decision 4. Reconciler ABORTS any tick where strict reads return non-nil error (does NOT swap empty into maps — see C3 fix). Both the strict-read error and a decrypt-failure error are logged as WARN with the credential ID omitted. | 1.A.5, 1.A.6, 1.B.5 | warm-boot test: stub returns 3 models + 2 creds → maps populated, `healthy == true`; cold-boot test: stub strict read returns `(nil, errors.New("DB down"))` → `WrapModels` returns `(nil, err)`, no background goroutine started. |
| 1.B.7 | Implement the **60s reconciler** as a `time.Ticker` goroutine started by the constructor AFTER successful priming. Each tick: acquire a 5s `context.WithTimeout` (per [PLANNER-K, W9]) and call strict reads under write lock. **Atomic-swap-on-success-only:** when ALL three strict reads return `(value, nil)` with non-nil values OR legitimately empty AND the previous snapshot was empty, atomically swap maps and update `healthy=true`. **If ANY strict read returns non-nil error OR a successful empty scan when the previous snapshot was non-empty:** ABORT the swap, keep the prior maps, set `healthy=false`, log a WARN — do NOT touch the maps. Revalidate negative entries (drop entries whose `checkedAt` is older than `now - ttl`). Reconciler MAY continue to attempt re-fill indefinitely; once `lastRefresh.Add(stalenessCap)` is exceeded the cache logs a WARN and continues serving last-known-good but callers can opt into `Strict` reads to assert freshness. Default `stalenessCap = 24h`. **Stop() cancellation contract (per W3):** on `Stop()`, signal via `stopCh` and CANCEL the in-flight scan context FIRST; if the scan goroutine doesn't honor cancellation within ~5s, abandon with a WARN (scan-in-flight may continue to completion but its result is discarded by the closed `stopCh`). | 1.B.6 | reconciler test: pre-populate → stop DB source → tick → last-known-good preserved → `healthy == false`; restore DB → tick → cache reflects new state; `Stop()` cancels in-flight scan context within 5s. **Abort-on-error test (NEW per C3):** pre-populate cache with 5 credentials; tick returns `(0 creds, nil)` on transient infra-error-class scan ⇒ assert no swap occurred, `credsByID` still has 5 entries. |
| 1.B.8 | Implement `pkg/modelscache/tokens.go` — `CachedTokenStore struct { inner auth.TokenStoreInterface; mu sync.RWMutex; entries *lru; positivesTTL time.Duration; negativeTTL time.Duration; idToHash map[string][32]byte; staleCap time.Duration }; func WrapTokens(inner auth.TokenStoreInterface, opts Options) *CachedTokenStore` plus **no-op `func (c *CachedTokenStore) Stop()`** (per W2: required by the deferred-teardown wiring at 1.C.4 even though the LRU is zero-state and no goroutine requires shutdown). SHA-256 the plaintext via `auth.HashToken` (no key field on the LRU can leak plaintext). Implement all 6 `TokenStoreInterface` methods: `ValidateToken(ctx, plaintext)` → hash → LRU lookup. **Three-tier state machine (per C2):** entries are tagged `positive` (within `positivesTTL` or `token.ExpiresAt`), `stale-positive` (TTL-clamped positive whose clock expired but the entry is within `staleCap`), or `negative` (within `negativeTTL`). On a key hit: `positive` → return cached token immediately; `stale-positive` → only fall through to inner for revalidation; `negative` → return cached negative verdict (fail-closed). On a miss, call inner; classify inner errors via PLANNER-I infra-detection (whitelist: `*net.OpError`, `context.DeadlineExceeded`, `database/sql` `driver.ErrBadConn`, timeout string-match on the error message — `isInfraError(err) bool` helper). Branch on `(innerResult, innerErr)`: **(a)** success → store as `positive`, populate `idToHash[token.ID]=hash` (per W4: must populate on the read path too so DeleteToken can fan-out); clamp display-expiry to `min(token.ExpiresAt, now+positivesTTL)`, transition to `stale-positive` after that clock. **(b)** `isInfraError(innerErr)` AND a `stale-positive` exists for this key → log WARN, return the stale-positive (degraded-allow). **(c)** `ErrTokenNotFound`/`ErrInvalidTokenFormat` (verdict-class) → store as `negative` (never serve stale) and return that error. **(d)** other unknown error → return the error (no fallback). `staleCap` defaults to 24h. UI-only methods per W5: `ListTokens(ctx)` and `GetTokenByID(ctx, id)` are **pass-through to inner** (no cache) — UI is non-hot-path and the cold-boot UI divergence that would result from caching these is worse than the DB hit. DB-down during UI call → UI surface emits its standard 503. Mutators: `CreateToken` writes through (inserts positive + populates `idToHash`); `DeleteToken` invalidates BOTH positive AND negative entries for that ID's hash (via `idToHash[id]` fan-out) AND removes the `idToHash` entry; `UpdateTokenPermission` write-through and invalidate. | 1.B.4 | unit tests below cover all 4 branches (a/b/c/d) plus stale-tier expiry + infra-classification. |
| 1.B.9 | Implement `pkg/modelscache/health.go` `Healthy()` method on `*CachedModelsConfig` — returns `c.healthy` under RLock; safe under concurrent reconciler writes (snapshot reads). | 1.B.7 | unit test: reconciler tick sets `healthy=false` while write-lock held; concurrent `Healthy()` returns the prior value (RLock-protected); once reconciler releases, the next call returns new value. |
| 1.B.10 | Implement `pkg/modelscache/warn.go` — dead-default boot WARN tripwire: emit `log.Printf("[WARN] UpstreamURL is the development default localhost:4001 for model %s — please configure before production use", id)` during boot priming if any enabled model still references the literal default. Single emit, idempotent, runs once during boot (not on every read). | 1.B.6 | boot test with default config + 1 enabled model → WARN in logs. |

### Test plan (1B)

- `pkg/modelscache/models_test.go`:
  - `TestCachedModelsConfig_BootPriming_*` — happy + cold.
  - `TestCachedModelsConfig_WriteThrough_ModelMutators` — AddModel invalidates negative; UpdateModel replaces authoritative payload; RemoveModel evicts both ID and name maps.
  - `TestCachedModelsConfig_WriteThrough_CredentialMutatorsInvalidateOnly` — verify lazy refill semantics.
  - `TestCachedModelsConfig_NegativeCache_*` — TTL expiry + reconciler revalidation.
  - `TestCachedModelsConfig_Reconciler_*` — happy tick, down tick, recovery, stop semantics, **abort-on-error** (NEW per C3: transient empty scan does not swap if previous was non-empty).
  - `TestCachedModelsConfig_DeepCopy_FallbackChain` — mutate returned slice, re-read, assert cache unchanged.
  - `TestCachedModelsConfig_ResolveInternalConfigWithAffinity_HotPathZeroDB` — counting inner source asserts no calls during warm cache.
  - `TestCachedModelsConfig_ResolveInternalConfig_LegacyHotPathZeroDB` (NEW per W1) — `ResolveInternalConfig` against known internal model with DB-down → returns cached provider, not `"external"`.
  - `TestCachedModelsConfig_DetectProvider_UnderDBDown` (NEW per W1) — drive `normalizers.DetectProvider(cached, "internalModelID")` with a frozen DB source → returns the cached credential provider, not the `"external"` fallback.
  - `TestCachedModelsConfig_Warn_DeadDefault` — verify WARN log emitted at boot.
- `pkg/modelscache/tokens_test.go`:
  - `TestCachedTokenStore_KeyingIsSHA256` — assert `[32]byte` keys, plaintext never persisted.
  - `TestCachedTokenStore_PositiveTTL` — within TTL → cached return, no inner call.
  - `TestCachedTokenStore_NegativeTTL` — verdict-class error → cached as negative for `negativeTTL`.
  - `TestCachedTokenStore_StaleTierHitsOnInfraError` (NEW per C2): positive TTL expired, inner returns `*net.OpError("connection refused")` → returns stale-positive (degraded-allow).
  - `TestCachedTokenStore_StaleTierDoesNotHitOnNegativeVerdict` (NEW per C2): stale-positive + inner returns `ErrTokenNotFound` → returns `ErrTokenNotFound` (fail-closed).
  - `TestCachedTokenStore_StaleTierCapEjects` (NEW per C2): stale-positive older than `staleCap` → treated as cold, fresh inner call.
  - `TestCachedTokenStore_LRUEvictsAtCap` — insert 10001 entries with cap 10000 → oldest evicted.
  - `TestCachedTokenStore_DeleteClearsBothEntries` — insert a token, delete it, assert positive AND negative entries for that ID's hash are gone, AND the `idToHash` entry is removed (per W4).
  - `TestCachedTokenStore_WriteThrough_CreateToken` — verify inner call followed by cache positive + `idToHash` populated.
  - `TestCachedTokenStore_idToHashPopulatedOnValidateToken` (NEW per W4) — read path populates `idToHash` even when entry was added via miss-fill.
  - `TestCachedTokenStore_StopNoop` (NEW per W2) — `Stop()` is non-blocking + idempotent.
  - `TestCachedTokenStore_ListTokensGetTokenByIDPassThrough` (NEW per W5) — DB-down during UI call returns DB error to caller (no cache hidden behind).
- `pkg/modelscache/contract_test.go`:
  - **FAILURE-MODE MATRIX (the headline test) — covers all 8 rows of `architecture-recommendation.md` §2 + 3 C2-amended token rows:**
    - Row 1 (HIT + OK/DOWN): warm cache, request → served cached, zero inner-source reads.
    - Row 2 (MISS + OK): cold cache, request → strict-fill, served.
    - Row 3 (NOT-FOUND + OK, negative): request unknown model, DB-healthy → negative cached, returns nil; legitimate external passthrough after decorator returns nil to the boundary site.
    - Row 4 (MISS unknown + DOWN): cache cold for never-seen model, DB unreachable → `(nil, ErrConfigUnavailable)`.
    - Row 5 (HIT but credential missing/undecryptable): model cached, credential returns ErrDecryptionFailed → `(nil, ErrCredentialMissing)`.
    - Row 6 (STALE + DOWN): cache served last-known-good; healthy=false but read returns the stale entry.
    - Row A (auth known + any): token positive cache returns AuthToken.
    - Row B (auth invalid + any): negative cache rejects.
    - Row C (auth never-seen + DOWN): 401 (degraded-allow posture; admits unvalidatable tokens).
    - **Row A2 (C2): stale-tier serves positives during infra outage at t=∞** — warm positive for 1h token, advance past `positivesTTL`, simulate `connection refused` from inner → returns the stale-positive (degraded-allow).
    - **Row B2 (C2): verdict-class errors do NOT fall back to stale-tier** — stale-positive exists, inner returns `ErrTokenNotFound` → returns `ErrTokenNotFound` (fail-closed).
    - **Row C2 (W4): `DeleteToken` removes `idToHash` entry** — read path populated `idToHash` for an ID; delete the token; subsequent re-validation produces cold-miss, not stale hit.
- `pkg/modelscache/outage_test.go`:
  - `TestOutageSimulation_NoMisroutes_OneHourSimulated` — pre-warm cache, freeze DB with a never-resolving query (or stop the test DB), advance clock + force **100 requests across an explicit mix per W7:** `(a)` external model — passes through unchanged; `(b)` internal model with single credential — exercises `ResolveInternalConfigWithAffinityCached` AND legacy `ResolveInternalConfig` via `DetectProvider`; `(c)` internal model with multi-credential affinity — exercises the engine path; `(d)` internal model + forced rate-limit on the selected credential — exercises the `credFailover` spawn path. After **+10 minutes simulated wall-clock** (well past `positivesTTL`), request tokens are STILL authorized via the stale-tier (acceptance of C2). Assert: **zero failures**; 503 only for the never-seen model; recovery converges ≤120s worst-case after DB returns (per W8).

### Coupling
- **Tight with:** 1A (consumes strict methods); 1D (config-store-health consumed at boundary).
- **Loose with:** 1C (env flag + wiring); 1E (env docs, dead-default WARN).

### Risks
- 🟡 The reconciler goroutine must be respected by `Stop()` during teardown — register `defer cache.Stop()` in `cmd/main.go` BEFORE `srv.Shutdown` so LIFO fires it AFTER request drain. Documented in 1C.
- 🟢 stdlib LRU is fine for 10k cap; revisit only if Q5 reveals larger cardinality.

### Exit criterion
`go test -race ./pkg/modelscache/...` green; outage simulation test passes; `Healthy()` method implemented on `CachedModelsConfig` and `CachedTokenStore` (latter is informational; tokens don't drive fail-fast — only models do).

---

## Sub-Stage 1C — `cmd/main.go` wiring

**Why this sub-stage:** the seam points are surgical — two existing assignments become wrapped values; one env-flag parse; one boot-priming call; one deferred teardown registration.

**Files touched (this sub-stage):**
- `cmd/main.go` — modified.

### Tasks

| # | Task | Depends On | Acceptance |
|---|------|------------|------------|
| 1.C.1 | Add env-flag parse near the top of `main()`: `cacheLayerEnabled := os.Getenv("CACHE_LAYER_ENABLED") != "false"` (default ON per leader decision 5). Log the resolved value once: `log.Printf("[cache] CACHE_LAYER_ENABLED=%v", cacheLayerEnabled)`. | none | env=false logs "false"; env unset logs "true". |
| 1.C.2 | At `cmd/main.go:90`, **declare wrapped vars** in the function scope BEFORE the listen block: `var wrappedModels *modelscache.CachedModelsConfig; var wrappedTokens *modelscache.CachedTokenStore` (per W2: both types now have `Stop()` methods — `CachedTokenStore.Stop()` is a no-op but the method must exist; `CachedModelsConfig.Stop()` signals the reconciler goroutine). Then replace `modelsConfig = modelsMgr` with `if cacheLayerEnabled { wrappedModels, primErr := modelscache.WrapModels(modelsMgr, modelscache.Options{PositiveTTL: 60s, NegativeTTL: 60s, StalenessCap: 24h, StrictFillTimeout: 5*time.Second}); if primErr != nil { log.Fatalf("Failed to prime models cache: %v", primErr) }; defer wrappedModels.Stop(); modelsConfig = wrappedModels } else { modelsConfig = modelsMgr }`. The local variable `modelsConfig` (interface-typed at line 47-48) flows downstream either way. | 1.B.7, 1.C.1 | env=true → WrapModels called; env=false → pass-through (no behavior change); boot priming failure → `log.Fatalf` (mirrors today's posture). |
| 1.C.3 | At `cmd/main.go:139`, replace `tokenStore := auth.NewTokenStore(dbStore.DB, dbStore.Dialect)` with `if cacheLayerEnabled { wrappedTokens = modelscache.WrapTokens(auth.NewTokenStore(dbStore.DB, dbStore.Dialect), modelscache.Options{PositiveTTL: 60s, NegativeTTL: 60s, LRUCap: 10000, StaleCap: 24*time.Hour}); tokenStore = wrappedTokens; defer wrappedTokens.Stop() } else { tokenStore = auth.NewTokenStore(dbStore.DB, dbStore.Dialect) }`. The same `tokenStore` variable flows to both `ui.NewServer` (line 149) and `proxy.NewHandler` (line 156). | 1.B.8, 1.C.1 | env=true → WrapTokens called; env=false → pass-through; `wrappedTokens.Stop()` is registered BEFORE the listen block (per W2). |
| 1.C.4 | Reorder the deferred teardown at the bottom of `main()` (around lines 65-100): per W2 the final teardown order on shutdown MUST be **`srv.Shutdown` → `wrappedModels.Stop()` → `wrappedTokens.Stop()` → `credLB.Stop()` → `modelsMgr.Close()` → `dbStore.Close()`**. Implementation: keep existing `defer dbStore.Close()` at line ~68. Register `defer modelsMgr.Close()` immediately after it (runs before dbStore.Close at LIFO). Register `defer credLB.Stop()` immediately after that (runs before modelsMgr.Close at LIFO). Register `defer wrappedTokens.Stop()` and `defer wrappedModels.Stop()` immediately before the listen block (~line 215, after cfg.Port is known but BEFORE `srv := &http.Server{...}`), so LIFO fires them right after `srv.Shutdown`. **Pre-listen registration is mandatory because defers register in source order — registering them after the listen block leaves them registered, but the LIFO order becomes ambiguous if anything else also defers between them and the bottom of main.** Each cache `Stop()` must be a no-op when the env-flag branch was `false` to avoid nil-deref panic — guard each with `if wrapped != nil { defer wrapped.Stop() }`. | 1.C.2, 1.C.3 | shutdown order on signal: `srv.Shutdown(ctx)` (drains HTTP via pre-registered `defer srv.Shutdown`) → wrapped models cache stops → wrapped tokens cache stops (no-op) → engine janitor stops → modelsMgr.Close → dbStore.Close. Verified via a stub `main_test.go`-style teardown order test (or by inspection of the LIFO order in code review). |

### Coupling
- **Tight with:** 1B (instantiates wrappers).
- **Independent of:** 1A, 1D, 1E (1D is testable in isolation via a fake `ModelsConfigInterface` that satisfies `ConfigStoreHealth`).

### Risks
- 🟡 If `cacheLayerEnabled=false` and the production behavior is unchanged, that's the entire rollback path — verified by running the existing test suite without the cache.
- 🟡 Boot-time `log.Fatalf` matches today's posture; deployment runbooks reference this exact line.

### Exit criterion
`go build ./...` clean; manual `CACHE_LAYER_ENABLED=true ./binary` runs the proxy without observable change to smoke-tested endpoints; `CACHE_LAYER_ENABLED=false ./binary` runs the same; the existing test suite is green under both.

---

## Sub-Stage 1D — Proxy fail-fast 503

**Why this sub-stage:** the architecturally specified fail-fast gate at the two boundary sites. This is the **only** `pkg/proxy` source change. The decorator at the seam (Sub-Stage 1C) means downstream consumers compile unchanged.

**Files touched (this sub-stage):**
- `pkg/proxy/handler_functions.go` — modified.
- `pkg/proxy/handler_anthropic.go` — modified.
- `pkg/proxy/handler_functions_failsafe_test.go` — new.
- `pkg/proxy/handler_anthropic_failsafe_test.go` — new.
- `pkg/modelscache/proxy_integration_test.go` — new.

### Tasks

| # | Task | Depends On | Acceptance |
|---|------|------------|------------|
| 1.D.1 | Add `ConfigStoreHealth` import + type-assertion in `pkg/proxy/handler_functions.go` around line 87-103. After `resolvedModel = conf.ModelsConfig.GetModel(originalModel)` (no signature change — GetModel still returns `*ModelConfig`), add: `var health modelscache.ConfigStoreHealth; if h, ok := conf.ModelsConfig.(modelscache.ConfigStoreHealth); ok { health = h }; if resolvedModel == nil && health != nil && !health.Healthy() { h.sendError(w, http.StatusServiceUnavailable, "Configuration store is unavailable; cannot resolve model '"+originalModel+"'", "config_store_unavailable", ""); h.publishEvent("config_store_unavailable", map[string]interface{}{...}); return }`. Use `modelscache.ErrConfigUnavailable` for `errors.Is` matching if useful. | 1.B.1 | contract: nil+healthy ⇒ today's external passthrough (unchanged); nil+!healthy ⇒ 503; nil+(no health capability) ⇒ today's behavior (legacy safe). |
| 1.D.2 | Same pattern in `pkg/proxy/handler_anthropic.go` around line 154-170 (mirror site). | 1.D.1 | same contract. |
| 1.D.3 | Error helper (next to existing `sendError` in `pkg/proxy/handler.go:357`) — already supports any code/message; call it with `http.StatusServiceUnavailable` and `errType="config_store_unavailable"`. | none | one-line decision; documented in code comment. |

### Test plan (1D)

- `pkg/proxy/handler_functions_failsafe_test.go`:
  - `TestOpenAIBoundary_HealthyNilReturnsExternalPassthrough` — `ModelsConfig` implements `Healthy()=true`, returns nil → modelList := []string{originalModel} (unchanged).
  - `TestOpenAIBoundary_UnhealthyNilReturnsServiceUnavailable` — `Healthy()=false`, returns nil → 503 `config_store_unavailable`.
  - `TestOpenAIBoundary_NoHealthCapabilityLegacySafe` — `ModelsConfig` does NOT implement `ConfigStoreHealth` → today's behavior (defensive — guards against any future seam regression where the decorator is forgotten in wiring).
- `pkg/proxy/handler_anthropic_failsafe_test.go`:
  - Same three tests on the Anthropic path.
- `pkg/modelscache/proxy_integration_test.go`:
  - End-to-end: pre-warm `CachedModelsConfig`, simulate DB-down (cold-cache unknown model scenario) → drive the proxy handler through `httptest.NewRecorder` → assert 503.

### Coupling
- **Loose with:** 1A (no strict-method calls here — type assertion only).
- **Independent of:** 1A, 1B (interface boundary), 1C, 1E — but **runtime behavior depends on 1C wiring** to enable the decorator.

### Risks
- 🟡 `Healthy()` must be read under RLock (architecturally the decorator's atomic-read); covered by Sub-Stage 1B task 1.B.9.
- 🟢 The "no health capability" defensive branch preserves back-compat for any future seam regression.

### Exit criterion
Both tests green; boundary fails closed (503) for nil+!healthy; fails open (today's behavior) for nil+healthy.

---

## Sub-Stage 1E — Hardening + tests + docs

**Why this sub-stage:** everything compiles and tests pass without these, but production-readiness requires the dead-default WARN, the deep-copy contract, the outage simulation contract, the env-doc, the regression sweep, and the final cross-phase contract test matrix.

**Files touched (this sub-stage):**
- `README.md` (or `docs/`) — modified.
- Any existing test file (re-run only — no source change).

### Tasks

| # | Task | Depends On | Acceptance |
|---|------|------------|------------|
| 1.E.1 | Verify dead-default WARN is captured (deferred from 1.B.10; can also live in 1.B itself). Single loud `log.Printf` on boot. | 1.B.10 | boot test with default config + 1 enabled model → WARN in logs. |
| 1.E.2 | Add a one-time decorator-meta log on successful boot: `log.Printf("[cache] models decorator enabled (positive=60s negative=60s stalenessCap=24h, %d models, %d credentials)", len, len)`. | 1.B.5 | informational log line on every successful boot. |
| 1.E.3 | Update `README.md` env-vars table (or `docs/environment.md` if it exists) with: `CACHE_LAYER_ENABLED` (default true), the behavior summary ("set false to disable caching; pre-MVP behavior"), TTLs, and outage handling. | 1.C.1 | grep `CACHE_LAYER_ENABLED` in docs returns the documented entry. |
| 1.E.4 | Run the entire `pkg/proxy` test suite with `CACHE_LAYER_ENABLED=true` (decorator active) — must be green. This is the "no behavior change when DB healthy" acceptance. | 1.C, 1.D | `go test -race ./pkg/proxy/...` clean. |
| 1.E.5 | Run `go test -race ./pkg/store/database/...` and `./pkg/modelscache/...` and `./pkg/auth/...` and `./pkg/ui/...` and `./cmd/...` clean. | all prior | entire repo `-race` clean across touched packages. |
| 1.E.6 | Final smoke: drive the proxy in `httptest`-style, fire a few real requests, verify zero 5xx for known-model warm-cache + 503 only for unknown-model cold-cache. | 1.C, 1.D | smoke test green. |
| 1.E.7 | Update `.agents/shared/context.md` (project state) with the cache-layer landing status. | all prior | context file current. |
| 1.E.8 | Final cross-phase contract test suite: `pkg/modelscache/contract_test.go` and `pkg/modelscache/outage_test.go` plus the two `handler_*_failsafe_test.go` files all green. | 1.B, 1.D | success criteria A-O all green. |

### Coupling
- **Loose with:** all prior sub-stages.

### Risks
- 🟢 Docs drift if env-vars table isn't updated.

### Exit criterion
All 15 success criteria A-O from `plan-overview.md` green; release ready for review.

---

## Cross-Phase Dependency Graph

```
1A (store strict)  ──► 1B (modelscache)  ──► 1C (wiring)  ──► 1E (tests/docs)
       │                      │                    │
       │                      ▼                    │
       └──► 1D (proxy 503) ◄──────────────────────┘
                                (interface-driven)
```

- **1A must complete first** (consumed by 1B).
- **1B → 1C** sequential (wiring needs both wrappers).
- **1D** can run parallel to **1B** after **1B.1** ships (`ConfigStoreHealth` interface declared) — adding the type-assertion in `handler_functions.go` and `handler_anthropic.go` does not depend on the wrappers existing yet.
- **1E** last, after all source changes merged.

---

## Implementation Order (Recommended)

For a single developer, the natural order is **1A.1 → 1A.2 → 1A.3 → 1A.4 → 1A.5 → 1A.6 → 1B.1-5 → 1D.1 → 1D.2 → 1B.6 → 1B.7 → 1B.8 → 1B.9 → 1B.10 → 1C.x → 1E.x**. With two developers, partition as: **(Stream A)** 1A → 1B.1-5 → 1B.6-10 / **(Stream B)** 1A → 1D in parallel; reconverge on 1C then 1E.

---

## Implementation-Time Notes (W4, W6, W8, W9 — folded in)

These are **NOT** new sub-stages and do not change the sub-stage structure. They are implementation-guidance items for the assigned developer, captured from the amendment pass so they survive the sub-stage PR review:

- **[W4] `idToHash` lifecycle:**
  1. Populate on `ValidateToken` misses (cold read fills both the LRU entry and the `idToHash` index — the read-path is the only way to learn about existing tokens at runtime for callers who haven't called CreateToken).
  2. Bind size: a separate `idToHash` is bounded at `defaultIDIndexCap = 50_000` (configurable constant in `pkg/modelscache/tokens.go`); eviction follows LRU via the LRU's own tracking OR a separate `container/list`. Reason: a hostile admin who deletes a token but whose `idToHash` entry lingers would leak memory; the cap bounds that risk.
  3. Delete in `DeleteToken`: `delete(idToHash, id)` AFTER fan-out clearing of positive+negative entries.
  4. Eviction policy: when the LRU evicts a positive entry, the `idToHash` entry for that token's ID is NOT deleted (the token might be re-fetched soon; deletion happens at the LRU eviction guard if we want stricter memory bounds, but skipped for MVP to keep semantics simple — flagged for Phase 2 if needed).

- **[W6] Negative-cache masks outage-created tokens ≤60s:**
  - **Accepted residual:** if a new token is created via API during a DB outage (impossible — the DB-backed `CreateToken` would fail), or if the cache's outer `CreateToken` returns success but the cache is fed by stale data, a token created DURING a recent outage may be invisible to `ValidateToken` for up to 60s until the negative entry expires. Documented in README as: "Recovery from negative-cache lockout: 60s worst case (matches positive TTL)." Flagged for Phase-2 tightening (e.g., shorter negative TTL on cache layer).

- **[W8] Recovery convergence ≤120s worst case:**
  - The 60s reconciler tick interval means the worst-case time-to-refresh-after-DB-returns is `60s + scan_time` (with the 5s strict-fill timeout per W9). For acceptance criterion H (renamed from "≤60s" to "≤120s worst case"), the plan asserts refresh occurs within two reconciler ticks. Test adjustment: the outage simulation's `Recover_DBTicksAndRefresh` assertion uses `clock.Advance(120s)` followed by `cachedSnapshot.healthy==true` after the next reconciler tick.

- **[W9] `context.WithTimeout` 2–5s on strict fills:**
  - Every strict-fill call from the decorator (`GetModelsStrict`, `GetEnabledModelsStrict`, `GetCredentialsStrict`, `ResolveInternalConfigWithAffinityCached`, `GetModelStrict`, `GetCredentialStrict`) wraps the call site with `ctx, cancel := context.WithTimeout(parent, 5*time.Second)` so a slow DB cannot stall the cache layer beyond its tight-budget. Reconciler tick passes its own cancellable context (`Stop()` cancels it before the scan completes; see W3). Boot priming uses a non-cancellable 5s timeout on the parent context.

---

## Acceptance Summary (this phase)

This phase, when complete, satisfies all 15 success criteria **A through O** listed in `plan-overview.md` because each is constructed by at least one sub-stage above:

- A, B, C (amended to: "authorization at t>60s into the outage via stale-tier per C2"), D, H (amended to ≤120s per W8), K, N → 1B outage simulation + 1C wiring + 1E contract tests.
- E, O → 1B write-through tests + 1A store-strict contract tests.
- F, G → 1C env-flag path + 1E regression sweep.
- I → 1B deep-copy tests.
- J, L → 1E `-race` clean.
- M → 1B health-capability interface + 1D integration.
- (N, see K) → 1E dead-default WARN.
- **W1 (DetectProvider-under-DB-down)** → 1B.5 cache-served legacy resolver + dedicated test in `pkg/modelscache/models_test.go`.
- **C2 (token stale-tier)** → 1B.8 three-tier state machine + dedicated Rows A2/B2/C2 in `contract_test.go`.
- **C3 (GetCredentialsStrict + abort-on-error swap)** → 1A.6 strict list read + 1B.6 boot abort + 1B.7 reconciler abort-on-error test.
- **W2 (teardown ordering)** → 1C.4 explicit LIFO order with nil-guards; `Stop()` no-op on `CachedTokenStore`.
- **W3 (Stop cancels context first)** → 1B.7 cancellation contract + reconciler test.
- **W4–W9** → captured as implementation notes above; no new sub-stages.
