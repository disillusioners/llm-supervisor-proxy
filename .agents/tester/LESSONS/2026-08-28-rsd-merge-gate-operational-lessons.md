# Merge-gate operational lessons — real-streaming-default (2026-08-28)

## 1. openai_internal_buffered shell mock is broken by credential-LB schema (pre-existing)
- Setup-phase failure at test/test_mock_openai_internal_buffered.sh:160-162: legacy `credential_id` payload rejected — `pkg/ui/server.go:436` now requires `credentials: []` for internal:true models.
- Last PASS was db7aca0+ (pre credential-LB merge). Broken at rsd base 9842c77 → NOT a real-streaming-default regression.
- Fix (needs-follow-up, ~3 lines): send `"credentials":[{"credential_id":"...","weight":1,"position":0}]`; ALSO add `X-LLMProxy-Buffer-Response: true` to keep testing buffered mode post-feature (script pre-dates the header).
- Lesson: after any schema-breaking merge, re-run registered shell mocks BEFORE the next branch's gate; PACKS.md PASS claims can silently rot.

## 2. TTFB measurement on this proxy: first `data:` line, not first body byte
- Proxy emits `: connected\n\n` SSE preamble immediately in BOTH modes (pkg/proxy/handler.go:897) and `: keepalive` comments during live streams.
- Naive first-body-byte TTFB cannot distinguish live vs buffered (~0ms both). Measure time-to-first-`data:` line. Also: live raw streams contain keepalive comment bytes → byte-compare only assembled content across modes, expect comment-level diffs (informational, not a parity violation).

## 3. Buffered-mode parity proves pre-existing-ness
- To classify a record/persistence gap as pre-existing vs feature-introduced: run the SAME scenario WITH `X-LLMProxy-Buffer-Response: true` (byte-for-byte pre-feature behavior). Same gap in buffered mode ⇒ pre-existing. Used to clear the Anthropic `reqLog.Usage` gap (usage=null in both modes) and to convict S3 (empty only in live mode ⇒ feature-introduced at e717be3).

## 4. Pack-script output-file collision + trap cleanup
- Pack scripts write + `rm -f /tmp/${PACK_NAME}_output.txt` on EXIT. Workers must tee to a DIFFERENT path (e.g. /tmp/<pack>_full.txt); redirecting the outer wrapper to the same path caused a false outer-timeout appearance (ultimatemodel worker) and lost captures elsewhere. Consider having the pack author rename the internal file or stop trap-deleting it (follow-up).

## 5. Worker completion-report delivery glitch → verify-or-complete replacement
- rsd-mock-m1 worker "completed" twice delivering only a preamble line (10+ min of real activity; artifact later proved absent first time). Recovery: (1) revive message demanding the structured report; (2) on second preamble, spawn a replacement with "STEP 0: check for prior attempt's artifact + reap stale processes on ITS ports only, then reuse-or-rebuild". Replacement succeeded cleanly. Rule of thumb: never trust a bare-preamble completion; convert to artifact-verification work.

## 6. Minimax shell mock near-cap was a cold-build artifact
- 2026-08-27 run consumed the full 120s internal alarm; 2026-08-28 run finished in 11s (warm cache). Before splitting a "slow" shell pack, re-run warm — `go run` cold compiles dominate.

## 7. Coverage gaps closed this gate (permanent)
- New packs: translator_unit_test (pkg/proxy/translator — had the NEW incremental_stream.go with zero pack coverage), gap_unit_test (credentiallb, proxyheader, proxy/normalizers, loopdetection/fingerprint, store parent), testroot_unit_test (test/ root). Registered inline: test/reasoning_content. All green; PACKS.md updated.
- Open follow-up: fe_reasoning_observability lacks an anthropic-internal NON-STREAM row — the exact cell where the S3 bug hid. Add post-fix.

## 8. Worktree bisect is cheap and safe for this repo
- `git worktree add --detach /tmp/<name> <sha>` + single-test `-run` executions (0.03s each) bisected a 12-commit window in minutes without touching the main checkout. Cleanup: `git worktree remove --force` + `git worktree list` verification. Recommended default for future first-bad-commit investigations.
