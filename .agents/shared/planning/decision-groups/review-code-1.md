# Code Review — `feature/decision-groups` (pre-test gate)

**Target:** `feature/decision-groups` @ `6aedf00` — 17 commits (14 backend + 3 FE), 57 files, +8016/−253, **NOTHING PUSHED**
**Worktree:** `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-decision-groups`
**Diff reviewed:** `git diff a2952a9 HEAD` (anchor base = the branch's own commits); text-lane fence re-verified against **both** `2b987bf` and `a2952a9`
**Plan of record:** `.agents/shared/planning/decision-groups/` (phase1/2/3-plan.md, decisions.md)
**Mode:** READ-ONLY. No code change, no plan edit, no commit/push/deploy, no KB/POK write. All probes ran in `/tmp` sandboxes.

---

# VERDICT: 🔴 CRITICAL ISSUES — 5 blocking (3 implementation defects + 2 test-coverage holes)

**5 blocking · 16 warnings · 13 suggestions.** The architecture is sound and most of the surface is genuinely correct — but three settled-semantics defects let the health cache become the failure oracle, which is the exact thing R2/OQ-1/D-C forbid, and two of the corpus's load-bearing AM-4/R2 claims have **no discriminating test at all**.

**Gate disposition:** fix the 5 blockers, rule the 3 open questions, then **re-run this gate**. The FE corpus amendment needs **no** count change.

---

## 🔴 BLOCKING

### B1 — The exhaustion envelope fires without the corpus's exhaustion predicate → 502 while a member was *suppressed*, never *failed*

**Where:** `pkg/decision/group.go:255-258` (exhaust), `:232-253` (the OQ-1 lift), `:237` (its gate), `:170-173` (head capture), `:167-183` (the suppression branch)

**Contract:** `phase1-plan.md:1359` — the 502 "fires **only when every member × credential was genuinely attempted and failed**. **Suppressed attempts never count as failures**"; `decisions.md:907-910` (D-C) — "The cache is never a failure oracle **at ANY layer**".

**Failure mode (reproduced by two independent analyses, in separate sandboxes):**
- A group where one member genuinely fails and a second member is **suppressed** (its first credential is marked) → **502 `decision_all_members_failed`** with the body *"all decision group members failed (1 attempted)"*, while the suppressed member was **never attempted** (`hits(dec-b)=0`). Telemetry: `member_attempted dec-a → upstream_error → member_suppressed dec-b → all_members_failed{attempted:1, skipped:1}`.
- The same 502 occurs with a **MODEL-layer** member mark against a fully healthy credential — **no credential-loop fix can touch that path**.
- Mechanism: the group-level OQ-1 lift is gated on `attempted == 0 && headSuppressed != nil` (`group.go:237`), and `headSuppressed` is captured **only for `idx == 0`** (`:170-173`). Once any member has been genuinely attempted, the lift can never fire, and the exhaustion at `:255` fires on `winner == nil` alone. The suppressed member is never re-offered.

**The distinction that changes the fix:** the OQ-1 head-lift is implemented **faithfully** against the leader-ordered predicate `suppress(M) := marked(M) ∧ ¬(M = policy-head ∧ every-candidate-marked)` (`architecture-recommendation.md:660`). This is therefore an **exhaustion-predicate** violation, **not** an OQ-1 violation. **Gate the envelope; do not widen the head-lift rule.**

**Minimal fix:** emit `decision_all_members_failed` only when every policy candidate × credential was genuinely attempted-and-failed per AM-4. When the walk ends with a candidate that was only *suppressed*, grant it the one honest attempt the corpus requires (deterministic choice — **ruling Q1** below). Note `group.go:233`'s own comment already claims the wider rule, so either the comment or the code must move.

---

### B2 — The D-C escape grants one honest attempt **per credential**, not per member

**Where:** `pkg/decision/group.go:370-372` (escape evaluated per credential) + `:218-224` (AM-4 rotation) + `:395-414` (`memberFullyUnavailable` — a **pure state read with no per-member latch**)

**Contract:** `decisions.md:907-910` — "If every credential of a member is marked/cooling, the member gets **exactly ONE** honest attempt **on its policy-first credential**; if that attempt genuinely fails per the AM-4 class policy, **the member fails and the loop advances**."

**Failure mode (reproduced by two models in two sandboxes):**
- Member with 2 credentials, both cooling → honest attempt → **429** (limit-class ⇒ AM-4 rotates) → the escape fires **again** → **`hits(dec-a)=2`**, the second call on a **cooling** credential that is **not** the policy-first one.
- Stronger form: `#1 → 429`, `#2 → 200` ⇒ **two upstream calls on a 200 response**, the second against a credential known to be cooling. One avoidable, billed call.
- D-C's "the member fails and the loop advances" is **not implemented** — the code rotates and keeps going.

**Why the corpus missed it:** the D-C test (`pkg/decision/handler_group_test.go:1097-1155`) uses **503** — 5xx ⇒ no rotation ⇒ one hit observed. The limit-class path is uncovered.

**Minimal fix:** make the grant **member-scoped** (a per-member flag set when D-C honours a credential, so no second credential of that member receives the exemption); on a rotation-eligible failure of that single honest attempt, **end the member and advance** per D-C's own wording (depends on **ruling Q2**).

---

### B3 — Both admin-API `ValidateFallbackChainLanes` mirrors are **unreachable**

**Where:** `pkg/ui/server.go:1220-1229` (POST) and `:1677-1686` (PUT)

**Failure mode:** the POST block sits **inside** `if newModel.Kind == models.KindDecision {` (opens `:1167`), whose own D5 rule (`:1211-1218`) rejects any non-empty `fallback_chain` and **returns first** — so the mirror only ever sees an empty chain, and `models.ValidateFallbackChainLanes` returns `nil` immediately (`pkg/models/group_lane.go:165-167`). The PUT block sits **inside** `if updatedModel.Kind == models.KindImageGen {` (opens `:1637`), whose chain rule (`:1668-1673`) returns first — same dead path. **Proven empirically:** POST → **201 Created**, PUT → **200 OK** for a chat model whose `fallback_chain` names a decision group.

There are exactly **3** call sites in the tree: `pkg/ui/server.go:1220`, `pkg/ui/server.go:1677`, and `pkg/store/database/store.go:2106` (the live canonical chokepoint). **There is no top-level API call.**

**Blast radius, stated honestly:** with the production DB-backed models manager, `pkg/store/database/store.go:2105-2106` still rejects the write with 400, so **no live misroute hole exists in production**. What is broken: (a) the admin-API layer does not own the rule it declares it owns; (b) the helper≡POST≡PUT parity corpus is structurally blind to the `chains` field; (c) any non-DB binding (the JSON twin, the test mocks) accepts it. This is precisely the "one path accepts what another rejects" drift the parity discipline exists to prevent — and `chains` is one of the four fields the commission names.

**Minimal fix:** move both blocks **out** of the kind-specific branches to the top level of each handler (the same level as the group-block checks at `:863` / `:1424`), so they run for chat/`""` models; then add `TestKindRules_Parity_FallbackChain_DecisionGroup` (POST **and** PUT arms) so the rule is pinned at the API layer.

---

### B4 — "A SUPPRESSED attempt must NEVER count as a failure" is **unpinned**; the named pin contains a tautology

**Where:** `pkg/decision/handler_group_test.go:989-1029` (`TestDecision_Group_Exhaustion_ExcludesSuppressedAttempts`)

**Failure mode:** the test's two assertions are `attempted < 1` (**satisfied by 2** — cannot detect a wrong count) and `skipped < 0` (**tautological** — an int count can never be negative). Its fixture **never produces a suppression at all**: a single-credential member whose only credential is marked is in the D-C state, so the D-C escape handles it, not the suppression path. Measured event: `all_members_failed attempted=2 skipped=0 members_attempted=2`.

**Proof:** with the suppression branch also incrementing `attempted`/`membersAttempted` (i.e. a suppressed attempt counting as a failure — a direct R2 violation), **all 34 group tests and the entire `pkg/decision` package stay green** (`ok … 1.436s`).

**Minimal fix:** make `dec-b` genuinely suppressible — give it `ExtraCredentialIDs: []string{"cred-dec-b-2"}` and mark **only** `cred-dec-b` (so it is *not* in the D-C state) — then assert the **exact** numbers: `attempted == 1`, `skipped == 1`, `members_attempted == 1`; replace `skipped < 0` with `skipped != 1`.

---

### B5 — AM-4's "upstream 401/403 ⇒ rotate within the member" has **no discriminating test anywhere**

**Where:** `pkg/decision/handler_group_test.go:795-809` (`TestDecision_Group_401_RotatesWithinMember`); implementation `pkg/decision/group.go:615-622`

**Failure mode:** the test wires **no credential engine** and gives the member **one** credential, so a rotation is unobservable; its body only asserts that the walk advances after the member's resilience is consumed.

**Proof:** with `failure.rotate = false` on the 401/403 arm, the **entire `pkg/decision` package passes** (`ok … 1.474s`). No test, in this package or elsewhere, catches the removal — despite the test's name claiming the cell is pinned.

**Impact:** an AM-4 row of the settled class table can be silently deleted without a red gate. The shipped behaviour is correct today; the risk is undetected future regression of member-level resilience.

**Minimal fix:** mirror the limit-class pin at `handler_group_test.go:1325-1386` — give the member a second credential (`ExtraCredentialIDs: []string{"cred-dec-a-2"}`), wire `SetCredentialEngine(engine)`, and assert `engine.excludedPair("dec-a","cred-dec-a") == true` plus `hitsFor("dec-a") == 2`.

---

## 🟡 WARNINGS (non-blocking; ordered by area)

**Y1 — The suppression `break` abandons the member's remaining credentials — and the obvious fix is a measured no-op.**
`pkg/decision/group.go:182` — a marked *first* credential suppresses the whole member; its other (available, healthy) credentials are never attempted, so the member's resilience is never consumed. This is the **root cause of B1**.
⚠️ **Correction to the first-pass proposal:** changing `:182` from `break` to `continue` **does not fix it** — the change was applied in a sandbox and **measured**: the probe result was identical (502, `hits(dec-b)=0`, `skipped:1`) and the full 74-name suite stayed green. Re-entering the loop calls `safeResolveCredential` again, which **re-offers the same credential** — the credential engine knows nothing about the skip-cache mark, and a suppression deliberately writes **no** engine state. `triedPairs` then ends the member.
*Minimal fix:* the marked credential must stop being **offered** — an exclusion must be written at the credential-selection seam (`ExcludeAndReselectForClass`, the seam rotation already uses at `group.go:419-426`). That write needs **ruling Q3**.

**Y2 — The OQ-1 lift's honest attempt is not counted when it fails.** `pkg/decision/group.go:238-251` — the success branch increments `attempted`/`membersAttempted` (`:241-242`), the failure branch does not ⇒ the exhaustion event reports `attempted=0, members_attempted=0` and the client body reads *"…(0 attempted)"* although one upstream call happened. *Fix:* increment both before the attempt, mirroring `:185-186`.

**Y3 — `members_attempted` counts member-*attempts*, not distinct members.** `pkg/decision/group.go:186` — incremented inside the credential loop, so a 2-credential member counts twice (observed: "2 members attempted" for one member). The corpus defines it as *distinct members attempted* (`phase1-plan.md:1339`). *Fix:* keep a `map[string]bool` of attempted member IDs.

**Y4 — AM-4's client-cancel arm is not implemented on this lane.** `pkg/decision/group.go:523` — the attempt context is `context.WithTimeout(context.Background(), …)`, so a client disconnect is invisible: the upstream call runs to completion and the lane keeps walking members after the client hung up. The text lane's mirror (`pkg/proxy/cred_failover_class.go:190-193`) treats a cancelled context as **advance-blocking**. The status set `{400,413,415,422}` matches the text lane exactly (`:627-628`) — only the cancel row is missing. ⚠️ This one is a **ruling**, not a plain bug: the house rule forbids the lane from using `r.Context()` (`pkg/proxy/handler.go:432-434`) so a disconnect cannot kill an already-billing call. *Fix if ruled in:* observe the cancel at the loop boundary (`r.Context().Err() != nil` before each member attempt) while keeping the attempt context detached.

**Y5 — The D-C precondition reads a narrower mark space than the suppression reader.** `pkg/decision/suppression.go:89-97` (`credentialScopeMarked` — credential scope only) vs `:68-79` (`:75` — credential scope **and** provider scope). A **provider-scoped** mark can therefore suppress a member's **only** attempt with no D-C relief (observed: a healthy member, same provider + row internal model, suppressed and never attempted ⇒ the request 502'd). *Fix:* either widen the D-C reader to the provider scope, or stop consulting the provider scope in `credentialMarked` (on the text lane the provider scope is an **ordering input only, never an exclusion**). Needs a decision.

