# Timeout Configuration Cleanup Plan

## Overview

Simplify the timeout configuration by removing unused `MAX_REQUEST_TIME` and properly implementing `STREAM_DEADLINE` for the streaming deadline feature.

## Current State Analysis

| Variable | Default | Current Purpose | Actually Used? |
|----------|---------|-----------------|----------------|
| `IDLE_TIMEOUT` | 60s | Time between tokens before spawning parallel requests | ✅ Yes |
| `STREAM_DEADLINE` | 110s | Defined in config but NOT in ConfigSnapshot or proxy code | ❌ No |
| `MAX_GENERATION_TIME` | 300s | Used for streaming deadline (wrong purpose) | ✅ Yes |
| `MAX_REQUEST_TIME` | 600s | Stored but never checked/enforced | ⚠️ Partial |

### Important Clarification

`MAX_REQUEST_TIME` IS used in the code (but NOT enforced in race coordinator):
- [`handler.go:33-37`](pkg/proxy/handler.go:33) - Calculates with fallback to `MaxGenerationTime * 2`
- [`handler.go:43`](pkg/proxy/handler.go:43) - Stored in `ConfigSnapshot.MaxRequestTime`
- [`handler_functions.go:103`](pkg/proxy/handler_functions.go:103) - Sets `hardDeadline` field
- [`ultimatemodel/handler.go:119-127`](pkg/ultimatemodel/handler.go:119) - Creates context timeout

**However**, the `hardDeadline` field in `requestContext` is **DEAD CODE** - it's set but never checked anywhere in the race retry flow.

## Target State

| Variable | Default | New Purpose |
|----------|---------|-------------|
| `IDLE_TIMEOUT` | 60s | Time between tokens before spawning parallel requests (unchanged) |
| `STREAM_DEADLINE` | 110s | **Streaming deadline** - pick best buffer and continue streaming |
| `MAX_GENERATION_TIME` | 300s | **Absolute hard timeout** from request start time (entire request lifecycle) |
| `MAX_REQUEST_TIME` | ~~removed~~ | Remove entirely |

## Behavior Changes

### Before
```
time_start ─────────────────────────────────────────────────────────►│       │       │       │       │
      ├─ MAIN REQUEST STARTS
      │       │
      ├───────┼───── IDLE_TIMEOUT (60s) ─────────────────────────────
      │       │           │
      │       │           └─ Spawn parallel requests
      │       │
      ├───────┼───────────┼─── MAX_GENERATION_TIME (300s) ───────────
      │       │           │           │
      │       │           │           └─ Pick best buffer, continue streaming
      │       │           │
      ├───────┼───────────┼───────────┼─── MAX_REQUEST_TIME (600s) ── [NOT ENFORCED!]
      │       │           │           │        │
      │       │           │           │        └─ Should force end (but doesn't)
```

### After
```
time_start ─────────────────────────────────────────────────────────►
      │
      ├─ MAIN REQUEST STARTS
      │       │
      ├───────┼───── IDLE_TIMEOUT (60s) ─────────────────────────────
      │       │           │
      │       │           └─ Spawn parallel requests
      │       │
      ├───────┼───────────┼─── STREAM_DEADLINE (110s) ───────────────
      │       │           │           │
      │       │           │           └─ Pick best buffer, continue streaming
      │       │           │
      ├───────┼───────────┼───────────┼─── MAX_GENERATION_TIME (300s) ─
      │       │           │           │        │
      │       │           │           │        └─ FORCE END (absolute hard timeout)
```

---

## Implementation Todo List

### Phase 1: Config Changes

- [ ] **pkg/config/config.go**
  - Remove `MaxRequestTime` field from `Config` struct
  - Remove `MaxRequestTime` validation code
  - Remove `MAX_REQUEST_TIME` env var parsing
  - Remove `GetMaxRequestTime()` method
  - Update `StreamDeadline` comment to clarify purpose
  - Update `MaxGenerationTime` comment to clarify new purpose

