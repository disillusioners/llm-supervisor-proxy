# Approach Comparison — Image-Gen Test Button

**Date:** 2026-10-20 · Aggregated from 4 worker reports (data-flow / resilience / structural / system-decomposition)
Axes: Complexity (20%, lower=better) · Scalability (20%) · Maintainability (25%) · Risk (20%, inverted — higher score = lower risk) · Cost (15%, inverted). Scores 1–5.

---

## Comparison 1 — Seam placement (Question B)

| Approach | Complexity (20%) | Scalability (20%) | Maintainability (25%) | Risk (20%) | Cost (15%) | Weighted | Verdict |
|---|---|---|---|---|---|---|---|
| **A: `pkg/imggen/test_exec.go`** — in-package free function, reuses `lookup`/`sharedHTTPClient`/`BuildUpstream`/`InspectResponse`/caps | 4 (one new file + one route; mirrors a shipped shape) | 4 (registry-driven — a new image-gen provider is probed automatically) | 5 (single source of machinery; white-box testable with `maxResponseBodyBytesForTest`; no handler injection needed) | 5 (bug class structurally dead — no client headers copied at all; cycle provably impossible) | 5 (purely additive; no production file behavior change) | **4.60** | ✅ **ADOPT** |
| B: executor in `pkg/ui/handlers_imggen.go` | 2 (must re-implement body rewrite, URL suffix, classifier, caps, timeout, headers) | 3 | 1 (two divergent implementations; a UI test passes even if production breaks) | 1 (re-opens duplicate-CT / Accept-Encoding gzip class; **silent misroute** to `/chat/completions` via `pkg/providers`) | 2 (double maintenance forever) | **1.75** | ❌ REJECT |
| C: resolver seams (`SetModelResolver`/`SetCredentialResolver`) / shared probe package | 3 | 3 | 2 (a third implementation shape; can't see private registries) | 2 (LB-engine rotating pick is semantically wrong for a probe; injecting the live Handler drags auth/meter/events in) | 3 | **2.60** | ❌ REJECT |

**Dominant axis:** Maintainability + Risk. A is the only candidate that can physically reach `lookup()` and `sharedHTTPClient` (both unexported — `minimax.go:212`, `client.go:26`); every alternative is a fork by construction. Tie-breakers not needed (decisive margin).

**Confidence: High.** Flips only if `pkg/imggen`'s internals were exported/rewritten first — no such refactor is planned.

---

## Comparison 2 — Success envelope / image delivery (Question E)

Upstream fact base: MiniMax image-gen returns **URL format** (`data[].image_urls`, body ≈ 451 B; `response_format:"url"`; `pkg/imggen/handler.go:51-52`, `live_e2e_test.go:110,126-129`). No `b64_json` on the production path.

| Approach | Complexity (20%) | Scalability (20%) | Maintainability (25%) | Risk (20%) | Cost (15%) | Weighted | Verdict |
|---|---|---|---|---|---|---|---|
| **A: CDN URLs inside the always-200 DTO** (`image_urls: []string`) | 5 (pass through; FE `<img src>` — zero new machinery) | 5 (≈451 B bodies; no payload scaling concern) | 5 (one wire shape; no server-side state; no second endpoint) | 4 (URL lifetime unverified — render immediately; not an echo of request data) | 5 (no extra fetch, no storage) | **4.90** | ✅ **ADOPT** |
| B: proxy fetches bytes → base64 data URL in DTO | 2 (a second upstream call + decode/re-encode) | 2 (multi-MB through `/fe/api` + gzipmw 100 MB decompressed cap) | 2 (new fetch machinery = the fork we are avoiding) | 1 (**violates exactly-one-call discipline**; 64 MB cap false-fails large images) | 2 (double bandwidth + latency) | **1.85** | ❌ REJECT |
| C: blob URL / bytes at a separate endpoint | 2 (second endpoint + state/TTL management) | 3 | 2 (two round-trips; cache invalidation) | 2 (state to expire; FE complexity) | 3 | **2.45** | ❌ REJECT |

**Adjudicated divergence recorded:** the resilience analysis argued for count-only success (no image data) on sanitization grounds. Overruled: the upstream returns **URLs**, so the multi-MB concern is moot; URLs are upstream-generated locators, not request echoes; the commission explicitly requires FE image rendering. The raw-body sanitization rule stands unchanged.

**Confidence: High.** Flips only if MiniMax were switched to `response_format:"b64_json"` in the canned body — deliberately pinned to `"url"` (and `n:1`, `1:1`) in the recommended canned request precisely to keep Approach A valid.

---

## Comparison 3 — Classifier order (Question F, the subtle one)

| Approach | Complexity | Scalability | Maintainability | Risk | Cost | Weighted | Verdict |
|---|---|---|---|---|---|---|---|
| **A: Outcome/`base_resp` first, then HTTP status** (§3.1 of the recommendation) | 4 (one extra branch + additive `Outcome.Code/Message`) | 4 | 5 (single classifier seam preserved; `InspectResponse` stays the only judge) | 5 (no false-success class) | 4 | **4.45** | ✅ **ADOPT** |
| B: decision-lane order verbatim (HTTP status first) | 5 (zero new code) | 4 | 3 | **1 (every MiniMax rejection → "success")** | 4 | **3.35** | ❌ REJECT — a green lie |

**Dominant axis:** Risk. B is simpler by ~10 lines and catastrophically wrong for this provider.

---

## Deduplication notes (per aggregation rules)

- The Content-Type/Accept-Encoding landmine was flagged independently by all four workers — kept at 🔴 with the most specific citations (`handler.go:352-377`; `test_exec.go:12-19`; pinned at `test_exec_test.go:186-190` and `handlers_decision_test.go:279-284`).
- The unauthenticated-`/fe/api` exposure was raised by resilience + structural — merged, kept 🔴, scoped as a pre-existing condition with in-feature mitigation (cap + cooldown) and an out-of-scope escalation.
- The `data`-shape ambiguity (array-of-objects vs object) was raised by data-flow + system-decomposition — kept 🟡 with both citations (`handler_test.go:111` vs `live_e2e_test.go:127-129`).
