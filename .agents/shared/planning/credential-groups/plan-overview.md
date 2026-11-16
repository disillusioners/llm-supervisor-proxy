# Credential Groups — Implementation Plan (overview)

Date: 2026-11-16 · Planner-synthesized overview (aggregation of three worker phase plans + three explorer research files)
Worktree: `llm-supervisor-proxy-wt-cred-groups` · branch `feature/credential-groups` · base `2b987bf` (= origin/latest at claim)
Status: **PLAN COMPLETE — nothing implemented, nothing deployed.** Committed as `docs(plan)`; no push, no ref moves (worktree claim `wt.claim.cred-groups` honored).

## Objective

Let the user bundle same-provider credentials into named **credential groups** and reference them from models alongside direct credentials, so adding/removing an account happens once and every referencing model follows. Grouping is **management-only**: at model-resolution the credential list is flattened (groups expand in place, declaration order, dedup keep-first) and credential selection — rotation, cooldowns, upstream skip cache, weights, sticky ordering — operates on the flat list and never knows groups exist.

## Scope

Backend (migration, types/validation, manager, flattening seam, decorator forwards, API) → Frontend (credentials container with vertical sub-nav, group management tab, mixed model-form picker) → Gate + Ship (Pack A/B + FE gate, landing, CI, image verify, daoduc-first deploy, live rotation proof). Config-only feature: `k8s/` chart untouched.

## Files in this directory

| File | Author | Content |
|---|---|---|
| `research-seam.md` | explorer | The resolution seam: 8 line-anchored sites, decorator surface, entry-point census, AST-guard precedents |
| `research-store-api.md` | explorer | Migration recipe, manager/CRUD patterns, naming-collision audit (zero collisions), gate conventions |
| `research-fe.md` | explorer | SPA map, decision-lane sub-tab spec (spec-only, zero commits), overlap forecast, tsc/static gate mechanics |
| `phase1-plan.md` | worker | Backend — 7 work units, 31 tasks, 23-test backend matrix |
| `phase2-plan.md` | worker | Frontend — component tree, 10 FE test suites, tsc parity, merge-conflict disclosure |
| `phase3-plan.md` | worker | Gate + Ship — 97 tasks, 8 ⛔ user-authorized gates, live verification, rollback |
| `plan-overview.md` | planner | This synthesis + binding cross-phase adjudications |

## Phases

| Phase | File | Objective | Exit criterion (abridged) |
|---|---|---|---|
| 1 — Backend | `phase1-plan.md` | Migration 033 + `CredentialGroup` types/validation + manager CRUD/reverse-lookups/fan-out + THE flattening seam + decorator forwards E-1..E-8 + `/fe/api/credential-groups` + AST pin | Pack A green incl. decorator-through tests, AST pin non-vacuous, expanded-cap + E-3 + empty-group pins pass |
| 2 — Frontend | `phase2-plan.md` | `CredentialsContainer` (left vertical sub-nav [Credentials][Credential Groups], decision-lane idiom) + `CredentialGroupsTab`/`CredentialGroupForm`/member picker + mixed model-form picker + FE tests | vitest suites green, tsc 19/9 unchanged, static sha256 zero-mod proof, plain-list regression byte-identical |
| 3 — Gate + Ship | `phase3-plan.md` | Local release gate → land (`--no-ff` latest → detached-master promotion, explicit-SHA pushes) → CI watch → image verify → daoduc-first deploy → live scenario → rollback documented | Exit criterion §9.1–9.12: marker verified both hosts, live rotation proof from both sources, census empty after cleanup |

## Locked core design (evidence in phase files)

