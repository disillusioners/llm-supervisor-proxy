# Phase 1: Backend — Credential Groups (persistence, flattening seam, manager, API)

Date: 2026-11-16
Author: planner[v2] via plan-creation worker (backend lane)
Status: Draft
Base: `feature/credential-groups` @ `2b987bf` (= origin/latest) — worktree `llm-supervisor-proxy-wt-cred-groups`
Cross-references: `phase2-plan.md` (frontend), `phase3-plan.md` (gate+ship) — written in parallel; do not wait.
Research inputs: `research-seam.md`, `research-store-api.md` (this dir). All file:line citations below re-verified against this worktree.
Addenda folded: `architecture-recommendation.md` (Architect, commit `c92d719`) — binding addenda **A-1…A-15** + council amendments **M-1…M-5**. Where this file and that document differ, **the architecture recommendation wins**. Addendum → task/matrix-row mapping is at the end of this file (§k).

---

## Objective

Add **credential groups** — a named, same-provider bundle of existing credentials — as a **management-only** entity that models may reference alongside direct credential refs. At model-resolution the reference list is **flattened in place** (group expands at its position, dedup keep-first) so that credential **selection never knows groups exist**. Backend delivers: migration, types+validation, manager CRUD + reverse lookups + engine fan-out, the single flattening seam, decorator forwards, and the `/fe/api/credential-groups` API.

**Done =** a model whose `credentials_json` mixes direct refs and group refs resolves to exactly the same flat credential list as a manually-enumerated model; group membership edits propagate to every referencing model's engine state immediately; deletion-while-referenced is blocked (409); and a plain (group-free) model behaves byte-identically to today.

## Scope

### In Scope
- Migration `033_add_credential_groups` (sqlite + postgres + registry append + `.down`).
- `pkg/models`: `CredentialGroupConfig` type, sentinels, validation (group-level + model-ref-level), `CredentialRef.GroupID` extension.
- `pkg/store/database`: manager CRUD, in-memory group cache (`groupsMu`-disciplined), reverse lookups, engine fan-out, events, extended `RemoveCredential` guard, the flattening helper + its **9 store call sites** (A-1), the **A-2 proxy/UI raw-list gate flattening view**, resolver branch rework, shadow-column rules (A-7).
- `pkg/modelscache`: interface + `strictSource` additions, decorator forwards, **group cache build + boot priming + reconcile heal** (A-4), cache strategy, decorator-through tests.
- `pkg/models` JSON twin: **stub implementations of the 8 new interface methods** (A-5) — compile-blocker, scheduled early.
- `pkg/ui/server.go`: `/fe/api/credential-groups` + `/{id}` routes/handlers, error mapping (A-9), `used_by` on list+detail (A-10), `handleTestModel` group expansion (A-11).
- Structural pins (AST walk + import guard, unchanged under A-2) and the backend test matrix.

