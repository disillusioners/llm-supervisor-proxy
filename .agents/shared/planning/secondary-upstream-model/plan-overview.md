# Plan Overview: Secondary Upstream Model

## Objective
Add the ability to configure a "secondary upstream model" per model entry. When the main upstream request fails (or is idle), the retry uses this different upstream provider model instead of retrying with the same one. This is distinct from the existing fallback model (which is a proxy-defined model).

## Scope Assessment
**LARGE** — Spans backend data model, database migration, proxy retry logic, API endpoints, and frontend UI. Touches core race coordinator execution path.

## Context
- Project: llm-supervisor-proxy
- Working Directory: /Users/nguyenminhkha/All/Code/opensource-projects/llm-supervisor-proxy
- Requested by: Leader

## Background: Terminology & Architecture

### Three Distinct Concepts (DO NOT confuse)
| Concept | What it is | Example | When used |
|---------|-----------|---------|-----------|
| **Upstream Model** (`internal_model`) | Provider's actual model name | `glm-5.0` | Every request to this proxy model |
| **Secondary Upstream Model** (`secondary_upstream_model`) ← NEW | Different provider model for retries | `glm-4.7-flash` | When main upstream fails, retry with this |
| **Fallback Model** (`fallback_chain`) | Different proxy model ID | `backup-model` | When all upstream attempts fail, switch to entirely different proxy model |

### Current Retry Flow
```
Request → RaceCoordinator
  ├── Main request (models[0] = primary proxy model ID)
  │   └── executeInternalRequest() → ResolveInternalConfig() → "glm-5.0"
  ├── [On idle] Second request (same models[0])
  │   └── executeInternalRequest() → ResolveInternalConfig() → "glm-5.0"  ← SAME
  └── [On error/fallback] Fallback request (models[1] = fallback proxy model)
      └── executeInternalRequest() → ResolveInternalConfig() → fallback's own model
```

### Desired Flow
```
Request → RaceCoordinator
  ├── Main request → "glm-5.0"
  ├── [On idle] Second request → "glm-4.7-flash"  ← SECONDARY
  └── [On error] Fallback request → fallback's own model (unchanged)
```

## Phase Index

| Phase | Name | Objective | Dependencies | Coupling | Est. Time |
|-------|------|-----------|-------------|----------|-----------|
| 1 | Backend Data Model & Persistence | Add field to ModelConfig, DB migration, query builder, store layer | None | — | 3h |
| 2 | Core Retry Logic | Modify race coordinator/executor to use secondary upstream model on retry | Phase 1 | tight | 4h |
| 3 | Frontend UI | Add secondary upstream model input to ModelForm, TypeScript types | Phase 1 | loose | 2h |
| 4 | Tests & Integration | End-to-end tests, mock tests for retry with secondary model | Phase 2 | tight | 3h |

### Coupling Assessment

| Phase Pair | Coupling | Reasoning |
|-----------|----------|-----------|
| Phase 1 → Phase 2 | **tight** | Phase 2 needs the `SecondaryUpstreamModel` field from ModelConfig + store queries |
| Phase 1 → Phase 3 | **loose** | FE only needs to know the field name and API contract; implementation is independent |
| Phase 2 → Phase 4 | **tight** | Tests verify the retry logic changes from Phase 2 |

**Scheduling Recommendation:**
- Phase 1 must complete first (root dependency)
- Phase 2 and Phase 3 can START in parallel after Phase 1 (loose coupling with each other)
- Phase 4 must wait for Phase 2

## Risks & Mitigations
| Risk | Impact | Mitigation |
|------|--------|------------|
| Breaking existing retry behavior for models without secondary config | high | Field is optional; empty = current behavior (same model retry). Backward compatible by default |
| Race coordinator model assignment logic is subtle | high | Comprehensive tests in Phase 4; careful modification of `spawn()` method |
| Confusion between secondary upstream and fallback model | medium | Clear naming (`secondary_upstream_model`), documentation, and visual separation in UI |
| External upstream models (non-internal) don't have provider model concept | low | Feature only applies to `Internal=true` models; validation in backend |

## Success Criteria
- [ ] User can configure `secondary_upstream_model` per model via UI and API
- [ ] When main upstream fails and secondary is configured, retry uses the secondary model
- [ ] When secondary is NOT configured, retry behavior is unchanged (same model)
- [ ] Fallback model logic is completely unchanged
- [ ] Backward compatible: existing configs work without modification
- [ ] All existing tests pass
- [ ] New tests cover: with secondary, without secondary, fallback unchanged

## Architecture Decisions

### Decision 1: Field Scope — Internal Models Only
**Decision:** `secondary_upstream_model` only applies to models with `internal=true`. External upstream models go through LiteLLM which handles its own routing.

**Rationale:** For external upstream, the proxy just forwards to LiteLLM — changing the model name means changing the LiteLLM routing. For internal upstream, the proxy directly controls which provider model is used.

### Decision 2: Where to Swap the Model
**Decision:** Swap the model at the `ResolveInternalConfig` level, NOT at the coordinator level. Add a parameter to ResolveInternalConfig that says "resolve for secondary" vs "resolve for primary".

**Rationale:** ResolveInternalConfig already handles peak hour substitution, credential resolution, and base URL resolution. The secondary model is conceptually "a different upstream model name with the same credential/provider". This avoids duplicating all the resolution logic.

### Decision 3: Apply to modelTypeSecond Only
**Decision:** Secondary upstream model applies ONLY to `modelTypeSecond` spawns (idle timeout parallel race). On error, fallback goes directly to the fallback proxy model (unchanged).

**Rationale:** The existing flow on idle is to spawn a "second" request with the same model. Now it uses a different model. On error, it spawns fallback which is already a different proxy model entirely. Changing the error flow would conflate secondary upstream with fallback.

## Tracking
- Created: 2026-04-13
- Last Updated: 2026-04-13
- Status: draft
