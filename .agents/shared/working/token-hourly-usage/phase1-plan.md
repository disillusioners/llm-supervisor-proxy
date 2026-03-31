# Phase 1: Usage Extraction & Backend Data Collection

## Objective
Extract usage data from LLM API responses, thread token identity through the request lifecycle, create the `token_hourly_usage` database table, and hook counting logic into all 7 finalization points in `handler.go`.

## Context
- **Previous Phase:** N/A (first phase)
- **Critical Findings:**
  - **C-1:** All finalization is inline at 7 locations in `handler.go` — no `finalizeSuccess()` or `handleModelFailure()` functions exist
  - **C-2 (DEEPEST):** Usage data (`store.Usage`) is **never extracted** from upstream responses anywhere. The structures exist but are always nil.
  - Usage data IS available in the code path (e.g., `event.Response.Usage` in internal streaming), but it's only injected into the SSE stream for the client — never stored
  - `authenticate()` discards the `*AuthToken` — only 1 caller exists (line ~335 in `handler.go`)
  - sqlc is NOT used — queries use `*sql.DB` directly with `QueryBuilder` for dialect handling

---

## PART A: Usage Extraction (PREREQUISITE — Must Be Done First)

This is the foundational work. Without extracting usage from responses, the counting feature has no data to count.

### 1A — Add usage extraction to `streamResult()` — Streaming path
**Why:** For streaming responses, usage data arrives in the **last SSE chunk** before `[DONE]`. We need to parse it and attach it to the request log.

| Detail | Value |
|--------|-------|
| File | `pkg/proxy/handler.go` — within `streamResult()` function |
| Location | Lines 635-654, specifically the `case <-buffer.Done():` block after draining chunks |

**Current code (lines 635-654):**
```go
case <-buffer.Done():
    // Stream complete - drain remaining data
    chunks, _ := buffer.GetChunksFrom(readIndex)
    for _, chunk := range chunks {
        if _, err := w.Write(chunk); err != nil {
            return
        }
        if bytes.HasPrefix(chunk, []byte("data: ")) {
            data := bytes.TrimPrefix(chunk, []byte("data: "))
            extractStreamChunkContent(data, &rc.accumulatedResponse, &rc.accumulatedThinking, &rc.accumulatedToolCalls, &rc.toolCallArgBuilders)
        }
        readIndex++
    }
```

**Changes needed:**
1. After the drain loop, add a **reverse scan** of chunks to find the last chunk containing `usage`:
```go
case <-buffer.Done():
    // Stream complete - drain remaining data
    chunks, _ := buffer.GetChunksFrom(readIndex)
    for _, chunk := range chunks {
        if _, err := w.Write(chunk); err != nil {
            return
        }
        if bytes.HasPrefix(chunk, []byte("data: ")) {
            data := bytes.TrimPrefix(chunk, []byte("data: "))
            extractStreamChunkContent(data, &rc.accumulatedResponse, &rc.accumulatedThinking, &rc.accumulatedToolCalls, &rc.toolCallArgBuilders)
        }
        readIndex++
    }
    
    // NEW: Extract usage from the last SSE chunk (it contains the "usage" field)
    for i := len(chunks) - 1; i >= 0; i-- {
        chunk := chunks[i]
        if bytes.HasPrefix(chunk, []byte("data: ")) {
            data := bytes.TrimPrefix(chunk, []byte("data: "))
            if string(data) == "[DONE]" || string(data) == "" {
                continue
            }
            if usage := extractUsageFromChunk(data); usage != nil {
                rc.reqLog.Usage = usage
                break
            }
        }
    }
```

2. Create a new helper function in `handler_helpers.go`:
```go
// extractUsageFromChunk parses usage data from an SSE chunk JSON payload.
// Returns nil if no usage field is present.
func extractUsageFromChunk(data []byte) *store.Usage {
    var chunk map[string]interface{}
    if err := json.Unmarshal(data, &chunk); err != nil {
        return nil
    }
    usageData, ok := chunk["usage"].(map[string]interface{})
    if !ok {
        return nil
    }
    return &store.Usage{
        PromptTokens:     int(usageData["prompt_tokens"].(float64)),
        CompletionTokens: int(usageData["completion_tokens"].(float64)),
        TotalTokens:      int(usageData["total_tokens"].(float64)),
    }
}
```

