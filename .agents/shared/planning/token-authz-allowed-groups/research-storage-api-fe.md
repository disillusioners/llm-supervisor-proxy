# AUTH COMMISSION — Research: storage, migration, API, FE, test-harness surfaces

Target: `feature/token-authz-allowed-groups` @ base `a3f0335`
Worktree: `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-token-authz`
All citations are `file:line` **at a3f0335**.

Subject: NEW token field `allowed_groups` — DB representation + migration + rollback,
`/fe/api` token contract, FE `TokenForm` picker widening, test-pin placement.

---

## 0. DRIFT REPORT (commission cites vs. code at a3f0335)

| Commission cite | Status at a3f0335 |
|---|---|
| `ui/server.go:2681-2698` (store `allowed_models` verbatim, no validation) | **CONFIRMED** — exactly those lines (`var allowedModels []string` :2681, copy :2682-2684, `CreateToken(...)` :2686, response echo :2698). No validation of any kind. |
| `ui/server.go:2392` (token list projection: id/name/created/dates/scope only, never token values) | **CITATION WRONG — the CLAIM is true, the LINE is not.** `ui/server.go:2392` is inside `handleModelTest`'s error path (`TestModelResponse{Success:false, Error:"credential has no API key…"}`). The token list projection is `listTokens` at **`pkg/ui/server.go:2613-2644`** (projection fields at **:2629-2639**). Token VALUES are never returned there (no `token`, no `token_hash`). |
| `handleTokens` at `:227` | Route registration **:227 CONFIRMED**; the handler FUNC is at **:2514** (`handleTokens`), dispatching GET→`listTokens` (:2519) / POST→`createToken` (:2521). |
| `handleTokenDetail` at `:228` route, `:2530` | Route **:228 CONFIRMED**; handler FUNC at **:2528**, id extraction at **:2530** (so :2530 is the `id := strings.TrimPrefix(...)` line, not the func). |
| `handleUpdateTokenPermission` (PATCH) at `:2567` | Name drift: there is **no `handleUpdateTokenPermission`**. The unexported func is **`updateTokenPermission` at `pkg/ui/server.go:2568`** (`:2567` is its doc-comment line). PATCH dispatch is `handleTokenDetail` → `s.updateTokenPermission(ctx,w,r,id)` at **:2554-2555**. |
| `TokenForm.tsx:34` `kind === 'chat'` filter | **CONFIRMED** — `const chatModels = models.filter((m) => !m.kind || m.kind === 'chat');` |
| migrate.go registry slice ~:29-83 | **CONFIRMED** — `var migrations = []migration{` at **:24**, first entry :25, last entry (`033`) at **:82**, closing brace **:83**. |
| `pkg/decision/handler.go:317-326` (group arm) vs `:340-354` (allow-list) | **CONFIRMED VERBATIM** — group branch :317-326 (`if model.IsGroup() && h.groupIsDecisionLane(model.ID)` at :323 → `serveDecisionGroup` :324); Step-7 allow-list at :340-354 (`IsModelAllowed` :343). The group arm returns at :325 **before** the allow-list. |
| QUARANTINE flake rows | TOCTOU = **QUARANTINE.md:10**; CloseLifecycle = **:7**; usage stress = **:9**; mcp httpbin pair = **:11**; PeakHourParity = **:12**. |

---

## 1. TOKEN STORE — `pkg/auth/store.go` (284 lines)

### Shape
- `pkg/auth/store.go:13-17` — `TokenStore{db *sql.DB; dialect database.Dialect}`. **DB-backed only** (no in-memory map anywhere in the type).
- `pkg/auth/store.go:19-28` — `TokenStoreInterface` (6 methods):
  - `ValidateToken(ctx, plaintext) (*AuthToken, error)` :22
  - `CreateToken(ctx, name, expiresAt *time.Time, createdBy string, allowedModels []string) (string, *AuthToken, error)` :23
  - `DeleteToken(ctx, id) error` :24
  - `ListTokens(ctx) ([]AuthToken, error)` :25
  - `GetTokenByID(ctx, id) (*AuthToken, error)` :26
  - `UpdateTokenPermission(ctx, id, allowedModels []string) error` :27
- `pkg/auth/store.go:31` — compile-time assertion `var _ TokenStoreInterface = (*TokenStore)(nil)`.
- `pkg/auth/store.go:34-36` — `NewTokenStore(db, dialect)`.

### Method → SQL map (dialect switch on every statement)
| Method | Lines | SQL |
|---|---|---|
| `CreateToken` | :40-90; JSON marshal :54-65; INSERT :67-72 (`$1..$7` PG :69 / `?` SQLite :71); Exec :74; returned struct :79-87 | `INSERT INTO auth_tokens (id, token_hash, name, expires_at, created_at, created_by, allowed_models) VALUES (...)` |
| `ValidateToken` | :93-144; SELECT :105-110; Scan :112; `ScanAllowedModels` :137; expiry :139-141 | `SELECT id, token_hash, name, expires_at, created_at, created_by, allowed_models FROM auth_tokens WHERE token_hash = ?` |
| `ListTokens` | :147-185; SELECT :148 (no hash column); Scan :163; scan-allowed :179 | `SELECT id, name, expires_at, created_at, created_by, allowed_models FROM auth_tokens ORDER BY created_at DESC` |
| `DeleteToken` | :188-210 | `DELETE FROM auth_tokens WHERE id = ?` |
| `GetTokenByID` | :213-249; SELECT :219-224; Scan :226; scan-allowed :246 | `SELECT id, name, expires_at, created_at, created_by, allowed_models FROM auth_tokens WHERE id = ?` |
| `UpdateTokenPermission` | :252-284; JSON :253-261; UPDATE :263-268; Exec :270 | `UPDATE auth_tokens SET allowed_models = ? WHERE id = ?` |

### Where `allowed_models` is read / written
- **Write (create)**: `pkg/auth/store.go:57-64` (nil/empty ⇒ `NULL`; non-empty ⇒ JSON array text), bound at :74, echoed into the struct at :86.
- **Write (update)**: `pkg/auth/store.go:254-261` + :265/:267/:270.
- **Read**: `pkg/auth/store.go:137` (ValidateToken), `:179` (ListTokens), `:246` (GetTokenByID) — all through `auth.ScanAllowedModels`.

