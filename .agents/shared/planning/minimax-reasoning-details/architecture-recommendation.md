# Architecture Recommendation: MiniMax reasoning_details Translator — Plan Enrichment

Date: 2026-08-18T05:43 UTC (Tuesday)
Author: architect (controller) — aggregation of 3 dispatched design analyses
Instance IDs: 03b3cc1f-46f7-4f7c-b735-c27d14b97598 (structural-design), 33725e66-c329-4afd-923c-2837d3b1d862 (data-flow-design), 58f9dfa0-cec1-4117-bea2-8fe1ed586ffe (resilience-design)
Status: Complete — enriches, does not rewrite, the 3-phase plan
Inputs: `plan-overview.md`, `technical-analysis.md`, `phase1-plan.md`, `phase2-plan.md`, `phase3-plan.md` + direct code citation by workers

---

## 0. Summary Verdict

**The committed architecture holds: dedicated translator module, flag+provider gate, per-path thin call-sites.** All three independent analyses converge on that. But the aggregation surfaced **four plan errors and one 🔴 hidden coupling** that will produce real bugs or wasted motion if the phase plans are implemented as written:

1. 🔴 **Header leak**: `executeExternal` forwards all client headers upstream (`handler_external.go:79-87`) — `x-proxy-interleaved-thinking` would leak to MiniMax on ultimate-external. Not caught by any planned test (they assert bodies, not headers).
2. **Wrong precedent characterization**: the plan's `[]byte`-canonical request API misreads the Anthropic precedent (which is typed-in/map-out on requests); it forces 3 redundant parse/marshal round-trips and multiplies byte-identical-negative-test fragility.
3. **Dead struct surface**: `StreamEvent.ReasoningDetails` is unused by design — the stream translator operates on `chunk []byte`; the field is dead surface area.
4. **False propagation premise (P1-6)**: `*RequestContext` does NOT reach ultimate paths (`pkg/ultimatemodel/Handler.Execute` signature at `pkg/ultimatemodel/handler.go:225-236` has no `rc`) — the flag needs a second propagation mechanism.
5. **Concatenation hazard (P2-1)**: "preserving any existing value" silently duplicates reasoning if MiniMax emits both `reasoning_content` and `reasoning_details` with the same text (the Q2 "yes" mode the mock harness deliberately tests).

Each is a targeted amendment, not a redesign. Sections 2–7 give the concrete deltas per focus area; Section 8 maps them onto P1/P2/P3 tasks.

---

## 1. Approach Comparison — how the plan's commitments held up

| Plan commitment | Verdict | Dominant axis | Evidence |
|---|---|---|---|
| Dedicated translator module, Anthropic-mirroring, direct invocation | ✅ **Confirmed** | Maintainability | Module placement + top-level-function style confirmed at `adapter_anthropic.go:98,143`, `handler_anthropic.go:502,531`; drift trap is the dominant risk (structural worker) |
| `TranslateRequest(body []byte)` byte-canonical API | ⚠️ **Needs amendment** | Complexity / Risk | Real Anthropic surface is typed-in/map-out (`request.go:14`); 3 of 4 request sites already hold `map[string]any` — byte-canonical forces 3 redundant round-trips (structural) |
| Stateless stream default + opt-in `StreamState` | ✅ **Confirmed** (with ownership change) | Risk | Failure-mode asymmetry: incremental-on-cumulative = visible quadratic growth (recoverable); cumulative-on-incremental = silent data loss (worst). Auto-detect rejected as fragile (data-flow) |
| `StreamState` as caller-passed param + `Reset()` discipline | ⚠️ **Needs amendment** | Maintainability | Per-stream instance next to `toolcall.NewToolCallBuffer` makes lifetime obvious; `Reset()` is a footgun under race retries (structural) |
| `rc.interleavedThinking` flag home | ✅ for race paths / ⚠️ ultimate paths need param | Complexity | `rc` never crosses into `ultimatemodel` package; parse once in `Execute`, pass down (structural) |
| P1-7 signature widening of `executeExternal` | ✅ **Confirmed** (narrowed) | Complexity | 1 production caller + ~10 test callers, all in one file — small contained blast radius; prefer `provider string` over `*CredentialConfig` (structural) |
| `ReasoningDetails` on `ChatMessage` + `StreamEvent` | ⚠️ **Split verdict** | Maintainability | `ChatMessage`: safe, keep. `StreamEvent`: dead surface, drop or `json:"-"` (data-flow) |
| Thin translator call-sites at 3 converters | ✅ **Confirmed** | Maintainability | Twin C is semantically divergent (content-flattening, extra-field harvest, Anthropic-only) — must NOT be unified; A+B unification is a parallel track, not a prerequisite (structural) |
| git-grep drift trap (P3-3) | ⚠️ **Weaken** | Maintainability | Lint, not structure; shells out, breaks on renames; AST test is deterministic and CI-portable (structural) |
| Log-and-strip on unknown types | ✅ **Confirmed, extended** | Resilience | Per-entry fail-soft + dedup + format-drift counter; stream translator error-free by construction (resilience) |

---

## 2. Focus 1 — Translator Module Design (API surface)

### 2.1 The real Anthropic precedent is mixed-type, not byte-canonical

The plan (P1-3) describes the precedent as `TranslateAnthropicToOpenAI` mirroring "returns mutated bytes + error". The code says otherwise:

- `TranslateRequest(anthropic *AnthropicRequest, modelMapping *ModelMappingConfig) map[string]interface{}` — **typed struct in, map out** (`pkg/proxy/translator/request.go:14`)
- `TranslateNonStreamResponse(openaiBody []byte, originalModel string) ([]byte, error)` — bytes (`response.go:11`)
- `TranslateBufferedStream(openaiBuffer []byte, ...)` — bytes (`stream.go:19`)

