# Mock Testing Audit Report: `feature/count-request-per-token`

**Date:** 2026-03-31
**Branch:** `feature/count-request-per-token`
**Commit:** `dc1346d` (feat: complete token-level hourly request counting feature)
**Auditor:** Test Leader Agent

---

## Executive Summary

| Phase | Mock Testing Quality | Verdict |
|-------|---------------------|---------|
| **Phase 1** — Usage Extraction & Counting Hooks | MEDIUM-HIGH | ✅ Adequate, with gaps |
| **Phase 2** — Usage API Endpoints | HIGH | ✅ Solid, minor gaps |
| **Phase 3** — Frontend Visualization | **ZERO** | 🔴 **Critical gap** |

**Overall: Mock testing is adequate for backend (Phases 1-2), but frontend (Phase 3) has zero test coverage.**

---

## Master Table: All Test Files

| # | File | Phase | Mock Strategy | Mock Accuracy | Signature Match | Integration Tested? |
|---|------|-------|--------------|--------------|-----------------|-------------------|
| 1 | `pkg/proxy/authenticate_test.go` | 1 | Interface mocks (`mockTokenStore`, `slowMockTokenStore`) | **HIGH** | YES | LOW — unit tests only |
| 2 | `pkg/proxy/counting_hooks_test.go` | 1 | Mock stubs (`MockCounter`) | **MEDIUM** | YES | LOW — pattern reproduction, not real handler |
| 3 | `pkg/ultimatemodel/usage_test.go` | 1 | None (pure function tests) | **N/A** | YES | HIGH — direct function testing |
| 4 | `pkg/usage/counter_test.go` | 1 | In-memory SQLite (real DB) | **HIGH** | YES | MEDIUM — SQLite only |
| 5 | `pkg/ui/handlers_usage_test.go` | 2 | In-memory SQLite (real DB) | **HIGH** | YES | HIGH — real SQL, real handlers |
| 6 | Frontend components | 3 | **NONE** | **N/A** | ⚠️ **MISMATCH** | **NONE** |

---

## Phase 1: Usage Extraction & Counting Hooks

### 1.1 `pkg/proxy/authenticate_test.go`

**Mock Strategy:** Interface mocks — `mockTokenStore` and `slowMockTokenStore` implement `auth.TokenStoreInterface`

**What's Mocked:**
- `auth.TokenStoreInterface.ValidateToken(ctx, plaintext) (*auth.AuthToken, error)`
- `slowMockTokenStore` for context cancellation testing

**What's Real:**
- `authenticate()` function (real code under test)
- Error types: `auth.ErrTokenNotFound`, `auth.ErrTokenExpired`

**Mock Accuracy: HIGH**
- `mockTokenStore.ValidateToken` correctly returns `ErrTokenNotFound` for missing tokens
- Expiration check uses `time.Now().After(*token.ExpiresAt)` — matches `store.go:118`
- `slowMockTokenStore` properly sleeps to test context cancellation

**Signature Match: YES**
```go
// Interface (store.go:21)
func ValidateToken(ctx context.Context, plaintext string) (*auth.AuthToken, error)
// Mock (authenticate_test.go:32)
func (m *mockTokenStore) ValidateToken(ctx context.Context, plaintext string) (*auth.AuthToken, error)
```

**Test Functions:**
| Test | Scenarios |
|------|----------|
| `TestAuthenticate` | Auth disabled, empty key, invalid format, token not found, valid token, expired, future expiry, lowercase bearer |
| `TestAuthenticate_ExtractAPIKey` | No header, empty header, bearer only, bearer+whitespace, valid extraction |
| `TestAuthenticate_WithContextCancellation` | Context cancelled before DB call |

**Edge Cases Covered:** ✅ Auth disabled, empty/whitespace tokens, invalid format, expired, context cancellation
**Edge Cases Missing:** ✗ `ValidateToken` returns generic DB error (not `ErrTokenNotFound`), `ErrInvalidTokenFormat` path

---

### 1.2 `pkg/proxy/counting_hooks_test.go`

