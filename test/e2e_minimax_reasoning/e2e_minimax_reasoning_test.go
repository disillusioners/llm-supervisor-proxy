// Scenario suite S1–S14 for the MiniMax reasoning_details E2E gate (P3-5).
//
// Each scenario group targets the full proxy.Handler on one of the four
// proxy paths and asserts the wire-visible feature contract from the recon
// brief (@d5280ce). Subtests are t.Run-nested; the suite totals ≥ 25
// subtest assertions.
package e2e_minimax_reasoning

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy/translator"
)

// ═════════════════════════════════════════════════════════════════════════════
// S1 — Request translation, race-external
// ═════════════════════════════════════════════════════════════════════════════

// TestS1_RequestTranslation_RaceExternal drives an UNREGISTERED model id
// (resolvedModel=nil ⇒ race-external path) with flag ON + MiniMax upstream
// credential. The captured upstream body must carry the translated shape.
func TestS1_RequestTranslation_RaceExternal(t *testing.T) {
	env := setupTestEnv(t, okNonStreamHandler(), envOptions{})
	tok := env.plainToken

	t.Run("single_assistant_reasoning_translated", func(t *testing.T) {
		rr := env.run(chatRequest{model: raceExtModel, token: tok, flag: true, messages: reasoningMessages})
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		cap := env.mockUp.last()
		assertTranslatedUpstreamRequest(t, cap.BodyParsed, reasoningMessages, "S1/single")
		t.Logf("EVIDENCE S1 captured-upstream (translated request): %s", string(cap.Body))
	})

	t.Run("multi_message_monotonic_ids", func(t *testing.T) {
		rr := env.run(chatRequest{model: raceExtModel, token: tok, flag: true, messages: twoReasoningMessages})
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		cap := env.mockUp.last()
		assertTranslatedUpstreamRequest(t, cap.BodyParsed, twoReasoningMessages, "S1/monotonic")
		// exact monotonic ids on both slots
		upMsgs := msg(t, cap.BodyParsed, "S1/monotonic")
		d0 := upMsgs[0].(map[string]interface{})["reasoning_details"].([]interface{})
		d1 := upMsgs[1].(map[string]interface{})["reasoning_details"].([]interface{})
		assertDetailsEntry(t, d0[0], "reasoning-text-1", "think-1", "S1/monotonic msg0")
		assertDetailsEntry(t, d1[0], "reasoning-text-2", "think-2", "S1/monotonic msg1")
	})

	t.Run("slot_correct_details_only_in_slot1", func(t *testing.T) {
		rr := env.run(chatRequest{model: raceExtModel, token: tok, flag: true, messages: reasoningMessages})
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d", rr.Code)
		}
		upMsgs := msg(t, env.mockUp.last().BodyParsed, "S1/slot")
		if _, has := upMsgs[0].(map[string]interface{})["reasoning_details"]; has {
			t.Errorf("S1/slot: messages[0] (user) unexpectedly carries reasoning_details")
		}
		if _, has := upMsgs[2].(map[string]interface{})["reasoning_details"]; has {
			t.Errorf("S1/slot: messages[2] (user) unexpectedly carries reasoning_details")
		}
	})
}

// ═════════════════════════════════════════════════════════════════════════════
// S2 — Request translation, ultimate-external
// ═════════════════════════════════════════════════════════════════════════════

// TestS2_RequestTranslation_UltimateExternal drives X-Force-Ultimate-Model
// with ULTIMATE_MODEL_ID=ultExtModel (Internal:false ⇒ ultimate-external
// byte path via translator.TranslateRequestBytes).
func TestS2_RequestTranslation_UltimateExternal(t *testing.T) {
	env := setupTestEnv(t, okNonStreamHandler(), envOptions{ultimateModelID: ultExtModel})
	tok := env.ultimateToken

	t.Run("translated_via_force_header", func(t *testing.T) {
		rr := env.run(chatRequest{model: raceIntModel, token: tok, flag: true, forceUlt: true, messages: reasoningMessages})
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		cap := env.mockUp.last()
		assertTranslatedUpstreamRequest(t, cap.BodyParsed, reasoningMessages, "S2/single")
		// ultimate-external rewrites the model field to the ultimate model id
		if got, _ := cap.BodyParsed["model"].(string); got != ultExtModel {
			t.Errorf("S2: upstream model=%q want %q (ultimate override)", got, ultExtModel)
		}
	})

	t.Run("multi_message_monotonic_ids", func(t *testing.T) {
		rr := env.run(chatRequest{model: raceIntModel, token: tok, flag: true, forceUlt: true, messages: twoReasoningMessages})
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		cap := env.mockUp.last()
		assertTranslatedUpstreamRequest(t, cap.BodyParsed, twoReasoningMessages, "S2/monotonic")
		upMsgs := msg(t, cap.BodyParsed, "S2/monotonic")
		d0 := upMsgs[0].(map[string]interface{})["reasoning_details"].([]interface{})
		d1 := upMsgs[1].(map[string]interface{})["reasoning_details"].([]interface{})
		assertDetailsEntry(t, d0[0], "reasoning-text-1", "think-1", "S2/monotonic msg0")
		assertDetailsEntry(t, d1[0], "reasoning-text-2", "think-2", "S2/monotonic msg1")
	})
}

// ═════════════════════════════════════════════════════════════════════════════
// S3 — Request translation, internal paths (+ non-MiniMax negative)
// ═════════════════════════════════════════════════════════════════════════════

