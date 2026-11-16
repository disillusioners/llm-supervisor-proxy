# Phase 1: Backend — Credential Groups (persistence, flattening seam, manager, API)

Date: 2026-11-16
Author: planner[v2] via plan-creation worker (backend lane)
Status: Draft
Base: `feature/credential-groups` @ `2b987bf` (= origin/latest) — worktree `llm-supervisor-proxy-wt-cred-groups`
Cross-references: `phase2-plan.md` (frontend), `phase3-plan.md` (gate+ship) — written in parallel; do not wait.
Research inputs: `research-seam.md`, `research-store-api.md` (this dir). All file:line citations below re-verified against this worktree.

---

## Objective

Add **credential groups** — a named, same-provider bundle of existing credentials — as a **management-only** entity that models may reference alongside direct credential refs. At model-resolution the reference list is **flattened in place** (group expands at its position, dedup keep-first) so that credential **selection never knows groups exist**. Backend delivers: migration, types+validation, manager CRUD + reverse lookups + engine fan-out, the single flattening seam, decorator forwards, and the `/fe/api/credential-groups` API.

**Done =** a model whose `credentials_json` mixes direct refs and group refs resolves to exactly the same flat credential list as a manually-enumerated model; group membership edits propagate to every referencing model's engine state immediately; deletion-while-referenced is blocked (409); and a plain (group-free) model behaves byte-identically to today.

## Scope

### In Scope
- Migration `033_add_credential_groups` (sqlite + postgres + registry append + `.down`).
- `pkg/models`: `CredentialGroupConfig` type, sentinels, validation (group-level + model-ref-level), `CredentialRef.GroupID` extension.
- `pkg/store/database`: manager CRUD, in-memory group cache, reverse lookups, engine fan-out, events, extended `RemoveCredential` guard, the flattening helper + its 8 call sites, resolver branch rework.
- `pkg/modelscache`: interface + `strictSource` additions, decorator forwards, cache strategy, decorator-through tests.
- `pkg/ui/server.go`: `/fe/api/credential-groups` + `/{id}` routes/handlers, error mapping.
- Structural pins (AST walk + import guard) and the backend test matrix.

### Out of Scope
- **Frontend** (types, tab, form, editor, remove-flow UI) — `phase2-plan.md`.
- **Gate/ship** (Pack A/B run, gofmt drift, commit/promotion, deploy) — `phase3-plan.md`.
- **Selection-engine changes** (`pkg/credentiallb`, `pkg/stickyorder`, `pkg/skipcache`, `pkg/grouplb`, `pkg/proxy` selection code) — explicitly NOT touched; groups are flattened away before they arrive. Evidence: `credentiallb.Engine.GetOrSelect` consumes only `st.credentials`/`st.bindings` (engine.go:225-348) fed by `OnModelChanged`/`RebindFromStore` (engine.go:366/:402); stickyorder keys by model ID; skipcache marks `(credential, provider, model)` post-resolution (race_executor.go:527-528, handler_anthropic.go:788).
- **A 5th model kind** — `kind` stays 4-wide (`config.go:96-132`); a credential group is a new top-level entity, not a model row.
- **Group-in-group / nesting** — impossible by schema+API (members are credential IDs only).
- **JSON-file config parity** — credential groups are **DB-backed only**; the JSON twin rejects group refs (spec §b).

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
- **No new `ModelConfig` field** → the two explicit-field deep-copy paths (`pkg/modelscache/copy.go:54` `deepCopyModelConfig`, `pkg/models/copy.go:102-130` `BuildModelCopy`) already copy `Credentials` verbatim via `copyCredentialRefs` (`pkg/modelscache/copy.go:24`, `pkg/models/copy.go:174`) → `GroupID` survives with **zero** copy-code edits. This **defuses** the deep-copy-drop trap (research-seam §e.7) rather than patching it. A regression test still pins it (§h T-14).
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
| T-3 | Write both `.down.sql` (`DROP TABLE IF EXISTS credential_groups`) | T-1,T-2 | matches 022+ down-file convention |
| T-4 | Append `{"033", "033_add_credential_groups.up"}` to the registry slice at `migrate.go:78` **with the Amendment-1 trap comment** | T-1,T-2 | registry len 33; latest = 033; trap comment present |
| T-5 | Add migration test (sqlite always; PG-gated twin) asserting table + indexes exist after `Initialize` and that a second `Initialize` is idempotent | T-4 | `--- PASS` literal; idempotent re-run |

