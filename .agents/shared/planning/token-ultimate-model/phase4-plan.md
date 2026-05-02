# Phase 4: Tests

## Objective
Write comprehensive tests for the per-token ultimate model override: unit tests for the override resolution logic, store-level tests, and handler-level tests covering the execution path.

## Coupling
- **Depends on**: Phases 1, 2, 3 (imports all modified code)
- **Coupling type**: **tight** — tests import all modified structs and functions
- **Shared files with other phases**: May need to update existing test mocks if `Execute()` signature changed
- **Shared APIs/interfaces**: Tests exercise the full stack from store → handler → Execute()
- **Why this coupling**: Tests by nature depend on all implementation code

## Context
- Phase 1: `AuthToken.UltimateModelID` field, migration #023, updated store queries
- Phase 2: `Execute()` signature change, `requestContext.ultimateModelID`, API endpoints
- Phase 3: Frontend changes (not directly tested in Go, but API contract is validated)

## Tasks

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 1 | Update existing test mocks for `Execute()` signature | If any test mocks call `Execute()`, add the new `*string` parameter (pass `nil`). Verify all existing tests still pass. | Various test files in `pkg/proxy/`, `pkg/ultimatemodel/` |
| 2 | Store-level test: read/write `ultimate_model` | Test that `CreateToken` stores `ultimate_model`, `ValidateToken` reads it back, `UpdateTokenPermission` updates it. Test NULL → empty string, non-NULL → value, update to NULL, update to new value. | `pkg/auth/store_test.go` (or equivalent) |
| 3 | Handler test: override resolution in proxy | Test that when `rc.ultimateModelID` is set, the resolved `ultimateModelID` variable equals the per-token value (not global). When empty, it equals global. Cover: both paths, empty string, non-empty string. | `pkg/proxy/handler_test.go` (or equivalent) |
| 4 | Execute() test: parameter override | Test that `Execute()` uses the override parameter when provided (non-nil, non-empty). Test that it falls back to global config when nil or empty string. | `pkg/ultimatemodel/handler_test.go` (or equivalent) |
| 5 | API endpoint test: create with `ultimate_model` | Test `POST /fe/api/tokens` with `ultimate_model` in body. Verify response includes it. Test without the field (backward compat). Test with null, empty string, and a model name. | `pkg/ui/handlers_test.go` (or equivalent) |
| 6 | API endpoint test: update `ultimate_model` | Test `PATCH /fe/api/tokens/:id` with `ultimate_model` in body. Verify update. Test clearing (empty string → NULL in DB). Test without the field (no change). | `pkg/ui/handlers_test.go` (or equivalent) |
| 7 | Integration test: full override flow | Test the full path: create token with `ultimate_model="gpt-4o"` → make request → ultimate model triggered → verify `Execute()` receives `"gpt-4o"` (not global). Also test with `ultimate_model=""` → global used. | New test file or extend existing |
| 8 | Edge case: per-token model not in allowed_models | Test that when `ultimate_model` is set but the model is NOT in `allowed_models`, the access check correctly denies (403). This validates the override + allowed_models interaction. | `pkg/proxy/handler_test.go` |

## Key Files
- `pkg/auth/store_test.go` — Store-level tests
- `pkg/ultimatemodel/handler_test.go` — Execute() parameter tests
- `pkg/proxy/handler_test.go` — Override resolution tests
- `pkg/ui/handlers_test.go` — API endpoint tests

## Constraints
- Follow existing test patterns in the project (mock stores, test helpers)
- Existing tests MUST continue to pass — the `Execute()` signature change may require updating mocks
- Test both SQLite and PostgreSQL paths if the project has dual-DB support in tests
- Mock the `modelsMgr.GetModel()` to return valid config for the per-token model name

## Deliverables
- [ ] All existing tests pass with updated mocks
- [ ] New store-level tests for `ultimate_model` column
- [ ] New handler-level tests for override resolution
- [ ] New Execute() tests for parameter override
- [ ] New API endpoint tests for create/update with `ultimate_model`
- [ ] Integration test for full override flow
- [ ] Edge case test for per-token model + allowed_models interaction
- [ ] `go test ./...` all pass
