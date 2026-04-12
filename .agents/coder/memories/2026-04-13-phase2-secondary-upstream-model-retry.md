# Phase 2: Core Retry Logic for Secondary Upstream Model

## Date: 2026-04-13
## Commit: 829c98c

## What Was Implemented
- Added `useSecondaryUpstream` flag to `upstreamRequest` struct with thread-safe getter/setter
- Set flag in `spawn()` for `modelTypeSecond` only (in race_coordinator.go)
- Modified `executeInternalRequest()` to check flag and use `SecondaryUpstreamModel` when configured
- Added `[SECONDARY]` logging when secondary model is used
- Published `race_secondary_model_used` event for frontend tracking

## Key Architecture Decisions
1. **Alternative approach chosen**: Did NOT change `ResolveInternalConfig` interface. Instead, call it normally to get provider/apiKey/baseURL, then just swap the model name.
2. **Flag-based flow**: Flag set at spawn time, checked at execution time — clean separation
3. **Peak hour excluded**: Peak hour does NOT apply to secondary model (user explicitly chose it for retry)
4. **Fallback untouched**: `modelTypeFallback` path completely unchanged

## Files Changed (6 files, 106 insertions)
- `pkg/proxy/race_request.go` — flag + getter/setter
- `pkg/proxy/race_coordinator.go` — set flag for modelTypeSecond
- `pkg/proxy/race_executor.go` — secondary model swap + logging + event

## Review Results
All 10 checks passed. No issues found.

## Pattern
This is a good pattern for any feature that needs to pass information from spawn-time to execution-time in the race executor: add a flag to `upstreamRequest` with mutex-protected accessors.
