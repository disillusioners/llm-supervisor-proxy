# Shadow Retry Feature

## Overview

Shadow Retry is a latency optimization feature that proactively starts a parallel request to a fallback model when the first idle timeout is detected on the main model. If the shadow request completes successfully before the main model exhausts its retry attempts, the shadow result is used instead, reducing total request latency.

## Problem Statement

When a streaming request experiences an idle timeout (no tokens received for an extended period), the proxy typically:
1. Retries the same model
2. Falls back to the next model in the chain after retries are exhausted

This sequential approach can result in significant latency when:
- The main model is experiencing issues
- The idle timeout detection threshold is reached multiple times

## Solution

Shadow Retry introduces a **parallel request strategy**:
- On the **first** idle timeout, immediately start a background request to the fallback model
- Continue retrying the main model (non-blocking)
- If shadow completes successfully first, use its result
- If main succeeds first, cancel the shadow request

## Design Decisions

### Trigger Conditions

| Condition | Rationale |
|-----------|-----------|
| First idle timeout only | Subsequent timeouts indicate persistent issues; shadow already running |
| Buffer must be empty | Cannot switch streams mid-response to client |
| Fallback model available | Shadow requires a fallback model in the chain |
| Shadow not already running | Prevent duplicate shadow requests |

### Model Selection

Shadow uses the **next model in the fallback chain** (`currentModelIndex + 1`), not always the first fallback. This ensures:
- Correct behavior when already on a fallback model
- Respects the configured model priority order

### Routing Strategy

Shadow requests route correctly based on the fallback model's configuration:

| Model Type | Routing | Credentials |
|------------|---------|-------------|
| **External** | HTTP to `UpstreamURL` | Uses `UpstreamCredentialID` |
| **Internal** | Direct provider API call | Uses fallback model's `credential_id` |

### Safety Mechanisms

1. **Non-blocking channel sends**: Prevents panics when main cancels shadow
2. **Context propagation**: Shadow respects client disconnect and timeout
3. **TruncateParams**: Strips unsupported parameters for fallback model
4. **Buffer isolation**: Shadow writes to its own buffer, swaps only on success

## Configuration

### Environment Variable

```bash
SHADOW_RETRY_ENABLED=true  # Default: true
```

### Config File

```json
{
  "shadow_retry_enabled": true
}
```

### Default Behavior

- **Enabled by default**: Feature is active without configuration
- **No additional config required**: Uses existing model fallback chain

## Implementation

### Key Components

#### 1. Shadow State (`pkg/proxy/handler_helpers.go`)

```go
type shadowRequestState struct {
    done       chan shadowResult  // Result channel (buffered, size 1)
    cancelFunc context.CancelFunc // Cancellation function
    started    bool               // Whether shadow has started
    model      string             // Shadow model ID
    startTime  time.Time          // When shadow started
}

type shadowResult struct {
    buffer    *bytes.Buffer  // SSE stream buffer
    completed bool           // Whether stream completed with [DONE]
    err       error          // Error if shadow failed
}
```

#### 2. Trigger Logic (`pkg/proxy/handler_functions.go`)

```go
func shouldStartShadow(rc *requestContext, counters *retryCounters) bool {
    // Must be enabled in config
    if !rc.conf.ShadowRetryEnabled {
        return false
    }
    // Only on first idle timeout
    if counters.idleRetries != 0 {
        return false
    }
    // Only if no data has been sent to client yet
    if rc.streamBuffer.Len() > 0 {
        return false
    }
    // Must have a fallback model available
    shadowModelIndex := rc.currentModelIndex + 1
    if shadowModelIndex >= len(rc.modelList) {
        return false
    }
    // Shadow must not already be running
    if rc.shadow != nil {
        return false
    }
    return true
}
```

#### 3. Request Execution

**External Upstream** (`executeExternalShadowRequest`):
- Builds HTTP request with shadow model
- Applies `TruncateParams` to strip unsupported parameters
- Streams response to buffer
- Uses external upstream credentials

