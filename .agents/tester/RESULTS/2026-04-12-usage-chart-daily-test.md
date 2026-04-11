# Test Report: Token Usage Tab Chart + Daily Bug Fix

**Date**: 2026-04-12
**Branch**: `feature/usage-chart-view`
**Commit**: 6ba701b → 5d00972 (quick fix during testing)
**Sessions**: go-unit-tests, frontend-build, api-verification, browser-testing

---

## Summary

| Category | Status | Details |
|----------|--------|---------|
| Go Unit Tests | ✅ PASS | 21/21 packages, go vet clean |
| Frontend Build | ✅ PASS | Vite build success (1.05s, 126.28 kB gzip) |
| Backend API Verification | ✅ PASS | Hourly/Daily endpoints verified, bug fix confirmed |
| Browser Automation | ✅ PASS | 6/6 test cases pass |
| Bug Fix Verification | ✅ PASS | Daily view correctly shows daily aggregated data |
| ensure.md (Critical) | ✅ PASS | All critical requirements met |

### Quick Fixes Applied: 1
- **formatHourBucket() null guard** — Added null/undefined check to prevent crash (W3 reviewer note)
- Commit: Included in 5d00972

---

## 1. Go Unit Tests

| Metric | Result |
|--------|--------|
| Total Packages | 24 |
| Packages with Tests | 21 |
| Passed | 21 |
| Failed | 0 |
| Go Vet | Clean |

Key packages verified:
- `pkg/usage` — ✅ PASS (usage data handling)
- `pkg/store/database` — ✅ PASS (SQL aggregation queries)
- `pkg/proxy/token` — ✅ PASS (token counting)
- `pkg/ui` — ✅ PASS (UI handler)

---

## 2. Frontend Build

| Metric | Value |
|--------|-------|
| Build Status | ✅ SUCCESS |
| Build Time | 1.05s |
| Output Size | 434.53 kB raw / 126.28 kB gzip |

**Note**: TypeScript checker reports 30 pre-existing type errors (not introduced by this branch). These are mismatches between Model type definitions and component props that existed before. The Vite build succeeds because esbuild is more lenient.

---

## 3. Backend API Verification

### 3a. Hourly View (No Regression)
```
GET /fe/api/usage?view=hourly
```
- ✅ Returns 7 rows (hourly data)
- Time format: `2026-04-11T13` (ISO 8601 hourly)
- 195 total requests, 2,407,245 prompt tokens, 14,461 completion tokens

### 3b. Daily View (Bug Fix)
```
GET /fe/api/usage?view=daily
```
- ✅ Returns 1 row (daily aggregation)
- Time format: `2026-04-11` (date only, NOT hourly)
- Values correctly summed: 195 requests, 2,407,245 prompt tokens
- **Bug Fix Verified**: Daily ≠ Hourly (different row count, different time format, summed values)

### 3c. Daily View with Token Filter
```
GET /fe/api/usage?view=daily&token_id=<id>
```
- ✅ Returns filtered daily data for specific token
- Correct aggregated values for that token

### Hourly vs Daily Comparison

| Metric | Hourly | Daily | Correct? |
|--------|--------|-------|----------|
| Rows | 7 | 1 | ✅ Fewer rows |
| Time format | `2026-04-11T13` | `2026-04-11` | ✅ Date only |
| Total requests | 195 | 195 | ✅ Sums match |

---

## 4. Browser Automation Testing

All 6 test cases executed via `opencode agent-browser`:

| Test | Description | Result | Screenshot |
|------|-------------|--------|------------|
| A | Navigate to Token Usage Tab | ✅ PASS | `/tmp/test_a_usage_tab.png` |
| B | Chart View is Default | ✅ PASS | `/tmp/test_b_chart_default.png` |
| C | Switch to Table View | ✅ PASS | `/tmp/test_c_table_view.png` |
| D | Hourly→Daily in Table Mode | ✅ PASS | `/tmp/test_d_table_hourly.png`, `/tmp/test_d_table_daily.png` |
| E | Switch Back to Chart View | ✅ PASS | `/tmp/test_e_chart_back.png`, `/tmp/test_e_chart_final.png` |
| F | Hourly/Daily in Chart Mode | ✅ PASS | `/tmp/test_f_chart_hourly.png`, `/tmp/test_f_chart_daily.png` |

### Observations:
- **Chart is default view** — confirmed by both visual inspection and code (displayMode = 'chart')
- **Chart shows multi-line** — one line per token + total line
- **Hourly/Daily toggle works in both modes** — intentional per W2
- **Daily data correctly aggregated** — fewer rows, date-only format, summed values

---

## 5. Bug Fix Verification

**Key Bug**: Daily view was showing hourly data.

**Status**: ✅ FIXED

Evidence:
1. API response: `view=daily` returns date-only buckets (`2026-04-11`) vs hourly (`2026-04-11T13`)
2. API response: Daily aggregates 7 hourly rows into 1 daily row
3. Browser: Daily table shows date-formatted rows, not hour-formatted
4. Browser: Daily chart renders different data than hourly chart

---

## 6. Reviewer Notes

| Note | Status |
|------|--------|
| W3: formatHourBucket() null guard | ✅ Fixed (quick fix applied) |
| W2: Hourly/Daily toggle in both modes | ✅ Confirmed intentional |

---

## ensure.md Validation

### Critical
- [x] All Go unit tests pass (`go test ./...`) — 21/21 packages
- [x] `go vet ./...` passes — Clean
- [x] Full project builds without compilation errors — ✅
- [x] Frontend builds successfully — ✅ (TypeScript errors are pre-existing)

---

## Documentation Updated
- [x] RESULTS/2026-04-12-usage-chart-daily-test.md — This report

---

## Overall Status
- Go Tests: ✅ PASS
- Frontend Build: ✅ PASS
- Backend API: ✅ PASS
- Browser Automation: ✅ PASS (6/6)
- Bug Fix: ✅ VERIFIED FIXED
- **Testing Complete**: ✅ READY FOR MERGE
