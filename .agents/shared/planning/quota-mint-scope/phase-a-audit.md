# Phase A — MINT-SCOPE AUDIT (wi-4b8d21e7)

Branch: `feature/quota-mint-scope` · round-1 base `origin/latest` 0c4a7d7 ·
round-2 baseline `530f5fe` (giter's integration merge of
`origin/latest@dd8c585`) · auditor: coder · date: 2026-11-17.

Question the audit answers: **which upstream body mints which layer's
mark, today?** The user's ruling (verbatim):

> basically, a model need try all credential before declare quota limit reach.
> If a request (hash) bound to quota limit cred -> proxy need auto switch cred.

All `file:line` citations below are refreshed against the final HEAD of
this branch (the round-2 merge moved every line number in
`pkg/proxy`).

---

## 1. The three mint layers

| # | Layer | Key | TTL (config knob) | Mint site (file:line) | What the mark does |
|---|-------|-----|-------------------|----------------------|--------------------|
| 1 | **MODEL** | model row ID | 30m (`model_skip_cache_ttl`, clamp [1m,24h]) | `pkg/proxy/race_coordinator.go:2220` (`markModelWallForErrorLocked`, reached from `markUpstreamSkipLocked` :2152); `pkg/proxy/handler_anthropic.go:2904` (`markAnthropicUpstreamSkip`, reached from the walk :632) — both through the scope gate `pkg/modelcache/policy.go:107` (`ApplyScopedWall`) | **suppresses the ATTEMPT** at zero cost (`pkg/proxy/race_executor.go:506`, `pkg/proxy/handler_anthropic.go:548`) |
| 2 | **UPSTREAM** | `(credential, wireModel)` + `(provider, wireModel)` | 60s (`upstream_skip_cache_ttl`) | `pkg/proxy/race_coordinator.go:2170-2171`; `pkg/proxy/handler_anthropic.go:2918-2919` | **DEMOTES** preference (`preferUnmarkedUpstreamCredential`, `pkg/proxy/race_executor.go:303`) — never excludes |
| 3 | **CREDENTIAL** (engine cooldown) | `(modelID, credID)` | 60s rate-limit / **15m quota** (`credential_quota_cooldown`) | `pkg/credentiallb/engine.go:717` (`st.cooldowns[excludedCredID] = now.Add(cooldown)`), reached from `pkg/proxy/race_coordinator.go:1963` (`ExcludeAndReselectForClass`), `pkg/proxy/internal_handler.go:282` and `pkg/proxy/handler_anthropic.go:902` | excludes the credential from the healthy pool for the window; the reselect REBINDS the conversation key to a sibling credential (`pkg/credentiallb/engine.go:781`) |

Model-layer **read** sites: `pkg/proxy/race_executor.go:506` (race lane),
`pkg/proxy/handler_anthropic.go:548` (Anthropic lane).
Model-layer **clear** (win-clear): `pkg/proxy/handler_helpers.go:891`.

The three layers never read each other's state (AST pin:
`pkg/proxy/cache_layer_separation_test.go:752`
`TestCacheLayer_Structural_PackageLevelNoCacheImports`).

---

## 2. Body-class → mark scope (BEFORE the fix)

