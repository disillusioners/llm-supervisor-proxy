# Research Report: OpenAI Decision API + Jev (TypeSafe) for the "decision" model type

Tool check: ✅ web search + web reader available and used throughout (9 primary-source reads). No repo writes.

**Bottom line up front:** Both providers are real and documented. OpenAI Decisions API = `POST /v1/decisions` on `gpt-6-luna` (public beta since 2026-10-06). Jev = TypeSafe AI's "System One" decision model served at `POST https://api.typesafe.ai/v1/systemone`. **Jev is NOT wire-compatible with the OpenAI Decision format — it uses a structurally different "System One" schema, so the proxy needs a translation layer for Jev-backed models.** Both are synchronous-only (no streaming) and bill input tokens only.

## Q1 — OpenAI Decisions API
Official sources read in full: API reference `https://developers.openai.com/api/reference/resources/decisions/methods/create` and the guide `https://developers.openai.com/api/docs/guides/decisions`.

### Endpoint & auth
- **Path:** `POST /v1/decisions` (full URL `https://api.openai.com/v1/decisions`) — [verified-from-docs]
- **Auth:** `Authorization: Bearer $OPENAI_API_KEY`, `Content-Type: application/json` — [verified-from-docs]
- **Status:** public beta (2026-10-06); GA "in the coming weeks" — [verified-from-docs]

### Request schema (complete — from the official reference)
| Field | Type | Req? | Notes |
|---|---|---|---|
| `model` | string | ✅ | Only `gpt-6-luna` currently [verified-from-docs] |
| `input` | string \| array of DecisionInputMessage | ✅ | String, or user messages (`role: "user"` only; `type: "message"` optional). `content` = string or parts array |
| ↳ part `input_text` | `{type:"input_text", text}` | | text maxLength 10,485,760 |
| ↳ part `input_image` | `{type:"input_image", image_url, detail?}` | | `detail`: `"low"|"high"|"auto"|"original"|null`, default auto. ≤128 images/request. No file IDs, no other roles, no audio/tools |
| `questions` | array of Predicate \| Choice \| Score | ✅ | 1–128 questions (count cap from liteLLM docs); answers returned in order |
| ↳ `predicate` | `{type:"predicate", instructions, name?}` | | yes/no likelihood |
| ↳ `choice` | `{type:"choice", instructions, choices[], name?}` | | `choices`: 2–255 unique `{value: string\|boolean, description?}` — values are typed (`true` ≠ `"true"`) |
| ↳ `score` | `{type:"score", instructions, levels[], name?}` | | `levels`: ordered `{label, description?}` (liteLLM: 2–10, lowest first) |
| `safety_identifier` | string \| null | ❌ | ≤128 chars, end-user identifier, org-scoped |

⚠️ **Image input doc discrepancy** [both facts verified, sources conflict]: the **API reference** schema says `image_url` accepts "a base64-encoded image in a data URL **or a publicly accessible HTTP(S) image URL**", but the **guide** says "Images must be inline base64 data URLs. Hosted HTTP or HTTPS image URLs and `file_id` inputs aren't supported by this endpoint" (secondary coverage agrees with the guide). **Plan for base64-only**; the reference schema text likely lags.

### Response schema (complete)
Envelope is `{model, answers[], usage}` — **no `id`, no `object`, no `created` fields** [verified-from-docs]. Answer variants (order matches `questions`):
- `predicate`: `{type:"predicate", name, probability}` — probability 0–1
- `choice`: `{type:"choice", name, choice, probabilities:[{value, probability}], confidence}` — `choice` echoes your `value` (string or boolean)
- `score`: `{type:"score", name, score, probabilities:[{value:int64, label, probability}], confidence}` — `score` is the probability-weighted average of level indices and can fall between levels (e.g. 1.1) [verified-from-docs: guide worked example]
- `refusal`: `{type:"refusal", name|null}` — model declined; other questions in the same request still answered [verified-from-docs]

### Usage / cost
- `usage`: `{input_tokens, input_tokens_details:{cached_tokens, cache_write_tokens}, output_tokens, output_tokens_details:{reasoning_tokens}, total_tokens}` — all int64 [verified-from-docs]
- **Pricing: $0.10 per 1M input tokens; zero charges for output, cache-read, cache-write.** Regional processing premiums and long-context multipliers apply [verified-from-docs: guide].
- **No decision-specific billing units** — plain token metering [verified-from-docs]

