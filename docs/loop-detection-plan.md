# Loop Detection Feature Plan

## Problem Statement

LLMs can enter hallucination loops where they repeat the same actions or messages endlessly:
- "Let me check that file..." → reads file → "Let me check that file..." (repeats)
- Identical or near-identical tool calls in succession
- Repetitive thinking patterns without progress

These loops waste resources, cost money, and degrade user experience. We need heuristic-based detection without using another LLM.

---

## Detection Strategies

### 1. Exact Sequence Matching
**Detect**: Identical consecutive messages or actions

```
Algorithm: Rolling hash of recent N messages
- Keep last K messages in a sliding window
- Compare hash of new message against window
- Match threshold: 2+ identical messages within window
```

### 2. Semantic Similarity (Lightweight)
**Detect**: Near-identical messages with minor variations

```
Algorithm: SimHash / MinHash with Jaccard similarity
- Tokenize message content (split on whitespace/punctuation)
- Generate SimHash fingerprint (64-bit)
- Compare Hamming distance between fingerprints
- Threshold: similarity > 0.85 within last 10 messages

IMPORTANT: SimHash requires minimum token count to avoid false positives.
- Minimum tokens: 15 (fall back to exact matching for shorter text)
- For code-heavy output: use higher threshold (0.90)
```

### 3. Action Pattern Detection
**Detect**: Repeated tool/file operations without progress

```
Algorithm: Action sequence analysis
- Track: action_type + target (e.g., "read:config.go", "grep:pattern")
- Count consecutive identical action+target pairs
- Also detect: action_type oscillation (read→write→read→write same file)
- Threshold: 3+ identical actions OR 4+ oscillations
```

### 4. Thinking Loop Detection
**Detect**: Repetitive reasoning patterns

```
Algorithm: N-gram repetition analysis
- Extract thinking/reasoning content (from <think|> or reasoning stream)
- Generate trigrams (3-word sequences)
- Calculate repetition ratio: unique_trigrams / total_trigrams
- Threshold: ratio < 0.3 (high repetition) over last 500 words
```

### 4a. Reasoning Model Considerations (o1, o3-mini, etc.)
**Important**: Reasoning models generate long, iterative text that naturally loops while evaluating approaches.

```
Special handling for reasoning models:
- Detect model type via regex: ["o1", "o3", "claude-3-opus"]
- Use more forgiving TrigramThreshold: 0.15 (vs 0.30 for normal)
- Only analyze content within designated thinking tags/streams
- Don't mix reasoning content with action schemas
- Higher min token threshold before analysis (e.g., 200+ tokens)
```

### 5. Progress Stagnation
**Detect**: No meaningful state change despite activity

```
Algorithm: State fingerprint comparison
- Track conversation state: files read, context established, conclusions reached
- Generate fingerprint of "what we know" after each message
- Compare fingerprints over sliding window
- Threshold: no state change after 5+ substantial messages
```

### 6. Cycle Detection in Action Graph
**Detect**: Circular patterns in multi-step workflows

```
Algorithm: Directed graph cycle detection
- Nodes: actions/states
- Edges: transitions between actions
- Detect: cycles of length 2-5 within recent actions
- Example cycle: A→B→C→A (same actions repeat)
```

---

## Implementation Architecture

### Core Components

```
pkg/loopdetection/
├── detector.go       # Main detector orchestrating all strategies
├── buffer.go         # Stream buffering logic (token/sentence thresholds)
├── strategies/
│   ├── exact.go      # Exact sequence matching
│   ├── similarity.go # SimHash-based similarity (with min-token guard)
│   ├── actions.go    # Action pattern detection
│   ├── thinking.go   # Thinking/reasoning analysis
│   ├── progress.go   # State stagnation detection
│   └── cycles.go     # Graph-based cycle detection
├── fingerprint/
│   ├── simhash.go    # SimHash implementation
│   └── ngram.go      # N-gram generation
├── recovery/
│   └── sanitize.go   # Context window sanitization for loop recovery
├── config.go         # Detection thresholds and settings
└── types.go          # Shared types and interfaces
```

### Integration Points

