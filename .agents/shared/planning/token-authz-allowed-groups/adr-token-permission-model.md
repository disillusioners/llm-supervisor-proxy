# ADR — Token permission model: reach-based scope enforcement (AUTH COMMISSION)

**Status:** ENRICHED (Architect) — Leader Rulings Q2(b) & Q13(a) folded
**Date:** 2026-11-18
**Commission:** AUTH COMMISSION — decision-group `allowed_models` bypass fix + `allowed_groups` token field + per-layer permission enforcement
**Base commit:** `a3f0335` (branch `feature/token-authz-allowed-groups`, worktree `llm-supervisor-proxy-wt-token-authz`)
**Authority:** this ADR is the in-repo statement of the token-scope CLASS. It is normative for the commission and binding on every later change that touches candidate construction, group membership, or token grants.
**Scope of this document.** This ADR rules **semantics only** — D1–D16, the class statement (§3), the invariants (§6), the New Lane Contract (§6.1), the Escalation-Mirror & Wildcard rules (§7), the Rollback & Delivery Coupling invariants (§7.1–§7.2), and the ruled-out alternatives (§8) are normative and binding. It does **not** own mechanism: the migration SQL bodies, the filter's exact Go signatures and placement, the API DTOs, the phase plan, and the ship runbook are owned by `plan.md` (+ `phase1/2/3-plan.md`), and the remaining register items are tracked in `decisions.md`.
**Citation convention:** every `file:line` is read INSIDE the worktree at `a3f0335`. The main checkout `/home/nea/Code/opensource-projects/llm-supervisor-proxy` is a stale tree — line numbers taken from it are invalid for this ADR (§11).

---

## 1. Context

### 1.1 The commission (the Emperor's ruling, verbatim)

> "we not only need to fix this allowed_models (update) but also the add feature allowed_groups (allow reaching the group and all models in that group). On each load balance, fallback layer, we need permission check for token and exclude the model from the list."

Three obligations are extracted from that ruling and are NOT re-opened by this ADR:

- **O-a** the `allowed_models` defect is fixed;
- **O-b** `allowed_groups` is added, granting a group **and all models in that group**;
- **O-c** the permission check happens **on each load-balance and fallback layer**, and the offending model is **excluded from the list** (not skipped at attempt time).

### 1.2 The class — three generations of ONE defect

| Gen | Date | Surface | Site | What happened |
|---|---|---|---|---|
| 1 | 2026-05-07 | ultimate-model bypass | `pkg/proxy/handler.go` (surface retired from the code since; only comments remain — `pkg/ui/server.go:1079`, `pkg/providers/openai.go:984`) | a model **other than the authorized one** was served (`allowed_models=["id-1"]` → `ultimate_model="id-2"` served). Recorded in `.agents/tester/LESSONS/model-id-access-control-bugs.md:25` (heading) / `:28` (the issue statement). |
| 2 | 2026-11-14 | text-lane chain / group-member escape | `pkg/proxy/handler_functions.go:201-202`, `pkg/proxy/handler_anthropic.go:331-332`, `pkg/store/database/store_group_rewrite.go:101-102` | the ENTRY string was authorized; the chain target or group member actually served was **never checked**. Captured as POK fact `allowlist-entry-model-only-chain-escape`; consciously DEFERRED in the v192 ship note (the deferral phrase appears in NO commit — a decision that was never written down is a decision that was never made). |
| 3 | 2026-11-17 | decision-group entry bypass | `pkg/decision/handler.go:317-326` (group arm) vs `:340-354` (Step-7 allow-list) | the group arm **returns at `:325` before the allow-list at `:342`** — a decision-GROUP request performs **no token-scope evaluation whatsoever**. Read-only report `/tmp/2026-11-17-decision-group-allowlist-bypass-read-only-report.md` (89 lines, sha256 prefix `712f891b`), anomaly `c630644d`. |

**The class, stated once:** *an authorization decision is taken on a narrower identifier than the one that is actually used.* Gen 1 and gen 2 serve an unauthorized **model**; gen 3 serves with **no** authorization decision at all. Both are the same class: the unit of authorization (a string, checked at a site) is narrower than the unit of execution (the candidate set, chosen elsewhere).

### 1.3 The scope-check sites — exactly FOUR, all on the client-requested string

Verified by exhaustive grep at `a3f0335` (every non-test `IsModelAllowed` call site; there are no others):

| # | Site | Guard | String checked | 403 at |
|---|---|---|---|---|
| S1 | `pkg/decision/handler.go:342-343` (MODEL arm, Step 7) | `authToken != nil && len(authToken.AllowedModels) > 0` | `modelID` (requested) | `:351` |
| S2 | `pkg/imggen/handler.go:267-268` | `authToken != nil && len(authToken.AllowedModels) > 0` | `modelID` | `sendModelNotAllowedError` (`:713-723`) |
| S3 | `pkg/proxy/handler.go:839` (chat) | `authToken != nil` + `modelID != ""` (`:832-837`) | `rc.reqLog.Model` (client-requested string, `:835`) | `:854` |
| S4 | `pkg/proxy/handler_anthropic.go:514` | `authToken != nil` (NO length pre-guard) | `reqLog.Model` (client-requested string) | `sendModelNotAllowedError` |

Supporting facts:

- `pkg/decision/group.go` (723 lines) contains **ZERO** allow-list/scope checks — `authToken` is a parameter at `:96` and `:312` and its only consumer is metering at `:341` (`h.meterOnSuccess(authToken, winner.memberID, ...)`). The token is already in hand at the construction point → **no plumbing change is needed for the decision lane.**
- `pkg/mcp/auth.go:48-57` validates the bearer and injects it into the request context; **nothing downstream reads `AllowedModels`** — the MCP lane has no scope check at all. `pkg/mcp` contains zero model-id references outside tests (verified: `grep -rn "modelID\|ModelID" pkg/mcp/ --include=*.go` → no non-test hits).
- `pkg/ui` never evaluates scope — it only EDITS it (`pkg/ui/server.go:2497`, `:2504`, `:2564`, `:2593-2596`, `:2682-2683`).
- The four 403 writers are byte-identical: `pkg/decision/handler.go:784-794`, `pkg/proxy/handler.go:525-535`, `pkg/imggen/handler.go:713-723` →
  `{"error":{"type":"access_denied","message":"model not allowed for this token","model":"<id>"}}` — hand-rolled `map[string]string`, **no `code` field**. The 401 envelope is `pkg/decision/handler.go:780-782` → `{"error":{"type":"authentication_error","message":"Invalid or expired API key"}}`.

### 1.4 The escape surfaces (the commission's per-layer exclusion targets)

| Surface | Site | Note |
|---|---|---|
| Entry + chain list construction (chat) | `pkg/proxy/handler_functions.go:196-206` (`:201-202`) | runs **before** authentication (`:705-723`) → cannot host a token filter as-is |
| Entry + chain list construction (anthropic) | `pkg/proxy/handler_anthropic.go:326-336` (`:331-332`) | same |
| Group → member + member's chain | `pkg/store/database/store_group_rewrite.go:55-113` (`:101` member, `:102` member chain) | `models.GroupRewriter` (`pkg/models/group_rewrite.go:82-91`) has **no token parameter**; reached via `applyGroupRewrite` (`pkg/proxy/handler_groups.go:68-94`) called at chat `pkg/proxy/handler.go:783` and anthropic `pkg/proxy/handler_anthropic.go:472` — both **after auth** and **before every `modelList` consumer** |
| Chain advance | `pkg/proxy/race_coordinator.go:1899-1912` (`c.models[i+1]` at `:1906`), called at `:858`, consumed by `spawn(modelTypeFallback, …)` `:436-458` | the coordinator's list is `c.models` |
| Hedge | `pkg/proxy/race_coordinator.go:421-435` (`modelTypeSecond` → `c.models[0]` at `:435`) | the code's own comment at `:426-428`: the hedge re-drives `models[0]` on the model's **secondary credential** — "it does not widen model coverage" → **NOT a model-scope surface** (ruled explicitly in D6/L5) |
| Re-entry | `pkg/proxy/handler_anthropic.go:754` (`tryGroupReentryAnthropic(arc)`), chat mirror `pkg/proxy/handler.go:1196-1287` | the re-offered member comes from the group engine → an excluded member can be **re-offered** unless the exclusion covers the offer path |
| Token availability in the coordinator | `pkg/proxy/race_coordinator.go`, `pkg/proxy/race_executor.go` | **ZERO** auth-token references (the only hit is a comment at `race_coordinator.go:2102`); the token is never threaded in; the coordinator is constructed at `pkg/proxy/handler.go:996` with `rc.modelList` |

