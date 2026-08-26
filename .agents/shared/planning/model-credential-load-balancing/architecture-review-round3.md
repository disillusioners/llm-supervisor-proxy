# Architecture Review — Round 3 Stress-Test: Rate-Limit Credential Failover

**Date:** 2026-08-25
**Plan under review:** `.agents/shared/planning/model-credential-load-balancing/` — Round-3 amendment (decisions.md §F.1–F.8 + base-drift tables, technical-analysis.md Round-3 regions, phase2 Tasks 11–15, phase3 Tasks 17–24, phase5 Tasks 23–34, all working-tree uncommitted @ 8f67bdf)
**Code verified against:** `feature/model-credential-load-balancing` @ HEAD `8f67bdf`
**Method:** 3 parallel analyst workers (data-flow-design / resilience-design / trade-off-analysis), each verifying claims against actual code with file:line quotes; architect-aggregated. All three confirmed skill injection (no skill-bank misses). REVIEW ONLY — no plan or source files were modified.

---

## Executive Verdict

**AMEND-AND-ADOPT.**

The Round-3 design direction is validated on every axis that matters: precedence concept (failover before model-switching at the correct interception point), engine ownership (decisively justified), classifier placement (dead code confirmed, seam exact), cooldown design (contention acceptable, soonest-expiry correct for 0-of-N), budget rule (loop-free by construction), and base-drift table (fully accurate). However, the stress-test surfaced **one blocking structural defect** — the race coordinator's spawn-window gate silently caps credential failover at ONE attempt and strands the promised model-fallback fall-through — plus two blocking API ambiguities in `ExcludeAndReselect` and two guard mechanisms that cite nonexistent state. None of these rework the design; all are amendable within the existing ruling structure (R3-1..R3-8 semantics unchanged).

---

## Verification Matrix (Focus Areas 1–8)

| # | Focus area | Verdict | Blocking? |
|---|---|---|---|
| 1 | R3-3 precedence ordering | **PARTIAL** — interception point real and correctly identified; one structural gate defeats the promised semantics | 🔴 YES (B1) |
| 2 | R3-2 ExcludeAndReselect API | **AMEND** — sound core; concurrency idempotency + third-mode API are unspecified | 🔴 YES (B2, B3) |
| 3 | R3-4 cooldown design | **CONFIRMED (with pins)** — contention, janitor, soonest-expiry, TTL interaction all hold | No |
| 4 | R3-1 classifier | **CONFIRMED (with amendments)** — dead code, seam, MiniMax coverage verified | No |
| 5 | R3-6 streaming guard | **PARTIAL** — enforceable on all three paths, but two of three cited guard mechanisms don't exist as described | 🟡 amendment required |
| 6 | Base-drift corrections | **CONFIRMED** — every checked row accurate | No |
| 7 | Interpretation calls (a)–(d) | (a) APPROVE, (b) APPROVE, (c) CORRECT premise, (d) APPROVE | No |
| 8 | 5-axis: engine-owned vs no-engine | **CONFIRMED engine-owned** — weighted 3.80 vs 1.95 (hybrid 3.55), High confidence | No |

---

## 1. R3-3 Precedence (highest risk) — PARTIAL, blocking

**What's confirmed.**
- Case 1 exists at `race_coordinator.go:350-362` (`latestReq.IsDone() && latestReq.GetError() != nil` → `shouldSpawn=true; triggerInfo{trigger: triggerMainError}`), falling through to `c.spawn(modelTypeFallback, ...)` at `:409`.
- `spawn()` at `:189-215`: `modelTypeFallback` → `modelID = c.models[1]` at **exactly line 215**. `c.models []string` (`:102`) is populated from `rc.modelList = [resolvedModel.ID] + FallbackChain` (`handler_functions.go:97-98` via `newRaceCoordinatorWithEvents` at `handler.go:830`). So `c.models[1]` = first fallback model — plan's reading is exact.
- Case 1 is the **sole** reactor to a failed attempt; there is no competing model-switch in the coordinator. `ProviderError` propagates **unwrapped** (`race_executor.go:366-371` non-stream, `:437-442` stream — raw `return err` → `req.MarkFailed(err)` at `:598`), so an `errors.As`-based `IsRateLimitError` at Case 1 is feasible.
- **Sibling hooks are real and pre-model-switch:** `ultimatemodel/handler_internal.go:36/46` (`executeInternal` / `ResolveInternalConfig`) and `proxy/internal_handler.go:109/111` (`HandleRequest` / resolve seam; chain `handler_anthropic.go:348 → :455 → :478 → :488`). The ultimate path has no fallback machinery at all; the Anthropic retry at the seam precedes the `/v1/messages` model loop (`handler_anthropic.go:197`, advance at ~247-265).
- **Winner-selected:** `manage()` returns once `c.winner != nil` (`:330-336`) — no spawns of any kind post-winner; not a gap. **Non-stream:** Case 1 fires identically (same `MarkFailed` path); `GetFinalErrorInfo` (`:782`, called `handler.go:936`) is the later terminal-shaping exit, correctly preserved per R3-4.

