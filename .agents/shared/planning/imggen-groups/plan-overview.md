# Plan Overview — imggen-groups (image-gen model groups + Text FE sub-tab restructure)

Date: 2026-11-20
Status: **Complete** — all phase plans written, verified on disk, cross-phase assumptions reconciled (this file). **Revised 2026-11-20** — reviewer verdict APPROVED-CONDITIONAL folded: 5 adjudicated rulings (D-14..D-18) + adopted warnings applied doc-only across the corpus (see "Revision fold" below).

> **Execution note (iteration 002):** all plan citations are **base-`5cf1ecdc` anchors** — implementers MUST re-anchor line references against the working tree before quoting or editing (known drift: the D-15 chat-side misroute cites are off by 100+ lines; several pins are ±6 lines).
> **Post-implementation ledger pointer (2026-11-20):** the W-4/W-7 flipped-pin ledger (ADDED `TestMemberLane_ImageGen_IsImageGenLane` + MODIFIED-in-place `TestValidateGroupMembers_LegacyEmptyKind_IsChatLane`, rename forbidden; G3 `member_must_be_chat_kind` parity matrices MODIFIED-with-justification in `pkg/models` + `pkg/ui`) is recorded as a dated addendum at `phase1-plan.md` §3.8 — normative; the §3.8 body is SETTLED and not rewritten.
Base / worktree: `feature/imggen-groups-text-subtabs` @ `5cf1ecdc` (== `origin/latest`) — `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-imggen-groups`
Authorship: 3 research explorers (read-only) + 3 plan-creation workers (one skill each); this overview is the planner's aggregation — every technical claim below originates in and cites the phase files.

Commission (Emperor, verbatim): "group image gen model (FE 2 sub tab like decision before), Another is FE change for Text model (currently 2 separated tab, move to sub tab like other)".

## Objective

Add image-generation model GROUPS to llm-supervisor-proxy by widening the ONE shared `kind='group'` entity (lane inferred from members — the v200 decision-groups pattern, NOT a new entity; expected zero new migrations), composed with the v203 token-authz layer; restructure the FE so Text's two top-level tabs collapse into one `[models | group]` container and all three kinds (Text / Image-Gen / Decision) share the identical sub-tab container.

