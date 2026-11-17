# Design Ruling — decision-groups commission

> **Commission:** decision-groups (decision models served by a GROUP, plus an admin "Decision Models" container tab with a vertical left-nav: `[models] [group]`). **Worktree:** `../llm-supervisor-proxy-wt-decision-groups` (branch `feature/decision-groups` @ `24c51ef` == `origin/latest`). **Author:** technical-analysis / design-ruling worker. **Date:** 2026-11-16. **Status:** Draft ruling — pending approver review. **Scope:** design only — NO code, NO tests, NO config edits, NO commits, NO pushes, NO deploys. Markdown artifact at `.agents/shared/planning/decision-groups/decisions.md`. Base research: `research-groups.md`, `research-decision.md`, `research-fe-precedent.md` (all in this directory), plus `group-backup-members/design.md` and `load-balance-groups/decisions.md` for house doc format.

---

## Ruling

**Widen (Option A).** The commission is implemented by widening the existing group kind-rules so that a group whose members are decision-kind models resolves to decision members on the `/v1/decisions` lane, reusing the single group entity, the single `models.kind="group"` row, the single `members_json` column, the single kind-blind `pkg/grouplb` engine, and the single four-phase demote-not-filter sweep; the alternative — a parallel decision-group path — is rejected because it would have to re-earn the v193 correctness properties (ordering-not-filtering, win-clear identity, the modelscache decorator-forward trap) from scratch while reusing an engine that is already kind-blind, and it would leave two group entities to keep in sync forever. The lane discriminator is **inferred at request time by unanimity over the CURRENT member rows (no new column, no migration, no DTO/FE field)**, enforced by **write-time homogeneity** so mixed-kind groups are unrepresentable. The decision lane runs its own member loop inside `pkg/decision` (the import guards forbid reusing `pkg/proxy`), reusing the manager's sweep and demotion machinery; sticky/binding is ruled out (no conversation key exists on this lane) and the member conversation key is empty ⇒ a fresh weighted pick per request. Demotion + win-clear ARE in scope, with the classification vocabulary extracted into a new leaf package because `pkg/decision` cannot import `pkg/providers` (transitive-closure guard, verified). Exhaustion returns 502 with a distinct machine code. The text lane's behaviour is byte-untouched and the decision provider vocabulary surface is provably unchanged.

---

> **⚠️ PARTIALLY SUPERSEDED (2026-11-16, user ruling R1–R5).** Two elements of the paragraph above are
> superseded: (1) *"reusing … the four-phase demote-not-filter sweep"* cannot be taken as **ordering**
> reuse — that sweep's phase-4 sort is mark-influenced (`store_group_rewrite.go:229-242`), and R1
> forbids a mark from deciding the pick; (2) *"Demotion + win-clear ARE in scope"* — **win-clear stands**,
> but "demotion" is no longer reordering: a mark **suppresses an ATTEMPT, never a PICK or a CHAIN**
> (ADR-5). Everything else — Option A, D-2 inference, no-sticky, exhaustion envelope, vocabulary
> unchanged, text lane untouched — stands. See **ADR-5 … ADR-10**.

## Evidence basis

Grouped by package; every claim traces to a direct read in this worktree at `24c51ef`. Research sources: `research-groups.md` (existing group machinery), `research-decision.md` (the decision lane end-to-end), `research-fe-precedent.md` (FE container tab + vertical nav + house conventions). Line numbers marked `[wt]` were read directly; a handful carried over from `[grep-main]` were re-verified (see `## Not verified`).

### `pkg/models` — kind rules and data model
- `KindGroup = "group"` `pkg/models/config.go:132`; `IsGroup()` `:154-156`; `IsDecision()` `:144-146`; `IsImageGen()` `:148-150` (groups are neither, so they pass chat misroute guards today).
- `GroupMember = {ModelID, Weight, BackupOnly}` `pkg/models/group.go:42-46`; `MaxGroupMembers = 16` `:25`.
- `validateGroupRules` (G1, G6, G8 self-half) `pkg/models/group.go:60-94`; collection rules `ValidateGroupMembers` (G2 ≥1 + cap, G3 existence + weight, G4 dup, G5 flat, G8 shadow) `:115-214`.
- **G3 chat-kind member rule** `pkg/models/group.go:180-184` (error string `:183`); **doc block** `:111-114` ("members must reference existing chat-kind models").
- **G8 shadow-target chat-kind** `pkg/models/group.go:196-198` (error string).
- G5 group-as-member `pkg/models/group.go:176-179`.
- Kind constants doc: `KindGroup` comment `pkg/models/config.go:117-132` ("served by the normal chat pipeline").
- Decision validation D4 `pkg/models/config.go:293-300` (provider allow-list `openai`/`typesafe`/`openrouter`); D5–D8 `:302-318`.
- `GroupRewriter` interface `pkg/models/group_rewrite.go:79-122`; `GroupRewriteResult` `:14-77` (GroupID, MemberID, MemberPool, FailedGroupID, AnyGroup, ...); empty conversation key ⇒ "fresh weighted pick, no binding" `:88-90`.
- Credential vocabulary `var validProviders` `pkg/models/credential.go:15-60`, checker `:63-65`, accessor `:73-80`.

### `pkg/store/database` — runtime resolution and write validation
- `isServableGroupMemberBase` (chat-only predicate) `pkg/store/database/store_group_rewrite.go:291-301`.
- `pickServableGroupMember` four-phase sweep `:130-245` (Phase 1 `:147-174`, Phase 2 `:176-201`, Phase 3 `:203-227`, Phase 4 `:229-243`); `memberDemotionExpiry` `:273-282`.
- `RewriteGroupEntries` `:49-86`; `ExcludeGroupMember` `:406-431`; `ClearGroupMemberCooldown` `:439-444`.
- `validateGroupWrite` (passes `""` internalProvider) `pkg/store/database/store.go:2112-2117`.
- `ResolveInternalConfigWithAffinity` row-identity resolution `pkg/store/database/store.go:2552-2567`; credential engine `GetOrSelect` `:2614-2619`.
- `GroupMemberRefs` `pkg/store/database/store.go:1677-1686`.
- Manager `UpdateModel` allows kind change in place (no immutability guard) `pkg/store/database/store.go:1699-1720` (verified).

### `pkg/grouplb` — kind-blind engine
- Imports stdlib only (`errors`, `hash/fnv`, `log`, `sync`, `time`) `pkg/grouplb/engine.go:3-9`.
- `MemberRef = {ModelID, Weight, Position, BackupOnly}` `pkg/grouplb/engine.go:45-62`.
- `ClearCooldown` `pkg/grouplb/engine.go:1274-1287`; `MemberCooldownUntil` `:1293-1311`.
- Import guard `TestEngine_ZeroCredentialImportGuard` `pkg/grouplb/engine_test.go:401`.

### `pkg/modelscache` — decorator forward trap
- Interface `pkg/models/config.go:446-477`; `CachedModelsConfig` `pkg/modelscache/models.go:93`.
- `RewriteGroupEntries` forward `pkg/modelscache/models.go:1043-1048`; `ExcludeGroupMember` `:1056-1061`; `ClearGroupMemberCooldown` (optional-interface forward) `:1079-1085` with CRITICAL-FIX comment `:1063-1085`; `GetModelNative` (cache-backed) `:1087-1139` (claim-blind, `:1105-1109`).
- Decorator-through tests `pkg/proxy/win_clear_winner_identity_test.go:120`, `:218`.

### `pkg/decision` — the lane
- Handler struct + resolvers `pkg/decision/handler.go:104-131`; `configGetter` `:116-118`; resolver setters `:737-752`; safe wrappers `:754-766`.
- `IsDecision()` gate `pkg/decision/handler.go:265-277` (returns 400 `decision_misroute`).
- Credential resolution `pkg/decision/handler.go:299-310`; single upstream call `:430-433`; commit point `:517` (PassAsIs) / `:541`/`:543` (PromoteToError); no early header set.
- `Outcome{RelayStatus, Reason}` `pkg/decision/converter.go:28-31`.
- Adapter collapse to 502 `pkg/decision/openaidecision.go:169-179`, `pkg/decision/typesafe.go:426-429`.
- `openAIErrorWithCode` `pkg/decision/handler.go:709-714`.
- Metering success-only, detached 5 s ctx `pkg/decision/handler.go:624`, `:635-640`; no row on failure `:606-608`.
- Import guards `pkg/decision/handler_test.go:40`, `:65`, `:90` (transitive closure).
- Registry `pkg/decision/registry.go:49-53` (openai/typesafe/openrouter).
- Type shape `pkg/decision/types.go:130-135` (no stream field, no conversation key).
- `ExecuteTest` seam `pkg/decision/test_exec.go:118`.
- `cred, ok := h.safeResolveCredential(modelID)` at `:299`; `cred.InternalModel` in scope at `:361`.

### `pkg/proxy` — chat lane guards and group machinery (unchanged behaviour)
- Chat decision-misroute guard `pkg/proxy/handler.go:576-582`; Anthropic `pkg/proxy/handler_anthropic.go:272-279`.
- `/v1/models` exclusion `pkg/proxy/handler.go:349-352`; claimed-ids hide `:336-348`.
- Group rewrite call site `pkg/proxy/handler.go:669`; `groupTriedMembers` `:691`, `:975-979`; re-entry `:1067-1160`.
- Win-clear `pkg/proxy/handler.go:905-920`; `clearWinnerUpstreamSkip` `pkg/proxy/handler_helpers.go:867-886`.
- Skip-cache `Mark` call sites `pkg/proxy/race_coordinator.go:2106-2107`, `pkg/proxy/handler_anthropic.go:2607-2608`.
- `payloadCommitted` (Anthropic) `pkg/proxy/handler_anthropic.go:664-672`.

### `pkg/providers` — classification vocabulary (NOT importable by pkg/decision)
- `pkg/providers/interface.go:52` — explicit note: not imported from `pkg/proxy/translator`.
- `pkg/providers/openai.go:31` imports `pkg/proxy/translator` → makes `pkg/providers` transitively depend on `pkg/proxy` → `pkg/decision` cannot import `pkg/providers` (verified).
- `IsRateLimitError` `pkg/providers/interface.go:225`; `IsQuotaError` `:348`; `ProviderError{StatusCode, ErrorType, ErrorCode, Message}` `:187`.
- Vocabulary constants `pkg/providers/interface.go:266-286`.
- Decision provider gate `pkg/models/config.go:293-300` (same set, separate surface).

