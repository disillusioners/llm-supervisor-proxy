# Phase 4: UltimateModel Memory Optimization

## Objective
Fix memory issues in `pkg/ultimatemodel/`:
1. Shared HTTP client instead of per-request allocation
2. Eliminate `dataLines` buffering (track only last usage-bearing chunk)
3. Reduce byte/string conversions per SSE chunk

## Coupling
- **Depends on**: None
- **Coupling type**: independent
- **Shared files with other phases**: None (different package)
- **Shared APIs/interfaces**: None
- **Why this coupling**: Changes are contained within ultimatemodel package

## Context
- `handler_external.go` creates a new `http.Client` + `http.Transport` for every request
- 100 idle connections allocated then immediately discarded — GC pressure
- `dataLines` slice stores ALL SSE data lines for reverse-scan at end
- Multiple `[]byte(line)` conversions per chunk (up to 4 per chunk)

## Tasks

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 1 | **Create shared HTTP client** | Define module-level `var httpClient = &http.Client{...}` with reusable `http.Transport`. Use this client in all request methods. | `pkg/ultimatemodel/handler_external.go` |
| 2 | **Eliminate dataLines buffering** | Replace slice accumulation with tracking of last usage-bearing chunk only. Use a single `[]byte` variable. | `pkg/ultimatemodel/handler_external.go` |
| 3 | **Reduce byte/string conversions** | Work with `[]byte` throughout the streaming loop. Compare bytes directly instead of converting to string. | `pkg/ultimatemodel/handler_external.go` |
| 4 | **Fix tool call chunk processing** | `toolcall.ProcessChunk()` does string conversion internally. Optimize or replace with byte-native version. | `pkg/ultimatemodel/handler_external.go`, `pkg/toolcall/buffer.go` |
| 5 | **Write ultimatemodel tests** | Test shared client concurrent usage. Test streaming without dataLines. Test byte conversion optimization. | `pkg/ultimatemodel/handler_external_test.go` |

## Key Files
- `pkg/ultimatemodel/handler_external.go` — Main optimization target (HTTP client, dataLines, conversions)
- `pkg/ultimatemodel/handler_internal.go` — May need similar fixes for consistency
- `pkg/toolcall/buffer.go` — Tool call processing, internal byte conversions

## Implementation Details

### Task 1: Shared HTTP Client

```go
// Package-level shared client
var (
    sharedClient = &http.Client{
        Timeout: 0,
        Transport: &http.Transport{
            MaxIdleConns:        100,
            MaxIdleConnsPerHost: 100,
            IdleConnTimeout:     300 * time.Second,
        },
    }
)

// In request methods:
resp, err := sharedClient.Do(upstreamReq)
```

### Task 2: Track Last Usage-Bearing Chunk

```go
// Before (accumulates all lines):
var dataLines [][]byte
// ... in loop ...
dataLines = append(dataLines, data)
// ... at end ...
for i := len(dataLines) - 1; i >= 0; i-- {
    if usage = extractUsageFromChunk(dataLines[i]); usage != nil {
        break
    }
}

// After (tracks only last):
var lastUsageChunk []byte
// ... in loop ...
// Only store if this looks like a usage-bearing chunk
if bytes.Contains(data, []byte("usage")) || bytes.Contains(data, []byte("total_tokens")) {
    lastUsageChunk = data
}
// ... at end ...
if lastUsageChunk != nil {
    usage = extractUsageFromChunk(lastUsageChunk)
}
```

### Task 3: Reduce Conversions

```go
// Before:
line, err := reader.ReadString('\n')  // Returns string
if bytes.HasPrefix([]byte(line), []byte("data: ")) {  // Convert to []byte
    data := bytes.TrimPrefix([]byte(line), []byte("data: "))  // 2nd conversion
    dataStr := string(data)  // Convert to string
    if dataStr != "[DONE]\n" && strings.Contains(dataStr, "content") {
        dataLines = append(dataLines, data)
    }
}

// After (use bufio.Reader with []byte):
for {
    line, err := reader.ReadBytes('\n')  // Returns []byte directly
    if bytes.HasPrefix(line, dataPrefix) {  // No conversion needed
        data := bytes.TrimPrefix(line, dataPrefix)
        // Work with data as []byte throughout
        if !bytes.Equal(data, doneMarker) && bytes.Contains(data, contentMarker) {
            lastUsageChunk = data
        }
    }
}
```

## Constraints
- `http.Client` must be goroutine-safe — it is by design
- Tool call processing may depend on string format — verify behavior unchanged
- SSE streaming must remain functional
- Existing `handler_external_test.go` tests must pass

## Risks & Mitigations

| Risk | Mitigation |
|------|------------|
| Shared client state leaks between requests | `http.Client` is stateless for requests; only `Transport` is shared |
| Tool call parsing breaks | Test with existing test cases, verify output identical |
| Usage extraction misses edge case | Ensure last chunk with "usage" is still captured |

## Deliverables
- [ ] Shared HTTP client with connection pooling
- [ ] `dataLines` slice eliminated (replaced with `lastUsageChunk`)
- [ ] Single `[]byte` type throughout streaming loop
- [ ] New tests for shared client and optimized streaming
- [ ] All existing handler_external_test.go pass
