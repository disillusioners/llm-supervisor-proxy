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
| proxy_unit_test | test/packs/proxy_unit_test.sh | handler, race_executor, adapters, streaming, auth | 120s | 2026-04-12 | PASS |
| ultimatemodel_unit_test | test/packs/ultimatemodel_unit_test.sh | handler, handler_external, handler_internal, usage | 120s | 2026-04-12 | PASS |
| store_unit_test | test/packs/store_unit_test.sh | database, querybuilder, mock_store | 120s | 2026-04-12 | PASS |
| models_unit_test | test/packs/models_unit_test.sh | config, peak_hours, credentials, errors | 120s | 2026-04-12 | PASS |
| toolrepair_unit_test | test/packs/toolrepair_unit_test.sh | repair, strategies, fixer | 120s | 2026-04-12 | PASS |
| loopdetection_unit_test | test/packs/loopdetection_unit_test.sh | detector, fingerprint, strategies | 120s | 2026-04-12 | PASS |
| auth_unit_test | test/packs/auth_unit_test.sh | token, store | 120s | 2026-04-12 | PASS |
| token_unit_test | pkg/proxy/token/ (inline) | counter, prompts, encoding, extraction | 120s | 2026-04-12 | PASS |
| misc_unit_test | test/packs/misc_unit_test.sh | config, crypto, events, bufferstore, providers, supervisor, toolcall, ui, usage | 120s | 2026-04-12 | PASS |

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

## Recent Test Results (2026-04-09)

### Commit b3d22e2 - Frontend API Optimization Fixes
| Category | Status | Details |
|----------|--------|---------|
| Go Build | ✅ PASS | `go build ./cmd/main.go` |
| Frontend Build | ✅ PASS | `npm run build` (775ms) |
| Go Unit Tests | ✅ PASS | 20/20 packages |
| Go Vet | ✅ PASS | No issues |
| Mock Cache Tests | ✅ PASS | Race condition verified fixed |
| Code Verification | ✅ PASS | All 8 fixes verified |

### Fixes Verified
1. ✅ Manual refresh button (refetchRequests with refreshKey)
2. ✅ SSE events update request list in real-time
3. ✅ App tag filtering works
4. ✅ App tags update when new tags appear (refetchAppTags)
5. ✅ Token permission changes persist (cache invalidation)
6. ✅ RAM polling stops when tab hidden, resumes when visible
7. ✅ No duplicate SSE-triggered fetches (debounce works)
8. ✅ App.tsx type mismatch fix