**🔴 B1 — Spawn-window gate defeats R3-3 AND R3-5 (blocking).**
The spawn block is gated by `race_coordinator.go:338`: `if c.winner == nil && len(c.requests) < len(c.models)`, and the all-failed check at `:420-421` (`len(c.requests) >= len(c.models)`) closes `done`. Credential-failover attempts, as specified, consume `c.requests` slots like any other attempt:
- User's core scenario — 3 MiniMax credentials on ONE model, **no fallback chain** (`len(c.models) == 1`): after the main attempt (`requests=1 ≥ models=1`), the gate blocks the spawn entirely. **The feature silently does nothing in the exact case it was built for.**
- 2-entry model list: exactly ONE cred-failover spawn fits; when it 429s, the all-failed check fires → terminal `"All models rate limited"` — `c.models[1]` is never tried despite F.3's "falls through to existing model-fallback spawn unchanged."
- R3-5's budget (`≤ len(credentials)−1` failover attempts) is unreachable in any configuration.

**Required amendment (Task 18):** exempt `modelTypeCredFailover` attempts from the spawn-window count and the all-failed accounting — e.g., maintain a separate `len(c.requests)` accounting for model-vs-credential attempts, or extend the cap by `len(modelCfg.Credentials)−1` when the main model is internal+multi-cred, and make the all-failed condition require "all fallback models tried OR budget exhausted with no healthy credential." The terminal message at `:860-868` must not fire while an untried fallback model or untried credential remains.

**Sub-findings (amend, non-blocking):**
- **(iii) Case-1 latest-only inspection:** Case 1 reads only `c.requests[len-1]`; an earlier attempt's 429 (e.g., main idle-stalled while a spawned attempt fails first) is never adjudicated for credential failover. Document as accepted behavior or adjudicate in spawn order.
- **Case 2 idle bypass (by design):** idle timeout (`:364-395`) spawns `modelTypeSecond` + `modelTypeFallback` together (`:399-405`) with no error classification — model-switching can precede the stalled credential's eventual 429. Plan should record this as an explicit non-goal.
- **modelTypeSecond re-key:** secondary attempts share `c.models[0]`'s modelID but resolve the primary credential (`race_executor.go:112`); a 429 there re-keys failover to the primary credential. Coherent but unspecified — pin it.
- **UI statuses:** `GetRequestStatuses` (`:745-758`) switches only on main/second/fallback — the new model type is silently dropped. Trivial addition to Task 18.
- **Naming:** three identifiers for one constant across docs — `credential_failover` (F.3 tree), `modelTypeCredentialFailover` (F.3 rationale / technical-analysis), `modelTypeCredFailover` (phase3 Task 18). Pin ONE canonical Go identifier.
- Citation nits: the function is `manage()` (`:252`), not `monitorLoop` (no such symbol exists); the spawn function is `spawn()` (`:189`), not `spawnAttempt`.

## 2. R3-2 ExcludeAndReselect — AMEND

**Confirmed sound:** atomicity of mark-cooling + reselect + rebind as one critical section under E-1 (readers see pre- or post-rebind, never torn); rebind-not-delete preserves #10 sliding-TTL; tried-set in the REQUEST context is the right scope (per-request lifetime; engine stays per-conversation stateless about retries); old binding after cooldown expiry is **intended** behavior — the conversation stays rebound until its own idle TTL, with no thundering re-bind (fresh picks are weighted-random, not "switch back").

