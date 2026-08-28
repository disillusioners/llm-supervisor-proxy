// PATH MATRIX suite — thinking visible in the FE API on EVERY capture path.
//
// The closure gate (Scenario A in e2e_fe_reasoning_observability_test.go)
// proved one path (race-external non-stream). This matrix walks every
// capture path the proxy has and asserts the SAME contract on each:
//
//	POSITIVE rows (R1..R11, M1..M2): upstream sends reasoning; the FE API
//	  GET /fe/api/requests/{id} assistant message `thinking` must equal the
//	  expected reasoning BYTE-EXACT and be non-empty.
//	NEGATIVE rows (N1..N4): upstream sends NO reasoning; the FE payload for
//	  that request id must contain ZERO occurrences of the substring
//	  "thinking" (omitempty cleanliness).
//
// Row map (path mechanics, file:line verified):
//
//	R1  race-external      non-stream  glmModel unregistered (Scenario A)
//	R2  race-external      stream      glmModel + stream:true
//	R3  race-internal      non-stream  raceIntModel Internal:true
//	R4  race-internal      stream      raceIntModel + stream:true
//	R5  ultimate-external  non-stream  ULTIMATE_MODEL_ID=ultExtModel + X-Force-Ultimate-Model
//	R6  ultimate-external  stream      same + stream:true
//	R7  ultimate-internal  non-stream  ULTIMATE_MODEL_ID=ultIntModel + X-Force-Ultimate-Model
//	R8  ultimate-internal  stream      same + stream:true
//	R9  anthropic-client   stream      POST /v1/messages, internal model
//	R10 anthropic-client   non-stream  POST /v1/messages, external upstream
//	R11 anthropic-client   non-stream  POST /v1/messages, internal model —
//	    the S3 cell (live-mode thinking AND content capture, fix 64da4ae)
//	N1..N4  the four race/ultimate paths, non-stream, NO reasoning upstream
//	M1  minimax translated race-external     stream  (reasoning_details → deltas)
//	M2  minimax translated ultimate-external non-stream
//
// X-Force-Ultimate-Model requires a token with ultimateModelEnabled=true
// (pkg/proxy/handler.go:466-469) — setupMatrixEnv creates it. ULTIMATE_MODEL_
// MAX_RETRIES=0 + ForceTrigger fires the ultimate path on the FIRST call.
package e2e_fe_reasoning_observability

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// matrixReasoning is the fixed reasoning constant for positive rows — two
// parts split across stream chunks so accumulation is exercised.
const (
	matrixReasoningPart1 = "First, the user asked about the capital of France; I recall it is Paris. "
	matrixReasoningPart2 = "Second, I should answer concisely without extra detail."
	matrixReasoningFull  = matrixReasoningPart1 + matrixReasoningPart2
)

// matrixUserMessages is the plain request-side conversation (no request-side
// reasoning so the response-side thinking is the ONLY expected thinking).
var matrixUserMessages = []map[string]interface{}{
	{"role": "user", "content": "What is the capital of France?"},
}

// assertFEPositive runs the shared positive-row assertion: fetch the newest
// request over the FE API, require thinking == want byte-exact + non-empty.
// Returns the id used (for the evidence log).
func assertFEPositive(t *testing.T, env *testEnv, row, want string) string {
	t.Helper()
	id := env.latestRequestID(t)
	status, raw := env.feGetRequest(t, id)
	if status != http.StatusOK {
		t.Fatalf("%s: FE detail status=%d body=%s", row, status, string(raw))
	}
	var detail struct {
		ID       string          `json:"id"`
		Messages []feMessageJSON `json:"messages"`
	}
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatalf("%s: FE detail not JSON: %v — %s", row, err, string(raw))
	}
	msgs := detail.Messages
	if len(msgs) < 2 {
		t.Fatalf("%s: expected ≥2 messages, got %d — %s", row, len(msgs), string(raw))
	}
	assistant := msgs[len(msgs)-1]
	if assistant.Role != "assistant" {
		t.Fatalf("%s: last message role=%q, want assistant — %s", row, assistant.Role, string(raw))
	}
	if assistant.Thinking == "" {
		t.Fatalf("%s: REAL FINDING — capture missing on this path. FE assistant.thinking is EMPTY; want %d-byte reasoning. Payload:\n%s", row, len(want), string(raw))
	}
	if assistant.Thinking != want {
		t.Errorf("%s: assistant.thinking mismatch.\n  got  (%d bytes): %q\n  want (%d bytes): %q\n  payload:\n%s",
			row, len(assistant.Thinking), assistant.Thinking, len(want), want, string(raw))
	} else {
		t.Logf("EVIDENCE %s: id=%s assistant.thinking == expected (%d bytes, byte-exact)", row, id, len(want))
	}
	return id
}