### 2A — Add usage extraction to `handleNonStreamResult()` — Non-streaming path
**Why:** For non-streaming responses, the JSON body contains usage alongside the message content.

| Detail | Value |
|--------|-------|
| File | `pkg/proxy/handler.go` — within `handleNonStreamResult()` function |
| Location | Lines 844-856, after `json.Unmarshal(finalBody, &resp)` |

**Current code (lines 844-856):**
```go
// Extract content for logging
var resp map[string]interface{}
if err := json.Unmarshal(finalBody, &resp); err == nil {
    if choices, ok := resp["choices"].([]interface{}); ok && len(choices) > 0 {
        if choice, ok := choices[0].(map[string]interface{}); ok {
            if msg, ok := choice["message"].(map[string]interface{}); ok {
                if content, ok := msg["content"].(string); ok {
                    rc.accumulatedResponse.WriteString(content)
                }
            }
        }
    }
}
```

**Changes needed:**
```go
// Extract content and usage for logging
var resp map[string]interface{}
if err := json.Unmarshal(finalBody, &resp); err == nil {
    // Extract content (existing code)
    if choices, ok := resp["choices"].([]interface{}); ok && len(choices) > 0 {
        if choice, ok := choices[0].(map[string]interface{}); ok {
            if msg, ok := choice["message"].(map[string]interface{}); ok {
                if content, ok := msg["content"].(string); ok {
                    rc.accumulatedResponse.WriteString(content)
                }
            }
        }
    }
    
    // NEW: Extract usage from response
    if usageData, ok := resp["usage"].(map[string]interface{}); ok {
        rc.reqLog.Usage = &store.Usage{
            PromptTokens:     int(usageData["prompt_tokens"].(float64)),
            CompletionTokens: int(usageData["completion_tokens"].(float64)),
            TotalTokens:      int(usageData["total_tokens"].(float64)),
        }
    }
}
```

### 3A — Add usage extraction to internal non-streaming path
**Why:** Internal provider non-streaming responses go through a different code path.

| Detail | Value |
|--------|-------|
| Files to check | `pkg/providers/` — look for internal non-streaming handler |
| Pattern | Find where `ChatCompletionResponse` is used and where `resp.Usage` is available but not stored |

**Changes needed:** Find the code path where internal non-streaming responses are processed and extract `resp.Usage` into the request context or buffer for later retrieval.

> **Note:** This task may require additional investigation during implementation to find the exact code path. Mark as TBD if the internal non-streaming provider code path is not found in initial exploration.

### 4A — Wire usage into race executor's SetUsage
**Why:** The `upstreamRequest.SetUsage()` / `GetUsage()` methods exist but are never called.

| Detail | Value |
|--------|-------|
| Files | `pkg/proxy/race_executor.go` |
| Location | After a response is fully received, call `upstreamReq.SetUsage()` |

**Changes needed:** In `handleStreamingResponse()` and `handleNonStreamingResponse()`, after the full response is read, extract usage and call `SetUsage()` so the winner's usage can be retrieved after the race completes.

---

## PART B: Token Identity Propagation

### 1B — Modify `authenticate()` to return token identity
**Why:** Currently returns only `bool` and discards `*AuthToken`. Need to thread token ID/name into `requestContext`.

| Detail | Value |
|--------|-------|
| File | `pkg/proxy/handler.go` — `authenticate()` function |
| Location | Lines 244-261 |
| Callers | Only 1 — at line ~335 |

**Current code:**
```go
func (h *Handler) authenticate(r *http.Request) bool {
    if h.tokenStore == nil { return true }
    apiKey := h.extractAPIKey(r)
    if apiKey == "" { return false }
    ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
    defer cancel()
    _, err := h.tokenStore.ValidateToken(ctx, apiKey)  // ← discards AuthToken
    return err == nil
}
```

**Change to:**
```go
func (h *Handler) authenticate(r *http.Request) (*auth.AuthToken, bool) {
    if h.tokenStore == nil { return nil, true }
    apiKey := h.extractAPIKey(r)
    if apiKey == "" { return nil, false }
    ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
    defer cancel()
    token, err := h.tokenStore.ValidateToken(ctx, apiKey)
    if err != nil { return nil, false }
    return token, true
}
```