### `pkg/ui/server.go` — admin API mirrors
- POST group kind check `pkg/ui/server.go:941-944`; PUT `:1443-1444`; shadow POST `:962-964`; PUT `:1464`.
- Kind allow-list POST `pkg/ui/server.go:1036-1041`; PUT `:1530-1535`.
- Decision provider gate POST `pkg/ui/server.go:1165-1178` (`:1171`); PUT `pkg/ui/server.go:1631-1643` (`:1636`) — verified in-worktree (the FE research's `:1654-1663` was a main-checkout grep).
- `GET /fe/api/models?kind=` switch `pkg/ui/server.go:790`, `:807`.

### `pkg/skipcache`
- `Mark`/`Marked`/`SoonestExpiry`/`Clear` `pkg/skipcache/skipcache.go:87`, `:102`, `:170`, `:135`; ordering-not-filtering doctrine `:13-16`, `:18-26`.
- References only from `pkg/proxy` and `pkg/store/database` (zero under `pkg/decision`).

### FE — `pkg/ui/frontend`
- `DecisionModelsTab.tsx` 541 lines; `DecisionModelForm.tsx:29` `PROVIDER_OPTIONS = ['openai','typesafe','openrouter']`.
- `GroupModelsTab.tsx` 360 lines; `GroupModelForm.tsx` 450 lines; `GroupMemberEditor.tsx:40` chat-kind filter; `MAX_GROUP_MEMBERS = 16` `:6`.
- `SettingsPage.tsx:66` `TabType` (11 values); `:293-295` `chatKindModels` filter; `:380-521` tab bar; vertical-nav precedent: NONE (zero `role="tablist"`, no nested tabs).
- `types.ts:170-186` `GroupMember` doc block; `Model.kind` union `:122`.
- Hooks `useDecisionModels` `useApi.ts:532-613` (cache key `'decision-models'`, URL `?kind=decision`); `useGroupModels` `:636-713` (`?kind=group`).
- `SettingsPage.tabs.test.tsx:285` `tabs.length === 11`, `:287` `groupIdx === 10`.
- `tsc-baseline.txt` TOTAL_ERRORS=19/TOTAL_FILES=9.

### Migrations / schema
- `030_add_model_kind` — `kind TEXT` unconstrained `migrations/sqlite/030_add_model_kind.up.sql:29`, `postgres/030_add_model_kind.up.sql:14`.
- `031_add_group_fields` — `members_json` + `shadow_model_id` on `models` `migrations/sqlite/031_add_group_fields.up.sql:39-40`, `postgres/031_add_group_fields.up.sql:10-11`.
- Registry `pkg/store/database/migrate.go:63` (030), `:69` (031), `:77` (032).

### Prior precedent
- There is NO prior precedent ruling on non-chat groups — only chat-only scope exclusions: `load-balance-groups/plan.md:28`, `architecture-recommendation.md:25`, `research-codebase.md:536`. G3 is a deliberate, user-decided invariant (flat membership, chat-only).

---

## D-1 — The A-vs-B ruling (widen vs parallel)

**Decision:** **(A) — WIDEN the existing group kind-rules.** A decision group is the SAME group entity (`models.kind="group"` row, `members_json`, `shadow_model_id`, `grouplb` engine, four-phase sweep) whose members happen to be decision-kind models; the only thing that changes is the lane the group is served on. No parallel entity, no second migration, no second engine.

**Rationale (file:line):**
- Groups are not a separate entity: a group IS a normal model row with `kind="group"` (`pkg/models/config.go:132`, `IsGroup()` `:154-156`) and `members_json`/`shadow_model_id` live on the `models` table (`migrations/sqlite/031_add_group_fields.up.sql:39-40`). A "parallel decision-group path" would need either a second member list (its own migration, despite 030 making `kind` unconstrained TEXT) or a lane field on the group row — which is exactly D-2 option (ii), not a genuinely parallel entity.
- The engine is already kind-blind: `pkg/grouplb/engine.go` imports only stdlib (`:3-9`), `MemberRef = {ModelID, Weight, Position, BackupOnly}` (`:45-62`), and `TestEngine_ZeroCredentialImportGuard` pins it (`pkg/grouplb/engine_test.go:401`). There is nothing to duplicate at the engine layer — only the seams around it.
- The four-phase demote-not-filter sweep (`pkg/store/database/store_group_rewrite.go:88-245`), the demotion expiry (`:273-282`), `ExcludeGroupMember` (`:406-431`), and `ClearGroupMemberCooldown` (`:439-444`) are kind-agnostic in their ordering logic. Reusing them is how the decision lane inherits the v193 correctness properties for free.
- There is no precedent blocking (A): only chat-only scope exclusions exist (`load-balance-groups/plan.md:28`, `architecture-recommendation.md:25`, `research-codebase.md:536`). G3 is a deliberate invariant, so widening it is a renegotiation — done deliberately, bidirectionally (text groups reject decision members; decision groups reject chat members), never a generic relaxation.

**Rejected alternative + why:** **(B) a parallel decision-group path** (a second entity, or a parallel `kind="decision-group"` family). Rejected because:
1. It would duplicate the v193 correctness properties — ordering-not-filtering, the win-clear identity discipline (`pkg/proxy/handler.go:905-920`), the decorator-forward trap (`pkg/modelscache/models.go:1063-1085`) — which were bought across three bug-fix rounds and are NOT inherited by a new path; the parallel path would have to re-earn them.
2. It would duplicate the validation surface (G1/G2/G6/G8 field rules `pkg/models/group.go:60-94`, collection rules `:115-214`, the two UI inline mirrors `pkg/ui/server.go:863-979` and `:1364-~1500`, and the parity test `pkg/ui/kind_rules_parity_test.go`).
3. It would leave two group entities to keep in sync forever (engine registration, decorator forwards, `OnGroupChanged` fan-in `pkg/store/database/store.go:1666`, admin CRUD, FE editors).
4. It gains nothing: the engine is kind-blind, so (B) would reuse `pkg/grouplb` anyway — the "parallel" is a lie; the only real difference would be the data model, which (A) already handles via homogeneity.

**Cost / risk:** (A) renegotiates exactly one deliberate invariant (G3, chat-kind membership) at five backend sites + four FE layers, all already enumerated, all already parity-tested. The risk is a missed site (the parity test `pkg/ui/kind_rules_parity_test.go` asserts each site independently — that is the guard). The risk is also the bidirectional rule: a careless widening (e.g. "accept any non-group kind") would let chat members into decision groups and vice versa — the ruling below forbids that.

**Verification:** parity test `pkg/ui/kind_rules_parity_test.go` (helper ≡ POST ≡ PUT, each asserted independently) goes green after the widened sites; `pkg/models/group_rules_test.go:105` and `pkg/store/database/store_group_test.go:146` updated to the widened rule; plus a NEW test asserting decision-kind members are accepted AND chat-kind members are rejected on a decision group (and the inverse). A mutation check: dropping the lane predicate re-opens the cross-lane leak.

---

## D-2 — The lane discriminator (the genuine open problem)

**Decision:** **(iii) write-time homogeneity, combined with (i) runtime inference — NO schema change, no new column, no migration, no DTO field, no FE field.** A group's members must all share one normalized kind (chat-or-legacy-`""` | decision); mixed-kind groups are unrepresentable at write time. At request time the lane is **inferred by unanimity over the CURRENT member rows** (not trusted from write time), so a member retyped after the group was written is handled fail-closed. The request-time predicate `isServableGroupMemberBase` becomes **kind-parameterized (defense in depth)**: it takes the expected kind and rejects any member whose normalized kind does not match — "" is accepted ONLY as chat (legacy), never as decision. The shadow target's kind must equal the group's lane. Text groups reject decision members (and decision-kind shadow targets); decision groups reject chat members (and chat-kind shadow targets).

**Rationale (file:line):**
- A group row carries no lane field today (`pkg/models/group.go:42-46`, `migrations/sqlite/031_add_group_fields.up.sql:39-40`) and the `kind` column is unconstrained TEXT (`migrations/sqlite/030_add_model_kind.up.sql:29`) — so a new kind needs no migration, but a new *lane* field would (schema + DTO + FE + all validation mirrors). That cost is exactly what (iii) avoids.
- Write-time homogeneity makes the lane a well-defined function of any one member, so (i) inference is unambiguous and cheap.
- The retyped-member hazard is real and verified: `UpdateModel` (`pkg/store/database/store.go:1699`) does NOT block a kind change (no immutability guard found; `validateBeforeWrite` at `:2085` calls `ValidateKindRules` with whatever kind is supplied). So a member CAN be retyped after the group was written → the predicate must re-check at request time, and inference must use the CURRENT rows.
- The parameterized predicate is the last line of defense: it re-checks the write-time invariant on every pick, covering the "member disabled/deleted/retyped between group write and request" hazard already documented at `pkg/store/database/store_group_rewrite.go:286-290`. Homogeneity alone cannot be trusted at request time.
- The shadow rule must be lane-aware too: a decision group may claim a decision-kind id, a chat group a chat-kind id — G8 at `pkg/models/group.go:196-198` currently requires chat-kind and must be widened in lockstep. Write-time validation order: (1) validate members (homogeneity ⇒ lane), (2) validate shadow target kind == lane.
- The empty-kind (`""`) legacy handling: `""` normalizes to chat (the existing convention `pkg/store/database/store_group_rewrite.go:291-301` treats `""` as servable). A decision group MUST NOT accept `""` members — legacy rows are chat-servable and cannot serve `/v1/decisions`.

**Rejected alternative + why:**
- **(ii) add an explicit lane/kind field to the group row** — rejected for v1: schema change + migration + DTO + FE + all validation mirrors, for a fact that is derivable from the member set. The migration cost is avoidable because `kind` is unconstrained TEXT and homogeneity makes the lane derivable. (Open question Q1 asks whether to revisit this for operational/debuggability.)
- **A generic relaxation ("accept any non-group kind")** — rejected: it silently cross-wires lanes and breaks the misroute guards (`pkg/proxy/handler.go:576-582`, `pkg/proxy/handler_anthropic.go:272-279`). The rule must be bidirectional, never generic.
- **Trusting write-time homogeneity at request time (no parameter)** — rejected: the retyped-member hazard (verified above) defeats it; the predicate exists precisely for this re-check.

**Cost / risk:** the runtime inference costs N member-row lookups per group request (N ≤ 16). The manager computes the lane from the group's member rows using the cache-backed native lookup (`pkg/modelscache/models.go:1087-1109`, `GetModelNative`), so warm-cache lookups are cheap. The four consumers (decision handler entry, chat misroute guard, Anthropic misroute guard, `/v1/models` filter) each invoke the same seam. The risk is the mixed/empty case at runtime (a member retyped or deleted): the group becomes unservable — fail-closed, logged, evented — rather than misrouted.

**Verification:** (1) a write test: a decision group with one chat member is rejected with the homogeneity error; a chat group with one decision member is rejected; (2) a runtime test: a group written as decision, whose member is then retyped to chat via `UpdateModel`, returns `group_unservable` (not a chat success); (3) the parameterized predicate is exercised by the skip-cache ordering tests `pkg/store/database/store_group_skipcache_test.go:62` and `store_group_test.go:592` (meaning-change cases noted in `research-groups.md §8.2`); (4) a mutation test: making the predicate kind-agnostic lets a decision member be served on the chat lane (must fail).

---

## D-3 — In-lane member loop vs reuse of the proxy group machinery

**Decision:** **The member loop lives inside `pkg/decision`** (import guards forbid `pkg/proxy`: `pkg/decision/handler_test.go:40`, `:65`, `:90`). The decision lane reuses the MANAGER's sweep, demotion, exclusion and win-clear seams — never re-implements ordering — and runs its own loop over them. There is NO shared "group lane" package extracted from `pkg/proxy`.

**Rationale (file:line):**
- The import guards are a hard, tested constraint: `TestPackage_ImportGuard_NoProxyImports` (`pkg/decision/handler_test.go:40`), `..._NoImggenImports` (`:65`), `..._TransitiveClosure` (`:90`) — the transitive test walks the package's dependency closure, so an indirect `pkg/proxy` dep is also banned.
- The decision lane needs an ordered member plan + advance-after-failure + win-clear. The manager already exposes exactly these: `RewriteGroupEntries` (`pkg/store/database/store_group_rewrite.go:49-86`, returns `GroupID`/`MemberID`/`FailedGroupID`/`MemberPool`), `ExcludeGroupMember` (`:406-431`), `ClearGroupMemberCooldown` (`:439-444`). The decision handler calls them through `models.GroupRewriter` (structurally asserted, decorator-forwarded at `pkg/modelscache/models.go:1043-1061`).
- The seam list for the decision handler, mirroring `research-decision.md §8`:
  1. **Group detection** replaces/extends the `IsDecision()` gate at `pkg/decision/handler.go:265-277`: `if model.IsDecision() { normal } else if model.IsGroup() && groupLane(model.ID)==decision { group branch } else { 400 decision_misroute }`.
  2. **Member loop wraps steps 8-15** (`pkg/decision/handler.go:299-505`): per-member credential resolution, adapter lookup, BuildUpstream, upstream call, InspectResponse. The write (steps 16, `:510-548`) fires only for the winning attempt.
  3. **Per-member credential resolution by ROW IDENTITY**, not claims-first — mirror `pkg/store/database/store.go:2552-2567` and `pkg/proxy/handler_groups.go:544-550` (`directModelGetter`). The decision lane's credential key is `(memberID, memberID)` (preserving the existing stable-binding shape, `cmd/main.go:327`).
  4. **Tried-set + advance cap** = `models.MaxGroupMembers` (16) (`pkg/models/group.go:25`), mirroring `arc.groupTriedMembers` (`pkg/proxy/handler_anthropic.go:1804`) and the offer walk (`pkg/proxy/handler_groups.go:457-493`).
  5. **Pre-commit feasibility**: the decision lane has no `payloadCommitted` guard (`pkg/proxy/handler_anthropic.go:664-672`) because it never sets headers before the write (`pkg/decision/handler.go:508-548`); the loop owns steps 8-15 and the writer is untouched until the winner.
  6. **Per-member conversation key = ""** — `RewriteGroupEntries(..., "")` and `ExcludeGroupMember(..., "", ...)` ⇒ fresh weighted pick, no binding (`pkg/models/group_rewrite.go:88-90`). This is the no-sticky consequence (D-6).
- Mirrors for each capability (from `research-decision.md §8`):
  - (a) pre-commit multi-member attempts → mirror the Anthropic pre-commit re-entry gate (`pkg/proxy/handler_anthropic.go:664-672`) — simpler in the decision lane (no header guard).
  - (b) demote-not-filter ordering → REUSE the four-phase sweep (`store_group_rewrite.go:88-245`), never re-implement.
  - (c) win-clear → mirror `clearWinnerUpstreamSkip` (`pkg/proxy/handler_helpers.go:867-886`).
  - (d) request-scoped tried-set → mirror `arc.groupTriedMembers` (`pkg/proxy/handler_anthropic.go:1804`) + the offer walk (`pkg/proxy/handler_groups.go:457-493`).

**Rejected alternative + why:** **extract the group machinery into a shared package (e.g. `pkg/grouplane`) imported by both `pkg/proxy` and `pkg/decision`.** Rejected because:
1. It would require moving/refactoring the chat lane's proven code — the v193 properties live in `pkg/store/database` + `pkg/proxy` and are pinned by proxy-level tests; a refactor risks byte-behaviour change in the text lane, which D-7 fences as untouched.
2. The shared surface would have to be parameterised by chat-vs-decision semantics (streaming, misroute envelopes, metering) — the two lanes differ on exactly those axes.
3. The decision lane's need is small (an ordered member plan + advance + win-clear) and the manager already exposes it; a shared package would be a large abstraction for a small reuse.
4. The import guards are a *deliberate* architecture boundary (Decision Models §3.5) — the point of the guards is that the decision lane must not inherit proxy's `ConfigSnapshot`/`Handler`/`Bus`-telemetry machinery.

**Cost / risk:** the loop is the bulk of the phase-1 work (per `research-decision.md §8a` — a group branch between resolution and credential resolution, a new optional field+setter on `Handler`, the member attempt loop wrapping steps 8-15, deferred write, per-member credential resolution, tried-set + cap). The risk is the deferred-write discipline: a single premature `w.WriteHeader` (step 16) leaks a partial response. Mitigation: the loop owns `:510-548` and the commit point is guarded by a "winner resolved" flag; the existing `openAIErrorWithCode` helper (`:709-714`) is used for pre-commit errors.

**Verification:** (1) a single-member decision group returns the member's typed answer byte-identically to today's single-model path (`pkg/decision` round-trip tests); (2) a two-member group with member A failing (transport error) serves member B and returns B's answer; (3) the response writer is never written before the winner is chosen (a test that fails the first member and asserts zero writes before the second attempt); (4) byte-identity: a non-group decision request is byte-identical to the pre-change path (cf. `pkg/proxy/handler_groups_test.go:400` `TestHandlerGroups_Am26_NonGroup_ByteIdentity`).

---

## D-4 — Demotion + win-clear on the decision lane

> **⚠️ SUPERSEDED IN PART (2026-11-16) by ADR-5 and ADR-6.** The demote-as-reordering framing below is
> superseded: under the user's authoritative semantics a mark **suppresses an ATTEMPT, never a PICK or
> a CHAIN** — the head is policy-determined and stays the head. Win-clear, classification, the
> `pkg/credclass` extraction and the decorator-forward requirement below **stand unchanged**. Read this
> section for the win-clear/classification design and ADR-5/ADR-6 for the ordering semantics.

**Decision:** Demotion + win-clear ARE in scope. `Outcome` (`pkg/decision/converter.go:28-31`) is extended to carry the upstream HTTP status (additive `UpstreamStatus int`); the ADAPTER captures the raw upstream status, and the LANE classifies and writes marks. Classification responsibility falls on the LANE, mirroring `classifyCredFailure` (`pkg/proxy/cred_failover_class.go:93`) — but the classification VOCABULARY is extracted into a new **leaf package `pkg/credclass`** (no internal imports) because `pkg/decision` cannot import `pkg/providers` (verified: `pkg/providers/openai.go:31` imports `pkg/proxy/translator`, so `pkg/providers` is transitively under `pkg/proxy` and is banned by `pkg/decision/handler_test.go:90`). Win-clear routes through the decorator forward (`pkg/modelscache/models.go:1079-1085`) → `pkg/store/database/store_group_rewrite.go:439-444` → `pkg/grouplb/engine.go:1274-1287`) and uses the WINNER's identity (`member.ID` + `cred.CredentialID` + `cred.InternalModel`, in scope at `pkg/decision/handler.go:299` and `:361`), never the picked head.

**Rationale (file:line):**
- The adapters collapse every upstream error envelope to 502 (`pkg/decision/openaidecision.go:169-179`, `pkg/decision/typesafe.go:426-429`), so the upstream status/body available at `pkg/decision/handler.go:492` is discarded by the adapter contract. `Outcome.RelayStatus` is 502 for every error — unusable as a classifier (research `research-decision.md §6.3`).
- The lane must classify before it can demote: the v193 doctrine is "Marks fire on LIMIT-CLASS failures only" (blueprint, Credential Failure Classification & Cooldown Policy; `pkg/proxy/race_coordinator.go:2106-2107` gates `Mark` on `classifyCredFailure != none`).
- `pkg/providers.IsRateLimitError`/`IsQuotaError` (`pkg/providers/interface.go:225`, `:348`) carry the vocabulary (`quotaTypeVocabulary`/`quotaCodeVocabulary`/`quotaMessageVocabulary` at `:266-286`) but are `*ProviderError`-shaped (errors.As) and live in a package `pkg/decision` cannot import.
- The extraction is behaviour-preserving: the leaf owns the vocabulary over `(status, errType, errCode, message)`; `pkg/providers`' classifiers delegate to it (extracting the 4 fields from `*ProviderError`); `pkg/decision` extracts the same 4 from the upstream response. One vocabulary, two adapters. The text-lane classifier tests (`pkg/providers/interface_quota_test.go`, `pkg/providers/ratelimit_classifier_test.go`) are the regression pins.
- Win-clear identity discipline is mandatory: `pkg/proxy/handler.go:905-920` (comment `:906-915`) — clear on the WINNER's identity, never the picked head (on a chain-entry win the picked head FAILED and its cooldown must survive). The decision lane's winner identity is `member.ID` + `cred.CredentialID` + `cred.InternalModel` — all in scope at `pkg/decision/handler.go:299` (credential resolution) and `:361` (BuildUpstream, which consumes `cred`).
- The decorator forward is mandatory in production (the trap): `pkg/modelscache/models.go:1063-1085` documents the silent-failure: production `conf.ModelsConfig` is `*CachedModelsConfig` (`cmd/main.go:182-188`), and without the forward the handler's dynamic assertion fails silently. `ClearGroupMemberCooldown` is the precedent forward (`pkg/modelscache/models.go:1079-1085`) — the optional-interface shape keeps the ~11 structural fakes valid.
- Placement: after the relay write and before/with metering — between `pkg/decision/handler.go:548` and `:554`.

