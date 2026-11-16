# Research: STORE / SCHEMA / API / NAMING — credential-groups commission

_Worktree `llm-supervisor-proxy-wt-cred-groups`, branch `feature/credential-groups`, base `2b987bf` (= origin/latest). Read-only. All citations file:line verified against this worktree 2026-11-16._

---

## (a) PERSISTENCE + MIGRATIONS

### How migrations run

- **Embedded twin SQL trees**: `pkg/store/database/migrate.go:11-12` (`//go:embed migrations/sqlite/*.sql`) and `:14-15` (`//go:embed migrations/postgres/*.sql`).
- **Registry is an ordered slice, NOT a directory scanner**: `var migrations = []migration{...}` at `migrate.go:24-78`. **32 entries; current latest = `{"032", "032_internal_only_data.up"}` (`migrate.go:77`).**
- ⚠️ **Amendment-1 trap (documented 4× in-code)**: appending the registry entry is what ACTIVATES a migration. `migrate.go:53-57` (029), `:59-63` (030), `:64-69` (031), `:70-77` (032) each carry a comment: "this registry is an ordered slice, NOT a directory scanner; without this append the 0NN SQL files are silently inert." A new `033` must add BOTH the file pair AND the registry entry.
- **Execution**: `RunMigrations` (`migrate.go:81-94`) iterates the slice; per entry: `ensureMigrationsTable` (`:222-250`, creates `schema_migrations(version TEXT PK, applied_at)` — TIMESTAMPTZ on PG) → `isMigrationApplied` (`:169-194`, dialect-aware `$1` vs `?`) → `loadMigrationSQL` (`:143-166`, dialect dir switch) → `DB.ExecContext(sql)`.
- **Rollback-safety**: on exec error the runner issues a defensive `ROLLBACK` (`migrate.go:113-133`, "Phase-1 S1 hardening") because `modernc.org/sqlite v1.46.1` does NOT auto-rollback a multi-statement ExecContext that fails mid-way; partial file-level `BEGIN...COMMIT` state is rolled back, and the ROLLBACK error is deliberately discarded. The original migration error is returned; `recordMigration` (`:197-219`) writes the version row only after success.
- **No down-runner exists** — `.down.sql` files (022–032) are convention/documentation; rollback of a shipped DB = restore backup. Migration 031 header: "Down form (separate file): plain DROP COLUMN per column" (`031_add_model...up.sql:37`). House style: additive table/column migrations 001–021 have NO `.down` file; from 022 on every migration carries one. For 033: provide the pair + a trivial `.down` (DROP TABLE) to match current convention.
- **Entry point**: `database.Initialize` (`init.go:11-25`) = `NewConnection` + `RunMigrations`; `InitializeAll` (`init.go:47-60`) = Initialize + `InitializeManagers`; `InitializeManagers` (`init.go:28-44`) returns `(*ConfigManager, *ModelsManager, error)`.

### sqlite + postgres twin pattern (concrete diff)

Same DDL in both trees; only deltas are timestamp defaults and PG's `IF NOT EXISTS` on ADD COLUMN — `migrations/sqlite/005_add_credentials.up.sql:2-9` vs `migrations/postgres/005_add_credentials.up.sql:2-9`:
- sqlite: `created_at TEXT NOT NULL DEFAULT (datetime('now'))`
- postgres: `created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()`
- sqlite: `ALTER TABLE models ADD COLUMN credential_id ...`; postgres: `ADD COLUMN IF NOT EXISTS`.
Dialect placeholders elsewhere: `if m.store.Dialect == PostgreSQL { $1 } else { ? }` (e.g. `store.go:2395-2399`). PG is selected by `DATABASE_URL`, else SQLite `~/.config/llm-supervisor-proxy/config.db` (project core blueprint).

### Additive CREATE TABLE precedent (copy-shape for credential_groups)

`migrations/sqlite/005_add_credentials.up.sql:1-12` — the canonical additive table migration:

```sql
-- Migration 005: Add credentials table and credential_id to models
CREATE TABLE IF NOT EXISTS credentials (
    id TEXT PRIMARY KEY,
    provider TEXT NOT NULL,
    api_key TEXT NOT NULL,
    base_url TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
ALTER TABLE models ADD COLUMN credential_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_models_credential_id ON models(credential_id);
CREATE INDEX IF NOT EXISTS idx_credentials_provider ON credentials(provider);
```

