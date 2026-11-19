# PHASE 2 — RUNTIME EXCLUSION ACROSS EVERY SURFACE

**Lane:** `feature/token-authz-allowed-groups` · **Base:** `a3f0335` · **Worktree:** `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-token-authz`
**Read first:** `plan.md` §2 (the contract — Phase 1 already implements it; this phase **consumes** it), §7 (matrix), §8 (per-layer mechanics), §9 (all-excluded failure), §10 (fail-closed), §11 (membership dynamism).
**Phase verdict:** **TIGHTLY COUPLED — internally sequential, and depends on Phase 1's symbols.** It is the phase where every security decision becomes executable. It cannot start before Phase 1's T1.1–T1.6 land (compile) and should not be reviewed piecemeal: the candidate-list invariant (§8.5) is only meaningful once *all* surfaces carry it.

---

## 1. Objective

Make the reach invariant true on every lane and every layer:

> **`I3`: every candidate that enters an execution list is in `Reach(T)` at the moment it enters** — therefore advance (`c.models[i+1]`), hedge (`c.models[0]`), credential-failover and re-entry **inherit** the exclusion with **no token in the hot loop** and **no attempt-time skip** (`D9`, `I4`).

Concretely, this phase:

1. **Fixes the live defect** — the decision GROUP arm no longer returns before any scope evaluation.
2. **Collapses the four entry checks into one predicate** (`D8`) — and thereby removes the latent divergence between the guarded form (`pkg/decision/handler.go:342`, `pkg/imggen/handler.go:267`, `pkg/proxy/handler.go:839`) and the **unguarded** form (`pkg/proxy/handler_anthropic.go:514`).
3. **Makes the member PICK scope-aware** at the single manager-level authority (all four phases of the sweep, including the unguarded **Phase 4**).
4. **Filters every chain/list build** at the four verified points C1–C4.
5. **Sequences the reach decision BEFORE candidate construction** (`D13`) so an out-of-scope request creates no group binding, no member expansion, no sticky pick and no metering.
6. **Distinguishes "empty because scope" (403) from "empty because unservable" (502)** on every arm.

**What must NOT change:** the credential layer (`I13`), the race coordinator (`I3` by construction), the sticky-ordering engine, the group-selection engine's health logic, the lane gates, the v202 quota doctrine, the 30-minute absolute binding TTLs (`P5`), and `pkg/decision/policy.go` (**byte-unchanged**, `P7`).

### 1.1 Leader rulings folded into this revision (2026-11-18 plan-revision pass)

These are **normative over any earlier wording in this file**. Each names the task that carries it.