**Rejected alternative + why:**
- **No demotion in phase 1** — rejected: a decision group whose member A is quota-exhausted would still pick A first on every request, fail, then fall back to B — every request pays A's failure latency. That is materially worse than the text lane and does not meet the commission's "same resilience" goal.
- **Duplicating the vocabulary in `pkg/decision`** — rejected: two vocabularies drift; the blueprint's provider-vocabulary POK (`decision-provider-vocabulary`) explicitly warns about drift (stale comment at `pkg/models/credential.go:55-58` is the documented precedent).
- **Importing `pkg/providers` directly** — impossible (verified transitive-closure guard, `pkg/decision/handler_test.go:90`); `pkg/providers/openai.go:31` imports `pkg/proxy/translator`.
- **Win-clear keyed on the picked head** — rejected: the chain-entry case (head failed, replacement won) would clear the WRONG member's cooldown; the incident this fix addresses (`pkg/proxy/handler.go:905-920` comment) is precisely this.
- **Win-clear without the decorator forward** — rejected: silent no-op in production (the trap, `pkg/modelscache/models.go:1063-1085`).

**Cost / risk:** the extraction (new leaf package + delegation in `pkg/providers`) is the single text-lane-adjacent source touch this design permits (D-7); it is behaviour-preserving and pinned by `pkg/providers/interface_quota_test.go` + `pkg/providers/ratelimit_classifier_test.go`. The risk is a missed vocabulary constant during the move — mitigated by a shared test table in the leaf package that both lanes run. The skip cache itself is NOT currently attached to the decision handler (its config seam exposes only `GetDecisionTimeout`, `pkg/decision/handler.go:116-118`) — phase 1 must extend the seam (D-8) to reach `*skipcache.Cache` (`pkg/proxy/handler.go:90`, `:166`) and `*models.CachedModelsConfig` (`cmd/main.go:182-188`).

**Verification:** (1) a quota-failed member (402 / quota vocabulary) is marked and the next request picks the other member first; (2) a plain 429 marks rate-limit with the upstream Retry-After (unbounded, `pkg/credentiallb/engine.go`); (3) an upstream 401/403 does NOT mark (terminal); (4) after a successful relay, the winner's cooldown row is gone (clone `TestWinClear_ReachesEngineThroughCachedConfig` `pkg/proxy/win_clear_winner_identity_test.go:120`); (5) the decorator-through test binds the CACHED config and asserts the engine row is cleared (failure text "ClearGroupMemberCooldown is a no-op through the decorator" `pkg/proxy/win_clear_winner_identity_test.go:191`); (6) the leaf vocabulary is pinned by the moved tests.

---

## D-5 — Exhaustion error shape

