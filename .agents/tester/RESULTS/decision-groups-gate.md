# FULL GATE — feature commission "decision-groups" — llm-supervisor-proxy

**Date**: 2026-11-17 · **Worktree**: `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-decision-groups`
**Branch**: `feature/decision-groups` · **HEAD**: `e5f0816` (28 commits on the anchor `a2952a9`)
**Base (merge-base feature ↔ origin/latest)**: `a2952a9` · **Corpus base**: `2b987bf`
**Runbook**: `.agents/shared/planning/decision-groups/phase3-plan.md` (gates G0–G14, P0.x, B1–B6, Tier-1/Tier-2 recipes, flake registry)
**Log root**: `/tmp/dg-gate/logs/` (all raw logs) · **Inputs**: `/tmp/dg-gate/inputs/`

> **Gate scope**: this is the PRE-MERGE gate of the pre-merge HEAD `e5f0816`. `origin/latest` has moved to
> `dd8c585` and `origin/master` to `3891e9d` (the dispatch noted ≥ `0c4a7d7`). The anchor `a2952a9` remains
> an ancestor, so the step-8 merge will be a **real merge** against newer text-lane work. A **post-merge
> full re-gate is REQUIRED** (runbook § Ship runbook rule 3: re-run the FULL gate on the merge regardless
> of conflict). Nothing in this report certifies the merged tree.

---

## 0. Environment, hermeticity, scope

| Item | Value | Evidence |
|---|---|---|
| Toolchain | `go1.26.0 linux/amd64` (pinned cached toolchain) | `go version` → `go version go1.26.0 linux/amd64` (every worker) |
| Offline pins | `GOTOOLCHAIN=local`, `GOPATH=/home/nea/go`, `GOMODCACHE=/home/nea/go/pkg/mod`, `GOCACHE=/home/nea/.cache/go-build` | worker setup blocks |
| HOME quarantine | `export HOME=$(mktemp -d)/dg-home` per worker shell; `DATABASE_URL` unset | smoke log shows `Buffer storage initialized at: /tmp/tmp.z4Knvnt0b2/smoke-home/.config/...` |
| Real-config guard | sha `68a0669307f5d8965acadb9d9a3aa10eb2cae179760a4dc3a74b8828393080c8`, 1677 B, mtime 2026-10-18 06:08:16 — captured **08:18, before any test ran** | unchanged after every suite and after the boot smoke |
| Node / FE | node v24.21.0, npm 11.19.0, vitest 3.2.7; `node_modules` present | FE worker |
| Scope | **full gate, no reduction** — the commission is cross-module (28 commits, 66 files, +9694/−314) and this is the ship gate | `git diff --stat 2b987bf..HEAD` = 66 files changed |

---

## 1. Gate matrix

