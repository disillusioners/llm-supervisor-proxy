# Plan: Ultimate Model Trigger Schedule (5/10/20/30/40)

Status: PLANNED (not implemented) — rev. 2, post-review revision
Date: 2026-08-27
Depends on: `docs/ultimate-model-design.md` (current design)
Rev. 2: folds in the two post-review CRITICAL fixes (MarkFailed double-count,
force-trigger storage contradiction), four confirmed leader decisions, and
review warnings/suggestions. All open questions are DECIDED — none remain.

## 1. Motivation

Today the ultimate model fires on the **3rd request** with the same message
hash and, after `max_retries` (default 2) ultimate attempts, the hash becomes
permanently blocked with an un-retryable error. This escalates too early and
gives up too early: normal models rarely get a fair chance, and a transient
ultimate-model failure burns the last attempt.

New behavior spreads escalation across the request lifetime:

- Give normal models **4 attempts** before first escalation (was 2).
- Re-escalate at fixed milestones: **5th, 10th, 20th, 30th, and 40th** request
  with the same hash.
- **40 is the absolute limit**: requests 41+ with the same hash receive an
  un-retryable error (reusing the existing SSE/JSON exhausted-error mechanism).

## 2. Current behavior (as implemented)

| Request # with same hash | Counter | Result |
|---|---|---|
| 1 | 0 (hash stored) | Normal flow |
| 2 | 1 | Normal flow (gate `newCount >= 2`) |
| 3 | 2 | **Ultimate model triggered** |
| 4+ | 3+ | `RetryExhausted` (3 > `MaxRetries`=2) → un-retryable error |

Key code:

- `pkg/ultimatemodel/hash_cache.go` — `StoreAndCheck` (duplicate detection) +
  `IncrementAndCheckRetry` (counts **duplicates only**, i.e. total − 1).
- `pkg/ultimatemodel/handler.go` `shouldTriggerInternal` (lines 88–138) —
  duplicate gate `newCount >= 2`, exhaustion `newCount > maxRetries`,
  special case `maxRetries == 0` = unlimited. Note: force on a **first-sight**
  hash under default `max_retries=2` falls through to the increment path with
  `newCount=1` → `Triggered=false` (force only bypasses the
  first-sight-not-triggered branch, not the duplicate gate).
- `pkg/proxy/handler.go` (lines 594–730) — exhausted check nested *inside*
  `if result.Triggered`; `SendRetryExhaustedError` on exhaustion.
- `pkg/config/config.go` — `UltimateModel.MaxRetries` (default 2, 0 =
  unlimited, cap 100) + env `ULTIMATE_MODEL_MAX_RETRIES`.

## 3. New behavior spec

| Attempt # (total requests with same hash) | Result |
|---|---|
| 1–4 | Normal flow |
| 5 | **Ultimate model** |
| 6–9 | Normal flow |
| 10 | **Ultimate model** |
| 11–19 | Normal flow |
| 20 | **Ultimate model** |
| 21–29 | Normal flow |
| 30 | **Ultimate model** |
| 31–39 | Normal flow |
| 40 | **Ultimate model** (final chance) |
| 41+ | Un-retryable error (`ultimate_model_retry_exhausted`) |

The trigger check runs **once per client request** — at the early-exit gate
(`pkg/proxy/handler.go:597`), before race coordination spawns attempts — so
credential-failover (credFailover) attempts never add to the counter.

### Decisions (confirmed)

| Question | Decision |
|---|---|
| 40th request behavior | **Inject ultimate** — 40 is the final injection; 41+ errors |
| Schedule configurable? | **Hardcoded** constants in `pkg/ultimatemodel`; no config/UI fields |
| Counter basis | **Total requests** — attempt 1 = first time the hash is ever seen; the "5th time" = 5th total call |
| Old `max_retries` config/env | **Drop** — remove field, default, validation, env override, UI input |
| Reset after ultimate success (success → `Remove(hash)` → schedule re-arms) | **CONFIRMED intended** (preserved semantic). Worst case up to ~**8** ultimate invocations per 40 identical requests (see §5) |
| Hash-cache eviction size | **CONFIRMED keep `MaxHash=100` hardcoded** — no config; eviction semantics tightened in §5 |
| Force-trigger counting | **CONFIRMED** — force uses `StoreIfAbsent` (store-if-new, NO counter effect); a force-seen hash counts as attempt 1 on the next normal `RecordAttempt` (see §4.1/§4.2) |
| `MarkFailed` fate | **CONFIRMED deleted entirely** — counting-neutral (attempts are counted at entry by `RecordAttempt`); also removes an unintended cross-token side effect (see §4.2) |

