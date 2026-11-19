# DECISIONS REGISTER — AUTH COMMISSION (Token Permission Model)

**Feature:** `token-authz-allowed-groups`
**Worktree:** `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-token-authz`
**Branch / base:** `feature/token-authz-allowed-groups` @ `a3f0335` — **REAL STATUS (gen-11 re-pin; MEASURED 2026-11-19T07:41:00Z):** branch `feature/token-authz-allowed-groups`; **base `a3f0335`**; **HEAD `1408e8b`** — ALL LANES LANDED (tester FINAL VERDICT PASS). **Chain: 22 commits incl. base (21 on top of base)** — the `1e65774` tip was 21 commits incl. base (20 on top); `1408e8b` (docs(tester): FINAL VERDICT PASS — token-authz gen-10 @ `1e65774`, 121-pin sweep clean) is the 21st commit on top of base. Enumerated:  `a3f0335` (base) → `2b9ab6c` (frozen token-scope contract) → `60d0db4` (Phase-1 completion: 034, admin API, PD pins) → `f9adbce` (empty/whitespace TEXT fail-closed + hermetic authz guard toolchain) → `b0d4875` (FE token-grant wire) → `5de3eb4` (allowed_groups picker + per-axis control) → `7ce1787` (static bundle rebuild) → `391f181` (Phase-2 per-layer enforcement, 4 lanes) → `e7ffcda` (chain/re-entry/per-member pins) → `156252a` (decorator degraded-branch fail-closed) → `1e423de` (C2 invalid flags in DTO + C3 grant-axis escalation) → `63e9665` (C2 BE pin + C3 FE pins) → `ce8f2f5` (W1 stale-claim grant validation) → `0be8d13` (W2 groupPick 403 conjunct) → `4be0a12` (F1 per-axis picker save + F2 DTO comment) → `81e8788` (gofmt drift + log/comment accuracy + guard-test hardening) → `eb137a1` (align stale empty-slice assertions with the conjunctive grant contract) → `135e1e8` (tester: release-gate report) → `f244b1d` (tester: shared-worktree provenance audit + gen-9 re-verification recipe) → `e502978` (tester: tree-cleanliness precondition for the gen-9 recipe) → `1e65774` (**land the 9 commission-named obligation pins** — PD-2/PD-7/PD-9/PD-10/PD-11; 4 test files, +1589). → `1408e8b` (docs(tester): FINAL VERDICT PASS — token-authz gen-10 @ `1e65774` (121-pin sweep clean); `.agents/tester/` only, 3 files, +55/−1 — docs-only, no source change). **Diff vs base (`a3f0335..1408e8b`): 97 files, +11710/−899.** **WORKING TREE CLEAN — no source-file edits pending** — `git status --porcelain` shows ONLY `.agents/` entries (`approver/active.md` M, `tidier/notes.md` M, approver tracking ??, this planning dir ??); **no stray temp test files remain — `pkg/decision/zz_probe_tmp_test.go` is verified ABSENT.** **Anchor convention:** every `file:line` anchor is **base-relative to `a3f0335`** unless marked; the anchor-shift table in `§0.1` gives the net `a3f0335..4be0a12` deltas (the later commits touch test/docs files plus the 4 test files of `1e65774`: `migrations_test.go`, `handler_group_test.go`, `handler_test.go`, `authz_claim_pins_test.go`; `1408e8b` touches `.agents/tester/` only ⇒ zero anchor shift).
**Decision maker:** the commission owner (ratify / overrule)
**Author:** worker via `requirements-analysis` (decision-register application)
**Status:** DRAFT — awaiting owner ruling. **Phase-1 code: COMPLETE (`2b9ab6c`+`60d0db4`).** Every open question below carries a **recommended default**; none is a survey.
**Scope note:** this lane is USER-ORDERED with explicit "ship it; no approval hold". The standing grant's ask-back on auth changes is **DISCHARGED** for this lane. This register does not propose an approval hold.

---

## 0. How to read this register

| Section | Meaning | Your move |
|---|---|---|
| §1 SETTLED BY RULING | Already decided by the Emperor / the commission. Each line cites the ruling. | Do **not** re-litigate. |
| §2 OPEN QUESTIONS | Q1–Q13. Each states the question, the options with their real costs, a **recommended default**, the rationale, and exactly what changes if you overrule. | Ratify or overrule each. |
| §3 RISKS IF NOT DECIDED | Tie-back to Q-ids: what breaks if a question is left open. | Read as urgency. |
| §4 OUT OF SCOPE (flagged only) | Adjacent findings — flagged, **no work planned**. | Note only. |
| §5 DRIFT REPORT + OPEN ITEMS | Evidence corrections vs the brief, and facts I could not verify (pinned by `plan.md`). | Review; plan.md consumes. |

**Evidence base:** re-derived at `a3f0335` in the worktree. Every factual claim below carries `file:line`. Line numbers were re-checked against the worktree tree, not copied. Corrections to the brief's line numbers are listed in §5.

---

## 1. SETTLED BY RULING — do not re-open

| # | Settled decision | Authority (citation) |
|---|---|---|
| **S-1** | Fix `allowed_models` **and** add `allowed_groups`. A group grant reaches **the group and all models in that group**. | Emperor, verbatim: *"we not only need to fix this allowed_models (update) but also the add feature allowed_groups (allow reaching the group and all models in that group)."* |
| **S-2** | **STRICT-EXPLICIT** semantics: a chain entry outside the grant is **EXCLUDED**; chains do **NOT** inherit reach from their parent member. | User-confirmed. |
| **S-3** | **Permission beats resilience**: the failover-narrowing warning is **ACCEPTED**. | User-confirmed. |
| **S-4** | A scoped token whose **entire candidate list** is excluded gets a clean **403 permission failure**, never a fallback failure (never a 502). | User-confirmed. |
| **S-5** | The allow-list **FAIL-OPEN** bug must be fixed to **fail-closed**. | Commission. |
| **S-6** | The FE picker must be **widened** to emit group ids + decision models. | Commission. |
| **S-7** | An **ADR must rule the class semantics**. | Commission. |
| **S-8** | **Wildcard = ABSENT on both axes**: `allowed_models` ABSENT **AND** `allowed_groups` ABSENT (both parsed cleanly) → unrestricted; an explicit `[]` is **PRESENT-empty** and grants nothing. Existing wildcard tokens keep working unchanged. | Commission; Leader Ruling Q13(a) (ADR D3/§7). |
| **S-9** | **Anonymous stays 401.** | Commission. |
| **S-10** | **Lane gates fail closed.** | Commission. |
| **S-11** | **v202 doctrine and the TTL table are untouched.** | Commission. |
| **S-12** | Scope is enforced at **each load-balance / fallback layer**: a model outside the grant is **excluded from the list**. | Emperor, verbatim: *"On each load balance, fallback layer, we need permission check for token and exclude the model from the list."* |
| **S-13** | The known **decision-GROUP scope bypass** must be fixed (the arm returns at `pkg/decision/handler.go:325`, before the allow-list at `:342-354` → zero scope evaluation for a decision-group request). | Commission + live finding (`.agents/tester/RESULTS/2026-11-17-deploy-e2e-decision-groups.md`, 🔴 finding) |
| **S-14** | **No approval hold** in this lane; the ask-back on auth changes is discharged. | Commission. |

**Non-negotiable consequences of S-1 + S-2 + S-8 (derived, not new decisions):**
- The grant axis is **two lists over one id namespace** (`models.id`): `allowed_models` (ids reached directly) and `allowed_groups` (group ids; a group grant expands to the group target **and its current members**).
- The **wildcard is conjunctive**: `allowed_models` absent **AND** `allowed_groups` absent → unrestricted. If either axis is present, the token is **restricted**, and the absent axis grants **nothing** on that axis. (This is the only reading of S-8 under which a migrated narrow token does not silently become a wildcard — see Q2/Q7.)
- A group id that appears in `allowed_models` grants the **group target only** — never its members (S-2: no inherited reach).

---

## 2. OPEN QUESTIONS

### Q1 — DB representation of `allowed_groups`, the exact migration, and the real rollback story

> **SUPERSEDED IN PART (fix-pass 2, C4 + A6 + leader Ruling 6 — recorded 2026-11-18, approver-iteration-001 derived):** the SQL blocks below are the ORIGINAL recommendation and are **superseded BY REFERENCE** — the binding migration bodies are `plan.md` §5.2 **as amended** (PD-2 per-row `DO $$ … EXCEPTION` guard on PG, PD-4 `NULLIF(…, '[]')` on SQLite, and the fix-pass-2 **ATOMICITY MANDATE**: DDL + repartition in ONE transaction per dialect, idempotent on re-run, named recovery path §5.6 item 5 — boot abort ⇒ helm rollback to the v202 anchor). Do not copy the SQL below into an implementation; read §5.2. **D-13 drift row records the divergence.**

**Question.** How is `allowed_groups` persisted (JSON TEXT column vs join table), what is migration `034` exactly, and what is the actual rollback path?

**Options and their real costs.**

| Option | What it is | Real cost |
|---|---|---|
| **(a) JSON TEXT column** — `auth_tokens.allowed_groups TEXT` holding a JSON array of group ids, mirroring `allowed_models` exactly. | One additive column; one `SELECT`/`UPDATE` column change; the existing read path (`auth.ScanAllowedModels`) is reused with a sibling scanner. | No referential integrity: a group id in the list can dangle (group deleted). Cannot answer "which tokens grant group G" without a full table scan + JSON parse (SQLite `json_each` / PG `jsonb`). |
| **(b) Join table** — `auth_token_allowed_groups(token_id, group_id)` with an FK. | Referential integrity; "which tokens grant G" is a cheap index lookup; natural "revoke on group delete" cascade. | A second table to migrate, a second write path, a second read path (a JOIN on every token validation), and a **cache-shape change**: the token cache (`pkg/modelscache/tokens.go:117` `storePositive`) caches a single `*auth.AuthToken`, so a join-table read means N+1 or a batched load per validation. Also breaks the "token SQL lives in `pkg/auth`" boundary. |

**RECOMMENDED DEFAULT: (a) JSON TEXT column**, one additive column on `auth_tokens`, symmetric with `allowed_models` in every respect (same scanner, same serializer, same NULL convention, same cache shape, same interface shape). Dangling group ids are **not** a defect: the request-time resolution treats an unknown group id as granting nothing (fail-closed), and Q4's write-time validation prevents *new* dangling ids.

**Exact migration.** Registry entry appended immediately after `pkg/store/database/migrate.go:82` (the `{"033", …}` line), with its **own sixth "Amendment-1 trap" comment** mirroring the fifth at `:78-82` — the registry is an ordered slice, **not** a directory scanner (`:24` `var migrations = []migration{`, entries carry no `.sql` suffix; loader `pkg/store/database/migrate.go:149` `func (s *Store) loadMigrationSQL(name string)`, sprintf at `:164-165`).

```
// sqlite/034_add_allowed_groups.up.sql
-- Migration 034: token permission model — allowed_groups (group reach)
ALTER TABLE auth_tokens ADD COLUMN allowed_groups TEXT DEFAULT NULL;
-- (SQLite has no ADD COLUMN IF NOT EXISTS — precedent 022_add_allowed_models.up.sql:4-5,
--  030_add_model_kind.up.sql:24-27. The PRAGMA read in 022 is a no-op, NOT a guard.)

-- postgres/034_add_allowed_groups.up.sql
ALTER TABLE auth_tokens ADD COLUMN IF NOT EXISTS allowed_groups TEXT DEFAULT NULL;
```

Plus the **data repartition** (see Q2 for why it is in scope) — dialect-specific, in the same migration:

