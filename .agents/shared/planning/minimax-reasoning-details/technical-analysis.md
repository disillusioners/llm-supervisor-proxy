# Technical Analysis: MiniMax reasoning_details ↔ reasoning_content Translation

Date: 2026-08-18T05:30:09 UTC (Tuesday)
Author: planner[v2] via technical-analysis worker
Analysis depth: deep-dive
Status: Round 2 — binding decisions incorporated

**Round 2 binding decisions (D1-D4)** are incorporated throughout the phase plans (see `plan-overview.md`, `phase{1,2,3}-plan.md`, `architecture-recommendation.md` §11). This technical-analysis document is preserved as the structural reference; updates below track only the binding-decision deltas.

## Question

How should llm-supervisor-proxy translate between MiniMax's `reasoning_details` (array-of-typed-entries wire format) and the client-facing `reasoning_content` (single string per-message contract) when:

- Flag `x-proxy-interleaved-thinking: true` is set in the request, AND
- Upstream credential is `ProviderMiniMax = "minimax"` (`pkg/providers/factory.go:17`; D3 case-insensitive compare per `handler_anthropic.go:297`),

across four paths (race-external, race-internal, ultimate-external, ultimate-internal) for request mapping, non-stream response mapping, and stream SSE response mapping — while preserving byte-identical behavior in all other cases?

**Note (D2):** internal response paths (race-internal + ultimate-internal) extract in `pkg/providers/openai.go` (provider-level), NOT via the translator. The translator module (`pkg/proxy/translator/minimax.go`) handles **external paths only**. On internal paths, openai.go emits one `thinking` StreamEvent per `reasoning_details` entry (consumed by the unchanged `case "thinking"` handlers at `race_executor.go:509` and `handler_internal.go:264`); non-stream extraction happens in `extractNonStreamContent` / response-assembly.

## Context Summary

llm-supervisor-proxy is a Go-based LLM proxy that supervises and routes requests across providers (pluggable via `pkg/providers/`). It supports two routing strategies (race and ultimate), each with its own per-path converter that maps between an internal shape and provider-specific shapes. MiniMax is one provider, exposed as `ProviderMiniMax = "minimax"` at `pkg/providers/factory.go:17` with a MiniMax-default OpenAI-compatible client (`factory.go:45-50`).

Client-facing APIs (per OpenAI convention) expose a single per-message `reasoning_content` string. MiniMax's wire format (per the user-supplied sample, verbatim) carries:

```
message.reasoning_details = [{type:'reasoning.text', id:'reasoning-text-1', format:'MiniMax-response-v1', index:0, text:'...'}]
```

Streaming deltas carry `reasoning_details` chunks. The user's reference doc URL may be unreachable, so the live wire format (in particular whether `text` is incremental or cumulative across deltas) must be re-validated during implementation.

Three structural realities dominate this feature:

1. **Three converter twins already drift**: `race_executor.go:722 convertToProviderRequest`, `pkg/ultimatemodel/handler_internal.go:453 convertRequest`, `pkg/proxy/internal_handler.go:272 convertRequest`. Any new field handling must keep all three in lock-step or drift grows. **D2 — twin C (`internal_handler.go:272`) is dormant and DE-SCOPED for this feature (B3); only A + B are wired.**
2. **Ultimate-external has no credential in scope**: at `pkg/ultimatemodel/handler_external.go:36-44` only `*models.ModelConfig` is available; no `cred` or `*CredentialConfig`. Provider detection cannot be resolved there without a parameter thread. **D3 — derive provider from credential when `modelCfg.CredentialID` is set, else gate=false.**
3. **Typed structs silently drop unknown JSON fields on unmarshal**: `providers.ChatMessage` (`interface.go:43-50`) and `providers.StreamEvent` (`interface.go:118-126`) have no `ReasoningDetails` field. Internal (struct-based) paths will lose `reasoning_details` unless a field is added or map pre/post-processing is inserted around the typed marshal points. **D1 — add `ReasoningSplit *bool` on the request struct (no MarshalJSON, no Extra); keep `ChatMessage.ReasoningDetails` for request-side carry; D2 — openai.go extracts during unmarshal on response side, so `ChatMessage.ReasoningDetails` is not consumed there.**

The feature is gated by a per-request header flag (header-only) — keeping backward-compat: no flag or false ⇒ all four paths byte-identical to today; non-MiniMax upstream ⇒ flag has no effect.

## Architecture

### Current Patterns

- **Layered proxy with pluggable providers** — `handler.go` (entry) → `race_executor.go` or `ultimatemodel/handler_*.go` (strategy) → `providers/` (provider client) → upstream HTTP. Strategy branch is endpoint-driven.
- **Per-path converters** — Each strategy has its own map↔struct mapping for requests and responses. The three converter twins handle the same logical conversion independently (drift trap).
- **Per-path SSE rewriter** — No central SSE rewriter exists; each path rolls its own stream transform (`race_executor.go:326-636`, `handler_internal.go:109-379`, `handler_external.go:182-314`). Race paths use typed `providers.StreamEvent`; ultimate-external parses chunks as **maps** (`handler_external.go:218`) — drift in two idioms.
- **RequestContext propagation** — `rc.ultimateModelEnabled` precedent at `pkg/proxy/handler.go:466-469` shows header read at entry → boolean on RequestContext → downstream passes by reference. Per-flag features travel on the same channel.
- **Anthropic precedent for translator module** — `pkg/proxy/translator/` is invoked as top-level exported functions (no registry/interface); call sites invoke by package import (`request.go:30 TranslateAnthropicToOpenAI`, `response.go:10 TranslateNonStreamResponse`). Mirrors the proposed MiniMax translator pattern.
- **Standard Go stdlib** — `net/http`, table-driven tests, e2e via bash + python mock servers (per project conventions).