**🔴 B2 — Concurrent same-conversation double-failover is unspecified.**
Two concurrent requests on the same conversation both 429 on cred A: first `ExcludeAndReselect` rebinds A→B and cools A; the second call (no precondition in the spec) re-marks A cooling (idempotent) and re-selects the next healthy after A — which can be **C** while request 1 is mid-attempt on B. The binding flaps; subsequent turns follow C even though B succeeded.
**Required pin:** `ExcludeAndReselect` takes a rebind-only-if-current-cred-matches precondition — if `bindings[convKey].credentialID != excludedCredID`, it is a no-op returning the current binding (`ok=true`, unchanged credential). This makes concurrent double-failover idempotent-by-construction.

**🔴 B3 — `ok=false` vs F.4 all-cooling soonest-expiry: the API cannot express the third mode.**
F.2 says `ok=false` when every other credential is cooling → caller falls through to model-fallback. F.4/precedence-tree says all-cooling → pick soonest-expiry, WARN, single attempt, then fall through. The signature `(credentialID string, ok bool)` cannot distinguish "no healthy pick, go to fallback" from "here is a soonest-expiry pick, one attempt only" — and without that distinction the caller cannot enforce F.4's single-attempt-then-fall-through (the engine doesn't know the request's tried-set, so a second call after the soonest-expiry pick fails would return yet another all-cooling pick, looping the fallback chain to the terminal error incorrectly).
**Required pin:** make the return mode-aware — recommended shape: `ExcludeAndReselect(...) (credentialID string, mode ReselectMode)` with `mode ∈ {ReselectHealthy, ReselectSoonestExpiry, ReselectNone}`; the engine emits the WARN on `ReselectSoonestExpiry`; callers treat `ReselectNone` as fall-through-to-model-fallback and `ReselectSoonestExpiry` as single-attempt-then-fall-through. (Acceptable alternative: keep `ok bool` for healthy picks and add a separate `Engine.PickSoonestExpiring` consulted only on `ok=false` — two calls, same semantics.)

**🟡 Pin — tried-set/failoverAttempts concurrency:** `requestContext` fields mutated from coordinator goroutines must be pinned to single-goroutine mutation (the monitor/`manage()` loop, serialized by the coordinator mutex) with a code comment; otherwise parallel attempts (RaceMaxParallel > 1) race the map/int.

## 3. R3-4 Cooldown — CONFIRMED (with pins)

- **Contention:** critical sections are sub-microsecond map ops; renormalization is O(k), k ≤ MaxCredentialsPerModel = 16; janitor takes per-model write locks one-at-a-time under outer RLock (reads proceed). Acceptable at 1–5 creds/model and moderate RPS; happy path is RLock (write only on first bind per 24h-TTL conversation). No pathological case at stated load.
- **Janitor correctness:** expired-between-check-and-sweep entries linger ≤5 min but are behaviorally inert — the selector's read-time `cooldownUntil > now` check is authoritative. Sweep-vs-`ExcludeAndReselect` serialize under the per-model write lock. Add a Phase-5 test asserting inert-lingering.
- **Soonest-expiry (re-validated vs fail-fast / error-passthrough):** correct choice for the user's HA intent; fail-fast forfeits service when retry-after is 5s; block-until-expiry is semantically wrong for HTTP and stampede-prone. The alternatives table holds.
- **🟡 Pin — "all-cooling" = 0-of-N ONLY.** 1-of-N healthy (the common case in a 3-cred setup with 2 rate-limited) already takes the healthy-skip path; F.4's prose reads as if it covered that case. Clarify, and note the 1-of-N case concentrates load on the last healthy credential — operator doc, not code.
- **Sliding-TTL interaction:** a binding can point at a credential that enters cooldown via ANOTHER request's 429 (stale affinity), but the µs-scale window between credA's first 429 and the rebind makes serving-429s-to-new-turns unrealistic. No flapping on expiry (binding simply outlives the cooldown). Coherent with #10.

