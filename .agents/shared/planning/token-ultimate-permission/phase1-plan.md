# Phase 1: Backend — DB Migration + Auth Struct + Token API

## Objective
Add the `ultimate_model_enabled` boolean column to the `auth_tokens` table, update the Go `AuthToken` struct and all auth store queries to include it, and create a new PATCH endpoint for toggling the permission on existing tokens.

## Coupling
- **Depends on**: None (root phase)
- **Coupling type**: —
- **Shared files with other phases**: `pkg/auth/token.go` (struct shared with Phase 2), `pkg/auth/store.go` (interface shared with Phase 2)
- **Shared APIs/interfaces**: `AuthToken.UltimateModelEnabled` field (consumed by Phase 2 proxy handler, Phase 3 frontend)
- **Why this coupling**: Phase 1 defines the data contract (struct field + API response shape) that both downstream phases depend on.

## Context
- The `auth_tokens` table was created in migration 004 with 6 columns
- All `auth_tokens` queries are **hand-written** in `pkg/auth/store.go` (NOT sqlc)
- Token API handlers are in `pkg/ui/server.go` (lines 843-964)
- No PATCH/PUT endpoint exists for tokens today
- Migrations are per-dialect: `migrations/sqlite/` and `migrations/postgres/`
- Migrations are registered in `migrate.go` as `migrations` slice

## Tasks

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 1 | Create migration 020 | Add `ultimate_model_enabled BOOLEAN NOT NULL DEFAULT FALSE` column to `auth_tokens`. Two files: `migrations/sqlite/020_add_ultimate_model_enabled.up.sql` and `migrations/postgres/020_add_ultimate_model_enabled.up.sql` | `migrations/sqlite/020_*.sql`, `migrations/postgres/020_*.sql` |
| 2 | Register migration | Add migration 020 to the `migrations` slice in `migrate.go` | `migrate.go` |
| 3 | Update AuthToken struct | Add `UltimateModelEnabled bool` field to `AuthToken` in `pkg/auth/token.go` | `pkg/auth/token.go` |
| 4 | Update TokenStore queries | Update all 4 existing queries in `pkg/auth/store.go` to include `ultimate_model_enabled` in SELECT and INSERT. Update dialect-aware SQL for both SQLite and PostgreSQL. | `pkg/auth/store.go` |
| 5 | Add UpdateTokenPermission method | New method on `TokenStoreInterface`: `UpdateTokenPermission(ctx context.Context, id string, ultimateModelEnabled bool) error`. Add to interface and implement in store. SQL: `UPDATE auth_tokens SET ultimate_model_enabled = ? WHERE id = ?` | `pkg/auth/store.go` |
| 6 | Update CreateToken API handler | Accept `ultimate_model_enabled` field in POST `/fe/api/tokens` request body. Pass to `CreateToken()` store method. Update `CreateToken` store method signature to accept the new field. | `pkg/ui/server.go`, `pkg/auth/store.go` |
| 7 | Add PATCH token endpoint | New handler `updateTokenPermission` on PATCH `/fe/api/tokens/{id}`. Accept JSON body `{"ultimate_model_enabled": true/false}`. Validate token exists, call `UpdateTokenPermission()`. Wire route in `setupRoutes`. | `pkg/ui/server.go` |
| 8 | Update listTokens response | Ensure GET `/fe/api/tokens` returns `ultimate_model_enabled` in each token JSON object. | `pkg/ui/server.go` |
| 9 | Write tests | Test migration applies cleanly. Test Create with field. Test PATCH toggle. Test List returns field. Test Validate returns field. | `pkg/auth/store_test.go`, `pkg/ui/server_test.go` (or appropriate test file) |

## Key Files
- `pkg/auth/token.go` — AuthToken struct definition
- `pkg/auth/store.go` — TokenStoreInterface + store implementation with all SQL queries
- `pkg/ui/server.go` — HTTP handlers for token CRUD (lines 843-964)
- `migrate.go` — Migration registration
- `migrations/sqlite/020_add_ultimate_model_enabled.up.sql` — New SQLite migration
- `migrations/postgres/020_add_ultimate_model_enabled.up.sql` — New PostgreSQL migration

## Constraints
- Maintain backward compatibility: all existing API responses gain a new field but don't break
- SQL must work for both SQLite and PostgreSQL (dialect-aware parameter placeholders)
- `DEFAULT FALSE` ensures existing tokens are locked down by default (security-by-default)
- `ValidateToken()` must return the new field so the proxy handler can check it (Phase 2 dependency)

## Deliverables
- [ ] Migration 020 files for both dialects
- [ ] Migration registered in migrate.go
- [ ] `AuthToken` struct updated with `UltimateModelEnabled bool`
- [ ] All store queries updated (Create, Validate, List, GetByID)
- [ ] New `UpdateTokenPermission()` method on interface + implementation
- [ ] `CreateToken` accepts and persists `ultimate_model_enabled`
- [ ] PATCH `/fe/api/tokens/{id}` endpoint functional
- [ ] GET `/fe/api/tokens` returns new field
- [ ] Tests passing