| Body class | Observed body (evidence) | MODEL mark | CREDENTIAL cooldown | UPSTREAM mark | Verdict |
|---|---|---|---|---|---|
| **Token-Plan class** (account-wide) | `openai: Token Plan usage limit reached: Upgrade your Token Plan or purchase Credits for more usage. (2056)` · `openai: The Token Plan usage limit has been reached. To avoid interruption, please upgrade your plan or buy credits and turn on auto consumption. (2067)` — pinned in `pkg/modelcache/modelcache_test.go` (`TestModelWall_LiveBodies_ClassificationPin`); classified by `pkg/providers/interface.go` (`quotaMessageVocabulary`: "token plan usage limit", "token plan"); HTTP 402 / numeric code 2067 | **YES** (`pkg/modelcache/reset.go` wallMarkers entry "token plan") | YES (15m) | YES (60s) | **WRONG SCOPE** — an ACCOUNT body minted a MODEL mark. **This is the 03:56:55Z incident.** |
| **Account plan-window wall** | `Usage limit reached for 5 hour. Your limit will reset at 2026-11-16 18:13:33` — `pkg/modelcache/reset.go` (the reset-shape doc) and `.agents/shared/planning/cache-layer-separation/design.md` | **YES** ("usage limit", "5 hour") | YES | YES | **WRONG SCOPE** — the subject is the ACCOUNT ("your limit"). Live evidence: every model benched by this body sat on the **same walled credential** (`.agents/shared/planning/sonic-team/model-skipcache-ttl/HANDOFF.md:73` — "every long-benched model sits on the UTC+8 z.ai cred"; :76 — agentic-Main suppressed >4h11m with **zero provider calls**) → the wall followed the ACCOUNT, not the model. |
| **Plan/daily/session/weekly limits** | `Your weekly limit will reset at …` · `daily limit reached` · `session limit reached` · `plan limit reached` | **YES** | YES | YES | **WRONG SCOPE** — all are account/plan windows. |
| **Account quota reset** | `You have exhausted your capacity on this model. Your quota will reset after 4h39m9s.` — the live 9router wire body, `pkg/imggen/ninerouter.go:290` | NO (vetoed) | YES | YES | correct (credential scope) |
| **Bare reset banner** | `Unavailable (reset after 62h 45m 6s)` | NO (no wall phrase) | YES | YES | correct |
| **Per-minute throttle** | `you have reached your usage limit for this minute` | NO (throttle veto) | YES (60s) | YES | correct |
| **Plain 429** (no quota vocabulary) | — | NO | YES (60s) | YES | correct |

### 2.1 Is any observed body MODEL-scoped?

**No — with exactly ONE candidate, deliberately excluded.**

Every limit body in the observed corpus names the ACCOUNT ("your quota",
"your limit", "your capacity", "Token Plan", credits, balance) or names
NO subject at all ("Usage limit reached for 5 hour"). Per the ruling's
own definition — *"MODEL LAYER only for genuinely MODEL-scoped walls (a
wall that would apply no matter which credential serves it)"* — a body
whose subject is the account does not qualify: a sibling credential (a
sibling ACCOUNT) serves the request normally. A body that names **no**
subject is exactly the "cannot distinguish" case the ruling resolves to
the **narrower** scope (*"when unsure, hide less"*).

**THE ONE CANDIDATE — the translator's 529 / `overloaded_error` shape**
(`pkg/proxy/translator/errors.go:15` `ErrOverloaded`, `:74-75` the
`case 529:` mapping) — is DELIBERATELY EXCLUDED:

1. it is the PROXY's own synthesis (what the CLIENT receives), not an
   upstream account wall — it carries no account subject at all;
2. a 529 is TRANSIENT: a model-layer mark benches the model for the
   whole mark window (up to 30m), which is far too long for a
   transient overload — the mark would hide a model that recovers in
   seconds;
3. admitting it would put the broad model mark back on a body the
   corpus does not show to be model-scoped — the exact shape of the
   2026-11-17 03:56:55Z incident.

⇒ the model vocabulary is **EMPTY** (`pkg/modelcache/scope.go:196`),
the model layer mints nothing for the observed corpus, and the layer
stays wired (TTL knob, `[1m,24h]` clamp, never-shorten, win-clear,
both attempt-suppression read sites).

### 2.2 Marker provenance (the scope vocabulary)

`pkg/modelcache/scope.go:97` (`accountScopeMarkers`) — the earlier
claim "every marker below is backed by an OBSERVED body" was **FALSE**
for most of the list. Per-marker provenance:

| marker | provenance |
|---|---|
| "token plan", "purchase credit", "buy credit", "credits for more usage", "auto consumption", "upgrade your", "your plan" | **observed** — the z.ai live bodies (codes 2056 / 2067) |
| "your quota", "your capacity", "exhausted your capacity", "quota will reset", "quota" | **observed** — the 9router live body (`pkg/imggen/ninerouter.go:290`) |
| "your limit" | **observed** — the z.ai 5-hour plan-window body |
| "resource_exhausted" | **observed** — the 9router nested body (the same file) |
| "insufficient credit", "quota exceeded" | **observed** — `pkg/providers/interface_quota_test.go` (message pins) |
| "quota exhausted" | **observed** — `pkg/proxy/race_reason_test.go:44`, `pkg/imggen/ninerouter_test.go:424` |
| "payment required", "billing" | **observed** — `pkg/providers/interface_quota_test.go`, the OpenAI-shaped quota body |
| "insufficient_credit", "insufficient_quota", "payment_required" | **observed** — the same test file (the structured spellings) |
| every other marker ("insufficient credits", "insufficient quota", "insufficient balance", "out of credit", "credit exhausted", "credits exhausted", "exceeded your quota", "quota reset", "your limits", "your usage", "your credit", "your credits", "your balance", "your account", the plan-window family "daily limit" / "session limit" / "plan limit" / "weekly limit" / "week limit", the structured spellings "insufficient_balance" / "credit_exhausted") | **inferred** — the ACCOUNT-SUBJECT reasoning applied to a family the corpus does not pin (each "your <x>" phrasing; the plan-window family) |

