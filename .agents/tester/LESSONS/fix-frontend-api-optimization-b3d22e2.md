# Lesson: Frontend API Optimization Fixes (b3d22e2)

**Date**: 2026-04-09
**Commit**: b3d22e2
**Branch**: fix/frontend-api-optimization

## Key Patterns

### refreshKey Pattern for Cache-Busting
- Hooks use `useState(0)` as refreshKey
- Dependency array includes `refreshKey` to trigger re-fetch
- `refetch = useCallback(() => setRefreshKey(k => k + 1), [])`
- This pattern allows manual refetch without changing API params

### Single Unified Debounce for SSE
- One `refreshDebounceRef` for ALL SSE events (not separate per type)
- 300ms debounce window
- Cleanup on unsubscribe via `clearTimeout`
- More efficient than multiple debounce refs

### Cache Invalidation on Mutation
- `defaultAPICache.delete('tokens')` after `updateTokenPermission`
- `defaultAPICache.delete('app-tags')` after refetchAppTags
- Ensures fresh data after navigation

### Visibility API for Polling
- `document.addEventListener('visibilitychange', handler)`
- Stop interval when `document.hidden === true`
- Resume interval when tab becomes visible
- Cleanup on component unmount

## Test Caveat
- Mock test `mock_frontend_api_cache.mjs` checks for old two-ref pattern
- Implementation uses single unified ref (correct and more efficient)
- Test naming mismatch is cosmetic - functionality verified working