**Mock Strategy:** Mock stubs — `MockCounter` implements `Increment(ctx, tokenID, hourBucket, reqCount, promptTok, compTok, totalTok) error`

**What's Mocked:**
- `MockCounter.Increment()` — tracks call counts and last arguments
- `MockCounter.ShouldError` flag — simulates error returns

**What's Real:**
- Pattern logic (short-circuit conditions, hour bucket formatting, goroutine spawning)

**Mock Accuracy: MEDIUM** ⚠️

**Key Issue:** The tests **reproduce the counting pattern** rather than testing actual `handler.go` code. The tests duplicate the pattern logic and verify it works, but they never call the real `handler.go` functions.

```go
// Test code (line 131) — reproduces pattern, not calling handler.go:
if tt.tokenID != "" && mock != nil {
    // This is a REPRODUCTION
```

**Signature Match: YES**
```go
// Real (counter.go:39)
func (c *Counter) Increment(ctx context.Context, tokenID, hourBucket string, reqCount, promptTok, completionTok, totalTok int) error
// Mock (counting_hooks_test.go:507)
func (m *MockCounter) Increment(ctx context.Context, tokenID, hourBucket string, reqCount, promptTok, completionTok, totalTok int) error
```

**Test Functions:** 39 tests covering condition logic, increment calls, nil counter short-circuit, empty token short-circuit, goroutine error handling, concurrent access (10 goroutines), nil usage handling, hour bucket UTC formatting

**Edge Cases Covered:** ✅ Empty tokenID, nil counter, whitespace tokenID, concurrent access, goroutine errors
**Edge Cases Missing:** ✗ **Integration with actual Handler** — all `NewHandler` calls in `handler_test.go` pass `nil` for counter, so counting path is NEVER exercised in the actual handler context

---

### 1.3 `pkg/ultimatemodel/usage_test.go`

**Mock Strategy:** None — pure function tests directly on `extractUsageFromChunk()` and `extractUsageFromResponse()`

**What's Mocked:** Nothing
**What's Real:** `extractUsageFromChunk()` (handler_external.go:247), `extractUsageFromResponse()` (handler_external.go:124)

**Mock Accuracy: N/A** — no mocks used, direct function testing

**Test Functions:**
| Test | Scenarios |
|------|----------|
| `TestExtractUsageFromChunk` | Valid usage, no usage field, malformed JSON, empty/nil data, zero values, partial fields, non-numeric strings, large values (1M), float truncation, negative values |
| `TestExtractUsageFromResponse` | Same as above + Anthropic-style response |
| `TestExtractUsageBothFunctionsConsistent` | Consistency between chunk and response |

**Edge Cases Covered:** ✅ All major parsing edge cases including malformed JSON, nil/empty, partial fields, large values, floats, Anthropic format
**Edge Cases Missing:** ✗ JSON number overflow (> max int), very large numbers

**Assessment:** This is the strongest test file — pure function tests with comprehensive edge cases covering real LLM API response formats.

---

### 1.4 `pkg/usage/counter_test.go`

**Mock Strategy:** In-memory SQLite database — real `usage.Counter` with `sql.Open("sqlite", ":memory:")`

**What's Mocked:** Nothing
**What's Real:** `sql.DB` (in-memory SQLite), `usage.Counter` (real implementation), table schema

**Mock Accuracy: HIGH** — uses actual database, not mocks. This is actually better than mocks for SQL testing because it validates real SQL queries.

**SQL Dialect Handling:**
- Uses `database.SQLite` for tests (line 40, 177)
- Source code handles PostgreSQL vs SQLite in `counter.go:41-57`

**Test Functions:**
| Test | Scenarios |
|------|----------|
| `TestCounter_Increment` | New row creation, existing row increment, multiple increments, zero tokens |
| `TestCounter_GetTokenUsage` | Exact range, partial range, non-existent token, empty range, ordering, single hour |

**Edge Cases Covered:** ✅ Increment creates/updates rows, accumulation, zero tokens, date filtering, ordering
**Edge Cases Missing:** ✗ Nil context, empty tokenID, empty hourBucket, **PostgreSQL dialect**, **DB error simulation**, context cancellation during query

---