```go
// In pkg/proxy/handler.go - during message streaming
// IMPORTANT: Do NOT run detection on every tiny chunk (1-5 chars).
// Stream chunks must be buffered into meaningful units first.

type StreamBuffer struct {
    textBuffer      strings.Builder
    tokenCount      int
    lastAnalyzeTime time.Time
    pendingActions  []Action
}

func (h *Handler) handleStream(ctx context.Context, req *Request) {
    detector := loopdetection.NewDetector(loopdetection.Config{
        WindowSize:          10,
        SimilarityThreshold: 0.85,
        ActionThreshold:     3,
        MinTokensForSimilarity: 15,  // Don't run SimHash on short text
    })
    
    buffer := &StreamBuffer{}
    
    for msg := range messageStream {
        // Accumulate stream into buffer
        buffer.textBuffer.WriteString(msg.Content)
        buffer.tokenCount += estimateTokens(msg.Content)
        
        // Collect complete tool calls immediately
        if msg.ToolCall != nil && msg.ToolCallComplete {
            buffer.pendingActions = append(buffer.pendingActions, Action{
                Type:   msg.ToolCall.Type,
                Target: msg.ToolCall.Target,
            })
            // Tool calls can be analyzed immediately
            if result := detector.AnalyzeActions(buffer.pendingActions); result.LoopDetected {
                return h.handleLoopInterruption(req, result)
            }
        }
        
        // Run text heuristics ONLY when buffer reaches threshold
        // Options: complete sentence, complete thought block, or token limit
        shouldAnalyze := buffer.tokenCount >= 20 || 
                         isCompleteSentence(buffer.textBuffer.String()) ||
                         msg.IsMessageEnd
        
        if shouldAnalyze {
            result := detector.Analyze(buffer.textBuffer.String())
            buffer.textBuffer.Reset()
            buffer.tokenCount = 0
            
            if result.LoopDetected {
                h.eventBus.Publish(events.LoopDetected, result)
                
                switch result.Severity {
                case loopdetection.SeverityWarning:
                    // Log warning, continue monitoring
                case loopdetection.SeverityCritical:
                    return h.handleLoopInterruption(req, result)
                }
            }
        }
    }
}
```

---

## Data Structures

### Message Context (Tracked per request)

```go
type MessageContext struct {
    ID           string
    Timestamp    time.Time
    Role         string // "user", "assistant", "system"
    ContentType  string // "text", "tool_call", "thinking"
    
    // For similarity detection
    ContentHash  uint64  // SimHash fingerprint
    TokenCount   int
    
    // For action tracking
    Actions      []Action // Tool calls, file ops, etc.
    
    // For progress tracking
    StateFingerprint string // Hash of "what we know"
}

type Action struct {
    Type     string // "read", "write", "grep", "execute", etc.
    Target   string // File path, command, search query
    Success  bool
}
```

### Detection Result

```go
type DetectionResult struct {
    LoopDetected  bool
    Severity      Severity // Warning, Critical
    Strategy      string   // Which strategy detected it
    Evidence      string   // Human-readable explanation
    Confidence    float64  // 0.0 - 1.0
    Pattern       []string // The repeated sequence
    Suggestions   []string // Recovery suggestions
}
```

---

## Configuration

```go
type Config struct {
    Enabled              bool    `json:"enabled"`
    
    // Window sizes
    MessageWindow        int     `json:"message_window"`         // Default: 10
    ActionWindow         int     `json:"action_window"`          // Default: 15
    
    // Thresholds
    ExactMatchCount      int     `json:"exact_match_count"`      // Default: 2
    SimilarityThreshold  float64 `json:"similarity_threshold"`   // Default: 0.85 (0.90 for code)
    ActionRepeatCount    int     `json:"action_repeat_count"`    // Default: 3
    OscillationCount     int     `json:"oscillation_count"`      // Default: 4
    
    // Thinking analysis
    ThinkingMinTokens    int     `json:"thinking_min_tokens"`    // Default: 100
    TrigramThreshold     float64 `json:"trigram_threshold"`      // Default: 0.3
    
    // Cycle detection
    MaxCycleLength       int     `json:"max_cycle_length"`       // Default: 5
    
    // Stream processing
    MinTokensForAnalysis int     `json:"min_tokens_for_analysis"` // Default: 20
    MinTokensForSimHash  int     `json:"min_tokens_for_simhash"`  // Default: 15
    
    // Model-specific overrides (for reasoning models like o1, o3-mini)
    ReasoningModelPatterns []string `json:"reasoning_model_patterns"` // ["o1", "o3", "claude-3-opus"]
    ReasoningTrigramThreshold float64 `json:"reasoning_trigram_threshold"` // Default: 0.15 (more forgiving)
    
    // Actions
    InterruptOnCritical  bool    `json:"interrupt_on_critical"`  // Default: false (start conservative!)
    WarningCooldown      int     `json:"warning_cooldown_sec"`   // Default: 30
    ShadowMode           bool    `json:"shadow_mode"`            // Default: true (log only, no interrupt)
}
```

---

## Algorithms in Detail

### SimHash Implementation

