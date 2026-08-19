# Mock Test P3-4 Findings: bash heredoc gotchas + race-internal reasoning_details bug

**Date**: 2026-08-19  
**Harness**: `test/test_mock_minimax_reasoning.sh` (P3-4)  
**Mock server**: `test/mock_llm_minimax_reasoning.go`  
**Outcome**: harness executed; 47/53 PASS; 6 FAIL — all in T3b due to a genuine
product bug on race-internal path. Bug also documents in
`.agents/tester/RESULTS/2026-08-19-minimax-reasoning-details-p3-4.md`.

---

## Finding 1 — bash heredocs cannot have content on the terminator line

**Symptom**: `bash -n` reports `syntax error: unexpected end of file` on a
function whose body is a heredoc.

**Wrong** (silently broken):
```bash
capture_count() {
    python3 - <<PY 2>/dev/null
import sys
print(sys.argv[1])
PY "$1" "$CAPTURE_FILE"  # ← "$1" and "$CAPTURE_FILE" are NOT args to python3
}
```

Bash parses `PY` as the heredoc terminator, then tries to interpret `"$1"`
as further heredoc content (not as command arguments) — the heredoc never
terminates → "unexpected end of file".

**Right** (use env vars):
```bash
capture_count() {
    MODE="$1" FP="$CAPTURE_FILE" python3 - <<'PY' 2>/dev/null
import os
mode = os.environ.get("MODE", "")
fp = os.environ.get("FP", "")
print(mode, fp)
PY
}
```

**Right** (also works, more verbose): put args before the heredoc and read
the body from a named fd or temp file.

**Lesson**: any future mock-helper that needs to pass args to a heredoc-fed
script must go through env vars. Document this in the harness README so the
next test author doesn't trip over the same gotcha.

---

## Finding 2 — Go's `json.Marshal` `omitempty` drops empty slices (not just nil)

**Symptom**: on race-internal path, an assistant message that should carry
`reasoning_details: [{...}]` is sent upstream with **no** `reasoning_details`
field at all (verified via captured request body — T3b failure).

**Root cause** (product bug, do not fix here per task constraint):
- `pkg/proxy/translator/minimax.go:133-141` writes
  `msg["reasoning_details"] = []any{ReasoningDetail{...}}` (struct values).
- `pkg/proxy/race_executor.go:180` then calls `convertToProviderRequest`
  which calls `providers.HydrateReasoningDetails(msg)`.
- `pkg/providers/openai.go:839-864` does `rd.(map[string]interface{})`
  which **fails silently on struct values** — the loop `continue`s without
  adding anything, returning `[]ReasoningDetailEntry{}` (empty, length 0).
- `OpenAIProvider.ChatCompletion` (`pkg/providers/openai.go:80`) calls
  `json.Marshal(req)`; the `omitempty` tag on `ChatMessage.ReasoningDetails`
  drops empty slices as well as nil.

**Verification (standalone)**:
```go
m := Msg{Role: "assistant", Det: []RDEntry{}}  // empty slice
json.Marshal(m)  // → {"role":"assistant"}  (Det dropped!)
```

**Lesson**: when a translator mutates a map with typed struct values, the
downstream `Hydrate*` helper must either accept structs (reflection /
type-switch) or the translator must write map values (`map[string]any`).
Race-external is unaffected because `executeExternalRequest` re-marshals
`bodyMap` directly via `json.Marshal(m)` which encodes struct values via
their own json tags — only the typed map→struct→marshal pipeline loses the
data.

---

## Finding 3 — Go marshaling adds spaces after colons

**Symptom**: grep pattern `"reasoning_split":true` (no space) does not match
captured JSON `"reasoning_split": true` (with space).

**Fix**: assertion patterns must match Go's default `json.Marshal` output:
`"key": value` (space after colon). This is universal across all Go-encoded
JSON in this project. Quick-fix the assertion pattern, not the marshaling.

---

## Finding 4 — Mock mode extractor needs prefix-match, not token-equals

**Symptom**: T3 and T3b both use `MODE-NS-DETAILS` marker but want distinct
captured bodies. Original `extractMode` did `strings.Fields` and exact-equals,
so `"hi MODE-NS-DETAILS-3msg"` was rejected (token doesn't equal mode).

**Fix**: in mock `extractMode`, switch to `strings.Contains(content, m)`
against a known-mode list ordered longest-first. Tolerates trailing suffixes
(`MODE-NS-DETAILS-3msg` matches `MODE-NS-DETAILS`). Avoids inflating the
mock's mode set with redundant aliases.

**Lesson**: marker extraction should be tolerant of trailing suffix text;
clients commonly decorate mode markers with run-id, scenario-id, etc.

---

## Finding 5 — T15 ultimate-path trigger mechanics validated

The duplicate-hash-retry pattern from `test_mock_ultimate_model.sh` works as
documented: first call with `MODE-ERROR-500` content fails → hash created →
second identical call triggers ultimate model. With
`ULTIMATE_MODEL_MAX_RETRIES=2` and `ULTIMATE_MODEL_MAX_HASH=100`, the second
call lands on the ultimate model (which also receives the same error since
the mock returns 500 for both primary and ultimate URLs). Both requests show
`reasoning_split: true` in the captured body — ultimate-path translation IS
wired (the bug from Finding 2 also affects ultimate-internal but ultimate-
external is unaffected; with the mock as ultimate's internal_base_url, the
ultimate path used here is ultimate-internal, so this also exercises the
bug path — which means the captured ultimate body also has missing
`reasoning_details`. The harness only asserted `reasoning_split` presence
on T15, so the bug is not double-counted in T15; the T3b evidence is the
authoritative repro.)

---

## Action items

- [ ] Fix product bug Finding 2 (out of scope for P3-4 — flag for P3-5 or
      follow-up PR): translator should write `[]map[string]any` (or
      `HydrateReasoningDetails` should accept structs).
- [ ] Update P3-5 (Go E2E suite, `test/e2e_minimax_reasoning/`) to assert
      both paths — if P3-5 covers race-internal, the suite will surface the
      same Finding 2 bug and document it as the source of truth.
- [ ] Consider a `pkg/proxy/translator/minimax_test.go` round-trip case that
      catches this in CI (in-place translation followed by typed hydration
      followed by marshal).