- [ ] **pkg/config/manager.go** (if exists separately)
  - Remove `GetMaxRequestTime()` method from `ManagerInterface`

- [ ] **pkg/store/database/store.go**
  - Remove `GetMaxRequestTime()` method from `ConfigManager`
  - Keep `StreamDeadlineMs` in database schema for backward compatibility

### Phase 2: Proxy Handler Changes

- [ ] **pkg/proxy/handler.go** - ConfigSnapshot Changes
  - **ADD** `StreamDeadline time.Duration` to `ConfigSnapshot` struct (after `IdleTimeout`)
  - **REMOVE** `MaxRequestTime time.Duration` from `ConfigSnapshot` struct
  - **UPDATE** `Clone()` method:
    ```go
    // BEFORE:
    maxRequestTime := cfg.MaxRequestTime.Duration()
    if maxRequestTime == 0 {
        maxRequestTime = cfg.MaxGenerationTime.Duration() * 2
    }
    return ConfigSnapshot{
        // ...
        MaxRequestTime: maxRequestTime,
    }
    
    // AFTER:
    return ConfigSnapshot{
        // ...
        StreamDeadline:     cfg.StreamDeadline.Duration(),
        MaxGenerationTime:  cfg.MaxGenerationTime.Duration(),
    }
    ```

- [ ] **pkg/proxy/handler_helpers.go** - Remove Dead Code
  - **REMOVE** `hardDeadline time.Time` field from `requestContext` (line 34)
  - This field is set but NEVER checked - it's dead code

- [ ] **pkg/proxy/handler_functions.go** - Remove Dead Code
  - **REMOVE** `hardDeadline: startTime.Add(conf.MaxRequestTime)` from initialization (line 103)

- [ ] **pkg/proxy/race_coordinator.go** - Two Timer Implementation
  - **CHANGE** `streamDeadlineTimer` to use `c.cfg.StreamDeadline`:
    ```go
    // BEFORE:
    streamDeadlineTimer := time.NewTimer(time.Duration(c.cfg.MaxGenerationTime))
    
    // AFTER:
    streamDeadlineTimer := time.NewTimer(c.cfg.StreamDeadline)
    ```
  - **ADD** `hardDeadlineTimer` using `c.cfg.MaxGenerationTime`:
    ```go
    hardDeadlineTimer := time.NewTimer(c.cfg.MaxGenerationTime)
    defer hardDeadlineTimer.Stop()
    ```
  - **ADD** case for hard deadline in select:
    ```go
    case <-hardDeadlineTimer.C:
        // Hard deadline reached - force end everything
        c.handleHardDeadline()
        return
    ```
  - **IMPLEMENT** `handleHardDeadline()` method to cancel all requests immediately

### Phase 3: Ultimate Model Handler

- [ ] **pkg/ultimatemodel/handler.go** - Simplified Change
  - **REPLACE** lines 119-127 with single line:
    ```go
    // BEFORE:
    maxRequestTime := cfg.MaxRequestTime.Duration()
    if maxRequestTime == 0 {
        maxRequestTime = cfg.MaxGenerationTime.Duration() * 2
    }
    ctx, cancel := context.WithTimeout(parentCtx, maxRequestTime)
    
    // AFTER:
    ctx, cancel := context.WithTimeout(parentCtx, cfg.MaxGenerationTime.Duration())
    ```

### Phase 4: Test Updates

- [ ] **pkg/config/config_test.go**
  - Remove `MaxRequestTime` from test configs (many occurrences)
  - Ensure `StreamDeadline` is properly tested

- [ ] **pkg/proxy/handler_test.go**
  - Remove `MAX_REQUEST_TIME` env var setup
  - Update tests to use `MAX_GENERATION_TIME` for hard timeout

- [ ] **pkg/proxy/race_retry_test.go**
  - Add `StreamDeadline` to test configs
  - Update `MaxGenerationTime` values for hard timeout tests

