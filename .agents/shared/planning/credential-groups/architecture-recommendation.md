# Credential Groups — Architecture Recommendation (stress-test of the approved draft)

Date: 2026-11-16 · Author: Architect (controller) — aggregation of 2 skill-equipped workers + 1 governor council
Worktree: `llm-supervisor-proxy-wt-cred-groups` · branch `feature/credential-groups` · plan under review `af4ca61` · code base `2b987bf` (= deployed v194 on both hosts)
Method: Worker A `data-flow-design` (claims 1+3, instance `cdc53814`), Worker C `structural-design` (claims 4+5+6, instance `5cee74d1`), Council `resilience-design` 2 models/2 consensus (claim 2, governor `3d174c64`). All load-bearing findings spot-verified by the architect against the worktree before being made binding.
Status: **COMPLETE — plan endorsed with modifications.** The locked representation and the management-only flattening design stand; the seam's *coverage*, the decorator's group cache, the proxy's raw-list gates, and the rollback runbook need the addenda below (A-1…A-15).

---

## 0. Verdict summary

| # | Claim | Verdict | The delta |
|---|-------|---------|-----------|
| 1 | Flattening seam: one helper, 8 sites | **MODIFY** | Shape and placement endorsed; coverage incomplete — 2 missed store/decorator sites (legacy 5-tuple resolver pair) + an entire *class* of consumers outside the seam: `pkg/proxy` + `pkg/ui` gates reading the RAW ref list (A-1, A-2) |
| 2 | `GroupID` rides `credentials_json`; accept R-1 with repair+rehearsal | **ENDORSE (modified-a)** | Option (b) sentinel is dead on evidence; (c) dual-write structurally broken; (a) hardened with council amendments M-1…M-5 (§2) |
| 3 | Cache coherence: fan-out + E-1..E-8 decorator forwards | **MODIFY** | E-1..E-8 surface right, but the decorator group cache it presupposes does not exist and has no priming task; lock discipline for `m.groups` unspecified (crash/deadlock fork); fan-out surface needs two corrections (A-3, A-4, A-5, A-15) |
| 4 | Migration 033 additive; `members_json` JSON-on-row; scoped-diff gate | **ENDORSE w/ amendments** | Membership shape and DDL endorsed; PG-gated twin mis-scoped (cannot run under the phase3 gate); `len(migrations)-1` test loops change meaning at 33 entries; scoped gate needs an explicit retire-after-train clause (A-6) |
| 5 | `/fe/api/credential-groups` CRUD + used_by + 409 | **ENDORSE w/ corrections** | Manager-side reverse lookup and `errors.Is` mapping endorsed; JSON envelope must cover 405/missing-id too; `used_by` is specified twice inconsistently across plan files; carry `used_by` server-side on the LIST as well (A-9, A-10) |
| 6 | FE container + left sub-nav, decision-lane idiom, tsc parity | **ENDORSE w/ corrections** | Decomposition endorsed (spec-only precedent is acknowledged); container must forward `status`; `w-40` precedent claim is false; static zero-mod proof command hashes 3 of 4 files; new test files are inside tsc scope — add a per-file tsc delta check (A-12, A-13) |

---

## 1. Claim-by-claim verdicts

### Claim 1 — THE FLATTENING SEAM: MODIFY (shape endorsed, coverage extended)

**Endorsed.** All 8 claimed sites verified at their exact anchors (`store.go:789` startup rebind, `:864`/`:867` bus-drain, `:1632`/`:1768` Add/UpdateModel engine notify, `:1834` RemoveModel nil, resolvers `:2543` and `:2679` with `==1` fast paths `:2588-2590`/`:2729-2730`). The E-3 mechanism is real: a raw single group ref takes `==1`, resolves `Credentials[0].CredentialID` (= the empty string, per the locked representation) → `GetCredential("")` → nil → `ok=false` → 502. Flatten-before-len-branches is provably required. Flatten-at-read is correctly rejected — the FE GET→PUT round-trip persists the posted row verbatim (`server.go:1235`/`:1689`) and the decorator deep-copies read rows (`modelscache/models.go:199+`). Narrower chokepoints fail: binding-sites-only leaves the `==1` fast paths broken; a single resolution wrapper cannot exist because the legacy 5-tuple resolver has two independent implementations.

**Modified — two missed resolution sites (A-1).** The legacy 5-tuple `ResolveInternalConfig` reads `PrimaryCredentialID()` raw at `store.go:2458` (`PrimaryCredentialID` = `Credentials[0].CredentialID`, `models/config.go:437-442`), and its decorator twin `CachedModelsConfig.ResolveInternalConfig` (`modelscache/models.go:750-770`) is **self-contained** — it never delegates to the store (it exists so `DetectProvider` survives DB outages), so fixing only the store side leaves production broken. Failure mode is **not** a 502: the sole production caller `normalizers.DetectProvider` (`normalizers/config.go:27`, called per streaming attempt at `race_executor.go:642`) degrades to `"unknown"` by design → provider-keyed stream normalizers silently inert for group-primary models. Silent, hence invisible to the 502-oriented regression pin.

