# LESSON: TestStoreEngine_CloseLifecycle — macOS TempDir cleanup race (pre-existing flake)

**Date:** 2026-08-27 (surfaced during §8 merge-gate run @ a0f4cd1; quarantined same day)

## Symptom
`go test ./pkg/store/...` intermittently FAILs the package with:
`testing.go:1464: TempDir RemoveAll cleanup: unlinkat /var/folders/.../T/TestStoreEngine_CloseLifecycle.../001: directory not empty`

Test body itself always completes in ~0.02s and its assertions NEVER fail — the failure is exclusively the Go testing framework's post-test `t.TempDir()` cleanup hook.

## Root cause (classification)
- `pkg/store/database/store_test.go:419` opens a SQLite store in `t.TempDir()`, constructs `ModelsManager` (owns a `credentiallb.Engine` with a background janitor goroutine), closes it, then the deferred cleanup RemoveAll's the temp dir.
- On macOS, `unlinkat` refuses to remove a non-empty directory; residual SQLite WAL/-shm sidecar files (or an fd still held by the not-yet-quiesced janitor) make the dir non-empty at cleanup time.
- `mgr.Close()` returns before the janitor goroutine has fully quiesced (no WaitGroup join on the test-visible path).

## Evidence
- Branch attribution: **PRE-EXISTING at base df795c8** — branch feature/ultimate-model-trigger-schedule touched neither store_test.go nor pkg/store/database/store.go (only database_test.go / mock_store_test.go, unrelated to lifecycle).
- Retry budget: 3× single-test → P/F/P (aggregate across session 4P/2F). Meets flaky definition (≥1 pass AND ≥1 fail, no code change).
- `make test` in the same session: PASSED (flake did not reproduce).

## Action taken
- QUARANTINED in QUARANTINE.md (first entry). Pack-skip wiring deliberately deferred to post-merge to preserve merge-gate independence at a0f4cd1.

## Fix candidates (test-side only, for post-merge)
1. Test waits for janitor quiescence after `mgr.Close()` (sync.WaitGroup join exposed by engine, or bounded poll on a quiescence signal).
2. `PRAGMA wal_checkpoint(TRUNCATE)` before the deferred cleanup.
3. Retry RemoveAll on macOS in the test's cleanup helper.

Un-quarantine gate: fix applied + 3× clean single-test runs.