### Module Boundaries

```
[client]
   ↓  (header: x-proxy-interleaved-thinking)
[handler.go entry]
   ↓  (RequestContext by ref; rc.interleavedThinking)
   ├── [race_executor.go: race loop] → [providers/<provider>] → [MiniMax API]
   │       ├── convertToProviderRequest (race_executor.go:722)        ← converter twin A
   │       ├── handleStreamingResponse / handleInternalStream (race_executor.go:326-636)
   │       └── handleNonStreamingResponse / handleInternalNonStream (race_executor.go:800-853 / :254-323)
   └── [ultimatemodel/handler_*.go] → [providers/<provider>] → [MiniMax API]
           ├── handler_external.go (NO credential context at :36-44)   ← credential gap (R3)
           ├── ultimatemodel convertRequest (handler_internal.go:453)  ← converter twin B
           ├── pkg/proxy/internal_handler.go convertRequest (:272)     ← converter twin C (dormant)
           ├── handleInternalStream (handler_internal.go:109-379)
           └── streamResponse (handler_external.go:182-314, MAP-based)

[credential store]: pkg/models/config.go (cred.Provider field, ResolveInternalConfig :590-658; :636)
```

Boundaries: handlers depend on the `providers` interface (typed `ChatCompletionRequest`, `ChatCompletionResponse`, `StreamEvent`); ultimatemodel depends on the same. The `providers/` package is the only place upstream HTTP lives. Translators live above this layer; converters live in the strategy layer.

### Architecture Diagram

```mermaid
flowchart TB
    Client[Client<br/>x-proxy-interleaved-thinking:<br/>true|1]
    Handler[handler.go entry<br/>rc.interleavedThinking bool<br/>handler.go:466-469 precedent]
    Handler --> Race{race?}
    Handler --> Ultimate{ultimate?}

    Race --> RaceExecutor[race_executor.go<br/>convertToProviderRequest :722]
    Ultimate --> UltExt[handler_external.go<br/>NO credential :36-44]
    Ultimate --> UltInt[handler_internal.go<br/>convertRequest :453]
    Ultimate -. dormant .-> InternalHandler[pkg/proxy/internal_handler.go<br/>convertRequest :272]

    RaceExecutor --> ProvClient[providers/ HTTP client]
    UltExt --> ProvClient
    UltInt --> ProvClient

    ProvClient --> MiniMax[MiniMax API<br/>reasoning_details array]

    MiniMax --> RaceStream[race stream loop :326-636<br/>typed StreamEvent]
    MiniMax --> UltIntStream[ultim-int stream :109-379<br/>typed StreamEvent]
    MiniMax --> UltExtStream[ult-ext stream :182-314<br/>MAP-based, :218]

    Translator[(proposed)<br/>pkg/proxy/translator/minimax.go<br/>- TranslateRequest<br/>- TranslateNonStreamResponse<br/>- TranslateStreamChunk<br/>- Inject reasoning_split)]

    Race -. invokes .-> Translator
    UltExt -. invokes .-> Translator
    UltInt -. invokes .-> Translator
    InternalHandler -. invokes .-> Translator
    RaceStream -. invokes .-> Translator
    UltIntStream -. invokes .-> Translator
    UltExtStream -. invokes .-> Translator
```

The translator is the single source of truth for reasoning_details ↔ reasoning_content translation. Each of the seven call-sites (request×4 + non-stream×4 + stream×4, with some overlap on the path) becomes a thin wrapper that delegates.

## Integration Points

| # | Integration | Type | Contract | Auth | Failure Mode | File:Line |
|---|-------------|------|----------|------|--------------|-----------|
| 1 | MiniMax API | sync (req) + async (stream) | HTTPS JSON; SSE `data: {json}` | Bearer / API key from credential | HTTP error → proxy 5xx; SSE error frame format unknown (Q4) | `factory.go:45-50` ; `race_executor.go:148-155` ; `handler_external.go:36-149` ; `handler_internal.go:109-379` |
| 2 | Credential store | sync read | JSON file via `pkg/models/config.go` | admin-controlled | Missing credential → 4xx from `GetCredential`; callers handle | `config.go:735-744` ; `config.go:590-658` (provider :636) |
| 3 | Anthropic translator module | sync, top-level functions | `[]byte ↔ []byte` (req/resp); stream helpers | n/a | Returns error → caller routes to 4xx/5xx | `pkg/proxy/translator/request.go:30`, `response.go:10`, `stream.go`, `translator_test.go` |
| 4 | Client SSE consumer | async stream | `data: {json}` lines per OpenAI SSE | n/a (client-config) | Mid-stream failure → SSE `data: [DONE]` may not be sent (Q4) | `handler.go:1262-1312` (final body write) ; `handler_internal.go:71` (typed marshal) ; `handler_external.go:149` (verbatim bytes) |

### Integration Details

