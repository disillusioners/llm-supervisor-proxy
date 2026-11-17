# Architecture Recommendation — decision-groups

**Commission:** decision models served by a GROUP (text-lane resilience), plus an admin
"Decision Models" container with a vertical left-nav `[ models | group ]`.
**Worktree:** `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-decision-groups`
(branch `feature/decision-groups` @ `24c51ef` == `origin/latest`).
**Author:** architect (independent validation of the planner's ruling — not a restatement of it).
**Status:** IN PROGRESS — §0–§2 are final; §3+ fill as evidence lands.
**Scope:** architecture only. No code, no tests, no config edits, no commits, no pushes, no deploys,
no KB/POK writes.

> **Authority order for this document.** (1) The USER RULING in §0 — authoritative, supersedes the
> planner and supersedes my original brief. (2) Source code as read in this worktree. (3) The
> planner's `decisions.md` / phase plans — treated as the *proposal under test*, not as truth.

---

## §0 — THE USER RULING (authoritative; encoded verbatim)

### R1 — The skip cache must not decide the group pick
Pick = **POLICY** (pools, weights, sticky), stable. **A mark never changes WHICH member is the head.**

### R2 — Marks suppress only the attempt
A mark suppresses **only the attempt** (zero-cost skip, no upstream call) — **never the pick, never
the chain**.

### R3 — "Failed" is defined by consumed resilience
A member is **not** failed until **its own** resilience is consumed: **hedge/racing + its own
`fallback_chain` FIRST**; only then advance to the next group member.

### R4 — Chain-only models are a supported design
Models deliberately created **INACTIVE**, not exposed to any group, reachable **only** as a
`fallback_chain` target (real example: `coding4`, reachable only via `agentic-Main`'s chain).
Validation **MUST ALLOW** inactive / chain-only targets. **Disabled-but-chain-referenced is NOT a
data defect to clean up.**

### R5 — Racing must be consumable
A **single-model list must still be able to hedge**, else "racing consumed" is impossible for
chain-less members.

### DOCTRINE LINE — to be pinned verbatim at the seam

```
The cache is an optimization, not the truth: it may reorder candidates, never exclude them — and it may suppress an ATTEMPT, never a PICK or a CHAIN.
```

**Where it is pinned (and where it deliberately is NOT, this release):**

| Location | Action | Why |
|---|---|---|
| The **decision-lane pick/attempt seam** (the new group branch in `pkg/decision/handler.go` and whatever helper performs the policy-pick → attempt-walk, to be named exactly once the loop's shape is fixed) | **PIN VERBATIM**, as a package-level doc comment on the pick function **and** as a `// DOCTRINE:` comment immediately above the marked-member attempt-suppression branch | This is the seam the ruling governs and the only lane this release may change |
| `pkg/decision/doc.go` (currently `:18` states "no race coordinator — decisions are cheap+fast, sync") | **AMEND**, because R3/R5 make that sentence false for the group branch | Stale doctrine is how the next agent re-derives the wrong semantics |
| `pkg/skipcache/skipcache.go` package doc (`:13-26`, which today carries only the v193 "may reorder" half) | **DEFER — do NOT rewrite this release** | The full line asserts text-lane semantics the text lane will not have until the *parallel* commission lands (§2.2). Writing it now makes the package doc describe behaviour that doesn't exist. Pin it here when the parallel commission lands, as one line, not as part of this release |
| `pkg/store/database/store_group_rewrite.go:88-95` (the sweep's v193 doctrine block) | **LEAVE BYTE-UNTOUCHED** | Text-lane behaviour is frozen this release (§2.2). The sweep's wording is the text lane's truth until the parallel commission changes the sweep |

---

## §1 — What the ruling supersedes in the planner's documents

| Planner position | Superseded by | Consequence |
|---|---|---|
| D-4 / plan overview §5: "**Marks are ordering inputs, never candidacy filters** — a marked member is still attempted (**demoted**, never removed)"; acceptance criterion 7 verbatim | **R1 + R2** — demotion *is* mark-driven reordering; R1 forbids the mark from changing the head at all, R2 restricts the mark's effect to suppressing an **attempt** | The plan's central reuse claim ("reuse the four-phase sweep, inherit v193 for free") **does not hold** on the decision lane. See §2.1 for the proof |
| D-4's demotion step, and blast-radius rows 16/17 ("sweep carries the inferred lane into the predicate"; "ExcludeGroupMember lane-aware") | **R2** | `ExcludeGroupMember` exists to *remove a member from the remaining offer walk* — that is pick-level suppression (it changes the chain). Under R2 the decision lane must not use it to express a mark |
| D-3's member loop: `per-member credential → upstream call → advance` | **R3** | One upstream call per member is *not* "resilience consumed". The member's own `fallback_chain` must be walked first. See §2.3 |
| ADR-2 / E-6 ("member-level fallback **accepted as a reduction**, pending evidence") | **R3 + §C of the brief** | No longer a reduction to be signed off: member-level ordering is the **order of operations**. E-6 is **closed as "not a reduction"**, and the residual question is only whether *credential* multiplicity is part of "its own resilience" |
| D-5's "one honest attempt when everything is benched" (`store_group_rewrite.go:117-124`, `:229-243`) | **R2** | Realigned: under R2 a fully-marked member set does not need an "honest attempt" *as a special case* — every attempt is suppressed, the request fails **pre-commit** with the exhaustion envelope. That is the same observable outcome, but for a different reason, so the *code path* is new, not inherited |
| plan overview §5 marker choice and the "42-site" framing | Unchanged in substance | Marker must change: `decision_all_members_failed` may still be the exhaustion code, but §8 will state the measured control requirement |

---

## §2 — Two contradictions I verified directly (before any worker report)

### 2.1 🔴 The four-phase sweep **is** mark-driven reordering — it cannot be reused under R1

`pkg/store/database/store_group_rewrite.go:88-124` (comment + Phase docs, read directly):

- Phase 1 classifies each engine offer; a **`skip-marked` or engine-cooling** member is
  "**DEMOTED into the bucket**" (`:105-108`).
- Phase 3 sorts the bucket **by `(expiry asc, ref order asc)`** (`:117-120`).
- Phase 4 **SERVES the head of that sorted bucket** (`:121-124`: "the head is SERVED — flows into
  the race list like any other pick").

So today, on the text lane, a mark **does** change which member is served first — that is the entire
point of the demotion ordering, and it is exactly what **R1 forbids** ("a mark never changes WHICH
member is the head"). Verified mechanics of the demotion key at `:145-180`
(`memberDemotionExpiry` → `bucket` append → `RebindPast`) and `:273-282`.

**Consequence — the plan's headline justification is wrong on its own terms.** D-1/D-4 argue "reuse
the sweep and inherit v193 for free". Under this ruling the decision lane must **not** reuse
`pickServableGroupMember`'s ordering: it must take the **policy head** (pools → weights → sticky)
and, if that head is marked, **skip its attempt at zero cost and move to the next member in policy
order** — the policy order being completely unaffected by marks. Reusing the sweep would implement
pre-*R1* semantics and would be a silent behavioural divergence between the two lanes' *contracts*,
which is worse than a divergence in code.

**This does not sink Option A** (the entity is still shared and `grouplb` is still engine-level
kind-blind), but it re-scopes Stage 4 and it makes Stage 2's "parameterize the predicate" necessary
rather than sufficient. It also means **`ExcludeGroupMember` is no longer the right expression for a
mark on this lane** — excluding changes the chain, which R2 forbids. What the decision lane needs
instead is a *policy-order iterator* + a *mark check at attempt time*. Whether the existing engine
can be driven in "policy order, no demotion" mode without changing text behaviour is the single
question §3.1/§3.2 must answer.

### 2.2 🟡 Lane gating is not optional — the sweep is shared code

`pickServableGroupMember` / `RewriteGroupEntries` live in `pkg/store/database` and are reached by
both lanes through `models.GroupRewriter`. Any change to their ordering semantics is a **text-lane
change**, which §2.2 of the brief forbids in this release. Gating seam and collision hotspots are
§3.2's deliverable; the constraint is recorded here so no downstream reader "fixes" the sweep.

### 2.3 🔴 The decision lane has **no** resilience to consume today — R3 is new machinery

Verified directly:

- `pkg/decision/doc.go:18` — "no race coordinator — decisions are cheap+fast, sync".
- `pkg/decision/handler.go:430` — "**Single upstream call (no retry / fallback / race** — the …".
- `pkg/decision/test_exec.go:175` — same statement in the test seam.
- `grep -rn "FallbackChain\|fallback_chain" pkg/decision/ --include=*.go` → **0 hits** (tests included).

So today, on this lane, "member failed" ≡ "one upstream call failed". R3 says that is not failure —
the member must consume **its own** `fallback_chain` (and hedge/racing where the lane has it) first.
R5 then says a single-model list must still be able to hedge. Since the lane has no race engine and
the ruling forbids inventing one ("do not invent a race engine"), §3.3 must state the **minimum**
honest translation of R5 for a non-streaming typed lane. My current reading (to be confirmed by
§3.3, not asserted as settled): the consumable unit of resilience on this lane is **the member's own
`fallback_chain` walk** — a chain of length ≥1 *is* the hedge, so a chain-less single member
satisfies R5 only if the chain is materialized (at minimum: the member itself as a one-hop chain),
and "racing consumed" must be observable as an **attempt count**, not as a race.

---

## §3 — Mandatory adjudications — dispatch map

| Brief section | Adjudication | Worker | Skill | Status |
|---|---|---|---|---|
| **A** | Reconcile "may reorder" with R1; observable contract (pick event / attempt walk when head is marked / demotion's fate / win-clear target / telemetry distinguishing pick vs attempt vs skip) | W5 | `resilience-design` | PENDING |
| **B** | Lane-gating seam + regression pins + collision hotspots with the parallel text-lane commission | W5 | `resilience-design` | PENDING |
| **C** | Decision-lane order-of-operations table; does the lane honor `fallback_chain` today (→ §2.3, already verified NO); racing today; R5's honest minimum | W6 | `resilience-design` | PENDING |
| **D** | R4 validation audit — every site that would reject an inactive/chain-only target; corrected rule; no silent auto-enable; chain-only never a group candidate | W7 | `system-decomposition` | PENDING |
| **E** | R5: no ≥2-member requirement; "racing consumed" observability for a chain-less single member | W6 | `resilience-design` | PENDING |
| **F1** | Extend vs parallel (A/B) + complete `IsGroup` consumer inventory | W1 | `system-decomposition` | IN FLIGHT |
| **F2** | Lane inference (D-2) risk + the test that would have caught the v193 class | W2 | `data-flow-design` | IN FLIGHT |
| **F3** | Silent-unservability negative control | W2 + §3.5 | `data-flow-design` | IN FLIGHT |
| **F4** | Decorator-forward enumeration (POK `modelscache-decorator-forward-trap`) | W3 | `structural-design` | IN FLIGHT |
| **F5** | FE structure, no-fork feasibility, lane-consistency guard, vertical-nav a11y | W8 | `structural-design` | PENDING |
| **F6** | API/validation parity + provable no-vocabulary-change | W9 | `system-decomposition` | PENDING |
| **F7** | Ship/deploy blast radius, marker + false-positive control, safe non-destructive E2E failover | W10 | `resilience-design` | PENDING |

---

## §4 — Trade-off matrices — PENDING (filled at fan-in)

## §5 — Risks — PENDING (filled at fan-in)

## §6 — Decisions pending / open questions — PENDING (filled at fan-in)

---

## §0b — SECOND USER RULING: PER-LAYER CACHES (authoritative; encoded verbatim)

> Build the **DECISION lane** with **PER-LAYER caches** from day one, not a single shared one:

| Layer | Question it answers | Populated by | Read by — ONLY |
|---|---|---|---|
| **GROUP** | "is this decision member servable in this group?" | policy facts (membership, enabled, lane-kind) | **the pick** |
| **MODEL** | "is this decision MODEL limited?" | model-attributable failures | **the chain/model layer** |
| **UPSTREAM** | "is this provider/credential working?" | credential failures | **credential selection** |

**Layers NEVER read each other's state.** Consequence: *a credential 429 can never change the group
pick — the invariant holds STRUCTURALLY rather than by discipline.* Where the decision lane has no
equivalent of a layer, say so explicitly rather than inventing one; **do not fake a layer just for
symmetry.**

### Leader decisions on OQ-1..OQ-4 (authoritative)

- **OQ-1 — Suppression never removes the last attempt.** When every candidate is marked, the
  **POLICY HEAD gets exactly one honest attempt** (head suppression lifts for that request). The
  cache can never be a failure oracle. *Deliberately diverges from the text lane's
  soonest-expiring rule — lane-gated; the parallel commission owns their alignment.*
- **OQ-2 — The chain clause is VACUOUS for decision members.** D5 is NOT lifted:
  `fallback_chain` stays **forbidden** for decision models this release. Record
  *"no equivalent; deliberately not built."* The MODEL layer has **no chain reader** on this lane;
  its only meaning is model-attributable failure state read within the member step.
- **OQ-3 — STRUCTURAL loop contract.** Single-member list valid and fully consumable; **NO
  speculative hedging engine.** "Racing consumed" = **the loop contract reaching its end.**
- **OQ-4 — Disabled group members are NOT attempted** (servability predicate unchanged). R4 =
  chain targets + validation only; inactive models **excluded from group candidates**; **no
  auto-enable.** Two populations, never conflated.

### Planner constraints accepted with this ruling

1. **The four-phase sweep returns a MARK-INFLUENCED order** (Phase 4 sorts by
   `memberDemotionExpiry`, `store_group_rewrite.go:229-242`) — matches my §2.1 verification. The
   decision lane must take **POLICY-ONLY ordering**, lane-gated away from the sweep's sort, and
   apply attempt suppression itself. Seam must be chosen explicitly: **parameter | separate path |
   fork of the ordering function — pick one and justify** (§3.2).
2. **SCOPE EXPANSION (accepted): per-credential attempts WITHIN a member (AC-R4).** Stage 4's
   one-credential-per-member design is **superseded**. `pkg/credentiallb` is import-legal from
   `pkg/decision`; `ExcludeAndReselect*` is the text-lane mirror. This makes the UPSTREAM layer
   **REAL** on this lane: reader = per-credential selection in the member loop; writer =
   credential-failure classification. The upstream-layer cache is state that writer/reader pair
   owns, structurally invisible to the pick.
3. **AC-C1 (pin one-credential-per-request) is VOID** — it would pin the defect R3 removes.
4. **Event vocabulary needs a SKIP outcome distinct from attempt/failure** (pick vs attempt vs skip
   observability).
5. **Doctrine line pins VERBATIM at the suppression decision site** — exact file/function named in
   the final §3.1.

---

## §1a — Amendments to the supersession ledger (second ruling)

| Prior position (incl. mine) | Now | Correction |
|---|---|---|
| **My §2.3 above**: "R3's `fallback_chain` walk is new machinery — the member's own chain must be walked first" | **WRONG — superseded by OQ-2** | The chain clause is **vacuous** for decision members; D5 keeps `fallback_chain` forbidden. The member's "own resilience" on this lane is **its credentials** (AC-R4), not a chain. §2.3's *evidence* stands (0 chain hits, no race coordinator, single upstream call — all verified); only the *inference* is corrected |
| Planner D-3 / Stage 4: one credential per member; E-6/ADR-2 "member-level, pending evidence" | **Superseded by AC-R4** | Per-credential attempts within a member are IN scope; `pkg/credentiallb` participates on this lane. E-6 is closed as **resolved-by-expansion**, not as a reduction |
| Planner D-4/D-5: "one honest attempt when everything is benched" via the sweep's soonest-expiring order (`store_group_rewrite.go:117-124`, `:229-243`) | **Superseded by OQ-1** | The honest attempt goes to the **POLICY HEAD**, not the soonest-expiring member. Different rule, different code path — cannot be inherited from the sweep |
| R3's "hedge/racing first" | **Superseded by OQ-3** | No speculative hedging engine; "racing consumed" = the loop contract reaching its end. Single-member list is valid and fully consumable |
| R4 as I framed it in §0 | **Refined by OQ-4** | Chain-only/inactive models are a supported population for **chain targets + validation**; but disabled members are **not attempted** in groups and inactive models are **not group candidates**. Two populations, never conflated |
| A single decision-lane skip cache (implicit in any reading of R1/R2) | **Superseded by the per-layer ruling** | Three caches with structural isolation, not one shared store |

**Open verification items carried into §3** (do not assume): (i) `pkg/credentiallb` import-legality
from `pkg/decision` is a *planner claim* — the transitive-closure guard
(`pkg/decision/handler_test.go:90`) must be re-run against `pkg/credentiallb`'s actual import graph;
(ii) D5's exact text forbidding `fallback_chain` for decision models
(`pkg/models/config.go:302-318`) — cite it, don't assume it; (iii) whether any decision model today
carries >1 credential (the AC-R4 reader path only matters if multiplicity is representable —
`store.go:2552-2567` single-cred fast path vs `pkg/credentiallb` `GetOrSelect`).

---

*(§3 dispatch map superseded — see §3-REV below.)*

## §3-REV — Dispatch map (post second ruling)

| Section | Deliverable | Worker | Skill | Status |
|---|---|---|---|---|
| §3.1 | Observable contract: pick vs attempt vs skip; OQ-1 honest-attempt; win-clear under layers; SKIP event; **per-layer cache architecture** (layer map, structural-isolation seam, relation to `pkg/skipcache`, name-collision hotspots, the three pinning tests); doctrine pin site | W5 | `resilience-design` | PENDING |
| §3.2 | Lane-gating seam choice (**parameter vs separate path vs fork** — pick one, justify); regression pins proving text byte-untouched; **collision hotspots** with the parallel text-lane commission; `pkg/credentiallb` import-legality verification | W6 | `structural-design` | PENDING |
| §3.3 | Final order-of-operations table (leader-settled) with today-state file:line per step; D5 chain-forbidden citation; credential-multiplicity-today evidence; ≥2-member check (R5/OQ-3) | W7 | `system-decomposition` | PENDING |
| §3.4 | R4 validation audit under OQ-4: every site that would reject an inactive/chain-only target; corrected rule; no-auto-enable; inactive ≠ group candidate | W7 | `system-decomposition` | PENDING |
| F1/F2+F3/F4 | Unchanged from §3 | W1/W2/W3 | — | IN FLIGHT |
| F5/F6/F7 | FE, API parity, ship/E2E | W8/W9/W10 | `structural-design`/`system-decomposition`/`resilience-design` | QUEUED |

---

## §2a — Interim evidence register (condensed worker findings; full reports live in the dispatch transcript)

### W1 — extend vs parallel (system-decomposition) — VERDICT: A confirmed; planner's rationale partly wrong

- Engine claim VERIFIED **stronger** than stated: the whole `pkg/grouplb` package is stdlib-only
  (engine/binding/selector/events/doc/testhooks), `MemberRef` carries no kind; import guard
  `engine_test.go:401`. Sweep kind-touchpoints = exactly 3 `isServableGroupMemberBase` call sites
  (`:164/:188/:212`) + the predicate `:291-301`; ordering comparator compares only `(expiry, idx)`
  `:233-238`; `memberDemotionExpiry` `:273-282` and `ClearGroupMemberCooldown` `:439-444` are
  kind-blind.
- **Planner OVERSTATES** Option B's engine cost (`pkg/grouplb` = 1,988 lines reusable wholesale;
  B's engine cost ≈ 1 seeding-gate line at `store.go:1667`) and **UNDERSTATES** the rest: B owes
  the 169-line order-sensitive sweep, ~234 lines of inline UI mirrors (the duplication class that
  already produced the repo's highest-severity review finding, `ui/server.go:780-785`), ~18–20 new
  kind-mirror arms + a migration + a re-implemented copy invariant. Net B ≈ +1,700 lines. D-1's
  rationale #1 ("B re-earns the lane work") is **wrong** — D-3/D-4/D-5 are option-neutral.
- **42-site table: no missed group-row surface, but 4 material non-group gaps:**
  - 🔴 **G1 fallback-chain bypass** (cross-confirmed by W2): chat main's `FallbackChain` may
    contain a group id (`pkg/proxy/handler_functions.go:200-202`, `handler_anthropic.go:290-292`
    build `[primary]+FallbackChain`; rewrite loop iterates ALL entries
    `store_group_rewrite.go:52`; write-side validates existence only `pkg/ui/server.go:1996-2010`,
    `pkg/models/config.go:1021-1025`; misroute guards check only `rc.resolvedModel`
    `handler.go:576`, `handler_anthropic.go:272`). With a sloppy predicate the decision member
    **silently enters the chat race list** (no per-attempt kind guard exists in
    `race_executor.go:114-140`); with a correct parameterized predicate the whole chat request
    502s `group_unservable` (`handler.go:670-684`) even though the primary is healthy. Re-entry
    replays `modelListOriginal` (`handler.go:1102`, `handler_groups.go:480`) — repeats per re-entry.
  - 🟡 G2 peak-hour substitution is chat-only (`race_executor.go:257`, `internal_handler.go:303`,
    `modelscache/models.go:775`); a decision member with `peak_hour_enabled` silently never switches.
  - 🟡 G3 no server-side FE feed for decision-lane groups: `?kind=decision` arm excludes groups
    (`ui/server.go:784`), `?kind=group` arm returns BOTH lanes unfiltered (`:786/:789/:800/:808`) —
    the FE plan's client-side lane filter is load-bearing and undocumented.
  - 🟡 G4 token allow-list picker is chat-only (`TokenForm.tsx:34`); decision-group ids can't be
    allow-listed from the FE (pre-existing class).
  - 🟢 G5 stale docs; G6 entry resolution is claims-first (`cmd/main.go:322` → `modelscache:457`)
    and members MUST use `GetModelNative` (`modelscache:1136-1140`); **G7 pre-existing (not this
    plan's)**: a *disabled group row* is still rewritten/served (`store_group_rewrite.go:56-60` no
    Enabled check).
- **Flip assumption:** A→B flips if the predicate cannot be guaranteed as an **expected-kind
  parameter** on BOTH the primary and fallback-chain paths, or if per-lane observability later
  demands a lane column.

### W2 — lane field, D-2 (data-flow-design) — VERDICT: inferred lane, with two mandatory amendments

- Exact predicates specified (transcribe-ready): `inferGroupLane` (fold over member rows via
  **raw-manager** `GetModelNative`, claims bypassed — never claims-first `GetModel`, which
  self-loops shadowed groups) + lane-parameterized `isServableGroupMemberBase`. Empty fold ⇒
  `(“”, false)` — never vacuous-true.
- Failure-mode table (7 rows): the two SILENT cases are (a) **direct-SQL retype of a single
  member** (ADR-1 blocks only the API path — `UpdateModel` confirmed unguarded
  `store.go:1699-1720`, existing-row kind never compared) and (d) **shadow-target retype**
  (ADR-1 covers members only). Cache staleness (f) is **fail-closed either direction** if the
  inference is cache-backed (≤60s, `ReconcileInterval` `modelscache/models.go:22`) — and the plan
  **contradicts itself**: D-2's cost note says "cache-backed native lookup" while D-8/Stage 2 say
  raw-manager — must be resolved to raw-manager.
- 🔴 **Fallback-chain bypass independently found** (same as W1 G1): the fix is **the lane check at
  the rewrite boundary** (a decision-lane group entry on a chat lane ⇒ `FailedGroupID`), NOT more
  handler guards. Orthogonal to lane representation.
- **v193-class test** (complete spec): seed a decision member, write the group row via **direct
  SQL** (bypassing widened G3), assert `RewriteGroupEntries` returns `FailedGroupID==""`,
  `MemberID=="dec-m"`, AND **no cooldown write** (`MemberCooldownUntil` cooling==false) — the
  no-cooldown clause is the missing assertion in the plan; `err==nil` style assertions are vacuous
  because `RewriteGroupEntries` **never errors and silently drops** failed entries (`:67-69`).
  Also missing: a chat-model-with-decision-group-in-chain ⇒ zero upstream hits to the member.
- Inferred vs persisted 5-axis: inferred wins Complexity/Cost decisively; persisted wins
  Maintainability (SELECT-able lane); **persistence does NOT eliminate the request-time re-check**
  (my hypothesis CONFIRMED) — it adds *routing stability* (a direct-SQL retype fails closed instead
  of silently re-laning), which is diagnosability/soundness of routing, not of selection. sqlc
  regen is NOT actually needed (schema.sql already stale; queries hand-written in
  `querybuilder.go` — 6 sites × 2 dialects). **Recommendation: (i) inferred + rewrite-boundary
  lane check + ADR-1 extended to shadow targets. Confidence HIGH.** Flip trigger: out-of-band SQL
  editing being a normal operator habit, or measured text-lane latency regression (~2N+1 SQL per
  group request).

### W3 — decorator surface (structural-design) — VERDICT: D-8 budget sufficient; no forgotten forwards; 3 plan defects

- **Decisive structural fact:** `CachedModelsConfig` **WRAPS** (`inner strictSource` named field,
  `modelscache/models.go:104-105`) — no method pass-through exists; an absent method is absent,
  and an optional-interface assertion takes its no-op branch **silently**. All 18
  `ModelsConfigInterface` members are implemented; `GroupLane` is the only planned addition.
- All three group forwards exist (`:1043-1048`, `:1056-1061`, `:1079-1085`); `GetModelNative` has
  a cache-first native impl (`:1139+`) that never re-enters `inner`. **No forgotten forward.**
- 🟡→🔴 **F1:** the plan's "missing forward is loud" claim is true for **1 of 4 consumers**. The
  three proxy consumers' `groupLaneOf` fallback is "Unknown lane ⇒ NOT excluded" = **fail-OPEN** ⇒
  a decision group is served on `/v1/chat/completions` + `/v1/messages` and listed in
  `/v1/models`. The decorator-through effect test is the ONLY barrier for those three — must stay
  an effect assertion, and `TestDecision_Group_ThroughCachedConfig` must assert the proxy-side
  gate too.
- 🟡 **F2:** wiring must pin `SetModelsConfig(modelsConfig)` (the decorator, `cmd/main.go:186`) —
  **never `modelsMgr`** — else the raw manager satisfies assertions while the proxy consumers
  (decorator) silently fail (the exact historical CRITICAL-FIX shape, `models.go:1073-1078`).
- 🟢 **F3:** ADR-1 cites "contract 14" for the decorator obligation; the decorator contract is
  **#22**. Also: **D-8's "~11 structural fakes" rationale is STALE** — `models.GroupRewriter` has
  exactly **2** implementers (decorator + raw manager), zero fakes; the 7 named fakes implement
  `ModelsConfigInterface`. Optional-interface is *convenient, not required*.
- **Recommended (zero-cost): compile-time assertion** in `pkg/modelscache`:
  `var _ interface{ GroupLane(string) (models.GroupLane, bool) } = (*CachedModelsConfig)(nil)`
  (in-package precedent `outage_test.go:27`) — converts the trap to a **build failure** without
  widening any interface.
- Decorator-through test spec: real SQLite manager + `NewCachedModelsConfig(mgr, Options{
  ReconcileInterval: time.Hour })` (freeze the reconciler — precedent
  `win_clear_winner_identity_test.go:163`), effect assertion with failure text naming the trap,
  mixed-kind ⇒ `("", false)`, anti-vacuity raw-manager pre-assert, and assertions must go through
  the **structural assertion form** (direct method calls would turn a missing method into a build
  break instead of a red test).
- Double-buffering impossible (import cycle `modelscache`→`store/database`); lane inference as
  planned is DB-direct ⇒ staleness ~0; the ≤60s window exists ONLY in the counterfactual
  cache-backed variant.

---

## §2b — 🔴 PREMISE CORRECTION (verified by me, not just reported): the branch base is stale

**`origin/latest` has moved.** The parallel commission's v194 (wi-4d2c8e17 *"cache-layer-separation"*)
is **already merged**: `origin/latest = 2b987bf`, `origin/master = fd1649c`. Worktree HEAD `24c51ef`
is behind. Verified directly (`git log origin/latest`, `git diff --stat 24c51ef..origin/latest`,
`git show 2b987bf:pkg/skipcache/skipcache.go`). Diffstat highlights: `store_group_rewrite.go` +121,
`skipcache.go` +57, `race_executor.go` +236, `race_coordinator.go` +280, NEW `pkg/modelcache/`
(4 files), NEW `pkg/proxy/cache_layer_separation_test.go` (+1456), FE `EventLog.tsx`/`types.ts` touched.

**What this changes architecturally (decision-level):**

1. **The per-layer ruling is ALREADY STRUCTURAL upstream.** The `pkg/skipcache` package doc at
   `2b987bf` declares the three-layer contract verbatim — GROUP ("is this member servable IN THIS
   GROUP?" — grouplb policy) / MODEL (`pkg/modelcache`) / UPSTREAM (skipcache) — *"The layers NEVER
   read each other's state"* — and carries the doctrine line in full, including *"may suppress an
   ATTEMPT, never a PICK or a CHAIN."* The decision lane's job is to **BIND** this architecture,
   not re-invent it. (The "deferred pin" in my §0 table is already done upstream — moot.)
2. **The sweep is already policy-only at `2b987bf`.** `pkg/store/database`'s `pkg/skipcache` import
   is deleted; `memberDemotionExpiry` = engine rotation cooldown only; marks are structurally
   unreachable from the text-lane group pick. My §2.1 contradiction is **true at `24c51ef` and
   RESOLVED upstream** — the decision lane inherits policy-only ordering by rebasing, and the
   "lane-gate away from the sweep's sort" problem reduces to *using the engine, not the sweep's
   chat-kind predicate*.
3. **Mandatory rebase `24c51ef → 2b987bf` before implementation.** Every pin the planner named is
   re-anchored: `store_group_skipcache_test.go:62` (HealthAwareSelection, "marked member NEVER
   picked") becomes `:59` `MarksDoNotChangeThePick` + `:85 ProviderWallDoesNotChangeThePick` +
   `:106 AllMarked_StillServes` + `:128 MarkNeverBenchesOrderingSeam` — the **contract inverted**,
   i.e. R1/R2 semantics are now the *pinned text-lane behavior*. `win_clear_winner_identity_test.go:218`
   → `:217`.
4. **"Text lane byte-untouched" is redefined**: this release must not **regress v194 semantics**
   (policy-only ordering, layer separation), not preserve the `24c51ef` demote order.

### W6 — gating seam (structural-design) — VERDICT: (b) SEPARATE PATH; zero new manager surface for ordering

- **Seam choice:** a new decision-lane policy-order module (`pkg/decision/group_policy.go`) driving
  the kind-blind `pkg/grouplb` engine through the **already-public** `ModelsManager.GroupEngine()`
  (`store.go:752`) — `GetOrSelectWithRepair` (`engine.go:408`), `PickBackupMemberWithRepair`
  (`:880`), `ExcludeAndReselect` (`:747`, writes rotation cooldown `:788`), `ClearCooldown`
  (`:1274`), `MemberCooldownUntil` (`:1293`). Injected into the handler via the existing
  setter-seam pattern (`SetModelResolver` `handler.go:740`; wired `cmd/main.go:321-328`).
  **Parameter (a)** breaks `models.GroupRewriter` (2 implementers + fakes) and edits the file v194
  just rewrote; **fork (c)** duplicates policy semantics. Separate path touches **zero** shared
  files for ordering: `pkg/decision/*`, `cmd/main.go`, `pkg/models/group.go` only.
- **`GroupLane` is decoupled from ordering** — needed only for the misroute-guard/`/v1/models`
  consumers; cost = 1 optional method + 1 decorator forward + 1 decorator-through test (D-8).
- **`pkg/credentiallb` import-legality: CONFIRMED** (planner claim holds). Per-file imports:
  `engine/binding/selector/testhooks.go` → `pkg/models` only; `failure_class.go`/`key.go` → stdlib
  only. `go list -deps` closure = `credentiallb → models → crypto`; **no proxy/providers/imggen**.
  The `pkg/decision` guard (`handler_test.go:39-60/:62-83/:85-108`) walks the full import graph via
  `go list -deps` and **already contains** `store/database → credentiallb` today — adding the
  direct import adds nothing to the closure. ⚠️ The guard is a **text regex over every .go file
  including comments** — a comment naming `pkg/proxy/...` trips it.
- **Corrections to received wisdom:** `ClearCooldown :1274` is `pkg/grouplb` (group-member rotation
  row), NOT `pkg/credentiallb` — **`pkg/credentiallb` has no public cooldown clear** (0 hits), so a
  decision-lane win-clear can clear the group row but **cannot** clear the credential cooldown row
  → flagged gap. Credential engine is NOT behind the decorator (`cmd/main.go:137` raw `modelsMgr`)
  → no forward needed for credential access.
- **Gating mechanics (3 stages, zero cross-reads):** (1) PICK — GROUP layer only (engine offers,
  empty conversation key ⇒ no binding, D-6); never reads demotion/skipcache/modelcache. (2)
  ATTEMPT — MODEL layer only (`modelcache.Marked(memberID)` → zero-cost suppression), mirroring
  v194's `race_executor.go:454`. (3) CREDENTIAL — UPSTREAM layer (`credentiallb`
  `GetOrSelect`/`ExcludeAndReselectForClass :637`), never the member pick. OQ-1 = suppression lifts
  for the policy head.
- **Doctrine pin sites (seam b):** package doc + `// DOCTRINE:` above the suppression branch, both
  in `pkg/decision/group_policy.go` (symbols: `decisionGroupPolicyOrder` /
  `decisionMemberAttemptSuppressed`); `pkg/decision/doc.go:18` ("no race coordinator…") must be
  amended — now false for the group branch.
- **Open questions carried upward (unresolved by W6):** (i) **engine key scoping** — a chat-lane
  request naming a decision group rewrites it (the G1 hole) and writes **60s rotation cooldowns on
  decision members** (`store_group_rewrite.go:165` → `ExcludeAndReselect` → `engine.go:788`):
  cross-lane pollution of GROUP-layer rotation state; lane-scoped key (`"decision:"+groupID`)
  isolates but the manager seeds only the raw `groupID` (`store.go:1666-1671`). (ii) **policy-order
  enumeration** — the engine yields ONE weighted head per pool; `RebindPast` is a no-op for an
  empty conversation key (`engine.go:1216`) → no cooldown-free "next candidate" API; walk shape =
  engine head + configured-order tail vs lane-local weighted permutation. (iii) whether
  `pkg/decision` may import `pkg/modelcache`/`pkg/skipcache` (not in v194's AST-pin scope, but no
  test enforces it). (iv) `credentiallb` cooldown-clear absence (win-clear gap).

### W9 — API/validation parity (system-decomposition) — VERDICT: surface NOT complete as planned (6 blocking, 4 non-blocking gaps)

- All plan line-cites EXACT except one (`server.go` PUT member loop is `:1414-1447`, not
  `:1417-1450`). UI POST/PUT **never call the canonical validators** — hand-written inline copies
  (`server.go:863-979` / `:1364-1481`); the parity test is the only drift lock.
- **Vocabulary proof: PROVEN at source, NOT proven by the plan's test.** Group write path is
  provider-agnostic at all 3 sites (`store.go:2113` passes `""`; `group.go:61` ignores it; group
  rows never reach `validateModelAgainstCredentials`, branch at `store.go:2091-2093`). The decision
  set `{openai,typesafe,openrouter}` is a strict **subset** of the 15-entry credential set;
  `validProviders` is read only by `CredentialConfig.Validate()` (`credential.go:167`); the
  per-ref rule (`store.go:1973-1986`) forces all refs of one model to share the primary provider —
  the maps are **nested, never crossed**. The plan's mixed-provider group test proves clause (a)
  only. Required test set: T1 mixed-provider group (DB+POST+PUT) · T2 out-of-set provider rejected
  at DB `config.go:293-300` via `store.go:2063` AND UI `:1171`/`:1636` · T3 source-literal pin of
  the 5 vocabulary surfaces · T4 anthropic credential saves while a decision model referencing it
  is rejected · **T5 registry key set == gate allow-list set** (`registry.go:50-52` vs
  `config.go:295`) — the real anti-saveable-but-502 pin.
- 🔴 **G1 ADR-1 is implemented NOWHERE** — `decisions.md:428` rules it "same release";
  `phase1-plan.md:706-713` fences it out; `phase2-plan.md:578` marks it "Open — phase 1" (a phase
  that never schedules it). The single-member re-lane hole stays open. Fix: Phase-1 stage —
  canonical guard inside `store.go` `UpdateModel` (fresh `scanModels` — the lock-free scan
  `RemoveModel` already uses, `store.go:1814-1831` — plus an **old-kind read**, which today's
  existence-only dummy scan `:1723-1730` lacks) so it fires only on an actual kind change; UI PUT
  inherits via `server.go:1703-1709` bare `{"error":…}`. **No new manager method, no decorator
  forward** (`GetModels` is forwarded + write-through, `modelscache/models.go:533/:804-871`) — D-8's
  budget preserved. Status: recommend **409 Conflict** mirroring the Am27 delete-guard idiom
  (`store.go:1828` + `server.go:1734` substring mapping) — needs a ruling (ADR-1 says only "4xx").
- 🔴 **G2 ADR-1 does not cover `shadow_model_id` targets** (matches W2's amendment). G8's
  shadow-kind rule is checked only at group-write time (`group.go:196-198`) ⇒ a retyped shadow
  target persists silently AND the group's claim then hides a chat model from `/v1/models`
  (`handler.go:337-341`) — a live cross-lane leak. Draft extension + error text provided
  (`model <id>: kind cannot be changed while it is the shadow target of group <gid> (remove the
  claim first)`).
- 🟡 G4 the "≥1 non-backup default member" rule (`group.go:139-147`) is canonical-only — absent in
  both mirrors, and the parity harness **cannot express it** (`marshalGroupModel` drops
  `backup_only`, `kind_rules_parity_test.go:467-501`). 🟡 G5 parity assertions are substring-based
  ⇒ "byte-identical chat wording" is unpinned. 🟡 G6 widened rule ORDER makes the member-kind
  message sibling-dependent; needs a `member-dec` fixture + lane-aware PUT base + mixed-kind cases.
  🟡 G7 `/v1/models` lane derivation unspecified — snapshot-fold over `GetEnabledModels()` diverges
  from manager inference (which counts disabled members); recommend all-models fold + a pin.
  🟢 G9 stale `{openai,typesafe}` comment (`credential.go:51-53`); 🟢 G10 "groups STAY listed"
  comment (`handler.go:329-332`) becomes false.
- **Contract 15 is unmet today**: a decision-lane group has `Kind=="group"` ⇒ `IsDecision()` false
  ⇒ it is **NOT excluded from `/v1/models`** (`handler.go:349-352` iterates enabled models,
  skipping only image-gen/decision *models*). FE contract to state explicitly: **`?kind=group`
  returns mixed lanes** (`server.go:785-787`) — the FE tabs must split by lane.

### W10 — ship/deploy + E2E (resilience-design, premise-corrected) — VERDICT: runbook facts corrected; phase3-plan has a self-contradictory gate

- **Version facts (verified via public Actions API + Hub):** CI **#194 is taken** (head_sha
  `fd1649c`, 10m37s) ⇒ this release = **#195**; app version string is the bare run number
  (`/fe/api/version` → `{"version":"195"}`, no "v"); OCI label = `"master"`. Repo is public ⇒
  Actions REST pollable **unauthenticated** (60/hr; `?head_sha=` gives the exact run) — the stall
  guard is implementable with no `gh`. Baseline run time 9-13 min; stall = no step transition ≥30
  min. `docker-ci.yml:6-8` declares `workflow_dispatch` ⇒ the plan's "Actions-UI retry rebuilds
  nothing" claim is **false** — a manual run is a first-class recovery path.
  > **⚠️ SUPERSEDED (2026-11-16, corpus B1 correction — the content above is RETAINED as the
  > architect's original advice, NOT as current guidance): a `workflow_dispatch` run is
  > **COMPILE-ONLY and can NEVER publish** — every publish step in `docker-ci.yml` is gated on
  > `github.event_name == 'push'` (`:110/:117/:121/:136/:153`). The corrected CI-recovery ladder is:
  > **rung 1** = re-run the ORIGINAL push-triggered run (admin-only, with user handoff);
  > **rung 2** = a **non-zero-delta CONTENT retrigger** (a one-line content commit touching a path
  > inside `docker-ci.yml`'s `on.push.paths` filter ⇒ `**` minus `!.git/**` ⇒ any tracked file
  > outside `.git/` → `--no-ff` latest → `--no-ff` master), because a true **empty** commit has an
  > **empty changed-file set ⇒ `on.push.paths` fires NOTHING ⇒ no event, no build, no publish**
  > (`docker-ci.yml:4-5`). See `phase3-plan.md` § Ship runbook step 3 (rungs 1–2) and risk row R6.
- **Marker (measured):** `decision_all_members_failed` = **0 hits on BOTH the v193 (`b11d7c8`) and
  v194 (`fd1649c`) cached binaries** → recommended blocking marker; plus a dedicated
  `const GroupLaneMarker = "decision_groups_lane_v1"` as a rename-proof secondary. `skip` is a
  crowded namespace (68 hits v193) — a SKIP outcome literal must avoid the substring "skip"
  (e.g. `member_suppressed`); `model_fallback_*` is **occupied by v194**. `cache/latest/` is stale
  (revision 9cf17bb, no binary) — per-SHA tags only. Never `strings | grep -ci sticky`.
- **🔴 phase3-plan gate DEFECT:** its whole-file `git diff --exit-code` list includes
  `handle_models_imggen_test.go` — a file phase 1 **must** modify (adds
  `TestHandleModels_FilterDecisionGroup`, a strict-prefix of the existing
  `TestHandleModels_FilterDecision` — the anti-vacuous grep needs end-anchoring). Fix = **Tier-1
  (whole-file byte-identical) vs Tier-2 (per-function byte-identity via extract-and-cmp)** split;
  runnable recipe provided.
- **Gate-matrix corrections:** CI is HALF-gated (compiles `./cmd`, `npm ci` + `vite build`, image
  push — but no `go test`, no vitest, no tsc; `package.json` build = `vite build` only) ⇒ wording
  "CI green = compilation + FE bundle + image push". tsc runbook bug: `| tee` swallows the exit
  code — use `bash -c '…; echo ${PIPESTATUS[0]}'`. Hermetic-env citations wrong
  (`APPLY_ENV_OVERRIDES` actually set at `handler_anthropic_test.go:880` + 6 live-test sites).
  New gates G15-G18 (incl. `pkg/config` fence must be stated vs BASE=2b987bf — v194 itself added
  +28 there; `pkg/models/group.go` diff is EXPECTED and must be excluded from boundary lists).
- **E2E (object graph + traps):** throwaway credential (encrypted, Postgres on primary — NOT
  config.db) + throwaway decision model (excluded from `/v1/models` by kind filter) + **a NEW
  group that IS publicly listed for the E2E window** (kind=group not excluded — ties to
  contract-15 gap). No usage row on failure (success-only metering,
  `handler.go:550-555/:599-639`); cooldowns/layer-caches/sticky are in-memory only. Recommended:
  **delete the group at the end** (zero residual live-config change) — leaving it is a user
  decision. 🔴 false-negative trap: a token with non-empty `AllowedModels` yields **403
  `model_not_allowed`** (`handler.go:279-296`) — verify the token's allow-list before concluding
  failover failed. Add the plan's missing proof: `/v1/models` id-list diff before/after.
  **OQ-1 on primary — honest split:** a `.invalid` transport probe is NOT limit-class ⇒ cannot
  create all-marked; an all-throwaway group against a pod-reachable **429 stub** CAN (feasibility
  NOT VERIFIED: no SSRF validation found on model/credential write paths, but no verified stub
  endpoint either); the **mixed live+marked case must stay in the local harness** (would require
  marking a live credential).
- **Deploy facts corrected (GitOps read):** primary is **already gen 34 @ `fd1649c`**; daoduc is
  **helm rev 37** (`values-pinned.yaml:153-162`) — the plan's "rev 36→37 / gen 33→34" targets are
  stale by one release. **Rollback semantics correction:** `helm rollback 36` from rev 38 restores
  TO v193 = a **two-release** rollback that also reverts v194's cache-layer-separation. Correct
  ladder: tier-1 = `:fd1649c` (v194) / `helm rollback 37`; tier-2 = `:b11d7c8` (v193,
  `sha256:261622cf…`) / `helm rollback 36`; **never** v192. `dcs` backlog **resolved**
  (`latest...origin/latest` in sync @ 7f89eac) — a pin push carries no backlog, but remains a
  separate authorisation. Runbook additions: relative pre/post generation assert, running
  imageID digest assert, anonymous-hole re-verify on both hosts.
- **Open questions answered by me (see §6):** rollback tier-1 = v194; E2E group = delete by
  default; OQ-1 mixed case = local harness, primary E2E covers transport-advance + layer
  isolation (+ optional 429-stub all-throwaway probe only if the user authorizes it).

### W5 — layer binding + observable contract (resilience-design, amended to 2b987bf) — VERDICT: REUSE-AND-BIND; the decision lane adds NO new cache store

- **Layer map:** all three stores EXIST @`2b987bf` and are already identity-partitioned
  (grouplb `(groupID,memberID)` · modelcache model-row id · skipcache `(scope,id,model)`). Bind
  the **process-wide instances as-is** (`modelcache.New()` `cmd/main.go:249`, skipcache
  `:242`, `credLB := modelsMgr.Engine()` `:138`); **do NOT lane-tag keys or fork instances** —
  keying already gives zero cross-lane interference, and forking re-opens the v193 "two views"
  class. The only genuine cross-lane write is the **GROUP rotation row written by chat traffic
  naming a decision group** (`ExcludeAndReselect` `engine.go:747` → 60s row `:788`) — fixed by the
  **rewrite-boundary lane check**, not by key scoping.
- **🔴 Pick placement is load-bearing.** The upstream AST pin
  (`cache_layer_separation_test.go:752-784` @2b987bf, helper `parsedImportPaths` `:725-738`) bans
  `pkg/skipcache`+`pkg/modelcache` imports in **`pkg/store/database`** (also grouplb, modelscache);
  `pkg/decision` is NOT in scope. So a pick function placed **in `pkg/store/database`** (new file,
  e.g. `store_group_policy.go`) is **provably policy-only for free** — it *cannot* compile a
  cross-layer read. Placed only in `pkg/decision`, isolation reverts to a mirror file-level pin.
- **Isolation options:** capability-token REJECTED (Go seals implementers, not callers); typed
  keys already adopted upstream; the pin is the mechanism. Decision lane's obligations: pick in
  `pkg/store/database` (inherits pin) + attempt stage in `pkg/decision` (may import both caches) +
  a **file-level mirror pin** over the decision pick/walk files.
- **Policy-order enumeration:** engine yields ONE weighted head per pool; `RebindPast` is a no-op
  for empty conversation key (`engine.go:1216-1224`). Recommended: **engine head +
  configured-order tail** (members_json order minus head — the text sweep's Phase-3 completeness
  shape); REJECT lane-local weighted permutation (second policy authority = drift). Request-scoped
  tried-set; never call `RebindPast`. ⚠️ **W5's aside "pass a decision-scoped conversation key" is
  OVERRULED by D-6** (sticky = NO on this lane; empty key stores no binding — which also cannot
  pollute). D-6 stands.
- **credentiallb clear gap (shared, not decision-specific):** `pkg/credentiallb` @2b987bf gained
  only a reader (`CooldownUntil :468`); no public Clear. Text lane's `clearWinnerLayerState`
  (`handler_helpers.go:873-904`) also cannot clear it ⇒ a proven-healthy credential stays
  deprioritised up to 60s/15m. Decision win-clear targets: MODEL `modelcache.Clear` (`:124`) +
  UPSTREAM `skipcache.Clear` (`:135`) + GROUP `ClearCooldown` (`:1274`, via the existing
  optional-interface forward `handler_helpers.go:897-902`); winner identity only. Propose
  `Engine.ClearCooldown` to the credentiallb owner — do not block on it.
- **MODEL-layer writer is UNPROVEN:** `modelcache.ApplyWall` classifies by **message vocabulary**
  (reset banners, `reset.go:38`); whether decision upstreams emit such banners is NOT VERIFIED.
  Honest layer-map statement: MODEL layer = bound reader (suppression check) with **no proven
  decision-lane writer today** — record "no decision-lane writer; layer inert until one exists"
  rather than faking one. (`pkg/credclass` covers the credential side only.)
- **Three pinning tests** specified with mutations: (a) pick byte-identical under an upstream-mark
  storm (mirror `cache_layer_separation_test.go:690-720`); (b) model mark ⇒ pick unchanged + SKIP
  + zero upstream calls + next member attempted (mirror `race_executor.go:454-460`); (c)
  file-level import pin via `parsedImportPaths` over the decision pick files (bans
  skipcache/modelcache/credentiallb).
- **Observable contract:** v194's suppression is **invisible in telemetry** (synthetic 429 +
  `suppressedAttemptError`, `race_executor.go:27-41/:454-460`) and v194's new FE vocabulary is
  **chain-level only** (`model_fallback_*`, `types.ts:326-330`) — vacuous here (OQ-2). Decision
  lane adds a `decision_*` namespace: `decision_member_picked` (must NOT carry mark state),
  `decision_member_skipped` (layer:"model", reason, zero-cost), `decision_member_attempted`
  (credential_id, provider, policy_position); terminal `decision` outcome
  `decision_all_members_failed`. 🟡 events carry no **request id** today — small handler change
  needed for correlatability. Propose `model_attempt_skipped` upstream (better than v194's silent
  synthetic 429).

### §2c — Worker-conflict adjudication (mine): pick placement

W6 said `pkg/decision/group_policy.go` driving `GroupEngine()` (zero new manager surface); W5 says
the pick must live in `pkg/store/database` to inherit the AST pin. **Synthesis — both, correctly
split:** the **policy-order computation** lives in `pkg/store/database` (new file
`store_group_policy.go`, pure function of the group row + engine policy — inherits the package AST
pin, so policy-only is compiler-proven); `pkg/decision` receives it as an **injected closure wired
in `cmd/main.go` from the RAW manager** (`modelsMgr`), mirroring the existing
`SetModelResolver`/`SetCredentialResolver` closure pattern (`handler.go:740/:747`, wired
`:321-328`). Zero decorator surface (the closure is built at boot, never travels through
`CachedModelsConfig` — the W3 trap cannot fire), zero interface changes, zero edits to files v194
touched. The decision lane additionally carries W5's file-level mirror pin over its own walk files
as defense in depth.

### W8 — FE structure (structural-design) — VERDICT: no-fork feasible YES, with 4 corrections; guard = BOTH (server-authoritative)

- **No-fork holds:** `DecisionModelsTab` is genuinely byte-unchangeable (all-props interface, one
  module import, **no container→tab reach-in needed** — a group write mutates only `kind=group`
  rows so the models child's feed is semantically unaffected; `useGroupModels` invalidates its own
  cache key on mutation, `useApi.ts:674-678`). The three shared group components parameterize by a
  required `lane` prop with the text branch byte-identical. **Plan citation drift:** the
  `GroupModelsTab.tsx` cites are wrong (real: `:156/:165/:235/:304`, plan's were
  DecisionModelsTab's numbers); `w-40` has **0 occurrences** in the FE (the grep hit was the
  substring in "yello**w-40**0") — it is a third new utility; `shrink-0` cite points at
  `flex-shrink-0` in the wrong file.
- 🔴 **`GroupModelForm.tsx:263-265` promises "backup-only members serve as last-resort fallback in
  the race chain" — false on the decision lane (OQ-3: no race chain).** The plan's copy table
  omits it ⇒ a lane-visible false resilience promise ships. Move the no-pinning/no-chain semantics
  from the empty state into the always-visible form chrome.
- **Feed:** client-side split workable for v1, but the plan's "15 s TTL" staleness claim is
  **WRONG** — hooks refetch only on mount + own mutations (`useApi.ts:572-575` vs `:315-319`) ⇒
  list staleness is **unbounded until reload**; `unknown` members are the COMMON case (any
  operator whose page predates another operator's decision model). Server-side
  `?kind=group&lane=` is the better end-state and the lane primitive is being built anyway —
  schedule as v2. 5-axis: client wins Complexity/Cost; server wins Maintainability decisively
  (one rule in the BE; display matches the routing inference).
- **Lane-consistency guard: (c) BOTH — server-side lane-stability on UPDATE is the authoritative
  fence.** The plan's "the server cannot detect it" claim is **FALSE**: the PUT path already
  resolves every member's kind from the live set (`server.go:1412` → `:1428-1446`), so diffing
  old-vs-new lane is nearly free. Reject `lane(newMembers) ≠ lane(storedMembers)` at helper
  (`group.go:115-214`) + PUT mirror + parity case; escape hatch = delete + recreate (G2 forbids
  the empty intermediate). Two FE-guard defects to fix: the §3.2↔§5.2 contradiction (an `unknown`
  group would be visible in the text tab **and uneditable in both**), and the predicate conflating
  "foreign lane" with "unresolvable" → **three-way rule** (resolves-here → allow; resolves-elsewhere
  → block + route; unresolvable → allow + repair).
- **Nav/a11y verified:** class pair lifted correctly (`SettingsPage.tsx:388-391`); the
  nav-outside-selector trick is **doubly safe** (grandchild buttons + no `overflow-x-auto` on the
  container — assert BOTH in A4); WCAG 2.5.3 Label-in-Name satisfied; `aria-current="page"` would
  be more precise than the pinned `"true"` (E1 stands unless amended); `<nav>` is itself a new
  HTML landmark; zero vertical-nav/ARIA-tab precedent confirmed.
- **Vacuous tests:** B8/D7 assert the absence of a provider field that **never existed**
  (`grep provider` in the two group-form files → 0) — replace with the provider-badge case
  (`GroupMemberEditor.tsx:91/:114-121`), which fails if the wrong list feeds `laneModels`.
  Stage-5 suite count omits 3 test directories — "whole FE suite" is the gate. tsc 19/9
  discipline and the static-asset embed discipline verified **correct** (4 tracked paths under
  `pkg/ui/static/`; touched group files are NOT in the baseline ⇒ must produce 0 errors).

---

# FINAL PASS — reconciled to ADR-11/ADR-12 (the ruling; supersedes stale rows above)

## §3-F — Five corrections ordered by the leader (executed here)

1. **ALL-MARKED row RETRACTED.** My §1 row ("under R2 a fully-marked member set … every attempt
   is suppressed, the request fails pre-commit" — zero attempts) is **WRONG** per OQ-1/ADR-11:
   **suppression never removes the last attempt.** When every candidate is marked, the **POLICY
   HEAD receives exactly one honest attempt** (head suppression lifts for that request); the cache
   is never a failure oracle. My §2 (:177 region) and W5's formulation were already correct:
   `suppress(M) := marked(M) ∧ ¬(M = policy-head ∧ every-candidate-marked)`. The §1 row dies;
   the terminal envelope fires only if that one honest attempt also genuinely fails.
2. **E-6 row CLOSED.** No residual question: credential multiplicity **IS** part of the member's
   own resilience (A-1/AC-R4) — per-credential attempts within a member; the UPSTREAM layer's
   **reader** is per-credential selection, its **writer** is credential-failure classification
   (`ExcludeAndReselectForClass` `pkg/credentiallb/engine.go:637`; TTL policy `failure_class.go:119`).
3. **`ExcludeGroupMember` bar NARROWED, not blanket.** **LEGAL:** genuine-failure advance
   bookkeeping — tried-set tracking, cooldown writes **only after genuine failures**. Phase 1
   `:318/:355/:1150` is **compliant** and aligned. **BARRED:** expressing a mark/suppression with
   it, or using it as an ordering device (that is pick-level state, R1/R2). My §1 row's blanket
   "must not use it to express a mark" stands; the implicit "and not for advance bookkeeping"
   reading is corrected here.
4. **AM-4 ENCODED (exact text-lane mirror — two lanes, ONE answer):**
   | Upstream class | Decision-lane action | Layer state written |
   |---|---|---|
   | **5xx** | **ADVANCE to the next member** — no within-member rotation, no mark, **no cooldown write** | **NONE — 5xx writes no layer state on any layer** |
   | **Limit-class (429/402 family)** + **upstream 401/403** (rotation-eligible) | **rotate WITHIN the member** (next credential, AC-R4) | UPSTREAM mark (`skipcache`) + credentiallb cooldown row |
   | **400/413/415/422 + client-cancel** (advance-blocking) | **fail-fast** (no further attempts) | none |
5. **Marker row ALIGNED.** The marker **stays `decision_all_members_failed`** (new Go string
   introduced by this feature). Requirement = distinctive-vs-v193 **with measured controls**:
   absent from the live v193 binary (`b11d7c8` — measured **0** via the `verify_image.sh` per-SHA
   cache), absent from v194 (`fd1649c` — measured **0**), **present in the new build**. Optional
   secondary: a dedicated `const decision_groups_lane_v1`. Never `strings | grep -ci sticky`.

**Planner-corpus facts attested by the leader (not re-verified by me):** decisions.md is now
830 lines / 12 ADRs; phase1 1523 (Amendment 1 §A1–A20, 50-name test table; ADR-1 being folded in);
phase2 798 (candidate-eligibility in ONE place, `laneModels` untouched); phase3 964 (G4 = 50 Go +
48 FE; import-guard test runs early as P0.9). Where this review cites phase-plan line numbers, they
were verified against the pre-consolidation corpus — treat minor drift post-consolidation as
expected; the *findings* are line-independent.

---

## §4 — Trade-off matrices (the approver's independent set)

### 4.1 Option A (widen) vs Option B (parallel decision-group path)

| Axis | A: WIDEN | B: PARALLEL | Edge |
|---|---|---|---|
| Complexity | Low-Med — ~42 sites, mostly one-line kind-arms inside existing code; one entity, one migration story (none) | High — ~18-20 NEW mirror arms + a new kind constant + 1 migration + a re-implemented copy invariant + a second member list | **A** |
| Scalability | Same engine, same stores; per-request lane inference ≈ 2N+1 raw SQL on the group path (N≤16) | Same engine reusable (W1: B does NOT pay engine duplication — planner's rationale #1 is wrong) | tie |
| Maintainability | One rule set + parity harness (which must gain the missing cases, §W9) | Two group entities to keep in sync forever; the inline-mirror duplication class that already produced the repo's highest-severity review finding (`ui/server.go:780-785`) | **A** |
| Risk | Med — renegotiates G3 (chat-only member invariant) and inherits 4 non-group gaps (G1-G4, §2a); flip assumption: expected-kind parameterization must hold on BOTH primary and fallback paths | Med-High — re-earning v193+v194 correctness (ordering-not-filtering, win-clear identity, layer separation) on a fresh path | **A** |
| Cost | ~0 new infrastructure | ≈ **+1,700 lines** (169-line order-sensitive sweep + 234-line UI mirrors + ~1,004-line FE set + arms) | **A** |

**Recommendation: A — CONFIRMED, with the rationale corrected.** The planner's headline reason
("B would duplicate ~1,300 lines of kind-agnostic machinery") is **refuted** — `pkg/grouplb` is
reusable wholesale under either option. A wins on the *other* grounds: no migration, no second
entity, no mirror-arm explosion, and the v193/v194 correctness properties are inherited, not
re-earned. The approver should approve A **for these reasons**, not the planner's.
**Flip assumption:** if expected-kind parameterization cannot be guaranteed on both the primary
and fallback-chain paths, A collapses to B.

### 4.2 Lane discriminator: inferred (D-2) vs persisted `lane` column vs hybrid

| Axis | Inferred (D-2) | Persisted + backfill | Hybrid |
|---|---|---|---|
| Complexity | **Low** (fold helper + parameterized predicate + 1 forward) | High (migration 033 + 12 query strings — sqlc regen NOT needed, queries are hand-written — + DTO + FE + backfill) | Highest |
| Scalability | Med (2N+1 SQL per group request) | Med (column read; N member lookups remain) | Low-Med |
| Maintainability | Med (lane invisible in the row) | High (SELECT-able) — but adds a column-vs-members drift class | Low (two truths) |
| Risk | Med — silent re-lane via **direct-SQL** retype (API path closed by ADR-1) | Med — migration-registry trap + backfill ambiguity + drift | High (non-atomic dual write) |
| Cost | **Low** | High | High |

**Recommendation: INFERRED — with two mandatory amendments:** (1) the lane check lives at the
**rewrite boundary** (a decision-lane group entry reached from a chat lane ⇒ `FailedGroupID`) —
closes the fallback-chain bypass and is orthogonal to lane representation; (2) **ADR-1 extended to
shadow targets** (retype rejection names members AND `shadow_model_id` references). Confidence
HIGH (W2). **A persisted column does NOT remove the request-time re-check** — members can still be
retyped/deleted; it buys routing stability under out-of-band SQL only. Flip trigger: direct DB
editing becomes a normal operator habit, or measured group-path latency regression.

### 4.3 Ordering seam: parameter vs separate path vs fork

| Axis | (a) Parameter on the sweep | (b) Separate path ✅ | (c) Fork |
|---|---|---|---|
| Text-lane safety | ✗ signature change breaks `models.GroupRewriter` + edits the file v194 just rewrote | ✅ zero shared-file edits for ordering | ✅ but duplicates policy semantics |
| Layer isolation (R1) | discipline | **compiler-proven** if the computation sits in `pkg/store/database` (§2c synthesis) | discipline |
| Decorator surface | new forward + trap | **none** (boot-time closure from the raw manager) | none |
| Collision (parallel commission) | ✗✗ | ✅ | ✅ |

**Recommendation: (b), refined by the §2c synthesis** — computation in a NEW
`pkg/store/database/store_group_policy.go` (inherits the v194 AST pin ⇒ provably policy-only),
handed to `pkg/decision` as an injected closure wired in `cmd/main.go` from the RAW manager.
**Policy-order walk = engine head + configured-order tail** (members_json order minus head);
request-scoped tried-set; `RebindPast` never called; conversation key stays **empty** (D-6).

### 4.4 FE feed: client-side lane split vs server-side `?kind=group&lane=`

Client-side wins v1 Complexity/Cost; server-side wins Maintainability decisively (one rule in the
BE; display matches routing inference). **Client-side for v1** — but the staleness model is
**unbounded hook-state staleness** (not "15 s"), `unknown` members are the common case, and the
failure mode must be "row in the wrong tab", never "row unreachable". Server-side feed = v2 (the
lane primitive is built anyway).

### 4.5 Lane-consistency guard: FE-only vs server vs both

**BOTH — server-authoritative.** The PUT path already resolves member kinds from the live set
(`server.go:1412→:1428-1446`); lane-stability-on-UPDATE is nearly free and closes the hole for
ALL writers. FE guard = UX routing only, with the **three-way rule** (resolves-here → allow;
resolves-elsewhere → block + route; unresolvable → allow + repair) — the plan's two-state guard
dead-ends `unknown` groups.

### 4.6 Layer caches: bind vs lane-scope vs new stores

**Bind the process-wide instances as-is.** All three stores are identity-partitioned; no
lane-tagging, no forked instances (the v193 "two views" class), no new stores. The MODEL layer is
bound as a reader with **no proven decision-lane writer today** — recorded as "no equivalent
writer; inert until one exists", never faked.

---

## §5 — Risk register (consolidated, deduped)

**🔴 Critical (blocks ship if unresolved)**

| # | Risk | Evidence | Mitigation (owner) |
|---|---|---|---|
| R1 | **Fallback-chain bypass of the lane guards** — a chat model whose chain names a decision group reaches the chat executor with zero per-attempt kind guard; two workers independently | `handler_functions.go:200-202`, `handler_anthropic.go:290-292`, `store_group_rewrite.go:52`, guards check only `rc.resolvedModel` (`handler.go:576`, `:619` region) | Rewrite-boundary lane check + write-time fallback-target lane rule (phase 1) |
| R2 | **Pick placed outside `pkg/store/database`** loses the v194 AST pin ⇒ layer isolation reverts to discipline | `cache_layer_separation_test.go:752-784` | Place computation in `store_group_policy.go`; decision-lane mirror file pin as defense |
| R3 | **Lane gate fails OPEN on 3 of 4 consumers** if the `GroupLane` forward is missing (decision groups served on chat lanes + listed in `/v1/models`) | decorator wraps-not-embeds `modelscache/models.go:104-105`; plan's `groupLaneOf` fallback "not excluded" | Compile-time `var _` assertion + decorator-through **effect** test + pin wiring to the decorator, never `modelsMgr` |
| R4 | **ADR-1 (+ shadow extension) unscheduled** ⇒ single-member silent re-lane stands | `decisions.md:428` vs old `phase1-plan.md:706-713` | Being folded into phase 1 (leader-attested) — verify it lands with the shadow-target clause and the store-side guard shape (fresh `scanModels` + old-kind read) |
| R5 | **Rebase `24c51ef → 2b987bf` skipped or botched** — `handler_anthropic.go` (+230) and FE `EventLog.tsx`/`types.ts` are HIGH-conflict; every pin anchor moved | §2b collision table | Rebase FIRST, re-anchor all pins, re-gate vs BASE=2b987bf |
| R6 | **phase3-plan gate self-contradiction** (whole-file byte-identity list contains files phase 1 must edit) | §W10 Tier-1/Tier-2 fix | Adopt the Tier split + per-function extract-and-cmp |

**🟡 Significant**

R7 contract 15 unmet today (`/v1/models` lists decision groups — `handler.go:349-352` excludes
only kind=decision *models*). R8 peak-hour substitution never fires for decision members
(`race_executor.go:257`, `internal_handler.go:303`). R9 no server-side decision-group feed
(`server.go:786-789` lane-blind). R10 FE "race chain" copy false on this lane
(`GroupModelForm.tsx:263-265`). R11 FE staleness unbounded; `unknown` = common. R12 **MODEL-layer
writer unproven** (`modelcache.ApplyWall` keys on message vocabulary — decision upstreams may
never emit it; layer inert). R13 `pkg/credentiallb` has **no public cooldown clear** (shared gap
with the text lane; propose `Engine.ClearCooldown`). R14 cross-lane GROUP rotation row from chat
traffic naming a decision group (fixed by R1's boundary check, not key scoping). R15 decision
events carry no request id. R16 CI half-gates (compiles + FE bundle only); tsc runbook PIPESTATUS
bug; token `AllowedModels` 403 false-negative trap in E2E. R17 rollback semantics: `helm
rollback 36` = two-release rollback; tier-1 must be `:fd1649c`. R18 homogeneity rule ORDER makes
member-kind messages sibling-dependent; parity fixtures missing (`member-dec`, lane-aware base,
mixed-kind cases; `marshalGroupModel` drops `backup_only`).

**🟢 Minor** — stale docs (`config.go:148-156`, `handler.go:329-332`, `credential.go:51-53`);
`w-40`/cite drift in phase2; vacuous B8/D7 FE tests (replace with provider-badge case);
anti-vacuous grep needs end-anchoring (`TestHandleModels_FilterDecision` prefix collision);
`pkg/models/group.go` diff is EXPECTED (exclude from boundary lists); `dcs` backlog resolved.

---

## §6 — Decisions pending (leader/approver) + standing disagreements

**Decisions pending (4):**

1. **Rollback tier-1 target** — I recommend `:fd1649c` (v194) / `helm rollback 37`, keeping
   `:b11d7c8` (v193) as tier-2. This **contradicts the original brief's** "roll back to the v193
   digest": from the post-ship revision, v193 is a two-release rollback that also reverts the
   shipped cache-layer-separation. Needs explicit ack because it changes a ship-gate fact.
2. **E2E group fate** — delete the throwaway group at the end (my default: zero residual
   live-config change, and it unpublishes the `/v1/models` leak) vs leave it as the future real
   group. User decision.
3. **OQ-1 proof on primary** — default: local harness covers the mixed live+marked case; primary
   E2E proves transport-advance + layer isolation only. Optional (only if the user authorizes):
   an all-throwaway group against a pod-reachable 429 stub (feasibility unverified).
4. **ADR-1 rejection status** — 409 Conflict (Am27 idiom, my recommendation) vs 400 (ADR-1 text
   says only "4xx").

**Standing disagreements (held loudly, not silently complied):**

1. **The planner's D-1 rationale #1 is wrong even though A is right** — approving A *because B
   would duplicate the engine* bakes a false model into the record. A wins on migration/entity/
   mirror/inheritance grounds (§4.1).
2. **The "42-site" blast radius undercounts** — four material non-group gaps (G1-G4) sit outside
   it; the consolidated corpus should carry them as sites 43-46.
3. **phase2's "the server cannot detect a lane flip" claim is false** and should be retracted in
   writing if the server-side lane-stability rule is not adopted (§4.5).
4. **phase3's gate matrix is self-contradictory** as written (R6) — the Tier-1/Tier-2 split is not
   optional polish; the gate fails by construction otherwise.
5. **The MODEL layer may be inert on this lane** (R12) — the corpus should say so explicitly
   rather than imply three live layers.

## §7 — Gaps (fan-in honesty)

- **W7 (order-of-operations + R4 validation audit) did not report before the leader's final-pass
  order.** Its scope was substantially covered by other evidence: the loop shape and member-walk
  (W5 §2 answers, §2c), the R4/write-time audit (W9's parity table — G3 checks existence+kind,
  not enabled; fallback-target validation is existence-only per W1, so nothing found REJECTS
  chain-only models), and credential multiplicity (W5 §1′ + leader's A-1/AC-R4 closure). Still
  **not independently verified in this review:** the exact D5 line citation forbidding
  `fallback_chain` for decision models (`pkg/models/config.go:302-318` region) and a definitive
  multiplicity-representability table (admin API/FE single-vs-multi credential picker). The
  approver should spot-check those two items; if either surprises, the affected §4 rows move, not
  the recommendation.
- All workers were read-only; nothing was built or run. Every "measured" figure (marker hits, CI
  timings, image digests) came from the verify-cache/Actions-API reads cited in §2a/§2b.

---

# §8 — FINAL RE-ANCHOR vs BASE `2b987bf` (supersedes §2c's seam proposal; the question is now REUSE vs BUILD)

Verified by me at `2b987bf`: the AST pin is real and package-level
(`pkg/proxy/cache_layer_separation_test.go:747-784`, hardened in `15709a1` "test: harden package
pin (AST) + pin D1 wiring / D5 tool-only / D6 all-suppressed"; the parser is comment-aware,
`:786+` `TestCacheLayer_Structural_ImportParserIsCommentAware`); `store_group_rewrite.go` imports
NEITHER cache package (one prose mention in the doc block only — the pin parses imports, not
text); **`isServableGroupMemberBase` is STILL chat-only at `2b987bf`** (`:299-308`: `case "",
models.KindChat: return true; default: return false`).

## 8.1 I-1 answered: the decision lane REUSES the sweep's ordering — two work items DELETED

At `2b987bf` the sweep is **policy-only** (demotion key = engine rotation cooldown only;
`memberSoonestSkipExpiry`/`memberWireModel` deleted; pin `store_group_skipcache_test.go:128
MarkNeverBenchesOrderingSeam`) and **cache-blind** (structurally unable to consult either cache —
the package-level pin forbids the import). Rotation cooldown is GROUP-layer policy bookkeeping
written **only after genuine failures** — exactly the use the leader's `ExcludeGroupMember`
ruling blesses. Therefore:

- **REUSE** `pickServableGroupMember` / `RewriteGroupEntries` as the decision lane's ordering
  source — **after** the one required edit: lane-parameterize `isServableGroupMemberBase` (still
  chat-only today) and let the manager compute the lane internally (unanimity fold over member
  rows via raw `GetModelNative`). **No signature change** ⇒ `models.GroupRewriter` unbroken, zero
  fakes touched, **zero new decorator surface for ordering** (the existing
  `RewriteGroupEntries` forward `pkg/modelscache/models.go:1043-1048` already rides; pkg/modelscache
  is UNCHANGED by v194). Chat-lane byte-identity: `inferGroupLane(chat-group) → chat` ⇒ the
  predicate's chat arm is byte-identical.
- **DELETED work items:** (1) "build a separate decision-lane ordering path" — and **my own §2c
  `store_group_policy.go` proposal is SUPERSEDED** by this finding (the pick need not move, be
  duplicated, or be re-homed; the AST pin already covers it where it sits); (2) "lane-gate away
  from the sweep's mark-driven sort" — **MOOT**: the mark-driven sort no longer exists upstream.
- **What still must be built around the reused ordering:** the **attempt walk** lives in
  `pkg/decision` (the sweep only ORDERS — suppression, per-credential rotation, tried-set, OQ-1
  head-honest-attempt, AM-4 classification are lane work); and the **rewrite-boundary lane check**
  (R1/R14: chat traffic naming a decision group must fail the entry, not write rotation rows).

## 8.2 REUSE-vs-BUILD table

| Decision-lane component | Ruling | Anchor @2b987bf |
|---|---|---|
| UPSTREAM layer store | **REUSE — SATISFIED-BY-v194** | `pkg/skipcache` re-scoped to UPSTREAM only; layer contract in package doc; instance `cmd/main.go:242` |
| MODEL layer store | **REUSE — SATISFIED-BY-v194** | `pkg/modelcache` NEW (Mark `:59`/Marked `:83`/Clear `:124`; `ApplyWall` `policy.go:31`; TTL `pkg/config/config.go:120-130`); instance `cmd/main.go:249` |
| GROUP layer policy | **REUSE** | `pkg/grouplb` (v194 zero-diff): `GetOrSelectWithRepair` `engine.go:408`, `PickBackupMemberWithRepair` `:880`, `ExcludeAndReselect` `:747`, `ClearCooldown` `:1274`, `MemberCooldownUntil` `:1293` |
| Ordering sweep | **REUSE after parameterization** (§8.1) | `store_group_rewrite.go` — cache-blind, policy-only, predicate still chat-only `:299-308` |
| Structural isolation | **REUSE — SATISFIED-BY-v194** | AST pin `cache_layer_separation_test.go:747-784`; decision lane adds only a FILE-level mirror pin over its own pick/walk files |
| Doctrine line | **REUSE — SATISFIED-BY-v194** | verbatim in the `pkg/skipcache` doc; decision lane pins it additionally at its suppression site (`pkg/decision` walk file) |
| Decorator forwards | **REUSE** | `modelscache/models.go:1043/:1056/:1079` (pkg/modelscache zero-diff in v194) |
| Credential engine | **REUSE** | `Engine()` `cmd/main.go:138`; `ExcludeAndReselectForClass` `credentiallb/engine.go:637`; NEW `CooldownUntil` `:468`; import-legal (closure walk, §2b) |
| Lane inference + parameterized predicate | **BUILD** (the ONLY `store_group_rewrite.go` edit) | internal `inferGroupLane` + kind-parameter; pin constraint: no skipcache/modelcache import in that file — trivially satisfied |
| `GroupLane` method + forward + effect test | **BUILD** — needed ONLY for the 3 guard consumers (chat/Anthropic misroute, `/v1/models`); NOT needed for ordering anymore | optional-interface shape; compile-time `var _` assertion (§2a W3) |
| Decision member loop (walk, suppression, tried-set, OQ-1, AM-4 via `pkg/credclass`) | **BUILD** | `pkg/decision/**` (v194 zero-diff — exclusively ours) |
| Per-credential rotation within member (AC-R4) | **BUILD** (binding) | reader `GetOrSelect`/`ExcludeAndReselectForClass`; writer = credential-failure classification |
| Exhaustion envelope + `decision_*` events (+ request id) | **BUILD** | `decision_all_members_failed`; FE `EventType` union (v194 added `model_fallback_*` `types.ts:326-330` — do not collide) |
| Validation widening + lane-stability + ADR-1(+shadow) + FE | **BUILD** | `pkg/models/group.go` (v194 zero-diff — ours), `pkg/ui/server.go` mirrors, parity cases, phase-2 FE |

## 8.3 LAYER MAP — reality rows (MODEL layer ruled explicitly)

- **GROUP** — reader: the sweep/engine (policy facts only). REUSE. Cross-lane rotation-row hazard
  (R14) fixed by the rewrite-boundary lane check, **not** key scoping.
- **MODEL** — **the decision lane KEEPS a bound reader but the layer is INERT on this lane today.**
  AM-4's class table writes **NO model-layer state** on any lane (5xx → advance, no state;
  limit-class + upstream 401/403 → UPSTREAM state only; 400/413/415/422 + client-cancel →
  fail-fast, no state). The only modelcache writer is `ApplyWall`, keyed on message vocabulary
  (`reset.go:38` region) — decision upstreams are **UNPROVEN** to emit it. **Ruling: keep the
  reader** (loop-contract parity — two lanes, ONE answer; the OQ-1 suppression-lift presupposes a
  suppression check), **document bound-but-inert**, pin it with test (b), and **invent no writer**.
  Alternative (omit the reader day one) is acceptable if the approver prefers zero unreachable
  code — my recommendation is KEEP, because re-adding it later silently changes the loop contract.
- **UPSTREAM** — reader: per-credential selection inside the member loop (AC-R4) + credential
  re-ordering via skipcache marks (`preferUnmarkedUpstreamCredential` precedent
  `race_executor.go:291-303`); writer: credential-failure classification (`pkg/credclass` →
  `ExcludeAndReselectForClass :637` + skipcache mark). REUSE both stores as-is; **5xx writes
  nothing on any layer** (AM-4).

## 8.4 Re-anchored pins, marker, E2E, collisions

- **Pinning tests:** (a) pick byte-identical under an upstream-mark storm — mirror
  `cache_layer_separation_test.go:690-720`; (b) model mark ⇒ SKIP + zero upstream calls + next
  member attempted — mirror `race_executor.go:454-460` (with the §8.3 inertness note); (c)
  **file-level** `parsedImportPaths` pin over `pkg/decision`'s pick/walk files: the ORDERING-consuming
  walk file may import modelcache/skipcache (attempt stage) but the pin bans them + `pkg/credentiallb`
  from any file that computes the PICK. Text-lane proof = do-not-regress the v194 pins
  (`store_group_skipcache_test.go:59/:85/:106/:128/:147`; `win_clear_winner_identity_test.go:120/:217`).
- **Marker:** unchanged — `decision_all_members_failed` (0 hits measured on `b11d7c8` AND
  `fd1649c`; present in the new build; optional secondary const `decision_groups_lane_v1`).
- **E2E:** unchanged from §2a/W10 (throwaway `.invalid` credential+model, throwaway group deleted
  by default, token-allow-list 403 trap, `/v1/models` id-list diff, OQ-1 mixed case local-only).
- **Remaining collision hotspots (REDUCED — the parallel commission landed):** `cmd/main.go`
  (adjacent hunks: v194 cache wiring `:242/:249/:255-256` vs our decision wiring `:320-330`
  region); `pkg/proxy/handler.go` guard region (~`:601`/`:619`) + `handler_anthropic.go` `:273`
  (v194 edited both files — medium/high rebase care); FE `EventLog.tsx`/`types.ts` (HIGH — our
  `decision_*` union additions sit where v194 added `model_fallback_*`); `store_group_rewrite.go`
  (v194 just rewrote it; our edit is one predicate — low-medium). **Exclusively ours (zero v194
  diff):** `pkg/decision/**`, `pkg/models/group.go`, `pkg/grouplb`, `pkg/modelscache`,
  `pkg/ui/server.go` group arms. Residual risk is rebase-conflict management, not semantic
  collision — and `modelsMgr.SetSkipCache(...)` is gone repo-wide; any plan text still referencing
  it is stale.

## 8.5 SATISFIED-BY-v194 (explicit, per the leader's instruction)

Structural isolation (AST pin, `15709a1`-hardened) · the doctrine line verbatim in
`pkg/skipcache`'s doc · policy-only ordering (mark-driven sort deleted; text-lane contract
inverted to `MarksDoNotChangeThePick`/`AllMarked_StillServes` — the text lane now ≡ OQ-1) · the
MODEL layer package existing · UPSTREAM-layer scoping of skipcache · `credentiallb.CooldownUntil`
accessor (`:468`). What v194 did NOT give this lane (still ours): the lane parameter on the
predicate, the member walk with suppression + per-credential rotation, AM-4 classification, the
lane guards, validation widening, FE.

## §7a — Gap CLOSED (W7 wrap-up report received after final pass)

1. **D5 citation (both bases, byte-identical):** `pkg/models/config.go:301-304` — `if
   len(m.FallbackChain) != 0 { return fmt.Errorf("model %s: kind %q does not support
   fallback_chain", m.ID, KindDecision) }` inside `validateDecisionRules`, reached via
   `ValidateKindRules` `case KindDecision` (`config.go:226-227`); enforced at DB
   `store.go:2063` + UI mirrors `ui/server.go:1180-1187` (POST) / `:1645-1652` (PUT); the FE
   never exposes a chain field for decisions (`DecisionModelsTab.tsx:131` stamps
   `fallback_chain: []`). `git diff 24c51ef 2b987bf -- pkg/models/config.go` = **empty**.
   **⇒ OQ-2's premise (D5 not lifted) is CONFIRMED with exact citation; the chain clause is
   vacuous by enforced rule, not by assumption.**
2. **Credential multiplicity: REPRESENTABLE today** — `Credentials []CredentialRef` cap 16
   (`config.go:357`, `MaxCredentialRefs` `:84`); decision validation requires `>=1` with **no
   cap of 1** (`:284-286`); admin API accepts (`server.go:1161`/`:1628`); FE ships a
   `MultiCredentialEditor` (`DecisionModelForm.tsx:468`, `MAX_CREDENTIALS=16`
   `MultiCredentialEditor.tsx:11`); and resolution for `len>=2` **already routes through
   `credentiallb.GetOrSelect`** (`store.go:2614-2619` @24c51ef / `:2605-2610` @2b987bf; cached
   twin `:2746`/`:2737`), pool keyed per model **ROW ID** (`RebindFromStore(mc.ID, …)`
   `store.go:789`). The gap is **consumption, not representation**: the lane resolves one
   credential and makes one upstream call (`decision/handler.go:299-310` + `:430-433`).
   **⇒ AC-R4 is necessary-and-real, not speculative** — an operator with 2 credentials on a
   decision model today gets only one attempted; the per-credential member loop is the fix, and
   the substrate (pool, weighted pick, `ExcludeAndReselectForClass`) already exists.
   *Not verified:* live credential counts on the user's two decision models.
3. **Anchor correction for earlier sections:** `ExcludeAndReselectForClass` sits at
   `credentiallb/engine.go:657` @2b987bf (`:637` was the 24c51ef anchor used in §2b/§8) —
   re-anchor post-rebase.
