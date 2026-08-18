# Test 8 "Retry Limit Exhausted" — Deterministic Harness Failure (Pre-Existing) + Exit-Code Masking

**Date:** 2026-08-18
**Pack:** `ultimate_model_shell_mock` (`test/test_mock_ultimate_model.sh`, ports 4001/4322)
**Context:** Surfaced during verification of commit 83814b0 (reasoning_content ultimate-internal fix).

## Symptom
- Check at script line 406: `Third request (retry 2/2): Error from ultimate model` — expected `error` in client response, got empty body.
- Proxy logged: `Error executing with quick: provider error: context canceled` ~5s after the request.
- 47/48 checks passed; all reasoning-content checks green.

## Triage (retry budget 3×, no code change)
| Run | Exit | Passed/Failed | Test 8 |
|---|---|---|---|
| 1 | 1 (masked as 0 pre-fix) | 47/1 | FAIL |
| 2 | 1 | 47/1 | FAIL |
| 3 | 1 | 47/1 | FAIL |

Verdict: **DETERMINISTIC, not flaky** → no quarantine (quarantine is for pass/fail mixes only).

## Root cause
Timeout mismatch in the harness:
- Proxy env: `MAX_GENERATION_TIME=20s` (script line 121)
- Test 8's four requests: `curl --max-time 5` (lines 376-416)

Curl disconnects at exactly 5s, mid-error-path; the proxy's request context is canceled
before it writes the error body → empty client response → assertion never sees `error`.

## Pre-existence evidence
- Test 8 added in `f3e4205` (2026-03-18), ancestor of baseline 8233dbc.
- `git diff 83814b0^ 83814b0 -- test/test_mock_ultimate_model.sh` is EMPTY — commit 83814b0
  did not modify this script (commit message implied it; it didn't).
- **Not a regression signal for the verified fix.**

## Fixes applied
1. **Exit-code masking** (commit `2f67976`): the EXIT trap's `cleanup_all` ended with
   `wait 2>/dev/null || true`, whose success overwrote the intended `exit 1`. Fixed by
   capturing `$?` on entry and returning it. Post-fix, exit code honestly reflects summary.
2. **Test 8 curl timeout** (see RESULTS 2026-08-18 report for final state): raise Test 8's
   `--max-time` above MAX_GENERATION_TIME so curl outlives the proxy error path.

## Lesson
- Error-path assertions need client timeouts strictly greater than the server-side
  generation-time budget; otherwise the client cancel IS the failure it's testing for.
- Shell EXIT traps that end in `|| true` mask exit codes — always capture/restore `$?`.