| # | Gate | Command | Verdict | Load-bearing evidence |
|---|---|---|---|---|
| G0 | Hermetic env + topology | `go version`; `git rev-parse HEAD`; `git merge-base` | ✅ OK | `HEAD=e5f0816fd1485deacdf96a9e1d256124da64de31`; `BASE=a2952a9`; `a2952a9..HEAD = 28` commits; `a2952a9` and `2b987bf` both ancestors of HEAD; status = only `.agents/` entries |
| G1 | gofmt ZERO-NEW-drift | `gofmt -l` over `git archive` base trees vs HEAD; `comm -23` | ✅ OK | base `2b987bf` flagged **47**, base `a2952a9` flagged **47**, head flagged **47**; `NEW drift vs 2b987bf = 0`, `vs a2952a9 = 0`; touched `*_test.go` drift **0**; gofmt = go1.26.0 |
| G2 | **Pack A** (9 pkgs) | `timeout 300 $GO test -v -count=1 -timeout=240s ./pkg/proxy/... ./pkg/store/... ./pkg/decision/... ./pkg/ui/... ./cmd/...` | ✅ **PASS** | **exit 0**, wall **134s**; `ok` × 9 (proxy **131.416s**, store/database 38.259s, decision 1.485s, ui 3.701s, cmd 0.017s); `--- PASS` **1339**, `--- FAIL` **0**, `--- SKIP` 11 (SSE heartbeat/long-wait + PG-conditional) |
| G3 | **Pack B** (34 pkgs) | `timeout 300 $GO test -v -count=1 -timeout=240s $(go list ./... \| grep -vE 'github\.com/disillusioners/llm-supervisor-proxy/(cmd\|pkg/(proxy\|store\|decision\|ui))(/\|$)')` | ⚠️ **PASS-with-quarantine** (exit 1 as executed) | 1107 top-level `--- PASS`, **0 real failures**; sole failures = the 2 pre-registered httpbin.org redirect tests at `handlers_api_test.go:1864/:1904` — **deterministic 6/6 isolated failures** (3× each), httpbin 403s redirect-to-internal **server-side** (curl-proven); `pkg/mcp` tree-hash **bit-identical to origin/master AND origin/latest** (`3d1d0c4b…`) ⇒ branch-orthogonal; TOCTOU + usage-stress did **not** fire; longest pkg `usage` 53.131s |
| G4 | **Anti-vacuous** | see §2 | 🟢 **GREEN** | **74/74** Go names each a literal end-anchored `--- PASS` (64 A + 10 B), 0 MISS, 0 SKIP, each matched exactly once · **48/48** FE tokens matched exactly once, 0 at n=0, 0 at n>1, `×`=0 · 5 withdrawn names absent from diff, HEAD source and both logs · 74 expected = 74 added vs `a2952a9` (gap 0, unlisted 0); the +6 vs `2b987bf` attributed to upstream `13a8e4e` (zero commission attribution) · no name pre-exists at either base |
| G5 | Build (real entry) | `go build -o $LOGDIR/llm-supervisor-proxy ./cmd` | ✅ OK | exit 0; **26,477,333 B** (~26 MB), sha `77b2c2a5…`; root stub `go build .` = 3,730,246 B, exits 0, **binds no port** (trap disproved, not assumed) |
| G6 | Build (full tree) | `go build ./...` | ✅ OK | exit 0, 0-byte diagnostics |
| G7 | go vet | `go vet ./...` | ✅ OK | exit 0, 0-byte diagnostics |
| G8 | Import guards (2 layers) | 3 Makefile raw-text greps + `go test ./pkg/decision/ -run 'TestPackage_ImportGuard' -v` | ✅ OK | all **3 greps EMPTY** (exit 1, no `GUARD FAIL`); wired `Makefile:52–66`, `@go test ./...` at `:68`; **negative control FIRES** on a comment-quoted banned path (ADR-14 raw-text trap) ⇒ the 3 empties are meaningful; **4** guard tests, literal `--- PASS` each, incl. `TestPackage_ImportGuard_GroupPolicyFiles_NoCacheImports`; exit 0 |
| G9 | FE vitest | `bash -c 'timeout 300 npx vitest run --reporter=verbose \| tee $1; exit ${PIPESTATUS[0]}'` | ✅ OK | **exit 0**; `Test Files 29 passed (29)`; `Tests 328 passed (328)`; `×` lines **0**; `✓` lines **328** (≥ 48) |
| G10 | FE tsc baseline parity | `bash -c 'timeout 240 npx tsc --noEmit \| tee $1; exit ${PIPESTATUS[0]}'` | ✅ OK | **exit 2** (expected); baseline sha `fa0a0d63…` **identical before/after** (file unmodified, `git diff HEAD -- tsc-baseline.txt` empty); per-file counts match on all 9 files (SettingsPage 6, ModelsTab 6, seven × 1); `TOTAL_ERRORS=19 TOTAL_FILES=9` **exact**; file-set delta **empty both directions** — no new error file, none resolved |
| G11 | FE vite build + static proof | `find pkg/ui/static \| xargs sha256sum` before/after + `npm run build` | ✅ OK | `G11 OK: rebuild is byte-identical to the committed assets` (4 files, identical sha256); porcelain **empty** ⇒ deterministic rebuild reproduces the committed assets; pairing: `ee4644e`/`ae5554a`/`6aedf00` each carry FE src **+** static; `5a26a78` is src-only but provably asset-neutral (type annotation + comment hoist; `git diff 6aedf00 HEAD -- pkg/ui/static` empty) |
| G12 | Isolated-boot smoke | boot the real entry on port 14399 with a quarantined HOME | ✅ OK | `/healthz` **200** (`ok`) · `/fe/api/version` **200** (`{"version":"dev"}`) · `/v1/models` **200** (`{"data":[],"object":"list"}`); **0 panics**; clean SIGTERM (exit 0); port 14399 released; both PIDs dead; **nothing on 8088 touched**; real config sha+size+mtime **identical** before/after and equal to the pre-gate capture |
| G13 | Boundaries | `git diff --stat $BASE..HEAD -- k8s/ .github/ pkg/store/database/migrations/ pkg/store/database/migrate.go pkg/config/` | ✅ OK | **0 changed files** for each of the five paths |
| G14 | `make test` umbrella | — | ⏭️ NOT RUN | optional per the matrix; G2+G3+G8 already cover it. `make test` is additionally known to swallow Go-test failures in CI (docker-ci.yml:104 `\|\| echo`) — CI green proves compilation only |

### Text-lane fence (Tier-1 / Tier-2)

| Check | Verdict | Evidence |
|---|---|---|
| Tier-1 whole-file byte-identity, **11 files × 3 bases** (`2b987bf`, `a2952a9`, BASE) | ✅ OK | **33/33 OK, 0 fails**; all 11 files existed at `2b987bf` (a pin on a non-existent file would be meaningless) |
| Tier-2 removed/renamed pre-existing test funcs (8 Go files) | ✅ OK | **EMPTY for every file** |
| Tier-2 assertion-line guard (8 Go + 5 FE updated files) | ✅ OK | **0 hits** on every file (no removed/changed assertion-bearing line) |
| Net churn | ✅ OK | base 2371 → head 2451 test funcs; **0 missing**; **0** pre-existing headers touched; **+80 added / 0 removed** (vs `2b987bf` — see §2 for the reconciliation) |

