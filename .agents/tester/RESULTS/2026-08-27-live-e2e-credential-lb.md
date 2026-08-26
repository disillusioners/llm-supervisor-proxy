# Test Report: LIVE E2E — credential LB vs REAL MiniMax traffic (user's running proxy)

Date: 2026-08-27 (UTC)
Target: user's manually-started proxy @ http://localhost:4123 (untouched throughout — client/observer only; both SSE listeners killed after use; server confirmed alive at end)
Model: `coding` (internal → MiniMax-M3), credentials [minimax-company (w1,pos0), minimax-mindspath (w1,pos1)], both provider=minimax @ api.minimax.io, PG-backed config (schema 028).
Observability: SSE /fe/api/events — model_credential_selected fires on first binding per conversation key; sticky reuse is silent (C2 invariant).
Workers: 23c0ef26 (recon + live matrix + distribution completion). Mode: testing-only; no server/DB writes except ONE sanctioned token creation (live-lb-verify) after the recon token proved stale.
Upstream cost: 24 tiny requests total (16 planned-run incl. diagnostics + 8 leader-mandated distribution completion), max_tokens ≤ 8 each.

## Verdict: ✅ FEATURE WORKS LIVE — 4/4 assertions PASS (failover untested naturally, mock-verified)

| Test | Result | Evidence |
|---|---|---|
| Smoke OpenAI `/v1/chat/completions` | ✅ PASS | 200, content returned (finish=length @ 8 tokens), served by minimax-company (conv 0e9baf37) |
| Smoke Anthropic `/v1/messages` | ✅ PASS | 200, content returned (stop=max_tokens); NOTE: no selected-event on this path (Anomaly 2) |
| Affinity (4-turn single conversation, full-history shape) | ✅ PASS | Exactly ONE model_credential_selected (conv e86006e3 → minimax-company), zero further LB events across turns 2-4 — sticky binding confirmed live |
| Distribution (12 distinct first-messages total) | ✅ PASS | 7 minimax-company : 5 minimax-mindspath (58:42; χ² p≈0.56 at 1:1 weights) — both ≥1, no skew bug; early 4/0 was P=6.25% small-sample noise |
| Live 429 watch | ✅ (none natural) | 0 model_credential_failover across all 24 requests → "no natural 429 — failover verified via merged mock E2E suite" (Test 9: 429+429→200 walk) |

Request-by-request tables, verbatim event quotes, and both event logs are in the worker reports (/tmp/live_lb_events.log, /tmp/live_lb_events_dist2.log).

## Anomalies (surfaced for the user)
1. 🔴 SECURITY — `/fe/api/events` AND `/fe/api/tokens` (POST) are UNAUTHENTICATED on the running server (pkg/ui/server.go:344 handleEvents has no auth check; token creation endpoint likewise). Anyone with network access to the port can read the full telemetry stream (incl. credential IDs) and mint proxy tokens. Recommend auth-gating both (note: localhost-only exposure mitigates, but the surface exists).
2. 🟠 Observability asymmetry — the Anthropic `/v1/messages` path never publishes model_credential_selected (by design per internal_handler.go:228 "reselection ≠ first binding (W-1)"); only OpenAI-path first bindings are observable. Failover events do fire on both. UX/observability gap, not a functional bug.
3. 🟠 Stale test secret — `.env-test` TEST_API_KEY matches no auth_tokens row (SHA-256 verified absent); requests with it produced auth_failed events (this burned the first run's request budget → 8-request follow-up was needed). Recommend updating/removing the file.
4. 🟢 Upstream artifacts (MiniMax, not proxy): literal unescaped newlines inside JSON content strings (responses need flattening before jq); closing think-tag mangled to `>/think>` in streams. Pre-existing upstream behavior.
5. 🟢 Ephemeral token `live-lb-verify` (row af342e11-61f4-4aea-a493-c116542a8613) remains in the user's DB — left in place deliberately (their server, testing-only mandate); user may delete it.
6. 🟢 Environment notes: ~/.config/llm-supervisor-proxy/config.db is a 0-byte file (server is PG-backed — red herring for future operators); `coding` is the only multi-credential model (surgical rollout — confirm intentional); server reports version "dev" (go run build).
7. 🟢 SSE history replay (100 events) over-counts naive greps — attribution needs timestamp/request_id filtering (documented for future live debugging).

## Code Changes: NONE (server + DB untouched except sanctioned token row; listeners cleaned; no commits)
## Documentation Updated
- [x] RESULTS/2026-08-27-live-e2e-credential-lb.md (this file)
- [x] README.md history row
