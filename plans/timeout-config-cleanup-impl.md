# Timeout Configuration Cleanup - Implementation Plan

## Summary

This plan implements the cleanup of timeout configuration by:
1. Removing unused `MAX_REQUEST_TIME` (dead code - set but never enforced)
2. Properly implementing `STREAM_DEADLINE` for the streaming deadline feature
3. Making `MAX_GENERATION_TIME` the absolute hard timeout

## Current State Verification

### Confirmed Dead Code
- [`handler_helpers.go:34`](pkg/proxy/handler_helpers.go:34): `hardDeadline time.Time` field - **SET but NEVER CHECKED**
- [`handler_functions.go:103`](pkg/proxy/handler_functions.go:103): `hardDeadline: startTime.Add(conf.MaxRequestTime)` - **DEAD CODE**
- [`race_coordinator.go:147`](pkg/proxy/race_coordinator.go:147): Uses `MaxGenerationTime` for streaming deadline - **WRONG VARIABLE**

### Config Mismatch
- [`handler.go:62`](pkg/proxy/handler.go:62): `ConfigSnapshot` has `MaxRequestTime` but NOT `StreamDeadline`
- The race coordinator needs `StreamDeadline` but it's not in the snapshot

## Implementation Phases

### Phase 1: Config Changes (pkg/config/config.go)

#### 1.1 Remove MaxRequestTime from Config struct
```go
// BEFORE (line 73):
MaxRequestTime       Duration            `json:"max_request_time"`       // Absolute hard timeout...

// AFTER: Remove this line entirely
```

#### 1.2 Remove MaxRequestTime validation (lines 221-230)
```go
// BEFORE:
if c.MaxRequestTime != 0 {
    if c.MaxRequestTime < Duration(time.Second) {
        return errors.New("max_request_time must be at least 1s")
    }
    if c.MaxRequestTime < c.MaxGenerationTime {
        return errors.New("max_request_time must be >= max_generation_time")
    }
}

// AFTER: Remove this block entirely
```

#### 1.3 Remove MAX_REQUEST_TIME env var parsing (lines 355-358)
```go
// BEFORE:
if v := os.Getenv("MAX_REQUEST_TIME"); v != "" {
    if d, err := time.ParseDuration(v); err == nil && d > 0 {
        cfg.MaxRequestTime = Duration(d)
    }
}

// AFTER: Remove this block entirely
```

#### 1.4 Remove GetMaxRequestTime method (lines 540-545)
```go
// BEFORE:
func (m *Manager) GetMaxRequestTime() time.Duration {
    m.mu.RLock()
    defer m.mu.RUnlock()
    return m.config.MaxRequestTime.Duration()
}

// AFTER: Remove this method entirely
```

#### 1.5 Remove from ManagerInterface (line 99)
```go
// BEFORE:
GetMaxRequestTime() time.Duration

// AFTER: Remove this line from interface
```

#### 1.6 Remove from Defaults (line 152)
```go
// BEFORE:
MaxRequestTime:       Duration(600 * time.Second), // 10 minutes absolute hard limit

// AFTER: Remove this line entirely
```

#### 1.7 Update comments for remaining fields
```go
// StreamDeadline (line 71):
StreamDeadline Duration `json:"stream_deadline"` // Time limit before picking best buffer and continuing streaming (default: 110s)

// MaxGenerationTime (line 72):
MaxGenerationTime Duration `json:"max_generation_time"` // Absolute hard timeout for entire request lifecycle (default: 300s)
```

---

### Phase 2: Database Store Changes (pkg/store/database/store.go)

#### 2.1 Remove GetMaxRequestTime method (lines 393-398)
```go
// BEFORE:
func (m *ConfigManager) GetMaxRequestTime() time.Duration {
    m.mu.RLock()
    defer m.mu.RUnlock()
    return time.Duration(m.cfg.MaxRequestTime)
}

// AFTER: Remove this method entirely
```

**Note**: Database schema does NOT need changes - `MaxRequestTime` was never stored in the database (only in JSON config).

---

### Phase 3: Proxy Handler Changes

#### 3.1 pkg/proxy/handler.go - Update ConfigSnapshot

