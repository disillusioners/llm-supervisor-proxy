# Phase 1+2 Frontend API Optimization

**Date:** 2026-04-08
**Branch:** `fix/frontend-api-optimization`
**Commit:** `0e1ff2c`

## Summary
Frontend API optimization: AbortController on all fetches, TTL cache + dedup, SSE debounce, ErrorBoundary.

## Key Files
- `pkg/ui/frontend/src/utils/apiCache.ts` (NEW) - TTL cache + request deduplication
- `pkg/ui/frontend/src/components/ErrorBoundary.tsx` (NEW) - React error boundary
- `pkg/ui/frontend/src/hooks/useApi.ts` (MODIFIED) - AbortController + cache integration
- `pkg/ui/frontend/src/hooks/useEvents.ts` (MODIFIED) - SSE debounce 300ms
- `pkg/ui/frontend/src/App.tsx` (MODIFIED) - ErrorBoundary wiring

## Cache TTLs
- useVersion: 300s, useProviders: 60s, useConfig/useAppTags: 30s, useModels/useTokens: 15s

## Notes
- Project uses Preact (not React)
- apiCache uses singleton `defaultAPICache` export
- Cache invalidation after all mutations (models, tokens, config, app-tags)
- Review: PASSED, no critical bugs
