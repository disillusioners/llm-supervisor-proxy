# decision-groups — Approver Tracking

Commission: decision-model groups with text-lane-grade resilience on /v1/decisions + admin UI (plan approval pre-implementation).
Artifact: .agents/shared/planning/decision-groups/ (plan-overview.md entry; decisions.md; phase1/2/3; research-*.md). review-plan-*.md and architecture-recommendation.md excluded from evaluation scope.
Worktree: feature/decision-groups @ 2b987bf (base = origin/latest at plan freeze).

## Iteration History
| Iter | Date (UTC) | Workers | Result |
|------|------------|---------|--------|
| 001 | 2026-11-16 21:2xZ | 3 (see below) | REJECTED — 2 blocking (both operative-text defects; narrow surgical fixes) |

## Iteration 001 — Dispatch
Mode: Plan Approval. Large-plan exception (corpus ~774K): 3 section-partitioned workers, one skill each (plan-approval), cold context (artifact + verbatim requirement contract only).
- approve-worker-req  35c226d1-f8fd-4e41-9630-e4df5e8666c3 — requirement coverage (R1-R5, items 1-8, doctrine, layer rules) + cross-doc coherence + extend-vs-parallel record → APPROVED, 0 blocking, 7 notes. Evidence: 98 tool-backed messages.
- approve-worker-feas ed5ffda4-cf74-4a38-9df7-1f0861367904 — feasibility vs tree @ 2b987bf, ~40 file:line spot-checks (backend + FE) → APPROVED, 0 blocking, 10 notes. Evidence: 125 tool-backed messages.
- approve-worker-gates d4ff1856-2af6-46d4-b038-1a6b579fa563 — phase3 test contract (R1-R5 mutation angle, layer isolation, text-untouched) + ship/deploy runbook + rollback safety → REJECTED, 2 blocking, 5 notes. Evidence: 64 tool-backed messages.

## Aggregated Verdict: REJECTED (iteration 001)

### Blocking Issues

B1. Stale duplicated heredoc silently drops the V1 FE anti-vacuous pin from the executable gate.
- Where: phase3-plan.md, Execution order step 3 (lines 896-923) + step 6 (lines 999-1007).
- Expected: per G4 row (:499), W8 (:342, "each written once"), Amendment 4 §1 (:292), phase2 Amendment 2 §5 (:866) — 5 UPDATED FE test files incl. DecisionModelForm.test.tsx (the V1 carrier), file list written ONCE, V1's pass asserted.
- Found: $LOGDIR/updated_fe_test_files.txt heredoc appears TWICE — :896-903 (correct 5-file list) then :914-923 (stale Amendment-1-era 4-file list). Run top-to-bottom, the stale list WINS; step 6 greps only T1/F1 (:1006-1007) with a "(4)" comment. V1 is never anti-vacuously verified — a wrong rendered provider surface ships green; W8's "written once" claim is false on the artifact.
- Fix: delete the stale 4-file heredoc (:914-923); keep the 5-file written-once list; extend step 6 to assert T1+F1+V1 passes per updated file.

B2. Overview-promised anonymous-/v1/messages deploy verification is unoperationalized.
- Where: plan-overview.md §3 fence 16 (:188-189) vs phase3-plan.md §6.1 (:1276), §6.2 (:1300), Evidence-to-retain (:1406).
- Expected: fence 16 — the anonymous /v1/messages hole (closed v192) "must still be closed after deploy — verified in the deploy gate" (affirmative probe).
- Found: no affirmative unauthenticated /v1/messages ⇒ 401 probe in either host's health check or the evidence table; the hole appears only as reactive rollback trigger #4 (:1376) and R11 (:1427) — checked only if something else already broke.
- Fix: add the unauthenticated probe (expect 401) to §6.2 primary health check (and daoduc if parity desired) + an evidence-table row.