### Out of Scope
- **Frontend** (types, tab, form, editor, remove-flow UI) — `phase2-plan.md`.
- **Gate/ship** (Pack A/B run, gofmt drift, commit/promotion, deploy) — `phase3-plan.md`.
- **Selection-engine changes** (`pkg/credentiallb`, `pkg/stickyorder`, `pkg/skipcache`, `pkg/grouplb`) — **NOT touched**; groups are flattened away before they arrive. Evidence: `credentiallb.Engine.GetOrSelect` consumes only `st.credentials`/`st.bindings` (engine.go:225-348) fed by `OnModelChanged`/`RebindFromStore` (engine.go:366/:402); stickyorder keys by model ID; skipcache marks `(credential, provider, model)` post-resolution (race_executor.go:527-528, handler_anthropic.go:788).
- **`pkg/proxy` / `pkg/ui` selection code — NOT untouched, but NOT group-aware.** A-2: the raw-list gates listed in §d.2b **must consume a flattened view** (plain `[]models.CredentialRef`), not group objects. The packages are edited to read the flat view; they never see a `CredentialGroup*` type or value, so the §g AST pin is unchanged and still enforceable.
- **A 5th model kind** — `kind` stays 4-wide (`config.go:96-132`); a credential group is a new top-level entity, not a model row.
- **Group-in-group / nesting** — impossible by schema+API (members are credential IDs only).
- **JSON-file config parity** — credential groups are **DB-backed only**; the JSON twin rejects group refs (spec §b) and grows **stub implementations** of the 8 new interface methods (A-5) so the tree still compiles (`race_executor_shadow_reasoning_test.go:92` asserts `*models.ModelsConfig` satisfies `models.ModelsConfigInterface`; the ui tests' `Server.modelsConfig` is a **test-local mock** (`newMockModelsConfigForHandlers()`), NOT the twin — those mocks are covered by T-10c).

---

## Locked representation (design decision, with rationale)

**A group ref is a `models.CredentialRef` with a new `GroupID` field, living in the model's existing `credentials_json` array.**

```go
// pkg/models/config.go:331-335 (extended)
type CredentialRef struct {
    CredentialID string `json:"credential_id"`
    GroupID      string `json:"group_id,omitempty"` // NEW: mutually exclusive with CredentialID
    Weight       int    `json:"weight"`
    Position     int    `json:"position"`
}
```

A ref is **exactly one of** direct (`CredentialID != ""`, `GroupID == ""`) or group (`GroupID != ""`, `CredentialID == ""`).

**Why this shape:**
- The commission says a model's list "can add both credential and group" and grouping "flattens the list if mixing" → the mixed list must be ONE ordered array; a second `credential_groups` array on the model cannot express interleaving/position.
- Expansion-in-place at the ref's position falls out of a single ordered walk.
- **No new `ModelConfig` field** → the two deep-copy paths (`pkg/modelscache/copy.go:54` `deepCopyModelConfig`, `pkg/models/copy.go:102-130` `BuildModelCopy`) copy `Credentials` via `copyCredentialRefs`, and **both are a plain `copy(dst, src)` over the value struct** (`pkg/modelscache/copy.go:24-31`, `pkg/models/copy.go:174-182`) → `GroupID` survives with **zero** copy-code edits. **A-14: preserve this text — `research-seam.md` §e.7 ("must be added to both") is factually wrong and must NOT drive a representation change.** A regression test still pins it (§h T-14), and T-27b (new) updates the now-stale doc comments on both copy funcs ("a ref carries only credential_id/weight/position" — `pkg/models/copy.go:171-173`) so no future field-enumeration "fix" breaks `GroupID` survival.
- **No `models` column change** → migration 033 is a new table only.

`Position` is write-order metadata consumed only by the UI (`pkg/ui/server.go:753` rewrites `Position = i`; `pkg/models/testrefs.go:72`); the selector uses `Weight` (`pkg/credentiallb/selector.go:28`). Expansion therefore synthesizes `Position = <group ref's Position>` for every member (cosmetic; documented) and `Weight = <group ref's Weight>` (locked).

---

## Tasks

Work units are ordered by dependency. "Depends" references task IDs.

### WU-1 — Migration 033 + schema

| # | Task | Depends On | Acceptance |
|---|------|------------|------------|
| T-1 | Write `pkg/store/database/migrations/sqlite/033_add_credential_groups.up.sql` (DDL in §a) | none | file exists; `CREATE TABLE IF NOT EXISTS` + `IF NOT EXISTS` indexes |
| T-2 | Write `pkg/store/database/migrations/postgres/033_add_credential_groups.up.sql` (PG deltas in §a) | none | file exists; `TIMESTAMPTZ`/`NOW()` |
| T-3 | Write both `.down.sql` (`DROP TABLE IF EXISTS credential_groups`) | T-1,T-2 | documentation-only artifact — **the house has NO uniform down-file convention at 022+: 025/026/027 have NO down file and no 022+ down file drops a table**; no down-runner exists (see §a) |
| T-4 | Append `{"033", "033_add_credential_groups.up"}` to the registry slice at `migrate.go:78` **with the Amendment-1 trap comment** | T-1,T-2 | registry len 33; latest = 033; trap comment present |
| T-5 | Add migration test — **SQLite-only** (A-6i): assert `credential_groups` + its index exist after `Initialize` and that a second `Initialize` is idempotent. **No `pg_integration`-tagged twin**: the 028 PG twin exists because 028 is the repo's first file-level `BEGIN…COMMIT` migration (032 is also file-level and has **no** PG twin); a single `CREATE TABLE IF NOT EXISTS` needs none, and a tagged test never runs under phase3's plain `go test` — a gate that cannot fail is not a gate | T-4 | `--- PASS` literal; idempotent re-run |
| T-5b | **Re-anchor the three `len(migrations)-1` loops** (`pkg/store/database/migrations_test.go:140`/`:251`/`:522`) deliberately for **33** entries (A-6ii). Growing 32→33 would otherwise run 001..**032** (032 is a data-only `UPDATE` on `models`) into the 028 fixtures. **Do NOT weaken the fixtures** — re-anchor the bound only | T-4 | the three loops exclude 033 and run 001..032/031 as intended; existing fixtures untouched |

### WU-2 — `pkg/models` types + validation

| # | Task | Depends On | Acceptance |
|---|------|------------|------------|
| T-6 | New `pkg/models/credential_group.go`: `CredentialGroupConfig{ID,Name,Provider,Members []string,CreatedAt,UpdatedAt}`, `MaxCredentialGroupMembers = 16` (mirror `group.go:25`), sentinels `ErrCredentialGroupNotFound`, `ErrCredentialGroupInUse`, `ErrCredentialGroupMemberNotFound`, `ErrCredentialGroupProviderMismatch`, `ErrCredentialGroupMemberDuplicate`, `ErrCredentialGroupMemberEmpty` (all `&ConfigError{}` per `credential.go:177-178`) | T-4 | compiles; sentinels are `errors.Is`-matchable |
| T-7 | Extend `CredentialRef` with `GroupID` (representation above) + `IsGroupRef()` helper | none | `config.go:331-335` compiles |
| T-8 | `ValidateCredentialGroup(g, credLookup)` — field + collection rules (§b) | T-6 | rejects empty id/name, unknown provider, >16 members, dup members, missing member, provider mismatch |
| T-9 | `ValidateCredentialRefs(refs, groups, creds)` — model-ref rules with group branch (§b) | T-7,T-8 | rejects both-set/neither-set, missing group, non-chat kind + group ref, expanded >16 |
| T-10 | JSON-twin reject: `pkg/models/config.go` validation (`Validate` / credential-ref path) returns a clear error when `GroupID != ""` | T-7 | `ModelsConfig.Validate` rejects group refs; test pins DB-only |
| T-10b | **JSON-twin STUB implementations of the 8 new interface methods** (A-5) on `*models.ModelsConfig` — **7 of the 8 return empty/nil; `FlattenCredentialRefs` is CARVED OUT: raw/identity fallback (never empty) per T-23d** (the twin already rejects group refs per T-10). **Compile blocker**: `race_executor_shadow_reasoning_test.go:92` asserts `var _ models.ModelsConfigInterface = (*models.ModelsConfig)(nil)`, (the ui tests construct a test-local mock, NOT the twin — see the §e A-5 note). Scheduled HERE (early, with WU-2) so the tree never hits a mid-WU compile stall; re-check the `strict.go:16-18` compile assertion after the interface grows | T-7 | tree compiles with the grown interface; 7 stubs return empty/nil, `FlattenCredentialRefs` returns the raw list |
| T-10c | **A-5 sweep — test-local interface implementers get the same 8 stubs** (approver iteration-001 blocker 1) — **7 no-op empty/nil + the `FlattenCredentialRefs` carve-out (raw/identity fallback, never empty) per T-23d**: `mockModelsConfig` in `pkg/proxy/normalizers/registry_config_test.go:14` (individual methods), `mockModelsConfig` in `pkg/proxy/internal_handler_test.go:39` (consumed by ~17 pkg/proxy test files), `mockModelsConfig` in `pkg/ui/handlers_models_test.go:24`, `stubModelsConfig` in `pkg/proxy/handle_models_imggen_test.go:27`; `engineBackedResolver` (`race_coordinator_credfailover_test.go:31`) is covered via its embedded `*mockModelsConfig`. `recordingModelsConfig` is compile-safe (interface embedding — no action); `fakeStrictSource` is already T-26. Scheduled HERE (WU-2, with T-10b) — BEFORE the interface grows at T-24 — so the tree compiles in all 3 packages at every commit | T-7 | all named implementers compile with the grown 26-method interface; 7 stubs return empty/nil, `FlattenCredentialRefs` returns the raw list |

### WU-3 — Manager CRUD + reverse lookups + group cache + fan-out + events

| # | Task | Depends On | Acceptance |
|---|------|------------|------------|
| T-11 | New `pkg/store/database/credential_group.go`: `ModelsManager` gains `groups map[string]models.CredentialGroupConfig` **plus a dedicated `groupsMu sync.RWMutex`** (A-3). Rebuilt in `NewModelsManager` (store.go:765-814) from a new `GetAllCredentialGroups` scan. `groupsMu` is **independent of `m.mu`** — flatten takes `groupsMu.RLock()` everywhere; group writes take `groupsMu.Lock()` at the swap | T-6 | manager constructs; map populated at boot; lock discipline per §c.1 |
| T-12 | `GetCredentialGroup` / `GetCredentialGroups` (legacy reads, mirror `store.go:2113`/`:2156`) + `GetCredentialGroupStrict` / `GetCredentialGroupsStrict` (mirror `:2204`/`:2258`) | T-11 | `--- PASS`; strict variants propagate errors |
| T-13 | `AddCredentialGroup` / `UpdateCredentialGroup` / `RemoveCredentialGroup` — validate, dialect-aware SQL via `m.qb`, swap `m.groups` under `groupsMu.Lock()`, then fan-out (§c.3). **`AddCredentialGroup` fans out TOO** (A-15i — closes the direct-SQL / DB-restore / R-1-repair hole; one reverse lookup on a rare admin op) | T-11,T-12 | CRUD round-trips; fan-out fires on **all three** writes |
| T-13c | **O-2 — plain-model persisted-shape regression test**: the **zero-migration guarantee** (a plain credential-list model keeps its stored shape; no migration/backfill touches it) is currently only **STRUCTURAL** — add a test that persists a plain model and reads it back **byte-identically** (no `group_id` key, no re-ordered refs, `credential_id` shadow untouched) | T-9 | plain model round-trips byte-identically through the store |
| T-13b | **Group-map refresh + fail-closed flatten** (approver iteration-002 load-bearing): (a) the STORE-side `m.groups` map has **NO reconcile** (unlike the decorator mirror's ≤60s self-heal) ⇒ a group written by direct SQL is invisible until restart — **MANDATE (approver iteration-003): the store group map is REBUILT FROM THE DB on the reconcile trigger — never merged** (the R-1 repair writes groups by DIRECT SQL, so a merge-based reconcile would keep the stale entry); the reconcile trigger is the decorator's (A-4), and the ≤60s self-heal + **restart-required** rule for direct-SQL writes is documented alongside it; (b) a **MISSING group** must fail **CLOSED** per A-8 with an explicit **WARN naming the missing group id** (a silent skip ⇒ 502 with no diagnostic); (c) an **EMPTY group** (allowed by the empty-group rule) contributes nothing to the flat list — if that leaves the model with **zero** refs the failure must be **observable**: explicit **WARN naming the empty group** + the legacy-degrade 502, never a silent zero-ref resolve | T-11,T-13 | missing group ⇒ WARN + fail closed; empty group ⇒ WARN + observable 502; refresh/restart rule documented |
| T-14 | `ModelsUsingCredentialGroup(groupID) []string` reverse lookup over `models.credentials_json` (precedent `RemoveCredential` scan `store.go:2384-2392`) — **single-scan implementation (approver iteration-002 load-bearing):** the list endpoint's `used_by` for ALL groups must be computed in **ONE batched query/pass** over `models.credentials_json` (collect every group ref in a single sweep) — NOT one full-table scan per group (N scans at list time) | T-11 | returns referencing model IDs, ordered; list `used_by` computed in ONE batched pass |
| T-15 | `GroupsContainingCredential(credID) []string` membership lookup — **lock-free internal accessor** (A-3): it is called from inside `RemoveCredential`'s `m.mu.Lock()` span (`store.go:2381`) and Go `RWMutex` is **non-reentrant** → taking `m.mu.RLock()` (or `groupsMu.RLock()` while `groupsMu` is held) inside it self-deadlocks. Documented precedent `store.go:2076-2079` | T-11 | returns group IDs containing the credential; no deadlock under `m.mu` |
| T-16 | Extend `RemoveCredential` guard (`store.go:2380-2418`): block (409-worthy error) when the credential is a member of any group **or** referenced by any model; message names **both** surfaces | T-15 | group-only credential delete blocked; message lists group + model |
| T-17 | `UpdateCredential` provider-change guard: block provider change when the credential is a member of a group whose provider differs (refinement, §c.4) | T-15 | provider change on a grouped credential rejected |
| T-18 | `FlattenCredentialRefs(modelID) []models.CredentialRef` exported preview accessor (FE display; phase2) — **extended by A-2** to serve the proxy/UI raw-list gates (§d.2b) | T-19 | returns flattened list; empty for unknown model |
| T-18c | **O-6 — `FlattenCredentialRefsByID` has NO production caller** (BE deep review): wire it to a real consumer **or delete it** — leave no dead, untested code in the lane | T-18 | the symbol has a production consumer with a test, or it does not exist |
| T-18b | **Shadow-column rules (A-7)**: `models.credential_id` stays derived from `PrimaryCredentialID()` (`store.go:1592`/`:1729`) → **EMPTY for group-ref-first models — never bind the group id**. It is write-only for legacy/external readers (no production SELECT reads it — `querybuilder.go:194-260` omits it; `dbModelRow` has no such field). Document "legacy readers see no credential" as intended; cross-ref `phase3-plan.md` T-8.4 (the R-1 repair rewrites it via the PUT re-derivation) | T-7 | group-ref-first model persists `credential_id = ""`; never the group id |

### WU-4 — The flattening seam

| # | Task | Depends On | Acceptance |
|---|------|------------|------------|
| T-19 | Implement unexported helper `FlattenCredentialRefs(model *models.ModelConfig) []models.CredentialRef` (algorithm §d.1) reading `m.groups` (in-memory) under `groupsMu.RLock()` | T-11,T-7 | unit tests: mixed refs, dedup keep-first, ordering, empty group |
| T-20 | Apply helper at the **5 engine-binding sites**: `store.go:789`, `:867`, `:1632`, `:1768` (pass flattened refs); `:1834` stays `nil` | T-19 | engine receives flat lists |
| T-21 | Apply helper at the **top** of `ResolveInternalConfigWithAffinity` (`store.go:2543`) — flatten **before** the `len==0`/`==1`/`>=2` branches; fast path reads `refs[0].CredentialID` | T-19 | E-3 single-group-ref resolves N members |
| T-22 | Apply helper at the **top** of `ResolveInternalConfigWithAffinityCached` (`store.go:2679`) — same flatten-before-branch rule | T-19 | cached variant resolves single-group-ref; zero DB reads |
| T-23 | Zero-DB contract pin: assert the cached resolver performs **no** DB query when flattening (group map in-memory) | T-22 | test green; no `m.store.DB` call on the resolve path |
| T-23b | **A-1 — the legacy 5-tuple resolver pair**: flatten **before** the `PrimaryCredentialID()` read in `ModelsManager.ResolveInternalConfig` (`store.go:2444`, read at `:2458`) → the flat primary credential is used instead of the raw (empty) `Credentials[0].CredentialID` | T-19 | group-primary model resolves its real provider via the 5-tuple resolver |
| T-23c | **A-1 — the decorator twin**: `CachedModelsConfig.ResolveInternalConfig` (`pkg/modelscache/models.go:750`) is **self-contained** (it never delegates to the store — it exists for DB-outage coverage) → it must flatten **cache-side from the decorator group mirror** (A-4's `groupsByID`/`groupsSnap`) before its own `PrimaryCredentialID()` read | T-23b,T-25 | DB-outage path resolves a group-primary model's provider |
| T-23d | **A-2 — proxy/UI raw-list gate flattening view**: extend the E-8 accessor (`FlattenCredentialRefs`) to the proxy path, or expose a cached flat list invalidated by the same fan-out, so every gate in §d.2b consumes a **flattened** `[]models.CredentialRef` (never a group object). A single group ref must NOT yield failover budget 0. **The §g AST pin still holds**: the pin forbids `CredentialGroup*` **types/values** in selection code, and the flattened view serves **plain `[]models.CredentialRef`** (groups already expanded away) — A-2 adds *call sites*, not *types*, so the pin is unchanged and still enforceable. **Mock/twin contract (stub carve-out — applies to EVERY stub site: the T-10b JSON twin and the T-10c test mocks):** `FlattenCredentialRefs` returns the **RAW ref list (identity fallback — NEVER empty)**; the **other 7** new-method stubs return empty/nil. **Rationale:** an empty/nil flatten stub makes the A-2 gates read a zero-length flat view ⇒ failover ineligible, budget 0, rotation/reorder off for EVERY model served by that config source — `mockModelsConfig` (`pkg/proxy/internal_handler_test.go:39`) backs ~17 pkg/proxy test files, so the Pack A failover/rotation suites go red. Tests that assert flattening itself use faithful fakes. **Census (approver iteration-003):** the §d.2b gate set is **15 anchors / 9 rows** (8 behavioural + 1 log-only). **Accessor shape — DECIDED HERE:** the architecture doc's A-2 wording leaves the accessor shape open; **this task decides it** (see the DB-read property clause) and the arch-doc wording does not bind the shape. **DB-read property (approver iteration-002 load-bearing):** the E-8 accessor, as extended, is a **READ-THROUGH** accessor — a **DB read per attempt**; state this explicitly and decide the shape HERE: if per-attempt DB reads on the hot path are unacceptable, the task MUST use the **cached-flat-list variant** (invalidated by the same fan-out, §c.3) instead — the choice is made in this task, not deferred. **Group-provider re-key (approver iteration-002 load-bearing):** the 9router-primary guards (`server.go:1046`/`:1089`/`:1168`/`:1540`/`:1572`/`:1633`) key on the **credential id** ⇒ a **group ref bypasses the guard**; the flattened view must re-key the provider check on the **GROUP's provider** so a group whose provider is the 9router-primary provider is still guarded (mirror of the FE-side `primaryProvider` re-derivation). | T-18,T-19 | every §d.2b gate reads the flat list |
| T-23e | **A-8 — fail-closed flatten**: expand only members whose provider matches the group's provider; **skip + WARN** on divergence (covers direct-SQL provider edits that bypass the write-time validator). Never expand a mixed-provider list into a same-provider model | T-19 | divergent member is skipped + WARN logged; remaining members expand |

### WU-5 — Decorator forwards (`pkg/modelscache`)

| # | Task | Depends On | Acceptance |
|---|------|------------|------------|
| T-24 | Add the 8 methods (§e) to `models.ModelsConfigInterface` (`config.go:446-477`) and the Strict twins to `modelscache.strictSource` (`strict.go:19-34`) | T-12,T-13,T-14,T-15,T-18 | both interfaces compile; `outage_test.go` assertion holds |
| T-25 | Decorator forwards + cache strategy for all 8 (mirror `AddModel` `models.go:804-826` delegate-then-swap; **no** `Internal=true` stamp) | T-24 | `--- PASS` |
| T-25c | **C1 — reconcile tick must not block the request path** (BE deep review, 2026-11-17; newly introduced vs base `2b987bf` — the pre-existing `reconcileOnce` took only `c.mu.RLock()` on the decorator, never a store write lock): the ≤60s reconcile tick currently takes the store's global write mutex `m.mu.Lock()` across an **UNBOUNDED-context DB read** (`context.Background()`, `credential_group.go:641`) + a **full models-table scan** + an **UNCONDITIONAL fan-out** (`:708-724`). `sync.RWMutex` is **writer-preferring** ⇒ a pending `Lock()` queues live request-path readers (`applyGroupRewrite` `pkg/proxy/handler_groups.go:68` → `modelscache/models.go:1213` UNCACHED → `store_group_rewrite.go:54` → `store.go:1082` `m.mu.RLock()`, live at `handler.go:712`/`:1160`, `handler_anthropic.go:433`); under a DB stall ≈5s×(2+N) ⇒ ≈10s at zero groups, ≈90s for one 16-member group, unbounded on PostgreSQL. **Blast radius is LATENCY ONLY** — `applyRefsLocked` touches `st.credentials`/`st.selector`; `st.cooldowns` and `st.bindings` are separate maps, so backoff is NOT defeated. **Fix:** (a) **bounded ctx** (5s, matching the sibling path's `modelscache/models.go:373` `StrictFillTimeout`); (b) the **DB read OUTSIDE the store write mutex** — the lock covers only compare+apply; (c) **ONE** `SELECT id, provider FROM credentials WHERE id IN (…)` instead of N+1 reads; (d) **CONDITIONAL fan-out** — compare the fresh scan against `m.groups` under `groupsMu` and **early-return when equal** (deletes the per-minute full models scan + the engine re-apply); (e) **log the discarded error instead of `_ =`**; (f) **REGRESSION TEST (RED against the current code)** proving the reconcile no longer blocks the request path | T-25b | the reconcile tick completes within its bounded ctx without holding `m.mu` across the DB read; the RED-first regression test goes green; no request-path reader is queued behind the reconcile writer | T-25 | one delegation per method |
| T-25d | **O-4 — consistent `groupsSnap` sort**: reconcile sorts by **id**, write-through sorts by **name** ⇒ the FE list order **flips** after the next reconcile. Sort by ONE key everywhere (id) so the FE order is stable across both paths | T-25b | `groupsSnap` order is identical after a write-through and after a reconcile |
| T-25e | **O-9 — strict-fill must insert into `groupsSnap` too**: today a strict fill can leave a group present in **DETAIL** yet absent from **LIST** for ≤60s — the strict-fill path must populate both mirrors (`groupsByID` **and** `groupsSnap`) | T-25b | after a strict fill, DETAIL and LIST agree (no ≤60s divergence) |
| T-25f | **Mirror hygiene (O-5 / O-7 / O-8)**: (O-5) make the mirror shape consistent across the write-through and strict-fill paths; (O-7) remove the `_ = sort.Strings` **no-op** and its sole-purpose import; (O-8) **prune `m.groupMemberProviders` on remove** (a removed group must not leave a provider entry behind) | T-25b | mirrors agree in shape; the no-op sort + import are gone; removed groups leave no `groupMemberProviders` residue |
| T-25b | **A-4 — BUILD + PRIME the decorator group cache**: add `groupsByID` + `groupsSnap` to the decorator and wire them into `strictSnapshot` (`modelscache/models.go:179-198` — today it reads **only** models/enabled/creds), `applySnapshot`, and `reconcileOnce` (≤60s heal parity). Boot priming + reconcile heal. **Without this, production group reads return EMPTY forever** (`CACHE_LAYER_ENABLED` default ON binds prod to the decorator — `cmd/main.go:170`/`:187`). Inner `m.groups` stays **AUTHORITATIVE for flattening**; the decorator group cache is a **read-only mirror** rebuilt on write-through + reconcile | T-12,T-25 | decorator `GetCredentialGroups` returns the seeded groups after boot; reconcile heals a drift |

### WU-6 — API surface (`pkg/ui/server.go`)

| # | Task | Depends On | Acceptance |
|---|------|------------|------------|
| T-27 | Register `GET|POST /fe/api/credential-groups` and `GET|PUT|DELETE /fe/api/credential-groups/{id}` beside the credentials pair (`server.go:195-197`). The `{id}` handler registers with the **literal trailing slash** `"/fe/api/credential-groups/"` — ServeMux prefix-subtree semantics (precedent `server.go:197`) (A-12vi) | T-13 | routes reachable; trailing-slash registration confirmed |
| T-28 | Handlers `handleCredentialGroups` / `handleCredentialGroupDetail` (§f): 64KB cap, required fields, `used_by` on **list and detail** (A-10), bare `{"error":...}` envelope at **every** error site incl. 405 + missing-id (A-9), `errors.Is` sentinel mapping, status mapping | T-13,T-14 | API tests green |
| T-28b | **A-11 — `handleTestModel` group expansion** (`server.go:2053-2078`): the posted `credential_id` may be a **group id** → expand it to the **first flattened member** and test that member. Without it the first user-facing flow dead-ends (FE posts a group id → `GetCredential(groupID)` → **400**, `server.go:2078-2086`, body `TestModelResponse{Success:false,Error:"Credential not found: <id>"}` — **NOT 404**) | T-18,T-28 | posting a group id tests the first flattened member (not 404) |
| T-28c | **UI-side `validateCredentials` mirror gains a group branch** (approver iteration-001 blocker 2 — else group refs 400 on every model write BEFORE store validation is reached): `validateCredentials` (`pkg/ui/server.go:682`) enforces a non-empty identifier → today every group ref (`{credential_id:"",group_id:…}`) fails rule (a) with the **UI-mirror** text `"credentials[0]: identifier is empty"` (`server.go:715` — the STORE-side text is `model %s: credentials[%d].credential_id is empty`, `store.go:1943`) on ALL FOUR model write/validate paths — POST `/fe/api/models` (`:1017`), PUT (`:1511`), copy (`:1863`), `/fe/api/models/validate` (`:2018`) — gated only by `Kind != KindGroup` (`:1016`), short-circuiting BEFORE `AddModel`/`UpdateModel` (so T-9's group-aware `ValidateCredentialRefs` is unreachable via API). Fix: accept a ref when `credential_id != ""` **OR** `group_id != ""`; group refs validated for existence (+ chat-kind-only) in the mirror, consistent with T-9's store-level rules; ALL FOUR paths covered | T-9,T-18,E-2 | a group-referencing model is creatable/editable/copyable/validatable via the API (no "identifier is empty" 400); missing group / cross-provider still rejected |

### WU-7 — Structural pins + backend test matrix + gate handoff

| # | Task | Depends On | Acceptance |
|---|------|------------|------------|
| T-29 | AST/structural pin (§g): full AST walk + import guard | T-19 | pin test `--- PASS` |
| T-30 | Backend test matrix (§h) implemented across `pkg/store/database`, `pkg/models`, `pkg/modelscache`, `pkg/proxy`, `pkg/ui` | T-20..T-29 | every added `func Test` shows literal `--- PASS` |
| T-31 | gofmt zero-new-drift on all touched files (line-set diff vs base `2b987bf`) | T-30 | no new flagged lines |
| T-27b | **A-14 — refresh the stale doc comments** on both copy funcs (`pkg/models/copy.go:171-173` "a ref carries only credential_id/weight/position" → also `group_id`; `pkg/modelscache/copy.go` equivalent) so no future field-enumeration "fix" breaks `GroupID` survival. **No behavioural change** — both remain a plain `copy(dst, src)` over the value struct | T-7 | comments mention `group_id`; copy behaviour unchanged |

---

## (a) Migration 033 spec

**Registry trap (call out explicitly):** `migrate.go:24-78` is an **ordered slice, not a directory scanner**. Appending the entry is what **activates** the SQL — the four in-code comments at `:53-57`/`:59-63`/`:64-69`/`:70-77` document this exact trap. A new `033` needs **BOTH** the file pair **AND** the registry append at `:78`, plus the same trap comment. Without the append the files are silently inert.

**sqlite — `033_add_credential_groups.up.sql`** (shape mirrors `005_add_credentials.up.sql:1-12`):
```sql
-- Migration 033: credential groups (management-only bundling of same-provider credentials)
CREATE TABLE IF NOT EXISTS credential_groups (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    provider     TEXT NOT NULL,
    members_json TEXT NOT NULL DEFAULT '[]',
    created_at   TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at   TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_credential_groups_provider ON credential_groups(provider);
```
**postgres — same file, dialect deltas** (mirrors `migrations/postgres/005_add_credentials.up.sql`):
```sql
CREATE TABLE IF NOT EXISTS credential_groups (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    provider     TEXT NOT NULL,
    members_json TEXT NOT NULL DEFAULT '[]',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_credential_groups_provider ON credential_groups(provider);
```
**`.down.sql` (both):** `DROP TABLE IF EXISTS credential_groups;` (**no uniform house convention at 022+ — 025/026/027 have NO down file, and no 022+ down file drops a table**; documentation-only, no down-runner exists — rollback = restore backup).

**Registry append (`migrate.go:78`):**
```go
    // Amendment-1 trap (fifth occurrence): this registry is an ordered
    // slice, NOT a directory scanner; this append activates the 033 SQL
    // files. Without this append the 033 files are silently inert and
    // credential groups are unavailable.
    {"033", "033_add_credential_groups.up"},
```
**No `models` column change** (representation §Locked) → no backfill, no data migration. Additive `CREATE TABLE IF NOT EXISTS` + index is house style. SQLite runner's defensive `ROLLBACK` (`migrate.go:113-133`) is load-bearing (modernc sqlite has no mid-`BEGIN` auto-rollback).

**A-6 — migration test scope (binding).**
- **(i)** The migration test is **SQLite-only** (T-5). The 028 PG twin exists because 028 is the repo's **first file-level `BEGIN…COMMIT`** migration (`migrations_pg_test.go:7-13`); 032 is also file-level and has **no** PG twin. A single `CREATE TABLE IF NOT EXISTS` needs none — and a `pg_integration`-tagged test never runs under phase3's plain `go test`, i.e. **a gate that cannot fail is not a gate**.
- **(ii)** The three `len(migrations)-1` loops (`migrations_test.go:140`/`:251`/`:522` — the comments self-document the exclusion) exclude only the **latest** entry. Growing 32→33 makes them run **001..032** (032 is a data-only `UPDATE` on `models`) into the 028 fixtures. **Re-anchor the bound deliberately for 33 entries; do NOT weaken the fixtures** (T-5b).

---

## (b) Types + validation spec (`pkg/models`)

**Type** (`credential_group.go`):
```go
const MaxCredentialGroupMembers = 16 // mirror MaxGroupMembers (group.go:25)

type CredentialGroupConfig struct {
    ID        string   `json:"id"`
    Name      string   `json:"name"`
    Provider  string   `json:"provider"`
    Members   []string `json:"members"`   // ORDERED member credential IDs
    CreatedAt string   `json:"created_at"`
    UpdatedAt string   `json:"updated_at"`
}
```
`members_json` = JSON array of credential-ID strings (house style: JSON-on-row, no join tables — `credentials_json` 028, `members_json` 031).

**Sentinels** (`&ConfigError{}`, `errors.Is`-matchable, precedent `credential.go:177-178`):
`ErrCredentialGroupNotFound`, `ErrCredentialGroupInUse`, `ErrCredentialGroupMemberNotFound`, `ErrCredentialGroupProviderMismatch`, `ErrCredentialGroupMemberDuplicate`, `ErrCredentialGroupMemberEmpty`.

**Group-level rules** (`ValidateCredentialGroup(g, credLookup)`):
- G-1 `ID` non-empty; G-2 `Name` non-empty; G-3 `Provider` ∈ `models.ValidProviderNames()` (`credential.go:59-72`).
- G-4 `len(Members) <= MaxCredentialGroupMembers` (16).
- G-5 no duplicate member IDs.
- G-6 every member exists (`credLookup` non-nil) — else `ErrCredentialGroupMemberNotFound`.
- G-7 every member's provider == group provider (case-insensitive, matching existing provider-match semantics `store.go:1960-1977`) — else `ErrCredentialGroupProviderMismatch`.
- G-8 **empty `Members` ALLOWED** (create-then-populate flow). Flatten of an empty group contributes nothing.
- G-9 **no nesting**: `Members` holds credential IDs only; the schema/API cannot express group-in-group (state it; no code path).

**Model-ref rules** (`ValidateCredentialRefs(refs, groups, creds)` — replaces/augments `validateModelAgainstCredentials` `store.go:1930-1977`):
- R-1 each ref is **exactly one** of direct/group (reject both-set and neither-set).
- R-2 direct ref: credential exists, `Weight > 0` (selector requires positive weights, `selector.go:28`), provider matches primary (existing rule `store.go:1960-1977`).
- R-3 group ref: group exists in `groups`, `Weight > 0`.
- R-4 **expanded length ≤ `MaxCredentialRefs` (16)** — validate the EXPANDED list, not the raw list (research-seam §e.8). Raw cap at `store.go:1956` is retained but the expanded cap is the binding one.
- R-5 **non-chat kinds forbid group refs** — `decision`, `imggen`, `group` kinds reject any `GroupID != ""` (dispatcher-locked; keeps FE Test buttons coherent — `handlers_decision.go:188-193`, `handlers_imggen.go:287-292` read `Credentials[0]` raw).
- R-6 dedup keep-first is a **flatten** concern, not a write-reject: a model may list the same credential directly and via a group; the flattened list dedups (pinned by test).
- R-7 a model whose list is **one empty group** flattens to 0 refs → **legacy degrade** (`ok=false`, primary path) — **allowed, pinned explicitly** (not an error; matches `len==0` semantics `store.go:2567`/`:2720`).

**JSON-twin reject:** `pkg/models/config.go` `Validate` / credential-ref path returns a clear error when `GroupID != ""` ("credential groups are supported only in the database config backend"). Credential groups are **DB-backed only**; the JSON twin (`config.go:1189`/`:1253`, no engine, single-credential, legacy/test lane) never flattens. Document in the plan + code comment. **A-5: the twin additionally grows STUB implementations of the 8 new interface methods** (7 return empty/nil; **`FlattenCredentialRefs` is carved out — raw/identity fallback, never empty, per T-23d**) so the grown `models.ModelsConfigInterface` still compiles — see T-10b.

**UI-side validation mirror — LOAD-BEARING (approver iteration-001 blocker 2):** the UI mirror `validateCredentials` (`pkg/ui/server.go:682`) runs BEFORE `AddModel`/`UpdateModel` on **all four** model write/validate paths — POST `/fe/api/models` (`:1017`), PUT (`:1511`), copy (`:1863`), `/fe/api/models/validate` (`:2018`) — and today rejects every group ref with 400 "identifier is empty" (rule (a): non-empty identifier; the `Kind != KindGroup` gate at `:1016` never exempts chat models). The group branch (**T-28c**) is therefore load-bearing for the headline flow: without it, group-referencing models cannot be created, edited, copied, or validated via the API at all, and the store-level T-9 validation is unreachable dead code on the API path.

---

## (c) Manager CRUD + reverse lookups + fan-out + events (`pkg/store/database`)

New file `credential_group.go` (house style: `store_group_rewrite.go`, `store_group_test.go`). All SQL via dialect-aware `m.qb`; all writes under `m.mu` (`sync.RWMutex`, struct `store.go:696-728`).

**c.1 Group state cache + LOCK DISCIPLINE (A-3 — binding).**
`ModelsManager.groups map[string]models.CredentialGroupConfig`, rebuilt at `NewModelsManager` (`store.go:765-814`) via a new `GetAllCredentialGroups()` scan; swapped on group writes. This is what makes flatten **zero-DB** (§d.4).

**`m.groups` gets a DEDICATED `groupsMu sync.RWMutex` — zero ordering interaction with `m.mu`:**
- **flatten takes `groupsMu.RLock()` everywhere** (all 8 store application sites — including the c.3 fan-out) — never `m.mu.RLock()`.
- **group writes take `groupsMu.Lock()` at the swap** — never `m.mu`.
- Why a dedicated lock: flatten is called from sites that already hold `m.mu.Lock()` (the AddModel/UpdateModel engine-notify spans at `store.go:1632`/`:1768`, the `RemoveCredential` span at `store.go:2381`) → an `m.mu.RLock()` inside flatten **self-deadlocks** (Go `RWMutex` is non-reentrant; documented precedent `store.go:2076-2079`). The alternative (lock-free map access) is a **fatal concurrent-map read** on the resolve-hot path (`store.go:2543`/`:2679`) — a process crash on the critical substrate.
- **Internal lock-free accessors**: membership lookups that are invoked from inside a held `m.mu.Lock()` span (notably `GroupsContainingCredential` called by `RemoveCredential`) use a **lock-free internal accessor** that does not take `m.mu.RLock()` and does not re-enter `groupsMu` while it is held.
- **Reconciliation (read-rule vs write-rule, so §c.1 and §c.3 cannot be misread as contradicting):** the bullets above govern the **read path** — *flatten never takes `m.mu`* (it would self-deadlock inside the held `m.mu.Lock()` spans). Group **writes** are the other half of A-3 (§c.3): they hold `m.mu` for the whole validate → SQL → swap → fan-out span **and** take `groupsMu` at the swap — both locks, strict order `m.mu` → `groupsMu`, reverse never occurs. Complementary halves of one discipline.

**c.2 CRUD** (mirror credential CRUD `store.go:2302`/`:2332`/`:2380`):
- `AddCredentialGroup(g)`: validate → `INSERT` → swap `m.groups[g.ID]` under `groupsMu.Lock()` → **fan-out** (A-15i: an added group can be referenced by a model row written by direct SQL / DB restore / the R-1 repair dance, which never passed through `AddCredentialGroup`; one reverse lookup on a rare admin op closes the hole).
- `UpdateCredentialGroup(id, g)`: ID immutable (`ErrCannotChangeCredentialID` precedent) → validate → `UPDATE` → swap `m.groups[id]` → **fan-out**.
- `RemoveCredentialGroup(id)`: **in-use guard FIRST** — `ModelsUsingCredentialGroup(id)` non-empty → `ErrCredentialGroupInUse` naming the referencing models (block, never cascade — precedent `RemoveCredential` `store.go:2384-2392`) → `DELETE` → delete `m.groups[id]` → no fan-out (unreferenced by construction).

**c.3 Fan-out (the engine-staleness fix).** On **every** group write (A-15i: add / update / member add / member remove), for **every** model in `ModelsUsingCredentialGroup(groupID)`:
```go
refs := m.FlattenCredentialRefs(model)   // fresh flat list (groupsMu.RLock inside)
m.engine.OnModelChanged(model.ID, refs)    // engine.go:366
m.publishCredentialsChanged(model.ID)      // store.go:2425-2434 (nil-bus tolerant)
```
The reverse lookup **is** the fan-out list. Without it, selection runs stale until restart (research-seam §e.3). Precedent for write-time fan-out: `rebuildClaimsLocked` on every model write (`store.go:901-906`).

**The fan-out executes INSIDE the `m.mu` write span** (A-3): the group write holds `m.mu.Lock()` across validate → SQL → group swap → fan-out. This serializes the group write against model writes and **kills the stale-flat-list last-writer-wins window** (a concurrent `UpdateModel` cannot interleave between the swap and the fan-out). Lock order is strictly `m.mu` → `groupsMu`; the reverse order never occurs.

**c.4 Credential-write guards (refinements, with rationale).**
- `RemoveCredential` (`store.go:2380-2418`): extend the in-use scan to also consult `GroupsContainingCredential(id)`; block with a message naming **both** surfaces ("credential 'x' is in use by model 'y' and group 'z'"). Closes the guard hole where a group-only credential escapes → dangling member (research-seam §e.4).
- `UpdateCredential` provider change: block when the credential is a member of a group whose provider differs — otherwise the same-provider invariant silently breaks. (Refinement; rationale: invariant preservation. Message names the group.)

**c.5 Reverse lookups.**
- `ModelsUsingCredentialGroup(groupID) []string` — scan `models.credentials_json` for `GroupID == groupID` (precedent `store.go:2384-2392`).
- `GroupsContainingCredential(credID) []string` — scan `m.groups` membership; **lock-free internal accessor** when called from inside a held `m.mu.Lock()` (A-3, §c.1).
- `FlattenCredentialRefs(modelID) []models.CredentialRef` — exported preview accessor for the FE (phase2) **and the A-2 proxy/UI gate view** (§d.2b).

**c.6 Events.** Reuse `publishCredentialsChanged` (`store.go:2425-2434`) per referencing model; the bus-drain refresh (`drainCredentialsChanged` `store.go:843`) already re-binds via `store.go:867` → now flattened. No new event type.
- **A-15.ii — no credential-UPDATE refresh chain exists.** `publishCredentialsChanged` callers are **model writes only** (`store.go:1633`/`:1769`); credential DELETE refreshes via direct `engine.OnCredentialDeleted` (`store.go:2416`). The engine holds **refs only** (key material is fresh-read at resolution), so **api_key rotation needs NO fan-out** — do **not** build on that phantom mechanism; group writes publish the per-model event only.
- **A-15.iii — the drain path self-heals**: on each event the drain re-reads `GetModelNative` + the **current** group state (`store.go:843-869`) → the flat list is recomputed, and orphan-binding eviction on shrink (`engine.go:383-393`) is exactly the right semantics for a flattened-list shrink.

**c.7 Shadow column (A-7).** `models.credential_id` remains derived from `PrimaryCredentialID()` (`store.go:1592`/`:1729`). For a **group-ref-first** model it is written **EMPTY** — never the group id. It is write-only for legacy/external readers (no production SELECT reads it — `querybuilder.go:194-260` omits it; `dbModelRow` has no such field). "Legacy readers see no credential" is **intended** and documented; the R-1 repair rewrites it via the PUT re-derivation (`phase3-plan.md` T-8.4).

---

## (d) THE FLATTENING SEAM spec

### d.1 The one helper
```go
// pkg/store/database/credential_group.go
func (m *ModelsManager) FlattenCredentialRefs(model *models.ModelConfig) []models.CredentialRef
```
Algorithm (deterministic, O(total refs + members), in-memory; takes `groupsMu.RLock()` for the whole walk — A-3):
1. `model == nil || len(model.Credentials) == 0` → `nil`.
2. Walk `model.Credentials` **in order**. For each ref:
   - **direct** (`GroupID == ""`): append `ref`.
   - **group** (`GroupID != ""`): look up `m.groups[ref.GroupID]`; if absent → **skip** (defensive; validation prevents). For each `memberID` in `group.Members` (stored order): append `models.CredentialRef{CredentialID: memberID, Weight: ref.Weight, Position: ref.Position}` — **subject to the A-8 fail-closed provider check below**.
3. **Dedup keep-first** by `CredentialID` across the whole list (first occurrence wins; later duplicates dropped).
4. Return.

**A-8 — fail-closed on provider divergence.** At flatten time, expand **only** members whose provider matches the **group's** provider; a divergent member is **skipped and WARN-logged** (never expanded). This covers direct-SQL provider edits that bypass the write-time validator (T-17): expanding a mixed-provider list into a same-provider model would break the provider-match invariant the whole credential-LB layer assumes. The check is done at flatten (not only at write) because the group map is the authority at resolve time.

`Weight` inherited from the group ref (locked). `Position` = the group ref's `Position` (cosmetic; not selector-relevant — evidence `selector.go:28` uses `Weight`; `Position` only written by UI `server.go:753`).

### d.2 The call sites — **8 store application sites + 1 decorator site** (9 application points total; A-1) — **AUTHORITATIVE CENSUS: every document cites this line** (application site vs decorator twin vs no-change site is marked per row)
| # | Site | Line | Change |
|---|------|------|--------|
| 1 | Startup rebind | `store.go:789` | `m.engine.RebindFromStore(mc.ID, m.FlattenCredentialRefs(&mc))` |
| 2 | Bus-drain refresh | `store.go:867` | `m.engine.OnModelChanged(modelID, m.FlattenCredentialRefs(&mc))` |
| 3 | AddModel | `store.go:1632` | `m.engine.OnModelChanged(model.ID, m.FlattenCredentialRefs(&model))` |
| 4 | UpdateModel | `store.go:1768` | `m.engine.OnModelChanged(model.ID, m.FlattenCredentialRefs(&model))` |
| 5 | RemoveModel | `store.go:1834` | **no change** (`nil` is group-free) |
| 6 | `ResolveInternalConfigWithAffinity` | `store.go:2543` | flatten at top; `len==0`/`==1`/`>=2` read the **flattened** slice; fast path uses `refs[0].CredentialID` |
| 7 | `ResolveInternalConfigWithAffinityCached` | `store.go:2679` | same flatten-before-branch rule; `resolveSingle(refs[0].CredentialID)` |
| 8 | `ResolveInternalConfig` (legacy 5-tuple) — **A-1** | `store.go:2444` | flatten **before** the `PrimaryCredentialID()` read at `:2458` → the flat primary credential is used, not the raw (empty) `Credentials[0].CredentialID` |
| 9 | (c.3) group-write fan-out — **TWO functions** | new | the **named** variant (group-write fan-out: `OnModelChanged(modelID, flattened)` per referencing model, inside the `m.mu` write span — A-3) **and** the **wholesale reconcile-heal variant** (a **superset** of the named one — it re-applies the engine across the model's credential set on the ≤60s reconcile path). **O-11:** both are named here so the census matches the implementation |
| 10 | **decorator twin** `CachedModelsConfig.ResolveInternalConfig` — **A-1** | `pkg/modelscache/models.go:750` | **self-contained** (never delegates; it exists for DB-outage coverage) → flatten **cache-side from the decorator group mirror** (A-4) before its own `PrimaryCredentialID()` read |

**Count note (approver iteration-002):** `RemoveModel` (`store.go:1834`) needs **NO change** (`nil` is group-free) and is listed for completeness, **not** as an application site — the helper is applied at **8** store sites (rows 1-4, 6-8, 9) + the decorator twin (row 10) = **9 application points total**.

**A-1 failure mode is SILENT, not 502.** If site 8 or site 10 is missed, `normalizers.DetectProvider` (`pkg/proxy/normalizers/config.go:27`, called per streaming attempt at `pkg/proxy/race_executor.go:642`) degrades to `"unknown"` by design → provider-keyed stream normalizers are **inert** for group-primary models. The MiniMax reasoning-split gate (`raceIntProviderIsMiniMax`, `race_executor.go:59-77`) reads `PrimaryCredentialID()` raw and is likewise skipped. **Pin with tests** asserting `DetectProvider` returns the **real** provider for a group-primary model (§h T-24/T-25).

### d.2b A-2 — every raw-credential-list gate outside the seam consumes a FLATTENED list
These sites read `modelCfg.Credentials` **raw**, outside any resolution seam. For a group-primary model (raw len 1) they silently disable the headline behaviour while every path still returns **200** — no resolution pin catches it. All must consume a **flattened** `[]models.CredentialRef` (via the extended E-8 accessor / a cached flat list invalidated by the same fan-out — T-23d). **Census authority (approver iteration-003):** the A-2 gate census is **15 anchors across 9 rows** — 8 **behavioural** rows + 1 **log-only** row (`race_coordinator.go:1999` `modelBudget()`, sole caller `:1948`) — and this §d.2b table is the single source for it.

| Site | Line | Raw read | Effect today for a group-primary model |
|------|------|----------|-------------------------------------------|
| `pkg/proxy/race_coordinator.go` | `:1827` | `len(modelCfg.Credentials) <= 1` | credential-failover eligibility = **false** |
| `pkg/proxy/race_coordinator.go` | `:1845` | budget `< len(Credentials)-1` | failover budget = **0** |
| `pkg/proxy/race_executor.go` | `:257` | `credBelongs` membership over raw refs | reselect of a group member rejected as "wrong-model spawn" |
| `pkg/proxy/race_executor.go` | `:309`/`:322` + `preferUnmarkedUpstreamCredential` `:306-340` | `< 2` gate + raw candidate scan | skip-cache reorder never runs; group members never reorder candidates |
| `pkg/proxy/race_executor.go` | `:59-77` `raceIntProviderIsMiniMax` | `PrimaryCredentialID()` raw | MiniMax reasoning-split gate skipped |
| `pkg/proxy/internal_handler.go` | `:259` | `len(h.config.Credentials) <= 1` | rotation off |
| `pkg/proxy/handler_anthropic.go` | `:876` | `len(modelConfig.Credentials) > 1` | rotation off |
| `pkg/ui/server.go` | `:1046`/`:1089`/`:1168`/`:1540`/`:1572`/`:1633` | `GetCredential(Credentials[0].CredentialID)` ×6 | 9router-primary 400-guard silently bypassed (×6) |
| `pkg/proxy/race_coordinator.go` | `:1999` (`modelBudget()`, sole caller `:1948` — `[LB-FAILOVER] rotation %d/%d`) | `len(mc.Credentials)-1` — **LOG-ONLY row** | a group-primary model (raw len 1) logs **"rotation 1/0"** — misleading forensics on the instrumented path; the budget value must come from the **flattened** count (log-only: no behaviour change, but the log must not lie) |

**Why the §g AST pin still holds under A-2:** the pin forbids **`CredentialGroup*` TYPES and VALUES** from appearing in selection code — it does not forbid the selection code from calling an accessor. The flattened view serves **plain `[]models.CredentialRef`** (the same shape as today, group objects already expanded away), so no group type/value ever enters `pkg/proxy`/`pkg/credentiallb`/`pkg/stickyorder`/`pkg/skipcache`. The pin is **unchanged and still enforceable**; A-2 adds *call sites*, not *types*.

**Binding tests (§h T-26):** single-group-ref model → **failover eligible**, **budget ≥ members−1**; **reselect of a group member passes `credBelongs`**; **reorder runs**.

### d.3 Flatten-before-len-branches rule + E-3 trap
The flattened slice is computed **before** the `len(...)==0` / `==1` / `>=2` branches in **both** affinity resolvers, and **before** the `PrimaryCredentialID()` read in the legacy 5-tuple resolver (`store.go:2458`) and its decorator twin (`modelscache/models.go:759-762`). This is load-bearing: a model whose raw list is a **single group ref** would otherwise take the `==1` fast path (`store.go:2588`/`:2729`) and resolve the **group's ID** (empty `CredentialID` under the locked representation) as a credential → credentials-table miss → `ok=false` → **502**. **Pin with a test** (§h T-5): raw `[{group_id:"g1"}]`, `g1` has 2 members → resolution returns one of the 2 members.

### d.4 Zero-DB cached-resolver contract
`ResolveInternalConfigWithAffinityCached` is **zero-DB by contract** (`store.go:2669-2671` comment; the decorator serves it cache-side `models.go:737`). Flatten reads `m.groups` (in-memory) → **no per-request query**. Group writes swap `m.groups` under `groupsMu.Lock()` → automatic invalidation. **Pin with a test** asserting no `m.store.DB` call on the resolve path.

### d.5 What must NOT change
- **No flatten at read/scan time** (`scanModels` `:932`, `GetModel` `:1028`, `GetModels` `:1223`): the FE GET→PUT round-trips raw refs verbatim (`server.go:1249`/`:1703`); flatten-at-read would rewrite group refs to expanded lists on next save (destroying grouping) and poison the decorator cache. **Raw-in reads, flatten-in resolve/bind.**
- **No group types or values in the selection packages.** `pkg/credentiallb`, `pkg/stickyorder`, `pkg/skipcache`, `pkg/grouplb` are **not touched** — the engine consumes only `st.credentials`/`st.bindings` (engine.go:225-348) fed by the flattened `OnModelChanged`/`RebindFromStore` calls (engine.go:366/:402). `pkg/proxy` and `pkg/ui` **are** touched, but only to read a **flattened `[]models.CredentialRef`** view (§d.2b) — never a group object. The §g AST pin is unchanged and still enforceable.

---

## (e) Decorator-forward list (method-by-method)

**Interfaces:** `models.ModelsConfigInterface` (`config.go:446-477`, 18 methods) + `modelscache.strictSource` (`strict.go:19-34` = 18 + 5 Strict reads + `ResolveInternalConfigWithAffinityCached`). Compile-time assertion in `outage_test.go` (`strict.go:16-18`). Production binds the **decorator** (`cmd/main.go:187`, `CACHE_LAYER_ENABLED` default ON `:170`) → a method added only to the concrete manager **silently no-ops in prod** while raw-manager tests pass. Each row = interface + strictSource + decorator forward + decorator-through test + cache strategy.

| # | Method | Interface | strictSource | Decorator forward (mirror) | Cache strategy | Through-test |
|---|--------|-----------|--------------|----------------------------|----------------|--------------|
| E-1 | `GetCredentialGroup(id) *CredentialGroupConfig` | yes | `GetCredentialGroupStrict(ctx,id) (*CG,error)` | read `c.groupsByID`; **miss → READ-THROUGH to the strict source — NO negative tier** (deviation ratified by the BE deep review, 2026-11-17). **Rationale:** the only production caller is the **admin group-detail handler** (`pkg/ui/server.go:3096`) — the proxy hot path never calls it (the A-2 gates go through `FlattenCredentialRefs`, in-memory under `groupsMu` only; validation uses `GetCredentialGroups()`, the mirror slice). The concrete cost of the missing tier is **ONE extra indexed primary-key round-trip per GET of a NON-EXISTENT group id** — bounded by operator click rate, not LLM traffic. A negative tier would be **WRONG here**: this commission's own doctrine rejects cached staleness that can hide a just-written row — exactly why `ModelsUsingCredentialGroupsAll` was promoted onto the interface — a negative entry would turn a correct **200** into a spurious **404** for the TTL window | cached map (mirror); **NO negative tier** — miss path is read-through to the strict source | fake method + exactly-one delegation |
| E-2 | `GetCredentialGroups() []CredentialGroupConfig` | yes | `GetCredentialGroupsStrict(ctx) ([]CG,error)` | `c.groupsSnap` (mirror `GetCredentials` `models.go:620`) | cached slice | idem |
| E-3 | `AddCredentialGroup(g) error` | yes | — | delegate `inner.AddCredentialGroup` → `deepCopy` → insert `groupsByID` + rebuild `groupsSnap` (mirror `AddModel` `models.go:804-826`; **no** `Internal=true` stamp) | write-through | idem |
| E-4 | `UpdateCredentialGroup(id,g) error` | yes | — | delegate → replace `groupsByID[id]` + rebuild snap (mirror `UpdateModel` `:838-871`; ID immutable → no rename handling) | write-through | idem |
| E-5 | `RemoveCredentialGroup(id) error` | yes | — | delegate → delete `groupsByID[id]` + rebuild snap (mirror `RemoveModel` `:880+`) | write-through | idem |
| E-6 | `ModelsUsingCredentialGroup(groupID) []string` | yes | — | delegate to inner (reverse scan; no cache) | read-through | idem |
| E-7 | `GroupsContainingCredential(credID) []string` | yes | — | delegate to inner | read-through | idem |
| E-8 | `FlattenCredentialRefs(modelID) []CredentialRef` | yes | — | delegate to inner (FE preview) — **and the A-2 proxy/UI flat-view source** (§d.2b) | read-through | idem |

**A-4 — the decorator group cache must be BUILT and PRIMED (prerequisite of E-1/E-2).** `c.groupsByID` / `c.groupsSnap` do not exist today (grep `groupsByID|groupsSnap` in `pkg/modelscache` → zero hits). `strictSnapshot` (`modelscache/models.go:179-198`) reads **only** models/enabled/creds → without extending it, `applySnapshot`, and `reconcileOnce` (≤60s heal), **production `GetCredentialGroups` returns EMPTY forever** (prod binds the decorator — `cmd/main.go:187`, `CACHE_LAYER_ENABLED` default ON `:170`). Build + boot-prime + reconcile-heal = T-25b. The inner `m.groups` stays **AUTHORITATIVE for flattening**; the decorator cache is a **read-only mirror** rebuilt on write-through + reconcile.

**A-5 — the JSON twin must implement the 8 methods as stubs.** `*models.ModelsConfig` is asserted to satisfy `models.ModelsConfigInterface` at `pkg/proxy/race_executor_shadow_reasoning_test.go:92` → without stubs the tree does **not compile**. **RESOLVED (approver iteration-003):** the ui tests do **NOT** construct the JSON twin — they construct a **test-local mock** (`newMockModelsConfigForHandlers()`, `pkg/ui/handlers_decision_test.go:98`); the twin's compile obligation comes from the interface assertion alone — which is exactly why the test-local implementers below must grow the same stubs. **7 of the 8 return empty/nil (the eighth is carved out); `FlattenCredentialRefs` is CARVED OUT — raw/identity fallback (never empty) per T-23d** (the twin rejects group refs per T-10). Scheduled as T-10b (early, WU-2) to avoid a mid-WU compile stall. Re-check the `strict.go:16-18` compile assertion after the interface grows. **Scope completion (approver iteration-001 blocker 1):** the interface growth (18→26 methods at T-24) also breaks ≥4 TEST-LOCAL implementers beyond the JSON twin — three `mockModelsConfig` (`pkg/proxy/normalizers/registry_config_test.go:14`, `pkg/proxy/internal_handler_test.go:39` consumed by ~17 pkg/proxy test files, `pkg/ui/handlers_models_test.go:24`) and `stubModelsConfig` (`pkg/proxy/handle_models_imggen_test.go:27`); `engineBackedResolver` (`race_coordinator_credfailover_test.go:31`) inherits via embedding. T-26-pattern no-op stubs for all of them = **T-10c** (WU-2, before T-24) — without it the compile stall lands at WU-5 in 3 packages and T-10b's "no mid-WU stall" rationale is false. `recordingModelsConfig` (interface-embed) is compile-safe; `fakeStrictSource` is already covered by T-26.

**Decorator-through test pattern:** extend `fakeStrictSource` (`models_test.go:236`/`:327`/`:334`) with E-1..E-8; assert exactly-one delegation per method (`models_test.go:1179` pattern). Also mirror the write-stamp assertions referenced at `ui/handlers_models_write_stamp_test.go:6`.

**Note:** the decorator's *affinity* resolution path needs **no** change — it delegates to `ResolveInternalConfigWithAffinityCached` (`models.go:737`), which flattens store-side (§d.2 #7). The decorator's **legacy 5-tuple** twin (`models.go:750`) **does** need a change — it is self-contained (A-1, §d.2 #10) and flattens cache-side from the A-4 group mirror.

---

## (f) API surface spec (`pkg/ui/server.go`)

Routes registered beside the credentials pair (`server.go:195-197`). **A-12vi — the `{id}` handler registers with the LITERAL trailing slash** `"/fe/api/credential-groups/"` (ServeMux prefix-subtree semantics; precedent `server.go:197`):
```go
mux.HandleFunc("/fe/api/credential-groups", s.handleCredentialGroups)       // GET list, POST create
mux.HandleFunc("/fe/api/credential-groups/", s.handleCredentialGroupDetail) // GET/PUT/DELETE by {id}
```
DTO: `CredentialGroup{ID, Name, Provider, Members []string, UsedBy []string}` — **no secrets** (groups hold refs, not keys).

| Method | Path | Behavior | Success |
|--------|------|----------|---------|
| GET | `/fe/api/credential-groups` | list all **+ server-side `used_by`** (A-10: carried on the LIST as well as the detail, so the FE badge map never derives from the 15s-TTL models cache; cheap at N-group scale). Groups are fetched **uncached**, mirroring the credentials functions | 200 JSON array |
| POST | `/fe/api/credential-groups` | 64KB `MaxBytesReader` (`:2464` precedent); required `id`,`name`,`provider`; `members` optional; `AddCredentialGroup` → `Save()` | 201 + group JSON |
| GET | `/fe/api/credential-groups/{id}` | detail **+ `used_by`** (model IDs) — the used-by surface. **No `/{id}/used-by` route** (dropped; plan-overview I-6 is binding) | 200; 404 bare if absent |
| PUT | `/fe/api/credential-groups/{id}` | id immutable; validate; `UpdateCredentialGroup` → `Save()` | 200 |
| DELETE | `/fe/api/credential-groups/{id}` | in-use → **409**; else `RemoveCredentialGroup` → `Save()` | 204 |

**A-9 — envelope + error mapping (CORRECTION).** The `/fe/api/*` convention is a bare `{"error":"<string>"}` object, but the credentials precedent is **not** uniform: it mixes `http.Error` **text/plain** bodies at `server.go:2529`/`:2538`/`:2654` into the JSON convention. The **new** endpoints must use the bare `{"error"}` JSON envelope at **EVERY** error site — including **405** and **missing-id** — because the FE's `res.json()` throws `SyntaxError` on `text/plain`. Do **not** copy the credentials precedent's `http.Error` calls.
**Status mapping uses `errors.Is` sentinels**, not the legacy `strings.Contains(err.Error(), "in use")` string match: `ConfigError` is a comparable pointer so `errors.Is` works (precedent `server.go:1732`/`:1822-1825`; the sentinel is already wrapped at `store.go:2389`). **The FE branches on STATUS CODES only — never on message text.**

**Validation error mapping:**
| Condition | Status |
|-----------|--------|
| missing required field / malformed JSON / >64KB | 400 |
| validation rejection (provider mismatch, missing member, **missing group ref**, >16, non-chat kind, both-set/neither-set) | 400 |
| group not found (`ErrCredentialGroupNotFound`) | 404 |
| group referenced by ≥1 model (`ErrCredentialGroupInUse`) | **409** |
| credential delete: in group **or** in model | **409** |
| `Save()` failure | 500 |
| wrong method | 405 |
| missing/empty `{id}` in the path | **400** (malformed request; bare `{"error"}` JSON — **not** `http.Error` text/plain) — **canonical contract (approver iteration-002):** a **well-formed** id naming no group → **404**; a **payload** naming a missing group → **400** (validation rejection); an **empty/missing** `{id}` path segment → **400** |

**409 semantics (locked point 4):** block with a clear message naming the referencing models, plus the explicit recovery action "inline members then delete" surfaced to the FE via `used_by` (FE renders the flow — phase2). Never cascade, never warn-and-allow (precedent `RemoveCredential` `store.go:2384-2392`).

**A-11 — `handleTestModel` (`server.go:2053-2078`).** The FE posts an explicit `credential_id`; when that id is a **group id**, expand it to the **first flattened member** and test that member (T-28b). Otherwise the first user-facing flow dead-ends (`GetCredential(groupID)` → 404).

---

## (g) AST/structural pin design

**Chosen variant: full AST semantic walk** (`pkg/proxy/translator/drift_trap_test.go:81-155`: `parser.ParseFile(AllErrors)` + `ast.Inspect` over `*ast.CompositeLit` with type resolution), **plus** a narrower import guard.

**Justification:** `CredentialGroupConfig` lives in `pkg/models` — which **every** package imports — so the import-block guard template (`cache_layer_separation_test.go:722-784`) is **unusable** for it (research-seam §d placement caveat). The AST walk pins the deeper property: **no `models.CredentialGroup*` value is constructed or flowed inside selection code**.

**Target packages (non-test `.go` files):** `pkg/credentiallb`, `pkg/stickyorder`, `pkg/skipcache`, `pkg/proxy`.
**Assertions:**
1. No `models.CredentialGroupConfig` / `models.CredentialGroup*` composite literal or selector expression appears in those dirs.
2. The `CredentialRef.GroupID` field is **read** only in `pkg/models` (definition), `pkg/store/database` (flatten), and `pkg/ui` (API/FE-facing) — pinned by an AST field-selector walk over all non-test `.go` files.
3. Import guard (weaker, still useful): `pkg/credentiallb`, `pkg/stickyorder`, `pkg/skipcache` do **not** import `pkg/store/database` (they must not — the flatten is store-side).

**A-2 does NOT weaken this pin.** The pin forbids `CredentialGroup*` **types/values**, not *accessors*. The A-2 flattened view (§d.2b) hands the proxy/UI gates a **plain `[]models.CredentialRef`** — the same shape as today, with group objects already expanded away — so no group type/value enters `pkg/proxy`/`pkg/credentiallb`/`pkg/stickyorder`/`pkg/skipcache`. The pin text is **unchanged** and remains enforceable; A-2 adds *call sites*, not *types*.

**Exit:** pin test `--- PASS`; a violation fails the build (selection saw a group object).

---

## (h) Backend test matrix (mapped to tasks)

**Column convention:** column 1 = **test id** (this matrix), column 3 = the **implementing task id** from the WU tables above. The two namespaces overlap by id (pre-existing house style, e.g. matrix `T-19` ≠ task `T-19`) — read the column header, not the bare number. Matrix ids run T-1…T-45; task ids run T-1…T-31 plus the suffixed rows T-5b/T-10b/T-10c/T-13b/T-13c/T-18b/T-18c/T-23b/T-23c/T-23d/T-23e/T-25b/T-25c/T-25d/T-25e/T-25f/T-27b/T-28b/T-28c.

| # | Test | Task | Assertion |
|---|------|------|-----------|
| T-1 | Flatten mixed refs | T-19 | `[{c1},{g1},{c2}]` → `[c1, g1.members..., c2]` |
| T-2 | Flatten dedup keep-first | T-19 | `[{c1},{g1(c1,c2)}]`, `g1` lists `c1` first → `[c1,c2]` |
| T-3 | Flatten ordering | T-19 | expansion strictly at the group ref's position |
| T-4 | Empty group | T-19 | contributes nothing; model with one empty group → 0 refs |
| T-5 | **E-3 single group ref** | T-21,T-22 | raw `[{group_id}]` with N members → resolves a member (not 502) |
| T-6 | `==0` legacy degrade | T-21,T-22 | empty flattened list → `ok=false`, no error |
| T-7 | `==1` fast path regression | T-21,T-22 | plain single direct ref → identical `ResolvedCredential` |
| T-8 | `>=2` engine path regression | T-20 | plain 2-ref model → engine `GetOrSelect` |
| T-9 | Same-provider reject | T-8,T-13 | group write + member add reject cross-provider |
| T-10 | Member existence reject | T-8,T-13 | unknown member ID rejected |
| T-11 | Expanded-cap reject | T-9 | expanded >16 rejected; raw ≤16 accepted |
| T-12 | Non-chat kind forbid | T-9 | decision/imggen/group kind + group ref rejected |
| T-13 | Fan-out propagation | T-13,T-20 | add/remove member → every referencing model's engine state fresh (assert via `GetOrSelect` set) |
| T-14 | Deep-copy preservation | T-7 | `GroupID` survives `deepCopyModelConfig` + `BuildModelCopy` |
| T-15 | Delete-while-referenced block | T-13,T-16 | 409 + message names models; inline-then-delete succeeds |
| T-16 | Credential-delete extended guard | T-16 | group-only credential → 409 naming the group |
| T-17 | CRUD API | T-27,T-28 | POST/GET/PUT/DELETE + 400/404/409/405 mapping + 64KB + **bare-JSON body shape at every error site** (A-9) |
| T-18 | Decorator through-tests | T-26 | each of E-1..E-8 delegates exactly once |
| T-19 | AST pin | T-29 | no group value in selection pkgs; `GroupID` read-restricted |
| T-20 | Zero-DB cached resolver | T-23 | no DB call on resolve with groups |
| T-21 | Migration | T-5 | table + indexes exist; idempotent re-run — **SQLite only** (A-6i) |
| T-22 | JSON-twin reject | T-10 | `ModelsConfig.Validate` rejects group refs |
| T-23 | Regression pin (plain list byte-identical) | T-21,T-22 | resolver output for group-free models identical before/after |
| **T-24** | **A-1 — legacy 5-tuple resolver** | T-23b | `ModelsManager.ResolveInternalConfig` returns the **real** provider for a group-primary model (not `""`/`false`) |
| **T-25** | **A-1 — decorator twin + DetectProvider** | T-23c | `CachedModelsConfig.ResolveInternalConfig` (self-contained path) resolves a group-primary model; `normalizers.DetectProvider` (`normalizers/config.go:27`, called per attempt at `race_executor.go:642`) returns the **real** provider, not `"unknown"` — the failure is **silent**, so this pin is the only detector |
| **T-26** | **A-2 — proxy/UI raw-list gates** | T-23d | single-group-ref model → **failover eligible** (`race_coordinator.go:1827`), **budget ≥ members−1** (`:1845`); **reselect of a group member passes `credBelongs`** (`race_executor.go:257`); **reorder runs** (`race_executor.go:309`/`:322` + `preferUnmarkedUpstreamCredential` `:306-340`); rotation gates open (`internal_handler.go:259`, `handler_anthropic.go:876`); MiniMax split gate active (`race_executor.go:59-77`); 9router 400-guard fires (`server.go:1046`/`:1089`/`:1168`/`:1540`/`:1572`/`:1633`) |
| **T-27** | **A-3 — lock discipline** | T-11,T-13,T-15 | group write + concurrent model write do not deadlock (`m.mu` → `groupsMu` order only); `GroupsContainingCredential` called from inside `RemoveCredential`'s `m.mu.Lock()` span does **not** self-deadlock; flatten under `groupsMu.RLock()` is race-clean (no fatal concurrent-map read) |
| **T-28** | **A-4 — decorator group cache primed** | T-25b | after boot the decorator's `GetCredentialGroups` returns the seeded groups (**not** empty); reconcile (≤60s) heals a drift; the decorator's self-contained resolver flattens from the mirror |
| **T-29** | **A-5 — JSON twin stubs compile** | T-10b | `var _ models.ModelsConfigInterface = (*models.ModelsConfig)(nil)` still holds (`race_executor_shadow_reasoning_test.go:92`); **7 stubs return empty/nil; `FlattenCredentialRefs` returns the raw/identity fallback (never empty) per T-23d**; `strict.go:16-18` assertion re-checked |
| **T-38** | **A-5 sweep — test-local implementers compile** (approver iteration-001 blocker 1) | T-10c | `mockModelsConfig` ×3 (`registry_config_test.go:14`, `internal_handler_test.go:39`, `handlers_models_test.go:24`) + `stubModelsConfig` (`handle_models_imggen_test.go:27`) compile against the 26-method interface; `engineBackedResolver` inherits via embedding; `pkg/proxy` + `pkg/ui` test packages build green at T-24 time (no WU-5 stall) |
| **T-30** | **A-6ii — migration loop re-anchor** | T-5b | the three `len(migrations)-1` loops (`migrations_test.go:140`/`:251`/`:522`) exclude 033 and do **not** run 032 into the 028 fixtures; fixtures unmodified |
| **T-31** | **A-7 — shadow column** | T-18b | a group-ref-first model persists `models.credential_id = ""` (**never** the group id); a plain model still persists its primary id |
| **T-32** | **A-8 — fail-closed flatten** | T-23e | a member whose provider diverges from the group's is **skipped + WARN-logged**, not expanded; the remaining members expand; the flat list never mixes providers |
| **T-33** | **A-9 — envelope + status mapping** | T-28 | **every** error site of the new endpoints returns a bare `{"error": "..."}` JSON body (**including 405 and missing-id**) — no `text/plain`; status mapping via `errors.Is` sentinels (400/404/409/405/500) |
| **T-34** | **A-11 — handleTestModel** | T-28b | posting a **group id** to `/fe/api/models/test` expands to the **first flattened member** and returns its result (not 404) |
| **T-35** | **A-14 — copy paths + doc comments** | T-27b,T-7 | `GroupID` survives `deepCopyModelConfig` + `BuildModelCopy`; both copy funcs remain a plain `copy(dst, src)` over the value struct (no field enumeration); doc comments mention `group_id` |
| **T-36** | **A-15 — fan-out completeness** | T-13 | **`AddCredentialGroup` fans out too** (a model referencing the new group via direct SQL / restore / repair gets a fresh flat list); api_key rotation needs **no** fan-out (engine holds refs only); the drain path self-heals (re-reads model + current group state per event) |
| **T-37** | **A-10 — `used_by` on list + detail** | T-28 | `GET /fe/api/credential-groups` and `GET /fe/api/credential-groups/{id}` both carry server-side `used_by`; **no** `/{id}/used-by` route exists |
| **T-41** | **Group reads (strict + non-strict)** | T-12 | `GetCredentialGroup` / `GetCredentialGroups` + their `Strict` twins return the group (id/name/provider/members) and propagate errors; `Strict` variants satisfy the decorator's strict-source contract (mirrors `store.go:2113`/`:2156`/`:2204`/`:2258`) |
| **T-42** | **Provider-change guard on a grouped credential** | T-17 | `UpdateCredential` rejects a provider change when the credential is a member of a group whose provider differs (invariant preserved; error names the group) |
| **T-43** | **C1 — reconcile does not block the request path** (RED-first regression) | T-25c | the regression test is **RED against the current code** and green after the fix: the reconcile tick completes within its bounded 5s ctx, does **not** hold `m.mu` across the DB read, and a live request-path reader (`m.mu.RLock()`) is never queued behind the reconcile writer |
| **T-44** | **O-2 — plain-model persisted shape** | T-13c | a plain credential-list model persists and reads back **byte-identically** (zero-migration guarantee proven by test, not only structurally) |
| **T-45** | **O-9 — DETAIL/LIST mirror agreement** | T-25e | after a strict fill the group appears in **both** DETAIL and LIST (no ≤60s divergence); `groupsSnap` order is stable across write-through and reconcile (O-4) |
| **T-40** | **Group-map reconcile + fail-closed flatten** (approver iteration-002 load-bearing) | T-13b | a missing group id ⇒ **WARN naming the group** + fail-closed (no silent skip ⇒ diagnosable 502); an empty group ⇒ **WARN naming the group** + observable legacy-degrade 502 (never a silent zero-ref resolve); the store map's refresh/reconcile path (or the ≤60s self-heal + **restart-required** rule for direct-SQL writes) is documented in the task |
| **T-39** | **UI mirror group branch** (approver iteration-001 blocker 2) | T-28c | all four paths (`server.go:1017`/`:1511`/`:1863`/`:2018`) accept a group ref — a group-referencing model is creatable/editable/copyable/validatable via the API; rule (a) no longer fires "identifier is empty" for `{credential_id:"",group_id:…}`; missing group / cross-provider still 400 |

**Anti-vacuous:** every ADDED `func Test` must show a literal `--- PASS` (PACKS.md:15). gofmt zero-new-drift via line-set diff vs base `2b987bf` (not hunk overlap). **Addendum closure:** every addendum folded in (A-1, A-2, A-3, A-4, A-5, A-6i, A-6ii, A-7, A-8, A-9, A-11, A-14, A-15) has ≥1 matrix row above; A-10 covered by T-37, A-12(vi) proven by **phase2 T-H (H5)** — the useApi URL-form pin that `{id}` requests reach the registered trailing-slash subtree handler (phase2-plan.md §5) — plus this phase's §f registration spec. **Approver iteration-001 blockers:** (1) A-5 test-local implementer sweep = task T-10c, matrix T-38 (WU-2, before T-24); (2) UI `validateCredentials` group branch = task T-28c, matrix T-39 (all four write/validate paths). **FlattenCredentialRefs stub contract (tie-breaker — ONE contract for the whole corpus):** at EVERY stub site (T-10b JSON twin, T-10c test mocks) `FlattenCredentialRefs` returns the **raw/identity fallback (never empty)** per T-23d; the **other 7** new-method stubs return empty/nil. **Approver iteration-002 load-bearing items:** group-map refresh + fail-closed flatten = task T-13b, matrix T-40 (missing group ⇒ WARN + fail closed; empty group ⇒ WARN + observable 502); list `used_by` in ONE batched pass = T-14; A-2 view DB-read property + 9router group-provider re-key = T-23d; T-28c dependency now includes E-2. No blanket claim that EVERY stub returns empty/nil survives anywhere in the corpus.

---

## (i) Risk register

| # | Risk | Impact | Likelihood | Mitigation |
|---|------|--------|------------|------------|
| 1 | **Decorator trap** — method added to concrete manager but not interface → silent no-op in prod | High | High | enumerate E-1..E-8 method-by-method; decorator-through tests; `outage_test.go` compile assertion |
| 2 | **Engine staleness** on group edits (binding sites are model-write-driven only) | High | Medium | fan-out `OnModelChanged` to every referencing model (§c.3); T-13 |
| 3 | **`RemoveCredential` guard hole** → dangling group member | High | Medium | extend guard to `GroupsContainingCredential`; T-16 |
| 4 | **Deep-copy drops new field** | Medium | Low | representation = `CredentialRef.GroupID` (copied verbatim); T-14 pins both copy paths |
| 5 | **Migration registry trap** — files inert without append | High | Medium | append at `migrate.go:78` + trap comment; T-21 asserts table exists |
| 6 | **Naming collision** — bare "group" owned by model load-balance groups | Medium | High | full "credential group" qualifier in DB/API/FE; AST pin T-19; grep audit |
| 7 | **JSON-twin parity** — second implementation | Medium | Medium | reject group refs at JSON validation; document DB-only; T-22 |
| 8 | **E-3 fast-path misfire** (single group ref → 502) | High | Low | flatten before len-branches; T-5 |
| 9 | **Flatten-at-read destroys FE round-trip** | High | Low | raw-in reads, flatten-in resolve/bind; T-19 + T-3 |
| 10 | **Cap semantics** (expanded >16) | Medium | Medium | validate expanded ≤16 at write; T-11 |
| 11 | **Provider change breaks group invariant** | Medium | Medium | block credential provider change when grouped (§c.4); test |
| 12 | **Zero-DB cached resolver violated** by per-request group query | High | Low | in-memory `m.groups` + swap invalidation; T-20 |
| 13 | **A-2 — proxy/UI raw-list gates defeat the headline behavior silently** (failover budget 0, rotation off, reorder off, MiniMax gate off, 9router guard bypassed) — all paths still return 200 | High | High | flatten at every gate in §d.2b (T-23d); T-26 binds failover-eligibility, budget, `credBelongs`, reorder |
| 14 | **A-1 — legacy 5-tuple resolver pair missed** → provider detection degrades to `"unknown"` (SILENT, not 502) | High | Medium | T-23b/T-23c + T-24/T-25 pins `DetectProvider` returns the real provider |
| 15 | **A-3 — lock-discipline fork**: `m.mu.RLock` inside flatten → self-deadlock; lock-free map → fatal concurrent-map read (process crash on the resolve-hot path) | High | Medium | dedicated `groupsMu` (§c.1); lock order `m.mu` → `groupsMu`; lock-free internal accessor; T-27 |
| 16 | **A-4 — decorator group cache absent/unprimed** → production group reads empty forever (`CACHE_LAYER_ENABLED` default ON) | High | High | build + boot-prime + reconcile-heal (T-25b); T-28 |
| 17 | **A-5 — interface growth breaks the JSON twin's compile assertions AND ≥4 test-local implementers across 3 packages** (hard compile blocker; `registry_config_test.go:14`, `internal_handler_test.go:39`, `handlers_models_test.go:24`, `handle_models_imggen_test.go:27`) | High | Medium | stubs scheduled early: T-10b (JSON twin) + T-10c (test-local sweep), both WU-2 BEFORE T-24; T-29/T-38 |
| 18 | **UI-side `validateCredentials` mirror rejects group refs before store validation is reached** — feature-dead-ending via API (approver iteration-001 blocker 2): all four model write/validate paths 400 "identifier is empty" | High | High (certain if unbuilt) | group branch in the mirror (T-28c) covering all four paths (`server.go:1017`/`:1511`/`:1863`/`:2018`); T-39 |
| 19 | **A-6ii — `len(migrations)-1` loops run 032 into the 028 fixtures** at 33 entries | Medium | High | deliberate re-anchor (T-5b); fixtures NOT weakened; T-30 |
| 20 | **A-7 — shadow column half-valid** (group id bound, or stale after repair) | Medium | Low | group-ref-first writes `credential_id = ""` (T-18b); repair rewrites it via PUT (phase3 T-8.4); T-31 |
| 21 | **A-8 — mixed-provider expansion** (direct-SQL provider edit bypasses the write validator) | Medium | Medium | fail-closed flatten: skip + WARN divergent members (T-23e); T-32 |
| 22 | **A-9 — `text/plain` `http.Error` body** → FE `res.json()` throws `SyntaxError` | Medium | Low | bare `{"error"}` JSON at every error site incl. 405 + missing-id; T-33 |
| 23 | **A-11 — `handleTestModel` **400s** on a group id** → first user-facing flow dead-ends | Medium | Low | expand group id → first flattened member (T-28b); T-34 |
| 24 | **A-14 — a future field-enumeration "fix" breaks `GroupID` survival** (stale doc comments invite it) | Medium | Low | refresh both copy-func doc comments (T-27b); T-35 pins copy behaviour |
| 26 | **C1 — the ≤60s reconcile tick blocks the request path** (writer-preferring `m.mu.Lock()` across an unbounded-context DB read + full models scan + unconditional fan-out; newly introduced vs base `2b987bf`) — latency only (no cooldown defeat: `st.cooldowns`/`st.bindings` are separate maps), but ≈10s at zero groups / ≈90s for one 16-member group under a DB stall | High | Medium | bounded ctx (5s, sibling `StrictFillTimeout`), DB read outside the write mutex, batched `SELECT … WHERE id IN (…)`, conditional fan-out (early-return when equal), logged discarded error, RED-first regression test (T-25c; T-43) |
| 25 | **A-15 — `AddCredentialGroup` without fan-out** leaves direct-SQL / restore / repair rows stale | Medium | Low | fan out on Add too (T-13); T-36 |

---

## (j) Success criteria

| # | Criterion | How to Measure | Threshold |
|---|-----------|----------------|-----------|
| 1 | Flatten correctness | T-1..T-4 unit tests | 100% pass; flattened == manual enumeration over ≥20 fixtures |
| 2 | E-3 trap closed | T-5 | single-group-ref model resolves a member |
| 3 | Selection is group-blind | T-19 AST pin | pin green; zero group values in selection pkgs |
| 4 | Regression: plain list byte-identical | T-7,T-8,T-23 | resolver output identical for group-free models |
| 5 | Group CRUD + fan-out | T-13,T-17 | engine state fresh after member edit; API round-trips |
| 6 | Delete guards | T-15,T-16 | 409 + message names surfaces; inline-then-delete works |
| 7 | Decorator forwards | T-18 | every new method delegates exactly once |
| 8 | Migration | T-21,T-30 | table + indexes exist on **SQLite**; idempotent; the three migration loops exclude 033 |
| 9 | Zero-DB resolve | T-20 | no DB call on cached resolve with groups |
| 10 | Gate | T-30,T-31 | Pack A/B green; gofmt zero-new-drift |
| 11 | **A-1 — no silent provider loss** | T-24,T-25 | `DetectProvider` returns the real provider for a group-primary model (store + decorator-twin paths) |
| 12 | **A-2 — headline behavior works** | T-26 | failover eligible; budget ≥ members−1; `credBelongs` passes for a group member; reorder runs; rotation gates open; 9router guard fires |
| 13 | **A-3 — no deadlock / no crash** | T-27 | group write + model write race clean; no self-deadlock under `m.mu` |
| 14 | **A-4 — decorator cache primed** | T-28 | production decorator returns the seeded groups after boot; reconcile heals drift |
| 15 | **A-5 — tree compiles** | T-29 | interface assertions hold; 8 stubs present |
| 16 | **A-8 — fail-closed** | T-32 | divergent member skipped + WARN; flat list never mixes providers |
| 17 | **A-9 — envelope** | T-33 | every new-endpoint error site returns bare `{"error"}` JSON (incl. 405 + missing-id) |
| 18 | **A-11 — test-model flow** | T-34 | posting a group id tests the first flattened member |
| 19 | **A-14 — copy survival** | T-35 | `GroupID` survives both copy paths; no field enumeration introduced |
| 20 | **A-15 — fan-out completeness** | T-36 | Add fans out; api_key rotation needs none; drain self-heals |

---

**Deferred by design (implementation-time notes — NOT re-planned here):** the ~18-ref cosmetic citation sweep across the phase files — implementers MUST **re-derive every file:line anchor at implementation time** (workers disagree on exact line numbers; the recorded anchors are indicative, not authoritative); **O-1 — the negative tier for `GetCredentialGroup` is a RATIFIED DEVIATION, not an open item** (Amendment 1, §e E-1: admin-only caller, miss = read-through; a negative entry would hide a just-created group for the TTL window → spurious 404); **AST-pin strengthening — accepted-with-documentation for this gate**: the multi-value `:=` hole is real, but the residual failure mode is a **fail-closed 502**, never a cross-credential leak — strengthening via `go/types`+`go/importer` is logged as a project future improvement; **O-3 — `RemoveCredential` write-lock span — deferred DELIBERATELY**: hoisting the scans out of the lock risks a TOCTOU between scan and delete (the guard must see a stable view); **O-10 — FE-side kind filter — owned by the FE lane** (phase2), not this lane; the §i/§j bare task/matrix id references; the T-5b/T-30 re-anchor wording; and one false supporting claim inside the A-5 rationale (to be verified against the code at implementation time, not now).

## Coupling

- **Tight with** WU-4 (flatten seam) — WU-3's helper is consumed at **8 store application sites + 1 decorator site** (9 points; A-1) and at the **A-2 proxy/UI gate list** (§d.2b); WU-5's decorator forwards + group-cache priming (A-4) depend on WU-3's manager methods.
- **Tight with** WU-2 — the JSON-twin stubs (A-5) must land with or before the interface growth in WU-5, or the tree does not compile mid-flight.
- **Tight with** `phase2-plan.md` — the API DTO (`Members`, `used_by` on list+detail), the `errors.Is` status mapping (A-9) and the FE remove-flow consume this phase's routes; the FE label "Credential Groups" and `CredentialGroup*` type prefix are phase2's, but the **wire shape** is fixed here. **A-10: phase2's former `getCredentialGroupUsedBy` references were replaced with the list+detail `used_by` in the integration pass (0 stale refs remain) — keep it that way; the `/{id}/used-by` route does not exist.**
- **Tight with** `phase3-plan.md` — gate/ship consumes the test matrix and gofmt drift; **A-7 cross-ref**: phase3 T-8.4 (the R-1 repair) rewrites the derived shadow `credential_id` via the PUT re-derivation.
- **Loose with** `pkg/credentiallb` / `pkg/stickyorder` / `pkg/skipcache` / `pkg/grouplb` — share the credential-selection domain but **no code change**; coupling is asserted negative (AST pin).
- **Independent of** the JSON-file twin beyond stubs + reject-only (A-5/T-10).

## Exit Criterion

Phase 1 is done when: migration 033 applies idempotently on **SQLite** (A-6i) and the three migration loops are re-anchored for 33 entries (A-6ii); `pkg/models` types+validation compile and pass, and the JSON twin carries the 8 stubs (A-5); manager CRUD + reverse lookups + fan-out pass under the `groupsMu` discipline (A-3) with fan-out on **all** group writes (A-15); the flatten helper is applied at **8 store application sites + 1 decorator site** (9 points; A-1) and every A-2 proxy/UI gate in §d.2b reads the flat list, with the E-3 and zero-DB pins green; the decorator group cache is built, boot-primed and reconcile-healed (A-4); all 8 decorator forwards pass through-tests; the `/fe/api/credential-groups` API passes its matrix incl. the bare-JSON envelope (A-9), list+detail `used_by` (A-10), trailing-slash registration (A-12vi) and the `handleTestModel` expansion (A-11); the AST pin is green and unchanged under A-2 (§g); the full backend matrix (§h) shows a literal `--- PASS` per added test with every folded addendum covered by ≥1 row; and gofmt reports zero new drift vs `2b987bf`. Handoff to `phase3-plan.md` for the gate+ship run.

---

## (k) Addendum → task/matrix-row mapping (fold traceability)

| Addendum | Lands in | One-line delta |
|---|---|---|
| **A-1** | §d.2 rows 8 & 10; §d.3; T-23b, T-23c; T-24, T-25 | seam is now **8 store application sites + 1 decorator site** (9 points; `RemoveModel` `:1834` needs NO change): legacy 5-tuple `ResolveInternalConfig` (`store.go:2444`, flatten before `:2458`) + the self-contained decorator twin (`modelscache/models.go:750`, cache-side flatten); silent failure pinned via `DetectProvider` |
| **A-2** | §d.2b; §d.5; §g; T-23d; T-26 | all 15 raw-list gates (9 rows: 8 behavioural + 1 log-only) (coordinator `:1827`/`:1845`, executor `:257`/`:309`/`:322`/`:306-340`/`:59-77`, `internal_handler.go:259`, `handler_anthropic.go:876`, `server.go:1046`/`:1089`/`:1168`/`:1540`/`:1572`/`:1633`) consume a flattened list; AST pin unchanged (types/values still forbidden) |
| **A-3** | §c.1, §c.3, §c.5; T-11, T-15; T-27 | dedicated `groupsMu sync.RWMutex`; flatten RLock everywhere; fan-out inside the `m.mu` write span; lock-free internal membership accessor |
| **A-4** | §e (A-4 para); T-25b; T-28 | decorator `groupsByID`/`groupsSnap` built + wired into `strictSnapshot`/`applySnapshot`/`reconcileOnce`; inner `m.groups` authoritative |
| **A-5** | §e (A-5 para); §Out of Scope; T-10b; T-29 | JSON twin grows 8 stub interface methods (compile blocker), scheduled early — `FlattenCredentialRefs` carved out (raw/identity fallback, never empty; T-23d); the other 7 return empty/nil |
| **A-6i** | §a notes; T-5; T-21 | migration test is **SQLite-only** (no `pg_integration` twin) |
| **A-6ii** | T-5b; T-30 | the three `len(migrations)-1` loops re-anchored for 33 entries; fixtures not weakened |
| **A-7** | §c.7; T-18b; T-31 | shadow `models.credential_id` stays `PrimaryCredentialID()`-derived → **empty** for group-ref-first models; never the group id |
| **A-8** | §d.1; T-23e; T-32 | flatten fails closed: only same-provider members expand; divergent member skipped + WARN |
| **A-9** | §f (A-9 para); T-28; T-33 | bare `{"error"}` JSON at **every** error site incl. 405 + missing-id; `errors.Is` sentinels; FE branches on status only |
| **A-10** | §f (list + detail rows); T-37 | `used_by` carried on **list and detail** server-side; no `/{id}/used-by` route (phase2's stale refs swept at the integration pass — **0 remain**, grep-verified) |
| **A-11** | §f (A-11 para); T-28b; T-34 | `handleTestModel` (`server.go:2053-2078`) expands a group id → first flattened member |
| **A-12(vi)** | §f route block; T-27 | `{id}` handler registers with the literal trailing slash `"/fe/api/credential-groups/"` |
| **A-14** | §Locked representation (preserved); T-27b; T-35 | copy paths stay `copy(dst, src)` over the value struct → `GroupID` survives with zero edits; only the stale doc comments are refreshed |
| **A-15** | §c.2, §c.3, §c.6; T-13; T-36 | `AddCredentialGroup` fans out too; **no** credential-UPDATE refresh chain is assumed (api_key rotation needs none); drain path self-heals |
| **M-1…M-5** | §2 of the architecture doc | rollback/repair runbook — **phase3 scope** (cross-ref `phase3-plan.md` T-8.4/T-8.5); no phase1 code impact |
