# Frontend Race Retry Support Implementation Plan

## Overview

This document outlines the implementation plan to add full frontend support for the Unified Race Retry feature. The race retry system replaces the old sequential retry mechanism with parallel racing requests.

**Related Documents:**
- [Unified Race Retry Design](./unified-race-retry-design.md)

---

## Current State Analysis

### Backend (Already Implemented)
- ✅ Race retry config fields in `pkg/config/config.go`
- ✅ Race coordinator in `pkg/proxy/race_coordinator.go`
- ✅ Stream buffer in `pkg/proxy/stream_buffer.go`
- ✅ Request execution in `pkg/proxy/race_executor.go`
- ❌ Event publishing for race lifecycle events

### Frontend (Needs Implementation)
- ❌ TypeScript types for race retry config
- ❌ Configuration UI in ProxySettings
- ❌ Event types for race events
- ❌ Event log display for race events

---

## Implementation Tasks

### Phase 1: Backend Event Publishing

Add event publishing to the race coordinator so the frontend can track race lifecycle.

#### 1.1 Update `pkg/proxy/race_coordinator.go`

Add event bus reference and publish methods:

```go
type raceCoordinator struct {
    // ... existing fields ...
    eventBus *events.Bus  // Add event bus
    requestID string      // For event correlation
}

// Add these event publishing calls:

// In Start():
c.publishEvent("race_started", map[string]interface{}{
    "id": c.requestID,
    "models": c.models,
})

// In spawn():
c.publishEvent("race_spawn", map[string]interface{}{
    "id": c.requestID,
    "request_index": idx,
    "model": modelID,
    "type": string(mType),
    "trigger": string(trigger),
})

// In manage() when winner found:
c.publishEvent("race_winner_selected", map[string]interface{}{
    "id": c.requestID,
    "winner_index": c.winnerIdx,
    "winner_type": string(c.winner.modelType),
    "winner_model": c.winner.modelID,
    "duration_ms": time.Since(c.startTime).Milliseconds(),
    "buffer_bytes": c.winner.buffer.TotalLen(),
})

// In manage() when all failed:
c.publishEvent("race_all_failed", map[string]interface{}{
    "id": c.requestID,
    "total_attempts": len(c.requests),
    "duration_ms": time.Since(c.startTime).Milliseconds(),
})
```

#### 1.2 Update `pkg/proxy/handler.go`

Pass event bus to race coordinator:

```go
// In HandleChatCompletions, update coordinator creation:
coordinator := newRaceCoordinatorWithEvents(rc.baseCtx, &rc.conf, r, rc.rawBody, rc.modelList, h.bus, rc.reqID)
```

---

### Phase 2: TypeScript Types

#### 2.1 Update `pkg/ui/frontend/src/types.ts`

**Add race retry config fields to `AppConfig`:**

```typescript
export interface AppConfig {
  // ... existing fields ...
  
  // Remove/deprecate these old fields:
  // max_upstream_error_retries: number;
  // max_idle_retries: number;
  // max_generation_retries: number;
  // shadow_retry_enabled: boolean;
  
  // Add new race retry fields:
  race_retry_enabled: boolean;
  race_parallel_on_idle: boolean;
  race_max_parallel: number;
  race_max_buffer_bytes: number;
}
```

**Add race event types to `EventType`:**

```typescript
export type EventType =
  // ... existing types ...
  
  // Remove/deprecate shadow retry events:
  // | 'shadow_retry_started'
  // | 'shadow_retry_won'
  // | 'shadow_retry_failed'
  // | 'shadow_retry_lost'
  
  // Add race retry events:
  | 'race_started'
  | 'race_spawn'
  | 'race_winner_selected'
  | 'race_all_failed';
```

**Add race event data fields to `EventData`:**

```typescript
export interface EventData {
  // ... existing fields ...
  
  // Race retry fields
  models?: string[];           // For race_started
  request_index?: number;      // For race_spawn
  type?: string;               // Request type: main, second, fallback
  trigger?: string;            // Spawn trigger: idle_timeout, main_error
  winner_index?: number;       // For race_winner_selected
  winner_type?: string;        // main, second, fallback
  winner_model?: string;       // Model ID of winner
  duration_ms?: number;        // Race duration in milliseconds
  buffer_bytes?: number;       // Winner's buffer size
  total_attempts?: number;     // For race_all_failed
}
```

---

### Phase 3: Configuration UI

#### 3.1 Update `pkg/ui/frontend/src/components/config/ProxySettings.tsx`

**Replace old retry fields with race retry configuration:**

```tsx
interface ProxySettingsProps {
  // Remove old props:
  // maxUpstreamErrorRetries: number;
  // maxIdleRetries: number;
  // maxGenerationRetries: number;
  // shadowRetryEnabled: boolean;
  
  // Add new props:
  raceRetryEnabled: boolean;
  raceParallelOnIdle: boolean;
  raceMaxParallel: number;
  raceMaxBufferBytes: number;
  
  // ... other existing props ...
}
```

**Replace the UI sections:**

Remove:
- Max Error Retries input
- Max Idle Retries input
- Max Generation Retries input
- Shadow Retry toggle

