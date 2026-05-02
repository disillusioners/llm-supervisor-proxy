# Per-Token Ultimate Model Override — Phase 1 + 2 Implementation

## What was built
Added per-token `ultimate_model` override to allow different API tokens to use different ultimate models instead of the global default.

## Phase 1 (Data Layer) — commit e6b289e
- Migration #023: `ultimate_model TEXT DEFAULT NULL` on `auth_tokens` (SQLite + PostgreSQL)
- `AuthToken.UltimateModelID string` field (empty = use global)
- Store queries: INSERT/SELECT/UPDATE all handle new column with COALESCE for NULL→""
- `TokenStoreInterface.UpdateTokenPermission` signature updated (cascaded to all mocks)

## Phase 2 (Backend Logic) — same commit
- `requestContext.ultimateModelID` field, populated from token during auth
- Override resolved at TWO call sites in handler.go: retry-exhausted branch (~L471) and main path (~L487)
- `Execute()` gets `tokenModelID *string` parameter for model override
- Token API: three-state handling (*string: nil=keep, ""=clear, "value"=set)
- Logging added when per-token override is active

## Key Design Decisions
- Override resolved at proxy handler level, NOT in ultimatemodel package (separation of concerns)
- `Execute()` parameter is `*string` (nil vs empty distinction)
- `GetModelID()` unchanged — returns global config, override applied at call site
- `ShouldTrigger()` unchanged — global-only check

## Review Notes
- 18 files changed, 218 insertions, 116 deletions
- All tests pass except pre-existing flaky `TestIdleTermination_Triggered`
- No duplicate fields in any struct
