# Plan: Ultimate Model Trigger Schedule (5/10/20/30/40)

Status: PLANNED (not implemented) — rev. 3, post-approver-revision
Date: 2026-08-26
Depends on: `docs/ultimate-model-design.md` (current design)
Rev. 2: folded in the two post-review CRITICAL fixes (MarkFailed double-count,
force-trigger storage contradiction), four confirmed leader decisions, and
review warnings/suggestions. All open questions were DECIDED — none remained.
Rev. 3 (this revision): completes the MarkFailed deletion sweep across the
test suite (rev. 2 missed 10 sites in `pkg/ultimatemodel/handler_test.go` plus
3 dedicated MarkFailed test functions, causing `go build` / `go test` to
fail after implementation), tightens §4.3/§4.6 enumerations to be exhaustive,
corrects the §4.5 frontend enumeration: BOTH `components/SettingsPage.tsx`
(5 sites — state hook, config-sync setter, payload write, prop pass,
handler binding) AND `components/config/ProxySettings.tsx` (7 sites) must
be edited together; the leader's original `:387` / `:405` citations were
exact (rev. 2's `SettingsPage.tsx:128, :171` were correct anchors in the
real file at `pkg/ui/frontend/src/components/SettingsPage.tsx` — rev. 3
must not regress them), rewords the §3 MaxHash decision (config-read with
default 100, not hardcoded),
fixes the §3 → §5 cross-reference (→ §4.1), pins the SendRetryExhaustedError
lockstep call site (`pkg/proxy/handler.go:662`), adds first-boot
`MaxRetries=0` observability verification, and mandates a
`grep -rn 'MarkFailed'` post-edit sweep as a HARD implementation procedure.
No architectural changes vs. rev. 2 — approver explicitly called this
"a completeness fix, not a redesign."

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
| Hash-cache eviction size | **CONFIRMED leave MaxHash config unchanged** — MaxHash is config-read (`pkg/ultimatemodel/handler.go:60-63`, default 100 when `≤ 0`); the schedule change leaves the field, its default, and its env override (`ULTIMATE_MODEL_MAX_HASH`) untouched. Eviction semantics tightened in §4.1 (counter entry deleted alongside evicted hash) |
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
  the **4 production call sites** (§4.3) and **all 19 test call sites**
  (§4.6: 6 in `pkg/proxy/` + 10 in `pkg/ultimatemodel/handler_test.go`),
  plus the **3 dedicated MarkFailed test functions** in
  `pkg/ultimatemodel/handler_test.go` (`:358, :444, :455`).
  `grep -rn 'MarkFailed' pkg/ test/` MUST return zero matches after the
  sweep (§4.6 HARD procedure).
- `SendRetryExhaustedError`: rename params to `currentAttempt`/`maxAttempts`;
  new message wording: `"Request attempt limit exceeded (attempt %d of %d
  max). Hash: %s..."`. **Keep** the wire error type string
  `ultimate_model_retry_exhausted` and JSON shape (`type`/`code`/`message`/
  `hash`) unchanged for client compatibility. Also align the **static
  fallback message** inside the marshal-error branch
  (`pkg/ultimatemodel/handler.go:580` — currently `"Ultimate model retry
  limit exceeded"`) with the new attempt wording. **Single production
  call site** (must be updated lockstep with the signature rename):
  `pkg/proxy/handler.go:662`
  (`h.ultimateHandler.SendRetryExhaustedError(w, result.Hash,
  result.CurrentRetry, result.MaxRetries, isStream)`). Argument names at
  the call site stay as-is — only the parameter names inside the function
  definition change — but the field values flowing through
  (`result.CurrentRetry` → `result.CurrentAttempt`,
  `result.MaxRetries` → `result.MaxAttempts`) must be renamed per §4.3
  alongside the field renames.
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
- **First-boot observability verification** (mandatory): after removing the
  struct field, the default, the validation, and the env override, run
  `grep -rn 'MaxRetries\|max_retries\|ULTIMATE_MODEL_MAX_RETRIES' pkg/config/`
  and confirm **zero** residual references in `pkg/config/`. Since the field
  is deleted from the struct entirely (not just zeroed), no reads can remain
  — the verification gates the §7 implementation order step 4. If any
  residual reference surfaces (a default, a JSON tag, an env check), the
  removal is incomplete and §7 step 4 is not done.

### 4.5 Frontend (`pkg/ui/frontend/src`)