### Value types / helpers (`pkg/auth/token.go`, 125 lines)
- `pkg/auth/token.go:26-34` — `AuthToken{ID, Name, TokenHash, ExpiresAt, CreatedAt, CreatedBy, AllowedModels []string}`; `AllowedModels` at **:33** (`nil/empty = all models allowed`). **This struct is where a new `AllowedGroups []string` field lands.**
- `pkg/auth/token.go:40-68` — `ScanAllowedModels(raw)`: nil → nil (:41-43); `string`/`[]byte` handling :46-55 (unknown type ⇒ empty slice, fail-closed :53-55); empty string → nil (:57-59); **malformed JSON ⇒ `[]string{}` + `log.Printf("SECURITY: malformed allowed_models JSON …")` (:62-66) — fail-closed**.
- `pkg/auth/token.go:114-125` — `IsModelAllowed(modelID)`: **`len(AllowedModels)==0 ⇒ true` (allow-all)** :116-118; exact, case-sensitive membership :119-124.
- `pkg/auth/token.go:72-81` `GenerateToken` (sk- + 32 random bytes hex), `:84-87` `HashToken` (SHA-256 hex), `:90-100` `ValidateTokenFormat`, `:103-108` `IsExpired`.

### In-memory vs DB: BOTH (decorator) — "three-tier token cache"
- `pkg/modelscache/tokens.go:46-63` — `CachedTokenStore{inner auth.TokenStoreInterface; opts; mu; entries *lru; idToHash; idOrder; stopOnce}`.
- `pkg/modelscache/tokens.go:40-44` — the three tiers: `tierPositive`, `tierStalePositive`, `tierNegative` (semantics documented :13-36).
- `pkg/modelscache/tokens.go:68-79` — `NewCachedTokenStore(inner, opts)` (panics on nil inner :69-71).
- `pkg/modelscache/tokens.go:177-250` — `ValidateToken` state machine (fresh positive :162-165, negative replay :167-171, infra→degraded-allow stale tier :214-242, unknown class ⇒ no cache write :244-249).
- `pkg/modelscache/tokens.go:263-275` `CreateToken` (write-through positive), `:281-292` `DeleteToken` (id→hash fan-out), **`:297-…` `UpdateTokenPermission` (delegates then DROPS the cached entry :301-304 so a permission change is not masked by a stale positive)**.
- **The decorator implements `auth.TokenStoreInterface` ⇒ any signature change to the interface breaks it** (see §6 mock inventory).

### Boot wiring (ONE shared instance for UI + all lanes) — `cmd/main.go`
- `cmd/main.go:183` — `var wrappedTokens *modelscache.CachedTokenStore`
- `cmd/main.go:275-282` — `var tokenStore auth.TokenStoreInterface`; cache path `:279-280` (`modelscache.NewCachedTokenStore(auth.NewTokenStore(dbStore.DB, dbStore.Dialect), modelscache.Options{})`), raw path `:282`.
- Consumers of the SAME instance: `cmd/main.go:293` (ui.NewServer), `:300` (proxy.NewHandler), `:310` (imggen.NewHandler), `:338` (decision.NewHandler), `:387` (mcp.NewServer).
- Cache layer default ON (`CACHE_LAYER_ENABLED`, default ON — project POK/blueprint), so **production is bound to the decorator** ⇒ a new interface method needs a decorator forward + a decorator-through test (house rule, cf. `pkg/modelscache/models.go:1321-1341` `ClearGroupMemberCooldown` forward + `pkg/proxy/win_clear_winner_identity_test.go:114-190`).

### DB read/write paths for `auth_tokens` **in pkg/store/database**: NONE
- `grep -n "auth_tokens" pkg/` → the ONLY Go code touching `auth_tokens` is `pkg/auth/store.go` (10 statements) plus TEST fixtures. `pkg/store/database` contains:
  - the migration SQL: `migrations/{sqlite,postgres}/004_add_auth_tokens.up.sql` (table DDL), `020_add_ultimate_model_enabled.up.sql:1`, `022_add_allowed_models.{up,down}.sql:1`, `023_add_ultimate_model.{up,down}.sql`, `024_convert_allowed_models_to_ids.{up,down}.sql`;
  - `pkg/store/database/sqlc/schema.sql:48-55` — a **stale sqlc snapshot** of `auth_tokens` (no `allowed_models`, no `allowed_groups`); not authoritative, not regenerated.
- ⇒ **The DB representation of the new field lives in `pkg/auth/store.go` (SELECT/INSERT/UPDATE column lists) + the migration SQL**, not in `pkg/store/database` Go code.

---

## 2. `/fe/api` TOKEN API CONTRACT — `pkg/ui/server.go` (3298 lines)

### Routes
- `pkg/ui/server.go:227` — `mux.HandleFunc("/fe/api/tokens", s.handleTokens)` → GET list, POST create.
- `pkg/ui/server.go:228` — `mux.HandleFunc("/fe/api/tokens/", s.handleTokenDetail)` → DELETE, PATCH `{id}`.
- **There is NO PUT route for tokens.** Token mutation surface = POST (create) + PATCH (permission) + DELETE (revoke).

### Request bodies — EXACT field names
| Verb | Struct | Fields |
|---|---|---|
| POST `/fe/api/tokens` | `CreateTokenRequest` **pkg/ui/server.go:2501-2505** | `name` (string, required — 400 if empty :2658-2661), `expires_at` (*string, RFC3339, optional — 400 "Invalid expires_at format (use ISO 8601)" :2665-2672), `allowed_models` (*[]string, omitempty, nil/empty = all allowed) |
| PATCH `/fe/api/tokens/{id}` | `UpdateTokenPermissionRequest` **pkg/ui/server.go:2563-2565** | `allowed_models` (*[]string, omitempty; **nil = keep existing**, non-nil incl. `[]` = replace) |

- PATCH merge semantics: `pkg/ui/server.go:2589-2597` (nil ⇒ `currentToken.AllowedModels`; non-nil ⇒ the posted value, empty array included).
- POST semantics: `pkg/ui/server.go:2680-2686` (nil ⇒ `allowedModels` stays nil ⇒ stored NULL ⇒ "all models allowed").
- `created_by` derivation: `pkg/ui/server.go:2674-2678` (`X-User` header, default `"api"`).

### Response bodies — EXACT field names
- `TokenResponse` **pkg/ui/server.go:2491-2498**: `id`, `name`, `expires_at` (omitempty), `created_at`, `created_by`, `allowed_models` (omitempty).
- `CreateTokenResponse` **pkg/ui/server.go:2508-2511**: embeds `TokenResponse` + **`token` (plaintext, shown once)**.
- **List** (`listTokens`, :2613-2644): `[]TokenResponse` — `id`, `name`, `created_at`, `created_by`, `allowed_models` (+`expires_at` when set :2636-2639). **Token values / hashes are NEVER returned.** (Drift-corrected: the projection is :2629-2639, not :2392.)
- **Create** (:2692-2709): the same projection + `token` = plaintext, HTTP **201 Created** (:2708).
- **PATCH** (:2609-2610): `{"success": true}` — no token data.
- **DELETE** (:2552): HTTP **204 No Content**, empty body.
- **GET on `/fe/api/tokens/{id}`**: not routed (the detail handler only accepts DELETE/PATCH → 405, :2557-2559).

