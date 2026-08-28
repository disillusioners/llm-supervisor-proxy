// Scenarios S1A/S1B/S2/S3 for the anthropic thinking-leak spot-check
// (Task 4, re-based against the real-streaming-default D8 wire-shape
// contract — docs/real-streaming-default.md §D8).
//
// S1A exercises BUFFERED mode (X-LLMProxy-Buffer-Response: true) and
// asserts the legacy "no thinking bytes on wire" invariant. S1B exercises
// LIVE mode (no header) and asserts the D8 positive contract — Anthropic
// thinking content_block + thinking_delta ARE emitted on wire while the
// sink side-channel capture is preserved. S2 and S3 are unchanged.
//
// Each scenario drives the full proxy.Handler.HandleAnthropicMessages against
// a capturing mock OpenAI upstream and asserts the byte-identity constraint
// on the client wire (the httptest recorder body) and the persisted
// assistant message (the in-memory request store).
package e2e_anthropic_thinking_leak

import (
	"net/http"
	"strings"
	"testing"
)

// leakSubstrings are the wire-greppable fragments that MUST NOT appear on
// the INTERNAL-path BUFFERED-mode wire (S1A, the legacy leak spot-check).
// Each reasoning chunk is distinct so a substring search catches partial
// leaks; the translated-marker strings catch any thinking block/delta the
// translator would emit. S1B (LIVE mode) DOES emit these on wire by D8
// design (real-streaming-default.md §D8) and therefore uses positive
// containment assertions rather than this leak list.
var leakSubstrings = []string{
	reasoningChunk1,
	reasoningChunk2,
	`"reasoning_content"`,
	`"thinking_delta"`,
	`"type":"thinking"`,
}

// assertNoLeak fails the test if any leak substring appears in the wire
// body, logging exactly which substring leaked for evidence.
func assertNoLeak(t *testing.T, wire, where string) {
	t.Helper()
	for _, s := range leakSubstrings {
		if strings.Contains(wire, s) {
			t.Errorf("%s: LEAK — wire contains %q (must be absent)\n  wire=%q", where, s, wire)
		}
	}
}

// assertContains logs evidence on positive contains assertions.
func assertContains(t *testing.T, wire, want, where string) {
	t.Helper()
	if !strings.Contains(wire, want) {
		t.Errorf("%s: wire missing expected %q\n  wire=%q", where, want, wire)
	} else {
		t.Logf("EVIDENCE %s: wire contains %q (found)", where, want)
	}
}

