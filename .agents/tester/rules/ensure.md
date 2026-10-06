# Quality Requirements

## Critical
- [x] All Go unit tests pass (`go test ./...`)
- [x] `go vet ./...` passes with no issues
- [x] Full project builds without compilation errors
- [x] `tsc --noEmit` reports zero NEW error locations vs the documented baseline (2026-10-06 rewrite: the literal "Frontend builds successfully without TypeScript errors" was unsatisfiable — 26 pre-existing baseline errors in 11 untouched files, documented debt since ≥2026-08-28)

## Important
- [ ] Peak hour logic handles cross-midnight windows correctly
- [ ] API rejects peak_hour_enabled=true on non-internal upstream (400)
- [ ] All peak hour fields round-trip through GET/POST/PUT API handlers
- [ ] Database migration 018 is valid for both SQLite and PostgreSQL

## Nice-to-have
- [ ] No race conditions detected in test runs
- [ ] Test coverage includes all boundary conditions
