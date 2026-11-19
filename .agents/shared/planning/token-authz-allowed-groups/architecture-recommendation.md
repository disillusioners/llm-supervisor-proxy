# Architecture Recommendation — AUTH COMMISSION (`token-authz-allowed-groups`)

**Date:** 2026-11-18
**Worktree:** `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-token-authz` (`feature/token-authz-allowed-groups` @ `a3f0335`)
**Dispatched Pressure-Test Workers:**
- `868b4af1-1bfd-4592-8274-c7a1b1a84fb9` (`data-flow-design`) — candidate-list construction, mid-request rebuilds, advance/hedge/re-entry, sticky reorder, ultimate-escalation remnants, Anthropic/image-gen paths
- `e9f6e95a-1b11-4fcc-bfbc-1b46258a8e35` (`resilience-design`) — `store_group_rewrite.go` 4-phase sweep, `grouplb`/`credentiallb`, cross-token binding, `skipcache`, `modelscache`, `pkg/decision`
- `6c0229b4-817a-426c-86b7-4feccab3885f` (`security-design`) — escalation-mirror risk, `034` SQL empirical probes, fail-closed scan/cache/PATCH, shadow/claim aliases, rollback invariant

---

## 1. Concurrence on Construction-Time Exclusion

**VERDICT: CONCUR** with construction-time candidate exclusion (`Reach(T)` resolved once per request before candidate construction, enforced at list-build and member-pick seams with zero token plumbing in `race_coordinator.go` / `race_executor.go` / `policy.go`). No competing architecture (such as attempt-time checking or hot-loop token threading) is warranted, so a 5-axis competing-architecture matrix is omitted per commission scope.

**Reasoning (evidence-verified across all runtime paths):**
1. **Every downstream selector is strictly list-bound:** `race_coordinator.go` and `race_executor.go` contain zero client auth-token references. Chain advance reads `c.models[i+1]` (`:1906`), hedging reads `c.models[0]` (`:435`), credential failover operates strictly on the failed attempt's `modelID`, Anthropic's sequential walk ranges `arc.modelList` (`handler_anthropic.go:550`), and `decision/policy.go` only reorders the slice handed to `buildGroupPolicy` (`decision/group.go:118`).
2. **Eliminates existence oracles & telemetry pollution:** Removing unauthorized candidates before attempt selection guarantees zero upstream calls, zero cooldown writes, and zero attempt-level telemetry leakage for out-of-scope models.
3. **Preserves layer boundaries:** `pkg/decision/policy.go` keeps its "reorder, never exclude" doctrine (`:15-18`) and zero-cache import boundary (`:20-24`); `pkg/credentiallb` stays strictly model-preserving (`I13`); `pkg/grouplb` keeps its selector API unchanged (`A5`).
4. **Condition on concurrence:** Construction-time exclusion is airtight **if and only if** the plan defects cataloged in §3 below are resolved—specifically **PD-2** (Postgres `034` boot-crash guard), **PD-3** (zero cooldown writes on scope-excluded group picks), **PD-5** (`PATCH` keep-existing not erasing `Invalid`), **PD-6** (handler failing closed before `race_coordinator.go:338-340` empty-list fallback), **PD-7** (suppressing phantom picks on dropped fallback groups), and **PD-8** (`applyGroupRewrite` signature + D13 entry-gate hoist). *(PD-1 — the ADR D3/§7 vs `plan.md` §2.3 wildcard contradiction — has already been corrected in `adr-token-permission-model.md` in this pass.)*

---

## 2. Exhaustive Pressure-Test Verdict Table

Every surface required by the commission was audited directly against worktree `a3f0335`.

