# review-plan-3 — Cycle-3 FINAL Delta Review, feature commission "decision-groups" (llm-supervisor-proxy)

- **Date:** 2026-11-16 · **Supersedes:** review-plan-2.md verdict (cycles 1–2 findings remain historically valid; this file is the final verdict of record)
- **Mode:** 🔴 Deep-Review continuation — cycle-1/2 governor revived a third time (2 councilors on models agentic + coding, full prior context; Round 0 only, **no refinement round needed** — first cycle with verdict-level consensus at zero factual disputes). Read-only compliance clean; no file written by the council.
- **Corpus state verified against reality (both models):** decisions.md **1178** (+ADR-18 `:1111-1156`) · phase1-plan.md **2633** (+Amendment 7 `:2534-2633`) · phase2-plan.md **889 — unchanged, confirmed** · phase3-plan.md **1505** (+Amendment 5 `:380-444`) · plan-overview.md 327 · advisory 990 untouched · HEAD `2b987bf`, tracked tree clean. New sections internally coherent with the corpus: ADR-18 rules the C2 closures + T1–T4 + STAGE 0 + advisory supersession; Amendment 7 names the doctrine seam, marks the last sites, adds the four staged vocabulary pins + Stage 0-A; Amendment 5 folds C2-2..C2-5 in place, re-derives G4 to 73/5/47, adds P0.0.
- **Session IDs:** governor `fac8c958-3347-464b-8207-b98e9421732a`; councilors agentic `8aefd01f…0f53`, coding `4bb0b56e…9994`.
- **Reviewer:** ensemble reviewer[v2] (controller/aggregator — findings produced by the council).

---

## VERDICT: APPROVED

Unanimous (agentic APPROVED · coding APPROVED; 0 critical / 0 warning). **All five cycle-2 blockers are closed in the artifacts — in-place, not merely marked. Nothing that remains would mislead an implementer to a wrong seam, an operator to a wrong abort target, or leave a ruled regression pin unenforced.** One named one-line remaining gap (OPTIONAL under the final-cycle policy) + a short optional-fix list foldable during implementation.

---

## A. Cycle-2 blockers — closure verified in the artifacts

