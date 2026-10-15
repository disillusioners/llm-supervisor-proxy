# Decision Log: `decision` Model Kind

> **Companion plan:** [`plan.md`](./plan.md) — implementation-ready plan with 13 sections + Appendix B (Amendment Log).
> **Purpose of this file:** for each open question in `plan.md` §13 (Q1–Q18), the plan's recommended default + what the user can approve or override + consequence of each choice. Read this file once and reply with item-by-item approvals (or overrides) before implementation begins.
>
> **Amendment history** (per `plan.md` Appendix B):
> - **Architect Round 1 (amendments 1-9)**: applied — Q5 (success-only metering), Q9 (timeout 30s/[10s,60s]), Q11 (provider identity — recommended default flipped to `openai` reused + `typesafe`), and a new Q13 (FA5 depth: parity test vs full extraction).
> - **Review Round 1 (findings 1-33)**: applied — Q14 (timeout Option A committed), Q15 (BuildUpstream error mapping), Q16 (global-ultimate kind-gate — scope-defer), Q17 (credential BaseURL steering), Q18 (openai-path byte-faithful pass-through). Q1-Q13 unchanged (no renumbering).
> - **Approver Polish Round 3 (items 1-7)**: applied — Q9 marked **superseded-by-Q14** (kept for stable numbering); Q19 added (Decision Models tab icon/label). Q1-Q18 unchanged.

---

## Format

Each item is structured as:
- **Question** — the open question from `plan.md` §13
- **Recommended default** — what the plan will implement if the user does not override
- **Rationale** — why this default
- **What changes if overridden** — concrete impact on the plan + which sections/files it affects

---

## Q1 — Endpoint path

**Question:** `POST /v1/decisions` (OpenAI-compatible, aggregator-normalized) — confirm. Also expose `/v1/systemone` native pass-through for native Jev clients?

**Recommended default:** **Yes, `POST /v1/decisions`; no native `/v1/systemone` pass-through at v1.**

**Rationale:** The OpenAI Decision wire is the de-facto standard (liteLLM, OpenRouter both normalize to it — research-web §Q1). The proxy's mission is to be a supervisor on top of standardized wire formats; native pass-throughs fragment the surface. Native Jev clients can speak OpenAI wire to the proxy with one trivial change (build the question array instead of the questions map — a 3-line wrapper). Defer until product signal.

**What changes if overridden:**
- **Approve**: implement as recommended. Route registered at `cmd/main.go:~285`. `pkg/decision/handler.go` has one entry point.
- **Override (add `/v1/systemone`)**: add a second route registration; add a second handler entry that translates on the way out without translating on the way in. ~150 extra lines + 5-6 extra unit tests. Pure additive; no risk to the recommended path.

---

## Q2 — Model kind value

**Question:** `models.kind` new value `"decision"` — confirm discriminator string (NO migration needed).

**Recommended default:** **`"decision"` (lowercase, hyphenated).**

**Rationale:** `pkg/models/config.go:90-101` declares `KindChat = "chat"` and `KindImageGen = "image-gen"`. Lowercase + hyphenated is the existing convention. The `kind` column is unconstrained TEXT (research-codebase §1; migration 030 has no CHECK constraint) — no migration required.

**What changes if overridden:**
- **Approve**: implement as recommended. `pkg/models/config.go:90-101` adds `KindDecision = "decision"`. `pkg/proxy/handler.go:455-462` guard code becomes `"decision_misroute"`. FE `types.ts:121` adds `'decision'` to the kind union.
- **Override (`"decision_model"` or `"eval"` or other)**: one-line change at the const declaration site + one-line change at the default arm's error message + one-line change at FE `types.ts:121`. The discriminator string is also user-visible in the FE tab label and the `data-tab-id` attribute. Choose a string that's idiomatic + descriptive.
- **Override (`"decisions"` plural)**: rejected for symmetry — chat is `chat` (singular), image-gen is `image-gen` (singular + hyphenated). Use `"decision"` for consistency.

---

## Q3 — Image policy

**Question:** Base64-only from day 1 (guide-compliant), or allow hosted URLs with proxy-side fetch-and-encode? Jev path is text-only regardless — 400 or drop?

**Recommended default:** **Posture (NOT validator check) per finding 31** — `pkg/decision/openaidecision.go` forwards data URLs as-is (no proxy-side hosted-URL fetch); `pkg/decision/typesafe.go` `BuildUpstream` rejects any `input_image` part with 400 + house OpenAI envelope (Jev is text-only). **Documented as design posture, not enforcement**: no edge validator checks the `image_url` shape; the upstream may accept base64 + reject hosted URLs (the documented guide behavior; the reference schema differs — R2). Operators are expected to follow the OpenAI guide.

