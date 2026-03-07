# Plan: Fixer Model for Tool Repair (Simplified)

## Overview

Add a "fixer model" as a last resort when all repair strategies fail. Send malformed JSON to a configurable LLM to fix it.

**Principle:** Keep it simple. ~70 lines, not 200.

---

## Flow

```
LLM Response → Tool Calls → Repair Strategies (extract_json → library_repair → remove_reasoning)
                                      ↓
                              Fail ✗ → Fixer Model (if configured)
                                      ↓
                              Success ✓ or Final Fail ✗
```

---

## Implementation

### 1. Config (Minimal - 2 fields)

**File:** `pkg/toolrepair/repair_config.go`

```go
// Add to Config struct:
FixerModel   string `json:"fixer_model" yaml:"fixer_model"`     // empty = disabled
FixerTimeout int    `json:"fixer_timeout" yaml:"fixer_timeout"` // seconds
```

**Defaults:**
```go
FixerModel:   "",   // empty = disabled
FixerTimeout: 10,   // 10 seconds
```

**Reuse existing:** `MaxArgumentsSize` for size limit (no new field needed)

---

### 2. Hardcoded Prompt (no template system)

```go
const fixerSystemPrompt = "You are a JSON repair tool. Fix malformed JSON and return ONLY the corrected JSON. No explanations, no markdown, just valid JSON."

func buildFixerUserPrompt(malformedJSON string) string {
    return "Fix this JSON. Return ONLY valid JSON:\n\n" + malformedJSON
}
```

---

### 3. Fixer Implementation (~40 lines)

**File:** `pkg/toolrepair/fixer.go` (new)

```go
package toolrepair

import (
    "context"
    "fmt"
    "strings"
    "time"
)

const fixerSystemPrompt = "You are a JSON repair tool. Fix malformed JSON and return ONLY the corrected JSON. No explanations, no markdown, just valid JSON."

type Fixer struct {
    provider ProviderInterface
    config   *Config
}

type ProviderInterface interface {
    ChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error)
}

func NewFixer(provider ProviderInterface, config *Config) *Fixer {
    return &Fixer{provider: provider, config: config}
}

func (f *Fixer) Fix(ctx context.Context, malformedJSON string) (string, error) {
    // Size check (reuse existing config)
    if f.config.MaxArgumentsSize > 0 && len(malformedJSON) > f.config.MaxArgumentsSize {
        return "", fmt.Errorf("JSON too large: %d bytes", len(malformedJSON))
    }

    // Timeout context
    ctx, cancel := context.WithTimeout(ctx, time.Duration(f.config.FixerTimeout)*time.Second)
    defer cancel()

    // Build request
    req := &ChatCompletionRequest{
        Model: f.config.FixerModel,
        Messages: []ChatCompletionMessage{
            {Role: "system", Content: fixerSystemPrompt},
            {Role: "user", Content: buildFixerUserPrompt(malformedJSON)},
        },
        MaxTokens:   2048,
        Temperature: floatPtr(0),
    }

    // Call fixer
    resp, err := f.provider.ChatCompletion(ctx, req)
    if err != nil {
        return "", fmt.Errorf("fixer request failed: %w", err)
    }

    // Extract and validate
    if len(resp.Choices) == 0 || resp.Choices[0].Message == nil {
        return "", fmt.Errorf("fixer returned empty response")
    }

    fixed := strings.TrimSpace(resp.Choices[0].Message.Content)
    if !isValidJSON(fixed) {
        return "", fmt.Errorf("fixer returned invalid JSON")
    }

    return fixed, nil
}

func buildFixerUserPrompt(malformedJSON string) string {
    return "Fix this JSON. Return ONLY valid JSON:\n\n" + malformedJSON
}

func floatPtr(v float64) *float64 { return &v }
```

---

### 4. Integration

**File:** `pkg/toolrepair/repair.go`

Add `ctx context.Context` parameter and fixer call:

```go
func (r *Repairer) RepairArguments(ctx context.Context, arguments, toolName string) *RepairResult {
    // ... existing strategy loop ...

    // Try fixer model if strategies failed
    if !result.Success && r.config.FixerModel != "" && r.fixer != nil {
        fixed, err := r.fixer.Fix(ctx, arguments)
        if err == nil {
            result.Repaired = fixed
            result.Success = true
            result.Strategies = append(result.Strategies, "fixer_model")
            return result
        }
        // Log error but continue
        log.Printf("[TOOL-REPAIR] fixer failed: %v", err)
    }

    // Final fail
    result.Error = "all repair strategies failed"
    return result
}
```

Add fixer to Repairer struct:

```go
type Repairer struct {
    config  *Config
    fixer   *Fixer  // Add this
}

func (r *Repairer) SetFixer(fixer *Fixer) {
    r.fixer = fixer
}
```

---

### 5. Wire Up

**File:** `pkg/proxy/handler_functions.go` (or wherever repairer is created)

```go
// After creating repairer:
if repairConfig.FixerModel != "" {
    repairer.SetFixer(toolrepair.NewFixer(provider, repairConfig))
}
```

---

## Files to Modify

| File | Change | Lines |
|------|--------|-------|
| `pkg/toolrepair/repair_config.go` | Add 2 config fields | +5 |
| `pkg/toolrepair/fixer.go` | New file | +50 |
| `pkg/toolrepair/repair.go` | Add ctx, fixer field, fixer call | +20 |
| `pkg/config/config.go` | Add defaults | +3 |
| `pkg/providers/openai.go` | Pass ctx to repairer | +5 |

**Total: ~80 lines**

---

## Error Handling

| Case | Action |
|------|--------|
| `FixerModel` empty | Skip fixer (disabled) |
| Fixer timeout | Log error, return original |
| Fixer returns invalid JSON | Log error, return original |
| Fixer returns valid JSON | Use fixed JSON |

---

## What We're NOT Doing (v1)

| Skip | Reason |
|------|--------|
| `FixerEnabled` field | Check `FixerModel != ""` |
| `FixerMaxBytes` field | Reuse `MaxArgumentsSize` |
| Prompt template | Hardcoded is enough |
| Tool name context | Add later if needed |
| UI changes | Config file is enough |
| Multiple fixer models | Just one for now |

---

## Estimated Time

| Task | Time |
|------|------|
| Config changes | 5 min |
| Fixer implementation | 20 min |
| Integration | 10 min |
| Wire up | 5 min |
| Testing | 20 min |

**Total: ~1 hour**