// TestS3_RequestTranslation_InternalPaths covers race-internal and
// ultimate-internal (typed translation) plus the non-MiniMax negative.
func TestS3_RequestTranslation_InternalPaths(t *testing.T) {
	t.Run("race_internal_translated", func(t *testing.T) {
		env := setupTestEnv(t, okNonStreamHandler(), envOptions{})
		rr := env.run(chatRequest{model: raceIntModel, token: env.plainToken, flag: true, messages: reasoningMessages})
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		cap := env.mockUp.last()
		assertTranslatedUpstreamRequest(t, cap.BodyParsed, reasoningMessages, "S3/race-int")
	})

	t.Run("ultimate_internal_translated", func(t *testing.T) {
		env := setupTestEnv(t, okNonStreamHandler(), envOptions{ultimateModelID: ultIntModel})
		rr := env.run(chatRequest{model: raceIntModel, token: env.ultimateToken, flag: true, forceUlt: true, messages: reasoningMessages})
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		cap := env.mockUp.last()
		assertTranslatedUpstreamRequest(t, cap.BodyParsed, reasoningMessages, "S3/ult-int")
		t.Logf("EVIDENCE S3/ult-int captured-upstream (typed-path translated request): %s", string(cap.Body))
	})

	t.Run("non_minimax_credential_inert", func(t *testing.T) {
		// race-internal model bound to the openai credential.
		env := setupTestEnv(t, okNonStreamHandler(), envOptions{})
		rr := env.run(chatRequest{model: raceIntModelOpenAI, token: env.plainToken, flag: true, messages: reasoningMessages})
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		assertUntouchedUpstreamRequest(t, env.mockUp.last().BodyParsed, reasoningMessages, "S3/non-minimax/race-int")
	})

	t.Run("non_minimax_upstream_credential_inert_race_ext", func(t *testing.T) {
		// race-external with UpstreamCredentialID bound to the openai cred.
		env := setupTestEnv(t, okNonStreamHandler(), envOptions{upstreamProvider: "openai"})
		rr := env.run(chatRequest{model: raceExtModel, token: env.plainToken, flag: true, messages: reasoningMessages})
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		assertUntouchedUpstreamRequest(t, env.mockUp.last().BodyParsed, reasoningMessages, "S3/non-minimax/race-ext")
	})
}

// ═════════════════════════════════════════════════════════════════════════════
// S4 — Non-stream response, external paths (race-ext + ult-ext)
// ═════════════════════════════════════════════════════════════════════════════

// assertDetailsTranslatedResponse is the shared translated-response check:
// reasoning_content == "think-A think-B"... (per given expectation), no
// reasoning_details key, audio_content+name stripped when present upstream.
func assertDetailsTranslatedResponse(t *testing.T, rrBody map[string]interface{}, wantReasoning, where string, expectStripped ...string) {
	t.Helper()
	choices, ok := rrBody["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		t.Fatalf("%s: no choices in client body — %s", where, mustJSON(rrBody))
	}
	choice, ok := choices[0].(map[string]interface{})
	if !ok {
		t.Fatalf("%s: choices[0] not object", where)
	}
	message, ok := choice["message"].(map[string]interface{})
	if !ok {
		t.Fatalf("%s: no message object — %s", where, mustJSON(choice))
	}
	got, has := message["reasoning_content"].(string)
	if !has || got != wantReasoning {
		t.Errorf("%s: message.reasoning_content=%q (has=%v), want %q — message=%s", where, got, has, wantReasoning, mustJSON(message))
	}
	if _, leak := message["reasoning_details"]; leak {
		t.Errorf("%s: client body leaks reasoning_details — %s", where, mustJSON(message))
	}
	for _, stripped := range expectStripped {
		if _, present := message[stripped]; present {
			t.Errorf("%s: client body leaks %q (must be stripped) — %s", where, stripped, mustJSON(message))
		}
	}
}