### WU-2 — `pkg/models` types + validation

| # | Task | Depends On | Acceptance |
|---|------|------------|------------|
| T-6 | New `pkg/models/credential_group.go`: `CredentialGroupConfig{ID,Name,Provider,Members []string,CreatedAt,UpdatedAt}`, `MaxCredentialGroupMembers = 16` (mirror `group.go:25`), sentinels `ErrCredentialGroupNotFound`, `ErrCredentialGroupInUse`, `ErrCredentialGroupMemberNotFound`, `ErrCredentialGroupProviderMismatch`, `ErrCredentialGroupMemberDuplicate`, `ErrCredentialGroupMemberEmpty` (all `&ConfigError{}` per `credential.go:177-178`) | T-4 | compiles; sentinels are `errors.Is`-matchable |
| T-7 | Extend `CredentialRef` with `GroupID` (representation above) + `IsGroupRef()` helper | none | `config.go:331-335` compiles |
| T-8 | `ValidateCredentialGroup(g, credLookup)` — field + collection rules (§b) | T-6 | rejects empty id/name, unknown provider, >16 members, dup members, missing member, provider mismatch |
| T-9 | `ValidateCredentialRefs(refs, groups, creds)` — model-ref rules with group branch (§b) | T-7,T-8 | rejects both-set/neither-set, missing group, non-chat kind + group ref, expanded >16 |
| T-10 | JSON-twin reject: `pkg/models/config.go` validation (`Validate` / credential-ref path) returns a clear error when `GroupID != ""` | T-7 | `ModelsConfig.Validate` rejects group refs; test pins DB-only |

### WU-3 — Manager CRUD + reverse lookups + group cache + fan-out + events

| # | Task | Depends On | Acceptance |
|---|------|------------|------------|
| T-11 | New `pkg/store/database/credential_group.go`: `ModelsManager` gains `groups map[string]models.CredentialGroupConfig` (rebuilt in `NewModelsManager` store.go:765-814 from a new `GetAllCredentialGroups` scan) | T-6 | manager constructs; map populated at boot |
| T-12 | `GetCredentialGroup` / `GetCredentialGroups` (legacy reads, mirror `store.go:2113`/`:2156`) + `GetCredentialGroupStrict` / `GetCredentialGroupsStrict` (mirror `:2204`/`:2258`) | T-11 | `--- PASS`; strict variants propagate errors |
| T-13 | `AddCredentialGroup` / `UpdateCredentialGroup` / `RemoveCredentialGroup` — validate, dialect-aware SQL via `m.qb`, mutate `m.groups` under `m.mu`, then fan-out (§c) | T-11,T-12 | CRUD round-trips; fan-out fires |
| T-14 | `ModelsUsingCredentialGroup(groupID) []string` reverse lookup over `models.credentials_json` (precedent `RemoveCredential` scan `store.go:2384-2392`) | T-11 | returns referencing model IDs, ordered |
| T-15 | `GroupsContainingCredential(credID) []string` membership lookup | T-11 | returns group IDs containing the credential |
| T-16 | Extend `RemoveCredential` guard (`store.go:2380-2418`): block (409-worthy error) when the credential is a member of any group **or** referenced by any model; message names **both** surfaces | T-15 | group-only credential delete blocked; message lists group + model |
| T-17 | `UpdateCredential` provider-change guard: block provider change when the credential is a member of a group whose provider differs (refinement, §c.4) | T-15 | provider change on a grouped credential rejected |
| T-18 | `FlattenCredentialRefs(modelID) []models.CredentialRef` exported preview accessor (FE display; phase2) | T-19 | returns flattened list; empty for unknown model |

### WU-4 — The flattening seam