```
-- SQLite
UPDATE auth_tokens
SET allowed_groups = (SELECT json_group_array(j.value) FROM json_each(auth_tokens.allowed_models) j
                        WHERE j.value IN (SELECT id FROM models WHERE kind = 'group')),
    allowed_models = (SELECT json_group_array(j.value) FROM json_each(auth_tokens.allowed_models) j
                        WHERE j.value NOT IN (SELECT id FROM models WHERE kind = 'group'))
WHERE allowed_models IS NOT NULL AND allowed_models NOT IN ('', '[]')
  AND EXISTS (SELECT 1 FROM json_each(auth_tokens.allowed_models) j
                WHERE j.value IN (SELECT id FROM models WHERE kind = 'group'));

-- Postgres
UPDATE auth_tokens t
SET allowed_groups = (SELECT json_agg(v) FROM jsonb_array_elements_text(t.allowed_models::jsonb) v
                        WHERE v IN (SELECT id FROM models WHERE kind = 'group')),
    allowed_models = (SELECT json_agg(v) FROM jsonb_array_elements_text(t.allowed_models::jsonb) v
                        WHERE v NOT IN (SELECT id FROM models WHERE kind = 'group'))
WHERE t.allowed_models IS NOT NULL AND t.allowed_models::jsonb <> '[]'::jsonb
  AND EXISTS (SELECT 1 FROM jsonb_array_elements_text(t.allowed_models::jsonb) v
                WHERE v IN (SELECT id FROM models WHERE kind = 'group'));
```
(`kind = 'group'` is `models.KindGroup`, `pkg/models/config.go:117`; group ids and model ids share the single `models.id` PK — `pkg/store/database/migrations/sqlite/001_initial.up.sql:19-20`. **CORRECTED (fix-pass 2, C4 — the original claim here was FALSE):** SQLite's `json_group_array` over zero rows returns the STRING `'[]'`, **NOT** NULL; only PG's `json_agg` returns NULL over zero rows. An emptied `allowed_models` therefore needs `NULLIF(json_group_array(...), '[]')` on SQLite (plan.md §5.2 PD-4) to become **NULL (absent)** — see Q7/Q13, this matters.)

**Real rollback story (no hand-waving).**
1. **No down-runner exists in this repo.** Every registry entry ends `.up`; `.down.sql` files are documentation-only operator artifacts (precedent `migrations/sqlite/033_add_credential_groups.down.sql:1-3`: *"No down-runner exists in this repo (025/026/027 have NO down file); rollback = restore backup."*; also `030_add_model_kind.up.sql:24-27`). If an operator runs the down file by hand, the `schema_migrations` row for `034` **must also be deleted** or the runner treats the version as applied (`pkg/store/database/migrate.go:103` `isMigrationApplied`, skip at `:107-109`, `recordMigration` at `:141`).
2. **CORRECTED (fix pass B-final, I3-2): an IMAGE rollback is NOT unconditionally safe — it MUST be preceded by the reverse data repartition.** The column is additive (no SCHEMA rollback ever needed), but the pre-034 binary maps `len(AllowedModels)==0` to **WILDCARD**: running the old image while the repartition is applied promotes every migrated narrow token (e.g. the smoke tokens) to **FULL ACCESS on public `/v1/*`** — the named historical failure mode (ADR §7.1 item 1 MUST; `plan.md` §14.7; `phase3-plan.md` runbook).
3. **CORRECTED (fix pass B-final, I3-2): ORDERING — reverse data repartition FIRST (while the new binary still serves), image rollback SECOND.** The down file carries the reverse statement + the `schema_migrations` row-deletion caveat; the ordering rule is pinned in `plan.md` §14.7 and the `phase3-plan.md` runbook (Tier-1 Step-0 `'[]'` reconcile → Step-1 reverse → Step-2 image rollback).
4. Ultimate fallback remains the pre-migration backup restore.

**Rationale.** (a) is symmetric with a column the codebase already owns end-to-end (`pkg/auth/store.go:57-64` write, `:137`/`:179`/`:246` read) — one mechanism, one place to get fail-closed right, one cache shape, one interface shape. (b) buys referential integrity the permission axis does not need (Q4 validates at write time; request-time resolution fails closed on a dangling id) at the price of a per-validation JOIN on the hot path and a second SQL home for token data (the house boundary is that token SQL lives in `pkg/auth`, `pkg/store/database` holds *no* `auth_tokens` Go code — only migration SQL and a stale `pkg/store/database/sqlc/schema.sql:48-55`).

**If you overrule (choose (b)).** The migration gains a second table + FK + cascade, the token read path gains a JOIN (and the `pkg/modelscache` positive cache — `pkg/modelscache/tokens.go:117` — must cache the joined set or a batch loader), and the interface shape in Q5 becomes materially more complex. I would need to re-cost the plan; the recommendation would revert to (a) unless the join-table's "which tokens grant group G" query is a stated requirement (it is not stated in the commission).

**Plan.md must pin:** the exact `034` file bodies, the registry append, and the SQLite JSON1 probe (see §5 OI-1).

---

### Q2 — Clean break vs compat for existing tokens (the live narrow tokens that name group ids), and whether a pre-ship data migration is required

**Question.** Under STRICT-EXPLICIT (S-2), a group id in `allowed_models` grants **the group target only, never its members**. The live inventory shows exactly that shape:

- **PRIMARY (5 tokens):** `smoke-primary-agentic` → `[agentic-group]`; `smoke-primary` → `[coding-group, quick-group]`; **wildcard** `Ha`, `tuyen`, `kha`.
- **DAODUC (6 tokens):** the same, plus `smoke-15m`.

A clean break therefore **breaks 2 tokens per host**: `smoke-primary-agentic` and `smoke-primary` would request their groups, reach the group target, find **every member outside the grant**, and get a **403** (S-4) instead of a served group.

**Options and their real costs.**

| Option | Behaviour of the 2 live narrow tokens | Real cost |
|---|---|---|
| **(a) Clean break, no data migration.** | **403 on their groups.** The operator must re-create the grants (move group ids into `allowed_groups`) by hand on both hosts. | Operator action required on a live lane; the smoke tokens break between deploy and the fix; any token created *outside* the FE with a group id in `allowed_models` (external consumers) breaks silently with a 403 that names the group. |
| **(b) Clean break + pre-ship data migration inside `034`.** Repartition every existing row: group-kind ids move from `allowed_models` → `allowed_groups`; `allowed_models` becomes NULL (absent). | **Behaviour preserved exactly** (see proof below). The code has **one** semantic path from day one — no compat branch. | The migration **writes live token permission rows** at deploy time. Per the standing grant's *"live-store CONFIG mutations (models/credentials/tokens rows) — code fixes fine, store writes not"* clause, this needs your reading confirmed (it is a code-shipped migration, in the house migration framework — precedent `032_internal_only_data`, a data migration — not an ad-hoc store write). Needs the dialect-specific JSON repartition + the SQLite JSON1 probe (§5 OI-1). |
| **(c) Permanent compat shim.** A group id in `allowed_models` **also** grants its members (the pre-change behaviour). | Preserved. | **Two** semantic paths forever; the STRICT-EXPLICIT rule is violated for the legacy shape only; the "fully-excluded → 403" path (S-4) becomes unreachable for legacy rows; every future reader of the code must know about the exception. Contradicts the ADR's job (S-7). |

**RECOMMENDED DEFAULT: (b)** — clean-break semantics **plus** the pre-ship data repartition inside `034`.

**Proof that (b) preserves the live tokens exactly.** Under the reach model of S-1/S-2/S-8:
`Reach(token) = { ids in allowed_models } ∪ { g ∈ allowed_groups } ∪ { members(g) : g ∈ allowed_groups }`.

