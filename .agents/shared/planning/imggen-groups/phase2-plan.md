# Phase 2 — imggen-groups: AUTHZ COMPOSITION + TEST PINS

> **Commission:** imggen groups (see `phase1-plan.md` header). **Phase 2 scope:**
> the v203 token-authz composition on the new image-gen GROUP surface — the
> three arms inside `serveImageGenGroup` (group-id-in-reach, member exclusion
> on a copy, the 403-vs-502 discriminator) — plus the authz pin battery T1–T8,
> the structural-guard extensions (`pkg/proxy/authz_guard_test.go`), the
> decorator-through pin, and the ADR O1 supersession record. **Phase 1's group
> machinery is this phase's foundation** (§2 names the artifacts).
> **Worktree:** `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-imggen-groups`
> (branch `feature/imggen-groups-text-subtabs`, base `origin/latest` @
> `5cf1ecdc`). Line-pins read at `5cf1ecd` on 2026-11-20.
> **Binding pattern:** the v203 mirror — `pkg/decision/group.go:97-208` is the
> template this phase transposes onto `pkg/imggen`; the frozen contract is
> `.agents/shared/planning/token-authz-allowed-groups/` (plan.md §2, decisions.md
> Q3 `:162` — "group ids only, ANY lane" — an image-gen group id belongs on the
> `allowed_groups` axis with the same validator, zero token-store changes).
> **Status:** Draft plan — no code, no tests, no config edits, no commits, no
> pushes, no deploys were performed to produce it.

---

## 1. Objective

A token's reach composes correctly with the new image-gen group surface, with
the v203 semantics mirrored exactly: **authorizing the resolved group id**
(the group arm resolves its OWN scope — the bypass-closed pattern), **member
exclusion at construction on a copy** (never inside the attempt loop), and the
**attempt-count-keyed 403-vs-502 discriminator** (scope-caused emptiness ⇒
`403 access_denied` in the lane's existing envelope, never the availability
`502`; unservable ⇒ the P1 exhaustion envelope, never a permission error).
Every arm is pinned: T1–T8 behavioural pins, the structural no-`.Allows(`
-in-loop pin extended to the imggen loop, the lane-contract raw-axis ban
honored (zero new raw-axis reads), and ADR O1's "Image-gen has no group or
chain surfaces" explicitly superseded for P4's decisions.md.

**No authz-package change.** `pkg/authz.Resolve` (`pkg/authz/scope.go:78-119`)
and `(*Scope).Allows` (`:127-139`) are frozen by v203 and handle an
image-gen group id TODAY (reach = `allowed_models ∪ allowed_groups ∪ ⋃
Members(g)`, `:93-118`; the group target itself is reachable `:104-107`;
conjunctive wildcard `:88-91`; fail-closed on an unreadable axis `:82-84`).
The membership expansion already flows through the lane's existing
`membersOf` adapter (`pkg/imggen/handler.go:789-809`, the
`models.GroupMembershipReader` in-memory snapshot read — Ruling 5). Migration
034 already repartitioned group ids into `allowed_groups` for ANY lane
(decisions.md `:162`). **Phase 2 is pure composition + pins on the consumer
side.**

## 2. P1 artifact dependencies (explicit — Phase 2 may assume, and requires)

