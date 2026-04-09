# Phase 3: Frontend Optimization & Polish

## Objective

Add React.lazy() code splitting for tab components, verify and fix useEffect dependency arrays, and resolve any stale closure risks in hooks.

## Coupling

- **Depends on**: Phase 1 (apiClient foundation)
- **Coupling type**: loose — adds Suspense boundaries and lazy imports around existing components; independent of apiClient internals
- **Shared files with other phases**: `App.tsx` (also modified by Phase 1 for CacheProvider), `SettingsPage.tsx` (also modified by Phase 2 for context)
- **Why this coupling**: Lazy loading wraps the component tree; must be added after Phase 1 changes to App.tsx are in place

## Context

- Phase 1 delivered: apiClient, useApiClient hook, CacheProvider in App.tsx
- Phase 2 delivered: SettingsContext, consolidated data fetching
- Current state: All component imports are static (no React.lazy)
- useEffect hooks mostly have stable `useCallback` deps but some may have ESLint warnings
- SSE (useEvents.ts) already has proper cleanup — no changes needed

## Tasks

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 1 | Add React.lazy() for Settings page | Wrap `SettingsPage` import with `lazy()`. Add Suspense boundary with loading fallback | `pkg/ui/frontend/src/App.tsx` |
| 2 | Create `LoadingFallback.tsx` | Simple spinner/skeleton component for Suspense fallback | `pkg/ui/frontend/src/components/LoadingFallback.tsx` (NEW) |
| 3 | Audit useEffect dependencies | Check all hooks for missing/extra deps. Fix any that cause unnecessary re-fetches | `pkg/ui/frontend/src/hooks/*.ts` |
| 4 | Fix stale closure in `useEventRefresh` | `useEventRefresh` callback may capture stale `onRefresh` reference. Use ref pattern to avoid | `pkg/ui/frontend/src/hooks/useEvents.ts` |
| 5 | Verify polling cleanup | Confirm `useRam` (5s interval) and `useVersion` cleanup works with new apiClient | `pkg/ui/frontend/src/hooks/useApi.ts` |
| 6 | Review usage hook dependencies | `useUsage` has 5 deps in useEffect — verify all are stable references after Phase 1 refactor | `pkg/ui/frontend/src/hooks/useApi.ts` (useUsage section) |

### Task Details

#### Task 1: React.lazy Pattern

```typescript
// Before:
import SettingsPage from './components/SettingsPage';

// After:
const SettingsPage = lazy(() => import('./components/SettingsPage'));

// In route/render:
<Suspense fallback={<LoadingFallback />}>
  <SettingsPage {...props} />
</Suspense>
```

**Scope:** Only lazy-load the Settings page (it's the heaviest component with multiple tabs). Dashboard is the default page and should load eagerly.

#### Task 3: useEffect Audit Checklist

For each hook in `useApi.ts`:
- [ ] `useRequests`: deps `[fetchRequests]` — `fetchRequests` should be stable via useCallback
- [ ] `useConfig`: deps `[fetchConfig]` — stable
- [ ] `useModels`: deps `[fetchModels]` — stable
- [ ] `useTokens`: deps `[fetchTokens]` — stable
- [ ] `useAppTags`: deps `[fetchAppTags]` — stable, but ESLint may warn if callback references `client` from Phase 1
- [ ] `useUsage`: deps `[selectedTokenId, from, to, view, fetchSummary, fetchUsage]` — verify fetch functions stable
- [ ] `useRam`: deps `[fetchRam]` — stable; interval cleanup verified

**Common fix pattern:** If `client` from `useApiClient()` causes dep changes, memoize client or use ref:
```typescript
const { client } = useApiClient();
const clientRef = useRef(client);
clientRef.current = client;
```

#### Task 4: useEventRefresh Stale Closure Fix

Current pattern (useEvents.ts):
```typescript
// Line 234
useEffect(() => {
  const handler = (event) => onRefresh(event); // captures onRefresh at effect time
  subscribe(handler);
  return () => unsubscribe(handler);
}, [onRefresh]); // if parent doesn't memoize onRefresh, re-subscribes every render
```

Fix with ref:
```typescript
const onRefreshRef = useRef(onRefresh);
onRefreshRef.current = onRefresh;

useEffect(() => {
  const handler = (event) => onRefreshRef.current(event);
  subscribe(handler);
  return () => unsubscribe(handler);
}, [subscribe]); // stable dependency
```

## Key Files

- `pkg/ui/frontend/src/App.tsx` — Lazy import for SettingsPage
- `pkg/ui/frontend/src/components/LoadingFallback.tsx` — NEW fallback component
- `pkg/ui/frontend/src/hooks/useApi.ts` — useEffect dependency audit
- `pkg/ui/frontend/src/hooks/useEvents.ts` — Stale closure fix

## Constraints

- Only lazy-load Settings page (not Dashboard — it's the default)
- Do NOT modify SSE connection logic in useEvents.ts (only the callback subscription pattern)
- Must preserve all existing behavior (polling intervals, SSE reconnection)
- LoadingFallback should match existing app styling

## Deliverables

- [ ] SettingsPage lazy-loaded with Suspense boundary
- [ ] LoadingFallback component created
- [ ] All useEffect hooks audited and fixed
- [ ] useEventRefresh stale closure fixed
- [ ] No ESLint react-hooks/exhaustive-deps warnings
- [ ] All polling intervals clean up correctly with new apiClient