**Integration 1: MiniMax API**
- **Protocol:** HTTPS REST (non-stream) + SSE (stream); OpenAI-compatible shape per `factory.go:45-50`
- **Data format:** JSON request/response; SSE `data: {json}` line-per-delta; non-stream `message.reasoning_details = [{type:"reasoning.text", id, format, index, text}]`
- **Authentication:** Bearer token from credential provider field (`config.go:636` → `cred.Provider` → `ResolveInternalConfig`)
- **Error handling:** HTTP non-2xx → proxy error wrapper; SSE error frame format unknown (Q4)
- **Observability:** logs only (research did not find metrics/traces wired for LLM providers)
- **Known issues:** doc URL may be unreachable; live wire format for stream `reasoning_details` (cumulative vs incremental) unverified (R9, Q1)

**Integration 3: Anthropic translator module (precedent for MiniMax translator)**
- **Protocol:** in-process function call
- **Data format:** `[]byte` for request (`TranslateAnthropicToOpenAI` at `request.go:30`); `[]byte → []byte` for response (`TranslateNonStreamResponse` at `response.go:10`); typed stream helpers in `stream.go`
- **Authentication:** n/a
- **Error handling:** returns error from converter; caller routes
- **Observability:** caller logs (no translator-level metrics)
- **Known issues:** Anthropic-only today; no registry/interface (deliberate, per dispatch)

## Trade-offs

### Alternatives Considered

1. **Option A — Dedicated translator**: Add `pkg/proxy/translator/minimax.go` (plus a stream helper) exposing pure functions — `TranslateRequest`, `TranslateNonStreamResponse`, `TranslateStreamChunk`, `InjectReasoningSplit`. All four paths call the translator; converters stay slim wrappers. Mirrors anthropic precedent (`pkg/proxy/translator/request.go:30`, `response.go:10`).
2. **Option B — Inline edits across the three converter twins**: Add the reasoning_details handling directly inside `race_executor.go:722 convertToProviderRequest`, `ultimatemodel/handler_internal.go:453 convertRequest`, `pkg/proxy/internal_handler.go:272 convertRequest`, and the four stream loops. No new module.

### Comparison

| Criterion | Option A (translator) | Option B (inline) | Winner |
|-----------|----------------------|--------------------|--------|
| Performance | One function indirection per chunk; can amortize to <1ms under typical SSE workloads | Zero indirection; effectively zero overhead | Option B (negligible) |
| Complexity | One new package + ~7 call-site wirings; reuses anthropic skeleton | Touches 7 sites; structural cleanup deferred | Option A |
| Maintainability | Single source of truth; bug fix in one place; table-driven unit tests mirror anthropic precedent | DRIFT TRAP — 3 converters already drift, plus 4 stream loops in 2 idioms; adding reasoning_details grows drift surface | **Option A (decisive)** |
| Testability | Pure functions, hermetic; per-call assertion against reasoning_details ↔ reasoning_content; one test file | Per-site assertions duplicated 3–7x; struct-vs-map requires separate test approach per path | Option A (decisive) |
| Team skills | Mirrors existing anthropic translator; small ramp-up | "Just edit the converters" — easier to start | Option B (slightly) |
| Time-to-implement | Translator + 7 wirings ≈ 1.4× initial | 7 sites edited ≈ 1× initial | Option B |
| Drift resistance | Single source eliminates drift | Drift is the dominant risk per cited evidence (race_executor.go:722 vs handler_internal.go:453 vs internal_handler.go:272) | **Option A (decisive)** |
| Backward-compat assertion surface | Single translator has a single no-op `false` branch; one place to gate | Each converter site must independently remember the no-op guard | Option A |

### Recommendation

**Pick: Option A — Dedicated translator (`pkg/proxy/translator/minimax.go` + stream helper).**

**Reasoning:** The drift trap is dominant — three converter twins that already drift (cited at `race_executor.go:722`, `handler_internal.go:453`, `internal_handler.go:272`) plus four stream loops in two idioms (typed vs map) mean any inline edit must be replicated 7 times and kept in sync forever. The translator centralizes the conversion; each call-site becomes a thin wrapper that delegates. Testability follows the proven anthropic table-driven test pattern (`translator_test.go`). Field injection (`reasoning_split`), request map mutation, and response transforms all collapse to a single source of truth. Performance overhead is negligible at SSE chunk rates.

**Sub-decision (applies whichever option is chosen) — struct-vs-map interception:**