```go
// Lightweight locality-sensitive hashing for near-duplicate detection
func ComputeSimHash(text string) uint64 {
    tokens := tokenize(text)
    weights := make([]int64, 64)
    
    for _, token := range tokens {
        hash := fnv1a64(token)
        for i := 0; i < 64; i++ {
            if hash & (1 << i) != 0 {
                weights[i]++
            } else {
                weights[i]--
            }
        }
    }
    
    var result uint64
    for i := 0; i < 64; i++ {
        if weights[i] > 0 {
            result |= (1 << i)
        }
    }
    return result
}

func HammingDistance(a, b uint64) int {
    return bits.OnesCount64(a ^ b)
}

func Similarity(a, b uint64) float64 {
    dist := HammingDistance(a, b)
    return 1.0 - float64(dist)/64.0
}
```

### Action Cycle Detection

```go
// Detect cycles in action sequence using modified Floyd's or DFS
func (d *CycleDetector) DetectCycle(actions []Action) *Cycle {
    // Build transition graph
    graph := buildActionGraph(actions)
    
    // Find cycles up to MaxCycleLength
    for cycleLen := 2; cycleLen <= d.config.MaxCycleLength; cycleLen++ {
        if cycle := findCycleOfLength(graph, cycleLen); cycle != nil {
            return cycle
        }
    }
    return nil
}

// Simpler approach: check if last N actions form a repeating pattern
func detectRepeatingPattern(actions []Action, patternLen int) bool {
    if len(actions) < patternLen * 2 {
        return false
    }
    recent := actions[len(actions)-patternLen*2:]
    first := recent[:patternLen]
    second := recent[patternLen:]
    return actionsEqual(first, second)
}
```

### Trigram Repetition Analysis

```go
func (t *ThinkingAnalyzer) AnalyzeRepetition(text string) float64 {
    words := strings.Fields(text)
    if len(words) < 10 {
        return 1.0 // Not enough to analyze
    }
    
    trigrams := make(map[string]int)
    for i := 0; i <= len(words)-3; i++ {
        trigram := strings.Join(words[i:i+3], " ")
        trigrams[trigram]++
    }
    
    uniqueTrigrams := len(trigrams)
    totalTrigrams := len(words) - 2
    
    // Ratio: higher = more unique = less repetition
    return float64(uniqueTrigrams) / float64(totalTrigrams)
}
```

---

## Recovery Strategies

When a loop is detected, the system can:

### 1. Soft Interruption (Warning Level)
- Log the detection
- Notify via event stream
- Continue monitoring
- Suggest intervention to user via UI

### 2. Hard Interruption (Critical Level)
- Stop the streaming response
- **IMPORTANT: Truncate/summarize looping history** before injecting system message
  - LLMs will repeat mistakes if the context window contains 5+ repetitions
  - Replace looping messages with: `[System: Previous 3 attempts repeated "read config.go". Trying different approach.]`
- Inject a system message to break the loop:
  ```
  "Your previous response was repetitive. Please take a different approach."
  ```
- Force a model fallback if available
- Return partial results to user

### 3. Context Window Sanitization (Critical for Recovery)
```go
func (d *Detector) SanitizeLoopHistory(messages []Message, loopResult DetectionResult) []Message {
    // Find the looping messages
    loopStart := len(messages) - len(loopResult.Pattern) * loopResult.RepeatCount
    
    // Replace them with a summary
    summary := fmt.Sprintf(
        "[System: The previous %d attempts looped repeating: %s. Try a different strategy.]",
        loopResult.RepeatCount,
        strings.Join(loopResult.Pattern, " → "),
    )
    
    // Truncate and insert summary
    sanitized := make([]Message, 0, loopStart + 1)
    sanitized = append(sanitized, messages[:loopStart]...)
    sanitized = append(sanitized, Message{
        Role:    "system",
        Content: summary,
    })
    
    return sanitized
}
```

### 4. User Prompt (Interactive Mode)
- If user is watching in real-time
- Prompt: "Loop detected. Continue anyway or interrupt?"
- Respect user choice for session

---

## Event Integration

```go
// Add to pkg/events/types.go
const (
    // ... existing events
    EventLoopDetected       = "loop_detected"
    EventLoopInterrupted    = "loop_interrupted"
    EventLoopWarning        = "loop_warning"
)

type LoopDetectedEvent struct {
    RequestID   string
    Strategy    string
    Severity    string
    Evidence    string
    Pattern     []string
    Timestamp   time.Time
}
```

---

## UI Dashboard Integration

### Add to existing dashboard:

1. **Real-time loop indicator**
   - Visual warning when loops detected
   - Show pattern that triggered detection

2. **Request detail view**
   - Loop detection section
   - Show detection timeline
   - Display repeated patterns

3. **Configuration panel**
   - Toggle detection on/off
   - Adjust thresholds
   - View/edit detection rules

