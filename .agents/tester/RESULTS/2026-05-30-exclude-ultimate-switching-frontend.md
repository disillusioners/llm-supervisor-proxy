# Test Report: Exclude from Ultimate Switching — Frontend Feature
Date: 2026-05-30
Sessions: frontend-build, backend-regression, browser-test

## Summary
- Frontend Build: ✅ PASS
- Backend Regression: ✅ PASS (6/6 packages, 0 failures)
- Browser Automation: ⚠️ PARTIAL (toggle visible + interactive, but **state does NOT persist**)
- Quick Fixes Applied: 0

---

## 1. Frontend Build: ✅ PASS

- **Command**: `cd pkg/ui/frontend && npm run build`
- **Result**: Built successfully in 1.23s
- **Output**: 47 modules transformed, 4 chunks (index.html, index.css, SettingsPage.js, index.js)
- **Errors**: 0
- **Warnings**: 0

---

## 2. Backend Regression: ✅ PASS

| Package | Status | Time |
|---------|--------|------|
| `pkg/models` | ✅ PASS | 0.029s |
| `pkg/proxy` | ✅ PASS | 25.120s |
| `pkg/proxy/normalizers` | ✅ PASS | 0.014s |
| `pkg/proxy/token` | ✅ PASS | 0.184s |
| `pkg/proxy/translator` | ✅ PASS | 0.013s |
| `pkg/ultimatemodel` | ✅ PASS | 4.228s |

**Total: 6 packages — ALL PASSED (0 failures, 0 errors)**

### Feature-specific tests passing:
- `TestModelConfig_ExcludeFromUltimateSwitching_JSONRoundTrip` ✅
- `TestModelConfig_ExcludeFromUltimateSwitching_DefaultFalse` ✅
- `TestModelConfig_ExcludeFromUltimateSwitching_JSONFieldMatches` ✅
- `TestModelConfig_ExcludeFromUltimateSwitching_WithOtherFields` ✅
- `TestModelConfig_ExcludeFromUltimateSwitching_ExplicitFalse` ✅

### go vet: ✅ PASS
No issues across all tested packages.

---

## 3. Browser Automation: ⚠️ PARTIAL

### ✅ Toggle Visibility
The "Exclude from Ultimate Model Switching" toggle is **visible** in the ModelForm at `/Settings/Models > Edit Model`.

### ✅ Toggle Interaction
The checkbox can be **checked/unchecked** via browser automation (`check` command works).

### ❌ Toggle State Persistence — BUG FOUND
**The toggle state does NOT persist after save.** The field is silently ignored.

### Root Cause Analysis

| Layer | Status | Detail |
|-------|--------|--------|
| Frontend (`ModelForm.tsx`) | ✅ OK | Has the toggle, sends data correctly |
| Backend `ModelConfig` struct | ✅ OK | Has `ExcludeFromUltimateSwitching` field |
| Backend handler | ✅ OK | Receives the field from frontend |
| **Database schema** | ❌ **MISSING** | No `exclude_from_ultimate_switching` column in `models` table |
| **`UpdateModel` SQL** | ❌ **MISSING** | Field not in UPDATE query |
| **`InsertModel` SQL** | ❌ **MISSING** | Field not in INSERT query |

The field is silently dropped during save and always returns `false` (Go zero value) when loaded.

### Screenshots
- `/tmp/02-model-form.png` — Model form with toggle visible
- `/tmp/03-toggle-checked.png` — Toggle successfully checked

---

## Action Needed

### ❌ BUG: Database persistence for `exclude_from_ultimate_switching`

**Required fixes (NOT quick-fix — involves multiple layers):**

1. **Add database migration** — Add `exclude_from_ultimate_switching INTEGER NOT NULL DEFAULT 0` to `models` table
2. **Update `InsertModel` SQL query** — Include the new field
3. **Update `UpdateModel` SQL query** — Include the new field
4. **Update Go store code** — Pass this field in insert/update operations

---

## Overall Status
- Frontend Build: ✅ PASS
- Backend Unit Tests: ✅ PASS (6/6 packages)
- Browser Visibility: ✅ PASS
- Browser Interaction: ✅ PASS
- Browser Persistence: ❌ FAIL — Database layer not implemented
- **Feature Status: ❌ NOT COMPLETE** — Persistence layer missing
