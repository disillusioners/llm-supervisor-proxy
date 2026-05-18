# Race Condition Fixes in Existing Tests

**Date**: 2026-05-18
**Commits**: `2f15b2c`, `02f1b84`
**Session**: build-regression (`ses_1c51cc72bffeI7uTP5fOGiH8yj`)

## Issue
When running `go test ./... -race`, two existing test functions in `pkg/proxy/race_executor_test.go` had race conditions:

### Race 1: TestHeartbeat_StartsBeforeWaitForWinner
- **Cause**: Test reads `ResponseRecorder.Body.String()` while handler goroutine concurrently writes to it
- **Fix**: Created `safeResponseWriter` wrapper with `sync.Mutex` around `httptest.ResponseRecorder`
- **Commit**: `2f15b2c`

### Race 2: TestRaceScenario_FallbackWins  
- **Cause**: `callCount` int variable accessed from multiple goroutines (mock server handler + test assertions)
- **Fix**: Changed from `int` to `atomic.Int64`
- **Commit**: `02f1b84`

## Lesson
When tests involve goroutines (HTTP mock servers, concurrent handlers), always use synchronization primitives:
- `sync.Mutex` for shared state that needs complex access
- `atomic.Int64` / `atomic.Int32` for simple counters
- `sync.WaitGroup` for coordination
- Run tests regularly with `-race` flag