### Mechanism checks B1–B6

| # | Check | Verdict | Evidence |
|---|---|---|---|
| B1 | CI-stall recovery ladder coherent | ❌ **RED** | see §4.1 |
| B2 | FE naming contract holds across the run | ✅ OK | 48/48 tokens matchable, each exactly once; every title `<id> — <description>` with U+2014; **E8 exists** (so phase3's 48 is right and phase2 §4a's "47 (E1–E7)" is stale) |
| B3 | vitest PIPESTATUS + zero-`×` gate actually gates | ✅ OK | the gate was executed as written (`bash -c … exit ${PIPESTATUS[0]}`) and returned **exit 0**; `×` = **0**, `✓` = **328** ≥ 48 — a failing case would trip the `FAIL_LINES -eq 0` assertion and the non-zero exit |
| B4 | migration fence pathspec matches the real path | ✅ OK | wrong `migrations/` matches **0 files**; real `pkg/store/database/migrations/` matches **80 files**; boundary diff over the real path = **0 changed files** |
| B5 | Tier-2 assertion-line guard fires on a synthetic body-only edit | ✅ OK | **positive control = 1 hit** (`-\t\tt.Fatalf("NewModelsManager: %v", err)`), **negative control = 0 hits** on a pure-additions diff, worktree file **untouched** |
| B6 | real-config hash guard compares the SAME file before and after | ✅ OK | `REAL_HOME_CONFIG` assigned `:763` → before-hash `:764` → HOME export `:766` → after-hash `:1142` (order correct); `grep 'sha256sum "$HOME'` **empty**; empirically the real config sha/size/mtime unchanged and equal to the pre-gate capture; guard sensitivity proven on a copy (different hash ⇒ it can fire) |

### Marker + doctrine

| Check | Verdict | Evidence |
|---|---|---|
| Marker literal `decision_all_members_failed` | ✅ OK | production const `pkg/decision/group.go:57`; `strings \| grep -cF` on the real binary = **1**; root-stub control = **0** (so the proof is bound to the real entry) |
| Doctrine line verbatim in **both** pinned copies | ✅ OK | `pkg/decision/suppression.go:8–9` ≡ `pkg/decision/policy.go:17–18` → `cmp` **BYTE-IDENTICAL**; the pinned test `TestDecision_DoctrineLine_PinnedVerbatimAtSeam` PASSes and names `suppression.go` (seam of record) + `policy.go` |

---

## 2. Anti-vacuous proof — 🟢 GREEN (74/74 Go, 48/48 FE)

### 2.1 The 74 Go names — every one a literal, end-anchored `--- PASS`, exactly once

```
anti-vacuous OK: 74 / 74
MISS count: 0
--- every MISS line verbatim ---
(no MISS lines)
```

- **0 skipped**: the sweep `grep -E "^--- SKIP: <name>( |$)"` over A.log + B.log returned **empty** for all 74.
- **A/B attribution: 64 A + 10 B** — exactly the contract. 0 names appear in both logs (unambiguous split);
  each of the 74 has exactly **1** matching PASS line across A+B (0 multiplicity deviation).
- The grep was **end-anchored** (`^--- PASS: <name>( |$)`) — required, because
  `TestHandleModels_FilterDecision` (existing) is a strict prefix of the new
  `TestHandleModels_FilterDecisionGroup`. Proof the anchor works: an **unanchored** grep returns **2** hits
  for the short name (the wrong-reason PASS), the anchored grep returns exactly **1**.

### 2.2 The 48 FE case tokens — each matched exactly once

```
A1 1  A2 1  A3 1  A4 1  A5 1  A6 1  A7 1  A8 1
A9 1  A10 1  A11 1  A12 1  A13 1  B1 1  B2 1  B3 1
B4 1  B5 1  B6 1  B7 1  B9 1  C1 1  C2 1  C3 1
C4 1  C5 1  C6 1  C7 1  D1 1  D2 1  D3 1  D4 1
D5 1  D6 1  D8 1  D9 1  D10 1  E1 1  E2 1  E3 1
E4 1  E5 1  E6 1  E7 1  E8 1  T1 1  F1 1  V1 1

tokens with n=0: 0      tokens with n>1: 0
check-mark (✓) lines: 328      multiplication-sign (×) lines: 0
```

Sample matched lines (real leading ids, em-dash separator):

```
✓ …/DecisionModelsContainer.test.tsx > … > A1 — renders exactly two nav items labelled Models and Group, in that order
✓ …/DecisionModelsContainer.test.tsx > … > A10 — no ARIA tab roles, no aria-selected and no roving tabindex appear in the container
✓ …/GroupModelForm.decisionLane.test.tsx > … > D10 — the decision-lane member hint promises only per-credential resilience
✓ …/GroupMemberEditor.test.tsx > … > T1 — the text picker still offers an inactive chat model (the decision-lane exclusion must not leak into the text lane)
```

