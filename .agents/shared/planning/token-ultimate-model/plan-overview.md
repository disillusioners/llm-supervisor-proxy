# Plan Overview: Per-Token Ultimate Model Override

## Objective
Add an optional `ultimate_model` field to each token that, when set, overrides the global `UltimateModelConfig.ModelID` for that token's ultimate model requests. When NULL/empty, the global config is used as before. Only `model_id` is per-token — `MaxHash` and `MaxRetries` remain global.

## Scope Assessment
**MEDIUM** — ~12 files touched across 3 layers (DB/Go backend/React frontend), but the change is well-scoped: one new nullable column, one new string field threaded through existing code paths, and one new form input. No new packages, no architectural changes. Estimated 4-6 hours.

## Context
- **Project**: llm-supervisor-proxy
- **Working Directory**: `/Users/nguyenminhkha/All/Code/opensource-projects/llm-supervisor-proxy`
- **Branch**: `feature/token-ultimate-model`
- **Latest migration**: #022 (allowed_models)
- **Pattern precedent**: Migration #020 added `ultimate_model_enabled`, #022 added `allowed_models` — follow the same pattern for #023.

## Phase Index

| Phase | Name | Objective | Dependencies | Coupling | Est. Time |
|-------|------|-----------|-------------|----------|-----------|
| 1 | Data Layer | Migration #023 + AuthToken struct + store queries | None | — | 1.5h |
| 2 | Backend Logic | Proxy handler override + API endpoints + ultimate model handler | Phase 1 | **tight** (same Go types, shared structs) | 2h |
| 3 | Frontend | Types + TokenForm + TokenList UI for ultimate_model field | Phase 2 | **loose** (consumes API contract from Phase 2) | 1.5h |
| 4 | Tests | Unit + integration tests for the new override path | Phases 1-3 | **tight** (tests import code from all phases) | 1.5h |

### Coupling Assessment

| Phase Pair | Coupling | Rationale | Scheduling |
|------------|----------|-----------|------------|
| 1 → 2 | **tight** | Phase 2 imports `AuthToken` struct and store methods from Phase 1; same Go types | Sequential — wait for Phase 1 review |
| 2 → 3 | **loose** | Phase 3 consumes the REST API contract (JSON field `ultimate_model`). Doesn't care about Go implementation | Can pipeline (start Phase 3 once API contract is agreed, before Phase 2 is fully reviewed) |
| 3 → 4 | **tight** | Tests import all code, need all types and components | Sequential after Phase 3 |

**Parallelism opportunity**: Phase 3 (frontend) can start once the API contract is defined in Phase 2, even before Phase 2 is fully merged. The contract is simple: one new nullable string field `ultimate_model` in the token JSON.

## Key Architecture Decision

**How the per-token model_id flows to execution:**

```
Request → authenticate() → AuthToken{UltimateModelID: "gpt-4o"}
  → rc.ultimateModelID = token.UltimateModelID      // Store in requestContext
  → ShouldTrigger/ForceTrigger → result.Triggered
  → ultimateModelID := resolve: rc.ultimateModelID || global.GetModelID()
  → IsModelAllowed(ultimateModelID) check            // Uses resolved model
  → Execute(..., &ultimateModelID)                   // Pass resolved model
     → Execute() uses override if non-nil/non-empty
```

**Decision**: Override is resolved at the **proxy handler level** (in `handler.go`), NOT in the ultimate model handler. The `Execute()` method receives an optional `*string` parameter. This keeps the ultimatemodel package independent of token concepts.

## Risks & Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Access check uses wrong model ID (global vs per-token) | **high** — wrong model checked against allowed_models, could allow/deny incorrectly | Resolve model ID ONCE at line ~487, use same variable for both access check AND Execute() |
| `GetModelID()` called elsewhere for logging (line 471) | **medium** — log shows global model instead of per-token model | Apply same override pattern: `ultimateModelID := h.ultimateHandler.GetModelID(); if rc.ultimateModelID != "" { ultimateModelID = rc.ultimateModelID }` |
| Per-token model not found in database | **medium** — `modelsMgr.GetModel(modelID)` returns nil → error | Execute() already handles this case (returns error). No special handling needed. |
| Per-token model is not in token's own `allowed_models` | **low** — confusing UX: token has ultimate model set but it's not in allowed_models | Frontend can show a warning, but backend should still respect both independently. Not blocking. |
| Migration #023 conflicts with future migrations | **low** | Follow established pattern: sequential numbering, both SQLite + PostgreSQL SQL files |

## Success Criteria
- [ ] Migration #023 adds `ultimate_model` TEXT column (nullable) to `auth_tokens` table
- [ ] `AuthToken` struct has `UltimateModelID string` field, serialized as `ultimate_model`
- [ ] Token CRUD API accepts and returns `ultimate_model` field
- [ ] When token has `ultimate_model` set, ultimate model execution uses it instead of global config
- [ ] When token has `ultimate_model` NULL/empty, behavior is identical to current (global config)
- [ ] Access check (`IsModelAllowed`) uses the resolved per-token model ID
- [ ] Both internal and external ultimate model providers work with per-token override
- [ ] Frontend shows conditional text input when `ultimate_model_enabled` is true
- [ ] All existing tests pass; new tests cover the override path
- [ ] Backward compatible: existing tokens with NULL field work unchanged

## Tracking
- Created: 2026-05-02
- Last Updated: 2026-05-02
- Status: **draft**
