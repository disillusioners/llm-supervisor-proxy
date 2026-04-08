# Test Packs

## Summary
- Total: 8 packs across 22 packages
- Unit: 8 | Integration: 0 | E2E: 0 | Mock: 0 (see MOCK_TESTS.md for peak hour fallback mock)
- All packs enforce **2-minute timeout** via `timeout` command (subprocess-based)

## Timeout Configuration
- **Script timeout**: 120s (`timeout 120s`)
- **Go test timeout**: 110s (`-timeout=110s`)
- **Buffer**: 10s for script overhead/cleanup
- **Exit codes**: 0=PASS, 1=FAIL, 124=TIMEOUT

## Unit Test Packs

| Pack | Script | Scope | Timeout | Last Run | Status |
|------|--------|-------|---------|----------|--------|
| proxy_unit_test | test/packs/proxy_unit_test.sh | handler, race_executor, adapters, streaming, auth | 120s | 2026-04-08 | PASS |
| ultimatemodel_unit_test | test/packs/ultimatemodel_unit_test.sh | handler, handler_external, handler_internal, usage | 120s | 2026-04-06 | PASS |
| store_unit_test | test/packs/store_unit_test.sh | database, querybuilder, mock_store | 120s | 2026-04-06 | PASS |
| models_unit_test | test/packs/models_unit_test.sh | config, peak_hours, credentials, errors | 120s | 2026-04-06 | PASS |
| toolrepair_unit_test | test/packs/toolrepair_unit_test.sh | repair, strategies, fixer | 120s | 2026-04-06 | PASS |
| loopdetection_unit_test | test/packs/loopdetection_unit_test.sh | detector, fingerprint, strategies | 120s | 2026-04-06 | PASS |
| auth_unit_test | test/packs/auth_unit_test.sh | token, store | 120s | 2026-04-09 | PASS |
| misc_unit_test | test/packs/misc_unit_test.sh | config, crypto, events, bufferstore, providers, supervisor, toolcall, ui, usage | 120s | 2026-04-06 | PASS |

## Mock Test Packs

| Pack | Script | Type | Timeout | Last Run | Status |
|------|--------|------|---------|----------|--------|
| peak_hour_fallback_mock | test/mock_llm_peak_hour_fallback.go | E2E Mock | N/A (Go test) | 2026-03-30 | PASS |

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