### 2.3 The 74-vs-added reconciliation (both open questions CLOSED)

```
added vs a2952a9 (the commission's own 28 commits): 74
added vs 2b987bf (includes re-anchored origin/latest work): 80
--- expected but ABSENT from the diff vs a2952a9 ---   EMPTY
--- PRESENT in the diff but UNLISTED (D-8 adjudication) ---   EMPTY
```

**The +6 are upstream, not commission work.** All six were introduced by
`13a8e4e fix(imggen): provider-aware probe body — 9router reference family`, which lives in the re-anchor
range `2b987bf..a2952a9` and is reachable from `origin/latest`; each is already present at `a2952a9` and
**none** is introduced by any commit in `a2952a9..HEAD`:
`TestExecuteTest_NineRouterProbeReferenceFamilyEndToEnd` · `TestExecuteTest_NineRouterQuota429SurfacesStatusFirst` ·
`TestHandleImgGenTest_NineRouterProbeUsesReferenceFamily` · `TestLiveE2E_NineRouterProbe` ·
`TestProbeBody_MinimaxDefaultUnchanged` · `TestProbeBody_NineRouterMatchesReferenceFamily`.
⇒ **74 expected = 74 added, gap 0, unlisted 0, zero commission attribution for the delta.**

> **Methodological note (the tester owns this one).** The first `comm` run produced garbage
> (`comm: file 1 is not in sorted order`) because the input list was in *ledger* order, not sorted — the worker
> re-ran with `LC_ALL=C sort -u` on all inputs. The corpus's own step-3 is safe here (it writes
> `expected_go_tests.clean` through `sort -u`); the defect was in the tester's hand-built input file, not in
> the runbook. The result was re-derived from sorted inputs and is the one reported above.

### 2.4 Per-file attribution of the 74 — the ledger's per-file split was wrong (total was right)

| file | measured | phase-3 ledger | verdict |
|---|---|---|---|
| `pkg/decision/handler_group_test.go` | 34 | 34 | ✅ |
| `pkg/store/database/store_group_test.go` | **16** | 17 | ❌ ledger −1 |
| `pkg/models/group_rules_test.go` | 6 | 6 | ✅ |
| `pkg/ui/kind_rules_parity_test.go` | 4 | 4 | ✅ |
| `pkg/decision/vocabulary_pin_test.go` | 2 | 2 | ✅ |
| `pkg/credclass/classify_test.go` | 2 | 2 | ✅ |
| `pkg/modelscache/models_group_lane_test.go` | 2 | 2 | ✅ |
| `pkg/proxy/handle_models_imggen_test.go` | 2 | 2 | ✅ |
| `pkg/proxy/handler_anthropic_misroute_test.go` | 2 | 2 | ✅ |
| `pkg/decision/handler_test.go` | 1 | 1 | ✅ |
| `pkg/proxy/handler_test.go` | 1 | 1 | ✅ |
| `pkg/proxy/handle_chat_misroute_test.go` | 1 | 1 | ✅ |
| **`pkg/store/database/models_manager_kind_test.go`** | **1** | *absent from the ledger* | ❌ ledger +1 |
| **TOTAL** | **74** | 74 | ✅ total reconciles |

**Resolution of the open 16-vs-17 item:** the 74th name,
`TestDecision_Vocabulary_CredentialModelSplit_AnthropicSaves_ModelRejected`, actually lives at
`pkg/store/database/models_manager_kind_test.go:726` (added by `f5ade55`) — a file the ledger omits entirely.
The phase-3 ledger needs one extra entry and `store_group_test.go` 17→16; the **grand total 74 is correct**.
Confirmed by two independent methods (`git grep` at HEAD and diff-derived per-file `+func Test` counts) which agree.
**This is a ledger-attribution defect, not a coverage defect.**

### 2.5 Collision guards

| Guard | Verdict | Evidence |
|---|---|---|
| (a) no expected name pre-exists at the base | ✅ CLEAN | empty at **both** `a2952a9` and `2b987bf` — a pre-existing test cannot satisfy any of the 74 greps |
| (b) Go prefix pairs | ⚠️ **3 found, safe** | `TestHandleChatCompletions_RejectsDecisionGroupMisroute` ⊂ `…_ThroughCachedConfig`; `TestHandleModels_FilterDecisionGroup` ⊂ `…_ThroughCachedConfig`; `TestAnthropicBoundary_DecisionGroupMisroute` ⊂ `…_ThroughCachedConfig` — each short name has its own separate PASS line; the end-anchored grep returns exactly 1 (an unanchored grep would return 2) |
| (c) FE token set | ✅ distinct, safe | 48 lines → **48 distinct**; the numeric prefix relations **do** exist (`A1<A10/A11/A12/A13`, `D1<D10`) — the `[^A-Za-z0-9]*` guard rejects a following digit, so each token credits exactly **1** line (A1 was not credited to A10) |