**Modified — a consumer class the census missed entirely (A-2, the highest-value defect).** `pkg/proxy` and `pkg/ui` gate behavior on the **raw** `modelCfg.Credentials` list, outside any resolution seam:

| Site | Read | Effect for a group-primary model (raw len 1) |
|------|------|----------------------------------------------|
| `race_coordinator.go:1827` | `len(modelCfg.Credentials) <= 1` | Credential-failover eligibility = false |
| `race_coordinator.go:1845` | budget `< len(Credentials)-1` | Failover budget = **0** |
| `race_executor.go:257` | `credBelongs` membership over raw refs | Reselect of a group member rejected as "wrong-model spawn" |
| `race_executor.go:309/:322` + `preferUnmarkedUpstreamCredential` `:306-340` | `< 2` gate + raw candidate scan | Skip-cache reorder never runs; group members never reorder candidates |
| `internal_handler.go:259`, `handler_anthropic.go:876` | rotation gates | Rotation off |
| `race_executor.go:59-77` `raceIntProviderIsMiniMax` | `PrimaryCredentialID()` raw | MiniMax reasoning split gate skipped (MiniMax in active use) |
| `server.go:1046/:1089/:1168/:1540/:1572/:1633` | `GetCredential(Credentials[0])` ×6 | 9router-primary 400-guard silently bypassed |

Net effect of missing these: a rate-limited group member **never rotates to its sibling** — the headline behavior of the commission ("add the account once, every model follows") silently fails while all resolution paths return 200. Binding requirement A-2.

**Consumer census otherwise closed.** Verified converging or N/A: race_executor `:477` (engine-issued IDs), `:491`/`:516` (covered resolvers), `handler_anthropic:783`, `internal_handler:224`, imggen + decision (cmd/main.go wiring to the covered seam), peak-hour swap (pure model-name substitution inside `resolveWithCredential`, `store.go:2475-2500`), usage metering (no credential keying), stickyorder, mcp, `GetFallbackChain`, `Save()` (no-op delegate). **`pkg/ultimatemodel` no longer exists** — blueprint stale. JSON twin (`models/config.go:1189/:1253`) is legacy/test-only and the plan correctly rejects group refs there (T-10).

### Claim 2 — DATA SHAPE / R-1: ENDORSE as **modified-a** (council, 2/2 consensus)

Keep the locked representation `{"credential_id":"","group_id":"g1","weight":N,"position":M}` (one `omitempty` field). Dominant axis: **Risk** — (a)'s rollback failure is loud, deterministic, bounded, rehearsal-provable; (b) and (c) buy transient rollback-window convenience for permanent debt, and (c)'s failure mode is *silent wrong serving* — strictly worse than a pageable 502 on the substrate carrying every ensemble turn.

- **(b) sentinel `"group:<id>"` — dead on evidence, not taste.** The old binary's write-path validator checks credential **existence**, not just non-emptiness (`store.go:1945-1946` `"references non-existent credential"`) → the sentinel is rejected exactly like `""` (same uneditable-model outcome, more misleading forensics), and **no** resolution path skip-and-falls-back on a `GetCredential` miss (`:2588-2593`, `:2607-2612`, cached `:2737-2742`). Sentinel = permanent wire-format tax for zero rollback benefit.
- **(c) dual-write/materialized — structurally broken.** The FE GET→PUT round-trip persists what it displays: same-array materialization keeps the poison ref or negates the feature; shadow-copy materialization creates chronic divergence (any old-binary edit updates one copy → roll-forward silently serves stale membership or destroys grouping), plus N-model write amplification per group edit.
- **(d) hybrids reduce to (b) or (c).** No flag day exists to end a transient dual-write.

Full rollback story with the five council amendments: §2.

### Claim 3 — CACHE COHERENCE: MODIFY

**Endorsed.** The decorator trap is real (`strictSource` at `modelscache/strict.go:19-34`, compile assertion `outage_test.go`; production binds the decorator, `cmd/main.go:187`, `CACHE_LAYER_ENABLED` default ON `:170`) — a manager-only method silently no-ops in prod while raw-manager tests pass. The E-1..E-8 method-by-method surface (interface + strictSource + forward + through-test + cache strategy) is the right mitigation, and the plan's "no decorator resolution changes" claim holds (resolution delegates to the covered store resolver, `models.go:737`).

**Modified — four gaps:**

