# Test Report: Model ID Consistency — IDs in Logic, Names Only for Display

**Date:** 2026-05-07
**Branch:** `fix/models-use-id-everywhere`
**Commits tested:** `ebc1d2a` → `2641a5f` → `d409f9c` → `96eacb4` → `ca0d670`
**Sessions:** 3 opencode sessions

---

## Summary
- **Overall Status**: ✅ **READY** (after 5 critical bug fixes)
- **Bugs Found**: 5 (3 critical security, 1 build-breaking, 1 test)
- **Quick Fixes Applied**: 5 (all fixed by functional test session)
- **New Test File**: `test/access_control_test.go` (579 lines)

---

## 1. Go Build: ✅ PASS
- `go build ./cmd/main.go` — compiled successfully

## 2. Go Vet: ✅ PASS
- `go vet ./...` — clean, no issues

## 3. Go Tests: ✅ PASS
- **Total packages**: 24
- **Failed**: none
- **Key packages**:

| Package | Status |
|---------|--------|
| pkg/auth/ | ✅ PASS |
| pkg/proxy/ | ✅ PASS |
| pkg/ultimatemodel/ | ✅ PASS |
| pkg/store/database/ | ✅ PASS |
| pkg/models/ | ✅ PASS |
| test/ (new access_control_test.go) | ✅ PASS |

## 4. Frontend Build: ✅ PASS
- `npm run build` — succeeded (2.05s)
- No TypeScript errors

## 5. Frontend Component Validation: ✅ PASS

### TokenForm.tsx
| Check | Status | Evidence |
|-------|--------|----------|
| Model dropdown value uses model.id | ✅ PASS | `value={model.id}` |
| Model dropdown displays model.name | ✅ PASS | `{model.name}` |
| Ultimate model stores ID | ✅ PASS | State stores ID from select |

### TokenList.tsx
| Check | Status | Evidence |
|-------|--------|----------|
| allowed_models shows names | ✅ PASS | `getModelDisplayName()` resolves ID→name |
| ultimate_model shows name | ✅ PASS | `models.find(m => m.id === token.ultimate_model)?.name` |

### ModelMultiSelect.tsx
| Check | Status | Evidence |
|-------|--------|----------|
| Stores IDs internally | ✅ PASS | `onChange([...selected, modelId])` |
| Displays names in UI | ✅ PASS | `{model.name}` in checkbox label |
| Search by name | ✅ PASS | `model.name.toLowerCase().includes(...)` |

## 6. Functional Mock Tests: ✅ PASS (after fixes)

### Group A: Access Control (fail-closed)
| Scenario | Expected | Result |
|----------|----------|--------|
| Allowed model match | 200 ALLOWED | ✅ PASS |
| Allowed model mismatch | 403 FORBIDDEN | ✅ PASS |
| Unknown model fail-closed | 403 FORBIDDEN | ✅ PASS |
| Open access unrestricted | 200 ALLOWED | ✅ PASS |

### Group B: Ultimate Model + allowed_models
| Scenario | Expected | Result |
|----------|----------|--------|
| Ultimate model allowed | 200 ALLOWED | ✅ PASS |
| Ultimate model forbidden | 403 FORBIDDEN | ✅ PASS |

### Group C: ID Consistency
| Check | Result |
|-------|--------|
| IsModelAllowed receives IDs | ✅ PASS |
| No GetModelName references | ✅ PASS (0 found) |

## 7. Code Verification
- `GetModelName` references: **0** (correctly removed)
- `GetModelIDByName` references: **10** (correctly present)

---

## Bugs Found & Fixed

| # | Severity | Issue | Fix |
|---|----------|-------|-----|
| 1 | CRITICAL | requiresInternalAuth not resolving names→IDs | Added GetModelIDByName call |
| 2 | CRITICAL | Access control inside auth block | Separated auth from access control |
| 3 | CRITICAL | Ultimate model not checked in access control | Added ultimate model access check |
| 4 | HIGH | Variable shadowing `token` package | Renamed to `authToken` |
| 5 | LOW | Test used non-existent model name | Changed to correct name |

All fixes in commit: `ca0d670`

---

## ensure.md Validation

### Critical Requirements
- [x] All Go unit tests pass (`go test ./...`) — ✅ PASS
- [x] `go vet ./...` passes with no issues — ✅ PASS
- [x] Full project builds without compilation errors — ✅ PASS
- [x] Frontend builds successfully without TypeScript errors — ✅ PASS

### Important Requirements
- [x] Peak hour logic handles cross-midnight windows correctly — ✅ PASS
- [x] API rejects peak_hour_enabled=true on non-internal upstream (400) — ✅ PASS

---

## Documentation Updated
- [x] README.md — added Model ID Everywhere test entry
- [x] PACKS.md — updated last run dates
- [x] LESSONS/model-id-access-control-bugs.md — documented 5 critical bugs found
- [x] RESULTS/2026-05-07-model-id-everywhere.md — this report
