# Phase 2: Duplicate Fetch Elimination

## Objective

Eliminate redundant API calls by consolidating credentials and providers data fetching into a single source of truth using React Context, reducing Settings page API calls from 8+ to ≤3.

## Coupling

- **Depends on**: Phase 1 (apiClient infrastructure with caching)
- **Coupling type**: tight — Phase 2 hooks use the `apiClient` from Phase 1; shares `useApi.ts` refactored hooks
- **Shared files with other phases**: `useApi.ts` (Phase 1), `SettingsPage.tsx` (Phase 3 for lazy loading)
- **Shared APIs/interfaces**: SettingsContext consumed by SettingsPage child components
- **Why this coupling**: Settings components need to call `useApiClient()` from Phase 1 for abort support

## Context

- Previous phase delivered: apiClient with cache + dedup, useApiClient hook, CacheProvider
- Key problem: In the Settings flow, credentials are fetched by 4 separate components:
  1. `SettingsPage.tsx` line 139 — `getCredentials()` on mount
  2. `CredentialsTab.tsx` line 41 — `getCredentials()` on mount
  3. `ConfigModal.tsx` line 116 — `getCredentials()` on modal open
  4. `CredentialForm.tsx` line 147 — `getCredentials()` on mount
- Similarly, `useProviders()` is called by both `ModelForm.tsx` and `CredentialsTab.tsx`

## Tasks

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 1 | Create `SettingsContext.tsx` | React context that holds credentials + providers state. Single fetch on provider mount. Expose `credentials`, `providers`, `refresh()`, `loading` | `pkg/ui/frontend/src/contexts/SettingsContext.tsx` (NEW) |
| 2 | Create `useSettingsData.ts` hook | Thin hook wrapping SettingsContext. Returns `{ credentials, providers, refreshCredentials, refreshProviders, loading }` | `pkg/ui/frontend/src/hooks/useSettingsData.ts` (NEW) |
| 3 | Update `SettingsPage.tsx` | Wrap children with `SettingsContextProvider`. Remove local `getCredentials()` useEffect. Pass credentials/providers via context instead of local state | `pkg/ui/frontend/src/components/SettingsPage.tsx` |
| 4 | Refactor `CredentialsTab.tsx` | Remove local `getCredentials()` call. Remove local `useProviders()` call. Consume from `useSettingsData()` | `pkg/ui/frontend/src/components/settings/CredentialsTab.tsx` |
| 5 | Refactor `CredentialForm.tsx` | Remove local `getCredentials()` call. Remove local `useProviders()` call. Get providers from `useSettingsData()` | `pkg/ui/frontend/src/components/settings/CredentialForm.tsx` |
| 6 | Refactor `ModelForm.tsx` | Remove local `useProviders()` call. Get providers from `useSettingsData()` | `pkg/ui/frontend/src/components/settings/ModelForm.tsx` |
| 7 | Refactor `ConfigModal.tsx` | Remove local credentials fetch on modal open. Get credentials from `useSettingsData()` | `pkg/ui/frontend/src/components/settings/ConfigModal.tsx` |
| 8 | Update TypeScript types | Add `SettingsContextType` interface. Update component prop interfaces where needed | `pkg/ui/frontend/src/types.ts` |

### Task Details

#### Task 1: SettingsContext Design

```typescript
interface SettingsContextType {
  credentials: Credential[];
  providers: Provider[];
  loadingCredentials: boolean;
  loadingProviders: boolean;
  refreshCredentials: () => Promise<void>;
  refreshProviders: () => Promise<void>;
}

function SettingsProvider({ children }: { children: React.ReactNode }) {
  const { client } = useApiClient(); // From Phase 1

  // Fetch credentials ONCE, share with all children
  const [credentials, setCredentials] = useState<Credential[]>([]);
  const [providers, setProviders] = useState<Provider[]>([]);

  // Single fetch on mount, with cache from Phase 1 apiClient
  useEffect(() => {
    client.fetch<Credential[]>('/credentials').then(setCredentials);
    client.fetch<Provider[]>('/providers').then(setProviders);
  }, [client]);

  // Refresh functions for mutations
  const refreshCredentials = useCallback(async () => {
    const data = await client.fetch<Credential[]>('/credentials', { ttl: 0 }); // bypass cache
    setCredentials(data);
  }, [client]);

  // ... similar for providers

  return (
    <SettingsContext.Provider value={{ credentials, providers, ... }}>
      {children}
    </SettingsContext.Provider>
  );
}
```

#### Task 3: SettingsPage.tsx Changes

**Before:**
```typescript
// Line 139-149: SettingsPage fetches credentials independently
useEffect(() => {
  const fetchCredentials = async () => {
    const data = await getCredentials();
    setCredentials(data || []);
  };
  fetchCredentials();
}, []);
```

**After:**
```typescript
// Remove useEffect, remove local credentials state
// Wrap children with SettingsProvider
<SettingsProvider>
  {/* tab content */}
</SettingsProvider>
```

#### Task 4-7: Component Refactor Pattern

Each component follows the same pattern:
1. Remove local fetch calls (`getCredentials()`, `useProviders()`)
2. Add `const { credentials, providers } = useSettingsData();`
3. Remove local loading state for these resources (use context's loading state)
4. Keep mutation functions (POST/PATCH/DELETE) local — only reads are shared

**Refresh after mutations**: After a credential mutation (create/update/delete), call `refreshCredentials()` from context to update shared state.

## Key Files

- `pkg/ui/frontend/src/contexts/SettingsContext.tsx` — NEW context provider (~100 lines)
- `pkg/ui/frontend/src/hooks/useSettingsData.ts` — NEW context consumer hook (~30 lines)
- `pkg/ui/frontend/src/components/SettingsPage.tsx` — Remove local fetch, add provider
- `pkg/ui/frontend/src/components/settings/CredentialsTab.tsx` — Use context
- `pkg/ui/frontend/src/components/settings/CredentialForm.tsx` — Use context
- `pkg/ui/frontend/src/components/settings/ModelForm.tsx` — Use context for providers
- `pkg/ui/frontend/src/components/settings/ConfigModal.tsx` — Use context

## Constraints

- SettingsContext should only be used within Settings flow (not Dashboard)
- Mutations (create/update/delete) remain in their respective components
- After mutations, must call refresh to update shared state
- Loading states for credentials/providers should show in consuming components
- CredentialsTab must still work when rendered outside SettingsProvider (defensive: fallback to fetch)

## Deliverables

- [ ] `SettingsContext.tsx` with single-fetch pattern
- [ ] `useSettingsData.ts` hook
- [ ] SettingsPage wraps children with SettingsProvider
- [ ] CredentialsTab uses context (no local fetch)
- [ ] CredentialForm uses context (no local fetch)
- [ ] ModelForm uses context for providers (no local useProviders)
- [ ] ConfigModal uses context (no local fetch on open)
- [ ] Settings page API calls reduced from 8+ to ≤3
- [ ] After credential/provider mutations, shared state refreshes correctly
