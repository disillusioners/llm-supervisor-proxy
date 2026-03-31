# Architecture Decisions: Token-level Hourly Request Counting

## Decision 1: Database-level UPSERT vs In-Memory Buffer

**Options Considered:**
| Option | Pros | Cons |
|--------|------|------|
| A. Direct DB UPSERT per request | Simple, durable, no data loss | Potential write contention under high load |
| B. In-memory buffer + periodic flush | Fewer DB writes, faster | Data loss on crash, more complex, memory usage |
| C. Hybrid (buffer + periodic flush to DB) | Best of both worlds | Most complex to implement |

**Decision:** Option A — Direct DB UPSERT per request

**Rationale:**
- SQLite WAL mode handles concurrent writes well for typical LLM proxy loads
- Simplicity wins — no background goroutines, no flush logic, no data loss
- LLM requests are inherently I/O-bound (waiting for upstream); the UPSERT overhead is negligible
- Can optimize to Option C later if needed

---

## Decision 2: Hourly Bucket Format

**Options:**
| Format | Example | Sortable | Human-Readable |
|--------|---------|----------|----------------|
| ISO hour | `2026-03-30T14` | ✅ Yes | ✅ Good |
| Unix timestamp (floored) | `1743352800` | ✅ Yes | ❌ Poor |
| Custom format | `2026-03-30_14` | ✅ Yes | ⚠️ OK |

**Decision:** ISO hour format — `"2006-01-02T15"` (e.g., `"2026-03-30T14"`)

**Rationale:**
- Naturally sortable as strings
- Human-readable without transformation
- Easy to generate in Go: `time.Now().UTC().Format("2006-01-02T15")`
- Easy to parse in frontend JavaScript

---

## Decision 3: authenticate() Refactoring Approach

**Options:**
| Option | Description |
|--------|-------------|
| A. Change signature to return `(*AuthToken, bool)` | Minimal change, 1 caller to update |
| B. Context-based propagation via `context.WithValue` | Standard Go pattern |
| C. Add new method `authenticateAndGetToken()` | Non-breaking, but duplication |

**Decision:** Option A — Change `authenticate()` to return `(*auth.AuthToken, bool)`

**Rationale:**
- Only 1 caller exists in the codebase (handler.go line ~335)
- Simplest approach — just update the return type and use both values
- Avoids context value overhead
- Token identity flows naturally through the return value into `requestContext`

**Implementation:**
```go
// pkg/proxy/handler.go
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

---

## Decision 4: Counter Component Location

**Options:**
| Option | Location |
|--------|----------|
| A. New `pkg/usage/` package | Dedicated package |
| B. Inside `pkg/store/database/` | Co-located with DB queries |
| C. Method on `Server` struct | Co-located with UI server |

**Decision:** Option A — New `pkg/usage/` package

**Rationale:**
- Clean separation of concerns
- The counter is a domain concept (usage tracking), not a database concern
- Can be tested independently
- Handler depends on counter interface, not database details
- Query methods live here (not in the UI layer)

**Interface:**
```go
// pkg/usage/counter.go
type Counter struct {
    db      *sql.DB
    dialect database.Dialect
}

