# Decision Log: Model → Credentials Load Balancing

Branch: `feature/model-credential-load-balancing`
Base: `latest @ fea5874`
Author: planner[v2] via technical-analysis worker
Date: 2026-08-21
Status: Draft (ready for plan-phase worker)

This file records every design decision made for the feature, in order, with
options, trade-offs, the decision, and rationale. Research facts are cited
inline (`path:line`). Companion contract document: `technical-analysis.md`.

---

## Table of Contents

- [A. Conversation Identity](#a-conversation-identity)
- [B. Persistence Schema](#b-persistence-schema)
- [C. Selection & Affinity Engine](#c-selection--affinity-engine)
- [D. Backward Compatibility & Rollout](#d-backward-compatibility--rollout)
- [E. Testing Strategy](#e-testing-strategy)
- [Secondary Decisions](#secondary-decisions)

---

## A. Conversation Identity

> **AMENDED 2026-08-21 (architect council / leader rulings):** Two rulings apply here:
>
> - **A-1** — Key formula becomes `sha256(modelID + "|" + tokenID + "|" + firstUserMessage)`.
>   `tokenID` is **mandatory** (sourced from `rc.tokenID` at
>   `pkg/proxy/handler.go:401`, post-auth) and the **salt is the reason**, not the
>   detail. Original single-token fleet skew (templated-agent fleets sharing a
>   byte-identical first message collapse to ONE credential → silent LB defeat for
>   exactly the high-volume workloads that need spreading) was a critical blind
>   spot the council found; the E2E 100-unique-message test cannot detect it.
>   Anonymous/unauthenticated requests degrade to today's behavior (empty
>   `tokenID` component). Residual **within-principal** templated skew is
>   accepted and **documented** — operators running large same-token templated
>   fleets are told (docs) that weighted distribution assumes conversation
>   diversity under one principal.
> - **A-2** — Multimodal fallback to no-affinity is **removed**. The contract
>   now hashes the canonical JSON of the first-user-message content whether it
>   is a string OR a multimodal array (`[]interface{}`); precedent already
>   exists at `pkg/ultimatemodel/hash_cache.go:172-186` (~20 lines). This
>   satisfies requirement (1) as stated.
> - **Key-computation location moves**: `initRequestContext` runs at
>   `pkg/proxy/handler.go:353` BEFORE `rc.tokenID` is populated at `:401`. Key
>   computation therefore MUST move **post-auth**, not stay in
>   `initRequestContext` as the original contract claimed. (Original §A
>   "Where the key is computed" said "in the LB engine" — kept; the engine
>   receives the post-auth `(modelID, tokenID, firstUserMessage)` triple
>   from the wiring site at `:401+`.)

### Original (superseded, retained for decision-log history)

The original §A picked `sha256(model_id + "|" + first_user_message_content)`
with no token salt, multimodal-as-no-affinity fallback, and key computation
"in the engine" without specifying a post-auth wiring site. **That decision
is superseded** by the AMENDED block above. The full original reasoning is
preserved below marked **superseded**, then the amendment entry follows the
decision block.

### Problem

We need a "sticky key" that identifies the same conversation across multiple
sequential requests so the LB engine can pin a credential for the whole
conversation. The client sends the full message history on every request, but
no client-supplied conversation ID is forwarded through the proxy. The
existing `ultimatemodel.HashMessages` (`pkg/ultimatemodel/hash_cache.go:158-193`)
hashes the FULL message array over `role|content` per message → **changes
every turn**, so it is unusable as a sticky key.

> **REVISED 2026-08-21 (reviewer pass — #16f):** Add **one line** so the
> why-affinity-matters is *explicit*. Upstream provider prompt caches (e.g.,
> Anthropic's `prompt-caching` beta, OpenAI's automatic prefix cache) are
> **keyed per-credential** — when the same conversation lands on a
> different credential, the cached prefix is invalidated and the model
> re-processes the entire conversation from scratch. Conversation-sticky
> affinity is therefore *required* for any non-trivial prompt to keep
> its cache hot; weighted-without-affinity silently wastes provider
> cache budget on every conversation that crosses credential
> boundaries. This is the **load-bearing reason** the §A key formula
> exists.

### Options Considered

1. **`sha256(model_id + "|" + token_id + "|" + first_user_message_content)`**
   **(AMENDED — was Option 1 below, now the only viable pick).** Salts the
   key with the caller's principal. Distinguishes the same prompt across
   principals and bounds the templated-fleet skew to one principal at a
   time. Token sourced from `rc.tokenID` post-auth (`handler.go:401`).
2. **`sha256(model_id + "|" + first_user_message_content)`** (superseded,
   retained for decision-log history — see amendment entry at end of §A).
   - **superseded** Stable across turns (first user message is always the
     conversation starter for OpenAI/Claude-code-style clients).
   - **superseded** Includes model_id so two models receiving the same
     prompt do not collide.
   - **superseded** First user message can be empty (degenerate request)
     → fall back to weighted-random-without-affinity for that one request.
   - **superseded** Multicontent `[]interface{}` content falls back to
     weighted-random for the conversation — **REMOVED by A-2**; canonical
     JSON of the content array is now hashed.
3. **Longest-common-prefix of all user messages**
   - O(n²) per request, churns with edits, and degrades to no-prefix-match
     quickly as conversations grow.
4. **Explicit `x-conversation-id` client header (new)**
   - No header exists today; adding one is out of scope and would require
     client cooperation. Rejected.
5. **Hybrid: first user message + last user message hash**
   - Doubles the collision rate for the same data, no real benefit. Rejected.

### Decision

**Pick Option 1 (AMENDED): `sha256(model_id + "|" + token_id + "|" +
first_user_message_content)`**, with canonical-JSON hashing for multimodal
content (per A-2). Token salt is post-auth — see "Where the key is
computed" below.

### Rationale

- **Token salt is mandatory (A-1)**: `rc.tokenID` comes from
  `auth.AuthToken` at `pkg/proxy/handler.go:401`. Salting the key with the
  caller principal distinguishes same-prompt conversations across users
  AND bounds templated-agent-fleet skew to one principal at a time. The
  failure mode the salt prevents: **thousands of conversations sharing a
  byte-identical first user message (templated agents, batch evals, cron
  prompts, fixed `/command` openers) hash to one key → first weighted
  pick wins the binding → the entire templated fleet pins to ONE
  credential for 24h → weighted LB silently broken for exactly the
  workloads that need rate-limit spreading.** The original E2E
  distribution test uses 100 *unique* first messages and cannot detect
  this skew.
- **Residual within-principal skew (accepted, documented)**: a single
  principal's templated fleet (same token + same first message) still
  collides. This is now bounded by principal rather than global, and
  content-identical conversations genuinely share upstream cache
  benefit -- a defensible trade. **Operators running large same-principal
  templated fleets must be told** (docs) that weight distribution assumes
  conversation diversity under one principal.
- **Anonymous/unauthenticated requests** degrade gracefully -- `tokenID`
  component is empty in the hash input, matching today's behavior
  (unsalted key).
- **Multimodal hashing (A-2)**: canonical JSON of first-user-message
  content is hashed whether the content is a string or a `[]interface{}`
  multimodal array -- precedent at
  `pkg/ultimatemodel/hash_cache.go:172-186` (~20 lines). Removes an
  entire no-affinity class; satisfies requirement (1) as stated.
- **Convention**: OpenAI / Anthropic / Claude-Code-style clients always include
  the conversation-opening user message. The first user message in
  `rc.originalMessages` (`pkg/proxy/handler_functions.go:48-53`,
  `handler_helpers.go:47-48`) is the immutable snapshot taken at request entry
  — safe to hash from.
- **Model isolation**: Including `model_id` prevents two different models from
  colliding on the same prompt (e.g., user opens a chat with `gpt-4o` then
  switches to `claude-3.5-sonnet` with the same first message — each model
  must pin independently to its own credential bucket).
- **Truncation safety**: Claude Code and similar clients truncate OLDER
  messages from history when tokens get tight. As long as the *first* user
  message is preserved, the key is stable. If the client drops the first
  message entirely (rare, but possible if context grows enormous), the key
  changes — **we accept this as a "few % affinity misses on edge cases"
  (constraint from caller)** and do not add a secondary key.
- **No first message found** (degenerate request with only system/assistant
  messages, or empty `content`): the LB engine falls back to **weighted random
  without persistence** for that single request — no binding is stored, so the
  next request can still bind cleanly. (Multimodal content is **NOT** in this
  fallback — per A-2 it is canonical-JSON-hashed; only truly missing/empty
  content triggers the fallback.)
- **Reuse the marshaling pattern**: we deliberately do NOT reuse
  `HashMessages` (`pkg/ultimatemodel/hash_cache.go:158-193`) because it hashes
  the full role/content array and changes every turn. We borrow the
  `crypto/sha256 + hex.EncodeToString` style only.

### Where the key is computed (AMENDED — post-auth wiring site)

**The engine receives a `(modelID, tokenID, firstUserMessage)` triple
from the post-auth wiring site at `pkg/proxy/handler.go:401+`, not from
`initRequestContext`.** The engine itself computes the SHA256 inside
`pkg/credentiallb/key.go:ComputeConversationKey(modelID, tokenID,
firstUserMessage)`. Critical ordering (the original contract's key-
computation location was unimplementable as written because
`rc.tokenID` is unset at `initRequestContext` time):

1. `initRequestContext` runs at `pkg/proxy/handler.go:353` — at this
   point `rc.tokenID` is **NOT yet set**.
2. `rc.tokenID` is populated at `pkg/proxy/handler.go:401` (from
   `auth.AuthToken`).
3. Key computation therefore MUST happen **after** `:401`, either inline
   at the call site that has both `rc.resolvedModel.ID` and `rc.tokenID`,
   OR lazily at the first engine call inside the
   `ResolveInternalConfigWithAffinity` path. The original contract
   claimed "in the engine" but pointed at `initRequestContext` — that
   was unimplementable. The AMENDED contract pins the wiring site to
   post-auth.

Reasons for this location:

1. The engine still owns the key format and is the single point that
   needs it.
2. `initRequestContext` (`pkg/proxy/handler_functions.go:23-126`) is on
   the critical path of every request; pulling sha256 out of it keeps
   the boundary light. We pass `modelID string`, `tokenID string`, and
   `firstUserMessage string` into `Engine.GetOrSelect()` instead — the
   caller extracts `firstUserMessage` (a single map walk, cheap) and
   `tokenID` (already on `rc` post-auth) and the engine does the hash.

### Secondary Decisions Tied to A

- `firstUserMessage` extraction walks `[]interface{}` looking for the
  first map with `"role" == "user"` and a non-empty `"content"`. The
  helper returns the **canonical content** (not the raw map):
  - If `content` is a string → return the string as-is.
  - If `content` is `[]interface{}` (multimodal array) → return the
    canonical JSON (sorted keys, no whitespace) of the array (A-2).
    Precedent: `pkg/ultimatemodel/hash_cache.go:172-186` walks
    multimodal content deterministically (~20 lines).
  - If `content` is missing/empty/`null` → return `""` (no-affinity
    fallback for this request only).
- The conversation key is a **64-char hex SHA256** (no truncation). Same
  birthday-paradox reasoning as `HashMessages:156-157` (16 chars =
  collision at ~2^32 hashes; full 64 keeps the headroom).
- `ExtractFirstUserMessage` lives in `pkg/credentiallb/key.go` and is
  called from the post-auth wiring site (see "Where the key is computed"
  above), NOT from `initRequestContext`.

---

### §A Amendment Log Entry (2026-08-21 — leader-final)

| # | Amendment | Status | Rationale |
|---|-----------|--------|-----------|
| **A-1** | Key formula gains `tokenID` salt: `sha256(modelID + "|" + tokenID + "|" + firstUserMessage)`. Anonymous requests degrade to today's unsalted key. | **REVERSED** | Templated-agent fleets sharing a byte-identical first message collapse to one credential → silent LB defeat for exactly the workloads that need spreading. The E2E test with 100 unique first messages cannot detect this. Salt binds affinity to the principal. |
| **A-1-wiring** | Key-computation wiring site moved from `initRequestContext` (`:353`) to post-auth (`:401+`) where `rc.tokenID` is populated. | **REVERSED** | Original contract pointed at `initRequestContext` time; line-level ordering evidence (`handler.go:353` vs `:401`) shows the token is unset at that point. Implementer following the original contract would have produced a runtime nil/empty tokenID. |
| **A-2** | Multimodal no-affinity fallback **removed**. Canonical JSON of multimodal `[]interface{}` content is hashed instead. | **REVERSED** | Requirement (1) is "every request gets affinity". The "multimodal = no affinity" loophole violated it. Precedent at `hash_cache.go:172-186` shows the fix is ~20 lines. |
| **A-1-residual** | Within-principal templated skew is **accepted and documented**. | **DOCUMENTED** | A single principal's templated fleet (same token + same first message) still collides. Bounded by principal rather than global; content-identical conversations share upstream cache benefit. Operators with large same-token templated fleets are told (docs) that weight distribution assumes conversation diversity. |

**Original §A decision (superseded)**: `sha256(model_id + "|" + first_user_message_content)`, computed in the engine at `initRequestContext` time, with no token salt and multimodal-as-no-affinity fallback. The original reasoning is retained above marked "superseded" for decision-log history.

---

## B. Persistence Schema

> **AMENDED 2026-08-21 (architect council / leader rulings):** **M-1 —
> REVERSE the DROP COLUMN in migration 028.**
>
> The original §B picked Option 1 + 3 (JSON column on `models` AND
> drop `models.credential_id`). **That decision is REVERSED**. The
> AMENDED decision is: **ADD `credentials_json` + backfill + KEEP
> `models.credential_id` as a derived shadow** (one source of truth +
> one derived shadow, not two independently writable sources). The
> shadow is written in the same `UPDATE` statement that backfills
> `credentials_json`. Application reads **only** `Credentials` (the
> JSON-parsed shape); the write path maintains the shadow (~3 lines).
>
> **DROP INDEX + DROP COLUMN** is deferred to migration **029+**, gated
> on a release-note deprecation window. **029 must be scheduled as a
> tracked issue at merge time** -- it is load-bearing for M-1. Without
> 029, the dual-source debt the original §B was avoiding silently
> returns.
>
> **Reversal rationale (council-verified):**
>
> 1. **Old binary vs migrated DB = total breakage** -- the current
>    binary reads `credential_id` in `scanModels` and `QueryBuilder`;
>    rolling back the binary without running the down-migration
>    crashes model resolution.
> 2. **The down-migration is lossy** for multi-credential models (only
>    `credentials[0]` survives) -- rollback destroys operator config.
> 3. **OSS external surface** -- backup scripts and third-party tooling
>    may `SELECT models.credential_id`. Silent breakage on upgrade is
>    the worst failure mode for a self-hosted OSS project (no
>    telemetry, no error until a script fails).
> 4. **Empirical gap in atomicity claim** -- Postgres migration 024 does
>    NOT use file-level `BEGIN`/`COMMIT` (its only `BEGIN` is inside
>    PL/pgSQL). 028 would be the repo's FIRST file-level transactional
>    migration -- unproven through this runner + pgx-v5-`ExecContext`
>    pairing. Keep the transaction; make the **PG-gated 028 transaction
>    test mandatory**, not best-effort.
>
> **Dual-source-of-truth objection (original §B) is ANSWERED, not
> ignored**: M-1 is one source + one derived shadow. Divergence
> requires an out-of-band writer -- the same trust boundary the design
> already accepts for app-level FK integrity. The objection's force
> drops from "perpetual bug source" to "deprecation-window discipline".
> Down-migration 028.down becomes **lossless** (`credential_id =
> json_extract(credentials_json, '$[0].credential_id')`).

### Original (superseded, retained for decision-log history)

The original §B picked Option 1 + 3: ADD `credentials_json`, backfill,
and DROP `models.credential_id` to avoid a dual-source-of-truth. **That
decision is REVERSED** by the AMENDED block above. The full original
reasoning is preserved below marked **superseded**, then the reversal
entry follows the decision block.

### Problem

Today: `models.credential_id TEXT` is a single-credential FK-by-convention
(see `pkg/store/database/store.go:1132-1170`,
`pkg/models/config.go:89`). Validation (`store.go:1061-1063`) checks the
referenced credential exists; credential deletion is guarded by
`ErrCredentialInUse` (`store.go:1289-1295`).

We need to express an ordered, weighted list of credentials per model.

### Options Considered

1. **JSON column on `models`**: `models.credentials_json TEXT NOT NULL
   DEFAULT '[]'` storing `[{"credential_id":"x","weight":1,"position":0}, ...]`.
   - Aligns with house style (`fallback_chain_json`,
     `truncate_params_json`, `auth_tokens.allowed_models` per
     `pkg/store/database/migrations/sqlite/001_initial.up.sql:23-24` and the
     JSON precedent documented in migration 024
     (`.../sqlite/024_convert_allowed_models_to_ids.up.sql`)).
   - Single-row model config; no second table to keep in sync.
2. **Join table `model_credentials(model_id, credential_id, weight,
   position)`** (the feature request's recommended shape).
   - Foreign-key-ish integrity at the DB layer.
   - **But**: house style has zero join tables. `credentials.id` is referenced
     by `models.credential_id` and `configs.upstream_credential_id` only by
     app-level checks (`store.go:1061-1063`, `store.go:1289-1295`), not by
     actual FK constraints. Adding the first join table breaks precedent.
   - Migration 024 precedent
     (`.../sqlite/024_convert_allowed_models_to_ids.up.sql:18-31`) shows the
     SQLite/Postgres dialect asymmetry pain (recursive CTE vs DO-block) for
     any non-trivial per-row data migration. A backfill `INSERT INTO
     model_credentials SELECT ... FROM models WHERE credential_id != ''` is
     simpler, but the dual-source question (below) still applies.
3. **Drop `models.credential_id` AND adopt JSON column** (sub-option of 1).
   - Single source of truth. The migration is one-shot (backfill + drop
     column). The credential-in-use guard (`store.go:1289-1295`) is rewritten
     to scan `credentials_json` (a single JSON parse, in app code).
   - **SUPERSEDED** by M-1. The destructive DROP COLUMN is the failure mode
     the council reversed: old-binary-vs-migrated-DB total breakage;
     lossy down-migration for multi-credential models; OSS tooling/backups
     break silently.
4. **(AMENDED) ADD `credentials_json`, backfill, KEEP `credential_id`
   as derived shadow.** Application reads only `Credentials`; the write
   path maintains the shadow (~3 lines). DROP INDEX + DROP COLUMN
   deferred to migration 029+ gated on a deprecation window. **NEW
   pick.** Rationale: see "Reversal rationale" in the AMENDED block
   above.

### Dual Source-of-Truth Tension (AMENDED)

**Original objection** (still valid as stated, but its force is
**reduced**, not eliminated):

If we **keep `models.credential_id`** for backward compat AND add
`models.credentials_json` for multi-credential, every write path must keep
them in sync.

- `AddModel` / `UpdateModel` must:
  - If `credentials_json` provided → set `credential_id` =
    `credentials_json[0].credential_id`.
  - If only `credential_id` provided → synthesize `credentials_json` =
    `[{credential_id, weight:1, position:0}]`.
- The credential-in-use guard (`store.go:1289-1295`) must scan BOTH
  `credential_id` AND `credentials_json` to avoid orphan rows.
- A bug where the two diverge is **silent**: the LB engine reads one, the UI
  shows the other, and a request goes to a credential that "looks" deleted
  from the user's perspective.

**AMENDED answer (M-1):**

The objection is correct for two *independently writable* sources. M-1
is **one source + one derived shadow** -- the shadow is mechanically
written in the same `UPDATE` that writes the source. **Divergence now
requires an out-of-band writer** -- the same trust boundary the design
already accepts for app-level FK integrity (the original §B's own
precedent for `credentials.id` integrity). The objection's force drops
from "perpetual bug source" to "deprecation-window discipline until
migration 029 lands".

### Decision

**Pick Option 1 + 4 (AMENDED): JSON column on `models`, AND keep
`models.credential_id` as a derived shadow** (the AMENDED replacement
for the superseded Option 1 + 3). Migration 028 is the ADD + backfill
+ same-statement shadow write -- the DROP COLUMN moves to 029+ as a
tracked issue.

This is **the recommended-by-caller option evaluated and overruled**
by the AMENDED block above, specifically because the destructive DROP
COLUMN converts a recoverable change into an irreversible one for every
external consumer of the column.

### Rationale

- **Single source of truth (AMENDED)**: `models.credentials_json` is the
  only place credentials are stored. The LB engine, the validation
  function, the UI, and the credential-in-use guard all read the same
  column. **`models.credential_id` becomes a derived shadow** (read by
  legacy binaries and external tooling during the deprecation window,
  written in the same `UPDATE` statement as the backfill).
- **House-style alignment**: matches `fallback_chain_json`,
  `truncate_params_json`, `auth_tokens.allowed_models` -- all JSON arrays on
  a single row, no FK, app-level integrity.
- **Migration 028 is clean (AMENDED)**: one migration
  (`028_add_model_credentials.up.sql`) with three parts:
  1. `ALTER TABLE models ADD COLUMN credentials_json TEXT NOT NULL DEFAULT '[]'`
  2. Data backfill (dual-dialect -- see `technical-analysis.md` §Migration 028
     for the full SQL): for every row with `credential_id != ''',
     `credentials_json = json_array(json_object('credential_id', credential_id,
     'weight', 1, 'position', 0))`. **The same `UPDATE` statement ALSO
     writes the shadow `credential_id = credential_id` (no-op on the
     surface) so that the legacy single-column read path stays correct
     during the deprecation window.**
  3. **No DROP COLUMN.** `models.credential_id` and the index on it
     stay in place; their removal is deferred to migration **029+**
     behind a release-note deprecation window ("`credential_id` is
     derived; will be removed in the next minor"). **029 must be
     scheduled as a tracked issue at merge time -- load-bearing for
     M-1.** Without 029, the dual-source debt the original §B was
     avoiding silently returns.
- **Drop INDEX evaluation for 028**: the SQLite DROP-COLUMN-on-indexed-
  column restriction documented in the original §B is no longer in
  play because **028 does not DROP the column**. The index stays
  until 029. If a future PG/SQLite dialect quirk forces an index
  rebuild, drop only the index inside 028 (not the column) and keep
  the column -- but the current dialect targets (SQLite ≥ 3.35 via
  `modernc.org/sqlite` v1.46.1; pgx/v5) do NOT require this. **The
  bundled-SQLite-in-modernc.org/sqlite engine version and file-level
  BEGIN...COMMIT through pgx-v5-`ExecContext` pairing must be PROVEN
  by the mandatory PG-gated 028 transaction test -- the first
  file-level transactional migration in the repo.**
- **Validation moves to credentials_json**: `ModelsManager.Validate()`
  (`store.go:1027-1125`) parses `credentials_json`, builds the set of
  credential IDs, and verifies each one exists in `credentials`. If the list
  is empty AND the model is `Internal`, return `credential_id is required`
  error (the single-credential fast path becomes "exactly one entry with
  weight=1 in credentials_json", which the UI auto-fills when the user picks
  one credential).
- **Credential-in-use guard updates** to scan `credentials_json`:
  ```go
  for _, model := range modelList {
      for _, ref := range model.Credentials {
          if ref.CredentialID == id {
              return fmt.Errorf("credential '%s' is in use by model '%s': %w", id, model.ID, models.ErrCredentialInUse)
          }
      }
  }
  ```
- **Backward-compat impact (AMENDED)**: external tools that query
  `models.credential_id` continue to work during the deprecation
  window. Old binaries that read `models.credential_id` keep working
  (the shadow stays populated by every write path) and degrade
  gracefully: they lose LB but continue serving single-credential
  models. **Lossless down-migration**: `028.down.sql` re-reads
  `credentials_json[0].credential_id` back into `credential_id`; for
  multi-credential models this is the only credential that survives
  down-migration, but `credentials_json` itself is preserved (so a
  re-up restores the full list).

### Provider-match Validation

`ModelsManager.Validate()` (`store.go:1061-1063`) currently does NOT enforce
that `model.CredentialID` references a credential of the same provider. With
multi-credential, this becomes a real risk (cross-provider routing would
fail). **Decision**: add provider-match validation — every credential in
`credentials_json` must have the same `provider` as the model's "expected
provider" (derived from the first credential's provider at config time, or
the model's own `InternalModel`-inferred provider if available). UI must
also enforce same-provider in the multi-credential picker.

### Migration Numbering (AMENDED)

- `028_add_model_credentials.up.sql` (this feature). The migration is
  ADD + backfill + same-statement shadow write; **NO DROP COLUMN**.
- **`029_drop_credential_id.up.sql`** (deferred, **TRACKED ISSUE AT
  MERGE TIME** -- load-bearing for M-1). DROP INDEX + DROP COLUMN,
  gated on a release-note deprecation window. The original §B claimed
  "we do NOT split add/backfill/drop into multiple migrations because
  they MUST be atomic" -- that constraint no longer applies because
  028 no longer DROPs. The deprecation window lets external tooling
  (backup scripts, third-party SELECTs) keep working during the
  transition. **Without 029 ever landing, the dual-source debt the
  original §B was avoiding silently returns.**
- The single 028 migration remains atomic from the application's
  perspective (column added AND backfilled AND shadow maintained in
  the same `BEGIN...COMMIT`). If the binary starts after the column
  is added but before the backfill, the engine sees empty
  `credentials_json` on every row → every existing model becomes "no
  credential" → runtime crash. The transaction wrapper prevents this.

### Migration Runner Update

`pkg/store/database/migrate.go:24-52` (the ordered `migrations` array)
gains `{"028", "028_add_model_credentials.up"}` after `027`. Down
migrations follow the existing **023/024 precedent** —
`pkg/store/database/migrations/sqlite/023_add_ultimate_model.down.sql` and
`pkg/store/database/migrations/sqlite/024_convert_allowed_models_to_ids.down.sql`
both exist on disk for operator rollback reference, alongside their
respective `.up.sql` files. We provide `028_add_model_credentials.down.sql`
that re-adds the column (if absent in a future state) and pulls values from
`credentials_json[0].credential_id`. Down-migration is **lossless**
for single-credential models; for multi-credential models it preserves
`credentials_json` (so a re-up restores the full list) and writes the
primary back to the shadow.

> **REVISED 2026-08-21 (reviewer pass — #16b, #16c):** Two
> mechanical-correctness notes:
>
> - **023/024 are the precedent for `.down` files existing** at all; the
>   caller constraint is that **`.down` files are NOT auto-run by the
>   migration runner** (`pkg/store/database/migrate.go:62-67` `RunMigrations`
>   loops over the `migrations` array which contains only `.up`
>   names; `loadMigrationSQL` reads `{name}.sql` directly from the
>   `migrations` array entries, and the array holds no `.down` entries —
>   see `migrate.go:24-52`). `.down.sql` files exist only as **manual
>   rollback artifacts**: an operator copies the SQL into a
>   `psql`/`sqlite3` session, runs it, and (if migration tracking
>   requires) deletes the matching row from `schema_migrations`. The
>   runner neither reads nor applies them.
> - The 023/024 cite here is correct: both have `.down.sql` siblings
>   on disk that match the `.up.sql` filename pattern, and the runner
>   ignores them. The 028 `.down.sql` (lossless, M-1) is the same
>   shape — a manual rollback artifact, not a runner-driven path.

---

### §B Amendment Log Entry (2026-08-21 -- leader-final)

| # | Amendment | Status | Rationale |
|---|-----------|--------|-----------|
| **M-1** | REVERSE the DROP COLUMN. Migration 028 becomes ADD `credentials_json` + backfill + same-statement shadow write to `credential_id`. DROP INDEX + DROP COLUMN deferred to 029+. | **REVERSED** | Old-binary-vs-migrated-DB total breakage; down-migration is lossy for multi-credential models; OSS external tooling/backup scripts break silently; 028 would be the repo's FIRST file-level transactional migration (PG migration 024 only has BEGIN inside PL/pgSQL). |
| **M-1-tracked** | Tracked issue for migration **029_drop_credential_id** MUST be filed at merge time. | **TRACKED ISSUE** | Load-bearing for M-1. Without 029 landing, the dual-source debt the original §B was avoiding silently returns. |
| **M-1-test** | PG-gated 028 transaction test is **MANDATORY**, not best-effort. | **MANDATED TEST** | The bundled SQLite version inside `modernc.org/sqlite` v1.46.1 and file-level BEGIN...COMMIT through pgx-v5 `ExecContext` are inferred from 001--027 precedent, never empirically verified through this migration. The test must PROVE the transaction works on both dialects. |
| **M-1-lossless-down** | 028.down.sql is lossless: re-reads `credentials_json[0].credential_id` back into `credential_id`; for multi-credential models, `credentials_json` is preserved (re-up restores the full list). | **UPDATED** | Reverses the lossy original down-migration that destroyed operator config on rollback. |

**Original §B decision (superseded)**: Option 1 + 3 (JSON column on `models` AND drop `models.credential_id` in 028). The original reasoning is retained above marked "superseded" for decision-log history.

---

---

## C. Selection & Affinity Engine

### Package Layout

#### Options

1. **New package `pkg/credentiallb/`** with `engine.go`,
   `selector.go` (weighted random), `store.go` (in-memory binding map +
   TTL).
2. **Extend `pkg/models`** with a `CredentialBalancer` struct on
   `ModelsConfig`.
3. **Extend `pkg/proxy`** with the engine inline in handler.go.

#### Decision

**Pick Option 1: new `pkg/credentiallb/`**.

#### Rationale

- `pkg/models` is for the *static, serializable* data types. A stateful
  in-memory engine with goroutines and TTL is a different concern.
- `pkg/proxy` is the request hot path; putting engine code inline pollutes
  it.
- A new package gives the engine a stable import path for tests
  (`pkg/credentiallb/*_test.go`) without pulling in the proxy package's
  fixtures.

### Weighted Random Algorithm

#### Options

1. **Efraimidis-Spirakis (ES)**: For each selection, generate
   `key_i = -ln(u_i) / weight_i`, pick max. Elegant, single pass, but
   requires float math per selection (k random numbers + k log calls per
   pick) and a tie-breaking rule.
2. **Cumulative-sum with prefix sums**: Pre-compute prefix sums
   `[w₁, w₁+w₂, ..., Σw]`. Per selection, pick `r = rand.Intn(Σw)`, binary
   search the prefix for `r`. O(k) build + O(log k) select.

#### Decision

**Pick Option 2: cumulative-sum with prefix sums**.

#### Rationale

- k per model is small (typical 2-5). O(k) build + O(log k) select is fast
  in absolute terms even at 10k req/s.
- No float math → no `-ln(u)` precision concerns, easier to test
  deterministically (seedable RNG).
- Prefix sums are rebuilt on config change (model credential list / weight
  update) — cheap, infrequent.
- Binary search is well-understood; no need to explain ES to the team.

### TTL & Eviction

#### Options

1. **Lazy expiry only**: check `now - boundAt > ttl` on each `GetOrSelect`
   call, evict inline. No background goroutine.
2. **Background janitor goroutine**: sweep every N seconds, evict any entry
   older than ttl. House style has no janitor (verified by grep — only
   `loopdetection` and `usage.Counter` have goroutines, neither with TTL).
3. **Hybrid**: lazy expiry on lookup + background janitor for size cap.

#### Decision

**Pick Option 3: hybrid (lazy + background janitor)**.

#### Rationale

- Lazy expiry covers correctness (no stale binding is ever returned).
- A pure-lazy approach can leak memory if a conversation starts, binds,
  then never returns: the binding sits there forever. A long-running proxy
  with millions of short conversations would accumulate.
- Janitor interval: 5 minutes (cheap; the map is bounded by
  active-conversations × TTL, so janitor is mostly a safety net). Default
  TTL: **24 hours** as a **sliding idle ceiling** — the `boundAt` field on
  each binding is **REFRESHED on every in-TTL `GetOrSelect` hit** (a
  conversation that keeps making requests stays bound indefinitely while
  active; the binding only expires after 24h of **idle**, not after 24h
  from first selection). Tunable via engine constructor parameter for tests.

### Sliding-TTL semantics (NEW 2026-08-21 — #10, leader-ruled)

> **REVISED 2026-08-21 (reviewer pass — #10):** The original §C and §A
> framed the TTL as a fixed ceiling from first selection ("24h binding
> lifetime", "expires after TTL"). That reading is **wrong** for an
> active-conversation workload: a Claude Code session that keeps
> requesting for 25 hours would lose its credential mid-flight under
> fixed-TTL semantics, breaking upstream prompt-cache affinity for the
> exact conversations the feature is designed to support. The amended
> semantics are **sliding-TTL-on-idle**:
>
> - `boundAt` is **refreshed on every in-TTL `GetOrSelect` hit** for the
>   same `(modelID, conversationKey)` — the binding slides forward by
>   another TTL window each call.
> - The binding is **eligible for expiry only when `now - boundAt > ttl`**
>   (i.e., 24h of consecutive idle).
> - The **janitor** sweeps any binding whose `boundAt + ttl < now`
>   (idle exceeding the ceiling) — janitor is a **bound-protection**
>   safety net, not the primary eviction mechanism.
> - **Lazy expiry** at lookup time applies the same `now - boundAt > ttl`
>   check, so a hit on a binding that has been idle > 24h drops it
>   immediately and picks a fresh credential.
> - **Interaction with E-2 filter-survivors (OnModelChanged)**: the
>   `boundAt` of a surviving binding is **NOT touched** by
>   `OnModelChanged` — the binding keeps its sliding idle window.
> - **Net effect**: a long-running active conversation stays bound
>   forever; a once-then-silent conversation drops after 24h idle; a
>   key that never re-appears drops bound reclaim cost on the very first
>   lookup that misses it (lazy expiry).
> - **Decisions §A lifetime language**: the §A rationale that mentioned
>   "24h → weighted LB silently broken for exactly the high-volume
>   workloads that need spreading" is **unaffected** — the templated-fleet
>   concern is about first-selection skew, not TTL expiry. The 24h
>   ceiling is now a /idle/ ceiling, not a /lifetime/ ceiling.

### Concurrency Model

**Decision**: single `sync.RWMutex` on the outer map
`map[modelID]map[conversationKey]*binding`. Read-locked on `GetOrSelect`
(99% of the case). Write-locked only on first binding, on weight change
(rebuilds prefix sums), and on janitor sweep.

- Inner per-model map: separate `sync.RWMutex` per model entry so a weight
  change on model A doesn't block model B reads. Confirmed pattern: matches
  `pkg/models/credential.go:133-147` (outer RWMutex + map) and
  `pkg/ultimatemodel/hash_cache.go:14` (RWMutex + slices).
- **No `sync.Map`** — house style is explicit RWMutex. `sync.Map` is
  optimized for write-once-read-many, which is the OPPOSITE of our access
  pattern (we rebuild on weight change, hot reads otherwise).

### Invalidation: Config-Change Propagation

#### Options

1. **Subscribe to existing event bus** (`pkg/events/bus.go`): when a model's
   credentials change, the engine receives an event and drops matching
   bindings.
2. **Direct method call from `ModelsManager`**: every
   `AddModel/UpdateModel/RemoveModel` invokes `Engine.OnModelChanged(modelID)`.
3. **Polling**: engine polls the store every N seconds.

#### Decision

**Pick Option 1 (event bus) with a fallback to direct call (Option 2) for
the migration-028 backfill case**.

#### Rationale

- The event bus exists (`pkg/events/bus.go`), is injected into
  `proxy.NewHandler` (`cmd/main.go:108` per research), and is the same
  mechanism `ultimatemodel.Handler.OnConfigChange` uses
  (`pkg/ultimatemodel/handler.go:150-160`). Reuse is preferred.
- **Tech-debt caveat noted** (not blocking): the existing publish at
  `pkg/config/config.go:518` publishes a `config.updated` event with the
  WHOLE config object as `Data`; the existing subscriber
  (`pkg/ultimatemodel/handler.go:155`) checks for a `field` key that is
  NEVER set by that publisher — so the existing ultimate-model reset path
  may be broken. We do NOT fix that here; we DO add a fresh event
  (`model.credentials.changed`, typed `Data` with `model_id`) that the
  engine subscribes to. The publisher lives in `ModelsManager.UpdateModel`
  (we control).
- Direct call (Option 2) covers the migration-028 backfill: after the SQL
  backfill runs, `cmd/main.go` startup calls `Engine.RebindModel(modelID,
  newCredentials)` for each row. This is the only path that runs at
  startup before the bus is wired.

### Restart Stickiness

#### Options

1. **Persist bindings** to `model_credential_bindings(model_id,
   conversation_key_hash, credential_id, bound_at)` with lazy write-behind
   or immediate UPSERT per first selection.
2. **In-memory only + TTL**: bindings die on restart; first request of each
   conversation post-restart re-selects.

#### Decision

**Pick Option 2 for v1 (in-memory + TTL). Defer Option 1 to a follow-up.**

#### Rationale

- Per-request UPSERT precedent exists at `pkg/usage/counter.go:49-67`
  (immediate UPSERT into `token_hourly_usage`). Per-request DB writes are
  acceptable in this codebase.
- BUT: persistence introduces complexity that is not justified by the v1
  feature request:
  - Bindings must be loaded on startup (per-model + per-conversation).
  - The conversation key is `sha256(first user message)` — we cannot
    reverse it. If we store the hash, we must also store the bound
    credential_id keyed by the hash. On restart, the proxy has no way to
    know which incoming requests belong to which persisted binding except
    by recomputing the hash — which is exactly what the runtime does
    anyway. So persistence does not save us from the lookup cost; it only
    saves us from the rare event of a binding TTL expiring mid-conversation
    due to a 30-second restart.
  - Cross-restart consistency: if a credential is deleted while the proxy
    is down, a stale binding points to a non-existent credential. The
    engine would have to fall back gracefully — same fallback logic as
    during a normal credential deletion event.
- **Accepted behavior documented**: a restart breaks affinity for any
  conversation whose binding's TTL would have outlived the restart. For
  24-hour TTL and a typical 30-second restart, this is essentially zero
  impact in practice.

### Sub-Decision: Persistence Type

`ModelConfig.Credentials` field shape:

```go
type CredentialRef struct {
    CredentialID string `json:"credential_id"`
    Weight       int    `json:"weight"`         // default 1; must be > 0
    Position     int    `json:"position"`        // deterministic order; 0-based
}

type ModelConfig struct {
    // ... existing fields ...
    Credentials []CredentialRef `json:"credentials,omitempty"` // ordered list; weight+position
    // credential_id REMOVED — see Decision B
}
```

The `position` field is used to break weight ties deterministically (pick
the lower-position credential on equal weight). The `weight` field is
validated `> 0` at write time (UI + `ModelsManager.Validate`).

---

## D. Backward Compatibility & Rollout

### Problem

Four code paths read a credential today:

1. **Race-internal** (`pkg/proxy/race_executor.go:137`):
   `cfg.ModelsConfig.ResolveInternalConfig(req.modelID)` → returns
   `(provider, apiKey, baseURL, internalModel, ok)`.
2. **Race-external** (`pkg/proxy/race_executor.go:282-300`): reads
   `cfg.UpstreamCredentialID` (env-only).
3. **Ultimate-internal** (`pkg/ultimatemodel/handler_internal.go:46`):
   `h.modelsMgr.ResolveInternalConfig(modelCfg.ID)`.
4. **Ultimate-external** (`pkg/ultimatemodel/handler.go:287-293`): reads
   `modelCfg.CredentialID` for **provider detection only** — does NOT
   inject credentials into the request; passes client auth through.

### Decision

| Path                  | Decision | Why |
|-----------------------|----------|-----|
| Race-internal         | **Participate in LB** | Replace `ResolveInternalConfig(req.modelID)` with `ResolveInternalConfigWithAffinity(req.modelID, conversationKey)`. Multi-credential models LB; single-credential models behave identically (fallback path returns existing result). |
| Race-external         | **UNCHANGED**         | `UpstreamCredentialID` is env-only and global; the caller constraint says "Keep UPSTREAM_CREDENTIAL_ID env path unchanged." |
| **Anthropic `/v1/messages` internal** **(NEW 2026-08-21 — C1, leader-ruled)** | **Participate in LB** | Reached via `pkg/proxy/handler_anthropic.go:340` `doAnthropicInternalRequest` (called at `:447`) → `InternalHandler.HandleRequest` at `pkg/proxy/internal_handler.go:67` (constructed via `NewInternalHandler` at `handler_anthropic.go:461`). Route registered at `cmd/main.go:139` (`mux.HandleFunc("/v1/messages", proxyHandler.HandleAnthropicMessages)`). This is the **PRIMARY client endpoint** for Claude Code, and the same credential-injection seam applies — in fact, the call currently calls `h.resolver.ResolveInternalConfig(h.config.ID)` at `internal_handler.go:69`, which is precisely the 5-tuple surfaced for the LB-aware variant. **This path is IN Phase 3 scope** alongside race-internal and ultimate-internal. C1 amendment: the thread is `anthropicRequestContext.conversationKey` (new field per #16(a) C1+ harmonization) → `InternalHandler.HandleRequest` takes a new `conversationKey string` arg → `ResolveInternalConfigWithAffinity(model.ID, conversationKey)`. Existing 5-tuple `ResolveInternalConfig` at `internal_handler.go:69` is replaced by the LB-aware variant when the engine is wired. |
| Ultimate-internal     | **Participate in LB** | Same `ResolveInternalConfig` call site (`pkg/ultimatemodel/handler_internal.go:46`) → same replacement. Ultimate model is just another internal model with its own config; if a user gives it credentials, they want LB. |
| Ultimate-external     | **UNCHANGED**         | Reads `modelCfg.CredentialID` only for provider detection; the proxy does NOT inject credentials. No LB needed. We can keep this read site using a backward-compat shim: `getEffectiveCredentialID(modelCfg)` returns `modelCfg.Credentials[0].CredentialID` when present, else "" (since we drop `modelCfg.CredentialID`, the existing line `modelCfg.CredentialID != ""` becomes a check against the first credential — but for ultimate-external, only provider detection matters, not authentication). |

> **REVISED 2026-08-21 (reviewer pass — C1):** The original §D listed three
> call sites (`race-internal`, `ultimate-internal`, `ultimate-external`)
> plus the `race-external` env-only path. The Anthropic `/v1/messages`
> internal path was **omitted** — it is the **primary client endpoint**
> for Claude Code and the LB must work there. C1 amendment: the
> `/v1/messages` internal path is added to the LB-participating list; the
> thread is `anthropicRequestContext.conversationKey` →
> `InternalHandler.HandleRequest` → `ResolveInternalConfigWithAffinity`.
> Verified file:line: `cmd/main.go:139` (route), `handler_anthropic.go:340`
> (call into `doAnthropicInternalRequest`), `handler_anthropic.go:447`
> (function def), `handler_anthropic.go:461` (`NewInternalHandler`
> construction), `internal_handler.go:67` (HandleRequest signature),
> `internal_handler.go:69` (current 5-tuple `ResolveInternalConfig` call
> — this is the seam to replace).

### Single-Credential Fast Path (Backward Compat)

For a model with exactly one credential in `credentials_json`:

- LB engine returns that credential on every request (deterministic, no
  weighted random needed).
- Affinity map entry is created once (first request) and pinned for the
  **sliding idle TTL** (default 24h of idle, refreshed on every hit; see
  §C "Sliding-TTL semantics" NEW 2026-08-21 #10) — this is functionally
  identical to today's behavior (no LB, single credential always used).

The fast path is selected inside the engine via a length check on the
configured credentials slice; no special-case logic in callers.

### Mid-Conversation Ultimate Switch and Affinity

When the primary request triggers ultimate-model switching, the LB engine
runs twice on the same conversation:

- **Primary side**: `GetOrSelect(primaryModelID, conversationKey)` →
  picks `primaryCredentialA` and pins.
- **Ultimate side** (after switching): `GetOrSelect(ultimateModelID,
  conversationKey)` → picks `ultimateCredentialX` and pins **independently**.

**Decision**: ultimate affinity is a separate bucket. Rationale:

- The conversation key includes `model_id` (Decision A). After switch,
  `model_id` is the ULTIMATE model's ID — different bucket, fresh selection.
- This is what users want: ultimate model has its own provider pool with
  its own credentials; affinity within each pool is preserved.
- The "few % affinity misses on edge cases" constraint allows for the
  first post-switch request to land on a fresh credential on the ultimate
  side; subsequent ultimate-side requests stick.

### Rollout (AMENDED — M-1)

- **Single binary rollout**: no feature flag. Migration 028 runs on
  startup; all existing models with `credential_id` get backfilled into
  `credentials_json` AND `credential_id` is **kept as a derived
  shadow** (same-statement UPDATE writes both). **NO DROP COLUMN in
  028.** Old binaries continue to work (read `credential_id` from the
  shadow); new binaries read `credentials_json` and ignore the shadow.
- **029+ DROP COLUMN rollout** (deferred, tracked issue at merge time,
  load-bearing for M-1): gated on a release-note deprecation window
  ("`credential_id` is derived; will be removed in the next minor").
  External tooling (backup scripts, third-party SELECTs) keeps working
  during the window.
- **UI rollout**: ModelsTab + ModelForm.tsx gains a "Credentials"
  multi-select picker alongside the existing single-credential
  dropdown. The existing dropdown stays as a "Primary credential"
  shortcut that maps to `credentials[0]`.
- **No data loss**: every existing model with `credential_id != ''`
  becomes `credentials_json = [{credential_id, weight:1, position:0}]`
  AND the shadow `credential_id` is unchanged. Verified zero-change
  behaviorally.

---

## E. Testing Strategy

> **AMENDED 2026-08-21 (architect council / leader rulings):** Two
> rulings apply here:
>
> - **PG-gated 028 transaction test is MANDATORY** (not best-effort).
>   028 is the repo's FIRST file-level transactional migration (PG
>   migration 024 does NOT use file-level BEGIN/COMMIT -- its only
>   BEGIN is inside PL/pgSQL). The bundled SQLite engine version
>   inside `modernc.org/sqlite` v1.46.1 and file-level
>   BEGIN...COMMIT through pgx-v5 `ExecContext` pairing must be
>   PROVEN by this test, not assumed. See test #1 under "Migration
>   Tests" below.
> - **Empty-key semantics are pinned to: NO binding stored, fresh
>   weighted pick per request.** Added as test #8 (the "no hotspot"
>   regression test). The technical-analysis reading of "" as "its
>   own bucket" is removed.

### Unit Tests (`pkg/credentiallb/*_test.go`)

1. **Weighted distribution sanity** (`engine_test.go`):
   - Configure weights `[1, 1, 2]` for 3 credentials.
   - Run `GetOrSelect` 10,000 times with unique conversation keys.
   - Assert per-credential selection counts within ±5% of expected
     (1/4, 1/4, 2/4 → 2500, 2500, 5000 with tolerance [2375, 2625] and
     [4750, 5250]). Use a fixed `math/rand` seed for determinism.
   - Equivalent of a chi-square goodness-of-fit with very loose bounds --
     we are NOT testing RNG correctness, only that the engine respects
     weights.
2. **Affinity** (`engine_test.go`):
   - Configure 3 credentials.
   - Call `GetOrSelect(modelID, "conv-A")` 100 times -- assert all 100
     return the same credential.
   - Call `GetOrSelect(modelID, "conv-A")` then change weights → assert
     next call returns a credential consistent with the NEW weights (this
     is the invalidation test, see #4).
3. **TTL expiry** (`engine_test.go`):
   - Construct engine with `ttl=100ms`.
   - Bind at `t=0`. Wait 150ms. Call again → assert binding is gone (new
     credential selected).
   - Construct engine with `ttl=24h` and run janitor sweep manually →
     assert no eviction.
4. **Weight change invalidation** (`engine_test.go`):
   - Bind 3 conversations to credential-A under weights `[1, 1, 1]`.
   - Call `Engine.OnModelChanged(modelID)` (simulating a config change
     that drops credential-A from the list).
   - Call `GetOrSelect` for the same 3 conversations → assert they now
     select from the new credential set, and the OLD binding is forgotten.
5. **Race detector** (`engine_test.go`):
   - Run with `go test -race ./pkg/credentiallb/...`.
   - 100 goroutines, 1000 calls each, on the same model -- assert no data
     race report, no panics, all goroutines see a consistent credential
     per conversation key.
6. **Empty credential list** (`engine_test.go`):
   - Configure model with `credentials = []`. `GetOrSelect` returns
     `("", false, ErrNoCredentials)`. Caller (the proxy) treats this as
     a 500 with a clear error.
7. **Single-credential fast path -- NO MAP WRITES (AMENDED — E-3)**:
   - Configure model with `credentials = [{id:"x", weight:1, position:0}]`.
   - 1000 calls with random conversation keys → all return `"x", false`.
   - **Assert the binding map size is 0** (test-only counter on internal
     map size). The single-credential fast path does **no map writes**;
     the rule is in favor of `decisions.md §E #7` and success criterion
     #5 over `phase2-plan.md` Task 4's "write-once binding" -- this test
     enforces the no-map-writes invariant.
8. **Empty-key semantics (AMENDED — W-2)**:
   - Configure model with 3 credentials `[1, 1, 1]`.
   - Run `GetOrSelect(modelID, "")` 1000 times.
   - **Assert per-credential selection counts match the configured
     weights** (loose bounds, ±5%).
   - **Assert no binding is ever stored** for the empty key (test-only
     counter on internal map size remains 0).
   - This is the regression test that pins the "no hotspot" semantics;
     the ""-as-own-bucket reading is rejected.
9. **`newlyBound` bool signal (AMENDED — W-1)**:
   - Configure model with 3 credentials.
   - First call to `GetOrSelect(modelID, "conv-A")` returns
     `(<some id>, true, nil)`.
   - Second call to `GetOrSelect(modelID, "conv-A")` (same key) returns
     `(<same id>, false, nil)`.
   - After `OnModelChanged` that drops the credential: next call returns
     `(<new id>, true, nil)`.
   - The `true` on the FIRST call is the only signal that lets the caller
     publish the `model_credential_selected` event only on first binding.
10. **`OnModelChanged` filter-survivors (AMENDED — E-2)**:
    - Bind 3 conversations to credential-A and credential-B under
      weights `[1, 1]`.
    - Call `Engine.OnModelChanged(modelID, []CredentialRef{{A, 1, 0},
      {B, 1, 1}, {C, 1, 2}})` (adds C, keeps A and B).
    - Assert the 6 bindings (3 conv × 2 creds) are still in the map
      (not cleared); C is in the new selector.
    - Call `Engine.OnModelChanged(modelID, []CredentialRef{{A, 1, 0}})`
      (drops B and C).
    - Assert the 3 A-bound conversations are still in the map; the 3
      B-bound conversations are gone.
11. **Janitor panic-recovery (AMENDED — E-4, recommended)**:
    - Construct engine with an injected `panicFunc` that panics on the
      first sweep.
    - Assert the engine logs the panic, recovers, and continues sweeping
      on the next interval (no silent-stop leak).
12. **`Engine.Stats()` returns counts (AMENDED — E-4, recommended)**:
    - After running affinity test #2, `Stats()` returns
      `{bindings_total: 1, hits_total: 99, misses_total: 1}` (or similar
      shape) for the model.

### Integration Tests (`pkg/proxy/handler_*_test.go`)

1. **Multi-credential end-to-end** (`handler_integration_test.go`):
   - Set up two mock providers on ports 4001 and 4002 (existing
     `pkg/proxy/test_helpers.go` pattern).
   - Configure a model with `credentials = [{4001, 1, 0}, {4002, 2, 1}]`.
   - Send 100 requests with random first-user messages → assert
     distribution roughly 33/67 between 4001 and 4002 (loose bounds).
   - Send 5 requests with the same first-user message → assert all 5 hit
     the same mock provider.
2. **Backward compat** (`handler_integration_test.go`):
   - Configure a model with exactly one credential. Send 10 requests →
     assert behavior is byte-identical to the pre-LB baseline (compare
     wire-level request bytes via the mock provider's recorded bodies).

### Migration Tests (`pkg/store/database/*_test.go`)

1. **Backfill correctness + TRANSACTION (AMENDED — M-1 test,
   MANDATORY)** (`migrate_test.go`):
   - **PG-gated path is MANDATORY, not best-effort.** Spin up a
     PostgreSQL test container (existing test setup); without a
     passing PG-gated transaction test, the migration is NOT approved.
   - Pre-populate a test DB (SQLite AND Postgres) with `models` rows
     where `credential_id = 'cred-X'` for some rows and `credential_id
     = ''` for others.
   - Run migration 028 wrapped in file-level `BEGIN...COMMIT`.
   - **Transaction assertion**: assert the SQLite `BEGIN TRANSACTION`
     / `COMMIT` actually commits AND that the file-level pairing
     through pgx-v5 `ExecContext` on PG also commits. Verify by
     inspecting the post-migration `credentials_json` AND `credential_id`
     shadow state.
   - **Backfill assertion**: assert every row with `credential_id !=
     ''` has `credentials_json = '[{"credential_id":"cred-X","weight":1,"position":0}]'`.
     Assert `credential_id` column still exists (NOT dropped by 028).
   - **Shadow assertion**: assert `credential_id` value still equals
     the original (the same-statement shadow write kept it correct).
   - **No-DROP assertion**: assert `PRAGMA table_info(models)` still
     shows the `credential_id` column.
   - **Down-migration round-trip (lossless)**: run `028.down.sql`,
     assert `credential_id` is repopulated from
     `credentials_json[0].credential_id`, and `credentials_json`
     itself is preserved (the down-migration does NOT drop
     `credentials_json`).
2. **Edge case: model with `credential_id = ''`** — assert
   `credentials_json` stays `'[]'` after migration; shadow
   `credential_id` stays `''`.
3. **Multi-credential backfill (AMENDED — M-1)**:
   - Manually set `credentials_json = '[{A,1,0},{B,1,1}]'` on a row,
     then run `028.down.sql`, then `028.up.sql` again.
   - Assert round-trip: `credentials_json` and the shadow
     `credential_id` (= `A`) survive the round-trip.

### Backward Compat Tests

1. **Credential-in-use guard** (`store_test.go`):
   - Add credential `X`. Add model with `credentials = [{X, 1, 0}]`.
     Attempt to delete credential `X` → assert `ErrCredentialInUse`.
   - Add credential `Y` not referenced → delete succeeds.

### E2E Script (`test/test_mock_credential_lb.sh`)

Mirrors `test/test_mock_ultimate_model.sh` (565 lines, mock upstream on
port 4001, proxy on 4322).

1. Start two mock upstreams (4001, 4002) that each record incoming
   requests and assert the upstream API key header matches their
   credential.
2. Start the proxy on 4322 with a config that defines a model with
   `credentials = [{4001_cred, 1, 0}, {4002_cred, 2, 1}]`.
3. **Affinity test**: send 5 chat completions with the same first user
   message ("What is 2+2?") → assert all 5 hit the same mock upstream
   (record which upstream each request landed on; assert single upstream
   name in the result set).
4. **Distribution test**: send 100 chat completions with unique first user
   messages → assert the distribution between the two mocks is within
   tolerance (4001 in [25, 50], 4002 in [50, 75] for weights 1:2).
5. **Credential provider check**: configure two credentials with
   mismatched providers → assert the UI blocks the save (POST
   `/fe/api/models/validate` returns 400 with provider mismatch error).
6. **Backward compat**: configure a model with a single credential → send
   10 requests → assert behavior identical to the pre-LB baseline (mock
   upstream receives requests with the expected credential).

---

## F. Rate-Limit Credential Failover (Round 3 — 2026-08-25)

> **AMENDED 2026-08-25 (Round 3 — Rate-Limit Failover):** This section is
> a **surgical, append-only** amendment per the user's verbatim
> requirement (recorded below) and the eight pre-pinned dispatcher
> rulings (R3-1..R3-8). Sections A–E and the Secondary Decisions are
> UNCHANGED. R3-4 includes the full alternatives table (the only ruling
> in this round where the house style demands one). The companion
> contract additions live in `technical-analysis.md` under the API
> Contract region, the new "Rate-Limit Failover Precedence Tree"
> section, and the Concurrency / Backward-Compat subsections.
>
> **User requirement (verbatim, 2026-08-25):**
> *"One more requirement that: for error case 'rate limiting', we switch
> to another credential of the same provider first before try reach
> another model. Loadbalance and HA in the case rate limiting on same
> kind of provider (example user can add 3 minimax credential, if one got
> rate limited, we just use another minimax credential)."*
>
> **Pre-pinned rulings (dispatcher)** — semantics are FIXED, this section
> elaborates rationale + alternatives; a sibling worker amends the phase
> files against the same rulings.

### F.1 Rate-Limit Classifier (R3-1)

**Ruling.** Add a rate-limit classifier and a retry-after extractor to
`pkg/providers` (the layer that already owns `ProviderError`
[`pkg/providers/interface.go:155-162`] and `IsRetryable`
[`pkg/providers/interface.go:168-170`, `pkg/providers/openai.go:217-224`]).

```go
// pkg/providers/interface.go (additions)

// ProviderError (extended — RetryAfter + ErrorType + ErrorCode fields):
//
// Existing struct (pkg/providers/interface.go:155-162) is extended
// ADDITIVELY. No existing field is renamed, removed, or reordered.
// Round 3c — W2: ErrorType + ErrorCode are NEW ADDITIVE fields, captured
// in `handleError` from the already-unmarshaled anonymous-local
// `{Error:{Message,Type,Code}}` struct (openai.go:238-246 today — the
// caller unmarshals the body, then discards the anonymous-local shape
// when constructing ProviderError). Populating ErrorType + ErrorCode
// makes the Round-3b classifier matrix row 11 (503 + rate-limit body)
// implementable: row 11 requires reading the body's `code` field even
// when StatusCode ≠ 429, which the current shape cannot do.
type ProviderError struct {
    Provider   string
    StatusCode int
    Message    string
    Retryable  bool
    BufferID   string
    // NEW (R3-1): captured from the Retry-After response header when
    // StatusCode == 429 (or any retryable response that includes it).
    // Zero when the header is absent or unparseable. Used by R3-2's
    // ExcludeAndReselect to seed the per-credential cooldown.
    RetryAfter time.Duration
    // NEW (Round 3c — W2): captured from the unmarshaled error body's
    // `type` and `code` fields. Empty string when the body is absent
    // or unparseable. Used by IsRateLimitError to support the matrix
    // row 11 case (503 + rate-limit body) AND by the
    // /v1/messages anthropic-passthrough classifier
    // (handler_anthropic.go:297-345 → doAnthropicRequest — see W1),
    // which classifies on `arc.lastStatusCode == 429` OR response-
    // body `type` substring `rate_limit` and reads the captured
    // values from these fields. Default-zero (empty string) preserves
    // existing error-comparison code paths.
    ErrorType string
    ErrorCode string
}

// IsRateLimitError classifies an error as a 429-style rate-limit
// condition. Returns true for: HTTP 429; OR an unmarshaled
// OpenAI-compatible error body whose code/type matches the rate-limit
// vocabulary. MiniMax emits the standard shape `{"error":{message,type,
// code}}` and flows through the existing unmarshal at
// pkg/providers/openai.go:238-245, so no new provider integration is
// required — the classifier just reads the already-decoded fields.
//
// Round 3c — W2 + S1: the classifier reads `ProviderError.ErrorType`
// (substring `rate_limit`) and `ProviderError.ErrorCode` (equality
// match against `rate_limit`, `rate_limit_error`, or
// `rate_limit_exceeded` — S1 added the third literal to the
// code-equality set). HTTP-status fallback is the StatusCode field
// (already 429 for the OpenAI flow). See technical-analysis.md §
// `pkg/providers — error-classification additions` for the full
// vocabulary table + unit-test matrix.
func IsRateLimitError(err error) bool
```

**Parser wiring.** `parseRetryAfter` already exists at
`pkg/providers/openai.go:592-609` (handles seconds + RFC1123 dates) but
is **DEAD CODE today** — `grep -rn parseRetryAfter pkg/ --include="*.go"`
returns only the declaration site. Wiring it inside `handleError`
(`pkg/providers/openai.go:234-279`) is conflict-free: capture
`RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))` next to
the existing `Retryable: retryable` field write at line 265.

**Round 3c — W2 wiring (ErrorType + ErrorCode).** Inside the same
`handleError` (`pkg/providers/openai.go:234-279`), the unmarshal
target today is an anonymous local struct
`{Error:{Message,Type,Code}}` (lines 238-246); the unmarshal
success path constructs `ProviderError` and the anonymous-local
fields are DISCARDED. Round 3c — W2 pins: the `ProviderError`
construction populates `ErrorType` and `ErrorCode` from the
already-decoded anonymous-local fields BEFORE the local goes
out of scope (or, equivalently, the unmarshal target is
upgraded to a named local and copied). The wiring is purely
additive — no existing call site changes; the classifier
(above) is the only consumer.

**Rationale.** (a) `pkg/providers` is the only layer that already knows
the OpenAI-compatible error JSON shape (the MiniMax `{"error":{...}}`
body unmarshals here, not in `pkg/proxy`); classifying upstream is a
provider concern, not a coordinator concern. (b) Extending
`ProviderError` (rather than introducing a new error type) keeps the
existing `errors.As(err, &providerErr)` pattern at
`pkg/providers/openai.go:219-221` and `pkg/proxy` call sites unchanged.
(c) Wiring the dead-code `parseRetryAfter` removes a known staleness
flag without disturbing any caller. (d) The retry-after duration is the
ONLY upstream-supplied timing signal we have — using it as the cooldown
seed (R3-2) avoids arbitrary constants when the upstream tells us when
to come back.

**Classifier vocabulary (cross-ref to technical-analysis.md).** The
classifier consumes the `ErrorType` (substring `rate_limit`) and
`ErrorCode` (equality against `rate_limit`, `rate_limit_error`, or
`rate_limit_exceeded`) fields, plus the `StatusCode` (HTTP 429).
**Round 3c — S1 vocabulary extension:** the code-equality set is
extended to include `rate_limit_exceeded` (alongside `rate_limit` and
`rate_limit_error`); this covers the `OpenAI`-style "rate_limit_exceeded"
code that some upstream proxies (e.g., third-party OpenAI-compatible
endpoints) emit instead of `rate_limit`. The full vocabulary table +
unit-test matrix live in technical-analysis.md §
`pkg/providers — error-classification additions` (Round 3b — review
amendment 6 + Round 3c — S1 row 12). **Round 3c — S1 unit-matrix
pin:** a matching row (row 12 — `code == "rate_limit_exceeded"` ⇒
`IsRateLimitError == true`) exists in the unit-test matrix; the
sibling worker owns the phase 5 test row.

**Alternatives considered (brief).** (a) Classifier in `pkg/proxy` near
the race coordinator — rejected: duplicates the unmarshal logic and
creates two error-shape definitions. (b) New `RateLimitError` struct —
rejected: requires every existing `errors.As` site to be widened.
(c) Ignore the header and use a fixed 60s cooldown — rejected: upstream
often signals 1-30s for token-bucket refill; fixed 60s is either too
long (lost throughput) or too short (likely to re-429).

### F.2 Affinity Break + ExcludeAndReselect (R3-2)

> **AMENDED 2026-08-25 (Round 3b — B2 + B3 — signature supersession;
> Round 3c — C2 unified semantics):** The Round-3 signature
> `(credentialID string, ok bool)` is SUPERSEDED by the mode-aware
> signature below. The semantics of the Round-3 rebind, the cooling-set
> skip, the sliding-TTL `#10` refresh, and the caller-side tried-set
> are UNCHANGED. **Round 3c — C2 unified `ReselectMode` semantics:**
> `ReselectNone` is now returned ONLY for single-credential models (no
> alternative exists) or genuinely-no-candidate (0 credentials valid).
> The B2 no-op path (current binding already moved off the excluded
> credential) and the empty-`conversationKey` path BOTH return
> `ReselectHealthy` — the prior reading (both → `ReselectNone`) was
> REJECTED because it would force the caller to model-switch while a
> healthy credential exists, violating R5 (Round-1 reviewer-5). The
> audit trail of the original Round 3 signature is preserved below
> marked "superseded Round 3 — B3".**

**Ruling (Round 3b — B3 supersession; Round 3c — C2 unified).** A
429 BREAKS conversation stickiness for the affected credential.
Engine gains:

```go
// pkg/credentiallb/engine.go (Round 3b — B3 SUPERSEDES the Round 3
// signature below; Round 3c — C2 unifies the ReselectMode semantics).
// The ok bool cannot express the F.4 single-attempt-then-fall-through
// contract because the engine does not know the request's tried-set;
// the mode carries the contract.

// ReselectMode describes the outcome of ExcludeAndReselect. The
// engine picks the mode; the caller enforces the F.4 fall-through
// contract based on it. Leader ruling accepted the B3-recommended
// enum shape (one call, mode in return) over the two-call
// `ok bool` + `PickSoonestExpiring` alternative (the two-call shape
// risks an extra all-cooling pick looping the fallback chain to the
// terminal error incorrectly — the engine has no tried-set awareness).
//
// **Round 3c — C2 unified semantics (the four surfaces — this enum,
// the return-bullets below, the technical-analysis.md precedence-tree
// pseudocode, and the technical-analysis.md failure-modes table — ALL
// agree on the mapping):**
//
//   - mode == ReselectHealthy: a credential is available for THIS
//     call. The engine returns a credentialID the caller should
//     spawn against (or, in the empty-key / B2 no-op sub-cases, the
//     current/freshly-picked credential). Caller MUST proceed with
//     credential failover — NOT fall through to model-fallback.
//   - mode == ReselectSoonestExpiry: ALL credentials are cooling
//     (0-of-N healthy); the engine picked the soonest-expiring
//     credential and emits the WARN. Caller MUST treat this as a
//     SINGLE attempt only — if that attempt also 429s, the cooldown
//     extends and the caller MUST fall through to model-fallback /
//     terminal error (no second call to ExcludeAndReselect, no loop).
//   - mode == ReselectNone: NO credential is available — either the
//     model has a single credential (no alternative exists) or
//     genuinely 0 credentials are valid. Caller falls through to
//     model-fallback.
type ReselectMode int

const (
    // ReselectHealthy: a credential is available and the caller MUST
    // proceed with credential failover. Sub-cases (Round 3c — C2):
    //   (a) Weighted-random among non-cooling (renormalized) picked a
    //       fresh healthy credential (the typical happy path).
    //   (b) B2 no-op (current binding already points at a credential
    //       OTHER than `excludedCredID`, because a concurrent same-
    //       conversation request already rebinded away from it) →
    //       return `(currentBinding.credentialID, ReselectHealthy)`.
    //       The unchanged binding is the rebind the concurrent
    //       request already committed; the caller continues with it.
    //       NOT an error, NOT ReselectNone.
    //   (c) Empty conversationKey (Round 1 W-2 semantics — no binding
    //       stored) → fresh NON-COOLING weighted-random (renormalized)
    //       pick, NO map write, `ReselectHealthy`. Mirrors the
    //       Round-1 C2 empty-key no-binding fast-path.
    // Caller spawns the new attempt against this credential; the
    // standard healthy-skip path applies on subsequent calls.
    ReselectHealthy ReselectMode = iota

    // ReselectSoonestExpiry: ALL credentials are cooling (0-of-N
    // healthy — Round 3b 0-of-N pin); the engine picked the
    // soonest-expiring credential and emits the WARN. Caller MUST
    // treat this as a SINGLE attempt only — if that attempt also
    // 429s, the cooldown extends and the caller MUST fall through to
    // model-fallback / terminal error (no second call to
    // ExcludeAndReselect, no loop). This is the F.4 contract.
    ReselectSoonestExpiry

    // ReselectNone: NO credential is available. Either (a) the model
    // has a single credential (no alternative exists); or (b) zero
    // credentials are valid (e.g., all credentials are excluded by
    // the caller via some out-of-band mechanism, or the model was
    // reconfigured to zero credentials between lookup and reselect).
    // Caller falls through to model-fallback.
    //
    // Round 3c — C2 narrowing: prior readings that returned
    // ReselectNone for (i) B2 no-op or (ii) empty conversationKey
    // are REJECTED — both now return ReselectHealthy. ReselectNone
    // is reserved for "no credential exists / no credential valid".
    ReselectNone
)

// ExcludeAndReselect marks `excludedCredID` as cooling for `modelID`
// (cooldown seeded from `retryAfter`, default 60s if zero), then
// REBINDS the existing (modelID, conversationKey) entry to the next
// healthy credential chosen by weighted random among non-cooling
// (renormalized), skipping cooling credentials. Old binding is
// rebound (single map write), NOT deleted-then-recreated, under the
// engine's existing locking discipline (engine outer Lock →
// per-model write Lock per F.5 / "Concurrency Notes" in
// technical-analysis.md).
//
// Round 3b — B2 PRECONDITION (concurrent double-429 idempotency):
// the rebind happens ONLY if `bindings[convKey].credentialID ==
// excludedCredID`. If the current binding already points at a
// different credential (because a concurrent request on the same
// conversation already rebinded away from `excludedCredID`), this
// call is a NO-OP and returns `(currentCredentialID,
// ReselectHealthy)` — the unchanged binding is returned as
// `credentialID` with mode `ReselectHealthy` (the caller continues
// with the unchanged binding, which is the rebind the concurrent
// request already committed). This prevents the binding-flap that
// two concurrent double-429 requests would otherwise cause
// (request 1 rebinds A→B mid-attempt on B while request 2 re-marks
// A cooling and re-selects C).
//
// Returns the new (or unchanged) credentialID plus a ReselectMode:
//   - mode == ReselectHealthy: a credential is available (sub-cases
//     per the enum comment above — fresh weighted-random pick OR
//     B2 no-op OR empty-key fresh pick). Caller MUST proceed with
//     credential failover — NOT fall through to model-fallback.
//   - mode == ReselectSoonestExpiry: all credentials cooling; the
//     engine picked the soonest-expiring one and emitted the WARN;
//     caller MUST spawn a SINGLE attempt only, and if it 429s,
//     fall through to model-fallback (F.4 contract — the engine
//     does NOT know the request's tried-set, so the single-attempt
//     invariant is caller-enforced via the mode).
//   - mode == ReselectNone: NO credential is available (single-
//     credential model, or 0 valid credentials). Caller falls
//     through to model-fallback.
//
// Per-conversation retry accounting (which credentials were already
// tried this request) lives in the REQUEST context
// (requestContext / anthropicRequestContext / upstreamRequest),
// NOT in the engine — the engine stays per-conversation-key stateless
// about retries. The "tried-this-request" set is owned by the caller.
// Round 3c — S2 pin: the per-request tried-set is mutated ONLY from
// the race coordinator's `manage()` loop, serialized by the
// coordinator mutex (per the Round-3b single-goroutine invariant).
// The engine never reads or writes the tried-set; the engine NEVER
// becomes a stateful retry counter.
func (e *Engine) ExcludeAndReselect(
    modelID, conversationKey, excludedCredID string,
    retryAfter time.Duration,
) (credentialID string, mode ReselectMode)
```

**Rationale.** (a) **Rebind, not delete-then-create** — a single map
write preserves the sliding-TTL invariant from #10: the rebind DOES
refresh `boundAt` for the new credential (same as a fresh
`GetOrSelect` would), but does NOT touch the cooling credential's
`boundAt` (the cooldown map is separate from the binding map; see
F.4). (b) **Per-request retry accounting in the caller, not the
engine** — the engine already has enough invariants (weighted order,
cooldown skipping, sliding TTL, filter-survivors invalidation) without
adding a third responsibility; the caller (race coordinator / ultimate
handler / internal handler) already owns the request lifecycle, so
"which credentials were tried this request" belongs there. (c)
**Mode-aware return (Round 3b — B3) — single-call, enum-shape** —
the F.4 single-attempt-then-fall-through contract requires the engine
to distinguish "no healthy pick, go to fallback" from "here is a
soonest-expiry pick, one attempt only"; the two-call alternative
(`ok bool` + `PickSoonestExpiring`) was REJECTED because a second
`ok=false` call after a soonest-expiry pick would return yet another
all-cooling pick and loop the fallback chain to the terminal error
incorrectly — the engine does not know the request's tried-set, so
the mode must carry the contract in a single return value. (d)
**Idempotent precondition (Round 3b — B2)** — concurrent
same-conversation double-429 would flap the binding (request 1 rebinds
A→B mid-attempt on B while request 2 re-marks A cooling and re-selects
C); the precondition `bindings[convKey].credentialID == excludedCredID`
makes the operation idempotent-by-construction: only the request
whose CURRENT binding is the one that 429d proceeds with the rebind;
the other request is a no-op returning the current unchanged binding.

### F.2 superseded Round 3 — B3 (audit trail, preserved verbatim)

```go
// pkg/credentiallb/engine.go (additions — Round 3)
// (superseded Round 3 — B3; the (credentialID string, ok bool) shape
// cannot express F.4's single-attempt-then-fall-through contract.)

// ExcludeAndReselect marks `excludedCredID` as cooling for `modelID`
// (cooldown seeded from `retryAfter`, default 60s if zero), then
// REBINDS the existing (modelID, conversationKey) entry to the next
// healthy credential chosen in weighted order, skipping cooling
// credentials. Old binding is rebound (single map write), NOT
// deleted-then-recreated, under the engine's existing locking
// discipline (engine outer Lock → per-model write Lock per F.5 /
// "Concurrency Notes" in technical-analysis.md).
//
// Returns the new credentialID + ok=true on success. Returns ok=false
// when every other credential for the model is also cooling — caller
// falls through to model-fallback (R3-3).
//
// Per-conversation retry accounting (which credentials were already
// tried this request) lives in the REQUEST context
// (requestContext / anthropicRequestContext / upstreamRequest),
// NOT in the engine — the engine stays per-conversation-key stateless
// about retries. The "tried-this-request" set is owned by the caller.
func (e *Engine) ExcludeAndReselect(
    modelID, conversationKey, excludedCredID string,
    retryAfter time.Duration,
) (credentialID string, ok bool)
```

**Original rationale (Round 3 — superseded)**: (a) Rebind, not
delete-then-create — preserved in the Round 3b ruling above. (b)
Per-request retry accounting in the caller, not the engine —
preserved. (c) Returns `ok=false` rather than synthesizing a pick
when every other credential is cooling — **REPLACED by the Round 3b
mode-aware return**; the original `ok=false` reading conflated "no
healthy pick, go to fallback" with "here is a soonest-expiry pick, one
attempt only" — the F.4 single-attempt-then-fall-through contract
requires the caller to distinguish them.

### F.3 Precedence Ordering (R3-3)

> **AMENDED 2026-08-25 (Round 3b — B1 + leader ruling iii + symbol
> fix + canonical naming + Case-1 latest-only pin + Case-2 uniform
> classification + modelTypeSecond re-key):** R3-3's "credential
> failover BEFORE model fallback" SEMANTICS STAND. The decision
> tree, citations, and naming are amended in place per the Round 3
> architect stress-test. The audit trail of the original Round-3
> text is preserved below marked "superseded Round 3" for
> decisions-log history.

**Ruling (Round 3b — supersedes Round 3 wording where corrected).**
Credential failover (same model, next credential) is attempted
BEFORE any model-switching machinery. Interception point in the
race path: `pkg/proxy/race_coordinator.go` `manage()` "Case 1"
(function def at `race_coordinator.go:252`; Case 1 verified at
current lines 350-364 — `latestReq.IsDone() && latestReq.GetError()
!= nil` → currently sets `shouldSpawn=true; triggerInfo=
spawnTriggerInfo{trigger: triggerMainError, ...}` → falls through
to `spawn(modelTypeFallback, ...)` at line 409, which picks
`c.models[1]`).

**Decision tree at that site (Round 3b — exact replacement of Case 1's body; Round 3c — C1 hoisted form, supersedes the Round-3 wording with real guards, the C1 hoisted admission condition, the B1 separate accounting, and canonical naming):**

```
1. HOISTED CRED-FAILOVER PRE-CHECKS (Round 3c — C1 — these run ABOVE
   the spawn-window gate at :338 and ABOVE the all-failed check at
   :420-421; the Round 3b B1 gate exemption is RE-EXPRESSED as this
   hoisted form, which is the only way the user's core scenario —
   3 credentials / 1 model / no fallback chain — gets any failover
   at all):

   If error classifies rate-limit per F.1/R3-1
   AND failed model is INTERNAL
   AND has >1 configured credentials
   AND no CONTENT flushed to client yet (race: c.winner == nil;
   ultimate: initial-call *ProviderError assertion;
   /v1/messages: arc.headersSent per handler_anthropic.go:648-654)
   AND retry budget not exhausted per F.5/R3-5:
       → spawn SAME-MODEL-NEXT-CREDENTIAL attempt. New canonical
         Go constant upstreamModelType = "credential_failover" →
         modelTypeCredFailover (ONE spelling everywhere; supersedes
         the Round-3 variants `modelTypeCredentialFailover` and
         `credential_failover`); new spawnTrigger spawnTrigger =
         "rate_limit" → triggerRateLimit (canonical constant).
         The spawn reads the cooldown-aware next credential from
         the engine via ExcludeAndReselect (the model's "current
         credential" is the rebind target — same modelID, different
         credential). The credentialID rides the trigger info from
         Case-1 classification to spawn()/executor (Round 3c —
         C3(1): `spawnTriggerInfo` additively gains `credentialID
         string`; verified absent at race_coordinator.go:79 today
         and is the only way the executor can pre-resolve without
         re-calling ExcludeAndReselect; absent field yields silent
         credential lookup failure in spawn()).

       Round 3c — C3(2): the `spawn()` switch (race_coordinator.go
       ~196-218) additively gains a `case modelTypeCredFailover:
       modelID = c.models[0]` branch — same model, credential re-
       selected. Verified today: switch maps only
       {modelTypeMain, modelTypeSecond → c.models[0];
       modelTypeFallback → c.models[1]}; an unmapped modelType
       yields modelID = "" silent misfire. The credFailover case
       reads `credID` from `spawnTriggerInfo.credentialID` (C3(1))
       and passes it downstream.

2. OTHERWISE (non-rate-limit, single-credential, post-first-byte,
   budget exhausted, etc.): the existing model-fallback spawn
   path is admitted by the C1 HOISTED GATE (replaces the Round-3b
   "B1 gate exemption" paragraph; semantically equivalent — see
   note below):

       gate := modelAttempts < len(models) ||
               credFailoverEligibleWithBudget()

   where:
       - modelAttempts = SEPARATE accounting counting only
         modelType ∈ {main, second, fallback} (Round 3b separate
         accounting; the credential-failover attempts are NOT
         counted here — they track independently against the
         per-model credential budget).
       - credFailoverEligibleWithBudget() = rate-limit classified
         AND internal AND >1 creds AND c.winner == nil AND
         retry budget remaining (the hoisted pre-checks above).
       - When `gate` is true: existing model-fallback spawn
         unchanged (modelTypeFallback / triggerMainError).
       - When `gate` is false: terminal "All models rate limited"
         (the :860-868 guard) — UNCHANGED.

   Round 3c — C1 SEMANTIC-EQUIVALENCE NOTE: the hoisted form
   `gate := modelAttempts < len(models) ||
   credFailoverEligibleWithBudget()` is SEMANTICALLY EQUIVALENT
   to the rejected cap-extension alternative
   (`cap += len(credentials) − 1`) — both admit
   `len(credentials) − 1` additional attempts before the
   terminal. Cap-extension was REJECTED purely as an
   IMPLEMENTATION (mutation of len-dependent invariants
   elsewhere — the existing :338 / :420-421 reads of
   `len(c.models)` would need synchronized widening across
   multiple sites); the hoisted form is the same semantic
   admission expressed via a separate accounting + OR-clause,
   which keeps the existing :338 / :420-421 invariants intact.

   TERMINAL GUARD (Round 3c — C1): `:860-868` "All models rate
   limited" MUST NOT fire while an untried fallback model OR an
   untried credential remains. The all-failed check at `:420-421`
   is amended to: "all fallback models tried **OR** credential
   budget exhausted with no healthy/soonest-expiry candidate
   remaining". Phase 5 Task 32 scenario-(a) stays as the
   regression guard (sibling worker owns the test; this contract
   owns the statement).
```

**Pre-first-byte guard — real implementations (Round 3b supersedes
Round-3 wording; the Round-3 invented state `chunksFlushedToClient`
and `bytesFlushedToClient` and the vacuous `status !=
statusStreaming` check are DELETED — they do not exist at HEAD):**

- **Race path**: the expressible guard is `c.winner == nil`
  (non-stream case is implicit — nothing is written pre-winner).
  For streaming, the TRUE invariant is **no CONTENT flushes
  pre-winner** — SSE framing headers + `": connected\n\n"`
  heartbeat comment DO precede `coordinator.Start()` at
  `handler.go:783-797` (verify: SSE headers written and
  `rc.headersSent = true` set at `:797` BEFORE `coordinator.Start()`
  is called) but they carry NO model content (the `: connected\n\n`
  line is a no-op SSE comment; the heartbeat is empty bytes).
  Winner requires `req.IsCompleted() && req.GetError() == nil`
  (`race_coordinator.go:299`); `streamResult` (call site `:852`,
  definition `:960`) and `handleNonStreamResult` (call site `:879`,
  definition `:1327`) run only after `WaitForWinner` returns.
  Round-3's `latestReq.status != statusStreaming` is **vacuous**
  (failed attempts are `statusFailed`, not `statusStreaming`); the
  invented `chunksFlushedToClient` / `bytesFlushedToClient`
  fields/methods **DO NOT EXIST** at HEAD and are PURGED from this
  contract.

**All-failed terminal condition (Round 3b — B1; Round 3c — C1
hoisted form):** the terminal `"All models rate limited"` at
`race_coordinator.go:860-868` MUST NOT fire while an untried
fallback model OR an untried credential remains. The all-failed
check at `race_coordinator.go:420-421` is amended to: "all
fallback models tried **OR** credential budget exhausted with
no healthy/soonest-expiry candidate remaining". After credential
exhaustion, the flow FALLS THROUGH to the existing model-fallback
spawn (`c.models[1]`) when it exists. The C1 hoisted
admission gate (above) expresses the same admission explicitly:
the gate admits model-fallback when EITHER model budget
remaining OR credential failover is eligible — the user's core
scenario: 3-credential single-model (no fallback chain) gets the
FULL failover chain (`len(modelCfg.Credentials)−1` attempts
before the terminal message); the 2-model case gets up to
`len(creds)−1` failover attempts and then the existing
`c.models[1]` model-fallback spawn.

**Case-2 idle-trigger classification (Round 3b — leader ruling iii,
SUPERSEDES the Round-3 reading):** the Case-2 idle-timeout spawn
path (`race_coordinator.go:364-395` → spawns `modelTypeSecond` +
`modelTypeFallback` together at `:399-405`) ALSO classifies
rate-limit before spawning — the uniform precedence rule is:
**a rate-limit error ⇒ credential failover first, on EVERY spawn
path.** The Round-3 "Case-2 idle bypass" reading (model-switching
can precede the stalled credential's eventual 429) is REJECTED —
the leader ruled uniform classification in scope. Implementation:
at the Case-2 spawn site, classify the err that triggered the idle
(if any) via `providers.IsRateLimitError`; on rate-limit, route to
credential failover instead of straight-to-second/fallback spawn.
The original Round-3 "non-goal" reading is preserved below marked
"superseded Round 3" for audit trail.

**Case-1 latest-only inspection (Round 3b — accepted v1 behavior):**
Case 1 reads only `c.requests[len(c.requests)−1]` (latest). An
earlier attempt's 429 (e.g., main idle-stalled while a spawned
attempt fails first) is not adjudicated for credential failover at
this site — adjudicating in spawn order would require iterating
all attempts (not just the latest) and is rare in practice.
**Accepted v1 behavior** with one-line rationale: race spawn
ordering + idle timeouts rarely interleave this way, and the
existing `manage()` loop runs after every attempt completes, so an
earlier attempt's classification surfaces on a subsequent
iteration. Documented for operator awareness.

**`modelTypeSecond` re-key (Round 3b — specified behavior):**
secondary attempts share `c.models[0]`'s modelID but resolve the
PRIMARY credential (verified at `race_executor.go:112`). A 429 on
a `modelTypeSecond` attempt re-keys failover to the primary
credential of `c.models[0]` (the secondary resolves to the primary
credential at the executor layer; the engine rebind writes the
primary back). This is **specified behavior, not an accident** —
it preserves provider-pool consistency within the same model.

**Canonical Go identifier (Round 3b — pin ONE spelling):** the
failover model-type constant is **`modelTypeCredFailover`** —
everywhere. The Round-3 variants (`modelTypeCredentialFailover`,
`credential_failover`) are PURGED from this contract prose. The
EVENT name stays `model_credential_failover` (R3-8 constant
`EventCredentialFailover`); the SPAWN-TRIGGER constant stays
`triggerRateLimit`. Operator-facing surfaces (logs, events,
exposes) keep the verbose `model_credential_failover` string.

**Operator note (Round 3b — operator-doc only, no code):** 1-of-N
healthy concentrates load on the last healthy credential — and
re-distribution after cooldown expiry is gradual (weighted-random
selection on every subsequent request, no "switch-back" thundering
herd). Documented for operator awareness; no code change.

**Ultimate-internal and `/v1/messages` internal hooks (equivalent
sites; the sibling worker maps the exact call sites):**

- `pkg/ultimatemodel/handler_internal.go:36-46` — `executeInternal`
  call site for `ResolveInternalConfig(modelCfg.ID)`. The Round-3
  amendment replaces this with `ResolveInternalConfigWithAffinity` (the
  Round-1 contract) AND adds a top-level error-classification gate on
  the returned `ProviderError`: if it classifies as rate-limit AND the
  caller is in pre-first-byte state, call
  `engine.ExcludeAndReselect(modelID, conversationKey, currentCredID,
  providerErr.RetryAfter)` and re-attempt with the new credential.
- `pkg/proxy/internal_handler.go:109-111` — `HandleRequest` call site
  for `ResolveInternalConfig(h.config.ID)`. Same shape as the
  ultimate-internal hook: classify the error, check pre-first-byte
  (initial-call `*ProviderError` type assertion), call
  `ExcludeAndReselect`, re-attempt.
- **`/v1/messages` internal — ANTHROPIC-PASSTHROUGH BRANCH (Round
  3b — leader ruling i, NEW IN SCOPE):** for internal models whose
  credential provider is `"anthropic"`, the
  `handler_anthropic.go:297-345` branch BYPASSES
  `doAnthropicInternalRequest` entirely — it reads the single-cred
  `modelConfig.CredentialID` + `cred.ResolveAPIKey()` and calls
  `h.doAnthropicRequest(w, arc)` (a different downstream function
  path). This branch IS IN SCOPE for R3-3 / R3-6 / R3-7 / F.7 and
  MUST participate in credential failover. **Implementation note
  for the sibling phase worker (Task 16 / Task 22 wiring):** this
  branch currently reads the single-cred `modelConfig.CredentialID`
  (the Round-1 single-credential shape) and MUST be swapped to the
  affinity resolver; the contract statement is owned by this
  document, the task wiring is owned by the sibling phase file.
  **Real pre-first-byte guard for this branch:** recorder's
  `arc.Code` + `arc.headersSent` as already specified; **header
  write cite FIXED (Round 3b)** — the header write is at
  `handler_anthropic.go:648-654` (`arc.headersSent`), NOT at
  `adapter_anthropic.go:170-176` (that is `SetStreamHeaders`).

**Race coordinator constructor wiring (Round 3c — C3(3)):**

The `newRaceCoordinatorWithEvents` constructor at
`race_coordinator.go:129` currently takes 8 params: `(ctx, cfg,
req, rawBody, models, eventBus, requestID, interleaved)` —
verified at 8f67bdf. The Round 3c contract requires the constructor
to ADDITIVELY gain two new params:

- `engine *credentiallb.Engine` — the Round-1 LB engine the
  coordinator consults for `ExcludeAndReselect` (Case-1
  classification routing into the precedence tree).
- `conversationKey string` — the Round-1 conversation key,
  threaded from `cmd/main.go` through the existing
  `handler.go:830` call site (no new wiring path).

Constructor-only injection: per Round 3c — W-3 the engine is
NOT exposed as a `Config.CredEngine` field. The config stays
flat; the engine and conversation key arrive via the
constructor arguments exactly as `eventBus` and `requestID`
already do today. This preserves the existing config-shape
contract and keeps the engine reference local to the
coordinator (lifetime = request scope).

Wiring chain (Round 3c — C3(3)):

```
cmd/main.go
    credLB := credentiallb.NewEngine(...)         // Round 1
    credLB.Start(...)
    ...
    proxy.NewHandler(cfg, ..., eventBus, credLB)   // Round 1 wiring
        │
        ▼
    pkg/proxy/handler.go:830
        h.coordinator = newRaceCoordinatorWithEvents(
            ctx, cfg, req, rawBody, models,
            eventBus, requestID, interleaved,
            credLB,                                // NEW (Round 3c — C3(3))
            h.conversationKey,                     // NEW (Round 3c — C3(3))
        )
```

The constructor param order follows the existing convention
(coordination-state args after the primitive args; engine + key
after the request-scoped lifecycle args). The sibling worker
adds the param-list change at the call site; this contract owns
the param SEMANTICS.

The 10-param constructor is now:
`newRaceCoordinatorWithEvents(ctx, cfg, req, rawBody, models,
eventBus, requestID, interleaved, engine, conversationKey)`.

Verified at 8f67bdf: the existing 8-param signature is the
ONLY call site at `handler.go:830` — no other callers.

**Rationale.** (a) Same-model credential failover preserves the
prompt-cache affinity on the OTHER credential of the same provider —
this is the user's exact requirement (loadbalance + HA within a
provider). (b) Pre-first-byte constraint is inherent to coordinator
flows: race path requires `winner` to be selected from completed
buffers (`Done` status), so no CONTENT flushes before the winner is
declared (SSE framing headers + `": connected\n\n"` precede
coordinator start per `handler.go:783-797` but carry NO model
content). (c) Adding a new `modelTypeCredFailover` (canonical —
Round 3b; supersedes `modelTypeCredentialFailover`) rather than
re-using `modelTypeFallback` preserves the existing model-fallback
semantics (which writes a different `modelList[i]`) and lets
observers distinguish "credential failover" from "model failover"
via the spawnTrigger. (d) The B1 gate exemption is the only way
the user's core scenario (3-credential single-model with no
fallback chain) gets any failover at all; without it the feature
silently does nothing in exactly the case it was built for. (e)
Uniform precedence rule (leader ruling iii) — Case-2 idle-trigger
must classify rate-limit first, on EVERY spawn path.

**Alternatives considered (brief).** (a) Always spawn credential
failover first, then model fallback — rejected: would double the
spawn cost when the next credential also fails; the budget-bounded
attempt (R3-5) already covers this. (b) Re-attach the existing
`latestReq` instead of spawning a new attempt — rejected: the
existing attempt may have been cancelled and its buffer is
poisoned; a fresh attempt is the only safe re-entry. (c) (Round
3b) Single shared spawn-window for model and credential attempts
— rejected (B1): silently strands credential failover at one
attempt in the exact case the feature was built for (multi-cred
model, no fallback chain); the separate accounting is the only way
to deliver the user's HA requirement.

### F.3 superseded Round 3 — Case-2 idle bypass reading (audit trail)

> **superseded Round 3 (preserved for decision-log history)**:
> the Round-3 contract read Case 2 (`race_coordinator.go:364-395`
> idle timeout) as a "Case-2 idle bypass" — model-switching can
> precede the stalled credential's eventual 429, so Case 2 was
> framed as an "explicit non-goal" for credential failover.
> **REVERSED 2026-08-25 (Round 3b — leader ruling iii):** the
> uniform precedence rule applies — a rate-limit error ⇒ credential
> failover first, on EVERY spawn path. The original Round-3
> non-goal reading is REJECTED; the implementation MUST classify
> the err that triggered the idle (if any) via
> `providers.IsRateLimitError` and route to credential failover on
> rate-limit before the existing second/fallback spawn.

### F.3 superseded Round 3 — original decision tree (audit trail)

> **superseded Round 3 (preserved for decision-log history)**:
> the Round-3 decision tree (original §F.3 wording) said
> `latestReq.status != statusStreaming && !winner.chunksFlushedToClient`
> for the race-path guard. **REVERSED 2026-08-25 (Round 3b):**
> `chunksFlushedToClient` / `bytesFlushedToClient` DO NOT EXIST at
> HEAD (verified `grep -rn` returns zero hits in
> `pkg/proxy/race_coordinator.go`); the `status != statusStreaming`
> check is vacuous (failed attempts are `statusFailed`); the
> expressible race-path guard is `c.winner == nil` (and the TRUE
> invariant is no CONTENT flushes pre-winner, with SSE framing
> bytes exempted as no-content). The original Round-3 ultimate
> guard cited `headersSent`, which is UNUSABLE because
> `ultimatemodel/handler.go:608-609` pre-sets it before
> `executeInternal`; the corrected guard is the initial-call
> `*ProviderError` type assertion (initial-call errors return
> before any content write).

### F.4 Per-Credential Cooldown State (R3-4)

**Ruling.** Engine maintains an in-memory cooldown map alongside the
binding map, guarded by the engine's existing locking discipline:

```go
// pkg/credentiallb/engine.go (engineState additions — Round 3)

type engineState struct {
    mu         sync.RWMutex
    credentials []models.CredentialRef
    prefixSum  []int
    totalWeight int
    bindings   map[string]*binding  // existing — keyed by conversationKey
    rng        *rand.Rand

    // NEW (R3-4): cooldown map. Keyed by modelID → credentialID →
    // cooldownUntil time.Time. Guarded by the same outer+per-model
    // locking discipline (E-1): outer RLock for reads + janitor sweep,
    // per-model write Lock for cooldown updates. The cooldown map is
    // SEPARATE from the binding map — a cooldown does NOT touch the
    // binding's boundAt (which is the sliding-TTL anchor per #10);
    // a rebind via ExcludeAndReselect DOES refresh boundAt (per #10
    // semantics), independently of the cooldown map.
    cooldowns  map[string]map[string]time.Time
    // ... stats counters (existing) ...
}
```

**Janitor interaction.** The existing janitor (5-minute sweep, outer
RLock + per-model write Lock one-at-a-time, E-1) sweeps expired
cooldowns alongside idle-TTL bindings in the same pass. A cooldown is
expired iff `cooldownUntil <= now`.

> **AMENDED 2026-08-25 (Round 3b — gauge semantics, amendment 7):
> `EngineStats.Cooldowns` is a **GAUGE**, NOT a counter — it holds
> the CURRENT size of the cooldown map, recomputed at each janitor
> sweep (or read live on `Stats()`). The Round 3 increment-per-removal
> prose above ("updates a `EngineStats.Cooldowns` counter on each
> cooldown it removes") is REPLACED. `Failovers` remains a monotonic
> counter (one increment per successful `ExcludeAndReselect`). The
> additive-fields-only rule still holds — the new fields are appended
> at the struct tail; pinned #5 field names `Hits`, `Misses`,
> `Bindings` are unchanged.**

> **AMENDED 2026-08-25 (Round 3c — S6 — `OnCredentialDeleted`
> cooldown hygiene):** `OnCredentialDeleted(credentialID)` (defined at
> `pkg/credentiallb/engine.go`) MUST also clear all cooldown entries
> for that credential in addition to dropping bindings. Rationale:
> a deleted credential cannot be selected anyway (it's gone from the
> model's `credentials` list and `weightedSelector` will skip it on
> next call), so the cooldown entries are dead weight. Keeping them
> is hygiene risk against unbounded map growth (in the
> pathological "many credentials deleted under load" case) and
> stale `EngineStats.Cooldowns` gauge counts (Round 3b — amendment
> 7 gauge semantics: the gauge includes the dead entries until the
> next janitor sweep or live read). The clear is a single-pass
> `delete(cooldowns, [modelID][credentialID])` loop across all
> models that have the credential in their cooldowns map,
> performed under the same outer Lock + per-model write Lock
> discipline (E-1) already used by the binding drop. The `EngineStats
> .Cooldowns` gauge is recomputed on the next sweep (or live read
> via `Stats()`) — the gauge is not mutated directly. Phase 2 task
> + test added by the sibling worker; this contract owns the
> statement.

**Selection skip rule.** `weightedSelector` and `ExcludeAndReselect`
both skip credentials with `cooldowns[modelID][credID] > now`. The
selection after cooldown skip is **weighted random among non-cooling
(renormalized)** (Round 3c — S3 standardization: replace any drifted
wording — "weighted order skipping cooling", "weighted selection",
"weighted order", "skip-then-pick" — with the single canonical form
"weighted random among non-cooling (renormalized)" wherever selection
after cooldown is described in active spec text; audit-trail blocks
untouched). Weights are renormalized across the healthy set (sum of
healthy weights becomes the new total).

**All-cooling fallback (R3-4 critical ruling, Round 3b — mode-aware).**
When ALL of a model's credentials are cooling (i.e., **0-of-N
healthy** — see clarification below), pick the **SOONEST-EXPIRING**
credential (availability beats strict cooldown), log a WARN, make
**a single attempt only (no loop)** — if that attempt also 429s,
the cooldown extends and the flow falls through to model fallback /
terminal error.

> **AMENDED 2026-08-25 (Round 3b — B3 mode-aware + 0-of-N pin;
> Round 3c — C2 narrowing of ReselectNone):** the Round-3 reading
> "all-cooling = ok=false ⇒ caller falls through to model-fallback"
> is REPLACED. With the B3 mode-aware `ExcludeAndReselect` signature
> (F.2), the caller now uses the returned mode to enforce the F.4
> single-attempt-then-fall-through contract:
>
> - `mode == ReselectHealthy` → caller spawns the new attempt
>   against `credentialID`. **Round 3c — C2**: this includes the
>   B2 no-op sub-case (current binding already points at a credential
>   OTHER than the excluded one) and the empty-`conversationKey`
>   sub-case (fresh weighted random among non-cooling (renormalized)
>   pick, NO map write). Both proceed with credential failover —
>   NOT fall through to model-fallback.
> - `mode == ReselectSoonestExpiry` → engine has already picked the
>   soonest-expiring credential AND emitted the WARN; caller MUST
>   spawn a SINGLE attempt only, and if it 429s, MUST fall through
>   to model fallback / terminal error (no second
>   `ExcludeAndReselect` call — the engine would return
>   `ReselectSoonestExpiry` again, looping the fallback chain to
>   the terminal error incorrectly because the engine has no
>   tried-set awareness). **Round 3c — W3 single-shot guard** (see
>   precedence-tree pseudocode): the caller sets a per-request
>   `soonestExpiryAttempted` flag and `continue`s after the
>   soonest-expiry attempt, so the NEXT 429 routes to
>   model-fallback without a second `modelTypeCredFailover` spawn.
> - `mode == ReselectNone` → NO credential is available. Either
>   (a) the model has a single credential (no alternative exists);
>   or (b) zero credentials are valid. **Round 3c — C2 narrowing:**
>   the B2 no-op and empty-`conversationKey` paths are no longer
>   "ReselectNone" — they are "ReselectHealthy" (see the F.2
>   contract). Caller falls through to model-fallback.

> **AMENDED 2026-08-25 (Round 3b — 0-of-N pin):** "all-cooling" in
> the F.4 ruling means **0-of-N healthy** ONLY. A 1-of-N healthy
> credential (the common case in a 3-cred setup with 2 rate-limited)
> takes the normal healthy-skip path — the F.4 soonest-expiry branch
> does NOT apply. (The 1-of-N case concentrates load on the last
> healthy credential; this is documented operator behavior, not a
> code concern.)
The existing terminal shape at `pkg/proxy/race_coordinator.go:860-868`
("All models rate limited" → `models.ErrorTypeRateLimit` /
`models.ErrorCodeRateLimit`, defined at `pkg/models/errors.go:7,17`)
remains the final client-facing error.

**Alternatives considered (the full table this ruling requires):**

| Criterion | **Soonest-expiry (CHOSEN)** | Fail-fast | Block-until-expiry |
|---|---|---|---|
| **Availability (per user's HA requirement)** | Best — picks soonest-expiring so user gets SOME service even when fully cooled | Worst — immediate fail to model fallback even when retry-after is 5s | Worst — request hangs up to max cooldown |
| **Per-request latency** | Bounded — one extra 429-RTT in the worst case | Best — zero added latency | Worst — unbounded wait (up to N seconds × #retries) |
| **User-perceived behavior** | Acceptable — partial service preserved; client sees eventual success or 502 after one extra attempt | Acceptable — clean failover to next model, no surprise retries | Poor — request blocks with no client-visible progress |
| **Repeat-storm risk (re-429 amplification)** | Low — single attempt per credential, no loop; the new 429 EXTENDS the cooldown and falls through to model fallback | None — fails immediately, no retry attempt | High — would block multiple concurrent requests on the same soonest-expiring credential, re-429ing them all |
| **Correctness under concurrent requests** | High — different requests may pick different soonest-expiring credentials; bounded by retry budget (R3-5) | High — no surprise behavior | Low — request 1 picks cred A, request 2 sees A still cooling and blocks; no progress until A expires, then both stampede |
| **Complexity / new primitives** | Low — reuses existing janitor sweep, single-attempt invariant, weighted selection | Lowest — no new code | High — needs blocking primitives, condition variables, or polling timers; never requested by any caller today |
| **Reversibility** | High — easy to switch to fail-fast later by changing the fallback path | High | Low — block-until-expiry would require new API surface (e.g., `WaitForCooldown`) that no other feature needs |
| **Alignment with R3-5 retry budget** | Strong — "each credential at most once" is the single-attempt invariant; budget bounds total failover attempts | Strong — zero budget consumed | Poor — would consume budget on time-waits, not on actual upstream attempts |

**Recommendation: pick Soonest-expiry.** It is the only option that
satisfies the user's stated "loadbalance and HA in the case rate
limiting" requirement without introducing unbounded latency or new
primitives. Fail-fast is the obvious alternative if latency ever
trumps availability, but the user did not ask for that tradeoff.
Block-until-expiry is rejected outright as semantically wrong (HTTP
requests should not block on credential cooldown timers) and
operationally dangerous (stampede).

### F.5 Retry Budget (R3-5)

**Ruling.** Each remaining credential is tried **AT MOST ONCE per
request**. Total credential-failover attempts bounded by
`len(credentials) - 1`. No repeats, no infinite loops. The caller
(the request context, per F.2) tracks which credentials have already
been tried this request and refuses to spawn another attempt for a
credential that's in its own tried-set.

**Latency note.** Each failover is a fresh upstream round-trip. 429
responses are typically fast (no body wait, often empty body or a
short OpenAI-compatible JSON envelope), so worst-case added latency is
`(N-1) × 429-RTT` where N = number of configured credentials. For N=3
(minimax A, B, C with one of them rate-limited): at most 2 extra
attempts, at ~50-200ms each on a healthy network.

**Round 3c — W3 soonest-expiry single-shot guard (pseudocode-level
contract, not F.4-mode change):** when `ExcludeAndReselect` returns
`mode == ReselectSoonestExpiry` (Round 3b — 0-of-N pin), the caller
sets a per-request `soonestExpiryAttempted` boolean flag and
spawns the SINGLE attempt. After spawning, the caller MUST
`continue` the loop (no second `modelTypeCredFailover` spawn,
no second `ExcludeAndReselect` call) so that if the soonest-
expiry attempt 429s again, the NEXT loop iteration routes
straight to model-fallback (sequential progression per F.4
single-attempt-then-fall-through, NOT a soonest-expiry pick loop).
The pseudocode lives in technical-analysis.md §
"Rate-Limit Failover Precedence Tree" → Pseudocode (race-internal);
this paragraph owns the contract statement and the flag's
lifetime (per-request, initialized false, set true on
soonest-expiry spawn, never reset within the same request).

**Rationale.** This is the **invariant that makes R3-3 and R3-4 safe**.
Without a hard cap, an all-cooling fallback loop (R3-4) combined with
soonest-expiry selection could replay the same credential repeatedly
in pathological scenarios. The caller-side tried-set (per F.2) is
cheaper to enforce than a per-engine retry counter, because the
counter would have to be keyed by `(modelID, conversationKey)` and
cleaned up on request completion — duplication of work the request
context already does.

### F.6 Streaming Constraint (R3-6)

> **AMENDED 2026-08-25 (Round 3b — invented-state purge + real
> guards; Round 3c — W1 passthrough classification):** Round 3's
> "nothing flushes pre-winner" is **false for SSE framing bytes**.
> The TRUE invariant is "no CONTENT flushes pre-winner". The
> invented `chunksFlushedToClient` / `bytesFlushedToClient`
> fields/methods and the vacuous `status != statusStreaming` check
> DO NOT EXIST at HEAD and are PURGED from this contract. Round 3's
> `headersSent` flag for ultimate-internal is UNUSABLE because
> `ultimatemodel/handler.go:608-609` pre-sets it before
> `executeInternal` runs. The corrected guards are pinned
> below.** **Round 3c — W1 passthrough classification contract:**
> the `/v1/messages` anthropic-passthrough branch
> (`handler_anthropic.go:297-345` → `doAnthropicRequest`) does NOT
> flow through `pkg/providers.OpenAIProvider.handleError` — it
> produces NO `*ProviderError`. The classification contract for
> this branch is therefore separate from the Round 3 R3-1
> `IsRateLimitError` classifier: classify on
> `arc.lastStatusCode == 429` OR response-body `type` substring
> `rate_limit`. The Retry-After header is captured via a NEW
> additive `arc.retryAfter time.Duration` field on
> `anthropicRequestContext` — captured at the point where
> `arc.lastStatusCode` is set, `handler_anthropic.go:423`. Today
> `doAnthropicRequest` does NOT touch `resp.Header`; the Retry-After
> capture is genuinely additive (no existing read site is
> disturbed).**

**Ruling.** Failover applies ONLY before the first CONTENT byte is
sent to the client (SSE framing bytes — headers + `": connected\n\n"`
comment + heartbeat — DO precede coordinator start per
`handler.go:783-797` but carry no model content; mid-stream credential
failover is fundamentally unsafe regardless of framing).

- **Race path** (Round 3b — supersedes Round 3): the expressible
  guard is **`c.winner == nil`** (non-stream case is implicit —
  nothing is written pre-winner). For streaming, the TRUE invariant
  is **no CONTENT flushes pre-winner** — `handler.go:783-797` writes
  SSE headers + `": connected\n\n"` comment and sets
  `rc.headersSent = true` at `:797` BEFORE `coordinator.Start()` is
  called (verify at HEAD); these framing bytes carry NO model content.
  Winner requires `req.IsCompleted() && req.GetError() == nil`
  (`race_coordinator.go:299`); `streamResult` (call site
  `handler.go:852`, definition `:960`) and `handleNonStreamResult`
  (call site `handler.go:879`, definition `:1327`) run only after
  `WaitForWinner` returns. The Round-3 references to
  `chunksFlushedToClient` / `bytesFlushedToClient` are DELETED (the
  fields/methods DO NOT EXIST at HEAD); the `latestReq.status !=
  statusStreaming` check is DELETED (vacuous — failed attempts are
  `statusFailed`).
- **Ultimate-internal path** (Round 3b — supersedes Round 3): retry
  only when the returned error is the INITIAL-CALL `*ProviderError`
  (type assertion). The Round-3 `headersSent` flag is UNUSABLE
  because `ultimatemodel/handler.go:608-609` sets `*headersSent =
  true` and `:643` starts the SSE heartbeat (writes
  `": heartbeat\n\n"`) BEFORE `executeInternal` runs; initial-call
  errors return before any CONTENT write (`handler_internal.go:155-157`
  non-stream, `:253-255` stream). Mid-stream failures surface as SSE
  error events and are not retryable.
- **`/v1/messages` internal path** (Round 3b — fixed cite): retry
  only when `arc.headersSent == false` AND the recorder's
  `arc.Code == http.StatusOK` (recorder decouples — `arc.Code !=
  http.StatusOK` is the failure signal). **Header-write cite FIXED
  (Round 3b):** the header write is at
  `handler_anthropic.go:648-654` (`arc.headersSent`), NOT at
  `adapter_anthropic.go:170-176` (that is `SetStreamHeaders`).
  Includes the **anthropic-passthrough internal branch** (Round 3b
  — leader ruling i, NEW IN SCOPE) per F.3 / F.7.

**Rationale.** Mid-stream credential failover is fundamentally
unsafe: the client has already consumed partial output (potentially
with `stream: true`), the response has its own streaming headers,
and a mid-stream swap would corrupt the protocol. The constraint is
intrinsic to the architecture, not an arbitrary limit — the system
already has well-defined "winner selected" semantics, and credential
failover lives entirely in the pre-winner window (or, for the
ultimate-internal and `/v1/messages` internal paths, the pre-
first-byte window of the initial provider call).

### F.7 Path Scope (R3-7)

> **AMENDED 2026-08-25 (Round 3b — leader ruling i — anthropic-
> passthrough internal branch IN SCOPE):** the Round-3 path-scope
> table omitted the `handler_anthropic.go:297-345` branch (internal
> model + anthropic-provider credential → `doAnthropicRequest`),
> which bypasses `doAnthropicInternalRequest` entirely. The leader
> ruled this branch IN SCOPE for R3-3 / R3-6 / R3-7 / F.7 for C1
> consistency — the whole `/v1/messages` internal family gets the
> same LB coverage. The branch currently reads the single-cred
> `modelConfig.CredentialID` (Round-1 shape) and MUST be swapped to
> the affinity resolver; the contract statement is owned here, the
> task wiring is owned by the sibling phase file.**

**Ruling.** Applies to the four LB'd internal paths (Round 3b —
fourth row added):

| Path | LB-aware? | Rate-limit failover? | Source |
|---|---|---|---|
| Race-internal (`executeInternalRequest` via `race_executor.go:137`) | YES (Round 1) | YES (Round 3) | `pkg/proxy/race_executor.go:137` `ResolveInternalConfig(req.modelID)` |
| Ultimate-internal (`handler_internal.go executeInternal`) | YES (Round 1) | YES (Round 3) | `pkg/ultimatemodel/handler_internal.go:46` |
| `/v1/messages` internal — `doAnthropicInternalRequest` branch | YES (Round 1, C1) | YES (Round 3) | `pkg/proxy/internal_handler.go:109-111` |
| **`/v1/messages` internal — anthropic-passthrough branch (Round 3b — leader ruling i, NEW IN SCOPE)** | **YES (Round 3b)** | **YES (Round 3b, Round 3c — W1 classification contract)** | `pkg/proxy/handler_anthropic.go:297-345` — for internal models whose credential provider is `"anthropic"`, this branch BYPASSES `doAnthropicInternalRequest` and calls `h.doAnthropicRequest(w, arc)` directly (reads single-cred `modelConfig.CredentialID` + `cred.ResolveAPIKey()`, sets `arc.credentialAPIKey`). MUST be swapped to the affinity resolver for full LB coverage; the same R3-1 / R3-2 / R3-3 / R3-6 pipeline applies (pre-first-byte guard: `arc.headersSent` per `handler_anthropic.go:648-654` — NOT `adapter_anthropic.go:170-176`). **Round 3c — W1 classification source:** this branch produces NO `*ProviderError` (it does not flow through `pkg/providers.OpenAIProvider.handleError`); classify on `arc.lastStatusCode == 429` OR response-body `type` substring `rate_limit`. `Retry-After` is captured via the NEW additive `arc.retryAfter time.Duration` field at the point where `arc.lastStatusCode` is set, `handler_anthropic.go:423`. |
| Race-external env path (`cfg.UpstreamCredentialID`) | NO (single env credential) | NO | `pkg/proxy/race_executor.go:279-300` (env-only path; single credential) |
| Ultimate-external passthrough | NO (client auth passes through; no LB domain) | NO | `pkg/ultimatemodel/handler.go:620-636` (provider probe only; no credential injection) |

**Rationale.** Race-external and ultimate-external do NOT participate
in the LB domain — they have a single credential (env or passthrough),
so there is no other credential to fail over to. Adding the
classification and `ExcludeAndReselect` machinery to those paths would
be pure overhead with no behavioral change. The path-scope ruling
mirrors the Round 1 LB-participation ruling in §D exactly. **The
Round 3b fourth row (anthropic-passthrough branch) IS in the LB
domain** — internal models with anthropic-provider credentials are
the same LB-participating family as the other `/v1/messages`
internal sub-path; excluding this branch would silently strand
credential failover for the C1-primary Claude Code endpoint when the
credential provider is `"anthropic"`.

### F.8 Observability (R3-8)

**Ruling.** New event `model_credential_failover` published via the
existing `events.Bus`; `EngineStats` extended with two additive fields;
new log line prefix.

```go
// pkg/credentiallb/events.go (additions)

const (
    EventBindingDropped            = "model_credential_binding_dropped"  // existing
    EventCredentialsChanged        = "model.credentials.changed"         // existing
    EventCredentialFailover        = "model_credential_failover"         // NEW (R3-8)
)

// Payload (published via events.Bus):
// {
//   "model_id":          string,
//   "from_credential_id": string,         // the credential that 429'd
//   "to_credential_id":   string,         // the next credential selected
//   "reason":             "rate_limit",   // constant for v1; future: "auth_expired" etc.
//   "retry_after_ms":     int64,          // upstream-supplied; 0 if absent
//   "cooldown_ms":        int64,          // effective cooldown applied (> retry_after_ms for fallback default)
//   "attempt_index":      int             // 1..N-1 for this request (per R3-5 budget)
// }
```

```go
// pkg/credentiallb/engine.go (EngineStats extension — Round 3b — gauge semantics for Cooldowns)

type EngineStats struct {
    Hits      uint64  // existing — in-TTL lookup hits
    Misses    uint64  // existing — lookups that selected a fresh credential
    Bindings  uint64  // existing — current binding-map size
    Failovers uint64  // NEW (R3-8) — monotonic counter; total credential failovers (R3-2 calls)
    Cooldowns uint64  // NEW (R3-8, Round 3b — GAUGE) — current cooldown-map size across all models; recomputed at each janitor sweep or read live on Stats(). NOT a monotonic counter.
}
```

**Log line prefix:** `[LB-FAILOVER]` for any log line emitted during
the R3-2 / R3-3 / R3-4 flow (classification, exclusion, rebind, all-
cooling fallback). Example:
`[LB-FAILOVER] model=minimax conv=abc123 from=cred-A to=cred-B reason=rate_limit retry_after=30s attempt=1`.

**Rationale.** (a) Additive `EngineStats` fields preserve the pinned
#5 shape (`Hits`, `Misses`, `Bindings` per model; field names are
**locked**) — the two new fields are appended at the struct tail.
(b) The event payload mirrors the existing
`model_credential_binding_dropped` precedent (no nested objects;
flat string/int64 fields). (c) The log prefix is greppable and
distinct from existing `[RACE]` / `[AUTH]` / `[UltimateModel]` /
`[credentiallb]` prefixes.

---

## Round 3 Base-Drift Re-Verification (fea5874 → 8f67bdf)

> **AMENDED 2026-08-25 (Round 3 — Base-Drift Re-Verification):** Eleven
> commits landed on `pkg/proxy/` + `pkg/ultimatemodel/` between the
> Round 2 base (`fea5874`) and the current working tip (`8f67bdf`,
> rebased onto `master 396fd12`). The Round 1 + Round 2 citations in
> §A–§E and in `technical-analysis.md` were re-verified line-by-line.
> The drift table below lists every site cited in the existing contract
> that moved; Phase-1 targets (`pkg/models/`, `pkg/store/`,
> `migrations/`) are UNTOUCHED and `race_executor.go` source is
> UNTOUCHED (only its test file changed). No contract conflict — every
> drift is line-number drift, not behavioral drift.

| Site | Old cite @ fea5874 | Current @ 8f67bdf | Drift | Status |
|---|---|---|---|---|
| `pkg/proxy/handler.go` post-auth `tokenID` site | 401 | 401 | 0 | ✓ exact |
| `pkg/proxy/handler.go` `initRequestContext` call | 353 | 352 | −1 | drifted |
| `pkg/proxy/handler.go` ultimate `Execute` call | 627 | 635 | +8 | drifted |
| `pkg/proxy/handler_anthropic.go` `doAnthropicInternalRequest` call | 340 | 348 | +8 | drifted |
| `pkg/proxy/handler_anthropic.go` func def (`doAnthropicInternalRequest`) | 447 | 455 | +8 | drifted |
| `pkg/proxy/handler_anthropic.go` `NewInternalHandler` construction | 461 | 478 | +17 | drifted |
| `pkg/proxy/handler_anthropic.go` `anthropicRequestContext` struct | 697 | 740 | +43 | drifted |
| `pkg/proxy/handler_anthropic.go` `CredentialID` probe (phase1 cite) | 294 | 302-303 | +8 | drifted |
| `pkg/proxy/internal_handler.go` `NewInternalHandler` | 38 | 48 | +10 | drifted |
| `pkg/proxy/internal_handler.go` `HandleRequest` signature | 67 | 109 | +42 | drifted |
| `pkg/proxy/internal_handler.go` `ResolveInternalConfig` seam | 69 | 111 | +42 | drifted |
| `pkg/proxy/race_executor.go` `executeRequest` / secondary resolve / primary resolve / DEBUG log | 73 / 112 / 137 / 151 | 73 / 112 / 137 / 151 | 0 / 0 / 0 / 0 | ✓ all exact (source unchanged; only test file changed) |
| `pkg/ultimatemodel/handler_internal.go` `executeInternal` / resolve | 36 / 46 | 36 / 46 | 0 / 0 | ✓ exact |
| `pkg/ultimatemodel/handler.go` provider probe (D3) | 288-289 | 625-635 | **+337** | **MAJOR drift** |
| `cmd/main.go` `/v1/messages` route | 139 | 139 | 0 | ✓ exact |
| `pkg/proxy/handler_functions.go` `initRequestContext` span | 23-126 | 23-131 | end +5 | drifted (end only) |
| `pkg/proxy/handler_helpers.go` `requestContext` struct | 23-108 | 23-108 | 0 | ✓ exact |
| `pkg/ui/server.go` DTO cites | 35 / 390 / 460 / 558 / 673 | same | 0 / 0 / 0 / 0 / 0 | ✓ exact (1-line trivial diff since fea5874) |
| `CredentialID:` sweep (`grep -rn "CredentialID:" --include="*.go"` pkg/ cmd/ test/) | 125 hits / 27 files | **137 hits / 32 files** | +12 / +5 | drifted (expected — new code) |

**New code since fea5874 that the contract must not conflict with.**

| Commit | Feature | Sites | Contract impact |
|---|---|---|---|
| `2e183d3` | `SetThinkingSink` + thinking-sink invariant in `internal_handler.go` | `internal_handler.go` | New `SetThinkingSink` method + `thinkingSink *strings.Builder` field. Round 1 contract's "engine stays decoupled from `events.Bus`" is unaffected. **NEW (Round 3)**: ExcludeAndReselect must not touch `thinkingSink`. |
| `272db86` / `e40db49` | Bare-JSON capture fallback + D3 probe relocation in `ultimatemodel/handler.go` | `ultimatemodel/handler.go:625-635` | D3 provider probe moved from lines 288-289 to 625-635 (+337 line drift). Round 1 contract references the OLD line — must be updated to the new line. **The `modelCfg.CredentialID` probe semantics are unchanged; only the line moved.** |
| `effc345` | `extractNonStreamContent` reasoning capture | `pkg/proxy/` (non-stream path) | Captures `reasoning_content` for non-stream responses. Round 1 contract does not reference this path; no conflict. |
| `396fd12` | SSE `Cache-Control: no-transform` | `pkg/proxy/` SSE write paths | Defeats Cloudflare buffering on SSE. Round 1 contract does not reference headers; no conflict. |

**Existing 429 machinery that the Round-3 contract builds on
(verified).**

| Site | Behavior |
|---|---|
| `pkg/providers/interface.go:155-162` | `ProviderError` struct (the type to extend with `RetryAfter` per R3-1) |
| `pkg/providers/interface.go:168-170` | `ProviderError.IsRetryable()` — existing retry classification |
| `pkg/providers/openai.go:217-224` | `OpenAIProvider.IsRetryable()` — classifies 429 + 5xx as retryable |
| `pkg/providers/openai.go:234-279` | `handleError` — the unmarshal site (parse `{"error":{message,type,code}}`) AND the seam for R3-1's `RetryAfter` write |
| `pkg/providers/openai.go:592-609` | `parseRetryAfter` — **DEAD CODE** today; R3-1 wires it |
| `pkg/proxy/race_coordinator.go:773-893` | `GetFinalErrorInfo` — terminal-shape 429 → `ErrorTypeRateLimit` / `ErrorCodeRateLimit` ("All models rate limited") |
| `pkg/proxy/race_coordinator.go:860-868` | The 429-→-rate_limit mapping (preserved as terminal shape by R3-4) |
| `pkg/models/errors.go:7,17` | `ErrorTypeRateLimit` and `ErrorCodeRateLimit` constants |
| `pkg/proxy/race_executor.go:73,112,137` | Provider call sites (non-stream + stream + primary resolve) — race-internal 429 source |
| `pkg/proxy/race_executor.go:279-300` | Race-external env path — UNCHANGED per R3-7 |

**Precedence baseline that R3-3 plugs into.**

| Site | Behavior |
|---|---|
| `pkg/proxy/handler.go:830` | `newRaceCoordinatorWithEvents(rc.modelList, ...)` — outer model chain |
| `pkg/proxy/race_coordinator.go:190-240` | `spawn` — `modelTypeMain/Second` → `c.models[0]`, `modelTypeFallback` → `c.models[1]` |
| `pkg/proxy/race_coordinator.go:350-364` | `manage()` "Case 1" (function def `:252`) — the interception point for R3-3 |
| `pkg/proxy/handler_finalize.go:48-69` | `handleModelFailure` — publishes `fallback_triggered` event on model switch (preserved unchanged by R3-3) |
| `pkg/proxy/handler.go:894` | `handleRaceFailure("all_models_failed")` — terminal entry point (preserved unchanged by R3-3 / R3-4) |

**Phase-1 targets are UNTOUCHED.** Verified by `git diff fea5874..8f67bdf
--stat -- pkg/models/ pkg/store/ pkg/store/database/`: zero changes to
`pkg/models/`, `pkg/store/`, or any migration file. Round 1's
"Phase-1 schema is locked" invariant is preserved. `race_executor.go`
source is also UNTOUCHED (only its test file changed in this window);
the Round 1 `ResolveInternalConfig` call sites at lines 73 / 112 / 137
remain at exactly the same lines.

---

## Secondary Decisions

### Package + File Layout

```
pkg/credentiallb/
├── engine.go         # Engine struct, GetOrSelect (with newlyBound), OnModelChanged (filter-survivors), janitor, Stats
├── selector.go       # weightedSelector (cumulative-sum + binary search)
├── key.go            # ComputeConversationKey(modelID, tokenID, firstUserMessage)
│                     # ExtractFirstUserMessage (canonical content incl. multimodal A-2)
├── binding.go        # binding struct + TTL helpers
└── *_test.go         # unit tests
```

### Public API Naming Convention

> **REVISED 2026-08-21 (reviewer pass — #16a):** Naming convention
> harmonized (stated once, applies to all code examples below):
>
> - The **`Handler` struct field** in `pkg/proxy/handler.go` is
>   `credEngine *credentiallb.Engine` (lowercase, unexported; matches
>   the existing unexported-field pattern at `pkg/proxy/handler_anthropic.go`
>   and the existing precedent pinned in
>   `technical-analysis.md:623-625`).
> - The **local variable in `cmd/main.go`** is `credLB :=
>   credentiallb.NewEngine(...)` (matches the existing pinned
>   `phase3-plan.md:48`).
> - The rationale: the field name `credEngine` self-documents the
>   semantic role ("the credential-LB engine"), while the local name
>   `credLB` is short and reads cleanly at the call site
>   (`proxy.NewHandler(..., credLB)`). Existing pins in
>   `phase3-plan.md:13,30,48,51,83` and `architecture-recommendation.md`
>   already use this split — the harmonization makes it law for the
>   rest of the contract.

- `Engine` (struct)
- `Engine.GetOrSelect(modelID, conversationKey string) (credentialID string,
  newlyBound bool, err error)` **(AMENDED — W-1)** — the hot-path
  entrypoint. The `newlyBound` bool is **mandatory**: it is the only
  signal that lets the caller publish the `model_credential_selected`
  event **only on first binding** (per request), and it also enables
  the per-binding stats (`Engine.Stats()`) recommended by E-4.
  `newlyBound = true` ⇔ a fresh binding was created for this call;
  `newlyBound = false` ⇔ an existing (in-TTL) binding was reused.
  **The signature MUST return `newlyBound`**, not infer it from a
  post-call map lookup; an inference approach cannot detect
  first-selection-within-a-request.
- `Engine.OnModelChanged(modelID string, newCredentials []models.CredentialRef)`
  **(AMENDED — E-2)** — invalidation entrypoint. **Filters, not clears**:
  preserves bindings whose credential still appears in `newCredentials`;
  drops only orphan bindings (whose credential was removed). This is
  strictly better than the original clear-all semantics — a routine
  weight nudge 1→2 should not flush cache affinity for every active
  conversation on the model.
- `Engine.OnCredentialDeleted(credentialID string)` — drop bindings
  referencing the deleted credential.
- `Engine.RebindFromStore(modelID string, refs []models.CredentialRef)` —
  startup-time bulk load (used by the migration-028 backfill).
- `Engine.Stats()` **(AMENDED — E-4, recommended)** — returns per-model
  binding counts and per-model hit rates for operator visibility.
- `Engine.Stop()` — graceful shutdown (stops janitor goroutine).

### Default Configuration

| Parameter              | Default       | Tunable |
|------------------------|---------------|---------|
| Conversation-key TTL   | 24 hours      | `Engine` constructor arg |
| Janitor sweep interval | 5 minutes     | `Engine` constructor arg |
| Min credential weight  | 1             | Validation |
| Max credentials per model | 16          | Validation (rationale: **bounded memory + sanity guard** — keeps the prefix-sum selector build at O(k) ≤ O(16) per model, bounds the per-model state at hundreds of bytes, and produces a clear validation error before operators can save a typo like 200 weights. The doc-precedent that called this "house ceiling on `fallback_chain_json` length" is **wrong / misleading** — `pkg/models/config.go:64-66` `MaxFallbackDepth` is deprecated and "fallback chain is now single-level (max 1 item)" (validated at `pkg/store/database/store.go:1047` and `pkg/models/config.go:498`); the 16-cap is a fresh cap for this feature, not a match to an existing one. Tunable if 16 ever proves too small.) |
| RNG source **(AMENDED — E-4, recommended)** | Per-model `math/rand/v2` (Go 1.22+) or per-model `*rand.Rand` | Constructor arg; one RNG per model removes the contention that a shared global would create |
| `Engine.Stats()` **(AMENDED — E-4, recommended)** | Always-on | None; returns `{bindings_total, hits_total, misses_total}` per model |

### Logging & Observability

- Log at INFO when a binding is created: `[credentiallb] model=X conv=Y→cred=Z`.
- Log at WARN when a binding is dropped due to credential deletion.
- Log at DEBUG when the janitor evicts entries (counts only).
- Publish an event `model_credential_binding_dropped` when a binding is
  dropped for observability in the existing SSE UI (uses
  `proxy.publishEvent`, not the engine itself).

### Why Engine Lives Outside `pkg/proxy`

- `pkg/proxy` is the request hot path with deep dep tree (auth, store,
  config, events, models, ultimatemodel, toolrepair, usage). Adding
  another layer increases binary size and complicates mocking.
- A separate package lets the engine be unit-tested without spinning up
  any proxy fixtures.

### Event Type Constants

```go
// pkg/credentiallb/events.go
const (
    EventBindingDropped     = "model_credential_binding_dropped"
    EventCredentialsChanged = "model.credentials.changed"
)
```

Published via `proxy.publishEvent` from the engine's caller-side hooks
(NOT from inside the engine — engine stays decoupled from `events.Bus`).

### `firstUserMessage` Extraction Helper

Lives in `pkg/credentiallb/key.go` as `ExtractFirstUserMessage(messages
[]interface{}) string`. Returns the canonical content per A-2:

- If `content` is a string → return the string as-is.
- If `content` is `[]interface{}` (multimodal) → return the canonical
  JSON of the array (sorted keys, no whitespace). Precedent at
  `pkg/ultimatemodel/hash_cache.go:172-186`.
- If `content` is missing/empty/`null`, or no user-role message is
  found → return `""`. The engine treats `""` as
  "no affinity for this request" → weighted-random without persistence.

### Empty Conversation Key (AMENDED — W-2)

The empty-key semantics are **pinned to one reading**:

- `conversationKey == ""` ⇔ **NO binding stored**, fresh weighted pick
  per request. The technical-analysis reading of "" as "its own bucket"
  is **REMOVED** — it created a silent 24h hotspot with zero cache
  benefit. Every request with an empty key re-rolls independently.
- Phase 2 test MUST assert: 100 requests with empty key on a
  multi-credential model produce a distribution matching the
  configured weights (NOT a single-credential hotspot).
- Empty-key requests still flow through the engine so the LB-aware
  call sites work uniformly; only the `Store` step is skipped.

### Order of Operations at Proxy Entry (AMENDED — A-1 wiring)

1. `initRequestContext` runs at `pkg/proxy/handler.go:353`. It parses
   body, sets `rc.resolvedModel`, `rc.originalMessages` (snapshot,
   `handler_functions.go:48-53`). **`rc.tokenID` is NOT yet set at this
   point.**
2. **NEW**: in `initRequestContext`, set
   `rc.firstUserMessage = ExtractFirstUserMessage(rc.originalMessages)`
   (cheap; single map walk). **Do NOT compute `rc.conversationKey` here
   -- the token is unavailable.**
3. `rc.tokenID` is populated at `pkg/proxy/handler.go:401` (from
   `auth.AuthToken`).
4. **NEW**: at the post-auth wiring site (`handler.go:401+`), set
   `rc.conversationKey = ComputeConversationKey(rc.resolvedModel.ID,
   rc.tokenID, rc.firstUserMessage)`. Cheap; one SHA256.
5. `HandleChatCompletions` flows as today. At race-internal call sites
   (`race_executor.go:137`) and ultimate-internal call sites
   (`ultimatemodel/handler_internal.go:46`), the call becomes
   `ResolveInternalConfigWithAffinity(modelID, rc.conversationKey)`.
6. `ResolveInternalConfigWithAffinity` looks up the model's
   `Credentials` list, picks one via the engine, and returns the same
   `(provider, apiKey, baseURL, internalModel, ok)` tuple as today.

### Why `rc.conversationKey` Not Computed Twice

Putting the key on `requestContext` (`pkg/proxy/handler_helpers.go:23-108`)
means it's computed once per request lifecycle and reused by both the
race-path and the ultimate-path. Reset on `rc.reset()` (line 113-132) --
add `rc.conversationKey = ""` and `rc.firstUserMessage = ""` to the
reset list. The key is computed **post-auth** (see step 4 above), not
inside `initRequestContext`.

---

## Open Questions

None blocking. Listed for the dispatcher to confirm during planning review:

1. **Max credentials per model**: I picked 16 to match the
   `fallback_chain_json` ceiling. If users have models with 50+ credentials
   in mind, raise this. (No evidence in the codebase either way.)
2. **TTL of 24 hours**: matches typical Claude-Code conversation length.
   If the system supports multi-day agent loops, raise to 7 days. Tunable
   per-engine-instance -- no schema impact.
3. **E2E test mock upstream**: I assume ports 4001 + 4002 (the existing
   pattern is 4001). If multiple test scripts need distinct port ranges,
   coordinate via `test_mock_clean_ports.sh`.

---

## Tracked Issues (file at merge time -- LEADER-MANDATED)

> **AMENDED 2026-08-21 (architect council / leader rulings):** Two
> tracked issues get filed at merge time:

1. **029 cleanup migration** (`DROP INDEX idx_models_credential_id +
   DROP COLUMN credential_id`) -- **LOAD-BEARING FOR M-1**.
   Without this issue landing, the dual-source debt the original §B
   was avoiding silently returns. Should be scheduled in the next
   release cycle, gated on a release-note deprecation window.

2. **`config.updated` event payload mismatch** (pre-existing, **out of
   feature scope**) -- `pkg/config/config.go:518` publishes a struct;
   `pkg/ultimatemodel/handler.go:155` asserts `map[string]interface{}`
   and reads `data["field"]` ⇒ the assertion always fails ⇒ the
   ultimate hash-cache reset on config change is **dead code**.
   Confirmed by both councilors; non-regressing; the new typed
   `model.credentials.changed` event does not depend on it. Filed
   separately.

---

## References

- `pkg/proxy/handler_functions.go:23-126` — `initRequestContext`
- `pkg/proxy/handler_helpers.go:23-108` — `requestContext` struct
- `pkg/proxy/race_executor.go:137` — race-internal credential resolution
- `pkg/proxy/race_executor.go:279-300` — race-external UpstreamCredentialID
- `pkg/ultimatemodel/handler_internal.go:46` — ultimate-internal resolution
- `pkg/ultimatemodel/handler.go:287-293` — ultimate-external provider probe
- `pkg/ultimatemodel/handler.go:150-160` — `OnConfigChange` precedent
- `pkg/ultimatemodel/hash_cache.go:158-193` — `HashMessages` (NOT reused;
  changes every turn)
- `pkg/store/database/store.go:1061-1063` — current credential validation
- `pkg/store/database/store.go:1289-1295` — current in-use guard
- `pkg/store/database/store.go:1316-1350` — `ResolveInternalConfig`
- `pkg/store/database/migrate.go:24-52` — migration ordering
- `pkg/store/database/migrations/sqlite/001_initial.up.sql` — DDL baseline
- `pkg/store/database/migrations/sqlite/024_convert_allowed_models_to_ids.up.sql`
  — JSON-array data migration precedent
- `pkg/store/database/migrations/sqlite/027_add_exclude_from_ultimate_switching.up.sql`
  — most recent column-add precedent
- `pkg/models/credential.go:131-166` — RWMutex map pattern
- `pkg/usage/counter.go:49-67` — immediate UPSERT precedent
- `pkg/events/bus.go` — pub/sub bus
- `pkg/config/config.go:518` — config publish site (tech-debt noted in §C)
- `.agents/shared/planning/exclude-ultimate-switching/plan.md` — sibling
  feature plan (style precedent)

---

## Amendment Log (2026-08-21 — leader-final)

> **AMENDED 2026-08-21 (architect council / leader rulings):** Two
>  rounds of leader-ruled amendments have been applied to this
>  decision log. The first round (architect council) is preserved
>  inline (`AMENDED 2026-08-21` blockquotes at each section head).
>  The second round (reviewer punch-list) is preserved below — every
>  Round-2 amendment is reversible by greppable string.

### Round 2 — Reviewer Punch-List (2026-08-21)

| # | Item | Section(s) updated | One-line summary |
|---|------|---------------------|------------------|
| **C1** | Anthropic `/v1/messages` internal path in scope | §D Backward Compatibility & Rollout (Path table + new blockquote) | `/v1/messages` is the PRIMARY client endpoint (Claude Code), reached via `cmd/main.go:139` → `handler_anthropic.go:340` → `handler_anthropic.go:461` (NewInternalHandler) → `internal_handler.go:67` (HandleRequest). LB must work there; the 5-tuple `ResolveInternalConfig` call at `internal_handler.go:69` is the seam to replace. Thread: `anthropicRequestContext.conversationKey` → `InternalHandler.HandleRequest` → `ResolveInternalConfigWithAffinity`. |
| **C2** | Empty-key `newlyBound=false` invariant | (no §X here; cross-ref to technical-analysis.md API Contract — this file already states "no binding stored" for empty key in §A "No first message found" and §C, matching the invariant) | Inherited — confirmed by the §A "weighted random without persistence" language and the §C single-credential fast path. The 3-tuple/struct return shape pinned in §C makes this directly observable. |
| **#3** (REVISED — leader correction) | `ResolveInternalConfigWithAffinity` return shape pinned (struct form) | §C (Public API Naming Convention — `Engine.Stats()` reference); cross-ref to technical-analysis.md §API Contract | Decision: **struct `ResolvedCredential` + trailing `ok bool`** (REVISED 2026-08-21 — leader-ruled struct branch of the 6-tuple/struct ruling; struct carries BOTH `credentialID` AND the leader-ruled `newlyBound` — tuple form could not carry both without a 7th element). The struct is the ONLY way `newlyBound` flows from the engine-binding-store side effect through to the proxy/ultimatemodel handlers, since those call sites receive the resolution result through this seam — if `newlyBound` does not flow through here, the W-1 only-on-first-binding event is unimplementable at the call sites. Field mapping: `ResolvedCredential.Provider/APIKey/BaseURL/InternalModel` ↔ legacy 5-tuple; **NEW** `CredentialID` (for `#16f` cache-affinity observability + `model_credential_selected` event payload); **NEW** `NewlyBound` (for W-1). The trailing `ok bool` is kept for parity with the legacy 5-tuple. (The previous Round-2 6-tuple pin was REJECTED by the leader because it dropped `newlyBound` — corrected to the struct form.) (Engine-internal `Stats().Bindings` shape is pinned separately in technical-analysis.md §API Contract — #5.) |
| **#5** | `Engine.Stats()` `Bindings` field name | §C (Public API Naming Convention — `Engine.Stats()` description) | Pinned shape: `Engine.Stats() map[string]EngineStats` where `EngineStats{Hits, Misses, Bindings uint64}`; the per-model binding-count field is named `Bindings` (no other alias). |
| **#6** | Stale 2-tuple doc-comment | (no §X here — the doc-comment lives in technical-analysis.md §API Contract region around line 429; this file uses the 3-tuple consistently) | Fixed in technical-analysis.md by the Round-2 amendment. |
| **#10** | Sliding idle TTL (not fixed-lifetime) | §C TTL & Eviction + new "Sliding-TTL semantics" blockquote; §D Single-Credential Fast Path | `boundAt` is refreshed on every in-TTL `GetOrSelect` hit. The binding is eligible for expiry only when `now - boundAt > ttl` (24h of consecutive idle). Janitor sweeps idle-expired bindings; lazy expiry checks idle time on lookup. E-2 filter-survivors preserves `boundAt` on surviving bindings. §A lifetime language clarified: 24h is an /idle/ ceiling, not a /lifetime/ ceiling. |
| **#16a** | Naming convention (handler field vs main.go local) | §C Public API Naming Convention (new blockquote at top) | `Handler.credEngine *credentiallb.Engine` (unexported field, `pkg/proxy/handler.go`); `credLB := credentiallb.NewEngine(...)` (local in `cmd/main.go`). Convention stated once, applies throughout. |
| **#16b** | 023/024 cited as the precedent for `.down.sql` files existing | §B Migration Runner Update | Precedent files: `pkg/store/database/migrations/sqlite/023_add_ultimate_model.down.sql` and `pkg/store/database/migrations/sqlite/024_convert_allowed_models_to_ids.down.sql`. |
| **#16c** | `.down.sql` files are NOT auto-run by the runner | §B Migration Runner Update (new blockquote) | Verified by `pkg/store/database/migrate.go:24-52` (the `migrations` array lists only `.up` names) and `pkg/store/database/migrate.go:62-67` (the `RunMigrations` loop iterates `migrations` only). `.down.sql` files are manual rollback artifacts. |
| **#16d** | 16-cap rationale — bounded memory + sanity guard, tunable | §C Default Configuration table (the `Max credentials per model` row) | Original rationale ("matches house ceiling on `fallback_chain_json` length") is **wrong** — `pkg/models/config.go:64-66` (deprecated) and `pkg/store/database/store.go:1047` show the actual house ceiling is 1 fallback. The 16-cap is a fresh sanity guard for this feature, tunable. |
| **#16f** | Credential-scoped upstream prompt-cache note | §A "Problem" section (new blockquote) | Upstream provider prompt caches are keyed per-credential; conversation-sticky affinity is required to keep caches hot. This is the load-bearing reason the §A key formula exists. |

### Round 3 — Rate-Limit Failover (2026-08-25)

> **AMENDED 2026-08-25 (Round 3 — Rate-Limit Failover):** Eight
> pre-pinned dispatcher rulings (R3-1..R3-8) applied as a SURGICAL
> AMENDMENT to the existing contract. Existing rulings (A-1, A-2, M-1,
> C1, C2, W-1..W-3, E-1..E-4, #3, #5, #6, #10, #16a-d, #16f) are
> UNCHANGED. Phase-1 schema targets (`pkg/models/`, `pkg/store/`,
> `migrations/`) are UNTOUCHED. `race_executor.go` source is
> UNTOUCHED. A sibling worker amends the phase files against the same
> rulings; the **contract wins** rule (per the plan header) governs.

| # | Ruling | Section(s) added | One-line summary |
|---|--------|---------------------|------------------|
| **R3-1** | Rate-limit classifier + retry-after extraction | §F.1 Rate-Limit Classifier | `IsRateLimitError(err error) bool` + `ProviderError.RetryAfter time.Duration` added to `pkg/providers`. Wires the existing-but-dead `parseRetryAfter` (`pkg/providers/openai.go:592-609`) inside `handleError` (`pkg/providers/openai.go:234-279`). Classifier reads the already-unmarshaled `{"error":{message,type,code}}` body — no new provider integration required. |
| **R3-2** | Affinity break + `ExcludeAndReselect` | §F.2 Affinity Break | Engine gains `ExcludeAndReselect(modelID, conversationKey, excludedCredID, retryAfter) (credentialID, mode ReselectMode)` (**Round 3b — SUPERSEDES Round-3 `(credentialID, ok)` signature, B3 mode-aware**; `ReselectHealthy`/`ReselectSoonestExpiry`/`ReselectNone` enum shape; leader accepted the B3-recommended single-call enum over the two-call `ok bool`+`PickSoonestExpiring` alternative). Marks `excludedCredID` cooling (cooldown seeded from `retryAfter`, else 60s default), REBINDS the existing `(modelID, conversationKey)` entry to next healthy credential in weighted order, skipping cooling credentials. Single map write under existing locking discipline. **Round 3b — B2 idempotent precondition:** rebind only if `bindings[convKey].credentialID == excludedCredID`; otherwise no-op returning the current unchanged binding (mode=ReselectNone). Per-conversation retry accounting (tried-this-request set) lives in the **request context** (not the engine). |
| **R3-3** | Precedence ordering — credential failover BEFORE model fallback | §F.3 Precedence Ordering | Interception point: `pkg/proxy/race_coordinator.go` `manage()` (function def `:252`) "Case 1" (current lines 350-364). Decision tree: if failed model internal AND >1 creds AND error classifies rate-limit AND no CONTENT flushes pre-winner (race: `c.winner == nil`; ultimate: initial-call `*ProviderError` type assertion; /v1/messages: `arc.headersSent` per `handler_anthropic.go:648-654`) AND remaining non-cooling creds exist AND budget not exhausted → spawn same-model-next-credential attempt (`modelTypeCredFailover` / `triggerRateLimit`); else fall through to existing model fallback. Ultimate-internal (`pkg/ultimatemodel/handler_internal.go:36-46`), `/v1/messages` internal (`pkg/proxy/internal_handler.go:109-111`), and the `/v1/messages` anthropic-passthrough branch (`handler_anthropic.go:297-345`, Round 3b leader ruling i — in scope) get equivalent hooks. **Round 3b:** B1 gate exemption for `modelTypeCredFailover` attempts (separate spawn-window accounting vs model attempts); Case-2 uniform classification (leader ruling iii); Case-1 latest-only inspection (accepted v1 behavior); `modelTypeSecond` re-key (specified); canonical naming `modelTypeCredFailover`. |
| **R3-4** | Per-credential cooldown state + soonest-expiry all-cooling fallback | §F.4 Cooldown State | In-memory `cooldowns map[modelID]map[credID]time.Time` guarded by engine's existing outer+per-model locking discipline (E-1). Janitor sweeps expired cooldowns alongside idle-TTL bindings in same pass. Weighted selection + `ExcludeAndReselect` skip cooling credentials. **All-cooling fallback** (Round 3b — 0-of-N ONLY pin): pick SOONEST-EXPIRING credential (availability beats strict cooldown), log WARN, **single attempt only (no loop)** — caller enforces via B3 mode-aware `ReselectSoonestExpiry` (engine emits WARN; caller does single-attempt-then-fall-through, no second `ExcludeAndReselect` call). 1-of-N healthy takes the normal healthy-skip path; F.4 prose is clarified. `pkg/proxy/race_coordinator.go:860-868` "All models rate limited" remains terminal. **FULL ALTERNATIVES TABLE**: soonest-expiry vs fail-fast vs block-until-expiry (the only ruling with a full table in this round). Soonest-expiry wins: best availability, bounded latency, no new primitives, aligns with R3-5 retry budget. **Round 3b — amendment 7 (gauge semantics):** `EngineStats.Cooldowns` is a GAUGE (current cooldown-map size, recomputed at each janitor sweep or read live on `Stats()`), NOT a monotonic counter; the Round-3 increment-per-removal prose at `decisions.md:1291-1293` is REPLACED. `Failovers` remains a monotonic counter. |
| **R3-5** | Retry budget | §F.5 Retry Budget | Each remaining credential tried AT MOST ONCE per request. Total credential-failover attempts bounded by `len(credentials) - 1`. No repeats, no infinite loops. Caller-side (request context) tried-set enforces the cap. Worst-case added latency: `(N-1) × 429-RTT` (429s typically fast, no body wait). |
| **R3-6** | Streaming constraint | §F.6 Streaming Constraint | Failover applies ONLY before first CONTENT byte sent to client (Round 3b — invented SSE framing bytes exempted: `handler.go:783-797` writes headers + `": connected\n\n"` comment BEFORE `coordinator.Start()`, but these carry no model content). Race path: `c.winner == nil` (Round 3b — invented `chunksFlushedToClient`/`bytesFlushedToClient` PURGED; `status != statusStreaming` vacuous and PURGED). Ultimate-internal: initial-call `*ProviderError` type assertion (Round 3b — `headersSent` UNUSABLE because `ultimatemodel/handler.go:608-609` pre-sets it before `executeInternal`). `/v1/messages`: recorder/`arc.headersSent` per `handler_anthropic.go:648-654` (Round 3b — cite fixed from `adapter_anthropic.go:170-176` which is `SetStreamHeaders`). Three path-specific implementations documented in the precedence tree section. **Round 3b — leader ruling i:** the `/v1/messages` anthropic-passthrough branch (`handler_anthropic.go:297-345`) is in scope and uses the same `arc.headersSent` guard. |
| **R3-7** | Path scope | §F.7 Path Scope | **Four** LB'd internal paths participate (Round 3b — leader ruling i added the anthropic-passthrough fourth row): race-internal (`executeInternalRequest`), ultimate-internal (`handler_internal.go executeInternal`), `/v1/messages` internal (`internal_handler.go HandleRequest`), and `/v1/messages` anthropic-passthrough branch (`handler_anthropic.go:297-345` — internal model + anthropic-provider credential → `doAnthropicRequest`, bypasses `doAnthropicInternalRequest`; MUST be swapped from single-cred `modelConfig.CredentialID` to the affinity resolver). **UNCHANGED**: race-external env path (`cfg.UpstreamCredentialID`, `race_executor.go:279-300`), ultimate-external passthrough. Single env credential = no LB domain. |
| **R3-8** | Observability | §F.8 Observability | New event `model_credential_failover` {model_id, from_credential_id, to_credential_id, reason:"rate_limit", retry_after_ms, cooldown_ms, attempt_index} published via existing `events.Bus`. `EngineStats` EXTENDED additively with `Failovers uint64` (monotonic counter — total successful `ExcludeAndReselect`) + `Cooldowns uint64` (**Round 3b — amendment 7 GAUGE**, not a counter; current cooldown-map size recomputed at each janitor sweep; the Round-3 increment-per-removal prose at `decisions.md:1291-1293` is REPLACED). Pinned #5 field names `Hits`, `Misses`, `Bindings` unchanged. New log prefix `[LB-FAILOVER]`. |
| **R3-drift** | Base-drift re-verification | Round 3 Base-Drift Re-Verification (fea5874 → 8f67bdf) | Eleven commits landed between fea5874 and 8f67bdf. Drift table covers 19 sites (most exact, several line-drifts, one MAJOR drift +337 on `pkg/ultimatemodel/handler.go:288-289 → 625-635`). **Phase-1 schema targets untouched**. `race_executor.go` source untouched. New code since fea5874 (`SetThinkingSink`, bare-JSON capture fallback, D3 probe relocation, `extractNonStreamContent`, SSE no-transform) does NOT conflict with R3-1..R3-8. |

**Round 3 contract wins (cross-reference matrix for sibling workers).**

| Worker concern | Round 3 ruling | Section in this file | Section in technical-analysis.md |
|---|---|---|---|
| Provider error type extension | R3-1 | §F.1 | New `### pkg/providers — error-classification additions` |
| Engine API surface | R3-2 | §F.2 | `### pkg/credentiallb — engine additions (Round 3)` block |
| Precedence tree wiring | R3-3 | §F.3 | New `## Rate-Limit Failover Precedence Tree` |
| Cooldown map + locking | R3-4 | §F.4 | `## Concurrency Model` — new "Round-3 Cooldown Map Locking" sub-section |
| Retry budget invariant | R3-5 | §F.5 | "Rate-Limit Failover Precedence Tree" + `### pkg/providers` |
| Pre-first-byte guard | R3-6 | §F.6 | "Rate-Limit Failover Precedence Tree" + Backward Compatibility Matrix rows |
| Path scope | R3-7 | §F.7 | Backward Compatibility Matrix rows |
| Event/stats/log schema | R3-8 | §F.8 | `### pkg/credentiallb — engine additions (Round 3)` |
| Drift verification | R3-drift | "Round 3 Base-Drift Re-Verification" | References (drift table appended) |

**Cross-document invariants preserved (Round 3 does not relax any).**

- **#10 (sliding idle TTL, NOT fixed-lifetime)**: A cooldown does NOT touch `boundAt` (cooldown is a separate map). A rebind via `ExcludeAndReselect` DOES refresh `boundAt` for the new credential (same as a fresh `GetOrSelect` would). Pinned explicitly in §F.4 and cross-ref'd in technical-analysis.md Concurrency Model.
- **#5 (`EngineStats` field names locked)**: New `Failovers` + `Cooldowns` fields are appended at the struct tail; `Hits`, `Misses`, `Bindings` unchanged.
- **C1 (`/v1/messages` internal path in scope)**: Round 3 R3-3 + R3-7 confirms the path participates; the Round 1 conversation-key wiring (`anthropicRequestContext.conversationKey` → `InternalHandler.HandleRequest` extra arg → `ResolveInternalConfigWithAffinity`) is the entry point that the Round 3 `ExcludeAndReselect` is called from.
- **W-2 (empty-key ⇔ NO binding stored)**: Empty-key requests cannot trigger `ExcludeAndReselect` because they have no binding to rebind. The request context's tried-set is also empty-key-unaware (no affinity = no per-conversation retry accounting needed). Empty-key 429s fall through directly to model fallback (R3-3's else branch).
---

## Round 3b — Architect Review Amendments (2026-08-25)

> **AMENDED 2026-08-25 (Round 3b — Architect Stress-Test):**
> Round 3 was architect stress-tested against HEAD @ 8f67bdf; the
> review (`architecture-review-round3.md`) returned
> **AMEND-AND-ADOPT** with eight consolidated amendments + three
> leader rulings. **R3-1..R3-8 SEMANTICS STAND** — these are
> specification-level fixes, not re-decisions. Amendments applied
> in place per section (preserving audit trail with "superseded
> Round 3" markers); this changelog documents the mapping for
> sibling-worker verification. The contract wins rule (per the
> plan header) governs — sibling phase workers amend against the
> SAME rulings.

### Amendment-to-Section Mapping (review items + leader rulings)

| # | Source | Title | Files amended (this contract owns) | Sections amended in this file |
|---|--------|-------|--------------------------------------|---------------------------------|
| **1 (B1)** | Review blocking finding | **Spawn-window gate exemption for `modelTypeCredFailover`** | decisions.md (this file) + technical-analysis.md | §F.3 Precedence Ordering (B1 gate exemption + all-failed amendment); §F.3 superseded Round-3 audit trail |
| **2 (B2)** | Review blocking finding | **`ExcludeAndReselect` idempotent precondition** | decisions.md (this file) + technical-analysis.md | §F.2 (B2 precondition + audit trail) |
| **3 (B3)** | Review blocking finding | **Mode-aware `ExcludeAndReselect` return** (single-call enum shape) | decisions.md (this file) + technical-analysis.md | §F.2 (B3 signature supersession + `ReselectMode` enum); §F.4 (mode-aware all-cooling fallback) |
| **4** | Review amendment | **Real streaming guards** (purge invented state) | decisions.md (this file) + technical-analysis.md | §F.6 (real guards: race `c.winner == nil`, ultimate initial-call `*ProviderError`, /v1/messages `arc.headersSent` with cite fix to `handler_anthropic.go:648-654`); §F.3 superseded Round-3 audit trail |
| **5** | Review amendment + **leader ruling i** | **Anthropic-passthrough internal branch in scope** | decisions.md (this file) + technical-analysis.md | §F.7 (fourth row in path-scope table); §F.3 (third hook bullet — leader ruling i) |
| **6** | Review amendment | **Classifier vocabulary table + 503 row + absent-`Retry-After` flow + HTTP-200 out-of-scope** | technical-analysis.md only (this file's §F.1 unchanged at spec level — the classifier interface is unchanged; the table + matrix are pinned in the contract; the out-of-scope note goes in technical-analysis.md) | (none at decision level — `§F.1` R3-1 semantics unchanged) |
| **7** | Review amendment | **`EngineStats.Cooldowns` gauge semantics** | decisions.md (this file) + technical-analysis.md | §F.8 (EngineStats extension comment); §F.4 (Janitor interaction — REPLACE increment-per-removal prose) |
| **8** | Review amendment + leader ruling | **E2E premise correction** (no `--hit-count-file` precedent; build per-credential mock in Round-1 E2E work or defer E2E counterpart) | **sibling phase worker only** (phase5 Task 23); this file is unaffected at the contract level | (none) |
| **Leader ruling ii** | Leader ruling | **Single-call enum shape for `ExcludeAndReselect` (B3)** — leader accepted the B3-recommended single-call `ReselectMode` enum over the two-call `ok bool` + `PickSoonestExpiring` alternative | decisions.md (this file) + technical-analysis.md | §F.2 (B3 signature, with one-line rationale pinned); §F.4 (mode semantics) |
| **Leader ruling iii** | Leader ruling | **Case-2 uniform classification — R3-3 SUPERSEDES the review's "non-goal" reading for Case-2 idle bypass** | decisions.md (this file) + technical-analysis.md | §F.3 (Case-2 idle-trigger classification paragraph + audit trail); §F.8 (R3-3 changelog entry) |
| **Symbol fixes** | Review §1 citation nits | **`monitorLoop` → `manage()` (`:252`), `spawnAttempt` → `spawn()` (`:189`); canonical Go identifier `modelTypeCredFailover`** (purge `modelTypeCredentialFailover`/`credential_failover` from prose where they name the Go constant) | decisions.md (this file) + technical-analysis.md | §F.3 (function/symbol names); §F.7 table; §F.8 R3-3 changelog row; Precedence Baseline table |
| **Pre-first-byte guard operator docs** | Review §1 sub-finding | **Case-1 latest-only inspection (accepted v1 behavior), `modelTypeSecond` re-key (specified behavior)** | decisions.md (this file) + technical-analysis.md | §F.3 (both pins) |
| **Operator note** | Review §3 pin | **1-of-N healthy load concentration + gradual re-distribution (no switch-back thundering)** | decisions.md (this file) + technical-analysis.md | §F.3 operator note (no code) |

### Cross-reference matrix (Round 3b — for sibling worker verification)

| Worker concern | Round 3b amendment | decisions.md section | technical-analysis.md section |
|---|---|---|---|
| Spawn-window gate exemption | B1 | §F.3 (B1 gate exemption paragraph + all-failed amendment) | Rate-Limit Failover Precedence Tree (gate exemption row) |
| `ExcludeAndReselect` precondition | B2 | §F.2 (B2 precondition paragraph) | engine-additions ExcludeAndReselect invariants block |
| Mode-aware return | B3 (leader ruling ii) | §F.2 (B3 supersession + ReselectMode enum) + §F.4 (mode semantics) | engine-additions ExcludeAndReselect (mode-aware signature) + precedence tree (mode-based fall-through) |
| Real streaming guards | 4 | §F.6 (real guards) | Precedence Tree (real guards) + Pre-First-Byte Guard table (corrected) |
| Passthrough branch in scope | 5 + leader ruling i | §F.7 (fourth row) + §F.3 (third hook bullet) | Precedence Tree (fourth path) + Backward Compatibility Matrix |
| Classifier vocabulary table + 503 row + out-of-scope | 6 | (decision-level §F.1 unchanged — pinned in technical-analysis.md) | Classifier section (vocabulary table + 503 row + out-of-scope note) |
| Gauge semantics | 7 | §F.8 (EngineStats extension) + §F.4 (Janitor interaction REPLACE) | Engine additions block (EngineStats extension) + Cooldown map locking (janitor note) |
| Case-2 uniform classification | leader ruling iii | §F.3 (Case-2 paragraph + audit trail) | Precedence Tree (Case-2 path) + Three-Path Coverage |
| Case-1 latest-only / modelTypeSecond re-key | sub-finding pins | §F.3 (both pins) | Precedence Tree pseudocode (Case-1 latest-only) + pseudocode (modelTypeSecond re-key) |
| Operator docs (1-of-N load + gradual re-distribution) | sub-finding pin | §F.3 (operator note) | operator note in Precedence Tree section |

### Cross-document invariants preserved (Round 3b does NOT relax any)

- **#10 (sliding idle TTL, NOT fixed-lifetime)**: A cooldown does NOT touch `boundAt` (cooldown is a separate map). A rebind via `ExcludeAndReselect` DOES refresh `boundAt` for the new credential (same as a fresh `GetOrSelect` would). Pinned explicitly in §F.4 and cross-ref'd in technical-analysis.md Concurrency Model. Round 3b does NOT change this.
- **#5 (`EngineStats` field names locked)**: New `Failovers` + `Cooldowns` fields are appended at the struct tail; `Hits`, `Misses`, `Bindings` unchanged. Round 3b only changes `Cooldowns` semantics (counter → gauge), NOT its field name or position. Add-only rule preserved.
- **C1 (`/v1/messages` internal path in scope)**: Round 3 + Round 3b confirm the path participates. Round 3b (leader ruling i) extends coverage to the anthropic-passthrough sub-branch — same family, same LB coverage.
- **W-2 (empty-key ⇔ NO binding stored)**: Empty-key requests cannot trigger `ExcludeAndReselect` because they have no binding to rebind. With B3 mode-aware return, empty-key returns `ReselectNone` (caller falls through to model-fallback); Round 3b pins this explicitly in §F.2 invariants. **Round 3c — C2 supersession:** empty-key now returns `ReselectHealthy` with a fresh weighted-random among non-cooling (renormalized) pick and NO map write — see the Round 3c changelog for the rationale (R5: do not model-switch while a healthy credential exists). The Round 3b wording is REPLACED in-place; the Round 3c changelog preserves the audit trail.
- **E-1 (janitor outer RLock + per-model write locks)**: The Round 3 cooldown sweep uses the SAME locking discipline. Round 3b only changes the `Cooldowns` field semantics (counter → gauge recomputation), NOT the lock ordering.
- **R3-1..R3-8 SEMANTICS STAND**: every ruling's behavior is unchanged; the eight amendments + three leader rulings are specification-level fixes (gate exemption, idempotent precondition, mode-aware return, real guards, path-scope row, classifier vocabulary, gauge semantics, E2E premise correction, single-call enum shape, Case-2 uniform classification).

## Round 3c — Reviewer Findings (2026-08-25)

> **AMENDED 2026-08-25 (Round 3c — Reviewer Findings):** A reviewer
> pass against Round 3b returned APPROVED-WITH-NOTES gated on one
> mandatory revision: 3 critical pins (C1, C2, C3), 4 warnings
> (W1, W2, W3, S6 — leader ruled S6 YES), 3 suggestions (S1,
> S2, S3) — all pin-level. The leader pre-ruled every open
> decision point (folded below). Amendments applied in place per
> section; this changelog documents the mapping for sibling-worker
> verification. The contract wins rule (per the plan header)
> governs — sibling phase workers amend against the SAME rulings.

### Item → Section Mapping

| # | Source | Title | Sections amended in this file | Sections amended in technical-analysis.md |
|---|--------|-------|---------------------------------|----------------------------------------------|
| **C1** | Reviewer critical | **Hoisted credFailover pre-checks (C1) — admission condition** | §F.3 (decision tree REPLACED with hoisted form: pre-checks ABOVE window gate; `gate := modelAttempts < len(models) \|\| credFailoverEligibleWithBudget()`; C1 semantic-equivalence note to rejected cap-extension; terminal guard preserved) | "Rate-Limit Failover Precedence Tree" §Pseudocode (Race-internal) — hoisted form block + admission gate expression |
| **C2** | Reviewer critical | **`ReselectMode` semantics unified to phase2's reading (C2)** | §F.2 (enum comments + return-bullets REPLACED with C2 mapping: (a) B2 no-op → ReselectHealthy, (b) empty convKey → ReselectHealthy fresh non-cooling pick, (c) ReselectNone only for single-cred or 0 valid creds; S2 pin: tried-set lives in requestContext, not engine); §F.4 (B3 mode-aware bullets updated for C2 narrowing) | engine-additions ReselectMode enum + ExcludeAndReselect invariants; Precedence Tree §Pseudocode (mode handling); Failure Modes table — all four surfaces agree |
| **C3** | Reviewer critical | **Three nonexistent references (C3)** — `spawnTriggerInfo.credentialID`; `case modelTypeCredFailover: modelID = c.models[0]`; `newRaceCoordinatorWithEvents` gains `engine` + `conversationKey` constructor params (NO `Config.CredEngine` field) | §F.3 (decision tree 1-2: C3(1) + C3(2) inline); new "Race coordinator constructor wiring (Round 3c — C3(3))" sub-section under §F.3 | Precedence Tree §Pseudocode (C3(1)/(2)/(3) inline); Three-Path Coverage (no change — wiring is at the constructor, not the per-path) |
| **W1** | Reviewer warning | **Passthrough classification source (W1)** — anthropic-passthrough branch produces NO `*ProviderError`; classify on `arc.lastStatusCode == 429` OR response-body `type` substring `rate_limit`; NEW additive `arc.retryAfter time.Duration` captured where `arc.lastStatusCode` is set (handler_anthropic.go:423) | §F.6 (AMENDED blockquote adds W1 passthrough classification contract); §F.7 path-scope table row 4 (W1 classification source pinned in the "Source" column) | Three-Path Coverage row 4 (W1 classification contract appended); Precedence Tree §Pre-First-Byte Guard (passthrough row cross-ref W1) |
| **W2** | Reviewer warning | **`ProviderError` extension (W2)** — additively gains `ErrorType string` + `ErrorCode string`, populated in `handleError` from already-unmarshaled anonymous-local fields (openai.go:238-246) | §F.1 (ProviderError struct extended additively; Round 3c — W2 wiring paragraph in Parser wiring block) | `pkg/providers — error-classification additions` (ProviderError struct + IsRateLimitError comment updated); classifier vocabulary table cross-ref |
| **W3** | Reviewer warning | **SoonestExpiry single-shot pseudocode (W3)** — `soonestExpiryAttempted` flag + `continue` after soonest-expiry attempt (no double-spawn; next 429 routes to model-fallback) | §F.4 (Round 3c — W3 paragraph under Latency note); §F.4 mode-aware bullets (W3 cross-ref) | Precedence Tree §Pseudocode (soonestExpiryAttempted set + continue after ReselectSoonestExpiry spawn); Failure Modes table (W3 cross-ref) |
| **S1** | Suggestion | **`rate_limit_exceeded` literal in code-equality set (S1)** | §F.1 (Classifier vocabulary paragraph cross-refs S1; the vocabulary table itself lives in technical-analysis.md) | `pkg/providers` vocabulary table row added; Match semantics paragraph extended; Unit-test matrix rows 12 + 13 added |
| **S2** | Suggestion | **Tried-set home in requestContext, not engine (S2)** | §F.2 (one sentence added to ExcludeAndReselect doc-comment: tried-set mutated only from manage() loop, lives in requestContext, engine never reads/writes it) | engine-additions ReselectMode invariants block (S2 pin added); Round-3b Tried-Set Single-Goroutine Mutation Invariant cross-ref |
| **S3** | Suggestion | **Wording standardization (S3)** — "weighted random among non-cooling (renormalized)" replaces "weighted order skipping cooling" / "weighted selection" / "skip-then-pick" wherever selection-after-cooldown is described in ACTIVE spec text | §F.4 (Selection skip rule paragraph REPLACED with S3 standardized wording + the audit-trail-untouched note) | engine-additions weightedSelector pseudocode (S3 wording inserted); Selection skip rule cross-ref; Precedence Tree §Pseudocode (uses standardized wording inline) |
| **S6** | Suggestion (leader ruled YES) | **`OnCredentialDeleted` clears cooldowns (S6)** — single-pass loop clears `cooldowns[modelID][credentialID]` across all models; gauge recomputed at next sweep (or live read) | §F.4 (new AMENDED blockquote: S6 cooldown hygiene); §F.8 cross-ref (EngineStats.Cooldowns gauge recomputation note) | Invalidation Semantics table (Credential-deleted row extended with S6 cooldown clear); engine additions invariants block (S6 bullet) |

### Rationale pins (the non-obvious decisions)

- **C2 narrowing rationale (recorded for audit trail):** the prior
  Round 3b reading that returned `ReselectNone` for both B2 no-op
  AND empty-`conversationKey` was REJECTED because it would force
  the caller to model-switch while a healthy credential exists,
  violating R5 (Round-1 reviewer-5 — "do not model-switch when a
  healthy credential is available"). The narrow `ReselectNone` to
  "no credential exists / no credential valid" restores the R5
  invariant.
- **C1 semantic-equivalence note:** the hoisted form
  `gate := modelAttempts < len(models) ||
  credFailoverEligibleWithBudget()` is SEMANTICALLY EQUIVALENT to
  the rejected cap-extension alternative (`cap += len(credentials)−1`).
  Both admit `len(credentials) − 1` additional attempts before the
  terminal. Cap-extension was REJECTED purely as an IMPLEMENTATION
  (mutation of len-dependent invariants elsewhere — the existing
  `:338` / `:420-421` reads of `len(c.models)` would need
  synchronized widening across multiple sites); the hoisted form
  is the same semantic admission expressed via a separate
  accounting + OR-clause, which keeps the existing `:338` /
  `:420-421` invariants intact.

### Cross-document invariants preserved (Round 3c does NOT relax any)

- **#10 (sliding idle TTL, NOT fixed-lifetime)**: Unchanged. Round 3c
  S6's `OnCredentialDeleted` cooldown clear does NOT touch any
  binding's `boundAt`.
- **#5 (`EngineStats` field names locked)**: Unchanged. Round 3c
  W2's `ErrorType` + `ErrorCode` are appended to `ProviderError`
  (a different struct than `EngineStats`); W1's `arc.retryAfter`
  is appended to `anthropicRequestContext`. Neither alters the
  pinned `EngineStats` shape.
- **C1 (`/v1/messages` internal path in scope)**: Unchanged.
- **E-1 (janitor outer RLock + per-model write locks)**: Unchanged.
  Round 3c S6's cooldown clear uses the SAME outer Lock +
  per-model write Lock discipline.
- **W-3 (constructor-only injection)**: New invariant — the
  Round 3c C3(3) constructor gains `engine` + `conversationKey`
  params but NO `Config.CredEngine` field is exposed. The config
  stays flat; engine + key arrive via constructor args exactly
  as `eventBus` and `requestID` already do today.
- **R3-1..R3-8 SEMANTICS STAND**: every ruling's behavior is
  unchanged; the ten Round 3c items are specification-level
  fixes (gate hoisting, ReselectMode unification, three
  nonexistent references, passthrough classification source,
  ProviderError extension, soonest-expiry single-shot,
  `rate_limit_exceeded` literal, tried-set home pin, S3 wording
  standardization, OnCredentialDeleted cooldown hygiene). No
  R3-1..R3-8 ruling is overridden.