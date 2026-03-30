# Peak Hour Auto-Switch Feature — Completion Summary

## Feature
Auto switch upstream model name during configured peak hours. Internal upstream only.

## Commits
| Commit | Phase | Description |
|--------|-------|-------------|
| `4542f49` | Phase 1 | Backend data model, scheduling logic, DB layer |
| `6753ea1` | Phase 1 | Review fixes: signature, tests, DB validation, float precision |
| `7cf060f` | Phase 2 | API & proxy integration, ResolveInternalConfig(), logging |
| `c7cb486` | Phase 2 | Review fixes: flaky tests, timezone test, log prefix |
| `de38b7d` | Phase 3 | Frontend peak hour UI (Preact) |
| `81e61cd` | Fix | Backend UTC bug, frontend timezone label |
| `fc999aa` | Bonus | Pre-existing data race fix found during testing |

## Architecture
- **Hook point:** `ResolveInternalConfig()` — both JSON-backed (`config.go`) and DB-backed (`store.go`)
- **Method:** `ModelConfig.ResolvePeakHourModel(now time.Time) string`
- **Migration:** 018 — 5 new columns on `models` table (SQLite + PostgreSQL)
- **API:** 400 rejection if peak_hour_enabled on non-internal upstream

## Testing
- 104+ backend subtests (unit + integration)
- 14 Go packages, 0 failures
- Race detection clean
- Frontend builds successfully
- All boundary/cross-midnight/timezone cases verified

## Key Decisions
- UTC offset stored as string ("+7", "-5", "+5.5")
- Time stored as "HH:MM" strings
- Half-open interval: [start, end)
- Frontend status indicator uses UTC-based calculation
- `[PEAK-HOUR]` log prefix for grep-ability