Go has no overloads; the existing module resolves the typed-vs-bytes tension by **distinct functions per input shape**. The proposed single `[]byte`-canonical surface ignores that.

### 2.2 Who holds what at the call-sites (evidence)

| Call-site | Holds today | Byte-canonical forces |
|---|---|---|
| race-external request (`race_executor.go:149-158`) | `rawBody []byte` re-parsed to `bodyMap` | fits |
| race-internal + ultimate-internal request (`convertToProviderRequest` `race_executor.go:654`, `convertRequest` `handler_internal.go:382`) | `map[string]interface{}` pre-conversion | map→bytes→re-parse waste |
| ultimate-external request (`handler_external.go:40-41, 55-58`) | `requestBody map + requestBodyBytes` + shallow `bodyCopy` | fits |
| ultimate-internal non-stream response (`handler_internal.go:71`) | typed `*ChatCompletionResponse` via `Encode(resp)` | struct→bytes→re-parse→re-encode waste |
| ultimate-external stream (`handler_external.go:218, 317-320`) | raw line bytes; map parse is transient (usage extraction only); emitted bytes are the raw line | fits |

### 2.3 Recommended API set (map-core + byte-wrappers; typed paths handled at provider layer per D2)

```go
// ---- request side ----
func TranslateRequestBody(body map[string]any) error            // core: in-place messages walk + reasoning_split
func TranslateRequestBytes(body []byte) ([]byte, error)          // wrapper: parse → core → marshal

// ---- non-stream response side ----
func TranslateNonStreamResponseBody(body map[string]any) error   // core: map walk, strip, concat
func TranslateNonStreamResponseBytes(body []byte) ([]byte, error)
// TranslateNonStreamResponseTyped REMOVED per D2 — internal Encode(resp) sites extract in pkg/providers/openai.go

// ---- stream side ----
type StreamTranslator struct { lastIssued map[int]string }       // per-stream lifetime, NOT goroutine-safe (documented)
func NewStreamTranslator() *StreamTranslator
func (t *StreamTranslator) ChunkBytes(line []byte) (strippedOriginal []byte, emitted [][]byte, err error)
```

Why this holds across all 4 paths:

- **Complexity**: no redundant parse/marshal round-trips — each site calls the variant matching what it already holds. The byte-identical negative tests stay honest because a gated-off site touches nothing.
- **Scalability**: per-chunk work stays O(chunk); the map-core avoids the double allocation the byte-canonical form implies.
- **Maintainability**: mirrors the *actual* precedent (functions per shape) rather than a fictitious canonical signature; table-driven tests per variant.
- **Risk**: gate short-circuits before any marshal, so the negative-case invariant is structurally guaranteed, not just tested.
- **Cost**: one extra function per side vs. plan; zero new infrastructure.

### 2.4 Thread-safety (settled by evidence)

All four stream loops are **single-goroutine sequential per request** (`for event := range eventCh` at `race_executor.go:364`, `handler_internal.go:165`; read-line loop at `handler_external.go:222`; `race_executor.go:1063`). Race *attempts* run concurrently but each owns its loop and state. No mutex needed; document `StreamTranslator` as not goroutine-safe.

### 2.5 Would `MapRequest(msgs, flag)` / `MapResponseNonStream(resp)` / `MapResponseStream(delta, state)` hold up?

Mostly yes — with the amendments above. The requested shape's spirit (stateless per-chunk translation, explicit state when needed) survives; what changes is (a) map-core variants for the sites that hold maps, (b) provider-layer extraction in `pkg/providers/openai.go` for the internal `Encode(resp)` sites (D2 supersedes the typed translator variant), (c) state as a per-stream instance rather than a bare parameter.

---

## 3. Focus 2 — Streaming Semantics (R9)

### 3.1 Verdict: stateless-incremental default + opt-in state — confirmed

The failure-mode asymmetry is decisive:

| Wrong-mode scenario | Client experience | Recoverability |
|---|---|---|
| stateless-mode meets **cumulative** wire (if MiniMax is cumulative) | `reasoning_content` grows quadratically (each chunk replays prior text); visible, looks stuck | Client-side clamp; still semantically correct; detected immediately in P3 e2e |
| cumulative-mode meets **incremental** wire | suffix detection fails → nothing emitted → **silent reasoning loss** | Invisible; worst case |

Defaulting to the mode whose failure is *visible* (stateless/incremental) is correct. Live verification remains a post-merge acceptance gate (plan already says so).

### 3.2 Auto-detection: rejected (fragile)

- Repetitive reasoning ("Wait, let me reconsider…" repeated for self-correction) produces substring-overlap false positives mid-stream.
- Detection needs N≥2 chunks; the first chunk is already emitted before a mode can flip.
- Interleaving with tool calls (R5) makes mid-stream mode flips produce inconsistent ordering.
- Verdict: keep explicit opt-in. No heuristic.

### 3.3 State ownership: per-stream translator instance (amends P2-3)

Every path has a natural per-request function scope where the instance is constructed:

| Path | Construction point |
|---|---|
| race-internal stream (`race_executor.go:326`) | `translator.NewStreamTranslator()` before `for event := range eventCh` (line 364), alongside existing per-stream locals (`:339-362`) |
| internal-handler / ultimate-internal stream (`handler_internal.go:109-379`) | before line 158/165, alongside `:152-156` locals |
| ultimate-external stream (`handler_external.go:182`) | next to `toolcall.NewToolCallBuffer` at `:199-213` |
| race-external stream (`race_executor.go:1025+`) | at loop start |

The `Reset()`-on-stream-end discipline (P2-8) is a footgun: race retries spawn new attempts and a stale state silently corrupts suffix-diffing. An instance whose lifetime is the loop scope removes Reset entirely.

