# Lesson: Token Allowed Models Integration Testing

**Date**: 2026-05-01
**Feature**: `feature/token-allowed-models`

## Key Findings

### 1. Empty Slice vs Nil Distinction is Critical
The `allowed_models` field has three distinct states:
- `nil` (NULL in DB) → all models allowed
- `[]` (empty array) → no models allowed  
- `["gpt-4"]` → only listed models allowed

The original code conflated `nil` and `[]`, causing a security issue where tokens meant to deny all models would actually allow all.

### 2. Migration Registration Must Be Manual
Adding migration SQL files isn't enough — `migrate.go` must explicitly register each migration in the migrations slice. Easy to forget.

### 3. Store Serialization Needs Special Handling
When serializing `[]string` to JSON for DB storage:
- `nil` → `NULL`
- `[]` → `'[]'`
Must handle both correctly to preserve semantics.

## Bugs Found During Testing
1. `IsModelAllowed()`: empty slice returned true (security bug)
2. `CreateToken()`: empty slice serialized as NULL (data integrity bug)
3. `migrate.go`: missing migration 022 registration (startup bug)

## Test Coverage
23 integration tests covering:
- CRUD operations with allowed_models
- Handler 403 enforcement
- Edge cases (nil, empty, case sensitivity)
- API endpoints (create, list, patch)
- DB serialization/deserialization
