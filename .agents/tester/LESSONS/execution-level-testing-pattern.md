# Execution-Level Testing Gap (C1+C2)

**Date:** 2026-04-15
**Commit:** 3cd5d56
**Branch:** feature/secondary-upstream-model

## Lesson

When testing features that transform values (e.g., model name swapping), it's not sufficient to only test the config resolution layer. Tests must verify the transformed value actually reaches the downstream consumer (provider).

### Pattern: Capture-and-Assert at Provider Level

1. Create a mock provider that records the `req.Model` it receives
2. Inject the mock via a test-overridable factory variable (e.g., `newProviderClient`)
3. Execute the full code path (executor or coordinator)
4. Assert the recorded model matches expected, not just the config value

### Why This Matters

If the model swap code were deleted from `executeInternalRequest()`, all config-level tests would still pass — giving false confidence. Only execution-level tests catch this.

### Files

- `pkg/proxy/race_executor.go`: Added `newProviderClient` var for test injection
- `pkg/proxy/race_executor_test.go`: `mockProviderWithCapture` pattern
- `pkg/proxy/race_coordinator_test.go`: Coordinator-level combo tests
