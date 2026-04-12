# Phase 1: Secondary Upstream Model — Backend Data Model & Persistence

**Date:** 2026-04-11
**Commit:** 8ecfe36
**Branch:** feature/secondary-upstream-model (or main)

## What Was Done
Added `secondary_upstream_model` field across the entire backend stack:
- `pkg/models/config.go` — struct field + validation (requires internal=true)
- DB migration 021 (SQLite + PostgreSQL)
- `pkg/store/database/migrate.go` — migration registration
- `pkg/store/database/querybuilder.go` — all SELECT/INSERT/UPDATE queries
- `pkg/store/database/store.go` — dbModelRow, scan, CRUD, dummy scan counts (17→18)
- `pkg/ui/server.go` — Model struct, GET/POST/PUT mapping, API validation
- `pkg/store/database/querybuilder_test.go` — placeholder counts updated (15→16)

## Key Patterns
- Adding a new column to models table requires changes in 7+ files
- Column ordering must be CONSISTENT across SELECT, INSERT, UPDATE
- Dummy scan count in store.go must match SELECT column count (18 after this change)
- Validation duplicated in both config.go (backend) and server.go (API handler)
- Use `coalesce(field, '')` for backward compat with NULL values
- Migration naming: `021_descriptive_name.up.sql`

## Lessons
- The querybuilder_test.go tests count `$` occurrences for placeholder verification
- Placeholder count went from 15 to 16 (for 16 data columns, excluding auto-managed ones)
- Dummy scans went from 17 to 18 (matches SELECT column count)