---

## Testing Strategy

### Unit Tests
- Each strategy in isolation
- Known loop patterns → should detect
- Normal conversations → should not false positive

### Integration Tests
- Full request flow with loop injection
- Verify interruption behavior
- Event emission verification

### Benchmark Tests
- Performance impact on normal requests
- Memory usage with sliding windows
- Latency added per message

---

## Performance Considerations

1. **SimHash is O(n)** where n = tokens in message
2. **Sliding windows use ring buffers** - O(1) add/remove
3. **Cycle detection limited to recent actions** - bounded complexity
4. **Target overhead**: <5ms per message, <10MB memory per request

---

## Implementation Phases

### Phase 1: Core Detection (Shadow Mode)
- [x] Implement exact matching strategy
- [x] Implement SimHash similarity with min-token guard
- [x] Basic action pattern detection
- [x] Stream buffering logic (sentence/token thresholds)
- [x] Detector orchestration
- [x] Unit tests
- [x] **Shadow mode logging** (detect but don't interrupt)

### Phase 2: Integration & Tuning
- [ ] Integrate into handler.go
- [ ] Add event types
- [ ] Configuration support
- [ ] Multi-turn context persistence
- [ ] Integration tests
- [ ] **Collect shadow mode logs to tune thresholds**

### Phase 3: Advanced Detection
- [ ] Thinking/reasoning analysis
- [ ] Reasoning model special handling
- [ ] Cycle detection in graphs
- [ ] Progress stagnation
- [ ] Context window sanitization

### Phase 4: Recovery & UI
- [ ] Enable Hard Interruption (after confidence in thresholds)
- [ ] Fallback integration
- [ ] UI dashboard updates
- [ ] User notification system
- [ ] Production monitoring

---

## Open Questions (Resolved)

### 1. Threshold Tuning ✅
**Answer**: Start in "Shadow Mode" - implement Phase 1 & 2 without Hard Interruption. Just log when a loop *would* have been detected. Use these logs to tune defaults.
- `SimilarityThreshold: 0.85` is good starting point for text
- Use `0.90` for code-heavy output where syntax naturally repeats
- Be conservative: false positives are worse than missing some loops

### 2. Streaming vs Batch ✅
**Answer**: **Batch is required** for text heuristics. Accumulate stream chunks in a buffer and run detector only when:
- Buffer yields a complete sentence/thought
- Specific token limit reached (e.g., 20 tokens)
- Message ends
- Tool call JSON is complete (can analyze immediately)

### 3. Multi-turn Context ✅
**Answer**: **Yes, must persist across multi-turn lifecycle.** Action Pattern Detection and Progress Stagnation are useless within a single streaming response. To catch "Read file → Fail → Read file → Fail" loops, the `MessageContext` window must span multiple turns of a single user request.

### 4. Model-specific Patterns ✅
**Answer**: **Yes, but start simple for V1.** Start with global configurations. In V2, allow overriding `Config` based on regex matching `req.Model`. Reasoning models (o1, o3-mini) need more forgiving `TrigramThreshold` (0.15 vs 0.30) due to natural iterative thinking.

### 5. False Positive Handling ✅
**Answer**: **Be extremely conservative.** A false positive that breaks user workflow is far more frustrating than waiting extra seconds. Always favor "Soft Interruption/Warning" until highly confident. Default `ShadowMode: true` for initial rollout.

---

## Remaining Considerations

1. **Performance impact on high-throughput proxies** - benchmark with realistic load
2. **Interaction with existing retry logic** - ensure loop detection doesn't conflict with intentional retries
3. **UI clarity** - users need to understand why their request was interrupted

---

## References

- [SimHash: Similarity Estimation Techniques from Rounding Algorithms](https://www.cs.princeton.edu/courses/archive/spr04/cos598B/notes/CharikarEst.pdf)
- [Detecting Near-Duplicates for Web Crawling (Google)](https://www.cs.utexas.edu/~suygangz/cs380d/papers/broder97resemblance.pdf)
- [Floyd's Cycle Detection Algorithm](https://en.wikipedia.org/wiki/Cycle_detection)

---

## Key Design Decisions (From Review Feedback)

| Issue | Solution |
|-------|----------|
| Stream chunks too small for analysis | Buffer until 20+ tokens or complete sentence/tool-call |
| SimHash false positives on short text | Minimum 15 tokens before SimHash, fall back to exact match |
| Context window contamination | Truncate/summarize looping history, not append warnings |
| Reasoning models (o1, o3) | More forgiving thresholds, only analyze designated streams |
| False positives worse than false negatives | Start in Shadow Mode, be extremely conservative |
| Multi-turn loops | Persist MessageContext across full request lifecycle |