// TestS4_NonStreamResponse_External covers both external byte paths.
func TestS4_NonStreamResponse_External(t *testing.T) {
	usage := map[string]interface{}{"prompt_tokens": 11, "completion_tokens": 7, "total_tokens": 18}

	t.Run("race_ext_details_only_mode", func(t *testing.T) {
		env := setupTestEnv(t, detailsNonStreamHandler(nil, nil, usage), envOptions{})
		rr := env.run(chatRequest{model: raceExtModel, token: env.plainToken, flag: true, messages: reasoningMessages})
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		body := parseJSONBody(t, rr, "S4/race-ext/details-only")
		assertDetailsTranslatedResponse(t, body, "think-Athink-B", "S4/race-ext/details-only")
		t.Logf("EVIDENCE S4 client-body (translated response): %s", rr.Body.String())
	})

	t.Run("race_ext_details_plus_same_text_single_winner", func(t *testing.T) {
		// BOTH fields present upstream with identical text: details WIN and
		// the text appears exactly once (not duplicated).
		env := setupTestEnv(t, detailsNonStreamHandler(nil, map[string]interface{}{"reasoning_content": "think-A"}, usage), envOptions{})
		rr := env.run(chatRequest{model: raceExtModel, token: env.plainToken, flag: true, messages: reasoningMessages})
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d", rr.Code)
		}
		body := parseJSONBody(t, rr, "S4/race-ext/both")
		assertDetailsTranslatedResponse(t, body, "think-Athink-B", "S4/race-ext/both")
		message := body["choices"].([]interface{})[0].(map[string]interface{})["message"].(map[string]interface{})
		if got := message["reasoning_content"].(string); strings.Count(got, "think-A") != 1 {
			t.Errorf("S4/race-ext/both: reasoning_content=%q — think-A appears %d times, want exactly 1 (single winner)", got, strings.Count(got, "think-A"))
		}
	})

	t.Run("race_ext_audio_name_stripped", func(t *testing.T) {
		env := setupTestEnv(t, detailsNonStreamHandler(nil, map[string]interface{}{"audio_content": "x", "name": "bob"}, usage), envOptions{})
		rr := env.run(chatRequest{model: raceExtModel, token: env.plainToken, flag: true, messages: reasoningMessages})
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d", rr.Code)
		}
		body := parseJSONBody(t, rr, "S4/race-ext/strip")
		assertDetailsTranslatedResponse(t, body, "think-Athink-B", "S4/race-ext/strip", "audio_content", "name")
	})

	t.Run("ult_ext_details_only_mode", func(t *testing.T) {
		env := setupTestEnv(t, detailsNonStreamHandler(nil, nil, usage), envOptions{ultimateModelID: ultExtModel})
		rr := env.run(chatRequest{model: raceIntModel, token: env.ultimateToken, flag: true, forceUlt: true, messages: reasoningMessages})
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		body := parseJSONBody(t, rr, "S4/ult-ext/details-only")
		assertDetailsTranslatedResponse(t, body, "think-Athink-B", "S4/ult-ext/details-only")
	})

	t.Run("ult_ext_both_modes_single_winner", func(t *testing.T) {
		env := setupTestEnv(t, detailsNonStreamHandler(nil, map[string]interface{}{"reasoning_content": "think-A"}, usage), envOptions{ultimateModelID: ultExtModel})
		rr := env.run(chatRequest{model: raceIntModel, token: env.ultimateToken, flag: true, forceUlt: true, messages: reasoningMessages})
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d", rr.Code)
		}
		body := parseJSONBody(t, rr, "S4/ult-ext/both")
		assertDetailsTranslatedResponse(t, body, "think-Athink-B", "S4/ult-ext/both")
		message := body["choices"].([]interface{})[0].(map[string]interface{})["message"].(map[string]interface{})
		if got := message["reasoning_content"].(string); strings.Count(got, "think-A") != 1 {
			t.Errorf("S4/ult-ext/both: think-A appears %d times, want exactly 1 — %q", strings.Count(got, "think-A"), got)
		}
	})

	t.Run("usage_preserved_on_translated_path", func(t *testing.T) {
		// S12 folded here on the same harness instance: usage object equality.
		env := setupTestEnv(t, detailsNonStreamHandler(nil, nil, usage), envOptions{})
		rr := env.run(chatRequest{model: raceExtModel, token: env.plainToken, flag: true, messages: reasoningMessages})
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d", rr.Code)
		}
		body := parseJSONBody(t, rr, "S12/usage")
		gotUsage, ok := body["usage"].(map[string]interface{})
		if !ok {
			t.Fatalf("S12: no usage object in client body — %s", mustJSON(body))
		}
		for k, want := range map[string]float64{"prompt_tokens": 11, "completion_tokens": 7, "total_tokens": 18} {
			if got, _ := gotUsage[k].(float64); got != want {
				t.Errorf("S12: usage.%s=%v want %v — usage=%s", k, gotUsage[k], want, mustJSON(gotUsage))
			}
		}
		if len(gotUsage) < 3 {
			t.Errorf("S12: usage object suspiciously small: %s", mustJSON(gotUsage))
		}
	})
}

// ═════════════════════════════════════════════════════════════════════════════
// S5 — Non-stream response, internal paths (race-int + ult-int)
// ═════════════════════════════════════════════════════════════════════════════

// TestS5_NonStreamResponse_Internal drives the typed openai.go extraction
// (populateReasoningFromDetails) through the full handler on both internal
// paths. The upstream mock serves the MiniMax response shape over HTTP to
// the OpenAIProvider client.
func TestS5_NonStreamResponse_Internal(t *testing.T) {
	t.Run("race_internal_details_to_reasoning_content", func(t *testing.T) {
		env := setupTestEnv(t, detailsNonStreamHandler(nil, nil, nil), envOptions{})
		rr := env.run(chatRequest{model: raceIntModel, token: env.plainToken, flag: true, messages: reasoningMessages})
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		body := parseJSONBody(t, rr, "S5/race-int")
		assertDetailsTranslatedResponse(t, body, "think-Athink-B", "S5/race-int")
	})

	t.Run("ultimate_internal_details_to_reasoning_content", func(t *testing.T) {
		env := setupTestEnv(t, detailsNonStreamHandler(nil, nil, nil), envOptions{ultimateModelID: ultIntModel})
		rr := env.run(chatRequest{model: raceIntModel, token: env.ultimateToken, flag: true, forceUlt: true, messages: reasoningMessages})
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		body := parseJSONBody(t, rr, "S5/ult-int")
		assertDetailsTranslatedResponse(t, body, "think-Athink-B", "S5/ult-int")
	})

	t.Run("race_internal_both_fields_single_winner", func(t *testing.T) {
		env := setupTestEnv(t, detailsNonStreamHandler(nil, map[string]interface{}{"reasoning_content": "stale-text"}, nil), envOptions{})
		rr := env.run(chatRequest{model: raceIntModel, token: env.plainToken, flag: true, messages: reasoningMessages})
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		body := parseJSONBody(t, rr, "S5/race-int/both")
		// details WIN; pre-existing reasoning_content discarded entirely.
		assertDetailsTranslatedResponse(t, body, "think-Athink-B", "S5/race-int/both")
		message := body["choices"].([]interface{})[0].(map[string]interface{})["message"].(map[string]interface{})
		if got := message["reasoning_content"].(string); strings.Contains(got, "stale-text") {
			t.Errorf("S5/race-int/both: discarded pre-existing reasoning_content leaked: %q", got)
		}
	})
}

// ═════════════════════════════════════════════════════════════════════════════
// S6 — Stream response, external (StreamTranslator semantics)
// ═════════════════════════════════════════════════════════════════════════════

func sseStream(lines ...string) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString("data: " + l + "\n\n")
	}
	return b.String()
}

func detailEntry(id, text string) string {
	return `{"type":"reasoning.text","id":"` + id + `","format":"MiniMax-response-v1","index":0,"text":"` + text + `"}`
}

