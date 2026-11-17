# Phase 1 — decision-groups: BACKEND implementation plan

> **Commission:** decision-groups (decision models served by a GROUP + the admin
> "Decision Models" container with a vertical `[models] [group]` nav).
> **Phase 1 scope:** BACKEND ONLY (Go). The FE container/nav is Phase 2 and is
> out of this file's scope.
> **Worktree:** `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-decision-groups`
> (branch `feature/decision-groups` @ **`2b987bf94e39abfc9df515cbbadc2bb335dbb47c`**
> == `origin/latest` — **re-anchored 2026-11-16** from `24c51ef865f1e8360c681dd7dd6970587f8b4b10`
> (v194 `fix/cache-layer-separation`; master `fd1649c`). Every repo line-pin in this
> document was read at `24c51ef` unless it carries an *Amendment 5* marker — see
> `## Amendment 5` §E9 for the re-derived pin table).
> **Binding ruling:** `.agents/shared/planning/decision-groups/decisions.md`
> (D-1 … D-8 + the 22 backend contracts). This plan IMPLEMENTS that ruling; it
> does not re-litigate it. Where the ruling is internally inconsistent or
> unimplementable, see `## Escalations` (file:line evidence given) — no silent
> deviation.
> **Planner adjudications (binding):** Q7 approved WITH CONSTRAINTS (the
> `pkg/credclass` extraction is a pure move + delegation, gated); Q8 endorsed
> (a separate `GroupLane(groupID)` method, NOT a field on `GroupRewriteResult`).
> **Research basis:** `research-groups.md`, `research-decision.md`,
> `research-fe-precedent.md` (same directory). Format precedent:
> `group-backup-members/design.md`.
> **Status:** Draft plan — no code, no tests, no config edits, no commits, no
> pushes, no deploys were performed to produce it. Nothing was built or run.

---

## Objective

A group whose members are **decision-kind** models becomes a first-class
**decision group**: it saves through the existing admin model API, it resolves to
its members on `POST /v1/decisions`, and it fails over across those members with
the v193 semantics — exhaustive **pre-commit** fallback (a request may only fail
after every member has genuinely failed THIS request), **demote-not-filter**
ordering (the skip cache reorders candidates, it never excludes them),
**win-clear on the winner's identity**, and a distinct exhaustion envelope
`502 + code=decision_all_members_failed`. The text lane's behaviour is unchanged.

---

## Scope

### In scope (Phase 1 = backend)

| # | Item | Ruling |
|---|------|--------|
| S1 | `pkg/credclass` leaf extraction of the limit-class vocabulary + `pkg/providers` delegation (pure move, gated) | D-4, Q7 |
| S2 | Write-time rule widening: member-kind + homogeneity + lane-aware shadow target (canonical helper + POST + PUT mirrors) | D-1, D-2, contract 1/4 |
| S3 | Request-time kind-parameterized servability predicate + the `GroupLane(groupID)` manager method + the `modelscache` decorator forward | D-2, D-8, contract 2/3/22 |
| S4 | Admin-API parity for decision groups (POST / PUT / helper, all three asserted independently) | D-2, contract 1/4 |
| S5 | The decision lane's in-lane member loop: group detection, per-member row-identity credential resolution, pre-commit write deferral, tried-set, exhaustion, win-clear, marks | D-3, D-4, D-5, D-6, contracts 5–13 |
| S6 | The four `GroupLane` consumers: decision-lane entry, chat misroute guard, Anthropic misroute guard, `/v1/models` filter | D-8, contracts 15/16 |
| S7 | Text-lane regression pins (**no v194 semantic regression** — the fence is re-based on `2b987bf`, not on v193) | D-7, contract 17, AC-R12 (ADR-9) |
| S8 | Feature marker literal for binary verification | task brief |

### Out of scope (explicit fence — do not touch in Phase 1)

1. **Sticky / binding / affinity on the decision lane.** Ruled OUT (D-6). No
   conversation key exists on this lane (`pkg/decision/handler.go:738` — the
   resolver takes one argument; `cmd/main.go:327` passes the model id twice);
   the member conversation key is `""` ⇒ "fresh weighted pick, no binding"
   (`pkg/models/group_rewrite.go:88-90`). The credential layer keeps its
   existing stable per-model key `(memberID, memberID)`.
2. **No schema change, no migration.** `kind TEXT` is unconstrained
   (`migrations/sqlite/030_add_model_kind.up.sql:29`); `members_json` +
   `shadow_model_id` already exist on `models`
   (`migrations/sqlite/031_add_group_fields.up.sql:39-40`). The lane is
   INFERRED, never persisted (D-2).
3. **No new provider.** The decision provider vocabulary `{openai, typesafe,
   openrouter}` is unchanged at all five surfaces — FE constant
   `pkg/ui/frontend/src/components/config/DecisionModelForm.tsx:29`, BE gate
   `pkg/models/config.go:293-300`, POST `pkg/ui/server.go:1171`, PUT
   `pkg/ui/server.go:1636`, registry `pkg/decision/registry.go:49-53`. The
   CREDENTIAL vocabulary (`pkg/models/credential.go:15-60`) is a separate map —
   do not cross-wire them (see `## R3 — provider vocabulary`).
4. **Flat membership stays flat** — G5 group-as-member remains forbidden in
   BOTH lanes (`pkg/models/group.go:176-179`).
