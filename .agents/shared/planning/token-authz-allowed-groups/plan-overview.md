# Plan Overview: AUTH COMMISSION — Token permission model (`allowed_groups` + reach-based enforcement)

Date: 2026-11-18T18:07:30Z
Author: planner[v2] via plan-creation worker
Status: Draft — superseded by REAL STATUS below
**REAL STATUS (gen-11 re-pin; MEASURED 2026-11-19T07:41:00Z):** branch `feature/token-authz-allowed-groups`; **base `a3f0335`**; **HEAD `1408e8b`** — ALL LANES LANDED (tester FINAL VERDICT PASS). **Chain: 22 commits incl. base (21 on top of base)** — the `1e65774` tip was 21 commits incl. base (20 on top); `1408e8b` (docs(tester): FINAL VERDICT PASS — token-authz gen-10 @ `1e65774`, 121-pin sweep clean) is the 21st commit on top of base. Enumerated:  `a3f0335` (base) → `2b9ab6c` (frozen token-scope contract) → `60d0db4` (Phase-1 completion: 034, admin API, PD pins) → `f9adbce` (empty/whitespace TEXT fail-closed + hermetic authz guard toolchain) → `b0d4875` (FE token-grant wire) → `5de3eb4` (allowed_groups picker + per-axis control) → `7ce1787` (static bundle rebuild) → `391f181` (Phase-2 per-layer enforcement, 4 lanes) → `e7ffcda` (chain/re-entry/per-member pins) → `156252a` (decorator degraded-branch fail-closed) → `1e423de` (C2 invalid flags in DTO + C3 grant-axis escalation) → `63e9665` (C2 BE pin + C3 FE pins) → `ce8f2f5` (W1 stale-claim grant validation) → `0be8d13` (W2 groupPick 403 conjunct) → `4be0a12` (F1 per-axis picker save + F2 DTO comment) → `81e8788` (gofmt drift + log/comment accuracy + guard-test hardening) → `eb137a1` (align stale empty-slice assertions with the conjunctive grant contract) → `135e1e8` (tester: release-gate report) → `f244b1d` (tester: shared-worktree provenance audit + gen-9 re-verification recipe) → `e502978` (tester: tree-cleanliness precondition for the gen-9 recipe) → `1e65774` (**land the 9 commission-named obligation pins** — PD-2/PD-7/PD-9/PD-10/PD-11; 4 test files, +1589). → `1408e8b` (docs(tester): FINAL VERDICT PASS — token-authz gen-10 @ `1e65774` (121-pin sweep clean); `.agents/tester/` only, 3 files, +55/−1 — docs-only, no source change). **Diff vs base (`a3f0335..1408e8b`): 97 files, +11710/−899.** **WORKING TREE CLEAN — no source-file edits pending** — `git status --porcelain` shows ONLY `.agents/` entries (`approver/active.md` M, `tidier/notes.md` M, approver tracking ??, this planning dir ??); **no stray temp test files remain — `pkg/decision/zz_probe_tmp_test.go` is verified ABSENT.** **Anchor convention:** every `file:line` anchor is **base-relative to `a3f0335`** unless marked; the anchor-shift table in `§0.1` gives the net `a3f0335..4be0a12` deltas (the later commits touch test/docs files plus the 4 test files of `1e65774`: `migrations_test.go`, `handler_group_test.go`, `handler_test.go`, `authz_claim_pins_test.go`; `1408e8b` touches `.agents/tester/` only ⇒ zero anchor shift).

**Lane:** `feature/token-authz-allowed-groups` · **Base commit:** `a3f0335` · **Worktree:** `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-token-authz` · **anchors base-relative** (shift table: `plan.md` §0.1)
**Authority chain:** Emperor's ruling (ADR §1.1) > ADR D1–D16 (`adr-token-permission-model.md` §2) > `decisions.md` Q1–Q13 recommended defaults > `plan.md` (execution detail) > the three phase files.
**Ship posture:** USER-ORDERED with *"ship it; no approval hold"* (`decisions.md` S-14). The standing grant's ask-back on auth changes is **DISCHARGED for this lane** — there is no approval gate anywhere in this plan.
**~~PLAN ONLY~~ SUPERSEDED (fix pass C, CRITICAL 5):** this line described the planning-only phase; the lane is now **fully landed** — see the REAL STATUS block immediately above. Every substantive claim below is cited to a sibling artifact (§Artifact index); file:line restatements are the verified-at-`a3f0335` code facts listed in `plan.md`/`adr-token-permission-model.md` §11.

**Orientation (defect → change → cost).** The defect is a token-scope ESCAPE class: token scope is checked at exactly four sites (ADR §1.3), all against the CLIENT-REQUESTED model string — and the decision GROUP arm returns at `pkg/decision/handler.go:325` *before* the allow-list at `:342-354`, so a decision-group request is served with **zero** scope evaluation; likewise the chat/anthropic group rewrite materialises `[member.ID] + member.FallbackChain` unchecked (`pkg/store/database/store_group_rewrite.go:101-102`), chain advance reads whatever list it was handed, and re-entry can re-offer an excluded member. This commission changes authorization from a string-level site-check into a **reach set** (`Reach(T)`, plan.md §2.1) computed **once per request**, with out-of-scope candidates **removed from every candidate list at construction** — plus a new `allowed_groups` token axis, a fail-closed grant scanner, and the admin/FE surface to write it. The cost: migration `034` (additive column **+ a data repartition** whose rollback has a load-bearing order, §Rollback), a **121-pin landed ledger (88 Go + 33 FE — `plan.md` §13.1a; no independently restated number)** + 10 FE pins, and one honest narrowing — the two live narrow smoke tokens lose their implicit member-chain reach (intended per ADR D5). Rollback Tier-1 has a **prohibited order** (image-rollback-first would make the narrow tokens full-access wildcards — §Rollback); everything else lands under the standing grant with no approval hold.

---

## Objective

Replace the string-level, site-by-site token scope check with a **reach-based, list-property authorization model** and add the `allowed_groups` permission axis, so that no request authenticated with token `T` can contact any model outside `Reach(T)` — on any lane, at any layer, including the decision-group lane that today bypasses scope entirely (plan.md §1.1; ADR D1/D7).

**The Emperor's ruling (verbatim, ADR §1.1):**

> "we not only need to fix this allowed_models (update) but also the add feature allowed_groups (allow reaching the group and all models in that group). On each load balance, fallback layer, we need permission check for token and exclude the model from the list."

Three obligations are extracted and **not re-opened** (ADR §1.1): O-a fix `allowed_models`; O-b add `allowed_groups` reaching the group **and all models in that group**; O-c check permission on **each load-balance/fallback layer** and **exclude** the model from the list (never an attempt-time skip).

**Reach formula (plan.md §2.1, frozen):**

```
Reach(T) =  { id : id ∈ T.allowed_models }
          ∪ { g  : g  ∈ T.allowed_groups }                 // the group TARGET is reachable …
          ∪ ⋃_{g ∈ T.allowed_groups} { m : m ∈ Members(g) } // … and its CURRENT members
```

`Members(g)` is read at request time out of the **IN-MEMORY models snapshot** the cache layer maintains (`GetModels()` / `currentModelRows()`), invalidated on group writes — **REWRITTEN per Ruling 5 / I14** (the former cache-backed `GetModelNative` mechanism is dropped: it is a live SELECT under `m.mu.RLock`); never snapshotted into the token row (D12/I6); `Reach` includes the group id itself (what makes the 034 repartition behaviour-preserving, plan.md §5.4); `Reach` is computed **once per request** before any candidate list is built (D2/I2).

### The defect in one table

Exactly **FOUR** scope-check sites exist today (ADR §1.3, exhaustive grep at `a3f0335`; research-authz-surfaces.md §1) — all evaluate the **client-requested string**, and they are **not equivalent**:

| # | Site | What it checks today | Guard shape | 403 at |
|---|---|---|---|---|
| S1 | decision MODEL arm — `pkg/decision/handler.go:342-343` | `modelID` = the client-requested string | guarded: `authToken != nil && len(AllowedModels) > 0` | `:351` |
| S2 | image-gen — `pkg/imggen/handler.go:267-268` | `modelID` | guarded (same form); entry-only lane (no chain) | `sendModelNotAllowedError` `:713-723` |
| S3 | chat — `pkg/proxy/handler.go:839` | `rc.reqLog.Model` (client string, `:835`) | guarded (`:832-837`) | `:854` |
| S4 | anthropic — `pkg/proxy/handler_anthropic.go:514` | `reqLog.Model` | **UNGUARDED** — no length pre-guard; equivalent to S1–S3 only because `IsModelAllowed` maps empty ⇒ wildcard (`pkg/auth/token.go:116`) | `sendModelNotAllowedError` |

**Escape surfaces** (ADR §1.4; research-runtime-plumbing.md):