**Y6 — Non-OpenAI-shaped error bodies lose the quota vocabulary ⇒ an AM-4 classification divergence.** `pkg/decision/openaidecision.go:169`, `pkg/decision/typesafe.go:440`, `pkg/decision/group.go:586-595` — both adapters populate `ErrorType/ErrorCode/ErrorMessage` only when the body parses as `{"error":{…}}` **with a non-empty message**; otherwise the Outcome carries no vocabulary. The text lane keeps it (`pkg/providers/interface.go` → `ProviderError.Message` falls back to the raw body) and `pkg/credclass/classify.go:156-162` explicitly relies on that carrier. Observable: a **429 whose body says "token plan"** arrives non-OpenAI-shaped ⇒ classified **rate_limit (60 s cooldown)** instead of **quota (15 m)** — the exact case the AM-4 rule names. *Fix:* in `attemptDecisionMember` (`:567-586`), fall back to the raw body as the `message` fact when the Outcome carries no vocabulary.

**Y7 — The lane fold bypasses the modelscache decorator and is computed twice per group entry → N+1 raw SQL on a per-request hot path.** `pkg/modelscache/models.go:1112-1121` → `pkg/store/database/store_group_rewrite.go:369-387` → `pkg/store/database/store.go:1050-1054`; consumed at `pkg/proxy/handler.go:413-427`. `inferGroupLane` calls the **raw** `GetModelNative` (a `SELECT … FROM models WHERE id = ?`) **per member**, bypassing the decorator — and the *same* fold runs **twice back-to-back per group entry** (`:74` computes it, then `:162` recomputes it inside `pickServableGroupMember`). Cost: ~2N redundant DB round-trips per group entry (N ≤ `MaxGroupMembers` = 16), plus 1+N per `groupLaneOf` guard call — on `/v1/models`, per chat request (`handler.go:674`) and per Anthropic request (`handler_anthropic.go:302`). Secondary effect: the listing is computed from **cached** rows while the lane comes from the **DB**, so the two can disagree for up to a reconcile interval. The codebase already documents this exact shape as a regression to avoid (`pkg/modelscache/models.go:1132-1137`). Semantics are unchanged — this is a **cost** regression, not a correctness one. *Fix:* (a) compute the lane once in `RewriteGroupEntries` and pass it into `pickServableGroupMember` (private signature change, zero behaviour change); (b) resolve member rows in one query (`WHERE id IN (…)`); (c) optionally a short-TTL lane cache.

**Y8 — The mixed-lane guard arm can never fire; the operator gets the wrong error class.** `pkg/proxy/handler.go:672-690`, `pkg/proxy/handler_helpers.go:929-957` — the second arm requires `laneOK && lane == models.GroupLaneChat && groupHasDecisionMember(...)`, but a **mixed** group has `laneOK == false` (the fold is not unanimous, `pkg/models/group_lane.go:95-101`), so *neither* arm fires. The request dies later at the rewrite boundary ⇒ **502 `group_unservable`** ("group 'X' has no servable members") instead of the loud 400 `decision_misroute`. It never serves silently ✔ — but the class is misleading. *Fix:* base the second arm on `groupHasDecisionMember(...)` alone.

**Y9 — A mixed-lane group is listed but never servable (the literal v193-class shape).** `pkg/proxy/handler.go:413-427` vs `pkg/proxy/handler_anthropic.go:296-330` — the listing excludes a group only when `lane == decision` (`ok && lane == GroupLaneDecision`); a mixed group has `ok=false` ⇒ it **is** listed. Reachability is narrow (all three write mirrors + the canonical seam reject mixed groups; ADR-1 blocks retyping a member in place) — it needs a hand-edited row — but it is exactly the shape the commission asks to be pinned. *Fix:* exclude when `lane == decision` **or** `groupHasDecisionMember(...)`; add a listing test over a hand-written mixed row.

**Y10 — The vocabulary pin's PUT surface is unpinned — in *both* pins.** `pkg/decision/vocabulary_pin_test.go:63-73` — the `post_mirror` and `put_mirror` entries carry the **same needle on the same file** (`pkg/ui/server.go`), so `strings.Contains` cannot detect one-sided drift. `pkg/ui/kind_rules_parity_test.go:1064-1066` asserts only `status == 400`. **Proofs:** deleting the PUT D4 gate leaves the source-literal pin **green**; disabling it (`if false`) leaves the runtime pin **green** too — the PUT then answers `400 "model not found"` (the model is never seeded in the mock), i.e. a status-only assertion satisfied by an unrelated 400. The POST arm *is* discriminating (removing it ⇒ POST returns 201 ⇒ FAIL). Production is still protected by the canonical rule — the hole is in the **test**. *Fix:* seed `dec-1` in the mock before the PUT and assert the **error substring**; split the source-literal pin's POST/PUT needles into two distinct literals.

**Y11 — The chat lane's primary decision misroute publishes no `decision_misroute` telemetry event.** `pkg/proxy/handler.go:675-681` returns the loud 400 but does **not** publish; the event fires only in the mixed arm (`:684`). Both Anthropic arms publish (`pkg/proxy/handler_anthropic.go:285`, `:311`, `:323`). `decisions.md:915-916` requires "loud 400 `decision_misroute` **+ a telemetry event**". One-line fix.

**Y12 — A stored *inactive* decision member renders as an EMPTY/unpicked row in the edit form.** `pkg/ui/frontend/src/components/config/GroupMemberEditor.tsx:57-59` (filter), `:111` (selected lookup), `:125-132` (option rendering). The decision arm filters candidates to `kind === 'decision' && enabled`, but the server's write model does **not** require members to be enabled (`pkg/models/group.go` `ValidateGroupMembers` — existence / `weight>0` / duplicates / flatness / lane-homogeneity, **no enabled clause**) and disabling a member is not blocked (`pkg/store/database/store.go:1706` guards only the *kind* change). So an operator who disables a decision model that is a group member sees a member row that looks unpicked. The chat lane renders the same case correctly (`select.value === "chat-off"`). Not data loss (the mirror blocks a truly empty row) — a display-correctness defect on a legal configuration, and the one class the corpus never pins. *Fix:* always offer the currently-selected member, even when it is not in the candidate list (render it `disabled` and labelled `<name> (<id>) — inactive`); add a case for it.

**Y13 — One historical text-lane error message changed, and the branch's own comment is now inaccurate.** `pkg/models/group.go:229` (+ the UI twins `pkg/ui/server.go:962`, `:1530-1532`) — for exactly one input shape (a **chat** group containing a **decision** member) the new mixed-lane message replaces the historical per-member chat message. Proven empirically by an overlay probe, base vs HEAD: base → *"member \"dec-model\" must be a chat-kind model (got kind \"decision\")"*; HEAD → *"members must all share one kind (chat or decision) — member \"dec-model\" has kind \"decision\""*. All six other probed shapes are byte-identical. The **rejection outcome is unchanged (fail-closed)** — only the wording moved. This contradicts the branch's own comment at `pkg/models/group.go:118-121`: *"the CHAT wording is byte-identical to the historical text (the text lane's messages are untouched)"*. Not part of the v192/v193 auth fix nor the v194 fallback fix, and no pre-existing test asserted the old wording. *Fix:* either scope the comment honestly, or preserve the historical text lane by emitting the per-member wording when `groupLane == GroupLaneChat` in `pkg/models/group.go:229` **and** its two UI mirrors.