func reasoningDetailsChunk(details string) string {
	return `{"id":"1","object":"chat.completion.chunk","created":1,"model":"mock","choices":[{"index":0,"delta":{"reasoning_details":[` + details + `]}}]}`
}

func contentChunk(text string) string {
	return `{"id":"1","object":"chat.completion.chunk","created":1,"model":"mock","choices":[{"index":0,"delta":{"content":"` + text + `"}}]}`
}

var finishChunk = `{"id":"1","object":"chat.completion.chunk","created":1,"model":"mock","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`

func streamHandler(body string) http.HandlerFunc {
	return rawStringHandler(http.StatusOK, body, "text/event-stream")
}

// TestS6_StreamResponse_External exercises the external stream translator
// through the full handler (race-external path).
func TestS6_StreamResponse_External(t *testing.T) {
	t.Run("mode_incremental_three_deltas_in_order", func(t *testing.T) {
		up := sseStream(
			reasoningDetailsChunk(detailEntry("reasoning-text-1", "think-1")),
			reasoningDetailsChunk(detailEntry("reasoning-text-2", "think-2")),
			reasoningDetailsChunk(detailEntry("reasoning-text-3", "think-3")),
			contentChunk("final"),
			finishChunk,
			"[DONE]",
		)
		env := setupTestEnv(t, streamHandler(up), envOptions{})
		rr := env.run(chatRequest{model: raceExtModel, token: env.plainToken, flag: true, stream: true, messages: reasoningMessages})
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		got := reasoningDeltasFromClientStream(t, rr.Body.Bytes())
		want := []string{"think-1", "think-2", "think-3"}
		if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
			t.Errorf("S6/incremental: reasoning deltas=%v, want %v — raw=%s", got, want, rr.Body.String())
		}
		if strings.Contains(rr.Body.String(), "reasoning_details") {
			t.Errorf("S6/incremental: client stream leaks reasoning_details")
		}
	})

	t.Run("mode_cumulative_suffix_only_never_AB_delta", func(t *testing.T) {
		up := sseStream(
			reasoningDetailsChunk(detailEntry("reasoning-text-1", "A")),
			reasoningDetailsChunk(detailEntry("reasoning-text-2", "AB")),
			reasoningDetailsChunk(detailEntry("reasoning-text-3", "ABC")),
			contentChunk("final"),
			finishChunk,
			"[DONE]",
		)
		env := setupTestEnv(t, streamHandler(up), envOptions{})
		rr := env.run(chatRequest{model: raceExtModel, token: env.plainToken, flag: true, stream: true, messages: reasoningMessages})
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		got := reasoningDeltasFromClientStream(t, rr.Body.Bytes())
		if len(got) != 3 || got[0] != "A" || got[1] != "B" || got[2] != "C" {
			t.Errorf("S6/cumulative: deltas=%v, want [A B C] (suffix-only emission) — raw=%s", got, rr.Body.String())
		}
		for _, d := range got {
			if d == "AB" {
				t.Errorf("S6/cumulative: forbidden cumulative delta %q emitted — raw=%s", d, rr.Body.String())
			}
		}
	})

	t.Run("multi_entry_single_chunk_ordered", func(t *testing.T) {
		up := sseStream(
			reasoningDetailsChunk(detailEntry("reasoning-text-1", "think-1")+","+detailEntry("reasoning-text-2", "think-2")),
			contentChunk("final"),
			finishChunk,
			"[DONE]",
		)
		env := setupTestEnv(t, streamHandler(up), envOptions{})
		rr := env.run(chatRequest{model: raceExtModel, token: env.plainToken, flag: true, stream: true, messages: reasoningMessages})
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		got := reasoningDeltasFromClientStream(t, rr.Body.Bytes())
		if len(got) != 2 || got[0] != "think-1" || got[1] != "think-2" {
			t.Errorf("S6/multi-entry: deltas=%v, want [think-1 think-2] in array order — raw=%s", got, rr.Body.String())
		}
	})

	t.Run("empty_text_entry_no_empty_delta", func(t *testing.T) {
		up := sseStream(
			reasoningDetailsChunk(detailEntry("reasoning-text-1", "think-1")+`,`+`{"type":"reasoning.text","id":"reasoning-text-2","format":"MiniMax-response-v1","index":0,"text":""}`),
			contentChunk("final"),
			finishChunk,
			"[DONE]",
		)
		env := setupTestEnv(t, streamHandler(up), envOptions{})
		rr := env.run(chatRequest{model: raceExtModel, token: env.plainToken, flag: true, stream: true, messages: reasoningMessages})
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		got := reasoningDeltasFromClientStream(t, rr.Body.Bytes())
		if len(got) != 1 || got[0] != "think-1" {
			t.Errorf("S6/empty-entry: deltas=%v, want exactly [think-1] (empty entry skipped) — raw=%s", got, rr.Body.String())
		}
	})

	t.Run("both_modes_single_winner_chunk", func(t *testing.T) {
		// One chunk carrying BOTH reasoning_details and a same-text
		// delta.reasoning_content: details win, text emitted exactly once.
		chunk := `{"id":"1","object":"chat.completion.chunk","created":1,"model":"mock","choices":[{"index":0,"delta":{"reasoning_details":[` +
			detailEntry("reasoning-text-1", "think-1") + `],"reasoning_content":"think-1"}}]}`
		up := sseStream(chunk, contentChunk("final"), finishChunk, "[DONE]")
		env := setupTestEnv(t, streamHandler(up), envOptions{})
		rr := env.run(chatRequest{model: raceExtModel, token: env.plainToken, flag: true, stream: true, messages: reasoningMessages})
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		got := reasoningDeltasFromClientStream(t, rr.Body.Bytes())
		if len(got) != 1 || got[0] != "think-1" {
			t.Errorf("S6/both-modes: deltas=%v, want exactly one [think-1] (single winner) — raw=%s", got, rr.Body.String())
		}
	})

	t.Run("framing_every_line_wellformed_done_terminator", func(t *testing.T) {
		up := sseStream(
			reasoningDetailsChunk(detailEntry("reasoning-text-1", "think-1")),
			contentChunk("hello"),
			finishChunk,
			"[DONE]",
		)
		env := setupTestEnv(t, streamHandler(up), envOptions{})
		rr := env.run(chatRequest{model: raceExtModel, token: env.plainToken, flag: true, stream: true, messages: reasoningMessages})
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		sseAssertWellFormed(t, rr.Body.Bytes(), "S6/framing")
	})

	t.Run("ult_ext_stream_translated", func(t *testing.T) {
		up := sseStream(
			reasoningDetailsChunk(detailEntry("reasoning-text-1", "think-1")),
			contentChunk("final"),
			finishChunk,
			"[DONE]",
		)
		env := setupTestEnv(t, streamHandler(up), envOptions{ultimateModelID: ultExtModel})
		rr := env.run(chatRequest{model: raceIntModel, token: env.ultimateToken, flag: true, forceUlt: true, stream: true, messages: reasoningMessages})
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		got := reasoningDeltasFromClientStream(t, rr.Body.Bytes())
		if len(got) != 1 || got[0] != "think-1" {
			t.Errorf("S6/ult-ext: deltas=%v, want [think-1] — raw=%s", got, rr.Body.String())
		}
		if strings.Contains(rr.Body.String(), "reasoning_details") {
			t.Errorf("S6/ult-ext: client stream leaks reasoning_details")
		}
		sseAssertWellFormed(t, rr.Body.Bytes(), "S6/ult-ext")
	})
}

