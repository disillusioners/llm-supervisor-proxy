# Bug: `exclude_from_ultimate_switching` Not Persisted to Database

**Date**: 2026-05-30
**Severity**: High — Feature non-functional (toggle doesn't save)
**Area**: Database/Store layer

## Problem
The "Exclude from Ultimate Model Switching" toggle is visible and interactive in the frontend, but the value does not persist after save. It always returns `false` (Go zero value).

## Root Cause
The backend Go struct (`ModelConfig`) has the field, and the frontend sends it correctly, but:
1. **No database migration** — The `models` table has no `exclude_from_ultimate_switching` column
2. **`InsertModel` SQL** — Missing the field in INSERT query
3. **`UpdateModel` SQL** — Missing the field in UPDATE query

## Fix Required
1. Add migration: `ALTER TABLE models ADD COLUMN exclude_from_ultimate_switching INTEGER NOT NULL DEFAULT 0`
2. Update `InsertModel` to include `exclude_from_ultimate_switching`
3. Update `UpdateModel` to include `exclude_from_ultimate_switching`
4. Update store Go code to pass the field

## Evidence
- Browser test screenshots: `/tmp/02-model-form.png`, `/tmp/03-toggle-checked.png`
- Toggle checks OK but re-opening the model shows unchecked state
- Backend tests pass because they test the Go struct, not the DB round-trip
