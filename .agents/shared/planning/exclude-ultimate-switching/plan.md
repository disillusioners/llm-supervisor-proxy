# Plan: Exclude Specific Models from Ultimate Model Switching

## Objective
Add a per-model boolean setting (`exclude_from_ultimate_switching`) to `ModelConfig` that prevents the ultimate model switch from triggering for requests targeting that model, even when the request is detected as a duplicate. The hash is still stored so other non-excluded models can still trigger the switch.

## Scope Assessment
**SMALL** — Single feature touching 3-4 files, all changes are localized and follow existing patterns (similar to `Internal`, `PeakHourEnabled` booleans on `ModelConfig`).

## Design Decision

**Approach: Check exclusion at the proxy handler call site** (not inside `ShouldTrigger()`)

The `ShouldTrigger()` function only receives `messages` — it has no model context. Rather than changing its signature (which would affect tests and the `ultimatemodel` package's API), the exclusion check goes in `pkg/proxy/handler.go` at the call site (line ~518), where `rc.resolvedModel` (`*models.ModelConfig`) is already available.

Requirement 3 states: "The hash should still be stored." This means `ShouldTrigger()` must still be called for excluded models (it stores the hash), but we skip acting on the trigger result. The exclusion gate sits between `result.Triggered` and the actual switching logic.

```
Current flow:
  ShouldTrigger() → if triggered → switch to ultimate model

New flow:
  ShouldTrigger() → if triggered → check exclusion → skip or switch
```

This way:
- Hash is always stored (requirement 3 ✅)
- Excluded models never trigger the switch (requirement 2 ✅)
- `ShouldTrigger()` API unchanged (minimal blast radius ✅)

## Tasks

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 1 | Add field to ModelConfig | Add `ExcludeFromUltimateSwitching bool` with JSON tag `exclude_from_ultimate_switching,omitempty` | `pkg/models/config.go:108` |
| 2 | Gate ultimate model switch in proxy handler | After `ShouldTrigger()` returns triggered=true, check exclusion. If excluded AND not forced, log, publish event, fall through to normal flow. ForceTrigger always bypasses exclusion. | `pkg/proxy/handler.go:536` |
| 3 | Update design doc | Add "Per-Model Exclusion" section. Document retry counter interaction (excluded models still increment counter). Update flow diagram. | `docs/ultimate-model-design.md` |
| 4 | Add tests | Unit test for serialization + 4 integration scenarios (see Test Plan below) | `pkg/models/config_test.go`, `pkg/proxy/handler_test.go`, `pkg/proxy/handler_integration_test.go` |
| 5 | Update integration test script | Add test case with excluded model | `test/test_mock_ultimate_model.sh` |

## Key Files
- `pkg/models/config.go` — Add the new boolean field to `ModelConfig` struct (after line 107, near other booleans)
- `pkg/proxy/handler.go` — Add exclusion check at line ~536, inside the `if result.Triggered` block
- `docs/ultimate-model-design.md` — Document the new per-model setting
- `pkg/proxy/handler_test.go` / `handler_integration_test.go` — Test coverage

## Constraints
- Follow existing Go naming conventions and JSON tag patterns (`snake_case`, `omitempty` for booleans)
- The field must default to `false` (zero value) — zero config change required for existing setups
- `ForceTrigger` (via `X-Force-Ultimate-Model` header) **must** bypass the exclusion — force means force regardless of per-model config
- **Excluded models also skip RetryExhausted error handling** — they always continue normal flow. An excluded model never receives a retry-exhausted error, even if the counter is high.

## Implementation Details

### Task 1: ModelConfig field

```go
// In ModelConfig struct, after SecondaryUpstreamModel:
ExcludeFromUltimateSwitching bool `json:"exclude_from_ultimate_switching,omitempty"`
```

Pattern matches `Internal`, `PeakHourEnabled` — bool with `omitempty`.

### Task 2: Proxy handler gate

At `pkg/proxy/handler.go`, inside the `if result.Triggered {` block (line ~536). The exclusion check **must** be conditional on `!forceUltimate` — ForceTrigger always wins:

```go
if result.Triggered {
    // Check if this model is excluded from ultimate model switching.
    // ForceTrigger (X-Force-Ultimate-Model header) always bypasses exclusion.
    if !forceUltimate && rc.resolvedModel != nil && rc.resolvedModel.ExcludeFromUltimateSwitching {
        log.Printf("[UltimateModel] Model '%s' excluded from ultimate switching, hash=%s, continuing normal flow",
            rc.reqLog.Model, result.Hash[:8])

        // Publish event for observability (consistent with other trigger paths)
        h.publishEvent("ultimate_model_excluded", map[string]interface{}{
            "id":    rc.reqID,
            "hash":  result.Hash[:8],
            "model": rc.reqLog.Model,
        })

        // Fall through to normal flow (hash was already stored by ShouldTrigger)
    } else {
        // ... existing ultimate model switch logic (unchanged)
        // This branch handles:
        //   1. Non-excluded models → normal trigger
        //   2. ForceTrigger → forced trigger (forceUltimate == true)
        //   3. External/unknown models (resolvedModel == nil) → normal trigger
    }
}
```

**Critical:** The `!forceUltimate` guard is first in the condition. When `forceUltimate` is true, the entire `if` evaluates to false and we enter the `else` branch — ForceTrigger always proceeds regardless of exclusion.

**Note:** Excluded models also skip the `RetryExhausted` check (it's inside the `else` branch). Excluded models always fall through to normal flow, period.

### Task 3: Design doc update

Add a new section "Per-Model Exclusion" to `docs/ultimate-model-design.md`. Content must cover:

1. **Feature description**: The `exclude_from_ultimate_switching` flag on model config
2. **Flow diagram update**: Show exclusion check between "triggered" and "switch to ultimate"
3. **Interaction with retry counter**: When an excluded model sends a duplicate request, `ShouldTrigger()` still increments the retry counter. This means if the same message content is sent first to an excluded model (consuming retry budget) and then to a non-excluded model, the non-excluded model may see a higher retry count or even `RetryExhausted`. This is accepted behavior — the retry counter is hash-based (message content), not model-based.
4. **ForceTrigger behavior**: `X-Force-Ultimate-Model` header bypasses all exclusion
5. **Example config**:
   ```json
   {
     "id": "gpt-4o",
     "name": "gpt-4o",
     "enabled": true,
     "exclude_from_ultimate_switching": true
   }
   ```

### Task 4: Test plan

#### Unit tests — ModelConfig serialization
- **JSON round-trip**: Marshal/unmarshal `ModelConfig` with `ExcludeFromUltimateSwitching: true`, verify field preserved
- **Default value**: Marshal model without field set, verify it defaults to `false`

#### Integration tests — Proxy handler gate
Four scenarios covering all critical paths:

| Scenario | Setup | Expected |
|----------|-------|----------|
| **Excluded model + duplicate** | Model with `ExcludeFromUltimateSwitching: true`, send same messages twice | Normal flow on both requests. Hash stored. No ultimate model switch. |
| **ForceTrigger + excluded model** | Model with `ExcludeFromUltimateSwitching: true`, send request with `X-Force-Ultimate-Model` header | Ultimate model switch triggered. Exclusion bypassed. |
| **RetryExhausted + excluded model** | Model with exclusion, configure `max_retries=2`, send same messages 4+ times | All requests go through normal flow. No retry-exhausted error sent. Hash counter increments but is irrelevant for excluded model. |
| **Cross-model detection** | Send messages to excluded model (stores hash), then send same messages to non-excluded model | Non-excluded model sees duplicate hash → ultimate model triggered. Excluded model's earlier hash storage enabled this detection. |

## Success Criteria
- [ ] `ModelConfig` has `exclude_from_ultimate_switching` field that serializes/deserializes correctly
- [ ] Requests to excluded models skip ultimate model switching but hash is stored
- [ ] Excluded models skip RetryExhausted error handling — always continue normal flow
- [ ] `X-Force-Ultimate-Model` header bypasses exclusion (force = force)
- [ ] Same messages sent to a non-excluded model after an excluded model still trigger ultimate model switching
- [ ] `ultimate_model_excluded` event published when exclusion activates
- [ ] Design doc updated with exclusion section including retry counter interaction
- [ ] All 4 test scenarios pass
- [ ] Existing tests unchanged (field defaults to false)

## Risks & Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Hash not stored for external models (resolvedModel == nil) | low | nil check in condition — nil means external/unknown model, not excluded. These proceed normally. |
| Retry counter consumed by excluded model | **medium** — excluded model's duplicate calls increment the retry counter. If the same messages are later sent to a non-excluded model, it may see elevated retry count or even `RetryExhausted` | Documented as accepted behavior. Retry counter is hash-based (message content), not per-model. This matches the design intent: the counter tracks how many times *these messages* have been seen globally. If this becomes problematic, a future enhancement could track counters per (hash, model) pair. |
| ForceTrigger bypass not working | low | `!forceUltimate` is the **first** condition checked — short-circuits before exclusion check. When `forceUltimate == true`, the entire `if` is false and `else` branch executes. |
| Breaking existing tests | low | New field defaults to false, no behavior change unless explicitly set |
| Excluded model receives RetryExhausted error | low | Both the trigger and RetryExhausted branches are inside the `else` block — excluded models skip both. Explicit constraint documented above. |

## Tracking
- Created: 2026-05-29
- Status: draft
- Review: addressed 1 critical + 3 warnings + 1 suggestion