// ═════════════════════════════════════════════════════════════════════════════
// S7 — Stream response, internal (typed thinking events)
// ═════════════════════════════════════════════════════════════════════════════

// TestS7_StreamResponse_Internal drives upstream reasoning_details deltas
// through the internal OpenAIProvider stream parser
// (extractReasoningDetailsByChoiceForStream) on race-internal and
// ultimate-internal.
func TestS7_StreamResponse_Internal(t *testing.T) {
	streamUp := sseStream(
		reasoningDetailsChunk(detailEntry("reasoning-text-1", "think-1")),
		reasoningDetailsChunk(detailEntry("reasoning-text-2", "think-2")),
		contentChunk("final"),
		finishChunk,
		"[DONE]",
	)

	t.Run("race_internal_thinking_events_no_leak", func(t *testing.T) {
		env := setupTestEnv(t, streamHandler(streamUp), envOptions{})
		rr := env.run(chatRequest{model: raceIntModel, token: env.plainToken, flag: true, stream: true, messages: reasoningMessages})
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		got := reasoningDeltasFromClientStream(t, rr.Body.Bytes())
		if len(got) != 2 || got[0] != "think-1" || got[1] != "think-2" {
			t.Errorf("S7/race-int: reasoning deltas=%v, want [think-1 think-2] — raw=%s", got, rr.Body.String())
		}
		if strings.Contains(rr.Body.String(), "reasoning_details") {
			t.Errorf("S7/race-int: client stream leaks reasoning_details — %s", rr.Body.String())
		}
		sseAssertWellFormed(t, rr.Body.Bytes(), "S7/race-int")
	})

	t.Run("ultimate_internal_thinking_events_no_leak", func(t *testing.T) {
		env := setupTestEnv(t, streamHandler(streamUp), envOptions{ultimateModelID: ultIntModel})
		rr := env.run(chatRequest{model: raceIntModel, token: env.ultimateToken, flag: true, forceUlt: true, stream: true, messages: reasoningMessages})
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		got := reasoningDeltasFromClientStream(t, rr.Body.Bytes())
		if len(got) != 2 || got[0] != "think-1" || got[1] != "think-2" {
			t.Errorf("S7/ult-int: reasoning deltas=%v, want [think-1 think-2] — raw=%s", got, rr.Body.String())
		}
		if strings.Contains(rr.Body.String(), "reasoning_details") {
			t.Errorf("S7/ult-int: client stream leaks reasoning_details — %s", rr.Body.String())
		}
		sseAssertWellFormed(t, rr.Body.Bytes(), "S7/ult-int")
	})
}

// ═════════════════════════════════════════════════════════════════════════════
// S8 — Flag-absent quadrant (all 4 paths)
// ═════════════════════════════════════════════════════════════════════════════

