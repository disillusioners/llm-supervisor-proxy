# Plan: Ultimate Model Trigger Schedule (5/10/20/30/40)

Status: PLANNED (not implemented)
Date: 2026-08-27
Depends on: `docs/ultimate-model-design.md` (current design)

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
  special case `maxRetries == 0` = unlimited.
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

### Decisions (confirmed)

| Question | Decision |
|---|---|
| 40th request behavior | **Inject ultimate** — 40 is the final injection; 41+ errors |
| Schedule configurable? | **Hardcoded** constants in `pkg/ultimatemodel`; no config/UI fields |
| Counter basis | **Total requests** — attempt 1 = first time the hash is ever seen; the "5th time" = 5th total call |
| Old `max_retries` config/env | **Drop** — remove field, default, validation, env override, UI input |

### Semantics preserved from current design

- Ultimate **success** → `hashCache.Remove(hash)` (hash + counter cleared);
  same content starts fresh.
- Ultimate **failure** → counter kept; hash stays; subsequent requests flow
  normally until the next milestone.
- `X-Force-Ultimate-Model` (ForceTrigger) → triggers immediately, **never
  increments the counter** (side-band), still stores hash if new. No
  exhaustion applies.
- Per-model `exclude_from_ultimate_switching` gate: exclusion still wins over
  both injection and the exhausted error — ordering in
  `pkg/proxy/handler.go:618` is preserved (excluded models never receive the
  exhausted error, matching today's nesting).
- Ultimate model not found in DB → `hashCache.Remove(hash)` + config error
  (unchanged).
- Hash cache reset when `ultimate_model.model_id` config changes (unchanged).

## 4. Changes by file

### 4.1 `pkg/ultimatemodel/hash_cache.go`

- Replace `StoreAndCheck` + `IncrementAndCheckRetry` with a single atomic
  method:

  ```go
  // RecordAttempt records the hash (inserting on first sight, with circular-
  // buffer eviction cleanup of the counter map) and increments the attempt
  // counter on EVERY call. Returns the total attempt count for this hash.
  func (c *HashCache) RecordAttempt(hash string) int
  ```

  - First sight: insert into circular buffer (evicting the oldest hash and
    deleting its counter entry when full, as today) **and** set counter to 1.
  - Subsequent: increment only.
  - Single mutex critical section — same concurrency contract as
    `StoreAndCheck` today.
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
  force  → Triggered=true immediately, NO RecordAttempt, no exhaustion
  else:
      count := RecordAttempt(hash)
      if count > maxAttempts  → Triggered=true, RetryExhausted=true
      else if count in {5,10,20,30,40} → Triggered=true
      else → Triggered=false
  ```

  Note: exhausted result keeps `Triggered=true` so the proxy's control flow
  (exhausted check inside the triggered branch, `handler.go:628`) stays
  structurally identical — only field names and wording change.
- `ShouldTriggerResult` rename fields: `CurrentRetry` → `CurrentAttempt`,
  `MaxRetries` → `MaxAttempts`. Keep `Triggered`, `Hash`, `RetryExhausted`.
- `MarkFailed` (deprecated shim): switch to `RecordAttempt`; keep behavior of
  returning the hash, or delete if unreferenced (verify with grep).
- `SendRetryExhaustedError`: rename params to `currentAttempt`/`maxAttempts`;
  new message wording: `"Request attempt limit exceeded (attempt %d of %d
  max). Hash: %s..."`. **Keep** the wire error type string
  `ultimate_model_retry_exhausted` and JSON shape (`type`/`code`/`message`/
  `hash`) unchanged for client compatibility.
- Update `Execute` comment block (failure keeps counter; success removes hash).

### 4.3 `pkg/proxy/handler.go` (lines 594–730)

- Structure unchanged. Mechanical updates:
  - `result.CurrentRetry` → `result.CurrentAttempt`,
    `result.MaxRetries` → `result.MaxAttempts` in logs, `reqLog.Error`,
    event payloads (`ultimate_model_retry_exhausted`,
    `ultimate_model_triggered`), and the `SendRetryExhaustedError` call.
  - Wording: "Ultimate model retry limit exceeded" → "Request attempt limit
    exceeded (attempt N/40)".
  - Event payload keys `current_retry` / `max_retries` are **kept** (frontend
    `EventLog.tsx` and any external consumers read them); only values change
    meaning (N = total attempts, 40). Documented in §6.

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
- `EventLog.tsx` (line 97): update exhausted message text to reflect attempts
  (e.g. `Ultimate model attempts exhausted: N/40`); keep reading
  `current_retry`/`max_retries` event keys.

### 4.6 Tests

New unit tests:

- Trigger schedule: calls 1–4 normal; **5** triggers; 6–9 normal; **10, 20,
  30, 40** trigger; **41** exhausted; 42+ exhausted.
- Ultimate failure at 5 → calls 6–9 normal, 10 triggers.
- Ultimate success at 5 → hash removed, counter resets (next call = attempt 1,
  next trigger at 5 again).
- ForceTrigger: triggers on first call, does not increment (subsequent normal
  call counts as attempt 1).
- Excluded model at attempt 41+: no exhausted error, normal flow (ordering).
- Concurrency: N goroutines with same hash → exactly one lands on each
  milestone count (mirror of existing race test).