### Is `allowed_models` validated on write? — **NO. Anywhere.**
- Create: `pkg/ui/server.go:2681-2686` — the posted array is passed straight to `tokenStore.CreateToken`; **no existence check, no kind check, no length cap, no dedupe**.
- PATCH: `pkg/ui/server.go:2589-2599` — same, straight through to `UpdateTokenPermission`.
- The store itself does not validate either (`pkg/auth/store.go:54-65`, `:253-261` — pure JSON marshal).
- Asymmetry worth flagging: PATCH caps the body at 64KB (`pkg/ui/server.go:2570` `http.MaxBytesReader`), **POST /fe/api/tokens has NO body cap** (`createToken` :2646-2656 decodes straight from `r.Body`) — unlike `handleCredentials` POST which does cap it (:2740-2741).

---

## 3. MIGRATION MECHANICS

### The registry is an ORDERED SLICE — `pkg/store/database/migrate.go`
- `pkg/store/database/migrate.go:24-83` — `var migrations = []migration{…}` (literal, ordered). Embeds: `//go:embed migrations/sqlite/*.sql` :11-12 and `//go:embed migrations/postgres/*.sql` :14-15 (files are embedded but **only the registry decides what runs**).
- The trap comments, verbatim:
  - 029 (3rd of 5 occurrences in the file): `pkg/store/database/migrate.go:53-58` — *"This registry is an ordered slice, NOT a directory scanner; appending this entry is what activates the 029 SQL files. Forgetting the append = silently inert migration (the SQL files load fine but never run)."*
  - 030: :59-63 — *"without this append the 030 SQL files are silently inert and image-gen kind stays memory-only."*
  - 031: :64-69 — *"registry append activates the 031 SQL files (models.members_json + models.shadow_model_id). Without this append the columns never land and every group write fails at the storage layer."*
  - 032: :70-77 — *"this append activates the 032 SQL files … Without this append the 032 files are silently inert …"*
  - **033 (the fifth, current)**: `pkg/store/database/migrate.go:78-82` —
    `// Amendment-1 trap (fifth occurrence): this registry is an ordered`
    `// slice, NOT a directory scanner; this append activates the 033 SQL`
    `// files. Without this append the 033 files are silently inert and`
    `// credential groups are unavailable.`
    `{"033", "033_add_credential_groups.up"},`
- The 034 entry therefore goes **immediately after line 82**, inside the same slice, with its own trap comment (sixth occurrence).

### Runner internals
- `pkg/store/database/migrate.go:86-99` — `RunMigrations(ctx)`: ensure `schema_migrations` (:88) then iterate the slice in order (:93-97).
- `pkg/store/database/migrate.go:102-146` — `runMigration(ctx, m)`: `isMigrationApplied` (:103) → skip if applied (:107-109) → `loadMigrationSQL` (:112) → `ExecContext` (:118) → on error a best-effort `ROLLBACK` (:136) + wrapped error (:137) → `recordMigration` (:141).
- `pkg/store/database/migrate.go:149-171` — `loadMigrationSQL(name)`: picks the embedded FS by dialect (:153-162) and reads `fmt.Sprintf("%s/%s.sql", dir, name)` (:164-165) ⇒ **the registry entry carries the file name WITHOUT the `.sql` suffix**.
- `pkg/store/database/migrate.go:174-199` — `isMigrationApplied` (SELECT COUNT(*) FROM schema_migrations WHERE version = ?).
- `pkg/store/database/migrate.go:202-224` — `recordMigration` (INSERT version + applied_at).
- `pkg/store/database/migrate.go:227-255` — `ensureMigrationsTable` (schema_migrations: version PK + applied_at).
- `pkg/store/database/migrate.go:258-275` — `GetAppliedMigrations`.
- `pkg/store/database/init.go:19` — production boot calls `store.RunMigrations(ctx)`.

### Does the runner apply `.down` files? — **NO. There is no down-runner.**
- Every registry entry ends in `.up`; `loadMigrationSQL` is only ever called with a `.up` name; nothing in `pkg/store/database` greps or loads `*.down.sql` (grep for `down.sql` across `pkg/` returns only: the 022/023/024/028/033 down FILES themselves, `pkg/store/database/store.go:688-689` (a comment about the 028 down migration), and `pkg/store/database/migrations_test.go:378` which **executes 028.down.sql verbatim via `loadMigrationSQL`** as a test-only proof).
- Precedent statement in-repo: `pkg/store/database/migrations/sqlite/033_add_credential_groups.down.sql:1-3` —
  `-- Migration 033 (down): documentation-only artifact. No down-runner exists`
  `-- in this repo (025/026/027 have NO down file); rollback = restore backup.`
  `DROP TABLE IF EXISTS credential_groups;`
  (postgres twin identical.)
- `pkg/store/database/migrations/sqlite/030_add_model_kind.up.sql:24-27` also states: *"Down form (separate file): plain DROP COLUMN … Downs are manual-run artifacts; see the 029 down operator note for the schema_migrations hygiene step."*

### Exact 034 file shapes (modelled on 022)