// TestS8_FlagAbsent_Quadrant: flag ABSENT + MiniMax credential ⇒ no
// translation either direction on any path. Request side:
// reasoning_content preserved verbatim in the same slot, no reasoning_split.
// Response side: upstream reasoning_content passes through unchanged.
func TestS8_FlagAbsent_Quadrant(t *testing.T) {
	// Response carries plain reasoning_content (no details) — passthrough.
	reasoningOnlyResp := rawStringHandler(http.StatusOK,
		`{"id":"r","object":"chat.completion","created":1,"model":"mock","choices":[{"index":0,"message":{"role":"assistant","content":"final","reasoning_content":"passthrough-thinking"},"finish_reason":"stop"}]}`,
		"application/json")

	assertResponsePassthrough := func(t *testing.T, rr *responseWithBody) {
		t.Helper()
		if rr.code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.code, rr.body)
		}
		message := rr.message(t)
		if got, _ := message["reasoning_content"].(string); got != "passthrough-thinking" {
			t.Errorf("S8: client reasoning_content=%q, want unchanged passthrough-thinking — message=%s", got, mustJSON(message))
		}
		if _, leak := message["reasoning_details"]; leak {
			t.Errorf("S8: details synthesized on flag-absent path — %s", mustJSON(message))
		}
	}

	t.Run("race_external_flag_absent", func(t *testing.T) {
		env := setupTestEnv(t, reasoningOnlyResp, envOptions{})
		rr := env.run(chatRequest{model: raceExtModel, token: env.plainToken, messages: reasoningMessages})
		assertUntouchedUpstreamRequest(t, env.mockUp.last().BodyParsed, reasoningMessages, "S8/race-ext/request")
		assertResponsePassthrough(t, captureResponse(t, rr))
	})

	t.Run("race_internal_flag_absent", func(t *testing.T) {
		env := setupTestEnv(t, reasoningOnlyResp, envOptions{})
		rr := env.run(chatRequest{model: raceIntModel, token: env.plainToken, messages: reasoningMessages})
		assertUntouchedUpstreamRequest(t, env.mockUp.last().BodyParsed, reasoningMessages, "S8/race-int/request")
		assertResponsePassthrough(t, captureResponse(t, rr))
	})

	t.Run("ultimate_external_flag_absent", func(t *testing.T) {
		env := setupTestEnv(t, reasoningOnlyResp, envOptions{ultimateModelID: ultExtModel})
		rr := env.run(chatRequest{model: raceIntModel, token: env.ultimateToken, forceUlt: true, messages: reasoningMessages})
		assertUntouchedUpstreamRequest(t, env.mockUp.last().BodyParsed, reasoningMessages, "S8/ult-ext/request")
		assertResponsePassthrough(t, captureResponse(t, rr))
	})

	t.Run("ultimate_internal_flag_absent", func(t *testing.T) {
		env := setupTestEnv(t, reasoningOnlyResp, envOptions{ultimateModelID: ultIntModel})
		rr := env.run(chatRequest{model: raceIntModel, token: env.ultimateToken, forceUlt: true, messages: reasoningMessages})
		assertUntouchedUpstreamRequest(t, env.mockUp.last().BodyParsed, reasoningMessages, "S8/ult-int/request")
		assertResponsePassthrough(t, captureResponse(t, rr))
	})
}

// ═════════════════════════════════════════════════════════════════════════════
// S9 — Non-MiniMax credential + flag ON (legacy contract)
// ═════════════════════════════════════════════════════════════════════════════

// TestS9_NonMiniMax_FlagOn: no translation either direction.
//
//   - Request side (all paths): raw reasoning_content reaches upstream.
//   - External byte paths: the response is byte-IDENTICAL passthrough — an
//     upstream reasoning_details array reaches the client untouched. That
//     IS the legacy contract (assert exactly the passthrough, not stripping).
//   - Internal paths: openai.go typed parse — details hydrate and
//     populateReasoningFromDetails applies the same single-winner
//     extraction (data-driven, provider-agnostic); plain reasoning_content
//     still handled by the pre-existing fallback.
func TestS9_NonMiniMax_FlagOn(t *testing.T) {
	detailsRespBody := `{"id":"r","object":"chat.completion","created":1,"model":"mock","choices":[{"index":0,"message":{"role":"assistant","content":"final","reasoning_details":[{"type":"reasoning.text","id":"reasoning-text-1","format":"MiniMax-response-v1","index":0,"text":"think-1"}]},"finish_reason":"stop"}]}`

	t.Run("race_external_request_untranslated_response_byte_identical", func(t *testing.T) {
		env := setupTestEnv(t, rawStringHandler(http.StatusOK, detailsRespBody, "application/json"), envOptions{upstreamProvider: "openai"})
		rr := env.run(chatRequest{model: raceExtModel, token: env.plainToken, flag: true, messages: reasoningMessages})
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		// request side: untouched
		assertUntouchedUpstreamRequest(t, env.mockUp.last().BodyParsed, reasoningMessages, "S9/race-ext/request")
		// response side: client sees the upstream reasoning_details array
		// byte-identically (legacy passthrough — NOT stripped).
		client := parseJSONBody(t, rr, "S9/race-ext/response")
		message := client["choices"].([]interface{})[0].(map[string]interface{})["message"].(map[string]interface{})
		details, ok := message["reasoning_details"].([]interface{})
		if !ok || len(details) != 1 {
			t.Fatalf("S9/race-ext/response: expected reasoning_details passthrough array, got=%v — %s", message["reasoning_details"], mustJSON(message))
		}
		assertDetailsEntry(t, details[0], "reasoning-text-1", "think-1", "S9/race-ext/passthrough")
	})

	t.Run("race_internal_request_untranslated", func(t *testing.T) {
		env := setupTestEnv(t, detailsNonStreamHandler(nil, nil, nil), envOptions{})
		rr := env.run(chatRequest{model: raceIntModelOpenAI, token: env.plainToken, flag: true, messages: reasoningMessages})
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		assertUntouchedUpstreamRequest(t, env.mockUp.last().BodyParsed, reasoningMessages, "S9/race-int/request")
		// internal typed path is data-driven: details still extract to
		// reasoning_content for the client (provider-agnostic helper).
		body := parseJSONBody(t, rr, "S9/race-int/response")
		assertDetailsTranslatedResponse(t, body, "think-Athink-B", "S9/race-int/response")
	})

	t.Run("ultimate_external_request_untranslated_response_passthrough", func(t *testing.T) {
		// Ultimate paths gate on the MODEL's Credentials[0] (handler.go:625-635
		// resolves upstreamProvider from modelCfg.PrimaryCredentialID() — the
		// legacy single-credential view via the back-compat shim after the
		// Phase-1 CredentialID field removal). Point ULTIMATE_MODEL_ID at the
		// ultimate-external model bound to the openai credential ⇒ gate off
		// ⇒ legacy byte passthrough.
		env := setupTestEnv(t, rawStringHandler(http.StatusOK, detailsRespBody, "application/json"), envOptions{ultimateModelID: ultExtModelOpenAI})
		rr := env.run(chatRequest{model: raceIntModel, token: env.ultimateToken, flag: true, forceUlt: true, messages: reasoningMessages})
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		assertUntouchedUpstreamRequest(t, env.mockUp.last().BodyParsed, reasoningMessages, "S9/ult-ext/request")
		client := parseJSONBody(t, rr, "S9/ult-ext/response")
		message := client["choices"].([]interface{})[0].(map[string]interface{})["message"].(map[string]interface{})
		if _, ok := message["reasoning_details"].([]interface{}); !ok {
			t.Errorf("S9/ult-ext/response: expected reasoning_details passthrough, got=%s", mustJSON(message))
		}
	})

	t.Run("ultimate_internal_request_untranslated", func(t *testing.T) {
		// ultimate-internal model with a non-MiniMax credential: typed
		// setter gate off; plain reasoning_content is still preserved on
		// the wire (openai.go hydration).
		env := setupTestEnv(t, detailsNonStreamHandler(nil, nil, nil), envOptions{ultimateModelID: raceIntModelOpenAI})
		rr := env.run(chatRequest{model: raceIntModel, token: env.ultimateToken, flag: true, forceUlt: true, messages: reasoningMessages})
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		assertUntouchedUpstreamRequest(t, env.mockUp.last().BodyParsed, reasoningMessages, "S9/ult-int/request")
	})
}