Add:
```tsx
{/* Race Retry Section */}
<div class="border-t border-gray-700 pt-4 mt-4">
  <h3 class="text-sm font-medium text-gray-200 mb-3">Race Retry (Parallel Requests)</h3>
  <p class="text-xs text-gray-400 mb-3">
    When enabled, multiple upstream requests race in parallel. The first to complete wins.
  </p>

  {/* Enable Race Retry */}
  <div class="mb-3">
    <label class="block text-sm font-medium text-gray-300 mb-1">Enable Race Retry</label>
    <div class="flex items-center gap-3">
      <button
        type="button"
        onClick={() => onRaceRetryEnabledChange(!raceRetryEnabled)}
        class={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors ${
          raceRetryEnabled ? 'bg-blue-600' : 'bg-gray-600'
        }`}
      >
        <span class={`inline-block h-4 w-4 transform rounded-full bg-white transition-transform ${
          raceRetryEnabled ? 'translate-x-6' : 'translate-x-1'
        }`} />
      </button>
      <span class="text-sm text-gray-400">
        {raceRetryEnabled ? 'Enabled' : 'Disabled'}
      </span>
    </div>
  </div>

  {/* Parallel on Idle */}
  <div class="mb-3">
    <label class="block text-sm font-medium text-gray-300 mb-1">Spawn Parallel on Idle</label>
    <div class="flex items-center gap-3">
      <button
        type="button"
        onClick={() => onRaceParallelOnIdleChange(!raceParallelOnIdle)}
        class={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors ${
          raceParallelOnIdle ? 'bg-blue-600' : 'bg-gray-600'
        }`}
      >
        <span class={`inline-block h-4 w-4 transform rounded-full bg-white transition-transform ${
          raceParallelOnIdle ? 'translate-x-6' : 'translate-x-1'
        }`} />
      </button>
      <span class="text-sm text-gray-400">
        {raceParallelOnIdle ? 'Enabled' : 'Disabled'}
      </span>
    </div>
    <p class="text-xs text-gray-500 mt-1">
      When main request hits idle timeout, spawn parallel requests instead of cancelling.
    </p>
  </div>

  {/* Max Parallel Requests */}
  <div class="mb-3">
    <label class="block text-sm font-medium text-gray-300 mb-1">Max Parallel Requests</label>
    <input
      type="number"
      value={raceMaxParallel}
      onInput={(e) => onRaceMaxParallelChange(parseInt((e.target as HTMLInputElement).value) || 3)}
      class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
      placeholder="3"
      min="1"
      max="5"
    />
    <p class="text-xs text-gray-500 mt-1">
      Maximum concurrent requests (main + second + fallback). Default: 3
    </p>
  </div>

  {/* Max Buffer Bytes */}
  <div class="mb-3">
    <label class="block text-sm font-medium text-gray-300 mb-1">Max Buffer Per Request (MB)</label>
    <input
      type="number"
      value={Math.round(raceMaxBufferBytes / (1024 * 1024))}
      onInput={(e) => onRaceMaxBufferBytesChange(parseInt((e.target as HTMLInputElement).value) * 1024 * 1024 || 5242880)}
      class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-shadow"
      placeholder="5"
      min="1"
      max="50"
    />
    <p class="text-xs text-gray-500 mt-1">
      Maximum bytes to buffer per request. Default: 5MB
    </p>
  </div>
</div>
```

#### 3.2 Update `pkg/ui/frontend/src/components/SettingsPage.tsx`

**Update state and handlers:**

```tsx
// Replace old state:
// const [maxUpstreamErrorRetries, setMaxUpstreamErrorRetries] = useState(0);
// const [maxIdleRetries, setMaxIdleRetries] = useState(0);
// const [maxGenerationRetries, setMaxGenerationRetries] = useState(0);
// const [shadowRetryEnabled, setShadowRetryEnabled] = useState(true);

// Add new state:
const [raceRetryEnabled, setRaceRetryEnabled] = useState(false);
const [raceParallelOnIdle, setRaceParallelOnIdle] = useState(true);
const [raceMaxParallel, setRaceMaxParallel] = useState(3);
const [raceMaxBufferBytes, setRaceMaxBufferBytes] = useState(5 * 1024 * 1024);

// Update useEffect to sync config:
useEffect(() => {
  if (config) {
    // ... existing sync ...
    setRaceRetryEnabled(config.race_retry_enabled ?? false);
    setRaceParallelOnIdle(config.race_parallel_on_idle ?? true);
    setRaceMaxParallel(config.race_max_parallel ?? 3);
    setRaceMaxBufferBytes(config.race_max_buffer_bytes ?? 5242880);
  }
}, [config]);

// Update handleApplyProxy:
const handleApplyProxy = async () => {
  try {
    const response = await onUpdateConfig({
      // ... other fields ...
      race_retry_enabled: raceRetryEnabled,
      race_parallel_on_idle: raceParallelOnIdle,
      race_max_parallel: raceMaxParallel,
      race_max_buffer_bytes: raceMaxBufferBytes,
    });
    // ...
  }
};
```

---

### Phase 4: Event Log UI

#### 4.1 Update `pkg/ui/frontend/src/components/EventLog.tsx`

**Add race event handlers to `getEventMessage()`:**

```tsx
case 'race_started': {
  const models = event.data?.models?.join(', ') || '?';
  return `Race started with models: ${models}`;
}
case 'race_spawn': {
  const d = event.data;
  const trigger = d?.trigger ? ` (${d.trigger})` : '';
  return `Spawned ${d?.type || '?'} request #${d?.request_index ?? '?'}: ${d?.model || '?'}${trigger}`;
}
case 'race_winner_selected': {
  const d = event.data;
  const duration = d?.duration_ms ? ` in ${d.duration_ms}ms` : '';
  const bytes = d?.buffer_bytes ? ` (${(d.buffer_bytes / 1024).toFixed(1)}KB)` : '';
  return `Winner: ${d?.winner_type || '?'} request #${d?.winner_index ?? '?'} (${d?.winner_model || '?'})${duration}${bytes}`;
}
case 'race_all_failed': {
  const d = event.data;
  return `All ${d?.total_attempts || '?'} race requests failed after ${d?.duration_ms || '?'}ms`;
}
```

**Add race event colors to `getEventColor()`:**

```tsx
case 'race_started':
  return 'text-cyan-400';