// feMessageJSON is the generic message shape (thinking presence-checkable).
type feMessageJSON struct {
	Role     string `json:"role"`
	Content  string `json:"content"`  // visible answer (S3 contract half)
	Thinking string `json:"thinking"` // absent in JSON ⇒ "" after decode
}

// assertFEAssistantContentNonEmpty asserts the CONTENT half of the S3
// contract on an already-asserted positive row: the FE assistant message
// must carry non-empty content. The S3 bug (fixed by 64da4ae) persisted
// BOTH thinking and content empty on live-mode non-stream internal-Anthropic
// — a byte-exact thinking assertion alone would not catch a recurrence that
// drops only content, so R11 checks both halves.
func assertFEAssistantContentNonEmpty(t *testing.T, env *testEnv, row, id string) {
	t.Helper()
	status, raw := env.feGetRequest(t, id)
	if status != http.StatusOK {
		t.Fatalf("%s: FE detail status=%d body=%s", row, status, string(raw))
	}
	var detail struct {
		Messages []feMessageJSON `json:"messages"`
	}
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatalf("%s: FE detail not JSON: %v — %s", row, err, string(raw))
	}
	if len(detail.Messages) == 0 {
		t.Fatalf("%s: FE detail has no messages — %s", row, string(raw))
	}
	assistant := detail.Messages[len(detail.Messages)-1]
	if assistant.Role != "assistant" {
		t.Fatalf("%s: last message role=%q, want assistant — %s", row, assistant.Role, string(raw))
	}
	if assistant.Content == "" {
		t.Errorf("%s: REAL FINDING (S3 class) — FE assistant.content is EMPTY (live non-stream internal capture dropped it). Payload:\n%s", row, string(raw))
	} else {
		t.Logf("EVIDENCE %s: id=%s assistant.content non-empty (%d bytes)", row, id, len(assistant.Content))
	}
}

// assertFENegative runs the shared negative-row assertion: the FE payload
// for the newest request must contain ZERO "thinking" occurrences.
func assertFENegative(t *testing.T, env *testEnv, row string) string {
	t.Helper()
	id := env.latestRequestID(t)
	status, raw := env.feGetRequest(t, id)
	if status != http.StatusOK {
		t.Fatalf("%s: FE detail status=%d body=%s", row, status, string(raw))
	}
	if n := strings.Count(string(raw), "thinking"); n != 0 {
		t.Errorf("%s: FE payload contains %d occurrence(s) of \"thinking\" but upstream sent NO reasoning — payload:\n%s", row, n, string(raw))
	} else {
		t.Logf("EVIDENCE %s: id=%s FE payload has ZERO \"thinking\" occurrences (omitempty clean)", row, id)
	}
	return id
}

// ═════════════════════════════════════════════════════════════════════════════
// Core 10-path matrix
// ═════════════════════════════════════════════════════════════════════════════

// TestPathMatrix_R1_RaceExternal_NonStream is matrix row 1 — the closure
// gate scenario itself, kept as a matrix row (byte-exact FE thinking).
func TestPathMatrix_R1_RaceExternal_NonStream(t *testing.T) {
	env := setupMatrixEnv(t, reasoningNonStreamHandler(matrixReasoningFull), matrixOptions{})
	rr := env.run(chatRequest{model: glmModel, token: env.plainToken, messages: matrixUserMessages})
	if rr.Code != http.StatusOK {
		t.Fatalf("R1: client status=%d body=%s", rr.Code, rr.Body.String())
	}
	assertFEPositive(t, env, "R1", matrixReasoningFull)
}

