# 2026-08-21 — minimax_interleaved_matrix: vacuous PASS from `\|` regex quoting

## Root cause

The pack `minimax_interleaved_matrix` was registered in PACKS.md as:

```
go test ./pkg/proxy/ ./pkg/ultimatemodel/ -run 'Interleaved\|MiniMax\|Reasoning' -count=1
```

The `\|` alternation idiom is **grep BRE / markdown-table escaping**, not Go RE2 syntax. Inside the single quotes, `go test` receives the literal characters `\|`, which RE2 compiles as an **escaped literal pipe** — i.e. the pattern matches the literal string `Interleaved|MiniMax|Reasoning`. No test name contains that string, so the run selects **zero tests** and exits 0 with `[no tests to run]` — a **vacuous PASS**.

The `\|` form only ever worked **unquoted** in bash, where the shell strips the backslash before `go test` sees the argument. The registered (single-quoted) form never ran the intended matrix. The last recorded "PASS 32/32 @ ee590c1" (2026-08-19) could not have been produced by the registered command as written; it must have been run with working alternation (unquoted or plain `|`).

## How it was detected (2026-08-21, HEAD `355f06c` = db7aca0 + 2 test commits)

1. Ran the registered command verbatim (wrapped in `timeout 300`, go `-timeout 280s`): exit 0, both packages `[no tests to run]`, 2s wall. Exit code alone looked like PASS — only the stderr line revealed zero tests ran.
2. `go test -list 'Interleaved\|MiniMax\|Reasoning'` → **empty** (proves pattern matches nothing).
3. `go test -list 'Interleaved|MiniMax|Reasoning'` → 17 funcs in `pkg/proxy` + 17 in `pkg/ultimatemodel` = **34 test functions** (the intended matrix; note count grew from 32 since `ee590c1` due to later test-only commits).

## Fix (harness-only, no production/test-code changes)

- PACKS.md `minimax_interleaved_matrix` row updated: corrected command in a fenced code block (safe from markdown table escaping), run record refreshed (2026-08-21, PASS 34/34 test funcs @ `355f06c`), pointer to this LESSONS file.
- Correct runnable form:

```bash
timeout 300 go test ./pkg/proxy/ ./pkg/ultimatemodel/ -run 'Interleaved|MiniMax|Reasoning' -count=1 -timeout 280s
```

## Verified result (after fix)

- `RESULT: PASS` — exit 0, wall 2s. 34/34 test functions PASS (46 `--- PASS` entries incl. 12 subtests), 0 FAIL, 0 SKIP.
- Byte-identity constraint (client-received bytes identical to `fea5874` behavior) asserted green by the negative matrix on branch `fix/ui-reasoning-observability`.

## Lesson (generalizable)

1. **Exit 0 is not proof of execution.** A `-run` regex that matches nothing still exits 0. Always check for `[no tests to run]` / compare the executed count against the expected pack size (e.g. via `-v` + `grep -c -- '--- PASS'`).
2. **Never copy `\|` alternation into Go `-run` patterns.** Go uses RE2: write plain `|` alternation. The `\|` idiom belongs to grep BRE and to markdown table cells; a fenced code block avoids the escaping trap entirely.
3. **Registered-pack commands must be verbatim-runnable.** When a pack command lives in a markdown table, prefer a fenced code block for the exact command; otherwise quoting/escaping drift makes the registration lie about what it runs.