| Surface | Evidence | Why it escapes |
|---|---|---|
| decision GROUP arm | `pkg/decision/handler.go:317-326` **returns at `:325`**, before the allow-list at `:342-354` | **zero scope evaluation** for a decision-group request (gen 3). `pkg/decision/group.go` has **zero** scope checks; `authToken` is threaded only for metering (`:341`) |
| group rewrite | `pkg/store/database/store_group_rewrite.go:101-102` materialises `[member.ID] + member.FallbackChain` **unchecked**; the four-phase sweep's **Phase 4 (`:270-284`) calls no predicate at all** | an out-of-scope member (or demoted member) is servable; `res.FailedGroupID` is set at `:85/:91/:96` (field declared at `pkg/models/group_rewrite.go:67`) and fires for ANY entry index incl. fallback-position groups |
| chain advance | `pkg/proxy/race_coordinator.go:1899-1912` (`c.models[i+1]` at `:1906`) | reads whatever list it was handed — constraining it needs the **list**, not another site check |
| hedge | `pkg/proxy/race_coordinator.go:421-435` (`c.models[0]` at `:435`) | **explicitly NOT a scope surface** — its own comment `:426-428`: "it does not widen model coverage" (do not "fix" it) |
| re-entry | chat `pkg/proxy/handler.go:1196-1287` (offer `:1213`, rebuild `:1231`, list/pick swap `:1248-1249`); anthropic `:754` / `handler_groups.go:452-542` | the re-offer can re-offer an excluded member unless the exclusion covers the offer path |
| fail-open scan | `pkg/auth/token.go`: malformed JSON (`:63-65`) **and** unknown-raw-type default (`:52-55`) each return `[]string{}` ⇒ `len==0` ⇒ wildcard (`:116`) | a corrupt row grants FULL ACCESS while the log at `:64` says "denying all models" (gen-class sibling of the escape) |

### The ruling in brief (what a reviewer MUST know; full rule set = ADR §2 D1–D16 + §6 I1–I16)

- **Reach-based, computed once per request** — the unit of authorization is the SET of models a request can contact, resolved before any candidate construction; every layer of the request reuses the same set (D1, D2).
- **`allowed_models` = model ids; `allowed_groups` = group ids** (one `models.id` namespace); a group grant reaches the group **and its current members** at request time (D4, D12). A group id in `allowed_models` is legal but **target-only** (D4/I7). Decision models are ordinary models ⇒ `allowed_models` (Q3 adopted).
- **No implicit reach** for a RESTRICTED token: chain targets are not inherited, members are not inherited from a name-level group grant, members' chains are not inherited; backup-only members ARE members (D5; plan.md §2.5 N1–N5). Permission beats resilience — failover narrowing accepted (ADR S-2/S-3).
- **Exclusion = removal from the candidate list at construction**, per layer; no attempt-time skip; the hedge and credential failover are ruled NOT scope surfaces (D6, D9).
- **Wildcard = both axes ABSENT AND the row parsed cleanly** — the test is **CONJUNCTIVE** (plan.md §2.3; decisions.md R-3). A per-axis rule would turn the repartitioned narrow token into a model-level wildcard: the fix would grant MORE access than the bug.
- **Fail-closed on an unreadable row** ⇒ DENY-ALL, every request 403; BOTH fail-open branches flip (malformed JSON `:63-65` AND unknown raw type `:52-55`); the invalid flag lives **on the token struct** because the token cache stores the `*auth.AuthToken` (`pkg/modelscache/tokens.go:117`) — a separately-returned flag would be dropped at the cache boundary (D10; plan.md §2.7).
- **One resolver, one predicate** (`pkg/authz.Scope`/`Resolve`/`Allows`) — no site-local re-implementation; the decision lane is folded into the same predicate (D7/D8). `pkg/decision/policy.go` stays **byte-unchanged** (P7 — its doctrine `:15-18` is "may reorder candidates, never exclude them").
- **Scope denial = 403 `access_denied`**, house envelope byte-identical (no `code` field), `"model"` = client-requested id, excluded member ids go to log+events only, check runs BEFORE any upstream attempt — and "empty because scope" stays **distinguishable** from "empty because unservable" (502) (D14; plan.md §9).
- **Decision-lane error precedence preserved**: 401 → 404 → 400 `decision_misroute` → 400 group-lane misroute → 403 → 502 (D15; a chat-kind group + out-of-scope token stays 400).
- **The exclusion fork, resolved (plan.md §17.2 / §8.1)**: (A) manager-level predicate for the member PICK (`isReachableMember` sits **beside** `isServableGroupMemberBase` at `store_group_rewrite.go:348-360`, which is NOT widened) **plus** (B) request-layer list filter for the CHAINS at four points C1–C4. Neither alone suffices: (B) alone would spuriously 403 a group with an in-scope sibling; (A) alone cannot cover the entry model's `FallbackChain`. The race coordinator needs **no change** (advance/hedge read the already-filtered `c.models`). The decision-lane filter sits **at or before** `pkg/decision/group.go:118` (`:118` and `:119` are adjacent statements — there is no seam "between" them; ADR §3.3's "between" wording is corrected here), never inside `policy.go`.

---

## Scope

### In Scope

| # | Area | Files | Source |
|---|---|---|---|
| S-A | Token grant data model + tri-state scan + fail-closed | `pkg/auth/token.go`, new `pkg/auth/permission.go`, `pkg/auth/store.go` | plan.md §1.2 |
| S-B | One reach resolver + one predicate | new `pkg/authz/` | plan.md §1.2 |
| S-C | Shared seam vocabulary (import-cycle breakers) | `pkg/models/group_rewrite.go` (`ScopeExcluded`), new `pkg/models/reach.go` (`ReachFilter`) | plan.md §1.2 |
| S-D | Storage: migration `034` (schema + data repartition) + registry append | `migrations/{sqlite,postgres}/034_add_allowed_groups.{up,down}.sql`, `pkg/store/database/migrate.go` | plan.md §1.2/§5 |
| S-E | Admin API: POST/PATCH/GET contract + validation + the PATCH body cap (landed, `:2570`; the POST cap was rejected fix-pass 2) | `pkg/ui/server.go` | plan.md §1.2/§6 |
| S-F | Runtime exclusion on every lane/surface (15 rows, §Per-surface coverage) | `pkg/proxy/*`, `pkg/store/database/store_group_rewrite.go`, `pkg/modelscache/models.go`, `pkg/decision/{handler,group}.go`, `pkg/imggen/handler.go` | plan.md §1.2/§8 |
| S-G | FE: **two per-axis pickers (groups excluded from the models picker — LANDED)** + per-axis authoritative save (F1) + second surface + axis-aware badge | `pkg/ui/frontend/src/**` | plan.md §1.2/§12 |
| S-H | Tests (**88 Go + 33 FE = 121 landed pins** — `plan.md` §13.1a enumerated set (gen-10)), gate boundary declaration, ship | §Success Criteria; phase files | plan.md §1.2/§13 |

Preservation set that must survive (plan.md §1.4): wildcard tokens bit-for-bit (P1); anonymous ⇒ 401 (P2); lane gates fail closed (P3); decision error precedence (P4); v202 quota doctrine + 30-minute ABSOLUTE binding TTLs untouched (P5); group selection stays ordering-not-filtering for *health* marks — the new axis is authorization, applied at construction (P6); `policy.go` byte-unchanged (P7); no new DB read on the hot path (P8/I14); no attempt-time skip (P9); `/v1/*` stays public, k8s/ untouched (P10).

### Out of Scope (flagged only — with reason)

