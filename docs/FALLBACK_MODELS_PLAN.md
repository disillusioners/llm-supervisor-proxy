# Fallback Models Feature Implementation Plan

## Document History
- **v1.0**: Initial plan
- **v1.1**: Updated based on feedback (see `docs/FALLBACK_MODELS_PLAN_FEEDBACK.md`)

## Feedback Incorporation
1. ✅ **Frontend Split**: Phase 1 uses minimal UI (Option A), Phase 2 is separate Preact refactor initiative
2. ✅ **Streaming Constraints**: Fallback only triggers before first byte sent (`headersSent` check)
3. ✅ **Timeout Budget**: Added `getRemainingTimeout()` and threshold check before fallback
4. ✅ **Circular Detection**: Added DFS cycle detection + max depth (3) validation
5. ✅ **Atomic Writes**: Added `models.json.tmp` → `os.Rename()` pattern
6. ✅ **Event Payload**: Added `from_model`, `to_model`, `reason` to `fallback_triggered` event

---

**Feature**: Auto-retry with fallback models when a request fails completely (max retries exceeded or MAX_GENERATION_TIME exceeded).

**Flow**: 
```
Request → Model A → [Failed after max retries] → Fallback Model B → [Failed] → Fallback Model C → [Success/All Failed]
```

---

## 1. Data Model & Storage

### 1.1 New JSON Configuration File

**File**: `config/models.json`

```json
{
  "models": [
    {
      "id": "gpt-4",
      "name": "GPT-4",
      "enabled": true,
      "fallback_chain": ["gpt-3.5-turbo", "claude-instant"]
    },
    {
      "id": "gpt-3.5-turbo", 
      "name": "GPT-3.5 Turbo",
      "enabled": true,
      "fallback_chain": []
    },
    {
      "id": "claude-instant",
      "name": "Claude Instant",
      "enabled": true,
      "fallback_chain": []
    }
  ]
}
```

### 1.2 New Package: `pkg/models/`

**File**: `pkg/models/config.go`

```go
package models

type ModelConfig struct {
    ID            string   `json:"id"`
    Name          string   `json:"name"`
    Enabled       bool     `json:"enabled"`
    FallbackChain []string `json:"fallback_chain"`
}

const MaxFallbackDepth = 3

type ModelsConfig struct {
    mu       sync.RWMutex
    Models   []ModelConfig `json:"models"`
    filePath string
}

func (mc *ModelsConfig) GetFallbackChain(modelID string) []string
func (mc *ModelsConfig) Load(filePath string) error
func (mc *ModelsConfig) Save() error           // Atomic write: tmp -> rename
func (mc *ModelsConfig) AddModel(model ModelConfig) error  // Includes validation
func (mc *ModelsConfig) UpdateModel(modelID string, model ModelConfig) error  // Includes validation
func (mc *ModelsConfig) RemoveModel(modelID string) error
func (mc *ModelsConfig) Validate() error        // DFS cycle detection + max depth check
func (mc *ModelsConfig) GetRemainingTimeout(ctx context.Context, totalTimeout time.Duration) time.Duration
```

### 1.3 Update `pkg/store/memory.go`

Add fields to track fallback usage:

```go
type RequestLog struct {
    // ... existing fields ...
    OriginalModel   string   `json:"original_model,omitempty"`   // First requested model
    FallbackUsed    []string `json:"fallback_used,omitempty"`   // Fallback models attempted
    CurrentFallback string   `json:"current_fallback,omitempty"` // Active fallback (if any)
}
```

---

## 2. Backend Changes

### 2.1 Handler Refactor (`pkg/proxy/handler.go`)

**Current flow (simplified)**:
```
for attempt <= MaxRetries {
    // retry same model
}
```

