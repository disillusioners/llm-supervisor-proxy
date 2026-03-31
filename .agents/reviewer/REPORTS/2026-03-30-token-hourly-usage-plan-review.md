# Plan Review: Token-level Hourly Request Counting

**Reviewer:** Reviewer Agent  
**Date:** 2026-03-30  
**Project:** llm-supervisor-proxy  
**Plan Location:** `.agents/shared/working/token-hourly-usage/`  
**Plan Files:** `plan-overview.md`, `phase1-plan.md`, `phase2-plan.md`, `phase3-plan.md`, `decisions.md`  
**Status:** 🔴 **Needs Major Revisions**

---

## Review Summary

| Metric | Value |
|--------|-------|
| **Verdict** | 🔴 **Blocking Issues Found** |
| **Critical Issues** | 6 |
| **Warnings** | 8 |
| **Suggestions** | 5 |
| **Sessions Used** | ses_2be902d88ffegqyvrcn9M02OzR, ses_2be8dab98ffeeyuwS6zVyfwAS5, ses_2be8b1462ffeWk5I5Vuc7UNuCH, ses_2be890262ffeLfMgsjWqlRnq2F |

---

## Executive Summary

The plan is well-structured and covers the right scope, but contains **6 critical blocking issues** that must be resolved before implementation can begin. Most critically:

1. **The plan references functions that don't exist** (`finalizeSuccess`, `handleModelFailure`)
2. **`reqLog.Usage` is never populated** — the counting would only count requests, not tokens
3. **Token ID propagation approach is partially wrong** — the proposed context pattern doesn't match the actual code flow
4. **PostgreSQL queries are unaddressed** — sqlc is SQLite-only, but PostgreSQL is supported

The good news: the core architecture decision (DB-level UPSERT with hourly buckets) is sound, the phase ordering is correct, and the frontend approach is reasonable. The issues are all in the implementation details.

---

## Review Plan

### Sessions Used

| Session | Target | Focus |
|---------|--------|-------|
| plan-review | Full codebase | Auth flow, request context, DB structure, UI patterns, existing usage tracking |
| plan-review-2 | Targeted deep-dive | Auth propagation, DB access in UI server, SQLite compatibility, sqlc config, race executor |
| plan-review-3 | Finalization flow | Actual finalization points, usage data availability, RequestLog store behavior |
| plan-review-4 | Detailed validation | All finalization points, token ID availability, PostgreSQL query approach, migration compatibility |

---

## Findings

### 🔴 Critical Issues

---

#### CRITICAL-1: `finalizeSuccess()` and `handleModelFailure()` Do Not Exist

