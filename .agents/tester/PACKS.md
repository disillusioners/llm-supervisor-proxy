# Test Packs

## Summary
- Total: 10 packs across 22 packages
- Unit: 9 | Integration: 0 | E2E: 0 | Mock: 1
- All packs enforce **2-minute timeout** via `timeout` command (subprocess-based)

## Timeout Configuration
- **Script timeout**: 120s (`timeout 120s`)
- **Go test timeout**: 110s (`-timeout=110s`)
- **Buffer**: 10s for script overhead/cleanup
- **Exit codes**: 0=PASS, 1=FAIL, 124=TIMEOUT

## Unit Test Packs

| Pack | Script | Scope | Timeout | Last Run | Status |
|------|--------|-------|---------|----------|--------|
| proxy_unit_test | test/packs/proxy_unit_test.sh | handler, race_executor, adapters, streaming, auth | 120s | 2026-04-15 | PASS |
| ultimatemodel_unit_test | test/packs/ultimatemodel_unit_test.sh | handler, handler_external, handler_internal, usage | 120s | 2026-04-13 | PASS |
| store_unit_test | test/packs/store_unit_test.sh | database, querybuilder, mock_store | 120s | 2026-04-13 | PASS |
| models_unit_test | test/packs/models_unit_test.sh | config, peak_hours, credentials, errors, secondary_upstream | 120s | 2026-04-13 | PASS |
| toolrepair_unit_test | test/packs/toolrepair_unit_test.sh | repair, strategies, fixer | 120s | 2026-04-13 | PASS |
| loopdetection_unit_test | test/packs/loopdetection_unit_test.sh | detector, fingerprint, strategies | 120s | 2026-04-13 | PASS |
| auth_unit_test | test/packs/auth_unit_test.sh | token, store | 120s | 2026-04-13 | PASS |
| token_unit_test | pkg/proxy/token/ (inline) | counter, prompts, encoding, extraction | 120s | 2026-04-13 | PASS |
| misc_unit_test | test/packs/misc_unit_test.sh | config, crypto, events, bufferstore, providers, supervisor, toolcall, ui, usage | 120s | 2026-04-13 | PASS |

## Mock Test Packs

| Pack | Script | Type | Timeout | Last Run | Status |
|------|--------|------|---------|----------|--------|
| peak_hour_fallback_mock | test/mock_llm_peak_hour_fallback.go | E2E Mock | N/A (Go test) | 2026-04-09 | PASS |
| frontend_api_cache_mock | test/mock_frontend_api_cache.mjs | Unit | 60s | 2026-04-09 | PASS |

---

## Updating PACKS.md

Update after each test run:
- **Last Run**: timestamp
- **Status**: PASS/FAIL/TIMEOUT
- Add new entry for new packs
- Mark deprecated packs as DEPRECATED

## Integrity Checks
- ✅ All 8 unit test pack scripts exist in `test/packs/`
- ✅ All scripts are executable (`chmod +x`)
- ✅ All scripts use `timeout 120s` for subprocess-based enforcement
- ✅ All scripts output `RESULT: PASS|FAIL|TIMEOUT`
- ✅ All scripts have cleanup traps on EXIT

## Recent Test Results (2026-04-13)

### Commit 3cd5d56 - Critical Test Gaps Fix (C1+C2)
| Category | Status | Details |
|----------|--------|---------|
| Go Build | ✅ PASS | `go build ./cmd/main.go` |
| Go Unit Tests | ✅ PASS | 21/21 packages, 946 tests |
| Go Vet | ✅ PASS | No issues |
| New Test Functions | ✅ 8 | 4 executor E2E + 4 coordinator peak+secondary |

#### New Test Functions
| Function | Location | Verifies |
|----------|----------|----------|
| TestExecuteInternalRequest_SecondaryModelSwap_E2E_NonStream | race_executor_test.go | Provider receives secondary model (non-stream) |
| TestExecuteInternalRequest_SecondaryModelSwap_E2E_Stream | race_executor_test.go | Provider receives secondary model (stream) |
| TestExecuteInternalRequest_NoSecondary_UsesPrimary_E2E | race_executor_test.go | Empty secondary → primary used |
| TestExecuteInternalRequest_SecondaryFalse_UsesPrimary_E2E | race_executor_test.go | Flag=false → primary used |
| TestRaceCoordinator_PeakHourWithSecondaryModel | race_coordinator_test.go | Main=peak, second=secondary |
| TestRaceCoordinator_PeakHourModelOnly_NoSecondary | race_coordinator_test.go | Peak active, no secondary |
| TestRaceCoordinator_SecondaryOverridesPeakHour | race_coordinator_test.go | Secondary independent of peak |
| TestRaceCoordinator_NoPeakHour_UsesInternalModel | race_coordinator_test.go | Peak disabled → internal model |

### Commit 9b20182 - Secondary Upstream Model Phase 4 Tests
| Category | Status | Details |
|----------|--------|---------|
| Go Build | ✅ PASS | `go build ./cmd/main.go` |
| Frontend Build | ✅ PASS | `npm run build` (1.00s) |
| Go Unit Tests | ✅ PASS | 21/21 packages, 935 tests |
| Go Vet | ✅ PASS | No issues |
| New Test Files | ✅ 7 files | 2011 lines of new tests |
| Test Matrix | ✅ 4/4 | All scenarios covered |

### Phase 4 Test Files
| File | Lines | Type |
|------|-------|------|
| pkg/models/config_secondary_test.go | 360 | NEW — validation |
| pkg/proxy/race_coordinator_test.go | 215 | EXTENDED — coordinator |
| pkg/proxy/race_executor_test.go | 279 | EXTENDED — executor |
| pkg/proxy/race_request_test.go | 162 | EXTENDED — request flag |
| pkg/proxy/race_retry_test.go | 68 | EXTENDED — retry integration |
| pkg/store/database/database_test.go | 370 | EXTENDED — store CRUD |
| pkg/ui/handlers_models_test.go | 557 | NEW — API handlers |