| Ruling | Task(s) | Contract, one line |
|---|---|---|
| **PD-9 (AMBER)** | **new T2.16** | A shadow/claimed id resolves to the claiming **group** row. The gate authorizes the **RESOLVED target only** — never the client string alone (ADR D1: the unit of authorization is the model that will actually be contacted; the client-requested string is only the entry point into that decision). The 403 quotes the **client-requested claimed string** in `"model"` and never the canonical group id; members are authorized individually by the filter; **alias expansion into `Reach(T)` is declined**. The narrowing of alias-only grants is **INTENTIONAL** and is handled by the **G0-4** read-only cross-check + the release note — **never** by widening the gate. G0 gains a `shadow_model_id` cross-check (Phase 3 T3.10). |
| **PD-5 (AMBER)** | **T2.9** | In `pkg/decision/group.go`, the scope filter on `group.Members` runs **BEFORE** the policy-order call, operating on a **COPY** of the slice (never mutating the cached row), and the **pre-filter vs post-filter base-servable count** decides `403 ScopeExcluded` vs `502 decision_all_members_failed`. |
| **PD-10 (RED)** | **T2.5** + **new T2.12** | `newRaceCoordinatorWithEvents` **refuses** an empty `models` slice (the `cfg.ModelID` fallback at `race_coordinator.go:338-340` is removed) — which requires a **signature change**; callers assert `len(rc.modelList) > 0` after `applyReachFilter` and emit `403 ScopeExcluded` **before** construction. |
| **PD-11 (GREEN)** | **T2.2** + **new T2.13** | One exact precedence order pinned for the decision lane, **derived from reading the actual code** (not assumed), so a disabled/out-of-scope model cannot leak which condition applied. Arch PD-12 folds here: the dead `len(authToken.AllowedModels) > 0` at `pkg/proxy/handler.go:857` is **deleted**, and the structural grep guard forbids `len(.*AllowedModels)` outside `pkg/auth`. |
| **PD-12 (AMBER)** | **T2.4** + **T2.6** | Per-position `ScopeExcluded`: `idx == 0` ⇒ `403`; `idx != 0` (fallback-position group) ⇒ **drop the entry + loud log, continue**; re-entry passes the same `Scope` and **never** emits `502 all_models_failed` when the sole reason for zero attempts was scope. Arch PD-7 folds in: `applyGroupRewrite` returns `pick = nil` whenever `res.GroupID == ""`. Arch PD-11 folds in: the scoped exclude returns an explicit `{Replacement, Pool, OK, ScopeOnly}` outcome. |
| **PD-13 (GREEN)** | **new T2.14** | An explicit, **greppable audit task** for residual `ultimatemodel` / `ExcludeFromUltimateSwitching` references (pass criterion in the task), plus the named **non-scope** classification for `ResolvePeakHourModel` (5 sites) and `SecondaryUpstreamModel` (same-row wire substitution, `I13`). |
| **PD-14 (GREEN)** | **T2.2** + §5 pin | `handler_anthropic.go:486-488` is the 502 `group_unservable` writer; `:530` is `sendModelNotAllowedError` — the **403 envelope is byte-identical across lanes**, and the anthropic scope-empty path emits **that 403**, never the anthropic 502. |
| **PD-15 (AMBER)** | **new T2.15** | Exact line cites for the Anthropic list construction, plus the **corrected** rule for a **group id inside a `FallbackChain`** (gen-10 re-pin: an **entry-chain group IS expanded** into its in-reach member — `handler_functions.go:200-202` + `store_group_rewrite.go:105-181` — so it is never a raw candidate and never 502s; the surviving wart is a group id inside a group **member's** own chain, appended at rewrite time and never re-scanned — documented, unpinned, not a scope escape). |

---

## 2. Files owned (exact)

### 2.1 Production code

| File | Action | What |
|---|---|---|
| `pkg/proxy/handler_helpers.go` | modify | `requestContext` (`:25`) gains `scope *authz.Scope` (placed beside `tokenID/tokenName` `:193-194`); `reset()` (`:207-228`) clears it. |
| `pkg/proxy/handler.go` | modify | (a) create the scope after auth (`:726-729`) via `authz.Resolve`; (b) **hoist** the entry gate from `:818-879` to before the rewrite at `:782` and re-express it through the predicate; (c) the rewrite call `:783` becomes the **scoped** variant and gains the `ScopeExcluded` 403 sub-branch; (d) `applyReachFilter` at the race loop-top (**C1**, `:991`) and after the re-entry list swap (**C4**, `:1248-1249`); (e) `tryGroupReentry` (`:1196-1287`) uses the scoped exclude (offer `:1213`) and the scoped rebuild (`:1231`). |
| `pkg/proxy/handler_anthropic.go` | modify | (a) `anthropicRequestContext` (`:1959-2031`) gains `scope *authz.Scope`; (b) create the scope after `arcTokenID`/conversation key (`:421`/`:449`); (c) **hoist** the entry gate from `:506-533` to before the rewrite at `:472`; (d) scoped rewrite + `ScopeExcluded` 403; (e) `applyReachFilter` at the pre-walk (**C2**, `:539`). |
| `pkg/proxy/handler_groups.go` | modify | (a) `applyGroupRewrite` (`:68-94`) gains a `reach models.ReachFilter` parameter and returns a **`rewriteOutcome` struct** (`list, pick, failGroupID, scopeExcluded` — see T2.4) **plus the PD-12 rule `pick = nil` whenever `res.GroupID == ""`**; (b) new `applyReachFilter(scope *authz.Scope, list []string) []string` (mirrors `applyStickyOrder` `:134`) **which also excludes group-kind chain entries (PD-15)**; (c) `tryGroupReentryAnthropic` (`:452-542`): scoped offer (`:466`, reading `groupExcludeOutcome.ScopeOnly`) + scoped rebuild (`:480`) + filter at `:504`. |
| **`pkg/proxy/race_coordinator.go`** | modify — **NARROWLY (PD-10)** | **T2.12 only:** delete `if len(models) == 0 { models = []string{cfg.ModelID} }` (`:338-340`); `newRaceCoordinatorWithEvents` (`:324`) and `newRaceCoordinator` (`:300`) gain an `error` return for `len(models) == 0`. **Nothing else in this file changes** — no scope predicate, no advance/hedge/failover edit (see §2.3's correction note and §8.4). |
| `pkg/store/database/store_group_rewrite.go` | modify | (a) `rewriteGroupEntries(modelList, conversationKey, reach)` core; `RewriteGroupEntries` becomes the nil-reach wrapper; **new** `RewriteGroupEntriesScoped`; (b) `pickServableGroupMember` (`:160-286`) gains a trailing `reach models.ReachFilter`; (c) new `isReachableMember(id string, reach models.ReachFilter) bool`; applied at **Phase 1 `:205`, Phase 2 `:229`, Phase 3 `:253`, and Phase 4 `:270-284`**; (d) member-chain filter at `:101-102`; (e) `ScopeExcluded` set; (f) index-0 vs fallback-position group handling (T2.6); (g) `ExcludeGroupMember` (`:489`) gains a scoped twin `ExcludeGroupMemberScoped`. |
| `pkg/modelscache/models.go` | modify | `RewriteGroupEntriesScoped` / `ExcludeGroupMemberScoped` forwards (type-assert the inner `models.GroupRewriterScoped`), modelled on `:1301-1306` / `:1314-1319`; **add `var _ models.GroupRewriterScoped = (*CachedModelsConfig)(nil)`** — the decorator-forward trap is a build break, never a silent no-op. |
| `pkg/decision/handler.go` | modify | (a) the MODEL arm (`:340-354`) re-expressed through the shared predicate; (b) the GROUP arm (`:317-326`) passes the scope into `serveDecisionGroup` (signature gains a `*authz.Scope`, or the token already there is resolved inside — recommended: resolve **inside** `serveDecisionGroup` from the `authToken` it already has at `:96`, so the handler keeps no cross-request state); (c) `membersOf` adapter beside `groupLane` (`:836-845`). |
| `pkg/decision/group.go` | modify | (a) new arm **after** the lane gate (`:101-114`) and **before** `buildGroupPolicy` (`:118`): group-level reach check ⇒ 403; (b) build a **member-filtered copy** of the group row and pass THAT to `buildGroupPolicy` at `:118`; (c) the empty-order arm (`:120-123`) needs the scope-vs-unservable distinction (T2.9); (d) `membersOf` helper near `memberRow`/`currentModelRows` (`:519-526`). |
| `pkg/imggen/handler.go` | modify | entry check (`:267-268`) through the shared predicate (entry-only — no chain on this lane). Also fix the stale comments `:263` and `:711` in passing. |

### 2.2 Test files owned by this phase (behavioural content)

`pkg/decision/handler_group_test.go` · `pkg/decision/handler_test.go` (new cases only) · `pkg/proxy/handler_test.go` · `pkg/proxy/handler_anthropic_test.go` · `pkg/proxy/handler_anthropic_misroute_test.go` · `pkg/proxy/handler_anthropic_tokenid_parity_test.go` · `pkg/proxy/handle_chat_misroute_test.go` · `pkg/proxy/handler_groups_shadow_member_test.go` · `pkg/store/database/store_group_test.go` · `pkg/imggen/handler_test.go` · **new** `pkg/proxy/reach_completeness_test.go` · **new** `pkg/proxy/authz_guard_test.go` · **new** `pkg/decision/authz_guard_test.go`.

### 2.3 Files this phase must NOT touch

`pkg/decision/policy.go` (**byte-identical to base** — `P7`, `D6/L3`, `A4`/`A9` ruled out), `pkg/grouplb/**`, `pkg/credentiallb/**`, `pkg/skipcache/**`, `pkg/modelcache/**`, **`pkg/proxy/race_executor.go` (no change at all — `I3` by inheritance)**, `pkg/store/database/store.go` (credential-layer pick sites `:2810`/`:2950`), `pkg/auth/**` (Phase 1 owns), `pkg/ui/server.go` (Phase 1 owns), `pkg/ui/frontend/**` (Phase 3 owns), `k8s/`, `.github/`, `pkg/config/`.

> **DRIFT REPORT + LIST CORRECTION (PD-10).** `pkg/proxy/race_coordinator.go` was previously listed here as untouchable ("no change at all"). That is **no longer true**: T2.12 (PD-10, RED) **must** delete the empty-list fallback at `:338-340` and change the constructor signatures to return an error. The file is therefore **narrowly owned by T2.12** — its diff must be **exactly** the deleted fallback + the two signature/error changes + the 37 mechanical test call-site updates in other files. Everything else in it (advance, hedge, credential failover, `manage()`) stays byte-identical, which is what §8.4 asserts after the correction. `pkg/proxy/race_executor.go` remains fully untouchable — the hot loop still receives a pre-filtered list and needs no change (`I3`).

---

## 3. Tasks (with `file:line` anchors)

### T2.0 — Scope creation on both text lanes (do this FIRST; everything else consumes it)

**chat** — `pkg/proxy/handler.go`, immediately after `:726-729` (token identity population) and **before** the conversation-key/rewrite block at `:731-770`:

```go
// THE authorization scope: Resolve() once per request (plan §2.6).
// nil => unrestricted (anonymous, or a wildcard token). The membersOf
// adapter reads the IN-MEMORY models snapshot (GetModels() /
// currentModelRows(), invalidated on group writes) so this adds NO DB
// read (I14, Ruling 5) — NEVER a per-member GetModelNative SELECT (a
// live read under m.mu.RLock).
rc.scope = authz.Resolve(authToken, h.membersOf(&rc.conf))
```
- Adapter (**Ruling 5, fix-pass 2 — HARD REQUIREMENT**): `func (h *Handler) membersOf(conf *ConfigSnapshot) func(string) []string` reads the **IN-MEMORY models snapshot** the cache layer already maintains — build a group→member-ids index ONCE per request from `conf.ModelsConfig.GetModels()` (the decorator's in-memory rows; the same snapshot class as `pkg/decision`'s `currentModelRows()`), filtered to `kind='group'`; **NEVER a per-member `directGetModel`/`GetModelNative` call** (the approver proved `GetModelNative` is a live SELECT under `m.mu.RLock`, `store.go:1095-1145` — the reader-stall pattern). Invalidated on group writes by the existing cache invalidation path (`OnGroupChanged` / drop-on-write) ⇒ reach staleness == existing model-row cache staleness. Return `nil` for a non-group/absent row.
- **Do not** compute the scope inside `initRequestContext` (`pkg/proxy/handler_functions.go`) — auth has not run there.

**anthropic** — `pkg/proxy/handler_anthropic.go`, right after the conversation-key block (`:449`-region), **before** the rewrite at `:472`:

```go
arc.scope = authz.Resolve(authToken, h.membersOf(&arc.conf))
```
- `anthropicRequestContext` (`:1959-2031`) gains `scope *authz.Scope` — the struct's **first** auth-related field (it has none today; the token is a local at `:402`).

### T2.1 — Hoist the entry gate BEFORE candidate construction (`D13`)

**Why:** today the chat rewrite (`:783`) and the anthropic rewrite (`:472`) run **before** the allow-list (`:818-879` and `:506-533`). A scope filter inside the rewrite would therefore run before the entry decision — and an out-of-scope request would still create a **group binding**, a **sticky pick** and a **member expansion** before being denied. `D13` requires the reach decision first. The status is 403 either way; what changes is the side effects (no binding, no expansion, no metering for a denied request).

**chat** — move the whole `// === MODEL ACCESS CHECK ===` block (`:818-879`, including the closing marker `:879`) to **immediately before** `:772` (the rewrite preamble comment). Re-express it:

```go
if rc.scope != nil {
	// PD-9 (T2.16): authorize the RESOLVED row, never the client string.
	// A claim is an ID-space alias (only groups claim:
	// modelscache/models.go:283-284), so a request naming the claimed
	// model id resolves to the claiming GROUP row — resolvedModel is the
	// claims-first GetModel result (pkg/proxy/handler_functions.go:147).
	// The model actually contacted is that group's MEMBER, so the
	// resolved target is the authorization unit (ADR D1).
	//
	// AUDIT BRIDGE: for every NON-claimed request resolvedModel.ID ==
	// reqLog.Model (the raw client string, handler_functions.go:108/:132),
	// so this strict check is behaviourally IDENTICAL to today's single
	// check. Only the claimed path differs — and there the difference IS
	// the ruling.
	if rc.resolvedModel != nil {
		if !rc.scope.Allows(rc.resolvedModel.ID) {
			/* event + 403 sendModelNotAllowedError(rc.reqLog.Model) — the CLIENT-requested string, never the canonical id */
		}
	} else if !rc.scope.IsUnrestricted() {
		// unknown model + restricted token: fail closed. doc-only arm —
		// the 404 at handler_functions.go:185-194 fires earlier (consumed
		// at handler.go:589-609), so this is unreachable today; it is kept
		// expressed through the PREDICATE (never through len(AllowedModels))
		// so the conjunctive rule cannot regress here. [plan §16 O9;
		// PD-11/arch PD-12 removes the old len(authToken.AllowedModels)
		// form at :857]
	}
}
```
- The 403 writer stays `h.sendModelNotAllowedError(w, rc.reqLog.Model)` (`pkg/proxy/handler.go:525-535`) — **byte-unchanged**, and the argument is the **client-requested** string (PD-9: the claimed id, never the canonical group id).
- Event vocabulary stays `"model_not_allowed"` (`:848`) — **no new outcome string**.
- **Behavioral note for the reviewer:** the reach test for the *known-model* case is identical to today's for a models-only restricted token (exact id match) and **strictly tighter** for a groups-only or groups+models token (a group id in `allowed_models` still admits the group target — `D4` — because `Reach` contains it). For a **claimed** request the gate authorizes the **resolved group id**, so a token that granted only the **shadow alias** is now **denied** — an **intentional** narrowing (PD-9/T2.16), quantified by G0-4 and stated in the release note; a token that granted the **group id** is unaffected.

**anthropic** — move the `// === MODEL ACCESS CHECK (Anthropic lane) ===` block (`:506-533`) to **immediately before** `:472` (the rewrite preamble; **verified**: the rewrite call is at `:472` and the access block starts at `:506` — the earlier `:463` cite is the *comment preamble* for the rewrite, so `:472` is the correct anchor). Re-express with the same shape, including the PD-9 strict gate (T2.16 — **do not** re-derive it here; this block only points at it):
```go
if arc.scope != nil {
	// PD-9 (T2.16): the RESOLVED row authorizes — resolvedModel is the
	// claims-first GetModel result (pkg/proxy/handler_anthropic.go:204).
	// For every non-claimed request resolvedModel.ID == reqLog.Model
	// (the client string, handler_anthropic.go:186), so the strict check
	// is behaviourally identical to today's single check.
	if resolvedModel != nil && !arc.scope.Allows(resolvedModel.ID) {
		/* event + sendModelNotAllowedError(reqLog.Model) — the CLIENT string */
	}
}
```
The `authToken != nil` conjunct is replaced by `arc.scope != nil` (nil ⇒ unrestricted ⇒ no denial) — this is exactly how the **unguarded** form (`:514`) is unified with the guarded forms (`D8`, `I-3` of the clarification set). Keep the comment correction: the old `:511-513` text ("unreachable by construction") stays, plus a line that the scope predicate is now the single mechanism. **`resolvedModel` is non-nil on this lane by construction** — the boundary 404 gate at `:236-261` rejected unknown/non-internal rows — so the strict arm is always meaningful here (unlike chat, where the `else` arm is defensive).
- **PD-14 (verified, pin it):** `:530` is `h.sendModelNotAllowedError(w, reqLog.Model)` — the **same** writer as chat (`handler.go:525-535`), so the 403 envelope is **byte-identical across lanes**. `:485-489` is the **502** writer (`models.NewAnthropicError("api_error", "group_unservable", groupFailMessage(failGroup))`) and must be used **only** for availability. **The anthropic scope-empty path emits the 403, never the 502** — see T2.5's empty-after-filter guard and pin `TestAnthropic_Group_AllMembersOutOfScope_403_EnvelopeParity`.

**Ordering proof obligation:** a test that fails if the rewrite runs before the gate — e.g. `TestChat_OutOfScopeGroup_CreatesNoBinding` asserts the grouplb engine recorded **no** `OnGroupChanged`/binding write for a denied request (T2.1's own pin; listed in §5.2).

### T2.2 — The shared predicate at all four entry sites (collapse, don't duplicate)

| Site | Exact anchor | Before | After |
|---|---|---|---|
| chat | `pkg/proxy/handler.go` (`:818-879` block, moved per T2.1) | `!authToken.IsModelAllowed(modelID)` + a `len(AllowedModels)>0` unknown-model arm | **`rc.scope.Allows(rc.resolvedModel.ID)`** — the **resolved** row (PD-9 strict, T2.16); unknown-model arm via `!scope.IsUnrestricted()` |
| anthropic | `pkg/proxy/handler_anthropic.go:514` (moved) | **unguarded** `authToken != nil && !authToken.IsModelAllowed(reqLog.Model)` | **`arc.scope.Allows(resolvedModel.ID)`** — the **resolved** row (PD-9 strict, T2.16) |
| decision MODEL | `pkg/decision/handler.go:340-354` | `len(authToken.AllowedModels) > 0` then `IsModelAllowed(modelID)` | `if scope := authz.Resolve(authToken, h.membersOf()); scope != nil && !scope.Allows(model.ID) { …403… }` — the **resolved** row; identical to today's check because **claims are group-only** (`modelscache/models.go:283`), so `model.ID == req.Model` on this arm |
| imggen | `pkg/imggen/handler.go:267-268` | `len(authToken.AllowedModels) > 0` then `IsModelAllowed(modelID)` | `if scope := authz.Resolve(authToken, h.membersOf()); scope != nil && !scope.Allows(<resolved image-gen row id>) { …403… }` — the **resolved** row; identical to today's check (same group-only-claim argument). **Implementation note:** the row is currently **discarded** at `pkg/imggen/handler.go:247` (`_, ok = h.resolveModel(modelID)`) — capture it (`row, ok := …`) and authorize `row.ID`; the 404 branch at `:248-260` is unchanged |

**PD-9 consistency note (all four rows):** the "After" column authorizes the **resolved** row everywhere — the client-requested string is **never** an authorization input on any lane. For chat and anthropic this is the only place the *claimed* path can differ (a claimed id resolves to the claiming group row); for the decision MODEL arm and for imggen the resolved id **is** the client string, because claims are installed by **groups only** (`pkg/modelscache/models.go:283-284`, JSON twin `pkg/models/config.go:727-728`). The decision **GROUP** arm is the third claimed-path site and is handled in T2.9 (ARM #1 authorizes `group.ID`, quotes `req.Model`).

**Hard rule (binding clarification #3):** after this phase, **no non-test file calls `IsModelAllowed`** — the four sites were never equivalent (the anthropic one had no length pre-guard) and the divergence becomes a *security* divergence the moment an empty list stops meaning wildcard. The demoted helper stays on `pkg/auth` for its own unit test and for `ScanGrant`-era callers that only need the model axis; the grep guard in **T2.13** enforces the rule (both the `IsModelAllowed` sweep and the `len(…AllowedModels)` sweep).

### T2.3 — Scoped member pick at the single manager authority

**File:** `pkg/store/database/store_group_rewrite.go`.

1. Core + wrappers:
```go
func (m *ModelsManager) RewriteGroupEntries(modelList []string, conversationKey string) models.GroupRewriteResult {
	return m.rewriteGroupEntries(modelList, conversationKey, nil)
}
func (m *ModelsManager) RewriteGroupEntriesScoped(modelList []string, conversationKey string, reach models.ReachFilter) models.GroupRewriteResult {
	return m.rewriteGroupEntries(modelList, conversationKey, reach)
}
func (m *ModelsManager) rewriteGroupEntries(modelList []string, conversationKey string, reach models.ReachFilter) models.GroupRewriteResult  // body = today's :55-113, plus the scope work below
```
2. Predicate:
```go
// isReachableMember is the AUTHORIZATION axis of candidacy. It is kept
// SEPARATE from isServableGroupMemberBase (:348-360, the SHAPE axis) so
// the health/shape doctrine and the permission rule stay separable and
// reviewable (plan §17.2).
func isReachableMember(id string, reach models.ReachFilter) bool {
	return reach == nil || reach.Allows(id)
}
```
3. Apply it at **all four** phases, **after** the existing shape check:
   - Phase 1 `:205`: `if !isServableGroupMemberBase(row, lane) { exclude; continue }` → add a **scope-refusal advance** when `!isReachableMember(pick, reach)`. **CORRECTED MECHANISM (fix-pass A, approver iteration-002 A1 — the earlier "the `seen` guard already prevents a re-offer" rationale is FALSE):** `getOrSelect` returns the **live conversation binding every call** (`pkg/grouplb/engine.go:489-500`) and the sweep guard is `if seen[pick] { break }` (`pkg/store/database/store_group_rewrite.go:197-202`) ⇒ a bare `seen[pick]=true; continue` RE-offers the SAME member and the phase BREAKS (silent demoted-bucket degradation: no binding write, `newlyBound=false`, a false health log, and the dead binding is never repaired). Only the health-benched path advances because it calls `RebindPast` / `RebindBackupPast` (`store_group_rewrite.go:214`, `:241`). **REQUIRED for a scope refusal: call `RebindPast(groupID, conversationKey, seenKeys(seen))` (or `RebindBackupPast` for the backup pool) — a BINDING write, NOT a cooldown write.** Ruling 1's cooldown-skip is untouched (no cooldown/skipcache write, ever); this is a different axis (binding advance). Pinned by **`TestScopeExclusion_NeverWritesCooldown_MultiToken`** (named, fix-pass A/A2).
   - Phase 2 `:229`: same.
   - Phase 3 `:253`: `if !isServableGroupMemberBase(row, lane) { continue }` → add `if !isReachableMember(r.ModelID, reach) { continue }` **before** it is added to the bucket (so it can never be the Phase-4 head).
   - **Phase 4 `:270-284` (THE TRAP — no predicate today):** re-check at the head before returning:
     ```go
     for len(bucket) > 0 {
         head := bucket[0]
         if isReachableMember(head.memberID, reach) { log…; return head.memberID, false, head.pool, false, true }
         bucket = bucket[1:]   // defence in depth: never execute an out-of-reach pick
     }
     ```
     (Skipping forward rather than returning `ok=false` keeps a reachable demoted member of a LATER pool servable.)
4. Member **chain** filter at `:101-102`:
```go
out = append(out, memberRow.ID)
for _, chained := range memberRow.FallbackChain {
	if isReachableMember(chained, reach) { out = append(out, chained) }   // D5: chains are NOT inherited
}
```
   with one aggregate log per dropped chain target.
5. `ScopeExcluded` — set it when the group had ≥1 base-servable member but **every** candidate was scope-filtered. Track `baseServable int` and `scopeFiltered int` inside `pickServableGroupMember` (return them, or use a small out-struct) and set `res.ScopeExcluded = true` **only** for the group whose `FailedGroupID` is being reported **and only when `idx == 0`** (T2.6).

### T2.4 — `applyGroupRewrite` gains the reach parameter

**File:** `pkg/proxy/handler_groups.go:68-94`.

Recommended shape (avoids a 5-value return):
```go
type rewriteOutcome struct {
	list          []string
	pick          *groupPickInfo
	failGroupID   string
	scopeExcluded bool   // the failure was caused by Reach, not availability
}

func (h *Handler) applyGroupRewrite(conf *ConfigSnapshot, modelList []string, conversationKey string, reach models.ReachFilter) rewriteOutcome
```
- Backend selection: prefer `models.GroupRewriterScoped` (type assertion on `conf.ModelsConfig`) and call `RewriteGroupEntriesScoped`; otherwise call `models.GroupRewriter.RewriteGroupEntries` and **log loudly once** (`[authz] models backend lacks the scoped rewrite — permission filtering falls back to the request-layer list filter`) and rely on `applyReachFilter` for fail-closed correctness (the pick may be sub-optimal but can never be out-of-scope after the list filter — **except** the `groupPick.MemberID` consistency rule below).
- **`T3` pick/list consistency assertion:** if `res.MemberID != "" && !contains(res.ModelList, res.MemberID)` after the list filter, log `[authz] PICK/LIST DIVERGENCE group=… member=…` and treat the entry as failed-with-`scopeExcluded` ⇒ 403 (fail closed; never serve a list whose stamped pick is absent).
- **PD-12 fold-in (arch PD-7) — `pick` MUST be `nil` whenever `res.GroupID == ""`.** **VERIFIED at `a3f0335`:** `pkg/proxy/handler_groups.go:80-93` returns `res.ModelList, &groupPickInfo{GroupID: res.GroupID, MemberID: res.MemberID, …}, ""` **unconditionally** once `res.AnyGroup` is true (`:76-78`). When the only group entry is at a **fallback position** (`idx > 0`) and T2.6 drops it, the rewrite leaves `res.GroupID == ""` **but `res.AnyGroup == true`** (`:67` sets it before the drop) — so today's code mints a **phantom pick** with an **empty `GroupID`**, and every downstream consumer treats it as a real group pick:
  - `pkg/proxy/handler.go:1138` (`if rc.groupPick != nil && …`) ⇒ enters the group re-entry path for a request that has **no group**;
  - `tryGroupReentry` (`:1211-1213`) calls `ExcludeGroupMember("" , …)` with an **empty group id**;
  - two `group_*` events are emitted with **empty ids** (`:1152-1158`);
  - `rc.groupTriedMembers[""]` gets written (`:1246`);
  - `rc.reqLog.RouteModel` is stamped with `pick.MemberID` (which is `""` when the group was dropped) — **blanking** the route model on a perfectly healthy non-group request.
  **Required shape** (the struct from the shape above makes this explicit rather than implicit):
  ```go
  if res.GroupID == "" && res.MemberID == "" {
      // PD-12/arch-PD-7: a dropped fallback-position group (or any rewrite
      // that produced no pick) must NOT mint a phantom pick. No GroupID ⇒
      // no group context, and every downstream group path is gated on
      // `rc.groupPick != nil`.
      return rewriteOutcome{list: res.ModelList, pick: nil, failGroupID: ""}
  }
  ```
  Accept criteria: `res.GroupID == ""` ⇒ `outcome.pick == nil` (**always**, even when `res.AnyGroup`); `res.FailedGroupID != ""` keeps today's `return nil, nil, failID` behaviour (list dropped, caller fails clean); a healthy non-group request's `RouteModel` stays empty (not blanked).
- **PD-12 fold-in (arch PD-11) — the scoped exclude must report WHY it produced nothing.** `ExcludeGroupMember` (`pkg/store/database/store_group_rewrite.go:489-514`, **verified name** — the leader's text says `ExcludeGroupMemberScoped`, which does not exist yet and is created by T2.3/T2.11) returns `(replacement string, pool string, ok bool)`. `ok=false` today collapses **two different facts**: (a) there is no alternative member *at all* (single-member group / all base-stale — a genuine availability failure), and (b) alternatives exist but **every one is out of this token's reach** (a scope exhaustion). §9.4 of `plan.md` depends on the difference: a scope exhaustion must **never** be reported as `502 all_models_failed`, and a re-entry's status code follows the SINGLE attempt-count-keyed rule in **plan.md §9.4** (0 upstream attempts ⇒ 403; ≥1 attempt ⇒ terminal 502 unchanged) — see §9.4, do not restate.
  **Required shape (ONE name — fix-pass 2 A3: `models.GroupExcludeOutcome` is the single frozen name for this seam; the PD-11 outcome-struct shape WINS because the `ScopeOnly` discriminator needs it; declared in `pkg/models`, compile-asserted by `var _ models.GroupRewriterScoped = (*CachedModelsConfig)(nil)`):**
  ```go
  // models.GroupExcludeOutcome reports WHY the offer walk produced no replacement.
  type GroupExcludeOutcome struct {
      Replacement string
      Pool        string
      OK          bool   // a usable, in-reach replacement exists
      ScopeOnly   bool   // true when every remaining candidate was filtered by Reach
  }
  func (m *ModelsManager) ExcludeGroupMemberScoped(groupID, conversationKey, memberID string, cooldown time.Duration, reach models.ReachFilter) models.GroupExcludeOutcome
  ```
  - `ScopeOnly = true` **only** when the sweep found ≥1 base-servable candidate and **every** one of them was out of reach; it must be `false` when the group has no servable candidate for availability reasons.
  - The legacy `ExcludeGroupMember` stays byte-unchanged (it delegates with a nil reach ⇒ `ScopeOnly` can never be true) so the ~11 structural fakes and the `models.GroupRewriter` interface are untouched (the house optional-seam pattern, precedent `ClearGroupMemberCooldown` at `:516-525`).
  - Callers (T2.7/T2.8) read `!outcome.OK && outcome.ScopeOnly` to choose "continue the walk / keep the terminal availability path" over "treat as real failure".
- Callers to update (exactly four): `pkg/proxy/handler.go:783` (**verified**), `pkg/proxy/handler_anthropic.go:472` (**verified**), `pkg/proxy/handler.go:1231` (**verified**), `pkg/proxy/handler_groups.go:480` (**verified**). *All four confirmed by `grep -rn "applyGroupRewrite" pkg/`; there is no fifth call site and no test call site.*

### T2.5 — `applyReachFilter` and the four list-build points (C1–C4)

**Helper** (new, in `pkg/proxy/handler_groups.go`, modelled on `applyStickyOrder` `:134`):
```go
// applyReachFilter removes every candidate outside the scope. nil scope
// (wildcard / anonymous) => the list is returned UNTOUCHED (same slice
// identity where possible — zero allocation on the wildcard path).
// Removals are logged once per request with the ids only.
func (h *Handler) applyReachFilter(scope *authz.Scope, list []string) []string
```
It must: preserve relative order, de-duplicate nothing (the list already has no dupes contractually — if a dupe appears, keep the first and drop the rest so the coordinator's index math stays stable), and **never return an empty slice silently** — the caller decides (empty list at the coordinator would fall back to `{cfg.ModelID}` at `race_coordinator.go:338-340`, which could re-introduce an unreachable model ⇒ **that path must be guarded**: if `len(filtered) == 0`, the caller must 403/502 **before** constructing the coordinator).

| Point | Exact anchor | Placement |
|---|---|---|
| **C1** chat race loop-top | `pkg/proxy/handler.go:991` — **before** `applyStickyOrder` (so sticky promotion cannot promote an entry the filter would remove) | `rc.modelList = h.applyReachFilter(rc.scope, rc.modelList)` then the existing sticky call |
| **C2** anthropic pre-walk | `pkg/proxy/handler_anthropic.go:539` — **before** the existing `applyStickyOrder` | same order |
| **C3** anthropic re-entry rebuild | `pkg/proxy/handler_groups.go:504` — **before** the existing `applyStickyOrder` (the rebuild at `:495` re-materialises members) | `arc.modelList = h.applyReachFilter(arc.scope, arc.modelList)` |
| **C4** chat re-entry rebuild | `pkg/proxy/handler.go:1248-1249` — right after `rc.modelList = newList` (the swap) | filter immediately, or rely on C1 on the next loop iteration **and** filter here for reviewability (recommended: both — C4 makes the obligation explicit; C1 makes it durable) |

**Empty-after-filter handling (the load-bearing guard) — **PD-10 folds in here: the constructor now REFUSES an empty list (T2.12), so this guard is the FIRST line of defence and the constructor is the second:**
- chat: if the filtered list is empty **and** `rc.groupPick != nil` ⇒ 403 with `ScopeExcluded` semantics; if `rc.groupPick == nil` (non-group request) the entry gate already caught it ⇒ unreachable; log defensively.
- anthropic: same shape before the `loop:` at `:548`.
- **Never** pass an empty list into `newRaceCoordinatorWithEvents` (`:996`). **Verified reason (kept, even though T2.12 removes the fallback):** `race_coordinator.go:338-340` **today** substitutes `{cfg.ModelID}` for an empty list. T2.12 **deletes that block and changes the signature to return an error** — so after this phase an empty list is a *contract violation* caught twice (caller 403/502 first; constructor error second, never a silent re-expansion). Both halves are required: the caller guard is the correct behaviour, the constructor refusal is what stops a **future** caller from reintroducing the trap.
- **PD-12 fold-in (arch PD-7):** the empty-after-filter verdict must **not** be derived from `rc.groupPick != nil` alone when `applyGroupRewrite` may have minted a phantom pick — after T2.4's `pick = nil` rule, `rc.groupPick != nil` **implies** a real `GroupID`, so the test is sound. State that dependency in the code comment; a reviewer must be able to see that the two tasks are load-bearing for each other.

### T2.6 — `FailedGroupID` index-0-vs-fallback rule (trap T1)

**File:** `pkg/store/database/store_group_rewrite.go:89-92` + the caller.

Today `res.FailedGroupID = row.ID` is set for **any** entry index — the setters are in `pkg/store/database/store_group_rewrite.go` at **`:85`** (decision-lane boundary refusal, `idx == 0` only), **`:91`** (no servable member) and **`:96`** (defensive re-validation miss); the `:91`/`:96` setters are the ones that hit a **fallback**-position group, so an all-unservable/unreachable fallback group 502s the whole request today. (The field is declared in `pkg/models/group_rewrite.go:67`.) New rule:

- `idx == 0` (the requested group): `res.FailedGroupID = row.ID`, and `res.ScopeExcluded` set when the reason was reach ⇒ caller emits **403** (scope) or **502** `group_unservable` (availability) as today.
- `idx != 0` (a fallback-position group): **drop the entry** with a loud log (`[authz] group=%s entry=%d — fallback-position group has no reachable member for this token; entry dropped`) and **do not** set `FailedGroupID`. This mirrors the existing decision-lane boundary refusal at `:82-88`, which already drops fallback-position entries instead of failing the request.
- Rationale: a fallback the token cannot use is not the request's failure; failing the request would let a permission setting convert a healthy primary into a 502.

### T2.7 — Chat re-entry (`tryGroupReentry`, `pkg/proxy/handler.go:1196-1287`)

- Offer walk `:1210-1222`: replace `gr.ExcludeGroupMember(...)` with the **scoped** form `gr2.ExcludeGroupMemberScoped(..., rc.scope)` when available (type assertion), else `ExcludeGroupMember` + a reach check on the returned `replacement` (if out of reach, treat as "not available" and continue the walk). The walk's tried-set logic (`:1217-1221`) is unchanged.
- Rebuild `:1231`: `applyGroupRewrite(&rc.conf, rc.modelListOriginal, rc.conversationKey, rc.scope)`; on `scopeExcluded` ⇒ return `false`; the status that follows is **plan.md §9.4's SINGLE attempt-count-keyed rule** (pointer only): 0 upstream attempts + all-scope-excluded ⇒ 403 at any pass #n; ≥1 attempt ⇒ the unchanged terminal 502.
- List swap `:1248-1249`: filter (C4).
- **Do not** touch `rc.groupTriedMembers`, `groupAdvances`, `RouteModel` stamping (`:1246-1254`) or the `EventMemberFailover` publish (`:1262-1285`).

### T2.8 — Anthropic re-entry (`tryGroupReentryAnthropic`, `pkg/proxy/handler_groups.go:452-542`)

- Offer `:466`: scoped exclude (same pattern as T2.7).
- Rebuild `:480`: scoped + `arc.scope`.
- List swap `:495` + sticky `:504`: filter before sticky (C3).
- Gate `pkg/proxy/handler_anthropic.go:728-765`: unchanged (tried-set harvest `:748-753` untouched).

### T2.8a — CG-2 mechanism: decision group-arm resolution (read before T2.9)

**Resolution chain (verbatim, worktree @ a3f0335):** `cmd/main.go:422` route → `handler.go:248` authenticate → `:282` `modelID=req.Model` → `:305` `model,ok := h.safeResolveModel(modelID)` (`:864-869`) → `h.resolveModel` wired `cmd/main.go:339-341` to `modelsConfig.GetModel` — **CLAIMS-FIRST** (prod: `*modelscache.CachedModelsConfig`). Store twin: `store.go:1063-1086` `GetModel` — claims index hit + owning group Enabled ⇒ returns the **GROUP row**; decorator mirror `modelscache/models.go:496-507` (redirect **before** hit/negative logic). Claims index: `rebuildClaimsLocked` `store.go:942-974` from `SELECT coalesce(shadow_model_id,''), id FROM models WHERE kind='group' AND enabled=1` (decorator twin `models.go:280-288`). Claim-blind twin: `GetModelNative` `store.go:1088-1099` / `models.go:1379-1397`.

**Group arm entry:** kind gate `handler.go:317-338` — `if !model.IsDecision() { if model.IsGroup() && h.groupIsDecisionLane(model.ID) { h.serveDecisionGroup(...); return } ... 400 decision_misroute }` (`:323-326`). MODEL-arm allowlist `:340-354`: `if authToken != nil && len(authToken.AllowedModels) > 0 { if !authToken.IsModelAllowed(modelID) { ...event model_not_allowed... h.sendModelNotAllowedError(w, modelID); return } }` (`:342-353`). `sendModelNotAllowedError` `:784-794` (403, envelope `model=<arg>`).

**BYPASS SPAN:** `handler.go:323-326` → `group.go:96-291` (`serveDecisionGroup`): lane gate `group.go:101-114` (NOT handler.go — pin the file), policy build `:118`, member walk `:142-244`, exhaustion `:246-291`, commit `:293-307`. `authToken` consumed ONLY by metering (`group.go:341` `meterOnSuccess`). ZERO `AllowedModels`/`IsModelAllowed` references in `group.go` + `policy.go`. Model-arm check `:342` unreachable from the group branch (`:325` returns first).

**Shadow/claim semantics:** `shadow_model_id` (models column, migration 031 `:39-40`; `ModelConfig` field `config.go:417-426`) is the INBOUND ID-space claim feeding the claims index; OUTBOUND `/v1/models` hiding is a SEPARATE second mechanism (`handler.go:401-406` build claimedIDs + `:437-439` skip). A member id is "claimed" ⇔ an ENABLED `kind='group'` row has `shadow_model_id=<id>` (computed `rebuildClaimsLocked` `store.go:951-974`). Group ids are never claim targets (`models/group.go:92-94` self-claim forbidden, `:247-249` target-must-not-be-group).

**Per-request-shape behaviour TODAY:**
- (a) group's **own** id → claims miss (self-claim forbidden) → own row → GROUP arm, NO scope check.
- (b) member id claimed by an **ENABLED** decision group → `GetModel` returns GROUP row → GROUP arm, NO scope check — **THE DEFECT**.
- (c) member id claimed by a **DISABLED** group → claim inactive (`enabled=1` filter + `:1078` re-check) → own row → MODEL arm, check FIRES.
- (d) standalone decision id → MODEL arm, check fires.

**Grant-side stays claim-blind (corpus §2.4 CORRECT):** `policy.go:53-57` members read from live row set never claims-first; `GetModelNative` twins; `store_group_rewrite.go:62`.

**Fixture spec for `TestDecision_ShadowClaimed_GroupArm_403OnClaimedString`** (two traps — the stock fixture cannot express this case):
- **Trap (i) — claim-blind stock env:** `newGroupEnv` (`handler_group_test.go:174-310`) maps member ids → member rows (`:259-271`) via `h.SetModelResolver`, so a member-id request hits the MODEL arm = **FALSE GREEN**. The fixture MUST override the resolver so the claimed member id (`"member-a"`) returns the GROUP row (mirroring `store.go:1077-1079`), and keep `lanes["decision-group"]=GroupLaneDecision` (`:255`).
- **Trap (ii) — allow-all stock token:** the seeded token is `AllowedModels:nil` (`:182-187`) = wildcard ⇒ no 403. The fixture token must be **NON-nil and EXCLUDE** the claimed string (e.g. `[]string{"decision-group"}`).
- Rows: group `{ID:"decision-group", Kind:KindGroup, Enabled:true, Members:[{member-a,1}]}` (`:241-248`); member `{ID:"member-a", Kind:KindDecision, Credentials:[...]}` (`:230-238`). Request id = **`"member-a"`** (the claimed string — NOT the group id, which is shape (a)).
- Assertions: 403 envelope `model=="member-a"`; `env.totalHits()==0` (`:327-333`) (today ≥1); `model_not_allowed` event `model:"member-a"` (bus `:301-307`); NO `member_attempted`/`all_members_failed` events (group-arm signatures `group.go:196-203`/`:349-354`).

### T2.9 — Decision lane: group-level check + member filter + the empty-order fork — **PD-5: exact ordering, filter on a COPY**

**File:** `pkg/decision/group.go`. `serveDecisionGroup` already receives `authToken` (`:96`) — the scope is resolved **inside** it, right after the lane gate, so no signature change to the handler and no cross-request state.

**VERIFIED CALL ORDERING at `a3f0335` (read the file; do not deviate):**

| # | Line | Statement |
|---|---|---|
| 1 | `group.go:101` | `lane, ok := h.groupLane(groupID)` |
| 2 | `group.go:102-114` | lane gate ⇒ **400 `decision_misroute`** (group is not a decision-lane group) |
| 3 | **NEW ARM #1 (here)** | scope resolution + **group-level** reach check ⇒ **403** |
| 4 | **NEW ARM #2 (here)** | build the member-filtered **COPY** |
| 5 | `group.go:118` | `policy := buildGroupPolicy(&filtered, h.currentModelRows(), lane)` |
| 6 | `group.go:119` | `order := policy.order()` |
| 7 | `group.go:120-123` | `len(order) == 0` fork: scope-caused ⇒ **403**; genuinely unservable ⇒ `exhaustDecisionGroup` **502** |
| 8 | `group.go:125+` | the attempt loop |

**PD-5 hard requirement — the filter runs BEFORE the policy order, and it must run on a COPY.**

- The order is taken at `:119`, so *any* filter placed after `:119` is a **post-filter** — the policy would have already ordered out-of-scope members, and the `len(order) == 0` fork at `:120` could not distinguish "the group had no servable members" from "scope removed them". **Placement is `at or before :118`, never between `:118` and `:119`** (those are adjacent statements — no seam exists there).
- Arch PD-15 / §3 PD-15 third bullet: **never mutate the cached slice.** `group` comes from the claims-first `GetModel`, which for the DB backend returns a copy (`pkg/store/database/store.go:1073-1086` → `getModelRowByID`), but for the `modelscache` decorator it is a `deepCopyModelConfig` (`pkg/modelscache/models.go:499-503`) and for the JSON twin a struct copy — **all three are copies of the struct, NOT necessarily deep copies of the `Members` backing array**. Slicing `group.Members` with `filtered.Members = filtered.Members[:0:0]` (three-index, cap 0) then appending forces a fresh allocation, which is the only shape that is safe regardless of whether `Members` was deep-copied. **Do not use `group.Members[:0]`** (cap preserved ⇒ writes into the source array) and **do not** `append` onto a nil slice that might alias.

```go
scope := authz.Resolve(authToken, h.membersOf())   // nil => unrestricted

// NEW ARM #1 — the group itself must be in reach (the gen-3 fix).
// Runs AFTER the lane gate (a chat-kind group must stay 400
// decision_misroute — precedence is preserved) and BEFORE any
// candidate construction (no pick, no binding, no attempt).
//
// PD-9 (T2.16): authorize the RESOLVED target — here the group id —
// NEVER the client-requested string. A claimed id resolves INTO this
// group arm (the resolve is claims-first: handler.go:305 / cmd/main.go
// :339-341), so `group.ID` is the row that will be contacted; the
// client string is only the entry point (ADR D1). The 403 however
// QUOTES the client-requested string (req.Model), which for a
// non-claimed request is identical to the group id.
if scope != nil && !scope.Allows(groupID) {
	h.publishEvent(eventTypeDecision, map[string]interface{}{
		"outcome": "model_not_allowed", "model": req.Model,
		"duration_ms": time.Since(startTime).Milliseconds(), "upstream_status": 0,
	})
	h.sendModelNotAllowedError(w, req.Model)   // :784-794, byte-identical writer
	return
}

// NEW ARM #2 — PD-5/PD-15: filter a COPY of the member slice, BEFORE
// buildGroupPolicy. policy.go stays BYTE-UNCHANGED (P7/A4): its
// doctrine is "may reorder candidates, never exclude them" (:15-18)
// and its header forbids the health/suppression import (:1-24).
filtered := *group
filtered.Members = make([]models.GroupMember, 0, len(group.Members))
scopeExcluded := 0
for _, mbr := range group.Members {
	if scope == nil || scope.Allows(mbr.ModelID) {
		filtered.Members = append(filtered.Members, mbr)
	} else {
		scopeExcluded++
	}
}

// PD-5 counting rule: how many members would buildGroupPolicy have
// admitted on the UNFILTERED row? Compute it on `group` (not on
// `filtered`), using the SAME candidacy predicate the policy uses
// (row exists AND row.Enabled AND models.MemberLane(row.Kind) == lane
// — see policy.go:65-92). If that number is > 0 and scopeExcluded > 0
// and the final order is empty, the emptiness is SCOPE-CAUSED.
baseServable := countBaseServableMembers(group.Members, h.currentModelRows(), lane)

policy := buildGroupPolicy(&filtered, h.currentModelRows(), lane)  // :118, unchanged call shape
order := policy.order()                                            // :119, unchanged
if len(order) == 0 {
	// NEW ARM #3 — "empty by scope" (403) vs "empty by unservability" (502).
	// PD-9 (T2.16): quote the CLIENT-requested string here too (req.Model),
	// for the same reason as ARM #1 — a claimed id must never leak the
	// canonical group id in the body. For a non-claimed request
	// req.Model == groupID, so nothing changes.
	if scope != nil && scopeExcluded > 0 && baseServable > 0 {
		h.sendModelNotAllowedError(w, req.Model)   // 403, NEVER 502
		return
	}
	h.exhaustDecisionGroup(w, groupID, 0, 0, 0, startTime)   // :120-123, unchanged 502
	return
}
```

- **Why `baseServable` must be counted on the UNFILTERED row:** if the group's members are *all disabled*, `baseServable == 0` ⇒ the empty order is genuine unservability ⇒ **502**, even if `scopeExcluded > 0` (a disabled out-of-scope member is still a disabled member — availability, not permission). The two counts are independent and the fork reads both. This is the assertable core of R2.8 and the pin is `TestDecision_Group_AllMembersOutOfScope_403_Not502` **against** `TestChat_Group_AllMembersDisabled_Still502`.
- `countBaseServableMembers` may be inlined; if a helper is extracted it lives in `pkg/decision/group.go` (**never** in `policy.go` — that file must stay byte-identical).
- `membersOf` adapter: `func (h *Handler) membersOf() func(string) []string` using `h.memberRow` (`pkg/decision/group.go:508-517`) over `h.currentModelRows()` (`:519-526`). Both are already claim-blind by construction (`memberRow` walks `GetModels()`), which is exactly what `membersOf` needs.
- The MODEL arm (`pkg/decision/handler.go:340-354`) is replaced per T2.2. **The precedence is untouched**: the group arm still returns at `:324` (`h.serveDecisionGroup(...)`) **after** the kind gate, so a chat-kind group stays **400 `decision_misroute`** (`:334-336`, `P4`) — but now the group arm does *its own* scope evaluation, closing gen 3.

> **DRIFT REPORT (symbol naming — required).** The leader's ruling text says scope-filtering must run before **`policy.SelectMembers`**. **No such symbol exists at `a3f0335`.** Verified: `grep -rn "SelectMembers" --include=*.go .` ⇒ **zero hits**. The code's real symbols are:
> - `buildGroupPolicy(group *models.ModelConfig, rows []models.ModelConfig, lane models.GroupLane) groupPolicy` — `pkg/decision/policy.go:58`
> - `(p groupPolicy) order() []policyMember` — `pkg/decision/policy.go:110`
> - the caller `pkg/decision/group.go:118` / `:119`
>
> The ruling's intent maps exactly onto "before `buildGroupPolicy` at `:118`". This file uses the code's real names throughout. The leader's text is directionally right and symbolically stale — no implementation ambiguity remains.

- **Done when:** the three arms are implemented in the order above; `pkg/decision/policy.go` shows a 0-byte diff; `TestDecision_Group_NotGranted_403_ZeroAttempts`, `TestDecision_Group_Granted_200`, `TestDecision_Group_AllMembersOutOfScope_403_Not502`, `TestDecision_GroupMember_OutOfScope_Excluded`, `TestDecision_ChatKindGroup_Misroute400_NotScope403`, `TestDecision_ShadowClaimed_GroupArm_403OnClaimedString` all show a literal `--- PASS`; and a unit pin asserts the cached row's `Members` slice is **byte-identical** before/after the filter (`TestDecisionGroup_FilterDoesNotMutateCachedRow`). **PD-9 detail:** ARM #1 authorizes `group.ID` (the resolved target) and ARM #1/ARM #3 quote **`req.Model`** (the client-requested string) in the 403 body — for a non-claimed request `req.Model == groupID`, so nothing changes there, and for a claimed request the canonical group id never appears in the body.

### T2.10 — image-gen entry

`pkg/imggen/handler.go:267-268` → the shared predicate (T2.2). No candidate list exists on this lane (`pkg/imggen/handler.go:3`: image-gen models have no `FallbackChain`) — state that in the comment so a future reader does not add a filter loop.

### T2.11 — Wire the scoped seam through the decorator (the compile-time guard)

`pkg/modelscache/models.go`:
```go
func (c *CachedModelsConfig) RewriteGroupEntriesScoped(modelList []string, conversationKey string, reach models.ReachFilter) models.GroupRewriteResult {
	if gr, ok := c.inner.(models.GroupRewriterScoped); ok { return gr.RewriteGroupEntriesScoped(modelList, conversationKey, reach) }
	return c.RewriteGroupEntries(modelList, conversationKey)   // no capability => unfiltered; the request layer still filters
}
func (c *CachedModelsConfig) ExcludeGroupMemberScoped(...) models.GroupExcludeOutcome  // ONE shape — the PD-11 outcome struct (A3 fix-pass 2)
var _ models.GroupRewriterScoped = (*CachedModelsConfig)(nil)   // NEW — production binding must not be a silent no-op
```
**Why the assertion:** production always runs through this decorator (`CACHE_LAYER_ENABLED` defaults ON); a missing forward would be a **silent no-op in production while raw-manager tests stayed green** (the documented modelscache-decorator-forward trap at `pkg/proxy/handler_helpers.go:908-918`).

### T2.12 — **PD-10 (RED): the race coordinator must REFUSE an empty model list; callers must assert before construction**

**Files:** `pkg/proxy/race_coordinator.go` (constructor), `pkg/proxy/handler.go:990-996` (the sole production construction site), `pkg/proxy/race_coordinator.go:300-302` (the 6-arg test wrapper).

**VERIFIED at `a3f0335` (exact):**
- `pkg/proxy/race_coordinator.go:324-337` — `newRaceCoordinatorWithEvents(ctx, cfg, req, rawBody, models []string, eventBus, requestID, interleaved, engine, conversationKey, liveFirstByteGate, sticky) *raceCoordinator`
- `:338-340` — `if len(models) == 0 { models = []string{cfg.ModelID} }` ← **the hidden 5th list-construction fallback**
- `:300-302` — `newRaceCoordinator(...)` delegates to the 12-arg form with `nil` bus etc. (**test-only**; `grep -rn "newRaceCoordinator(" pkg/ | grep -v _test` returns nothing ⇒ zero production callers)
- `pkg/proxy/handler.go:996` — the **only** production call of `newRaceCoordinatorWithEvents` (`grep` confirms 1 hit outside `_test.go`); **42 call sites / 11 files (fix pass C — measured with the corpus's own metric; supersedes the earlier 37/11 and 92/13 provisional figures)**.

**Why this is RED:** after `applyReachFilter` (T2.5) a scope-empty list is a legitimate intermediate state. Today it would reach the constructor and be **silently re-expanded to `{cfg.ModelID}`** — an out-of-scope model re-introduced by the *fallback itself*, converting the intended `403 ScopeExcluded` into a real upstream attempt and a `502`. That is the exact defect class the commission exists to close.

**Do (three parts; all three are required — the signature change alone is not enough):**

1. **Remove the fallback and refuse.** The constructor must **return an error** on `len(models) == 0`, which means a **signature change**:
   ```go
   // PD-10: an empty list is a CONTRACT VIOLATION, never an invitation to
   // guess. Callers must have already decided 403 (scope) or 502
   // (unservability) BEFORE construction; re-expanding to cfg.ModelID here
   // silently re-introduced an out-of-scope model (the commission's defect).
   func newRaceCoordinatorWithEvents(
       ctx context.Context, cfg *ConfigSnapshot, req *http.Request, rawBody []byte,
       models []string, eventBus *events.Bus, requestID string, interleaved bool,
       engine *credentiallb.Engine, conversationKey string, liveFirstByteGate bool,
       sticky *stickyorder.Engine,
   ) (*raceCoordinator, error) {
       if len(models) == 0 {
           return nil, fmt.Errorf("race coordinator: empty model list (caller must resolve 403/502 before construction)")
       }
       ...
   }
   ```
   - The `:338-340` block is **deleted**, not wrapped.
   - `newRaceCoordinator` (`:300-302`) becomes `(*raceCoordinator, error)` too and forwards the error verbatim.
2. **Update the test call sites (fix pass C: measured **42 / 11 files**; the "both constructors 98 / 13" figure is deprecated).** Mechanical: `coord, err := newRaceCoordinatorWithEvents(...)`; `if err != nil { t.Fatalf(...) }`. Every existing test passes a non-empty `modelIDs` today, so no assertion changes — but the compile break is **deliberate**: it forces the author of each site to see the new contract. Count them from the compiler, never from this table (`go vet ./pkg/proxy/` will enumerate; 11 files per the grep above).
3. **Caller-side assert BEFORE construction** (`pkg/proxy/handler.go`, immediately after the C1 filter at `:991` and before `:996`):
   ```go
   if len(rc.modelList) == 0 {
       // Scope-empty (or an internally-empty build): decide HERE, never in
       // the constructor. A group pick means the group was in reach but no
       // member was → scope exclusion. No pick means the entry gate already
       // caught it (defensive).
       if rc.groupPick != nil {
           /* event "model_not_allowed" + sendModelNotAllowedError(w, rc.reqLog.Model) + return */
       }
       /* defensive: log [authz] EMPTY LIST with no pick + emit the availability path */
       return
   }
   ```
   `handler_groups.go` has **no** construction site (verified: the only `newRaceCoordinatorWithEvents` outside `pkg/proxy/race_coordinator.go` is `handler.go:996`) — so the leader's "`handler.go` / `handler_groups.go` MUST assert" resolves to **`handler.go` only**, and `handler_groups.go`'s obligation is the `applyGroupRewrite` outcome (T2.4). **DRIFT REPORT:** the ruling named `handler_groups.go` as a caller; at `a3f0335` it is not one.

**Done when:** the empty-list fallback is gone; `(*raceCoordinator, error)` is the signature; all test sites compile (42/11 measured, fix pass C); the caller asserts pre-construction; and the **dedicated regression pin** `TestRaceCoordinator_EmptyModelList_Refused` (Phase 3 §5 cross-listed, Phase 2 §5.3) shows a literal `--- PASS`: (a) `newRaceCoordinatorWithEvents(..., []string{}, ...)` returns `err != nil` and `nil` coordinator — i.e. **no `{cfg.ModelID}` re-expansion**; (b) an end-to-end scope-empty group request returns **403** with the fake upstream's call counter **== 0** (proving no attempt was made); (c) a scan assertion that `pkg/proxy/race_coordinator.go` no longer contains the literal `models = []string{cfg.ModelID}`.

### T2.13 — **PD-11 (GREEN): pin ONE precedence order for the decision lane, derived from the code — plus arch PD-12 (delete the dead per-axis guard)**

**First: report what the code REALLY does.** Read all four lanes and record the observed order. **Observed at `a3f0335` (verified by reading each file):**

| Lane | Observed error precedence (line cites) | Notes |
|---|---|---|
| **chat** (`pkg/proxy/handler.go`) | 503 `config_store_unavailable` / 404 `model_not_found` in `initRequestContext` (`handler_functions.go:160-194`, consumed at `handler.go:589-609`) → **400 `decision_misroute`** (`:653-695`) → **401** (`:700-723`, `sendAuthError` `:514-522`) → **403** (`:818-879` model-access block) → runtime 502 | The 404 fires **before** auth (the resolve happens in the context init) |
| **anthropic** (`pkg/proxy/handler_anthropic.go`) | 503 (`:211-228`) → 404 `model_not_found` (`:236-261`) → **400 `decision_misroute`** for a decision model (`:273-290`) / a decision group (`:295-309`) / a chat group carrying a decision member (`:310-323`) → **401** (`:402-421`) → group rewrite (`:472`) → **403** (`:506-533`) → 502 `group_unservable` (`:485-489`) | **The 403 block runs AFTER the rewrite** — T2.1 hoists it |
| **decision** (`pkg/decision/handler.go` + `group.go`) | **401** (`handler.go:255`, `sendAuthError` `:777`) → **400** `invalid JSON` (`:266-272`) → **400** `missing_model` (`:274-283`) → **400** validators (`:287-297`) → **404 `model_not_found`** (`handler.go:305-315`) → **400 `decision_misroute`** (`:317-337`) → group arm dispatch (`:322-326`) → *inside `serveDecisionGroup`:* **400 `decision_misroute`** for a non-decision-lane group (`group.go:101-114`) → group-level scope **403** (new ARM #1) → member filter → **403 / 502** (`group.go:120-123`) → **403** model-arm scope (`handler.go:340-354`) → 502 | Note **401 precedes 404** on this lane and **follows** it on chat/anthropic (chat authenticates after the resolve, decision authenticates before) |
| **imggen** (`pkg/imggen/handler.go`) | **401** (`:203-212`) → **400** `missing_model` (`:231-239`) → **404** (`:247-260`) → **403** (`:267-278`) | Same shape as decision: auth first |

**DECIDED — the pin for `pkg/decision/handler.go` (and `group.go`), stated as the required final order:**

```
401 (auth)  →  404 (unknown model)  →  400 decision_misroute (kind/lane)  →  403 (scope)  →  502 (unservable)
```

Rationale, in order:
1. **401 first is already true and is correct** on this lane (`handler.go:255` before `:305`) — an unauthenticated caller must not learn whether a model exists. **Do not move it.** (This differs from chat/anthropic, where the resolve precedes auth; the discrepancy is PRE-EXISTING and out of scope — but the plan must say so, or a reviewer will "fix" one lane into the other.)
2. **404 before 403:** an unknown id gets `model_not_found`, not `access_denied` — that is today's behaviour on every lane (`chat:589-609`, `anthropic:236-261`, `imggen:247-260`, `decision:305-315`) and it is also the **safe** order: a 403 for an unknown id would confirm "this token is restricted" while telling the caller nothing true about the id.
3. **400 `decision_misroute` before 403:** the kind/lane gate is **route correctness**, not permission — a chat model posted to `/v1/decisions` must get the same 400 regardless of the token (`TestDecision_ChatKindGroup_Misroute400_NotScope403` pins it). Putting scope first would make the response **token-dependent**, leaking the existence of a decision-lane misconfiguration only to restricted tokens.
4. **403 before 502:** the group arm's scope arms (ARM #1 and ARM #3's scope branch) must fire **before** `exhaustDecisionGroup` — otherwise an all-out-of-scope group reports `decision_all_members_failed`, i.e. an availability error for a permission denial.

**The requirement the ORDER exists to satisfy — no leak on a disabled + out-of-scope model:** a model that is both DISABLED and OUT-OF-SCOPE must produce **one** answer, and it must not reveal which condition applied. **Observed reality to state explicitly:** this codebase has **no disabled-model rejection on any lane** — `GetModel`/`GetModelNative` return disabled rows for non-group ids (`pkg/store/database/store.go:1073-1099`; the doc comment at `pkg/proxy/handler_functions.go:173-177` says it outright: "*GetModel never filters `enabled` for non-group rows*"), and the only `Enabled` gates in the request path are the **group-member** shape check (`pkg/store/database/store_group_rewrite.go:348-360`) and the claims index (`store.go:1078`, `modelscache/models.go:500`). Consequence: on the four lanes a disabled **standalone** model is served, not rejected — so "disabled vs out-of-scope" cannot leak through an ordering choice on those lanes because **the disabled case never produces an error at all**. **Therefore:**
   - On the **model arm**, the order above is complete: 401 → 404 → 400 → 403. Nothing else can fire.
   - On the **group arm** (CORRECTED to landed reality, gen-10 re-pin — the earlier "a disabled group resolves as unknown (404)" claim was FALSE): `GetModel` / `getModelRowByID` (`store.go:1102-1140`) applies **NO `enabled` filter for direct IDs**, and the handler never filters `enabled` on the group arm ⇒ a **standalone disabled model OR a disabled group named by its OWN id resolves to that row and IS SERVED**. What IS disabled-filtered is ONLY **(i) the group's SHADOW CLAIM** (`rebuildClaimsLocked` uses `enabled = 1`, and `GetModel` checks the owning group's `Enabled` at `store.go:1078`) — so a disabled group's *shadowed alias* resolves to the member's own row (404 if none) — and **(ii) group MEMBERS** (`policy.go:71-73` / `isServableGroupMemberBase` ⇒ all-disabled members ⇒ 502). **The no-leak property is STRUCTURAL, not an error-shape choice:** the envelopes (`403 access_denied`, `502 decision_all_members_failed`, `200`) never distinguish disabled from out-of-scope — `baseServable == 0` ⇒ 502 wins on disabled+out-of-scope, and a healthy sibling next to a disabled+out-of-scope member ⇒ 200 without attempting or naming the skipped member. Pinned by `TestDecision_DisabledVsOutOfScope_NoLeak` (LANDED `1e65774`), sub-test D `group_row_disabled_does_not_leak_either`.
   - **Do not add a disabled-model gate** in this phase. It would be a new error class on four lanes, it would change behaviour for live tokens, and it is not required by the commission. **State this in the plan so nobody bolts one on "for symmetry".**
   - The **one place** the distinction must be asserted is the **decision group's** `403`-vs-`502` fork (ARM #3): all-out-of-scope ⇒ 403; all-disabled ⇒ 502 — and those two are distinguishable **by design** (they are different failures with different operator remedies). The pin is `TestDecision_Group_AllMembersOutOfScope_403_Not502` vs `TestChat_Group_AllMembersDisabled_Still502`.

**Arch PD-12 folds into this task — delete the dead per-axis guard.**
- **VERIFIED:** `pkg/proxy/handler.go:857` — `} else if len(authToken.AllowedModels) > 0 {` — the unknown-model fail-closed arm. It is **dead today**: the `:589-609` 404 fires before this block for any unresolvable id, so `modelID == ""` with a resolved model is unreachable. It nonetheless **violates the one-predicate rule** (`D8`) and would become a **live leak** the moment external pass-through were re-introduced (it reads the model axis only, so a groups-only restricted token would take the `else` branch and be allowed).
- **Do:** the hoisted gate (T2.1) replaces the whole `:818-879` block, including `:857`, with the scope-predicate form. **Delete it; do not port it.** The T2.1 shape (`else if !rc.scope.IsUnrestricted()`) is the correct expression and is already written there.
- **Extend the structural guard:** `TestPackage_ImportGuard_NoIsModelAllowedInLanes` (P-G22b) gains a second assertion — **no non-test file outside `pkg/auth` contains `len(` followed by any expression ending in `AllowedModels`**: `grep -rnE 'len\([^)]*AllowedModels\)' --include=*.go pkg/ | grep -v _test | grep -v '^pkg/auth/'` must be **empty**. Today it would find `pkg/proxy/handler.go:857`, `pkg/decision/handler.go:342`, `pkg/imggen/handler.go:267` — after T2.2/T2.13 it finds **zero**. A negative control (a synthetic fixture string) must FIRE.

**Done when:** the precedence table above is in the phase file and in the code comments at the hoist site; `TestDecision_ChatKindGroup_Misroute400_NotScope403` + `TestDecision_Group_NotGranted_403_ZeroAttempts` + `TestDecision_Model_NotGranted_403` + `TestAuth_Anonymous_401_AllLanes` all green; `handler.go:857` is gone; the extended grep guard passes with a firing negative control, and the four `len(...AllowedModels)` sites are documented as **removed** (not merely unused).

### T2.14 — **PD-13 (GREEN): the residual-escalation AUDIT (greppable, with a pass criterion) + the named non-scope classification**

**Why an explicit task:** "we grepped once" is not a durable control. `pkg/ultimatemodel/` was deleted with the external-upstream retirement, and the whole security argument of this commission assumes **no other path can escalate a model choice around `Reach(T)`**. That assumption must be re-checkable by a future reader in one command, not by trusting a sentence in a plan.

**A. The audit (repo-wide, read-only — no code change expected).**

**VERIFIED at `a3f0335`:**
- `ls pkg/` ⇒ **no `ultimatemodel`** directory (present: auth, bufferstore, config, credclass, credentiallb, crypto, decision, events, grouplb, imggen, logger, loopdetection, mcp, memlimit, middleware, modelcache, models, modelscache, providers, proxy, proxyheader, skipcache, stickyorder, store, supervisor, toolcall, toolrepair, ui, usage).
- `grep -rn "ExcludeFromUltimateSwitching" --include=*.go .` ⇒ **zero hits** (non-test AND test).
- `grep -rni "ultimate" --include=*.go pkg/` ⇒ **19 hits, ALL comments or test-fixture column names**: comments at `pkg/ui/server.go:1079`, `:1431`, `:1635`, `:1939`; `pkg/providers/openai.go:984`, `:995`; `pkg/providers/interface.go:241`; `pkg/providers/opencodego.go:59`; `pkg/proxyheader/proxyheader.go:18`; `pkg/stickyorder/doc.go:54`; **and** `ultimate_model TEXT` in five `pkg/mcp/*_test.go` DDL fixtures plus `pkg/auth/store_test.go:523/:815` test-section comments and five `pkg/proxy/*_test.go` comment sites. **Zero non-test, non-comment, executable references.**

**Required output of the audit (append to this task when executed, as a gate artifact):**
1. `ls pkg/ | grep -i ultimate` ⇒ empty.
2. `grep -rn "UltimateModel\|ExcludeFromUltimateSwitching" --include=*.go pkg/ cmd/ | grep -v _test.go | grep -v '^\s*//' | grep -v '// '` ⇒ **empty** (every remaining hit is a comment or a test fixture).
3. `grep -rn "\.Allows(\|authz\." --include=*.go pkg/ | grep -v _test | grep -v "^pkg/authz/" | grep -v "^pkg/proxy/\|^pkg/decision/\|^pkg/imggen/"` ⇒ **empty** (no fourth consumer).
4. **A structural test** — `TestPackage_NoUltimateEscalation` in new `pkg/proxy/authz_guard_test.go`: walks `pkg/` (and `cmd/`) sources, asserts **no non-test file** contains `UltimateModel` or `ExcludeFromUltimateSwitching` outside a comment line, **with a negative control that must FIRE** (inject the string into a temp fixture and assert the checker catches it). Rationale for a test rather than a documented grep: the documented-grep gate is only as durable as the last person who read the doc; the test runs on every `go test`.

**Pass criterion (all four):** 1–3 produce empty output **and** (4) shows a literal `--- PASS`. Anything else is a **finding**, not a waiver — a live escalation hook would invalidate the commission's core premise and must be reported before the gate.

**B. The named NON-SCOPE classification (fold arch PD-13 into the phase's scope table).**

Both of these substitute the **upstream wire string within the SAME already-admitted `models.id` row**. They do **not** select a different row, so they cannot widen reach and are **explicitly out of scope** (ADR D6/L5 + `I13`):

| Mechanism | Exact sites (verified `a3f0335`) | Classification |
|---|---|---|
| `ResolvePeakHourModel` | `pkg/proxy/race_executor.go:320`; `pkg/proxy/internal_handler.go:322`; `pkg/store/database/store.go:2684`; `pkg/modelscache/models.go:1033`; `pkg/models/config.go:1270`; definition `pkg/models/peak_hours.go:146` **(6 sites total — 5 call sites + the definition; the brief said 5)** | **NON-SCOPE** — swaps `InternalModel` inside the row the coordinator already held. `Reach(T)` was evaluated on the row id at construction; the wire string never enters a list. |
| `SecondaryUpstreamModel` (hedge) | `pkg/proxy/race_executor.go:541` (guard), `:545` (substitution), `:558` (event payload) | **NON-SCOPE** — hedge reuses `c.models[0]` (`race_coordinator.go:421-435`, `:435`) on a **secondary credential**; the model identity is unchanged. Pinned at the set level by `TestHedge_And_CredFailover_NeverIntroduceModel` (P-G10). |

**Done when:** the audit artifact (4 items) is recorded in the gate RESULTS file and `TestPackage_NoUltimateEscalation` shows `--- PASS`; the non-scope table above is present in this phase file **and** cross-referenced from Phase 3's ship note (T3.12) so a reader of the release notes learns the same classification.

### T2.15 — **PD-15 (AMBER): pin the Anthropic chain construction, and fail closed on a group id inside a `FallbackChain`**

**Read and pin the EXACT construction lines. VERIFIED at `a3f0335` — `pkg/proxy/handler_anthropic.go`:**

| Line(s) | Statement |
|---|---|
| `:202-205` | `var resolvedModel *models.ModelConfig` / `resolvedModel = conf.ModelsConfig.GetModel(originalModel)` |
| `:326-336` | **the list construction:** `:326` comment, `:327` `var modelList []string`, `:328` `if resolvedModel != nil {`, `:330` `modelList = make([]string, 0, 1+len(resolvedModel.FallbackChain))`, `:331` `modelList = append(modelList, resolvedModel.ID)`, `:332` `modelList = append(modelList, resolvedModel.FallbackChain...)`, `:333-335` the unknown-model arm `modelList = []string{originalModel}` |
| `:356` | `modelList: modelList,` (assigned into `anthropicRequestContext` at `:356`, struct field at `:1965`) |
| `:472` | `if newList, pick, failGroup := h.applyGroupRewrite(&arc.conf, arc.modelList, arc.conversationKey); …` — **the group rewrite** |
| `:491-492` | `arc.modelListOriginal = arc.modelList` / `arc.modelList = newList` (the swap) |
| `:506-533` | the `// === MODEL ACCESS CHECK (Anthropic lane) ===` block (`:514` the unguarded predicate, `:524-528` the event, `:530` `h.sendModelNotAllowedError(w, reqLog.Model)`) |
| `:539` | `arc.modelList = h.applyStickyOrder(&arc.conf, arc.modelList, arc.conversationKey, arc.reqID)` |
| `:548` `:550` | `loop:` / `for modelIndex, currentModel := range arc.modelList {` |

**The contract to pin (this is what "filtered by `scope.Allows` at construction time" means for this lane):**
1. **Construction is at `:330-332`, and it is NOT scope-filtered at that point** — it is built from `resolvedModel.ID + resolvedModel.FallbackChain`, *before* auth (`:402-421`), *before* the rewrite (`:472`), and *before* the access check (`:514`). **`Reach(T)` does not exist yet at `:330`.** Therefore C2 (`:539`) is the first point where the list can be filtered, and **T2.1's hoist is what makes the filter meaningful** — the entry 403 at the hoisted position means an out-of-scope *request* never reaches the walk at all, while C2 removes out-of-scope *chain entries*.
2. **Chat is identical in shape** — `pkg/proxy/handler_functions.go:196-206` (construction: `:198-202` `[resolvedModel.ID] + FallbackChain`, `:203-205` unknown arm) mirrors it; C1 at `handler.go:991` is the filter point.
3. **The 403 writer is shared** (PD-14, verified): `pkg/proxy/handler_anthropic.go:530` calls `h.sendModelNotAllowedError(w, reqLog.Model)` — the **same** method as chat (`pkg/proxy/handler.go:525-535`, `"type":"access_denied"`, message `"model not allowed for this token"`, `"model": <client string>`). So the anthropic 403 envelope is **byte-identical** to chat's; the anthropic-specific writer at `:485-489` is the **502 `group_unservable`** path (`models.NewAnthropicError("api_error","group_unservable",…)`), which must **not** be used for scope. **Pin: `TestAnthropic_Group_AllMembersOutOfScope_403_Not502` asserts the 403 body is the chat-shaped `{"error":{"type":"access_denied",…,"model":…}}` — no `api_error` type, no `group_unservable` code.**
4. **PD-15's fail-closed rule — a GROUP id inside a `FallbackChain`.** `RewriteGroupEntries` is **single-pass** (`pkg/store/database/store_group_rewrite.go:55-113`: it iterates `modelList` once and expands each group **entry**; a chain element appended at `:102` is never re-scanned). So a model `A` with `FallbackChain: ["G"]` where `G` is a group id yields an execution list containing the raw group id. `Reach(T)` may well contain `G` (a group grant), so `applyReachFilter` **keeps** it, and then:
   - the walk reaches `currentModel == G` → `directGetModel(&arc.conf, G)` (`handler_anthropic.go:799`) returns the **group row**, which carries **no credentials** (`members_json` only) → `ResolveInternalConfigWithAffinity` returns `ok=false` (`pkg/store/database/store.go:2767-2773`: `len(flatRefs) == 0` ⇒ `return ResolvedCredential{}, false`) → the attempt fails and the request ends in a **runtime 502**.
   - **This is fail-CLOSED (no escalation), so it is not a security defect** — but it is a **silent availability defect** and an **undocumented shape**, and worse, it is *scope-dependent*: a wildcard token hits it, a restricted token might have its `G` filtered and get a clean 403 instead. That asymmetry is the reason to rule on it.
   - **RULED:** such a chain entry is **EXCLUDED at construction** (treated exactly like an out-of-reach member), never admitted as a model id. Implement in `applyReachFilter` (single place, both lanes) — **do not** add a second check in the loop:
     ```go
     // PD-15: a chain entry that names a GROUP is never a model id. The
     // rewrite is single-pass and expands only list ENTRIES, so a chain
     // group id survives into the execution list; it would resolve to a
     // credential-less group row and 502. Excluded here (fail closed,
     // loud log 1x per request), identical on both lanes.
     ```
     Detection uses the **claim-blind** row, then `row.IsGroup()` (`pkg/models/config.go:157-159`). `nil` row ⇒ keep (it is an unknown/raw name — today's passthrough shape, unchanged). **Correction (gen-10 re-pin):** the landed rewrite EXPANDS a group-kind **entry** into its in-reach member (`store_group_rewrite.go:105-181`), so an **entry-chain** group is never admitted as a raw candidate and never 502s (pinned `TestChainGroupEntry_NeverAdmitted_No502`); the surviving wart is a group id inside a group **member's** own chain.
   - **Write-time validation note (NOT this phase's work):** the durable fix is rejecting a group id in `FallbackChain` at model-write validation (`pkg/models/group.go` / `models.ValidateKindRules` family). **Out of scope** — recorded here so it is not lost, and so the request-time exclusion is understood as the compensating control, not the whole fix.
   - **The decision lane needs no equivalent:** `buildGroupPolicy` iterates `group.Members` only; `policy.order()` returns member ids; chains are not in the decision attempt list.

**Done when:** the line table above is present in the phase file; `applyReachFilter` excludes group-kind chain entries on **both** lanes with a single-request log; `TestChainGroupEntry_NeverAdmitted_No502` (new, `pkg/proxy/handler_test.go` + an anthropic twin) shows `--- PASS` with the group row's credential-resolution **never attempted** (assert the resolver was not called for the group id); `TestAnthropic_Group_AllMembersOutOfScope_403_Not502` asserts the chat-shaped 403 body.

### T2.16 — **PD-9 (AMBER): shadow/claimed-id resolution — authorize the resolved target, quote the claimed string**

**The problem, verified at `a3f0335`.** Only **groups** claim, and a claim is an **ID-space** alias: `pkg/modelscache/models.go:283-284` (`if m.IsGroup() && m.Enabled && m.ShadowModelID != "" && m.ShadowModelID != id { claims[m.ShadowModelID] = id }`) and the JSON twin `pkg/models/config.go:727-728`. Claims-first resolution (`pkg/models/config.go:586-604`, `pkg/modelscache/models.go:496-507`, `pkg/store/database/store.go:1073-1086`) returns the **GROUP row** when a client requests the claimed string `S`. So for a request naming `S`:
- `rc.resolvedModel` / `resolvedModel` is the group `G` (members, no credentials);
- `rc.reqLog.Model` / `reqLog.Model` is the **client-requested string** `S` (`handler.go:839-840` reads `rc.reqLog.Model`, with the §3.4 Q1-tail comment at `:823-835` explaining that choice; anthropic mirrors it at `:506-513`).

**The narrowing the architect found (arch PD-9):** a token whose `allowed_models` contains **only `S`** (not canonical `G`) passes today's hoisted entry gate (`Allows(S)` — the client string is the authorization input today), then the reach filter strips canonical `G` **and** its members ⇒ `403 ScopeExcluded`. **Fail-closed** (an alias can never escalate) but **undocumented**, and **unchecked in G0**. Under the ruling below the **entry** denial moves earlier and becomes the *primary* answer for that token — which is exactly what the commission's own pin requires (see item 2).

**RULED semantics (leader PD-9, `plan.md` §8.2a line ~703, and the commission's original pin) — implement exactly this, STRICT:**

1. **Entry authorization evaluates the RESOLVED target ONLY.** The gate asks one question: *is the row that will actually be contacted in `Reach(T)`?* For a claimed request that row is the **group** `G`; for every other request it is the model itself.
   ```go
   // PD-9: authorize the RESOLVED row, never the client string alone.
   // A claim is an ID-space alias (only groups claim:
   // modelscache/models.go:283-284), so a request naming the claimed
   // model id resolves to the claiming GROUP row. The model actually
   // contacted is that group's MEMBER, so the resolved target is the
   // authorization unit (ADR D1).
   //
   // AUDIT BRIDGE: for every NON-claimed request resolvedModel.ID ==
   // reqLog.Model, so this strict check is behaviourally identical to
   // today's single check — only the claimed path differs.
   allowed := rc.resolvedModel != nil && rc.scope.Allows(rc.resolvedModel.ID)
   ```
   - **Why the RESOLVED TARGET ALONE (not a union of client string + resolved id).** Three reasons, all in the corpus:
     1. **ADR D1** — "*The unit of authorization is the model that will actually be contacted. The client-requested string is only the entry point into that decision, never the decision itself.*" A union makes the client string a **sufficient** authorization input whenever it differs from the resolved target ⇒ granting alias `S` would imply serving group `G`'s members, which the operator never granted. That is **implicit reach** — the exact defect class (`D5`) this commission closes.
     2. **The commission's own pin fails under a union.** The named pin is "*shadow/claimed decision id resolves into the group arm and **403s** on the claimed string*" — a pin that must **PASS for a token that is NOT granted the group**. Under a union, a token granting the alias `S` but not `G` **passes** the gate (`Allows(S)` is true) ⇒ the pin **fails**. Under strict it 403s quoting `S` ⇒ the pin **passes**.
     3. **`plan.md` §8.2a already rules strict** ("*`Reach(T)` authorizes against the **resolved** group/target. If the client requested a shadow/claimed id, the entry gate rejects with `403` quoting the **client-requested** string.*"). This task **aligns** to that ruling; it does not reinterpret it.
   - **The audit bridge survives and is the reason the strict change is low-risk:** for every **non-claimed** request `resolvedModel.ID == reqLog.Model` (chat: the client string is the raw body `"model"` — `pkg/proxy/handler_functions.go:108`/`:132`; anthropic: `anthropicReq.Model` — `pkg/proxy/handler_anthropic.go:186`), so the strict check is **behaviourally identical** to today's single check on that path. The strictness bites **only** the claimed path — which is precisely where the ruling wants it.
   - **Chat implementation:** `rc.resolvedModel` is the claims-first `GetModel` result (`pkg/proxy/handler_functions.go:147`; the field is `pkg/proxy/handler_helpers.go:34`).
   - **Anthropic implementation:** `resolvedModel` is the claims-first `GetModel` result (`pkg/proxy/handler_anthropic.go:204`).
   - **Decision implementation:** the decision lane's **MODEL arm** (`pkg/decision/handler.go:340-354`) authorizes `model.ID` — and because **claims are group-only** (`modelscache/models.go:283`), a non-group row always satisfies `model.ID == req.Model` (`modelID` is `req.Model`, `handler.go:282`; the resolve is `h.safeResolveModel(modelID)` at `:305`, claims-first via `cmd/main.go:339-341`), so the strict form is identical to today's check there. The **GROUP arm** is where a claim lands: `serveDecisionGroup` receives the resolved **group** row, so ARM #1 must authorize `group.ID` and quote the **client-requested** string `req.Model` (`pkg/decision/types.go:130-135`) in the 403 — see T2.9 (the ARM #1 writer is corrected there to quote `req.Model`, which for a non-claimed request equals the group id).
2. **The 403 quotes the CLIENT-REQUESTED CLAIMED STRING in `"model"` — always.** Both text-lane denial arms (`handler.go` hoisted gate, `handler_anthropic.go` hoisted gate) call `h.sendModelNotAllowedError(w, rc.reqLog.Model)` / `(w, reqLog.Model)` — **do not change those arguments** to the resolved id. Pinned by `TestDecision_ShadowClaimed_GroupArm_403OnClaimedString` (§5.1) and by the lane pins (`TestChat_ShadowClaimed_403QuotesClaimedString`, `TestAnthropic_ShadowClaimed_403QuotesClaimedString`, plus the alias-only twins below): body `model == "S"`, `type == "access_denied"`, and **no canonical `G` anywhere in the body** (leaking the group id would disclose the claim topology).
3. **The members are then authorized individually.** Once the group is admitted, `applyGroupRewrite` picks a member and `applyReachFilter` demands `Allows(memberID)` — the ruling's "the member's own id is what scope must allow". So a token that grants `G` but **not** the member ids gets a **403 ScopeExcluded** at the filter, not a served request. That is the intended strictness and it must be **documented in the release note** (Phase 3 exit criterion 10), because it is a real narrowing relative to the implicit "group grant ⇒ all members" intuition.
4. **A group id can NEVER be a claim target.** `ShadowModelID` is validated against model ids on write (`pkg/models/config.go:417-426` documents it as "the optional single ID-space claim of a …"; `pkg/ui/server.go:1082-1083` rejects a self-shadow), and claim installation only accepts `KindGroup` **owners** (`modelscache/models.go:283`), i.e. the *claimed* id is a model id. **Do not add claim expansion to `Reach(T)`** — that was the architect's alternative and it is **explicitly declined**: expanding aliases into the resolver would make `Reach` depend on group rows and turn a permission set into a topology query.
5. **The strictness is intentional; the repair path is explicit.** An operator who wants an alias-granting token to actually serve must grant the **group id** (or the member ids) explicitly. Record it; do not "helpfully" expand — a union *is* the helpful expansion this item forbids.
6. **The narrowing is INTENDED and is handled by INSPECTION + DOCUMENTATION, never by widening the gate.** The narrowing of **alias-only** grants (a token listing `S` but not `G` now 403s at the entry instead of deeper in the filter) is the **same class** as the `allowed_models`-group-id narrowing already ruled by **Q2** (the `034` repartition moves group ids out of `allowed_models`; a token that named a group there keeps its *effective* reach but changes form). Two mechanisms carry it, and **neither is a code change to the gate**:
   - **(a) G0-4** — the read-only `models.shadow_model_id` × live `auth_tokens` cross-check (below, owned by Phase 3 T3.10) names every live token that references a shadow id, so the affected set is **known and quotable** before the deploy;
   - **(b) the release note** — states the alias semantics in plain words (Phase 3 exit criterion 10(b2)): a token granting a shadow alias now needs the **group id** (or the member ids) to serve; the operator repair is "grant the group id", and the note says so.
   - **Do not** "fix" the narrowing by re-admitting the client string. The whole point of the commission is that the client string is not the authorization unit.

**G0 cross-check (owned by Phase 3 T3.10, SQL text carried there).** Add a fourth read-only query: which live tokens reference a `models.shadow_model_id` value at all? `SELECT t.id, t.name, t.allowed_models, t.allowed_groups FROM auth_tokens t WHERE … json-array-elements ∩ (SELECT shadow_model_id FROM models WHERE shadow_model_id IS NOT NULL AND shadow_model_id <> '')` — guarded by the same `DO $$ … EXCEPTION` shape as PD-2's queries. **Inspection, not a gate, not a write**: it names which tokens the alias semantics affect so the release note is auditable, and it is the **only** instrument that quantifies the intentional narrowing.

**Done when:** the **strict** gate is implemented on both text lanes (chat hoisted block + anthropic hoisted block) with the comment above and the audit-bridge sentence; the decision group's ARM #1 authorizes `group.ID` and quotes `req.Model` (T2.9); the 403 continues to quote the client string (asserted byte-exactly, **no canonical id in the body**); `TestDecision_ShadowClaimed_GroupArm_403OnClaimedString` + the four lane pins (`TestChat_ShadowClaimed_403QuotesClaimedString`, `TestChat_ShadowClaimed_AliasOnlyToken_403`, `TestAnthropic_ShadowClaimed_403QuotesClaimedString`, `TestAnthropic_ShadowClaimed_AliasOnlyToken_403`) + `TestChat_ShadowClaimed_GroupGrant_AdmitsClaimedRequest` all show `--- PASS`; and the "grant the group id or the member ids" repair path is written into T3.12's release note.

---

## 4. Dependencies

| Depends on | Why |
|---|---|
| **Phase 1** T1.1–T1.6 | `authz.Resolve`/`Allows`, `models.ReachFilter`, `models.GroupRewriteResult.ScopeExcluded`, `AuthToken.AllowedGroups` + `Invalid` flags |
| **Phase 1** T1.7 (migration) | the tests in §5 seed `allowed_groups`; `pkg/store/database` tests run real migrations, so the column must exist in the runner |
| Phase 1's demotion of `IsModelAllowed` | T2.2 / **T2.13** remove the last four call sites and the extended grep guard (`TestPackage_NoPerAxisLenGuardOutsideAuth`) proves none returned |
| HOME-quarantined `go test` | `pkg/proxy` sets `APPLY_ENV_OVERRIDES` ⇒ a non-isolated run rewrites the real `config.json` (`PACKS.md:36`, project risk note) |
| Timeouts: `pkg/proxy` needs `-timeout=240s` | `pkg/proxy` measured 131 s in the 2026-11-17 gate; `-timeout=110s` will fail spuriously (`PACKS.md:20`, `:9-14`) |

Blocks: **Phase 3's ship** (the FE cannot be verified end-to-end until enforcement exists) and **Phase 3's live E2E probe** (the 403/200 probes need this phase's behaviour).

---

## 5. Tests owned by this phase

**⚠️ RECONCILED (fix pass C): the authoritative LANDED set is `plan.md` §13.1a (Phase 2 = 38 real Go names). The commission-named obligation pins listed in this section (`TestDecision_Precedence_401_404_400_403_502`, `TestDecision_DisabledVsOutOfScope_NoLeak`, `TestChat_FallbackPositionGroup_NoPhantomPick`, `TestChainGroupEntry_NeverAdmitted_No502`, the shadow-claimed quartet) **LANDED in `1e65774`** (4 test files, +1589) and are enumerated in `plan.md` §13.1a (88 Go + 33 FE = 121). The gate counts ledger ROWS in §13.1a (obligation→pin map in `phase3 §5.1`).**

### 5.1 The commission's named pins

| Pin | File | Asserts | Zero-upstream proof |
|---|---|---|---|
| `TestDecision_Group_NotGranted_403_ZeroAttempts` | `pkg/decision/handler_group_test.go` (harness `newGroupEnv` `:175-259`, group `"decision-group"` `:242`) | restricted token (no group grant) posting the group ⇒ **403** `access_denied`, body `model` = group id, `type` = `access_denied`, **no `code` key** | assert the fake upstream provider's call counter **== 0** and no metering row |
| `TestDecision_Group_Granted_200` | same | `AllowedGroups:["decision-group"]` ⇒ **200**; served member ∈ group; metering = member id (the `:341` path still works) | counter ≥ 1 with only the chosen member |
| `TestDecision_Model_NotGranted_403` | `pkg/decision/handler_test.go` (extend `:666-675`) | non-group decision model not in reach ⇒ 403, unchanged envelope | 0 upstream |
| `TestAuth_Anonymous_401_AllLanes` | `pkg/decision/handler_test.go`, `pkg/proxy/authenticate_test.go`, `pkg/imggen/handler_test.go`, `pkg/proxy/handler_anthropic_test.go` | 401, body unchanged, on all four lanes | 0 upstream |
| `TestDecision_ChatKindGroup_Misroute400_NotScope403` | `pkg/decision/handler_group_test.go` + `pkg/proxy/handler_anthropic_misroute_test.go:140-228` | chat-kind group + out-of-scope token ⇒ **400 `decision_misroute`** (precedence preserved) | 0 upstream |
| `TestDecision_ShadowClaimed_GroupArm_403OnClaimedString` | `pkg/decision/handler_group_test.go` (shadow seam; precedent `pkg/proxy/handler_groups_shadow_member_test.go:71`) | a claimed id resolves into the group arm and the 403 body's `model` is the **claimed (client-requested)** string | 0 upstream |

### 5.2 Per-layer exclusion pins (assert ZERO upstream calls for the excluded id)

| Pin | File | Asserts |
|---|---|---|
| `TestChat_GroupMember_OutOfScope_NeverPicked` (P-G7) | `pkg/store/database/store_group_test.go` + a `pkg/proxy` end-to-end | `GroupRewriteResult.ModelList` ⊆ Reach **and** an in-scope sibling still serves (proves the pick is scope-aware, not just post-filtered) |
| `TestChat_GroupMember_OutOfScope_PickListConsistent` | `pkg/proxy` (T3 assertion) | `MemberID ∈ ModelList` always; the divergence branch never fires in the positive case |
| `TestChat_ChainEntry_OutOfScope_NeverSpawned` (P-G8) | `pkg/proxy/handler_test.go` (mock `:2105/:2121`) | model `A` with `FallbackChain=[B]`, token `["A"]` ⇒ the coordinator's `models` excludes `B`; **B's call counter == 0** |
| `TestAnthropic_ChainEntry_OutOfScope_NeverAttempted` (P-G9) | `pkg/proxy/handler_anthropic_test.go` | same on the sequential lane; `arc.modelList` filtered |
| `TestAnthropic_GroupMember_OutOfScope_NeverPicked` | `pkg/proxy/handler_anthropic_test.go` | member pick filtered; parity with chat via the shared helper (`pkg/proxy/handler_anthropic_tokenid_parity_test.go:230/:240` keeps both call sites pinned) |
| `TestChat_Reentry_OutOfScopeTarget_NeverSpawned` (P-G11) | `pkg/proxy/handler_test.go` | after a terminal member failure, the re-offer/rebuild never yields an out-of-reach member; **0 calls** to it |
| `TestAnthropic_Reentry_OutOfScopeTarget_NeverAttempted` | `pkg/proxy/handler_anthropic_test.go` | same |
| `TestHedge_And_CredFailover_NeverIntroduceModel` (P-G10) | `pkg/proxy/cache_layer_separation_test.go` (extend `:593-622`) | the hedge is `c.models[0]` on a secondary credential; the set of model ids attempted ⊆ Reach; **no new model id appears** |
| `TestDecision_GroupMember_OutOfScope_Excluded` | `pkg/decision/handler_group_test.go` | a mixed group (1 in-reach, 1 out) serves the in-reach one; the out-of-reach member's counter == 0 |
| `TestImagegen_NotGranted_403` (P-G12) | `pkg/imggen/handler_test.go:672` | entry-only 403 through the shared predicate |
| `TestDecision_Group_AllMembersOutOfScope_403_Not502` | `pkg/decision/handler_group_test.go` | 403 `access_denied`, **never** `decision_all_members_failed`, and 0 upstream |
| `TestChat_Group_AllMembersOutOfScope_403_Not502` | `pkg/proxy/handler_test.go` | 403, **never** `group_unservable` |
| `TestAnthropic_Group_AllMembersOutOfScope_403_Not502` | `pkg/proxy/handler_anthropic_test.go` | 403 (chat writer reused), never the 502 Anthropic envelope |
| `TestChat_Group_AllMembersDisabled_Still502` | `pkg/proxy/handler_test.go` | the **availability** arm is intact: all-disabled members ⇒ 502 `group_unservable` (the two arms are distinguishable) |
| `TestChat_FallbackPositionGroup_Unreachable_Dropped` (T1) | `pkg/proxy/handler_test.go` | a healthy primary still serves; the fallback-position unreachable group is dropped, **no 502** |
| `TestChat_Phase4_DemotedMember_OutOfScope_Skipped` (T2) | `pkg/store/database/store_group_test.go` | a demoted (benched) out-of-reach member is never returned as the Phase-4 head; a reachable later member is |
| `TestChat_OutOfScopeGroup_CreatesNoBinding` (D13) | `pkg/proxy/handler_test.go` | a denied group request performs **no** group binding / `OnGroupChanged` / sticky write |
| `TestWildcard_Unchanged_AllSurfaces` (P-G13) | `pkg/proxy` + `pkg/decision` | a both-axes-ABSENT token reaches: a chat model, a group **and all its members**, a decision model, a decision group **and its members**, an image-gen model — bit-for-bit today's behaviour; `authz.Resolve` returns `nil` |
| `TestDenyAll_InvalidGrantRow_403_AllLanes` | `pkg/proxy` + `pkg/decision` + `pkg/imggen` | a token whose `AllowedModels` is malformed ⇒ 403 everywhere (fail-closed flip, end-to-end) |
| **`TestChainGroupEntry_GroupInFallbackChain_Dropped_NotAdmitted`** | `pkg/proxy/handler_test.go` | **PD-15, second half.** Distinct from the structural pin above: here the chain entry names a **group** the token IS granted (so `Reach` contains it). Asserts the entry is still excluded (a group is not a model id), the group row's credential resolution is never attempted, and a scope-identical wildcard token behaves the same (the exclusion is **structural**, not scope-dependent — the asymmetry the ruling exists to remove) |
| **`TestDecision_DisabledVsOutOfScope_NoLeak`** | `pkg/decision/handler_group_test.go` (LANDED `1e65774`) | **PD-11 pin.** A member that is **both** disabled and out of scope, in a group whose other members are healthy: the response is **200** serving a healthy member, and the disabled+out-of-scope member's counter is **0** — neither condition leaks (no 403, no 502, no distinguishing detail). Converse fixtures: all-disabled members ⇒ 502; all-out-of-scope ⇒ 403; **disabled+out-of-scope group ⇒ 502 + 0 upstream hits**. **Sub-test D (`group_row_disabled_does_not_leak_either`) pins the LANDED behaviour:** a **disabled group named by its OWN id is SERVED** (no `enabled` filter on direct IDs — `store.go:1102-1140`); only its **shadow claim** is inactive and only its **members** are disabled-filtered; the no-leak property is **structural** (the envelopes 403 / 502 / 200 never distinguish disabled from out-of-scope). |
| **`TestDecision_Precedence_401_404_400_403_502`** | `pkg/decision/handler_test.go` | **PD-11 pin.** One table, one lane, five rows proving the pinned order: anonymous ⇒ 401; unknown id ⇒ 404; chat-kind model ⇒ 400 `decision_misroute` **regardless of token scope** (run twice: wildcard + restricted, same 400); in-reach-checked-but-denied ⇒ 403; servable-but-broken ⇒ 502. Zero upstream calls in every error row |
| **`TestChat_ShadowClaimed_403QuotesClaimedString`** + **`TestAnthropic_ShadowClaimed_403QuotesClaimedString`** | `pkg/proxy/handler_test.go` / `handler_anthropic_test.go` | **PD-9 pins.** A token granting **neither** the claimed id `S` nor the group `G`: the 403 body's `model` is `S` (client-requested), `type` is `access_denied`, and the body contains **no occurrence of `G`** — the canonical group id must not leak |
| **`TestChat_ShadowClaimed_AliasOnlyToken_403`** + **`TestAnthropic_ShadowClaimed_AliasOnlyToken_403`** | same | **PD-9 STRICT pin (the commission's own pin, on the text lanes).** A token whose grants contain **only the alias `S`** and **not** the group `G`: the request naming `S` ⇒ **403** quoting `S` — **not** served. This is the pin a union gate would FAIL (the union would admit it via `Allows(S)` and serve the group's members — implicit reach). Also asserts **0 upstream attempts** and no `group_*` event with an empty id |
| **`TestChat_ShadowClaimed_GroupGrant_AdmitsClaimedRequest`** | `pkg/proxy/handler_test.go` | **PD-9 STRICT pin (the other half).** A token granting **`G`** (the canonical group id) but not `S`: a request naming `S` is **admitted** and served (200) — proving the **resolved target** is the authorization unit (ADR D1) and that a group grant still works through its alias |

### 5.3 Structural / guard pins

| Pin | File | Asserts |
|---|---|---|
| `TestReachCompleteness_ExecutionListsSubsetOfReach` (P-G24, `I15`) | new `pkg/proxy/reach_completeness_test.go` (+ a `pkg/decision` sibling) | drives each lane and asserts (a) the list handed to `newRaceCoordinatorWithEvents` (`:996`) ⊆ Reach, (b) `arc.modelList` ⊆ Reach at the walk (`:550`), (c) `pickServableGroupMember`'s pick ∈ Reach, (d) the decision `order` ⊆ Reach |
| `TestPackage_ImportGuard_NoScopeInHotLoop` (P-G22) | new `pkg/proxy/authz_guard_test.go` | `pkg/proxy/race_coordinator.go` and `race_executor.go` contain **no** `authz.` import and **no** `.Allows(` call; `pkg/decision/group.go`'s attempt loop (`:142-244`) contains none either (no attempt-time skip); **negative control must FIRE** |
| `TestPackage_ImportGuard_NoIsModelAllowedInLanes` (P-G22b) | same | no non-test file outside `pkg/auth` calls `IsModelAllowed(` (the four sites were unified) |
| **`TestRaceCoordinator_EmptyModelList_Refused`** | `pkg/proxy/race_coordinator_test.go` | **PD-10 dedicated regression pin (RED).** (a) `newRaceCoordinatorWithEvents(…, []string{}, …)` returns `err != nil` AND a `nil` coordinator — the `{cfg.ModelID}` re-expansion is **gone**; (b) a scan assertion that `pkg/proxy/race_coordinator.go` no longer contains the literal `models = []string{cfg.ModelID}`; (c) the caller-side pre-spawn assert: an end-to-end scope-empty **group** request returns **403** with the fake upstream counter `== 0` (nothing was constructed, nothing was attempted) |
| **`TestPackage_NoPerAxisLenGuardOutsideAuth`** | `pkg/proxy/authz_guard_test.go` (extends P-G22b) | **PD-11 fold (arch PD-12).** `grep -rnE 'len\([^)]*AllowedModels\)' --include=*.go pkg/ | grep -v _test | grep -v '^pkg/auth/'` yields **zero** files. Today it finds `pkg/proxy/handler.go:857`, `pkg/decision/handler.go:342`, `pkg/imggen/handler.go:267` — all three removed by T2.2/T2.13. **Negative control must FIRE** (a synthetic fixture string such as `len(t.AllowedModels)` must be detected) |
| **`TestPackage_NoUltimateEscalation`** | `pkg/proxy/authz_guard_test.go` | **PD-13 audit as a test.** No non-test file under `pkg/` or `cmd/` contains `UltimateModel` / `ExcludeFromUltimateSwitching` outside a comment line; negative control FIRE. (Complements the four manual greps recorded in T2.14 — the test is the durable half) |
| **`TestDecisionGroup_FilterDoesNotMutateCachedRow`** | `pkg/decision/handler_group_test.go` | **PD-5 pin.** Capture `group.Members` (ids, weights, `BackupOnly`) before the request, run an out-of-scope member-filtered request, re-read the group row from the cache/manager and assert the member slice is **byte-identical** — proves the three-index copy, not `[:0]` |
| **`TestChat_FallbackPositionGroup_NoPhantomPick`** | `pkg/proxy/handler_test.go` | **PD-12 fold (arch PD-7).** A model whose `FallbackChain` contains a group with **no reachable member**: assert `applyGroupRewrite`'s outcome has `pick == nil` **and** `GroupID == ""`; the healthy primary still serves **200**; **no** `group_*` event with an empty id is published; `RouteModel` is not blanked |
| **`TestChainGroupEntry_NeverAdmitted_No502`** | `pkg/proxy/handler_test.go` + `pkg/proxy/handler_anthropic_test.go` twin | **PD-15 pin.** Model `A` with `FallbackChain=["G"]` where `G` is a **group** id and the token's reach contains `G`: the execution list **excludes** `G`; the credential resolver is **never called** with `G`; the request does not 502; one `[authz]` log names the dropped chain target |
| **`TestGroupExcludeScoped_ScopeOnlySignal`** | `pkg/store/database/store_group_test.go` | **PD-12 fold (arch PD-11).** A 2-member group where member `m2` is out of reach: `ExcludeGroupMemberScoped(m1, …)` ⇒ `OK == false` **and** `ScopeOnly == true`; an availability-shaped group (all base-stale) ⇒ `OK == false`, `ScopeOnly == false`. The two facts must be distinguishable at the seam, not inferred by callers |
| **`TestAnthropic_Group_AllMembersOutOfScope_403_EnvelopeParity`** | `pkg/proxy/handler_anthropic_test.go` | **PD-14 pin.** The 403 body is **byte-identical** to the chat writer's (`{"error":{"type":"access_denied","message":"model not allowed for this token","model":"<client string>"}}`): no `api_error` type, **no `code` key**, no `group_unservable`. Verifies `handler_anthropic.go:530` reuses `pkg/proxy/handler.go:525-535` |
| `TestPackage_ImportGuard_GroupPolicyFiles_NoCacheImports` (P-G23, existing) | `pkg/decision` | still passes; **plus** `git diff --stat $BASE..HEAD -- pkg/decision/policy.go` must be **0 bytes** (asserted in the gate, not the test) |
| **`TestScopeExclusion_NeverWritesCooldown_MultiToken`** | `pkg/store/database/store_group_test.go` + `pkg/decision/handler_group_test.go` (**fix-pass A, A2 — the NAMED Ruling-1 pin; IN the required count**) | a scope-refused offer writes **no** cooldown (`ExcludeAndReselect` not called, no `st.cooldowns[id]`, no `skipcache.Mark`) while it DOES advance the binding via `RebindPast`/`RebindBackupPast` (the corrected A1 mechanism); after Token A's scope exclusion of M1, wildcard Token B still selects M1 |
| **`TestConversationKey_TokenSalting`** | `pkg/credentiallb` (new; **fix-pass 2 🟡6 — PD-15 bullet-4 disposition pins**) | cross-token conversation bindings stay isolated by `tokenID` salting in `conversationKey` (`key.go:48-51`): two restricted tokens sharing a conversation never read, share, or corrupt each other's group bindings — asserted, not modified |
| **`TestConversationKey_MidBindingRevocation`** | `pkg/credentiallb` (new; fix-pass 2 🟡6) | a grant change (group revoked) mid-conversation never extends the existing binding past the 30-minute ABSOLUTE `group_binding_ttl` — the next binding re-resolves against current grants |
| `TestPackage_ImportGuard_StoreDatabaseNoAuthImports` | new (`pkg/store/database`) | `pkg/store/database` does **not** import `pkg/auth` or `pkg/authz` (the `models.ReachFilter` cycle-breaker is what makes this possible — a regression here would be an import cycle anyway, but the guard names the intent) |

---

## 6. Coupling

- **Tight with:** **Phase 1** — the contract this phase *consumes*: `authz.Scope` / `Resolve` / `Allows` (the one predicate, D7/D8), `models.ReachFilter` (the interface that lets `pkg/store/database` carry an authorization predicate **without** importing `pkg/auth`/`pkg/authz` — `pkg/auth` already imports `pkg/store/database`, `pkg/auth/store.go:9`, so the other direction would be a cycle), `models.GroupRewriteResult.ScopeExcluded` (the "empty because scope" signal), `auth.ScanGrant` + the `AuthToken` invalid flags (the deny-all state), the `auth.OptionalTokenPermission` write shape inherited by the mocks (A4: ONE write-payload name), and **migration 034** (Phase 2's `pkg/store/database` tests run the real migrations). T2.11 also **extends** the seam Phase 1 left additive-only: `models.GroupRewriterScoped` + the decorator forward + its compile-time assertion.
- **Loose with:** **Phase 3** — the enforcement *domain*: Phase 2 owns the behaviour (403 vs 502, the wildcard invariance, the zero-upstream-call pins) that Phase 3's live probes assert after the deploy. Shared domain and shared vocabulary ("model_not_allowed"), no shared interface or shared file.
- **Independent of:** **the FE half of Phase 3** — Phase 3's unit tests mock `fetch`/callbacks and need only Phase 1's frozen wire contract; Phase 2 needs no FE code to compile, test or ship.

## 7. Risks

Phase-specific risks — each lands on **this phase's** files or tasks (`pkg/proxy/**`, `pkg/store/database/store_group_rewrite.go`, `pkg/modelscache/models.go`, `pkg/decision/**`, `pkg/imggen/handler.go`). The global register lives in `plan.md` §15 / `decisions.md` §3; the id is cross-referenced where this phase carries the concrete instance of it.

| # | Risk | Impact | Likelihood | Mitigation (concrete — task / file / pin) |
|---|---|---|---|---|
| R2.1 | **Phase 4 (the demoted sweep) serves an out-of-scope member.** `pickServableGroupMember`'s Phase 4 (`:270-284`) executes `bucket[0]` and calls **no** predicate today — the demoted head is a full candidate that flows into the race list end-to-end. A scope filter that covers only Phases 1/2/3 leaves this hole open. | High | Medium | **T2.3** applies `isReachableMember` at Phase 1 `:205`, Phase 2 `:229` and Phase 3 `:253` (so an out-of-reach member never enters the bucket) **and** re-checks the Phase-4 head before returning (skip-forward, so a reachable later member is still served); pinned by **`TestChat_Phase4_DemotedMember_OutOfScope_Skipped`** and the reach-completeness test's "pick ∈ Reach" assertion. |
| R2.2 | **Stale `groupPick.MemberID`.** The stamped pick (`handler_groups.go:87-93`) can disagree with the filtered list ⇒ the re-entry tried-set mis-tracks (`handler.go:805`, `:1213`) and the metering `RouteModel` stamp names a member that is not served. | High | Medium | The pick is made **by** the scoped predicate (in reach by construction); **T2.4** adds the divergence assertion — if `res.MemberID ∉ res.ModelList` after filtering, log `[authz] PICK/LIST DIVERGENCE` and fail the entry closed as `scopeExcluded` (403), never serve a list whose stamped pick is absent; pinned by **`TestChat_GroupMember_OutOfScope_PickListConsistent`**. |
| R2.3 | **The four entry sites stay divergent.** Three sites guard with `len(AllowedModels) > 0` while the anthropic one (`:514`) is **unguarded** — they agree today only because empty means wildcard. If any site keeps a local form, the conjunctive rule is applied inconsistently ⇒ a **security** divergence, not a cosmetic one. | High | Medium | **T2.2** collapses all four onto `scope.Allows(...)` (the guarded form disappears; the wildcard decision moves into the resolver); **`TestPackage_ImportGuard_NoIsModelAllowedInLanes`** asserts that no non-test file outside `pkg/auth` calls `IsModelAllowed(` at all, and the reach-completeness test asserts the four lanes agree on the same set. |
| R2.4 | **A scope check leaks into the hot loop.** The tempting fix for the chain-advance surface is to thread the token into the race coordinator and re-check on every advance — that adds state to the hottest path and re-opens the site-check class (`A2`). | Medium | Medium | Advance (`race_coordinator.go:1899-1912`, `:1906`) and hedge (`:421-435`, `:435`) read the **already-filtered** `c.models`, so the coordinator needs **no** change; **`TestPackage_ImportGuard_NoScopeInHotLoop`** asserts no `authz.` import and no `.Allows(` call in `race_coordinator.go` / `race_executor.go` / the decision attempt loop, **with a negative control that must FIRE**. |
| R2.5 | **The gate order is not moved ⇒ a denied request still creates side effects.** Today the rewrite (`:783` chat / `:472` anthropic) runs **before** the allow-list (`:818-879` / `:506-533`), so an out-of-scope group request still creates a group binding, a sticky pick, a member expansion and a metering row before it is denied. | Medium | Medium | **T2.1** hoists the entry gate ahead of the rewrite on both lanes and states the D13 sequencing in the code comment; pinned by **`TestChat_OutOfScopeGroup_CreatesNoBinding`** (no engine binding write for a denied request). |
| R2.6 | **An empty filtered list reaches the coordinator.** `newRaceCoordinatorWithEvents` substitutes `{cfg.ModelID}` for an empty list (`race_coordinator.go:338-340`) ⇒ an unreachable model can be **re-introduced** by the fallback itself — the exact failure this commission exists to prevent. | High | Low | **T2.5** handles the empty-after-filter case explicitly **before** the coordinator is constructed (403 / 502, never an empty list); **`TestReachCompleteness_ExecutionListsSubsetOfReach`** asserts every list handed to the coordinator and to the anthropic walk is a subset of Reach, and the empty-list path has its own case. |
| R2.7 | **A fallback-position group 502s the whole request.** `res.FailedGroupID = row.ID` is set for **any** entry index (the setters at `store_group_rewrite.go:85/:91/:96`; `:91`/`:96` hit a fallback entry) ⇒ a scope-filtered fallback group currently fails the request, converting a healthy primary into a 502. | Medium | Medium | **T2.6** drops fallback-position entries with a loud log (mirroring the decision-lane boundary refusal at `:82-88`) and reports `FailedGroupID` only for `idx == 0`; pinned by **`TestChat_FallbackPositionGroup_Unreachable_Dropped`** (the healthy primary still serves, no 502). |
| R2.8 | **"Empty by scope" becomes indistinguishable from "empty by unservability".** `pkg/decision/group.go:120-123` maps an empty order to `exhaustDecisionGroup` (502 `decision_all_members_failed`); without the split, a permission failure is reported as an availability failure — or, worse, an availability failure is reported as a permission failure. | High | Medium | **T2.9** builds a **member-filtered copy** of the group row (so the policy builder is untouched) and counts the base-servable members, giving ARM #2 (scope-empty ⇒ 403) and ARM #3 (unservable ⇒ the existing 502) distinct inputs; pinned by **`TestDecision_Group_AllMembersOutOfScope_403_Not502`** and **`TestChat_Group_AllMembersDisabled_Still502`** — the two arms are asserted against each other. |
| R2.9 | **The decision filter is placed inside `pkg/decision/policy.go`.** Its header forbids importing the health/suppression layer and its doctrine says a layer "may reorder candidates, never exclude them" (`:15-18`, imports `:20-24`); a filter there would also break the import guard and the layer fence. | High | Low | The filter sits **at or before `:118`** in `pkg/decision/group.go` (T2.9) — never in `policy.go`; the gate asserts **`pkg/decision/policy.go` shows a 0-byte diff** (Phase 3 T3.8) and `TestPackage_ImportGuard_GroupPolicyFiles_NoCacheImports` still passes. |
| R2.10 | **The decorator forward is missing ⇒ a silent no-op in production.** Production binds the cached decorator (`CACHE_LAYER_ENABLED` defaults ON); a missing `RewriteGroupEntriesScoped` forward would be invisible in raw-manager tests while production silently skips the scoped pick (the documented modelscache-decorator-forward trap). | High | Low | **T2.11** adds `var _ models.GroupRewriterScoped = (*CachedModelsConfig)(nil)` — the missing forward becomes a **build failure**, not a runtime no-op — and the reach-completeness test asserts the scoped pick is actually in force on the production binding. |
| R2.11 | **Per-request scope resolution costs latency.** Reach resolution is O(grants + Σ members of granted groups) per request; a wide group on a hot lane could show up as a p99 regression. | Medium | Low | One resolution per request, membership read from the **IN-MEMORY models snapshot** (`GetModels()` / `currentModelRows()`, invalidated on group writes — Ruling 5, fix-pass 2; `GetModelNative` is FORBIDDEN as a per-request reach source — I14 hard requirement); the gate's **G10 perf sanity** step (Phase 3) compares chat + decision p99 against a pre-change capture, and the phase exit criteria require the diff to show exactly one resolution site per lane. |
| **R2.12** | **A scope-empty list is silently re-expanded by the coordinator constructor.** *(**PD-10, RED**.)* `pkg/proxy/race_coordinator.go:338-340` — `if len(models) == 0 { models = []string{cfg.ModelID} }`. VERIFIED at `a3f0335`; there are **four** visible list constructions (chat `handler_functions.go:198-202`, anthropic `handler_anthropic.go:328-336`, group rewrite `store_group_rewrite.go:57-111`, filter helper) and this is the **fifth, hidden one** — it converts the intended `403 ScopeExcluded` into a real attempt on a model the token cannot use, i.e. a `502` plus an upstream call plus a scope escape in the telemetry. | **Critical** | Medium (any scope-empty filtered list reaches it) | **T2.12**: delete the fallback, change `newRaceCoordinatorWithEvents`/`newRaceCoordinator` to return `(*raceCoordinator, error)` (test sites mechanically updated by the compiler — 92/13 provisional, dev report pending), and add the caller-side `len(rc.modelList) > 0` assert with a `403 ScopeExcluded` emit **before** construction at `handler.go:990-996`. Pin: **`TestRaceCoordinator_EmptyModelList_Refused`** (error + nil coordinator + the literal is gone + end-to-end 403 with counter `== 0`). |
| **R2.13** | **A dropped fallback-position group mints a phantom pick with an empty `GroupID`.** *(**PD-12 / arch PD-7, AMBER**.)* VERIFIED: `handler_groups.go:87-93` returns non-nil `pick` whenever `res.AnyGroup` — including when `res.GroupID == ""` after the fallback entry was dropped. Downstream that means `handler.go:1138` enters the group re-entry path, `tryGroupReentry` calls `ExcludeGroupMember("")`, two `group_*` events carry empty ids, `groupTriedMembers[""]` is written, and `RouteModel` is **blanked** on a healthy non-group request. | High | Medium (any `FallbackChain` containing a group whose members are unreachable/out-of-scope) | **T2.4** returns `pick = nil` whenever `res.GroupID == ""` (acceptance: always, even when `res.AnyGroup`), and T2.5's empty-list verdict then relies on `groupPick != nil` ⇒ real group. Pin: **`TestChat_FallbackPositionGroup_NoPhantomPick`** (200 from the healthy primary; no empty-id `group_*` event; `RouteModel` not blanked). |
| **R2.14** | **Scope exhaustion is indistinguishable from unservability at the exclude seam.** *(**PD-12 / arch PD-11, AMBER**.)* `ExcludeGroupMember` returns `ok=false` for both "no alternative member exists" and "alternatives exist but all are out of reach" — so §9.4's deliberate behaviour (never 502 for a scope reason; never a mid-request 403 from a re-entry) is **inferred** by callers rather than **testable** at the seam. | High | Medium | **T2.4** introduces `models.GroupExcludeOutcome{Replacement, Pool, OK, ScopeOnly}` on the NEW `ExcludeGroupMemberScoped`; the legacy method stays byte-unchanged (nil reach ⇒ `ScopeOnly` can never be true) so the 11 structural fakes and the `models.GroupRewriter` interface are untouched. Pin: **`TestGroupExcludeScoped_ScopeOnlySignal`** (scope-shaped vs availability-shaped fixtures produce different `ScopeOnly`). |
| **R2.15** | **A group id inside a `FallbackChain` reaches credential resolution and 502s — CORRECTED (gen-10 re-pin): entry-chain groups are EXPANDED, so this only applies to a group id inside a group *member's* own chain.** *(**PD-15 / arch PD-10, AMBER**.)* **Landed reality:** `handler_functions.go:200-202` puts `[resolvedModel.ID, resolvedModel.FallbackChain...]` into `modelList`, and `store_group_rewrite.go:105-181` iterates the list and expands every `kind='group'` **entry** into its in-reach member — replacing the raw group id, so the group row never reaches credential resolution and never 502s. Pinned by `TestChainGroupEntry_NeverAdmitted_No502` (LANDED `1e65774`): the primary's chain group expands to `pin-chain-member`, the raw group id is absent from `res.ModelList`, the pick is `(pin-plain-group, pin-chain-member)`, and the request ends **200** after the primary 500s. **The narrower remaining wart:** a group id inside a group *member's* own `FallbackChain` (appended at rewrite time to the output list and never re-scanned) — pre-existing, documented, deliberately unpinned; reach filtering still applies to every candidate. | Low (narrower than believed) | Low | The expansion is the landed behaviour (no extra work); keep the exclusion as defence-in-depth for the member-chain shape; pin **`TestChainGroupEntry_NeverAdmitted_No502`** (LANDED) and record the member-chain wart as an out-of-scope follow-up. |
| **R2.16** | **The decision-lane precedence is "pinned" by prose that does not match the code.** *(**PD-11, GREEN** — but the risk of a prose pin is real.)* The four lanes already differ (chat/anthropic resolve **before** auth; decision/imggen authenticate **first**), and the leader's ruling asked for "one exact precedence order matching the other lanes". A plan that asserts symmetry the code does not have produces either a wrong test or a "harmonising" change that alters live 401-vs-404 behaviour. | Medium | Medium | **T2.13** first **reports what the code does** (a per-lane table with line cites, in the phase file), then pins **only** the decision lane's order — `401 → 404 → 400 misroute → 403 → 502` — and explicitly rules the chat/anthropic divergence **pre-existing and out of scope**. Pin: **`TestDecision_Precedence_401_404_400_403_502`** (a five-row table on one lane, with the 400 row run under both a wildcard and a restricted token to prove scope-independence). |
| **R2.17** | **A dormant escalation hook survives unnoticed.** The whole security argument assumes no path can choose a model around `Reach(T)`. `pkg/ultimatemodel/` is gone and `ExcludeFromUltimateSwitching` has **zero** hits, but "we grepped once" is not a durable control and a future rebase/merge can resurrect a comment-adjacent code path nobody re-checks. | Medium | Low | **T2.14** makes the audit **greppable and testable**: four concrete commands with empty-output pass criteria (recorded in the gate artifact) **plus** `TestPackage_NoUltimateEscalation` in `pkg/proxy/authz_guard_test.go` with a firing negative control. The same task carries the **named non-scope** classification for `ResolvePeakHourModel` (6 sites: 5 calls + the `pkg/models/peak_hours.go:146` definition) and `SecondaryUpstreamModel` (`race_executor.go:541/:545/:558`) so the plan is silent on neither — and Phase 3's ship note repeats the classification. |
| **R2.18** | **The strict PD-9 gate silently denies a live alias-granting token.** *(**PD-9, AMBER** — the narrowing is INTENTIONAL, the surprise is not.)* A token whose `allowed_models` lists a group's `shadow_model_id` alias `S` (not the canonical `G`) reached the group **before** this change (today's gate authorizes the client string). Under the strict ruling it is **denied at the entry with 403** — correct per ADR D1, the leader's PD-9, `plan.md` §8.2a and the commission's own pin, but a **behaviour change on a live token** the operator did not ask for. If it is discovered *after* the deploy it reads as "the proxy broke my token". | High (an operator-visible 403 on a token that worked yesterday) | Low–Medium (a live token must actually grant a shadow alias — G0-4 exists to find out) | The narrowing is **deliberate** and is carried by **inspection + documentation, never by widening the gate**: (a) **G0-4** (Phase 3 T3.10) names **every** live token referencing a `shadow_model_id`, on **both** hosts, before the deploy; (b) the **release note** states the alias semantics and the repair ("grant the group id or the member ids") — Phase 3 exit criterion 10(b2); (c) the gate itself stays strict — `TestChat_ShadowClaimed_AliasOnlyToken_403` + `TestAnthropic_ShadowClaimed_AliasOnlyToken_403` **pin the denial** so it cannot be quietly reverted to a union; (d) the decision lane's ARM #1/ARM #3 quote the client string so the operator sees *which* id was denied. **Do not** "fix" this by re-admitting the client string — that is the implicit reach the commission closes. |

---

## 8. Exit criteria

1. `go build ./...`, `go vet ./...`, `gofmt` zero-new-drift.
2. **Pack A** green (`./pkg/proxy/... ./pkg/store/... ./pkg/decision/... ./pkg/ui/... ./cmd/...`, `-timeout=240s` inside `timeout 300`, HOME-quarantined, config hash-guarded) **except** the pre-existing quarantined flakes (`QUARANTINE.md:7` CloseLifecycle, `:10` TOCTOU in Pack B, `:12` PeakHourParity) — each must be adjudicated per the existing registry, not silently ignored.
3. **Every pin in §5 shows a literal `--- PASS`** in the gate's own log, exactly once (anti-vacuous).
4. **`pkg/decision/policy.go`: 0-byte diff.** `pkg/grouplb/**`, `pkg/credentiallb/**`: **0-byte diff**. **`pkg/proxy/race_executor.go`: 0-byte diff.**

   > **CORRECTION (PD-10 supersedes the earlier "race_coordinator.go 0-byte diff" criterion — DRIFT REPORT).** `pkg/proxy/race_coordinator.go` **must change**: T2.12 deletes the `if len(models) == 0 { models = []string{cfg.ModelID} }` block at `:338-340` and changes `newRaceCoordinatorWithEvents` / `newRaceCoordinator` to return `(*raceCoordinator, error)`. The earlier criterion (and §2.3's "no change at all" for this file) was written before PD-10 was ruled and is **retracted**. The replacement criterion is **narrower and stronger**: the diff in `race_coordinator.go` is **exactly** (a) the deleted empty-list fallback, (b) the two signature changes and their error returns; **zero** diff in the advance/hedge/credential-failover logic, and `race_executor.go` stays byte-identical (so no scope predicate enters the hot loop — `TestPackage_ImportGuard_NoScopeInHotLoop` still passes).
5. **The invariant holds by construction:** the reach-completeness test passes for all five lanes **and** the hot-loop guard test passes (no scope predicate in the coordinator/executor/attempt loop).
6. **The two failure arms are distinguishable:** an all-out-of-scope group ⇒ 403 on all three group-capable lanes; an all-disabled group ⇒ 502 on the text lanes and `decision_all_members_failed` on the decision lane.
7. **Precedence intact:** the chat-kind-group-to-`/v1/decisions` case returns **400**, never 403.
8. **No side effects for denied requests:** the binding-write assertion (T2.1's proof) passes.
9. **Wildcard bit-for-bit:** the wildcard pin passes on every surface, and no existing test that uses a wildcard token changed its expected outcome.
10. Manual traceability review: for each row of `plan.md` §8.2, the reviewer can point at the code line that implements it.
11. **PD-5 closed:** the filter runs **before** `buildGroupPolicy` (`group.go:118`) on a **copy**; `TestDecisionGroup_FilterDoesNotMutateCachedRow` green; `pkg/decision/policy.go` still 0-byte diff.
12. **PD-10 closed:** `TestRaceCoordinator_EmptyModelList_Refused` green; `grep -n "models = \[\]string{cfg.ModelID}" pkg/proxy/race_coordinator.go` ⇒ **no hits**; the caller emits 403 before construction; **all test call sites compile** (42/11 measured; the signature change is the enumeration mechanism).
13. **PD-11 closed:** the per-lane precedence table is in T2.13 with line cites; `TestDecision_Precedence_401_404_400_403_502` + `TestDecision_DisabledVsOutOfScope_NoLeak` green (**LANDED (`1e65774`)**, parallel test-only lane); `pkg/proxy/handler.go:857` is **deleted** and `TestPackage_NoPerAxisLenGuardOutsideAuth` finds **zero** `len(…AllowedModels)` outside `pkg/auth`.
14. **PD-12 closed:** `pick = nil` whenever `res.GroupID == ""` (landed `TestFallbackPositionGroup_Unreachable_Dropped` + **LANDED `TestChat_FallbackPositionGroup_NoPhantomPick` (`1e65774`)**); `models.GroupExcludeOutcome.ScopeOnly` distinguishes the two exhaustion causes (`TestGroupExcludeScoped_ScopeOnlySignal`); a scope-exhausted re-entry's status follows the SINGLE attempt-count-keyed rule in **plan.md §9.4** (0 attempts ⇒ 403; ≥1 attempt ⇒ unchanged 502) — pointer only.
15. **PD-13 closed:** the four audit commands yield empty output (artifact recorded) **and** `TestPackage_NoUltimateEscalation` shows `--- PASS`; the two non-scope mechanisms are named in the phase file and in T3.12's ship note.
16. **PD-14 closed:** `TestAnthropic_Group_AllMembersOutOfScope_403_EnvelopeParity` green — the anthropic 403 body is byte-identical to chat's writer, and the anthropic 502 `group_unservable` writer (`handler_anthropic.go:485-489`) is used **only** for availability.
17. **PD-15 closed:** the exact construction lines are cited (`handler_anthropic.go:326-336`, `:356`, `:472`, `:491-492`, `:506-533`, `:539`, `:548/:550`); group-kind chain entries are excluded at the filter with a log (landed `TestChat_ChainEntry_OutOfScope_NeverSpawned` + **LANDED `TestChainGroupEntry_NeverAdmitted_No502` (`1e65774`)**); `TestChainGroupEntry_NeverAdmitted_No502` + its wildcard-twin green; the write-time validation is recorded as an out-of-scope follow-up.
18. **PD-9 closed (STRICT):** the gate authorizes the **resolved target only** on both text lanes (`rc.resolvedModel.ID` / `resolvedModel.ID`, with the "non-claimed requests collapse to today's single check" audit-bridge comment); the decision group's ARM #1/ARM #3 authorize `group.ID` and quote `req.Model` (T2.9); `TestChat_ShadowClaimed_403QuotesClaimedString` + `TestAnthropic_ShadowClaimed_403QuotesClaimedString` + **`TestChat_ShadowClaimed_AliasOnlyToken_403`** + **`TestAnthropic_ShadowClaimed_AliasOnlyToken_403`** + `TestChat_ShadowClaimed_GroupGrant_AdmitsClaimedRequest` + `TestDecision_ShadowClaimed_GroupArm_403OnClaimedString` all green; the G0-4 `shadow_model_id` cross-check is scheduled in T3.10; the intentional narrowing + the "grant the group id or the member ids" repair path are in T3.12's release note. **Anti-union guard:** `grep -n "Allows(clientModel)\|Allows(reqLog.Model) ||\||| .*Allows(" pkg/proxy/handler.go pkg/proxy/handler_anthropic.go` ⇒ **no hits** — no gate may authorize the client string as an alternative input.

---

## 9. Parallelism note (the reasoning behind §6 Coupling)

**TIGHTLY COUPLED — must be sequential and reviewed as one unit.** Reasons, stated:

1. The invariant is **global**: partial enforcement is worse than none in a security feature (it creates the illusion of coverage). A PR that filters the chat lane but not the anthropic one would pass every chat test.
2. The **pick/list consistency** (T3) couples the manager predicate to the request-layer filter: they must land together or the divergence branch fires in production.
3. `applyGroupRewrite`'s signature change (T2.4) forces the four call sites in the same commit — there is no compiling intermediate state.
4. `D13`'s hoist (T2.1) changes the order of two blocks in the same function, and the re-entry paths depend on it.

**Sub-ordering inside the phase (do in this order):** T2.11 (decorator) → T2.3 (manager predicate, unit-testable alone) → **T2.12 (constructor refusal + signature change — FIRST, because every later caller depends on the new signature and the compiler enumerates all 37 sites in one pass)** → T2.4 (helper signature + `pick = nil` + `groupExcludeOutcome`) → T2.0/T2.1 (scope + hoist) → T2.5 (C1–C4 + the PD-15 group-chain exclusion) → T2.7/T2.8 (re-entry) → T2.9 (decision, PD-5 ordering) → T2.10 (imggen) → T2.2 (final collapse of the four entry sites, safe only once the predicate is everywhere) → **T2.13 (precedence pin + the `:857` deletion, which is only safe after T2.2)** → **T2.14 (audit — last, so it inspects the finished tree)** → §5 tests.

**Why T2.12 moves to the front of the sequence.** The signature change is the *only* step in this phase whose blast radius is mechanically enumerable — the compiler names all 42 call sites across 11 files. Doing it first means every later task is written against the final contract; doing it late means re-touching the same test files after the behavioural work. It also front-loads the RED blocker: if the constructor refusal turns out to break an existing test's assumptions, we learn that on day one, not at the gate.

**Parallelism that IS safe:** Phase 3 (FE) runs fully in parallel with Phase 2 — it depends only on Phase 1's frozen wire contract, not on enforcement.