### 2.6 The 5 WITHDRAWN names

Absent from the diff (both bases), absent from HEAD source, and **0 occurrences anywhere in A.log or B.log** —
`TestDecision_Group_MarkedMember_DemotedNotFiltered` · `TestDecision_Group_NoCooldownWriteOnBenchedPick` ·
`TestGroup_RewriteGroupEntries_DecisionGroup_PicksDecisionMember` ·
`TestDecision_Group_MemberWithTwoCredentials_AttemptsBothBeforeAdvancing` ·
`TestDecision_Group_SkipCacheInstance_NotSharedWithTextLane`.

### 2.7 Whole-suite tally

| metric | A.log | B.log | A+B |
|---|---|---|---|
| top-level `--- PASS:` | 1339 | 1107 | **2446** |
| top-level `--- FAIL:` | **0** | 2 | 2 |
| top-level `--- SKIP:` | 11 | 1 | 12 |
| indented sub-test PASS / FAIL | 998 / 0 | 1196 / 0 | 2194 / 0 |
| package `ok` / `FAIL` / no-test | 9 / 0 / 0 | 30 / 1 / 3 | 39 / 1 / 3 |
| exit code | **0** | 1 | — |

Package accounting closes: A = 9 = 9 ok; B = 34 = 30 ok + 1 FAIL + 3 no-test-files (repo root, `pkg/logger`,
`scripts`). Pack overlap A∩B = **0** ⇒ no mis-sliced run. The 74 expected names are a **strict subset** of the
2446 top-level PASS lines (absent-from-PASS count = 0). The 12 SKIPs are 4 PostgreSQL-conditional, 7 SSE
heartbeat, 1 hang/idle-timeout, 1 file-gated log skip — **none** is among the 74.

---

## 3. Flake classification (honest)

| Test | Pack | Classification | Evidence |
|---|---|---|---|
| `TestHandleTestMCPServerDirect_RedirectTo{Localhost,PrivateIP}Blocked` | B (`pkg/mcp`) | **NOT a flake — deterministic third-party failure**, pre-registered (QUARANTINE row 11) | 6/6 isolated attempts FAIL identically at `:1864/:1904`; `curl https://httpbin.org/redirect-to?url=http://127.0.0.1:8080/sse` → **HTTP 403 text/plain** server-side, before the proxy's `CheckRedirect` classifier runs; `pkg/mcp` tree-hash **bit-identical to origin/master and origin/latest** ⇒ branch-orthogonal; same signature as the 2026-11-14 and 2026-11-16 gates |
| `TestStoreEngine_PeakHourParity` | A (`pkg/store/database`) | window-only flake — **did not fire** | `--- PASS` on the full Pack A run (not 23:59:00–23:59:59.999 UTC) |
| `TestStoreEngine_CloseLifecycle` | A (`pkg/store/database`) | cleanup-race flake — **did not fire** | `--- PASS` on the full Pack A run |
| `TestEngine_GetOrSelect_TOCTOU_CoolingReverify` | B (`pkg/credentiallb`) | known ~1/10 flake — **did not fire** | `--- PASS (0.10s)` |
| `TestConcurrent{IncrementModelUsage,DifferentBuckets}_Stress` | B (`pkg/usage`) | SQLITE_BUSY flake — **did not fire** | `--- PASS (7.01s)` / `--- PASS (15.98s)` |
| `TestToolCallBuffer_MiniMaxFromLogFile` | B (`pkg/bufferstore`) | pre-existing file-gated skip | `--- SKIP` — "Log file not found at ../../logs/minimax_stream_test.log, skipping test" (not a branch artifact) |

**No new flake was discovered by this gate.** The only failing pair is a pre-registered external-dependency row
with an unchanged signature — which the corpus's rule treats as *adjudicated, recorded* (a **different**
signature would be a STOP). No failure was silenced, re-run-to-green, or quarantined away.

---

## 4. RED / AMBER items

### 4.1 🔴 RED — B1: the CI-stall recovery ladder is self-contradictory (corpus/runbook defect, not a code defect)

- **`:1254` (rung 2) is false.** It claims `git commit --allow-empty … && git push origin HEAD:refs/heads/master`
  "produces a **push event ⇒ publish ⇒ a per-SHA tag**". A true empty commit has an **empty changed-file set**,
  so `on.push.paths` fires **nothing** — no event, no build, no publish. The corpus says so itself at `:1251`,
  and `docker-ci.yml:4–5` says it outright: *"a zero-delta push (empty commit / --no-ff merge with an identical
  tree) emits no workflow event at all"* (comment added by `955f5c7`). **The ladder's "no admin available" branch
  cannot publish at all.** Rung 1 (admin re-run) is sound — a re-run preserves `event_name == 'push'`, so the
  `:110/:117/:121/:136` publish gates re-fire.
