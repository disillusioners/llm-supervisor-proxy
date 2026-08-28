# Decisions: DB Caching / Resilience Layer MVP

This file records the binding decisions that shaped `plan-overview.md` and `phase1-plan.md`. Each decision is labeled **[LEADER]** (resolved by leader before plan creation), **[PLANNER]** (made by this planner during plan creation), **[ARCH]** (inherited from architecture-recommendation.md as binding council consensus), or **[AMENDMENT]** (added during the amendment pass on review).

**Amendment-pass anchor:** planner received an APPROVED-WITH-NOTES amendment verdict after the first plan delivery; this revised version applies C1 (additive-only strict methods), C2 (token stale-tier), C3 (`GetCredentialsStrict` + abort-on-empty-swap), and the W1–W9 implementation-time items as noted inline below.

---

## [LEADER-1] MVP = Phase 0 + Phase 1 shipped together as ONE release

**Source:** leader dispatch instructions.

**Driving evidence (from arch §8.1):**
> "Auth cache re-sequenced from 'hardening' to same-release-as-MVP (D2 proof above)."
>
> "Without [the auth cache], ~100% of authenticated internal requests 401 during an outage — the ≥1h-zero-failure goal is unmet."

**Council D2 proof (arch §1.4):** with the models cache fixed, internal models resolve correctly → `requiresInternalAuth` = true → `authenticate` (handler.go:301-303) conflates the DB error into "invalid" → **~100% of authenticated internal requests 401 during an outage** without token caching.

**Plan reflection:**
- `plan-overview.md` Phase column shows Phase 0+1 collapsed into a single release unit; explicit "all five sub-stages together as ONE release" statement.
- `phase1-plan.md` title: "Phase 1: DB Cache MVP (Architecture Phase 0 + Phase 1, single release)".
- `phase1-plan.md` Sub-Stage 1C wires the auth decorator at `cmd/main.go:139` in the same release as the models decorator at `:90`.
- All 15 success criteria A-O are constructed from one combined phase.

**Reversibility:** low — splitting MVP into two releases would require re-doing the outage-simulation acceptance (criteria A-D) twice. Not contemplated.

---

## [LEADER-2] Phase 2 deferred (explicit out-of-scope)

**Source:** leader dispatch instructions.

**Items deferred** (per dispatch + arch §7.2):
- Usage replay ring (~10k bounded UPSERT) — T3 degradable in MVP; 54 lost increments in the 2026-08-27 incident, non-fatal.
- Cache metrics (hit/miss/stale/negative counters via events bus or `/fe/api`).
- `[]byte`-with-explicit-wipe hardening of credential API keys (Go string immutability bounds MVP).
- Store-side publish-on-mutator for credential events (`AddCredential`/`UpdateCredential`); decorator write-through already covers correctness.
- Multi-replica cross-process invalidation (architecture flip condition; single-binary deployment today).
- Removal of the dead-default `localhost:4001` literal itself (separate workstream; tripwire warning is the MVP touch).
- Env-configurable TTLs (defaults are hardcoded first; env knobs in Phase 2).
- Reconciler backoff / bounded-retry on DB-down (today is straight-line 60s reconcile).
- pprof endpoint hardening for credential residency (verified: no pprof routes exist today; flagged as ops note).
- APST disable / PG hot standby / alerting / pod-recreation alerts / PG 17→18 upgrade ops / pve3 NVMe remediation (DevOps incident follow-ups, not proxy-side).

**Plan reflection:**
- `plan-overview.md` "Out of Scope" section lists all of the above with justifications for each boundary.
- `decisions.md` (this file) explicitly tags Phase 2 as deferred to a separate future plan.

**Note in plan (already in `plan-overview.md`):** "write-through invalidation already covers credential mutations for MVP correctness." This is the rationale for deferring store-side credential-mutator publishes without leaving a correctness gap.

---

## [LEADER-3] Auth cache positive TTL = 60s (positive AND negative caching)

**Source:** leader dispatch instructions.

**Overrides:** arch Q1 (which offered 30s/5m options with no default).

**Rationale (per leader):** "revocation SLO ≤60s acceptable; in-memory lookup → no perf concern". Positive and negative caching both enabled.

**Plan reflection:**
- `phase1-plan.md` Sub-Stage 1B.8 implementation: `ValidateToken` clamps expiry to `min(token.ExpiresAt, now+positiveTTL)`; negative entries stored with `negativeTTL` (also 60s).
- `phase1-plan.md` Sub-Stage 1C.3 env-flag path uses hardcoded `PositiveTTL: 60s, NegativeTTL: 60s` constants.
- `plan-overview.md` "Open Questions" row Q1 reflects leader resolution: "Auth positive TTL = 60s (revocation SLO ≤60s acceptable)".

