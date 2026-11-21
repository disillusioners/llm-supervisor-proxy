# imggen-groups gate lessons (2026-11-21, gate @ 42c04f8, base 5cf1ecdc)

Operational lessons from the independent full gate. All evidence: RESULTS/2026-11-21-imggen-groups-full-gate.md + /tmp/gate-imggen/ logs.

## 1. GNU `env` option-order trap (hit 4×, all self-corrected, zero result impact)

`env HOME=$(mktemp -d) -u DATABASE_URL go test …` exits 127 **before running anything**: GNU env stops option-parsing at the first `NAME=VALUE` and treats `-u` as the program name. Correct order: **`env -u DATABASE_URL HOME=$(mktemp -d) …`** (options first, assignments after). Symptom is unmistakable (`env: '-u': No such file or directory`) and distinct from a test failure — adjudicate exit-127-with-this-message as an invocation error, never as a test result. Fix landed in later dispatches this gate; keep the corrected order in every future template.

## 2. `-race` is ≈6× plain on this host — heavy race packs must PRE-SPLIT

Measured this gate: store/database plain 53.6s → race >240s (inner cap fired at 240.116s); pkg/proxy plain 145.4s → race ~298s structural (prior precedent 298.021s), inner 240s fired at 240.111s. The single-invocation `240s-inner/300s-outer` envelope **cannot** complete either package under `-race` here. The dispatcher-side "race 2–3×" prior was wrong.

**Proven recipe (used twice, exact both times) — the complement-tail split:**
1. Slice 1: full `-race -v` run under the standard envelope; when the inner cap fires, the log still holds every completed result (a result = a `^--- (PASS|FAIL|SKIP):` line; `=== RUN` alone is NOT a result — the killed test records none).
2. Universe: `go test -race <pkg> -list '.*'` (note: `-list` **ignores** `-run`; call out build-tagged exclusions like `pg_integration`).
3. Complement = universe − recorded(slice-1); run it as ONE anchored alternation `-run '^(A|B|…)$'` under the same envelope.
4. Union verdict: recorded(s1) ∪ recorded(tail) == universe, `comm -3` empty; races/FAILs summed across BOTH logs.
Both splits closed at 231/231 and 644/644 with zero races and zero assertion failures — the kills were budget, never defects. Future gates: pre-split store/database and proxy race packs instead of burning a 240s slice-1, OR negotiate a documented release-gate race budget like the 2026-11-08 Pack A precedent — but the 5-min outer hard cap stays.

## 3. Wall-clock-sensitive tests under parallel gate load

`TestServeImageGenGroup_RotatingMember_RespectsWalkBudget` (150ms/220ms thresholds, ~70ms slack; registered CI-watch candidate) passed in ALL three of its executions this gate (Pack B pack-context 0.11s, `-race` imggen 0.14s) — including while up to five go-test workers ran concurrently. Practical mitigation used: exclude it from the targeted pin battery when it is already covered by concurrently-running packs (cap parallel executions of the same wall-clock test at ~2-3); re-run isolated once if it ever fires (per phase4-plan §4).

## 4. Census/anti-vacuous method notes (hardened this gate)

- **(package, name) attribution is mandatory**: 16 top-level test names legitimately exist in TWO packages (e.g. `TestPackage_ImportGuard_NoProxyImports` in decision+imggen; `TestTokenize` in loopdetection+fingerprint). A name-only grep would fabricate DUP/false-MISS; attribute every `=== RUN` line to the `ok/FAIL <pkg>` summary that terminates its block.
- Grep forms that work: `^=== RUN   NAME$` (three spaces), `^--- PASS: NAME( |$)` (duration suffix alternation) — anchors exclude `Name/Sub` subtests.
- FE token census: count only `it()` titles; `describe()` headers prefix every nested test (multiplicity = suite size) and must NOT be asserted as ×1. Cross-run the census against an independent second vitest run.
- `go test -list` ignores `-run`; `-v` logs are the census substrate (this gate ran Pack A/B with `-v` precisely so G7 arithmetic keys off the gate's own logs).

## 5. Bookkeeping drift

- KV claim-row commit counts go stale vs the final tip ("20" vs actual 21): trust `git rev-list --count <base>..<tip>` at gate time.
- POK `gate`/`verify` families hit the 8-active-entry bound mid-gate (worker writes refused, by design); the durable copies live in this LESSONS file + the RESULTS gate record (anomaly 48d600e3 filed by a worker for the curator).
- G8's shorthand touchpoint list (4 config.go locations) under-counts the DECLARED shape: decisions.md D-17 enumerates 5 implementers (incl. the `*config.Manager` getter impl) — adjudicate fences against the D-ruled declaration, not the gate-table shorthand.