// TestPathMatrix_R2_RaceExternal_Stream drives the race-external path with
// stream:true and reasoning split across two SSE deltas.
func TestPathMatrix_R2_RaceExternal_Stream(t *testing.T) {
	env := setupMatrixEnv(t, reasoningStreamHandler(matrixReasoningPart1, matrixReasoningPart2), matrixOptions{})
	streamTrue := true
	rr := env.run(chatRequest{model: glmModel, token: env.plainToken, stream: &streamTrue, messages: matrixUserMessages})
	if rr.Code != http.StatusOK {
		t.Fatalf("R2: client status=%d body=%s", rr.Code, rr.Body.String())
	}
	assertFEPositive(t, env, "R2", matrixReasoningFull)
}

// TestPathMatrix_R3_RaceInternal_NonStream drives the registered
// Internal:true model (race-internal coordinator path), non-stream.
func TestPathMatrix_R3_RaceInternal_NonStream(t *testing.T) {
	env := setupMatrixEnv(t, reasoningNonStreamHandler(matrixReasoningFull), matrixOptions{})
	rr := env.run(chatRequest{model: raceIntModel, token: env.plainToken, messages: matrixUserMessages})
	if rr.Code != http.StatusOK {
		t.Fatalf("R3: client status=%d body=%s", rr.Code, rr.Body.String())
	}
	assertFEPositive(t, env, "R3", matrixReasoningFull)
}

// TestPathMatrix_R4_RaceInternal_Stream drives the registered Internal:true
// model with stream:true (typed provider thinking events → buffered SSE).
func TestPathMatrix_R4_RaceInternal_Stream(t *testing.T) {
	env := setupMatrixEnv(t, reasoningStreamHandler(matrixReasoningPart1, matrixReasoningPart2), matrixOptions{})
	streamTrue := true
	rr := env.run(chatRequest{model: raceIntModel, token: env.plainToken, stream: &streamTrue, messages: matrixUserMessages})
	if rr.Code != http.StatusOK {
		t.Fatalf("R4: client status=%d body=%s", rr.Code, rr.Body.String())
	}
	assertFEPositive(t, env, "R4", matrixReasoningFull)
}

