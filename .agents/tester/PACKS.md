# Test Packs

## Summary
- Total: 8 packs across 22 packages
- Unit: 8 | Integration: 0 | E2E: 0 | Mock: 0 (see MOCK_TESTS.md for peak hour fallback mock)

## Unit Test Packs

| Pack | Location | Scope | Last Run | Status |
|------|----------|-------|----------|--------|
| proxy_unit_test | pkg/proxy/ | handler, race_executor, adapters, streaming, auth | 2026-04-06 | PASS |
| ultimatemodel_unit_test | pkg/ultimatemodel/ | handler, handler_external, handler_internal, usage | 2026-04-06 | PASS |
| store_unit_test | pkg/store/database/ | database, querybuilder, mock_store | 2026-04-06 | PASS |
| models_unit_test | pkg/models/ | config, peak_hours, credentials, errors | 2026-04-06 | PASS |
| toolrepair_unit_test | pkg/toolrepair/ | repair, strategies, fixer | 2026-04-06 | PASS |
| loopdetection_unit_test | pkg/loopdetection/ | detector, fingerprint, strategies | 2026-04-06 | PASS |
| auth_unit_test | pkg/auth/ | token, store | 2026-04-06 | PASS |
| misc_unit_test | pkg/config, pkg/crypto, pkg/events, pkg/bufferstore, pkg/providers, pkg/supervisor, pkg/toolcall, pkg/ui, pkg/usage | various | 2026-04-06 | PASS |

## Mock Test Packs

| Pack | Location | Type | Last Run | Status |
|------|----------|------|----------|--------|
| peak_hour_fallback_mock | test/mock_llm_peak_hour_fallback.go | E2E Mock | 2026-03-30 | PASS |

---

## Updating PACKS.md

Update after each test run:
- **Last Run**: timestamp
- **Status**: PASS/FAIL/TIMEOUT
- Add new entry for new packs
- Mark deprecated packs as DEPRECATED