### 2B — Update the single caller of `authenticate()`
**Why:** The only caller (line ~335) must use the new signature.

| Detail | Value |
|--------|-------|
| File | `pkg/proxy/handler.go` |
| Location | Lines 335-352 |

**Current code:**
```go
if h.requiresInternalAuth(rc) {
    if !h.authenticate(r) {
        // Update request log to failed status
        rc.reqLog.Status = "failed"
        // ...
    }
}
```

**Change to:**
```go
if h.requiresInternalAuth(rc) {
    token, ok := h.authenticate(r)
    if !ok {
        // Update request log to failed status
        rc.reqLog.Status = "failed"
        // ...
    } else {
        // Store token identity for counting
        if token != nil {
            rc.tokenID = token.ID
            rc.tokenName = token.Name
        }
    }
}
```

### 3B — Add `tokenID` and `tokenName` fields to `requestContext`
**Why:** Thread token identity through the request lifecycle.

| Detail | Value |
|--------|-------|
| File | `pkg/proxy/handler_helpers.go` |
| Location | Lines 22-79 (`requestContext` struct) |

**Add to struct:**
```go
type requestContext struct {
    // ... existing fields ...
    tokenID   string  // Token ID for counting
    tokenName string  // Token name for display
}
```

### 4B — Add `TokenID` and `TokenName` fields to `RequestLog`
**Why:** Store token identity with each request log entry.

| Detail | Value |
|--------|-------|
| File | `pkg/store/memory.go` |
| Location | `RequestLog` struct (lines 40-72) |

**Add to struct:**
```go
type RequestLog struct {
    // ... existing fields ...
    TokenID   string `json:"token_id"`
    TokenName string `json:"token_name"`
}
```

---

## PART C: Database Table and Counting Hook

### 1C — Create migration 019: `token_hourly_usage` table
**Why:** New table to persist hourly aggregated usage data.

| Detail | Value |
|--------|-------|
| Files | `pkg/store/database/migrations/sqlite/019_token_hourly_usage.up.sql` |
| | `pkg/store/database/migrations/postgres/019_token_hourly_usage.up.sql` |
| Registration | `pkg/store/database/migrate.go` |
| Table name | `token_hourly_usage` (consistent naming throughout) |

**SQLite migration:**
```sql
CREATE TABLE token_hourly_usage (
    token_id           TEXT    NOT NULL,
    hour_bucket        TEXT    NOT NULL,
    request_count      INTEGER NOT NULL DEFAULT 0,
    prompt_tokens      INTEGER NOT NULL DEFAULT 0,
    completion_tokens  INTEGER NOT NULL DEFAULT 0,
    total_tokens       INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (token_id, hour_bucket)
);
CREATE INDEX idx_token_hourly_usage_token ON token_hourly_usage(token_id);
CREATE INDEX idx_token_hourly_usage_hour ON token_hourly_usage(hour_bucket);
```

**PostgreSQL migration:**
```sql
CREATE TABLE token_hourly_usage (
    token_id           TEXT    NOT NULL,
    hour_bucket        TEXT    NOT NULL,
    request_count      INTEGER NOT NULL DEFAULT 0,
    prompt_tokens      INTEGER NOT NULL DEFAULT 0,
    completion_tokens  INTEGER NOT NULL DEFAULT 0,
    total_tokens       INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (token_id, hour_bucket)
);
CREATE INDEX idx_token_hourly_usage_token ON token_hourly_usage(token_id);
CREATE INDEX idx_token_hourly_usage_hour ON token_hourly_usage(hour_bucket);
```

### 2C — Create the counter component
**Why:** A dedicated component for incrementing usage counters.

| Detail | Value |
|--------|-------|
| File | `pkg/usage/counter.go` (new file) |