**Add StreamDeadline** (after line 60):
```go
type ConfigSnapshot struct {
    UpstreamURL             string
    UpstreamCredentialID    string
    IdleTimeout             time.Duration
    StreamDeadline          time.Duration  // ADD THIS LINE
    MaxGenerationTime       time.Duration
    // MaxRequestTime removed - was line 62
    ...
}
```

**Update Clone() method** (lines 31-55):
```go
// BEFORE:
func (c *Config) Clone() ConfigSnapshot {
    cfg := c.ConfigMgr.Get()
    maxRequestTime := cfg.MaxRequestTime.Duration()
    if maxRequestTime == 0 {
        maxRequestTime = cfg.MaxGenerationTime.Duration() * 2
    }
    return ConfigSnapshot{
        // ...
        MaxRequestTime: maxRequestTime,
    }
}

// AFTER:
func (c *Config) Clone() ConfigSnapshot {
    cfg := c.ConfigMgr.Get()
    return ConfigSnapshot{
        UpstreamURL:             cfg.UpstreamURL,
        UpstreamCredentialID:    cfg.UpstreamCredentialID,
        IdleTimeout:             cfg.IdleTimeout.Duration(),
        StreamDeadline:          cfg.StreamDeadline.Duration(),  // ADD
        MaxGenerationTime:       cfg.MaxGenerationTime.Duration(),
        MaxStreamBufferSize:     cfg.MaxStreamBufferSize,
        ModelsConfig:            c.ModelsConfig,
        LoopDetection:           cfg.LoopDetection,
        ToolRepair:              cfg.ToolRepair,
        SSEHeartbeatEnabled:     cfg.SSEHeartbeatEnabled,
        RaceRetryEnabled:        cfg.RaceRetryEnabled,
        RaceParallelOnIdle:      cfg.RaceParallelOnIdle,
        RaceMaxParallel:         cfg.RaceMaxParallel,
        RaceMaxBufferBytes:      cfg.RaceMaxBufferBytes,
        EventBus:                c.EventBus,
    }
}
```

#### 3.2 pkg/proxy/handler_helpers.go - Remove hardDeadline field

**Remove from requestContext** (line 34):
```go
// BEFORE:
type requestContext struct {
    // ...
    hardDeadline time.Time  // REMOVE THIS LINE
    // ...
}

// AFTER: Field removed entirely
```

#### 3.3 pkg/proxy/handler_functions.go - Remove hardDeadline initialization

**Remove from initRequestContext** (line 103):
```go
// BEFORE:
return &requestContext{
    // ...
    hardDeadline: startTime.Add(conf.MaxRequestTime),  // REMOVE THIS LINE
}

// AFTER:
return &requestContext{
    conf:             conf,
    targetURL:        targetURL,
    reqID:            reqID,
    startTime:        startTime,
    reqLog:           reqLog,
    modelList:        modelList,
    requestBody:      requestBody,
    rawBody:          bodyBytes,
    isStream:         isStream,
    originalHeaders:  r.Header,
    method:           r.Method,
    baseCtx:          r.Context(),
    originalMessages: originalMessages,
    bypassInternal:   bypassInternal,
    // hardDeadline removed
}
```

---

### Phase 4: Race Coordinator Changes (pkg/proxy/race_coordinator.go)

#### 4.1 Change streamDeadlineTimer to use StreamDeadline (line 147)
```go
// BEFORE:
streamDeadlineTimer := time.NewTimer(time.Duration(c.cfg.MaxGenerationTime))

// AFTER:
streamDeadlineTimer := time.NewTimer(c.cfg.StreamDeadline)
```

#### 4.2 Add hardDeadlineTimer using MaxGenerationTime
```go
// Add after streamDeadlineTimer (around line 148):
hardDeadlineTimer := time.NewTimer(c.cfg.MaxGenerationTime)
defer hardDeadlineTimer.Stop()
```

#### 4.3 Add case for hard deadline in select
```go
// Add new case in the select statement (around line 160):
case <-hardDeadlineTimer.C:
    // Hard deadline reached - force end everything
    log.Printf("[RACE] Hard deadline reached after %v, forcing end", time.Since(c.startTime))
    c.handleHardDeadline()
    return
```