### 1.5 The architectural consequence (ruled into the ADR)

Because advance, hedge and failover all select from the **same** `c.models` list, excluding unauthorized candidates **at list-construction time** makes every downstream layer inherit the exclusion. Therefore:

- a per-attempt skip is **not** needed (and is forbidden by the commission — D9);
- the token does **not** need to be threaded into the coordinator's inner loops — only into the construction seams;
- any constructor-level empty-list fallback (`race_coordinator.go:338-340` `if len(models)==0 { models = []string{cfg.ModelID} }`) MUST NEVER be reachable after a scope-filtered list becomes empty — empty-by-scope fails closed with 403 before coordinator construction.

### 1.6 The fail-open defect (must close in the same change)

`pkg/auth/token.go:40-68` (`ScanAllowedModels`) returns `[]string{}` on malformed JSON (`:62-66`, logging "SECURITY: malformed allowed_models JSON for token, denying all models" at `:64`) and on an unknown raw type (`:53-54`). But `IsModelAllowed` (`pkg/auth/token.go:114-125`) treats `len(AllowedModels)==0` as **wildcard** (`:116-118`). ⇒ **a corrupt row grants FULL ACCESS.** The log message says "denying all" while the code grants everything.

### 1.7 The decision lane's failure semantics today

`pkg/decision/group.go:96-123`: lane gate at `:101-114` (fail-closed, 400 `decision_misroute`), policy build at `:118` (`buildGroupPolicy`, `pkg/decision/policy.go:58-96`, member append at `:93`), order at `:119`, empty order at `:120-123` → `exhaustDecisionGroup` (`:345-358`) → **502** `decision_all_members_failed`.

**Layer fence (must not be violated):** `pkg/decision/policy.go` imports ONLY `math/rand` + `pkg/models` (`:20-24`) and its header doctrine is verbatim at `:15-18`:
> "The cache is an optimization, not the truth: it may reorder candidates, never exclude them — and it may suppress an ATTEMPT, never a PICK or a CHAIN."

⇒ a scope filter **does not belong inside `policy.go`**; it belongs in `pkg/decision/group.go` **at or before `:118`** — the candidate set is fixed by `buildGroupPolicy` at `:118`, so the filter must act on a **copy** of the group row **before** the policy is built. (`:118` and `:119` are adjacent statements — `policy := buildGroupPolicy(...)` / `order := policy.order()` — so there is no seam "between" them.)

**Error precedence on the decision lane (verified order, must be preserved):**
`401` (`:255`) → `404` (`:313`) → `400 decision_misroute` (`:334-336`) → `400 group-lane misroute` (`pkg/decision/group.go:110-112`) → `403` (`:351`, extended to GROUP arm) → `502`/upstream.
The commission requires that a **chat-kind group + out-of-scope token stays 400 `decision_misroute`, NOT 403** — the misroute gate (`:323`/`:334`) precedes any scope gate.

---

## 2. Decision — the normative rules

Every rule below is **RULE** (binding). Where a rule incorporates a Leader Ruling (Q2, Q13), it is marked explicitly.

### D1 — Scope binds to the SERVED model, never to the requested string
The unit of authorization is the model that will actually be contacted. Every model id that a request can contact must be inside the token's reach set (`Reach(T)`). The client-requested string is only the **entry point** into that decision, never the decision itself. *This is the rule that kills gen 1 and gen 2.*

### D2 — Reach is computed ONCE per request, BEFORE any candidate construction
`Reach(T)` is resolved from the token's grants and the **current** group memberships, and is then reused by every layer of that request:

```
Reach(T) =  { id : id ∈ T.allowed_models }
          ∪ { g  : g  ∈ T.allowed_groups }
          ∪ ⋃_{g ∈ T.allowed_groups} { m : m ∈ Members(g) }
```

No layer may re-resolve membership, and no layer may build a candidate before the reach set exists. *Rationale: D2 is what makes the exclusion structural instead of per-site.*

### D3 — Per-axis tri-state and conjunctive token states (LEADER RULING Q13(a) FOLDED)
Each grant axis (`allowed_models`, `allowed_groups`) is **independently** in one of three states:
- **ABSENT** — no list was declared on this axis (**storage `NULL` ONLY** — Go `nil` slice, `Invalid == false`). Contributes nothing to `Reach(T)`. **CORRECTED (fix pass B, E2 — matches landed `ScanGrant`, `pkg/auth/token.go:50-85`): an empty/whitespace `TEXT` value is NOT ABSENT — it is INVALID (DENY-ALL).** Only SQL `NULL` is ABSENT; pinned by `TestScanGrant_OnlySQLNullIsAbsent`, `TestAuthzResolve_EmptyTextGrantIsDenyAll_NotWildcard`, `TestChat_EmptyTextGrantRow_DenyAll`.
- **PRESENT** — an explicit JSON array was declared, **including an explicit empty array `[]`** (Go non-nil slice, `Invalid == false`). An explicit `[]` grants **nothing** on that axis; it is **NEVER** equivalent to ABSENT.
- **INVALID** — the stored value was non-NULL and could not be parsed as a JSON string array: **empty/whitespace `TEXT`**, malformed JSON, unknown driver raw type, or a non-array JSON literal such as `'null'`, `'123'`, `'"x"'`, or an object `'{...}'` (fix pass B, E2 — empty text is unreadable, never ABSENT).

From the two per-axis states, every token resolves to exactly one of three token-level states:
- **UNRESTRICTED (wildcard)** — **BOTH** `allowed_models` is **ABSENT** **AND** `allowed_groups` is **ABSENT**, **AND** neither axis is **INVALID**. Reach = every model (subject to lane/kind/health gates). This is today's wildcard semantics preserved bit-for-bit (D11).
- **RESTRICTED** — **≥ 1 axis is PRESENT** (even if its array is `[]`) and **neither axis is INVALID**. Reach = `Reach(T)` per D2. If both axes are `[]` (or one is `[]` and the other is ABSENT), `Reach(T) = ∅` (every model request yields 403 `access_denied`).
- **DENY-ALL (internal failure state)** — **either** axis is **INVALID**. `Reach(T) = ∅`. Not operator-selectable; it is the fail-closed landing of D10.

**Escalation-Mirror Guard (normative):** Wildcardness is **conjunctive over ABSENT**, never per-axis and never `[]`. A token with `allowed_models` ABSENT and `allowed_groups` PRESENT (`["coding-group"]` or `[]`) is **RESTRICTED** (its models axis grants nothing). Conversely, a token whose operator clears an axis via `PATCH {"allowed_groups": []}` retains an explicit `[]` (PRESENT-empty) and **NEVER** silently promotes to wildcard (see §7 and §7.2).

### D4 — `allowed_models` carries MODEL ids; `allowed_groups` carries GROUP ids
- A **group id in `allowed_models`** grants the group **entry only** (name-level): the request may *name* that group, but **no member of it may be served** (D5). This is the legacy spelling; it is retained as a legal-but-narrow write-side grant, not as a synonym for `allowed_groups` (see A3).
- A **model id in `allowed_groups`** grants **nothing** — it is not a group, so it contributes no member reach (D5), and is rejected at write time (see §9 O3).

### D5 — No implicit reach anywhere (for RESTRICTED tokens)
- The **chain targets** of a granted model are **not** granted — they must be listed explicitly, or reached through a group grant.
- The **members** of a group named in `allowed_models` are **not** granted.
- The **chain targets of a granted member** are **not** granted (chain rule again).
- **Backup-only members** of a granted group **ARE** members — `BackupOnly` is a routing attribute, not a permission attribute.
- Reach is only ever what the operator said: an id in `allowed_models`, or a group id and current member ids of a group in `allowed_groups`, resolved at request time.
*The wildcard is the single, deliberate exception (D3/D11): "no grants on both axes (both ABSENT)" means "no restrictions".*

### D6 — Exclusion is by REMOVAL from the candidate list, per layer
For every layer that can contribute a candidate to the execution list, a candidate outside `Reach(T)` is **removed from the list at construction/advance-list-build time**. It is never attempted, never attempted-then-skipped, never offered as a hedge, never re-offered on re-entry, never written into a cooldown/ban state, and never counted as an attempt.