**The mirror is deliberate, not blind.** What differs from the decision lane (full deltas in `phase1-plan.md` §3–§4 and the research): the member loop lives INSIDE `pkg/imggen` (import guard bans `pkg/proxy`/`pkg/decision` — the loop mirrors `serveDecisionGroup`'s SHAPE, never imports it); failure classification translates imggen's own `InspectResponse` Outcome into advance/rotate verbs (429 quota ⇒ ADVANCE, never relay-and-stop); a bounded walk budget replaces decision's whole-request budget (16 × 120s = 32 min naive worst case would die at ingress first); metering keeps imggen's bill-on-error Amendment-14 continuity rather than decision's success-only symmetry; sticky binding is N/A (no conversation key); v1 writes ZERO cross-request health state (fresh policy order per request leaves nothing for marks to order — the fallback-engine law applied honestly).

## Deliverables (all in `.agents/shared/planning/imggen-groups/`)

| File | Lines (post-revision) | Phase | Author |
|---|---|---|---|
| `phase1-plan.md` | 748 | P1 backend group machinery | plan-worker-backend (`6f9e366b`) |
| `phase2-plan.md` | 337 | P2 authz composition + pins | plan-worker-backend (`6f9e366b`) |
| `phase3-plan.md` | 315 | P3 FE containers + lane vocabulary FE half | plan-worker-fe (`98fb1205`) |
| `phase4-plan.md` | 210 | P4 gates / ship tail / rollback | plan-worker-ship (`cb3b4a3f`) |
| `decisions.md` | 191 | Decision register D-1..D-18 + warnings fold | plan-worker-ship (`cb3b4a3f`) |

Research inputs (arrived as reports; anchors folded into the phase files): plan-explorer-group-machinery (`82a774ea`), plan-explorer-authz-cache (`ac1c8cf3`), plan-explorer-frontend (`001b9652`).

## Phase map

| Phase | Objective | Exit criterion (one line — full lists in each file §11/§12) |
|---|---|---|
| **P1** — backend group machinery (`phase1-plan.md`) | S0 lane-vocabulary widening (`GroupLaneImageGen`; the load-bearing `MemberLane` fold; pinned test `group_rules_test.go:555-556` flips deliberately; write validators + admin-API parity; `isServableGroupMemberBase` imggen arm; rewrite-boundary refusals both directions); `/v1/image_generation` kind/lane gate (closes the REAL pre-existing gap: today ANY model id resolves, `handler.go:271/:339-351`); in-package `serveImageGenGroup` member loop; rulings F-1..F-6 | Kind/lane gate + group serving + walk budget + metering landed with the Phase-1 test battery green |
| **P2** — authz composition + pins (`phase2-plan.md`) | The three v203 arms mirrored from `decision/group.go:117-208` (resolve-inside-arm + group-id-in-reach quoting the client string; copy-before-policy member exclusion; attempt-count-keyed 403-vs-502 discriminator on the UNFILTERED row); T1–T8 authz battery; structural guards extended (imggen loop no-`.Allows(` pin; W-1: resolve arm string-pinned + numeric floor ≥6 = final post-fold count); ADR O1 supersession | T1–T8 green: group-not-granted ⇒ 403 `access_denied` + ZERO upstream attempts; granted ⇒ 200; wildcard ⇒ 200; all-members-out-of-scope ⇒ 403 NOT 502 |
| **P3** — FE (`phase3-plan.md`) | `SubTabContainer` shared pass-through core extracted; Decision rewired as thin wrapper (A-suite green UNEDITED — the extraction's byte-stability gate); Text container (bar 11→10, `group_models` retires, wrap flow re-routes via controlled child); ImgGen container; FE half of the lane vocabulary (every `Record<GroupLane,…>` arm; chat copy byte-frozen ABSOLUTELY; ONE sanctioned DECISION-lane guard-string delta (§4.4)) | 10-tab bar; three identical containers; `DecisionModelsContainer.test.tsx` diff EMPTY and green; tsc verdict frozen-or-documented |
| **P4** — gates / ship tail / rollback (`phase4-plan.md`) | G1–G15 gates (Pack A/B, gofmt flagged-LINE-SET, anti-vacuous ledger, FE count+file-set); landing recipe; ship tail with live-state capture, registry-side digest verify (marker = exhaustion-code constant + `image_misroute`), daoduc-first, dcs two-step, **§6.8 STOP-GATE: live-store group-row creation needs caller confirmation**; riders R1–R3; rollback ladder with the BENIGN-INERT data-compat position | All gates green; ship tail executed per checklist; anchors captured at ship |

## Cross-phase dependency map

1. **THE joint fold commit (P1 ⇄ P3, ADR-19)** — ONE commit contains BOTH halves: server (`pkg/models/group_lane.go`: `GroupLaneImageGen` + `MemberLane` case + fold arms + the flipped pin's Go tests) and FE (`groupLane.ts` + `types.ts` + every copy-map arm + `groupLane.test.ts` re-derivation). P1 §7 defines the server content and the minimal-diff FE-twin contract; P3 T3 is the FE half. Gate: P3 success criterion S9 (commit file-list proof) + P1's parity battery. **Never split this commit.**
2. **P2 depends on P1** — explicit D1–D8 artifact table (`phase2-plan.md` §2): the group branch, kind gate, and lane seam must exist before the authz arms pin them.
3. **P3's imggen copy literals finalize against P1's rulings** — see reconciliation (IA-3) below; placeholders in P3 §3.2 are binding ONLY after this reconciliation, which this overview performs.
4. **P4 gates everything** — P1+P2+P3 batteries all roll up into P4's G1–G15; the FE zero-mod proof applies ONLY to FE-untouched phases (this commission's FE phases claim the pairing rule + 2× rebuild determinism instead — `phase4-plan.md` §3).

Recommended execution order (matches dependencies): joint fold commit → P1 machinery → P2 pins → P3 containers (T1–T2 extraction is independent of P1/P2 and may interleave early) → P4 gates → ship tail.

## Reconciliation register (planner-adjudicated; resolves every PROPOSED decision + interface assumption)

| Item | Was | Now SETTLED to | Evidence |
|---|---|---|---|
| **D-4** timeout budget | PROPOSED (shape only) | **F-1**: per-member = existing `ImageGenTimeout` (120s default, clamp [30s,4min]) honored per attempt; total walk = NEW ConfigSnapshot-HOT knob `image_gen_group_walk_timeout`, default 240s, clamp [60s,10min], checked before each member attempt (member ctx = min(remaining, ImageGenTimeout)); budget exhaustion ⇒ the F-6 envelope + `budget_exhausted=true` in the terminal event. Knob persistence (⇒ possible migration 035) is fenced both-branches in P4 §0 | `phase1-plan.md` §3.1 |
| **D-5** metering | PROPOSED | **F-2**: winner ⇒ MEMBER row (`request_count=1`, `image_count=BillableImages`), group row untouched; failed walk ⇒ GROUP row once (`request_count=1`, `image_count=0`); per-member failure rows REJECTED; no-row-at-all REJECTED (Amendment 14 continuity). Pinned by a 2-member/1-failure usage-row-count test (D-5's own consequence) | `phase1-plan.md` §3.2 |
| **D-6** win-clear depth | PROPOSED (cooldown-only) | **F-3 — stricter than proposed**: per-request rotation ONLY (`triedPairs` in-request state, satisfying D-6's in-request suppression consequence); ZERO cross-request health writes — no marks, no skipcache, no group-owned cooldown; F-8 exposure stays NONE (no new mint sites at all) | `phase1-plan.md` §3.3 |
| **D-12** FE unification + unknown bucket | PROPOSED | **P3's positions adopted**: shared pass-through `SubTabContainer` + per-kind thin wrappers (Credentials' fetch-owning shape stays OUT); unknown/mixed lane bucket stays TEXT-ONLY under 3 lanes (single-home invariant, historical-home rule `groupLane.ts:181-184`); chat copy byte-frozen ABSOLUTELY — the ONE sanctioned delta is a DECISION-lane string (`GroupModelForm.tsx:43` guard reword — it names the retiring tab) | `phase3-plan.md` §2.2, §3.3, §4.4 |
| **IA-1** server accepts image-gen members | assumption | Confirmed — P1 S0.3/S0.4 write gates; ALL phases land in ONE release on this branch (no staging question remains) | `phase1-plan.md` §4 |
| **IA-3** imggen semantics for FE copy | placeholders | Derivable from F-3/F-6: NO race engine, NO chain, NO sticky — copy promises weighted order + failover walk + per-member credential rotation ONLY; badge `🖼️` (not 🔀); `EMPTY_BODY`/hint wording finalizes on this basis | `phase1-plan.md` §3.3/§3.6 → `phase3-plan.md` §3.2 |
| **IA-4** member eligibility filter | assumption | Both plans mirror the decision arm (`kind === 'image-gen' && enabled`) — consistent, adopted | P1 §4 / P3 §6 |
| **IA-6** joint-commit authorship | open | Resolved: ONE joint fold commit; P1 owns server content + contract; P3 T3 implements the FE half; file-list proof is the gate (item 1 above) | P1 §7 / P3 T3+ S9 |
| IA-2 / IA-5 | assumptions | Confirmed as written (server fold grows `GroupLaneImageGen`; no new endpoints/hooks — verified by both research and P1) | research + P1 |

## Revision fold — reviewer adjudications (2026-11-20, APPROVED-CONDITIONAL → doc-only corpus edits applied)

Architecture and corpus design approved; execution was blocked on two critical plan-text defects (C-1, C-2) plus adjudicated warnings — ALL now folded. Register of record: `decisions.md` §ADJUDICATED RULINGS (D-14..D-18) + §Reviewer warnings adopted.

| Ruling | Adjudication (SETTLED — do not re-open) | Landed in |
|---|---|---|
| **D-14** (C-1) | FE lane VALUE = `'image-gen'` (mirrors server constant literally; never on the wire; lane value == kind value); FE/server parity battery compares BUCKET MEMBERSHIP, not literal strings | P3 §1.2/§3.1/§3.2 + IA-7; decisions.md D-14 |
| **D-15** (C-2) | Chat-side image-gen-lane misroute arms in BOTH chat routes (`pkg/proxy/handler.go` + `handler_anthropic.go`), mirroring the decision lane's dual guard; `'image_gen_misroute'` (chat family) vs `'image_misroute'` (imggen lane); smoke NOT amended to expect 502 | P1 §4.3 (S3, T1.8a); P4 G10/§6.6/§6.9; decisions.md D-15 |
| **D-16** (W-8) | Quota-walk bounded short-circuit: 3 consecutive quota-class failures ⇒ stop + exhaustion envelope carrying the quota class (`short_circuited=true`, `last_class` in the terminal event); walk budget stays the outer bound; no account-fingerprinting; supersedes blanket honest-attempt for the quota class ONLY | P1 §3.1/§3.3/§3.6; decisions.md D-16 |
| **D-17** (W-2) | Walk-budget knob `image_gen_group_walk_timeout` on `config.ManagerInterface`; ALL 5 implementers enumerated (config.go:802 `*config.Manager`, store.go:336, mock_store.go:98, imggen handler.go:108-110, handler_test.go:237; + `imgGenParityConfig` compile-cascade); hot-read path; pre-declared in P4 G8 | P1 §4.7; P4 G8; decisions.md D-17 |
| **D-18** (shadow) | `shadow_model_id` on the imggen lane: write-time lane check + `/v1/models` listing side-effect ONLY — no claims/behavior implication | P1 §3.7; decisions.md D-18 |

Warnings adopted and folded: **W-1** (string-pin the imggen resolve arm preferred; numeric floor, if kept, = FINAL post-fold count 6, non-vacuous — P2 §4.3/T2.5), **W-3** (`currentModelRows()` explicit task, ONE read per request — P1 T1.7a), **W-4** (G3 flip = MODIFIED-with-justification, G8 green — P1 §3.8), **W-5** (both lane strings swept incl. article fix — P1 T1.2b), **W-6** ("88 .sql verified + FRESH pairing at gate time" — P4 G9 authority), **W-7** (ADDED `TestMemberLane_ImageGen_IsImageGenLane` + MODIFIED legacy test, rename FORBIDDEN — P1 §3.8/T1.1), **A-2** (rollback: "fails closed somewhere on the credential/provider/upstream path" — all paths — P1 §9/§10, P4 §8.2), **A-5** (decision-lane zero-edit gate artifact — P1 §8, P2 §3.2), **A-6** (zero-engine-state assertion after 403, P2 T-battery), **S-1** (two EMPTY id-set diff gates disambiguated — P4 G10(a) vs §6.6), **S-2** (P1 doc.go true import set incl. `pkg/modelscache` + positive ban), **S-3** ("no reconcile interaction" risk row — P1 §9 R4c).

**Two phrasing flags for the caller (substance unaffected):**
1. The reviewer's W-7/A-5 text attributed `authz_guard_test.go` to "the pkg/decision exception"; the file lives in **`pkg/proxy`**. The plans state the factually correct alignment: the `git diff $BASE..HEAD -- pkg/decision/` gate holds with **zero exceptions**; the one declared exception (`pkg/proxy/authz_guard_test.go`) is outside that path. If the reviewer prefers the ledger phrasing adjusted instead, it is a one-line decisions.md edit.
2. §4.1's `MemberLane` row initially retained pre-W-7 "rename the assertion" wording contradicting §3.8/T1.1 — a one-line conformance fix was dispatched to the backend worker and is verified below.

Remaining genuinely-open items for the CALLER (not blockers, carried from the original register): ratify the four planner-settled rulings above if any position is disputed (esp. F-1's 240s default vs "cover the full 16-member quota wall" — P1 §3.1 explicitly notes operators with many 9router members may raise it); P4 §6.8 live-store STOP-GATE (creating imggen group rows in the live store after deploy = live-store config mutation ⇒ ask first per the standing never-auto-ask-back list).



## Research insights that shaped the plan

- Groups are ONE entity with the lane INFERRED (never persisted) — decision groups shipped zero migrations; imggen needs none unless F-1's knob persists (⇒ 035, fenced).
- The modelscache decorator already forwards `GroupLane`/`GroupMembers` — the decorator trap is mostly avoidable here; the LANE VOCABULARY is the choke point (without the fold, `GroupLane` fails closed through the decorator).
- imggen already had the authz-side group seam (`modelsConfig`/`membersOf`) — only the serving side was missing; and it had NO kind gate at all (any model id resolved) — a real pre-existing gap P1 closes.
- The decision lane ADVANCES on member failure (verified loop + advance checks) — the commission's "single-attempt" hypothesis was wrong, and the mirror copies the failover walk.
- The imggen lane already had the 403 `access_denied` envelope with structural zero-upstream ordering — P2's arms extend an existing discipline rather than inventing one.

## Top cross-phase risks (deduped; full registers in each phase file)

1. **Lane-fold twin drift** (P1 §7 / P3 R6 / P4 decisions D-3): one contract, two halves — mitigated by the single joint commit + file-list proof + parity battery.
2. **The fold change touches EVERY lane** (P1 §9): chat rewrite boundary, decision gates, `/v1/models`, FE — every `MemberLane`/`UnanimousGroupLane`/`inferGroupLane` consumer enumerated and pinned in P1.
3. **Shared-container extraction regressing the live v200 decision surface** (P3 R3): gated by the unedited A-suite (diff EMPTY) — the strongest single FE gate.
4. **9router account-wide quota failing all members identically** (P1 §3.3 argument / P4 §4): classified as quota (never a 502 availability wall); each member still gets its honest attempt; the walk budget bounds the pathological case.
5. **Rollback data-compat** (P4 §8, argued BENIGN-INERT): old binary + new group rows ⇒ unknown lane ⇒ text sweep refuses, FE folds to text child, clean 502 — re-deploy is the repair, no cleanup.
6. **tsc baseline drift from the SettingsPage restructure** (P3 R1 / P4 gates): count+file-set verdicts make drift loud; deliberate-delta protocol in-place.

## Ship tail & riders (pointer — `phase4-plan.md` §5–§7 owns execution)

Standing flow: gates → master promotion → WAIT CI (9–13 min band; v205+ expected, capture at ship) → registry-side digest verify (marker: exhaustion-code constant + `image_misroute`) → daoduc first (helm) → probe → primary → dcs pins two-step. Riders: R1 oauth2 Phase-4 plan digest refresh (`docs/phase4-ensem-execution-plan` branch, one-line §4.1 edit) if this ships first; R2 OAuth2 config untouched; R3 heartbeat.go:114 pre-existing defect — read the stack before any rollback. Capture observed live helm rev + image tag on BOTH hosts immediately before deploy — context hints (daoduc rev 51 / primary gen 44 @ v204) are hints only.
