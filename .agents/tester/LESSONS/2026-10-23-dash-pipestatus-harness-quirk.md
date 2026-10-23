# Worker bash tool runs /bin/sh (dash) — ${PIPESTATUS[0]} fails after pack completion

**Date:** 2026-10-23 · **Context:** auto-build-version functional gate (wt feature/auto-build-version @ e12c52b)

## Symptom
Two of four Go-suite workers (G1, G3) wrapped their pack as `cmd | tee log; echo ${PIPESTATUS[0]}`.
The worker bash tool executes multi-line scripts under `/bin/sh` (dash on this VM), which has no
`PIPESTATUS` array — the trailing echo fails with `Bad substitution` AFTER the pack already completed.
The pack result itself was unaffected, but the exit-code capture line was lost.

## Impact
Reporting-harness only — zero test impact. Verdicts were adjudicated from the tee'd full-log
signature (complete all-`ok` package matrix, 0 `FAIL`/`panic`/`DATA RACE`/build-error lines),
which is sufficient evidence per the test-pack skill's evidence-survival rule.

## Fix pattern (use in future pack dispatches)
- Wrap multi-line pack bodies in an explicit bash heredoc when using bash-isms:
  `bash <<'EOF' … EOF`
- Or avoid `PIPESTATUS` entirely: `set -o pipefail` + plain exit, or `go test … > log 2>&1; echo $?`,
  or capture `RC=$?` immediately after the bare command before any pipe.
- A third worker (gate-guards) hit the same dash issue on `pipefail` and recovered in one step
  using the test-pack-execution skill's portability rule — the skill already documents this;
  dispatchers should keep the heredoc hint in pack messages that use pipes/bash-isms.