## Phase 2: Usage API Endpoints

### 2.1 `pkg/ui/handlers_usage_test.go`

**Mock Strategy:** In-memory SQLite database — real `database.Store` with `sql.Open("sqlite", ":memory:")`

**What's Mocked:** Nothing — direct test database
**What's Real:** `database.Store`, `*sql.DB`, SQL queries, HTTP handlers, server struct

**Mock Accuracy: HIGH** — in-memory SQLite is superior to interface mocks for SQL-heavy code because:
1. Actual SQL behavior tested (LEFT JOIN, GROUP BY, ORDER BY, LIMIT, COALESCE)
2. Real data flow from INSERT through query to JSON response
3. Validates dialect-specific query branches

**Signature Match: YES**
```go
// Test calls (line 104, 278, 371):
ts.handleUsage(w, req)
ts.handleUsageTokens(w, req)
ts.handleUsageSummary(w, req)

// Source signatures (handlers_usage.go):
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request)
func (s *Server) handleUsageTokens(w http.ResponseWriter, r *http.Request)
func (s *Server) handleUsageSummary(w http.ResponseWriter, r *http.Request)
```

**Total: 33 test functions** covering all 3 endpoints.

### Per-Endpoint Coverage:

#### `/fe/api/usage`
| Scenario | Tested? |
|----------|---------|
| Valid data query | ✅ |
| Filter by token_id | ✅ |
| Filter by date range | ✅ |
| Empty result | ✅ |
| Non-existent token | ✅ |
| view parameter | ✅ |
| Default view | ✅ |
| Method not allowed (405) | ✅ |
| dbStore nil (503) | ✅ |
| Content-Type | ✅ |
| Response fields | ✅ |
| Orphan token name | ✅ |
| **Invalid date format** | ❌ MISSING |
| **Inverted date range** | ❌ MISSING |
| **Range > 1 year** | ❌ MISSING |
| **DB query error** | ❌ MISSING |
| **DB scan error** | ❌ MISSING |

#### `/fe/api/usage/tokens`
| Scenario | Tested? |
|----------|---------|
| Valid data query | ✅ |
| Empty result | ✅ |
| Token ordering | ✅ |
| Token with no usage excluded | ✅ |
| Token name empty | ✅ |
| Method not allowed | ✅ |
| dbStore nil | ✅ |
| **DB query error** | ❌ MISSING |
| **DB scan error** | ❌ MISSING |

#### `/fe/api/usage/summary`
| Scenario | Tested? |
|----------|---------|
| Valid data query | ✅ |
| Filter by token_id | ✅ |
| Empty result | ✅ |
| Large date range (~1 year) | ✅ |
| Peak hour calculation | ✅ |
| Peak hour across tokens | ✅ |
| Token with no usage | ✅ |
| Token name empty | ✅ |
| Method not allowed | ✅ |
| dbStore nil | ✅ |
| **Invalid date format** | ❌ MISSING |
| **Inverted date range** | ❌ MISSING |
| **Range > 1 year** | ❌ MISSING |
| **DB query error** | ❌ MISSING |
| **DB scan error** | ❌ MISSING |
| **Peak hour query error** | ❌ MISSING |

**Assessment:** Very solid coverage. The main gap is validation error tests and DB error simulation.

---

## Phase 3: Frontend Visualization

### 3.1 Frontend Test Coverage: **ZERO**

| Component | Lines | Test Coverage |
|-----------|-------|--------------|
| `UsageTab.tsx` | 165 | ❌ **NONE** |
| `UsageSummaryCards.tsx` | 65 | ❌ **NONE** |
| `UsageTable.tsx` | 201 | ❌ **NONE** |
| `useApi.ts` | 425 | ❌ **NONE** |

**Evidence:**
- No `*.test.ts`, `*.test.tsx`, `*.spec.ts`, `*.spec.tsx` files found anywhere in `pkg/ui/frontend/`
- No test scripts in `package.json`
- No test dependencies (vitest, jest, @testing-library/preact)
- No vitest.config.ts or jest.config.ts

**Impact:** All frontend logic (date range selection, API fetching, state management, error handling, chart rendering, table display) is completely untested.