### Streaming, idempotency, latency, rate limits, errors
- **Streaming: none.** No `stream` parameter in the schema; synchronous only [verified-from-docs; liteLLM feature table "Streaming: No"]
- **Idempotency/retry params: none** [verified-from-docs: negative finding from full schema]
- **Latency:** OpenAI claims "typed answers about 10x faster than the Responses API" [verified as a claim]; no independent measurement published yet
- **Rate limits:** no Decisions-specific schedule published; standard usage-tier RPM/TPM assumed, 429 + retry-after [inferred]
- **Error shape:** assume standard OpenAI envelope `{"error":{"message","type","param","code"}}` [inferred from platform conventions — no decisions-specific error doc found]
- **SDKs:** official Go SDK ≥3.73.0 (`github.com/openai/openai-go/v3`, `client.Decisions.New`) [verified-from-docs] — useful reference types, but this proxy speaks raw HTTP (house style)
- **Compliance:** ZDR + HIPAA eligible; data residency US + Europe [verified-from-docs]

## Q2 — Jev (TypeSafe AI)
**What it is:** Jev is the flagship "System One" decision model from **TypeSafe AI** (typesafe.ai), early access ~2026-09-16. Evaluates typed questions against a `state`, returns typed answers with calibrated probabilities — no text generation. [verified-from-docs: docs.typesafe.ai]

Official sources read: `https://docs.typesafe.ai` (intro), `/introduction/quickstart`, `/api` (full HTTP reference), `/models`.

### Endpoint, auth, base URL
- **Base URL:** `https://api.typesafe.ai` [verified-from-docs]
- **Endpoint:** `POST /v1/systemone` (all models on this one path) [verified-from-docs]
- **Auth:** API key from TypeSafe console, `Authorization: Bearer <key>` [inferred — TypeSafe reference lists Authorization header (value stripped by reader); Bearer format corroborated by liteLLM + Kong docs. High confidence, not verbatim-verified]
- Also reachable via aggregators: OpenRouter `typesafe/jev-1.13`; liteLLM pass-through `/typesafe/v1/systemone` [verified-from-docs]

### Models
- `jev-latest` — moving alias; response `model` reports the **versioned ID** that actually answered (e.g. `jev-1.13.0`) — pin versions if thresholds are tuned [verified-from-docs: /models]
- Known IDs: `jev-1.13.0`, `jev-latest`, `jev-preview` [verified-from-docs: liteLLM registry; OpenRouter]
- Constraints: text-only (images/audio/video must be pre-processed into text), English best / CJK weaker, no fine-tuning, 64k combined context budget (state + all questions; 32k variant = state + longest question) [verified-from-docs: /models]
- **Pricing:** $0.042 per 1M input tokens, output tokens free [inferred: marktechpost + cellcog citing TypeSafe pricing page; input-only billing doc-verified]
- **Rate limits:** tokens/sec + requests/sec; over-limit → 429; also 529 Overloaded documented; exact quotas not published [verified status codes / gap on numbers]

### Native request/response schema ("System One" format)
Request [verified-from-docs: docs.typesafe.ai/api + liteLLM verbatim example]:
```json
{
  "model": "jev-latest",
  "state": "Customer wrote: I was charged twice for my order #4411...",
  "questions": {
    "is_refund_request": {"type": "noul", "instructions": "Is the customer asking for a refund?"},
    "urgency": {"type": "choice", "instructions": "How urgent is this?",
      "criteria": {"low": "can wait a week", "high": "needs action today"}},
    "frustration": {"type": "score", "instructions": "How frustrated is the customer?",
      "criteria": ["calm", "mildly annoyed", "angry"]}
  }
}
```
- `state`: string | object | array — the thing being judged; question keys are caller-chosen ids, "not sent to the underlying model"
- `noul` (their predicate): `instructions` (string|object|array) + optional `criteria: {true: "...", false: "..."}`; a noul needs `instructions` OR `criteria`
- `choice`: required `criteria` = map<label → description|null>, max 255 options
- `score`: required `criteria` = ordered array of level descriptions, 2–10 levels
- Questions evaluated in parallel, in isolation; "adding questions barely changes the response time"