**Y14 — The OQ-1 lift branch has no discriminating test.** `pkg/decision/handler_group_test.go:934-957` — disabling the head capture (`group.go:170`) leaves the suite green, because in the single-credential all-marked fixture the **D-C escape** already yields the honest attempt. The branch *is* reachable (a two-credential fixture produced `[member_suppressed, member_suppressed, success]` with exactly 1 hit) — but no test uses that shape. Coverage, not behaviour. *Fix:* add a subtest with `ExtraCredentialIDs` on both members (first credential marked only) asserting `totalHits() == 1 && hitsFor("dec-a") == 1`.

**Y15 — The named 5xx AM-4 pin does not test the rotation it names.** `pkg/decision/handler_group_test.go:715-739` — with 5xx ⇒ rotate, the named test still passes (no engine wired; asserts only mark absence + hit counts). The semantic *is* pinned, by `handler_group_test.go:1074-1092` (`NoCooldownWriteOnSuppressedPick`) and `:1388-1409` (`MemberFailedPredicate_OwnResilienceExhausted`), which wire the engine and assert `engine.excludedPair(...) == false`. *Fix:* wire the stub engine in the named test and assert `excludedPair("dec-a","cred-dec-a") == false` (or rename it `…_WritesNoMark`).

**Y16 — Corpus per-file attribution drift (documentation only; the operative gate passes).** `phase3-plan.md:786` ("69 ADDED … 60 Pack A + 9 Pack B"), `:788` ("Pack A = 41 · Pack B = 9"), `:388`/`:501` (the G4 ledger attributes a test to `store_group_test.go` that actually lives in `models_manager_kind_test.go`), `:840` (header says 16, list holds 17), `:866` (header says 5, list holds 6); `phase2-plan.md:803`/`:898` say 47 while `phase3-plan.md:1019-1028` (the gate) says 48 and `phase2-plan.md:441` defines E8. The totals are right (74 = 64 + 10; FE 48) and the gate is **set-based** (`comm` in both directions), so it passes — but the prose contradicts the diff and itself. *Fix:* fold these corrections into the in-flight corpus amendment.

---

## 🟢 SUGGESTIONS (non-blocking)