**`pkg/store/database/migrations/sqlite/034_add_allowed_groups.up.sql`** (SQLite has **no** `ADD COLUMN IF NOT EXISTS` — see `030_add_model_kind.up.sql:24-27`; the `PRAGMA table_info` line in 022 sqlite up :6 is a **no-op read**, not a guard):
```sql
-- Migration 034: add allowed_groups column to auth_tokens
-- (AUTH COMMISSION — token allowed_groups / decision-group scope).
-- Nullable TEXT; NULL/'' = no group scope restriction (mirrors allowed_models).
ALTER TABLE auth_tokens ADD COLUMN allowed_groups TEXT DEFAULT NULL;
```
(If you copy 022's sqlite file verbatim, copy its comments too — `022_add_allowed_models.up.sql:1-13` — but note the PRAGMA at :6 does nothing.)

**`pkg/store/database/migrations/postgres/034_add_allowed_groups.up.sql`** (exact 022 postgres form, `022_add_allowed_models.up.sql:1`):
```sql
ALTER TABLE auth_tokens ADD COLUMN IF NOT EXISTS allowed_groups TEXT DEFAULT NULL;
```

**Down files (both dialects)** (exact 022 down form, `022_add_allowed_models.down.sql:1`):
```sql
ALTER TABLE auth_tokens DROP COLUMN IF EXISTS allowed_groups;
```

**Registry append** (`pkg/store/database/migrate.go`, after :82):
```go
	// Amendment-1 trap (sixth occurrence): this registry is an ordered
	// slice, NOT a directory scanner; this append activates the 034 SQL
	// files. Without this append the 034 files are silently inert and
	// the allowed_groups token scope does not exist.
	{"034", "034_add_allowed_groups.up"},
```
(name must be `034_add_allowed_groups.up` — no `.sql`; `loadMigrationSQL` appends it.)

### How a migration is ROLLED BACK **today** (the real story)
1. **DB layer**: there is no automated down. The only sanctioned rollback is **restore the pre-migration database backup** (the standing deploy flow mandates "deploy daoduc (backup first)"; the 033 down file says exactly "rollback = restore backup"). The `.down.sql` files are **operator-run manual artifacts**; if an operator runs one, the `schema_migrations` row must also be deleted (the "schema_migrations hygiene step" referenced by `030_add_model_kind.up.sql:26-27`), otherwise the runner will consider the version applied and never re-run it.
2. **App layer**: roll the binary back (helm rollback / set image to the prior tag) — an image rollback does **not** touch the DB, so the column simply stays (additive, NULL-default ⇒ backward compatible: old binaries ignore it).
3. **Partial-failure case**: SQLite/PG `ALTER TABLE ADD COLUMN` is atomic, so a failed 034 leaves the column absent; but if the Exec succeeded and `recordMigration` failed (:141), the column exists while `schema_migrations` has no `034` row ⇒ a re-run hits "duplicate column name". Recovery = manual `DROP COLUMN` (+ delete the version row) or restore the backup.
4. ⇒ **Plan requirement**: the 034 rollback story must be stated as (a) *additive column ⇒ app-level rollback (image) is safe with the column present*, and (b) *DB-level rollback = restore backup or operator-run `.down.sql` + `schema_migrations` row deletion*, and must name the backup artifact.

---

## 4. GROUP MEMBERSHIP STORAGE + ID NAMESPACES

### A group row IS a `models` row
- `pkg/store/database/migrations/sqlite/001_initial.up.sql:19-20` — `CREATE TABLE IF NOT EXISTS models ( id TEXT PRIMARY KEY, … )`.
- `models.kind` added by migration 030 (`pkg/store/database/migrations/sqlite/030_add_model_kind.up.sql:29` — `ALTER TABLE models ADD COLUMN kind TEXT;`).
- `models.members_json` + `models.shadow_model_id` added by migration 031 (`pkg/store/database/migrations/sqlite/031_add_group_fields.up.sql:39-40`).
- `pkg/models/config.go:350-427` — `ModelConfig`: `ID` :351, `Name` :352, `Enabled` :353, `Kind` :403, `Members []GroupMember` :415, `ShadowModelID` :426.
- Group kind constant + helpers: `pkg/models/group.go` (KindGroup usages), lane fold `pkg/models/group_lane.go:32-40` (`GroupLaneChat` :36, `GroupLaneDecision` :40), `MemberLane(kind)` :53.

### How members are referenced inside `members_json`
- `pkg/models/group.go:27-48` — `GroupMember{ModelID string \`json:"model_id"\`; Weight float64 \`json:"weight"\`; BackupOnly bool \`json:"backup_only,omitempty"\`}`; the doc at :28-35 is explicit: *"members are PROXY MODEL IDs, not credential triples … ModelID: references models.id of an EXISTING model row of the group's LANE"*. **⇒ members are model IDs (never names).**
- Serialization: `pkg/store/database/store.go:653-662` `marshalGroupMembersJSON` (nil/empty ⇒ `'[]'`, marshal failure ⇒ `'[]'`); parse: `pkg/store/database/store.go:637-646` `parseGroupMembersJSON` (empty/`[]` ⇒ nil, unparseable ⇒ nil).
- Column plumbing: INSERT column list `pkg/store/database/querybuilder.go:97`; upsert SET :117/:124; PG UPDATE SET :154; SQLite UPDATE SET :177; SELECT `coalesce(members_json,'[]')` :201/:211/:225/:235/:249/:259/:273/:283; row binds `pkg/store/database/store.go:1665` and `:1811`.

### ⚠️ ID NAMESPACE — group ids and model ids SHARE ONE NAMESPACE
- Single `models.id` PRIMARY KEY ⇒ a group row and a chat/decision/image-gen row cannot share an id; `allowed_groups` entries are **`models.id` values of `kind='group'` rows**.
- Group creation goes through the SAME endpoint as models: `POST /fe/api/models` with `kind:"group"` (`pkg/ui/server.go:1002-1018` POST branch; DTO `Model` :46-78 with `kind` :57, `members` :76, `shadow_model_id` :77). Live groups are ordinary `models` rows.
- Group ids are **free-form operator-chosen strings** (no `grp-`/`dg-` generator in Go). Live ids observed in the decision-groups E2E runbook: `dg-e2e-decision-group` (group), `dg-e2e-throwaway` (decision-kind member), `dg-e2e-cred` (credential) — `.agents/tester/RESULTS/2026-11-17-deploy-e2e-decision-groups.md:48/:50/:126-128/:134/:155-156`; the live group-id set recorded at :134 is `agentic-group, coding-group, dg-e2e-decision-group, quick-group, title, vision-group`.
- ⇒ **Q1/Q3 semantic decision:** `allowed_models` and `allowed_groups` are the SAME id space (`models.id`). Today `allowed_models` CAN already contain a group id — the chat lane evaluates the **client-requested id string** (see §6 / `pkg/proxy/handler.go:823-839`), so a token listing a group id reaches that group in the chat lane; the DECISION lane's group arm (`pkg/decision/handler.go:323-326`) never consults either list ⇒ **scope escape**. A new `allowed_groups` field therefore needs an explicit semantic contract (see §9 Q1/Q2).

---

## 5. FE — `pkg/ui/frontend/src/`

### `components/tokens/TokenForm.tsx` (141 lines)
- Props `pkg/ui/frontend/src/components/tokens/TokenForm.tsx:5-15` — `models: { name: string; id: string; kind?: 'chat' | 'image-gen' | 'decision' | 'group' }[]` **:10**; `token?: ApiToken` :11; `onSubmit: (name, expiresAt, allowedModels?: string[]) => Promise<void>` **:12**; `onCancel` :13; `onStatus` :14.
- **The filter (to be widened)**: `pkg/ui/frontend/src/components/tokens/TokenForm.tsx:30-34` —
  `// load-balance-groups 2026-10-16 (plan §6.4 arm 4 of 4 — P3.7): …`
  `const chatModels = models.filter((m) => !m.kind || m.kind === 'chat');`  ← **:34 CONFIRMED**
- State: `selectedModels` seeded from `token?.allowed_models || []` **:25**.
- **Payload emitted**: `pkg/ui/frontend/src/components/tokens/TokenForm.tsx:45-47` — `const expires = expiresAt ? new Date(expiresAt).toISOString() : null;` then `await onSubmit(name.trim(), expires, selectedModels);` ⇒ **a FLAT `string[]` of model ids, with NO kind discrimination and NO separate groups list**. The picker renders `chatModels` at :109-113 (`<ModelMultiSelect models={chatModels} selected={selectedModels} onChange={setSelectedModels} />`).
- Display helper: `getModelDisplayName` :59-61 (id → name via the `models` prop; falls back to the id).

### `components/tokens/ModelMultiSelect.tsx` (199 lines)
- Props :3-8 — `models: { name: string; id: string; [key: string]: any }[]`, `selected: string[]`, `onChange: (selected: string[]) => void`, `disabled?`.
- Search filter :31-33; toggle emits **ids only** :39-45; `selectAll` :48-52; `clearAll` :55-58; toggle-all :61-73; display text :76-81 ("All models" when empty); trigger button :89-110 (`aria-haspopup="listbox"` — the FE test opens the dropdown through this); dropdown :113-… .
- ⇒ **The picker carries NO kind metadata into the payload** — widening the picker means the emitted array will contain group ids (and decision model ids) indistinguishably from chat ids. If `allowed_groups` must be a SEPARATE wire field, the picker/emit layer must be split (two pickers) or the split must happen in the submit handler.

### `components/tokens/TokenList.tsx` (302 lines)
- Props :5-13 — `tokens: ApiToken[]`, `models: { name: string; id: string }[]` (**no kind** — TokenList's type is narrower than TokenForm's), `onUpdatePermission: (id, allowedModels?: string[]) => Promise<boolean>` :11.
- Optimistic map :18; `getAllowedModels` :57-62; `startEditModels` :65-68; `saveEditModels` :77-109 (optimistic set :84, `onUpdatePermission(tokenId, editingModelsValue)` :86, revert on failure :89-96 / :99-104); `getAllowedModelsDisplay` :117-140 ("All models" badge when empty :120); **inline `ModelMultiSelect` at :253** (the second, per-token permission editor — a second surface that must gain `allowed_groups`).
- ⇒ TokenList's `models` prop type has NO `kind` ⇒ if the group picker is widened here too, the prop type must be widened to `{ name, id, kind? }[]` and `SettingsPage.tsx:744` must stop dropping `kind` (it currently maps `{ name, id, kind }` — the extra `kind` is silently dropped by the narrower prop type).

### Where the picker's model list comes from
- `pkg/ui/frontend/src/components/SettingsPage.tsx:752-756` — `<TokenForm models={models.map(m => ({ name: m.name, id: m.id, kind: m.kind }))} … />`; `TokenList` gets the same mapping at **:744**.
- `models` is the **UNFILTERED** model list: `App.tsx:294` passes `models={models}` down; the hook is `pkg/ui/frontend/src/hooks/useApi.ts:278-296` `useModels()` → `fetch(`${API_BASE}/models`)` at **:286** — **no `?kind=` parameter** ⇒ the array contains chat, image-gen, decision AND group rows.
- Backend: `pkg/ui/server.go:939-1000` `handleModels` GET — `kindParam := r.URL.Query().Get("kind")` :948; `"" ⇒ kindFilter = nil` (:951-952 ⇒ **ALL rows, incl. groups and decision**); `kind=image-gen` :953-954; `kind=decision` :955-963; `kind=group` :964-967; `kind=chat` :968-978 (excludes image-gen/decision/group); unknown ⇒ chat-arm + server log :979-987. Rows are emitted via `modelConfigToDTO(mc)` :996 (:989-997).
- DTO: `pkg/ui/server.go:46-78` `Model{id,name,enabled,fallback_chain,truncate_params,kind(:57),internal,credentials,internal_base_url,internal_model,release_stream_chunk_deadline,peak_hour_*,secondary_upstream_model,members(:76),shadow_model_id(:77)}`.
- ⇒ **No new endpoint is needed**: the picker already receives every row (with `kind`); dropping the `:34` filter exposes group ids + decision models. But `useGroupModels()` (`useApi.ts:615-700`, `GET /fe/api/models?kind=group`) already exists as a server-side group filter — an alternative source for a group-only picker.

### TS types
- `pkg/ui/frontend/src/types.ts:240-249` — `ApiToken{ id, name, token?, prefix, expires_at?, created_at, last_used_at?, allowed_models: string[] | null }`; **`allowed_models` at :248** ⇒ add `allowed_groups: string[] | null` here (or a sibling `scope` object). Note `:244 prefix` — the BE never returns `prefix` (pre-existing FE/BE drift; the BE returns `token` only once at creation).
- `pkg/ui/frontend/src/types.ts:238` — `export type GroupModel = Model & { kind: 'group' }`.

### Payload emitters (`pkg/ui/frontend/src/hooks/useApi.ts`)
- `createToken` **:898-911** — body `{ name, expires_at, allowed_models: allowedModels && allowedModels.length > 0 ? allowedModels : null }` (:899-903); POST `/tokens` :904-907; cache invalidate :908; refetch :909.
- `updateTokenPermission` **:913-934** — PATCH `/tokens/${id}` body `{ allowed_models: allowedModels !== undefined && allowedModels.length > 0 ? allowedModels : null }` (:918-920); 404 ⇒ false :923; optimistic local merge :927; cache invalidate :929.
- `deleteToken` :936-940; `fetchTokens` :878-896 (`GET ${API_BASE}/tokens`, cached 15s :881-888).
- ⇒ **The wire field name `allowed_models` is snake_case at the API boundary** — a new field follows the same convention: `allowed_groups`.

### FE wiring chain (all files that must change for a new field)
| File | Lines | What |
|---|---|---|
| `pkg/ui/frontend/src/types.ts` | 240-249 | `ApiToken` — add the field |
| `pkg/ui/frontend/src/hooks/useApi.ts` | 898-911, 913-934 | POST/PATCH bodies + optimistic merge |
| `pkg/ui/frontend/src/components/tokens/TokenForm.tsx` | 5-15, 25, **30-34**, 45-47, 109-113 | prop type, picker filter, emitted payload, picker render |
| `pkg/ui/frontend/src/components/tokens/TokenList.tsx` | 5-13, 57-68, 77-109, 117-140, 253 | prop type (`models` has no `kind` today), display, inline editor |
| `pkg/ui/frontend/src/components/SettingsPage.tsx` | 61, 63, 370-395, 741-756 | prop types, `handleCreateToken`, TokenList/TokenForm wiring |
| `pkg/ui/frontend/src/App.tsx` | 283, 285 (+ handlers) | prop types `onCreateToken` / `onUpdateTokenPermission` |
| `pkg/ui/frontend/src/components/tokens/ModelMultiSelect.tsx` | 3-8, 39-45 | generic picker — reusable as-is (ids only); no change strictly required |

### `components/tokens/__tests__/TokenForm.test.tsx` (88 lines) — what it asserts today
- Header comment :5-15 (Decision Commission §6.7.a / P3.8 amendment 7 rationale).
- Fixture helper :17-29 (`makeModel`), multi-kind fixture :43-51: `chat-1`, `chat-2` (no kind), `img-1` (image-gen), `dec-1` (decision), **`grp-1` (kind='group') at :49**.
- The single test :41-85 opens the ModelMultiSelect dropdown (:66-71) and asserts:
  - **present**: `'Chat One'` :80, `'Chat Two (no kind)'` :81
  - **absent**: `'Image Gen One'` :82, `'Decision One'` :83, **`'Group One'` :84**
- ⇒ **Widening the filter makes THIS TEST RED at :82-84** (image-gen/decision/group all become visible). The test must be rewritten (split: "chat filter removed ⇒ group + decision rows now surface" + a new assertion that the emitted payload carries the group id).
- FE gate: `PACKS.md:62` `fe_vitest_suite` = `cd pkg/ui/frontend && timeout 300 npx vitest run` (last recorded PASS 357/357, 33 files, 12.36s @ ad6996ac); FE typecheck gate = live `tsc --noEmit` vs committed baseline (19 errors / 9 files) — `PACKS.md:262`, `.agents/tester/rules/ensure.md:7` ("zero NEW error locations"; `types.ts` in the change set ⇒ the tsc-baseline comparator applies, `PACKS.md:254`).

---

## 6. TEST HARNESS — `.agents/tester/`

### Pack definitions (`.agents/tester/PACKS.md`)
- **:26** `## Release-Gate Slices (normative — established/corrected 2026-11-17)`
- **:27** `- **Pack A** (9 packages): `./pkg/proxy/... ./pkg/store/... ./pkg/decision/... ./pkg/ui/... ./cmd/...`` → expands to pkg/proxy, pkg/proxy/normalizers, pkg/proxy/token, pkg/proxy/translator, pkg/store, pkg/store/database, pkg/decision, pkg/ui, cmd (:28).
- **:30-31** `- **Pack B** = the module-anchored complement — **34 packages** @ e5f0816:` + `go list ./... | grep -vE 'github\.com/disillusioners/llm-supervisor-proxy/(cmd|pkg/(proxy|store|decision|ui))(/|$)'`
- **:32** `THE `(/|$)` SUFFIX IS THE LOAD-BEARING PART` (the end-anchor-only variant leaks Pack A into Pack B → 38 packages; the repo-relative variant is identical today).
- **:33** Pack B grew 32→33 (+`pkg/modelcache`)→34 (+`pkg/credclass`) — *"Re-measure; never copy a frozen count."*
- **:34-35** **BOUNDARY PATH SPEC**: the migration fence is `pkg/store/database/migrations/` (**80 files**), NOT the bare `migrations/` (0 files — *"it silently matches nothing, so an unregistered new migration would ship green"*) — `git --no-pager diff --stat $BASE..HEAD -- k8s/ .github/ pkg/store/database/migrations/ pkg/store/database/migrate.go pkg/config/`
- **:36** **config.json hash guard** — hash the ABSOLUTE real-home path on BOTH sides (`REAL_HOME_CONFIG` captured BEFORE `export HOME=$(mktemp -d)`).
- **:9-14** timeouts: script `timeout 120s`; go test `-timeout=110s` for unit packs; **release-gate slices use `-timeout=240s` inside the unchanged outer `timeout 300`** (pkg/proxy has outgrown 110s: 131s in the 2026-11-17 decision-groups gate, :20).
- **:62** `fe_vitest_suite` = `cd pkg/ui/frontend && timeout 300 npx vitest run`.
- **:231** all packs: go1.26.0 on PATH + `GOTOOLCHAIN=local` + pinned `GOMODCACHE/GOCACHE/GOPATH` + **isolated HOME** (`/tmp/gate-*`).

### Anti-vacuous rule (literal `--- PASS`)
- Normative examples: `PACKS.md:15` *"Anti-vacuous: 25 added tests (16 Pack A + 9 pkg/skipcache) each literal `--- PASS`"*; `:18` *"anti-vacuous **10/10 literal `--- PASS`**"*; `:20` *"anti-vacuous 74/74 added Go names each literal end-anchored `--- PASS` (64 A + 10 B; 0 MISS, 0 SKIP, each matched exactly once) + **48/48** FE case tokens each matched exactly once"*.
- Worked proof file: `.agents/tester/RESULTS/decision-groups-gate.md:38` (G4 row) and `:79-84` (*"## 2. Anti-vacuous proof — 🟢 GREEN (74/74 Go, 48/48 FE)"*, *"anti-vacuous OK: 74 / 74"*); FE anti-vacuous is a **PRESENCE** check — *"a MISS on any token ⇒ the gate fails"* (`.agents/tester/RESULTS/gate-scratch-decision-groups.md:216`).
- ⇒ **Rule: every ADDED `func Test…` must show a literal `--- PASS` line in the gate's OWN logs; every ADDED FE case token must appear exactly once in the FE log; a MISS = gate failure. Names that are added but never executed (0 `--- PASS`) are the classic vacuity failure.**

### Known-flake registry (`.agents/tester/QUARANTINE.md`) — what can make a gate red
| Row | Test | Why | Exposure |
|---|---|---|---|
| **:7** | `TestStoreEngine_CloseLifecycle` | harness/cleanup race — `t.TempDir()` RemoveAll `directory not empty` (engine janitor / WAL-shm residue); assertions never fail | pkg/store + Pack A |
| **:8** | `TestValidateUpstreamURL/SSRF_{octal,decimal,hex}_IP…` | env-deterministic on this VM | pkg/mcp |
| **:9** | `TestConcurrentIncrementModelUsage_Stress`, `TestConcurrentDifferentBuckets_Stress` | SQLITE_BUSY "database is locked (5)" | pkg/usage |
| **:10** | **`TestEngine_GetOrSelect_TOCTOU_CoolingReverify`** | Load-context TOCTOU timing race ("TOCTOU cooling leak: post-flag served A"), `pkg/credentiallb/cooldown_test.go:481` and `:517`; **fails ONLY in full-package context** (package-wide load widens the race window) — isolated 3P/3; "~1/5 under -race historically" | pkg/credentiallb (Pack B) — **NOT in Pack A** |
| **:11** | `TestHandleTestMCPServerDirect_RedirectTo{Localhost,PrivateIP}Blocked` | DETERMINISTIC third-party: httpbin.org now 403s redirect-to-internal server-side; fails BEFORE the proxy classifier (`pkg/mcp/handlers_api_test.go:1864/:1904`) | pkg/mcp (Pack B) — **the sole Pack B failure in every recent gate** |
| **:12** | `TestStoreEngine_PeakHourParity` | TIME-OF-DAY defect: half-open `[00:00,23:59)` at minute granularity ⇒ fires only 23:59:00–23:59:59.999 UTC (`pkg/store/database/store_test.go:482-495/:507`, `pkg/models/peak_hours.go:66-79`) | Pack A |
- Also relevant: **HOME isolation is mandatory** — `go test` without an isolated HOME rewrites `~/.config/llm-supervisor-proxy/config.json` (PORT=9797 leak from the daemon env; `APPLY_ENV_OVERRIDES` is set by pkg/proxy tests) ⇒ quarantine HOME before the full suite and hash-guard the real config (PACKS.md:36; project risk note "go test w/o HOME isolation rewrites …").

### Where each pin lives (EXISTING files — no new-file invention needed)

| Pin | Host file | Anchors |
|---|---|---|
| **decision GROUP-arm scope** (the defect: group arm bypasses the token scope) | **`pkg/decision/handler_group_test.go` EXISTS (1960 lines, 34 `func Test…`)** | harness `newGroupEnv` :175-259; token seeded at :182-187 (`AllowedModels: nil`); group row `ID: "decision-group"` :242; lane seam `stubGroupModels.GroupLane` :132-137; `newMemTokenStore()` :180; existing group tests :433-… (e.g. `TestDecision_Group_ResolvesToDecisionMember` :433) |
| decision non-group allow-list (403 shape to copy) | `pkg/decision/handler_test.go` | `TestHandler_Auth_TokenNotInAllowList_403` **:666-675** (sets `env.tokenStore.tokens["tok-test"].AllowedModels = []string{"some-other-model"}` :670 → expects 403 :672); `memTokenStore` :270-350 (CreateToken :296, UpdateTokenPermission :341); `sendModelNotAllowedError` `pkg/decision/handler.go:784` |
| chat lane scope | `pkg/proxy/handler.go:832-856` (allow-list evaluation; **client-requested id string** :823-839) | `pkg/proxy/handler_test.go` — `modelAccessMockTokenStore` :2105/:2121; group-arm misroute tests `pkg/proxy/handler_test.go:2425-2451`, `pkg/proxy/handle_chat_misroute_test.go:191-295` |
| anthropic lane scope | `pkg/proxy/handler_anthropic.go:514` (`IsModelAllowed`) | `pkg/proxy/handler_anthropic_test.go`, `pkg/proxy/handler_anthropic_tokenid_parity_test.go:230/:240`, `pkg/proxy/handler_anthropic_misroute_test.go:140-228` |
| imggen lane scope | `pkg/imggen/handler.go:268` (`IsModelAllowed`) | `pkg/imggen/handler_test.go:672` ("T1.7.2 — IsModelAllowed 403 when token has restricted list") |
| race coordinator (lane/decision gates) | `pkg/proxy/race_coordinator.go:1660/:1763` | `pkg/proxy/race_coordinator_test.go` |
| **token store (DB) pins** | **`pkg/auth/store_test.go` (816 lines)** | schema bootstrap `runTestMigrations` **:50-70** (hand-rolled DDL, `allowed_models TEXT` :60); `newTestDB` :19-47; existing allowed_models tests: `TestCreateTokenWithAllowedModels` :578, `TestValidateTokenReturnsAllowedModels` :604, `TestListTokensReturnsAllowedModels` :632, `TestGetTokenByIDReturnsAllowedModels` :686, `TestUpdateTokenPermissionWithAllowedModels` :714, `TestUpdateTokenPermissionClearAllowedModels` :750, `TestCreateTokenWithEmptyAllowedModels` :782 |
| token value helpers | `pkg/auth/token_test.go` | `TestScanAllowedModels` :104 (table-driven, :169-183); `TestAuthToken_IsModelAllowed` :191 (:287-292) |
| **migration pin (034 lands + column exists)** | **`pkg/store/database/migrations_test.go`** | `columnExists(t, db, table, column)` helper **:586**; `TestMigration029_ForwardFreshDB` :631; `TestMigration030_ForwardFreshDB` :676; `TestMigration031_ForwardFreshDB` :728 (schema_migrations row + column assertions :742-761) ⇒ **copy this shape for `TestMigration034_ForwardFreshDB`** |
| **pkg/store/database token pins** | **NONE EXIST for `auth_tokens`** — the auth SQL lives in `pkg/auth`; the only store-side auth_tokens surface is migration SQL + `pkg/store/database/sqlc/schema.sql:48-55` (stale snapshot) | — |
| **`/fe/api` handler pins** | **`pkg/ui/handlers_token_test.go` (296 lines)** | `mockTokenStore` :15-76 (CreateToken :35, UpdateTokenPermission :66-76 sets `token.AllowedModels` :74); `tokenTestServer` :79-95; PATCH tests :98 (404), :117 (invalid JSON), :179, :203, :220 |
| FE token-form pins | `pkg/ui/frontend/src/components/tokens/__tests__/TokenForm.test.tsx` (88 lines) | :41-85 the single test; **:84 `'Group One'` absent-assertion = the line that flips** |
| decorator (cache) pins | `pkg/modelscache/tokens_test.go` | `fakeTokenStore` :57/:104; stale-tier tests :220/:255/:280 |

### Interface-change blast radius — every `auth.TokenStoreInterface` implementor
Production: `pkg/auth/store.go:14` (TokenStore), `pkg/modelscache/tokens.go:50` (CachedTokenStore — CreateToken :263, UpdateTokenPermission :297).
Test mocks (each must be re-signed if a signature changes):
`pkg/decision/handler_test.go:296/:341` · `pkg/imggen/handler_test.go:175/:220` · `pkg/mcp/auth_test.go:29/:45` · `pkg/modelscache/tokens_test.go:57/:104` · `pkg/proxy/authenticate_test.go:47/:67` **and** `:348/:364` (slowMockTokenStore) · `pkg/proxy/handler_anthropic_tokenid_parity_test.go:230/:240` · `pkg/proxy/handler_test.go:2105/:2121` · `pkg/ui/handlers_imggen_test.go:740/:764` · `pkg/ui/handlers_token_test.go:35/:66`.
⇒ **9 test mocks + 1 decorator + 1 store.** House alternative to re-signing everything: the **optional-interface forward** precedent (`pkg/modelscache/models.go:1321-1341` `ClearGroupMemberCooldown` + `pkg/proxy/handler_helpers.go:899-901` type-assertion forward + the decorator-through test `pkg/proxy/win_clear_winner_identity_test.go:114-190`).

### Hand-rolled `auth_tokens` tables that MUST gain the new column (7 files)
Because the token store's SELECT/INSERT/UPDATE lists live in `pkg/auth/store.go`, adding `allowed_groups` to those lists breaks every test that hand-rolls the table:
`pkg/auth/store_test.go:53-62` · `pkg/mcp/auth_routing_test.go:58-67` · `pkg/mcp/e2e_test.go:72-81` · `pkg/mcp/handlers_api_test.go:49-58` · `pkg/mcp/mcp_endpoint_split_validation_test.go:60-69` · `pkg/mcp/test_helpers_test.go:43-52` · `pkg/proxy/handler_integration_test.go:52-59`.
(`pkg/ui/handlers_usage_test.go:33-…` creates `auth_tokens` **without** `allowed_models` — usage-join only, no token-store SQL ⇒ unaffected.)

---

## 7. TEST SCHEMA BOOTSTRAP (how a new column gets covered)

- **pkg/store/database (real migrations)**: helper `newSQLiteConnectionAtPath(dbPath)` — **`pkg/store/database/connection.go:102`** — then `store.RunMigrations(context.Background())` — **`pkg/store/database/migrate.go:86`** (pattern at `pkg/store/database/database_test.go:66-69`, `:85-87`, … and `pkg/store/database/migrations_test.go:641`, `:686`, `:738`). ⇒ **a 034 column is automatically present in every `pkg/store/database` test**; assert it with `columnExists(t, store.DB, "auth_tokens", "allowed_groups")` (`pkg/store/database/migrations_test.go:586`).
- **pkg/auth (hand-rolled)**: `newTestDB` (`pkg/auth/store_test.go:19-47`) → `runTestMigrations(db)` (**:50-70**) creates the DDL by hand ⇒ **the 034 column MUST be added to that literal** (or the test suite errors with "no such column: allowed_groups").
- **pkg/mcp / pkg/proxy (hand-rolled)**: the 6 tables listed in §6 — same obligation if the store SQL changes.
- **pkg/ui (mock)**: `pkg/ui/handlers_token_test.go` uses `mockTokenStore` (no DB) ⇒ no schema work, but the mock must carry the new field.
- **pkg/store/database SQLC snapshot**: `pkg/store/database/sqlc/schema.sql:48-55` is stale (no `allowed_models`) ⇒ not regenerated by the migrations; confirm it is out of the build (sqlc.yaml) before touching it.

---

## 8. WHAT THE PLAN MUST SPECIFY (derived, concrete)

1. **DB representation**: `auth_tokens.allowed_groups TEXT` — JSON array of **`models.id` values of `kind='group'` rows** (same namespace as `allowed_models`); `NULL` = "no group scope restriction" (mirrors `allowed_models`' NULL=allow-all, `pkg/auth/store.go:55-65`); malformed JSON ⇒ **fail closed** (mirror `ScanAllowedModels` `pkg/auth/token.go:62-66`).
2. **Migration 034**: sqlite + postgres `.up.sql` (shapes in §3), optional `.down.sql` (documentation-only, per 033 precedent), **registry append at `pkg/store/database/migrate.go:83`** with the sixth trap comment. Postgres form is idempotent (`IF NOT EXISTS`); sqlite is not (022/023 precedent) — idempotency comes from `schema_migrations`.
3. **Rollback**: additive column ⇒ image rollback is safe with the column present; DB rollback = restore the pre-migration backup (or operator-run `.down.sql` + delete the `schema_migrations` row). **No down-runner exists.**
4. **API contract**: `CreateTokenRequest` (+`allowed_groups`) and `UpdateTokenPermissionRequest` (+`allowed_groups`), both `*[]string` omitempty with nil=keep-existing (PATCH) / nil=allow-all (POST); response `TokenResponse` (+`allowed_groups`); **token values still never returned** except the one-shot `token` on POST.
5. **FE**: widen `TokenForm.tsx:34`; keep the wire shape flat (`string[]` of ids) or introduce a second field — the picker (`ModelMultiSelect`) emits ids only, so a group-vs-model split needs either two pickers or a split in the submit handler; update `types.ts:248`, `useApi.ts:899-903/:918-920/:927`, `TokenList.tsx` (prop type + inline editor :253), `SettingsPage.tsx:61/:63/:370-395/:744/:753`, `App.tsx:283/:285`.
6. **Test pins**: decision group-arm → `pkg/decision/handler_group_test.go`; token store → `pkg/auth/store_test.go` (+ `token_test.go`); migration → `pkg/store/database/migrations_test.go` (`TestMigration034_ForwardFreshDB` modelled on :676/:728); `/fe/api` → `pkg/ui/handlers_token_test.go`; FE → `TokenForm.test.tsx` (**rewrite :82-84**); plus the 7 hand-rolled `auth_tokens` DDLs and 9 interface mocks.

---

## 9. OPEN QUESTIONS (planner decisions needed)

1. **Semantic scope of `allowed_groups`**: group ids ONLY, or group ids **plus** decision models (the commission says the picker must "emit group ids + decision models")? If decision models ride in `allowed_groups`, the name is a misnomer and the decision-lane non-group path (`pkg/decision/handler.go:340-354`) would need rewriting too.
2. **Interaction with `allowed_models` in the chat lane**: chat evaluation uses the client-requested id string (`pkg/proxy/handler.go:823-839`); a token with `allowed_groups=[g1]` and EMPTY `allowed_models` is still allowed **every** chat model (`IsModelAllowed` allow-all at `pkg/auth/token.go:116-118`). Does `allowed_groups` merely *authorize group routing*, or must it also **restrict** the chat lane to group members?
3. **Where the check lives**: (a) only at the decision group arm (`pkg/decision/handler.go:323-326`, before `serveDecisionGroup` :324), or (b) per-layer (chat `pkg/proxy/handler.go:839`, anthropic `pkg/proxy/handler_anthropic.go:514`, imggen `pkg/imggen/handler.go:268`, decision `:343`) for every group id encountered?
4. **Members vs group scope**: must a decision group also be checked against `allowed_models` **per member** (a group whose members are all disallowed)? That is the deeper escape; `allowed_groups` alone does not close it.
5. **Migration naming**: `034_add_allowed_groups`? And should `.down.sql` files exist at all (033 precedent = documentation-only)? Confirm the exact file names before the registry append.
6. **Interface shape**: extend `CreateToken`/`UpdateTokenPermission` signatures (⇒ 9 mocks + decorator re-signed) vs a **new** method on `TokenStoreInterface` (same blast radius) vs the **optional-interface forward** precedent (`pkg/modelscache/models.go:1321-1341`) that keeps the mocks untouched.
7. **Cache semantics**: `CachedTokenStore.UpdateTokenPermission` drops the cached entry (`pkg/modelscache/tokens.go:297-304`) — the new field's write path must do the same, or a scope change will be masked for the TTL window (`TokensPositiveTTL`).
