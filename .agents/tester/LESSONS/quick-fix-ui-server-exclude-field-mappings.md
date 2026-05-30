# Quick Fix: UI Server Missing `ExcludeFromUltimateSwitching` Field Mappings

**Date**: 2026-05-30
**Commits**: `9560db0`, `4ccef35`
**Files**: `pkg/ui/server.go`

## Problem
The DB persistence fix (migration 027, querybuilder, store.go) was correct, but the **UI server API layer** was missing the field entirely:
1. The `Model` struct (used by the UI API) had no `ExcludeFromUltimateSwitching` field
2. The `handleModelDetail` PUT handler didn't map the field from API input → `ModelConfig`
3. The `handleModels` GET handler didn't map the field from `ModelConfig` → API response
4. The `handleModels` POST handler didn't map the field from API input → new model

## Impact
- Toggle always appeared OFF on page load (GET handler missing field)
- Toggle value silently discarded on save (PUT/POST handler missing mapping)

## Fix
Two commits adding 4 lines total:
1. **9560db0**: Added field to `Model` struct + PUT handler mapping
2. **4ccef35**: Added field mapping in GET and POST handlers

## Lesson
When adding a new model field, the full chain must be updated:
1. ✅ `ModelConfig` struct (pkg/models)
2. ✅ Database migration
3. ✅ Query builder (Insert/Update/Select)
4. ✅ Store layer (scan/insert/update)
5. ✅ **UI server Model struct** ← easily forgotten
6. ✅ **UI server handler mappings (GET/POST/PUT)** ← easily forgotten

This is a common pattern: the UI API layer has its own Model struct separate from the internal ModelConfig, and both need the field + all handler mappings.
