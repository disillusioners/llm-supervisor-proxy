# Lesson: Empirical baseline runs beat static shim analysis — scoped source-only stash pattern

**Date**: 2026-09-22 · **Context**: FE scroll fix gate (`fix/fe-scroll-long-last-message` @ 7022be1, uncommitted)

## What happened

Static analysis predicted the symptom-2 headline test (`RequestDetail.test.tsx:363` initial-anchor-bottom) would **pass** on pre-fix source because "the reviewer-added layout shim clamps scrollTop before the hook reads it". The empirical baseline run **disproved it**: the test fails on pre-fix with `expected +0 to be 10000` — the pre-fix component never anchors scrollTop at all in that scenario. The masking argument was about the wrong seam.

Two OTHER predictions from the same static analysis were empirically confirmed (the `:392` overshoot test IS masked by the shim clamp; the `:439` jump-to-last test IS masked by its own manual `dispatchEvent`). So static analysis had a 3/4 hit rate — good for triage, insufficient for gate evidence.

## The pattern that worked (reuse for any pre-commit regression-catcher proof)

```bash
# 1. Stash ONLY the source files; keep the NEW test files
git stash push -m "tester-gate-baseline-check" -- <source files>
# 2. Run the named tests — failures here = tests genuinely catch the bug
npx vitest run <test file> -t "<filter>"
# 3. Pop IMMEDIATELY (nothing in between)
git stash pop
# 4. Re-run the full suite to confirm restoration
```

Why source-only stash beats full `git stash`: a full stash reverts the new tests too, so the demonstration degrades to "test not-exist on baseline" (weak). Keeping post-fix tests against pre-fix source turns each test into a live bug-detector — the strong form.

## Operational notes

- Verify the expected `git status --porcelain` shape at EVERY step; STOP on deviation; single push/pop only; never `checkout/restore/reset` to "recover".
- Baseline runs EXPECT exit 1 from vitest — that's success evidence, not task failure. State this explicitly in the worker dispatch or the worker will treat it as a gate failure.
- Exclusive window required: no concurrent suite/build runs while the tree is mutated.

## Related findings from the same gate

1. `npm run build` in this repo = `vite build` only (esbuild strips types, no checking). The ensure.md "no TypeScript errors" clause needs a separate `npx tsc --noEmit` leg (30 standing baseline errors across unrelated files — documented debt; classify new-debt vs baseline, never fail the gate on pre-existing).
2. `pkg/ui/static/` build outputs are TRACKED (re-tracked by `afe93ad`, 2026-09-22: "fresh clone ships working UI"). Every FE build = 3D+1M+3-untracked hash churn that must be consciously committed or reverted. Prior convention (pre-afe93ad) was ignore+untrack — check the convention date before assuming.
3. Prediction-vs-actual deviations in baseline runs are high-value signals — always have the worker record predicted vs actual and flag deviations back for adjudication (here it upgraded symptom-2 coverage from "no catcher" to "2 catchers").