---

### 3.2 TypeScript/Go Type Alignment: **1 FIELD MISMATCH** ⚠️

| Go Struct | TypeScript Interface | Match? |
|-----------|---------------------|--------|
| `UsageDataRow` | `HourlyUsageRow` | ⚠️ **MISMATCH** |
| `UsageTotals` | `UsageTotals` | ✅ MATCH |
| `UsageResponse` | `UsageResponse` | ✅ MATCH |
| `UsageTokenSummary` | `UsageToken` | ✅ MATCH |
| `GrandTotal` | `UsageGrandTotal` | ✅ MATCH |

**Specific Mismatch — `HourlyUsageRow.token_id` (Phase 3 Review Fix #3):**

```go
// Go: pkg/ui/handlers_usage.go:13-19
type UsageDataRow struct {
    TokenName        string `json:"token_name"`
    HourBucket       string `json:"hour_bucket"`
    RequestCount     int    `json:"request_count"`
    PromptTokens     int    `json:"prompt_tokens"`
    CompletionTokens int    `json:"completion_tokens"`
    TotalTokens      int    `json:"total_tokens"`
    // NOTE: No token_id field
}
```

```go
// SQL for /fe/api/usage endpoint (handlers_usage.go:167):
// SELECT coalesce(t.name, ''), u.hour_bucket, u.request_count, ...
// NOTE: Does NOT select u.token_id
```

```typescript
// TS: pkg/ui/frontend/src/types.ts:313-321
export interface HourlyUsageRow {
    token_name: string;
    token_id: string;  // <-- MISMATCH: exists in TS but Go NEVER sends this
    hour_bucket: string;
    request_count: number;
    prompt_tokens: number;
    completion_tokens: number;
    total_tokens: number;
}
```

**Impact:** The `/fe/api/usage` endpoint never returns `token_id` in data rows, so `row.token_id` in the frontend will always be `undefined`. However, the table component doesn't seem to use `token_id` in data rows — it uses `token_name` for display. The `token_id` is only present in the TypeScript type definition, not actually used in rendering.

**Severity:** MEDIUM — the type is misleading but doesn't cause visible bugs since the table uses `token_name` for display.

---

### 3.3 Cross-Cutting: Counting Hooks Wiring vs Testing

#### ✅ Counting Hooks ARE Properly Wired in `handler.go`

| Location | Line | Context |
|----------|------|---------|
| `handler.go` | 471-485 | Ultimate model success |
| `handler.go` | 805-819 | streamResult success |
| `handler.go` | 946-960 | handleNonStreamResult success |

All three locations use the same safe pattern:
```go
if rc.tokenID != "" && h.counter != nil {
    // extract usage
    go func() {
        if err := h.counter.Increment(ctx, tokenID, hourBucket, 1, ...); err != nil {
            log.Printf("failed to increment usage counter: %v", err)
        }
    }()
}
```

#### ❌ BUT: Handler Tests Pass `nil` Counter — Integration Path Never Tested

```go
// handler_test.go — ALL NewHandler calls pass nil for counter:
h := NewHandler(cfg, bus, reqStore, nil, nil, nil)          // line 248
h := NewHandler(cfg, events.NewBus(), reqStore, bufStore, nil, nil)  // lines 1480, 1533, 1567, 1619
h := NewHandler(cfg, events.NewBus(), reqStore, nil, nil, nil)      // line 1592
```

**Impact:** The `counting_hooks_test.go` verifies the MOCK pattern works correctly, but the actual handler integration path (from `Handler.handleNonStreamResult()` → counting hook → `counter.Increment()`) is NEVER exercised in tests. The real wiring could be broken without any test catching it.

---

## Critical Gaps Summary

### 🔴 HIGH Severity

| Gap | Location | Impact | Recommendation |
|-----|----------|--------|----------------|
| **Frontend has zero tests** | `pkg/ui/frontend/src/` | Date range, state management, API integration, rendering all untested | Add vitest + @testing-library/preact |
| **Handler tests never pass counter** | `pkg/proxy/handler_test.go` | Counting hook integration path untested | Add integration test with real `*usage.Counter` |
| **`token_id` in TypeScript type but never returned by Go** | `types.ts:315` vs `handlers_usage.go:167` | Misleading type definition | Either add `token_id` to Go `UsageDataRow` or remove from TypeScript |

### 🟡 MEDIUM Severity

| Gap | Location | Impact | Recommendation |
|-----|----------|--------|----------------|
| **Phase 2 missing validation error tests** | `handlers_usage_test.go` | Invalid date format, inverted range, range > 1 year not tested | Add test cases for `validateDateRange()` errors |
| **Phase 2 missing DB error tests** | `handlers_usage_test.go` | DB query/scan failures not tested | Use interface mock for `dbStore` to simulate errors |
| **counting_hooks_test.go is pattern reproduction** | `counting_hooks_test.go` | Tests mock behavior, not real handler integration | Either accept as design choice or add real integration test |
| **Phase 1 counter tests SQLite only** | `counter_test.go` | PostgreSQL dialect not tested | Add PostgreSQL dialect test cases |

### 🟢 LOW Severity

| Gap | Location | Impact | Recommendation |
|-----|----------|--------|----------------|
| `authenticate_test.go` missing generic DB error test | `authenticate_test.go` | `ErrInvalidTokenFormat` path untested | Add test for `ValidateToken` returning generic error |
| `usage_test.go` missing number overflow test | `usage_test.go` | JSON numbers > max int not tested | Add edge case for very large token counts |
| `counter_test.go` missing nil/empty input tests | `counter_test.go` | Nil context, empty strings not tested | Add test cases for bad inputs |

---

## Overall Assessment

### What Was Done Well ✅

1. **Phase 1 usage extraction tests** (`usage_test.go`): Excellent pure function tests with comprehensive edge cases covering real LLM API response formats. This is the gold standard of the feature.

2. **In-memory SQLite approach** (`counter_test.go`, `handlers_usage_test.go`): Using real databases is actually superior to interface mocks for SQL-heavy code — it validates actual SQL behavior.

3. **Interface refactor** (commit `b50cf48`): The `auth.TokenStoreInterface` for mockability was a good design decision, making auth tests clean and accurate.

4. **Counting hooks pattern**: The `if tokenID != "" && counter != nil` short-circuit pattern is well-documented and tested in isolation.

5. **Phase 2 coverage**: 33 test functions covering all 3 endpoints with real database integration is solid.

### What Needs Improvement 🔴

1. **Frontend testing is completely absent** — this is a significant risk for a user-facing feature.

2. **No integration test with real counter** — the counting hooks are wired correctly in code, but the actual handler→counter integration is never exercised.

3. **Type mismatch** between TypeScript `HourlyUsageRow` (has `token_id`) and Go `UsageDataRow` (no `token_id`).

4. **Missing validation error tests** in Phase 2 API handlers.

### Overall Mock Testing Verdict

**Backend (Phases 1-2): ADEQUATE** — Mock testing is solid with high-accuracy mocks. The approach of using in-memory SQLite over interface mocks is actually better for this codebase.

**Frontend (Phase 3): INADEQUATE** — Zero test coverage. The TypeScript type mismatch is a quality concern.

**Integration: PARTIAL** — Counting hooks pattern is tested in isolation but never exercised through the real handler code path.

---

## Recommendations (Priority Order)

1. **[HIGH]** Add frontend tests — vitest + @testing-library/preact for UsageTab, UsageTable, UsageSummaryCards, useApi
2. **[HIGH]** Add integration test that wires a real `*usage.Counter` into a test handler and verifies counting works end-to-end
3. **[HIGH]** Fix `token_id` type mismatch — add to Go `UsageDataRow` or remove from TypeScript `HourlyUsageRow`
4. **[MEDIUM]** Add Phase 2 validation error tests (invalid date format, inverted range, > 1 year)
5. **[MEDIUM]** Add Phase 2 DB error tests (use interface mock for `dbStore` to simulate `QueryContext` errors)
6. **[LOW]** Consider adding PostgreSQL dialect tests for counter and API handlers