- **Un-marked residue contradicts the fix in a LIVE table.** `:338` (the §8 "warnings folded" ledger) still reads
  *"`workflow_dispatch` **IS first-class** (`docker-ci.yml:6-11`) … **Manual dispatch on `master` rebuilds and
  publishes with no null-content commit**; the null-content-commit retrigger is the fallback"*, and `:360`
  (the §10 in-place-correction index) repeats it. Neither carries a SUPERSEDED marker, although the corpus's own
  convention (R16) puts one at every corrected site (26 such markers exist elsewhere in the file). An operator
  reading `:338` would take the wrong action.
- **Why this is not cosmetic**: the corpus directory is **untracked** (`?? .agents/shared/planning/decision-groups/`),
  so this defective runbook text is **committed at ship** unless corrected first; and it is the artifact the
  operator follows when CI stalls — which is exactly when a wrong action costs a release.
- **House precedent for the fix** (blueprint "Verification & Build Harness"): the *proven* retrigger is a
  **one-line null-content commit** (a real content change ⇒ non-empty changed-file set ⇒ event fires) followed by
  `--no-ff latest` → `--no-ff master` (wi-7769a812: `429a6ef→d9cdbea→487be74`). `--allow-empty` is not that.
- **Mechanically-valid repairs** (for the corpus owner — not applied by the tester):
  1. re-word rung 2 as a **non-zero-delta** retrigger commit (touches ≥ 1 file ⇒ changed-file set non-empty ⇒
     event fires) — and re-check the `tree(promo) == tree(latest)` invariant that such a commit breaks;
  2. or add `workflow_dispatch` to the publish gates at `:110/:117/:121/:136` so a dispatch can publish (a
     workflow edit is itself a content change that triggers its own push);
  3. or keep rung 2 admin-only and end the ladder at "hand off to the user", with no autonomous fallback.
  Plus: mark `:338`/`:360` `*(SUPERSEDED — B1 ladder, :1251–:1257)*`, replace the `:457` tilde command with the
  absolute-path form, and align `:1464` with the real pair actually produced.

### 4.2 🟠 AMBER — B6 residue ×3 (corpus text; the mechanism itself PASSES)

1. **The smoke's own before/after pair does not exist** — `:1127–1128` writes `config_smoke_before.sha`;
   `grep -c config_smoke_after` = **0** ⇒ a dangling write, never compared.
   **Worse than dangling — it is the WRONG FILE.** The real entry uses the **DB-backed** ConfigManager
   (`cmd/main.go` → `pkg/store/database/store.go`; migration `017_config_json` moved config from the file into
   the DB `configs` table), so the binary **never creates `config.json`** — it writes `config.db` (126,976 B) +
   `buffers/` under `$SMOKE_HOME`. The corpus's pair would compare "absent" vs "absent" forever. The correct
   quarantine evidence for the real binary is `config.db` + `buffers/` under `$SMOKE_HOME` (this gate captured
   exactly that). The file-based `config.json` rewrite still governs the **go-test** path (`pkg/proxy` sets
   `APPLY_ENV_OVERRIDES=1`), so the HOME quarantine remains the real protection for the suites.
2. **The evidence table over-demands** — `:1464` requires "the config.json before/after hashes (**real +
   quarantined**)"; only the **real** pair is produced (`:764`/`:1142`/`:1144`).
3. **The gate-matrix row P0.5 still uses the tilde form** — `:457` `sha256sum ~/.config/llm-supervisor-proxy/config.json`
   resolves to the **quarantine** after the `:766` export ⇒ the B6'' trap in the matrix row. The Execution-order
   block (`:763`) is correct.

### 4.3 🟠 AMBER — corpus documentation drift (no code impact)

- **`phase2-plan.md §4a` declares 47 FE tokens (E1–E7); the real suite has 48 (E1–E8).** `E8 — a member whose
  kind belongs to no lane is ignored and the surviving members decide` **exists** in `groupLane.test.ts`, so
  **phase3's 48-token list is correct** and phase2 §4a is the stale copy.
- **Heredoc comment labels are off by one in two blocks** — `store_group_test.go` says "16" and lists 17;
  `group_rules_test.go` says "5" and lists 6. The totals still reconcile to exactly **74 (64 A + 10 B)**.
- **The phase-3 G4 ledger's PER-FILE SPLIT is wrong** (total is right) — `store_group_test.go` holds **16**, not
  17: the 74th name (`TestDecision_Vocabulary_CredentialModelSplit_AnthropicSaves_ModelRejected`) actually lives at
  `pkg/store/database/models_manager_kind_test.go:726` (added by `f5ade55`), a file the ledger omits. The ledger
  needs one extra entry and 17→16 on `store_group_test.go`. Proven by two independent methods. **Attribution
  defect, not a coverage defect** (§2.4).
- **The corpus's "Pack A = 10 packages"** is a miscount of its own 9-item list (approver note N1); measured 9.
- **The corpus's "Pack B = 32 packages"** is stale — measured **34** (`+pkg/modelcache` 2026-11-16,
  `+pkg/credclass` this branch).