Updates to existing tests:

- `pkg/ultimatemodel/handler_test.go` — rewrite retry-limit cases
  (`TestShouldTrigger_MaxRetriesZero`, counter=2/3 fixtures → schedule-based).
- `pkg/ultimatemodel/hash_cache_test.go` — `StoreAndCheck`/retry-counter
  tests → `RecordAttempt` tests (insert-on-first, increment, eviction
  cleanup, Remove/Reset clear counter).
- `pkg/proxy/handler_integration_test.go` (lines 577–586, 1398–1407) —
  currently expect trigger on 2nd call; change to 5th. Line 1184
  `ULTIMATE_MODEL_MAX_RETRIES=2` env → remove.
- `pkg/proxy/handler_test.go` (lines 2084–2285) — `MaxRetries = 3` fixtures →
  schedule-based (loop to 5 or use ForceTrigger).
- `pkg/proxy/handler_capture_persistence*.go` (4 files) — remove inert
  `t.Setenv("ULTIMATE_MODEL_MAX_RETRIES", "0")` lines; tests rely on
  ForceTrigger which is unaffected.
- `pkg/store/database/database_test.go` (line 1522) + `mock_store_test.go`
  (line 176) — drop `MaxRetries` roundtrip assertions.
- `pkg/models/config_ultimate_test.go` — update for removed field.
- `test/` e2e harnesses using `ULTIMATE_MODEL_MAX_RETRIES` /
  `MaxRetries` (e.g. `test/e2e_minimax_reasoning/harness_test.go` env
  comment "MAX_RETRIES=0") — remove/env-cleanup.
- `test/packs/ultimatemodel_unit_test.sh` — rewrite for new schedule if it
  asserts trigger-on-3rd.

### 4.7 Docs

- `docs/ultimate-model-design.md`:
  - §2.2 interface listing (`StoreAndCheck` → `RecordAttempt`).
  - §3.2 `ShouldTrigger` contract + new schedule table.
  - §3.4.3 retry-counter interaction paragraph → attempt-counter semantics.
  - §8 edge-case table (add "attempt 41+", adjust "duplicate detected").
- `AGENTS.md`:
  - Ultimate Model Configuration env table: remove
    `ULTIMATE_MODEL_MAX_RETRIES` row.
  - Add one line describing the 5/10/20/30/40 schedule + 40 limit.
- This plan doc stays as the change record.

## 5. Edge cases

| Scenario | Behavior |
|---|---|
| Concurrent identical requests | Single mutex op in `RecordAttempt` assigns distinct attempt numbers; only the request landing exactly on a milestone triggers (same guarantee as today's atomic `StoreAndCheck`) |
| Hash evicted from circular buffer (100 slots) | Counter entry deleted with eviction → counting restarts at 1 (existing eviction-cleanup behavior; reaching 40 attempts without eviction is the norm, eviction implies ≥100 distinct hashes since) |
| Server restart | Hash cache ephemeral → counters reset (unchanged) |
| Ultimate model ID changed | Cache reset incl. counters (unchanged) |
| Stale config JSON with `max_retries` key | Silently ignored by unmarshaler |
| `ULTIMATE_MODEL_MAX_RETRIES` env still set in deployments | Inert (override code removed); no error |
| Force header on every request | Every request goes ultimate; counter never increments (unchanged) |
| Excluded model duplicates | Exclusion gate precedes exhausted check → never blocked (unchanged ordering) |

## 6. Compatibility notes

- Wire error type `ultimate_model_retry_exhausted`, error JSON shape, and the
  SSE-vs-JSON dual format are unchanged — existing clients keep working.
- Event names (`ultimate_model_triggered`, `ultimate_model_retry_exhausted`)
  and payload keys (`current_retry`, `max_retries`) unchanged; values now
  mean total attempts (e.g. `41` / `40`). Frontend copy updated accordingly.
- No DB migration (config is a JSON blob; request-log columns untouched).

## 7. Implementation order

1. `hash_cache.go`: `RecordAttempt` + counter rename; update `hash_cache_test.go`.
2. `handler.go`: constants + `shouldTriggerInternal` rewrite + result-field
   renames + error-message wording; rewrite `handler_test.go` schedule cases.
3. `pkg/proxy/handler.go`: mechanical renames/wording; fix
   `handler_integration_test.go` + `handler_test.go` fixtures.
4. `pkg/config/config.go`: remove `MaxRetries` everywhere; fix
   `config`/`store`/`models` tests.
5. Capture-persistence + e2e test env cleanups.
6. Frontend: `types.ts`, `SettingsPage.tsx`, `ProxySettings.tsx`,
   `EventLog.tsx`.
7. Docs: `ultimate-model-design.md`, `AGENTS.md`.

## 8. Verification

```bash
go test ./pkg/ultimatemodel/... ./pkg/config/... ./pkg/proxy/... ./pkg/store/... ./pkg/models/...
make test                       # full suite
go build ./...                  # embed check
cd pkg/ui/frontend && npm run build   # TS type check
```

Manual smoke (optional): point proxy at mock upstream, send same request 5×,
verify 5th carries `X-LLMProxy-Ultimate-Model`; send to 41×, verify exhausted
error on 41st.
