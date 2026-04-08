# Phase 1: Token Ultimate Model Permission — Backend Foundation

## Date: 2026-04-08
## Branch: feature/token-ultimate-permission
## Commit: 73d8242

## What was done
- Migration 020: `ultimate_model_enabled BOOLEAN NOT NULL DEFAULT FALSE` on `auth_tokens` (both SQLite + PostgreSQL)
- `AuthToken` struct: added `UltimateModelEnabled bool` with `json:"ultimate_model_enabled"`
- Updated ALL 5 store queries: ValidateToken, CreateToken, ListTokens, GetTokenByID, DeleteToken (no change needed for delete)
- New `UpdateTokenPermission()` method on `TokenStoreInterface` + implementation
- CreateToken accepts `ultimate_model_enabled` in POST body
- New PATCH `/fe/api/tokens/{id}` endpoint
- GET `/fe/api/tokens` returns new field
- 7 new tests added

## Key Learnings
1. **5 queries not 4**: Reviewer caught that GetTokenByID was missed in the plan. Always enumerate ALL queries touching a table.
2. **Dialect-aware SQL**: SQLite uses `?`, PostgreSQL uses `$1,$2` etc. Both must be updated for every query.
3. **UPDATE returning 0 rows → 404**: No need to add GetTokenByID to interface for PATCH validation. Use RowsAffected() check.
4. **Migration files location**: `pkg/store/database/migrations/sqlite/` and `pkg/store/database/migrations/postgres/` (not root-level `migrations/`)
5. **Migrations registered in**: `pkg/store/database/migrate.go` (not root `migrate.go`)

## Files Changed (9 files, +367/-72)
- `pkg/store/database/migrations/sqlite/020_add_ultimate_model_enabled.up.sql` (new)
- `pkg/store/database/migrations/postgres/020_add_ultimate_model_enabled.up.sql` (new)
- `pkg/store/database/migrate.go`
- `pkg/auth/token.go`
- `pkg/auth/store.go`
- `pkg/auth/store_test.go`
- `pkg/ui/server.go`
- `pkg/proxy/authenticate_test.go`
- `pkg/proxy/handler_integration_test.go`
