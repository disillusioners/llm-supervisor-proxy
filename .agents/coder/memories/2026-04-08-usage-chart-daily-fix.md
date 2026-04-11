# Usage Chart + Daily Aggregation Fix

**Date:** 2026-04-08
**Commit:** 5d00972

## What Was Done

### Bug Fix: Backend Daily Aggregation
- `pkg/ui/handlers_usage.go`: The `view` query parameter was read but never used for SQL aggregation
- Fixed by adding conditional SQL: when `view=daily`, uses `SUBSTR(u.hour_bucket, 1, 10)` to extract day, GROUP BY day, SUM all numeric fields
- When `view=hourly` (default), behavior unchanged
- Both PostgreSQL and SQLite dialects handled across all 4 query paths (with/without token_id × dialect)

### New Feature: Usage Chart
- Installed `chart.js` v4.5.1 (canvas-based, no React dependency needed)
- Created `UsageChart.tsx`: multi-line chart with per-token colored lines + bold dashed white Total line
- Chart uses Preact `useRef` + `useEffect` pattern with proper Chart.js cleanup on unmount
- 8-color palette for tokens, dark theme compatible

### Frontend Updates
- `UsageTab.tsx`: Added `displayMode` state ('chart'|'table'), default 'chart'. Both chart/table and hourly/daily toggles always visible
- `types.ts`: Renamed `HourlyUsageRow` → `UsageDataRow` with backward-compatible alias. Handles both `"YYYY-MM-DDTHH"` and `"YYYY-MM-DD"` bucket formats
- `helpers.ts`: `formatHourBucket()` now detects format via `bucket.includes('T')` and formats daily as "Mar 30, 2026"
- `UsageTable.tsx`: Updated to use `UsageDataRow` type

## Key Patterns
- Chart.js in Preact: register components, use useRef for canvas, useEffect for lifecycle, destroy on cleanup
- SUBSTR(u.hour_bucket, 1, 10) works for both SQLite and PostgreSQL for day extraction
- Dual SQL query paths in handlers: check `view` variable and construct different queries for hourly vs daily
