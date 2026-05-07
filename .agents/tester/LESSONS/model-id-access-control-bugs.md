# Access Control Bugs Found During Model ID Refactor

**Date:** 2026-05-07
**Branch:** `fix/models-use-id-everywhere`
**Commit:** `ca0d670`

## Summary

During functional testing of the model ID consistency refactor, **5 critical security bugs** were discovered and fixed. The original branch code had fail-closed logic that was NOT actually working correctly.

## Bugs Found

### Bug 1: `requiresInternalAuth` Not Resolving Model Names to IDs
- **Severity:** CRITICAL
- **File:** `pkg/proxy/handler.go`
- **Issue:** When checking internal auth requirement, the code was using raw model names instead of resolving them to IDs via `GetModelIDByName`. This caused access control to be skipped for internal models.
- **Fix:** Added `GetModelIDByName` call to resolve model names to IDs before `GetModel` lookup

### Bug 2: Access Control Inside Auth Block
- **Severity:** CRITICAL
- **File:** `pkg/proxy/handler.go`
- **Issue:** Access control (allowed_models check) was placed inside the authentication block. If auth failed or wasn't needed, access control was skipped entirely. Unknown models with restricted tokens were being ALLOWED instead of denied.
- **Fix:** Separated authentication from access control — access control runs whenever a token is provided, regardless of auth flow

### Bug 3: Ultimate Model Not Checked in Access Control
- **Severity:** CRITICAL
- **File:** `pkg/proxy/handler.go`
- **Issue:** Ultimate model bypass was not subject to `allowed_models` restrictions. A token with `allowed_models = ["id-1"]` could still use `ultimate_model = "id-2"`.
- **Fix:** Added ultimate model access check before execution, verifying ultimate model ID is in allowed_models

### Bug 4: Variable Shadowing `token` Package
- **Severity:** HIGH (build-breaking)
- **File:** `pkg/proxy/handler.go`
- **Issue:** Local variable `token` shadowed the `token` package import, causing `token.FallbackEnabled()` to call method on wrong type
- **Fix:** Renamed variable to `authToken`

### Bug 5: Test Case Using Non-Existent Model Name
- **Severity:** LOW
- **File:** `test/integration_allowed_models_test.go`
- **Issue:** `TestHandler_CaseSensitivity_ExactMatch` used model name "GPT-4" which doesn't exist in test DB
- **Fix:** Changed to "gpt-4" (actual model name in test data)

## Testing Impact
- Created `test/access_control_test.go` — 579 lines of comprehensive access control tests
- All 6 functional scenarios pass:
  - ✅ Allowed model match → ALLOWED
  - ✅ Allowed model mismatch → 403
  - ✅ Unknown model fail-closed → 403
  - ✅ Open access unrestricted → ALLOWED
  - ✅ Ultimate model allowed → ALLOWED
  - ✅ Ultimate model forbidden → 403

## Lesson Learned
When refactoring identity (names → IDs), **every code path that touches model identity must be audited**. The original refactor changed `IsModelAllowed` to accept IDs but missed the callers that still passed names. Always verify security-critical code paths with explicit functional tests, not just unit tests.