**Implementation using `QueryBuilder` pattern (NOT sqlc):**
```go
package usage

import (
    "context"
    "database/sql"
    "fmt"
    
    "github.com/user/llm-supervisor-proxy/pkg/store/database"
    "github.com/user/llm-supervisor-proxy/pkg/store/memory"
)

type Counter struct {
    db      *sql.DB
    dialect database.Dialect
}

func NewCounter(db *sql.DB, dialect database.Dialect) *Counter {
    return &Counter{db: db, dialect: dialect}
}

// Increment adds usage for a token in a specific hour bucket.
// Uses UPSERT (INSERT OR REPLACE / ON CONFLICT) for atomic increment.
func (c *Counter) Increment(ctx context.Context, tokenID, hourBucket string, reqCount, promptTok, completionTok, totalTok int) error {
    var query string
    if c.dialect == database.PostgreSQL {
        query = `INSERT INTO token_hourly_usage (token_id, hour_bucket, request_count, prompt_tokens, completion_tokens, total_tokens)
                 VALUES ($1, $2, $3, $4, $5, $6)
                 ON CONFLICT (token_id, hour_bucket) DO UPDATE SET
                     request_count = token_hourly_usage.request_count + EXCLUDED.request_count,
                     prompt_tokens = token_hourly_usage.prompt_tokens + EXCLUDED.prompt_tokens,
                     completion_tokens = token_hourly_usage.completion_tokens + EXCLUDED.completion_tokens,
                     total_tokens = token_hourly_usage.total_tokens + EXCLUDED.total_tokens`
        _, err := c.db.ExecContext(ctx, query, tokenID, hourBucket, reqCount, promptTok, completionTok, totalTok)
        return err
    }
    // SQLite
    query = `INSERT INTO token_hourly_usage (token_id, hour_bucket, request_count, prompt_tokens, completion_tokens, total_tokens)
             VALUES (?, ?, ?, ?, ?, ?)
             ON CONFLICT (token_id, hour_bucket) DO UPDATE SET
                 request_count = request_count + excluded.request_count,
                 prompt_tokens = prompt_tokens + excluded.prompt_tokens,
                 completion_tokens = completion_tokens + excluded.completion_tokens,
                 total_tokens = total_tokens + excluded.total_tokens`
    _, err := c.db.ExecContext(ctx, query, tokenID, hourBucket, reqCount, promptTok, completionTok, totalTok)
    return err
}

// HourlyUsageRow represents a single row of usage data.
type HourlyUsageRow struct {
    TokenID          string
    HourBucket       string
    RequestCount     int
    PromptTokens     int
    CompletionTokens int
    TotalTokens      int
}

// GetTokenUsage retrieves hourly usage for a specific token within a time range.
func (c *Counter) GetTokenUsage(ctx context.Context, tokenID, fromHour, toHour string) ([]HourlyUsageRow, error) {
    qb := database.NewQueryBuilder(c.dialect)
    
    var query string
    var args []interface{}
    
    if c.dialect == database.PostgreSQL {
        query = `SELECT token_id, hour_bucket, request_count, prompt_tokens, completion_tokens, total_tokens
                 FROM token_hourly_usage
                 WHERE token_id = $1 AND hour_bucket >= $2 AND hour_bucket <= $3
                 ORDER BY hour_bucket ASC`
        args = []interface{}{tokenID, fromHour, toHour}
    } else {
        query = `SELECT token_id, hour_bucket, request_count, prompt_tokens, completion_tokens, total_tokens
                 FROM token_hourly_usage
                 WHERE token_id = ? AND hour_bucket >= ? AND hour_bucket <= ?
                 ORDER BY hour_bucket ASC`
        args = []interface{}{tokenID, fromHour, toHour}
    }
    
    rows, err := c.db.QueryContext(ctx, query, args...)
    if err != nil {
        return nil, fmt.Errorf("query usage: %w", err)
    }
    defer rows.Close()
    
    var results []HourlyUsageRow
    for rows.Next() {
        var r HourlyUsageRow
        err := rows.Scan(&r.TokenID, &r.HourBucket, &r.RequestCount, &r.PromptTokens, &r.CompletionTokens, &r.TotalTokens)
        if err != nil {
            return nil, fmt.Errorf("scan row: %w", err)
        }
        results = append(results, r)
    }
    return results, rows.Err()
}
```

### 3C — Wire counter into `Handler`
**Why:** The handler needs the counter to increment on each request.

| Detail | Value |
|--------|-------|
| File | `pkg/proxy/handler.go` — `NewHandler()` constructor |
| Location | Lines 99-133 |