| # | Verdict | Evidence (both models, re-located) |
|---|---|---|
| C2-1 doctrine seam | **CLOSED ✓** | `pkg/decision/suppression.go` at **all four pin sites** (`phase1:577`, `:1353`, `:1443`, `:1581`) + commit plan `:894` + Amendment-3 touch list `:1866`; residual `layer_upstream.go` mentions all carry DISSOLVED/SUPERSEDED markers (`:1352`, `:1517`, `:2080`, `:2629`). Never-edit constraint encoded ("`pkg/skipcache/skipcache.go:29-31` … CITED read-only, NEVER edited" — `phase1:2560-2561`; `decisions:1115-1118`; `phase3:413`) + optional read-only drift assertion (`phase1:2562-2565`). Seam ownership concrete (owns `decideAttempt`, D-C `CooldownUntil` branch, UPSTREAM+MODEL mark writers — `phase1:2550-2558`); `pkg/decision` correctly outside the AST pin's forbidden set. |
| C2-2 separate-instance sites | **CLOSED ✓** | All four marked (`phase1:404` corrected wiring; `phase1:1626-1627` AM-1 dissolves / AM-2 rationale void; `phase3:75-77` superseded; `phase3:1278` operator note now **CHECKABLE-TRUE** — "VERIFY LAYER SEPARATION, NOT INSTANCE SEPARATION", grounded in `cache_layer_separation_test.go:752-786`). Replacement wiring **verified real in-tree at `2b987bf`**: `SetSkipCache` zero hits repo-wide; actual seams `UpstreamSkipCache:`/`ModelSkipCache:` on the proxy config (`cmd/main.go:255-256`), one process-wide instance each (`:242` skipcache.New(), `:249` modelcache.New(), layer-isolation comment `:245-247`). |
| C2-3 R11 | **CLOSED ✓** | `phase3:1427`: tier-1 = fresh-verified live-at-incident; tier-2 = prior release; "the incident path NEVER strips a newer release than the incident requires … Never the v193-only two-release prescription (SUPERSEDED)". **No operative v193-only prescription remains anywhere in phase3** (remaining `b11d7c8` references are historical/measurement/fresh-verify contexts; the "R11" at `:1488` is a different, historical reconciliation table). |
| C2-4 fresh-verify scope | **CLOSED in §7.2** (one optional residue) | Rule extended to every plan-time deploy/dcs number (`phase3:404`); §7.2 audit table `:1344-1356` lists every frozen number (rev 36/37, gen 33/34, CI ≥195, both digests, the dcs "8-ahead" figure) with "stale — read at ship"; dcs figure declared DEAD (`:1353`; dcs synced `7f89eac`) and escalation rule rewritten (`:1355`). Residual: R14's trigger description (`:1430`) still carries the dead 8-ahead figure — safe direction (fires on measured drift only) → OPTIONAL #2. |
| C2-5 Tier-1 gate | **CLOSED ✓ — assert-set equality BOTH directions** | Tier-1 table = **11 rows** (`phase3:588-602`) = mechanical command = **11 files** (`:638-648`); `comm` both directions ∅. Resolution exactly as cycle-2 prescribed: `win_clear_winner_identity_test.go` **asserted in the command** (`:645`; cell `:597` names the clone's destination `pkg/modelscache/models_group_lane_test.go`); `group_rules_test.go` Tier-1 row **deleted with rationale** (`:601-602`) — lives correctly in Tier-2 only (`:616`). |

## B. New/claimed items — verified

1. **T1–T4 ENCODED, named + staged ✓** — `TestDecision_Vocabulary_OutOfSetProvider_Rejected_AtAllGates` (**T2, Stage 3**, `pkg/ui/kind_rules_parity_test.go`) `phase1:592`; `TestDecision_Vocabulary_SourceLiteralPin_FiveSurfaces` (**T3, Stage 6**, new `pkg/decision/vocabulary_pin_test.go`) `:593`; `TestDecision_Vocabulary_CredentialModelSplit_AnthropicSaves_ModelRejected` (**T4, Stage 3**, `pkg/store/database/models_manager_kind_test.go`) `:594`; `TestDecision_Vocabulary_RegistryKeySetEqualsGateAllowList` (**T5-Go, Stage 6**) `:595`; duplicated in Amendment 7 §G3 `:2592-2597`; T1 covered by the two existing mixed-providers tests (`:2587-2590`). **G4 = 73/5/47 CONFIRMED mechanically, whole-document**: 73 heredoc names all present in phase1, 0 one-sided; phase1's 93 distinct − 73 = 5 withdrawn + 15 base-existing; ledger matches (34/17/5/4/2/2/2/2/1/1/1/1 = 73; Pack A 64 / Pack B 9, `phase3:500-501`, echo `:860`); FE 47 unchanged.
2. **STAGE 0-A ✓ (all four elements)** — kickoff re-anchor + bounded pin-reverification (`phase1:114-131`; Amendment 7 §G4 `:2599-2611`), anchor-of-record in KV, unconditional full-gate re-run; `phase3:452` **P0.0** (blocking); `decisions:1146-1152`. The "it moved again mid-review (`a2952a9`/`8d434ba`)" fact is recorded in both — the base-currency class is now structurally guarded at kickoff, not chased per-cycle.
3. **Advisory supersession ✓** — record at `decisions.md:1146-1150` (coding cites `:1154-1157` for the same record) names `architecture-recommendation.md:741`/`:778`, declares them retracted proposals under ADR-17/18, states the architect's file is not edited. **Verified untouched**: still 990 lines; `:741`/`:778` still contain the original `store_group_policy.go` recommendation — governed by the ADR record.
4. **Prose/script split — NOT DONE as claimed** (named honestly): stale comment counts survive **inside** the step-3 script block (`phase3:766` "FINAL 50", `:769` "69", `:771` "Pack A = 41") above the correct 73-name heredoc (`:860`); duplicate 4-file `updated_fe_test_files.txt` heredoc (`:918`) still overwrites the correct 5-file list (`:897`, dropping `DecisionModelForm.test.tsx`); no `grep V1` case check (`:1006-1007` checks T1/F1 only) though G4 claims "T1, F1, V1 each show a pass". **OPTIONAL** (both councilors): comments don't break copy-paste execution; V1 still executes (and fails loudly) inside the full vitest suite (G9) — the hole is the anti-vacuous *presence* dimension for one named case. Highest-value two-line fix: add the V1 grep; delete `:918`.

## C. Trust-but-verify — FALSE ALARM, confirmed from the artifact (both)

All four pin tests exist, named and staged (operative Test-plan table `phase1:592-595`; Amendment 7 §G3 `:2592-2597`; ADR-18 `decisions:1142-1144` "T1–T4 — ENCODED, no further deferral (LEADER DECISION)"; all four mechanically present in the phase3 73-name heredoc). The "still NOT authorized" claim lives **only** in `review-plan-2.md:54`/`:71` — cycle-2 review text written before Amendment 7 landed, echoed by the planner's worker as report boilerplate. **Record as FALSE ALARM — report-only, no doc change needed.** (The cycle-2 text was accurate for its cycle; it is superseded by this verdict.)

## D. One remaining encoding item — REMAINING GAP, severity OPTIONAL

Wishlist id **`404ad40b` appears nowhere in the corpus** (grep zero hits, all files); phase2 confirmed unchanged (889 lines) — the staleness passage still reads as an open item ("Optional mitigation **not** implemented (not ruled) … Flagged for the leader", `phase2:868-872`; Risk #1 `:625`). The leader's ACCEPTED-BY-LEADER disposition is **not encoded**. Under the final-cycle policy: non-blocking — the passage is factually accurate (both bounds stated, residual owned, no operator action depends on the wishlist id); a traceability omission, not a misleading encoding. **Fix = one line** in phase2 (or an ADR-18 addendum): "ACCEPTED-BY-LEADER this release; wishlist 404ad40b."

## OPTIONAL IMPROVEMENTS (non-blocking; "blocking trivia" for the implementation pass)

1. Add the `grep V1` case check + delete the stale duplicate heredoc (`phase3:918`, `:1006-1007`) — makes G4's "T1, F1, V1" claim true in the script (highest value; two lines).
2. R14 trigger re-scope (`phase3:1430`): replace the dead "8 commits ahead" figure with "measured drift > 0".
3. Stale count prose sweep: `phase3:11` ("#23–#27" → 23–30), `:766`/`:769`/`:771` (50/69/41 → 73/64+9), §1 split `:388` (60 → 64), `:1341` ("the 50"), R6 `:1388`/`:1483` (names two non-existent pins), R8 `:1390`/`:1485` (old G11 verdict vs one-rule verdict `:518`).
4. plan-overview.md: header still `@ 24c51ef` (`:8`), banner still "CYCLE 1" (`:14-23`), "Open items: T1–T4" at `:21` now false.
5. Ship-script plan-time value (`phase3:1083`, `origin/master` = `b11d7c8`) — delete; rely on fresh-verify.
6. phase2 ACCEPTED-BY-LEADER label + wishlist pointer `404ad40b` (the one remaining gap; one line).
7. Advisory RED-1 divergence (`architecture-recommendation.md:303`/`:724` still prescribe `FailedGroupID` vs the plan's endorsed drop-and-continue; §7a `:964-990` doesn't record the adjudication) — advisory-side note for the architect; no plan change.
8. `phase1:703` stale sub-cite `:191` (second fn at `:217`); `phase1:894` cosmetic double-bracket.

## Ship-time reminders (both councilors)

`origin/latest` = `a2952a9`, `origin/master` = `8d434ba` — already past the corpus base. This is precisely what STAGE 0-A / P0.0 exists for: re-anchor at kickoff, bounded pin-reverification, anchor-of-record in KV, full-gate re-run. Honour the fresh-verify rule at ship; the negative-control image checks rely on the per-SHA verify-image cache.

## Process notes

- First **unanimous APPROVED** at zero factual disputes, from the same councilors who blocked twice — convergence is meaningful rather than lenient: closure was verified against the artifacts at re-located line numbers with in-tree checks (`SetSkipCache` zero-hit; `cmd/main.go:242/:249/:255-256`).
- Both councilors applied the final-cycle severity policy honestly — refusing to bless one inaccurate **claim** (prose/script split) while classifying its residue OPTIONAL, and refusing to bury the one unencoded disposition (D).
- Read-only compliance clean; both instances completed; sticky error flag cleared; manifest removed; no files written by the council.

**For the approver: the corpus is implementation-ready — APPROVED, with one named one-line remaining gap (phase2 ACCEPTED-BY-LEADER label + wishlist `404ad40b`, OPTIONAL) and a short optional-fix list foldable during implementation under the leader's authority.**
