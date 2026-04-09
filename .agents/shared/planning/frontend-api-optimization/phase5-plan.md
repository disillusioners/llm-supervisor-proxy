# Phase 5: Testing & Verification

## Objective

Write comprehensive tests for all new code (apiClient, cache, rate limiter), verify existing tests still pass, and perform manual verification of API call reduction and resource cleanup.

## Coupling

- **Depends on**: Phases 1-4 (all implementation must be complete)
- **Coupling type**: tight — tests verify all implementations from prior phases
- **Shared files with other phases**: All files from Phases 1-4 (read-only for testing)

## Context

- Phase 1: apiClient, useApiClient, CacheContext
- Phase 2: SettingsContext, useSettingsData, refactored components
- Phase 3: React.lazy, useEffect fixes
- Phase 4: Rate limiter, timeout wrapper, SSE heartbeat
- All existing tests must continue to pass (regression check)

## Tasks

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 1 | Unit tests for `apiClient.ts` | Test AbortController cancellation, request deduplication, TTL cache expiration, cache invalidation, error handling | `pkg/ui/frontend/src/lib/apiClient.test.ts` (NEW) |
| 2 | Unit tests for `CacheContext.tsx` | Test provider, cache sharing across consumers, invalidation | `pkg/ui/frontend/src/contexts/CacheContext.test.tsx` (NEW) |
| 3 | Integration tests for Settings flow | Test single credentials fetch, single providers fetch, context sharing, refresh after mutation | `pkg/ui/frontend/src/components/__tests__/SettingsContext.test.tsx` (NEW) |
| 4 | Backend rate limiter tests | Test token bucket logic, 429 response, Retry-After header, concurrent requests, IP isolation | `pkg/middleware/ratelimit_test.go` (NEW) |
| 5 | Backend timeout wrapper tests | Test query timeout, context cancellation, connection pool behavior | `pkg/store/database/connection_test.go` (modify) |
| 6 | Backend SSE heartbeat tests | Test heartbeat messages, stale connection cleanup, max connection age | `pkg/ui/server_test.go` (modify) |
| 7 | Run full existing test suite | Verify all existing Go + frontend tests still pass | `go test ./...`, `npm test` |
| 8 | Manual API call count verification | DevTools Network tab: count before/after API calls per page | Manual |
| 9 | Manual memory leak check | DevTools Memory tab: heap snapshots before/after navigation | Manual |

### Task Details

#### Task 1: apiClient Tests (~20 test cases)

```typescript
describe('ApiClient', () => {
  test('deduplicates identical GET requests', async () => { ... });
  test('does not deduplicate different endpoints', async () => { ... });
  test('caches GET responses within TTL', async () => { ... });
  test('bypasses cache when TTL=0', async () => { ... });
  test('invalidates specific cache key', async () => { ... });
  test('clears entire cache', async () => { ... });
  test('aborts in-flight request when signal aborted', async () => { ... });
  test('does not cache mutations (POST/PUT/DELETE)', async () => { ... });
  test('handles network errors', async () => { ... });
  test('handles AbortError silently', async () => { ... });
  test('LRU eviction when cache exceeds max size', async () => { ... });
});
```

#### Task 4: Rate Limiter Tests (~15 test cases)

```go
func TestRateLimiter_AllowWithinBurst(t *testing.T) { ... }
func TestRateLimiter_RejectOverLimit(t *testing.T) { ... }
func TestRateLimiter_429WithRetryAfter(t *testing.T) { ... }
func TestRateLimiter_PerIPIsolation(t *testing.T) { ... }
func TestRateLimiter_TokenRefill(t *testing.T) { ... }
func TestRateLimiter_ConcurrentRequests(t *testing.T) { ... }
func TestRateLimiter_DoesNotApplyToSSE(t *testing.T) { ... }
```

#### Task 8: Manual Verification Checklist

**Dashboard page load:**
| Endpoint | Before | After | Notes |
|----------|--------|-------|-------|
| GET /requests | 1 | 1 | No change needed |
| GET /config | 1 | 1 | No change needed |
| GET /models | 1 | 1 | No change needed |
| GET /tokens | 1 | 1 | No change needed |
| GET /app-tags | 1 | 1 | No change needed |
| GET /version | 1 | 1 | No change needed |
| GET /ram | 1 | 1 | Polling every 5s |
| SSE /events | 1 | 1 | No change needed |
| **Total** | **~5 + SSE** | **~5 + SSE** | ✓ Maintained |

**Settings page load (fresh):**
| Endpoint | Before | After | Notes |
|----------|--------|-------|-------|
| GET /config | 0 | 0 | Received via props |
| GET /models | 0 | 0 | Received via props |
| GET /tokens | 0 | 0 | Received via props |
| GET /credentials | 3-4 | 1 | Context: single fetch |
| GET /providers | 2 | 1 | Context: single fetch |
| **Total** | **5-6** | **2** | ✓ 60-70% reduction |

**Settings page load (cached):**
| Endpoint | Before | After | Notes |
|----------|--------|-------|-------|
| GET /credentials | 3-4 | 0 | Served from TTL cache |
| GET /providers | 2 | 0 | Served from TTL cache |
| **Total** | **5-6** | **0** | ✓ 100% reduction |

## Key Files

- `pkg/ui/frontend/src/lib/apiClient.test.ts` — NEW
- `pkg/ui/frontend/src/contexts/CacheContext.test.tsx` — NEW
- `pkg/ui/frontend/src/components/__tests__/SettingsContext.test.tsx` — NEW
- `pkg/middleware/ratelimit_test.go` — NEW
- `pkg/store/database/connection_test.go` — Modified
- `pkg/ui/server_test.go` — Modified

## Constraints

- All existing tests must pass without modification (backward compatibility)
- New tests should not require external dependencies (mock fetch, mock DB)
- Manual verification should be done in both Chrome and Firefox if possible
- Test coverage target: ≥80% for new code

## Deliverables

- [ ] apiClient unit tests passing
- [ ] CacheContext unit tests passing
- [ ] SettingsContext integration tests passing
- [ ] Rate limiter unit tests passing
- [ ] Timeout wrapper tests passing
- [ ] SSE heartbeat tests passing
- [ ] All existing Go tests pass (`go test ./...`)
- [ ] All existing frontend tests pass (`npm test`)
- [ ] Full build succeeds (`go build` + `npm run build`)
- [ ] Manual verification report: API call counts before/after
- [ ] Manual verification report: no memory leaks detected