- **Before:** `allowed_models = ["coding-group"]`, `allowed_groups` absent ⇒ `Reach = {coding-group}` ⇒ the group target is reachable; its members are reachable **only through the group expansion at request time** (the members were never individually listed). **After the repartition:** `allowed_models = NULL`, `allowed_groups = ["coding-group"]` ⇒ `Reach = {coding-group} ∪ members(coding-group)` ⇒ the group target is reachable **and** its members are reachable through the group ⇒ **identical effective reach**.
- **Wildcard tokens (`Ha`, `tuyen`, `kha`):** both axes absent before and after (the repartition's `WHERE` clause only touches rows that name a group id) ⇒ **unchanged** (S-8 satisfied).
- **The token is NOT accidentally promoted to wildcard:** the wildcard test is conjunctive (both axes absent). A migrated token has `allowed_models` absent **but** `allowed_groups` present ⇒ **not** a wildcard ⇒ the models axis grants nothing, only the group axis grants. This is exactly why Q7's conjunctive rule is load-bearing (see R-3).

**Rationale.** (b) is the only option that satisfies STRICT-EXPLICIT (S-2) *and* keeps the live lane working *and* leaves one code path for the ADR (S-7). (a) buys purity by breaking a live lane; (c) buys compatibility by permanently violating the ruling.

Ruled (b) partly because the two affected tokens per host are the smoke tokens the post-deploy probes themselves use — option (a) would break this ship's own deploy gates.

**If you overrule to (a)** (no data migration): the plan must add an explicit **operator runbook** — "re-create the grants for `smoke-primary-agentic` and `smoke-primary` on both hosts" — and the release checklist must carry it as a **blocking deploy step**; the 403 will otherwise surface as a live-lane failure. **If you overrule to (c)** (compat shim): the ADR must state the exception explicitly, the shim must be a single named predicate (not spread across the four scope sites), and S-4's 403 path becomes legacy-only.

**Plan.md must pin:** the exact repartition SQL per dialect, the SQLite JSON1 probe, the reversal statement in the down file, and the "group ids in `allowed_models` are accepted on write but grant the target only" rule (Q4).

---

### Q3 — What may `allowed_groups` carry: group ids only, or also decision models?

**Question.** Is `allowed_groups` a *group-id* list, or a second place to name models (including decision models)?

**RECOMMENDED DEFAULT: group ids only.** `allowed_groups` accepts **only** ids of `models` rows with `kind = 'group'`, of **any lane** (chat-lane groups **and** decision-lane groups — `pkg/models/group_lane.go:36-43` `GroupLaneChat` / `GroupLaneDecision`). Decision **models** (`kind = 'decision'`, `pkg/models/config.go:106`) are ordinary models and live in `allowed_models`, like chat and image-gen models.

**Options and their real costs.**

| Option | Consequence |
|---|---|
| **(a) Group ids only** (recommended). | One id namespace per list; the write-time kind rule is simple and checkable; the reach expansion is well defined. Cost: an operator who wants "this decision model" must use `allowed_models` — one more concept for the FE to teach (but the FE picker already distinguishes group rows by `kind`, so the split is mechanical — see Q11). |
| **(b) `allowed_groups` accepts models too** (a second list of model ids). | The two lists overlap ⇒ the reach computation becomes order-sensitive and ambiguous ("granted twice, differently"). The kind rule disappears; Q4's validation becomes "does this id exist" only. The STRICT-EXPLICIT story ("allowed_models = direct ids, allowed_groups = group reach") is lost. |

**Rationale.** The Emperor's own wording separates the two concepts (*"fix this allowed_models"* vs *"add feature allowed_groups (allow reaching the group and all models in that group)"*): `allowed_groups` is a **group-reach** mechanism; the expansion rule ("and all models in that group") is what makes it different from a model list. Allowing models in `allowed_groups` would make the two lists redundant and the reach rule ambiguous.

**If you overrule to (b).** The reach formula becomes `Reach = allowed_models ∪ allowed_groups ∪ ⋃members(g)` with no kind distinction — the ADR's class semantics (S-7) get blurrier, Q4's kind validation disappears, and the FE split (Q11) becomes a straight pass-through of one list into two wire fields. I would need to re-cost the FE.

---

### Q4 — Must `POST`/`PATCH` validate that `allowed_groups` ids exist and are `kind='group'`? And `allowed_models` for parity?

**Question.** Today there is **zero validation** of `allowed_models` anywhere: create (`pkg/ui/server.go:2681-2686`) and PATCH (`:2589-2599`) pass the list straight through; the store only marshals it (`pkg/auth/store.go:54-65`, `:252-261`). No existence check, no kind check, no cap, no dedupe.

**Options and their real costs.**

| Option | Behaviour | Real cost |
|---|---|---|
| **(a) Validate `allowed_groups` strictly (existence **and** `kind='group'`), leave `allowed_models` unvalidated.** | A typo'd group id is a **400** at write time instead of a silent no-op grant. | Asymmetric with `allowed_models`; a token whose group was **deleted** still carries a dangling grant (request-time fail-closed, never a crash). |
| **(b) Validate both, existence-only.** | Uniform. | A token naming a **deleted model** can no longer be saved with a full-list PATCH (the FE re-sends the whole list) — the operator is **wedged** out of editing that token until they remove the stale id. Mitigable with a precise 400 message + an FE "stale id" marker. |
| **(c) Validate both, existence + kind (`allowed_models` may not name a group).** | Strictest invariant. | **Breaks the legacy shape at the API** (today's live narrow tokens name groups in `allowed_models`); contradicts the Q2 recommendation to *accept* a group id there as a target-only grant; a breaking API change for external consumers. |

**RECOMMENDED DEFAULT: (a) for `allowed_groups` (strict: existence + `kind='group'`), plus (b)'s *existence-only* rule for `allowed_models`, with the wedge handled by an explicit message.** Concretely:

- `allowed_groups`: every inbound id must **exist** as a `models` row **and** have `kind = 'group'`; otherwise **400** with the offending id(s) named. The FE picker can only emit existing group ids, so this path is only reachable via a direct API call.
- `allowed_models`: every inbound id must **exist** (any kind). A **group id is accepted** here and grants the **group target only** (S-2, no inherited reach). This is the Q2 compat shape at the API level.
- **Validation runs on the INBOUND set only, never on the merged result.** This is the rule that prevents the wedge: a token whose group or model was deleted can still be PATCHed (omitting the deleted axis ⇒ "keep existing"), and the operator only trips the 400 when they actively re-submit a stale id.
- **The 400 body** must follow the house envelope for `/fe/api/*` (bare `{"error":"<string>"}` — see the Project Core blueprint, "Cross-cutting contracts").

**Rationale.** The asymmetry is justified by the **failure asymmetry**: a bad id in `allowed_groups` silently grants **nothing** where the operator believes they granted a whole group (security-shaped surprise); a bad id in `allowed_models` grants nothing where the operator believes they granted one model (same shape, lower blast radius). Existence-only on `allowed_models` keeps the two lists symmetric on the check that matters (typos) without inventing a kind rule that would contradict Q2/Q3.

**If you overrule to (c)** (strict kind on `allowed_models`): the Q2 recommendation must change to (a) — no data migration, because the legacy shape becomes illegal at the API — and the live narrow tokens break (R-1). **If you overrule to no validation at all:** a typo'd group id silently grants nothing, the FE shows the grant as active, and the operator debugs a 403 they cannot see — this is the failure mode S-1 exists to prevent.

**Plan.md must pin:** the exact 400 bodies, the validation helper's home (the UI server has the model set: `pkg/ui/server.go:939-1000` `handleModels`, `kindParam` `:948`, all-rows default `:951-952`), and the inbound-vs-merged rule.

---

### Q5 — `TokenStoreInterface` shape, and its 9-mock + decorator blast radius

> **SUPERSESSION (fix-pass 2, A4 — recorded 2026-11-18):** the ONE write-payload type is **`auth.OptionalAllowlist{Set bool; Values *[]string}`** (per axis), carried by `auth.OptionalTokenPermission` — **`auth.TokenPermission` is RETIRED as the write type** (the plain two-slice struct and the "UI merge" wording are superseded). `Set:false` = the axis is untouched (column omitted — PD-3); `Set:true+Values=&[]` = `'[]'`; `Set:true+Values=&ids` = array; `Set:true+Values:nil` = NULL (store-level only, unreachable from the PATCH wire under Ruling 2). The LANDED interim `auth.TokenPermission` nil-skip payload is behaviorally identical (nil ≡ Set:false) and is renamed in the completion commit. Everything else in Q5 (one struct, 11 implementers, one atomic commit, decorator forward + decorator-through test) stands.

**Question.** `auth.TokenStoreInterface` (`pkg/auth/store.go:21`, methods `:22-27`) today has `CreateToken(ctx, name, expiresAt, createdBy, allowedModels []string)` (`:23`) and `UpdateTokenPermission(ctx, id, allowedModels []string)` (`:27`). How does `allowed_groups` enter?

**Implementers that must move (verified):** `pkg/auth/store.go` (real store), `pkg/modelscache/tokens.go:297` (decorator — `CachedTokenStore` implements the interface), and **9 test mocks**: `pkg/decision/handler_test.go:296`/`:341`, `pkg/imggen/handler_test.go:175`/`:220`, `pkg/mcp/auth_test.go:29`/`:45`, `pkg/modelscache/tokens_test.go:57`/`:104`, `pkg/proxy/authenticate_test.go:47`/`:67`/`:348`/`:364`, `pkg/proxy/handler_anthropic_tokenid_parity_test.go:230`/`:240`, `pkg/proxy/handler_test.go:2105`/`:2121`, `pkg/ui/handlers_imggen_test.go:740`/`:764`, `pkg/ui/handlers_token_test.go:35`/`:66`.

**Options and their real costs.**

| Option | Blast radius | Real cost |
|---|---|---|
| **(a) Extend the two existing methods with a permission value** (e.g. `perm auth.TokenPermission{AllowedModels, AllowedGroups []string}` — **superseded fix-pass 2 A4: the ONE write-payload name is `auth.OptionalTokenPermission`/`OptionalAllowlist`; see the Q5 supersession line above**). | All 11 implementers change — **compiler-enforced** (a missed mock is a build break, not a silent gap). | A mechanical diff across 6 packages. **Must land as one atomic commit** (the interface + every mock + the decorator). |
| **(b) Add a new method** (`SetAllowedGroups(ctx, id, groups)`). | All 11 implementers must add the method (same blast radius) **plus** a second write path. | **Non-atomic permission write**: create-then-grant ⇒ a window where the token exists with no group grant. Two-step security writes are a smell; a crash between the two calls leaves a token with weaker permissions. Rejected on that ground, not on size. |
| **(c) Optional-interface forward** (a separate `auth.GroupGrantStore` interface, used via type assertion). | The interface does not grow; the decorator forwards it (house rule: a new optional method needs a decorator forward + a decorator-through test — precedent `pkg/modelscache/models.go:1321-1341` `ClearGroupMemberCooldown` + `pkg/proxy/win_clear_winner_identity_test.go:114-190`). | **Silent non-implementation**: the mocks do not implement it ⇒ the UI's group-permission path is exercised only against the real store ⇒ **vacuous tests** (the anti-vacuous gate at `.agents/tester/PACKS.md:15`/`:18`/`:20` cannot catch a missing implementation). The UI needs a runtime "not supported" branch — a second behaviour path. Plus the same non-atomicity as (b). |

**RECOMMENDED DEFAULT: (a)** — extend the two existing methods with a **single struct parameter** carrying both axes.

- `CreateToken(ctx, name, expiresAt, createdBy string, perm auth.OptionalTokenPermission)` and `UpdateTokenPermission(ctx, id string, perm auth.OptionalTokenPermission)` (fix-pass 2 A4 one-name rule; was `auth.TokenPermission`).
- **One struct, not two adjacent `[]string` parameters.** `CreateToken(ctx, name, expiresAt, createdBy, models, groups []string)` compiles if the two slices are swapped — a silent, security-shaped inversion. The struct makes the swap impossible and is future-proof (a third axis needs no signature change).
- The struct carries **concrete** values (nil = the axis is absent, empty-non-nil = the axis is explicitly empty — Q7/Q13), so the **"keep vs replace" merge stays in the UI** where it lives today (`pkg/ui/server.go:2589-2597`), and no new read-modify-write is introduced inside the store.
- **Decorator rule honoured:** `pkg/modelscache/tokens.go` `CreateToken` (`:263`, write-through) and `UpdateTokenPermission` (`:297`, delegate-then-drop-the-cached-entry — the entry drop is what stops a stale positive from masking a permission change) must forward the new payload **and** still drop the cache entry on a permission change. A **decorator-through test** is mandatory (house rule, precedent above).
- The interface change lands as **one atomic commit** with all 9 mocks — the compiler enumerates the sites for us, which is the point of choosing (a) over (c).

**If you overrule to (c)** (optional interface): the plan must add an explicit "store does not support group grants" branch in the UI **and** an explicit test that the mock DOES implement it (otherwise the FE path is vacuous) — I would also re-open Q4 (validation) because a mock without the method would silently accept unvalidated group ids.

---

### Q6 — Wire payload shape and backwards compatibility

**Question.** What goes on the wire for `POST /fe/api/tokens`, `PATCH /fe/api/tokens/{id}`, and the token list/create responses, and does it break existing consumers?

**Current contract (verified).**
- Routes: `pkg/ui/server.go:227` `/fe/api/tokens` (GET list `:2519`, POST create `:2521`; handler `:2514`), `:228` `/fe/api/tokens/` (DELETE, PATCH `:2554-2555`; handler `:2528`; id trim `:2530`). **No PUT.** GET on `/fe/api/tokens/{id}` is **not routed** (405 at `:2557-2559`) ⇒ there is no per-token GET to extend.
- `CreateTokenRequest` `:2501-2505`: `name` (required, 400 at `:2658-2661`), `expires_at` *string RFC3339 (400 `:2665-2672`), `allowed_models` *[]string (nil/empty = all allowed). `created_by` from `X-User`, default `"api"` (`:2674-2678`). **POST has no body cap** (contrast PATCH's 64 KB `MaxBytesReader` at `:2570`).
- `UpdateTokenPermissionRequest` `:2563-2565`: `allowed_models` *[]string — **nil = keep existing**, non-nil (incl. `[]`) = replace. The merge is at `:2589-2597`.
- `TokenResponse` `:2491-2498` (id, name, expires_at omitempty, created_at, created_by, allowed_models omitempty); `CreateTokenResponse` `:2508-2511` adds the one-shot plaintext `token`. LIST projection `:2613-2644` (`:2629-2639`) — **never token values, never hashes**. POST → **201** `:2708`. PATCH → `{"success":true}` `:2609-2610`. DELETE → **204** `:2552`.

**RECOMMENDED DEFAULT.**
- **Field name `allowed_groups`** (snake_case — the wire convention already in use: `allowed_models`, `created_at`, `expires_at`).
- `POST /fe/api/tokens` body: `allowed_groups` `[]string` alongside `allowed_models`. `POST` has **no body cap** today (`:2646-2712` has no `MaxBytesReader`). ~~**add one** in the same commit~~ — **REJECTED (fix-pass 2 scope ruling #3): the planned POST-side 64 KB cap addition is withdrawn**; the PATCH-side cap at `:2570` is landed fact and stays.
- `PATCH /fe/api/tokens/{id}` body: `allowed_groups` `*[]string` with the **same nil = keep / `[]` = replace** semantics as `allowed_models`.
- **Responses**: add `allowed_groups` to `TokenResponse` (omitempty, mirroring `allowed_models` `:2497`) so the FE can round-trip without a second fetch; the LIST projection (`:2629-2639`) must carry it too.
- **Backwards compatibility:**
  - *Old FE → new BE*: the old FE never sends `allowed_groups` ⇒ PATCH ⇒ **nil = keep** ⇒ an old FE editing `allowed_models` does **not** wipe group grants ✓. POST ⇒ nil = absent ⇒ no group grant ✓.
  - *New FE → old BE*: the BE decoder is `json.NewDecoder(r.Body).Decode(&req)` with **no `DisallowUnknownFields`** (verified: the only mention in the repo is a comment at `pkg/decision/openaidecision.go:18`) ⇒ an unknown `allowed_groups` key is silently ignored ⇒ no 400 ✓.
  - *External API consumers*: adding an optional field is additive. The one **behavioural** change is documented under Q13 (an explicit `[]` on the wire now means "grant nothing" instead of "wildcard").
  - No API versioning needed; the FE ships embedded in the binary (`pkg/ui/static`), so FE and BE ship together — no skew window.

**If you overrule.** Any other field name (`allowedGroups`, `groups`) breaks the snake_case convention and the FE's wire convention (`useApi.ts` payload builders) — I would need the rename costed. Omitting `allowed_groups` from the **response** would force the FE to keep a local shadow copy of the grants — worse, and it makes the FE's badge (Q11) unable to distinguish "absent" from "empty".

---

### Q7 — Fail-closed representation: how a malformed row is distinguished from a wildcard

**Question.** The root cause of the fail-open bug: `ScanAllowedModels` logs *"SECURITY: malformed allowed_models JSON for token, denying all models"* (`pkg/auth/token.go:64`) and returns `[]string{}` (`:65`), but `IsModelAllowed` treats `len(AllowedModels) == 0` as **wildcard** (`:116`) ⇒ **a corrupt row grants FULL ACCESS**. The log says deny; the code grants everything. How is "malformed" distinguished from "wildcard"?

**The three states that must be representable (per axis).**

| DB state | Meaning | Must map to |
|---|---|---|
| `NULL` (column absent) | *No list on this axis.* | **Absent** — contributes nothing to the reach set; combined with the conjunctive wildcard rule, "absent on both axes" = **wildcard**. |
| valid JSON array (possibly `[]`) | *An explicit list on this axis.* | **Present** — `[]` = grant nothing on this axis; non-empty = grant exactly those ids. |
| non-NULL, unparseable | *Corrupt.* | **Invalid** — **deny everything on that axis** (fail-closed, S-5). |

**RECOMMENDED DEFAULT.**

1. **Per-axis tri-state**, carried on the token struct as an explicit flag so it survives the token cache: e.g. `AuthToken{AllowedModels []string, AllowedGroups []string, PermissionInvalid bool}` — or two per-axis flags if the axes must fail independently. **The flag must live on the struct**, not be returned separately: the decorator caches the token object (`pkg/modelscache/tokens.go:117` `storePositive` stores `*auth.AuthToken`), so a separately-returned flag would be dropped at the cache boundary and the fail-closed signal would be **lost in production** (CACHE_LAYER_ENABLED defaults **ON** ⇒ production is bound to the decorator).
2. **The scanner must return the tri-state, not a bare slice.** `ScanAllowedModels` currently returns `[]string` with `nil` for NULL/empty and `[]string{}` for malformed — the conflation. Either the scanner returns `(ids []string, invalid bool)` or a dedicated `ScanPermission` type is introduced. **The nil-vs-empty invariant must be documented on the struct**: `nil` = absent, `non-nil` (even empty) = present.
3. **The wildcard rule is CONJUNCTIVE:** `wildcard = modelsAbsent && groupsAbsent`. A token with `allowed_models` absent but `allowed_groups` present is **restricted** (the models axis grants nothing). A token with either axis **invalid** is **not** a wildcard and is denied on that axis.
4. **Keep the existing "len == 0 ⇒ allow" convention** for the *wildcard* case only. Changing `IsModelAllowed`'s empty-slice meaning to "deny" would silently flip the behaviour of every mock that constructs `auth.AuthToken{AllowedModels: []string{}}` (and any row whose stored list is literally `[]`), which is a much larger blast than adding a flag. With the flag in place, the *only* producer of an empty-non-nil list is a malformed read (or an explicit `[]` write — Q13), and both are handled deliberately.

**Why the conjunctive rule is load-bearing (not cosmetic).** Under the Q2(b) data repartition, a narrow token ends with `allowed_models = NULL` + `allowed_groups = ["coding-group"]`. A **per-axis** wildcard rule ("absent on the models axis ⇒ wildcard on the models axis") would turn that migrated token into a **model-level wildcard** — a privilege escalation introduced *by the fix*. The conjunctive rule is what makes the repartition behaviour-preserving.

**Also required:** the `AuthToken.AllowedModels` field comment (`pkg/auth/token.go:33`, currently *"nil/empty = all models allowed"*) is the invariant being changed and **must be rewritten**; and the sibling `pkg/imggen/handler.go:264` and `pkg/decision/handler.go:340` comments that state *"Empty AllowedModels = all models allowed"* must be corrected in the same change, or the next reader inherits the wrong rule.

**If you overrule to a struct-based permission object** (`type TokenPermission struct{ Wildcard bool; Models, Groups []string; Invalid bool }` replacing the two slices on `AuthToken` — a HYPOTHETICAL overrule alternative, NOT the frozen write payload; the frozen type is `auth.OptionalTokenPermission`/`OptionalAllowlist`, A4): that is **cleaner but wider** — every `auth.AuthToken{AllowedModels: …}` literal in the 9 mocks and the 4 scope sites changes, and the FE/DTO mapping changes. It is the better end-state if you want the permission model to be one value rather than two slices plus flags. If you overrule to a **per-axis wildcard** (each axis independent): the Q2(b) repartition is **unsafe** and must be replaced by Q2(c) (compat shim), because a migrated token would become a model-level wildcard.

**Plan.md must pin:** the exact struct/flag names, the scanner signature, and the helper that computes "wildcard" (one function, called by all four scope sites — never re-derived per site).

---

### Q8 — Fully-excluded candidate list: exact status, body, and envelope detail

**Question.** S-4 settles **403, not 502**. What exactly is the status and body, and where is the check performed relative to upstream attempts?

**RECOMMENDED DEFAULT.**

- **Status 403**, with the **existing envelope byte-identical** — no new fields:
  `{"error":{"type":"access_denied","message":"model not allowed for this token","model":"<id>"}}`
  (hand-rolled `map[string]string`, **no `code` field**; verified identical across the three writers: `pkg/decision/handler.go:784-794`, `pkg/proxy/handler.go:525-535`, `pkg/imggen/handler.go:713-723` — the decision writer assigns the encode to `_`, the other two do not: a 1-token source difference, not a wire difference).
- **`model` = the client-requested id** (the group id in the group case), not an excluded member id. The client asked for the group; the error must name what it asked for. The excluded member ids go to the **log** and the **events bus** (the existing vocabulary already has `model_not_allowed` with a `model` field — `pkg/proxy/handler.go:851-856`).
- **The check runs BEFORE any upstream attempt** — no paid call, no attempt bookkeeping, no cooldown mutation. In the chat lane that means after the group rewrite and before the race chain is seeded; in the decision lane it means before `buildGroupPolicy` (`pkg/decision/group.go:118`).
- The decision lane's existing **precedence is preserved**: 401 → 404 → 400 `decision_misroute` → 400 group-lane misroute → **403** → 502 (`pkg/decision/handler.go:255`, `:313`, `:334-336`; `pkg/decision/group.go:110-112`; `:351`; and the exhaustion envelope `pkg/decision/group.go:345-358` = 502 `decision_all_members_failed`).
- **The chat-lane group request must not fall into the decision lane's 400 `decision_misroute`**: a chat-kind group with an out-of-scope token is a **403** in the chat lane (the misroute rule is the decision lane's own).

**Options and their real costs.**

| Option | Cost |
|---|---|
| **(a) Reuse the envelope byte-identically** (recommended). | The client cannot machine-distinguish "group denied" from "model denied" — acceptable: both are `access_denied` on the same axis. |
| **(b) Extend the envelope** with a `code` (e.g. `"model_not_allowed"`) and/or an `excluded` array. | A **wire change across three writers** + the FE error rendering + the decision-lane FE; needs an ADR amendment (S-7). Machine-readable group denials are genuinely useful for operators — but they are **not** required by the commission. |

**If you overrule to (b).** The ADR must pin the exact envelope shape, all three writers must change **in the same commit** (drift between them is a client-visible inconsistency), and the FE error surface must be extended (the FE parses `error.message` today). I would also re-open the 401 envelope for symmetry — but note S-9 (anonymous stays 401) forbids changing its **status**, not its body.

---

### Q9 — The MCP lane: in scope, or an explicit non-goal + follow-up?

**Question.** `pkg/mcp/auth.go:48-57` validates the bearer and injects the token into the request context; **nothing downstream reads `AllowedModels`** (grep-verified across `pkg/mcp`: the only occurrences of the context key are `pkg/mcp/auth.go:16` and `:56`; there are **zero** `AllowedModels` references in the package). Does the token-permission model cover the MCP lane?

**RECOMMENDED DEFAULT: explicit NON-GOAL for this commission, with a named follow-up.**

**Rationale.** The MCP lane does not select a model at all — it proxies to a **configured upstream MCP server** (`pkg/mcp/proxy.go:189-240` `ForwardHTTPRequest`; `getUpstreamURL` `:122`; `ConnectUpstream` `:156`), and the model-permission axis has **no MCP surface**. The uncovered axis is a *different* one: **which upstream MCP servers a token may reach**. That is a server-level authorization model (not model-level), it is not described by `allowed_models`/`allowed_groups`, and it is not implied by S-1. Bundling it here would expand the commission into a new permission axis with its own ADR.

**Residual risk (stated, not solved here):** an upstream MCP server's *tool* may itself call an LLM outside the proxy's visibility — that is not a proxy scope defect and is not addressable by a token model in this binary.

**Follow-up to file (not in this plan):** *"MCP server-level authorization: a token-scoped allow-list of upstream MCP server ids, enforced in `pkg/mcp`'s bearer middleware before `ForwardHTTPRequest`."*

**If you overrule (put MCP in scope).** The work is: a new server-grant axis on the token (a third list — Q5/Q6/Q7 all grow), a new enforcement site in `pkg/mcp`'s middleware, a new wire field, and its own FE surface. That is roughly a second commission; I would need to re-scope and re-cost, and the ADR (S-7) would have to rule **two** permission axes.

---

### Q10 — Group-membership dynamism: request-time resolution and the invalidation point

**Question.** A group grant means "the group **and all models in that group**" (S-1). Group membership is mutable (`models.members_json`, `pkg/models/group.go:46` `GroupMember{ModelID, Weight, BackupOnly}`; `MaxGroupMembers = 16`, `pkg/models/group.go:26`). When is the expansion resolved, and what invalidates it?

**RECOMMENDED DEFAULT: resolve the expansion at REQUEST TIME, against the live model set; never bake it into the cached token.**

- **What the token cache may hold:** the **grant lists** (ids only) — `allowed_models`, `allowed_groups`. That is what `pkg/modelscache/tokens.go` caches today (the `*auth.AuthToken` at `:117`).
- **What must never be cached:** the **expanded member set** of a granted group. The positive token cache TTL is **60 s** (`pkg/modelscache/models.go:19` `defaultPositiveTTL`), and the stale-positive tier's hard cap is **24 h** (`pkg/modelscache/models.go:21` `defaultStaleCap`) ⇒ baking the expansion into the cached token would keep a **removed** member reachable for up to 24 h. For a permission axis that is unacceptable.
- **Invalidation point:** none is needed for the expansion — it is recomputed per request from the live model set. The token cache invalidates the **grant lists** on a permission change (the decorator drops the cached entry on `UpdateTokenPermission`, `pkg/modelscache/tokens.go:294-304`) and the TTL bounds grant staleness at 60 s.
- **Operator-facing consequence (must be in the ADR, S-7):** granting a group grants its **future** members as well — a member added to a granted group becomes reachable at the next request, and a member removed from it stops being reachable at the next request. This is the honest reading of *"the group and all models in that group"*.
- **Remaining staleness to be bounded — REWRITTEN (fix-pass 2, leader Ruling 5, A5):** the expansion MUST read an **IN-MEMORY snapshot source** from the existing cache-layer snapshot class (the decorator's `GetModels()` row set / `pkg/decision`'s `currentModelRows()`), **invalidated on group writes** — the approver PROVED `GetModelNative` (`store.go:1095-1145`, forwarded `modelscache/models.go:1301-1303`) is a live SELECT under `m.mu.RLock`, so a per-request reach read through it imports the documented ~90 s reader-stall pattern onto `/v1/*`. **I14 is a HARD REQUIREMENT: zero new per-request DB reads for scope resolution** — no strict-read option, no accepted-risk. The expansion inherits the models-cache TTL (reach staleness == existing model-row cache staleness; no new knob, §plan 17.3 OI-2 stands). Seam picked by fix-pass 2: a per-request in-memory index built once from the decorator's rows (plan.md §4.2, phase2 T2.0).

**If you overrule to a cached expansion** (e.g. resolve the expansion when the token is validated and cache it): you must state the **maximum acceptable reach-staleness** for a removed member. Any answer above ~60 s needs an explicit revocation path (a group-membership change would have to invalidate every token that grants that group — a cross-token index that does not exist today). I do not recommend it.

---

### Q11 — FE picker shape: one picker with a submit-side split, or two labeled pickers?

> **SUPERSEDED (fix pass C, dated notice):** the recommendation below (ONE picker + submit-side split, delete the kind filter) is **superseded by the LANDED design** — TWO per-axis authoritative pickers with group rows EXCLUDED from the models picker (`TokenForm.tsx:84-85`/`:159-181`; pin `TokenForm_PickerExcludesGroupModels`; `grantAxes.tsx` stored-membership-first `axisOfId`). The text below is retained as archaeology; **do NOT implement**.

**Question.** `TokenForm.tsx` currently filters to chat rows only — `const chatModels = models.filter((m) => !m.kind || m.kind === 'chat');` (`:34`) — and emits a **flat** `string[]` of ids (`:45-47`, `onSubmit(name, expiresAt, allowedModels?: string[])` `:12`). `ModelMultiSelect.tsx` emits ids only and carries **no kind** into the payload (`:39-45`). The new wire field `allowed_groups` needs a kind-aware split somewhere. One picker + submit-side split, or two labeled pickers?

**~~RECOMMENDED DEFAULT: ONE picker + a SUBMIT-SIDE SPLIT.~~ (SUPERSEDED — fix pass C dated notice above: the LANDED design is TWO per-axis pickers with groups excluded from the models picker. Retained as archaeology; do NOT implement.)**

**Rationale.** The kind is available exactly where the split must happen: `TokenForm`'s `models` prop **carries `kind`** (`:10` — `kind?: 'chat' | 'image-gen' | 'decision' | 'group'`), and the submit handler is where the payload is built (`:45-47`). `ModelMultiSelect` is reusable **as-is** (it only needs ids) — that is exactly why it takes `{ name, id, [key: string]: any }[]` (`ModelMultiSelect.tsx:4`) and drops the kind. Two labeled pickers would (i) duplicate the dropdown component and its open/search/select-all behaviour (`:37-60`), (ii) split **one** operator decision ("what may this token reach?") into two ("which models" + "which groups"), and (iii) require a second `onSubmit` signature through `SettingsPage.tsx:61`/`:63`/`:370-395`/`:741-756` and `App.tsx:283`/`:285`.

**Concrete shape (for `plan.md`):**
- **Widen the filter** at `TokenForm.tsx:34` to admit **all** rows (chat + image-gen + decision + group) — S-6 requires the picker to emit **group ids + decision models**. With every kind admitted, the kind filter becomes a no-op and should be deleted (keeping a filter that excludes nothing is a trap for the next reader). The legacy chat normalization `!m.kind || m.kind === 'chat'` must still treat a **missing/empty kind as a model, never a group** (a `kind` of `''` is the legacy chat normalization — `pkg/models/config.go:87-95`).
- **Split at submit** (`:45-47`): `selected` ids partitioned by the `models` prop's kind ⇒ `kind === 'group'` → `allowed_groups`; everything else → `allowed_models`.
- **Explicit-empty encoding** (Q13) — the submit must encode the two axes distinctly, and **POST and PATCH differ**: POST with nothing selected ⇒ **both axes absent** (wildcard, S-8); PATCH with an axis cleared ⇒ that axis **explicitly empty** (`[]`), never `null` (which means "keep"). Today's FE builders collapse both: `useApi.ts:902` (create) and `:920` (PATCH) send `null` for an empty selection ⇒ under the new rule the PATCH builder must send `[]` to clear. **This asymmetry must be deliberate and commented**, not accidental.
- **Second surface:** `TokenList.tsx`'s inline editor (`:77-109`, the `ModelMultiSelect` at `:253`). Its prop type declares `models: { name: string; id: string }[]` (`:7`) — **no `kind`** — so the prop type must be widened to carry `kind` (the caller at `SettingsPage.tsx:744` already passes `kind` at runtime). The badge/display (`:117-140`) must become **axis-aware**: "All models" is correct **only** when both axes are absent (wildcard); otherwise show the model list **and** the group list separately.
- **Optimistic merge:** `useApi.ts:927` merges `allowed_models` into the local token state ⇒ it must merge `allowed_groups` too, or the FE will show a stale grant until the next fetch.
- **Types:** `pkg/ui/frontend/src/types.ts:240-249` `ApiToken` gains `allowed_groups: string[] | null` (mirroring `allowed_models` at `:248`).
- **No new endpoint is needed:** `useModels()` (`useApi.ts:278-296`) fetches `/fe/api/models` with **no `?kind=`** ⇒ **all** rows (chat + image-gen + decision + group) are already in the FE's model list. `useGroupModels()` (`useApi.ts:615-700`) exists as a group-only alternative. No backend change for the picker.

**The FE test that goes red — and the exact assertion to flip.** `pkg/ui/frontend/src/components/tokens/__tests__/TokenForm.test.tsx:41-85`: the fixture includes `grp-1` / kind `'group'` at `:49` and the test asserts `not.toContain('Group One')` at **`:84`** (plus `not.toContain('Image Gen One')` `:82` and `not.toContain('Decision One')` `:83`, and the positive `toContain('Chat One')` `:80` / `toContain('Chat Two (no kind)')` `:81`). Widening the filter makes **`:82`, `:83` and `:84`** red; **`:84` is the group assertion** and all three must be flipped. The test's own title (`:41`) says *"excludes image-gen + decision rows"* — it must be renamed to state the new contract, or it will keep asserting the old one in its name.

**If you overrule to two labeled pickers.** The `TokenForm`/`TokenList`/`SettingsPage`/`App` prop chains gain a second field and a second state; the FE test needs a second case; and the "one operator decision" is split. Cost is moderate, not prohibitive — but the split still has to happen at submit time (the pickers emit ids), so the **kind-aware partition must exist anyway**; two pickers only add UI surface on top of it.

---

### Q12 — The enforcement seam: where the exclusion actually happens, per lane

**Question.** S-2/S-12 fix the **behaviour** (a model outside the grant is excluded from the list at each load-balance / fallback layer) but not the **seam**. The four existing scope sites evaluate only the **client-requested string** — `pkg/proxy/handler.go:839` (chat), `pkg/proxy/handler_anthropic.go:514` (anthropic), `pkg/imggen/handler.go:267-268` (imggen), `pkg/decision/handler.go:342-343` (decision **MODEL** arm) — so none of them ever sees a group's expanded member list. Where does the member-level exclusion live?

**RECOMMENDED DEFAULT: a COMBINATION — (A) a manager-level reach predicate for the member PICK + (B) a request-layer list filter for the CHAINS — refined from option (B) alone by `plan.md` §8.3 + §17.2, and marked here as REFINED so the owner ratifies what will actually be built (not the superseded (B)-alone shape).**

**Why (B) alone is insufficient (and why this is not a re-opened question).** A request-layer chain filter at the rebuild points would **spuriously 403 a group that has an in-scope sibling**: the excluded member kills the request before an allowed sibling is tried — a real availability regression, not a theoretical one. **Why (A) alone is insufficient:** a manager-level predicate on the member pick **cannot cover the entry model's `FallbackChain`**, which STRICT-EXPLICIT (S-2) requires to be filtered independently of the member pick. **Adopted: (A) + (B) together.** Concretely:

- `isServableGroupMemberBase` (`pkg/store/database/store_group_rewrite.go:348-360`) is **NOT widened** — a new `isReachableMember` sits **BESIDE** it, keeping *shape* (exists / enabled / lane) separable from *authorization* (reach).
- Phase 4 (demoted picks, `store_group_rewrite.go:270-284`) does **NOT** call the base predicate, so Phase 4 must apply the reach check **too**.
- A `ScopeExcluded` flag is added to `GroupRewriteResult` so *"empty by scope"* (⇒ 403) is distinguishable from *"empty by unservability"* (⇒ 502 `group_unservable`). The rule: **idx == 0** (the requested group) ⇒ 403 / 502 as today; **idx != 0** (a fallback-position group) ⇒ **DROP the entry with a loud log**, never 403 the whole request.

**Chat / OpenAI lane.**
- The chain is produced by the group rewrite: `applyGroupRewrite` (`pkg/proxy/handler_groups.go:68-91`) → `models.GroupRewriter.RewriteGroupEntries` (`pkg/store/database/store_group_rewrite.go:55`), and is seeded into the race at the loop-top (see build points below).
- The chain filter (B) is applied at the **four build points** (plan.md §8.3), with the placement rule **filter BEFORE `applyStickyOrder`** — sticky ordering must never promote an entry the permission filter would then remove:
  1. **C1** chat race loop-top — `pkg/proxy/handler.go:991` (`applyStickyOrder`), aliased into the coordinator at `:996`;
  2. **C2** Anthropic pre-walk — `pkg/proxy/handler_anthropic.go:539` (`applyStickyOrder`), walk at `:550`;
  3. **C3** Anthropic group re-entry rebuild — `pkg/proxy/handler_groups.go:504` (after `arc.modelList = newList` at `:495`);
  4. **C4** chat re-entry rebuild — `pkg/proxy/handler.go:1248-1249` (the swap), with C1 re-filtering on the next loop iteration. (Note: the earlier anchor `:1247` in this register was wrong — `:1247` sits inside `tryGroupReentry`'s tried-mark block `:1246-1249`; `:1348-1353` is `streamResult`, not a rebuild. The assignment of `rc.modelListOriginal` is at `:800`.)
- **The race coordinator needs no change**: it walks `c.models` (`pkg/proxy/race_coordinator.go:1899-1912` `fallbackAdvanceTargetLocked` → `c.models[i+1]`), so if the chain is filtered **before** the coordinator is seeded, the advance can never walk into an excluded model. The credential-level hedge (`modelTypeSecond` → `c.models[0]`, `pkg/proxy/race_coordinator.go:421-435`) is the **same** model, so it is not a new scope surface. The Anthropic tried-set harvest (`pkg/proxy/handler_anthropic.go:748-753`) is unaffected.

**Decision lane.**
- **Two** checks are needed, and today there is **neither**:
  1. a **group-level** check, at the group arm — the arm currently returns at `pkg/decision/handler.go:325`, *before* the allow-list at `:342-354` ⇒ **zero** scope evaluation for a decision-group request (S-13);
  2. a **member-level** check, filtering the group's member list.
- The member filter belongs in `pkg/decision/group.go`, applied to the member list **at or before `buildGroupPolicy`** (`pkg/decision/group.go:118`) — **NOT** inside `pkg/decision/policy.go`, whose doctrine forbids exclusion (*"it may reorder candidates, never exclude them — and it may suppress an ATTEMPT, never a PICK or a CHAIN"*, `pkg/decision/policy.go:15-18`). **Correction to the brief's placement hint:** "between `:118` and `:119`" is **not a real seam** — those are two adjacent statements (`policy := buildGroupPolicy(...)` / `order := policy.order()`), and `:118` is where the candidate set is *fixed*. The filter must sit **at or before `:118`**.
- The policy's own candidacy rule (`pkg/decision/policy.go:58-96`; the append at `:93`) then runs on an already-permission-filtered member list, and the empty-list case (`pkg/decision/group.go:120-123`) becomes the **403** path (S-4) instead of the 502 exhaustion path (`pkg/decision/group.go:345-358`).

**Rejected alternative (clarified): thread the token's reach into `models.GroupRewriter.RewriteGroupEntries`.** The **`pkg/models.GroupRewriter` INTERFACE signature must not grow** (`pkg/models/group_rewrite.go:82-122` stays byte-unchanged); the reach predicate lives in the `pkg/store/database` *implementation*, not in the seam contract. What is rejected is widening the contract, not store-side filtering — store-side filtering is exactly what the adopted (A) shape does (via `isReachableMember` beside `isServableGroupMemberBase`).

**Open sub-detail for `plan.md`:** the exact helper name/signature and the exact call sites (the four chat points + the two decision points) — I am not inventing a signature for a helper that does not exist yet; `plan.md` pins it.

**If you overrule** (filter inside the rewriter): the `models.GroupRewriter` interface grows, every implementation changes, and the rewriter gains an auth dependency — a wider blast for the same behaviour. I would need the plan to re-cost it.

---

### Q13 — The write-path semantics of "clear the allow-list" (explicit empty vs absent)

**Question.** This is the one question the brief does not raise, and it is a **privilege-escalation trap** in the current representation.

**The trap.** Today the store conflates "no list" and "empty list": `pkg/auth/store.go:57` (`if len(allowedModels) > 0`) and `:254` (same) write **NULL** for both ⇒ NULL means *wildcard*. Under the new conjunctive wildcard rule (Q7), NULL on **both** axes means *wildcard*. So consider a group-restricted token (`allowed_models` absent, `allowed_groups = ["coding-group"]`) whose operator **clears the group list**: if "clear" is written as NULL, both axes are absent ⇒ **the token becomes a wildcard** — an operator who meant "no groups" gets "everything".

**RECOMMENDED DEFAULT: make the three states representable end-to-end, and use the explicit-empty encoding to clear.**

1. **Store serialization changes from `len(x) > 0` to `x != nil`:** `nil` → **NULL** (absent), empty-non-nil → **`"[]"`** (present-and-empty), non-empty → JSON array. Applied at `pkg/auth/store.go:57-64` and `:252-261`.
2. **Wire:** `allowed_models` / `allowed_groups` are `*[]string` — `nil` (absent) vs `[]` (explicitly empty) are already distinguishable by the Go type. The **meaning** is pinned per operation:
   - **POST**: `nil` = the axis is absent (and both absent = wildcard, S-8); `[]` = the axis is present and grants nothing.
   - **PATCH**: `nil` = **keep existing** (today's rule, `pkg/ui/server.go:2589-2597`); `[]` = **replace with an explicitly empty list** (= grant nothing on that axis).
3. **FE:** the PATCH builder must send `[]` (not `null`) when the operator clears an axis — today's builders collapse both (`useApi.ts:920` sends `null` for an empty selection) ⇒ **the PATCH builder changes**; the POST builder keeps `null` for "nothing selected" (= wildcard).
4. **The API must document the flip.** `allowed_models: []` on PATCH **today** means "wildcard" (the store writes NULL); after this change it means "grant nothing". The FE is unaffected (it sends `null`/`[]` deliberately now), but **external consumers** that relied on `[]` ⇒ wildcard are affected. This is a **fail-closed tightening**, which is the direction S-5 demands — but it is a wire behaviour change and must be stated in the ADR (S-7) and the release notes.

**Options and their real costs.**

| Option | Cost |
|---|---|
| **(a) Explicit-empty encoding end-to-end** (recommended). | The store's serialization convention changes (2 lines + the DTO comments); the FE PATCH builder changes; the wire meaning of `[]` flips (documented). In exchange: "clear" is expressible, and the escalation trap in Q7 is closed. |
| **(b) Keep the nil/empty conflation** (empty ⇒ NULL ⇒ wildcard on that axis). | No code change. But "clear the groups list" **escalates the token to wildcard** (the trap above), and "grant nothing on one axis" is inexpressible. |
| **(c) Keep the conflation, and prevent the trap by a UI-side rule** ("you cannot clear the last axis to absent; clearing both axes is refused with a 400"). | A special-case API rule for a representable case — policy in the UI, semantics in the store; and it still leaves "grant nothing" inexpressible. |

**If you overrule to (b).** The trap must be closed by **(c)**'s UI rule, and the ADR must state that "clearing the last list restores full access" — an operator-visible footgun in a security feature. I do not recommend it.

---

## 3. RISKS IF NOT DECIDED

| # | Risk | Tie-back |
|---|---|---|
| **R-1** | **The live lane breaks.** Under STRICT-EXPLICIT with no data repartition, `smoke-primary-agentic` → `[agentic-group]` and `smoke-primary` → `[coding-group, quick-group]` get **403 on their groups** on both hosts (2 tokens/host). This is not theoretical — those tokens exist in the live inventory today. | Q2 |
| **R-2** | **The fail-open bug survives.** `pkg/auth/token.go:64` logs *"denying all models"* while `:116` grants **everything** for a malformed row ⇒ a corrupt row is a **full-access token**. S-5 is unmet and the defect ships as a "fix". | Q7 |
| **R-3** | **The fix itself becomes the escalation.** With a per-axis wildcard rule, a repartitioned narrow token (`allowed_models` absent + `allowed_groups` present) becomes a **model-level wildcard** — the fix grants more access than the bug did. The conjunctive wildcard rule is the only thing that prevents this. | Q2, Q7 |
| **R-4** | **Migration `034` ships inert.** The registry is an ordered slice, **not** a directory scanner (`pkg/store/database/migrate.go:24-83`); without the append after `:82` the SQL files are **silently inert** — the "Amendment-1 trap" (5th occurrence, `:78-82`) that `033` already fell into once. Nothing in the build fails; the failure surfaces at the first `allowed_groups` SELECT. | Q1 |
| **R-5** | **The permission path goes vacuous.** With an optional-interface shape (Q5(c)), the 9 mocks silently do not implement the group grant ⇒ the UI's group-permission path is exercised only against the real store ⇒ the anti-vacuous gate (`.agents/tester/PACKS.md:15`/`:18`/`:20`) cannot catch it ⇒ a runtime no-op that reads as "working". | Q5 |
| **R-6** | **"Clear the allow-list" escalates.** Without the explicit-empty encoding, clearing the group list writes NULL on both axes ⇒ **wildcard**. | Q6, Q13 |
| **R-7** | **FE and BE disagree about what a group is.** If the FE emits one flat list and the wire has two fields, the STRICT-EXPLICIT split is lost at the last hop — the BE cannot tell a group from a model, and the whole "group reach" feature silently degrades to "model reach". | Q6, Q11 |
| **R-8** | **The three 403 writers drift.** Extending the envelope without pinning it in the ADR (S-7) lets `pkg/decision/handler.go:784-794`, `pkg/proxy/handler.go:525-535` and `pkg/imggen/handler.go:713-723` diverge ⇒ client-visible inconsistency between lanes. | Q8 |
| **R-9** | **The release gate reports a false boundary violation.** `034` + the registry append change `pkg/store/database/migrations/` and `pkg/store/database/migrate.go` — **boundary paths** that the gate asserts are 0 changed files (`.agents/tester/PACKS.md:35`, the "BOUNDARY PATHSpec"). The change must be **pre-declared to the tester** as an authorized boundary change, or the gate burns a cycle. | Q1 |
| **R-10** | **A hidden DDL breaks the usage lane.** There are **8** hand-rolled `auth_tokens` tables in tests, not 7 (see §5 D-1); the 8th (`pkg/ui/handlers_usage_test.go:33`) lacks even `allowed_models` and survives today only because the usage queries do not select that column. Any new code path that selects `allowed_groups` through it breaks the usage lane. | Q4, §5 |
| **R-11** | **Cross-tenant cooldown benching on scope-excluded group picks — CORRECTED (fix-pass 2, leader Ruling 1): the cooldown-skip is MANDATED.** *(Numbering per the 🟡7 legend: the architect's cooldown defect is **A-PD-3**; the leader disposition is **L-PD-6**; ADR §4.7 corrected accordingly.)* The `(group, member)` cooldown/skip-cache pool is **shared across tokens** (`ExcludeAndReselect` `0 → defaultCooldown` coercion at `pkg/grouplb/engine.go:788-791` writing `st.cooldowns[id]`, plus the shared `(group, member)` `skipcache.Mark`). **RULED (fix-pass 2): scope-excluded candidates are removed at construction/advance and are NEVER attempts — therefore they can never legitimately write cooldowns. The skip is MANDATORY:** scope rejections in all selection phases skip every cooldown/skip-cache write; `ExcludeGroupMemberScoped` never writes a cooldown for a scope-refused offer; the architect's multi-token pin is IMPLEMENTED (P-G32: "Token A's scope exclusion never cools M1 for wildcard Token B"). **RETRACTION RECORD:** the prior claim that "the leader ruled to accept and document instead of fixing" was **UNSOURCED — no ratification record exists** — and is **RETRACTED** by fix-pass 2; R-11's earlier "accepted & documented, NOT fixed" title/body, ADR §4.7, plan.md §9.2a, and plan-overview row 20 are all corrected to the mandate. A-3's §1.4 airtightness condition is thereby SATISFIED. | A-PD-3, L-PD-6, ADR §4.7 |
| **R-12** | **Re-entry exhaustion keeps the availability envelope — leader-ratified (fix-pass 2, 🟡9; ratifies plan.md §9.4).** A re-entry that exhausts **because the remaining members are out of reach** falls through to today's terminal path (upstream-derived `all_models_failed` / anthropic "All models failed" 502), **not** a 403. Reason (§9.4): at that point upstream attempts have already occurred, so a 403 would be a *false* permission signal (an attempt happened = the request was partly authorized) and would break the "403 before any attempt" guarantee. The observable contract still holds: no out-of-scope model is ever attempted. **Ratified as leader-ratified 2026-11-18 (fix-pass 2)** — §9.4's stated plan decision now carries a ratification record. | plan.md §9.4, §7 row 27 |
---

## 4. OUT OF SCOPE (flagged only — no work planned)

| # | Item | Why out of scope |
|---|---|---|
| **O-1** | The FE/BE `prefix` drift — `pkg/ui/frontend/src/types.ts:244` declares `prefix: string`, the BE never returns it (`TokenResponse` `pkg/ui/server.go:2491-2498`). | Pre-existing, unrelated to permissions, harmless. Not fixed here. |
| **O-2** | **MCP server-level authorization** (which upstream MCP servers a token may reach). | A different permission axis (Q9); a follow-up, not this commission. |
| **O-3** | The stale `pkg/store/database/sqlc/schema.sql:48-55` (no `allowed_models`, and no `allowed_groups`). | **Out of the build**: `pkg/store/database/sqlc/` contains only `queries.sql` + `schema.sql`, and the only Go reference is a comment (`pkg/store/database/store.go:23`). Do **not** regenerate it in this commission (it would pull the whole schema). Flagged for a separate cleanup. |
| **O-4** | The periodic-reconcile write-mutex stall (`pkg/store/database/credential_group.go:637-638` — the writer-preferring `m.mu.Lock` held across a DB read stalls the live request path). | A known, separately-tracked risk; unrelated to the token permission model. |
| **O-5** | The decision lane never writes per-attempt ordering to stdout (the SSE event stream is the only proof source). | Observability gap, not a permission gap. |
| **O-6** | The FE **vocabulary collision**: a new "Groups" picker next to the existing **Credential Groups** surfaces. Two different group entities (model load-balance groups vs credential groups) would share the bare noun in the same tab bar. | Per the house naming rule, the load-balance entity keeps the bare noun "group" and the credential entity must stay qualified — so the **new picker's label must disambiguate** (e.g. "Load-balance groups"). The exact label is a `plan.md` copy decision; the requirement to disambiguate is flagged here. |
| **O-7** | The `TokenList` prop type lacks `kind` (`:7`) even though the caller passes it (`SettingsPage.tsx:744`). | Not a defect on its own — it becomes one only if the FE needs kind in `TokenList` (it does, per Q11 ⇒ it **is** in scope, as part of Q11's implementation). Listed here only to mark the boundary. |
| **O-8** | The 403 envelope's missing `code` field (machine-readable denial reasons). | Deferred by the Q8 recommendation (a); re-openable without disturbing the commission. |

---

### 4.1 Deferred (recorded explicitly as DEFERRED, not open) — fix pass B item 13

These are **deliberately deferred to post-ship**, not open questions:

| # | Deferred item | Why deferred |
|---|---|---|
| DF-1 | Delete the legacy `ScanAllowedModels` / `IsModelAllowed` shims (zero non-test callers) | post-ship cleanup; the shim keeps unrelated tests compiling |
| DF-2 | `createToken` 64 KB body cap | pre-existing gap, OAuth-gated; post-ship follow-up (the POST-cap ADD was rejected in fix pass A) |
| DF-3 | Binding-map-untouched D13 runtime pin | needs a runtime harness; follow-up |
| DF-4 | CVE scan + live-host probes | gate/ship scope (T3.12), not corpus work |

---

## 5. DRIFT REPORT + OPEN ITEMS FOR `plan.md`

### 5.1 Corrections to the brief's evidence base (verified in the worktree at `a3f0335`)

| # | Brief said | Verified | Correction |
|---|---|---|---|
| **D-1** | "**7** hand-rolled `auth_tokens` DDLs that MUST gain the column". | **8.** `pkg/auth/store_test.go:53`, `pkg/mcp/auth_routing_test.go:58`, `pkg/mcp/e2e_test.go:72`, `pkg/mcp/handlers_api_test.go:49`, `pkg/mcp/mcp_endpoint_split_validation_test.go:60`, `pkg/mcp/test_helpers_test.go:43`, `pkg/proxy/handler_integration_test.go:52` **plus `pkg/ui/handlers_usage_test.go:33`**. | The 8th (`pkg/ui/handlers_usage_test.go:33-41`) has **neither** `allowed_models` **nor** `allowed_groups`, and survives today only because the usage-lane queries do not select `allowed_models` (its INSERTs at `:104`, `:152`, … omit the column too). It must be assessed against the new code path — see R-10. |
| **D-2** | "a SCOPE filter belongs in `pkg/decision/group.go` **BETWEEN :118 and :119**". | `:118` = `policy := buildGroupPolicy(...)`; `:119` = `order := policy.order()` — **adjacent statements**. | **Not a real seam.** The filter must sit **at or before `:118`** (before the candidate set is fixed); `pkg/decision/policy.go:15-18` forbids exclusion inside the policy. See Q12. |
| **D-3** | "`TokenStoreInterface` `:19-28`". | The interface declaration is at `pkg/auth/store.go:21`; the six method lines `:22`–`:27` are **exact**. | Declaration line is `:21`, not `:19`. Method lines confirmed. |
| **D-4** | "fail-open: `pkg/auth/token.go:62-66` … `IsModelAllowed` `:114-125`". | The malformed branch is `:63-65` (`if err := json.Unmarshal` `:63`, the `SECURITY:` log `:64`, `return []string{}` `:65`); `IsModelAllowed` is declared at `:114` and the wildcard branch is `:116` (`if len(t.AllowedModels) == 0`). | Ranges were off by one at each end. The **semantics** are exactly as described: the log says *deny*, the code at `:116` **grants everything**. |
| **D-5** | "`loadMigrationSQL :149-171`", "`RunMigrations :86-99`". | Both are **methods**: `func (s *Store) RunMigrations(ctx context.Context) error` at `:86`; `func (s *Store) loadMigrationSQL(name string) (string, error)` at `:149` (sprintf `:164-165`). | Method, not free function — a plan that calls them must use the receiver. |
| **D-6** | The four scope-check sites. | **All four exact**: `pkg/proxy/handler.go:839`, `pkg/proxy/handler_anthropic.go:514`, `pkg/imggen/handler.go:267` (guard) / `:268` (call), `pkg/decision/handler.go:342` (guard) / `:343` (call). | Confirmed, no drift. |
| **D-7** | The decision-group bypass at `pkg/decision/handler.go:317-326`, returning at `:325`, before the allow-list at `:342-354`. | **Exact** — the group arm is `:323-326`, `h.serveDecisionGroup(...)` at `:324`, `return` at `:325`; the allow-list guard is `:342`, `IsModelAllowed` `:343`. | Confirmed. Also confirmed: the 403 envelope writer is `:784-794` (`sendModelNotAllowedError`), byte-identical to `pkg/proxy/handler.go:525-535` and `pkg/imggen/handler.go:713-723` (the decision writer assigns the encode to `_`; the other two do not — a source-level difference, not a wire difference). |
| **D-8** | The FE test to flip: `TokenForm.test.tsx:41-85`, fixture `grp-1` at `:49`, absences at `:82`/`:83`/`:84`. | **Exact** — `:80` `toContain('Chat One')`, `:81` `toContain('Chat Two (no kind)')`, `:82` `not.toContain('Image Gen One')`, `:83` `not.toContain('Decision One')`, `:84` `not.toContain('Group One')`; fixture group row at `:49`. | Confirmed. Note the test **title** (`:41`) still encodes the old contract ("excludes image-gen + decision rows") and must be renamed with the assertions. |
| **D-9** | "`pkg/ui/server.go` POST has **NO** body cap (contrast PATCH 64KB `:2570`)". | Confirmed: `createToken` (`:2646-2712`) has **no** `MaxBytesReader`; PATCH's is at `:2570`. | Confirmed — but the planned POST-side addition was **REJECTED (fix-pass 2 scope ruling #3)**; only the PATCH cap remains. |
| **D-10** | "`pkg/store/database` holds NO `auth_tokens` Go code … a STALE `sqlc/schema.sql:48-55` … confirm out of build before touching". | Confirmed **out of build**: `pkg/store/database/sqlc/` holds only `queries.sql` + `schema.sql`; no generated Go package; the only Go mention is a comment at `pkg/store/database/store.go:23`. | Confirmed — leave it stale; do not regenerate (O-3). |
| **D-11** | "`pkg/auth/store.go:13-17`" for the store struct; "`:57-64`" create serialization; "reads via `ScanAllowedModels` at `:137`/`:179`/`:246`". | Struct at `:14`; the serialization block is `:54-65` (the `if len(allowedModels) > 0` guard at `:57`); the deserialize sites are `:136`/`:178`/`:245` (the calls are one line below each). | Off-by-a-few on the ranges; **the load-bearing facts are exact**: `len(x) > 0` ⇒ NULL for nil *and* empty (the Q13 trap), and the three read sites. |
| **D-12** | "`pkg/decision/group.go:120-123` empty order ⇒ exhaustDecisionGroup 502". | `:120` `if len(order) == 0 {`, `:121` the exhaustion call, `:122` `return`, `:123` `}`. | Confirmed — this is the branch that becomes the **403** path (Q8/Q12). |
| **D-13** | **The Q1 recommendation's SQL diverges from the binding migration (`plan.md` §5.2 as amended) — recorded, superseded BY REFERENCE.** The original Q1 SQL (this register, §Q1 body above) carries neither the PD-2 per-row `DO $$ … EXCEPTION WHEN invalid_text_representation` guard (PG) nor the PD-4 `NULLIF(…, '[]')` (SQLite), and it predates the fix-pass-2 **Ruling 6** atomicity/idempotency/named-recovery mandate (DDL + repartition in ONE transaction per dialect; boot-abort ⇒ helm rollback to the v202 anchor). The binding bodies are `plan.md` §5.2 verbatim; this row records the drift so no implementer copies the register SQL. **Namespace note:** the ADR's `### D13 —` heading is a DIFFERENT namespace (ADR decision-record numbering) and is NOT this register row. | Q1 supersession line, plan.md §5.2/§5.6, Ruling 6 |

### 5.2 Open items I could NOT verify from the evidence base — `plan.md` must pin them (I am not inventing the values)

| # | Open item | What `plan.md` must pin |
|---|---|---|
| **OI-1** | **SQLite JSON1 availability.** The repartition (Q1/Q2) needs `json_each` / `json_group_array`. The shipped driver is `modernc.org/sqlite v1.46.1` (`go.mod:8`), imported at `pkg/store/database/connection.go:14`; modernc's amalgamation ships JSON1, but I did **not** execute a probe. | A **probe** (`SELECT json_each(...)` / `json_group_array` against the shipped driver) **before** the repartition SQL is relied on. If JSON1 is unavailable, the repartition must move to a Go-side backfill (idempotent, boot-time) — a materially different plan. |
| **OI-2** | **The models-cache TTL on the permission path.** The token cache is 60 s (`pkg/modelscache/models.go:19`), but the group→member expansion reads the **model set**, which may itself be cached. | **RESOLVED (fix-pass 2, Ruling 5):** the permission path reads the model set from the **IN-MEMORY snapshot** (decorator rows / `currentModelRows()`), invalidated on group writes — a DB read per request is FORBIDDEN on this path (I14 hard requirement); the bounded reach-staleness window is the existing models-cache TTL. Pinned in plan.md §4.2/§4.4. |
| **OI-3** | **The permission-filter helper's exact signature and call sites.** | Name, signature, and the exact four chat points + two decision points (Q12). I am not inventing a signature for a helper that does not yet exist. |
| **OI-4** | **`pkg/grouplb` / `pkg/credentiallb` interaction.** The Emperor's ruling names the "load balance" and "fallback" layers; the group rewrite lives in `pkg/store/database/store_group_rewrite.go` and the member selection in `pkg/grouplb`. | Whether the reach filter needs any cooperation from `pkg/grouplb`/`pkg/credentiallb` at all, or is fully contained in the request layer (my recommendation). If it does, the exact seam there. |
| **OI-5** | **`pkg/modelscache` decorator-forward obligations.** The house rule: a new/extended interface method needs a decorator forward **and** a decorator-through test (precedent `pkg/modelscache/models.go:1321-1341` + `pkg/proxy/win_clear_winner_identity_test.go:114-190`). | The exact decorator methods to extend (`pkg/modelscache/tokens.go:263` create write-through, `:297` update-then-drop) and the decorator-through test's shape. |
| **OI-6** | **The `AuthToken` permission-field shape** (flag-on-struct vs a `TokenPermission` sub-struct) and the scanner signature. | Q7's final shape. Both are acceptable; the plan picks one and the ADR (S-7) rules it. |
| **OI-7** | **The FE's "All models" copy and the group-picker label.** | The badge text when only one axis is restricted, and the disambiguating label for the load-balance-group picker (O-6). |

---

## 6. RATIFICATION BLOCK — for the owner to fill

| Q | Recommended default (one line) | Ratify | Overrule (state the alternative) |
|---|---|---|---|
| Q1 | JSON TEXT column + `034` (SQLite `ALTER TABLE ADD COLUMN`, PG `ADD COLUMN IF NOT EXISTS`) + registry append after `migrate.go:82`; rollback = additive (no schema rollback), data repartition reversible by a documented reverse UPDATE | ☐ | |
| Q2 | Clean break **+ pre-ship data repartition in `034`** (legacy group ids move to `allowed_groups`; live narrow tokens behave identically) | ✅ **RATIFIED — (b)** · ruling date 2026-11-18 · authority: leader ruling under TrueAuto per AUTH COMMISSION explicit order · **conditions:** (1) reverse-repartition-first is a **BLOCKING rollback step**; (2) the pre-ship malformed-row read-only inspection on **both hosts** stays a release-gate step | |
| Q3 | `allowed_groups` = **group ids only** (any lane); decision models stay in `allowed_models` | ☐ | |
| Q4 | `allowed_groups`: existence **+** `kind='group'` → 400. `allowed_models`: existence-only → 400. Validation on the **inbound** set only. A group id in `allowed_models` is accepted (target-only grant) | ☐ | |
| Q5 | Extend the **two** existing methods with ONE write payload `auth.OptionalTokenPermission`/`OptionalAllowlist` (A4 supersession); all 11 implementers move in one atomic commit; decorator forward + decorator-through test | ☐ | |
| Q6 | `allowed_groups` on POST/PATCH/responses; snake_case; **the POST 64 KB body-cap clause is REJECTED (fix-pass 2 scope ruling — only the PATCH cap at `:2570`, which LANDED, remains)**; old-FE/new-BE and new-FE/old-BE both safe | ☐ | |
| Q7 | Per-axis tri-state (absent / present / invalid) + **conjunctive wildcard**; the invalid flag lives **on the token struct** (cache-safe) | ☐ | |
| Q8 | **403**, existing envelope byte-identical, `model` = client-requested id; excluded members to log + events; check **before** any upstream attempt | ☐ | |
| Q9 | MCP = **explicit non-goal** + named follow-up (server-level authorization) | ☐ | |
| Q10 | Expansion resolved **per request** against the live model set; **never** cached; the token cache holds grant lists only | ☐ | |
| Q11 | **LANDED (fix pass B-final I3-3 — supersedes the one-picker recommendation): TWO per-axis pickers**, groups EXCLUDED from the models picker (`TokenForm.tsx:84-85`/`:159-181`; pin `TokenForm_PickerExcludesGroupModels`); split authoritative at save (F1 — the picker is authoritative per axis; cross-axis additions honored per `grantAxes.tsx`: a stored group id stays on its stored axis, a NEW group id classifies onto groups). Ratification trail: the one-picker default was superseded by the landed implementation, verified by the approver's workers | ☐ | |
| Q12 | **(A)+(B) combination** (manager-level `isReachableMember` beside `isServableGroupMemberBase` + request-layer chain filter at C1–C4; `ScopeExcluded` flag distinguishes 403 from 502; idx==0 keeps 403/502, idx!=0 drops with loud log). `pkg/models.GroupRewriter` interface unchanged. Refined by `plan.md` §8.3 + §17.2 | ☐ | |
| Q13 | Explicit-empty end-to-end: store `nil`→NULL, `[]`→`"[]"`; PATCH `[]` = clear; POST `nil` = absent = wildcard | ✅ **RATIFIED — (a)** · ruling date 2026-11-18 · authority: leader ruling under TrueAuto per AUTH COMMISSION explicit order · **condition:** the FE PATCH-builder change and the store serialization change MUST ship in the **same image** (single-image coupling invariant) | |


**Overruling any row is a legitimate outcome** — each row states what changes. If you overrule Q2 (the live-token behaviour question) or Q13 (the clear-semantics question), the release checklist must gain the corresponding operator step; those two are the only ones with a **live-lane** consequence.

### 6.1 Leader rulings recorded 2026-11-18, fix-pass 2 (reviewer 0a249260 verdict + approver iteration-001; authority: commission owner, per planner dispatch)

*Provenance line: "Leader rulings recorded 2026-11-18, authority: commission owner (per planner dispatch, reviewer 0a249260 verdict); Rulings 5–6 are approver-iteration-001 derived."*

| # | Ruling | Disposition |
|---|---|---|
| **Ruling 1** | **Cooldown-skip = IN.** Scope-excluded candidates are never attempts ⇒ never legitimate cooldown writes. plan §9.2a + P-G32 WIN; the architect's multi-token pin is implemented ("Token A's scope exclusion never cools M1 for wildcard Token B"). The prior "accept and document" claim in R-11 was **UNSOURCED (no ratification record) — RETRACTED**; R-11 and ADR §4.7 corrected to MANDATE the skip; phase2 T2.3 resolves to skip-only. | IMPLEMENTED in this fix pass (R-11, ADR §4.7, plan §9.2a, P-G32, phase2 T2.3) |
| **Ruling 2** | **PATCH contract = Q13(a), `nil` = KEEP (TWO wire states).** The cycle-1 reset-to-absent third state is **REVERSED/REMOVED** from plan §2.8/§6.1: key omitted **or** explicit `null` ⇒ column untouched (raw bytes preserved, incl. malformed DENY-ALL); `[]` ⇒ `'[]'`; ids ⇒ array. Wildcard promotion on EDIT is intentionally inexpressible (re-create the token; release note + FE-9 note). Landed `updateTokenPermission` keep-contract is CORRECT as-is. The json.RawMessage/null-vs-omitted wire distinction is NO LONGER needed (reversal recorded in plan §2.8). `OptionalAllowlist` is the ONE internal write-payload name (A4). | IMPLEMENTED in this fix pass |
| **Ruling 3** | **Alias/claim gate = RESOLVED-ONLY (§7.1 wins).** No union gate: authorize `Allows(resolved.ID)` only; 403 at ENTRY quoting the client-requested claimed string; ZERO side effects before the 403; alias-only grant does NOT pass (strict row-id narrowing, documented); G0 shadow-ref check stays. The on-disk corpus already carried strict (T2.16 + R2.18); verified and reconciled — no union text remains normative. | VERIFIED STANDING (other planner's landing CORRECT — reconciled, no change needed) |
| **Ruling 4** | **Corpus freeze = YES.** After this fix pass: 8 core docs hash-pinned (manifest hash-of-hashes) at one revision; freeze line written into plan.md header + phase3 T3.9 gate inputs; worktree state recorded; reviewer gate re-dispatched on the frozen corpus. | EXECUTED at the end of this fix pass |
| **Ruling 5** | **I14 = HARD REQUIREMENT: zero new per-request DB reads for scope resolution.** `GetModelNative` is a live SELECT under `m.mu.RLock` (approver-proven, `store.go:1095-1145` / forwarded `modelscache/models.go:1301-1303`). Reach membership MUST read an **in-memory snapshot source** (existing cache-layer snapshot class), invalidated on group writes. Escalation clause evaluated: **NOT triggered** — a valid seam exists without new design (a per-request in-memory index built once from the decorator's in-memory rows — `GetModels()` / `currentModelRows()` — invalidated by the existing group-write invalidation path), pinned in plan §4.2/§4.4, phase2 T2.0, ADR I14, decisions Q10/OI-2. | IMPLEMENTED in this fix pass (seam specified; no accepted-risk recorded) |
| **Ruling 6** | **Migration 034 atomicity = REQUIRED.** DDL + repartition in ONE transaction per dialect (both support transactional DDL); idempotent on re-run; failure/recovery path NAMED (boot abort ⇒ helm rollback to the v202 anchor `:0082773` / daoduc rev 46 / tier-1 image `:3cf3cb8` (v200) / primary gen 41; G0 is the preventive gate). Empirical verification of the frozen SQL on `[broken`/`{}`/`123`/`null` run in a TEMP DIR; results recorded (fix-pass report + §5.5). | IMPLEMENTED in this fix pass |

**LEADER ADJUDICATION RECORD (fix pass B-final, 2026-11-19): approver iteration-003 REJECTED** with **3 doc-contract blockers** — **I3-1** fail-open text in three normative sections (plan §4.1 scanner block / phase1 T1.2 / ADR Clarification #3 still carried the pre-`f9adbce` empty-string ⇒ ABSENT contract; the approver's workers verified the LANDED `ScanGrant` correct and safe); **I3-2** rollback-ordering contradiction (decisions Q1 rollback items 2–3 said "code rollback trivially safe / no ordering" vs ADR §7.1 / `plan.md` §14.7 / `phase3` runbook; landed reverse-first order is correct); **I3-3** `plan.md` §12 mandated a one-picker design while the LANDED FE is two per-axis pickers with groups excluded from the models picker (verified correct + pinned). **Max 3 iterations reached; leader adjudication per the TrueAuto 3-iteration protocol: all three fixed in this final pass, corpus re-frozen (generation 8), proceeding to the release gate with this record.**

**LEADER NOTE (gen-10 re-pin, 2026-11-19):** the **9 commission-named obligation pins LANDED** in `1e65774` (4 test files, +1589; test-only; the frozen 034 SQL untouched; **non-vacuity proven** — 16 mutations incl. 8 production injections). **Two corpus false premises were corrected to LANDED behaviour** (the code was always correct): (i) a **disabled group named by its OWN id is SERVED** (no `enabled` filter on direct IDs — `store.go:1102-1140`); only its **shadow claim** is inactive and only its **members** are disabled-filtered; the no-leak property is **structural** (403 / 502 / 200 never distinguish disabled from out-of-scope); (ii) **entry-chain groups ARE EXPANDED** into their in-reach member (`handler_functions.go:200-202` + `store_group_rewrite.go:105-181`) so the raw group id is never a candidate and never 502s — the surviving, documented, unpinned wart is a group id inside a group **member's** own chain. **Ledger final at 121 pins (88 Go + 33 FE).** Corpus **re-frozen (generation 10)**; proceeding to the release gate.

**LEADER NOTE (gen-11 re-pin, 2026-11-19):** cycle-4 council returned **1 blocker + 3 ride-alongs** (docs-only; zero code/test/migration/git changes). **Blocker FIXED:** the `plan-overview.md` artifact-index row for `phase1-plan.md` carried a stale total (the pre-gen-10 pin count) → now reads "(121 total — 88 Go + 33 FE, `plan.md` §13.1a)". **Ride-along 1:** EVERY artifact-index line count re-measured via `wc -l` (plan.md 1,586 · adr 455 · decisions 562 · phase1 404 · phase2 811 · phase3 388 · architecture-recommendation 145 · research-authz-surfaces 264 · research-runtime-plumbing 455 · research-storage-api-fe 366; the `plan-overview.md` self-row stays "(this file)" — a self-count cannot be stable) + a **drift note** appended to the index header ("line counts measured at gen-11 via `wc -l`; they drift with every corpus pass — re-measure before citing"). **Ride-along 2:** the planning-only marker line's parenthetical (a stale HEAD SHA + a stale commit count + a stale tree-state qualifier) **DROPPED ENTIRELY** — the residue-proof form keeps the supersession marker and points at the REAL STATUS block above without restating numbers; the REAL STATUS block itself advanced to **HEAD `1408e8b`** (tester FINAL VERDICT PASS — token-authz gen-10 @ `1e65774`, 121-pin sweep clean), diff vs base **97 files, +11710/−899**. **Ride-along 3:** `phase3-plan.md` §5.1 obligation row (f) status cell → "**LANDED (`1e65774`) + LANDED (§13.1a 29/30)**" (the "working tree at measurement" qualifier is gone — the pin is committed). **Chain count:** measured truth at `1408e8b` is **22 commits incl. base (21 on top of base)** (`git log --oneline a3f0335..1408e8b | wc -l` = 21) — the council's snapshot figure (21 incl. base / 20 on top) predates the tester verdict-commit, so the corpus records the measured count and keeps the snapshot figure explicitly labelled as the `1e65774` tip. **Re-pinned generation 11** (the manifest hash lives ONLY in the `plan.md` header — it is not restated here, because this file's own hash feeds that manifest and a self-citation would not converge; the priors `f68743d6…` / `e39dfc72…` / `36a20d82…` are superseded); freeze line remains **line 7** and all three exclusion recipes (`sed '7d'` / `grep -v 'corpus frozen at'` / anchored `sed`) are byte-identical.

**LEADER ADJUDICATION RECORD (fix pass C, 2026-11-19): reviewer cycle-3 REJECTED** with **5 corpus-only CRITICALS** — (1) G12(b) raw-grep false-on-a-correct-tree (3 legitimate hits); (2) §5.1 mandated 9 pins absent at HEAD; (3) I3-3 one-picker/"delete the kind filter" contract surviving in ~7 places after the §12.1 fix; (4) E2 `''`-conflation residual in the binding ADR; (5) R5 stale status/count strings in `plan-overview.md`. **The code was verified CORRECT on every security-critical mechanic by BOTH reviewer models.** **Governor's hybrid adopted: all five fixed corpus-side; the 9 pins are LANDED under the canonical names by the parallel test-only lane; corpus re-frozen (generation 9); proceeding to the release gate with this record.**

**Rejected-by-leader scope expansions (recorded 2026-11-18 fix-pass 2 — do NOT implement; exact dispositions):**

| # | Expansion | Disposition (exact) |
|---|---|---|
| 1 | CG-1 tri-state wire extension beyond Q13(a) | **REJECTED and actively REMOVED** — Ruling 2 mandates the removal; the wire is two-state (omitted/null = keep, `[]` = clear, ids = replace). No third wire state exists anywhere in the corpus. |
| 2 | NoUltimateEscalation audit (as a NEW addition) | **REJECTED — not added by this pass.** The pre-existing PD-13/T2.14 residual-escalation audit content predates this ruling and stands as already-planned corpus; this pass adds no new audit requirement, gate step, or pin beyond it. |
| 3 | POST 64 KB MaxBytesReader | **REJECTED — the planned POST-side cap addition is REMOVED** from plan §6.1, P-G21, phase1 §1/§2.1/T1.8/T1.11 (`TestTokenAPI_PostBodyCap` deleted), plan-overview, and decisions Q6/D-9. The PATCH-side 64 KB cap that LANDED at `:2570` is untouched fact. |
| 4 | MCP startup log line | **REJECTED — not implemented in this commission**; the MCP non-goal + named follow-up stands as adopted (Q9); no startup-log task is added. |
| 5 | Write-time existence validation EXPANSION | **REJECTED (the expansion, not the base):** decisions Q4's adopted inbound-only base (groups: exists + `kind='group'`; models: existence-only; inbound set only) stands **exactly as adopted — neither extended nor repealed**. Sweep result: no corpus passage extends validation beyond the Q4 base (no merged-result validation found); nothing to remove. |
| 6 | P-G32 removal | **REJECTED — P-G32 STAYS**, rewritten per C2/Ruling 1 as the mandated cooldown-skip + multi-token pin under its canonical name. |
