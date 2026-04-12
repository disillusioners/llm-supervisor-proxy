# Phase 1 Review: Secondary Upstream Model Data Model

**Date:** 2026-04-13
**Commit:** 8ecfe36
**Branch:** feature/secondary-upstream-model (or main)
**Verdict:** PASS — 0 critical, 0 warnings, 1 suggestion

## Key Findings
- Implementation is clean and follows existing patterns perfectly
- Field ordering (SQL columns ↔ Go struct fields) is perfectly aligned — column #18 = struct field #18
- PostgreSQL UPDATE parameter numbering correctly adjusted ($15 for field, $16 for WHERE)
- All 3 dummy scan counts updated from 17→18
- All 3 handler mapping sites (GET/POST/PUT) updated in server.go
- Validation in both config.go and server.go (POST+PUT)
- Backward compatible: DEFAULT '' + coalesce() + omitempty

## Pattern to Remember
When adding new model fields to this codebase:
1. Add column LAST in SELECT queries (appended after existing columns)
2. Add struct field LAST in dbModelRow (matching SELECT order)
3. Update INSERT columns + VALUES + ON CONFLICT SET
4. Update UPDATE SET clause + renumber PostgreSQL WHERE clause param
5. Update dummy scan counts (+1) in AddModel, UpdateModel, RemoveModel
6. Add mapping in scanModels(), GetModel(), and all scan helper sites
7. Add validation in config.go Validate() AND server.go POST/PUT handlers
8. Add field to server.go Model struct + all 3 handler mapping sites

## Suggestion (non-blocking)
- Error message wording inconsistency: "requires internal to be true" vs "requires internal upstream to be enabled"
