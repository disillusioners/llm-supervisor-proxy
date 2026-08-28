# Test Report: gzip request-body support — feature verification (original scenario, end-to-end)

Date: 2026-08-28
Branch/HEAD: feature/gzip-request-support @ 7a9ecff (commits 666cbb3 + 7a9ecff)
Baseline reference: full-sweep green @ 22e76d6 (rsd merge gate)
Mode: REPORT-ONLY — no fixes, no source modifications, no commits

Worker instances: infra-reg-gzip (4ef568ef), infra-e2e-gzip (250df207), run-build-gate (03d7294f),
run-gzipmw (684e81c9), run-reg-proxy (f26228c7), run-reg-ultimate (5cb7e6bb), run-reg-token (77a24457),
run-reg-reasoning (fb568186), run-reg-store (e97c07c1), run-reg-models (9dbfb9b7), run-reg-auth (6e3f04fd),
run-reg-mcp (dd751383), run-reg-misc (3b189490), run-reg-translator (8b44497e), run-reg-gap (450f1077),
run-reg-testroot (5eaea2b0), run-reg-toolrepair (ba2abfb1), run-reg-loop (faea307c), run-e2e-gzip (3650ec87),
run-reg-e2edirs (7360d5a8), run-reg-remainder (b165e4e7)

## Summary

- **Overall: ✅ READY — feature verified end-to-end; zero regressions.**
- Repo-wide regression: **37/37 packages green** (32 test-bearing PASS + 5 [no test files] PASS; `go build ./...` + `go vet ./...` clean).
- Original-scenario E2E (real binary + stub upstream): **6/6 scenarios PASS**.
- ensure.md (Go scope): Critical 3/3 (all Go unit tests pass; vet clean; build clean). Frontend gate intentionally not run — no frontend change; 30 standing tsc errors are documented baseline debt (ensure.md TS wording known-unmeetable).
- Quick fixes applied: **0 product fixes** (per task contract). 3 harness-only fixes inside the new E2E script during its first execution.
- Quarantined: 0 new (store CloseLifecycle quarantine respected — did not fire).

## Scope Decision

Full repo-wide sweep WAS warranted and mandated by the task (middleware wraps the entire mux in cmd/main.go → every route is in the blast radius). Executed as 16 registered unit/gate packs + 2 coverage-closure packs + 1 new E2E mock pack, all parallel-batched, every pack dual-layer timeout (120s internal / `timeout 300` outer; closure packs 240s/300s).

## 1. Repo-wide regression (mandate item 1)

Build gate: `go build ./...` PASS (1.61s warm) + `go vet ./...` PASS — via new pack `build_gate_test` (FIRST run, registered).

| Pack | Result | Evidence (all @ 7a9ecff) |
|------|--------|--------------------------|
| build_gate_test | PASS | build + vet clean, 1s |
| gzipmw_unit_test | PASS | 21 funcs + 18 subtests (FIRST run; new package) |
| proxy_unit_test | PASS | 455 top + 475 sub, 0 fail, 7 branch-gated skips — incl. 5 NEW handler_gzip_test.go tests |
| ultimatemodel_unit_test | PASS | 155 top — incl. 3 NEW handler_external_gzip_test.go tests |
| store_unit_test | PASS | 99 + 4 PG-skips (expected, no TEST_DATABASE_URL) |
| models_unit_test | PASS | 87 + 267 sub |
| auth_unit_test | PASS | 48 + 39 sub = 87 |
| mcp_unit_test | PASS | 245 + 471 entries, 3 env-conditional SSRF skips (expected) |
| misc_unit_test | PASS | 403 across 9 pkgs (config/crypto/events/bufferstore/providers/supervisor/toolcall/ui/usage) |
| translator_unit_test | PASS | 169 entries (123 funcs + 46 sub) |
| gap_unit_test | PASS | 274 entries / 118 funcs (credentiallb/proxyheader/normalizers/fingerprint/store) |
| testroot_unit_test | PASS | 43 entries (35 funcs + 8 sub) |
| toolrepair_unit_test | PASS | 17 / 105 |
| loopdetection_unit_test | PASS | 33/33 |
| token_unit_test (inline) | PASS | ok 0.158s |
| reasoning_content_dir (inline) | PASS | 2 funcs / 14 sub |
| e2e-dir closure pack | PASS 4/4 | fe_reasoning_observability 21; minimax_reasoning 37; ultimate_internal_reasoning 1; anthropic_thinking_leak 4 (S3 PASSES — test recalibrated at fea5874; PACKS.md "S3 ❌" note is stale for this branch) |
| remainder closure pack | PASS 6/6 | test/e2e_reasoning_content 5+7 (baseline match); root, cmd, pkg/logger, scripts, pkg/store/database/db = [no test files] |

