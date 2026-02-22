# Loop Detection - Implementation Complete

**Status**: ✅ Phase 3 Complete  
**Tests**: 30/30 passing  
**Build**: ✅ Passing

---

## Overview

The loop detection system monitors LLM responses for repetitive patterns and can intervene when the model enters a hallucination loop. It uses heuristic-based detection strategies (no additional LLM required) with configurable thresholds and shadow mode for safe rollout.

---

## Detection Strategies

### Phase 1 Strategies (Core)

| Strategy | Detects | File |
|----------|---------|------|
| **Exact Match** | Identical consecutive messages | `strategy_exact.go` |
| **Similarity** | Near-identical messages via SimHash | `strategy_similarity.go` |
| **Action Pattern** | Repeated tool/file operations | `strategy_actions.go` |

### Phase 3 Strategies (Advanced)

| Strategy | Detects | File |
|----------|---------|------|
| **Thinking** | Repetitive reasoning patterns via trigram analysis | `strategy_thinking.go` |
| **Cycle** | Circular action patterns (A→B→C→A→B→C) | `strategy_cycle.go` |
| **Stagnation** | No meaningful progress despite activity | `strategy_stagnation.go` |

---

## Strategy Details

### 1. Exact Match (`strategy_exact.go`)

**Detects**: Identical consecutive messages within sliding window

```go
// Triggered when same message appears N times consecutively (default: 3)
// Compares both ContentHash (SimHash) and raw Content
```

**Configuration**:
- `exact_match_count`: 3 (default)

**Example detection**:
```
Evidence: "Identical message repeated 3 times: 'Let me check that file...'"
Severity: Critical
Confidence: 1.0
```

---

### 2. Similarity (`strategy_similarity.go`)

**Detects**: Near-identical messages with minor variations

```go
// Uses SimHash to compare message fingerprints
// Only applies to messages with >= MinTokensForSimHash tokens
// Lower Hamming distance = higher similarity
```

**Configuration**:
- `similarity_threshold`: 0.85 (85% similar = potential loop)
- `min_tokens_for_simhash`: 15 (avoid false positives on short text)

**Example detection**:
```
Evidence: "Found 3 similar messages (threshold: 85%) in the last 3 messages"
Severity: Warning (2 matches) / Critical (3+ matches)
Confidence: 0.60 - 0.85
```

---

### 3. Action Pattern (`strategy_actions.go`)

**Detects**: Two patterns:
1. **Consecutive repeats**: Same action repeated (e.g., `read:config.go` x3)
2. **Oscillation**: A↔B pattern (e.g., `read` → `write` → `read` → `write`)

**Configuration**:
- `action_repeat_count`: 3 (consecutive identical actions)
- `oscillation_count`: 4 (A↔B oscillations)

**Example detection**:
```
Evidence: "Action 'read:config.go' repeated 3 times consecutively"
Evidence: "Oscillation detected: read:config.go ↔ write:config.go repeated 3 times"
Severity: Critical (repeats) / Warning (oscillation)
Confidence: 0.80 - 0.95
```

---

### 4. Thinking (`strategy_thinking.go`) - Phase 3

**Detects**: Repetitive reasoning/thinking patterns using trigram analysis

```go
// Accumulates thinking content across messages
// Calculates trigram repetition ratio: unique_trigrams / total_trigrams
// Lower ratio = more repetition = potential loop
// Special handling for reasoning models (o1, o3, deepseek-r1)
```

**Configuration**:
- `trigram_threshold`: 0.3 (ratio below this = loop)
- `thinking_min_tokens`: 100 (minimum before analysis)
- `reasoning_model_patterns`: ["o1", "o3", "deepseek-r1"]
- `reasoning_trigram_threshold`: 0.15 (more forgiving for reasoning models)

**Example detection**:
```
Evidence: "Thinking content has high trigram repetition (ratio=0.15, threshold=0.30, tokens=250)"
Severity: Warning / Critical (if very low ratio)
Confidence: 0.70 - 0.90
```

---

### 5. Cycle (`strategy_cycle.go`) - Phase 3

**Detects**: Circular patterns in multi-step action workflows

```go
// Looks for repeating action subsequences of length 2-5
// Catches patterns like A→B→C→A→B→C that ActionPattern misses
// Validates it's a genuine cycle (not all same action)
```

