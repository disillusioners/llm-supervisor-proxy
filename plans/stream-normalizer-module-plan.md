# Stream Response Normalizer Module Plan

## Problem Statement

Some upstream LLM providers (like glm-5) return malformed OpenAI-compatible streaming responses that fail client-side validation:

1. **`delta.role` is empty string** instead of `"assistant"` or omitted
2. **`tool_calls[].index` is missing** in streaming chunks

## Solution: Stream Response Normalizer

Create a pluggable, interface-based normalizer module that fixes common upstream response issues.

---

## Architecture Design

### Interface Definition

```go
// NormalizeContext carries state across chunks within a single request
type NormalizeContext struct {
    ToolCallIndex int    // Tracks current tool call index for stateful normalizers
    Provider      string // Provider identifier for per-provider rules
    RequestID     string // For logging and traceability
}

// StreamNormalizer interface - implemented by each normalizer rule
type StreamNormalizer interface {
    // Name returns the normalizer's identifier
    Name() string
    
    // Normalize fixes a single SSE data line
    // Returns (modifiedLine, wasModified) to skip marshal if unchanged
    Normalize(line []byte, ctx *NormalizeContext) ([]byte, bool)
    
    // EnabledByDefault returns true if this normalizer should be on
    EnabledByDefault() bool
    
    // Reset clears any state for a new request stream
    Reset(ctx *NormalizeContext)
}
```

### Normalizer Registry

```go
// Registry manages all stream normalizers
type NormalizerRegistry struct {
    mu          sync.RWMutex
    normalizers map[string]StreamNormalizer
    enabled     map[string]bool
}

// Methods
func (r *NormalizerRegistry) Register(n StreamNormalizer)
func (r *NormalizerRegistry) Enable(name string)
func (r *NormalizerRegistry) Disable(name string)
func (r *NormalizerRegistry) Normalize(line []byte, ctx *NormalizeContext) ([]byte, bool)
func (r *NormalizerRegistry) ResetAll(ctx *NormalizeContext)
func (r *NormalizerRegistry) SetProviderOverrides(provider string, enabled map[string]bool)
```

---

## Normalizers to Implement

### 1. FixEmptyRoleNormalizer

**Problem:** `delta.role: ""` instead of `"assistant"` or omitted

**Fix:** Replace empty string role with "assistant" or remove the field

```go
// Normalize handles: {"delta": {"role": ""}}
// Becomes: {"delta": {"role": "assistant"}}
```

### 2. FixMissingToolCallIndexNormalizer (Stateful)

**Problem:** `tool_calls` without `index` field in streaming chunks

**Fix:** Add index based on tracking across chunks (stateful normalizer)

```go
// Normalize handles: {"tool_calls": [{"id": "call_1", ...}]}
// Becomes: {"tool_calls": [{"index": 0, "id": "call_1", ...}]}

// State tracking:
// - First chunk with tool_calls: index = 0
// - Subsequent chunks: increment index when new tool call ID appears
// - Reset() clears state for new request
```

---

## Provider Detection

Provider detection determines which normalizers to apply based on the upstream provider:

```go
// DetectProvider identifies the upstream provider from model configuration
func DetectProvider(cfg *ConfigSnapshot, modelID string) string {
    if cfg.ModelsConfig != nil {
        model := cfg.ModelsConfig.GetModel(modelID)
        if model != nil && model.Internal {
            return model.Provider // e.g., "anthropic", "openai", "glm-5"
        }
    }
    return "external" // LiteLLM or other external upstream
}

// GetNormalizerConfig returns which normalizers to enable for a provider
func GetNormalizerConfig(provider string) map[string]bool {
    // Check environment variables first
    if os.Getenv("STREAM_NORMALIZER_GLM5_ENABLED") == "true" {
        if isGLM5Provider(provider) {
            return map[string]bool{
                "fix_empty_role":      true,
                "fix_tool_call_index": true,
            }
        }
    }
    
    // Default: enable normalizers that are enabled by default
    return nil // Use defaults
}

func isGLM5Provider(provider string) bool {
    return strings.Contains(strings.ToLower(provider), "glm")
}
```

---

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `STREAM_NORMALIZER_ENABLED` | `true` | Enable/disable entire normalizer |
| `STREAM_NORMALIZER_GLM5_ENABLED` | `true` | Enable glm-5 specific normalizers |

