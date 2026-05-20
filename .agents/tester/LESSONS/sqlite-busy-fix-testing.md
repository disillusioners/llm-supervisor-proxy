# SQLite BUSY Fix — Testing Lessons

## Date: 2026-05-21
## Branch: `fix/sqlite-busy-usage-counter`

### The Fix
- Moved SQLite PRAGMAs to DSN string (WAL mode, busy_timeout=0, synchronous=FULL, foreign_keys=ON)
- Set `MaxOpenConns=1` for SQLite (serialized access) — skips generic pool config
- Removed retry logic from `counter.go` — not needed with serialized connection

### Stress Test Design
- 7 stress tests covering: pure Increment, pure IncrementModelUsage, mixed, high load, different buckets, query interleaving, config verification
- 30-50 goroutines × 50-100 iterations = 26,000+ concurrent operations
- All pass with `-race` flag
- Zero SQLITE_BUSY errors, zero lost writes

### Key Insight
The fix leverages SQLite's serialized mode (single connection via MaxOpenConns=1) rather than retrying on busy. This is simpler and more reliable than exponential backoff. WAL mode allows concurrent reads while writes are serialized.

### PostgreSQL Safety
- PostgreSQL path completely isolated in separate function
- `configurePool()` uses DefaultMaxOpenConns=25 for PostgreSQL
- SQLite's MaxOpenConns=1 never touches PostgreSQL path
