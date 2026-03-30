# Mock Test: Peak Hour Fallback Bug Reproduction

### Metadata
- **Created**: 2026-04-11
- **Script**: `test/mock_llm_peak_hour_fallback.go`
- **Language**: Go
- **Status**: PLANNED

### Configuration
- **Timeout**: 60 seconds
- **Mock LLM Port**: 19001
- **Proxy Port**: 19002
- **Cleanup**: Kill processes on all ports before/after

### What It Tests
- Peak hour model switching + fallback chain behavior
- When peak hour is active AND peak upstream fails, does the fallback proxy_model get invoked?

### Bug Description
When a proxy_model has peak hour active and the peak hour upstream_model fails, the fallback proxy_model is NOT being invoked. Static code analysis couldn't find the bug — we need a runtime reproduction via mock test.

### Architecture (MUST understand)
There are 2 kinds of models:
- **proxy_model** — our proxy's model ID (e.g., `glm-5`, `smart`) — used for routing, fallback chains
- **upstream_model** — actual provider model name (e.g., `glm-4.7`, `MiniMax-M2.7-highspeed`) — sent to provider API

### Expected Flow
```
Client → proxy_model "test-peak" 
  → Peak hour ACTIVE (wide range to cover test time)
  → upstream_model = "mock-peak-upstream" (instead of "mock-normal-upstream")
  → mock-peak-upstream FAILS (simulated)
  → Fallback: proxy_model "test-fallback" should invoke
    → "test-fallback" has upstream_model = "mock-fallback-upstream"
    → Should succeed
```

### Mock Services Required
- Mock LLM Server on port 19001:
  - If `model` == `"mock-peak-upstream"` → return error (500 or 429)
  - If `model` == `"mock-fallback-upstream"` → return success (200 with valid chat completion)
  - Debug logging showing exactly what model name it receives

### Test Model Configuration (2 proxy_models)

**Model 1: "test-peak"** (primary)
- `internal: true`
- `internal_model: "mock-normal-upstream"`
- `fallback_chain: ["test-fallback"]`
- `peak_hour_enabled: true`
- `peak_hour_start: "00:00"`
- `peak_hour_end: "23:59"`
- `peak_hour_timezone: "+0"`
- `peak_hour_model: "mock-peak-upstream"`

**Model 2: "test-fallback"** (fallback)
- `internal: true`
- `internal_model: "mock-fallback-upstream"`
- No peak hour config

### Test Scenarios
1. **Happy path without peak hour failure**: Verify mock-fallback-upstream returns success
2. **Peak hour active + upstream failure**: Verify fallback chain activates
3. **Bug reproduction**: If fallback doesn't activate, trace WHERE it breaks with debug logging

### Success Criteria
- [ ] Mock LLM server starts and responds correctly
- [ ] Test can reproduce the bug (fallback not invoked) OR confirm it's fixed
- [ ] If bug reproduced: Exact location and runtime values documented
- [ ] All processes cleaned up after test

### Key Files to Study/Modify
- `pkg/proxy/race_coordinator.go` — manages race/fallback logic
- `pkg/proxy/race_executor.go` — executes upstream requests
- `pkg/proxy/handler.go` — request handler
- `pkg/proxy/handler_functions.go` — handler logic
- `pkg/proxy/handler_helpers.go` — buildModelList, etc.
- `pkg/models/config.go` — ResolveInternalConfig
- `pkg/store/database/store.go` — DB-backed ResolveInternalConfig
- `test/mock_llm.go` — existing mock LLM infrastructure
- `test/mock_llm_race.go` — existing race mock test
- `test/test_mock_race_retry_internal_path.sh` — existing race retry test pattern

### Implementation Notes
- Use EXISTING proxy infrastructure — don't create new test harness
- Use real race coordinator, executor, and handler code
- Only mock should be the upstream LLM server
- Use JSON-backed config (not DB) for simplicity
- If bug is found, add extensive debug logging to trace the exact failure point
- Look at existing tests in `test/` directory for patterns

### Last Run
- **Date**: —
- **Session**: —
- **Result**: —
- **Quick Fixes**: —
- **Report**: —