**Reversibility:** medium — TTLs become env-configurable in Phase 2 per [LEADER-2] deferred list. Production-side the cap is 60s until then.

---

## [LEADER-4] Boot behavior: keep today's `log.Fatalf` fail-fast

**Source:** leader dispatch instructions.

**Overrides:** arch Q4 (which offered bounded-retry as a planner option).

**Rationale (per leader):** "today's behavior" preserved. No boot retry loop in MVP.

**Plan reflection:**
- `phase1-plan.md` Sub-Stage 1B.6: `WrapModels` returns `(*CachedModelsConfig, error)`; boot priming failure propagates an error.
- `phase1-plan.md` Sub-Stage 1C.2: `if primErr != nil { log.Fatalf("Failed to prime models cache: %v", primErr) }` — mirror's today's `cmd/main.go:55-58` fail-fast on `database.InitializeAll`.
- `plan-overview.md` "Open Questions" row Q4 reflects leader resolution.
- Bounded-retry deferred to Phase 2 per [LEADER-2].

**Reversibility:** low during MVP — explicit fail-fast is the operational contract; changing to bounded-retry changes deployment runbooks.

---

## [LEADER-5] `CACHE_LAYER_ENABLED` default ON

**Source:** leader dispatch instructions.

**Confirms:** arch §7.5 recommendation.

**Rationale (per leader):** env flag exists purely as instant rollback lever. Document in README/env docs.

**Plan reflection:**
- `phase1-plan.md` Sub-Stage 1C.1: `cacheLayerEnabled := os.Getenv("CACHE_LAYER_ENABLED") != "false"` (default ON; explicit `=false` disables).
- `phase1-plan.md` Sub-Stage 1E.3: README/docs updated.
- `plan-overview.md` "Risks" row 10 covers playbook docs-drift mitigation.

**Reversibility:** trivial — env=true ↔ env=false flip on restart.

---

## [LEADER-6] Apply all four §8 planner corrections

**Source:** leader dispatch instructions.

**The four corrections (from arch §8, verbatim):**
1. **Auth cache re-sequenced** from "hardening" to **same-release-as-MVP** — see [LEADER-1] above.
2. **"Zero store changes" is false as stated** — the additive strict methods are required (the interface has no error returns; council D1).
3. **The resolver variant is required** — without it the hottest internal path stays DB-bound.
4. **~2-site `pkg/proxy` touch** for fail-fast 503 is required.

**Plan reflection:**
- Correction (2) → `phase1-plan.md` Sub-Stage 1A (additive strict methods `GetModelStrict`, `GetModelByNameStrict`, `GetCredentialStrict`, plus additive strict list-trio `GetModelsStrict`/`GetEnabledModelsStrict`/`GetCredentialsStrict` (per C1 — supersedes the originally-proposed `(nil, error)` signature change for `GetModels`/`GetEnabledModels`)). All inside `pkg/store/database/store.go`.
- Correction (3) → `phase1-plan.md` Sub-Stage 1A task 1.A.4 (`ResolveInternalConfigWithAffinityCached(cached, conversationKey, credLookup) (ResolvedCredential, bool)`); consumed by Sub-Stage 1B.5 (`CachedModelsConfig.ResolveInternalConfigWithAffinity` overridden to read cache maps only and call the strict resolver variant).
- Correction (4) → `phase1-plan.md` Sub-Stage 1D (2 sites — `handler_functions.go:87-103` and `handler_anthropic.go:154-170` — fail-fast 503 only when `nil + !Healthy()`).
- Correction (1) → already covered by [LEADER-1] / Sub-Stage 1B.8 / Sub-Stage 1C.3.

**Reversibility:** medium — corrections are necessary for the architecture to be implementable; reverting any of them would re-introduce the original incident class.

---

## [PLANNER-A] Single-developer sequence picked over parallelization by default

**Decision maker:** planner (in `phase1-plan.md` "Implementation Order").

**Rationale:** the natural order (1A → 1B → 1D-in-parallel-after-1.B.1 → 1C → 1E) is unambiguous; parallelization only saves ~1.5 days even when [LEADER-2-deferred] Phase 2 work is excluded. Single-developer gives simpler code review and easier revert.