| # | Item | Reason / disposition |
|---|---|---|
| O1 | **MCP server-level authorization** | The MCP lane never selects a model — its unit is the server, a different axis (ADR §4.4; decisions.md Q9 adopted). **Named follow-up:** *"MCP server-level authorization: a token-scoped allow-list of upstream MCP server ids, enforced in `pkg/mcp`'s bearer middleware before `ForwardHTTPRequest`."* A **loud startup log line** names the gap (decisions.md O5) — startup-log clause REJECTED fix-pass 2 (decisions.md §6.1 Ruling-rejections #4; no startup-log task added). |
| O2 | Image-gen group/chain surfaces | Image-gen models have no `FallbackChain`; entry check only — but it still calls the shared predicate (plan.md §1.3). |
| O3 | `pkg/ui` scope enforcement | The admin API is the **editor** of scope, never its enforcer. |
| O4 | Compat flag (group id in `allowed_models` ⇒ member reach) | Ruled out — decisions.md Q2/A10; the pre-ship repartition makes it unnecessary. |
| O5 | `code` field / excluded-array on the 403 envelope | Deferred (decisions.md Q8 (b)); envelope byte-identical; re-openable later. |
| O6 | Credential-layer scope | The credential layer never introduces a model id (rotation/hedge/failover are model-preserving) — I13. |
| O7 | **Stale `pkg/store/database/sqlc/schema.sql:48-55`** | Out of the build (no generated package; only mention is a comment at `pkg/store/database/store.go:23`). **Must NOT be regenerated** — left stale deliberately (plan.md §1.3, §16 O3). |
| O8 | Periodic-reconcile write-mutex stall (`credential_group.go:637-638`) | Separate performance item; this plan only guarantees **no new DB read** (I14). |
| O9 | FE/BE `prefix` drift (`types.ts:244`) | Pre-existing, harmless (plan.md §16 O1). |
| O10 | Decision lane writes no per-attempt stdout ordering | Observability gap, not permission (plan.md §16 O5). |
| O11 | Stale in-code comments (`pkg/imggen/handler.go:263`, `:711`; unreachable fail-closed arms `handler.go:857` / `handler_anthropic.go:511-513`) | Fixed **in passing** in the implementing commits; behaviour designed independently of `:857` (plan.md §16 O8/O9). |

---

## Phases

| Phase | Name | Objective | Tasks | Coupling | Status |
|-------|------|-----------|-------|----------|--------|
| 1 | Frozen token scope contract + storage + admin API | Deliver the contract as CODE: `OptionalTokenPermission`/`OptionalAllowlist` write payload (A4) + `AuthToken` fields + `ScanGrant` tri-state (both fail-open branches closed); `pkg/authz` resolver/predicate; `models.ReachFilter` + `GroupRewriteResult.ScopeExcluded`; migration `034` (column + repartition) + registry append; nil-vs-empty store serialization; `allowed_groups` on POST/PATCH/GET + validation (two-state PATCH per Ruling 2; NO POST body cap — rejected fix-pass 2); compiler-forced re-signing of 9 mocks + 8 DDL fixtures. **No lane calls the predicate yet** — deliberately reviewable as "the permission model exists and is correct". | 11 (T1.1–T1.11) | **INDEPENDENT as a unit** (the base phase; phase1-plan.md §1/§7); internally sequential (interface → store → scan → resolver → migration → API). Blocks Phase 2 at compile level and Phase 3-FE via the frozen wire contract. | **COMPLETE — landed `2b9ab6c` + `60d0db4` (+`f9adbce` + `eb137a1`); tree state per REAL STATUS (no source-file edits pending; no stray temp test files)** |
| 2 | Runtime exclusion across every surface | Make invariant I3 true everywhere: scope created on both text lanes (T2.0), entry gate hoisted BEFORE candidate construction (D13), the shared predicate at all four entry sites, scoped member pick at the single manager authority (all four phases incl. Phase 4), `applyReachFilter` at C1–C4, re-entry scoped on both lanes, decision-lane group check + member filter, image-gen entry, decorator compile-time guard, 403-vs-502 distinction. Coordinator/executor/policy.go/credential layer: **zero change**. | 18 (T2.0–T2.16 incl. T2.8a) | **TIGHTLY COUPLED — reviewed as one unit.** Depends on Phase 1. | **LANDED — `391f181` + `e7ffcda` + `156252a` + `1e423de` + `63e9665` + `ce8f2f5` + `0be8d13` + `1e65774` (the 9 obligation pins)** |
| 3 | Frontend + test ledger + gate + ship | Express the two-axis model in the FE (**TWO per-axis authoritative pickers — groups EXCLUDED from the models picker, the LANDED design**; per-axis split authoritative at save per F1; axis-aware badge; `grantAxes.tsx` stored-membership-first `axisOfId`); assemble the anti-vacuous ledger (**121 pins = 88 Go + 33 FE, `plan.md` §13.1a**); run the gate with the boundary declaration pre-filed; ship under the standing grant; post-deploy evidence + tiered rollback anchors. | 13 (T3.1–T3.13) | **SPLIT**: the FE half is INDEPENDENT (parallel with Phase 2); the gate half is TIGHTLY COUPLED; the ship half is STRICTLY LAST. | **FE LANDED — `b0d4875`, `5de3eb4`, `4be0a12`, tidier `81e8788`; tester gate PASS (`1408e8b`); ship PENDING** |

**The enabler of parallelism — the frozen "token scope contract" (plan.md §2, wire shape §2.8).** Phase 1 freezes §2 (tri-state grant axes, conjunctive wildcard, fail-closed, request-time expansion) and §2.8 (the exact POST/PATCH/response JSON) so that the backend engineer (Phase 2: `pkg/auth`+`pkg/authz`+`pkg/ui` plumbing) and the FE engineer (Phase 3-FE) implement **against that text, not against each other's code**. Any deviation is a plan amendment, not a private decision (phase3-plan.md §7).

**Critical path:** Phase 1's atomic **contract commit (T1.1–T1.6)** → Phase 2 (all surfaces, one unit) → Phase 3 gate (G1–G10) → ship (D1–D8). Maximum-parallelism split inside Phase 1 (phase1-plan.md §7): land T1.1–T1.6 first, then T1.7 (migration) and T1.8/T1.11 (API) proceed **in parallel with the start of Phases 2 and 3**. Recommended sub-order inside Phase 2 (fix-pass 2 🟡12): T2.11 (decorator seam) → T2.3 (scoped pick) → T2.4 (rewrite outcome) → **T2.12 (PD-10 coordinator refusal) → T2.13 (PD-11 precedence) → T2.14 (PD-13 audit)** → T2.0/T2.1 (scope + hoist) → T2.5 (filter C1–C4) → T2.7/T2.8 (re-entry) → T2.9 (decision lane) → T2.10 (imggen) → T2.2 (predicate collapse) → §5 tests.

### Per-surface coverage (the commission's checklist — compact plan.md §8.2)

| # | Surface | Today | After | Exclusion happens at | Status on denial |
|---|---|---|---|---|---|
| 1 | decision MODEL arm (`handler.go:340-354`) | guarded `len()>0` + `IsModelAllowed(client string)` | shared predicate `scope.Allows(modelID)`; no `len()` guard | construction (entry gate) | 403 access_denied |
| 2 | decision GROUP arm (`handler.go:317-326` → `group.go`) | **ZERO scope evaluation** (returns `:325` before `:342-354`) | (a) group-level check after the lane gate (`:101-114`); (b) member-filtered copy before `buildGroupPolicy` (`:118`); new scope arm between `:101-114` and `:120-123` | construction (at or before `group.go:118`) | 403 (distinguishable from 502 `decision_all_members_failed`) |
| 3 | chat group-member resolution (`handler.go:783` → `handler_groups.go:68` → `store_group_rewrite.go`) | base shape predicate at `:205/:229/:253`; **Phase 4 `:270-284` unguarded** | `isReachableMember` at all four phases; `ScopeExcluded` when non-empty-but-all-out-of-reach | construction (member pick) | 403 (via `ScopeExcluded`) instead of 502 `group_unservable` |
| 4 | chat chain entries (`handler_functions.go:200-202`) | `[resolvedModel.ID] + FallbackChain`, no token (pre-auth) | `applyReachFilter` post-rewrite, pre-race | construction (list filter) | 403 only if the head is out of reach; else silent removal |
| 5 | anthropic group-member resolution (`handler_anthropic.go:472` → shared helper) | identical to #3 | identical to #3 | construction | 403 (house chat writer reused at `:530`) |
| 6 | anthropic chain entries (`handler_anthropic.go:330-332`) | same as #4 | list filter at C2 (`:539`, pre-walk) | construction | as #4 |
| 7 | chat chain ADVANCE (`race_coordinator.go:1899-1912`, `c.models[i+1]` at `:1906`) | walks the (unfiltered) chain | **NO CHANGE** — reads the filtered `c.models` | inherited | n/a |
| 8 | anthropic sequential advance (`handler_anthropic.go:548-767`, walk at `:550`) | walks `arc.modelList` | **NO CHANGE** — walks the filtered list | inherited | n/a |
| 9 | hedge (`race_coordinator.go:421-435`, `c.models[0]` at `:435`) | same model, secondary credential | **NO CHANGE** — ruled NOT a scope surface (`:426-428`) | none | n/a |
| 10 | cred-failover (`race_coordinator.go:459-485`) | next credential, same model | **NO CHANGE** (I13) | none | n/a |
| 11 | chat re-entry (`handler.go:1196-1287`: offer `:1213`, rebuild `:1231`, swap `:1248-1249`) | re-offers any member; rebuild re-materialises unfiltered members | scoped exclude + scoped rebuild + C4 filter (belt-and-braces with the `:971` loop-top) | construction (offer + rebuild) | status follows **plan.md §9.4's SINGLE attempt-count-keyed rule** (0 attempts + all-scope-excluded ⇒ 403 at any pass #n; ≥1 attempt ⇒ unchanged 502) — pointer only |
| 12 | anthropic re-entry (gate `:728-765`; body `handler_groups.go:452-542`, sticky `:504`) | same as #11 | same as #11 | construction | as #11 |
| 13 | image-gen (`imggen/handler.go:267-268`) | guarded `len()>0` + `IsModelAllowed` | shared predicate, entry-only (no chain on this lane) | entry gate | 403 |
| 14 | credential-layer pick (`store.go:2810/:2950` → `credentiallb`) | picks a **credential** for an already-chosen model | **NO CHANGE** (I13) | none | n/a |
| 15 | decision-group candidate list (`policy.go:58-96`/`:110-136`, consumed at `group.go:118-119`) | candidacy = exists + enabled + lane | the group row handed to `buildGroupPolicy` carries only in-Reach members; `policy.go` byte-unchanged | construction (at or before `:118`) | 403 when `len(order)==0` **because of scope** |

Filter placement rule: filter **before** `applyStickyOrder` at C1–C4, so sticky ordering cannot promote an entry the permission filter then removes (plan.md §8.3). Verified chat anchors: race loop-top is `:971` (the `for {`), sticky reorder `:991`, coordinator construction `:996`; OpenAI re-entry rebuild `:1231`, list/pick swap `:1248-1249` — the earlier `:1247` figure is wrong and must not be used (plan.md §8.3 drift report).

---

## Coupling Map

| | Phase 1 | Phase 2 | Phase 3 |
|---|---|---|---|
| **Phase 1** | — | **tight** (the frozen token scope contract §2 + compile symbols T1.1–T1.6: `authz.Resolve`/`Allows`, `models.ReachFilter`, `ScanGrant`, `auth.OptionalTokenPermission`/`OptionalAllowlist`, `ScopeExcluded`; + T1.7 migration for Phase 2's store tests) | **tight on the wire contract** (§2.8) for the FE half — the FE needs no Go code beyond the frozen text; **independent** until T1.1–T1.6 land |
| **Phase 2** | **tight** (consumes Phase 1's symbols; demotion of `IsModelAllowed` completed here) | — | **independent for the FE unit work** (Phase 3-FE tests run against mocks); **loose** until the live E2E probes; **tight** at the gate |
| **Phase 3** | **tight** (FE implements the frozen §2.8 wire shape; cannot merge before the API accepts the field) | **independent (FE unit) / tight (gate + ship + live probes)** | — |

Shared-contract statement (phase3-plan.md §7): the FE half and Phase 2 run in parallel **only** because plan.md §2.8 + §2.2/§2.3 are frozen at the end of Phase 1. The ship half has no parallelism.
**Phase-2 internal ordering (revision pass):** T2.12 (the `NewRaceCoordinator` empty-list signature change, PD-10) now precedes the other Phase-2 runtime tasks; see phase2-plan.md §3.

---

## Risks

| # | Risk | Impact | Likelihood | Mitigation |
|---|------|--------|------------|------------|
| 1 | **R-3 — the fix becomes the escalation:** a per-axis wildcard rule promotes the repartitioned narrow token (`allowed_models` NULL + `allowed_groups` present) to a model-level wildcard — the fix grants MORE access than the bug (decisions.md §3 R-3). | High | Low (if the contract is implemented verbatim) | The wildcard test is **CONJUNCTIVE** and lives in ONE function (`authz.Resolve`, plan.md §2.3); never re-derived per site. Pins: `TestAuthzResolve_ConjunctiveWildcard` + `TestTokenAPI_PatchClearDoesNotEscalate` green (phase1 exit 4) and the conjunctive-proof row in the `Resolve` truth table (P-G15). |
| 2 | **R-4 — migration `034` ships inert:** the registry is an **ordered slice, not a directory scanner** (`pkg/store/database/migrate.go:24-83`; last entry `{"033",…}` at `:82`) — without the append the SQL files are silently inert and nothing in the build fails (decisions.md R-4, the sixth Amendment-1 trap). | High | Low–Medium | Registry append **immediately after `:82`** carrying the sixth trap comment (plan.md §5.3); `TestMigration034_ForwardFreshDB` proves the column + `schema_migrations` row on a fresh DB (P-G16); phase1 exit 6 makes "034 is not inert" a gate condition. |
| 3 | **R-5 — the permission path goes vacuous:** with an optional-interface shape, the 9 test mocks silently don't implement the group grant ⇒ the UI path is exercised only against the real store ⇒ the anti-vacuous gate can't catch a runtime no-op (decisions.md R-5). | High | Low | `TokenStoreInterface` re-signed so **9 mocks + 1 decorator + 1 store = 11 implementers move in ONE atomic commit** (compiler-enumerated; phase1 T1.6; plan.md §13.2); where the compiler cannot help (`GroupRewriterScoped`), a compile-time assertion `var _ models.GroupRewriterScoped = (*CachedModelsConfig)(nil)` converts a missing decorator forward into a **build break** (P-G25). |
| 4 | **R-2 — the fail-open bug survives:** a malformed row keeps granting everything while the log says "denying all" (decisions.md R-2). | High | Low | `ScanGrant` tri-state flips **BOTH** branches — malformed JSON (`token.go:63-65`) AND unknown raw type (`:52-55`) — into `Invalid` flags carried **on the struct** (cache stores the `*AuthToken`, `modelscache/tokens.go:117`). Pins: `TestScanGrant_TriState` incl. the unknown-raw-type row (P-G14, rewrites the `token_test.go:120-124` expectation) + end-to-end DENY-ALL 403 on all lanes; G0 query 2 proves no live row is unreadable **before** the flip (plan.md §14.3). |
| 5 | **R-6 — "clear the allow-list" escalates:** clearing one axis writes NULL on both ⇒ wildcard (decisions.md R-6; the store collapses nil AND empty into NULL today, `pkg/auth/store.go:57` create / `:254` update). | High | Low–Medium | Explicit-empty encoding end-to-end: `nil`⇒NULL (absent), `[]`⇒`"[]"` (present-and-empty). **TWO wire states (leader Ruling 2, fix-pass 2 = Q13(a)):** PATCH key omitted **or explicit `null`** = keep (column untouched — one name for the write payload, `auth.OptionalAllowlist`, A4); PATCH `[]` = clear. **Wildcard promotion on EDIT is intentionally inexpressible** (re-create the token; release note + FE-9 note). Release note documents the wire flip (fail-closed direction). |
| 6 | **R-1 — the live lane breaks** if Q2 is overruled to a clean break without the repartition: the two narrow tokens per host 403 on their groups (decisions.md R-1). | High | Low (as planned) | The repartition inside `034` (Q2 (b)) makes the migration behaviour-preserving (plan.md §5.4 proof); G0 query 1 names exactly the affected tokens pre-deploy; post-deploy probe T3.12 item 2 — narrow tokens still serve their groups — is the repartition-preserved evidence. |
| 7 | **R-9 — the release gate reports a false boundary violation:** `034` + the registry append change `pkg/store/database/migrations/` and `migrate.go`, which the gate fence asserts at 0 changed files (`.agents/tester/PACKS.md:35`) (decisions.md R-9). | Medium | High (if undeclared) | **Pre-declare the authorized boundary changes to the tester BEFORE the gate** (phase3 T3.8): the four `034_*.sql` files + the registry append. Unauthorized must remain 0 files: `k8s/`, `.github/`, `pkg/config/`; plus 0-byte diffs for `pkg/decision/policy.go`, `pkg/grouplb/**`, `pkg/credentiallb/**`, `pkg/proxy/race_executor.go` — **`race_coordinator.go` is NOT a 0-byte path** (fix-pass 2 A1: T2.12/PD-10 REQUIRES it to change — the deleted `cfg.ModelID` fallback + the two `(*raceCoordinator, error)` signatures; bounded diff shape per phase3 T3.8/G11). |
| 8 | **R-10 — a hidden DDL fixture breaks a lane:** there are **8** hand-rolled `auth_tokens` CREATE TABLE fixtures, not 7; the 8th (`pkg/ui/handlers_usage_test.go:33`) has **neither** grant column and survives only because usage queries never select it (decisions.md R-10). | Medium | Medium | Enumerate and patch all 8 (plan.md §5.7): `pkg/auth/store_test.go:53`, `pkg/mcp/auth_routing_test.go:58`, `pkg/mcp/e2e_test.go:72`, `pkg/mcp/handlers_api_test.go:49`, `pkg/mcp/mcp_endpoint_split_validation_test.go:60`, `pkg/mcp/test_helpers_test.go:43`, `pkg/proxy/handler_integration_test.go:52`, `pkg/ui/handlers_usage_test.go:33` (the last gains **BOTH** columns). A compile-only sweep (`go test ./pkg/... -run XXX_NONEXISTENT`) proves every test package builds (phase1 T1.6). |
| 9 | **R-7 — FE and BE disagree about what a group is:** per-axis pickers + `grantAxes.tsx` keep the STRICT-EXPLICIT split at the last hop (LANDED; decisions.md R-7). | Medium | Low | The frozen wire contract §2.8: ONE picker + **submit-side split by `kind`** (`kind==='group'` ⇒ `allowed_groups`, else `allowed_models`; missing kind ⇒ model, `pkg/models/config.go:87-95`); FE-2/FE-3 pins assert the split; `ModelMultiSelect` reused unchanged. |
| 10 | **R-8 — the three 403 writers drift** into lane-inconsistent envelopes (decisions.md R-8). | Low–Medium | Low | The three existing writers stay **byte-identical and reused as-is** (`pkg/decision/handler.go:784-794`, `pkg/proxy/handler.go:525-535`, `pkg/imggen/handler.go:713-723`); no 4th writer, no new fields; per-lane 403 pins (P-G1, P-G12, phase2 §5.2) assert the exact envelope. |
| 11 | **Live-token reach narrowing** — member chains are no longer implicit for the two narrow smoke tokens per host (plan.md §15). | Medium (intended) | Certain | This is the commission's intent (D5: no implicit reach); the smoke tokens are single-hop so their requests are unaffected (plan.md §15 verdict column). Release note carries it explicitly (phase3 exit 10 (b)); the repartition still preserves member reach. |
| 12 | **Repartition interpretive assumption** — the plan assumes any token whose `allowed_models` names a group id *intended* member reach; a deliberately target-only token would end wider than intended (plan.md §15). | Medium | Low | Auditable, not silent: G0 query 1 names exactly which tokens the repartition touches (`smoke-primary-agentic`, `smoke-primary`, both hosts) and the result is recorded in the ship artifact. |
| 13 | **SQLite JSON1 unavailability** — the repartition needs `json_each`/`json_group_array`/`json_valid`; the pure-Go driver's JSON1 support is asserted, not assumed (OI-1). | Medium | Low | Permanent probe test `TestMigration034_SQLiteJSON1Available` (P-G18) makes it a CI-enforced fact; a **stated fallback** — idempotent Go-side boot backfill (`migrateTokenGrants`) — is a real fork decided by the probe result, not assumed (plan.md §5.5). JSON1 is a dev/test concern only: live hosts run PostgreSQL. |
| 14 | **Hot-path cost / reconcile-stall interaction** — a new DB read on the request path would queue behind the known writer-mutex reconcile stall. | **High** (Ruling 5, fix-pass 2: HARD REQUIREMENT) | Low after the seam fix | **I14 = HARD REQUIREMENT (leader Ruling 5, fix-pass 2):** `membersOf` reads the **IN-MEMORY models snapshot** the cache layer already maintains (`GetModels()` / `currentModelRows()`), invalidated on group writes — **never per-member `GetModelNative` SELECTs** (the approver proved those are live reads under `m.mu.RLock`, `store.go:1095-1145`); Reach resolved once per request, O(1) map lookup per candidate (plan.md §4.4); structural guard `TestPackage_ImportGuard_NoScopeInHotLoop` (P-G22) + G10 perf sanity. |
| 15 | **Pick/list divergence (trap T3)** — the stamped `groupPick.MemberID` absent from the filtered list would mean the pick authority and the filter disagree. | Medium | Low | The pick is made by the scoped predicate, so it is in reach by construction; the assertion branch logs `[authz] PICK/LIST DIVERGENCE` and treats the entry as `ScopeExcluded` (**fail closed**, never serve a list whose stamped pick is absent) — pinned by `TestChat_GroupMember_OutOfScope_PickListConsistent` (phase2 §5.2; plan.md §8.4 T3). |
| 16 | **PD-2 — Postgres `034` malformed-row boot crash (RED):** a `[`-prefixed malformed row makes the in-process boot migration abort ⇒ a boot-outage loop on any host that carries one. | High | Low (with the guard) | Version-agnostic `DO $$ … EXCEPTION WHEN invalid_text_representation` row-by-row guard (plan.md §5.2/§5.4); G0-0 records the PG major version and G0-2 names any malformed row; G0 is read-only and non-blocking. The mitigation does NOT rely on PG 16+ (no `pg_input_is_valid`). |
| 17 | **PD-3 — PATCH "heal to wildcard" (RED):** a nil axis on PATCH must SKIP the column in SQL; healing a DENY-ALL row to NULL/wildcard would silently widen access. | High | Low | Column-skip in the UPDATE (plan.md §6.1/§6.3); **P-G26 RULED outcome: PATCH with an omitted axis (or explicit `null`) and an invalid flag ⇒ `200 {"success":true}`, column byte-untouched, invalid-omitted NEVER heals to NULL/wildcard — NO 400**. The architect's `400`-rejection variant is REJECTED (Ruling 2, fix-pass A); the leader ruled column-skip. |
| 18 | **PD-10 — `NewRaceCoordinator` empty-list fallback (RED):** the hidden `if len(models)==0 { models=[]string{cfg.ModelID} }` fallback at `race_coordinator.go:338-340` (a 5th list build) turns a scope 403 into a 502. | High | Low | The constructor returns `(*raceCoordinator, error)` and refuses an empty slice; the fallback is deleted; callers assert `len>0` pre-spawn (plan.md §8.6); P-G31. |
| 19 | **PD-9 — strict resolved-target authorization narrows alias-only grants (AMBER):** a token granting only a shadow/claimed alias is denied at entry (the resolved target authorizes), 403 quoting the client-requested claimed string. | High | Low–Medium | G0-4 names the affected tokens pre-deploy; the release note carries the narrowing; the anti-union grep guard is an exit criterion; never widened by unioning the alias into Reach (plan.md §7.1, ADR D1). |
| 20 | **PD-6 — cross-tenant cooldown benching (AMBER — fix-pass 2 Ruling 1: COOLDOWN-SKIP MANDATED):** a scope-excluded candidate is never an attempt and never writes a cooldown (no `ExcludeAndReselect` write, no `skipcache.Mark`); the shared-pool benching window is thereby closed at the source. | High (defect) | — | **Fix MANDATED** (leader Ruling 1, fix-pass 2): scope rejections in all selection phases skip every cooldown/skip-cache write; `ExcludeGroupMemberScoped` never writes a cooldown for a scope-refused offer; multi-token pin P-G32 ("Token A's scope exclusion never cools M1 for wildcard Token B"). The prior accept-and-document disposition is RETRACTED (unsourced); decisions.md R-11 + ADR §4.7 corrected. |

---

## Success Criteria

| # | Criterion | How to Measure | Threshold |
|---|-----------|----------------|-----------|
| 1 | **Pack A green** — `./pkg/proxy/... ./pkg/store/... ./pkg/decision/... ./pkg/ui/... ./cmd/...` (9 pkgs), `-timeout=240s` inside `timeout 300`, **HOME-quarantined** (`HOME=$(mktemp -d)`, `unset DATABASE_URL PORT`, config.json hash-guarded against the absolute real-home path) | gate log exit code | exit 0; only registry-quarantined flakes adjudicated (`QUARANTINE.md:7` CloseLifecycle, `:12` PeakHourParity) — never silently ignored |
| 2 | **Pack B green** — module-anchored complement (re-measure the count, keep the `(/|$)` anchor) | gate log exit code | sole expected failure = the quarantined **httpbin redirect pair** (`QUARANTINE.md:11`, deterministic external-dependency) + **TOCTOU_CoolingReverify** (`QUARANTINE.md:10`, Pack B) — adjudicate, do not "fix" |
| 3 | **Anti-vacuous (Go):** every ADDED `func Test` shows a literal `--- PASS` in the gate's own log | grep the gate's own logs for the **`plan.md` §13.1a enumerated set — 88 Go + 33 FE = 121 pins** (single source of truth); set arithmetic vs `$BASE` | exactly **1** `--- PASS` per name; 0 MISS / 0 SKIP; `added == expected`, `unlisted == 0`, `absent == 0`; modified tests (e.g. `TestScanAllowedModels` → `TestScanGrant`) declared in the ledger so they are not "unlisted" |
| 4 | **Anti-vacuous (FE):** every ADDED FE case token appears exactly once | grep the vitest log for the **33 FE cases in `plan.md` §13.1a** (no other count) | exactly 1 occurrence each; skipped (`×`) count = **0** |
| 5 | **FE suite green** — `cd pkg/ui/frontend && timeout 300 npx vitest run` | vitest summary | all green, `×` = 0; the three flipped assertions (`TokenForm.test.tsx:82/:83/:84` — `:84` is the group assertion) pass under the **renamed** title (old title at `:41` deleted) |
| 6 | **tsc baseline frozen** — `npx tsc --noEmit` vs the committed comparator (`tsc-baseline.txt`: 19 errors / 9 files, verdicts are count+file-set, not line numbers) | run the comparator | **zero NEW error locations**; baseline file unmodified |
| 7 | **`034` is applied, not inert** | `TestMigration034_ForwardFreshDB` (column + `schema_migrations` row) locally; post-deploy `SELECT ... FROM schema_migrations WHERE version='034'` on **both** hosts | column exists; `034` row present on both hosts; repartition idempotency proven (`TestMigration034_RepartitionIdempotent` — second run changes 0 rows) |
| 8 | **Compiler-forced surface complete:** 9 interface mocks + decorator + store re-signed; 8 DDL fixtures carry the new column(s) | `go build ./...` + `go vet ./...` green; compile-only sweep compiles every test package | 0 compile errors; the 11th implementer missing = build break by construction (Q5 (a)) |
| 9 | **Decorator obligations proven** | decorator-through test (P-G20) + compile-time assertion (P-G25) | `CachedTokenStore` forwards `auth.OptionalTokenPermission` **and drops the cached entry** on `UpdateTokenPermission` (a pre-change validation returns the NEW grants after the change — the drop is effective, not merely called); `var _ models.GroupRewriterScoped` compiles |
| 10 | **Zero-new-DB-read on the hot path (I14)** | structural guard `TestPackage_ImportGuard_NoScopeInHotLoop` (P-G22) + diff review of the request path | `race_coordinator.go`/`race_executor.go` contain **no** `authz.` import and no scope-predicate call; the decision attempt loop contains none; **no** new `QueryContext`/`SELECT` introduced for scope resolution; **negative control must FIRE** |
| 11 | **Behaviour pins hold** (phase2 exit 2/5/6/7/8/9) | the named pins in phase2 §5.1/§5.2 | decision-group not-granted ⇒ **403 + fake-upstream call counter == 0**; granted ⇒ 200 with member-id metering; wildcard bit-for-bit on every surface (`authz.Resolve` returns nil); all-out-of-scope group ⇒ **403 never 502** on all three group-capable lanes; all-disabled ⇒ 502 intact (arms distinguishable); chat-kind group + out-of-scope token ⇒ **400**, never 403; denied request creates **no** group binding/sticky write |
| 12 | **Boundary + purity checks** | `git diff --stat $BASE..HEAD` on the declared paths | only the pre-declared `034` files + `migrate.go` under the fence; **0-byte diff** for `pkg/decision/policy.go`, `pkg/grouplb/**`, `pkg/credentiallb/**`, `pkg/proxy/race_executor.go`; **`race_coordinator.go` MUST change** (A1: T2.12 bounded diff — deleted `cfg.ModelID` fallback + `(*raceCoordinator, error)` signatures, nothing else); `k8s/`, `.github/`, `pkg/config/` = 0 files |
| 13 | **Deploy evidence** (phase3 T3.12) | live probes both hosts | `/fe/api/version` == shipped build on both; `schema_migrations` has `034` on both; **narrow tokens still serve their groups** (repartition-preserved); wildcard reaches every kind; out-of-scope ⇒ 403 `access_denied` with the client-requested id and **no `code` key**; decision-group request without the grant ⇒ 403 (gen-3 defect dead in production); no 502 `group_unservable` regression in first minutes; zero unexpected restarts |
| 14 | **G11 — race_coordinator diff-shape check:** the diff of `pkg/proxy/race_coordinator.go` vs base is reviewed for the removed fallback and the new error return; constructor signature change present; no `cfg.ModelID` fallback remains | diff review + grep | `grep -n "cfg.ModelID" pkg/proxy/race_coordinator.go` returns 0 lines; constructor signature is `(*raceCoordinator, error)`; P-G31 green |
| 15 | **G12 — four greppable ruling checks (the fold-in guard):** (i) no `cfg.ModelID` fallback in race_coordinator; (ii) **INTENT: no per-axis LENGTH GUARD governs authorization** (pin `TestPackage_NoPerAxisLenGuardOutsideAuth` — comment-stripped, negative control); a raw grep is NOT used (3 correct-tree hits are EXPECTED: `pkg/authz/scope.go:93` capacity hint, `pkg/modelscache/tokens.go:144` slice copy, `pkg/proxy/handler.go:855` comment); reach only via `authz.Resolve`/`Scope.Allows`; (iii) no live `UltimateModel`/`ExcludeFromUltimateSwitching` reference (PD-13 audit); (iv) **COUNT-FREE, COMMENT-STRIPPED INTENT** on the 034 SQL: `NULLIF` present in **each** executable SET arm of the **SQLite FORWARD** file only (the PG down = plain `json_agg` + exception guards, NO NULLIF), the PG guard structurally inside the `DO $$ … LOOP` (mirrors TestMigration034_PGGuardShape), `pg_input_is_valid` absent from **executable** SQL (comment-only OK) | grep of the tree | all four checks hold as INTENT assertions; **the 034 SQL is FROZEN — a mismatch is a gate-WORDING defect to report, NEVER a licence to edit the migration**; P-G22 and P-G33 carry the structural guards |
| 16 | **New Go pins P-G26…P-G34 green** (PD-3 keep-invalid = **200 + column byte-untouched + invalid-omitted NEVER heals, NO 400** — Ruling 2; D13 entry-gate hoist zero writes; anthropic envelope parity; cache-read clone; decision group row copy; empty-list guard; cross-tenant cooldown documented interaction; enabled/disabled precedence; shadow/claimed alias 403) | gate log `--- PASS` | exactly 1 `--- PASS` per name; 0 MISS / 0 SKIP |
| 17 | **Phase pin sets:** phase1 T1.10/T1.11 carry the PG-guard shape test + the `'[broken'` seed case; phase2 carries the alias-only 403 twins + the strict-gate pins (P-G6/P-G34); phase3 carries the G0-0..G0-4 inspection set executed on both hosts | gate log + ship artifact | all named pins green; G0 output recorded for both hosts |

---

## Research Insights

Key verified findings that shaped this plan (all read at `a3f0335` inside the worktree — the main checkout is a **stale tree**; never cite line numbers from it: ADR §11, research files' headers):

- **Exactly four scope-check sites**, all on the client-requested string; S4 (anthropic, `handler_anthropic.go:514`) is the **unguarded** form — the equivalence with the guarded three evaporates once "empty" becomes a distinct state (ADR §1.3/D8; research-authz-surfaces.md §1).
- **The decision-group bypass** is total: the group arm returns at `handler.go:325` before the allow-list; `pkg/decision/group.go` (723 lines) has zero scope concepts and `authToken` is consumed only by metering (`:341`) — so no plumbing change is needed to enforce there (ADR §1.3).
- **Escape surfaces enumerated** with exact seams (ADR §1.4; research-runtime-plumbing.md): rewrite chains `store_group_rewrite.go:101-102`, Phase 4 `:270-284` predicate-less, `FailedGroupID` setters `:85/:91/:96`, advance `:1906`, hedge `:435` with its own "does not widen model coverage" comment (`:426-428`), re-entry `:754`/`:1196-1287`.
- **Fail-open has TWO branches, not one**: the commission's evidence named only malformed JSON; the unknown-raw-type `default:` case (`token.go:52-55`) also returns `[]string{}` ⇒ full access (ADR §11 correction 2).
- **The store collapses nil and empty** (`auth/store.go:57` create, `:254` update) — the explicit-empty encoding is therefore load-bearing, not cosmetic (plan.md §2.2/§2.8).
- **Line-number drift corrections** the implementers must honour: chat race loop-top `:971` (sticky `:991`, coordinator `:996`); OpenAI re-entry rebuild `:1231` + swap `:1248-1249` (`:1247` wrong); decision-lane scope filter **at or before** `group.go:118` (adjacent statements — no "between" seam); `anthropicRequestContext` has **no token field today** (`:1959-2031`), the only struct change on that lane; `FailedGroupID` declared at `pkg/models/group_rewrite.go:67` (ADR §11; research-runtime-plumbing.md §0; research-storage-api-fe.md §0).
- **Fixture/mock census (verified):** 8 hand-rolled `auth_tokens` DDL fixtures (plan.md §5.7 — the brief's "7" was a drift); 9 test mocks + 1 decorator + 1 store implement `TokenStoreInterface` (plan.md §13.2); decorator-through precedent `pkg/modelscache/models.go:1321-1341` + `pkg/proxy/win_clear_winner_identity_test.go:114-190`.
- **FE anchors (verified, LANDED):** the models picker **EXCLUDES group rows** (`TokenForm.tsx:84-85`/`:159-181`; pin `TokenForm_PickerExcludesGroupModels`); the landed suite is the 33 FE cases in `plan.md` §13.1a (13 TokenForm + 8 grantAxes + 4 TokenList + 8 useApi). *(The old "kind filter at `:34` / one picker / flip `:82/:83/:84`" wording is superseded — dated notice only.)*
- **Live inventory** (plan.md §14.3/§15, 2026-11-17): PRIMARY 5 tokens — wildcard `Ha`/`tuyen`/`kha`, narrow `smoke-primary-agentic`→`[agentic-group]`, `smoke-primary`→`[coding-group, quick-group]`; DAODUC 6 — the same plus `smoke-15m` whose class is **UNKNOWN** and must be settled by G0 query 3 before the deploy.
- **Migration precedent:** `032_internal_only_data` is the shipped in-process data-migration precedent (BEGIN TRANSACTION, idempotent, reversible-by-not-delete); migrations run in-process at boot (`pkg/store/database/init.go:19`) before cache priming — no migrate Job, old-image rollback benign at the schema level.
- **Known-flake registry** (`.agents/tester/QUARANTINE.md:7-12`): `TestStoreEngine_CloseLifecycle` (:7), the mcp SSRF trio (:8), usage stress pair (:9), **`TestEngine_GetOrSelect_TOCTOU_CoolingReverify` (:10 — Pack B)**, **the httpbin redirect pair (:11 — DETERMINISTIC Pack B failure, external-dependency, adjudicate never "fix")**, `TestStoreEngine_PeakHourParity` (:12 — fires 23:59:00–23:59:59.999 UTC only).

---

## Open Questions

**Ratification table (decisions.md §6 — Q1–Q13).** Every row carries a recommended default; **ratify or overrule each**. Nothing blocks the build — the plan implements the defaults pending your marks. **Q2 and Q13 are the only rows with a live-lane consequence**: overruling either forces an operator step onto the release checklist (decisions.md §6 footer).

| Q | Recommended default (one line) | Ratify / Overrule |
|---|---|---|
| Q1 | JSON TEXT column + migration `034` (SQLite `ALTER TABLE ADD COLUMN`, PG `ADD COLUMN IF NOT EXISTS`) + registry append after `migrate.go:82`; rollback = additive (no schema rollback), data repartition reversible by a documented reverse UPDATE | ☐ Ratify ☐ Overrule |
| **Q2** | **Clean break + pre-ship data repartition inside `034`** (legacy group ids move to `allowed_groups`; live narrow tokens behave identically) — **live-lane consequence** | ☐ Ratify ☐ Overrule |
| Q3 | `allowed_groups` = group ids only (any lane); decision models stay in `allowed_models` | ☐ Ratify ☐ Overrule |
| Q4 | `allowed_groups`: existence + `kind='group'` ⇒ 400; `allowed_models`: existence-only ⇒ 400 (a group id accepted, target-only); validation on the **inbound** set only | ☐ Ratify ☐ Overrule |
| Q5 | Extend the two existing `TokenStoreInterface` methods with ONE write payload `auth.OptionalTokenPermission`/`OptionalAllowlist` (A4); all 11 implementers in one atomic commit; decorator forward + decorator-through test | ☐ Ratify ☐ Overrule |
| Q6 | `allowed_groups` on POST/PATCH/responses; snake_case; ~~POST gains a 64 KB body cap~~ **REJECTED fix-pass 2** (PATCH cap landed only); old-FE/new-BE and new-FE/old-BE both safe | ☐ Ratify ☐ Overrule |
| Q7 | Per-axis tri-state (absent / present / invalid) + **conjunctive wildcard**; the invalid flag lives **on the token struct** (cache-safe) | ☐ Ratify ☐ Overrule |
| Q8 | 403, existing envelope byte-identical, `model` = client-requested id; excluded members to log + events; check before any upstream attempt | ☐ Ratify ☐ Overrule |
| Q9 | MCP = explicit non-goal + named follow-up (server-level authorization) ~~+ loud startup log~~ **(startup-log clause rejected fix-pass 2 — decisions.md §6.1)** | ☐ Ratify ☐ Overrule |
| Q10 | Member expansion resolved **per request** against the live model set; never cached; the token cache holds grant lists only | ☐ Ratify ☐ Overrule |
| Q11 | **LANDED: TWO per-axis pickers**, groups EXCLUDED from the models picker (`TokenForm.tsx:84-85`/`:159-181`; pin `TokenForm_PickerExcludesGroupModels`); split authoritative at save (F1) — supersedes the one-picker recommendation | ☐ Ratify ☐ Overrule |
| Q12 | Chain filter in the request layer at the four list-build points + the two decision points; never inside `policy.go`; never inside `models.GroupRewriter` — **refined by plan.md §17.2 to (A) manager predicate for the member PICK + (B) request-layer list filter for the CHAINS** | ☐ Ratify ☐ Overrule |
| **Q13** | **Explicit-empty end-to-end: store `nil`⇒NULL, `[]`⇒`"[]"`; PATCH `[]` = clear; POST `nil` = absent = wildcard** — **live-lane consequence** (the wire meaning of `PATCH {"allowed_models":[]}` flips to "grant nothing") | ☐ Ratify ☐ Overrule |

**RULED (fix-pass A, no longer open): PD-9 = strict row-id narrowing (resolved-only + release note + G0 shadow check); PD-10 = no new rule (single-pass chain rewrite ⇒ a group id in a chain is a documented pre-existing 502 wart, not a scope escape; reach filtering still applies).** **Also open for the owner (plan.md §17.4):** RQ-1 ratify the 13 rows as recommended (§17.2 lists the five places the plan refines them) · RQ-2 whether the 403 body later gains a `code` (default: no, deferred, re-openable) · RQ-3 whether `smoke-15m` is wildcard or narrow (settled empirically by G0 query 3 pre-deploy; no code impact).

### Fold-in open items (wave 2)

- **(a) Architect's §1.4 concurrence — SATISFIED (fix-pass 2 Ruling 1): the cooldown-skip is MANDATED.** The architect's §1.4 concurrence was conditional ("airtight if and only if the catalogued defects are resolved", naming A-3/A-PD-3). Fix-pass 2 implements the architect's remedy as a MANDATE: scope rejections in Phases 1–3 skip the `ExcludeAndReselect` cooldown writes, `ExcludeGroupMemberScoped` never writes a cooldown for a scope-refused offer, and the multi-token pin P-G32 ("Token A's scope exclusion never cools M1 for wildcard Token B") is a required pin. The earlier "leader ruled accept-and-document" claim is **RETRACTED** (unsourced — no ratification record); decisions.md R-11 and ADR §4.7 are corrected to mandate the skip. No open item remains on this defect.
- **(b) Live PG major-version question — RESOLVED (not open).** The version-agnostic guard was ruled (PD-2), so PG 16+ is NOT required. G0-0 records `SELECT version();` verbatim in the ship artifact for traceability only; no `pg_input_is_valid` is used anywhere.
- **(c) `smoke-15m` classification — remains a G0-3 inspection item.** Wildcard vs narrow is settled empirically by G0 query 3 pre-deploy; no code impact either way.

**Named follow-ups deferred by ruling (not blocking):** MCP server-level authorization (a token-scoped allow-list of upstream MCP server ids enforced in `pkg/mcp`'s bearer middleware before `ForwardHTTPRequest` — decisions.md Q9/O5; the startup-log clause was rejected fix-pass 2 (decisions.md §6.1 — no startup-log task)) · the 403 `code` field (Q8 (b)).

---

## Rollback

**SCHEMA vs DATA — two facts, never conflated.** (a) The **COLUMN** `auth_tokens.allowed_groups` is additive (`TEXT DEFAULT NULL`) ⇒ **schema-safe**: the old binary never selects it, so **no schema rollback is ever needed for an image rollback**. (b) The **DATA REPARTITION** is state-changing ⇒ Tier-1 is **NOT permission-neutral on its own**. After `034`, `smoke-primary` has `allowed_models=NULL` + `allowed_groups=["coding-group","quick-group"]` and `smoke-primary-agentic` likewise. The pre-034 binary does not read `allowed_groups` at all, and its `IsModelAllowed` (`pkg/auth/token.go:116`) treats `len(AllowedModels)==0` as **wildcard** ⇒ with the repartition applied and the OLD image running, those two tokens become **UNRESTRICTED — full access to every model on a public `/v1/*` endpoint**. That is the exact defect class this commission closes, so the rollback path must never open it.

| Tier | Action | Order / safety |
|---|---|---|
| **Tier-1** | Image/helm rollback **paired with** the reverse repartition: **Step 1 = the operator-run, idempotent reverse UPDATE** (moves group-kind ids from `allowed_groups` back into `allowed_models` — guarded by `json_valid` so a malformed row is named, not fatal; drafted in the `034_*.down.sql` files); **Step 2 = the image/helm rollback** (daoduc `helm rollback <rev N-1>`; primary `kubectl set image` to the prior digest). | **ORDER IS LOAD-BEARING — reverse repartition FIRST, then image rollback.** Under the **new** binary a reversed row still has `allowed_models` PRESENT ⇒ the conjunctive wildcard does NOT apply ⇒ the two narrow tokens stay **RESTRICTED** — they temporarily 403 on member requests, a narrow, operator-owned, single-hop smoke-token impact, **NO privilege widening** — and the image rollback then restores the **ORIGINAL** semantics exactly. |
| **Tier-2** | Restore the **pre-deploy DB backup** (the D3 daoduc backup / the primary's equivalent), **then** the image/helm rollback. | Reverses the data repartition **and** the schema addition together — the cleanest recovery and the only tier that restores the data without a hand-written reverse UPDATE. |
| **Tier-3** | Operator-run `034_add_allowed_groups.down.sql` **+ delete the `schema_migrations` row for `034`** (no down-runner exists in this repo — `.down.sql` files are documentation-only operator artifacts; if one is run, the version row must also be deleted or the runner treats 034 as applied forever). | **Tier-3 ALSO requires the image rollback**: the new binary selects `allowed_groups` and cannot run against a schema without the column. Order: reverse repartition (or backup restore) → image rollback → down file + version-row deletion. For partial-failure recovery or a deliberate schema retreat — not a routine image rollback. |

> **EXPLICIT PROHIBITION — FORBIDDEN ORDER: image rollback first.** That opens a window in which the two narrow tokens (`smoke-primary`, `smoke-primary-agentic`) are **full-access wildcards under the old binary** on a public `/v1/*` endpoint. The reverse repartition is a **named step of Tier-1**, not a deferred Tier-3 action. The SCHEMA-only statement above must never be read as a data statement.

> **Single-image coupling invariant (Q13(a)):** the FE PATCH builder (undefined/`[]`/`null` tri-state) and the store serialization changes (nil⇒NULL, `[]`⇒`"[]"`) MUST ship in the **same image**. Splitting them re-opens the R-6 escalation (clearing one axis writes NULL on both ⇒ wildcard) or the R-5 fail-open. There is no FE-only or BE-only rollback. Reverse-repartition-FIRST (Q2(b)) is a **BLOCKING** rollback step; it is not optional and not a deferred Tier-3 action.

---

## Ship runbook

**No approval hold.** The standing grant's ask-back on auth changes is **DISCHARGED for this lane** per the commission (plan.md header; decisions.md S-14). Migration `034` — including its data repartition — is a code-shipped, in-house-framework migration with direct shipped precedent (`032_internal_only_data`), planned as an ordinary sequenced step. G0 below is read-only and is **not** a hold.

**Gate (phase3 T3.9; plan.md §14.2):** G1 build/vet/gofmt zero-new-drift → G2 Pack A → G3 Pack B → G4 vitest → G5 tsc baseline → G6 vite build → G7 anti-vacuous ledger → G8 boundary check → G9 boot smoke (`/healthz`, `/fe/api/version`, `/fe/api/models` 200/200/200, clean SIGTERM, real config hash unchanged) → G10 perf sanity. **G0** = pre-ship read-only DB inspection on **both hosts** (G0-0 PG major version; guarded G0-1 group-id inventory; guarded G0-2 unreadable-row scan; G0-3 full token inventory to classify `smoke-15m`; G0-4 shadow/claimed-id cross-check — plan.md §14.3 + phase3 T3.10) — run **before the deploy, not as a gate**; record the output in the ship artifact; nothing is written, nothing waits.

**Landing recipe (house; the main checkout HOLDS `master` — never merge there; plan.md §14.1):**
1. In the sibling worktree: `git merge --no-ff origin/latest`; `git push origin <merge-sha>:refs/heads/latest` (explicit-SHA refspec ⇒ fast-forward regardless of local staleness).
2. Same worktree: `git checkout --detach origin/master` → `git merge --no-ff origin/latest` → gate on **exact parents** (parent1 = prior master tip, parent2 = latest tip) **and tree identity** (`tree(promo) == tree(latest tip)`) → `git push origin <promo-sha>:refs/heads/master`.
3. Cleanup: remove the worktree; `git branch -d` the temp branch. Never `cp -a` a worktree to /tmp and run git there (`.git` is a pointer file).

**Deploy sequence (standing grant; phase3 T3.11 / plan.md §14.5):**

| # | Step | Gate |
|---|---|---|
| D1 | **WAIT for the CI build** (a queued build is not a built image) | CI green |
| D2 | Verify the **image digest registry-side** (not from the CI log alone) | digest matches on all tags |
| D3 | **Deploy daoduc FIRST** (backup host), with a **pre-deploy DB backup** | backup artifact recorded (the Tier-2 anchor) |
| D4 | Migration runs **in-process at boot** (`init.go:19` → `RunMigrations`) — no migrate Job | `schema_migrations` has `034` |
| D5 | **Quick probe daoduc**: `/fe/api/version` == new build; `/healthz`; one `/v1/models`; one granted decision-group request 200 | probe log |
| D6 | **Deploy ensem (primary)** | 0 restarts |
| D7 | **Quick probe primary**: same as D5 **+ one 403 fence probe** (narrow token naming a model outside its reach ⇒ 403 `access_denied`, client-requested id) | probe log |
| D8 | **Push dcs pins, two-step, daoduc-first** (backup `values-pinned.yaml`, then primary `deployment.yaml`) | pins = highest deployed |

**Cross-lane rules:** never downgrade a host; a dcs push is re-fetch/rebase/retry — **never force**; pins record the highest deployed version across hosts. **Post-deploy evidence:** the seven items in Success Criteria #13. **Release note carries THE canonical list — `plan.md` §6.2 RN-1…RN-10** (single source; no locally restated list).

---

## Live-token blast radius

Live inventory per host (plan.md §15; verified 2026-11-17). Both hosts: 2 decision models; 6 groups on primary / 5 on daoduc — **membership definitions untouched**; only token→membership reach changes form.

| Host | Token | Grants today | Behaviour BEFORE | Behaviour AFTER (with 034's repartition) | Verdict |
|---|---|---|---|---|---|
| PRIMARY | `Ha` | wildcard | all models | all models | **UNCHANGED** (P1) |
| PRIMARY | `tuyen` | wildcard | all models | all models | **UNCHANGED** |
| PRIMARY | `kha` | wildcard | all models | all models | **UNCHANGED** |
| PRIMARY | `smoke-primary-agentic` | `allowed_models=["agentic-group"]` | reaches the group and (implicitly) **every member + member chain** | `allowed_models=NULL`, `allowed_groups=["agentic-group"]` ⇒ reaches the group **and its current members**; **member chains no longer implicit** (N3) | reach preserved for members; chains narrowed — **the commission's intent (D5)**; the smoke token is single-hop |
| PRIMARY | `smoke-primary` | `allowed_models=["coding-group","quick-group"]` | both groups + all members + member chains | `allowed_models=NULL`, `allowed_groups=["coding-group","quick-group"]` ⇒ both groups + their current members; chains no longer implicit | as above |
| DAODUC | the same 5 | as above | as above | as above | as above |
| DAODUC | `smoke-15m` | **UNKNOWN class** (decisions.md G3) | — | determined by **G0 query 3** before the deploy | must be classified pre-deploy; wildcard ⇒ unchanged; narrow ⇒ same reasoning as the two above |

**Wildcard invariance:** the repartition's `WHERE … EXISTS (group id in allowed_models)` clause never matches a wildcard row (no group id present) ⇒ the three wildcard tokens per host are **byte-untouched** (plan.md §5.4). **Zero window:** the repartition runs in-process at boot before the host serves traffic — there is **no operator re-scope step and no approval hold**; the earlier operator-PATCH-step idea is withdrawn/superseded (plan.md §15).

---

## Artifact index

All paths relative to `.agents/shared/planning/token-authz-allowed-groups/`. Line counts as measured now (`wc -l`). (line counts measured at gen-11 via `wc -l`; they drift with every corpus pass — re-measure before citing)

| File | Lines | One-line purpose |
|---|---|---|
| `plan-overview.md` | (this file) | Orientation + synthesis: the defect, the ruling, phases, coupling, risks, success criteria, ratification items, rollback, ship, blast radius — the entry point; the specialists carry the depth |
| `plan.md` | 1,586 | The full implementation plan: frozen contract §2, data model + plumbing §4, migration 034 §5 (Ruling-6 atomicity), API §6 (two-state PATCH), §6.2 RN-1…RN-10 canonical release-note list, semantics matrix §7, per-layer mechanics + site table §8, 403-vs-502 §9 (§9.2a cooldown-skip mandate), fail-closed §10, membership dynamism §11, FE §12 (LANDED two per-axis pickers), Go pins §13 + §13.1a (121 landed pins = 88 Go + 33 FE), ship/rollback §14, blast radius §15, out-of-scope §16, decisions cross-ref §17, architect defect fold-in §18 |
| `adr-token-permission-model.md` | 455 | The class-semantics ADR: the ruling §1.1, three generations §1.2, D1–D16 §2, invariants I1–I16 §6, ruled-out alternatives, lineage closure §5, §4.7 cooldown-skip mandate (fix-pass 2) |
| `decisions.md` | 562 | The decision register: settled §1, Q1–Q13 + ratification block §2/§6 (leader rulings 1–6 recorded fix-pass 2), risks R-1…R-12 §3 (R-11 corrected; R-12 added), out-of-scope §4, drift corrections + OI-1…OI-7 §5 |
| `phase1-plan.md` | 404 | Phase 1 (contract + storage + admin API): objective, exact files owned, T1.1–T1.11, OptionalAllowlist write-payload contract (A4), two-state PATCH (Ruling 2), tests enumerated in `plan.md` §13.1a (121 total — 88 Go + 33 FE, `plan.md` §13.1a), dependencies, exit criteria, coupling |
| `phase2-plan.md` | 811 | Phase 2 (runtime exclusion on every surface): files owned, T2.0–T2.16, cooldown-skip mandate (Ruling 1), in-memory membersOf (Ruling 5), strict PD-9 pins, per-layer exclusion pins, structural guards, exit criteria |
| `phase3-plan.md` | 388 | Phase 3 (FE + test ledger + gate + ship): FE file-by-file, FE-1…FE-9, ledger rules T3.7, boundary declaration T3.8, gate G1–G12 + freeze-hash input, deploy D1–D8, tiered rollback T3.13 incl. Step-0 `'[]'` reconcile |
| `architecture-recommendation.md` | 145 | The architect's pressure-test + defect catalog (A-1..A-15) against the corpus; the source of the PD-1..PD-15 fold-in |
| `research-authz-surfaces.md` | 264 | Evidence: the four scope-check sites, the escape surfaces, MCP/`pkg/ui` findings, drift notes |
| `research-runtime-plumbing.md` | 455 | Evidence: candidate-list plumbing — modelList builds, rewrite, advance/hedge/re-entry seams, coordinator construction, drift report |
| `research-storage-api-fe.md` | 366 | Evidence: storage/migration/rollback surface, `/fe/api` token contract, FE anchors, test-harness surfaces, drift report |

---

## Fold-in status

All 15 leader-ruled defects (PD-1..PD-15; legend: architect PDs = A-PD-n, leader PDs = L-PD-n — fix-pass 2 🟡7) are folded into the corpus and verified; plan.md §18 is the audit table (one row per PD, with severity, what changed, where in plan.md, the architect PD it also closes, and the files touched). Adjudications as of fix-pass 2 (2026-11-18): **PD-9/L-PD-9 strict vs union** — strict resolved-target authorization stands (an alias-only grant narrows, carried by the release note, never by widening the gate); **PD-6/L-PD-6 accept-and-document vs the architect's fix** — the earlier accept-and-document disposition was RETRACTED (unsourced) and **the cooldown-skip is MANDATED** (Ruling 1), with the multi-token pin implemented (P-G32). Open for the owner: (a) in §Open Questions; PG major-version and `smoke-15m` are resolved/inspection-only.
