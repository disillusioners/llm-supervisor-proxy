# 2026-08-21 — Reasoning Observability E2E Verification (db7aca0): Run Patterns & Gotchas

## Context
Closure verification of `fix/ui-reasoning-observability` @ db7aca0 (original symptom: glm-5.3 reasoning_content invisible in FE API/Web UI). All PASS. See RESULTS/2026-08-21-reasoning-observability-e2e-verification.md.

## Key facts established (reusable)
1. **pkg/ui/server.go was never broken.** The FE API (`GET /fe/api/requests/{id}`, server.go:140,230-246) always serialized the full RequestLog; `thinking` sat at `messages[i].thinking` (store/memory.go:19-24, `omitempty`). The bug was 100% capture-side. Verify WHERE a symptom lives before assuming the render/API layer.
2. **FE API is unauthenticated** — `/fe/api/*` has no middleware (only /v1/* client endpoints need Bearer). Harnesses can mount `ui.NewServer`+`RegisterHandlers` on httptest and GET freely. List endpoint `/fe/api/requests` is newest-first.
3. **RequestLog is in-memory only** (ring of 100, `store.NewRequestStore`). No DB column/migration for thinking. E2E assertions must run in-process (keep the reqStore handle or share it with the mounted UI server); restart wipes evidence.
4. **Ultimate-external MiniMax translation gate keys on the MODEL's CredentialID** (not the env upstream cred) — gotcha that bit the M2 matrix row; match per-model credential when exercising the flag-ON path (cf. minimax harness S9).
5. **Anthropic external path DELIBERATELY emits thinking_delta to the client** (TestAnthropic_ThinkingStream); the leak constraint applies to the INTERNAL path (thinking-sink swallows; wire stays silent). Assert both sides: no wire bytes AND non-empty persisted thinking.
6. **Live-smoke fallback safety:** a model's `fallback_chain` can reference a MiniMax-credentialed model; guard with `APPLY_ENV_OVERRIDES=1 RACE_PARALLEL_ON_IDLE=false` and verify `upstream_requests: {fallback: not_started}` post-call when MiniMax budget must be preserved.
7. `GET /fe/api/models/{id}` rejects GET (405 — only PUT/DELETE); read model facts from the LIST payload.
8. Git worktrees (`git worktree add /tmp/... fea5874`) cleanly base-classify failures without dirtying the branch; also used for mutation-proofs (re-inject bug → test fails → discard worktree).

## Process notes
- One worker died at instance level (ultimate_model_retry_exhausted pattern) mid-pack; revive was rejected (terminated) → ONE replacement per policy completed the pack. Do not loop.
- Markdown tables eating shell metacharacters is a real test-integrity risk: the `\|` regex vacuous pass (LESSONS/2026-08-21-interleaved-matrix-regex-vacuous-pass.md) survived two prior "green" records because exit-0 with zero tests looks like success. Expected-count guards (`34 test funcs`, `[no tests to run]` check) now baked into PACKS.md.