The scope direction is the SAFE one: an over-broad scope marker only
narrows the scope (no model mark) — the class direction is not (§3, F-1).

---

## 3. The 03:56:55Z mechanism (base behavior, reproduced)

Model `m1` with credentials `A` (account exhausted) and `B` (healthy):

1. attempt `main:m1` resolves `A` → upstream `402 Token Plan usage limit`.
2. `race_coordinator.go` (Case-1) → `markUpstreamSkipLocked` → the
   UPSTREAM layer is marked (credential `A`, provider, wire model) for
   60s ✔ (correct layer) — and the MODEL layer is marked ✘ (the
   Token-Plan body matched the v197 wall vocabulary).
3. `credFailoverPlanLocked` → `ExcludeAndReselectForClass(quota)` →
   the engine cools `A` for 15m and reselects the healthy sibling `B` ✔
   (the rotation IS planned).
4. the spawned `cred_failover:m1` attempt reaches
   `pkg/proxy/race_executor.go:506` → the model mark is live → the
   attempt is **suppressed at zero cost** ✘ — `B` is never called and the
   walk advances into the chain.

Net effect: one dry account hides the model's other (healthy)
credentials — exactly the user's ruling.

---

## 4. Findings

| id | finding | evidence | disposition |
|---|---|---|---|
| **F-1** | The Token-Plan class is the ACCOUNT class and must never reach the model layer. | `pkg/providers/interface.go` (the class vocabulary) | **fixed** (the scope gate) |
| **F-2** | The plan-window family ("Your limit will reset", daily/session/weekly/plan limit) is ALSO account-scoped — the live wall followed the credential, not the model. | `HANDOFF.md:73,:76` | **fixed** (the same classification) |
| **F-3** | The **Anthropic passthrough** lane seeded the credential cooldown through `ExcludeAndReselect` — the RATE-LIMIT variant — so an account-wide quota body got the **60s** cooldown instead of the **15m** quota cooldown. | `pkg/proxy/handler_anthropic.go:902` (now the class-aware call) | **fixed** |
| **F-4** | The Anthropic lane's quota DETECTION read only the status (402) and the body `type`/`code` fields — never the message — so a `429` carrying a Token-Plan body was classified rate-limit. | `pkg/proxy/handler_anthropic.go:2810` (`isAnthropicQuotaFailure`) | **fixed** (the canonical classifier + the account-subject message vocabulary) |
| **F-5** | The non-race twin (`pkg/proxy/internal_handler.go`) mints **no** model-layer mark at all — it only mints the credential-layer cooldown (`:282`). | no `modelcache` reference in the file | **no change needed** — documented in the code |
| **F-6** | The UPSTREAM layer's provider-scope mark also demotes the *sibling* credentials' preference (never excludes them; the rotation attempt skips the preference reader entirely — `pkg/proxy/race_executor.go` skips it for a `credFailover` row). | `pkg/proxy/race_coordinator.go:2171` | **left as shipped (v194 doctrine)** — it cannot hide a sibling; the credential-layer cooldown is what keeps the walled account out of the first pick. |
| **F-7** | Hash-binding: the conversation key binds **credential**, not member — `pkg/credentiallb/engine.go` binds `(modelID, conversationKey) → credentialID`, and `ExcludeAndReselect` **rebinds** it to the sibling credential of the SAME model (`pkg/credentiallb/engine.go:781`). The group member binding (`conversationKey → memberID`, `pkg/grouplb/engine.go:394`) is a separate layer and is untouched by a credential failover. **CORRECTION (round 2):** the earlier version of this row said "the binding pins the CREDENTIAL, not the member" — true of the binding's VALUE, but misleading: the binding is SCOPED by the member (the model id), and the reselect REBINDS it in place, so the member affinity holds. The earlier row also implied a fix was needed in the binding layer; there is none. | the two binding pins (`TestQuotaMintScope_HashBoundMemberAutoSwitch`, `TestAnthropicQuotaClass_HashBoundMemberAutoSwitch`) | **verified + pinned** — member affinity with credential mobility already holds; nothing to change. The observation is READ-ONLY (`credentiallb.Engine.BoundCredentialForTest`, `pkg/credentiallb/testhooks.go:119`) — `GetOrSelect` is get-or-SELECT and would WRITE the state it then asserts. |
| **F-8** | The credential-layer cooldown is written by the ROTATION path, and that path is budget-gated (`pkg/proxy/race_coordinator.go`: ≤ len(credentials)−1 rotations per model per request). So the LAST failing credential of an exhausted pool gets NO credential-layer cooldown row — only the upstream-layer demotion (60s, written unconditionally). | the all-walled acceptance test (the assertion that exposed it) | **recorded, NOT changed** — a pre-existing CREDENTIAL-layer coverage gap, not a model-vs-credential scope defect; and the ruled all-exhausted degradation is the UPSTREAM layer's (prefer clean sibling → soonest-recovering → one honest attempt). Fixing it needs an engine "exclude-without-reselect" API whose rebind semantics would have to be re-specified — a separate commission. |
| **F-9** | **F-8's formula** (what a body marks, layer by layer): `model mark = wall-shape ∧ scope == model` → **never** for an observed body (the model vocabulary is empty); `credential cooldown = rotation happened ∧ the class's policy` → the quota class seeds `max(quota_cooldown, Retry-After)` (15m default), the rate-limit class `Retry-After` else 60s — and it is written ONLY when the rotation budget allowed a rotation (F-8); `upstream demotion = limit-class failure` → 60s, always written (never budget-gated). | the three mint sites | **the operator-facing [MODEL-SKIP] line now states exactly this** (`pkg/proxy/model_scope_log.go`), including the F-8 case where no credential-layer cooldown exists. |
| **F-10** | The Anthropic lane's `model_credential_failover` event hard-coded `reason="rate_limit"` and `cooldown_ms=60000` while the engine benched the account for 15m — the FE EventLog renders those fields, so the operator saw a policy that never ran. | `pkg/proxy/handler_anthropic.go:2545` | **fixed** (the real class + the effective cooldown) |

---

## 5. STOP-GATE check (between Phase A and Phase B)

The STOP condition fires only if the audit finds an observed body or a
load-bearing behavior that **contradicts the ruling's assumptions**.

* "account-wide quota bodies … must mint marks at the UPSTREAM/CREDENTIAL
  layer … NEVER the model layer" — **consistent**: every observed quota
  body is account-scoped (F-1, F-2).
* "MODEL LAYER only for genuinely MODEL-scoped walls" — **consistent**:
  no observed body is model-scoped; the ONE candidate (the translator's
  529 / `overloaded_error`) is a proxy-side synthesis of a TRANSIENT
  condition and is deliberately excluded (§2.1).
* "A model is declared quota-limited ONLY when EVERY credential is
  exhausted; then existing upstream-layer degradation applies (prefer
  clean sibling → soonest-recovering, one honest attempt — doctrine
  intact)" — **consistent**: the all-exhausted degradation is the shipped
  upstream-layer behavior; no model-layer suppression is introduced, so
  the "one honest attempt" is preserved.
* No contradiction found → **Phase B proceeded** (no user round-trip).

### Consequence to be explicit about

With no observed model-scoped body, the model layer's *mint* path is
inert after the fix. This is deliberate and it is the SAFE state: the
model mark is the broadest mark in the system (it hides a model across
ALL of its credentials), and the ruling says the broad mark must be
earned by positive model-scope evidence. The layer is **not** removed —
`model_skip_cache_ttl` (default 30m), the clamp, the never-shorten rule,
the win-clear seam and both suppression read sites stay exactly as
shipped.

---

## 6. The AFTER mapping table (body class → scope → layers marked)

| body class | scope (`pkg/modelcache/scope.go`) | MODEL layer | UPSTREAM layer | CREDENTIAL layer |
|---|---|---|---|---|
| Token-Plan class (2056 / 2067) | **account** | no mark | demote (credential + provider, 60s) | cooldown 15m — only when the rotation happened |
| the 5-hour plan window ("your limit will reset at …") | **account** | no mark | demote (60s) | cooldown 15m — only when the rotation happened |
| the 9router account quota ("your quota will reset after …" / "exhausted your capacity") | **account** | no mark | demote (60s) | cooldown 15m — only when the rotation happened |
| the plan-window family (daily / session / plan / weekly limit) | **account** | no mark | demote (60s) | cooldown 15m — only when the rotation happened |
| the per-minute throttle ("your usage limit for this minute") | **account** | no mark | demote (60s) | cooldown 60s (rate-limit class) |
| a body with NO account subject and NO model subject ("Unavailable (reset after 62h 45m 6s)", a 5xx, an empty body) | **unknown** | no mark | demote (60s) when limit-class | cooldown per class — only when the rotation happened |
| a body whose TYPE/CODE says quota ("insufficient_quota", "quota_exceeded", "payment_required", "billing", "resource_exhausted") | **account** (the type/code token is in the class vocabulary ⇒ in the scope vocabulary) | no mark | demote (60s) | cooldown 15m |
| a genuinely model-scoped body | **model** | **MARK** (30m cap) | — | — |
| *(no observed body in this class — the vocabulary is empty)* | | | | |

---

## 7. Phase B — the change table (final citations)

| site | file:line | change |
|---|---|---|
| the scope decision | `pkg/modelcache/scope.go:210` (`classifyScope`), `:97` (the account vocabulary), `:196` (the model vocabulary — empty) | three scopes: account / unknown / model; the unknown scope NEVER marks |
| the mint policy | `pkg/modelcache/policy.go:107` (`ApplyScopedWall`), `:42` (`applyWall` — now UNEXPORTED) | the scope gate in front of the unchanged wall-shape policy; the compiler now enforces the gate |
| the class vocabulary | `pkg/providers/interface.go:321` (structured), `:330` (message), `:397` (`ClassifyQuotaBody`), `:414` (`IsQuotaBody`), `:429` (`QuotaVocabularyTokens`), `:457` (`IsQuotaMessage`), `:501` (`IsQuotaError`) | ONE canonical vocabulary, shared by both lanes; per-token provenance in the file header |
| the race lane's mint site | `pkg/proxy/race_coordinator.go:2152` (`markUpstreamSkipLocked`), `:2220` (`markModelWallForErrorLocked`), the Case-1 site `:768` + `:796` | the scope gate + the `[MODEL-SKIP]` line AFTER the rotation decision |
| the Anthropic lane's mint site | `pkg/proxy/handler_anthropic.go:2904` (`markAnthropicUpstreamSkip`) | the same gate + the same line |
| the Anthropic lane's classifier | `pkg/proxy/handler_anthropic.go:2810` (`isAnthropicQuotaFailure`), `:2746` (`anthropicQuotaFields` — decode-first), `:2779` (`anthropicCredentialAttributableStatus` — the S-3 gate) | decode-first + the canonical classifier + the status gate on the message path |
| the Anthropic lane's credential cooldown (F-3) | `pkg/proxy/handler_anthropic.go:902` | the class-aware `ExcludeAndReselectForClass` |
| the Anthropic event (F-10) | `pkg/proxy/handler_anthropic.go:2545` | the real class + the effective cooldown |
| the operator-facing line | `pkg/proxy/model_scope_log.go:60` (`logModelScopeSkip`) | one wording for both lanes; names only the layers actually marked |
| the non-race twin | `pkg/proxy/internal_handler.go:282` | no model mark site (F-5) — the credential layer alone |
| the read-only binding observation | `pkg/credentiallb/testhooks.go:119` (`BoundCredentialForTest`) | the observation seam for the two hash-binding pins |

**The v197 pins still pass VERBATIM** — `ModelWall` (the wall-shape
classifier) and the clamp/knob/never-shorten policy are untouched; the
scope gate is a NEW function in front of them.

## 8. Red-first evidence

See the coder report (the round-2 re-proof) and the commit history: the
acceptance tests are committed BEFORE the fix (`76ff867`), so the
branch's own history carries the red→green transition.