| # | Task | Depends On | Acceptance |
|---|------|------------|------------|
| T-19 | Implement unexported helper `flattenedCredentialRefs(model *models.ModelConfig) []models.CredentialRef` (algorithm §d.1) reading `m.groups` (in-memory) | T-11,T-7 | unit tests: mixed refs, dedup keep-first, ordering, empty group |
| T-20 | Apply helper at the **5 engine-binding sites**: `store.go:789`, `:867`, `:1632`, `:1768` (pass flattened refs); `:1834` stays `nil` | T-19 | engine receives flat lists |
| T-21 | Apply helper at the **top** of `ResolveInternalConfigWithAffinity` (`store.go:2543`) — flatten **before** the `len==0`/`==1`/`>=2` branches; fast path reads `refs[0].CredentialID` | T-19 | E-3 single-group-ref resolves N members |
| T-22 | Apply helper at the **top** of `ResolveInternalConfigWithAffinityCached` (`store.go:2679`) — same flatten-before-branch rule | T-19 | cached variant resolves single-group-ref; zero DB reads |
| T-23 | Zero-DB contract pin: assert the cached resolver performs **no** DB query when flattening (group map in-memory) | T-22 | test green; no `m.store.DB` call on the resolve path |

### WU-5 — Decorator forwards (`pkg/modelscache`)

| # | Task | Depends On | Acceptance |
|---|------|------------|------------|
| T-24 | Add the 8 methods (§e) to `models.ModelsConfigInterface` (`config.go:446-477`) and the Strict twins to `modelscache.strictSource` (`strict.go:19-34`) | T-12,T-13,T-14,T-15,T-18 | both interfaces compile; `outage_test.go` assertion holds |
| T-25 | Decorator forwards + cache strategy for all 8 (mirror `AddModel` `models.go:804-826` delegate-then-swap; **no** `Internal=true` stamp) | T-24 | `--- PASS` |
| T-26 | Decorator-through tests: extend `fakeStrictSource` (`models_test.go:236`/`:327`/`:334`) with the new methods; assert exactly-one delegation (`models_test.go:1179` pattern) for each | T-25 | one delegation per method |

### WU-6 — API surface (`pkg/ui/server.go`)

| # | Task | Depends On | Acceptance |
|---|------|------------|------------|
| T-27 | Register `GET|POST /fe/api/credential-groups` and `GET|PUT|DELETE /fe/api/credential-groups/{id}` beside the credentials pair (`server.go:195-197`) | T-13 | routes reachable |
| T-28 | Handlers `handleCredentialGroups` / `handleCredentialGroupDetail` (§f): 64KB cap, required fields, `used_by` in detail, bare `{"error":...}` envelope, status mapping | T-13,T-14 | API tests green |

### WU-7 — Structural pins + backend test matrix + gate handoff

| # | Task | Depends On | Acceptance |
|---|------|------------|------------|
| T-29 | AST/structural pin (§g): full AST walk + import guard | T-19 | pin test `--- PASS` |
| T-30 | Backend test matrix (§h) implemented across `pkg/store/database`, `pkg/models`, `pkg/modelscache`, `pkg/ui` | T-20..T-29 | every added `func Test` shows literal `--- PASS` |
| T-31 | gofmt zero-new-drift on all touched files (line-set diff vs base `2b987bf`) | T-30 | no new flagged lines |

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
**`.down.sql` (both):** `DROP TABLE IF EXISTS credential_groups;` (022+ convention; no down-runner exists — rollback = restore backup).

**Registry append (`migrate.go:78`):**
```go
    // Amendment-1 trap (fifth occurrence): this registry is an ordered
    // slice, NOT a directory scanner; this append activates the 033 SQL
    // files. Without this append the 033 files are silently inert and
    // credential groups are unavailable.
    {"033", "033_add_credential_groups.up"},
```
**No `models` column change** (representation §Locked) → no backfill, no data migration. Additive `CREATE TABLE IF NOT EXISTS` + index is house style. SQLite runner's defensive `ROLLBACK` (`migrate.go:113-133`) is load-bearing (modernc sqlite has no mid-`BEGIN` auto-rollback).

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

**JSON-twin reject:** `pkg/models/config.go` `Validate` / credential-ref path returns a clear error when `GroupID != ""` ("credential groups are supported only in the database config backend"). Credential groups are **DB-backed only**; the JSON twin (`config.go:1189`/`:1253`, no engine, single-credential, legacy/test lane) never flattens. Document in the plan + code comment.

---

## (c) Manager CRUD + reverse lookups + fan-out + events (`pkg/store/database`)