1. **Representation** — a group ref is a `models.CredentialRef` with new `GroupID`, persisted as `{"credential_id":"","group_id":"…"}` inside the model's existing `credentials_json`. No `models` column change; both deep-copy paths already copy `Credentials` verbatim (trap defused by construction). [phase1 §Locked representation]
2. **THE flattening seam** — ONE helper (`flattenedCredentialRefs`) in `pkg/store/database/store.go` applied at 8 line-anchored sites: five engine-binding sites (`:789`, `:867`, `:1632`, `:1768`, `:1834`) and both resolver variants (`:2543`, `:2679`), always **flatten-before-len-branches** (kills the E-3 single-group-ref 502 trap). Every production entry point converges on the two resolvers; `pkg/proxy`, `pkg/credentiallb`, `pkg/stickyorder`, `pkg/skipcache`, `pkg/modelscache` change nothing for resolution. Raw-in reads / flatten-in resolve-and-bind (flatten-at-read would destroy FE round-trip grouping). [phase1 §d; research-seam]
3. **Naming scheme** (verified collision-free repo-wide) — DB `credential_groups`; Go `CredentialGroup` / `MaxCredentialGroupMembers`; API `/fe/api/credential-groups(/{id})`; FE `CredentialGroup*` types/components; label **"Credential Groups"**. Bare "group" is never used for this entity (owned by model LB groups: `pkg/grouplb`, `kind=="group"`, `GroupModel*`). Not a 5th model kind. [research-store-api §c]
4. **Locked semantics** — same-provider members (validated at group write AND member add); group refs on **chat-kind models only**; delete-while-referenced = 409 naming referencing models + FE "inline members then delete" flow; no cycles (members are credential IDs only, schema/API cannot express nesting); no new model-side provider restrictions (picker rule parity); plain-credential-list regression byte-identical.
5. **Engine fan-out** — group writes fan out `engine.OnModelChanged(modelID, flattenedRefs)` to every referencing model (reverse lookup = fan-out list); without it selection runs stale until restart. In-memory group cache honors the cached resolver's zero-DB contract. [phase1 §c/§d.4]

## Cross-phase integration decisions (planner adjudications — BINDING)

| # | Decision | Ruling |
|---|---|---|
| I-1 | **Model-form Test button with group primary** (phase2 note 2; phase1 gap) | **Backend expansion**: `handleTestModel` (`server.go:2053`) expands a group id via the same flatten helper (group → first flattened member). ⚠️ **Phase-1 addendum**: implementers MUST add this one task to WU-6 — it is not in phase1-plan.md today. FE delta stays one line. |
| I-2 | `CredentialRef` discriminator (phase2 OQ-2) | **Resolved: additive `group_id?: string`** — both lanes independently chose it. |
| I-3 | Group-list fetch ownership (phase2 OQ-1) | **Container owns the fetch** (badge map + used-by need it). |
| I-4 | Response conventions (phase2 OQ-3) | Follow credentials conventions exactly: POST → 201 + entity; PUT → 200; DELETE → 204/409. FE re-fetches the list after every mutation (its existing pattern). |
| I-5 | Member cap authority (phase2 OQ-4) | **Backend is authority**: `MaxCredentialGroupMembers = 16` (mirrors `MaxGroupMembers`) AND expanded model list ≤ `MaxCredentialRefs` 16 (the binding cap). FE mirrors as UX guard. 64KB envelope remains transport authority. |
| I-6 | used-by surface (phase1 §f vs phase2 §4) | **Group DETAIL response's `used_by` field is the authority** (phase1 §f: `GET /{id}` → 200 + used_by model IDs). Phase2's planned `GET /{id}/used-by` separate endpoint is **dropped** — FE reads `used_by` from the detail fetch (one-line adjustment). Credentials-tab membership badge stays FE-computed. |
| I-7 | Provider-change block on grouped credentials (phase1 refinement beyond literal lock) | **Endorsed** — blocks `UpdateCredential` provider changes while grouped; preserves the locked same-provider invariant at the source. |
| I-8 | Inline-then-delete (phase2 note 4) | **FE-driven** via existing model-save; inlined members inherit the group ref's weight; positions reindexed; NO backend endpoint (phase1 correctly builds none). |

## Test matrix → phase/gate map (consolidated)

| Commission matrix item | Proven in | Gate |
|---|---|---|
| Flattening correctness (mixed refs, dedup keep-first, ordering) | phase1 WU-4 (+ E-3, empty-group pins) | Pack A |
| Same-provider validation reject | phase1 WU-2 (group write + member add) | Pack A |
| Add/remove member → propagates to every referencing model's flat list | phase1 WU-3 (fan-out + reverse lookup) | Pack A |
| Deletion-while-referenced block + inline-then-delete | phase1 WU-3/WU-6 (409) + phase2 (FE flow) | Pack A + vitest + live (L-8) |
| Credential-delete extended guard (group OR model refs → 409) | phase1 WU-3 | Pack A |
| CRUD API | phase1 WU-6 | Pack A (`pkg/ui`) |
| Decorator-forward through-tests | phase1 WU-5 (E-1..E-8, exactly-one-delegation) | Pack A |
| AST pin — selection never sees group objects | phase1 WU-7 (AST-walk variant; import-guard unusable from `pkg/models`) + mutation probe | Pack A |
| FE tests | phase2 T-A..T-J | vitest |
| Regression pin — plain list byte-identical | phase1 WU-7 + phase2 T-J + live L-5/L-7 | Pack A + vitest + live |
| End-to-end live scenario (2 real creds + mixed model + rotation from both sources) | phase3 T-9 / L-* | Live (stdout `[credentiallb]`/`[DEBUG]` evidence channel; sticky-pin defeated via distinct conversation keys) |

