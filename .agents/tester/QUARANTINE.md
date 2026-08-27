# Quarantined Tests

## Active

| Test | Pack | Date Quarantined | Reason | Retry Budget | Attempts (P/F) | Status |
|------|------|------------------|--------|--------------|----------------|--------|
| TestStoreEngine_CloseLifecycle | store_unit_test (test/packs/store_unit_test.sh); also surfaces in `go test ./pkg/store/...` and `make test` | 2026-08-27 | macOS-only harness/cleanup race: Go testing framework's `t.TempDir()` RemoveAll hook fails with `unlinkat ... directory not empty` — engine janitor goroutine / SQLite WAL-shm residue outlives the test body. Test assertions NEVER failed (body 0.02s every run; failure site testing.go:1464 cleanup hook). Pre-existing at base df795c8 (branch feature/ultimate-model-trigger-schedule touched neither store_test.go nor engine-lifecycle code — confirmed via git log/diff). Surfaced during merge-gate run: slice `go test ./pkg/store/...` run-1 FAIL → run-2 PASS; `make test` same session PASS. | 3 | 2P/1F (plus gate slice 1P/1F, make-test 1P → aggregate 4P/2F) | QUARANTINED |

### Quarantine wiring note (pending follow-up)
- Pack-level skip (store_unit_test.sh / store slice) NOT yet wired — deliberately deferred: the quarantine was identified during the independent §8 merge-gate re-run at a0f4cd1, and modifying test code mid-gate would contaminate gate independence. Wire the skip + root-cause fix on mainline after merge.
- Root-cause fix candidates (test-side only): have the test wait for janitor quiescence after `mgr.Close()` (sync.WaitGroup join or bounded poll), and/or `PRAGMA wal_checkpoint(TRUNCATE)` before deferred cleanup; alternatively retry RemoveAll on macOS.
- Un-quarantine requires: fix applied + 3× clean single-test re-runs.

## Resolved (history)

| Test | Pack | Date Resolved | Fix | Confirming Runs |
|------|------|---------------|-----|-----------------|
| (none yet) | | | | |
