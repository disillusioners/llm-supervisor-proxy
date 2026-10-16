# Architecture Recommendation — `group` Model Kind (Load-Balance Groups)

**Date:** 2026-10-16 (UTC) · **Mode:** Standard Design (competitive fan-out, 3 workers) · **Stage:** PLAN-ONLY enrichment of `plan.md` @ `feature/load-balance-groups` (5b13388)
**Method:** 3 skill-equipped analyst workers, one skill each, all anchors verified read-only in this worktree:
`data-flow-design` (FA1+FA4+FA5, instance fbf2178d) · `structural-design` (FA3+FA7+FA8, instance 143e8f61) · `trade-off-analysis` (FA2+FA6, instance 336c529e). Architect spot-verified the three highest-severity findings before aggregation.
**Inputs:** plan.md (622L), decisions.md (Q1–Q16), research-codebase.md (744L). **Not modified:** plan.md / decisions.md / research-codebase.md — all plan changes are routed as numbered amendments (§10) for the planner.

---

## 0. Verdict at a Glance

| # | Focus area | Verdict | Plan impact |
|---|---|---|---|
| FA1 | Shadowing mechanism | **Endorse-with-amendments** — claim column + claims-first `GetModel` stands | Amendments 4, 5, 20 (modelscache eviction hook; ultimate-loss doc; dual-claim edge) |
| FA2 | Membership shape | **Endorse** — inline pairs survive the "more for less?" challenge; reference = more-for-more | Amendments 12 (ops notes + FE hint) |
| FA3 | Engine topology | **Endorse-with-amendments** — clone stands vs generalize/shared-core | Amendments 2 (member-key `|` gate 🔴), 14, 15 |
| FA4 | Pipeline seam | **Challenge** — resolver seam stands, but "Anthropic = zero code changes" is **false at HEAD** | Amendment 1 (🔴 Anthropic failover branch), 10 |
| FA5 | Transparency & masking | **Endorse-with-amendments** — mask stands; per-chunk patch unsafe as specified | Amendments 6 (per-SSE-event buffering), 7 (§2.5 factual fix) |
| FA6 | Metering topology | **Endorse-with-amendments** — both-rows stands; plumbing does not exist as written | Amendments 3 (🔴 plumbing spec), 11, 13, 19 |
| FA7 | Storage & invalidation | **Endorse-with-amendments** — JSON columns + 031 stand; site arithmetic corrected | Amendments 8, 16 |
| FA8 | FE structure | **Endorse-with-amendments** — shape stands; 9-site count CONFIRMED; shadow picker missing | Amendments 9, 17, 18 |

