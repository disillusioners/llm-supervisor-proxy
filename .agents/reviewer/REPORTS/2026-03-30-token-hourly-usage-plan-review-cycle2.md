# Plan Review (Cycle 2): Token-level Hourly Request Counting

**Reviewer:** Reviewer Agent  
**Date:** 2026-03-30  
**Project:** llm-supervisor-proxy  
**Plan Location:** `.agents/shared/working/token-hourly-usage/`  
**Plan Files:** `plan-overview.md`, `phase1-plan.md`, `phase2-plan.md`, `phase3-plan.md`, `decisions.md`  
**Status:** 🟡 **Ready with 3 New Issues to Address**

---

## Review Summary

| Metric | Value |
|--------|-------|
| **Verdict** | 🟡 **Ready — 3 New Issues Found** |
| **Previous C-1 (Invented functions)** | ✅ Resolved |
| **Previous C-2 (Usage not extracted)** | ✅ Resolved (but NEW gaps found) |
| **Previous C-3 (Wrong token propagation)** | ✅ Resolved |
| **Previous C-4 (Naming inconsistency)** | ✅ Resolved |
| **Previous C-5 (PostgreSQL unaddressed)** | ✅ Resolved |
| **Previous C-6 (No DB access in UI server)** | ✅ Resolved |
| **New Issues** | 3 (2 warnings, 1 suggestion) |
| **Sessions Used** | ses_2be4feb37ffeoDvHOV7plIBMyh, ses_2be4c7296ffeuK7Wao5kWVGn1T |

---

## Executive Summary

The revised plan is **substantially improved**. All 6 critical issues from Cycle 1 have been addressed:

| Issue | Resolution |
|-------|------------|
| C-1: Invented functions | ✅ Correctly mapped to 7 actual inline finalization points |
| C-2: Usage not extracted | ✅ Part A added with 4 tasks, code paths identified |
| C-3: Wrong propagation | ✅ Simplified to `(*AuthToken, bool)` return, 1 caller to update |
| C-4: Naming inconsistency | ✅ Unified to `token_hourly_usage` everywhere |
| C-5: PostgreSQL unaddressed | ✅ Uses `*sql.DB` directly with dialect branching throughout |
| C-6: No DB in UI server | ✅ Part A in Phase 2 with struct + constructor + main.go changes |

However, validation against actual code reveals **3 new issues**:

1. **NEW-1 (Warning)**: Ultimate Model requests never populate `reqLog.Usage` — usage extraction in `streamResult()`/`handleNonStreamResult()` only covers race coordinator paths, not Ultimate Model's direct writes
2. **NEW-2 (Warning)**: External streaming providers may not inject usage into SSE chunks — the reverse-chunk-scan approach won't find usage for external providers
3. **NEW-3 (Suggestion)**: Task 3A is marked TBD but the code path (`handleInternalNonStream`) is already traceable

The plan is **80% ready for implementation**. The new issues are addressable with small additions to Phase 1 Part A.

---

## Validation of the 6 Previous Critical Issues

### C-1: Invented Function Names ✅ RESOLVED

**Finding:** The revised plan correctly identifies all 7 actual finalization points with **exact line numbers** that match the codebase:

| # | Point | Plan Lines | Actual Lines | `h.store.Add`? |
|---|-------|-----------|--------------|-----------------|
| 1 | Auth failure | 337-342 | 337-342 ✅ | Yes (line 342) |
| 2 | Ultimate retry exhausted | 382-388 | 382-388 ✅ | Yes (line 388) |
| 3 | Ultimate execution error | 428-432 | 428-432 ✅ | Yes (line 432) |
| 4 | Ultimate success | 454-457 | 454-457 ✅ | Yes (line 457) |
| 5 | All race models failed | 537-541 | 537-541 ✅ | Yes (line 541) |
| 6 | Streaming success | 754-758 | 754-758 ✅ | Yes (line 758) |
| 7 | Non-streaming success | 859-868 | 859-868 ✅ | Yes (line 868) |

The line numbers are **precisely correct** — confirmed by reading `pkg/proxy/handler.go`.

Additionally, the plan correctly notes there is an 8th `store.Add()` at line 411, but correctly identifies it as an **intermediate update** (sets status to "running" before ultimate model execution), not a finalization point. ✅

---

### C-2: Usage Not Extracted ✅ RESOLVED (but new gaps found)

**Finding:** Part A was added with 4 tasks covering the code paths. The plan now correctly identifies:
- `streamResult()` at lines 635-768 (`case <-buffer.Done()`) — reverse chunk scan to find usage
- `handleNonStreamResult()` at lines 793-884 (`resp["usage"]` extraction)
- `handleInternalNonStream()` in `race_executor.go:203-225` — internal non-streaming path
- `SetUsage()`/`GetUsage()` wiring in race executor

