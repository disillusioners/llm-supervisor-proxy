# Lesson: behavior-contract fixes break e2e harnesses that pin the old contract — audit quadrants, not just code

Date: 2026-09-26
Refs: fix/minimax-reasoning-translation-gate @ 6829d98; test commit cc5e15c; RESULTS/2026-09-26-minimax-reasoning-gate-verification.md

## What happened
6829d98 widened the MiniMax translation gate from "client header present" to "(header || non-empty reasoning_content) && provider is MiniMax" — an intentional behavior-contract change at 6 sites. The developer updated 13 files including 20 new unit matrix tests, but did NOT update `test/e2e_minimax_reasoning/e2e_minimax_reasoning_test.go`, whose `TestS8_FlagAbsent_Quadrant` still asserted the OLD contract ("flag absent ⇒ translator fully inert") on exactly the 3 changed paths. The 4th subtest (race-external, an UNCHANGED path) kept passing — a perfect 1:1 fingerprint of the change set.

## Why it looked like a regression but wasn't
- Failures were deterministic wire-format assertions (translation markers present where verbatim passthrough was expected), NOT crashes or environmental issues.
- The failing fixtures carried non-empty `reasoning_content` with the header absent — under the NEW intended contract they MUST translate. The tests were asserting the bug.

## The diagnostic fingerprint (reusable)
When a contract-widening fix lands, a stale-contract test failure shows ALL of:
1. Failing tests cluster on CHANGED code paths; tests on unchanged sibling paths still pass.
2. Failure diffs show the NEW intended behavior (e.g. `reasoning_split:true` "unexpectedly" on the wire).
3. The failing expectation text describes the OLD rule verbatim.

## Fix pattern
Swap the stale assertion helper for the new-contract helper (`assertUntouchedUpstreamRequest` → `assertTranslatedUpstreamRequest`) on the affected subtests ONLY; leave orthogonal-gate assertions (non-MiniMax, no-credential) untouched — they encode different, still-valid invariants. Add a one-line contract-change comment citing the fix sha and incident. Audit ALL sibling uses of the old helper before editing: 9 of 12 `assertUntouchedUpstreamRequest` sites were still correct.

## Process rule going forward
Any PR that intentionally changes a wire/behavior contract MUST include, in its own diff, updates to every e2e quadrant test pinning the old contract — grep the test tree for the old-contract assertion helper. "Developer ran affected packages green" is insufficient when the affected-package tests are new but the harness tree is stale: run the FULL ./... sweep (as packs) on contract-change PRs.
