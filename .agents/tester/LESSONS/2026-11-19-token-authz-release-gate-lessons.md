# Token-authz release gate — durable lessons (2026-11-19)

Gate: AUTH COMMISSION `feature/token-authz-allowed-groups`, base `a3f0335`, gated tree `eb137a1` (= `81e8788` + test-only fix).

## 1. A contract narrowing can leave pre-existing assertions stale — and the stale ones hide in the Pack-B-only tier
`60d0db4` migrated two repo-root `test/` tests' SETUP to the commission's new conjunctive grant semantics
(`auth.FullPermission([]string{}, nil)` = a PRESENT-EMPTY models axis) but left the PRE-COMMISSION expectation
("empty ⇒ allow all"). The shipped contract (`pkg/authz/scope.go` `Resolve()`) is conjunctive: UNRESTRICTED requires
BOTH axes ABSENT (nil); a PRESENT-EMPTY axis ⇒ RESTRICTED granting nothing ⇒ 403.

Two lessons, both expensive if unlearned:

- **After any contract NARROWING, re-derive the pre-existing expectations, not just the new pins.** A migrated fixture
  is not a migrated assertion. The defect is invisible to every new pin (the commission's own
  `pkg/authz/scope_test.go` asserts the new contract and passed).
- **A green Pack A is NEVER a full-suite green.** The repo-root `test/` tier lives exclusively in Pack B (the
  module-prefix-anchored complement). Pack A = 9 packages (proxy/store/decision/ui/cmd) and does not compile the
  repo-root `test` package at all ⇒ the failure was first visible only in Pack B. Full-suite coverage = Pack A + Pack B.

## 2. Race budget: multi-package `go test -race` wall ≈ max(package), not sum(package)
The commissioned 1200–1500s budget for `pkg/store/... + pkg/proxy` was a sum-style projection. Measured wall for the
same pack: **317s** (`pkg/proxy` 298.021s, `pkg/store/database` 275.882s, `pkg/store` 1.062s — run concurrently).
The projection over-stated real cost ~4×. Budget races by the SLOWEST package plus compile headroom, not by the sum.

## 3. A quarantined third-party failure keeps the raw slice EXIT=1 — the verdict needs adjudication
`pkg/mcp`'s httpbin.org redirect pair fails deterministically (server-side 403 before the redirect classifier runs),
so Pack B's raw exit stays 1 forever. Report BOTH numbers: the raw exit and the adjudicated verdict (only the
quarantined pair failed ⇒ PASS-with-adjudication). Never present the adjudicated verdict as the raw exit code.

## 4. A vite build wipes the embed directory — serialize FE rebuilds against Go pack lanes
`pkg/ui/frontend/vite.config.ts` sets `outDir='../static'`; vite EMPTIES `pkg/ui/static` before writing. A concurrent
`go test` of `pkg/ui` / `cmd` compiles `//go:embed static/*` inside that empty window ⇒
`[setup failed] pattern static/*: no matching files found`. It reads like a code defect and is not one. Fix: serialize
the FE rebuild against Go pack lanes in the same worktree, or build to a scratch outDir and copy in. (POK
`gate-embed-race-serialize-fe-build`.)

## 5. Give each gate lane its own scratch directory
A shared `/tmp/gate-<sha>/` produced cross-lane log collisions and let one lane's build race another lane's compile.
Per-lane subdirectories (`/tmp/gate-<sha>/<lane>/`) remove the class. (POK `gate-scratch-dir-per-lane`.)

## 6. Ruling checks must match the ENFORCED predicate, not the prose
G12(b) ("no `len(…AllowedModels…)` outside `pkg/auth`") is enforced by the house guard
`pkg/proxy/authz_guard_test.go:109`, whose predicate is a length COMPARISON and whose positive controls bless three
bare `len()` uses that are allocation-capacity hints with no authorization decision (`pkg/authz/scope.go:93`,
`pkg/modelscache/tokens.go:144/:148`). A literal prose grep prints 3 false-positive lines on a CORRECT tree.
Write ruling checks count-free and comment-stripped, and bind them to the guard test that actually enforces them.

## 7. A frozen ledger can lag a parallel landing lane — pin the gated tree and grade the delta separately
The corpus was frozen at gen-8 while a parallel test-only lane landed 9 obligation pins that the ledger did not yet
name. The sweep must compute set arithmetic against the PINNED gated tree (`git diff <base> <gated-sha>`) and report
post-gate landings in a separate "pending" bucket (expected-absent), never as unlisted/absent failures.

## 8. Verify the premise before "fixing" a failing test
The two failing assertions were confirmed to carry a present-empty axis (not nil) BEFORE being edited — a nil-axis
failure would have been a PRODUCTION defect, not a stale test. The quick-fix lane was instructed to STOP and report
in that case; it did not need to.