> **Frontend enumeration corrected (rev. 3).** Rev. 2 cited
> `SettingsPage.tsx:128, :171` and the leader cited `:387, :405`. **Both
> anchors are exact** — `SettingsPage.tsx` lives at
> `pkg/ui/frontend/src/components/SettingsPage.tsx` (the rev. 3 "phantom-file
> correction" blockquote was wrong: the r3 worker only ran
> `ls pkg/ui/frontend/src/` top-level and missed the `components/` subdir).
> The Max-Retries UI therefore spans **two** files that must be edited
> together: `components/SettingsPage.tsx` owns the React state and the
> prop/handler plumbing into `ProxySettings`, and
> `components/config/ProxySettings.tsx` consumes those props and renders
> the input. Removing only one file leaves the other with broken type
> references and `npm run build` fails.

- `types.ts`: drop `max_retries` from the `ultimate_model` config interface
  (line 60). **Keep** `max_retries?` in the retry-exhausted **event** payload
  interface (line 297 — it is an event field, not config).
- `components/SettingsPage.tsx` (the parent state file — real, not
  phantom) — remove every reference to `ultimateModelMaxRetries` and
  `setUltimateModelMaxRetries`:
  - **:90** — `useState` hook:
    `const [ultimateModelMaxRetries, setUltimateModelMaxRetries] = useState(2);`
    — delete the entire line.
  - **:128** — config-sync effect setter:
    `setUltimateModelMaxRetries(config.ultimate_model?.max_retries ?? 2);`
    — delete the entire line (and drop the `// Ultimate model sync` comment
    above it if no other synced fields remain in that block).
  - **:171** — config save payload:
    `max_retries: ultimateModelMaxRetries,` inside the `ultimate_model: { ... }`
    block — delete the line.
  - **:387** — prop pass into `ProxySettings`:
    `ultimateModelMaxRetries={ultimateModelMaxRetries}` — delete the JSX
    attribute (and the surrounding whitespace). The leader's `:387`
    citation was exact.
  - **:405** — handler binding:
    `onUltimateModelMaxRetriesChange={setUltimateModelMaxRetries}` — delete
    the JSX attribute. The leader's `:405` citation was exact.
- `components/config/ProxySettings.tsx` — remove every reference to
  `ultimateModelMaxRetries` and `onUltimateModelMaxRetriesChange` (literal
  removal of only the previously-listed sites fails the TS compile; all
  must go):
  - **:24** — prop type `ultimateModelMaxRetries: number;`
  - **:42** — prop type `onUltimateModelMaxRetriesChange: (value: number) => void;`
  - **:70** — destructured `ultimateModelMaxRetries` from props
  - **:86** — destructured `onUltimateModelMaxRetriesChange` from props
  - **:415** — input value binding `value={ultimateModelMaxRetries}`
  - **:420** — handler invocation `onUltimateModelMaxRetriesChange(val);`
  - **:424–436** — validation/border-class logic and conditional error/warning
    UI keyed on `ultimateModelMaxRetries`
  - **Surrounding**: the `<label>`/`<input>` JSX block that wraps the
    Max-Retries field (a single contiguous block around lines ~410–445)
    must be removed; the parent state hooks
    (`useState<...>("ultimateModelMaxRetries", ...)`) inside the parent
    Settings page that pipe into these props must be deleted too —
    identified via `grep -n 'ultimateModelMaxRetries' pkg/ui/frontend/src/`.
- `components/EventLog.tsx`:
  - Line 97 — update exhausted message text to reflect attempts (e.g.
    `Ultimate model attempts exhausted: N/40`); keep reading
    `current_retry`/`max_retries` event keys.
  - Line 186 — update the categorical label `ULTIMATE_RETRY_EXHAUSTED`
    wording to attempts (e.g. `ULTIMATE_ATTEMPTS_EXHAUSTED`) so the label
    matches the new semantics; it is a display label only (no wire/format
    coupling).