**Code path analysis is correct:**
- For **internal streaming**: Usage is injected into the final SSE chunk at `race_executor.go:480-488`. The plan's reverse chunk scan will find it. ✅
- For **internal non-streaming**: `handleInternalNonStream()` marshals `ChatCompletionResponse` (which includes `Usage` field per `pkg/providers/interface.go:91-99`) to JSON and puts it in the buffer. `handleNonStreamResult()` sends this directly to the client. The plan's `resp["usage"]` extraction works. ✅
- For **external non-streaming**: Same as internal — `resp["usage"]` extraction works. ✅

**However, 2 NEW gaps identified — see NEW-1 and NEW-2 below.**

---

### C-3: Wrong Token Propagation ✅ RESOLVED

**Finding:** The revised approach is correct and simple:
- `authenticate()` changes from `func (h *Handler) authenticate(r *http.Request) bool` to `func (h *Handler) authenticate(r *http.Request) (*auth.AuthToken, bool)`
- Single caller at line 336 updated to use both return values
- `tokenID` and `tokenName` added to `requestContext` struct

This is the simplest possible change. Only 1 call site needs updating. ✅

---

### C-4: Naming Inconsistency ✅ RESOLVED

**Finding:** Table name is now `token_hourly_usage` consistently throughout all 5 plan files. ✅

---

### C-5: PostgreSQL Unaddressed ✅ RESOLVED

**Finding:** The revised plan uses `*sql.DB` directly with dialect branching throughout:
- Phase 1 Part C (counter): `$1...$6` for PostgreSQL, `?` for SQLite
- Phase 2 Part B (API queries): Same dialect branching pattern
- The plan explicitly states "NOT sqlc" and follows the existing `auth/store.go` pattern

**SQLite ON CONFLICT DO UPDATE:** The plan uses `ON CONFLICT (token_id, hour_bucket) DO UPDATE SET` for SQLite. This is **valid** — `modernc.org/sqlite v1.46.1` is based on SQLite 3.47, which supports `ON CONFLICT`. The project's existing `QueryBuilder` uses `INSERT OR REPLACE` as a stylistic choice, not a requirement. The plan's approach works. ✅

---

### C-6: No DB Access in UI Server ✅ RESOLVED

**Finding:** Phase 2 Part A correctly identifies all 3 changes needed:
1. Add `dbStore *database.Store` field to `Server` struct
2. Update `NewServer()` constructor signature and body
3. Update `main.go` line ~93 call site

The plan shows exact before/after code for all 3 locations. ✅

---

## New Issues Found

### 🟡 NEW-1: Ultimate Model Requests Never Populate Usage (Major Gap)

**Severity:** 🟡 Warning  
**Location:** Phase 1, Part A — the usage extraction tasks  
**Impact:** Ultimate Model requests (when triggered) will never record token usage

**Finding:** The usage extraction tasks (1A and 2A) only cover the **race coordinator** code paths:

1. **`streamResult()`** — receives responses from race coordinator, NOT Ultimate Model
2. **`handleNonStreamResult()`** — receives responses from race coordinator, NOT Ultimate Model

Ultimate Model has **separate code paths** that bypass both:

**Ultimate Model streaming** (`pkg/ultimatemodel/handler_internal.go`):
- Writes SSE directly to client at lines 273-291
- Injects usage into final chunk (lines 280-288) — **usage IS present in the stream**
- But the client is the SSE consumer, not the proxy's `streamResult()` — usage is lost to the client

**Ultimate Model non-streaming** (`pkg/ultimatemodel/handler_internal.go:60-66` and `handler_external.go:107-110`):
- Uses `json.NewEncoder(w).Encode(resp)` (internal) or `io.Copy(w, resp.Body)` (external)
- **No extraction of `resp.Usage`** — usage goes directly to the client
- `reqLog.Usage` is never set

**The problem:** When Ultimate Model is triggered, the request goes through Ultimate Model's own handler which writes directly to the response writer. The race coordinator's `streamResult()` and `handleNonStreamResult()` are never called for Ultimate Model requests.

**Evidence:**
- `handler.go:425`: `h.ultimateHandler.Execute(r.Context(), w, r, ...)` — writes directly to `w`
- `ultimatemodel/handler_internal.go:65`: `json.NewEncoder(w).Encode(resp)` — sends JSON to client directly
- `ultimatemodel/handler_external.go:107`: `io.Copy(w, resp.Body)` — copies upstream body to client directly