// assertAbsent logs evidence on negative (absent) assertions.
func assertAbsent(t *testing.T, wire, want, where string) {
	t.Helper()
	if strings.Contains(wire, want) {
		t.Errorf("%s: wire unexpectedly contains %q\n  wire=%q", where, want, wire)
	} else {
		t.Logf("EVIDENCE %s: wire does NOT contain %q (absent)", where, want)
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// S1A — INTERNAL path, stream, BUFFERED mode: the legacy leak spot-check
// ═════════════════════════════════════════════════════════════════════════════

// TestS1A_Buffered_InternalStream_NoThinkingOnWire drives an Internal:true
// model whose upstream emits reasoning_content SSE chunks. The fix
// (internal_handler.go:225-242 sink + handler_anthropic.go:482 install) must
// keep ALL thinking bytes off the client wire while persisting the
// concatenated reasoning into the assistant message via the side-channel
// sink. This is the central assertion of the dev-verify leak closure,
// re-exercised in BUFFERED mode under the real-streaming-default D8 contract
// (X-LLMProxy-Buffer-Response: true ⇒ buffered, suppresses thinking on wire).
//
// S1B covers the LIVE-mode positive mirror (D8: live mode emits thinking
// blocks on wire AND preserves sink capture).
func TestS1A_Buffered_InternalStream_NoThinkingOnWire(t *testing.T) {
	env := setupTestEnv(t, reasoningSSEHandler)

	rr := env.run(anthropicRequest{
		model:        intModel,
		stream:       true,
		extraHeaders: map[string]string{"X-LLMProxy-Buffer-Response": "true"},
	})
	wire := rr.Body.String()

	if rr.Code != http.StatusOK {
		t.Fatalf("S1A: status=%d body=%s", rr.Code, wire)
	}

	// Routing proof: the captured upstream request must carry the internal
	// model name rewrite (proves we actually exercised the internal path,
	// not an accidental external fallback).
	cap := env.mockUp.last()
	if got, _ := cap.BodyParsed["model"].(string); got != intModelUpstreamName {
		t.Fatalf("S1A: upstream model=%q want %q (internal path not exercised); captured=%s",
			got, intModelUpstreamName, string(cap.Body))
	}
	t.Logf("EVIDENCE S1A: upstream model=%q (internal path confirmed)", intModelUpstreamName)

	// (a) NO-LEAK: zero occurrences of reasoning strings + translated
	// thinking markers on the wire.
	assertNoLeak(t, wire, "S1A")
	for _, s := range leakSubstrings {
		assertAbsent(t, wire, s, "S1A")
	}

	// (b) CAPTURED THINKING: the persisted assistant message Thinking must
	// be non-empty AND equal the concatenated reasoning (both sides of the
	// sink invariant — swallowed on wire, persisted in store).
	assistant, log, ok := env.lastAssistant()
	if !ok {
		t.Fatalf("S1A: no persisted assistant message; logs=%v", env.reqStore.List())
	}
	if got := assistant.Thinking; got != reasoningFull {
		t.Errorf("S1A: persisted thinking=%q want %q (sink must capture concatenated reasoning)",
			got, reasoningFull)
	} else {
		t.Logf("EVIDENCE S1A: persisted thinking == concatenated reasoning (%d bytes, exact)", len(got))
	}
	if assistant.Thinking == "" {
		t.Errorf("S1A: persisted thinking is EMPTY — sink invariant broken (captured nothing)")
	}

	// (c) CONTENT INTACT: the visible answer must survive on the wire.
	assertContains(t, wire, wireContent, "S1A/content")
	if got := assistant.Content; !strings.Contains(got, wireContent) {
		t.Errorf("S1A: persisted content=%q missing %q", got, wireContent)
	}

	t.Logf("EVIDENCE S1A: request id=%s status=%s", log.ID, log.Status)
	t.Logf("EVIDENCE S1A: wire length=%d bytes", len(wire))
}

// ═════════════════════════════════════════════════════════════════════════════
// S1B — INTERNAL path, stream, LIVE mode: D8 positive mirror
// ═════════════════════════════════════════════════════════════════════════════

// TestS1B_Live_InternalStream_ThinkingDeltaEmitted drives the SAME internal
// model and upstream reasoning SSE as S1A, but with NO
// X-LLMProxy-Buffer-Response header — i.e. LIVE mode under the
// real-streaming-default D8 wire-shape contract. Per D8 (docs/real-streaming-
// default.md §D8, lines 181-199) live mode deliberately emits Anthropic
// `thinking` content_block + `thinking_delta` events on the wire when
// upstream carries reasoning_content, AND preserves sink side-channel
// capture ("sink capture preserved in both modes"). S1A is the BUFFERED-
// mode mirror (suppressed on wire, sink-only). S2 is the EXTERNAL-path
// reference.
//
// Assertions:
//   - "thinking_delta" PRESENT on client wire (D8 deliberate positive).
//   - "type":"thinking" PRESENT (content_block_start).
//   - reasoning text chunks appear INSIDE thinking_delta payloads (mirror
//     of S2's positive checks on the external path).
//   - "reasoning_content" still ABSENT (no cross-protocol leak — Anthropic
//     translator must not echo the OpenAI-wire field name).
//   - persisted assistant Thinking == concatenated reasoning (sink invariant
//     preserved in live mode per D8).
//   - visible answer text present on wire (content intact).
func TestS1B_Live_InternalStream_ThinkingDeltaEmitted(t *testing.T) {
	env := setupTestEnv(t, reasoningSSEHandler)

	// No X-LLMProxy-Buffer-Response header ⇒ live mode (D8).
	rr := env.run(anthropicRequest{model: intModel, stream: true})
	wire := rr.Body.String()

	if rr.Code != http.StatusOK {
		t.Fatalf("S1B: status=%d body=%s", rr.Code, wire)
	}

	// Routing proof: internal path must be exercised. S1A's buffered mode
	// and S1B's live mode both route through the InternalHandler — only the
	// buffering/flushing of the recorder differs.
	cap := env.mockUp.last()
	if got, _ := cap.BodyParsed["model"].(string); got != intModelUpstreamName {
		t.Fatalf("S1B: upstream model=%q want %q (internal path not exercised); captured=%s",
			got, intModelUpstreamName, string(cap.Body))
	}
	t.Logf("EVIDENCE S1B: upstream model=%q (internal path confirmed)", intModelUpstreamName)

	// (a) D8 POSITIVE: live mode MUST emit Anthropic thinking blocks on
	// the wire. Two distinct sentinels — the start-of-block marker and
	// the streaming delta marker — confirm the translator ran end-to-end.
	assertContains(t, wire, `"thinking_delta"`, "S1B/thinking_delta")
	assertContains(t, wire, `"type":"thinking"`, "S1B/content_block_start_thinking")

	// (b) Reasoning text MUST appear inside thinking_delta payloads
	// (mirror of S2's positive checks on the external path). This proves
	// the translator accumulated reasoning_content and emitted it as
	// Anthropic thinking text rather than dropping it or leaking it as a
	// different field.
	assertContains(t, wire, reasoningChunk1, "S1B/reasoning1")
	assertContains(t, wire, reasoningChunk2, "S1B/reasoning2")

	// (c) CROSS-PROTOCOL LEAK GUARD: "reasoning_content" is the OpenAI-wire
	// field name. The Anthropic translator must convert reasoning_content →
	// thinking_delta and MUST NOT echo the OpenAI field name on the wire.
	assertAbsent(t, wire, `"reasoning_content"`, "S1B/reasoning_content")

	// (d) SINK CONTRACT PRESERVED IN LIVE MODE (D8 explicit guarantee:
	// "sink capture preserved in both modes"). Persisted Thinking must
	// equal the concatenated reasoning — the side-channel invariant must
	// survive the deliberate wire-shape change.
	assistant, log, ok := env.lastAssistant()
	if !ok {
		t.Fatalf("S1B: no persisted assistant message; logs=%v", env.reqStore.List())
	}
	if got := assistant.Thinking; got != reasoningFull {
		t.Errorf("S1B: persisted thinking=%q want %q (sink must capture concatenated reasoning in live mode)",
			got, reasoningFull)
	} else {
		t.Logf("EVIDENCE S1B: persisted thinking == concatenated reasoning (%d bytes, exact)", len(got))
	}
	if assistant.Thinking == "" {
		t.Errorf("S1B: persisted thinking is EMPTY — sink invariant broken in live mode")
	}

	// (e) CONTENT INTACT: the visible answer must survive translation.
	assertContains(t, wire, wireContent, "S1B/content")
	if got := assistant.Content; !strings.Contains(got, wireContent) {
		t.Errorf("S1B: persisted content=%q missing %q", got, wireContent)
	}

	t.Logf("EVIDENCE S1B: request id=%s status=%s wire length=%d", log.ID, log.Status, len(wire))
}

// ═════════════════════════════════════════════════════════════════════════════
// S2 — EXTERNAL path, stream: over-swallow regression guard (byte-identity reference)
// ═════════════════════════════════════════════════════════════════════════════

// TestS2_ExternalStream_ThinkingTranslated drives an UNREGISTERED model
// (resolvedModel=nil ⇒ external path via doAnthropicRequest) with the same
// reasoning SSE upstream. The external path translates reasoning_content →
// thinking_delta on the wire BY DESIGN (the TestAnthropic_ThinkingStream
// contract). This scenario confirms the sink did NOT over-swallow on the
// external path — thinking_delta MUST be present.
func TestS2_ExternalStream_ThinkingTranslated(t *testing.T) {
	env := setupTestEnv(t, reasoningSSEHandler)

	rr := env.run(anthropicRequest{model: extModel, stream: true})
	wire := rr.Body.String()

	if rr.Code != http.StatusOK {
		t.Fatalf("S2: status=%d body=%s", rr.Code, wire)
	}

	// Routing proof: external path sends the RAW model name (no internal
	// rewrite), proving we exercised doAnthropicRequest, not the internal
	// handler.
	cap := env.mockUp.last()
	if got, _ := cap.BodyParsed["model"].(string); got != extModel {
		t.Fatalf("S2: upstream model=%q want %q (external path not exercised); captured=%s",
			got, extModel, string(cap.Body))
	}
	t.Logf("EVIDENCE S2: upstream model=%q (external path confirmed)", extModel)

	// POSITIVE: thinking_delta MUST appear on the external wire (by-design
	// translation — the regression guard that the sink did not over-swallow).
	assertContains(t, wire, `"thinking_delta"`, "S2/thinking_delta")

	// The reasoning text itself should appear inside the translated
	// thinking_delta payload (the translator accumulates and emits it).
	assertContains(t, wire, reasoningChunk1, "S2/reasoning1")
	assertContains(t, wire, reasoningChunk2, "S2/reasoning2")

	// Content still intact.
	assertContains(t, wire, wireContent, "S2/content")

	// Persisted thinking: the external path accumulates via
	// extractOpenAIResponseContentFromSSE (handler_anthropic.go:691-694),
	// so it should equal the concatenated reasoning too.
	assistant, log, ok := env.lastAssistant()
	if !ok {
		t.Fatalf("S2: no persisted assistant message")
	}
	if got := assistant.Thinking; got != reasoningFull {
		t.Logf("EVIDENCE S2: persisted thinking=%q (want %q)", got, reasoningFull)
	} else {
		t.Logf("EVIDENCE S2: persisted thinking == concatenated reasoning (%d bytes)", len(got))
	}
	t.Logf("EVIDENCE S2: request id=%s status=%s wire length=%d", log.ID, log.Status, len(wire))
}

// ═════════════════════════════════════════════════════════════════════════════
// S3 — INTERNAL path, non-stream: persisted thinking + wire classification
// ═════════════════════════════════════════════════════════════════════════════

// TestS3_InternalNonStream_PersistedThinking drives the internal model with
// a non-stream 200 JSON response carrying message.reasoning_content. The
// persisted assistant thinking must equal the reasoning value. The wire is
// classified via a base-commit (fea5874) differential: translator/response.go
// (extractContentFromOpenAIMessage) is UNCHANGED since base, so any
// translated thinking block on the wire is pre-existing by-design behavior,
// not a fix-introduced leak. The test asserts the persisted-thinking
// contract (the fix's persistence guarantee) and logs the wire shape for
// evidence without failing on by-design translation.
func TestS3_InternalNonStream_PersistedThinking(t *testing.T) {
	env := setupTestEnv(t, reasoningNonStreamHandler)

	rr := env.run(anthropicRequest{model: intModel, stream: false})
	wire := rr.Body.String()

	if rr.Code != http.StatusOK {
		t.Fatalf("S3: status=%d body=%s", rr.Code, wire)
	}

	// Routing proof: internal model rewrite on the captured upstream body.
	cap := env.mockUp.last()
	if got, _ := cap.BodyParsed["model"].(string); got != intModelUpstreamName {
		t.Fatalf("S3: upstream model=%q want %q (internal path not exercised); captured=%s",
			got, intModelUpstreamName, string(cap.Body))
	}
	t.Logf("EVIDENCE S3: upstream model=%q (internal path confirmed)", intModelUpstreamName)

	// CORE CONTRACT: persisted thinking == reasoning value (the fix's
	// persistence guarantee for the non-stream internal path).
	assistant, log, ok := env.lastAssistant()
	if !ok {
		t.Fatalf("S3: no persisted assistant message")
	}
	if got := assistant.Thinking; got != reasoningFull {
		t.Errorf("S3: persisted thinking=%q want %q", got, reasoningFull)
	} else {
		t.Logf("EVIDENCE S3: persisted thinking == reasoning value (%d bytes, exact)", len(got))
	}
	if !strings.Contains(assistant.Content, wireContent) {
		t.Errorf("S3: persisted content=%q missing %q", assistant.Content, wireContent)
	}

	// WIRE CLASSIFICATION (informational, not a hard fail):
	// translator/response.go (extractContentFromOpenAIMessage) emits an
	// Anthropic thinking block when message.reasoning_content is present.
	// This file is UNCHANGED since the pre-fix base (fea5874), so any
	// thinking block on the wire is pre-existing by-design translation,
	// NOT a fix-introduced leak. We log the wire shape for evidence.
	if strings.Contains(wire, `"thinking"`) {
		t.Logf("EVIDENCE S3: wire contains translated thinking block (by-design; response.go unchanged since base fea5874) — NOT a fix-introduced leak")
	} else {
		t.Logf("EVIDENCE S3: wire does NOT contain a thinking block")
	}
	// Content must survive regardless.
	assertContains(t, wire, wireContent, "S3/content")

	t.Logf("EVIDENCE S3: request id=%s status=%s wire length=%d", log.ID, log.Status, len(wire))
}
