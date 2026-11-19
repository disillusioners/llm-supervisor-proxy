# IMPLEMENTATION PLAN — AUTH COMMISSION: token permission model (`allowed_groups` + reach-based enforcement)

**Lane:** `feature/token-authz-allowed-groups` · **Worktree:** `/home/nea/Code/opensource-projects/llm-supervisor-proxy-wt-token-authz` · **Base:** `a3f0335` (BASE only — Phase 1 has LANDED on top; see REAL STATUS below).
**Authority chain:** the Emperor's ruling (§1.1 of `adr-token-permission-model.md`) > the ADR's normative rules D1–D16 > `decisions.md` Q1–Q13 recommended defaults (binding input) > this plan (execution detail).
**Companion artifacts (read, not duplicated here):** `adr-token-permission-model.md` (455 lines — the ruling), `decisions.md` (560 lines — the register, Q1–Q13 + R-1…R-10 + OI-1…OI-7), `research-authz-surfaces.md`, `research-runtime-plumbing.md`, `research-storage-api-fe.md`.
**REAL STATUS (gen-11 re-pin; MEASURED 2026-11-19T07:41:00Z):** branch `feature/token-authz-allowed-groups`; **base `a3f0335`**; **HEAD `1408e8b`** — ALL LANES LANDED (tester FINAL VERDICT PASS). **Chain: 22 commits incl. base (21 on top of base)** — the `1e65774` tip was 21 commits incl. base (20 on top); `1408e8b` (docs(tester): FINAL VERDICT PASS — token-authz gen-10 @ `1e65774`, 121-pin sweep clean) is the 21st commit on top of base. Enumerated:  `a3f0335` (base) → `2b9ab6c` (frozen token-scope contract) → `60d0db4` (Phase-1 completion: 034, admin API, PD pins) → `f9adbce` (empty/whitespace TEXT fail-closed + hermetic authz guard toolchain) → `b0d4875` (FE token-grant wire) → `5de3eb4` (allowed_groups picker + per-axis control) → `7ce1787` (static bundle rebuild) → `391f181` (Phase-2 per-layer enforcement, 4 lanes) → `e7ffcda` (chain/re-entry/per-member pins) → `156252a` (decorator degraded-branch fail-closed) → `1e423de` (C2 invalid flags in DTO + C3 grant-axis escalation) → `63e9665` (C2 BE pin + C3 FE pins) → `ce8f2f5` (W1 stale-claim grant validation) → `0be8d13` (W2 groupPick 403 conjunct) → `4be0a12` (F1 per-axis picker save + F2 DTO comment) → `81e8788` (gofmt drift + log/comment accuracy + guard-test hardening) → `eb137a1` (align stale empty-slice assertions with the conjunctive grant contract) → `135e1e8` (tester: release-gate report) → `f244b1d` (tester: shared-worktree provenance audit + gen-9 re-verification recipe) → `e502978` (tester: tree-cleanliness precondition for the gen-9 recipe) → `1e65774` (**land the 9 commission-named obligation pins** — PD-2/PD-7/PD-9/PD-10/PD-11; 4 test files, +1589). → `1408e8b` (docs(tester): FINAL VERDICT PASS — token-authz gen-10 @ `1e65774` (121-pin sweep clean); `.agents/tester/` only, 3 files, +55/−1 — docs-only, no source change). **Diff vs base (`a3f0335..1408e8b`): 97 files, +11710/−899.** **WORKING TREE CLEAN — no source-file edits pending** — `git status --porcelain` shows ONLY `.agents/` entries (`approver/active.md` M, `tidier/notes.md` M, approver tracking ??, this planning dir ??); **no stray temp test files remain — `pkg/decision/zz_probe_tmp_test.go` is verified ABSENT.** **Anchor convention:** every `file:line` anchor is **base-relative to `a3f0335`** unless marked; the anchor-shift table in `§0.1` gives the net `a3f0335..4be0a12` deltas (the later commits touch test/docs files plus the 4 test files of `1e65774`: `migrations_test.go`, `handler_group_test.go`, `handler_test.go`, `authz_claim_pins_test.go`; `1408e8b` touches `.agents/tester/` only ⇒ zero anchor shift).
**Corpus frozen at `a640a9d44bcf71ca…` (full manifest sha256 `a640a9d44bcf71ca940b0ba1705ae2d24df2f08801e2769d9c1cb896955fd02a`) — 2026-11-19, **fix pass C / gen-11 re-pin (cycle-4 council: 1 blocker + 3 ride-alongs — artifact-index counts + pin ledger refreshed to 121, REAL STATUS advanced to HEAD `1408e8b` with the tester FINAL VERDICT PASS)**. Superseded freeze hashes (NO LONGER REPRODUCE): `bdccbc2e…`, `c7efdccf…`, `6c52fe80…`, `f2673ded…`, `07b9f295…`, `a2d6ebe6…`, `cb2820c0…`, `e8f53029…`, `95fad8a4…`, `f68743d6…`, `e39dfc72…`, `36a20d82…`.** The 8 core docs are hash-pinned at this revision; `plan.md` itself is hashed EXCLUDING its own freeze line (line 7). **Reproduce:** `cd <corpus-dir>; { for f in adr-token-permission-model.md architecture-recommendation.md decisions.md phase1-plan.md phase2-plan.md phase3-plan.md plan-overview.md; do sha256sum "$f" | cut -d' ' -f1; done; sed '7d' plan.md | sha256sum | cut -d' ' -f1; } | sha256sum` — **byte-identical equivalent (verified): `grep -v 'corpus frozen at' plan.md | sha256sum`**; the freeze line MUST be line 7 of plan.md, and plan.md ends with a trailing newline so all exclusion forms agree byte-for-byte.
**Ship posture:** USER-ORDERED with explicit *"ship it; no approval hold"*. The standing grant's ask-back on auth changes is **DISCHARGED for this lane**. **There is no approval gate in this plan.** Migration `034` — including its data repartition — is a **code-shipped, in-house-framework migration** with direct shipped precedent (`pkg/store/database/migrations/sqlite/032_internal_only_data.up.sql`, a data-only convergence migration), not an ad-hoc live-store write: it is in-lane and is planned as an ordinary sequenced migration step (§5, §14).

---

## 0.1 Anchor-shift table (base → landed, MEASURED read-only)

Corpus anchors are `a3f0335`-base-relative. **Generation 1** measured with:
`git diff --numstat a3f0335 60d0db4 | sort -k1,1nr` (added/removed lines; net = added − removed).

| File (most-cited by the corpus) | +added | −removed | NET shift |
|---|---|---|---|
| `pkg/ui/handlers_token_test.go` | 522 | 5 | +517 |
| `pkg/store/database/migrations_test.go` | 471 | 18 | +453 |
| `pkg/auth/store_test.go` | 461 | 44 | +417 |
| `pkg/modelscache/tokens_test.go` | 261 | 4 | +257 |
| `pkg/authz/scope_test.go` (new) | 220 | 0 | +220 |
| `pkg/authz/guard_test.go` (new) | 175 | 0 | +175 |
| `pkg/ui/server.go` | 156 | 21 | **+135** |
| `pkg/auth/store.go` | 146 | 45 | **+101** |
| `pkg/auth/token.go` | 130 | 27 | +103 |
| `pkg/authz/scope.go` (new) | 125 | 0 | +125 |
| `pkg/auth/permission.go` (new) | 76 | 0 | +76 |
| `pkg/auth/token_test.go` | 74 | 71 | +3 |
| `pkg/modelscache/tokens.go` | 67 | 13 | +54 |
| `pkg/store/database/migrate.go` | 13 | 0 | +13 |
| `pkg/models/reach.go` (new) | 12 | 0 | +12 |
| `pkg/models/group_rewrite.go` | 8 | 0 | +8 |

**Generation 3 — NET `a3f0335` → `4be0a12` (the authoritative shift; `git diff --numstat a3f0335 4be0a12 | sort -k1,1nr`; 91 files, +9562/−870). Top shifts used by corpus anchors:**

| File | +added | −removed | NET |
|---|---|---|---|
| `pkg/proxy/authz_scope_test.go` (new) | 737 | 0 | +737 |
| `pkg/store/database/migrations_test.go` | 568 | 18 | +550 |
| `pkg/ui/handlers_token_test.go` | 522 | 5 | +517 |
| `pkg/auth/store_test.go` | 461 | 44 | +417 |
| `pkg/proxy/authz_chain_pins_test.go` (new) | 418 | 0 | +418 |
| `pkg/proxy/authz_guard_test.go` (new) | 413 | 0 | +413 |
| `pkg/ui/frontend/src/components/tokens/__tests__/TokenForm.test.tsx` | 368 | 63 | +305 |
| `pkg/store/database/store_group_scope_test.go` (new) | 306 | 0 | +306 |
| `pkg/ui/frontend/src/components/tokens/grantAxes.tsx` (new) | 288 | 0 | +288 |
| `pkg/ui/frontend/src/components/tokens/TokenList.tsx` | 275 | 93 | +182 |
| `pkg/authz/scope_test.go` (new) | 268 | 0 | +268 |
| `pkg/modelscache/tokens_test.go` | 261 | 4 | +257 |
| `pkg/proxy/handler.go` | 261 | 77 | **+184** |
| `pkg/proxy/handler_groups.go` | 253 | 20 | **+233** |
| `pkg/proxy/race_coordinator_test.go` | 231 | 61 | +170 |
| `pkg/authz/guard_test.go` (new) | 223 | 0 | +223 |
| `pkg/proxy/authz_scope_test.go`… see above | | | |
| `pkg/ui/frontend/src/components/tokens/__tests__/grantAxes.test.tsx` (new) | 222 | 0 | +222 |
| `pkg/store/database/store_group_rewrite.go` | 222 | 15 | **+207** |
| `pkg/proxy/handler_anthropic.go` | 191 | 34 | **+157** |
| `pkg/ui/server.go` | 187 | 36 | **+151** |
| `pkg/ui/frontend/src/components/tokens/__tests__/TokenList.test.tsx` (new) | 183 | 0 | +183 |
| `pkg/ui/handlers_token_flags_test.go` (new) | 168 | 0 | +168 |
| `pkg/decision/group.go` | 158 | 1 | +157 |
| `pkg/auth/token.go` | 154 | 27 | +127 |

**Use the NET column for base→HEAD anchor translation.** Generations 1/2 below are historical (per-commit) and remain for provenance.

**Generation 2 — `60d0db4` → `b0d4875`** (`git diff --numstat 60d0db4 b0d4875 | sort -k1,1nr`; 8 files, +492/−19):

| File | +added | −removed | NET |
|---|---|---|---|
| `pkg/ui/frontend/src/hooks/__tests__/useApi.token-permission.test.ts` (new) | 219 | 0 | +219 |
| `pkg/ui/frontend/src/hooks/useApi.ts` | 50 | 7 | **+43** (corpus anchors `:898-934` shift) |
| `pkg/authz/guard_test.go` | 49 | 1 | +48 |
| `pkg/authz/scope_test.go` | 48 | 0 | +48 |
| `pkg/auth/token_test.go` | 39 | 4 | +35 |
| `pkg/ui/frontend/src/types.ts` | 32 | 1 | **+31** (corpus anchors `:240-249` shift) |
| `pkg/authz/scope.go` | 29 | 4 | **+25** (the `:47-51` flag site shifts) |
| `pkg/auth/token.go` | 26 | 2 | +24 |


**Generation 4 — `4be0a12` → `1e65774`** (`git diff --numstat 4be0a12 1e65774`; the late commits touch **test + docs files only** — no production source): `eb137a1` (align stale empty-slice assertions), `135e1e8`/`f244b1d`/`e502978` (tester gate reports + recipes), **`1e65774` (the 9 commission-named obligation pins — 4 test files, +1589: `pkg/store/database/migrations_test.go`, `pkg/decision/handler_group_test.go`, `pkg/decision/handler_test.go`, `pkg/proxy/authz_claim_pins_test.go`)**. **Net vs base (`a3f0335..1e65774`): 97 files, +11656/−899.**

Whole-diff gen-1: **42 files changed, 3179 insertions(+), 325 deletions(-)** (`git diff --stat a3f0335 60d0db4 | tail -1`). Use this table to translate a base anchor into a landed one (add the net shift for same-block line shifts); **re-verify against the landed file before citing a shifted line as fact**. Post-Phase-2/3 SHAs land later (fix pass B) and will require a second table.

---

## 1. Objective, scope, non-goals, invariants

### 1.1 Objective

Replace the **string-level, site-by-site** token scope check with a **reach-based, list-property** authorization model, and add the `allowed_groups` permission axis, in one architectural change:

1. **Fix the live defect** — the decision **GROUP** arm returns at `pkg/decision/handler.go:325` *before* the allow-list at `:340-354`, so a decision-group request is served with **zero** scope evaluation (auth OK, scope unchecked). `pkg/decision/group.go` contains no scope concept at all (`authToken` is a parameter at `:96` whose **only** consumer is metering at `:341`).
2. **Add `allowed_groups`** — a token grant that reaches *the group and its current members* (writable on the token via POST/PATCH — there is **no PUT route** for tokens, `pkg/ui/server.go:227-228`; resolved at request time).
3. **Exclude out-of-scope candidates from every candidate list, per layer** — "load balance, fallback layer … exclude the model from the list" (Emperor, verbatim).
4. **Close the fail-open** — a malformed/unreadable grant row currently **grants full access** while logging that it denies (`pkg/auth/token.go:64` log vs `:116-118` wildcard).
5. **Close the class structurally**, not as a fourth point-fix (§3 below).

### 1.2 In scope

| # | Area | Files |
|---|---|---|
| S-A | Token grant data model + tri-state scan + fail-closed (`pkg/auth`) | `pkg/auth/token.go`, new `pkg/auth/permission.go`, `pkg/auth/store.go` |
| S-B | One reach resolver + one predicate (new package) | new `pkg/authz/` |
| S-C | The shared seam vocabulary | `pkg/models/group_rewrite.go` (`GroupRewriteResult.ScopeExcluded`), new `pkg/models/reach.go` (`ReachFilter`) |
| S-D | Storage: migration `034` (schema + data repartition) + registry append | `pkg/store/database/migrations/{sqlite,postgres}/034_add_allowed_groups.{up,down}.sql`, `pkg/store/database/migrate.go` |
| S-E | Admin API: POST/PATCH/GET contract + validation + body cap | `pkg/ui/server.go` |
| S-F | Runtime exclusion on every lane/surface | `pkg/store/database/store_group_rewrite.go`, `pkg/modelscache/models.go`, `pkg/proxy/handler.go`, `pkg/proxy/handler_anthropic.go`, `pkg/proxy/handler_groups.go`, `pkg/decision/handler.go`, `pkg/decision/group.go`, `pkg/imggen/handler.go` |
| S-G | FE: **two per-axis pickers (groups excluded from the models picker — LANDED)** + per-axis authoritative save + second surface | `pkg/ui/frontend/src/**` |
| S-H | Tests (Go + FE), gate boundary declaration, ship | see §13, §14 |

### 1.3 Non-goals (explicit)

