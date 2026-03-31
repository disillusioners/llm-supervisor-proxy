# Phase 2: Backend API Endpoints

## Objective
Add `*database.Store` to the UI Server, then expose REST API endpoints to query hourly usage data per token with filtering and aggregation.

## Context
- **Previous Phase:** Phase 1 completed — `token_hourly_usage` table exists, usage extraction works, counting increments on requests
- **Critical Finding (C-6):** The UI `Server` struct has NO `*database.Store` field. It has `tokenStore *auth.TokenStore` (which has `*sql.DB` internally) but no direct database store access for custom queries.
- **Critical Finding (C-5):** sqlc is NOT used. All queries use `*sql.DB` directly with `QueryBuilder` for dialect handling.
- **Counter location:** `pkg/usage/counter.go` already has `GetTokenUsage()` query methods

---

## PART A: Wire Database Access to UI Server

### 1A — Add `dbStore` field to `Server` struct
**Why:** The usage API endpoints need database access.

| Detail | Value |
|--------|-------|
| File | `pkg/ui/server.go` |

**Current struct (lines 58-67):**
```go
type Server struct {
    bus          *events.Bus
    configMgr    config.ManagerInterface
    proxyConfig  *proxy.Config
    modelsConfig models.ModelsConfigInterface
    store        *store.RequestStore
    bufferStore  *bufferstore.BufferStore
    tokenStore   *auth.TokenStore
    mu           sync.Mutex
}
```

**Add field:**
```go
type Server struct {
    // ... existing fields ...
    tokenStore   *auth.TokenStore
    dbStore      *database.Store  // ADD: For usage queries
    mu           sync.Mutex
}
```

### 2A — Update `NewServer()` constructor
**Why:** Must accept and store the new `dbStore` parameter.

| Detail | Value |
|--------|-------|
| File | `pkg/ui/server.go` — `NewServer()` function |

**Current signature (lines 69-79):**
```go
func NewServer(
    bus *events.Bus,
    configMgr config.ManagerInterface,
    proxyConfig *proxy.Config,
    modelsConfig models.ModelsConfigInterface,
    store *store.RequestStore,
    bufferStore *bufferstore.BufferStore,
    tokenStore *auth.TokenStore,
) *Server
```

**Updated signature:**
```go
func NewServer(
    bus *events.Bus,
    configMgr config.ManagerInterface,
    proxyConfig *proxy.Config,
    modelsConfig models.ModelsConfigInterface,
    store *store.RequestStore,
    bufferStore *bufferstore.BufferStore,
    tokenStore *auth.TokenStore,
    dbStore *database.Store,  // ADD
) *Server {
    return &Server{
        // ... existing fields ...
        dbStore:      dbStore,  // ADD
    }
}
```

### 3A — Update `main.go` call site
**Why:** Pass `dbStore` to the UI server.

| Detail | Value |
|--------|-------|
| File | `cmd/main.go` |
| Location | Line ~93 |

**Current:**
```go
uiServer := ui.NewServer(bus, configMgr, proxyConfig, modelsConfig, reqStore, bufferStore, tokenStore)
```

**Updated:**
```go
uiServer := ui.NewServer(bus, configMgr, proxyConfig, modelsConfig, reqStore, bufferStore, tokenStore, dbStore)
```

---

## PART B: API Endpoints

All queries use `*sql.DB` directly with dialect-aware placeholders (`?` for SQLite, `$N` for PostgreSQL), following the pattern established in `pkg/auth/store.go` and `pkg/store/database/querybuilder.go`.

### 1B — Create usage handler file
**Why:** Separate file for usage-related API handlers.

| Detail | Value |
|--------|-------|
| File | `pkg/ui/handlers_usage.go` (new file) |

### 2B — Implement `GET /fe/api/usage`
**Why:** Primary endpoint for fetching hourly breakdown data.

| Detail | Value |
|--------|-------|
| File | `pkg/ui/handlers_usage.go` |

**Query Parameters:**
| Param | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `token_id` | string | No | all tokens | Filter to specific token |
| `from` | string | No | last 24h | Start hour (format: `2026-03-30T14` or `2026-03-30`) |
| `to` | string | No | now | End hour |
| `view` | string | No | `hourly` | `hourly` or `daily` |

**Response format:**
```json
{
  "token_id": "tok_abc123",
  "from": "2026-03-30T00",
  "to": "2026-03-31T00",
  "view": "hourly",
  "data": [
    {
      "hour_bucket": "2026-03-30T14",
      "request_count": 42,
      "prompt_tokens": 15000,
      "completion_tokens": 8000,
      "total_tokens": 23000
    }
  ],
  "totals": {
    "request_count": 500,
    "prompt_tokens": 200000,
    "completion_tokens": 100000,
    "total_tokens": 300000
  }
}
```