**Plan reflection:** `phase1-plan.md` "Implementation Order (Recommended)" suggests the natural single-dev path, then notes the two-developer split for completeness. No code or file ordering change.

**Reversibility:** trivial — switching to two-developer is a process decision, not a code one.

---

## [PLANNER-B] Standard library `container/list` for token LRU (no third-party deps)

**Decision maker:** planner.

**Rationale:** arch Q5 caps tokens at 10k; stdlib `container/list` is sufficient and zero-dep. Third-party LRU (e.g., hashicorp/golang-lru) would add a dependency the codebase doesn't currently carry — out of scope for MVP per [LEADER-2].

**Plan reflection:**
- `phase1-plan.md` Sub-Stage 1B.4: "bounded-LRU keyed by `[32]byte` using `container/list` from stdlib". *(Implementation-time correction N1: the key type is whatever `auth.HashToken` returns — a hex `string` (`pkg/auth/token.go:103`) — so the shipped LRU and `idToHash` are keyed by that hex string; the `[32]byte` mention above is the raw digest the hex encodes.)*
- `phase1-plan.md` "Risks" row 11 notes Q5 remains open for ops measurement; revisit only if Q5 reveals larger cardinality.

**Reversibility:** trivial — swap to a third-party LRU later if needed; the package-private `lru` API is the only thing that would change.

---

## [PLANNER-C] Additive-only strict methods (C1 amendment — supersedes the prior concrete-signature-change proposal)

**Decision maker:** planner (reconsidered after C1 amendment in code review).

**Prior ruling (now superseded):** "Change `GetModels`/`GetEnabledModels` signatures to surface infra errors." This was found to be self-contradictory in Go's type system because `models.ModelsConfigInterface` declares `GetModels() []ModelConfig` with no error return — changing the concrete `*ModelsManager` would break interface satisfaction at `main.go:90` (proven by `cmd/main.go:90`'s direct concrete-type assignment), and would also break ~12 concrete-type call sites in `pkg/store/database/database_test.go` (verified at HEAD `68dbe63`: lines 215, 233, 265, 276, 305, ...).

**New ruling (C1 amendment):** introduce ADDITIVE `GetModelsStrict(ctx) ([]models.ModelConfig, error)`, `GetEnabledModelsStrict(ctx) ([]models.ModelConfig, error)`, and `GetCredentialsStrict(ctx) ([]models.CredentialConfig, error)` as NEW methods on `*ModelsManager`. Legacy methods keep their no-error shape and their silent-`[]` behavior — which is unreachable in prod once the decorator is wired because the prod callers (`pkg/ui/server.go:508/:807`, `pkg/proxy/handler.go:242`) reach the model/credential list only via the wrapped interface, and the decorator's own `GetModels`/`GetEnabledModels` overrides return cached data unconditionally.

**Rationale:** the silent-`[]` on `*ModelsManager.GetModels`/`GetEnabledModels` is unreachable in prod (no production caller reaches `*ModelsManager` directly after wrapping). The reconcile/boot-prime path uses the strict variants to get error visibility AND to detect transient-empty-vs-legit-empty. The risk that "interface satisfaction silently breaks if someone re-uses the strict methods on the legacy signature" is moot because strict methods return `(values, err)` — making them impossible to satisfy on a no-error interface without a future interface change.

**Plan reflection:**
- `phase1-plan.md` Sub-Stage 1A task 1.A.5 (additive trio).
- `phase1-plan.md` Sub-Stage 1A task 1.A.6 (`GetCredentialsStrict` per C3).
- `phase1-plan.md` Sub-Stage 1B.5 + 1B.6 + 1B.7 (the decorator consumes the trio for boot-prime + reconciler abort-on-empty per C3).
- `plan-overview.md` "In Scope" bullet on the trio; cross-phase risk-statement (C1 amendment eliminates the original concern).

**Reversibility:** trivial — additive methods are easy to remove.

---

## [PLANNER-I] Infra-error classifier for `CachedTokenStore` stale-tier fallback (per C2)

**Decision maker:** planner.

**Purpose:** distinguish "infrastructure cannot answer right now" from "verdict is no" — the former triggers the stale-tier fallback, the latter must remain fail-closed.

