# Fix: pkg/ui/server.go Missing ExcludeFromUltimateSwitching Field Mappings

**Date**: 2026-05-30
**Severity**: High — Toggle non-functional (never showed stored value)
**Commits**: 9560db0, 4ccef35

## Problem
The "Exclude from Ultimate Model Switching" toggle persisted to DB but:
1. The UI server's `Model` struct didn't have the field → PUT requests dropped it
2. The GET handler didn't include the field → Frontend always saw `false` (toggle always OFF on load)
3. The POST handler didn't include the field → New models couldn't set it

## Root Cause
When the original DB persistence fix was applied (migration 027, store.go, querybuilder.go), the **UI API layer** (`pkg/ui/server.go`) was not updated. Three handlers needed the field mapped:
- `handleModelDetail` (PUT) — needed to read the field from API input
- `handleModels` (GET) — needed to return the field in API response
- `handleModels` (POST) — needed to include the field for new models

## Fixes Applied
1. **Commit 9560db0**: Added `ExcludeFromUltimateSwitching` to Model struct + handleModelDetail PUT mapping
2. **Commit 4ccef35**: Added field to handleModels GET (line 400) and POST (line 468) handlers

## Lesson
When adding a new field to a model, check ALL layers: database → store → query builder → **UI API handlers (GET/POST/PUT)** → frontend type → frontend form. The UI API layer in `pkg/ui/server.go` is a separate mapping layer that needs explicit field additions.
