# Phase A — MINT-SCOPE AUDIT (wi-4b8d21e7)

Branch: `feature/quota-mint-scope` · base `origin/latest` 0c4a7d7 (the v197
model-skipcache-ttl lane) · auditor: coder · date: 2026-11-17.

Question the audit answers: **which upstream body mints which layer's mark,
today?** The user's ruling (verbatim):

> basically, a model need try all credential before declare quota limit reach.
> If a request (hash) bound to quota limit cred -> proxy need auto switch cred.

---

## 1. The three mint layers (BEFORE the change)

| # | Layer | Key | TTL (config knob) | Mint site (file:line) | What the mark does |
|---|-------|-----|-------------------|----------------------|--------------------|
| 1 | **MODEL** | model row ID | 30m (`model_skip_cache_ttl`, clamp [1m,24h]) | `pkg/proxy/race_coordinator.go:2163` (via `markModelWallForErrorLocked` :2151 ← `markUpstreamSkipLocked` :2143); `pkg/proxy/handler_anthropic.go:2767` (via `markAnthropicUpstreamSkip` :2739 ← call site :632) | **suppresses the ATTEMPT** at zero cost (`pkg/proxy/race_executor.go:454`, `pkg/proxy/handler_anthropic.go:548`) |
| 2 | **UPSTREAM** | `(credential, wireModel)` + `(provider, wireModel)` | 60s (`upstream_skip_cache_ttl`) | `pkg/proxy/race_coordinator.go:2131-2132`; `pkg/proxy/handler_anthropic.go:2752-2753` | **DEMOTES** preference (`preferUnmarkedUpstreamCredential`, `pkg/proxy/race_executor.go:303`) — never excludes |
| 3 | **CREDENTIAL** (engine cooldown) | `(modelID, credID)` | 60s rate-limit / **15m quota** (`credential_quota_cooldown`) | `pkg/credentiallb/engine.go:718` (`st.cooldowns[excludedCredID] = now.Add(cooldown)`), reached from `pkg/proxy/race_coordinator.go:1928` (`ExcludeAndReselectForClass`), `pkg/proxy/internal_handler.go:268` (`ExcludeAndReselectForClass`), `pkg/proxy/handler_anthropic.go:877` (**`ExcludeAndReselect` — the RATE-LIMIT variant**, see finding F-3) | excludes the credential from the healthy pool for the window; the reselect REBINDS the conversation key to a sibling credential |

Model-layer **read** sites: `pkg/proxy/race_executor.go:454` (race lane),
`pkg/proxy/handler_anthropic.go:548` (Anthropic lane).
Model-layer **clear** (win-clear): `pkg/proxy/handler_helpers.go:891`.

The three layers never read each other's state (AST pin:
`pkg/proxy/cache_layer_separation_test.go:752`
`TestCacheLayer_Structural_PackageLevelNoCacheImports`).

---

## 2. Body-class → mark scope (BEFORE the change)

| Body class | Observed body (evidence) | MODEL mark | CREDENTIAL cooldown | UPSTREAM mark | Verdict |
|---|---|---|---|---|---|
| **Token-Plan class** (account-wide) | `openai: Token Plan usage limit reached: Upgrade your Token Plan or purchase Credits for more usage. (2056)` · `openai: The Token Plan usage limit has been reached. To avoid interruption, please upgrade your plan or buy credits and turn on auto consumption. (2067)` — `pkg/modelcache/modelcache_test.go:262-263` (`TestModelWall_LiveBodies_ClassificationPin`); classification by `pkg/providers/interface.go:292-295` (`quotaMessageVocabulary`: "token plan usage limit", "token plan"); HTTP 402 / numeric code 2067 — `pkg/providers/interface.go:257-264` | **YES** — `pkg/modelcache/reset.go:82` (`wallMarkers` entry "token plan") | YES (15m quota) | YES (60s) | **WRONG SCOPE** — an ACCOUNT body mints a MODEL mark. **This is the 03:56:55Z incident.** |
| **Account quota reset** | `You have exhausted your capacity on this model. Your quota will reset after 4h39m9s.` — live 9router wire body, `pkg/imggen/ninerouter.go:290` | NO (vetoed: `pkg/modelcache/reset.go:116` "your quota") | YES | YES | correct (credential scope) |
| **Bare reset banner** | `Unavailable (reset after 62h 45m 6s)` | NO (no wall phrase) | YES | YES | correct |
| **Plan-window wall** | `Usage limit reached for 5 hour. Your limit will reset at 2026-11-16 18:13:33` — `pkg/modelcache/reset.go:14`, `.agents/shared/planning/cache-layer-separation/design.md:161` | **YES** — `pkg/modelcache/reset.go:81` ("usage limit"), :89 ("5 hour") | YES | YES | **WRONG SCOPE** — the subject is the ACCOUNT ("your limit"). Live evidence: every model benched by this body sat on the **same walled credential** (`.agents/shared/planning/sonic-team/model-skipcache-ttl/HANDOFF.md:73` — "every long-benched model sits on the UTC+8 z.ai cred"; :76 — agentic-Main suppressed >4h11m with **zero provider calls**) → the wall followed the ACCOUNT, not the model. |
| **Plan/daily/session/weekly limits** | `Your weekly limit will reset at …` · `daily limit reached` · `session limit reached` · `plan limit reached` | **YES** — `pkg/modelcache/reset.go:83-87` | YES | YES | **WRONG SCOPE** — all are account/plan windows. |
| **Per-minute throttle** | `you have reached your usage limit for this minute` | NO (throttle veto, `pkg/modelcache/reset.go:96-102`) | YES (60s) | YES | correct |
| **Plain 429** (no quota vocabulary) | — | NO | YES (60s) | YES | correct |