**Impact on counting:** Requests WILL be counted (the finalization points at lines 454-457 run), but `reqLog.Usage` will always be nil for Ultimate Model requests. Token usage will be 0 for those requests.

**Mitigation:** The plan should add a note that Ultimate Model usage is not captured, or add extraction in the Ultimate Model handlers themselves. The former is acceptable (Ultimate Model is typically a fallback, not the primary path).

**Recommendation:** Add to Phase 1 Constraints: "Usage extraction in streamResult() and handleNonStreamResult() covers race coordinator requests only. Ultimate Model requests will have usage=0. This is acceptable for initial implementation since Ultimate Model is a fallback path."

---

### 🟡 NEW-2: External Streaming Providers May Not Have Usage in SSE Chunks

**Severity:** 🟡 Warning  
**Location:** Phase 1, Task 1A  
**Impact:** External streaming requests may not have usage data in the SSE stream

**Finding:** The plan's reverse chunk scan approach relies on usage being present in the SSE chunks. This works for **internal streaming** (`race_executor.go:480-488` injects usage into final chunk), but **external streaming** passes chunks through without modification.

In `race_executor.go` `handleStreamingResponse()` (lines 780-969):
- Reads chunks from upstream external provider
- Passes them through tool call buffers
- Adds to response buffer
- **No usage injection** — external providers may or may not include usage in their SSE stream

If an external provider (e.g., LiteLLM, some OpenAI-compatible API) doesn't send usage in streaming chunks, the plan's reverse scan will find nothing, and `reqLog.Usage` will be nil.

**Note:** The plan already handles nil usage gracefully ("count request, tokens = 0"), so this is a degradation of data quality rather than a failure. But it should be documented.

**Recommendation:** Add to Phase 1 Constraints: "For external streaming providers, usage extraction depends on the upstream provider including usage in SSE chunks. Not all providers do this. Nil usage will be treated as 0 tokens."

---

### 🟢 NEW-3: Task 3A is Marked TBD But Code Path Is Traceable

**Severity:** 🟢 Suggestion  
**Location:** Phase 1, Task 3A  
**Impact:** Low — task is feasible, not a blocker

**Finding:** The task says:

> **Note:** This task may require additional investigation during implementation to find the exact code path. Mark as TBD if the internal non-streaming provider code path is not found in initial exploration.

The code path **is** traceable. Session analysis found:

**Internal non-streaming code path:**
1. `race_executor.go:203-225` — `handleInternalNonStream()` marshals `ChatCompletionResponse` (which includes `Usage`) to JSON
2. Adds to buffer at line 220: `upstreamReq.buffer.Add(data)`
3. Winner selected, `handleNonStreamResult()` runs
4. Sends buffered body to client at line 842: `w.Write(finalBody)`

**The key insight:** Internal non-streaming already works through Task 2A's `resp["usage"]` extraction in `handleNonStreamResult()`. The `handleInternalNonStream()` function itself doesn't need modification — it already marshals the response with usage included.

**Recommendation:** Remove the TBD marking. Task 3A can be marked as "covered by Task 2A — internal non-streaming response goes through handleNonStreamResult() which extracts resp['usage']".

---

## Minor Observations (Not Issues)

### The `store.Usage` vs `providers.Usage` Structs

The plan references `store.Usage` (from `pkg/store/memory.go:26-31`) which is the correct struct. The `providers.Usage` (from `pkg/providers/interface.go:109-114`) has identical fields. Both are `int` for token counts and `float64` in JSON (which is correctly cast to `int` in the extraction code). This is handled correctly in the plan. ✅

### The Counter's GetTokenUsage Returns `[]HourlyUsageRow` 

Phase 1 Part C (Task 2C) defines `HourlyUsageRow` and `GetTokenUsage()` on the counter. Phase 2 Part B (API handlers) duplicates this struct definition. This duplication is fine (no shared package), but the plan should note that the struct definition appears in both places. ✅

### Counting Uses `context.Background()` in the Goroutine

The plan uses `context.Background()` in the goroutine. This is fine — the UPSERT is a simple write that doesn't need request-scoped context. The existing timeout context (`rc.baseCtx`) would cancel if the request is aborted, but the goroutine continues anyway (it's fire-and-forget). This is acceptable. ✅

### Logging Uses `log.Printf` — Matches Existing Pattern

The plan uses `log.Printf("failed to increment usage counter: %v", err)`. The codebase uses the standard `log` package throughout (confirmed by grep). No structured logging is introduced. ✅

---

## Assessment by Criterion (Updated)

### Completeness — 🟡 Partial (Improved)

