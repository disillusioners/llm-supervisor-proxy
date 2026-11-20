# DECISIONS — imggen-groups commission (image-gen model groups + Text FE sub-tab restructure)

> **Commission:** (1) image-gen model GROUPS — the Option-A widening of the ONE shared group entity to a third lane; (2) Text models FE restructure to sub-tab containers; (3) a shared sub-tab container across all three kinds. **Worktree:** `../llm-supervisor-proxy-wt-imggen-groups` (branch `feature/imggen-groups-text-subtabs`, base `origin/latest` @ `5cf1ecdc`, giter-fenced, no push). **Author:** phase-4 worker (gates/ship/rollback + this register). **Date:** 2026-11-20. **Status:** REVISED 2026-11-20 — reviewer verdict **APPROVED-CONDITIONAL** folded: adjudicated rulings **D-14..D-18** appended, adopted warnings in **§Reviewer warnings adopted** (W/A/S sets). D-1..D-13 stand as written (one cross-ref added at D-7.4). Items previously marked **PROPOSED** remain PROPOSED for planner reconciliation in `plan-overview.md` except where an adjudication below settles them.
> **Evidence base:** every citation below was re-read in THIS worktree at `5cf1ecdc` unless explicitly marked otherwise. Drift vs the commission brief is recorded in §Drift. Anything I could not verify is in §Not verified — nothing is cited blindly.

---

## Ruling summary

Widen, don't fork. An image-gen group is **the same `models.kind="group"` row** with image-gen members; its lane is **inferred from the members** by the single shared fold and is never persisted. The member loop lives **inside `pkg/imggen`** (the import guard forbids reusing `pkg/proxy`/`pkg/decision`), mirroring `serveDecisionGroup`'s shape. The **lane vocabulary is the load-bearing change** — `MemberLane` gains an image-gen arm, one pinned test flips deliberately, and the FE twin mirrors it in the same commit (ADR-19). v203 authz composes unchanged: the **group id is the authz unit** on the `allowed_groups` axis for ANY lane; member-level exclusion happens on a copy before policy; the 403-vs-502 discriminator is order-computed-before-first-attempt; an **`image_misroute` lane gate is ADDED (none exists today)**. Sticky binding is N/A (no conversation key). `/v1/models` excludes imggen groups; the regression gate is an EMPTY id-set diff on a store with no imggen groups. The provider vocabulary atomicity set is NOT triggered. The token-authz ADR's O1 ("Image-gen has no group or chain surfaces") is **superseded**. FE: the decision-lane container pattern is extracted and shared; SettingsPage goes 11→10 tabs (`group_models` retires); chat-lane FE copy is BYTE-FROZEN.

---

## D-1 — Option-A widening: same `kind='group'` entity, lane inferred, no new entity/table *(SETTLED)*

- **Context.** A third lane could be built as a parallel imggen-group path or by widening the ONE shared group entity the decision-groups commission already widened once (chat → chat+decision).
- **Ruling.** Widen. An image-gen group is an ordinary `models.kind="group"` row whose members are image-gen models; the lane is **inferred at request time by unanimity over the CURRENT member rows**, never persisted, enforced by write-time homogeneity. No new entity, no new table. **No new migration is expected** — the registry stops at `034_add_allowed_groups` (`pkg/store/database/migrate.go:95`); the next number `035` is reserved ONLY if columns/knobs actually land (a timeout knob defaulting in-code needs no migration).
- **Evidence.** Lane-inference contract: `pkg/decision/group.go:4-8` ("The lane is INFERRED from the members (never persisted); the write path guarantees the members share one lane"). Fold primitives + "lane vocabulary" doc: `pkg/models/group_lane.go:12-30`. Registry tip: `pkg/store/database/migrate.go:82-95` (`033`, `034`); migration files on disk end at `034_add_allowed_groups.{up,down}.sql` (both dialects; 88 SQL files total, counted at base).
- **Consequences.** The text lane's group machinery is reused, not duplicated; the v193/v200 correctness properties (ordering-not-filtering, win-clear identity, decorator-forward trap) carry over for free. Phase-1 must widen the write-time validator's member-kind rule (the G3 analog) and the servability predicate's lane parameter — the two places decision-groups already widened.
- **Implementing phase.** Phase 1 (backend).

