# Plan Overview: Frontend API Optimization

## Objective

Reduce redundant frontend-backend API calls, add request lifecycle management (AbortController, caching, deduplication), eliminate duplicate data fetching in the Settings flow, and harden backend with timeouts, rate limiting, and SSE heartbeat.

## Scope Assessment

**LARGE** — 24 files (9 new, 15 modified), ~1000-1500 lines of changes across frontend TypeScript and backend Go. Spans multiple concerns: API infrastructure, data flow architecture, backend reliability, and testing.

## Context

- **Project:** llm-supervisor-proxy
- **Working Directory:** `/Users/nguyenminhkha/All/Code/opensource-projects/llm-supervisor-proxy`
- **Branch:** `fix/frontend-api-optimization`
- **Requested by:** Leader

### Investigation Summary

Verified via deep codebase exploration:

| Original Bug Report | Verified? | Reality |
|---|---|---|
| 8-12 API calls on page load | ⚠️ Partial | Dashboard: 5 calls (reasonable). Settings: 8+ due to credential/provider duplication |
| All tabs mount simultaneously | ❌ False | Tabs use conditional rendering — already lazy |
| app-tag called excessively | ❌ False | Only 1 fetch in App.tsx |
| setInterval without cleanup | ❌ False | All intervals have proper cleanup |
| Settings fetched in App + SettingsTab | ❌ False | Settings receives props |
| No AbortController | ✅ True | No cancellation mechanism anywhere |
| No caching/deduplication | ✅ True | Every hook fetches independently |
| Credentials fetched 4x in Settings | ✅ True | SettingsPage, CredentialsTab, ConfigModal, CredentialForm all fetch separately |
| Providers fetched 2x | ✅ True | ModelForm + CredentialsTab both call useProviders() |
| Backend DB queries use context.Background() | ✅ True | No timeouts on any DB query |
| No connection pool limits | ✅ True | sql.Open with no pool config |
| SSE no heartbeat | ✅ True | No stale connection cleanup |

## Phase Index

| Phase | Name | Objective | Dependencies | Coupling | Est. Complexity |
|-------|------|-----------|-------------|----------|-----------------|
| 1 | Frontend API Infrastructure | Create apiClient with AbortController, deduplication, TTL cache | None | — | High (8/10) |
| 2 | Duplicate Fetch Elimination | Consolidate credentials/providers to single source via React Context | Phase 1 | tight | Medium (6/10) |
| 3 | Frontend Optimization & Polish | React.lazy code splitting, useEffect cleanup, stale closure fixes | Phase 1 | loose | Medium (5/10) |
| 4 | Backend API Hardening | DB timeouts, connection pool limits, SSE heartbeat, rate limiting | None | independent | Medium-High (7/10) |
| 5 | Testing & Verification | Unit tests, integration tests, manual verification | Phases 1-4 | tight | Medium (5/10) |

### Coupling Assessment

| From → To | Coupling | Reason |
|-----------|----------|--------|
| Phase 1 → Phase 2 | **tight** | Phase 2 uses apiClient from Phase 1; refactored hooks share same file patterns |
| Phase 1 → Phase 3 | **loose** | Phase 3 adds Suspense/lazy wrapping around components; independent of apiClient internals |
| Phase 4 | **independent** | All backend Go code; no file overlap with frontend TypeScript |
| Phase 5 → Phases 1-4 | **tight** | Tests verify all implementations |

### Scheduling Recommendation

```
Phase 1 (frontend infrastructure)
    ├── Phase 2 (duplication fix)     ← after Phase 1
    ├── Phase 3 (polish)              ← after Phase 1, can overlap Phase 2 review
    └── Phase 4 (backend hardening)   ← INDEPENDENT, run in parallel with Phase 2-3
Phase 5 (testing)                     ← after all implementation
```

**Max parallelism: 2 coders** — one on frontend (Phase 2→3), one on backend (Phase 4).

## Risks & Mitigations

| Risk | Impact | Likelihood | Mitigation |
|------|--------|------------|------------|
| Cache invalidation bugs (stale data shown) | High | Medium | Aggressive invalidation on mutations; manual verification |
| Breaking existing hook consumers | High | Low | Incremental refactor — keep old API surface while adding new |
| Backend timeout too aggressive (false failures) | Medium | Medium | Configurable timeouts; start conservative (10s) |
| Rate limiting false positives on legitimate usage | Medium | Low | Start with generous limits; configurable |
| useApi.ts refactor breaks SSE reconnection | High | Low | SSE (useEvents.ts) is separate; don't modify its fetch pattern |
| React.lazy causes flash of loading state | Low | Medium | Add proper Suspense fallbacks |

## Success Criteria

- [ ] Dashboard page load: ≤5 API calls (maintained, no regression)
- [ ] Settings page load: ≤3 unique API calls (down from 8+)
- [ ] All fetch calls support AbortController cleanup on unmount
- [ ] In-flight request deduplication works (same endpoint = same Promise)
- [ ] Credentials fetched exactly 1 time per Settings page visit
- [ ] Providers fetched exactly 1 time per Settings page visit
- [ ] Backend DB queries have configurable timeouts
- [ ] SSE sends heartbeats every 30s, cleans stale connections
- [ ] All existing tests continue to pass
- [ ] New tests for apiClient, cache, and rate limiter pass

## File Change Summary

### New Files (9)
```
pkg/ui/frontend/src/lib/apiClient.ts              — Core API client with cache + dedup
pkg/ui/frontend/src/hooks/useApiClient.ts          — Hook wrapper with AbortController lifecycle
pkg/ui/frontend/src/contexts/CacheContext.tsx       — Shared cache state context
pkg/ui/frontend/src/contexts/SettingsContext.tsx    — Settings data (credentials, providers) context
pkg/ui/frontend/src/hooks/useSettingsData.ts        — Hook consuming SettingsContext
pkg/ui/frontend/src/lib/apiClient.test.ts           — Unit tests for apiClient
pkg/ui/frontend/src/contexts/CacheContext.test.tsx   — Unit tests for cache context
pkg/ui/frontend/src/lib/LoadingFallback.tsx          — Suspense fallback component
pkg/ui/middleware/ratelimit.go                       — Rate limiting middleware (Go)
```

### Modified Files (15)
```
pkg/ui/frontend/src/hooks/useApi.ts                 — Refactor to use apiClient
pkg/ui/frontend/src/App.tsx                         — Add CacheProvider, lazy imports
pkg/ui/frontend/src/components/SettingsPage.tsx      — Add SettingsContextProvider
pkg/ui/frontend/src/components/settings/CredentialsTab.tsx — Remove duplicate fetch
pkg/ui/frontend/src/components/settings/CredentialForm.tsx — Remove duplicate fetch
pkg/ui/frontend/src/components/settings/ModelForm.tsx      — Remove duplicate providers fetch
pkg/ui/frontend/src/components/settings/ConfigModal.tsx    — Remove duplicate credentials fetch
pkg/ui/frontend/src/types.ts                        — Add context types
pkg/store/database/connection.go                    — Add pool config + timeout wrapper
pkg/ui/server.go                                    — Add rate limiting middleware, SSE heartbeat
cmd/main.go                                         — Update server config
pkg/proxy/handler.go                                — Request context logging
pkg/middleware/ratelimit_test.go                     — Rate limiter tests (new test file)
```

## Tracking

- Created: 2026-04-09
- Last Updated: 2026-04-09
- Status: draft