5. **Cap 16 stays** — `models.MaxGroupMembers = 16` (`pkg/models/group.go:25`).
6. **No text-lane behaviour change.** The v191–v193 fixes (the anonymous
   `/v1/messages` auth hole, the agentic hidden-member fix, the v193 skip-cache
   ordering law) are untouched. *(Amendment 5 / ADR-16 — the fence is re-based:
   v194 **deliberately changed** the text lane, so the invariant is **"no v194
   semantic regression"** — this release must not move the text lane relative to
   `2b987bf`, not relative to v193. The text-group pins are re-derived against
   v194's behaviour — `## Amendment 5` §E9.1.)*
7. **No FE work** (Phase 2): no `DecisionModelsTab` container, no vertical nav,
   no `GroupMemberEditor` lane parameterization, no `pkg/ui/static/` rebuild.
8. **No new models-manager method beyond the lane query** (D-8): the sweep,
   exclusion, win-clear and decorator forwards are REUSED.
9. **Not this commission:** Q1 (persist a lane column), Q2 (relay the last
   upstream body), Q3 (block kind retyping), Q4 (list decision groups in
   `/v1/models`), Q5 (ARIA tabs), Q6 (FE copy) — all remain as the ruling left
   them.

---

## Ruling conformance map

| Ruling | Where implemented | Stage |
|--------|-------------------|-------|
| D-1 widen (single group entity, no parallel path) | Stage 1–2 (rule widening), Stage 4 (in-lane loop inside `pkg/decision`) | 1, 2, 4 |
| D-2 write-time homogeneity + request-time unanimity inference | Stage 1 (write), Stage 2 (runtime predicate + `GroupLane`) | 1, 2 |
| D-3 in-lane member loop (import guards forbid `pkg/proxy`) | Stage 4 | 4 |
| D-4 demotion + win-clear, vocabulary extracted to `pkg/credclass` | Stage 0 (vocabulary), Stage 4 (marks + win-clear) | 0, 4 |
| D-5 exhaustion = 502 + `decision_all_members_failed` | Stage 4 | 4 |
| D-6 sticky = NO | Stage 4 (empty conversation key), stated in `## R2` below | 4 |
| D-7 scope fence | `## Out of scope` + Stage 5 pins | — |
| D-8 one new manager method + decorator forward + decorator-through test | Stage 2 (`GroupLane`), Stage 5 (decorator-through test) | 2, 5 |
| **ADR-5 … ADR-12 (user ruling R1–R5, OQ-1..OQ-4 closures, RULING-3 per-layer caches)** | **Amendment 1** — policy-only ordering, attempt suppression, per-credential attempts, per-layer caches | **Amendment 1** |

---

## Stage 0-A (kickoff, ADR-18) — base re-anchor + pin-reverification sweep

> **LEADER APPROVED (ADR-18 STAGE 0) — permanent, every implementation kickoff.**
> The review cycle itself watched the base move again (`origin/latest` is now
> `a2952a9`, `origin/master` `8d434ba` — past this corpus's `2b987bf`), so
> base currency is a **gate step**, not a one-time re-anchor.

| # | Task | Acceptance |
|---|------|------------|
| 0A.1 | **giter re-anchors the branch to fresh `origin/latest`** at implementation kickoff | the worktree HEAD == fresh `origin/latest`; the anchor SHA is recorded |
| 0A.2 | **Bounded pin-reverification sweep** — every BASE-relative pin in this document re-checked against the new tree (the §E9 table is the starting inventory) | every drift **flagged and adjudicated BEFORE stage work starts** — no stage begins on an unadjudicated pin |
| 0A.3 | **Anchor-of-record** | the anchor-of-record SHA goes into the KV row for the commission |
| 0A.4 | **Ship-step full gate** | the full-gate re-run stays **unconditional regardless of merge conflicts** |
| — | **Corpus pin** | **this corpus remains `2b987bf`-pinned for cycle 3 — no re-anchor mid-review** (ADR-18) |

---

## Stage 0-B (implementation) — `pkg/credclass` extraction (pure move + delegation) + GATE *(was "Stage 0"; retitled by Amendment 7 / ADR-18 so the kickoff gate owns "Stage 0-A")*

**Why first:** the decision lane needs the limit-class vocabulary but cannot
import `pkg/providers` — `pkg/providers/openai.go:31` imports `pkg/proxy/translator`,
so `pkg/providers` sits transitively under `pkg/proxy`, which the decision
package's transitive-closure import guard bans
(`pkg/decision/handler_test.go:90`). A leaf package with **no internal imports**
is the only way to share one vocabulary (D-4).

| Seam | File:line | What changes |
|------|-----------|--------------|
| New leaf package | `pkg/credclass/classify.go` (NEW) | Stdlib-only (`strings`). Owns the vocabulary + the field-level classifiers over `(status int, errType, errCode, message string)`: `IsRateLimit(...)`, `IsQuota(...)`. Exports the vocabulary tables (moved verbatim from `pkg/providers/interface.go:266-286` — `quotaTypeVocabulary`, `quotaCodeVocabulary`, `quotaMessageVocabulary` — plus the rate-limit code set `{rate_limit, rate_limit_error, rate_limit_exceeded}` and the `rate_limit` type-substring rule from `pkg/providers/interface.go:241-243`) and the `containsAny` helper. **No import of any `pkg/...` package** (the leaf law). |
| Provider delegation | `pkg/providers/interface.go:225` (`IsRateLimitError`) | Keep the exported signature and the `errors.As(err, &pe)` unwrap; extract `pe.StatusCode / pe.ErrorType / pe.ErrorCode / pe.Message` and delegate to `credclass.IsRateLimit`. **Zero signature change, zero behaviour change.** |
| Provider delegation | `pkg/providers/interface.go:348` (`IsQuotaError`) | Same shape → `credclass.IsQuota`. Quota is still checked before rate-limit by the CALLER (`pkg/proxy/cred_failover_class.go:97-108`), not by the leaf. |
| Shared vocabulary test | `pkg/credclass/classify_test.go` (NEW) | The moved vocabulary table (one table, run by both lanes — D-4's "shared test table in the leaf package that both lanes run"). |

**Tasks**

| # | Task | Depends on | Acceptance |
|---|------|-----------|------------|
| 0.1 | Create `pkg/credclass` with the moved vocabulary + `IsRateLimit`/`IsQuota` over the 4-field tuple | — | `pkg/credclass` imports only stdlib (`go list -deps` shows no `pkg/...`); the vocabulary literals are byte-identical to `pkg/providers/interface.go:266-286` |
| 0.2 | Rewrite `pkg/providers.IsRateLimitError` / `IsQuotaError` as delegations | 0.1 | Exported signatures unchanged; `pkg/providers/interface_quota_test.go` + `pkg/providers/ratelimit_classifier_test.go` pass **unchanged** (the gate) |
| 0.3 | Add `pkg/credclass/classify_test.go` (the moved table + the two live-shape cases: "plain `usage limit` stays rate_limit", "`token plan` is status-agnostic") | 0.1 | The leaf's table pins every vocabulary entry |
| 0.4 | **GATE** — run `pkg/providers` + `pkg/proxy` classifier suites before ANY later stage starts | 0.2, 0.3 | `go test ./pkg/providers/... ./pkg/proxy/...` green; **if the extraction requires any behaviour edit, STOP and escalate** (Q7's constraint: the text lane is not a refactor site) |

**Risk / rollback** — Medium / trivially revertable. The only text-lane-adjacent
source touch Phase 1 permits (D-7 item 7). Rollback = revert the two delegation
hunks + delete the leaf.

---

## Stage 1 — Write-time rule widening (`pkg/models/group.go` + doc)

**Design decision that keeps the text lane byte-untouched (D-7):** the member
kind rule and the shadow rule become **lane-parameterized**, and the *chat-lane
wording is preserved verbatim*. Concretely, the validator computes the group's
LANE from its members (unanimity over their normalized kinds) and then words
each error with that lane:

| Situation | Error text | Chat-lane text unchanged? |
|-----------|-----------|----------------------------|
| Member kind not in {chat, legacy `""`, decision} | chat lane: `... must be a chat-kind model (got kind %q)`; decision lane: `... must be a decision-kind model (got kind %q)` | **YES** (identical) |
| Members of two different lanes (chat + decision) | NEW: `... members must all share one kind (chat or decision) — member %q has kind %q` | n/a (new rule) |
| Shadow target of the wrong lane | chat lane: `... must reference a chat-kind model (got kind %q)`; decision lane: `... must reference a decision-kind model (got kind %q)` | **YES** (identical) |
| Shadow target is a group | chat lane: `... must reference a chat-kind model, not a group`; decision lane: `... must reference a decision-kind model, not a group` | **YES** (identical) |

Ordering inside the validator (so the existing single-fault fixtures keep their
current messages):

```
per member: empty model_id → weight>0 → duplicate → existence → not-a-group (G5)
collect each member's normalized lane:  "" → chat, chat → chat, decision → decision, anything else → invalid
if the set of VALID lanes has ≥2 entries            → HOMOGENEITY error (return)
for each member with an INVALID kind:
        wording = lane of the valid set (chat when the set is empty) → the member-kind error
for each member: its normalized lane must equal the group lane (implied by unanimity; asserted anyway)
shadow target: exists → not-a-group → its normalized lane must equal the group lane
```

| Seam | File:line | What changes |
|------|-----------|--------------|
| New shared primitives | `pkg/models/group_lane.go` (NEW) | `type GroupLane string`; `GroupLaneChat = "chat"`, `GroupLaneDecision = "decision"` (D-8: typed constants in `pkg/models`, **no migration**); `func MemberLane(kind string) (GroupLane, bool)` (the `""`→chat normalization); `func UnanimousGroupLane(kinds []string) (GroupLane, bool)` (the single-source-of-truth fold — used by the canonical validator, the two UI mirrors and the runtime inference so the lane can never drift between them). |
| Canonical member rule (G3) | `pkg/models/group.go:180-184` (message at `:183`) | Replace the chat-only check with: invalid-kind check (lane-worded) + the homogeneity fold + the member-lane equality check. |
| Canonical shadow rule (G8) | `pkg/models/group.go:193-198` (messages at `:194`, `:197`) | Lane-worded shadow target checks (the group lane is known by then — the member pass precedes the shadow block in the same function). |
| Canonical doc block | `pkg/models/group.go:111-114` | "members must reference existing chat-kind models" → lane-consistent wording ("chat-kind or decision-kind"), with the `""`-legacy note. |
| GroupMember doc | `pkg/models/group.go:29-46` | The `ModelID` field doc says "EXISTING text (chat-kind) model row" → lane-consistent wording. |
| KindGroup doc | `pkg/models/config.go:117-132` | "served by the normal chat pipeline" → "served by the lane its members belong to (chat or decision)". |
| Provider gate (UNCHANGED) | `pkg/models/group.go:57-61` | `_ = internalProvider` — a group carries no credentials, so no provider gate applies to the group row. **Unchanged** (see `## R3`). |

**Tasks**

| # | Task | Depends on | Acceptance |
|---|------|-----------|------------|
| 1.1 | Add `pkg/models/group_lane.go` (`GroupLane`, `MemberLane`, `UnanimousGroupLane`) | — | `MemberLane("")==chat`, `MemberLane(KindChat)==chat`, `MemberLane(KindDecision)==decision`, `MemberLane(KindImageGen)==(_, false)`; `UnanimousGroupLane(["chat",""])==chat`; `UnanimousGroupLane(["chat","decision"])==(_, false)` |
| 1.2 | Widen `ValidateGroupMembers` (member rule + homogeneity + lane-worded errors) | 1.1 | A decision-member group validates clean; a chat+decision group is rejected with the homogeneity message; an image-gen member still yields `must be a chat-kind model` **byte-identical** to today's text (`pkg/models/group.go:183`) |
| 1.3 | Lane-word the shadow-target rules | 1.2 | A decision group with a chat shadow target → `must reference a decision-kind model`; a chat group with a decision shadow target → `must reference a chat-kind model` (unchanged text) |
| 1.4 | Update the three doc blocks (`group.go:111-114`, `group.go:29-46`, `config.go:117-132`) | 1.2 | Docs match the code; no rule text changes |
| 1.5 | Unit-pin the new rules in `pkg/models/group_rules_test.go` (new functions, existing table untouched) | 1.2, 1.3 | See `## Test plan` rows M1–M5 |
| 1.6 | **Amend the stale package doc** `pkg/decision/doc.go:6-15` — its import-direction prose claims `{models, auth, usage, events}` ONLY, while the real closure already includes `config`, `store/database`, `grouplb`, `skipcache`, `toolrepair`, `credentiallb`, `crypto` (**ADR-14 item 4**) | — | The doc's prose matches the real closure, so the feature does not read as a guard violation from day one, **AND** the second stale claim is fixed: `pkg/decision/doc.go:17-19`'s "One upstream attempt per request (no retry, no fallback, no race coordinator …)" becomes **FALSE** under this commission (per-member credential attempts — ADR-6/A-1 — and group-level member fallback — ADR-5/ADR-6); the doc must describe the loop contract instead (still no speculative/hedge race engine — OQ-3). **The amended prose must not name the banned paths** (the guard is raw text — see `## Amendment 3` §C3; review warning 9) |

**Risk / rollback** — Medium. The rule is a deliberate invariant (G3) being
renegotiated bidirectionally, never relaxed generically (D-1's rejected
alternative). Rollback = revert Stage 1; the DB write path, the UI mirrors and
the JSON backend all route through the one canonical validator, so a revert is
one file (plus the mirrors in Stage 3).

---

## Stage 2 — Request-time predicate + `GroupLane` + decorator forward

**Lane inference (D-2).** The manager infers the lane by **unanimity over the
CURRENT member rows** (never trusted from write time — `UpdateModel` has no
kind-immutability guard, `pkg/store/database/store.go:1699` / `:2085`), then the
predicate re-checks each member against that lane (defence in depth).

| Seam | File:line | What changes |
|------|-----------|--------------|
| Servability predicate | `pkg/store/database/store_group_rewrite.go:291-301` (`isServableGroupMemberBase`, doc block `:284-290`) | Becomes kind-parameterized: `isServableGroupMemberBase(row *models.ModelConfig, lane models.GroupLane) bool`. Chat lane: `row.Kind == "" || row.Kind == KindChat` (unchanged). Decision lane: `row.Kind == KindDecision` — **`""` is NEVER accepted as decision** (contract 3). Health-agnostic by design (unchanged). |
| Sweep call sites | `pkg/store/database/store_group_rewrite.go:164`, `:188`, `:212` | The three `isServableGroupMemberBase(row)` calls now pass the group's inferred lane. The lane is computed ONCE per sweep (below) and threaded through. |
| Sweep entry | `pkg/store/database/store_group_rewrite.go:130-245` (`pickServableGroupMember`) | Computes the group lane once (new private helper `inferGroupLane`) and passes it to the three predicate sites. **An unresolvable lane (mixed kinds) ⇒ `ok=false` ⇒ `FailedGroupID`** (fail closed, never misrouted — D-2). The four-phase ordering logic itself is UNCHANGED (the v193 doctrine is inherited verbatim). *(superseded by Amendment 1 / ADR-5 — for the DECISION lane only: the sweep's mark-influenced Phase-4 sort is NOT consumed; the sweep is the TEXT lane's path and must REFUSE a decision-lane group. The kind-parameterized predicate itself stands.)* *(Amendment 5 / ADR-16 — at `2b987bf` the sweep is already **policy-headed and cache-blind** (`pkg/store/database/store_group_rewrite.go:88-99`, `:281`, `:284-292`): the decision lane **consumes it directly** (I-1 dissolves). What survives of this row is exactly the **kind-parameterized predicate** — `isServableGroupMemberBase` is still chat-only at `:301-311` and is what Stage 2 widens; the manager must stay **cache-blind** (AST pin — `## Amendment 5` §E5).)* |
| Runtime inference (NEW) | `pkg/store/database/store_group_rewrite.go` (new private `inferGroupLane(group *models.ModelConfig) (models.GroupLane, bool)`) | Over the group's CURRENT member rows: each member row is fetched by **direct ID** (`GetModelNative` — claims bypassed, `pkg/store/database/store.go:2552-2567`); a member row that no longer exists is SKIPPED (it is unservable anyway); a DISABLED row still counts for the unanimity (its kind is readable, and a retyped-but-disabled member must still fail closed); the fold uses `models.UnanimousGroupLane`. Mixed ⇒ `("", false)`. |
| NEW manager method | `pkg/store/database/store_group_rewrite.go` (new exported method on `*ModelsManager`) | `func (m *ModelsManager) GroupLane(groupID string) (models.GroupLane, bool)` — resolves the group row by direct ID and returns the inferred lane. **OPTIONAL interface** (NOT on `models.GroupRewriter`) so the ~11 structural fakes stay valid — the house shape (D-8; precedent `pkg/modelscache/models.go:1063-1085`). |
| Decorator forward | `pkg/modelscache/models.go` (new method, next to the `ClearGroupMemberCooldown` forward at `:1079-1085`) | `func (c *CachedModelsConfig) GroupLane(groupID string) (models.GroupLane, bool)` — structural assertion on the inner manager, `("", false)` on a miss, plus the **CRITICAL-FIX comment** cloned from `:1063-1085` (the production `conf.ModelsConfig` is `*CachedModelsConfig` — `CACHE_LAYER_ENABLED` default ON, `cmd/main.go:182-188` — so a missing forward is a SILENT no-op in production while raw-manager tests stay green: the `modelscache-decorator-forward-trap`). |

**Tasks**

| # | Task | Depends on | Acceptance |
|---|------|-----------|------------|
| 2.1 | Parameterize `isServableGroupMemberBase` + thread the lane through the sweep's three call sites | 1.1 | A chat group's behaviour is byte-identical to today (the predicate's chat arm is unchanged); a decision group's members are servable; a decision group with a `""`-kind member is NOT servable |
| 2.2 | Add `inferGroupLane` (CURRENT member rows, direct-ID, unanimity) + wire it into `pickServableGroupMember` | 2.1 | A mixed-kind group ⇒ `FailedGroupID` (fail closed); a retyped member (chat→decision in a 2-member group) ⇒ mixed ⇒ unservable |
| 2.3 | Add the exported `GroupLane(groupID)` method on `*ModelsManager` | 2.2 | `(decision, true)` for a decision group; `(chat, true)` for a chat group; `("", false)` for a mixed group / a non-group / an unknown id |
| 2.4 | Add the `CachedModelsConfig.GroupLane` forward + the CRITICAL-FIX comment | 2.3 | The decorator satisfies `interface{ GroupLane(string) (models.GroupLane, bool) }`; the decorator-through test (Stage 5) proves the effect reaches the inner manager |
| 2.5 | Extend the sweep's `GroupRewriteResult` reporting? **NO** — Q8 endorsed the separate method; `models.GroupRewriteResult` (`pkg/models/group_rewrite.go:14-77`) stays **byte-unchanged** | — | `git diff --stat pkg/models/group_rewrite.go` is empty; the text lane's golden/byte-identity assertions are untouched |

**Q8 rationale (recorded per the planner's instruction):** the text lane shares
`GroupRewriteResult` and repo tests assert on it (golden/byte-identity
assertions, e.g. `pkg/proxy/handler_groups_test.go:400`); mutating a shared
struct risks text-lane test churn and violates the byte-untouched fence
(D-7), whereas an **additive method** is invisible to the text lane and its
decorator pair is already budgeted by D-8.

**Risk / rollback** — Medium/High (this is the semantics core). Rollback =
revert Stages 1+2 together (the write rule and the runtime rule are one
change set; a half-revert leaves a decision group unservable, which is
fail-closed but confusing).

---

## Stage 3 — Admin API mirrors (POST / PUT / helper parity)

The repo's parity discipline asserts **each site independently**
(`pkg/ui/kind_rules_parity_test.go` — helper ≡ POST ≡ PUT, three separate
assertions per case).

| Seam | File:line | What changes |
|------|-----------|--------------|
| POST member mirror | `pkg/ui/server.go:942` (block `:912-945`) | Lane-worded member-kind error + the homogeneity check, using the shared `models.MemberLane` / `models.UnanimousGroupLane` primitives. The chat-lane wording stays byte-identical. |
| POST shadow mirror | `pkg/ui/server.go:959` (group), `:963` (kind) | Lane-worded shadow errors. |
| PUT member mirror | `pkg/ui/server.go:1444` (block `:1417-1450`) | Same as the POST mirror. |
| PUT shadow mirror | `pkg/ui/server.go:1461` (group), `:1465` (kind) | Same as the POST mirror. |
| Canonical helper | `pkg/models/group.go` (Stage 1) | The reference implementation the parity test compares against. |
| DB write path | `pkg/store/database/store.go:2112-2117` (`validateGroupWrite` → `models.ValidateKindRules` + `models.ValidateGroupMembers`) | **Unchanged** — it already routes every group write through the canonical validator. The group write passes `""` as `internalProvider` (`:2113`) ⇒ no provider gate on the group row (R3). |

**Tasks**

| # | Task | Depends on | Acceptance |
|---|------|-----------|------------|
| 3.1 | Mirror the member-kind + homogeneity rules at POST | 1.2 | The POST mirror's error text for an image-gen member is byte-identical to today's (`pkg/ui/server.go:942`); a mixed-kind group POSTs 400 with the homogeneity message |
| 3.2 | Mirror the shadow rules at POST | 1.3, 3.1 | A decision group POSTed with a chat shadow target ⇒ 400 `must reference a decision-kind model` |
| 3.3 | Mirror both at PUT | 3.1, 3.2 | PUT behaves identically to POST for the same payloads (the parity test asserts the three sites independently) |
| 3.4 | Parity pins for the decision lane | 3.1–3.3 | New parity test function (helper ≡ POST ≡ PUT) for: decision members accepted; chat member in a decision group rejected; decision member in a chat group rejected; mixed-kind rejected; lane-matched shadow accepted; lane-mismatched shadow rejected |

**Risk / rollback** — Low/Medium. The mirrors are inline duplicates by house
design ("The mirror is intentionally inline-duplicated (PUT carries an
identical copy) per the T1.2.3 design; the parity test is the drift lock",
`pkg/ui/server.go:855-863`). Rollback = revert the mirror hunks (the canonical
validator keeps the DB path honest).

---

## Stage 4 — The decision lane's in-lane member loop

**Import-guard law (D-3):** `pkg/decision` may NOT import `pkg/proxy`
(`pkg/decision/handler_test.go:40` — direct import ban; `:90` — transitive
closure ban). The lane therefore consumes the manager's seams **through the
interfaces in `pkg/models`** and never re-implements the ordering.

> ⚠️ **IMPLEMENTER TRAP (ADR-14 item 2) — READ BEFORE WRITING ANY FILE IN THIS
> PACKAGE.** The `pkg/decision` import guard scans **every `.go` file in the
> package directory, `_test.go` INCLUDED**, as **RAW TEXT** — a **comment that
> merely QUOTES a banned path FAILS the guard** (ADR-14's negative control). So
> the doctrine-line pin, the layering documentation, every seam comment **and
> every test comment** written inside `pkg/decision` must refer to the banned
> subtrees as *"the banned subtrees"* — **never the literal import paths**. No
> AST precision here: raw text only. See `## Amendment 3` §C3.
>
> ⚠️ **This stage's ordering, advance, win-clear, marks and exhaustion accounting
> are SUPERSEDED by Amendment 1 / ADR-5 (R1/R2), ADR-6 (R3), ADR-11 (OQ-1..OQ-4)
> and ADR-12 (RULING-3).** Read **Amendment 1 §A2–§A5** as the binding text;
> the tables below are retained for the record and are marked in place. The
> re-derived seams are in Amendment 1 §A5; the re-derived loop is in §A5; the
> re-derived tests are in §A16.

### 4.1 Handler seams (new, all optional / nil-safe)

| Seam | File:line | What changes |
|------|-----------|--------------|
| Handler fields | `pkg/decision/handler.go:104-111` | Add `modelsConfig models.ModelsConfigInterface` (the GroupRewriter + GroupLane assertions), `skipCache *skipcache.Cache` (nil-safe — `pkg/skipcache` methods are nil-receiver-safe, `pkg/skipcache/skipcache.go:87-181`), and the group-related seams below. |
| Handler config seam | `pkg/decision/handler.go:116-118` (`configGetter`) | **No `pkg/config` change.** The skip-cache TTL arrives as an **injected seam**: `skipCacheTTL func() time.Duration`, set from `cmd/main.go` (where `credLB` / `stickyEngine` / `modelsConfig` are already in scope — `cmd/main.go:137` / `:144` / `:182-188`), reading the same config-owned window the proxy reads from its snapshot (`pkg/proxy/handler.go:101`; the field is `pkg/config/config.go:120`, default `:255`). The local `configGetter` interface is **UNCHANGED** and `pkg/config` gains **NO** new interface member — the config getter is the FALLBACK only if the seam proves awkward at wiring time (planner adjudication E-3). Nil-safe / fail-SOFT: an absent seam ⇒ the TTL falls back to a fixed default window (`skipcache.DefaultTTL`) and the demotion path still runs — never panic, never skip the mark. |
| Setters | `pkg/decision/handler.go:737-766` (next to `SetModelResolver` / `SetCredentialResolver`) | `SetModelsConfig(models.ModelsConfigInterface)`, `SetSkipCache(*skipcache.Cache)`. Nil-safe wrappers mirroring `safeResolveModel` / `safeResolveCredential` (`:754-766`). |
| Group detection | `pkg/decision/handler.go:265-277` | The `!model.IsDecision()` → 400 `decision_misroute` gate is **EXTENDED, not widened blindly**: `model.IsDecision()` → normal path; `model.IsGroup() && lane == decision` → the group branch; **everything else** (chat, image-gen, unknown kinds, a group whose lane is chat or unresolvable) → the existing 400 `decision_misroute` (the existing tests at `pkg/decision/handler_test.go:692` and `pkg/decision/proxy_handler_decision_models_test.go:156` / `:188` stay green — the chat and image-gen 400s are NOT relaxed). |
| Lane query | `pkg/decision/handler.go` (new private helper) | `h.groupLane(groupID)` → structural assertion `interface{ GroupLane(string) (models.GroupLane, bool) }` on `h.modelsConfig`; **absent seam ⇒ loud log + fail closed to 400 `decision_misroute`** (D-8). |
| Outcome extension | `pkg/decision/converter.go:28-31` | `Outcome` gains additive fields: `UpstreamStatus int` (the raw upstream HTTP status) + `ErrorType / ErrorCode / ErrorMessage string` (the error-envelope fields BOTH adapters already parse — `pkg/decision/openaidecision.go:162-175`, `pkg/decision/typesafe.go:432-444`). `RelayStatus`/`Reason` unchanged; the `UpstreamProvider` interface signature (`pkg/decision/converter.go:88`) is **unchanged** (see `## Escalations E-4`). |
| Upstream-status stamp | `pkg/decision/handler.go:492` (the `InspectResponse` call site) | After the adapter returns, the handler stamps `outcome.UpstreamStatus = resp.StatusCode` (the authoritative source — the adapter never sees the status: `InspectResponse(respBytes, req)` takes no status, `pkg/decision/converter.go:88`). |
| Commit point | `pkg/decision/handler.go:517` (PassAsIs), `:541`/`:543` (PromoteToError) | The member loop OWNS this block: it runs **only for the winner**. No write happens before a winner exists (contract 6). |
| Pre-commit attempt body | `pkg/decision/handler.go:299-505` (steps 8–14) | Extract the per-attempt body into a **non-writing** helper (`attemptDecisionMember`) returning a typed result (credential, provider, typed response, upstream status, error type/code/message, failure reason). The non-group path calls it and then performs today's writes **in today's order** (byte-identical non-group behaviour — D-3's byte-identity verification). The group path calls it in a loop and writes only for the winner. |
| Metering | `pkg/decision/handler.go:554-556`, `:609-641` | Success-only (unchanged). For a group request the metered model id is the **served member's row id** (contract 14 — the decision lane has no `RouteModel` stamp, so the member row IS the metered row); the **client-facing group id** goes into the decision event (`model` field) plus a `member` field. The non-group path meters the client-facing id (unchanged). Failure paths write NO usage row (unchanged, `:606-608`). |
| Win-clear | `pkg/decision/handler.go` (new helper, called between `:548` and `:554`) | Mirrors `clearWinnerUpstreamSkip` (`pkg/proxy/handler_helpers.go:867-886`) but keyed by the **WINNER**: `skipCache.Clear(ScopeCredential, cred.CredentialID, wireModel)`, `skipCache.Clear(ScopeProvider, lower(cred.Provider), wireModel)`, and the OPTIONAL `interface{ ClearGroupMemberCooldown(groupID, memberID string) }` assertion → the decorator forward (`pkg/modelscache/models.go:1079-1085`) → the manager seam (`pkg/store/database/store_group_rewrite.go:439-444`) → `grouplb.ClearCooldown` (`pkg/grouplb/engine.go:1274`). The identity is the **winner's** (`memberID` + `cred.CredentialID` + `cred.InternalModel`), never the picked head (precedent + comment `pkg/proxy/handler.go:905-920`). *(superseded by Amendment 1 / ADR-12 — the decision lane writes no grouplb member cooldown, so `ClearGroupMemberCooldown` leaves this lane's path; the win-clear now targets the decision lane's OWN MODEL + UPSTREAM layer caches, keyed by the winner's identity.)* |
| Marks | `pkg/decision/handler.go` (new helper, called on a failed member attempt) | Mirror `markUpstreamSkipLocked` (`pkg/proxy/race_coordinator.go:2088-2111`): **only on a limit-class failure** (quota first, then rate-limit — the same order as `classifyCredFailure`, `pkg/proxy/cred_failover_class.go:97-108`): `skipCache.Mark(ScopeCredential, cred.CredentialID, wireModel, ttl)` + `skipCache.Mark(ScopeProvider, lower(cred.Provider), wireModel, ttl)`. `ttl` = the config-owned `UpstreamSkipCacheTTL`. **No mark** on 5xx / transport errors / upstream 401/403 / client-attributable 4xx. *(superseded by Amendment 1 / ADR-12 — the marks move to the decision lane's OWN per-layer caches: the UPSTREAM layer (credential failures) and the MODEL layer (model-attributable failures); the text lane's global `*skipcache.Cache` is not shared with this lane.)* |
| Exhaustion | `pkg/decision/handler.go` (new) | 502 + `openAIErrorWithCode(w, 502, "all decision group members failed (N attempted)", "decision_all_members_failed")` (the helper at `:709-714`). No usage row. Exactly ONE decision event with `outcome=all_members_failed`. The last upstream body is NOT relayed (D-5). *(superseded by Amendment 1 / ADR-5, ADR-6 — the 502 fires only after every **member × credential** was genuinely **attempted and failed**; suppressed attempts never count as failures and are reported separately in the event.)* |
| Event | `pkg/decision/handler.go:253-277` (the event vocabulary site) | The exhausted request publishes ONE event with `outcome=all_members_failed`, `model` = the client-facing group id, `members_attempted` = N. |

### 4.2 The loop (pseudo-shape — the plan, not the code)

```
lane, ok := h.groupLane(model.ID)                 // optional seam; absent ⇒ 400 decision_misroute
if !ok || lane != models.GroupLaneDecision { -> 400 decision_misroute (unchanged envelope) }
gr, ok := h.modelsConfig.(models.GroupRewriter)    // optional seam; absent ⇒ 400 decision_misroute (loud log)
tried := map[string]bool{}
candidate, first := "", true
for attempt := 0; attempt <= models.MaxGroupMembers; attempt++ {     // cap = 16 (pkg/models/group.go:25)
    if first {
        res := gr.RewriteGroupEntries([]string{model.ID}, "")        // conversation key "" ⇒ NO binding (D-6)
        if res.FailedGroupID != "" { break }                        // no servable member ⇒ exhaustion (N=0)
        candidate, first = res.MemberID, false
    }
    if candidate == "" || tried[candidate] { break }                // the offer-walk guard
    tried[candidate] = true
    outcome := h.attemptDecisionMember(candidate, &req, rawBody, r)  // steps 8-14, NO writes
    if outcome.ok { winner = candidate; break }                     // ONE commit, at the winner
    if limitClass(outcome) { h.markLimit(outcome) }                 // demote, never exclude
    next, pool, avail := gr.ExcludeGroupMember(model.ID, "", candidate, ttl)   // genuine-failure bookkeeping
    if !avail || next == "" { break }                               // no alternative ⇒ exhaustion
    candidate = next                                                // the sweep's head (same authority)
}
if winner == "" { -> 502 decision_all_members_failed (N = len(tried)); one event; no usage row }
-> win-clear(winner) ; commit (the winner's response) ; meter(winner) ; event(group id + member)
```

**Invariants this shape guarantees**

- **Exhaustive pre-commit** (contract 6): every write happens after the loop,
  only for the winner; every attempt before it leaves the writer untouched.
- **Order from the manager, never re-implemented** (contract 7): the first pick
  is `RewriteGroupEntries`'s head (the four-phase sweep,
  `pkg/store/database/store_group_rewrite.go:130-245`); every later pick is
  `ExcludeGroupMember`'s head — which internally re-runs the SAME sweep
  (`pkg/store/database/store_group_rewrite.go:420`). Offer and rebuild can
  therefore not diverge.
- **Tried-set + cap** (contract 8): `tried` is request-scoped; the loop is
  bounded by `models.MaxGroupMembers` (16).
- **Row-identity credentials** (contract 9): the member's credential is
  resolved through the EXISTING seam `safeResolveCredential(memberID)` →
  `modelsConfig.ResolveInternalConfigWithAffinity(memberID, memberID)`
  (`cmd/main.go:324-328`) → the manager resolves by **direct ID**
  (`GetModelNative`, claims bypassed — `pkg/store/database/store.go:2552-2567`).
  **No `GetModel` call** — the claims-first lookup
  (`pkg/modelscache/models.go:453-463`) would redirect a shadow-claimed member
  back to the group row and self-loop.
- **No sticky** (contract 10, D-6): every `RewriteGroupEntries` /
  `ExcludeGroupMember` call passes `""` as the conversation key ⇒ a fresh
  weighted pick per request, no binding stored.
- **Demote, never filter** (contract 11): the marks are written by the lane;
  the SWEEP consumes them as ordering inputs (`memberDemotionExpiry`,
  `pkg/store/database/store_group_rewrite.go:273-282`) ⇒ a benched member is
  demoted, never removed. All-marked ⇒ the sweep's Phase-4 honest attempt
  (`pkg/store/database/store_group_rewrite.go:229-243`).
- **No cooldown write on a benched pick**: the lane writes a grouplb cooldown
  ONLY through `ExcludeGroupMember` after a **genuine failure**; a demotion
  (a mark) never produces a cooldown write (the sweep's demotion path writes
  none — `pkg/store/database/store_group_rewrite.go:168-173`).
- **Win-clear identity** (contract 12): the WINNER's member id + credential id
  + internal model; the failed head's cooldown survives.

> ⚠️ *(superseded by Amendment 1 / ADR-5, ADR-6, ADR-11, ADR-12 — the loop below
> consumes the sweep's mark-influenced ordering and advances member-by-member.
> The re-derived loop is Amendment 1 §A5: POLICY-ONLY ordering, attempt
> SUPPRESSION, per-credential attempts, OQ-1's policy-head honest attempt, and
> exhaustion counted over genuinely attempted member × credential pairs.)*

### 4.3 The other three `GroupLane` consumers (D-8)

| Seam | File:line | What changes |
|------|-----------|--------------|
| Chat misroute guard | `pkg/proxy/handler.go:576-582` | **ADDITIVE, lane-gated**: after the existing `IsDecision()` guard, a **group** whose inferred lane is `decision` is rejected with the same 400 `decision_misroute` envelope (same code string, same envelope family — the Anthropic route already emits the same code string, `pkg/proxy/handler_anthropic.go:272-279`). The guard runs BEFORE the group rewrite (`pkg/proxy/handler.go:669`), so a decision group can never reach the chat executor. **Chat groups are unaffected** (their lane is `chat`). See `## Escalations E-1`. |
| Anthropic misroute guard | `pkg/proxy/handler_anthropic.go:272-279` | Same additive guard, Anthropic envelope family (`models.NewAnthropicError`), BEFORE the Anthropic rewrite (`pkg/proxy/handler_anthropic.go:432`). |
| `/v1/models` filter | `pkg/proxy/handler.go:349-352` | A group whose inferred lane is `decision` is EXCLUDED from the OpenAI-compatible listing (contract 15). Chat groups stay listed (today's behaviour — `pkg/proxy/handle_models_group_test.go`). The shadow-claim hide loop (`:336-348`) is unchanged (a decision group's shadow target is itself a decision row ⇒ already excluded). *(Amendment 6 / ADR-17 **RED-4** — folded per the architect's W9: **(i)** the lane derivation folds over **ALL models** (`GetModels()`), not `GetEnabledModels()` — the manager's inference counts disabled members, so an enabled-only fold would re-lane a group with a disabled decision member (architect G7); **(ii)** pinned by the ThroughCachedConfig effect test (a group with a disabled decision member is still excluded); **(iii)** the "groups STAY listed" comment (`handler.go:329-332` @24c51ef) becomes false and is updated in the same commit (G10); **(iv)** the FE contract is stated explicitly: `?kind=group` returns **mixed lanes** (`pkg/ui/server.go:785-787`) — the FE tabs must split by lane (**Phase 2**).)* |
| Shared helper | `pkg/proxy/handler_helpers.go:867-886` (next to `clearWinnerUpstreamSkip`) | `func groupLaneOf(modelsCfg models.ModelsConfigInterface, groupID string) (models.GroupLane, bool)` — the structural-assertion helper (the house pattern, `pkg/proxy/handler_helpers.go:879-885`). Unknown lane ⇒ the group is NOT excluded (today's behaviour preserved). *(ADR-15 **W-fail-open** — fail-open stays for genuinely unknown shapes, BUT a group inferred as **chat** that contains **≥1 decision member** must NEVER silently serve: loud **400 `decision_misroute` + a telemetry event**. D-2's write-time homogeneity makes the shape unrepresentable at write time; this is the runtime defence-in-depth net. The helper therefore also scans the group's member rows for a decision-kind member before serving.)* *(Amendment 6 / ADR-17 **RED-3** — the gate must be provably wired on **ALL FOUR** consumers: **compile-time `var _` assertions** at every consumer binding `*modelscache.CachedModelsConfig` to the seam type (so a removed decorator forward breaks the BUILD), in `pkg/proxy/handler.go` (chat guard + `/v1/models`), `pkg/proxy/handler_anthropic.go`, and `pkg/decision/handler.go`; plus **effect tests through the CACHED config** for each (the three `…_ThroughCachedConfig` tests). **The wiring is pinned to the decorator, never the raw `modelsMgr`** (architect R3).)* |

**Tasks**

| # | Task | Depends on | Acceptance |
|---|------|-----------|------------|
| 4.1 | Extract the non-writing per-attempt helper; keep the non-group path byte-identical | 0.1 | Every existing `pkg/decision` test passes UNCHANGED (statuses, bodies, event order) — the non-group decision path is byte-identical to the pre-change path (D-3's byte-identity verification) |
| 4.2 | Add the handler seams (`modelsConfig`, `skipCache`, the TTL getter) + the nil-safe wrappers | 4.1 | A handler built without the new seams behaves exactly as today (nil-safe) |
| 4.3 | Extend `Outcome` with `UpstreamStatus` + the error-envelope fields; stamp the status at the `InspectResponse` call site | 4.1 | `Outcome` stays backward compatible; both adapters populate the error fields on the error path |
| 4.4 | Implement the group branch (detection → lane → rewrite → loop → commit) | 4.2, 4.3, 2.4 | A single-member decision group returns that member's typed answer byte-identically to the single-model path; a two-member group with A failing serves B |
| 4.5 | Implement the marks (limit-class only) + the win-clear (winner's identity) | 4.4 | A quota-failed member is benched and the next request picks the other member first; the winner's marks + cooldown are cleared; the failed head's cooldown survives |
| 4.6 | Implement the exhaustion envelope + the single event + no metering row | 4.4 | 502 + `code=decision_all_members_failed` + `N attempted` in the message; zero usage rows; exactly one `all_members_failed` event |
| 4.7 | Add the three proxy consumers (chat guard, Anthropic guard, `/v1/models`) | 2.4, 4.4 | A decision group on `/v1/chat/completions` ⇒ 400 `decision_misroute` with **zero upstream hits**; same on `/v1/messages`; a decision group is absent from `/v1/models`; a CHAT group is unchanged on all three |
| 4.8 | Wire the seams in `cmd/main.go` | 4.2 | `cmd/main.go:320-328` gains `SetModelsConfig(modelsConfig)` + `SetSkipCache(upstreamSkipCache)` — the **same** skip-cache instance the manager reads (`cmd/main.go:240-241`), otherwise the marks would never demote | *(SUPERSEDED by Amendment 5 / ADR-16 + Amendment 7 / ADR-18 (C2-2) — the instance is **SHARED** (process-wide), not "the manager's"; and the `SetSkipCache` citation is **DEAD**: the manager seam was **removed by v194** — zero hits repo-wide at `2b987bf`, the manager is cache-blind by design (`store_group_rewrite.go:277-283`). The corrected wiring: `SetModelsConfig(modelsConfig)` + `SetUpstreamSkipCache(upstreamSkipCache)` + `SetModelSkipCache(modelSkipCache)` — the **process-wide** instances of `cmd/main.go:242` / `:249` (wired to the proxy at `:255-256`), handed to the decision handler unchanged.)* |
| 4.10 | ~~**Separate skip-cache instance** (ADR-15 / CG-2)~~ | 4.5 | *(SUPERSEDED by Amendment 5 / ADR-16 — the instance is **SHARED** (process-wide), not separate: upstream-layer health is per-credential, and the landed sweep is cache-blind so no ordering leak survives. The lane reads the shared `pkg/skipcache` + `pkg/modelcache` instances; the fence is LAYER separation. The pin becomes `TestDecision_Group_CacheLayerSeparation_SharedUpstreamInstanceOneAnswer`.)* |
| 4.11 | **Fail-open hardened** (ADR-15 W-fail-open) | 4.7 | A group inferred as chat containing ≥1 decision member NEVER silently serves — loud 400 `decision_misroute` + a telemetry event. Pinned by `TestDecision_Group_ChatInferredWithDecisionMember_NeverSilentlyServes` |
| 4.9 | **Write every in-package comment/doc layer-safe** (ADR-14 item 2) | 4.4 | No file inside `pkg/decision` — including `_test.go` — quotes a banned import path, even in a comment or a doc block; the doctrine-line seam, the three layer files and every seam comment use "the banned subtrees" instead. A raw-text scan of the package dir shows zero literal occurrences |

**Risk / rollback** — High (the bulk of the work). Rollback = revert Stage 4
(+ its `cmd/main.go` wiring); the earlier stages are independent of it.

---

## Stage 5 — Tests + regression pins

See `## Test plan` (full table) and `## Known breakage sites` (per-site
verdict). Every NEW manager method ships with its **decorator forward + a
decorator-through test** (R5 / D-8 / contract 22) — the merge gate.

---

## Stage 6 — Feature marker for binary verification

| Seam | File:line | What changes |
|------|-----------|--------------|
| Marker literal | `pkg/decision/handler.go` (the exhaustion envelope) | The machine code **`decision_all_members_failed`** is the distinctive, greppable marker. It is unique in the tree (no false hits) and carries **no Tailwind-class false-positive risk** (unlike `sticky`, which the image-verification harness must never `grep -ci` — the blueprint's warning). |

**Verification recipe (registry-only image inspection, per the house harness):**

```sh
# after the image is pulled and its binary extracted:
strings <binary> | grep -c 'decision_all_members_failed'    # MUST be >= 1
```

Do **not** use `strings | grep -ci sticky` (Tailwind false hits — the house
lesson). Use `grep -c` with the exact literal.

**Marker NEGATIVE CONTROL (pinned by Amendment 2 / ADR-13:836-841):** the string
`decision_all_members_failed` must be **ABSENT from the live v193 binary** —
verifiable against the `~/llmproxy-verify/verify_image.sh` cache for the **v193
per-SHA tag** (per-SHA tags are immutable ⇒ the cache entry is safe; moving tags
go stale) — and **PRESENT in the new build**. No Tailwind / static-UI false-hit
classes: `grep -ci sticky` stays forbidden.

---

## Coupling map

> ⚠️ *(Amendment 1 / ADR-5, ADR-6, ADR-12 — Stage 4's couplings change: the decision lane now depends on the GROUP-layer policy cache and the credential engine (`pkg/credentiallb`, import-guard-legal per ADR-6), and NOT on the manager's sweep ordering; Stage 2's predicate becomes the manager-side defence in depth only. See Amendment 1 §A2, §A5, §A17.)*

| Stage | 0 | 1 | 2 | 3 | 4 | 5 | 6 |
|-------|---|---|---|---|---|---|---|
| **0** credclass | — | independent | independent | independent | **blocks 4.5** (the lane classifies) | independent | independent |
| **1** write rules | independent | — | **tight** (the lane vocabulary must match the write rule) | **tight** (the mirrors copy the rule) | **tight** (a decision group must be writable) | independent | independent |
| **2** predicate + `GroupLane` | independent | **tight** | — | loose (the mirrors do not read the lane) | **tight** (the loop + the 3 proxy consumers read the lane) | independent | independent |
| **3** admin mirrors | independent | **tight** | loose | — | loose (a group must be creatable before it is served) | independent | independent |
| **4** decision loop | **tight** | **tight** | **tight** | loose | — | independent | **tight** (the marker is produced by 4.6) |
| **5** tests | independent | independent | independent | independent | independent | — | independent |
| **6** marker | independent | independent | independent | independent | **tight** | independent | — |

**Critical path:** 0 → 1 → 2 → 4 → 5 (with 3 in parallel with 1/2, gated by 1).

---

## Risks

| # | Risk | Severity | Likelihood | Mitigation |
|---|------|----------|------------|------------|
| R1 | The decorator forward for `GroupLane` is missed ⇒ the seam silently no-ops in PRODUCTION while raw-manager tests pass green (the `modelscache-decorator-forward-trap`) | **High** | Medium | The decorator-through test is a **merge gate** (Stage 5); the forward carries the CRITICAL-FIX comment (precedent `pkg/modelscache/models.go:1063-1085`); the decision lane **fails closed** (400 `decision_misroute`) when the seam is absent, so a missing forward is loud, not silent |
| R2 | The chat/Anthropic guards are missed ⇒ a decision group is served on a chat route (a real misroute: the decision member's `internal_model` would be sent to a chat upstream) | **High** | Medium | The guards are Stage-4 tasks with their own tests (zero upstream hits asserted); the guard runs BEFORE the rewrite (`pkg/proxy/handler.go:576` vs `:669`); the manager's sweep is lane-aware, so the chat lane would pick a decision member — the guard is the only barrier, hence its test is a merge gate |
| R3 | The lane inference disagrees between write time and request time (e.g. a member retyped between the two) | Medium | Medium | Request-time inference uses the CURRENT rows (never the write-time value) + the parameterised predicate re-checks per pick (D-2's defence in depth); a mixed group is unservable (fail closed) and is logged + evented |
| R4 | The deferred-write discipline breaks (a premature `w.WriteHeader` inside the loop) | **High** | Medium | The loop owns the write block (`:508-548`) and only executes it for the winner; a test asserts **zero** writes before the winner's attempt and exactly one total |
| R5 | The decision lane marks a cache instance the manager does not read ⇒ demotion never happens | Medium | Low | `cmd/main.go` wires the SAME `upstreamSkipCache` instance into the manager (`:240-241`) and the decision handler (Stage 4 task 4.8); the demotion test asserts the next request picks the other member | *(superseded by Amendment 4 / ADR-15 (CG-2) — the decision lane's skip-cache instance is **SEPARATE** and **never shared** with the manager's / text lane's instance (Amendment 1 §A5; ADR-9's byte-untouched fence). The "SAME instance" wiring this row prescribed is exactly the cross-lane mark-leak AM-2 exists to prevent: a decision-lane mark would demote a TEXT-lane member. The demotion this risk describes now happens inside the decision lane's OWN instance (the lane reads it directly), so the risk is retired rather than merely re-scoped. Pinned by `TestDecision_Group_SkipCacheInstance_NotSharedWithTextLane` — instance isolation, distinct from the layer-isolation pin.)* *(SUPERSEDED AGAIN by Amendment 5 / ADR-16 — the "**SEPARATE, never shared**" basis is **void**: the landed sweep is **cache-blind** (`pkg/store/database/store_group_rewrite.go:281`, `:284-292`), so a mark cannot leak into ordering, and upstream-layer health is **per-credential, not per-lane** — the process-wide instance gives the two lanes **ONE answer** (`pkg/skipcache/skipcache.go:4-17`; `cmd/main.go:242`, `:249`, `:255-256`). The decision lane consumes the **shared** instance; the fence is **LAYER separation**. The Wave-A pin test is WITHDRAWN — see `## Amendment 5` §E2, §E11.)* |
| R6 | The `pkg/credclass` extraction changes a classifier's behaviour (a missed vocabulary constant) | Medium | Low | The extraction is a pure move + delegation; the existing classifier suites are the **gate** (Stage 0 task 0.4); a shared vocabulary table lives in the leaf and both lanes run it; Q7's constraint forbids behaviour edits (STOP + escalate instead) |
| R7 | The injected skip-cache TTL seam adds one parameter to the decision handler's wiring and is set in `cmd/main.go` — **no `pkg/config` change** | Low | Medium | The seam is nil-safe and fails SOFT: an absent seam falls back to a fixed default window and the demotion path still runs (never panic, never skip the mark). `pkg/config` gains NO interface member — the boundary stays tighter than the original escalation; the config getter remains the fallback only if the seam proves awkward at wiring time (planner adjudication E-3) |
| R8 | The `Outcome` extension changes the adapter contract | Low | Medium | The `UpstreamProvider` interface signature is UNCHANGED (`pkg/decision/converter.go:88`); only the `Outcome` struct gains additive fields; the existing adapter tests pass unchanged — flagged as `## Escalations E-4` |
| R9 | Text-lane churn from the widened rules (false positives on the chat lane) | Medium | Low | The chat-lane error texts are byte-identical (lane-worded); the chat lane's predicate arm is unchanged; the text-lane regression pins (Stage 5) cover it |
| R10 | The decision lane's group branch changes the NON-group decision path (a refactor slip) | Medium | Medium | The per-attempt body is extracted but the non-group path's writes stay in today's order; every existing `pkg/decision` test must pass UNCHANGED (a merge gate) |
| R11 | The loop attempts a member twice (no tried-set) or spins (no cap) | Low | Low | The `tried` set + the `MaxGroupMembers` cap; the offer-walk guard (an already-tried replacement is excluded and the scan advances, mirroring `pkg/proxy/handler_groups.go:465-479`) |

---

## Test plan

Harness legend: **httptest fake upstream** = the `pkg/decision` `fakeUpstream`
fixture (`pkg/decision/handler_test.go:121-196`); **real SQLite** = the
`pkg/store/database` in-memory/temp-file manager (`newGroupManager`,
`pkg/store/database/store_group_test.go:35-62`); **pure unit** = no I/O;
**API/DB** = the `pkg/ui` mock-model server (`newModelTestServer`,
`pkg/ui/kind_rules_parity_test.go:412-445`).

### 0 — vocabulary leaf

| File | Test | Asserts | Harness |
|------|------|---------|---------|
| `pkg/credclass/classify_test.go` (NEW) | `TestCredClass_IsQuota_VocabularyTable` | Every quota vocabulary entry classifies quota; a plain `usage limit` does NOT; `token plan` is status-agnostic; HTTP 402 is quota | pure unit |
| `pkg/credclass/classify_test.go` (NEW) | `TestCredClass_IsRateLimit_VocabularyTable` | 429 → rate-limit; type-substring `rate_limit` → rate-limit; code set `{rate_limit, rate_limit_error, rate_limit_exceeded}` → rate-limit; a quota body is NOT rate-limit | pure unit |
| `pkg/providers/interface_quota_test.go` (EXISTING) | all | **unchanged** — the Stage-0 gate | pure unit |
| `pkg/providers/ratelimit_classifier_test.go` (EXISTING) | all | **unchanged** — the Stage-0 gate | pure unit |

### 1 — write rules (canonical validator)

| File | Test | Asserts | Harness |
|------|------|---------|---------|
| `pkg/models/group_rules_test.go` (NEW fns; the existing table at `:105` is **LEFT ALONE**) | `TestValidateGroupMembers_DecisionMembers_Accepted` | A group of two decision-kind members validates with no error | pure unit |
| `pkg/models/group_rules_test.go` (NEW) | `TestValidateGroupMembers_MixedKindMembers_Rejected` | chat + decision members ⇒ the homogeneity error (a dedicated message, not the member-kind one) | pure unit |
| `pkg/models/group_rules_test.go` (NEW) | `TestValidateGroupMembers_DecisionGroup_RejectsChatMember` | The bidirectional rule: a decision-kind member cannot join a chat-member group (mixed ⇒ rejected) | pure unit |
| `pkg/models/group_rules_test.go` (NEW) | `TestValidateGroupMembers_LegacyEmptyKind_IsChatLane` | A `""`-kind member is chat; mixing it with a decision member is rejected (the `""` legacy never enters a decision group) | pure unit |
| `pkg/models/group_rules_test.go` (NEW) | `TestValidateGroupMembers_DecisionGroup_ShadowTargetMustMatchLane` | A decision group with a chat shadow target ⇒ `must reference a decision-kind model`; a decision group with a decision shadow target ⇒ OK | pure unit |

### 2 — runtime predicate + `GroupLane`

| File | Test | Asserts | Harness |
|------|------|---------|---------|
| `pkg/store/database/store_group_test.go` (NEW) | `TestGroup_GroupLane_InfersChat`, `TestGroup_GroupLane_InfersDecision`, `TestGroup_GroupLane_MixedGroupReturnsFalse` (three top-level tests) | `GroupLane` returns chat for a chat group, decision for a decision group, `("", false)` for a mixed group / a non-group / an unknown id | real SQLite |
| `pkg/store/database/store_group_test.go` (NEW) | `TestGroup_RewriteGroupEntries_DecisionGroup_PicksDecisionMember` | **SUPERSEDED by Amendment 1 / ADR-5** — the sweep is the TEXT lane's path and must REFUSE a decision-lane group; the replacement is `TestGroup_RewriteGroupEntries_DecisionGroup_RefusedOnTextSweep` (Amendment 1 §A16) | superseded |
| `pkg/store/database/store_group_test.go` (NEW) | `TestGroup_RewriteGroupEntries_MixedKindGroup_FailsClosed` | A mixed-kind group ⇒ `FailedGroupID` (never a misroute) | real SQLite |
| `pkg/store/database/store_group_test.go` (NEW) | `TestGroup_RewriteGroupEntries_DecisionGroup_RejectsLegacyEmptyKindMember` | A `""`-kind member is NOT servable in a decision group | real SQLite |
| `pkg/modelscache/models_group_lane_test.go` (NEW) | `TestCachedModelsConfig_GroupLane_ReachesInnerManager` | **The decorator-through test (the trap).** Bind the CACHED decorator over a real manager; assert the lane matches the raw manager's. The failure text MUST name the trap: `GroupLane is a no-op through the decorator` | real SQLite + decorator |
| `pkg/modelscache/models_group_lane_test.go` (NEW) | `TestCachedModelsConfig_GroupLane_MixedGroup_ReturnsFalse` | The decorator does not paper over a mixed group | real SQLite + decorator |

### 3 — admin API parity

| File | Test | Asserts | Harness |
|------|------|---------|---------|
| `pkg/ui/kind_rules_parity_test.go` (NEW fn; the existing table at `:520-620` is **LEFT ALONE**) | `TestKindRules_Parity_Group_DecisionLane` | **Each site asserted independently** (helper ≡ POST ≡ PUT, three separate assertions per case, the house discipline): decision members accepted; chat member rejected; decision member rejected; mixed rejected; lane-matched shadow accepted; lane-mismatched shadow rejected | API/DB |
| `pkg/ui/kind_rules_parity_test.go` (EXISTING `:312-317`) | all | **unchanged** — the decision provider gate parity (R3: the provider vocabulary is untouched) | API/DB |
| `pkg/store/database/store_group_test.go` (NEW) | `TestGroup_AddModel_MixedKindMembers_Rejected` | The DB write path rejects a mixed-kind group (the canonical validator, reached via `validateGroupWrite` — `pkg/store/database/store.go:2112-2117`) | real SQLite |
| `pkg/store/database/store_group_test.go` (NEW) | `TestGroup_AddModel_DecisionMembers_Accepted` | A decision-member group saves through `AddModel`/`UpdateModel` | real SQLite |

### 4 — decision lane (the bulk)

| File | Test | Asserts | Harness |
|------|------|---------|---------|
| `pkg/decision/handler_group_test.go` (NEW) | `TestDecision_Group_ResolvesToDecisionMember` | A group id on `/v1/decisions` resolves to a member and returns that member's typed answer (200) | httptest fake upstream |
| `pkg/decision/handler_group_test.go` (NEW) | `TestDecision_Group_DefaultPoolFirst_BackupPoolAfter` *(re-derived by Amendment 1 / ADR-12 — the pool order is a GROUP-layer policy fact)* | The default member serves first; with the default excluded, the BACKUP member serves (pool=backup) — the two-pool semantics are the manager's (`pkg/store/database/store_group_rewrite.go:147` vs `:176`) | httptest fake upstream + real SQLite |
| `pkg/decision/handler_group_test.go` (NEW) | `TestDecision_Group_WeightsAffectPick` *(re-derived by Amendment 1 / ADR-5 — the weighted pick is now the lane's own POLICY head: fresh, no binding, no marks)* | Over N fresh (empty-key) requests the heavier member is picked more often — the weighted selector (`pkg/grouplb/selector.go:26`) with **no** binding (D-6) | httptest fake upstream + real SQLite |
| `pkg/decision/handler_group_test.go` (NEW) | `TestDecision_Group_AllMembersFail_502DecisionAllMembersFailed` *(re-derived by Amendment 1 / ADR-5, ADR-6 — the attempt count is over member × credential, and suppressed attempts are excluded)* | EVERY member was attempted before the failure (assert the attempt count == the member count); the response is 502 with `code=decision_all_members_failed`; **no** usage row was written; exactly ONE `all_members_failed` event; the log carries the attempted count | httptest fake upstream + real SQLite usage DB |
| `pkg/decision/handler_group_test.go` (NEW) | `TestDecision_Group_FirstFailsSecondSucceeds_ExactlyOneCommit` | Member A fails (transport error), member B succeeds ⇒ 200 with B's answer; **exactly one** upstream commit; the winner's member id is recorded in the event | httptest fake upstream |
| `pkg/decision/handler_group_test.go` (NEW) | `TestDecision_Group_NoWriteBeforeWinner` | The response writer records **zero** `WriteHeader` calls before the winner's attempt and exactly one after (contract 6) | httptest recorder wrapper |
| `pkg/decision/handler_group_test.go` (NEW) | `TestDecision_Group_MarkedMember_DemotedNotFiltered` | **SUPERSEDED by Amendment 1 / ADR-5** — the "demote-not-filter" framing is withdrawn; the replacement is the suppression test set (`TestDecision_Group_MarkedHead_ZeroUpstreamCallSkip`, `TestDecision_Group_AllMarked_PolicyHeadHonestAttempt`, `TestDecision_Group_PolicyPick_IgnoresMarks` — see Amendment 1 §A16) | superseded |
| `pkg/decision/handler_group_test.go` (NEW) | `TestDecision_Group_WinClear_ClearsWinnerNotFailedHead` | The WINNER's skip marks + member cooldown are cleared; the FAILED head's cooldown **survives** (identity discipline — the precedent `pkg/proxy/win_clear_winner_identity_test.go:218`) | httptest fake upstream + real SQLite + skip cache |
| `pkg/decision/handler_group_test.go` (NEW) | `TestDecision_Group_NoCooldownWriteOnBenchedPick` | **SUPERSEDED by Amendment 1 / ADR-5** — "benched" is withdrawn vocabulary; the replacement is `TestDecision_Group_NoCooldownWriteOnSuppressedPick` (Amendment 1 §A16) | superseded |
| `pkg/decision/handler_group_test.go` (NEW) | `TestDecision_Group_Exhaustion_DoesNotReattemptTriedMember` | The tried-set holds: each member is attempted at most once per request | httptest fake upstream |
| `pkg/decision/handler_group_test.go` (NEW) | `TestDecision_Group_NonGroupRequest_ByteIdentical` | A non-group decision request is byte-identical to the pre-change path (status + body + event shape) — D-3's byte-identity verification | httptest fake upstream |
| `pkg/decision/handler_group_test.go` (NEW) | `TestDecision_Group_LaneSeamAbsent_FailsClosed400` | With the `GroupLane` seam absent the handler logs loudly and returns 400 `decision_misroute` (D-8) | pure unit + recorder |
| `pkg/decision/handler_group_test.go` (NEW) | `TestDecision_Group_PolicyPick_IgnoresMarks` | **Amendment 1 / ADR-5:** a mark on the head does NOT change which member is the head; the head reported in telemetry is the POLICY head (AC-R1) | httptest fake upstream + skip cache |
| `pkg/decision/handler_group_test.go` (NEW) | `TestDecision_Group_MarkedHead_ZeroUpstreamCallSkip` | **Amendment 1 / ADR-5:** a marked head's attempt is suppressed — ZERO upstream hits, the event shows `member_suppressed`, the next candidate is attempted (AC-R2) | httptest fake upstream + skip cache |
| `pkg/decision/handler_group_test.go` (NEW) | `TestDecision_Group_AllMarked_PolicyHeadHonestAttempt` | **Amendment 1 / ADR-11 (OQ-1):** with every candidate marked, the POLICY head is attempted exactly once (1 upstream hit) — the head-suppression lift | httptest fake upstream + skip cache |
| `pkg/decision/handler_group_test.go` (NEW) | `TestDecision_Group_AttemptOrder_PolicyOrderMinusMarked` | **Amendment 1 / ADR-5 (AC-R3):** the attempt sequence is the policy order MINUS the marked candidates — no reordering | httptest fake upstream + skip cache |
| `pkg/decision/handler_group_test.go` (NEW) | `TestDecision_Group_Exhaustion_ExcludesSuppressedAttempts` *(re-derived by Amendment 2 / ADR-13)* | **Amendment 1 / ADR-5, ADR-6:** the exhaustion event's `attempted` counts GENUINE attempts only; `skipped` is separate; the 502 fires only after every member × credential was genuinely attempted and failed | httptest fake upstream + skip cache |
| `pkg/decision/handler_group_test.go` (NEW) | `TestDecision_Group_MemberWithTwoCredentials_AttemptsBothBeforeAdvancing` | **SUPERSEDED by Amendment 2 / ADR-13 (AM-4)** — rotation is class-conditional, so "both" is no longer unconditional; the replacement is `TestDecision_Group_MemberWithTwoCredentials_LimitClassRotatesBeforeAdvancing` (Amendment 2 §B4) | superseded |
| `pkg/decision/handler_group_test.go` (NEW) | `TestDecision_Group_MemberFailedPredicate_OwnResilienceExhausted` *(re-derived by Amendment 2 / ADR-13)* | **Amendment 1 / ADR-6 (AC-R5):** the member-failed predicate is true only when the member's own resilience (per-credential attempts; chain ABSENT) is exhausted | httptest fake upstream + real SQLite |
| `pkg/decision/handler_group_test.go` (NEW) | `TestDecision_Group_SingleMemberGroup_FullyConsumable` | **Amendment 1 / ADR-11 (OQ-3, AC-R8):** a single-member, single-credential group is fully valid and fully consumable — one attempt, then exhaustion (no hedge engine) | httptest fake upstream |
| `pkg/decision/handler_group_test.go` (NEW) | `TestDecision_Group_PickAttemptSkipEventOutcomes` | **Amendment 1 / ADR-10 (AC-R10):** `member_picked` / `member_attempted` / `member_suppressed` are DISTINCT outcomes | httptest fake upstream |
| `pkg/decision/handler_group_test.go` (NEW) | `TestDecision_Group_LayerSeparation_CredentialMarkCannotChangePick` | **Amendment 1 / ADR-12 (AC-R11):** a UPSTREAM-layer (credential) mark provably cannot alter the group pick; the pick reads no failure state; the MODEL layer's chain reader is recorded ABSENT | httptest fake upstream + skip cache |
| `pkg/decision/handler_group_test.go` (NEW) | `TestDecision_Group_InactiveMember_NotOfferedAsCandidate` | **Amendment 1 / ADR-7 (AC-R6):** an inactive model is not offered as a group candidate | real SQLite |
| `pkg/models/group_rules_test.go` (NEW fn) | `TestValidateGroupMembers_InactiveChainOnlyTarget_Accepted` | **R4 write-time ACCEPTANCE pin (approver iteration-002 FIX 1; ADR-7 / ADR-11 OQ-4):** a model saved with an **INACTIVE / chain-only `fallback_chain` target** is **ACCEPTED** — the test FAILS if validation rejects it (R4 verbatim: "validation MUST ALLOW inactive/chain-only targets"; "disabled-but-chain-referenced" is NOT a defect). Complements `TestDecision_Group_InactiveMember_NotOfferedAsCandidate` (the candidate-surface half of AC-R6); this is the write-time acceptance half. Asserts acceptance at the canonical validator AND the API parity site. | pure unit + API/DB mock |
| `pkg/decision/handler_group_test.go` (NEW) | `TestDecision_Group_MemberNeverAutoEnabled` | **Amendment 1 / ADR-7 (AC-R7):** adding a member to a group never flips `enabled` | real SQLite |
| `pkg/decision/handler_group_test.go` (NEW) | `TestDecision_Group_ChainTargetNotConflatedWithMember` | **Amendment 1 / ADR-11 (OQ-4):** chain targets and group members are TWO populations; the chain clause is vacuous on this lane and never consulted | real SQLite |
| `pkg/decision/handler_group_test.go` (NEW) | `TestDecision_DoctrineLine_PinnedVerbatimAtSeam` | **Amendment 1 / ADR-10 (AC-R9) — target NAMED by Amendment 7 / ADR-18 (C2-1):** the extended doctrine line is present VERBATIM in the suppression seam's source (**`pkg/decision/suppression.go`** — the file Stage 4 creates; `pkg/skipcache/skipcache.go:29-31` is v194's canonical home, **cited read-only, never edited**; the test MAY additionally assert skipcache's copy as read-only drift detection) | source-literal pin |
| `pkg/decision/handler_group_test.go` (NEW) | `TestDecision_Group_NoCooldownWriteOnSuppressedPick` | **Amendment 1:** a SUPPRESSED pick writes no cooldown and no grouplb state (replaces the superseded "benched pick" test) | real SQLite + skip cache |
| `pkg/store/database/store_group_test.go` (NEW) | `TestGroup_RewriteGroupEntries_DecisionGroup_RefusedOnTextSweep` | **Amendment 1 / ADR-5:** the text-lane sweep REFUSES a decision-lane group — no decision member is ever offered to the chat path | real SQLite |
| `pkg/store/database/store_group_test.go` (NEW) | `TestUpdateModel_KindChange_GroupMember_Rejected` | **Amendment 2 / ADR-1 (criterion 1):** retyping a member of ANY group is refused; the stored kind is unchanged; group resolution unchanged | real SQLite |
| `pkg/store/database/store_group_test.go` (NEW) | `TestUpdateModel_KindChange_GroupMember_RejectionNamesGroupAndMember` | **Amendment 2 / ADR-1 (criterion 3):** the rejection names BOTH the group id and the member id | real SQLite |
| `pkg/store/database/store_group_test.go` (NEW) | `TestUpdateModel_KindChange_NonMember_Succeeds` | **Amendment 2 / ADR-1 (criterion 2):** a model NOT in any group still retypes (no blanket immutability) | real SQLite |
| `pkg/store/database/store_group_test.go` (NEW) | `TestGroup_SingleMemberDecisionGroup_MemberRetypeRefused` | **Amendment 2 / ADR-1:** the hole-closure case — a single-member decision group whose member retype to chat is REFUSED (the E-2 hole is closed by guard) | real SQLite |
| `pkg/ui/kind_rules_parity_test.go` (NEW) | `TestKindRules_Parity_KindImmutability` | **Amendment 2 / ADR-1 (criterion 4):** the guard asserted at the canonical write path AND the API PUT, independently | API/DB |
| `pkg/decision/handler_group_test.go` (NEW) | `TestDecision_Group_MemberWithTwoCredentials_LimitClassRotatesBeforeAdvancing` | **Amendment 2 / ADR-13 (AM-4, AC-R4):** a LIMIT-CLASS failure on credential #1 rotates to credential #2 BEFORE the loop advances | httptest fake upstream + real SQLite |
| `pkg/decision/handler_group_test.go` (NEW) | `TestDecision_Group_5xx_AdvancesWithoutRotationOrMark` | **Amendment 2 / ADR-13 (AM-4):** a 5xx ADVANCES to the next member immediately — no within-member rotation, NO upstream-layer mark, NO cooldown write | httptest fake upstream + skip cache |
| `pkg/decision/handler_group_test.go` (NEW) | `TestDecision_Group_401_RotatesWithinMember` | **Amendment 2 / ADR-13 (AM-4):** upstream 401/403 is rotation-eligible — the member's next credential is attempted, with the upstream-layer write per classification | httptest fake upstream + skip cache |
| `pkg/decision/handler_group_test.go` (NEW) | `TestDecision_Group_400_413_415_422_FailFastAdvanceBlocked` | **Amendment 2 / ADR-13 (AM-4):** advance-blocking classes FAIL FAST as the text lane (`pkg/proxy/cred_failover_class.go:190`) | httptest fake upstream |
| `pkg/decision/handler_group_test.go` (NEW) | `TestDecision_Group_SkipCacheInstance_NotSharedWithTextLane` | **WITHDRAWN by Amendment 5 / ADR-16** — the instance is shared ON PURPOSE (one answer about one credential); the invariant it pinned is wrong, not merely re-based. Replaced by `TestDecision_Group_CacheLayerSeparation_SharedUpstreamInstanceOneAnswer` | withdrawn |
| `pkg/decision/handler_group_test.go` (NEW) | `TestDecision_Group_CacheLayerSeparation_SharedUpstreamInstanceOneAnswer` | **Amendment 5 / ADR-16 (AM-2 re-derivation):** the decision lane's UPSTREAM read is the SAME process-wide instance the text lane writes (one answer), and NEITHER health layer can reach the group pick (the layer fence) | httptest fake upstream + skip cache |
| `pkg/store/database/store_group_test.go` (NEW) | `TestGroup_SweepKindParameterization_MutationControl` | **Amendment 5 / ADR-16 (WB-4):** the predicate consults the expected kind — a decision member is servable when the expected kind is decision and NOT servable when it is chat (the v193-class mutation control) | real SQLite |
| `pkg/ui/kind_rules_parity_test.go` (NEW) | `TestDecision_Vocabulary_OutOfSetProvider_Rejected_AtAllGates` | **Amendment 7 / ADR-18 (T2 — Stage 3):** a provider OUTSIDE `{openai, typesafe, openrouter}` is rejected for a decision model at EVERY gate — the canonical rule (`config.go:293-300`), the DB write path (`store.go:2063` region), the POST mirror (`server.go:1171`), the PUT mirror (`server.go:1636`) — parity across all sites | API/DB parity harness |
| `pkg/decision/vocabulary_pin_test.go` (NEW) | `TestDecision_Vocabulary_SourceLiteralPin_FiveSurfaces` | **Amendment 7 / ADR-18 (T3 — Stage 6):** the five vocabulary surfaces cannot drift independently — the FE constant (`DecisionModelForm.tsx:29`), the canonical gate (`config.go:293-300`), the POST mirror (`server.go:1171`), the PUT mirror (`server.go:1636`), the registry init (`registry.go:50-52`) all carry the same set | source-literal pin |
| `pkg/store/database/models_manager_kind_test.go` (NEW fn) | `TestDecision_Vocabulary_CredentialModelSplit_AnthropicSaves_ModelRejected` | **Amendment 7 / ADR-18 (T4 — Stage 3):** the credential/decision vocabulary split, positive case — an **anthropic** credential SAVES (`validProviders`, `credential.go:15-60`) while a decision MODEL referencing it is REJECTED (D4's gate — the maps are nested, never crossed) | real SQLite |
| `pkg/decision/vocabulary_pin_test.go` (NEW) | `TestDecision_Vocabulary_RegistryKeySetEqualsGateAllowList` | **Amendment 7 / ADR-18 (T5-Go — Stage 6):** the registry key set ≡ the gate allow-list set (`registry.go:50-52` vs `config.go:295`) — the anti-saveable-but-502 pin (V1/T5-FE already landed in phase 2) | pure unit |
| `pkg/decision/handler_group_test.go` (NEW) | `TestDecision_Group_AllCredentialsCooling_PolicyFirstCredentialHonestAttempt` | **Amendment 4 / ADR-15 (D-C):** every credential of a member marked/cooling ⇒ exactly ONE honest attempt on that member's policy-first credential; a genuine failure per the AM-4 class policy fails the member and advances the loop | httptest fake upstream + real SQLite + skip cache |
| `pkg/decision/handler_group_test.go` (NEW) | `TestDecision_Group_ChatInferredWithDecisionMember_NeverSilentlyServes` | **Amendment 4 / ADR-15 (W-fail-open):** a group inferred as chat containing ≥1 decision member NEVER silently serves — loud 400 `decision_misroute` + a telemetry event | httptest fake upstream + real SQLite |
| `pkg/decision/handler_group_test.go` (NEW) | `TestDecision_Group_Exhaustion_5xxMemberCountsAsGenuinelyFailed` | **Amendment 2 / ADR-13 (AM-4):** a 5xx'd member counts as GENUINELY FAILED for this request — the exhaustion criterion is per the failure-class policy | httptest fake upstream |
| `pkg/decision/handler_group_test.go` (NEW) | `TestDecision_Group_StampedStatusDrivesClassification` | **The E-4 pin:** the handler-stamped `outcome.UpstreamStatus` actually drives the classification — a member answering **429** produces a limit-class demotion (a skip mark IS written for `(credential, wire-model)` + `(provider, wire-model)`), while a member answering **400 / 413 / 415 / 422** writes **NO** mark but still **advances** to the next member. This is what stops a silent "status always zero ⇒ nothing ever demotes" implementation | httptest fake upstream + skip cache |
| `pkg/decision/handler_group_test.go` (NEW) | `TestDecision_Group_ThroughCachedConfig` | The PRODUCTION binding (a `*modelscache.CachedModelsConfig`) enters the group branch — the end-to-end decorator proof on the decision lane | httptest fake upstream + decorator + real SQLite |
| `pkg/decision/handler_test.go:692` (EXISTING) | `TestHandler_ResolveModel_NotDecisionKind_400` | **LEFT ALONE** — a chat model is still 400 `decision_misroute` (the gate was EXTENDED, not widened) | httptest fake upstream |
| `pkg/decision/proxy_handler_decision_models_test.go:156`, `:188` (EXISTING) | the chat / image-gen misroute 400s | **LEFT ALONE** — unchanged | recorder only |

### 4b — the other three `GroupLane` consumers

| File | Test | Asserts | Harness |
|------|------|---------|---------|
| `pkg/proxy/handle_chat_misroute_test.go` (NEW fn in the existing file) | `TestHandleChatCompletions_RejectsDecisionGroupMisroute` | A decision GROUP on `/v1/chat/completions` ⇒ 400 + `code=decision_misroute` + **zero upstream hits**; a CHAT group on the same route is unaffected | proxy harness + a config that implements `GroupLane` |
| `pkg/proxy/handler_anthropic_misroute_test.go` (NEW fn) | `TestAnthropicBoundary_DecisionGroupMisroute` | A decision GROUP on `/v1/messages` ⇒ 400 `decision_misroute` (Anthropic envelope) + zero upstream hits; a chat group unaffected | proxy harness |
| `pkg/proxy/handle_models_imggen_test.go` (NEW fn) | `TestHandleModels_FilterDecisionGroup` | A decision group is ABSENT from `/v1/models`; a chat group is still listed (today's behaviour, `pkg/proxy/handle_models_group_test.go`) | proxy harness |

### 5 — text-lane regression pins (R6)

| File | Test | Asserts | Harness |
|------|------|---------|---------|
| `pkg/proxy/handler_groups_test.go:400` (EXISTING) | `TestHandlerGroups_Am26_NonGroup_ByteIdentity` | **LEFT ALONE** — the non-group chat path is byte-identical | proxy harness |
| `pkg/store/database/store_group_skipcache_test.go` (EXISTING, **rewritten by v194**) | `TestGroup_SkipCache_MarksDoNotChangeThePick` (`:59`), `TestGroup_SkipCache_ProviderWallDoesNotChangeThePick` (`:85`), `TestGroup_SkipCache_AllMarked_StillServes` (`:106`), `TestGroup_SkipCache_MarkNeverBenchesOrderingSeam` (`:128`), `TestGroup_SkipCache_ProviderMarkDoesNotWallSibling` (`:147`) | **LEFT ALONE — and these are now the R1 pins of record**: v194 rewrote the file and **inverted the meaning** (the old `…HealthAwareSelection` / `…NilCacheByteIdentical` are GONE) — the new pins assert the mark does NOT change the pick | real SQLite |
| `pkg/proxy/cache_layer_separation_test.go` (EXISTING, **new in v194**) | the whole layer-contract suite (`TestCacheLayer_Structural_PackageLevelNoCacheImports` `:752`, `…ImportParserIsCommentAware` `:789`, `…GroupPick_StableUnderUpstreamMarks` `:690`, …) | **LEFT ALONE — the layer-contract pins this release must not regress** | proxy harness |
| existing suites | `pkg/models`, `pkg/store/database`, `pkg/ui`, `pkg/proxy`, `pkg/decision`, `pkg/modelscache`, `pkg/providers`, `pkg/credclass` | **ALL pass unchanged** except the explicitly re-pinned rows below — the local gate is `make test` (the CI workflow swallows Go-test failures, `docker-ci.yml:104`) | — |

---

## Known breakage sites — verdict per site

| Site | What happens | Verdict | Why |
|------|---------------|---------|-----|
| `pkg/models/group_rules_test.go:105` (`TestValidateGroupMembers_CollectionRuleViolations`) | The table's rows assert error SUBSTRINGS. Under the lane-worded design the chat-lane substrings are **byte-identical** (`must be a chat-kind model`, `must reference a chat-kind model`, `must reference a chat-kind model, not a group`) | **LEFT ALONE** | The image-gen member case has an empty valid-lane set ⇒ the chat wording is used ⇒ the assertion still matches. New decision-lane cases are added as **NEW functions** (the table's `mutate` signature cannot seed a collection). |
| `pkg/ui/kind_rules_parity_test.go:520-620` (+ `:312-317`) | The parity table's chat-lane substrings are unchanged under the lane-worded design | **LEFT ALONE** (the table) + **NEW function** for the decision lane | The decision-lane cases need a decision fixture and a different base group ⇒ a separate fixture helper (`addDecisionGroupMembers`) + a separate test function; the shared `addGroupMembers` fixture is NOT mutated (no churn in the GET `?kind=` tests) |
| `pkg/store/database/store_group_test.go:146` (`TestGroup_AddModel_GViolations`) | The table has no member-kind row (only existence/weight/dup/G6/G8) | **LEFT ALONE** + **NEW sibling functions** | The homogeneity rule needs a COLLECTION (a decision member row) that the table's `mutate func(m *models.ModelConfig)` cannot seed |
| `pkg/store/database/store_group_test.go:592` / `:591-630` (`TestGroup_RewriteGroupEntries_Am28StaleMember`) | The members are DISABLED, not retyped ⇒ still base-stale ⇒ the sweep skips them exactly as today | **LEFT ALONE** | `isServableGroupMemberBase` rejects a disabled row on the `row == nil \|\| !row.Enabled` arm, before the kind arm; the lane inference counts the disabled row's kind (chat) ⇒ the lane is chat ⇒ identical behaviour |
| `pkg/store/database/store_group_test.go:926`, `:944-950` (`TestGroup_C1_BackupLessGroupsKeepV1Semantics`) | Chat-only fixtures (`coding`, `coding-alt`); the exclusion + all-cooling semantics are kind-independent | **LEFT ALONE** | The lane inference resolves to `chat`; the predicate's chat arm is unchanged |
| `pkg/store/database/store_group_skipcache_test.go:62` | Chat-only fixture; the skip-mark keys are unchanged | **LEFT ALONE — but the file was REWRITTEN by v194** | The old `…HealthAwareSelection` pin is GONE; v194's own R1 pins now stand at `:59`/`:85`/`:106`/`:128`/`:147` (the mark does NOT change the pick). Our change must not regress them (Amendment 5 §E9, row 33) |
| `pkg/decision/handler_test.go:692` | Pins chat → 400 `decision_misroute` | **LEFT ALONE** | The gate is EXTENDED with an explicit group branch (`IsDecision()` → normal; `IsGroup() && lane==decision` → group; **else** → the existing 400), so the chat case still 400s |
| `pkg/decision/proxy_handler_decision_models_test.go:156`, `:188` | Pin chat → 400 and image-gen → 400 | **LEFT ALONE** | Same reason — a naive "accept anything that is not chat/image-gen" widening would have broken these; the explicit branch does not |
| `pkg/ui/kind_rules_parity_test.go:312-317` (decision provider gate) | Unchanged | **LEFT ALONE** | R3: no new provider; the gate's vocabulary is untouched |
| `pkg/store/database/store.go:2113` (`validateGroupWrite` passes `""`) | Unchanged | **LEFT ALONE** | R3: the group's own write path consults no provider vocabulary |

---

## R1 — Decision-model groups resolve on `/v1/decisions`

Covered by Stage 4 (tasks 4.4) + the tests
`TestDecision_Group_ResolvesToDecisionMember`,
`TestGroup_RewriteGroupEntries_DecisionGroup_PicksDecisionMember`.
Acceptance: a group row (`kind="group"`) whose members are `kind="decision"`
is accepted by the decision lane and served by one of its members.

## R2 — Resolution + failover on the decision lane

- **Exhaustive PRE-COMMIT fallback** — the loop owns the write block
  (`pkg/decision/handler.go:507-548`); the commit point is `:517` (PassAsIs) /
  `:541` / `:543` (PromoteToError); no path writes a header before a winner
  exists (test `TestDecision_Group_NoWriteBeforeWinner`).
- **v193 doctrine verbatim** *(superseded by Amendment 1 / ADR-5 — the decision lane's
  rule is SUPPRESSION, not reordering: a marked head is skipped at zero upstream
  cost; the extended doctrine line is pinned verbatim at the suppression seam)* — "The cache is an optimization, not the truth: it
  may reorder candidates, never exclude them"
  (`pkg/skipcache/skipcache.go:13-26`): the lane MARKS
  (`pkg/decision/handler.go`, new mark helper; the shape mirrors
  `pkg/proxy/race_coordinator.go:2088-2111`), the SWEEP consumes the marks as
  ORDERING inputs only (`memberDemotionExpiry`,
  `pkg/store/database/store_group_rewrite.go:273-282`), the WIN clears the
  winner's marks (`pkg/skipcache/skipcache.go:135`), a benched pick writes NO
  cooldown, and the tried-set is request-scoped.
- **Member pools + weights** — the manager's two pools and the weighted
  selector are reused unchanged (`pkg/store/database/store_group_rewrite.go:147`
  / `:176`; `pkg/grouplb/selector.go:26`).
- **Sticky/binding is RULED OUT (D-6)** — evidence: there is **no conversation
  key** on this lane (the resolver takes one argument,
  `pkg/decision/handler.go:738`; `cmd/main.go:327` passes the model id twice);
  the request body has no conversation shape (`pkg/decision/types.go:130-135`);
  the sticky engine is injected only into the proxy (`cmd/main.go:144` → `:282`).
  The member conversation key is therefore `""` ⇒ "fresh weighted pick, no
  binding" (`pkg/models/group_rewrite.go:88-90`). No affinity machinery is
  invented.

## R3 — Provider vocabulary (no new provider)

**Proof that a group member's provider passes through NEITHER gate today:**
`pkg/models/group.go:57-61` skips the provider gate for groups
(`_ = internalProvider` — a group carries no credentials) and
`pkg/store/database/store.go:2113` passes `""` as `internalProvider` into
`validateGroupWrite`. Providers are validated only on the MEMBER rows, when
those members are written as decision models
(`pkg/models/config.go:293-300`).

**Therefore the five decision-vocabulary surfaces are UNCHANGED:** the FE
constant `pkg/ui/frontend/src/components/config/DecisionModelForm.tsx:29`, the
BE gate `pkg/models/config.go:293-300`, the POST mirror `pkg/ui/server.go:1171`,
the PUT mirror `pkg/ui/server.go:1636`, the registry
`pkg/decision/registry.go:49-53`. The **credential** vocabulary is a separate
map (`pkg/models/credential.go:15-60`) — no cross-wiring.

**Acceptance test (the user's live shape):** a decision group with an `openai`
member and an `openrouter` member **saves successfully** at the API layer and
at the DB layer; and a credential whose provider is `anthropic` does **not**
become a decision provider (the registry lookup 502s — the existing
`TestHandler_OpenRouter_UnregisteredChatProvider_502` shape).

| File | Test | Asserts | Harness |
|------|------|---------|---------|
| `pkg/store/database/store_group_test.go` (NEW) | `TestGroup_AddModel_DecisionMembers_MixedProviders_Accepted` | A decision group with an `openai` member and an `openrouter` member saves (no provider gate on the group row) | real SQLite |
| `pkg/ui/kind_rules_parity_test.go` (NEW fn) | `TestKindRules_Parity_Group_DecisionMembers_MixedProviders` | The POST/PUT mirrors accept the same group (no "saveable-but-502" hole) | API/DB |
| `pkg/decision/openrouter_decision_test.go` (EXISTING) | `TestHandler_OpenRouter_UnregisteredChatProvider_502` | **LEFT ALONE** — `anthropic` is still not a decision provider | recorder only |

## R4 — Config surface (admin API / DTO)

The DTO is unchanged: `Model` (`pkg/ui/server.go:46-77`; `Kind` at `:57`,
`Members` at `:76`). Routes unchanged: `/fe/api/models` (POST),
`/fe/api/models/` (PUT), `/fe/api/models/validate`. The validation rules:
group kind; flat membership (G5 forbidden in both lanes); the member-kind rule
+ homogeneity (Stage 1); the lane-matched shadow target (Stage 1); the cap 16
(`pkg/models/group.go:25`); ≥1 default member (`pkg/models/group.go:145`).
**All three sites (helper / POST / PUT) are asserted independently** by the
parity test (Stage 5).

## R5 — The decorator-forward trap (mandatory per method)

**One** new manager method is added (`GroupLane`). It ships with:
1. the forward (`pkg/modelscache/models.go`, next to `:1079-1085`) **and**
2. a decorator-through test (`pkg/modelscache/models_group_lane_test.go`,
   `TestCachedModelsConfig_GroupLane_ReachesInnerManager`) whose failure text
   names the trap, cloned from `pkg/proxy/win_clear_winner_identity_test.go:120`
   / `:191`.
If any further manager method is added during implementation, the same pair is
mandatory for it (the acceptance criterion is per-method).

## R6 — Text-lane regression pins

See `## Test plan → 5`. The chat-lane error texts are byte-identical (the
lane-worded design), the chat arm of the servability predicate is unchanged,
the sweep's ordering logic is untouched, and the v191–v193 fixes are not in
the diff. The `make test` gate is local (CI swallows Go-test failures,
`docker-ci.yml:104`).

---

## Escalations

**E-1 — D-7 (the text-lane fence) contradicts D-8 / contracts 15–16 / the
blast-radius table.**
D-7 item 1 (`decisions.md:256`) fences `pkg/proxy/handler.go` and
`pkg/proxy/handler_anthropic.go` as untouched; D-7's diff-review list
(`decisions.md:269`) omits them. But D-8 (`decisions.md:280-291`) names the chat
misroute guard, the Anthropic misroute guard and the `/v1/models` filter as
`GroupLane` consumers, and the blast-radius table rows 20/21/22
(`decisions.md:320-322`) plus contracts 15/16 (`decisions.md:368-369`) REQUIRE
those exact files to change ("a decision group posted to
`/v1/chat/completions` returns 400 `decision_misroute`").
**Proposed resolution (implemented as planned):** implement the three consumers
as the contracts require, with the guards ADDITIVE and LANE-GATED — they fire
only for a group whose inferred lane is `decision` (a shape that cannot exist
today), so the text lane's behaviour for every existing row is unchanged. The
chat-lane **error texts** are byte-identical. If the approver instead wants the
literal D-7 file fence, then contracts 15/16 and blast-radius rows 20–22 must
be struck, and a decision group posted to a chat route would be served as a
chat group — the misroute the ruling wants closed.

**E-2 — D-2's verification item (2) is unachievable for a single-member group.**
D-2 (`decisions.md:148`) asks for "a group written as decision, whose member is
then retyped to chat via `UpdateModel`, returns `group_unservable` (not a chat
success)". With **no persisted lane** (D-2's own decision, `decisions.md:131`),
a group whose only member is now chat-kind is **indistinguishable** from a
native chat group: the inference honestly re-lanes it. The achievable,
testable form (planned): (a) a **multi-member** decision group with one member
retyped ⇒ **mixed** ⇒ unservable on both lanes (fail closed — testable, and
pinned by `TestGroup_RewriteGroupEntries_MixedKindGroup_FailsClosed`); (b) a
single-member group retyped ⇒ re-lanes to chat (documented limitation). The
cure is the ruling's own Q3 (block kind retyping at the API level —
`pkg/store/database/store.go:1699` has no immutability guard), which the
ruling places outside this commission. **No schema change is proposed here.**
D-2's verification item (2) is therefore satisfied ONLY in its **multi-member**
form (mixed ⇒ unservable, pinned); the single-member form is an accepted,
documented limitation (planner adjudication E-2) whose cure — the D-2/Q3
kind-immutability guard — sits OUTSIDE this commission's fence and is being
surfaced to the user as a recommended same-release guard.
*(superseded by Amendment 2 / ADR-1 — the guard is IN for this release: the
single-member re-lane hole is CLOSED BY GUARD, not accepted. See Amendment 2,
Fold 1 / Stage 3b.)*

**E-3 — the skip-cache TTL needs a config getter that does not exist today.**
The decision lane must mark with the CONFIG-OWNED window
(`upstream_skip_cache_ttl`, `pkg/config/config.go:120`, default `:255`) — the
proxy reads it from its snapshot (`pkg/proxy/handler.go:101`), but
`pkg/config` exposes **no** `GetUpstreamSkipCacheTTL()` accessor (the field
exists; the getter does not). The decision handler's local `configGetter`
(`pkg/decision/handler.go:116-118`) therefore needs one additive getter
(`pkg/config/config.go`: interface member + `Manager` impl, ~4 lines, no
behaviour change) — a file outside D-7's enumerated diff list
(`decisions.md:269`) but required by D-8's "phase 1 must extend the seam"
(`decisions.md:204`). **Alternative if the approver forbids `pkg/config`:** the
decision handler takes a `skipCacheTTL func() time.Duration` seam injected from
`cmd/main.go` (`configMgr.Get().UpstreamSkipCacheTTL.Duration()`, the same
snapshot read the proxy performs) — no `pkg/config` change at all. The plan
proceeds with the getter and flags the alternative.

**E-4 — "the ADAPTER captures the raw upstream status" is not possible with the
current adapter signature.** D-4 (`decisions.md:186`) says the adapter captures
the upstream status into `Outcome.UpstreamStatus`. The adapter seam is
`InspectResponse(respBytes []byte, originalReq *DecisionRequest)`
(`pkg/decision/converter.go:88`) — **no status parameter** — and the call site
is `pkg/decision/handler.go:492`. The adapter therefore cannot see the status.
**Proposed resolution (implemented as planned):** keep the interface signature
UNCHANGED (extending it to 3 args would ripple into both adapters,
`pkg/decision/test_exec.go:208` and every adapter test — churn D-7 discourages)
and let the **handler** stamp `outcome.UpstreamStatus = resp.StatusCode` onto
the Outcome the adapter returned (the same additive field, the same material
contract: the Outcome carries the upstream status so the lane can classify).
The error-envelope fields (`ErrorType` / `ErrorCode` / `ErrorMessage`) **are**
adapter-captured — both adapters already parse them
(`pkg/decision/openaidecision.go:162-175`, `pkg/decision/typesafe.go:432-444`).

**E-5 — the "no servable member at all" sub-case shares the exhaustion code.**
D-5 defines ONE exhaustion code (`decision_all_members_failed`). A group whose
members are all disabled/deleted produces **no** attempt (the sweep returns
`FailedGroupID`, `pkg/store/database/store_group_rewrite.go:63-65`), so the
message reads "all decision group members failed (0 attempted)". **Proposed
resolution (implemented as planned):** keep ONE code (D-5's letter) and
distinguish the sub-case in the **event** (`members_attempted: 0`) and the log
line, not on the wire.

**E-6 — within-member credential iteration is not implemented.** *(superseded by Amendment 1 / ADR-6 + ADR-11 — the framing is RETRACTED: member-level is not a scope reduction but the ORDER OF OPERATIONS, and per-credential attempts (A-1 / AC-R4) are now in scope. AC-C1 is formally VOID.)* The commission's
R2 phrasing ("every member × credential") is satisfied at the granularity the
ruling defines: the lane resolves **one** credential per member through the
existing seam (`ResolveInternalConfigWithAffinity(memberID, memberID)`) and
advances **between members**. The ruling's D-3 seam list contains **no**
credential-LB seam for the decision lane (the credential-level exclusion
`ExcludeAndReselectForClass` lives in `pkg/credentiallb` and is reachable only
from `pkg/proxy` — `pkg/proxy/internal_handler.go:263`,
`pkg/proxy/race_coordinator.go:1900`), and `pkg/decision` may not import
`pkg/proxy` (`pkg/decision/handler_test.go:40`, `:90`). Within-member
credential rotation is therefore **out of scope** unless the approver adds a
seam (which would also need a re-ruling of D-3).

---

## Not verified

- **Nothing was executed.** No build, no `go test`, no lint, no image build, no
  smoke. Every breakage statement is static reasoning from code reads.
- `pkg/decision/handler.go` was read in regions (`:95-150`, `:245-320`,
  `:320-440`, `:440-570`, `:570-700`), not end-to-end; `:150-245` (auth +
  body handling) and `:700-780` (the error helpers) were read only partially.
- `pkg/decision/handler_test.go` is 2288 lines; only the import guards
  (`:40`, `:65`, `:90`), the harness (`:121-540`) and the named cases were
  read — individual assertion bodies beyond those points were not.
- `pkg/store/database/store_group_test.go` was read in regions (`:100-200`,
  `:520-660`, `:900-960`); the D8/C1/D2/D3 arms at `:755-1150` were not read
  line-by-line (the fixture helpers `newGroupManager` / `validGroup` at
  `:35-80` were).
- `pkg/ui/kind_rules_parity_test.go` was read at `:300-330` and `:405-760`; the
  `newModelTestServer` harness internals beyond `:412-445` were not.
- `pkg/providers/interface.go` was read at `:180-380`; the rest of the file
  (including the `ProviderError` constructors) was not.
- The live behaviour of the user's real decision upstreams (error envelopes,
  statuses) is documented by the adapter comments and the `live_decision`-tagged
  tests; it was NOT exercised. `pkg/decision/live_e2e_test.go` is build-tagged
  and must stay out of the failover pack.
- The user's actual decision models (ids, `internal_model`, credentials) live in
  the running DB — not read.
- The gzipmw self-scoping to `/fe/api/*` is asserted per the project blueprint,
  not re-verified line by line here.

---

## Conventional-commit plan (house style)

One commit per stage, in dependency order; each commit is self-contained and
green on its own.

1. `refactor(credclass): extract limit-class vocabulary into a leaf package`
   — `pkg/credclass/*` (new), `pkg/providers/interface.go` (delegation only),
   `pkg/providers/interface_quota_test.go` + `pkg/providers/ratelimit_classifier_test.go`
   UNCHANGED, `pkg/credclass/classify_test.go` (new).
2. `feat(group-rules): lane-consistent member kind + homogeneity + shadow rules`
   — `pkg/models/group_lane.go` (new), `pkg/models/group.go`,
   `pkg/models/group_rules_test.go` (new functions only).
3. `feat(group-lane): kind-parameterized servability predicate + GroupLane query`
   — `pkg/store/database/store_group_rewrite.go`,
   `pkg/modelscache/models.go` (the forward),
   `pkg/modelscache/models_group_lane_test.go` (new),
   `pkg/store/database/store_group_test.go` (new functions only).
4. `feat(admin-api): decision-group parity across helper / POST / PUT`
   — `pkg/ui/server.go`, `pkg/ui/kind_rules_parity_test.go` (new function only).
5. `feat(decision): decision-group member loop with pre-commit failover`
   — `pkg/decision/handler.go`, `pkg/decision/converter.go`,
   `cmd/main.go` (the injected `skipCacheTTL func() time.Duration` parameter on
   `decision.NewHandler`), `pkg/decision/handler_group_test.go` (new).
   *(superseded by Amendment 1 — the file list GROWS: the three per-layer caches of
   ADR-12, plus the additive policy-only picker in `pkg/grouplb/selector.go` if the
   export is taken — see Amendment 1 §A5, §A13. *(Amendment 5 / ADR-16 — the three
   lane-local layer files are DISSOLVED in favour of the landed `pkg/skipcache` /
   `pkg/modelcache` / sweep.)* *(Amendment 7 / ADR-18 C2-1 — the suppression seam
   file is **`pkg/decision/suppression.go`**.)* )*
6. `feat(proxy): reject decision-lane groups on the chat routes + hide them from /v1/models`
   — `pkg/proxy/handler.go`, `pkg/proxy/handler_anthropic.go`,
   `pkg/proxy/handler_helpers.go`, the three proxy test files (new functions).
7. `docs(planning): add the decision-groups phase-1 backend plan`
   — this file (`.agents/shared/planning/decision-groups/phase1-plan.md`).

House rules that apply: no repo-wide `gofmt -w` (the bar is **zero new
drift** — compare the flagged-line SET against the base, not hunk overlap);
gofmt `-l` over every touched `_test.go` after any fixture sweep; the local
gate is `make test` (CI swallows Go-test failures — `docker-ci.yml:104`);
no FE asset rebuild in Phase 1 (no FE source changed).

---

## Acceptance checklist (the ruling's 22 backend contracts)

| # | Contract | Stage | Evidence |
|---|----------|-------|----------|
| 1 | Homogeneous member kinds; a mixed write is rejected with a dedicated message | 1 | `TestValidateGroupMembers_MixedKindMembers_Rejected` |
| 2 | The lane is inferred by unanimity over the CURRENT member rows; empty/mixed ⇒ unservable | 2 | `TestGroup_GroupLane_InfersChat`, `TestGroup_GroupLane_InfersDecision`, `TestGroup_GroupLane_MixedGroupReturnsFalse`, `TestGroup_RewriteGroupEntries_MixedKindGroup_FailsClosed` |
| 3 | The predicate is kind-parameterized; `""` is chat-only | 2 | `TestGroup_RewriteGroupEntries_DecisionGroup_RejectsLegacyEmptyKindMember` |
| 4 | The shadow target's kind equals the group's lane | 1, 3 | `TestValidateGroupMembers_DecisionGroup_ShadowTargetMustMatchLane` + the parity function |
| 5 | The decision lane's group branch is entered only for a decision-lane group | 4 | `TestDecision_Group_ResolvesToDecisionMember`, `TestDecision_Group_LaneSeamAbsent_FailsClosed400` |
| 6 | Exactly one client write, at the winner | 4 | `TestDecision_Group_NoWriteBeforeWinner` |
| 7 | The attempt order comes from the manager's sweep | 4 | the loop shape (Stage 4 §4.2) + `TestDecision_Group_DefaultPoolFirst_BackupPoolAfter` | *(superseded by Amendment 1 / ADR-5 — the attempt order is the POLICY order, computed in-lane; the sweep's mark-influenced sort is not consumed)* |
| 8 | Tried-set + advance cap = 16 | 4 | `TestDecision_Group_Exhaustion_DoesNotReattemptTriedMember` | *(superseded by Amendment 1 / ADR-6 — the tried-set is now per (member, credential); the 16-member cap still bounds the member walk)* |
| 9 | Per-member credentials by row identity; key `(memberID, memberID)` | 4 | the seam (no `GetModel` on the member path) + `TestDecision_Group_ThroughCachedConfig` | *(extended by Amendment 1 / ADR-6 — within a member, EACH of its own credential refs is attempted before any advance; the rotation is the credential engine's `ExcludeAndReselectForClass`)* |
| 10 | The member conversation key is `""` | 4 | the loop's `""` arguments + `TestDecision_Group_WeightsAffectPick` |
| 11 | Marks fire only on limit-class failures; all-marked ⇒ one honest attempt | 4 | `TestDecision_Group_MarkedMember_DemotedNotFiltered` | *(REPLACED by Amendment 1 / ADR-5 — a marked head is SKIPPED at zero upstream cost (never reordered, never removed); all-marked ⇒ the POLICY head gets exactly one honest attempt (OQ-1))* |
| 12 | Win-clear uses the winner's identity and routes through the decorator forward | 4 | `TestDecision_Group_WinClear_ClearsWinnerNotFailedHead` + the decorator-through test | *(re-derived by Amendment 1 / ADR-12 — the decision lane's win-clear targets its OWN MODEL + UPSTREAM layer caches with the winner's identity; the manager's `ClearGroupMemberCooldown` forward leaves this lane's path)* |
| 13 | Exhaustion ⇒ 502 + `decision_all_members_failed`; no metering row; one event | 4 | `TestDecision_Group_AllMembersFail_502DecisionAllMembersFailed` | *(re-derived by Amendment 1 / ADR-5, ADR-6 — exhaustion counts only GENUINE attempts over member × credential; suppressed attempts never count)* |
| 14 | Metering is success-only on the served member's row; the group id is in the event | 4 | the metering task + the exhaustion test's "no usage row" assertion |
| 15 | `/v1/models` excludes decision-lane groups | 4 | `TestHandleModels_FilterDecisionGroup` |
| 16 | The chat + Anthropic guards reject a decision GROUP with 400 `decision_misroute` | 4 | `TestHandleChatCompletions_RejectsDecisionGroupMisroute`, `TestAnthropicBoundary_DecisionGroupMisroute` |
| 17 | The text lane's behaviour through the manager is unchanged | 1, 2, 5 | the byte-identical pins + the whole existing suites *(Amendment 5 — the invariant is **"no v194 semantic regression"**, re-based on `2b987bf`)* |
| 18 | No new provider — the five vocabulary surfaces are byte-unchanged | — | R3's proof + `TestGroup_AddModel_DecisionMembers_MixedProviders_Accepted` |
| 19 | Flat membership: group-as-member forbidden in both lanes | 1 | `TestValidateGroupMembers_CollectionRuleViolations` (existing row, LEFT ALONE) |
| 20 | Cap 16 unchanged | 1 | the existing cap rows (LEFT ALONE) |
| 21 | The vocabulary lives in the leaf `pkg/credclass`; `pkg/providers` delegates; the decision lane imports the leaf | 0, 4 | the Stage-0 gate + the leaf's own table |
| 22 | The optional `GroupLane` forward + the decorator-through test are merge gates | 2, 5 | `TestCachedModelsConfig_GroupLane_ReachesInnerManager` |
| **AC-R1** | *(Amendment 1 / ADR-10)* Mark never changes the head (pick is policy-only) | Amendment 1 §A2, §A6 | `TestDecision_Group_PolicyPick_IgnoresMarks` |
| **AC-R2** | *(Amendment 1 / ADR-10)* Marked head ⇒ zero-upstream-call **skip**, distinguishable from a failure | Amendment 1 §A3, §A5 | `TestDecision_Group_MarkedHead_ZeroUpstreamCallSkip` |
| **AC-R3** | *(Amendment 1 / ADR-10)* Attempt sequence = policy order minus marked; exhaustion counts only genuine attempts | Amendment 1 §A3, §A4 | `TestDecision_Group_AttemptOrder_PolicyOrderMinusMarked`, `TestDecision_Group_Exhaustion_ExcludesSuppressedAttempts` |
| **AC-R4** | *(Amendment 1 / ADR-10)* Member's own credentials attempted before advancing (**replaces the VOID AC-C1**) — **UPGRADED to necessary-for-correctness by ADR-17**: multiplicity is REPRESENTABLE today (cap 16 `config.go:357`/`:84`; `>=1` with no cap of 1 `:284-286`; admin API `server.go:1161`/`:1628`; FE `DecisionModelForm.tsx:468`) and `len>=2` **already routes through `credentiallb.GetOrSelect`** (`store.go:2605-2610` @`2b987bf`; pool keyed per model ROW ID, `RebindFromStore` `store.go:789`) — **the gap is consumption, not representation; the per-credential member loop IS the fix** | Amendment 1 §A5 | `TestDecision_Group_MemberWithTwoCredentials_LimitClassRotatesBeforeAdvancing` |
| **AC-R5** | *(Amendment 1 / ADR-10)* "Member failed" predicate explicit: own resilience exhausted | Amendment 1 §A5 | `TestDecision_Group_MemberFailedPredicate_OwnResilienceExhausted` |
| **AC-R6** | *(Amendment 1 / ADR-10)* Inactive/chain-only accepted as chain target, not a group candidate, never auto-enabled | Amendment 1 §A14 | `TestDecision_Group_InactiveMember_NotOfferedAsCandidate` (candidate surface) **+ `TestValidateGroupMembers_InactiveChainOnlyTarget_Accepted`** (R4 write-time ACCEPTANCE — added approver iteration-002) |
| **AC-R7** | *(Amendment 1 / ADR-10)* Decision-group members never silently auto-enabled | Amendment 1 §A14 | `TestDecision_Group_MemberNeverAutoEnabled` |
| **AC-R8** | *(Amendment 1 / ADR-10)* Single-member group's resilience consumption defined + documented (option **a** — structural) | Amendment 1 §A11 | `TestDecision_Group_SingleMemberGroup_FullyConsumable` |
| **AC-R9 (ADR-10)** | *(Amendment 1)* Doctrine line present **verbatim** at the seam | Amendment 1 §A8 | `TestDecision_DoctrineLine_PinnedVerbatimAtSeam` |
| **AC-R12 (ADR-9)** | *(Amendment 1)* Text-lane behaviour byte-untouched; every shared-file change lane-gated | Amendment 1 §A17 | the text-lane pins (Stage 5) |
| **AC-R10** | *(Amendment 1 / ADR-10)* Pick vs attempt vs skip are **distinct** in telemetry/events | Amendment 1 §A7 | `TestDecision_Group_PickAttemptSkipEventOutcomes` |
| **AC-R11** | *(Amendment 1 / ADR-12)* The three layers are separable in code and in tests; a credential-layer state change provably cannot alter the group pick; each layer's absence is recorded | Amendment 1 §A13 | `TestDecision_Group_LayerSeparation_CredentialMarkCannotChangePick` |

---

## Planner adjudications

> **Adjudicated by:** the planner (dispatcher), 2026-11-16, on the six
> escalations raised in `## Escalations` of this plan.
> **Effect:** the adjudications below are BINDING on Phase 1. Where they touch
> an earlier section, the earlier section has been corrected in place (the
> corrections are listed in `### Change log` at the end of this section); every
> other section of this plan is unchanged.

### E-1 — ADJUDICATED IN YOUR FAVOUR: D-7 is a BEHAVIOURAL fence, not a file-set fence

Recorded verbatim:

> "D-7 forbids changing text-lane BEHAVIOUR; it does not forbid editing a file
> that must learn a new lane-gated branch. The three `pkg/proxy` consumers stay
> in scope, implemented additively, lane-gated on a shape that cannot exist
> today (a group whose inferred lane is `decision`), with chat-group behaviour
> byte-identical and the existing pins (`handler_groups_test.go:400`,
> `handler_groups_equivalence_test.go`, `group_fallback_hidden_member_test.go`,
> `group_fallback_part2_test.go`, `handler_groups_shadow_member_test.go`) as the
> proof. The alternative reading would ship a decision group served as a chat
> group — a misroute strictly worse than the fence breach, and a direct
> contradiction of contracts 15/16."

**Ordering constraint (the structural argument, not a policy one):** the chat
guard (`pkg/proxy/handler.go:576-582`) and the Anthropic guard
(`pkg/proxy/handler_anthropic.go:272-279`) run **BEFORE** the group rewrite
(`pkg/proxy/handler.go:669`; the Anthropic twin `pkg/proxy/handler_anthropic.go:432`).
A decision group therefore cannot reach the chat executor at all — the misroute
is **structurally impossible**, not merely forbidden: the decision-lane member
never enters the chat race list, never reaches an upstream, and never burns a
token. The `/v1/models` filter (`pkg/proxy/handler.go:349-352`) is a
discovery-surface guard and sits upstream of both.

**Consequence for the plan:** Stage 4's three proxy consumers stay in scope as
written (additive, lane-gated, chat-group behaviour byte-identical). The proof
set named above joins the Stage-5 text-lane regression pins, alongside the
existing pins already listed in `## Test plan → 5`. **No change to the Stage 4
tasks, the Stage 5 pin list, or the blast-radius reasoning was required.**

### E-2 — ACCEPTED AS A DOCUMENTED LIMITATION, with the fail-closed shape

Recorded: **multi-member retyped ⇒ mixed ⇒ unservable (fail closed, pinned) is
the tested form**; **single-member retyped ⇒ re-lanes to chat is accepted and
documented**. Its cure is the D-2/Q3 kind-immutability guard
(`pkg/store/database/store.go:1699` has no immutability guard on a kind
change), which is **OUTSIDE this commission's fence** and is being **surfaced
to the user as a recommended same-release guard** — it is a small, separate,
low-risk change and is NOT implemented here.

**In-place correction applied:** the claim about D-2's verification item (2) is
narrowed to the **multi-member form only** (see the `## Escalations` E-2
paragraph, which now states that the verification item is satisfied ONLY in
its multi-member form). No schema change is proposed, and nothing else in the
plan overclaims the retyped-member behaviour.

**Consequence for the plan:** Stage 2's acceptance criteria and the contract-2
row were already written in the multi-member form
(`TestGroup_RewriteGroupEntries_MixedKindGroup_FailsClosed`); they are
unchanged. The single-member re-lane remains a documented limitation.

### E-3 — RULED: use the INJECTED SEAM, not the `pkg/config` getter

Ruled: the skip-cache TTL arrives as an **injected seam** —
`skipCacheTTL func() time.Duration` — injected from `cmd/main.go`, where
`credLB` / `stickyEngine` / `modelsConfig` are already in scope
(`cmd/main.go:137` / `:144` / `:182-188`). **`pkg/config` gains NO new
interface member** and the boundary stays tighter. The config getter
(`GetUpstreamSkipCacheTTL()`) is the **FALLBACK only if the seam proves awkward
at wiring time** — it is NOT the planned implementation.

**Nil-safe / optional posture (kept):** an absent seam ⇒ the TTL falls back to
a fixed/default window (`skipcache.DefaultTTL`) and the demotion path still
runs — it must fail **SOFT**: never panic, never skip the demotion path (a
missing TTL must not silently disable the marks).

**In-place corrections applied:**
- the Stage 4 seams-table row for the handler config seam now describes the
  injected-seam form (no `pkg/config` change, the local `configGetter`
  interface unchanged, fail-soft fallback);
- risk **R7** is reworded from "the `pkg/config` getter addition" to "the
  injected seam adds one parameter to `decision.NewHandler` and is wired in
  `cmd/main.go` — no `pkg/config` change".

**Consequence for the plan:** the commit-plan line for Stage 4 still mentions
`pkg/config/config.go` as an alternative; that mention is superseded by this
adjudication (the seam is the planned form; the getter is the fallback only).

### E-4 — APPROVED

The handler stamps `outcome.UpstreamStatus = resp.StatusCode`; the
`UpstreamProvider.InspectResponse` signature stays **unchanged**
(`pkg/decision/converter.go:88`); the adapters' already-parsed error-envelope
fields (`pkg/decision/openaidecision.go:162-175`,
`pkg/decision/typesafe.go:432-444`) carry the rest.

**The classification input is materially identical** (the raw upstream status +
the error envelope) even though the capture point moved from the adapter to
the handler: the lane classifies on exactly the same four facts
(`status`, `errType`, `errCode`, `message`).

**ADDED acceptance criterion (E-4 pin):** a test proving the stamped status
actually drives classification — a **429** from a member must produce a
limit-class demotion (a mark IS written for `(credential, wire-model)` and
`(provider, wire-model)`), and a **400 / 413 / 415 / 422** must **NOT** write a
mark but must still **advance** to the next member. This is the pin that stops
a silent "status always zero ⇒ nothing ever demotes" implementation.

**In-place correction applied:** the test row
`TestDecision_Group_StampedStatusDrivesClassification` was added to the
Stage-4 group of the `## Test plan` table (harness: httptest fake upstream +
skip cache).

### E-5 — APPROVED

One exhaustion code (D-5) is shared by the zero-attempted sub-case; the
distinction lives in `members_attempted: 0` on the **event** and in the **log
line**, never on the wire. **The wire contract is deliberately coarse:** a
client sees one machine code (`decision_all_members_failed`) for both "no
member could serve" and "every member failed"; the per-request detail is an
operator concern (event + log), not a client contract. No change to the plan's
Stage 4 implementation is required — the plan already writes exactly one code
and carries the distinction in the event and the log.

### E-6 — NOT ADJUDICATED BY THE PLANNER; kept as written and explicitly marked as a SCOPE REDUCTION PENDING USER SIGN-OFF

The member-level implementation stands as written in this plan (Stage 4 §4.2
and the loop shape). It is **not** silently accepted: the commission's R2 text
demands "every member × credential genuinely failed THIS request", so
member-level-only is a **reduction of a stated requirement**, not an
implementation detail — hence the explicit scope-reduction label below.

### E-6 — Scope reduction: member-level fallback only (pending user sign-off)

> ⚠️ *(superseded by Amendment 1 / ADR-6 + ADR-11 — the planner's "pending user sign-off"
> framing is RETRACTED by the user ruling: member-level is the order of operations,
> and per-credential attempts are implemented (A-1 / AC-R4). The reduction below is
> therefore NOT proposed for v1; it is retained for the record only.)*
>
> ⚠️ *(further superseded by Amendment 2 / ADR-1 — the E-2 "accepted limitation"
> record is RETIRED: ADR-1 blocks kind retyping for group members in this release,
> so the single-member re-lane hole is CLOSED BY GUARD. See Amendment 2, Fold 1 /
> Stage 3b.)*

**(a) What is reduced.** On the decision lane a member is attempted **once**
per request: exactly **one** credential is picked for it through the existing
seam — `ResolveInternalConfigWithAffinity(memberID, memberID)`
(`cmd/main.go:324-328` → row identity, `pkg/store/database/store.go:2552-2567`)
— i.e. one weighted pick from that member's credential refs. The lane does
**not** iterate the member's remaining credentials: a member whose credential
set has 2+ entries contributes **one** attempt per request, not one attempt
per credential. The fallback advances **between members**, bounded by the
tried-set and the 16-member cap.

**(b) The concrete path to full member × credential (the remedy, if the user
rejects the reduction).** The credential engine is already in scope at
`cmd/main.go:137` (`credLB := modelsMgr.Engine()`), and
`ExcludeAndReselectForClass` (`pkg/credentiallb/engine.go:657` @`2b987bf`; the legacy
delegate `:644-652`) is reachable **without importing `pkg/proxy`** — the two
production call sites that use it today live in `pkg/proxy`
(`pkg/proxy/internal_handler.go:263`, `pkg/proxy/race_coordinator.go:1900`),
which is precisely why the decision lane cannot use it today (the import
guard bans `pkg/proxy`, `pkg/decision/handler_test.go:40`, `:90`).
*(characterization corrected by Amendment 3 / ADR-14 — the *call sites* of the
credential-LB exclusion live in the text lane today, but the **package itself is
import-legal from `pkg/decision`** (verdict: LEGAL, unconditionally) —
`pkg/decision` already carries `pkg/credentiallb` in its transitive closure
(`decision → auth → store/database → credentiallb`, a non-test dep). The
per-credential rotation is therefore reachable WITHOUT any new guard argument.)*
**Stated plainly: whether `pkg/credentiallb` is permitted by the
`pkg/decision` import guard is UNVERIFIED and must be checked before
committing to that path.** A read-only static check performed while recording
this adjudication (2026-11-16, worktree @ `24c51ef`) found `pkg/credentiallb`
importing only `pkg/models` and its own subpackages
(`pkg/credentiallb/binding`, `/engine`, `/selector`, `/testhooks`) — no
`pkg/proxy` edge — and `pkg/models` importing only `pkg/crypto`; but the
formal guard is a `go list -deps` walk of the package closure
(`pkg/decision/handler_test.go:90`, `helpers_test.go:19-39`) and has **not**
been run, so the permission question stays **UNVERIFIED** until that check
passes.
*(CLOSED by Amendment 3 / ADR-14 — the permission question was a **no-op**: the
verdict is **LEGAL, unconditionally**; the guard tests PASS with literal
`=== RUN` / `--- PASS` output, the `go list -deps` closures are clean, a real
direct-import scratch passed ALL guards (positive control), and `pkg/decision`
**already** carries `pkg/credentiallb` in its transitive closure
(`decision → auth → store/database → credentiallb`, a non-test dep) — the
guard has been passing with that edge present all along. Characterization
corrected per ADR-14 item 1: `pkg/credentiallb` imports `pkg/models`
(→ `pkg/crypto`) and has **8 files** — it is **NOT stdlib-only**, and the
3-file phrasing above is withdrawn.)* The remedy, if required, is: (i) verify the guard permits
`pkg/credentiallb`; (ii) add a per-member credential iteration to the Stage-4
loop (one attempt per credential ref, the credential-level tried-set mirroring
`rc.credTriedAcrossRace`, `pkg/proxy/handler.go:887-892` / `:951-960`); (iii)
use `ExcludeAndReselectForClass` for the per-class cooldown so the
quota/rate-limit policy stays owned by `pkg/credentiallb`
(`pkg/credentiallb/failure_class.go`); (iv) re-pin the exhaustion test to
count **credential** attempts rather than member attempts. **This remedy is
NOT implemented in this plan.**

**(c) Practical blast radius.** The reduction only bites for decision models
that carry **2+ credential refs**. The user's two live decision models are
**one credential each** (per the commission brief), which is exactly why
member-level fallback is proposed for v1 at all: for the shipped
configuration, "every member × credential failed" and "every member failed"
are the same statement. The reduction therefore does not weaken the user's
current deployment; it becomes material only if a decision model is later
given a second credential (the credential-pool shape the text lane supports
via `pkg/credentiallb`). **Pending explicit user sign-off.**

### Change log for this adjudication

In-place corrections applied to the sections above (everything else unchanged):

1. **Stage 4 seams table**, row "Handler config seam" — rewritten to the
   injected-seam form (E-3a).
2. **Risks**, row **R7** — reworded to the injected-seam form (E-3b).
3. **`## Escalations` E-2 paragraph** — the D-2 verification claim narrowed to
   the multi-member form, with the Q3 cure recorded as an out-of-fence,
   user-surfaced recommendation (E-2).
4. **`## Test plan`**, Stage 4 group — one added test row,
   `TestDecision_Group_StampedStatusDrivesClassification` (the E-4 pin).

No section was restructured, renumbered, or reflowed. No code was written, no
test was run, no commit was made.

---

## Amendment 1 (2026-11-16) — user ruling R1–R5, OQ closures, per-layer caches

> **Authority:** `.agents/shared/planning/decision-groups/decisions.md` — the
> **ADRs — USER RULING** block (`decisions.md:573-807`): **ADR-5** (pick is
> policy; a mark suppresses an attempt), **ADR-6** (R3 order of operations; the
> retraction of ADR-2's framing), **ADR-7** (R4 chain-only / inactive models),
> **ADR-8** (R5 racing must be consumable), **ADR-9** (coordination; the text
> lane is a parallel commission), **ADR-10** (AC-R1..AC-R10), **ADR-11** (the
> four OQ closures + **AC-C1 FORMALLY VOID**), **ADR-12** (RULING-3: the three
> per-layer caches + AC-R11).
> **Precedence:** where this amendment and an earlier section of this plan
> disagree, **this amendment wins** (`decisions.md:575-577`: "Where the two
> disagree, this section wins"). The earlier text is **not silently rewritten** —
> it carries `*(superseded by Amendment 1 / ADR-…)*` markers in place.
> **Nothing here is code.** No build, no test run, no commit.

### A1 — What changed, at a glance

| # | Change | ADR | Criteria |
|---|--------|-----|----------|
| I-1 | The decision lane's ORDERING is **policy-only**; the four-phase sweep's mark-influenced sort is **not** consumed by this lane | ADR-5 (R1, consequences 1), ADR-12 (GROUP layer) | AC-R1, AC-R11 |
| I-2 | Contract 11 is **replaced**: a marked head is **skipped at zero upstream cost** (never reordered, never removed) | ADR-5 (R2) | AC-R2, AC-R3 |
| I-3 | Exhaustion accounting is **re-derived**: the 502 fires only after every **member × credential** was genuinely **attempted and failed**; suppressed attempts never count | ADR-5, ADR-6, ADR-10 | AC-R3, AC-R4, AC-R5 |
| A-1 | **Per-credential attempts within a member** before any advance (the rotation mechanism = an `ExcludeAndReselectForClass`-equivalent) | ADR-6 (R3) | AC-R4 |
| A-2 | The **member-failed predicate** is explicit: true only when the member's own resilience (per-credential attempts; chain ABSENT per OQ-2) is exhausted | ADR-6 | AC-R5 |
| A-3 | The pick is computable **independently of any failure state**; the head reported in telemetry is the **policy head** | ADR-5 (R1) | AC-R1 |
| A-4 | **Pick vs attempt vs skip** become distinct outcomes in the decision-lane event set | ADR-10 | AC-R10 |
| A-5 | The **doctrine line is pinned verbatim at the seam** | ADR-10 (AC-R9), ADR-5's preamble | AC-R9 (ADR-10) |
| OQ-1 | **All-marked** ⇒ the **policy head receives exactly one honest attempt**; head suppression lifts for that request only | ADR-11 | AC-R2, AC-R3 |
| OQ-2 | **D5 stands** — `fallback_chain` stays **forbidden** for decision models; the chain clause is vacuous | ADR-11 | AC-R5 (chain clause) |
| OQ-3 | **Structural loop contract** (option (a)): a single-member list is fully valid and fully consumable; **no hedge/race engine** this release | ADR-11, ADR-8 | AC-R8 |
| OQ-4 | **Disabled group members are NOT attempted** (the predicate is unchanged); chain targets and group members are **two populations** | ADR-11, ADR-7 | AC-R6, AC-R7 |
| RULING-3 | **Three per-layer caches** — GROUP (policy facts, read by the pick only) / MODEL (model-attributable failures; **chain reader ABSENT**) / UPSTREAM (credential failures, read by per-credential selection only). **No cross-layer reads.** | ADR-12 | AC-R11 |
| A-6/A-7 | Inactive / chain-only models are **valid chain targets, never defects**, are **not** group candidates, and are **never auto-enabled** | ADR-7, ADR-11 | AC-R6, AC-R7 |
| — | **AC-C1 is FORMALLY VOID**; **AC-R4 replaces it** | ADR-11 | — |

**ID-collision note (read before citing):** the ADRs carried the same `AC-R9` id
twice — ADR-9's criterion ("text-lane behaviour is byte-untouched by this
release") has since been **renumbered AC-R12** (a renumbering note is recorded
in `decisions.md` ADR-10), while ADR-10's AC-R9 is "doctrine line present
**verbatim** at the seam". They are **different criteria**. This plan cites
them as `AC-R12 (ADR-9)` and `AC-R9 (ADR-10)` respectively.

### A2 — I-1: the ordering is POLICY-ONLY (ADR-5 R1/R2; ADR-12 GROUP layer)

**The change.** "Reuse the four-phase sweep for ordering" is **withdrawn for the
decision lane**. The sweep's Phase-4 head is sorted by `memberDemotionExpiry`
(soonest-expiring mark first — `pkg/store/database/store_group_rewrite.go:229-242`,
`:273-282`), so consuming that order would let a **mark decide the pick** —
forbidden by R1 (`decisions.md:586-588`, consequence 1 at `:601-607`).

**What IS reused from the sweep (policy facts only):**

| Reused | Where it lives | Why it is policy |
|--------|----------------|------------------|
| Pool partitioning (default pool before the backup pool) | `pkg/store/database/store_group_rewrite.go:359-377` (`defaultRefs` / `backupRefs`), the engine's two-pool layout `pkg/grouplb/engine.go:11-25` | pool membership is configuration, not failure state |
| Weights (`MemberRef.Weight` ← `GroupMember.Weight`) | `pkg/store/database/store.go:1677-1686`, `pkg/grouplb/engine.go:57-62` | configuration |
| The candidacy predicate (exists + enabled + lane-matching) | the kind-parameterized `isServableGroupMemberBase` (Stage 2) | candidacy is not failure state — and the lane applies the SAME shape itself |
| Configured member order (the tie-break / the post-head order) | the member list order (`GroupMember` position) | configuration |

**What is NOT reused:** the mark-influenced Phase-4 sort, `memberDemotionExpiry`,
the engine's cooldown-driven offer loop (`GetOrSelectWithRepair`), and
`ExcludeGroupMember` as an **ordering** device.
> ⚠️ *(SUPERSEDED by Amendment 5 / ADR-16 — **I-1 DISSOLVES**: at `2b987bf` the sweep
> is already policy-headed and cache-blind (`pkg/store/database/store_group_rewrite.go:88-99`,
> `:281`, `:284-292`), so the decision lane **consumes the sweep directly** — no
> separate ordering path, no lane-gated parameter, and **AM-1** (the exported
> `pkg/grouplb` policy selector) dissolves with it. See `## Amendment 5` §E1, §E2.)* The decision lane writes **no**
grouplb member cooldown and reads **no** grouplb cooldown ⇒ the text lane's
engine state is untouched by this lane (ADR-9's byte-untouched fence).

**The policy pick (the replacement for the sweep's ordering).**

```
policyFacts(groupID) → { lane, defaultPool[], backupPool[] }      // GROUP layer: policy facts ONLY
policyHead(policyFacts)  = fresh weighted pick from the default pool   // weights, no marks, no binding
policyOrder(policyFacts) = [head] + remaining default members (configured order)
                            + backup members (configured order)
```

- **Fresh weighted pick, no binding** — D-6 stands (no conversation key exists
  on this lane: `pkg/decision/handler.go:738`, `cmd/main.go:327`); the engine's
  empty-key semantics are "fresh weighted pick, no binding"
  (`pkg/models/group_rewrite.go:88-90`).
- **The weighted selector is unexported today** (`pkg/grouplb/selector.go:26`,
  `:52` — `newWeightedSelector` / `pick`; the file is a mechanical clone of
  `pkg/credentiallb/selector.go`). `pkg/decision` therefore needs a
  **policy-only picker**. Planned: an **additive exported** policy selector in
  `pkg/grouplb` (e.g. `NewPolicySelector(refs []MemberRef)` + `PickWeighted(r
  float64)`) — a NEW API, no change to any existing engine behaviour, hence
  text-lane-neutral. Fallback if the export is refused: a small local picker in
  `pkg/decision` (the repo already carries two clones of this algorithm, so a
  third is precedented but less DRY).
> ⚠️ *(SUPERSEDED by Amendment 5 / ADR-16 — **AM-1 DISSOLVES**: the lane needs no
> picker of its own; the landed sweep's Phase-1 head is the engine's own
> sticky/weighted offer (`pkg/store/database/store_group_rewrite.go:101-112`),
> which is already policy. No `pkg/grouplb` export is required.)*
- **Structural guarantee (AC-R1, AC-R11):** the pick function's input type
  carries **only** policy facts — it has no parameter through which a mark, a
  cooldown or any other failure state could reach it. "A credential 429 can
  never change the group pick" therefore holds **structurally, not by
  discipline** (`decisions.md:788-790`).

### A3 — I-2: contract 11 is REPLACED (ADR-5 R2)

**Old contract 11** ("marks fire only on limit-class failures; all-marked ⇒ one
honest attempt against the soonest-expiring mark") is **superseded**. The
decision lane's rule is now:

1. Marks fire **only on limit-class failures** — unchanged (ADR-5 does not
   touch the writer's trigger).
2. A marked head is **skipped at zero upstream cost** — it is **not reordered**
   and **not removed**. The head stays the head (policy); its *attempt* is
   suppressed.
3. The attempt sequence = **the policy order minus the marked candidates**
   (no reordering, no removal from the sequence — a suppressed candidate is
   recorded as a **skip**, not a failure).
4. Exhaustion accounting counts **only genuine attempts**.

**The doctrine line is extended, not weakened** — ADR-5's preamble
(`decisions.md:579-582`) pins it verbatim:

> The cache is an optimization, not the truth: it may reorder candidates, never exclude them — and it
> may suppress an ATTEMPT, never a PICK or a CHAIN.

The text lane's copy (`pkg/skipcache/skipcache.go:13-26`) is **unchanged** in
this release (collision hotspot, ADR-9).

### A4 — I-3: exhaustion accounting re-derived (ADR-5 / ADR-6 / ADR-10)

**Old criterion (withdrawn):** "every base-servable member of both pools has
had its honest attempt ordered". **New criterion:**

> The 502 `decision_all_members_failed` fires **only** when (a) **every
> member × credential** pair has been **genuinely attempted** and **failed**,
> and (b) no unattempted candidate remains.
> **Suppressed attempts are not attempts and not failures** — they never
> contribute to the failure count. Per-credential attempts count
> **individually**.
> *(re-derived by Amendment 2 / ADR-13 (AM-4) — "genuinely failed" is **per the
> failure-class policy**: a member that 5xx'd HAS genuinely failed for this
> request, and its remaining credentials are NOT attempted (the advance is
> immediate); rotation happens only for rotation-eligible classes (limit-class +
> upstream 401/403), and 400/413/415/422 + client-cancel fail fast.)*

**Re-derived detection (the decision procedure the loop follows):**

```
attempted   := {(member, credential) genuinely attempted}      // never includes a skip
failed      := {(member, credential) that failed}
skipped     := {(member, credential) suppressed by a mark}
exhausted?  := attempted == failed                              // every genuine attempt failed
              AND no (member, credential) pair is left unattempted-and-unsuppressed
              AND the candidate list is fully walked
```

**Event payload (the wire stays coarse — E-5):** `outcome=all_members_failed`
plus `attempted` (genuine attempts), `skipped` (suppressed attempts),
`members_attempted` (distinct members attempted). The **wire** carries only the
one code (`decisions.md:212`) — the detail is operator-facing, never
client-facing.

**Test consequences (see A16):** the "all members fail" case must assert
`attempted == Σ(member credential refs)` and `skipped == 0`; the marked-member
case must assert `attempted` **excludes** the suppressed pair.

### A5 — Stage 4 RE-DERIVED: per-credential attempts (ADR-6; A-1 / A-2; AC-R4 / AC-R5)

This replaces the superseded Stage-4 seam table and loop shape.

| Seam | File:line | What changes |
|------|-----------|--------------|
| GROUP-layer cache (NEW) | `pkg/decision/layer_group.go` (new) *(SUPERSEDED by Amendment 5 / ADR-16 — DISSOLVED: the GROUP layer IS the sweep + `pkg/grouplb` policy; no lane-local GROUP cache.)* | `type groupPolicy struct { GroupID string; Lane models.GroupLane; Default []policyCandidate; Backup []policyCandidate }`; `type policyCandidate struct { MemberID string; Weight float64; Pool string; Credentials []models.CredentialRef }`. **Policy facts ONLY** — no failure state in the type. **Reader: the pick only.** Writer: the group write path (the modelscache write-through drops the affected group's entries). Fallback if the memo's invalidation cannot ride that path: rebuild per request (N ≤ 16 member rows) — still policy-only, still structurally isolated. |
| Policy pick (NEW) | `pkg/decision/layer_group.go` | `policyHead(...)` / `policyOrder(...)` per A2 — fresh weighted pick, no binding, no marks, no cooldowns. |
| MODEL-layer cache (NEW) | `pkg/decision/layer_model.go` (new) *(SUPERSEDED by Amendment 5 / ADR-16 — DISSOLVED: the lane consumes the landed `pkg/modelcache` (process-wide instance); the CHAIN reader stays ABSENT per OQ-2.)* | Model-attributable failures. **Reader: the member loop's per-member attempt/suppression decision only.** **The chain reader is ABSENT on this lane (OQ-2) — recorded as absent, NOT faked** (`decisions.md:795-798`). Writer: model-attributable failure classification. |
| UPSTREAM-layer cache (NEW) | `pkg/decision/layer_upstream.go` (new) *(SUPERSEDED by Amendment 5 / ADR-16 — DISSOLVED: the lane consumes the landed `pkg/skipcache` (process-wide instance).)* | Credential failures. **Reader: per-credential selection only.** Writer: credential-failure classification. **A separate instance from the text lane's global `*skipcache.Cache`** *(ADR-15 / CG-2 — never shared; pinned by `TestDecision_Group_SkipCacheInstance_NotSharedWithTextLane`)* *(SUPERSEDED by Amendment 5 / ADR-16 — the lane-local cache file is DISSOLVED: the lane consumes the **process-wide** `pkg/skipcache` (UPSTREAM) and `pkg/modelcache` (MODEL) instances; the fence is LAYER separation, not instance separation — see `## Amendment 5` §E2)* (`cmd/main.go:240-241`) so a decision-lane mark can never influence text-lane ordering (ADR-9's byte-untouched fence). |
| Doctrine pin (A-5) | **`pkg/decision/suppression.go`** (the suppression seam — NAMED by Amendment 7 / ADR-18 C2-1; the dissolved `layer_upstream.go` is retired) | The file's doc comment carries the **verbatim** extended doctrine line (A3). This is **the seam** where AC-R9 (ADR-10) is satisfied; a source-literal pin test enforces it (A16). |
| Per-credential attempt loop (A-1) | `pkg/decision/handler.go` | Within a member, attempt the member's **own** credential refs before any advance (2..16 refs are representable — `pkg/models/config.go:357`; cap `pkg/ui/server.go:698-700`; a decision model may carry 2..16 same-provider refs — `pkg/models/config.go:284-286`, `:287-300`). The **rotation mechanism** is the credential engine's `ExcludeAndReselectForClass` (`pkg/credentiallb/engine.go:657` @`2b987bf`) — `pkg/credentiallb` is **import-guard-legal** from `pkg/decision` (**ADR-17 upgrade: AC-R4 is necessary-for-correctness** — multiplicity is REPRESENTABLE (cap 16 `config.go:357`/`:84`; `>=1` no cap-of-1 `:284-286`; admin API `server.go:1161`/`:1628`; FE `DecisionModelForm.tsx:468`) and `len>=2` already routes through `credentiallb.GetOrSelect` (`store.go:2605-2610` @`2b987bf`; pool keyed per model ROW ID, `RebindFromStore` `store.go:789`) — the gap is CONSUMPTION, and this loop is the fix) (ADR-6: the guards ban only `pkg/proxy` / `pkg/imggen`, `pkg/decision/handler_test.go:40` / `:65`). The first credential still comes from the **existing** seam `safeResolveCredential(memberID)` (`cmd/main.go:324-328` → `pkg/store/database/store.go:2552-2567`); after a rotation the engine's rebound pick is returned by the same seam ⇒ **no new resolution seam and no new manager method** (D-8's budget is untouched). The composed credential tuple mirrors `resolveWithCredential` (`pkg/store/database/store.go:2489-2508`): provider = credential's, baseURL = the member row's `InternalBaseURL` else the credential's, wire model = the member row's `InternalModel` (peak-hour override applies). |
| Member-failed predicate (A-2) | `pkg/decision/handler.go` | `memberResilienceExhausted(member)` is TRUE **only** when the member's own resilience is consumed: every one of the member's credential refs has been genuinely attempted and failed (or is unattemptable), **and** the chain clause is ABSENT (OQ-2 — no chain is consulted). Chain targets and group members are **two populations** and are never conflated here (OQ-4). *(re-derived by Amendment 2 / ADR-13 (AM-4) — the predicate is CLASS-CONDITIONAL: failed = (own rotation-eligible credentials exhausted) OR (a 5xx) OR (an advance-blocking class 400/413/415/422 / client-cancel).)* |
| Attempt suppression | `pkg/decision/handler.go` | `decideAttempt(member, credential)` reads the MODEL layer (member-level) and the UPSTREAM layer (credential-level) and returns **attempt** or **skip**. A skip performs **zero upstream calls** and is recorded as a skip, never a failure (AC-R2). |
| OQ-1 head lift | `pkg/decision/handler.go` | When **every** candidate (member × credential) is marked, the **policy head's** first credential attempt is **unsuppressed** — exactly one honest attempt for that request; head suppression lifts **for that request only** (ADR-11:748-755). |
| Exhaustion | `pkg/decision/handler.go` | Per A4 — the 502 + `decision_all_members_failed` fires only when every member × credential was genuinely attempted and failed. **Suppressed attempts never count as failures.** No usage row; exactly one `all_members_failed` event (D-5). |
| Win-clear (re-derived) | `pkg/decision/handler.go` | The **WINNER's** identity (member id + credential id + internal model) clears the decision lane's **own** MODEL + UPSTREAM layer marks. The manager's `ClearGroupMemberCooldown` forward is **no longer on this lane's path** (the decision lane writes no member cooldowns ⇒ no cross-lane engine state); the text lane's win-clear (`pkg/proxy/handler.go:905-920`) is untouched. |
| Event vocabulary (A-4) | `pkg/decision/handler.go` | New distinct outcomes: **`member_picked`** (the policy head), **`member_attempted`** (a genuine upstream attempt), **`member_suppressed`** (suppressed — zero upstream call), **`member_failed`** (the member's own resilience exhausted). The existing attempt-failure outcomes (`upstream_transport_error`, `deadline`, `inspect_error`, …) stay and describe **why** an attempt failed. `all_members_failed` stays as the exhaustion outcome (AC-R10). |
| Commit point | `pkg/decision/handler.go:517` / `:541` / `:543` | **Unchanged** — the loop still writes exactly once, at the winner (contract 6). |
| Metering | `pkg/decision/handler.go:554-556`, `:609-641` | **Unchanged** — success-only; the served member's row is metered; no row on the exhaustion path. |

**The re-derived loop (replaces the superseded Stage-4 pseudo-shape):**

```
policy := policyOrder(groupPolicyFacts)          // policy only — no marks, no cooldowns
head   := policy[0]
attempted, failed, skipped := {}, {}, {}
for member in policy:                             // policy order, each member once
    for {
        cred := resolveCredential(member)         // the engine's pick for (memberID, memberID)
        if cred not found or (member,cred) in attempted:
            break                                  // this member's own resilience is consumed
        if marked(member, cred) and not allMarked: // OQ-1 lifts suppression for the head only
            skipped.add(member, cred); publish(member_suppressed); continue
        // *(ADR-15 D-C — if EVERY credential of this member is marked/cooling, the member gets
        //   exactly ONE honest attempt on its POLICY-FIRST credential; if that attempt genuinely
        //   fails per the AM-4 class policy, the member fails and the loop advances.
        //   The cache is never a failure oracle at ANY layer.)
        // *(Amendment 5 / ADR-16 — the precondition is the credential engine's exported
        //   cooldown reader: credLB.CooldownUntil(memberID, credID)
        //   (pkg/credentiallb/engine.go:468-477) — "every credential cooling or marked"
        //   is decided by that reader plus the UPSTREAM layer's marks.)*
        attempted.add(member, cred)
        outcome := attempt(member, cred)          // steps 8-14, NO writes
        if outcome.ok:  winner = (member, cred); break
        failed.add(member, cred)
        // *(superseded by Amendment 2 / ADR-13 (AM-4) — rotation is CLASS-CONDITIONAL:
        //   rotation-eligible (limit-class + upstream 401/403) ⇒ rotate within the member
        //     (upstream-layer write per classification);
        //   5xx ⇒ ADVANCE immediately — no rotation, NO mark, NO cooldown write;
        //   400/413/415/422 + client-cancel ⇒ fail-fast advance-blocking (cred_failover_class.go:162/:190))*
        // if limitClass(outcome): rotate(member, cred)   // ExcludeAndReselectForClass → the next credential
        publish(member_attempted / the failure outcome)
        publish(member_failed) when memberResilienceExhausted(member)
    }
    if winner: break
if no winner and every candidate was genuinely attempted and failed:
    → 502 decision_all_members_failed (attempted, skipped, members_attempted)
```

**Divergences from the text lane (recorded for the parallel commission,
ADR-9):** (i) the decision lane's pick is policy-only and never reorders on
marks; (ii) the all-marked case attempts the **policy head**, not the
soonest-expiring member; (iii) within a member the lane rotates **credentials**,
not chain entries (the chain clause is vacuous — OQ-2).

### A6 — A-3: the pick is failure-independent; telemetry reports the POLICY head

- The pick is a pure function of pools + weights + configured order — **no
  failure state may reach it** (AC-R1).
- The head reported in telemetry is the **policy head** — the member the policy
  would pick **regardless of marks**. When that head is marked, the event
  stream shows `member_picked` (head) **and** `member_suppressed` (the suppressed
  attempt) — never a "different head" (AC-R1 + AC-R10).

### A7 — A-4 / AC-R10: the event vocabulary (pick vs attempt vs suppress)

> ⚠️ *(ADR-15 W-event-rename — the suppression outcome is **`member_suppressed`**, not
> `member_skipped`: v193 already uses a `member_skipped` namespace (**68 hits** in the
> v193 tree) and v194 owns the `model_fallback_*` family — reusing either name would
> make the decision lane's telemetry indistinguishable from text-lane events.
> `member_suppressed` is lane-scoped and matches the pick/attempt/**suppress**
> vocabulary of AC-R10. No test was renamed; only the asserted literal changes.)*

| Outcome | Meaning | Upstream calls |
|---------|---------|----------------|
| `member_picked` | the **policy head** (and, per member, the member the policy order names) | 0 |
| `member_attempted` | a genuine upstream attempt was issued for (member, credential) | 1 |
| `member_suppressed` | the attempt was **suppressed by a mark** (zero upstream cost) | **0** |
| `member_failed` | the member's own resilience is exhausted (A-2) — the advance signal | — |
| `all_members_failed` | exhaustion (D-5) | — |

The existing decision outcomes (`model_not_found`, `decision_misroute`,
`credential_unresolved`, `unsupported_provider`, `build_failed`,
`upstream_transport_error`, `deadline`, `upstream_read_failed`,
`response_too_large`, `inspect_error`, `success`, `error`) are **unchanged**
and continue to describe why an attempt failed.

### A8 — A-5 / AC-R9 (ADR-10): the doctrine line pinned verbatim AT THE SEAM

**The seam:** **`pkg/decision/suppression.go`** — the decision lane's
attempt-suppression seam (the module that decides attempt-vs-skip). Its
doc comment carries the literal, byte-for-byte:

> The cache is an optimization, not the truth: it may reorder candidates, never exclude them — and it
> may suppress an ATTEMPT, never a PICK or a CHAIN.

**Enforcement:** a source-literal pin test reads that file and asserts the
string is present verbatim (`TestDecision_DoctrineLine_PinnedVerbatimAtSeam`).
*(ADR-14 item 2 — the guard is RAW TEXT over every `.go` file in the package dir,
`_test.go` included: the seam file's doc comment must carry the doctrine line
**without naming the banned subtrees' literal import paths**, and the same goes
for the pin test's own comments.)*
The **text lane's** copy (`pkg/skipcache/skipcache.go:13-26`) is **not**
rewritten in this release (ADR-9 collision hotspot) — the two copies coexist,
and the text-lane semantics are unchanged.

### A9 — OQ-1: the all-marked case (ADR-11)

**Ruled:** suppression **never removes the last attempt**. When **every**
candidate is marked, the **POLICY HEAD receives exactly one honest
(unsuppressed) attempt** — head suppression lifts **for that request only**
(`decisions.md:750-752`).

**Recorded divergence (flagged for the parallel text-lane commission):** the
text lane's current rule serves the **soonest-expiring** marked member
(`pkg/store/database/store_group_rewrite.go:229-243`). Choosing the
soonest-expiring member would let a **mark decide the head**, violating R1
(`decisions.md:753-755`). This release is **lane-gated** — the text lane is
untouched — and the divergence is **flagged** for the parallel commission.

### A10 — OQ-2: D5 stands (ADR-11) — and is **ENFORCED** (ADR-17)

`fallback_chain` remains **FORBIDDEN** for decision-kind models
(`pkg/models/config.go:301-304` — **byte-identical at both bases**
(`git diff 24c51ef 2b987bf -- pkg/models/config.go` = empty); enforced at the DB
write path (`pkg/store/database/store.go:2063` region), the UI mirrors
(`pkg/ui/server.go:1180-1187` POST / `:1645-1652` PUT), and the FE
(`DecisionModelsTab.tsx:131` stamps `fallback_chain: []`) — **ADR-17**). Recorded verbatim (`decisions.md:757-761`):

> "no equivalent concept on this lane; deliberately not built"

Per-member resilience on the decision lane = **per-credential attempts**
(A-1 / AC-R4). Chains-on-decision-models = **future work**.

### A11 — OQ-3: the structural loop contract (ADR-11; ADR-8 AC-R8)

**Option (a) — structural.** The member loop treats a **single-member list as
fully valid and fully consumable**: the loop contract can issue multiple
attempts (per credential) and must be exhausted before the request may fail.
**No speculative / hedge race engine** on the decision lane this release.
**"Racing consumed" is defined by the loop contract reaching its end**, not by
a live race (`decisions.md:763-767`). A single-member, single-credential group
therefore has a defined, testable resilience-consumption path: one attempt,
then exhaustion (AC-R8).

### A12 — OQ-4: disabled group members (ADR-11)

**Disabled group members are NOT attempted** — the request-time servability
predicate stays **exactly as today** (disabled ⇒ unservable; the kind-parameterized
form from Stage 2 keeps the same shape). ADR-7 / R4 applies to **CHAIN TARGETS
and VALIDATION only**: inactive / chain-only models are valid chain targets,
never flagged as defects, and are **not** surfaced as group candidates
(A-6). **Chain targets and group members are two populations — do not conflate
them in validation or in tests** (`decisions.md:773`).

### A13 — RULING-3 / ADR-12: the three per-layer caches (+ AC-R11)

| Layer | Question | Contents | Reader | Writer | Status on this lane |
|---|---|---|---|---|---|
| **GROUP** | "is this decision member servable in this group?" | **policy facts only** (lane, pools, weights, order, candidate set) | the **PICK only** | the group write path | REAL — this is the structural expression of R1 |
| **MODEL** | "is this decision MODEL limited?" | model-attributable failures | the **member loop's per-member attempt/suppression decision** | model-attributable failure classification | REAL, but its **chain reader is ABSENT** (OQ-2) — **recorded as absent, not faked** |
| **UPSTREAM** | "is this provider/credential working?" | credential failures | **per-credential selection only** | credential-failure classification (the `ExcludeAndReselectForClass`-equivalent) | REAL — its reader is the per-credential selection inside the member loop. *(ADR-14 item 3 — `pkg/grouplb` **bans** `pkg/credentiallb` (`pkg/grouplb/engine_test.go:401`): the upstream-layer reader/writer and the per-credential rotation live **IN `pkg/decision`** and must NEVER be pushed down into `pkg/grouplb` — a structural fence, not a style preference.)* *(Amendment 5 / ADR-16 — the reader is now the **landed** `pkg/skipcache` process-wide instance (`cmd/main.go:242`, `:255`), not a lane-local file.)* |

**No cross-layer reads.** *(Amendment 5 / ADR-16 — the lane-local `layer_model.go` / `layer_upstream.go` files are DISSOLVED: the MODEL layer is the landed `pkg/modelcache` (one process-wide instance, `cmd/main.go:249`) and the UPSTREAM layer is the landed `pkg/skipcache` (`:242`); the lane-local `layer_group.go` is dissolved too — the GROUP layer IS the sweep + `pkg/grouplb` policy. **The MODEL layer's CHAIN reader remains ABSENT on this lane** (OQ-2) — ADR-12's record stands unchanged; only the provenance of the member reader changes.)* *(Characterization per ADR-14 item 1: `pkg/credentiallb`
is **NOT stdlib-only** — it imports `pkg/models` (→ `pkg/crypto`) and has **8
files**; the `pkg/grouplb` stdlib-only claim and `pkg/credclass`'s stdlib-only
design both stand.)* The invariant "a credential 429 can never change the
group pick" holds **structurally**: the pick function's input type carries only
GROUP-layer policy facts (A2), and the UPSTREAM/MODEL caches are reachable only
from the member loop (A5). **AC-R11** is satisfied by three separable
structures + three separable tests (A16).

**Where a layer is absent, it is SAID, not faked** (`decisions.md:789-790`):
the MODEL layer's **chain reader is ABSENT** (OQ-2) — recorded in the layer's
doc comment and in the AC-R11 test.

### A14 — A-6 / A-7: inactive and chain-only models (ADR-7; ADR-11 OQ-4)

- **Validation accepts an inactive / chain-only model as a chain target** — it
  is **never a defect** (`decisions.md:658-660`). For decision models the chain
  clause is vacuous (OQ-2), so on this lane the rule applies to the **group
  candidate surface**: an inactive model is **not offered as a group
  candidate** (the FE picker — Phase 2 — and the server-side candidate list).
  **PINNED (approver iteration-002 FIX 1):** the write-time ACCEPTANCE half is
  `TestValidateGroupMembers_InactiveChainOnlyTarget_Accepted` — it saves a
  model with an INACTIVE chain target and asserts ACCEPTANCE (a rejection
  fails the test). R4 is now pinned in BOTH directions: accepted as a chain
  target, never offered as a group candidate.
- **Nothing auto-enables a model** as a side effect of group membership
  (AC-R7): adding a member to a group must not flip `enabled`.
- The **group member** population and the **chain target** population are
  distinct in validation and in tests (OQ-4).

### A15 — AC-C1 is FORMALLY VOID (ADR-11)

AC-C1 (ADR-2's guard criterion — a test pinning "one decision model resolves
exactly one credential per request") is **FORMALLY VOID**: pinning it would
pin the very defect R3 removes. **AC-R4 replaces it** (`decisions.md:775-776`).
**This plan never carried AC-C1** (it carries the ruling's own contract
numbering 1–22); the strike applies to the ADR record and to the plan's
one-credential-per-request framing — the **E-6 scope reduction** (below) is
retracted by ADR-6.

### A16 — the re-derived test set (additions + supersessions)

**Superseded test names (recorded, not silently dropped):**

| Old name | Disposition | New name(s) |
|----------|-------------|-------------|
| `TestDecision_Group_MarkedMember_DemotedNotFiltered` | **SUPERSEDED** — the "demote-not-filter" framing is withdrawn (ADR-5: a mark suppresses an attempt; it never reorders) | `TestDecision_Group_MarkedHead_ZeroUpstreamCallSkip` + `TestDecision_Group_AllMarked_PolicyHeadHonestAttempt` + `TestDecision_Group_PolicyPick_IgnoresMarks` |
| `TestDecision_Group_NoCooldownWriteOnBenchedPick` | **SUPERSEDED** — "benched" is withdrawn vocabulary | `TestDecision_Group_NoCooldownWriteOnSuppressedPick` |
| `TestGroup_RewriteGroupEntries_DecisionGroup_PicksDecisionMember` | **SUPERSEDED** — the sweep is the TEXT lane's path; it must REFUSE a decision group | `TestGroup_RewriteGroupEntries_DecisionGroup_RefusedOnTextSweep` |
| `TestDecision_Group_DefaultPoolFirst_BackupPoolAfter` | **KEPT, re-derived** — the pool order is a policy fact (ADR-12 GROUP layer) | unchanged name, new semantics |
| `TestDecision_Group_WeightsAffectPick` | **KEPT, re-derived** — the weighted pick is now the lane's own policy head (fresh, no binding) | unchanged name, new semantics |

**Added tests (this amendment):**

| File | Test | Asserts |
|------|------|---------|
| `pkg/decision/handler_group_test.go` | `TestDecision_Group_PolicyPick_IgnoresMarks` | A mark on the head does **not** change which member is the head (AC-R1) — the head reported in telemetry is the policy head |
| `pkg/decision/handler_group_test.go` | `TestDecision_Group_MarkedHead_ZeroUpstreamCallSkip` | A marked head's attempt is suppressed: **zero upstream hits**, the event shows `member_suppressed`, and the next candidate is attempted (AC-R2) |
| `pkg/decision/handler_group_test.go` | `TestDecision_Group_AllMarked_PolicyHeadHonestAttempt` | With **every** candidate marked, the **policy head** is attempted exactly once (1 upstream hit) — the OQ-1 lift (ADR-11) |
| `pkg/decision/handler_group_test.go` | `TestDecision_Group_AttemptOrder_PolicyOrderMinusMarked` | The attempt sequence = the policy order minus the marked candidates — **no reordering** (AC-R3) |
| `pkg/decision/handler_group_test.go` | `TestDecision_Group_Exhaustion_ExcludesSuppressedAttempts` | The exhaustion event's `attempted` counts **genuine** attempts only; `skipped` is reported separately; the 502 fires only after every member × credential was genuinely attempted and failed (ADR-5 AC-R3, ADR-6 AC-R4) |
| `pkg/decision/handler_group_test.go` | `TestDecision_Group_MemberWithTwoCredentials_AttemptsBothBeforeAdvancing` | A member with 2 credential refs is attempted on **both** before the loop advances to the next member (AC-R4) — advancing early is a loop failure, not an optimisation |
| `pkg/decision/handler_group_test.go` | `TestDecision_Group_MemberFailedPredicate_OwnResilienceExhausted` | The member-failed predicate is true **only** when the member's own resilience (per-credential attempts; chain ABSENT) is exhausted (AC-R5) |
| `pkg/decision/handler_group_test.go` | `TestDecision_Group_SingleMemberGroup_FullyConsumable` | A single-member, single-credential group is fully valid and fully consumable: one attempt, then exhaustion — the structural loop contract, no hedge engine (OQ-3, AC-R8) |
| `pkg/decision/handler_group_test.go` | `TestDecision_Group_PickAttemptSkipEventOutcomes` | `member_picked` / `member_attempted` / `member_suppressed` are **distinct** outcomes (AC-R10) |
| `pkg/decision/handler_group_test.go` | `TestDecision_Group_LayerSeparation_CredentialMarkCannotChangePick` | A UPSTREAM-layer (credential) mark provably **cannot** alter the group pick; the pick reads no failure state; the MODEL layer's chain reader is recorded as **ABSENT** (AC-R11) |
| `pkg/decision/handler_group_test.go` | `TestDecision_Group_InactiveMember_NotOfferedAsCandidate` | An inactive model is **not** offered as a group candidate (A-6, AC-R6) |
| `pkg/decision/handler_group_test.go` | `TestDecision_Group_MemberNeverAutoEnabled` | Adding a member to a group never flips `enabled` (A-7, AC-R7) |
| `pkg/decision/handler_group_test.go` | `TestDecision_Group_ChainTargetNotConflatedWithMember` | A chain target and a group member are **two populations**: the chain clause is vacuous on this lane and is never consulted (OQ-4) |
| `pkg/decision/handler_group_test.go` | `TestDecision_DoctrineLine_PinnedVerbatimAtSeam` | The extended doctrine line is present **verbatim** in the suppression seam's source (**`pkg/decision/suppression.go`** — Amendment 7 / ADR-18 C2-1; skipcache's copy cited read-only) (AC-R9, ADR-10) |
| `pkg/decision/handler_group_test.go` | `TestDecision_Group_NoCooldownWriteOnSuppressedPick` | A **suppressed** pick writes **no** cooldown and **no** grouplb state (supersedes the "benched pick" test) |
| `pkg/store/database/store_group_test.go` | `TestGroup_RewriteGroupEntries_DecisionGroup_RefusedOnTextSweep` | The text-lane sweep **refuses** a decision-lane group (no decision member is ever offered to the chat path) — the manager-side defence in depth |

### A17 — coordination: collision hotspots and the text-lane fence (ADR-9)

**This release must NOT change text-lane behaviour** (ADR-9:697-698, AC-R9
(ADR-9)). Every shared-file change below is **lane-gated** and carries its
text-lane pin. Collision-hotspot files (expect merge conflicts with the
parallel commission; the ship step must **surface, never force** them — ADR-9's
table at `:701-715`): `pkg/store/database/store_group_rewrite.go`,
`pkg/store/database/store.go`, `pkg/grouplb/{engine,selector,binding}.go`,
`pkg/modelscache/models.go`, `pkg/skipcache/skipcache.go`,
`pkg/proxy/{handler,handler_groups,handler_anthropic,handler_helpers,race_coordinator}.go`,
`pkg/credentiallb/engine.go`, `pkg/models/{group,config}.go`,
`pkg/providers/interface.go`, `pkg/ui/server.go`, `cmd/main.go`.

**The text-lane pins that must stay green** (the proof of AC-R12 (ADR-9)):
`pkg/proxy/handler_groups_test.go:400`, `pkg/proxy/handler_groups_equivalence_test.go`,
`pkg/proxy/group_fallback_hidden_member_test.go`,
`pkg/proxy/group_fallback_part2_test.go`,
`pkg/proxy/handler_groups_shadow_member_test.go`, plus the whole existing
`pkg/proxy` / `pkg/store/database` / `pkg/models` / `pkg/ui` suites.

### A18 — acceptance-criteria deltas (ADR-10 AC-R1..AC-R11)

| ID | Criterion | Where it is satisfied |
|----|------------|------------------------|
| AC-R1 | Mark never changes the head (pick is policy-only) | A2, A6 + `TestDecision_Group_PolicyPick_IgnoresMarks` |
| AC-R2 | Marked head ⇒ zero-upstream-call **skip**, distinguishable from a failure | A3, A5 + `TestDecision_Group_MarkedHead_ZeroUpstreamCallSkip` |
| AC-R3 | Attempt sequence = policy order minus marked; exhaustion counts only genuine attempts | A3, A4 + `TestDecision_Group_AttemptOrder_PolicyOrderMinusMarked`, `TestDecision_Group_Exhaustion_ExcludesSuppressedAttempts` |
| AC-R4 | Member's own credentials attempted before advancing | A5 + `TestDecision_Group_MemberWithTwoCredentials_AttemptsBothBeforeAdvancing` |
| AC-R5 | "Member failed" predicate explicit: own resilience exhausted | A5 + `TestDecision_Group_MemberFailedPredicate_OwnResilienceExhausted` |
| AC-R6 | Inactive / chain-only accepted as chain target, not a group candidate, never auto-enabled | A14 + `TestDecision_Group_InactiveMember_NotOfferedAsCandidate` |
| AC-R7 | Decision-group members never silently auto-enabled | A14 + `TestDecision_Group_MemberNeverAutoEnabled` |
| AC-R8 | Single-member group's resilience consumption defined + documented (option a) | A11 + `TestDecision_Group_SingleMemberGroup_FullyConsumable` |
| AC-R9 (**ADR-10**) | Doctrine line present **verbatim** at the seam | A8 + `TestDecision_DoctrineLine_PinnedVerbatimAtSeam` |
| AC-R12 (**ADR-9**) | Text-lane behaviour byte-untouched; shared changes lane-gated | A17 + the text-lane pins |
| AC-R10 | Pick vs attempt vs skip are **distinct** in telemetry/events | A7 + `TestDecision_Group_PickAttemptSkipEventOutcomes` |
| **AC-R11** | The three layers are separable in code and in tests; a credential-layer state change provably cannot alter the group pick; each layer's absence-on-this-lane is recorded | **A13** + `TestDecision_Group_LayerSeparation_CredentialMarkCannotChangePick` |

### A19 — amendment risks, and what the amendment could not resolve

| # | Risk | Severity | Mitigation |
|---|------|----------|------------|
| AM-1 | The policy-only picker is a **third implementation** of the weighted selector if the `pkg/grouplb` export is refused | Medium | *(SUPERSEDED by Amendment 5 / ADR-16 — **AM-1 DISSOLVES with I-1**: the landed sweep is policy-headed + cache-blind, the lane consumes it directly, no picker is needed)* | Prefer the additive exported `pkg/grouplb` policy selector (text-lane-neutral: a new API, no behaviour change); the fallback is a small local picker in `pkg/decision` |
| AM-2 | The decision lane's UPSTREAM-layer cache is a **second** skip-cache instance (the text lane keeps the global one) | Medium | *(SUPERSEDED by Amendment 5 / ADR-16 — the "second instance" rationale is **VOID**: the landed sweep is cache-blind so no ordering leak survives, and upstream-layer health is per-credential, not per-lane — the fence is **LAYER separation**)* | Deliberate (ADR-9's byte-untouched fence): a shared instance would let a decision-lane mark influence text-lane ordering. The decision lane's instance is wired in `cmd/main.go` beside the text lane's and never shared |
| AM-3 | The per-credential rotation reaches into `pkg/credentiallb` (import-guard-legal per ADR-6, **not yet formally verified**) | Medium | The formal guard is the `go list -deps` walk (`pkg/decision/handler_test.go:90`); a read-only static check found `pkg/credentiallb` importing only `pkg/models` + its own subpackages — **verify before committing to the path** | *(RESOLVED by Amendment 3 / ADR-14 — verdict **LEGAL, unconditionally**; the guard tests PASS with literal `=== RUN` / `--- PASS` output, the closures are clean, a real direct-import scratch passed all guards (positive control) and a quoted-banned-path negative control FAILED the guard. The key fact: `pkg/decision` **already** carries `pkg/credentiallb` in its transitive closure (`decision → auth → store/database → credentiallb`, a non-test dep) — the "convince the guard" branch was a no-op. Characterization corrected per ADR-14 item 1: `pkg/credentiallb` is **NOT stdlib-only** — it imports `pkg/models` (→ `pkg/crypto`) and has **8 files**.)* |
| AM-4 | The member-failed predicate's treatment of a **non-limit-class** failure (5xx) inside a member is not spelled out by the ADRs | Low | The plan rotates to the member's next credential on any failed attempt (the class only decides the seeded cooldown) — matching AC-R4's "own resilience first"; flagged for the leader |
| AM-5 | The GROUP-layer memo's invalidation may not ride the modelscache write-through | Low | Fallback = rebuild the policy facts per request (N ≤ 16 rows) — still policy-only, still structurally isolated (A2) |

**Found impossible / not foldable in this amendment:**
- **The per-layer split cannot be made "shared" with the text lane in this
  release** — ADR-9 forbids text-lane behaviour changes, so the three layers are
  decision-lane constructs (the text lane keeps its single global skip cache).
  The two lanes' caches are deliberately separate instances.
- **The MODEL layer's chain reader cannot exist on this lane** — OQ-2 makes the
  chain clause vacuous; the layer is recorded **ABSENT** rather than faked
  (ADR-12's explicit instruction).

### A20 — adjacent rulings NOT folded (flagged)

- **ADR-1 (kind immutability, closes Q3) is NOT folded here** — it is outside
  this amendment's authorised scope (ADR-5..ADR-12). *(RESOLVED by Amendment 2 /
  Fold 1 — ADR-1 is folded as Stage 3b; the E-2 limitation is closed by the
  guard.)* **Flag:** ADR-1 rules that
  kind retyping is blocked **in the same release**, which contradicts this
  plan's `## Planner adjudications` E-2 record ("its cure — the D-2/Q3
  kind-immutability guard — sits OUTSIDE this commission's fence and is being
  surfaced to the user as a recommended same-release guard"). The
  **single-member re-lane hole** this plan documents (E-2) is closed by ADR-1's
  retyping-site fence; until ADR-1 is folded into a stage, the plan still
  carries the E-2 limitation. **Decide before implementation.**
- **ADR-2's evidence stands** (credential multiplicity is representable but
  inert on this lane today: `pkg/decision/handler.go:299` →
  `pkg/store/database/store.go:2742-2746` → `pkg/credentiallb/engine.go:344-347`);
  its *decision* is retracted by ADR-6.
- **ADR-3 / ADR-4** (the `data-center-scripts` pin-push question; the
  environment facts for deploy + E2E) are **out of scope** for the phase plan.

---

## Amendment 2 (2026-11-16) — ADR-1 fold + AM-4 failure-class policy

> **Authority:** `decisions.md` **ADR-1** — kind immutability for group members,
> closes Q3 (`decisions.md:426-461`) — and **ADR-13** — the second tranche of
> leader rulings: **AM-4** failure-class policy, picker asymmetries kept, the
> marker negative control, `ExcludeGroupMember` legality
> (`decisions.md:813-846`).
> **Precedence:** the same rule as Amendment 1 — where this amendment and an
> earlier section disagree, **this amendment wins**; the earlier text carries
> `*(superseded by Amendment 2 / ADR-…)*` markers in place and stays visible.
> **Nothing here is code.** No build, no test run, no commit.

### B1 — Fold 1: ADR-1 — the kind-immutability guard lands THIS release

**What changed.** The single-member re-lane hole that Amendment 1 recorded as an
"accepted, documented limitation" is now **CLOSED BY GUARD**: ADR-1 rules that a
model which is a member of a group has an **immutable kind**, and the guard
ships in this release (`decisions.md:428-430`). The E-2 records below carry the
supersession marker; the historical text stays visible.

**Stage placement (stated why):** the guard is a **WRITE-path rule with a parity
requirement** — the model-update path refuses, the error rides the existing
admin envelope, and ADR-1 criterion 4 demands helper ≡ PUT parity. That is
Stage 3's shape (admin API mirrors + parity), not Stage 4's runtime-lane shape.
It lands as a new sub-step **Stage 3b** (no earlier section is renumbered).

**Stage 3b — kind-immutability guard for group members (ADR-1)**

| Seam | File:line | What changes |
|------|-----------|--------------|
| The kind-change path (NOT the group-save path) | `pkg/store/database/store.go:1699` (`UpdateModel`) | The model-update write refuses a **kind change** when the row is a member of **any** group (membership derived from the collection). Group save/validation is NOT the fence (`decisions.md:431-432`). |
| Membership derivation | `pkg/models/config.go:533` (`GetModels` — already forwarded by the decorator, `pkg/modelscache/models.go`) | Scan the model set for a group row whose `Members` contain the id — **no new manager method** (D-8's one-method budget stays spent on `GroupLane`); ADR-1's decorator note prefers exactly this (`decisions.md:454-457`). **IF** a new manager method proves unavoidable, D-8 / contract 14 applies: decorator forward + decorator-through test. |
| Canonical helper | `pkg/store/database` (new small `validateKindChange`) | Returns the ADR-1 error: the message **names both ids** — `model <member-id>: kind cannot be changed while it is a member of group <group-id> (remove it from the group first)` (`decisions.md:442-443`). |
| API mirror | the PUT handler (`pkg/ui/server.go`, `handleModelDetail`) | The same rejection as the `/fe/api/*` bare `{"error":"<string>"}` envelope, status **4xx** (`decisions.md:441-444`). The POST path is not the fence (the row cannot be a group member before it exists). |
| Scope fence | — | **Scoped**: a model that is NOT a member of any group may still be retyped — no blanket kind-immutability (`decisions.md:449-450`). |

**Tasks (ADR-1's five criteria, one-to-one):**

| # | Task | Acceptance |
|---|------|------------|
| 3b.1 | Reject a kind change for a member of ANY group (canonical write path) | Retyping is refused; the **stored kind is unchanged**; group resolution is unchanged afterwards (criterion 1) |
| 3b.2 | Keep the guard scoped | A model NOT in any group still retypes successfully (criterion 2) |
| 3b.3 | The rejection names BOTH ids | `model <member>: kind cannot be changed while it is a member of group <group> (remove it from the group first)` (criterion 3) |
| 3b.4 | Parity | Asserted at the canonical write path AND the API PUT, independently (criterion 4) |
| 3b.5 | The hole-closure case | A single-member decision group whose member retype to chat is **refused** — the E-2 hole is closed (the guard is the fence ADR-1 describes at `:435-438`) |

**Retired records (marked in place, text kept):** the `## Escalations` E-2
paragraph, the `## Planner adjudications` E-2 subsection, and Amendment 1 §A20's
ADR-1 flag — all now point here.

### B2 — Fold 2: AM-4 RULED — the failure-class policy is mirrored EXACTLY (ADR-13)

**What changed.** Amendment 1 §A5's loop rotated to the member's next credential
**on any failed attempt**. That is **withdrawn**: the decision lane mirrors the
text-lane failure-class policy **EXACTLY** (ADR-13:815-823; the text-lane cites:
`pkg/proxy/cred_failover_class.go:162` `rotationEligible`, `:190`
`memberAdvanceBlocked`; marks fire on limit-class only —
`pkg/proxy/race_coordinator.go:2106-2107`).

| Failure class | Decision-lane behaviour | Upstream-layer write? |
|---------------|--------------------------|------------------------|
| **rotation-eligible** (limit-class + upstream 401/403) | **rotate within the member** — the member's next credential is attempted | YES — per classification (`ExcludeAndReselectForClass`) |
| **5xx** | **ADVANCE to the next member immediately** — no within-member rotation, **no mark, no cooldown write** (transient, provider-attributable) | NO |
| **memberAdvanceBlocked** (400/413/415/422 + client-cancel) | **fail-fast advance-blocking**, as the text lane | NO |
| **every credential of a member marked/cooling** (ADR-15 **D-C**) | **exactly ONE honest attempt on the member's policy-first credential**; a genuine failure per the AM-4 class policy fails the member and advances the loop — **the cache is never a failure oracle at ANY layer** | NO (the honest attempt is unsuppressed) |

**Exhaustiveness redefined (ADR-13:828-829):** "only fail after every member
genuinely failed" is **per the failure-class policy** — **a member that 5xx'd
HAS genuinely failed for this request**. Its remaining credentials are not
attempted (the advance is immediate); they are neither attempted nor failed —
they are **not reached**.

**The member-failed predicate (AC-R5) re-derived:**

> `memberResilienceExhausted(member)` is TRUE when the member's own
> rotation-eligible credential attempts are consumed **and** failed, **OR** the
> member's last failure was a **5xx**, **OR** an **advance-blocking** class
> (400/413/415/422 / client-cancel) fired. The chain clause stays ABSENT (OQ-2).

**Test supersessions / re-derivations (recorded, not silent):**

| Test | Disposition |
|------|-------------|
| `TestDecision_Group_MemberWithTwoCredentials_AttemptsBothBeforeAdvancing` | **SUPERSEDED** — the name no longer says what it tests (rotation is class-conditional). Replaced by `TestDecision_Group_MemberWithTwoCredentials_LimitClassRotatesBeforeAdvancing` |
| `TestDecision_Group_MemberFailedPredicate_OwnResilienceExhausted` | **KEPT, re-derived** — the predicate is class-conditional per the table above |
| `TestDecision_Group_Exhaustion_ExcludesSuppressedAttempts` | **KEPT, re-derived** — suppressed attempts are still never failures, AND a 5xx'd member counts as genuinely failed |

### B3 — Fold 3: the marker NEGATIVE CONTROL pinned (ADR-13:836-841)

Stage 6's marker recipe gains an explicit control, pinned alongside it:

- **positive:** `decision_all_members_failed` must be **PRESENT in the new
  build** (`strings <binary> | grep -c 'decision_all_members_failed'` ≥ 1);
- **negative:** the same string must be **ABSENT from the live v193 binary** —
  verifiable against the `~/llmproxy-verify/verify_image.sh` cache for the
  **v193 per-SHA tag** (per-SHA tags are immutable ⇒ the cache entry is safe;
  moving tags go stale);
- **no false-hit classes:** `grep -ci sticky` stays forbidden (Tailwind /
  static-UI false hits) — `grep -c` with the exact literal only.

### B4 — test-name delta over Amendment 1

**Added (Fold 1 — ADR-1):**

| File | Test | Asserts |
|------|------|---------|
| `pkg/store/database/store_group_test.go` | `TestUpdateModel_KindChange_GroupMember_Rejected` | Retyping a member of ANY group is refused; the stored kind is unchanged; group resolution unchanged (ADR-1 criterion 1) |
| `pkg/store/database/store_group_test.go` | `TestUpdateModel_KindChange_GroupMember_RejectionNamesGroupAndMember` | The rejection names BOTH the group id and the member id (criterion 3) |
| `pkg/store/database/store_group_test.go` | `TestUpdateModel_KindChange_NonMember_Succeeds` | A model NOT in any group still retypes (criterion 2 — no blanket immutability) |
| `pkg/ui/kind_rules_parity_test.go` | `TestKindRules_Parity_KindImmutability` | The guard is asserted at the canonical write path AND the API PUT, independently (criterion 4) |
| `pkg/store/database/store_group_test.go` | `TestGroup_SingleMemberDecisionGroup_MemberRetypeRefused` | The hole-closure case: a single-member decision group whose member retype to chat is REFUSED (the E-2 hole is closed by guard) |

**Added / renamed (Fold 2 — ADR-13):**

| File | Test | Asserts |
|------|------|---------|
| `pkg/decision/handler_group_test.go` | `TestDecision_Group_MemberWithTwoCredentials_LimitClassRotatesBeforeAdvancing` | A **limit-class** failure on credential #1 rotates to credential #2 BEFORE the loop advances (AC-R4, class-conditional) |
| `pkg/decision/handler_group_test.go` | `TestDecision_Group_5xx_AdvancesWithoutRotationOrMark` | A **5xx** advances to the next member immediately: no within-member rotation, **no upstream-layer mark, no cooldown write** |
| `pkg/decision/handler_group_test.go` | `TestDecision_Group_401_RotatesWithinMember` | Upstream **401/403** is rotation-eligible: the member's next credential is attempted, with the upstream-layer write per classification |
| `pkg/decision/handler_group_test.go` | `TestDecision_Group_400_413_415_422_FailFastAdvanceBlocked` | Advance-blocking classes **fail fast** as the text lane (`pkg/proxy/cred_failover_class.go:190`) |
| `pkg/decision/handler_group_test.go` | `TestDecision_Group_Exhaustion_5xxMemberCountsAsGenuinelyFailed` | A 5xx'd member counts as **genuinely failed** for this request — the exhaustion criterion is per the failure-class policy |

### B5 — flags

- **AM-4 vs AC-R4 wording:** AC-R4 ("member's own credentials attempted before
  advancing") is now **class-conditional** — under a 5xx the member advances
  before its remaining credentials are consumed. The criterion's *intent*
  (rotation-eligible failures consume the member's own resilience first) is
  preserved; the ADR-13 ruling governs. Recorded here so phase 3 does not read
  AC-R4 literally.
- **`ExcludeGroupMember` legality (ADR-13:843-846):** genuine-failure advance
  bookkeeping remains legal; using it to express a mark/suppression or as an
  ordering device stays barred. Amendment 1's design (the lane performs
  suppression itself; the sweep is not consumed) already complies.
- **Picker asymmetries (ADR-13:831-834):** KEPT AS PLANNED — `laneModels`
  untouched; decision candidates = kind AND enabled; the text picker keeps
  offering inactive chat models (the parallel commission's call).

---

## Amendment 3 (2026-11-16) — AM-3 closure

> **Authority:** `decisions.md` **ADR-14** — *AM-3 CLOSED: `pkg/credentiallb` is
> import-legal from `pkg/decision`* (developer verdict, `decisions.md:850-879`).
> Same precedence rule as Amendments 1–2: this amendment wins where they
> disagree; earlier text keeps its history and carries markers in place.
> **Nothing here is code.** No build, no test run, no commit.

### C1 — AM-3 is RESOLVED: **LEGAL, unconditionally** (ADR-14)

**Verdict.** `pkg/decision` **may import `pkg/credentiallb`** — no further
argument, no gate to convince. Completion-grade evidence (developer
`b14df3b4`, ADR-14:854-861):

- the three guard tests exist and **pass** at `pkg/decision/handler_test.go:40-63`
  / `:65-88` / `:90-109` (literal `=== RUN` / `--- PASS` output);
- the `go list -deps` closures are **clean** of the banned subtrees;
- a **real direct-import scratch in `/tmp` passed ALL guards** (positive control);
- a **negative control — a QUOTED banned path in a doc comment — FAILED the
  guard** (see C3: the guard is raw text).

**The key fact that makes the question a no-op (ADR-14:862-866).** `pkg/decision`
**already carries `pkg/credentiallb` in its transitive closure today**:
`decision → auth → store/database → credentiallb` — a **non-test** dependency
(`pkg/auth` imports `pkg/store/database`; the DB manager imports
`pkg/credentiallb`). The guard's closure walk has been passing with that edge
present all along, so a direct import adds **nothing** the guard has not already
accepted. **Amendment 1's "convince the guard" branch (AM-3) was a no-op.**

**Characterization correction (ADR-14 item 1) — applies to every place this plan
describes the package:** `pkg/credentiallb` is **NOT stdlib-only** — it imports
**`pkg/models`** (which reaches `pkg/crypto`), and the package has **8 files**
(`binding.go`, `doc.go`, `engine.go`, `events.go`, `failure_class.go`, `key.go`,
`selector.go`, `testhooks.go`), not the 3 the research memo enumerated. **No
phase document should repeat the stdlib-only or 3-file phrasing.** The
conclusion is unaffected — the closure is still clean of the banned subtrees.
**Unchanged (correct as written):** `pkg/grouplb`'s stdlib-only claim
(`pkg/grouplb/engine.go:3-9` — stdlib only ✓) and `pkg/credclass`'s
stdlib-only design (Stage 0 ✓).

### C2 — Stage 1 residual: `pkg/decision/doc.go:6-15` is STALE (ADR-14 item 4)

`pkg/decision/doc.go:6-15` states that `pkg/decision`'s import direction is
`pkg/decision → {pkg/models, pkg/auth, pkg/usage, pkg/events}` **ONLY**. The
**real** closure already includes `config`, `store/database`, `grouplb`,
`skipcache`, `toolrepair`, `credentiallb`, `crypto`. **Stage 1 must amend the
prose**, or the feature reads as a guard violation from day one (a reader —
human or agent — trusting the doc would conclude the existing closure is a
violation). **The raw-text trap of C3 applies to the amendment itself**: the
amended `doc.go` must not name the banned paths.

### C3 — IMPLEMENTER TRAP: the import guard is RAW TEXT over EVERY `.go` file, `_test.go` included (ADR-14 item 2)

The `pkg/decision` import guard scans **every `.go` file in the package
directory, including `_test.go`**, as **raw text** — a **comment that merely
QUOTES a banned path fails the guard** (ADR-14's negative control proved it in
both directions). Consequences for this plan's deliverables:

- **(a) the doctrine-line pin and any layering documentation written INSIDE
  `pkg/decision` must not name the banned paths** — refer to them as *"the
  banned subtrees"* (or equivalent); never write the literal import paths in a
  comment, a doc block, or a constant string inside the package. This applies
  to: the extended doctrine line's seam (Amendment 1 §A8 — the seam file's doc
  comment), the suppression seam (**`pkg/decision/suppression.go`** — Amendment 7 /
  ADR-18 C2-1; the dissolved `layer_*.go` files are retired), the
  GroupRewriter/GroupLane seam
  comments (Stage 2), and the group-lane comments (Stage 4).
- **(b) test comments are scanned too** — a `_test.go` in `pkg/decision` that
  quotes a banned path in a comment fails the guard exactly the same way.
- **Different mechanism from the cachelayer AST pin** — there is **no AST
  precision** here: no import-path awareness, no distinction between a comment
  and code, no "quoted path" escape. Raw text only.

### C4 — PLACEMENT CONSTRAINT: `pkg/grouplb` bans `pkg/credentiallb` (ADR-14 item 3)

`pkg/grouplb` **bans** `pkg/credentiallb` — the zero-credential guard
(`pkg/grouplb/engine_test.go:401`, `TestEngine_ZeroCredentialImportGuard`: the
banned set includes the `pkg/credentiallb` import path **and** any
`credentiallb.` symbol use at all). Therefore:

> The credential-aware logic — the **upstream-layer cache reader/writer** and the
> **per-credential rotation** — lives **IN `pkg/decision`** and **must NEVER be
> pushed down into `pkg/grouplb`**.

This is a **structural fence, not a style preference**: pushing it down breaks
grouplb's own guard (and with it the text lane). It also constrains the
policy-only picker of Amendment 1 §A2: the picker stays a **pure policy**
function (pools + weights + order — no credential awareness), and the
credential-aware steps stay on the decision-lane side of the fence.

### C5 — test-table effect: **NONE**

This tranche adds **no tests**. The **59-name added top-level test list**
published with Amendment 2 is **UNCHANGED** — phase 3 must consume that list,
not re-derive it from the file. *(count-prose correction — Amendment 4 / ADR-15
adds **three** tests, so the authoritative list is now **62 names**; this
sentence's "59" is the count as of this tranche. Review warning 7.)* The ADR-1 and ADR-13 additions and the
AM-4 rename reported with Amendment 2 all stand as published.

---

## Amendment 4 (2026-11-16) — review cycle 1, Wave A (HEAD-independent)

> **Authority:** `review-plan-1.md` (deep-review, verdict CRITICAL GAPS — the
> ruling layer verified coherent, the blockers concentrated in the gate/runbook
> layer and base currency) **+ `decisions.md` ADR-15** — review-cycle-1 leader
> decisions + ruled warnings (`decisions.md:881-925`).
> **Wave:** **A — HEAD-INDEPENDENT.** Everything folded here is decidable
> WITHOUT re-reading repo source at a new SHA. Every **BASE-relative** item is
> listed, not done — see `### Wave B (gated on base_reanchored=true)`.
> **Same precedence rule as Amendments 1–3.** No code, no build, no test run, no
> commit.

### D1 — CG-2 (BLOCKING): the skip-cache instance contradiction is closed (ADR-15)

**The contradiction (review-plan-1.md:25-29).** `phase1-plan.md:397` (Stage 4
task 4.8) and `phase1-plan.md:464` (risk **R5**) prescribed wiring the **SAME**
`upstreamSkipCache` instance into the manager and the decision handler, while
Amendment 1 §A5 (`phase1-plan.md:1306`, `:1562`, `:1571`) and ADR-9's
byte-untouched fence require a **separate, never-shared** instance. The
reviewer's point is exact: R5 was **unmarked**, prescribed concrete wiring, and
**none of the 59 tests pinned instance separation** — an implementer following
R5 would pass every named gate while opening the cross-lane mark-leak AM-2
exists to prevent.

**Resolution (two parts, both now):**
1. **R5 carries a supersession marker** (below) — the "SAME instance" wiring is
   withdrawn; the decision lane owns a **separate** instance.
2. **A new instance-separation pin test** — `TestDecision_Group_SkipCacheInstance_NotSharedWithTextLane`:
> ⚠️ *(SUPERSEDED by Amendment 5 / ADR-16 — the separate-instance resolution is
> itself superseded: the instance is **SHARED** (one process-wide upstream layer =
> one answer about one credential), the fence is **LAYER separation**, and the
> Wave-A pin test is WITHDRAWN in favour of
> `TestDecision_Group_CacheLayerSeparation_SharedUpstreamInstanceOneAnswer`.)*
   a mark written by the decision lane does **not** appear in the text lane's
   instance, and a mark written by the text lane's instance does **not** appear
   in the decision lane's. This is **distinct** from
   `TestDecision_Group_LayerSeparation_CredentialMarkCannotChangePick`, which
   pins **layer** isolation (a credential-layer state change cannot alter the
   group pick); the new test pins **instance** isolation.

**Why the demotion the risk described still works (ADR-9 fence intact):** the
decision lane reads and writes **its own** instance directly (the UPSTREAM-layer
cache, Amendment 1 §A5), so a decision-lane mark demotes the decision lane's own
members. The manager's / text lane's instance is never read by the decision lane
and never written by it.

### D2 — D-C (ADR-15): all-credentials-cooling gets an honest attempt (OQ-1 symmetry at member level)

**The rule.** If **every credential of a member** is marked/cooling, the member
gets **exactly ONE honest attempt on its policy-first credential**; if that
attempt genuinely fails **per the AM-4 class policy** (`pkg/proxy/cred_failover_class.go:162`
/ `:190`), the member **fails** and the loop advances. **The cache is never a
failure oracle at ANY layer** — the same invariant OQ-1 established at the
candidate level, now mirrored at the member level.

**Encoded in:** Amendment 1 §A5's re-derived loop (a D-C branch at the
suppression decision) and Amendment 2 §B2's AM-4 policy table (a D-C row).
**New test:** `TestDecision_Group_AllCredentialsCooling_PolicyFirstCredentialHonestAttempt`.

### D3 — W-fail-open (ADR-15): fail-open for unknown shapes, but never for a chat-inferred group with a decision member

**Keep** the fail-open fallback for **genuinely unknown** shapes (text-lane
parity for legacy / hand-edited rows — `groupLaneOf` returns "unknown" ⇒ the
group is served as today). **But** add the hard rule:

> A group inferred as **chat** that contains **≥1 decision member** must
> **NEVER silently serve**: **loud 400 `decision_misroute` + a telemetry
> event**.

D-2's write-time homogeneity makes this shape **unrepresentable at write
time**; this is the **runtime defence-in-depth net** — it catches the
hand-edited / legacy / retyped rows that bypass the validator, and it stops a
decision-kind member from being served on a chat route.

**Where it lands:** the shared lane seam (`groupLaneOf`,
`pkg/proxy/handler_helpers.go:867-886`) for the chat + Anthropic lanes, and the
decision lane's group branch for `/v1/decisions` (a mixed group is inferred
"unknown" ⇒ the lane is not "decision" ⇒ 400 `decision_misroute` — already the
fail-closed path; the new rule makes the **chat** side explicit and loud).
**New test:** `TestDecision_Group_ChatInferredWithDecisionMember_NeverSilentlyServes`.

### D4 — W-event-rename (ADR-15): `member_skipped` → **`member_suppressed`**

The decision lane's suppression outcome is renamed **`member_suppressed`**.
**Collision avoided:** v193 already uses a `member_suppressed` namespace (**68
hits** in the v193 tree) and v194 owns the `model_fallback_*` family — reusing
either name would make the decision lane's telemetry indistinguishable from
text-lane events. `member_suppressed` is lane-scoped and matches the
pick/attempt/**suppress** vocabulary of AC-R10.

Applied in place: Amendment 1 §A5's event-vocabulary row + its loop, §A6, §A7's
table, and the test rows that assert the outcome strings. **No test is
renamed** — `TestDecision_Group_PickAttemptSkipEventOutcomes` keeps its name
(the category is still pick/attempt/suppress); only the asserted literal
changes.

### D5 — Warning 9: Stage 1 task 1.6 must also fix the "one upstream attempt" claim

`pkg/decision/doc.go:17-19` states *"One upstream attempt per request (no retry,
no fallback, no race coordinator …)"*. Under this commission that becomes
**FALSE**: the decision lane now performs **per-member credential attempts**
(ADR-6 / A-1) and **group-level member fallback** (ADR-5 / ADR-6). Task 1.6's
acceptance therefore covers **both** the import-closure prose **and** this
claim — the doc is amended to describe the loop contract (per-member
credential attempts + group member fallback; still no speculative/hedge race
engine — OQ-3).

### D6 — Warning 7: count-prose hygiene (swept)

Swept this file for stale counts beside authoritative tables. **One instance
found and corrected:** Amendment 3 §C5's "the **59-name** added top-level test
list … is **UNCHANGED**" — now superseded by this tranche (three new tests ⇒
**62**). The review's other stale counts (**33 / 41 / 50** beside the 59, and
**"FE #23-27"** beside `decisions.md` 23-30) are **not in this file** — they
live in the sibling plans (`phase2-plan.md`, `phase3-plan.md`); they are listed
as an item for those documents in `### Wave B` (not amended here — this
tranche's file scope is `phase1-plan.md`).

### D7 — the three new tests (this tranche)

| File | Test | Asserts |
|------|------|---------|
| `pkg/decision/handler_group_test.go` | `TestDecision_Group_SkipCacheInstance_NotSharedWithTextLane` | **CG-2 / ADR-15:** the decision lane's skip-cache instance is NEVER the manager's / text lane's — a mark written by one lane does not appear in the other (instance isolation; distinct from layer isolation) |
| `pkg/decision/handler_group_test.go` | `TestDecision_Group_AllCredentialsCooling_PolicyFirstCredentialHonestAttempt` | **D-C / ADR-15:** with EVERY credential of a member marked/cooling, that member gets exactly ONE honest attempt on its policy-first credential; a genuine failure per the AM-4 class policy fails the member and advances the loop; the cache is never a failure oracle |
| `pkg/decision/handler_group_test.go` | `TestDecision_Group_ChatInferredWithDecisionMember_NeverSilentlyServes` | **W-fail-open / ADR-15:** a group inferred as chat that contains ≥1 decision member NEVER silently serves — loud 400 `decision_misroute` + a telemetry event |

**The added top-level test list is now 62 names** (59 + these 3). The
Amendment-2 list stands for its 59; the three above are the delta. Phase 3 must
consume the **62-name** list. *(Amendment 5 — the authoritative list is now **63**:
`TestDecision_Group_SkipCacheInstance_NotSharedWithTextLane` is WITHDRAWN,
`TestDecision_Group_CacheLayerSeparation_SharedUpstreamInstanceOneAnswer`
replaces it, and `TestGroup_SweepKindParameterization_MutationControl` is added.)*

### Wave B (gated on base_reanchored=true)

Every item below is **BASE-relative** (it needs a re-read of repo source at the
re-anchored tip) and is therefore **NOT done in Wave A**. Listed for giter's
re-anchor pass:

| # | Wave-B item | Why it is Wave B |
|---|-------------|-----------------|
| WB-1 | **CG-4 base move** — re-anchor all five plan-of-record docs' base assertions (`24c51ef == origin/latest`) to the new tip; re-derive every BASE-relative line pin; redefine "text-lane byte-untouched" as **"no v194 semantic regression"**; extend the ADR-9 collision-hotspot list with the v194 files (`pkg/modelcache/`, `race_executor.go`, `cache_layer_separation_test.go`); re-trigger the full gate on the merge **regardless of conflict**; re-derive the text-group regression pins against the NEW text behavior; state which lane-gating items become **reuse** vs remain **build** if the parallel commission already landed R1–R5 text-lane semantics | the new tip is not yet known; giter's v194 delta map governs |
| WB-2 | **CG-5 rollback ladder** — tier-1 = `:fd1649c` / `helm rollback 37`, tier-2 = v193; expected CI **≥ 195**; refresh rev / gen / dcs numbers | each number must be verified against giter's fresh state, never copied blindly |
| WB-3 | **Decorator-forward enumeration re-verify against the NEW base** — v194 touched `pkg/modelscache`; re-confirm that `GroupLane` is still the ONLY new `ModelsManager` method this release spends, and re-enumerate the decorator forwards at the new tip | the decorator surface moved with v194 |
| WB-4 | **D-2 negative-control test spelling** — spell out the negative-control tests that would have caught v193's class of defect (a kind-aware predicate with a fail-closed assertion), re-anchored to the new base | base-relative line pins + the parallel commission's text-lane semantics |
| WB-5 | **AM-3 P0.9 both-guard-layer coverage** — the both-guard-layer recipe for the lane-inference seam, re-anchored | base-relative |
| WB-6 | **D-A verification 1** — establish with file:line whether a member's `shadow_model_id` **participates in effective-kind resolution** for group members; if it cannot, record that explicitly and keep the shadow-model scope OUT | base-relative line pins |
| WB-7 | **D-A verification 2** — the `pkg/ui/server.go` rejection-status precedent (409 Conflict vs the existing 4xx family); the fallback rule (409) is already decided, the precedent check is not | base-relative line pins |
| WB-8 | **§A2 ordering-seam re-read** — the policy-only picker's seam (the additive exported `pkg/grouplb` policy selector vs an in-lane picker) re-read against the new base's `pkg/grouplb/selector.go` + v194's cache-layer separation; confirm the picker stays policy-only (no failure state) after the base move | the base moved the selector/engine surface |
| WB-9 | **Base-relative line-pin re-derivation of every repo citation in this plan** — `pkg/decision/handler.go`, `pkg/decision/doc.go`, `pkg/store/database/store_group_rewrite.go`, `pkg/store/database/store.go`, `pkg/modelscache/models.go`, `pkg/ui/server.go`, `pkg/models/group.go`, `pkg/grouplb/*`, `pkg/credentiallb/*`, `pkg/skipcache/*`, `pkg/providers/interface.go`, `pkg/config/config.go`, `cmd/main.go` — all line numbers in this document were read at `24c51ef` | every citation is a base-relative pin |
| WB-10 | **Count prose in the sibling plans** (warning 7) — the stale **33 / 41 / 50** counts and **"FE #23-27"** live in `phase2-plan.md` / `phase3-plan.md`; correct them there against the **62-name** backend list and `decisions.md`'s FE 23-30 range | the amendments in this tranche are scoped to `phase1-plan.md`; the sibling docs are amended in their own pass |

---

## Amendment 5 (2026-11-16) — base re-anchor `2b987bf` + reuse-vs-build re-derivation

> **Authority:** `decisions.md` **ADR-16** — v194 base re-anchor + reuse-vs-build
> re-derivation (`decisions.md:967-1002`) — **plus my own verification against
> the real tree at `2b987bf`** (every cite below is read in this worktree at that
> SHA; the leader's verified facts are confirmed, not copied).
> **Base:** `24c51ef` → **`2b987bf`** (`origin/latest`; master `fd1649c` = v194
> `fix/cache-layer-separation`). The branch is re-anchored; the planning docs are
> untracked and byte-identical.
> **Precedence:** same rule as Amendments 1–4 — this amendment wins; earlier text
> keeps its history and carries markers in place. **Markdown only** — no build, no
> test run, no commit.

### E1 — the reuse-vs-build table (deletions are GOOD — recorded as deleted)

| Item | Disposition | Cite (my read at `2b987bf`) |
|------|-------------|------------------------------|
| **I-1 — lane-gated policy-only ordering** (Amendment 1 §A2's separate ordering path / lane-gated parameter) | **DISSOLVED — CONSUME the landed sweep** | The sweep's own doc now encodes the ruling: *"The group pick is POLICY (pools, weights, sticky) and the cache must not decide it. A health mark (upstream or model layer) never changes WHICH member becomes the head; it only suppresses that member's ATTEMPT downstream"* — `pkg/store/database/store_group_rewrite.go:90-96` |
| **AM-1 — exported `pkg/grouplb` policy selector** | **DISSOLVED** (it existed only to serve I-1) | The sweep's Phase-1 head is the engine's own offer (sticky/weighted) — `store_group_rewrite.go:101-112`; the selector stays unexported (`pkg/grouplb/selector.go:26`) |
| **The sweep's ordering source** | **RE-DEFINED (meaning moved)** | `memberDemotionExpiry` now reads **only** `groupEngine.MemberCooldownUntil` (GROUP-owned rotation state) — `store_group_rewrite.go:284-292`; the sweep is **cache-blind**: the only `skipcache`/`modelcache` occurrence in the file is the pin's own comment at `:281` |
| **Plan-local `pkg/decision/layer_group.go`** (GROUP-layer cache) | **DISSOLVED — CONSUME** | The GROUP layer IS the sweep + `pkg/grouplb` policy (`pkg/skipcache/skipcache.go:4-10`: "GROUP — 'is this member servable IN THIS GROUP?' (grouplb policy)") |
| **Plan-local `pkg/decision/layer_model.go`** (MODEL-layer cache) | **DISSOLVED — CONSUME `pkg/modelcache`** | `pkg/modelcache/` is a landed real package (`modelcache.go`, `policy.go`, `reset.go`) — no internal imports ⇒ import-guard-safe from `pkg/decision`; one process-wide instance (`cmd/main.go:249`) |
| **Plan-local `pkg/decision/layer_upstream.go`** (UPSTREAM-layer cache) | **DISSOLVED — CONSUME `pkg/skipcache`** | `pkg/skipcache/skipcache.go:1-17` is now the **LAYER CONTRACT**; one process-wide instance (`cmd/main.go:242`) |
| **AM-2 — "a second skip-cache instance, never shared"** (Amendment 4 §D1, task 4.10, R5's Wave-A marker) | **SUPERSEDED — SHARED instance; the fence is LAYER separation** | See **E2** |
| **Stage 2's kind-parameterized `isServableGroupMemberBase`** | **BUILDS (survives)** | still chat-only at `store_group_rewrite.go:301-311` (the `switch row.Kind { case "", models.KindChat:` arm at `:305-310`) — this is the surviving manager-side widening |
| **Stage 4's member loop + per-credential rotation (AM-4 classes)** | **BUILDS (survives)** | `pkg/credentiallb` gains the exported cooldown reader `CooldownUntil(modelID, credID)` (`pkg/credentiallb/engine.go:468-477`) — the D-C precondition check (E4) |
| **`pkg/credclass`** (Stage 0) | **BUILDS** | `pkg/providers/interface.go:225`/`:348` unchanged at `2b987bf` |
| **Admin-API mirrors + ADR-1's kind guard** (Stages 1, 3b) | **BUILDS** | `pkg/ui/server.go` zero-diff at `2b987bf` (`:942`, `:959`, `:1444`, `:1461` all still name the same strings) |
| **`GroupLane` manager method + decorator forward** (Stage 2) | **BUILDS — still the ONLY new ModelsManager method** | See **E7** |
| **FE (Phase 2)** | out of this file | — |
| **`store_group_policy.go`** (the architect's named seam) | **DOES NOT EXIST at `2b987bf`** — the seam is `store_group_rewrite.go`; **RETRACTED by ADR-17** (architect §8.1 supersedes the architect's own §2c) — the file must appear **nowhere** in the final corpus except as a retracted proposal | see **E6** |

### E2 — AM-2 verdict: **CONFIRMED** — the instance is SHARED; the fence is LAYER separation

**Verdict.** The decision lane consumes the **process-wide** UPSTREAM instance
(`pkg/skipcache`) and the **process-wide** MODEL instance (`pkg/modelcache`).
**The separate-instance design is withdrawn.**

**Cites:**
1. **The ordering-leak rationale is void.** The sweep is **cache-blind** — the
   only `skipcache`/`modelcache` occurrence in `pkg/store/database/store_group_rewrite.go`
   is the pin's own comment at `:281`; `memberDemotionExpiry` reads only the
   GROUP-owned rotation cooldown (`:284-292`). A mark therefore **cannot** reach
   the group pick, so it cannot leak across lanes through ordering.
2. **The layer contract says the opposite of "separate".** `pkg/skipcache/skipcache.go:4-17`
   declares the UPSTREAM layer as *"A process-wide, in-memory registry"* read by
   *"credential selection + attempt suppression ONLY"*; `pkg/modelcache/modelcache.go:1-27`
   is the same shape for the MODEL layer; `cmd/main.go:242-249` constructs **one**
   instance of each and hands **both** to the proxy config (`:255-256`:
   `UpstreamSkipCache:` / `ModelSkipCache:`) — one instance, read by both
   protocol lanes.
3. **Upstream-layer health is per-credential, not per-lane.** A credential that
   is rate-limited is rate-limited for both lanes; two instances would give the
   two lanes **two answers about one credential** — the opposite of the
   "one answer" goal. ADR-16 records the same conclusion.
4. **The fence that actually matters is layer separation**, and it is enforced
   **structurally**: `TestCacheLayer_Structural_PackageLevelNoCacheImports`
   (`pkg/proxy/cache_layer_separation_test.go:752-786`) forbids any **non-test**
   file in `pkg/store/database`, `pkg/grouplb` or `pkg/modelscache` from
   importing `pkg/skipcache` or `pkg/modelcache` — so the group pick can never
   read a health layer, by construction (not by discipline).

**Consequences (the test-table one is the sharp end):**
- **Task 4.10** ("Separate skip-cache instance") is **SUPERSEDED** — the decision
  lane consumes the shared instances.
- **R5's Wave-A marker** ("**SEPARATE** and **never shared**") is **SUPERSEDED
  AGAIN** — it encodes the now-dead ordering-leak rationale. It stays visible.
- **The Wave-A instance-separation pin is WITHDRAWN**:
  `TestDecision_Group_SkipCacheInstance_NotSharedWithTextLane` — the invariant
  it pinned no longer exists (and must not: the instance is shared on purpose).
  It is replaced by a **layer-separation** pin,
  **`TestDecision_Group_CacheLayerSeparation_SharedUpstreamInstanceOneAnswer`**,
  which asserts the real invariant: the decision lane's UPSTREAM read is the
  **same process-wide instance** the text lane writes (one answer about one
  credential), and neither health layer can reach the **group pick** (layer
  fence). The complementary pick-side pin
  (`TestDecision_Group_LayerSeparation_CredentialMarkCannotChangePick`) stays
  as it is.

### E3 — the MODEL layer derivation: **CONSUME `pkg/modelcache`**; the chain reader stays ABSENT

**Decision.** The decision lane's **member-level** suppression reader consumes the
landed MODEL layer (`pkg/modelcache`, one process-wide instance,
`cmd/main.go:249`) — because on this lane **the member IS the model**: the
MODEL layer's question is *"is this MODEL limited/unavailable?"*
(`pkg/modelcache/modelcache.go:5-9`) and the decision lane's member-level
question is exactly that. The lane's MODEL-layer read is therefore the landed
package, **not** a plan-local cache file.

**What does NOT change (explicit reconciliation of ADR-12's record):**
- **The MODEL layer's CHAIN reader remains ABSENT on this lane** — OQ-2 makes the
  chain clause vacuous (`fallback_chain` forbidden for decision models,
  `pkg/models/config.go:301-304`, **enforced**: DB `store.go:2063`, UI
  `server.go:1180-1187`/`:1645-1652`, FE `DecisionModelsTab.tsx:131`; ADR-11's
  closure + **ADR-17**'s enforced-rule citation). ADR-12's "recorded ABSENT"
  note (`decisions.md:795-798`) **stands unchanged** — it was always about the
  **chain** reader, never about the member reader. What changes is only the
  **provenance**: the member reader now reads the landed `pkg/modelcache`
  instead of a plan-local file.
- The landed package's own doc says the MODEL layer is *"READ by the model/chain
  layer only (suppress that model's ATTEMPT at zero cost and proceed INTO its
  fallback chain)"* (`pkg/modelcache/modelcache.go:18-22`) — the decision lane
  uses the **attempt-suppression** half; the **chain** half it does not have.

### E4 — D-C implementation note: `CooldownUntil` is the precondition check

`pkg/credentiallb` now exports the read-only cooldown reader
**`CooldownUntil(modelID, credID) (time.Time, bool)`**
(`pkg/credentiallb/engine.go:468-477`) — *"the exported read-only cooldown
reader… Consumers outside the package consult this to keep selection-time
reordering consistent with GetOrSelect's own cooldown awareness"*.

**Encoded:** the D-C branch (ADR-15) uses it as the precondition — for each
credential of a member, the lane asks the credential engine whether that
`(memberID, credID)` pair is cooling; **if EVERY credential of the member is
cooling or marked**, the member gets **exactly ONE honest attempt on its
policy-first credential** (the attempt is unsuppressed); a genuine failure per
the AM-4 class policy then fails the member and advances the loop. The cache is
never a failure oracle at any layer.

### E5 — the AST pin constraint, verbatim, and its encoding at EVERY touch point

**What the pin forbids** (`pkg/proxy/cache_layer_separation_test.go:752-786`,
hardened in `15709a1`): *no **non-test** file in `pkg/store/database`,
`pkg/grouplb` or `pkg/modelscache` may import `pkg/skipcache` or
`pkg/modelcache`* — checked by **parsing the import block** (`go/parser`,
`ImportsOnly`), not by grepping raw text, so a quoted path in a comment does not
false-fail it (`TestCacheLayer_Structural_ImportParserIsCommentAware`, `:789`).
The same file's package-level doc (quoted at `store_group_rewrite.go:277-283`)
states the property: *"This package holds NO reference to either cache… The
isolation is a package-level property, not a rule to remember."*

**Encoded at every touch point of this plan:**

| Touch point | Constraint |
|-------------|------------|
| Stage 2 — `pkg/store/database/store_group_rewrite.go` (the kind-parameterized predicate + the lane inference) | The edit must **stay cache-blind**: no `skipcache`/`modelcache` import, no `m.skipCache`/`m.modelCache` field read. The manager answers only the GROUP-layer question from GROUP-owned rotation state. |
| Stage 2 — `pkg/modelscache/models.go` (the `GroupLane` forward) | The decorator must **not** import either cache package — the forward is a lane query, never a health read. |
| Stage 4 — `pkg/decision/*` (the lane's own readers) | `pkg/decision` is **NOT** in the pin's forbidden set ⇒ it MAY import `pkg/skipcache` + `pkg/modelcache` (both have **no internal imports**) — that is exactly how the lane reads the UPSTREAM/MODEL layers. |
| Stage 4 — `pkg/grouplb/*` | **No credential/health awareness may be pushed into `pkg/grouplb`** — ADR-14 item 3's fence, reinforced: `pkg/grouplb` bans `pkg/credentiallb` (`pkg/grouplb/engine_test.go:401`) AND (now) the health caches. |
| Any future edit to those three packages | Run `go test ./pkg/proxy/ -run TestCacheLayer_Structural` before the gate — the pin is the automated proof. |

### E6 — the architect seam is superseded: `store_group_policy.go` does not exist — and is now formally **RETRACTED** (ADR-17)

At `2b987bf` there is **no** `pkg/store/database/store_group_policy.go` (verified
by directory listing). The seam file is
**`pkg/store/database/store_group_rewrite.go`** — and it now *carries* the
policy doc itself (`:88-99`). Every citation in this plan that pointed at a
"policy seam file" resolves to `store_group_rewrite.go`; the advisory's named
file is **not** to be created (that would be a parallel policy path — exactly
what D-1 rejected).

### E7 — WB-3 decorator re-enumeration against `2b987bf`: **UNCHANGED — `GroupLane` is still the only new ModelsManager method**

Re-enumerated the decorator's exported surface at `2b987bf`
(`pkg/modelscache/models.go`): the interface members (`GetModel` `:453`,
`GetModelByName` `:506`, `GetModels` `:533`, `GetEnabledModels` `:542`,
`GetTruncateParams` `:549`, `GetFallbackChain` `:563`, `GetCredential` `:580`,
`GetCredentials` `:620`, `ResolveInternalConfigWithAffinity` `:680`,
`ResolveInternalConfig` `:750`, `AddModel` `:804`, `UpdateModel` `:838`,
`RemoveModel` `:880`, `AddCredential` `:911`, `UpdateCredential` `:939`,
`RemoveCredential` `:972`, `Save` `:983`, `Validate` `:986`) **plus** the four
group/cache seams (`RewriteGroupEntries` `:1043`, `ExcludeGroupMember` `:1056`,
`ClearGroupMemberCooldown` `:1079`, `GetModelNative` `:1139`). **`pkg/modelscache`
was zero-diff in v194**; the `SetSkipCache → ModelSkipCache` seam change touched
the **manager + proxy config**, not the decorator's forwarded set. ⇒ **The
one-new-method budget of D-8 is intact: `GroupLane` is the only new
`ModelsManager` method this release spends**, and it still needs its decorator
forward + decorator-through test.

### E8 — D-A's two verifications (both settled)

**(a) Does a member's `shadow_model_id` participate in effective-kind resolution
for group members? — NO.** Every non-test use of `ShadowModelID` at `2b987bf`:
the claims index (**group rows only**: `pkg/modelscache/models.go:248-249`
— `if m.IsGroup() && m.Enabled && m.ShadowModelID != ""`), the claim eviction
on writes (`:820`, `:854`, `:865`, `:891`), the `/v1/models` shadow-hide scan
(**group rows only**: `pkg/proxy/handler.go:382-383`), and the group-save
validation of the **group's own** shadow target (`pkg/ui/server.go:904`,
`:946-974`). **No member row's `shadow_model_id` is consulted anywhere**, and
the kind-resolution path reads only the row's own `kind`
(`isServableGroupMemberBase`, `store_group_rewrite.go:301-311`, and the
claim-blind `GetModelNative`). ⇒ **The shadow-model scope stays OUT of ADR-1's
guard** (ADR-1's rejection targets the member row's own `kind` field); this is
recorded explicitly, as ADR-16 requires.

**(b) The `pkg/ui/server.go` rejection-status precedent — it EXISTS, so the
409 fallback does not apply.** The group-save validation family writes
**`http.StatusBadRequest` (400)** with the bare `{"error":"<string>"}` envelope
(the `groupReject` helper, `pkg/ui/server.go:864-868` in the POST block and its
PUT twin) — e.g. `pkg/ui/server.go:942` (member kind), `:959` / `:963` (shadow
target). ⇒ **ADR-1's kind-change guard uses 400 Bad Request**, matching the
existing group-save validation family; **409 Conflict is the fallback only if no
precedent exists — one does, so 409 is not used.**

### E9 — WB-9: the re-derived pins (my own re-read at `2b987bf`)

**178 distinct repo/planning pins** were extracted from this document and
re-verified against the tree. **Result: 133 unchanged (the `pkg/decision`,
`pkg/models`, `pkg/modelscache`, `pkg/grouplb`, `pkg/providers`, `pkg/ui`,
`pkg/credentiallb` surfaces are zero-diff), 45 re-derived** (line shifts or
meaning moves, listed below). **No pin points at a file that no longer exists,
except the two v194 REMOVALS** (`SetSkipCache` and the manager's skip-cache
field), which are recorded as removed.

| # | Pin in this plan (was) | Re-derived at `2b987bf` | Kind of move |
|---|------------------------|---------------------------|--------------|
| 1 | `store_group_rewrite.go:49-86` (RewriteGroupEntries) | `:47-84` | shift |
| 2 | `store_group_rewrite.go:130-245` (the sweep) | `:131-249` (doc `:88-99` now carries the ruling) | shift + **meaning** (policy-headed, cache-blind) |
| 3 | `store_group_rewrite.go:147` (Phase-1 head) | doc `:101-112`, code `:148+` | **meaning** (the head is an engine offer = policy) |
| 4 | `store_group_rewrite.go:164` / `:187` / `:211` (predicate calls) | `:166` / `:189` / `:213` | shift |
| 5 | `store_group_rewrite.go:168-173` (the demotion path) | `:170-175` — now the **GROUP-rotation** demotion (no mark read) | shift + **meaning** |
| 6 | `store_group_rewrite.go:229-242` / `:229-243` (Phase 4) | `:230-248` — **the code now sorts by the rotation expiry; the comment at `:230-232` still says "soonest-expiring mark" (stale comment in v194 — flagged, not fixed here)** | shift + **meaning** |
| 7 | `store_group_rewrite.go:273-282` (memberDemotionExpiry) | `:284-292` — reads **only** `groupEngine.MemberCooldownUntil` | shift + **meaning** |
| 8 | `store_group_rewrite.go:291-301` (isServableGroupMemberBase) | `:294-311` (func at `:301`) — still chat-only | shift |
| 9 | `store_group_rewrite.go:340-353` (memberSoonestSkipExpiry) | **GONE** (the skip-cache read left the sweep) | removal |
| 10 | `store_group_rewrite.go:359-377` (defaultRefs/backupRefs) | `:334-352` | shift |
| 11 | `store_group_rewrite.go:406-431` (ExcludeGroupMember) | `:381-404` | shift |
| 12 | `store_group_rewrite.go:439-444` (ClearGroupMemberCooldown) | `:414-419` | shift |
| 13 | `store.go:726` / `:733` (the manager's skip-cache field / `SetSkipCache`) | **GONE repo-wide** (v194); the manager is cache-blind by design | removal |
| 14 | `store.go:1677-1686` (GroupMemberRefs) | `:1668-1677` | shift |
| 15 | `store.go:1699` (UpdateModel) | `:1690` | shift |
| 16 | `store.go:2085` (validateBeforeWrite) | `:2080` | shift |
| 17 | `store.go:2112-2117` / `:2113` (validateGroupWrite / `""` provider) | `:2103-2108` | shift |
| 18 | `store.go:2552-2567` (row-identity resolution) | `:2543-2558` (func at `:2543`) | shift |
| 19 | `store.go:2742-2746` (the engine's `GetOrSelect`) | `:2737` (cached path) / `:2607` (raw path) | shift |
| 20 | `skipcache.go:87` / `:102` / `:135` / `:154` / `:170` (Mark/Marked/Clear/AnyMarked/SoonestExpiry) | `:100` / `:115` / `:148` / `:174` / `:191` | shift |
| 21 | `skipcache.go:13-26` (the doctrine) | `:4-17` is now the **LAYER CONTRACT**; the doctrine literal is at `:29-31` and `:144` | shift + **meaning** |
| 22 | `proxy/handler.go:349-352` (the `/v1/models` exclusion) | `:391-397` (the check at `:393`) | shift |
| 23 | `proxy/handler.go:576-582` (the chat decision-misroute guard) | `:608-625` (the guard at `:612-623`) | shift |
| 24 | `proxy/handler.go:669` (the chat group rewrite) | `:712` (the Anthropic twin `:433`; the re-entry rebuild `:1160`) | shift |
| 25 | `proxy/handler.go:905-920` (the win-clear call site) | v194 **renamed the helper**: `clearWinnerUpstreamSkip` → **`clearWinnerLayerState`** (`pkg/proxy/handler_helpers.go:873`, doc `:840-872`) and it now clears **all three layers** (GROUP / MODEL / UPSTREAM) | shift + **meaning** (rename) |
| 26 | `proxy/handler_anthropic.go:272-279` (the Anthropic misroute guard) | `:266-283` | shift |
| 27 | `proxy/handler_helpers.go:867-886` / `:879-885` (the win-clear helper) | `:840-884` (new name) | shift + **meaning** |
| 28 | `proxy/race_coordinator.go:2088-2111` / `:2106-2107` (the mark site) | `:2112-2135` (the `Mark` calls at `:2131-2132`) | shift |
| 29 | `proxy/race_coordinator.go:1900` / `proxy/internal_handler.go:263` (the credential rotation calls) | `:1928` / `:268` | shift |
| 30 | `proxy/handler_groups.go:544-550` (directModelGetter) | `:555-590` | shift |
| 31 | `proxy/handler_groups.go:457-493` (the offer walk) | `:452-533` (the walk's core `:465-479` — **unchanged**) | shift (envelope) |
| 32 | `models/config.go:533` (GetModels) | the impl is `:1295`; the **decorator's** `GetModels` (`modelscache/models.go:533`) is unchanged | shift |
| 33 | `store_group_skipcache_test.go:62` / `:82` / `:101` (the ordering pins) | **v194 REWROTE the file**: `:59` `TestGroup_SkipCache_MarksDoNotChangeThePick`, `:85` `…ProviderWallDoesNotChangeThePick`, `:106` `…AllMarked_StillServes`, `:128` `…MarkNeverBenchesOrderingSeam`, `:147` `…ProviderMarkDoesNotWallSibling` — the **meaning is inverted** (the old tests asserted "the marked member is never picked"; the new ones assert "the mark does not change the pick") | **meaning** |
| 34 | `store_group_skipcache_test.go:165` (`…NilCacheByteIdentical`) | **GONE** (the file was rewritten) | removal |
| 35 | `credentiallb/engine.go:637` (ExcludeAndReselectForClass) | `:637-641` (the func at `:640`) | shift |
| 36 | `credentiallb/engine.go:344-347` (the binding write) | `:344-347` — unchanged | ✓ |
| 37 | `cmd/main.go:320-328` / `:327` (the decision handler wiring) | `:320-331` / the credential resolver at `:326-330` | shift |
| 38 | `cmd/main.go:240-241` (the skip-cache instance) | `:242` (`upstreamSkipCache := skipcache.New()`) + `:249` (`modelSkipCache := modelcache.New()`) + `:255-256` (both wired) | shift + **meaning** (the model layer is new) |
| 39 | `cmd/main.go:137` / `:144` | `:137` (credLB) / `:144` (stickyEngine) — unchanged | ✓ |
| 40 | `cmd/main.go:182-188` (the modelscache wrap) | unchanged | ✓ |
| 41 | `proxy/handler.go:90` / `:166` (the cache fields) | `:91` (`UpstreamSkipCache`) / `:99` (`ModelSkipCache`) — **the model layer is new** | shift + **meaning** |

**Base assertions + the fence redefinition (WB-1's core).** Every
`24c51ef == origin/latest` claim in this document becomes **`2b987bf`**; the
architectural fence changes from *"text-lane byte-untouched"* to **"no v194
semantic regression"** — v194 **deliberately changed** the text lane (the sweep's
ordering + the proxy's per-layer suppression), so the correct invariant is that
**this release must not move the text lane relative to `2b987bf`**, not relative
to v193. The text-group pins are re-derived accordingly (E9.1).

**E9.1 — the text-lane regression pins, re-derived against v194** (the Stage-5
pin list): `pkg/store/database/store_group_skipcache_test.go` (v194's own R1
pins — `…MarksDoNotChangeThePick` `:59`, `…ProviderWallDoesNotChangeThePick`
`:85`, `…AllMarked_StillServes` `:106`, `…MarkNeverBenchesOrderingSeam` `:128`,
`…ProviderMarkDoesNotWallSibling` `:147`), `pkg/store/database/store_group_test.go`
(`:146`, `:592`, `:914` — the C1 backup-less arm), `pkg/proxy/handler_groups_test.go:400`
(byte identity), `pkg/proxy/cache_layer_separation_test.go` (**the whole v194
suite — the layer-contract pins this release must not regress**),
`pkg/proxy/group_fallback_hidden_member_test.go`,
`pkg/proxy/group_fallback_part2_test.go`, `pkg/proxy/handler_groups_shadow_member_test.go`,
`pkg/proxy/handler_groups_equivalence_test.go`.

### E10 — WB-4: the D-2 negative-control checks (named)

The v193 defect class was a **kind-aware predicate that silently served a
decision member on the chat lane** — a fail-open, not a fail-closed. The named
controls this release ships (all in the added-test list):

| Named check | What it would have caught |
|--------------|---------------------------|
| `TestGroup_RewriteGroupEntries_MixedKindGroup_FailsClosed` | a mixed-kind group serving *some* members instead of failing closed |
| `TestGroup_RewriteGroupEntries_DecisionGroup_RefusedOnTextSweep` | a decision member offered to the chat path by the text sweep |
| `TestDecision_Group_ChatInferredWithDecisionMember_NeverSilentlyServes` | a chat-inferred group silently serving a decision member (the runtime net) |
| `TestGroup_SweepKindParameterization_MutationControl` **(NEW)** | a kind-agnostic predicate: it asserts the predicate consults the expected kind — a decision member is servable when the expected kind is decision and **not** servable when it is chat (the mutation check D-2 asked for) |

### E11 — the test-name delta and the FINAL list

**Withdrawn (Wave A):** `TestDecision_Group_SkipCacheInstance_NotSharedWithTextLane`
— the invariant it pinned is not merely re-based, it is **wrong** under the
AM-2 re-derivation (the instance is shared on purpose). **Replaced by**
`TestDecision_Group_CacheLayerSeparation_SharedUpstreamInstanceOneAnswer`.
**Added (this tranche):** `TestGroup_SweepKindParameterization_MutationControl`.

⇒ The authoritative added top-level test list is now **63 names** (62 → −1
withdrawn, +1 replacement, +1 new). Phase 3 must consume the 63-name list.
*(count superseded — Amendment 6 raised it to **69**; Amendment 7 raises it to
**73**.)*

---

## Amendment 6 (2026-11-16) — architect final: four RED gaps

> **Authority:** `decisions.md` **ADR-17** (the encoding; `decisions.md` ADR-17
> section) + `architecture-recommendation.md` **§7a** (`:964+`), **§2a W1's G1**
> (`:264-275`), the **pick-placement block** (`:546-560`), **§8.1/§8.2**
> (reuse-vs-build — already aligned with Amendment 5 §E1), **W9's gap list**
> (`:436+`). Every file:line below was **re-verified in this worktree at
> `2b987bf`** before folding. Markdown only — no code, no build, no commit.

### F1 — RED-1: the fallback-chain bypass → the REWRITE-BOUNDARY LANE CHECK (+ the write-time rule)

**The hole (ADR-17 item 1; architect §2a W1's G1, `:264-275` — cross-confirmed by W2).**
A **chat main's `FallbackChain` may name a group id**: the model list is built as
`[primary] + FallbackChain` on both lanes (`pkg/proxy/handler_functions.go:200-202`,
`pkg/proxy/handler_anthropic.go:290-292`); the rewrite loop iterates **ALL**
entries (`pkg/store/database/store_group_rewrite.go:52`); the write side
validates **existence only** (`pkg/ui/server.go:1996-2010` — by NAME, not id;
`pkg/models/config.go:1021-1025` — warn-but-allow); and the misroute guards
check **only `rc.resolvedModel`** (the chat guard at
`pkg/proxy/handler.go:608-625` @2b987bf, the Anthropic guard at
`pkg/proxy/handler_anthropic.go:266-283`), so a **fallback-position** group
entry is never lane-checked. With a sloppy predicate the decision member
**silently enters the chat race list** (no per-attempt kind guard exists in
`pkg/proxy/race_executor.go:114-140`); with the correct parameterized predicate
the whole chat request **502s `group_unservable`**
(`pkg/proxy/handler.go:712-724` @2b987bf) even though the primary is healthy —
and re-entry replays `modelListOriginal` (`pkg/proxy/handler.go:1160`,
`pkg/proxy/handler_groups.go:480`), repeating per re-entry.

**The fix (Stage 2c — the rewrite-boundary lane check), encoded with the
architect's semantics:**

| Aspect | Encoded behaviour |
|--------|-------------------|
| The check | Inside `RewriteGroupEntries`'s entry loop (`pkg/store/database/store_group_rewrite.go:52`): for a **group** entry whose inferred lane is **decision**, the entry is **refused on this lane**. |
| The refusal | **No member is produced; NO rotation row is written** (`ExcludeAndReselect` must not fire for a lane-mismatched entry — the GROUP rotation table must stay free of decision-group rows written by chat traffic, architect `:546-560` / `:890`). |
| Direct-named entry (the group is the requested model) | `FailedGroupID` at the boundary — belt and braces; the pre-rewrite misroute guard (`pkg/proxy/handler.go:608-625`) normally catches this first with 400 `decision_misroute`. |
| **Fallback-position entry** | The entry is **DROPPED from the execution list** with a loud log + a `decision_misroute` telemetry event, and the walk **continues** — so a healthy chat primary **does NOT 502**; the decision group is simply not a candidate on this lane. (This is the outcome the architect's own G1 text demands — *"even though the primary is healthy"* is named as the defect; the whole-request 502 of a naive `FailedGroupID` would preserve it. The per-entry drop is the encoded resolution of that tension; flagged for the reviewer.) |
| The write-time rule (Stage 1 rule **F-2**) | A **chat** model's `fallback_chain` must not name a **decision-lane group** — validated at write time (`pkg/ui/server.go:1996-2010` and `pkg/models/config.go:1021-1025` currently check existence only). Rejected with the group-save 400 family. |

**Stage 2c tasks:**

| # | Task | Acceptance |
|---|------|------------|
| 2c.1 | The boundary lane check in the rewrite loop | A decision-lane group entry on a chat lane produces **no member** and **no rotation row**; the entry is logged + evented |
| 2c.2 | Direct-named ⇒ `FailedGroupID`; fallback-position ⇒ drop-and-continue | A healthy chat primary with a decision-group fallback **serves 200 from the primary** (no 502, no decision-member attempt); a direct chat post of the decision group id still fails closed |
| 2c.3 | The write-time rule (F-2) | A chat model whose `fallback_chain` names a decision-lane group is **rejected at write time** (both the canonical path and the UI mirrors) |

**Named test:** `TestGroup_RewriteGroupEntries_DecisionGroupInChatChain_BoundaryRefusal`
— a chat model whose `fallback_chain` names a decision group **never serves that
group on the chat lane** (no decision member in the execution list, no decision
rotation row), **and the healthy-primary case does NOT 502** (200 from the chat
main). Plus `TestUpdateModel_FallbackChain_DecisionGroupTarget_Rejected` (the
write-time rule).

### F2 — RED-2: the AST-pin pick placement is LOAD-BEARING (obligations + the mirror pin + the retraction)

**The architect (`:546-560`):** the upstream AST pin
(`pkg/proxy/cache_layer_separation_test.go:752-786` @`2b987bf`, helper
`parsedImportPaths` `:725-738`) bans `pkg/skipcache` + `pkg/modelcache` imports
in **`pkg/store/database`** (also `pkg/grouplb`, `pkg/modelscache`); **`pkg/decision`
is NOT in scope**. So a pick placed **in `pkg/store/database`** is **provably
policy-only for free** — it *cannot* compile a cross-layer read. Placed **only**
in `pkg/decision`, isolation reverts to a **mirror file-level pin**.

**The obligations, encoded:**

| # | Obligation | Where |
|---|------------|-------|
| O-1 | **The pick STAYS in `pkg/store/database`** — it inherits the package pin and cannot compile a cross-layer read | Amendment 5 §E1 dissolved the lane-local layer files; the pick is the sweep (`RewriteGroupEntries` / `pickServableGroupMember`, `pkg/store/database/store_group_rewrite.go:47` / `:131`) — **reaffirmed**, and the file stays on the AST pin's forbidden-import list |
| O-2 | **The attempt stage lives in `pkg/decision`** — it MAY import both caches (`pkg/skipcache`, `pkg/modelcache`; neither has internal imports) | Amendment 1 §A5's loop — reaffirmed; `pkg/decision` is not in the AST pin's forbidden set |
| O-3 | **A file-level MIRROR PIN over the decision pick/walk files** — since `pkg/decision` may import the caches, the pick/walk files must be pinned separately: they carry **no cache import at all** (the pick is policy; the caches belong to the attempt/suppression stage only) | NEW named test **`TestPackage_ImportGuard_GroupPolicyFiles_NoCacheImports`** — a file-level mirror of the upstream AST pin, scanning the decision pick/walk files (`layer_group.go`-successor policy files + the walk) for `pkg/skipcache` / `pkg/modelcache` imports |
| O-4 | **`store_group_policy.go` is RETRACTED** (ADR-17; architect §8.1 supersedes the architect's own §2c) | The file must appear **nowhere** in the final corpus except as a retracted proposal — Amendment 5 §E6 already records its absence; this amendment records the **retraction** itself |

**Named test:** `TestPackage_ImportGuard_GroupPolicyFiles_NoCacheImports` (new).

### F3 — RED-3: the fail-open lane gate must be wired on **ALL FOUR** consumers (compile-time `var _` + effect tests)

**The hole (ADR-17 item 3; architect risk register R3, `:779`):** if the
`GroupLane` forward is missing, the decorator **wraps-not-embeds**
(`pkg/modelscache/models.go:104-105`) so a structural assertion at the call site
fails silently, and the plan's `groupLaneOf` fallback says *"not excluded"* —
⇒ a decision group is **served on `/v1/chat/completions` + `/v1/messages` and
listed in `/v1/models`** (`:331-332`). **Three of the four consumers are
covered by effect tests; the gate must be provably wired on all four.**

**The fix — compile-time `var _` assertions at every consumer + effect tests,
with the wiring pinned to the decorator (never the raw `modelsMgr`):**

| Consumer | Compile-time assertion site | Effect test |
|----------|-----------------------------|-------------|
| 1. Decision handler entry | `pkg/decision/handler.go` — `var _ groupLaneSeam = (*modelscache.CachedModelsConfig)(nil)` (the seam type declared beside the handler's optional-interface shape) | `TestDecision_Group_ThroughCachedConfig` (**already named**) |
| 2. Chat misroute guard | `pkg/proxy/handler.go` — `var _ groupLaneReader = (*modelscache.CachedModelsConfig)(nil)` | **NEW** `TestHandleChatCompletions_RejectsDecisionGroupMisroute_ThroughCachedConfig` |
| 3. Anthropic misroute guard | `pkg/proxy/handler_anthropic.go` — the same `var _` form | **NEW** `TestAnthropicBoundary_DecisionGroupMisroute_ThroughCachedConfig` |
| 4. `/v1/models` filter | `pkg/proxy/handler.go` (the `HandleModels` consumer) — the same `var _` form | **NEW** `TestHandleModels_FilterDecisionGroup_ThroughCachedConfig` |

The `var _` assertions live in the **non-test** consumer files (a compile-time
guarantee, not a test-time one) and bind against
`*modelscache.CachedModelsConfig` — so a **removed decorator forward breaks the
build**, not the production lane. The effect tests bind the **cached** config
(the production binding, `cmd/main.go`) and prove the gate's observable effect
through it. **`TestDecision_Group_ThroughCachedConfig` already covers consumer 1;
the three new tests close consumers 2–4.**

### F4 — RED-4: contract-15 per the architect's W9 (folded exactly as written)

The architect's W9 finding (`:478-482`) and its G7/G10 companions:

| W9 item | The fix, folded |
|---------|------------------|
| **"Contract 15 is unmet today": a decision-lane group has `Kind=="group"` ⇒ `IsDecision()` false ⇒ NOT excluded from `/v1/models`** (`handler.go:349-352` @24c51ef — at `2b987bf` the check is `:391-397`, the skip at `:393`) | The exclusion becomes **lane-aware**: a group whose inferred lane is decision is excluded — **already encoded** in Stage 4.3 (the `groupLaneOf` consumer row) and pinned by `TestHandleModels_FilterDecisionGroup`. **Reconciled:** the Stage 4.3 row additionally requires the **all-models fold** (below) and the effect test becomes the **ThroughCachedConfig** variant (F3). |
| **G7 — the lane derivation is unspecified; snapshot-fold over `GetEnabledModels()` diverges from manager inference (which counts disabled members); recommend all-models fold + a pin** | The `/v1/models` lane derivation folds over **ALL models** (`GetModels()`), matching the manager's inference (a disabled member still counts toward unanimity) — **encoded in the Stage 4.3 row** and asserted by the ThroughCachedConfig test (a group with a **disabled** decision member is still excluded). |
| **G10 — the "groups STAY listed" comment (`handler.go:329-332` @24c51ef) becomes false** | The comment is updated **in the same commit** as the lane-aware exclusion (it is inside the touched region, `pkg/proxy/handler.go:391-397`). |
| **FE contract stated explicitly: `?kind=group` returns mixed lanes** (`pkg/ui/server.go:785-787`) — the FE tabs must split by lane | **Phase 2's obligation** — recorded here so phase 2 cannot omit it; the server-side `?kind=group` arm stays unfiltered in phase 1 (no behaviour change to `/fe/api/*`). |

### F5 — upgrade 1: OQ-2 upgraded from assertion to **enforced-rule citation**

OQ-2's records (Amendment 1 §A10, §E3, §A1's OQ-2 row) are upgraded: the chain
clause is vacuous **by enforced rule**, not by assumption —

- **canonical:** `pkg/models/config.go:301-304` — `if len(m.FallbackChain) != 0 { return fmt.Errorf("model %s: kind %q does not support fallback_chain", m.ID, KindDecision) }`, reached via `ValidateKindRules` `case KindDecision` (`config.go:226-227`); **byte-identical at both bases** (`git diff 24c51ef 2b987bf -- pkg/models/config.go` = empty);
- **DB:** the write path's validation entry (`pkg/store/database/store.go:2063` region — `validateBeforeWrite` → `validateModelAgainstCredentials`);
- **UI mirrors:** `pkg/ui/server.go:1180-1187` (POST) / `:1645-1652` (PUT) — both verified in-worktree;
- **FE:** `pkg/ui/frontend/src/components/config/DecisionModelsTab.tsx:131` stamps `fallback_chain: []` — the FE never exposes a chain field for decisions.

**⇒ "no equivalent concept on this lane; deliberately not built"** now cites the
**enforced** rule (ADR-17; architect §7a.1). Marked in place at §A10 and §E3.

### F6 — upgrade 2: AC-R4 upgraded to **necessary-for-correctness**

Credential multiplicity is **REPRESENTABLE today** — cap 16
(`pkg/models/config.go:357`, `MaxCredentialRefs :84`), decision validation `>=1`
with **no cap of 1** (`:284-286`), the admin API accepts
(`pkg/ui/server.go:1161` / `:1628` — both verified), the FE ships
`MultiCredentialEditor` (`DecisionModelForm.tsx:468`), and `len>=2` **already
routes through `credentiallb.GetOrSelect`** (`pkg/store/database/store.go:2605-2610`
@`2b987bf`; the pool is keyed per model **ROW ID** — `RebindFromStore(mc.ID, …)`
`store.go:789`). **The gap is consumption, not representation:** an operator
with 2 credentials on a decision model gets exactly **one** attempted today
(`pkg/decision/handler.go:299` + `:430-433`) — **the per-credential member loop
IS the fix**, and the substrate (pool, weighted pick,
`ExcludeAndReselectForClass`) already exists. Recorded at the AC-R4
acceptance-checklist row and Amendment 1 §A5's per-credential row.

### F7 — upgrade 3: the anchor sweep (`:637` → `:657` @`2b987bf`)

`ExcludeAndReselectForClass` sits at **`pkg/credentiallb/engine.go:657`**
@`2b987bf` (was `:637` @`24c51ef`; the legacy `ExcludeAndReselect` delegate is
at `:644-652`). **Corrected in place at every operative row:** the
`## Escalations` E-6 block, Amendment 1 §A5's per-credential row, and the
Planner-adjudications E-6 (b) citation (whose `:615-620` legacy-delegate cite
becomes `:644-652`). Amendment 5 §E9 row 35 already recorded the shift; the
operative rows now agree with it.

### F8 — OQ-1 proof venue: **CONFIRMED, no new test**

ADR-17's mandatory deterministic unit test for OQ-1 is **satisfied** by
`TestDecision_Group_AllMarked_PolicyHeadHonestAttempt` — already in the
63-name table (Amendment 1 §A9's verification item 1). **No new test is added**
for OQ-1; E2E observation of the mixed case remains local-harness-only
(ADR-16's ruling).

### F9 — the test-name delta and the FINAL list

**Added (this tranche — 6):**

| File | Test | Asserts |
|------|------|---------|
| `pkg/store/database/store_group_test.go` | `TestGroup_RewriteGroupEntries_DecisionGroupInChatChain_BoundaryRefusal` | **RED-1:** a chat model whose `fallback_chain` names a decision group **never serves that group on the chat lane** (no decision member in the execution list, **no decision-group rotation row**) **and the healthy-primary case does NOT 502** (200 from the chat main); the direct-named case still fails closed |
| `pkg/store/database/store_group_test.go` | `TestUpdateModel_FallbackChain_DecisionGroupTarget_Rejected` | **RED-1 (write-time rule):** a chat model whose `fallback_chain` names a decision-lane group is rejected at write time (the existence-only validation is closed) |
| `pkg/decision/handler_test.go` | `TestPackage_ImportGuard_GroupPolicyFiles_NoCacheImports` | **RED-2:** the file-level mirror pin — the decision **pick/walk** files carry no `pkg/skipcache` / `pkg/modelcache` import (the pick stays policy-only even though the package may import the caches) |
| `pkg/proxy/handler_test.go` | `TestHandleChatCompletions_RejectsDecisionGroupMisroute_ThroughCachedConfig` | **RED-3 (consumer 2):** the chat guard's effect through the CACHED decorator (the production binding) |
| `pkg/proxy/handler_anthropic_misroute_test.go` | `TestAnthropicBoundary_DecisionGroupMisroute_ThroughCachedConfig` | **RED-3 (consumer 3):** the Anthropic guard's effect through the CACHED decorator |
| `pkg/proxy/handle_models_imggen_test.go` | `TestHandleModels_FilterDecisionGroup_ThroughCachedConfig` | **RED-3/RED-4 (consumer 4):** the `/v1/models` exclusion through the CACHED decorator, **with the all-models fold** (a group with a DISABLED decision member is still excluded) |

⇒ **The authoritative added top-level test list is now 69 names** (63 + 6).

---

## Amendment 7 (2026-11-16) — review cycle 2

> **Authority:** `review-plan-2.md` (cycle-2 delta re-review; governs the delta)
> **+ `decisions.md` **ADR-18** (the cycle-2 leader rulings). Same precedence rule
> as Amendments 1–6. **Markdown only** — no code, no build, no test run, no
> commit. **The corpus remains `2b987bf`-pinned for cycle 3 — no re-anchor
> mid-review** (ADR-18 STAGE 0).

### G1 — C2-1 (BLOCKING): the doctrine-line pin target is **named** — `pkg/decision/suppression.go`

**Leader pick (ADR-18 C2-1).** The verbatim doctrine line pins at the **NEW
decision-lane suppression seam file that Stage 4 creates** — the file where the
marked-member attempt-suppression branch lives:

> **`pkg/decision/suppression.go`**

Derived from the Stage-4 layout: it owns `decideAttempt(member, credential)` (the
marked-member attempt-suppression branch), the **D-C branch** (the
all-credentials-cooling honest attempt, with the
`credLB.CooldownUntil(memberID, credID)` precondition), the UPSTREAM-layer mark
writers (`pkg/skipcache`) and the MODEL-layer mark writers (`pkg/modelcache`).
Its doc comment carries the verbatim extended doctrine line. **`pkg/decision` is
NOT in the AST pin's forbidden set** (ADR-16 §E5), so this file MAY import
`pkg/skipcache` + `pkg/modelcache` — it is the attempt/suppression stage, not the
pick.

**`pkg/skipcache/skipcache.go:29-31` is v194's canonical home — CITED READ-ONLY,
NEVER EDITED** (the architect's §0 constraint). The four pin sites below now name
`pkg/decision/suppression.go`; `TestDecision_DoctrineLine_PinnedVerbatimAtSeam`
targets that file **and MAY additionally assert skipcache's copy as read-only
drift detection** (an optional secondary assertion — if v194's copy drifts, the
test reports it; this release never edits it).

**Marked in place at all four sites:** the Stage-5 test row (`:560` region),
Amendment 1 §A5's Doctrine-pin row (`:1330` region), Amendment 1 §A8 (the seam,
`:1420` region), and Amendment 1 §A16's test row (`:1558` region). The §A8
touch-point list and the commit-plan file list are updated to the successor name.

### G2 — C2-2 (BLOCKING): the two remaining unmarked sites + the dead `SetSkipCache` citation

| Site | Marker applied |
|------|----------------|
| **Stage 4 task 4.8** (`:404`) | *(superseded by Amendment 5 / ADR-16 — the instance is SHARED; and the `SetSkipCache` citation is **DEAD**: the manager seam was **removed by v194** — zero hits repo-wide at `2b987bf`. The manager is cache-blind by design (`store_group_rewrite.go:277-283`); the caches ride the **proxy config** (`UpstreamSkipCache:` / `ModelSkipCache:`, `cmd/main.go:255-256`) and the decision handler receives the SAME instances via its own setters.)* The corrected wiring: `SetModelsConfig(modelsConfig)` + `SetUpstreamSkipCache(upstreamSkipCache)` + `SetModelSkipCache(modelSkipCache)` — the process-wide instances of `cmd/main.go:242` / `:249`. |
| **A19 rows AM-1 / AM-2** (`:1603-1604`) | *(superseded by Amendment 5 / ADR-16 — AM-1 DISSOLVES with I-1 (the landed sweep is policy-headed + cache-blind; no picker is needed); AM-2's "second instance" rationale is VOID (the fence is LAYER separation, not instance separation). AM-3 on the same table was already marked — these two are now marked to match.)* |

(The other two sites the review flagged — `phase3-plan.md:75` and `:1198` — are
**phase-3** documents, outside this tranche's file scope; recorded for the
phase-3 owner: the operator-verification line must verify **LAYER separation**,
not instance separation, per ADR-18 C2-2.)

### G3 — C.1 (LEADER: ENCODED, no deferral): the four vocabulary pins, named + staged

The leader's decision (ADR-18 T1–T4) folds the four vocabulary pin tests as
NAMED tests. T1 is **partially covered already** — stated, not duplicated: the
DB arm is `TestGroup_AddModel_DecisionMembers_MixedProviders_Accepted` and the
POST+PUT arms are `TestKindRules_Parity_Group_DecisionMembers_MixedProviders`
(both in the added list); T1 needs **no new name**.

| Pin | New named test | Stage | File | Asserts |
|-----|----------------|-------|------|---------|
| **T2** | `TestDecision_Vocabulary_OutOfSetProvider_Rejected_AtAllGates` | **Stage 3** | `pkg/ui/kind_rules_parity_test.go` | A provider **outside** `{openai, typesafe, openrouter}` is rejected for a decision model at every gate: the canonical rule (`pkg/models/config.go:293-300`), the DB write path (`store.go:2063` region), the POST mirror (`server.go:1171`), and the PUT mirror (`server.go:1636`) — parity across all sites |
| **T3** | `TestDecision_Vocabulary_SourceLiteralPin_FiveSurfaces` | **Stage 6** | `pkg/decision/vocabulary_pin_test.go` (new) | The **source literals cannot drift independently**: the FE constant (`DecisionModelForm.tsx:29`), the canonical gate (`config.go:293-300`), the POST mirror (`server.go:1171`), the PUT mirror (`server.go:1636`), and the registry init (`registry.go:50-52`) all carry the same `{openai, typesafe, openrouter}` set |
| **T4** | `TestDecision_Vocabulary_CredentialModelSplit_AnthropicSaves_ModelRejected` | **Stage 3** | `pkg/store/database/models_manager_kind_test.go` | The **credential/decision vocabulary split, positive case**: an **anthropic** credential **saves successfully** (`validProviders` includes it, `pkg/models/credential.go:15-60`) while a decision **model** referencing it is **rejected** (D4's gate — the maps are nested, never crossed) |
| **T5-Go** | `TestDecision_Vocabulary_RegistryKeySetEqualsGateAllowList` | **Stage 6** | `pkg/decision/vocabulary_pin_test.go` (new) | The **registry key set ≡ the gate allow-list set** (`registry.go:50-52` vs `config.go:295`) — the real anti-saveable-but-502 pin (V1/T5-FE already landed in phase 2) |

### G4 — STAGE 0 (kickoff): the permanent base-currency gate (LEADER APPROVED)

Added to the plan as **Stage 0-A** (immediately before the existing Stage 0,
which is marked *Stage 0-B (implementation)* — no renumbering):

> At implementation **kickoff**, giter re-anchors the branch to fresh
> `origin/latest` (**it moved again during review** — `a2952a9` / master
> `8d434ba`; the CG-4 class recurs); the developer runs a **bounded
> pin-reverification sweep** (every BASE-relative pin re-checked, every drift
> flagged and **adjudicated BEFORE stage work starts**); the anchor-of-record
> SHA goes into the KV row; the ship-step full-gate re-run stays **unconditional
> regardless of merge conflicts**. **The corpus remains `2b987bf`-pinned for
> cycle 3 — no re-anchor mid-review** (ADR-18 STAGE 0).

### G5 — the optional sweep, folded (the in-scope items)

- **§E11's stale count**: "63 names" → marked superseded; the authoritative list is **69 → 73** after this tranche (G3's four + the renamed count).
- **§D4's heading typo**: "W-event-rename (ADR-15): `member_suppressed` → `member_suppressed`" — the LHS was already renamed by the Wave-A global substitution; corrected to the original form for the record.
- **A8's touch-point list + the commit-plan file list**: the dissolved `layer_*.go` names updated to the successor (`pkg/decision/suppression.go`).
- **Out of scope for this tranche** (recorded for the owners): the phase-3 items (C2-3 R11 abort row, C2-4 deploy/dcs numbers, C2-5 Tier-1 table/command, the stale count prose in phase3, the advisory's dangling `store_group_policy.go` lines) — they live in `phase3-plan.md` / `architecture-recommendation.md`, not in this file.

### G6 — test delta

**Added (G3 — 4):** `TestDecision_Vocabulary_OutOfSetProvider_Rejected_AtAllGates`,
`TestDecision_Vocabulary_SourceLiteralPin_FiveSurfaces`,
`TestDecision_Vocabulary_CredentialModelSplit_AnthropicSaves_ModelRejected`,
`TestDecision_Vocabulary_RegistryKeySetEqualsGateAllowList`.

**Renamed (C2-1 — the pin target, not a test name):**
`TestDecision_DoctrineLine_PinnedVerbatimAtSeam` keeps its name; its **target
file** changes from the dissolved `pkg/decision/layer_upstream.go` to
**`pkg/decision/suppression.go`** (+ the optional read-only skipcache drift
assertion).

⇒ **The authoritative added top-level test list is now 73 names** (69 + 4).
