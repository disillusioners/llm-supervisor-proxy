# Quarantined Tests

## Active

| Test | Pack | Date Quarantined | Reason | Retry Budget | Attempts (P/F) | Status |
|------|------|------------------|--------|--------------|----------------|--------|
| TestStoreEngine_CloseLifecycle | store_unit_test (test/packs/store_unit_test.sh); also surfaces in `go test ./pkg/store/...` and `make test` | 2026-08-27 | macOS-only harness/cleanup race: Go testing framework's `t.TempDir()` RemoveAll hook fails with `unlinkat ... directory not empty` — engine janitor goroutine / SQLite WAL-shm residue outlives the test body. Test assertions NEVER failed (body 0.02s every run; failure site testing.go:1464 cleanup hook). Pre-existing at base df795c8 (branch feature/ultimate-model-trigger-schedule touched neither store_test.go nor engine-lifecycle code — confirmed via git log/diff). Surfaced during merge-gate run: slice `go test ./pkg/store/...` run-1 FAIL → run-2 PASS; `make test` same session PASS. | 3 | 2P/1F (plus gate slice 1P/1F, make-test 1P → aggregate 4P/2F) | QUARANTINED |
| TestValidateUpstreamURL/SSRF_octal_IP_should_be_rejected, _/SSRF_decimal_IP_should_be_rejected, _/SSRF_hex_IP_should_be_rejected | mcp_unit_test (test/packs/mcp_unit_test.sh); also `go test ./pkg/mcp/...` | 2026-09-22 | DETERMINISTIC env failure on ensemble-vm Linux + GOTOOLCHAIN=go1.26.0: ValidateUpstreamURL returns nil error for `http://0177.0.0.01/`, `http://2130706433/`, `http://0x7f000001/` (validation_test.go:200 wantErr). Byte-identical failure on base b832ea3f (separate-worktree proof 2026-09-22) → PRE-EXISTING, zero diff under pkg/mcp in fix/fe-api-payload-ram-incident. Suspected stdlib/toolchain parse-behavior delta (system go 1.22.2, forced GOTOOLCHAIN=go1.26.0 — download unavailable). Needs upstream root-cause; NOT flaky. | 3 | fix 1F + base 1F (deterministic, identical) | QUARANTINED (env-deterministic; skip-wiring deferred — repo was read-only during incident verification) |
| TestConcurrentIncrementModelUsage_Stress, TestConcurrentDifferentBuckets_Stress | misc_unit_test (pkg/usage slice); also `go test ./pkg/usage/...` | 2026-09-22 | SQLITE_BUSY timing flakes under concurrent stress (counter_stress_test.go:205/:495, "database is locked (5)"). Flake-confirmed on BOTH branches 2026-09-22: fix full-suite 1F → stress re-run 3×P; base b832ea3f full-suite P → stress re-run 2F/3. Pre-existing; zero diff under pkg/usage in fix/fe-api-payload-ram-incident; counters exact (9000/9000) when passing — no logic break. | 3 | fix agg 1P/1F + 3P; base agg 1P + 2F/3 | QUARANTINED (flaky; skip-wiring deferred) |

### Quarantine wiring note (pending follow-up)
- Pack-level skip (store_unit_test.sh / store slice) NOT yet wired — deliberately deferred: the quarantine was identified during the independent §8 merge-gate re-run at a0f4cd1, and modifying test code mid-gate would contaminate gate independence. Wire the skip + root-cause fix on mainline after merge.
- Root-cause fix candidates (test-side only): have the test wait for janitor quiescence after `mgr.Close()` (sync.WaitGroup join or bounded poll), and/or `PRAGMA wal_checkpoint(TRUNCATE)` before deferred cleanup; alternatively retry RemoveAll on macOS.
- Un-quarantine requires: fix applied + 3× clean single-test re-runs.

## Resolved (history)

| Test | Pack | Date Resolved | Fix | Confirming Runs |
|------|------|---------------|-----|-----------------|
| (none yet) | | | | |