## 4. R3-1 Classifier — CONFIRMED (with amendments)

- `parseRetryAfter` is **dead code** — `grep -rn parseRetryAfter pkg/ --include="*.go"` returns exactly the declaration at `openai.go:593`; all other hits are plan files. Conflict-free wiring confirmed.
- `handleError` (`openai.go:234-279`): unmarshal target `apiErr.Error.{Message,Type,Code}` (238-244); `ProviderError` constructed at 261-266 — `RetryAfter` appends cleanly at ~266.
- **MiniMax coverage is genuine:** `factory.go:45-50` maps `ProviderMiniMax` → `NewOpenAIProvider(apiKey, "https://api.minimax.io/v1")`; the OpenAI-style envelope is the wire shape for ALL seven active brands (anthropic/gemini "not implemented" at `factory.go:28-33`). Codebase precedent: `pkg/proxy/translator/errors.go:13,70,129` (`rate_limit_error`) + fixture `translator_test.go:603-636`.
- **Composes with `IsRetryable`:** no conflict — 429+5xx are retryable, only 429/rate-limit-body is rate-limit; the two classifiers are orthogonal and both remain useful.
- **Amendments:** (i) pin the classification **vocabulary table** — `models/errors.go:7,17` says `rate_limit` (no suffix), translator says `rate_limit_error`; the classifier must match both by substring-on-type + equality-on-code, stated as ONE table in the plan; (ii) add Phase-5 matrix row `503 / {"error":{"code":"rate_limit"}}` → `true` (body-code check when status ≠ 429 is specified in technical-analysis.md:858-872 but untested); (iii) document the missing-`Retry-After` flow explicitly (header absent on most brands in practice → `RetryAfter=0` → caller applies 60s default); (iv) mark HTTP-200-with-embedded-error **out of scope** for R3-1 (would need mid-stream parsing).

## 5. R3-6 Streaming Guard — PARTIAL (mechanism corrections)

The constraint (failover only pre-first-content-byte) is enforceable on all three paths — but two of the three mechanisms the plan names do not exist:

- **Race path — 🟡 amend F.3/F.6.** "Nothing flushes pre-winner" is **false for streaming**: `handler.go:783-820` writes SSE headers + `": connected\n\n"` and starts the heartbeat BEFORE `coordinator.Start()` (`rc.headersSent = true` at `:795`). True invariant: **no CONTENT flushes pre-winner** — winner requires `req.IsCompleted() && req.GetError() == nil` (`:299`); `streamResult` (call `:852`, def `:960`) / `handleNonStreamResult` (call `:879`, def `:1327`) run only post-`WaitForWinner`. **`winner.chunksFlushedToClient` and `latestReq.bytesFlushedToClient()` are invented — no such field/method exists at HEAD.** The expressible guard is `c.winner == nil` (for non-stream, nothing is written at all); F.3's `latestReq.status != statusStreaming` is vacuous (failed attempts are `statusFailed`).
- **Ultimate-internal — 🟡 amend F.6/Task 21.** The cited `headersSent` marker is **unusable**: `Execute` sets `*headersSent = true` at `ultimatemodel/handler.go:608-609` and starts the SSE heartbeat (`:643`, writes `": heartbeat\n\n"` during execution) BEFORE `executeInternal`. Correct guard: retry only when the returned error is the **initial provider-call `*ProviderError`** (type assertion) — structurally sound because initial-call errors return before any content write (`handler_internal.go:155-157` non-stream, `:253-255` stream) and mid-stream failures surface as SSE error events.
- **`/v1/messages` internal — confirmed directly enforceable:** client `w` untouched until `handleAnthropicInternal{NonStream,Stream}Response` run after `HandleRequest` returns; recorder decouples (`handler_anthropic.go:470` created, `:496` `recorder.Code != http.StatusOK`); `arc.headersSent` set only at real first writes (`:588`, `:654`, `:1065`). Citation nit: this path's header write is `handler_anthropic.go:648-654`, not `adapter_anthropic.go:170-176` (that is `SetStreamHeaders`).

## 6. Base-Drift Corrections — CONFIRMED

