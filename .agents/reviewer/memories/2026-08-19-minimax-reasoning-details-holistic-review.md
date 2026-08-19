# 2026-08-19 — minimax-reasoning-details holistic diff review (Deep-Review council)

Diff: ac877ea..3898547 (16 commits, ~6.1k ins). Council (2 models) converged; verdict ISSUES.

## Binding decisions — all honored
D1 `ReasoningSplit *bool` omitempty (false never serialized) ✅; D2 data-driven extraction, no provider gate in shared parser ✅; D3 credential-derived gates, case-insensitive, empty-CredentialID⇒false, all 3 gate sites ✅; D4 narrow strip (EqualFold) on exactly 2 external paths ✅; R3 boundary (no providers→translator import; local ReasoningDetailEntry) ✅. Gated-off path structurally no-op on all 4 paths — gates short-circuit before parse/marshal. Drift trap AST test genuinely real (narrow scope caveat).

## Critical (must fix)
- **C1**: gate-ON external streams emit invalid SSE — `ChunkBytes` strips `data: ` prefix and never restores; passthrough re-marshal also drops trailing `\n` on ultim-ext and alphabetizes keys. Both call sites re-frame only emitted chunks. `minimax_stream.go:425-436,632-639`; sites `race_executor.go:1342-1379`, `handler_external.go:344-377`.
- **C2**: test suites structurally blind to C1 (no SSE-framing assertion, all site tests use interleaved=true); flag-absent+MiniMax quadrant has zero dedicated tests; `TestRaceExternal_NegativeCase_ByteIdentical_NonMiniMax` never calls bytes.Equal (misleading name; diff due to pre-existing model-override re-marshal).

## Warnings
W1 `entryDebugSampling` unsynchronized counter (race) → atomic; W2 false byte-equality godoc on `marshalChunkForPassthrough`; W3 choices[0]-only processing leaks reasoning_details on n>1 (test PINS the leak) → loop all choices; W4 internal stream extraction lacks cross-chunk dedup state (cumulative mode double-emits; single-winner diverges from external path); W5 ultimate-internal request side lacks reasoning_content→reasoning_details translation (race-internal has it); W6 containment-dedup edge cases violate single-winner (empty-out on equal text; request-side overwrite is details-LOSES); W7 drift WARN nearly unfireable (dedup counter gates log); W8 non-stream strips `name`/`audio` unconditionally when gate ON; W9 gofmt failures in 7 files.

## Lessons
- Per-phase green + all-assertions-pass can hide wire-format breakage: positive-path tests must assert raw bytes/SSE framing, not field presence.
- A test naming a contract ("byte-identical") must actually enforce bytes.Equal.
- "Byte-identical" godoc claims deserve independent verification — false doc was the root cause enabler of C1.
# 2026-08-19 — minimax-reasoning-details fix-round re-review (council r2, 3898547..8f2217e)

Verdict: NOTES (mergeable). C1/C2 verified genuinely fixed by both councilors — byte-level traces: unchanged chunks verbatim (prefix+newline), mutated+emitted uniformly framed by translator, marshalChunkForPassthrough deleted, tests replayed mentally against pre-fix behavior and would have caught C1. W1-W9 all resolved. Round-1 invariants intact (D1-D4, R3, structural no-op, retry idempotence).

Residual findings (0🔴/4🟡/6🟢):
- 🟡 openai.go:447,489 — `len(emittedPerChoice)>0` gate: all-entries-skipped details + reasoning_content diverges from external single-winner. Fix: hasDetails-style flag.
- 🟡 minimax_stream.go:393-400,422 — stale ChunkBytes godoc still says "no data: prefix" (pre-fix contract) → double-framing trap for future callers. Fix before merge (cheap).
- 🟡 openai.go:670 — dead `extractReasoningDetailsFromRawData` (test-only life) vs untested live replacement :833 → drift. Fix: delete + port tests.
- 🟡 handler_internal.go:88-112 — inline typed entry sidesteps AST drift trap; ID scheme diverges from race twin (counter vs i+1).
- 🟢 misnamed prefix test (asserts preserve), stale entryDebugSampling comments, mixed \n\n\n terminator (SSE-legal, document), dead delta-copy block in buildReasoningContentChunk, choice-index default-0 keying edge.

Residual unverified: live MiniMax upstream (mock-only), no UTF-8 round-trip test, drift-trap test not directly run by councilors.

Lesson: round-2 confirmation bias risk — councilor "coding" took claimed fixes at higher trust; "agentic" re-derived. Fix-verification prompts must demand mental replay of pre-fix behavior (worked here: C2 tests validated by would-it-have-caught-C1). Doc rot follows code fixes: godoc/comment claims from the old contract (C1 enabler in round 1!) again found stale — always re-check doc claims after behavior changes.
# 2026-08-19 — delta review 068317c (T3b) + b2dfde0 (S3) — APPROVED

0🔴/2🟡/3🟢. (a) Byte-identity intact: gate-off untouched (gate short-circuits pre-hydration); flag-on all 4 paths converge on struct-declaration key order (type,id,format,(index),text) — the rejected map-mode candidate would have broken this (map marshal sorts keys); zero-index omitempty symmetric across paths. (b) S3 correct: per-request local counters both sides (no shared state); all 4 paths identical id sequences; no new imports. (c) Both fixes pinned by tests that fail pre-fix: unit TestHydrateReasoningDetails_AcceptsTranslatorStruct + e2e TestS3 captured-upstream-body asserts; S3 parity via shared-expectation helper applied to both internal paths.

Follow-ups (non-blocking): 🟡 index-type round-trip hole — struct case writes "index": int but extractor asserts float64 → non-zero Index silently drops key (unreachable today, translator emits Index:0); fix float64(typed.Index). 🟡 R3 doc drift — 068317c's `case translator.ReasoningDetail:` is a type reference in providers, contradicting documented carve-outs (constants→helpers→type quietly grew); sanction or convert at wiring site. 🟢 pointer form unhandled in switch (unreachable); drift trap still sidestepped by inline ReasoningDetailEntry (e2e parity pin mitigates; no unit-level id assert in ultimatemodel); shell assert_not_contains '"index"' over-broad.

Lesson: struct-vs-map emission choice at a hydrate boundary is a cross-path byte-identity decision — the determining factor is which marshal (struct decl-order vs map sorted) the FINAL wire writer uses on each path; verify convergence there, not at the emission site. Also: Go json numbers round-trip as float64 in map-land but stay int when inserted directly — type-assert mismatch is the classic silent-drop hole at map/struct boundaries.