**New flow (streaming-aware)**:
```
fallbackChain := modelsConfig.GetFallbackChain(originalModel)
allModels := append([]string{originalModel}, fallbackChain...)

for _, modelID := range allModels {
    // Check remaining timeout budget before attempting
    remainingTime := getRemainingTimeout(ctx, conf.MaxGenerationTime)
    if remainingTime < minFallbackThreshold {
        break // Fail immediately, no time for fallback
    }
    
    for attempt <= MaxRetries {
        // Check if we can still fallback (before any bytes sent)
        canFallback := !headersSent

        requestBody["model"] = modelID
        // ... existing retry logic
        
        if success {
            return response
        }

        if !canFallback {
            // Streaming already started, cannot fallback
            // Fail with error
            break
        }
    }
    
    if success { break }
    
    // Transition to next fallback model
    if hasMoreFallbacks && !headersSent {
        reqLog.FallbackUsed = append(reqLog.FallbackUsed, modelID)
        reqLog.CurrentFallback = nextModel
        publishEvent("fallback_triggered", FallbackEvent{
            FromModel: modelID,
            ToModel:   nextModel,
            Reason:    determineFailureReason(),
        })
    }
}
```

**Key changes**:

| Line(s) | Change |
|---------|--------|
| 24-30 | Add `ModelsConfig *models.ModelsConfig` to `Config` struct |
| 59-66 | Update `NewHandler` to accept `ModelsConfig` |
| 129 | Store `originalModel` separately in `reqLog.OriginalModel` |
| 110 | Add `remainingTime` calculation before each fallback attempt |
| 164-451 | Wrap retry loop in model-iteration loop with `headersSent` check |
| New | Add `getRemainingTimeout()` helper |
| New | Add `determineFailureReason()` helper |
| New | Add `handleFallbackTransition()` method |

### 2.2 New API Endpoints (`pkg/ui/server.go`)

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/api/models` | GET | List all models with fallback chains |
| `/api/models` | POST | Add new model |
| `/api/models/{id}` | PUT | Update model (name, enabled, fallback_chain) |
| `/api/models/{id}` | DELETE | Remove model |
| `/api/models/reload` | POST | Reload from JSON file |

### 2.3 New Event Types (`pkg/events/bus.go`)

```go
// Add to event handling:
"fallback_triggered"   // When switching to fallback model
"fallback_exhausted"   // When all fallbacks failed  
"fallback_success"     // When fallback succeeded

// Event payload for fallback_triggered:
type FallbackEvent struct {
    FromModel string `json:"from_model"`
    ToModel   string `json:"to_model"`
    Reason    string `json:"reason"` // "max_retries" | "deadline_exceeded" | "upstream_error"
}
```

---

## 3. Frontend Refactor

### 3.1 Current State

- Single HTML file (`pkg/ui/static/index.html`) with ~500 lines
- Inline CSS, JavaScript all in one file
- No component structure
- Difficult to maintain

### 3.2 Proposed Refactor Options

#### Option A: Keep Single File, Modular JS (Minimal Change)
- Extract JS into separate sections
- Add JS classes for state management
- **Pros**: No build step, minimal change
- **Cons**: Still hard to maintain long-term

#### Option B: Preact + HTM (Recommended)
- **Preact**: 3KB React alternative
- **HTM**: JSX-like syntax without build step
- **Pros**: Components, modern patterns, no build step, CDN-loadable
- **Cons**: Slight learning curve

```html
<script src="https://unpkg.com/preact@10/dist/preact.umd.js"></script>
<script src="https://unpkg.com/htm@3/dist/htm.umd.js"></script>
```

#### Option C: Alpine.js
- **Pros**: Simple, declarative, CDN-loadable, good for progressive enhancement
- **Cons**: Different paradigm from React-like

#### Option D: Petite-Vue
- **Pros**: 6KB, Vue syntax, CDN-loadable
- **Cons**: Limited Vue features

### 3.3 Recommended Structure (Preact)

```
pkg/ui/static/
├── index.html           # Entry point, loads components
├── js/
│   ├── app.js           # Main app component
│   ├── components/
│   │   ├── Header.js
│   │   ├── RequestList.js
│   │   ├── RequestDetail.js
│   │   ├── EventLog.js
│   │   ├── ConfigModal.js
│   │   └── ModelsModal.js    # NEW
│   ├── stores/
│   │   ├── requestStore.js
│   │   └── configStore.js
│   └── utils/
│       └── api.js
└── css/
    └── styles.css
