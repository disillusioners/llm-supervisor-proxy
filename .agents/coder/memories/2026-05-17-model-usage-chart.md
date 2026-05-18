# Model Usage Chart Feature — Implementation Experience

## Date: 2026-05-17
## Branch: feature/model-usage-chart
## Commit: 0890b71

## Summary
Added per-model usage tracking and chart visualization alongside existing per-token usage tracking.

## Architecture
- **New table**: `model_hourly_usage` (parallel to `token_hourly_usage`)
- **Recording**: 3 locations in handler.go (ultimate model, streaming race winner, non-streaming race winner)
- **Model usage recording is NOT gated by tokenID** — tracked regardless of authentication (only requires `reqLog.Model != ""`)
- **API**: `GET /fe/api/usage/models` with from/to/view params
- **Frontend**: ModelUsageChart.tsx + UsageTab.tsx with 3-way toggle (By Token / By Model / Table)

## Key Learnings
1. **Parallel backend/frontend works well for CRUD**: Backend and frontend were implemented simultaneously with zero conflicts
2. **Review contradictions**: Reviewer reported conflicting info about tokenID gating (said it was both required and not required for model usage). Fix session confirmed it was NOT gated — reviewer was wrong about the critical issue
3. **Unnecessary API calls**: Initial implementation fetched model data on every render regardless of display mode. Fixed to only fetch when `displayMode === 'by-model-chart'`
4. **Recording locations**: Must track ALL 3 request paths in handler.go: ultimate model path (~line 680), streaming race winner (~line 1164), non-streaming race winner (~line 1387)

## Files Changed (11)
- Migration: `pkg/store/database/migrations/sqlite/025_model_hourly_usage.up.sql`, `pkg/store/database/migrations/postgres/025_model_hourly_usage.up.sql`
- Backend: `pkg/usage/counter.go`, `pkg/proxy/handler.go`, `pkg/ui/handlers_usage.go`, `pkg/ui/server.go`, `pkg/store/migrate.go`
- Frontend: `pkg/ui/frontend/src/types.ts`, `pkg/ui/frontend/src/hooks/useApi.ts`, `pkg/ui/frontend/src/components/usage/ModelUsageChart.tsx`, `pkg/ui/frontend/src/components/usage/UsageTab.tsx`
