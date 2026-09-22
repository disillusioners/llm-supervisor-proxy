# FE Scroll Fix Verification Gate — RequestDetail windowed list

- **Date**: 2026-09-22 16:15–16:40 UTC
- **Branch**: `fix/fe-scroll-long-last-message` @ HEAD `7022be1` (fix UNCOMMITTED — gate ran pre-commit by design)
- **Change set**: 4 files, +722/−53 (`pkg/ui/frontend/src/utils/windowing.ts`, `pkg/ui/frontend/src/components/RequestDetail.tsx`, + both test files)
- **Verdict**: ✅ **PASS** — all gate legs green; 3 decision notes for the committer (see §7)
- **Workers**: fe-suite-gate e1402a4f (test-pack-execution) · fe-build-gate 139e517c · fe-map-gate 9e77a34f · fe-stash-gate c705f90e (wave 2, exclusive)

## 1. Full suite (fresh, ×2)

| Run | When | Result | Duration | Exit |
|---|---|---|---|---|
| Pre-build (worker A) | 16:20:33 | **52/52 passed** (36 windowing @38ms + 16 component @533ms), 0 failed/skipped | 2.95s | 0 |
| Post-stash-pop (worker C) | after baseline dance | **52/52 passed** | 3.19s | 0 |

Vitest 3.2.7, 2 test files. Zero anomalies (no act() warnings, no unhandled rejections). Matches expected 52/52 exactly (baseline before fix: 30).

## 2. Production build

- Command: `cd pkg/ui/frontend && timeout 300 npm run build` → script is **`vite build` ONLY** (no tsc)
- Result: **SUCCESS**, exit 0, 3.87s, 49 modules transformed. One informational warning (Browserslist/caniuse-lite 7 months old — pre-existing, unrelated)
- Output lands in `pkg/ui/static/` (vite.config `outDir: '../static'`, `emptyOutDir: true`, `base: '/ui/'`); Go embeds it via `//go:embed static/*` (pkg/ui/server.go:77) — new code ships on next `go build`

## 3. TypeScript gate leg (added — build script does not type-check)

