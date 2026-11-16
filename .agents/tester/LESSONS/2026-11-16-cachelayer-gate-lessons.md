# Lessons — cachelayer ship gate (wi-4d2c8e17, 2026-11-16)

Durable test-craft lessons from the fix/cache-layer-separation gate @ 30e3ec4 (base 24c51ef).

## 1. The `-run` false-green trap: "no tests to run" exits 0
A design-doc test name that differs from the real test name (`TestCacheLayer_Structural_NoCacheImportsInGroupPick` vs the real `TestCacheLayer_Structural_PackageLevelNoCacheImports`) makes `go test -run '<wrong>'` print `testing: warning: no tests to run` / `PASS` / `ok ... [no tests to run]` and **exit 0** — a silent FALSE GREEN. Rule: any targeted `-run` must be verified by a literal `--- PASS:`/`--- FAIL:` line for the expected test in the output; `[no tests to run]` + exit 0 = verdict FAIL. Always enumerate real test names from `git diff <base>..<tip> -- '*_test.go' | grep -E '^\+func Test'`, never from prose/design docs. (Caught pre-emptively by dispatcher correction; independently confirmed by the pin worker's counter-run.)

## 2. Executed RED for a test file coupled to branch-only production symbols
A NEW test file that references branch-only production API (here: `ModelSkipCache` field + `preferUnmarkedUpstreamCredential`) cannot compile on the base tree, so naive RED-checks yield only compile-class evidence. Surgical technique that produced executed REDs WITHOUT faking anything:
1. Scratch base via `git archive <base>` (never `cp -a` a worktree).
2. Excise branch-only wiring from the shared TEST harness helper (one line; base-faithful because base has no consumer of the symbol).
3. Park every test that directly references branch-only symbols in a sibling file behind a `//go:build <excised>` tag (verbatim move).
4. Watch for `type` declarations interleaved between parked tests and their methods — a `^func`-based map misses them; let the compiler arbitrate the final set.
5. PROVE integrity: `cmp` every surviving test function body against the original file — all must be byte-identical, and the only function-level diff must be the excised harness line.
Label the result honestly ("executed RED on minimally-excised scratch copy") and note which tests PASS on base (regression-guards, not RED pins — here T_C/T_D/GroupPick_Stable).

## 3. `make test` coverage map (llm-supervisor-proxy)
`make test` = 3 import guards (imggen↛proxy, decision↛proxy, decision↛imggen) + `go test ./...`. It runs **NO** frontend build/vitest/tsc and **NO** `go vet`. Any gate that claims "make test green = fully gated" must add the FE gate (tsc parity + vitest + vite-build zero-mod) and the build_gate pack. Also: make exits **2** (recipe failure) when go test returns 1 — the quarantined httpbin pair makes `make test` non-zero on EVERY branch by construction; adjudicate against QUARANTINE.md (house convention since 2026-11-06, 4 prior gates) and prove branch-orthogonality with `git rev-parse <base>:pkg/mcp == <tip>:pkg/mcp`.

## 4. Structural pins: prove non-vacuity, know their reach
The AST import pin (`parser.ImportsOnly` + recursive WalkDir, `_test.go` excluded, fail-closed on parse errors) catches blank/named/aliased imports, new subpackages, and build-tagged files (4/4 mutation catches; comment-only counter-probe stays green). But it is a **one-hop** pin: transitive reachability (a pinned package importing a package that imports the cache) is NOT enforced, and matching is exact-string on two import paths. Today's import closures are clean; treat "impossible by construction" as temporal, and re-verify closures when adding imports to pkg/credentiallb / pkg/models / anything in store/database's dep set. Follow-up ticket material, not a gate blocker.

## 5. Anti-vacuous is cheap when built into the pack run
Verbose Pack A run + `git diff <base>..<tip> -- '*_test.go'` enumeration = per-test literal `--- PASS` table for free (37 added funcs this gate, 29 Pack-A-scoped + 8 modelcache). Remember: packages added by the branch land in the Pack B complement (pkg/modelcache here) — scope the enumeration per slice.