## D-2 — The member loop lives INSIDE `pkg/imggen`, mirroring `serveDecisionGroup` *(SETTLED)*

- **Context.** The loop needs group policy/order/demotion semantics that already exist in `pkg/store`/`pkg/grouplb`, but `pkg/imggen` is a guarded leaf.
- **Ruling.** The loop is a new in-package member walk in `pkg/imggen`, mirroring the shape of the decision lane's loop (`serveDecisionGroup`, `pkg/decision/group.go`): pick-is-policy, attempt-may-be-suppressed, member fails only when its own resilience is consumed, exactly one client write at the winner, exhaustion ⇒ a distinct machine code. `pkg/imggen` continues to import ONLY `{pkg/models, pkg/auth, pkg/usage, pkg/events}` — never `pkg/proxy`, never `pkg/decision`.
- **Evidence.** The binding import rule: `pkg/imggen/doc.go:11-20` (import direction + the three in-package guard tests `TestPackage_ImportGuard_NoProxyImports` / `_NoDecisionImports` / `_TransitiveClosure`). The `make test` grep gate: `Makefile:51-56` ("pkg/imggen must not import pkg/proxy (Amendment 9)"). The loop contract being mirrored: `pkg/decision/group.go:10-30`.
- **Consequences.** Any shared classification vocabulary `pkg/imggen` needs but cannot import (e.g. `pkg/providers`) must arrive as a leaf package, the `pkg/credclass` precedent. `make test` (not plain `go test`) runs the grep guards — the gate plan covers this.
- **Implementing phase.** Phase 1.

## D-3 — Lane vocabulary is the load-bearing change; pinned test flips deliberately; ADR-19 same-commit FE twin *(SETTLED)*

- **Context.** Today `image-gen` carries NO lane evidence: `MemberLane(KindImageGen)` returns `ok=false` and the fold ignores the member as unservable (`pkg/models/group_lane.go:26-30`, `:49-52`). A pinned test asserts exactly that.
- **Ruling.** `MemberLane` (`pkg/models/group_lane.go:53-62`) gains an image-gen arm returning a new `GroupLaneImageGen` lane; `GroupLanesOf`/`UnanimousGroupLane` extend the fold accordingly (stable order chat, decision, image-gen). The pinned assertion `MemberLane(image-gen) must not be a valid lane` (`pkg/models/group_rules_test.go:555-556`) **flips deliberately** — the commit that flips it must say so. The FE twin `pkg/ui/frontend/src/components/config/groupLane.ts` mirrors the vocabulary **in the same commit** (ADR-19: "the two must not ship different answers", `pkg/models/group_lane.go:3-8`).
- **Evidence.** `pkg/models/group_lane.go:35-43` (lane constants), `:53-62` (MemberLane — the exact switch that widens), `:3-8` (ADR-19 mirror mandate); pinned test at `pkg/models/group_rules_test.go:545-575` (the image-gen assertion at `:555-556`); FE twin `groupLane.ts` header (memberLane "applies the server's rule").
- **Consequences.** Every surface that folds lanes must be enumerated and widened together or the FE and BE disagree: the write validator, the admin-API mirrors, request-time inference, `/v1/models` exclusion, the FE fold. A mixed-lane group stays unrepresentable at write time and fail-closed at request time. The new constant string doubles as a binary ship marker (see phase4-plan §6).
- **Implementing phase.** Phase 1 (Go) + Phase 3 (FE twin, same commit as the Go change per ADR-19).

## D-4 — Timeout budget: per-member + total-walk clamp *(PROPOSED — backend worker recommends)*

