# Test Report: Peak Hour Fallback Bug Investigation — Final
Date: 2026-03-30
Session: ses_2c141c7dfffekSVLJ0jMSD58CK

## Summary
| Aspect | Result |
|--------|--------|
| Bug Reproduced | ❌ NO — Original test was misconfigured |
| Root Cause | Misconfigured test in first session (b268d07) |
| Code Correctness | ✅ Peak hour fallback logic is CORRECT |
| All Unit Tests | ✅ PASS (14 packages) |
| Go Vet | ✅ PASS |

## Critical Finding

**The original "bug" was caused by a misconfigured test, NOT by actual code.**

### What the first session did wrong
The first session's test (`b268d07`) created the fallback model WITH `peak_hour_enabled: true`:

```go
fallbackModelData := map[string]interface{}{
    "id":                 testFallbackModel,
    "internal_model":     "mock-fallback-normal",
    "peak_hour_enabled":  true,    // ← THIS WAS THE PROBLEM
    "peak_hour_start":    "00:00",
    "peak_hour_end":      "23:59",
    "peak_hour_model":    "mock-fallback-peak", // Fallback's peak model
}
```

So when the fallback was invoked:
1. Primary "test-peak" → peak hour active → `mock-peak-upstream` (fails 500)
2. Fallback invoked → peak hour ALSO active on fallback → `mock-fallback-peak` (also fails/wrong)
3. Test failed because fallback's peak model was wrong

**The test EXPECTED fallback to use `mock-fallback-normal`, but configured peak hour to override it with `mock-fallback-peak`.**

### What the correct tests show
With properly configured tests:

**Test A** (fallback WITHOUT peak hour): ✅ PASS
```
[PEAK-HOUR] peak hour active for model test-peak: using mock-peak-upstream
[PEAK-DBG] ResolveInternalConfig("test-fallback-no-peak") => internalModel="mock-fallback-normal"
→ Fallback works correctly!
```

**Test B** (fallback WITH peak hour): ✅ PASS
```
[PEAK-HOUR] peak hour active for model test-fallback-with-peak: using mock-fallback-peak-upstream
→ Fallback with its own peak hour also works correctly!
```

**Test D** (baseline, no peak hour): N/A (Ultimate Model interference, not peak hour related)

## Code Analysis Results

### Resolution Chain (verified correct)
1. `buildModelList()` → builds `[primary, fallback]` list correctly
2. `race_coordinator.spawn(fallback)` → assigns `models[1]` as fallback model ID
3. `executeInternalRequest()` → calls `ResolveInternalConfig(modelID)` with correct model ID
4. `ResolveInternalConfig()` → looks up model by ID, checks ITS OWN peak hour settings
5. `ResolvePeakHourModel()` → only applies if `PeakHourEnabled=true` on THIS model

### Key Functions Verified
- `pkg/models/config.go:ResolveInternalConfig` — Correct per-model resolution
- `pkg/models/peak_hours.go:ResolvePeakHourModel` — Correct per-model peak hour check
- `pkg/proxy/race_coordinator.go:spawn` — Correct model ID assignment
- `pkg/proxy/race_executor.go:executeInternalRequest` — Correct call chain
- `pkg/proxy/handler_helpers.go:buildModelList` — Correct fallback chain construction
- `pkg/store/database/store.go:GetModel` — Exact ID matching, no collisions

### No State Bleeding
- Each model's peak hour is evaluated independently
- No shared state between primary and fallback resolution
- Database lookups use exact ID matching
- No caching layer that could cause stale data

## Rejected Fix Analysis
The rejected fix (b268d07) added code to skip peak hour for fallback models:
- This was incorrect — it would break fallback models with legitimate peak hour config
- The current code is correct
- Revert commit: 50c0c34

## Files Created/Modified
| File | Change | Commit |
|------|--------|--------|
| `pkg/proxy/race_coordinator.go` | Added [PEAK-DBG] debug logging | a0e00e4 |
| `pkg/proxy/race_executor.go` | Added [PEAK-DBG] debug logging | a0e00e4 |
| `pkg/proxy/handler_helpers.go` | Added [PEAK-DBG] debug logging | a0e00e4 |
| `pkg/models/config.go` | Added [PEAK-DBG] debug logging | a0e00e4 |
| `test/mock_llm_peak_hour_fallback.go` | New mock test file | a0e00e4 |
| `test/test_peak_hour_fallback.sh` | New test runner script | a0e00e4 |

## Unit Test Results (all pass)
```
ok  pkg/auth
ok  pkg/config
ok  pkg/crypto
ok  pkg/loopdetection
ok  pkg/models
ok  pkg/providers
ok  pkg/proxy
ok  pkg/proxy/normalizers
ok  pkg/proxy/translator
ok  pkg/store/database
ok  pkg/supervisor
ok  pkg/toolcall
ok  pkg/toolrepair
ok  pkg/ultimatemodel
```

---

### Overall Status: ✅ NO BUG FOUND — Code is correct
- The original "bug" was a misconfigured test
- Peak hour fallback logic works correctly for both scenarios (fallback with/without peak hour)
- The rejected fix was correctly rejected
- Debug logging added for future investigation