### Aggregation Decisions
- Both blockings KEPT (no downgrade): B1 is an operative-text self-contradiction defeating the corpus's own anti-vacuous doctrine (internal contradiction ⇒ REJECTED criterion); not covered elsewhere — approve-worker-req's empirical heredoc check verified a DIFFERENT artifact (the 73-name Go-test list). B2 is a promised-but-unlanded verification step for the v192 auth fix on a commission whose constraint 8 is "do NOT touch the v191-v193 fixes" (missing operationalized requirement).
- Cross-worker conflict resolved: approve-worker-req's coverage row item 8 cited the overview gloss ("verified in deploy gate") — superseded by approve-worker-gates' deeper runbook verification (most-specific-variant rule).
- No Note upgraded to Blocking (Cardinal 4). approve-worker-req's classification-ambiguity flag ruled: its N-1..N-7 remain Notes — two workers independently verified the operative/executable artifacts coherent and self-correcting; blocking severity reserved for the two operative defects above.
- Note dedup: gates-N1 ≡ req-N1 (73/69 count drift — merged; worker-req's empirical check shows no wrong gate outcome possible, only spurious-failure adjudication risk); req-N3 ≡ feas-note-10 (plan-overview §5 stale ship numbers — merged; fresh-verify rule + Stage 0-A designated catch).

### Non-blocking Notes (merged summary, for iteration-002 hygiene pass)
1. Count-hygiene drift in operative prose (73 vs 69/60+9/50/41): phase3 :388/:435/:499/:503/:535/:766-771/:1436/:1492; heredoc header "(16)" with 17 names (:823); step-3 preamble "69 ADDED" (:769); Tier-2 "16 NEW" (:616). Executable enforcement verified correct and mutually consistent — fix prose only.
2. phase1 §E9 row 35 pins ExcludeAndReselectForClass at :637-641/:640 — ground truth @2b987bf is :657 (F7's "rows now agree" claim wrong for this row); Stage 0-A sweep will re-verify.
3. R4 positive side un-named: recommend ChainTargetNotConflatedWithMember fixture save an inactive decision model as fallback_chain target and assert acceptance (write-time validation of chain-only models).
4. Citation drifts (feas notes 1,2,4,5,9): ±1-9 lines — E9#4 :165 vs :166; cmd/main.go wiring :329-336 vs cited :320-331; config.go :121/:266 vs :120/:255; GroupModelsTab :156/:165 vs :222/:228; phase2 §3.2 pre-re-anchor pin :291-301 vs :294-311.
5. Stale premises (feas notes 3,6,7,8): skipcache NOT in pkg/decision closure @2b987bf (v194 removed the seam); w-40/w-36 precedent claim false on both halves (harmless — standard Tailwind); overview "pool" field slip (actual: BackupOnly); Pack-B count 31/32 vs actual 33/34 (informational only).
6. plan-overview staleness: §1 header :8 (24c51ef), §5 ship numbers (:231, :237-240 v193-only rollback); N4/N5 (phase1 objective paragraph unmarked; phase3 CI-number self-contradiction :1186 vs :1369) — all covered by the banner's supersession rule + fresh-verify.
7. phase3 step-10 correction cites :474/:475/:482/:208; actual :502/:503/:510/:219 (substance correct).

### What Verified Clean (do not re-litigate in iteration 002)
- Requirement coverage: complete — all items 1-8, R1-R5, doctrine verbatim (pkg/skipcache/skipcache.go:29-31), layer rules incl. explicit MODEL-layer absence, pools/weights, no-new-sticky (D-6), vocabulary 5-surface pins T1-T5, CRUD/validation, text-untouched, FE vertical-nav spec, ship/deploy shape, fences.
- Feasibility: all load-bearing seams exist at claimed shapes (~40 spot-checks passed; F1 fallback-chain bypass hole verified real with correct fix target).
- Test contract: R1/R2/R3/layer-isolation/text-untouched each have killing tests incl. mutation angle; AST pin cache_layer_separation_test.go:752; flakes pre-registered per QUARANTINE.md; tsc parity mechanics correct.
- Runbook: master promotion (exact-parents + tree-identity + explicit-SHA refspecs), early-stall guard (#190 precedent), digest verify (repo-external, per-SHA tags), daoduc→primary order, non-destructive E2E E1-E10 (E8b keeps the group), rollback ladder per-host correct (helm vs kubectl set image; no digest field in k8s/values.yaml — verified), fresh-verify rule covers v195 drift.

Fix guidance: iteration 002 = apply B1 + B2 edits only (plus optional hygiene pass on Notes 1-7); re-dispatch fresh workers on the amended corpus.

---

## Iteration 002 — REJECTED (2026-11-16, rejection 2 of 3 — iteration 003 is the final before ESCALATED)

Delta scope (mechanically verified): ONLY phase3-plan.md modified (mtime 21:52:44; all other corpus files predate 21:52; HEAD unchanged 2b987bf). Iteration-001 APPROVED partitions (req 35c226d1, feas ed5ffda4) had byte-unchanged inputs — verdicts carried forward.

Dispatch: 1 fresh worker, plan-approval, cold context (same gates/runbook partition framing as iteration 001; zero rejection history or fix locations passed; neutral rubric tightened to "trace executable gate scripts top-to-bottom / every promised case asserted by a matching grep" and "overview deploy promises must be affirmatively operationalized").
- approve-worker-gates-2 c95e33a8-2abb-43ac-818a-65b640c5530b — REJECTED, 6 blocking, 11 notes. Evidence: 67 tool-backed messages; heredoc extracted and re-derived (73 unique names, 0 base collisions, 0 prefix pairs, 5 withdrawn absent); live refs/dcs pins measured.

### Iteration-001 blockings: BOTH VERIFIED FIXED (fresh independent confirmation)
- B1(001) heredoc duplication: fixed — updated_fe_test_files.txt written once (:897-904), 73-name heredoc re-derived clean, V1 in step-6 loop (:998-1000).
- B2(001) fence 16: fixed — affirmative unauth /v1/messages=>401 probes daoduc :1269 + primary :1293 + evidence rows :1400; triggers demoted to backstops.

### Iteration-002 Blocking Issues (all worker-raised; all KEPT after judgment-band review)

B1'. R4 positive/write-time acceptance unpinned. phase1 :940 / :1530-1536 vs decisions.md :656-673 (ADR-7/AC-R6 "Validation accepts an inactive/chain-only model as a chain target"). No 73-inventory test fails if rule F-2 is implemented "reject ANY decision-kind chain target" instead of "reject decision-lane GROUP target" — R4 verbatim "Validation MUST ALLOW" violated, ships green. Fix: positive save-acceptance test (chain target = inactive decision model). [Conflict with iter-001 req worker's R4 "covered" row resolved to this, the more specific variant.]

B2'. FE counterpart block contradicts UPDATED classification. phase3 :675-688 (byte-identity, "expect EMPTY diff", DecisionModelForm.test.tsx at :682) vs :896-903 (UPDATED heredoc, same file at :902) vs phase2 :866 ("Updated files are now 5"). Executing verbatim on a phase-2-conformant tree = guaranteed false STOP; operator "fix" drops the V1 pin. Fix: reconcile to the 5-UPDATED/7-LEFT-ALONE classification.

B3'. T1/F1/V1 assertion mechanism cannot match. phase3 :998-1000 greps case tokens in fe_vitest.log, but vitest default reporter prints per-FILE lines only (vitest.config.ts sets no reporter; no --reporter=verbose; no case-id-in-it()-title requirement). Systematic false MISS (false STOP) or token-collision false OK; 44 NEW-file cases likewise unasserted per-case. Fix: verbose reporter or case-id-in-title contract (and assert on it).

B4'. Real-config hash guard vacuous by ordering. phase3 :747 exports HOME=$(mktemp -d) BEFORE :749-750 hashes ~/.config/llm-supervisor-proxy/config.json → hashes the quarantine path → "REAL CONFIG UNTOUCHED" prints unconditionally; promised P0.5 guard (:457) cannot fire (actual protection comes from the quarantine itself — defect is evidentiary false assurance). Fix: hash the ABSOLUTE path before the export. [Kept blocking on false-assurance grounds — same class as iter-001 B2 precedent.]

B5'. E2E failover observation non-deterministic. phase3 :1314 (E4 throwaway "first in member list") vs :1318 (E7 expects throwaway attempted before commit) vs pick semantics plan-overview :147-148 / phase1 :554 (fresh weighted pick, empty key). Throwaway not guaranteed head with 3 default-pool members → E7 unsatisfiable / false NOT-VERIFIED (R12). Fix: throwaway = sole default-pool member, live members backup_only (or bounded retry loop).

B6'. Frozen rollback ladder strips two releases. phase3 :1351-1356 (tier-1=v194 :fd1649c, tier-2=v193 :b11d7c8/helm rollback 36) vs ADR-18 C2-3 (:398-400) + R11 (:1421) ("tier-1 = fresh-verified live-at-incident; never strip a newer release than the incident requires"); live dcs pins BOTH read 8d434ba (v195) — measured. Executing the table verbatim strips v195 AND v194 (cache-layer-separation) from the ensemble substrate; fresh-verify overlay cannot re-derive "prior release" from frozen sha7 + frozen helm revision. Fix: express tiers as re-read-at-ship (tier-1 = live-at-incident; tier-2 = revision immediately preceding tier-1).

### Aggregation Decisions (iteration 002)
- All 6 blockings KEPT: each worker-raised with doc:line + expected/found + tree-verified evidence; none duplicates a covered/Downgradable class (B1' completeness vs verbatim user semantic; B2'/B3' executable-gate self-contradiction/broken mechanism; B4' false-assurance guard; B5' user-verbatim E2E acceptance non-deterministic; B6' rollback safety contradiction vs live state).
- No Note upgraded by the approver (B1' elevation is the fresh cold worker's own classification, not an approver upgrade); no new approver-introduced blocking.
- Note dedup: N2≡iter-001-N1 (73/69 prose drift, executable artifacts verified correct); N3≡iter-001-note7 (config.go :502/:503/:509/:219 actual); N4≡iter-001-N5 (CI number). New notes: N1 (Pack A/B expected-output counts 10/31 vs measured 9/33), N5 (G11 static proof written twice; HEAD-only commit check), N6 (rollback does not revert dcs GitOps pin — pairs with B6'), N7 (porcelain-empty expectation unachievable vs house session-artifact convention), N8 (deploy text probe not group-scoped), N9 (E3/E4 endpoint+payload undocumented), N10 (R5 structural claim not pinned; conformant per AC-R8), N11 (doctrine CHAIN clause structurally untestable on this lane — disclose as such in RESULTS).
- Carried forward: iteration-001 clean verdicts stand (fresh worker re-confirmed the anti-vacuous Go-list machinery, CI/Makefile/chart/AST-pin/doctrine-line realities, non-destructive E2E design, R1/R2/R3/layer-isolation/text pins).

Next: iteration 003 = final before ESCALATED. Fix scope: the 6 blockings (all narrow, localized edits to phase3-plan.md + one phase1 test addition); hygiene notes optional.

---

## Iteration 003 — REJECTED (2026-11-16, rejection 3 of 3) → STATUS: ESCALATED

Delta scope (mechanically verified): ONLY phase1-plan.md (mtime 22:16:46, 2639L) + phase3-plan.md (22:19:27, 1531L) changed; decisions/phase2/plan-overview byte-unchanged; HEAD 2b987bf.

Dispatch: 1 fresh worker, plan-approval, cold context (same gates/runbook partition; neutral rubric tightenings: multiset duplicate-free checks, R4 both-directions, deterministic-E2E staging, incident-resolvable rollback ladder).
- approve-worker-gates-3 b50d0405-507b-4803-9131-45d2e4876319 — REJECTED, 6 blocking, 14 notes. Evidence: 58 tool-backed messages (go list, git ls-files counts, workflow if:-conditions, dcs pins, function-level diff semantics).

### Iteration-002 fixes: ALL SIX LANDED; four verified clean, two landed defective
- FIX 1 (R4 acceptance): VERIFIED COVERED — TestValidateGroupMembers_InactiveChainOnlyTarget_Accepted in heredoc, covers R4-b write-time acceptance; R4-c group-target rejection also covered (RED-1). ✓
- FIX 2 (FE classification): VERIFIED — 3 disjoint lists (5 NEW / 5 UPDATED / 7 LEFT-ALONE), all paths exist, byte-identity loop reads the authoritative left_alone file. ✓
- FIX 3 (case greps): LANDED BUT BROKEN → new B2/B3 (verbose reporter + token file present; foundation and exit-code semantics defective).
- FIX 4 (hash guard): LANDED BUT HALF-APPLIED → new B6 (before-hash fixed to $REAL_HOME_CONFIG; after-hash still quarantined $HOME).
- FIX 5 (E2E determinism): VERIFIED DETERMINISTIC — worker independently traced the real sweep (store_group_rewrite.go:47 → pickServableGroupMember :131, Phase 1 default :148-175 / Phase 2 backup :177-202); sole-default-pool throwaway = deterministic head; failover path real; non-destructive. ✓
- FIX 6 (rollback ladder): VERIFIED — tier-based, placeholders + at-incident measurement, per-host shapes correct, dcs-pin non-reversion stated (N14). ✓
- Planner self-corrections VERIFIED: duplicate name removed (74 names, sort -u=74, uniq -d empty — worker re-checked the multiset independently); arithmetic corrected (heredoc expectation :862 = "74 (64 Pack A + 10 Pack B)"). Stale "73" prose persists at :1504/:1518 (N3).

### Iteration-003 Blocking Issues (all worker-raised; ALL KEPT after judgment-band review)

B1''. W4 CI recovery path cannot publish. phase3 :1219 (W4 first) / :1448 (R6) vs docker-ci.yml — every publish step gated `github.event_name == 'push'` (Login :110, Buildx :117, metadata :121, Build-and-push :136, push:true :141); dispatch run = compile-only, NO per-SHA tag; demoted null-commit fallback also no-op per workflow header :4-7 (zero-delta push emits no event). After a stall (#190 precedent) the primary recovery produces nothing for step 5 to verify. [Supersedes iter-001/002 "W4 factually correct" gloss — most-specific variant.]

B2''. FE per-case gate rests on a non-existent contract. phase3 :1000-1021/:1070-1076 depends on "phase 2's NAMING CONTRACT puts the case id FIRST in every title" — no such contract in phase2-plan.md (0 hits); phase2 :425/:854 enumerate D6 as sub-cases D6a-D6d which the token pattern structurally cannot match. 47 spurious MISS → false STOP on a green suite, or operator loosens pattern → anti-vacuity lost. (Also: phase2 is byte-unchanged this iteration — the contract was never added where the fix claimed it.)

B3''. vitest exit code swallowed + per-case grep PASS-blind. phase3 :1069 `| tee` returns tee's exit (plan fixes exactly this for tsc two lines below via PIPESTATUS — not here); verbose reporter prints `× <title>` for FAILURES too → a failing FE case is recorded "OK FE case A1" and the suite's non-zero exit is swallowed. False-GREEN hole in a ship gate.

B4''. Boundary gate pathspec matches 0 files. phase3 :1063 (G13) pathspec `migrations/` → git ls-files = 0 (real path pkg/store/database/migrations/, 80 files); an UNREGISTERED new migration SQL (D-7 fence violation) ships green. Defect inherited from house gate (.agents/tester/PACKS.md:15) — but the plan asserts this fence (P0.3/G13) and it cannot fire for its own violation class. Fix: `pkg/store/database/migrations/`.

B5''. Tier-2/net-churn check is a no-op for body-only edits. phase3 :623-631/:654-673 use `git diff | grep -E '^\-func (Test...)'` — a body-only assertion change emits NO -func line, so pre_existing_funcs_touched.txt is empty for exactly the case the check exists to catch; weakened pre-existing assertions in the Tier-2 files phase 1 edits ship green. Mitigation verified real (11 Tier-1 files whole-file byte-pinned, all exist) — residual gap is the user-verbatim text-untouched fence for EDITED files. Fix: per-function body diff base-vs-HEAD.

B6''. G12 hash guard compares wrong pair. phase3 :1096-1111 — config_real_before.sha hashes $REAL_HOME_CONFIG (fix applied) but config_real_after.sha hashes "$HOME/..." = QUARANTINED home (:750) → real config exists ⇒ hashes always differ ⇒ "REAL CONFIG UNTOUCHED" can never print (guaranteed false STOP); absent ⇒ vacuous pass. Half-applied fix; smoke's own before/after (:1097) never compared either. Fix: hash $REAL_HOME_CONFIG in the after-step + add smoke-home before/after.

### Aggregation Decisions (iteration 003)
- All 6 blockings KEPT. B2''/B3'' are defects IN the landed fix (false STOP + false GREEN); B6'' is a half-applied fix (gate cannot complete on a normal machine); B1''/B4''/B5'' are pre-existing corpus defects surfaced by this pass's deeper executable trace (each = promised safety property provably unenforced: recovery artefact, migration fence, text-untouched-for-edited-files). Downgrading any would repeat the false-assurance class this approver has kept blocking in every prior iteration (001-B2, 002-B4 precedents).
- No Note upgraded; no approver-introduced blocking. Note dedup vs prior iterations: N1≡002-N1 (Pack counts 10/31 vs measured 9/33); N2≡001-N1/002-N2 (stale count prose; now also "(16)"/"(5)" per-file headers); N3 new (R1/R20 still say 73); N4≡CI-number; N5≡citation drift (config.go :502/:503/:510/:512/:219, cmd/main.go :469). New notes: N6 (overview :243-247 E2E staging prose now STALE vs fixed phase3 E4 — fix created cross-doc drift in byte-unchanged overview; also :237-240 frozen rollback bullet unmarked), N7 (E6/E7 success-path counter fields undefined — assertion-precision gap, failover observable via E6c/metering/200), N8 (cross-shell $PROMO_SHA/$SHA7/$LOGDIR — empty var would still proceed to helm; re-derive at deploy), N9 (R5 structural-only — one-liner disclosure owed), N10 (dcs pin precondition implicit; /fe/api/version catches stale), N11 (§7.2 stale-by-construction contradiction), N12 (porcelain-empty vs untracked corpus), N13 (G13 command omits pkg/config — covered by P0.7), N14 positive (rollback ladder verified sound; live pins 8d434ba both hosts).

### Final State
Verdict: REJECTED (3rd rejection). Per max-3 rule: Status = ESCALATED; no iteration 004 dispatch. Leader presents full tracking history to user.
Where the corpus stands: the SEMANTIC test contract is complete and verified (R1-R5 incl. BOTH R4 directions, layer isolation, doctrine verbatim, vocabulary, Tier-1 text pins — 74/74 names reconcile, duplicate-free); E2E verified deterministic + non-destructive; rollback ladder verified sound. The six unresolved blockings are all GATE/RUNBOOK MECHANISM defects (executable scripts that provably cannot fire, cannot match, cannot complete, or swallow failures) — localized, mechanical fixes, but each defeats a promised safety property on the ensemble's critical substrate.
