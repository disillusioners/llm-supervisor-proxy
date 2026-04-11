# Usage Chart + Daily Aggregation Fix

## Date: 2026-04-08
## Commit: 6ba701b

## What was done
1. Fixed backend daily aggregation bug in `pkg/ui/handlers_usage.go`
   - The `view` query parameter was read but never used for SQL grouping
   - Added conditional SQL: when view=daily, uses SUBSTR(hour_bucket,1,10) + GROUP BY + SUM
   - All 4 query paths handled (with/without token_id × PostgreSQL/SQLite)

2. Installed chart.js v4.5.1 in frontend
   - No React wrapper needed — uses canvas refs directly with Preact

3. Created UsageChart.tsx
   - Multi-line chart: one line per token + dashed white "Total" line
   - 8-color palette, dark theme compatible
   - Proper Chart.js lifecycle: register components, destroy on unmount

4. Updated UsageTab.tsx
   - Added displayMode toggle (chart/table), defaults to chart
   - Hourly/daily toggle only shown in table mode
   - Summary cards visible in both modes

5. Updated types and helpers
   - Renamed HourlyUsageRow → UsageDataRow (backward-compatible alias)
   - formatHourBucket() now handles both "YYYY-MM-DDTHH" and "YYYY-MM-DD"

## Key patterns
- Chart.js with Preact: import from 'chart.js', register components, use useRef + useEffect
- SUBSTR works for both SQLite and PostgreSQL for day extraction
- Backend conditionally builds different SQL based on view parameter