| # | Surface | Verified Code Site (`a3f0335`) | Covered by Planned Construction-Time Exclusion? | Defect / Plan Gap? |
|---|---|---|---|---|
| 1 | **Chat entry & chain assembly** | `pkg/proxy/handler_functions.go:196-206` (`:200-202` `[resolvedModel.ID] + FallbackChain`); `pkg/proxy/handler.go:783` (rewrite), `:839` (entry gate), `:991` (loop top), `:996` (coordinator) | **YES** (with C1 + hoisted gate) | **PD-8 🟡**: D13 hoist (`:839` before `:783`) lacks an explicit step/pin preventing binding/sticky writes before 403. **PD-12 🟡**: `:857` `len(AllowedModels)>0` survives as dead code violating D8. **PD-15 🟢**: `applyReachFilter` must allocate (never `list[:0]` in-place). |
| 2 | **Chain advance** | `pkg/proxy/race_coordinator.go:858` → `fallbackAdvanceTargetLocked` (`:1899-1912`, literal `c.models[i+1]` at `:1906`), `spawn(modelTypeFallback, …)` (`:436-458`) | **YES — PROVEN** | None in coordinator. Legacy idle `c.models[1]` (`:457`) is guarded by `len > 1` (`:437-440`, `:948-950`). Zero token references in coordinator/executor. |
| 3 | **Hedge (`modelTypeSecond`)** | `pkg/proxy/race_coordinator.go:421-435` (`c.models[0]` at `:435`), spawn `:947`; `SecondaryUpstreamModel` at `pkg/proxy/race_executor.go:539-563` | **YES** (ruled non-scope, L5) | **PD-13 🟡**: `SecondaryUpstreamModel` (`race_executor.go:545`) aliases the *wire* model on the same row; `plan.md` should cite it explicitly under L5/I13 alongside hedge. |
| 4 | **Peak-hour wire substitution** | `ResolvePeakHourModel` at `pkg/proxy/race_executor.go:320`, `internal_handler.go:322`, `pkg/store/database/store.go:2684`, `pkg/modelscache/models.go:1033`, `pkg/models/config.go:1270` | **YES** (same row identity) | **PD-13 🟡**: Substitutes upstream wire `InternalModel` within the *same* already-admitted row; plan was silent on it (now explicitly ruled non-scope in ADR D6/L5 & I13). |
| 5 | **Race coordinator empty-list fallback** | `pkg/proxy/race_coordinator.go:338-340` (`if len(models) == 0 { models = []string{cfg.ModelID} }`) | **PARTIAL (latent trap)** | **PD-6 🟡**: A 5th hidden constructor list build! If an empty post-filter list reaches `:338-340`, it re-injects `cfg.ModelID` and turns a scope-empty `403` into an attempt/`502`. Handler MUST fail closed with `403` pre-constructor. |
| 6 | **Chat re-entry** | Gate `pkg/proxy/handler.go:1138-1159`; body `:1196-1287` (`ExcludeGroupMember` `:1213`, `applyGroupRewrite(rc.modelListOriginal)` `:1231`, swap `:1248-1249`) | **YES** (via scoped twins + C1/C4) | **PD-8 🟡**: `applyGroupRewrite` signature change omitted from `plan.md` §4.3. **PD-11 🟡**: `ExcludeGroupMemberScoped` `(string, string, bool)` lacks `ScopeOnly` signal. **PD-3 🟡**: re-entry offer walk must not cool scope-refused offers. |
| 7 | **Fallback-position group (`idx > 0`)** | `pkg/store/database/store_group_rewrite.go:85/:91/:96` (`FailedGroupID`); `pkg/proxy/handler_groups.go:80-93` | **PARTIAL** | **PD-7 🟡**: When a fallback-position group is dropped by T1 (`GroupID==""`, `AnyGroup==true`), `applyGroupRewrite` mints a phantom `pick` with `GroupID==""`, triggering `handler.go:1138`, empty-id events, and blank `RouteModel`. **PD-10 🟡**: Group id inside `FallbackChain` passes `applyReachFilter` and hits credential-less 502. |
| 8 | **Ultimate escalation remnants** | Repo-wide grep (`pkg/ultimatemodel/` deleted; zero `UltimateModel*` non-test code; comments only at `ui/server.go:1079`, `providers/openai.go:995`, `stickyorder/doc.go:54`) | **YES — fully retired** | None. No live escalation/substitution path remains in code. |
| 9 | **Anthropic entry, walk & re-entry** | `pkg/proxy/handler_anthropic.go:326-336`, `:472` (rewrite), `:514` (gate), `:539` (sticky), `:548-767` (walk `:550`), `:728-765` (re-entry); `pkg/proxy/handler_groups.go:452-542` (`:466/:480/:504`) | **YES** (after C2/C3 + hoist) | Same **PD-8 🟡** (entry-gate hoist `:514` before `:472`), **PD-11 🟡**, **PD-3 🟡** as chat re-entry. No mid-walk re-resolution or peak-hour on Anthropic lane. |
| 10 | **Sticky / race-chain reorder** | `pkg/proxy/handler_groups.go:134-152` → `pkg/stickyorder/order.go:26-47` (`PromoteToFront`); `race_coordinator.go:1323` (`InvalidateOnFailure` only) | **YES — cannot inject** | None. `PromoteToFront` copies existing slice elements and cannot synthesize or alias ids. Filter-before-sticky rule at C1–C4 is confirmed exact. |
| 11 | **Group rewrite & 4-phase sweep (incl. Phase 4 skip-cache demoted sweep)** | `pkg/store/database/store_group_rewrite.go:55-113` (rewrite), `:160-286` (`pickServableGroupMember`: Phase 1 `:188-215`, Phase 2 `:217-242`, Phase 3 `:244-268`, **Phase 4 `:270-284`**), `:348-360` (`isServableGroupMemberBase`), `:434` (`isServableGroupMember`), `:489-520` (`ExcludeGroupMember`) | **YES** (when `isReachableMember` added to all 4 phases + `:280` + `:101-102`) | **PD-3 🟡**: Base-stale arm at `:206/:230` calls `ExcludeAndReselect(pick, 0)`, which `grouplb` coerces to `defaultCooldown` — MUST NOT be called for scope-excluded members! Phase 4 `:270-284` currently has NO predicate; adding `isReachableMember` at bucket-build + `:280` closes it. Backup-only & nested-group rules (`pkg/models/group.go:189-192`, `:170-175`, `:348-360`) verified safe. |
| 12 | **Group LB (`grouplb`) & 30m binding** | `pkg/grouplb/engine.go:445-600` (`getOrSelect`), `:604` (`OnGroupChanged`), `:788-799` (`ExcludeAndReselect` cooldown coercion/write) | **YES** (manager-gated) | **PD-3 🟡**: `grouplb/engine.go:788-791` coerces `0 → defaultCooldown` and writes `st.cooldowns[id]`, creating a cross-tenant availability side channel if scope exclusions trigger it. Same-token mid-revocation falls through safely to Phase 3 rescue (`N-2 🟢` test recommended). |
| 13 | **Cross-token conversation-hash binding** | `pkg/credentiallb/key.go:48-51` (`sha256(modelID + "\|" + tokenID + "\|" + leadingUserRun)`), salted at `pkg/proxy/handler.go:758-759` | **YES — structurally isolated** | None (structurally impossible for Token B to read/mutate Token A's binding). `N-3 🟢`: add a group-layer `TestConversationKey_TokenSalting` pin mirroring `race_coordinator_credfailover_test.go:466`. |
| 14 | **Credential LB & cred-failover** | `pkg/credentiallb/engine.go:226-241`; `pkg/store/database/store.go:2735-2790`; `race_coordinator.go:459-485`, `:809`, `:1927+` | **YES — strictly model-preserving** | None. Keyed by `modelID`, returns `CredentialID` only; `InternalModel` preserves row identity (`I13`). |
| 15 | **Skip-cache** | `pkg/skipcache/skipcache.go:3-30`, `:59-62`, `:88-105`; `pkg/decision/suppression.go:34-96` | **YES — ordering/attempt only** | None. Zero cache imports in `store_group_rewrite.go`, `grouplb`, `decision/policy.go`. Cannot add candidates or bypass reach. |
| 16 | **`pkg/modelscache` decorators** | `pkg/modelscache/models.go:1301-1319` (rewriter forwards), `:1337-1347` (trap doc), `:1431-1455` (`GetModelNative`); `pkg/modelscache/tokens.go:117-181`, `:241-256`, `:297-310`; `models.go:25` (`idToHash` 50k FIFO cap) | **YES** (with compile guard + struct flags) | **PD-14 🟡**: `idToHash` 50,000-entry FIFO eviction can leave `UpdateTokenPermission` cache-drop stale for ≤60s (or ≤24h stale-positive during DB infra outage). `ModelsInvalid`/`GroupsInvalid` on `*AuthToken` safely survive cache hits (`tokens.go:117-166`). |
| 17 | **Decision MODEL arm** | `pkg/decision/handler.go:340-354` (guard `:342`, call `:343`, 403 `:351`) | **YES** (after `scope.Allows` unification) | None once `len(AllowedModels)>0` guard is removed (`D8`). |
| 18 | **Decision GROUP arm & policy** | `pkg/decision/handler.go:317-326` (returns `:325` — **live Gen-3 bypass**); `pkg/decision/group.go:96-123` (`:101-114` gate, `:118` `buildGroupPolicy`, `:119` `order()`, `:120-123` 502); `pkg/decision/policy.go:58-96`, `:110-136` | **YES** (after filter before `:118` + scope-empty 403 arm) | Live bypass confirmed. **PD-15 🟢**: must filter on a **copy** of the group row before `:118` (`policy.go` stays byte-unchanged; mixed disabled + out-of-scope counts cleanly distinguish 403 vs 502). |
| 19 | **Image-gen** | `pkg/imggen/handler.go:3-4`, `:230-296` (entry gate `:267-268`), `:329-337` | **YES** | None. No fallback chains, no groups, single upstream POST. |
| 20 | **Shadow / claimed model IDs** | `pkg/proxy/handler.go:823-835`, `handler_functions.go:200-202`, `pkg/modelscache/models.go:283-284`, `pkg/decision/handler.go:313`, `decision/group.go:97` | **PARTIAL** | **PD-9 🟡**: No escalation (alias cannot bypass reach), **but** a token granting ONLY a group's `shadow_model_id` alias `S` (not canonical `G`) passes hoisted `Allows(S)` then gets 403 `ScopeExcluded` at C1–C4 — an undocumented narrowing with no `shadow_model_id` check in G0. |
| 21 | **Token scan, `034` SQL, PATCH merge, FE coupling** | `pkg/auth/token.go:40-68`, `pkg/auth/store.go:54-65`, `:137/:179/:246`, `:252-261`, `pkg/ui/server.go:2589-2599`, `useApi.ts:898-934`, `034` SQL | **PARTIAL (defects in plan)** | **PD-1 🔴 [FIXED IN ADR]**: ADR D3/§7 wildcard wording fixed to ABSENT-only. **PD-2 🔴**: PG `034` SQL crashes on `[`-prefixed invalid JSON. **PD-4 🟡**: SQLite `json_group_array` returns `'[]'`, not `NULL`. **PD-5 🟡**: `PATCH` keep-existing drops `Invalid` flags and heals `DENY-ALL` to `NULL`. **PD-15 🟢**: `'null'` literal & `omitempty` wording. |
| 22 | **MCP lane** | `pkg/mcp/auth.go:22-58` | N/A (different axis — server-level, non-goal) | None (startup log line planned per `decisions.md` Q9 — startup-log clause rejected fix-pass 2, decisions.md §6.1; non-goal stands). |

---

## 3. Plan & Implementation Defects (Numbered, Deduplicated, Severity-Rated)

1. **PD-1 🔴 [RESOLVED IN ADR — SYNC `decisions.md` S-8] Normative Escalation-Mirror Contradiction on Wildcard Definition**
   - *Finding:* `adr-token-permission-model.md` D3 (`:103`), §7 (`:307`, `:318`), and `decisions.md` S-8 (`:36`) previously allowed `"empty"` / `"absent or empty"` to mean wildcard, contradicting `plan.md` §2.3 (`ABSENT and ABSENT`) and Leader Ruling Q13(a). Implementing the old ADR text would promote `PATCH {"allowed_models": []}` into an unrestricted wildcard.
   - *Action taken:* Enriched `adr-token-permission-model.md` D3, D11, §7 to mandate **conjunctive ABSENT-only** (`NULL`/`''` on both axes + clean parse); explicit `[]` is **PRESENT-empty** (grants nothing). Ensure `decisions.md` S-8 wording references ABSENT-only for consistency.

2. **PD-2 🔴 `034` PostgreSQL Repartition Crashes Boot on `[`-Prefixed Malformed JSON (`plan.md:468-471`, `:878-887`)**
   - *Finding (empirically proven on `ensemble_dev`):* `t.allowed_models ~ '^\s*\['` in `034_add_allowed_groups.up.sql` does **not** short-circuit `t.allowed_models::jsonb <> '[]'::jsonb`. A malformed row beginning with `[` (e.g., `[broken`) raises `ERROR: invalid input syntax for type json`, aborting the in-process boot migration (`pkg/store/database/init.go:19`) and causing a boot outage loop. G0 pre-ship queries 1 & 2 (`plan.md:878-887`) suffer the identical crash/blind-spot.
   - *Required Fix:* Guard the PG `034` repartition and G0 queries with `pg_input_is_valid(t.allowed_models, 'jsonb')` (if live hosts are PG 16+) or a `DO $$ ... BEGIN ... EXCEPTION WHEN invalid_text_representation THEN ... END $$` per-row validation block.

3. **PD-3 🟡 Cross-Tenant Cooldown Poisoning on Scope-Excluded Group Picks (`store_group_rewrite.go:206/:230`, `grouplb/engine.go:788-799`)**
   - *Finding:* `pickServableGroupMember`'s stale-member path calls `ExcludeAndReselect(pick, 0)`. Inside `grouplb/engine.go:788-791`, `cooldown <= 0` is coerced to `e.defaultCooldown` and unconditionally written to `st.cooldowns[id]`. If `isReachableMember` branches into `ExcludeAndReselect`, any restricted token's request benches an out-of-scope group member for **all other tokens** (cross-tenant availability DoS).
   - *Required Fix:* Scope rejections in Phases 1–3 (`store_group_rewrite.go`) MUST skip `ExcludeAndReselect` cooldown writes (advance the local `seen` loop without writing `st.cooldowns`, falling through to Phase 3/4 bucket enumeration), and `ExcludeGroupMemberScoped` must never write cooldowns for scope-refused offers. Add a multi-token test asserting Token A's scope exclusion never cools M1 for wildcard Token B.

4. **PD-4 🟡 SQLite `json_group_array` Empty Result Returns `'[]'`, Not `NULL` (`plan.md:432-433`)**
   - *Finding (empirically proven on `modernc.org/sqlite v1.46.1`):* `SELECT json_group_array(j.value) ...` over 0 rows returns `'[]'` (whereas Postgres `json_agg` returns `NULL`). `plan.md:432-433` erroneously claims SQLite returns `NULL`. Under `plan.md` §2.3, `'[]'` is PRESENT-empty (valid), **but** `TestMigration034_RepartitionIdempotent` / P-G17 (`plan.md:830`) expects `allowed_models IS NULL` on SQLite, creating a dialect skew and test failure.
   - *Required Fix:* Wrap the SQLite `SET allowed_models = NULLIF((SELECT json_group_array(...)), '[]')` in `034` so both SQLite and PostgreSQL consistently set an emptied `allowed_models` column to `NULL` (ABSENT).

5. **PD-5 🟡 `PATCH` Keep-Existing Erases `ModelsInvalid`/`GroupsInvalid` (`pkg/ui/server.go:2592-2597`)**
   - *Finding:* When `PATCH /fe/api/tokens/{id}` omits one axis (`req.AllowedModels == nil`), the merge copies `currentToken.AllowedModels` (`nil` when `ModelsInvalid == true`) and writes `NULL` via `UpdateTokenPermission` (`pkg/auth/store.go:252-261`), silently healing a corrupt `DENY-ALL` token into an absent/unrestricted axis.
   - *Required Fix:* In `server.go` PATCH handler, if an omitted axis has `ModelsInvalid`/`GroupsInvalid == true` on `currentToken`, reject with `400` (operator must explicitly supply a valid array or `[]`) so an unreadable row can never be cleared into `NULL` by an unrelated PATCH. Pin with `TestPatch_KeepsInvalidAxis_DoesNotHeal`.
   - **⚠️ SUPERSEDED IN PLACE (fix-pass A, leader Ruling 2 — the architect's text above is RETAINED VISIBLE as the rejected alternative):** the ruled outcome is the **column-skip** — PATCH with an omitted axis **or explicit `null`** and an invalid flag returns **`200 {"success":true}`**, the column is **byte-untouched**, and **invalid-omitted NEVER heals** to `NULL`/wildcard. **No `400`.** The canonical pin is **`TestTokenAPI_PatchNilAxis_DoesNotHealDenyAll` (P-G26)**; `TestPatch_KeepsInvalidAxis_DoesNotHeal` is a retired alias.

6. **PD-6 🟡 `race_coordinator.go:338-340` Empty-List Constructor Fallback (`if len(models) == 0 { models = []string{cfg.ModelID} }`)**
   - *Finding:* A hidden 5th list-construction fallback inside `newRaceCoordinatorWithEvents`. If an empty filtered `rc.modelList` (e.g., from T3 pick/list divergence or fallback exhaustion) reaches coordinator construction, it silently re-injects `cfg.ModelID`, converting a scope `403` into an attempt/`502`.
   - *Required Fix:* Handlers (`handler.go`, `handler_groups.go`) MUST assert `len(rc.modelList) > 0` after `applyReachFilter` (returning `403 ScopeExcluded` on initial build) before calling `newRaceCoordinatorWithEvents`.

7. **PD-7 🟡 `applyGroupRewrite` Mints Phantom `pick` When Fallback-Position Group Is Scope-Dropped (`handler_groups.go:80-93`)**
   - *Finding:* When `res.AnyGroup == true` and fallback-position group(s) (`idx > 0`) are dropped by T1 (`res.GroupID == ""`), `handler_groups.go:80-93` still returns `pick != nil` with `GroupID: ""`. This trips `handler.go:1138`, runs `ExcludeGroupMember("", ...)`, emits two empty-id `group_*` events, pollutes `groupTriedMembers[""]`, and blanks `RouteModel`.
   - *Required Fix:* Return `pick = nil` whenever `res.GroupID == ""`.

8. **PD-8 🟡 `applyGroupRewrite` Signature Change & D13 Entry-Gate Hoist Omitted from `plan.md` §4.3 / §8 Steps**
   - *Finding:* `plan.md` §4.3(a)–(e) never specifies the updated `applyGroupRewrite` signature threading `reach` and `scopeExcluded`, and D13's entry-gate hoist (`handler.go:839` before `:783`; `handler_anthropic.go:514` before `:472`) lacks an explicit task/pin verifying zero group-binding or sticky writes on entry `403`.
   - *Required Fix:* Add explicit `applyGroupRewrite(..., reach models.ReachFilter)` tasking in `phase2-plan.md` T2.4 and a D13 regression pin asserting zero binding/sticky state on entry `403`.

9. **PD-9 🟡 Claimed / Shadowed Group Alias Grant Narrowing (`handler.go:823-835` vs `handler_functions.go:200-202`)**
   - *Finding:* Only groups claim (`modelscache/models.go:283-284`). A token whose `allowed_models` contains ONLY a group's `shadow_model_id` alias `S` (not canonical `G`) passes the hoisted entry gate `Allows(S)`, then `applyReachFilter` strips canonical `G` and its members → `403 ScopeExcluded`. Fail-closed (no escalation), but undocumented in `plan.md` §7/§15 and unchecked in G0.
   - *Required Fix:* Owner decision (see §6): either accept as strict row-id narrowing (document in §7 matrix + release notes + inspect `models.shadow_model_id` in G0) or define alias-to-group-id behavior explicitly.

10. **PD-10 🟡 Group ID Inside a `FallbackChain` Admitted by `applyReachFilter`, Fails at Runtime (`store_group_rewrite.go:101-102`)**
    - *Finding:* `RewriteGroupEntries` is single-pass; a group ID placed in a model's or member's `FallbackChain` is not expanded. Because `Reach(T)` includes granted group IDs, `applyReachFilter` leaves it in `modelList`, and `ResolveInternalConfigWithAffinity` (`store.go:2767-2773`) fails because group rows have no credentials.
    - *Required Fix:* Document in `plan.md` §7/§8 (write-time validation forbidding group IDs in `FallbackChain` or explicit out-of-scope note; fail-closed on permission).
    - **⚠️ CORRECTION IN PLACE (gen-10 re-pin, 2026-11-19 — the architect's capture above is RETAINED VISIBLE, the landed reality differs):** the finding's premise "`RewriteGroupEntries` is single-pass; a group ID placed in a model's or member's `FallbackChain` is not expanded" is **FALSE for ENTRY-chain groups** — `handler_functions.go:200-202` places `[resolvedModel.ID, resolvedModel.FallbackChain...]` into `modelList` and `store_group_rewrite.go:105-181` expands every `kind='group'` **entry** into its in-reach member, replacing the raw group id ⇒ no credential-less group row, no runtime 502 (pinned `TestChainGroupEntry_NeverAdmitted_No502`, LANDED `1e65774`). The **narrower** surviving wart is a group id inside a group **member's** own `FallbackChain` (appended at rewrite time, never re-scanned) — pre-existing, documented, unpinned, not a scope escape.

11. **PD-11 🟡 `ExcludeGroupMemberScoped` Return Tuple Lacks Scope-Exhaustion Signal (`plan.md` §4.3(a), §9.4)**
    - *Finding:* Returning `(replacement, pool, ok)` collapses scope-exhaustion and unservability into `ok=false`, making §9.4's deliberate availability fallback inferred rather than explicit/testable at the seam.
    - *Required Fix:* Return a small outcome struct `{Replacement, Pool, OK, ScopeOnly}` so callers and unit tests observe the reason explicitly.

12. **PD-12 🟡 Surviving Per-Axis Guard at `pkg/proxy/handler.go:857` (`len(authToken.AllowedModels) > 0`)**
    - *Finding:* Dead today (404 fires at `:589-606`), but violates D8 and would leak group-only restricted tokens if external pass-through is ever re-introduced.
    - *Required Fix:* Delete/unify `:857` in Phase 2 and extend the grep guard (`P-G22`) to forbid any `len(.*AllowedModels)` outside `pkg/auth`.

13. **PD-13 🟡 Wire-Level Row Substitutions (`ResolvePeakHourModel` & `SecondaryUpstreamModel`) Omitted from `plan.md` Scope Table**
    - *Finding:* `ResolvePeakHourModel` (5 sites) and `SecondaryUpstreamModel` (`race_executor.go:545`) swap upstream wire strings within the *same* `models.id` row. Now explicitly ruled non-scope in ADR D6/L5 & I13; mirror in `plan.md` §8.2.

14. **PD-14 🟡 Lossy `idToHash` Cache Index in `CachedTokenStore` (`modelscache/models.go:25`, `tokens.go:297-304`)**
    - *Finding:* `idToHash` is FIFO-capped at 50,000 entries. Updating a token whose hash was evicted leaves old grants cached for ≤60s (or ≤24h stale-positive during DB infra outage). Acknowledge in `plan.md` §11.

15. **PD-15 🟢 Minor Spec Hygiene Items**
    - **`ScanGrant` on JSON literal `'null'`:** map `'null'` to `INVALID` (not `present=true, ids=nil` → write-back `NULL`).
    - **`plan.md` §2.8 `omitempty` wording:** `TokenResponse` uses `*[]string` (`pkg/ui/server.go:2634`, `:2698`), emitting JSON `null` rather than omitting the key; align §2.8 wording (`null` ≡ ABSENT).
    - **Decision row copy:** `pkg/decision/group.go:118` MUST filter a copy of `group.Members`, never mutating the cached slice.
    - **Cross-token & same-token binding pins:** add `TestConversationKey_TokenSalting` for the group layer (`credentiallb/key.go:48-51`) and a same-token mid-binding revocation Phase-3 rescue test.

---

## 4. Rollback & Single-Image Coupling Invariants (Summary — Normative Home in ADR §7.1–§7.2)

- **Rollback Ordering Invariant (Q2(b)):** **Reverse data repartition FIRST, image/helm rollback SECOND.** Under the pre-034 binary, `IsModelAllowed` maps `len(AllowedModels) == 0` (`NULL` **or** `'[]'`) to **wildcard** (`pkg/auth/token.go:116`). Rolling back the image while migrated narrow tokens (`allowed_models = NULL`) or post-ship `'[]'` rows exist on disk turns them into full-access wildcards on public `/v1/*`. Reverse-repartition under the new image keeps rows RESTRICTED (`allowed_models` PRESENT). The reverse UPDATE ships in `034_*.down.sql` (with the `schema_migrations` row-deletion note), and pre-ship malformed-row inspection on both live PG hosts is a mandatory gate step.
- **Single-Image Delivery Coupling Invariant (Q13(a)):** The FE PATCH-builder (`[]` on clear, omitted/`null` on keep) and BE explicit-empty serialization (`nil → NULL`, `[] → "[]"`) MUST ship in the same binary/embedded-SPA image. Any future decoupling of FE/BE delivery re-opens the split-brain escalation trap and is forbidden without re-ruling.

---

## 5. Future-Proofing — The "New Lane Contract" (Summary — Normative Home in ADR §6.1)

Any new request lane, model kind, hedge, escalation path, or candidate source must satisfy all 7 checks in the same commit:
1. Single `authz.Resolve` post-auth, pre-construction (zero `len(AllowedModels)` checks).
2. Sequenced before bindings, sticky state, cooldowns, or metering; error precedence preserved (`401 → 404 → 400 misroute → 403 → 502`).
3. Construction-time list removal (`ReachFilter` / `applyReachFilter`) **before** `applyStickyOrder`; no attempt-time skips; no constructor empty-list fallback.
4. `ScopeExcluded` (`403 access_denied`) distinguished from unservable (`502`); zero cooldown writes on scope exclusion.
5. Explicit alias / shadow-claim / multi-hop chain closure in the §7 semantics matrix.
6. Explicit classification of any upstream target selection as **row selection** (scoped) vs **same-row wire substitution** (non-scope `I13`).
7. Reach-completeness (`I15`), hot-loop zero-`authz`, and zero-`len(.*AllowedModels)` structural gate tests, plus repo-recorded deferrals (`I16`).

---

## 6. Open Decisions / Action Items for Commission Owner Before Build

1. **PD-2 — Live PostgreSQL Major Version:** Confirm whether primary & daoduc run PG 16+ (enabling `pg_input_is_valid(allowed_models, 'jsonb')`) or require a `DO $$ ... EXCEPTION WHEN invalid_text_representation ... $$` block in `034` and G0 probes.
2. **PD-4 — SQLite `034` `NULLIF`:** Confirm adding `NULLIF((SELECT json_group_array(...)), '[]')` to SQLite `034` so SQLite and Postgres produce identical `allowed_models = NULL` post-repartition.
3. **PD-9 — `shadow_model_id` Alias Grants:** Confirm accepting strict row-id narrowing (with a read-only G0 query checking `models.shadow_model_id` against live `auth_tokens.allowed_models` + release-note documentation), rather than adding claim-alias expansion to `Reach(T)`.