- `timeout 240 npx tsc --noEmit` → exit 2, **30 errors, ALL pre-existing** in unrelated files (ConfigModal, EventLog, SettingsPage, config/* tabs, mcp/MCPServersTab, usage charts, utils/helpers)
- **New-debt errors referencing the 4 changed files: 0** ✅
- ensure.md Critical "Frontend builds successfully without TypeScript errors": **PASS scoped to change set** (0 new TS errors; build succeeds). The literal full-repo reading fails on documented baseline debt (PACKS.md 2026-08-28 note: "30 standing tsc errors are known baseline debt") — unchanged by this fix

## 4. Symptom-closure mapping (empirically verified via scoped stash)

Method: `git stash push -- <2 SOURCE files only>` (test files stay post-fix) → run named tests against pre-fix source → `git stash pop` immediately. Failures on pre-fix = the tests genuinely catch the original bugs.

### Symptom 1 — long LAST message unreachable, jumps back (overshoot → empty slice → collapse → clamp loop)

| Test (`windowing.test.ts`) | Pre-fix failure (verbatim) |
|---|---|
| :158 `clamps start to total when scrollTop is far past the estimated extent` | `expected 45448 to be less than or equal to 800` |
| :165 `returns a non-empty tail-anchored window when scrollTop is far past the estimated extent` | `expected -44648 to be greater than 0` |
| :176 `preserves the documented invariant 0 <= start <= end <= total for all sane inputs` | `at scrollTop=220000: expected 994 to be less than or equal to 800` |
| :297 `clamps to a tail window when raw scrollTop overshoots far past the measured total` | `TypeError: buildOffsets is not a function` |

**Run result: 4/4 catchers FAIL on pre-fix** (exit 1, 2.13s) — symptom 1 closure empirically proven.

### Symptom 2 — initial load lands MIDDLE of tall last message (one-shot anchor, no re-anchor)

| Test (`RequestDetail.test.tsx`) | Pre-fix failure (verbatim) |
|---|---|
| :363 `initial anchor on detail change pins scrollTop to the visible bottom (symptom 2 fix)` | `expected +0 to be 10000` (pre-fix never anchors: scrollTop stays 0) |
| :350 `disables native overflow-anchor on the scroll container` | `expected '' to be 'none'` (third leg of fix) |

**Run result: 2 FAIL / 3 PASS on pre-fix** (exit 1, 2.70s). The initial-anchor headline test **IS a genuine catcher** — it fails on pre-fix source (scrollTop never set).

⚠️ **Prediction deviation (resolved in favor of evidence)**: static analysis (map worker) predicted the initial-anchor test would PASS on pre-fix ("reviewer shim masks it"). The empirical stash run disproved this — the test fails with `expected +0 to be 10000`. Lesson recorded in LESSONS/. Conversely, two other predictions were CONFIRMED empirically: `extreme scrollTop overshoot (symptom 1 regression)` (:392) and `clicking jump-to-last` (:439) DO pass on pre-fix (shim clamp / self-dispatched scroll event mask them — see §6 recommendations).

## 5. Vacuity check (both headline tests non-tautological)

- **Initial-anchor-bottom** (RequestDetail.test.tsx:363-390): asserts live DOM `scrollTop === scrollHeight − clientHeight` derived from production constants — behavioral observable, fails if anchor never fires (proven: fails pre-fix). **NOT tautological.**
- **Stick-to-bottom/no-jump-back** (RequestDetail.test.tsx:392-437): asserts live `scrollTop` + parses rendered DOM text `"Showing X–Y of 800 messages"` with numeric bounds on the window — **NOT tautological** (but currently masked on pre-fix by the shim clamp; guard, not catcher — see §6).

## 6. Test-quality findings (non-blocking recommendations)

1. 🟠 `:392` extreme-overshoot component test is masked by the reviewer-added `installLayoutShim` clamping setter — passes on pre-fix. Recommend raw `Object.defineProperty` scrollTop for this test so it becomes a true catcher (unit-level windowing tests already cover the clamp).
2. 🟠 `:439` jump-to-last test dispatches its own `'scroll'` event (line 465), masking the new synthetic-scroll dispatch in `jumpToMessage` — passes on pre-fix. Recommend deleting the manual dispatchEvent.
3. 🟢 Symptom-2's production scenario (late height materialization via ResizeObserver) is structurally untestable in happy-dom — see §8. Recommend a future synthetic-ResizeObserver-entry test if a seam is added.

## 7. Build-artifact tracking — COMMITTER DECISION REQUIRED

- `pkg/ui/static/` **IS tracked** (convention re-established by `afe93ad` 2026-09-22: "bundles re-tracked (fresh clone ships working UI)"; `.gitignore` no longer covers it)
- The gate's build mutated the tree: **3 D + 1 M + 3 untracked** under `pkg/ui/static/` (Vite hash churn: `emptyOutDir` deletes old bundles, new hashes arrive untracked)
- Options: **(A)** include the 7 artifact changes in the commit (project convention — fresh clones ship a working UI); **(B)** source-only commit + `git checkout HEAD -- pkg/ui/static/` (must be explicit; next build would re-churn)
- The gate intentionally left the churn in place (read-only mandate; no cleaning)

## 8. Headless limits → manual smoke checklist

Untestable in vitest+happy-dom: real browser scrollTop clamping (fractional/inertia/momentum), real layout heights (`getBoundingClientRect` = 0), **ResizeObserver firing on real late materialization (symptom-2's production trigger)**, native `overflow-anchor` effect, scrollbar behavior, reflow/paint cost.

Manual smoke: (1) 800-message convo, tall last message — scroll down reaches bottom, no jump-back; (2) F5 reload — lands at true bottom, not mid-message; (3) scroll up then down — sticks to bottom across late heights; (4) spam `⤓ Last` while content streams; (5) DevTools Performance — no scrollTop oscillation loop.

## 9. Final tree state (worker C, verbatim-verified)

- `git status --porcelain`: 11 lines = 4 source M + 3 static D + 1 static M + 3 static untracked (§7 churn)
- `git diff --stat -- pkg/ui/frontend/src`: **4 files, +722/−53** — source diff intact
- Full `git diff --stat`: 8 files, +724/−146. `git stash list`: **empty**. No commits, no pushes, no destructive ops (single stash push/pop pair only)

## 10. ensure.md status (scoped to FE-only change set)

| Requirement | Status |
|---|---|
| Critical: Frontend builds without TS errors | ✅ PASS scoped (build exit 0; tsc 0 new-debt; 30 pre-existing documented) |
| Critical: Go tests / go vet / full build | OUT OF SCOPE — zero Go files changed (last full-green 2026-08-29 dbcCache gate) |
| Important/Nice-to-have (peak hours, migration 018, races) | OUT OF SCOPE — unrelated modules |

**Scope decision**: full FE suite + build WAS the mandate (52 tests, 2.95s — no split needed); Go packs excluded (no Go changes); tsc leg added ad hoc to complete the ensure.md Critical clause.