- **Context.** The decision lane's whole-request budget is `DecisionTimeout`, default 30s, clamp [10s, 60s] (`pkg/decision/handler.go:27`). The imggen lane's single attempt uses `ImageGenTimeout`, default 120s, clamp [30s, 4min] (`pkg/imggen/handler.go` lifecycle step 7, `:159-160`). Measured 9router image-gen latency: HTTP 200 in 13.93s/21.42s (p50 17.68s, n=2); a quota-429 burns **15-17s before failing** (POK `9router-imggen-latency-jpeg`, `9router-imggen-quota-wall`).
- **Ruling (proposed).** A group walk needs BOTH: a **per-member budget** in the `ImageGenTimeout` clamp family (so one healthy member is not starved by earlier failures — p95 headroom ≈ 5.6× today) AND a **total-walk cap** so N members × ~20s worst-case cannot hold a request open for minutes. The total cap must be ≥ (fastest-healthy-member budget); the per-member budget must not fall below the observed max (21.42s) plus margin, or healthy 9router members get cut. Exact numbers are the backend worker's to pin in phase1-plan; this register records the SHAPE (two clamps, both bounded) as the recommended default.
- **Evidence.** Clamps: `pkg/decision/handler.go:27`; `pkg/imggen/handler.go:159-160`; `pkg/config/config.go:113` (the hot-clamp precedent wording). Latency/quota: POK entries (project operating knowledge, cited as research — not re-measured in this worktree).
- **Consequences.** A knob introduced here is the one plausible trigger for a `035` migration (persisted config) — if the knob is env/in-code default only, no migration (D-1). The gate plan's migration fence covers both branches.
- **Implementing phase.** Phase 1.

## D-5 — Metering stance on the group walk: bill-on-error vs success-only *(PROPOSED)*

- **Context.** The imggen lane meters **bill-on-error** — `request_count=1` + `image_count=BillableImages(body)` even on error-in-200, a documented divergence from chat's success-only convention (Amendment 14, `pkg/imggen/handler.go:168-171`, execution `:582-586`). The decision lane is **success-only** (step 16; zero tokens on failure).
- **Ruling (proposed).** Keep the imggen lane's own convention for the WINNER's metering (a group answer is still one imggen answer — Amendment 14 semantics unchanged), and do NOT meter failed member attempts as images. The open question the backend worker must close: does a suppressed/failed member attempt bump `request_count` anywhere (usage rows are per winner today)? Recommended default: no — one request, one metering row, at the winner, exactly like the single-model path.
- **Evidence.** `pkg/imggen/handler.go:168-171` (Amendment 14), `:582-586` (BillableImages on the shared body), `:661-662`, `:698` (the divergence comments); decision contrast: `pkg/decision/handler.go` step 16 (success-only).
- **Consequences.** Whatever lands must be pinned by a test asserting the usage row count for a 2-member walk with 1 failure — this is exactly the kind of silent accounting drift nobody notices in review.
- **Implementing phase.** Phase 1.

## D-6 — Win-clear / demotion wiring depth *(PROPOSED)*