```

### 3.4 New UI: Models Management Screen

**Location**: New modal accessible from header (gear icon → Models tab)

**Layout**:
```
┌─────────────────────────────────────────────────────────┐
│  Models Configuration                              [X]  │
├─────────────────────────────────────────────────────────┤
│                                                         │
│  ┌─────────────────────────────────────────────────┐   │
│  │ Model ID          │ Fallback Chain     │ Actions│   │
│  ├─────────────────────────────────────────────────┤   │
│  │ ● gpt-4           │ gpt-3.5, claude   │ ✏️ 🗑️  │   │
│  │ ● gpt-3.5-turbo   │ (none)            │ ✏️ 🗑️  │   │
│  │ ○ disabled-model  │ ...               │ ✏️ 🗑️  │   │
│  └─────────────────────────────────────────────────┘   │
│                                                         │
│  [+ Add Model]                                          │
│                                                         │
└─────────────────────────────────────────────────────────┘
```

**Edit Model Dialog**:
```
┌───────────────────────────────────────┐
│  Edit Model                     [X]   │
├───────────────────────────────────────┤
│                                       │
│  Model ID:  [gpt-4______________]     │
│                                       │
│  Display Name: [GPT-4____________]    │
│                                       │
│  ☑ Enabled                            │
│                                       │
│  Fallback Chain:                      │
│  ┌─────────────────────────────────┐  │
│  │ 1. [gpt-3.5-turbo▼]    [×]     │  │
│  │ 2. [claude-instant▼]   [×]     │  │
│  │ [+ Add Fallback]              │  │
│  └─────────────────────────────────┘  │
│                                       │
│  [Cancel]              [Save]         │
└───────────────────────────────────────┘
```

---

## 4. Implementation Phases

### Phase 1: Core Backend + Minimal UI (Est. 6-8 hours)

| Task | Files | Priority |
|------|-------|----------|
| Create `pkg/models/config.go` with validation | New | High |
| Create `config/models.json` | New | High |
| Update `store/memory.go` | Modify | High |
| Refactor handler fallback logic (streaming-aware) | `proxy/handler.go` | High |
| Add model API endpoints | `ui/server.go` | High |
| Add new event types | `events/bus.go` | High |
| Add atomic file writes | `pkg/models/config.go` | High |

**Frontend (Minimal - Option A)**: Add Models tab to existing config modal in single HTML file.

### Phase 2: Frontend Refactor (Est. 6-8 hours) - SEPARATE INITIATIVE

| Task | Files | Priority |
|------|-------|----------|
| Set up Preact structure | `ui/static/js/` | High |
| Extract existing components | `ui/static/js/components/` | High |
| Create Models management modal | `ui/static/js/components/ModelsModal.js` | High |
| Update request detail to show fallback info | `RequestDetail.js` | Medium |
| Add fallback events to log renderer | `EventLog.js` | Medium |

### Phase 3: Integration & Testing (Est. 2-4 hours)

| Task | Priority |
|------|----------|
| Integration testing | High |
| Update `test/verify.sh` for fallback scenarios | Medium |
| Add hot-reload for models.json changes | Low |
| Documentation | Low |

---

## 5. File Changes Summary

### Phase 1 (Backend + Minimal UI)

#### New Files
```
config/models.json
pkg/models/config.go           # With validation + atomic writes
```

#### Modified Files
```
cmd/main.go                    # Initialize ModelsConfig
pkg/proxy/handler.go           # Fallback logic with streaming awareness
pkg/store/memory.go            # New fields: OriginalModel, FallbackUsed
pkg/ui/server.go               # New model API endpoints
pkg/ui/static/index.html       # Add Models tab to config modal (minimal)
```

### Phase 2 (Frontend Refactor - Separate Initiative)

#### New Files
```
pkg/ui/static/js/app.js
pkg/ui/static/js/components/*.js
pkg/ui/static/js/stores/*.js
pkg/ui/static/js/utils/api.js
pkg/ui/static/css/styles.css
```

---

## 6. API Contract

### GET `/api/models`
```json
{
  "models": [
    {
      "id": "gpt-4",
      "name": "GPT-4", 
      "enabled": true,
      "fallback_chain": ["gpt-3.5-turbo"]
    }
  ]
}
```

### POST `/api/models`
```json
// Request
{
  "id": "claude-3",
  "name": "Claude 3",
  "enabled": true,
  "fallback_chain": ["claude-instant"]
}

// Response: 201 Created
// Error Response: 400 Bad Request (validation error)
{
  "error": "circular fallback detected" | "fallback chain exceeds max depth of 3" | "unknown model in fallback chain"
}
```

### PUT `/api/models/{id}`
```json
// Request
{
  "name": "GPT-4 Turbo",
  "enabled": true,
  "fallback_chain": ["gpt-3.5-turbo", "gpt-3.5"]
}

// Response: 200 OK
// Error Response: 400 Bad Request (validation error)
```

### DELETE `/api/models/{id}`
```
// Response: 204 No Content
```

### POST `/api/models/validate`
```json
// Request (for preview before saving)
{
  "fallback_chain": ["gpt-3.5-turbo", "claude-instant"]
}

// Response: 200 OK
{
  "valid": true
}

// Or with error:
{
  "valid": false,
  "error": "circular fallback detected"
}
```

---

## 7. Edge Cases & Additional Considerations

### 7.1 Streaming Fallback Constraints
- **Critical**: Fallback only triggers if failure occurs **before first byte streamed** to client
- If HTTP 200 OK headers sent and streaming has started, fallback cannot be performed safely
- Check `headersSent` flag before attempting fallback
- Rationale: Switching models mid-stream would corrupt the SSE format

### 7.2 Timeout Budget Management
- Track remaining context deadline when transitioning between models
- Calculate: `remainingTime = deadline - time.Now()`
- Only attempt fallback if `remainingTime > minFallbackThreshold` (e.g., 10s)
- If insufficient time remaining, fail immediately with appropriate error

### 7.3 Circular Fallback Detection & Max Depth
- **Detection**: Use DFS cycle detection on model graph before saving
- **API Response**: Return `400 Bad Request` if cycle detected
- **Max Depth**: Limit fallback chain to **maximum 3 models** (including primary)
- Add validation in `AddModel()` and `UpdateModel()` functions

### 7.4 Atomic File Writes
- Write to `models.json.tmp` first, then `os.Rename()` to atomically replace
- Prevents corruption if daemon crashes mid-write

### 7.5 Event Consistency
- `fallback_triggered` event must include:
  - `from_model`: Failing model ID
  - `to_model`: Target fallback model ID  
  - `reason`: "max_retries" | "timeout" | "error"
- UI event log should render: "Model A failed, switching to Model B"

### 7.6 Unknown Model in Fallback Chain
- Log warning, skip to next fallback
- Don't fail the entire request

### 7.7 Empty Fallback Chain
- Normal behavior, no fallback triggered

### 7.8 Request Body Model Override
- Fallback should update `requestBody["model"]`
- Client receives response from fallback model (model field in response reflects actual model used)

---

## 8. Testing Checklist

### Backend Unit Tests
- [ ] Circular fallback: A → B → A detected and rejected (400)
- [ ] Max depth exceeded: 4+ models in chain rejected (400)
- [ ] Unknown model in chain: Warning logged, model skipped
- [ ] Atomic file write: No corruption on crash during save

### Integration Tests
- [ ] Basic fallback: Primary fails → Fallback succeeds
- [ ] Multi-level fallback: A → B → C succeeds
- [ ] All fallbacks exhausted: Proper error returned
- [ ] No fallbacks configured: Original retry behavior
- [ ] Disabled model in chain: Skipped gracefully
- [ ] Streaming request: Fallback triggers before headers sent
- [ ] Streaming request: No fallback after first byte (proper error)
- [ ] Timeout budget: Fallback skipped when insufficient time remaining
- [ ] Event payload: fallback_triggered includes from_model, to_model, reason

### UI Tests
- [ ] UI: Add/edit/delete models
- [ ] UI: Validation errors displayed on save attempt
- [ ] UI: Reorder fallback chain via drag-and-drop
- [ ] Events: Fallback triggered/success/exhausted logged
- [ ] Request detail shows fallback history (OriginalModel, FallbackUsed)

---

## 9. Future Enhancements (Out of Scope)

- [ ] Fallback conditions (only on timeout, only on 5xx, etc.)
- [ ] Per-request fallback override via header
- [ ] Fallback statistics/metrics
- [ ] Automatic fallback chain suggestions based on failure history
- [ ] Weighted fallback selection for load balancing
- [ ] User-configurable min fallback threshold (currently hardcoded ~10s)