### 2.1 Is any observed body MODEL-scoped?

**No.** Every body in the observed corpus names the ACCOUNT
("your quota", "your limit", "your capacity", "Token Plan", credits,
balance) or names NO subject at all ("Usage limit reached for 5 hour",
"Unavailable (reset after 62h 45m 6s)"). The repository contains **zero**
observed bodies of the shape "the MODEL itself is unavailable"
(`grep -rniE "does not exist|no such model|not available|deprecat|overloaded"`
over `pkg/` → only client-side/config errors, no upstream wall body).

Per the ruling's own definition — *"MODEL LAYER only for genuinely
MODEL-scoped walls (a wall that would apply no matter which credential
serves it)"* — a body whose subject is the account does NOT qualify:
another credential of the same model (another account) serves the request
normally. And a body that names **no** subject is exactly the
"cannot distinguish" case the ruling resolves to the **narrower** scope
(*"when unsure, hide less"*).

**Therefore: no observed body is model-scoped, and the model layer must
not mint for any of them.** The v197 lane's contrary claim — *"the
genuine plan wall says 'Your LIMIT will reset', never 'quota'"*
(`pkg/modelcache/reset.go:112`) — is what let the Token-Plan body
through: `pkg/modelcache/reset.go:113-127` (quota veto) covered
"your quota" but **not** "token plan" / "purchase Credits", so the two
layers disagreed about the same body:

* the CREDENTIAL layer classified it as an ACCOUNT quota
  (`pkg/providers/interface.go:348` `IsQuotaError` → "token plan") →
  rotate to the sibling credential;
* the MODEL layer classified it as a MODEL wall
  (`pkg/modelcache/reset.go:137` `ModelWall` → "token plan") → suppress
  that same model's next attempt.

### 2.2 The 03:56:55Z mechanism (base behavior, reproduced)

Model `m1` with credentials `A` (account exhausted) and `B` (healthy):

1. attempt `main:m1` resolves `A` → upstream `402 Token Plan usage limit`.
2. `race_coordinator.go:768` → `markUpstreamSkipLocked` →
   `race_coordinator.go:2131-2132` marks the UPSTREAM layer
   (credential `A`, provider, wire model) for 60s ✔ (correct layer), and
   `race_coordinator.go:2143` → `markModelWallForErrorLocked` →
   `race_coordinator.go:2163` `ApplyWall` → **MODEL mark on `m1`** ✘.
3. `race_coordinator.go:782` → `credFailoverPlanLocked` →
   `race_coordinator.go:1928` `ExcludeAndReselectForClass(quota)` →
   the engine cools `A` for 15m (`pkg/credentiallb/engine.go:718`) and
   reselects the healthy sibling `B` ✔ (the rotation IS planned).
4. the spawned `cred_failover:m1` attempt reaches
   `pkg/proxy/race_executor.go:454` → `cfg.ModelSkipCache.Marked("m1")`
   is **true** → the attempt is **suppressed at zero cost** ✘ — `B` is
   never called, and the walk advances into the chain.

Net effect: one dry account hides the model's other (healthy)
credentials — exactly the user's ruling.

---

## 3. Findings beyond the ruling's literal text

