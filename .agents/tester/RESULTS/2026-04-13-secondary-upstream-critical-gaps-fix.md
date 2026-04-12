# Test Report: Critical Test Gaps Fix (C1 + C2)
Date: 2026-04-13
Commit: 3cd5d56

## Summary
- **New Test Functions**: 8 (4 for C1, 4 for C2)
- **New Lines**: 1141 (+ 3 lines source change for testability)
- **Total Tests**: 946 PASS, 0 FAIL (up from 935)
- **Packages**: 21/21 PASS
- **Quick Fixes**: 0
- **ensure.md**: ALL CRITICAL PASS

## Critical Gaps Fixed

### C1: Execution-Level Model Swap Verification
**Problem**: All secondary tests only verified `ResolveInternalConfig()` return values. None called `executeInternalRequest()` to verify the model name actually reaches the provider.

**Fix**: Added mock provider that captures the model name from actual completion calls.

| Test Function | Verifies |
|---------------|----------|
| `TestExecuteInternalRequest_SecondaryModelSwap_E2E_NonStream` | Provider receives "glm-4-flash" (secondary) not "glm-5.0" (primary) — non-streaming |
| `TestExecuteInternalRequest_SecondaryModelSwap_E2E_Stream` | Same — streaming request |
| `TestExecuteInternalRequest_NoSecondary_UsesPrimary_E2E` | Empty secondary → provider receives primary model |
| `TestExecuteInternalRequest_SecondaryFalse_UsesPrimary_E2E` | Flag=false → provider receives primary even if secondary configured |

### C2: Peak Hour + Secondary Combo Runtime Test
**Problem**: Peak hour + secondary combo only tested at config validation level. No runtime tests.

**Fix**: Added integration tests through race coordinator verifying model routing at execution level.

| Test Function | Verifies |
|---------------|----------|
| `TestRaceCoordinator_PeakHourWithSecondaryModel` | Main=peak model, second=secondary model (NOT peak) |
| `TestRaceCoordinator_PeakHourModelOnly_NoSecondary` | Peak active, no secondary → second uses primary |
| `TestRaceCoordinator_SecondaryOverridesPeakHour` | Secondary independent of peak hour settings |
| `TestRaceCoordinator_NoPeakHour_UsesInternalModel` | Peak disabled → main uses regular internal model |

## Files Changed

| File | Changes |
|------|---------|
| `pkg/proxy/race_executor.go` | +3 lines — injectable `newProviderClient` for testability |
| `pkg/proxy/race_executor_test.go` | +844 lines — 4 new E2E model swap tests + mock provider |
| `pkg/proxy/race_coordinator_test.go` | +297 lines — 4 new peak hour + secondary combo tests |

## Verification

| Check | Status |
|-------|--------|
| `go test ./...` | ✅ PASS (21/21 packages, 946 tests) |
| `go vet ./...` | ✅ PASS (clean) |
| All new tests individually | ✅ 8/8 PASS |

## Test Matrix Coverage (Updated)

| Scenario | Internal | SecondaryUpstreamModel | Expected | Status |
|----------|----------|------------------------|----------|--------|
| No secondary configured | true | "" | modelTypeSecond uses primary | ✅ PASS (config + E2E) |
| Secondary configured | true | "glm-4-flash" | modelTypeSecond uses "glm-4-flash" | ✅ PASS (config + E2E) |
| Non-internal with secondary | false | "glm-4-flash" | Rejected by validation | ✅ PASS |
| Peak hour + secondary | true | "glm-4-flash" | Main=peak, second=secondary | ✅ PASS (E2E runtime) |

## Commit
- **Hash**: 3cd5d56
- **Message**: `test: add execution-level secondary model swap and peak hour combo tests`