### 3.4 Per-entry emission + SSE re-flush points (amends P2-5)

Per-entry emission (N client chunks from 1 upstream chunk) is safe on all four paths, but the guarantee differs:

- race-internal (A): chunks appended to `upstreamReq.buffer.Add` (`:427/503/533/604`) — buffered sink, final write later; order trivially preserved.
- internal/ultimate-internal (B, D): writes go to `buf` with a **single** `w.Write(buf.Bytes()); flusher.Flush()` at end (`handler_internal.go:356-357`); append N chunks in order.
- ultimate-external (C): **no mid-stream flush exists today** — bytes accumulate in `buf` (`:219`), single write+flush at `:310-311`. Safe, but note: this path has no incremental flush, so "translator runs before each flush point" (plan's R6 mitigation) degenerates to "runs before the single flush". Document it; do not add per-chunk flushing to this path as part of this feature.

**Codify in P2-2 godoc**: "Caller must write all returned chunks in order before the next flush boundary."

---

## 4. Focus 3 — Gate Placement & Credential Threading (R3)

### 4.1 Flag home: RequestContext for race; parameters for ultimate (amends P1-5/P1-6)

- `requestContext` (unexported, `handler_helpers.go:23`; `ultimateModelEnabled bool` at `:96` with parse at `handler.go:465-469`) is the right home **for proxy-package paths** — the `rc.ultimateModelEnabled` precedent applies cleanly. Adding `interleavedThinking bool` is a 2-line change.
- **P1-6's premise is false**: `pkg/ultimatemodel/Handler.Execute` (`pkg/ultimatemodel/handler.go:225-236`) receives `(parentCtx, w, r, requestBody, originalModelID, hash, headersSent, tokenModelID)` — no `rc`. The race coordinator gets `&rc.conf` + `r` (`pkg/proxy/handler.go:765`). So the flag cannot ride `rc` into ultimate paths.
- **Recommendation**: parse the header **once in `Execute`** (it has `r`) and pass `interleaved bool, upstreamProvider string` down to `executeInternal`/`executeExternal` as plain parameters. Matches `executeInternal`'s existing self-resolution idiom (`ResolveInternalConfig` at `handler_internal.go:30`, provider at `config.go:636`).

### 4.2 R3: signature widening confirmed, narrowed to `provider string`

- `executeExternal` is unexported, **1 production caller** (`handler.go:296`) + ~10 test callers in one file — blast radius small and contained.
- Prefer `upstreamProvider string` over `*models.CredentialConfig`: narrower, no nil-handling, and `Execute` already owns `modelCfg`/`modelsMgr` to do the lookup.
- **Reject `context.Context` value threading**: `context.WithValue` appears exactly once in the tree (`pkg/mcp/auth.go:56`, unexported key) — not a convention; hidden data flow; the value is needed at 3 in-package decision points where a parameter is visible and testable.

### 4.3 🔴 Hidden coupling: header leak on BOTH external paths (amends P1-7; **D4**)

`executeExternal` forwards **all** client headers upstream (`handler_external.go:79-87`, skipping only Host/Content-Length/Transfer-Encoding); race-external strips only `x-llmproxy-*` (`race_executor.go:163-166`). Left as-is, `x-proxy-interleaved-thinking` leaks to MiniMax on **BOTH** external paths. **Decision (D4):** strip exactly `x-proxy-interleaved-thinking` (case-insensitive) at BOTH `handler_external.go:79-87` AND `race_executor.go:163-166`. The existing general `x-llmproxy-*` strip at race-ext is unchanged. The general `x-proxy-*` strip is **REJECTED** — would change flag-absent forwarding behavior and violate the critical backward-compat invariant. The planned negative-case tests won't catch this — they assert bodies, not headers — so add a header-assertion to P3-2/P3-5 (assert outbound headers to mock upstream contain no `x-proxy-interleaved-thinking` keys on either external path).

---

## 5. Focus 4 — Struct Evolution Round-Trip Hazards

### 5.1 `ChatMessage.ReasoningDetails` — kept for request-side carry (D1/D2)

`providers.ChatMessage` (`interface.go:43-50`) has no custom `MarshalJSON`/`UnmarshalJSON`, no interface types. `ReasoningContent string` already exists at `:49` targeting the same JSON family but a different shape (string vs array) — no conflict. `omitempty` on a nil slice → byte-identical when absent. **D1:** keep this field for request-side carry-through so per-message `reasoning_details` survives the `map→*ChatCompletionRequest` hydration on internal paths. **D2:** NOT used for response extraction on typed paths (openai.go extracts during unmarshal and writes `ReasoningContent` directly; sets `ReasoningDetails = nil` on the typed response so `omitempty` drops the key on Encode).

### 5.2 `StreamEvent.ReasoningDetails` — dead surface, drop it (amends P1-2)

`providers.StreamEvent` (`interface.go:118-126`) is an in-memory normalized event, not a wire struct — and the stream translator operates on `chunk []byte` (confirmed §2). Adding the field is harmless but unused. Either omit it from `StreamEvent` entirely or tag `json:"-"` with a godoc note. Dead surface on a shared interface is a standing invitation for misuse.

### 5.3 Map re-marshal vs verbatim passthrough — the invariant map (amends P2-4/P2-6)

| Path | Response re-marshaled today? | Translator operates on | Gated-off guarantee |
|---|---|---|---|
| race-internal non-stream (`race_executor.go:254`) | YES | `bodyBytes` | skip call → unchanged |
| race-internal stream (`:326`) | YES (per-chunk `json.Marshal` `:402/419/483/526/602`) | raw SSE line | skip → unchanged |
| internal-handler non-stream (`internal_handler.go:110`) | YES (`Encode(resp)` `:117`) | typed struct (§2.3 variant) | skip → unchanged |
| internal-handler stream (`:121`) | YES (per-chunk `:178/200/254`) | raw line | skip → unchanged |
| ultimate-external non-stream (`handler_external.go:36`) | **NO — verbatim** (`io.ReadAll` `:115`, `w.Write` `:148`) | bytes | **strictest: must touch nothing** |
| ultimate-external stream (`:182`) | **NO — verbatim** into `buf` (`:262`) | raw line | **strictest** |
| ultimate-internal non-stream (`handler_internal.go:57`) | YES (`Encode(resp)` `:71`) | typed struct | skip → unchanged |
| ultimate-internal stream (`:110`) | YES (per-chunk `:186/202/249/279/325`) | raw line | skip → unchanged |

Key architectural consequence: **the gate must short-circuit before any parse** — on the two verbatim paths, a gated-off translator must not even re-marshal "for symmetry", because map re-marshal reorders keys (Go maps iterate non-deterministically → `json.Marshal(map)` alphabetizes) and reformats numbers (float64). Byte-identical negative tests on ultimate-external pass only if the off-path is a pure no-op.

### 5.4 Unknown-field preservation — moot for this feature, but verify in P3

Typed `[]ReasoningDetail` drops unknown sub-fields (e.g. a future `signature`) on internal paths; map paths preserve them. **The asymmetry is moot because the strip (R11) deletes the whole array on response side regardless.** But this only holds while the strip is total: P3-2 must assert "no `reasoning_details` key at any nesting level" (already in Success Criterion #3) — keep it.

### 5.5 Double-serialization on typed non-stream paths (amends P2-1)

Traced end-to-end (`internal_handler.go:110-118`): today the site consumes a typed `*ChatCompletionResponse` and `Encode(resp)`s it — it never holds response bytes. Intercepting at byte level would require reading the body and re-parsing: a heavy refactor. **The typed walker variant (§2.3) avoids the round-trip entirely**: mutate `resp.Choices[i].Message.ReasoningContent` (concat), set `ReasoningDetails = nil`, `omitempty` drops the key on encode. One function, no double-serialization.

---

## 5.6 Drift Resistance (R2) — Focus 5

### Measured similarity of the converter twins

| Twin | Location | Lines | Character |
|---|---|---|---|
| A | `convertToProviderRequest` `race_executor.go:654-788` | 135 | map→typed; handles `name` (`:727-729`); reasoning_content (`:723-725`); tool_choice via `TranslateOpenAIToolChoiceToAnthropic` (`:780` — a likely copy-paste oddity for an OpenAI-shaped request) |
| B | `convertRequest` `handler_internal.go:382-515` | 134 | **~90% identical to A**; divergences: no `name` field; tool_choice passed raw (`:503`); model from body |
| C | `convertRequest` `internal_handler.go:272-434` | 163 | **structurally divergent**: flattens multimodal content via `strings.Builder` (`:296-307`); **no reasoning_content handling**; harvests unknown fields into `Extra` (`:425-431`); handles top_p/n/stop/penalties/logit_bias/user that A/B ignore; reachable only via `handler_anthropic.go:461-462` |

### Verdict: translator call-sites (plan) over shared-converter refactor — confirmed; A+B unification is a parallel track

1. **Do not fold twin C in**: its content-flattening and extra-field harvesting are semantic behavior; unification changes wire behavior. Plan's exclusion is correct.
2. **A+B extraction is a parallel track (post-merge, separate PR)**: they are near-duplicates whose drift (missing `name` in B; raw vs translated tool_choice) looks accidental. A shared `ConvertOpenAIMap(body map[string]any, model string)` would shrink the drift patrol surface from 3 files to 1 + genuine variant C. **Verify the tool_choice divergence against git history before collapsing** — `TranslateOpenAIToolChoiceToAnthropic` at twin A `:780` looks like a copy-paste oddity since both build OpenAI-shaped requests.
3. **Replace the git-grep trap with a Go AST test**: `go/parser` walk over the 4 call-site files asserting no `reasoning_details`/`reasoning_split` composite-literal or map-index assignment outside `translator/`. Deterministic, no subprocess, no git dependency, CI-portable. (Keep the grep as a belt-and-suspenders local check if desired.)

Why call-sites beat shared-converter as the *prerequisite*: the plan's ordering (map mutation **before** map→struct conversion) composes with any converter state — translator injection does not depend on unification, and unification without the translator would not have stopped reasoning-field drift anyway.

---

## 6. Focus 6 — Failure Modes & Graceful Degradation

### 6.1 Error contract (amends P2-2; new)

**Make the stream translator error-free by construction** — `ChunkBytes` never returns a fatal error; on internal anomaly it logs (sampled WARN) and emits a passthrough chunk (reasoning_details stripped, or byte-unmodified if the field is absent). Rationale: (a) mid-stream abort discards 99% of a stream over a 1% malformed reasoning entry; (b) the codebase precedent for chunk-parse failure is skip-and-continue (`translator/stream.go:42-46`: log + `continue`); (c) an error-free contract eliminates 4 per-site abort/continue decisions that would drift.

**Non-stream keeps its `error` return** — malformed body is genuinely user-visible, and callers already route translator errors to `WriteError` (HTTP 502 `api_error` per `translator/errors.go:74`, precedent `adapter_anthropic.go:143-146`). Non-stream has no partial-state concern.

| Error class | Caller-fatal? | Translator action |
|---|---|---|
| Upstream read error / EOF-before-`[DONE]` / context cancel | YES | N/A — outside translator |
| Upstream SSE error chunk | YES | N/A — caller aborts, maps to client `event: error` |
| Translator's own `json.Unmarshal(chunk)` fails | NO | log WARN → passthrough (strip key if parseable, else raw) |
| Entry missing `text` key (v1 shape) | NO | **skip entry, emit nothing** — never an empty `reasoning_content=""` delta |
| Entry `type` unknown / missing | NO | debug-log (sampled) + skip |
| `reasoning_details` wrong JSON type (string/object) | NO | strip key + WARN |
| Duplicate text vs existing `reasoning_content` | NO | drop duplicate (containment check, §6.3) |
| Mid-stream format version change | NO | WARN + counter; continue; treat as new stream state |

### 6.2 Format versioning (`format:'MiniMax-response-v1'`)

- **Log-only at WARN (sampled) + atomic counter; no version gate, no rejection.** The proxy's job is loss-aware translation, not upstream validation. R11 (no-leak) is preserved by always stripping `reasoning_details`.
- The dominant v2 failure is **silent reasoning loss**: if MiniMax renames `text`→`content`, both the typed struct and the map walk yield empty strings per entry → client sees empty reasoning with no error anywhere. Mitigation: pluggable text extraction — `extractEntryText(entry)` checks `text`, falls back to `content`, then gives up (skip + WARN). Cheap forward-compat without version-knowledge leaking into the module.
- Request-side v1 injection going stale if MiniMax moves to v2 is 🟡 tolerable (MiniMax ignores or auto-upgrades; no client-visible breakage); revisit on observed drift counter.

### 6.3 🔴 Both-fields duplication hazard (amends P2-1)

The plan's P2-1 says concat details into `reasoning_content` "preserving any existing value". If MiniMax emits **both** fields with the same text (the Q2 "yes" mode the mock harness deliberately tests — `test/test_mock_minimax_reasoning.sh` mode (i)), the client sees duplicated reasoning. Fix: per-entry dedup via string containment — if `entry.text` is contained in (or equals) the existing `reasoning_content` (non-stream) or `state.lastIssued[choiceIdx]` (stream), skip the entry. Detection is cheap (strings.Index). This rule also covers the cumulative-wire mode partially (each cumulative chunk contains prior text) — but do not conflate: cumulative detection stays the explicit opt-in state; dedup is unconditional.

### 6.4 New P3 test cases (gaps found)

- chunk parse failure → output identical (passthrough)
- both fields present → deduplicated single reasoning string
- entry missing `text` key → **no** client chunk (silent skip, not empty string)
- mid-stream v1→v2 → WARN, no abort, v2 entries skipped
- `StreamTranslator` lifetime: fresh instance per request (grep/AST guard or lint)
- outbound headers to mock upstream contain no `x-proxy-interleaved-thinking` key (catches §4.3 leak; per binding D4 — narrow strip on BOTH external paths, general `x-proxy-*` strip REJECTED)

---

## 7. Risk Register (aggregated, deduped — highest severity per concern kept)

| # | Risk | Severity | Mitigation (delta to plan) |
|---|---|---|---|
| H1 | Header leak `x-proxy-interleaved-thinking` → MiniMax on ultimate-external (`handler_external.go:79-87`) | 🔴 | Strip in P1-7's header loop; header-assertion test in P3 (§4.3) |
| H2 | Both-fields duplication reaches client (P2-1 concat) | 🔴 | Containment dedup, unconditional (§6.3) |
| H3 | Silent reasoning loss on format v2 (renamed text key) | 🔴 (latent) | Pluggable `extractEntryText` fallback + drift counter (§6.2) |
| H4 | `rc` propagation premise false for ultimate paths (P1-6) | 🟡 | Parse once in `Execute`, pass `interleaved bool` + `upstreamProvider string` down (§4.1) |
| H5 | Byte-identical negative tests break if gated-off path re-marshals (ultimate-external verbatim paths) | 🟡 | Gate short-circuits before parse; strictest no-op on verbatim paths (§5.3) |
| H6 | `StreamState` Reset footgun under race retries (P2-3/P2-8) | 🟡 | Per-stream `StreamTranslator` instance; delete Reset discipline (§3.3) |
| H7 | Empty `reasoning_content=""` deltas from entries missing `text` | 🟡 | Skip-entry rule: emit only non-empty after TrimSpace (§6.1) |
| H8 | Ultimate-external stream has no mid-stream flush (single write at `:310`) | 🟢 | Document; do not add flushing in this feature (§3.4) |
| H9 | `StreamEvent.ReasoningDetails` dead surface | 🟢 | Drop or `json:"-"` (§5.2) |
| H10 | git-grep drift trap fragile in CI | 🟢 | AST test replacement (§5.6) |

R1 (backward compat) is structurally covered by the gate short-circuit; R9 remains a post-merge live-verification gate as the plan already states.

---

## 8. Phase-Plan Incorporation Map

Concrete deltas the phase plans should absorb (do NOT rewrite tasks wholesale — amend):

### Phase 1 amendments

| Task | Amendment |
|---|---|
| P1-2 | **(D1)** Add `ReasoningSplit *bool \`json:"reasoning_split,omitempty"\`` on the chat-completion **request** struct (`pkg/providers/interface.go:39`); pointer type; no MarshalJSON; no `Extra` reuse. Keep `ChatMessage.ReasoningDetails` (request-side carry). **Drop** `StreamEvent.ReasoningDetails` (or `json:"-"` + godoc) — D2 makes it strictly dead. |
| P1-3 | API set → map-core + byte-wrapper (§2.3): `TranslateRequestBody(map[string]any) error` core; `TranslateRequestBytes([]byte) ([]byte, error)` wrapper. **D2 scope: external paths only.** Document: not goroutine-safe; gate is caller's job. |
| P1-5 | Keep header parse at handler entry → `rc.interleavedThinking` (race paths). Acceptance unchanged. |
| P1-6 | **B3 + H4 — three-mechanism flag plumbing.** (a) race via `rc`; (b) ultimate paths: parse header once in `ultimatemodel.Execute`, pass `interleaved bool, upstreamProvider string` down; (c) **race-internal: thread explicit `interleaved bool` parameter** through coordinator (`:765`) → executor signatures (B3 — `executeInternalRequest(ctx, cfg, rawBody, req)` gets neither rc nor `*http.Request`). Single shared helper. |
| P1-7 | **(D3)** Narrow to `upstreamProvider string` (not `*CredentialConfig`). Derive provider from credential when `modelCfg.CredentialID` is set (`GetCredential(...).Provider` → `strings.ToLower(provider) == factory.ProviderMiniMax` per `handler_anthropic.go:297`; canonical reference at `pkg/providers/factory.go:17`). Empty/unset `CredentialID` ⇒ gate=false. Operator requirement documented; P3-5 fixture attaches credential. **(D4)** Strip exactly `x-proxy-interleaved-thinking` (case-insensitive) at BOTH `handler_external.go:79-87` AND `race_executor.go:163-166`. Existing general `x-llmproxy-*` strip at race-ext unchanged. REJECTED: general `x-proxy-*` strip. |
| P1-8 (wiring) | **5 wiring sites (B6).** Race-internal + ultimate-internal set typed `ReasoningSplit *bool` (D1) on the request struct AFTER convertRequest returns; `ChatMessage.ReasoningDetails` is populated from the input map. Ultimate-external + race-external use the translator: ultim-ext bytes wrapper; race-ext **map-core** at both `convertToProviderRequest` (twin A, with gate in caller per W6) AND `race_executor.go:147-155` (5th site, B6). **Twin C DE-SCOPED (B3).** Dormant — do not wire. |

### Phase 2 amendments

| Task | Amendment |
|---|---|
| P2-1 | **(D2)** Translator handles EXTERNAL response paths only. **INTERNAL paths extract in `pkg/providers/openai.go`** — stream parser emits one `thinking` StreamEvent per `reasoning_details` entry (consumed by existing `case "thinking"` handlers); non-stream extracts in `extractNonStreamContent` / response-assembly. **Implementation locus (R2-residual D5):** extraction function lives adjacent to `extractReasoningContent` (`pkg/providers/openai.go:498`, called at `:396`); stream-parser calls from the existing `json.Unmarshal`-of-chunk site (`pkg/providers/openai.go:374`). **Operational order (R2-residual D5):** check `reasoning_details` FIRST; only when absent/empty fall back to `reasoning_content` (single-winner rule, reasoning_details wins when both present). **R3 layering edge (helper-only import):** openai.go imports `translator.extractEntryText` as HELPER-ONLY; the import site carries the comment `// extractEntryText is a helper-only import from pkg/proxy/translator; it must NOT pull provider types back — see R3 layering decision`. **R2-residual D2 wrong-layer guard:** the openai.go parser is data-driven and provider-agnostic; it does NOT branch on `cred.Provider`. Provider gating belongs to the caller/flag layer at `pkg/proxy/handler.go` entry. **Single-winner rule** (D2) applied at extraction (typed paths) or translator-side (byte paths) — consistent outcome. Typed variant `TranslateNonStreamResponseTyped` is NOT exposed (D2). **Skip entries:** H2 dedup at extraction time on typed paths (translator-side on byte paths); H7 empty-text skip; unknown-type debug-log + skip. `extractEntryText` exported for openai.go to call (H3 forward-compat consistency). |
| P2-2 | `(D2)` `StreamTranslator` instance API (§2.3), error-free construction (§6.1): returns chunks, never fatal. **External paths only.** Godoc: "input is SSE line without the `data: ` prefix; caller must write all returned chunks (with prefix and trailing `\n\n`) in order before next flush boundary" (W9); "not goroutine-safe". |
| P2-3 | **D2 — replace** `StreamState` + `Reset()` with `NewStreamTranslator()` per-stream instance on EXTERNAL paths only; delete P2-8's Reset discipline. State map keyed by choice index, as planned. **Internal stream construction points removed** (D2; openai.go emits `thinking` events directly). |
| P2-4 | **D2 — ultim-ext non-stream**: translator on bytes, gated — **strictest no-op** when off (§5.3). **Internal non-stream paths:** openai.go extraction during unmarshal; translator NOT invoked. **(W5)** P2-4(b) at `race_executor.go:275` uses openai.go extraction (NOT bytes wrapper); site marshals a typed response. |
| P2-5 | **D2 — ultim-ext stream**: translator on raw line **before** `toolCallBuffer.ProcessChunk` (W3 — explicit ordering; translator first so tool-call buffer sees stripped chunk). **Internal stream paths:** no translator invocation. Note single-flush path (H8). |
| P2-6 | **(D2)** Map-vs-struct resolved by removing typed variant — internal typed paths extract in openai.go, NOT the translator. Map variant + bytes wrapper only. |
| P2-8 | Delete Reset flushing; keep "no flush needed in stateless mode". |
| P2-9 | Pluggable `extractEntryText` + format-drift WARN counter (§6.2). **Exported** for openai.go to call (D2). |
| (new) | **W9 — `data: ` prefix contract** in `ChunkBytes` godoc; remove dead `state != nil` guard from P2-2. |

### Phase 3 amendments

| Task | Amendment |
|---|---|
| P3-1 | Add cases: parse-failure passthrough; both-fields dedup; missing-`text` skip; mid-stream v1→v2; per-stream instance lifetime guard; **W10 idempotence test**; **W11 empty-`[]` round-trip test**; **W9 input contract test (data: prefix)**; **empty-input no-op test (suggestion)**; **openai.go extraction tests (D2)** — stream `thinking` per entry; non-stream populates `ReasoningContent` + clears `ReasoningDetails`; single-winner at extraction. |
| P3-2 | **(D4)** Add header assertion: outbound headers to mock contain NO `x-proxy-interleaved-thinking` keys on BOTH external paths (was: ultim-ext only). **(W8)** Add `usage` field preservation criterion. |
| P3-3 | **Per suggestion** — narrow scope to `&translator.ReasoningDetail{...}` composite literals only (deterministic; fewer false positives). Mark P1-2 / P2-3 acceptance refs as **deferred** until P3-3 lands. |
| P3-5 | **(D1)** Per-path assertions for typed `ReasoningSplit *bool` on internal paths (race-internal + ultimate-internal). **(D2)** Single-winner assertion (consistent outcome). **(D3)** Credential-derived gate assertions for ultimate-external in all four combinations (set/unset × minimax/openai); fixture attaches credential. **(D4)** Header assertion scoped to BOTH external paths. **(W3)** Tool-call/reasoning interleaving order documented per path family. Scenario (b)/(c): assert dedup behavior when mock emits both fields (mode i). |
| P3-6 | Verification report: record observed wire semantics (incremental/cumulative), format value, both-fields mode, drift-counter baseline. |

---

## 9. Decisions Pending (for the leader)

1. **A+B converter unification** — recommend as post-merge parallel PR, contingent on checking the tool_choice divergence (`TranslateOpenAIToolChoiceToAnthropic` at `race_executor.go:780`) against git history. Not a blocker.
2. **`extractEntryText` fallback keys** — `text` → `content` proposed; decide whether any other v2-candidate keys are worth pre-registering or leave to drift observation.
3. ~~**Header strip scope** — strip just `x-proxy-interleaved-thinking`, or all `x-proxy-*`/`x-llmproxy-*` in `executeExternal`? (Recommend the general strip; it aligns with race-external behavior at `race_executor.go:163-166`.)~~ — **RETIRED** as a pending question; resolved by binding decision D4 (see §11 D4 row). Decision: narrow strip (case-insensitive `x-proxy-interleaved-thinking` only) on BOTH external paths; general `x-proxy-*` strip REJECTED to preserve flag-absent byte-identical invariant.

## 10. Open Questions

- R9 live verification (incremental vs cumulative) — unchanged from plan; post-merge acceptance gate.
- Q4 SSE error-frame format from MiniMax — outside translator scope; caller-fatal handling unchanged.
- Whether MiniMax accepts both `reasoning_content` and `reasoning_details` on input (affects request-side keep-both fallback) — already handled by plan's live-verification fallback.

---

## 11. Round 2 Decisions (binding) — summary for the reviewer

These four decisions are binding and incorporated verbatim into the phase plans:

| ID | Decision | Files touched | Resolves |
|----|----------|---------------|----------|
| **D1** | Add typed `ReasoningSplit *bool \`json:"reasoning_split,omitempty"\`` on the chat-completion request struct (`pkg/providers/interface.go:39`). Pointer type, no MarshalJSON, no `Extra` reuse. | `interface.go`, `pkg/ultimatemodel/handler_internal.go:382-462` (twin B sets it), `pkg/proxy/race_executor.go:653-722` (twin A site OR pre-hook) | B1 |
| **D2** | Provider-level extraction in `pkg/providers/openai.go`. Stream parser emits one `thinking` StreamEvent per `reasoning_details` entry (consumed by existing `case "thinking"` handlers); non-stream extracts in `extractNonStreamContent` / response-assembly. Translator scope reduced to EXTERNAL paths only. `TranslateNonStreamResponseTyped` is NOT exposed. `extractEntryText` is exported and shared. Single-winner rule applied at extraction (typed) or translator-side (byte). | `pkg/providers/openai.go`, `pkg/proxy/translator/minimax.go`, all phase plans | B2, W2 |
| **D3** | Ultimate-external provider gate derived from credential: `modelCfg.CredentialID` → `GetCredential(...).Provider` → `strings.ToLower(provider) == factory.ProviderMiniMax` (canonical reference value at `pkg/providers/factory.go:17`). Empty/unset `CredentialID` ⇒ gate=false. Operator requirement documented; P3-5 fixture attaches credential. | `pkg/ultimatemodel/handler_external.go:36-44`, `pkg/ultimatemodel/handler.go` (caller), P1-7 + P3-5 | B5, W1 |
| **D4** | NARROW header strip on BOTH external paths: `x-proxy-interleaved-thinking` (case-insensitive) at `pkg/ultimatemodel/handler_external.go:79-87` AND `pkg/proxy/race_executor.go:163-166`. Existing general `x-llmproxy-*` strip at race-ext unchanged. General `x-proxy-*` strip REJECTED (violates critical invariant). | both external path files, P1-7 + P3-2/P3-5 | B4, W7 |
| **R3** (layering — new, leader decision) | `pkg/providers/openai.go` imports `translator.extractEntryText` as a **HELPER-ONLY dependency** — `providers → translator` direction is an inversion of the usual `translator → providers` type flow. The import site MUST carry the comment `// extractEntryText is a helper-only import from pkg/proxy/translator; it must NOT pull provider types back — see R3 layering decision`. `pkg/providers` MUST NOT export `ReasoningDetail`; the type remains in `pkg/proxy/translator`. REJECTED option (a): moving `ReasoningDetail` to `pkg/providers` — scope-creep for marginal benefit. **Coupled with R2-residual D2 guard:** the openai.go parser does NOT branch on `cred.Provider`; provider gating belongs to the caller/flag layer (`pkg/proxy/handler.go` entry), never the parser. | `pkg/providers/openai.go` import block, P2-1a | (R3) |

---

*Confidence: **High** on structural and data-flow recommendations (all code-cited, verified by 3 independent analyses). **Medium** on resilience specifics (`extractEntryText` fallback keys, dedup containment rule) — they hedge against unverified upstream behavior; the live-verification gate will refine them. The assumption that would flip the streaming recommendation: if live MiniMax proves cumulative-only, the default flips to always-on state (the per-stream instance makes that a constructor change, not an API break). Round 2 decisions D1-D4 reduce the open surface area by removing typed-variant dead surface (D2) and re-scoping the header strip to the narrow invariant-preserving scope (D4).*

---

## Amendment Log (Round 2)

| ID | Change | Anchor | Review item |
|----|--------|--------|-------------|
| AM-01 | §0: D1-D4 binding decisions added to summary; D4 narrowed from general `x-proxy-*` strip to specific `x-proxy-interleaved-thinking` on BOTH external paths. | D1, D2, D3, D4 | B1-B5, B4, W7 |
| AM-02 | §0 #4: B3 expanded — `executeInternalRequest(ctx, cfg, rawBody, req)` also lacks rc + `*http.Request`; needs explicit `interleaved bool` parameter. | B3 | B3 |
| AM-03 | §0 #3: D2 reinforces H9 — openai.go emits `thinking` events directly, so `StreamEvent.ReasoningDetails` is now strictly dead. | D2, H9 | B2 |
| AM-04 | §4.3: H1 mitigation narrowed (D4) — strip exactly `x-proxy-interleaved-thinking` (case-insensitive) on BOTH external paths; general `x-proxy-*` strip REJECTED. | D4 | B4, W7 |
| AM-05 | §5.1: clarified `ChatMessage.ReasoningDetails` is kept for request-side carry only (D1); not used for response extraction on typed paths (D2 — openai.go extracts during unmarshal). | D1, D2 | B1 |
| AM-06 | §5.2: added §5.2 explaining D1 typed `ReasoningSplit *bool` on the request struct (was missing before). Renumbered subsequent sections. | D1 | B1 |
| AM-07 | §5.3: H9 reinforced by D2 — `StreamEvent.ReasoningDetails` is now strictly dead. | D2 | B2 |
| AM-08 | §8: Phase-Plan Incorporation Map expanded to cover D1, D2, D3, D4; Phase 1 / Phase 2 / Phase 3 amendment tables rewritten to capture all binding decisions + warnings. | D1, D2, D3, D4, B3, B6, W1, W3-W11 | all |
| AM-09 | §11 added: Round 2 Decisions summary table for reviewer traceability. | D1-D4 | all |
| AM-09c (correction) | Post-aggregation cleanup: §2.3 heading rewritten to drop "typed variant" — "(map-core + byte-wrappers + typed variant)" → "(map-core + byte-wrappers; typed paths handled at provider layer per D2)". §2.3 API listing line removed (`TranslateNonStreamResponseTyped(resp *providers.ChatCompletionResponse)`); replaced with comment documenting the removal. §2.5 (b) rewritten: typed translator variant → "provider-layer extraction in `pkg/providers/openai.go` for the internal `Encode(resp)` sites (D2 supersedes the typed translator variant)". §2.3 is what P1-3 builds from — now matches the typed-variant removal across all phase plans. | D2 | post-verification |
| AM-R3 (R3 layering decision) | Leader decision (option b) — recorded in §11 decisions table. `pkg/providers/openai.go` imports `translator.extractEntryText` as HELPER-ONLY — the `providers → translator` direction is an inversion of the usual `translator → providers` type flow, documented at the import site. `pkg/providers` MUST NOT export `ReasoningDetail`; type stays in `pkg/proxy/translator`. Reflected in §8 P2-1 row (R3 layering edge noted; helper-only import comment specified). | R3 | R3 layering |
| AM-R2-D1 (R2-residual D1) | Purge stale general-`x-proxy-*`-strip references. **§6.4 (line 256):** header-assertion test rewritten for narrow strip (single header `x-proxy-interleaved-thinking` absent). **§9 Q3 (line 324):** RETIRED — struck through; resolution cited as binding D4 (§11 D4 row). | R2-residual D1 | D4 narrow-strip |
| AM-R2-D2 (R2-residual D2) | Wrong-layer guard added to §8 P2-1 row: the openai.go parser is data-driven and provider-agnostic; it does NOT branch on `cred.Provider`. Provider gating belongs to the caller/flag layer at `pkg/proxy/handler.go` entry. | R2-residual D2 | D2 wrong-layer |
| AM-R2-D5 (R2-residual D5) | §8 P2-1 row extended: implementation locus (extraction function adjacent to `extractReasoningContent` at `pkg/providers/openai.go:498`, called at `:396`; stream-parser from `json.Unmarshal` site at `:374`); operational order of single-winner rule codified (check `reasoning_details` first; fall back to `reasoning_content` only when absent/empty). | R2-residual D5 | D2 locus |
| AM-06 (ride-alongs) | **Provider constant consistency:** verified `factory.ProviderMiniMax` exists at `pkg/providers/factory.go:17`. Standardized on the constant across §8 P1-7 row + §11 D3 row (replace `"minimax"` literal with `factory.ProviderMiniMax`). **Citation fix:** §0 + §4.1 corrected from `handler.go:227-236` to `pkg/ultimatemodel/handler.go:225-236` (the `Handler.Execute` signature). **AST drift-trap scope:** already aligned with P3-3 (per-suggestion narrow target on `&translator.ReasoningDetail{...}` composite literals) — verified, no edit needed. | R3 ride-alongs | provider-constant + citation |
ode(resp)` sites (D2 supersedes the typed translator variant)". §2.3 is what P1-3 builds from — now matches the typed-variant removal across all phase plans. | D2 | post-verification |