| id | finding | evidence | disposition |
|---|---|---|---|
| **F-1** | The Token-Plan class is the ACCOUNT class and must never reach the model layer. | `pkg/providers/interface.go:292-295`; `pkg/modelcache/reset.go:82` | **fixed in Phase B** (mint-scope classification) |
| **F-2** | The plan-window family ("Your limit will reset", daily/session/weekly/plan limit) is ALSO account-scoped — the live wall followed the credential, not the model. | `.agents/shared/planning/sonic-team/model-skipcache-ttl/HANDOFF.md:73,:76` | **fixed in Phase B** (same classification; the ruling's "try all credentials" invariant applies to every quota body, not only the Token-Plan shape) |
| **F-3** | The **Anthropic passthrough** lane mints the credential cooldown through `ExcludeAndReselect` (`pkg/proxy/handler_anthropic.go:877`) — the RATE-LIMIT variant — so an account-wide quota body gets the **60s rate-limit** cooldown instead of the **15m quota** cooldown. The walled account returns to the pool after 60s (the "cooldown trap" `pkg/credentiallb/failure_class.go:29-35` names). | `pkg/proxy/handler_anthropic.go:877` vs `pkg/proxy/race_coordinator.go:1928` / `pkg/proxy/internal_handler.go:268` | **fixed in Phase B** (class-aware `ExcludeAndReselectForClass`) |
| **F-4** | The Anthropic lane's quota DETECTION reads only the status (402) and the body `type`/`code` fields — never the message — so a `429` carrying a Token-Plan body is classified rate-limit. | `pkg/proxy/handler_anthropic.go:2634-2675` (`isAnthropicQuotaFailure`) vs `pkg/providers/interface.go:348-369` | **fixed in Phase B** (the message vocabulary is reused, not duplicated) |
| **F-5** | The non-race twin (`pkg/proxy/internal_handler.go`) mints **no** model-layer mark at all (zero references to `pkg/modelcache`) — it only mints the credential-layer cooldown (`:268`) and reads the upstream layer. | `grep -c modelcache pkg/proxy/internal_handler.go` → 0 | **no change needed** — documented here so the leader knows the lane was checked, not skipped |
| **F-6** | The UPSTREAM layer's provider-scope mark `(provider, wireModel)` also demotes the *sibling* credentials of the same provider (preference only — never excludes; the rotation attempt skips the preference reader entirely: `pkg/proxy/race_executor.go:528` skips it for a `credFailover` row). | `pkg/proxy/race_coordinator.go:2132`; `pkg/proxy/race_executor.go:526-529` | **left as shipped (v194 doctrine)** — it cannot hide a sibling; the credential-layer cooldown is what keeps the walled account out of the first pick. Recorded, not changed. |
| **F-7** | Hash-binding: the conversation key binds **credential**, not member — `pkg/credentiallb/engine.go` binds `(modelID, conversationKey) → credentialID`, and `ExcludeAndReselect` **rebinds** it to the sibling credential of the SAME model (`pkg/credentiallb/engine.go:720-750`). The group member binding (`conversationKey → memberID`, `pkg/grouplb/engine.go:394`) is a separate layer and is untouched by a credential failover. | see Phase C test `TestQuotaMintScope_HashBoundMemberAutoSwitch` | **verified + pinned in Phase C** — member affinity with credential mobility already holds; nothing to change |
| **F-8** | The credential-layer cooldown is written by the ROTATION path (`credFailoverPlanLocked` → `ExcludeAndReselectForClass`), and that path is budget-gated (`race_coordinator.go:1845`: ≤ len(credentials)−1 rotations per model per request). So the LAST failing credential of an exhausted pool gets NO credential-layer cooldown row — only the upstream-layer demotion (60s, written unconditionally at `race_coordinator.go:2131`). | Phase C test `TestQuotaMintScope_AllCredentialsWalled_NoModelSuppression` (the assertion that exposed it) | **recorded, NOT changed** — a pre-existing CREDENTIAL-layer coverage gap, not a model-vs-credential scope defect; and the ruled all-exhausted degradation is the UPSTREAM layer's (prefer clean sibling → soonest-recovering → one honest attempt). Fixing it needs an engine "exclude-without-reselect" API whose rebind semantics would have to be re-specified — a separate commission, not a mint-scope change. |

---

## 4. STOP-GATE check (between Phase A and Phase B)

The STOP condition fires only if the audit finds an observed body or a
load-bearing behavior that **contradicts the ruling's assumptions**.

* Ruling assumption: "account-wide quota bodies … must mint marks at the
  UPSTREAM/CREDENTIAL layer … NEVER the model layer." — **consistent**:
  every observed quota body is account-scoped (F-1, F-2).
* Ruling assumption: "MODEL LAYER only for genuinely MODEL-scoped walls"
  — **consistent**: no observed body is model-scoped (§2.1). The
  model layer therefore mints **nothing** for the observed corpus; the
  layer's machinery (TTL knob, `[1m,24h]` clamp, never-shorten, win-clear,
  attempt-suppression read sites) stays wired so an observed model-scoped
  body can be added without touching a mint site.