**Whitelist of infra-class errors (any of these `true` ⇒ `isInfraError(err)` returns true):**
- `errors.As(err, &netErr)` AND `netErr.Op` is non-empty (covers `*net.OpError` for connection refused, DNS, etc.).
- `errors.Is(err, context.DeadlineExceeded)` — proxy timeout from `WrapTokens` callers.
- `errors.Is(err, context.Canceled)` — only when the cancellation originates from inside the cache (e.g., shutdown); callers pass `ctx.Canceled` through as a non-infra error.
- `errors.Is(err, driver.ErrBadConn)` — `database/sql`'s standard "connection in a bad state" sentinel.
- String match: error message contains `"connection refused"`, `"no such host"`, `"i/o timeout"`, `"connection reset"`. (String match is a last-resort because errors are opaque; documented as a fragility in `pkg/modelscache/health.go`.)

**Non-infra (verdict-class) errors explicitly do NOT trigger stale fallback:**
- `auth.ErrTokenNotFound` (positive-not-found, equivalent to `sql.ErrNoRows`).
- `auth.ErrInvalidTokenFormat` (pre-DB format check; never infrastructure).
- Any `nil` error → positive token.

**Decision rationale:** restricts the stale tier to a well-defined, auditable set of error classes. String-match is documented as fragile because Go's `net` package does not always wrap in `*net.OpError` (e.g., during driver-level timeouts). The list is short on purpose.

**Plan reflection:**
- `phase1-plan.md` Sub-Stage 1B.8 (three-tier state machine; the `isInfraError` helper).
- `pkg/modelscache/tokens_test.go` tests: `TestCachedTokenStore_StaleTierHitsOnInfraError` (positive), `TestCachedTokenStore_StaleTierDoesNotHitOnNegativeVerdict` (fail-closed preserved).

**Reversibility:** medium — swapping the whitelist is a small compile-rebuild; expanding it requires a test update too.

---

## [PLANNER-J] Decrypt-failure on credential scan: ABORT swap (per C3)

**Decision maker:** planner.

**Decision rule:** if `GetCredentialsStrict` (or any list scan during boot-prime/reconciler) encounters a row whose stored `APIKey` fails `crypto.Decrypt`, the method returns `(partial-with-valid-rows, ErrDecryptionFailureInScan)` (per-entry skip-and-flag rather than hard fail). The decorator's boot-prime / reconciler treats ANY non-nil error as authoritative "DO NOT SWAP" — set `healthy=false`, log WARN with credential ID omitted, preserve the prior snapshot.

**Decision rationale (vs. partial-swap):** partial-swap would be more available (some credentials served while others 503) but risks masking real configuration corruption. A bad `APIKey` row is almost certainly a deployment issue (wrong encryption key, partial migration, manual SQL edit) — not a transient outage. Treat it conservatively.

**Implementation rule:** the per-entry skip happens in `*ModelsManager.scanCredentials` (or its strict twin) so the cache layer is shielded from implementation details. Cache sees only the `(partial, error)` shape and applies the universal "error ⇒ no swap" rule (same as infra errors).

**Plan reflection:**
- `phase1-plan.md` Sub-Stage 1A.6 (`GetCredentialsStrict` returns the dual-shape).
- `phase1-plan.md` Sub-Stage 1A.5 model/credential scan analogue (same abort-on-error rule).
- `phase1-plan.md` Sub-Stage 1B.6/1B.7 (boot-prime and reconciler both abort on any error).
- `pkg/store/database/store_strict_test.go` `TestGetCredentialsStrict_DecryptFailureAbortsScan` etc.

**Reversibility:** low during MVP — flipping to partial-swap is a one-line change in the cache layer; flagged for Phase 2 if needed.

---

## [PLANNER-K] Strict-fill `context.WithTimeout` budget = 5s (per W9)

**Decision maker:** planner.

**Rationale:** every strict-fill call from the decorator wraps the inner source with `ctx, cancel := context.WithTimeout(parent, 5*time.Second)` so a slow DB cannot stall the cache layer beyond a tight budget. 2s minimum observed in `pkg/auth` cold-path tests; 5s balances "no panic on slow DB" against "caller doesn't wait 30s". Reconciler tick passes its own cancellable context (Signals `stopCh` cancels the in-flight scan, see PLANNER-Stop-cancel contract below).

**Scope of application:** `GetModelsStrict`, `GetEnabledModelsStrict`, `GetCredentialsStrict`, `ResolveInternalConfigWithAffinityCached`, `GetModelStrict`, `GetCredentialStrict`, `GetModelByNameStrict`. UI-only passes-through (`ListTokens`, `GetTokenByID`) do NOT get a cache-side timeout because they pass through to the inner store's own context.

**Boot priming:** uses a non-cancellable 5s timeout on the parent context (boot should fail-fast rather than hang).

