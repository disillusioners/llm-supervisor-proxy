# Phase 4 Secondary Upstream Model Tests Review

**Date:** 2026-04-13
**Commit:** 9b20182
**Status:** Needs Work (2 critical, 3 warnings, 4 suggestions)

## Key Findings

### Critical
1. **No end-to-end model swap verification** in `race_executor_test.go` — tests only verify `ResolveInternalConfig()` returns correct config, never call `executeInternalRequest()` to verify the secondary model name actually reaches the provider.
2. **No peak hour + secondary combo test** in runtime layer — config_secondary_test.go has peak+secondary coexistence at validation level, but zero runtime/proxy layer tests verify "main uses peak, retry uses secondary".

### Warnings
1. `TestRaceCoordinator_SecondaryFlagAccessible` only tests getter/setter, not actual retry behavior
2. Edge cases missing: whitespace-only model name, secondary=primary
3. `time.Sleep` in retry tests (minor flakiness risk)

## Note: Opencode Session Accuracy
- Data-layer session INCORRECTLY claimed database_test.go had no SecondaryUpstreamModel tests — it actually has 8 dedicated test functions with thorough CRUD coverage.
- Runtime session correctly identified the model swap verification gap.
