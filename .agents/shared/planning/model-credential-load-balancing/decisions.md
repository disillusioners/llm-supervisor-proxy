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