New file `credential_group.go` (house style: `store_group_rewrite.go`, `store_group_test.go`). All SQL via dialect-aware `m.qb`; all writes under `m.mu` (`sync.RWMutex`, struct `store.go:696-728`).

**c.1 Group state cache.** `ModelsManager.groups map[string]models.CredentialGroupConfig`, rebuilt at `NewModelsManager` (`store.go:765-814`) via a new `GetAllCredentialGroups()` scan; mutated on group writes under `m.mu`. This is what makes flatten **zero-DB** (§d.4).

**c.2 CRUD** (mirror credential CRUD `store.go:2302`/`:2332`/`:2380`):
- `AddCredentialGroup(g)`: validate → `INSERT` → `m.groups[g.ID] = g` → **no fan-out** (unreferenced at creation).
- `UpdateCredentialGroup(id, g)`: ID immutable (`ErrCannotChangeCredentialID` precedent) → validate → `UPDATE` → swap `m.groups[id]` → **fan-out**.
- `RemoveCredentialGroup(id)`: **in-use guard FIRST** — `ModelsUsingCredentialGroup(id)` non-empty → `ErrCredentialGroupInUse` naming the referencing models (block, never cascade — precedent `RemoveCredential` `store.go:2384-2392`) → `DELETE` → delete `m.groups[id]` → no fan-out (unreferenced by construction).

**c.3 Fan-out (the engine-staleness fix).** On any group write that changes a referenced group's membership (update/member add/remove), for **every** model in `ModelsUsingCredentialGroup(groupID)`:
```go
refs := m.flattenedCredentialRefs(model)   // fresh flat list
m.engine.OnModelChanged(model.ID, refs)    // engine.go:366
m.publishCredentialsChanged(model.ID)      // store.go:2425-2434 (nil-bus tolerant)
```
The reverse lookup **is** the fan-out list. Without it, selection runs stale until restart (research-seam §e.3). Precedent for write-time fan-out: `rebuildClaimsLocked` on every model write (`store.go:901-906`).

**c.4 Credential-write guards (refinements, with rationale).**
- `RemoveCredential` (`store.go:2380-2418`): extend the in-use scan to also consult `GroupsContainingCredential(id)`; block with a message naming **both** surfaces ("credential 'x' is in use by model 'y' and group 'z'"). Closes the guard hole where a group-only credential escapes → dangling member (research-seam §e.4).
- `UpdateCredential` provider change: block when the credential is a member of a group whose provider differs — otherwise the same-provider invariant silently breaks. (Refinement; rationale: invariant preservation. Message names the group.)

**c.5 Reverse lookups.**
- `ModelsUsingCredentialGroup(groupID) []string` — scan `models.credentials_json` for `GroupID == groupID` (precedent `store.go:2384-2392`).
- `GroupsContainingCredential(credID) []string` — scan `m.groups` membership.
- `FlattenCredentialRefs(modelID) []models.CredentialRef` — exported preview accessor for the FE (phase2).

**c.6 Events.** Reuse `publishCredentialsChanged` (`store.go:2425-2434`); the bus-drain refresh (`drainCredentialsChanged` `store.go:843`) already re-binds via `store.go:867` → now flattened. No new event type.

---

## (d) THE FLATTENING SEAM spec

### d.1 The one helper
```go
// pkg/store/database/credential_group.go
func (m *ModelsManager) flattenedCredentialRefs(model *models.ModelConfig) []models.CredentialRef
```
Algorithm (deterministic, O(total refs + members), in-memory):
1. `model == nil || len(model.Credentials) == 0` → `nil`.
2. Walk `model.Credentials` **in order**. For each ref:
   - **direct** (`GroupID == ""`): append `ref`.
   - **group** (`GroupID != ""`): look up `m.groups[ref.GroupID]`; if absent → **skip** (defensive; validation prevents). For each `memberID` in `group.Members` (stored order): append `models.CredentialRef{CredentialID: memberID, Weight: ref.Weight, Position: ref.Position}`.
3. **Dedup keep-first** by `CredentialID` across the whole list (first occurrence wins; later duplicates dropped).
4. Return.