- **The corpus's "the module-prefix anchor is load-bearing" claim is imprecise** (and its parenthetical is false):
  the repo-relative variant `grep -vE '(cmd|pkg/(proxy|store|decision|ui))(/|$)'` yields the **identical**
  34-package set. The part that actually leaks is the **end-anchor-only** `…)$` variant → **38 packages** (pulls in
  `pkg/proxy/normalizers`, `pkg/proxy/token`, `pkg/proxy/translator`, `pkg/store/database`). The **`(/|$)` suffix**
  is load-bearing, not the module prefix. Corrected normatively in `PACKS.md`.

### 4.4 🟠 AMBER — `pkg/skipcache/skipcache.go` holds a paraphrase, not the byte-exact doctrine

`contains_exact_doctrine = False`; the file carries a **re-wrapped** variant at `:29–31` and a different,
shorter statement at `:144–146`. The two **pinned** copies (`suppression.go`, `policy.go`) are byte-identical, so
the gate holds — but the corpus's "MAY also appear read-only in skipcache" is paraphrase-only, not a third pin.

### 4.5 🟡 INFO — not provable here, explicitly not claimed

- The marker's **negative control** (absence from the LIVE v193/v194 binaries on the primary/backup hosts) is a
  **deploy-time** check on the hosts. This worktree proves only that the marker is compiled into the branch's
  real-entry binary.
- The **401 shape** on anonymous `/v1/messages` is **not** proven by the boot smoke: with a zero-model store the
  P1 boundary gate returns `404 model_not_found` **before** the auth gate (and `400 max_tokens is required`
  without `max_tokens`). **No anonymous request was served** (no 200, no upstream attempt). The 401/403/200
  contract is pinned by the branch's own test (`8113caf`), which **PASSED in Pack A**.
- The locally built binary carries **no `vcs.*` stamp** (`mod … (devel)`); the CI-built image does.

### 4.6 🟡 STANDING HOUSE ITEM — the two httpbin.org tests are not hermetic (not this commission's debt)

The G3 slice exits **1** because two `pkg/mcp` tests assert against a **live external service**
(`https://httpbin.org/redirect-to?url=<internal>`). Egress is **not** the problem — httpbin itself now refuses
redirect-to-internal targets **server-side** with `403 Forbidden redirect target: redirect target is an internal
address` (the 403 arrives before the proxy's `CheckRedirect` classifier runs), so the tests' `strings.Contains
(err, "redirect")` assertion can never hold. Re-running with egress would **not** help; the only durable fix is
to make the two tests hermetic (a local redirect server instead of httpbin). This has been the standing
`mcp_unit_test` FAIL-known state since 2026-11-06 (QUARANTINE row 11) and was the sole B-slice failure in the
2026-11-09, 2026-11-14 and 2026-11-16 gates as well. **Not a blocker for this commission** — the corpus's own
G3 criterion reads *"exit 0 **except the pre-registered quarantined rows**"* — but it is a house-hygiene debt
worth a commission of its own.

### 4.7 🟢 VERDICT ON G3's RAW EXIT CODE (explicit, so it is not read as a hidden RED)

Pack B exited **1**. Per the corpus's own G3 acceptance criterion — *"exit 0 except the pre-registered quarantined
rows (§ Pre-registered flakes)"* — and per the adjudication rule (*a quarantined test failing with a **different
signature** is a NEW failure ⇒ STOP*; this one has the **identical** `:1864/:1904` signature and the identical
403 cause, re-confirmed in three prior gates), **G3 is a PASS-with-quarantine**. Attribution is proven, not
asserted: `a2952a9:pkg/mcp` and `HEAD:pkg/mcp` are the **same tree** `3d1d0c4b…`, and
`handlers_api_test.go` is the **same blob** `39e3978e…` on both sides — the commission carries **zero** delta
under `pkg/mcp`, so it cannot have regressed it. Nothing was silenced, re-run-to-green, or quarantined away.

---

## 5. Ship-readiness verdict

**Verdict: 🔴 RED — one blocking item (corpus/runbook), all code and test gates GREEN.**

- **Everything that is code or test is GREEN**: G0, G1, G2, **G3 (PASS-with-quarantine — §4.7)**, **G4
  (74/74 + 48/48 — §2)**, G5–G13, the Tier-1 fence (**33/33** byte-identical across 3 bases), the Tier-2 fence
  (0 removed funcs, 0 assertion-line changes, 8 Go + 5 FE files), the boundary fence (0 changed files on all five
  paths), the marker pin, the doctrine pin (byte-identical in both pinned copies), and the import guards
  (2 layers, with a firing negative control).
- **The single blocking item is §4.1 (B1)** — a **text defect in the ship runbook**, not a defect in the feature.
  It blocks because the corpus is **untracked and would be committed at ship**, and because the ladder is the
  operator's only guide during a CI stall — where it prescribes an action that provably does nothing.
  The fix is small and mechanical (3 candidate repairs, plus 3 one-line marker/path corrections); the corpus
  owner (giter/approver) owns the file — the tester does not edit it.
