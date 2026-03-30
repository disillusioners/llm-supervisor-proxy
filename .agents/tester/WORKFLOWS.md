# Test Plan: Peak Hour Auto-Switch Feature

## Feature Under Test
Models configured as internal upstream can have a peak hour window (start time, end time, UTC timezone offset). During that window, the proxy automatically switches to an alternative upstream model name.

**Commits**: 4542f49, 6753ea1, 7cf060f, c7cb486, de38b7d, 81e61cd

---

## Test Matrix

### T1: Backend Unit Tests + Race Detection
- **Scope**: All Go tests, focus on peak hour tests
- **Command**: `go test ./... -v -race -count=1`
- **Expected**: 80+ tests pass, no race conditions
- **Key files**: `pkg/models/peak_hours_test.go`, `pkg/store/database/config_peak_hour_test.go`

### T2: Backend Logic Validation (ResolvePeakHourModel)
- **Scope**: Verify peak hour resolution logic
- **Validation points**:
  - Returns empty string when disabled
  - Returns PeakHourModel when within window
  - Cross-midnight windows (22:00-06:00)
  - Timezone offset handling (UTC-based)
  - Boundary: start inclusive [start, end) exclusive
- **Method**: Run unit tests (T1 covers this), but verify specific test cases exist

### T3: API Validation
- **Scope**: API handler behavior for peak hour fields
- **Validation points**:
  - POST with peak_hour_enabled=true on non-internal → 400
  - PUT with peak_hour_enabled=true on non-internal → 400
  - GET round-trips all 5 peak hour fields
  - POST round-trips all 5 peak hour fields
  - PUT round-trips all 5 peak hour fields
- **Method**: Check existing integration tests, or spawn opencode to verify

### T4: Frontend Build
- **Scope**: TypeScript compilation and build
- **Command**: `cd pkg/ui/frontend && npm run build`
- **Expected**: Clean build, no errors

### T5: Database Migration Verification
- **Scope**: Migration 018 correctness
- **Validation points**:
  - SQLite migration file exists with 5 columns
  - PostgreSQL migration file exists with 5 columns
  - Migration registered in migrate.go
- **Method**: File verification (opencode session)

### T6: Build Verification
- **Scope**: Full project compilation + go vet
- **Commands**: `go build ./...` and `go vet ./...`
- **Expected**: Clean build, no vet warnings

---

## Execution Strategy

### Session 1 (Primary): Full Unit Tests + Build Verification
- Run `go test ./... -v -race -count=1`
- Run `go vet ./...`
- Run `go build ./...`
- Run frontend build
- Analyze all output, report detailed results

### Session 2 (Validation): API + Logic + Migration Deep Verification
- Verify test coverage for ResolvePeakHourModel (all edge cases from plan)
- Verify API validation tests exist and pass
- Verify migration files are correct
- Verify peak hour fields in query builder, store.go, server.go

### Session 3 (ensure.md): Quality Gate Validation
- Validate all ensure.md requirements
- Aggregate final results

---

## Success Criteria
- [ ] All Go unit tests pass (0 failures)
- [ ] No race conditions detected
- [ ] `go vet` clean
- [ ] `go build` succeeds
- [ ] Frontend build succeeds
- [ ] All ensure.md critical requirements pass