`Weight` inherited from the group ref (locked). `Position` = the group ref's `Position` (cosmetic; not selector-relevant — evidence `selector.go:28` uses `Weight`; `Position` only written by UI `server.go:753`).

### d.2 The 8 call sites (all in `pkg/store/database/store.go`)
| # | Site | Line | Change |
|---|------|------|--------|
| 1 | Startup rebind | `store.go:789` | `m.engine.RebindFromStore(mc.ID, m.flattenedCredentialRefs(&mc))` |
| 2 | Bus-drain refresh | `store.go:867` | `m.engine.OnModelChanged(modelID, m.flattenedCredentialRefs(&mc))` |
| 3 | AddModel | `store.go:1632` | `m.engine.OnModelChanged(model.ID, m.flattenedCredentialRefs(&model))` |
| 4 | UpdateModel | `store.go:1768` | `m.engine.OnModelChanged(model.ID, m.flattenedCredentialRefs(&model))` |
| 5 | RemoveModel | `store.go:1834` | **no change** (`nil` is group-free) |
| 6 | `ResolveInternalConfigWithAffinity` | `store.go:2543` | flatten at top; `len==0`/`==1`/`>=2` read the **flattened** slice; fast path uses `refs[0].CredentialID` |
| 7 | `ResolveInternalConfigWithAffinityCached` | `store.go:2679` | same flatten-before-branch rule; `resolveSingle(refs[0].CredentialID)` |
| 8 | (c.3) group-write fan-out | new | `OnModelChanged(modelID, flattened)` |

### d.3 Flatten-before-len-branches rule + E-3 trap
The flattened slice is computed **before** the `len(...)==0` / `==1` / `>=2` branches in **both** resolvers. This is load-bearing: a model whose raw list is a **single group ref** would otherwise take the `==1` fast path (`store.go:2588`/`:2729`) and resolve the **group's ID** as a credential → credentials-table miss → `ok=false` → **502**. **Pin with a test** (§h T-2): raw `[{group_id:"g1"}]`, `g1` has 2 members → resolution returns one of the 2 members.

### d.4 Zero-DB cached-resolver contract
`ResolveInternalConfigWithAffinityCached` is **zero-DB by contract** (`store.go:2669-2671` comment; the decorator serves it cache-side `models.go:737`). Flatten reads `m.groups` (in-memory) → **no per-request query**. Group writes swap `m.groups` under `m.mu` → automatic invalidation. **Pin with a test** asserting no `m.store.DB` call on the resolve path.

### d.5 What must NOT change
- **No flatten at read/scan time** (`scanModels` `:932`, `GetModel` `:1028`, `GetModels` `:1223`): the FE GET→PUT round-trips raw refs verbatim (`server.go:1249`/`:1703`); flatten-at-read would rewrite group refs to expanded lists on next save (destroying grouping) and poison the decorator cache. **Raw-in reads, flatten-in resolve/bind.**
- **No changes** in `pkg/proxy`, `pkg/credentiallb`, `pkg/stickyorder`, `pkg/skipcache`, `pkg/grouplb`, `pkg/modelscache` resolution — one seam covers all 8 runtime entry points (research-seam §b).

---

## (e) Decorator-forward list (method-by-method)

**Interfaces:** `models.ModelsConfigInterface` (`config.go:446-477`, 18 methods) + `modelscache.strictSource` (`strict.go:19-34` = 18 + 5 Strict reads + `ResolveInternalConfigWithAffinityCached`). Compile-time assertion in `outage_test.go` (`strict.go:16-18`). Production binds the **decorator** (`cmd/main.go:187`, `CACHE_LAYER_ENABLED` default ON `:170`) → a method added only to the concrete manager **silently no-ops in prod** while raw-manager tests pass. Each row = interface + strictSource + decorator forward + decorator-through test + cache strategy.