**Configuration**:
- `max_cycle_length`: 5 (maximum cycle length to check)
- `min_occurrences`: 2 (minimum repetitions to trigger)

**Example detection**:
```
Evidence: "Action cycle [read:config.go → grep:pattern → read:handler.go] repeated 2 times (cycle length: 3)"
Severity: Warning (2 occurrences) / Critical (3+ occurrences)
Confidence: 0.75 - 0.90
```

---

### 6. Stagnation (`strategy_stagnation.go`) - Phase 3

**Detects**: Messages being produced but no meaningful progress made

```go
// Compares SimHash fingerprints of content across window
// If average similarity is too high, content is stagnating
// Different from Similarity: looks at cumulative content, not pairwise
```

**Configuration**:
- `window_size`: 5 (messages to consider)
- `change_threshold`: 0.3 (minimum 30% change required)
- `min_messages`: 5 (minimum before check)

**Example detection**:
```
Evidence: "Content stagnation detected: 86.7% average similarity across 5 messages (max acceptable: 70%)"
Severity: Warning / Critical (if very high similarity)
Confidence: 0.60 - 0.85
```

---

## Context Window Sanitization

**File**: `sanitize.go`

**Purpose**: Remove repetitive messages from conversation history to break loop patterns during recovery.

**Why it's critical**: LLMs will repeat mistakes if the context window contains many repetitions. Simply injecting a warning doesn't work.

### How It Works

```go
func SanitizeLoopHistory(messages []map[string]interface{}, result *DetectionResult) []map[string]interface{}
```

1. Calculates how many messages to trim based on `RepeatCount`
2. Keeps messages before the loop started
3. Adds a strategy-specific summary message

### Strategy-Specific Summaries

| Strategy | Summary Template |
|----------|-----------------|
| `exact_match` | "Your previous N responses were identical. Please take a completely different approach." |
| `similarity` | "Your previous N responses were nearly identical (X% similar). Please try a fundamentally different approach." |
| `action_pattern` | "Action 'X' was repeated N times without progress. Please try a different action or target." |
| `cycle` | "Action cycle detected (N repetitions). Please break the cycle by trying something new." |
| `thinking` | "Your reasoning has become repetitive. Please stop re-evaluating the same points and move forward." |
| `stagnation` | "No meaningful progress in the last N messages. Please summarize what you know and take concrete action." |

---

## Configuration

### Backend Config (`pkg/config/config.go`)

```go
type LoopDetectionConfig struct {
    Enabled              bool     `json:"enabled"`
    ShadowMode           bool     `json:"shadow_mode"`              // true = log only
    MessageWindow        int      `json:"message_window"`           // 10
    ActionWindow         int      `json:"action_window"`            // 15
    ExactMatchCount      int      `json:"exact_match_count"`        // 3
    SimilarityThreshold  float64  `json:"similarity_threshold"`     // 0.85
    MinTokensForSimHash  int      `json:"min_tokens_for_simhash"`   // 15
    ActionRepeatCount    int      `json:"action_repeat_count"`      // 3
    OscillationCount     int      `json:"oscillation_count"`        // 4
    MinTokensForAnalysis int      `json:"min_tokens_for_analysis"`  // 20
    
    // Phase 3 additions
    ThinkingMinTokens         int      `json:"thinking_min_tokens"`          // 100
    TrigramThreshold          float64  `json:"trigram_threshold"`            // 0.3
    MaxCycleLength            int      `json:"max_cycle_length"`             // 5
    ReasoningModelPatterns    []string `json:"reasoning_model_patterns"`     // ["o1", "o3", "deepseek-r1"]
    ReasoningTrigramThreshold float64  `json:"reasoning_trigram_threshold"`  // 0.15
}
```

### Default Values

| Setting | Default | Purpose |
|---------|---------|---------|
| `enabled` | `true` | Master toggle |
| `shadow_mode` | `true` | Log only, no interruption |
| `exact_match_count` | `3` | Identical messages to trigger |
| `similarity_threshold` | `0.85` | 85% similarity threshold |
| `trigram_threshold` | `0.30` | Repetition ratio threshold |
| `reasoning_trigram_threshold` | `0.15` | More forgiving for o1/o3 |