- [ ] **test/test_race_retry.sh**
  - Remove `MAX_REQUEST_TIME` export
  - Add `STREAM_DEADLINE` export if needed

### Phase 5: Documentation Updates

- [ ] **README.md**
  - Update timeout configuration table
  - Remove `MAX_REQUEST_TIME` entry
  - Update `MAX_GENERATION_TIME` description
  - Update `STREAM_DEADLINE` description

- [ ] **AGENTS.md**
  - Update environment variables table
  - Remove `MAX_REQUEST_TIME` entry

- [ ] **plans/unified-race-retry-design.md**
  - Update design document to reflect new timeout hierarchy
  - Remove references to `hardDeadline` as separate from `MaxGenerationTime`

- [ ] **docs/ultimate-model-design.md**
  - Update references from `MaxRequestTime` to `MaxGenerationTime`

### Phase 6: Frontend Updates (Optional)

- [ ] **pkg/ui/frontend/src/components/config/ProxySettings.tsx**
  - Already has `StreamDeadline` - verify it works correctly
  - Remove any `MaxRequestTime` / `max_request_time` references

- [ ] **pkg/ui/frontend/src/components/SettingsPage.tsx**
  - Verify `streamDeadline` state is properly saved/loaded

---

## Files to Modify

### Core Changes
| File | Changes |
|------|---------|
| `pkg/config/config.go` | Remove `MaxRequestTime`, update comments |
| `pkg/proxy/handler.go` | Update `ConfigSnapshot`, add `StreamDeadline` |
| `pkg/proxy/handler_helpers.go` | Remove `hardDeadline` field |
| `pkg/proxy/handler_functions.go` | Remove `hardDeadline` init |
| `pkg/proxy/race_coordinator.go` | Use `StreamDeadline`, enforce `MaxGenerationTime` |
| `pkg/ultimatemodel/handler.go` | Use `MaxGenerationTime` for timeout |

### Test Files
| File | Changes |
|------|---------|
| `pkg/config/config_test.go` | Remove `MaxRequestTime` from tests |
| `pkg/proxy/handler_test.go` | Remove `MAX_REQUEST_TIME` env var |
| `pkg/proxy/race_retry_test.go` | Add `StreamDeadline`, update tests |
| `test/test_race_retry.sh` | Remove `MAX_REQUEST_TIME` |

### Documentation
| File | Changes |
|------|---------|
| `README.md` | Update timeout docs |
| `AGENTS.md` | Update env var table |
| `plans/unified-race-retry-design.md` | Update design |
| `docs/ultimate-model-design.md` | Update references |

### Database (No schema change needed)
| File | Changes |
|------|---------|
| `pkg/store/database/store.go` | Remove `GetMaxRequestTime()` method only |

---

## Validation

After implementation, verify:

1. **Streaming deadline works**: Set `STREAM_DEADLINE=30s`, verify best buffer is picked after 30s
2. **Hard deadline enforced**: Set `MAX_GENERATION_TIME=60s`, verify request is forcefully terminated after 60s
3. **Idle timeout works**: Set `IDLE_TIMEOUT=10s`, verify parallel requests spawn after 10s idle
4. **All tests pass**: `go test ./...` and `make test`
5. **No dead code remains**: 
   ```bash
   grep -r "hardDeadline" pkg/proxy/       # Should return no results
   grep -r "MaxRequestTime" pkg/proxy/     # Should return no results
   grep -r "MAX_REQUEST_TIME" . --include="*.go"  # Should return no results
   ```
6. **Ultimate model works**: Test duplicate request handling still respects timeout
7. **No regression**: Race retry behavior is preserved

---

## Migration Notes

For existing deployments:

- `MAX_REQUEST_TIME` env var will be ignored (remove from configs)
- `STREAM_DEADLINE` will now be actively used (set to desired value)
- `MAX_GENERATION_TIME` now serves as absolute hard timeout (may need to increase)