**Rationale:** OpenAI Decisions API is in beta with conflicting docs (research-web §Q1 unverified #3) — the API reference says hosted URLs are allowed, the guide says they're not. LiteLLM chose base64-only; the safer path is base64-only. For typesafe, the Jev model is text-only by spec (research-web §Q2); the proxy returning 400 with a message like "this decision model uses TypeSafe (Jev) which does not support image input" is clearer than silently dropping images. **The plan deliberately does NOT add a validator check on `image_url`** — finding 31 clarified this as posture (forward the bytes as the operator sent them), not enforcement (a proxy-side `image_url` shape check). The upstream is the authoritative source for "is this image URL acceptable?"; the proxy's job is to forward.

**What changes if overridden:**
- **Approve (posture)**: implement as recommended. `pkg/decision/openaidecision.go` forwards image parts as-is. `pkg/decision/typesafe.go` `BuildUpstream` rejects any `input_image` part with 400 + house OpenAI envelope. No edge validator added.
- **Override (proxy-side fetch-and-encode hosted URLs for openai-decision)**: add an HTTP fetch step in `BuildUpstream` for the openai-decision path, with SSRF guard (mirror `pkg/mcp/mcp.go:59` `ValidateUpstreamURL`). ~80 extra lines + SSRF hardening + 3-4 unit tests. Defers a decision the upstream vendor is still making — risk for a future schema reversal.
- **Override (silently drop images for typesafe)**: emit a server log warning but accept the request and serve decisions on text-only. Less explicit but more permissive; trades clarity for UX. ~10 lines.

---

## Q4 — Cross-format validation

**Question:** Reject at proxy edge what cannot translate (choice with 1 option, score with 1 level, true+"true" collision, >255 choices, >10 levels, >128 questions) — adopt liteLLM's 400-before-upstream pattern?

**Recommended default:** **Yes, adopt liteLLM's gotcha list at proxy edge as typed predicates** (amendment 4 — see `pkg/decision/validators.go`).

**Rationale:** LiteLLM v1.104.2+ ships exactly this translation (research-web §Q2); their gotcha list is battle-tested. Validating at the proxy edge saves an upstream call, returns a clear error message, and avoids silent partial translation (e.g. truncating to first 10 levels and not telling the client). With the hybrid typed seam (amendment 4), the validators become typed predicates on `*DecisionRequest` — testable and pure. `typesafe.BuildUpstream` re-asserts a subset before emitting System One (defense-in-depth against future OpenAI Decision schema additions the common validator hasn't learned).

**What changes if overridden:**
- **Approve**: implement as recommended. `pkg/decision/validators.go` has 6 typed predicates (`validateQuestionsCount`, `validateChoicesPerQuestion`, `validateScoresPerQuestion`, `validateBooleanStringCollision`, etc.); handler runs them once after parsing; `typesafe.BuildUpstream` re-asserts before emit. ~80 extra lines + 6 unit tests.
- **Override (let upstream handle it)**: remove the 6 validators; rely on upstream's own validation. ~80 fewer lines + 6 fewer tests. Saves code but risks confusing error messages (upstream may not return the OpenAI error envelope shape).

---

## Q5 — Metering

**Question:** Both providers bill input-only — map input_tokens into existing hourly usage counters; decide treatment of Jev's free output_tokens (log-only?); decision-specific counters probably unnecessary. **AND** (per architect amendment 5) success-only vs failure-billed?

**Recommended default:** **Map `input_tokens` + `output_tokens` into existing writers (`Increment` + `IncrementModelUsage`) on **success only** (chat semantics); no decision-specific counters at v1; document the kind-blend caveat + the success-only policy in the blueprint.**

**Rationale (metering scope):** Both providers return `{input_tokens, output_tokens}` (research-web §Q1, §Q2). The existing writers (`pkg/usage/counter.go:68, 194`) accept both fields; no schema change needed. Jev's free output_tokens are still metered (zero cost, but the count is real — useful for analytics). The kind-blend caveat (surprise #6) is recoverable at the model-id level via `model_hourly_usage.model_id` → `models.kind='decision'`; that's a FE-only follow-up (out of v1 scope).

**Rationale (failure-policy, amendment 5 — important):** Chat (`pkg/proxy/handler.go:899`/`:915`) writes `Increment` only on **success**. Imggen (`pkg/imggen/handler.go:601-607`) writes `IncrementTokenImages` on **both** success and failure (with `imageCount=0` on failure — `request_count=1` is still billed). Mirroring imggen would make decision the only token-billed route that counts failures, mixing a third semantic into FE totals. **The plan adopts chat's success-only semantics**: `Increment` is called only after a successful upstream response. Failure telemetry flows via the `DecisionEvent` published to `events.Bus`. This is a **deliberate divergence from imggen's `meterFailure`** — documented in `plan.md` §7.2 and R19.

**What changes if overridden:**
- **Approve**: implement as recommended. `pkg/decision/handler.go` `meter` calls `Increment` + `IncrementModelUsage` after successful upstream only; detached 5s ctx (mirror `pkg/imggen/handler.go:581`). ~30 extra lines; no new tests for writers (existing writer tests cover).
- **Override (migration 031 `decision_count` — separate counters)**: trigger the ~10-12 coupled sites documented in `plan.md` §7.4. Migration ×2 dialects + registry append + 4 writer signatures + 2 UPSERT shapes + FE usage SQL columns. **Deferred trigger only** — do NOT do speculatively.
- **Override (imggen-style failure billing — `request_count=1` on failure)**: adopt imggen's `meterFailure` semantics. ~10 extra lines in `meterFailure`. Decision is now the only token-billed route counting failures; document the divergence explicitly in the blueprint + FE usage view (operators may be confused).
- **Override (Jev output_tokens log-only, not metered)**: small win on storage but loses ability to compare token efficiency vs chat. ~10 fewer lines.

---

## Q6 — Racing/fallback

**Question:** Decisions are cheap+fast — include decision models in race/fallback chains, or carve out like imggen (no race, sync + long deadline)?

**Recommended default:** **Carve out like imggen** — no race, no fallback chain, sync + `DecisionTimeout`.

**Rationale:** Imggen's posture is "exactly one upstream attempt" (research-codebase §4, surprise-free 🟢); `validateImageGenRules` rejects `FallbackChain` non-empty (D5 analog). Both vendors return synchronous responses (no streaming); racing doesn't help. The proxy's race coordinator is designed for streaming chat where racing saves latency on idle/slow upstreams; decisions are short and fast already.

**What changes if overridden:**
- **Approve**: implement as recommended. `validateDecisionRules` rejects `FallbackChain` non-empty (D5). The decision handler is sync, single-attempt, no race coordinator integration.
- **Override (race decision models)**: integrate with `pkg/proxy/race_coordinator.go`. Significantly more complex — race coordinator is streaming-aware; decisions are sync. ~300-500 extra lines + a non-trivial design question about whether to spawn N upstreams or retry-on-error. Defer until product signal.

---

## Q7 — Misroute guard

**Question:** Add `IsDecision()`-style guard rejecting decision-kind models on `/v1/chat/completions` (imggen precedent at `pkg/proxy/handler.go:455`)?

**Recommended default:** **Yes — and add a sibling guard at `/v1/messages` (the Anthropic route)** (amendment 1), plus the reverse-direction `IsDecision()` check on the decision resolver (closes the imggen reverse-direction gap; surprise #2).

**Rationale:** Imggen's chat-side guard (`pkg/proxy/handler.go:455-462`, verified) is one-directional — only rejects image-gen on the chat path. The reverse direction (chat model posted to `/v1/image_generation`) is **not** guarded by `IsImageGen()` in the imggen resolver — it's only stopped by the provider-registry lookup (research-codebase §2 surprise #2). Decision ships with **both** guards as a quality bar. **Amendment 1 added a third guard**: `pkg/proxy/handler_anthropic.go:~155-183` (the `/v1/messages` post-resolve path) had **zero** misroute checks today (verified: `grep -nE 'IsImageGen\|IsDecision' pkg/proxy/handler_anthropic.go` → 0 hits). A decision model POSTed there would execute a real chat upstream call (the money-burning scenario). Anthropic envelope via `models.NewAnthropicError` (the route's envelope family). A future commission can backport the fix to image-gen's analogous gap on the same route.

**What changes if overridden:**
- **Approve**: implement as recommended. Three sites:
  - `pkg/proxy/handler.go:455-462` adds `IsDecision()` branch with code `"decision_misroute"` (OpenAI envelope).
  - **`pkg/proxy/handler_anthropic.go:~155-183`** (NEW per amendment 1) adds `IsDecision()` branch with Anthropic envelope.
  - `pkg/decision/handler.go` `resolveModel` explicitly checks `model.IsDecision()` and returns 400 with house OpenAI envelope if not.
  - ~10 lines per site + 3 unit tests (one per site, including the new Anthropic-route test).
- **Override (chat-side guard only, no reverse, no Anthropic)**: one site; mirrors imggen exactly. ~5 lines. Loses the reverse-direction closure AND fails to close the Anthropic-route gap (decision models can still hit `/v1/messages` and execute a real upstream call).
- **Override (no chat-side guard, let downstream handle)**: relies on the chat handler receiving an unknown model id and returning its own 400. Cleaner surface but harder to debug; not advised.

---

## Q8 — Upstream registry

**Question:** Support `openai` + `typesafe` provider adapters now; keep OpenRouter/liteLLM as fallback transports?

**Recommended default:** **Add `openai` (reused; nothing added — see Q11 for the corrected identity rationale per amendments 2-3) + `typesafe` (new); OpenRouter/liteLLM as future transports (out of v1 scope).**

**Rationale:** The user requirement is at least two providers (OpenAI + Jev). Adding OpenRouter/liteLLM as transports for v1 expands the credential surface for no immediate user value — clients can route via the OpenAI + typesafe providers already, and aggregator URLs are well-known constants. Defer until product asks.

**What changes if overridden:**
- **Approve**: implement as recommended. `pkg/providers/factory.go` adds **1 const** (`ProviderTypesafe`); `openai` is reused from the existing chat registry (amendment 2 corrected the prior collision claim). `pkg/decision/` adds 2 provider files (`openaidecision.go` keyed on `"openai"`; `typesafe.go` keyed on `"typesafe"`). Under override A (`openai-decision` + `typesafe`), `pkg/providers/factory.go` adds 2 consts instead of 1; both `pkg/decision` files renamed accordingly.
- **Override (add OpenRouter + liteLLM too)**: 4 more provider files; 4 more consts; 4 more metadata entries; 4 more credential test cases. ~600 extra lines; the proxy can route via either direct upstream or via the aggregator. Adds value if users want to reach additional vendor networks without signing up for individual accounts.

---

## Q9 — `DecisionTimeout` config surface

> **Superseded by Q14 (per approver polish round 3, item 1)**. Q14 is the canonical home for this question; defaults agree (Option A config quartet / 30s / `[10s, 60s]`). Q9 retained here for stable numbering — references to "Q9" elsewhere in plan history remain valid. See **Q14** for current rationale + override pricing.

**Question (historical):** Config quartet (mirror `ImageGenTimeout`) vs package constant.

**Recommended default (historical — agrees with Q14):** **`DecisionTimeout` config quartet** (mirrors `ImageGenTimeout`; operator-tunable). **Default 30s; clamp `[10s, 60s]`** (per architect amendment 8).

**Rationale (historical):** `ImageGenTimeout` is a config knob with default 120s, clamp `[30s, 4min]`, accessor on `ManagerInterface` + both impls (research-codebase §8). Operators tune it per-deployment. Decision-mirror surfaces are short — but the vendor "10x faster than Responses API" claim is **unverified marketing** (amendment 8). A 60s default would mask slow upstreams; a 30s default + tight clamp surfaces latency issues early. Tune post-deploy on live data.

**What changes if overridden (historical):**
- **Approve (config quartet, 30s/[10s,60s])**: implement as recommended. 4 surface changes in `pkg/config/config.go`; ~20 lines + 2 unit tests.
- **Override (config quartet with wider defaults — 60s/[15s,2min])**: relax the default. Operator may want more headroom if live tests show 30s is too tight. ~3 lines changed.
- **Override (package constant)**: simpler; `pkg/decision/handler.go` has `const defaultDecisionTimeout = 30 * time.Second`. Operator cannot tune per-deployment. ~5 lines, no config surface, no tests. Trade-off: less operator flexibility for less code.

---

## Q10 — `ExcludeFromUltimateSwitching=true` enforcement

**Question:** Adopt explicit enforcement (D9) given the doc/code drift in image-gen (surprise #4)?

**Recommended default:** **Yes, explicit enforcement** via D9 in `validateDecisionRules` + inline mirror + DB write path.

**Rationale:** Image-gen's doc says it forces `ExcludeFromUltimateSwitching=true` on write but no code enforces it (research-codebase §4 surprise #4 — `grep 0`). Decision ships with **explicit** enforcement: `validateDecisionRules` (D9) + the inline mirror in `pkg/ui/server.go` POST + PUT + the DB write path (`validateModelAgainstCredentials`). This means a decision model with `ExcludeFromUltimateSwitching=false` will be rejected at the FE API and at the DB write. Ultimate-model shadowing is impossible.

**What changes if overridden:**
- **Approve**: implement as recommended. 3 enforcement sites mirror D9.
- **Override (no enforcement; allow either value)**: matches image-gen's posture; explicit but unenforced. Decision models could be ultimate-shadowed (the ultimate-model handler replaces the body `model` field with its configured ID — for a decision model, this would forward a chat model ID to the decision upstream, which would 400 upstream). Less safe; the unenforced posture already failed for image-gen.
- **Override (enforcement deferred until ultimate-model understands kinds)**: out of scope for v1.

---

## Q11 — Provider id naming (CORRECTED per amendments 2-3)

**Question (corrected):** Reuse `openai` (identity B — recommended) vs `openai-decision` (identity A — override) — both paired with `typesafe`.

**Recommended default:** **`openai` (reused; nothing added) + `typesafe` (new)** — corrected from the original `openai-decision` + `typesafe` recommendation per architect amendment 3.

**Rationale (corrected — the original collision claim was false):**

The original plan asserted that reusing bare `"openai"` for decision models would collide with chat dispatch. **That was wrong** (corrected per architect amendment 2, verified in source):

- Chat dispatch calls `providers.NewProvider(cred.Provider, …)` from `pkg/proxy/internal_handler.go:335` and `pkg/proxy/race_executor.go:28` (variable `newProviderClient`). Decision models never reach those paths — `/v1/decisions` is a separate route, and the chat-side `IsDecision()` guard at `pkg/proxy/handler.go:455-462` gates them out.
- **`pkg/imggen` proves the reuse pattern at scale**: `pkg/imggen/minimax.go:22` keys the imggen-local registry on `"minimax"` — identical to chat `ProviderMiniMax = "minimax"` (`pkg/providers/factory.go:17`). The imggen-local registry (`minimax.go:199-215`, lookup at `:212-214`) never calls the chat factory. **One year of operation, zero collision.**
- The only thing that would reject a decision model with `internal_provider = "openai"` is our *own* `validateDecisionRules` allowlist — a design choice, not a structural constraint.

**Trade-off (amendment 3):**

| Approach | Provider ids | Trade-off |
|---|---|---|
| A (override) | `openai-decision` + `typesafe` | **Operator identity clarity** — distinct ids per kind. Cost: operator enters the same OpenAI API key twice (chat `openai` + decision `openai-decision`) and rotates it in two places. **Mandatory FE display label**: "OpenAI Decisions" (never bare "OpenAI") to avoid visual collision with chat `openai` in the credentials tab. |
| **B (recommended)** | **`openai` + `typesafe`** | **Credential singularity + minimal registry surface** — one OpenAI credential row serves chat and decisions (matches how OpenAI itself works: one key, all endpoints). Mirrors imggen/`minimax` exactly. Cost: operator sees `openai` in both chat and decision forms (mitigated by FE hint: "this OpenAI credential works for chat and decisions"). |

**Why B wins on credentials (decisive):** an operator's OpenAI key is *one* credential. Under A, the operator enters the same API key twice and rotates it in two places. Under B, one credential row serves both — which matches how OpenAI itself works. B's "ambiguity" risk is answered by the truth: the key is for both.

**What changes if overridden:**
- **Approve (`openai` + `typesafe`)**: implement as recommended. `pkg/providers/factory.go` adds 1 const (`ProviderTypesafe`); `openai` is reused from the existing chat registry. `pkg/decision/openaidecision.go` is keyed on `"openai"` (decision-local; does not call `providers.NewProvider`). `pkg/ui/frontend/src/types.ts:109` adds `typesafe`. FE form filter `["openai","typesafe"]`; FE shared-credential hint near the OpenAI credential entry.
- **Override (`openai-decision` + `typesafe`, identity A)**: add 2 consts (`ProviderOpenAIDecision`, `ProviderTypesafe`); rename `pkg/decision/openaidecision.go` const to `"openai-decision"`; `validProviders` += `"openai-decision"`; FE union += `"openai-decision"`; FE form filter `["openai-decision","typesafe"]`; **FE display label "OpenAI Decisions"** (mandatory). ~7 line changes across 5 files. No risk to the recommended path.

---

## Q12 — `pkg/providers` integration

**Question:** Full `Provider` interface impls for both, or credential-validation entries only?

**Recommended default:** **Credential-validation entries only**; decision handler builds its own upstream HTTP client (mirrors imggen — the hybrid typed seam per amendment 4 is internal to `pkg/decision`, not exposed via `pkg/providers`).

**Rationale:** `pkg/providers` is the **chat** provider layer — the `Provider` interface exposes `ChatCompletion` / `StreamChatCompletion`. Decisions have a different wire entirely. Adding full `Provider` impls for the decision adapters would mean (a) defining new chat-shaped methods that don't make sense for decisions, or (b) reusing the chat methods with wire translation — which duplicates the decision handler's translation logic. Imggen's pattern (provider const + chat-layer `NewOpenAIProvider` reuse + dedicated imggen-handler adapter) is cleaner: the chat layer knows the provider is valid; the imggen handler knows the wire. Under the recommended identity B (Q11), the chat layer doesn't even need a new entry for `openai` — only `typesafe` (returning `fmt.Errorf("… not yet implemented")` per house style at `pkg/providers/factory.go:27-32`) is added.

**What changes if overridden:**
- **Approve (minimal)**: implement as recommended. `pkg/providers/factory.go` adds 1 const (`ProviderTypesafe`); `NewProvider` switch returns `fmt.Errorf("… not yet implemented")` for `typesafe` (house style at `pkg/providers/factory.go:27-32`); `openai` is reused as-is. `pkg/decision/` ships its own adapters (`openaidecision.go` keyed on `"openai"`; `typesafe.go` keyed on `"typesafe"`) over the typed seam (amendment 4 — `pkg/decision/types.go` + `pkg/decision/validators.go`). Under override A (Q11), `pkg/providers/factory.go` adds 2 consts and `NewProvider` switch returns `NewOpenAIProvider` for `openai-decision`.
- **Override (full Provider impls)**: build full `Provider` interface impls for both. `openai`/`openai-decision` reuses `NewOpenAIProvider`; `typesafe` requires a custom impl that translates System One ↔ chat format (which doesn't quite work — System One has no `messages[]`). ~300 extra lines; design questions about how to map decisions to chat requests.

---

## Q13 — NEW (amendment 6): FA5 depth — keep 3 sites + parity test vs full single-entrypoint extraction

**Question (new — architect amendment 6):** Kind-rules enforcement topology. The plan inherits the established pattern of 3 enforcement sites (shared `validateDecisionRules` helper via 2 call paths + inline mirrors in `pkg/ui/server.go` POST `:869-947` / PUT `:1080-1143`). The architect verifies the image-gen mirrors are bit-for-bit in sync with each other and with `validateImageGenRules` on all 7 shared rules (one year, zero mirror↔helper divergence). The demonstrated failure is **doc/code drift** (`ExcludeFromUltimateSwitching` is claimed by docs but enforced nowhere for image-gen — a different class from mirror drift), which decision's D9 already fixes.

**Recommended default:** **Keep 3 sites for v1 + add the decision-rule parity test** (P1.9) + record the **binding 4th-kind trigger** for single-entrypoint extraction.

**Rationale:**
1. **Consistency with FA1** — this commission's posture is duplication-for-simplicity with guards, not refactors of stable adjacent code. The full-extraction option (B) rewrites the chat+imggen mirror blocks mid-feature — the same blast-radius class rejected in FA1-C at larger scale.
2. **Empirical drift evidence** — mirror↔helper sync has held for a year. The proven failure mode (a rule forgotten *everywhere*) is exactly what D9-at-all-3-sites already fixes for decision, and what a parity test then locks in.
3. **B's unique bonus (fixing image-gen's D9 doc/code drift)** is out-of-scope creep for a plan whose Appendix explicitly says "NO pkg/imggen changes" — it should be its own small commission or ride the 4th-kind refactor.

**Binding 4th-kind trigger (recorded per architect amendment 9c):** when a 4th model kind is commissioned, **extract a single validation entrypoint AND extract the passthrough kernel from `pkg/imggen` + `pkg/decision` together** — both fire as one trigger. (Caveat: the trigger assumes the 4th kind is also sync + token-billed + no-streaming; a differently-shaped 4th kind — embeddings, audio — would re-open the analysis.)

**What changes if overridden:**
- **Approve (parity test, 3 sites, deferred extraction)**: implement as recommended. `pkg/ui/__tests__/kind_rules_parity_test.go` (or `pkg/models/__tests__/`) asserts POST inline ≡ PUT inline ≡ `validateDecisionRules` for the D1–D9 rule set. ~80–100 LOC + the binding-trigger note in the blueprint. Runs under `make test` (the real gate).
- **Override (full extraction now — fix image-gen D9 drift as a bonus)**: extract a single `ValidateAndMapToHouseError` entrypoint; rewrite the 2 × ~80-line mirror blocks in `pkg/ui/server.go` (POST `:869-947`, PUT `:1080-1143`) to construct a `ModelConfig` and call it. Touches the chat + imggen mirror blocks (retroactively fixes image-gen's `ExcludeFromUltimateSwitching` doc/code drift). ~400-500 LOC; risk concentrated in the rewrite of stable paths; bigger PR. Legitimate override if the image-gen D9 drift is a known concern.

---

## Q14 — NEW (finding 13): `DecisionTimeout` config surface — Option A (committed) vs Option B

**Question:** Config quartet (Option A — mirrors `ImageGenTimeout`) vs package constant (Option B).

**Recommended default:** **Option A — `DecisionTimeout` config quartet** (mirrors `ImageGenTimeout`; operator-tunable). **Default 30s; clamp `[10s, 60s]`** (per architect amendment 8 + finding 13 committing Option A consistently across §3.7 / §8.1 / §13).

**Rationale:** `ImageGenTimeout` is a config knob with default 120s, clamp `[30s, 4min]`, accessor on `ManagerInterface` + both impls (research-codebase §8). Operators tune it per-deployment. Decision-mirror surfaces are short — but the vendor "10x faster than Responses API" claim is **unverified marketing** (amendment 8). A 60s default would mask slow upstreams; a 30s default + tight clamp surfaces latency issues early. Tune post-deploy on live data. The package-constant alternative (Option B) is **explicitly NOT adopted** per finding 13 — it loses operator flexibility.

**What changes if overridden:**
- **Approve (Option A — committed)**: implement as recommended. 4 surface changes in `pkg/config/config.go`; ~20 lines + 2 unit tests.
- **Override (Option B — package constant)**: simpler; `pkg/decision/handler.go` has `const defaultDecisionTimeout = 30 * time.Second`. Operator cannot tune per-deployment. ~5 lines, no config surface, no tests. Trade-off: less operator flexibility for less code. **Sweep**: switching to Option B requires editing §3.7, §8.1, §13 Q9 row, R20, and the §3.7/§8.1 cell in Appendix A — finding 13's whole point was to commit consistently.

---

## Q15 — NEW (finding 8): BuildUpstream error mapping — HTTP status contract

**Question:** When `BuildUpstream` returns an error (per the wiring inside `pkg/decision/handler.go` step 10), what HTTP status does the proxy return?

**Recommended default:** **502 `decision_upstream_build_failed`** on BuildUpstream errors (mirror `pkg/imggen/handler.go:316`); **400 at lifecycle step 5** for typed-validation errors (gotcha predicates). House OpenAI envelope (`models.NewOpenAIError`) for both.

**Rationale:** BuildUpstream errors indicate the proxy itself could not produce a valid upstream request — not an upstream-side issue. 502 (Bad Gateway) is the canonical "upstream request couldn't be constructed" status. Mirrors imggen's posture (`pkg/imggen/handler.go:316` returns 502 on BuildUpstream failure). Typed-validation errors (gotcha predicates at lifecycle step 5) are client-side input errors → 400. This is the same client-vs-proxy-vs-upstream distinction the imggen handler enforces.

**What changes if overridden:**
- **Approve (502 on BuildUpstream, 400 on validation)**: implement as recommended. Adds a single `if err != nil { … 502 `decision_upstream_build_failed` }` branch in `pkg/decision/handler.go` step 10 (after `BuildUpstream`).
- **Override (500 on BuildUpstream)**: 500 hides the upstream-credibility signal — a BuildUpstream error usually means the credential is missing the right format, the URL is malformed, or the model name doesn't resolve. 502 is the more honest status.
- **Override (drop the mapping entirely; panic)**: BuildUpstream errors propagate as Go panics. Worst option — production crashes are worse than 502s.

---

## Q16 — NEW (finding 11): Global ultimate `model_id` kind-gate

**Question:** Should the operator-configured global ultimate `model_id` (free-text input, `SettingsPage:104`) reject decision models?

**Recommended default:** **No — scope-defer** (R21 records the risk; follow-up commission trigger).

**Rationale:** D9 at the model-write site catches the parallel mistake: a decision model is rejected at write time if `ExcludeFromUltimateSwitching=false`. The free-text ultimate `model_id` field is operator-self-managed; if an operator names a decision model as the global ultimate target, they have **explicitly bypassed the gate** (D9 doesn't apply to the ultimate target's kind). Per-kind filtering on the free-text input adds friction without a clear win for v1. R21 records the risk; the follow-up commission trigger is set for when product asks "why is this decision model being ultimate-targeted?".

**What changes if overridden:**
- **Approve (scope-defer)**: implement as recommended. No code change. R21 in the plan records the risk; follow-up commission trigger set.
- **Override (kind-gate the free-text input)**: at write time, when an operator saves a global ultimate `model_id` whose target model's kind is non-chat, return 400 with a clear message ("ultimate model must be a chat model"). ~30 lines + 2 unit tests. Adds friction: an operator who picks a decision model (against the recommended pattern) gets blocked; if the operator wants the override path, they must first change the model's kind. Trade-off: catches the case where an operator fat-fingers a decision model id; costs a clear-confusion moment when the operator intentionally wants the bypass.

---

## Q17 — NEW: Credential `BaseURL` steering under identity B

**Question:** Since identity B (Q11) reuses the chat `openai` credential for decision models, the credential's `BaseURL` (operator-overridable; e.g. Azure-fronted OpenAI proxy or private TypeSafe deployment) would also steer decision traffic. Decide whether decision traffic honors the shared credential's `BaseURL` as-is (recommended default) or per-kind BaseURL resolution.

**Recommended default:** **Honor as-is**: one credential = one endpoint identity. The decision adapter's `BuildUpstream` constructs the upstream URL from `cred.BaseURL` (overriding the per-provider default `https://api.openai.com` or `https://api.typesafe.ai`). This is consistent with how imggen handles minimax (`pkg/imggen/handler.go:286` — `cred.BaseURL` is the source of truth for the upstream URL).

**Rationale:** An OpenAI credential's `BaseURL` is one piece of operator configuration; the credential is the unit of identity. Under identity B, decision traffic is bound by the credential's identity. Per-kind BaseURL resolution would split a single credential across two endpoints, which contradicts the credential-singularity argument from Q11 (one OpenAI key, all endpoints). The natural fit for an operator who wants a different BaseURL for decision is to create a **second credential** with the desired BaseURL (the `pkg/models/credential.go` machinery supports this — no schema change; weight distribution + per-credential routing handles multi-endpoint).

**What changes if overridden:**
- **Approve (honor as-is)**: implement as recommended. No code change vs the plan's existing wiring (`cred.BaseURL` is already used at `pkg/imggen/handler.go:72` for minimax). Per-provider `DEFAULT_BASE_URL` (openai = `https://api.openai.com`, typesafe = `https://api.typesafe.ai`) is the **fallback when `cred.BaseURL == ""`**.
- **Override (per-kind BaseURL resolution)**: add a `kind` discriminator to `CredentialConfig` + per-kind `BaseURL` resolution. ~150 lines + migration 032 + 4 surface changes. Larger surface; defer to a follow-up commission.

---

## Q18 — NEW (finding 3): OpenAI path fidelity — byte-faithful pass-through vs typed re-marshal

**Question:** The openai-decision adapter's `BuildUpstream` — byte-faithful pass-through (capture raw request bytes, rewrite only the `model` field) vs typed re-marshal (parse to `DecisionRequest`, modify `Model`, re-emit bytes via `json.Marshal`)?

**Recommended default:** **Byte-faithful pass-through (committed per finding 3)**. The openai-decision adapter captures the raw request bytes; the `model` field is rewritten via string substitution (NOT via typed re-marshal); the bytes are forwarded to OpenAI verbatim. **No typed re-marshal** — unknown future OpenAI Decision fields flow through without modification.

**Rationale:** LiteLLM v1.104.2+ and OpenRouter both normalize to the OpenAI Decision wire — the de-facto standard. The proxy's job is to forward the wire as-is with only the `model` rewritten (so a proxy client sending `"model":"decision-router"` reaches OpenAI as `gpt-6-luna`). Typed re-marshal via `json.Marshal` on a struct that does not model a future OpenAI Decision field would **silently drop** that field — a forward-compat bug. `DisallowUnknownFields` would **reject** unknown fields — also a forward-compat bug (rejects new features). The byte-faithful path is the only forward-compatible option. Recorded as A26 in §12.3.

**What changes if overridden:**
- **Approve (byte-faithful — committed)**: implement as recommended. `pkg/decision/openaidecision.go` captures the request body bytes before parsing (or buffers them in a `bytes.Buffer` that the parser doesn't consume). The `model` field is rewritten by string substitution (e.g. `bytes.Replace(reqBody, []byte("\"decision-router\""), []byte("\"gpt-6-luna\""), 1)`) — the proxy client model name → the upstream model name. Forwarded to OpenAI verbatim. ~30 lines for the byte-faithful path; the typed parse is only used for routing/lookup (provider resolution + BuildUpstream body construction). Trade-off: typed validator can't enforce shape on unknown fields (no defense-in-depth for the openai path; typed re-marshal + DisallowUnknownFields would catch unknown fields — at the cost of forward-compat).
- **Override (typed re-marshal)**: parse to `DecisionRequest`, modify `Model`, re-emit via `json.Marshal(req)`. ~20 lines (simpler!). **Bug**: unknown future OpenAI Decision fields are silently dropped on the way to OpenAI. Documented as the failure mode; **not recommended**.
- **Override (typed re-marshal + `DisallowUnknownFields`)**: parse with `json.NewDecoder(r).DisallowUnknownFields()`; on unknown field → 400. Even worse forward-compat — every OpenAI Decision schema addition (e.g. a new optional `web_search_options` field) would require a typed struct + validator update before clients could use it. **Not recommended**.

---

## Q19 — NEW (approver polish round 3, item 2): Decision Models tab icon/label

**Question:** The `SettingsPage.tsx` tab button construction at `~:351` notes `data-tab-id="decision_models"`, `aria-hidden`, and an emoji span `🎯 or ⚖️ — TBD by user` — this is an orphan user decision not covered by "approve all 18".

**Recommended default:** **`🎯` (target/bullseye)** — recommended default. Conveys "decision" (aimed choice); mirrors the ImgGen 🖼️ (image) emoji pattern at `SettingsPage.tsx`. **Label text**: `Decision Models` (PascalCase, mirroring `ImgGen Models`). `aria-hidden` set per imggen pattern.

**Rationale:** A tab icon must read at-a-glance; "target" is the canonical visual metaphor for "decision" in dashboards (Apple's "snowflake" for snowflake queries, Lucide's `crosshair` for aim, etc.). The ImgGen tab uses 🖼️ (image); a "non-image" tab needs a distinct metaphor — `🎯` (target) reads as "decision" cleanly. `⚖️` (scale/balance) is decision-appropriate but reads as "tradeoff" rather than "aimed choice" — secondary option.

**What changes if overridden:**
- **Approve (🎯 — recommended)**: implement as recommended. ~5 lines (emoji + label string + `aria-hidden` per imggen pattern).
- **Override (⚖️ — scale/balance)**: change the emoji span to `⚖️`. ~1 line.
- **Override (🎯 + ⚖️ as feature-flag, set later)**: ship with 🎯 as committed default; the operator can override later via the existing per-tab emoji seam (no code change for v1 — fallback to the recommended default). Documented as a future minor polish; out of scope for v1.

---

## How to approve / override

Reply with one of:
- **"Approve all"** — implement with all 19 defaults.
- **"Approve Q1-Q19 except: Q5 = override, Q11 = override, Q14 = Option B, Q15 = 500 on BuildUpstream"** — implement with overrides named.
- **"Override: Q3 = fetch-and-encode hosted URLs; Q11 = openai-decision + typesafe; Q13 = full extraction now; Q18 = typed re-marshal + DisallowUnknownFields; Q19 = ⚖️"** — implement with all other defaults.

Each override is treated as item-by-item. The implementer applies the approved defaults; overrides land as one-line to one-file changes per item.

If a question is unanswered, the default applies.