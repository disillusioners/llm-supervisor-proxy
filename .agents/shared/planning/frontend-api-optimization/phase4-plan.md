# Phase 4: Backend API Hardening

## Objective

Add request timeouts to database queries, configure connection pool limits, implement SSE heartbeat with stale connection cleanup, and add basic rate limiting middleware to protect the API from abuse.

## Coupling

- **Depends on**: None (fully independent of frontend phases)
- **Coupling type**: independent — all changes are in Go backend files, no overlap with TypeScript frontend
- **Shared files with other phases**: None
- **Why independent**: Backend Go code and frontend TypeScript have zero file overlap

## Context

- Current DB queries use `context.Background()` — no timeout, no cancellation
- `sql.Open()` called with no `SetMaxOpenConns`, `SetMaxIdleConns`, or `SetConnMaxLifetime`
- SSE handler (`handleEvents()` in server.go) has no heartbeat — stale connections accumulate
- No rate limiting on any API endpoint
- HTTP server has basic timeouts (ReadTimeout=30s, WriteTimeout=variable) but no per-request deadlines

## Tasks

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 1 | Add context timeouts to DB queries | Wrap `*sql.DB` with timeout-aware querier. Replace `context.Background()` with timeout contexts (5s for reads, 10s for writes) | `pkg/store/database/connection.go` |
| 2 | Configure connection pool limits | Add `SetMaxOpenConns(25)`, `SetMaxIdleConns(5)`, `SetConnMaxLifetime(5min)` after `sql.Open()`. Make configurable via env vars | `pkg/store/database/connection.go` |
| 3 | Add SSE heartbeat + stale cleanup | Add 30s heartbeat ping to SSE handler. Track last activity per connection. Clean up connections idle >2min. Add max connection age (10min) | `pkg/ui/server.go` (handleEvents) |
| 4 | Create rate limiting middleware | Token bucket rate limiter. Apply to `/fe/api/*` endpoints (not SSE). Configurable requests/min. Return 429 with Retry-After header | `pkg/middleware/ratelimit.go` (NEW) |
| 5 | Wire rate limiting into server | Apply rate limiting middleware to UI API routes in `RegisterHandlers()` | `pkg/ui/server.go` |
| 6 | Add request timeout middleware | Add per-request deadline (30s for UI API, separate from proxy timeout) | `pkg/ui/server.go` |

### Task Details

#### Task 1: Timeout Querier Wrapper

Rather than modifying generated sqlc code, wrap the `*sql.DB`:

```go
// pkg/store/database/connection.go

type TimeoutDB struct {
    *sql.DB
    defaultTimeout time.Duration
}

func NewTimeoutDB(db *sql.DB, timeout time.Duration) *TimeoutDB {
    return &TimeoutDB{DB: db, defaultTimeout: timeout}
}

func (tdb *TimeoutDB) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
    ctx, cancel := context.WithTimeout(ctx, tdb.defaultTimeout)
    defer cancel()
    return tdb.DB.QueryContext(ctx, query, args...)
}

func (tdb *TimeoutDB) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
    ctx, cancel := context.WithTimeout(ctx, tdb.defaultTimeout)
    defer cancel()
    return tdb.DB.ExecContext(ctx, query, args...)
}
```

**Timeouts:**
- Read queries: 5s default
- Write queries: 10s default (separate constant)
- Configurable via `DB_QUERY_TIMEOUT` and `DB_WRITE_TIMEOUT` env vars

**Files using context.Background() that need updating:**
- `pkg/store/database/store.go` lines 53, 412, 546 (and others)

#### Task 2: Connection Pool Configuration

```go
// In connection.go, after sql.Open():
func configurePool(db *sql.DB) {
    maxOpen := getEnvInt("DB_MAX_OPEN_CONNS", 25)
    maxIdle := getEnvInt("DB_MAX_IDLE_CONNS", 5)
    maxLifetime := getEnvDuration("DB_CONN_MAX_LIFETIME", 5*time.Minute)

    db.SetMaxOpenConns(maxOpen)
    db.SetMaxIdleConns(maxIdle)
    db.SetConnMaxLifetime(maxLifetime)
}
```

#### Task 3: SSE Heartbeat Implementation

```go
// In handleEvents() function:

// Heartbeat ticker
heartbeat := time.NewTicker(30 * time.Second)
defer heartbeat.Stop()

// Stale connection checker
staleChecker := time.NewTicker(1 * time.Minute)
defer staleChecker.Stop()

// Max connection age
maxAge := time.NewTimer(10 * time.Minute)
defer maxAge.Stop()

go func() {
    for {
        select {
        case <-heartbeat.C:
            fmt.Fprintf(w, ": heartbeat\n\n") // SSE comment = keepalive
            flusher.Flush()
        case <-staleChecker.C:
            if time.Since(lastActivity) > 2*time.Minute {
                cancel() // Close stale connection
                return
            }
        case <-maxAge.C:
            cancel() // Force reconnect after max age
            return
        case <-ctx.Done():
            return
        }
    }
}()
```

#### Task 4: Rate Limiting Middleware

```go
// pkg/middleware/ratelimit.go (NEW)

type RateLimiter struct {
    visitors map[string]*Visitor
    mu       sync.Mutex
    rate     int           // requests per minute
    burst    int           // burst size
    cleanupInterval time.Duration
}

type Visitor struct {
    tokens    float64
    lastSeen  time.Time
    mu        sync.Mutex
}

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        ip := extractIP(r)
        if !rl.Allow(ip) {
            w.Header().Set("Retry-After", "60")
            http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
            return
        }
        next.ServeHTTP(w, r)
    })
}
```

**Configuration:**
- Default: 120 requests/minute per IP (generous for single-user UI)
- Burst: 30 (allows rapid tab switching)
- Configurable via `API_RATE_LIMIT` and `API_RATE_BURST` env vars
- Applied only to `/fe/api/*` routes (NOT to proxy LLM routes or SSE)

#### Task 6: Request Timeout Middleware

```go
func TimeoutMiddleware(timeout time.Duration, next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        ctx, cancel := context.WithTimeout(r.Context(), timeout)
        defer cancel()
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}
```

Apply 30s timeout to all `/fe/api/*` routes.

## Key Files

- `pkg/store/database/connection.go` — Pool config + timeout wrapper
- `pkg/store/database/store.go` — Replace context.Background()
- `pkg/ui/server.go` — SSE heartbeat, rate limiting middleware, request timeout
- `pkg/middleware/ratelimit.go` — NEW rate limiter

## Constraints

- Rate limiting must NOT affect LLM proxy routes (only `/fe/api/*`)
- SSE connections must be excluded from rate limiting (long-lived)
- DB timeouts must be conservative enough to not break slow but valid queries
- All new parameters must have sensible defaults (work without env vars)
- Changes must be backward compatible (no breaking API changes)
- Pool configuration must work for both SQLite and PostgreSQL

## Deliverables

- [ ] Timeout-aware DB wrapper with configurable timeouts
- [ ] Connection pool limits configured (25 max open, 5 max idle)
- [ ] SSE sends heartbeat every 30s
- [ ] Stale SSE connections (>2min idle) cleaned up
- [ ] Rate limiting middleware with 120 req/min per IP
- [ ] Per-request 30s timeout on UI API routes
- [ ] All timeouts configurable via environment variables
- [ ] Existing tests continue to pass