| # | Method | Interface | strictSource | Decorator forward (mirror) | Cache strategy | Through-test |
|---|--------|-----------|--------------|----------------------------|----------------|--------------|
| E-1 | `GetCredentialGroup(id) *CredentialGroupConfig` | yes | `GetCredentialGroupStrict(ctx,id) (*CG,error)` | read `c.groupsByID`; miss → negative-cache (mirror `GetCredential` `models.go:580`) | cached map + neg | fake method + exactly-one delegation |
| E-2 | `GetCredentialGroups() []CredentialGroupConfig` | yes | `GetCredentialGroupsStrict(ctx) ([]CG,error)` | `c.groupsSnap` (mirror `GetCredentials` `models.go:620`) | cached slice | idem |
| E-3 | `AddCredentialGroup(g) error` | yes | — | delegate `inner.AddCredentialGroup` → `deepCopy` → insert `groupsByID` + rebuild `groupsSnap` (mirror `AddModel` `models.go:804-826`; **no** `Internal=true` stamp) | write-through | idem |
| E-4 | `UpdateCredentialGroup(id,g) error` | yes | — | delegate → replace `groupsByID[id]` + rebuild snap (mirror `UpdateModel` `:838-871`; ID immutable → no rename handling) | write-through | idem |
| E-5 | `RemoveCredentialGroup(id) error` | yes | — | delegate → delete `groupsByID[id]` + rebuild snap (mirror `RemoveModel` `:880+`) | write-through | idem |
| E-6 | `ModelsUsingCredentialGroup(groupID) []string` | yes | — | delegate to inner (reverse scan; no cache) | read-through | idem |
| E-7 | `GroupsContainingCredential(credID) []string` | yes | — | delegate to inner | read-through | idem |
| E-8 | `FlattenCredentialRefs(modelID) []CredentialRef` | yes | — | delegate to inner (FE preview) | read-through | idem |

**Decorator-through test pattern:** extend `fakeStrictSource` (`models_test.go:236`/`:327`/`:334`) with E-1..E-8; assert exactly-one delegation per method (`models_test.go:1179` pattern). Also mirror the write-stamp assertions referenced at `ui/handlers_models_write_stamp_test.go:6`.

**Note:** the decorator's *resolution* path needs **no** change — it delegates to `ResolveInternalConfigWithAffinityCached` (`models.go:737`), which flattens store-side (§d.2 #7). Only the 8 new methods need forwards.

---

## (f) API surface spec (`pkg/ui/server.go`)

Routes registered beside the credentials pair (`server.go:195-197`):
```go
mux.HandleFunc("/fe/api/credential-groups", s.handleCredentialGroups)       // GET list, POST create
mux.HandleFunc("/fe/api/credential-groups/", s.handleCredentialGroupDetail) // GET/PUT/DELETE by {id}
```
DTO: `CredentialGroup{ID, Name, Provider, Members []string, UsedBy []string}` — **no secrets** (groups hold refs, not keys).

| Method | Path | Behavior | Success |
|--------|------|----------|---------|
| GET | `/fe/api/credential-groups` | list all (no `used_by` — perf) | 200 JSON array |
| POST | `/fe/api/credential-groups` | 64KB `MaxBytesReader` (`:2464` precedent); required `id`,`name`,`provider`; `members` optional; `AddCredentialGroup` → `Save()` | 201 + group JSON |
| GET | `/fe/api/credential-groups/{id}` | detail **+ `used_by`** (model IDs) — the used-by surface | 200; 404 bare if absent |
| PUT | `/fe/api/credential-groups/{id}` | id immutable; validate; `UpdateCredentialGroup` → `Save()` | 200 |
| DELETE | `/fe/api/credential-groups/{id}` | in-use → **409**; else `RemoveCredentialGroup` → `Save()` | 204 |

**Envelope:** `/fe/api/*` errors are **always** bare `{"error":"<string>"}` (verified at every credentials error site `:2470`/`:2478`/`:2484`/`:2490`/`:2505`/`:2513`/`:2548`/`:2570`/`:2608`/`:2639`/`:2647`).

**Validation error mapping:**
| Condition | Status |
|-----------|--------|
| missing required field / malformed JSON / >64KB | 400 |
| validation rejection (provider mismatch, missing member, >16, non-chat kind, both-set/neither-set) | 400 |
| group not found | 404 |
| group referenced by ≥1 model (`ErrCredentialGroupInUse`) | **409** |
| credential delete: in group **or** in model | **409** |
| `Save()` failure | 500 |
| wrong method | 405 |

**409 semantics (locked point 4):** block with a clear message naming the referencing models, plus the explicit recovery action "inline members then delete" surfaced to the FE via `used_by` (FE renders the flow — phase2). Never cascade, never warn-and-allow (precedent `RemoveCredential` `store.go:2384-2392`).