**Plan reflection:**
- `phase1-plan.md` Sub-Stage 1A.5/1A.6 (each strict method accepts a `ctx` argument; decorator passes a 5s-budgeted context).
- `phase1-plan.md` Sub-Stage 1B.7 (reconciler tick passes its own cancellable context).
- `phase1-plan.md` Implementation-time note [W9].

**Reversibility:** trivial — constant change in one place; downstream consumers untouched.

---

## [PLANNER-Stop-cancel] Reconciler `Stop()` cancellation contract (per W3)

**Decision maker:** planner.

**Contract:**
1. `Stop()` signals `stopCh` and immediately calls `cancel()` on the in-flight scan context (if one is running).
2. The scan goroutine's outer `select` watches both `stopCh` AND `<-ctx.Done()`; whichever fires first exits the loop.
3. If the in-flight scan ignores cancellation (e.g., the DB driver is stuck), the goroutine waits ~5s, then abandons with a WARN. The scan-in-flight MAY continue to completion (its result is discarded by the closed `stopCh`-observed read).
4. After `Stop()` returns, the cache's `Healthy()` may briefly report stale; next constructor invocation re-primes.

**Rationale:** correctness — `Stop()` MUST cancel outstanding work, not just signal; otherwise the goroutine can run indefinitely past process shutdown and produce log spam. The ~5s budget is bounded by PLANNER-K's read-timeout ceiling for SQL drivers and prevents `main()` from hanging past `srv.Shutdown`.

**Plan reflection:**
- `phase1-plan.md` Sub-Stage 1B.7 (cancellation contract embedded in 1.B.7 acceptance).
- `pkg/modelscache/models_test.go` `TestCachedModelsConfig_Reconciler_StopCancelsInflightScan` (NEW per W3).

**Reversibility:** trivial — the contract is enforceable as long as the goroutine watches both signals.

---

---

## [PLANNER-D] Hard-coded default UpstreamURL literal in the dead-default tripwire

**Decision maker:** planner.

**Rationale:** arch §4 tripwire recipe says "loud WARN when `UpstreamURL` is still the `http://localhost:4001` default". Literal-match is the simplest implementation; smarter heuristics (e.g., any localhost URL on a non-dev build) are out of scope for MVP and would risk false positives.

**Plan reflection:**
- `phase1-plan.md` Sub-Stage 1B.10: emits WARN if `cfg.UpstreamURL == "http://localhost:4001"` literal-match.
- Smarter detection deferred.

**Reversibility:** trivial — adjust the literal in one place.

---

## [PLANNER-E] Dead-default WARN is fired during boot priming only, not every read

**Decision maker:** planner.

**Rationale:** emitting the WARN every read would flood logs at request scale (hundreds per second). Boot-only is sufficient: the WARN is for operators during deployment, not runtime consumers.

**Plan reflection:** `phase1-plan.md` Sub-Stage 1B.10 specifies "Single emit, idempotent, runs once during boot (not on every read)".

**Reversibility:** trivial — move into a reconciler tick if operators want periodic reminders.

---

## [PLANNER-F] Token-cache `idToHash` index is added to fan-out `DeleteToken` correctly

**Decision maker:** planner.

**Rationale:** `auth.DeleteToken(ctx, id)` only receives the ID. To clear both positive and negative entries for that token's hash, the cache must maintain an `id → hash` reverse index. Without it, a `DeleteToken` would leave stale token entries (positive OR negative — both keyed by hash) in the LRU until their TTL expires.

**Plan reflection:** `phase1-plan.md` Sub-Stage 1B.8 implements `idToHash map[string][32]byte` and the fan-out delete. *(Shipped as `idToHash map[string]string` per correction N1 — hash keys are the hex string from `auth.HashToken`.)*

**Reversibility:** low (LRU eviction falls back to TTL if the index misses — slight entry-leak window; acceptable residual for MVP).

---

## [PLANNER-G] Decorator imports `pkg/store/database` only for the `Dialect` constant

**Decision maker:** planner.

**Rationale:** the decorator needs to know whether the underlying store is PostgreSQL vs SQLite so the same cache works in both modes. Using the existing `database.Dialect` constant avoids re-implementing dialect detection. The dependency is one-way (decorator → store) and there is no cycle because `pkg/modelscache` is a leaf package.

**Plan reflection:** `phase1-plan.md` Sub-Stage 1B preamble documents the import convention.