1. **The decorator group cache does not exist** (grep `groupsByID|groupsSnap` in `pkg/modelscache`: zero hits) and no task primes it. E-1/E-2 presuppose `c.groupsByID`/`c.groupsSnap`; `strictSnapshot` (`models.go:179-198`) reads only models/enabled/creds. Without an explicit priming + reconcile extension, production `GetCredentialGroups` returns **empty forever** (A-4).
2. **Lock discipline for `m.groups` is unspecified — crash/deadlock fork** (A-3). Flatten is called at sites holding `m.mu.Lock()` (AddModel/UpdateModel engine-notify span) → `m.mu.RLock` inside flatten deadlocks; lock-free → fatal concurrent-map read on the resolve-hot path (`:2543`/`:2679`) — a process crash on the critical substrate. Additionally `GroupsContainingCredential` called from inside `RemoveCredential`'s `m.mu.Lock()` self-deadlocks if it takes `m.mu.RLock` (Go RWMutex non-reentrant; documented precedent `store.go:2076-2079`).
3. **Fan-out premise correction:** today's code has **no credential-UPDATE refresh chain** — `publishCredentialsChanged` callers are model writes only (`:1633`/`:1769`); credential DELETE refreshes via direct `engine.OnCredentialDeleted` (`:2416`). This is correct by design (engine holds refs only; key material is fresh-read at resolution), so api_key rotation needs no fan-out — do not build on the phantom mechanism. The plan's c.6 "reuse" is fine for what it actually claims (group writes publishing per-model events).
4. **TOCTOU:** the stale-overwrite window exists only if fan-out lands outside `m.mu` — the fix is the A-3 gate (fan-out inside the write span). The drain path is self-healing in the convergence direction (re-reads `GetModelNative` + current group state per event, `:843-869`); orphan-binding eviction on shrink (`engine.go:383-393`) is exactly the right semantics for flattened-list shrinkage.
5. **Interface additions are a compile blocker beyond the decorator** (A-5): the JSON twin `*models.ModelsConfig` asserts `models.ModelsConfigInterface` (`race_executor_shadow_reasoning_test.go:92`) and is constructed as `Server.modelsConfig` in tests — it must grow stub implementations of all 8 new methods or the tree does not compile. Unscheduled in phase1.

### Claim 4 — MIGRATION + MEMBERSHIP: ENDORSE with amendments

**Endorsed.** Registry mechanism verified (ordered slice, 32 entries, trap comments ×4, append at `:78` = activation). 033 DDL mirrors the `005_add_credentials.up.sql` house shape exactly; `.down` = `DROP TABLE IF EXISTS` matches 022+ convention (8 down files). Membership as `members_json` ordered `[]string` is right: "no join tables anywhere" holds (`credentials_json` 028, `members_json` 031; codecs `store.go:597/:625/:641/:652`); `ModelsUsingCredentialGroup` scans `models.credentials_json` independent of membership shape; `GroupsContainingCredential` reads the in-memory map; JSON array order is free (join table would need a position column); scale is trivial (N groups × ≤16). Accepted trade-off to state explicitly: JSON-on-row has no DB-level referential integrity on membership (same property as `credentials_json`) — mitigated by the T-16 guard + A-8 fail-closed flatten. Locked representation endorsed: verbatim round-trip real (`server.go:1235`/`:1689`, Position re-stamp `:753`), deep-copy survives `GroupID` for free (both copy paths are `copy(dst, src)` over the value struct — `models/copy.go:174-180`, `modelscache/copy.go:24-31`; **research-seam.md §e.7 is factually wrong on this** — A-14).

**Amendments:**
- **PG-gated twin mis-scoped (A-6).** The 028 PG test exists because 028 is the repo's *first file-level BEGIN…COMMIT* migration (`migrations_pg_test.go:7-13`); 032 also uses file-level transactions and has **no** PG twin. A single `CREATE TABLE IF NOT EXISTS` needs none — and a `pg_integration`-tagged test would never run under phase3's plain `go test` anyway (a gate that cannot fail is not a gate). T-5 → SQLite-only + note; real PG coverage would need a real PG step.
- **`len(migrations)-1` loops change meaning (A-6).** Three test loops (`migrations_test.go:140/:251/:522`, comments self-document the exclusion) exclude only the *latest* entry; growing 32→33 makes them run 001..032 (032 is a data-only UPDATE on `models`) and may break 028-fixture preconditions mid-implementation. Re-anchor the bound deliberately; do not "fix" fixtures by weakening them.
- **Scoped-diff gate endorsed with retirement (A-6).** T-3.6's scoped diff is the right inversion of the standing empty-diff rule — but it must be declared **train-scoped and retired with 033** (next train: "no migrations beyond 033", or re-scope to 034). Without that, train 034 inherits an ambiguous standing rule.
- **Shadow column (A-7).** `models.credential_id` is Go-bound from `PrimaryCredentialID()` (`store.go:1592/:1729`); **no production SELECT reads it** (`querybuilder.go:194-260` omits it; `dbModelRow` has no such field) — write-only for legacy/external readers. A group-ref-first model writes it **empty** (not corrupted, not the group id — keep it that way). The R-1 repair must also rewrite it to the flattened primary (§2 step 3), or the repaired row is half-valid for exactly the population that reads it.

