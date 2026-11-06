# Sticky last-success gate (2026-11-06) — lessons & gotchas

Gate: feature/sticky-last-success-ordering (fee32a8 + gate commit 5f75216), base 63df316. Full report: `RESULTS/2026-11-06-sticky-last-success-ordering-gate.md`.

## 1. httpbin.org policy change → 2 deterministic pkg/mcp failures (attribution method that cleared the feature)

`TestHandleTestMCPServerDirect_RedirectToLocalhostBlocked` / `_RedirectToPrivateIPBlocked` now fail DETERMINISTICALLY: httpbin.org returns server-side `403 Forbidden redirect target: redirect target is an internal address` (text/plain) for `redirect-to?url=<internal>` — the 403 lands BEFORE the proxy's CheckRedirect classifier, so the tests' "error mentions redirect" assertions fail. `httpbin.org/status/200` still 200 ⇒ policy, not outage.

**Attribution method (reusable):** scratch detached worktree of the base (`git -C <wt> worktree add --detach /tmp/<base> <sha>`) → run the failing package on BOTH sides under full env discipline → diff byte-stripped assertion text + sorted set-diff of all RUN/PASS/FAIL name lines → `git rev-parse <rev>:<pkg>` to prove the package tree is bit-identical. 2 runs per side to rule out a flake window. Remove the scratch worktree after. Both tests are now QUARANTINE.md rows (skip-wiring deferred).

## 2. Boot-time PORT env needs APPLY_ENV_OVERRIDES=1 (smoke-test gotcha)

`PORT=18234 ./lsp` does NOT bind 18234: `applyEnvOverrides` (pkg/config/config.go:463-476) only reads PORT when `APPLY_ENV_OVERRIDES` is set. Correct smoke launch: `env -u PORT -u DATABASE_URL APPLY_ENV_OVERRIDES=1 PORT=<p> HOME=<tmp> ./lsp`. (Inverted implication of the daemon-env hazard: test processes LEAK PORT=9797 INTO the binary unless `-u PORT`; the binary IGNORES PORT unless the override flag is set.)

## 3. FE bundle-content assertions: check the LAZY chunk, not the index chunk

SettingsPage is lazy-loaded — a feature string (e.g. `sticky_last_success_enabled`) lives in `SettingsPage-<hash>.js`, NOT in `index-<hash>.js`. Grepping only the index chunk produces a false "FE not wired" alarm. Correct check: `git diff --name-only <base>..HEAD -- pkg/ui/static/assets/` → find the changed chunk → grep THAT file (old vs new chunk via `git show <base>:<old-chunk>` proves the string is new).

## 4. pkg/proxy test-authoring seams (from the scenario pack, commit 5f75216)

- `race_started` event capture is a CHANNEL: `waitRaceStartedModels` counts events CONSUMED — after consuming request 1's event, the next wait must not double-count (n=1, not cumulative).
- `[STICKY]`/`[RACE]` log lines emitted inside `HandleChatCompletions` do NOT reach stderr under `log` capture; echo them as `EVIDENCE` lines via `t.Logf` for the report trail.
- Deterministic sequential race burn: `RaceParallelOnIdle=false` (no idle spawns) + `RaceMaxParallel=3` (failure-spawn reachable) + single credential per model (cred-failover ineligible ⇒ chain advances directly). This reproduces the live incident's A→B→C attempt walk.
- Short-TTL scenario without production changes: `stickyorder.NewEngine(150ms, 50ms)` injected as `NewHandler`'s 8th arg — TTL is a constructor param, NOT config.

## 5. Worktree FE testing: node_modules may already exist

The sticky worktree already had a real `pkg/ui/frontend/node_modules` (dev ran tests there) — check BEFORE symlinking from the main checkout. Symlink is still the fallback for fresh worktrees.

## 6. Wave discipline for timing-sensitive stages

Measurement stages (wall-clock per attempt) must run AFTER compute-heavy packs finish — concurrent `go test ./...` runs inflate latencies. Splitting the gate into wave 1 (regression/static/smoke) + wave 2 (scenario measurements, alone) kept Stage-2 numbers clean (±1% run-to-run).