---

## (g) AST/structural pin design

**Chosen variant: full AST semantic walk** (`pkg/proxy/translator/drift_trap_test.go:81-155`: `parser.ParseFile(AllErrors)` + `ast.Inspect` over `*ast.CompositeLit` with type resolution), **plus** a narrower import guard.

**Justification:** `CredentialGroupConfig` lives in `pkg/models` — which **every** package imports — so the import-block guard template (`cache_layer_separation_test.go:722-784`) is **unusable** for it (research-seam §d placement caveat). The AST walk pins the deeper property: **no `models.CredentialGroup*` value is constructed or flowed inside selection code**.

**Target packages (non-test `.go` files):** `pkg/credentiallb`, `pkg/stickyorder`, `pkg/skipcache`, `pkg/proxy`.
**Assertions:**
1. No `models.CredentialGroupConfig` / `models.CredentialGroup*` composite literal or selector expression appears in those dirs.
2. The `CredentialRef.GroupID` field is **read** only in `pkg/models` (definition), `pkg/store/database` (flatten), and `pkg/ui` (API/FE-facing) — pinned by an AST field-selector walk over all non-test `.go` files.
3. Import guard (weaker, still useful): `pkg/credentiallb`, `pkg/stickyorder`, `pkg/skipcache` do **not** import `pkg/store/database` (they must not — the flatten is store-side).

**Exit:** pin test `--- PASS`; a violation fails the build (selection saw a group object).

---

## (h) Backend test matrix (mapped to tasks)

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
| T-17 | CRUD API | T-27,T-28 | POST/GET/PUT/DELETE + 400/404/409/405 mapping + 64KB |
| T-18 | Decorator through-tests | T-26 | each of E-1..E-8 delegates exactly once |
| T-19 | AST pin | T-29 | no group value in selection pkgs; `GroupID` read-restricted |
| T-20 | Zero-DB cached resolver | T-23 | no DB call on resolve with groups |
| T-21 | Migration | T-5 | table + indexes exist; idempotent re-run |
| T-22 | JSON-twin reject | T-10 | `ModelsConfig.Validate` rejects group refs |
| T-23 | Regression pin (plain list byte-identical) | T-21,T-22 | resolver output for group-free models identical before/after |

**Anti-vacuous:** every ADDED `func Test` must show a literal `--- PASS` (PACKS.md:15). gofmt zero-new-drift via line-set diff vs base `2b987bf` (not hunk overlap).

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
| 8 | Migration | T-21 | table + indexes exist on sqlite + PG; idempotent |
| 9 | Zero-DB resolve | T-20 | no DB call on cached resolve with groups |
| 10 | Gate | T-30,T-31 | Pack A/B green; gofmt zero-new-drift |

---

## Coupling

- **Tight with** WU-4 (flatten seam) — WU-3's helper is consumed at 8 store.go sites; WU-5's decorator forwards depend on WU-3's manager methods.
- **Tight with** `phase2-plan.md` — the API DTO (`Members`, `used_by`) and the FE remove-flow consume this phase's routes; the FE label "Credential Groups" and `CredentialGroup*` type prefix are phase2's, but the **wire shape** is fixed here.
- **Tight with** `phase3-plan.md` — gate/ship consumes the test matrix and gofmt drift.
- **Loose with** `pkg/credentiallb` / `pkg/stickyorder` / `pkg/skipcache` / `pkg/grouplb` — share the credential-selection domain but **no code change**; coupling is asserted negative (AST pin).
- **Independent of** the JSON-file twin (explicitly out of scope; reject-only).

## Exit Criterion

Phase 1 is done when: migration 033 applies idempotently on both dialects; `pkg/models` types+validation compile and pass; manager CRUD + reverse lookups + fan-out pass; the flatten helper is applied at all 8 sites with the E-3 and zero-DB pins green; all 8 decorator forwards pass through-tests; the `/fe/api/credential-groups` API passes its matrix; the AST pin is green; the full backend matrix (§h) shows a literal `--- PASS` per added test; and gofmt reports zero new drift vs `2b987bf`. Handoff to `phase3-plan.md` for the gate+ship run.
