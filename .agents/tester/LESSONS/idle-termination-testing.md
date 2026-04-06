# Idle Termination Feature Testing

**Date**: 2026-04-06
**Commit**: 068aa0d
**Feature Commits**: 0c37be9, 5bdacb5

## Feature Summary
When a stream winner is selected and upstream sends no data for a configurable timeout (default 2m), the stream is terminated with an SSE error.

- `idle_termination_enabled` (bool, default: true)
- `idle_termination_timeout` (Duration, default: 120s)

## Files Changed
- `pkg/config/config.go` — Config fields, defaults, validation, env overrides
- `pkg/proxy/handler.go` — streamResult idle check, ConfigSnapshot.Clone()
- `pkg/store/database/store.go` — mergeConfig presence detection

## Tests Written

### A. Config & Validation (pkg/config/config_test.go)
Added to `TestConfig_Validate` table:
1. `idle_termination_enabled_with_valid_timeout` — enabled=true, timeout=60s → PASS
2. `idle_termination_enabled_with_timeout_too_low` — enabled=true, timeout=500ms → error
3. `idle_termination_enabled_with_zero_timeout` — enabled=true, timeout=0 → error
4. `idle_termination_disabled_with_zero_timeout` — enabled=false, timeout=0 → PASS
5. `idle_termination_disabled_with_valid_timeout` — enabled=false, timeout=30s → PASS

Added to `TestDefaults`:
6. `IdleTerminationEnabled == true`
7. `IdleTerminationTimeout == 120s`

Added to `TestManager_Load_EnvOverrides`:
8. `IDLE_TERMINATION_ENABLED=false` overrides
9. `IDLE_TERMINATION_TIMEOUT=30s` overrides

### B. mergeConfig & Persistence (pkg/store/database/database_test.go)
- `TestIdleTerminationPersistence` — Saves and loads across simulated restart
- `TestIdleTerminationMergeConfig` — Partial update preserves values, explicit disable, custom timeout

### C. Handler Streaming Tests (pkg/proxy/handler_test.go)
- `TestConfig_Clone_IdleTermination` — ConfigSnapshot deep-copies idle termination fields
- `TestIdleTermination_Triggered` — Mock upstream hangs → SSE error sent (~2s test)
- `TestIdleTermination_Disabled_NoTermination` — Disabled → normal streaming completes
- `TestIdleTermination_NormalStreamingNotTerminated` — Active streaming → not falsely terminated

## Key Patterns Learned
- `isIdleTerminationProvided()` checks `Enabled || Timeout != 0` — partial updates need to set Timeout to trigger merge
- Handler tests use `t.Setenv()` before creating config manager for env-based config
- `streamResult` idle check is in the `ticker.C` case — requires ~1 tick delay (100ms-1s) to fire
- The `mock-hang` pattern in mockLLMHandler blocks after a few chunks, triggering idle timeout naturally