Every spot-checked row is accurate @ 8f67bdf: D3 probe at `ultimatemodel/handler.go:625-636` (comment 625-630, `modelCfg.CredentialID != ""` at 631, `GetCredential` 632, provider lowercase 633-635) — +337 drift real; `internal_handler.go` `NewInternalHandler` :48 / `HandleRequest` :109 / resolve seam :111 ✓; `race_executor.go` 73/112/137/151 all exact ✓; `handler.go` 352/401/635 all exact ✓; supporting rows (`providers/interface.go:155-161,168-170`, `openai.go:234-279`, `parseRetryAfter` dead, `models/errors.go:7,17`) ✓. The drift table is trustworthy.

## 7. Interpretation Call Rulings (a–d)

- **(a) Task-numbering layout — APPROVED.** Per-phase task numbering with cross-file references is the established convention across Rounds 1–2; Round 3 follows it. No collision introduced (phase2 Tasks 11–15, phase3 17–24, phase5 23–34 live in separate files).
- **(b) Cite-refresh parentheticals (`was X @ fea5874`) — APPROVED.** The convention preserves the audit trail, and the underlying drift values verified accurate (§6 above). Keep it.
- **(c) `--fail-429-once` E2E mock flag — PREMISE CORRECTED, intent approved.** The claimed precedent does not exist: `grep` for `hit-count-file`/`hitCount`/`hit_count` across `test/ cmd/ pkg/` returns **zero hits**, and `test/test_mock_credential_lb.sh` **does not exist** (it is the planned Round-1 E2E script, referenced in decisions.md §E:1033 but never written). The REAL precedent is `test/mock_llm_race.go`'s additive flag pattern (`-idle-pause`/`-deadline-interval`/`-slow-start` at lines 36-38, `-port` default 4001 at :42) plus its existing 429 simulation (lines 91-97 — prompt-content-keyed, no `Retry-After` header, no hit counter, single-instance). The E2E counterpart therefore needs **new mock capability** (hit counter + per-credential identity + `Retry-After` emission), not just a new flag. Amendment: Phase 5 Task 23 must either (1) build the per-credential mock as part of the Round-1 E2E script work, or (2) defer the E2E counterpart to a follow-up and keep the handler-level Task 23 test as the gate. Recommend (1); accept (2) as fallback.
- **(d) Budget-not-clock governs re-429 — APPROVED.** Within one request, the tried-set (budget) prevents re-attempting any credential — no waiting on cooldown mid-request; across requests, cooldown governs selection. No plan text conflates the two (F.4/F.5 wording is coherent). R3-5 loop-free-by-construction also confirmed (exclusion ∩ tried-set, bounded by `len(credentials)`).

## 8. Approach Comparison — Engine-Owned vs No-Engine (Focus 8)

| Approach | Complexity | Scalability | Maintainability | Risk | Cost | Recommendation |
|---|---|---|---|---|---|---|
| **A: engine-owned (as planned)** | Med — `ExcludeAndReselect` + cooldown map + janitor coupling + stats/event + 3 hooks + 12 tests, but reuses E-1, janitor, dead `parseRetryAfter`, existing unmarshal | High — shared cooldown + skip-on-selection across all 3 paths automatically; rebind preserves prompt-cache under concurrency | High — ONE source of truth in `pkg/credentiallb`; fixes land once; #10 invariant reused | Low — rebind is load-bearing for prompt-cache continuity (§A #16f); bounded state; soonest-expiry satisfies HA without unbounded latency | Med — phase2 T11-15 + phase3 T17-22 + phase5 T23-34; minimal net-new infra | **ADOPT — weighted 3.80** |
| **B: no-engine retry loop** | Low-Med — no new engine API, but still needs classifier + shared cooldown to satisfy HA, converging toward A's complexity | Low — without shared cooldown every request re-tries the cooling credential; with it, invariants duplicated outside the engine | Low — failover invariants replicated ×3 call sites, no shared contract, guaranteed drift | Med-High — no rebind ⇒ conversations return to the 429ing credential after cooldown; fails the user's verbatim HA requirement | Med — ~3 retry loops + per-site cooldown + equivalent tests | Reject — 1.95 |
| **Hybrid: engine-cooldown + call-site retry, no rebind** | Low-Med | High | Med | Med — no rebind ⇒ post-cooldown return to the degraded credential; defeats cache continuity for and after the cooldown window | Low-Med | Reject — ~3.55; only wins if rebind is judged unimportant, contradicting pinned R3-2 |

