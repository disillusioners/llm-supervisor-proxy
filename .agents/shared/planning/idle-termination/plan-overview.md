# Plan: Stream Idle Termination

## Objective

Detect when the upstream provider sits idle (no data sent) after the deadline stream mechanism has selected a winner, and terminate the request if idle duration exceeds a configurable timeout (default: 2 minutes). This prevents clients from waiting indefinitely for stalled upstream connections.

## Scope Assessment: **SMALL**

This is a single cohesive feature that touches 4 layers (config → core logic → config merge → UI) but follows well-established patterns already present in the codebase. The existing `IsIdle()` / `TrackActivity()` infrastructure handles idle detection; the streaming loop in `streamResult()` provides a natural injection point; and the config/merge/UI patterns are formulaic additions. Estimated: 2-3 hours for an experienced coder.

## Context

- **Project**: llm-supervisor-proxy (Go + React/TypeScript frontend)
- **Working Directory**: `/Users/nguyenminhkha/All/Code/opensource-projects/llm-supervisor-proxy`
- **Key insight**: `IsIdle()` and `TrackActivity()` already exist on `upstreamRequest`. The streaming loop in `streamResult()` already has a 10ms ticker that checks for new data. This is where idle termination plugs in.

## Architecture Overview

```
handleStreamingDeadline() picks winner
    └─► winner continues streaming to client via streamResult()
            │
            ├── TrackActivity() called on each chunk read (already exists)
            │
            └── NEW: In streamResult() ticker loop:
                    if winner.IsIdle(idleTerminationTimeout):
                        cancel winner
                        send SSE error to client
                        return
```

---

## Tasks

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 1 | **Add config fields** | Add `IdleTerminationEnabled bool` (default: `true`) and `IdleTerminationTimeout Duration` (default: 2m) to Config struct with JSON tags `idle_termination_enabled` / `idle_termination_timeout` | `pkg/config/config.go` |
| 2 | **Add to Defaults** | Set `IdleTerminationEnabled: true` and `IdleTerminationTimeout: Duration(120 * time.Second)` in the Defaults var | `pkg/config/config.go` |
| 3 | **Add env overrides** | Add `IDLE_TERMINATION_ENABLED` (bool) and `IDLE_TERMINATION_TIMEOUT` (duration) env var handling in `applyEnvOverrides()` | `pkg/config/config.go` |
| 4 | **Update mergeConfig** ⚠️ CRITICAL | Add BOTH fields to `mergeConfig()` in store.go. The bool needs special handling (use a presence-detection pattern like `isRaceRetryProvided`). Without this, saving any other setting from the UI will reset the fields to zero-value, breaking the feature | `pkg/store/database/store.go` |
| 5 | **Update validation** | Only validate `IdleTerminationTimeout ≥ 1s` when `IdleTerminationEnabled == true`. When disabled, timeout value is irrelevant | `pkg/config/config.go` |
| 6 | **Add to ConfigSnapshot + Clone()** | Add both fields to `ConfigSnapshot` struct and copy them in `Clone()` so they're available in the handler | `pkg/proxy/handler.go` |
| 7 | **Implement idle termination logic** | In `streamResult()` streaming loop, add idle check in the `ticker.C` case. If `IdleTerminationEnabled && winner.IsIdle(IdleTerminationTimeout)` and `!buffer.IsDone()`, cancel the winner, send SSE error, and return | `pkg/proxy/handler.go` |
| 8 | **Update error mapping** | Add "idle termination" pattern to `getCommonFailureStatusLocked()` so it maps to 504 Gateway Timeout (or handle directly in streamResult) | `pkg/proxy/race_coordinator.go` (optional — may be handled at task 7) |
| 9 | **Add to AppConfig types** | Add `idle_termination_enabled: boolean` and `idle_termination_timeout: string` to the `AppConfig` interface | `pkg/ui/frontend/src/types.ts` |
| 10 | **Add frontend config props** | Add `idleTerminationEnabled` (bool) and `idleTerminationTimeout` (string) props to `ProxySettings` component; add toggle switch + timeout input | `pkg/ui/frontend/src/components/config/ProxySettings.tsx` |
| 11 | **Add frontend state + sync** | Add state in `SettingsPage.tsx` for both fields, sync from fetched config in `useEffect`, and include in `handleApplyProxy` submission | `pkg/ui/frontend/src/components/SettingsPage.tsx` |
| 12 | **Test** | Verify: (a) normal streaming unaffected, (b) idle upstream triggers termination after timeout, (c) disabling via toggle prevents termination, (d) config saves/loads correctly, (e) UI displays and submits correctly | Tests + manual verification |

---

## Implementation Notes

### Task 7 — Core Logic Detail

The idle check goes in `streamResult()` inside the `ticker.C` case (fires every 10ms):

```go
case <-ticker.C:
    // Safety backup if notification missed
    chunks, _ = buffer.GetChunksFrom(readIndex)
    if len(chunks) > 0 {
        // ... write chunks (existing code) ...
    }

    // NEW: Idle termination check
    if rc.conf.IdleTerminationEnabled && rc.conf.IdleTerminationTimeout > 0 && !buffer.IsDone() {
        if winner.IsIdle(rc.conf.IdleTerminationTimeout) {
            log.Printf("[STREAM] Idle termination: upstream idle for %v, terminating stream",
                time.Since(winner.GetLastActivity()))
            winner.Cancel()
            h.sendSSEError(w, models.ErrorTypeServerError,
                "Upstream idle timeout — response terminated")
            return
        }
    }
```