### Per-Provider Config

```go
// In config
ProviderNormalizers map[string]ProviderNormalizerConfig

type ProviderNormalizerConfig struct {
    Enabled      bool
    Normalizers  []string // which normalizers to apply
}
```

---

## Integration Points

### In race_executor.go

The normalizer integrates into the streaming response handling. This is a **new integration point** (the existing deprecated `normalizeStreamChunk()` function in `handler_helpers.go` is defined but never called).

#### handleStreamingResponse() Integration

Location: [`pkg/proxy/race_executor.go:468`](pkg/proxy/race_executor.go:468)

```go
// Current code:
if !req.buffer.Add(line) {
    return fmt.Errorf("buffer limit exceeded")
}

// With normalizer:
normCtx := &normalizers.NormalizeContext{
    Provider:  providerID,
    RequestID: req.id,
}
normalized, modified := normalizerRegistry.Normalize(line, normCtx)
if modified {
    log.Debug().Str("request_id", req.id).Msg("normalized malformed stream chunk")
}
if !req.buffer.Add(normalized) {
    return fmt.Errorf("buffer limit exceeded")
}
```

#### handleInternalStream() Integration

Location: [`pkg/proxy/race_executor.go:177`](pkg/proxy/race_executor.go:177)

```go
// Apply normalizer for internal provider responses
normCtx := &normalizers.NormalizeContext{
    Provider:  providerID,
    RequestID: req.id,
}
normalized, _ := normalizerRegistry.Normalize(line, normCtx)
// Continue with normalized line
```

### New File Structure

```
pkg/proxy/normalizers/
├── interface.go           # StreamNormalizer interface + NormalizeContext
├── registry.go            # NormalizerRegistry with thread-safe operations
├── empty_role.go          # FixEmptyRoleNormalizer
├── tool_call_index.go     # FixMissingToolCallIndexNormalizer (stateful)
├── config.go              # NormalizerConfig, provider detection
└── normalizers_test.go    # Unit tests
```

---

## Migration Notes

The existing [`normalizeStreamChunk()`](pkg/proxy/handler_helpers.go:402) function in `handler_helpers.go` is **deprecated and unused** (grep confirms only the definition exists, no calls). 

This plan introduces a **new integration point** in the streaming response handlers:
- [`handleStreamingResponse()`](pkg/proxy/race_executor.go:468) - for external upstream responses
- [`handleInternalStream()`](pkg/proxy/race_executor.go:177) - for internal provider responses

The deprecated function can be removed in a future cleanup after the normalizer is integrated.

---

## Implementation Order

1. **Phase 1:** Create interface, context, and registry
2. **Phase 2:** Implement FixEmptyRoleNormalizer  
3. **Phase 3:** Implement FixMissingToolCallIndexNormalizer (stateful)
4. **Phase 4:** Integrate into handler pipeline (race_executor.go)
5. **Phase 5:** Add configuration, provider detection, and tests

---

## Acceptance Criteria

### Functional Requirements
- [ ] Normalizers are pluggable via interface
- [ ] Registry supports enable/disable per normalizer
- [ ] Per-provider override configuration works
- [ ] Both glm-5 issues are fixed:
  - [ ] Empty role becomes "assistant"
  - [ ] Missing tool_calls index is added
- [ ] Valid responses pass through unchanged
- [ ] Stateful normalizers track state correctly across chunks
- [ ] Reset() properly clears state for new requests

### Operational Requirements
- [ ] Normalizers log when they fix malformed responses (debug level)
- [ ] Debug logs include request ID for traceability
- [ ] Metrics counter tracks normalization events per provider

### Performance Requirements
- [ ] Performance benchmark: normalization adds <1ms per chunk
- [ ] Memory: no heap allocations when normalizer is disabled
- [ ] Modification flag prevents unnecessary marshaling for unchanged chunks

### Robustness Requirements
- [ ] Error handling: malformed JSON doesn't crash the stream (return original)
- [ ] Concurrency: normalizers are safe for concurrent use across requests
- [ ] Each request has its own NormalizeContext for state isolation

### Testing Requirements
- [ ] Unit tests for each normalizer
- [ ] Unit tests for registry operations
- [ ] Integration test with mock malformed responses
- [ ] Benchmark tests for performance validation