* Ruling assumption: "A model is declared quota-limited ONLY when EVERY
  credential is exhausted; then existing upstream-layer degradation
  applies (prefer clean sibling → soonest-recovering, one honest attempt
  — doctrine intact)." — **consistent**: the all-exhausted degradation is
  the shipped upstream-layer behavior (`preferUnmarkedUpstreamCredential`
  + `credentiallb` `ReselectSoonestExpiry`); no model-layer suppression
  is introduced, so the "one honest attempt" is preserved.
* No contradiction found → **Phase B proceeds** (no user round-trip
  needed).

### Consequence to be explicit about

With no observed model-scoped body, the model layer's *mint* path is
inert after the fix. This is deliberate and it is the SAFE state: the
model mark is the broadest mark in the system (it hides a model across
ALL of its credentials), and the ruling says the broad mark must be
earned by positive model-scope evidence. The layer is **not** removed —
`model_skip_cache_ttl` (default 30m), the clamp, the never-shorten rule,
the win-clear seam and both suppression read sites stay exactly as
shipped (requirement 5).

---

## 5. Phase B — what changed (the mint-scope fix)

| site | file:line | change |
|---|---|---|
| the scope decision (NEW) | `pkg/modelcache/scope.go` | `Scope` (ScopeAccount / ScopeModel) + `ClassifyScope` + the account vocabulary (the observed bodies) + the model vocabulary (EMPTY — no observed body) |
| the mint policy | `pkg/modelcache/policy.go` (`ApplyScopedWall`) | the ONE mint entry point: the scope gate in front of the unchanged wall-shape policy (`ApplyWall`) |
| the race lane's mint site | `pkg/proxy/race_coordinator.go:2151-2177` | `ApplyWall` → `ApplyScopedWall` + the `[MODEL-SKIP]` log line |
| the Anthropic lane's mint site | `pkg/proxy/handler_anthropic.go:2755-2780` | `ApplyWall` → `ApplyScopedWall` + the `[MODEL-SKIP]` log line |
| the non-race twin | `pkg/proxy/internal_handler.go:263-274` | **no model mark site** (F-5) — comment only; the lane mints the credential layer alone |
| the Anthropic passthrough's credential cooldown (F-3) | `pkg/proxy/handler_anthropic.go:877-901` | `ExcludeAndReselect` → `ExcludeAndReselectForClass` with the quota class (15m) for an account-quota body |
| the Anthropic lane's quota DETECTION (F-4) | `pkg/proxy/handler_anthropic.go:2634-2665` + `pkg/providers/interface.go` (`IsQuotaMessage`) | the account-quota MESSAGE vocabulary joins the type/code checks (one vocabulary, no drift) |

**The v197 pins still pass VERBATIM** — `ModelWall` (the wall-shape
classifier) and `ApplyWall` (the clamp/knob/never-shorten policy) are
untouched; the scope gate is a NEW function in front of them.

## 6. Phase C — red-first evidence

RED (base `0c4a7d7`, fresh `git archive` scratch — never `cp -a` the
worktree):

```
=== RUN   TestQuotaMintScope_TokenPlanAccountWallRotatesToHealthyCredential
    quota_mint_scope_test.go:213: winner model = "m2", want m1 — a walled ACCOUNT must not push the request onto the fallback model
--- FAIL: TestQuotaMintScope_TokenPlanAccountWallRotatesToHealthyCredential (0.30s)
=== RUN   TestQuotaMintScope_AllCredentialsWalled_NoModelSuppression
    quota_mint_scope_test.go:277: the model layer marked m1 — an account-scoped body must never mint a model mark (not even when every credential is exhausted)
--- FAIL: TestQuotaMintScope_AllCredentialsWalled_NoModelSuppression (0.30s)
=== RUN   TestQuotaMintScope_AllCooling_OneHonestAttemptSoonestRecovering
    quota_mint_scope_test.go:327: the model layer marked m1 — an account-scoped body must never mint a model mark
--- FAIL: TestQuotaMintScope_AllCooling_OneHonestAttemptSoonestRecovering (0.30s)
=== RUN   TestQuotaMintScope_HashBoundMemberAutoSwitch
    quota_mint_scope_test.go:369: winner model = "m2", want m1 — the member binding must survive the credential switch
--- FAIL: TestQuotaMintScope_HashBoundMemberAutoSwitch (0.30s)
=== RUN   TestQuotaMintScope_PerCredentialQuotaRotationRegression
    quota_mint_scope_test.go:403: winner model = "m2", want m1 (the credential rotation must serve the request)
--- FAIL: TestQuotaMintScope_PerCredentialQuotaRotationRegression (0.30s)
FAIL
```

"winner model = m2" IS the 03:56:55Z shape: the walled account pushed the
request off the model whose healthy sibling credential was never tried.

GREEN (this branch): 5/5 `--- PASS` — see the coder report.
