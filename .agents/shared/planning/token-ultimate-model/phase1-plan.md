# Phase 1: Data Layer

## Objective
Add the `ultimate_model` column to the database, update the `AuthToken` Go struct, and update all token store queries (INSERT, UPDATE, SELECT) to read/write the new field.

## Coupling
- **Depends on**: None
- **Coupling type**: — (root phase)
- **Shared files with other phases**: `pkg/auth/token.go` (struct used by Phases 2, 4), `pkg/auth/store.go` (queries used by Phase 2)
- **Shared APIs/interfaces**: `AuthToken` struct, `TokenStore` interface
- **Why this coupling**: The `AuthToken` struct is the core data model used everywhere — DB layer, API layer, and proxy handler all depend on it.

## Context
- Latest migration is #022 (`allowed_models`)
- Follow the same pattern: SQLite + PostgreSQL SQL files, registered in `migrate.go`
- `ultimate_model_enabled` was added in migration #020, `allowed_models` in #022

## Tasks

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 1 | Create migration #023 SQL files | Add `ultimate_model TEXT DEFAULT NULL` to `auth_tokens`. Both SQLite and PostgreSQL variants. | `pkg/store/database/migrations/sqlite/023_add_ultimate_model.up.sql`, `pkg/store/database/migrations/postgres/023_add_ultimate_model.up.sql` |
| 2 | Register migration #023 in Go | Add entry `{"023", "023_add_ultimate_model.up"}` to the migrations slice | `pkg/store/database/migrate.go` |
| 3 | Add `UltimateModelID` to `AuthToken` struct | `UltimateModelID string \`json:"ultimate_model"\`` — placed after `UltimateModelEnabled`, before `AllowedModels`. Empty string = use global. | `pkg/auth/token.go` |
| 4 | Update INSERT query | Add `ultimate_model` to INSERT column list and VALUES placeholder. Serialize: empty string → NULL in DB (same pattern as `allowed_models`). | `pkg/auth/store.go` |
| 5 | Update SELECT queries | Add `ultimate_model` to all SELECT queries for `auth_tokens` (ValidateToken, ListTokens, GetTokenByID if exists). Deserialize: NULL → empty string. | `pkg/auth/store.go` |
| 6 | Update UPDATE query | Add `ultimate_model` to UPDATE query used by `UpdateTokenPermission()`. Serialize: empty string → NULL. | `pkg/auth/store.go` |

## Key Files
- `pkg/auth/token.go` — AuthToken struct definition and methods
- `pkg/auth/store.go` — Token store with all DB queries
- `pkg/store/database/migrate.go` — Migration registration
- `pkg/store/database/migrations/sqlite/023_add_ultimate_model.up.sql` — New migration (SQLite)
- `pkg/store/database/migrations/postgres/023_add_ultimate_model.up.sql` — New migration (PostgreSQL)

## Constraints
- Column is nullable TEXT — NULL means "use global config"
- Follow the established serialization pattern: `allowed_models` uses nil/empty → NULL, JSON for non-empty. For `ultimate_model`: empty string → NULL, non-empty → store as-is (it's just a model name string, not JSON).
- Must be backward compatible: existing rows have NULL, which deserializes to empty string, which means "use global"
- Do NOT change any existing columns or their behavior

## Deliverables
- [ ] Migration #023 SQL files for both SQLite and PostgreSQL
- [ ] Migration #023 registered in `migrate.go`
- [ ] `AuthToken.UltimateModelID` field added
- [ ] All token store queries updated (INSERT, SELECT, UPDATE)
- [ ] `go build ./...` passes
- [ ] `go test ./pkg/auth/...` passes
