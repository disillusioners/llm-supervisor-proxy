# Tester Documentation: llm-supervisor-proxy

## Project Overview
Go-based proxy server for supervising and managing LLM API requests. Uses SQLite/PostgreSQL for storage, Preact frontend for UI.

## Technology Stack
- **Backend**: Go (net/http, custom migration framework)
- **Frontend**: Preact + Vite + TypeScript + Tailwind CSS
- **Database**: SQLite (default), PostgreSQL (supported)
- **Build**: `go build ./cmd/main.go`, `npm run build` (frontend)

## Testing History

| Phase | Date | Status | Details |
|-------|------|--------|---------|
| Phase 1 | 2026-03-31 | ✅ PASS | Token hourly usage (backend), 355+ tests |
| Phase 2 | 2026-03-31 | ✅ PASS | Usage API endpoints, 202 tests |
| Phase 3 | 2026-03-31 | ✅ PASS | Frontend visualization, 231 tests |
| Test Pack | 2026-04-06 | ✅ PASS | 819 tests, 5 new test files, 4109 lines |
| Idle Termination | 2026-04-06 | ✅ PASS | 8 new tests, 575 new lines, commit 068aa0d |
| Memory Traps Fix | 2026-04-08 | ✅ PASS | Full integration test with race, 23 packages, quick fix commit 972dd01 |
| Token Ultimate Permission | 2026-04-09 | ✅ PASS | 22/22 packages, 9/9 API tests, feature branch `feature/token-ultimate-permission` |
| Fallback Token Count Tests | 2026-04-11 | ✅ PASS | 23 test functions, 129 subcases, 1107 lines, 21 packages, race clean |
| Usage Chart + Daily Bug Fix | 2026-04-12 | ✅ PASS | 21 packages, API verified (hourly/daily), browser automation 6/6, bug fix confirmed |
| Usage Chart + Daily Fix | 2026-04-12 | ✅ PASS | 21/21 Go tests, API verified, browser 6/6 PASS, bug fix verified, branch `feature/usage-chart-view` |
| Secondary Upstream Model (Phase 4) | 2026-04-13 | ✅ PASS | 7 new/extended test files, 2011 lines, 935 tests total, commit 9b20182 |
| C1+C2 Critical Test Gaps | 2026-04-13 | ✅ PASS | 8 new test functions, 1144 lines, execution-level model swap + peak combo, commit 3cd5d56 |

## Test Commands
- **Unit tests**: `go test ./... -count=1`
- **Unit tests (verbose)**: `go test ./... -v -count=1`
- **Unit tests (with race)**: `go test ./... -v -race`
- **Go vet**: `go vet ./...`
- **Frontend build**: `cd pkg/ui/frontend && npm run build`
- **Full build**: `go build ./cmd/main.go` (note: `go build .` conflicts with `test_load.go`)

## Test Pack Structure

| Pack | Directory | Tests | Key Files |
|------|-----------|-------|-----------|
| proxy_unit_test | pkg/proxy/ | ~300+ | race_executor_test.go, handler_*.go, adapter_*.go |
| token_unit_test | pkg/proxy/token/ | ~23 | counter_test.go, prompts_test.go |
| ultimatemodel_unit_test | pkg/ultimatemodel/ | ~114 | handler_external_test.go, handler_internal_test.go, usage_test.go |
| store_unit_test | pkg/store/database/ | ~50+ | querybuilder_test.go, database_test.go |
| models_unit_test | pkg/models/ | ~100 | peak_hours_test.go, config_deep_test.go, config_secondary_test.go |
| toolrepair_unit_test | pkg/toolrepair/ | ~45 | strategies_test.go, repair_test.go |
| loopdetection_unit_test | pkg/loopdetection/ | ~31 | detector_test.go |
| auth_unit_test | pkg/auth/ | ~20 | token_test.go, store_test.go |
| misc_unit_test | pkg/{config,crypto,events,...} | ~60+ | various |

## Testing Conventions
- Standard Go testing with `testing` package
- Table-driven tests for parameterized scenarios
- No external test frameworks required
- In-memory SQLite for database-layer tests
- Interfaces used for mockability (e.g., `auth.TokenStoreInterface`)
- `httptest.NewServer` for HTTP handler mocking