| # | P1 artifact | Anchor (post-P1) | Why P2 needs it |
|---|-------------|------------------|-----------------|
| D1 | `pkg/imggen/group.go` with `serveImageGenGroup` carrying the marked SCOPE seam block, `scopeExcluded` counter (init 0), and the attempt loop whose ORDER is computed before the first attempt with zero `.Allows(` inside | P1 §4.6.1 steps 2-4, §7.3 | The three arms replace the seam block; the discriminator keys on the pre-attempt order; the structural pin greps the loop |
| D2 | The lane seam (`groupLaneSeam` + decorator compile-proof) and the fail-closed lane gate BEFORE scope | P1 §4.6.1 step 1, §4.6.3 | ARM#1 runs AFTER the lane gate (misroute stays token-independent); the gate needs the seam |
| D3 | The kind gate + `image_misroute` envelope on the entry path | P1 §4.6.2 | Precedence pin (T8): 400 misroute fires regardless of scope — the kind gate ordering is load-bearing |
| D4 | The in-lane policy builder (pools/weights/configured order; may reorder, never exclude) + `currentModelRows()` | P1 §4.6.1 step 3 | ARM#2 filters a COPY **before** the policy call; ARM#3's counting rule reuses the same candidacy predicate on the UNFILTERED row |
| D5 | The exhaustion envelope (`502 imggen_all_members_failed`) + terminal-event builder with counters | P1 §3.6, §4.6.1 step 8 | ARM#3's 502 branch writes THAT envelope; T4 asserts the 403 body does NOT contain the 502 code |
| D6 | The walk accounting (`attempted`, `skipped`, `membersAttempted`, per-attempt events) | P1 §4.6.1 step 4 | Events carry `upstream_status=0` on 403 paths; T1/T4 assert zero upstream hits via the accounting + stub counters |
| D7 | Fold widening (`MemberLane(image-gen)` valid; decorator `GroupLane` forward answers image-gen) | P1 T1.1 | The lane gate's resolution is what routes an image-gen group INTO the arm at all (without D7 the gate fails closed and the arm is unreachable) |
| D8 | Test scaffolding: the group test env builder (stubbed upstreams with hit counters, token minting) | P1 §8 loop battery | T1–T8 reuse the same env; do not rebuild it |

**Phase 2 must NOT touch:** the loop's rotation/advance mechanics, the
classification, the budget, the metering stances, the write path, the FE twin
(P3), `/v1/models` — all P1-owned, landed, and pinned before P2 composes.

## 3. Scope

### 3.1 In scope (Phase 2)

| # | Item | Template |
|---|------|----------|
| S1 | ARM#1 — group-id-in-reach inside the group arm, after the lane gate, before candidate construction; 403 quotes the CLIENT string; the arm resolves its OWN scope | `pkg/decision/group.go:117-146` |
| S2 | ARM#2 — member scope-exclusion on a COPY (fresh backing array) before the policy builder; `scopeExcluded` counting | `:148-166` |
| S3 | ARM#3 — the empty-order fork: `countBaseServableMembers` imggen twin on the UNFILTERED row; scope-caused ⇒ 403 access_denied, unservable ⇒ 502 machine code; order computed BEFORE the first attempt | `:168-208`, `:643-663` |
| S4 | The entry-gate comment rewrite (`handler.go:302-304`) + header contract note (`:1-18`) + ADR O1 supersession record | token-authz ADR `:450` |
| S5 | Structural guards extended: imggen attempt-loop no-`.Allows(` pin; **W-1 closed explicitly — the imggen resolve arm is STRING-PINNED** (its literal arm text asserted; the entry-gate resolve cannot satisfy the pin) **AND the numeric floor is `>= 6` = the FINAL post-fold count** (5 verified census sites + the 1 new arm; today's `>= 3` passes with 5 and would not notice the arm's removal — non-vacuous; §4.3/T2.5); decorator string-pins re-checked | `pkg/proxy/authz_guard_test.go:281-325`, `:340-382`, `:394-413` |
| S6 | The pin battery T1–T8 (new `pkg/imggen/authz_group_test.go`) + disabled-vs-out-of-scope no-leak + through-decorator binding | `pkg/proxy/authz_scope_test.go:200-269` + `pkg/decision/handler_group_test.go:2004-2050`, `:1894`, `:640` |

### 3.2 Out of scope (fence)

1. **No `pkg/authz` change** — the resolver/predicate are frozen (v203
   contract; leaf-import guard `scope.go:75-77`). No new Scope fields, no
   wildcard changes, no deny-all changes.
2. **No token-store / migration / admin-API change** — `allowed_groups`
   already accepts group ids of ANY lane (decisions.md `:162`); the FE
   TokenForm already edits the axis (v203). An image-gen group id is just
   another `kind='group'` row id.
3. **No raw-axis reads** — the lane may not read
   `token.AllowedModels`/`token.AllowedGroups` (`authz_guard_test.go:344`
   axis regex); the arm uses `authz.Resolve` + `.Allows` only.
4. **No P1 mechanics changes** (§2 fence) and no P3/P4 work: the FE scope
   UI, ship gates, deploy, and decisions.md authorship are theirs (P4
   FORMALIZES what S4 records).