**Reversibility:** low (would require a new dialect-detection helper if `pkg/modelscache` ever splits off from `pkg/store/database`).

---

## [PLANNER-H] Stale cap = 24h default (per arch §2, both councilors converged)

**Decision maker:** planner (inherited from arch).

**Rationale:** arch §2 explicitly states: "Stale window — unbounded during outage, with a configurable hard cap, default 24h. Justification against the ≥1h goal: real outages don't schedule themselves; model settings change ~weekly, so 24h-stale config is almost certainly more correct than 503ing working traffic; the cap exists only to prevent silent permanent divergence. Both councilors independently converged on 24h."

**Plan reflection:** `phase1-plan.md` Sub-Stage 1B.7: "Default `stalenessCap = 24h`"; Sub-Stage 1C.2 passes `StalenessCap: 24h`.

**Reversibility:** trivial — env-configurable in Phase 2 per [LEADER-2].

---

## [ARCH-1] (Council) Option A+ — Decorator at the seam (Option A with three material corrections)

**Source:** arch §0, §1.1, §6 unanimous verdict.

**Verbatim:** "Recommended: Option A+ — caching decorators at the composition-root seams, plus additive-only strict read methods in `store/database`, plus a ~2-site fail-fast touch in `pkg/proxy`, with the auth-token cache shipping in the SAME release as the models cache."

**Three corrections to "pure A"** (anchored in arch §1.1):
1. No error returns on the interface → additive strict methods required.
2. Resolver re-reads → resolver variant required.
3. Fail-fast needs a signal → `ConfigStoreHealth` interface required.

**Plan reflection:** every component named in the plan comes from arch §1.2 (component layout); every method signature comes from arch §1.3 (data structures) and §1.4 (cached columns); every failure mode is from arch §2 matrix; every invalidation rule is from arch §3; every security control is from arch §5.

**Reversibility:** low — flip conditions (multi-replica, multi-thousand-model scale) are documented as out-of-band (arch §6).

---

## [ARCH-2] (Council) ConfigStoreHealth interface boundary

**Source:** arch §1.1 correction (3), §1.2 component layout.

**Verbatim:** "a minimal health capability `ConfigStoreHealth{ Healthy() bool }` exposed by the decorator and type-asserted at those two sites."

**Plan reflection:**
- `phase1-plan.md` Sub-Stage 1B.1 declares `pkg/modelscache/health.go` with the interface.
- `phase1-plan.md` Sub-Stage 1D uses it at the two boundary sites via type-assertion only (no concrete-type assertions downstream — verified).
- `plan-overview.md` "Critical shared contract — single source of truth" pins it.

**Reversibility:** trivial — interface is package-private to `pkg/modelscache`; consumers type-assert with the standard `if h, ok := ...; ok` pattern.

---

## [ARCH-3] (Council) Decryption-failure hardening — never serve ciphertext

**Source:** arch §5, §2 matrix row 5.

**Verbatim (arch §5):** "Decrypt-failure hazard — fixed, not inherited: current `GetCredential` WARNs and serves ciphertext as the APIKey (store.go:1530-1538). The cache negative-caches decrypt failures and never serves ciphertext (matrix row 5 → 503)."

**Plan reflection:**
- `phase1-plan.md` Sub-Stage 1A task 1.A.3 adds `GetCredentialStrict` returning `(nil, ErrDecryptionFailed)` on decrypt failure.
- `phase1-plan.md` Sub-Stage 1B.5 implements the cache's `GetCredential` to consume that error → return `ErrCredentialMissing`.
- `plan-overview.md` "Success Criteria" row N asserts this at runtime.

**Reversibility:** low — reversing this would re-arm the original ciphertext-as-APIKey hazard.

---

## [ARCH-4] (Council) Synchronous write-through as primary invalidation

**Source:** arch §3.

**Verbatim (arch §3):** "Primary: synchronous write-through in the decorators. Mutators delegate to the inner store; on success, mutate/invalidate the cache under the write lock. ... Event bus vs direct call: ... Ruling (council D3): the bus is nicely decoupled but must **never be load-bearing for correctness**; synchronous write-through is the primary mechanism."

**Plan reflection:**
- `phase1-plan.md` Sub-Stage 1B.5 mutators write through.
- `phase1-plan.md` Sub-Stage 1B.7 reconciler is the safety net.
- `plan-overview.md` "Risks" row 5 captures the bus-drop acceptance.

**Reversibility:** low — bus-async invalidation is documented as a Phase-2 candidate but is NOT a correctness requirement in MVP.

---