case 'race_spawn':
  return 'text-blue-400';
case 'race_winner_selected':
  return 'text-green-400';
case 'race_all_failed':
  return 'text-red-400';
```

**Add race event labels to `getEventTypeLabel()`:**

```tsx
case 'race_started':
  return 'RACE_STARTED';
case 'race_spawn':
  return 'RACE_SPAWN';
case 'race_winner_selected':
  return 'RACE_WINNER';
case 'race_all_failed':
  return 'RACE_ALL_FAILED';
```

---

## Implementation Checklist

### Phase 1: Backend Event Publishing
- [ ] Add `eventBus` and `requestID` fields to `raceCoordinator` struct
- [ ] Add `publishEvent()` method to `raceCoordinator`
- [ ] Publish `race_started` event in `Start()`
- [ ] Publish `race_spawn` event in `spawn()`
- [ ] Publish `race_winner_selected` event when winner found
- [ ] Publish `race_all_failed` event when all fail
- [ ] Update `newRaceCoordinator()` call in `handler.go` to pass event bus

### Phase 2: TypeScript Types
- [ ] Add race retry config fields to `AppConfig` interface
- [ ] Add race event types to `EventType` type
- [ ] Add race event data fields to `EventData` interface
- [ ] (Optional) Mark old retry fields as deprecated

### Phase 3: Configuration UI
- [ ] Update `ProxySettings.tsx` props interface
- [ ] Replace old retry UI with race retry controls
- [ ] Update `SettingsPage.tsx` state variables
- [ ] Update `SettingsPage.tsx` useEffect sync
- [ ] Update `SettingsPage.tsx` handleApplyProxy

### Phase 4: Event Log UI
- [ ] Add race event message handlers
- [ ] Add race event color mappings
- [ ] Add race event label mappings

### Phase 5: Testing
- [ ] Test config UI displays current race retry settings
- [ ] Test config save/update works correctly
- [ ] Test event log shows race events in real-time
- [ ] Test event log displays all event details correctly

---

## Migration Notes

### Backward Compatibility

The old config fields (`max_upstream_error_retries`, `max_idle_retries`, `max_generation_retries`, `shadow_retry_enabled`) should be kept in the TypeScript types for backward compatibility with older backend versions, but marked as optional:

```typescript
export interface AppConfig {
  // ... new race retry fields ...
  
  // Deprecated - kept for backward compatibility
  max_upstream_error_retries?: number;
  max_idle_retries?: number;
  max_generation_retries?: number;
  shadow_retry_enabled?: boolean;
}
```

### Environment Variables

The frontend should be aware that these environment variables control the race retry behavior:
- `RACE_RETRY_ENABLED` (default: `false`)
- `RACE_PARALLEL_ON_IDLE` (default: `true`)
- `RACE_MAX_PARALLEL` (default: `3`)
- `RACE_MAX_BUFFER_BYTES` (default: `5242880` = 5MB)

---

## File Changes Summary

| File | Changes |
|------|---------|
| `pkg/proxy/race_coordinator.go` | Add event bus, publish race events |
| `pkg/proxy/handler.go` | Pass event bus to coordinator |
| `pkg/ui/frontend/src/types.ts` | Add race config fields, event types |
| `pkg/ui/frontend/src/components/config/ProxySettings.tsx` | Replace retry UI with race config |
| `pkg/ui/frontend/src/components/SettingsPage.tsx` | Update state/handlers |
| `pkg/ui/frontend/src/components/EventLog.tsx` | Add race event handlers |

---

## Estimated Effort

| Phase | Effort |
|-------|--------|
| Phase 1: Backend Events | 1-2 hours |
| Phase 2: TypeScript Types | 30 minutes |
| Phase 3: Configuration UI | 1-2 hours |
| Phase 4: Event Log UI | 30 minutes |
| Phase 5: Testing | 1 hour |
| **Total** | **4-6 hours** |