5. **A-5 alignment (leader ruling):** `pkg/decision/` stays ZERO-EDIT for
   this commission — P1's gate artifact
   (`git diff $BASE..HEAD -- pkg/decision/` EMPTY, zero exceptions) binds
   P2 too. The ONE declared exception to the lane-corpus-untouched rule is
   this phase's `pkg/proxy/authz_guard_test.go` extension — which lives in
   `pkg/proxy`, OUTSIDE `pkg/decision/`, so it pierces nothing; the wording
   here is aligned verbatim with P1 §8's gate artifact. No `pkg/decision`
   file, test or fixture, is touched for any reason in this phase.

## 4. Components (file-anchored)

### 4.1 `pkg/imggen/group.go` — the three arms (S1–S3)

**ARM#1 — the group itself must be in reach** (replaces the P1 seam block,
mirroring `pkg/decision/group.go:117-146`):

- `scope := authz.Resolve(authToken, h.membersOf())` — resolved INSIDE the
  group arm, right after the lane gate (the v203 bypass-closed pattern: the
  arm owns its scope; the handler keeps no cross-request scope state). This
  is a SECOND `authz.Resolve` site in `pkg/imggen` (the entry gate
  `handler.go:305` keeps its own for plain models) — mirror-legal; see §4.3.
- `if scope != nil && !scope.Allows(group.ID)` ⇒ publish
  `{outcome: model_not_allowed, model: <CLIENT string>, duration_ms,
  upstream_status: 0}` + `sendModelNotAllowedError(w, clientModel)` +
  return. **Authorize the RESOLVED group id (`group.ID`); quote the
  CLIENT-requested string** (`req` body's `model` field) — on this lane a
  claimed id could resolve into the arm, so the canonical id never appears
  in the body while the client string does (`pkg/decision/group.go:132-138`
  PD-9 rationale; imggen's claims are group-only, `handler.go:267-270`).
- Ordering: lane gate → ARM#1 → candidate construction. Misroute answers
  stay token-independent (a chat/decision group is a 400 regardless of
  grants — T8).

**ARM#2 — filter a COPY before the policy** (mirroring `:148-166`):

```
filtered := *group
filtered.Members = make([]models.GroupMember, 0, len(group.Members))
scopeExcluded := 0
for _, mbr := range group.Members {
    if scope == nil || scope.Allows(mbr.ModelID) { filtered.Members = append(...); continue }
    scopeExcluded++
}
```

- The copy is a FRESH backing array (`make(..., 0, len)`) — never
  `group.Members[:0]` (capacity-sharing would write into the cached row;
  `:153-156` PD-15). The cached row is never mutated.
- The policy builder (P1 D4) receives `&filtered`; it stays byte-unchanged
  (its doctrine: may reorder, never exclude). Exclusion happens HERE and
  nowhere else — structurally pinned (§4.3).

**ARM#3 — the empty-order fork** (mirroring `:168-208` + `:643-663`):

- `baseServable := countBaseServableMembers(group.Members, rows,
  models.GroupLaneImageGen)` — the imggen twin of the decision counter,
  computed on the **UNFILTERED** member list with the SAME candidacy
  predicate the policy uses (row exists ∧ row enabled ∧ member lane ==
  image-gen). ONE live-row read (`currentModelRows()`) serves BOTH the
  count and the policy (do not call it twice — the deep-copy cost).
- `order := policy.order()`; if `len(order) == 0`:
  - `scope != nil && scopeExcluded > 0 && baseServable > 0` ⇒ **403**
    `model_not_allowed` (client string quoted, same envelope as ARM#1) —
    the emptiness was scope-CAUSED.
  - else ⇒ the P1 **exhaustion envelope** (`502 imggen_all_members_failed`)
    — all-disabled/all-vanished/mixed-retyped is AVAILABILITY, not
    permission (`:194-198`: a disabled out-of-scope member is still a
    disabled member).
- **Attempt-count-keyed, order-computed-before-first-attempt** is the
  structural law (`:189-192` leader ruling): zero upstream attempts ran ⇒
  the permission verdict is available. P1 landed the ordering; P2 pins it
  (T4 asserts ZERO upstream hits on the 403 path — the gate returns before
  credential resolution `handler.go:323`, provider lookup `:339`,
  BuildUpstream `:353`, the POST `:366+`).

### 4.2 `pkg/imggen/handler.go` — comment + envelope reuse (S4)

| Anchor | Change |
|---|---|
| `:302-304` | REWRITE the "this lane has NO candidate list … the entry check is the WHOLE scope obligation" note: the entry check covers PLAIN models; the GROUP arm owns its scope (resolve-inside-arm, member filter, empty-order fork — cite `pkg/decision/group.go:117-208` as the pattern). |
| `:1-18` (header) | The contract note gains the group surface (P1 T1.9 lands the no-fallback wording; P2 adds the scope-surface sentence: "a group's scope is resolved inside its serving branch; a plain model's at the entry gate"). |
| `:750-760` | NO change — `sendModelNotAllowedError` is reused byte-identically by the arms (same nested `{"error":{"type":"access_denied","message":"model not allowed for this token","model":<client string>}}`, no `code` field). |

### 4.3 `pkg/proxy/authz_guard_test.go` — structural guards (S5)

| Anchor | Change |
|---|---|
| `:281-325` `TestPackage_ImportGuard_NoScopeInHotLoop` | ADD the imggen analogue of the decision pin (`:301-316`): locate the imggen attempt loop in `pkg/imggen/group.go` by its sentinel (`for idx := range order {`) and assert no `.Allows(` appears after it — "the member filter must run on the COPY before the policy". Extend the negative-control block (`:318-324`) unchanged in spirit (it already fires on banned shapes). |
| `:340-382` `TestLaneContract_ReachResolutionOnlyViaAuthz` | The lane list (`:342`) already includes `pkg/imggen`; the raw-axis ban (`:344`) holds with zero new reads (composition uses `.Allows` only). **W-1 (leader ruling) — STRING-PIN the imggen arm, preferred over a bare numeric floor:** add an assertion that the arm's resolve EXISTS AS LITERAL TEXT in `pkg/imggen/group.go` — pin the distinctive arm sentinel (e.g. the arm's `scope := authz.Resolve(authToken, h.membersOf())` line, matched inside the ARM block bounded by its opening comment marker, so the ENTRY-gate resolve at `handler.go:305` cannot satisfy the pin). A removed arm-resolve then fails the guard BY NAME, not by a count drift. **If the numeric floor is kept alongside (recommended — belt and braces), set it to the FINAL post-fold arm count, not a loose bound:** the lanes hold 5 `authz.Resolve(` sites today (`pkg/proxy/handler.go:777`, `pkg/proxy/handler_anthropic.go:473`, `pkg/decision/group.go:124`, `pkg/decision/handler.go:359`, `pkg/imggen/handler.go:305` — verified at base) and the fold adds EXACTLY ONE (the imggen group arm) ⇒ **final count 6; floor becomes `>= 6`** so the gate is non-vacuous (today's `>= 3` passes with 5 and would not notice the arm's removal). Keep the negative controls firing. |
| `:394-413` decorator string-pins | NO new decorator methods ship (P1 §4.6.3: `GroupLane` + `GroupMembers` forwards already exist and are pinned at `:401-408`) — re-run green; no edit unless the pin list drifts upstream. |

### 4.4 New `pkg/imggen/authz_group_test.go` — the T1–T8 battery (S6)

Mirrors the four-arm shape of `pkg/proxy/authz_scope_test.go:200-269`
(group-not-granted / granted / wildcard / all-out) with the envelope helper
shape `:166-191` (decode nested error; assert `type=access_denied`,
`model=<CLIENT string>`, `code==""`), reusing P1's group env (D8) —
stubbed upstreams with per-member hit counters, token minting with
`auth.FullPermission(models, groups)`:

| # | Pin | Assertion |
|---|-----|-----------|
| T1 | group NOT granted (token grants only an unrelated model) | `403 access_denied`, envelope quotes the client string, `upstream hits == 0`, event `outcome=model_not_allowed upstream_status=0` |
| T2 | group granted via `allowed_groups` | `200` from a member, `hits >= 1` (reach expands to members — `scope.go:111-116`) |
| T3 | wildcard token (both axes absent) | `200` — conjunctive wildcard unchanged (`scope.go:88-91`); bit-for-bit today's behavior |
| T4 | group granted by MODEL axis only (`allowed_models=[g]`, NO group axis ⇒ reach = {g} with ZERO member expansion, `scope.go:93-107`); members all enabled ⇒ `baseServable>0`, `scopeExcluded==N`, order empty | `403` **NOT** `502` — body must NOT contain `imggen_all_members_failed`; `hits == 0` |
| T5 | one member excluded, one admitted | excluded member NEVER attempted (0 hits) and NEVER NAMED in the body; admitted member serves; `scopeExcluded` never leaks (no 403 on a non-empty order) |
| T6 | disabled-vs-out-of-scope answers BYTE-IDENTICAL | swap a disabled member for an enabled out-of-scope one ⇒ same status, same envelope bytes (no existence/enablement oracle) — mirror `pkg/decision/handler_group_test.go:2004-2050` incl. the forbidden-substring sweep (`access_denied`, the machine code, the member id) on the 200 path |
| T7 | group served THROUGH the production decorator binding | build a migrated SQLite stack, wrap `*modelscache.CachedModelsConfig`, wire `SetModelsConfig`, serve — mirror `pkg/decision/handler_group_test.go:1894 TestDecision_Group_ThroughCachedConfig` (the decorator-forward-trap end-to-end proof; without the forward the lane gate fails closed and this pin goes red) |
| T8 | chat/decision group posted to `/v1/image_generation` | `400 image_misroute` — NEVER 403/502 — INCLUDING with a wildcard token (misroute precedence is token-independent; mirror `:640` seam-absent fail-closed shape and `pkg/decision/handler.go:352-358` precedence law) |

Plus: **exhaustion-vs-scope pair** (all members DISABLED + scopeExcluded>0 ⇒
502 not 403 — `baseServable==0` is availability); **404-vs-403** (unknown id
⇒ 404 regardless of grants — existing precedence `handler.go:297-300`
re-pinned beside the new arms).

**A-6 (leader ruling) — the zero-engine-state-after-403 assertion lives in
THIS battery (P1 cross-references it, `phase1-plan.md` §8; no duplicate
pin there).** After every 403 outcome exercised by T1 and T4 — and after
the ARM#3 403 branch of the exhaustion-vs-scope pair — assert **ZERO
engine-state writes**: no marks, no cooldown entries (engine or
group-owned), no skip-cache writes, on any layer the test env can observe.
Implementation shape: the env exposes read-only counters over the
health-layer seams (empty in v1 — the assertion is that they are STILL
empty). Purpose is forward-looking: it guards the F-3 v1 stance on the
permission paths AND the v2 health-mirror follow-up — when the mirror
lands, the 403 paths must STILL write nothing (a permission denial is not
a health fact; the store-side precedent is the out-of-reach member that is
"SKIPPED, never cooled" — `store_group_rewrite.go:241-242`).

### 4.5 ADR O1 supersession record (S4)

ADR O1 (`.agents/shared/planning/token-authz-allowed-groups/adr-token-permission-model.md:450`):
"**O1 — Image-gen has no group or chain surfaces.** … the imggen lane
therefore [has a single-model scope surface]" — **now false.** Phase 2's
commit records the supersession in the code comments (§4.2) and flags it for
P4's `decisions.md` (P4 owns the formal entry; this plan's §7 states the
hand-off). No edit to the shipped ADR file inside this branch (planning-doc
amendments are P4's ledger discipline).

## 5. Dependencies

1. **On P1, hard:** every §2 artifact (D1–D8). P2 composes onto a landed,
   pinned group branch — it does not build the loop, the policy, or the
   envelope.
2. **On v203 (shipped, frozen):** `pkg/authz` resolver/predicate;
   `models.GroupMembershipReader` expansion; migration 034's axis
   repartition; the structural-guard corpus
   (`authz_guard_test.go:281-413`).
3. **On P3 (parallel, soft):** NONE for composition. P3's FE scope UI needs
   no P2 artifact; the ADR-19 fold twin is P1's. P4 needs both P1 and P2
   green (Pack A/B full suite) before gates.
4. **Ordering inside P2:** ARM#1 first (it makes the arm safe standalone),
   ARM#2 (the copy), ARM#3 (the fork) — each pin lands with its arm; the
   guard extensions (§4.3) land LAST (they grep the final loop text).

## 6. Task breakdown (review-ordered)

| # | Task | Files | Acceptance |
|---|------|-------|------------|
| T2.1 | Replace the P1 scope seam: resolve-inside-arm + ARM#1 (group-id-in-reach, client-string 403) | `pkg/imggen/group.go`, `pkg/imggen/handler.go` (comment) | T1 + T8 green; entry gate for plain models untouched (existing 403/404 pins green) |
| T2.2 | ARM#2 member-exclusion copy + `scopeExcluded` | `pkg/imggen/group.go` | T2 + T5 green; policy builder file byte-unchanged |
| T2.3 | ARM#3 counting rule + empty-order fork | `pkg/imggen/group.go` | T4 green (403 not 502, zero hits); disabled-all ⇒ 502 pin green |
| T2.4 | Comment rewrites + envelope reuse verification | `pkg/imggen/handler.go` | Comments cite the decision pattern; `sendModelNotAllowedError` byte-identical (zero diff on `:750-760`) |
| T2.5 | Structural guards: imggen loop pin + W-1 arm STRING-PIN + numeric floor `>= 6` (the FINAL post-fold count — 5 today +1 arm; §4.3) | `pkg/proxy/authz_guard_test.go` | Guard green on the final text; RED on a deliberately-injected loop-time `.Allows(` and on a REMOVED arm-resolve (both negative controls proved once, locally) |
| T2.6 | T1–T8 battery + no-leak pair + through-decorator | NEW `pkg/imggen/authz_group_test.go` | Every added `func Test` shows literal `--- PASS`; env reuses P1's builder |
| T2.7 | ADR O1 supersession flag for P4 | plan note + commit message | P4 decisions.md TODO carried in the ship checklist (§7) |

## 7. Cross-phase couplings

- **To P1 (upstream):** §2 D1–D8. Any P1 change to the loop's structure
  (sentinel line, order-before-attempt, scopeExcluded plumbing) breaks P2's
  arms and the §4.3 grep — coordinate on the `for idx := range order {`
  sentinel and the seam block; both are contract surfaces named here.
- **To P3 (FE):** none required. P3's token-scope UI is lane-agnostic
  (v203 shipped it); an image-gen group id is selectable on the
  `allowed_groups` axis with zero FE change.
- **To P4 (ship):** (a) the ADR O1 supersession entry; (b) the resolve-floor
  bump (W-1: arm string-pin + floor `>= 6`, the final post-fold count) listed in the gate diff review; (c) T1–T8 in the Pack-B
  run (`pkg/imggen` is Pack B); (d) the 403-envelope bytes are a CLIENT
  CONTRACT — P4's release notes must state the imggen group 403 shape
  matches chat/decision lanes exactly; (e) the A-6 zero-engine-state-after-403
  assertion joins the T-battery here (P1 §8 cross-references it; no duplicate
  pin on the P1 side).

## 8. Test strategy

Inherits P1 §8's harness rules verbatim (Pack A/B split with the
module-prefix anchor; anti-vacuous `--- PASS`; gofmt zero-new-drift with the
flagged-LINE-SET method over ALL touched `_test.go`; HOME isolation; flake
registry — re-run, never chase). Additions:

- **Structural red-proof:** each guard extension (§4.3) is proved ONCE
  against a deliberately-broken copy (inject `.Allows(` after the loop
  sentinel; delete the arm's `authz.Resolve(`) — the guard must FIRE; then
  revert. The negative-control strings already in
  `authz_guard_test.go:318-324`/`:373-381` are the precedent.
- **Byte-envelope pin:** T1/T4/T6 assert the RAW body bytes of the 403
  (not just decoded fields) against the chat lane's fixture — the three
  lanes' 403 must be byte-identical (FE parses them identically).
- **Decorator-through (T7)** runs the REAL `*CachedModelsConfig` over a
  migrated SQLite stack — no fakes on the critical path
  (`models_group_lane_test.go:33-81` stack-builder shape).
- **No new migration, no store writes:** the reconciliation gate stays at
  88 SQL / 91 grep (P1 §2.2.2) — P2 adds zero schema surface.

## 9. Risks + mitigations

| # | Risk | Impact | Likelihood | Mitigation |
|---|------|--------|-----------|------------|
| R1 | The arm-forget-to-resolve hole: scope resolved ONLY at the entry gate would authorize the group id but NOT filter members (a token granted the group could reach out-of-scope members through it) | High | Low (pattern-pinned) | T2.1 resolves INSIDE the arm (v203 bypass-closed); T5 proves member-level exclusion; guard §4.3 pins no loop-time re-check |
| R2 | 403/502 conflation: an all-excluded group answered 502 leaks reachability semantics to clients and breaks the discriminator contract | High | Medium | ARM#3 counting rule on the UNFILTERED row (`:643-663` mirror); T4 asserts the 502 code ABSENT from the 403 body; disabled-all counter-case pinned |
| R3 | Copy-not-taken (in-place filter mutates the cached row) — cache corruption under the decorator | High | Low | `make(..., 0, len)` fresh-array law (PD-15 mirror); T7 through-decorator would surface aliasing as flaky cross-test state |
| R4 | Guard floor drift: a loose numeric floor (today's `>= 3` passes with 5 sites) lets a future removal of the ARM#1 resolve pass vacuously | Medium | Medium | W-1 (§4.3): STRING-PIN the arm's literal resolve text AND set the floor to the FINAL post-fold count `>= 6` (5 verified today + exactly 1 added) — a removed arm fails the guard BY NAME and by count |
| R5 | Loop-text sentinel drift: P1 renaming the loop opener breaks the §4.3 grep (guard goes red on a cosmetic change) | Low | Medium | Sentinel `for idx := range order {` is the same string the decision guard pins (`:310`) — naming it a contract surface in §7; a rename requires updating BOTH guards in one commit |
| R6 | ADR O1 left stale — future readers trust "no group surfaces" and re-narrow the lane | Medium | Medium | §4.5 supersession recorded in-code + P4 ledger TODO (T2.7); the comment cites the ADR line it supersedes |
| R7 | Claimed-id body leak: the 403 quoting the RESOLVED id would leak canonical ids for claimed requests | Medium | Low | PD-9 mirror: quote `req`'s client string, authorize `group.ID` (T1 asserts the quoted string is the client's) |

## 10. Rollback thinking

Phase 2 adds ZERO schema, ZERO store writes, ZERO new packages — it is
consumer-side composition + tests. Rollback is the release-level image
rollback (P4's ladder). On an OLD binary the composed arms vanish with the
group branch itself (P1 R6 verdict: benign-inert). No P2-specific data or
config exists to unwind; the W-1 guard changes (arm string-pin + `>= 6` floor) and T1–T8 ride the branch
and revert with it.

## 11. Success criteria (checkable)

| # | Criterion | Measure | Threshold |
|---|-----------|---------|-----------|
| 1 | Token granted the group (group axis) serves a member | T2 | 200, ≥1 hit |
| 2 | Token NOT granted ⇒ 403, zero upstream attempts | T1 | 403 + envelope bytes + hits==0 |
| 3 | Group granted but every base-servable member out of reach ⇒ 403 (never 502) | T4 | 403; `imggen_all_members_failed` ABSENT from body; hits==0 |
| 4 | All-disabled group (scopeExcluded>0) ⇒ 502 exhaustion | ARM#3 counter-pin | 502 + machine code |
| 5 | Excluded member never attempted, never named | T5 | 0 hits; id absent from body |
| 6 | Disabled-vs-out-of-scope byte-identical | T6 | Raw-body equality |
| 7 | Production decorator binding serves the group | T7 | 200 through `*CachedModelsConfig` |
| 8 | Chat/decision group on the lane ⇒ 400 misroute regardless of grants | T8 (wildcard variant too) | 400 + code; never 403/502 |
| 9 | Structural guards | §4.3 suite | Green on final text; fires on injected violations |
| 10 | Lane contract | raw-axis scan + W-1 arm string-pin + floor | Zero raw-axis reads; arm resolve pinned as literal text; `authz.Resolve` count ≥ 6 (final post-fold count) |
| 11 | Envelope parity across lanes | Byte pin vs chat fixture | Byte-identical 403 bodies |
| 12 | No authz/store/schema change | Diff review + migration gate | `pkg/authz` untouched; 88/91 unchanged |
| 13 | Zero engine state after every 403 (A-6) | Health-layer write counters (empty in v1) asserted after T1, T4, and the ARM#3 403 branch | All counters zero on every 403 path |