**What's improved:** All 6 previous gaps are now addressed. Part A of Phase 1 is comprehensive.

**Remaining gaps:**
- Ultimate Model usage extraction is missing (NEW-1) — acceptable as noted above
- External streaming may not have usage (NEW-2) — graceful degradation

**What's well-covered:**
- Both SQLite and PostgreSQL dialects ✅
- All race coordinator paths ✅
- Internal non-streaming ✅
- Token identity propagation ✅
- DB injection to UI server ✅
- API endpoints ✅
- Frontend components ✅

### Feasibility — ✅ Much Improved

All 6 previous blockers are resolved. The remaining issues are:
- NEW-1 (Ultimate Model): Acceptable limitation, documented
- NEW-2 (External streaming): Graceful degradation
- NEW-3 (Task 3A TBD): Not actually TBD

The plan is **feasible as written** with the noted limitations.

### Phase Ordering — ✅ Correct

Dependencies are correct and unchanged from Cycle 1. ✅

### Risk Assessment — ✅ Improved

The risks section is now comprehensive and accurate:
- "Usage extraction may differ across LLM providers" ✅ — matches NEW-2
- "7 finalization points" ✅
- "authenticate() signature change" ✅
- "Token usage data not populated" ✅ — graceful degradation noted

### Architecture Decisions — ✅ All Sound

| # | Decision | Assessment |
|---|----------|------------|
| 1 | DB-level UPSERT | ✅ Sound |
| 2 | Hourly bucket format | ✅ Sound |
| 3 | authenticate() refactoring | ✅ Correctly simplified |
| 4 | Counter package location | ✅ Sound |
| 5 | Frontend CSS charts | ✅ Sound |
| 6 | Skip counting when auth disabled | ✅ Sound |
| 7 | Token name JOIN | ✅ Sound |
| 8 | No sqlc, raw SQL with dialect branching | ✅ Correctly updated |
| 9 | Counting goroutine | ✅ Sound with error logging |
| 10 | Usage extraction strategy | ✅ Correct, with noted limitations |

### Database Design — ✅ Sound

- `token_hourly_usage` with composite PK ✅
- Indexes on both columns ✅
- `ON CONFLICT DO UPDATE` with increment ✅
- Dual-dialect migrations ✅

### API Design — ✅ Sound

Same as Cycle 1 assessment — RESTful, good filtering, proper aggregation.

### Frontend Approach — ✅ Sound

Same as Cycle 1 assessment — well-scoped, no new dependencies.

### Testing Strategy — 🟢 Still Light

Still no explicit testing tasks. Given the complexity of the feature (spans 3 modules, has race conditions with goroutines), consider adding:
- Integration test: send request → check DB for count
- Unit test: `extractUsageFromChunk()` with mock chunks

---

## Recommendations

### Must Address

None — no blocking issues remain.

### Should Address

1. **NEW-1**: Add a note in Phase 1 Constraints: "Usage extraction in streamResult() and handleNonStreamResult() covers race coordinator requests only. Ultimate Model requests (when triggered) will have usage=0. This is acceptable since Ultimate Model is a fallback path."

2. **NEW-2**: Add a note in Phase 1 Constraints: "For external streaming providers, usage extraction depends on the upstream provider including usage in SSE chunks. Nil usage is gracefully handled as 0 tokens."

3. **NEW-3**: Remove the TBD marking from Task 3A. The internal non-streaming path is already covered by Task 2A's `handleNonStreamResult()` extraction.

### Nice to Have

4. Add integration test task for counting verification (send test request → check DB)

---

## Conclusion

The revised plan is **substantially improved** and addresses all 6 critical issues from Cycle 1. The 3 new issues identified are **non-blocking**:

- **NEW-1**: Ultimate Model usage gap — acceptable limitation, can be documented
- **NEW-2**: External streaming usage gap — graceful degradation, documented
- **NEW-3**: Task 3A TBD — not actually TBD, can be clarified

The plan is **ready for implementation** with the 3 recommended documentation additions. No structural changes are required.

---

## Comparison: Cycle 1 vs Cycle 2

| Criterion | Cycle 1 | Cycle 2 |
|-----------|---------|---------|
| Critical Issues | 6 | 0 |
| New Issues | — | 3 (warnings/suggestions) |
| Blocking Issues | 6 | 0 |
| Status | 🔴 Needs Major Revisions | 🟡 Ready with Notes |

The planner did excellent work addressing all previous blockers. The remaining issues are well-understood and documented.

---

*Review generated by Reviewer Agent. Sessions: ses_2be4feb37ffeoDvHOV7plIBMyh, ses_2be4c7296ffeuK7Wao5kWVGn1T*