- **TS compile gate** (mandatory): after the edits,
  `cd pkg/ui/frontend && npm run build` must complete with zero TypeScript
  errors. If `ultimateModelMaxRetries` or `onUltimateModelMaxRetriesChange`
  still resolves anywhere, the build fails. Implementor must
  `grep -rn 'ultimateModelMaxRetries\|onUltimateModelMaxRetriesChange\|UltimateModelMaxRetries' pkg/ui/frontend/src/`
  and confirm **zero** matches before declaring §4.5 done.

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
- **MarkFailed deletion sweep — exhaustive enumeration (rev. 3)**. Rev. 2
  listed only the 4 production sites (§4.3) + 6 test sites in `pkg/proxy`,
  totaling **10**; rev. 2 **missed** all 10 call sites in
  `pkg/ultimatemodel/handler_test.go` plus 3 dedicated MarkFailed test
  functions, totaling **13 more sites** = 23 sites in total. With the API
  gone, the build will fail unless every one is removed or rewritten. The
  complete inventory is below.

  **Production call sites (4 — covered by §4.3):** `pkg/proxy/handler.go:952,
  :997, :1178, :1389`. *Each is deleted together with its adjacent
  messages-mapping block.*

  **Test call sites in `pkg/proxy/` (6 — covered by rev. 2):**
  `pkg/proxy/handler_integration_test.go:575, :898, :1211, :1399` and
  `pkg/proxy/handler_test.go:2232, :2293` (rev. 1 had missed `:898` and
  `:1211`). *Treatment: same as production — DELETE the call and any
  surrounding assertion that was checking "hash stored after MarkFailed"
  mechanics.*

  **Dedicated MarkFailed test functions in `pkg/ultimatemodel/handler_test.go`
  (3 — DELETE the entire function):**

  | Function | Lines | Treatment | Rationale |
  |---|---|---|---|
  | `TestShouldTrigger_AfterMarkFailed` | :358–387 | **DELETE entire function** | Tests trigger behavior after `MarkFailed` seeds the cache. With `RecordAttempt`-at-entry, failure-marking semantics are gone — the test's premise no longer exists. Replaced by the new "Trigger schedule" unit test above (calls 1–4 normal, 5 triggers, 41 exhausted). |
  | `TestMarkFailed_EmptyMessages` | :444–453 | **DELETE entire function** | Pure `MarkFailed` API contract test (empty-messages returns empty hash). The API is being removed; no equivalent. |
  | `TestMarkFailed_ReturnsHash` | :455–473 | **DELETE entire function** | Pure `MarkFailed` API contract test (returns hash + stores in cache). The API is being removed; replaced implicitly by `RecordAttempt` unit tests (insert-on-first, ReturnsCount=1). |

  **MarkFailed call sites inside other tests in
  `pkg/ultimatemodel/handler_test.go` (10 — per-site treatment):**

  | Line | Enclosing test | What the site asserts | Treatment | Rationale |
  |---|---|---|---|---|
  | **:368** | `TestShouldTrigger_AfterMarkFailed` (:358–387) | n/a (parent function deleted) | **DELETE** | Goes with the parent-function deletion above. |
  | **:399** | `TestShouldTrigger_MaxRetriesZero` (:389–408) | "After `MarkFailed` seeds, `ShouldTrigger` triggers under `MaxRetries=0`" | **REWRITE** | The `MaxRetries=0` "unlimited retries" branch no longer exists — the schedule is fixed regardless of any config. Replace `h.MarkFailed(messages)` with **3 successive `h.ShouldTrigger(messages)` calls**: the 3rd lands on the first milestone (attempt 3 < 5 → no trigger), so the test's "should trigger with unlimited retries" assertion becomes obsolete. **Better: delete the entire function** — `TestShouldTrigger_MaxRetriesZero` is exercising a config knob (`MaxRetries=0`) that the schedule change removes entirely; the schedule unit tests above cover the same ground under fixed semantics. |
  | **:421** | `TestShouldTrigger_RetryExhausted` (:410–440) | "After `MarkFailed` seeds, 1st call counter=1 not triggered, 2nd counter=2 triggered, 3rd counter=3 exhausted (with `MaxRetries=2`)" | **REWRITE** | Replace `h.MarkFailed(messages)` with the first `h.ShouldTrigger(messages)` call (counter → 1). Rewrite subsequent assertions for the **fixed schedule**: 4 calls = no trigger (counter 1..4); 5th call = triggered (milestone 5); 40th = triggered (final injection); 41st = triggered + exhausted (`AttemptsExhausted=true`); 42nd = exhausted. The `RetryExhausted` field becomes `AttemptsExhausted` and `CurrentRetry` becomes `CurrentAttempt`. |
  | **:449** | `TestMarkFailed_EmptyMessages` (:444–453) | n/a (parent function deleted) | **DELETE** | Goes with the parent-function deletion above. |
  | **:464** | `TestMarkFailed_ReturnsHash` (:455–473) | n/a (parent function deleted) | **DELETE** | Goes with the parent-function deletion above. |
  | **:528** | `TestOnConfigChange_ModelIDChanged` (:522–546) | Seeds the hash cache via `MarkFailed`, then fires an `ultimate_model.model_id` config-change event and asserts the cache was reset | **REWRITE** | Replace `h.MarkFailed([]map[string]interface{}{...})` with `h.hashCache.RecordAttempt(HashMessages([]map[string]interface{}{...}))` — equivalent seed (counter=1, hash present), uses the new API. The remainder of the test (config-change handler + `GetStats() == 0` assertion) is unchanged. |
  | **:554** | `TestOnConfigChange_OtherField` (:548–572) | Seeds the hash cache via `MarkFailed`, then fires a non-`ultimate_model.model_id` config-change event and asserts the cache was **NOT** reset | **REWRITE** | Same as :528 — replace with `h.hashCache.RecordAttempt(HashMessages(...))`. |
  | **:580** | `TestOnConfigChange_NoData` (:574–595) | Seeds the hash cache via `MarkFailed`, then fires a config-change event with no `Data` and asserts the cache was **NOT** reset | **REWRITE** | Same as :528 — replace with `h.hashCache.RecordAttempt(HashMessages(...))`. |
  | **:1099** | `TestHandler_ConcurrentShouldTrigger` (:1090–1134) | Pre-seeds via `MarkFailed` so the cache returns counter ≥ 1; runs 100 concurrent `ShouldTrigger` calls on the same hash and asserts **exactly 99 triggered / 1 not triggered** | **REWRITE** | Replace `h.MarkFailed(messages)` with **one** `h.ShouldTrigger(messages)` call (counter → 1, the 99 follow-on concurrent calls each increment to 2..100, of which 99 land on milestones 5/10/20/30/40/{40+spread} — **the test's "99 triggered / 1 not triggered" expectation is broken by the new schedule**). Replace the test entirely with the concurrency scenario already in the new-unit-tests block above: N goroutines, multiset equality to `{1..N}`. The seed call becomes a single pre-call (counter=1) before launching the N goroutines. |
  | **:1155** | `TestHandler_FullFlow` (:1138–1193) | Sequence: 1st `ShouldTrigger` no-trigger, then `MarkFailed`, then 1st post-MarkFailed no-trigger (counter=1), 2nd post-MarkFailed triggered (counter=2), then `OnConfigChange` reset, then no-trigger again | **REWRITE** | Replace `hash := h.MarkFailed(messages)` (and the surrounding `// 2. Mark as failed (stores hash)` comment) with **four** pre-`ShouldTrigger` calls to climb the counter to 4, then a 5th that asserts trigger at milestone 5. Drop the `_ = hash` placeholder (line :1192). The `OnConfigChange` reset assertion (lines :1174–1184) and the post-reset no-trigger check (lines :1186–1190) are unchanged. |

  > **Note on per-site decisions.** Each site above was read in context
  > before this plan was finalized. Three of the rewrites (:528, :554, :580)
  > use the same mechanical replacement (`MarkFailed` → `RecordAttempt` via
  > the unexported `hashCache`). The four test-function-level rewrites
  > (:399, :421, :1099, :1155) restructure assertions against the fixed
  > 5/10/20/30/40 schedule rather than the per-test `MaxRetries` knob the
  > tests were originally written against. If any of these per-site
  > treatments is rejected during implementation (e.g., the test owner
  > prefers to delete `TestShouldTrigger_MaxRetriesZero` outright), the
  > implementation can take that path — the row's "Rationale" column states
  > the recommended direction.
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
- `test/e2e_ultimate_internal_reasoning/e2e_ultimate_internal_reasoning_test.go:193-200`
  — remove the `t.Setenv("ULTIMATE_MODEL_MAX_RETRIES", "0")` line at
  **:200** (the surrounding `APPLY_ENV_OVERRIDES` / `MAX_GENERATION_TIME` /
  `ULTIMATE_MODEL_ID` / `ULTIMATE_MODEL_MAX_HASH` lines at :196–199 stay —
  only the max-retries line goes). The explanatory comment at **:193**
  ("`ULTIMATE_MODEL_MAX_RETRIES=0` → unlimited, so X-Force-Ultimate-Model
  …") goes with it.
- `test/e2e_fe_reasoning_observability/harness_test.go:278` — remove the
  `t.Setenv("ULTIMATE_MODEL_MAX_RETRIES", "0")` call. The surrounding
  `APPLY_ENV_OVERRIDES` / `MAX_GENERATION_TIME` / `ULTIMATE_MODEL_MAX_HASH`
  lines at :275–277 stay (only the max-retries line goes).
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

  **HARD implementation procedure — MarkFailed grep sweep.** Before
  declaring §4.6 (and §4.3) complete, the implementor MUST run:

  ```bash
  grep -rn 'MarkFailed' pkg/ test/
  ```

  and confirm **zero** matches across the entire repository. This is a
  build-gate: with the API deleted (`handler.go:144-152` removed in §4.2),
  any residual reference — production, test, comment, or doc — will fail
  `go build ./...` or `go test ./pkg/ultimatemodel/...`. The grep is
  expected to return no lines. If any line surfaces, the deletion sweep is
  incomplete and §4.6 is not done.

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
| Hash evicted from circular buffer (`MaxHash` config-read, default 100 — `handler.go:60-63`) | Counter entry deleted with eviction → counting restarts at 1. Eviction can therefore reset the counter below milestone 5 **whenever ≥100 distinct hashes intervene** (existing eviction-cleanup behavior; reaching 40 attempts without eviction is the norm). Unit test: climb to 3 → evict via 100 other hashes → same hash restarts at 1 (§4.6) |
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
   renames + `MarkFailed` deletion (`handler.go:144-152`) + error-message
   wording (incl. the `:580` fallback) + `SendRetryExhaustedError` parameter
   rename (lockstep with `pkg/proxy/handler.go:662`).
3. `pkg/proxy/handler.go`: mechanical renames/wording + publish-site doc
   comment at `:654-659` and `:684-690` + remove the **4 production
   `MarkFailed` call sites** (`:952, :997, :1178, :1389`).
4. `pkg/ultimatemodel/handler_test.go` MarkFailed sweep (rev. 3 critical):
   delete the **3 dedicated test functions** (`TestShouldTrigger_AfterMarkFailed`,
   `TestMarkFailed_EmptyMessages`, `TestMarkFailed_ReturnsHash`) and
   rewrite/delete the **10 call sites inside other tests**
   (`:368, :399, :421, :449, :464, :528, :554, :580, :1099, :1155`) per the
   per-site treatment table in §4.6.
5. `pkg/proxy/handler_integration_test.go` + `pkg/proxy/handler_test.go`
   MarkFailed sweep: remove the **6 `MarkFailed` call sites**
   (`:575, :898, :1211, :1399, :2232, :2293`) and rewrite the fixtures that
   asserted 2nd-call trigger (now 5th) and the `MaxRetries = 3` fixtures
   (`:2084, :2099, :2160, :2221, :2285`) to be schedule-based.
6. `pkg/config/config.go`: remove `MaxRetries` everywhere; verify zero
   residual references via the §4.4 first-boot grep gate; fix
   `config`/`store` tests (`:1410, :1522`, `mock_store_test.go:176`).
7. Capture-persistence + e2e test env cleanups: 4× inert `MaxRetries=0`
   setenvs in `handler_capture_persistence_test.go:138, :290, :491, :641`
   + `handler_capture_persistence_toolcall_test.go:159`; the **3 e2e
   harness t.Setenv sites** (`e2e_minimax_reasoning/harness_test.go:287-292`,
   `e2e_ultimate_internal_reasoning/...:200`,
   `e2e_fe_reasoning_observability/harness_test.go:278`); mock scripts
   (§4.6).
8. Frontend: `types.ts`, `components/SettingsPage.tsx` (state hook at :90,
   config-sync setter at :128, payload write at :171, prop pass at :387,
   handler binding at :405) AND `components/config/ProxySettings.tsx`
   (:24, :42, :70, :86, :415, :420, :424–436), `components/EventLog.tsx`
   — verified via the §4.5 TS-compile grep gate. **Both frontend files
   must be edited together**; the SettingsPage sites are real, not phantom,
   and the leader's `:387` / `:405` citations were exact.
9. Docs: `ultimate-model-design.md`, `AGENTS.md`, `README.md`.
10. **Final build-gate**: `grep -rn 'MarkFailed' pkg/ test/` must return
    zero matches (§4.6 HARD procedure); `go test ./pkg/ultimatemodel/...
    ./pkg/proxy/... ./pkg/config/...` must pass; `npm run build` in
    `pkg/ui/frontend` must succeed with zero TS errors.

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