**Pre-existing-failure classification: NONE NEEDED — zero failures observed.** The one anticipated baseline fail (anthropic_thinking_leak S3, documented @ 22e76d6) now PASSES at 7a9ecff (test recalibrated at base fea5874 to treat the wire-translated thinking block as by-design current behavior).

Diff-base note (from recon): `22e76d6..HEAD` contains 32 non-gzip files — all real-streaming-default carry-over merged at 071ce1d BEFORE the gzip feature; gzip-only footprint is exactly the 5 expected files (666cbb3 + 7a9ecff). `cmd/main.go` is double-touched (RSD wiring + gzip wrap); the gzip wrap's presence is proven behaviorally by the E2E (feature active through the real binary).

## 2. Original scenario, live end-to-end (mandate item 2)

Pack: `test/mock_gzip_request_decompression.sh` (NEW, 849 lines; real binary, SQLite, stub upstream recording exact bytes; ports 10140/10141; 240s internal / `timeout 300` outer; ~24s actual).

| Scenario | Verdict | Evidence |
|----------|---------|----------|
| a — /v1/chat/completions identity | ✅ PASS | 200/200; client bodies byte-identical (276 B cmp-ok); upstream bodies byte-identical (411 B cmp-ok); stub saw NO Content-Encoding on either request; client response carries NO Content-Encoding (no response compression) |
| b — /v1/messages (Anthropic) identity | ✅ PASS | 200/200; upstream bodies byte-identical (220 B cmp-ok, Anthropic→OpenAI translation); stub saw NO Content-Encoding; client response uncompressed. Client-side bodies differ ONLY in the per-request Anthropic `msg_` id (inherent to protocol, identical for two uncompressed requests) — informational, not gzip-induced |
| c — gzip body WITHOUT header | ✅ PASS | 400 (4xx) from JSON parsing; upstream call count unchanged (feature NOT triggered) |
| d — corrupt gzip + header | ✅ PASS | 400 (envelope code=invalid_gzip_body); upstream call count unchanged (rejected before handler/upstream) |
| e — 150 MiB zip bomb + header | ✅ PASS | **413** (actual = required; envelope code=request_body_too_large; cap = 100 MiB per gzip.go); resolved in ~0s; post-bomb healthz 200 + control request 200 (server alive) |
| f — passthrough untouched | ✅ PASS | /healthz 200; /fe/api/config GET 200; /fe/api/events SSE delivered 4383 B within budget (config.updated event triggered; heartbeat cadence in code is 30s — spec's 15s figure was stale) |

Cleanup verified: ports 10140/10141 free, no orphan processes, port 8088 untouched.

## Environment notes

- DATABASE_URL unset throughout (SQLite path — the tested default). 4 PostgreSQL store tests + 3 MCP SSRF probes skip by design without it.
- macOS gzip ≥1.13 refuses process-substitution inputs (`/dev/fd/63 is not a regular file`, CVE-2022-1271 hardening) — harness fixed to write regular files (see LESSONS).
- Middleware facts confirmed from source: MaxDecompressedBytes = 100 MiB → 413; corrupt → 400 invalid_gzip_body; non-gzip encoding (deflate) → 415; SSE heartbeat = 30s (pkg/ui/server.go:366).

## Artifacts & uncommitted state (report-only run — nothing committed, per task contract)

- NEW untracked: test/packs/gzipmw_unit_test.sh, test/packs/build_gate_test.sh, test/mock_gzip_request_decompression.sh
- Modified (docs): .agents/tester/{PACKS,MOCK_TESTS,README}.md, this RESULTS file
- Recommendation: commit the three test scripts + docs with the feature merge.

## Documentation Updated

- [x] PACKS.md — 2 new packs registered + statuses; sweep results stamped @ 7a9ecff gzip gate
- [x] MOCK_TESTS.md — gzip E2E spec + last-run result
- [x] README.md — history row
- [x] LESSONS/2026-08-28-gzip-e2e-harness-lessons.md
- [x] RESULTS/2026-08-28-gzip-request-support-verification.md (this file)

## Overall Status

- Repo regression: ✅ PASS (37/37 packages)
- E2E original scenario: ✅ PASS (6/6)
- **Testing Complete: ✅ READY** — feature behaves exactly per requirement; no fixes attempted (report-only mandate).