Response [verified-from-docs: liteLLM verbatim TypeSafe response]:
```json
{
  "model": "jev-1.13.0",
  "answers": {
    "is_refund_request": {"type": "noul", "noul": 0.99},
    "urgency": {"type": "choice", "choice": "high", "confidence": 1.0,
      "probabilities": {"low": 0.0, "high": 1.0}},
    "frustration": {"type": "score", "score": 0.95, "confidence": 0.86,
      "legend": {"0": "calm", "1": "mildly annoyed", "2": "angry"},
      "probabilities": {"0": 0.07, "1": 0.91, "2": 0.02}}
  },
  "usage": {"input_tokens": 391, "output_tokens": 67}
}
```
- No streaming [verified-from-docs: liteLLM feature table]

### 🔴 CRITICAL — is Jev OpenAI-Decision-compatible?
**No. Proprietary "System One" format; translation required.** Structural differences (all [verified-from-docs]):
| Aspect | OpenAI `/v1/decisions` | TypeSafe `/v1/systemone` |
|---|---|---|
| Evidence field | `input` (string or messages w/ input_text/input_image parts) | `state` (string\|object\|array; no images) |
| Questions | ordered array, optional `name` | named map (key = id) |
| Yes/no type | `predicate` → `probability` | `noul` → `noul` (field name differs) |
| Choice options | `choices` array of {value: string\|boolean, description?} | `criteria` map label → description\|null (string keys only) |
| Score levels | `levels` array of {label, description?} | `criteria` array of strings/objects |
| Answers | ordered array | map keyed by question id; `legend` on score; probabilities as maps |
| Usage | 5 fields + cache/reasoning details | {input_tokens, output_tokens} only |
| Extras | `safety_identifier`, refusal answers, image input | neither (safety_identifier → 400 on TypeSafe) |

Semantics map 1:1, and **liteLLM v1.104.2+ ships exactly this bidirectional translation** (`docs.litellm.ai/docs/decisions`) — existence proof + reference implementation. Documented translation gotchas to adopt: OpenAI requires `instructions` (fill generic when absent, fold noul criteria into it); System One choice-of-1-label / score-of-1-level has no OpenAI equivalent (reject 400); boolean-vs-string value collision (`true` vs `"true"`) illegal across the bridge; text parts of `input` get joined into `state` (per-message structure lost); score semantics differ subtly (OpenAI score = weighted average, can be fractional; System One score = 0..levels−1, between-levels allowed).

## Q3 — Unverified / couldn't confirm (per field)
1. OpenAI Decisions rate-limit numbers — no endpoint-specific schedule published [gap]
2. OpenAI Decisions error body for this endpoint — standard envelope [inferred], not endpoint-verified
3. OpenAI image input: hosted HTTP(S) URLs — reference says allowed, guide says refused. Treat as base64-only until live-tested
4. TypeSafe auth header verbatim — Bearer [inferred] from secondary integrations
5. TypeSafe exact rate-limit quotas + exact pricing table ($0.042/1M is secondary-sourced twice; input-only billing doc-verified)
6. TypeSafe full model catalog table (GET /v1/models exists; jev-latest / jev-1.13.0 / jev-preview confirmed)
7. TypeSafe error JSON body shape — "standard HTTP status codes with a JSON body" [verified], exact fields [unverified]
8. Latency figures — both vendors' speed claims are vendor claims; no independent measurements

## Proposed wire schema (assumptions flagged)
Recommendation for the proxy's client-facing surface: **speak the OpenAI Decisions format at `POST /v1/decisions`** (de-facto standard — liteLLM, OpenRouter both normalize to it) and translate to System One for Jev-backed models. Mirrors the existing imggen pattern (dedicated endpoint, bypasses chat adapters).

