# PHASE 1 — THE FROZEN TOKEN SCOPE CONTRACT + STORAGE + ADMIN API

**Lane:** `feature/token-authz-allowed-groups` · **Base:** `a3f0335` · **Worktree:** `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-token-authz`
**Read first:** `plan.md` §2 (the contract — this phase **implements** it and must not deviate), §4.1–§4.3, §5, §6.
**Phase verdict:** **INDEPENDENT-AS-A-UNIT, and the FOUNDATION for phases 2 and 3.** It has no dependency on Phase 2 or Phase 3. Phases 2 and 3 both depend on it for **compile-level** reasons (interface + struct + `authz` package + `models.ReachFilter`), which is why it lands first. Internally it is sequential (interface → store → scan → resolver → migration → API), because each step's tests anchor on the previous step's symbols.

---

## 1. Objective

Deliver the **frozen token-scope contract** so that backend and FE can proceed in parallel:

1. The **data model** (`auth.OptionalTokenPermission` carrying `auth.OptionalAllowlist{Set bool; Values *[]string}` per axis — the ONE frozen write payload, A4; plus `AuthToken.AllowedModels/AllowedGroups/ModelsInvalid/GroupsInvalid`) and the **tri-state scanner** (`ScanGrant`) with **both** fail-open branches closed.
2. The **one resolver + one predicate** (`pkg/authz.Scope`, `authz.Resolve`, `(*Scope).Allows`) — the only authority any lane may consult.
3. The **seam vocabulary** that lets a permission predicate cross package boundaries without an import cycle (`models.ReachFilter`, `models.GroupRewriteResult.ScopeExcluded`).
4. **Storage**: migration `034` (additive column **+ the pre-ship data repartition**) with the registry append and its sixth trap comment; the store's nil-vs-empty serialization fix.
5. The **admin API**: `allowed_groups` on POST/PATCH/GET, the two-state PATCH semantics (omitted/null = keep, `[]` = clear — leader Ruling 2), write-time validation. (The POST 64 KB body cap was REJECTED fix-pass 2; only the landed PATCH cap remains.)
6. The **compiler-forced blast radius**: the `TokenStoreInterface` re-signing across 9 mocks + the decorator, and `allowed_groups` added to the **8** hand-rolled `auth_tokens` DDL fixtures.

**Leader rulings folded into this revision (2026-11-18 plan-revision pass).** Five leader rulings land inside this phase and are **normative over any earlier wording in this file**:

| Ruling | Lands in | Contract, one line |
|---|---|---|
| **PD-2 (RED)** | T1.7 (migration `034`, **PG** twin) | The PG repartition uses a **version-agnostic** `DO $$ … BEGIN … EXCEPTION WHEN invalid_text_representation THEN … END $$` **row-by-row** guard. A malformed row is **SKIPPED, never aborting** the in-process boot migration (`pkg/store/database/init.go:19`). No dependency on PG 16+ `pg_input_is_valid`. The G0 pre-ship queries 1 & 2 (Phase 3 T3.10) carry the identical protection. |
| **PD-3 (RED)** | T1.3 (store `UPDATE`), T1.8 (PATCH merge), T1.11 (pins) | A `nil` axis on `PATCH /fe/api/tokens/{id}` **omits that column from the SQL `UPDATE` entirely**. Untouched raw bytes stay untouched, so an existing `DENY-ALL` (`ModelsInvalid`/`GroupsInvalid`) malformed row can **never** be healed into `NULL`/wildcard by an unrelated PATCH. |
| **PD-4 (AMBER)** | T1.7 (migration `034`, **SQLite** twin) | The SQLite repartition wraps `json_group_array(…)` in **`NULLIF(…, '[]')`**, so an emptied axis writes **`NULL`** — dialect parity with PG's `json_agg` (which returns `NULL`, not `'[]'`). The SQLite JSON1 verification step stays. |
| **PD-7 (AMBER)** | T1.4 (decorator) | The token cache **clones** `AllowedModels`/`AllowedGroups` (and carries the invalid flags) on every cache read — no shared backing array across goroutines or with the caller — covers `AllowedGroups` on both invalidation paths, and **states the `idToHash` 50 000-entry FIFO bound** and its ≤60 s (≤24 h stale-positive) consequence. |
| **PD-8 (AMBER)** | T1.8b (validation), T1.11 (pins) | Validation is unambiguous: **(a)** malformed/non-array ⇒ `400`; **(b)** every id in `allowed_groups` must **exist AND be a group** (`400` on unknown *or* non-group); **(c)** `allowed_models` keeps the Q4 rule (any kind — chat/decision/image-gen — and group ids tolerated on write with target-only semantics). Validation runs on the **inbound set only**; reads stay lenient. **There is no `PUT` route** — "POST/PUT/PATCH" means **POST + PATCH** (T1.8). |

**Nothing in this phase changes request-time routing behaviour.** A binary built at the end of Phase 1 stores, returns and validates the new axis, and the resolver exists and is unit-tested — but no lane calls it yet. That is deliberate: Phase 1 is reviewable in isolation as "the permission model exists and is correct"; Phase 2 is "the model is enforced".

---

## 2. Files owned (exact)

### 2.1 Production code (create / modify)

| File | Action | What |
|---|---|---|
| `pkg/auth/permission.go` | **NEW** | **Define the frozen write payload — do NOT spec a flat two-slice struct** (A4/fix-pass A: a Set-less struct would RE-CREATE the PD-3 heal bug): `type OptionalAllowlist struct{ Set bool; Values *[]string }` + `type OptionalTokenPermission struct{ AllowedModels, AllowedGroups OptionalAllowlist }` (§plan 4.1). Doc comment carries the `Set`/`Values` rule verbatim. |
| `pkg/auth/token.go` | modify | (a) `AuthToken` gains `AllowedGroups []string`, `ModelsInvalid bool`, `GroupsInvalid bool` beside `AllowedModels` (`:33`); (b) **rewrite the `:33` comment** to the nil-vs-empty rule; (c) add `ScanGrant(raw interface{}) (ids []string, present bool, invalid bool)` — the tri-state scanner — with **both** fail-open branches (`:52-55` unknown raw type, `:62-66` malformed JSON) returning `invalid=true`; (d) keep `ScanAllowedModels` as a deprecated shim delegating to `ScanGrant` (comment: *"NOT an authorization decision"*); (e) demote `IsModelAllowed` (`:110-125`) with a comment stating it is a **model-axis helper only, never the authorization decision** (all four lanes stop calling it in Phase 2 — a Phase 2 grep guard proves it). |
| `pkg/auth/store.go` | modify | (a) `TokenStoreInterface` (`:21-28`) — `CreateToken` and `UpdateTokenPermission` take `perm auth.OptionalTokenPermission` (ONE write-payload type, A4; supersedes `auth.TokenPermission`) replacing `allowedModels []string` (§Q5 (a); **one struct, not two adjacent slices** — a swap must be impossible); (b) `CreateToken` (`:40-90`): serialize **both** axes with `x != nil` (not `len(x) > 0`), INSERT gains `allowed_groups` (`$8` / `?`), echo into the struct; (c) `ValidateToken` (`:93-144`) / `ListTokens` (`:147-185`) / `GetTokenByID` (`:213-249`): SELECT gains `allowed_groups`, scan via `ScanGrant` into `(ids, present, invalid)` → `AllowedGroups` + `GroupsInvalid`; the existing `allowed_models` scan also upgraded to `ScanGrant` → `ModelsInvalid`; (d) `UpdateTokenPermission` (`:252-284`): UPDATE writes **only the `Set` axes** with the `Set`/`Values` rule (dynamic SET clause — PD-3). |
| `pkg/modelscache/tokens.go` | modify | `CreateToken` (`:263-275`) and `UpdateTokenPermission` (`:297-304`) forward the `auth.OptionalTokenPermission` payload verbatim (ONE write-payload name, A4); `UpdateTokenPermission` **keeps** its delegate-then-drop-the-cached-entry behaviour (the drop is what stops a stale positive from masking a permission change). No caching of anything new. |
| `pkg/authz/scope.go` | **NEW** | `Scope`, `Resolve`, `Allows`, `IsUnrestricted` (§plan 4.1). `Resolve` takes `membersOf func(groupID string) []string` injected by the caller — **`pkg/authz` must NOT import `pkg/proxy`, `pkg/decision`, `pkg/store/database`, or `pkg/modelscache`**; it may import `pkg/auth` only. |
| `pkg/models/reach.go` | **NEW** | `type ReachFilter interface{ Allows(modelID string) bool }` — the cycle-breaker (§plan 4.1). Imports nothing. |
| `pkg/models/group_rewrite.go` | modify (additive) | `GroupRewriteResult` gains `ScopeExcluded bool` with a doc comment tying it to the 403-vs-502 distinction. **No interface change** — `models.GroupRewriter` (`:82-121`) is untouched. |
| `pkg/store/database/migrations/sqlite/034_add_allowed_groups.up.sql` | **NEW** | §plan 5.2 body verbatim (column + repartition + `BEGIN/COMMIT`). |
| `pkg/store/database/migrations/sqlite/034_add_allowed_groups.down.sql` | **NEW** | §plan 5.2 down body (reverse repartition + drop column + the "no down-runner, delete the schema_migrations row" note). |
| `pkg/store/database/migrations/postgres/034_add_allowed_groups.up.sql` | **NEW** | §plan 5.2 PG body. |
| `pkg/store/database/migrations/postgres/034_add_allowed_groups.down.sql` | **NEW** | PG twin of the down. |
| `pkg/store/database/migrate.go` | modify | Append `{"034", "034_add_allowed_groups.up"},` **immediately after `:82`** with the **sixth** Amendment-1 trap comment (§plan 5.3, verbatim). |
| `pkg/ui/server.go` | modify | `CreateTokenRequest` (`:2501-2505`) + `UpdateTokenPermissionRequest` (`:2563-2565`) + `TokenResponse` (`:2491-2498`) gain `AllowedGroups *[]string \`json:"allowed_groups,omitempty"\``; `createToken` (`:2646-2712`) decodes/passes/echoes both axes (**NO POST body cap — rejected fix-pass 2**); `updateTokenPermission` (`:2568-2611`) merge per the two-state contract (`nil`/omitted = keep = column untouched, `[]` = clear); `listTokens` (`:2613-2644`, projection `:2629-2639`) carries `allowed_groups`; a new `validateTokenGrants(ctx, perm) (errMsg string)` helper applies §plan 6.3 (groups: exists + `kind='group'`; models: exists; **inbound set only**), returning the bare `{"error":"…"}` shape. |