- **§4.2–§4.7 are non-blocking**: documentation residue, a proven-harmless ledger attribution defect, an
  explicitly-not-claimed deploy-time check, and a standing house-hygiene debt (the two non-hermetic httpbin tests).
- **This verdict covers the PRE-MERGE HEAD `e5f0816` only.** `origin/latest` = `dd8c585` ⇒ the step-8 merge is a
  real merge, and the corpus's ADR-16 rule requires the **FULL gate to be re-run on the merge regardless of
  conflict** (§8).

---

## 6. Artifacts

**Written by the tester in the worktree (uncommitted — committed at ship per house convention):**

| File | Change |
|---|---|
| `.agents/tester/RESULTS/decision-groups-gate.md` | this report |
| `.agents/tester/RESULTS/gate-scratch-decision-groups.md` | running scratch ledger (per-worker findings, reconciliations, side-effect flags) |
| `.agents/tester/PACKS.md` | Summary 40 → **43** Go packages; new **"## Release-Gate Slices (normative)"** section (Pack A 9 pkgs, Pack B 34 pkgs + the `(/|$)` finding + the **corrected** migration pathspec + the config-hash-guard rule); new 2026-11-17 gate note; `mcp_unit_test` row reconfirmation |
| `.agents/tester/QUARANTINE.md` | row 11 — Attempts **+8F** (Pack B 2F + 6 isolated adjudication attempts), Status: 2026-11-17 reconfirmation (deterministic 6/6, curl-proven 403, tree-hash bit-identical to origin/master AND origin/latest) |
| `pkg/ui/frontend/tsc-baseline.txt` | **UNMODIFIED** (proven by sha before/after) |
| any source file | **UNMODIFIED** — no source edits by any worker; `git status` shows only `.agents/` entries |

**Raw logs** (read-only, `/tmp/dg-gate/logs/`): `A.log` + `A.exit` + `A_pkgs.txt` · `B.log` + `B.exit` + `B_pkgs.txt` ·
`fe_vitest.log` · `fe_tsc.log` · `fe_vite.log` · `tsc_baseline_{before,after}.sha` · `static_{before,after}.sha` ·
`static_status.txt` · `build_all.log` · `vet.log` · `guard_tests.log` · `llm-supervisor-proxy` + `root_stub` ·
`smoke.log` · `config_real_{before,after}.{sha,stat}` · `config_smoke_{before,after}.sha` · `fmt_*.txt` ·
`anticvacuous_go.txt` · `fe_token_census.txt` · `added_{a2952a9,2b987bf}.txt`

**Gate inputs** (`/tmp/dg-gate/inputs/`): `expected_go_tests.txt` (74) · `withdrawn_go_tests.txt` (5) ·
`expected_fe_case_tokens.txt` (48) · `expected_fe_test_files.txt` (5) · `updated_fe_test_files.txt` (5) ·
`left_alone_fe_test_files.txt` (7) · `tier1_files.txt` (11) · `tier2_files.txt` (8) ·
`config_real_before.sha` (captured 08:18, before any run)

---

## 7. Side-effect flags for the leader (out-of-scope writes by workers)

The dispatch forbade KB/POK writes. Three out-of-scope writes occurred, none of which affects the gate verdicts:

| Slug / id | Written by | Nature | Action available to me |
|---|---|---|---|
| POK `packb-anchor-count-34-at-e5f0816` | packb worker | Pack-B count drift 32→33→34 + anchor equivalence | `pok_curate(archived=True)` **REFUSED** — `pok_curator_required` (tester is not a curator) |
| POK `gate-02-realentry-boot-smoke-home`, POK `gate-03-anon-messages-401-gate-order` + 1 KB record | smoke worker | DB-backed config architecture + gate-ordering fact | same — curator-only; no KB delete tool exists |
| skill `ci-workflow-claim-verification` (`c57103f7-c6b4-47b6-9d91-abb232efcf13`) | mech worker | minted from the B1 findings | not a KB/POK write; noted for transparency |

**Dispatcher ownership**: the "no KB/POK writes" constraint was in my brief but I did not propagate it into the
worker briefs — that is my miss, not only the workers'. A constraint reminder was injected into the still-running
anti-vacuous worker as soon as this was observed.

**House bookkeeping (no code impact)**: `shared_meta_kv` `wt.claim.decision-groups.branch_sha` still reads
`a2952a9` (the stage-0a re-anchor point / merge-base) while the tip is `e5f0816` — 28 commits ahead. Not a
contradiction (`a2952a9` **is** an ancestor), but the KV should be refreshed at ship.

---

## 8. Post-merge obligation (not covered by this report)

`origin/latest` = `dd8c585`, `origin/master` = `3891e9d`. The step-8 merge of `origin/latest` into this branch is
a **real merge**; the corpus's ADR-16 rule requires the **FULL gate re-run on the merge regardless of conflict**,
and the ship-step rule requires that **every hotspot conflict be surfaced and re-derived, never forced**. This
report certifies the **pre-merge** HEAD `e5f0816` only.