**Overall:** the plan is architecturally sound — every load-bearing choice (ID-space claim, inline members, engine clone, resolver-seam integration, mask-don't-leak, JSON columns, FE shape) survives competitive challenge on in-source evidence. Three plan defects must be amended before implementation: (1) 🔴 the Anthropic `/v1/messages` failover path silently fails group requests on first member 429; (2) 🔴 the member-key construction has an unguarded collision surface (`|` in credential IDs); (3) 🔴 metering task P2.8 references an identifier that does not exist at the metering sites. All three fixes are cheap (≤30 LOC each). The remainder is documentation and anchor hygiene.

**Hard-requirements scorecard (enforced by all three workers):** (1) client IO unchanged ✅ · (2) chat-only ✅ · (3) sticky routing fully designed ✅ · (4) shadowing semantics ✅ · (5) flat + minimal, cuts parked in §14 ✅ · (6) FE tab consistent ✅. **No violations found.**

---

## 1. FA1 — Shadowing Mechanism

**Question:** claim field vs rename-on-shadow vs dual-ID emission vs resolution-order-by-kind.

| Approach | Complexity | Scalability | Maintainability | Risk | Cost | Recommendation |
|---|---|---|---|---|---|---|
| A: `shadow_model_id` + claims-first `GetModel` (plan) | Low (~60 LOC) | High | High | 🟡 60s cache window (Am. 4) | Low | **✅ ADOPT** |
| B: Rename-on-shadow | Med (identity churn + revert machinery) | Med | Low | 🔴 ID churn invalidates caches mid-flight | High | Reject |
| C: Dual-ID emission (no hiding) | Low | Med (two visible rows) | Med | 🟡 client ambiguity | Low | Reject — breaks hiding requirement 4 |
| D: Resolution-order-by-kind | **Impossible** | n/a | n/a | n/a | n/a | Rejected on PK evidence — duplicate IDs hard-blocked (`ErrDuplicateModelID`, `config.go:694-699`); two rows can never share an ID to order |

### Stress-test findings (verified at HEAD)
1. Resolution-site inventory holds: `store.go:854` GetModel; strict twins `:1149/:1191/:1197/:1205/:1217`; `handler_functions.go:128` + silent-external-passthrough `:148-165`; `handler.go:338/:565`; `handler_anthropic.go:158/:418`; `race_executor.go:55/:121/:177/:275`; `race_coordinator.go:1409/:1521`; `/v1/models` `handler.go:245-272`; ultimate `:746-768`. All ID-keyed — claim-first in `GetModel` is the single authoritative choke point. Strict-twin bypass mandatory and planned.
2. **🟡 modelscache claim-window (Amendment 4).** The cache is `pkg/modelscache` (NOT `pkg/ultimatemodel/modelscache` as research §1.3 cites — path corrected; architect-verified). `GetModel` caches per-requested-ID (`:375` `negByID` negative entries under `ModelsNegTTL`, default 60s at `:55-56`), and `AddModel`/`UpdateModel`/`RemoveModel` (`:643/:665/:687`) evict only by the row's **own** ID. When group G claims X, a cached `modelsByID[X] → chat_X` entry (or a 60s negative entry for X) survives and **bypasses the manager's claim logic**. Plan P2.2's "invalidate the cache for the group ID and the shadowed ID" is directionally right but under-specified: it needs a dedicated claim-eviction hook (evict `modelsByID[old/new shadow]` AND `negByID[old/new shadow]`) plus claim-awareness (or documented eviction ordering) in `CachedModelsConfig.GetModel`.
3. **🟡 Undocumented semantic (Amendment 5):** while X is claimed, `GetModel(X)` returns the group row with `ExcludeFromUltimateSwitching=true` (G7) ⇒ **X's ultimate-switching silently stops** for tokens that pinned X. Behaviorally defensible (the group takes over X's identity wholesale) but must be documented in §3.6.4 + risk list.
4. **🟢 Dual-claim edges (Amendment 20):** G8's uniqueness scan blocks two *enabled* groups claiming one ID at write time; both-disabled claims are inert (no index entry); enable-ordering resolves via the scan. Single-valued `claims` map tolerates violation only via non-API writes — document one line.
5. Claim-on-a-group rejected (G8 chat-kind target) ✅ · deletion-reappear is derived (no tombstones) ✅ · `GetModelByName` has zero client-facing callers (only `pkg/modelscache/*` tests + `store.go:936`) ✅ · FE chat tab deliberately still shows the claimed model to admins (strict-twin parity; plan should state it) ✅.
6. **Auth:** `IsModelAllowed` (`token.go:133-143`) evaluates the client-requested ID string unchanged — a token allow-listed on the shadowed ID transparently reaches the group; fail-closed otherwise. Exactly as the commission requires. ✅

### Recommendation: **A — adopt the plan's claim mechanism, plus Amendments 4/5/20.**
**Confidence: High.** Flip assumption: none — B/C/D are dominated on evidence.

---

## 2. FA2 — Membership Shape

**Question:** inline `(credential_ref, upstream_model, weight)` pairs vs references-to-model-rows vs hybrid — is inline truly minimal, or does reference give MORE for LESS?

| Approach | Complexity | Scalability | Maintainability | Risk | Cost | Recommendation |
|---|---|---|---|---|---|---|
| A: Inline pairs (plan) | 5 — one LB level, no nested resolve | 3 — N-cred target ⇒ N member rows (legal, cooldown-correct) | 4 — one validator + Q11 scan; free-text typo + rotation churn drag | 4 — no recursion, no listing leak, no new orphan class | 5 — FE = dropdown + text; ~250 LOC validation | **✅ ADOPT (4.15 wtd)** |
| B: Model-row references | 2 — nested resolution, two LB levels | 4 — inherits per-model credential LB free | 3 — reuses validation but member-model delete = NEW orphan class | 2 — member rows keep own `fallback_chain` ⇒ nested failover + recursion guard | 2 — ~300+ LOC + pre-provisioned rows | v2 only (2.65) |
| C: Hybrid | 2 — dual member shapes forever | 4 | 2 | 2 | 1 — ~400 LOC | Reject for v1 (2.25) |

### Stress-test findings
1. The resolver-blocker dodge is real in mechanism (`resolveWithCredential` `store.go:2107`, `:2118` hard-binds one row → one upstream string; reference-shape would bypass by re-entering the resolver per member — which is precisely what drags in nesting/recursion/visibility).
2. **Selector composition verified sound:** prefix-sum over member keys; G4 (no dup `cred|model` pair) + same-`upstream_model`-different-credential members = distinct keys, legal; a 2-credential target expressed as 2 members composes arithmetically (weights do double duty as credential weights) and cooldown isolation is per member key. The "per-member credential LB is LOST" objection resolves to an **ops pattern, not a defect** — but it must be documented (Amendment 12) or operators will read N near-identical rows as a validation accident.
3. Members structurally cannot leak into `/v1/models` (listing emits `m.ID` of real rows only — `handler.go:252-262`). ✅
4. The strongest counter-case for B — **credential rotation**: rotation changes the member key ⇒ sticky bindings drop via `OnGroupChanged` (by design); reference-shape would inherit rotation invisibly. Priced; still net-negative for B (Risk/Maintainability). Runbook note in Amendment 12.
5. decisions.md Q2's "visibility = UX disaster" framing is **overstated** (chat members are listed today by operator choice; B forces no *new* visibility) — but B still loses on the axes that matter. The plan's choice survives an honest re-weighing.

