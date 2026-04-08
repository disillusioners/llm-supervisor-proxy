# Quick Fix: Normalizers Registry Config Test Race Condition

**Date**: 2026-04-08
**File**: `pkg/proxy/normalizers/registry_config_test.go`
**Commit**: `972dd01`
**Branch**: fix/memory-traps

## Issue
The normalizers test had mock counter fields using plain `int` type with non-atomic increment (`counter++`). When running with `-race`, this caused a data race because the counters were accessed concurrently by test goroutines and the mock functions.

## Root Cause
Pre-existing issue — not caused by memory optimization phases. The test mock functions incremented counters without synchronization, and the race detector caught it during the full integration test.

## Fix
- Changed mock counter fields from `int` to `int64`
- Used `atomic.AddInt64()` for all increments
- Added `sync/atomic` import
- 1 file changed, 5 insertions, 4 deletions

## Lesson
When running tests with `-race`, always ensure shared mutable state uses atomic operations or mutexes, even in test code. The race detector is thorough and catches test-only races too.