- For struct-based paths (race-internal, ultimate-internal, race-external in some shapes): add `ReasoningDetails []ReasoningDetail` to `providers.ChatMessage` (`interface.go:43-50`) for **request-side carry only**. **Do NOT add `ReasoningDetails` to `providers.StreamEvent`** (`interface.go:118-126`) — strictly dead per D2 (openai.go's stream parser emits `thinking` StreamEvents directly; the field would be unused). On the response side, openai.go extracts `reasoning_details` during unmarshal — `ChatMessage.ReasoningDetails` is NOT consumed there. This preserves the wire field through `json.NewEncoder/Decoder` for request-side carry. Translator mutates the field at request time (per-message `reasoning_details` → `ChatMessage.ReasoningDetails`); response-side extraction is handled in `pkg/providers/openai.go`, not the translator.
- For map-based path (ultimate-external stream at `handler_external.go:218`): intercept the `map[string]any` after `json.Unmarshal`, mutate/delete keys before re-serialize. Translator takes/returns `map[string]any`.
- For ultimate-external non-stream (`handler_external.go:114-149`): write happens via `w.Write(bodyBytes)` — translator re-serializes the mutated map and feeds bytes back in.

The shallow `bodyCopy` at `handler_external.go:55-58` does a top-level map copy via key/value iteration, so top-level additions like `reasoning_split: true` propagate. Nested mutations remain shallow; the translator must avoid relying on nested mutation through the same map reference (Q5).

**Assumptions:**
- The drift trap is real and growing. Adding reasoning_details inline makes future field changes (different providers, different shapes) harder.
- Anthropic translator's direct-invocation pattern (`request.go:30`, `response.go:10`) is a sufficient scaling precedent — no registry/interface needed for one provider.
- Performance impact of an extra function call per chunk is negligible (typical SSE rates are tens-to-hundreds of chunks per request; per-chunk overhead <1ms).

**Reversibility:** Medium. The 7 wrappers call into the translator; if the translator is later inlined or split per-provider, the wrappers move with no signature change. The translator module can stay as a utility even if only some paths use it.

## Scalability

### Growth Assumptions

- Provider adoption: MiniMax is one of N providers (`factory.go:17`); future array-based reasoning providers are plausible (Anthropic `thinking` blocks already modeled as `translator/types.go:ContentBlock`). Translator pattern scales to additional providers without per-strategy changes.
- Reasoning traffic: stream reasoning chunks per request can be tens-to-hundreds of small deltas for long thinking. Translator per-chunk function call is O(deltas).
- Backward-compat surface: 4 paths × 2 sides (req/resp) × 2 modes (stream/non-stream) = 16 sensitivity points. Every change must keep no-flag paths byte-identical.
- MiniMax usage growth is expected if the feature ships; assume 10× over 12 months for analysis purposes (no current data).

### Current Bottlenecks

| # | Bottleneck | Threshold | File:Line | Impact |
|---|------------|-----------|-----------|--------|
| 1 | 3-converter drift | Each new field added to `providers.ChatMessage` (or similar) requires 3 edits + silent failure on the 3rd | `race_executor.go:722` ; `handler_internal.go:382-462 (copy :453)` ; `internal_handler.go:272` | New reasoning-style field propagates incorrectly or silently drops |
| 2 | Map-based stream parsing on ultimate-external | Map allocation per chunk; `toolcall.ToolCallBuffer` already present :199-278 | `handler_external.go:182-314` (Unmarshal :218) | Reasoning translation adds another map-mutation stage — must not race tool-call buffer (R5) |
| 3 | SSE buffering & flush | `bytes.Buffer` per path with explicit flush | race :~527 ; ultim-int :163/:356 ; ultim-ext :310 | Translator must emit per-chunk correctly, not batch — client expects stream semantics (R6) |
| 4 | Ultimate-external credential scope | No `cred` in scope at handler_external.go:36-44 | `handler_external.go:36-44` (function signature) | Cannot branch on `provider == "minimax"` without credential thread or path scoping (R3) |

### Scaling Characteristics

- **Vertical vs horizontal:** Translator is stateless across streams; supports HPA trivially (same as today).
- **Stateless vs stateful:** Stream-chunk translator is stateless by default (safe assumption: incremental deltas). If MiniMax emits cumulative `text`, add per-`(streamID, choice_index)` state (see Streaming Reconstruction).
- **Sync vs async:** Translation is sync per chunk; no new async channels needed.
- **Scaling cliffs:** None directly added by this feature; translator does not introduce a new database or queue. Chunk-emission rate (one per SSE delta) is unchanged.

## Technical Debt

### Items Affecting This Analysis

| # | Debt Item | Impact on Recommendation | Severity | File:Line |
|---|-----------|--------------------------|----------|-----------|
| 1 | Three converter twins already drift | Determines translator vs inline decision (decisive for Option A) | High | `race_executor.go:722` ; `handler_internal.go:382-462 (copy :453)` ; `internal_handler.go:272` |
| 2 | Ultimate-external has no credential in scope | Determines whether external path can branch on `provider=="minimax"` at request time | High | `handler_external.go:36-44` (function signature) |
| 3 | Typed structs DROP unknown fields on unmarshal | Without adding `ReasoningDetails` to `ChatMessage`, internal request paths silently lose per-message `reasoning_details` data on `map→*ChatCompletionRequest` hydration | High (request-side) | `interface.go:43-50` (`ChatMessage` — kept for request-side carry per D1) ; **`interface.go:118-126` (`StreamEvent` — NOT modified; strictly dead per D2, openai.go emits `thinking` events directly)** ; inferred from `race_executor.go:509-521` (`case "thinking"` consumes thinking events emitted by openai.go's stream parser) |
| 4 | Anthropic translator is provider-monolithic in concept | Either scope (separate sibling) or extend translator module | Low | `pkg/proxy/translator/` (entire dir) |
| 5 | `bodyCopy` at ultimate-external is shallow key/value copy | Top-level mutations (e.g., `reasoning_split: true`) propagate; nested mutations do not | Medium | `handler_external.go:55-58` (map copy loop), model override :61-63 |
| 6 | `toolcall.ToolCallBuffer` delays chunk emission on ultim-ext | Translator must run as a peer / before / after the buffer consistent with phase logic | Medium | `handler_external.go:199-278` |

### Items NOT Affecting This Analysis

- E2E mock-server conventions — orthogonal; reuse `test/test_mock_ultimate_model.sh`, `test/test_mock_openai_internal_buffered.sh`, `test/e2e_ultimate_internal_reasoning/` patterns.
- Credential storage format — orthogonal (read-only consumer).
- Logging format — orthogonal (caller-side, no change).

### Recommended Paydown (in priority order)

1. **Add `ReasoningDetails` field to `providers.ChatMessage` + `providers.StreamEvent`** (`interface.go:43-50`, `:118-126`). Required for internal (struct) paths to preserve the field through marshal/unmarshal. Inline with feature landing.
2. **Resolve ultimate-external credential strategy** (Risk R3): thread `cred` or resolved `*CredentialConfig` into `handler_external.go` signature, or scope the feature to "response-side only on ultim-ext" and document the limitation. Decide before implementation lands on ultim-ext.
3. **Consolidate or mandate checklist for the three converter twins**: translator approach is the consolidation; the fallback is a header comment in each converter listing the sister file:line references and a unit-test assertion that the three converters stay in sync on selected fields.
4. **Add `reasoning_details` field to the e2e MiniMax mock server** — required for end-to-end coverage; falls into "verify with a live call" once mock covers the field.

## Risk Register

| # | Risk | Invariant / Recommendation | Mitigation | Anchor |
|---|------|--------------------------|------------|--------|
| R1 | Backward-compat regression — adding field handling might leak MiniMax details to client when flag is off or upstream is non-MiniMax | NO FLAG (or false) ⇒ all 4 paths MUST be byte-identical to today; non-MiniMax upstream ⇒ flag has NO effect | Gate translator on `rc.interleavedThinking && providerIsMiniMax`; unit tests asserting byte-identical for both negative cases (3 × 4 = 12 negative-case assertions) | `handler.go:1262-1312` ; `race_executor.go:800-853` ; `handler_internal.go:56-107` ; `handler_external.go:114-149` |
| R2 | Three-converter drift trap | Converter twins at `race_executor.go:722`, `handler_internal.go:453`, `internal_handler.go:272` must stay in sync | (a) Translator consolidates → drift eliminated; (b) fallback: header comment in each converter listing sister file:line references + a unit-test assert that converters are synchronized on selected fields | `race_executor.go:722` ; `handler_internal.go:382-462` ; `internal_handler.go:272` |
| R3 | Ultimate-external credential gap — no `cred` in scope | Cannot branch on `provider=="minimax"` at request time at `handler_external.go:36-44` | **Recommend:** thread the credential (or resolved `*CredentialConfig`) into `handler_external.go`'s function signature, mirroring how ultimate-internal reaches credential via `config.ModelsConfig.GetCredential(credentialID)`. Alternative: scope the feature on ultim-ext to "response-side translation only" (request side is no-op) and document. | `handler_external.go:36-44` ; precedent of WARN if missing tool_call_id: `handler_internal.go:457-462` |
| R4 | Struct-drop of unknown fields on internal paths — typed structs silently drop `reasoning_details` on json.Unmarshal | Translator cannot see data the struct doesn't expose | Add optional `ReasoningDetails []ReasoningDetail` (or `[]any`) field to `providers.ChatMessage` (`interface.go:43-50`) and `providers.StreamEvent` (`interface.go:118-126`). Zero-value leaves behavior unchanged (back-compat). | `interface.go:43-50` ; `interface.go:118-126` |
| R5 | Tool-call / reasoning interleaving order — on stream paths, deltas for both interleave | Reasoning_content emission must preserve upstream ordering; tool-call index normalization (e.g., `race_executor.go:439-457`) must NOT be applied to reasoning deltas | Translator is event-by-event; do not reorder across events; per-event emission in the order received. Final verification per upstream chunk. | `race_executor.go:439-457` ; `race_executor.go:509-521` |
| R6 | SSE buffering / flush interactions — chunk emitters have differing flushing patterns | Stream consumers (clients) rely on per-chunk timing | Emit one client chunk per upstream chunk; do not coalesce across chunks; respect `bytes.Buffer` flush points (race :~527, ultim-int :356, ultim-ext :310). Translator hooks BEFORE each flush. | `race_executor.go:~527` ; `handler_internal.go:356` ; `handler_external.go:310` |
| R7 | `reasoning_split` field placement — user wrote `extra_body={"reasoning_split": true}`; Python-SDK deep-merges to top-level body field; doc URL may be unreachable | Spec is unverified | Wire shape assumption: top-level field `{..., "reasoning_split": true, ...}`. Mark "verify during implementation with a live MiniMax call". Fallback if top-level fails: nested key under a vendor-specific name. | inferred from user note |
| R8 | Header value normalization — precedent at `handler.go:465` uses `== "true" \|\| == "1"` (case-sensitive, lowercase only) | New flag should not be more permissive than the precedent unless deliberately so | Recommend: match precedent — accept `"true"` and `"1"` only (case-sensitive). Reject `"True"`, `"TRUE"`, `"yes"` as false. Document. (Note: user's spec example used `true|True` — the precedent doesn't support `True`; verify or expand at impl.) | `handler.go:465-469` ; precedent `X-Force-Ultimate-Model` |
| R9 | Streaming `reasoning_details` semantics — incremental vs cumulative `text` per chunk | Assumption: emit each chunk's `text` as a separate client `reasoning_content` delta | If MiniMax streams cumulatively, translator must track last-issued text and emit only the suffix. Detect by inspecting first/last chunk at runtime; safe default = re-emit `entry.text[len(last):last+len(text)]` only when `text` is a strict superset of `last`. | `race_executor.go:509-521` (precedent delta transform) |
| R10 | `tool_call_id` repair runs on response path (race_executor.go:800-853), reasoning_details is mostly request-side | No conflict today (different paths in the response chain) | Document the boundary: request-side reasoning_details translates BEFORE any tool_call_id repair; response-side reasoning_details → reasoning_content translation runs BEFORE tool-call repair (`race_executor.go:800-853`) so tool-call indices normalize last. | `race_executor.go:800-853` ; `handler_internal.go:449-462` |
| R11 | `id`, `format`, `index`, `type` echoing back to client | Client API exposes only `reasoning_content` (string) | Translator strips all reasoning_details metadata on response side; client sees a single string per chunk. | inferred from `interface.go:43-50` |

### Backward-Compat Invariants (explicit)

1. No `x-proxy-interleaved-thinking` header OR value other than truthy set ⇒ path through unchanged. No translator invocation. Precedent at `handler.go:465-469`.
2. Header truthy AND `strings.ToLower(cred.Provider) != factory.ProviderMiniMax` ⇒ translator not invoked; pass-through (R1).
3. Header truthy AND `strings.ToLower(cred.Provider) == factory.ProviderMiniMax` ⇒ translator invoked on all four paths.
4. Streaming `case "thinking"` precedent at `race_executor.go:509-521` and `handler_internal.go:264-274` is UNCHANGED for non-MiniMax cases.
5. New `ReasoningDetails` struct field (if added per R4) defaults to nil/empty; no JSON serialization impact on existing wire shapes.
6. `bodyCopy` shallow top-level add (`reasoning_split: true` etc.) on ultimate-external does not affect non-MiniMax paths when translator is gated off.

### Flag Placement — Final Decision

**Pick: header-only** (`x-proxy-interleaved-thinking: true` or `1`), stored as boolean on `RequestContext.interleavedThinking`.

**Reasoning:**

- **Precedent alignment** — `X-Force-Ultimate-Model` follows the exact pattern at `handler.go:465-469`: header read at handler.go entry → boolean stored on `rc.ultimateModelEnabled` → downstream passes by reference. Establishing a new pattern (body field) without a precedent would invite inconsistency.
- **Reachability** — `RequestContext` is passed by reference down all four paths (cited). The flag reaches `race_executor.go`, `ultimatemodel/handler_external.go`, `ultimatemodel/handler_internal.go`, and `pkg/proxy/internal_handler.go` without re-parsing. Per-path `cred.Provider` is then resolved from each path's own credential scope.
- **Body fallback overkill** — A body fallback would require parsing `messages`/request body in `handler.go` and threading the flag through race_executor + ultimate paths; more invasive than reading a header.
- **Authorization** — No admin gating proposed for the new flag (Q9). If abuse vectors surface (e.g., upstream-cost amplification), consider a per-credential allowlist as a follow-up.

**Header value normalization:** match precedent at `handler.go:465` — accept `"true"` and `"1"` only (case-sensitive). Reject `"True"`, `"TRUE"`, `"yes"`. (The user's dispatch example used `true|True` which is wider than the precedent; if the implementation deliberately widens it, document and add unit tests for the new accepted values.)

### Streaming Reconstruction — Final Algorithm

**State:** Per-stream (one state object per active stream). Stateless across streams.

**Per-chunk algorithm (applied after parsing each upstream SSE chunk into the chosen shape — typed struct or map):**

1. Locate `reasoning_details` in the `delta` (or `message` for non-stream) of the chunk.
2. If absent → no translation; emit as-is (or already-typed, with the field stripped if struct).
3. If present:
   a. For each entry in the `reasoning_details` array, in array order:
      - If `entry.type == "reasoning.text"`:
        - **Safe default emission (assumes incremental):** emit a separate client chunk with `delta.reasoning_content = entry.text` and no `reasoning_details` field. Multiple entries in one upstream chunk → multiple client chunks, in array order.
        - If `entry.text == ""` (or all-whitespace) → skip this entry entirely (do not emit an empty `reasoning_content` delta).
        - If verified cumulative at runtime: state must track `lastIssuedText` per `(streamID, choice_index)`; emit `entry.text[len(lastIssuedText):]` only; update `lastIssuedText = entry.text`.
      - Else (`entry.type` unknown) → strip (no client-visible equivalent). Log debug at low sampling rate to capture unknown types for future expansion.
   b. Strip the upstream `reasoning_details` field from the chunk before write so it never leaks to the client. Leave unrelated `extra_body`-style nested keys untouched.

**Stateless vs stateful:** Default to stateless (safe assumption: incremental). If `reasoning_details` is cumulative, add a tiny state object keyed by `(streamID, choice_index)` carrying `lastIssuedText`. State lifetime matches the stream — discard on stream end.

**Ordering:** Translate then emit per entry in array order. Do NOT reorder against `index`. If duplicates on `index` are observed, log and use first-wins.

**Empty/final state:** Emit any pending suffix on stream close. If upstream ends mid-text, the last entry's `text` is the final client chunk. State discards on stream end.

**Tool-call interleave:** Reasoning deltas and tool-call deltas are independent streams within the SSE event loop. Translator only touches the `reasoning_details` field; it does not read or modify `tool_calls`, `index`, or other fields. Per-event ordering is preserved by re-emitting in chunk order.

### `reasoning_split` Placement — Final Decision

**Pick: top-level body field** `{..., "reasoning_split": true, ...}`.

The user's Python-SDK `extra_body={"reasoning_split": true}` is SDK-side deep-merge behavior; on the wire it is a top-level JSON key, consistent with the OpenAI-compatible client shape that MiniMax uses (`factory.go:45-50`).

**Verification:** Marked "verify during implementation with a live MiniMax call". The reference doc URL may be unreachable; if top-level fails, the fallback is a nested key (e.g., a vendor-specific namespace); this fallback should not be needed based on the user's written expectation.

**Where to inject:** Request map mutation before serialization. Both map-based and struct-based paths must include it — easiest done in the translator (Option A) so all four paths share one injection point.

## Open Questions

| # | Question | Anchor (file:line) to verify at implementation |
|---|----------|----------------------------------------------|
| Q1 | MiniMax wire format: is `reasoning_details.text` emitted as cumulative or incremental on stream? | Verify with a live MiniMax call + observe first/last chunk shape; do not rely on the unreachable doc URL |
| Q2 | Does MiniMax emit `reasoning_details` alongside the final `message.reasoning_content` for non-stream? | If yes, translator must strip the legacy field; if no, nothing to strip |
| Q3 | Header value normalization: precedent at `handler.go:465` accepts only `"true"` and `"1"` (case-sensitive lowercase). Should the new flag match precedent exactly, or accept `"True"` per user's spec? | `handler.go:465-469` — confirm value-list behavior, then mirror or document widening |
| Q4 | SSE error frame format on MiniMax stream (does upstream send `data: {...error...}` before close?) | Verify by inducing an error against the live MiniMax API |
| Q5 | `bodyCopy` shallow semantics at ultim-ext — top-level mutations (`reasoning_split`) propagate via the `for k,v := range` loop (`handler_external.go:55-58`); nested mutations don't. Translator must avoid relying on nested mutation through same map reference | `handler_external.go:55-58` ; verify with a unit test; consider explicit `map[string]any` rebuild if mutations beyond top-level are needed |
| Q6 | `cred.Provider` for MiniMax-typed credentials — is the wire provider string `"minimax"` exactly? | `factory.go:17` (definition) + `config.go:636` (read site) — verify by reading test fixture credentials in `test/e2e_ultimate_internal_reasoning/` or similar |
| Q7 | Should the existing `case "thinking"` handlers at `race_executor.go:509-521` and `handler_internal.go:264-274` be REPLACED by the translator, or kept as a fallback for other providers? | Decide: translator handles MiniMax `reasoning_details`; `case "thinking"` handles generic providers that emit `reasoning_content` directly. Both can coexist. |
| Q8 | When the body field `reasoning_split` is set but the request has no `messages[i].reasoning_content` entries (cold-start conversation), does MiniMax still produce `reasoning_details`? | Live call. Could inform whether the request-side translator is a no-op for empty inputs |
| Q9 | Admin gating of `x-proxy-interleaved-thinking`: should this be admin-controlled per credential, or open per client? | Out of scope for this analysis; document and decide at impl; precedent is admin-gated for `X-Force-Ultimate-Model` (handler.go:466) |
| Q10 | Does the dormant `pkg/proxy/internal_handler.go:272 convertRequest` ever run? | grep callers of `internal_handler.go`; if dormant, recommended to remove but out of scope for this feature |
| Q11 | Request-side `reasoning_details` ordering: when multiple `messages` carry `reasoning_content`, is the array index order preserved end-to-end? | Live call; if not, the translator must serialize per-message explicitly |

## Final Recommendations (delivered to planner)

1. **Architecture:** Dedicated translator `pkg/proxy/translator/minimax.go` (+ stream helper) — single source of truth, mirrors anthropic precedent (`request.go:30`, `response.go:10`). Drift trap R2 drives this decision decisively.
2. **Flag placement:** Header-only `x-proxy-interleaved-thinking: true|1`, gated on `RequestContext.interleavedThinking` bool, resolved at handler.go entry (precedent `handler.go:465-469`) and passed by reference down all four paths.
3. **Provider gate:** Per-path `cred.Provider == "minimax"` check — race uses caller-scope credential; ultimate-internal reaches via `config.ModelsConfig.GetCredential`; **ultimate-external needs credential threading or path scoping (R3 mitigation)** before request-side translation lands on ultim-ext.
4. **Struct surface (R4):** Add optional `ReasoningDetails []ReasoningDetail` (or `[]any`) field to `providers.ChatMessage` (`interface.go:43-50`) and `providers.StreamEvent` (`interface.go:118-126`) so struct paths preserve the field through marshal/unmarshal.
5. **Streaming reconstruction:** Per-entry emission in array order; `text → reasoning_content` string per client chunk; strip `reasoning_details` field from chunk before write. Stateless default; add per-`(streamID, choice_index)` state only if cumulative semantics are observed at runtime.
6. **`reasoning_split`:** Top-level body field; verify live (Q1, R7).
7. **Backward-compat invariants (R1):** explicit assertions in unit tests — no-flag ⇒ byte-identical on all 4 paths; non-MiniMax ⇒ byte-identical; the negative case is the most-tested case.
8. **Header value normalization (R8):** match precedent at `handler.go:465` — accept `"true"` and `"1"` only (case-sensitive lowercase). Reject wider values like `"True"`, `"yes"` unless deliberately widened with documentation.

## References

- `pkg/providers/interface.go:43-50` (ChatMessage.ReasoningContent), `:103-108` (Choice.Message/Delta *ChatMessage), `:118-126` (StreamEvent.ReasoningContent)
- `pkg/providers/factory.go:17` (ProviderMiniMax = "minimax"), `:45-50` (MiniMax factory case, default baseURL https://api.minimax.io/v1, OpenAI-compatible client)
- `pkg/models/config.go:735-744` (GetCredential), `:590-658` (ResolveInternalConfig, provider from credential :636)
- `pkg/proxy/handler.go:465-469` (X-Force-Ultimate-Model parse precedent), `:1262-1312` (final body write)
- `pkg/proxy/handler_functions.go:55-58` (request-side stream field read)
- `pkg/proxy/internal_handler.go:272` convertRequest (dormant converter twin C)
- `pkg/proxy/race_executor.go:113-115` (req.stream), `:148-155` (map round-trip), `:240` (contentType check), `:254-323` (handleInternalNonStream ; `json.Marshal` at `:275` ; `buffer.Add` at `:318`), `:326-636` (handleStreamingResponse / handleInternalStream), `:364-636` (event loop), `:439-457` (tool-call index normalization), `:509-521` (`case "thinking"`, emits `delta.reasoning_content` at `:521`), `:718-721` (tool_call_id copy precedent), `:722-725` (convertToProviderRequest, converter twin A), `:800-853` (handleNonStreamingResponse ; `buffer.Add` at `:848`)
- `pkg/ultimatemodel/handler_external.go:36-44` (function signature — no credential in scope, model override :61-63), `:55-58` (shallow bodyCopy), `:109` (isStream param), `:114-149` (`io.ReadAll` at `:115`, verbatim `w.Write` at `:149`, last transform point), `:182-314` (streamResponse ; `json.Unmarshal` at `:218` ; `toolcall.ToolCallBuffer` at `:199-278` ; flush at `:310`)
- `pkg/ultimatemodel/handler_internal.go:27/50` (isStream param), `:56-107` (handleNonStream ; `json.NewEncoder(w).Encode(resp)` at `:71`), `:109-379` (handleInternalStream ; `case "thinking"` at `:264-274` ; `bytes.Buffer` at `:163`, flush at `:356`), `:382-462` (convertRequest — converter twin B, `tool_call_id` copy at `:449-452`, WARN missing at `:457-462`, `reasoning_content` copy at `:453-456`)
- `pkg/proxy/translator/` — `request.go:30` (TranslateAnthropicToOpenAI), `response.go:10` (TranslateNonStreamResponse), `stream.go` (SSE helpers), `types.go` (ContentBlock, UsageInfo), `tools.go`, `errors.go`, `translator_test.go` (table-driven tests)
- e2e conventions: `test/test_mock_ultimate_model.sh` ; `test/test_mock_openai_internal_buffered.sh` ; `test/e2e_ultimate_internal_reasoning/`
- MiniMax reference doc URL (may be unreachable): https://platform.minimax.io/docs/givers/guides/text-m3-function-call — user-provided sample is the ground truth where the URL is silent.

---

## Amendment Log (Round 2)

| ID | Change | Anchor | Review item |
|----|--------|--------|-------------|
| AM-01 | Status updated to "Round 2 — binding decisions incorporated". Header note added pointing readers to `plan-overview.md`, phase plans, and `architecture-recommendation.md` §11 for full D1-D4 details. | D1, D2, D3, D4 | all |
| AM-02 | Question section: added D2 scope-reduction note — internal response paths extract in `pkg/providers/openai.go` (provider-level), NOT via the translator. Translator handles external paths only. | D2 | B2 |
| AM-03 | Structural reality #1: D2 — twin C (`internal_handler.go:272`) is dormant and DE-SCOPED (B3); only A + B are wired. | D2, B3 | B3 |
| AM-04 | Structural reality #2: D3 — derive provider from credential when `modelCfg.CredentialID` is set, else gate=false. | D3 | B5 |
| AM-05 | Structural reality #3: D1 + D2 — `ReasoningSplit *bool` on request struct; `ChatMessage.ReasoningDetails` for request-side carry only; openai.go extracts on response side. | D1, D2 | B1, B2 |
| AM-05b (R2-residual D3) | Purge round-1 struct framing. structural-reality #3 (line 37) — clarified: `StreamEvent.ReasoningDetails` is **NOT** added (strictly dead per D2); on response side `ChatMessage.ReasoningDetails` is NOT consumed (openai.go extracts during unmarshal). Tech-debt item 3 (line 213) — updated: only `ChatMessage` kept (request-side carry); `StreamEvent` removed from the framing. Risk R4 (line 238) — updated: add typed `ReasoningSplit *bool` on request struct + `ChatMessage.ReasoningDetails` (request-side carry only); `StreamEvent` MUST NOT receive `ReasoningDetails` (strictly dead). | R2-residual D3 | D2 dead surface |

*This technical-analysis document is the structural reference; per-task amendments are tracked in `plan-overview.md` and the per-phase `phaseN-plan.md` Amendment Logs.*
B5 |
| AM-05 | Structural reality #3: D1 + D2 — `ReasoningSplit *bool` on request struct; `ChatMessage.ReasoningDetails` for request-side carry only; openai.go extracts on response side. | D1, D2 | B1, B2 |
| AM-05b (R2-residual D3) | Purge round-1 struct framing. structural-reality #3 (line 37) — clarified: `StreamEvent.ReasoningDetails` is **NOT** added (strictly dead per D2); on response side `ChatMessage.ReasoningDetails` is NOT consumed (openai.go extracts during unmarshal). Tech-debt item 3 (line 213) — updated: only `ChatMessage` kept (request-side carry); `StreamEvent` removed from the framing. Risk R4 (line 238) — updated: add typed `ReasoningSplit *bool` on request struct + `ChatMessage.ReasoningDetails` (request-side carry only); `StreamEvent` MUST NOT receive `ReasoningDetails` (strictly dead). | R2-residual D3 | D2 dead surface |

*This technical-analysis document is the structural reference; per-task amendments are tracked in `plan-overview.md` and the per-phase `phaseN-plan.md` Amendment Logs.*
r-phase `phaseN-plan.md` Amendment Logs.*
