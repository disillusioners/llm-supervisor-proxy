# Resolved: `exclude_from_ultimate_switching` Not Persisted to Database

**Date**: 2026-05-30
**Status**: ✅ RESOLVED
**Original Bug**: See `exclude-ultimate-switching-db-persistence-bug.md`

## Resolution
Developer fixed the DB layer (migration 027, querybuilder, store.go). During E2E re-test, 2 additional bugs were found in `pkg/ui/server.go` and fixed:
1. **Commit `9560db0`**: Added `ExcludeFromUltimateSwitching` to UI server Model struct + PUT handler mapping
2. **Commit `4ccef35`**: Added field mapping in GET and POST model handlers

## Final Verification
- 27/27 Go packages PASS
- 9/9 exclusion feature tests PASS
- Browser E2E: Toggle ON persists ✅, Toggle OFF persists ✅
- Frontend build clean

## Root Lesson
When adding model fields, the UI server has its own Model struct and handler mappings that must also be updated. See `quick-fix-ui-server-exclude-field-mappings.md` for the full pattern.
