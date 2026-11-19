# PHASE 3 — FRONTEND, TEST LEDGER, GATE, SHIP

**Lane:** `feature/token-authz-allowed-groups` · **Base:** `a3f0335` · **Worktree:** `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-token-authz`
**Read first:** `plan.md` §2.8 (the frozen wire contract), §12 (FE changes), §13 (test pins), §14 (ship), §15 (blast radius).
**Phase verdict:** **SPLIT — the FE half is INDEPENDENT (parallel with Phase 2, gated only on Phase 1's frozen contract); the ship half is TIGHTLY COUPLED and strictly last.**

### 0.1 Leader rulings folded into this revision (2026-11-18 plan-revision pass)

This phase is where the rulings become **evidence**. Four items are added or amended; all are normative over earlier wording.

| Ruling | Task(s) | What changes here |
|---|---|---|
| **G0 expansion (PD-2 + PD-9)** | **T3.10** | G0 gains (a) the **PG major-version** read, (b) the **malformed-JSON-row** check under the same version-agnostic `DO $$ … EXCEPTION` guard, and (c) a **`models.shadow_model_id` × live `auth_tokens.allowed_models`** cross-check. G0 stays an **INSPECTION** — not a gate, not a write, no operator re-scope step (the in-`034` repartition performs the re-scope atomically). |
| **Release-note conditions (Q2 + Q13)** | **T3.11** (D3/D4), **T3.13**, **§8** | Two ratified conditions must appear in the release note **as conditions, not caveats**: (1) **reverse repartition FIRST is a BLOCKING rollback step** (image-rollback-first makes the narrow tokens full-access wildcards under the old binary); (2) the **FE PATCH-builder + the store serialization change MUST ship in the SAME image** (single-image coupling invariant — decoupling re-opens the split-brain escalation trap). |
| **PD-3 / PD-8 / PD-10 test pins** | **§5** (new block) | Phase 3 owns the **cross-phase pin ledger**; the four ruling pins are named here so T3.7's set arithmetic cannot report them "unlisted": the PD-10 empty-`models` regression, the PD-3 nil-axis no-heal, the PD-8 validation triple, and the PD-2/PD-4 migration parity pair. Ownership stays with Phases 1–2; Phase 3 counts and gates them. |
| **PD-9 release wording** | **T3.12** item 4b | The live probe gains a **claimed-id** 403 case, and the note must state the **INTENTIONAL narrowing**: a token granting the **canonical group id** reaches the group target (the resolved target authorizes — ADR D1), while a token granting **only the shadow alias** is **denied** (403 quoting the client-requested id) — the same class as the Q2 group-id narrowing, quantified by G0-4 and repaired by granting the group id. The **members** are authorized individually in either case. |

---

## 1. Objective

1. **FE**: express the two-axis permission model — **TWO per-axis authoritative pickers (groups EXCLUDED from the models picker — LANDED design, fix-pass-C supersession of the old one-picker plan)**, per-axis split authoritative at save (F1), thread `allowed_groups` through every layer, axis-aware badge.
2. **Test ledger**: assemble the **complete** added-test inventory across Phases 1–3 and prove it against the base (set arithmetic), so the anti-vacuous rule is satisfiable at gate time.
3. **Gate**: run Pack A / Pack B / FE suite / tsc baseline, with the **boundary declaration pre-filed** so the migration fence does not burn a cycle.
4. **Ship**: merge → CI → digest verify → daoduc (backup first) → probe → primary → probe → dcs pins (two-step). **No approval hold** (ask-back discharged). Rollback anchors + post-deploy verification.

---

## 2. Files owned (exact)

### 2.1 FE (production)

| File | Anchors | Changes |
|---|---|---|
| `pkg/ui/frontend/src/components/tokens/TokenForm.tsx` | **LANDED (fix pass C): TWO per-axis pickers** — the models picker **EXCLUDES group rows** (`:84-85`/`:159-181`; pin `TokenForm_PickerExcludesGroupModels`), the groups picker carries `allowed_groups`; per-axis submit; unrestricted (both `null`) / restricted (`[]` or ids). **(Superseded instruction — do NOT implement:** the earlier "delete the kind filter at `:34`, all kinds selectable in one picker" design — dated supersession, plan §12.1 re-written to the landed design.) |
| `pkg/ui/frontend/src/components/tokens/ModelMultiSelect.tsx` | `:3-8`, `:39-45` | **no change** (ids-only, reusable as-is). |
| `pkg/ui/frontend/src/components/tokens/TokenList.tsx` | props `:5-13` (`:7` type lacks `kind`), state `:18-21`, `getAllowedModels` `:57-62`, `startEditModels` `:65-68`, `saveEditModels` `:77-109`, display `:117-140`, inline picker `:253` | (a) widen the `models` prop type to include `kind?` (the caller `SettingsPage.tsx:744` already passes it — silently dropped today); (b) add a parallel `allowed_groups` optimistic map + edit state; (c) split at save; (d) **badge becomes axis-aware**: "All models" ONLY when **both** axes are absent (wildcard) — otherwise render model chips and group chips separately; (e) pass both axes to `onUpdatePermission`. |
| `pkg/ui/frontend/src/types.ts` | `ApiToken` `:240-249` (`allowed_models` `:248`) | add `allowed_groups: string[] \| null;` with a comment stating **null = axis ABSENT (not "none")** and the conjunctive wildcard rule. |
| `pkg/ui/frontend/src/hooks/useApi.ts` | `createToken` `:898-911` (body `:899-903`), `updateTokenPermission` `:913-934` (body `:918-920`, optimistic merge `:927`) | (a) both take/send `allowedGroups`; (b) **POST**: nothing selected ⇒ `allowed_models: null, allowed_groups: null` (wildcard); (c) **PATCH: an axis the operator cleared sends `[]`, not `null`** — `null` means KEEP (`decisions.md` Q13 / R-6: sending `null` for a cleared axis silently keeps the old grant; sending `null` for **both** would keep everything, and the escalation trap lives on the other side of this distinction); the builder must therefore take *explicit* per-axis intent, not just arrays — recommended signature `updateTokenPermission(id, perm: { models?: string[] \| null; groups?: string[] \| null })` where `undefined` = omit the key entirely (keep), `[]` = clear, ids = replace; (d) optimistic merge (`:927`) updates **both** axes. |
| `pkg/ui/frontend/src/components/SettingsPage.tsx` | `:61`, `:63`, `:370-395` (`handleCreateToken` `:370`), `:741-756` (TokenList `:744`, TokenForm `:753`) | thread the new params; `handleCreateToken` forwards both axes; the `models.map` at `:744` / `:753` already passes `kind` (keep). |
| `pkg/ui/frontend/src/App.tsx` | `:283`, `:285` (+ pass-through `:296`-region) | widen both callback prop types. |

### 2.2 FE (tests)

| File | Change |
|---|---|
| `pkg/ui/frontend/src/components/tokens/__tests__/TokenForm.test.tsx` | **rewrite** (see T3.3) |
| `pkg/ui/frontend/src/components/tokens/__tests__/TokenList.test.tsx` (new, if absent — check first) | axis-aware badge cases |
| `pkg/ui/frontend/src/hooks/__tests__/useApi.token-permission.test.ts` (new) | PATCH builder: clear ⇒ `[]`; keep ⇒ key omitted; POST ⇒ both `null` |

### 2.3 Gate / ship artifacts (not source)

`.agents/tester/PACKS.md` (**append-only** lane note — do not rewrite the normative sections), the gate RESULTS file under `.agents/tester/RESULTS/`, and the ship record. **No `k8s/`, `.github/`, or `pkg/config/` change.**

### 2.4 Files this phase must NOT touch

Any Go production file (Phases 1–2 own them). If the gate finds a Go defect, it routes **back** to the owning phase — this phase does not patch Go behaviour implicitly.

---

## 3. Tasks

### T3.1 — Types + payload builders (do first: it fixes the contract in the FE's own vocabulary)

`types.ts:248` region → `allowed_groups: string[] | null`.

**Ruling 2 reversal (fix-pass 2) — TWO wire states per axis (Q13(a)):** the cycle-1 tri-state (explicit `null` = reset-to-absent) is REJECTED and removed. The BE contract is two-state: **key omitted OR explicit `null` ⇒ keep** (column untouched); **`[]` ⇒ clear**; **ids ⇒ replace**. The builder signature `{ models?: string[] | null; groups?: string[] | null }` still carries every reachable state; write the comment at the builder explaining the two states and the trap — the next reader will otherwise "simplify" `[]` back into `null` and silently re-open the escalation trap. **Unrestricted/wildcard promotion on EDIT is intentionally inexpressible** (a PRESENT axis can never return to ABSENT via PATCH): an operator promotes a restricted token back to wildcard by **re-creating the token** — the FE edit surface has NO "remove restrictions" action (create-only); state that in the FE-9 note and the release note.

`useApi.ts:898-911` / `:913-934` per §2.1. POST: nothing selected ⇒ both axes `null` (ABSENT = wildcard). PATCH: per-axis intent as above.

### T3.2 — TokenForm: TWO per-axis pickers (LANDED design; the old "delete the kind filter" instruction is SUPERSEDED — do NOT implement)

- **LANDED design (fix pass C): the models picker EXCLUDES group rows** (`TokenForm.tsx:84-85`/`:159-181`) and a separate groups picker carries them; the split is per-axis and the picker is authoritative at save (F1). *(Superseded — do NOT implement: deleting `:30-34` and admitting all kinds into one picker; kept only as a dated historical note.)*
- **Per-axis submit (LANDED, F1):** each picker is authoritative for its own axis — `onSubmit(name, expires, modelIdsFromModelsPicker, groupIdsFromGroupsPicker)`. **Cross-axis additions** are honored by the landed `grantAxes.tsx` stored-membership-first `axisOfId` (a **stored** group id stays on its stored axis even if only a target-only group id in `allowed_models`; a **NEW** group id classifies onto the groups axis) — pins `TokenList_TargetOnlyGroupId_SurvivesNoChangeSave`, `TokenForm_NewGroupSelection_LandsOnGroupsAxis`, `TokenList_BothAxes_NoChangeSave_KeepsIdOnBothAxes`, `TokenList_CrossAxisAddition_IsPreserved`, `TokenList_DuplicateStoredIds_AreDedupedPerAxis`.
- T3.2b — **stale-id safety (edit mode):** if `token` is set, an id that is in `token.allowed_groups` but absent from `models` must stay on the **groups** axis; likewise an id in `token.allowed_models` stays on models. Implement as: `kindOf(id) ?? (token?.allowed_groups?.includes(id) ? 'group' : 'model')`. This prevents a silently re-axised grant.
- Picker render `:109-113`: unchanged component, now unfiltered. Update the copy `:113-118` ("No selection means all models and all groups are reachable" when both empty).
- **Ruling 2 reversal — NO "remove restrictions" action in FE edit:** wildcard promotion on EDIT is intentionally **inexpressible** (explicit `null` = keep; only `[]`/ids writes exist). The repair path is **re-create the token**; the helper copy says so and T3.12's release note carries it (FE-9 note: Unrestricted mode = create-only in FE edit). Do not add a promotion affordance.
- **Dead-code note (TokenForm edit mode is unrendered today):** `TokenForm` edit mode is **DEAD CODE** today — `SettingsPage.tsx:739-758` renders `TokenForm` **without** a `token` prop (create-only). The edit-mode branches (T3.2b stale-id safety, `token?.allowed_groups` reads) are therefore unreachable in production until edit mode is wired; keep them defensive but do not treat them as exercised by FE-1..FE-5. If edit mode is later enabled, re-run FE-5 against a real token prop.

### T3.3 — Flip the red test (and rename it)

`pkg/ui/frontend/src/components/tokens/__tests__/TokenForm.test.tsx`:
- Header comment `:5-15` and the test title `:41` still encode the **old** contract ("excludes image-gen + decision rows") — both must be rewritten in the same edit, or the file keeps asserting the retired rule in its name.
- Assertions `:80-84`: `:80` `'Chat One'` and `:81` `'Chat Two (no kind)'` stay `toContain`. **`:82` `'Image Gen One'`, `:83` `'Decision One'`, `:84` `'Group One'` all flip to `toContain`.** `:84` is the commission's named assertion.
- **New cases** (all must appear exactly once in the FE log — anti-vacuous):
  1. *selecting a group row emits it under `allowed_groups` and NOT under `allowed_models`* (assert on the `onSubmit` mock's arguments).
  2. *selecting a decision model emits it under `allowed_models`*.
  3. *nothing selected ⇒ submit receives two empty arrays* (the POST layer then encodes them as `null`, asserted separately in T3.4).
  4. *edit mode: a group id stays on the groups axis when its row is missing from `models`* (T3.2b).

### T3.4 — useApi builder tests

`useApi.token-permission.test.ts`:
- POST: nothing selected ⇒ body `allowed_models: null, allowed_groups: null`.
- PATCH clear-one-axis ⇒ body carries `[]` for that axis and **omits** the other.
- PATCH replace ⇒ body carries the ids.
- **Ruling 2:** PATCH explicit `null` on one or both axes ⇒ **keep** (identical to omitting the key); assert the BE leaves both columns byte-untouched and returns 200.
- Optimistic merge updates both axes in local state (including a reset).

### T3.5 — TokenList: second surface + axis-aware badge

- Prop type `:7` gains `kind?`.
- Add `optimisticAllowedGroups` mirroring `:20`, `getAllowedGroups` mirroring `:57-62`, `startEditModels` seeds both axes (`:65-68`), `saveEditModels` splits + calls the new callback (`:77-109`), revert paths (`:89-104`) restore **both** axes.
- Display `:117-140`: replace the "All models" badge condition with `bothAxesAbsent`; otherwise two chip groups (models, groups) with independent "+N" overflow. The badge text for wildcard: **"All models (wildcard)"** — it must not read as "no restrictions at all" (lane gates still apply).
- **Ruling 2 note:** the server value `null` means the axis is **ABSENT** (response encoding — `*[]string` emits JSON `null` for ABSENT, plan §2.8); the optimistic merge (`:927`) keeps treating it as ABSENT for the `bothAxesAbsent` badge — unchanged from the §2.8 response contract. Do not coerce `null` → `[]` in local state. (This is the RESPONSE null, not the REQUEST null: on the request, explicit `null` = keep per Ruling 2.)
- Inline editor (LANDED): the TokenList edit surface uses the **per-axis pickers** (models + groups) with the same `grantAxes` classification; there is no single flat picker.

### T3.6 — SettingsPage / App wiring

`SettingsPage.tsx:61/:63/:370-395` and `App.tsx:283/:285` per §2.1. Type-level only; no behaviour change beyond forwarding. Then run `tsc --noEmit` and compare against the **committed baseline** (19 errors / 9 files per `PACKS.md:262`): **zero NEW error locations** (`PACKS.md:254`, `.agents/tester/rules/ensure.md:7`). `types.ts` is in the change set ⇒ the comparator applies strictly.

### T3.7 — The test ledger (the anti-vacuous precondition)

Assemble one table with **every** added/changed test name from Phases 1–3 and prove, against `$BASE`:

| Column | Meaning |
|---|---|
| Name | exact `func Test…` name (Go) or FE case title token |
| Phase | 1 / 2 / 3 |
| File | owning file |
| Pack | A / B / FE |
| `--- PASS` count in the gate log | must be **exactly 1** |
| Changed vs added | a **modified** existing test (e.g. `TestScanAllowedModels` → `TestScanGrant`) is listed explicitly so the set arithmetic does not report it as "unlisted" |

Rules from the house corpus (`.agents/tester/PACKS.md:15/:18/:20`, `RESULTS/decision-groups-gate.md:79-84`):
- every added Go name must show a literal end-anchored `--- PASS` in the gate's **own** logs, **0 MISS / 0 SKIP**, matched **exactly once**;
- every FE case token must appear **exactly once** in the vitest log (`×` = 0);
- set arithmetic: `added == expected`, `unlisted == 0`, `absent == 0`;
- a MISS on any token ⇒ **the gate fails**. No exceptions, no "it ran locally".
- **The ledger MUST include every row of §5.1** (the cross-phase ruling pins added by this revision). They are Go tests owned by Phases 1–2, so they arrive via Pack A/B — but they must be **named** in the ledger, or the set arithmetic reports `unlisted == 0` while silently omitting the ten most consequential assertions of the commission (R3.14). A pin that landed under a different name is **renamed in the ledger to the code's name**; the expected count does not change.

### T3.8 — Boundary declaration (PRE-FILE, before the gate)

Per `decisions.md` R-9 and `.agents/tester/PACKS.md:34-35` (the fence asserts 0 changed files under `pkg/store/database/migrations/` and `pkg/store/database/migrate.go`):

> **Authorized boundary changes for this lane:** `pkg/store/database/migrations/sqlite/034_add_allowed_groups.{up,down}.sql`, `pkg/store/database/migrations/postgres/034_add_allowed_groups.{up,down}.sql`, and the registry append in `pkg/store/database/migrate.go` (immediately after `:82`).
> **Unauthorized (must remain 0 files):** `k8s/`, `.github/`, `pkg/config/`.

Also pre-declare: **`pkg/decision/policy.go` must show a 0-byte diff**; `pkg/grouplb/**`, `pkg/credentiallb/**`, `pkg/proxy/race_executor.go` **0-byte diffs** (§Phase-2 exit criteria 4).

> **CORRECTION (PD-10 — DRIFT REPORT, must be in the pre-filed declaration).** `pkg/proxy/race_coordinator.go` is **NO LONGER** a 0-byte-diff path. T2.12 (PD-10, RED) deletes the empty-list fallback at `:338-340` and changes `newRaceCoordinatorWithEvents` / `newRaceCoordinator` to return `(*raceCoordinator, error)`; the **42** test call sites across 11 `pkg/proxy/*_test.go` files are mechanically updated in the same commit. **Pre-declare it as an AUTHORIZED change with a bounded shape:** the diff is exactly (a) the deleted `models = []string{cfg.ModelID}` fallback, (b) the two signature/error changes, (c) the mechanical `coord, err := …` updates at call sites. **Anything else in that file is unauthorized** — in particular no scope predicate, no advance/hedge/credential-failover edit — and the gate should read a larger diff as a finding, not as noise. `pkg/proxy/race_executor.go` stays at **0 bytes**.

### T3.9 — Gate run

| Step | Command / scope | Pass condition |
|---|---|---|
| G1 | `go build ./...` + `go vet ./...` + `gofmt -l` (zero-new-drift vs `$BASE`) | exit 0 / empty NEW list |
| G2 | **Pack A** `./pkg/proxy/... ./pkg/store/... ./pkg/decision/... ./pkg/ui/... ./cmd/...` (9 pkgs), `-timeout=240s` inside `timeout 300`, `HOME=$(mktemp -d)`, `unset DATABASE_URL PORT`, config.json hash-guarded on the **absolute real-home path** | exit 0; only registry-quarantined flakes adjudicated (`QUARANTINE.md:7`, `:12`; `:10` is Pack B) | **ENV PRECONDITION (fix pass C):** `TestPackage_ImportGuard_AuthzLeafConstraint_TransitiveClosure` (`pkg/authz/guard_test.go:151`) now `t.Fatalf`s ⇒ run Pack B with `GOPATH=/home/nea/go GOMODCACHE=/home/nea/go/pkg/mod`; `pkg/authz` is NOT in Pack A, so G2's quarantine does not cover it.
| G3 | **Pack B** module-anchored complement (34 pkgs, keep `(/|$)` — re-measure, never copy the count) | sole expected failure = the quarantined httpbin redirect pair (`QUARANTINE.md:11`, deterministic; adjudicate) |
| G4 | **FE**: `cd pkg/ui/frontend && timeout 300 npx vitest run` | all green, `×` = 0 |
| G5 | **tsc**: `npx tsc --noEmit` vs committed baseline | **zero NEW error locations** |
| G6 | **vite build** | byte-identical to the committed `pkg/ui/static` (house check; a diff means the SPA was not rebuilt) |
| G7 | **Anti-vacuous** (§T3.7 ledger) | exact counts, 0 MISS |
| G8 | Boundary check `git diff --stat $BASE..HEAD -- k8s/ .github/ pkg/store/database/migrations/ pkg/store/database/migrate.go pkg/config/` | only the pre-declared 034 files + `migrate.go` |
| **G11** | **PD-10 diff-shape check:** `git diff $BASE..HEAD -- pkg/proxy/race_coordinator.go` | the diff is **only** the deleted `models = []string{cfg.ModelID}` fallback + the two signature/error changes; **zero** hunks touching `manage()`, advance, hedge or credential-failover logic. `pkg/proxy/race_executor.go` **0 bytes** (T3.8's corrected declaration) |
| **G12** | **Ruling-pin presence sweep** (the greppable half of the Phase-2 audits, run against the merged tree): (a) `grep -n "models = \[\]string{cfg.ModelID}" pkg/proxy/race_coordinator.go` ⇒ **no hits**; (b) **INTENT assertion, NOT a raw grep count (fix pass C, CRITICAL 1 — the raw grep prints 3 correct-tree hits and would fail a correct tree):** the landed pin **`TestPackage_NoPerAxisLenGuardOutsideAuth`** (`pkg/proxy/authz_guard_test.go:120`, comment-stripping + firing negative control) enforces the INTENT — **no per-axis LENGTH GUARD may govern authorization; reach is decided only by `authz.Resolve` / `Scope.Allows`**. If a grep prescription is kept it MUST exclude `pkg/auth/**`, capacity hints (`make(map[…], len(...))`), slice copies (`make([]string, len(...))`) and comment lines, and treat these HEAD hits as EXPECTED: `pkg/authz/scope.go:93` (map capacity hint), `pkg/modelscache/tokens.go:144` (slice copy), `pkg/proxy/handler.go:855` (a comment) — **the landed code is FROZEN; a mismatch is a gate-WORDING defect to report, NEVER a licence to edit code.**; (c) `grep -rn "ExcludeFromUltimateSwitching\|UltimateModel" --include=*.go pkg/ cmd/ | grep -v _test` ⇒ **comments only** (PD-13); (d) **COUNT-FREE, COMMENT-STRIPPED INTENT assertions** (fix-pass A/R3 — the old `grep -c` counts were FALSE against the landed artifact: `NULLIF` measures 3 with comments and `pg_input_is_valid` measures 1 as a comment): **(i)** `NULLIF` present in **each** executable SET arm of the **SQLite FORWARD** file only (per-arm, `--`-stripped — the PG down file carries **NO NULLIF**: PG `json_agg` returns SQL NULL over 0 rows; the earlier over-broad NULLIF wording is corrected fix pass B-final); **(ii)** the PG guard is structurally **inside** the `DO $$ … LOOP` (mirror `TestMigration034_PGGuardShape`: `EXCEPTION` appears after `LOOP`, `RAISE WARNING` present); **(iii)** `pg_input_is_valid` absent from **EXECUTABLE** SQL (a comment-only occurrence is acceptable). **THE 034 SQL IS FROZEN — these greps describe the artifact; a mismatch is a gate-WORDING defect to report, NEVER a licence to edit the migration.** | all sub-checks hold; output pasted into the gate RESULTS file |
| G-f | **CORPUS FREEZE input (gen-11 re-pin):** the gate runs against the corpus frozen at the manifest hash in the `plan.md` header. **Reproduce verbatim:** `cd <corpus-dir>; { for f in adr-token-permission-model.md architecture-recommendation.md decisions.md phase1-plan.md phase2-plan.md phase3-plan.md plan-overview.md; do sha256sum "$f" | cut -d' ' -f1; done; sed '7d' plan.md | sha256sum | cut -d' ' -f1; } | sha256sum` — **the pinned fallback/equivalent: `grep -v 'corpus frozen at' plan.md | sha256sum` (byte-identical; verified) — use either. The freeze line MUST be line 7 of plan.md for the `sed '7d'` form to reproduce the same content.** — the printed hash must equal the header's full manifest sha256 (plan.md hashed excluding its own freeze line). A post-freeze core-doc edit invalidates the gate | hash matches |
| **G-H1** | **Degraded-review tail re-verify (fix pass B, item 12a):** the C2/C3 lane's independent review ran **DEGRADED** (skill-bank miss → manual fallback) over `156252a..63e9665`; commits **`ce8f2f5`, `0be8d13`, `4be0a12`** received **structural self-inspection only**. The release gate MUST re-verify `156252a..4be0a12` (full behavioural review, not self-inspection). | re-review recorded |
| **G-H2** | **`-race` timeouts (fix pass B, item 12b):** `go test -race ./pkg/store/... ./pkg/proxy/...` needs **1200–1500 s**; the 240 s Pack-A default is insufficient under `-race` — do not read a 240 s timeout as a failure. | run with the -race budget |
| G-PG | **PG-migration CI status (fix-pass 2 carry):** record whether the PostgreSQL `034` migration is CI-tested (a live-PG unit pack) or explicitly **not** CI-tested — state which in the RESULTS file so the G0 pre-ship inspection is understood as the only PG-side pre-deploy verification |
| G9 | Boot smoke in isolation: `/healthz`, `/fe/api/version`, `/fe/api/models` 200/200/200; zero panics; clean SIGTERM; real config hash unchanged | house standard |
| G10 | **Perf sanity** (chat + decision p99 vs a pre-change capture) | "no visible regression"; `plan.md` §4.4 constraint (one resolution/request, **in-memory snapshot membership** — Ruling 5/I14; NOT `GetModelNative`) re-verified by reading the diff, not by measurement alone |

**Gate hazard to respect:** `pkg/proxy` measured **131 s** in the 2026-11-17 gate and `pkg/store/database` **38 s** — a `-timeout=110s` run on `pkg/proxy` fails spuriously. Use the release-gate slice timeouts (`PACKS.md:9-14`, `:20`).

### T3.10 — G0 pre-ship read-only DB inspection (BOTH hosts) — must run **before** the deploy, **not** as a gate

Run the queries in `plan.md` §14.3 **plus the three revision-pass additions below**, on **both hosts**. Record the output in the ship artifact. **This is read-only and is not a hold**: it names which tokens the repartition touches (`smoke-primary-agentic`, `smoke-primary` on both hosts) and proves no row is unreadable before the fail-closed flip. Nothing is written; nothing waits.

**PD-2 mandate — every G0 query that casts a TEXT column must be version-agnostic and per-row guarded.** The pre-PD-2 G0 queries suffered the *identical* crash as the migration: a `[`-prefixed malformed row raises `invalid input syntax for type json` and the inspection dies on the very row it exists to find — a **blind spot disguised as a clean result**. The SQL text is owned by Phase 1 T1.7 and must be **copied verbatim**, i.e. each query is a `DO $$ … BEGIN … EXCEPTION WHEN invalid_text_representation THEN RAISE NOTICE 'malformed allowed_models row: % %', r.id, r.name; END; END $$;` block with the cast inside the inner `BEGIN`, and **no `pg_input_is_valid`** (no PG-16 assumption).

| # | Query (read-only) | Pass criterion |
|---|---|---|
| **G0-0** | **PG major version:** `SELECT version();` | Recorded verbatim in the ship artifact. **Not** a gate on the guard (the guard is version-agnostic by ruling) — it is recorded so a future reader knows which server the inspection ran against, and so a PG-16-only optimization is never silently assumed |
| **G0-1** | **Which tokens name a GROUP id in `allowed_models`:** `DO $$ … FOR r IN SELECT t.id, t.name, t.created_by, t.allowed_models FROM auth_tokens t WHERE t.allowed_models IS NOT NULL AND btrim(t.allowed_models) <> '' AND t.allowed_models ~ '^\s*\[' LOOP BEGIN IF EXISTS (SELECT 1 FROM jsonb_array_elements_text(r.allowed_models::jsonb) v WHERE v IN (SELECT id FROM models WHERE kind='group')) THEN RAISE NOTICE 'group-named token: % % %', r.id, r.name, r.allowed_models; END IF; EXCEPTION WHEN invalid_text_representation THEN RAISE NOTICE 'malformed allowed_models row: % %', r.id, r.name; END; END LOOP; END $$;` | Output recorded; expected to name `smoke-primary-agentic` + `smoke-primary` (both hosts). The **malformed branch prints a row** rather than aborting — that is the PD-2 protection |
| **G0-2** | **Unreadable grant columns:** the §14.3 query-2 shape, `DO $$`-guarded, testing both `allowed_models` and `allowed_groups` for non-`[` garbage **and** for `[`-prefixed malformed values (via `PERFORM r.allowed_models::jsonb` inside the guard) | Every unreadable row is **named**; zero rows ⇒ clean; ≥1 row ⇒ named in the ship note (the fail-closed flip will deny those tokens, which must be a known, quoted cost) |
| **G0-3** | **Full token inventory** to classify `smoke-15m` (wildcard vs narrow) | `smoke-15m` classified; recorded |
| **G0-4** *(NEW — PD-9)* | **Shadow/claimed-id cross-check:** which live tokens reference a `models.shadow_model_id` value? Same `DO $$ … EXCEPTION` shape: iterate `models WHERE shadow_model_id IS NOT NULL AND shadow_model_id <> ''`, and for each, report every `auth_tokens` row whose `allowed_models` **or** `allowed_groups` JSON array contains that shadow id (guarded cast; `RAISE NOTICE 'token % (%) grants shadow id % (group %)', t.id, t.name, s.shadow, s.id`). | Output recorded. **Inspection only** — it exists because PD-9's alias semantics introduce an **intentional narrowing** (a token granting only a shadow alias is denied at the entry — the resolved target authorizes, ADR D1), and the release note must be auditable. **No operator action, no gate**: the affected tokens are **named** here so the operator can grant the group id (or the member ids) if they want them to serve — and the note states that repair |

**What G0 is NOT (state it in the artifact so it is never mistaken for a gate):** not a write, not a hold, not an operator re-scope step. The repartition inside `034` performs the re-scope atomically at boot with zero window (`plan.md` §14.4) — by the time the host serves traffic every row is already in the new form. The **reverse** repartition is a Tier-1 **rollback** step (T3.13), not a pre-ship step.

### T3.11 — Ship

**Landing (house recipe — the main checkout HOLDS `master`; never merge there):**
1. `git merge --no-ff origin/latest` in the sibling worktree; `git push origin <merge-sha>:refs/heads/latest` (explicit-SHA refspec ⇒ fast-forward regardless of local staleness).
2. In the **same** worktree: `git checkout --detach origin/master` → `git merge --no-ff origin/latest` → assert **parent1 = prior master tip**, **parent2 = latest tip**, and **`tree(promo) == tree(latest tip)`** → `git push origin <promo-sha>:refs/heads/master`.
3. Clean up the worktree; `git branch -d` the temp branch. Base every merge on **origin** refs; a worktree `.git` is a pointer file (never `cp -a` to `/tmp` and run git there).

**Deploy sequence (standing grant — NO approval hold):**

| # | Step | Gate |
|---|---|---|
| D1 | **WAIT for the CI build** (a queued build is not a built image) | CI green |
| D2 | Verify the **image digest registry-side** (not from the CI log) | digest matches the CI-reported digest; all tags |
| D3 | **Deploy daoduc FIRST**, with a **pre-deploy DB backup** | backup artifact recorded (this is the tier-2 rollback anchor) |
| D4 | The migration runs **in-process at boot** (`pkg/store/database/init.go:19` → `RunMigrations`) — no migrate Job; verify `schema_migrations` has `034` | SQL check |
| D5 | **Quick probe daoduc**: `/fe/api/version` == the new build; `/healthz`; one `/v1/models`; one granted decision-group request 200 | probe log |
| D6 | **Deploy ensem (primary)** | 0 restarts |
| D7 | **Quick probe primary**: same as D5 **+ one 403 probe** (a narrow token naming a model outside its reach ⇒ 403 `access_denied` with the client-requested id) | probe log |
| D8 | **Push dcs pins, two-step, daoduc-first** | pins = highest deployed; `git fetch` + rebase + retry, **never force** |

**Cross-lane rules:** never downgrade a host; a dcs push is re-fetch/rebase/retry, never `--force`; pins record the **highest** deployed version across hosts.

**⚠️ Two RATIFIED CONDITIONS must be in the release note and the ship artifact — as conditions, not footnotes.**

1. **Q2(b) — reverse repartition FIRST is a BLOCKING rollback step.** Tier-1 rollback is **step 1: reverse repartition, step 2: image/helm rollback**, in that order, on the affected host. The reverse is an **operator-run, idempotent UPDATE** (drafted in `034_add_allowed_groups.down.sql`, `plan.md` §5.2) and it is a **named step of Tier-1**, not a deferred tier-3 action. Reason, stated plainly so nobody re-orders it under pressure: the pre-034 binary reads only `allowed_models` and maps `len(AllowedModels)==0` to **wildcard** (`pkg/auth/token.go:114-118`, verified `:116`); after the repartition `smoke-primary` / `smoke-primary-agentic` carry `allowed_models = NULL` ⇒ **image-rollback-first makes them full-access tokens on the public `/v1/*` endpoint** — the exact defect class this commission closes.
2. **Q13(a) — SINGLE-IMAGE COUPLING: the FE PATCH-builder and the store serialization change MUST ship in the SAME image.** The FE's **two-state** PATCH builder (Ruling 2: `[]` = clear, key omitted/`null` = keep — `pkg/ui/frontend/src/hooks/useApi.ts:913-934`, verified: today `:919-920` collapses "cleared" and "absent" into `null`) and the BE's explicit-empty serialization (`nil → NULL`, `[] → "[]"` — T1.3) are **two halves of one contract**. Shipping either alone re-opens the split-brain escalation trap: an old FE against a new BE sends `null` for a cleared axis ⇒ **keep** (silent no-op, the operator believes access was removed); a new FE against an old BE sends `[]` ⇒ the old store's `len(x) > 0` guard turns it into **NULL** ⇒ **wildcard**. Neither half is deployable alone; a future decoupling requires a **re-ruling**, and the release note must say so.

**Evidence that both halves shipped together** (record in the ship artifact): the image contains **both** the FE bundle change (`vite build` assets byte-identical to the committed `pkg/ui/static`, gate step G6) **and** the Go store change (the `TestUpdateTokenPermission_OmitsNilColumn` pin) — same digest, same tags, verified registry-side at D2.

### T3.12 — Post-deploy verification (the evidence that the commission landed)

1. Both hosts `/fe/api/version` == the shipped build; `schema_migrations` has `034` on both.
2. **The narrow tokens still serve their groups** — this is the **repartition-preserved** proof (`smoke-primary-agentic` ⇒ `agentic-group`; `smoke-primary` ⇒ `coding-group` + `quick-group`). A 403 here means the repartition did not apply — stop and roll back tier-1.
3. A **wildcard** token still reaches every kind (a chat model, a group, a decision model, a decision group, an image-gen model).
4. An **out-of-scope** request returns 403 `access_denied`, body `model` = the client-requested id, **no `code` key**.
4b. **(PD-9) A claimed-id probe — two cases, because the gate is STRICT (the resolved target authorizes, ADR D1; T2.16):**
    - **(i) the token is NOT granted the group** (it grants neither the canonical group id nor the claimed id, **or** it grants **only the claimed id**): the request naming the claimed id ⇒ **403** whose body quotes the **client-requested claimed string** and contains **no occurrence of the canonical group id** (no claim-topology leak). **An alias-only grant is denied** — this is the intentional narrowing, and it is exactly the commission's pin.
    - **(ii) the token IS granted the canonical group id**: the same request naming the claimed id ⇒ **admitted** (200) — the resolved target authorizes, and a group grant still works through its alias.
    If G0-4 found **no** live token referencing a shadow id, record "no live alias granters — probe exercised with a synthetic token, then deleted" instead of skipping silently.
5. A **decision-group** request with a token that does **not** grant the group returns **403** (the gen-3 defect is dead in production).
5b. **(PD-10 smoke) An empty-scope probe:** a restricted token whose grants are stripped of every member id requests a group whose members are all out of scope ⇒ **403** (never 502, never a served attempt), and the request log shows **zero** upstream attempts for that group. This is the production proof that the constructor's empty-list fallback cannot re-expand a model at runtime.
6. **No 502 `group_unservable` regression** in the first minutes of traffic.
7. Zero unexpected restarts; no new `SECURITY:` / `[authz] DENY-ALL` lines beyond any intentionally flagged row from G0.
8. **(PD-13) The non-scope classification is confirmed in production telemetry:** `ResolvePeakHourModel` / `SecondaryUpstreamModel` substitutions appear in the log exactly as before (the same wire-string swaps, no new ids in any execution list) — i.e. nothing was scoped that shouldn't have been, and nothing unscoped slipped in. Cross-reference: T2.14's non-scope table + `TestPackage_NoUltimateEscalation` green in the gate.
9. **(Q2/Q13) The release note is published and contains the two ratified conditions verbatim** (T3.11's block): the tier-1 ordering rule as a **blocking** step, and the single-image coupling invariant as a **shipping constraint**. A note without them is an incomplete ship, not a cosmetic gap.

### T3.13 — Rollback (anchors, tiered)

**Two facts must never be conflated:** the **SCHEMA** (the added column) is additive and schema-safe; the **DATA** (the repartition) is **not** neutral, because it moved group ids out of `allowed_models` — the one column the pre-034 binary reads. The rows below are split by what they actually touch, and **the order inside Tier-1 is load-bearing**.

| Tier | Action | Safety / ordering |
|---|---|---|
| **Tier-1** | **reverse repartition FIRST, then image / helm rollback, on the affected host** (daoduc helm rev N-1; primary `kubectl set image` to the prior digest). **Step 1 = the operator-run, idempotent reverse UPDATE** (drafted in the down file, `plan.md` §5.2: *"REVERSE the data repartition (deterministic: group ids go back into `allowed_models`)"*), **Step 2 = the image/helm rollback.** | **Order is load-bearing — this is a PROHIBITION, not a preference.** After the repartition, `smoke-primary` has `allowed_models = NULL` + `allowed_groups = ["coding-group","quick-group"]`, and `smoke-primary-agentic` likewise with `["agentic-group"]`. The pre-034 binary does **not** read `allowed_groups` at all, and its `IsModelAllowed` treats `len(AllowedModels)==0` as **wildcard** (`pkg/auth/token.go:116`) ⇒ **image-rollback-first would make both tokens UNRESTRICTED — full access to every model — on a public `/v1/*` endpoint.** That is the exact defect class this commission closes; the rollback path must never open it. **Correct order:** reverse repartition first ⇒ under the **new** binary the reversed row has `allowed_models = [group ids]` (PRESENT ⇒ **not** a wildcard under the conjunctive rule) ⇒ the tokens stay **RESTRICTED** (they 403 on member requests — a narrow, operator-owned, single-hop smoke-token impact) and **no privilege is widened**; then the image rollback restores the **original** semantics exactly (old binary + original data = pre-change behaviour). |
| **Tier-2** | restore the pre-deploy DB backup (D3's daoduc backup / the primary's equivalent), **then** the image/helm rollback | Reverses the **schema addition and the data repartition together**; then redeploy the prior image. The cleanest recovery, and the only tier that restores the *data* without a hand-written reverse UPDATE. |
| **Tier-3** | operator-run `034_add_allowed_groups.down.sql` **+ delete the `schema_migrations` row `034`** (no down-runner exists; the version row is the hygiene step, `plan.md` §5.6) | **Requires the image rollback as well**: the down file **drops the column**, and the **new** binary selects `allowed_groups` — it cannot run against a schema without the column. Order: reverse repartition (or backup restore) → image rollback → down file + version-row deletion. For a partial-failure recovery or a deliberate schema retreat — **not** for a routine image rollback. |

### ⚠️ The rollback subtlety that must be stated in the ship record (and in the Tier-1 row above)

**An image rollback is NOT permission-neutral**, because the repartition moved group ids out of `allowed_models`. The OLD binary evaluates `allowed_models` only — so after the repartition and before any reversal, the migrated narrow tokens read `NULL` and become **wildcards** (old code: empty ⇒ wildcard). Three consequences:

1. **The rollback is safe from an availability standpoint** (nothing 403s; the tokens get *more* access), but it is **not safe from a permission standpoint** (a narrow token becomes a full-access token on a public endpoint).
2. Therefore the runbook entry reads: *"tier-1 = reverse repartition FIRST, then image rollback — never the other way round."* The reverse repartition is a **named step of Tier-1**, not a deferred tier-3 action.
3. **The temporary impact of the correct order is narrow and acceptable:** under the new binary, a reversed token 403s on member requests (single-hop, operator-owned smoke tokens) — the price of not widening privilege. The alternative is full access.

**SCHEMA-ONLY statement (scoped, not to be read as a data statement):** the **column** `auth_tokens.allowed_groups` is additive with a NULL default and the pre-034 binary never selects it ⇒ **an image rollback never needs a schema rollback**. That is a fact about the SCHEMA alone; it says nothing about the repartitioned rows, which is exactly what the ordering rule exists to handle.

This is the honest cost of the clean break and belongs in the release note, not in a footnote.

**C4 deploy strategy (fix pass B, item 8 — leader-ruled option (a)).** For THIS rollout: `Recreate` / `maxSurge: 0` per host, **daoduc first** (Helm override) → verify green (G0 re-run + probes) → **primary** (dcs `deployment.yaml` strategy edit), other host absorbing per standing failover; **revert to `RollingUpdate`** after both green. Rationale: a RollingUpdate overlap lets a pre-034 pod read repartitioned rows as wildcards; (b) needed live-store mutations (ask-back) and (c) churned the frozen SQL. Rollback ordering unchanged (Step-0 → Step-1 reverse → image rollback). Full text: `plan.md` §14.7.

**Tier-1 Step-0 — post-ship `'[]'` enumerate-and-reconcile (fix-pass 2, 🟡1 — BEFORE Steps 1 and 2).** The two-state PATCH contract (Ruling 2) means tokens cleared **after** the ship carry `allowed_models = '[]'` / `allowed_groups = '[]'` — PRESENT-empty (grants nothing) under the new binary, but `len(AllowedModels)==0` ⇒ **wildcard** under the old binary, and the reverse repartition **skips** `'[]'` rows. Runbook step: `SELECT id, name FROM auth_tokens WHERE allowed_models = '[]' OR allowed_groups = '[]';` — **enumerate, then revoke or re-create each named token BEFORE the reverse repartition and the image rollback.**

**PG reverse-repartition SQL VERBATIM (fix pass B, item 7 — the earlier `NULLIF` claim on the PG down file was WRONG).** The FROZEN `pkg/store/database/migrations/postgres/034_add_allowed_groups.down.sql` uses **plain `json_agg(v)` with NO `NULLIF`** plus a per-row `DO $$ … EXCEPTION WHEN invalid_text_representation THEN NULL; END $$` guard, inside one `BEGIN … COMMIT`, and ends with `ALTER TABLE auth_tokens DROP COLUMN IF EXISTS allowed_groups;`. **Why no `NULLIF` on PG:** `json_agg` over zero rows already returns SQL `NULL`; it is **SQLite's** `json_group_array` that returns the string `'[]'` (hence `NULLIF(…, '[]')` on the SQLite forward file only). The runbook must quote the PG down statement **verbatim as landed** (the `UNION ALL` of `jsonb_array_elements_text(allowed_models)` + `jsonb_array_elements_text(allowed_groups)` into `allowed_models`, then `allowed_groups = NULL`), not a paraphrase. **Gate rule unchanged: a mismatch between this text and the frozen file is a gate-WORDING defect to report — NEVER a licence to edit the migration SQL.**

**PG-migration CI status (fix-pass 2 carry):** the PostgreSQL `034` migration is **NOT CI-tested** (no live-PG unit pack exists); state that in the gate RESULTS file — the G0 pre-ship inspection on both hosts is the only PG-side pre-deploy verification, and the boot-abort recovery path (§5.6 item 5: helm rollback to the v202 anchor) is the named failure path.

**FE flags (fix-pass 2 carries):** (a) the FE must **NOT render a DENY-ALL / invalid-axis token as wildcard** — an INVALID axis renders as a distinct "restricted (unreadable grant)" state, never "All models"; (b) an **inverted-semantics integration test** belongs in the blast inventories (an end-to-end PATCH `[]` ⇒ grants-nothing probe against the deployed binary).

**Tier-1 Step-1 operational guard (PD-2 spillover — must be in the runbook).** The reverse UPDATE reads `allowed_groups`, a column the new binary's `ScanGrant` can classify as **invalid** (`GroupsInvalid`). An operator-run reverse statement must therefore be **`json_valid`-guarded** exactly like the forward repartition (`... WHERE allowed_groups IS NOT NULL AND json_valid(allowed_groups)`). **NULLIF is SQLite-shard only:** the PG down file uses plain `json_agg` (+) exception guards and **NO NULLIF** (fix pass C ship-safe); where a SQLite aggregation empties a side, `NULLIF(…, '[]')` applies — **only on the SQLite shard**. Reason: an unguarded cast raises mid-rollback, and a half-applied reverse is worse than either end state — the operator is then running an old binary against rows that are neither repartitioned nor restored. The down file already carries the `json_valid` predicate; **the runbook must quote it verbatim rather than paraphrasing it** (R3.12). Also note the reverse is **order-stable enough** for permissions (set membership is order-insensitive) but **not byte-stable** — a re-run after a partial reverse can reorder the JSON array. That is acceptable (`ScanGrant` returns a set) and must be stated so nobody treats array order as a contract.

---

## 4. Dependencies

| Half | Depends on |
|---|---|
| **FE** | **Phase 1 only** — the frozen wire contract (`plan.md` §2.8) and the API accepting `allowed_groups`. It does **not** need enforcement to compile or to be unit-tested; the FE tests mock `fetch`/callbacks. It **does** need Phase 2 for the live E2E probe (§T3.12 items 2–5). |
| **Ship** | Phase 1 + Phase 2 complete, all exit criteria met, gate green, boundary declared, G0 run, **CI build finished**. |

Blocks: nothing (terminal phase).

---

## 5. Tests owned by this phase

| # | Test | File | Assertion |
|---|---|---|---|
| FE-1 | `TokenForm — all kinds are selectable (image-gen + decision + group rows surface)` (renamed `:41`) | `TokenForm.test.tsx` | `:80`/`:81` `toContain`; **`:82`/`:83`/`:84` flipped to `toContain`** |
| FE-2 | `TokenForm — a selected group row is emitted under allowed_groups` | same | `onSubmit` args: group id ∈ groups array, ∉ models array |
| FE-3 | `TokenForm — a selected decision model is emitted under allowed_models` | same | decision id ∈ models array |
| FE-4 | `TokenForm — nothing selected submits two empty arrays` | same | `([], [])` (the API layer turns them into `null`) |
| FE-5 | `TokenForm — edit mode keeps a stale group id on the groups axis` | same | T3.2b rule |
| FE-6 | `TokenList — badge shows Wildcard only when both axes absent` | `TokenList.test.tsx` | three fixtures: both-absent ⇒ wildcard badge; models-only ⇒ model chips, no wildcard; groups-only ⇒ group chips, no wildcard |
| FE-7 | `TokenList — save splits axes and round-trips both` | same | callback receives both arrays; optimistic state updates both |
| FE-8 | `useApi — createToken sends both axes as null when empty` | `useApi.token-permission.test.ts` | POST body shape |
| FE-9 | `useApi — updateTokenPermission sends [] to clear and omits (or nulls) to keep` | same | PATCH body shape (the Q13/Ruling-2 two-state trap); explicit `null` asserted as keep (identical to omitted — Ruling 2 reversal); **note: Unrestricted/wildcard mode = create-only in FE edit** (promotion inexpressible; re-create the token) |
| FE-10 | `useApi — optimistic merge updates both axes` | same | local token state |

**⚠️ RECONCILED (gen-10 re-pin): the authoritative LANDED pin set is `plan.md` §13.1a — 88 Go + 33 FE = 121, ALL LANDED.** The 9 commission-named obligation pins landed in `1e65774` (4 test files, +1589); they are enumerated in §13.1a (Go rows 42 + 80–87) and are **no longer INCOMING**.

### 5.1 Cross-phase ruling pins counted by this phase (ownership stays with Phases 1–2)

**This phase owns no Go test bodies** — but it owns the **ledger** (T3.7), and the ledger is the anti-vacuous gate.

**SCOPING RULE (gen-10 re-pin):** the "no waiver" rule applies to **LEDGER ROWS in `plan.md` §13.1a** — each of the **121** enumerated rows (88 Go + 33 FE) must show a literal `--- PASS` exactly once. **All pins exist** (the 9 obligation pins landed in `1e65774`); there is no "not yet existing symbol" arm any more.

**Obligation → landed-pin mapping (NO commission obligation is deleted — each row names the pin that proves it):**

| Obligation | Pin that proves it | Status at HEAD |
|---|---|---|
| (a) precedence ladder 401→404→400→403→502 | `TestDecision_Precedence_401_404_400_403_502` | **LANDED (`1e65774`)** |
| (b) disabled-vs-out-of-scope no-leak | `TestDecision_DisabledVsOutOfScope_NoLeak` | **LANDED (`1e65774`)** |
| (c) 403 quoting the claimed string | `TestChat_ShadowClaimed_403QuotesClaimedString` + `TestChat_ShadowClaimed_AliasOnlyToken_403` + `TestAnthropic_ShadowClaimed_AliasOnlyToken_403` + `TestChat_ShadowClaimed_GroupGrant_AdmitsClaimedRequest` (ties to P-G6/P-G34 + the decision-lane twin) | **LANDED (`1e65774`)** |
| (d) group-id-in-chain never admitted, no 502 | `TestChainGroupEntry_NeverAdmitted_No502` (**LANDED `1e65774`** — the entry-chain group EXPANDS to its member; the raw group id is absent from `res.ModelList`; the pick is `(group, member)`; the request ends 200 after the primary 500s) + landed `TestChat_ChainEntry_OutOfScope_NeverSpawned` / `TestAnthropic_ChainEntry_OutOfScope_NeverAttempted` | LANDED |
| (e) fallback-position phantom pick | `TestChat_FallbackPositionGroup_NoPhantomPick` + landed `TestFallbackPositionGroup_Unreachable_Dropped` | LANDED (`1e65774`) + LANDED (§13.1a 78) |
| (f) PG malformed-row skipped-not-abort | `TestMigration034_PGMalformedRowSkippedNotAbort` + landed `TestMigration034_PGGuardShape` / `TestMigration034_PGuardShape_DownTwinAndGlobalScan` | **LANDED (`1e65774`) + LANDED (§13.1a 29/30)** |
| (g) decision-lane shadow/claimed group-arm 403 (P-G6) | decision-lane twin of (c): the claimed-id **resolved into the group arm** is exercised by the P-G6 design; at HEAD the write-side alias rejection pin `TestTokenAPI_StaleClaimAlias_Rejected` exists, and the **claimed-id 403 probe** is the live ship probe (T3.12 item 4b). Marked **LANDED** — `TestDecision_Group_NotGranted_403_ZeroAttempts` + `TestChat_ShadowClaimed_403QuotesClaimedString` + `TestTokenAPI_StaleClaimAlias_Rejected` + the live probe 4b. | partial (live probe) |



| Pin (added by this revision) | Owner | Ruling | The one thing the gate must see |
|---|---|---|---|
| **`TestRaceCoordinator_EmptyModelList_Refused`** | Phase 2 (T2.12) | **PD-10 (RED)** | The coordinator **refuses** an empty `models` slice (`err != nil`, nil coordinator), the literal `models = []string{cfg.ModelID}` is **absent** from `race_coordinator.go`, and the caller-side pre-spawn assert returns **403 ScopeExcluded** with the upstream counter `== 0`. **This is the dedicated regression pin the ruling demands** — a scope-empty list must never become an attempt |
| **`TestTokenAPI_PatchNilAxis_DoesNotHealDenyAll`** (canonical, P-G26) + store-layer half `TestUpdateTokenPermission_OmitsNilColumn` | Phase 1 (T1.3 / T1.11) | **PD-3 (RED)** | An omitted axis **or explicit `null`** (both = keep, Ruling 2) leaves the stored **column byte-untouched**: response 200, raw malformed bytes survive, a both-axes-keep PATCH executes no `UPDATE`, and a `DENY-ALL` row is never healed to `NULL`/wildcard (C2: ONE canonical name; the 400-rejection variant is rejected) |
| **`TestTokenAPI_Validation`** (extended) + **`TestTokenAPI_Validation_MalformedShape`** | Phase 1 (T1.8b / T1.11) | **PD-8 (AMBER)** | (a) malformed/non-array ⇒ **400**; (b) unknown id **or non-group id** in `allowed_groups` ⇒ **400**; (c) a **group id in `allowed_models` ⇒ accepted** (target-only semantics); plus the inbound-only rule (a stale id on an omitted axis does **not** 400) |
| **`TestMigration034_NullNotEmptyArray`** + **`TestMigration034_SQLiteDialectParity`** + **`TestMigration034_PGGuardShape`** + `TestMigration034_Repartition` case (6) | Phase 1 (T1.7 / T1.10) | **PD-4 + PD-2** | SQLite **and** PG parity: an emptied `allowed_models` is `IS NULL` (`NULLIF(…,'[]')` on SQLite, `json_agg` returning NULL on PG); the PG up file keeps the **inner per-row** `EXCEPTION WHEN invalid_text_representation` guard with **no `pg_input_is_valid`**; a `[broken` row is **skipped, not aborting** boot |
| **`TestAnthropic_Group_AllMembersOutOfScope_403_EnvelopeParity`** | Phase 2 (§5.3) | **PD-14 (GREEN)** | The anthropic scope-empty path emits the **chat-shaped 403** (`type: access_denied`, `model` = client string, **no `code` key**), never the anthropic 502 `group_unservable` |
| **`TestPackage_NoUltimateEscalation`** | Phase 2 (§5.3) | **PD-13 (GREEN)** | The audit is a **test**, not a doc: no non-test `UltimateModel*` / `ExcludeFromUltimateSwitching` reference, **negative control FIRE** |
| **`TestDecision_Precedence_401_404_400_403_502`** + **`TestPackage_NoPerAxisLenGuardOutsideAuth`** | Phase 2 (T2.13) | **PD-11 (GREEN)** | One pinned order on the decision lane; **zero** `len(…AllowedModels)` outside `pkg/auth` (the dead `handler.go:857` is gone) |
| **`TestChat_FallbackPositionGroup_NoPhantomPick`** + **`TestGroupExcludeScoped_ScopeOnlySignal`** | Phase 2 (T2.4) | **PD-12 (AMBER)** | `pick == nil` whenever `res.GroupID == ""`; the exclude seam reports `ScopeOnly` explicitly |
| **`TestChat_ShadowClaimed_403QuotesClaimedString`** + **`TestChat_ShadowClaimed_AliasOnlyToken_403`** + **`TestAnthropic_ShadowClaimed_AliasOnlyToken_403`** + **`TestChat_ShadowClaimed_GroupGrant_AdmitsClaimedRequest`** | Phase 2 (T2.16) | **PD-9 (AMBER)** | The 403 quotes the **claimed client string** and never the canonical group id; a token granting **only the alias** is **denied** (the resolved target authorizes — ADR D1; the narrowing is intentional); a token granting the **group id** is admitted through its alias |
| **`TestScopeExclusion_NeverWritesCooldown_MultiToken`** | Phase 2 (T2.3, fix-pass A/A2) | **Ruling 1 (cooldown-skip)** | A scope-refused offer writes **no** cooldown (`ExcludeAndReselect` not called, no `st.cooldowns[id]`, no `skipcache.Mark`) while it advances the binding via `RebindPast`/`RebindBackupPast`; after Token A's scope exclusion of M1, wildcard Token B still selects M1 |
| **`TestDecisionGroup_FilterDoesNotMutateCachedRow`** | Phase 2 (T2.9) | **PD-5 (AMBER)** | The filter ran on a **copy**; the cached `Members` slice is byte-identical afterwards |

**THE enumerated required-pin set is `plan.md` §13.1a — 88 Go + 33 FE = 121 LANDED pins, the single `added == expected` source of truth.** The table above is the obligation map; the rows below are the ruling-pin subset.

**Ledger rule for these rows (add to T3.7):** each must show a literal `--- PASS` **exactly once** in the gate's own Pack A/B log; a MISS fails the gate with no waiver. If any owner's implementation lands the pin under a different name, **the ledger row is updated to the code's name** (the arbitration is the code, not this table) — but the count must not change.

---

**Go tests owned by this phase: none.** The Go ledger is owned by Phase 1 (§phase1 §5) and Phase 2 (§phase2 §5); this phase only **counts** them (§T3.7) and runs the gate.

Anti-vacuous: each FE-1…FE-10 case title token must appear **exactly once** in the vitest log; `×` (skipped) count must be 0.

---

## 6. Coupling

- **Tight with:** **Phase 1** — the frozen wire contract this phase *expresses*: `allowed_groups` on `POST /fe/api/tokens`, `PATCH /fe/api/tokens/{id}` and every token response; the **two-state** PATCH per axis (key omitted or explicit `null` = keep — column untouched; `[]` = explicitly empty; ids = replace — Ruling 2/fix-pass 2); snake_case field names; the `ApiToken` shape (`types.ts:240-249`); and the validation contract (`allowed_groups` must exist and be `kind='group'`; `allowed_models` existence-only; inbound-only). The FE and the admin API must agree **byte-for-byte** on these, or the split at submit is meaningless.
- **Loose with:** **Phase 2** — the enforcement domain: this phase's live probes (T3.12) assert Phase 2's behaviour (403 `access_denied` vs 502, the wildcard invariance, the zero-upstream-call semantics) and this phase's gate measures the merged whole. Shared domain and shared vocabulary ("model_not_allowed"), no shared interface.
- **Independent of:** **the FE half of Phase 2** — the FE unit tests mock `fetch`/callbacks and need only Phase 1's contract; this phase needs no enforcement code to compile, to unit-test, or to be reviewed.

## 7. Risks

Phase-specific risks — each lands on **this phase's** files or tasks (`pkg/ui/frontend/**`, the gate run, the ship runbook). The global register lives in `plan.md` §15 / `decisions.md` §3; the id is cross-referenced where this phase carries the concrete instance of it.

| # | Risk | Impact | Likelihood | Mitigation (concrete — task / file / pin) |
|---|---|---|---|---|
| R3.1 | **FE and BE disagree about what a group is.** If the picker emits one flat list and the submit does not split by kind, the BE cannot tell a group from a model and the whole "group reach" feature silently degrades to "model reach" — the wire contract is lost at the last hop. *(global: `decisions.md` R-7)* | High | Medium | **T3.2** splits at submit by the row's `kind` (`'group'` → groups, everything else → models, missing kind ⇒ model per the legacy chat normalization) and the split is asserted on the **callback arguments**, not on the rendered DOM: **FE-2** (`a selected group row is emitted under allowed_groups`) and **FE-3** (`a selected decision model is emitted under allowed_models`); the second surface is covered by **FE-6/FE-7** (`TokenList`). |
| R3.2 | **The PATCH builder collapses "cleared" and "untouched".** Today's builder sends `null` for an empty selection (`useApi.ts:919-920`); after the contract change `null`/omitted = **KEEP** and `[]` = **CLEAR** ⇒ sending `null` for a cleared axis silently keeps the old grant, and the operator believes they removed access. *(global: `decisions.md` R-6 / Q13)* | High | Medium | **T3.1** gives the builder an explicit per-axis intent signature (omitted key = keep, `[]` = clear, ids = replace) with a comment at the call site explaining the three states (so the next reader does not "simplify" it back); pinned by **FE-9** (`updateTokenPermission sends [] to clear and omits to keep`) and **FE-8** (POST sends both axes `null` when empty). |
| R3.3 | **A per-axis picker admits the wrong kind (a group row in the models picker, or a model row in the groups picker).** | Medium | Medium | **LANDED design (fix pass C): the models picker EXCLUDES group rows** (`TokenForm.tsx:84-85`/`:159-181`; pin `TokenForm_PickerExcludesGroupModels`) and the groups picker carries `allowed_groups`; `grantAxes.tsx` `axisOfId` classifies stored-vs-new ids correctly; pinned by the grantAxes a–f cases. *(The old "delete the kind filter / one picker" contract is superseded — do NOT implement.)* |
| R3.4 | **The badge lies about the grant.** "All models" is correct **only** when *both* axes are absent (the conjunctive wildcard rule); showing it for a groups-only token tells the operator the token is unrestricted when it is not. | Medium | Medium | **T3.5** makes the display axis-aware (wildcard badge only when both axes absent; otherwise model chips and group chips separately); pinned by **FE-6** with three fixtures (both-absent ⇒ wildcard badge; models-only; groups-only). |
| R3.5 | **The red test is only half-flipped.** Widening the picker makes **three** assertions red (`:82` image-gen, `:83` decision, `:84` group — `:84` is the commission's named one); flipping only `:84` leaves the file asserting the retired contract, and its title (`:41`) plus header comment (`:5-15`) still name the old rule. | Medium | Medium | **T3.3** flips all three, renames the test title and rewrites the header comment in the same edit; the FE anti-vacuous presence check then requires every case token exactly once (a renamed-but-unexecuted case is a MISS). |
| R3.6 | **The gate boundary fence burns a cycle.** `034` + the registry append change `pkg/store/database/migrations/` and `pkg/store/database/migrate.go` — the paths the gate asserts are **0 changed files** (`.agents/tester/PACKS.md:35`); the gate would fail on a deliberate, declared change. *(global: `decisions.md` R-9)* | Medium | High | **T3.8** pre-declares the authorized boundary paths (the four `034` SQL files + `migrate.go`) and the paths that must stay at 0 files (`k8s/`, `.github/`, `pkg/config/`) **before** the gate runs; **T3.9**'s G8 re-checks exactly that set. |
| R3.7 | **A rollback opens the privilege hole.** An image-rollback **before** the reverse repartition leaves the two narrow tokens with `allowed_models = NULL` under a binary that maps absence to wildcard (`pkg/auth/token.go:116`) ⇒ they become **full-access tokens** on a public `/v1/*` endpoint — the exact defect class this commission closes. | High | Low | **T3.13** makes the reverse repartition **Step 1 of Tier-1** (not a deferred tier-3 action) and states the forbidden order as a prohibition; the ordering rule is required in the **runbook** (exit criterion 8) and in the **release note** (exit criterion 10(c)), and the Tier-2 backup restore remains the clean recovery. |
| R3.8 | **The gate times out spuriously.** `pkg/proxy` measured **131 s** at the 2026-11-17 gate ⇒ a `-timeout=110s` unit-pack run fails on a slow runner and is mistaken for a regression. | Medium | Medium | **T3.9** uses the release-gate slice timeouts (`-timeout=240s` inside the unchanged outer `timeout 300`, `PACKS.md:9-14`/`:20`) and the HOME quarantine + config.json hash guard (`PACKS.md:36`); the registry-quarantined flakes (`QUARANTINE.md:7/:11/:12`) are **adjudicated against the registry**, not silently ignored or "fixed". |
| R3.9 | **The tsc baseline drifts.** `types.ts` is in the change set ⇒ the committed baseline comparator applies; a **new** error location is a gate failure, and a baseline file edited in the same change set would hide it. | Medium | Medium | **T3.9**'s G5 requires **exact** parity with the committed baseline (zero NEW error locations, `PACKS.md:254`, `.agents/tester/rules/ensure.md:7`) and **the baseline file itself unmodified** — asserted by the gate diff, not by the test run. |
| R3.10 | **The FE case set is vacuous.** An added case that never executes reads as green; a case title that appears twice (or zero times) breaks the presence check without failing vitest. | Medium | Low | **T3.7**'s ledger requires every FE case token exactly once in the vitest log with `× = 0`, and **T3.9**'s G4 asserts the same; the `vite build` byte-identity check (G6) additionally proves the shipped SPA really contains the new UI. |
| **R3.11** | **G0 is run with the PRE-PD-2 SQL and dies on the row it exists to find.** *(**PD-2 — G0 half**.)* The original G0 queries 1 & 2 cast `allowed_models::jsonb`; a `[`-prefixed malformed row raises `invalid input syntax for type json` and the inspection **aborts instead of reporting**. An aborted inspection reads as "no finding" to whoever skims the artifact — the worst failure mode for a pre-ship check, and the exact blind spot the migration guard was fixed for. | **High** | Medium (one malformed row on either host) | **T3.10** mandates the version-agnostic `DO $$ … BEGIN … EXCEPTION WHEN invalid_text_representation THEN RAISE NOTICE …` per-row shape for **every** G0 query that casts, copied from Phase 1 T1.7's SQL; the malformed branch **prints the row** so the finding is a *report*, not a crash. G0-1's pass criterion is "output recorded", not "output empty" — an empty result and an aborted run must be distinguishable (the `EXCEPTION` arm is what guarantees the run completes). |
| **R3.12** | **The rollback rule is recorded as a caveat instead of a blocking step.** The historical failure mode for this class: the runbook lists "image rollback" as step 1 (the intuitive action) and mentions the reverse repartition as a footnote or a tier-3 option. Under time pressure someone follows step 1. | **Critical** | Low | **T3.11** states the two ratified conditions **as conditions**, and **§8 criterion 8** requires the ORDERING rule in the runbook and criterion 10(c) requires it in the release note — both naming the concrete consequence (`smoke-primary` / `smoke-primary-agentic` → full-access wildcards on the public `/v1/*` endpoint under the old binary, `pkg/auth/token.go:114-118`), so the cost of mis-ordering is unambiguous. |
| **R3.13** | **FE and BE ship decoupled.** *(**Q13(a) single-image coupling**.)* The FE builder's two-state PATCH contract (`[]` clear / omitted-or-`null` keep — `useApi.ts:913-934`) and the BE's explicit-empty serialization (`nil → NULL`, `[] → "[]"` — Phase 1 T1.3) are two halves of one contract. Old-FE-plus-new-BE sends `null` for a cleared axis ⇒ **keep** (silent no-op: the operator believes access was removed, it was not). New-FE-plus-old-BE sends `[]` ⇒ the old `len(x) > 0` guard writes **NULL** ⇒ **wildcard** (the escalation trap, reopened at deploy time). | **Critical** | Low today, High the first time someone ships the FE separately | **T3.11** makes single-image delivery a **ratified condition in the release note** (not a preference), **T3.9**'s G6 (`vite build` assets byte-identical to the committed `pkg/ui/static`) plus the same-digest check at D2 prove both halves are in the shipped image, and **§8 criterion 10(d)** requires the invariant stated. Any future decoupling needs a **re-ruling** — written down so it cannot be "just this once". |
| **R3.14** | **A cross-phase pin is missing from the ledger and the gate passes vacuously.** The revision added 10+ pins across Phases 1–2; a ledger that silently omits one (a rename, a moved file) reports `unlisted == 0` because the expectation was never stated. | Medium | Medium | **§5.1** names every ruling pin with its owner and the single fact the gate must see; **T3.7**'s rule is restated: a renamed pin is **updated in the ledger to the code's name** (the code arbitrates), the count must not change, and a MISS fails the gate with **no waiver**. |

---

## 8. Exit criteria

1. FE-1…FE-10 green; `vitest` all green with `× = 0`.
2. `tsc --noEmit` **exact** baseline parity (zero NEW error locations; the baseline file unmodified).
3. `vite build` produced assets byte-identical to the committed `pkg/ui/static` (house check).
4. Gate G1–G12 pass with the boundary declaration honored (only the pre-declared files under the fence) — including **G11** (the PD-10 bounded diff shape) and **G12** (the four greppable ruling checks).
5. Anti-vacuous ledger: exact counts, 0 MISS, 0 unlisted, 0 absent — **including every row of §5.1** (the cross-phase ruling pins), each with a literal `--- PASS` exactly once.
6. **G0 recorded** in the ship artifact: **G0-0 PG version** + G0-1 group-id inventory + G0-2 unreadable-row scan + G0-3 `smoke-15m` classification + **G0-4 `shadow_model_id` cross-check (PD-9)** — **all five** under the version-agnostic `DO $$ … EXCEPTION` guard (PD-2), with the explicit statement that G0 is an inspection, not a gate and not a write.
7. **Deploy evidence** per T3.12 items 1–9, including the repartition-preserved probe (narrow tokens still serve), the 403 probe, **the claimed-id probe (item 4b, PD-9)**, **the empty-scope probe (item 5b, PD-10)** and the non-scope telemetry confirmation (item 8, PD-13).
8. **Rollback anchors recorded** (helm rev N-1 / prior image digest / backup id) **plus the tier-1 ORDERING rule** (reverse repartition FIRST, then image rollback — T3.13) — the rule, not merely a caveat, must be in the runbook, stated as a **BLOCKING step** (ratified condition Q2(b)).
9. dcs pins pushed two-step (daoduc first), pins = highest deployed.
10. Release note carries **THE canonical list — `plan.md` §6.2 RN-1…RN-10** (single source; this section does NOT restate it): includes the `[]` flip, PATCH-null-keeps, **old-FE-clear-is-a-silent-no-op**, PD-9 alias narrowing, F3 deny-all-heals-on-save, the Tier-1 ordering rule as a BLOCKING step, and single-image coupling.

---

## 9. Parallelism note (the reasoning behind §6 Coupling)

- **FE half: INDEPENDENT.** Runs fully in parallel with Phase 2 once Phase 1's T1.1–T1.6 are committed. It cannot *merge* before Phase 1 (the API must accept the field) and cannot pass the **live** probes before Phase 2, but every unit test it owns runs against mocks.
- **Gate half: TIGHTLY COUPLED, strictly last.** It measures the merged whole; a failure routes back to the owning phase, it does not get patched here.
- **Ship half: STRICTLY LAST, sequential, no parallelism** (daoduc before primary; pin push after both).

**Explicit statement of the shared contract that enables the parallelism:** `plan.md` §2.8 (the wire shape) plus §2.2/§2.3 (the tri-state and the conjunctive wildcard) are frozen at the end of Phase 1. Backend (Phase 2) and FE (Phase 3) implement against that text, not against each other's code. Any deviation is a plan amendment, not a private decision.