**Decisive test:** the same conversation's next turn — B (pure) deterministically re-selects the still-429ing credential via the unchanged binding; B-with-shared-cooldown re-imports engine invariants outside the engine. Either way B fails the HA requirement; A satisfies it in one critical section. Confidence **High**; flips only if prompt-cache continuity AND cross-request HA are both judged unimportant — contradicted by the plan's pinned rationale and the Claude-Code-style client workload.

---

## Consolidated Required Amendments (blocking adoption as-written)

1. **B1 (Task 18):** amend the spawn-window gate (`race_coordinator.go:338`) and all-failed check (`:420-421`) so `modelTypeCredFailover` attempts do not consume model-attempt slots — otherwise the user's core scenario (multi-cred model, no fallback chain) gets ZERO failovers and the 2-model case gets ONE with no model-fallback fall-through.
2. **B2 (F.2):** pin `ExcludeAndReselect`'s rebind-only-if-current-cred-matches precondition (idempotent under concurrent same-conversation 429s; no binding flap).
3. **B3 (F.2/F.4):** make the return mode-aware (`Healthy` / `SoonestExpiry` / `None`) so F.4's single-attempt-then-fall-through is caller-enforceable and the WARN is engine-side.
4. **F.3/F.6 + Task 23:** replace invented/nonexistent guard state (`chunksFlushedToClient`, `status != statusStreaming`, ultimate `headersSent`) with the real guards — race: `c.winner == nil` (+ "no content pre-winner" wording); ultimate-internal: initial-call `*ProviderError` type assertion; `/v1/messages`: recorder/`arc.headersSent` as already specified (fix the header-write cite to `handler_anthropic.go:648-654`).
5. **F.7 + Task 16/22:** add an explicit in/out row for the **anthropic-passthrough internal sub-branch** (`handler_anthropic.go:297-345`) — internal models whose credential provider is `"anthropic"` bypass `doAnthropicInternalRequest` entirely; unlisted in the path-scope table.
6. **Task 17:** pin the classifier vocabulary table (`rate_limit` AND `rate_limit_error`); add the 503-with-rate-limit-body matrix row; document absent-`Retry-After` → 60s default; mark 200-with-error out of scope.
7. **F.8:** pin `EngineStats.Cooldowns` to **gauge semantics** (current map size, recomputed per janitor sweep); delete the increment-per-removal prose at decisions.md:1291-1293.
8. **Phase 5 Task 23:** correct the E2E premise (no existing `--hit-count-file` precedent; script itself is planned-but-unwritten) — build the per-credential mock in the Round-1 E2E work or defer the E2E counterpart.

**Non-blocking recommendations:** canonical naming for the failover model-type constant; `GetRequestStatuses` row for the new model type; document Case-2 idle bypass and Case-1 latest-only inspection as explicit non-goals/behaviors; operator-facing note on slow re-distribution after cooldown expiry; tried-set single-goroutine-mutation comment; Phase-5 test for inert-lingering expired cooldown entries; disambiguate `streamResult` call-site (:852) vs definition (:960) in F.6.

## Gaps

None. All three dispatched workers completed with skill injection confirmed on the first line of each report (`data-flow-design`, `resilience-design`, `trade-off-analysis`). No re-dispatches were needed.

## Conclusion

The Round-3 amendment's architecture is sound and its evidence base is unusually accurate (base-drift table 100% on spot-check; hook sites real; dead-code claim true). The blocking findings are specification-level omissions concentrated in one file's control flow (`race_coordinator.go` gates) and one API (`ExcludeAndReselect`), not conceptual flaws. **Amend per the eight items above, then adopt.** Estimated amendment effort: plan-document edits only (Task 18 acceptance criteria, F.2/F.4/F.6/F.7/F.8 prose, phase5 Task 23 wording) — no ruling changes to R3-1..R3-8 semantics.
