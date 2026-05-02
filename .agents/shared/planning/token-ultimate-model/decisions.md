# Architecture Decisions: Per-Token Ultimate Model Override

## Decision 1: Where to resolve the per-token override

**Decision**: Resolve at the **proxy handler level** (`handler.go`), not inside `ultimatemodel/handler.go`.

**Rationale**: 
- The ultimatemodel package has no concept of tokens — it only knows about model configs and hash caching
- Token info is only available in the proxy handler (via `authenticate()`)
- Keeping the override resolution in `handler.go` means the ultimatemodel package stays independent
- The `Execute()` method simply receives an optional model ID — it doesn't know or care where it came from

**Alternative considered**: Add token awareness to the ultimatemodel package. Rejected because it would couple the ultimatemodel package to the auth system, violating separation of concerns.

## Decision 2: `Execute()` parameter type

**Decision**: `tokenModelID *string` (pointer to string)

**Rationale**:
- `nil` = no override provided (use global config) — distinguishes from empty string
- `*string` clearly signals "optional" in Go
- Matches the existing Go pattern for optional parameters

**Alternative considered**: `tokenModelID string` (empty = use global). Rejected because it's ambiguous — does empty mean "no override" or "explicitly cleared"?

## Decision 3: Only `model_id` is per-token

**Decision**: `MaxHash` and `MaxRetries` remain global-only.

**Rationale**: Per the requirements, only the model selection needs per-token granularity. Hash buffer size and retry limits are infrastructure concerns, not per-user preferences.

## Decision 4: API field uses `*string` for three-state handling

**Decision**: API structs use `UltimateModel *string` with JSON `"ultimate_model,omitempty"`

**Rationale**:
- `nil` (omitted from JSON) = no change on update, not provided on create = use global
- `""` (empty string) = explicitly clear override, use global
- `"gpt-4o"` = set override to this model
- This three-state handling matches the `allowed_models` pattern where nil = keep existing

## Decision 5: Frontend uses conditional text input, not model selector

**Decision**: Free-text input for the model name, not a dropdown/selector from available models.

**Rationale**:
- The model might not exist in the proxy's model list yet (configured later)
- Simpler implementation — no need to fetch and filter model list
- Matches the admin-user workflow where they know the model name
- The backend validates the model exists at execution time (returns error if not found)

**Future consideration**: Could add autocomplete/suggestions from the model list as a UX enhancement, but not required for MVP.
