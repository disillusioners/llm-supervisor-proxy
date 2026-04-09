# Phase 1: Frontend API Infrastructure

## Objective

Create a centralized `apiClient` layer that wraps `fetch` with AbortController support, request deduplication (in-flight dedup), and TTL-based caching. Refactor all existing hooks in `useApi.ts` to use this new layer, ensuring every API call can be cancelled on component unmount.

## Coupling

- **Depends on**: None (foundation phase)
- **Coupling type**: N/A (root phase)
- **Shared files with other phases**: `pkg/ui/frontend/src/hooks/useApi.ts` (also touched by Phase 2)
- **Shared APIs/interfaces**: `apiClient` interface consumed by Phase 2, 3
- **Why this coupling**: apiClient is the foundation all other frontend phases build on

## Context

- Current state: All hooks in `useApi.ts` call a bare `apiFetch()` helper (line 10) that wraps `fetch()` with no cancellation, no caching, no deduplication
- The hooks return `[data, loading, error, fetchFn]` tuples
- All `useCallback` instances use `[]` deps (stable function identity) — this is good and should be preserved
- SSE handling in `useEvents.ts` is separate and already well-implemented — DO NOT MODIFY

## Tasks

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 1 | Create `apiClient.ts` core | Implement fetch wrapper with: AbortController auto-creation, in-flight request map (dedup by method+URL), TTL cache for GET requests, cache invalidation API, error handling | `pkg/ui/frontend/src/lib/apiClient.ts` (NEW) |
| 2 | Create `useApiClient.ts` hook | Hook that manages AbortController lifecycle: creates controller on mount, aborts all pending on unmount, provides `apiClient` instance with bound controller | `pkg/ui/frontend/src/hooks/useApiClient.ts` (NEW) |
| 3 | Create `CacheContext.tsx` | React context wrapping apiClient cache: provides `invalidateCache(key?)`, `clearCache()`, cache stats. LRU eviction when cache exceeds N entries | `pkg/ui/frontend/src/contexts/CacheContext.tsx` (NEW) |
| 4 | Refactor hooks in `useApi.ts` | Replace direct `apiFetch()` calls with `apiClient.fetch()`. Each hook uses `useApiClient()` for abort support. Configure per-endpoint TTL (config=60s, models=30s, tokens=30s, requests=5s, app-tags=120s, usage=10s) | `pkg/ui/frontend/src/hooks/useApi.ts` |
| 5 | Split `useApi.ts` into individual hook files | Extract each hook to its own file for maintainability: `useRequests.ts`, `useConfig.ts`, `useModels.ts`, `useTokens.ts`, `useAppTags.ts`, `useUsage.ts`, `useProviders.ts`, `useRam.ts`, `useVersion.ts` | `pkg/ui/frontend/src/hooks/*.ts` (NEW files) |
| 6 | Update `App.tsx` integration | Wrap app with `CacheProvider`. Update imports from split hook files. Verify parallel fetch behavior preserved | `pkg/ui/frontend/src/App.tsx` |

### Task Details

#### Task 1: apiClient.ts Design

```typescript
// Core types
interface ApiClientConfig {
  ttl?: number;           // Cache TTL in ms (0 = no cache)
  deduplicate?: boolean;  // Deduplicate in-flight requests (default: true)
  signal?: AbortSignal;   // External abort signal
}

interface CacheEntry<T> {
  data: T;
  timestamp: number;
  ttl: number;
}

class ApiClient {
  private cache: Map<string, CacheEntry<unknown>>;
  private inflight: Map<string, Promise<unknown>>;
  private maxCacheSize: number;

  async fetch<T>(path: string, options?: RequestInit & ApiClientConfig): Promise<T>;
  invalidate(key?: string): void;
  clear(): void;
}
```

**Key behaviors:**
- `fetch()` checks cache first (if TTL > 0), then inflight map, then makes real request
- Same URL returns same Promise while in-flight (dedup)
- Cache key = `METHOD:PATH` for GET, no caching for mutations
- TTL configurable per-call via `ApiClientConfig`
- `AbortSignal` from `useApiClient` propagated to all fetch calls

#### Task 2: useApiClient Hook Design

```typescript
function useApiClient() {
  const controllerRef = useRef<AbortController>(null);
  
  // Create new controller for each mount cycle
  useEffect(() => {
    controllerRef.current = new AbortController();
    return () => controllerRef.current?.abort(); // Abort on unmount
  }, []);

  const client = useMemo(() => new ApiClient(), []);
  client.setSignal(controllerRef.current?.signal);

  return { client };
}
```

#### Task 4: Hook Refactor Pattern

Each hook follows this pattern:
```typescript
export function useModels() {
  const { client } = useApiClient();
  const [models, setModels] = useState<Model[]>([]);
  const [loading, setLoading] = useState(true);

  const fetchModels = useCallback(async () => {
    setLoading(true);
    try {
      const data = await client.fetch<Model[]>('/models', { ttl: 30000 });
      setModels(data);
    } catch (err) {
      if (err instanceof DOMException && err.name === 'AbortError') return;
      console.error('Failed to fetch models:', err);
    } finally {
      setLoading(false);
    }
  }, [client]);

  useEffect(() => { fetchModels(); }, [fetchModels]);

  return { models, loading, fetchModels };
}
```

**AbortError handling**: Silently ignore aborted requests (expected behavior on unmount).

#### Task 5: File Split Strategy

Current `useApi.ts` is a single file with ~400+ lines and 10 hooks. Split into:
- `useApi.ts` → re-exports all hooks (backward compat shim)
- Individual files for each hook

This is optional but recommended for maintainability. If time-constrained, skip this and keep single file with refactored internals.

## Key Files

- `pkg/ui/frontend/src/lib/apiClient.ts` — Core API client (NEW, ~150-200 lines)
- `pkg/ui/frontend/src/hooks/useApiClient.ts` — Abort lifecycle hook (NEW, ~40 lines)
- `pkg/ui/frontend/src/contexts/CacheContext.tsx` — Cache context provider (NEW, ~80 lines)
- `pkg/ui/frontend/src/hooks/useApi.ts` — Refactored to use apiClient (~400 lines modified)

## Constraints

- SSE hooks (`useEvents.ts`) must NOT be modified — they work correctly
- All existing hook return types must be preserved (backward compatible)
- AbortError from cancelled requests must be silently caught (not surface as errors)
- Cache must be keyed by method+URL only (no body hash for simplicity)
- No external dependencies — implement cache/dedup with vanilla TypeScript

## Deliverables

- [ ] `apiClient.ts` with fetch wrapper, cache, and dedup
- [ ] `useApiClient.ts` hook with AbortController lifecycle
- [ ] `CacheContext.tsx` provider with invalidation API
- [ ] All hooks in `useApi.ts` refactored to use apiClient
- [ ] App.tsx wrapped with CacheProvider
- [ ] No AbortError surfacing to UI
- [ ] Existing functionality unchanged
