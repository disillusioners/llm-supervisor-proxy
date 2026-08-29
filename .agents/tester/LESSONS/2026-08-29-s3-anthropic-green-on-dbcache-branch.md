# 2026-08-29 — anthropic_thinking_leak S3 is GREEN on feature/db-cache-layer (stale PARTIAL status superseded)

## State change
The 2026-08-28 rsd merge gate recorded this suite as **PARTIAL** (S3 = internal-Anthropic NON-stream persistence empty, first-bad e717be3 — see 2026-08-28-rsd-s3-nonstream-persistence-bug.md).

At `b48a14f` on `feature/db-cache-layer` (base 42d98f3 + test commits c9559cf/b48a14f), the full suite passes **4/4 under `-race`**: S1A, S1B, S2, and **S3** (persisted thinking 51 bytes exact; persisted content present).

## Root cause of the flip
The S3 production fix landed in this branch's ancestry: `64da4ae` (capture content/thinking/tool_calls for live non-stream internal-Anthropic) + `e60de91` (route live non-stream through TranslateNonStreamResponse) — the "fix direction (a)/(b)" named in the 2026-08-28 LESSONS. The S3 test was re-based at 22e76d6 and its assertions were intact — the fix made them pass.

## Implication
Treat `test/e2e_anthropic_thinking_leak` as **expected-green** on this branch and forward. The "Anthropic reqLog.Usage gap" baseline-debt note in project context refers to a different (reqLog.Usage) gap, not this one. Historical note below supersedes cleanly.

Run evidence: `timeout 300 go test -race -count=1 -timeout 280s -v ./test/e2e_anthropic_thinking_leak/` → ok 1.798s, log /tmp/race_e2e_anthropic.log (dbcache gate, worker race-e2e-anthropic).