func (c *Counter) Increment(ctx context.Context, tokenID, hourBucket string, reqCount, promptTok, completionTok, totalTok int) error
func (c *Counter) GetTokenUsage(ctx context.Context, tokenID, fromHour, toHour string) ([]HourlyUsageRow, error)
```

---

## Decision 5: Frontend Visualization Approach

**Options:**
| Option | Description | Bundle Size |
|--------|-------------|-------------|
| A. HTML table + CSS bar charts | Lightweight, no dependencies | 0 KB |
| B. Chart.js / recharts | Rich charting | ~50-100 KB |
| C. SVG-based custom charts | Moderate complexity | ~5 KB |

**Decision:** Option A — HTML table + CSS bar charts

**Rationale:**
- Zero new dependencies — keeps bundle small
- The existing UI uses Tailwind for all styling
- Bar charts can be achieved with Tailwind `w-[X%]` classes
- Can upgrade to a chart library later if needed
- Tables are actually more useful for hourly data (precise numbers matter)

---

## Decision 6: Handling Requests Without Token Store

**Scenario:** When `tokenStore == nil` (auth disabled), no token counting happens.

**Decision:** Skip counting silently — no error, no log noise.

**Rationale:**
- When auth is disabled, there's no token identity to count
- This is the existing behavior (requests pass through without authentication)
- Adding a special "anonymous" count is a future enhancement, not a requirement

---

## Decision 7: Token Name Resolution in Usage Queries

**Problem:** `token_hourly_usage` stores `token_id` but not `token_name`. API responses need names.

**Options:**
| Option | Description |
|--------|-------------|
| A. JOIN with auth_tokens in SQL | Always fresh names, single query |
| B. Store name in usage table too | Denormalized, faster queries |
| C. Resolve in Go after query | Two queries, clean separation |

**Decision:** Option A — JOIN with `auth_tokens` in SQL

**Rationale:**
- Most efficient (single query)
- Always shows current token name (even if renamed)
- `auth_tokens` is a small table — JOIN cost is negligible
- Follows existing patterns in `pkg/auth/store.go`

---

## Decision 8: Database Query Pattern (sqlc vs raw SQL)

**Problem:** The plan originally referenced sqlc-generated code, but the project uses `*sql.DB` directly.

**Options:**
| Option | Description |
|--------|-------------|
| A. Use sqlc for new queries | Consistent code generation |
| B. Use `*sql.DB` directly with dialect branching | Matches existing auth/store.go pattern |

**Decision:** Option B — Use `*sql.DB` directly with dialect branching

**Rationale:**
- The project already uses this pattern in `pkg/auth/store.go` and `pkg/store/database/querybuilder.go`
- sqlc is not configured for the project (only SQLite in `sqlc.yaml`)
- Dialect handling is done via `QueryBuilder.Placeholder()` (`?` vs `$N`)
- Raw `*sql.DB` queries are simple, debuggable, and match existing code style

---

## Decision 9: Counting Goroutine (Fire-and-Forget)

**Problem:** Counting should not block the request response.

**Options:**
| Option | Description |
|--------|-------------|
| A. Synchronous counting (block response) | Ensures data is persisted before response |
| B. Goroutine (fire-and-forget) | Non-blocking, fast response |
| C. Channel-based async | Structured concurrency |

**Decision:** Option B — Goroutine with error logging

**Rationale:**
- LLM requests are slow; the counting UPSERT adds latency to the response
- For a monitoring feature, occasional missed counts are acceptable
- Log errors so operators can detect database issues
- Can be upgraded to buffered channel or batch writes later

```go
if rc.tokenID != "" && h.counter != nil {
    go func() {
        if err := h.counter.Increment(context.Background(), ...); err != nil {
            log.Printf("failed to increment usage counter: %v", err)
        }
    }()
}
```

---

## Decision 10: Usage Extraction Strategy

**Problem:** Usage data exists in LLM responses but is never extracted.

**Decision:** Parse usage in the finalization paths

**Rationale:**
- Streaming: The last SSE chunk before `[DONE]` contains `usage`. Scan chunks in reverse.
- Non-streaming: Parse the JSON body; `resp["usage"]` is available after `json.Unmarshal`.
- Extract at handler finalization time (not in race executor) to keep changes localized
- Use `nil` usage gracefully — count the request but record 0 tokens

---

## C-2 Resolution: Usage Extraction Scope

**The Deepest Issue:** `store.Usage` is **never populated** anywhere. The feature is useless without it.

**Scope:** This is a prerequisite that must be completed in Phase 1 before counting can work.

**Tasks identified:**
1. Parse SSE chunks in reverse in `streamResult()` to find usage data
2. Parse `resp["usage"]` in `handleNonStreamResult()` non-streaming path
3. Check internal non-streaming provider path (may need additional investigation)
4. Wire into `upstreamRequest.SetUsage()` / `GetUsage()` so winner's usage is retrievable

**Verification:** After Phase 1, run a test request and verify `RequestLog.Usage` is non-nil in the store.