**Internal Upstream** (`executeInternalShadowRequest`):
- Resolves internal provider config for shadow model
- Creates direct provider client
- Converts request to provider format
- Streams events to SSE buffer format
- Uses shadow model's credentials

#### 4. Result Coordination

In the main stream loop:

```go
if rc.shadow != nil {
    select {
    case result := <-rc.shadow.done:
        if result.err == nil && result.completed && result.buffer != nil {
            // Shadow won - swap buffer, flush, return success
            rc.streamBuffer = *result.buffer
            // ... flush to client ...
            return attemptSuccess
        } else if result.err != nil {
            // Shadow failed - mark completed, continue main
            rc.shadow.completed = true
        }
    default:
        // No shadow result yet, continue main
    }
}
```

When main succeeds first:

```go
if rc.shadow != nil && rc.shadow.cancelFunc != nil {
    rc.shadow.cancelFunc()  // Cancel running shadow
}
```

### Request Flow

```
┌─────────────────────────────────────────────────────────────────┐
│                     SHADOW RETRY FLOW                           │
├─────────────────────────────────────────────────────────────────┤
│  1. Main request starts streaming                               │
│  2. First idle timeout detected (idleRetries == 0)              │
│     ├─→ Check: streamBuffer.Len() == 0?                         │
│     │   ├─ YES → Check: fallback available?                     │
│     │   │   ├─ YES → Start shadow request (background)          │
│     │   │   └─ NO  → Continue normal retry                      │
│     │   └─ NO  → Continue normal retry                          │
│     └─→ Increment idleRetries, continue main retry              │
│                                                                 │
│  3. Race condition:                                             │
│     ├─→ Main succeeds first                                     │
│     │   └─→ Cancel shadow, return main result                   │
│     │                                                           │
│     └─→ Shadow succeeds first (and buffer empty)                │
│         └─→ Cancel main, swap buffer, return shadow result      │
└─────────────────────────────────────────────────────────────────┘
```

## Observability

### Events Published

| Event | When | Data |
|-------|------|------|
| `shadow_retry_started` | Shadow request initiated | `{id, model, trigger, internal}` |
| `shadow_retry_won` | Shadow completed first | `{id, model, duration, mainModel}` |
| `shadow_retry_lost` | Main completed first | `{id, model, duration, mainModel, reason}` |
| `shadow_retry_failed` | Shadow encountered error | `{id, model, error}` |

### Log Messages

```
[SHADOW] Starting shadow request to model <model> for request <id> (internal=<true/false>)
[SHADOW] Shadow request completed successfully, using shadow buffer (model: <model>)
[SHADOW] Main stream succeeded, cancelling shadow request
[SHADOW] Channel full or closed, result not sent
```

### Metrics (Future)

Recommended metrics to track:
- `shadow_retry_requests_total{trigger="idle",result="won|lost|failed"}`
- `shadow_retry_latency_seconds{trigger="idle",result="won"}`
- `shadow_retry_cost_dollars{model}`

## Edge Cases

### 1. Mid-Stream Switching

**Scenario**: Main has already sent data to client when idle timeout occurs.

**Behavior**: Shadow is **not** triggered (condition: `streamBuffer.Len() > 0`).

**Rationale**: Cannot switch response streams mid-flight to client.

### 2. No Fallback Available

**Scenario**: Main model is the last in the fallback chain.

**Behavior**: Shadow is **not** triggered (condition: `modelIndex + 1 >= len(modelList)`).

**Rationale**: No fallback model to shadow to.

### 3. Client Disconnect

**Scenario**: Client disconnects while shadow is running.

**Behavior**: Shadow's context is canceled via `rc.baseCtx`, goroutine exits cleanly.

### 4. Main Succeeds Immediately

**Scenario**: Main request succeeds right after shadow starts.

**Behavior**: Shadow is canceled via `cancelFunc()`, goroutine exits cleanly.

### 5. Both Fail

**Scenario**: Both main and shadow requests fail.

**Behavior**: Normal retry/failure flow continues. Shadow failure is logged, main continues.

### 6. Internal Model Shadow

