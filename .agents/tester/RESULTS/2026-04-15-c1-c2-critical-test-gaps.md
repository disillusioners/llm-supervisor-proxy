# C1+C2 Critical Test Gap Fix Results

**Date:** 2026-04-15
**Commit:** 3cd5d56
**Branch:** feature/secondary-upstream-model

## Summary

Fixed 2 critical test gaps identified by Phase 4 reviewer. Both gaps were at the execution level — existing tests only verified config resolution, not actual model name propagation to upstream providers.

## Tests Added (8 new test functions, 1144 lines)

### C1: End-to-End Model Swap Verification

| Test Function | What It Verifies |
|---------------|------------------|
| `TestExecuteInternalRequest_SecondaryModelSwap_E2E_NonStream` | Provider receives secondary model (glm-4-flash) not primary (glm-5.0) in non-streaming |
| `TestExecuteInternalRequest_SecondaryModelSwap_E2E_Stream` | Provider receives secondary model in streaming |
| `TestExecuteInternalRequest_NoSecondary_UsesPrimary_E2E` | Falls back to primary when no secondary configured |
| `TestExecuteInternalRequest_SecondaryFalse_UsesPrimary_E2E` | Uses primary when useSecondary=false |

### C2: Peak Hour + Secondary Combo Runtime Test

| Test Function | What It Verifies |
|---------------|------------------|
| `TestExecuteInternalRequest_PeakHourAndSecondary_Combo_NonStream` | Main→peak model, Second→secondary model |
| `TestExecuteInternalRequest_PeakHourAndSecondary_Combo_Stream` | Same combo for streaming |
| `TestRaceCoordinator_PeakHourWithSecondaryModel` | Coordinator-level combo behavior |
| `TestRaceCoordinator_PeakHourModelOnly_NoSecondary` | Peak hour alone works |
| `TestRaceCoordinator_SecondaryOverridesPeakHour` | Secondary takes precedence |

## Files Changed

| File | Change | Lines |
|------|--------|-------|
| `pkg/proxy/race_executor.go` | Added `newProviderClient` var for test injection | +5/-2 |
| `pkg/proxy/race_executor_test.go` | 5 executor-level E2E tests | +844 |
| `pkg/proxy/race_coordinator_test.go` | 3 coordinator-level combo tests | +297 |

## Verification

| Check | Status |
|-------|--------|
| `go test ./... -count=1` | ✅ ALL PASS (23 packages) |
| `go vet ./...` | ✅ CLEAN |
| Build | ✅ PASS |
| Commit | ✅ 3cd5d56 |

## Key Implementation

- **`mockProviderWithCapture`**: Mock provider that records `req.Model` from `ChatCompletion()` and `StreamChatCompletion()` calls
- **`newProviderClient`**: Test-injectable variable in `race_executor.go` for replacing the provider factory
- Tests verify at the **provider level** — proving model names actually reach upstream
