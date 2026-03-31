# Testing Lesson: test_load.go Conflicts with cmd/main.go

**Date:** 2026-03-31
**Phase:** Phase 3 Frontend Visualization Testing

## Issue

When running `go build .` in the project root, the build picks up `test_load.go` instead of `cmd/main.go`.

Both files have:
- `package main`
- `func main()`

This causes a conflict because Go doesn't allow two files with `package main` and `func main()` in the same directory.

## Impact

- Running bare `go build .` fails
- Must use explicit path: `go build ./cmd/main.go` or `go build ./...`

## Workaround

Use explicit build paths:
```bash
go build ./cmd/main.go   # ✅ Works
go build ./...           # ✅ Works
go build .               # ❌ Conflicts with test_load.go
```

## Severity

Low — does not affect CI/CD pipelines that typically use `go build ./...`.

## Recommendation

Consider removing or renaming `test_load.go` to avoid confusion, or move it to a different directory.

## Files

- `cmd/main.go` — Main application entry point
- `test_load.go` — Test/load file (has duplicate main function)
