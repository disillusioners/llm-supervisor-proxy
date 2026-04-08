# Plan Overview: Token Permission for Ultimate Model Access

## Objective
Add a per-token `ultimate_model_enabled` boolean that gates access to the ultimate model feature. Tokens without this permission silently skip the ultimate model flow (no error for implicit triggers) and return a clear 403 error for explicit triggers.

## Scope Assessment
**MEDIUM** — Changes span 3 layers (DB/auth, proxy handler, frontend) but the change is conceptually simple (add one boolean field, thread it through existing flows, add one PATCH endpoint). Estimated 1-2 days.

## Context
- **Project**: llm-supervisor-proxy
- **Working Directory**: `/Users/nguyenminhkha/All/Code/opensource-projects/llm-supervisor-proxy`
- **Branch**: `feature/token-ultimate-permission` (new)
- **Base branch**: `main`

### Architecture Notes
- `auth_tokens` queries are **hand-written** in `pkg/auth/store.go` (NOT sqlc-managed)
- No PATCH/PUT token endpoint exists today — only GET, POST, DELETE
- Token info is stored in `requestContext` struct fields (`tokenID`, `tokenName`), not `context.Context` values
- Ultimate model trigger is purely hash-based (`ShouldTrigger()`) with NO token permission check
- Frontend uses **pill-shaped slider toggle** pattern for boolean settings (see ProxySettings)
- Migrations are per-dialect: `migrations/sqlite/` and `migrations/postgres/`, registered in `migrate.go`

## Phase Index

| Phase | Name | Objective | Dependencies | Coupling | Est. Time |
|-------|------|-----------|-------------|----------|-----------|
| 1 | Backend: DB + Auth + API | Add column, update struct, add PATCH endpoint, update queries | None | — | 3-4h |
| 2 | Backend: Proxy Handler Gate | Check token permission before ultimate model trigger | Phase 1 | **tight** | 1-2h |
| 3 | Frontend: Token UI | Display + toggle ultimate_model_enabled in token management UI | Phase 1 | **loose** | 2-3h |

### Coupling Assessment

| From → To | Coupling | Reason |
|-----------|----------|--------|
| Phase 1 → Phase 2 | **tight** | Phase 2 reads `UltimateModelEnabled` from `AuthToken` struct + `requestContext` that Phase 1 adds. Same struct, same auth flow. |
| Phase 1 → Phase 3 | **loose** | Phase 3 calls the PATCH endpoint and reads the new field from API responses. Only depends on the API contract, not internal implementation. |

### Scheduling Recommendation

```
Phase 1 (DB + Auth + API)
    ├──→ Phase 2 (Proxy Gate)     [sequential — tight coupling]
    └──→ Phase 3 (Frontend UI)    [can start after Phase 1 completes, parallel with Phase 2 review]
```

Phases 2 and 3 **can run in parallel** since they touch completely different files and only depend on Phase 1's API contract.

## Risks & Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Existing tokens lose ultimate model access after migration | **high** — breaking existing behavior | Migration sets `DEFAULT FALSE`; document that admins must explicitly enable tokens post-migration. This is intentional — security-by-default. |
| Proxy handler auth flow refactor | **medium** — `requestContext` needs new field | Small change; field added alongside existing `tokenID`/`tokenName` fields |
| No existing PATCH pattern for tokens | **low** — new code, not refactoring | Follow existing handler patterns in `pkg/ui/server.go` |
| Frontend toggle needs real-time update | **low** — standard pattern | Follow pill-toggle pattern from ProxySettings; optimistic UI update |

## Success Criteria
- [ ] Migration 020 adds `ultimate_model_enabled BOOLEAN DEFAULT FALSE` to `auth_tokens` (both SQLite + PostgreSQL)
- [ ] `AuthToken` struct includes `UltimateModelEnabled bool`
- [ ] `TokenStoreInterface.ValidateToken()` returns the new field
- [ ] `TokenStoreInterface.CreateToken()` accepts and stores the new field
- [ ] `TokenStoreInterface.ListTokens()` returns the new field
- [ ] New `UpdateTokenPermission()` method on `TokenStoreInterface`
- [ ] New PATCH `/fe/api/tokens/{id}` endpoint toggles `ultimate_model_enabled`
- [ ] Proxy handler checks token permission before ultimate model trigger
- [ ] Tokens without permission skip ultimate model flow silently (no error for implicit triggers)
- [ ] Frontend shows ultimate model badge in token list
- [ ] Frontend has toggle for ultimate model in token creation form
- [ ] Frontend has toggle button in token list row to enable/disable
- [ ] All existing tests pass
- [ ] New tests for the permission check

## Tracking
- Created: 2026-04-08
- Last Updated: 2026-04-08
- Status: draft