### Claim 5 — API SURFACE: ENDORSE with corrections

**Endorsed.** Route registration pattern (`server.go:196-197`), status mapping (POST→201/PUT→200/DELETE→204-or-409), 64KB `MaxBytesReader`, no-secrets DTO, manager-side reverse lookups (precedent: `RemoveCredential`'s in-manager scan `:2384-2392`; the handler has no DB access; the fan-out consumes the same lookup), `errors.Is` sentinel mapping over the legacy `"in use"` string match (house precedent `server.go:1732/:1822-1825`; `ConfigError` is a comparable pointer so `errors.Is` works — and the sentinel is already wrapped, `store.go:2389`), FE-driven inline-then-delete with no backend endpoint (FE has `used_by` + members + `onUpdateModel` + weight inheritance), no change to `GET /fe/api/credentials/{id}` (badge FE-computed), auth inherited automatically (`/fe/api` Prefix on the protected ingress, `ingress-protected.yaml:46` — no chart change, `k8s/` diff stays empty).

**Corrections:**
1. **Envelope overstatement (A-9):** the credentials precedent mixes `http.Error` **text/plain** bodies at `:2529/:2538/:2654` into the JSON convention. The new endpoints must use the bare `{"error"}` envelope at *every* error site including 405/missing-id — the FE's `res.json()` throws `SyntaxError` on text/plain. FE relies on **status codes only**, never message text.
2. **`used_by` specified twice, inconsistently (A-10):** plan-overview I-6 (binding: `used_by` on the detail response; `GET /{id}/used-by` dropped) vs `phase2-plan.md:298/:301/:319/:338/:390/:422/:427` (still builds `getCredentialGroupUsedBy` against the dropped route). If uncorrected the FE 404s and the 409 delete-block flow renders an empty model list — the documented recovery becomes unperformable. Improvement adopted: carry `used_by` server-side on the **list** too (cheap at N-group scale) so the badge map never derives from the 15s-TTL models cache; groups fetched uncached (mirror the credentials functions).
3. Trailing-slash registration: ServeMux prefix-subtree requires the literal `/fe/api/credential-groups/` for the `{id}` handler (precedent `server.go:197`) — confirm registration strings.

### Claim 6 — FE STRUCTURE: ENDORSE with corrections

**Endorsed.** Container granularity, conditional mount/unmount, no router below SettingsPage, children own headings, `CredentialGroup*` naming (`Group*` namespace verifiably taken: `types.ts:182-192`), `invalidateModelCaches()` discipline (module-private, same-file additions work), the 10 suites are house-idiomatic (vitest globals:false + imports from 'vitest', `vi.mock`+`importOriginal`, URL-routed fetch stubs, `makeProps`, happy-dom traps correctly identified). tsc reasoning "new clean files don't move the 19/9 baseline" is correct (verifier asserts totals + per-file key set + counts, `tsc-baseline.test.ts:139-155`).

**Corrections:**
1. **Container must forward `status`** (`A-12`): `SettingsPage.tsx:649` passes only `setStatus` today; `CredentialsTab` takes both (`CredentialsTab.tsx:7-8/:28`). The mount move must not drop the prop.
2. **`w-40` precedent is false** (grep: zero `w-40` tokens in `src/`; `EventLog.tsx:174-178` is a colour map). Cosmetic — use the class, drop the citation.
3. **`MultiCredentialEditor` row-0 provider re-derivation is the single most load-bearing FE detail** (`isOptionDisabled` `:70-95`, `primaryProvider` from `credentials[0]?.credential_id` `:65-68`): when row 0 is a group ref the lock must re-key on the group's provider or it silently disappears. Member-overlap duplicate detection stays warn-only FE-side and is a **backend invariant** (R-6 dedup keep-first) — state it as such, not as an FE gap.
4. **`handleTestModel` task is missing from phase1** (confirmed: no task; only plan-overview:47 flags it) — A-11.
5. **New FE test files are inside tsc scope** (`tsconfig include: ["src"]`, `noUnusedLocals/Parameters`; only 2 of 22 existing test files carry `@ts-nocheck`) — type-clean or the 19/9 verdict breaks. Add a pre/post per-file tsc delta check on the 9 baseline files: an *accidental fix* of a frozen error in `CredentialsTab.tsx`(1)/`ModelForm.tsx`(1)/`SettingsPage.tsx`(6) fails the gate with a confusing verdict (A-12).
6. **Static zero-mod proof is broken (A-13):** `sha256sum pkg/ui/static/**/*` under default bash (globstar off) hashes **3 of 4 files** — silently omitting `index.html`, the embed entry point (`server.go:88`). Verified by execution in this worktree. Use `find pkg/ui/static -type f | sort | xargs sha256sum`.
7. **Merge disclosure upgrade:** both lanes add a container + left vertical nav to the same SettingsPage area and new types to the same `types.ts` cluster — the merge is *semantic*, not merely textual; record the decision lane's new-file names and reconcile the `border-l-2`/`aria-current` idiom as a shared convention at merge time.

---

## 2. Recommended rollback story (R-1) — modified-a

**Standing invariant (the real defense):** groups adopted only in attended windows; after any group usage, T-9.9 cleanup (inline-then-delete) drives the **census empty**, so the default rollback is `helm rollback` (daoduc) / prior-tag rollout (primary) **alone**. The repair is the contingency, not the plan of record.

**Council-certified failure signature (re-verified in code by the architect):** under the old binary, group-only models → deterministic 502 every request + FE edit rejected (`credential_id is empty`). Mixed models → **not graceful degradation**: the `|| credID == ""` guard exits *before* the dangling-ref heal (`store.go:2608-2610`, cached `:2738-2740`) → no cooldown seeds, nothing excludes `""` → first fresh pick landing on the `""` ref pins the conversation (binding capture, `engine.go:344-346`, `newlyBound=true` with empty `cred=`; 24h sliding TTL) → 100% failure for that conversation until repair or restart; restart re-rolls but reproduces. `[LB-FAILOVER]` does **not** appear; the heal WARN's absence is not a health signal.

**Triggers (classify first):** T1 group-model resolution defect on the NEW binary → rollback first, repair second. T2 plain-list regression → Class 1/2. T3 infra/CVE revert → controlled: repair first if census non-empty, then rollback clean. Re-derive tier-1/tier-2 anchors at trigger time (a rollback mints a revision; stale numbers lie).

**Census — first step of the runbook itself, raw SQL only, BOTH hosts (separate DBs), never the API** (the old binary's parse drops `group_id` → API census = silent false negatives):
```sql
SELECT id, name FROM models WHERE credentials_json LIKE '%"group_id"%';
```
Dialect-neutral, no false positives (`omitempty` keeps direct refs clean), no false negatives by construction. Empty → rollback alone is safe. **Before any rollback, capture** the census artifact + `credential_groups` definitions (ids, `members_json`, providers — never key values): the repair's expansion source is that artifact/the table, not the old binary's API (it has no groups endpoint).

**Repair (per census model, under the OLD binary):**
1. Read the model row + each referenced group's members **from the DB**.
2. Expand refs position-ordered; group ref → its members at that position, weight = the group ref's weight, positions reindexed; **dedup keep-first by credential_id (M-1 — mandatory)**; ≤16 refs, weight>0, same-provider.
3. **Primary path:** `PUT /fe/api/models/<id>` with the full body + expanded credentials → 200. Passes the old validator (existence `:1945`, duplicates `:1948`, weight, provider parity) **and** rebinds the engine + atomically evicts every `""` binding (surviving-map filter, `engine.go:388-394`). Also rewrite the derived shadow `credential_id` (A-7) via the same PUT (it re-derives from `PrimaryCredentialID()`).
4. **Fallback (API unavailable only):** scoped `UPDATE models SET credentials_json=…` — bypasses validators, so the constructed JSON must be validator-clean (no dups: they silently double weight) — **plus mandatory `kubectl -n llmproxy rollout restart`** on that host (len≥2 selectors and stale `""` bindings survive otherwise).
5. Verify per model: `/v1/models` lists it; one probe inference → 200. All census models green = repair exit.
6. `credential_groups` table + `members_json` untouched → repair reversible and idempotent; the orphaned table is harmless under the old binary (forward-only runner never re-examines applied versions; old binary boots clean on a rolled-forward DB).

**The five amendments (fold into the runbook before ship):**
- **M-1** repair expansion must **dedup keep-first** — the OLD validator rejects duplicate refs (`store.go:1948-1950`) while direct+group overlap is legal-by-design in the new binary (R-6). Without it the incident-time PUT 400s exactly when needed; SQL without dedup silently doubles the overlapped credential's weight. T-8.5 must exercise one overlap case.
- **M-2** the census is the **first step of the rollback runbook**, not a post-scenario artifact — it is point-in-time; any group-ref edit after the last census silently voids "rollback = helm alone".
- **M-3** **PUT-primary / SQL-fallback + mandatory rollout restart** if SQL is used (engine-mechanical, not preference — see repair step 3/4).
- **M-4** census is **raw SQL only — never the API** under the old binary.
- **M-5** **capture** census + group definitions before any rollback; repair expansion reads that artifact/the table, never the old binary's API.

**Rehearsal (T-8.5 — leader-AUTHORIZED, daoduc only, BEFORE any primary deploy):** during the backup soak window — throwaway group (2 real same-provider creds) + throwaway **group-only** model + one **mixed/overlap** case (M-1) → confirm serving on the new binary → `helm rollback` to the prior per-SHA revision → **observe the exact R-1 signature** (group-only 502 + empty-`cred=` `newlyBound` log; mixed conversation pinning; FE edit 400; census SQL returns exactly the throwaways) → repair via **PUT** and **once via SQL + restart** (fallback proof), including the overlap case → probe 200 → roll forward → delete throwaways → census re-run **must be empty**. Evidence: timestamped curl in/out + status codes, `helm history` + pod `imageID` digests, census outputs before/after, pod-direct port-forward **4321** (never 8080), zero secret material. **Gate #17 stands: if the repair cannot be proven executable, the train does not proceed to primary.** Step 0 of any live execution: verify per-host DB connectivity + engine (all SQL above is deliberately dialect-neutral).

**GitOps pin-revert obligation (T-8.6):** after **any** rollback, revert **BOTH** pins together in `data-center-scripts` (backup `values-pinned.yaml`, primary `deployment.yaml`) to the rolled-back tag+digest with ledger comment, via pin-branch → `--no-ff latest` → push — ⛔ user-gated: **pre-negotiate that push authorization now, not mid-incident**. Never leave a pin pointing at a non-live revision.

**Residual risks (severity-ordered):** 🔴 R-1 window real and sharper than the plan's text (per-conversation sticky capture, no self-heal); 🔴 repair-PUT 400s on overlap models if M-1 omitted; 🟠 census staleness/methodology drift (M-2/M-4); 🟠 repair parity — the new-binary flattening is plan-only; if implementation drifts from locked semantics, re-derive the repair (phase3 A-1); 🟡 SQL-path rebind gap (superseded by PUT-primary; restart rule stands); 🟡 unplanned-rollback window = stalled agent turns; 🟡 rehearsal rot — re-rehearse whenever the census transitions empty→non-empty; 🟢 post-repair grouping loss (cosmetic, `members_json` preserved); 🟢 orphaned table; 🟢 sticky re-roll on restart reproduces rather than cures.

**Flipping assumption (the only one):** group refs remain a rare, operator-managed state — the census-empty steady state is maintainable (T-9.9 discipline holds) AND every rollback window includes an operator who can execute census + repair within an acceptable MTTR (minutes, proven by T-8.5). If either half breaks (mass group adoption, or a requirement for unattended auto-rollback), (a)'s contingency violates the recovery objective and **(c)'s shadow-materialized form must be re-argued** with its chronic dual-copy hazard consciously accepted. **(b) can never flip back in** — its enabling premise (old code tolerates unknown ids) is verified false, not assumed.

---

## 3. Additional architectural risks the plan missed

Severity-ordered; each is binding via the addendum shown.

1. 🔴 **Proxy/UI raw-list gates defeat the headline behavior silently** (A-2) — failover budget 0, rotation off, reorder off, MiniMax split gate off, 9router guard bypassed; all paths still return 200, so no regression pin catches it unless written.
2. 🔴 **Decorator group cache absent + unprimed** (A-4) — production group reads empty forever under default CACHE_LAYER_ENABLED.
3. 🔴 **`m.groups` lock discipline fork: deadlock or fatal concurrent-map read** (A-3) — the latter is a process crash on the resolve-hot path of the critical substrate.
4. 🔴 **Interface growth breaks the JSON twin's compile assertions** (A-5) — hard compile blocker, unscheduled.
5. 🟠 **Mixed-model rollback signature is sticky conversation capture, not degrade** (§2) — the plan's "502 for that model" understates the mixed case.
6. 🟠 **`len(migrations)-1` test-loop semantics shift at 33 entries** (A-6).
7. 🟠 **`used_by` route contradiction across plan files** (A-10) — the 409 recovery flow is unperformable if phase2 is implemented as written.
8. 🟠 **Flatten must fail closed on provider divergence** (A-8) — direct-SQL provider edits bypass T-17; expanding a mixed-provider group would break the provider-match invariant the whole credential-LB layer assumes.
9. 🟠 **Shadow column half-repair** (A-7) — repair must rewrite it; and it must never bind a group id.
10. 🟡 **Static zero-mod proof omits `index.html`** (A-13) — the exact R-18 scenario the gate exists to catch.
11. 🟡 **tsc baseline is count+file-set sensitive to accidental fixes and to new test files** (A-12).
12. 🟡 **Text/plain `http.Error` bodies on `/fe/api/*` break `res.json()`** (A-9).
13. 🟡 **research-seam §e.7 is factually wrong about the copy paths** (A-14) — must not drive representation changes; plan text already correct, preserve it.
14. 🟢 Stale doc comments on the copy funcs ("a ref carries only credential_id/weight/position") — update to prevent a future field-enumeration "fix" that actually breaks `GroupID` survival.
15. 🟢 `AddCredentialGroup` without fan-out is reachable via direct SQL/DB-restore/R-1-repair dance (A-15) — fan out on Add too; one reverse-lookup on a rare admin op.

---

## 4. Binding addenda for the implementer (I-1..I-8 style)

| # | Addendum | Ruling |
|---|----------|--------|
| A-1 | **Seam = 9 store sites + 1 decorator site** | Add `ResolveInternalConfig` (`store.go:2444`, flatten before the `PrimaryCredentialID()` read) AND the decorator twin `CachedModelsConfig.ResolveInternalConfig` (`models.go:750`, flatten cache-side from the decorator group mirror — it never delegates; it exists for DB-outage coverage). Failure if missed is silent (`"unknown"` provider, MiniMax gate off), not 502 — pin with tests asserting DetectProvider returns the real provider for a group-primary model. |
| A-2 | **Every raw-credential-list gate outside the seam consumes a flattened list** | Inventory (all verified): `race_coordinator.go:1827/:1845`, `race_executor.go:257/:309/:322` + `preferUnmarkedUpstreamCredential:306-340` + `raceIntProviderIsMiniMax:59-77`, `internal_handler.go:259`, `handler_anthropic.go:876`, `server.go:1046/:1089/:1168/:1540/:1572/:1633`. Serve them from a flattened view (the planned E-8 accessor extended to the proxy path, or a cached flat list invalidated by the same fan-out). Binding tests: single-group-ref model → failover eligible, budget ≥ members−1; reselect of a group member passes `credBelongs`; reorder runs. |
| A-3 | **Lock discipline** | `m.groups` gets a dedicated `groupsMu sync.RWMutex` (flatten takes RLock everywhere; group writes Lock at swap; zero interaction with `m.mu` ordering). Group-write engine fan-out executes INSIDE `m.mu` write span (serializes vs model writes; kills the stale-flat-list last-writer-wins window). Internal lock-free accessors for membership lookups called under `m.mu.Lock()` (RWMutex non-reentrant; precedent `store.go:2076-2079`). |
| A-4 | **Decorator group cache: build + prime + reconcile** | Add groups to `strictSnapshot`/`applySnapshot`/`reconcileOnce` (boot priming + ≤60s heal parity). Inner `m.groups` is authoritative for flattening; the decorator group cache is a read-only mirror rebuilt on write-through + reconcile. Without this, production group reads are empty forever (default CACHE_LAYER_ENABLED). |
| A-5 | **JSON twin implements the 8 new interface methods** | Stub implementations on `*models.ModelsConfig` (empty/nil; it already rejects group refs per T-10). Compile-time assertions exist (`race_executor_shadow_reasoning_test.go:92`; twin constructed as `Server.modelsConfig` in ui tests). Unscheduled in phase1 — schedule it or the tree does not compile. Re-check `strict.go:16-18` assertion after the interface grows. |
| A-6 | **Migration test + gate amendments** | (i) T-5 = SQLite-only; the 028 PG-twin mandate is file-level-transaction-specific and a `pg_integration`-tagged test never runs under phase3's plain `go test`. (ii) Re-anchor the three `len(migrations)-1` loops (`migrations_test.go:140/:251/:522`) deliberately for 33 entries — they will otherwise run 032 into the 028 fixtures; do not weaken fixtures. (iii) T-3.6's scoped diff is train-scoped, RETIRES with 033; next train's standing rule = "no migrations beyond 033" or an explicit 034 re-scope. |
| A-7 | **Shadow column rules** | `models.credential_id` stays derived from `PrimaryCredentialID()` — empty for group-ref-first models (never bind the group id); no production SELECT reads it (verified). R-1 repair step 3 rewrites it via the PUT re-derivation. Document "legacy readers see no credential" as intended. |
| A-8 | **Flatten fails closed on provider divergence** | At flatten time, expand only members whose provider matches the group's provider; skip + WARN on divergence. Covers direct-SQL provider edits that bypass T-17. Never expand a mixed-provider list into a same-provider model. |
| A-9 | **API envelope + error mapping** | Bare `{"error"}` JSON at EVERY error site of the new endpoints incl. 405 + missing-id (do not copy the credentials precedent's `http.Error` text/plain at `:2529/:2538/:2654` — FE `res.json()` throws). `errors.Is` sentinels; FE branches on STATUS CODES only. |
| A-10 | **`used_by` surface: one spec, server-side** | plan-overview I-6 is binding (`used_by` on detail; `/{id}/used-by` route dropped) — update phase2's 7 references to `getCredentialGroupUsedBy` before implementation. Improvement: include `used_by` in the LIST response too (server-side computed) so badges never derive from the 15s-TTL models cache; groups fetched uncached like credentials. |
| A-11 | **`handleTestModel` expansion task lands in WU-6** | Confirmed absent from phase1. Expand group id → first flattened member (`server.go:2053-2078`). FE delta stays one line (I-1). Without it the first user-facing flow dead-ends. |
| A-12 | **FE corrections bundle** | (i) Container forwards `status` AND `setStatus` to CredentialsTab (`:649` passes only setStatus today). (ii) Re-derive `primaryProvider` from the group's provider when row 0 is a group ref (`MultiCredentialEditor.tsx:65-95`). (iii) Member-overlap duplicate rule = warn-only FE, backend invariant (R-6). (iv) New test files are inside tsc scope — type-clean; add a pre/post per-file tsc delta check on the 9 baseline files (accidental fix of a frozen error = gate failure). (v) Drop the false `w-40` precedent claim. (vi) Register `{id}` routes with the trailing slash. |
| A-13 | **Static zero-mod proof command** | Replace `sha256sum pkg/ui/static/**/*` with `find pkg/ui/static -type f \| sort \| xargs sha256sum` — the glob provably omits `index.html` (3 of 4 files hashed; verified by execution). |
| A-14 | **Copy-path fact preserved** | Both copy paths are `copy(dst, src)` over the value struct → `GroupID` survives with zero edits. `research-seam.md` §e.7 ("must be added to both") is wrong — do not act on it, and do not avoid the `GroupID` field because of it. Update the stale doc comments on the copy funcs. |
| A-15 | **Fan-out completeness** | (i) `AddCredentialGroup` fans out too (closes the direct-SQL/restore/repair-dance hole). (ii) Credential api_key rotation needs NO fan-out (engine holds refs only; no credential-update refresh chain exists today — verified callers `:1633`/`:1769` only; do not build on that phantom mechanism). (iii) The drain path self-heals (re-reads model + current groups per event). |

---

## 5. Plan-document corrections required (before/during implementation)

1. `phase2-plan.md` — replace the 7 `getCredentialGroupUsedBy` / `/{id}/used-by` references with detail+list `used_by` (A-10).
2. `phase1-plan.md` — add the WU-6 `handleTestModel` task (A-11); add the JSON-twin stub task (A-5); extend §d.2 site table to the 9+1 shape (A-1); add the A-2 proxy-view requirement to WU-4's scope; add the A-3 lock paragraph to §c; add A-4 to WU-5.
3. `phase3-plan.md` — fix T-2.5's hash command (A-13); scope T-5 SQLite-only + retire the scoped gate in the exit criteria (A-6); fold M-1..M-5 into T-8.4/T-8.5 text.
4. `research-seam.md` §e.7 — mark corrected by this document (A-14).
5. Plan premise correction: no credential-UPDATE refresh chain exists today (A-15.ii) — remove any text implying reuse of one for group writes beyond the per-model publish.

## 6. Confidence

**High.** Every binding claim above is code-cited in the worktree and the architect spot-verified the load-bearing anchors (legacy resolver pair, validator existence/duplicate rejects, sticky `""` capture, binding eviction, coordinator budget gates, migration-loop idiom, decorator cache absence, sha256 glob omission). The single flipping assumption is stated in §2 (census-empty maintainability + operator-in-loop MTTR). Assumptions NOT independently verified: per-host production DB engine (verify as step 0 of any live execution) and the not-yet-written new-binary flattening (repair parity depends on implementation matching locked semantics — phase3 A-1 covers re-derivation).

---

## Appendix — provenance

- Worker A `data-flow-design`, instance `cdc53814-6719-42c5-8987-2710af44abf2` — claims 1+3 (seam census, consumer inventory, lock map, fan-out surface).
- Worker C `structural-design`, instance `5cee74d1-0421-4617-9abd-99cf562ba1a4` — claims 4+5+6 (migration/membership, API, FE; discovered the A-2 proxy raw-list class and the A-4/A-5 decorator/twin gaps).
- Council `resilience-design`, governor `3d174c64-fe12-41bf-bf80-9f9d1dd6294a`, 2 models (agentic, coding), full consensus after one refinement round — claim 2 (modified-a, M-1..M-5, rollback story, flipping assumption).
- Architect verification passes: `store.go:1940-1952/:2440-2472/:1625-1640`, `modelscache/models.go:748-772`, `models/config.go:435-443`, `engine.go:340-350/:383-396`, `race_coordinator.go:1820-1850`, `race_executor.go:253-262/:305-325`, `migrations_test.go` loop grep, `pkg/modelscache` group-cache grep, static-tree glob-vs-find execution.