**Key considerations:**
- Guard: `IdleTerminationEnabled && IdleTerminationTimeout > 0` (both must be true)
- Only check when buffer is NOT done (stream still in progress)
- `IsIdle()` already checks that status is `statusStreaming`, so pre-stream phases are safe
- `Cancel()` terminates the upstream connection
- `sendSSEError()` sends the error as an SSE data event (headers already sent for streaming)

### Task 4 — mergeConfig Detail

The bool field needs a presence-detection helper (same pattern as `isRaceRetryProvided`):

```go
// isIdleTerminationProvided checks if idle termination config was explicitly provided
func isIdleTerminationProvided(cfg config.Config) bool {
    return cfg.IdleTerminationEnabled || cfg.IdleTerminationTimeout != 0
}
```

Then in `mergeConfig()`:

```go
if isIdleTerminationProvided(incoming) {
    result.IdleTerminationEnabled = incoming.IdleTerminationEnabled
    if incoming.IdleTerminationTimeout != 0 {
        result.IdleTerminationTimeout = incoming.IdleTerminationTimeout
    }
}
```

### Task 5 — Validation Detail

In `Config.Validate()`, only validate timeout when enabled:

```go
if c.IdleTerminationEnabled {
    if c.IdleTerminationTimeout < Duration(time.Second) {
        return fmt.Errorf("idle_termination_timeout must be at least 1 second when enabled")
    }
}
```

### Task 10 — Frontend Input

Follow the exact pattern of the Stream Deadline input, but with an enable/disable toggle:
- **Toggle switch** (like `race_retry_enabled`): "Enable idle stream termination"
- **Text input** with clock icon (disabled/grayed when toggle is off): placeholder `"2m"`
- Help text: "Max idle time after stream winner is selected. If upstream sends no data for this duration, the stream is terminated."

---

## Files Affected

| File | Change Type | Description |
|------|-------------|-------------|
| `pkg/config/config.go` | Modify | Add fields to Config struct, Defaults, env overrides, validation |
| `pkg/store/database/store.go` | Modify ⚠️ | Add fields + presence helper to mergeConfig() |
| `pkg/proxy/handler.go` | Modify | Add to ConfigSnapshot, Clone(), and streamResult() |
| `pkg/ui/frontend/src/types.ts` | Modify | Add `idle_termination_enabled: boolean` and `idle_termination_timeout: string` to AppConfig |
| `pkg/ui/frontend/src/components/config/ProxySettings.tsx` | Modify | Add props + toggle switch + timeout input |
| `pkg/ui/frontend/src/components/SettingsPage.tsx` | Modify | Add state, sync, and submission for both fields |

---

## Risks & Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| **mergeConfig not updated** → fields zeroed on any config save | Critical | Task 4 is explicitly called out with ⚠️. Presence-detection helper follows proven `isRaceRetryProvided` pattern. Verification step in testing. |
| **IdleTimeout vs IdleTerminationTimeout interaction** — both coexist | Medium | **This is graceful escalation, not a conflict:** At ~60s idle → `IdleTimeout` triggers the coordinator to spawn a parallel request (if `RaceParallelOnIdle=true`). At ~2m idle → `IdleTerminationTimeout` terminates the stream entirely. The termination path calls `winner.Cancel()`, and the `defer winner.cancel()` in handler.go ensures spawned parallel requests are also cleaned up. This is correct layered behavior: try parallel first, then give up. |
| **False positive idle detection** — slow but active upstream (e.g., long reasoning) terminated prematurely | Medium | Default 2m is generous. Activity resets timer via existing `TrackActivity()`. Users can disable via `IdleTerminationEnabled` toggle. |
| **Race between idle check and data arrival** — check fires just before data arrives | Low | 10ms ticker granularity means at most 10ms of unnecessary wait. Data would arrive on next tick. The `IsIdle()` check uses `time.Since(lastActivityTime)` which is inherently racy but acceptable — worst case: one extra tick of streaming before termination. |
| **Buffer not done but winner already completed** — edge case | Low | `winner.Cancel()` is idempotent. The buffer `IsDone()` check prevents false termination after stream completes. |

---

## Success Criteria

- [ ] New `idle_termination_enabled` (bool, default true) and `idle_termination_timeout` (duration, default 2m) config fields exist
- [ ] `mergeConfig()` preserves both fields — saving other settings does NOT reset them
- [ ] Idle upstream (no data for > timeout) triggers termination with SSE error to client when enabled
- [ ] Toggling `idle_termination_enabled = false` disables the feature completely (no idle checks run)
- [ ] Active streaming (data arriving regularly) is completely unaffected
- [ ] Validation rejects `idle_termination_timeout < 1s` only when enabled; when disabled, timeout value is ignored
- [ ] UI shows toggle switch + timeout input in Proxy Settings with proper labels and help text
- [ ] Config round-trip works: set via UI → save → reload → values persist

## Tracking

- Created: 2026-04-01
- Status: draft
