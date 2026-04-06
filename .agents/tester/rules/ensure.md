# Quality Requirements

## Critical
- [x] All Go unit tests pass (`go test ./...`)
- [x] `go vet ./...` passes with no issues
- [x] Full project builds without compilation errors
- [x] Frontend builds successfully without TypeScript errors

## Important
- [ ] Peak hour logic handles cross-midnight windows correctly
- [ ] API rejects peak_hour_enabled=true on non-internal upstream (400)
- [ ] All peak hour fields round-trip through GET/POST/PUT API handlers
- [ ] Database migration 018 is valid for both SQLite and PostgreSQL

## Nice-to-have
- [ ] No race conditions detected in test runs
- [ ] Test coverage includes all boundary conditions
