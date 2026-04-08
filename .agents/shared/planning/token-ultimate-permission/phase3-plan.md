# Phase 3: Frontend — Token UI for Ultimate Model Permission

## Objective
Update the frontend token management UI to display the `ultimate_model_enabled` field, allow toggling it during token creation, and provide a toggle button in the token list to enable/disable the permission for existing tokens.

## Coupling
- **Depends on**: Phase 1 (needs PATCH `/fe/api/tokens/{id}` endpoint + API responses including `ultimate_model_enabled`)
- **Coupling type**: **loose** — only depends on the API contract (JSON response shape), not on internal implementation
- **Shared files with other phases**: None (frontend files are separate from backend)
- **Shared APIs/interfaces**: REST API endpoints from Phase 1
- **Why this coupling**: Frontend consumes the API. As long as Phase 1 delivers the correct JSON shape, Phase 3 is independent.

## Context
- Frontend stack: Preact + TypeScript + Tailwind
- Token components in `pkg/ui/frontend/src/components/tokens/`: `TokenList.tsx`, `TokenForm.tsx`
- Token hook: `pkg/ui/frontend/src/hooks/useTokens.ts`
- Types: `pkg/ui/frontend/src/types.ts` — `ApiToken` interface
- Settings page: `pkg/ui/frontend/src/components/SettingsPage.tsx` — 7 tabs
- No PATCH/PUT for tokens exists yet — need to add `updateToken()` to hook
- Boolean toggle pattern: **pill-shaped slider** used in ProxySettings (e.g., `raceRetryEnabled`, `idleTerminationEnabled`)

## Tasks

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 1 | Update ApiToken type | Add `ultimate_model_enabled: boolean` to `ApiToken` interface in `types.ts` | `pkg/ui/frontend/src/types.ts` |
| 2 | Add updateToken to useTokens hook | Add `updateToken(id: string, data: { ultimate_model_enabled: boolean })` function that calls PATCH `/fe/api/tokens/{id}`. Handle loading/error states. | `pkg/ui/frontend/src/hooks/useTokens.ts` |
| 3 | Update TokenForm for creation | Add a toggle/checkbox for `ultimate_model_enabled` in the token creation form. Default: false. Follow the pill-toggle pattern from ProxySettings. Include in POST body when creating token. | `pkg/ui/frontend/src/components/tokens/TokenForm.tsx` |
| 4 | Add toggle to TokenList rows | For each token row in the list, add a small toggle button (or badge + toggle) that shows current state and allows toggling `ultimate_model_enabled`. Use optimistic UI update pattern. | `pkg/ui/frontend/src/components/tokens/TokenList.tsx` |
| 5 | Add visual badge | Show a distinctive badge/indicator next to tokens that have ultimate model access enabled (e.g., a small "ULTIMATE" badge with a colored pill). | `pkg/ui/frontend/src/components/tokens/TokenList.tsx` |
| 6 | Handle API errors gracefully | If PATCH fails, show error toast/notification and revert the toggle to previous state. | `pkg/ui/frontend/src/components/tokens/TokenList.tsx` |

## Key Files
- `pkg/ui/frontend/src/types.ts` — ApiToken interface
- `pkg/ui/frontend/src/hooks/useTokens.ts` — Token CRUD hook
- `pkg/ui/frontend/src/components/tokens/TokenForm.tsx` — Token creation form
- `pkg/ui/frontend/src/components/tokens/TokenList.tsx` — Token list with actions

## Constraints
- Follow existing UI patterns (pill-toggle from ProxySettings, table layout from TokenList)
- Optimistic UI updates for toggle — don't block UI while waiting for API response
- Handle the case where token has been deleted by another session (404 on PATCH → refresh list)
- Accessible: toggle should have proper aria labels
- No new dependencies required

## Design Notes

### TokenForm Toggle
```
[Token Name: ___________]
[Expires:    ___________]
[⬤ Ultimate Model Access: OFF]  ← pill toggle, default OFF
[Create Token]
```

### TokenList Row (with toggle)
```
| Name       | Created    | Expires | Ultimate | Actions     |
|------------|------------|---------|----------|-------------|
| my-token   | 2026-04-08 | Never   | [⬤ OFF] | [Delete]    |
| admin-key  | 2026-04-01 | Never   | [⬤ ON]  | [Delete]    |
```

The "Ultimate" column contains a pill toggle that can be clicked to toggle the permission. When ON, show a small colored badge like `[ULTIMATE]` next to the toggle.

## Deliverables
- [ ] `ApiToken` type includes `ultimate_model_enabled: boolean`
- [ ] `useTokens` hook has `updateToken()` method
- [ ] Token creation form has ultimate model toggle
- [ ] Token list shows ultimate model status per token
- [ ] Token list has inline toggle for changing permission
- [ ] Error handling for failed toggle (revert + notification)
- [ ] Frontend builds without errors