### 2.2 Test files owned by this phase — **signature/DDL only**

Phase 1 owns these edits **only** as mechanical re-signing / fixture DDL, because the compiler forces them into the same atomic commit as the interface change. **Behavioural test content in these packages is Phase 2's** (§8.2 of this file — the hand-off).

| File | Phase-1 edit |
|---|---|
| `pkg/decision/handler_test.go:296/:341` | re-sign the mock's `CreateToken` / `UpdateTokenPermission` |
| `pkg/imggen/handler_test.go:175/:220` | re-sign |
| `pkg/mcp/auth_test.go:29/:45` | re-sign |
| `pkg/modelscache/tokens_test.go:57/:104` | re-sign (`fakeTokenStore`) |
| `pkg/proxy/authenticate_test.go:47/:67` **and** `:348/:364` | re-sign (both mocks incl. `slowMockTokenStore`) |
| `pkg/proxy/handler_anthropic_tokenid_parity_test.go:230/:240` | re-sign |
| `pkg/proxy/handler_test.go:2105/:2121` | re-sign (`modelAccessMockTokenStore`) |
| `pkg/ui/handlers_imggen_test.go:740/:764` | re-sign |
| `pkg/ui/handlers_token_test.go:35/:66` | re-sign (`mockTokenStore`) + the DDL-free mock carries the new field |
| `pkg/auth/store_test.go:53-62` | DDL fixture: add `allowed_groups TEXT DEFAULT NULL` (has `allowed_models` at `:60`) |
| `pkg/mcp/auth_routing_test.go:58-67` | DDL: add `allowed_groups` |
| `pkg/mcp/e2e_test.go:72-81` | DDL: add `allowed_groups` |
| `pkg/mcp/handlers_api_test.go:49-58` | DDL: add `allowed_groups` |
| `pkg/mcp/mcp_endpoint_split_validation_test.go:60-69` | DDL: add `allowed_groups` |
| `pkg/mcp/test_helpers_test.go:43-52` | DDL: add `allowed_groups` |
| `pkg/proxy/handler_integration_test.go:52-59` | DDL: add `allowed_groups` (has `allowed_models` at `:58`) |
| **`pkg/ui/handlers_usage_test.go:33-41`** | DDL: **add BOTH** `allowed_models` and `allowed_groups` — the 8th fixture, which today has neither (`plan.md` §5.7, `decisions.md` D-1). Verified it never constructs a token store, so this is a latent break being closed proactively. |

**Rule that prevents a Phase-1/Phase-2 collision:** Phase 1 touches those files **only** on the lines the compiler names (signatures) and the `CREATE TABLE` literals. Phase 2 owns every *new* test case and every behavioural assertion inside them.

### 2.3 Files explicitly NOT owned by this phase

`pkg/proxy/handler.go`, `pkg/proxy/handler_anthropic.go`, `pkg/proxy/handler_groups.go`, `pkg/proxy/handler_functions.go`, `pkg/proxy/race_coordinator.go`, `pkg/proxy/race_executor.go`, `pkg/decision/handler.go`, `pkg/decision/group.go`, `pkg/decision/policy.go` (**never**), `pkg/imggen/handler.go`, `pkg/store/database/store_group_rewrite.go`, `pkg/modelscache/models.go`, `pkg/ui/frontend/**` — all **Phase 2 / Phase 3**.

---

## 3. Tasks (with `file:line` anchors)

### T1.1 — `OptionalTokenPermission`/`OptionalAllowlist` + `AuthToken` fields + comment rewrite
- **Files:** `pkg/auth/permission.go` (new), `pkg/auth/token.go:26-34`.
- **Anchors:** `AuthToken` `pkg/auth/token.go:26-34`; the wrong comment is `:33` (*"nil/empty = all models allowed"*).
- **Do:** add the four fields; add the struct; rewrite `:33` to state nil=ABSENT / non-nil(possibly empty)=PRESENT and point at `authz.Resolve` for the wildcard rule. Add the `denyAll` doc for the two `Invalid` flags **explaining why they live on the struct** (`pkg/modelscache/tokens.go:108-125` caches the `*AuthToken`).
- **Done when:** `go build ./pkg/auth/...` green; no change to any existing literal's meaning (all existing `AuthToken{...}` constructions still compile).