**Query implementation (dialect-aware):**
```go
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodGet {
        http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
        return
    }
    
    ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
    defer cancel()
    
    // Parse query params
    tokenID := r.URL.Query().Get("token_id")
    fromHour := r.URL.Query().Get("from")
    toHour := r.URL.Query().Get("to")
    view := r.URL.Query().Get("view")
    if view == "" { view = "hourly" }
    
    // Default time range: last 24 hours
    if fromHour == "" {
        fromHour = time.Now().UTC().Add(-24 * time.Hour).Format("2006-01-02T15")
    }
    if toHour == "" {
        toHour = time.Now().UTC().Format("2006-01-02T15")
    }
    
    // Build dialect-aware query
    var query string
    var args []interface{}
    
    if s.dbStore.Dialect == database.PostgreSQL {
        if tokenID != "" {
            query = `SELECT token_id, hour_bucket, request_count, prompt_tokens, completion_tokens, total_tokens 
                     FROM token_hourly_usage 
                     WHERE token_id = $1 AND hour_bucket >= $2 AND hour_bucket <= $3 
                     ORDER BY hour_bucket ASC`
            args = []interface{}{tokenID, fromHour, toHour}
        } else {
            query = `SELECT token_id, hour_bucket, request_count, prompt_tokens, completion_tokens, total_tokens 
                     FROM token_hourly_usage 
                     WHERE hour_bucket >= $1 AND hour_bucket <= $2 
                     ORDER BY token_id, hour_bucket ASC`
            args = []interface{}{fromHour, toHour}
        }
    } else {
        if tokenID != "" {
            query = `SELECT token_id, hour_bucket, request_count, prompt_tokens, completion_tokens, total_tokens 
                     FROM token_hourly_usage 
                     WHERE token_id = ? AND hour_bucket >= ? AND hour_bucket <= ? 
                     ORDER BY hour_bucket ASC`
            args = []interface{}{tokenID, fromHour, toHour}
        } else {
            query = `SELECT token_id, hour_bucket, request_count, prompt_tokens, completion_tokens, total_tokens 
                     FROM token_hourly_usage 
                     WHERE hour_bucket >= ? AND hour_bucket <= ? 
                     ORDER BY token_id, hour_bucket ASC`
            args = []interface{}{fromHour, toHour}
        }
    }
    
    // Execute and build response...
}
```

### 3B — Implement `GET /fe/api/usage/tokens`
**Why:** Frontend needs to know which tokens have usage data for a token selector.

| Detail | Value |
|--------|-------|
| File | `pkg/ui/handlers_usage.go` |

**Response format:**
```json
{
  "tokens": [
    { "token_id": "tok_abc123", "name": "Production App" },
    { "token_id": "tok_def456", "name": "Dev Testing" }
  ]
}
```

**Query:** JOIN with `auth_tokens` to resolve names (dialect-aware):
```go
// For PostgreSQL:
query = `SELECT DISTINCT u.token_id, COALESCE(a.name, 'Unknown') as name 
         FROM token_hourly_usage u 
         LEFT JOIN auth_tokens a ON u.token_id = a.id 
         ORDER BY u.token_id`

// For SQLite:
query = `SELECT DISTINCT u.token_id, COALESCE(a.name, 'Unknown') as name 
         FROM token_hourly_usage u 
         LEFT JOIN auth_tokens a ON u.token_id = a.id 
         ORDER BY u.token_id`
```

> **Note:** This query is identical across both dialects (no placeholders needed).

### 4B — Implement `GET /fe/api/usage/summary`
**Why:** Dashboard-level overview of all token usage.

| Detail | Value |
|--------|-------|
| File | `pkg/ui/handlers_usage.go` |

**Response format:**
```json
{
  "from": "2026-03-30T00",
  "to": "2026-03-31T00",
  "tokens": [
    {
      "token_id": "tok_abc123",
      "name": "Production App",
      "total_requests": 500,
      "total_prompt_tokens": 200000,
      "total_completion_tokens": 100000,
      "total_tokens": 300000
    }
  ],
  "grand_total": {
    "total_requests": 750,
    "total_prompt_tokens": 300000,
    "total_completion_tokens": 150000,
    "total_tokens": 450000
  }
}
```

### 5B — Register routes in `server.go`
**Why:** Wire up the new handlers.

| Detail | Value |
|--------|-------|
| File | `pkg/ui/server.go` — route registration section |

**Add routes:**
```go
mux.HandleFunc("/fe/api/usage", s.handleUsage)
mux.HandleFunc("/fe/api/usage/tokens", s.handleUsageTokens)
mux.HandleFunc("/fe/api/usage/summary", s.handleUsageSummary)
```

## Key Files
- `pkg/ui/server.go` — Server struct + constructor + route registration
- `pkg/ui/handlers_usage.go` — New file with all usage handlers
- `cmd/main.go` — Updated NewServer() call
- `pkg/usage/counter.go` — Counter with query methods (from Phase 1)

## Constraints
- **No sqlc** — use `*sql.DB` directly with `QueryBuilder`-style dialect branching
- Must follow existing handler patterns in `pkg/ui/server.go`
- Date parameters: accept ISO hour `"2026-03-30T14"` or ISO date `"2026-03-30"` (auto-expand)
- Proper error handling (404 for unknown token, 400 for bad params)
- All queries must work on both SQLite and PostgreSQL

## Deliverables
- [ ] `*database.Store` field added to `Server` struct
- [ ] `NewServer()` constructor updated with new parameter
- [ ] `main.go` call site updated
- [ ] `GET /fe/api/usage` returns hourly data with filtering
- [ ] `GET /fe/api/usage/tokens` returns list of tokens with usage
- [ ] `GET /fe/api/usage/summary` returns aggregated overview
- [ ] Routes registered and accessible
- [ ] All queries use dialect-aware placeholders
