# Memory Trap Investigation - 2026-04-08

## Symptom: ~2GB memory with 1-2 concurrent requests

## Documents Found
- `docs/golang-trap/golang_memory_traps.md` - 20 common Go memory traps
- `golang_traps_audit_report.md` - Previous audit (found only time.After issues, now fixed)
- The previous audit **missed the real systemic issues** - it only checked surface patterns

## Root Cause Assessment
The 2GB memory consumption is caused by **multiple compounding issues**, not a single bug:
1. Race retry pattern (3 parallel requests) multiplies all per-request allocations by 3x
2. GetAllRawBytes() is called multiple times, each allocating full buffer size
3. Unbounded data accumulation in streaming paths
4. Per-request HTTP clients with their own connection pools
5. Backing array retention via slice/string operations

## Priority-Ranked Issues

### CRITICAL (Most Impactful)

1. **GetAllRawBytes() called multiple times** (handler.go)
   - Allocates ENTIRE buffer contents each call
   - 3-5 calls per request × race retry × buffer size = 4.5-7.5GB potential
   - Fix: Stream to file instead of capturing full bytes

2. **Unbounded dataLines collection** (handler_external.go:182-203)
   - Accumulates ALL SSE data lines in memory for usage extraction
   - Only needs last chunk with usage field
   - Fix: Track only last usage-bearing line

3. **Losing request buffers not released** (race_coordinator.go:503)
   - After winner selected, losing buffers stay in memory
   - Fix: Add buffer cleanup in Cancel()

4. **HTTP response body not released on cancel** (race_executor.go:160)
   - defer only runs when goroutine exits, not on cancel
   - Fix: Drain and close body in Cancel()

5. **Per-request HTTP Client** (handler_external.go:75)
   - New client with own connection pool per request
   - Fix: Reuse global client

6. **GetChunksFrom(0) double copy** (stream_buffer.go:101)
   - Creates copy of all chunks when readIndex=0
   - Fix: Return slice view without copy