Full detail: phase1 §h, phase2 §5, phase3 §4 (with coverage-closure check §4.4).

## Risk register (consolidated — full registers in each phase file)

| Risk | Sev | Owner phase / mitigation |
|---|---|---|
| **R-1 binary rollback breaks group-referencing models** (old binary drops `group_id` on unmarshal → `credential_id:""` → validator reject / `==1` 502 / selector `""` — **empirically reproduced**) | 🔴 | phase3: data-repair procedure (T-8.4), group-usage census, rollback rehearsal T-8.5, post-scenario "census empty" exit criterion §9.11 |
| **R-2 migration-033 activation trap** (registry is an ordered slice; append at `migrate.go:78` activates the SQL) | 🔴 | phase1 WU-1 trap callout; phase3 replaces the standing "migration diff EMPTY" gate with a **scoped** diff (exactly the 033 pair + append) |
| Decorator trap (new methods no-op behind `CachedModelsConfig`, prod-bound) | 🔴 | phase1 WU-5: interface + `strictSource` + forward + through-test per method (E-1..E-8) |
| Engine staleness on group edits (binding sites are model-write-driven) | 🔴 | phase1 WU-3 fan-out via reverse lookup |
| `RemoveCredential` guard hole (group-only refs escape in-use scan) | 🟠 | phase1 WU-3 extended guard (names both surfaces) |
| E-3 fast-path misfire (single group ref → `==1` branch → 502) | 🟠 | Seam placement + explicit pin (phase1 §d.3) |
| Naming-collision confusion (model "groups" vs credential groups) | 🟠 | Locked scheme + phase3 T-1.5 naming scan + FE label always fully qualified |
| Decision-lane merge surface (SettingsPage HIGH, types/tabs-test MEDIUM) | 🟠 | phase2 §7 disclosure + per-file resolution strategy; our lane ships first, conflicts resolved at their merge |
| CI gates nothing (`|| echo` Go-test swallow; no job timeout) | 🟠 | Real gate is local (phase3 T-2); early-stall watch + null-commit retrigger chain |
| Sticky-pin obscuring the rotation proof | 🟡 | phase3: distinct leading messages per request; single-source observation = FAILED proof, not weak pass |
| tsc baseline drift / static-area mutation | 🟡 | New clean files change nothing (verified mechanics); sha256 zero-mod proof; re-freeze procedure only-if |

## Ship runbook

See `phase3-plan.md` §5 (T-0..T-9): freeze tip → local release gate → land (explicit-SHA pushes, 8 ⛔ USER-AUTHORIZED GATE markers) → pre-deploy baseline + pre-flight dump → CI watch → image verify (marker `CREATE TABLE IF NOT EXISTS credential_groups`, negative control verified 0 on live `fd1649c`) → **daoduc first, primary last** → live scenario → rollback documented-before-deploy. GitOps pins move together; `main` untouched. NEVER push without explicit user instruction.

## Open questions carried to the caller (user decisions at ship time)

1. **Pre-authorize the primary-side mutation pair list** — same-provider credential pair not serving ensemble traffic, decided before deploy, not mid-deploy (phase3 §8 item 2).
2. **Authorize the backup-line rollback rehearsal (T-8.5)?** Recommendation: YES — it is the only way R-1's repair is proven rather than asserted (~30s rollback on idle standby). If declined, the ship record must state the repair is unproven (§8 item 3).
3. **Confirm the DB-dump operator** for T-4.3 (procedure is path-only; dump artifact must land OUTSIDE any tracked repo; never echo credential values) (§8 item 4).

Planner-adjudicated defaults (caller may override at ship): full mixed-ref rotation proof runs on the **backup**; the **primary** gets a reduced proof on a **non-ensemble** mixed-ref model so ensemble traffic is untouched (§8 item 1).

## Boundary

This directory is a PLAN. Implementation hands off to the coding lane (giter/coder) under the same worktree claim; phase3's pushes/deploy run only under explicit user authorization per gate marker.