// TestPathMatrix_R5_UltimateExternal_NonStream drives X-Force-Ultimate-Model
// with ULTIMATE_MODEL_ID=ultExtModel (Internal:false ⇒ executeExternal).
func TestPathMatrix_R5_UltimateExternal_NonStream(t *testing.T) {
	env := setupMatrixEnv(t, reasoningNonStreamHandler(matrixReasoningFull), matrixOptions{ultimateModelID: ultExtModel})
	rr := env.run(chatRequest{
		model: raceIntModel, token: env.ultimateToken, forceUlt: true,
		messages: matrixUserMessages,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("R5: client status=%d body=%s", rr.Code, rr.Body.String())
	}
	assertFEPositive(t, env, "R5", matrixReasoningFull)
}

// TestPathMatrix_R6_UltimateExternal_Stream is R5 with stream:true
// (executeExternal → streamResponse passive capture).
func TestPathMatrix_R6_UltimateExternal_Stream(t *testing.T) {
	env := setupMatrixEnv(t, reasoningStreamHandler(matrixReasoningPart1, matrixReasoningPart2), matrixOptions{ultimateModelID: ultExtModel})
	streamTrue := true
	rr := env.run(chatRequest{
		model: raceIntModel, token: env.ultimateToken, forceUlt: true, stream: &streamTrue,
		messages: matrixUserMessages,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("R6: client status=%d body=%s", rr.Code, rr.Body.String())
	}
	assertFEPositive(t, env, "R6", matrixReasoningFull)
}

// TestPathMatrix_R7_UltimateInternal_NonStream drives X-Force-Ultimate-Model
// with ULTIMATE_MODEL_ID=ultIntModel (Internal:true ⇒ executeInternal).
func TestPathMatrix_R7_UltimateInternal_NonStream(t *testing.T) {
	env := setupMatrixEnv(t, reasoningNonStreamHandler(matrixReasoningFull), matrixOptions{ultimateModelID: ultIntModel})
	rr := env.run(chatRequest{
		model: raceIntModel, token: env.ultimateToken, forceUlt: true,
		messages: matrixUserMessages,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("R7: client status=%d body=%s", rr.Code, rr.Body.String())
	}
	assertFEPositive(t, env, "R7", matrixReasoningFull)
}

// TestPathMatrix_R8_UltimateInternal_Stream is R7 with stream:true
// (executeInternal → typed thinking events, passive capture).
func TestPathMatrix_R8_UltimateInternal_Stream(t *testing.T) {
	env := setupMatrixEnv(t, reasoningStreamHandler(matrixReasoningPart1, matrixReasoningPart2), matrixOptions{ultimateModelID: ultIntModel})
	streamTrue := true
	rr := env.run(chatRequest{
		model: raceIntModel, token: env.ultimateToken, forceUlt: true, stream: &streamTrue,
		messages: matrixUserMessages,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("R8: client status=%d body=%s", rr.Code, rr.Body.String())
	}
	assertFEPositive(t, env, "R8", matrixReasoningFull)
}

// TestPathMatrix_R9_AnthropicClient_Internal_Stream drives the Anthropic
// Messages endpoint (POST /v1/messages) with a registered INTERNAL model,
// stream:true — doAnthropicInternalRequest → thinking sink → FE persistence.
func TestPathMatrix_R9_AnthropicClient_Internal_Stream(t *testing.T) {
	env := setupMatrixEnv(t, reasoningStreamHandler(matrixReasoningPart1, matrixReasoningPart2), matrixOptions{})
	rr := env.runAnthropic(anthropicRequest{
		model:    anthropicIntModel,
		stream:   true,
		token:    env.plainToken,
		messages: matrixUserMessages,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("R9: client status=%d body=%s", rr.Code, rr.Body.String())
	}
	assertFEPositive(t, env, "R9", matrixReasoningFull)
}

// TestPathMatrix_R10_AnthropicClient_External_NonStream drives the Anthropic
// Messages endpoint with an UNREGISTERED model (external upstream,
// OpenAI-translation mode), non-stream.
func TestPathMatrix_R10_AnthropicClient_External_NonStream(t *testing.T) {
	env := setupMatrixEnv(t, reasoningNonStreamHandler(matrixReasoningFull), matrixOptions{})
	rr := env.runAnthropic(anthropicRequest{
		model:    glmModel,
		stream:   false,
		token:    env.plainToken,
		messages: matrixUserMessages,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("R10: client status=%d body=%s", rr.Code, rr.Body.String())
	}
	assertFEPositive(t, env, "R10", matrixReasoningFull)
}

// TestPathMatrix_R11_AnthropicClient_Internal_NonStream drives the Anthropic
// Messages endpoint (POST /v1/messages) with a registered INTERNAL model,
// stream:false, LIVE mode (no X-LLMProxy-Buffer-Response — the default) —
// doAnthropicInternalRequest → handleNonStream capture (fix 64da4ae) → FE
// persistence. This is the S3 cell: the matrix previously covered R9
// (internal STREAM) and R10 (external NON-STREAM) but not this combination,
// which is exactly where the S3 bug (empty persisted Thinking+Content in
// live mode) hid. Asserts the full S3 contract: byte-exact FE thinking AND
// non-empty FE content for the assistant message.
func TestPathMatrix_R11_AnthropicClient_Internal_NonStream(t *testing.T) {
	env := setupMatrixEnv(t, reasoningNonStreamHandler(matrixReasoningFull), matrixOptions{})
	rr := env.runAnthropic(anthropicRequest{
		model:    anthropicIntModel,
		stream:   false,
		token:    env.plainToken,
		messages: matrixUserMessages,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("R11: client status=%d body=%s", rr.Code, rr.Body.String())
	}
	id := assertFEPositive(t, env, "R11", matrixReasoningFull)
	assertFEAssistantContentNonEmpty(t, env, "R11", id)
}

// ═════════════════════════════════════════════════════════════════════════════
// Negative rows — omitempty cleanliness (NO reasoning upstream)
// ═════════════════════════════════════════════════════════════════════════════

func TestPathMatrix_N1_RaceExternal_NoReasoning(t *testing.T) {
	env := setupMatrixEnv(t, plainNonStreamHandler(), matrixOptions{})
	rr := env.run(chatRequest{model: glmModel, token: env.plainToken, messages: matrixUserMessages})
	if rr.Code != http.StatusOK {
		t.Fatalf("N1: client status=%d body=%s", rr.Code, rr.Body.String())
	}
	assertFENegative(t, env, "N1")
}

func TestPathMatrix_N2_RaceInternal_NoReasoning(t *testing.T) {
	env := setupMatrixEnv(t, plainNonStreamHandler(), matrixOptions{})
	rr := env.run(chatRequest{model: raceIntModel, token: env.plainToken, messages: matrixUserMessages})
	if rr.Code != http.StatusOK {
		t.Fatalf("N2: client status=%d body=%s", rr.Code, rr.Body.String())
	}
	assertFENegative(t, env, "N2")
}

func TestPathMatrix_N3_UltimateExternal_NoReasoning(t *testing.T) {
	env := setupMatrixEnv(t, plainNonStreamHandler(), matrixOptions{ultimateModelID: ultExtModel})
	rr := env.run(chatRequest{
		model: raceIntModel, token: env.ultimateToken, forceUlt: true,
		messages: matrixUserMessages,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("N3: client status=%d body=%s", rr.Code, rr.Body.String())
	}
	assertFENegative(t, env, "N3")
}

func TestPathMatrix_N4_UltimateInternal_NoReasoning(t *testing.T) {
	env := setupMatrixEnv(t, plainNonStreamHandler(), matrixOptions{ultimateModelID: ultIntModel})
	rr := env.run(chatRequest{
		model: raceIntModel, token: env.ultimateToken, forceUlt: true,
		messages: matrixUserMessages,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("N4: client status=%d body=%s", rr.Code, rr.Body.String())
	}
	assertFENegative(t, env, "N4")
}

// ═════════════════════════════════════════════════════════════════════════════
// MiniMax translated rows — reasoning_details → reasoning_content
// ═════════════════════════════════════════════════════════════════════════════

// TestPathMatrix_M1_MiniMax_RaceExternal_Stream drives the MiniMax
// translation gate (credential provider=minimax + X-Proxy-Interleaved-
// Thinking) on race-external STREAM: upstream reasoning_details are
// translated to reasoning_content deltas for the client, and the FE
// thinking must equal the translated text (what the client sees).
func TestPathMatrix_M1_MiniMax_RaceExternal_Stream(t *testing.T) {
	const e1, e2 = "mm-think-one ", "mm-think-two"
	env := setupMatrixEnv(
		t,
		minimaxDetailsStreamHandler("reasoning-text-1", e1, "reasoning-text-2", e2),
		matrixOptions{upstreamProviderMinimax: true},
	)
	streamTrue := true
	rr := env.run(chatRequest{
		model: glmModel, token: env.plainToken, flag: true, stream: &streamTrue,
		messages: matrixUserMessages,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("M1: client status=%d body=%s", rr.Code, rr.Body.String())
	}
	// Sanity: the client stream itself must show the translated deltas.
	if !strings.Contains(rr.Body.String(), "mm-think-one") || !strings.Contains(rr.Body.String(), "mm-think-two") {
		t.Fatalf("M1: client stream does not carry translated reasoning deltas — raw=%s", rr.Body.String())
	}
	assertFEPositive(t, env, "M1", e1+e2)
}

// TestPathMatrix_M2_MiniMax_UltimateExternal_NonStream drives the MiniMax
// translation gate on ultimate-external NON-STREAM: reasoning_details are
// translated into reasoning_content in the response body, and the FE
// thinking must equal the translated text.
func TestPathMatrix_M2_MiniMax_UltimateExternal_NonStream(t *testing.T) {
	const e1, e2 = "mm-ult-one ", "mm-ult-two"
	env := setupMatrixEnv(
		t,
		minimaxDetailsNonStreamHandler(e1, e2),
		// ultimate-external gate reads the MODEL's CredentialID (the
		// minimax harness S9 documents this), so the ultimate model must
		// be MiniMax-credentialed for the gate to fire.
		matrixOptions{ultimateModelID: ultExtModelMiniMax},
	)
	rr := env.run(chatRequest{
		model: raceIntModel, token: env.ultimateToken, flag: true, forceUlt: true,
		messages: matrixUserMessages,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("M2: client status=%d body=%s", rr.Code, rr.Body.String())
	}
	assertFEPositive(t, env, "M2", e1+e2)
}
