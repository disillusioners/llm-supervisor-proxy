# Architecture Recommendation — `decision` Model Kind

**Date:** 2026-10-15 (UTC) · **Mode:** Standard Design (competitive fan-out, 3 workers) · **Stage:** PLAN-ONLY enrichment of `plan.md` @ `feature/decision-model-type` (b2660a6)
**Method:** 3 skill-equipped analyst workers, one skill each, all anchors verified read-only in this worktree:
`structural-design` (FA1+FA2, instance 631a3087) · `trade-off-analysis` (FA3+FA5, instance 94063882) · `data-flow-design` (FA4+FA6+FA7, instance c0399d99).
**Inputs:** plan.md (884L), decisions.md (Q1–Q12), research-codebase.md, research-web.md.

---

## 0. Verdict at a Glance

| # | Focus area | Verdict | Plan impact |
|---|---|---|---|
| FA1 | Pipeline topology | **Endorse** imggen-style sibling carve-out (`pkg/decision`) | + binding follow-up note (4th-kind kernel trigger) |
| FA2 | Provider abstraction | **Challenge** — replace thin body-map seam with **hybrid typed seam** | Amendment 4 |
| FA3 | Provider identity | **Challenge rationale + recommend reuse `openai`** | Amendments 2–3 (Q11 re-decision) |
| FA4 | Metering | **Endorse** reuse `Increment`/`IncrementModelUsage`, zero migration | Amendment 5 (failure-metering semantics) |
| FA5 | Kind-rules enforcement | **Endorse for v1 with guard** — keep 3-site + add parity test | Amendment 6 (alt: full extraction) |
| FA6 | FE structure | **Endorse** per-kind hook + 9 explicit triple-deletes | Amendments 7, 9c (follow-up helper constraint) |
| FA7 | Isolation & guards | **Endorse design; 🔴 one real gap** (`/v1/messages` unguarded) + 2 🟡 notes | Amendments 1, 7, 9a–9b |

**Overall:** the plan is architecturally sound — topology, zero-migration path, metering reuse, guard design, and FE posture all survive competitive challenge on evidence. One real defect class (a missed entry point) and two design upgrades (typed seam, identity rationale) require amendment before implementation.

---

## 1. FA1 — Pipeline Topology

