# Test Report: Exclude from Ultimate Switching — Full E2E Re-Test (Post DB Fix)
Date: 2026-05-30
Sessions: backend-full, frontend-build-2, browser-e2e, post-fix-regression

## Summary
- Go Build: ✅ PASS
- Go Vet: ✅ PASS
- Backend Full Suite: ✅ PASS (27/27 packages, 0 failures)
- Frontend Build: ✅ PASS (1.27s, 0 errors)
- Browser E2E Persist: ✅ PASS (Toggle ON/OFF both persist correctly)
- Bugs Found & Fixed: 3 (2 quick fixes in pkg/ui/server.go)
- Quick Fixes Committed: `9560db0`, `4ccef35`

---

## 1. Backend Full Suite: ✅ PASS

| Metric | Value |
|--------|-------|
| Packages | 27/27 passed |
| Failures | 0 |
| Errors | 0 |
| Skipped | 7 (long-running, PostgreSQL integration) |

### Key Packages

| Package | Status | Time |
|---------|--------|------|
| `pkg/models` | ✅ PASS | 0.052s |
| `pkg/proxy` | ✅ PASS | 24.758s |
| `pkg/ultimatemodel` | ✅ PASS | 4.219s |
| `pkg/store` | ✅ PASS | 0.021s |
| `pkg/store/database` | ✅ PASS | 2.407s |
| `pkg/ui` | ✅ PASS | 0.036s |
| `pkg/mcp` | ✅ PASS | 29.141s |
| `pkg/usage` | ✅ PASS | 6.182s |
| `test` | ✅ PASS | 5.723s |

### Exclude Feature Tests (9/9 PASS)

| Test | Result |
|------|--------|
| `TestModelConfig_ExcludeFromUltimateSwitching_JSONRoundTrip` | ✅ |
| `TestModelConfig_ExcludeFromUltimateSwitching_DefaultFalse` | ✅ |
| `TestModelConfig_ExcludeFromUltimateSwitching_JSONFieldMatches` | ✅ |
| `TestModelConfig_ExcludeFromUltimateSwitching_WithOtherFields` | ✅ |
| `TestModelConfig_ExcludeFromUltimateSwitching_ExplicitFalse` | ✅ |
| `TestUltimateModel_ExcludedModel_DuplicateContinuesNormalFlow` | ✅ |
| `TestUltimateModel_ExcludedModel_ForceTriggerBypassesExclusion` | ✅ |
| `TestUltimateModel_ExcludedModel_RetryExhaustedNoError` | ✅ |
| `TestUltimateModel_ExcludedModel_CrossModelDetection` | ✅ |

### Go Build: ✅ PASS
### Go Vet: ✅ PASS

---

## 2. Frontend Build: ✅ PASS

| Metric | Value |
|--------|-------|
| Build Time | 1.27s |
| Modules | 47 |
| Errors | 0 |

---

## 3. Browser E2E: ✅ PASS

### Test Scenarios

| Scenario | Expected | Actual | Result |
|----------|----------|--------|--------|
| Initial load (field=false) | Toggle OFF | Toggle OFF | ✅ PASS |
| Toggle ON → Save | Persisted ON | Persisted ON | ✅ PASS |
| Re-open model → Toggle state | Still ON | Still ON | ✅ PASS |
| Toggle OFF → Save | Persisted OFF | Persisted OFF | ✅ PASS |
| Re-open model → Toggle state | Still OFF | Still OFF | ✅ PASS |

### API Persistence Verification
```
SCENARIO 1 (ON):  DB value after update = 1 ✅
SCENARIO 2 (OFF): DB value after update = 0 ✅
```

### Screenshots
- `/tmp/09-toggle-on-persisted.png` — Toggle ON persisted after re-open
- `/tmp/10-toggle-off-saved.png` — Toggle OFF saved correctly

---

## 4. Bugs Found & Fixed

### Bug 1: Model struct missing field (commit `9560db0`)
- **File**: `pkg/ui/server.go`
- **Issue**: UI server's `Model` struct was missing `ExcludeFromUltimateSwitching` field
- **Fix**: Added field to struct and `handleModelDetail` PUT handler mapping
- **Quick fix**: Yes (2 lines)

### Bug 2: GET/POST handlers missing field (commit `4ccef35`)
- **File**: `pkg/ui/server.go`
- **Issue**: `handleModels` GET handler wasn't mapping the field when converting `ModelConfig` → `Model`, so frontend always received `false`
- **Fix**: Added field mapping in GET and POST handlers
- **Quick fix**: Yes (2 lines)

### Root Cause Chain
1. Migration 027 added the DB column ✅ (done by developer)
2. `querybuilder.go` queries updated ✅ (done by developer)
3. `store.go` scan/insert/update updated ✅ (done by developer)
4. **UI server `Model` struct** missing field ❌ → Fixed (9560db0)
5. **UI server handler mappings** missing field ❌ → Fixed (4ccef35)

---

## 5. Post-Fix Regression: ✅ PASS

| Test | Result |
|------|--------|
| `go build ./cmd/main.go` | ✅ PASS |
| `go test ./pkg/ui/...` | ✅ PASS (83 tests) |
| `go test ./pkg/store/...` | ✅ PASS (100 tests) |
| `go test ./pkg/ultimatemodel/...` | ✅ PASS (119 tests) |
| `go vet ./...` | ✅ PASS |

---

## ensure.md Validation

| Requirement | Status |
|-------------|--------|
| All Go unit tests pass | ✅ PASS (27/27) |
| `go vet ./...` passes | ✅ PASS |
| Full project builds without compilation errors | ✅ PASS |
| Frontend builds successfully without TypeScript errors | ✅ PASS |

---

## Code Changes Summary

| Commit | File | Description |
|--------|------|-------------|
| `9560db0` | `pkg/ui/server.go` | Added `ExcludeFromUltimateSwitching` to Model struct + PUT handler |
| `4ccef35` | `pkg/ui/server.go` | Added field mapping in GET and POST model handlers |

---

## Overall Status: ✅ ALL PASS

- Backend: 27/27 packages, 9/9 exclusion tests
- Frontend: Build clean
- Browser E2E: Toggle ON/OFF persist verified
- Bugs found: 2, both fixed and committed
- Regression: Clean after fixes
- **Feature Status: ✅ COMPLETE — End-to-end flow verified working**