- **Context.** The text lane clears all three layers (GROUP/MODEL/UPSTREAM) keyed by the WINNER's identity (`clearWinnerLayerState`, `pkg/proxy/handler_helpers.go` — renamed by v194, clears three layers). The decision lane ruled (ADR-12) that it writes NO grouplb member cooldown and targets only its own model+upstream layers.
- **Ruling (proposed).** v1 ships **group-owned cooldown only** (the imggen lane's own member-suppression state), NOT a full three-layer health mirror. Rationale: imggen has no conversation binding (D-8) so sticky-ordering layers are inert; the quota-429 class is account-wide on 9router (POK `9router-imggen-quota-wall`) so a member-level provider mark is the one with real signal. The backend worker may overrule toward a deeper mirror if the reuse cost is genuinely low — that is a phase-1 call; this register records the minimal-default.
- **Evidence.** `pkg/decision/group.go:10-30` (the loop contract: suppression is a skip, never a pick change); the ADR-12 supersession note in `.agents/shared/planning/decision-groups/phase1-plan.md:328`; POK quota-wall entry (account-wide exhaustion).
- **Consequences.** If v1 is minimal, a member that 429s must still be suppressed for the walk's remainder within the same request (in-request skip), or the loop can re-pick it.
- **Implementing phase.** Phase 1.

## D-7 — v203 authz composition: group id = the unit; exclusion on a copy; 403-vs-502; `image_misroute` gate ADDED *(SETTLED)*

- **Context.** v203/034 made `allowed_groups` a first-class axis: the group TARGET id is reachable and a group grant expands to members (`pkg/authz/scope.go:93-108`, the "group TARGET itself is reachable" comment). The decision lane composes this with a member-exclusion copy and a permission-vs-availability discriminator.
- **Ruling.** Mirror the composition exactly:
  1. **Group id = the authz unit on the `allowed_groups` axis for ANY lane** — no lane-specific grant semantics (`pkg/authz/scope.go:93-118`; `Allows` is THE predicate, every lane calls it, D7/D8 of the auth commission).
  2. **Member exclusion on a copy before policy** — `filtered := *group` with a fresh backing array (`pkg/decision/group.go:157-166`), so policy code stays byte-unchanged.
  3. **403-vs-502 discriminator, attempt-count-keyed** — the order is computed BEFORE the first attempt; scope-caused emptiness with ≥1 base-servable member ⇒ 403 `access_denied`; all-disabled ⇒ 502 (`pkg/decision/group.go:185-209`).
  4. **`image_misroute` lane gate ADDED — none exists today.** Verified: `/v1/image_generation` resolves the row (`resolveModel`), checks scope, resolves a credential, looks up the provider — **no kind check anywhere** (`pkg/imggen/handler.go:266-340`); a chat model posted there today walks as far as provider lookup and 502s confusingly. The group form needs the lane gate first (an imggen group posted to chat/messages must 400 with the house misroute shape), and the singleton form gains the same guard (`image_misroute`), mirroring `decision_misroute` (`pkg/decision/handler.go:74`). *(Revised — the full guard is DUAL and per-route-family: see **D-15** for the chat-side arms in BOTH chat routes and the `'image_gen_misroute'` vs `'image_misroute'` string split.)*
- **Consequences.** The misroute gate is a NEW 400 surface on a lane that had none — pin it in phase 1 and mention in the release note (clients that were posting wrong-kind models and receiving 502s will now see 400s; that is a fix, not a regression).
- **Implementing phase.** Phase 1.

## D-8 — Sticky binding: N/A *(SETTLED)*

- **Context.** Group-layer conversation-hash binding (v198, 30m absolute TTL) keys on a conversation key.
- **Ruling.** Not applicable — the imggen lane has **no conversation key** (empty key ⇒ fresh weighted pick, no binding; `pkg/models/group_rewrite.go:88-90`, the decision lane's identical ruling). Do not wire sticky ordering here.
- **Implementing phase.** Phase 1 (explicit non-action; recorded so nobody "adds" it).

## D-9 — `/v1/models`: imggen groups join the exclusion; gate = EMPTY id-set diff *(SETTLED)*

- **Context.** `/v1/models` excludes image-gen and decision singletons, decision-lane groups, and shadow-claimed members (`pkg/proxy/handler.go:432-440` — exclusion sites `:432` kinds, `:435` decision groups, `:438` claimed; the lane fold `:415-428`).
- **Ruling.** Imggen-lane groups join the group exclusion (a new sibling of the `:435` check). The regression gate: on a store with **no imggen groups**, the `/v1/models` id-set must be **BYTE-IDENTICAL** before/after the change (EMPTY id-set diff); on a store WITH an imggen group, the diff is exactly that group's id. Mirror the decision precedent pins (`TestHandleModels_FilterDecisionGroup` + `_ThroughCachedConfig`).
- **Evidence.** `pkg/proxy/handler.go:415-440` (verified at base; note the brief's `:423-426` was approximate — see §Drift).
- **Implementing phase.** Phase 1.

## D-10 — Provider vocabulary: atomicity set NOT triggered *(SETTLED)*

- **Context.** The decision-provider vocabulary is a 4-site cluster (registry + 3 validation gates + FE constants) that must land together. The fear: an imggen-group member loop needs new provider gates.
- **Ruling.** Not triggered. The imggen kind already validates `minimax|9router` (`pkg/models/config.go:255`); group membership reuses the member rows' own credentials — no new provider name, no new FE vocabulary, no parity-test pin needed.
- **Implementing phase.** Phase 1 (non-action; recorded to close the question).

## D-11 — ADR O1 supersession *(SETTLED)*

- **Context.** The token-authz ADR's out-of-scope list states: "**O1 — Image-gen has no group or chain surfaces.** … the imggen lane therefore needs only the entry check (S2)" (`.agents/shared/planning/token-authz-allowed-groups/adr-token-permission-model.md:450`).
- **Ruling.** O1 is **superseded by this commission**: image-gen now HAS a group surface. The supersession is a doc edit in the imggen-groups planning corpus (a dated supersession note pointing at this register's D-7), NOT an edit of the shipped authz code — the entry check (scope on the resolved row/group id) remains the whole scope obligation at the singleton level, now composed with the group branch's member exclusion.
- **Implementing phase.** Phase 1 (docs accompany the code) — phase 4's ship-tail checklist confirms the note exists.

## D-12 — FE unification: shared sub-tab container; 11→10 tabs; chat-lane copy BYTE-FROZEN; unknown-lane bucket *(PROPOSED — FE worker recommends)*

- **Context.** SettingsPage currently has 11 top-level tabs (`TabType` `pkg/ui/frontend/src/components/SettingsPage.tsx:79`: proxy, models, imggen_models, decision_models, group_models, credentials, loop_detection, tool_repair, tokens, usage, mcp_servers), with `group_models` a standalone tab (`:567-572`, cross-tab navigation `setActiveTab('group_models')` `:300`). The decision lane already ships the target pattern: `DecisionModelsContainer.tsx` owns a vertical left nav with child tabs `[models] [group]`, is a pure pass-through, and keeps `DecisionModelsTab` byte-unchanged.
- **Ruling (proposed).** (a) Extract the container pattern into a **shared sub-tab container** (generic over child panels; the decision container becomes an instance, not the source); (b) Text becomes a `[Models|Group]` container (chat-lane groups move in; the `group_models` top-level tab **retires**, 11→10); (c) ImgGen becomes a `[Models|Group]` container (the new imggen-group surface is its Group child); (d) **chat-lane FE copy is BYTE-FROZEN** (ADR-9/AC-R12 lineage — wrap, never refactor: the chat ModelsTab/GroupModelsTab panels keep their strings, props and tests byte-identical, only their mount point moves); (e) **unknown-lane bucket policy**: a group whose lane the FE cannot fold renders in the **chat** container's Group tab (the conservative bucket — matches the server's fail-closed "not a decision/imggen group" default), never hidden entirely. The FE worker owns the final shape; the planner reconciles.
- **Evidence.** Container pattern: `pkg/ui/frontend/src/components/config/DecisionModelsContainer.tsx:1-60` (ChildTab, pass-through props, NAV_ITEMS with testIds `decision-container-tab-*`); tab surface: `SettingsPage.tsx:79`, `:567-572`, `:300`; FE lane fold: `groupLane.ts` (memberLane mirrors the server rule).
- **Consequences.** The tsc baseline freeze interacts here: `SettingsPage.tsx` is frozen at **5** errors (`pkg/ui/frontend/tsc-baseline.txt` per-file table; TOTAL 18/9) — the restructure must not add a 6th (a decrease requires a baseline update in the same commit). FE test-ids move (`data-tab-id="group_models"` disappears) — pin the new ids in phase 3.
- **Implementing phase.** Phase 3 (FE), against Phase 1's frozen contracts.

## D-13 — Decorator: 0-1 new forwards; if any, the 7-step mandatory surface *(SETTLED)*

- **Context.** Production always runs through `*CachedModelsConfig` (`CACHE_LAYER_ENABLED` default ON), so a missing forward is a **silent no-op in production while raw-manager tests stay green** — the modelscache-decorator-forward trap (`pkg/modelscache/models.go:1624-1656`, the compile-time seam-assertion block).
- **Ruling.** Expected **zero** new forwards: `GroupLane` is already forwarded (`pkg/modelscache/models.go:1465-1472`) and returns the lane for ANY group, imggen included, once D-3 widens the fold. At most ONE new forward is admissible (an imggen-lane-specific query the phase-1 design actually needs). If one lands, the **house mandatory surface** (the 7 steps, per the decision-groups precedent — phase1-plan Stage 2 tasks 2.3-2.5 + risk R1 + Amendment 6/ADR-17 RED-3):
  1. OPTIONAL interface (NOT on `models.GroupRewriter`) so the ~11 structural fakes stay valid;
  2. exported method on `*ModelsManager`;
  3. decorator forward in `pkg/modelscache/models.go` with the CRITICAL-FIX trap comment;
  4. compile-time `var _` seam assertion in the models.go assertion block (`:1646-1656` region);
  5. compile-time `var _` assertions at EVERY consumer binding the decorator to the seam type;
  6. a decorator-through test proving the effect reaches the inner manager (failure text names the trap);
  7. consumer fails CLOSED when the seam is absent.
- **Implementing phase.** Phase 1 (and the gate plan G-checks the forward pair if it lands).

---

# ADJUDICATED RULINGS (2026-11-20 revision — reviewer APPROVED-CONDITIONAL; adopt as stated, do not re-open)

## D-14 — FE lane VALUE = `'image-gen'` *(SETTLED, adjudication C-1)*

- **Context.** The FE `GroupLane` union grows an image-gen arm (D-3/D-12); a reviewer asked what literal the FE carries and whether FE↔server string coupling is a parity hazard.
- **Ruling.** The FE lane VALUE is `'image-gen'` — a literal mirror of the server constant (`GroupLaneImageGen`, `pkg/models/group_lane.go:35-43` family). The FE value **never crosses the wire** (the lane is additive-only, never persisted, never serialized — `types.ts:201-210` doc, phase3 §6), so the only coupling is source-level symmetry. The FE↔server **parity battery compares BUCKET MEMBERSHIP, not literal strings**: the pins assert which groups land in which lane bucket (`splitGroupsByLane` outputs), never the lane literal itself — a renamed literal with identical bucket behaviour is not a parity break, a divergent fold is.
- **Consequences.** No wire-contract pin on the lane literal; the same-commit ADR-19 twin rule (D-3) remains the drift guard.
- **Implementing phase.** Phase 3 (FE twin, same commit as the Go fold — D-3).

## D-15 — Chat-side image-gen-lane misroute arms, BOTH chat routes; per-family code strings *(SETTLED, adjudication C-2)*

- **Context.** D-7.4 added the imggen-lane gate (`image_misroute` on `/v1/image_generation`). The reviewer widened the guard: the CHAT side must also refuse an image-gen-lane group id, mirroring the decision lane's dual guard (`pkg/proxy/handler.go:499` chat + `pkg/proxy/handler_anthropic.go:230` messages → `decision_misroute`).
- **Ruling.** Chat-side image-gen-lane misroute arms land in **BOTH chat routes** (`pkg/proxy/handler.go` + `pkg/proxy/handler_anthropic.go`): an imggen-lane group id posted to `/v1/chat/completions` or `/v1/messages` ⇒ 400. **Code strings are per route family:** `'image_gen_misroute'` (chat family, both routes) vs `'image_misroute'` (imggen lane, `imggenMisrouteCode`, phase1-plan §4.6 `:312`) — two distinct machine codes, never one shared string. **Smoke is NOT amended to expect 502** — no smoke text anywhere claims a wrong-lane post yields 502; the expectation family is the 400s.
- **Consequences.** The release note records BOTH strings (phase4-plan §6.9); the misroute battery covers all four crossings (chat→imggen route, imggen-group→chat routes ×2 wire protocols, non-image-gen singleton→imggen route); W-5 sweeps both strings consistently.
- **Implementing phase.** Phase 1 (P1's scope line owns "the misroute/kind gates"); structural pins land with Phase 2's guard extension (S5).

## D-16 — Quota-walk bounded short-circuit *(SETTLED, adjudication W-8)*

- **Context.** 9router img-gen quota is ACCOUNT-WIDE and a quota-429 burns 15-17s before failing (POK `9router-imggen-quota-wall`); an unbounded walk could burn N×~17s of dead time on an exhausted account.
- **Ruling.** The walk gains a **bounded short-circuit: 3 consecutive quota-class failures ⇒ stop the walk** and emit the **exhaustion envelope carrying the quota class** (the envelope's discriminator names quota exhaustion, not generic all-members-failed). The walk budget (D-17) remains the **outer bound** — the short-circuit fires first when quota is the failure mode. Detection stays **simple**: consecutive quota-class classification, **no account-fingerprinting** (no cross-member account heuristics in v1, despite the account-wide reality).
- **Consequences.** The exhaustion envelope gains a quota-class discriminator (operator-visible); the loop test battery gains the 3-consecutive case; D-4's two-clamp shape is unchanged (the short-circuit sits inside the budget).
- **Implementing phase.** Phase 1.

## D-17 — Walk-budget knob `image_gen_group_walk_timeout` on `config.ManagerInterface` *(SETTLED, adjudication W-2)*

- **Context.** D-4 proposed the two-clamp budget without fixing where the total-walk knob lives; the reviewer fixed it.
- **Ruling.** The knob `image_gen_group_walk_timeout` goes **ON `config.ManagerInterface`**, with **all 5 implementers enumerated in phase1-plan §4.7**: (1) the knob block `pkg/config/config.go:159-167` (`ImageGenGroupWalkTimeout Duration` beside `ImageGenTimeout`); (2) `ManagerInterface :207` (`GetImageGenGroupWalkTimeout() time.Duration`, same pattern as `GetImageGenTimeout`); (3) `Defaults :308` (240s); (4) the clamp block `:336-346` (`[60s, 10min]`, `<=0` ⇒ degrade to default); (5) the handler's per-request getter read. **Pre-declared in phase4-plan's G8 boundary** (this supersedes the earlier "pkg/config/ = 0 bytes" default — the bounded shape is now the declared expectation). **Hot-read path noted:** the getter is a per-request ConfigSnapshot-class read (HOT — applies via `PUT /fe/api/config` without restart), per the §4.7 doc-comment mandate.
- **Consequences.** P0.5/G8 in phase4-plan carry the pre-declared shape; no migration (the knob rides the existing config store; D-1's no-migration expectation is unchanged).
- **Implementing phase.** Phase 1 (§4.7); gated by Phase 4 G8.

## D-18 — `shadow_model_id`: write-time lane check + `/v1/models` listing side-effect ONLY *(SETTLED)*

- **Context.** Shadow (claims) machinery interacts with group members (`claimedIDs` shadow-hide in `/v1/models`; shadow-target validation at write time); the reviewer bounded the interplay.
- **Ruling.** The image-gen lane's `shadow_model_id` interplay is **exactly two things**: (1) the **write-time lane check** (a shadow target's lane must be consistent with the group's — phase1-plan §8 write-path "shadow-target lane pin") and (2) the **`/v1/models` listing side-effect** (claimed-member shadow-hide composes with the imggen-group exclusion). **No claims/behavior implication** — the claims/rewrite/race semantics are untouched by the third lane.
- **Consequences.** No claims-path edits in any phase; a claims-related diff in the commission's boundary check is a finding, not expected noise.
- **Implementing phase.** Phase 1 (write-time pin) + the existing `/v1/models` exclusion work (D-9).

---

## Reviewer warnings adopted (2026-11-20 — folded, not re-opened)

| # | Adopted warning | Folded where |
|---|-----------------|--------------|
| **W-1** | Resolve-arm pins: **string-pin the resolve arm preferred** (assert the misroute code string in body+event); any numeric floor (arm count) must state the **FINAL arm count** after D-15's arms land — a stale count can pass vacuously if arms are removed and added in one diff. | Phase 1/2 pin battery; phase4 G10 |
| **W-3** | `currentModelRows()` becomes an **explicit phase-1 task** with **ONE read per request** — mirror the decision lane's PD-5 rule (one live-row read shared by the base-servable count and the policy, `pkg/decision/group.go:176-181`; P1 §4.6 step 3 already sites it). | Phase 1 |
| **W-4** | The pinned-test flip is listed in the ledger as **MODIFIED-with-justification** (naming per W-7) and the **boundary gate stays green** — the flip is a test edit, never a boundary edit. | Phase 4 G7/G8 |
| **W-5** | **Both lane strings swept** — `'image_gen_misroute'` (chat family) and `'image_misroute'` (imggen lane) used consistently across code, events, docs and comments, **including the `"a image-gen-kind"` → `"an image-gen-kind"` article fix** in every comment the widening touches. | Phases 1-3 sweep; phase4 G14 |
| **W-6** | Migration reconciliation authority phrasing: **"88 .sql verified + FRESH pairing at gate time"** — the historical 91-grep figure is dead (§Drift #2 stands). | Phase 4 G9 |
| **W-7** | Ledger naming FIXED: **ADDED `TestMemberLane_ImageGen_IsImageGenLane`** + **MODIFIED `TestValidateGroupMembers_LegacyEmptyKind_IsChatLane`**; **renaming the legacy test is FORBIDDEN** — this supersedes phase1-plan T1.1's "rename the assertion" wording (the flip lands as add+modify, never rename). | Phase 1 T1.1; Phase 4 G7 |
| **A-5** | **Decision-lane Go zero-edit promoted to a gate-artifact row:** `git diff $BASE..HEAD -- pkg/decision/` EMPTY **(zero exceptions)**; the one declared exception is `pkg/proxy/authz_guard_test.go`, OUTSIDE that path — any decision-side diff is a RED gate (no plan declares a `pkg/decision` edit today — all P1/P2 `pkg/decision` references are citations/mirrors, verified). | Phase 4 G8 |
| **A-6** | **Zero-engine-state assertion after the 403 arm** — a scope-refused request writes NO engine state (no cooldown/mark/skip mutations); guards the v2 health-mirror follow-up (D-6): v1 writing nothing on 403 gives v2's deeper mirror a clean baseline. | Phase 2 pin battery |
| **A-2** | **Rollback wording (phase4 §8.2 corrected):** under an old image an imggen group **"fails closed somewhere on the credential/provider/upstream path"** — ALL paths, not one envelope (the chat sweep refusal and the imggen-route credential resolution fail with different envelopes; the invariant is never-wrong-lane, never a specific 502). | Phase 4 §8.2 |
| **S-1** | The **TWO EMPTY id-set diff gates are DISAMBIGUATED and named**: the **catalog-exclusion regression gate** (pre-merge, Go test pins, same store, base-vs-HEAD binary — phase4 G10(a)) vs the **live-dormancy proof** (ship-tail, live `/v1/models` pre-vs-post deploy on the same host — phase4 §6.6). Each diffs a different thing at a different time; **neither can silently stand in for the other**. | Phase 4 G10(a) + §6.6 |
| **S-2** | **P1 must update `pkg/imggen/doc.go` to name the TRUE import set** — `{pkg/models, pkg/auth, pkg/usage, pkg/events, pkg/modelscache}` (the lane-seam `var _` assertion imports `modelscache`; doc.go `:11-20` currently omits it) — **and state the ban positively** (the list is exhaustive; every other repo package is banned). The `Makefile` grep gates (proxy/decision/imggen) stay as-is. | Phase 1 |
| **S-3** | **Risk row adopted: "no reconcile interaction"** — the known periodic-reconcile/write-mutex stall risk (POK) has NO interaction with this commission: imggen groups are ordinary `kind='group'` rows on the existing reconcile path; no new reconcile triggers, no schema the reconcile reads differently. Recorded for `plan-overview.md`'s risk table. | plan-overview (planner folds) |

---

## Drift (brief vs worktree, re-verified at `5cf1ecdc`)

| # | Brief said | Worktree says |
|---|---|---|
| 1 | `/v1/models` group exclusion at `pkg/proxy/handler.go:423-426` | The lane fold is `:415-428`; the exclusion sites are `:432` (image-gen/decision kinds), `:435` (decision groups — where the imggen sibling goes), `:438` (claimed). Approximate, not wrong. |
| 2 | "migration count reconciliation gate (88 SQL / 91 grep)" | **88 .sql files verified** (`find pkg/store/database/migrations -name '*.sql' \| wc -l`). The **91-grep counterpart could NOT be reproduced** at this base with the obvious candidates (registry refs, `.sql` greps). Re-derive the exact grep↔file-count pairing at gate time IF a migration lands; research expects none (D-1). |
| 3 | internal-oauth2 `phase4-ensem-execution-plan.md` as a path in this tree | The file is **NOT on this branch** — it lives only on `docs/phase4-ensem-execution-plan` @ `cd445c8` (verified via `git ls-remote`; the worktree's `internal-oauth2/` dir has phase1/phase2-switch/plan-overview only). The phase-4 rider must operate on that branch. |

## Not verified (stated, not cited)

- **Live helm rev / primary generation** — context-of-record says daoduc helm rev 51 (post Phase-3 oauth2 cutover) and primary gen 44 @ v204 `abd61d7` digest `cfc34862` (as of 2026-11-20); the ship tail **captures at ship time** and never trusts these numbers (phase4-plan §6.3).
- **9router latency/quota figures** (p50 17.68s / max 21.42s / quota-429 after 15-17s) — cited from project operating knowledge (POK `9router-imggen-latency-jpeg`, `9router-imggen-quota-wall`), not re-measured in this worktree.
- **Phase 1-3 file lists, test ledgers, timeout numbers, FE container names** — owned by the parallel phase plans; this register records shapes and defaults, and marks them PROPOSED where the owning worker refines.