**Question:** sibling carve-out (`pkg/decision`, plan's choice) vs supervised-chat-pipeline extension vs shared typed-passthrough kernel used by both `pkg/imggen` and `pkg/decision`.

### 5-axis comparison

| Approach | Complexity | Scalability | Maintainability | Risk | Cost | Recommendation |
|---|---|---|---|---|---|---|
| A: sibling carve-out (plan) | **Low** — 1 new package + 1 misroute line + kind rules; ~80% copy-rename verified real (imggen = 1182 LOC across 5 files) | Med — future kinds re-pay duplication | **High** — proven 1-year pattern; import-purity guard, test harness, FE mirror all exist | **Low** — no touch on stable chat/imggen paths | Med — ~1500 new LOC + ~400 test LOC | **✅ ADOPT** |
| B: extend chat pipeline (`pkg/proxy`) | **High** — decisions have no `messages[]`, no streaming, no race; forced-fit branch across `handler.go` (1683L), `race_coordinator.go`, `adapter.go`, 4+ more files | Med — race coordinator is streaming-aware; a decision branch disables it by flag | Low — an 11-layer pipeline gains a 12th kind-flag branch | **High** — live chat path + 60+ test files in blast radius | High | Reject |
| C: shared typed-passthrough kernel | **High now** — kernel must express 15+ handler steps; the single-classifier seam (`pkg/imggen/minimax.go:93-150`, rule at `doc.go:20-24`) is MiniMax-wire-specific | **High** — 4th/5th kind gain real economy | Med — kernel is a stable contract once proven, but `pkg/imggen` must be rewritten to consume it (breaking its import-purity guard, test harness, Makefile audit) | **High** — touches the RAM-incident-stability package (2026-09-22) | High now / Med amortized | Reject for v1; **binding follow-up at 4th kind** |

### Recommendation: **A — endorse the plan's carve-out.**

1. **Blast-radius asymmetry** (dominant axis: Risk). A's blast radius is the feature; C's is the feature plus a working, stability-valuable package. B's is the live chat path.
2. **The codebase has a written policy of local duplication over shared abstraction** for exactly this class — the T1.2.3 inline-mirror design (plan §2.1; `pkg/ui/server.go:863-868` comment). C violates it for a one-time saving.
3. **The "80% copy" is not invariant.** The transferable 80% is the handler shell (`pkg/imggen/handler.go:155-475`, 11 steps in lockstep). The 20% that differs is precisely where the architectural rules live — import purity, single-classifier (`Outcome{RelayStatus, Reason}`, never branch on `resp.StatusCode`), bounded-RAM caps. A kernel must generalize the classifier across *different wire contracts* (MiniMax `base_resp.status_code`-in-200 vs real OpenAI HTTP envelopes), which either forks the Outcome shape or loses the single-classifier guarantee.

**Required addition (🔴):** plan must record — *"If a 4th model kind is commissioned, extract a shared `pkg/passthrough` kernel from `pkg/imggen` + `pkg/decision` together; do not extract speculatively at 3rd-kind time."* The plan currently treats this as an open option; it should be a binding trigger. (Caveat: the trigger assumes the 4th kind is also sync + token-billed + no-streaming; a different shape — embeddings, audio — would re-open the analysis.)

**Confidence: High.** Flip assumption: a 4th kind with the same shape is already scheduled → C now.

---

## 2. FA2 — Translation / Provider Abstraction Inside `pkg/decision`

**Question:** shape of the provider seam; where shared edge validation lives; how openai (near pass-through) and typesafe (full bidirectional System-One translation) plug in; aggregator extension path.

### 5-axis comparison

| Approach | Complexity | Scalability | Maintainability | Risk | Cost | Recommendation |
|---|---|---|---|---|---|---|
| A: thin 3-method body-map seam (plan §3.2 `converter.go`) | Low for openai; **High for typesafe** — ~250 LOC of `map[string]interface{}` key-walking | Low — each aggregator adds more map-key work | Med | **Med-High** — map access bypasses Go's type system; the Q4 gotcha list becomes per-key branches; every key combo is a distinct test path | Low (pure copy-rename) | Reject |
| B: fully typed DTO seam (every adapter typed end-to-end) | Med | High | High | Med — schema changes ripple through structs; ceremony on the pass-through adapter | Med | — |
| C: **hybrid** — typed client-wire structs at the edge; openai-decision re-emits bytes (model-field rewrite only); typesafe does typed struct→System-One translation | Med | **High** — new provider = parse wire → translate structs → emit wire, all pure functions | **High** — gotcha validators are typed predicates; typesafe translation testable on typed outputs | **Low** — each adapter is the simplest thing that works | Med | **✅ ADOPT** |

### Recommendation: **C — hybrid typed seam** (challenge to plan §3.2/§4.2).

- **The edge must parse the client wire anyway.** The Q4/P2.6 gotcha validators (choice-with-1-option, score-with-1-level, `true`/`"true"` collision, >255 choices, >10 levels, >128 questions) cannot be evaluated on an opaque body map without type-assertion ladders. Parsing once into `pkg/decision/types.go` (`DecisionRequest`, `Question`, `DecisionResponse`) makes the validators typed predicates and makes `typesafe.go` a struct→struct pure function — which is exactly how liteLLM's reference bridge (research-web §Q2) is built.
- **`pkg/imggen`'s body-map seam is correct *there*** because that adapter is near-pure pass-through (one field rewrite, ~75 LOC). Copying that shape onto a structural translator is cargo-cult: `typesafe.go` would re-implement a typed translator without the types.
- **Edge validation lives in BOTH places** (defense-in-depth): the handler runs the shared gotcha validators (400, house OpenAI envelope) *and* `typesafe.BuildUpstream` re-asserts the typed invariants before emitting System One — protecting against a future OpenAI Decision schema addition that the common validator hasn't learned.
- **Typed `Usage` struct** replaces the plan's `BillableUsage` rename: `BillableImages` returns `int`; `BillableUsage` returns `(inputTokens, outputTokens int64, err error)` — a different semantic, not a rename. Make it a struct.

### Aggregator extension path (what stays stable vs variable)

| Stable forever (client contract) | Variable per adapter |
|---|---|
| Client wire = OpenAI Decisions shape (`input`, `questions[]`, `safety_identifier`; response `model`/`answers[]`/`usage`) | `BuildUpstream` — openai-decision: bytes + model rewrite; typesafe: typed→System One; future OpenRouter/liteLLM: bytes + baseURL/auth remap (both normalize to OpenAI Decisions wire — research-web §Q1) |
| Error envelope = `models.NewOpenAIError`; gotcha-validator 400s | `InspectResponse` — per-wire classifier |
| No `id`/`object`/`created` synthesis (neither vendor returns them — verified) | `Usage` extraction |

The registry (`registry.go`, mirroring `pkg/imggen/minimax.go:199-215`) grows one entry per provider; the handler is unchanged. **Adding an aggregator later never touches the client-facing wire** — it is a new `BuildUpstream`/`InspectResponse` pair keyed into the local registry.

**Confidence: Medium-High.** This is design judgment on code not yet written; the forcing function (typed validation at the edge) is evidence-backed.

---

## 3. FA3 — Provider Registry & Identity Model

**Question:** new ids `openai-decision` + `typesafe` (plan) vs reusing existing `openai` for OpenAI-backed decisions (imggen reuses `minimax`) vs kind-suffixed ids + central per-kind allowlist table.

### Evidence first: the plan's collision claim is **false**

Plan §5.1 and decisions.md Q11 assert reusing bare `openai` "would collide and break the chat provider dispatch." Verified in source — **it would not**:

- Chat dispatch calls `providers.NewProvider(cred.Provider, ...)` from `pkg/proxy/internal_handler.go:335` and `pkg/proxy/race_executor.go:28` — paths a decision-kind model never reaches (`/v1/decisions` is a separate route; the chat-side `IsDecision()` guard at `pkg/proxy/handler.go:455-462` is the gate).
- `pkg/imggen` proves the reuse pattern at scale: `pkg/imggen/minimax.go:22` registers `"minimax"` — the same id as chat `ProviderMiniMax = "minimax"` (`pkg/providers/factory.go:17`) — and the imggen-local registry (`minimax.go:199-215`, `lookup` :212-214) never calls the chat factory. Zero collision in one year of operation.
- The only thing that would reject a decision model with `internal_provider = "openai"` is the plan's *own chosen gate* (`validateDecisionRules` allowlist, plan §5.6 :390-394) — a design choice, not a structural constraint. `internalProvider ∈ {"openai", "typesafe"}` works identically.
- The provider-match invariant at `pkg/store/database/store.go:1620-1632` is per-model credential consistency, not a per-kind gate.

### 5-axis comparison

| Approach | Complexity | Scalability | Maintainability | Risk | Cost | Recommendation |
|---|---|---|---|---|---|---|
| A: new ids `openai-decision`+`typesafe` (plan) | Med — 2 ids × (factory.go 5 surfaces in 1 file + `validProviders` + FE union) | Med | Med — genuine 3-place drift class (factory.go, credential.go, types.ts); FE types union is type-only, BE list is single-source via `GET /fe/api/providers` (`pkg/ui/server.go:222` → `providers.GetProviders()`) | Low (proven pattern) | Med | Fallback if user prefers explicit per-kind ids |
| B: **reuse `openai` + new `typesafe`** | **Low** — 1 new id; decision-local registry keys `"openai"` exactly as imggen keys `"minimax"` | Med — one credential/one key serves chat + decisions | **High** — mirrors imggen exactly; smallest registry surface | Med — operator sees `openai` in both chat and decision forms (mitigate: FE hint "works for chat + decisions") | **Low** | **✅ RECOMMEND** |
| C: kind-suffixed ids + central per-kind allowlist table in `pkg/models` | High — new table type + refactor of 4 consumers | **High** — future kind = one row | **High** — kills the drift class structurally | Med — refactor touches stable chat+imggen validation paths | High (~200-300 LOC) | Correct long-term answer; over-scoped for v1. Becomes mandatory at 4th kind (same trigger as FA1 kernel) |

### Recommendation: **B — reuse `openai`, add only `typesafe`.**

- **Credential semantics (decisive):** an operator's OpenAI key is *one* credential. Under A, the operator enters the same API key twice (chat `openai` + decision `openai-decision`) and rotates it in two places. Under B, one credential row serves both — which matches how OpenAI itself works (one key, all endpoints). B's "ambiguity" risk is answered by the truth: the key is for both.
- **Precedent:** imggen/`minimax` is the identical shape, one year proven, zero collision.
- **FE filtering by kind is unaffected either way:** `DecisionModelForm` filters the BE-served provider list to a hardcoded allowlist (`["openai","typesafe"]` under B) exactly as `ImgGenModelForm.tsx:10-14` hardcodes `minimax`. `validateDecisionRules` carries the authoritative per-kind allowlist regardless.
- **Under B, `factory.go` needs a `typesafe` entry only** (const + switch arm returning not-implemented for chat + `IsProviderSupported` + metadata). `openai` exists already.

**This amends decisions.md Q11's recommended default.** The user decision point stays (identity model is a values call), but the decision log must present the corrected rationale: the trade is *operator identity clarity (A)* vs *credential singularity + minimal registry surface (B)* — **not** dispatch safety. If the user keeps A, require the FE display label "OpenAI Decisions" (not "OpenAI") to avoid visual collision with chat `openai` in the credentials tab.

**Also corrected:** research-codebase §3's "4 places" framing — `factory.go`'s 5 surfaces are one file edited in one PR; the true cross-file drift class is 3 places (factory.go, credential.go `validProviders`, FE types.ts union), and the FE union is compile-time-checked, not runtime.

**Confidence: Medium-High** (evidence verified; recommendation is a values-weighted call that Q11's owner makes final).

---

## 4. FA4 — Metering

**Question:** reuse existing `Increment`/`IncrementModelUsage` token writers (zero schema, FE blending) vs `decision_count` column (migration 031, ~10-12 coupled sites) vs deferring a kind discriminator on usage tables.

### 5-axis comparison

| Approach | Complexity | Scalability | Maintainability | Risk | Cost | Recommendation |
|---|---|---|---|---|---|---|
| A: reuse Increment writers (plan) | **Low** — 2 calls in `meter()` | Med — blend grows with decision traffic | **High** — zero new writers; exclusivity rule respected (decision uses `Increment` only — never co-call with image writers, counter.go:100-114 double-count trap) | Low — battle-tested, `writeMu`-guarded | **Lowest** (~30 LOC) | **✅ ADOPT for v1** |
| B: migration 031 `decision_count` | High — ~10-12 sites: migrations ×2 dialects + `migrate.go` registry (the twice-hit silent-inert trap) + 2 writers + UPSERT shapes + row fields + test DDLs + FE SQL | High — kind-native counts | Low — every miss is a silent leak (029 precedent) | Med | Highest | Reject for v1 |
| C: defer discriminator | Low now | Med | High | Low | Low now / Med later | ≡ A with a written trigger (plan §7.4 already documents it) |

### Recommendation: **A — endorse the plan (decisions.md Q5 default).**

**Escape hatch verified — and cheaper than B-now:** adding a discriminator later is an *additive* `ADD COLUMN … DEFAULT 0` (029 precedent; backfill-zero; no dual-write window). Attribution after the fact:

- **Model-level: recoverable.** `model_hourly_usage` PK is `(model_id, hour_bucket)` (migration 025) → join to `models.kind='decision'` (migration 030) works; the plan §7.3 join claim is correct for model views.
- **Token-level: never recoverable, by schema.** `token_hourly_usage` carries `token_id` only (migration 019 — no model_id). No later migration fixes historical token-level attribution. Accepted small loss; document it (it is also true under B for pre-B rows).

**🟡 Amendment — `request_count`-on-failure semantics (plan §7.2 defect):** the plan mirrors imggen's `meterFailure` (`pkg/imggen/handler.go:601-607`), which bills `request_count=1, 0 tokens` on upstream errors. Chat — the other user of `Increment` — is **success-only** (`pkg/proxy/handler.go:899/:915`). Mirroring `meterFailure` makes decision the only token-billed route that counts failures, blending a third semantic into FE totals. **Recommend: success-only metering (chat semantics); failure telemetry flows via `DecisionEvent`.** If failure-counting is wanted, state the divergence explicitly in plan §7.2 and Q5 instead of inheriting it silently.

**Confidence: High.**

---

## 5. FA5 — Kind-Rules Enforcement Topology

**Question:** keep the 3-site pattern (helper via 2 code paths + inline mirrors in `pkg/ui/server.go` POST :869-947 / PUT :1080-1143) vs extract a single validation entrypoint.

### Evidence on today's drift

- The image-gen mirrors are **bit-for-bit in sync** with each other and with `validateImageGenRules` on all 7 shared rules (verified by diff) — one year, zero mirror↔helper divergence.
- The demonstrated failure is a different class: `ExcludeFromUltimateSwitching` is claimed by docs but enforced **nowhere** for image-gen (not in `config.go:157-187`, not in either mirror) — a doc/code drift, not a mirror drift. The plan's D9 already fixes this class for decision (enforced at all 3 sites).

### 5-axis comparison

| Approach | Complexity | Scalability | Maintainability | Risk | Cost | Recommendation |
|---|---|---|---|---|---|---|
| A: keep 3 sites (plan) | Low | Med — each new kind = +1 inline block (~80L ×2) + `internal_provider` gate triplication | Med — drift class real but empirically slow | Low | **Lowest** | Adopt for v1 **with guard** |
| A′: keep 3 sites + **parity test** (assert POST mirror ≡ PUT mirror ≡ `validateDecisionRules` for the decision rule set) | Low | Med | **High** — mechanical drift guard, runs in `make test` (the real gate — CI is non-gating, docker-ci.yml:83) | Low | +80–100 LOC | **✅ RECOMMEND** |
| B: extract `ValidateAndMapToHouseError` single entrypoint; POST/PUT construct `ModelConfig` and call it | High — rewrite 2×80L blocks that also encode kind-whitelist + per-field 400 messages; touches chat+imggen behavior | **High** — new kind = 1 function | **High** — single source of truth; bonus: retroactively fixes image-gen D9 drift | Med — risk concentrated in rewrite of stable paths | High (~400-500 LOC) | Strong alternative; becomes the answer at 4th kind |

### Recommendation: **A′ — keep the 3-site pattern for v1, add the decision-rule parity test, record the extraction trigger.**

Worker B's analysis scored full extraction highest (3.40 vs 2.95 weighted) and it is the structurally correct end-state. I land on A′ for this commission on three grounds: (1) **consistency with FA1** — this commission's posture is duplication-for-simplicity with guards, not refactors of stable adjacent code (B rewrites the chat+imggen mirror blocks mid-feature, the same blast-radius class rejected in FA1-C at larger scale); (2) the empirical drift evidence shows mirror↔helper sync holding for a year — the proven failure mode (a rule forgotten *everywhere*) is exactly what D9-at-all-3-sites already fixes for decision, and what a parity test then locks in; (3) B's unique bonus (fixing image-gen's D9 drift) is out-of-scope creep for a plan whose Appendix explicitly says "NO pkg/imggen changes" — it should be its own small commission or ride the 4th-kind refactor.

**Binding follow-up (same trigger as FA1):** 4th kind → extract the single entrypoint and rewrite the mirrors in the same commission. If the user *wants* image-gen's `ExcludeFromUltimateSwitching` drift fixed now, B is the vehicle — that is a legitimate override, priced above.

**Confidence: Medium.** Flip assumption: user prioritizes fixing the image-gen D9 doc/code drift in this commission → B.

---

## 6. FA6 — Frontend Structure

**Question:** new Decision Models tab + `useDecisionModels` hook + 6 dual-invalidation pairs → triple deletes + C-16 extension; per-kind hook (plan) vs generic `useModelsByKind` refactor vs shared invalidate helper.

### 5-axis comparison

| Approach | Complexity | Scalability | Maintainability | Risk | Cost | Recommendation |
|---|---|---|---|---|---|---|
| A: per-kind hook, 9 explicit deletes (plan §6.4) | **Low** — pure mirror of verified-SAFE pattern (distinct cache keys, deletion-only mutations; `delete()` clears cache+promises maps, apiCache.ts:131-141) | Med — 4th kind ⇒ 12 deletes | Med | **Low** | Lowest | **✅ ADOPT for v1** |
| B: generic `useModelsByKind(kind)` refactor | High — renames the `'models'` cache key, touches both existing hooks + tests | **High** — prefix invalidation scales | High | Med — key churn; `deleteByPrefix('models')` **misses in-flight-only promises and doesn't match `*-models` keys** | Highest | Reject for v1 |
| C: shared `invalidateAllModelLists()` helper | Low-Med | **High** — one edit per kind | **High** — single choke point | Low | Low | Named follow-up (with the constraint below) |

### Recommendation: **A for v1 — endorse the plan** (`useDecisionModels` mirroring `useImgGenModels`, cache key `'decision-models'`, 9 explicit triple-deletes, C-16 single-site extension at `ModelsTab.tsx:157` — `undefined !== 'image-gen'` today, so chat rows are already safe under the `&& kind !== 'decision'` extension).

Verified context: the apiCache stale-write race is real (in-flight GET repopulates a deleted key on resolve — `getOrFetch` :80-84 checks promises, fetcher `.then` sets unconditionally :89-92; bounded by 15s TTL). A third key grows the exposure windows **linearly** (2→3 per mutation), not compound — acceptable at this scale.

**Constraints recorded for the follow-up (and for the plan text):**
1. The helper must use an **explicit key list** `['models','imggen-models','decision-models']` — NOT `deleteByPrefix('models')`: the prefix matches none of the actual keys (`imggen-models`, `decision-models` don't start with `models`) and prefix deletion misses in-flight-only promise entries.
2. v1's drift guard is test-shaped: extend the `useModels`/`useImgGenModels` mutation tests to assert all three keys are deleted on every mutation (the FE analog of FA5's parity test). The plan's §6.9 `useDecisionModels.test.ts` covers the new hook only.
3. One Worker-A dissent noted: ship the helper in v1. Rejected here for the same mid-feature-refactor discipline as FA5 — but the helper is small enough that either posture is defensible; if the implementer finds itself touching all 6 sites anyway, extracting the 5-line helper is acceptable.

**Confidence: High.**

---

## 7. FA7 — Isolation & Guards

**Design endorsed with one 🔴 gap and two 🟡 notes.** Guard matrix (entry point × guard × status, all anchors verified in-source):

| Entry point | Guard | Status |
|---|---|---|
| `/v1/chat/completions` | `IsDecision()` reject @ `pkg/proxy/handler.go:455` (post-resolve, pre-auth :465), code `decision_misroute` | ✅ planned (plan site 5) |
| `/v1/messages` (Anthropic) | **NONE** — `handler_anthropic.go:155-183` resolves on a separate path; zero misroute checks in file (grep 0 hits) | 🔴 **GAP — plan misses it (Amendment 1)** |
| `/v1/decisions` | explicit `IsDecision()` in resolver (Q7) | ✅ planned — closes imggen's reverse gap for decision |
| `/v1/image_generation` | resolver :715-725 checks `m == nil` only (doc-comment drift :711-713); real barrier = minimax-only registry :302-315 → 502 pre-flight | 🟢 benign — **no upstream call, no metering** (meter :486 is post-upstream); plan note (Amendment 9a) |
| `/v1/models` | `IsImageGen()` :258 → add `‖ IsDecision()` (plan site 6) | ✅ verified anchor |
| `/fe/api/models` | `?kind=` switch :758-770; default arm `case "": nil` returns all kinds (server.go:763-764) — hence C-16 is a client filter | 🟡 see token-pickers below |
| Ultimate switching | `ShouldTrigger`/`ForceTrigger` called **only** from chat flow (`pkg/proxy/handler.go:667-672`; 0 hits in anthropic/imggen/main) — `/v1/decisions` can never be ultimate-shadowed even without D9; the :455 guard also preempts `X-Force-Ultimate` shadowing | ✅ D9 = belt-and-suspenders, exactly as plan claims |
| events.Bus → FE | list patcher is type-dispatched (`useEvents.ts:259` PATCH_TYPES, :266 REFRESH_TYPES, others ignored); imggen events carry no `id` ⇒ dropped by the per-request accumulator (:107 reqId gate) | ✅ `DecisionEvent` mirroring `ImageGenEvent` is FE-inert; no chat-list corruption |
| gzip / body caps | `CompressResponse` scoped `/fe/api/*` (gzipmw/response.go:307-311) — no-op for `/v1/*`; caps are per-package constants | ✅ `pkg/decision` self-contained (own 16MB/64MB constants) |

### 🔴 GAP 1 — `/v1/messages` (Amendment 1)
A decision model POSTed to the Anthropic route resolves (`anthropic.go:158`), passes internal auth (:246), and executes a **real upstream call** via internal passthrough (:573) on the chat wire — the money-burning scenario the misroute-guard class exists to prevent. **Required:** post-resolve `IsDecision()` reject in `handler_anthropic.go` emitting an **Anthropic-envelope** 400 (`models.NewAnthropicError` — the route's envelope family). This is also a **pre-existing image-gen gap** (zero `IsImageGen` checks in the file) — record as an optional backport note; do not scope-creep this commission.

### 🟡 GAP 2 — token pickers + ultimate target (Amendment 7)
- `TokenForm.tsx:157` (per-token ultimate-model `<select>`, unfiltered over `[...models]` — already lists image-gen today, pre-existing quirk) and the allowed-models picker (:101) will both list decision models. One-line `kind` exclusion each, mirroring C-16.
- Global ultimate `model_id` is free-text (`SettingsPage:104`); D9 gates *switching for the requested model* (:677) — it does **not** prevent configuring a decision model as the ultimate *target*. Plan note; no code change required for v1.

### Import purity — endorsed, 3-layer
Makefile grep + in-package `TestPackage_ImportGuard_NoProxyImports` + **NEW** `TestPackage_ImportGuard_TransitiveClosure` (asserts no dep under `pkg/proxy` **or** `pkg/imggen`) — strictly stronger than imggen's guard (direct imports only; closure was a manual `go list -deps` audit). Sound; adopt as planned (plan §3.5).

**Confidence: High** (every row verified in-source).

---

## 8. Consolidated Recommended Architecture

1. **Topology:** new `pkg/decision` sibling, imggen-modeled, sync single-attempt, own handler/client/converter/registry/adapters. No race/fallback/loop-detection. Import-purity 3-layer guard incl. transitive closure. *(FA1 — as planned)*
2. **Provider seam:** hybrid typed seam — `pkg/decision/types.go` with `DecisionRequest`/`Question`/`DecisionResponse`/`Usage` structs; handler parses client wire once, runs shared gotcha validators (typed predicates, 400 + house OpenAI envelope); `openaidecision.go` re-emits bytes with model-field rewrite; `typesafe.go` does typed→System-One bidirectional translation and re-validates invariants (defense-in-depth). Local registry keyed on `cred.Provider` (minimax pattern). *(FA2 — amended)*
3. **Identity:** reuse provider id `openai` for OpenAI-backed decisions; add only `typesafe`. One OpenAI credential serves chat + decisions. `validateDecisionRules` allowlist `{"openai","typesafe"}`; FE form filters BE-served list to the same pair; FE hint text on the shared credential. *(FA3 — amended; Q11 user-confirmable)*
4. **Metering:** reuse `Increment` + `IncrementModelUsage` (never the image writers — exclusivity rule); detached 5s context; **success-only** `request_count` semantics; zero migrations. Model-level attribution recoverable via `model_hourly_usage.model_id → models.kind` join; token-level attribution permanently blind (schema-inherent, documented). *(FA4 — endorsed + amended)*
5. **Kind rules:** `KindDecision` + `IsDecision()` + `validateDecisionRules` (D1–D9) at the 3 established sites; **+ decision-rule parity test** locking POST ≡ PUT ≡ helper. 4th-kind trigger recorded for single-entrypoint extraction. *(FA5 — endorsed + guarded)*
6. **Guards:** chat-side `IsDecision()` 400 (`decision_misroute`); **`/v1/messages` post-resolve `IsDecision()` Anthropic-envelope 400 (new site)**; reverse `IsDecision()` in decision resolver; `/v1/models` exclusion; D9 ultimate exclusion enforced at write; TokenForm picker filters. *(FA7 — endorsed + amended)*
7. **FE:** `decision_models` tab, `DecisionModelsTab`/`DecisionModelForm` (provider dropdown = only divergence from ImgGen form), `useDecisionModels` hook (`'decision-models'` key), 9 explicit triple-deletes with mutation-test coverage, C-16 extension at `ModelsTab.tsx:157`, types.ts unions extended, tsc 26/11 held, same-commit bundle discipline. *(FA6 — as planned)*
8. **Config:** `DecisionTimeout` quartet; default **30s**, clamp **[10s, 60s]** (60s default masks slow upstreams; vendor "10x faster" claim is unverified marketing — tune post-deploy). *(amended from plan §3.7)*

## 9. Plan Amendments Required

1. 🔴 **Add the `/v1/messages` misroute guard** to plan §3.1 (site list), §3.4, P1.3, and §9.2 test table: post-resolve `IsDecision()` reject in `pkg/proxy/handler_anthropic.go` (~:155-183 path), Anthropic-envelope 400 via `models.NewAnthropicError`. Add a non-blocking note: pre-existing image-gen gap on the same route (optional backport, out of v1 scope).
2. 🔴 **Correct the false collision rationale** in plan §5.1 (line ~337) and decisions.md Q11 (line ~173): reusing `openai` does **not** break chat dispatch (chat calls `providers.NewProvider` from `internal_handler.go:335`/`race_executor.go:28`; imggen reuses `minimax` identically). Re-frame Q11's trade-off as operator identity clarity vs credential singularity.
3. 🟡 **Change Q11's recommended default to `openai` (reused) + `typesafe`**: drop `openai-decision`; `validateDecisionRules` allowlist `{"openai","typesafe"}`; FE form filter `["openai","typesafe"]`; factory gains only the `typesafe` entry; FE hint "this OpenAI credential works for chat and decisions". Keep `openai-decision`+`typesafe` as the documented override (with mandatory FE label "OpenAI Decisions" if chosen).
4. 🟡 **Replace the body-map seam with the hybrid typed seam** (plan §3.2 `converter.go`, §4.2, P2.2/P2.3): add `pkg/decision/types.go`; gotcha validators as typed predicates; `BillableUsage` → typed `Usage` struct; `typesafe.BuildUpstream` re-validates (defense-in-depth).
5. 🟡 **Fix metering failure semantics** (plan §7.2, Q5): do NOT mirror imggen `meterFailure`'s failure-billed `request_count`; meter success-only (chat semantics); failure telemetry via `DecisionEvent`. If failure-counting is desired, document the divergence explicitly instead of inheriting it.
6. 🟡 **Add the decision-rule parity test** (plan §9.2 / P1.5): assert `ui/server.go` POST inline ≡ PUT inline ≡ `validateDecisionRules` for the D1–D9 set (~80–100 LOC). Record the binding 4th-kind trigger for single-entrypoint extraction (and note full extraction now as the priced alternative that also fixes image-gen's D9 drift).
7. 🟡 **Add TokenForm picker filters + ultimate-target note** (plan §6 / new sub-section): `TokenForm.tsx:157` ultimate-model select and `:101` allowed-models picker exclude non-chat kinds (one-line each, mirrors C-16); note that global ultimate `model_id` (free-text, `SettingsPage:104`) is not kind-gated — accepted for v1.
8. 🟢 **Tighten `DecisionTimeout`** (plan §3.7/§8.1, Q9): default 30s, clamp [10s, 60s] — not 60s/[15s, 2min]. Tune post-deploy on live data.
9. 🟢 **Plan notes to add:** (a) imggen's minimax-registry barrier makes decision-model-on-`/v1/image_generation` a benign pre-flight 502 (no upstream call, no metering) — no action; (b) the FE invalidation follow-up helper must use an explicit key list `['models','imggen-models','decision-models']`, never `deleteByPrefix` (prefix matches none of the keys; misses in-flight-only promises); (c) binding follow-up: 4th kind triggers both the passthrough-kernel extraction (FA1) and the validation-entrypoint extraction (FA5); (d) FE mutation tests must assert triple-key deletion on all model hooks.

## 10. No-Change Endorsements

- **FA1 topology** — sibling carve-out; chat-pipeline extension and shared kernel correctly rejected for v1.
- **Zero-migration Path A** (kind value in unconstrained migration-030 TEXT column; gatekeeper = validation sites) — verified sound; the 5-site schema-change checklist is correctly *not* triggered.
- **FA4 reuse of existing usage writers** — Q5 default stands (with Amendment 5's semantics fix).
- **FA6 FE shape** — per-kind hook, explicit triple-deletes, C-16 single-site extension, tsc + same-commit bundle discipline.
- **Guard design** — chat-side `IsDecision()` guard, reverse resolver check (closing imggen's documented reverse gap), `/v1/models` exclusion, import-purity 3-layer incl. the new transitive-closure test, D9 `ExcludeFromUltimateSwitching` enforcement at all 3 sites (verified belt-and-suspenders — ultimate gating exists only in the chat flow, `handler.go:667-672`).
- **`DecisionEvent` on events.Bus** — verified FE-inert (type-dispatched patcher; no chat-list corruption).
- **Q6 carve-out posture** (no race/fallback, sync + timeout), **Q12 minimal `pkg/providers` integration** (credential-validation entries; decision-local HTTP client), **Q1 no native `/v1/systemone` pass-through at v1**, **Q2 kind string `"decision"`**, **Q3 base64-only + typesafe 400-on-image**, **Q4 proxy-edge gotcha validators** — all stand.
- **Phasing P1→P4** — dependency order is sound; amendments above slot into P1 (1, 6), P2 (3, 4, 5), P3 (7), §3.7 (8) without re-ordering.

## 11. Decisions Pending (user)

1. **Q11 identity model** — recommend reuse `openai` + `typesafe` (Amendment 3); `openai-decision` + `typesafe` remains the documented override.
2. **FA5 depth** — keep-3-sites + parity test (recommended) vs full single-entrypoint extraction now (fixes image-gen D9 drift as a bonus; ~400–500 LOC, touches stable mirrors).
3. **Q5 failure metering** — success-only (recommended) vs imggen-style failure-billed `request_count` (must then be documented as a semantic divergence).
4. **Q9 timeout default** — 30s/[10s,60s] (recommended) vs plan's 60s/[15s,2min].

## 12. Open Questions

- OpenAI Decisions `input_image` hosted-URL discrepancy (research-web §Q1 conflict) — resolve at live e2e (P4.2); base64-only posture is correct either way.
- TypeSafe auth-header format (Bearer inferred, not verbatim-verified) — one-line fix path if live test rejects.
- System One `criteria` field shape for `predicate` (plan §4.2 treats as no-op) — verify at live e2e.
- Whether a 4th kind exists on any roadmap — determines whether the FA1/FA5 extraction triggers fire soon (both recorded as binding follow-ups, not speculative work).

---

*Aggregation: Architect controller from three verified worker reports (instances 631a3087 / 94063882 / c0399d99). All file:line anchors in this document were verified read-only in the worktree by the analysts. Dedup applied: FE-invalidation findings merged with Worker C's `deleteByPrefix` caveat retained as most specific; provider-identity findings reconciled with the collision-claim correction taking precedence.*