### Client → proxy request (OpenAI Decisions wire, verified shape)
```json
{
  "model": "decision-router",
  "input": "Customer wrote: I was charged twice for my order #4411 and want a refund today.",
  "questions": [
    {"type": "predicate", "name": "is_refund_request", "instructions": "Is the customer asking for a refund?"},
    {"type": "choice", "name": "department", "instructions": "Which department should handle this?",
     "choices": [
       {"value": "billing", "description": "Payments, invoices, refunds"},
       {"value": "technical", "description": "Bugs and outages"},
       {"value": "other"}]},
    {"type": "score", "name": "frustration", "instructions": "How frustrated is the customer?",
     "levels": [{"label": "calm"}, {"label": "mildly annoyed"}, {"label": "angry"}]}
  ],
  "safety_identifier": "user-1234"
}
```
Supports `input` as string or messages array with input_text/input_image parts [verified]. Auth: proxy's own sk- Bearer token (house style), upstream Bearer per credential.

### Proxy → client response (OpenAI Decisions wire, verified shape)
```json
{
  "model": "gpt-6-luna",
  "answers": [
    {"type": "predicate", "name": "is_refund_request", "probability": 0.97},
    {"type": "choice", "name": "department", "choice": "billing",
     "probabilities": [{"value": "billing", "probability": 0.93}, {"value": "technical", "probability": 0.04}, {"value": "other", "probability": 0.03}],
     "confidence": 0.91},
    {"type": "score", "name": "frustration", "score": 1.2,
     "probabilities": [{"value": 0, "label": "calm", "probability": 0.1}, {"value": 1, "label": "mildly annoyed", "probability": 0.7}, {"value": 2, "label": "angry", "probability": 0.2}],
     "confidence": 0.61}
  ],
  "usage": {"input_tokens": 401, "input_tokens_details": {"cached_tokens": 0, "cache_write_tokens": 0}, "output_tokens": 0, "output_tokens_details": {"reasoning_tokens": 0}, "total_tokens": 401}
}
```
`refusal` answers possible per-question [verified]. When Jev serves: translate System One answers back (noul→probability, map→array probabilities, synthesize `label` from `legend`, synthesize OpenAI-style usage detail objects as zeros) [verified semantics; synthesis of absent detail fields is ASSUMPTION-by-design — liteLLM does the same].

**ASSUMPTIONS** (design choices, not spec): response `model` echoes versioned upstream ID; error envelope on proxy surface = house OpenAI envelope via models.NewOpenAIError; no `id`/`object`/`created` synthesis (neither provider returns them).

### Open schema questions for the user
1. Endpoint path: `/v1/decisions` (OpenAI-compatible, aggregator-normalized) — confirm; also expose `/v1/systemone` pass-through for native Jev clients?
2. Model kind value: `models.kind` new value `"decision"` — confirm discriminator string (NO migration needed per codebase research).
3. Image policy: base64-only from day 1 (guide-compliant), or allow hosted URLs with proxy-side fetch-and-encode? (Jev path is text-only regardless — 400 or drop?)
4. Cross-format validation: reject at proxy edge what cannot translate (choice with 1 option, score with 1 level, true+"true" collision, >255 choices, >10 levels, >128 questions) — adopt liteLLM's 400-before-upstream pattern?
5. Metering: both providers bill input-only — map input_tokens into existing hourly usage counters; decide treatment of Jev's free output_tokens (log-only?); decision-specific counters probably unnecessary.
6. Racing/fallback: decisions are cheap+fast — include decision models in race/fallback chains, or carve out like imggen (no race, sync + long deadline)?
7. Misroute guard: add IsDecision()-style guard rejecting decision-kind models on /v1/chat/completions (imggen precedent at pkg/proxy/handler.go:455)?
8. Upstream registry: support `openai` + `typesafe` provider adapters now; keep OpenRouter/liteLLM as fallback transports?

**Primary sources:** developers.openai.com/api/reference/resources/decisions/methods/create · developers.openai.com/api/docs/guides/decisions · docs.typesafe.ai/api · docs.typesafe.ai/models · docs.typesafe.ai/introduction/quickstart · docs.litellm.ai/docs/decisions · docs.litellm.ai/docs/pass_through/typesafe · openrouter.ai/blog/insights/what-is-jev · cellcog.ai/blog/openai-decisions-api · typesafe.ai/blog/introducing-system-one-models-and-jev
