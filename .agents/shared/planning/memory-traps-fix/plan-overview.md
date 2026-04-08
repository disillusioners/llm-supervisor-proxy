# Plan Overview: Fix Go Memory Traps

## Objective
Fix 12 memory issues in llm-supervisor-proxy that cause ~2GB memory consumption with only 1-2 concurrent requests. The race retry pattern (3 parallel requests) multiplies every per-request allocation by 3×, making these optimizations critical.

## Scope Assessment
**LARGE** — 11 actual issues (1 false positive: retryCounter map is properly bounded), across 7 files in 2 packages. Multiple interdependencies require careful phasing. Estimated 2-3 days.

## Context
- **Project**: llm-supervisor-proxy
- **Working Directory**: `/Users/nguyenminhkha/All/Code/opensource-projects/llm-supervisor-proxy`
- **Branch**: `fix/memory-traps` (from `latest`)
- **Root Cause**: Per-request allocations × 3 parallel race requests × multiple redundant copies = 2GB+

## Issue Verification

| # | Issue | Severity | Verified | Notes |
|---|-------|----------|----------|-------|
| 1 | Repeated `GetAllRawBytes()` copies | CRITICAL | ✅ | 5 call sites, each allocates full response |
| 2 | Unbounded `dataLines` collection | CRITICAL | ✅ | Stores ALL SSE lines, only last needed |
| 3 | Losing request buffers NOT released | CRITICAL | ✅ | `cancelAllExcept()` only cancels context |
| 4 | HTTP response body never released | CRITICAL | ✅ | `defer` only on goroutine exit |
| 5 | Per-request HTTP client | CRITICAL | ✅ | New `http.Client`+`Transport` each time |
| 6 | `GetChunksFrom(0)` double copy | CRITICAL | ✅ | Two `make()` calls + filtering |
| 7 | `strings.Builder` never shrinks | HIGH | ✅ | No capacity hints, 10MB+ possible |
| 8 | Double `[]byte(line)` per chunk | HIGH | ✅ | 4 conversions per SSE chunk |
| 9 | `json.Unmarshal` every SSE chunk | HIGH | ✅ | 10K chunks → 10K unmarshals |
| 10 | Pruning only on 10ms ticker | HIGH | ✅ | Buffer grows 10× larger |
| 11 | `retryCounter` unbounded growth | HIGH | ❌ **FALSE POSITIVE** | Circular buffer eviction works correctly |
| 12 | String concat `+=` O(n²) | HIGH | ✅ | In `internal_handler.go:296` |

## Phase Index

| Phase | Name | Objective | Dependencies | Coupling | Key Files |
|-------|------|-----------|-------------|----------|-----------|
| 1 | Stream Buffer Optimizations | Eliminate redundant buffer copies and improve pruning | None | — | `stream_buffer.go` |
| 2 | Race Cancel Cleanup | Fix response body and buffer leaks on winner selection | None | — | `race_executor.go`, `race_request.go`, `race_coordinator.go` |
| 3 | Handler Logging Dedup | Call `GetAllRawBytes()` once and reuse | Phase 1 (API changes) | loose | `handler.go` |
| 4 | UltimateModel Memory | Shared HTTP client, eliminate dataLines buffering, reduce conversions | None | — | `handler_external.go` |
| 5 | Minor Optimizations | Builder capacity, JSON selective unmarshal, string concat fix | None | — | `handler_helpers.go`, `race_executor.go`, `handler_internal.go` |

### Coupling Assessment

| From → To | Coupling | Reason | Schedule |
|-----------|----------|--------|----------|
| Phase 1 → Phase 3 | **loose** | Phase 3 depends on Phase 1's new API (`GetAllRawBytesOnce()`), not implementation | Pipeline: start Phase 3 after Phase 1 review |
| Phase 2 | **independent** | Different files, no shared APIs | Can run parallel with Phase 1, 4, 5 |
| Phase 4 | **independent** | Different package entirely | Can run parallel with Phase 1, 2, 5 |
| Phase 5 | **independent** | Unrelated changes across files | Can run parallel with Phase 1, 2, 4 |

**Parallelization Opportunities:**
- Phases 1, 2, 4, 5 are all **independent** — can run in parallel
- Phase 3 has **loose** dependency on Phase 1 — pipeline after Phase 1 review
- **Recommended**: Run Phase 1 + Phase 2 in parallel, then Phase 3 + Phase 4 + Phase 5 in parallel

### Estimated Memory Savings

| Phase | Before | After | Savings |
|-------|--------|-------|---------|
| 1 | ~15MB/req (3-5 full copies) | ~3MB/req (1 copy) | **~12MB/req × 3 race = 36MB** |
| 2 | ~30MB leaked (2 losers' buffers) | 0MB leaked | **~30MB** |
| 3 | ~6MB extra (redundant calls) | 0MB extra | **~6MB × 3 = 18MB** |
| 4 | ~20MB (dataLines + client overhead) | ~0.1MB | **~60MB** |
| 5 | ~5MB (Builder + unmarshal + concat) | ~0.5MB | **~15MB** |
| **Total** | | | **~159MB per request cycle** |

With proper cleanup + reduced GC pressure, total memory should drop from ~2GB to ~200-400MB.

## Risks & Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Race condition in buffer cleanup | HIGH | Add comprehensive tests with concurrent cancel scenarios |
| Breaking streaming behavior | HIGH | Existing 231+ tests provide strong regression coverage |
| Shared HTTP client thread safety | MEDIUM | `http.Client` is documented as goroutine-safe |
| Buffer view (no-copy) mutation | MEDIUM | Return read-only semantics; document clearly |
| Phase 3 depends on Phase 1 API | LOW | Define interface early, Phase 3 only needs method signature |

## Test Strategy

1. **Existing tests MUST pass** — 231+ backend tests are the regression gate
2. **New tests per phase:**
   - Phase 1: Buffer copy benchmark test, pruning threshold test
   - Phase 2: Cancel cleanup verification test (goroutine + memory leak test)
   - Phase 4: Shared HTTP client concurrent test, streaming without dataLines test
3. **Memory validation**: Run `go test -bench=. -benchmem` before and after
4. **Integration verification**: Run full test suite + manual load test with `pprof`

## Success Criteria
- [ ] All 11 verified issues fixed (issue #11 confirmed false positive)
- [ ] All existing 231+ tests pass
- [ ] New tests for critical fixes (Phase 1, 2, 4)
- [ ] Memory consumption drops to <500MB under 2 concurrent requests
- [ ] No streaming behavior regressions

## Tracking
- Created: 2026-04-08
- Last Updated: 2026-04-08
- Status: draft