**Severity:** 🔴 Critical  
**Location:** Phase 1, Task 1.7; Phase 2 Task references  
**Affected Files:** `pkg/proxy/handler_finalize.go` (file referenced but doesn't exist)

**Finding:** The plan repeatedly references `finalizeSuccess()` and `handleModelFailure()` as the hooking points for counting. These functions **do not exist** in the codebase.

The actual finalization happens **inline** in `pkg/proxy/handler.go` at **7 different locations**:

| Line | Status | Path |
|------|--------|------|
| 342 | `"failed"` | Auth failure |
| 388 | `"failed"` | Ultimate Model — retry exhausted |
| 432 | `"failed"` | Ultimate Model — Execute error |
| 457 | `"completed"` | Ultimate Model — success |
| 541 | `"failed"` | Race Retry — all models failed |
| 758 | `"completed"` | Race Retry — streaming success |
| 868 | `"completed"` | Race Retry — non-streaming success |

All of them follow the same pattern:
```go
rc.reqLog.Status = "completed"  // or "failed"
rc.reqLog.EndTime = time.Now()
rc.reqLog.Duration = time.Since(rc.startTime).String()
rc.reqLog.Messages = append(rc.reqLog.Messages, assistantMsg)  // success only
h.store.Add(rc.reqLog)
```

**Fix Required:** The plan must be updated to identify these 7 actual finalization points, not invented function names. A helper function `finalizeAndCount()` should be extracted to avoid code duplication.

---

#### CRITICAL-2: `reqLog.Usage` Is Never Populated

**Severity:** 🔴 Critical  
**Location:** Phase 1, Task 1.7; plan-overview.md line 61  
**Evidence:** `store.Usage` struct exists (`memory.go:26-31`) but is **never assigned** anywhere in the codebase.

**Finding:** The plan assumes `rc.reqLog.Usage` will contain token usage data (prompt/completion/total tokens) that can be read at finalization time. This is **not true**.

The actual state:
- `store.Usage` struct is defined but unused
- `upstreamRequest.usage` (`race_request.go:62`) exists with `SetUsage()`/`GetUsage()` methods
- **Neither method is called anywhere in the codebase** (0 grep matches)
- Token usage is only injected into SSE streaming chunks for internal providers (`race_executor.go:482-488`) — it's sent to the client, not stored
- `reqLog.Usage` is never assigned in any code path (Ultimate Model, race executor, streaming, non-streaming)

**Impact:** The counting hook would only record `request_count = 1` with `prompt_tokens = 0`, `completion_tokens = 0`, `total_tokens = 0` for all requests. Token-level token counting would not work.

**Fix Required:** The plan must include **Task 0** (or part of Phase 1): extract token usage from race request responses and populate `reqLog.Usage`. This is a non-trivial task because:
- Non-streaming: parse usage from JSON response
- Streaming: aggregate from accumulated usage (if available)
- External providers: may or may not return usage data

The plan's current "graceful handling" of nil usage (count requests, tokens = 0) is correct — but only if we first *try* to extract the usage. The plan doesn't account for the extraction work.

---

#### CRITICAL-3: Token ID Propagation — Wrong Approach

**Severity:** 🔴 Critical  
**Location:** Phase 1, Tasks 1.1–1.4; Decisions.md Decision 3  
**Affected Files:** `pkg/proxy/handler.go`, `pkg/proxy/handler_functions.go`

**Finding:** Decision 3 proposes using `context.WithValue()` via a context key (`pkg/auth/context.go`) to propagate `*AuthToken` through the request context. The analysis shows this approach is **overcomplicated and partially incorrect** for this codebase.

**Why the proposed approach is wrong:**

1. **`authenticate()` is called AFTER `initRequestContext()`** (handler.go:321 vs 336). The token identity should be captured during the `authenticate()` call, not in `initRequestContext`.

2. **`authenticate()` can't modify `r.Context()` directly** — it only has `*http.Request`. To propagate via context, the caller would need to do `r = r.WithContext(context.WithValue(r.Context(), AuthTokenKey, token))` inside `HandleChatCompletions` after `authenticate()` returns true.

3. **Simpler approach exists:** `requestContext` struct already exists (`handler_functions.go:22-79`) and is passed to every function. Add `tokenID string` and `tokenName string` fields to it. Set them in `HandleChatCompletions` after successful authentication. This requires zero new files.

**Correct approach:**
```go
// In HandleChatCompletions, after authenticate() returns true:
if h.requiresInternalAuth(rc) {
    if !h.authenticate(r) {
        h.sendAuthError(w)
        return
    }
    // Token is already validated; extract ID and name
    // But authenticate() discards the token...
}
```

**The real fix for authenticate():**
```go
// Current (handler.go:259):
_, err := h.tokenStore.ValidateToken(ctx, apiKey)
return err == nil

// Should be:
token, err := h.tokenStore.ValidateToken(ctx, apiKey)
if err != nil {
    return nil, false  // or return token, false
}
return token, true
```

Then in `HandleChatCompletions`, store the token:
```go
// After authenticate returns true:
token, _ := h.authenticate(r)
if token != nil {
    rc.tokenID = token.ID
    rc.tokenName = token.Name
}
```

**Fix Required:** Update Phase 1 Tasks 1.1–1.4. Remove the `pkg/auth/context.go` context-key approach. Instead:
1. Change `authenticate()` to return `(*auth.AuthToken, bool)`
2. Add `tokenID` and `tokenName` to `requestContext` struct
3. Set them in `HandleChatCompletions` after successful auth

---

#### CRITICAL-4: `request_counts` vs `token_hourly_usage` Naming Inconsistency

**Severity:** 🔴 Critical  
**Location:** plan-overview.md line 49, phase1-plan.md line 59

**Finding:** The plan uses two different table names inconsistently:
- `plan-overview.md` line 49: `request_counts` table
- `phase1-plan.md` line 59: `token_hourly_usage` table (with CREATE TABLE code)
- `phase2-plan.md` uses `token_hourly_usage`

The plan overview says `request_counts`, but the actual migration template says `token_hourly_usage`. This must be unified.

**Fix Required:** Decide on a single table name. `token_hourly_usage` is more descriptive. Update plan-overview.md to match.

---

#### CRITICAL-5: PostgreSQL Queries Are Unaddressed

**Severity:** 🔴 Critical  
**Location:** Phase 1, Task 1.6; Phase 2, Task 2.1  
**Affected Files:** `pkg/store/database/sqlc/queries.sql`, `pkg/store/database/querybuilder.go`

**Finding:** The plan adds sqlc queries for both the UPSERT and read operations. However:

1. **sqlc is configured for SQLite only** (`sqlc.yaml:3`: `engine: "sqlite"`). It cannot generate PostgreSQL code.

2. **No PostgreSQL sqlc config exists** — the project doesn't have `sqlc.postgres.yaml` or similar.

3. **Phase 2 queries use SQLite syntax**: `SUBSTR(hour_bucket, 1, 10)` is SQLite. PostgreSQL uses `SUBSTRING()` or `LEFT()`. Query parameter placeholders are `?` (SQLite) vs `$1` (PostgreSQL).

4. **The project's existing pattern** for dual-database support uses the `QueryBuilder` class (`querybuilder.go`) with `dialect` checks. This pattern is used for `UpsertConfig()` and `InsertModel()`.

5. **The UPSERT for counting**: The plan proposes `ON CONFLICT DO UPDATE` which is valid for **both** SQLite (3.24+) and PostgreSQL. However, the project's existing `QueryBuilder` uses `INSERT OR REPLACE` for SQLite (which deletes and re-inserts, resetting counters). For incrementing counters, `ON CONFLICT DO UPDATE` is required for both databases.

**Fix Required:** 
- Phase 1: Add `UpsertTokenHourlyUsage()` method to `QueryBuilder` class with proper dual-dialect support
- Phase 2: Add read query methods to `QueryBuilder` with `GetTokenHourlyUsage()`, `GetAllTokenHourlyUsage()`, `GetTokensWithUsage()`, `GetTokenDailyUsage()` — all with PostgreSQL (`SUBSTRING()`, `$1` placeholders) and SQLite (`SUBSTR()`, `?` placeholders) variants
- The Phase 2 queries using `SUBSTR` must be wrapped in QueryBuilder dialect checks

---

#### CRITICAL-6: UI Server Has No Database Access

**Severity:** 🔴 Critical  
**Location:** Phase 2, Tasks 2.2–2.6  
**Affected Files:** `pkg/ui/server.go`, `cmd/main.go`

**Finding:** Phase 2 plans to add `/fe/api/usage` endpoints to the UI server. However, the `Server` struct does **not** have database access:

```go
// pkg/ui/server.go:58-67
type Server struct {
    bus          *events.Bus
    configMgr    config.ManagerInterface
    proxyConfig  *proxy.Config
    modelsConfig models.ModelsConfigInterface
    store        *store.RequestStore  // IN-MEMORY only
    bufferStore  *bufferstore.BufferStore
    tokenStore   *auth.TokenStore
    mu           sync.Mutex
}
```

The `store` field is `*store.RequestStore` — the **in-memory** store, not the database.

In `main.go:93`, the UI server is created without database access:
```go
uiServer := ui.NewServer(bus, configMgr, proxyConfig, modelsConfig, reqStore, bufferStore, tokenStore)
```

The `database.Store` with `DB *sql.DB` exists (`connection.go:24-28`) and is used for `auth.NewTokenStore()` on line 39-49, but **it's not passed to the UI server**.

**Fix Required:** 
1. Add `*database.Store` field to `Server` struct in `pkg/ui/server.go`
2. Add it to `NewServer()` constructor
3. Inject it in `main.go` (line 93): `ui.NewServer(..., dbStore, ...)`
4. Update the `usage` handlers to use `dbStore.DB` or `dbStore.Queries`

This is a non-trivial change that affects `main.go`, `server.go`, and requires a new constructor. It should be its own task in Phase 2.

---

### 🟡 Warnings

---

#### WARNING-1: Counting is Fire-and-Forget, But Errors Should Be Logged

**Severity:** 🟡 Warning  
**Location:** Phase 1, Task 1.7

**Finding:** The plan says "fire-and-forget is acceptable" for counting. This is correct from a performance perspective, but the plan doesn't address error handling. If the UPSERT fails repeatedly:
- No data is recorded (silent failure)
- There's no retry mechanism
- There's no alerting/metrics for failed counting

**Recommendation:** At minimum, log errors when the UPSERT fails (even if fire-and-forget). Consider adding a counter metric for failed writes.

---

#### WARNING-2: Race Retry Path — Token ID Must Be Available

**Severity:** 🟡 Warning  
**Location:** Phase 1, Tasks 1.3–1.4

**Finding:** The `requestContext` is created at line 321, before authentication (line 336). The token ID will be set after authentication. But the race coordinator executes requests and can fail/retry within the same `requestContext`. As long as the token ID is set on `rc` before `coordinator.Start()` (line 476), it will be available throughout.

This is fine — just needs to be confirmed in implementation.

---

#### WARNING-3: `GET /fe/api/usage/tokens` Requires JOIN

**Severity:** 🟡 Warning  
**Location:** Phase 2, Task 2.4

**Finding:** The plan acknowledges that token names need to be joined from `auth_tokens`. The plan says "may need a custom query outside sqlc". This is correct — the `QueryBuilder` should have a `GetTokensWithUsageAndNames()` method that does the JOIN.

Additionally, token names can change. The JOIN approach ensures the current name is always shown, which is the correct behavior.

---

#### WARNING-4: Daily Aggregation — `SUBSTR` Is SQLite-Only

**Severity:** 🟡 Warning  
**Location:** Phase 2, Task 2.1

**Finding:** The `GetTokenDailyUsage` query uses `SUBSTR(hour_bucket, 1, 10)` which is SQLite syntax. PostgreSQL uses `SUBSTRING(col, 1, 10)` or `LEFT(col, 10)`. This must be handled in the QueryBuilder dialect check.

Also, `SUBSTR(hour_bucket, 1, 10)` for `"2026-03-30T14"` gives `"2026-03-30"` which is correct, but the QueryBuilder must generate the right function for each dialect.

---

#### WARNING-5: No Date Parsing/Validation

**Severity:** 🟡 Warning  
**Location:** Phase 2, Tasks 2.3, 2.5

**Finding:** The plan says "accept ISO hour format or ISO date (auto-expand to day)" but doesn't specify the validation logic. If someone passes `?from=not-a-date`, what happens? The plan should specify:
- Expected format: `YYYY-MM-DD` or `YYYY-MM-DDTHH`
- Default: last 24 hours
- Error handling: return 400 for invalid format

---

#### WARNING-6: Pagination Not Specified

**Severity:** 🟡 Warning  
**Location:** Phase 2, Task 2.3

**Finding:** For busy tokens with many hours of data, the `/fe/api/usage` endpoint could return hundreds of rows. The plan doesn't mention pagination. For a table/UI display, this is probably fine (user can narrow date range), but it should be documented as a known limitation.

---

#### WARNING-7: Frontend Caching Not Specified

**Severity:** 🟡 Warning  
**Location:** Phase 3, Task 3.2

**Finding:** The `useUsage()` hook doesn't specify caching. On every date range change or token selection, it will refetch. For a settings page with infrequent access, this is probably fine. But the plan should explicitly say "no client-side caching required" rather than leaving it ambiguous.

---

#### WARNING-8: Token Selector Default

**Severity:** 🟡 Warning  
**Location:** Phase 3, Tasks 3.3, 3.6

**Finding:** When the Usage tab opens, what should the default state be?
- Show all tokens? (could be very large)
- Show last 24h? (could be a lot of data)
- The plan says "default view: Show last 24 hours for all tokens when tab opens" (Phase 3 Constraints)

This needs to be confirmed with the frontend — loading ALL tokens' last 24h data at once could be a lot. Consider defaulting to a specific token or showing summary only.

---

### 🟢 Suggestions

---

#### SUGGESTION-1: Consider Extracting a `finalizeAndCount()` Helper

**Severity:** 🟢 Suggestion

Instead of modifying 7 inline finalization points, extract a helper:
```go
func (h *Handler) finalizeAndCount(rc *requestContext, status string) {
    rc.reqLog.Status = status
    rc.reqLog.EndTime = time.Now()
    rc.reqLog.Duration = time.Since(rc.startTime).String()
    h.store.Add(rc.reqLog)
    // Then call counter
    h.counter.Increment(...)
}
```

This reduces code duplication and makes the counting hook a single point.

---

#### SUGGESTION-2: Token Usage Extraction Is Complex — Consider a Separate Phase

**Severity:** 🟢 Suggestion

Extracting token usage from race request responses (streaming + non-streaming, internal + external) is non-trivial. The plan treats it as a side note ("gracefully handle nil usage"). Consider:

1. **Phase 1A**: Just count requests (no token usage)
2. **Phase 1B** (separate): Extract and store token usage

This way Phase 1 is achievable, and token usage is an enhancement.

---

#### SUGGESTION-3: Add an Index on `(hour_bucket, token_id)`

**Severity:** 🟢 Suggestion

The plan adds indexes on `token_id` and `hour_bucket` separately. For the common query pattern (get all tokens' usage in a date range, ordered by hour), a composite index `(hour_bucket, token_id)` would be more efficient.

```sql
CREATE INDEX idx_token_hourly_usage_hour_token ON token_hourly_usage(hour_bucket, token_id);
```

---

#### SUGGESTION-4: Consider `date_trunc('hour', ...)` for PostgreSQL Aggregation

**Severity:** 🟢 Suggestion

Instead of `SUBSTR`-based daily aggregation, PostgreSQL can use `date_trunc('hour', timestamp)` for cleaner time bucketing. Since the hour_bucket is stored as a string in ISO format, `SUBSTRING` works, but the QueryBuilder could be more explicit.

---

#### SUGGESTION-5: Add Integration Test for Counting

**Severity:** 🟢 Suggestion

The plan doesn't mention integration tests. Since counting spans the full request lifecycle (auth → request → response → finalization), an integration test that:
1. Sends a proxied request with a known token
2. Checks `token_hourly_usage` table for the expected increment

This would catch regressions in the counting hook.

---

## Assessment by Criterion

### Completeness — 🟡 Partial

**Missing components identified:**
1. Phase 1 doesn't include the work to populate `reqLog.Usage` (CRITICAL-2)
2. No migration for adding token fields to existing request logs (N/A for new table)
3. No error handling/logging strategy for failed counting writes
4. No mention of Ultimate Model bypass path — does it need counting too? (Yes — lines 457)
5. Phase 2 doesn't address the need to inject `*database.Store` into the UI server

**What's well-covered:**
- All 3 phases have clear task breakdowns
- Both SQLite and PostgreSQL migrations are planned
- API endpoints cover the essential queries
- Frontend components are well-scoped

### Feasibility — 🟡 Needs Fixes

The plan is 80% feasible. The 20% blockers are:
1. Non-existent functions (CRITICAL-1) — must use actual code paths
2. `reqLog.Usage` is never populated (CRITICAL-2) — needs extraction work
3. UI server needs DB injection (CRITICAL-6) — affects Phase 2 tasks
4. PostgreSQL via QueryBuilder (CRITICAL-5) — more complex than sqlc-only

With the fixes above, it's fully feasible.

### Phase Ordering — ✅ Correct

Dependencies are correct:
- Phase 1 → Phase 2: Counting must work before API can read data
- Phase 2 → Phase 3: API must exist before frontend can use it

Within phases, tasks are in logical order (auth → context → migration → hook).

**One suggested change:** Split Phase 1 into 1A (just counting requests) and 1B (token usage extraction). This makes Phase 1 achievable in the estimated 3-4h.

### Risk Assessment — 🟡 Partially Accurate

**Identified risks that are accurate:**
- ✅ SQLite write contention (LOW likelihood for typical LLM proxy load)
- ✅ `authenticate()` signature change (mitigation is correct — return `(*AuthToken, bool)`)
- ✅ Migration compatibility (mitigation is correct — dual migrations)
- ✅ Frontend chart library (decision is correct — CSS tables/bars)

**Missing risks:**
- ❌ `finalizeSuccess()` doesn't exist — **this is a HIGH-impact risk that wasn't identified**
- ❌ `reqLog.Usage` never populated — **HIGH-impact risk that wasn't identified**
- ❌ UI server DB access not available — **HIGH-impact risk that wasn't identified**
- ❌ PostgreSQL queries unaddressed — **MEDIUM-impact risk that wasn't identified**

**Suggested additional risk:** "Token usage extraction complexity" — extracting usage from race request responses (especially streaming) is harder than expected.

### Architecture Decisions — 🟡 5/7 Sound

| # | Decision | Assessment |
|---|----------|------------|
| 1 | DB-level UPSERT vs In-Memory | ✅ Correct — simplicity, durability, survives restarts |
| 2 | Hourly bucket ISO format | ✅ Correct — sortable, human-readable, `time.Format("2006-01-02T15")` |
| 3 | authenticate() refactoring | ❌ Wrong — context.WithValue approach is overcomplicated. Simpler: change signature + add to requestContext |
| 4 | Counter package location | ✅ Correct — new `pkg/usage/` package is clean separation |
| 5 | Frontend CSS bar charts | ✅ Correct — zero dependencies, Tailwind-native, tables are better for hourly data |
| 6 | Skip counting when auth disabled | ✅ Correct — anonymous counting is future work |
| 7 | Token name JOIN | ✅ Correct — always current names, single query |

### Database Design — 🟡 Mostly Sound

**What's correct:**
- Table name `token_hourly_usage` (after fixing CRITICAL-4)
- `token_id TEXT` matches `auth_tokens.id TEXT` ✅
- Composite primary key `(token_id, hour_bucket)` ✅
- Indexes on both columns ✅

**Issues:**
- `ON CONFLICT DO UPDATE` syntax: The plan has this correctly (Phase 1), but sqlc is SQLite-only. Need QueryBuilder approach for dual-database.
- Missing composite index `(hour_bucket, token_id)` for the common query pattern

### API Design — 🟡 Reasonable with Gaps

**What's good:**
- RESTful approach with query parameters ✅
- `view=hourly|daily` toggle is flexible ✅
- `/fe/api/usage/summary` for dashboard ✅
- `/fe/api/usage/tokens` for selector ✅
- Default date range (last 24h) ✅

**Gaps:**
- No pagination specified (could return hundreds of rows)
- No date format validation/error codes
- Response format mentions `"token_id"` at top level for single-token queries, but what about all-tokens queries?

### Frontend Approach — ✅ Sound

- Reusing existing Settings tab pattern ✅
- CSS bar charts (no new dependencies) ✅
- TypeScript types match API responses ✅
- `useUsage()` hook follows existing `useApi.ts` patterns ✅
- Token selector + date picker + summary cards ✅
- Summary cards format: 1.5K, 2.3M formatting ✅

**Minor issue:** The default view (all tokens + last 24h) could be data-heavy. Should consider showing summary only by default.

### Testing Strategy — 🟢 Planned But Light

The plan doesn't explicitly mention tests. Given:
- Integration tests would be valuable (send request → check DB)
- Unit tests for QueryBuilder dialect generation
- Frontend: existing testing pattern unknown (Preact + Vitest?)

**Recommendation:** Add at minimum:
1. Integration test for counting hook (Phase 1)
2. Unit tests for QueryBuilder dual-dialect queries (Phase 1/2)
3. Frontend: existing component test patterns in the project

---

## Recommendations

### Must Fix Before Implementation

1. **CRITICAL-1**: Update Phase 1 Task 1.7 to identify the 7 actual finalization points in `handler.go`. Consider extracting a `finalizeAndCount()` helper.

2. **CRITICAL-2**: Add a pre-task or sub-task in Phase 1 to populate `reqLog.Usage`. This is non-trivial and should be estimated separately. Consider Phase 1A (request counting only) and Phase 1B (token usage extraction).

3. **CRITICAL-3**: Revise Decision 3 and Phase 1 Tasks 1.1–1.4:
   - Change `authenticate()` to return `(*auth.AuthToken, bool)`
   - Add `tokenID` and `tokenName` fields to `requestContext`
   - Set them in `HandleChatCompletions` after successful auth
   - Remove the `pkg/auth/context.go` context-key file

4. **CRITICAL-4**: Unify table name to `token_hourly_usage` throughout.

5. **CRITICAL-5**: Add `UpsertTokenHourlyUsage()` and read query methods to `QueryBuilder` class (not sqlc) with proper dual-dialect support.

6. **CRITICAL-6**: Add Phase 2 Task 0 — inject `*database.Store` into UI server:
   - Add field to `Server` struct
   - Add to `NewServer()` constructor
   - Update `main.go` to pass `dbStore`

### Should Fix

7. **WARNING-1**: Add error logging when counting UPSERT fails.

8. **WARNING-4**: Fix `GetTokenDailyUsage` query in QueryBuilder with `SUBSTR` (SQLite) vs `SUBSTRING`/`LEFT` (PostgreSQL).

9. **WARNING-5**: Specify date format validation and error codes (400 for invalid format).

10. **WARNING-6**: Document pagination strategy (or explicitly state "no pagination needed for typical usage").

### Nice to Have

11. **SUGGESTION-1**: Extract `finalizeAndCount()` helper to reduce code duplication.

12. **SUGGESTION-3**: Add composite index `(hour_bucket, token_id)`.

13. **SUGGESTION-5**: Add integration test for counting.

---

## Conclusion

The plan is well-structured and demonstrates good understanding of the project architecture. The core approach (DB-level hourly counting with UPSERT) is sound. However, 6 critical issues must be resolved before implementation:

1. **Non-existent functions** — use actual code paths
2. **`reqLog.Usage` never populated** — add extraction work
3. **Wrong token propagation approach** — simplify to struct fields
4. **Naming inconsistency** — unify table name
5. **PostgreSQL unaddressed** — use QueryBuilder, not sqlc-only
6. **UI server no DB access** — inject `database.Store`

With these fixes, this is a solid plan that can be implemented successfully.

---

*Review generated by Reviewer Agent. Sessions: ses_2be902d88ffegqyvrcn9M02OzR, ses_2be8dab98ffeeyuwS6zVyfwAS5, ses_2be8b1462ffeWk5I5Vuc7UNuCH, ses_2be890262ffeLfMgsjWqlRnq2F*