**Decision:** **502 with a distinct machine code `decision_all_members_failed`**, body = the house OpenAI envelope `{"error":{"type":"server_error","code":"decision_all_members_failed","message":"all decision group members failed (N attempted)"}}` via `openAIErrorWithCode` (`pkg/decision/handler.go:709-714`). The last upstream body is NOT relayed verbatim (the failure is aggregate, not one member's). No metering row (failure path writes none, `pkg/decision/handler.go:606-608`). One decision event with outcome `all_members_failed` (mirroring the existing event vocabulary `pkg/decision/handler.go:253-277`).

**Rationale (file:line):**
- 502 is the lane's universal upstream-failure status: every adapter promotes to 502 (`pkg/decision/openaidecision.go:169-179`, `pkg/decision/typesafe.go:426-429`) and the promoted-error relay writes 502 (`pkg/decision/handler.go:534-543`). A distinct code keeps clients able to distinguish "all members exhausted" from "one upstream 502" without overloading a status.
- 503 is reserved for config-store unavailability on the chat lane (`pkg/proxy/handler.go:483-489`, `code=config_store_unavailable`) — reusing it would overload a different meaning.
- The Anthropic-lane precedent for exhaustion is 502 "All models failed after retries" (`pkg/proxy/handler_anthropic.go:698-714`) — consistent status, but that envelope carries no code field; the decision lane adds one (`decision_all_members_failed`) for machine distinguishability.
- The last upstream body is not relayed because the failure is aggregate: relaying member N's body would mislead a client into thinking member N's upstream error is the answer. Per-member detail goes to the log and the event, not the wire.
- No metering row: the existing failure paths write none (`pkg/decision/handler.go:606-608`); an exhausted group produced no successful usage.

**Rejected alternative + why:**
- **503** — rejected: reserved for config-store unavailability (`pkg/proxy/handler.go:483-489`); overloading it conflates "the store is down" with "every member failed".
- **Relaying the last upstream body verbatim (Anthropic-style)** — rejected: the failure is aggregate, not a single upstream's; the last body is member-specific and would mislead. (If the user prefers upstream-verbatim, that is Q2 with a recommendation.)
- **A 200 with an error field** — rejected: violates the lane's status contract (success = upstream status, `pkg/decision/handler.go:510-528`).

**Cost / risk:** one new code string (`decision_all_members_failed`), one new event outcome, one new log line. Low cost. *(SUPERSEDED 2026-11-16 — ADR-5 / ADR-11 / ADR-13: exhaustion is **class-conditional** ("genuinely failed" per the AM-4 failure-class policy — a 5xx'd member HAS failed), the all-marked case is the **policy-head honest attempt** (NOT the sweep's soonest-expiring rule), and the sweep's ordering is no longer reused (policy-only pick). The 502 shape, no-usage-row, and single-event requirements above STAND.)*

**Verification:** (1) a group whose sole member fails with a transport error returns 502 + `code=decision_all_members_failed` and writes no usage row; (2) a two-member group with member A (transport error) then member B (success) returns B's answer 200; (3) the event stream contains exactly one `all_members_failed` outcome per exhausted request; (4) the log contains the member count attempted.

---

## D-6 — Sticky = NO

**Decision:** **Sticky last-success ordering is OUT of scope for the decision lane.** No conversation key exists on this lane; the member conversation key is empty (`""`) ⇒ the grouplb engine performs a fresh weighted pick per request with NO binding stored (`pkg/models/group_rewrite.go:88-90`). The credential layer keeps its existing stable per-model binding with key `(memberID, memberID)` (`cmd/main.go:327`); the MEMBER layer has no affinity. Do not invent affinity machinery.

**Rationale (file:line):**
- Zero hits for `sticky`, `stickyorder`, `conversationKey`, `binding`, `affinity` under `pkg/decision` except two comments describing the credential key as `(modelID, modelID)` (`pkg/decision/handler.go:21`, `:296`).
- The resolver signature cannot carry a key: `credentialResolver func(modelID string)` (`pkg/decision/handler.go:738`); the wiring passes the model id twice (`cmd/main.go:327`).
- The sticky engine is constructed at `cmd/main.go:144` and injected ONLY into `proxy.NewHandler` (`cmd/main.go:282`); `pkg/proxy` applies it via `applyStickyOrder` (`pkg/proxy/handler_groups.go:134`, call sites `pkg/proxy/handler.go:877`, `pkg/proxy/handler_anthropic.go:499`).
- The request body gives no natural key: `DecisionRequest = {model, input, questions, safety_identifier}` (`pkg/decision/types.go:130-135`) — `input` may be a bare string; no conversation/message list is required.
- The commission constraint (user-stated) rules sticky out; the engine's documented empty-key semantics (`pkg/models/group_rewrite.go:88-90`) is exactly the intended behaviour.
- Consequence: a decision group whose members are openai + openrouter alternates per request by weight; there is no per-conversation pinning. This is the only available semantics and is acceptable (decision requests are stateless typed calls, not conversations).

**Rejected alternative + why:** **inventing a conversation key from `safety_identifier` or a hash of `input`.** Rejected because: (1) `safety_identifier` is optional and not present in most requests; (2) hashing `input` to key affinity would be a new, untested affinity scheme with no precedent in the codebase and no validation; (3) the commission explicitly rules sticky out; (4) the credential layer's stable per-model binding already provides credential stickiness where it matters.

**Cost / risk:** zero implementation cost — the empty key is the default path. The risk is only expectation-setting: operators must understand decision groups do NOT pin a member per conversation (unlike chat groups). Document in the admin UI and the FE empty state.

**Verification:** (1) two consecutive identical requests to a two-member decision group may pick different members (weighted) — a test asserting NO binding is stored (the engine's binding map stays empty for an empty key); (2) the credential layer still uses `(memberID, memberID)` (the existing `ResolveInternalConfigWithAffinity` path, unchanged).

---

## D-7 — Scope fence

**Decision:** The following are OUT of scope and must not be touched by phase 1 or phase 2:

1. **Text lane behaviour is byte-untouched.** No change to `pkg/proxy/handler.go`, `pkg/proxy/handler_anthropic.go`, `pkg/proxy/handler_groups.go`, `pkg/proxy/race_coordinator.go`, or their tests' expectations. The chat path through the manager passes the chat expected kind ⇒ the parameterized predicate behaves identically to today for chat groups (the only permitted manager change is the kind parameter, which is a no-op for chat).
2. **v191-v193 fixes untouched.** The anonymous-`/v1/messages` auth fix (401/403 parity), the agentic hidden-member fix (ordering-never-filtering + win-clear, v193 @ b11d7c8), the v193 skip-cache law (marks are ordering inputs, never candidacy filters) — all untouched.
3. **No new provider.** The decision provider vocabulary `{openai, typesafe, openrouter}` is unchanged at all five surfaces: FE constant `pkg/ui/frontend/src/components/config/DecisionModelForm.tsx:29`, BE gate `pkg/models/config.go:293-300`, POST `pkg/ui/server.go:1165-1178` (`:1171`), PUT `pkg/ui/server.go:1631-1643` (`:1636`), registry `pkg/decision/registry.go:49-53`. The credential vocabulary (`pkg/models/credential.go:15-60`) is unchanged. A group's members are validated only when the MEMBER row is written (as a decision model); the group's own write path passes `""` as `internalProvider` (`pkg/store/database/store.go:2113`) and `validateGroupRules` ignores it (`pkg/models/group.go:57-61`). **The provider vocabulary surface is provably unchanged by this design.**
4. **Flat membership stays flat.** G5 group-as-member remains forbidden (`pkg/models/group.go:176-179`) in BOTH lanes.
5. **Cap 16 stays.** `models.MaxGroupMembers = 16` (`pkg/models/group.go:25`); the FE mirror `GroupMemberEditor.tsx:6` stays in lock-step.
6. **No schema change / no migration.** (D-2) — the lane is inferred, not persisted. `kind TEXT` is already unconstrained (030) and `members_json` already exists (031).
7. **The ONE permitted text-lane-adjacent source touch** is the vocabulary delegation in `pkg/providers` (D-4) — behaviour-preserving, pinned by `pkg/providers/interface_quota_test.go` and `pkg/providers/ratelimit_classifier_test.go`. No other text-lane source is touched.
8. **No new models-manager method beyond the lane query (D-8).** The sweep, exclusion, win-clear and decorator forwards are REUSED.

**Rejected alternative + why:** each item above rejects a scope-creep variant (e.g. "while we're here, add a new provider", "relax G5 for decision groups", "bump the cap for credential-pool-like groups", "add a lane column for clarity"). Rejected because: each would multiply the blast radius, re-open settled commissions, and none is required by the commission goal.

**Cost / risk:** the fence is what makes the commission shippable in two phases. The risk is a phase plan that quietly widens scope — the contracts below are the acceptance checklist.

**Verification:** a diff review: phase 1 touches only `pkg/decision/*`, `pkg/models/*` (group.go kind rules + group_rewrite.go if the lane is added to the result), `pkg/store/database/store_group_rewrite.go` (parameterized predicate), `pkg/modelscache/models.go` (one optional forward), `pkg/credclass` (new leaf), `pkg/providers` (delegation only), `cmd/main.go` (wiring), and the new FE files in phase 2. Anything else is a fence violation.

---

## D-8 — What a new models-manager method costs

**Decision:** Phase 1 adds **exactly ONE new models-manager method**, plus its mandatory decorator forward + decorator-through test. Everything else reuses existing seams.

**Method expected in phase 1:**
1. **`GroupLane(groupID string) (models.GroupLane, bool)`** — a side-effect-free lane query on `*database.ModelsManager`. Returns `models.GroupLaneChat` / `models.GroupLaneDecision` (new typed constants in `pkg/models`, no migration) or `ok=false` (group not found, mixed member kinds, or no resolvable members). Implemented by computing unanimity over the group's member rows (each resolved via `GetModelNative`, claims bypassed — `pkg/store/database/store.go:2552-2567`). **Cost:** decorator forward + decorator-through test (mandatory pair, per the trap). **Placement:** OPTIONAL interface (NOT on `models.GroupRewriter`, mirroring `ClearGroupMemberCooldown` at `pkg/modelscache/models.go:1079-1085`) so the ~11 structural fakes stay valid; the decorator forward is mandatory and the decorator-through test proves it; the handler logs loudly when the seam is absent (fail-closed to 400 `decision_misroute`).

**Consumers (four):** (1) decision handler entry `pkg/decision/handler.go:265`; (2) chat misroute guard `pkg/proxy/handler.go:576`; (3) Anthropic misroute guard `pkg/proxy/handler_anthropic.go:272`; (4) `/v1/models` filter `pkg/proxy/handler.go:349-352`. Each consumes it through the optional-interface assertion pattern (`pkg/proxy/handler_helpers.go:879-885`).

**Decorator forward + decorator-through test (the trap):** the forward mirrors `ClearGroupMemberCooldown` (`pkg/modelscache/models.go:1079-1085`); the decorator-through test clones `TestWinClear_ReachesEngineThroughCachedConfig` (`pkg/proxy/win_clear_winner_identity_test.go:120`) — bind the CACHED config, assert the effect reaches the engine; failure text must name the trap ("GroupLane is a no-op through the decorator").

**Rejected alternative + why:**
- **Adding `GroupLane` to `models.GroupRewriter`** — rejected: the ~11 structural test fakes (`pkg/modelscache/models.go:1066-1071`) would all need updating and the JSON backend would break. The optional-interface shape is the house pattern for exactly this.
- **A new manager method for the ordered member plan** — rejected: `RewriteGroupEntries` (`pkg/store/database/store_group_rewrite.go:49-86`) already returns `GroupID`/`MemberID`/`FailedGroupID`/`MemberPool`; reusing it avoids a second forward + second trap.
- **Lane inference in the decision handler via `GetModelNative`** — considered, but rejected: it would duplicate the member-load pass the manager performs inside the sweep, and it would leave the chat/Anthropic/`/v1/models` consumers without a seam (they cannot call `GetModelNative` cleanly for a group they haven't resolved). One manager method serves all four consumers.

**Cost / risk:** one new method + one optional forward + one decorator-through test. The risk is the silent no-op if the forward is missed (the trap) — mitigated by the decorator-through test being a merge gate.

**Verification:** the decorator-through test passes against the cached config; the same test with the forward removed fails with the trap message; the four consumers all reject/serve correctly (chat guard: a decision group posted to `/v1/chat/completions` returns 400 `decision_misroute`; Anthropic: 400 `decision_misroute`; `/v1/models`: the decision group is absent from the list; decision handler: enters the group branch).

---

## Blast radius

`Site | File:line | Rule today | Rule after | Test that pins it`

| # | Site | File:line | Rule today | Rule after | Test that pins it |
|---|------|-----------|------------|------------|-------------------|
| 1 | G3 member kind (canonical) | `pkg/models/group.go:180-184` | member must be chat-kind | member kind must equal the group's lane (chat OR decision) | `pkg/models/group_rules_test.go:105` (rewritten) + new lane cases |
| 2 | G8 shadow-target kind | `pkg/models/group.go:196-198` | shadow target must be chat-kind | shadow target kind must equal the group's lane | `pkg/models/group_rules_test.go` + `pkg/ui/kind_rules_parity_test.go:520-620` |
| 3 | Homogeneity (NEW rule) | `pkg/models/group.go` (new, after G2) | (none) | all members share one normalized kind | new test (write-time rejection) |
| 4 | Request-time predicate | `pkg/store/database/store_group_rewrite.go:291-301` | chat-only (`""`/`KindChat`) | kind-parameterized; `""` accepted only as chat | `pkg/store/database/store_group_skipcache_test.go:62`, `store_group_test.go:592` (meaning change) |
| 5 | POST member kind mirror | `pkg/ui/server.go:941-944` | chat-only | lane-consistent | `pkg/ui/kind_rules_parity_test.go:520-620` |
| 6 | PUT member kind mirror | `pkg/ui/server.go:1443-1444` | chat-only | lane-consistent | `pkg/ui/kind_rules_parity_test.go:520-620` |
| 7 | POST shadow kind mirror | `pkg/ui/server.go:962-964` | chat-only | lane-consistent | `pkg/ui/kind_rules_parity_test.go:520-620` |
| 8 | PUT shadow kind mirror | `pkg/ui/server.go:1464` | chat-only | lane-consistent | `pkg/ui/kind_rules_parity_test.go:520-620` |
| 9 | Decision lane entry | `pkg/decision/handler.go:265-277` | `!IsDecision()` → 400 `decision_misroute` | decision model OR decision group; else 400 | `pkg/decision/handler_test.go:692`, `pkg/decision/proxy_handler_decision_models_test.go:156`,`:188` |
| 10 | Credential resolution | `pkg/decision/handler.go:299-310` | one credential for the requested id | per-member credential by row identity | `pkg/decision/handler_test.go:716`, `:730` |
| 11 | Upstream call | `pkg/decision/handler.go:430-433` | single upstream call | member loop (max 16) | new group-failover tests |
| 12 | Commit point | `pkg/decision/handler.go:517`, `:541`, `:543` | single write | write only the winner | new test (zero writes before winner) |
| 13 | Outcome struct | `pkg/decision/converter.go:28-31` | `{RelayStatus, Reason}` | + `UpstreamStatus int` (additive) | `pkg/decision` adapter unit tests |
| 14 | Metering | `pkg/decision/handler.go:604-641` | success-only, `modelID` (client-facing) | success-only, member id (served row) | `pkg/decision/handler_test.go:1593-1688` |
| 15 | Decision handler config seam | `pkg/decision/handler.go:116-118` | `GetDecisionTimeout()` only | + skip cache + models config | new wiring tests |
| 16 | Manager sweep | `pkg/store/database/store_group_rewrite.go:88-245` | chat expected kind | carries the inferred lane into the predicate | `pkg/store/database/store_group_skipcache_test.go:62`, `store_group_test.go:592` (meaning change) |
| 17 | ExcludeGroupMember | `pkg/store/database/store_group_rewrite.go:406-431` | chat expected kind | lane-aware (internal) | `pkg/store/database/store_group_test.go:637` |
| 18 | ClearGroupMemberCooldown | `pkg/store/database/store_group_rewrite.go:439-444` | (unchanged) | (unchanged — reused) | `pkg/proxy/win_clear_winner_identity_test.go:120`,`:218` (clone) |
| 19 | Decorator forwards | `pkg/modelscache/models.go:1043-1048`, `:1056-1061`, `:1079-1085` | (existing) | + one optional forward for `GroupLane` | new decorator-through test (clone of `:120`) |
| 20 | Chat misroute guard | `pkg/proxy/handler.go:576-582` | rejects decision MODEL | also rejects decision GROUP | `pkg/proxy/handler.go:576` tests (new case) |
| 21 | Anthropic misroute guard | `pkg/proxy/handler_anthropic.go:272-279` | rejects decision MODEL | also rejects decision GROUP | `pkg/proxy/handler_anthropic_misroute_test.go` (new case) |
| 22 | `/v1/models` filter | `pkg/proxy/handler.go:349-352` | excludes image-gen + decision MODELS | also excludes decision-lane GROUPS | `pkg/proxy/handle_models_imggen_test.go:129-164` + sibling |
| 23 | Provider vocabulary (group) | `pkg/models/group.go:57-61` | skips provider gate (`_ = internalProvider`) | unchanged (group carries no credentials) | `pkg/models/group_rules_test.go` |
| 24 | Provider vocabulary (decision) | `pkg/models/config.go:293-300` | `{openai, typesafe, openrouter}` | unchanged | `pkg/ui/kind_rules_parity_test.go:312-317` |
| 25 | Provider vocabulary (POST) | `pkg/ui/server.go:1165-1178` (`:1171`) | same set | unchanged | `pkg/ui/kind_rules_parity_test.go` |
| 26 | Provider vocabulary (PUT) | `pkg/ui/server.go:1631-1643` (`:1636`) | same set | unchanged | `pkg/ui/kind_rules_parity_test.go` |
| 27 | Provider vocabulary (registry) | `pkg/decision/registry.go:49-53` | openai/typesafe/openrouter | unchanged | `pkg/decision/openrouter_decision_test.go` |
| 28 | Credential vocabulary | `pkg/models/credential.go:15-60` | 15 providers | unchanged | `pkg/models/credential.go` tests |
| 29 | KindGroup doc | `pkg/models/config.go:117-132` | "served by the normal chat pipeline" | update wording (docs only) | (none — doc) |
| 30 | GroupMember doc | `pkg/models/group.go:111-114` | "chat-kind models" | "lane-consistent models" | (none — doc) |
| 31 | FE types doc | `pkg/ui/frontend/src/types.ts:168-186` | `model_id` references chat-kind | lane-consistent | `GroupMemberEditor` tests |
| 32 | FE member editor filter | `pkg/ui/frontend/src/components/config/GroupMemberEditor.tsx:40` | chat-only filter | parameterized by lane | `GroupMemberEditor.test.tsx:55-71` |
| 33 | FE group form prop | `pkg/ui/frontend/src/components/config/GroupModelForm.tsx:49-53` | `chatModels` (chat only) | lane-parameterized | `GroupModelForm.test.tsx` |
| 34 | FE settings filter | `pkg/ui/frontend/src/components/SettingsPage.tsx:293-295` | `chatKindModels` | + `decisionKindModels` | `SettingsPage.tabs.test.tsx` |
| 35 | FE chat tab filter | `pkg/ui/frontend/src/components/config/ModelsTab.tsx:363` | chat filter (unchanged) | unchanged | `ModelsTab.chatFilter.test.tsx` |
| 36 | FE group tab | `pkg/ui/frontend/src/components/config/GroupModelsTab.tsx:303` | `chatModels={chatModels}` | lane-specific feed | `GroupModelsTab.test.tsx` |
| 37 | FE group G8 mirror | `pkg/ui/frontend/src/components/config/GroupModelForm.tsx:189-200` | chat-only shadow | lane-consistent | `GroupModelForm.test.tsx` |
| 38 | FE Decision form provider | `pkg/ui/frontend/src/components/config/DecisionModelForm.tsx:29` | `{openai, typesafe, openrouter}` | unchanged | `DecisionModelForm.test.tsx:271-286` |
| 39 | FE tab bar | `pkg/ui/frontend/src/components/SettingsPage.tsx:66`, `:380-521` | 11 top-level tabs | decision_models tab becomes a container (11 stays) | `SettingsPage.tabs.test.tsx:285`,`:287` |
| 40 | FE vertical nav | (none — new) | no precedent (zero `role="tablist"`) | new vertical left-nav `[models] [group]` inside the container | new tests |
| 41 | Migrations | `migrations/sqlite/030…:29`, `031…:39-40` | unconstrained kind, members on `models` | unchanged (no new migration) | (none) |
| 42 | v193 skip-cache law | `pkg/skipcache/skipcache.go:13-26` | ordering, never filtering | unchanged | `pkg/store/database/store_group_skipcache_test.go` |

**Blast-radius site count: 42 sites.**

---

## Contracts the phase plans must honour

Numbered, testable. Phase 1 (backend) and phase 2 (FE) plans are written against this list; each item is an acceptance criterion.

**Backend (phase 1):**

1. A group's members are homogeneous in normalized kind (chat-or-legacy-`""` | decision). A mixed-kind write is rejected with a dedicated error message.
2. The lane is inferred at request time by unanimity over the CURRENT member rows (not trusted from write time). Empty/mixed ⇒ the group is unservable.
3. `isServableGroupMemberBase` is kind-parameterized; `""` is accepted only as chat (legacy), never as decision.
4. The shadow target's kind must equal the group's lane.
5. The decision lane's group branch is entered only for a group whose inferred lane == decision.
6. The member loop writes to the client exactly once, at the winner (`pkg/decision/handler.go:517`/`:541`/`:543`); every attempt before the winner leaves the writer untouched.
7. The member attempt order comes from the manager's four-phase sweep (reused), never re-implemented in `pkg/decision`.
8. Tried-set + advance cap = `models.MaxGroupMembers` (16).
9. Per-member credential resolution by row identity (claims bypassed); credential key = `(memberID, memberID)`.
10. The group member's conversation key is `""` (no affinity).
11. ~~Marks fire only on limit-class failures; all-marked ⇒ one honest attempt against the soonest-expiring mark.~~ **SUPERSEDED 2026-11-16 by ADR-5/R2:** marks fire only on limit-class failures (unchanged), but a mark **suppresses an attempt** (zero-cost skip) and never reorders the pick; the all-marked case is **OQ-1** in ADR-10 (recommended: suppression never removes the last attempt — the policy head is attempted).
12. Win-clear uses the WINNER's identity (member id + credential id + internal model), never the picked head, and routes through the decorator forward.
13. Exhaustion ⇒ 502 + code `decision_all_members_failed`; no metering row; one event.
14. Metering: success-only; the served member's row is metered (mirroring the text lane's `RouteModel` stamp); the client-facing group id is recorded in the decision event.
15. `/v1/models` excludes decision-lane groups.
16. Chat misroute guards (OpenAI + Anthropic) reject a decision GROUP with 400 `decision_misroute`.
17. The text lane's behaviour through the manager is unchanged (chat expected kind ⇒ identical predicate behaviour).
18. No new provider: the decision provider vocabulary surface (5 sites) is byte-unchanged.
19. Flat membership: group-as-member still forbidden (G5) in BOTH lanes.
20. Cap 16 unchanged.
21. The vocabulary is extracted into a new leaf package `pkg/credclass` (no internal imports); `pkg/providers` delegates; `pkg/decision` imports the leaf; the existing classifier tests pin the delegation.
22. The optional `GroupLane` forward + decorator-through test are merge gates (the trap).

**Frontend (phase 2):**

23. The "Decision Models" tab becomes a container holding a vertical left-nav `[models] [group]`; the top-level tab count stays 11 (the existing `decision_models` button is the container; `group_models` stays for text groups).
24. The vertical nav reuses the top bar's active/inactive class pair translated to a left border; it does NOT use `flex overflow-x-auto` (so it does not match the `SettingsPage.tabs.test.tsx` selector `div.flex.overflow-x-auto > button`).
25. The vertical nav is class + conditional-content-branch (no ARIA tab roles), consistent with the house convention of zero ARIA tab usage — but every nav button carries `data-testid` and `aria-label`.
26. The decision group form mirrors the group form but is fed by a decision-models list (not the chat list); `GroupMemberEditor` is parameterized by lane (one component, two filtered feeds), not duplicated.
27. The FE member picker's lane filter and the G8 client-side mirror are lane-aware.
28. The tsc baseline (19 errors / 9 files) is not increased; any touched baseline file keeps its per-file count; new files produce zero errors.
29. FE source changes carry their regenerated `pkg/ui/static/` assets in the same commit (embed discipline); zero-mod proof = sha256sum byte-identical.
30. The `SettingsPage.tabs.test.tsx` assertions (`tabs.length === 11`, `groupIdx === 10`) stay green.

---

## Open questions

Stable numbering, Q1..Qn. Each states a recommendation and what evidence would settle it. Items the approver/user must decide are flagged.

- **Q1 (user) — Should a lane field be persisted on the group row after all?** Recommendation: NO for v1 (inference is sufficient and avoids a migration). Revisit if operational debugging of mixed/retyped groups becomes painful. Evidence that would settle it: a real incident where a retyped member caused an unservable group and the inference was insufficient to diagnose it. If yes, add `lane TEXT` to `models` (migration 032) and make it the source of truth, with inference as the fallback for legacy rows.
- **Q2 (user) — Should the exhaustion response relay the last upstream body verbatim (Anthropic-style) instead of the house envelope?** Recommendation: NO (the failure is aggregate; relaying member N's body misleads). Evidence: client-side error handling that expects upstream-verbatim bodies. If yes, change D-5 to relay the last member's body and drop the distinct code.
- **Q3 (user) — Should kind retyping be blocked at the API level? — ✅ RULED 2026-11-16: YES, same release; see ADR-1 (kind-change path, names group + member).** Recommendation: YES — `UpdateModel` should reject a kind change (immutability), because the retyped-member hazard is the main operational risk of inferred lanes. Evidence: the verified absence of any kind-change guard today (`pkg/store/database/store.go:1699`, `validateBeforeWrite` `:2085`). This is a small, separate, low-risk change; it is NOT in this commission's scope unless the approver folds it in.
- **Q4 (user) — Should a decision group appear in `/v1/models` at all?** Recommendation: NO (consistent with the house rule that decision models "must never appear in the OpenAI-compatible chat pickers", `pkg/proxy/handler.go:349-352`). Evidence: any client that expects to discover decision groups via `/v1/models`. If yes, the filter becomes lane-aware-listing (decision groups listed only when `?kind=decision` is passed), which is a larger change.
- **Q5 (user) — Vertical nav ARIA: should the new nav introduce `role="tablist"`/`role="tab"`/`aria-selected`/`role="tabpanel"`, or stay class-only? — ✅ RESOLVED 2026-11-16 (planner): class-only, PLUS `aria-current="true"` on the active item only (additive; no role, no roving tabindex, no keyboard change; recorded in `phase2-plan.md §Planner adjudications` E1).** Recommendation: stay class-only (consistent with the whole FE, which has zero ARIA tab usage — `research-fe-precedent.md §A3`). Evidence: an accessibility audit. If yes, it becomes the first ARIA-tab pattern in the codebase and must be applied consistently (a larger, separate commission).
- **Q6 (user) — Should the decision group's empty state / FE copy describe "no per-conversation pinning" (D-6)?** Recommendation: YES — the empty state should state that members are picked by weight per request, with no conversation pinning. Evidence: user testing.
- **Q7 (approver) — Is the leaf-package extraction (`pkg/credclass`) acceptable, given it touches `pkg/providers` (delegation) even though D-7 fences the text lane? — ✅ RESOLVED 2026-11-16 (planner): APPROVED WITH CONSTRAINTS — pure move + delegation, zero signature/behaviour change, ships as Step 0 behind a hard gate (pre-existing classifier suites unchanged), escalate rather than edit behaviour.** Recommendation: YES, because the delegation is behaviour-preserving and pinned by the existing classifier tests; it is the only way to share the vocabulary without violating the transitive-closure guard. Evidence: `pkg/providers/interface_quota_test.go` + `pkg/providers/ratelimit_classifier_test.go` green after the move.
- **Q8 (approver) — Should the lane query be a separate method, or should `GroupRewriteResult` gain an additive `GroupLane` field instead (zero new manager methods)? — ✅ RESOLVED 2026-11-16 (planner): SEPARATE `GroupLane(groupID)` METHOD. The text lane shares `GroupRewriteResult` and repo tests use golden/byte-identity assertions; mutating that struct risks text-lane churn and violates the byte-untouched fence, while an additive method is invisible to the text lane and its decorator pair is already budgeted by D-8.** Recommendation: separate method (side-effect-free, serves four consumers cleanly). Evidence: a phase-1 prototype measuring whether the extra method adds meaningful complexity. If the approver prefers zero new methods, switch to the additive-field variant and re-derive the four consumers from the rewrite result.

---

## ADRs — leader rulings (2026-11-16)

Recorded by the planner on the leader's instruction. These **close** the corresponding entries in
`## Open questions`. The phase plans are not edited by the planner; where a ruling yields a new
acceptance criterion, the criterion is stated here **and** reported to the leader for carriage into
the plan.

### ADR-1 — Kind immutability for group members (closes Q3)

**Decision.** YES — block kind retyping, **in the same release**. A model that is a member of a group
must have an **immutable kind**; a retyping attempt is **rejected** with a clear, actionable error.

**Rejection layer (explicit).** The **kind-change path**, not the group-save path. The model-update
path refuses the write because the row is group-committed; group save/validation is not the fence.

**Why.** Multi-member groups already fail closed on a kind mismatch (D-2's homogeneity + request-time
unanimity ⇒ mixed ⇒ unservable). But a **single-member** group whose member is retyped would
**silently re-lane**: with no persisted lane field on the group row (D-2's deliberate cost), a
one-member group whose member became `chat` is indistinguishable from a native chat group. The server
cannot detect it from the row, so the fence has to live at the retyping site.

**Error contract.**
- Envelope: the existing `/fe/api/*` bare `{"error":"<string>"}` family — no new envelope.
- Content: the message must **name the group and the member**, e.g.
  `model <member-id>: kind cannot be changed while it is a member of group <group-id> (remove it from the group first)`.
- Status: a client error (4xx), consistent with the model-update path's existing validation rejections.

**Acceptance criteria (for carriage into `phase1-plan.md`).**
1. Retyping a model that is a member of **any** group is rejected; the stored `kind` is unchanged;
   group resolution is unchanged afterwards.
2. Retyping a model that is **not** a member of any group still succeeds — the guard is scoped to
   group members only, and must not become a blanket "kinds are immutable" change.
3. The rejection names **both** the group id and the member id (actionable, not generic).
4. Parity discipline: the guard is asserted at the canonical write path **and** at the API path
   (helper ≡ PUT), per the repo's multi-site rule-parity convention.
5. **Decorator note (implementation constraint, not a new decision):** if the membership lookup is
   implemented as a **new** manager method, D-8 / contract 14 applies — it needs a `modelscache`
   decorator forward **and** a decorator-through test. Prefer deriving membership from the already
   forwarded `GetModels` to avoid spending a second new-method budget.

**Residual after this ADR.** The single-member re-lane hole closes. The multi-member case was already
fail-closed. Nothing about D-2's no-migration stance changes.

### ADR-2 — Credential multiplicity on the decision lane (closes E-6) — ⚠️ FRAMING RETRACTED 2026-11-16 by ADR-6; evidence stands, decision superseded

> **⚠️ RETRACTION NOTICE (2026-11-16, user ruling).** This ADR's *framing* is retracted. Member-level
> fallback is **not optional** and **not a scope reduction**: per **R3 / ADR-6** it is the **ORDER OF
> OPERATIONS** (a member's own hedge/racing + own `fallback_chain` is consumed FIRST; advance only
> after). The evidence below remains factually correct and useful — it establishes what the lane does
> today — but the **decision** it records does not stand. Superseded by **ADR-6**.

**Leader ruling.** Member-level fallback is **ACCEPTED FOR THIS RELEASE, CONDITIONAL** on establishing
what the decision lane does with credentials **today**.

- If credential multiplicity **does not exist** on this lane, then member-level exhaustion **is**
  member × credential exhaustion in practice, and the commission's wording is satisfied with no
  extra work — this must be stated explicitly, not implied.
- If multiplicity **does exist**, member-level is a **genuine reduction** and the plan must implement
  member × credential exhaustion **before shipping**.

**Status — EVIDENCE GATE CLEARED (2026-11-16). Verdict: multiplicity is REPRESENTABLE but INERT on
this lane.** *(The decision that followed — "member-level exhaustion ACCEPTED" — is RETRACTED by
ADR-6: member-level is now the order of operations, and a member's own per-credential resilience must
be consumed before advancing. The facts below are unchanged and still true.)*

*Evidence.*
- **Attachment is 1:N.** `ModelConfig.Credentials []CredentialRef` — `pkg/models/config.go:357`
  (doc `:350-356`); ref shape `{CredentialID, Weight, Position}` `:331-335`; storage
  `models.credentials_json` (migration 028, `migrations/sqlite/028….up.sql:21`, PG twin `:12`), with
  legacy `credential_id` a shadow of `[0]` (`:25`).
- **Representable for an ordinary model, 1..16.** `validateCredentials` (`pkg/ui/server.go:682-758`)
  requires ≥1 (`:692-694`), caps at `models.MaxCredentialRefs` = 16 (`:698-700`; const
  `pkg/models/config.go:84`), enforces same-provider refs (`:741-747`) and unique ids (`:730-735`).
  **No rule caps at one.** Mirrors at `pkg/models/config.go:1055-1056` and
  `pkg/store/database/store.go:1965-1966`. The FE decision form reuses `MultiCredentialEditor`
  unchanged (`DecisionModelForm.tsx:4`, `:460-474`).
- **Representable for decision-kind specifically.** `validateDecisionRules`
  (`pkg/models/config.go:277-320`): D3 is a **≥1** rule (`:284-286`), not `==1`; D4 gates only the
  **primary** ref's provider (`:287-300`). A decision model may carry **2..16** same-provider refs.
- **But the lane consumes exactly ONE, permanently bound.** `pkg/decision/handler.go:299` →
  `modelsConfig.ResolveInternalConfigWithAffinity(modelID, modelID)` (`cmd/main.go:324-328`, **same
  string both args**) → decorator hot path `pkg/modelscache/models.go:680-737` →
  `ResolveInternalConfigWithAffinityCached` (`pkg/store/database/store.go:2688-2773`): 0/1 refs ⇒
  primary-only/fast path (`:2729-2740`); **2+ ⇒ `m.engine.GetOrSelect(modelID, conversationKey)`
  (`:2742-2746`) → exactly one weighted pick, stored under the key (`pkg/credentiallb/engine.go:313`,
  `:344-347`) and returned as a single `ResolvedCredential` (`store.go:2655-2662`)**. Same-string key
  ⇒ **one model-global binding**, TTL-refreshed on every hit (`engine.go:210-215`, `:266-281`).
- **No reachable rotation/exclusion/retry.** Cooldown is seeded only by `ExcludeAndReselect*` /
  `OnCredentialDeleted`, whose call sites are `pkg/proxy/internal_handler.go:263`,
  `pkg/proxy/race_coordinator.go:1900`, and the dangling-credential heal
  (`store.go:2649-2650`, `:2759-2760`) — and that heal **fails the current call** (502,
  `pkg/decision/handler.go:307-310`) rather than retrying.
- **Consequence.** "Member × credential genuinely failed" **cannot occur on this lane today** — every
  member attempt is de facto a single-credential attempt. The only reduction is relative to the
  *representable* data model, which is unreachable from this call shape.

**Scope note (now resolved).** The shipped behaviour is documented as: "per-request exhaustion covers
every **member**, and on this lane that is exactly equal to every **credential**, because the lane
resolves exactly one credential per member per request (verdict above)." If a future change makes
per-credential rotation reachable on this lane, that equality claim becomes false and this ADR must be
re-opened.

**Guard criterion (for carriage into `phase1-plan.md`) — ⚠️ FORMALLY VOID 2026-11-16 (ADR-11):**
pinning one-credential-per-request would pin the very defect R3/ADR-6 removes; **AC-R4 replaces it**.
Original text retained for the record:
a test asserting that **one decision model resolves exactly one credential per request** — so a future
change to the credential path cannot silently invalidate the "member-level = member × credential"
claim without turning a test red. This is the only code work this ADR adds.

**Operational observation recorded for the follow-up (not a blocker).** Because the binding key is the
same string for both arguments, a decision model with 2..16 refs keeps its **first weighted pick
indefinitely** (TTL-refreshed per hit) and nothing on this lane writes a credential cooldown on
failure. With our group loop, a limit-class failure demotes the **member**, which masks this at member
granularity — but the member's *credential* binding does not rotate. This sharpens the wishlist item
below rather than blocking the release.

**Future-improvement wishlist (for the leader to record; the planner does not write the wishlist).**
- Slug: `decision-lane-credential-multiplicity`
- Text: "Per-credential exhaustion and credential rotation on the /v1/decisions lane: the data model
  accepts 2..16 credential refs per decision model, but the lane resolves exactly one credential per
  request under a permanent per-model binding (`ResolveInternalConfigWithAffinity(modelID, modelID)`),
  and nothing on this lane writes a credential cooldown on failure — so a rate-limited bound credential
  never rotates away. Implement within-member credential rotation (an `ExcludeAndReselectForClass`
  equivalent; `pkg/credentiallb` is import-guard-legal from `pkg/decision`) so decision-lane exhaustion
  is member × credential and a failing credential rotates, matching the text lane."

### ADR-3 — `data-center-scripts` backlog is out of scope (closes the pin-push question)

**Decision.** Confirmed **OUT OF SCOPE** for this commission. Do **not** push `data-center-scripts`;
do **not** carry its 8-commit backlog (`latest` @ `b0e98f4` ahead of `origin/latest`).

**Deploy path.** Deploy the newly built image using the **EXISTING pinned manifests** (house flow) —
a local-only manifest/pin edit plus `kubectl apply` / `kubectl set image`. No new `dcs` commit, no
push.

**Consequence for the runbook.** The pin-push step is **removed** from the ship/deploy sequence. The
`dcs` backlog is left untouched and is an **open item for the human owner**, not an action for this
commission or for any agent in this release.

### ADR-4 — Environment facts for deploy + E2E

Verified by the leader; no action required to establish them, but both constrain the runbook.

1. **The remote-hands MCP gateway is DISABLED on this deployment.** Deploy and E2E must use the
   **house SSH + kubectl/helm** path, with kubeconfigs sourced from the `data-center-scripts` repo.
   **Paths only** — never echo a secret value, never `cat` a kubeconfig. Any runbook step that
   assumes MCP-mediated host access is invalid for this deployment.
2. **The ensemble secret store holds ONLY `9router-api-key`.** There is **no admin secret** for the
   primary `/fe/api` admin surface. The E2E admin path must therefore be the **house kubeconfig +
   port-forward / apply path** to `/fe/api`. An operator must not go looking for an admin credential —
   none exists.

---

## ADRs — USER RULING (2026-11-16, mid-amendment): pick / attempt / chain semantics (R1–R5)

**Authority.** These supersede the *framing* of the earlier demote-based text. Where the two disagree,
**this section wins**. Per instruction the earlier text is **not silently rewritten** — it is marked
`SUPERSEDED` in place so the record shows the change of semantics.

**Doctrine line to pin in code, VERBATIM:**

> The cache is an optimization, not the truth: it may reorder candidates, never exclude them — and it
> may suppress an ATTEMPT, never a PICK or a CHAIN.

### ADR-5 — Pick is POLICY; a mark suppresses an ATTEMPT (supersedes the "demote" framing of D-4)

**R1.** The skip cache **MUST NOT decide the group pick**. Pick = POLICY (pools, weights, sticky) and is
**stable**. A mark **never** changes WHICH member is the head.

**R2.** Marks suppress **only the attempt** — a zero-cost skip, **no upstream call** — never the pick,
never the chain.

**Supersession (explicit).** The following standing text is **SUPERSEDED** by ADR-5:
- `## D-4` and contract 11's phrasing *"marks demote (unmarked first, soonest-expiring after)"*, and
- any contract text implying a marked member is *attempted last* after reordering.

Under R1/R2 a marked member is **not reordered and not attempted**: the head stays the head (policy),
and its attempt is skipped at zero cost. "Demote" as *reordering* is no longer the decision lane's
semantics. The v193 doctrine's *"may reorder candidates"* remains true as general text-lane allowance,
but **R1 narrows it for the group pick**: marks do not move the head.

**Consequences for the decision lane (material, reported to the leader):**
1. **Consuming the existing sweep's ordering as-is violates R1.** `pkg/store/database/store_group_rewrite.go`
   Phase 4 sorts by `memberDemotionExpiry` (soonest-expiring first, `:229-242`), i.e. the returned order
   **is** mark-influenced. A lane that consumes that order for its head inherits mark-decided picking.
   Either the lane needs a **policy-only ordering** (lane-gated parameter or separate path), or it takes
   the policy order and applies suppression itself. **This makes the "reuse the sweep for ordering"
   plan item impossible as specified** (`decisions.md:168` (b), `:114`).
2. **All-marked degenerate case needs a rule (OPEN).** If every candidate is marked, suppressing every
   attempt yields a failure with **zero upstream calls** — i.e. the cache becomes a failure oracle,
   contradicting the doctrine. Recommended rule: suppression never removes the **last** attempt —
   when no unmarked candidate remains, the **policy head is attempted** (the pick is still policy).
   This requires explicit confirmation.

**Acceptance criteria (for carriage).**
- **AC-R1** — A mark on the head does **not** change which member is reported/treated as the head
  (pick identity is a pure function of pools + weights + sticky, independent of marks).
- **AC-R2** — A mark on the head causes a **zero-upstream-call skip** for that member, observable as a
  skip (not a failure) in telemetry/events.
- **AC-R3** — With N candidates where k are marked, the attempt sequence is the policy order **minus**
  the marked ones (no reordering), and exhaustion accounting counts only genuine attempts.

### ADR-6 — R3 order of operations: a member is not failed until its OWN resilience is consumed

**R3.** A member is **NOT "failed"** until its own resilience is consumed: **hedge/racing + its own
`fallback_chain` FIRST**; only then advance to the next group member.

**Consequence — this RETRACTS the earlier E-6 framing.** Member-level fallback is **not optional** and
**not a scope reduction**: R3 makes it the **ORDER OF OPERATIONS**. `ADR-2` must therefore be read as
*superseded in framing* (its evidence remains factually correct; its decision does not stand).

**What "own resilience" is on the decision lane TODAY — it is nothing:**
- one credential per request (`pkg/decision/handler.go:299` → `store.go:2742-2746` → `credentiallb/engine.go:344-347`);
- one upstream call, no retry/race (`pkg/decision/handler.go:430-433`);
- `fallback_chain` is **forbidden** on decision models by D5 (`pkg/models/config.go:302-304`).

So R3's "hedge/racing + own fallback_chain FIRST" is **vacuous on this lane unless the member gains
resilience**. The minimum faithful implementation is **per-credential attempts within a member** —
multiplicity is representable (2..16 refs, `pkg/models/config.go:357`, cap `pkg/ui/server.go:698-700`),
so a member with 2+ refs has real resilience to consume before advancing. `pkg/credentiallb` is
import-guard-legal from `pkg/decision` (guards ban only `pkg/proxy` and `pkg/imggen`,
`pkg/decision/handler_test.go:40/:65`, transitive `:90-108`).

**Conflicts requiring a decision (reported to the leader):**
1. **D5 vs R3**: decision models forbid `fallback_chain` (`config.go:302-304`). Either lift D5 for
   decision models (new scope) or accept that chain-resilience is empty for decision members and R5's
   hedging requirement carries the whole load.
2. **Member × credential is back in scope.** The earlier "member-level only" position cannot satisfy R3
   for a multi-credential member.

**Acceptance criteria.**
- **AC-R4** — A member with 2+ credentials is attempted across **its own** credentials before any
  advance to the next group member; advancing early is a failure of the loop, not an optimisation.
- **AC-R5** — The "member failed" predicate is explicit and testable: it is true only when the member's
  own resilience (per-credential attempts; chain if one exists) is exhausted.

### ADR-7 — R4 chain-only / deliberately-inactive models are SUPPORTED, not a defect

**R4.** Models may be deliberately created **INACTIVE**, exposed to no group, reachable **only** as a
`fallback_chain` target (live example: `coding4` via `agentic-Main`'s chain). Validation **MUST allow**
inactive/chain-only targets. **"Disabled-but-chain-referenced" is NOT a data defect to clean up.**

**Implications for this commission.**
- Validation must not reject an inactive model merely because it is a chain target, and no sweep,
  migration, or "cleanup" may treat it as orphaned data.
- Such models must **NOT** be surfaced as **group candidates** (they are chain-reachable, not
  group-eligible). The FE member picker and any server-side candidate list must exclude them.
- Nothing may **auto-enable** a model as a side effect of adding it to a group.

**Acceptance criteria.**
- **AC-R6** — An inactive/chain-only model is accepted as a `fallback_chain` target and remains
  inactive; it is **not** offered as a group candidate; adding it to a group does **not** flip
  `enabled`.
- **AC-R7** — Decision-group members are **never silently auto-enabled** by save/validation.

### ADR-8 — R5 racing must be CONSUMABLE (single-model list can still hedge)

**R5.** Racing must be consumable — a **single-model list must still be able to hedge**, else
"racing consumed" is impossible for chain-less members.

**Gap on this lane.** There is no hedging today (one `Do`, `pkg/decision/handler.go:430-433`). A
single-member, single-credential decision group therefore has **nothing to consume**, which makes R3
vacuous for that shape — exactly the case R5 calls out.

**Options (needs a decision — this is the largest scope question in the ruling).**
- **(a) Structural only:** define "resilience consumed" as a loop contract that *could* issue multiple
  attempts, and satisfy R5 by having per-credential attempts (ADR-6). A one-credential member still
  has a single attempt — R5 is satisfied in *structure*, not in *behaviour*.
- **(b) Real hedging:** implement speculative/parallel or retry attempts for the decision lane so even a
  one-model list can hedge. This is a **genuine new capability** and materially expands Stage 4.

**Acceptance criteria.**
- **AC-R8** — A single-member decision group's member has a defined, testable "resilience consumption"
  path (per option chosen above), and the release documents which option was chosen.

### ADR-9 — Coordination: a PARALLEL commission is fixing the TEXT lane to these semantics

A separate commission is bringing the **text lane** to R1–R5. **This release MUST NOT change text-lane
behaviour.** Any shared-code semantic change must be **lane-gated** and provably text-neutral.

**Collision-hotspot files** (shared by both commissions — expect merge conflicts; the ship step must
**surface, never force**, them):

| File | Why it collides |
|---|---|
| `pkg/store/database/store_group_rewrite.go` | the four-phase sweep, `memberDemotionExpiry` ordering, the request-time predicate, `ExcludeGroupMember`, `ClearGroupMemberCooldown` |
| `pkg/store/database/store.go` | `ResolveInternalConfigWithAffinity*`, group engine wiring, credential resolution |
| `pkg/grouplb/engine.go`, `selector.go`, `binding.go` | selection, cooldowns, binding semantics |
| `pkg/modelscache/models.go` | decorator forwards — **both** commissions may add methods (the trap site) |
| `pkg/skipcache/skipcache.go` | `Mark`/`Marked`/`SoonestExpiry` + the doctrine comment that now changes |
| `pkg/proxy/handler.go`, `handler_groups.go`, `handler_anthropic.go`, `handler_helpers.go`, `race_coordinator.go` | text-lane pick/attempt/chain logic (this release touches only the three lane-gated guards) |
| `pkg/credentiallb/engine.go` | binding + cooldown seeding (`ExcludeAndReselect*`) |
| `pkg/models/group.go`, `pkg/models/config.go` | group rules, kind rules, decision rule set (D5) |
| `pkg/providers/interface.go` | classification vocabulary; our `pkg/credclass` extraction makes it delegate |
| `pkg/ui/server.go` | group/kind validation mirrors (helper ≡ POST ≡ PUT parity) |
| `cmd/main.go` | wiring for both lanes |

**Acceptance criteria.**
- **AC-R12** *(renumbered from AC-R9 on 2026-11-16 to resolve the ID collision noted by phase1 §A20/7)* — Text-lane behaviour is byte-untouched by this release; every shared-file change is lane-gated and carries its text-lane pin. A conflict in a hotspot file is resolved by **re-deriving from the merged semantics**, never by taking one side wholesale.

### ADR-10 — Consolidated criteria for carriage into the phase plans (AC-R1..AC-R9)

| ID | Criterion |
|---|---|
| AC-R1 | Mark never changes the head (pick is policy-only) |
| AC-R2 | Marked head ⇒ zero-upstream-call **skip**, distinguishable from a failure |
| AC-R3 | Attempt sequence = policy order minus marked; exhaustion counts only genuine attempts |
| AC-R4 | Member's own credentials attempted before advancing |
| AC-R5 | "Member failed" predicate explicit: own resilience exhausted |
| AC-R6 | Inactive/chain-only accepted as chain target, not a group candidate, never auto-enabled |
| AC-R7 | Decision-group members never silently auto-enabled |
| AC-R8 | Single-member group's resilience consumption defined + documented (option a or b) |
| AC-R9 | Doctrine line present **verbatim** at the seam |
| AC-R10 | Pick vs attempt vs skip are **distinct** in telemetry/events |

*Renumbering note (2026-11-16): the criterion originally numbered **AC-R9 inside ADR-9** (text-lane
byte-untouched) is now **AC-R12**; **AC-R9 in this table** (doctrine line verbatim at the seam) is
unchanged. Plan citations reading `AC-R9 (ADR-9)` should read `AC-R12 (ADR-9)`.*

**Open questions raised by this ruling (for the leader).**
- **OQ-1** — All-marked degenerate case: recommended rule is "suppression never removes the last
  attempt; the policy head is attempted". Needs confirmation.
- **OQ-2** — D5 vs R3: lift the `fallback_chain` ban for decision models, or accept that decision
  members have no chain and R5 alone carries per-member resilience?
- **OQ-3** — R5 scope: option (a) structural or (b) real hedging on the decision lane?
- **OQ-4** — Is a **disabled** decision-group member (a) skipped as unservable (current predicate) or
  (b) attempted? R4 covers chain targets, not group members; the two must not be conflated.

---

### ADR-11 — Closures of OQ-1..OQ-4 (leader, 2026-11-16)

**OQ-1 — all-marked degenerate case: CLOSED.** Suppression **never removes the last attempt**. When
**every** candidate is marked, the **POLICY HEAD receives exactly one honest (unsuppressed) attempt** —
head suppression lifts **for that request only**. The cache can never be a failure oracle.
*Rationale:* letting "soonest-expiring" choose the honest attempt would let a **mark decide the head**,
violating R1. This **intentionally diverges** from the text lane's current soonest-expiring rule —
lane-gated (text lane untouched in this release) and **flagged for the parallel text-lane commission**.

**OQ-2 — D5 vs R3: CLOSED.** The chain clause is **VACUOUS** for decision members. Do **not** lift D5 —
`fallback_chain` remains **FORBIDDEN** for decision-kind models in this release. Recorded explicitly:
**"no equivalent concept on this lane; deliberately not built"** (user direction: do not fake a
concept for symmetry). Per-member resilience on the decision lane = **per-credential attempts**
(A-1 / AC-R4). Chains-on-decision-models = **future work** (leader records the wishlist entry).

**OQ-3 — R5 scope: CLOSED.** **STRUCTURAL loop contract — option (a).** The member loop treats a
single-member list as **fully valid and fully consumable** (per-credential attempts, exhaustive before
the request may fail). **No speculative/hedge race engine** on the decision lane this release.
"Racing consumed" is defined by **the loop contract reaching its end**, not by a live race.
Non-streaming typed lane + the user's no-invention direction govern.

**OQ-4 — disabled group member: CLOSED.** Disabled group members are **NOT attempted** — the
request-time servability predicate stays **exactly as today** (disabled ⇒ unservable). R4 applies to
**CHAIN TARGETS and VALIDATION only**: inactive/chain-only models are valid chain targets, never
flagged as defects; they are **not** surfaced as group candidates (A-6); nothing auto-enables (A-7).
**Chain targets and group members are two populations — do not conflate them in validation or in tests.**

**AC-C1 — FORMALLY VOID.** Agreed with the planner's reasoning: pinning one-credential-per-request
would pin the defect R3 removes. **AC-R4 replaces it.**

### ADR-12 — Per-layer caches on the decision lane (RULING-3, user, verbatim intent)

Build the decision lane with **PER-LAYER caches from day one**, not a single shared one:

| Layer | Question | Contents | Reader |
|---|---|---|---|
| **GROUP** | "is this decision member servable in this group?" | **policy facts only** | the **pick** only |
| **MODEL** | "is this decision MODEL limited?" | model-attributable failures | the **chain/model layer** only |
| **UPSTREAM** | "is this provider/credential working?" | credential failures | **credential selection** only |

**Layers NEVER read each other's state.** Consequence: **a credential 429 can never change the group
pick — the invariant holds STRUCTURALLY, not by discipline.** Where the decision lane has no equivalent
of a layer, **say so explicitly rather than inventing one**; do not fake a layer for symmetry.

**Interplay with ADR-5..ADR-11 (recorded as ruled):**
- With **A-1** (per-credential attempts within a member), the **UPSTREAM layer becomes real** on this
  lane — its **reader** is per-credential selection inside the member loop; its **writer** is
  credential-failure classification (an `ExcludeAndReselectForClass`-equivalent).
- The **MODEL layer's chain reader is ABSENT** (OQ-2: chain clause vacuous) — **recorded as such**,
  not faked. The model layer still has a reader on this lane: the member loop's per-member
  attempt/suppression decision.
- The **GROUP layer is pick-only policy facts** — this is the structural expression of R1.
- **OQ-1's policy-head honest attempt is part of this design**: when every candidate is marked, the
  head's suppression lifts for that request — the pick never consulted anything but policy.

**Acceptance criterion (adds to ADR-10):**
- **AC-R11** — The three layers are separable in code and in tests: a credential-layer state change
  (upstream 429 mark) provably cannot alter the group pick; the group pick provably reads no
  failure-state; each layer's absence-on-this-lane is recorded where applicable (MODEL-layer chain
  reader: ABSENT, per OQ-2).

---

### ADR-13 — Second tranche of leader rulings (2026-11-16): AM-4 failure-class policy, picker asymmetries kept, marker negative control, ExcludeGroupMember legality

**AM-4 — 5xx inside a member: RULED ADVANCE, not rotate.** The decision lane mirrors the text-lane
failure-class policy **EXACTLY** (cite: `pkg/proxy/cred_failover_class.go:162` `rotationEligible`,
`:190` `memberAdvanceBlocked`; marks fire on limit-class only — `pkg/proxy/race_coordinator.go:2106-2107`):
- **rotation-eligible** (limit-class + upstream 401/403) ⇒ **rotate within the member** (an
  upstream-layer write per classification);
- **5xx** ⇒ **ADVANCE to the next member immediately** — no within-member rotation, **no mark, no
  cooldown write** (transient, provider-attributable);
- **memberAdvanceBlocked** classes (400/413/415/422 + client-cancel) ⇒ **fail-fast advance-blocking**,
  as the text lane.

*Rationale (overriding phase1's earlier rotate-on-any-failure):* R3's literal resilience is
"hedge/racing + own `fallback_chain`" — it does **not** name blanket credential consumption; rotation
policy is failure-class-driven; and the goal is **two lanes, ONE answer** (the text lane advances on
5xx today). **Exhaustiveness** ("only fail after every member genuinely failed") is **per the
failure-class policy** — a member that 5xx'd **HAS genuinely failed for this request**.

**Picker asymmetries: KEPT AS PLANNED.** `laneModels` untouched (zero risk to the shadow picker);
decision candidates = kind **AND** enabled (A-6); the text picker keeps offering inactive chat models,
pinned by FE test T1. Changing the text picker is the **parallel commission's** call, not this
release's.

**Marker: CONFIRMED UNCHANGED** — `decision_all_members_failed` (a NEW Go string this feature
introduces), consistent across phase1/phase3. The architect's "marker must change" row is really the
requirement *"distinctive vs v193 + measured controls"*, so the **negative control is pinned
explicitly**: the string must be **ABSENT from the live v193 binary** (verifiable against the
`verify_image.sh` cache for the v193 per-SHA tag) and **present in the new build**; no
Tailwind/static-UI false-hit classes (the `grep -ci sticky` prohibition stands).

**ExcludeGroupMember legality (for the record).** Usage for **GENUINE-FAILURE advance bookkeeping**
(the request-scoped tried-set; cooldown writes ONLY after genuine failures) **REMAINS LEGAL**. What
stays barred is using it to **express a mark/suppression** or **as an ordering device**. Phase1's
`:318` / `:355` / `:1150` reading is correct as written.

---

### ADR-14 — AM-3 CLOSED: `pkg/credentiallb` is import-legal from `pkg/decision` (developer verdict, 2026-11-16)

**Verdict: LEGAL — unconditionally.** Completion-grade evidence (developer `b14df3b4`):
- the guard tests exist and pass at `pkg/decision/handler_test.go:40-63` / `:65-88` / `:90-109` with literal `=== RUN` / `--- PASS` output;
- the `go list -deps` closures are **clean of `pkg/proxy` + `pkg/imggen`**;
- a **real direct-import scratch in `/tmp` passed ALL guards** (positive control);
- a **negative control — a QUOTED banned path in a doc comment — FAILED the guard**, proving the
  raw-text mechanism works in both directions.

**Key fact (makes the question a no-op):** `pkg/decision` **ALREADY has `pkg/credentiallb` in its
transitive closure today** — `decision → auth → store/database → credentiallb`, a **non-test dep**.
The guard's closure walk has been passing with that edge present all along; a direct import adds
nothing the guard has not already accepted. **P0.9's "convince the guard" branch is a no-op.**

**Corrections and constraints carried by this closure:**
1. **Research-characterization correction.** `pkg/credentiallb` is **NOT stdlib-only**: it imports
   `pkg/models` (→ `crypto`), and the package has **8 files**, not the 3 the research memo enumerated
   (`engine.go`/`binding.go`/`failure_class.go`). Conclusion unaffected — the closure is still clean
   of banned paths — but no phase document should repeat the stdlib-only or 3-file phrasing.
2. **Raw-text guard trap.** The `pkg/decision` import guard scans **every `.go` file in the package
   dir, including `_test.go`**, as **raw text** — a **comment that merely QUOTES `pkg/proxy` or
   `pkg/imggen` fails the guard** (proven by the negative control). The doctrine-line pin and any
   layering documentation written inside `pkg/decision` **must not name those paths**. (Different
   mechanism from the cachelayer AST pin — no AST precision here.)
3. **Placement constraint.** `pkg/grouplb` **bans `pkg/credentiallb`** (`engine_test.go:401`,
   zero-credential guard). Credential-aware logic — the upstream-layer cache reader/writer and the
   per-credential rotation — **lives IN `pkg/decision` and must NEVER be pushed down into
   `pkg/grouplb`.**
4. **Stale prose to fix in Stage 1.** `pkg/decision/doc.go:6-15` claims imports
   `{models, auth, usage, events}` only; the real closure already includes `config`,
   `store/database`, `grouplb`, `skipcache`, `toolrepair`, `credentiallb`, `crypto`. Stage 1 must
   amend `doc.go`, or the feature reads as a guard violation from day one.

---

### ADR-15 — Review-cycle-1 leader decisions + ruled warnings (2026-11-16)

Recorded against `review-plan-1.md` (verdict CRITICAL GAPS; ruling layer verified coherent, blockers
in the gate/runbook layer and base currency). **The reviewer's finding that the planner's "zero open
items" claim was FALSE as to internal consistency (CG-2's unmarked R5) is acknowledged as a
correction** — the drift class is the corpus's third; the supersede-don't-erase discipline held, but
unmarked operative text slipped through. The D-5 residual at `decisions.md:240` is fixed in this ADR
tranche.

**D-A — ADR-1 scope extension (guard target).** The guard rejects **ANY persisted mutation that
changes a group member's EFFECTIVE lane/kind** — including **`shadow_model_id` IFF it participates in
effective-kind resolution for group members**. *Verification requirement (Wave-B, post-re-anchor):
establish with file:line whether a member's `shadow_model_id` can affect lane resolution; if it
cannot, record that explicitly and keep it out of scope.* **Rejection status:** mirror the existing
group-save validation precedent in `pkg/ui/server.go`; **if no precedent exists, use 409 Conflict**.
Which applies — and why — is a Wave-B verification (base-relative line pins); the fallback rule is
decided now.

**D-B — E2E delete-vs-keep: KEEP the created decision group.** The group left in place is the user's
desired end state. E2E stays **non-destructive**; the user's 2 live decision models are never mutated
or broken. (Closes review warning 6.)

**D-C — All-credentials-cooling: OQ-1 symmetry at member level.** If **every credential of a member**
is marked/cooling, the member gets **exactly ONE honest attempt on its policy-first credential**; if
that attempt genuinely fails **per the AM-4 class policy**, the member fails and the loop advances.
**The cache is never a failure oracle at ANY layer.** Pinned by a new test (see ADR-15 test delta).

**Ruled warnings (folded now; the remainder per the artifact's own recommendations):**
- **W-fail-open (`groupLaneOf`):** KEEP fail-open for **genuinely unknown** shapes (text-lane parity
  for legacy/hand-edited rows) — BUT write-time homogeneity (D-2) must make mixed groups impossible,
  and **any group inferred as chat that contains ≥1 decision member must NEVER silently serve**:
  loud **400 `decision_misroute` + a telemetry event**. Pinned by a new test.
- **W-event rename:** `member_skipped` → **`member_suppressed`** (lane-scoped, consistent with the
  AC-R10 pick/attempt/suppress vocabulary; avoids the 68-hit v193 `member_skipped` namespace and
  v194's `model_fallback_*`).
- **W-tsc exit-code swallow:** the tsc gate recipe must use `bash -c` + `PIPESTATUS[0]` (house dash
  rules) — `| tee` alone swallows the exit code.
- **W-vocabulary set-equality pin (T5):** add the missing pin — decision-provider vocabulary
  set-identity across the FE constant + the 3 validation gates + registry init, so "NO vocabulary
  change" is **pinned, not asserted**.
- **W-D-5 leftover:** `decisions.md:240` fixed in this tranche (supersession marker; 502 shape
  unchanged).
- **W-E2E AllowedModels:** add the 403 false-negative **pre-check before E5** (a token whose
  `AllowedModels` excludes the group yields 403, which the E2E must not misread as failover
  evidence).

**Wave split (explicit, so nothing is lost).** This ADR is **Wave A — HEAD-independent**. The
following are **Wave B, gated on the KV row showing `base_reanchored=true`** and on giter's v194
delta map (do not re-derive the range independently): **CG-4** (re-anchor all five docs' base
assertions to the new tip; re-derive every BASE-relative pin; redefine "text-lane byte-untouched" as
"no v194 semantic regression"; extend the ADR-9 hotspot list with the v194 files; re-trigger the full
gate on the **merge regardless of conflict**; re-derive the text-group regression pins against the
NEW text behavior; state which lane-gating items become **reuse** vs remain **build** if the parallel
commission already landed R1–R5 text-lane semantics) · **CG-5** (rollback tier-1 = `:fd1649c` /
`helm rollback 37`, tier-2 = v193; expected CI **≥ 195**; refresh rev/gen/dcs numbers — **each
verified against giter's fresh state, never copied blindly**) · the three **re-verification items**
(decorator-forward enumeration against the NEW base; D-2 negative-control tests spelled out; AM-3
P0.9 both-guard-layer coverage) · **D-A's two verifications** (shadow participation; status-code
precedent).

**Test delta (this ADR adds three Go tests; phase 1 owns the names):** the D-C all-credentials-cooling
honest-attempt pin; the instance-separation pin (CG-2); the chat-inferred-with-decision-member
never-silently-serve pin (W-fail-open).

---

### ADR-16 — v194 base re-anchor: reuse-vs-build re-derivation (2026-11-16; base 24c51ef → 2b987bf)

**Base moved.** v194 `fix/cache-layer-separation` (36 files, +3507/−630, 15 commits) landed at
`origin/latest 2b987bf` / master `fd1649c`; the worktree branch was re-anchored (planning docs
untracked, verified byte-identical). **Every base assertion in the five plan docs (`24c51ef ==
origin/latest`) is superseded; phase docs are re-anchored in Wave B.** Delta facts per giter's map,
load-bearing items independently verified in-worktree (cites below).

**The user's R1/R2 semantics LANDED on the text lane.** The sweep's own doc
(`pkg/store/database/store_group_rewrite.go:88-99`) now encodes the ruling nearly verbatim —
*"The group pick is POLICY (pools, weights, sticky) and the cache must not decide it. A health mark
(upstream or model layer) never changes WHICH member becomes the head; it only suppresses that
member's ATTEMPT downstream."* Verified structurally:

- the sweep is **cache-blind** — the only `skipcache`/`modelcache` occurrence is the pin's own comment
  (`:281`); ordering rides **GROUP-owned rotation cooldown only** (`memberDemotionExpiry` reads
  `groupEngine.MemberCooldownUntil`, `:284-292` — explicitly *"NEVER a skip/model health mark"*);
- Phase 1 head = engine offers (sticky/weighted — POLICY, `:101-112`); engine-cooling members are
  demoted into the bucket; Phase 4's demoted head "is a full candidate" (`:118-124`); the old
  filter-class special cases are **GONE** (`:126-128`);
- `pkg/skipcache` declares the **LAYER CONTRACT** as its package doc (`:4-15`: GROUP=grouplb policy /
  MODEL=`pkg/modelcache` / UPSTREAM=skipcache; *"layers never read each other's state"*); `pkg/modelcache`
  is a **new real package** (`modelcache.go`, `policy.go`, `reset.go`); the manager seam is
  `ModelSkipCache:` (`cmd/main.go:256`), `SetSkipCache` is gone repo-wide; `pkg/credentiallb` gains
  `CooldownUntil(modelID, credID)` (`engine.go:468`).

**Work items DISSOLVED (recorded as deleted — a good outcome per the leader):**

| Item | Disposition |
|---|---|
| **I-1 — lane-gated policy-only ordering** | **DISSOLVES.** The sweep at `2b987bf` is policy-headed and cache-blind; the decision lane consumes it directly. No separate ordering path, no lane-gated parameter. |
| **AM-1 — exported `pkg/grouplb` policy selector** | **DISSOLVES** (was I-1's implementation cost). |
| **AM-2 — "second skip-cache instance, never shared"** | **RE-DERIVED (pending phase-1 Wave-B confirmation): CONSUME the landed per-layer packages; the fence is LAYER separation, not instance separation.** Under suppression-only semantics a mark can no longer leak into ordering, so the original reason for a separate instance is gone — and divergent health views across instances would give two lanes two answers. Consequence: the **instance-separation pin test dispatched in Wave A is likely superseded by a layer-separation pin**; CG-2's R5 supersession marker remains but on the new basis. |

**Re-derived direction (phase-1 Wave B produces the stage-level table):** REUSE-first — consume
`pkg/skipcache` (UPSTREAM), `pkg/modelcache` (MODEL; whether the decision lane's member-suppression
reader consumes it, vs the OQ-2 "chain reader ABSENT" record, is the open derivation), `pkg/grouplb`
policy, and the sweep as-is. **Surviving build work:** the kind widening of `isServableGroupMemberBase`
(still chat-only at `:294-296` — verified), the decision-lane member loop with per-credential rotation
(AM-4 classes; `CooldownUntil` at `engine.go:468` is the natural precondition check for D-C's
all-credentials-cooling honest attempt), `pkg/credclass`, the admin-API mirrors, and the FE.

**Constraints encoded:** (1) the **AST pin** on `store_group_rewrite.go` (15709a1) constrains any
decision-lane edit to that file — phase 1 must read what the pin forbids and encode it at every touch
point; (2) the architect's named seam `store_group_policy.go` **does not exist** at `2b987bf` — today's
seam file is `store_group_rewrite.go` (advisory superseded); (3) **"text-lane byte-untouched" is
redefined as "no v194 semantic regression"**, and the text-group regression pins are re-derived against
**v194's** text behaviour, not v193's; (4) `pkg/modelscache` was zero-diff in v194, but the
`SetSkipCache → ModelSkipCache` seam change may have altered the forwarded method set — **decorator
enumeration must be re-verified against `2b987bf`**; (5) FE pins on `EventLog.tsx` (+18) and `types.ts`
(+5) need re-derivation.

---

**D-A resolutions (Wave B, phase1 §E8 — recorded as ruled):**
- **(a) `shadow_model_id` scope: OUT.** Verified: every non-test `ShadowModelID` use at `2b987bf` is
  group-row-only (claims index `pkg/modelscache/models.go:248-249`; `/v1/models` hide
  `pkg/proxy/handler.go:382-383`) or the group's own shadow-target validation
  (`pkg/ui/server.go:904`, `:946-974`); kind resolution reads only the row's own `kind`
  (`isServableGroupMemberBase` `:301-311`, claim-blind `GetModelNative`). **Shadow cannot affect
  lane ⇒ it stays out of ADR-1's guard.**
- **(b) Rejection status: 400, not 409.** A precedent EXISTS — the group-save family writes
  `http.StatusBadRequest` with the bare `{"error":"…"}` envelope (`pkg/ui/server.go:864-868`;
  member-kind reject `:942`; shadow rejects `:959`/`:963`). **ADR-1's kind-change guard uses 400
  Bad Request**; the 409 fallback does not apply.

**AM-2 verdict: CONFIRMED (phase1 §E2).** Shared instance; the fence is LAYER separation —
`pkg/skipcache/skipcache.go:4-17` declares itself "a **process-wide** registry … read by credential
selection + attempt suppression ONLY"; `cmd/main.go:242`/`:249` construct one instance each and
`:255-256` hand both to the proxy config (one instance, both lanes); upstream health is
per-credential, not per-lane. Enforced structurally by
`TestCacheLayer_Structural_PackageLevelNoCacheImports`
(`pkg/proxy/cache_layer_separation_test.go:752-786`). **The Wave-A instance-separation test is
WITHDRAWN as pinning a wrong invariant**, replaced by
`TestDecision_Group_CacheLayerSeparation_SharedUpstreamInstanceOneAnswer`.

**Upstream defect flagged (not ours to fix in this commission):** v194's own Phase-4 comment at
`store_group_rewrite.go:230-232` is stale — it says "soonest-expiring mark" while the code now sorts
by the GROUP rotation expiry. Reported for the parallel text-lane commission / upstream owner.

---

### ADR-17 — Architect final fold (2026-11-16): four RED gaps, retraction, D5 enforced, AC-R4 upgraded, four rulings

`architecture-recommendation.md` complete (§0–§8 + §7a). Registered per the leader:

**Four RED gaps — CRITICAL-grade plan items** (fixes live in the architect doc; phase1 folds each with file:line):
1. **Fallback-chain bypass** — a chat main's `FallbackChain` may name a group id
   (`pkg/proxy/handler_functions.go:200-202`, `handler_anthropic.go:290-292`; the rewrite loop
   iterates ALL entries `store_group_rewrite.go:52`; write-side validates existence only
   `pkg/ui/server.go:1996-2010`, `pkg/models/config.go:1021-1025`; the misroute guards check only
   `rc.resolvedModel` `handler.go:576`, `handler_anthropic.go:272`). Fix = the **rewrite-boundary
   lane check**: a decision-lane group entry reached from the chat lane ⇒ `FailedGroupID` — closes
   the bypass and is orthogonal to lane representation.
2. **AST-pin pick placement is load-bearing** — a pick in `pkg/store/database` **inherits the pin**
   (cannot compile a cross-layer read; `cache_layer_separation_test.go:752-784` @2b987bf); placed
   only in `pkg/decision`, isolation reverts to a mirror file-level pin. Obligations: pick stays in
   place (pinned) + attempt stage in `pkg/decision` (may import both caches) + a **file-level
   mirror pin over the decision pick/walk files**.
3. **Fail-open lane gate on 3 of 4 consumers** — fix = **compile-time `var _` assertions + effect
   tests** so the gate is wired on ALL consumers, not 3/4.
4. **Contract-15 `/v1/models`** — the decision-group exclusion must be complete per the architect's
   §2a W9 gap list (phase1 folds the exact fix as written there).

**Retraction encoded:** I-1 dissolution CONFIRMED by the architect (§8.1). **`store_group_policy.go`
is RETRACTED** — the architect's own §2c proposal is superseded by §8.1; the file must appear
NOWHERE in the final corpus except as a retracted proposal.

**D5 CONFIRMED ENFORCED (OQ-2 upgraded from assertion to enforced rule):** `pkg/models/config.go:301-304`
byte-identical at both bases (`git diff 24c51ef 2b987bf -- pkg/models/config.go` = empty); enforced at
DB `store.go:2063` + UI mirrors `ui/server.go:1180-1187` (POST) / `:1645-1652` (PUT); the FE never
exposes a chain field for decisions (`DecisionModelsTab.tsx:131` stamps `fallback_chain: []`). **The
chain clause is vacuous by enforced rule.**

**AC-R4 UPGRADED to necessary-for-correctness.** Credential multiplicity is REPRESENTABLE today —
cap 16 (`config.go:357`, `MaxCredentialRefs :84`), decision validation `>=1` with **no cap of 1**
(`:284-286`), admin API accepts (`server.go:1161`/`:1628`), FE ships `MultiCredentialEditor`
(`DecisionModelForm.tsx:468`), and `len>=2` **already routes through `credentiallb.GetOrSelect`**
(`store.go:2605-2610` @2b987bf; pool keyed per model ROW ID, `RebindFromStore(mc.ID,…)` `store.go:789`).
**The gap is consumption, not representation:** an operator with 2 credentials on a decision model
gets exactly one attempted today — **the per-credential member loop IS the fix**, and the substrate
(pool, weighted pick, `ExcludeAndReselectForClass`) already exists. **Anchor re-based:**
`ExcludeAndReselectForClass` sits at `credentiallb/engine.go:657` @2b987bf (was `:637` @24c51ef) —
swept across the corpus.

**Four rulings encoded:**
- **Rollback ladder:** tier-1 = **current live v194** (`:fd1649c` / daoduc helm 37); tier-2 = v193
  (`:261622cf` / helm 36). **Fresh-verify every number at ship time** (CI number, primary gen, dcs pins).
- **E2E group fate:** KEEP the created decision group (D-B stands — user's desired end state;
  non-destructive E2E).
- **OQ-1 proof venue:** MANDATORY deterministic unit test in the Go table — **SATISFIED**: the
  all-marked ⇒ policy-head-honest-attempt case is `TestDecision_Group_AllMarked_PolicyHeadHonestAttempt`,
  already in the 63-name table. E2E observation only if non-destructive.
- **ADR-1 status code:** RESOLVED — a precedent EXISTS (`pkg/ui/server.go:864-868`, the group-save
  family returns `400 Bad Request` with the bare `{"error":"…"}` envelope) ⇒ **400**, not 409; the
  fallback never triggered. (Encoded in ADR-16's D-A resolution.)

---

**Planner adjudication on RED-1's refusal semantics (2026-11-16, for cycle-2 review).** The architect's
fix text ("⇒ `FailedGroupID` at the boundary") and its own defect description ("the whole chat request
502s even though the primary is healthy" is part of the *defect*) pull in opposite directions for the
fallback-position case. **Phase1's encoding is ENDORSED:** `FailedGroupID` only for a **direct-named**
decision-lane group; a **fallback-position** entry is **dropped and continued** (loud log +
`decision_misroute` event, **no rotation row written**). Rationale: a fatal 502 on a healthy primary
reintroduces exactly the defect being fixed, and drop-and-continue matches chain semantics elsewhere
in the lane. Pinned by `TestGroup_RewriteGroupEntries_DecisionGroupInChatChain_BoundaryRefusal`
(healthy-primary case does NOT 502).

**Architect W9-G2 (ADR-1 shadow extension): MOOT by D-A.** The leader's D-A ruling was conditional on
shadow participation in effective-kind resolution; phase1 §E8 verified it does **not** (group-row-only
uses; claim-blind kind resolution). Shadow scope stays **OUT** unless the leader re-opens D-A.

**Remaining unfolded (flagged for cycle 2):** the full **T1–T5 vocabulary pin set** (registry key set ≡
gate allow-list; source-literal pin across the 5 surfaces; anthropic-credential-saves /
decision-model-rejected cases) was **not** authorized this tranche — only T5's FE half (V1) landed.
Recommend a small phase-1 addendum in the next tranche, or an explicit reviewer deferral.

---

### ADR-18 — Review cycle-2 rulings (2026-11-16)

**C2-1 — Doctrine-line pin target (LEADER PICK, encoded).** The verbatim doctrine line pins at the
**NEW decision-lane suppression seam file that phase1 Stage 4 creates** — the plan must **name that
file explicitly** at every pin site. `pkg/skipcache/skipcache.go:29-31` is v194's canonical home —
**CITED read-only, NEVER edited** (architect §0 respected). `TestDecision_DoctrineLine_PinnedVerbatimAtSeam`
targets the seam file and **MAY additionally assert skipcache's copy read-only as drift detection.**

**C2-2 — the four unmarked sites.** All four (`phase1:404` incl. the **dead `SetSkipCache` citation** —
zero hits repo-wide at `2b987bf`; `phase3:75`; `phase3:1198`; `phase1:1603-1604`) are superseded by
**ADR-16**: shared instance + **layer-separation fence** is the real invariant. The operator-verification
line at `phase3:1198` must verify **LAYER SEPARATION, not instance separation** — as written, an
operator would chase an impossible invariant and file a false defect.

**C2-3 — abort row R11.** Aligned to the corrected ladder: **tier-1 = fresh-verified live-at-incident;
tier-2 = prior release; the incident path NEVER strips a newer release than the incident requires.**
The v193-only two-release-rollback prescription is dead operative text — superseded.

**C2-4 — fresh-verify scope + dead dcs figure.** The fresh-verify rule extends to every plan-time
deploy/dcs number (`:1189/:1193/:1220/:1357/:1359` class). The dcs "8 commits ahead" figure is **DEAD**
(dcs synced @ `7f89eac`) — replaced by the fresh-verify rule; **R14 fires on MEASURED drift only**
(measure at ship, compare, act on delta).

**C2-5 — Tier-1 table vs command (12 vs 10).** The executable command must assert **exactly** the
table's file set. `win_clear_winner_identity_test.go`: satisfiable-but-unasserted (net-churn is blind
to assertion-body edits) ⇒ **added to the command** + its cell names the clone's file
(`pkg/modelscache/models_group_lane_test.go`). `group_rules_test.go`: **un satisfiable as commanded**
(phase 1 edits it) ⇒ **Tier-1 row deleted** (it was already correctly absent from the command). Sides
fixed: command+table brought to agreement by adding the one and deleting the other — the table shrank
to the command's truth, the command grew to cover the pin.

**T1–T4 — ENCODED, no further deferral (LEADER DECISION).** Four vocabulary pin tests added as NAMED
tests in the phase-1 table with stage assignments (T2/T3/T4/T5-Go; V1/T5-FE already landed). G4
re-derived to **73/5/47** (or as naming derives), set-identity phase1↔phase3 whole-document.

**STAGE 0 — permanent base-currency gate (LEADER APPROVED).** New Stage 0 in phase 1: at
implementation **kickoff**, giter re-anchors the branch to fresh `origin/latest` (it moved AGAIN
during review — `a2952a9` / master `8d434ba`; the CG-4 class recurs); the developer runs a **bounded
pin-reverification sweep** (every BASE-relative pin re-checked, every drift flagged and adjudicated
**before stage work starts**); the anchor-of-record SHA goes into the KV row; the ship-step full-gate
re-run stays **unconditional regardless of merge conflicts**. **The corpus REMAINS `2b987bf`-pinned
for cycle 3 — no re-anchor mid-review.**

**Advisory supersession (optional sweep, recorded here rather than editing the architect's file):**
ADR-17 supersedes the two dangling advisory lines at `architecture-recommendation.md:741` and `:778`
that still recommend the **retracted** `store_group_policy.go`. The architect's file is not edited by
the planner; this ADR is the record that those lines are retracted proposals, not live guidance.

---

### ADR-19 — The FE lane fold MIRRORS the server fold, function-for-function, case-for-case (leader re-ruling, implementation feedback loop)

**Ruling.** The backend landed the canonical server fold — `pkg/models/group_lane.go:46-101`
(`MemberLane` / `GroupLanesOf` / `UnanimousGroupLane`) and
`pkg/store/database/store_group_rewrite.go:369-384` (`inferGroupLane`). The FE **must mirror the
server** — *two answers must not ship*. The FE has implemented that fold; its per-case deltas are the
input, and the corpus's former **E1..E7 expectations are SUPERSEDED** by the server-defined fold
(cited per case in `phase2-plan.md` §6.1).

**E8 — NEW case.** A member of **no lane** (image-gen / group / out-of-vocabulary provider) is
**IGNORED** by the fold (`pkg/models/group_lane.go:52-53`, `:67-71`).

**The one non-mirrorable behavior — ACCEPTED and documented.** The server reads member rows
**directly**; the FE holds **list snapshots**. The FE therefore mirrors the **collection-shaped twin**
at `pkg/ui/frontend/src/components/config/group_lane.go:137-154` — **no new endpoint, no new hook**.
This deviation is documented **in-code** at `pkg/ui/frontend/src/components/config/groupLane.ts:91-113`
and is an **accepted residual** for this release.

**Case re-pinning summary (server is the authority).** **E4 CHANGED** — an unresolvable/vanished
member is **SKIPPED** and the survivors decide; `[decision, vanished] ⇒ decision`; only-vanished ⇒
`null`. **E5 CHANGED** — zero surviving members ⇒ `null` (the row stays visible in the text tab).
**E6 fixture re-pinned** (mixed set + all-vanished). **E7 unchanged.** Plus: the **D2 fixture shifts**
to a genuinely mixed **surviving** set; **D6a extends** (a decision group with a vanished member is
known-lane and still blocked); **D6c's comment is corrected** (unknown = mixed **or** empty surviving
set) with a mixed variant added.

**Effect on the gate.** The canonical FE token list grows **E1..E7 ⇒ E1..E8**: **47 ⇒ 48 FE case
tokens**. The Go ledger is **unchanged** (74 = 64 Pack A + 10 Pack B).

---

### ADR-20 — Three leader rulings (Q1–Q3) + the AC-R10 observability completion (implementation, HEAD `5362ec0`)

**Q1 — the failure-oracle escape's PLACEMENT.** The honest attempt goes to the **FIRST MEMBER IN
POLICY ORDER THAT IS FULLY UNAVAILABLE** — **never** a cache-picked candidate; R1 keeps the pick
policy-stable. **TWO caps, both mandatory:** (a) at most **ONE** honest attempt per **MEMBER**
(per-member latch) and (b) at most **ONE** per **REQUEST**; any **later** fully-unavailable member is
**SKIPPED zero-cost**, because a real upstream attempt is already in flight. **Effect:** an all-marked
group ⇒ the **policy head** takes the honest attempt (ADR-11 preserved); a mixed group ⇒ the **first
fully-unavailable member** takes it; **nothing takes a second one.**
*Code:* `pkg/decision/group.go` — the D-C escape + member latch; the caps are stated at `:401-402`
("Per-member latch + per-request cap … at most one honest attempt per member AND at most one per
request"); the policy-head honest attempt `:241`; the honest attempt on the member's **policy-first
credential** `:427`. *Mutation control:* `pkg/decision/handler_group_test.go:1337` (`limit_class_latch`)
— the second member is marked fully-unavailable at `:1350-1351` and the latch must keep it at **one**
honest attempt (assertion idiom `:416-417`, "… got %d hits").

**Q2 — D-C and AM-4 COMPOSE; neither overrides the other.** Inside a member: **(1)** rotate across
**usable** credentials **per AM-4** (limit-class + upstream 401/403 rotate; **5xx advances** per the
class table; 400/413/415/422 + client-cancel fail-fast) **while usable credentials remain**;
**(2)** when **none remain usable**, the D-C escape grants **exactly one** honest attempt on that
member's **policy-first credential**, subject to Q1's caps; **(3)** if that honest attempt then
**genuinely fails** per the class policy, the **member FAILS** and the loop advances, with the class
table governing the failure's classification. **D-C defines the exhaustion POINT of the within-member
rotation; it never replaces the class table.**

**Q3 — NO WRITE ON MARK-READ.** Layer discipline applies in the **write** direction too: the
**upstream layer is written by credential-failure classification** (the AM-4 class dispatch), **never
by the act of reading a mark**. A read that mutates selection state is a **feedback loop**
(read → write → future selection) and breaks the no-cross-read rule **symmetrically**. If the read
path needs a side effect, it belongs to **the layer's writer**.

**Observability completion (AC-R10).** The **fail-fast** event now carries **`attempted`,
`skipped`, `members_attempted`** — the **SAME field spellings** as the exhaustion envelope
(`pkg/decision/group.go:338-340` vs `:362-364`), single-sourced vocabulary. The fail-fast path
**previously published NO event**; it now publishes **one instance of the EXISTING
`eventTypeDecision`** carrying the counters (leader-accepted: **no new event type, no field
removed/renamed, no semantic change** to existing fields). **Purpose:** make the invariant *"a
suppressed attempt never counts as a failure"* **FALSIFIABLE** — the mutation is now **RED** at
`pkg/decision/handler_group_test.go:1111` ("fail-fast attempted = 2, want 1").

---

### ADR-21 — Known deviation: `pkg/skipcache` carries a PARAPHRASE of the doctrine (RECORDED, NOT FIXED)

**Deviation.** `pkg/skipcache` holds a **paraphrase** of the doctrine line — **not** the byte-exact
line. The **two PINNED copies** — `pkg/decision/suppression.go:8-9` and `pkg/decision/policy.go:17-18` —
are **cmp-identical to each other** and carry the verbatim line.

**Why it is recorded and NOT fixed:** `pkg/skipcache` is **another commission's landed surface** — this
release does not edit it (that would be a cross-commission change inside a file the text lane owns).
**This ADR is the record**: the doctrine line's canonical pinned home for THIS release is the decision
lane's seam file (`pkg/decision/suppression.go:8-9`, mirrored at `pkg/decision/policy.go:17-18`), and
`pkg/skipcache`'s wording is a paraphrase owned by the text-lane commission.

**Effect on the gate:** none — the AC-R9 pin asserts the verbatim line at the **seam file**
(`TestDecision_DoctrineLine_PinnedVerbatimAtSeam`), and the two pinned copies are byte-identical to
each other, so the drift detector stays meaningful. The skipcache paraphrase is **known, accepted, and
not attributed to this commission**.

---

## Not verified

- Nothing was executed: no build, no `go test`, no FE run, no smoke. All breakage statements are static reasoning from code reads.
- `pkg/decision/handler.go` was read in regions (`:100-150`, `:248-312`, `:355-370`, `:425-440`, `:508-548`, `:696-716`), not end-to-end; `:150-250`, `:312-355`, `:370-425`, `:440-508`, `:548-696` were not read line-by-line.
- `pkg/decision/handler_test.go` is 2288 lines; only the import guards (`:40`, `:65`, `:90`) and the named tests quoted in `research-decision.md §7.1` were inventoried.
- `pkg/proxy/handler.go:960-1130` (re-entry loop body) was not read in full; the `ExcludeGroupMember` usage claims rest on grep hits plus `pkg/proxy/handler_groups.go:457-493`.
- Whether a member row's `kind` can be changed in place by `UpdateModel` was verified (no immutability guard found at `pkg/store/database/store.go:1699`/`:2085`) — but the FULL UpdateModel path was not read.
- The FE research cited the PUT decision-provider gate at `pkg/ui/server.go:~1654-1663` from a main-checkout grep; the worktree read gives `:1631-1643` (condition at `:1636`) — the worktree value is used throughout this document.
- The FE research's line numbers carry `[wt]`/`[grep-main]` markers; every FE site cited here was re-verified by direct worktree read.
- The user's actual decision models (ids, internal_model values, credentials) live in the running DB and were not read.
- gzipmw self-scoping to `/fe/api/*` is asserted per the project blueprint, not re-verified line by line in this worktree.
- Live upstream error envelopes for the decision providers were not exercised (only the `live_decision`-tagged tests document them).
- The vertical nav has no in-repo precedent (zero `role="tablist"` usage in `pkg/ui/frontend/src`); the recommended idiom is derived from the top tab bar's class pair, not from an existing vertical nav.
- `pkg/providers`'s import of `pkg/proxy/translator` (`pkg/providers/openai.go:31`) was verified; the exact transitive edge is the only blocker preventing `pkg/decision` from importing `pkg/providers`.

---

*End of ruling. This document is the artifact at `.agents/shared/planning/decision-groups/decisions.md`. Phase 1 (backend) and phase 2 (FE) plans are to be written against the Contracts section (items 1-30).*
