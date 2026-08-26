# Architecture Recommendation: Model → Credentials Load Balancing

Date: 2026-08-21
Author: architect (controller) — synthesis of a 2-model governor council (`trade-off-analysis` skill; models `agentic` + `coding`; both completed, both read-only-clean)
Feature branch: `feature/model-credential-load-balancing` (base `latest @ fea5874`)
Status: **Ready for dispatcher review — 2 mandatory amendments, 1 conditional, 1 doc pass**
Scope: DIAGNOSIS/REVIEW ONLY. No planner file was modified; no code was touched.

---

## Verdict Summary

The plan is **architecturally sound**. The schema choice (JSON column over join table), the
`pkg/credentiallb` engine package, the invalidation design, and **every** wiring placement
(`race_executor.go:112/:137`, `handler_internal.go:46`, provider-probe shims, `rc.reset()`,
dispatcher ruling #2 on engine ownership, ultimate-bucket independence) survived independent
code verification by both councilors. The two load-bearing decisions that did **not** survive
are the conversation key's blind spot and the destructive DROP COLUMN. Both are fixable with
contained edits; neither invalidates the phase structure.

**Council consensus (identical verdicts from both models, independently scored):**

| # | Focus Area | Verdict | One-line reason |
|---|------------|---------|-----------------|
| 1 | Conversation identity (`sha256(model_id\|"first_user_msg")`) | **AMEND** | Templated-agent fleets sharing a byte-identical first message collapse onto ONE credential — LB silently broken for exactly the workloads that need spreading |
| 2 | Schema: `models.credentials_json` vs join table | **UPHOLD** | House style verified (zero join tables, JSON-array precedent `store.go:607-615`); reverse-lookup cost unchanged (today's guard already full-scans, `store.go:1289-1295`); TEXT-in-both-dialects sidesteps JSONB portability |
| 3 | Destructive migration (ADD→backfill→DROP COLUMN in 028) | **REVERSE** | Old-binary-vs-migrated-DB is total breakage; down-migration is lossy for multi-credential models; OSS tooling/backups break silently. Go expand-now/contract-later |
| 4 | Engine design (`pkg/credentiallb`) | **UPHOLD** (amend internals) | Package isolation, prefix-sum selector, hybrid TTL+event invalidation, defensive still-in-config check (strongest idea in the contract) all hold; 4 internal amendments |
| 5 | Wiring & runtime | **UPHOLD** (amend details) | All call sites verified in code; 3 detail amendments (event unimplementable as contracted, empty-key ambiguity, constructor-only injection) |
| 6 | Variant selection | **(ii) plan + non-destructive migration** | Identical winner and ranking from both models' independent 5-axis matrices |

---

## 1. Conversation Identity — AMEND (🔴 Critical)

### The blind spot both councilors found independently

`sha256(model_id + "|" + first_user_message)` identifies **conversations** correctly, but it
identifies **identical prompts**, not conversations. Two failure classes:

1. **Templated-agent skew (🔴).** Thousands of conversations sharing a byte-identical first
   message — agent fleets, batch evals, cron prompts, fixed `/command` openers — hash to one
   key. The first weighted pick wins the binding; every subsequent same-key request reuses it.
   The entire templated fleet pins to ONE credential for 24h, silently defeating weighted LB
   for exactly the high-volume workloads that need rate-limit spreading. The plan's E2E
   distribution test uses 100 *unique* first messages, so it cannot detect this.
2. **Cross-user collision (🟡).** The same conversation replayed by different users (shared
   first message) shares one binding — acceptable for prompt-cache affinity (cache hits are
   content-addressed anyway), but it amplifies failure class 1 and leaks affinity *between*
   principals.

### The fix (mandatory) — A-1: principal-salted key

```
key = sha256(modelID + "|" + tokenID + "|" + firstUserMessage)
```

- `rc.tokenID` already exists — `pkg/proxy/handler.go:401` (from `auth.AuthToken`).
- Anonymous/unauthenticated requests degrade to today's behavior (unsalted key).
- Distinguishes the same prompt across principals; reduces (does not eliminate) templated skew
  *within* one principal's fleet — see residual note below.
- **Wiring consequence (adopted from the `agentic` councilor):** `initRequestContext` runs at
  `handler.go:353`, **before** `rc.tokenID` is populated at `:401`. Key computation must move
  post-auth (or compute lazily at first engine call). The `coding` councilor's claim that the
  token is available at `initRequestContext` time is contradicted by the line-level ordering;
  the synthesis adopts the ordering evidence.

### Multimodal fallback — Disagreement 1 (conditional amendment A-2)

Both councilors agree the current plan **violates requirement (1)** for multimodal
conversations: no binding stored ⇒ every request re-rolls the weighted pick ⇒ zero cache
affinity for vision-first workflows.

- **`agentic` (adopted):** hash the canonical JSON of the content (string *or*
  `[]interface{}` array) instead of bailing to `""`. Precedent already exists —
  `hash_cache.go:172-186` walks multimodal content deterministically; ~20 lines. Removes an
  entire no-affinity class and satisfies requirement (1) as stated.
- **`coding` (fallback):** defer to v2 but make the engine contract explicit — multimodal ⇒
  no binding ⇒ per-request re-roll — and document it in `doc.go`.

**Recommendation: adopt A-2 (multimodal hashing) in v1.** Requirement (1) is a stated user
requirement, not a nice-to-have; the fix is grounded in verified code precedent. If schedule
pressure is real, the fallback is acceptable **only** with the mandatory doc.go contract note.

### Residual accepted risk (document, don't fix)

Within a *single principal's* templated fleet (same token + same first message), the salted
key still collides. This is now bounded by principal rather than global, and content-identical
conversations genuinely share upstream cache benefit — a defensible trade. Operators running
large same-principal templated fleets should be told (docs) that weight distribution assumes
conversation diversity.

---

## 2. Schema — UPHOLD (JSON column over join table)

Unanimous. Evidence:

- **House style verified:** `fallback_chain_json`, `truncate_params_json`,
  `auth_tokens.allowed_models` are all JSON-array columns on the parent row
  (`pkg/store/database/store.go:607-615`); the repo has **zero** join tables; `credential_id`
  already has no DB-enforced FK — integrity is app-level.
- **Reverse-lookup cost unchanged:** the credential-in-use guard (`store.go:1289-1295`)
  already full-scans the models list and **never used the index being dropped**. After the
  swap it scans + JSON-parses; refs ≤ 16 per model — negligible. (🟢 optional: Postgres
  expression index on `credentials_json` if reverse lookups ever matter.)
- **Portability:** TEXT in both dialects sidesteps the JSONB-vs-TEXT divergence entirely; the
  backfill uses string concatenation for byte-parity.
- **sqlc:** regen is proportionate — the generated `db/` package is currently an orphan; only
  `schema.sql` + regen needed.
- **Bonus correctness:** the same-provider invariant makes every existing `CredentialID`
  provider probe (ultimate-external, anthropic passthrough, MiniMax race check) correct as
  `PrimaryCredentialID()` shims, since all credentials in a list share one provider.

**The join-table recommendation in the original feature request stays overruled.** The
planner's reasoning holds: no FK discipline exists to leverage, and a join table would be the
repo's first, in tension with every precedent.

---

## 3. Destructive Migration — REVERSE the DROP COLUMN (🔴 Critical)

### Why the DROP must leave migration 028

1. **Old binary vs migrated DB = total breakage.** The current binary reads `credential_id`
   in `scanModels` and the `QueryBuilder`. Rolling back the binary without running the
   down-migration crashes model resolution. And the down-migration is **lossy** for
   multi-credential models (only `credentials[0]` survives) — rollback destroys operator
   config.
2. **OSS external surface.** Backup scripts and third-party tooling may `SELECT
   models.credential_id`. Silent breakage on upgrade is the worst failure mode for a
   self-hosted OSS project — no telemetry, no error until a script fails.
3. **Empirical gap in the atomicity claim.** The `agentic` councilor caught a factual error in
   `plan-overview.md`'s research: Postgres migration 024 does **not** use file-level
   `BEGIN`/`COMMIT` (its only `BEGIN` is inside PL/pgSQL). **028 would be the repo's first
   file-level transactional migration** — unproven through this runner + driver pairing.
   Keep the transaction; make the PG-gated 028 test **mandatory**, not best-effort.

### The fix (mandatory) — M-1: expand-now / contract-later

- **028 (amended):** ADD `credentials_json` + backfill + **keep** `credential_id` as a
  **derived shadow**: `credential_id = credentials[0].credential_id`, written in the same
  UPDATE statement. Application reads only `Credentials`; the shadow write is ~3 lines in the
  write path.
- **029+ (deferred):** `DROP INDEX idx_models_credential_id` + `DROP COLUMN credential_id`,
  gated on a release-note deprecation window ("credential_id is derived; will be removed in
  the next minor"). Schedule 029 as a tracked issue at merge time.
- **Result:** old binary degrades gracefully (loses LB, keeps serving single-credential
  models — no crash); external tooling keeps working during the window; the down-migration
  becomes lossless (`credential_id = json_extract(credentials_json, '$[0].credential_id')`
  — verified round-trip).

### Answering the plan's dual-source objection

`decisions.md` §B argues against two **independently writable** sources requiring perpetual
sync. M-1 is **one source + one derived shadow** — divergence requires an out-of-band writer
(the same trust boundary the design already accepts for app-level FK integrity). The
objection's force is reduced from "perpetual bug source" to "deprecation-window discipline".

### The condition that carries the recommendation

The non-destructive variant is **conditional on a real roadmap to the 029 cleanup**. A
two-week window is fine; a two-year one silently recreates the dual-source problem the
planner was avoiding. The tracked issue is load-bearing, not a footnote.

---

## 4. Engine Design — UPHOLD architecture, amend internals

**Upheld (both councilors):**
- Package isolation (`pkg/credentiallb` imports stdlib + `pkg/models` only).
- Prefix-sum + binary-search selector (k ≤ 16; integer math; seedable).
- Hybrid TTL (24h lazy) + janitor (5m) + event invalidation + **defensive still-in-config
  lookup check** — the strongest idea in the contract; it compensates for the event bus's
  drop-on-full semantics (`pkg/events/bus.go`) and makes every missed event self-heal within
  one request.
- Explicit RWMutex over `sync.Map` (house style; access pattern is wrong for `sync.Map`).
- Zero-edits-to-`pkg/proxy` in Phase 2 (seam discipline verified).
- **Restart-persistence deferral is architecturally sound**: a restart costs one re-pick per
  active conversation against a 24h TTL — a cache-miss burst, not a correctness break. A
  bindings table would be the repo's first join table (contradicting §B) for marginal
  benefit. Cross-instance affinity belongs to the v2 Redis direction.

**Amendments:**

| # | Amendment | Why |
|---|-----------|-----|
| E-1 | **Janitor: outer RLock + per-model write locks** (one at a time), not outer write lock | The contract self-contradicts: §Locking Edge Cases claims the sweep "never blocks reads for more than one model," but the locking table has the janitor take the outer **write** lock — stalling **all** reads each sweep. Fix the contract; the outer-RLock variant preserves the claimed behavior. |
| E-2 | **`OnModelChanged` filters, not clears** — preserve bindings whose credential survives the new list; drop only orphans | As contracted, an operator nudging a weight 1→2 flushes cache affinity for **every active conversation** on that model. Cache-preserving filtering is strictly better and no harder to implement. |
| E-3 | **Single-credential fast path: no map writes.** Rule in favor of `decisions.md` §E #7 + success criterion #5 over `phase2-plan.md` Task 4's "write-once binding" | The two docs directly contradict; an implementer following phase2 will fail tests written from decisions.md. No-map-writes is also the measurably cheaper path (test-assertable via internal map size). |
| E-4 | Per-model RNG (or `math/rand/v2`); `Engine.Stats()`; janitor panic-recovery test | A shared `rand.Rand` + mutex serializes first-selections across all models; per-model RNG removes the contention. Stats enable operators to see binding counts/hit-rates. Janitor panic-recovery test covers the silent-stop leak risk (contract risk #8). |

---

## 5. Wiring & Runtime — UPHOLD placements, amend details

**Verified in code (by at least one councilor each):**
- `pkg/proxy/race_executor.go:112` (secondary) and `:137` (primary) — correct replacement sites;
  the secondary path correctly reuses the primary's credential (one engine call).
- `pkg/ultimatemodel/handler_internal.go:46` — correct ultimate-internal site.
- Provider probes (`handler.go:287-293`, `handler_anthropic.go:294-295`,
  `race_executor.go:55-58`) — correct `PrimaryCredentialID()` shim targets.
- `rc.reset()` (`handler_helpers.go:113-132`) — `conversationKey` must be added to the reset
  list (plan already says so; verified consistent).
- `NewHandler` is currently 6 params (`main.go:107`); the 7th param is additive and nil-safe.
- **Dispatcher ruling #2 (ModelsManager owns the engine; `main.go` must NOT construct a
  second engine) is correct and important** — two independent binding stores would split-brain
  conversation affinity. Keep the ruling; enforce in review.
- **Ultimate independence (F) holds**: `(ultimateModelID, convKey)` is its own bucket; with
  A-1's token salt it remains correct under per-token ultimate overrides.

**Detail amendments:**

| # | Amendment | Why |
|---|-----------|-----|
| W-1 | **`GetOrSelect` must return `(credentialID, newlyBound, err)`** — or the only-on-first-binding event requirement must be dropped | As contracted, `model_credential_selected` "only on first binding" is **unimplementable**: the signature carries no new-binding signal, and phase3's per-request bool detects first-selection-*within-a-request*, not first-binding-ever. Extend the signature (preferred — it also enables E-4 stats) or drop the requirement. |
| W-2 | **Pin empty-key semantics to: no binding stored, fresh weighted pick per request.** Make the phase2 test assert it | The three docs specify empty-`conversationKey` three different ways; the technical-analysis reading (empty string = its own bucket) creates a **silent 24h hotspot with zero cache benefit**. `decisions.md` §A's reading is the correct one; codify it everywhere. |
| W-3 | **Constructor injection only** — drop the contract's `Config.CredEngine` field | The contract adds a `Config` field *and* the 7th constructor param; phase3 says constructor-only. Two injection paths = two ways to disagree. Take phase3's. |
| — | Pre-existing `config.updated` breakage (🟡): `config.go:518` publishes a struct; `handler.go:155` asserts `map[string]interface{}` and reads `data["field"]` ⇒ the assertion always fails ⇒ ultimate hash-cache reset on config change is **dead code** | Confirmed by both councilors. Pre-existing, non-regressing, out of scope — but it deserves its own tracked issue rather than silence. The new typed `model.credentials.changed` event is the right pattern and does not regress it. |

---

## 6. Five-Axis Variant Comparison

Both models scored independently; identical winner and ranking.

| Approach | Complexity (20%) | Scalability (20%) | Maintainability (25%) | Risk (20%, inv.) | Cost (15%, inv.) | agentic total | coding total |
|----------|------------------|-------------------|-----------------------|-------------------|-------------------|---------------|--------------|
| (i) Plan as-is | 4 / 3 | 3 / 4 | 3 / 3 | 2 / 2 | 5 / 4 | 3.30 | 3.15 |
| **(ii) + non-destructive migration (M-1)** | 3 / 2 | 3 / 4 | 4 / 4 | **4 / 4** | 4 / 3 | **3.60** ✅ | **3.45** ✅ |
| (iii) + restart-persistent bindings | 2 / 2 | 4 / 3 | 2 / 3 | 2 / 4 | 2 / 2 | 2.40 | 2.85 |

*(cells: agentic / coding; Risk and Cost inverted so higher = safer/cheaper)*

### Recommendation

**Option (ii) — plan + non-destructive migration (M-1), paired with A-1 (token-salted,
post-auth conversation key).**

Option (iii) is dominated: its only benefit — surviving a restart against a 24h TTL — is
marginal, and cross-instance affinity belongs to the v2 Redis direction, not a SQL bindings
table. Option (i) loses on inverted Risk (2 vs 4 from both models): the DROP COLUMN converts a
recoverable change into an irreversible one for every external consumer of the column.

**Confidence: Medium-High.** The `coding` councilor showed the recommendation is robust
across the decision tree — (ii) wins on every combination of accepting/rejecting A-1 and M-1
individually.

**Flips to (i) if all of:** no external reader of `models.credential_id` exists; deployments
are single-operator + backup-disciplined; binary rollbacks never happen.
**Degrades below (i) if:** the 029 cleanup migration is never scheduled (dual-source debt
reintroduced). **Flips toward (iii) only if:** multi-instance shared affinity becomes
near-term.

---

## 7. Consolidated Amendment List (dispatcher-facing)

### Mandatory (block implementation start)

| # | Change | Lands in |
|---|--------|----------|
| A-1 | Key = `sha256(modelID + "\|" + tokenID + "\|" + firstUserMessage)`; computed **post-auth** (after `rc.tokenID` at `handler.go:401`, not inside `initRequestContext` at `:353`) | decisions.md §A, technical-analysis key.go, phase3 |
| M-1 | 028 = ADD + backfill + keep `credential_id` as **derived shadow** (same-statement write); DROP INDEX/DROP COLUMN deferred to 029+ with deprecation window; 029 scheduled as tracked issue at merge | decisions.md §B, phase1 Task 1 |
| E-3 | Fast path: **no map writes** (rule in favor of decisions.md §E #7 / success criterion #5) | phase2 Task 4 |
| W-1 | `GetOrSelect` → `(credentialID, newlyBound, err)` — or drop the only-on-first-binding event requirement | technical-analysis API, phase3 |
| W-2 | Pin empty-key semantics: no binding stored, fresh pick per request; phase2 test asserts it | all three docs |

### Conditional (recommended, schedule-permitting)

| # | Change | Lands in |
|---|--------|----------|
| A-2 | Multimodal: hash canonical content (string *or* array) — precedent `hash_cache.go:172-186`. **Fallback if deferred:** explicit "no affinity for multimodal v1" contract note in `doc.go` (mandatory, not optional) | decisions.md §A, key.go |
| E-1 | Janitor: outer RLock + per-model write locks (fix contract contradiction) | technical-analysis §Locking |
| E-2 | `OnModelChanged`: filter-survivors, don't clear-all | pkg/credentiallb contract |

### Doc reconciliation pass (before Phase 1 starts)

1. Key-computation location: decisions.md §A says "in the engine"; contract §Order of
   Operations says `initRequestContext`. A-1 makes it post-auth — update both.
2. Correct plan-overview.md's false claim that PG migration 024 uses file-level
   `BEGIN`/COMMIT` (it does not; 028 is the repo's first).
3. Make the PG-gated 028 transaction test **mandatory** (first file-level transactional
   migration in the repo; empirically unproven through this runner + pgx v5).
4. W-3: constructor-only engine injection; remove `Config.CredEngine` from the contract.
5. E-3: align phase2 Task 4 with decisions.md §E #7 / success criterion #5.

### Tracked issues to file (out of feature scope)

1. **029 cleanup migration** (DROP INDEX + DROP COLUMN `credential_id`) — load-bearing for M-1.
2. **`config.updated` event payload mismatch** (`config.go:518` vs `handler.go:155`) —
   pre-existing dead code in the ultimate hash-cache reset path.

---

## 8. Risks (ordered, consolidated)

### 🔴 Critical
1. **Templated-first-message skew** — same-key conversations pin to one credential; weighted
   LB silently broken for scripted/agent workloads; no plan-doc analysis; E2E test cannot
   detect it. → **Fix: A-1.**
2. **Destructive DROP COLUMN in 028** — lossy rollback for multi-credential models;
   old-binary-vs-migrated-DB total breakage; OSS tooling/backups break silently. → **Fix: M-1.**

### 🟡 Warning
3. Multimodal no-affinity violates requirement (1) — per-request credential churn for
   vision-first conversations. → **Fix: A-2 (or mandatory doc note).**
4. Empty-key semantics specified three ways; the ""-as-own-bucket reading creates a silent
   24h hotspot. → **Fix: W-2.**
5. Janitor locking self-contradiction; outer write lock stalls all reads per sweep. → **Fix: E-1.**
6. `OnModelChanged` fleet-wide cache flush on routine reweighting. → **Fix: E-2.**
7. `model_credential_selected`-only-on-first unimplementable with the contracted signature. → **Fix: W-1.**
8. Fast-path contradiction between phase2 and decisions.md tests. → **Fix: E-3.**
9. In-memory bindings lost on restart → cache-miss burst after deploys (accepted v1; document
   operator-visible behavior).
10. Pre-existing `config.updated` breakage (ultimate hash-cache reset dead code) — needs its
    own tracked issue.

### 🟢 Suggestions
Per-model RNG / `math/rand/v2`; `Engine.Stats()`; janitor panic-recovery test; binding
hit-rate logging; optional Postgres expression index on `credentials_json`; consider 7-day
TTL for long agent loops; weight-maximum validation; `Position` field redundancy (harmless);
builder pattern for `NewHandler` someday.

---

## 9. Key Assumptions

1. External readers of `models.credential_id` may exist (OSS; unknowable) — favors M-1.
2. **029 cleanup gets scheduled** — load-bearing for M-1; without it, dual-source debt returns.
3. Templated/shared-first-message workloads are material for some deployments — favors A-1.
4. Single-instance-per-operator is the v1 norm — validates restart-persistence deferral.
5. Migration 028's file-level transaction works through both drivers (SQLite ≥ 3.35 via
   `modernc.org/sqlite` v1.46.1; pgx/v5 `ExecContext`) — **must be proven by the mandatory
   PG-gated test**, not assumed.

## 10. Unverified Items

- Bundled SQLite engine version inside `modernc.org/sqlite` v1.46.1 (go.mod confirms driver;
  `sqlite_version()` not executed).
- File-level `BEGIN…COMMIT` through pgx v5 multi-statement `ExecContext` — inferred from
  001–027 precedent; hence the mandatory PG-gated test.
- SQLite DROP COLUMN-on-indexed-column restriction — documented behavior, not executed.
- Real-world fraction of multimodal-first and templated-first traffic (no telemetry).
- Whether third-party tooling reads `models.credential_id` (unknowable for OSS).
- Phase-4 frontend citations (`ModelForm.tsx`, `types.ts`) not verified by councilors.
- CI race-test configuration and sqlc regen gating not confirmed.
- `handler_external.go` compile-shim target likely but unverified.
