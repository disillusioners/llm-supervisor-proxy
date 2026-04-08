# Phase 2: Backend — Proxy Handler Gate

## Objective
Add a permission check in the proxy handler's ultimate model trigger flow: if the authenticated token does not have `ultimate_model_enabled`, skip the ultimate model flow entirely and proceed with normal proxy behavior. This is a silent skip — no error returned — because the ultimate model trigger is an internal optimization, not user-requested.

## Coupling
- **Depends on**: Phase 1 (needs `AuthToken.UltimateModelEnabled` field and `ValidateToken()` returning it)
- **Coupling type**: **tight** — reads the `UltimateModelEnabled` field from the `AuthToken` struct that Phase 1 adds. The auth validation flow is in the same file.
- **Shared files with other phases**: `pkg/proxy/handler.go` (not shared with Phase 3)
- **Shared APIs/interfaces**: `AuthToken.UltimateModelEnabled` (consumed from Phase 1)
- **Why this coupling**: The proxy handler directly accesses the `AuthToken` struct returned by `ValidateToken()`. If Phase 1 changes the struct, Phase 2 must match.

## Context
- Auth flow is in `pkg/proxy/handler.go` lines 229-273: `authenticate()` validates token and stores `tokenID`/`tokenName` in the `requestContext` struct
- Ultimate model trigger is in `pkg/proxy/handler.go` lines 380-511: after building messages, `ShouldTrigger()` checks hash cache
- Token info is stored in `requestContext` struct fields (not `context.Context` values)
- Currently NO permission check — purely hash-based decision
- The `requestContext` struct needs a new `ultimateModelEnabled` field

## Tasks

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 1 | Add field to requestContext | Add `ultimateModelEnabled bool` field to the `requestContext` struct in `pkg/proxy/handler.go` | `pkg/proxy/handler.go` |
| 2 | Store permission during auth | In the `authenticate()` method, after `ValidateToken()` succeeds, set `rc.ultimateModelEnabled = token.UltimateModelEnabled` | `pkg/proxy/handler.go` |
| 3 | Add permission gate before trigger | Before the ultimate model trigger section (around line 380), add a check: if `!rc.ultimateModelEnabled`, skip the entire ultimate model flow and proceed directly to normal proxy. This is a simple early-continue/bypass, NOT an error return. | `pkg/proxy/handler.go` |
| 4 | Write tests | Add test cases: (A) Token with permission → ultimate model triggers normally. (B) Token without permission → ultimate model skipped, normal proxy used. (C) Token without permission but hash in cache → still skips, no error. Use existing mock patterns from `pkg/proxy/` tests. | `pkg/proxy/handler_test.go` or appropriate test file |

## Key Files
- `pkg/proxy/handler.go` — Main proxy handler with auth flow + ultimate model trigger

## Constraints
- **Silent skip, not error**: The ultimate model is an internal optimization. When a token lacks permission, the request should proceed via normal proxy as if ultimate model doesn't exist. Do NOT return 403 for implicit triggers.
- If a user explicitly configures a model to use ultimate model routing (not just hash-triggered), that's a different case — for now, the hash-trigger path is the only trigger, and it should silently skip.
- Must not change the `ShouldTrigger()` or `Execute()` signatures — those are in `pkg/ultimatemodel/` and should remain unchanged.
- The gate is purely a check on `requestContext.ultimateModelEnabled` before entering the trigger block.

## Deliverables
- [ ] `requestContext` struct has `ultimateModelEnabled` field
- [ ] `authenticate()` populates the field from validated token
- [ ] Ultimate model trigger section checks permission before proceeding
- [ ] Tokens without permission fall through to normal proxy (no error, no log spam)
- [ ] Tests covering: permission granted → trigger, permission denied → skip, permission denied + hash cached → skip
