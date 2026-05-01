# Token Allowed Models Feature (2026-04-30)

**Branch:** `feature/token-allowed-models`
**Commits:** 6 (5 feature + 1 fix)

## Key Implementation Details:
1. **Migration 022**: `allowed_models TEXT DEFAULT NULL` on `auth_tokens` — stores JSON array like `["gpt-4","claude-3"]`. NULL = all models allowed.
2. **Fail-closed on malformed JSON**: `ScanAllowedModels()` returns `[]string{}` (deny all) + logs security warning when JSON is malformed. Critical security decision.
3. **[]byte handling**: DB drivers (lib/pq) return `[]byte` for TEXT columns — type switch handles both `string` and `[]byte`.
4. **Dual enforcement**: `allowed_models` check in BOTH the normal request path AND the ultimate model path (X-Force-Ultimate-Model header bypass was fixed).
5. **`ultimate_model_enabled` preserved**: Works independently alongside `allowed_models`.
6. **TokenList stale state fix**: Use `isTokenEnabled(token)` not `token.ultimate_model_enabled` when saving inline edits.
7. **ModelMultiSelect**: Select All respects search filter — only toggles visible items.
8. **Integration test schema**: Changed `DEFAULT '[]'` to `DEFAULT NULL` to match migration.

## Architecture:
- `pkg/auth/token.go`: `AllowedModels []string`, `ScanAllowedModels()`, `IsModelAllowed()`
- `pkg/auth/store.go`: INSERT/SELECT/UPDATE with JSON serialization
- `pkg/ui/server.go`: API request/response structs, create/PATCH endpoints
- `pkg/proxy/handler.go`: 403 enforcement in normal + ultimate model paths
- `pkg/ui/frontend/src/components/tokens/ModelMultiSelect.tsx`: New multi-select component
