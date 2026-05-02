# Phase 2: Backend Logic

## Objective
Thread the per-token `ultimate_model` override through the proxy handler and ultimate model execution path. Update the token CRUD API to accept and return the new field.

## Coupling
- **Depends on**: Phase 1 (needs `AuthToken.UltimateModelID` field and updated store queries)
- **Coupling type**: **tight** — imports the `AuthToken` struct and calls store methods from Phase 1
- **Shared files with other phases**: `pkg/proxy/handler.go` (also touched by tests in Phase 4), `pkg/ui/server.go` (also used by Phase 3 frontend)
- **Shared APIs/interfaces**: `Execute()` signature change affects tests, Token API contract affects frontend
- **Why this coupling**: Same Go module, direct struct and function imports

## Context
- Phase 1 delivered: `AuthToken.UltimateModelID` field, updated store queries, migration #023
- **Key architectural insight**: The override is resolved at the **proxy handler level** (`handler.go`), NOT inside the `ultimatemodel` package. The `Execute()` method receives an optional `*string` parameter.

## Tasks

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 1 | Add `ultimateModelID` to `requestContext` | Add `ultimateModelID string` field to the `requestContext` struct. Set it during auth: `rc.ultimateModelID = token.UltimateModelID` | `pkg/proxy/handler_helpers.go` |
| 2 | Set `rc.ultimateModelID` during auth | In `HandleChatCompletions()`, after `rc.ultimateModelEnabled = token.UltimateModelEnabled`, also set `rc.ultimateModelID = token.UltimateModelID` | `pkg/proxy/handler.go` (~line 390) |
| 3 | Add `tokenModelID` parameter to `Execute()` | Change signature: `Execute(..., headersSent *bool, tokenModelID *string) (*store.Usage, error)`. In the body, after `modelID := cfg.UltimateModel.ModelID`, add override: `if tokenModelID != nil && *tokenModelID != "" { modelID = *tokenModelID }` | `pkg/ultimatemodel/handler.go` |
| 4 | Resolve per-token model at call site (~line 487) | Replace `ultimateModelID := h.ultimateHandler.GetModelID()` with: resolve once → `ultimateModelID := h.ultimateHandler.GetModelID(); if rc.ultimateModelID != "" { ultimateModelID = rc.ultimateModelID }`. This same variable is used for: logging, access check, event publish, and Execute call. | `pkg/proxy/handler.go` (~line 487) |
| 5 | Pass override to `Execute()` call | Update the `Execute()` call at ~line 543 to pass `&ultimateModelID` (the resolved local variable, not `rc.ultimateModelID`) | `pkg/proxy/handler.go` (~line 543) |
| 6 | Fix logging at ~line 471 (retry exhausted) | Where `GetModelID()` is called for logging, apply the same override pattern: resolve `ultimateModelID` using `rc.ultimateModelID` if set. | `pkg/proxy/handler.go` (~line 471) |
| 7 | Add `UltimateModel` to token API request/response structs | Add `UltimateModel *string \`json:"ultimate_model,omitempty"\`` to `CreateTokenRequest`, `UpdateTokenPermissionRequest`, and `TokenResponse`. Use `*string` to distinguish "not provided" (nil) from "explicitly cleared" (empty string). | `pkg/ui/server.go` |
| 8 | Wire API fields to store calls | In `createToken()`: pass `UltimateModel` from request to store. In `updateTokenPermission()`: if `UltimateModel` is non-nil in request, update it (nil = keep existing, empty string = clear to NULL). In token list/detail responses: populate from `AuthToken.UltimateModelID`. | `pkg/ui/server.go` |
| 9 | Verify `ShouldTrigger()` still works correctly | `ShouldTrigger()` checks `cfg.UltimateModel.ModelID == ""` to short-circuit. This should remain global-only (if no global model is configured, ultimate model is disabled entirely — per-token override is irrelevant). No change needed. Verify this is correct. | `pkg/ultimatemodel/handler.go` (~line 72) |

## Key Files
- `pkg/proxy/handler.go` — Main proxy handler: auth block + ultimate model trigger/execute (~line 340-569)
- `pkg/proxy/handler_helpers.go` — `requestContext` struct definition
- `pkg/ultimatemodel/handler.go` — `Execute()`, `GetModelID()`, `ShouldTrigger()` methods
- `pkg/ui/server.go` — Token CRUD API handlers + request/response structs

## Constraints
- Only `model_id` is per-token. `MaxHash` and `MaxRetries` remain global-only.
- `ShouldTrigger()` and `ForceTrigger()` do NOT need per-token logic — they only check if the feature is configured and compute hashes. The model ID resolution happens after trigger, at execution time.
- `GetModelID()` should remain unchanged (returns global config). The override is applied at the call site.
- The `Execute()` parameter should be `*string` (pointer) to clearly indicate optional.
- API `UltimateModel` field should be `*string` in Go to handle the three states: not provided (nil = keep existing on update), provided empty ("" = clear override, use global), provided non-empty (set override).
- Both internal and external ultimate model paths are covered because `modelID` is resolved before the routing decision in `Execute()`.

## Deliverables
- [ ] `requestContext.ultimateModelID` field added and set during auth
- [ ] `Execute()` accepts optional `*string` parameter for model override
- [ ] Override resolved at call site (line ~487), used consistently for logging + access check + execution
- [ ] Token API accepts `ultimate_model` in create/update, returns it in responses
- [ ] `go build ./...` passes
- [ ] Existing tests pass (may need signature updates for `Execute()` calls in test mocks)