---

## Frontend Integration

**File**: `pkg/ui/frontend/src/components/ConfigModal.tsx`

New "Loop Detection" tab with:
- ✅ Enable / Shadow Mode toggles
- ✅ Window size settings
- ✅ Exact match threshold
- ✅ Similarity detection settings
- ✅ Action pattern thresholds
- ✅ Stream processing settings

---

## File Structure

```
pkg/loopdetection/
├── detector.go              # Main orchestrator
├── config.go                # Configuration struct
├── types.go                 # Core types and interfaces
├── buffer.go                # Stream buffering logic
├── sanitize.go              # Context window sanitization (Phase 3)
├── strategy_exact.go        # Exact match detection
├── strategy_similarity.go   # SimHash similarity detection
├── strategy_actions.go      # Action pattern detection
├── strategy_thinking.go     # Thinking loop detection (Phase 3)
├── strategy_cycle.go        # Cycle detection (Phase 3)
├── strategy_stagnation.go   # Stagnation detection (Phase 3)
├── detector_test.go         # Test suite (30 tests)
└── fingerprint/
    ├── simhash.go           # SimHash implementation
    └── ngram.go             # Trigram analysis
```

---

## Test Coverage

| Category | Tests |
|----------|-------|
| SimHash / Fingerprint | 5 |
| Exact Match | 2 |
| Similarity | 1 |
| Action Pattern | 2 |
| Thinking Strategy | 4 |
| Cycle Strategy | 3 |
| Stagnation Strategy | 2 |
| Sanitization | 3 |
| Stream Buffer | 3 |
| Configuration | 4 |
| **Total** | **30** |

---

## Integration Points

### Stream Processing (`handler_functions.go`)

```go
// Per-request detector (persists across retries)
if rc.loopDetector == nil {
    rc.loopDetector = loopdetection.NewDetector(ldCfg)
}

// Stream buffering
streamBuf.AddText(newContent)

// Tool call extraction
if toolActions := extractToolCallActions(data); len(toolActions) > 0 {
    for _, action := range toolActions {
        streamBuf.AddAction(action)
    }
}

// Analysis when threshold met
if streamBuf.ShouldAnalyze(false) {
    if result := detector.Analyze(text, actions); result != nil {
        h.publishLoopEvent(rc.reqID, result, detector.IsShadowMode())
    }
}
```

### Event Publishing

```go
// Events published to event bus
h.publishEvent("loop_detected", map[string]interface{}{
    "id":           reqID,
    "strategy":     result.Strategy,
    "severity":     result.Severity.String(),
    "evidence":     result.Evidence,
    "confidence":   result.Confidence,
    "pattern":      result.Pattern,
    "repeat_count": result.RepeatCount,
})
```

---

## Recovery Flow (Future)

When loop detected and `shadow_mode = false`:

1. **Detection**: Strategy returns `DetectionResult`
2. **Sanitization**: `SanitizeLoopHistory()` trims looping messages
3. **Summary Injection**: Strategy-specific guidance added
4. **Retry**: Request retried with sanitized context
5. **Fallback**: Optional model fallback if retry fails

---

## Performance

- **SimHash**: O(n) where n = tokens
- **Sliding windows**: Ring buffers, O(1) add/remove
- **Cycle detection**: Bounded to recent actions
- **Target overhead**: <5ms per message, <10MB memory per request

---

## Phase Completion Summary

| Phase | Features | Status |
|-------|----------|--------|
| Phase 1 | Core detection (exact, similarity, actions) | ✅ Complete |
| Phase 2 | Integration, config, tool extraction | ✅ Complete |
| Phase 3 | Thinking, cycle, stagnation, sanitization | ✅ Complete |
| Phase 4 | Recovery & UI (active interruption) | 🔲 Future |

---

## Recommendations for Production

1. **Start in Shadow Mode**: Default `shadow_mode: true` to collect data
2. **Monitor Logs**: Review `[LOOP-DETECTION][SHADOW]` logs for tuning
3. **Tune Thresholds**: Adjust based on false positive/negative rates
4. **Enable Gradually**: Disable shadow mode per-model once confident
5. **Implement Recovery**: Wire up sanitization to retry flow for active intervention