| Layer | Seam (verified at `a3f0335`) | What the layer contributes | Scope obligation |
|---|---|---|---|
| **L0 — entry** | chat `pkg/proxy/handler.go:839`; anthropic `pkg/proxy/handler_anthropic.go:514`; decision MODEL arm `pkg/decision/handler.go:342-343`; imggen `pkg/imggen/handler.go:267-268` | the client-requested string | the requested id must be in reach → else 403. **These four sites collapse into ONE shared predicate** (D8). |
| **L1 — entry chain** | `pkg/proxy/handler_functions.go:201-202`; `pkg/proxy/handler_anthropic.go:331-332` | the entry model's `FallbackChain` | chain targets filtered by reach at list construction (D5) |
| **L2 — group member + member chain** | `pkg/store/database/store_group_rewrite.go:89` (pick across all 4 phases `:160-286`, incl. Phase 4 `:270-284`) + `:101-102` (member + member chain) | the picked member and its chain | (a) the **pick must consider only in-reach members**, so an out-of-scope member does not kill a group that has an in-scope sibling, and **must NOT write cooldowns** for scope-excluded members; (b) the member's chain is filtered by reach; (c) fallback-position groups (`idx > 0`) that yield no in-reach member are dropped silently without minting a phantom group pick or failing the request |
| **L3 — decision group** | `pkg/decision/group.go` **at or before `:118`** — a **copy** of the group row handed to `buildGroupPolicy` carries only in-reach members (`policy.go` byte-unchanged) | `policy.order()` — the decision candidate list | members filtered by reach **before the candidate set is fixed**; **NOT inside `policy.go`** (layer fence, §1.7) |
| **L4 — chain advance** | `pkg/proxy/race_coordinator.go:1899-1912` (`:1906`), called `:858`, consumed by `spawn(modelTypeFallback, …)` `:436-458`; anthropic walk `pkg/proxy/handler_anthropic.go:548-767` (`:550`) | the next entry of `c.models` / `arc.modelList` | **inherited** — the coordinator and anthropic loop only ever select from the filtered list. No per-attempt check, no token in the coordinator. |
| **L5 — wire substitutions (hedge, secondary upstream, peak-hour)** | hedge `pkg/proxy/race_coordinator.go:421-435` (`c.models[0]` `:435`) + `SecondaryUpstreamModel` (`race_executor.go:539-563`); `ResolvePeakHourModel` (`race_executor.go:320`, `internal_handler.go:322`, `store.go:2684`) | upstream wire model alias / secondary credential for the **same** already-admitted row | **NOT a model-scope surface** — row identity (`models.id`) is unchanged; hedge comment `:426-428`: "it does not widen model coverage". Ruled explicitly so future reviewers neither skip nor re-open them. |
| **L6 — re-entry** | anthropic `pkg/proxy/handler_anthropic.go:754` (`handler_groups.go:452-542`); chat `pkg/proxy/handler.go:1196-1287` | the re-offered group member & rebuilt list | the re-offer and rebuild go through the **same** scope-aware authority as L2 (with no cooldown write on scope exclusion), and the rebuilt list is reach-filtered **before** sticky re-ordering |

### D7 — One reach resolver, one predicate
The reach computation and the "is this candidate in reach" predicate live in **one** place (`pkg/authz`) and are called by every lane (chat, anthropic, decision, image-gen). A lane may not re-implement membership resolution, wildcard detection, or reach evaluation locally. *This is the anti-regrow mechanism: gen 3 happened because the decision lane had its own group path and nobody put the check there.*

### D8 — The four entry sites become one predicate
S1–S4 are four hand-written variants of the same decision, and they are **not** equivalent today: S1/S2/S3 use the guarded form `len(authToken.AllowedModels) > 0` while **S4 uses the unguarded form** `!authToken.IsModelAllowed(reqLog.Model)` (`pkg/proxy/handler_anthropic.go:514`). They are equivalent only because `IsModelAllowed` treats an empty list as wildcard (`pkg/auth/token.go:116-118`) — i.e. the equivalence **evaporates the moment an empty list becomes a distinct state** (D3/D10). All four (plus the decision GROUP arm) must call the single shared predicate; **zero `len(.*AllowedModels)` authorization checks** may remain outside `pkg/auth`.

### D9 — No attempt-time skip
An out-of-scope model is removed **before** any attempt. Rationale (three reasons, all load-bearing): (i) the Emperor ruled "exclude the model from the list"; (ii) an attempt-time skip leaks the existence of out-of-scope models to the caller (403-vs-404 oracle) and to telemetry; (iii) it would require the token inside the coordinator's inner loops (D2, §1.5) and would re-open the site-check class (A1, A2).

### D10 — Fail closed on an unreadable grant row
The malformed-JSON branch (`pkg/auth/token.go:62-66`), the unknown-raw-type branch (`:53-54`), and any non-array JSON literal (such as `'null'`) must **not** produce a state indistinguishable from ABSENT. The scan layer must return the tri-state (*ABSENT*, *PRESENT*, *INVALID* → **DENY-ALL**), the `INVALID` flags must live **on the cached token struct** (never dropped at the cache boundary or wiped by a `PATCH` keep-existing write-back), and the log line at `:64` ("denying all models") must become **true**.

### D11 — Wildcard tokens keep working, unchanged
A token with **ABSENT `allowed_models` AND ABSENT `allowed_groups`** and cleanly-parsed rows keeps today's semantics: every model is reachable, subject to the lane's own gates. The live wildcard tokens therefore keep working **bit-for-bit** (see §4.1).

