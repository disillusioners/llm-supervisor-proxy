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
| Token Allowed Models | 2026-05-01 | ✅ PASS | 23 integration tests, 3 bugs found+fixed, branch `feature/token-allowed-models` |
| Token Ultimate Model Override | 2026-05-02 | ✅ PASS | 24 new tests, 1297 lines, branch `feature/token-ultimate-model` |
| DeepSeek Provider | 2026-05-04 | ✅ PASS | 22 packages, 2229+ tests, frontend build PASS, branch `feature/deepseek-provider` |
| Reasoning Content Fix | 2026-05-05 | ✅ PASS | 22 packages, 14 E2E subtests, ensure.md ALL PASS, branch `fix/forward-reasoning-content` |
| E2E Reasoning Content Tests | 2026-05-05 | ✅ PASS | 5 E2E tests, 25 packages regression, commit 3c9d6b8 |
| Ultimate Model "Not Found" Fix | 2026-05-07 | ✅ PASS | 22/22 packages regression, 26 focused tests, 361 new lines, commit 8250179 |
| Token Ultimate Model ID Refactor | 2026-05-07 | ✅ PASS | 24/24 packages, 1044 tests, 7 new tests, frontend PASS, branch `fix/ultimate-model-use-id` |
| Model ID Everywhere | 2026-05-07 | ✅ PASS | 24/24 packages, 5 critical bugs found+fixed, 579-line access control test, frontend PASS, branch `fix/models-use-id-everywhere` |
| X-Force-Ultimate-Model Security | 2026-05-07 | ✅ PASS | 21/21 packages, TestA5 PASS, header gated behind auth+admin, commit `17a7f59` |
| Model Resolution Pipeline Refactor | 2026-05-07 | ✅ PASS | 24/24 packages, 171 functional tests, frontend PASS, branch `fix/remove-getmodelidbyname` |
| Remove ResolveModelByName | 2026-05-08 | ✅ PASS | 24/24 packages, access control tests PASS, frontend PASS, commit `a14a0dc`, branch `fix/remove-resolve-model-by-name` |
| Delete Model Button Fix | 2026-05-08 | ✅ PASS | 22/23 packages (2 pre-existing), frontend PASS, browser 5/5, quick fix `585fe6a`, branch `fix/delete-model-button` |
| Ultimate Model Not Found Log Fix | 2026-05-10 | ✅ PASS | 25/25 packages, ultimatemodel 97 tests, proxy 200+ tests, commit `8510485`, branch `fix/ultimate-model-not-found-log` |
| Credential ID Model Create Fix | 2026-05-10 | ✅ PASS | 25/27 packages (2 pre-existing race), frontend PASS, code logic verified, commit `a2a6c41`, branch `fix/model-credential-create` |
| Model Usage Chart | 2026-05-18 | ✅ PASS | 24/24 packages, 1192 new test lines, 25 test functions, 2 race fixes, branch `feature/model-usage-chart` |
| MCP Proxy Server | 2026-05-19 | ✅ PASS | 25/25 packages (new pkg/mcp), 100+ MCP tests, functional verified, quick fix `32cb5cf` |
| MCP Endpoint Split | 2026-05-20 | ✅ PASS | 23/23 packages, 15 new functional tests (664 lines), all 7 areas verified, branch `feature/mcp-endpoint-split` |
| MCP Test Connection Fix | 2026-05-21 | ✅ PASS | 25/25 packages, 4146 tests, browser 3/3, quick fix `ab85359`, branch `fix/mcp-test-connection` |
| SQLite BUSY Fix | 2026-05-21 | ✅ PASS | 25/25 packages, 2698 tests, 7 new stress tests (670 lines), zero SQLITE_BUSY errors, branch `fix/sqlite-busy-usage-counter` |
| MCP Accept Header Fix | 2026-05-21 | ✅ PASS | 27/27 packages, browser verified zai-web-read SUCCESS (not 400), commit `3729f5d` |
| SQLite Usage Hang Fix | 2026-05-21 | ✅ PASS | 30/30 packages, concurrent reads test PASS, stress tests PASS, frontend PASS, commit `f5a1670`, branch `fix/sqlite-usage-hang` |
| Exclude from Ultimate Switching | 2026-05-30 | ✅ PASS | 27/27 packages, 9/9 feature tests, browser E2E ON+OFF persist verified, 2 quick fixes (9560db0, 4ccef35) |
| Reasoning Content Ultimate-Internal (verify 83814b0) | 2026-08-18 | ✅ PASS | Symptom GONE: new capturing-upstream e2e (`c3a4b35`) PASS→FAIL-on-revert→PASS; ultimatemodel+proxy unit packs PASS; ultimate shell mock 48/48 + buffered 60/60 (after pre-existing Test 8 harness repair `2f67976`+`d1028be`); vet/build clean; 3rd converter probed REACHABLE-but-dormant (no live loss path) |
| MiniMax reasoning_details E2E Gate (P3-2/4/5/6/7) | 2026-08-19 | ✅ READY | Shell mock 53/53 (`882fa3f`), e2e 43/43 (`166aa7f`), matrix 32/32 (`ee590c1`), header table 21 verified, sweep 12/12 zero flakes, drift 0; **2 product bugs found+fixed**: T3b race-internal reasoning loss (`068317c`) + S3 id parity (`b2dfde0`); verification-report.md written; HEAD `fa8c11d` |

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
