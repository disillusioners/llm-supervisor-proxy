# POST-MERGE FULL GATE — feature commission "decision-groups" — llm-supervisor-proxy

**Date**: 2026-11-17 · **Worktree**: `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-decision-groups`
**Merged tree under test**: `5ffcbe355d67c41eebb71105deb958385a23d29c` (branch `feature/decision-groups`)
**Merge parents**: OURS `1f6fa2b35a3e099922235f25e682edf8e90179cd` (30 commission commits + artifacts) + UPSTREAM v195 `dd8c58554f9465670705e07fd93c3c79f132adb8`
**Tree identity**: `ea57ec7ec8dde2613815b0b25e0757243e91bff1` — `5ffcbe3^{tree} == 3cf3cb8^{tree} == origin/master^{tree}`
**Logs**: `/tmp/dg-gate2/logs/` · **Inputs**: `/tmp/dg-gate/inputs/` (pre-merge) + `/tmp/dg-gate2/` (merged)
**Pre-merge gate** (certified `e5f0816`): `.agents/tester/RESULTS/decision-groups-gate.md`

> **Scope**: this is the **post-merge** gate on the MERGED tree, as ADR-16 requires ("re-run the FULL gate on
> the merge regardless of conflict"). `dd8c585..HEAD` = **31** commits = our 30 + the merge ⇒
> **`git diff dd8c585..HEAD` is exactly our contribution** — the basis for every ours-vs-upstream split below.
> CI run #200 is **out of scope**: this gate covers the SOURCE tree; image verification is a separate step.

---

## 0. Verdict

# 🟢 GREEN (ship) — with 3 named AMBERs, 0 REDs

Every code-and-test gate on the merged tree is GREEN, including all four re-derivations the merge required.
The pre-merge run's single RED (**B1**) is **substantively corrected** in the merged corpus; what remains is a
three-row documentation residue (§4.1) that should be corrected in a follow-up text-only commit — it is a
**gate-mechanics/doc item, not a product defect**, and the corpus itself carries the mitigation that makes it
non-blocking ("Do **not** ship an unbuilt sha").

**This file is a NEW artifact — it needs a follow-up commit** (the pre-merge artifacts were committed in `1f6fa2b`).

---

## 1. Gate matrix

| # | Gate | Verdict | Load-bearing evidence |
|---|---|---|---|
| G0 | Hermetic env + topology | 🟢 OK | `HEAD=5ffcbe3`; parents `1f6fa2b` + `dd8c585`; all 4 bases ancestors; `dd8c585..HEAD=31`; `a2952a9..dd8c585=54`; `tree(promo 3cf3cb8)==tree(latest 5ffcbe3)==ea57ec7` **YES**; worktree clean |
| G1 | gofmt ZERO-NEW-drift | 🟢 OK | base `dd8c585` flagged **46**, base `a2952a9` flagged **47**, head **46** ⇒ `NEW drift vs dd8c585 = 0`, `vs a2952a9 = 0`; touched `*_test.go` drift **0**; cross-checked with a second gofmt binary (identical sets). The 47→46 drop is upstream's *fix* to `pkg/proxy/normalizers/registry_config_test.go` — drift went **down** |
| G2 | **Pack A** (9 pkgs) | 🟢 **PASS** | **exit 0**, 140s; 9/9 `ok`; **1395** top-level `--- PASS`, **0** `--- FAIL`, 11 SKIP; proxy 130.867s, store/database 50.592s, decision 1.514s, ui 6.590s; 2422 top-level executions; `pkg/decision` 249 (160 decision-named) |
| G3 | **Pack B** (34 pkgs) | 🟠 PASS-with-quarantine (exit 1) | 1151 top-level PASS, **2** FAIL, 1 SKIP; 61s; sole failures = the 2 pre-registered httpbin tests at the **identical** signature `:1864/:1904` (403); 3/3 isolated re-runs identical; `pkg/mcp` has **0 files** in our diff; anchor proof `credclass`=1 / `pkg/proxy`=0 / `cmd|decision|store|ui`=0; package set unchanged (no new packages) |
| G4 | **Anti-vacuous** | 🟢 GREEN (Go) / 🟢 GREEN on contract (FE) + 🟠 label AMBER | **Go: 74/74** end-anchored `--- PASS` (64 A + 10 B), 0 MISS, 0 SKIP, 0 FAIL, each exactly once, 0 in both logs · **FE: 48/48** tokens present, 0 MISS, `×`=0, and **every one of our 48 cases has exactly one line in OUR 8 files** · set arithmetic vs `dd8c585` **exact** (added 74, removed 0, absent 0, unlisted 0) · collision with upstream's 103 names **0** · per-file attribution **identical** to pre-merge (§2) |
| G5 | Build (real entry) | 🟢 OK | `build ./cmd exit=0`; **26,700,265 B** (~26 MB); root stub `go build .` = 3,730,294 B, exit 0, `package main`/`test_load.go` = a different program (not the proxy) |
| G6 | Build (full tree) | 🟢 OK | exit 0, **0-byte** diagnostics |
| G7 | go vet | 🟢 OK | exit 0, **0-byte** diagnostics |
| G8 | Import guards (2 layers) | 🟢 OK | all **3** raw-text greps EMPTY (exit 1, no `GUARD FAIL`); wired `Makefile:54/:59/:64` + `@go test ./...` at `:68`; **Makefile untouched by the merge** (both diffs empty); **negative control FIRES** on a comment-quoted banned path; **4** `TestPackage_ImportGuard_*` literal PASS incl. `TestPackage_ImportGuard_GroupPolicyFiles_NoCacheImports` |
| G9 | FE vitest | 🟢 OK | **exit 0**; `Test Files 38 passed (38)`; `Tests 416 passed (416)`; `×` lines **0**; `✓` lines 416; all 17 roster files present |
| G10 | FE tsc parity | 🟢 OK | **exit 2**; baseline sha `1bc60a57…` **identical** before/after; per-file counts identical on all 9 files; file-set delta **empty both ways**; `TOTAL_ERRORS=18 TOTAL_FILES=9` actual == baseline. **Attribution**: OUR diff touched `tsc-baseline.txt` **zero** times; upstream's did (`+13/−3`, SettingsPage 6→5, TOTAL 19→18) |
| G11 | FE vite + static | 🟢 OK | **`G11 OK: rebuild is byte-identical to the committed assets`** (4 files, identical sha256); porcelain **EMPTY**; the merge commit carried **both** a FE-source change and a `pkg/ui/static` change (pairing rule satisfied) ⇒ the merge resolved assets by **rebuilding from the merged source**, not by picking a side |
| G12 | Isolated-boot smoke | 🟢 OK | `/healthz` **200** (`ok`) · `/fe/api/version` **200** (`{"version":"dev"}`) · `/v1/models` **200** (`{"data":[],"object":"list"}`); **0 panics**; clean SIGTERM (`Shutting down server...` → `Server exiting`); port 14398 released; real config sha+size+mtime **identical** and == the pre-gate capture |
| G13 | Boundaries (ours vs upstream) | 🟢 OK | **OURS** (`dd8c585..HEAD`): `k8s/` 0 · `.github/` 0 · `pkg/store/database/migrations/` 0 · `pkg/store/database/migrate.go` 0 · `pkg/config/` **0**. **UPSTREAM** (`a2952a9..dd8c585`) brought `pkg/config/config.go` (+87), 2 `pkg/config` test files, `migrate.go` (+5) and **migration 033** (4 SQL files: postgres+sqlite up/down). B4 re-check: bare `migrations/` = **0 files**, real path = **84 files** |
| — | Tier-1 fence | 🟢 OK | **10** files blob-IDENTICAL at **all four** revisions (`a2952a9`/`dd8c585`/`1f6fa2b`/`HEAD`); `engine_test.go` → our contribution **0** (§3.2) |
| — | Tier-2 fence | 🟢 OK | 8 files: **0** removed test funcs, **0** assertion-guard hits; the single removed line is **upstream's comment**; our delta vs `dd8c585` removes **0** lines; 3 touched `pkg/decision` test files → 0 removed lines, 0 guard hits |
| — | Marker / doctrine | 🟢 OK | `pkg/decision/group.go:57`; binary `grep -cF` = **1**, root stub = **0**; doctrine byte-identical in `suppression.go:8` and `policy.go:17` (`cmp` exit 0 each); the pinned test PASSes (both subtests) |
| — | Deletion accounting | 🟢 OK | `dd8c585..HEAD` = **+19796 / −271**; excluding `pkg/ui/static` = **+19783 / −258**; the 258 = docs/comments + the chat-kind→lane refactors + the quota/rate-limit vocabulary relocation + FE component edits. **No test assertion deleted** (independently confirmed: 0 removed test funcs + 0 assertion-guard hits) |

---

## 2. The four merge-specific re-derivations

### 2.1 Item 1 — the 74-name reconciliation vs the MERGED BASE `dd8c585` — 🟢 PASS

```
expected=74 · added vs dd8c585=74 · removed=0 · expected-but-absent=0 · unlisted=0
withdrawn absent from merged diff: OK · withdrawn absent from HEAD source: 5/5 · absent from both logs: 5/5
```

- **Collision with upstream: ZERO.** Upstream v195 added **103** test names (`a2952a9..dd8c585`); the
  intersection with our 74 is **empty in both directions**, and **0** upstream names appear in both logs — so no
  upstream test can satisfy one of our greps.
- **Exactly-once**: our 74 are each defined **exactly once** at HEAD; 0 pre-exist at `dd8c585` or `a2952a9`;
  `A_only=64 B_only=10 BOTH=0 NONE=0`.
- **Per-file attribution: IDENTICAL** to the known-correct pre-merge split — the merge moved **none** of our 74
  between files (per-test diff of defining file, merged vs `1f6fa2b` → empty).

| file | n | file | n |
|---|---|---|---|
| `pkg/decision/handler_group_test.go` | 34 | `pkg/proxy/handle_models_imggen_test.go` | 2 |
| `pkg/store/database/store_group_test.go` | 16 | `pkg/proxy/handler_anthropic_misroute_test.go` | 2 |
| `pkg/models/group_rules_test.go` | 6 | `pkg/decision/handler_test.go` | 1 |
| `pkg/ui/kind_rules_parity_test.go` | 4 | `pkg/proxy/handle_chat_misroute_test.go` | 1 |
| `pkg/credclass/classify_test.go` | 2 | `pkg/proxy/handler_test.go` | 1 |
| `pkg/decision/vocabulary_pin_test.go` | 2 | `pkg/store/database/models_manager_kind_test.go` | 1 |
| `pkg/modelscache/models_group_lane_test.go` | 2 | **TOTAL** | **74** |

- **Merge integrity (extra)**: all **103/103** upstream-added names are still defined at MERGED and all
  **103/103** still have a top-level PASS line — upstream's whole suite survived alongside ours.

### 2.2 Item 2 — `pkg/grouplb/engine_test.go` attribution — 🟢 FENCE PASS, ⚠️ premise corrected

**(a) Our contribution = 0.** `git diff a2952a9..1f6fa2b -- pkg/grouplb/engine_test.go` → **EMPTY**; blob
`27a910b30f` at both our anchor and our tip. **Our branch never touched the file.**
**(b) Upstream's change**: `a2952a9..dd8c585` → 14 insertions / 14 deletions.
**(c) Name at each revision**: `TestEngine_BoundAtRefreshedOnHit` at `a2952a9` **and** `1f6fa2b`;
`TestEngine_CreatedAtNotRefreshedOnHit` at `dd8c585` **and** `HEAD`.
**(d) Merge resolution**: the merge **took upstream's side** — HEAD's blob `c50a936937` == `dd8c585`'s, and
`dd8c585..HEAD` for that file is **empty**. Correct: our side had no change.
**(e) ⚠️ NOT a rename-only change — the assertion was INVERTED:**
```diff
-	if !after.After(before) {
-		t.Fatalf("in-TTL hit must REFRESH boundAt (#10): %v → %v", before, after)
+	if !after.Equal(before) {
+		t.Fatalf("in-TTL hit must NOT refresh createdAt (absolute lifetime): %v → %v", before, after)
```
plus two further assertion-bearing message-text changes (`boundAt`→`createdAt` in
`TestEngine_OnGroupChanged_SurvivorFilter`; `idle-expired`→`lifetime-expired` in `TestEngine_JanitorTTLSweep`).
This is upstream's sliding→absolute TTL semantics landing in the test contract. **The attribution and the fence
verdict are unaffected** (our contribution is zero either way), but anyone who accepted "rename only" would
wrongly conclude the assertion semantics were preserved.
**(f) Fence verdict**: our contribution to that file = **0 diff**; the other **10** Tier-1 files byte-identical
(blob-proven at all four revisions).

### 2.3 Item 3 — the 9 adapter methods in our `pkg/decision` test stub — 🟢 every fence leg PASS

| Check | Result |
|---|---|
| Stub located | `pkg/decision/handler_group_test.go` → `type stubGroupModels` (`ModelsConfigInterface` at `:139/:145`) |
| **Additions only** — every `^-` line (excl. diff header) | **EMPTY** (0 removed / renamed / re-signed) |
| The 9 adapters | `GetCredentialGroup`, `GetCredentialGroups`, `AddCredentialGroup`, `UpdateCredentialGroup`, `RemoveCredentialGroup`, `ModelsUsingCredentialGroup`, `ModelsUsingCredentialGroupsAll`, `GroupsContainingCredential`, `FlattenCredentialRefs` |
| **No re-signing** — signature set-diff | **EMPTY** |
| Assertion-line guard on the stub | **0 hits** |
| Call sites | **0 direct calls each** ⇒ compile-time interface satisfiers, consumed via `SetModelsConfig(&stubGroupModels{…})` |
| All 3 `pkg/decision` test files the merge touched | 0 removed lines, 0 removed test funcs, 0 guard hits |

**⚠️ Provenance correction to the brief**: upstream grew the **INTERFACE** — `models.ModelsConfigInterface`
(`pkg/models/config.go:458`) gained the 9 credential-group methods upstream — and **our merge commit `5ffcbe3`
added the 9 no-op adapters to our stub** (35 insertions, 0 deletions). The stub file is ours and is absent at
both `a2952a9` and `dd8c585`. The fence verdict is unaffected; the provenance record differs from the brief.

### 2.4 Item 4 — `pkg/ui/static` merge resolution — 🟢 PASS (the merge rebuilt correctly)

- The merge commit carried **both** a FE-source change **and** a `pkg/ui/static` change (pairing rule).
- A rebuild from the merged committed sources reproduces the committed assets **byte-for-byte**
  (`G11 OK`, 4 files, identical sha256) and the porcelain is **empty** ⇒ the merge resolved build artifacts by
  **rebuilding from the merged source**, not by picking a side.
- HEAD's bundle is a genuine **rebuild** (new content hashes on 2 of 4 assets), not a byte-copy of either parent:
  `SettingsPage-BDCXaBm2.js` (211.93 kB), `index-DdLeb6Xb.js` (313.51 kB), `index-Diz8jSFK.css` (52.83 kB),
  `index.html`.
- **Both feature sets are present in the shipped bundle** (string literals survive minification): ours —
  `Decision Model` ×10, `Decision Group` ×3; upstream's — `Credential Groups` ×3, `/fe/api/credential-groups` ×1.
  The CSS is byte-identical to upstream's; `index.html` is a fresh manifest pointing at both HEAD assets.

---

## 3. Flake classification (honest)

| Test | Pack | Classification | Evidence |
|---|---|---|---|
| `TestHandleTestMCPServerDirect_RedirectTo{Localhost,PrivateIP}Blocked` | B (`pkg/mcp`) | **NOT a flake — deterministic third-party failure**, pre-registered (QUARANTINE row 11) | 6/6 isolated attempts FAIL identically at `:1864/:1904`; httpbin returns a server-side `403 Forbidden redirect target: redirect target is an internal address` **before** the proxy's `CheckRedirect` classifier runs; both tests are **defined at MERGED_BASE**; `pkg/mcp` has **0 files** in our merged diff; neither name is in our 74; same signature as the 2026-11-14 and 2026-11-16 gates |
| `TestStoreEngine_PeakHourParity` | A | window-only flake — **did not fire** | `--- PASS` on the merged Pack A run |
| `TestStoreEngine_CloseLifecycle` | A | cleanup-race flake — **did not fire** | `--- PASS` |
| `TestEngine_GetOrSelect_TOCTOU_CoolingReverify` | B | known ~1/10 flake — **did not fire** | `pkg/credentiallb ok 4.415s` |
| `TestConcurrent{IncrementModelUsage,DifferentBuckets}_Stress` | B | SQLITE_BUSY — **did not fire** | `pkg/usage ok 56.777s` |
| `TestToolCallBuffer_MiniMaxFromLogFile` | B | file-gated skip | `--- SKIP` — log file absent (not a branch artifact) |

**No new flake was discovered.** No failure was silenced, re-run-to-green, or quarantined away.

### 3.1 A grep trap worth institutionalising

A naive `grep -c FAIL` returns **775** in A.log and **2204** in B.log — **every one** is the substring
`FAILOVER` inside test names (`FAILOVER` alone = 2202 of B's 2204). The anchored `^--- FAIL:` returns **A=0, B=2**.
Any future gate that greps failures without anchoring will read this suite as catastrophically broken.

---

## 4. AMBERs (non-blocking)

### 4.1 🟠 B1 residue — the ladder is fixed, three live rows still prescribe the old action

**The substance IS corrected in the merged corpus.** Rung 2 (`:1269`) is now the **NON-ZERO-DELTA CONTENT
RETRIGGER**, states "**NOT** `--allow-empty`", and explicitly refutes the old claim: *"a true empty commit has
an empty changed-file set ⇒ `on.push.paths` fires NOTHING ⇒ no event, no build, no publish … a `--allow-empty`
retrigger is therefore **useless** (it is the exact trap this rung previously encoded)"*. The two rows the
pre-merge gate cited (`:338` §8 and `:360` §10) are now **struck through and marked
`SUPERSEDED (B1, tester gate RED)`**. The workflow itself is **byte-identical** across `a2952a9..HEAD`, and every
publish step remains gated on `github.event_name == 'push'` (`:110` Login, `:117` Buildx, `:121` Extract-metadata
— the step that computes the tags, `:136` build-and-push, `:153` scan), so the chain
`push event ⇒ publish ⇒ per-SHA tag` is sound.

**But three rows still instruct the operator to do the thing that provably does nothing:**

| Location | What it still says | Why it matters |
|---|---|---|
| `phase3-plan.md:1272` (W4 summary row) | "…**empty-commit retrigger** as the autonomous emergency fallback" | a summary row an operator reads instead of the ladder |
| `phase3-plan.md:1501` (risk register **R6**) | "…the **empty-commit retrigger to master** (the documented CI-stall emergency exception)" | **the row an operator actions under stall pressure** |
| `architecture-recommendation.md:491` | "a manual run is a **first-class** recovery path" | un-marked and normative; asserts the opposite of the corrected truth (a dispatch is COMPILE-ONLY) |

**Why this is AMBER, not RED**: the mandated mechanism is correct at the ladder; the residue is a three-row
documentation defect in a tracked file; the failure mode is "the operator waits for a build that never starts",
and the corpus itself carries the mitigation for exactly that ("Do **not** ship an unbuilt sha", `:1501`).
**Recommended follow-up (text-only, no code)**: re-word `:1272` and `:1501` to "**non-zero-delta** content
retrigger (`:1269`)" and mark `architecture-recommendation.md:491` SUPERSEDED.

Two smaller corpus nits in the same area: the paths filter is cited as `docker-ci.yml:13-16` but the real block
is `:14-16` (`:13` is a branch pattern); and "Do **not** ship an unbuilt sha" is **bold-split**, so a naive
`grep -i 'do not ship'` returns **0** — the guard survives but is not plainly greppable.

### 4.2 🟠 FE case-label namespace collision with upstream (gate mechanics)

Upstream v195's credential-groups FE tests independently reused our label letters in **five upstream files**
(`CredentialsContainer` A1–A13, `CredentialGroupsTab` B1–B7/B9, `CredentialGroupForm` C1–C7,
`CredentialGroupMemberPicker` D1–D4, `CredentialsTab` E1–E3; **our-diff-added-lines = 0** for every one).
Consequence: **35 of our 48 tokens match 2–3 lines** in the whole vitest log instead of 1.

**The contract still holds**: every one of our 48 cases has **exactly one** line in **our** 8 files (48 distinct
lines; 0 tokens credited solely by a foreign file); `×`=0; 416/416 tests pass. The corpus's own FE
anti-vacuous rule is a **presence** check ("a MISS on any token ⇒ the gate fails") and there are **0 MISSes**.

**House-gate consequence**: the standing whole-log census ("each token hits exactly 1", POK
`fe-gate-05-token-census-grep`) is **no longer satisfiable tree-wide** and must become **file-scoped** — census
within our 8 files, which yields **48/48 at n=1** each with **no source change**. A future test-only commission
should also namespace our labels (`DG-A1`, …) to restore tree-wide uniqueness.

### 4.3 🟠 `pkg/skipcache` holds a paraphrase, not the byte-exact doctrine

`pkg/skipcache/skipcache.go:29-31` is a 3-line re-wrap of the doctrine; `:144-146` is a **divergent** variant
that replaces the ATTEMPT clause with the win-clear clause. The two **pinned** copies (`suppression.go:8-9`,
`policy.go:17-18`) are byte-identical to each other and to the pinned literal, and the pinned test passes — so
the gate holds; skipcache is a third-party layer with its own restatement, never a third pin.

### 4.4 🟡 Not provable here — explicitly not claimed

- The marker's **negative control** (absence from the LIVE v193/v194 binaries on the hosts) is a **deploy-time**
  check on the hosts. This gate proves only that the marker is compiled into the merged tree's real-entry binary.
- The local binary sha (`d563df8d…`) ≠ the KV's CI-built sha (`dc3f53c4…`) **because of the `-ldflags
  "-X main.Version"` stamp only** (`Makefile:34`; CI injects `APP_VERSION=run_number`). **Tree identity is
  identical** (`ea57ec7…` for `5ffcbe3`, `3cf3cb8` and `origin/master`) ⇒ not a content difference.
- The locally built binary carries **no `vcs.*` stamp** (`mod … (devel)`); the CI-built image does.
- The FE byte-identity proof used the **pre-existing** `node_modules` (no fresh `npm ci` reproducibility check).

---

## 5. House-keeping facts this re-run changed

- **The corpus is now TRACKED** — committed in `1f6fa2b` ("docs(decision-groups): the planning corpus, the gate
  artifacts and the tracking notes"), not untracked as it was pre-merge. Consequence: the pre-merge corpus text
  is **not git-diff-recoverable** (single commit), so the B1 correction can only be assessed in its merged
  state, not diff-proven. `phase3-plan.md` sha `b2a5de2f77a7e67dd13475e252c0b5fe38dab12537cc3a4b411e5e45d6c46e8b`.
- **The pre-merge artifact set is committed**; this file and the PACKS.md note are **NEW** and need a follow-up
  commit.

---

## 6. Artifacts

**Written by the tester in the worktree (uncommitted → needs a follow-up commit):**

| File | Change |
|---|---|
| `.agents/tester/RESULTS/decision-groups-gate-post-merge.md` | **NEW** — this report |
| `.agents/tester/RESULTS/gate-scratch-decision-groups.md` | extended with the merged-run working ledger |
| `.agents/tester/PACKS.md` | new 2026-11-17 post-merge note |
| `pkg/ui/frontend/tsc-baseline.txt` | **UNMODIFIED** (sha before/after identical) |
| any source file | **UNMODIFIED** — no source edits by any worker; `git status --porcelain` shows only `.agents/` entries |

**Raw logs** (`/tmp/dg-gate2/logs/`): `A.log` + `A.exit` + `A_pkgs.txt` · `B.log` + `B.exit` + `B_pkgs.txt` ·
`fe_vitest.log` · `fe_tsc.log` · `fe_vite.log` · `tsc_baseline_{before,after}.sha` · `static_{before,after}.sha` ·
`static_status.txt` · `build_all.log` · `vet.log` · `guard_tests.log` · `llm-supervisor-proxy` + `root_stub` ·
`smoke.log` · `config_real_{before,after}.{sha,stat}` · `config_smoke_{before,after}.sha` · `fmt_{base,head,new}_*` ·
`anticvacuous_go.txt` · `fe_token_census.txt` · `added_{merged_base,upstream}.txt` · `attrib74_merged.txt`

**CI run #200**: out of scope for this gate (source tree only). Image/digest verification is a separate step.

---

## 7. AMBER CLOSURE — both post-merge AMBERs were corrected in the working tree after this report

A subsequent edit to the corpus (working tree, **uncommitted** at the time of writing) closed **both** AMBERs
this report raised. Verified by reading the diff, not assumed.

### 7.1 §4.1 — the B1 residue: **all three live rows now corrected**

| Row | Before | After |
|---|---|---|
| `phase3-plan.md` **W4 summary row** (now `:1325`) | "…**empty-commit retrigger** as the autonomous emergency fallback" | "**rung 1** = re-run the ORIGINAL push-triggered run (admin-only fast path, user handoff); **rung 2** = **non-zero-delta content retrigger** … a `workflow_dispatch` run is **COMPILE-ONLY and NEVER publishes**" |
| `phase3-plan.md` **risk R6** (now `:1554`) — *the row an operator actions under stall pressure* | "…the **empty-commit retrigger to master** (the documented CI-stall emergency exception)" | "…the **non-zero-delta CONTENT retrigger** … **The empty-commit / `--allow-empty` variant CANNOT publish**: a true empty commit has an **empty changed-file set** ⇒ **`on.push.paths` fires NOTHING** ⇒ **no event, no build, no publish** (`docker-ci.yml:4-5`) — do **not** use it" |
| `architecture-recommendation.md` (the "workflow_dispatch is first-class" claim) | un-marked, normative | now carries a `⚠️ SUPERSEDED (2026-11-16, corpus B1 correction …)` block stating the dispatch is **COMPILE-ONLY and can NEVER publish** and pointing at the corrected ladder (rung 1 admin re-run; rung 2 non-zero-delta content retrigger) |

The ladder itself (`:1322`, rung 2) was already correct in the merged corpus; the three *summary/risk* rows that
an operator actually reads under time pressure were the residue — and they are now consistent with it.

### 7.2 §4.2 — the FE label-namespace collision: **the corpus census is now file-scoped**

- `phase3-plan.md` step 6's heredoc changed from `expected_fe_case_tokens.txt` (bare token list) to
  **`expected_fe_case_tokens.tsv`** — 48 rows of `<token> TAB <assigned file>` — and the census loop is now
  `grep -F "$f" "$LOGDIR/fe_vitest.log" | grep -Ec "…"` with the pass rule **exactly 1 hit in the assigned file**
  ("`MISS` — or a count != 1 — on any token ⇒ the gate fails").
- `phase2-plan.md` §4a gained a forward note recording the collision as **not a defect in the naming contract**,
  adopting the file-scoped census, and deferring a `DG-` namespace rename to a **future test-only commission**
  (a source change, explicitly out of scope for this release).
- **This is consistent with the measured post-merge result**: the merged gate found each of our 48 tokens
  carrying **exactly one** line across **our 8 files** (48 distinct lines, 0 tokens credited solely by a foreign
  file) — i.e. the new rule passes on the merged tree, where the old whole-log rule would have reported 36 of
  48 tokens as multi-hit (measured: 12 tokens ×1, 31 ×2, 5 ×3 whole-log).

> ### ⚠️ CORRECTION — the new file-scoped rule is a FALSE STOP **as written** (anomaly `df94bc75`)
>
> Verified by literal reproduction of the corpus's own command against the merged tree's log: **OK=0, MISS=48** —
> every token reports **0 hits**. Cause: the TSV's path column is **repo-root-absolute**
> (`pkg/ui/frontend/src/components/config/__tests__/DecisionModelsContainer.test.tsx`) while vitest's verbose
> reporter prints paths **relative to `pkg/ui/frontend/`** (`src/...`), so `grep -F "$f"` matches no log line.
> The census **intent is sound**: with the prefix stripped, the same command yields **OK=48, MISS=0** — every one
> of our 48 case ids appears **exactly once** in its assigned file.
> **Fix (one line, either form):** make the TSV path column relative to `pkg/ui/frontend/`, **or** strip the
> `pkg/ui/frontend/` prefix inside the `grep -F "$f"` filter. The corpus owner applies it — the tester does not
> edit the planner/approver's artifact, and the file is under concurrent edit.
>
> Net effect on the AMBER: the *collision* AMBER is genuinely addressed (the file-scoped census is the right
> mechanism and the merged tree satisfies it), but the **executable command must be corrected** before the next
> gate run, or a green suite reports 48 MISSes.

**Status: §4.1 is CLOSED in the working tree. §4.2's collision is addressed but its executable command needs the
one-line path fix above before it can be relied on.**


### 7.3 §4.3 — unchanged

`pkg/skipcache` still holds a paraphrase rather than the byte-exact doctrine; the two **pinned** copies
(`suppression.go:8`, `policy.go:17`) remain byte-identical to each other and to the pinned literal, and the
pinned test passes — so the doctrine gate still holds. Not corrected, and not a ship blocker.