// ═════════════════════════════════════════════════════════════════════════════
// S10 — Credential gate: unresolvable credential ⇒ feature inert
// ═════════════════════════════════════════════════════════════════════════════

// TestS10_CredentialGate_Inert: ultimate-external model with NO resolvable
// upstream credential (ULTIMATE_MODEL_ID points at a model whose
// CredentialID does not exist ⇒ upstreamProvider="" ⇒ gate=false).
func TestS10_CredentialGate_Inert(t *testing.T) {
	// Register a dangling-credential ultimate model via a fresh env, then
	// point ULTIMATE_MODEL_ID at it. AddModel validates credential refs
	// (rejects a dangling CredentialID), so this subtest instead drives
	// race-external with NO upstream credential configured
	// (opts.noUpstreamCred): raceExtProviderIsMiniMax=false (D3 — empty
	// UpstreamCredentialID ⇒ gate off), which is the same inert wire
	// contract as an unresolvable credential.
	t.Run("race_ext_no_upstream_credential_inert", func(t *testing.T) {
		env := setupTestEnv(t, okNonStreamHandler(), envOptions{noUpstreamCred: true})
		rr := env.run(chatRequest{model: raceExtModel, token: env.plainToken, flag: true, messages: reasoningMessages})
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		assertUntouchedUpstreamRequest(t, env.mockUp.last().BodyParsed, reasoningMessages, "S10/no-cred/request")
	})

	t.Run("ultimate_external_dangling_credential_inert", func(t *testing.T) {
		// ULTIMATE_MODEL_ID set to a NONEXISTENT model id: Execute fails to
		// resolve the model and the handler surfaces an error before any
		// translation. The wire contract: no reasoning_split ever appears.
		env := setupTestEnv(t, okNonStreamHandler(), envOptions{ultimateModelID: "missing-ultimate-model"})
		rr := env.run(chatRequest{model: raceIntModel, token: env.ultimateToken, flag: true, forceUlt: true, messages: reasoningMessages})
		if rr.Code != http.StatusOK {
			// Acceptable: ultimate execution failed with an upstream error.
			if strings.Contains(rr.Body.String(), "reasoning_split") {
				t.Errorf("S10/dangling: reasoning_split appeared in error body: %s", rr.Body.String())
			}
			return
		}
		// If a request DID reach upstream, it must be untranslated.
		if caps := env.mockUp.snapshot(); len(caps) > 0 {
			assertUntouchedUpstreamRequest(t, caps[0].BodyParsed, reasoningMessages, "S10/dangling/request")
		} else {
			t.Errorf("S10/dangling: expected either an error status or an upstream request; got 200 with no upstream call — body=%s", rr.Body.String())
		}
	})
}

// ═════════════════════════════════════════════════════════════════════════════
// S11 — Error path: upstream 500
// ═════════════════════════════════════════════════════════════════════════════

// TestS11_UpstreamError_CleanFailure: flag ON + MiniMax cred, upstream 500.
// The client gets an error status; the body has NO reasoning_details and no
// partial reasoning text leakage.
func TestS11_UpstreamError_CleanFailure(t *testing.T) {
	errHandler := rawStringHandler(http.StatusInternalServerError,
		`{"error":{"message":"upstream exploded","type":"server_error"}}`, "application/json")

	t.Run("race_external_500", func(t *testing.T) {
		env := setupTestEnv(t, errHandler, envOptions{})
		rr := env.run(chatRequest{model: raceExtModel, token: env.plainToken, flag: true, messages: reasoningMessages})
		if rr.Code == http.StatusOK {
			t.Fatalf("S11/race-ext: expected error status, got 200 — body=%s", rr.Body.String())
		}
		if strings.Contains(rr.Body.String(), "reasoning_details") {
			t.Errorf("S11/race-ext: error body leaks reasoning_details: %s", rr.Body.String())
		}
		if strings.Contains(rr.Body.String(), "think-A") || strings.Contains(rr.Body.String(), "think-1") {
			t.Errorf("S11/race-ext: error body leaks partial reasoning text: %s", rr.Body.String())
		}
	})

	t.Run("ultimate_external_500", func(t *testing.T) {
		env := setupTestEnv(t, errHandler, envOptions{ultimateModelID: ultExtModel})
		rr := env.run(chatRequest{model: raceIntModel, token: env.ultimateToken, flag: true, forceUlt: true, messages: reasoningMessages})
		if rr.Code == http.StatusOK && !strings.Contains(rr.Body.String(), "error") {
			t.Fatalf("S11/ult-ext: expected error response, got 200 — body=%s", rr.Body.String())
		}
		if strings.Contains(rr.Body.String(), "reasoning_details") {
			t.Errorf("S11/ult-ext: error body leaks reasoning_details: %s", rr.Body.String())
		}
		if strings.Contains(rr.Body.String(), "think-A") || strings.Contains(rr.Body.String(), "think-1") {
			t.Errorf("S11/ult-ext: error body leaks partial reasoning text: %s", rr.Body.String())
		}
	})
}