### T1.2 — The tri-state scanner (`ScanGrant`) closing BOTH fail-open branches
- **Files:** `pkg/auth/token.go`.
- **Anchors:** `ScanAllowedModels` `:40-68`; **unknown-raw-type** `:52-55`; empty string `:57-59`; malformed JSON `:62-66`; `nil` `:41-43`.
- **Do:** add `ScanGrant` returning `(ids, present, invalid)`. `nil` → absent; **`""`/whitespace TEXT → INVALID (fail-closed, Phase-1.1 `f9adbce` — NOT absent)**; `string`/`[]byte` valid JSON (incl. `"[]"`) → present; **non-array JSON literals (objects, `123`, `"x"`, `null` as TEXT) → `invalid=true`**; the unknown-raw-type **and** unmarshal-failure arms → `invalid=true` **and** keep/extend the `SECURITY:` log (it must name the axis). Retain `ScanAllowedModels` as a shim.
- **Hard requirement (binding clarification #2):** the **unknown-raw-type** branch is a fail-open branch too — the commission's evidence named only malformed JSON. The `TestScanAllowedModels` row at `pkg/auth/token_test.go:120-124` (input `123` ⇒ expects `[]string{}`) documents today's conflation and must be **replaced** in T1.7.
- **Done when:** unit tests in T1.7 prove all five inputs map to the right tri-state.

### T1.3 — Store: nil-vs-empty serialization + `allowed_groups` column plumbing
- **Files:** `pkg/auth/store.go`.
- **Anchors:** interface `:21-28` (`CreateToken` `:23`, `UpdateTokenPermission` `:27`); create serialization `:54-65` (**the `len(allowedModels) > 0` guard is at `:57`**); INSERT `:67-72`; echo `:79-87`; validate SELECT/scan `:105-110` / `:137`; list `:148-163` / `:179`; by-id `:219-226` / `:246`; update serialization `:252-261` (`:254`); UPDATE `:263-268`.
- **Do:**
  1. `interface`: `CreateToken(ctx, name string, expiresAt *time.Time, createdBy string, perm auth.OptionalTokenPermission) (string, *AuthToken, error)`; `UpdateTokenPermission(ctx, id string, perm auth.OptionalTokenPermission) error` (ONE write-payload type — A4; `auth.TokenPermission` is retired as the write type per the Q5 supersession line).
  2. Serialize each axis with `if axis != nil { json.Marshal }` ⇒ `nil`→NULL, `[]`→`"[]"`, ids→array. **This is the escalation-trap fix** (`decisions.md` Q13 / R-6): leaving `len(x)>0` while the wildcard rule becomes conjunctive would make "clear the groups" write NULL on both axes ⇒ wildcard.
  3. All four SQL statements gain `allowed_groups`; all three read sites use `ScanGrant` and set `GroupsInvalid`/`ModelsInvalid`.
  4. **PD-3 (RED) — THE COLUMN-SKIP RULE for `UpdateTokenPermission`.** The `UPDATE` must **not** write a column whose axis is `nil`, because a `nil` axis on the request means "keep whatever is stored" and the stored bytes may be a **malformed `DENY-ALL` row** (`ScanGrant` ⇒ `present=false`+`invalid=true`, surfacing as `AllowedModels == nil` + `ModelsInvalid == true`). Today's shape (`pkg/auth/store.go:252-261`) collapses that case into `allowedModelsJSON = nil` ⇒ `SET allowed_models = NULL` ⇒ a corrupt deny-all row is **silently healed into an absent (wildcard) axis** — the exact failure PD-3 names. Build the statement from the non-nil axes only:
     ```go
     // PD-3: a nil axis is OMITTED from the UPDATE. Never derive the SQL
     // column set from the merged "current" values — a malformed
     // (DENY-ALL) row reads as nil, and writing NULL would heal it into a
     // wildcard. The repair path is an explicit valid array (or []) for
     // that axis on a later request.
     sets, args := []string{}, []interface{}{}
     if perm.AllowedModels != nil { sets = append(sets, "allowed_models = ?"); args = append(args, marshalAxis(perm.AllowedModels)) }
     if perm.AllowedGroups != nil { sets = append(sets, "allowed_groups = ?"); args = append(args, marshalAxis(perm.AllowedGroups)) }
     if len(sets) == 0 { return nil }   // no-op: nothing to update, NOT an error
     query := "UPDATE auth_tokens SET " + strings.Join(sets, ", ") + " WHERE id = ?"  // $n on PostgreSQL
     ```
     - The **store's** `nil` means "omit the column"; the **API layer** (T1.8) is what decides that an omitted request axis is `nil` and an explicit `[]` is non-nil-empty. Two independent gates, one invariant.
     - Both axes `nil` ⇒ a **no-op statement skip** (`affected == 0` must **not** be reported as `ErrTokenNotFound` on this arm — return `nil`, since "nothing to change" is a success; keep `ErrTokenNotFound` for the case where a real `UPDATE` matched zero rows).
     - **Existence is the API layer's job, not the no-op arm's.** Because the no-op arm returns without touching the table, a `PATCH {}` on a **nonexistent** id would return success from the store. That is correct at this layer: T1.8 keeps the existing `GetTokenByID` pre-check (`pkg/ui/server.go:2579-2587`, 404 mapping preserved), so the 404 contract lives at the API boundary and does not depend on `RowsAffected`. Pinned by `TestTokenAPI_PatchNonexistent_404` (T1.11) so a future refactor cannot delete the pre-check believing the store covers it.
     - **Divergence note (one line):** the architect proposed rejecting the PATCH with `400` when an omitted axis is `Invalid` (arch PD-5); the **LEADER RULED FOR COLUMN-SKIP**, which is strictly better — it needs no knowledge of `Invalid` in the store, keeps `pkg/auth` free of API policy, and still never heals the row.
- **ONE write-payload type (fix-pass 2, A4 + leader Ruling 2):** the write payload is `auth.OptionalTokenPermission{ AllowedModels, AllowedGroups auth.OptionalAllowlist }` with `OptionalAllowlist{ Set bool; Values *[]string }` — **the single frozen name** (supersedes `auth.TokenPermission` as the write type; Q5 supersession line in `decisions.md`). The wire is **TWO-state per axis** (Ruling 2 = Q13(a); the cycle-1 third "reset-to-absent" state is REVERSED and no longer exists):
  - request key **omitted, or present with explicit `null`** ⇒ `Set:false` — the API layer maps BOTH to `Set:false`; the SQL `UPDATE` omits that column (raw bytes survive, including a seeded malformed `DENY-ALL` row; **invalid-omitted NEVER heals**).
  - `[]` ⇒ `Set:true, Values:&[]string{}` ⇒ write `'[]'` (PRESENT-empty, grants nothing).
  - ids ⇒ `Set:true, Values:&ids` ⇒ write the JSON array (replace).
  - `Set:true, Values:nil` (write `NULL`) **exists in the store-level shape but is UNREACHABLE from the PATCH wire** under Ruling 2 — no FE or external caller can produce it; it is reserved for internal/store-level callers only. **Wildcard promotion on EDIT is intentionally inexpressible** (re-create the token; release note + FE-9 note: Unrestricted mode = create-only in FE edit).
  The statement is assembled **dynamically** from the `Set` axes only; both axes `Set:false` ⇒ **no `UPDATE` executed at all**, `err == nil` (a no-op is a success, not `ErrTokenNotFound`). **The scan-path heal is ELIMINATED by the no-merge** (a malformed column can never be healed by an unrelated PATCH — only an explicit `[]`/ids write replaces it). **Reconciliation with the LANDED code (C3):** the landed interim payload `auth.TokenPermission` with the nil-axis-skip rule is **behaviorally identical** (its `nil` axis ≡ `Set:false`); the completion commit renames to the frozen Optional* names — semantics unchanged.
  - **`CreateToken` keeps the full-write shape** (both axes `Set:true` — no keep-semantics on create).
  - **All-axes-omitted PATCH:** keep the `GetTokenByID` 404 pre-check, execute no column UPDATE, return `200 {"success":true}` (P-G26-equivalent API pin: PATCH omitted/null ⇒ 200, column byte-untouched, invalid-omitted NEVER heals).
- **Done when:** `pkg/auth` compiles; `pkg/modelscache` + the 9 mocks re-signed (T1.4/T1.6); round-trip tests (T1.11) prove all THREE reachable update states: **omitted = byte-identical (incl. a seeded malformed row) AND explicit `null` = byte-identical (both ⇒ keep)**, **`[]` = `'[]'`**, **ids = JSON array**.

### T1.3c — Fixture-audit note (OI-2)

**Audit result for the `len(x)>0` → `x != nil` serialization switch** (under the frozen `auth.OptionalAllowlist` write-payload shape):

- **Exactly ONE fixture FLIPS:** `pkg/auth/store_test.go:782-812` `TestCreateTokenWithEmptyAllowedModels` — creates with `[]string{}` twice (`:790`/`:797`), asserts read-back nil at `:804-806` ("Empty []string stored returns nil, want nil") — bakes "explicit [] = wildcard" into the store layer. Under `x != nil` it writes `'[]'` and reads back non-nil empty ⇒ **MUST be consciously rewritten** (expect non-nil empty) or retired. Its `:809-811` sub-assertion stays green.
- **nil-clear pins stay green under the switch but get re-signed by the T1.3 interface change:** `TestUpdateTokenPermission` `:551` ("Back to all-allowed (nil)") and `TestUpdateTokenPermissionClearAllowedModels` `:770` (nil-to-clear).
- **Enforcement-semantics conflict (separate from serialization, flag it):** `token_test.go:210-221` (`TestAuthToken_IsModelAllowed` "empty allowed models allows any model") contradicts the new "explicit [] grants nothing" rule — covered by the `IsModelAllowed` demotion (plan §4.1) but listed so the gate's set arithmetic expects the modified test.
- **Mock divergence warning:** `handlers_token_test.go:66-76` `mockTokenStore.UpdateTokenPermission` assigns directly (no serializer) — cannot catch NULL-write regressions; new `[]`-round-trip handler tests must assert at the REAL store or pin the mock's limitation.

### T1.4 — Decorator forward (`pkg/modelscache/tokens.go`) + **PD-7: clone-on-read + the `idToHash` bound**
- **Anchors:** `CreateToken` `:263-275` (verified: it delegates then `storePositive(hash, token, expiresAt, now)`); `UpdateTokenPermission` `:297-304` (verified: delegate → `c.entries.Delete(hash)` and **keeps** the `idToHash` entry); `storePositive` `:117` (verified: stores the caller's `*auth.AuthToken` **pointer**, no copy); `rememberID` `:97-110` (FIFO cap at `:103` against `defaultIDIndexCap`); `cachedVerdict` `:153-171` (verified: `return e.token, nil, true` at `:165` — the **cached pointer is handed out as-is**).
- **Do:**
  1. Re-sign both methods to the `auth.OptionalTokenPermission` payload (ONE write-payload type, A4), forward verbatim, **preserve the entry-drop**. Add a comment: the drop is the invalidation that makes a group-grant change visible within one validation.
  2. **PD-7 (a) — clone on cache read.** `cachedVerdict`'s positive arm currently returns `e.token` (`:165`), i.e. the **same `*auth.AuthToken`** to every concurrent request. Today that is benign (nobody mutates it); after this commission the struct carries two **slices** (`AllowedModels`, `AllowedGroups`) that a future caller could sort/append, and a shared backing array across goroutines is a data race the race detector flags and a correctness hazard the type system does not. Return a **fresh copy with fresh slices**:
     ```go
     func cloneToken(t *auth.AuthToken) *auth.AuthToken {
         if t == nil { return nil }
         c := *t
         c.AllowedModels = append([]string(nil), t.AllowedModels...)
         c.AllowedGroups = append([]string(nil), t.AllowedGroups...)   // nil stays nil — ABSENT must survive the cache
         return &c
     }
     ```
     Apply at **both** read arms (`cachedVerdict` fresh-positive `:165`, and the stale-positive arm used in the infra-error fallback — grep `tierStalePositive`/`e.token` in the same file) and at `storePositive` (`:117`) so the *cached* value is itself a clone of what the inner store returned (defence in depth: `CreateToken` today caches the very pointer it also returns to the caller at `:274`).
     - **Flag preservation is the point, not a side effect:** the copy must carry `ModelsInvalid`/`GroupsInvalid` **verbatim** — a `denyAll` scope resolved from a cache hit must be indistinguishable from one resolved off a fresh DB read. `TestCachedTokenStore_ForwardsGroupsAndDropsOnUpdate` (T1.11) is extended with the negative-flag assertion.
     - **`nil` vs `[]` must survive the clone.** `append([]string(nil), []string{}...)` returns `nil`, which would silently convert a PRESENT-empty axis into ABSENT — the escalation trap in miniature, inside the cache. Use `make([]string, len(x)); copy(...)` and **test it** (`TestCachedTokenStore_ClonePreservesPresentEmpty`), or `slices.Clone` and then explicitly restore empty-vs-nil. This is the single most dangerous line in the task; the pin is mandatory.
  3. **PD-7 (b) — state the bound, and state what it costs.** `idToHash` is FIFO-capped at **50 000** entries (`pkg/modelscache/models.go:25` `defaultIDIndexCap = 50000`; eviction at `tokens.go:103-107`). Consequence chain, to be written as a comment on `rememberID`: a token whose `id→hash` entry was evicted can no longer be cache-dropped by `UpdateTokenPermission` (the lookup at `:301` misses) ⇒ **the old grants stay cached for ≤ `TokensPositiveTTL` (60 s default, `models.go:19`)**, or **≤ `defaultStaleCap` (24 h, `models.go:21`) as a stale-positive during a DB infra outage** (the stale tier is served exactly when the inner store errors). 50 000 tokens is far beyond this deployment's token count, so the bound is a documented ceiling, not a live defect — **do not "fix" it by widening the cap**; widening moves the memory bound, not the race. If a strict bound is ever wanted, the correct fix is a `DeleteToken`-style `id`-keyed permission generation counter, which is out of scope here.
- **Done when:** `pkg/modelscache` compiles; the decorator-through test (T1.8) proves the drop is effective, not merely called; `TestCachedTokenStore_ForwardsGroupsAndDropsOnUpdate` + `TestCachedTokenStore_ClonePreservesPresentEmpty` + the invalid-flag assertion all show a literal `--- PASS`; `go test -race ./pkg/modelscache/...` green.

### T1.5 — `pkg/authz` resolver + predicate; `models.ReachFilter`; `GroupRewriteResult.ScopeExcluded`
- **Files:** `pkg/authz/scope.go` (new), `pkg/models/reach.go` (new), `pkg/models/group_rewrite.go` (additive field).
- **Anchors:** `GroupRewriteResult` `pkg/models/group_rewrite.go:24-68` (field list; add `ScopeExcluded` beside `FailedGroupID` — **the field is declared at `pkg/models/group_rewrite.go:67`** (`FailedGroupID string`); *the brief's `:56-58` is the `BindingStale` comment block*); `models.GroupRewriter` **`:82-122`** — **must remain byte-unchanged**. The **setters** of `FailedGroupID` live in a different file — `pkg/store/database/store_group_rewrite.go:85/:91/:96` — and are the ones the fallback-position trap analysis cites.
- **Do:** implement `Resolve` exactly per §plan 2.1/2.3:
  - `t == nil` ⇒ `nil` (unrestricted);
  - `t.ModelsInvalid || t.GroupsInvalid` ⇒ deny-all;
  - **both axes ABSENT** ⇒ `nil` (conjunctive wildcard);
  - otherwise: `ids = set(t.AllowedModels) ∪ set(t.AllowedGroups) ∪ ⋃_{g ∈ AllowedGroups} membersOf(g)`.
  `membersOf` is only invoked for ids in `AllowedGroups`. `Allows` on a nil receiver ⇒ `true`; on deny-all ⇒ `false`; on restricted ⇒ map lookup, and `Allows("")` ⇒ `false`.
- **Hard requirements:** `pkg/authz` imports **only** `pkg/auth` (plus stdlib). **No** import of `pkg/proxy`/`pkg/decision`/`pkg/store/database`/`pkg/modelscache` — proven by an import-guard test (T1.9). `pkg/models/reach.go` imports nothing.
- **Done when:** the truth table (T1.9) passes, including the **conjunctive proof** (models-absent + groups-present ⇒ restricted, models axis grants nothing) and the **R-3 anti-escalation proof**.

### T1.6 — Re-sign the 9 mocks + add the column to the 8 DDL fixtures
- **Files/anchors:** §2.2 table above. Compiler-enumerated: run `go build ./...` and fix each reported site.
- **Do:** mechanical. **No behaviour change**; where a mock stores `token.AllowedModels`, it also stores `AllowedGroups`.
- **Done when:** `go build ./...` and `go vet ./...` are clean; `go test ./pkg/... -run XXX_NONEXISTENT` compiles every test package (a compile-only sweep).

### T1.7 — Migration `034`: schema + **data repartition** + registry append
- **Files:** the four `034_*` SQL files, `pkg/store/database/migrate.go`.
- **Anchors:** registry slice `pkg/store/database/migrate.go:24` (`var migrations = []migration{`), last entry `:82` (`{"033", …}`), close `:83`; loader `loadMigrationSQL` `:149` (sprintf `:164-165`, no `.sql` in the registry name); runner `RunMigrations` `:86`, `runMigration` `:102` (`isMigrationApplied` `:103`, skip `:107-109`, `ExecContext` `:118`, best-effort ROLLBACK `:136`, `recordMigration` `:141`); boot `pkg/store/database/init.go:19`.
- **Do:** §plan 5.2 / §5.3 bodies **verbatim**, **as amended by PD-2 and PD-4 below**. Both dialects. `BEGIN/COMMIT` wrapper (028/032 precedent). `json_valid`/regex guard so a malformed row cannot abort boot. Append the sixth trap comment.
- **Hard requirements:**
  - (a) the repartition **must** be idempotent — proven by T1.10 running the UPDATE twice;
  - (b) an emptied `allowed_models` becomes **NULL**, never `'[]'` — **on BOTH dialects** (see PD-4);
  - (c) the `WHERE ... EXISTS(group id)` clause guarantees wildcard rows are untouched;
  - (d) **PD-2: a malformed row must never abort boot** (see below) — on PG this is now enforced structurally, not by a regex hope.
- **PD-2 (RED) — the PostgreSQL guard: version-agnostic, row-by-row, SKIP not ABORT.** *Why the original shape fails:* the regex `t.allowed_models ~ '^\s*\['` **filters which rows are visited**; it does **not** make the cast safe inside the visited rows. A row whose value is `[broken` satisfies the regex and then raises `ERROR: invalid input syntax for type json` on `t.allowed_models::jsonb` (empirically proven on `ensemble_dev`). Migrations run **in-process at boot** (`pkg/store/database/init.go:19` → `Store.RunMigrations`), so that error **aborts the process**, and a CrashLooping pod re-runs the same failing statement forever — a boot-outage loop on a live host. The architect asked whether to use `pg_input_is_valid` (PG 16+); **the LEADER RULED FOR THE VERSION-AGNOSTIC `DO $$ … EXCEPTION` FORM** — no assumption about the hosts' PG major version.
  - Exact required shape (per-row guard, `BEGIN`/`COMMIT` outside, one `BEGIN…EXCEPTION…END` **inside** the loop so one bad row cannot abort the batch):
    ```sql
    DO $$
    DECLARE r RECORD;
    BEGIN
      FOR r IN SELECT id FROM auth_tokens
                WHERE allowed_models IS NOT NULL
                  AND btrim(allowed_models) <> ''
                  AND allowed_models ~ '^\s*\[' LOOP
        BEGIN                                   -- inner block: guard is PER ROW
          UPDATE auth_tokens t
             SET allowed_groups = (SELECT json_agg(v) FROM jsonb_array_elements_text(t.allowed_models::jsonb) v
                                    WHERE v IN (SELECT id FROM models WHERE kind = 'group')),
                 allowed_models = (SELECT json_agg(v) FROM jsonb_array_elements_text(t.allowed_models::jsonb) v
                                    WHERE v NOT IN (SELECT id FROM models WHERE kind = 'group'))
           WHERE t.id = r.id
             AND t.allowed_models::jsonb <> '[]'::jsonb
             AND EXISTS (SELECT 1 FROM jsonb_array_elements_text(t.allowed_models::jsonb) v
                          WHERE v IN (SELECT id FROM models WHERE kind = 'group'));
        EXCEPTION WHEN invalid_text_representation THEN
          RAISE WARNING '034: skipping malformed allowed_models on token % (left untouched; the scanner denies it)', r.id;
        END;
      END LOOP;
    END $$;
    ```
    (This matches `plan.md` §5.2 as revised; the phase file states the requirement so an implementer cannot "simplify" the inner block away. `RAISE WARNING` — not `NOTICE` — so the skip is visible in pod stdout at default log level.)
  - **The skip must SKIP the ROW, not the migration.** Accept criteria: with a `[broken` row seeded, `RunMigrations` **returns nil**, the malformed row is **byte-identical afterwards**, every well-formed group-naming row is repartitioned, and `schema_migrations` gains `034`. Pinned by `TestMigration034_PGMalformedRowSkippedNotAbort` (T1.10) — **LANDED (`1e65774`)**; the landed structural guards are `TestMigration034_PGGuardShape` + `TestMigration034_PGuardShape_DownTwinAndGlobalScan` — sqlite-dialect harness can only approximate the `DO $$` block, so the pin is a **source-level structural assertion plus a dialect-independent behaviour test** (see T1.10's note).
  - **G0 (Phase 3 T3.10) gets the same protection** — queries 1 and 2 must be `DO $$ … EXCEPTION`-guarded too, or the pre-ship inspection itself crashes on the very row it exists to find. Cross-referenced from T3.10; this phase owns the SQL text.
- **PD-4 (AMBER) — the SQLite twin: `NULLIF(…, '[]')`.** *Why:* on `modernc.org/sqlite v1.46.1`, `SELECT json_group_array(j.value)` over **zero** rows returns the string `'[]'`, whereas PG's `json_agg` over zero rows returns **`NULL`** (empirically proven). `plan.md`'s original claim that SQLite returns `NULL` was wrong. Under §2.3, `'[]'` is **PRESENT-empty** (valid, grants nothing) while `NULL` is **ABSENT** — so the two dialects would produce *different permission states* for the same logical "this axis is now empty", and `TestMigration034_RepartitionIdempotent` / P-G17 (which expect `allowed_models IS NULL`) would fail on SQLite.
  - Required shape for **both** `SET` arms of the SQLite UPDATE:
    ```sql
    SET allowed_groups = NULLIF((SELECT json_group_array(j.value) FROM json_each(auth_tokens.allowed_models) j
                                  WHERE j.value IN     (SELECT id FROM models WHERE kind = 'group')), '[]'),
        allowed_models = NULLIF((SELECT json_group_array(j.value) FROM json_each(auth_tokens.allowed_models) j
                                  WHERE j.value NOT IN (SELECT id FROM models WHERE kind = 'group')), '[]')
    ```
  - **Both arms**, not just `allowed_models`: a token whose `allowed_models` names **only** group ids gets `allowed_groups = '["g1"]'` and `allowed_models = NULL` — the first arm non-empty, the second emptied. The mirror case (no group ids) never enters the UPDATE at all (the `EXISTS` clause). So in practice only the second arm empties — but wrap **both** so a future edit cannot reintroduce the skew.
  - **Keep the SQLite JSON1 probe** (`TestMigration034_SQLiteJSON1Available`, T1.10) — `NULLIF` changes nothing about JSON1 availability, and the probe is what makes the fallback decision (§plan 5.5) evidence-based.
- **Done when:** `TestMigration034_*` (T1.10) green on a fresh DB and on a seeded DB, with the null-vs-`'[]'` parity assertion and the malformed-row-skip assertion both showing literal `--- PASS`.

### T1.8 — Admin API contract
- **Files:** `pkg/ui/server.go`.
- **Anchors:** routes `:227` / `:228`; handlers `handleTokens` `:2514` (dispatch `:2519`/`:2521`), `handleTokenDetail` `:2528` (id trim `:2530`, PATCH dispatch `:2554-2555`); structs `TokenResponse` `:2491-2498`, `CreateTokenRequest` `:2501-2505`, `CreateTokenResponse` `:2508-2511`, `UpdateTokenPermissionRequest` `:2563-2565`; `updateTokenPermission` `:2568`, PATCH 64 KB cap `:2570`, merge `:2589-2597`, response `:2609-2610`; `listTokens` `:2613-2644` (projection `:2629-2639`); `createToken` `:2646-2712` (name 400 `:2658-2661`, expiry 400 `:2665-2672`, `X-User` `:2674-2678`, allowed-models copy `:2680-2686`, response `:2692-2709`, 201 `:2708`).
- **Do:**
  1. Add `AllowedGroups` to the three structs (§plan 2.8).
  2. `createToken`: decode both axes, **validate** (T1.8b), pass `auth.OptionalTokenPermission{…}` (both axes `Set:true`), echo both. **POST body cap: REJECTED (fix-pass 2 scope ruling)** — the cycle-1/Q6 plan to add a POST-side `http.MaxBytesReader` is removed; only the PATCH-side cap (landed at `:2570`) remains fact.
  3. `updateTokenPermission`: extend the merge — per axis: `nil` ⇒ **leave the Go value `nil` and let the store omit the column** (PD-3); non-nil ⇒ the posted value (incl. empty). **Validate only the non-nil axes** (inbound-only rule ⇒ no wedge). **Do NOT** copy `currentToken.<axis>` into the payload: that is precisely the behaviour PD-3 removes — copying `currentToken.AllowedModels` when `ModelsInvalid == true` yields `nil`, which the old store turned into `SET … = NULL`, healing a DENY-ALL row. Concretely:
     ```go
     perm := auth.OptionalTokenPermission{}         // both axes Set:false = "change nothing"
     if req.AllowedModels != nil {
         v := *req.AllowedModels
         perm.AllowedModels = auth.OptionalAllowlist{Set: true, Values: &v}  // [] -> '[]'; ids -> array
     }   // key omitted OR explicit null -> req field nil -> stays Set:false -> column untouched (Ruling 2)
     if req.AllowedGroups != nil {
         v := *req.AllowedGroups
         perm.AllowedGroups = auth.OptionalAllowlist{Set: true, Values: &v}
     }
     if msg := s.validateTokenGrants(ctx, perm); msg != "" { /* 400 {"error": msg} */ return }
     if err := s.tokenStore.UpdateTokenPermission(ctx, id, perm); err != nil { /* 404 / 500 as today */ }
     ```
     `currentToken` is then fetched only for the **404** pre-check (keep the existing `GetTokenByID` call at `:2579` and its error mapping — dropping it would change the 404-vs-500 contract).
  4. `listTokens`: project `allowed_groups` (nullable `*[]string` in `TokenResponse`, same shape as `allowed_models` at `:2634`).
- **T1.8b — validation helper** (§plan 6.3) — **PD-8, unambiguous**:
  1. **Malformed / non-array** values ⇒ `400`. In Go terms the decode is already typed (`*[]string`), so a JSON `{"allowed_groups": "coding-group"}` fails `json.Decode` ⇒ the existing generic "Invalid JSON body" 400 at `:2654`/`:2574` covers it; **state that in the test** (`TestTokenAPI_Validation_MalformedShape`) so nobody adds a second, divergent shape-checker.
  2. `allowed_groups` ids must **exist AND be `kind='group'`** ⇒ `400` naming the offending id. Both halves are required: a known **chat** model id in `allowed_groups` is a **400**, not a silently-ineffective grant (the group axis must never hold a non-group id, or `membersOf` returns `nil` and the token silently grants nothing but the id itself).
  3. `allowed_models` keeps the **Q4 rule**: any id of **any** kind (chat / decision / image-gen) is accepted, and a **group id is accepted on write** with target-only semantics (`D4`) — the resolver adds its members, the row stays a group id. **Do not** reject group ids on this axis: the FE picker is unfiltered (Phase 3) and the repartition's pre-state is exactly that shape.
  4. Lookup prefers the **claim-blind** `GetModelNative` via a structural assertion (`Server.modelsConfig` is `models.ModelsConfigInterface` at `pkg/ui/server.go:129`, which does **not** declare `GetModelNative` — assert to the `directModelGetter` shape, precedent `pkg/proxy/handler_groups.go:555-589`), falling back to `GetModel`. **Why claim-blind:** a validation of `allowed_models` run through claims-first `GetModel` would resolve a claimed id to the *group* row and then a `kind` test would report the wrong kind; the write path must validate **row identity**, same rule as the rewrite path (`§3.4 Q1 tail`).
  5. **Inbound-only**: the helper receives `perm` with `nil` for omitted axes and **must skip a nil axis** (never look up `""`). Never validate the merged result (R1.9's wedge).
- **Done when:** `TestTokenAPI_*` (T1.11) green: POST echo, PATCH nil/`[]`/ids, both 400s (unknown id, non-group id), the group-id-in-`allowed_models` acceptance case, and the PD-3 no-heal case. (The POST >64 KB case is REMOVED — the POST body cap was rejected fix-pass 2; `TestTokenAPI_PostBodyCap` is deleted.)

> **Route inventory (verified at `a3f0335`; stated so no implementer hunts for a missing route).** `pkg/ui/server.go:227` registers `/fe/api/tokens` → `handleTokens` (`:2514`), which dispatches **GET** (`:2519` list) and **POST** (`:2521` create) and 405s everything else (`:2522-2524`). `:228` registers `/fe/api/tokens/` → `handleTokenDetail` (`:2528`), which dispatches **DELETE** (`:2542`) and **PATCH** (`:2554-2555`) and 405s everything else (`:2557-2559`). **There is NO `PUT` and no per-id `GET`** — so "POST/PUT/PATCH validation" in the ruling text means **POST + PATCH**, which is exactly what T1.8/T1.8b cover. **Any change that adds a `PUT` route is out of scope** for this commission.

### T1.9 — `authz` + `models` unit tests
| Test | File | Asserts |
|---|---|---|
| `TestAuthzResolve_TruthTable` | `pkg/authz/scope_test.go` (new) | nil token ⇒ unrestricted; both axes absent ⇒ `nil`; one axis present-empty ⇒ **restricted, nothing granted**; ids ⇒ granted; group ⇒ group id + members (`membersOf` stub); **either Invalid ⇒ deny-all** |
| `TestAuthzResolve_ConjunctiveWildcard` | same | **models ABSENT + groups PRESENT ⇒ NOT wildcard**; a model id is denied; the granted group + its members allowed — the **R-3** anti-escalation proof |
| `TestAuthzResolve_NoMembersExpansionForAbsentGroups` | same | `membersOf` is **never** called when `AllowedGroups` is empty (channel/counter assertion) — the hot-path guarantee |
| `TestAuthzScope_AllowsEmptyID` | same | restricted scope denies `""` |
| `TestPackage_ImportGuard_AuthzStaysLeaf` | `pkg/authz/guard_test.go` (new) | `pkg/authz` source imports no `pkg/proxy`, `pkg/decision`, `pkg/store/database`, `pkg/modelscache` (house `TestPackage_ImportGuard_*` pattern; a negative control must FIRE) |
| `TestReachFilter_InterfaceShape` | `pkg/models/reach_test.go` (new) | `*authz.Scope` satisfies `models.ReachFilter` at compile time — **placed in `pkg/models`+`pkg/authz` cross-package test** to avoid an import cycle (`pkg/models` must not import `pkg/authz`; put the assertion in `pkg/authz` instead: `var _ models.ReachFilter = (*Scope)(nil)`) |

### T1.10 — Migration tests (`pkg/store/database/migrations_test.go`)
| Test | Asserts |
|---|---|
| `TestMigration034_ForwardFreshDB` | copy the `TestMigration031_ForwardFreshDB` `:728` shape (`columnExists` helper `:586`, `PRAGMA table_info(...)` at `:590`): column exists; `schema_migrations` row `034` present |
| `TestMigration034_SQLiteJSON1Available` | `json_group_array` / `json_each` / `json_valid` work on `modernc.org/sqlite v1.46.1` — **the OI-1 probe as a permanent CI fact** (unchanged by PD-4; the probe is what makes the §plan 5.5 fallback fork evidence-based) |
| `TestMigration034_Repartition` | seed: (1) wildcard row (`NULL`), (2) `["g1"]` where `g1` is `kind='group'`, (3) `["m1","g1"]` (m1 a chat model), (4) `'[]'`, (5) malformed `'not-json'`, **(6) malformed-with-bracket `'[broken'` (PD-2's exact shape)** ⇒ after the UPDATE: (1) untouched, (2) `allowed_models IS NULL` + `allowed_groups={"g1"}`, (3) `allowed_models={"m1"}` + `allowed_groups={"g1"}`, (4) untouched, (5) untouched (guard), **(6) untouched AND the migration returned nil (no abort)** |
| `TestMigration034_RepartitionIdempotent` | run the repartition UPDATE a **second** time ⇒ `RowsAffected() == 0` and every row byte-identical |
| `TestMigration034_NullNotEmptyArray` | **PD-4 pin.** An emptied `allowed_models` is `NULL`, **never** `'[]'` — on SQLite because of `NULLIF(…, '[]')`; assert with `IS NULL` (not `= ''`, not `= '[]'`). A second arm asserts the *dialect-parity contract* in prose: PG's `json_agg` over zero rows is `NULL` by definition, so the two SET expressions must agree; the PG half is asserted structurally (next row) |
| `TestMigration034_PGGuardShape` | **PD-2 pin (structural, source-level).** Read `pkg/store/database/migrations/postgres/034_add_allowed_groups.up.sql` and assert: (a) it contains a `DO $$` block; (b) the block has an **inner** `BEGIN … EXCEPTION WHEN invalid_text_representation THEN … END` **inside** the `FOR … LOOP` (i.e. the `EXCEPTION` token appears **after** the `LOOP` token in the file); (c) it contains **no** `pg_input_is_valid` (version-agnostic requirement — a future "improvement" to a PG-16-only function would break the hosts' compatibility and must fail this pin); (d) `RAISE WARNING` (visible at default log level) appears, so the skip is observable. *Why structural:* the SQLite test harness cannot execute plpgsql; a behavioural PG test would need a live PG in the unit pack. The pin still catches the real regression class — a later edit that moves the `EXCEPTION` outside the loop, deletes it, or swaps in `pg_input_is_valid`. **Behavioural coverage for the skip** comes from `TestMigration034_Repartition` case (6) on SQLite (`json_valid` arm) plus the G0 dry-run in Phase 3 T3.10 against both live hosts. |
| `TestMigration034_SQLiteDialectParity` | **PD-4 pin (behavioural).** Seed a token whose `allowed_models` names **only** group ids; after the repartition assert `allowed_models IS NULL` **and** `allowed_groups` is the non-empty JSON array — i.e. the emptied axis is ABSENT while the populated axis is PRESENT. This is the shape the PG twin must produce too (and the shape `ScanGrant` must classify as `present=true` on one axis, `present=false` on the other). |

### T1.11 — Store + API tests
| Test | File | Asserts |
|---|---|---|
| `TestCreateToken_GrantAxesRoundTrip` | `pkg/auth/store_test.go` | `Perm{Models:nil, Groups:["g1"]}` ⇒ DB `NULL` / `'["g1"]'`; read-back `AllowedModels == nil`, `AllowedGroups == ["g1"]` |
| `TestCreateToken_ExplicitEmptyIsNotNull` | same | `Perm{Models:[]string{}, Groups:nil}` ⇒ DB `'[]'` (not `NULL`); read-back non-nil-empty |
| `TestValidateToken_SurfacesInvalidFlags` | same | a row with `allowed_models='not-json'` ⇒ `ModelsInvalid == true`, `AllowedModels == nil` |
| `TestUpdateTokenPermission_BothAxes` | same | update one axis, the other unchanged (the store writes only the `Set` axes — the merged-value read-merge-write is **retired** by CG-1) |
| `TestUpdateTokenPermission_RoundTrip` | same | **fix-pass 2 two-state pin (Ruling 2).** omitted ⇒ column byte-identical (incl. a seeded malformed row); **explicit `null` ⇒ ALSO byte-identical (keep — the cycle-1 reset semantics are rejected)**; `[]` ⇒ `'[]'`; ids ⇒ JSON array — all reachable update states round-trip through `OptionalAllowlist` |
| `TestScanGrant_TriState` | `pkg/auth/token_test.go` (replaces/extends `TestScanAllowedModels` `:104` table `:169-183`) | `nil` ⇒ absent · **`""`/whitespace ⇒ INVALID (fix pass C E2 — NOT absent; only SQL `NULL` is ABSENT)** · `"[]"` ⇒ present-empty · `'["a"]'` ⇒ present · `"not-json"` ⇒ **invalid** · **`123` (unknown raw type) ⇒ invalid** (the row at `:120-124` must change expectation) |
| `TestTokenAPI_CreateEchoesGroups` | `pkg/ui/handlers_token_test.go` | POST with `allowed_groups` ⇒ 201, echo both axes, plaintext token once |
| `TestTokenAPI_PatchMergeSemantics` | same | `nil` ⇒ keep; `[]` ⇒ clear to explicit-empty; ids ⇒ replace — asserted against the mock's stored `auth.OptionalTokenPermission` (the ONE write payload) |
| `TestTokenAPI_PatchClearDoesNotEscalate` | same | a token with models ABSENT + groups `["g1"]`, PATCH `{"allowed_groups":[]}` ⇒ stored `AllowedGroups` non-nil-empty (never nil) ⇒ **not a wildcard** (the Q13/R-6 trap, asserted at the API boundary) |
| `TestTokenAPI_Validation` | same | group id that does not exist ⇒ 400; group id that is a chat model ⇒ 400; unknown model id ⇒ 400; **a group id in `allowed_models` ⇒ ACCEPTED (Q4/target-only semantics, PD-8 (c))**; **a stale id on the OTHER axis (omitted) does not 400** (inbound-only rule) |
| `TestTokenAPI_Validation_MalformedShape` | same | **PD-8 (a):** `{"allowed_groups": "coding-group"}` (string, not array) ⇒ 400, body not applied; `{"allowed_models": 5}` ⇒ 400. Asserts the *existing* typed-decode 400 is the mechanism (no second shape-checker was added) |
| `TestTokenAPI_PatchNilAxis_DoesNotHealDenyAll` | same | **PD-3 head-line pin.** Mock `GetTokenByID` returns a token with `AllowedModels == nil, ModelsInvalid == true` (the DENY-ALL shape) and `AllowedGroups == ["g1"]`. `PATCH {"allowed_groups":["g2"]}` ⇒ the mock's `UpdateTokenPermission` receives `perm.AllowedModels == nil` and the store layer **omits the column** ⇒ assert (a) the received payload's models axis is nil, (b) the store-level assertion that the generated SQL contains **no** `allowed_models =` fragment, (c) the persisted raw `allowed_models` bytes are **unchanged** (still the malformed original), (d) the response is 200 (the patch itself succeeded) |
| `TestUpdateTokenPermission_OmitsNilColumn` | `pkg/auth/store_test.go` | **PD-3 store-level pin.** (1) omitted axis ⇒ the other column's raw bytes are byte-identical after the call; (2) `perm` with **both** axes nil ⇒ no `UPDATE` executed at all and `err == nil` (the no-op arm must not report `ErrTokenNotFound`); (3) a malformed `allowed_models` row + a groups-only perm ⇒ the malformed column is still malformed (never healed) |


| `TestTokenAPI_PatchNonexistent_404` | same | **PD-3 companion pin.** `PATCH` on an unknown id ⇒ **404** — proves the 404 contract comes from the API's `GetTokenByID` pre-check (`pkg/ui/server.go:2579-2587`), not from the store's `RowsAffected`, so the PD-3 no-op arm cannot silently turn a missing token into a success |
| `TestCachedTokenStore_ForwardsGroupsAndDropsOnUpdate` | `pkg/modelscache/tokens_test.go` | validate ⇒ cache positive; `UpdateTokenPermission(perm with new groups)` ⇒ forwards **and** the next validate re-reads the new grants (the drop is effective, not merely called) |
| `TestCachedTokenStore_ClonePreservesPresentEmpty` | same | **PD-7 pin (a).** Cache a token with `AllowedGroups = []string{}` (PRESENT-empty) ⇒ the value returned by the next `ValidateToken` is **non-nil and empty**, never `nil`. Also asserts `AllowedModels`/`AllowedGroups` do **not** share a backing array with the cached entry (mutate the returned slice, re-validate, the cached value is unchanged) |
| `TestCachedTokenStore_CloneCarriesInvalidFlags` | same | **PD-7 pin (b).** A token with `ModelsInvalid == true` cached and then read back keeps `ModelsInvalid == true` and `AllowedModels == nil` — a cache hit must be indistinguishable from a DB read for the deny-all state |

---

## 4. Dependencies

| Depends on | Why |
|---|---|
| nothing (this is the base phase) | — |
| **`go1.26` toolchain** at `$HOME/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.0.linux-amd64/bin/go` | house build |
| **HOME quarantined** for every `go test` run | `go test` without it rewrites `~/.config/llm-supervisor-proxy/config.json` (`APPLY_ENV_OVERRIDES` set by `pkg/proxy` tests; `PORT=9797` leak). Phase 1 does not run `pkg/proxy` tests, but the rule is uniform. |

Blocks: **Phase 2** (needs `authz`, `models.ReachFilter`, `ScanGrant`, `auth.OptionalTokenPermission`/`OptionalAllowlist`, `GroupRewriteResult.ScopeExcluded`) and **Phase 3** (needs the frozen wire contract §plan 2.8).

---

## 5. Tests owned by this phase

**Ledger (gen-10 re-pin, reconciled):** Phase 1 lands **42 Go pins** (T1.9 6 + T1.10 8 + T1.11 8 + `handlers_token_flags_test.go` 2 C2/W1 + the authz guard/scope/modelscache names counted to Phase 1's contract) — the ORIGINAL pre-landing "new/changed" pin-count figure predates the landed set. **The single `added == expected` source of truth is `plan.md` §13.1a (88 Go + 33 FE = 121, ALL LANDED).** Each landed name must show a literal `--- PASS` exactly once; `TestScanGrant_TriState` replaces `TestScanAllowedModels` (declared as a rename, not unlisted).

**Revision-pass additions to the ledger (Phase 3 T3.7 must list these by name):**

| Test | Ruling | Why it exists |
|---|---|---|
| `TestMigration034_PGGuardShape` | PD-2 | structural guard-shape pin (the only way to test plpgsql without a live PG in the unit pack) |
| `TestMigration034_SQLiteDialectParity` | PD-4 | NULL-vs-`'[]'` parity, behaviourally |
| `TestMigration034_Repartition` (case 6 `[broken`) | PD-2 | the skip-not-abort behaviour on the SQLite twin |
| `TestUpdateTokenPermission_OmitsNilColumn` | PD-3 | the store-level column-skip contract |
| `TestTokenAPI_PatchNilAxis_DoesNotHealDenyAll` | PD-3 | the API-level no-heal contract |
| `TestTokenAPI_PatchNonexistent_404` | PD-3 | the 404 contract stays at the API boundary, not on `RowsAffected` |
| `TestTokenAPI_Validation_MalformedShape` | PD-8 | non-array ⇒ 400 |
| `TestCachedTokenStore_ClonePreservesPresentEmpty` | PD-7 | PRESENT-empty must not become ABSENT through the cache |
| `TestCachedTokenStore_CloneCarriesInvalidFlags` | PD-7 | deny-all must survive a cache hit |

---

## 6. Coupling

- **Tight with:** **Phase 2** — the frozen contract this phase *is*: `authz.Scope` / `Resolve` / `Allows` (the one resolver + one predicate), `models.ReachFilter` (the cycle-breaker both sides import), `models.GroupRewriteResult.ScopeExcluded`, `auth.ScanGrant` + `AuthToken.AllowedGroups` + the two `Invalid` flags, the `auth.OptionalTokenPermission` write shape (the `TokenStoreInterface` signature Phase 2's mocks inherit), and **migration 034's schema + repartition** (Phase 2's `pkg/store/database` tests run the real migrations, so the column must exist in the runner).
- **Loose with:** **Phase 3** — the token-permission *domain* (grants, scopes, the admin surface). Phase 1 owns the wire contract; Phase 3 owns the widget that expresses it. Shared domain, no shared code.
- **Independent of:** **none** — this is the base phase of the plan; no upstream phase exists, so there is nothing it shares with.

## 7. Risks

Phase-specific risks — each lands on **this phase's** files or tasks. The global register lives in `plan.md` §15 / `decisions.md` §3; the id is cross-referenced where this phase carries the concrete instance of it.

| # | Risk | Impact | Likelihood | Mitigation (concrete — task / file / pin) |
|---|---|---|---|---|
| R1.1 | **Migration `034` ships inert.** The registry is an ordered slice, not a directory scanner (`pkg/store/database/migrate.go:24-83`): a SQL file without a registry append is silently inert, and **nothing in the build fails** — the failure surfaces at the first `allowed_groups` SELECT. This has already happened **five** times (029–033). *(global: `decisions.md` R-4)* | High | Medium | **T1.7** appends `{"034", "034_add_allowed_groups.up"},` immediately after `:82` with its own sixth trap comment; **`TestMigration034_ForwardFreshDB`** asserts both the column and the `schema_migrations` row on a fresh DB; Phase 3's **T3.8** pre-declares `migrate.go` + the 034 files as authorized boundary changes so the gate does not read "inert" as "unchanged". |
| R1.2 | **Concurrent edits to the 11 interface implementers.** `auth.TokenStoreInterface` changes (T1.3) break the store, the decorator and **9 mocks**; if two implementers are edited in parallel and merged independently, the tree is broken mid-flight and the build failure lands on whoever merges second (the house "parallel edits to one file" race). *(global: `decisions.md` R-5's blast-radius half)* | High | Medium | T1.3 → T1.6 land as **ONE atomic commit** (the interface, the store, the decorator and all 9 mocks together) — never split, so the compiler enumerates the sites in a single pass; the phase exit criterion "**`go build ./...` green proves the 9 mocks + the decorator moved**" is the check, and `go build ./...` is run after each implementer edit, not only at the end. |
| R1.3 | **The permission path goes vacuous.** If the group axis were added as an *optional* seam instead of an interface change, the 9 mocks would silently not implement it ⇒ the UI's group-permission path would only ever be exercised against the real store ⇒ tests pass while the path is a no-op, and the anti-vacuous gate cannot catch it. *(global: `decisions.md` R-5)* | High | Low | **T1.3** extends the two existing methods with the `auth.OptionalTokenPermission` struct (Q5 (a) as superseded by the fix-pass-2 A4 one-name rule) ⇒ a missed implementer is a **compile error, not a silent gap**; **`TestCachedTokenStore_ForwardsGroupsAndDropsOnUpdate`** (T1.11) proves the decorator's forward and its cache-drop are *effective*, not merely called. |
| R1.4 | **"Clear the groups list" escalates the token to wildcard.** The store used to serialize with `len(x) > 0` (`pkg/auth/store.go:57` create, `:254` update), so nil **and** empty both became NULL ⇒ under the conjunctive wildcard rule, clearing one axis of a two-axis-restricted token wrote NULL on both ⇒ the token became **unrestricted**. *(global: `decisions.md` R-6 / Q13)* | High | Medium | **T1.3** changes the write payload to `auth.OptionalAllowlist` with the `x != nil`-equivalent `Set` rule (`Set:false`→column untouched, `[]`→`'[]'`, ids→JSON array) in the update path — clearing writes `'[]'` (PRESENT-empty, grants nothing), never NULL. **Wildcard promotion on EDIT is intentionally inexpressible** (leader Ruling 2, fix-pass 2): explicit `null` and omitted BOTH keep (the cycle-1 "reset-to-absent" third state was rejected and removed); an operator promotes a restricted token back to wildcard only by **re-creating the token** (release note + FE-9 note). **T1.11** asserts: `TestCreateToken_ExplicitEmptyIsNotNull` (DB value), `TestUpdateTokenPermission_RoundTrip` (omitted/null = byte-identical incl. a seeded malformed row; `[]` = `'[]'`; ids = array), and `TestTokenAPI_PatchNilAxis_DoesNotHealDenyAll` (the P-G26 canonical no-heal pin). |
| R1.5 | **One fail-open branch survives.** Closing only the malformed-JSON branch (`pkg/auth/token.go:62-66`) leaves the **unknown-raw-type** branch (`:52-55`) still returning `[]string{}` ⇒ still indistinguishable from wildcard ⇒ a corrupt row keeps **full access** while the log claims a deny. *(global: `decisions.md` R-2)* | High | Medium | **T1.2**'s `ScanGrant` returns `invalid=true` for **both** branches; **`TestScanGrant_TriState`** carries the `123` (unknown raw type) row — the existing table row at `pkg/auth/token_test.go:120-124` must change expectation, and the gate's set arithmetic (§Phase 3 T3.7) surfaces the rename as a *modified* test so it is not counted as unlisted. |
| R1.6 | **The fix itself becomes the escalation.** A *per-axis* wildcard rule ("models absent ⇒ model-level wildcard") would turn a repartitioned narrow token (`allowed_models` NULL + `allowed_groups` present) into a model-level wildcard — the fix would grant more access than the defect did. *(global: `decisions.md` R-3)* | High | Medium | The conjunctive rule is normative in `plan.md` §2.3 and is asserted by **`TestAuthzResolve_ConjunctiveWildcard`** (T1.9): models-absent + groups-present ⇒ RESTRICTED, the model axis grants nothing, the granted group and its members are reachable. |
| R1.7 | **SQLite JSON1 unavailable on the shipped driver.** The repartition needs `json_each` / `json_group_array` / `json_valid`; if the shipped `modernc.org/sqlite` build lacks JSON1, migration `034` aborts **at boot on every host**. *(global: `decisions.md` §5.2 OI-1)* | High | Low | **`TestMigration034_SQLiteJSON1Available`** (T1.10) makes JSON1 a **CI-enforced fact** rather than an assumption (the driver is `modernc.org/sqlite v1.46.1`, `go.mod:8`); the documented **fallback** (an idempotent Go-side backfill run right after `RunMigrations`, `plan.md` §5.5) is a real fork decided by the probe result, not by optimism. |
| R1.8 | **The 8th hand-rolled DDL fixture is missed.** `pkg/ui/handlers_usage_test.go:33-41` creates `auth_tokens` with **neither** grant column and survives today only because the usage-lane queries never select them; the first added permission test in `pkg/ui` would then fail with "no such column". *(the brief said 7; the verified count is 8 — `decisions.md` D-1)* | Medium | Medium | **T1.6**'s fixture table lists all **8** explicitly and marks #8 as "add BOTH columns"; the fixture edits land in the same atomic commit as the store's SELECT/INSERT column change, so the compile-and-run of `pkg/ui` in the phase exit criteria surfaces a missed fixture immediately. |
| R1.9 | **Write-time validation wedges the operator.** Validating the *merged* result would make a token whose model was deleted impossible to PATCH (the FE re-sends the whole list) ⇒ the operator is locked out of editing that token until they remove the stale id. *(global: `decisions.md` Q4 rationale)* | Medium | Medium | The **inbound-only** rule is normative (`plan.md` §6.3) and asserted by **`TestTokenAPI_Validation`**: a stale id on the *other* axis (omitted from the request) does **not** 400; only an id actively re-submitted is checked, and the 400 names the offending id. |
| **R1.10** | **The `034` PostgreSQL guard aborts boot on a `[`-prefixed malformed row.** *(**PD-2, RED**, empirically proven on `ensemble_dev`.)* The regex `allowed_models ~ '^\s*\['` selects rows to visit; it does **not** make `::jsonb` safe for a visited row. `[broken` satisfies the regex and raises `invalid input syntax for type json`. Migrations run **in-process** (`pkg/store/database/init.go:19`), so the error fails the boot and the pod CrashLoops, re-running the same failing statement — **a boot-outage loop on the live host serving the ensemble's LLM lane**. The G0 pre-ship queries suffer the identical crash (and therefore a blind spot: the inspection dies on the very row it exists to find). | **Critical** | Medium (one malformed row anywhere on either host is enough) | **T1.7** mandates the **version-agnostic** `DO $$ … BEGIN … EXCEPTION WHEN invalid_text_representation THEN … END $$` **inner per-row** block (leader ruling; **not** `pg_input_is_valid` — no PG-16 assumption). Pins: `TestMigration034_PGGuardShape` (structural: `EXCEPTION` appears after `LOOP`, no `pg_input_is_valid`, `RAISE WARNING` present) + `TestMigration034_Repartition` seed case (6) (`[broken` ⇒ row untouched, `RunMigrations` returns nil). G0 protection is mandated in **Phase 3 T3.10** using the same SQL text. |
| **R1.11** | **A `PATCH` silently heals a `DENY-ALL` token into a wildcard.** *(**PD-3, RED**.)* `ScanGrant` maps a malformed `allowed_models` to `AllowedModels == nil` + `ModelsInvalid == true`. The current merge (`pkg/ui/server.go:2592-2597`) copies `currentToken.AllowedModels` — i.e. `nil` — into the payload, and the current store (`pkg/auth/store.go:252-261`, `:254` `len(x) > 0`) turns `nil` into `SET allowed_models = NULL`. Under the conjunctive wildcard rule that is **an unrestricting write performed by an unrelated edit** (the operator changes the groups list and unknowingly grants every model). | **Critical** | Medium (a malformed row must exist, but the whole point of `ModelsInvalid` is that such rows do exist) | **T1.3** item 4 builds the `UPDATE` from the **non-nil axes only** (a nil axis is **omitted from the SQL**, value and column alike) plus the both-nil no-op arm; **T1.8** item 3 stops copying `currentToken` into the payload. Pins: `TestUpdateTokenPermission_OmitsNilColumn` (store: raw bytes byte-identical, both-nil ⇒ no UPDATE) and `TestTokenAPI_PatchNilAxis_DoesNotHealDenyAll` (API: the malformed column is still malformed, response 200). **Divergence noted:** the architect proposed a `400` on an omitted invalid axis (arch PD-5); the **leader ruled column-skip** — no `Invalid` knowledge in the store, no API policy in `pkg/auth`, and the repair path stays "supply a valid array explicitly". |
| **R1.12** | **SQLite and PostgreSQL disagree about what "emptied" means.** *(**PD-4, AMBER**, empirically proven on `modernc.org/sqlite v1.46.1`.)* `json_group_array` over **0** rows returns the string `'[]'`; PG's `json_agg` returns **`NULL`**. `'[]'` is PRESENT-empty (valid, grants nothing) while `NULL` is ABSENT — so the same logical outcome produces **two different permission states**, `TestMigration034_RepartitionIdempotent`/P-G17 fail on SQLite (`allowed_models IS NULL` expected), and a later dialect-specific "fix" would silently change one host's authorisation. | High | **High** (deterministic on every SQLite run) | **T1.7** wraps **both** SET arms in `NULLIF((SELECT json_group_array(…)), '[]')`; pins `TestMigration034_NullNotEmptyArray` + `TestMigration034_SQLiteDialectParity` (the emptied axis is `IS NULL`, the populated axis is a non-empty array). The JSON1 probe (`TestMigration034_SQLiteJSON1Available`) is retained untouched. |
| **R1.13** | **The token cache hands the *same* `*AuthToken` (and its slice backing arrays) to every concurrent request.** *(**PD-7, AMBER**.)* Verified at `a3f0335`: `storePositive` (`pkg/modelscache/tokens.go:117`) caches the caller's pointer; `cachedVerdict` (`:165`) returns `e.token` as-is; `CreateToken` (`:263-275`) caches the very pointer it returns to the API layer. With one slice today a stray `sort`/`append` is a latent race; with **two** slices plus two booleans, a shared array across goroutines becomes a concrete correctness hazard for the least-understood axis in the system (reach). | High | Low today, Medium after Phase 2 (more readers) | **T1.4** adds `cloneToken` at every read arm **and** at `storePositive` (fresh slices, flags copied verbatim, PRESENT-empty preserved via `make+copy`, never `append([]string(nil), …)`); pins `TestCachedTokenStore_ClonePreservesPresentEmpty` + `TestCachedTokenStore_CloneCarriesInvalidFlags`, and `go test -race ./pkg/modelscache/...` is an exit criterion. |
| **R1.14** | **A cache-drop miss leaves old group grants live.** *(**PD-7 (b)**, arch PD-14.)* `idToHash` is FIFO-capped at **50 000** entries (`pkg/modelscache/models.go:25`; eviction `tokens.go:103-107`). `UpdateTokenPermission` drops the cached entry by looking the hash up in that index (`tokens.go:297-304`); if the entry was evicted, the drop is a **silent miss** and the old grants stay cached for ≤ `TokensPositiveTTL` (**60 s**, `models.go:19`) — or ≤ `defaultStaleCap` (**24 h**, `models.go:21`) when the stale-positive tier is serving during a DB infrastructure outage. | Medium | Low (50 000 tokens ≫ this deployment; the stale tier additionally requires a DB outage) | **T1.4** item 3 writes the bound and its consequence as a comment on `rememberID` so the next reader knows the ceiling is **documented, not accidental**. **No code change** — widening the cap moves the memory bound, not the race, and a generation-counter fix is out of scope for this commission. Phase 3's exit criteria carry the 60 s/24 h statement into the release note so an operator is never surprised by a lagging group grant. |

---

## 8. Exit criteria

### 8.1 Must all hold

1. `go build ./...`, `go vet ./...`, `gofmt` zero-new-drift.
2. `go test ./pkg/auth/... ./pkg/authz/... ./pkg/models/... ./pkg/store/database/... ./pkg/modelscache/... ./pkg/ui/...` green (HOME-quarantined, `-timeout=110s`; `pkg/store/database` may need the release-gate slice timeout — use `-timeout=240s`).
3. **The contract is executable, not prose:** a reviewer can read `pkg/authz/scope.go` + `pkg/auth/permission.go` + `pkg/models/reach.go` and match every sentence of `plan.md` §2 to code.
4. **The escalation trap is provably closed:** `TestTokenAPI_PatchClearDoesNotEscalate` + `TestAuthzResolve_ConjunctiveWildcard` both green.
5. **Both fail-open branches closed:** `TestScanGrant_TriState` includes the unknown-raw-type row.
6. **`034` is not inert:** `TestMigration034_ForwardFreshDB` green (the registry append is what makes it so) and `TestMigration034_RepartitionIdempotent` proves re-run safety.
7. **`pkg/decision/policy.go` diff vs base == 0 bytes** (this phase does not touch it; the check is cheap insurance).
8. **`pkg/models/group_rewrite.go` change is additive only** — `git diff` shows exactly one new field + its comment; the `GroupRewriter` interface block is byte-identical.
9. **No production file in §2.3 was modified** (`git diff --name-only` check).
10. **`go build ./...` green proves the 9 mocks + the decorator moved** — a missed one is a compile error, by construction (Q5 (a)'s whole point).
11. **PD-2 closed:** `TestMigration034_PGGuardShape` green (the PG up file carries an **inner** `BEGIN … EXCEPTION WHEN invalid_text_representation` **inside** the `FOR … LOOP`, no `pg_input_is_valid`, `RAISE WARNING` present) **and** `TestMigration034_Repartition` seed case (6) proves a `[broken` row is left untouched while `RunMigrations` returns nil — i.e. **skip, not abort**, so the in-process boot migration (`pkg/store/database/init.go:19`) cannot crash-loop.
12. **PD-3 closed:** `TestUpdateTokenPermission_OmitsNilColumn` + `TestTokenAPI_PatchNilAxis_DoesNotHealDenyAll` both green; and a code-level check: `pkg/auth/store.go`'s `UpdateTokenPermission` contains **no** `allowed_models = ?` fragment that is unconditionally present (the statement is assembled from the non-nil axes).
13. **PD-4 closed:** `TestMigration034_NullNotEmptyArray` + `TestMigration034_SQLiteDialectParity` green — an emptied axis is `IS NULL` on SQLite, matching PG's `json_agg`-returns-NULL semantics; both SET arms carry `NULLIF(…, '[]')`.
14. **PD-7 closed:** `TestCachedTokenStore_ClonePreservesPresentEmpty` + `TestCachedTokenStore_CloneCarriesInvalidFlags` green **and** `go test -race ./pkg/modelscache/...` green (the clone is the race fix, not a cosmetic copy).
15. **PD-8 closed:** `TestTokenAPI_Validation` covers unknown-group ⇒ 400, non-group-in-`allowed_groups` ⇒ 400, group-id-in-`allowed_models` ⇒ accepted, stale-id-on-omitted-axis ⇒ no 400; `TestTokenAPI_Validation_MalformedShape` covers the non-array 400. The route inventory is documented (POST + PATCH only; **no PUT**).

### 8.2 Hand-off to Phase 2 (what Phase 2 may assume)

- `authz.Resolve(t, membersOf)` exists and is correct; `(*Scope).Allows` is the only predicate.
- `models.ReachFilter` exists and is implemented by `*authz.Scope` (compile-asserted).
- `models.GroupRewriteResult.ScopeExcluded` exists.
- `auth.ScanGrant` exists; `AuthToken` carries `AllowedGroups` + the two `Invalid` flags, **and the flags survive the token cache**.
- The DB has `allowed_groups`; **the live-shaped data is already repartitioned** (proven by T1.10).
- The wire contract is frozen; the FE (Phase 3) can be written against `plan.md` §2.8 without reading Go.
- **What Phase 1 deliberately did NOT do:** no lane calls the predicate; behaviour is unchanged.

---

## 9. Parallelism note (the reasoning behind §6 Coupling)

**INDEPENDENT as a unit, sequential internally.** It can start immediately and needs no other phase. Phase 2 and Phase 3 can both start **only after** T1.1–T1.6 are committed (the symbols must exist to compile against), but they do **not** need T1.7–T1.11 to *begin* their own work — the migration and the API/validation tests are additive to a working compile. **Recommended split point for maximum parallelism:** land T1.1–T1.6 as an atomic "contract" commit, then T1.7 (migration) and T1.8/T1.11 (API) proceed in parallel with the *start* of Phase 2 and Phase 3.