| Non-goal | Reason |
|---|---|
| **MCP server-level authorization** | The MCP lane never selects a model (`pkg/mcp` has zero `AllowedModels` references); its unit is the server. A different axis; `decisions.md` Q9 default adopted. A **loud startup log line** names the gap (`decisions.md` O5) — **startup-log clause REJECTED fix-pass 2 (decisions.md §6.1 Ruling-rejections #4): no startup-log task is added; the non-goal + named follow-up stands.** Named follow-up: *"MCP server-level authorization: a token-scoped allow-list of upstream MCP server ids, enforced in `pkg/mcp`'s bearer middleware before `ForwardHTTPRequest`."* |
| **Image-gen group/chain surfaces** | Image-gen models have no `FallbackChain`; only the **entry** check applies (`pkg/imggen/handler.go:267-268`). It still calls the shared predicate. |
| **`pkg/ui` scope enforcement** | The admin API is the **editor** of scope, never its enforcer. |
| **Compat flag** (group id in `allowed_models` ⇒ member reach) | `decisions.md` Q2/A10 ruled out; the pre-ship repartition makes it unnecessary. |
| **A `code` field / excluded array on the 403 envelope** | `decisions.md` Q8 default (a): envelope byte-identical. Re-openable later without disturbing this commission. |
| **Credential-layer scope** | The credential layer never introduces a model id (rotation/hedge/failover are model-preserving). `I13`. |
| **`pkg/store/database/sqlc/schema.sql` regeneration** | Out of the build (only `queries.sql` + `schema.sql`, no generated package; only mention is a comment at `pkg/store/database/store.go:23`). Left stale deliberately. |

### 1.4 Invariants that MUST survive (preservation set)

| # | Invariant | Enforcement |
|---|---|---|
| P1 | **`allowed_models` ABSENT AND `allowed_groups` ABSENT ⇒ wildcard** (ABSENT-only — never "empty": an explicit `'[]'` is PRESENT-empty and grants nothing; the 3 live wildcard tokens per host keep working bit-for-bit). | The wildcard test is **CONJUNCTIVE** (§2.4). |
| P2 | **Anonymous ⇒ 401** | No change to `pkg/decision/handler.go:247-257`, `pkg/proxy/handler.go:705-723`, `pkg/proxy/handler_anthropic.go:403-422`, `pkg/imggen/handler.go` auth arm. |
| P3 | **Lane gates fail closed** | `pkg/decision/group.go:101-114`, `pkg/decision/handler.go:855-862` untouched. |
| P4 | **Decision error precedence** 401 → 404 → 400 `decision_misroute` → 400 group-lane misroute → 403 → 502 | §7 matrix; a chat-kind group + out-of-scope token stays **400** (`pkg/decision/handler.go:323` precedes the scope gate). |
| P5 | **v202 quota mint-scope doctrine + 30-minute ABSOLUTE conversation-hash binding TTLs** (`group_binding_ttl` / `model_binding_ttl`) | Not touched; no change to `pkg/grouplb`, `pkg/credentiallb`, `pkg/skipcache`, `pkg/modelcache`. |
| P6 | **Group selection = ORDERING-NOT-FILTERING** for *health* marks | The new axis is **authorization**, not health: reach exclusion is candidate-eligibility, applied at the base predicate and at list construction — it does not cool, does not mark, does not re-order. |
| P7 | **`pkg/decision/policy.go`'s layer fence** (imports only `math/rand` + `pkg/models`; doctrine *"may reorder candidates, never exclude them"*, `:15-18`, imports `:20-24`) | The decision scope filter lives in `pkg/decision/group.go`, **never** in `policy.go`. `TestPackage_ImportGuard_GroupPolicyFiles_NoCacheImports`-style guard extended (§13). |
| P8 | **No new DB read on the hot path** (`I14`, HARD — Ruling 5) | Membership expansion reads the **IN-MEMORY models snapshot** (`GetModels()` / `currentModelRows()`), invalidated on group writes — **REWRITTEN per Ruling 5 / I14** (`GetModelNative` is a live SELECT under `m.mu.RLock`, FORBIDDEN as a per-request reach source); the reach set is per-request and never cached. |
| P9 | **No attempt-time skip** (`I4`, `D9`) | Scope checks are greppable-absent from `pkg/proxy/race_coordinator.go` / `race_executor.go` / `pkg/decision`'s attempt loop; a structural test pins it. |
| P10 | **`/v1/*` stays public**; ingress topology untouched | No k8s/ change (boundary-declared as 0 files, §14). |

---

## 2. THE TOKEN SCOPE CONTRACT (FROZEN — backend and FE implement against this without talking to each other)

> This section is the keystone. It is stated so that two engineers, one on `pkg/auth`+`pkg/authz`+`pkg/ui` and one on `pkg/ui/frontend`, can implement in parallel. Every term below is normative.

### 2.1 The unit of authorization is a SET, not a string

```
Reach(T) =  { id : id ∈ T.allowed_models }
          ∪ { g  : g  ∈ T.allowed_groups }                 // the group TARGET is reachable …
          ∪ ⋃_{g ∈ T.allowed_groups} { m : m ∈ Members(g) } // … and its CURRENT members
```

- `Members(g)` is read **at request time** from the group row's `Members` (`models.ModelConfig.Members`, `pkg/models/config.go:415`; `GroupMember{ModelID,Weight,BackupOnly}`, `pkg/models/group.go:27-48`) out of the **IN-MEMORY models snapshot** the cache layer maintains (`GetModels()` / `currentModelRows()`), invalidated on group writes — **REWRITTEN per Ruling 5 / I14; the former "cache-backed `GetModelNative`" mechanism is DROPPED** (`GetModelNative` is a live SELECT under `m.mu.RLock`). **never** snapshotted into the token row (`D12`, `I6`).
- **`Reach` includes the group id itself.** This is load-bearing: it is what makes the `034` repartition behaviour-preserving (§5.4) — the client may still *name* the group.
- **`Reach` is computed ONCE per request** (§4.2), before any candidate list is constructed (`D2`, `I2`).

### 2.2 Grant states — per axis, three (the tri-state)

Each grant axis (`allowed_models`, `allowed_groups`) is **independently** one of:

| State | Storage | Go value | Meaning |
|---|---|---|---|
| **ABSENT** | **`NULL` only** (an empty/whitespace `TEXT` is **INVALID**, not ABSENT — fix pass B E2, landed `ScanGrant`) | `slice == nil` | No list on this axis. Contributes nothing. |
| **PRESENT** | valid JSON array, **including `'[]'`** | `slice != nil` (may be empty) | An explicit list. `[]` ⇒ grants **nothing** on this axis. |
| **INVALID** | non-NULL, unreadable (**empty/whitespace `TEXT`**, malformed JSON, an unknown raw type from the driver, **or a non-array JSON literal/object**) | `slice == nil` + `Invalid == true` | **Deny this axis entirely** (fail-closed). Pins: `TestScanGrant_OnlySQLNullIsAbsent`, `TestAuthzResolve_EmptyTextGrantIsDenyAll_NotWildcard`, `TestChat_EmptyTextGrantRow_DenyAll`. |

The **nil-vs-empty distinction is normative and must be preserved end-to-end** (`decisions.md` Q7/Q13). It is what makes "clear this axis" expressible without escalating to wildcard (§6, §7.7).

### 2.3 Token-level states — three

| Token state | Condition | Semantics |
|---|---|---|
| **UNRESTRICTED (wildcard)** | `allowed_models` **ABSENT** *and* `allowed_groups` **ABSENT*** *and neither axis INVALID* | Every model reachable, subject to the lane's own gates. **Exactly today's semantics** (P1). |
| **RESTRICTED** | ≥1 axis **PRESENT** (either axis, even if empty) | Reach per §2.1. |
| **DENY-ALL** | either axis **INVALID** | Reach = ∅. Every model request ⇒ 403. Not operator-selectable; it is the fail-closed landing. |

\* **THE WILDCARD TEST IS CONJUNCTIVE — this is the single most load-bearing line in the contract.** A token with `allowed_models` **ABSENT** and `allowed_groups` **PRESENT** is **RESTRICTED**, not a model-level wildcard. Reason: after the `034` repartition a narrow token has exactly that shape (`allowed_models` NULL, `allowed_groups=["coding-group"]`); a **per-axis** wildcard rule would silently promote it to "every model allowed" — i.e. the fix would grant more access than the defect (`decisions.md` R-3). The wildcard predicate is ONE function (`authz.Resolve`), never re-derived per site.

### 2.4 Matching rules

- **Exact, case-sensitive string equality** on `models.id` (`pkg/auth/token.go:119-124` already does this for the model axis; the group axis uses the same rule). No prefixes, no globs, no `"*"` entry, no regex. *Wildcardness is a state of the token, never a value inside a list.*
- **Group ids and model ids share ONE namespace** — single `models.id` PK (`pkg/store/database/migrations/sqlite/001_initial.up.sql:19-20`), `pkg/models/config.go:351`.
- **Group ids are never claim targets.** `allowed_groups` entries match `group.ID` (the row's own id, read **claim-blind** from the in-memory models snapshot — `GetModels()`/`currentModelRows()`; REWRITTEN per Ruling 5/I14), never a claimed/shadowed alias. The **member's own row id** is what the reach set must contain.
- **`allowed_groups` carries group ids ONLY** (any lane: chat-lane and decision-lane groups alike, `pkg/models/group_lane.go:36-43`). Decision **models** are ordinary models and belong in `allowed_models` (`decisions.md` Q3 adopted).
- **A group id in `allowed_models` is legal but narrow**: it grants the group **target only** — no member of it is reachable through that spelling (`D4`, `I7`). It exists so legacy/external writes stay accepted on write (Q4) while reach stays strict.

### 2.4a Request-side vs grant-side resolution asymmetry (CG-2)

**REQUEST side (the resolver / `GetModel`):** claims-first. A client-requested string that is a `shadow_model_id` claim resolves to the **claiming group row** (`pkg/models/config.go:586-604`, `pkg/modelscache/models.go:496-507`, `pkg/store/database/store.go:1073-1086`). Therefore a request naming a claimed id enters the **GROUP arm** at `pkg/decision/handler.go:323-326`, not the MODEL arm. The group arm's scope evaluation (`serveDecisionGroup`) receives `authToken` at `pkg/decision/group.go:96`; the client-requested string survives as `req.Model` / `rc.reqLog.Model` and is what the 403's `"model"` must quote.

**GRANT side (`Reach(T)`, `membersOf`):** stays **claim-blind** — **REWRITTEN per Ruling 5 / I14: membership comes from the IN-MEMORY models snapshot (`GetModels()` / `currentModelRows()`), invalidated on group writes; the former `GetModelNative` read path is DROPPED as the mechanism** (it is a live SELECT under `m.mu.RLock`; the claim-blind *semantics* — matching against the row's own id, never a claimed alias — is unchanged). `allowed_groups` entries match `group.ID` (the row's own id), never a claimed/shadowed alias; `allowed_models` entries are matched by exact id. Group ids are never claim targets (models/group.go:92-94 self-claim forbidden, :247-249 target-must-not-be-group).

**Consequence — per-shape behaviour for a decision-group request:**

| Shape | Request string | Resolves to | Arm | Scope check | 403 `"model"` |
|---|---|---|---|---|---|
| (a) | the group's **own** id | the group row (self-claim forbidden) | GROUP | group-level + member filter | the **client-requested** group id |
| (b) | a **member** id claimed by an **ENABLED** decision group | the claiming GROUP row (claims-first) | GROUP — **THE DEFECT PATH** (bypass span `handler.go:323-326` → `group.go:96-291`; `authToken` used only for metering at `group.go:341`) | same as (a) — must be added (gen-3 fix) | the **client-requested claimed string** (never the canonical group id) |
| (c) | a member id claimed by a **DISABLED** group | the member's own row (claim inactive: `enabled=1` filter at `store.go:1078` + `modelscache/models.go:500` re-check) | MODEL | the model-arm predicate fires | the client-requested string |
| (d) | a standalone decision id | its own row | MODEL | the model-arm predicate fires | the client-requested string |

**Pin P-G6 (corrected, CG-2):** *"A decision-group member id claimed by an ENABLED group (`models.shadow_model_id` → the claims index) resolves via claims-first `GetModel` (`store.go:1073-1086` / `modelscache models.go:496-507`) to the GROUP row, enters the GROUP arm at `handler.go:323-326`, and is served with NO token-scope check (bypass span `handler.go:323-326` → `group.go:96-291`, `authToken` used only for metering at `group.go:341`); the fix's 403 (`sendModelNotAllowedError`, `handler.go:784-794`) must echo the CLIENT-REQUESTED string."* The zero-upstream assertion is retained.

### 2.5 No implicit reach (STRICT-EXPLICIT)

For a **RESTRICTED** token, reach is **only** what §2.1 says. Specifically **NOT** granted:

| # | Not granted | Rule |
|---|---|---|
| N1 | the **chain targets** of a granted model (`A.FallbackChain`) | `D5` / `I5` — they must be listed explicitly or reached via a group expansion |
| N2 | the **members** of a group named only in `allowed_models` | `D4` / `I7` |
| N3 | the **chain targets of a granted member** (member's own `FallbackChain`) | `D5` |
| N4 | the **backup-only members** of a granted group | They *are* members — granted by `allowed_groups` (pool is a routing attribute, not a permission attribute). Stated so it is not accidentally excluded. |
| N5 | **anything** for a DENY-ALL token | `D3` |

**Failover-narrowing is ACCEPTED** (Emperor, user-confirmed): permission beats resilience. A restricted token whose whole candidate list is out of reach gets a clean **403**, never a fallback failure (§9).

The **wildcard** is the single deliberate exception: "no grants" = "no restrictions" (P1).

### 2.6 Resolution timing

| What | When | Cacheable? |
|---|---|---|
| The **grant lists** (`allowed_models`, `allowed_groups`, `Invalid` flags) | On token validation — carried **on the `*auth.AuthToken`** | **Yes** — already cached (`pkg/modelscache/tokens.go:108-125` `storePositive` caches the token object; positive TTL 60 s, stale cap 24 h). This is why the **INVALID flag must live on the struct**, not be returned separately: a separately-returned flag is **dropped at the cache boundary** (`decisions.md` Q7.1). |
| The **member expansion** (`Members(g)` for each granted group) | **Request time**, every request | **NO.** Never baked into the token and never cached in a new long-lived map (`D12`, `I6`; §11). |

### 2.7 Fail-closed rule (normative)

> Any grant axis that cannot be read and parsed ⇒ that axis grants nothing; if that makes the token non-wildcard, the token reaches nothing it was not explicitly granted, and a request whose entry/candidates are all unreachable gets the house **403** `access_denied`. The log line becomes **true**.

Both fail-open branches collapse into INVALID:
- malformed JSON — `pkg/auth/token.go:62-66` (unmarshal `:62`, comment `:63`, `SECURITY:` log `:64`, `return []string{}` `:65`);
- **unknown raw type** — `pkg/auth/token.go:52-55` (`default:` case, neither `string` nor `[]byte` ⇒ `return []string{}`). *The commission's evidence named only the first; both fail open today and both must land in INVALID.*

**CORRECTED (fix pass B, E2):** the `nil` path (`:41-43`) remains **ABSENT**; the empty-string path (`:57-59`) is **NO LONGER ABSENT** — landed `ScanGrant` (`f9adbce`) maps empty/whitespace `TEXT` to **INVALID (DENY-ALL)**. Only SQL `NULL` is ABSENT (pins: `TestScanGrant_OnlySQLNullIsAbsent`, `TestAuthzResolve_EmptyTextGrantIsDenyAll_NotWildcard`, `TestChat_EmptyTextGrantRow_DenyAll`).

### 2.8 Wire shape (the frozen FE/BE contract)

**`POST /fe/api/tokens`** (`pkg/ui/server.go:2501-2505` today; 4 fields → 5):

```json
{ "name": "…", "expires_at": "RFC3339|null",
  "allowed_models": ["…"] | null,     // omitted/null = axis ABSENT
  "allowed_groups": ["…"] | null }     // omitted/null = axis ABSENT (both absent = wildcard)
```
`[]` on POST ⇒ that axis is **PRESENT and empty** ⇒ grants nothing.

**`PATCH /fe/api/tokens/{id}`** (`pkg/ui/server.go:2563-2565` today) — **TWO wire states per axis (leader Ruling 2, fix-pass 2, = Q13(a); the cycle-1 "reset-to-absent" third state is REVERSED and no longer exists):**

```json
{ "allowed_models": ["…"] | [],        // key OMITTED, or present-with-EXPLICIT-null, or ids
  "allowed_groups": ["…"] | [] | null }  // wire meaning per axis:
                                          //   key omitted  -> KEEP EXISTING (the SQL column is NOT touched)
                                          //   explicit null -> KEEP EXISTING (identical to omitted — the request layer
                                          //                    is effectively TWO-state: key-present-with-array vs not)
                                          //   []            -> REPLACE WITH EMPTY (writes '[]'; grants nothing on this axis)
                                          //   ids           -> REPLACE (writes the JSON array)
```

**Reversal recorded (fix-pass 2):** the cycle-1 CG-1 wire extension (a distinct explicit-`null` = reset-to-absent state, carried by a custom `UnmarshalJSON` / `json.RawMessage` wrapper) is **REJECTED and REMOVED** — Go pointer fields cannot distinguish JSON-`null` from a missing key and **do not need to**: both decode to `nil` ⇒ keep. **Wildcard promotion on EDIT is intentionally inexpressible** — a PRESENT axis can never be returned to ABSENT via PATCH (`null`/omitted = KEEP, `[]` = PRESENT-empty); only creation can leave an axis ABSENT, so an operator promotes a restricted token back to wildcard by **re-creating the token** (documented in the release note + FE-9 note: Unrestricted mode = create-only in FE edit). This matches the LANDED code semantics verbatim (`pkg/ui/server.go` `updateTokenPermission` + `pkg/auth/store.go` `UpdateTokenPermission`, reconciled fix-pass 2 — C3).

**Store shape (ONE write-payload name — fix-pass 2 A4):** `auth.OptionalAllowlist{Set bool; Values *[]string}` per axis, carried by `auth.OptionalTokenPermission` (§4.1; supersedes `auth.TokenPermission` as the write type per the Q5 supersession line). `Set:false` ⇒ the SQL `UPDATE` omits the column (raw bytes preserved, including a seeded malformed `DENY-ALL` row); `Set:true, Values:&[]` ⇒ write `'[]'`; `Set:true, Values:&ids` ⇒ write the array; `Set:true, Values:nil` (write `NULL`) is store-level-only and **UNREACHABLE from the PATCH wire** under Ruling 2. **CreateToken keeps the full-write shape (no keep-semantics on create).** The LANDED interim payload (`auth.TokenPermission` + nil-axis-skip) is behaviorally identical (nil ≡ `Set:false`) and is renamed in the completion commit — C3 reconciliation, semantics = keep.

**All-axes-omitted PATCH:** keep the `GetTokenByID` 404 pre-check (`pkg/ui/server.go:2579`); execute **no** column `UPDATE` at all; return `200 {"success":true}`. A no-op is a success, not an error (and must not be reported as `ErrTokenNotFound`).

**Responses** (`TokenResponse` `pkg/ui/server.go:2491-2498`, list projection `:2629-2639`, create `:2692-2709`):

```json
{ "id":"…","name":"…","expires_at":"…","created_at":"…","created_by":"…",
  "allowed_models": ["…"] | null,   // *[]string: emits JSON null when the axis is ABSENT (NOT omitted)
  "allowed_groups": ["…"] | null }  // *[]string: emits JSON null when the axis is ABSENT (NEW)
```
**Wording note (PD-15 — FINAL, fix pass B item 9, verified against the LANDED DTO `pkg/ui/server.go:2497-2507` + `:2673-2682`):** `TokenResponse.AllowedModels` / `AllowedGroups` are `*[]string` with `json:"…,omitempty"` **but the handlers ALWAYS populate them with non-nil pointers** (`&t.AllowedModels` / `&t.AllowedGroups`) ⇒ **the keys are ALWAYS EMITTED** — an ABSENT axis arrives as an explicit JSON **`null`**, never as an omitted key (`omitempty` never fires on a non-nil pointer). `allowed_models_invalid` / `allowed_groups_invalid` are plain `bool` with **no `omitempty`** ⇒ **always emitted**. **The exact final wire shape** (GET/list/create-echo): `{id, name, created_at, created_by, allowed_models: null|[], allowed_groups: null|[], allowed_models_invalid: bool, allowed_groups_invalid: bool, expires_at?}`; **PATCH `/fe/api/tokens/{id}` returns `{"success":true}` only** (`{success: boolean}`). FE rule: `null` ≡ ABSENT, `[]` ≡ PRESENT-empty (grants nothing), and the two `*_invalid` booleans drive the DENY-ALL warning. State this positively so the next reader does not re-invert it.
Token **values/hashes are never returned** (only the one-shot `token` on POST 201). Field naming is snake_case (the established wire convention: `allowed_models`, `created_at`, `expires_at`).

**Wire behaviour change that MUST be documented (fail-closed direction):** `PATCH {"allowed_models": []}` today writes NULL ⇒ wildcard; after this change it writes `'[]'` ⇒ *grants nothing*. `decisions.md` Q13 adopted. Release notes carry it.
**THE release-note list (fix pass B-final — ONE canonical list; `phase3-plan.md` exit 10 and `plan-overview` point here, they do NOT restate their own lists):**

| # | Line (verbatim content required in the release note) |
|---|---|
| RN-1 | `PATCH {"allowed_models":[]}` now means *grant nothing*, not wildcard (fail-closed tightening — Q13). |
| RN-2 | **Q13(a) PATCH-null-keeps:** `PATCH {"allowed_models":null}` (or key omitted) = **KEEP** (column untouched). Two wire states only. |
| RN-3 | **Old-FE clear is a silent no-op:** the old FE always sends the key and emits `null` on clear ⇒ under RN-2 that is KEEP — the operator's clear does not take effect until the new FE ships. Covered by the single-image coupling (SPA embedded); stated so the operator is never surprised. |
| RN-4 | **PD-9 alias narrowing:** the gate authorizes the **resolved target only**; a token granting only a shadow/claimed alias is denied at entry (403 quoting the client-requested claimed string, zero side effects); repair = grant the group id (or the member ids). G0-4 names affected tokens. |
| RN-5 | **F3 deny-all-heals-on-save:** an operator save of a DENY-ALL (invalid-flag) token overwrites/heals that axis to `'[]'` (flag flips `false`, warning disappears) — semantically safe (`'[]'` grants nothing) but the corrupt-row evidence is overwritten; re-create the token to preserve it. |
| RN-6 | **Tier-1 rollback ORDERING (BLOCKING):** reverse repartition FIRST (with the Step-0 `'[]'` enumerate-and-reconcile), image/helm rollback SECOND — image-first promotes migrated narrow tokens to full access on public `/v1/*`. |
| RN-7 | **Single-image coupling (Q13(a)):** the FE PATCH-builder and the store serialization change ship in the SAME image; any future decoupling requires a re-ruling. |
| RN-8 | **Member chains no longer implicit** for repartitioned narrow tokens; members are authorized individually (intended narrowing). |
| RN-9 | **Group id in a `FallbackChain` limitation (PD-10, CORRECTED gen-10):** entry-chain groups ARE expanded into their in-reach member (never a raw group candidate ⇒ never a 502 — pinned `TestChainGroupEntry_NeverAdmitted_No502`); the remaining wart is a group id inside a group *member's* own chain (appended at rewrite time, never re-scanned) — pre-existing, documented, unpinned, not a scope escape. |
| RN-10 | **Object-valued / empty-string grant rows are DENY-ALL** (`ScanGrant` non-array JSON and empty/whitespace TEXT ⇒ INVALID); G0 queries F/G name any affected tokens. |

**F3 release-note line (fix pass B, item 10):** an operator save of a **DENY-ALL** (invalid-flag) token **overwrites/heals that axis to `'[]'`** — the flag flips `false` and the warning disappears. Semantically safe (`'[]'` grants nothing), but the **corrupt-row evidence is overwritten**; an operator who wants to preserve the evidence should **re-create the token** instead.

---

## 3. Class closure (why this is not a fourth point-fix)

The class is **named and stated in-repo** (§3.1 of the ADR; cite `adr-token-permission-model.md:166-198`) — a request authenticated with `T` contacts a model outside `Reach(T)`. Three generations: gen 1 (served ≠ authorized, ultimate model), gen 2 (chain/member reach, deferred in the v192 ship note and never written into a decision record), gen 3 (the decision-group arm returned before any check).

The two structural defects: **(S1) unit mismatch** (a *string* cannot constrain a *set* of candidates) and **(S2) site-check instead of list-property** (a point check at site A cannot constrain a list built at site B ⇒ it regrows at every new site).

**The structural close, as implemented by this plan:**
1. The unit becomes the set ( §2.1) — one resolver, one predicate (`D7`, §4).
2. The check becomes a **property of every candidate list**, applied at each layer's construction seam (§8) — advance/hedge/failover/re-entry then inherit it *by construction*, with **no token in the hot loop** and **no attempt-time skip**.
3. The decision lane is folded into the **same** predicate — no private group path can diverge again (`D7`).
4. Layer fences stay fences (`policy.go` untouched — P7).
5. **Deferral discipline:** any deferred scope obligation is written into the repo (`decisions.md`), never into a ship note (`I16`). This plan's deferrals are all recorded (§16, §17).

---

## 4. Token-scope data model + runtime plumbing (Q2)

### 4.1 The structs (exact)

**`pkg/auth/permission.go` (new).**

```go
package auth

// OptionalAllowlist is THE write-payload axis type (fix-pass 2, A4 — one
// name everywhere; supersedes auth.TokenPermission as the write type,
// decisions.md Q5 supersession). Set=false == the axis is untouched by
// this write (the SQL UPDATE omits the column — PD-3); Set=true writes
// Values: nil -> NULL (store-level only, UNREACHABLE from the PATCH wire
// under Ruling 2), [] -> '[]', ids -> JSON array. See auth.ScanGrant for
// the read side (the nil-vs-empty READ distinction stays normative, §2.2).
type OptionalAllowlist struct {
	Set    bool
	Values *[]string
}

// OptionalTokenPermission is the write payload carried by
// CreateToken/UpdateTokenPermission (one type for both methods).
type OptionalTokenPermission struct {
	AllowedModels OptionalAllowlist
	AllowedGroups OptionalAllowlist
}
```

**`pkg/auth/token.go` — `AuthToken` gains two fields (additive; existing literals keep compiling):**

```go
type AuthToken struct {
	ID            string
	Name          string
	TokenHash     string
	ExpiresAt     *time.Time
	CreatedAt     time.Time
	CreatedBy     string

	// AllowedModels is the MODEL-axis grant. nil = ABSENT (no list on
	// this axis); non-nil, possibly empty = PRESENT ([] grants nothing).
	AllowedModels []string
	// AllowedGroups is the GROUP-axis grant (ids of models rows with
	// kind='group'). Same nil-vs-empty rule.
	AllowedGroups []string

	// ModelsInvalid / GroupsInvalid are the fail-closed flags: the
	// column existed but could not be read/parsed. They live ON THE
	// STRUCT because the token cache caches the *AuthToken
	// (pkg/modelscache/tokens.go:117 storePositive) — a separately
	// returned flag would be dropped at the cache boundary.
	// Either flag true => the token is DENY-ALL (never wildcard).
	ModelsInvalid bool
	GroupsInvalid bool
}
```
The comment at `pkg/auth/token.go:33` (*"nil/empty = all models allowed"*) is **WRONG after this change** and must be rewritten to the nil-vs-empty rule (with the wildcard rule pointing at `authz.Resolve`).

**`pkg/auth/token.go` — the scanner.** Replace `ScanAllowedModels` with a tri-state scanner; both fail-open branches land in INVALID:

```go
// ScanGrant parses ONE grant column into its tri-state.
//   raw == nil                                   -> (nil,  present=false, invalid=false)  ABSENT
//   unknown raw type (not string / []byte)        -> (nil,  present=false, invalid=true )  INVALID   [old :52-55]
//   "" / whitespace-only TEXT                     -> (nil,  present=false, invalid=true )  INVALID (fail-closed — f9adbce Phase-1.1)
//   valid JSON array (incl. "[]")                 -> (ids,  present=true,  invalid=false)  PRESENT
//   non-array JSON literal ({"g1":"g1"}, 123, "x", null as TEXT) -> (nil, present=false, invalid=true) INVALID (fail-closed)
//   malformed JSON                                -> (nil,  present=false, invalid=true )  INVALID   [old :62-66]
func ScanGrant(raw interface{}) (ids []string, present bool, invalid bool)
```
`ScanAllowedModels` is **retired** from the store's read path and kept only as a deprecated `[]string`-shaped shim (comment: *"NOT an authorization decision — use authz.Resolve"*), so unrelated callers/tests that merely need the ids keep compiling. Read sites become `ids, present, invalid := auth.ScanGrant(raw)` at `pkg/auth/store.go:137/:179/:246`.

**`IsModelAllowed` (`pkg/auth/token.go:110-125`)** is **demoted**: it can only see one axis, so it can no longer be the authorization decision (a conjunctive rule needs both). It stays on the type (its own unit test `pkg/auth/token_test.go:191-292` remains valid for the model axis) but **all four lanes stop calling it**. A structural grep test pins that (§13 P-G4).

**`pkg/authz/scope.go` (new) — the ONE resolver + the ONE predicate.**

```go
package authz

// Scope is the per-request reach set of one token.
//   nil *Scope  => UNRESTRICTED (anonymous or wildcard token): Allows() == true for everything.
//   denyAll     => DENY-ALL: Allows() == false for everything.
type Scope struct {
	denyAll     bool
	unrestricted bool
	ids         map[string]struct{}
}

// Resolve computes Reach(T) once per request (plan §2.1/§2.6).
//   t == nil                       -> nil (unrestricted; the lane's own auth gate decides anonymously)
//   t.ModelsInvalid||GroupsInvalid -> &Scope{denyAll:true}
//   both axes ABSENT               -> nil (CONJUNCTIVE wildcard, plan §2.3)
//   otherwise                      -> restricted set = allowed_models ∪ allowed_groups ∪ ⋃ Members(g)
// membersOf returns a group's CURRENT member row ids from the IN-MEMORY
// models snapshot the cache layer already maintains (Ruling 5: NEVER a
// per-request GetModelNative SELECT — that is a live DB read under
// m.mu.RLock). pkg/proxy: an index built once per request from
// GetModels(); pkg/decision: h.currentModelRows(). Invalidated on group
// writes by the existing cache invalidation path. It is called ONLY for
// group ids present in t.AllowedGroups.
func Resolve(t *auth.AuthToken, membersOf func(groupID string) []string) *Scope

// Allows is THE predicate. Every lane and every candidate-construction
// site calls this — never a local re-implementation (D7/D8).
func (s *Scope) Allows(id string) bool

// IsUnrestricted reports the wildcard state (telemetry/debug only —
// never used as a per-site shortcut).
func (s *Scope) IsUnrestricted() bool
```
Rules pinned: `Allows` on a nil receiver returns `true` (that is the wildcard/anonymous case); `Allows("")` returns `false` on a restricted scope (an empty id is never a granted id).

**`pkg/models/reach.go` (new) — the seam vocabulary that avoids an import cycle.**
`pkg/auth` already imports `pkg/store/database` (`pkg/auth/store.go:9`), so `pkg/store/database` may **not** import `pkg/auth` (or `pkg/authz`). The predicate therefore crosses the boundary as a tiny interface declared in `pkg/models` (imported by both):

```go
package models

// ReachFilter is the authorization predicate a candidate-construction
// site consults. Implemented by *authz.Scope. nil => no filtering
// (wildcard token / no permission backend).
type ReachFilter interface{ Allows(modelID string) bool }
```

**`pkg/models/group_rewrite.go` — one ADDITIVE field** on `GroupRewriteResult` (no interface change):

```go
// ScopeExcluded is true when the group had base-servable members but
// EVERY one of them was outside the caller's reach set. It is the
// "empty because scope" signal that must surface as 403, never as the
// 502 group_unservable exhaustion (plan §9).
ScopeExcluded bool
```

### 4.2 Where the scope is created and how it reaches each surface

| Lane | Creation site (exact) | Carried by |
|---|---|---|
| **chat** | `pkg/proxy/handler.go` — immediately after the post-auth conversation-key derivation, **before** the entry gate and **before** `applyGroupRewrite` (`:783`). One call: `scope := authz.Resolve(authToken, h.membersOf(&rc.conf))`. | `rc.scope *authz.Scope` — a new field on `requestContext` (`pkg/proxy/handler_helpers.go:25`, next to `tokenID/tokenName` `:193-194`), cleared in `reset()` (`:207-228`) so a recycled context cannot leak a stale scope. |
| **anthropic** | `pkg/proxy/handler_anthropic.go` — right after `arcTokenID` is set (`:421`) / conversation-key (`:449`), **before** `applyGroupRewrite` (`:472`). | `arc.scope *authz.Scope` — a new field on `anthropicRequestContext` (`:1959-2031`), which **has no token field today** (this is the only struct change there). |
| **decision** | `pkg/decision` — the resolver is called with the token already in hand: `serveDecisionGroup` receives `authToken` at `pkg/decision/group.go:96`; the MODEL arm has it at `pkg/decision/handler.go:248`. | Passed as a parameter / local (`scope := authz.Resolve(authToken, h.membersOf())`), not stored on the handler (no cross-request state). |
| **image-gen** | `pkg/imggen/handler.go` — at the entry check site (`:267`), local variable. | local |

`membersOf` adapters (thin, per package, no shared state):
- `pkg/proxy`: `func (h *Handler) membersOf(conf *ConfigSnapshot) func(string) []string` → **reads the IN-MEMORY models snapshot, never a per-member `GetModelNative` call** (Ruling 5, fix-pass 2: `GetModelNative` is a live SELECT under `m.mu.RLock` — the documented reader-stall pattern must NOT be imported onto `/v1/*`). Build the member index ONCE per request from the rows the cache layer already holds in memory (`conf.ModelsConfig.GetModels()` on the `*modelscache.CachedModelsConfig` decorator — the same snapshot class `pkg/decision`'s `currentModelRows()` walks), filter `kind='group'`, map `row.Members[i].ModelID`; **invalidated on group writes** by the existing cache invalidation path (`OnGroupChanged` / the modelscache drop-on-write), so reach staleness == existing model-row cache staleness (no new knob, no strict read). Zero SQL on the reach path.
- `pkg/decision`: `func (h *Handler) membersOf() func(string) []string` → `h.memberRow(gid)` (**`pkg/decision/group.go:510`** `memberRow`, `:521` `currentModelRows` — *drift-corrected: the brief's `:519-526` starts mid-`currentModelRows`*) and its `Members`.
- `pkg/imggen`: only needs the group-id set (no member expansion is consulted on that lane because it has no chain) but uses the same resolver for uniformity.

### 4.3 The interface/seam impact — exact signatures and the RECOMMENDED shape

**Recommended shape, in one sentence: the member PICK is made scope-aware at the manager level via an OPTIONAL-INTERFACE FORWARD (no `pkg/grouplb` API change); the CHAIN is filtered in the request layer at the four list-build points; the two are complements, not alternatives (§8.1).**

Rationale in §8.1. Exact signatures:

**(a) `pkg/models` — a new OPTIONAL seam next to `models.GroupRewriter` (`pkg/models/group_rewrite.go:82-121`), following the `ClearGroupMemberCooldown` / `GroupLane` optional-interface precedent (`pkg/modelscache/models.go:1321-1341`, `:1370`):**

```go
// GroupRewriterScoped is the OPTIONAL reach-aware extension of
// GroupRewriter. A backend that does not implement it is treated as
// "no permission capability": the caller then applies its own
// post-rewrite list filter (fail-closed on SERVED ids) and logs loudly.
// Production always implements it (compile-time assertion in
// pkg/modelscache — a missing decorator forward must be a BUILD
// failure, never a silent no-op).
type GroupRewriterScoped interface {
	RewriteGroupEntriesScoped(modelList []string, conversationKey string, reach ReachFilter) GroupRewriteResult
	ExcludeGroupMemberScoped(groupID, conversationKey, memberID string, cooldown time.Duration, reach ReachFilter) GroupExcludeOutcome // ONE shape (A3 fix-pass 2: the PD-11 outcome struct — the ScopeOnly discriminator needs it)
}
```
`models.GroupRewriter` (`:82-121`) is **NOT** changed ⇒ the ~11 structural fakes (JSON twin, test stubs) stay valid.

**(b) `pkg/store/database` — manager implementation.** `RewriteGroupEntries` (`pkg/store/database/store_group_rewrite.go:55`) becomes a thin wrapper over a new scoped core:
```go
func (m *ModelsManager) RewriteGroupEntries(modelList []string, conversationKey string) models.GroupRewriteResult {
	return m.rewriteGroupEntries(modelList, conversationKey, nil)          // nil reach = no filtering
}
func (m *ModelsManager) RewriteGroupEntriesScoped(modelList []string, conversationKey string, reach models.ReachFilter) models.GroupRewriteResult {
	return m.rewriteGroupEntries(modelList, conversationKey, reach)
}
func (m *ModelsManager) rewriteGroupEntries(modelList []string, conversationKey string, reach models.ReachFilter) models.GroupRewriteResult
```
and `pickServableGroupMember` (`:160-286`) gains one trailing parameter:
```go
func (m *ModelsManager) pickServableGroupMember(group *models.ModelConfig, conversationKey string, reach models.ReachFilter) (memberID string, newlyBound bool, pool string, staleRepaired bool, ok bool)
```
The single candidate gate widens additively — **signature change confined to the package**:
```go
// isServableGroupMemberBase is unchanged (row shape only, :348-360).
// A new sibling predicate carries the authorization axis:
func isReachableMember(id string, reach models.ReachFilter) bool { return reach == nil || reach.Allows(id) }
```
applied at **all four** phases: Phase 1 `:205`, Phase 2 `:229`, Phase 3 `:253`, **and Phase 4 (`:270-284`) — which today calls NO predicate at all**, so without it a demoted out-of-scope member remains servable (§8.5 T2).

**PD-8 (AMBER) — `applyGroupRewrite` signature (handler_groups.go:68-94) MUST thread `reach` + `scopeExcluded` (A8):**
```go
func applyGroupRewrite(m *ModelsManager, modelList []string, conversationKey string, reach models.ReachFilter) (models.GroupRewriteResult, bool /*scopeExcluded*/)
```
The D13 entry-gate hoist is an explicit task+pin: chat `handler.go:839` (entry gate) runs **BEFORE** `:783` (rewrite); anthropic `handler_anthropic.go:514` (gate) runs **BEFORE** `:472` (rewrite). Pin (P-G27): on an entry 403, **ZERO** group-binding / sticky-state writes occur (no `rc.groupPick`, no `applyStickyOrder` promotion, no `groupTriedMembers`).

**PD-11 (AMBER) — `ExcludeGroupMemberScoped` (and `ExcludeGroupMember`) return an outcome struct, not a bare tuple (arch A-11) — and fix-pass 2 A3 freezes the ONE name: `models.GroupExcludeOutcome` (declared in `pkg/models`, next to `ReachFilter`):**
```go
type GroupExcludeOutcome struct { Replacement string; Pool string; OK bool; ScopeOnly bool }
```
`ScopeOnly=true` marks a scope-exhaustion (distinct from unservability) so §9.4's availability fallback is explicit and testable at the seam, not inferred from `ok=false`. The compile-time assertion `var _ models.GroupRewriterScoped = (*CachedModelsConfig)(nil)` (§4.3(c)) pins the decorator to this ONE signature.

**PD-7 (AMBER) — phantom pick (A7):** `applyGroupRewrite` (`handler_groups.go:80-93`) MUST return `pick = nil` whenever `res.GroupID == ""`; otherwise a scope-dropped fallback-position group mints a phantom pick that trips `handler.go:1138`, writes `groupTriedMembers[""]`, emits empty-id events, and blanks `RouteModel`.

**(c) `pkg/modelscache` — the decorator forward (MANDATORY, compile-time asserted):**
```go
func (c *CachedModelsConfig) RewriteGroupEntriesScoped(...) models.GroupRewriteResult  // forwards via type assertion, adds NO caching
func (c *CachedModelsConfig) ExcludeGroupMemberScoped(...) models.GroupExcludeOutcome  // forwards (ONE shape — A3)
var _ models.GroupRewriterScoped = (*CachedModelsConfig)(nil)                          // NEW compile-time assertion
```
Precedent: `pkg/modelscache/models.go:1301-1306` (rewrite forward, no caching — the rewrite is per-request) and `:1314-1319` (exclude forward).

**(d) `pkg/proxy` — the chain filter (one helper, four call points).** Signature mirrors the proven `applyStickyOrder` (`pkg/proxy/handler_groups.go:134`) shape:
```go
// applyReachFilter removes every candidate outside the scope from the
// list. nil scope => the list passes through UNTOUCHED (wildcard /
// anonymous). It logs each removal (id only) once per request.
func (h *Handler) applyReachFilter(scope *authz.Scope, list []string) []string
```

**(e) `pkg/grouplb` / `pkg/credentiallb`: NO API change.** The engine offers a member; the manager validates and `ExcludeAndReselect`s (the loop already exists at `:190-215`, `:219-242`) — so the authorization axis rides the existing "engine offers → manager validates" shape (A5 ruled out an engine-level filter). The credential layer is untouched (`I13`).

### 4.3a Plumbing note — resolver seam + cache decorator mirror (CG-2)

The permission predicate crosses package boundaries without an import cycle (`pkg/auth` already imports `pkg/store/database`, so the reverse direction is forbidden): `models.ReachFilter` (declared in `pkg/models`, imported by both) is the cycle-breaker, and `*authz.Scope` is its sole production implementer. The **scoped** manager seam (`GroupRewriterScoped`, `RewriteGroupEntriesScoped`, `ExcludeGroupMemberScoped`) is an **OPTIONAL interface** beside `models.GroupRewriter` (which stays byte-unchanged so the ~11 structural fakes stay valid), backed by the compile-time assertion `var _ models.GroupRewriterScoped = (*CachedModelsConfig)(nil)` — a missing decorator forward is a **build break**, never a silent no-op (the documented modelscache-decorator-forward trap). The decorator forwards via type assertion and adds **no caching** (the rewrite is per-request). `membersOf` is injected into `authz.Resolve` by the caller — **REWRITTEN per Ruling 5 / I14: it reads the IN-MEMORY models snapshot the cache layer maintains (`GetModels()` / `currentModelRows()`), invalidated on group writes; the former "cache-backed `GetModelNative`" mechanism is DROPPED** (`GetModelNative` is a live SELECT under `m.mu.RLock`) — so `pkg/authz` stays a leaf (imports only `pkg/auth`).

### 4.4 Hot-path cost

- Reach resolution: **O(grants + Σ members of granted groups)**, once per request; then **O(1) map lookup per candidate**.
- **Lazy-index statement (fix-pass A — cost honesty):** `GetModels()` returns a **DEEP COPY** of the cached row set, so naively calling it per member is O(all rows) each time. The accepted seam therefore builds the group→member index **ONCE per request** (a single `GetModels()` walk, then map lookups), and **only when `AllowedGroups` is non-empty** — a wildcard token (both axes ABSENT) and a models-only token build **no index at all**. That laziness is what keeps the wildcard path allocation-free and the restricted path O(rows) once, not O(rows × members).
- The member-pick walk becomes up to **N offer round-trips** per group entry in the worst case (N = member count, bounded by the existing loop bounds `len(defaults)`/`len(backups)` and `MaxGroupMembers = 16`, `pkg/models/group.go:26`) — the loop that already exists for the health axis; no new engine call shape.
- **Corpus-vs-code divergence — RESOLVED (fix pass C):** `pkg/authz/scope.go:47-51` now documents the **IN-MEMORY snapshot** (`GetModels()` / `currentModelRows()`, "NEVER a per-member live DB read") — matching this corpus; the earlier "still says GetModelNative / code comment pending" note is retired.
- **No new DB read — HARD REQUIREMENT (I14, leader Ruling 5, fix-pass 2):** reach membership is read from the **in-memory models snapshot** the cache layer already maintains (`GetModels()` / `currentModelRows()`), invalidated on group writes — **never** via per-member `GetModelNative` calls, which the approver proved are live SELECTs under `m.mu.RLock` (`pkg/store/database/store.go:1095-1145`, forwarded `modelscache/models.go:1301-1303`) and would import the documented ~90 s reader-stall pattern onto `/v1/*`. This matters because the periodic reconcile already holds the store write mutex across a DB read (known project risk) — the permission path must not add a reader.
- Target: **no measurable p99 regression**; measure on the chat + decision lanes during the gate (§14). No numeric budget was supplied (ADR §10 G4); the constraint is "one resolution per request, membership from **the in-memory models snapshot the cache layer maintains (`GetModels()` / `currentModelRows()`), invalidated on group writes** (Ruling 5/I14 — NOT `GetModelNative`, which is a live SELECT), map lookup per candidate".

---

## 5. DB representation + migration `034` + rollback (Q1)

### 5.1 Representation

`auth_tokens.allowed_groups TEXT` — a JSON array of **`models.id` values of `kind='group'` rows**, symmetric with `allowed_models` (`auth_tokens.allowed_models TEXT`, migration 022). `NULL` = ABSENT; `'[]'` = PRESENT-and-empty. `decisions.md` Q1 (a) adopted; the join-table alternative is rejected (per-validation JOIN on the hot path, second SQL home for token data, cache-shape change).

Token SQL lives in `pkg/auth` — `pkg/store/database` holds **no** `auth_tokens` Go code (only migration SQL + the stale sqlc snapshot).

### 5.2 Migration files (exact bodies)

**ATOMICITY MANDATE (leader Ruling 6, fix-pass 2):** DDL + data repartition run in **ONE transaction per dialect** — both SQLite and PostgreSQL support transactional DDL, so there is no excuse for a partial application (an `ALTER` that commits while the repartition aborts would brick the next boot with duplicate-column). Each `034_*.up.sql` is therefore **one `BEGIN…COMMIT` unit** containing the column add AND the repartition, **idempotent on re-run**, with the failure/recovery path NAMED in the runbook (§5.6): **boot abort ⇒ helm rollback to the v202 anchor (image `:0082773`; daoduc helm rev 46 / tier-1 image `:3cf3cb8` (v200); primary gen 41)**; the G0 pre-ship inspection (§14.3) is the preventive gate. Empirical verification of the frozen SQL against the four adversarial inputs (`[broken`, `{}`, `123`, `null`) is recorded in §5.5.

**`pkg/store/database/migrations/sqlite/034_add_allowed_groups.up.sql`**

```sql
-- Migration 034: token permission model — allowed_groups (group reach)
-- + pre-ship data repartition (plan §5.4; decisions.md Q2 (b)).
--
-- SQLite has NO `ADD COLUMN IF NOT EXISTS`; idempotency comes from
-- schema_migrations (migrate.go:103 isMigrationApplied). Precedent:
-- 022_add_allowed_models.up.sql:4-5, 030_add_model_kind.up.sql:24-27.
-- The PRAGMA in 022 is a NO-OP READ, not a guard — do not copy it as one.
--
-- TRANSACTION WRAPPER (028/032 precedent): modernc.org/sqlite does not
-- auto-rollback a partial multi-statement Exec — wrap explicitly.
-- Migrations are IN-PROCESS (no k8s Job / InitContainer).

BEGIN TRANSACTION;

ALTER TABLE auth_tokens ADD COLUMN allowed_groups TEXT DEFAULT NULL;

-- DATA REPARTITION (idempotent): move ids that name a kind='group' row
-- out of allowed_models into allowed_groups. GROUP ids and MODEL ids
-- share ONE namespace (models.id PK, 001_initial.up.sql:19-20).
-- json_valid() also guards against a malformed row aborting boot: a
-- malformed allowed_models is left untouched (the new scanner denies it).
-- json_group_array over zero rows returns '[]' on this driver (PD-4),
-- so NULLIF(..., '[]') converts an emptied column to NULL (ABSENT);
-- dialect parity with PG's json_agg (returns NULL). Both SET expressions
-- read the PRE-update row values (SQLite UPDATE semantics).
UPDATE auth_tokens
SET allowed_groups = NULLIF((SELECT json_group_array(j.value)
                         FROM json_each(auth_tokens.allowed_models) j
                        WHERE j.value IN (SELECT id FROM models WHERE kind = 'group')), '[]'),
    allowed_models = NULLIF((SELECT json_group_array(j.value)
                         FROM json_each(auth_tokens.allowed_models) j
                        WHERE j.value NOT IN (SELECT id FROM models WHERE kind = 'group')), '[]')
WHERE allowed_models IS NOT NULL
  AND allowed_models NOT IN ('', '[]')
  AND json_valid(allowed_models)
  AND EXISTS (SELECT 1 FROM json_each(auth_tokens.allowed_models) j
               WHERE j.value IN (SELECT id FROM models WHERE kind = 'group'));

COMMIT;
```

**`pkg/store/database/migrations/postgres/034_add_allowed_groups.up.sql`**

```sql
-- Migration 034 (PostgreSQL): see the sqlite twin for the full rationale.
BEGIN;

ALTER TABLE auth_tokens ADD COLUMN IF NOT EXISTS allowed_groups TEXT DEFAULT NULL;

-- Data repartition (idempotent). Row-by-row guard (PD-2): a DO $$
-- block catches invalid_text_representation per row, so a malformed
-- TEXT row beginning with '[' (e.g. '[broken') is skipped, never
-- aborting the in-process boot migration. Version-agnostic: no
-- dependency on PG 16+ pg_input_is_valid. json_agg over zero rows
-- returns NULL -> ABSENT (dialect parity via SQLite NULLIF, PD-4).
DO $$
DECLARE
  r RECORD;
BEGIN
  FOR r IN
    SELECT id FROM auth_tokens
     WHERE allowed_models IS NOT NULL
       AND btrim(allowed_models) <> ''
       AND allowed_models ~ '^\s*\['
  LOOP
    BEGIN
      UPDATE auth_tokens t
      SET allowed_groups = (SELECT json_agg(v) FROM jsonb_array_elements_text(t.allowed_models::jsonb) v
                            WHERE v IN (SELECT id FROM models WHERE kind = 'group')),
          allowed_models = (SELECT json_agg(v) FROM jsonb_array_elements_text(t.allowed_models::jsonb) v
                            WHERE v NOT IN (SELECT id FROM models WHERE kind = 'group'))
      WHERE t.id = r.id
        AND t.allowed_models::jsonb <> '[]'::jsonb
        AND EXISTS (SELECT 1 FROM jsonb_array_elements_text(t.allowed_models::jsonb) v
                    WHERE v IN (SELECT id FROM models WHERE kind = 'group'));
    EXCEPTION WHEN invalid_text_representation THEN
      -- malformed row: leave untouched; the new scanner denies it (fail-closed).
      NULL;
    END;
  END LOOP;
END $$;

COMMIT;
```

**Down files (documentation-only operator artifacts — there is NO down-runner).**

`034_add_allowed_groups.down.sql` (both dialects; the reverse UPDATE is dialect-specific):

```sql
-- Migration 034 (down): DOCUMENTATION-ONLY artifact. No down-runner
-- exists in this repo (precedent 033_add_credential_groups.down.sql:1-3:
-- "No down-runner exists in this repo (025/026/027 have NO down file);
-- rollback = restore backup.").
-- OPERATOR NOTE: if you run this by hand you MUST also delete the
-- schema_migrations row for version '034' (migrate.go:103), or the
-- runner treats 034 as applied and never re-runs it.

BEGIN;
-- 1) REVERSE the data repartition (deterministic: group ids go back into
--    allowed_models; the union is order-stable enough for a permission
--    list because matching is set membership, order-insensitive).
-- SQLite:
UPDATE auth_tokens
SET allowed_models = (SELECT json_group_array(value) FROM (
        SELECT j.value FROM json_each(allowed_models) j
        UNION ALL
        SELECT j.value FROM json_each(allowed_groups) j)),
    allowed_groups = NULL
WHERE allowed_groups IS NOT NULL AND json_valid(allowed_groups);
-- PostgreSQL: the same shape with jsonb_array_elements_text + json_agg.
-- 2) Drop the column.
ALTER TABLE auth_tokens DROP COLUMN IF EXISTS allowed_groups;   -- PG
-- SQLite (no IF EXISTS): ALTER TABLE auth_tokens DROP COLUMN allowed_groups;
COMMIT;
```

### 5.3 Registry append (the sixth "Amendment-1 trap") — `pkg/store/database/migrate.go`

The registry is an **ordered slice literal**, `var migrations = []migration{` at `:24`, first entry `:25`, **last entry `{"033", "033_add_credential_groups.up"}` at `:82`**, closing brace `:83`. Entries carry **no** `.sql` suffix (`loadMigrationSQL` sprintf at `:164-165`). Append **immediately after `:82`**, inside the slice:

```go
	// Amendment-1 trap (sixth occurrence): this registry is an ordered
	// slice, NOT a directory scanner; this append activates the 034 SQL
	// files (auth_tokens.allowed_groups + the token-grant data
	// repartition). Without this append the 034 files are silently inert
	// and the allowed_groups token scope does not exist — and nothing in
	// the build fails; the failure surfaces at the first allowed_groups
	// SELECT.
	{"034", "034_add_allowed_groups.up"},
```

**Migration-order note:** `034` runs after `031_add_group_fields` (which creates `members_json`/`shadow_model_id`) and after `030_add_model_kind` (which creates `models.kind`) — both required by the repartition's `WHERE kind='group'`. Runner order is slice order, so this is automatic; the plan states it so nobody re-numbers. **Boot order:** `database.Initialize` → `RunMigrations` (`pkg/store/database/init.go:19`) runs **before** the managers exist and before cache priming ⇒ zero staleness, no cross-process window (same reasoning as 032's header).

### 5.4 Why the repartition is in-lane, and what it preserves

- **In-lane:** it is a **code-shipped migration inside the house framework**, not an ad-hoc store write. Direct precedent: `migrations/sqlite/032_internal_only_data.up.sql` — a data-only convergence migration, idempotent, `BEGIN TRANSACTION`-wrapped, "reversible-by-not-delete", header noting "migrations are in-process". Per the commission, the ask-back on auth changes is **discharged**; no approval hold is planned.
- **Behaviour preservation proof** (`decisions.md` Q2): before, `allowed_models=["coding-group"]` ⇒ `Reach={coding-group}` — but the group *target* is reachable and today's code serves its members implicitly. After: `allowed_models=NULL`, `allowed_groups=["coding-group"]` ⇒ `Reach={coding-group} ∪ members(coding-group)` ⇒ **identical effective reach**, and the token is **not** a wildcard (conjunctive rule, §2.3). The 3 wildcard tokens have no group id in `allowed_models` ⇒ the `WHERE ... EXISTS(group id)` clause never matches them ⇒ **byte-untouched**.
- **Idempotency proof obligation (a test, not a claim):** after the first run no row satisfies `EXISTS(... kind='group')` in `allowed_models` ⇒ a second run is a no-op. Pinned by `TestMigration034_RepartitionIdempotent` (§13).
- **PD-2 (RED) — Postgres guard:** the `034` up migration uses a version-agnostic `DO $$ ... BEGIN ... EXCEPTION WHEN invalid_text_representation THEN ... END $$` row-by-row guard (safe on ALL PG versions; no dependency on PG 16+ `pg_input_is_valid`); a malformed row beginning with `[` is skipped, never aborting boot. The G0 pre-ship inspection retains the PG major-version check + the malformed-JSON-row check (§14.3). SQLite twin unchanged except PD-4's `NULLIF` (§5.2). Phase1-plan.md carries the matching update (owned by that worker).

### 5.5 The SQLite JSON1 probe (OI-1) — an explicit step, with a stated fallback

**Adversarial-input verification (fix-pass 2, Ruling 6 — EXECUTED, temp dir only, zero worktree writes; SQL under test = the LANDED `pkg/store/database/migrations/sqlite/034_add_allowed_groups.up.sql`, copied verbatim to `mktemp -d`).** Harness: python3 `sqlite3` (SQLite amalgamation build with JSON1; **caveat: not the shipped `modernc.org/sqlite` driver** — the modernc-side probe remains `TestMigration034_SQLiteJSON1Available`). Observed results (verbatim):

| Input (`allowed_models`) | Migration result | Reading |
|---|---|---|
| `[broken` | **MIGRATION OK; row byte-identical** (`'[broken'`, `allowed_groups` NULL) | `json_valid` guard SKIPS the row — no abort, no heal (the scanner later DENIES it) |
| `{}` | **OK; row byte-identical** (`'{}'`, groups NULL) | `json_valid`=1 but the `EXISTS(group id)` clause never matches ⇒ untouched |
| `123` | **OK; row byte-identical** (`'123'`, groups NULL) | same — untouched |
| `null` (TEXT `'null'`) | **OK; row byte-identical** (`'null'`, groups NULL) | untouched |
| `'[]'` (control) | **OK; row byte-identical** (`'[]'`, groups NULL) | explicit-empty PRESERVED — confirms the Tier-1 Step-0 `'[]'`-rows-skip behavior (🟡1) |
| `'["g1","m1"]'` | **OK; repartitioned** → models `'["m1"]'`, groups `'["g1"]'` | the intended repartition |
| NULL (wildcard control) | **OK; byte-untouched** | wildcard never enters the UPDATE |
| second UPDATE (idempotency) | **OK, 0 rows change** | idempotent re-run |

Conclusion: the frozen SQL satisfies Ruling 6's adversarial contract on this build — every malformed/unexpected value is **skipped, never aborting, never healed**; the repartition and idempotency behave as pinned.

The repartition needs `json_each` / `json_group_array` / `json_valid`. Driver: `modernc.org/sqlite v1.46.1` (`go.mod:8`), imported at `pkg/store/database/connection.go:14` (pure-Go, JSON1 compiled in for the current amalgamation — **not executed by the research, therefore proven here, not assumed**).

- **Step P1-3a (permanent test, ships):** `TestMigration034_SQLiteJSON1Available` — opens `newSQLiteConnectionAtPath` (`pkg/store/database/connection.go:102`), runs `SELECT json_group_array(value) FROM json_each('["a","b"]')` and `SELECT json_valid('["a"]')`; asserts the values. This makes the probe a **CI-enforced fact**, not a runbook promise. **JSON1 is a SQLite/dev/test concern only** — the live hosts run PostgreSQL (`DATABASE_URL`; the project `.env` fence carries `POSTGRES_DB`), where `json_agg`/`jsonb_array_elements_text` are core.
- **Step P1-3b (pre-deploy, runbook):** execute the same two expressions against the *shipped* sqlite build in the gate environment (they are the same binary as the test) — recorded in the gate's log.
- **Stated fallback (if P1-3a fails):** the repartition moves out of SQL into an **idempotent Go-side backfill** run at boot immediately after `RunMigrations` (a new `migrateTokenGrants(ctx, db)` in `pkg/store/database`, reading `auth_tokens.allowed_models` with `json.Unmarshal`, partitioning against a `SELECT id FROM models WHERE kind='group'`, writing both columns; guarded by a `schema_migrations`-style one-shot marker or by its own idempotent WHERE). Cost: +1 boot-time pass, no request-path change. **This fallback is a real fork and must be decided by the probe result, not assumed.**

### 5.6 Real rollback story

1. **No down-runner exists.** Every registry entry ends `.up`; `loadMigrationSQL` is only ever called with a `.up` name. Precedent statement: `migrations/sqlite/033_add_credential_groups.down.sql:1-3`.
2. **App-layer rollback — TWO SEPARATE FACTS, never conflated.**
   - **(a) SCHEMA:** the column is additive with a NULL default; the pre-034 binary never selects it ⇒ **an image/helm rollback never needs a schema rollback** (§14.7 Tier-1).
   - **(b) DATA:** the **data repartition is the only state-changing part**, and it is **NOT neutral**: it moved the group ids out of `allowed_models` — the one column the pre-034 binary reads. With the repartition applied and the OLD image running, the pre-034 `IsModelAllowed` treats `len(AllowedModels)==0` as **wildcard** (`pkg/auth/token.go:116`) ⇒ the migrated narrow tokens become **UNRESTRICTED (full access)**. ⇒ **the reverse repartition must run BEFORE the image rollback** (the idempotent reverse UPDATE drafted in the down file, §5.2) — the ordering and its prohibition are spelled out in §14.7 and in `phase3-plan.md` T3.13. Under the NEW binary a reversed row is still RESTRICTED (`allowed_models` PRESENT ⇒ not a wildcard under the conjunctive rule), so the reversal never widens privilege.
   - tier-2 = pre-migration DB restore (the deploy flow already mandates "deploy daoduc (backup first)") — it reverses the repartition and the schema together.
3. **Operator-run down** requires the `schema_migrations` hygiene step: delete the `034` row (`migrate.go:103` `isMigrationApplied` skips an applied version; `recordMigration` at `:141`).
4. **Partial-failure case:** if `Exec` succeeded but `recordMigration` failed (`:141`), the column exists with no version row ⇒ a re-run hits "duplicate column name". Recovery = manual `DROP COLUMN` **plus** delete the version row, or restore the backup. The `BEGIN/COMMIT` wrapper removes the multi-statement half-apply variant.
5. **NAMED recovery path (Ruling 6, fix-pass 2):** a boot that aborts inside `034` (transactional DDL ⇒ atomic roll-back of the whole migration; no partial state) is recovered by **helm/image rollback to the v202 anchor** (`:0082773`; daoduc helm rev 46, tier-1 image `:3cf3cb8` (v200), primary gen 41 — §14.5 D-series), after which the G0 pre-ship inspection (§14.3) is re-run as the preventive gate before any retry. This path is quoted verbatim in the phase-3 runbook (T3.13).

### 5.7 The hand-rolled `auth_tokens` DDL fixtures — **8**, not 7

**DRIFT REPORT (required):** the brief said **7**; the verified count is **8** (`decisions.md` D-1, re-verified here). All eight must gain `allowed_groups TEXT DEFAULT NULL` (and the ones missing `allowed_models` must gain it too, since the store's SELECT list will now read both):

| # | File:line | Has `allowed_models` today? | Action |
|---|---|---|---|
| 1 | `pkg/auth/store_test.go:53-62` | yes (`:60`) | add `allowed_groups` |
| 2 | `pkg/mcp/auth_routing_test.go:58-67` | yes | add `allowed_groups` |
| 3 | `pkg/mcp/e2e_test.go:72-81` | yes | add `allowed_groups` |
| 4 | `pkg/mcp/handlers_api_test.go:49-58` | yes | add `allowed_groups` |
| 5 | `pkg/mcp/mcp_endpoint_split_validation_test.go:60-69` | yes | add `allowed_groups` |
| 6 | `pkg/mcp/test_helpers_test.go:43-52` | yes | add `allowed_groups` |
| 7 | `pkg/proxy/handler_integration_test.go:52-59` | yes (`:58`) | add `allowed_groups` |
| 8 | **`pkg/ui/handlers_usage_test.go:33-41`** | **NO — neither column** (`id, token_hash, name, expires_at, created_at, created_by`) | **Assess + fix.** Today it survives because the usage-lane queries never select the grant columns (its INSERTs at `:104`, `:152`, `:203`, `:278`, … omit them). Any test in that package that constructs `auth.NewTokenStore` over this DB would now fail with "no such column". **Action:** add BOTH `allowed_models TEXT DEFAULT NULL` and `allowed_groups TEXT DEFAULT NULL`; keep the usage assertions unchanged (additive ⇒ zero risk). Verified: this file never constructs a token store (`grep tokenStore` → no hits), so it is a *latent* break, not an active one — but it is a fixture that will bite the first added permission test in `pkg/ui`. |

`pkg/store/database` needs no fixture work: its tests run the **real** migrations via `newSQLiteConnectionAtPath` + `RunMigrations` (`pkg/store/database/database_test.go:66-69`, `migrations_test.go:641/:686/:738`), so the column appears automatically.

---

## 6. API contract changes (Q7/Q8)

### 6.1 Request/response

| Verb | Field | Type | Semantics |
|---|---|---|---|
| `POST /fe/api/tokens` (`pkg/ui/server.go:2501-2505`, handler `:2514`, impl `:2646-2712`) | `allowed_groups` | `*[]string` `json:"allowed_groups,omitempty"` | `nil`/omitted ⇒ axis **ABSENT**; `[]` ⇒ PRESENT-empty (grants nothing); ids ⇒ PRESENT. `allowed_models` keeps its field but its **meaning for `[]`** flips to PRESENT-empty (§2.8). (POST keeps the **full-write** shape — `auth.OptionalTokenPermission` with both axes `Set:true`; no keep-semantics on create.) |
| `PATCH /fe/api/tokens/{id}` (`:2563-2565`, dispatch `:2554-2555`, impl `:2568`) — **TWO wire states per axis** (leader Ruling 2, fix-pass 2 = Q13(a); reconciled to the LANDED code) | `allowed_models` / `allowed_groups` | plain `*[]string` — NO `json.RawMessage`/custom-`UnmarshalJSON` wrapper (the cycle-1 omitted-vs-null distinction is REVERSED; both decode to `nil` ⇒ keep) | **key OMITTED, or explicit `null`** ⇒ that axis is **untouched** — the SQL `UPDATE` does **not** include that column (raw bytes, including a seeded malformed `DENY-ALL` row, stay byte-identical); **`[]`** ⇒ **REPLACE with empty** (writes `'[]'`, grants nothing on that axis); **ids** ⇒ replace (writes the array). The merge site at `:2589-2597` is **retired** — no read-merge-write; `currentToken.<axis>` is never copied into the payload. `GetTokenByID` (`:2579`) is kept **only** for the 404 pre-check. **PD-3 (RED):** a `nil` axis (omitted OR explicit null) skips the column — **invalid-omitted NEVER heals**. **All-axes-omitted PATCH:** no column UPDATE, `200 {"success":true}`. Wildcard promotion on EDIT is intentionally **inexpressible** (re-create the token; release note + FE-9 note). Store-level contract (ONE write-payload name, A4): `OptionalAllowlist{Set bool; Values *[]string}` (Set:false=omit column — maps BOTH wire states omitted AND explicit null; Set:true+Values:&[]=clear; Set:true+Values:&ids=replace; Set:true+Values:nil=store-level only, unreachable from the PATCH wire under Ruling 2). |
| Responses (`TokenResponse` `:2491-2498`; list `:2629-2639`; create `:2692-2709`) | `allowed_groups` | `*[]string` `omitempty` | Mirrors `allowed_models`; lets the FE round-trip without a second fetch. |
| `DELETE` (`:2552`) | — | — | unchanged (204). |
| `GET /fe/api/tokens/{id}` | — | — | still **not routed** (405 at `:2557-2559`). |

**POST body cap — REJECTED (fix-pass 2 scope ruling):** the cycle-1/Q6 plan to ADD a POST-side 64 KB `http.MaxBytesReader` to `createToken` (`:2646-2712`, which has none today) is **rejected-by-leader and REMOVED from this plan**; only the PATCH-side cap (landed at `:2570`) remains fact.

### 6.2 Backwards compatibility

| Direction | Behaviour | Why |
|---|---|---|
| **old FE → new BE** | the old FE **ALWAYS sends the key** (`useApi.ts:919-920` — null-or-ids; a cleared axis emits explicit `null`). Under Ruling 2 explicit `null` = **keep** ⇒ the old FE's "clear" is a **SAFE SILENT NO-OP** on the new BE (the operator believes access was removed; it was not) until the new FE ships. **Single-image coupling covers this** (the SPA is embedded and ships in the same binary — Q13(a)), so in-house skew is one deploy wide at most; the release note states it. POST without the key ⇒ ABSENT axis. | two-state wire (Ruling 2); release-note line required. |
| **new FE → old BE** | the BE decodes with `json.NewDecoder(r.Body).Decode(&req)` and **no `DisallowUnknownFields`** ⇒ an unknown `allowed_groups` key is ignored, no 400. | verified: the only `DisallowUnknownFields` mention in the repo is a comment at `pkg/decision/openaidecision.go:18`. |
| **external API consumers** | additive field; the ONE behavioural change is `PATCH {"allowed_models":[]}` meaning *grant nothing* instead of *wildcard* (§2.8) — a fail-closed tightening, documented in the release note. `PATCH {"allowed_models":null}` keeps today's *keep existing* meaning (leader Ruling 2: the cycle-1 reset-to-absent variant was rejected). In-house FE/BE ship in one binary, so in-house skew is nil. | `decisions.md` Q13 (as ratified under Ruling 2, fix-pass 2). |
| **FE/BE skew** | none: the SPA is embedded (`pkg/ui/static`) and ships in the binary. | house build. |

### 6.3 Validation on write — **adopted default** (`decisions.md` Q4)

- `allowed_groups`: every inbound id must **exist** as a `models` row **and** have `kind='group'`; otherwise **400** naming the offending id(s). Envelope follows the `/fe/api/*` convention: bare `{"error":"<string>"}`.
- `allowed_models`: every inbound id must **exist** (any kind). A group id is **accepted** here (target-only grant, §2.4).
- **PD-8 (AMBER) — input validation unambiguous (POST/PUT/PATCH):** (a) malformed/non-array values ⇒ `400`; (b) every id in `allowed_groups` MUST exist AND `IsGroup()` (`400` on unknown or non-group model); (c) `allowed_models` keeps the Q4 rule (accepts model ids of any kind — chat/decision/imggen — and tolerates group ids on write with target-only semantics). Validation runs on the INBOUND set only (anti-wedge), never on the merged/current result.
- **PD-3 (RED) — PATCH column-skip (ruled outcome, fix-pass 2):** nil axis (key omitted **or** explicit `null`) ⇒ skip that column in SQL ⇒ **`200 {"success":true}`, the column byte-untouched, and an invalid-omitted axis is NEVER healed** to `NULL`/wildcard (no `400` — the architect's 400-rejection variant is the REJECTED alternative). Canonical pin: **`TestTokenAPI_PatchNilAxis_DoesNotHealDenyAll` (P-G26)** — other names (`TestPatch_KeepsInvalidAxis_DoesNotHeal`, `TestUpdateTokenPermission_OmitsNilColumn` as the store-layer half) are aliases of this one behavior, not separate pins (C2 unification).
- **Validation runs on the INBOUND set only, never on the merged/current result** — this is the anti-wedge rule: a token whose model was deleted can still be PATCHed, and the operator only trips 400 when actively re-submitting a stale id.
- Home for the existence lookup: `pkg/ui` already holds `modelsConfig` (`Server` struct `:125-135`); add a small helper that prefers the claim-blind `GetModelNative` via a structural type assertion (same shape as `pkg/proxy`'s `directModelGetter`, `pkg/proxy/handler_groups.go:552-589`), falling back to `GetModel`.
- **No read-time validation** — reads stay lenient; the resolver is authoritative (a dangling group id grants nothing; request-time fail-closed, never a crash).

---

## 7. Semantics matrix (Q3) — request kind × token grant state

`Reach` per §2.1. "in-reach entry" = the client-requested string ∈ Reach. "in-reach candidate" = the model that would be contacted ∈ Reach. 403 = the house envelope `{"error":{"type":"access_denied","message":"model not allowed for this token","model":"<client-requested id>"}}`.

| # | Request | Token state | Verdict | Status / body | Where decided |
|---|---|---|---|---|---|
| 1 | single model (chat) | wildcard | allowed | normal | entry gate admits; list unfiltered |
| 2 | single model (chat) | restricted, model in `allowed_models` | allowed | normal | entry gate |
| 3 | single model (chat) | restricted, model only in a granted group's members | allowed | normal | entry gate (member is in Reach) |
| 4 | single model (chat) | restricted, model not in Reach (and group id in `allowed_models` only ⇒ target-only) | **403** | 403 access_denied, `model` = client string | entry gate (`handler.go:818-879` block, after hoist) |
| 5 | group (chat) | wildcard | allowed, all members serveable | normal | rewrite unfiltered |
| 6 | group (chat) | restricted, group in `allowed_groups`, ≥1 member in Reach | allowed | normal; only in-Reach members are candidates | manager predicate + list filter |
| 7 | group (chat) | restricted, group in `allowed_groups`, **no** member in Reach | **403** | 403 access_denied, `model` = group id | rewrite returns `FailedGroupID` **+ `ScopeExcluded`** ⇒ 403 arm (§9) |
| 8 | group (chat) | restricted, group named only in `allowed_models` (target-only) | **403** | 403 access_denied, `model` = group id | same as #7 (`ScopeExcluded`) |
| 9 | group (chat) | restricted, group **not** named at all | **403** | 403 access_denied, `model` = group id | entry gate (L0) — before the rewrite (D13) |
| 10 | group (chat), unknown groups | any | **404** `model_not_found` | unchanged | boundary 404 (`ErrModelNotFound`, `pkg/proxy/handler.go:589-606`) |
| 11 | decision **model** | wildcard | allowed | normal | decision MODEL arm |
| 12 | decision **model** | restricted, in Reach | allowed | normal | decision MODEL arm (shared predicate) |
| 13 | decision **model** | restricted, not in Reach | **403** | 403 access_denied | `pkg/decision/handler.go:340-354` (shared predicate) |
| 14 | decision **group** | wildcard | allowed, all members | normal | group arm |
| 15 | decision **group** | restricted, group in `allowed_groups`, ≥1 member in Reach | allowed | normal, only in-Reach candidates | `pkg/decision/group.go` filtered member copy before `:118` |
| 16 | decision **group** | restricted, group in `allowed_groups`, no member in Reach | **403** | 403 access_denied, `model` = group id | new arm in `group.go` between the lane gate `group.go:101-114` and `buildGroupPolicy` `group.go:118` / `policy.order()` `group.go:119` (scope-empty); shapes (a)/(b) 403s carry the **client-requested** string (which survives as `req.Model` inside `serveDecisionGroup`) |
| 17 | decision **group** | restricted, group not named at all | **403** | 403 access_denied, `model` = group id | same new arm (group-level check) |
| 18 | **chat-kind group** posted to `/v1/decisions` | restricted and out of scope | **400 `decision_misroute`** | unchanged misroute body | the kind gate `:317-336` **precedes** the scope gate (P4) |
| 19 | decision group, unresolvable lane | any | **400 `decision_misroute`** | `pkg/decision/group.go:110-112` | lane gate precedes scope |
| 20 | image-gen model | wildcard | allowed | normal | imggen entry gate |
| 21 | image-gen model | restricted, in Reach | allowed | normal | imggen entry gate |
| 22 | image-gen model | restricted, not in Reach | **403** | 403 access_denied | `pkg/imggen/handler.go:267-268` (shared predicate) |
| 23 | decision model posted to `/v1/chat/completions` | any | **400** (chat misroute) | unchanged | chat misroute guard precedes scope |
| 24 | any model | **DENY-ALL** (invalid grant row) | **403** | 403 access_denied | every entry gate (`Allows` false everywhere) |
| 25 | any | **anonymous** (no token, internal model) | **401** | unchanged | auth gate (P2) |
| 26 | any | **anonymous** (no token, public model) | allowed | unchanged | as today |
| 27 | any group, all base-servable members **disabled/vanished** | wildcard **or** restricted | **502** `group_unservable` (text lanes) / **502** `decision_all_members_failed` (decision) | unchanged | the "empty by unservability" arm — **distinguishable from 403** (§9) |

**Column that must never appear:** a restricted token never produces a 502 for a scope reason, and a wildcard token never produces a 403 for a scope reason.

### 7.1 Additional pins (PD-9, PD-11, PD-15)
- **PD-9 (AMBER) — shadow/claimed alias (A9):** `Reach(T)` authorizes against the **resolved** group/target. If the client requested a shadow/claimed id, the entry gate rejects with `403` quoting the **client-requested claimed string** in `"model"` (fail-closed; no escalation). G0 gains a cross-check for any live token referencing shadow names (§14.3). Pin P-G6 (existing).
- **PD-11 (GREEN) — enabled/disabled precedence (A-? ):** ONE exact precedence for `pkg/decision/handler.go`: Auth `401` → Lane gate `400` `decision_misroute` → Scope vs Disabled ordered so a disabled + out-of-scope request does not leak (disabled arm and scope arm must not reorder to expose scope). Other lanes already follow Auth → Scope → Disabled; decision matches. Pin P-G5 (misroute) + a new precedence pin.
- **PD-15 (AMBER) — Anthropic fallback-chain construction (A15):** in `pkg/proxy/handler_anthropic.go:514-568`, `fallbackChain` is built from `GetModelNative` and filtered by `scope.Allows` at construction time; pin the exact lines in the implementation. `pkg/decision/group.go:118` MUST filter a **copy** of `group.Members`, never mutating the cached slice (A15 spec hygiene).

## 8. Per-layer exclusion mechanics (Q2/Q9)

### 8.1 The fork, resolved — and why BOTH shapes are needed

**Recommendation: (A) manager-level predicate for the member PICK + (B) request-layer list filter for the CHAIN.** Neither alone is sufficient; this is the honest reading of the evidence, and the two are complements at different layers:

| Shape | Covers | Cannot cover |
|---|---|---|
| **(A)** widen the candidate gate in `pkg/store/database/store_group_rewrite.go` (`:205/:229/:253` + **Phase 4 `:270-284`**) and filter the materialised `[member.ID] + member.FallbackChain` (`:101-102`) | the group **member pick** (so an out-of-scope member cannot kill a group that has an in-scope sibling — `D6/L2(a)`), the member's **own chain**, and every re-offer (offer and rebuild share one authority, `ExcludeGroupMember` `:489` → `pickServableGroupMember`) | the **entry model's** `FallbackChain` — those entries are appended at list assembly (`pkg/proxy/handler_functions.go:200-202`, `pkg/proxy/handler_anthropic.go:330-332`) as **separate `modelList` entries** and are never "group members" |
| **(B)** filter the rewritten list in `pkg/proxy` at the four list-build points | the entry chain, the member chain, and any other list entry — a single set-membership sweep, immune to which seam produced the entry | the **pick choice**: a post-hoc filter would drop an out-of-scope pick and shorten the list even when an in-scope sibling exists ⇒ a spurious 403 |

**Decisive argument for (A) as the member authority:** the exclude-and-reselect machinery (offer → validate → exclude → re-offer, `:190-215` / `:219-242`) exists **only** at the manager level. Re-deriving a scope-aware pick in `pkg/proxy` would need either a second pick implementation (a second authority — exactly the divergence that caused the incident `73e3bd0`) or an engine API change (ruled out: A5). So: **(A) for members, (B) for chains, one resolver shared by both.**

**Why (B) cannot be skipped even with (A):** `D5`/`I5` — chain targets are **not inherited**. A restricted token listing only model `A` must not be served `A.FallbackChain[0]`. `modelList` at that point is `[A, chain0, chain1, …]`; only a list filter enforces the rule.

**Why the coordinator still needs no change:** advance reads `c.models[i+1]` (`race_coordinator.go:1899-1912`, literal at `:1906`), and the hedge reads `c.models[0]` (`:421-435`, `:435`). Both are reads of the **already-filtered** list ⇒ the property propagates by construction (`I3`, `I4`). **Hedge and credential-failover are NOT scope surfaces** (the code's own comment at `:426-428`: *"it does not widen model coverage"*); stated explicitly so nobody "fixes" them.

### 8.2 Per-surface site table (one row per surface — the commission's checklist)

| # | Surface | File:line | Lane/arm | CURRENT behaviour | NEW behaviour | Status on denial | Exclusion happens at |
|---|---|---|---|---|---|---|---|
| 1 | **decision MODEL arm** | `pkg/decision/handler.go:340-354` (guard `:342`, call `:343`, 403 `:351`) | decision, non-group | guarded form `len(AllowedModels)>0` + `IsModelAllowed(client string)` | shared predicate `scope.Allows(modelID)`; no `len()` guard (the state model owns wildcard) | 403 access_denied | construction (entry gate) |
| 2 | **decision GROUP arm** | `pkg/decision/handler.go:317-326` (returns `:325`) → `pkg/decision/group.go:96` | decision, group | **ZERO scope evaluation** (returns before the allow-list); `policy.go` candidacy only | (a) group-level `scope.Allows(groupID)` after the lane gate `:101-114`; (b) members filtered into a **copy** of the group row before `buildGroupPolicy` at `:118` (PD-5: scope-filtering of `group.Members` runs **before** `policy.SelectMembers`, comparing pre-filter base-servable member count vs post-filter count to drive `ScopeExcluded` (403) vs `decision_all_members_failed` (502); pin exact call ordering) | 403 access_denied (new arm; `ScopeExcluded`-style distinction from 502) | construction (before `:118`) |
| 3 | **chat group-member resolution** | `pkg/proxy/handler.go:783` → `handler_groups.go:68` → `store_group_rewrite.go:55/:89` → `:160-286` | chat | engine offer + base shape predicate at `:205/:229/:253`; **Phase 4 `:270-284` unguarded** | scoped rewrite: base predicate **+ `isReachableMember`** at all four phases; `ScopeExcluded` set when the member set was non-empty but all out of reach | 403 (via `ScopeExcluded`) instead of 502 | construction (member pick) |
| 4 | **chat chain entries** | `pkg/proxy/handler_functions.go:196-206` (build `:200-202`) | chat | `[resolvedModel.ID] + FallbackChain` — no token (pre-auth) | list filtered in `pkg/proxy` **post-rewrite, pre-race** (`applyReachFilter`) | 403 only if the **head** is out of reach (entry gate); otherwise silent removal | construction (list filter) |
| 5 | **anthropic group-member resolution** | `pkg/proxy/handler_anthropic.go:472` → same shared helper | anthropic | identical to #3 (parity by construction) | identical to #3 | 403 instead of 502 (Anthropic envelope slot: keep the house chat 403 writer `pkg/proxy/handler.go:525-535`, which the anthropic lane already reuses at `:530`) | construction |
| 6 | **anthropic chain entries** | `pkg/proxy/handler_anthropic.go:326-336` (build `:330-332`) | anthropic | same as #4 | same list filter, applied at `:539` (pre-walk, after the entry gate) | as #4 | construction |
| 7 | **chat chain ADVANCE** | `pkg/proxy/race_coordinator.go:858` call → `:1899-1912` (`c.models[i+1]` at `:1906`) | chat | walks the unfiltered chain | **NO CHANGE** — reads the filtered `c.models` | n/a | inherited |
| 8 | **anthropic sequential advance** | `pkg/proxy/handler_anthropic.go:548-767` (`for modelIndex, currentModel := range arc.modelList` `:550`) | anthropic | walks `arc.modelList` | **NO CHANGE** — walks the filtered list | n/a | inherited |
| 9 | **hedge (`modelTypeSecond`)** | `pkg/proxy/race_coordinator.go:947` spawn → `:421-435` (`c.models[0]` `:435`), `SetUseSecondaryUpstream(true)` `:491-493` | chat | credential-level hedge on the **same** model | **NO CHANGE**; explicitly **not** a scope surface (`:426-428`) | n/a | none (out of scope by ruling) |
| 10 | **cred-failover (`modelTypeCredFailover`)** | `race_coordinator.go:809` spawn → `:459-485`, plan `:1929`+ | chat | next **credential** on the same model | **NO CHANGE** | n/a | none (`I13`) |
| 11 | **chat re-entry** | gate `pkg/proxy/handler.go:1138-1159`; body `:1196-1287`: offer walk `:1210-1222` (`ExcludeGroupMember` `:1213`), rebuild `:1231` (`applyGroupRewrite` on `rc.modelListOriginal`), list swap `:1248-1249` | chat | re-offers any group member; rebuild re-materialises unfiltered members | (a) the offer walk goes through `ExcludeGroupMemberScoped` ⇒ out-of-scope members are never offered; (b) the rebuild is the same authority ⇒ filtered; (c) `applyReachFilter` re-applied on the rebuilt list at the loop top (belt-and-braces, same place sticky ordering re-applies) | **status follows the SINGLE attempt-count-keyed rule in §9.4** (0 upstream attempts + all candidates scope-excluded ⇒ 403 at any pass #n; ≥1 attempt ⇒ the unchanged terminal 502) — pointer only, no restatement | construction (offer + rebuild) |
| 12 | **anthropic re-entry** | gate `handler_anthropic.go:728-765` (harvest `:748-753`, call `:754`); body `handler_groups.go:452-542` (offer `:466`, rebuild `:480`, sticky `:504`) | anthropic | same as #11 | same as #11 (scoped exclude + rebuild + filter at `:504`) | as #11 | construction |
| 13 | **image-gen** | `pkg/imggen/handler.go:267-268` | imggen | guarded `len()>0` + `IsModelAllowed` | shared predicate; entry-only (no chain on this lane) | 403 | entry gate |
| 14 | **credential-layer pick** | `pkg/store/database/store.go:2810`, `:2950` → `pkg/credentiallb` `GetOrSelect` (`engine.go:226`) | all | picks a **credential** for an already-chosen model | **NO CHANGE** — the model was already admitted | n/a | none (`I13`) |
| 15 | **decision-group candidate list** | `pkg/decision/policy.go:58-96` (build), `:110-136` (order), consumed `pkg/decision/group.go:118-119` | decision | candidacy = exists+enabled+lane | the **group row handed to `buildGroupPolicy`** carries only in-Reach members (filter in `group.go`, **not** in `policy.go`); `policy.go` is byte-unchanged | 403 when `len(order)==0` **because of scope** | construction (at or before `:118`) |

### 8.2a Additional pins (PD-12, PD-14, PD-15, A7, A8, A10, A12, A13)
- **PD-12 (AMBER) — `RewriteGroupModel`/`applyGroupRewrite` per-position ScopeExcluded (A7 phantom pick folded):** `idx == 0` (requested group) ⇒ `ScopeExcluded` ⇒ `403`; `idx != 0` (fallback-position group) ⇒ drop the entry + loud log + continue (never 502); re-entry passes the **same** `Scope` and never emits `502 all_models_failed` when a re-entry target was excluded by scope with zero in-scope attempts. `applyGroupRewrite` returns `pick = nil` whenever `res.GroupID == ""` (A7).
- **PD-14 (GREEN) — Anthropic envelope parity (A14):** `handler_anthropic.go:486-488` (502 `group_unservable` writer) and `:530` (`sendModelNotAllowedError`) reuse the chat writer (`handler.go:525-535`) ⇒ the 403 envelope is byte-identical across lanes. Pin (P-G28).
- **PD-15 (AMBER) — Anthropic `fallbackChain` construction:** pin exact lines in `handler_anthropic.go:514-568` where `fallbackChain` is built from `GetModelNative` and filtered by `scope.Allows` at construction time (A15).
- **A8 — D13 entry-gate hoist:** chat `handler.go:839` (entry gate) BEFORE `:783` (rewrite); anthropic `handler_anthropic.go:514` (gate) BEFORE `:472` (rewrite). Pin: ZERO group-binding/sticky writes on an entry 403 (P-G27).
- **C6 disposition (fix-pass 2) — the claimed-id re-check is RESTORED inside the rewrite guard (zero side effects):** D13's transient side-effect carve-out (a claimed request slipping past the hoisted gate and creating binding/sticky state before the rewrite's filter 403s) is CLOSED: `applyGroupRewrite` re-checks `Allows(resolved group id)` for every group entry BEFORE materialising a pick or binding, and denies with ZERO side effects (no binding, no pick, no member expansion). Defence-in-depth behind the T2.16 strict entry gate; pinned by `TestChat_OutOfScopeGroup_CreatesNoBinding` extended to the claimed-id case.
- **A10 — group id in `FallbackChain` (CORRECTED to landed reality, gen-10 re-pin — the earlier "NOT expanded ⇒ credential-less row ⇒ runtime 502" claim was FALSE):** **ENTRY-chain groups ARE expanded.** `handler_functions.go:200-202` places `[resolvedModel.ID, resolvedModel.FallbackChain...]` into `modelList`, and `store_group_rewrite.go:105-181` iterates `for _, entry := range models` and expands any `kind='group'` entry into its in-reach member, **replacing the raw group id** (the group row never reaches credential resolution ⇒ never a 502). Pinned by `TestChainGroupEntry_NeverAdmitted_No502` (LANDED `1e65774`): the primary's `FallbackChain = ["pin-plain-group"]` expands to `pin-chain-member` and serves **200** after the primary 500s; the raw group id is absent from `res.ModelList`; the rewrite pick is `(pin-plain-group, pin-chain-member)`. **The real, narrower wart (PD-10 / RN-9):** a group id inside a group *member's* own `FallbackChain` (appended at rewrite time to the output list and never re-scanned by the single-pass rewrite) — a documented **pre-existing wart**, deliberately NOT pinned, never a scope escape; reach filtering still applies to every candidate.
- **A12 — `handler.go:857` guard:** delete/unify the surviving `len(authToken.AllowedModels) > 0` guard in Phase 2; extend the structural grep guard (P-G22) to forbid any `len(.*AllowedModels)` outside `pkg/auth`.
- **A13 — wire-level row substitutions (non-scope):** `ResolvePeakHourModel` (5 sites: `race_executor.go:320`, `internal_handler.go:322`, `store.go:2684`, `modelscache/models.go:1033`, `models/config.go:1270`) and `SecondaryUpstreamModel` (`race_executor.go:545`) substitute the upstream WIRE string within the same already-admitted `models.id` row — explicitly NON-SCOPE by ADR D6/L5 & I13. No change required; listed so the plan is silent on neither.
- **PD-15 bullet-4 disposition (fix-pass 2, 🟡6):** the credential-layer non-interference clause — cross-token conversation bindings stay isolated by `tokenID` salting in `conversationKey` (`pkg/credentiallb/key.go:48-51`), and a grant change mid-conversation never extends a binding past the 30-minute ABSOLUTE TTL — is dispositioned as **PINS, not code change**: `TestConversationKey_TokenSalting` and `TestConversationKey_MidBindingRevocation` (phase2 §5.3). No new knob; the existing salting + absolute-TTL mechanisms are asserted, not modified.

### 8.3 The exact four chat build points for `applyReachFilter` (and the drift it corrects)

**DRIFT REPORT (required).** `decisions.md` Q12 names the OpenAI loop-top as `pkg/proxy/handler.go:1247` and the OpenAI re-entry rebuild as `:1348-1353`. Verified at `a3f0335`:
- `:1247` is inside `tryGroupReentry`'s tried-mark block (`:1246-1249` is `rc.groupTriedMembers[...] = true` / `groupAdvances++` / `rc.modelList = newList` / `rc.groupPick = pick`). **The chat race loop-top is `:991` (`applyStickyOrder`) with the coordinator constructed at `:996`** — those are the correct anchors.
- `:1348-1353` is `streamResult` (buffer/relay), **not** a rebuild. The only rebuild sites for `rc.modelListOriginal` are `:800` (assignment) and `:1231` (reuse). **Correct anchor: `:1231`, with the list swap at `:1248-1249`.**

The four points where the execution list is (re)built/finalised, therefore:

| Point | Exact anchor | Why |
|---|---|---|
| **C1** chat race loop-top | `pkg/proxy/handler.go:991` (immediately before/after `applyStickyOrder`; the list is aliased into the coordinator at `:996`) | covers the initial list **and** every re-entry replay (the loop re-runs) |
| **C2** anthropic pre-walk | `pkg/proxy/handler_anthropic.go:539` (the `applyStickyOrder` call immediately before the walk at `:550`) | covers the initial list |
| **C3** anthropic re-entry rebuild | `pkg/proxy/handler_groups.go:504` (right after `arc.modelList = newList` `:495`, at the existing sticky re-order) | the rebuild re-materialises members |
| **C4** chat re-entry rebuild | `pkg/proxy/handler.go:1248-1249` (the `rc.modelList = newList` swap) — plus C1 re-filters on the next loop iteration | belt-and-braces: the loop-top filter alone would suffice, but the explicit site makes the obligation reviewable (`I15`) |

Placement rule: **filter BEFORE `applyStickyOrder`** at each point (sticky ordering must not promote an entry the permission filter would then remove — otherwise the promoted head would be filtered out and the promotion silently lost).

### 8.4 The four re-entry/advance traps, each with its resolution

| Trap | Evidence | Resolution |
|---|---|---|
| **T1 — `FailedGroupID` fires for FALLBACK-position groups** | the setters `res.FailedGroupID = row.ID` live in `pkg/store/database/store_group_rewrite.go` at **`:85`** (decision-lane boundary refusal, `idx == 0` only), **`:91`** (no servable member) and **`:96`** (defensive re-validation miss) — the `:91`/`:96` setters fire for **any** entry index, so an all-out-of-scope group in the fallback chain currently 502s the **whole** request. (The field itself is declared in a different file: `pkg/models/group_rewrite.go:67` — `FailedGroupID string`.) | **Drop the entry, log loudly** (mirroring the decision-lane boundary refusal at `:82-88`), **unless the entry is index 0** (the requested group) — then `ScopeExcluded` ⇒ 403. Rationale: a fallback the token cannot use is not the request's failure; failing the whole request would let a permission setting turn a healthy primary into a 502. |
| **T2 — Phase 4 does not call the predicate** | `:270-284` (`if len(bucket) > 0` `:273`, `head := bucket[0]` `:280`) — the demoted sweep executes a `demotedMember` without re-validation | **Apply `isReachableMember` when building the bucket** (Phases 1/2/3 already classify there) **and** re-check at `:280` before returning (defence in depth: the bucket is only as fresh as the rows read). |
| **T3 — `groupPickInfo.MemberID` can go stale** | `handler_groups.go:87-93` returns `res.MemberID`; `rc.groupPick` drives the tried-set (`handler.go:805`), metering (`:810-812`) and the re-entry offer (`:1213`) | The pick is made **by the scoped predicate**, so the picked member is in reach by construction; the list filter then cannot remove it. **Assertion, not hope:** if after filtering `pick.MemberID ∉ newList`, log `[authz] PICK/LIST DIVERGENCE` and treat the entry as `ScopeExcluded` (fail closed) — never serve a list whose stamped pick is absent. |
| **T4 — anthropic has no coordinator** | `handler_anthropic.go:548-767` walks sequentially | The filter is applied to `arc.modelList` (C2/C3) ⇒ the walk inherits it. `arc.groupPick` is set from the same scoped rewrite ⇒ no divergence. |

### 8.5 Why construction-time exclusion makes advance/hedge/failover inherit it

The invariant (`I3`): *every candidate entering an execution list is in `Reach(T)` at the moment it enters.* Advance (`c.models[i+1]`), hedge (`c.models[0]`), cred-failover (`triggerInfo.modelID` = the failed model) and re-entry (rebuild + offer) are all **reads of that list** or of the failed model's own id ⇒ no path can introduce a new model id ⇒ no per-attempt check and no token in the coordinator (`D9`, `I4`, `A2`).

**Structural proof obligation (a test, §13 P-G6):** grep-guard that `pkg/proxy/race_coordinator.go`, `pkg/proxy/race_executor.go` contain **no** scope predicate call and **no** `authz.` import — the "no attempt-time skip, no token in the hot loop" invariant becomes mechanically checkable.

### 8.6 `NewRaceCoordinator` empty-list guard (PD-10, RED)
`pkg/proxy/race_coordinator.go:338-340` is exactly `if len(models) == 0 { models = []string{cfg.ModelID} }` — a hidden 5th list-construction fallback. `newRaceCoordinatorWithEvents` returns `*raceCoordinator` today, so "return an error on `len(models)==0`" requires a **signature change**: specify `newRaceCoordinatorWithEvents(...) (*raceCoordinator, error)` (or a guarded constructor) that returns an error when `len(models) == 0`, removing the `cfg.ModelID` fallback. Callers (construction site `pkg/proxy/handler.go:996`) MUST assert `len(models) > 0` after `applyReachFilter` and return `403 ScopeExcluded` on an empty filtered list **before** calling the constructor. Pin (P-G31) regression test.

## 9. All-excluded failure (Q4) — 403 permission failure, not a fallback failure

### 9.1 The distinction, stated unambiguously

| Situation | Meaning | Envelope |
|---|---|---|
| **empty because SCOPE** | candidates existed and were removed **by `Reach(T)`** | **403** `access_denied`, house 403 writer, `"model"` = **client-requested id** |
| **empty because UNSERVABLE** | no member is base-servable (disabled/vanished/wrong lane), or the pool is empty | text lanes **502** `group_unservable` (`store_group_rewrite.go:91`; envelope `pkg/proxy/handler.go:783-798`, `sendError :797`; message `handler_groups.go:430-432`; anthropic `handler_anthropic.go:472-489`, `:486-488`); decision **502** `decision_all_members_failed` (`pkg/decision/group.go:345-358`, code const `:57`) |
| **re-entry exhausted after ≥1 served attempt** | the token's in-reach members all failed upstream | **unchanged** terminal path (`group_member_failed` + `group_fallback_exhausted` ⇒ `handleRaceFailure(..., "all_models_failed")`, `pkg/proxy/handler.go:1157-1161`; anthropic `:766` ⇒ "All models failed" 502). **Not** a permission failure. |

### 9.2 Detection mechanism — one additive signal, three consumers

`GroupRewriteResult.ScopeExcluded` (§4.1) is set **only** when: the group had ≥1 base-servable member, **and** every such member failed `isReachableMember`. That is precisely "empty because scope", computed where the knowledge exists, transported additively.

Emission per arm:

| Arm | Detection point | Emit |
|---|---|---|
| chat (index-0 group) | `applyGroupRewrite` (`handler_groups.go:68-94`) returns `failGroup` + `scopeExcluded`; call site `handler.go:783-798` — the `failGroup != ""` branch gains one `if scopeExcluded {` sub-branch | 403 via `h.sendModelNotAllowedError(w, rc.reqLog.Model)` (`pkg/proxy/handler.go:525-535`) + event outcome `"model_not_allowed"` |
| anthropic (index-0 group) | same helper; call site `handler_anthropic.go:472-489` | 403 via the **same** chat writer (`handler_anthropic.go:530` already reuses it) + event |
| chat/anthropic fallback-position group | same helper, `idx != 0` | **drop the entry**, loud log, no envelope change (T1) |
| decision group | `pkg/decision/group.go`: count members before/after the reach filter (or compare `len(group.Members)` filtered vs `policy.Members`) | 403 via `h.sendModelNotAllowedError(w, groupID)` (`pkg/decision/handler.go:784-794`), event outcome `"model_not_allowed"` (existing vocabulary) |
| decision group, group id itself not in Reach | new arm right after the lane gate `:101-114` | same 403 |
| image-gen | entry-only (no candidate list) | nothing extra — the entry gate covers it |

### 9.2a Cross-tenant cooldown benching — COOLDOWN-SKIP MANDATED (leader Ruling 1, fix-pass 2; supersedes the earlier "accept and document" disposition)

**Shared-pool property (background, still true):** the `(group, member)` cooldown/skip-cache pool is **shared across tokens** — the `ExcludeAndReselect` `0 → defaultCooldown` coercion at `pkg/grouplb/engine.go:788-791` (writing `st.cooldowns[id]`) plus the shared `(group, member)` `skipcache.Mark` (`pkg/skipcache/skipcache.go`) mean a scope exclusion *could* bench a member for other tokens via the stale-member arms at `pkg/store/database/store_group_rewrite.go:206` and `:230`. Classification of the *escape* direction is unchanged: no out-of-scope model is ever served (reach invariant I1/I3 holds).

**RULED (fix-pass 2): the skip is MANDATORY, not optional.** Scope-excluded candidates are removed at construction/advance and are **NEVER attempts** — therefore they can never legitimately write cooldowns (the commission's own Requirement-2 principle). Implementation mandate: **scope rejections in all candidate-selection phases skip every cooldown/skip-cache write** — no `ExcludeAndReselect` cooldown write for a scope-refused offer, no `skipcache.Mark`, no `st.cooldowns[id]` write (phase2 T2.3's per-phase rule is skip-only; `ExcludeGroupMemberScoped` never writes a cooldown for a scope-refused offer). A permission denial is not a health fact and must not create cross-request state.

**Retraction record:** the prior "leader PD-6 ruled to **accept and document**" claim (in this section's earlier revision, decisions.md R-11, and ADR §4.7) was **UNSOURCED — no ratification record exists**; it is **RETRACTED** by fix-pass 2. R-11 and ADR §4.7 are corrected to **MANDATE the skip**. The architect's multi-token pin is IMPLEMENTED as a required pin (P-G32, rewritten): **"Token A's scope exclusion never cools M1 for wildcard Token B."**

Normative home: `decisions.md` §3 R-11 (corrected) and `adr-token-permission-model.md` §4.7 (corrected). This plan.md records the mandate + the retraction only.

### 9.3 Exact envelope (byte-identical, no new fields)

```
{"error":{"type":"access_denied","message":"model not allowed for this token","model":"<client-requested id>"}}
```
- `"model"` = the **client-requested** id (the group id when a group was named) — matching the other lanes' convention of naming what the client asked for.
- **Excluded member ids go to the log and the events payload**, never into the response (they would leak the existence of out-of-scope models — `A1`).
- No `code` field (the three existing writers have none; `decisions.md` Q8 (a) adopted). A 4th writer is **not** added: the three existing writers are byte-identical (`pkg/decision/handler.go:784-794`, `pkg/proxy/handler.go:525-535`, `pkg/imggen/handler.go:713-723`) and are reused as-is.
- **Check runs BEFORE any upstream attempt** — no paid call, no attempt bookkeeping, no cooldown write for a scope denial.

### 9.4 The one documented gap: re-entry

**THE SINGLE RULE for re-entry scope-exhaustion status — attempt-count-keyed (leader ruling, fix-pass A; this §9.4 is the ONE normative home — every other site POINTS here, never restates):**

- **ZERO upstream attempts** occurred because **every remaining candidate was scope-excluded** ⇒ **403 `access_denied`** (house envelope, `"model"` = client-requested id) — at construction pass **#1 AND any re-entry pass #n**. A permission denial with no attempt made is never an availability failure.
- **≥1 upstream attempt occurred** (the request was partly authorized and members failed upstream) ⇒ the **existing terminal 502 `all_models_failed`** envelope, **UNCHANGED** — never a mid-request 403.

*Rationale (kept from the original):* once an attempt has happened, a 403 would be a false permission signal and would break the "403 before any attempt" guarantee. The observable contract holds in both branches: no out-of-scope model is ever attempted. Ratification: decisions.md R-12 (leader-ratified). Recorded here and cross-referenced in §17.

---

## 10. Fail-closed semantics (Q5)

| Aspect | Specification |
|---|---|
| **Trigger** | either grant axis INVALID — malformed JSON (`pkg/auth/token.go:62-66`) **or** unknown raw type (`:52-55`). |
| **State** | token is DENY-ALL; `Allows()` false for every id, including group ids. Not wildcard. |
| **Status** | **403** `access_denied` (the same house envelope) on every lane, for every model/group request. |
| **Server-side logging** | the existing `SECURITY:` line (`pkg/auth/token.go:64`) becomes **true**: it must be extended to name the axis and the token id, and an additional line must be emitted at **resolve** time (`[authz] token=<id> DENY-ALL: grant axis unreadable`) so the denial is traceable to the request. **Re-scoped (fix-pass A):** "once per axis per token-validation" is **unobtainable at resolve time** — `Resolve` runs **per request** over a **cached** `AuthToken`, so there is no per-validation hook on this path. Emit the `[authz] … DENY-ALL` line **on the first denial per request** instead (cheap, traceable), and keep the `SECURITY:` scanner line (which DOES run per validation at `pkg/auth/token.go:64`) as the once-per-validation record. Do not attempt to dedupe across requests. |
| **Wildcard impact** | **none.** Wildcard requires both axes ABSENT and clean (conjunctive, §2.3). An INVALID axis can never be ABSENT. |
| **Existing live rows** | the pre-ship read-only inspection (§14, Step 0) proves no live row is unreadable before the flip; if one is found, it is reported and the flip still ships (it is a **tightening**: today such a row serves everything). |
| **Test** | `TestScanGrant_*` (both branches) + `TestAuthzResolve_InvalidAxis_DeniesAll` + a per-lane 403 pin (§13). |

- **PD-15 (GREEN) spec hygiene (i):** `ScanGrant` must map the JSON literal `'null'` to **INVALID** (not `present=true, ids=nil`, which would write back `NULL`). Pin in `TestScanGrant_*`.
- **PD-13 (AMBER) / A14 — `idToHash` FIFO cap:** `CachedTokenStore.idToHash` is FIFO-capped at 50,000 entries (`pkg/modelscache/models.go:25`, `tokens.go:297-304`); an evicted hash can make `UpdateTokenPermission`'s cache-drop miss, leaving old grants cached ≤60s (or ≤24h stale-positive during a DB infra outage). Acknowledged and bounded here: no new eviction policy in this commission; the drop is best-effort and the 60s positive TTL bounds staleness. Document in the release note.

## 11. Group membership dynamism (Q6)

- **Resolution:** request time, every request, from the **current** group row's `Members`, read out of the **IN-MEMORY models snapshot** (`GetModels()` / `currentModelRows()`), invalidated on group writes — **REWRITTEN per Ruling 5 / I14**; the former "cache-backed `GetModelNative`" mechanism is DROPPED (`GetModelNative` is a live SELECT under `m.mu.RLock`; the claim-blind *fill semantics* at `modelscache/models.go:1459` is unchanged).
- **Never cached:** the expanded member set. The token cache holds the **grant lists only** (`pkg/modelscache/tokens.go:108-125`), TTL 60 s positive / 24 h stale cap — baking the expansion in would keep a **removed** member reachable for up to 24 h. Not acceptable for a permission axis (`decisions.md` Q10 adopted).
- **Invalidation:** none is required for the expansion (recomputed per request). The grant lists are invalidated by `UpdateTokenPermission` (`pkg/modelscache/tokens.go:297-304`: delegate-then-**drop the cached entry**) — the new `allowed_groups` write rides that same drop; a **decorator-through test** pins it (§13).
- **PD-7 (AMBER) — `NewCachedTokenStore` invalidation + clone (A14):** verify and specify: `NewCachedTokenStore` invalidates on `CreateToken`/`UpdateToken` covering `AllowedGroups`, and clones `AllowedGroups` + the `ModelsInvalid`/`GroupsInvalid` flag fields on cache read (zero shared-slice mutation across goroutines). Pin with a decorator-through test (P-G20) and a clone-mutation test (P-G29).
- **PD-13 (AMBER) — `idToHash` cap:** acknowledged in §10; the reach-staleness window equals the existing model-row staleness window; no new knob.
- **Bounded staleness inherited from the models cache:** the expansion reads rows through the same cache that already serves row shape (`isServableGroupMemberBase` `:348-360` has the identical property). The pre-existing bound (write-through on `AddModel`/`UpdateModel`/`RemoveModel` `pkg/modelscache/models.go:1062/:1096/:1138`, plus the reconciler snapshot swap `:407`→`:449`) applies unchanged. **The plan does NOT add a strict (uncached) read** — that would add a DB read per request (`I14` violated) to fix a pre-existing, bounded staleness. **OI-2 is answered as: read through the existing in-memory models snapshot the cache layer maintains (`GetModels()` / `currentModelRows()`), invalidated on group writes (Ruling 5/I14 — NOT `GetModelNative`); the reach-staleness window equals the existing model-row staleness window; no new knob, no strict/uncached read.** (`decisions.md` §5.2 OI-2 asks the plan to pin this — this paragraph is the pin.)
- **Explicit statement: no new long-lived cache is introduced.** No new map, no migration-time snapshot, no token-row expansion.
- **PD-15 (GREEN) spec hygiene (iii):** `pkg/decision/group.go:118` MUST filter a **copy** of `group.Members`, never mutating the cached slice. Pin P-G30.

---

## 12. FE changes

### 12.1 Shape decision — **TWO PER-AXIS PICKERS, GROUPS EXCLUDED FROM THE MODELS PICKER** (LANDED design; fix pass B-final I3-3 — supersedes the earlier one-picker recommendation)

**REWRITTEN to match the LANDED FE at `4be0a12`/`81e8788`.** The earlier §12.1/§12.2/§12.3 mandated ONE flat picker with the kind filter deleted; the landed implementation is **TWO authoritative per-axis pickers**:

- **models picker** — carries chat / image-gen / decision rows and **EXCLUDES group rows** (`TokenForm.tsx:84-85` / `:159-181`); pinned by **`TokenForm_PickerExcludesGroupModels`** (§13.1a FE-81).
- **groups picker** — carries the group rows (`allowed_groups` axis).
- **Submit/save is per-axis and the PICKER IS AUTHORITATIVE (F1):** what the operator sees selected on an axis is exactly what is saved on that axis — including **cross-axis additions** per `grantAxes.tsx` (a **stored** group id stays on its **stored** axis; a **NEW** group id classifies onto the **groups** axis by `kind`). Pinned by the grantAxes pins (§13.1a FE-93…100: `axisOfId` unit trio + `TokenList_TargetOnlyGroupId_SurvivesNoChangeSave` / `TokenForm_NewGroupSelection_LandsOnGroupsAxis` / `TokenList_BothAxes_NoChangeSave_KeepsIdOnBothAxes` / `TokenList_CrossAxisAddition_IsPreserved` / `TokenList_DuplicateStoredIds_AreDedupedPerAxis`).
- The decision supersession trail lives in `decisions.md` Q11 (row rewritten fix pass B-final).

### 12.2 File-by-file (LANDED)

| File | Landed change |
|---|---|
| `pkg/ui/frontend/src/components/tokens/TokenForm.tsx` | **two per-axis pickers** (models picker excludes group rows — `:84-85`/`:159-181`); per-axis `onSubmit`; unrestricted (both `null`) / restricted (per-axis `[]` or ids) wire semantics |
| `pkg/ui/frontend/src/components/tokens/grantAxes.tsx` (**NEW, landed**) | `axisOfId` stored-membership-first classifier (target-only group id in `allowed_models` STAYS on the models axis; stored group id stays on groups; a NEW group id classifies by `kind` onto groups) — the C3 fix + F1 cross-axis semantics |
| `pkg/ui/frontend/src/components/tokens/TokenList.tsx` | axis-aware display: model chips + group chips distinct; wildcard badge ONLY when both axes ABSENT; DENY-ALL (invalid-axis) warning rendering |
| `pkg/ui/frontend/src/types.ts` | `allowed_groups: string[] \| null` + the two `allowed_*_invalid: boolean` DTO fields |
| `pkg/ui/frontend/src/hooks/useApi.ts` | `createToken` FULL WRITE (both axes always on the wire — `null` when nothing selected, `[]` restricted-empty, ids otherwise); `updateTokenPermission` two-state MERGE per Ruling 2 (`[]` = clear, key-omitted/`null` = keep), optimistic merge both axes |
| `pkg/ui/frontend/src/components/SettingsPage.tsx` / `App.tsx` | thread the per-axis callbacks |

### 12.3 FE tests — LANDED set (§13.1a is the ledger; this section records the mapping)

The old "flip all three assertions at `TokenForm.test.tsx:82/:83/:84`" wording described the one-picker design and is **SUPERSEDED**: the landed suite is **13 TokenForm + 8 grantAxes + 4 TokenList + 8 useApi = 33 FE cases** (§13.1a FE-91…123, titles verbatim), including the former named assertions as: `TokenForm_PickerIncludesDecisionAndImageGenModels` (models picker widens), `TokenForm_PickerExcludesGroupModels` (groups excluded), `TokenForm_GroupPickerEmitsAllowedGroups` (split), the C3 (a)/(b) cross-axis pins, and the F1 both-axes/cross-axis save pins. **No backend endpoint added:** `useModels()` already returns group rows; the FE filters client-side per axis.

## 13. Test pins mapped to surfaces (Q10)

### 13.1 Go pins

**Pin-ledger single source of truth (gen-10 re-pin, 2026-11-19):** the enumerated `added == expected` set is **§13.1a: 88 Go + 33 FE = 121 pins, ALL LANDED** (79 at `4be0a12` + the 9 obligation pins in `1e65774`; Phase 1 = 42, Phase 2 = 46, FE = 33). Every other count in the corpus points to §13.1a; no locally restated numbers.

| # | Pin | File (host) | Asserts | Pack |
|---|---|---|---|---|
| P-G1 | **decision GROUP arm, group not granted** | `pkg/decision/handler_group_test.go` (exists; 1960 lines, harness `newGroupEnv` `:175-259`, token seeded `AllowedModels:nil` `:182-187`, group row id `"decision-group"` `:242`) | restricted token without the group ⇒ **403** `access_denied`, `model`=group id, and **ZERO upstream attempts** (assert the fake upstream's call counter == 0) | A (`./pkg/decision/...`) |
| P-G2 | **decision GROUP arm, group granted** | same file | `AllowedGroups:["decision-group"]` ⇒ **200**, the served member = an in-group member, metering row = member id | A |
| P-G3 | **decision MODEL arm 403** (regression of the existing pin) | `pkg/decision/handler_test.go:666-675` (token `AllowedModels=["some-other-model"]` `:670` ⇒ 403 `:672`) | unchanged shape, now through the shared predicate | A |
| P-G4 | **anonymous ⇒ 401** on every lane | existing auth tests (`pkg/decision/handler_test.go` memTokenStore `:270-350`, `pkg/proxy/authenticate_test.go`, `pkg/imggen/handler_test.go`, `pkg/proxy/handler_anthropic_test.go`) | status 401, body unchanged | A |
| P-G5 | **400 `decision_misroute` precedence preserved** | `pkg/decision/handler_group_test.go` / `pkg/proxy/handler_anthropic_misroute_test.go:140-228` | chat-kind group + out-of-scope token ⇒ **400**, **not** 403 | A |
| P-G6 | **shadow/claimed decision id resolves into the group arm and 403s on the CLAIMED string** | `pkg/decision/handler_group_test.go` (new case using the shadow seam) + `pkg/proxy/handler_groups_shadow_member_test.go:71` precedent | **corrected pin (CG-2):** a decision-group member id claimed by an ENABLED group resolves via claims-first `GetModel` to the GROUP row, enters the GROUP arm at `handler.go:323-326`, and is served with NO token-scope check (bypass span `handler.go:323-326` → `group.go:96-291`, `authToken` used only for metering at `group.go:341`); the fix's 403 (`sendModelNotAllowedError`, `handler.go:784-794`) must echo the CLIENT-REQUESTED claimed string; the group arm is entered; **zero** upstream calls | A |
| P-G7 | **chat group-member exclusion** — an out-of-scope member is never picked; the group still serves an in-scope sibling | `pkg/store/database/store_group_test.go` (host of the rewrite pins at `:…`, keep-authorized `TestGroup_RewriteGroupEntries_*`) + `pkg/proxy` end-to-end | `GroupRewriteResult.ModelList` ⊆ Reach; `MemberID` ∈ Reach; **assert zero upstream calls to the excluded member** via the proxy fake | A |
| P-G8 | **chat chain entry exclusion** — a granted model's fallback chain target is NOT served | `pkg/proxy/handler_test.go` (model-access mock `:2105/:2121`) | request for `A` with `FallbackChain=[B]`, token `allowed_models=["A"]` ⇒ zero attempts on `B`; the list seen by the coordinator excludes `B` | A |
| P-G9 | **anthropic chain + member exclusion** | `pkg/proxy/handler_anthropic_test.go`, `pkg/proxy/handler_anthropic_tokenid_parity_test.go:230/:240` | same two assertions on the sequential lane; `arc.modelList` filtered | A |
| P-G10 | **hedge / cred-failover never introduce a model** | `pkg/proxy/cache_layer_separation_test.go:593-622` (existing hedge-boundedness pins) + new assertion | the hedge is `c.models[0]` on a secondary credential; the model id set is unchanged by any credential path | A |
| P-G11 | **re-entry target outside the grant never spawns** | `pkg/proxy` re-entry tests + `pkg/proxy/handler_groups.go` re-entry | after a member failure, the re-offer/rebuild never yields an out-of-reach member; **zero** upstream calls for it | A |
| P-G12 | **image-gen 403** (existing pin) | `pkg/imggen/handler_test.go:672` ("T1.7.2 — IsModelAllowed 403 when token has restricted list") | updated to the shared predicate; same status/body | A |
| P-G13 | **Wildcard-unchanged pin** | `pkg/auth/token_test.go` + `pkg/proxy`/`pkg/decision` | token with both axes ABSENT ⇒ reach everything (chat model, group + all members, decision model + group + members, image-gen) — **bit-for-bit today's behaviour**; assert `authz.Resolve(...) == nil` | A + B |
| P-G14 | **`ScanAllowedModels`/`ScanGrant` malformed row now DENIES** | `pkg/auth/token_test.go:104` (`TestScanAllowedModels`, table `:169-183`) → rewritten as `TestScanGrant` | malformed JSON ⇒ `invalid=true`; **unknown raw type (`123`) ⇒ `invalid=true`**; **empty/whitespace TEXT ⇒ `invalid=true` (fix pass C E2 — NOT absent; only SQL `NULL` is ABSENT)** (today's table expects `[]string{}` at `:121-124` — that row's *expectation* changes); `nil`/`""` ⇒ absent; `"[]"` ⇒ present-empty | A | **ARCHAEOLOGY (preserved, fix pass C):** the original `""` ⇒ absent wording in the historical table is a **superseded pin description — do not implement** (landed `ScanGrant` maps empty TEXT to INVALID).
| P-G15 | **`authz.Resolve` truth table** | new `pkg/authz/scope_test.go` | wildcard (both absent) / restricted (either present, incl. empty) / deny-all (either invalid) / conjunctive proof: models-absent + groups-present ⇒ **restricted, models axis grants nothing** | A |
| P-G16 | **migration 034 forward** | `pkg/store/database/migrations_test.go` (copy `TestMigration031_ForwardFreshDB` `:728` shape; `columnExists` helper `:586`) | fresh DB: `columnExists(auth_tokens, allowed_groups)`; `schema_migrations` row `034` present | A |
| P-G17 | **migration 034 repartition + idempotency + NULL-not-`[]`** | same file | seed 3 tokens (wildcard, group-only, group+model) ⇒ after 034: group-only has `allowed_models IS NULL` + `allowed_groups={"g1"}`; mixed keeps the model id; wildcard **unchanged**; **run 034's UPDATE twice ⇒ second run changes 0 rows** | A |
| P-G18 | **SQLite JSON1 available** | same file (`TestMigration034_SQLiteJSON1Available`) | `json_group_array`/`json_each`/`json_valid` work on the shipped driver | A |
| P-G19 | **store round-trip of both axes + nil-vs-empty** | `pkg/auth/store_test.go` (exists; `newTestDB` `:19-47`, `runTestMigrations` `:50-70`, existing `*AllowedModels` pins `:578/:604/:632/:686/:714/:750/:782`) | `CreateToken` with `AllowedModels=nil, AllowedGroups=["g1"]` ⇒ DB `NULL` + `'["g1"]'`; with `[]` ⇒ `'[]'`; read-back preserves **nil vs empty** | A |
| P-G20 | **decorator-through test** (house rule) | `pkg/modelscache/tokens_test.go` (fake `:57/:104`; stale tiers `:220/:255/:280`) | `CachedTokenStore` forwards the new `OptionalTokenPermission` payload **and drops the cached entry on `UpdateTokenPermission`**; a token validated before a group-grant change returns the NEW grants after it (the drop is not merely called) | A |
| P-G21 | **`/fe/api` contract** | `pkg/ui/handlers_token_test.go` (mock `:15-76`; PATCH tests `:98/:117/:179/:203/:220`) | POST echoes `allowed_groups`; **PATCH key omitted=keep / explicit `null`=keep (identical — Ruling 2 two-state wire) / `[]`=clear / ids=replace**; 400 on a group id that does not exist or is not `kind='group'`; 400 on an unknown `allowed_models` id; all-axes-omitted PATCH ⇒ `200 {"success":true}` with no column UPDATE; POST echo unchanged. (The ">64 KB POST body" assertion is REMOVED — the POST cap was rejected fix-pass 2; the PATCH-side cap at `:2570` is landed fact and stays tested.) | A |
| P-G22 | **no attempt-time skip, no token in the hot loop** | new structural test (`grep`-style, house precedent `TestPackage_ImportGuard_*`) | `pkg/proxy/race_coordinator.go` + `race_executor.go` contain **no** `authz.` import and **no** scope-predicate call; the four lane files contain **no** `IsModelAllowed(` call | A |
| P-G23 | **`policy.go` untouched** | existing guard `TestPackage_ImportGuard_GroupPolicyFiles_NoCacheImports` + a byte-identity check in the gate | `pkg/decision/policy.go` imports only `math/rand` + `pkg/models`; diff vs base = 0 | A |
| P-G24 | **reach-completeness (the `I15` structural test)** | new `pkg/proxy/reach_completeness_test.go` (+ decision sibling) | drives every lane and asserts (a) the list handed to the coordinator (`handler.go:996`) ⊆ Reach, (b) `arc.modelList` ⊆ Reach, (c) every spawned `raceCoordinator` model ∈ Reach, (d) `pickServableGroupMember`'s pick ∈ Reach | A |
| P-G25 | **decorator-forward compile assertion** | `pkg/modelscache` build | `var _ models.GroupRewriterScoped = (*CachedModelsConfig)(nil)` compiles ⇒ a missing forward is a build break, never a silent no-op | A (build) |
| P-G26 | **PATCH keep-invalid (PD-3) — CANONICAL pin for the no-heal behavior** *(C2: the architect's 400-rejection variant is the REJECTED alternative; fix-pass 2 unifies the naming — `TestTokenAPI_PatchNilAxis_DoesNotHealDenyAll` is the canonical test name; aliases: the store-layer half `TestUpdateTokenPermission_OmitsNilColumn`, the retired name `TestPatch_KeepsInvalidAxis_DoesNotHeal`, and phase3 §5.1's PD-3 row, which all point here)* | `pkg/ui/handlers_token_test.go` | omitted axis **or explicit `null`** with `ModelsInvalid`/`GroupsInvalid==true` ⇒ **`200 {"success":true}`**; column NOT updated (byte-untouched); raw bytes preserved; **invalid-omitted NEVER heals** to `NULL`/wildcard | A |
| P-G27 | **D13 entry-gate hoist (PD-8/A8)** | `pkg/proxy/handler_test.go` / `handler_anthropic_test.go` | entry 403 ⇒ zero group-binding/sticky writes (`rc.groupPick`, `applyStickyOrder` promotion, `groupTriedMembers`) | A |
| P-G28 | **Anthropic envelope parity (PD-14)** | `pkg/proxy/handler_anthropic_test.go` | 403 body byte-identical to chat writer (`handler.go:525-535`) | A |
| P-G29 | **Cache-read clone (PD-7)** | `pkg/modelscache/tokens_test.go` | mutating a returned `AllowedGroups` slice does not mutate the cached entry; `ModelsInvalid`/`GroupsInvalid` survive | A |
| P-G30 | **Decision group row copy (PD-15)** | `pkg/decision/group_test.go` | filtering uses a copy; cached `group.Members` unmutated | A |
| P-G31 | **`NewRaceCoordinator` empty-list guard (PD-10)** | `pkg/proxy/race_coordinator_test.go` | `len(models)==0` ⇒ constructor returns error (no `cfg.ModelID` fallback); handler returns 403 | A |
| P-G32 | **Cooldown-skip: Token A's scope exclusion never cools M1 for wildcard Token B** *(canonical name after the fix-pass-2 rewrite — the ledger counts the §13.1 required set under this name; C2/Ruling 1)* | `pkg/store/database/store_group_test.go` + `pkg/decision/handler_group_test.go` | **MANDATED (leader Ruling 1, fix-pass 2).** A scope-excluded candidate is never an attempt ⇒ never a cooldown: assert (a) a scope-refused offer writes NO `ExcludeAndReselect` cooldown row, NO `skipcache.Mark`, NO `st.cooldowns[id]` entry; (b) after Token A's scope exclusion of member M1, wildcard Token B still selects M1 with zero cooldown interference (the multi-token pin); (c) `ExcludeGroupMemberScoped` never writes a cooldown for a scope-refused offer | A |
| P-G33 | **Enabled/disabled precedence (PD-11)** | `pkg/decision/handler_group_test.go` | disabled + out-of-scope request follows the pinned precedence, no scope leak | A |
| P-G34 | **Shadow/claimed alias (PD-9)** | `pkg/decision/handler_group_test.go` + `handler_groups_shadow_member_test.go` | 403 `model` = client-requested claimed string; zero upstream calls | A |

### 13.1a ENUMERATED required-pin set — **PUBLISHED (fix pass B, final; all names VERBATIM, zero elisions)**

**THE single `added == expected` source of truth: 88 Go + 33 FE = 121 pins, ALL LANDED** (79 at `4be0a12` + the **9 commission-named obligation pins landed by `1e65774`** — Phase 1 = 42, Phase 2 = 46, FE = 33). `81e8788` (tidier) added/removed/renamed ZERO pins. **Nothing below is INCOMING any more.** Measured from `git diff a3f0335 4be0a12 -- '*_test.go'` (`+func Test` set) and the four FE test files. Every name below is the **full literal symbol** — no `…_` prefix elision — so this section is copy-pasteable into the gate. Every count elsewhere in the corpus (`phase1 §5`, `phase2 §5`, `phase3 §5/§5.1`, `plan-overview`) points HERE.

**Composition:** Phase 1 + Phase-1.1 + C2/W1 BE = **41**; Phase 2 = **38**; FE = **33** (Go 79 = 41 + 38). The diff also carries **2 renamed/replaced** pre-existing names — `TestScanAllowedModels`→`TestScanGrant_TriState`, `TestNewRaceCoordinatorWithEmptyModels`→`TestRaceCoordinator_EmptyModelList_Refused` — counted once each (under the new name).

#### Phase 1 + 1.1 + C2/W1/PD-2 — 42 Go pins (verbatim)

| # | test name | file |
|---|---|---|
| 1 | `TestCreateToken_GrantAxesRoundTrip` | pkg/auth/store_test.go |
| 2 | `TestCreateToken_ExplicitEmptyIsNotNull` | pkg/auth/store_test.go |
| 3 | `TestValidateToken_SurfacesInvalidFlags` | pkg/auth/store_test.go |
| 4 | `TestUpdateTokenPermission_BothAxes` | pkg/auth/store_test.go |
| 5 | `TestUpdateTokenPermission_OmitsNilColumn` | pkg/auth/store_test.go |
| 6 | `TestUpdateTokenPermission_RoundTrip` | pkg/auth/store_test.go |
| 7 | `TestScanGrant_TriState` | pkg/auth/token_test.go |
| 8 | `TestScanGrant_OnlySQLNullIsAbsent` | pkg/auth/token_test.go |
| 9 | `TestScanAllowedModels_ShimPreservesIDs` | pkg/auth/token_test.go |
| 10 | `TestPackage_ImportGuard_UsesPinnedToolchain` | pkg/authz/guard_test.go |
| 11 | `TestPackage_ImportGuard_AuthzStaysLeaf` | pkg/authz/guard_test.go |
| 12 | `TestPackage_ImportGuard_AuthzLeafConstraint_TransitiveClosure` | pkg/authz/guard_test.go |
| 13 | `TestPackage_ImportGuard_NegativeControlFires` | pkg/authz/guard_test.go |
| 14 | `TestPackage_ImportGuard_AuthzImportsOnlyAuth` | pkg/authz/guard_test.go |
| 15 | `TestAuthzResolve_TruthTable` | pkg/authz/scope_test.go |
| 16 | `TestAuthzResolve_ConjunctiveWildcard` | pkg/authz/scope_test.go |
| 17 | `TestAuthzResolve_NoMembersExpansionForAbsentGroups` | pkg/authz/scope_test.go |
| 18 | `TestAuthzScope_AllowsEmptyID` | pkg/authz/scope_test.go |
| 19 | `TestAuthzResolve_EmptyTextGrantIsDenyAll_NotWildcard` | pkg/authz/scope_test.go |
| 20 | `TestReachFilter_InterfaceShape` | pkg/authz/scope_test.go |
| 21 | `TestCachedTokenStore_ForwardsGroupsAndDropsOnUpdate` | pkg/modelscache/tokens_test.go |
| 22 | `TestCachedTokenStore_CloneCarriesInvalidFlags` | pkg/modelscache/tokens_test.go |
| 23 | `TestCachedTokenStore_ClonePreservesPresentEmpty` | pkg/modelscache/tokens_test.go |
| 24 | `TestMigration034_ForwardFreshDB` | pkg/store/database/migrations_test.go |
| 25 | `TestMigration034_SQLiteJSON1Available` | pkg/store/database/migrations_test.go |
| 26 | `TestMigration034_Repartition` | pkg/store/database/migrations_test.go |
| 27 | `TestMigration034_RepartitionIdempotent` | pkg/store/database/migrations_test.go |
| 28 | `TestMigration034_NullNotEmptyArray` | pkg/store/database/migrations_test.go |
| 29 | `TestMigration034_PGGuardShape` | pkg/store/database/migrations_test.go |
| 30 | `TestMigration034_PGuardShape_DownTwinAndGlobalScan` | pkg/store/database/migrations_test.go |
| 31 | `TestMigration034_SQLiteDialectParity` | pkg/store/database/migrations_test.go |
| 32 | `TestTokenAPI_CreateEchoesGroups` | pkg/ui/handlers_token_test.go |
| 33 | `TestTokenAPI_PatchMergeSemantics` | pkg/ui/handlers_token_test.go |
| 34 | `TestTokenAPI_PatchClearDoesNotEscalate` | pkg/ui/handlers_token_test.go |
| 35 | `TestTokenAPI_Validation` | pkg/ui/handlers_token_test.go |
| 36 | `TestTokenAPI_PatchNonexistent_404` | pkg/ui/handlers_token_test.go |
| 37 | `TestTokenAPI_Validation_MalformedShape` | pkg/ui/handlers_token_test.go |
| 38 | `TestTokenAPI_PatchNilAxis_DoesNotHealDenyAll` | pkg/ui/handlers_token_test.go |
| 39 | `TestTokenAPI_PatchOmittedInvalidAxis_RealStore_ByteIdentical` | pkg/ui/handlers_token_test.go |
| 40 | `TestTokenAPI_InvalidFlagsExposed` | pkg/ui/handlers_token_flags_test.go |
| 41 | `TestTokenAPI_StaleClaimAlias_Rejected` | pkg/ui/handlers_token_flags_test.go |
| 42 | `TestMigration034_PGMalformedRowSkippedNotAbort` | pkg/store/database/migrations_test.go |

#### Phase 2 — 46 Go pins (verbatim)

| # | test name | file |
|---|---|---|
| 42 | `TestExcludeGroupMemberScoped_LegacyFallback_RefusesOutOfReachReplacement` | pkg/modelscache/authz_scoped_fallback_test.go |
| 43 | `TestRewriteGroupEntriesScoped_LegacyFallback_DegradesUnscoped` | pkg/modelscache/authz_scoped_fallback_test.go |
| 44 | `TestChat_ChainEntry_OutOfScope_NeverSpawned` | pkg/proxy/authz_chain_pins_test.go |
| 45 | `TestAnthropic_ChainEntry_OutOfScope_NeverAttempted` | pkg/proxy/authz_chain_pins_test.go |
| 46 | `TestChat_GroupMember_OutOfScope_NeverPicked_SiblingServes` | pkg/proxy/authz_chain_pins_test.go |
| 47 | `TestChat_Reentry_OutOfScopeTarget_NeverSpawned` | pkg/proxy/authz_chain_pins_test.go |
| 48 | `TestAnthropic_Reentry_OutOfScopeTarget_NeverAttempted` | pkg/proxy/authz_chain_pins_test.go |
| 49 | `TestPackage_NoPerAxisLenGuardOutsideAuth` | pkg/proxy/authz_guard_test.go |
| 50 | `TestPackage_ImportGuard_NoIsModelAllowedInLanes` | pkg/proxy/authz_guard_test.go |
| 51 | `TestPackage_NoUltimateEscalation` | pkg/proxy/authz_guard_test.go |
| 52 | `TestPackage_ImportGuard_NoScopeInHotLoop` | pkg/proxy/authz_guard_test.go |
| 53 | `TestLaneContract_ReachResolutionOnlyViaAuthz` | pkg/proxy/authz_guard_test.go |
| 54 | `TestGroupRewriterScoped_ProductionDecoratorForward` | pkg/proxy/authz_guard_test.go |
| 55 | `TestChat_OutOfScopeModel_403_ZeroAttempts` | pkg/proxy/authz_scope_test.go |
| 56 | `TestChat_WildcardToken_Unchanged_200` | pkg/proxy/authz_scope_test.go |
| 57 | `TestChat_GroupGrant_AdmitsMembers_200` | pkg/proxy/authz_scope_test.go |
| 58 | `TestChat_Group_AllMembersOutOfScope_403_Not502` | pkg/proxy/authz_scope_test.go |
| 59 | `TestChat_DynamicMembership_ReflectsImmediately` | pkg/proxy/authz_scope_test.go |
| 60 | `TestChat_DenyAll_InvalidGrantRow_403` | pkg/proxy/authz_scope_test.go |
| 61 | `TestChat_EmptyTextGrantRow_DenyAll` | pkg/proxy/authz_scope_test.go |
| 62 | `TestReachCompleteness_ExecutionListsSubsetOfReach` | pkg/proxy/authz_scope_test.go |
| 63 | `TestAnthropic_Group_AllMembersOutOfScope_403_EnvelopeParity` | pkg/proxy/authz_scope_test.go |
| 64 | `TestAnthropic_OutOfScopeModel_403_ZeroAttempts` | pkg/proxy/authz_scope_test.go |
| 65 | `TestAnthropic_WildcardToken_Unchanged_200` | pkg/proxy/authz_scope_test.go |
| 66 | `TestDecision_Group_NotGranted_403_ZeroAttempts` | pkg/proxy/authz_scope_test.go |
| 67 | `TestDecision_Group_Granted_NotScopeDenied` | pkg/proxy/authz_scope_test.go |
| 68 | `TestImggen_NotGranted_403` | pkg/proxy/authz_scope_test.go |
| 69 | `TestDecisionGroup_FilterDoesNotMutateCachedRow` | pkg/proxy/authz_scope_test.go |
| 70 | `TestHedge_And_CredFailover_NeverIntroduceModel` | pkg/proxy/cache_layer_separation_test.go |
| 71 | `TestRaceCoordinator_EmptyModelList_Refused` | pkg/proxy/race_coordinator_test.go |
| 72 | `TestPackage_ImportGuard_StoreDatabaseNoAuthImports` | pkg/store/database/authz_import_guard_test.go |
| 73 | `TestScopeExclusion_NeverWritesCooldown_MultiToken` | pkg/store/database/store_group_scope_test.go |
| 74 | `TestGroupScopeExclusion_AllMembersOutOfScope_ScopeExcluded` | pkg/store/database/store_group_scope_test.go |
| 75 | `TestGroupScopeExclusion_AllMembersDisabled_NotScopeExcluded` | pkg/store/database/store_group_scope_test.go |
| 76 | `TestGroupExcludeScoped_ScopeOnlySignal` | pkg/store/database/store_group_scope_test.go |
| 77 | `TestGroupScopeExclusion_NeverPicked_AnyPhase` | pkg/store/database/store_group_scope_test.go |
| 78 | `TestFallbackPositionGroup_Unreachable_Dropped` | pkg/store/database/store_group_scope_test.go |
| 79 | `TestIndexZeroGroup_Unreachable_FailsWithScopeExcluded` | pkg/store/database/store_group_scope_test.go |
| 80 | `TestDecision_DisabledVsOutOfScope_NoLeak` | pkg/decision/handler_group_test.go |
| 81 | `TestDecision_Precedence_401_404_400_403_502` | pkg/decision/handler_test.go |
| 82 | `TestChat_FallbackPositionGroup_NoPhantomPick` | pkg/proxy/authz_claim_pins_test.go |
| 83 | `TestChat_ShadowClaimed_403QuotesClaimedString` | pkg/proxy/authz_claim_pins_test.go |
| 84 | `TestChat_ShadowClaimed_AliasOnlyToken_403` | pkg/proxy/authz_claim_pins_test.go |
| 85 | `TestAnthropic_ShadowClaimed_AliasOnlyToken_403` | pkg/proxy/authz_claim_pins_test.go |
| 86 | `TestChat_ShadowClaimed_GroupGrant_AdmitsClaimedRequest` | pkg/proxy/authz_claim_pins_test.go |
| 87 | `TestChainGroupEntry_NeverAdmitted_No502` (entry-chain group EXPANDS to its member; raw group id absent from `res.ModelList`; pick = (group, member); 200 after the primary 500s) | pkg/proxy/authz_claim_pins_test.go |

#### Phase 3 — 33 FE pins (verbatim `it(...)` titles)

| # | case title | file |
|---|---|---|
| 91 | `TokenForm_PickerIncludesDecisionAndImageGenModels` | TokenForm.test.tsx |
| 92 | `TokenForm_PickerExcludesGroupModels` | TokenForm.test.tsx |
| 93 | `TokenForm_GroupPickerEmitsAllowedGroups` | TokenForm.test.tsx |
| 94 | `TokenForm_GroupIdNeverEmittedUnderAllowedModels` | TokenForm.test.tsx |
| 95 | `TokenForm_DecisionModelEmitsUnderAllowedModels` | TokenForm.test.tsx |
| 96 | `TokenForm_UnrestrictedSendsNullAxis` | TokenForm.test.tsx |
| 97 | `TokenForm_RestrictedEmptySendsEmptyArray` | TokenForm.test.tsx |
| 98 | `TokenForm_RestrictedSelectionSendsIdArray` | TokenForm.test.tsx |
| 99 | `TokenForm_ClearingGroupsNeverSendsNull` | TokenForm.test.tsx |
| 100 | `TokenForm_EditModeCannotPromoteAxisToUnrestricted` | TokenForm.test.tsx |
| 101 | `TokenForm_InvalidAxisRendersDenyAllWarning` | TokenForm.test.tsx |
| 102 | `TokenForm_AxisWithoutInvalidFlagIsNotReportedInvalid` | TokenForm.test.tsx |
| 103 | `TokenForm_EditModeKeepsStaleGroupIdOnGroupsAxis` | TokenForm.test.tsx |
| 104 | `axisOfId: target-only group id stored in allowed_models stays on models axis` | grantAxes.test.tsx |
| 105 | `axisOfId: stored group id stays on groups axis` | grantAxes.test.tsx |
| 106 | `axisOfId: a NEW group id (not stored) classifies by kind onto groups axis` | grantAxes.test.tsx |
| 107 | `TokenList_TargetOnlyGroupId_SurvivesNoChangeSave` | grantAxes.test.tsx |
| 108 | `TokenForm_NewGroupSelection_LandsOnGroupsAxis` | grantAxes.test.tsx |
| 109 | `TokenList_BothAxes_NoChangeSave_KeepsIdOnBothAxes` | grantAxes.test.tsx |
| 110 | `TokenList_CrossAxisAddition_IsPreserved` | grantAxes.test.tsx |
| 111 | `TokenList_DuplicateStoredIds_AreDedupedPerAxis` | grantAxes.test.tsx |
| 112 | `TokenList_BadgeShowsWildcardOnlyWhenBothAxesAbsent` | TokenList.test.tsx |
| 113 | `TokenList_SaveSplitsAxesAndRoundTripsBoth` | TokenList.test.tsx |
| 114 | `TokenList_InvalidAxisRendersDenyAllWarning` | TokenList.test.tsx |
| 115 | `TokenList_GroupChipsAreDistinctFromModelChips` | TokenList.test.tsx |
| 116 | `useApi_CreateToken_SendsBothAxesNullWhenNothingSelected` | useApi.token-permission.test.ts |
| 117 | `useApi_CreateToken_SendsEmptyArrayForRestrictedEmptyAxis` | useApi.token-permission.test.ts |
| 118 | `useApi_CreateToken_SendsIdArrayForRestrictedAxis` | useApi.token-permission.test.ts |
| 119 | `useApi_UpdateTokenPermission_SendsEmptyArrayToClearAxis` | useApi.token-permission.test.ts |
| 120 | `useApi_UpdateTokenPermission_OmitsUntouchedAxis` | useApi.token-permission.test.ts |
| 121 | `useApi_UpdateTokenPermission_ExplicitNullMeansKeep` | useApi.token-permission.test.ts |
| 122 | `useApi_UpdateTokenPermission_SendsIdArrayToReplace` | useApi.token-permission.test.ts |
| 123 | `useApi_UpdateTokenPermission_OptimisticMergeUpdatesBothAxes` | useApi.token-permission.test.ts |

#### Reconciliation — final disposition (all names resolved at `1e65774`)

| Earlier (speculative / INCOMING) name | Final disposition |
|---|---|
| `TestDecision_Precedence_401_404_400_403_502`, `TestDecision_DisabledVsOutOfScope_NoLeak`, `TestChat_FallbackPositionGroup_NoPhantomPick`, `TestChat_ShadowClaimed_403QuotesClaimedString`, `TestChat_ShadowClaimed_AliasOnlyToken_403`, `TestAnthropic_ShadowClaimed_AliasOnlyToken_403`, `TestChat_ShadowClaimed_GroupGrant_AdmitsClaimedRequest`, `TestChainGroupEntry_NeverAdmitted_No502`, `TestMigration034_PGMalformedRowSkippedNotAbort` | **LANDED — committed in `1e65774`** (4 test files, +1589) under these canonical names; enumerated above (Go rows 42 + 80–87). Non-vacuity proven (16 mutations, incl. 8 production injections). |
| `TestConversationKey_TokenSalting` | **PRE-EXISTING** (not added): `TestComputeConversationKey_TokenSalting` already existed at `pkg/proxy/race_coordinator_credfailover_test.go:475` — not in `expected`. |
| `TestConversationKey_MidBindingRevocation` | **DEFERRED** (follow-up — the 30-minute absolute binding TTL is pinned elsewhere); not in `expected`. |

**Gate rule:** the `+func Test` set must equal these **88 Go** names and the vitest log these **33 FE** case titles, each exactly once (`--- PASS` / no `×`), 0 MISS / 0 unlisted / 0 absent.

### 13.2 Interface mocks that must be re-signed (9 files, compiler-enumerated) + 8 DDL fixtures

`pkg/decision/handler_test.go:296/:341` · `pkg/imggen/handler_test.go:175/:220` · `pkg/mcp/auth_test.go:29/:45` · `pkg/modelscache/tokens_test.go:57/:104` · `pkg/proxy/authenticate_test.go:47/:67` **and** `:348/:364` (slow mock) · `pkg/proxy/handler_anthropic_tokenid_parity_test.go:230/:240` · `pkg/proxy/handler_test.go:2105/:2121` · `pkg/ui/handlers_imggen_test.go:740/:764` · `pkg/ui/handlers_token_test.go:35/:66`.
DDL fixtures: the **8** in §5.7.

**Why extend the interface instead of an optional seam (Q5 (a) adopted):** the compiler enumerates every implementer ⇒ a missed mock is a **build break**, not a silent gap (the vacuity risk of an optional interface, `decisions.md` R-5). The optional-seam pattern is used only where the compiler cannot help and the stubs must stay valid (`GroupRewriterScoped`), and there it is backed by a compile-time assertion (P-G25).

### 13.3 Anti-vacuous rule (house, mandatory)

Every **added** `func Test…` must show a literal `--- PASS` line **in the gate's own logs**, matched **exactly once**; every added FE case token must appear **exactly once**. A MISS = gate failure (`.agents/tester/PACKS.md:15/:18/:20`, `RESULTS/decision-groups-gate.md:79-84`). Names added but never executed (0 `--- PASS`) are the classic vacuity failure. The gate must also prove **expected == added** (set arithmetic against `$BASE`), as in the 2026-11-17 gate (`74 expected = 74 added; 0 unlisted`).

---

## 14. Ship + rollback plan

### 14.1 Landing recipe (house; `master` is HELD by the main checkout)

Work in the **ephemeral sibling worktree off REMOTE tips** (`../llm-supervisor-proxy-wt-token-authz`, already created: branch base `a3f0335`).

1. Publish `latest`: `git merge --no-ff origin/latest`; `git push origin <merge-sha>:refs/heads/latest` (explicit-SHA refspec ⇒ always fast-forward regardless of local staleness).
2. Promote to `master`: **in the same worktree**, `git checkout --detach origin/master` → `git merge --no-ff origin/latest` → gate on **exact parents** (parent1 = prior master tip, parent2 = latest tip) **and tree identity** (`tree(promo) == tree(latest tip)`) → `git push origin <promo-sha>:refs/heads/master` (explicit SHA ⇒ FF vs prior master).
3. Cleanup: remove the worktree; `git branch -d` the temp branch.
Hazards: never merge in the main checkout (it HOLDS `master` + `.agents/` edits); always base merges on **origin** refs; a worktree `.git` is a pointer file (never `cp -a` to `/tmp` and run git there).

### 14.2 Gate sequence

| Step | What | Notes |
|---|---|---|
| G0 | **PRE-SHIP READ-ONLY DB INSPECTION (both hosts)** — see §14.3 | before the fail-closed flip |
| G1 | `go build ./...` + `go vet ./...` + gofmt zero-new-drift | `go build ./cmd` is the real entry (~26 MB); root `test_load.go` is a stub |
| G2 | **Pack A** `./pkg/proxy/... ./pkg/store/... ./pkg/decision/... ./pkg/ui/... ./cmd/...` (9 pkgs), `-timeout=240s` inside `timeout 300`, **HOME-quarantined** (`export HOME=$(mktemp -d)`, unset `DATABASE_URL`/`PORT`), config.json hash-guarded against the **absolute real-home path** | `PACKS.md:27-28`, `:36`, `:9-14` | **ENV PRECONDITION (fix pass C):** `TestPackage_ImportGuard_AuthzLeafConstraint_TransitiveClosure` (`pkg/authz/guard_test.go:151`) now `t.Fatalf`s (mechanical pass) ⇒ run with `GOPATH=/home/nea/go GOMODCACHE=/home/nea/go/pkg/mod`; `pkg/authz` is NOT in Pack A ⇒ G2's quarantine does not cover it (it runs in Pack B).
| G3 | **Pack B** module-anchored complement (34 pkgs; keep the `(/|$)` suffix) | sole expected failure = the quarantined httpbin redirect pair (`QUARANTINE.md:11`, deterministic) — adjudicate, do not "fix" |
| G4 | **FE**: `npx vitest run` (all green) + `tsc --noEmit` **exact** baseline parity (zero NEW error locations) + `vite build` | `PACKS.md:62`, `:254`, `:262` |
| G5 | **Anti-vacuous** proof (Go + FE), set arithmetic vs `$BASE` | §13.3 |
| G6 | **BOUNDARY DECLARATION — pre-declared to the tester** (§14.4) | avoids a burned gate cycle (`decisions.md` R-9) |
| G7 | Boot smoke in isolation: `/healthz`, `/fe/api/version`, `/fe/api/models` 200/200/200, zero panics, clean SIGTERM, real config hash unchanged | house gate |
| G8 | **Perf sanity**: p99 on chat + decision lanes vs a pre-change capture (no numeric budget supplied — assert "no visible regression") | §4.4 |

### 14.3 G0 — the pre-ship read-only DB inspection gate (BOTH hosts)

Read-only, no writes, no token values printed (paths/ids only):

```sql
-- 0. PG major version (determines which guards are available; the
--    migration itself is version-agnostic via the DO $$ block, PD-2).
SELECT version();

-- 1. Which tokens name a GROUP id somewhere in allowed_models? (their
--    effective reach changes form at 034 — must be known BEFORE the deploy).
--    Version-agnostic: the cast is guarded per row.
DO $$
DECLARE r RECORD;
BEGIN
  FOR r IN
    SELECT t.id, t.name, t.created_by, t.allowed_models
      FROM auth_tokens t
     WHERE t.allowed_models IS NOT NULL AND btrim(t.allowed_models) <> ''
       AND t.allowed_models ~ '^\s*\['
  LOOP
    BEGIN
      IF EXISTS (SELECT 1 FROM jsonb_array_elements_text(r.allowed_models::jsonb) v
                  WHERE v IN (SELECT id FROM models WHERE kind='group')) THEN
        RAISE NOTICE 'group-named token: % % %', r.id, r.name, r.allowed_models;
      END IF;
    EXCEPTION WHEN invalid_text_representation THEN
      RAISE NOTICE 'malformed allowed_models row: % %', r.id, r.name;
    END;
  END LOOP;
END $$;

-- 2. Any row whose grant columns are UNREADABLE? (the fail-closed flip
--    would turn it from full-access into deny-all — must be known, and
--    named in the ship note). Version-agnostic via DO $$.
--    PAIRED with query 1's per-row EXCEPTION shape (fix-pass 2, yellow-8):
--    the regex pre-filter alone can MISS a '['-prefixed malformed row, so
--    every '['-prefixed row is also cast inside the guard (the PERFORM
--    ::jsonb arm below) — a '[broken' row is CAUGHT by the cast attempt,
--    never skipped silently by the regex.
DO $$
DECLARE r RECORD;
BEGIN
  FOR r IN SELECT id, name, allowed_models, allowed_groups FROM auth_tokens LOOP
    BEGIN
      IF r.allowed_models IS NOT NULL AND btrim(r.allowed_models) <> ''
         AND NOT (r.allowed_models ~ '^\s*\[') THEN
        RAISE NOTICE 'unreadable allowed_models: % %', r.id, r.name;
      END IF;
      IF r.allowed_groups IS NOT NULL AND btrim(r.allowed_groups) <> ''
         AND NOT (r.allowed_groups ~ '^\s*\[') THEN
        RAISE NOTICE 'unreadable allowed_groups: % %', r.id, r.name;
      END IF;
      -- also catch '['-prefixed malformed rows
      IF r.allowed_models IS NOT NULL AND btrim(r.allowed_models) <> ''
         AND r.allowed_models ~ '^\s*\[' THEN
        PERFORM r.allowed_models::jsonb;
      END IF;
      IF r.allowed_groups IS NOT NULL AND btrim(r.allowed_groups) <> ''
         AND r.allowed_groups ~ '^\s*\[' THEN
        PERFORM r.allowed_groups::jsonb;
      END IF;
    EXCEPTION WHEN invalid_text_representation THEN
      RAISE NOTICE 'malformed grant row: % %', r.id, r.name;
    END;
  END LOOP;
END $$;

-- 3. Lesson-learned from decisions.md G3: classify `smoke-15m`.
SELECT id, name, allowed_models FROM auth_tokens ORDER BY created_at;

-- 4. PD-9: cross-check live tokens referencing shadow_model_id names.
SELECT t.id, t.name, t.allowed_models, t.allowed_groups
FROM auth_tokens t
WHERE t.allowed_models IS NOT NULL
  AND EXISTS (SELECT 1 FROM jsonb_array_elements_text(
              CASE WHEN t.allowed_models ~ '^\s*\[' THEN t.allowed_models::jsonb ELSE '[]'::jsonb END) v
              WHERE v IN (SELECT shadow_model_id FROM models WHERE shadow_model_id IS NOT NULL))
   OR t.allowed_groups IS NOT NULL
  AND EXISTS (SELECT 1 FROM jsonb_array_elements_text(
              CASE WHEN t.allowed_groups ~ '^\s*\[' THEN t.allowed_groups::jsonb ELSE '[]'::jsonb END) v
              WHERE v IN (SELECT shadow_model_id FROM models WHERE shadow_model_id IS NOT NULL));
```
### OI-1 — G0 dual-flavor (PostgreSQL + SQLite) + schema anchors (CG-1/OI-1)

The live hosts run **PostgreSQL** (`DATABASE_URL` set → `pkg/store/database.InitializeAll`), but every dev/test DB is **SQLite** (`~/.config/llm-supervisor-proxy/config.db` or `file::memory:`), and the migration must be provable on both. G0 therefore carries **both flavours** for every query that casts a grant column. All casts are **per-row guarded** (`DO $$ … BEGIN … EXCEPTION WHEN invalid_text_representation THEN … END $$` on PG; `json_valid`/`NOT GLOB` guards on SQLite) — a `[`-prefixed malformed row must **print a row, not abort the inspection** (PD-2).

**Schema anchors (read these, never `pkg/store/database/sqlc/schema.sql` — it is STALE and lacks `allowed_models`/`kind`/`members_json`/`shadow_model_id`):**
- `auth_tokens` DDL = migrations **004** (base) + **022** (adds `allowed_models TEXT`, NULL = absent — the **only** grant column today); migration **034** adds `allowed_groups TEXT DEFAULT NULL`.
- `models`: `kind` added by **030**; `members_json` + `shadow_model_id` added by **031** (both TEXT). `models.id` is the single PK shared by group and model rows (`migrations/sqlite/001_initial.up.sql:19-20`; `pkg/models/config.go:351`).

**PG version check (run first, record verbatim):**
```sql
SHOW server_version;   -- (or: SELECT version();)
```
*Why:* `json_array_elements_text` + `jsonb` require PG ≥ 9.4; the guards are version-agnostic by ruling (no `pg_input_is_valid`), but the recorded version tells a future reader which server the inspection ran against and prevents a PG-16-only optimization from being silently assumed.

**SQLite twin (dev `config.db`):** queries 1 & 2 below use `json_each` / `json_valid` / `NOT GLOB` instead of `jsonb_array_elements_text` / `::jsonb` / regex `~`. (JSON1 availability is already CI-pinned by `TestMigration034_SQLiteJSON1Available`, §plan 5.5 — cross-ref.)

**Query A — live `allowed_models` → group reference (BOTH flavours):** which live tokens name a GROUP id in `allowed_models` (their effective reach changes form at 034)?
- PG (per-row guarded, as in query 1 above, verbatim from §14.3).
- SQLite:
```sql
SELECT t.id, t.name, t.allowed_models
FROM auth_tokens t
WHERE t.allowed_models IS NOT NULL AND btrim(t.allowed_models) <> ''
  AND json_valid(t.allowed_models)
  AND EXISTS (SELECT 1 FROM json_each(t.allowed_models) v
              WHERE v.value IN (SELECT id FROM models WHERE kind = 'group'));
```

**Query B — unreadable grant columns (BOTH flavours):** malformed-JSON scan.
- PG: as query 2 above, `DO $$ … EXCEPTION WHEN invalid_text_representation`.
- SQLite:
```sql
SELECT id, name, allowed_models, allowed_groups
FROM auth_tokens
WHERE (allowed_models IS NOT NULL AND btrim(allowed_models) <> ''
       AND NOT json_valid(allowed_models))
   OR (allowed_groups IS NOT NULL AND btrim(allowed_groups) <> ''
       AND NOT json_valid(allowed_groups));
```

**Query C — live `allowed_models` / `allowed_groups` → shadow/claimed reference (BOTH flavours, PD-9):** which live tokens reference a `models.shadow_model_id` value at all?
- PG: as G0-4 in §phase3 T3.10 (per-row guarded `DO $$`).
- SQLite:
```sql
SELECT t.id, t.name, t.allowed_models, t.allowed_groups, m.id AS group_id, m.shadow_model_id
FROM auth_tokens t
JOIN models m ON m.shadow_model_id IS NOT NULL AND m.shadow_model_id <> ''
WHERE (t.allowed_models IS NOT NULL AND json_valid(t.allowed_models)
       AND EXISTS (SELECT 1 FROM json_each(t.allowed_models) v WHERE v.value = m.shadow_model_id))
   OR (t.allowed_groups IS NOT NULL AND json_valid(t.allowed_groups)
       AND EXISTS (SELECT 1 FROM json_each(t.allowed_groups) v WHERE v.value = m.shadow_model_id));
```

**Query D — full token inventory** (classify `smoke-15m`): `SELECT id, name, allowed_models, allowed_groups FROM auth_tokens ORDER BY created_at;` (flavour-agnostic).

**Query E — pre-existing explicit-empty rows (fix-pass A note — REQUIRED, feeds the Tier-1 Step-0 rollback step):** enumerate tokens already carrying an explicit-empty axis (these become WILDCARDS under the old binary and are SKIPPED by the reverse repartition's `NOT IN ('', '[]')` predicate): `SELECT id, name, allowed_models, allowed_groups FROM auth_tokens WHERE allowed_models = '[]' OR allowed_groups = '[]';` (flavour-agnostic; runs on both hosts, read-only). Record the result in the ship artifact — the Tier-1 Step-0 list is exactly this set.

**Query F — object-valued JSON shapes (fix pass B, C5 — REQUIRED):** the SQLite `034` migration guards malformed rows with `json_valid` + `EXISTS(group id)`, but has **no `json_type='array'` check** — so an **object-valued** grant (`'{}'`, `'{"g1":"g1"}'`) passes `json_valid` and is silently left untouched (production PG is guarded by the `DO $$ … EXCEPTION` cast, but the dev dialect is not). Enumerate them pre-ship: `SELECT id, name, allowed_models, allowed_groups FROM auth_tokens WHERE (allowed_models IS NOT NULL AND btrim(allowed_models) <> '' AND json_valid(allowed_models) AND json_type(allowed_models) <> 'array') OR (allowed_groups IS NOT NULL AND btrim(allowed_groups) <> '' AND json_valid(allowed_groups) AND json_type(allowed_groups) <> 'array');` (SQLite; PG twin per-row `DO $$`-guarded). **These rows are DENY-ALL under the landed `ScanGrant` (non-array JSON ⇒ INVALID)** — name them in the ship note; they are safe (deny-all), but the operator should know which tokens flip. **Frozen-SQL rule: this is a G0 query, NEVER a 034 edit.**

**Query G — legacy empty-string grant rows (fix pass B, E1 — REQUIRED):** under `f9adbce` an empty/whitespace `''` column is **INVALID / DENY-ALL** (previously read as ABSENT ⇒ wildcard). The ship-prep measured **0** for `'[]'`, but the `''` count is the mandatory new pre-ship check: `SELECT COUNT(*) FROM auth_tokens WHERE (allowed_models IS NOT NULL AND btrim(allowed_models) = '') OR (allowed_groups IS NOT NULL AND btrim(allowed_groups) = '');` (and the full-row variant `SELECT id, name FROM auth_tokens WHERE …` to name them). Zero ⇒ clean; ≥1 ⇒ name the token in the ship note (it is a **fail-closed tightening** — a previously-wildcard-ish token becomes DENY-ALL and must be repaired explicitly).

**Pass criteria (inspection, not a gate):** G0-1 names the affected tokens; G0-2 names every unreadable row (zero ⇒ clean); **G0-4 must return its rows or "none" — never an aborted run** (the per-row guard guarantees completion).

Expected (live inventory 2026-11-17): PRIMARY 5 tokens — narrow 2 (`smoke-primary-agentic`→`[agentic-group]`, `smoke-primary`→`[coding-group, quick-group]`) + wildcard 3 (`Ha`, `tuyen`, `kha`); DAODUC 6 (same + `smoke-15m`). `smoke-15m`'s class is **not** recorded in the commission (`decisions.md` G3) ⇒ query 3 settles it before the deploy. **No row should match the malformed check (query 2)**; if one does, it is reported in the ship note and the deploy proceeds (the flip is a tightening), but the token is flagged for the owner. **Query 4 (shadow cross-check) must return zero rows** or the affected tokens are named in the release note (PD-9).

### 14.4 Boundary declaration to the tester (mandatory, before the gate)

`.agents/tester/PACKS.md:35` asserts **0 changed files** under `pkg/store/database/migrations/` and `pkg/store/database/migrate.go`. This lane **intentionally** changes exactly those. Pre-declare:

> **Authorized boundary changes for this lane:** `pkg/store/database/migrations/sqlite/034_add_allowed_groups.up.sql`, `…/034_add_allowed_groups.down.sql`, `pkg/store/database/migrations/postgres/034_add_allowed_groups.{up,down}.sql`, and the registry append in `pkg/store/database/migrate.go` (after `:82`). **Unauthorized:** `k8s/`, `.github/`, `pkg/config/` — must remain 0 files.

### 14.5 Deploy sequence (standing grant; NO approval hold)

| # | Step | Gate / probe |
|---|---|---|
| D0 | Merge → `latest` → `master` (§14.1) | tree identity + parent assertions |
| D1 | **WAIT for the CI build** (do not proceed on a queued build) | CI green |
| D2 | Verify the **image digest registry-side** (not from the CI log alone) | digest match, all tags |
| D3 | **Deploy daoduc FIRST, backup before** | backup artifact recorded |
| D4 | **Quick probe daoduc**: `/fe/api/version` == the new build number; `/healthz`; one `/v1/models`; one decision-group request with a granted token | probe log |
| D5 | **Deploy ensem (primary)** | 0 restarts |
| D6 | **Quick probe primary**: same as D4 + one 403 probe with a narrow token naming a non-granted model | probe log |
| D7 | **Push dcs pins (two-step, daoduc-first)** | pins = highest deployed; re-fetch/rebase/retry, **never force** |

**Migration runs in-process at boot** (`pkg/store/database/init.go:19` → `RunMigrations`) — there is no migrate Job. Boot order: migrations → managers → cache priming ⇒ the column and the repartition are visible to the first request, with zero staleness.

**Cross-lane rules:** never downgrade a host; dcs push = re-fetch/rebase/retry (never force); pins = highest deployed.

### 14.6 Post-deploy verification (required evidence)

1. Both hosts' `/fe/api/version` == the shipped build.
2. `SELECT ... FROM schema_migrations WHERE version='034'` present on both.
3. The narrow tokens **still serve their groups** (D6 probe) — proving the repartition preserved reach.
4. A wildcard token still reaches every kind (P1).
5. An out-of-scope request returns **403 access_denied** with the client-requested id (fence probe).
6. Zero 502 `group_unservable` regressions in the first minutes of traffic.

### 14.7 Rollback anchors

**Two facts must never be conflated:** the **SCHEMA** (the added column) is additive and schema-safe; the **DATA** (the repartition) is *not* neutral, because it moved group ids out of `allowed_models` — the one column the pre-034 binary reads. The rollback rows below are therefore split by what they actually touch, and the ORDER inside Tier-1 is load-bearing.

| Tier | Action | Safety / ordering |
|---|---|---|
| **Tier-1** | **reverse repartition FIRST, then image / helm rollback, on the affected host** (daoduc helm rev N-1; primary `kubectl set image` to the prior digest). **Step 1 = the operator-run, idempotent reverse UPDATE** (the one drafted in the down file, §5.2: *"REVERSE the data repartition (deterministic: group ids go back into `allowed_models`)"*), **Step 2 = the image/helm rollback.** | **Order is load-bearing — this is a PROHIBITION, not a preference.** Under the **new** binary a reversed row has `allowed_models = [group ids]` and `allowed_groups` absent ⇒ `allowed_models` is PRESENT ⇒ **not** a wildcard under the conjunctive rule ⇒ the two narrow tokens stay **RESTRICTED** (they 403 on member requests — a narrow, operator-owned, single-hop smoke-token impact) and **no privilege is widened**; the image rollback then restores the **original** semantics exactly (old binary + original data = pre-change behaviour). **FORBIDDEN ORDER: image rollback FIRST** — the pre-034 binary never reads `allowed_groups` and its `IsModelAllowed` treats `len(AllowedModels)==0` as **wildcard** (`pkg/auth/token.go:116`), so with the repartition applied and the OLD image running, `smoke-primary` and `smoke-primary-agentic` would become **UNRESTRICTED — full access to every model** on a public `/v1/*` endpoint. That is precisely the defect class this commission closes; the rollback path must never open it. |
| **Tier-2** | restore the pre-deploy DB backup (the daoduc backup from D3 / primary's equivalent), **then** the image/helm rollback | Reverses the **schema addition and the data repartition together**; then redeploy the prior image. This is the cleanest recovery and the only tier that restores the *data* without a hand-written reverse UPDATE. **GAP RECORDED (fix-pass A): Tier-2 has NO primary-side backup anchor** — the D3 backup exists on daoduc only; the primary's "equivalent" is unverified and must be established (or explicitly waived in the runbook) before Tier-2 is claimed available on the primary. |
| **Tier-3** | operator-run `034_add_allowed_groups.down.sql` **+ delete the `schema_migrations` row `034`** | **Requires the image rollback as well**: the down file **drops the column**, and the **new** binary selects `allowed_groups` — it cannot run against a schema without the column. Order: reverse repartition (or backup restore) → image rollback → down file + version-row deletion. No down-runner exists; the version row is the hygiene step (§5.6). Tier-3 is for a partial-failure recovery or a deliberate schema retreat, not for a routine image rollback. |

**SCHEMA-ONLY statement (scoped, and not to be read as a data statement):** the **column** `auth_tokens.allowed_groups` is additive with a NULL default; the pre-034 binary never selects it ⇒ **no schema rollback is ever required for an image rollback**. That is a statement about the SCHEMA alone — it says nothing about the repartitioned rows, which is exactly what Tier-1's ordering rule exists to handle.

**C4 — DEPLOY STRATEGY FOR THIS ROLLOUT: leader-ruled option (a) — `Recreate` / `maxSurge: 0` per host (fix pass B, item 8).** A standard **RollingUpdate overlap window** leaves a **pre-034 old pod** serving alongside the new one, reading repartitioned rows (`allowed_models = NULL`, `allowed_groups = [...]`) as **WILDCARDS** — the exact defect class this commission closes. Ruled mitigation:

1. **daoduc FIRST** with a Helm strategy override (`Recreate` / `maxSurge: 0`) for **THIS rollout only**.
2. **Verify green** — including a **G0 re-run** and the probes.
3. **primary** next, via a `data-center-scripts` `deployment.yaml` **strategy edit** (`Recreate` / `maxSurge: 0`).
4. During each brief pod swap the **other host absorbs traffic** per the standing failover.
5. **Revert both hosts to `RollingUpdate`** once both are green.
6. **Rollback ordering unchanged:** Tier-1 Step-0 `'[]'` reconcile → Step-1 034 reverse repartition FIRST → then image/helm rollback.

**Why (a) and not (b)/(c):** option (b) required **live-store token mutations** (the ask-back class — forbidden); option (c) churned the **frozen 034 SQL**. (a) needs no data mutation and no SQL edit — only a deployment-strategy change that is reverted after the rollout.

**Tier-1 Step-0 — post-ship `'[]'` enumerate-and-reconcile (fix-pass 2, 🟡1 — runs BEFORE both Tier-1 steps):** the two-state PATCH contract (Ruling 2) means a token whose axis was **explicitly cleared** post-ship carries `allowed_models = '[]'` / `allowed_groups = '[]'` — **PRESENT-empty under the new binary, but `len(AllowedModels)==0` ⇒ WILDCARD under the old binary**. The reverse repartition's `WHERE allowed_models NOT IN ('', '[]')`-style predicate **skips** `'[]'` rows, so an image rollback after a reverse would leave them as **full wildcards on the old binary**. Required runbook step (BEFORE Step-1's reverse and the image rollback): enumerate `SELECT id, name FROM auth_tokens WHERE allowed_models = '[]' OR allowed_groups = '[]';` and **reconcile each named token** (re-create with the intended grants, or revoke) BEFORE rolling back the image. The enumerate query itself is quoted verbatim in the phase-3 runbook (T3.13).

**Commission-time rollback anchors (fix-pass 2, 🟡10 — recorded at ship kickoff):** the fleet is at **v202 `@ 0082773`** (CI #202, digest index `sha256:a6a9f218…`); **daoduc helm rev 46**, **tier-1 rollback image `:3cf3cb8` (v200)**, **primary gen 41**. These are the anchors the §14.5 D-series and the runbook name for tier-1/tier-2 recovery (Ruling 6's named recovery path, §5.6 item 5).

---

## 15. Blast radius (live tokens, per host, before/after)

| Host | Token | Grants today | Behaviour BEFORE | Behaviour AFTER (with 034's repartition) | Verdict |
|---|---|---|---|---|---|
| **PRIMARY** | `Ha` | wildcard | all models | all models | **UNCHANGED** (P1) |
| **PRIMARY** | `tuyen` | wildcard | all models | all models | **UNCHANGED** |
| **PRIMARY** | `kha` | wildcard | all models | all models | **UNCHANGED** |
| **PRIMARY** | `smoke-primary-agentic` | `allowed_models=["agentic-group"]` | reaches the group and (implicitly) **every member + member chain** | `allowed_models=NULL`, `allowed_groups=["agentic-group"]` ⇒ reaches the group **and its current members**; **member CHAINS are no longer implicit** (N3) | **reach preserved for members; chains narrowed** — this is the commission's intent (`D5`), and the smoke token's requests are single-hop |
| **PRIMARY** | `smoke-primary` | `allowed_models=["coding-group","quick-group"]` | both groups + all their members + member chains | `allowed_models=NULL`, `allowed_groups=["coding-group","quick-group"]` ⇒ both groups + their current members; member chains no longer implicit | as above |
| **DAODUC** | the same 5 | as above | as above | as above | as above |
| **DAODUC** | `smoke-15m` | **UNKNOWN class** (`decisions.md` G3) | — | determined by G0 query 3 | **must be classified before the deploy**; if wildcard ⇒ unchanged; if narrow ⇒ same reasoning as the two above |
| both | 2 decision models / 6 groups (primary) / 5 (daoduc) | — | — | membership **definitions untouched**; only token→membership reach changes | unchanged |

**How this table relates to the rollback (§14.7 / `phase3-plan.md` T3.13) — read this before treating the table as a rollback statement.** The table's "Behaviour AFTER" column describes the **NEW binary's** behaviour with the repartition applied, and that is the **steady state this lane intends**: the narrowed member CHAINS are correct and intended (`D5`/`N3` — chains are not inherited) and are **not** a rollback artefact. The rollback path lives on a **separate axis**: the reverse repartition + image rollback (§14.7 Tier-1) restores the **ORIGINAL semantics under the OLD binary** (old binary + original data = pre-change behaviour, chains implicit again) — it does **not** change what the NEW binary does, and the NEW binary's behaviour is **unchanged** by the existence of a rollback plan. The one thing the rollback must never do is run the image rollback **before** the reverse repartition: the old binary reads `allowed_models` only and maps its absence to **wildcard** (`pkg/auth/token.go:116`), so an un-reversed repartitioned row would make `smoke-primary` / `smoke-primary-agentic` **full-access tokens** on a public `/v1/*` endpoint.

**Recommended pre-ship data step (NOT an operator re-scope, NOT a gate):** run G0 (§14.3) and record the inventory in the ship artifact. **There is no operator runbook step and no approval hold** — the `034` data repartition performs the re-scope atomically at boot with **zero window**: by the time the host serves traffic, every row is already in the new form. `decisions.md` Q2 (b) adopted; the earlier operator-PATCH-step idea is **withdrawn/superseded** and is not planned anywhere in this document. (The **reverse** repartition is planned only as the **first step of the Tier-1 rollback**, §14.7 — it is not a pre-ship step and not a gate.)

**Assumption stated explicitly (cross-ref `decisions.md` Q2):** the plan **assumes** the repartition covers every row that needs it, i.e. that a token whose `allowed_models` names a group id is *intended* to reach that group's members (the pre-change behaviour). If the owner intended a target-only grant for a specific token, that token's post-migration reach would be wider than intended — flagged here as the one interpretive assumption of the repartition; G0 query 1 names exactly which tokens are affected (`smoke-primary-agentic`, `smoke-primary`, on both hosts) so the assumption is auditable.

---

## 16. Out of scope (flagged only)

| # | Item | Why |
|---|---|---|
| O1 | FE/BE `prefix` drift (`types.ts:244` declares `prefix`; the BE never returns it) | pre-existing, harmless |
| O2 | MCP server-level authorization | different axis (§1.3); follow-up named |
| O3 | Stale `pkg/store/database/sqlc/schema.sql:48-55` | out of the build; do **not** regenerate |
| O4 | Periodic-reconcile write-mutex stall (`pkg/store/database/credential_group.go:637-638` etc.) | separate performance item; this plan only guarantees **no new DB read** (`I14`) |
| O5 | Decision lane writes no per-attempt ordering to stdout | observability gap, not permission |
| O6 | FE vocabulary collision with **Credential Groups** | resolved as a label decision (§12.2: "Load-balance groups") |
| O7 | 403 envelope lacks a machine-readable `code` | deferred (`decisions.md` Q8 (b)); re-openable |
| O8 | Stale in-code comments (`pkg/imggen/handler.go:263` cites `pkg/auth/token.go:129-144`; `:711` cites `handler.go:346-356`) | fix **in passing** in the implementing commits (§13 P-G22's guard is the durable fix) |
| O9 | `pkg/proxy/handler.go:857` fail-closed arm is **unreachable** (the unknown-model 404 fires earlier at `:589-606`); the anthropic equivalent is documented unreachable at `handler_anthropic.go:511-513` | **doc/comment matter only** — the plan designs fail-closed behaviour independently of `:857` (§10) and rewrites the comments |
| O10 | Stale main checkout line numbers (`/home/nea/Code/opensource-projects/llm-supervisor-proxy` is a stale tree) | all citations here are worktree-relative at `a3f0335` |

---

## 17. Open items delegated to `decisions.md` (listed, not duplicated)

`decisions.md` **is the register**; the plan adopts its defaults except where §17.2 states otherwise.

### 17.1 Adopted as recommended (no divergence)

Q1 JSON TEXT column + `034` + registry append + additive rollback · Q2 clean break **+ pre-ship data repartition in `034`** · Q3 `allowed_groups` = group ids only · Q4 validation (groups strict existence+kind / models existence-only / inbound only / 400) · Q5 extend the two methods with ONE write payload `auth.OptionalTokenPermission`/`OptionalAllowlist`, 11 implementers atomic, decorator forward + decorator-through test · Q6 `allowed_groups` on POST/PATCH/responses (the POST 64 KB cap clause was REJECTED fix-pass 2) · Q7 per-axis tri-state + **conjunctive** wildcard + flag on the struct · Q8 403, envelope byte-identical, `model` = client-requested id, excluded members to log+events, check before any attempt · Q9 MCP non-goal + follow-up (the startup-log clause REJECTED fix-pass 2) · Q10 request-time expansion, never cached · Q11 TWO per-axis pickers (groups excluded from the models picker — LANDED; supersedes one-picker) · Q13 explicit-empty end-to-end (store `nil`→NULL, `[]`→`'[]'`).

### 17.2 Where this plan states its own recommendation (differs from / refines the register)

| Item | `decisions.md` | This plan | Why |
|---|---|---|---|
| **Q12 enforcement seam** | "filter the **chain** in the request layer at all four points" (rejects the store-side filter) | **(A) manager-level predicate for the member PICK *plus* (B) request-layer list filter for the CHAINS** | (B) alone cannot make the pick scope-aware ⇒ an out-of-scope pick would be dropped post-hoc and a group with an in-scope sibling would spuriously 403 (`D6/L2(a)`). (A) alone cannot cover the entry model's `FallbackChain` (those entries are list entries, never group members). The two are complements at different layers; §8.1 has the full argument. |
| **Q8 envelope — `model` field on a *scope-empty group*** | client-requested id | same, **plus** the explicit rule that excluded **member** ids never enter the body (log+events only) | leak-avoidance (`A1`); stated so the implementer does not "helpfully" add them |
| **Q12's line anchors** | chat loop-top `:1247`; OpenAI re-entry `:1348-1353` | **`:991`/`:996` and `:1231`/`:1248-1249`** | verified drift (§8.3) |
| **Re-entry all-excluded semantics** (§9.4 — SINGLE SOURCE) | not addressed | **attempt-count-keyed: 0 upstream attempts + all-scope-excluded ⇒ 403 (any pass #n); ≥1 attempt ⇒ unchanged 502** | a 403 after a real upstream attempt is a false permission signal |
| **`isServableGroupMemberBase` vs a sibling predicate** | "widen the single candidate gate" | **do NOT widen it** — add `isReachableMember(id, reach)` beside it and call both | keeps the shape predicate pure (health/shape) and the authorization axis separable/reviewable (`P6`); also keeps the three existing call sites' semantics literally unchanged |
| **Scope-carrying seam** | "`models.GroupRewriter` untouched / filter in the request layer" | `GroupRewriterScoped` optional interface **+ compile-time assertion** in the decorator | the decorator-forward trap (`handler_helpers.go:918+` warns: a missing forward is a SILENT no-op in production while raw-manager tests stay green) — the assertion converts it to a build break |

### 17.3 OI items this plan pins (the register asked for these)

| OI | Pin |
|---|---|
| **OI-1** (SQLite JSON1) | permanent test `TestMigration034_SQLiteJSON1Available` + §5.5 fallback (Go-side idempotent backfill), decided by the probe |
| **OI-2** (models-cache TTL on the permission path) | §11: read through the **in-memory models snapshot the cache layer maintains (`GetModels()`/`currentModelRows()`), invalidated on group writes** (Ruling 5/I14 — not `GetModelNative`); reach staleness == existing model-row staleness; **no new knob, no strict read** |
| **OI-3** (helper signature + call sites) | §4.3(d) (`applyReachFilter`) + §8.3 (C1–C4) |
| **OI-4** (`pkg/grouplb` / `pkg/credentiallb`) | §4.3(e): **no cooperation needed**; engine API unchanged; credential layer not a scope surface |
| **OI-5** (decorator obligations) | §4.3(c) + P-G20 + P-G25 |
| **OI-6** (`AuthToken` shape + scanner signature) | §4.1 (two slices + two `Invalid` flags on the struct; `ScanGrant(raw) (ids, present, invalid)`) |
| **OI-7** (FE copy/labels) | §12.2 ("Load-balance groups"; badge "All models (wildcard)" only when both axes absent) |

### 17.3a Leader rulings closing previously-open owner decisions (fix-pass A — RULED, not open)

| Formerly-open decision | **RULING (fix-pass A, 2026-11-18)** |
|---|---|
| **PD-9 alias narrowing** (strict row-id narrowing vs alias expansion) | **RULED: STRICT row-id narrowing** — the gate authorizes the **resolved target only** (no union); an alias-only grant does NOT pass; 403 quotes the client-requested claimed string with zero side effects; carried by the **release note** + the **G0-4 shadow/claimed check**. (Consistent with ADR D1 and Ruling 3.) |
| **PD-10 group id inside a `FallbackChain`** | **RULED: NO new rule this commission — CORRECTED to landed reality (gen-10 re-pin):** **entry-chain groups ARE expanded** by the rewrite (`handler_functions.go:200-202` + `store_group_rewrite.go:105-181`) into their in-reach member, so the raw group id never reaches credential resolution and never 502s (pinned: `TestChainGroupEntry_NeverAdmitted_No502`, LANDED `1e65774`). The **narrower** pre-existing wart — a group id inside a group *member's* own chain (appended at rewrite time, never re-scanned) — remains **documented, unpinned, and NOT a scope escape**; reach filtering still applies to every candidate, so no out-of-scope model is served. No write-time validation, no chain re-scan. |

### 17.4 Genuinely still open for the owner (nothing blocks the build)

| # | Question | Recommended default |
|---|---|---|
| RQ-1 | Ratification of the 13 Q rows (§6 of `decisions.md`) | ratify as recommended; §17.2 lists the five places this plan refines them |
| RQ-2 | Whether the 403 body should later gain a `code` | no (deferred; re-openable) |
| RQ-3 | Whether `smoke-15m` is wildcard or narrow | determined by G0 query 3 before the deploy, no code impact |

---

## 18. Architect defect fold-in — verification note

**PD numbering legend (fix-pass 2, 🟡7):** the **architect's** PDs are cited as **A-PD-n** (rendered `A-n` in the "Also closes architect" column); the **commission/leader** PDs (2026-11-18 and later) are cited as **L-PD-n** (rendered `PD-n` in the "PD" column). R-11's title/body numbering is corrected accordingly (it cites the architect's cooldown defect as **A-PD-3** and the leader disposition as **L-PD-6**).

One row per leader PD (PD-1 and PD-6 land in `decisions.md` / the ADR and are owned by Worker B). "Also closes architect" uses the architect's PD numbering (A-n). Target files for content dispatched elsewhere are noted.

| PD | Severity | What changed | Where in plan.md (§) | Also closes architect | Files touched (this corpus) |
|---|---|---|---|---|---|
| PD-1 | RED | Wildcard wording (conjunctive ABSENT-only) — already fixed in ADR | (ADR/decisions.md) | A-1 | `adr-token-permission-model.md`, `decisions.md` — **owned by Worker B** |
| PD-2 | RED | Postgres `034` up migration uses version-agnostic `DO $$ … EXCEPTION WHEN invalid_text_representation` row-by-row guard; G0 retains PG version + malformed-JSON checks | §5.2, §5.4, §14.3 | A-2 | `plan.md` (phase1-plan.md carries matching update — owned by that worker) |
| PD-3 | RED | `PATCH` nil axis skips the column in SQL (no heal of DENY-ALL); column-skip ruled over the architect's 400-rejection variant | §6.1, §6.3 | A-5 | `plan.md` |
| PD-4 | AMBER | SQLite `034` repartition wrapped with `NULLIF(json_group_array(...), '[]')` | §5.2 | A-4 | `plan.md` |
| PD-5 | AMBER | Scope-filtering of `group.Members` before `policy.SelectMembers`; pre/post base-servable count drives ScopeExcluded (403) vs decision_all_members_failed (502) | §8.2 row 2, §9.2a | A-11, A-15 | `plan.md` (phase2-plan.md carries call ordering — owned by that worker) |
| PD-6 | AMBER | **REWRITTEN fix-pass 2 (leader Ruling 1): cooldown-skip MANDATED** — scope-excluded candidates are never attempts and never write cooldowns (no `ExcludeAndReselect` write, no `skipcache.Mark`); the multi-token pin is implemented (P-G32). The prior "accept and document" disposition is **RETRACTED** (unsourced — no ratification record) | §9.2a | A-3 | decisions.md (R-11, corrected), adr-token-permission-model.md (§4.7, corrected); plan.md §9.2a records the mandate + retraction |
| PD-7 | AMBER | `NewCachedTokenStore` invalidates on CreateToken/UpdateToken covering AllowedGroups; clones AllowedGroups + invalid-flag fields on cache read | §11 | A-14 | `plan.md` |
| PD-8 | AMBER | `/fe/api/tokens` POST/PUT/PATCH validation unambiguous (malformed ⇒ 400; allowed_groups ids must exist + IsGroup(); allowed_models keeps Q4 rule); `applyGroupRewrite` signature threads reach + scopeExcluded; D13 entry-gate hoist (chat :839 before :783, anthropic :514 before :472) with zero binding/sticky writes on entry 403 | §4.3, §6.3, §7.1 | A-8 | `plan.md` (phase2-plan.md T2.4 — owned by that worker) |
| PD-9 | AMBER | Shadow/claimed alias: Reach(T) authorizes against resolved group/target; 403 quotes client-requested claimed string; G0 shadow cross-check | §7.1, §14.3 | A-9 | `plan.md` |
| PD-10 | RED | `NewRaceCoordinator` refuses empty models slice (remove `cfg.ModelID` fallback; return error on len==0); callers assert len>0 pre-spawn | §8.6 | A-6 | `plan.md` |
| PD-11 | GREEN | One exact precedence order for decision/handler.go (Auth 401 → Lane gate 400 decision_misroute → Scope vs Disabled) | §7.1 | A-12 | `plan.md` |
| PD-12 | AMBER | `RewriteGroupModel`/`applyGroupRewrite` per-position ScopeExcluded (idx==0 ⇒ 403; idx!=0 ⇒ drop + log + continue; re-entry passes same Scope); phantom pick (pick=nil when GroupID=="") | §8.2a, §4.3 | A-7, A-11 | `plan.md` |
| PD-13 | GREEN | Explicit Phase 2 audit task for residual `ultimatemodel` / `ExcludeFromUltimateSwitching` references (zero dormant un-scoped escalation hook) — owned by the phase worker; wire-substitution non-scope classification (arch A-13) and the idToHash 50k cap (arch A-14) are folded alongside in §8.2a/§10/§11 | §8.2a, §10, §11 | A-13, A-14 | phase2-plan.md (audit task); plan.md §8.2a/§10/§11 |
| PD-14 | GREEN | `handler_anthropic.go:486-488` + `:530` envelope parity (403 access_denied via shared writer) | §8.2a | A-15 | `plan.md` |
| PD-15 | AMBER | Spec hygiene: ScanGrant maps 'null' ⇒ INVALID; §2.8 omitempty wording (null ≡ ABSENT); decision group.go:118 filters a copy; group id in FallbackChain fail-closed | §2.8, §10, §11, §8.2a | A-10, A-15 | `plan.md` |

**Notes:**
- PD-1 and PD-6 are owned by Worker B (decisions.md / ADR); listed here for audit completeness only.
- phase1-plan.md and phase2-plan.md matching updates (PD-2, PD-5, PD-8) are dispatched to their owning workers; this worker owns plan.md only.
- A-3 (cross-tenant cooldown poisoning) IS leader PD-6: **fix-pass 2 MANDATES the cooldown-skip** (the earlier "accept-and-document" disposition is retracted as unsourced — no ratification record); see §9.2a, decisions.md R-11 (corrected) and ADR §4.7 (corrected).