### Semantics preserved from current design

- Ultimate **success** → `hashCache.Remove(hash)` (hash + counter cleared);
  same content starts fresh. **Intended** (leader-confirmed): with success at
  every milestone the schedule re-arms each time, so up to ~8 ultimate
  invocations can occur per 40 identical requests (trigger at 5 → reset →
  trigger at 5 again, ...).
- Ultimate **failure** → counter kept; hash stays; subsequent requests flow
  normally until the next milestone.
- `X-Force-Ultimate-Model` (ForceTrigger) → triggers immediately, **never
  increments the counter**, still stores hash if new — now via
  `StoreIfAbsent` (insert-only, no increment; §4.1). A force-seen hash counts
  as attempt 1 on the next normal `RecordAttempt` call. No exhaustion applies.
  **Explicit behavior change** (leader-confirmed): today, force on a
  first-sight hash under the default `max_retries=2` returns
  `Triggered=false` (and even increments the duplicate counter to 1); the e2e
  harness masks this with `t.Setenv("ULTIMATE_MODEL_MAX_RETRIES", "0")` at
  `test/e2e_minimax_reasoning/harness_test.go:287-292`. Under the new
  schedule, force triggers unconditionally on the first call — a regression
  test covers this without the env workaround (§4.6).
- Per-model `exclude_from_ultimate_switching` gate: exclusion still wins over
  both injection and the exhausted error — ordering in
  `pkg/proxy/handler.go:618` is preserved (excluded models never receive the
  exhausted error, matching today's nesting).
- Ultimate model not found in DB → `hashCache.Remove(hash)` + config error
  (unchanged).
- Hash cache reset when `ultimate_model.model_id` config changes (unchanged) —
  **dead path in production today**: the reset handler `OnConfigChange`
  (`pkg/ultimatemodel/handler.go:196`) asserts
  `event.Data.(map[string]interface{})` with a `"field"` key, while the only
  publisher emits `config.updated` with `Data: m.config` (a `Config` struct,
  `pkg/config/config.go:517-522`), and `OnConfigChange` is never subscribed to
  the event bus. See the annotated row in §5.

## 4. Changes by file

### 4.1 `pkg/ultimatemodel/hash_cache.go`

- Replace `StoreAndCheck` + `IncrementAndCheckRetry` with **two atomic
  primitives** (both single-mutex critical sections — same concurrency
  contract as `StoreAndCheck` today):

  ```go
  // RecordAttempt records the hash (inserting on first sight, with circular-
  // buffer eviction cleanup of the counter map) and increments the attempt
  // counter on EVERY call. Returns the total attempt count for this hash.
  func (c *HashCache) RecordAttempt(hash string) int
  ```

  - First sight: insert into circular buffer (evicting the oldest hash and
    deleting its counter entry when full, as today) **and** set counter to 1.
  - Subsequent: increment only.

  ```go
  // StoreIfAbsent inserts the hash if not already present WITHOUT
  // incrementing the attempt counter. Used only by the force-trigger
  // branch: a force-seen hash counts as attempt 1 on the next normal
  // RecordAttempt call.
  func (c *HashCache) StoreIfAbsent(hash string)
  ```

  - Insert only, no increment — with the same first-sight eviction semantics
    as `RecordAttempt` (evicting the oldest hash + counter entry when full).

- Rename `retryCounter` map → attempt counter (`attemptCounter`). Keep
  `Remove`/`Reset` (also clear the counter entry — already the case), keep
  `Contains`, `GetStats`.
- Delete `IncrementAndCheckRetry`, `GetRetryCount`, `ClearRetryCount` unless
  still referenced after refactor (expected: unused → delete; `GetRetryCount`
  delete only if no external callers — verify with grep during implementation).

### 4.2 `pkg/ultimatemodel/handler.go`

- Add package constants:

  ```go
  // triggerAttempts is the hardcoded escalation schedule: request numbers
  // (total, per hash) on which the ultimate model is injected.
  var triggerAttempts = [5]int{5, 10, 20, 30, 40}

  // maxAttempts is the absolute per-hash request limit; attempts beyond it
  // receive an un-retryable error.
  const maxAttempts = 40
  ```

- Rewrite `shouldTriggerInternal`:

  ```
  cfg empty ModelID or empty messages → not triggered (unchanged)
  force  → StoreIfAbsent(hash); Triggered=true immediately;
           NO RecordAttempt — never increments the counter, no exhaustion.
           (A force-seen hash counts as attempt 1 on the next normal
           RecordAttempt call.)
  else:
      count := RecordAttempt(hash)
      if count > maxAttempts  → Triggered=true, AttemptsExhausted=true
      else if count in {5,10,20,30,40} → Triggered=true
      else → Triggered=false
  ```

  Note: exhausted result keeps `Triggered=true` so the proxy's control flow
  (exhausted check inside the triggered branch, `handler.go:628`) stays
  structurally identical — only field names and wording change.
- `ShouldTriggerResult` rename fields: `CurrentRetry` → `CurrentAttempt`,
  `MaxRetries` → `MaxAttempts`, `RetryExhausted` → `AttemptsExhausted`
  (Go field rename only — the wire JSON error type stays unchanged, §6).
  Keep `Triggered`, `Hash`.
- **Delete `MarkFailed` entirely** (`pkg/ultimatemodel/handler.go:144-152`).
  **NEVER re-wire it to `RecordAttempt`**: `RecordAttempt` already counts at
  entry (insert-on-first-sight), so a failure-branch call on top would
  double-increment — ultimate would fire on the **3rd** request and exhaust
  at ~**21**, contradicting the §3 schedule table. Deletion is
  counting-neutral (by the time a failure branch runs, the entry-time
  `RecordAttempt` has already stored the hash and incremented — failure
  marking adds nothing). Deletion also removes an unintended **cross-token
  side effect**: the proxy's failure branches call `MarkFailed` regardless of
  `rc.ultimateModelEnabled`, so today even ultimate-disabled tokens get their
  hashes stored in the cache on failure. The method IS referenced — remove
  the 4 production call sites (§4.3) and 6 test call sites (§4.6).
- `SendRetryExhaustedError`: rename params to `currentAttempt`/`maxAttempts`;
  new message wording: `"Request attempt limit exceeded (attempt %d of %d
  max). Hash: %s..."`. **Keep** the wire error type string
  `ultimate_model_retry_exhausted` and JSON shape (`type`/`code`/`message`/
  `hash`) unchanged for client compatibility. Also align the **static
  fallback message** inside the marshal-error branch
  (`pkg/ultimatemodel/handler.go:580` — currently `"Ultimate model retry
  limit exceeded"`) with the new attempt wording.
- Update `Execute` comment block (failure keeps counter; success removes hash).

### 4.3 `pkg/proxy/handler.go` (lines 594–730)

- Structure unchanged. Mechanical updates:
  - `result.CurrentRetry` → `result.CurrentAttempt`,
    `result.MaxRetries` → `result.MaxAttempts`,
    `result.RetryExhausted` → `result.AttemptsExhausted` in logs, `reqLog.Error`,
    event payloads (`ultimate_model_retry_exhausted`,
    `ultimate_model_triggered`), and the `SendRetryExhaustedError` call.
  - Wording: "Ultimate model retry limit exceeded" → "Request attempt limit
    exceeded (attempt N/40)".
  - Add a **doc comment at the event publish site**
    (`pkg/proxy/handler.go:654-659`, plus the sibling
    `ultimate_model_triggered` payload at `:684-690`): the
    `current_retry`/`max_retries` payload keys are **kept for frontend
    compatibility** (`EventLog.tsx` and any external consumers read them);
    their values now mean **total attempts** (N/40). Documented in §6.
- Remove **all 4 production `MarkFailed` call sites** (each together with its
  messages-mapping block):
  - `pkg/proxy/handler.go:952` — client-write-failed branch (client/proxy
    dropped the connection mid-stream)
  - `pkg/proxy/handler.go:997` — all-models-failed path
    (`handleRaceFailure(..., "all_models_failed")`)
  - `pkg/proxy/handler.go:1178` — mid-stream-error branch (stream buffer
    closed with error)
  - `pkg/proxy/handler.go:1389` — idle-termination branch

  Rationale: counting-neutral and side-effect-free — see §4.2.

### 4.4 `pkg/config/config.go`

- Remove `MaxRetries` from `UltimateModelConfig` (line 155).
- Remove from `Defaults` (line 200).
- Remove `Validate` checks (lines 261–265).
- Remove env override block `ULTIMATE_MODEL_MAX_RETRIES` (lines 405–408).
- Migration note: previously-saved config JSON blobs contain
  `"max_retries": N`; with the struct field gone, Go's unmarshaler **silently
  ignores** the stale key — no DB migration, no validation rejection (a hard
  rejection would break every existing deployment since `max_retries` was in
  `Defaults`).

### 4.5 Frontend (`pkg/ui/frontend/src`)

- `types.ts`: drop `max_retries` from the `ultimate_model` config interface
  (line 60). **Keep** `max_retries?` in the retry-exhausted **event** payload
  interface (line 297 — it is an event field, not config).
- `SettingsPage.tsx`: remove `ultimateModelMaxRetries` state, sync
  (line 128), and apply payload entry (line 171).
- `components/config/ProxySettings.tsx`: remove the Max Retries input + props.
- `components/EventLog.tsx`:
  - Line 97 — update exhausted message text to reflect attempts (e.g.
    `Ultimate model attempts exhausted: N/40`); keep reading
    `current_retry`/`max_retries` event keys.
  - Line 186 — update the categorical label `ULTIMATE_RETRY_EXHAUSTED`
    wording to attempts (e.g. `ULTIMATE_ATTEMPTS_EXHAUSTED`) so the label
    matches the new semantics; it is a display label only (no wire/format
    coupling).

### 4.6 Tests

New unit tests:

- Trigger schedule: calls 1–4 normal; **5** triggers; 6–9 normal; **10, 20,
  30, 40** trigger; **41** exhausted; 42+ exhausted. Enumerate the
  boundaries explicitly: `4` = no trigger; `5/10/20/30/40` = trigger AND NOT
  exhausted; `41`, `42` = exhausted — **exhaustion is strictly `> 40`**.
- Ultimate failure at 5 → calls 6–9 normal, 10 triggers.
- Ultimate success at 5 → hash removed, counter resets (next call = attempt 1,
  next trigger at 5 again).
- ForceTrigger regression: **force triggers on the first call without the env
  workaround** — today force on first-sight under default `max_retries=2`
  returns `Triggered=false` (the e2e harness masks it via
  `t.Setenv("ULTIMATE_MODEL_MAX_RETRIES", "0")` at
  `test/e2e_minimax_reasoning/harness_test.go:287-292`); the new schedule
  must trigger unconditionally. Also: force does not increment (subsequent
  normal call counts as attempt 1).
- `StoreIfAbsent` unit: inserts without incrementing; force-seen hash → next
  `RecordAttempt` returns 1.
- Eviction: climb a hash to 3 → insert 100 other hashes (evicting it from
  the circular buffer) → the same hash restarts at 1.
- Excluded model at attempt 41+: no exhausted error, normal flow (ordering).
- Concurrency: N goroutines with same hash → the multiset of returned counts
  is **exactly `{1..N}`** (assert set equality, not just mutual exclusion);
  only the request landing exactly on a milestone triggers (mirror of
  existing race test).

Updates to existing tests:

- `pkg/ultimatemodel/handler_test.go` — rewrite retry-limit cases
  (`TestShouldTrigger_MaxRetriesZero`, counter=2/3 fixtures → schedule-based).
- `pkg/ultimatemodel/hash_cache_test.go` — `StoreAndCheck`/retry-counter
  tests → `RecordAttempt` + `StoreIfAbsent` tests (insert-on-first,
  increment, insert-without-increment, eviction cleanup, Remove/Reset clear
  counter).
- Remove **all 6 `MarkFailed` test call sites**:
  `pkg/proxy/handler_integration_test.go:575, :898, :1211, :1399` and
  `pkg/proxy/handler_test.go:2232, :2293` (rev. 1 missed `:898` and
  `:1211`).
- `pkg/proxy/handler_integration_test.go` (lines 577–586, 1398–1407) —
  currently expect trigger on 2nd call; change to 5th. Line 1184
  `ULTIMATE_MODEL_MAX_RETRIES=2` env → remove.
- `pkg/proxy/handler_test.go` — the five `MaxRetries = 3` fixtures at
  `:2084`, `:2099`, `:2160`, `:2221`, `:2285` → schedule-based (loop to 5
  or use ForceTrigger).
- Capture-persistence env cleanups (corrects rev. 1's "4 files" wording):
  `handler_capture_persistence_test.go` — 4× inert
  `t.Setenv("ULTIMATE_MODEL_MAX_RETRIES", "0")` at `:138`, `:290`, `:491`,
  `:641`; plus `handler_capture_persistence_toolcall_test.go:159` — remove;
  these tests rely on ForceTrigger which is unaffected.
- `pkg/store/database/database_test.go` — drop `MaxRetries` roundtrip
  assertions: `loadedCfg.UltimateModel.MaxRetries != 5` check (line 1522) and
  the `"max_retries": 5` JSON literal in the config-blob fixture
  (line 1410); `mock_store_test.go:176` (`MaxRetries: 5`) — drop.
- ~~`pkg/models/config_ultimate_test.go`~~ — **removed from this list**
  (rev. 1 phantom entry): the file contains only
  `ExcludeFromUltimateSwitching` tests and zero `MaxRetries` references —
  no update needed.
- `test/e2e_minimax_reasoning/harness_test.go:287-292` — remove the
  `t.Setenv` env-workaround block (`APPLY_ENV_OVERRIDES` +
  `ULTIMATE_MODEL_MAX_RETRIES=0`) once the force-on-first-call regression
  lands (the explanatory comment at `:287-288` goes with it).
- Mock e2e scripts (export/assert `ULTIMATE_MODEL_MAX_RETRIES` — remove or
  update to the new schedule):
  - `test/test_mock_ultimate_model.sh:127` — `export
    ULTIMATE_MODEL_MAX_RETRIES="2"` → remove.
  - `test/test_mock_ultimate_model.sh:549` — assertion "Retry limit enforced
    after consecutive failures (MAX_RETRIES=2)" → update to attempts
    wording (41st request exhausts).
  - `test/test_mock_minimax_reasoning.sh:127` — same export → remove.
- `test/packs/ultimatemodel_unit_test.sh` — **no change needed** (rev. 1's
  conditional resolved): verified to be a generic wrapper (runs
  `go test ./pkg/ultimatemodel/` with no schedule assertions).

### 4.7 Docs

- `docs/ultimate-model-design.md`:
  - §2.2 interface listing (`StoreAndCheck` → `RecordAttempt` +
    `StoreIfAbsent`).
  - §3.2 `ShouldTrigger` contract + new schedule table.
  - §3.4.3 retry-counter interaction paragraph → attempt-counter semantics.
  - §8 edge-case table (add "attempt 41+", adjust "duplicate detected").
- `AGENTS.md`:
  - Ultimate Model Configuration env table: remove
    `ULTIMATE_MODEL_MAX_RETRIES` row (line 137).
  - Add one line describing the 5/10/20/30/40 schedule + 40 limit.
- `README.md:144` — remove the `ULTIMATE_MODEL_MAX_RETRIES` env-var table
  row (same as the AGENTS.md cleanup).
- This plan doc stays as the change record.

## 5. Edge cases

| Scenario | Behavior |
|---|---|
| Concurrent identical requests | Single mutex op in `RecordAttempt` assigns distinct attempt numbers; only the request landing exactly on a milestone triggers (same guarantee as today's atomic `StoreAndCheck`) |
| Hash evicted from circular buffer (`MaxHash=100`, hardcoded — confirmed) | Counter entry deleted with eviction → counting restarts at 1. Eviction can therefore reset the counter below milestone 5 **whenever ≥100 distinct hashes intervene** (existing eviction-cleanup behavior; reaching 40 attempts without eviction is the norm). Unit test: climb to 3 → evict via 100 other hashes → same hash restarts at 1 (§4.6) |
| Server restart | Hash cache ephemeral → counters reset (unchanged) |
| Ultimate model ID changed | Intended: cache reset incl. counters. **Dead path in production today** — `OnConfigChange` (`pkg/ultimatemodel/handler.go:196`) is never subscribed, and the only publisher emits `config.updated` with `Data: m.config` (a `Config` struct, `pkg/config/config.go:517-522`) which fails its `map[string]interface{}` assertion — the reset never fires. No behavior change claimed by this plan; wiring it up is out of scope |
| Multi-instance deployment (N replicas) | Counters are per-replica in-memory → the schedule becomes **probabilistic across N replicas** (a hash's attempt count is split by whichever replica serves each request). Same limitation as today; merely more visible at 40 requests. Accepted for v1 — Redis-backed counter sync deferred to v2 |
| Stale config JSON with `max_retries` key | Silently ignored by unmarshaler |
| `ULTIMATE_MODEL_MAX_RETRIES` env still set in deployments | Inert (override code removed); no error. Note the env was already gated on `APPLY_ENV_OVERRIDES` (`pkg/config/config.go:360-363`), so it was inert in default deployments even before this change |
| Force header on every request | Every request goes ultimate; counter never increments (`StoreIfAbsent`, insert-only); no exhaustion (unchanged) |
| Ultimate succeeds at a milestone | `Remove(hash)` → schedule re-arms (confirmed intended). With success at every milestone, up to ~**8 ultimate invocations** can occur per 40 identical requests (40/5) |
| Conversation grows between requests | Appending/changing messages changes the full-conversation hash → the schedule restarts at 1 for the new hash. Inherent to today's full-message hashing (`HashMessages` over role+content); unchanged |
| Excluded model duplicates | Exclusion gate precedes exhausted check → never blocked (unchanged ordering) |

## 6. Compatibility notes

- Wire error type `ultimate_model_retry_exhausted`, error JSON shape, and the
  SSE-vs-JSON dual format are unchanged — existing clients keep working. The
  Go field rename `RetryExhausted` → `AttemptsExhausted` is internal only.
- Event names (`ultimate_model_triggered`, `ultimate_model_retry_exhausted`)
  and payload keys (`current_retry`, `max_retries`) unchanged; values now
  mean total attempts (e.g. `41` / `40`). Frontend copy updated accordingly
  (§4.5), and a doc comment at the publish site records the compat intent
  (§4.3).
- **Counter-basis change**: the counter moves from *duplicates only*
  (total − 1) to *total requests with the hash*. Observable in event payload
  values and log/reqLog wording only — no external contract changes.
- No DB migration (config is a JSON blob; request-log columns untouched).
- Footnote: the `ULTIMATE_MODEL_MAX_RETRIES` env override was gated on
  `APPLY_ENV_OVERRIDES` (`pkg/config/config.go:360-363`) — already inert in
  default deployments; removing the override block (§4.4) changes nothing
  for deployments that never opted into env overrides.

## 7. Implementation order

1. `hash_cache.go`: `RecordAttempt` + `StoreIfAbsent` + counter rename;
   update `hash_cache_test.go`.
2. `handler.go`: constants + `shouldTriggerInternal` rewrite + result-field
   renames + `MarkFailed` deletion + error-message wording (incl. the
   `:580` fallback); rewrite `handler_test.go` schedule cases.
3. `pkg/proxy/handler.go`: mechanical renames/wording + publish-site doc
   comment + remove the 4 `MarkFailed` call sites and 6 test-site calls;
   fix `handler_integration_test.go` + `handler_test.go` fixtures.
4. `pkg/config/config.go`: remove `MaxRetries` everywhere; fix
   `config`/`store` tests.
5. Capture-persistence + e2e test env cleanups (incl. the mock scripts,
   §4.6).
6. Frontend: `types.ts`, `SettingsPage.tsx`, `ProxySettings.tsx`,
   `EventLog.tsx`.
7. Docs: `ultimate-model-design.md`, `AGENTS.md`, `README.md`.

## 8. Verification

```bash
go test ./pkg/ultimatemodel/... ./pkg/config/... ./pkg/proxy/... ./pkg/store/... ./pkg/models/...
make test                       # full suite
go build ./...                  # embed check
cd pkg/ui/frontend && npm run build   # TS type check
bash test/test_mock_ultimate_model.sh # e2e gate — MOCK remote only (hard
                                      # requirement: never verified against a
                                      # real ultimate model — consumption is
                                      # costly)
```

Manual smoke (optional): point proxy at mock upstream, send same request 5×,
verify 5th carries `X-LLMProxy-Ultimate-Model`; send to 41×, verify exhausted
error on 41st.