- **S1 — `normalizeKind` is a dead exported normalisation** (`pkg/ui/frontend/src/components/config/groupLane.ts:31`, tests-only) whose name invites the exact mistake commit `6aedf00` fixed. `ModelKind` (`:12`) exists only as its return type and `GroupLane` (`:7`) is re-exported but every consumer imports from `../../types`. Unexport/delete, or rename to `legacyKindOnly`. (Also raised by the council.)
- **S2 — `A4`'s assertion is vacuous in its own context** — `pkg/ui/frontend/src/components/config/__tests__/DecisionModelsContainer.test.tsx:107-115` asserts `div.flex.overflow-x-auto > button` count = 0, but the container is rendered standalone so the selector can never match; the real guarantee is structural in `SettingsPage.tsx:653+` and is pinned by no test.
- **S3 — The FE mirror lacks the server's non-group guard** — `groupLane.ts:136-154` vs `pkg/store/database/store_group_rewrite.go:370-372` (`if group == nil || !group.IsGroup()`). Not a live divergence (every FE call site passes a group row), but the mirror is not literally function-for-function as ADR-19 demands.
- **S4 — Dead fields in the decision core.** `pkg/decision/group.go:67` — `memberFailure.body` is declared, never written, never read (the fail-fast path uses `memberAttempt.body`, `:339`). `pkg/decision/group.go:580` — `Outcome.UpstreamStatus` is written but never read (classification uses `resp.StatusCode` at `:586`), while `pkg/decision/converter.go:33-36` claims "the Outcome is the material the lane classifies on (status + error envelope)".
- **S5 — Eight unused exported `pkg/credclass` symbols, and the vocabulary tables are mutable exported slices** — `pkg/credclass/classify.go:27,35,39,45,63-73,74,100`. `QuotaCodeVocabulary = QuotaTypeVocabulary` **shares one backing array**, so any future external caller could mutate the shared vocabulary at runtime and silently change classification for **both** lanes. Unexport the tables (or expose accessor funcs) and keep only `IsQuota`/`IsRateLimit` exported.
- **S6 — The "ONE mark-key chain" comments invite a unification that would break semantics.** `pkg/decision/group.go:448-461` and `pkg/decision/suppression.go:12-22` describe a single chain (`row.InternalModel → memberID`), but the **MODEL layer** legitimately uses a different key space (the member **row ID** — `suppression.go:131-133` write, `:105-110` read). State the two key spaces explicitly, and note that the chain key is the row's internal model while the wire model may be the resolved credential's peak-hour substitute (marks then do not collide with the text lane's marks for the same credential during a peak window).
- **S7 — godoc typo** — `pkg/models/group_lane.go:89`: `// UnanimousGroupLane folds the given member kinds into the single lane// they share` (missing space → renders as `lane// they share`).
- **S8 — `Config` loses its godoc** — `pkg/proxy/handler.go:76-80`: the `var _ groupLaneReader = (*modelscache.CachedModelsConfig)(nil)` assertion was inserted **between** the `// Config holds runtime configuration for the proxy handler` comment and `type Config struct`.
- **S9 — Duplicated compile-time assertion** — `pkg/proxy/handler_anthropic.go:83-87` repeats the assertion from `handler.go:80` (one per package is enough) and splits the `────` section banner from the `// Anthropic Messages API Handler` banner.
- **S10 — Inaccurate log wording** — `pkg/store/database/store_group_rewrite.go:164` logs `unresolvable member lane (mixed kinds)` for every unresolvable lane, including the "no valid lane at all" case.
- **S11 — Unsatisfiable error wording** — `pkg/models/group.go:243` (and the UI twins) word the invalid-kind message with the group's lane, so a group mixing an image-gen member with a decision member is told it "must be a **decision-kind** model (got kind \"image-gen\")" — an instruction that can never be satisfied. The text lane's byte-identical wording still holds (clarity, not drift).
- **S12 — Record the ADR-1 status class as deliberate** — `pkg/ui/server.go:1404-1411` / `pkg/store/database/store_kind_guard.go:49` use **400**; the house "resource in use" precedent is **409** (`pkg/ui/server.go:1833`, `:2733`), but 400 is this handler's dominant validation family and no client branches on the class. Worth a doc comment, not a change.
- **S13 — O(n) work per attempt** — `decisionMarkKey → memberRow → currentModelRows → GetModels()` copies the whole model set per credential attempt (`pkg/decision/group.go:395-414`, `:430-480`).

---

## ✅ VERIFIED PASS (cleared — do not re-litigate)

**Settled semantics that are genuinely implemented and pinned:**
- **Layer isolation is structurally sound.** `pkg/decision/policy.go:20-24` imports only `math/rand` + `pkg/models` — the pick is a pure function of pools/weights/order, so **no credential- or model-layer state can reach it**. The layer fence is mechanical, not conventional. The MODEL layer's absent CHAIN reader is honestly **recorded as absent**, not faked (`suppression.go:16-20`, `:99-110`).
- **The mark-key chain is exactly one chain.** One helper (`pkg/decision/group.go:475-480`), **five** mark sites (write limit-class `:166`/`:202`, honest-attempt head `:245`, win-clear `:262`, per-credential read `:167`/`:359`, D-C read `:400`/`:408`), `row.InternalModel → memberID`. **No site derives a key from a credential.** The `mark_key_equality` subtest **does discriminate** — the old cred-derived write key fails it (through the `degenerate_row` case; the `validated_row` case is a correct non-discriminating control).
- **The doctrine is pinned verbatim at the seam** — `pkg/decision/suppression.go:8`; a one-word change breaks the pin.
- **The decorator-forward trap is closed** — forward at `pkg/modelscache/models.go:1112`, a true decorator-through test at `pkg/modelscache/models_group_lane_test.go:89-109` (raw vs cached, both asserted), plus compile-time assertions at `pkg/proxy/handler.go:80`. Removing the forward breaks the build *and* the test.
- **ADR-1 kind-retyping has no bypass** — canonical guard `pkg/store/database/store.go:1706-1713` → `pkg/store/database/store_kind_guard.go:33-53` (compares **effective** kinds, so `chat → ""` is not a retyping); API mirror at `pkg/ui/server.go:2767-2793` runs before the credential gate; a PUT omitting `kind` on a member is correctly refused (the PUT is a full replace).
- **The misroute guards discriminate** — chat and Anthropic guards and the `/v1/models` exclusion all fail loudly under mutation.
- **AM-4 rows pinned:** limit-class ⇒ rotate+mark ✔; 5xx ⇒ advance, no rotation/mark/cooldown ✔ (pinned by `NoCooldownWriteOnSuppressedPick` + `MemberFailedPredicate_OwnResilienceExhausted`); 400/413/415/422 ⇒ fail-fast ✔.
- **The credential/vocabulary move out of `pkg/providers` is faithful** — table-by-table identical (status 429/402, 3 rate-limit codes, 7 quota type entries, 13 quota message entries, message not consulted by rate-limit), `pkg/providers` tests byte-identical to both bases and green, `pkg/credentiallb` and `pkg/proxy/cred_failover_class.go` untouched.
- **Pre-commit guarantee holds** — the only client writes are `commitDecisionWinner` (`:279-310`), `failFastDecisionGroup` (`:332-342`), `exhaustDecisionGroup` (`:325-327`), all after the walk; no usage row is metered on exhaustion or fail-fast.
- **Win-clear is correct** — `group.go:262` clears the winner's upstream credential/provider scopes + the model-layer row id, keyed by the **same** `decisionMarkKey` the write side uses ⇒ equal by construction; the failed head's marks survive.

**The text-lane fence HOLDS:**
- Tier-1: all **11** pin files byte-identical vs **both** `2b987bf` and `a2952a9` (22/22 cells EMPTY).
- Tier-2: **10** files × **2** bases — no removed/renamed test function, no removed or modified assertion-bearing line (every file is **pure addition**, 0 deletions).
- Repo-wide: **2377 → 2451** test names, **74 added**, **0 removed or renamed anywhere** — matching the corpus's "74/74".
- The v192/v193 auth fix is untouched: `pkg/proxy/handler_anthropic.go` has **zero removed lines**; the auth markers (`IsModelAllowed`, `authToken == nil`, 401, 403) are byte-identical base → HEAD. The anonymous `/v1/messages` hole stays closed.
- The v194 skip-cache pins are byte-identical and green (5/5, raw `--- PASS` shown).
- `gofmt`: repo-wide flagged set **identical** at HEAD and both bases (47 = 47 = 47); **zero** touched files flagged; the 11 files swept by `f5ade55` and reverted by `9150400` show a net diff of 0 each.
- Commit hygiene: **17 commits, single linear chain, zero merges**; each FE commit ships the regenerated `pkg/ui/static` bundles **in the same commit** as its source, and every asset referenced by that commit's `index.html` resolves inside it; the committed bundles are **byte-identical** (sha256) to a fresh rebuild of the committed source.
- Not pushed: **0 of 17** SHAs appear in any live remote ref.
- `git status --porcelain` = exactly the three expected session artifacts; nothing partially staged; no source/test/asset left uncommitted.
- Diff hygiene: no secret values, no debug prints in production code, no TODO/FIXME/XXX/HACK, no commented-out code blocks.

**The frontend is sound:**
- The vertical-nav contract is met without any ARIA-tablist misuse — native `<button type="button">`, `aria-current` only on the active one, no `role=`/`aria-selected`/`tabindex` in the file.
- Two child tabs, **models** default, group panel swaps; `DecisionModelsTab.tsx` is **byte-identical** to the base.
- One lane-parameterized component tree — **no fork** of the text-group UI; the `lane` prop is required, so a missed call site is a compile error.
- Candidate eligibility lives in **exactly one place**; the chat arm is **character-for-character** unchanged; `laneModels` untouched (still the shadow picker's third population); **no member `"enabled"` key** in any payload; single-member groups submit.
- **The lane-fold mirror is exact on all ten branches** of the server's `MemberLane`/`GroupLanesOf`/`UnanimousGroupLane` fold, and the `6aedf00` unknown-kind fix is **correct and complete** — `grep -rn normalizeKind src/` finds only the definition and its own test.
- FE gate: vitest **328/328**, zero `×`; tsc **19 errors / 9 files** = the frozen baseline exactly; case-token gate **52/52** with no collisions; G11 rebuild byte-identical; embed references clean.

**Counts reconcile:** 74 Go names = **Pack A 64 + Pack B 10**, matching the corpus with the bidirectional `comm` **empty in both directions**; 5 WITHDRAWN names absent; FE **48** canonical tokens each matching exactly one title (executed as 49 `it()` cases because D6 has five variants).

---

## ❓ OPEN QUESTIONS FOR THE LEADER (these block the B1/B2 fixes)

1. **Q1 — Mixed-case honest attempt.** When the walk ends with a suppressed (not failed) candidate, which candidate receives the honest attempt — the first suppressed candidate, or the policy head's policy-first credential? ADR-11 names only the all-marked head-lift, so extending it is a **ruling change**. **B1's fix depends on this.**
2. **Q2 — D-C vs AM-4 precedence.** Does D-C's "the member fails and the loop advances" override AM-4's rotate-within-member for the limit class on this lane? The corpus text says yes (ADR-15, later) over AM-4 (ADR-13, earlier). **B2's fix depends on this.**
3. **Q3 — Credential-exclusion write.** May the decision lane write a credential-layer exclusion (`ExcludeAndReselectForClass`, `pkg/decision/group.go:419-426`) when it **reads** an upstream-layer mark? ADR-12's writer table (`decisions.md:825-833`) covers credential-failure classification as a *writer*, not mark-reads. If **no** → keep the current member-exit behaviour, document it as deliberate, and fix only the exhaustion envelope (B1) — leaving Y1 documented.

---

## EVIDENCE BASE, DISSENT RECORD, AND WHAT WAS NOT CHECKED

**Dispatch (5 workers + 1 governor council; fan-in via todo graph, all nodes closed):**

| Node | Skill | Scope | Result |
|---|---|---|---|
| `review-worker-decision-core` | `code-review` | `pkg/decision/*` + `pkg/credclass` + the providers vocabulary move | 2 🔴 · 6 🟡 · 3 🟢 · 174 tests pass, `-race` clean |
| `review-worker-lane-store-api` | `code-review` | lane machinery, store, admin API, proxy guards, `cmd/main.go` | 1 🔴 · 4 🟡 · 6 🟢 |
| `review-worker-frontend` | `Mirror a server fold in the frontend (llmproxy)` | `pkg/ui/frontend/**` | 0 🔴 · 1 🟡 · 3 🟢 |
| `review-worker-textlane` | `pr-review` | text-lane fence + diff hygiene | 0 🔴 · 2 🟡 · 1 🟢 |
| `review-worker-test-quality` | `regression-test-nontautology-proof` | 15 sandbox mutation proofs | 2 🔴 · 4 🟡 · 1 🟢 |
| `review-council` (governor; `agentic` + `coding`) | `ensemble-adversarial-diff-review` | whole-feature adversarial pass | **CRITICAL ISSUES**, 2 must-fix, 7 concerns, 3 questions |

**Dissent record (surfaced, not laundered):** at council round 0 the `agentic` councilor returned **CRITICAL** (C1/C2/C3) and the `coding` councilor returned **APPROVED** with the D-C latch rated a non-blocking *concern*. In round 1 each was asked to **disprove** the other: `coding` reproduced the defect and **reversed to CRITICAL**; `agentic` **retracted two of its own three findings** after measurement and re-grounded the third on a stronger citation. Both must-fixes are now reproduced by two different models in two different sandboxes, and both are independently confirmed by a separate worker. **Resolution:** the blocking rating stands — with the explicit note that the `coding` councilor saw the D-C latch but **missed** the blocking half of the exhaustion defect (it caught only the cosmetic counting half), and that its "the OQ-1 head-lift is wrong" framing was retracted in favour of the exhaustion-predicate framing adopted above.

**Corrections made during aggregation (findings kept, proposals replaced):**
- W1 proposed changing `pkg/decision/group.go:182` from `break` to `continue`; the council **measured that change to be a no-op** and the correct fix requires an exclusion at the credential-selection seam (Y1, Q3).
- W1's F7 (provider-scope mark asymmetry) and the council's concern #6 are the same defect → one entry (Y5).
- W2's cache-bypass finding and W5's redundant-fold finding are one root cause → one entry (Y7).
- W2's vocabulary-pin finding, the council's concern #3 and W4's F5 are the same defect → one entry (Y10).
- W1's F4 and the council's concern #5 are the same defect → one entry (Y2).
- W3's F2 and the council's OPTIONAL are the same → one entry (S1).
- W3's F1 (inactive member renders empty) was confirmed against the server write path: `ValidateGroupMembers` has **no `enabled` clause**, and `pkg/decision/policy.go:74-76` excludes inactive members from candidates — so the FE mirrors the server correctly and the defect is display-only (Y12).

**Claims verified by the reviewer directly in-source** (not taken on trust): the `break` at `group.go:182` and `attempted++` after it (`:186`); the OQ-1 gate `attempted == 0` at `:237` and `headSuppressed` captured only for `idx == 0` (`:170-173`); the exhaustion at `:255-258`; the D-C escape as a pure state read at `:370-372` / `:395-414` and the credential-scope-only reader at `:377`; all **3** `ValidateFallbackChainLanes` call sites and the brace nesting that makes the two API mirrors unreachable; the DB chokepoint at `pkg/store/database/store.go:2105-2106`; `policy.go:74-76` (inactive ⇒ never a candidate); `ValidateGroupMembers` (no enabled clause) and `pkg/store/database/store.go:1706` (kind-only guard); `pkg/modelscache/models.go:1112-1121` (forward to the raw inner manager); `store_group_rewrite.go:369-387` (per-member raw `GetModelNative`); `handler.go:413-427` (listing) and `:675-681`/`:684` (no event on the primary misroute arm) vs `handler_anthropic.go:285`/`:311`/`:323` (events present); `pkg/decision/handler_group_test.go` (~`:1027`) asserting `skipped < 0`; `pkg/models/group.go:118-121` + `:229` (the inaccurate byte-identity comment); `pkg/decision/group_lane.go:164-186`.

**NOT checked by any reviewer in this gate:** live upstream behaviour; deployment / staging / docker-ci / image verification; the Postgres path (SQLite only); the **full** Pack A / Pack B suites (only the 7 packages holding added tests were run with `-run` filters); live E2E (`live_decision`-tagged tests not run); `CACHE_LAYER_ENABLED=OFF` runs of the group path (both bindings read, neither executed); the `pkg/credentiallb` TOCTOU flake (pre-existing, untouched by this diff); the **content** of the in-flight corpus amendment (only confirmed to touch the FE fold + the token count — it does **not** touch the OQ-1 or D-C text); whether the proposed fixes keep all 74 Go tests green (**no fix was applied** — read-only gate).

**Gate discipline:** no file in the worktree was modified, staged, committed or pushed; `git status --porcelain` is unchanged from the pre-review state; all sandbox work was confined to `/tmp` and removed. The only writes made by this review are **this artifact**.

---
---

# DELTA RE-CHECK — closure verification of the 5 blockers (APPENDED; the original verdict above stands unchanged)

**Delta verified:** `6aedf00` → **`5362ec0`** — 7 review-fix commits (`484ace5` B1+B2 · `3ea10da` B3 · `4a199a3`+`de9ed1d` B4+B5 · `1a7c78a` R2 · `3f458b8` R3 · `5362ec0` AC-R10), 4 files, **+315/−70** (`pkg/decision/group.go`, `pkg/decision/handler_group_test.go`, `pkg/ui/kind_rules_parity_test.go`, `pkg/ui/server.go`). Everything outside this delta stands as verified in the original review above and was **not** re-litigated.
**Mode:** read-only; every mutation/probe ran in `/tmp` copies (`tar --exclude=.git` / `git archive`); the worktree was never written (`git diff HEAD --stat -- pkg/ cmd/` = 0 lines before and after).

## VERDICT: 🔴 REMAINING CRITICAL — B2 is only PARTIALLY fixed, plus one schema defect in the accepted event addition

**4 of the 5 blockers are genuinely closed and mutation-proven.** B2 is closed for the **marked** shape but the **cooling-only** shape — the shape this review's B2 explicitly reproduced — is **unchanged from `6aedf00`** and still bills twice. The corpus's new control cannot see it.

---

## ✅ CLOSED (mutation-proven, not marked)

| Blocker | Status | Mutation evidence (raw RED reproduced independently) |
|---|---|---|
| **B1** exhaustion envelope gated | **CLOSED** | Gate at `pkg/decision/group.go:271-274`; `hasSuppressedOnly` set `:173`, `lastFailure` only from a **genuine** failure (`:205-206`, `:250-251`). Reviewer's own mutation (gate disabled) → `--- FAIL` `handler_group_test.go:1096` *"the exhaustion envelope must NOT fire when a member was only suppressed"*; unmutated → PASS. The head-lift predicate at `:243` is **byte-identical** to `6aedf00` (NOT widened); Q1's first-fully-unavailable rule lives in `attemptSuppressed` `:400-410` — probe: `dec-a=1, dec-b=0, dec-c=1` (exactly one honest attempt, later fully-unavailable members skipped **zero-cost**). |
| **B3** admin-API chain mirrors unreachable | **CLOSED** | `pkg/ui/server.go:1091` (POST, under only `func handleModels:761` → `case MethodPost:824`) and `:1642` (PUT, under `func handleModelDetail:1333` → `case MethodPut:1370`) — **no kind branch encloses either**. Empirical matrix: chat+decision-group chain → **400** (was 201/200); chat+chat-group → 201; decision+chain → 400 (kind rule); image-gen+chain → 400. Store chokepoint **retained** at `pkg/store/database/store.go:2106` (untouched). Parity subtest `kind_rules_parity_test.go:939`: deleting both API blocks → RED at **`:967`** *"POST: status = 201, want 400"* and **`:991`** *"PUT: status = 200, want 400"* — the exact dev-claimed lines, and the harness's mock is validation-free so the API site is the only enforcement there (a true control). |
| **B4** vacuous exhaustion test | **CLOSED** | `handler_group_test.go:1041-1117`: the `skipped < 0` tautology is **gone**; now asserts from the **event payload** — `foundEnvelope == false` (`:1095-1098`), `member_attempted == 1` (`:1102-1104`), `member_suppressed == 1` (`:1106-1108`), fail-fast counters **exactly 1/1/1** (`:1110-1116`). Mutation M6 (suppressed counted as attempted) → RED `:1111` *"fail-fast attempted = 2, want 1"*; mutation M4 (envelope ungated) → RED `:1096`. Fixture marks **only** `cred-dec-b` with a second credential present — genuinely suppressible, not D-C. |
| **B5** 401/403 rotate-within-member unpinned | **CLOSED** | `handler_group_test.go:810-862`: stub engine wired (`:816`, `:823`), member carries 2 credentials (`ExtraCredentialIDs`, `:816`), a resolver that returns the second credential after rotation, and assertions at the **engine's exclusion seam** — `engine.excludedPair("dec-a","cred-dec-a") == true` (`:852-854`) **and** `hitsFor("dec-a") == 2` (`:855-857`). Mutation (`failure.rotate = false` on the 401/403 arm, `group.go:655-660`) → RED at **`:853`** *"the first credential must be excluded (rotated) on a 401"* — the exact dev-claimed line. |
| **R1** 7 deleted tests restored | **CLEAN** | 34 top-level `func Test` at HEAD == 34 at `6aedf00`; **0 deleted, 0 added** (name contract preserved). Exact body compare vs `6aedf00`: all 7 restored functions **byte-identical**. Only **3** bodies differ, all legitimate B-fix targets, and all three are **strengthened, not weakened** (the old `401_RotatesWithinMember` body survives verbatim as the `single_credential` subtest). |
| **R2/R3** counters published + pinned | **CLEAN** | Fail-fast event carries the counters; pinned from the payload at `:1110-1116`; mutation dropping the fields → RED `:1108`. |
| **Additive event** | **ADDITIVE ✔** (2 🟡 below) | Existing `eventTypeDecision` const (`pkg/decision/handler.go:98`); key-universe diff over **all 8** publishers: **no new key, no field removed or renamed**; the `outcome` value comes from the pre-existing closed `classifyMemberFailure` vocabulary already published at `group.go:210-217`. Consumers verified repo-wide: SSE relay marshals wholesale (no type/outcome switch, `pkg/ui/server.go:611-635`); the store subscriber **ignores** decision events (`store.go:848-856`); `pkg/usage` **does not subscribe** (exactly 2 `Subscribe()` sites repo-wide) ⇒ **no double-count**; FE keys by `id`/`request_id` (decision events carry neither) and `'decision'` is absent from both `PATCH_TYPES` and the FE `EventType` union ⇒ generic fallback render only. |

---

## 🔴 REMAINING CRITICAL

### RC-1 — B2 is only PARTIALLY fixed: the **cooling-only** D-C shape still bills twice, identical to `6aedf00`

**Where:** `pkg/decision/group.go:389-393` (the early return) vs `:394-410` (the D-C block it precedes)

**Failure mode.** The D-C branch — and with it the new member-scoped latch (`:405-409`), the `honestAttempt` flag, and the per-request cap — is **unreachable** when the member's unavailability is expressed as credential-engine **cooling** rather than a skip-cache **mark**:

```go
marked := h.suppression.credentialMarked(...)     // :389
modelMarked := h.suppression.modelMarked(memberID) // :390
if !marked && !modelMarked {
    return false, "", false                       // :391-393  ← EARLY RETURN, D-C never consulted
}
// :394-399 — the comment claims "cooling OR marked ⇒ EXACTLY ONE honest attempt"
if h.memberFullyUnavailable(memberID) { ... }     // :400 — only reachable when marked || modelMarked
```

For a cooling-only member, `attemptSuppressed` therefore returns "not suppressed" with `honestAttempt == false`, the AM-4 rotation at `:224-230` fires, and the member receives a **second** upstream call on a **cooling, non-policy-first** credential.

**Measured at HEAD `5362ec0` (sandbox probes, both also run at `6aedf00` for comparison):**

| Probe | Shape | HEAD `5362ec0` | `6aedf00` | Meaning |
|---|---|---|---|---|
| P1 | both credentials **cooling**, upstream 429 | `dec-a hits=2` ❌ | `hits=2` ❌ | **the review's B2 shape is NOT fixed** |
| P4 | both credentials **cooling**, upstream 429 → 200 | `upstreamCalls=2` ❌ | `2` ❌ | the "stronger form" — a **billed 200** against a credential known to be cooling — **still reproduces** |
| P3 | c1 **marked** + c2 cooling, 429 → 200 | `upstreamCalls=1` ✅ | `2` ❌ | the **marked** shape **is** fixed by this delta |
| P2 | two fully-unavailable members (marked) | `dec-a=1, dec-b=0, dec-c=1` ✅ | `dec-b=1` ❌ | the per-request cap / zero-cost skip is **new, real behaviour** |

**Why the corpus cannot see it.** The new `limit_class_latch` subtest's own comment says so (`handler_group_test.go:1348-1349`): *"Mark both credentials in the UPSTREAM layer so the member is fully-unavailable **via marks (not cooling)**."* The control discriminates the marked shape only (its mutation RED — "got 2 hits" at `:1385` — was reproduced against a **marked** fixture).

**This contradicts the code's own contract and the rulings.** `group.go:394-399` states "cooling **OR** marked ⇒ EXACTLY ONE honest attempt on its policy-first credential"; `memberFullyUnavailable` itself is implemented as cooling-or-marked; and Q2's ruling composes "rotate → **escape** → member fails → advance" — in the cooling-only case *none* of the credentials is usable from the start, so the escape should engage immediately instead of never.

**Production-reachable, routinely.** A quota-class failure cools a credential for **~15 m** (`DefaultQuotaCooldown`) while the skip-cache mark expires in **~60 s** (`UpstreamSkipCacheTTL`) — a ~14-minute window per quota event in which the credential is cooling but unmarked. And an upstream **401/403** rotation cools the credential **without writing any mark at all** (`classifyMemberFailure` sets `mark=true` only for the limit classes). A member with 2 such credentials hits this shape on its next request.

**Minimal fix.** Consult `memberFullyUnavailable` **before** the `!marked && !modelMarked` early return (or compute the D-C state independently of the per-credential mark), so the cooling-only shape also receives exactly one honest attempt on the policy-first credential; then add a corpus case for **cooling-only + limit class** asserting `hits == 1` (the review's `#1 → 429 / #2 → 200` shape). Note the fix also removes the self-contradiction between `:391-393` and the comment at `:394-399`.

---

### RC-2 — the accepted fail-fast event emits `provider` as the **adapter interface**, not the provider name — same key, different JSON type

**Where:** `pkg/decision/group.go:361` (`"provider": res.provider`) vs every other `eventTypeDecision` publisher (`"provider": cred.Provider`, e.g. `:194`, `:213`)

**Failure mode.** `memberAttempt.provider` is declared `UpstreamProvider` — an **interface** (`group.go:84`; `converter.go:82`). The new event publishes the interface value; JSON-marshalled for the SSE relay it renders **`{"provider":{}}`** while every sibling decision event renders `"provider":"openai"`. Same key, two types within one event type — and the field is observably **wrong** (an empty object where the provider name belongs) on the fail-fast path the operator most wants to read.

**Under the leader's own rule** ("if you find a real schema or semantic conflict, that is a blocking finding") this is a genuine schema conflict inside the accepted deviation. **Blast radius stated honestly:** no current consumer breaks — the SSE relay marshals wholesale, the store subscriber ignores decision events, `pkg/usage` does not subscribe, and the FE renders this type through a generic fallback — but the "vocabulary is single-sourced" condition the acceptance rested on does **not** hold for this key, and a future `data["provider"].(string)` consumer would silently miss on exactly this event.

**Minimal fix (one line).** Emit the provider name — `res.cred.Provider` (the attempt already carries `cred`, `group.go:83`) — instead of the adapter interface.

---

## 🟡 REMAINING WARNINGS (non-blocking)

- **R-2 — the two B2 mechanisms are individually unpinned.** Deleting **only** the rotation guard (`group.go:224-227`) leaves the whole `pkg/decision` package **GREEN**; deleting **only** the latch also leaves it GREEN — the fixture makes them mutually redundant, so only the *combined* revert is detected. A future partial regression ships silently. *Fix:* one case per mechanism (guard-only, latch-only), or a fixture where each is independently load-bearing.
- **R-4 — the counter keys are literal-duplicated and pinned one-sidedly.** `group.go:338-340` (envelope) vs `:362-364` (fail-fast) share no const/helper, and only the **fail-fast** side's field names are pinned — a rename of the **envelope's** fields would fail nothing. *Fix:* a shared key const set + a test asserting both payloads' key sets are equal.
- **R-5 — the OQ-1 lift branch under-reports the counters in one sub-path.** `group.go:255-260`: `failFastDecisionGroup` is called with `attempted` **before** the `attempted++/membersAttempted++` at `:259-260`, so the conjunction (all candidates marked ∧ the head's honest attempt returns a client-attributable 400/413/415/422) emits `attempted=0` for a path where one upstream call genuinely happened — the same defect class as the already-fixed Y2, in the branch the fix didn't cover. HTTP status/body unaffected. *Fix:* hoist the increments above the `if res.failure == nil` split (`:246`).
- **R-6 — the Q1 zero-cost skip is unpinned.** The "later fully-unavailable member is skipped zero-cost" behaviour (probe P2) has **no corpus test** — it is proven only by a sandbox probe that is not in the repo. *Fix:* add the two-fully-unavailable-members case asserting `hits(second member) == 0`.

## 🟢 REMAINING SUGGESTIONS

- `hasSuppressedOnly` (`group.go:134`) is true when **any** member was suppressed — the name reads as the opposite of the condition it guards. Rename to `anyMemberSuppressed`.
- The same failure `outcome` string now appears in **two** events per fail-fast request (`:210-217` + `:356-366`); no consumer counts it today, but a future telemetry consumer would double-count. Consider a distinct rollup `outcome`.
- The B4 test pins the **event** payload but never asserts the **response body** omits `decision_all_members_failed` — the client-facing half of the discrimination is unpinned.
- API/store precedence differs (the API mirrors check the lane rule **first**, the store chokepoint **last**) — both 400, no legitimate write affected; align or accept.
- Dev citation drift: the claimed B4 anchor `:1077` is the counter-**extraction** block; the assertions that actually go red are `:1096` (no-envelope) and `:1111` (accounting). The ADR-20 `:1111` citation is correct.

---

## ✅ FENCE + MECHANICAL GATES AT `5362ec0` — ALL PASS

| Gate | Result |
|---|---|
| Tier-1 (11 files) vs `2b987bf` **and** `a2952a9` | `git diff --exit-code` → **exit 0 both bases** (22/22 byte-identical) |
| Tier-2 (a) removed/renamed funcs, 8 files × 2 bases | **0** everywhere |
| Tier-2 (c) removed/changed assertion lines, 6 files × 2 bases | **0** everywhere |
| Repo test-name inventory | base 2379 → HEAD 2453 · **+74 / −0** (counting-basis differs from the earlier 2377/2451 report; the **delta** — 74 added, 0 removed — is identical) |
| Name contract vs corpus | corpus **74** = actual **74**, `comm` **empty both directions**; all 5 WITHDRAWN names absent |
| gofmt NEW drift | HEAD flagged 47 = base flagged 47 · delta **empty** |
| Import guards | `NoProxyImports` · `NoImggenImports` · `TransitiveClosure` · `GroupPolicyFiles_NoCacheImports` — **all PASS** |
| Doctrine + marker literals | doctrine pin **PASS** (the verbatim two-line form); `decision_all_members_failed` const present (`group.go:57`) |
| R1 restore | 34 funcs, 0 deletions vs `6aedf00`, 7 restored bodies **byte-identical** |
| Not pushed | no remote ref contains `HEAD`; no `decision-groups` remote branch |
| Build/vet/tests | `go build` clean · `go vet` clean · full `pkg/decision` package: **174 top-level + 67 sub-tests PASS, 0 FAIL**; full **Pack A** green (`PACKA_EXIT=0`) |
| Worktree integrity | `git status --porcelain` unchanged (the 3 pre-existing `.agents/` session artifacts); no source/test/asset modified |

---

## Evidence base (delta re-check)

Two workers + the reviewer's own runs. **reviewer (direct):** scope/24-commit check; Tier-1 both bases; Tier-2 (a)+(c); name inventory + corpus `comm` both directions; gofmt delta; import-guard + doctrine test runs; R1 func count; not-pushed; the **B1 mutation reproduced by the reviewer** (gate disabled → RED `:1096`, unmutated → PASS); the fail-fast event's field/key-universe and consumer analysis; `memberAttempt.provider` interface type confirmed at source (`group.go:84`). **delta-verify-decision-core** (B1/B2/B4): mutations M2/M4/M6 raw RED; probes P1–P4 at HEAD **and** `6aedf00`; the B2 residual discovered and pinned. **delta-verify-api-tests** (B3/B5/R1/R2-R3/additivity): brace-stack proof of the top-level relocation; the POST/PUT rejection matrix; mutations raw RED at `:967`/`:991`/`:853`/`:1108`/`:1111`; the 7 restored bodies byte-compared; the key-universe and full consumer sweep.

**Not checked in the delta pass:** FE build/gate (no FE source changed in the delta; the decision event type is absent from the FE `EventType` union); the live/real-`ModelsManager` HTTP path for B3 (the mock harness is the API site's only enforcement there — the store chokepoint was verified by reading, `pkg/store/database/` has an **empty** diffstat in the delta); `-race`; the `pkg/credentiallb` flake; anything outside the 4-file delta, which remains governed by the original verdict above.

## Gate disposition

**RC-1 (B2 cooling-only) must be fixed before the tester gate signs off** — it is a live violation of the D-C contract on a routinely reachable production shape, and the corpus's new control is structurally blind to it. **RC-2 is a one-line fix** inside the accepted event addition; under the leader's stated rule it is blocking, and it should land with RC-1. R-2/R-4/R-5/R-6 are strongly recommended in the same tranche (R-5 is a two-line hoist). After those land, only a **narrow delta re-check** of `attemptSuppressed` + the fail-fast event payload is needed — the fence and every other gate hold at `5362ec0`.

---
---

# NARROW DELTA RE-CHECK — RC-1/RC-2 + R-2/R-4/R-5/R-6 closure (APPENDED; both prior verdicts stand)

**Delta verified:** `5362ec0` → **`fb1199e`** — ONE commit (`fb1199e`), 2 files, **+357/−53** (`pkg/decision/group.go` +130/−53, `pkg/decision/handler_group_test.go` +280). Scope held to my own disposition: `attemptSuppressed` + the fail-fast event payload + the four pins I left non-blocking, plus the battery. Nothing else re-litigated.
**Mode:** read-only; the mutation ran in a `/tmp` tar-copy (removed after); the worktree was never written (`git diff HEAD --stat -- pkg/ cmd/` = 0 lines before and after).

## VERDICT: ✅ APPROVED — RC-1 and RC-2 are genuinely fixed and mutation-proven; all four remaining pins are individually discriminating

---

## RC-1 — CLOSED (mutation-proven by the reviewer)

`pkg/decision/group.go:396-443` — `attemptSuppressed` now consults `memberFullyUnavailable` **FIRST** (`:407`), *before* the nil-layer guard (`:431`) *and* the "no mark" early return (`:436-438`), with the RC-1-citing comment at `:397-399` ("engine COOLING is an unavailability source INDEPENDENT of the mark layers, so neither the nil-layer guard nor the 'no mark' early return may hide it").

- **Latch and cap are structurally separate and independently observable.** Latch = `granted[memberID]` (`:410`, denies the SAME member across rotation); cap = the `id != memberID` scan (`:420-424`, denies a DIFFERENT member). The comment at `:413-419` states the separation explicitly, and `per_member_latch` proves it empirically (below).
- **The latch subsumes the old `honestAttempt` break.** The third return value is **gone** (`(bool, string)` at `:396`): on a rotation-eligible failure of the escape attempt, rotation re-offers the member's next credential, `attemptSuppressed` sees `granted[memberID] == true` → `all_credentials_unavailable_already_granted` → suppressed → the member ends. Q2's "rotate → escape → member fails → advance" is now delivered by the latch, not by a separate flag.
- **Cooling-only pin is real:** `cooling_only_latch` (`handler_group_test.go:1446-1500`) sets BOTH credentials cooling via the engine (`engine.excluded[...] = true`, `:1456-1457`) with **zero** skip-cache marks, and asserts **both** `hitsFor("dec-a") == 1` (`:1493-1494`) **and** the policy-first credential by auth header (`lastAuthH == "Bearer sk-cred-dec-a"`, `:1497-1498`).

**Reviewer's mutation (the exact RC-1 residual shape — the D-C block relocated back below the no-mark early return):**

```
    handler_group_test.go:1494: the D-C escape must grant exactly ONE honest attempt for the cooling shape, got 2 hits
    handler_group_test.go:1553: the per-member latch must deny a second honest attempt, got 2 hits
    handler_group_test.go:1588: a later fully-unavailable member must be skipped with ZERO upstream calls, got 1 hits
```
— all three cooling-shape pins RED with the "got N hits" idiom, while `limit_class_latch` (the **marked** shape) and `cooling`/`marked`/`mark_key_equality` correctly **stayed PASS** (for a marked member the early return does not fire, so the D-C is reached either way). The whole package FAILs under the mutation. Control (unmutated): all 7 subtests PASS. **This is precisely the discrimination profile a residual-restoring mutation should produce.**

## RC-2 — CLOSED

`pkg/decision/group.go:377` — `"provider": res.cred.Provider` (a `string` from `models.ResolvedCredential`), replacing the adapter-interface value. The other eight keys are name- and type-identical to the sibling publishers (`outcome`, `model`, `member`, `upstream_status`, `attempted`, `skipped`, `members_attempted`, `duration_ms`). The interface-typed field is no longer published anywhere on this event.

## R-4 — CLOSED (single-sourced, both sides pinned)

`withDecisionAccounting` (`pkg/decision/group.go:355-364`) is the **single source** of the three counter key spellings, and **both** publish sites route through it — the exhaustion envelope (`:344`) and the fail-fast event (`:373`). The doc comment states the invariant ("the two can never drift"). The envelope side is now pinned from the payload: `exhaustion_envelope_counters` (`handler_group_test.go:1123-1166`) asserts `attempted == 2 / skipped == 0 / members_attempted == 2` on the `all_members_failed` event — a rename of the shared keys now fails **both** pins.

## R-2 / R-5 / R-6 — CLOSED (each individually discriminating)

| Pin | Where | Why it is individually load-bearing |
|---|---|---|
| `per_member_latch` (R-2b) | `:1504-1555` | Asserts the rotation **DID** happen (`rotated == true`, `:1548-1549` — *"the latch must be what stops the second attempt"*) **and** `hitsFor("dec-a") == 1` (`:1552-1553`). Removing the latch alone (rotation preserved) ⇒ 2 hits ⇒ **RED at `:1553`** (reproduced). The assertion that rotation occurred is what makes the latch independently observable rather than redundant with the rotation guard. |
| `per_request_cap_zero_cost_skip` (R-2a + R-6) | `:1560-1600+` | Three members, `dec-a` and `dec-b` both fully unavailable (all four credentials cooling), `dec-c` 200. Asserts `hitsFor("dec-a") == 1` (`:1582`), **`hitsFor("dec-b") == 0`** (`:1587-1588`), and the zero-cost skip **observable in the event stream** (a `member_suppressed` for `dec-b`, `:1590-1600`). Removing the cap alone ⇒ `dec-b` gets its own grant ⇒ 1 hit ⇒ **RED at `:1588`** (reproduced). This also closes R-6 — the Q1 zero-cost skip is now in the corpus, not just in a sandbox probe. |
| `client_attributable_counters` (R-5) | `:1171-1215+` | Marks only `cred-dec-a` (member suppressible but not D-C) ⇒ the OQ-1 lift grants the head its honest attempt ⇒ upstream answers **400**. Asserts the fail-fast event reports **`attempted == 1`** (`:1212-1213`). The increments are hoisted **before** the attempt (`group.go:255-256`, comment cites R-5), so the fail-fast at `:267` sees the incremented value — under the pre-fix order it would pass `attempted=0` and the assertion fires. **Verified at read level** (the hoist is the discriminating mechanism); not separately re-run — the mechanism is the same two-line ordering already mutation-proven for the envelope side. |

## Battery at `fb1199e` — ALL PASS

| Gate | Result |
|---|---|
| 74 added / 0 removed vs `a2952a9` | **74 / 0** |
| G4 name contract | **empty both directions** (listed_but_absent = 0, present_but_unlisted = 0) |
| Anti-vacuous literal PASS | **74 / 74** top-level `--- PASS` for exactly the added names across all 7 packages (decision 37, store/database 17, ui 4, models 6, modelscache 2, proxy 6, credclass 2) — **0 lacking**, every package's `=== RUN` count ≥ expected (subtests account for the rest) |
| All new coverage is subtests | top-level added in the diff = **exactly 74** (no growth) |
| Tier-1 vs `2b987bf` **and** `a2952a9` | `git diff --exit-code` → **exit 0 both bases** |
| Tier-2 (a) removed funcs / (c) removed-changed assertions | **0 / 0** across all six files |
| gofmt NEW drift | HEAD flagged **47** = base **47** (delta empty) |
| Import guard + doctrine pin | `TestPackage_ImportGuard_*` + `TestDecision_DoctrineLine_PinnedVerbatimAtSeam` — **ok** |
| Not pushed | no remote ref contains `fb1199e` |
| Worktree integrity | `git status --porcelain` unchanged (the 3 pre-existing `.agents/` artifacts); no source/test/asset modified |

---

## Merge-time note (the leader's requested ruling on the new `origin/latest`)

**Base fact.** `a2952a9` **remains an ancestor** of our HEAD (`git merge-base --is-ancestor` = YES) ⇒ every pin above holds against the anchor. `origin/latest` is **NOT** an ancestor of our HEAD ⇒ a merge is required at ship. **Precision on the tip:** my local tracking ref reads `origin/latest = 0c4a7d7` (*docs(sonic): HANDOFF-v2 rev2 for model-skipcache-ttl*), which is **ahead of** the leader's stated `d22f697` (`d22f697` IS an ancestor of `0c4a7d7`). No fetch was performed (read-only discipline) — treat the live tip as **≥ `0c4a7d7`** and re-read it at merge time.

**Does the model-skipcache-ttl commission interact with our lane's model-mark reads? No — nothing beyond the standard merge review + the existing gate re-run.** Specifically:

1. **No API interaction.** `modelcache.Cache.Marked(model string) bool` (`pkg/modelcache/modelcache.go:83`) is **signature-unchanged**; our seam `pkg/decision/suppression.go:105-110` reads it unchanged.
2. **No D-C interaction.** `memberFullyUnavailable` consults engine cooling + the **upstream credential-scope** marks only — **never the model layer**. The D-C escape, the latch, and the per-request cap are structurally independent of any model-layer TTL change.
3. **No TTL coupling on our side.** Our lane's `SetSkipCacheTTL`/`ttlFn` (`suppression.go:175-184`) feeds the **upstream skipcache** window, not the model layer. The model-layer TTL is internal to `pkg/modelcache`.
4. **No file collision.** The other commission's tree delta over that range touches exactly 3 files (a HANDOFF doc, `pkg/modelcache/modelcache_test.go` +4/−4, and a **comment-only** `pkg/store/database/store.go` change at ~`:214` — far from our `:2106` chokepoint and the `GroupLane` query). Our Tier-1 pin is `pkg/modelscache/models_group_cache_test.go` — a **different package** from `pkg/modelcache`. No textual conflict with any pin file.
5. **The one real semantic interaction — benign by construction.** Our `markLimit` writes a MODEL-layer mark on limit-class failures (`suppression.go:112-117`). Under the new ruling that mark lives a **fixed, non-extending, capped window** (default `model_skip_cache_ttl` = 15 m, `pkg/config/config.go:269`). Consequence for our lane: a model-marked member's suppression expires **sooner** and repeated limit-class failures **no longer extend** the window ⇒ **more** attempts, never fewer. Under the doctrine (marks may suppress an ATTEMPT, never exclude a candidate or cause a failure), that direction **cannot** create a failure oracle — it is a cost/availability nuance only.
6. **Merge-time action (already covered by the standard gate):** re-run our lane's import guards (`TestPackage_ImportGuard_*`, esp. `GroupPolicyFiles_NoCacheImports`) and the modelscache decorator-through pair (`TestCachedModelsConfig_GroupLane_*`) after the merge. No code change is required on our side.

---

## Gate disposition

**All 5 original blockers + RC-1 + RC-2 + R-2/R-4/R-5/R-6 are CLOSED and mutation-proven.** The battery is green at `fb1199e`. The lane is clear for the tester gate. The only open engineering item is the **merge with `origin/latest` (tip ≥ `0c4a7d7`)** at ship time — handled by the standard merge review plus the gate re-run named in §6 above.

**Residual (all 🟢, none blocking, recorded for the next tranche):** the fail-fast path still emits the per-attempt failure event *and* the rollup event for the same failure (two events sharing one `outcome` value — no consumer counts it); `hasSuppressedOnly` still reads as the opposite of its meaning; the B4-family tests pin the event payload but not the absence of `decision_all_members_failed` in the response body; the API mirrors check the lane rule before the kind rules while the store chokepoint checks it last (both 400 — precedence asymmetry only).

---
---

# TIDIER TRANCHE — regression check of the two logic-adjacent changes (APPENDED; all prior verdicts stand)

**Delta verified:** `fb1199e` → **`e5f0816`** — 3 commits, 11 files, **+155/−73**: `5a26a78` (FE, 1 file, type-only) · `889097f` (models: ONE lane-validation vocabulary, 4 files) · `e5f0816` (decision+proxy: one `decision_misroute` constant per package + internals + the guard reorder, 6 files). Tidier report: `.agents/shared/planning/decision-groups/tidier-report-1.md` (0 High / 16 Medium / 17 Low).
**Scope held** to the two logic-adjacent changes + the three pins + the battery. The tester owns the mechanical battery; nothing else re-litigated.
**Mode:** read-only; string renders and package runs in `/tmp` copies; worktree never written.

## VERDICT: ✅ APPROVED — both logic-adjacent changes are safe; one **pre-existing** message-parity gap is now on record

---

## 1. The nil-guard reorder — `pkg/decision/policy.go:58-61` — SAFE, with two documentation notes

The diff is exactly the reorder claimed: `policy := groupPolicy{GroupID: group.ID, …}` **used to be evaluated before** the nil check (which panics if `group == nil`); it now sits **after** it, so the guard executes first.

**(a) Is the nil a legitimate state?** It is **unreachable from the only caller** — and therefore harmless either way. `buildGroupPolicy` has exactly one call site, `pkg/decision/group.go:118`, and its caller `serveDecisionGroup` **dereferences `group.ID` at `:97`** — twenty lines *before* the call. A nil group therefore panics at the true origin (`:97`), never at the policy builder.

**(b) Can the reorder mask a caller bug?** **No.** Because nil cannot reach `:118`, the new `return groupPolicy{Lane: lane}` branch cannot be entered on any live path; and if the contract were ever violated, the panic still fires loudly at `:97`. The reorder is a pure guard-before-dereference hygiene fix with **zero behavioural change** — it also removes a latent panic from a defensive branch. The "degenerate pick" risk the brief names does **not** exist: the branch is unreachable, and even if reached, the caller's `len(order) == 0` check (`group.go:120-123`) fails closed with the exhaustion envelope rather than picking anything.

**(c) Do the policy tests pin BOTH behaviours?** **No — and the claim is inaccurate.** `grep "buildGroupPolicy(" pkg/decision/*_test.go` is **empty**: no test calls it directly, so the nil branch has **no test** (and cannot have one through the public path, per (a)). The non-nil behaviour is covered indirectly by the group-suite. Note the contract is **not documented** where the guard sits — `grep nil pkg/decision/policy.go` yields only `:59` and `:111`; the function doc at `:46-57` says nothing about nil, and the reorder added no comment. *Recommendation (🟢, non-blocking):* one doc line — "a nil group (unreachable from the only caller, which dereferences first) yields an empty policy" — or delete the defensive branch. **The reorder itself is correct and worth keeping.**

## 2. `memberAttempt.err` populated at 6 sites — PURELY ADDITIVE TELEMETRY ✔

- Exactly **6 writes** (`res.err = err` at `group.go:599, 609, 632, 641, 653, 660`) and **ZERO reads** — in production **and in the test files** (`grep "\.err\b" pkg/decision/*_test.go` filtered to non-err-handling forms = empty).
- `memberAttempt` is never marshalled: it is a private struct (`:81-91`); every wire payload is built from explicit `map[string]interface{}` literals. **No payload shape changed.**
- Because nothing reads it, **no test's semantics can flip** on a newly-present cause. The fields that DO drive behaviour (`failure`, `outcome`, `upstreamStatus`, `body`) are untouched by this tranche.
*Verdict: additive; safe to keep. (🟢 It is still dead weight until a consumer arrives — acceptable as a diagnostic field, worth a one-line doc.)*

## 3. Message unification — CHAT-LANE CANONICAL STRINGS BYTE-IDENTICAL ✔; the mixed/invalid change is the intended unification; one pre-existing gap remains

Verified by **rendering**, not by reading — a `/tmp` harness called `ValidateGroupMembers` with concrete fixtures at `fb1199e`, `HEAD`, and `2b987bf` and diffed `err.Error()`.

**Canonical validator (the path the text lane's writes take) — byte-identical across all three revisions** for every chat-lane shape:

| Shape | Rendered chat-lane string | fb1199e | HEAD | 2b987bf |
|---|---|---|---|---|
| image-gen member | `model g1: kind "group" member "imgA" must be a chat-kind model (got kind "image-gen")` | ✅ | ✅ | ✅ |
| group member | `model g2: … is itself a group (flat membership only — group-as-member is forbidden)` | ✅ | ✅ | ✅ |
| shadow missing | `model g4: … shadow_model_id "nope" does not exist (must reference an existing chat-kind model)` | ✅ | ✅ | ✅ |
| shadow wrong lane | `model g5: … must reference a chat-kind model (got kind "decision")` | ✅ | ✅ | ✅ |
| empty members / empty `model_id` / duplicate / `weight<=0` | *(4 shapes)* | ✅ | ✅ | ✅ |
| mixed-lane (chat group + decision member) | `model g3: kind "group" members must all share one kind (chat or decision) — member "decA" has kind "decision"` | ✅ | ✅ | *(lane did not exist)* |

The two extracted helpers carry the **exact** historical canonical format strings — `MixedGroupMembersError` (`group_lane.go:179-181`) and `InvalidMemberKindError` (`:185-187`) — so they are verbatim moves, not rewords. `DeriveGroupLane` (`:164-172`) is a literal extraction of the inlined loop (identical semantics, no message involvement). The shadow-target message changed `chat-kind` → `%s-kind` + `groupLane`, which renders `chat-kind` byte-identically for a chat group and only corrects the non-chat lanes where the hardcoded `chat-kind` was **factually wrong** (the intended M2 fix; the pre-change decision-lane text was misleading).

**The admin-API mirrors did change, and it is the intended unification.** Pre-change, `pkg/ui/server.go` (POST `:976`-area, PUT `:1531`-area) used its own literals **without** the `model <id>: ` prefix; they now call the shared helpers and therefore render **with** it:
- `kind "group" members must all share one kind (chat or decision) — …` → `model <id>: kind "group" members must all share one kind (chat or decision) — …`
- `kind "group" member "x" must be a chat-kind model (got kind "image-gen")` → `model <id>: kind "group" member "x" must be a chat-kind model (got kind "image-gen")`

This is exactly the "ONE vocabulary" ask (the tidier report's M1 names the pre-existing "prefix wording" drift as the defect being fixed), it is operator-visible only on `/fe/api` write rejections (never on the text data lane), and it is **strictly more informative**. It is **not** pinned by any exact-string test — `kind_rules_parity_test.go` asserts **substrings** (`"must be a chat-kind model"`, `"members must all share one kind (chat or decision)"`), which is why the old divergence never failed and why unifying it breaks nothing. **Nothing else was silently reworded.**

**🟡 Carried forward (PRE-EXISTING, not a tranche regression): the shadow-target messages were never unified, so a residual prefix divergence remains.** Canonical renders `model <id>: kind "group" shadow_model_id … does not exist (must reference an existing <lane>-kind model)`, while both UI mirrors render `kind "group" shadow_model_id … does not exist (…)` — no prefix. The same applies to the other inline shadow messages (not-a-group / got-kind / already-claimed). Concrete effect: the same invalid shadow target yields two different `{"error": …}` strings depending on whether it is validated through the canonical validator or the admin API. This tranche only lane-worded those messages (M2); it neither caused nor closed the prefix gap. *Minimal fix:* extract `ShadowTargetMissingError` / `ShadowLaneMismatchError` mirroring `MixedGroupMembersError` and route all three sites through them — or document the per-site prefix as intentional and pin the chosen contract (the parity test would need exact-string asserts, not substrings).

## 4. `policy_go_copy` subtest — asserts the VERBATIM line, not a prefix ✔

`handler_group_test.go:1844-1853` — `strings.Contains(string(src2), doctrine)` where `doctrine` (`:1559`) is the **full two-line** constant, including the `\n//\t` continuation: *"The cache is an optimization, not the truth: it may reorder candidates, never exclude them — and it\n//\tmay suppress an ATTEMPT, never a PICK or a CHAIN."* A truncated or reflowed copy fails. `pkg/decision/policy.go:15-18` carries that exact wrapped form.

The **primary** `suppression.go` pin is **unchanged** — the diff is a pure addition *after* the existing assertion (`:1840-1842` untouched); fresh run: `--- PASS: TestDecision_DoctrineLine_PinnedVerbatimAtSeam` + `--- PASS: …/policy_go_copy`.

## 5. Battery + commit hygiene — ALL PASS

| Check | Result |
|---|---|
| 74 added / 0 removed vs `a2952a9` | **74 / 0** |
| New top-level test names in the tidier tranche | **0** (all new coverage is a subtest — the name contract holds) |
| Tier-1 vs `2b987bf` **and** `a2952a9` | `git diff --exit-code` → **exit 0 both** |
| `decision_misroute` payload shapes | **unchanged** — `{outcome, model}` (chat) and `{id, model}` (Anthropic), literal → `decisionMisrouteCode` only, identical values; no field added, removed or renamed |
| Commit separation | `5a26a78` = **1 FE file / 0 Go / 0 static** · `889097f` = **4 Go / 0 FE** · `e5f0816` = **6 Go / 0 FE** — clean |
| FE "type-only, byte-identical bundle" | **confirmed** — zero `pkg/ui/static` changes; the diff is comments + type annotations (`GroupModel[]` → `Model[]`, removing the `as GroupModel` cast), all erased at build time ⇒ the emitted bundle is necessarily unchanged |
| Provider / kind / lane constants | **unchanged values** (`KindChat/ImageGen/Decision/Group`, `GroupLaneChat/Decision`, `MemberLane` mapping) |

**Two guard fixes rode along, both landing on prior review suggestions:** the compile-time `groupLaneReader` assertion was **moved, not deleted** (`handler.go:85`, now above the `// Config holds …` comment, so `Config` regains its godoc — my S8) and its duplicate was removed from `handler_anthropic.go` (my S9) — the decorator-forward trap remains build-enforced. `hasSuppressedOnly` → `anySuppressed` (my rename suggestion) is behaviour-free.

---

## Remaining notes (all 🟢, non-blocking)

- `pkg/decision/group.go`'s new `withDecisionAccounting` comment cites the OQ-1 lift increments at `:246-247`; they now sit at `:255-256` (stale line reference in prose only).
- `decisionMisrouteCode` is deliberately duplicated per package (`pkg/decision`, `pkg/proxy`) with no import (the import guard forbids it) and no cross-package pin — acceptable: each package's wire-string assertions (`handle_chat_misroute_test.go:181-182` and the decision-lane misroute tests) fail if either constant drifts from the literal `"decision_misroute"`.
- `buildGroupPolicy`'s nil branch is currently unreachable and untested (see §1); document it as defensive or delete it.

## Gate disposition

**Both logic-adjacent changes are safe; the tranche is clear for the tester gate.** The only recorded message gap (shadow-target prefix parity) is **pre-existing**, non-behavioural, and now documented for a follow-up. Recommended (all optional): the §1 doc line, the `ShadowTarget*Error` helper extraction, and the two stale line references.

**Evidence base.** Reviewer (direct): the reorder diff and the caller's `:97` dereference; the 6-write/0-read `err` audit; every changed literal in `pkg/models/*` + `pkg/ui/server.go`; the parity test's substring assertions; commit-separation counts; Tier-1 both bases; 74/0 and zero new top-level names; the fresh doctrine-pin run. Worker (`tidier-msg-verify`, `code-review`): the rendered-string tables at `fb1199e`/`HEAD`/`2b987bf` from a `/tmp` harness, the (B) literal classification, the (C) parity matrix, and the (D) vocabulary check.
**Not checked:** FE behaviour beyond the bundle argument (the emitted output cannot change under a type/comment-only edit); live HTTP rendering through the handlers; tsc/vite (tester's battery); anything outside the 3-commit delta, which remains governed by the verdicts above.