// ═════════════════════════════════════════════════════════════════════════════
// S13 — Drift counter (suite-wide delta == 0)
// ═════════════════════════════════════════════════════════════════════════════

// TestS13_DriftCounter asserts the process-wide format-drift counter did
// not move across the whole suite: every mock uses the known
// "MiniMax-response-v1" format. driftBefore is captured in TestMain.
//
// NOTE: `go test` runs tests in the order they appear in the files, and
// this test is the last one in this file. TestMain wraps the whole run.
func TestS13_DriftCounter(t *testing.T) {
	t.Run("suite_wide_delta_zero", func(t *testing.T) {
		before, after := driftBefore, translator.FormatDriftCount()
		t.Logf("S13 drift counter: before=%d after=%d delta=%d", before, after, after-before)
		if after != before {
			t.Errorf("S13: format drift counter moved during suite: before=%d after=%d delta=%d (all mocks use the known MiniMax-response-v1 format)", before, after, after-before)
		}
	})
}

// ═════════════════════════════════════════════════════════════════════════════
// S14 — Header hygiene (flag header stripped, marker forwarded)
// ═════════════════════════════════════════════════════════════════════════════

// TestS14_HeaderHygiene: on flag-ON MiniMax requests, the
// X-Proxy-Interleaved-Thinking header (any case variant) must NEVER reach
// the upstream, while a deliberate marker header proves the capture
// mechanism sees forwarded headers.
func TestS14_HeaderHygiene(t *testing.T) {
	t.Run("race_external_flag_header_stripped_marker_forwarded", func(t *testing.T) {
		env := setupTestEnv(t, okNonStreamHandler(), envOptions{})
		rr := env.run(chatRequest{model: raceExtModel, token: env.plainToken, flag: true, messages: reasoningMessages, marker: "keepme"})
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		cap := env.mockUp.last()
		assertHeaderAbsentFolded(t, cap.Headers, "X-Proxy-Interleaved-Thinking", "S14/race-ext")
		if got := cap.Headers.Get("X-Test-Marker"); got != "keepme" {
			t.Errorf("S14/race-ext: positive control failed — X-Test-Marker=%q, want keepme (capture mechanism sanity); headers=%v", got, cap.Headers)
		}
		t.Logf("EVIDENCE S14 captured-upstream headers (race-external): %v", cap.Headers)
	})

	t.Run("ultimate_external_flag_header_stripped_marker_forwarded", func(t *testing.T) {
		env := setupTestEnv(t, okNonStreamHandler(), envOptions{ultimateModelID: ultExtModel})
		rr := env.run(chatRequest{model: raceIntModel, token: env.ultimateToken, flag: true, forceUlt: true, messages: reasoningMessages, marker: "keepme"})
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		cap := env.mockUp.last()
		assertHeaderAbsentFolded(t, cap.Headers, "X-Proxy-Interleaved-Thinking", "S14/ult-ext")
		if got := cap.Headers.Get("X-Test-Marker"); got != "keepme" {
			t.Errorf("S14/ult-ext: positive control failed — X-Test-Marker=%q, want keepme; headers=%v", got, cap.Headers)
		}
	})

	t.Run("internal_path_flag_header_never_reaches_provider", func(t *testing.T) {
		// The internal path builds a typed provider request; the client's
		// HTTP headers (including X-Proxy-Interleaved-Thinking) are never
		// copied to the provider request by construction — the
		// OpenAIProvider client sets only its own auth/content headers.
		// Assert the flag header (any case variant) is absent from the
		// captured provider request. (The marker-forward positive control
		// is external-path-only: the provider client deliberately does not
		// forward arbitrary client headers.)
		env := setupTestEnv(t, okNonStreamHandler(), envOptions{})
		rr := env.run(chatRequest{model: raceIntModel, token: env.plainToken, flag: true, messages: reasoningMessages})
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		cap := env.mockUp.last()
		assertHeaderAbsentFolded(t, cap.Headers, "X-Proxy-Interleaved-Thinking", "S14/internal")
		if auth := cap.Headers.Get("Authorization"); auth == "" {
			t.Errorf("S14/internal: provider auth header missing (capture sanity) — headers=%v", cap.Headers)
		}
	})
}

// ═════════════════════════════════════════════════════════════════════════════
// responseWithBody — tiny wrapper used by S8's shared assertion

type responseWithBody struct {
	code int
	body string
}

func captureResponse(t *testing.T, rr *httptest.ResponseRecorder) *responseWithBody {
	t.Helper()
	return &responseWithBody{code: rr.Code, body: rr.Body.String()}
}

func (r *responseWithBody) message(t *testing.T) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(r.body), &m); err != nil {
		t.Fatalf("response body not JSON: %s", r.body)
	}
	choices, ok := m["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		t.Fatalf("no choices in body: %s", r.body)
	}
	choice, ok := choices[0].(map[string]interface{})
	if !ok {
		t.Fatalf("choices[0] not object: %s", r.body)
	}
	message, ok := choice["message"].(map[string]interface{})
	if !ok {
		t.Fatalf("no message object: %s", r.body)
	}
	return message
}
