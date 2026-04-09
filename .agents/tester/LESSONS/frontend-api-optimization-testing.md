# Frontend API Optimization Testing Patterns

**Date**: 2026-04-09
**Commit**: 0e1ff2c
**Project**: llm-supervisor-proxy

## What Was Tested

### 1. APICache Class
Location: `pkg/ui/frontend/src/utils/apiCache.ts`

**Key behaviors verified:**
- TTL-based expiration
- Request deduplication (in-flight requests)
- getOrFetch pattern
- delete/deleteByPrefix/sweep/destroy

**Test file**: `test/mock_frontend_api_cache.mjs` (615 lines)

### 2. AbortController Integration
All 10 hooks in `useApi.ts` properly implement AbortController:
- useRequests, useRequestDetail, useConfig, useModels, useAppTags
- useVersion, useRam, useTokens, useProviders, useUsage

**Pattern**: Create controller in useEffect → pass signal to fetch → abort in cleanup

### 3. Cache TTL Strategy
| Data Type | TTL | Rationale |
|-----------|-----|----------|
| Config | 30s | Frequently updated by admin |
| Models | 15s | May change often (enable/disable) |
| Tokens | 15s | Auth state changes frequently |
| Providers | 60s | Rarely changes |
| App Tags | 30s | Moderate change frequency |
| Version | 300s | Rarely changes |

### 4. useRam() Visibility API
Polling stops when tab is hidden:
```typescript
const handleVisibilityChange = () => {
  if (!document.hidden) {
    fetchRam();
  }
};
document.addEventListener('visibilitychange', handleVisibilityChange);
```

### 5. SSE Debounce
300ms debounce prevents cascading refetches:
```typescript
const debouncedRefreshRequests = () => {
  if (requestsDebounceRef.current) {
    clearTimeout(requestsDebounceRef.current);
  }
  requestsDebounceRef.current = setTimeout(() => {
    onRefresh();
  }, 300);
};
```

## Key Insights

1. **AbortController must be created inside useEffect** - not in the component body, to avoid stale closures
2. **Cache invalidation on mutations** - All POST/PUT/DELETE operations must call `defaultAPICache.delete()`
3. **Visibility API prevents wasted polling** - Saves bandwidth when tab is in background
4. **Debounce prevents thundering herd** - Multiple SSE events don't trigger multiple refetches

## Testing Approach

For frontend/browser-based features, we used:
1. Code analysis to verify implementation patterns
2. Node.js mock tests for APICache class (can run outside browser)
3. Manual verification of integration points

## Known Limitations

- Browser-specific behaviors (visibility API, AbortController) cannot be fully tested in Node.js
- Real user testing recommended for:
  - Closing tab during load (AbortController cancellation)
  - Cache effectiveness on page refresh
  - ErrorBoundary graceful degradation

## Related Files

- `test/mock_frontend_api_cache.mjs` - APICache unit tests
- `pkg/ui/frontend/src/utils/apiCache.ts` - Cache implementation
- `pkg/ui/frontend/src/hooks/useApi.ts` - All API hooks
- `pkg/ui/frontend/src/hooks/useEvents.ts` - SSE debounce
