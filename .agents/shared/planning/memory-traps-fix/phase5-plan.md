# Phase 5: Minor Optimizations

## Objective
Fix remaining high-severity memory issues that don't fit into larger phases:
1. `strings.Builder` capacity hints in `handler_helpers.go`
2. Selective `json.Unmarshal` in `race_executor.go` (only chunks with usage)
3. String concatenation `+=` fix in `handler_internal.go`

## Coupling
- **Depends on**: None
- **Coupling type**: independent
- **Shared files with other phases**: None
- **Shared APIs/interfaces**: None
- **Why this coupling**: Small, unrelated fixes across different files

## Context
- These are smaller issues that can be fixed independently
- Each is straightforward but worth fixing for completeness
- Expected memory savings smaller but cumulative with other phases

## Tasks

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 1 | **Add strings.Builder capacity hints** | `requestContext.accumulatedResponse` and `accumulatedThinking` grow without capacity hints. Add `strings.Grow(n)` calls with reasonable estimates based on response size hints. | `pkg/proxy/handler_helpers.go` |
| 2 | **Selective json.Unmarshal** | `extractUsageFromSSEChunk()` unmarshals every chunk, but usage only appears in the last chunk. Add quick pre-check: `bytes.Contains(line, []byte("usage"))` before unmarshal. | `pkg/proxy/race_executor.go` |
| 3 | **Fix string concat in loop** | `internal_handler.go:296` uses `+=` in a loop for response building. Replace with `strings.Builder` or `bytes.Buffer`. | `pkg/ultimatemodel/handler_internal.go` |
| 4 | **Write tests** | Tests for new optimization behavior. Benchmark comparisons. | Relevant `_test.go` files |

## Key Files
- `pkg/proxy/handler_helpers.go` — Builder capacity issues
- `pkg/proxy/race_executor.go` — json.Unmarshal optimization
- `pkg/ultimatemodel/handler_internal.go` — String concat fix

## Implementation Details

### Task 2: Selective Unmarshal

```go
// Before:
func extractUsageFromSSEChunk(req *upstreamRequest, line []byte) {
    jsonPart := line[len(dataPrefix):]
    var chunk map[string]interface{}
    if err := json.Unmarshal(jsonPart, &chunk); err != nil {
        return
    }
    // ... extract usage
}

// After:
func extractUsageFromSSEChunk(req *upstreamRequest, line []byte) {
    // Quick check before expensive unmarshal
    if !bytes.Contains(line, []byte("\"usage\":")) {
        return
    }
    
    jsonPart := line[len(dataPrefix):]
    var chunk map[string]interface{}
    if err := json.Unmarshal(jsonPart, &chunk); err != nil {
        return
    }
    // ... extract usage
}
```

### Task 3: String Concat Fix

```go
// Before (handler_internal.go ~line 296):
var response string
for _, chunk := range chunks {
    response += chunk + "\n"
}

// After:
var builder strings.Builder
builder.Grow(len(chunks) * 200)  // Estimate
for _, chunk := range chunks {
    builder.Write(chunk)
    builder.WriteString("\n")
}
response := builder.String()
```

## Constraints
- Must not change functional behavior
- Must pass existing tests
- Performance improvement measurable with benchmarks

## Deliverables
- [ ] Builder capacity hints added
- [ ] Selective unmarshal implemented
- [ ] String concat fixed
- [ ] New tests for optimizations
- [ ] All existing tests pass