**Changes:**
1. Add `counter *usage.Counter` field to `Handler` struct
2. Add `counter *usage.Counter` parameter to `NewHandler()`
3. Initialize counter in constructor

**In `main.go`:** Pass `dbStore.DB` and `dbStore.Dialect` to `usage.NewCounter()` and pass to `proxy.NewHandler()`.

### 4C — Hook counting into all 7 finalization points in `handler.go`
**Why:** Every request completion must increment the counter.

| # | Location | Status | Lines | Counting |
|---|----------|--------|-------|---------|
| 1 | Auth failure | `failed` | 337-342 | ❌ Skip (no valid token) |
| 2 | Ultimate retry exhausted | `failed` | 382-388 | ❌ Skip (no valid token) |
| 3 | Ultimate execution error | `failed` | 428-432 | ❌ Skip (no valid token) |
| 4 | Ultimate success | `completed` | 454-457 | ✅ Count if `rc.tokenID != ""` |
| 5 | All race models failed | `failed` | 537-541 | ❌ Skip (no valid token) |
| 6 | Streaming success | `completed` | 754-758 | ✅ Count if `rc.tokenID != ""` |
| 7 | Non-streaming success | `completed` | 859-868 | ✅ Count if `rc.tokenID != ""` |

**Pattern to add at each success point (after `h.store.Add(rc.reqLog)`):**
```go
// Count this request for hourly usage tracking
if rc.tokenID != "" && h.counter != nil {
    var promptTokens, completionTokens, totalTokens int
    if rc.reqLog.Usage != nil {
        promptTokens = rc.reqLog.Usage.PromptTokens
        completionTokens = rc.reqLog.Usage.CompletionTokens
        totalTokens = rc.reqLog.Usage.TotalTokens
    }
    hourBucket := rc.reqLog.StartTime.UTC().Format("2006-01-02T15")
    go func() {
        if err := h.counter.Increment(context.Background(), rc.tokenID, hourBucket, 1, promptTokens, completionTokens, totalTokens); err != nil {
            // Log error but don't fail the request
            log.Printf("failed to increment usage counter: %v", err)
        }
    }()
}
```

> **Note:** The counting is fired in a goroutine to avoid blocking the response. The `rc.tokenID` is already populated at authentication time (line ~335-352), so it's available at all finalization points.

**For auth-failed and other error paths (points 1, 2, 3, 5):** These reach finalization without going through the auth pass, so `rc.tokenID` is empty. No counting needed.

## Key Files
- `pkg/proxy/handler.go` — `authenticate()` change, 7 finalization points, counter wiring
- `pkg/proxy/handler_helpers.go` — `requestContext` struct, `extractUsageFromChunk()` helper
- `pkg/store/memory.go` — `RequestLog` struct
- `pkg/usage/counter.go` — New counter component (entire file)
- `pkg/store/database/migrations/sqlite/019_token_hourly_usage.up.sql` — SQLite migration
- `pkg/store/database/migrations/postgres/019_token_hourly_usage.up.sql` — PostgreSQL migration
- `pkg/store/database/migrate.go` — Migration registration
- `pkg/providers/*.go` — Internal non-streaming usage extraction (TBD)

## Constraints
- **No sqlc** — use `*sql.DB` directly with `QueryBuilder` for dialect handling
- Counting goroutine must not block the request response
- Gracefully handle nil usage (count request, tokens = 0)
- `authenticate()` caller update must be precise (only 1 call site)
- Hour bucket format: `"2006-01-02T15"` (e.g., `"2026-03-31T14"`)

## Deliverables
- [ ] `authenticate()` returns `(*auth.AuthToken, bool)` — token identity threaded into requestContext
- [ ] `extractUsageFromChunk()` helper parses SSE usage data
- [ ] `handleNonStreamResult()` extracts usage from JSON body
- [ ] Internal non-streaming usage extraction (TBD if path exists)
- [ ] `RequestLog` has `TokenID`, `TokenName`, and `Usage` fields populated
- [ ] `token_hourly_usage` table exists with SQLite + PostgreSQL migrations
- [ ] `pkg/usage/counter.go` with `Increment()` using dialect-aware UPSERT
- [ ] Counter wired into handler via `NewHandler()`
- [ ] All 3 success finalization points (4, 6, 7) increment counter
- [ ] No regression in existing proxy functionality