**Scenario**: Fallback model is configured as internal (direct provider call).

**Behavior**: Shadow uses `executeInternalShadowRequest` with:
- Shadow model's credentials (not main request's)
- Shadow model's base URL
- Direct provider API call (no HTTP to upstream)

## Cost Implications

⚠️ **Warning**: Shadow retry **doubles API costs** for requests that experience idle timeouts.

**Example**:
- Main model: 1 request (idle timeout)
- Shadow model: 1 request (parallel)
- Total: 2 requests charged

**Mitigation strategies** (not implemented):
- Rate limit shadow requests per model
- Maximum concurrent shadow requests
- Cost budget tracking
- Opt-in via request header (`X-Shadow-Retry: true`)

## Performance Characteristics

### Latency Improvement

| Scenario | Without Shadow | With Shadow |
|----------|---------------|-------------|
| Main idle x2, fallback succeeds | `idle_timeout * 2 + fallback_time` | `max(idle_timeout, fallback_time)` |
| Main succeeds after 1 idle | `idle_timeout + main_time` | `idle_timeout + main_time` (no change) |

**Best case**: Shadow completes faster than main exhausts retries.

### Resource Usage

- **Memory**: Additional buffer for shadow stream (~same size as main buffer)
- **CPU**: Minimal (goroutine + channel coordination)
- **Network**: 2x API requests during shadow window

## Testing

### Unit Tests

Located in `pkg/proxy/handler_functions_test.go`:
- `TestShouldStartShadow` - Trigger conditions
- `TestShadowModelIndex` - Model selection logic
- `TestShadowInternalRouting` - Internal vs external routing

### Integration Tests

Manual testing recommended:
1. Configure model with fallback
2. Simulate idle timeout on main model
3. Verify shadow is triggered (check logs)
4. Verify shadow result is used when it completes first

### Test Commands

```bash
# Build
make build

# Run all tests
make test

# Run proxy-specific tests
go test ./pkg/proxy/... -v
```

## Future Enhancements

### Planned

1. **Cost controls**: Rate limiting, budget tracking
2. **Configurable triggers**: Nth idle timeout (not just first)
3. **Error triggers**: Shadow on 5xx errors, not just idle
4. **Client opt-in**: Header-based activation (`X-Shadow-Retry`)

### Considered but Deferred

1. **Preemptive shadow**: Start shadow before any idle (high cost)
2. **Multiple shadows**: Shadow to multiple fallbacks simultaneously
3. **Shadow priorities**: Weighted selection among fallbacks

## Troubleshooting

### Shadow Not Triggering

**Check**:
1. `SHADOW_RETRY_ENABLED=true` in environment
2. Fallback model configured in chain
3. Idle timeout actually occurring (check logs for "Stream idle timeout detected")
4. Buffer is empty when idle occurs (check logs for buffer size)

### Shadow Always Failing

**Check**:
1. Fallback model is enabled and configured correctly
2. Credentials for fallback model are valid
3. `TruncateParams` not stripping required parameters
4. Internal model routing is correct (check `internal` flag in model config)

### High Costs

**Mitigate**:
1. Disable feature: `SHADOW_RETRY_ENABLED=false`
2. Review idle timeout threshold: `IDLE_TIMEOUT=30s` (reduce to trigger later)
3. Review fallback model costs

## Related Documentation

- [Configuration Guide](./configuration.md)
- [Model Fallback Chains](./model-fallback.md)
- [Internal Upstreams](./internal-upstreams.md)
- [Events Reference](./events.md)

## Changelog

### v1.0.0 (2026-03-14)

**Added**:
- Initial implementation of shadow retry feature
- Support for both internal and external upstream routing
- Non-blocking channel communication
- Automatic TruncateParams application
- Event publishing for observability

**Configuration**:
- `ShadowRetryEnabled` config option (default: `true`)
- `SHADOW_RETRY_ENABLED` environment variable

**Events**:
- `shadow_retry_started`
- `shadow_retry_won`
- `shadow_retry_lost`
- `shadow_retry_failed`
