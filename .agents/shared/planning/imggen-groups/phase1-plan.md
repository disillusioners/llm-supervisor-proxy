# Phase 1 — imggen-groups: BACKEND GROUP MACHINERY

> **Commission:** imggen groups (Emperor, verbatim intent: *"group image gen model
> (FE 2 sub tab like decision before)"*). Backend half: image-gen model GROUPS
> mirroring the decision-groups pattern (v200) + credential groups (v199),
> composed with the v203 token-authz layer.
> **Phase 1 scope:** the lane-vocabulary widening, the write path, the
> `/v1/image_generation` group serving branch (member loop, classification,
> timeout budget, metering), the `/v1/models` exclusion, the sweep/shape-predicate
> arms, and the misroute/kind gates. **The v203 authz composition + its pins are
> Phase 2** (`.agents/shared/planning/imggen-groups/phase2-plan.md`) — but the
> P1 serving branch must land with the ordering structure P2 composes onto (see
> §7 Cross-phase couplings, the P1→P2 interface).
> **Worktree:** `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-imggen-groups`
> (branch `feature/imggen-groups-text-subtabs`, base `origin/latest` @
> `5cf1ecdce73353a23bee30f7c33a8ba69994af74`). Every line-pin below was read at
> `5cf1ecd` in THIS worktree on 2026-11-20. Known ±1 drift vs the explorer
> digest was re-anchored: `GroupMember` is `pkg/models/group.go:44-48` (not
> :38-47); `MaxGroupMembers` `:22-25`; decision wiring `cmd/main.go:400-410`;
> the credential-group kind gate is `pkg/models/credential_group.go:88-101`
> (the digest's `pkg/proxy/credential_groups_test.go:596-601` file does NOT
> exist in this tree — the R-5 pin is `pkg/models/credential_group_kind_test.go:23-38`).
> **Binding rulings carried:** the fallback-engine LAW (a mark suppresses an
> ATTEMPT, never a PICK or a CHAIN — `pkg/store/database/store_group_rewrite.go:220-266`,
> post-ADR-5 supersession wording), ADR-19 (server lane fold + FE twin in the
> SAME commit — `pkg/models/group_lane.go:3-8`, `store_group_rewrite.go:52-58`),
> Ruling 5 (membership reads from the in-memory snapshot, never a live DB read).
> **Status:** Draft plan — no code, no tests, no config edits, no commits, no
> pushes, no deploys were performed to produce it.

---

## 1. Objective

A group whose members are **image-gen-kind** models becomes a first-class
**image-gen group**: it saves through the existing admin model API
(POST/PUT `/fe/api/models`), it is served by `POST /v1/image_generation`, and
it fails over across its members with the v200 decision-group semantics —
exhaustive pre-commit fallback (a request may only fail after every
base-servable member across both pools had its honest attempt), per-member
credential rotation consumed before the walk advances, a bounded per-member
deadline inside a bounded total walk budget, win-clear on the winner's
identity, and a distinct exhaustion envelope `502 + code=imggen_all_members_failed`.
The chat and decision lanes' behaviour is unchanged (byte-level pins), the
misroute answer becomes token-independent (a chat/decision group posted to
`/v1/image_generation` is a clean `400 image_misroute`, never a 403/502), and
image-gen groups are absent from `/v1/models`.

## 2. Scope

### 2.1 In scope (Phase 1)

| # | Item | Precedent / ruling |
|---|------|--------------------|
| S1 | Lane vocabulary widening — the S0 set: `GroupLaneImageGen`, the `MemberLane` fold, `GroupLanesOf`/`UnanimousGroupLane`/`DeriveGroupLane` arms, `MixedGroupMembersError` text widening, `ValidateFallbackChainLanes` imggen analog, write validator + admin-API mirrors, `isServableGroupMemberBase` arm, rewrite-boundary refusal (both directions), FE fold twin (ADR-19, minimal diff) | decision-groups S0; ADR-19 |
| S2 | `/v1/image_generation` kind/lane gate + the group serving branch: lane gate (fail-closed), in-lane policy (pools+weights+configured order), member loop with per-member credential rotation, failure classification (imggen's own `InspectResponse` seam), per-member deadline + walk budget, exhaustion envelope, winner relay + metering | `pkg/decision/group.go:97-444` shape-mirror, in-package |
| S3 | `/v1/models` catalog exclusion for image-gen groups + **C-2: chat-side image-gen-lane misroute arms on BOTH chat routes** (`image_gen_misroute` string; no smoke relabeled to 502) | `pkg/proxy/handler.go:420-435`, `:676-730`; `handler_anthropic.go:271-325` |
| S4 | Timeout budget knobs + config heat ruling (ruling F-1) | config.go clamps |
| S5 | Metering stance for group winners + failed walks (ruling F-2) | Amendment 14 continuity |
| S6 | Win-clear/demotion wiring depth (ruling F-3: v1 = per-request rotation, NO cross-request health writes), sticky-binding N/A ruling (F-4), exhaustion envelope ruling (F-6) | fallback-engine law |
| S7 | P1 test battery: write-path pins, flip `group_rules_test.go:555-556`, parity pins, shape-predicate pins, loop/exhaustion/misroute pins, decorator-through lane test extension, import-guard keeps green | §8 Test strategy |

### 2.2 Out of scope (explicit fence — do not touch in Phase 1)

1. **The v203 authz composition arms + their pins** — group-id-in-reach
   (ARM#1), member scope-filter-on-copy (ARM#2), the 403-vs-502 discriminator
   (ARM#3), tests T1–T8, `authz_guard_test.go` extensions. **Phase 2 owns
   these.** P1 lands the entry-gate scope behavior for plain models unchanged
   (`pkg/imggen/handler.go:305-316`) and builds the group branch with the
   scope seam marked for P2 (§7.3). The two phases are review units on ONE
   branch — neither ships alone; P4 ships both.
2. **No schema change, no migration.** `kind TEXT` is unconstrained;
   `members_json` + `shadow_model_id` already exist; the lane is INFERRED from
   members, never persisted (`pkg/decision/group.go:5-7`;
   `pkg/store/database/store_group_rewrite.go:540-571`). Migration count stays
   at 034 — **the reconciliation gate (88 SQL/91 grep) must show NO delta.**
3. **No new provider.** `validateImageGenRules` already gates
   `internal=true` (`pkg/models/config.go:241-243`), `internal_model`
   (`:244-246`), ≥1 credential (`:247-249`), primary provider
   `minimax|9router` (`:250-256`), rejects `fallback_chain` (`:257-259`) /
   `secondary_upstream_model` (`:260-262`) / `peak_hour` (`:263-265`) / stream
   deadline (`:266-268`). The decision-provider-vocabulary atomicity set (FE
   constant + `models/config.go` gate + `pkg/ui/server.go` ×2 + registry —
   POK `decision-provider-vocabulary`) triggers **ONLY if a new provider NAME
   ships. It does not.** Do not touch the credential vocabulary
   (`pkg/models/credential.go:15` `validProviders`) — it is a separate map.
4. **Credential-group refs on image-gen rows stay FORBIDDEN.** Verified, not
   changed: `ValidateCredentialRefs` R-5 gate
   (`pkg/models/credential_group.go:96-101`): `nonChat := k == KindDecision ||
   k == KindImageGen || k == KindGroup` — an image-gen member row may not
   carry `CredentialRef{GroupID}`; pinned by
   `pkg/models/credential_group_kind_test.go:23-38`. The digest's "check the
   same gate" instruction is ANSWERED by the existing gate; nothing to add.
5. **No grouplb engine changes, no sticky/binding/affinity on this lane**
   (ruling F-4): no conversation key exists (`pkg/imggen/handler.go` passes
   the stable key `(modelID, modelID)` at `cmd/main.go:343-349`); each group
   request is a FRESH weighted pick. `RebindPast`/`RebindBackupPast` are never
   wired here. The `pkg/grouplb` engine is kind-blind (`engine.go:12-26`) and
   stays untouched.
6. **No cross-request health writes in v1** (ruling F-3): no marks, no
   skip-cache writes, no engine cooldown writes, no `SetCredentialEngine`
   wiring. See §6.3 for the argument and the v2 follow-up flag.
7. **FE beyond the ADR-19 fold twin** — the 2-sub-tab UI, member pickers,
   toasts, tsc-baseline consequences are Phase 3 (parallel worker). P1 lands
   ONLY the minimal fold-twin diff (§7.2 contract).
8. **The pre-existing v204 `sendHeartbeat` nil-pointer panic** (heartbeat.go,
   SSE flush post-disconnect) is a separate commission — do not fix here.

## 3. Ruling conformance (positions this plan takes)

The commission asks each ruling as **tasks + tests + a decision entry** (P4's
`decisions.md` formalizes them; each item below names its decision key).

### 3.1 F-1 — Per-member timeout budget (decision key: `imggen-groups/walk-budget`)

**Position: per-member deadline + a total walk budget, both hot.**

- **Per-member deadline** = the existing `ImageGenTimeout` read per request
  (`pkg/imggen/handler.go:128-137` → `configMgr.GetImageGenTimeout()`,
  `pkg/config/config.go:159-167`, default 120s, clamp `[30s, 4min]`
  `config.go:336-346`). Live headroom holds: p50 17.68s / max 21.42s
  (POK `9router-imggen-latency-jpeg`) ⇒ 5.6× at the 120s default.
- **Total walk budget** = ONE new knob `image_gen_group_walk_timeout`
  (Duration, JSON `image_gen_group_walk_timeout`), **default 240s**
  (= 2 × the per-member default), clamp `[60s, 10min]`. Mechanics:
  - The budget is checked **before each member attempt**; a member attempt's
    ctx = `min(remaining budget, ImageGenTimeout)` (per-member clamp still
    honored — a budget of 300s does not stretch one attempt past 120s).
  - Budget exhausted mid-walk ⇒ the walk stops and writes the **exhaustion
    envelope** with `outcome=all_members_failed` and an accounting suffix
    naming the cause (`budget_exhausted=true` in the terminal event — the
    body stays the §3.6 envelope; the class rides the event, not a new body
    code).
  - **Config heat: HOT** (ConfigSnapshot-read, applied by
    `PUT /fe/api/config` without restart) — same getter pattern as
    `GetImageGenTimeout` (`pkg/config/config.go:207` interface method;
    `cmd/main.go` configMgr). No boot-owned value is introduced.
- **W-8 (leader ruling) — quota-walk short-circuit (INNER bound; the walk
  budget above stays the OUTER bound):** **N=3 consecutive quota-class
  failures** during the walk ⇒ STOP + the exhaustion envelope **carrying
  the quota class** (`last_class=upstream_quota_exceeded` + a
  `short_circuited=true` event key; the BODY stays the §3.6 machine code).
  Detection stays SIMPLE: a consecutive counter over quota-class outcomes
  ONLY (any non-quota outcome or a success RESETS it) — **no
  account-fingerprinting** (no alias-set correlation, no provider-set
  heuristics). Rationale: the 9router account-wide quota wall fails every
  member identically in ~15-17s each; the 3rd consecutive quota failure is
  sufficient evidence the wall is account-level, and the remaining members'
  "honest attempts" would burn budget re-proving it. This ruling
  SUPERSEDES the blanket "every member its honest attempt" wording for the
  quota class (§3.6/R4 are amended accordingly); every OTHER class keeps
  the exhaustive walk.
- **Argument.** The naive mirror (decision's accepted worst case) would be
  16 × 120s = 32 min of client connection. Ingress (cloudflared/nginx) would
  kill the connection first, producing an unclassifiable client-side timeout
  and no honest envelope. A bounded walk makes failure honest at a cost the
  operator tunes. A quota-429 member burns ~15-17s (POK
  `9router-imggen-quota-wall`), so a 240s default covers a full 16-member
  quota wall walk (~16 × 17s ≈ 272s… marginal) — note in the decision entry
  that operators with many 9router members may raise the knob; the DEFAULT
  favors bounding the connection over completing pathological walks.
- **Alternative rejected:** per-member × N arithmetic (no knob): hard-codes
  the 32-min worst case; rejected for the ingress-timeout argument above.

### 3.2 F-2 — Metering stance for group walks (decision key: `imggen-groups/metering`)

**Position: mirror the decision winner-metering; keep Amendment 14's
bill-on-error continuity on the GROUP row for failed walks.**

- **Winner (success):** meter the **WINNER MEMBER's row** —
  `request_count=1`, `image_count=provider.BillableImages(winner body)`
  (`pkg/imggen/minimax.go:196-228`; the parser tolerates the JSON-STRING
  `metadata.success_count`). The group row gets NOTHING. This mirrors
  `commitDecisionWinner` → `meterOnSuccess(model_id = winner MEMBER id)`
  (`pkg/decision/group.go:424-428`) — the member row IS the metered row; FE
  usage charts attribute images to the model that rendered them.
- **Failed walk (exhaustion OR fail-fast):** meter the **GROUP row** —
  `request_count=1`, `image_count=0` (the `meterFailure` shape,
  `pkg/imggen/handler.go:704-707`). Per-member failure metering is REJECTED:
  it would multiply request_count across members that produced nothing.
  A decision-style "no usage row at all" is REJECTED: the imggen lane's
  existing contract bills on error (Amendment 14, `handler.go:168-171` flow
  step 10) — dropping the row on groups would silently under-count imggen
  requests in FE usage charts (contract continuity beats cross-lane
  symmetry).
- Both meters run on the DETACHED context discipline (`handler.go:664-669`).
- **Tests:** winner meters member row only (group row untouched); exhaustion
  meters group row once with image_count=0; no per-member failure rows.

### 3.3 F-3 — Win-clear + demotion wiring depth (decision key: `imggen-groups/health-depth`)

**Position: (b)-lite — per-request rotation only; ZERO cross-request health
writes in v1.**

- **In:** the per-member credential rotation loop (the `triedPairs` +
  resolve-inner-loop shape, `pkg/decision/group.go:235-258`) — pure
  in-request state; consumes the member's own resilience before advancing;
  PLUS the W-8 quota-walk short-circuit (§3.1 — 3 consecutive quota-class
  failures stop the walk; in-request counter, still ZERO cross-request
  state).
- **Out (v1):** no `SetCredentialEngine` / `SetUpstreamSkipCache` /
  `SetModelSkipCache` wiring (contrast `cmd/main.go:400-410` for decision);
  no mark writes; no group-owned rotation cooldown writes.
- **Argument.** (1) The dominant imggen failure is the 9router ACCOUNT-WIDE
  quota wall (POK `9router-imggen-quota-wall`: all `ag/*` aliases share one
  backend) — a cross-request mark on one member adds no information the next
  request will not rediscover in ~15s, and marking ALL members approximates a
  lane-wide cooldown the walk budget already bounds. (2) The fallback-engine
  LAW: marks/cooldowns are ORDERING inputs, never candidacy filters
  (`store_group_rewrite.go:225-231`) — the imggen walk uses a fresh policy
  order per request (no sticky binding, F-4), so there is no demoted-execute
  sweep for a mark to order; writing marks that nothing reads is cost without
  effect. (3) F-8 exposure: quota-cooldown mint sites are the exact surface
  the v202 quota-mint-scope fix constrained (POK
  `quota-mint-scope-f8-exposure-a2-budget`); adding write sites on a NEW lane
  re-opens that audit surface for zero v1 benefit. **F-8 exposure stays
  NONE: the imggen lane performs no cooldown writes (today, and in v1).**
- **Consequence:** the store-side sweep's Phase-4 demoted ordering
  (`store_group_rewrite.go:254-257`) sees no live imggen cooldowns — members
  anchor at "now" — benign; the walk's own order is unaffected. The v2
  follow-up (full health-layer mirror) is recorded in P4's decisions.md as
  explicitly deferred, NOT designed here.

### 3.4 F-4 — Sticky binding: N/A (decision key: `imggen-groups/no-sticky`)

Stated as a ruling so nobody wires it later: **no conversation key, no
binding, no affinity map on the imggen group lane.** Each request is a fresh
weighted pick (the `buildGroupPolicy` analog's head pick,
`pkg/decision/policy.go:102-110` shape). The member's credential-layer key
stays the existing stable `(memberID, memberID)`
(`cmd/main.go:343-349`). Do NOT call `RebindPast`/`RebindBackupPast`/
`GetOrSelectWithRepair` from this lane.

### 3.5 F-5 — `/v1/models` exclusion (decision key: `imggen-groups/catalog`)

Widen the group-lane exclusion at `pkg/proxy/handler.go:420-429`: today only
decision-lane groups are collected into `decisionGroups` (`:426` lane ==
`GroupLaneDecision`); widen to **every non-chat group lane** (decision +
image-gen). Standalone image-gen rows are already excluded (`:432
m.IsImageGen()`). **Gate = EMPTY id-set diff**: with N image-gen groups
configured, the `/v1/models` id set equals the id set with those rows deleted
— the group id absent AND no other id appears/disappears (POK
`models-excludes-decision-groups-only`: the gate is the empty diff, not "no
group listed"). Backup-only members are image-gen rows → already absent.

### 3.6 F-6 — Exhaustion envelope (decision key: `imggen-groups/exhaustion`)

`502` + machine code **`imggen_all_members_failed`** via the OpenAI envelope
+ code field (the `openAIErrorWithCode` shape,
`pkg/decision/group.go:441-443` precedent; imggen uses
`models.NewOpenAIError` + a code — match the decision body shape: message
names the attempted count, code is the greppable marker). **No image usage
row on the winner path is N/A here; on the failed walk the usage row is the
group-row request_count only** (§3.2). **Exactly ONE terminal event**
(`outcome=all_members_failed`, `upstream_status=0`, `model=groupID`, the
walk's accounting counters) — per-attempt events are allowed and mirror the
decision per-attempt shape (`pkg/decision/group.go:301-308`). The envelope
names the class when the walk was cut by budget (§3.1) or by the W-8
quota short-circuit (`short_circuited=true`, `last_class=
upstream_quota_exceeded`) or ended in a uniform quota wall (the
account-wide case: the last failure's class rides the terminal event as
`last_class=upstream_quota_exceeded` — the BODY stays the stable machine
code; class detail is event-only, so clients never parse class out of the
error body). **W-8 supersession note:** the exhaustive-walk doctrine
("every base-servable member its honest attempt") holds for every failure
class EXCEPT quota — 3 consecutive quota-class failures terminate the walk
(§3.1); all other classes keep the exhaustive walk, and the walk budget
remains the outer bound over BOTH.

### 3.7 — `shadow_model_id` on the imggen lane (decision key: `imggen-groups/shadow-scope`)

Stated explicitly (leader ruling): on the imggen lane, `shadow_model_id`
carries **write-time lane-check + `/v1/models` listing side-effects ONLY —
no claims and no behavior implication.** The write-time half is §4.2's
shadow-target lane check (`pkg/models/group.go:242-252`: an image-gen group
claims an image-gen-kind row); the listing half is the shadow-hide rule
under §3.5's exclusion (a claimed standalone row vanishes from `/v1/models`
exactly as it does on the chat lane). The imggen group arm introduces NO
claim-first resolution behavior of its own: `resolveModel`
(`pkg/imggen/handler.go:271`) is unchanged, and P2's ARM#1 quotes the
client string precisely because claims stay group-only. Nobody may later
cite this commission as precedent for imggen-lane claim semantics.

### 3.8 — Corpus ledger (leader rulings W-4 + W-7 — P4's decisions.md carries these verbatim)

- **G3 `member_must_be_chat_kind` — MODIFIED, with justification.** The
  original load-balance-groups contract (a group member must be a
  chat-kind model — the wording survives in `pkg/models/group.go:187`
  "members must reference existing chat-kind models") is modified to: *a
  member must be a model of the GROUP'S LANE kind* (chat, decision, or
  image-gen — `MemberLane` decides, §4.1). Justification: the lane is
  derived from the members (`group_lane.go:12-19`), so the member-kind rule
  IS the lane rule; three lanes share one entity + one validator, and the
  fold (`UnanimousGroupLane`) makes the widened rule self-consistent at
  write time. The CHAT-lane wording of every error string stays
  byte-identical (the historical-default convention,
  `group.go:200-204`).
- **G8 (shadow-claim integrity + uniqueness) — UNCHANGED, stays GREEN.**
  The claim machinery is lane-agnostic (`group.go:237-260`); §3.7 records
  that no new claim semantics ship.
- **Test ledger (W-7):** the fold flip lands as **ADDED
  `TestMemberLane_ImageGen_IsImageGenLane`** (new dedicated test asserting
  `MemberLane(KindImageGen) == (GroupLaneImageGen, true)`) **AND MODIFIED
  `TestValidateGroupMembers_LegacyEmptyKind_IsChatLane`** (the existing
  test at `pkg/models/group_rules_test.go:545` — its `:555-556` pin
  `MemberLane(image-gen) must not be a valid lane` FLIPS IN PLACE to assert
  the new truth). **RENAMING the legacy test is FORBIDDEN** — its name
  carries its scope (the legacy-""-is-chat ruling); the flip is an
  assertion edit inside it, and the diff must show the file as modified,
  not renamed+recreated.

### 3.8 addendum — post-implementation ledger verification (2026-11-20)

The §3.8 ledger above is SETTLED normative text; this dated addendum records how it LANDED (no rewrite of the settled body).

- **W-7 test ledger — BOTH entries landed exactly as pinned.** **ADDED** `TestMemberLane_ImageGen_IsImageGenLane` (`pkg/models/group_rules_test.go:600`: `MemberLane(KindImageGen) == (GroupLaneImageGen, true)` + `UnanimousGroupLane([image-gen])=(image-gen,true)` + stable-order `GroupLanesOf` pin). **MODIFIED IN PLACE** `TestValidateGroupMembers_LegacyEmptyKind_IsChatLane` (`:555`, ORIGINAL name kept, rename forbidden): the test's pin now asserts `MemberLane("") == (GroupLaneChat, true)` and `MemberLane(KindChat) == (GroupLaneChat, true)` (the legacy-""-is-chat ruling survives; the former "image-gen must not be a valid lane" pin is superseded by the dedicated ADDED positive test). Git sees the file as modified, not renamed/recreated.
- **W-4 / G3 `member_must_be_chat_kind` — MODIFIED with justification in BOTH parity matrices.** `pkg/models/group_rules_test.go:175-179` and `pkg/ui/kind_rules_parity_test.go:586-594`: the case ID is kept verbatim; its fixture is now a **no-lane kind** (`Kind: "mystery-kind"`, so `MemberLane` returns `ok=false` — an unknown kind belongs to no lane), and it still asserts the historical chat-worded rejection `"must be a chat-kind model"`. Justification: the member-kind rule IS the lane rule (lane inferred from members), so the case now pins only the no-lane rejection; **image-gen member legality is pinned by the new positive cases** (the ADDED dedicated test above + the FE parity `TestKindRules_Parity_Group_ImageGenLane` at `pkg/ui/kind_rules_parity_test.go:1073`), not by renaming or repurposing the legacy case. G8 stays GREEN (lane-agnostic claim machinery).

## 4. Components (file-anchored)

### 4.1 `pkg/models/group_lane.go` — the vocabulary (S1)

| Anchor | Change |
|---|---|
| `:35-43` | Add `GroupLaneImageGen GroupLane = "image-gen"` to the const block (+ doc comment naming `/v1/image_generation` as its lane). |
| `:21-31` (doc) | Extend the lane-vocabulary comment block: `image-gen — members are image-gen-kind models`. |
| `:53-62` `MemberLane` | **LOAD-BEARING FOLD.** Add `case KindImageGen: return GroupLaneImageGen, true`. Today `image-gen` falls to `default: return "", false` — pinned by `pkg/models/group_rules_test.go:555-556` ("MemberLane(image-gen) must not be a valid lane") inside `TestValidateGroupMembers_LegacyEmptyKind_IsChatLane` (`:545`), which **deliberately FLIPS IN PLACE** (assert the new truth `(GroupLaneImageGen, true)` inside the legacy test — NO rename, per the §3.8/W-7 ledger); the dedicated `TestMemberLane_ImageGen_IsImageGenLane` is **ADDED separately**. Never delete silently. |
| `:72-94` `GroupLanesOf` | Add the image-gen arm; keep stable order **chat, decision, image-gen** (order is part of the contract — `:64-66` "stable order"). |
| `:103-109` `UnanimousGroupLane` | No code change — it folds over `GroupLanesOf`; the widening is automatic. Pin the mixed case `[chat, image-gen]` ⇒ `ok=false` in tests. |
| `:168-177` `DeriveGroupLane` | No code change (first-valid-member loop); the chat DEFAULT when no member carries a valid lane stays. |
| `:182-184` `MixedGroupMembersError` | Widen the text: `"...must all share one kind (chat, decision or image-gen) — ..."`. The text is shared by the canonical validator + POST/PUT mirrors (ONE vocabulary) — widening it here widens all three atomically. FE mirror copy is P3's (the FE does not carry this string server-side). |
| `:186-190` `InvalidMemberKindError` + `pkg/models/group.go:222-225` | **W-5 (leader ruling) — sweep BOTH hard-coded lane strings.** (a) `group.go:225` defensive-fallback string `"members must all share one kind (chat or decision)"` widens to `"(chat, decision or image-gen)"` — it is a SEPARATE literal from `MixedGroupMembersError` and must not be missed. (b) Article fix at `group_lane.go:189`: the template `"must be a %s-kind model"` renders `"a image-gen-kind"` when the lane word is `image-gen` — reword so every lane renders grammatically (e.g. `"must be a kind-%s model"` or move the article into the interpolated lane label). The same `a %s-kind` pattern sits in the two shadow strings `group.go:248`/`:251` — sweep them with the same fix so no lane-worded message carries the bug. |
| `:192-220` `ValidateFallbackChainLanes` | Add the imggen analog: a chain naming an **image-gen-lane** group is refused with the mirror-worded error (`"...must not reference the image-gen-lane group %q (an image-gen group is served by /v1/image_generation only)"`). Today the check is decision-only (`:213 lane != GroupLaneDecision → continue`). Generalize: refuse when the target group's lane ∈ {decision, image-gen}. |

### 4.2 `pkg/models/group.go` — the write validator (S1)

| Anchor | Change |
|---|---|
| `:196-235` | The lane fold needs NO structural change — `DeriveGroupLane`/`GroupLanesOf`/`MemberLane` widen underneath it. The per-member kind check (`:230-235`, `InvalidMemberKindError`) now ACCEPTS image-gen members (the error fires only for kinds in no lane — group, unknown). |
| `:237-252` | Shadow-target lane check (`:250-251`): automatic via `MemberLane` — an image-gen group claims an image-gen-kind row. No code change; add the pin. |
| `:44-48` `GroupMember` | No change (ModelID/Weight/BackupOnly already lane-agnostic). |
| `:22-25` | `MaxGroupMembers = 16` stays. |

### 4.3 `pkg/store/database/store_group_rewrite.go` — shape predicate + boundary (S1)

| Anchor | Change |
|---|---|
| `:526-538` `isServableGroupMemberBase` | **MANDATORY ARM** — add `case models.GroupLaneImageGen: return row.Kind == models.KindImageGen`. Without it the DEFAULT arm (`:533-537`, chat `""`/`chat` only) silently classifies every image-gen member as unservable. |
| `:126-132` | Rewrite-boundary lane refusal — the chat lane refuses decision-lane groups today. **Widen BOTH directions:** (a) chat lane refuses image-gen-lane groups identically (direct-named → `FailedGroupID`; fallback-position → dropped with loud log); (b) verify no NEW refusal is needed for image-gen groups entering DECISION re-entry paths (decision groups never appear in chat chains — same boundary; the imggen lane does not consume `RewriteGroupEntries` at all, §4.6). |
| `:220-266` | `pickServableGroupMember` four-phase sweep: NO code change (kind-blind through `isServableGroupMemberBase`). Do NOT introduce mark-influenced-sort language anywhere in new comments — the LAW is marks-suppress-attempts-never-picks (`:225-231`). |

**C-2 (leader ruling) — chat-side misroute arms, BOTH chat routes.** The
rewrite-boundary refusal above is the LAST line of defence; the decision
lane's dual guard also refuses at the ROUTE, BEFORE the group rewrite, so
the misroute is structurally impossible. The imggen mirror lands in BOTH
chat route families:

| Anchor | Change |
|---|---|
| `pkg/proxy/handler.go:712-730` (the group arm of the post-resolve misroute guard; standalone image-gen arm already EXISTS at `:676-682` with code `image_gen_misroute`) | ADD the image-gen-lane case to the switch: `case laneOK && lane == models.GroupLaneImageGen:` ⇒ `400`, log "image-gen group is served by /v1/image_generation only", code **`image_gen_misroute`** (the chat-family string — it already exists at `:680`; `image_misroute` WITHOUT `gen` stays exclusive to the imggen lane's own envelope, §4.6.2). |
| `pkg/proxy/handler_anthropic.go:296-325` (the group arm; NO image-gen standalone exists — the gap is DOCUMENTED at `:271-273` as "that is a separate commission": this commission IS it) | ADD (a) the standalone image-gen post-resolve guard mirroring `:274-286` (Anthropic envelope family `models.NewAnthropicError`, code `image_gen_misroute` — the cross-route canonical string, same pattern as `decision_misroute` at `:267-269`), and (b) the `GroupLaneImageGen` case in the group switch at `:299+`. |
| Mixed-net note | The decision net's `groupHasDecisionMember` arm (`handler.go:722`, `handler_anthropic.go:312`) has NO imggen twin BY DESIGN: a chat-lane group carrying an image-gen member is already caught structurally — write-time homogeneity rejects the shape, and at runtime the widened `isServableGroupMemberBase` (§4.3 row 1) makes the member unservable in the chat lane, so it can never be picked. State this reasoning in the commit message. |
| Smoke discipline | **Do NOT amend any smoke expectation to 502** — misroute answers are 400s on every route; an existing smoke that today observes the rewrite-boundary failure of an image-gen group on a chat route gets the NEW 400 answer (the route guard fires first), never a 502 relabel. |

### 4.4 `pkg/ui/server.go` — admin-API mirrors (S1)

| Anchor | Change |
|---|---|
| `:1187-1240` (POST) / `:1744-1790` (PUT) | No structural change expected — both mirrors call `models.DeriveGroupLane`/`GroupLanesOf`/`MemberLane` (`:1192`, `:1747-1752`), so the widening is automatic. **Verify + pin parity**: `pkg/ui/kind_rules_parity_test.go:510 TestKindRules_Parity_Group_AllThreeSitesInSync` must gain image-gen-group fixtures (helper ≡ POST ≡ PUT — add a positive image-gen group case + a mixed `[chat, image-gen]` rejection case at all three sites). |
| `:216-218` routes | Unchanged — groups save through the ordinary `/fe/api/models` POST/PUT (`handleModels :1004`, `handleModelDetail :1570`). |

### 4.5 `pkg/proxy/handler.go` — `/v1/models` (S3)

| Anchor | Change |
|---|---|
| `:420-435` | Widen the group-lane collection from `lane == models.GroupLaneDecision` (`:426`) to non-chat lanes (decision + image-gen). Keep the map name/shape; the empty-diff gate is §3.5. |

### 4.6 `pkg/imggen` — the group serving branch (S2, the bulk)

**Import law (binding):** `pkg/imggen` may NEVER import `pkg/proxy` or
`pkg/decision` — direct OR transitive
(`pkg/imggen/import_guard_test.go:32-63`, `:67-100`; restated
`cmd/main.go:331-338`, `pkg/imggen/doc.go:9-20`). ⇒ **the member loop lives
INSIDE `pkg/imggen`, mirroring `serveDecisionGroup`'s SHAPE without
importing it.** **S-set (leader ruling, P1 half) — the `doc.go` update must
name the TRUE import set, positively:** after this commission the package
imports `{pkg/models, pkg/auth, pkg/authz, pkg/usage, pkg/events,
pkg/modelscache}` — `pkg/authz` since v203 (`pkg/imggen/handler.go:33`;
`doc.go:11-12`'s "{pkg/models, pkg/auth, pkg/usage, pkg/events} ONLY" is
stale on it) and `pkg/modelscache` from §4.6.3's compile-time seam proof
(the `var _ groupLaneSeam = (*modelscache.CachedModelsConfig)(nil)`
assertion imports the type — the decision precedent
`pkg/decision/handler.go:154`; guard-legal: modelscache sits BELOW the
banned lanes). The doc comment states BOTH halves: the true allow-set AND
the positive ban — "never `pkg/proxy`, never `pkg/decision`, direct or
transitive" — so neither direction is inferable-only. If the classifier
needs `pkg/credclass`/`pkg/credentiallb` TYPES (§4.6.4), they are
guard-legal — but v1 needs only the `Outcome` + status + code (see below),
so prefer importing NOTHING beyond the true set above.

**4.6.1 New file `pkg/imggen/group.go`** — the mirror of
`pkg/decision/group.go`, containing:

- `imggenAllMembersFailedCode = "imggen_all_members_failed"` (the greppable
  marker; decision precedent `decisionAllMembersFailedCode`
  `pkg/decision/group.go:56-58`) and `imggenMisrouteCode = "image_misroute"`.
- `memberFailure` struct — mirror `:61-78` shape: `{outcome, status, body,
  advance, rotate, class}`; v1 leaves `class` a plain string (no
  `credentiallb.FailureClass` import needed — rotation is per-request; the
  class string rides events only).
- `serveImageGenGroup(w, r, group, authToken, bodyBytes, startTime)`:
  1. **Lane gate (fail-closed, BEFORE scope)** — the seam must be present AND
     the group's lane must be image-gen; refusal ⇒ `400 image_misroute` +
     event (`upstream_status=0`), mirroring `pkg/decision/group.go:100-115`.
     The gate runs before any scope work so misroute answers never leak lane
     config token-dependently (precedence law: kind/lane BEFORE scope,
     `pkg/decision/handler.go:352-358`).
  2. **SCOPE SEAM (P2)** — a marked block, initially the pass-through that
     keeps today's semantics for the group id (the entry gate at
     `handler.go:305-316` already ran on the resolved row). P2 replaces this
     block with the three arms (§7.3). **Structural requirement P1 owns:**
     the member attempt ORDER is computed BEFORE the first attempt, and the
     loop NEVER consults scope (no `.Allows(` inside the loop — the shape P2's
     `authz_guard_test.go` extension pins, mirror `pkg/proxy/authz_guard_test.go:301-316`).
  3. **Policy + order** — an in-package `buildGroupPolicy` analog over
     `group.Members` × `currentModelRows()`: vanished → skip; `!row.Enabled`
     → skip; `MemberLane(row.Kind) != imageGenLane` → skip (defence-in-depth,
     `pkg/decision/policy.go:58-96` shape); weight default 1; pools by
     `BackupOnly`. Order = fresh weighted head from the DEFAULT pool, then
     remaining default members in configured order, then the backup pool in
     configured order (`policy.go:98-110` shape — NO sticky, F-4). **Policy
     may reorder, never exclude** (scope exclusion happens on the COPY in P2;
     health never enters the policy — the LAW).
  4. **Member loop** — `for idx := range order`: skip `tried`; inner
     per-member credential rotation loop (`h.resolveCredential(memberID)` =
     the existing seam → `ResolveInternalConfigWithAffinity(memberID,
     memberID)`, `cmd/main.go:343-349`); `triedPairs` on
     `memberID|credentialID`; rotation-eligible classes consume the member's
     credentials BEFORE the walk advances (mirror
     `pkg/decision/group.go:228-330` including the `:309/:356` advance
     checks and the `:315-324` rotate branch).
  5. **Per-attempt mechanics = the existing standalone path, extracted**:
     refactor steps 6-9 of `HandleImageGeneration`
     (`handler.go:336-418`: provider lookup → `BuildUpstream` → single POST
     under `min(remaining walk budget, imageGenTimeout)` → 64MB LimitReader
     read → `provider.InspectResponse`) into `attemptImageGenMember(...)`
     used by BOTH the standalone path and the group loop — one code path, no
     drift. Header discipline (Content-Type app-owned, client auth headers
     stripped, Accept-Encoding excluded, `handler.go:389-404`) is shared.
  6. **Classification (§4.6.4)** → `advance/rotate` + relay-or-continue.
  7. **Winner** — first `Outcome.RelayStatus == 0`; win-clear v1 = no-op
     (F-3: no cross-request writes — keep the call site shape so v2 can fill
     it); relay the winner body verbatim (the standalone relay semantics),
     meter per §3.2, ONE terminal success event (`model=groupID`,
     `member=winnerID`, `provider`, `image_count`, `duration_ms`,
     `upstream_status=200`-or-upstream-status) — mirror
     `pkg/decision/group.go:379-393`.
  8. **Exhaustion** — §3.6 envelope.

- **4.6.2 Handler dispatch + kind gate** (`pkg/imggen/handler.go`):
  - After `resolveModel` + 404 (`:271-284`), insert the kind/lane gate:
    - `row.IsGroup()` && lane == image-gen ⇒ `serveImageGenGroup` (the lane
      resolved through the optional seam, §4.6.3);
    - `row.IsGroup()` && lane != image-gen (chat/decision/unknown) ⇒ `400
      image_misroute` ("image-gen groups are served by /v1/image_generation
      only" wording mirrors the decision text
      `pkg/decision/handler.go:335-337`);
    - `!row.IsGroup()` && `!row.IsImageGen()` ⇒ `400 image_misroute` —
      **this closes a REAL pre-existing gap**: today ANY model id resolves
      (`:271`), passes scope, resolves a credential, and dies at provider
      lookup as `502 unsupported_provider` (`:339-351`) — a chat model whose
      provider happens to be minimax would be SERVED. The gate makes the
      answer kind-shaped and token-independent.
    - plain image-gen row ⇒ today's path unchanged (`:305-418`).
  - Update the precedence comment (`:297-300`) and the "this lane has NO
    candidate list … the entry check is the WHOLE scope obligation" comment
    (`:302-304`) — the second is REWRITTEN to state the group surface: the
    entry check covers plain models; the group arm (P2) owns its scope.
    Update the flow-step comment block (`:143-173`) step list for the group
    branch. Update the header contract comment (`:1-18`): the no-fallback
    contract gains "a GROUP surface with in-group member failover (the group
    is the failover unit; standalone models keep exactly ONE attempt)".
  - Event `outcome=image_misroute`, `upstream_status=0`.
- **4.6.3 Lane seam** — the optional-seam mirror of
  `pkg/decision/handler.go:144-154`:
  `type groupLaneSeam interface { GroupLane(groupID string) (models.GroupLane, bool) }`
  + `var _ groupLaneSeam = (*modelscache.CachedModelsConfig)(nil)` compile-time
  proof + a nil-safe `h.groupLane(groupID)` helper resolving through
  `h.modelsConfig` (`:85-102`, `SetModelsConfig :814-818`). **Zero new
  manager/decorator methods needed**: `ModelsManager.GroupLane` exists
  (`store_group_rewrite.go:587`) and the decorator forwards it
  (`pkg/modelscache/models.go:1465-1472` — the forward-trap doc
  `:1440-1464` is the precedent NOT to re-trigger). Without the fold (S0.2)
  this seam returns false for every imggen group and the lane gate fails
  closed — that coupling is the documented trap.
- **4.6.4 Failure classification** — an in-package
  `classifyMemberFailure(upstreamStatus int, outcome *Outcome) *memberFailure`
  mirroring `pkg/decision/group.go:833-880` in SHAPE but branching on the
  **imggen-own classifier outputs** (the `InspectResponse` Outcome seam,
  `pkg/imggen/doc.go:26-30` — NOT `pkg/providers.IsQuotaError`, NOT a proxy
  classifier):
  - **Quota class**: upstream 429 / `outcome.Code == CodeCooldown` (9router
    preserves 429 with UNWRAPPED nested-JSON quota text,
    `pkg/imggen/ninerouter.go:64-82`, live nested shape `:286-295`) ⇒
    `rotate=true`, `outcome=upstream_quota_exceeded`. POK
    `9router-imggen-quota-wall`: account-wide quota can fail ALL members
    identically — the walk still owes each member its honest attempt; the
    terminal envelope names the class (§3.6).
  - **Auth-rejected**: upstream 401/403 ⇒ `rotate=true`
    (mirror `:858-865`; no mark in v1 — F-3).
  - **Client-attributable**: 400/413/415/422 ⇒ **FAIL-FAST** `advance=false`,
    relay the member's upstream status+body (the standalone PromoteToError
    relay semantics — the request bytes do not change between members;
    mirror `:866-876`).
  - **5xx / everything else** ⇒ `advance=true`, no rotation (transient,
    provider-attributable; mirror `:877-879`).
- **4.6.5 doc.go** — extend the package contract comments: the import-law
  correction (§4.6), the group surface (one attempt per standalone model; a
  group = one winner across ≤16 members under a walk budget), and the
  classifier note (classification branches on `Outcome`, never raw status —
  Amendment 3 discipline preserved inside the loop).

### 4.7 `pkg/config/config.go` — the walk-budget knob (S4)

| Anchor | Change |
|---|---|
| `:159-167` (knob block) | Add `ImageGenGroupWalkTimeout Duration json:"image_gen_group_walk_timeout,omitempty"` beside `ImageGenTimeout`, doc comment citing §3.1 defaults/clamps + HOT read semantics. |
| `:207` (ManagerInterface) | Add `GetImageGenGroupWalkTimeout() time.Duration` — **W-2 (leader ruling): the getter goes ON `config.ManagerInterface`** (the full manager surface), and the handler-side slice widens with it. Per-request getter the handler reads = the same HOT pattern as `GetImageGenTimeout` (ConfigSnapshot-backed read per request; `PUT /fe/api/config` applies it without restart — no boot-owned value). |
| `:308` (Defaults) | `ImageGenGroupWalkTimeout: Duration(240 * time.Second)`. |
| `:336-346` (clamp block) | Clamp `[60s, 10min]`; `<= 0` ⇒ the handler falls back to the default (mirror `handler.go:128-137` degrade shape). |
| `:799-802` | `*config.Manager` gains the getter (the ManagerInterface primary implementer — see the implementer sweep below). |

**W-2 — implementer sweep (ALL five sites + one compile-cascade).** Adding
a method to `ManagerInterface` + the imggen `configGetter` re-signs every
implementer; enumerate them so none is missed:

| # | Site | Role |
|---|------|------|
| 1 | `pkg/config/config.go:802` (`*config.Manager`) | ManagerInterface primary implementer (the "+1 more" — the Manager's own getter beside `GetImageGenTimeout` at `:799-802`) |
| 2 | `pkg/store/database/store.go:336` (`*ConfigManager`) | DB-backed config manager getter (mirror of `GetImageGenTimeout` at `:332-340`) |
| 3 | `pkg/store/database/mock_store.go:98` (`*MockConfigManager`) | the mock — every store test that asserts the config surface needs the method present |
| 4 | `pkg/imggen/handler.go:108-110` (the local `configGetter` interface) | the consumer interface widens: `GetImageGenGroupWalkTimeout() time.Duration` beside `:109` |
| 5 | `pkg/imggen/handler_test.go:237` (`stubConfig`) | the in-package test stub must gain the method or the package stops compiling |
| +1 cascade | `pkg/ui/handlers_imggen_test.go:772` (`imgGenParityConfig`) | NOT a ManagerInterface implementer, but it is handed to the REAL `imggen.NewHandler` (whose `configGetter` widens, site 4) — it breaks compile unless the method is added; named here so the cascade is not discovered at build time |

## 5. Dependencies (ordered)

1. **S0 fold first** (§4.1) — everything downstream reads it: the write
   validator (§4.2), the mirrors (§4.4), the shape predicate (§4.3), the
   imggen lane seam + gate (§4.6), `/v1/models` (§4.5), the FE twin (§7.2).
   Without `MemberLane(KindImageGen)=true` the imggen lane gate fails closed
   on every group (the documented trap,
   `pkg/modelscache/models.go:1452-1459`).
2. **S0 lands with the FE fold twin in the SAME commit** (ADR-19) — §7.2.
3. Write path (§4.2-4.4) before serving (§4.6) — the serving tests need
   saveable groups.
4. The attempt extraction (§4.6.1 step 5) before the loop — one code path.
5. Budget knob (§4.7) with the loop (it parameterizes the attempt ctx).
6. P2 composes onto the landed §4.6 branch (see §7.3) — P1 is P2's
   artifact dependency, named in `phase2-plan.md` §2.
7. P3 (FE UI) consumes the fold twin + the admin-API acceptance of
   image-gen groups; P4 ships. **Nothing backend falls between P1 and P2** —
   the union of the two files is the whole backend scope.

## 6. Task breakdown (review-ordered)

| # | Task | Files | Acceptance |
|---|------|-------|------------|
| T1.1 | Lane vocabulary: const + `MemberLane` fold + `GroupLanesOf` arm + doc block; flip the `group_rules_test.go:555-556` pin IN PLACE (assert new truth — MODIFIED legacy test, NO rename, §3.8) + ADD dedicated `TestMemberLane_ImageGen_IsImageGenLane` + `[chat,image-gen]`/`[decision,image-gen]` mixed pins + `UnanimousGroupLane([image-gen])=(image-gen,true)` pin | `pkg/models/group_lane.go`, `pkg/models/group_rules_test.go` | Both test-ledger entries (`--- PASS` on the ADDED test; the MODIFIED legacy test green under its ORIGINAL name); no other `pkg/models` test churn |
| T1.2b | String sweep (W-5): `group.go:225` fallback literal + `group_lane.go:189` article fix + `group.go:248/:251` shadow-string sweep | `pkg/models/group.go`, `pkg/models/group_lane.go` | Grep proves zero remaining `(chat or decision)` literals and zero `a %s-kind` templates; message-shape pins updated |
| T1.2 | Error-text widening + `ValidateFallbackChainLanes` imggen arm + validator/mirror verification pins (helper ≡ POST ≡ PUT, positive + mixed rejection) | `pkg/models/group_lane.go`, `pkg/models/group.go`, `pkg/ui/kind_rules_parity_test.go` | Parity test green with image-gen fixtures at all three sites |
| T1.3 | Shape predicate arm + rewrite-boundary refusal widening (chat refuses image-gen groups both positions) + FE fold twin (§7.2) — **one commit with T1.1** | `store_group_rewrite.go`, FE fold files | Unit pins: imggen member base-servable in an imggen-lane group; refused in a chat chain (direct → `FailedGroupID`, fallback → dropped+log) |
| T1.4 | Walk-budget knob + getter + clamps | `pkg/config/config.go` | Config test: default 240s, clamp bounds, hot-getter presence |
| T1.5 | Lane seam + kind/lane gate + misroute envelope + precedence/comment rewrites | `pkg/imggen/handler.go`, new `pkg/imggen/group.go` (seam+gate parts) | Chat group ⇒ 400 `image_misroute` + event; decision group ⇒ same; chat standalone model ⇒ same (gap closed); plain image-gen ⇒ unchanged path (existing tests green) |
| T1.6 | Attempt extraction (`attemptImageGenMember`) — behavior-preserving refactor of steps 6-9 | `pkg/imggen/handler.go` | Standalone tests byte-green with zero fixture edits |
| T1.7 | The group serving branch: policy/order, member loop + rotation, classification, budget, W-8 quota short-circuit (3-consecutive counter, §3.1), winner relay + metering, exhaustion | `pkg/imggen/group.go` | §8 loop battery green incl. rows (h)/(i) |
| T1.7a | **W-3 (leader ruling): `currentModelRows()` helper** — it does NOT exist in `pkg/imggen` today; add the nil-safe `GetModels` forward mirroring `pkg/decision/group.go:676-683` (nil `modelsConfig` ⇒ nil), used by the policy builder + ARM#3's counting rule (P2). **Pin ONE read per request** — both consumers share the single call's slice; a second call per request is a review RED (the deep-copy cost rule, decision precedent `:175-177`) | `pkg/imggen/group.go` | One-read pin: a test/stub counter asserts exactly one `GetModels` invocation per group request |
| T1.8 | `/v1/models` widening + empty-diff pin | `pkg/proxy/handler.go`, proxy test | Id-set diff EMPTY with imggen groups configured |
| T1.8a | **C-2: chat-side misroute arms in BOTH chat routes** — image-gen-lane group case in `handler.go:712-730` switch; Anthropic standalone image-gen guard (closing the documented `:271-273` gap) + group case in `handler_anthropic.go:296-325`; code `image_gen_misroute` (chat family); no smoke expectation amended to 502 | `pkg/proxy/handler.go`, `pkg/proxy/handler_anthropic.go` | §8 misroute-arms rows green; every misroute answer is a 400 with the code string; zero upstream hits |
| T1.9 | doc.go contract-comment updates: TRUE import set named positively (`{models, auth, authz, usage, events, modelscache}` + the never-proxy/never-decision ban — S-set), group surface, classifier | `pkg/imggen/doc.go` | Comments match shipped behavior and the §4.6 import law verbatim; no stale "ONE attempt" absolute claim |

## 7. Cross-phase couplings

### 7.1 To Phase 2 (authz composition)

P2 owns the three v203 arms INSIDE `serveImageGenGroup` + tests T1-T8 +
`authz_guard_test.go` extensions + the ADR O1 supersession note. **P1 must
land:** (a) the marked SCOPE seam block (§4.6.1 step 2); (b) the structural
ordering — order computed before the first attempt, zero `.Allows(` in the
loop; (c) `scopeExcluded` counter plumbing left in place (initialized 0).
P2's artifact dependencies on P1 are named in `phase2-plan.md` §2.

### 7.2 To Phase 3 (FE) — the ADR-19 same-commit FE fold twin CONTRACT

ADR-19 (`pkg/models/group_lane.go:3-8`, `store_group_rewrite.go:52-58`): the
server fold and the FE twin must not ship different answers — **the twin is
the FOLD PRIMITIVES, not the UI.** P1's T1.1/T1.3 commit MUST include this
minimal FE diff (P3 plans the UI around it):

1. `pkg/ui/frontend/src/components/config/groupLane.ts:58-62 memberLane`:
   `'image-gen'` maps to `'image-gen'` (today → `null`, comment `:50-54`
   says image-gen belongs to NO lane — the comment flips too); the stable
   lane-order constant (mirroring `GroupLanesOf` chat, decision, image-gen)
   is extended.
2. `pkg/ui/frontend/src/types.ts:211 GroupLane` widens to
   `'chat' | 'decision' | 'image-gen'`.
3. The FE fold battery (vitest) gains: image-gen member ⇒ image-gen lane;
   `[chat, image-gen]` mixed ⇒ null/unrepresentable — SAME verdict as the
   server fold on every input (the parity law: the twin answers what the
   server answers).
4. **What the twin must NOT do in P1's commit:** no new tabs, no member
   pickers, no SettingsPage changes — P3 owns those
   (`SettingsPage.tsx:340-345 splitGroupsByLane`, `GroupModelForm.tsx:28-45`,
   `GroupModelsTab.tsx:49-93` are P3's surface). The `tsc-baseline`
   consequence of widening the union type is P3's to reconcile (widening a
   union can surface new exhaustive-switch errors — P3 must show the
   baseline verdict; P1's minimal diff keeps switches closed via the
   existing default arms).

### 7.3 P1→P2 interface (the exact seam)

```
// SCOPE (Phase 2 — authz composition): today the entry gate
// (handler.go:305-316) is the whole scope obligation for the resolved
// row. Phase 2 replaces this block with the three arms, mirroring
// pkg/decision/group.go:117-208:
//   ARM#1 group-id-in-reach (403 quotes the CLIENT string)
//   ARM#2 member exclusion on a COPY before buildGroupPolicy
//   ARM#3 empty-order fork: scope-caused ⇒ 403, unservable ⇒ 502
// The loop below must NEVER consult scope (structural law).
```

P2 additionally grows `authz.Resolve` count in `pkg/imggen` (the arm resolves
its own scope) — the lane-contract floor
(`pkg/proxy/authz_guard_test.go:369 totalResolveCalls >= 3`) still holds;
P2 decides whether to raise the floor to pin both imggen sites.

### 7.4 To Phase 4 (decisions + ship)

P4's `decisions.md` records, at minimum: F-1 walk budget (defaults/clamps/
heat, now including the W-8 quota short-circuit as the INNER bound), F-2
metering stance, F-3 health-depth deferral, F-4 no-sticky, F-5 catalog
gate, F-6 envelope (with the W-8 `short_circuited`/`last_class` keys), the
C-2 chat-side misroute arms (both routes, `image_gen_misroute` string law,
no-502-relabel discipline), the W-2 knob surface (ManagerInterface + the
5-implementer sweep), §3.7 shadow_model_id scope statement, the §3.8
corpus ledger (G3 MODIFIED-with-justification, G8 green, W-7 test ledger
with the rename prohibition), the A-2 all-paths rollback wording, the A-5
decision-zero-edit gate artifact, the ADR O1 supersession (with P2), the
rollback/data-compat assessment (§10), and the group_rules pin flip.

## 8. Test strategy (Phase 1 battery)

**Packs:** A = `./pkg/proxy/... ./pkg/store/... ./pkg/decision/... ./pkg/ui/... ./cmd/...`;
B = `go list ./... | grep -vE '<module>/(cmd|pkg/(proxy|store|decision|ui))(/|$)'`
(module-prefix anchor is LOAD-BEARING — repo-relative re-runs ALL).
**Anti-vacuous:** every ADDED `func Test` shows a literal `--- PASS`.
**gofmt:** ZERO new drift — NEW drift = HEAD flagged-LINE-SET minus base
flagged set (`git show <base>:<file> | gofmt -d`), then `gofmt -l` ALL
touched `_test.go`.
**HOME isolation** before full-suite runs (`go test` rewrites
`~/.config/llm-supervisor-proxy/config.json` otherwise — POK
`go-test-home-isolation`). **Flakes — re-run to confirm, never chase:**
`pkg/credentiallb` TOCTOU_CoolingReverify ~1/10 plain; `pkg/store`
TestStoreEngine_PeakHourParity fires only 23:59:00-23:59:59.999 UTC.
**Migration gate:** NO migration lands — the 88 SQL/91 grep reconciliation
shows zero delta (out-of-scope fence §2.2.2).

| Group | Tests (new/changed) |
|---|---|
| Fold | T1.1 flip + mixed pins; `pkg/modelscache/models_group_lane_test.go` extended: image-gen group lane resolves THROUGH the decorator (positive, trap-named failure message per `:89-109`); MIXED group ⇒ `ok=false` (negative, `:114-140` shape) |
| Write path | Parity trio (helper/POST/PUT): positive image-gen group saved; `[chat,image-gen]` rejected with the widened message; shadow-target lane pin; `ValidateFallbackChainLanes`: chain naming an imggen group rejected at all sites |
| Shape/boundary | `isServableGroupMemberBase` image-gen arm (servable in imggen lane, unservable in chat lane); chat rewrite refuses imggen group (direct `FailedGroupID` / fallback dropped+log) |
| Kind gate | Chat group / decision group / chat standalone posted to `/v1/image_generation` ⇒ `400 image_misroute`, code in body, event `outcome=image_misroute`, `upstream_status=0`, **zero upstream hits**; plain image-gen row unchanged (existing suite green) |
| Loop | (a) first-member 200 winner relayed verbatim, member row metered (request+images), group row untouched; (b) member A 429-quota ⇒ rotation to member A's cred 2, then advance to member B ⇒ winner B; (c) all members fail ⇒ `502 imggen_all_members_failed`, ONE terminal event with counters, group-row request_count=1 image_count=0, no member failure rows; (d) client 400 ⇒ fail-fast relays that member's status+body, walk stops; (e) budget: per-member ctx capped at `min(remaining, ImageGenTimeout)`; budget exhaustion mid-walk ⇒ exhaustion envelope + `budget_exhausted` event key; (f) walk ADVANCES on member failure (NOT single-attempt — mirror the decision-law pin: every base-servable member attempted before the request may fail) **for every class EXCEPT quota (W-8, row (h))**; (g) disabled/vanished/wrong-kind member skipped by policy, never attempted; (h) **W-8 short-circuit:** 3 CONSECUTIVE quota-class failures (across members) ⇒ walk stops, exhaustion envelope, terminal event carries `short_circuited=true` + `last_class=upstream_quota_exceeded`, remaining members NOT attempted; counter RESET semantics: quota, quota, 5xx, quota, quota ⇒ NO short-circuit (the 5xx resets the consecutive run); budget still bounds the whole walk (outer over inner); (i) **W-3 one-read pin:** exactly one `GetModels` call per group request (policy + counting share it) |
| Misroute-vs-scope precedence | Misroute is token-INDEPENDENT: wildcard token + chat group ⇒ still 400 (no 403/502) — precedence pinned (P2 deepens with T8) |
| Chat-route misroute arms (C-2) | image-gen-lane GROUP posted to `/v1/chat/completions` ⇒ `400` code `image_gen_misroute`, zero upstream hits (guard fires pre-rewrite); same on `/v1/messages` (Anthropic envelope family, same code string); standalone image-gen MODEL on `/v1/messages` ⇒ `400 image_gen_misroute` (the `handler_anthropic.go:271-273` documented gap CLOSED by this commission); chat-lane group unaffected (lane-gated arms); NO smoke expectation anywhere amended to 502 |
| Import guards | `pkg/imggen` guards stay green (`import_guard_test.go:32-100`) — the loop is in-package; `go list -deps` closure clean |
| Catalog | `/v1/models` empty-diff gate (§3.5) |

**A-5 (leader ruling) — the decision-lane zero-edit GATE ARTIFACT.** The
decision lane's Go sources are untouched by this commission, and the gate
is a named, checkable artifact:

```
git diff $BASE..HEAD -- pkg/decision/        # $BASE = 5cf1ecdc
# ⇒ MUST be EMPTY (zero exceptions)
```

The ONE declared exception to the lane-corpus-untouched rule anywhere in
this commission is P2's `pkg/proxy/authz_guard_test.go` extension
(§4.3/phase2) — which lives in `pkg/proxy`, OUTSIDE `pkg/decision/`, so it
does not pierce this gate. Any non-empty `pkg/decision/` diff is a RED
gate: not a fixture edit, not a rename — a stop-and-explain. (Existing
`pkg/proxy` group suites stay fixture-identical; the C-2 arms are
ADDITIONS with their own new tests.)

**A-6 (leader ruling) — zero-engine-state-after-403 assertion.** The 403
arms live in P2's T-battery (`phase2-plan.md` §4.4, T1/T4 additions): after
any 403 outcome, assert ZERO engine-state writes (no marks, no cooldowns,
no skip-cache entries) — this pins the v1 no-health-writes stance (F-3) on
the permission paths AND guards the v2 health-mirror follow-up: when the
mirror lands, the 403 paths must STILL write nothing. P1 cross-references
it here only — no duplicate pin on the P1 side.

**W-7 test ledger pointer:** the fold-flip ledger (ADDED
`TestMemberLane_ImageGen_IsImageGenLane` + MODIFIED
`TestValidateGroupMembers_LegacyEmptyKind_IsChatLane`, rename forbidden)
is normative text at §3.8; the battery above implements it verbatim.

## 9. Risks + mitigations

| # | Risk | Impact | Likelihood | Mitigation |
|---|------|--------|-----------|------------|
| R1 | `GroupLane` fold is the CHOKE POINT — consumers of `MemberLane`/`UnanimousGroupLane`/`inferGroupLane` span: `pkg/models/group.go` validator; `group_lane.go` folds; `store_group_rewrite.go:526-571` (shape + inference); the chat rewrite boundary `:126-132`; decision policy `pkg/decision/policy.go:79`; decision gates `pkg/decision/handler.go:324/:872-877`; `/v1/models` `pkg/proxy/handler.go:426`; decorator `pkg/modelscache/models.go:1465-1472`; FE twin; `pkg/ui` mirrors; `group_rules_test.go`; `models_group_lane_test.go` | High | Medium | T1.1 lands first; EVERY consumer enumerated here gets a pin (the battery §8 covers each row); decision-lane pins (`handler_group_test.go`) must stay byte-green — any decision-side diff is a RED gate, not a fixture edit |
| R2 | `isServableGroupMemberBase` default arm is chat-only — a missed arm = members SILENTLY unservable (empty order ⇒ 502 on every request) | High | Medium | T1.3 mandatory arm + explicit servable/unservable pins; the decorator-through lane test catches the silent-shape failure mode |
| R3 | imggen kind-gate absence (pre-existing): any model id resolves today; a minimax-provider chat model would be SERVED on this lane | High | Certain (it's the status quo) | T1.5 gate closes it; gap-pin added; gate is token-independent (misroute before scope) |
| R4 | 9router ACCOUNT-WIDE quota: all members fail identically in ~15-17s each — a full 16-member wall burns ~4.5 min without a bound | Medium | Medium | W-8 short-circuit (§3.1: 3 consecutive quota-class failures stop the walk) + §3.1 walk budget as the outer bound + §3.6 class-bearing terminal event; classification keeps 429=quota (never 502) per POK `9router-imggen-quota-wall` |
| R4b | A chat-lane group that (post-runtime-retype) carries an image-gen member could reach a chat route | Medium | Low | C-2 route arms are lane-gated on the GROUP lane; the member itself is unservable in the chat lane via the widened `isServableGroupMemberBase` (§4.3) + the rewrite-boundary refusal — the mixed shape can never be picked (the no-`groupHasImageGenMember`-twin reasoning, §4.3 C-2 block) |
| R4c | **No reconcile interaction (S-set, stated):** the periodic-reconcile write-mutex stall risk (system risk, `pkg/store/database/credential_group.go:637` lock span) does NOT interact with this commission — the walk reads ONLY the in-memory snapshot (`currentModelRows()`/`membersOf`), the commission performs ZERO store writes (F-3), touches no credential-group surface, and adds no reconcile participant | None | None | Recorded so no reviewer re-derives the interaction; if a v2 health-mirror lands later, THAT commission re-opens the assessment (its writes would join the reconcile-relevant surface) |
| R5 | Per-member timeout multiplication (worst case 16 × 120s) | Medium | Medium | Budget clamps the walk; ingress-timeout argument documented in §3.1; knob is hot for operator tuning |
| R6 | Rollback data-compat: OLD binary + NEW imggen-group rows. Old `MemberLane(image-gen)=false` ⇒ `UnanimousGroupLane` fails ⇒ `GroupLane` seam answers unknown-lane; old binary's imggen handler has NO kind gate ⇒ the group row resolves and then **fails closed somewhere on the credential/provider/upstream path** (whichever step the old binary reaches first — credential resolution on a group row, provider lookup, or the upstream call: every path refuses; there is no path that serves a member) — **benign-inert** (no wrong-lane serve, no crash; A-2 wording: ALL paths, not one envelope). Old binary's WRITE path rejects any edit of such groups (InvalidMemberKindError) — rows are frozen but harmless. Old binary's `/v1/models` decision-only exclusion: imggen GROUP ids WOULD APPEAR in `/v1/models` on the old binary (they are group-kind rows, not caught by `:432 m.IsImageGen()`) — minor leak of an id (no capability); acceptable for a rollback window, self-heals on re-upgrade. **Verdict: benign-inert, NO cleanup migration needed** | Medium | Low | Recorded for P4's decisions.md; no 035 migration (fence §2.2.2) |
| R7 | Contract-comment + ADR discipline: stale "ONE attempt"/"no candidate list" comments and ADR O1 ("Image-gen has no group or chain surfaces") contradict the shipped surface | Low | Medium | T1.9 rewrites doc.go/handler comments; ADR O1 supersession is P2's to note, P4's to formalize (§7.4) |
| R8 | Attempt-extraction refactor (T1.6) silently changes standalone behavior | Medium | Low | Behavior-preserving by construction (same code moved); standalone suite byte-green with ZERO fixture edits is the gate |
| R9 | FE twin ships divergent fold answers (ADR-19 breach) | Medium | Low | §7.2 contract pins the twin to the server verdicts; same-commit law; P3's vitest battery extends the parity set |

## 10. Rollback thinking

Single release, no migration, no store writes (F-3) ⇒ rollback = image
rollback (P4 owns the ladder; tier-1 = prior tag). Data-compat assessed at
R6: new rows are benign-inert on an old binary — the group row, wherever
the old binary reaches it, **fails closed somewhere on the
credential/provider/upstream path** (every path refuses; no path serves a
member), edits of such rows are rejected (frozen but harmless), and the
only observable is minor `/v1/models` id visibility during the rollback
window. No reverse-migration step exists or is needed; re-upgrade restores
the group surface over the same rows.

## 11. Success criteria (checkable)

| # | Criterion | Measure | Threshold |
|---|-----------|---------|-----------|
| 1 | An image-gen group (members = image-gen rows) saves via POST and PUT `/fe/api/models` and reads back with lane inferred image-gen | API round-trip + decorator lane test | 200 both; `GroupLane(gid)=(image-gen,true)` through `*CachedModelsConfig` |
| 2 | `POST /v1/image_generation` with the group id serves a 200 from a healthy member; relay byte-identical to the standalone relay of that member | Handler test + diff | Exact body equality; winner member row metered (req=1, images=BillableImages); group row unmetered |
| 3 | Failover: member A quota-429 ⇒ member A cred rotation ⇒ member B serves; walk ADVANCES (never single-attempt) | Loop test with stubbed upstreams | ≥2 upstream hits, final 200, per-attempt events present |
| 4 | All-members-fail ⇒ `502` + `imggen_all_members_failed`, ONE terminal event, group-row request_count=1 image_count=0, no member usage rows | Loop test | Exact envelope; usage-store assertions |
| 5 | Chat/decision group or non-image-gen model on the lane ⇒ `400 image_misroute` (token-independent), zero upstream hits | Misroute battery incl. wildcard token | 400 + code + event; 0 hits |
| 6 | `/v1/models` id set unchanged by the presence of image-gen groups | Empty-diff test | Diff = ∅ |
| 7 | Decision lane zero-edit (A-5 gate artifact) + chat lanes additive-only | `git diff $BASE..HEAD -- pkg/decision/` EMPTY (zero exceptions); existing `pkg/proxy` group suites fixture-identical (C-2 arms are additions with new tests; the ONE declared lane-corpus exception is P2's `pkg/proxy/authz_guard_test.go` extension, outside `pkg/decision/`) | Empty diff; all existing suites green unedited |
| 12 | W-8 quota short-circuit | Loop test row (h) | 3 consecutive quota failures stop the walk with `short_circuited=true` + `last_class` event keys; non-quota outcome resets the run; budget remains the outer bound |
| 13 | Chat-route misroute arms (C-2) | §8 chat-arms rows, both routes | `400 image_gen_misroute` on `/v1/chat/completions` AND `/v1/messages` (group + Anthropic standalone); zero upstream hits; zero smoke expectations relabeled to 502 |
| 14 | One snapshot read per group request (W-3) | GetModels call-counter pin | Exactly 1 call per request |
| 8 | Migration count unchanged | Reconciliation gate | 88 SQL / 91 grep, no delta |
| 9 | Fold twin parity | FE fold battery in the same commit | Twin verdicts == server verdicts on the shared fixture set |
| 10 | Budget honored | ctx-cap test | per-member ctx ≤ min(remaining, ImageGenTimeout); walk stops at budget with the envelope |