Twin at `migrations/postgres/005_add_credentials.up.sql` (same statements, PG types). A `033_add_credential_groups` would mirror: `CREATE TABLE IF NOT EXISTS credential_groups (id TEXT PRIMARY KEY, provider TEXT NOT NULL, ... created_at, updated_at)` + registry append + trap comment + `.down` (DROP TABLE).

### How MODELS and CREDENTIALS persist today — real tables + JSON columns, not blobs

- **`credentials` table** (migration 005): `id TEXT PK, provider, api_key, base_url, created_at, updated_at`. Row struct `dbCredentialRow` (`store.go:536`).
- **`models` table** (001_initial + ALTERs 003/005/008-018/020-031): includes `credential_id` (005:10, retained as derived SHADOW), `credentials_json TEXT NOT NULL DEFAULT '[]'` (028:21), `members_json TEXT` + `shadow_model_id TEXT` (031:39-40), `kind` (030), `image_count` (029). Row struct `dbModelRow` (`store.go:500`) with `isEnabled`/`isInternal`/`isPeakHourEnabled` helpers (`:546/:558/:570`); scan via `scanModels` (`:932`) and mapping `dbModelRowToConfig` (`:1317`).
- **Model→credential refs**: `models.credentials_json` = JSON array of `CredentialRef{credential_id, weight, position}` — `parseCredentialsJSON` (`store.go:597`), `marshalCredentialsJSON` (`:652`). Migration 028 backfills from legacy `credential_id` with JSON1-safe `json_array(json_object(...))` and keeps `credential_id` as shadow = `credentials_json[0].credential_id` (028:1-45, Amended M-1; file-level `BEGIN TRANSACTION; ... COMMIT;` — the repo's FIRST such migration).
- **Group membership**: `models.members_json` = JSON array of `GroupMember{model_id, weight, backup_only?}` — `parseGroupMembersJSON` (`store.go:625`), `marshalGroupMembersJSON` (`:641`). **No join tables anywhere**; membership is JSON-on-row, house style since 028 (031 header explicitly cites "house style: credentials_json, migration 028").
- No UNIQUE constraint precedent for cross-row claims: G8 claim uniqueness is an application-level scan, schema-unique rejected (031 header `:27-30`).

---

## (b) MANAGER + CRUD PATTERNS (recipe for new entity + manager)

Credentials are NOT a separate manager — they live on `ModelsManager` (`pkg/store/database/store.go`). The evidenced recipe:

1. **Types in `pkg/models`**: `CredentialConfig` (`pkg/models/credential.go:84`), `CredentialRef` (`pkg/models/config.go:331-335`), `GroupMember` (`pkg/models/group.go:42-46`); kind constants `KindChat="chat"` / `KindGroup="group"` etc. (`config.go:96-132`); cap constants `MaxCredentialRefs = 16` (`config.go:84`), `MaxGroupMembers = 16` (`group.go:25`); sentinel errors as `&ConfigError{...}`: `ErrCredentialNotFound`, `ErrCredentialInUse` (`credential.go:177-178`). Per-kind validation split field-level vs collection-aware (`validateGroupRules` / `ValidateGroupMembers`, `group.go:60/:96`).
2. **Migration**: SQL pair in both trees + **registry append** (trap, §a) + trap comment.
3. **Manager construction**: `NewModelsManager(store *Store, eventBus *events.Bus)` (`store.go:765-770`) builds `{store, qb: NewQueryBuilder(store.Dialect), eventBus, engine: credentiallb.NewEngine(...), groupEngine}` (`store.go:696-728`). All SQL strings come from the dialect-aware `QueryBuilder` `m.qb` (e.g. `m.qb.GetAllModels()` at `:2385`; `querybuilder.go`). Manager holds `sync.RWMutex` + `closeOnce`; `Close()` (`:877`) unsubscribes + stops engines.
4. **Wiring**: `InitializeManagers` (`init.go:28-44`) is the single construction site; `cmd/main.go` consumes via accessors `Engine()` (`store.go:736`) and `GroupEngine()` (`store.go:743`) — `credLB := modelsMgr.Engine()` at `cmd/main.go:143`. A new manager/entity follows the same seam: construct in `InitializeManagers` (or as a field of ModelsManager), expose via accessor, consume in `cmd/main.go`.
5. **CRUD quartet + strict variants**: legacy in-memory map read (`GetCredential(id) *CredentialConfig :2113`, `GetCredentials() :2156`) AND context-aware strict DB reads for the cache layer (`GetCredentialStrict(ctx,id) :2204`, `GetCredentialsStrict(ctx) :2258` — db-cache-layer 1A convention).
6. **Write path**: `AddCredential(cred) error :2302`, `UpdateCredential(id, cred) error :2332`, `RemoveCredential(id) error :2380`; UI handler calls manager write then `s.modelsConfig.Save()` (:2510, :2613, :2644).
7. **Events**: `publishCredentialsChanged(modelID)` (`:2425-2434`) publishes `credentiallb.EventCredentialsChanged` on the bus (nil-bus tolerant); engine refresh rides `drainCredentialsChanged` (`:843`).
8. **Validation backstop**: manager-side `Validate()` (`:1848`) + `validateModelAgainstCredentials` (`:1930`) + `validateBeforeWrite` (`:2080`) + `validateGroupWrite` (`:2103`); UI-side inline mirrors run BEFORE the manager write (`server.go:682` `validateCredentials`; group G1–G8 mirror `server.go:863-975`).
9. **Tests**: `mock_store.go` + `mock_store_test.go` in-package; twin tests `models_credentials_test.go`, `models_manager_kind_test.go`, `store_group_test.go`, `migrations_test.go` + `migrations_pg_test.go` (PG-gated transaction test mandated for file-level BEGIN/COMMIT — 028 header `:13-16`).

---

## (c) NAMING-COLLISION AUDIT — every existing 'group'-named thing

| # | Identifier | Meaning | Evidence |
|---|-----------|---------|----------|
| 1 | `models.KindGroup = "group"` | model-kind discriminator for VIRTUAL load-balance groups (4th kind) | pkg/models/config.go:132 |
| 2 | `ModelConfig.Members []GroupMember` | kind=="group" member list (`members_json`, migration 031) | pkg/ui/server.go:76; store.go:625 |
| 3 | `ModelConfig.ShadowModelID` | group's ID-space claim (`shadow_model_id`, 031) | pkg/models/group.go:90; server.go:905-975 |
| 4 | `models.GroupMember{ModelID string; Weight float64; BackupOnly bool}` | one (model, weight) member entry; backup_only = two-tier pools | pkg/models/group.go:42-46 |
| 5 | `models.MaxGroupMembers = 16` | member cap | pkg/models/group.go:25 |
| 6 | `models.validateGroupRules` / `ValidateGroupMembers` | G1–G8 rule set for group-kind rows | pkg/models/group.go:60/:96 |
| 7 | `models.GroupRewriter` / `GroupRewriteResult` | contract interface + result for the modelList rewrite | pkg/models (implemented at store_group_rewrite.go:32-47) |
| 8 | **`pkg/grouplb`** | the load-balance-groups sticky weighted picker over MEMBER **MODEL IDs** — NOT credentials | pkg/grouplb/doc.go:1 ("Package grouplb is the load-balance-groups sticky weighted picker") |
| 9 | `grouplb.Engine` (groups map, GetOrSelect, PickBackupMember, OnGroupChanged, ExcludeAndReselect, HasGroup, HasBackupPool, DefaultPoolHealthy) | per-(group, conversationKey)→memberID binding engine | pkg/grouplb/engine.go:75-113, :276, :290, :315, :394, :590, :720, :870 |
| 10 | `grouplb.MemberRef`, `grouplb.ErrNoMembers`, `grouplb.DefaultMaxGroupMember` | member view / no-members error / cap mirror | engine.go:45/:31/:42 |
| 11 | `grouplb.groupState` | per-group internal state (members, backup pool, cooldowns, per-group RNG) | pkg/grouplb/binding.go:43 |
| 12 | `ModelsManager.GroupEngine() *grouplb.Engine` | accessor for the group picker | store.go:743-745 |
| 13 | `ModelsManager.notifyGroupEngineLocked(kind, modelID, members)` + `GroupMemberRefs(members)` | write-path engine notification + ref conversion | store.go:1657/:1668 |
| 14 | `ModelsManager.RewriteGroupEntries(modelList, conversationKey)` | **THE modelList rewrite** (Am21): every group entry → sticky-picked member + member's fallback chain; both protocol sites call it; Am22 direct-ID lookup (claims bypassed — a claims-first lookup would infinite-loop the day-1 self-shadow group); Am28 stale-member skip-and-reselect | pkg/store/database/store_group_rewrite.go:1-80 (pickServableGroupMember `:99-111` per design.md) |
| 15 | `/fe/api/*group*` endpoints | **NONE EXIST** — group rows ride the models endpoints via `?kind=group` (`kindFilter` in handleModels; chat tab excludes groups) | pkg/ui/server.go:786-789, :800-808; route table :159-204 has no group route |
| 16 | FE `kind?: 'chat' \| 'image-gen' \| 'decision' \| 'group'` | wire kind union incl. group | pkg/ui/frontend/src/types.ts:122 |
| 17 | FE `GroupMember` interface | JSON mirror of Go GroupMember | types.ts:182-186 |
| 18 | FE `GroupModel = Model & { kind: 'group' }` | narrowed alias for group rows | types.ts:192 |
| 19 | FE `members?: GroupMember[]` | wire field on Model | types.ts:152 |
| 20 | FE event labels `group_fallback_attempted/advanced/exhausted`, `group_member_failed`; fields `group_id?`, `member_id?` | observability vocabulary | types.ts:322-325, :379-380 |
| 21 | FE components `GroupModelsTab.tsx`, `GroupModelForm.tsx`, `GroupMemberEditor.tsx` (+ tests, hook `useGroupModels`) | the Group Models admin tab | pkg/ui/frontend/src/components/config/ |
| 22 | config fields | **NO group-named config field** — the group member exclusion window REUSES `upstream_skip_cache_ttl` (comments only); `credential_quota_cooldown` is credential-side | pkg/config/config.go:107, :121 (comments :89, :118-120) |
| 23 | proxy-side group vocabulary | `group_unservable` failure envelope; handler `tryGroupReentry` | grouplb/engine.go:848; pkg/proxy/handler.go:1217 (per design.md) |
| 24 | `pkg/credentiallb` | per-MODEL credential load balancing engine (rotation/failover across a model's credentials) — different concern, adjacent name space | pkg/credentiallb/; modelsMgr.Engine() store.go:736 |

**Collision verdict**: `credential_group` / `CredentialGroupsManager` / `/fe/api/credential-groups` / `credential_groups` table / FE "Credential Groups" have **ZERO matches repo-wide** (grepped Go; FE types.ts has no credential-group identifiers — its Group* are all model-groups, its Credential*/credential_id are flat refs). The names are collision-free. The one real confusion risk is semantic adjacency, not identifier overlap: the bare word "group" is owned by MODEL groups (`pkg/grouplb`, `kind:"group"`, `members_json`, GroupModel, "Group Models" tab) and `credentiallb` owns per-model credential rotation — docs, API paths, and FE labels must always carry the full "credential group" qualifier and must NOT shorten to "group" or "cred-group" in user-facing strings. Model `kind` stays UNTOUCHED — a credential group is a new top-level entity, not a 5th model kind.

---

## (d) FE-API CREDENTIALS ROUTE INVENTORY + DELETE-WITH-REFERENCES

### Route registration (server.go:195-197, inside setupRoutes; stdlib ServeMux, prefix-subtree pattern)

| Route | Handler | Behavior |
|---|---|---|
| `GET /fe/api/credentials` | `handleCredentials` (server.go:2445) | lists all, API keys masked via `maskAPIKey` (:2454) |
| `POST /fe/api/credentials` | `handleCredentials` (:2462-2530) | 64KB MaxBytesReader (:2464); DTO `Credential{ID, Provider, APIKey, BaseURL}` (server.go:80-81); required: id (:2475), provider (:2481), api_key (:2487); `AddCredential` → `Save()` → 201 + masked echo |
| `GET /fe/api/credentials/{id}` | `handleCredentialDetail` (:2534, :2543-2560) | 404 bare `{"error":"credential not found"}` |
| `PUT /fe/api/credentials/{id}` | `handleCredentialDetail` (:2562-2628) | provider required; **empty api_key preserves the stored one** (:2586-2595); `UpdateCredential` → `Save()` → 200 masked |
| `DELETE /fe/api/credentials/{id}` | `handleCredentialDetail` (:2630-2651) | `RemoveCredential`; **in-use → HTTP 409 CONFLICT** (`strings.Contains(err.Error(), "in use")` → 409, else 404); success = 204 |

### Envelope convention
`/fe/api/*` errors are ALWAYS the bare `{"error":"<string>"}` object with `http.StatusBadRequest`/`StatusNotFound`/`StatusConflict`/`StatusInternalServerError` — verified at every error site (:2470, :2478, :2484, :2490, :2505, :2513, :2548, :2570, :2608, :2639, :2647). (`/v1/*` uses OpenAI JSON envelopes; `/fe/api/*` is unauthenticated inside the binary, OAuth at ingress — core blueprint.)

### Delete-with-references behavior TODAY (the precedent credential_groups must match)
`RemoveCredential` (`store.go:2380-2418`):
1. **In-use guard FIRST** (`:2384-2392`): scans every model row, walks `model.Credentials` refs; on the FIRST referencing model returns `fmt.Errorf("credential '%s' is in use by model '%s': %w", id, model.ID, models.ErrCredentialInUse)` — **BLOCKED, never cascaded, never warned-and-allowed**. The error message names the first referencing model ID.
2. DELETE row (`:2394-2404`); `rowsAffected == 0` → `models.ErrCredentialNotFound` (:2407-2409).
3. Defensive engine cleanup `m.engine.OnCredentialDeleted(id)` (:2416) for direct-external-SQL deletes / mid-flight races (the in-use guard makes in-process referenced deletion impossible).
4. UI maps the wrapped error to **409 Conflict** (server.go:2634-2635) — note it matches on the *string* "in use", not the sentinel (a `errors.Is` check would be cleaner for a new endpoint).
5. Identical block exists in the JSON-file twin `pkg/models` (`config.go:1420`).

### Used-by / reverse-lookup / usage-count endpoints
**NONE exist anywhere.** The only "used by" mechanism is the delete-time scan above (returns first referencing model, not a count). `/fe/api/usage*` (:201-204) are request-metering, not reference counts. A "used by N models" read for credential groups is NEW surface — the natural house-shaped implementation is a manager-side scan over `models.credentials_json` (the RemoveCredential pattern generalized).

### Models-side wire shape (context for mixing refs)
`server.go:33-45` documents the Phase-4 wire: `credentials []` array wins over legacy top-level `credential_id` (retained shadow); group rows bypass credential validation entirely (server.go:1011-1021: groups carry no credentials by design — `if newModel.Kind != models.KindGroup` gates `validateCredentials`).

---

## (e) HOUSE CONVENTIONS + TEST GATE

### Shared conventions state
- **`.agents/shared/conventions.md` DOES NOT EXIST** (verified — `.agents/shared/` contains only `context.md` + `planning/`). The blueprint-level conventions live in `.agents/shared/context.md` + project blueprints.
- `.agents/shared/context.md` is STALE (last updated 2026-08-28, describes the db-cache-layer branch state; useful only for cache-layer standing notes).

### Prior-art planning dirs (this worktree, all present)
- `planning/load-balance-groups/` — plan.md + decisions.md (Q1–Q21 decision log with USER-DECIDED markers, Architect Round-1 amendment log, Round-2 membership pivot; research-codebase.md). This is the DIRECT structural precedent: group = virtual model kind, members_json pivot, G1–G8 validation, claims (shadow_model_id), Am-N amendment discipline.
- `planning/group-backup-members/` — design.md (D1–D7): `backup_only` added as ADDITIVE JSON field (no migration cost), D2 = write-time validation ERROR + client-side mirror in `GroupModelForm.tsx` handleSubmit; seam extensions at `store_group_rewrite.go:99-111` + `pkg/proxy/handler.go:1217`.
- `planning/model-credential-load-balancing/` — the credential LB precedent (5 phases, decisions, arch review round 3): `credentials_json` ordered weighted refs, provider-match invariant, `credentiallb` engine + event invalidation.
- Naming relevance: every prior commission picked ONE word-pair and suffixed everywhere (`*-groups` → grouplb/GroupMember/GroupModelsTab; `credential*` → CredentialRef/credentials_json/credentiallb). `credential-groups` fits both lineages without overlap; the split noun "credential_groups" has zero prior claim.

### Tester gate (PACKS.md + QUARANTINE.md)
- **Pack A** (release gate): `./pkg/proxy/... ./pkg/store/... ./pkg/decision/... ./pkg/ui/... ./cmd/...` — internal `-timeout=240s` inside outer `timeout 300` (since 2026-11-08; pkg/proxy outgrew 110s), HOME-isolated, config.json hash-guarded (PACKS.md:13-15).
- **Pack B**: anchored complement — `go list ./... | grep -vE '<module>/(cmd|pkg/(proxy|store|decision|ui))(/|$)'` (module-prefix anchor is load-bearing; repo-relative re-runs ALL) (verification blueprint + PACKS.md:14).
- **Anti-vacuous rule**: every ADDED `func Test` must show a literal `--- PASS` line (2026-11-14 gates also required `=== RUN` presence) — PACKS.md:15.
- **gofmt zero-new-drift**: HEAD flagged-LINE-SET minus base flagged set, per file: `git show <base>:<file> | gofmt -d`; diff-hunk overlap is UNRELIABLE (±3 context lines distort); after aligned-fixture sweeps run `gofmt -l` on ALL touched `_test.go` (sweeps silently add whole-file drift) (verification blueprint).
- **FE gate**: vitest suite (269 tests at 2026-11-14 gate) + tsc --noEmit against the count-based baseline (`tsc-baseline.txt`: 19 errors / 9 files — verdicts are count+file-set, exit code 2) + vite build with sha256 byte-proof of `pkg/ui/static/` before/after.
- **Known flakes (QUARANTINE.md active rows + PACKS)**:
  - `TestEngine_GetOrSelect_TOCTOU_CoolingReverify` (pkg/credentiallb cooldown_test.go:481/:517) — package-context timing race, ~1/10 plain (worse under -race); isolated 3P/3; quarantined documentation-level.
  - `TestStoreEngine_PeakHourParity` (pkg/store/database store_test.go:469) — asserts valid ONLY 23:59:00–23:59:59.999 UTC (isWithinWindow half-open drops minute 1439); fired 23:59:02Z at the 2026-11-14 gate; isolated PASS; re-run outside the window.
  - `TestHandleTestMCPServerDirect_RedirectToLocalhostBlocked` + `_RedirectToPrivateIPBlocked` (pkg/mcp handlers_api_test.go:1864-1865/:1904-1905) — DETERMINISTIC external failure: httpbin.org now 403s redirect-to internal targets before the classifier; reconfirmed 3 gates (2026-11-06/14/16). Sole expected Pack B failures.
  - `TestStoreEngine_CloseLifecycle` (t.TempDir cleanup race, store_unit_test), MCP SSRF octal/decimal/hex IP trio (toolchain-deterministic), usage stress pair (SQLITE_BUSY) — all quarantined.
- **Toolchain/environment**: go 1.26 via cached toolchain `$HOME/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.0.linux-amd64/bin/go`, `GOTOOLCHAIN=local`; offline requires pinned GOPATH/GOMODCACHE/GOCACHE; `APPLY_ENV_OVERRIDES` traps config writes; root `go build .` = 3.7MB inert stub — real build is `go build ./cmd`.
- **Claim state** (shared_meta_kv): `wt.claim.cred-groups` held by giter — worktree + branch `feature/credential-groups` off origin/latest `2b987bf`; "no push, no ref moves."

---

## Bottom-line recipe for 033_credential_groups (as evidenced)

1. `pkg/models`: new file `credential_group.go` — `CredentialGroupConfig{ID, Provider, ...}` + cap constant + `ErrCredentialGroupNotFound`/`ErrCredentialGroupInUse` sentinels (`&ConfigError{}` per credential.go:177).
2. Migrations: `migrations/{sqlite,postgres}/033_add_credential_groups.up.sql` (CREATE TABLE IF NOT EXISTS credential_groups ... + indexes; PG TIMESTAMPTZ/NOW + IF NOT EXISTS deltas) + `.down.sql` (DROP TABLE) + **registry append at migrate.go:78 with the Amendment-1 trap comment**.
3. `pkg/store/database`: CRUD on `ModelsManager` (house pattern) or a sibling manager constructed in `InitializeManagers`; dialect-aware queries via `m.qb`; strict variants for the cache layer; delete-time in-use scan over `models.credentials_json` mapping to 409.
4. `pkg/ui/server.go`: `mux.HandleFunc("/fe/api/credential-groups", ...)` + `"/fe/api/credential-groups/"` detail handler beside the credentials pair (:195-197); bare `{"error":...}` envelope; 64KB body cap; masked secrets never echoed.
5. FE: new `CredentialGroup*` types in types.ts + tab/form/editor components beside the `GroupModel*` trio (names distinct from `GroupModelForm`/`GroupMemberEditor`).
6. Gate: Pack A/B with `-timeout=240s`, anti-vacuous `--- PASS` for every added test, gofmt zero-new-drift (line-set diff vs base), FE vitest + tsc count-baseline + static sha256 proof.