### Recommendation: **A — inline pairs, as planned; add the Amendment 12 ops notes.**
**Confidence: High.** Flip assumption: operators reject the N-rows-per-target pattern in practice → re-open reference-shape at v2.

---

## 3. FA3 — LB Engine Topology

**Question:** clone `pkg/credentiallb` → `pkg/grouplb` (plan) vs generalize the frozen engine in place vs shared generic core.

| Approach | Complexity | Scalability | Maintainability | Risk | Cost | Recommendation |
|---|---|---|---|---|---|---|
| A: Clone (plan) | Low — ~700 LOC mechanical (honest measure ≈75%) | High — per-group state isolated | Med — sibling drift (test parity catches) | Low — zero touch on frozen, flake-carrying package | ~700 + ~600 test LOC | **✅ ADOPT** |
| B: Generalize `credentiallb` in place | Med | Med | Low — frozen rule forbids | **High** — touches `pkg/credentiallb` (known `-race` flake class lives here) | ~500 + full retest | Reject (frozen rule) |
| C: Shared generic core + adapters | High now | High | Med | Med — adapter layer risk | ~1000 + dual test matrices | Reject for v1 (disproportionate) |

### Stress-test findings
1. Layout verified: `engine.go` (719L), `selector.go`, `binding.go`, `key.go`, `events.go`, **`testhooks.go` (196L, 7 test seams)**, `doc.go` (E-1..E-4). E-1 two-level locking `engine.go:36-65`/`doc.go:42-55`; E-3 n==1 fast path `:241-246`; constants 24h/5m/60s at `:16-21`. The 7-coupling map in plan §3.5 is **complete on engine mechanics** but **misses two specs** (Amendments 14–15): the clone must carry `testhooks.go` 1:1 (the deterministic TOCTOU-test promise in §9.1 is undeliverable without `InjectAllCoolingForTest` et al.), and the per-group init posture must be pinned (lazy-first-call, no boot `RebindFromStore` walk — `RebindFromStore` exists at `engine.go:396`, consumed once at `store.go:680`; grouplb has no persisted-selector analog).
2. **🔴 Member-key collision (Amendment 2).** `CredentialConfig.ID` is free-form — `Validate` (`credential.go:107`) checks only non-empty (architect re-verified). `("foo","bar|baz")` and `("foo|bar","baz")` produce the same key `"foo|bar|baz"` ⇒ silent wrong-binding. Fix: reject `"|"` in credential IDs at Validate + G3 defense-in-depth (or escape/hash the key — rejected: less debuggable / behavior change).
3. Sticky-key scoping verified: `ComputeConversationKey(modelID, …)` includes the modelID param (`key.go:40-43`) ⇒ group-scoped bindings by construction; verbatim reuse is correct.
4. Member-precise cooldowns (Q14) are only expressible in a clone (the frozen engine's cooldown map is `credentialID`-keyed — `binding.go:53-55`); this independently justifies A over B.
5. Account-wide provider 429 under member-precise cooling: only the failed `(cred, model)` pair cools; hammering sibling members eventually cools them too — eventual consistency, acceptable, one documented sentence.

### Recommendation: **A — clone, with Amendments 2/14/15.**
**Confidence: High.** Flip assumption: a second LB consumer lands soon (e.g. MCP multi-backend) → shared-core extraction fires as a v2+ trigger.

---

## 4. FA4 — Pipeline Integration Seam

**Question:** resolver-seam-only integration shared by both protocol paths, fallback lockout interplay, ultimate exclusion, misroute posture.

| Approach | Complexity | Scalability | Maintainability | Risk | Cost | Recommendation |
|---|---|---|---|---|---|---|
| A: Resolver kind-branch (both variants) + two failover hooks (OpenAI planned + Anthropic NEW) | Low-Med | High | High — one seam, two thin hooks | Low once Am. 1 lands | ~60–80 LOC total | **✅ ADOPT (amended)** |
| B: Handler-level per-protocol branching (no shared seam) | High | Med | Low — 2 forks | Med | High | Reject |
| C: Adapter-level virtualization | High (dead seam — see FA5) | Med | Med | Med | High | Reject |

### Stress-test findings
1. **🔴 CRITICAL — plan defect (Amendment 1).** Plan §3.6.3 claims the Anthropic path needs **zero code changes**. False at HEAD: on member 429, `/v1/messages` internal failover (`internal_handler.go`) hits `if len(h.config.Credentials) <= 1 { return err }` at **`:240`** (architect re-verified verbatim) — a group has 0 credentials, so the error returns immediately: **one member 429 fails the whole request despite healthy members**. Even past the guard, `:244` calls the **credential** engine (`h.credEngine.ExcludeAndReselect`) which has no group state, and the retry rebuilds from `h.config.InternalModel` (empty for groups) ⇒ guaranteed second failure. Required: an `IsGroup()` branch — lift the guard for groups, call `grouplb.ExcludeAndReselect(groupID, convKey, memberKey(resolved.CredentialID, resolved.InternalModel), retryAfter)`, rebuild the retry from the resolver's member-shaped result. ~20–30 LOC + test. Without it, groups on `/v1/messages` are strictly worse than the chat models they shadow.
2. Cached variant placement verified: kind-branch in `ResolveInternalConfigWithAffinityCached` must sit at `store.go:2292` **before** the `Credentials==0` fall-through (~`:2333`) — a group's empty `Credentials` would otherwise degrade to `ok=false` (Amendment 10 pins this).
3. OpenAI race path as planned ✅: `credFailoverPlanLocked` group branch (`race_coordinator.go:1465`, guard `:1410` needs the G-aware member-count form); per-attempt re-fetch re-finds the group; `Internal=true` (G1) routes all ~11 dispatch predicates correctly.
4. Chat→group fallback (Q16): coherent as documented — the `:1410` guard skips member exclusion for fallback-reached groups and sticky convKey returns the same member per attempt; escape is the chain falling to the next row. Documented posture stands.
5. Misroute posture: groups pass chat guards and are caught by neither imggen nor decision guards (`IsImageGen`/`IsDecision` false) ✅; `/v1/decisions` with a group ID fails cleanly at the resolver's `IsDecision()` check (`decision/handler.go:265`) ✅; `/v1/image_generation` with a group ID is **partially unverified** (imggen dispatches raw bytes; expected clean upstream failure) — one-line check at implementation (Open Question 1).
6. First-byte fallback lockout on `/v1/messages` verified (`handler_anthropic.go:332-334` `headersSent` break): no mid-stream member change; streaming is lock-bound to one member once response starts. ✅
7. Ultimate: G7 write-time enforcement + kind-blind runtime gate never selects a group; `X-Force-Ultimate-Model` residual (R9) accepted as documented.

### Recommendation: **A — the seam stands, but the plan must add the Anthropic failover branch (Amendment 1) and pin the cached-variant placement (Amendment 10).**
**Confidence: High.** The 🔴 was verified line-by-line by the analyst and re-verified by the architect.

---

## 5. FA5 — Transparency & Masking

**Question:** mask the OpenAI response `model` field vs accept the leak vs adapter-seam masking.

| Approach | Complexity | Scalability | Maintainability | Risk | Cost | Recommendation |
|---|---|---|---|---|---|---|
| A: Handler-level patch (plan) | Low (~80 LOC) | High | High | 🟡 SSE straddling unless buffered per event (Am. 6) | Low | **✅ ADOPT (amended)** |
| B: Accept the leak | Zero | n/a | Trivial | 🔴 transparency requirement violated | Zero | Reject |
| C: Adapter-seam via `RequestMetadata` | **Med-High — the seam is DEAD CODE** (see below) | High | Med | 🟢 once wired | Med-High | Reject for v1 |

### Stress-test findings
1. **Factual discrepancy RESOLVED against the plan:** `RequestMetadata{ClientModel, UpstreamModel}` (`adapter_openai.go:53-58`) is populated only by `ParseRequest`, which has **zero production callers** (grep: `handler_functions.go:88` calls only `ToStoreMessages`; handler/race_executor never call it). Plan §2.5's "exists and is populated" is wrong; research §3.6's "declared but never wired" is right (Amendment 7). Option C would require threading ParseRequest through the production path first — disproportionate for v1.
2. **🟡 Straddling (Amendment 6):** the stream relay at `handler.go:1172-1184` writes **raw read-buffer chunks** (`buffer.GetChunksFrom`); a JSON object can straddle chunk boundaries, so a per-chunk top-level-key patch fails open (verbatim) on straddled events ⇒ **intermittent leak**. Fix: buffer/patch per complete SSE `data:` event (or reuse the existing per-event parsing machinery adjacent at `:1182`), group-kind-only, fail-open-to-verbatim retained.
3. Non-stream `finalBody` patch at `:1587` is safe (body already parsed). Error envelopes carry no top-level `model` field — no patch needed on errors. Usage/final chunks: no `model`. MiniMax-flavored members: top-level key only. ✅
4. Non-group hot-path cost: zero if gated at the call site (`if rc.resolvedModel.IsGroup()`) — verify the gate placement in implementation; P2.7's byte-unchanged regression assert for non-group traffic is the right lock. ✅
5. Anthropic already masks (`translator/response.go:38`, `incremental_stream.go:458`) — parity free. ✅

### Recommendation: **A — mask, with per-SSE-event buffering (Amendments 6–7).**
**Confidence: High.**

---

## 6. FA6 — Metering Topology

**Question:** both rows (group + member) vs group-only vs member-only; timeout pattern; plumbing reality.

| Approach | Complexity | Scalability | Maintainability | Risk | Cost | Recommendation |
|---|---|---|---|---|---|---|
| A: Both rows (plan) | 4 — plumbing needed, not free as written | 4 | 4 — continuity + cost attribution | 3 — plumbing gap (Am. 3) | 4 | **✅ ADOPT (amended)** |
| B: Group-name only | 5 | 5 | 3 | 5 | 4 | ❌ fails per-target visibility presumption (Q9) |
| C: Member-only | 4 | 4 | 2 — dashboard continuity broken | 4 | 4 | ❌ fails continuity |

### Stress-test findings
1. **🔴 Plan defect (Amendment 3):** P2.8/Q9 meter `IncrementModelUsage(resolved.InternalModel)` — **`resolved` is not in scope at any of the 3 sites.** Resolution happens per-attempt in `race_executor.go:303` (lock-free write `:310-311`); `requestContext` has no served-member field; `winner.GetModelID()` returns the **group row ID**. Required plumbing (~15–25 LOC + test): lock-protected getter on `upstreamRequest` (house W4 discipline) or stamp the winner's `resolved.InternalModel`/member key into `rc` post-`WaitForWinner`; gate the member row on **winner-row-kind == group && resolvedOK** (also meters fallback-reached groups correctly for free).
2. **Anchor corrections (Amendment 11):** the 3 sites are `handler.go:908-934` (**ultimate re-execution branch** — plan mislabels it "stream"), `:1446-1472` (true stream, inside `streamResult`), `:1656-1682` (non-stream). The **ultimate site must be excluded** from member-row metering (the ultimate target was served, not a member) — group-name row only there, documented.
3. `writeMu` exclusivity holds: two `IncrementModelUsage` calls = two serialized UPSERTs on distinct `model_id` rows; image writers have zero non-test callers outside `pkg/imggen` — unreachable. ✅
4. 5s detached-context adoption verified safe and strictly improving (current: detached goroutine on bare `context.Background()`, no timeout, correctly not `r.Context()`); R10's "bounded behavior change" framing is right — it touches ALL chat traffic, not just groups. ✅
5. **`/v1/messages` meters nothing** (zero `counter.` refs in `handler_anthropic.go`) — pre-existing gap, unchanged; the plan must say so explicitly (Amendment 13). Success-only semantics confirmed (no failure-path metering). ✅
6. Cross-feature collision edge: a member's `upstream_model` string can equal an operator model's ID ⇒ both features write one `model_hourly_usage` row. Cosmetic blend; extend A7 to name it (Amendment 19).

### Recommendation: **A — both rows, with the plumbing specified rather than implied (Amendments 3, 11, 13, 19).**
**Confidence: High.**

---

## 7. FA7 — Storage & Invalidation

**Question:** JSON columns (plan) vs separate table; migration mechanics; invalidation completeness.

| Approach | Complexity | Scalability | Maintainability | Risk | Cost | Recommendation |
|---|---|---|---|---|---|---|
| A: `members_json` + `shadow_model_id` columns, migration 031 (plan) | Low | Med (JSON per read; group rows rare) | High — one write path, house precedent | Low — additive, no backfill | ~30 mechanical sites | **✅ ADOPT** |
| B: `model_group_members` table | High | High | Low — two write paths, hand-built integrity | Med | ~60+ sites | Reject (house-style violation) |

### Stress-test findings
1. **Arithmetic corrections (Amendment 8):** today's SELECT lists are **20 columns** (→22), `dbModelRow` 20 fields (→22), `scanModels` 20 Scan targets (→22); INSERT/UPDATE placeholders are 19 (→21 — plan correct there); test pins at `querybuilder_test.go:239/:262/:275` assert 19 → bump to 21. The plan's §3.3 "12 SELECT strings" is wrong in count AND misses a site class: **inline SELECTs at `store.go:858-873` (GetModel) and `:940-955` (GetModelByName) bypass querybuilder entirely** and also need +2 columns (the plan lists their scan sites but not their SQL strings). Final enumeration: 8 builder strings × 2 dialects + 2 inline sites; re-derive the exact list at implementation (docs disagree 8/12/16/18 — see Open Question 3).
2. **No M-1 shadow-write dance needed (Amendment 16):** both columns are purely additive (no legacy column to keep alive); `json.Marshal` on INSERT/UPDATE suffices. The M-1 precedent citation stays useful but the plan should say the dual-write pattern does NOT apply.
3. Migration 031 mechanics verified: registry append at `migrate.go` after `"030"` (ordered-slice trap, two prior near-misses — the contains-`"031"` test in P1.3 is the right lock); down files = plain `DROP COLUMN` per the post-023 SQLite rule (`023_add_ultimate_model.down.sql:1` verified). sqlc dir stale (predates 028–030) — no regeneration needed. ✅
4. JSON-backend twin: struct fields shared; JSON-path work = Validate (`config.go:971`), claim index on Load, GetModel claim check, resolver kind-branch, DeleteCredential Q11 scan — plan covers all five. ✅
5. Q11 block-at-delete: the pre-existing in-use check (`store.go:2019-2024`) walks `model.Credentials` only — P1.8's `members_json` walk is correctly additive. Deleting a *shadowed* model row is tolerated (claim stands, nothing to hide) — verified harmless. ✅
6. G5 divergence (provider-match invariant `store.go:1631-1644` must not apply to members) — correctly designed; the feature itself. ✅

### Recommendation: **A — as planned, with Amendment 8's corrected site arithmetic and 16's no-shadow note.**
**Confidence: High.**

---

## 8. FA8 — FE Structure

**Question:** tab/editor/hook/cache shape vs the Decision-tab precedent.

| Approach | Complexity | Scalability | Maintainability | Risk | Cost | Recommendation |
|---|---|---|---|---|---|---|
| Mirror Decision-tab precedent (plan) | Low | High | High — sibling parity | Low | ≈ Decision commission | **✅ ADOPT** |
| Custom shape | High | Low | Low — drift | Med | Lower upfront | Reject |

### Stress-test findings
1. **Cache-delete count SETTLED — the plan's 9 is correct, research §8.3's "12 sites" is an over-count.** Verified: exactly 9 mutation sites in `useApi.ts` (`useModels` `:300-302/:315-317/:325-327`, `useImgGenModels` `:402-404/:417-419/:427-429`, `useDecisionModels` `:503-505/:517-519/:526-528`); credential/token mutations invalidate NO model caches today (and correctly so — models reference credentials by ID). Quadruple delete ⇒ 9 sites × 4 keys = 36 deletions. Amendment 18 records the correction so the executing worker doesn't "find" phantom sites.
2. **🟡 Real gap (Amendment 9):** `GroupModelForm` has **no `shadow_model_id` editor specified** — the field exists in the payload but no UI sets it (operators would edit raw JSON). Add a clearable chat-kind dropdown populated from `useModels()` filtered to chat kind.
3. Component precedents verified: `MultiCredentialEditor.tsx` same-provider guard `:82-93` (lifted for groups), dup check `:76-81` (kept), MAX 16 `:11`; `DecisionModelForm` hardcoded-stamp pattern `:18-20` transfers to GroupModelForm (kind + `exclude_from_ultimate_switching` + `internal`). Tab union `SettingsPage.tsx:52` (10→11); `ModelsTab.tsx:162` one-line extension; TokenForm picker filters as planned. ✅
4. Line-citation drift (Amendment 17): types.ts kind union is at **:123** (not :109/:121/:153); `ImgGenModel` :155, `DecisionModel` :161.
5. tsc 26/11 baseline, tabs-test extension, same-commit hashed-bundle discipline — all correctly planned. ✅
6. Free-text `upstream_model` in the member editor: v1-acceptable (DecisionModelForm precedent), but see Amendment 12's duplicate-`upstream_model` FE hint as cheap mitigation.

### Recommendation: **Mirror the precedent, with Amendments 9/17/18.**
**Confidence: High.**

---

## 9. Consolidated Recommended Architecture

1. **Shadowing** *(FA1 — as planned + Am. 4/5/20)*: single-ID claim (`shadow_model_id`), claims-first `ModelsManager.GetModel`, admin strict-twin bypass, `/v1/models` second-pass hide, delete/disable → reappear; **claim-eviction hook into `pkg/modelscache`** (evict `modelsByID` + `negByID` for old/new shadow IDs on every claim-relevant write); auth evaluates the client string unchanged; ultimate-stops-while-shadowed documented.
2. **Membership** *(FA2 — as planned + Am. 2/12)*: inline `(credential_id, upstream_model, weight)` triples; member key `credentialID + "|" + upstreamModel` **with `"|"` banned in credential IDs at Validate + G3**; N-members-per-target pattern and rotation-orphans-bindings documented; FE duplicate-`upstream_model` hint.
3. **Engine** *(FA3 — as planned + Am. 14/15)*: `pkg/grouplb` clone with the 7-coupling map, **testhooks 1:1**, lazy per-group state (no boot rebind), member-precise cooldowns, verbatim group-scoped `ComputeConversationKey`, `OnGroupChanged` binding drop, deterministic (non-sleep) race tests.
4. **Pipeline** *(FA4 — amended)*: resolver kind-branch in both resolver variants (cached-variant placement pinned); OpenAI `credFailoverPlanLocked` group branch; **NEW Anthropic group-aware failover branch in `internal_handler.go`** (Am. 1 — the plan's single behavioral defect); misroute guards untouched; G7 ultimate exclusion; chat→group fallback posture documented.
5. **Transparency** *(FA5 — amended)*: mask `model` to the client-requested name, group-kind-only, **per-SSE-event buffered patch** (never raw chunk), fail-open-to-verbatim, non-group byte-unchanged regression assert; §2.5 `RequestMetadata` claim corrected (dead seam).
6. **Metering** *(FA6 — amended)*: both rows via **winner-based gating with explicit plumbing** (getter/stamp — not the as-written nonexistent `resolved`), ultimate-branch site excluded, 5s detached contexts, `/v1/messages` no-metering stated, `model_member_selected` on `newlyBound`.
7. **Storage** *(FA7 — as planned + Am. 8/16)*: migration 031 (two nullable columns, both dialects, registry append + contains-test), corrected site arithmetic (SELECT 20→22, INSERT/UPDATE 19→21, inline SELECTs included), no M-1 shadow write, JSON-twin complete, Q11 block-at-delete, `validateGroupMembers` provider divergence.
8. **FE** *(FA8 — as planned + Am. 9/17/18)*: 11th tab `group_models`; GroupModelsTab/Form **(with shadow-target picker)**/MemberEditor (cross-provider); `useGroupModels` + `'group-models'` key; quadruple delete at the **9** confirmed sites; 4 lock-step arms; tsc 26/11; same-commit bundles.

## 10. Plan Amendments Required

**🔴 must-fix before implementation**
1. 🔴 **§3.6.3 / P2 / §9.4 — Anthropic failover branch.** Replace "zero code changes" with a group-aware branch in `pkg/proxy/internal_handler.go` (~`:239-250`): lift the `len(Credentials) <= 1` guard for `IsGroup()` configs, call `grouplb.ExcludeAndReselect(groupID, convKey, memberKey(resolved.CredentialID, resolved.InternalModel), retryAfter)`, rebuild the retry from the member-shaped resolver result (not `h.config.InternalModel`). ~20–30 LOC + test. Add the risk row (first member 429 on `/v1/messages` otherwise fails the request).
2. 🔴 **§3.5 / §5.1 G3 — member-key collision gate.** Reject credential IDs containing `"|"` in `CredentialConfig.Validate` (`pkg/models/credential.go:107`) + G3 defense-in-depth. (`("foo","bar|baz")` vs `("foo|bar","baz")` otherwise collide ⇒ silent wrong-binding.)
3. 🔴 **§3.7 / P2.8 — metering plumbing spec.** `resolved.InternalModel` is not in scope at the metering sites; specify winner-based plumbing (lock-protected `GetResolved()` on `upstreamRequest` or `rc` stamp post-`WaitForWinner`), gate the member row on winner-row-kind == group && resolvedOK.

**🟡 correctness / completeness**
4. 🟡 **P2.2 / §3.4 — claim-eviction hook into `pkg/modelscache`.** On every claim-relevant write: rebuild `claims`, evict `modelsByID[old/new shadow]` and `negByID[old/new shadow]` (60s negative-TTL window); state claim authority vs `CachedModelsConfig.GetModel` ordering. (Corrects research's `pkg/ultimatemodel/modelscache` path — the package is `pkg/modelscache`.)
5. 🟡 **§3.6.4 / §11 — document ultimate-stops-while-shadowed:** while X is claimed, X's ultimate switching silently stops (claims-first returns the group row; G7 forces exclusion).
6. 🟡 **§4 / P2.7 — per-SSE-event masking:** buffer/patch per complete `data:` event, not per raw read-buffer chunk (straddling ⇒ intermittent leak); keep fail-open-to-verbatim.
7. 🟡 **§2.5 — factual correction:** `RequestMetadata{ClientModel,UpstreamModel}` is populated only by `ParseRequest`, which has zero production callers (dead seam); adapter-seam masking is priced out for v1. Research was right, plan wrong.
8. 🟡 **§3.3 — storage site arithmetic:** SELECT columns 20→22 (not 19→21 — that is INSERT/UPDATE only, correct there); `dbModelRow`/`scanModels` 20→22; **add the inline SELECTs at `store.go:858-873` and `:940-955`** to the touch list; re-derive the exact builder-string count at implementation.
9. 🟡 **§6.2 / P3.5 — `shadow_model_id` editor:** clearable chat-kind dropdown in `GroupModelForm` (populated from `useModels()` filtered to chat kind); the field currently has no UI.
10. 🟡 **§3.6.1 — cached-resolver placement:** kind-branch at `store.go:2292` before the `Credentials==0` fall-through (~`:2333`); note the cached-path RLock is held (branch is pure compute — safe).
11. 🟡 **§2.6/§3.7 — metering site anchors:** `:908-934` is the ultimate re-execution branch (not "stream"); true stream `:1446-1472`; non-stream `:1656-1682`; **exclude the ultimate site from member-row metering** and document group-name-only there.
12. 🟡 **§3.2 / Q10 — ops notes:** N-members-per-target pattern for multi-credential targets (legal, cooldown-correct — document so it isn't read as a validation accident) + FE duplicate-`upstream_model` hint (~10 LOC) + rotation-orphans-bindings runbook sentence.
13. 🟡 **§3.7 — explicit sentence:** `/v1/messages` group requests meter nothing (pre-existing Anthropic metering gap, unchanged).

**🟢 hygiene**
14. 🟢 **§3.5/§9.1 — testhooks 1:1:** grouplb clones `testhooks.go` with the 7 named seams (`InjectAllCoolingForTest`, `cooldownUntil`, `bindingBoundAtForTest`, `forceCooldownExpiryForTest`, `InjectPreconditionStateForTest`, `InjectSingleCredExclusionForTest`, sweepHook install) — the deterministic race-test promise depends on them.
15. 🟢 **§3.5 — init posture:** per-group state lazy on first `GetOrSelect`; no boot-time `RebindFromStore` walk (state the choice; cold-start log line covers it).
16. 🟢 **§3.3 — no M-1 shadow write:** both 031 columns are purely additive; note that the credentials_json dual-write pattern does NOT apply.
17. 🟢 **§6.5 — types.ts citations:** kind union at `:123`, `ImgGenModel` `:155`, `DecisionModel` `:161` (plan's :109/:121/:153 are stale).
18. 🟢 **§6.3 — cache-site count note:** 9 mutation sites confirmed; credential/token mutations correctly invalidate no model caches today (research §8.3's "12 sites" is an over-count — do not implement phantom sites).
19. 🟢 **§12 A7 — extend:** member `upstream_model` may collide with an operator model's ID in `model_hourly_usage` (cross-feature blend, cosmetic, accepted).
20. 🟢 **§3.4 — dual-claim edge doc:** both-disabled claims are inert; enable-order resolves via the G8 scan; single-valued `claims` map assumes API-only writes.

## 11. No-Change Endorsements

- **FA2 inline membership** — the "reference gives more for less" challenge answered: it is more-for-more (nested failover surface, new orphan class, pre-provisioned rows); inline stands.
- **FA3 clone** vs generalize-in-place (frozen rule + `-race` flake live there) vs shared core (disproportionate; record as a trigger if a second LB consumer appears).
- **FA1 claim column** vs rename-on-shadow / dual-ID / kind-priority (D is impossible on PK evidence).
- **G1 `Internal=true`** (Q13) — upheld by independent evidence from two workers (~11 dispatch predicates + resolver gate).
- **FA5 mask-don't-leak** (option B fails the transparency requirement; C dead seam).
- **FA6 both-rows** (B/C each fail a stated requirement).
- **FA7 JSON columns + migration 031 + registry-append-with-test + Q11 block-at-delete + G5 provider divergence.**
- **FA8 FE shape** — tab/editor/hook structure, quadruple-delete at 9 sites, 4 lock-step arms, tsc/embed discipline.
- **Q4 sticky design** (hash, in-memory, 24h TTL, 5m janitor, cold-start log, exclude-and-reselect, removal-drop) — requirement 3 fully designed; **G6/G7/G8, flat membership, chat→group fallback posture (Q16), metering success-only semantics.**
- **Phasing P1→P4** — dependency order sound; amendments slot into P1 (2, 8), P2 (1, 3–7, 10–13, 15), P3 (9, 12-FE, 17–18) without re-ordering.

## 12. Decisions Pending (user)

1. **Ultimate-stops-while-shadowed** — accept the documented semantic (a claimed model's ultimate switching halts until unclaimed)? *Recommend: accept + document (Am. 5).*
2. **N-members-per-target** for multi-credential targets — accept the pattern (vs deferring per-target credential pooling to v2)? *Recommend: accept + document (Am. 12).*
3. **Shadow-clearing** — allow clearing `shadow_model_id` via group edit (claim dropped, model reappears)? *Recommend: allow.*
4. **Fallback-reached groups meter the member row** (winner-based gating meters chat→group fallback traffic to the member too) — confirm intended. *Recommend: yes (consistent with Q9's both-rows intent).*

## 13. Open Questions

1. `/v1/image_generation` with a group ID — expected clean upstream failure (imggen dispatches raw bytes; no `GetModel` lookup); one-line verification during P2 (partially unverified).
2. `SettingsPage.tabs.test.tsx` length-assertion shape (per-tab-id vs hard count) — verify at P3.6.
3. Exact SELECT-site enumeration (docs disagree 8/12/16/18 strings) — re-derive from source at P1.4; the arity/placeholder test pins are the real lock.
4. Research §1.3's `pkg/ultimatemodel/modelscache` path citation is wrong (`pkg/modelscache` is the real package) — planner should correct research-codebase.md when next touched.

---

*Aggregation: Architect controller from three verified worker reports (instances fbf2178d / 143e8f61 / 336c529e). The three 🔴 findings (Anthropic failover guard+engine, member-key `|` collision, metering plumbing) were independently re-verified by the architect in-source before inclusion. Dedup applied: FE cache-count findings merged with the 9-vs-12 resolution retained; modelscache findings merged with the path correction retained as most specific; metering anchor corrections merged into one amendment (11).*
