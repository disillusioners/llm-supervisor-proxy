# Architecture Decisions: Secondary Upstream Model

## Decision 1: Feature Scope — Internal Models Only
**Date:** 2026-04-13  
**Status:** Decided  
**Context:** The proxy has two types of upstream: internal (direct provider calls) and external (via LiteLLM).  
**Decision:** `secondary_upstream_model` only applies to models with `internal=true`.  
**Rationale:** For external upstream, the proxy just forwards to LiteLLM — the proxy doesn't control which provider model is used. For internal upstream, the proxy directly controls the provider model name via `ResolveInternalConfig`.  
**Consequences:** Non-internal models can't use this feature. This is acceptable because non-internal models route through LiteLLM which has its own fallback mechanisms.

## Decision 2: Implementation Approach — Flag in Executor, Not New Interface Method
**Date:** 2026-04-13  
**Status:** Decided  
**Context:** Need to tell `executeInternalRequest` to use a different upstream model for retries. Two approaches: (A) modify ResolveInternalConfig interface, or (B) handle in executor by reading model config directly.  
**Decision:** Approach B — handle in executor. Use `ResolveInternalConfig` for credential/provider/baseURL resolution, then swap the model name using `ModelConfig.SecondaryUpstreamModel`.  
**Rationale:** Avoids changing the `ModelsConfigInterface` which has multiple implementations (JSON, database). Keeps the change localized to `race_executor.go`.  
**Consequences:** Executor needs access to ModelConfig to read SecondaryUpstreamModel. Already available via `cfg.ModelsConfig.GetModel()`.

## Decision 3: Applies to modelTypeSecond Only (Idle Timeout Parallel Race)
**Date:** 2026-04-13  
**Status:** Decided  
**Context:** The coordinator spawns three types: main (initial), second (parallel race on idle), fallback (different proxy model on error).  
**Decision:** Secondary upstream model only applies to `modelTypeSecond` spawns. Main always uses primary. Fallback uses its own model.  
**Rationale:** On idle timeout, the current behavior is to spawn a parallel request with the SAME model. This changes it to use a DIFFERENT upstream model (same credential/provider). On error, fallback already switches to a different proxy model entirely — that's a different level of fallback.  
**Consequences:** If main fails with an error, the secondary upstream model is NOT tried. Only the fallback model is used. Users who want error-time retry with a different upstream model should configure their fallback chain accordingly.

## Decision 4: Peak Hour Does Not Apply to Secondary Model
**Date:** 2026-04-13  
**Status:** Decided  
**Context:** Peak hour substitution currently overrides `InternalModel` with `PeakHourModel` during specific time windows.  
**Decision:** Peak hour substitution does NOT apply to the secondary upstream model. Secondary is always used as-is when configured.  
**Rationale:** Peak hour is about "use a different model during busy times". Secondary is about "use a different model for retries". These are orthogonal concerns. If peak hour is active, the main model changes but the retry target stays the same (secondary).  
**Consequences:** If a user wants different secondary models during peak hours, they'd need a separate feature. This is acceptable for v1.

## Decision 5: UI Placement — Inside Internal Upstream, Before Peak Hour
**Date:** 2026-04-13  
**Status:** Decided  
**Context:** The ModelForm has a nested structure: Base → Internal Upstream → Peak Hour.  
**Decision:** Place secondary upstream model field inside the Internal Upstream section, after "Upstream Model Name" and before the Peak Hour section. Use purple/indigo visual theme.  
**Rationale:** The hierarchy is natural: (1) what model to use → (2) what model to retry with → (3) what model during peak hours. Purple distinguishes from blue (internal section) and amber (peak hour).  
**Consequences:** Users must enable Internal Upstream to see the secondary model field. This is correct since the feature only applies to internal models.