#### 4.4 Implement handleHardDeadline method
```go
// Add new method after handleStreamingDeadline:
func (c *raceCoordinator) handleHardDeadline() {
    c.mu.Lock()
    defer c.mu.Unlock()

    log.Printf("[RACE] Hard deadline reached, cancelling all requests")
    
    // Publish race_hard_deadline event
    c.publishEvent("race_hard_deadline", map[string]interface{}{
        "duration_ms": time.Since(c.startTime).Milliseconds(),
    })

    // Cancel ALL requests immediately (including winner if any)
    for _, req := range c.requests {
        if req != nil {
            req.Cancel()
        }
    }

    // Signal done
    c.onceDone.Do(func() { close(c.done) })
    c.onceStream.Do(func() { close(c.streamCh) })
}
```

---

### Phase 5: Ultimate Model Handler (pkg/ultimatemodel/handler.go)

#### 5.1 Simplify timeout logic (lines 119-127)
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

---

### Phase 6: Test Updates

#### 6.1 pkg/config/config_test.go
- Search and remove all `MaxRequestTime` occurrences in test configs
- Ensure `StreamDeadline` is properly tested

#### 6.2 pkg/proxy/handler_test.go
- Remove `MAX_REQUEST_TIME` env var setup
- Update tests to verify `StreamDeadline` is in ConfigSnapshot

#### 6.3 pkg/proxy/race_retry_test.go
- Add `StreamDeadline` to test configs
- Update `MaxGenerationTime` values for hard timeout tests

#### 6.4 test/test_race_retry.sh
- Remove `MAX_REQUEST_TIME` export
- Add `STREAM_DEADLINE` export if needed for testing

---

### Phase 7: Documentation Updates

#### 7.1 README.md (lines 76-78)
```markdown
<!-- BEFORE -->
| `IDLE_TIMEOUT` | `60s` | Max time to wait between tokens before considering the stream hung. |
| `MAX_GENERATION_TIME` | `300s` | Hard limit for the entire request lifecycle. |
| `MAX_REQUEST_TIME` | `600s` | **Absolute hard timeout for entire request** (covers all retries). |

<!-- AFTER -->
| `IDLE_TIMEOUT` | `60s` | Max time to wait between tokens before spawning parallel requests. |
| `STREAM_DEADLINE` | `110s` | Time limit before picking best buffer and continuing streaming. |
| `MAX_GENERATION_TIME` | `300s` | **Absolute hard timeout** for entire request lifecycle. |
```

#### 7.2 AGENTS.md (lines 28-30 in environment variables table)
Same updates as README.md

#### 7.3 docs/ultimate-model-design.md
- Update references from `MaxRequestTime` to `MaxGenerationTime`

---

## Files to Modify Summary

| File | Changes |
|------|---------|
| `pkg/config/config.go` | Remove `MaxRequestTime` field, validation, env parsing, getter, interface method, default |
| `pkg/proxy/handler.go` | Add `StreamDeadline` to `ConfigSnapshot`, remove `MaxRequestTime`, update `Clone()` |
| `pkg/proxy/handler_helpers.go` | Remove `hardDeadline` field from `requestContext` |
| `pkg/proxy/handler_functions.go` | Remove `hardDeadline` initialization |
| `pkg/proxy/race_coordinator.go` | Use `StreamDeadline`, add `hardDeadlineTimer`, implement `handleHardDeadline()` |
| `pkg/ultimatemodel/handler.go` | Use `MaxGenerationTime` directly for timeout |
| `pkg/store/database/store.go` | Remove `GetMaxRequestTime()` method |
| `README.md` | Update env var table |
| `AGENTS.md` | Update env var table |

---

## Validation Checklist

After implementation:

1. **Build succeeds**: `make build`
2. **All tests pass**: `go test ./...`
3. **No dead code remains**:
   ```bash
   grep -r "hardDeadline" pkg/proxy/       # Should return no results
   grep -r "MaxRequestTime" pkg/           # Should return no results
   grep -r "MAX_REQUEST_TIME" . --include="*.go"  # Should return no results
   ```
4. **Streaming deadline works**: Set `STREAM_DEADLINE=30s`, verify best buffer is picked after 30s
5. **Hard deadline enforced**: Set `MAX_GENERATION_TIME=60s`, verify request is forcefully terminated after 60s
6. **Idle timeout works**: Set `IDLE_TIMEOUT=10s`, verify parallel requests spawn after 10s idle

---

## Behavior Diagram

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
