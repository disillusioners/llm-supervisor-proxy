# Lesson: Peak Hour Fallback Bug

**Date**: 2026-03-30
**Commit**: b268d07
**Category**: Bug Fix — Peak Hour + Fallback Interaction
**Severity**: High (fallback chain broken during peak hours)

## The Bug
When a primary proxy_model had peak hour active and its upstream_model failed, the fallback proxy_model was NOT correctly invoked. The fallback model also had peak hour substitution applied, causing it to use the wrong upstream model.

## Root Cause
`pkg/proxy/race_executor.go` — `executeInternalRequest()` called `ResolveInternalConfig()` which applies peak hour substitution unconditionally for ALL internal models, including fallback models in the fallback chain.

The fallback model was designed to be a "normal" model that works when the peak-hour-overridden upstream fails. But because `ResolveInternalConfig` was applied to the fallback too, it substituted the fallback's model with a peak-hour variant (which doesn't exist or is wrong).

## The Fix
Added a conditional check in `race_executor.go` to skip peak hour substitution for fallback model requests:

```go
if req.modelType == modelTypeFallback {
    modelConfig := cfg.ModelsConfig.GetModel(req.modelID)
    if modelConfig != nil {
        internalModel = modelConfig.InternalModel
    }
}
```

## Key Insight
**Peak hour substitution should only apply to the PRIMARY model choice, not to fallback chain models.** The fallback exists precisely to provide an alternative when the primary (possibly peak-hour-modified) upstream fails. Applying peak hour to the fallback defeats the purpose of having a fallback.

## Testing Approach
Created `test/mock_llm_peak_hour_fallback.go` — a mock integration test that:
1. Sets up 2 internal models: one with peak hour + fallback chain, one without
2. Mock LLM server returns 500 for peak upstream, 200 for fallback upstream
3. Verifies the fallback model's normal (non-peak) upstream is invoked after primary fails

## Prevention
- Any changes to `ResolveInternalConfig` or `race_executor.go` should re-run the peak hour fallback test
- When adding model-level features (like peak hour), consider how they interact with fallback chains