### D12 — Group membership is resolved at REQUEST TIME
`Reach(T)` is computed from the **current** membership of every group in `allowed_groups` (the group row's `Members`, read from the **IN-MEMORY models snapshot** the cache layer already maintains — `GetModels()` / `currentModelRows()`; **REWRITTEN per Ruling 5 / I14**: `GetModelNative` is a live SELECT under `m.mu.RLock` (`store.go:1095-1145`) and is FORBIDDEN as a per-request reach source; the snapshot is **invalidated on group writes** via `OnGroupChanged` / the cache drop-on-write). Membership is **never** snapshotted into the token row at save time. A membership change takes effect on the next request without re-minting the token (subject only to the pre-existing bounded model-cache staleness window).

### D13 — The scope decision precedes candidate construction on every lane
On the chat lane the group rewrite (`:783`) currently runs **before** the entry allow-list (`:839`); on the anthropic lane the rewrite (`:472`) runs before the check (`:514`). The reach decision must be taken **first**, so that an out-of-scope request creates no group binding, no member expansion, no sticky pick, no cooldown mutation, and no metering. This is a **sequencing** rule (the status of the entry denial does not change — it is 403 either way).

### D14 — Scope denial is always 403 `access_denied`, and "empty by scope" ≠ "empty by unservability"
- Scope denial at candidate construction → the house 403 envelope (`pkg/decision/handler.go:784-794` / `pkg/proxy/handler.go:525-535` / `pkg/imggen/handler.go:713-723`), with `"model"` carrying the **client-requested string** (never excluded member ids, which go to log and events only).
- Unservable (disabled/vanished member, empty pool) → the **existing** envelopes stay exactly as they are: 502 `group_unservable` on the text lanes, 502 `decision_all_members_failed` (`pkg/decision/group.go:345-358`) on the decision lane.
- The decision lane therefore needs a **new arm between the lane gate (`:101-114`) and the empty-order check (`:120-123`)** that distinguishes "the policy produced no candidate **because scope excluded them**" from "the policy produced no candidate because none is servable".
- *Post-attempt re-entry boundary:* once ≥1 in-reach upstream attempt has occurred, a subsequent re-entry exhaustion keeps the normal availability envelope rather than emitting a post-attempt 403 (preserving the guarantee that 403 fires before any upstream attempt, while still guaranteeing zero out-of-scope attempts).

### D15 — Error precedence is preserved
`401` → `404` → `400 decision_misroute` → `400 group-lane misroute` → `403` → `502`/upstream. A chat-kind group posted to `/v1/decisions` with an out-of-scope token stays **400 `decision_misroute`** (not 403) because the misroute gate (`pkg/decision/handler.go:323`, `:334-336`) precedes the scope gate.

### D16 — Untouched by this ADR (and cross-token binding isolation)
Anonymous stays 401. Lane gates stay fail-closed (`pkg/decision/group.go:101-114`, `pkg/decision/handler.go:847-860`). The v202 quota mint-scope doctrine and the 30-minute **absolute** conversation-hash binding TTLs (`group_binding_ttl` / `model_binding_ttl`) are untouched. The credential layer (`pkg/credentiallb`) is untouched: credential rotation, hedging, and wire-alias substitution (D6/L5) never widen model coverage. Cross-token conversation bindings remain isolated by `tokenID` salting in `conversationKey` (`pkg/credentiallb/key.go:48-51`), so two restricted tokens sharing a conversation never read, share, or corrupt each other's group bindings.

---

## 3. Class semantics

### 3.1 The class statement (the thing that must not regrow)

> **CLASS (token scope escape):** a request authenticated with token `T` contacts a model outside `Reach(T)`, where `Reach(T)` is derived from `T`'s grants and the current group memberships. Every instance of this class — a substituted model, a chain target, a group member, an unauthorized group entry, a re-offered member, or a fail-open scan/write-back escalation — is the same defect regardless of which lane, which layer, or which id was used.

### 3.2 The two structural defects behind all three generations

- **(S1) Unit mismatch.** The authorization unit was *a string*; the execution unit is *a set of candidate models*. Any check expressed over the string is structurally incapable of constraining the set.
- **(S2) Site-check instead of list-property.** The check was a *point check at a site*; the execution list is built at *another* site. A site-check must be re-added at every new site — and the class regrew once per new site class (the chain/member surface, the decision-group surface).

### 3.3 The structural close (what this ADR establishes)

1. **The unit becomes the set.** `Reach(T)` (D1/D2/D3/D5) — a set, computed once per request, with explicit-grant semantics (no inherited reach) and fail-closed tri-state scanning.
2. **The check becomes a property of the candidate list.** Every candidate that enters an execution list must be in `Reach(T)` at that moment (D6). Because advance (L4), wire-substitution/hedge (L5), credential failover (D16), and re-entry (L6) all read from that same filtered list, the property **propagates by construction** — there is no inner loop left where a check can be forgotten.
3. **One resolver, one predicate** (D7/D8) — the decision lane and all future lanes use the single resolver and predicate; no private path can diverge or re-implement a per-axis `len()` guard.
4. **Layer fences stay fences.** `pkg/decision/policy.go` keeps its "reorder, never exclude" doctrine; the decision scope filter lives in `pkg/decision/group.go` **at or before `:118`** (D6/L3, §1.7) — `policy.go` is byte-unchanged.
5. **List-order invariant with sticky promotion.** At every construction/rebuild point, reach filtering precedes sticky re-ordering (`applyStickyOrder`), so sticky promotion can never re-promote or lose priority on an unauthorized target, and sticky promotion never synthesizes new model IDs.

### 3.4 The per-layer exclusion, restated as a single rule

> **The reach invariant (per request, per lane):** the execution list of a request authenticated with `T` is a **subset of** `Reach(T)`; `Reach(T)` is a function of `T`'s cleanly-parsed grants and the group memberships **as of that request**; and `Reach(T)` expands to the universe **iff** all grant axes are **ABSENT** and cleanly parsed.

---

## 4. Consequences

### 4.1 Blast radius on EXISTING tokens — LEADER RULING Q2 = OPTION (b) FOLDED

Live inventory (read-only, 2026-11-17, supplied by the commission):
- **PRIMARY** — 5 tokens: narrow 2 (`smoke-primary-agentic` → `[agentic-group]`, `smoke-primary` → `[coding-group, quick-group]`, operator-owned) + wildcard 3 (`Ha`, `tuyen`, `kha`).
- **DAODUC** — 6 tokens: the same set plus `smoke-15m`.

| Token class | Today | After this ADR | Verdict |
|---|---|---|---|
| **Wildcard** (3 on primary; counterparts on daoduc) | every model reachable | every model reachable (D11) | **UNCHANGED** ✓ |
| **Narrow, group-id spelling** (`allowed_models=["<group-id>"]`) | the group id is authorized; **every member and every member's chain is reached implicitly** — the same escape class as the decision group arm | **member reach is PRESERVED** by migration `034`'s data repartition (the group ids move to `allowed_groups`, `allowed_models` becomes ABSENT ⇒ the group's **current members** stay reachable); **implicit member-CHAIN reach is NARROWED** — chain targets are no longer inherited (D5) | **REACH PRESERVED (repartition) — member-CHAIN reach NARROWED** (the commission's intent, D5) |
| **Narrow, model-id spelling** (`allowed_models=["<model-id>"]`) | the model is authorized; **its whole fallback chain is reached implicitly** (gen 2) | the model is authorized; its chain targets are **not** (D5) → the chain is filtered out of the list (D6/L1) | **BEHAVIOUR CHANGE — the token loses cross-model resilience unless the chain targets are listed explicitly** |

**Settled Ruling — Q2 = Option (b) (Clean break + pre-ship data repartition inside migration `034`):**
The leader has settled Q2 as **Option (b)**. Three binding reasons govern this decision:
1. **Smoke-token continuity for deploy gates:** the affected live narrow tokens are the very smoke tokens (`smoke-primary-agentic`, `smoke-primary`) exercised by this commission's post-deploy probes; a no-migration clean break would break this ship's own deploy gates.
2. **Behaviour preservation:** under the conjunctive ABSENT-only wildcard rule (D3), moving `allowed_models=["coding-group"]` → `allowed_models=ABSENT, allowed_groups=["coding-group"]` yields identical effective member reach (`Reach = {coding-group} ∪ Members(coding-group)`) while keeping the token RESTRICTED (never promoted to wildcard).
3. **Code-shipped migration, not an ad-hoc live-store write:** the repartition ships as an idempotent, in-process migration inside the house migration framework (precedent: `032_internal_only_data`) deployed atomically with the image. The standing grant's ask-back on ad-hoc live-store mutations does not attach; the commission's explicit order covers the ship.

*(Note: for any token whose `allowed_models` currently grants a group's `shadow_model_id` claim alias rather than the group row ID itself, strict row-ID `Reach` resolution narrows that alias unless explicitly addressed in the plan/release notes.)*

### 4.2 The fail-closed flip
- A **corrupt** `allowed_models` or `allowed_groups` row changes from **full access** (today, fail-open, `pkg/auth/token.go:62-66` + `:116-118`) to **DENY-ALL** (D10). Intended; the log line at `:64` becomes true.
- The **unknown-raw-type** branch (`pkg/auth/token.go:53-54`) and non-array JSON literals (`'null'`) land in **DENY-ALL** by the same rule (D10).
- The `INVALID` flag lives on the cached token struct (`*auth.AuthToken`) and MUST NOT be erased by an omitting `PATCH` merge.

### 4.3 Performance / latency
- Reach resolution is **O(grants + Σ members)** per request, computed **once** (D2), and is a **map lookup per candidate** thereafter.
- Membership is read from an **IN-MEMORY snapshot source** — **REWRITTEN (fix-pass 2, leader Ruling 5, approver-iteration-001; A5):** the approver PROVED `GetModelNative` (`pkg/store/database/store.go:1095-1145`, forwarded `pkg/modelscache/models.go:1301-1303`) is a **live SELECT under `m.mu.RLock`**, so reading membership through it per request imports the documented ~90 s reader-stall pattern onto `/v1/*`. **I14 is a HARD REQUIREMENT: zero new per-request DB reads for scope resolution** — reach membership comes from the IN-MEMORY snapshot the cache layer already maintains (the decorator's `GetModels()` row set / `pkg/decision`'s `currentModelRows()`), built once per request and **invalidated on group writes** by the existing cache invalidation path. No strict-read option; no accepted-risk. The seam is pinned in plan.md §4.2/§4.4 and phase2 T2.0. This matters because the periodic reconcile currently holds the store's write mutex across a DB read and stalls the live request path (known project risk).
- The reach set is **not** cached in the token row and **not** cached across requests (D12): membership is dynamic (credential groups v199, decision groups v200).
- The token's own grant lists ride in the already-cached `AuthToken` (`pkg/modelscache/tokens.go:117`, the `CachedTokenStore`), invalidated on `UpdateTokenPermission` (`:297-304`).

### 4.4 The MCP lane
`pkg/mcp` **never selects a model** (zero model-id references outside tests). Its authorization unit is the **MCP server**, not the model. Therefore model scope has nothing to bite on, and the MCP lane's missing scope check is **not** a fourth instance of this class — it is a *different axis* (per-server grants), which is **out of scope** for this commission (§12 O2) and is deferred with a startup warning (§9 O5). The ADR does not claim the MCP lane is authorized.

### 4.5 Telemetry
The scope-denial outcome vocabulary stays `"model_not_allowed"` — already emitted at `pkg/decision/handler.go:345`, `pkg/imggen/handler.go:270`, `pkg/proxy/handler.go:848` — and the decision-lane group path must emit the **same** outcome (with the group id) rather than inventing a new one. A scope denial must be distinguishable in telemetry from an exhaustion (502) — that is precisely the D14 distinction.

### 4.6 What does NOT change
Lane gates, provider protocol, credential rotation, the sticky-ordering engine, the group selection engine's *health* logic, the quota mint-scope doctrine (v202), the 30-minute absolute binding TTLs, the `/v1/*` public topology, and the admin API's role as the **editor** of scope (not its enforcer).

### 4.7 Cross-tenant cooldown benching — **COOLDOWN-SKIP MANDATED** (fix-pass 2, leader Ruling 1; supersedes the earlier "accepted and documented, not fixed" wording)

- **Shared-pool property (background, still true).** The `(group, member)` cooldown/skip-cache pool is **shared across tokens**: the `ExcludeAndReselect` `0 → defaultCooldown` coercion at `pkg/grouplb/engine.go:788-791` (writing `st.cooldowns[id]`) plus the shared `(group, member)` `skipcache.Mark` (`pkg/skipcache/skipcache.go`) would let a scope exclusion bench a member for other tokens via the stale-member arms at `pkg/store/database/store_group_rewrite.go:206`/`:230`.

- **RULED (fix-pass 2): the skip is MANDATORY.** Scope-excluded candidates are removed at construction/advance and are **NEVER attempts** — therefore they can never legitimately write cooldowns (the commission's Requirement-2 principle). Scope rejections in all selection phases skip every cooldown/skip-cache write: no `ExcludeAndReselect` cooldown write for a scope-refused offer, no `skipcache.Mark`, no `st.cooldowns[id]` write; `ExcludeGroupMemberScoped` never writes a cooldown for a scope-refused offer. The architect's multi-token pin is IMPLEMENTED as required pin **P-G32: "Token A's scope exclusion never cools M1 for wildcard Token B."** A permission denial is not a health fact and must not create cross-request state.

- **RETRACTION RECORD:** the prior text here (and in decisions.md R-11, plan.md §9.2a, plan-overview row 20) said the leader ruled to **"accept and document"** instead of fixing. That claim was **UNSOURCED — no ratification record exists** — and is **RETRACTED** by fix-pass 2. With the skip mandated, the architect's §1.4 airtightness condition ("if and only if the catalogued defects are resolved", naming A-PD-3) is **SATISFIED**. The escape-direction classification is unchanged: no out-of-scope model is ever served (reach invariant I1/I3 holds).

---

## 5. Lineage closure — structural proof across all 3 generations

**The pattern that must stop:** point-fixing has failed **twice** to stop the class regrowing, and each failure had the same root flaw: a narrow string check at one site while execution lists were assembled or substituted elsewhere.

| Gen | What escaped | Fix/disposition applied then | Why point-fixing failed | Structural invariants in THIS ADR that close it |
|---|---|---|---|---|
| **Gen 1** (2026-05-07) | `allowed_models=["id-1"]` served `ultimate_model="id-2"` | Added an ultimate-model site check (`ca0d670`); surface later retired | Checked **one substitution site** instead of making every served candidate pass `Reach(T)` | **D1/I1** (scope binds to every *served* model, not entry string) + **D6/I3** (list-construction membership) + **I12** (any new escalation/substitution path inherits the reach obligation) |
| **Gen 2** (2026-11-14) | Entry string authorized, but `FallbackChain` targets and group members (`[member.ID] + member.FallbackChain`) escaped unchecked | Consciously deferred in v192 ship note; never recorded in an ADR or decision log | String check on `reqLog.Model` (**S1** unit mismatch) + rewrite/list build lived downstream of auth (**S2** site-vs-list split) + deferral lived only in an ephemeral ship note | **D2/I2** (`Reach(T)` resolved as a set before construction) + **D5/I5** (no implicit chain/member reach) + **D6/I3/I4** (scoped 4-phase member pick + reach-filtered chains + no attempt-time skip) + **I16** (deferrals must be repo-recorded) |
| **Gen 3** (2026-11-17) | Decision GROUP arm returned at `handler.go:325` before Step-7 allow-list (`:342`) — zero scope check | Read-only report only; surfaced as 🔴 defect | Decision lane grew a **private group path** (`serveDecisionGroup`) that bypassed the model-arm allow-list | **D7/D8** (one resolver `authz.Resolve`, one predicate `Allows`, zero site-local checks) + **D6/L3** (decision group filtered on row copy before `policy.go:118`) + **D14/I9** (scope-empty 403 vs unservable 502) + **I15** (reach-completeness test across every construction seam) |

**Third point-fix avoided:** the decision-group bypass could have been patched by adding `IsModelAllowed` inside `pkg/decision/group.go`'s attempt loop. That would have been a fourth point-fix at a fourth site, leaving the class alive for the next lane or candidate builder. Making `Reach(T)` a property of every constructed candidate list closes the entire class.

---

## 6. Invariants — the reviewer-runnable checklist

A reviewer runs this list against any change that touches candidate construction, group membership, token grants, or a new lane. **Every item must hold.** Each is stated so it can be checked against code, not prose.

| # | Invariant | How a reviewer checks it |
|---|---|---|
| **I1** | No lane ever contacts a model outside `Reach(T)` for a request authenticated with `T`. | Read every candidate-construction site (chat, anthropic, decision, image-gen) and confirm every candidate is reach-filtered. |
| **I2** | `Reach(T)` is resolved **once per request**, before any candidate is constructed, and the same set is used by every layer of that request. | One resolution call site per request path; no second membership resolution. |
| **I3** | Every candidate entering an execution list is a member of `Reach(T)` **at the moment it enters**. | The list handed to the coordinator (`pkg/proxy/handler.go:996`), to the anthropic walk (`pkg/proxy/handler_anthropic.go:550`), and to `buildGroupPolicy` (`pkg/decision/group.go:118`) is reach-filtered; no empty-list constructor fallback (`race_coordinator.go:338`) can re-inject an unfiltered id. |
| **I4** | Exclusion is by **removal from the list**; there is no attempt-time skip, no "try-then-skip", no cooldown write on scope exclusion, and no out-of-scope attempt in any telemetry. | Grep the attempt loop for scope checks — there must be **none**; verify scope-excluded member picks do not call `ExcludeAndReselect` with cooldown writes; grep telemetry for out-of-scope model ids — there must be **none**. |
| **I5** | Chain targets are **not** inherited from their parent entry or parent member. | A token listing only `A` cannot be served `B` where `B ∈ A.FallbackChain`. |
| **I6** | A group grant reaches the group's **current** members, resolved at request time; membership is never snapshotted into the token row. | Changing a group's membership changes reach on the next request, with no token rewrite. |
| **I7** | A group id in `allowed_models` grants the **entry only** — never its members. | A token listing only `<group-id>` cannot be served a member of that group. |
| **I8** | "Both axes **ABSENT** + clean row" ⇒ unrestricted; explicit `[]` ⇒ PRESENT-empty (grants nothing on that axis); "unreadable row" ⇒ **DENY-ALL**. These states are **never** conflated. | A malformed `allowed_models` or `allowed_groups` row yields DENY-ALL (`Reach = ∅`); an explicit `[]` on one axis with ABSENT on the other yields RESTRICTED (`Reach = ∅`), never wildcard. |
| **I9** | A scope denial at construction is always **403 `access_denied`** (house envelope, client-requested id in `"model"`), never a 502 exhaustion envelope; and "empty because scope" remains distinguishable from "empty because unservable". | Decision lane: the new arm sits between `pkg/decision/group.go:101-114` and `:120-123`; text lanes propagate `ScopeExcluded`; 502 remains reachable only for the unservable case. |
| **I10** | Decision-lane error precedence is unchanged: 401 → 404 → 400 `decision_misroute` → 400 group-lane misroute → 403 → 502. | A chat-kind group + out-of-scope token returns **400 `decision_misroute`**, not 403. |
| **I11** | Anonymous ⇒ 401; lane gates stay fail-closed. | `pkg/decision/handler.go:247-257`, `pkg/decision/group.go:101-114` unchanged in behaviour. |
| **I12** | **No new authorization unit appears.** Any lane that can select a model inherits the reach obligation; a new lane is a new reach obligation, not an exemption. | Adding a route/model-kind/hedge/escalation requires the New Lane Contract (§6.1) in the same commit. |
| **I13** | The credential layer and wire-level row aliases are **not** scope surfaces: rotation, credential failover, hedging, secondary-upstream, and peak-hour wire aliases never widen model coverage. | Hedge (`race_coordinator.go:421-435`), `SecondaryUpstreamModel`, `ResolvePeakHourModel` preserve row identity; no credential path introduces a new `models.id`. |
| **I14** | Reach resolution introduces **no new per-request DB read** on the hot path — **HARD REQUIREMENT (fix-pass 2, Ruling 5)**: membership is read from the **in-memory models snapshot** (decorator rows / `currentModelRows()`), invalidated on group writes; `GetModelNative` is a live SELECT under `m.mu.RLock` and is FORBIDDEN as a per-request reach source. | No new `QueryContext`/`SELECT` on the request path for scope resolution; structural guard `TestPackage_ImportGuard_NoScopeInHotLoop` + the §4.3 seam pin. |
| **I15** | Every candidate-construction site is covered by the **reach-completeness test**; a new site without a test fails the gate. | The structural test enumerates the construction sites and asserts every candidate ∈ reach. |
| **I16** | A deferred scope obligation is **written into the repo** (a decision record) — never left in a ship note. | The v192 deferral (gen 2) is the counter-example; future commissions must record any deferral in-repo. |

### 6.1 The New Lane Contract (future-proofing checklist)

Any future feature that adds a **new request lane**, **new model kind**, **new candidate source** (e.g., router/tier/hedge/escalation/cascade), or **new alias/claim mechanism** MUST satisfy all seven obligations in the **same** PR/commit before merge:

1. **Single-resolver entry:** Resolve `scope := authz.Resolve(authToken, membersOf)` **once** post-auth and pre-construction via `pkg/authz` — never compute `len(AllowedModels)`, never inspect raw token slices, and never invent a lane-local permission predicate.
2. **Sequenced before side effects:** Evaluate entry reach **before** creating any group binding, sticky state, cooldown mutation, metering record, or upstream call, while preserving the lane's error precedence (`401 → 404 → 400 misroute → 403 → 502`).
3. **Construction-time exclusion only:** Filter every candidate list, rewrite, re-offer, and rebuild through `ReachFilter` / `applyReachFilter` **before** any ordering step (e.g., `applyStickyOrder`) and **before** any execution loop. Do not add attempt-time skips, do not thread `AuthToken` into execution coordinators, and ensure an empty post-filter list returns `403` rather than falling back to a default/entry-model slice.
4. **Distinguish scope-empty from unservable (403 vs 502) and isolate cooldowns:** Track whether exclusion emptied the candidate set (`ScopeExcluded`) so scope denial returns the house `403 access_denied` envelope (with client-requested id in `"model"` and excluded ids only in logs/events), never `502`. Scope-excluded rejections MUST NOT write cooldowns or health penalties.
5. **Alias / claim / chain closure:** If the new lane introduces model aliases, shadow claims, or multi-hop chains, explicitly add its resolution rule to the semantics matrix and verify whether grants bind to the requested alias, canonical row id, or both — never leave alias behavior undefined.
6. **Wire-substitution vs row-selection boundary:** Classify any new upstream target selection explicitly as either (a) **row selection** (different `models.id` → MUST be in `Reach(T)`) or (b) **same-row wire/credential substitution** (same `models.id` → explicitly ruled non-scope under D16/I13 and documented in the ADR).
7. **Structural gate coverage:** Extend the reach-completeness test suite (`I15`), the zero-`len(.*AllowedModels)` / zero-hot-loop-`authz` grep guards, and — if a grant column or API payload changes — the single-image coupling & rollback ordering checks (§7.1–§7.2).

**ENFORCEMENT ROW (fix-pass A, leader note) — the New Lane Contract is GATED, not advisory:**

| Guard | Assertion | Where |
|---|---|---|
| **Structural gate test (mandatory)** | **zero `len(.*AllowedModels)` outside `pkg/auth`**; **reach only via `authz.Resolve`** (no lane-local predicate, no raw grant-slice inspection) | `TestPackage_NoPerAxisLenGuardOutsideAuth` + `TestPackage_ImportGuard_NoIsModelAllowedInLanes` (phase2 §5.3); runs on every `go test`, with a firing negative control |

A new lane that cannot pass these two greps is **not mergeable** — the Contract's obligations 1 and 7 are thereby mechanically checkable, not prose. (Rollback/delivery coupling invariants live at §7.1–§7.2 of this ADR; this row gates the New Lane Contract at §6.1.)

---

## 7. What "wildcard", "explicit empty", and "deny-all" mean going forward

**Definition (normative — Escalation-Mirror Guard):** a token is **UNRESTRICTED (wildcard)** iff **all** of the following hold:
1. `allowed_models` is **ABSENT** (**storage `NULL` only** — an empty/whitespace `TEXT` is INVALID/DENY-ALL, fix pass B E2; Go `nil` slice) **AND** `allowed_groups` is **ABSENT** (**storage `NULL` only**, same rule, Go `nil` slice);
2. both grant columns **parsed cleanly** (`ModelsInvalid == false && GroupsInvalid == false`).

**Semantics:** every model is reachable — including group members and chain targets — subject to the lane's own gates (kind, lane, enabled, decision misroute). This is exactly today's semantics and it preserves the live wildcard tokens unchanged (D11).

**What wildcard is NOT:**
- **NOT** per-axis. If `allowed_groups` is PRESENT and `allowed_models` is ABSENT (the exact shape produced by migration `034` for narrow group tokens), the token is **RESTRICTED** — `allowed_models` grants nothing on the model axis. Per-axis wildcarding would turn the fix into a privilege escalation.
- **NOT** an explicit empty array `[]`. Under **Leader Ruling Q13 = Option (a)**, `[]` is **PRESENT-and-empty** — it grants **nothing** on that axis. Thus `allowed_models: []` + `allowed_groups: NULL` is **RESTRICTED (`Reach = ∅`)**, never wildcard.
- **NOT** "malformed". Today a malformed row *is* a wildcard (fail-open, `pkg/auth/token.go:62-66` + `:116-118`). After D10, malformed / unknown raw type / non-array JSON literal ⇒ **DENY-ALL (`Reach = ∅`)**.
- **NOT** "unrestricted in every dimension". The lane gates, the decision-misroute gate, the credential layer, the quota policy, the group health policy and the decision-group exhaustion semantics all still apply. Wildcard is about **model reach only**.
- **NOT** a statement about the MCP lane: the MCP lane's unit is the server, not the model (§4.4).
- **NOT** a per-grant wildcard. There is no `"*"` entry, no prefix glob, no regex entry. A grant is an **exact id** (case-sensitive, per `pkg/auth/token.go:113`). *Wildcardness is a state of the token, never a value inside a list.*

**Wire behaviour tightening (Q13(a) — MUST be in release notes):** `PATCH /fe/api/tokens/{id}` with `{"allowed_models": []}` previously collapsed `[]` → `NULL` (wildcard); under explicit-empty encoding it writes `'[]'` (**grants nothing**). `PATCH` `null`/omitted preserves existing grants, **except** that a `keep` (`nil`) on an `INVALID` axis must never silently overwrite the corrupt state with `NULL` (fail-closed preservation).

**Storage requirement (CORRECTED fix pass C, E2):** the storage/scan layer must distinguish **ABSENT** (**SQL `NULL` ONLY** — an empty/whitespace `TEXT` is INVALID, DENY-ALL, since `f9adbce`), **PRESENT** (JSON array incl. `'[]'`), and **INVALID** (empty/whitespace TEXT, parse failure / unknown raw type / non-array JSON literal). The `INVALID` flag lives on the cached `*auth.AuthToken` so it survives cache hits (`pkg/modelscache/tokens.go:117`).

### 7.1 Normative Rollback Invariant (LEADER RULING Q2 = OPTION (b) FOLDED)

Because migration `034` repartitions group IDs out of `allowed_models` into `allowed_groups` (leaving `allowed_models` empty/NULL on those rows), and because post-ship creates/patches can also write `'[]'` (explicit-empty) rows:

> **ROLLBACK ORDERING INVARIANT (blocking runbook rule, never optional):**
> 1. **Tier-1 rollback MUST execute the REVERSE DATA REPARTITION FIRST, and the IMAGE/HELM ROLLBACK SECOND.**
> 2. **IMAGE ROLLBACK FIRST IS STRICTLY FORBIDDEN.** Under the pre-034 binary, `IsModelAllowed` treats any `len(AllowedModels) == 0` row (`NULL` **or** `'[]'`) as an unrestricted **wildcard** (`pkg/auth/token.go:116`). Rolling back the image while migrated narrow rows (`allowed_models = NULL`) or post-ship explicit-empty rows (`allowed_models = '[]'`) exist on disk silently promotes those tokens to **full-access wildcards on public `/v1/*` endpoints** — reopening the exact escalation class this ADR closes.
> 3. **Reverse-first safety:** Executing the reverse repartition while the **new** image is still live keeps reversed rows **RESTRICTED** (`allowed_models` is PRESENT ⇒ target-only under D3/D4, never wildcard), so no privilege is widened during the brief window before the image rolls back. (Post-ship `'[]'` rows intended as zero-reach must likewise be reviewed/reconciled before rolling back to pre-034 binaries.)
> 4. **Down-migration & pre-ship gate obligations:** The deterministic reverse-repartition statement MUST ship in the `034` down artifact (with the `schema_migrations` row-deletion caveat), and the **pre-ship read-only malformed-row inspection** on both live hosts (using a safe validity predicate that does not crash on malformed JSON) remains a **mandatory release-gate step**.

### 7.2 Single-Image Delivery Coupling Invariant (LEADER RULING Q13 = OPTION (a) FOLDED)

> **EMBEDDED FE/BE COUPLING INVARIANT:** The frontend PATCH-builder change (sending `[]` when an operator clears an axis, and omitting/`null` only when keeping an untouched axis) and the backend store explicit-empty serialization (`nil → NULL`, `[] → "[]"`) **MUST ship in the same binary/embedded-SPA image (single atomic deploy)**.
>
> No split-brain window between FE and BE is permitted:
> - Old FE (`[]` collapsed to `null`) against New BE (`null` = keep existing) silently ignores operator attempts to clear a grant axis;
> - New FE (`[]` = clear) against Old BE (`[]` collapsed to `NULL`) silently escalates cleared tokens to **wildcard**.
>
> Any future architecture that decouples frontend and backend deployments re-opens this split-brain privilege-escalation trap and is **FORBIDDEN** without an explicit API versioning/re-ruling ADR.

---

## 8. Ruled-out alternatives (each with the one-line WHY)

| # | Alternative | WHY it is ruled out |
|---|---|---|
| **A1** | Keep the entry-string check and add **per-attempt skips** for out-of-scope candidates. | The Emperor ruled "exclude the model from the list"; a skip leaks out-of-scope model existence (403-vs-404 oracle), pollutes attempt accounting, and is a site-check — the class itself. |
| **A2** | Thread the token into the race coordinator and re-check on every advance. | The coordinator only selects from the candidate list; filtering at construction makes the advance correct **by inheritance** — deep threading adds state to the hottest loop for no semantic gain. |
| **A3** | Treat a group id in `allowed_models` as a full group grant (member reach) — i.e. keep today's behaviour. | It preserves the ambiguous double spelling that let this class regrow three times; `allowed_groups` exists precisely to make member reach **sayable**. |
| **A4** | Put the scope filter inside `pkg/decision/policy.go` (the candidate-list builder). | `policy.go`'s header forbids importing the health/suppression layer and its doctrine says a layer "may reorder candidates, never exclude them" — a scope filter there is a layer-fence violation. |
| **A5** | Enforce scope at the **grouplb engine** level (a predicate parameter on the selector API). | It pushes a per-request authorization predicate into the hot selector and the credential-layer engine; the manager-level pick loop already has the exclude-and-reselect machinery to walk past out-of-scope members (and scope-excluded offers must not write cooldowns). |
| **A6** | Snapshot group membership into the token row at save time. | Membership is dynamic (credential groups v199, decision groups v200); a snapshot would make every membership change require a token rewrite and would go stale. |
| **A7** | Express "deny all models" or "grant nothing" by reusing `NULL` / ABSENT. | ABSENT on both axes **is** the wildcard state — the collapse is the defect (`pkg/auth/token.go:62-66` vs `:116-125` and `store.go` nil/empty collapse); explicit `[]` and `INVALID` must stay distinct from `ABSENT` (Q13(a)). |
| **A8** | Enforce model scope on the MCP lane as part of this commission. | The MCP lane never selects a model (`pkg/mcp` has zero model-id references outside tests); its unit is the server — a different axis, deferred (§9 O5). |
| **A9** | Close the decision-group bypass with a scope check **inside the attempt loop** of `pkg/decision/group.go`. | That is a fourth point-fix at a fourth site — the exact failure mode of gens 1 and 2; the filter belongs at candidate construction (`:118`/`:119`). |
| **A10** | Ship a **compat flag** (default ON) where a group id in `allowed_models` still means a group grant, or skip the `034` data repartition. | **Ruled out by Leader Ruling Q2(b):** keeping a compat flag leaves the class ambiguous; skipping the repartition breaks the live smoke tokens required by the deploy gate; `034`'s repartition preserves behaviour cleanly in-framework. |
| **A11** | Keep the four entry checks as four hand-written site checks. | They are already divergent (S4 has no length pre-guard, `pkg/proxy/handler_anthropic.go:514`), and the divergence becomes a **security** divergence the moment empty stops meaning wildcard (D8). |
| **A12** | Make `allowed_groups` resolve membership at token-save time and store the expansion. | Same as A6: dynamic membership would be frozen at mint time. |
| **A13** | Enforce "cannot clear the last axis" solely as a UI validation rule while keeping backend nil/empty conflation. | **Ruled out by Leader Ruling Q13(a):** policy in the UI is the wrong layer, leaves direct API calls unprotected, and makes explicit "grant nothing on one axis" inexpressible. |

---

## 9. Disposition of Register Items (Q1–Q13 reconciled with Leader Rulings)

| # | Item | Disposition / Normative Position |
|---|---|---|
| **O1** | **DB representation of `allowed_groups`** (Q1) | **SETTLED (`decisions.md` Q1(a) / `plan.md` §5):** TEXT column on `auth_tokens` holding a JSON array of group ids, added by migration `034` appended to `pkg/store/database/migrate.go:82`. Rollback follows §7.1. |
| **O2** | **Wildcard vs explicit-empty vs deny-all** (Q7, **Q13**) | **SETTLED BY LEADER RULING Q13(a) (§2 D3, §7, §7.2):** **Wildcard** = both axes **ABSENT** (**SQL `NULL` only** — empty/whitespace TEXT is INVALID/DENY-ALL, fix pass C E2) AND parsed cleanly. **`[]`** = **PRESENT-and-empty** (grants nothing on that axis). **Deny-all** = **INVALID** on either axis (malformed JSON / unknown raw type / non-array JSON literal). Single-image FE/BE coupling invariant (§7.2) is binding. |
| **O3** | **API validation on write** (Q4) | **SETTLED (`decisions.md` Q4(a) / `plan.md` §6.3):** Reject (400) on write: unknown or non-group id in `allowed_groups`; unknown id in `allowed_models` (group id in `allowed_models` is accepted as target-only grant, D4). Validation runs on inbound set only. Reads stay lenient and `authz.Resolve` is authoritative. |
| **O4** | **Compat mode & pre-ship repartition** (**Q2**) | **SETTLED BY LEADER RULING Q2(b) (§4.1, §7.1):** No compat flag; clean break **plus** idempotent data repartition inside migration `034` (moving group-kind ids from `allowed_models` to `allowed_groups`), preserving live narrow smoke tokens with zero operator write. |
| **O5** | **MCP lane** (Q9) | **SETTLED (`decisions.md` Q9):** Model scope does not apply to MCP; emit a loud startup log line naming the gap. Per-server authorization is a separate commission. **(startup-log clause rejected fix-pass 2 — decisions.md §6.1 Ruling-rejections #4; register entry retained, clause superseded.)** |
| **O6** | **Envelope for a scope-empty group** (Q8) | **SETTLED (`decisions.md` Q8(a) / D14):** **403 + house `access_denied` envelope** (`{"error":{"type":"access_denied","message":"model not allowed for this token","model":"<client-requested id>"}}`), `"model_not_allowed"` telemetry, zero upstream calls — never 502. |
| **O7** | **Where the reach resolver lives** | **SETTLED (`plan.md` §4.1):** New small package `pkg/authz` importing `pkg/auth` only (never `pkg/decision/policy.go` or health/cache layers); shared `models.ReachFilter` interface breaks the `pkg/store/database` import cycle. |
| **O8** | **Telemetry naming** | **SETTLED (D14, §4.5):** Reuse `"model_not_allowed"` across all lanes including decision-group scope denials. |
| **O9** | **Rollback ordering & pre-ship gate** (**Q2**) | **SETTLED BY LEADER RULING Q2(b) (§7.1):** **Reverse repartition FIRST, image/helm rollback SECOND** (image-first strictly forbidden). Pre-ship read-only malformed-row inspection on both hosts is a mandatory release-gate step. |
| **O10** | **Test strategy** | **SETTLED (`plan.md` §13):** Reach-completeness structural test (`I15`), hot-loop zero-authz / zero-`len(AllowedModels)` grep guards, per-lane 403 vs 502 pins, fail-closed `ScanGrant` + `UpdateTokenPermission` cache-drop pins, conjunctive wildcard + `PATCH []` non-escalation pins, and migration `034` idempotency pins. |

---

## 10. Evidence gaps (stated, not invented)

- **G1** — The commission did not supply the **exact membership-resolution API** for the scope-aware group pick. The plan resolves this via `GroupRewriterScoped` + `isReachableMember` beside `isServableGroupMemberBase` (`pkg/store/database/store_group_rewrite.go:348-360`), applied at all 4 phases including Phase 4 (`:270-284`).
- **G2** — Entry check hoisting on chat (`:839` before `:783`) and anthropic (`:514` before `:472`) is required by D13 and stepped in `phase2-plan.md`; the dead unknown-model arm at `pkg/proxy/handler.go:857` (`len(authToken.AllowedModels) > 0`) must be removed/converted in the same commit so no per-axis guard survives.
- **G3** — The class of `smoke-15m` on daoduc (wildcard vs narrow) is determined by the pre-deploy read-only query (`plan.md` §14 Step 0).
- **G4** — No p99 baseline numbers were supplied for the lanes, so §4.3 states a **constraint** (no new DB read; one resolution per request) rather than a numeric budget.
- **G5** — FE changes (`TokenForm.tsx`, `TokenList.tsx`, `useApi.ts`, `SettingsPage.tsx`, `App.tsx`) are owned by `plan.md` §12 / `phase3-plan.md`, subject to the §7.2 single-image coupling invariant.

---

## 11. Drift report — every commission citation re-verified at `a3f0335`

**Verified EXACT (re-checked in the worktree, not in the stale main checkout):**

| Citation | Verdict |
|---|---|
| `pkg/decision/handler.go:317-326` (group arm, return at `:325`) | **EXACT** |
| `pkg/decision/handler.go:340-354` (Step-7 allow-list; check `:343`; 403 at `:351`) | **EXACT** |
| `pkg/imggen/handler.go:267-268` | **EXACT** |
| `pkg/proxy/handler.go:839` (chat) | **EXACT** (`:835` = `rc.reqLog.Model`, the client-requested string) |
| `pkg/proxy/handler_anthropic.go:514` (anthropic) | **EXACT** |
| `pkg/auth/token.go:63-65` (malformed ⇒ `[]`) | **EXACT** (`:62` unmarshal, `:63` comment, `:64` log, `:65` return) |
| `pkg/auth/token.go:114-125` (`IsModelAllowed`) | **EXACT** (wildcard at `:116-118`) |
| `pkg/store/database/store_group_rewrite.go:55-113` (rewrite; `:101` member, `:102` member chain) | **EXACT** |
| `pkg/models/group_rewrite.go:82-91` (`GroupRewriter`, no token parameter) | **EXACT** |
| `pkg/proxy/handler_groups.go:68-94` (`applyGroupRewrite`) | **EXACT** |
| `pkg/proxy/handler.go:783` / `pkg/proxy/handler_anthropic.go:472` (rewrite call sites, both after auth) | **EXACT** (chat auth `:705-723`; anthropic auth `:403-422`) |
| `pkg/proxy/race_coordinator.go:1899-1912` (`fallbackAdvanceTargetLocked`; literal `c.models[i+1]` at `:1906`), called at `:858` | **EXACT** |
| `pkg/proxy/race_coordinator.go:421-435` (`modelTypeSecond` → `c.models[0]` at `:435`) | **EXACT** — including the verbatim comment "it does not widen model coverage" at `:426-428` |
| `pkg/proxy/race_coordinator.go:338-340` (`if len(models) == 0 { models = []string{cfg.ModelID} }`) | **EXACT** (newly verified constructor empty-list fallback) |
| `pkg/proxy/handler_anthropic.go:754` (`tryGroupReentryAnthropic`) | **EXACT** |
| `pkg/proxy/handler.go:996` (coordinator construction with `rc.modelList`) | **EXACT** |
| `pkg/decision/group.go:118-119` (policy → order) and `:120-123` (empty → 502) | **EXACT** |
| `pkg/decision/group.go:345-358` (`exhaustDecisionGroup`, 502 `decision_all_members_failed`) | **EXACT** |
| `pkg/decision/policy.go:20-24` (imports only `math/rand` + `pkg/models`) and `:15-18` (the "reorder, never exclude" doctrine) | **EXACT** |
| `pkg/decision/handler.go:784-794` / `pkg/proxy/handler.go:525-535` / `pkg/imggen/handler.go:713-723` (the three byte-identical 403 writers) | **EXACT** |
| `pkg/mcp/auth.go:48-57` (token validated + injected, scope unread) | **EXACT** |
| `pkg/credentiallb/key.go:48-51` (`tokenID` in `conversationKey` SHA-256) | **EXACT** (isolates cross-token group bindings) |

**Clarifications / corrections:**
1. **`pkg/proxy/handler_anthropic.go:472` is the group-rewrite CALL SITE, not the scope check.** The anthropic scope check is at `:514`.
2. **`pkg/auth/token.go:53-54`** — an **unknown raw type** (neither `string` nor `[]byte`) *also* returns `[]string{}` and therefore also fail-opens. D10 covers both branches plus non-array JSON literals.
3. **`pkg/auth/token.go:57-59`** — **CORRECTED (fix pass B-final / approver iteration-003 I3-1): an empty/whitespace string is INVALID (DENY-ALL) since `f9adbce`** (fail-closed Phase-1.1 — reading `""` as ABSENT made a legacy empty column indistinguishable from NULL, and NULL on BOTH axes is the WILDCARD state); only `raw == nil` (`:41-43`) is ABSENT. Both remain distinguishable from PRESENT-empty (`[]`). Pin: `TestScanGrant_OnlySQLNullIsAbsent` (companion: `TestAuthzResolve_EmptyTextGrantIsDenyAll_NotWildcard`, `TestChat_EmptyTextGrantRow_DenyAll`).
4. **The four entry sites are not equivalent** — S4 (`pkg/proxy/handler_anthropic.go:514`) uses the unguarded form while S1/S2/S3 guard with `len(authToken.AllowedModels) > 0`; all four unify under `scope.Allows(...)` (D8).
5. **`.agents/tester/LESSONS/model-id-access-control-bugs.md:25-29`** — Gen 1 ("Bug 3 — Ultimate Model Not Checked in Access Control") is retired from the codebase (`pkg/ui/server.go:1079`, `pkg/providers/openai.go:995` comments only).
6. **`pkg/proxy/handler.go:857`** — the `else if len(authToken.AllowedModels) > 0` arm is dead/unreachable today (404 fires at `:589-606`), but represents a surviving per-axis wildcard test that must be removed under D8.

---

## 12. Out of scope (flagged only — NOT planned here)

- **O1 — Image-gen has no group or chain surfaces.** `pkg/imggen/handler.go:3` states image-gen models have no `FallbackChain`; the imggen lane therefore needs only the **entry** check (S2), calling the **shared** predicate (D8).
- **O2 — The MCP authorization axis (per-server grants).** The MCP lane's unit is the server, not the model (§4.4). A per-server grant axis is a **separate commission**; deferred with a startup log line (§9 O5 — startup-log clause rejected fix-pass 2, decisions.md §6.1; the deferral itself stands).
- **O3 — `pkg/ui` scope evaluation.** The admin API is the **editor** of scope, never its enforcer.
- **O4 — Stale blueprint reference.** The blueprint "Credential Failure Classification & Cooldown Policy" cites the "four-phase selection sweep" at `pkg/store/database/store_group_rewrite.go:105-117`; the sweep actually lives in `pickServableGroupMember` (`:160-286`), and `isServableGroupMemberBase` is at `:348-360`.
- **O5 — Stale in-code comments.** `pkg/imggen/handler.go:263` cites `pkg/auth/token.go:129-144` for `IsModelAllowed` (actually `:110-125`); `pkg/imggen/handler.go:711` cites `handler.go:346-356` (actually `:525-535`).
- **O6 — The stale main checkout.** `/home/nea/Code/opensource-projects/llm-supervisor-proxy` is stale — **all** citations in this ADR are worktree-relative (`llm-supervisor-proxy-wt-token-authz` @ `a3f0335`).
