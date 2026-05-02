# Phase 3: Frontend

## Objective
Add `ultimate_model` to the frontend types, TokenForm (create/edit), and TokenList (inline editing) components. Show the field conditionally when `ultimate_model_enabled` is true.

## Coupling
- **Depends on**: Phase 2 (API contract: `ultimate_model` field in token JSON)
- **Coupling type**: **loose** — consumes the REST API contract. Doesn't import Go code.
- **Shared files with other phases**: None (separate React/TypeScript codebase)
- **Shared APIs/interfaces**: REST API JSON contract (`/fe/api/tokens` endpoints)
- **Why this coupling**: Frontend only needs the API contract to work. Can start before Phase 2 is fully merged.

## Context
- Phase 2 delivered: Token API returns `ultimate_model` field (nullable string), accepts it in create/update
- API contract:
  - `GET /fe/api/tokens` → each token has `ultimate_model: string | null`
  - `POST /fe/api/tokens` → body can include `ultimate_model: string | null`
  - `PATCH /fe/api/tokens/:id` → body can include `ultimate_model: string | null`
  - `null` / absent = use global config, `""` (empty string) = clear override, `"gpt-4o"` = use this model

## Tasks

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 1 | Add `ultimate_model` to `ApiToken` interface | `ultimate_model: string \| null;` in the `ApiToken` interface. Place it after `ultimate_model_enabled`. | `pkg/ui/frontend/src/types.ts` |
| 2 | Update `TokenForm` to include ultimate model input | Add a text input for `ultimate_model` that appears **conditionally** when `ultimate_model_enabled` is true. Label: "Ultimate Model Override" with help text "Override the global ultimate model for this token. Leave empty to use the global default." Placeholder: e.g., "gpt-4o". Use the same styling as other text inputs in the form. | `pkg/ui/frontend/src/components/tokens/TokenForm.tsx` |
| 3 | Update `onSubmit` signature in `TokenForm` | Add `ultimateModel?: string \| null` parameter to the `onSubmit` callback. Pass the input value (or null if empty). | `pkg/ui/frontend/src/components/tokens/TokenForm.tsx` |
| 4 | Update `TokenList` inline editing | Add an inline display/edit for `ultimate_model` on each token row. Show the model name as a badge/pill when set, or "Global default" when null. Allow inline editing (similar to how `allowed_models` is edited). Show only when `ultimate_model_enabled` is true. | `pkg/ui/frontend/src/components/tokens/TokenList.tsx` |
| 5 | Update `useApi` hook — `createToken` | Add `ultimateModel` parameter to `createToken()`. Include `ultimate_model` in the request body. Send `null` if empty/undefined. | `pkg/ui/frontend/src/hooks/useApi.ts` |
| 6 | Update `useApi` hook — `updateTokenPermission` | Add `ultimateModel` parameter to `updateTokenPermission()`. Include `ultimate_model` in the PATCH body. | `pkg/ui/frontend/src/hooks/useApi.ts` |
| 7 | Wire everything together | Ensure TokenForm passes `ultimate_model` through to `useApi` calls. Ensure TokenList passes it on save. Handle optimistic updates for inline editing. | `TokenForm.tsx`, `TokenList.tsx`, `useApi.ts` |

## Key Files
- `pkg/ui/frontend/src/types.ts` — TypeScript interfaces
- `pkg/ui/frontend/src/components/tokens/TokenForm.tsx` — Create/edit token dialog
- `pkg/ui/frontend/src/components/tokens/TokenList.tsx` — Token table with inline editing
- `pkg/ui/frontend/src/hooks/useApi.ts` — API hooks for CRUD operations

## Constraints
- Field is **conditional**: only show when `ultimate_model_enabled` is true
- Help text must clearly indicate: "Leave empty to use global default"
- The field accepts a model name string (free text, not a dropdown) — different from `allowed_models` which is a multi-select
- Follow existing UI patterns: same styling as other text inputs, same inline edit pattern as `allowed_models`
- Handle null vs empty string: in the UI, empty input = null (use global). Display "Global default" when null.

## Deliverables
- [ ] `ApiToken` type updated with `ultimate_model` field
- [ ] TokenForm has conditional ultimate model text input
- [ ] TokenList shows/edits ultimate model inline
- [ ] useApi hooks pass `ultimate_model` in create/update calls
- [ ] `npm run build` passes
- [ ] Manual browser verification: create token with override, edit, toggle, verify display
