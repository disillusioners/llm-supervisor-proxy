package proxy

// fix/minimax-reasoning-translation-gate E2E — primary 429 → race
// coordinator model-fallback → MiniMax receives TRANSLATED wire body.
//
// COVERAGE GAP being filled:
// race_minimax_body_gate_test.go (and the unit matrix in
// race_interleaved_matrix_test.go) covers the gate LOGIC at the
// executeInternalRequest / executeExternalRequest level with a single
// model. They do NOT exercise the full incident path: race coordinator
// with TWO internal models, the primary fails with a real rate-limit
// error (IsRateLimitError returns true), the coordinator spawns the
// fallback via triggerMainError, and the fallback model — configured
// with a MiniMax credential — receives the request from the executor.
//
// This file proves the full path end-to-end:
//
//   1. PRIMARY scripted to 429 → race coordinator sees the rate-limit
//      error and falls through to modelTypeFallback (single-credential
//      per model ⇒ credFailoverEligibleLocked returns false ⇒ no
//      modelTypeCredFailover intercept; triggerMainError spawns
//      c.models[1]).
//
//   2. FALLBACK runs through the full race-coordinator→executeRequest
//      →executeInternalRequest path. The gate fires in
//      executeInternalRequest because hasReasoning=true AND
//      raceIntProviderIsMiniMax=true AND interleaved=false (no
//      X-Proxy-Interleaved-Thinking header on the incident request).
//
//   3. The MiniMax upstream — wired via a real OpenAIProvider pointing
//      at an httptest server — receives the translated wire body. The
//      httptest handler captures the body bytes so the test can assert
//      on the wire format (reasoning_split:true top-level + per-message
//      reasoning_details + absence of raw reasoning_content).
//
// Pre-fix (gate = header-only), this exact request arrived at MiniMax
// with raw reasoning_content and was rejected with 400 (2013). See
// incident 2026-09-25 08:59Z.
//
// TWO TESTS:
//
//   - TestRaceMinimaxFallback_E2E_Primary429_FallbackTranslated — main
//     acceptance test (incident-shaped request → MiniMax fallback now
//     arrives with translated wire body).
//
//   - TestRaceMinimaxFallback_E2E_NonMiniMaxFallback_NotTranslated —
//     guard / symmetry pin: same incident-shaped request routed to a
//     non-MiniMax fallback (openai provider) MUST NOT be translated
//     (no reasoning_split / reasoning_details injection). This proves
//     the widening didn't become unconditional passthrough-translation
//     for every provider.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
)

// streamingFourTwoNineProvider is a providers.Provider that 429s every
// streaming attempt with a *ProviderError that the race coordinator's
// IsRateLimitError classifier recognizes (StatusCode=429 + ErrorType
// contains "rate_limit"). It records every call so the test can assert
// that fallback fired (i.e. fallback provider received a request).
type streamingFourTwoNineProvider struct {
	mu       sync.Mutex
	calls    int32
	sawReq   bool // sawReq flips true the moment StreamChatCompletion is invoked
	lastPath string
}

func (p *streamingFourTwoNineProvider) Name() string { return "streaming-429" }

func (p *streamingFourTwoNineProvider) ChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (*providers.ChatCompletionResponse, error) {
	atomic.AddInt32(&p.calls, 1)
	p.mu.Lock()
	p.sawReq = true
	p.mu.Unlock()
	return nil, &providers.ProviderError{
		Provider:   p.Name(),
		StatusCode: 429,
		Message:    "rate limited (primary)",
		ErrorType:  "rate_limit_error",
		ErrorCode:  "rate_limit",
		Retryable:  true,
	}
}

func (p *streamingFourTwoNineProvider) StreamChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (<-chan providers.StreamEvent, error) {
	atomic.AddInt32(&p.calls, 1)
	p.mu.Lock()
	p.sawReq = true
	p.mu.Unlock()
	return nil, &providers.ProviderError{
		Provider:   p.Name(),
		StatusCode: 429,
		Message:    "rate limited (primary)",
		ErrorType:  "rate_limit_error",
		ErrorCode:  "rate_limit",
		Retryable:  true,
	}
}

func (p *streamingFourTwoNineProvider) IsRetryable(err error) bool { return true }

func (p *streamingFourTwoNineProvider) callCount() int {
	return int(atomic.LoadInt32(&p.calls))
}

func (p *streamingFourTwoNineProvider) sawCall() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sawReq
}

// miniMaxCapturingUpstream is the httptest handler the fallback
// provider's MiniMax credential points at. It captures the wire body
// bytes verbatim and returns a small but valid OpenAI streaming SSE
// response (one content chunk + finish + [DONE]) so the race
// coordinator's winner-eligibility predicate fires.
type miniMaxCapturingUpstream struct {
	mu       sync.Mutex
	body     []byte
	hits     int32
	gotModel string
	gotAuth  string
}

func (u *miniMaxCapturingUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	u.mu.Lock()
	u.body = body
	u.hits++
	u.gotModel = r.Header.Get("X-Stainless-Model") // defensive; OpenAI provider does not set this
	if v := r.URL.Query().Get("model"); v != "" {
		u.gotModel = v
	}
	u.gotAuth = r.Header.Get("Authorization")
	u.mu.Unlock()

	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)

	// Single content chunk — enough to fire the first-byte winner gate
	// in live mode (or the IsCompleted gate in buffered mode).
	fmt.Fprintf(w, "data: {\"id\":\"chatcmpl-minimax-test\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"MiniMax-M3\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":null}]}\n\n")
	if flusher != nil {
		flusher.Flush()
	}
	// Finish chunk — sets finish_reason so the upstream-side IsCompleted
	// gate flips true in buffered mode.
	fmt.Fprintf(w, "data: {\"id\":\"chatcmpl-minimax-test\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"MiniMax-M3\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
	fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

func (u *miniMaxCapturingUpstream) hitCount() int32 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.hits
}

func (u *miniMaxCapturingUpstream) capturedBodyBytes() []byte {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make([]byte, len(u.body))
	copy(out, u.body)
	return out
}

// incidentBodyBytes is the exact wire body shape from the incident
// (2026-09-25 08:59Z): user → assistant-with-reasoning_content +
// tool_calls → tool result → user. NO X-Proxy-Interleaved-Thinking
// header on the request — the test asserts that the body-gate widening
// fires anyway because hasReasoning=true.
//
// Pre-fix: this body arrived at MiniMax with the raw reasoning_content
// string intact and MiniMax rejected it with 400 (2013).
func incidentBodyBytes() []byte {
	// Single-line JSON keeps json.Unmarshal canonicalization deterministic
	// across the body and the captured wire bytes the upstream receives.
	return []byte(`{"model":"glm-agentic","stream":true,"messages":[{"role":"user","content":"What is the weather in SF?"},{"role":"assistant","content":"","reasoning_content":"I should call the weather tool","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\": \"SF\"}"}}]},{"role":"tool","tool_call_id":"call_1","content":"72F sunny"},{"role":"user","content":"Now summarize"}]}`)
}

// installTwoModelEnv wires a 2-internal-model environment for the
// race-coordinator E2E test. Each model has ONE credential (so
// credFailoverEligibleLocked returns false → model fallback path is
// the only viable rescue). Returns the cfg, the two providers
// (primary = scripted 429, fallback = real OpenAIProvider pointed at
// the captured-wire-body httptest server) and the capturing upstream.
//
// fallbackModelID is the model ID used both in the race-coordinator's
// models[] list AND as the ModelConfig.ID — they MUST match so the
// fallback row's req.modelID resolves in cfg.ModelsConfig.GetModel.
// fallbackProviderType is the Provider field on the credential (drives
// raceIntProviderIsMiniMax → the gate).
//
// newProviderClient is replaced with a per-providerType dispatcher;
// t.Cleanup restores the original.
func installTwoModelEnv(
	t *testing.T,
	fallbackModelID, fallbackProviderType, fallbackBaseURL string,
	fallbackProvider providers.Provider,
) (*ConfigSnapshot, *streamingFourTwoNineProvider, *miniMaxCapturingUpstream) {
	t.Helper()

	primary := &streamingFourTwoNineProvider{}
	upstream := &miniMaxCapturingUpstream{}
	_ = upstream // populated lazily when the fallback httptest fires (caller owns the server)

	origNew := newProviderClient
	t.Cleanup(func() { newProviderClient = origNew })
	newProviderClient = func(providerType, apiKey, baseURL string) (providers.Provider, error) {
		switch providerType {
		case "zai":
			return primary, nil
		case fallbackProviderType:
			return fallbackProvider, nil
		default:
			return providers.NewProvider(providerType, apiKey, baseURL)
		}
	}

	cfg := newTestConfigSnapshot("glm-agentic")
	cfg.ModelsConfig = &mockModelsConfig{
		models: []models.ModelConfig{
			{
				ID:            "glm-agentic",
				Name:          "glm-agentic",
				Enabled:       true,
				Internal:      true,
				Credentials:   models.TestRefs("zai-cred"),
				InternalModel: "glm-5",
			},
			{
				ID:            fallbackModelID,
				Name:          fallbackModelID,
				Enabled:       true,
				Internal:      true,
				Credentials:   models.TestRefs(fallbackProviderType + "-cred"),
				InternalModel: "MiniMax-M3",
			},
		},
		credentials: []models.CredentialConfig{
			// primary provider BaseURL is unused — the scripted
			// streamingFourTwoNineProvider never makes an HTTP call, but
			// we still set it to a benign value so log lines are
			// informative if a future test exercises the real zai path.
			{ID: "zai-cred", Provider: "zai", APIKey: "test-zai-key", BaseURL: "http://primary-not-used"},
			// Fallback provider BaseURL is what executeInternalRequest
			// hands to the OpenAIProvider factory. The httptest server
			// URL captures the wire body the race-coordinator → executor
			// path sends — this is the body we assert against below.
			{ID: fallbackProviderType + "-cred", Provider: fallbackProviderType, APIKey: "test-fallback-key", BaseURL: fallbackBaseURL},
		},
	}
	return cfg, primary, upstream
}

// TestRaceMinimaxFallback_E2E_Primary429_FallbackTranslated is the
// acceptance test for the fix/minimax-reasoning-translation-gate
// widening at the race-coordinator boundary. Mirrors the 2026-09-25
// 08:59Z incident:
//
//   - Client sent /v1/chat/completions streaming with tool-carrying
//     assistant messages containing non-empty reasoning_content.
//   - NO X-Proxy-Interleaved-Thinking header on the request.
//   - Primary zai/GLM-5 returned 429 → race coordinator spawned the
//     fallback (modelTypeFallback via triggerMainError) on the
//     configured MiniMax-M3 model.
//
// Assertions (all five must hold):
//
//	(a) fallback fired — the MiniMax httptest upstream actually received
//	    the request (hitCount == 1)
//	(b) captured MiniMax wire body has per-message reasoning_details arrays
//	(c) top-level reasoning_split: true present in the captured body
//	(d) NO raw reasoning_content string key remains in the captured body
//	(e) request SUCCEEDS end-to-end — the client receives a streamed
//	    response with non-empty content
//
// Pre-fix (gate = header-only), this exact request arrived at MiniMax
// with raw reasoning_content and was rejected with 400 (2013) — the
// upstream 429 was lost in the noise of the original 400. With the
// widening, the gate fires on body content and the request reaches
// MiniMax in MiniMax-native wire format.
func TestRaceMinimaxFallback_E2E_Primary429_FallbackTranslated(t *testing.T) {
	// Build a capturing httptest server. The fallback provider is a
	// real OpenAIProvider pointing at it — that way the body bytes
	// captured at the wire are EXACTLY what the proxy would have sent
	// to a real MiniMax upstream (post-json.Marshal of the typed
	// ChatCompletionRequest).
	upstream := &miniMaxCapturingUpstream{}
	srv := httptest.NewServer(upstream)
	t.Cleanup(srv.Close)
	fallbackProvider := providers.NewOpenAIProvider("test-fallback-key", srv.URL)

	cfg, primary, _ := installTwoModelEnv(t, "minimax-coding", "minimax", srv.URL, fallbackProvider)

	// Bounds: keep the test snappy even on the failure path.
	cfg.StreamDeadline = 5 * time.Second
	cfg.MaxGenerationTime = 10 * time.Second
	// Live mode winner gate (matches real production default — handler.go
	// sets this from `!rc.bufferMode && rc.isStream`). Without this,
	// the test would block on the buffered-mode IsCompleted gate and
	// serialize the full stream to completion.
	cfg.RaceParallelOnIdle = true

	body := incidentBodyBytes()

	// http.Request carries NO X-Proxy-Interleaved-Thinking header —
	// this is the incident shape. The thread-down `interleaved=false`
	// into the executor + hasReasoning=true + providerIsMiniMax=true
	// is what fires the gate.
	req := newTestRequest()
	if v := req.Header.Get("X-Proxy-Interleaved-Thinking"); v != "" {
		t.Fatalf("test fixture leaked X-Proxy-Interleaved-Thinking header: %q", v)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	t.Cleanup(cancel)

	// Race coordinator with 2 models. Engine=nil ⇒ cred failover is
	// inert (single-credential-per-model ⇒ credFailoverEligibleLocked
	// returns false anyway, but nil engine keeps the path
	// single-source-of-truth and avoids any binding churn).
	coord := newRaceCoordinatorWithEvents(
		ctx, cfg, req, body,
		[]string{"glm-agentic", "minimax-coding"},
		nil, // bus — irrelevant for this test
		"req-test-fallback-e2e",
		false, // interleaved — incident shape: NO header
		nil,   // engine — nil-safe (no cred failover path needed)
		"conv-key-fallback-e2e",
		true, // liveFirstByteGate — matches live mode (no buffer header)
	)
	coord.Start()

	winner := coord.WaitForWinner()
	if winner == nil {
		t.Fatalf("expected a winner (MiniMax fallback succeeds); spawnTriggers=%v statuses=%v",
			coord.spawnTriggers, coord.GetRequestStatuses())
	}

	// --- Assertion (a): fallback fired. ---
	if winner.modelID != "minimax-coding" {
		t.Fatalf("winner modelID = %q, want minimax-coding (fallback row)", winner.modelID)
	}
	if winner.modelType != modelTypeFallback {
		t.Errorf("winner modelType = %q, want modelTypeFallback", winner.modelType)
	}
	if got := upstream.hitCount(); got != 1 {
		t.Errorf("MiniMax upstream hit count = %d, want 1 (fallback fired exactly once)", got)
	}
	if !primary.sawCall() {
		t.Error("primary scripted provider was never called (race coordinator did not invoke main)")
	}
	if primary.callCount() != 1 {
		t.Errorf("primary provider call count = %d, want 1 (no cred failover, no double-spawn)", primary.callCount())
	}

	// Sanity: the trigger that spawned the fallback row MUST be
	// main_error (NOT rate_limit — single-credential model ⇒ no cred
	// failover intercept). This is what the incident actually exercised.
	foundMainErrorFallback := false
	for _, trig := range coord.spawnTriggers {
		if trig.trigger == triggerMainError {
			foundMainErrorFallback = true
		}
	}
	if !foundMainErrorFallback {
		t.Errorf("expected a triggerMainError spawn (primary 429 ⇒ model fallback); triggers=%v", coord.spawnTriggers)
	}

	// --- Assertions (b)(c)(d): wire body is translated. ---
	captured := upstream.capturedBodyBytes()
	if len(captured) == 0 {
		t.Fatal("MiniMax upstream received an empty body")
	}
	bodyStr := string(captured)

	// (b) per-message reasoning_details array
	if !strings.Contains(bodyStr, `"reasoning_details":[{`) {
		t.Errorf("captured MiniMax wire body missing per-message reasoning_details array:\n%s", bodyStr)
	}
	// The reasoning_details must carry the translated text. Empty array
	// would satisfy the substring check above; pin the content too.
	if !strings.Contains(bodyStr, `"text":"I should call the weather tool"`) {
		t.Errorf("captured wire body missing translated reasoning text:\n%s", bodyStr)
	}
	// And the synthesized type/format/id markers.
	if !strings.Contains(bodyStr, `"type":"reasoning.text"`) {
		t.Errorf("captured wire body missing reasoning.text type marker:\n%s", bodyStr)
	}
	if !strings.Contains(bodyStr, `"format":"MiniMax-response-v1"`) {
		t.Errorf("captured wire body missing MiniMax-response-v1 format marker:\n%s", bodyStr)
	}

	// (c) top-level reasoning_split: true
	if !strings.Contains(bodyStr, `"reasoning_split":true`) {
		t.Errorf("captured MiniMax wire body missing top-level reasoning_split:true:\n%s", bodyStr)
	}

	// (d) NO raw reasoning_content string key remains. The substring
	// check covers both the message and any other nesting shape — the
	// translator strips all per-message reasoning_content strings
	// before the body hits the wire.
	if strings.Contains(bodyStr, `"reasoning_content":`) {
		t.Errorf("captured wire body still has raw reasoning_content string (translation failed):\n%s", bodyStr)
	}

	// --- Assertion (e): client received a streamed response. ---
	if winner.GetError() != nil {
		t.Errorf("winner has error %v (expected success)", winner.GetError())
	}
	chunks, _ := winner.GetBuffer().GetChunksFrom(0)
	if len(chunks) == 0 {
		t.Errorf("winner buffer has zero chunks — client received no streamed response")
	}
	if !bytes.Contains(bytes.Join(chunks, nil), []byte(`"content":"ok"`)) {
		t.Errorf("winner buffer missing expected streamed content chunk: %q", bytes.Join(chunks, nil))
	}

	// Final sanity: original incident-essence messages preserved
	// verbatim through the translation. The user/tool turns are
	// untouched by the translator (it only mutates messages carrying
	// non-empty reasoning_content). Field order is NOT asserted
	// because convertToProviderRequest re-marshals via map[string]any
	// and Go's json.Marshal sorts keys alphabetically.
	if !strings.Contains(bodyStr, `"role":"user","content":"What is the weather in SF?"`) {
		t.Errorf("captured wire body dropped the original user message:\n%s", bodyStr)
	}
	if !strings.Contains(bodyStr, `"role":"tool"`) ||
		!strings.Contains(bodyStr, `"tool_call_id":"call_1"`) ||
		!strings.Contains(bodyStr, `"content":"72F sunny"`) {
		t.Errorf("captured wire body dropped the tool result fields:\n%s", bodyStr)
	}
	if !strings.Contains(bodyStr, `"name":"get_weather"`) {
		t.Errorf("captured wire body dropped the tool-call name:\n%s", bodyStr)
	}
}

// TestRaceMinimaxFallback_E2E_NonMiniMaxFallback_NotTranslated is the
// guard / symmetry pin for the body-gate widening. Same incident-shaped
// request, but the fallback model is configured with an `openai`
// credential (NOT minimax). The gate condition
// `(interleaved || hasReasoning) && raceIntProviderIsMiniMax` MUST
// evaluate to false — the body MUST arrive at the fallback upstream
// unchanged:
//
//   - NO reasoning_split top-level key
//   - NO reasoning_details per-message array
//   - The raw reasoning_content string IS preserved (the openai wire
//     carries it as-is — the body is byte-identical to the input)
//
// This proves the widening didn't become unconditional passthrough-
// translation for every provider. A regression here means a non-MiniMax
// upstream now sees MiniMax wire vocabulary injected into a body it
// can't parse, which would be a breaking change for every non-MiniMax
// fallback.
func TestRaceMinimaxFallback_E2E_NonMiniMaxFallback_NotTranslated(t *testing.T) {
	upstream := &miniMaxCapturingUpstream{}
	srv := httptest.NewServer(upstream)
	t.Cleanup(srv.Close)
	fallbackProvider := providers.NewOpenAIProvider("test-fallback-key", srv.URL)

	cfg, primary, _ := installTwoModelEnv(t, "openai-coding", "openai", srv.URL, fallbackProvider)

	cfg.StreamDeadline = 5 * time.Second
	cfg.MaxGenerationTime = 10 * time.Second
	cfg.RaceParallelOnIdle = true

	body := incidentBodyBytes()

	req := newTestRequest()
	if v := req.Header.Get("X-Proxy-Interleaved-Thinking"); v != "" {
		t.Fatalf("test fixture leaked X-Proxy-Interleaved-Thinking header: %q", v)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	t.Cleanup(cancel)

	coord := newRaceCoordinatorWithEvents(
		ctx, cfg, req, body,
		[]string{"glm-agentic", "openai-coding"},
		nil,
		"req-test-fallback-e2e-guard",
		false, // interleaved
		nil,   // engine
		"conv-key-fallback-e2e-guard",
		true, // liveFirstByteGate
	)
	coord.Start()

	winner := coord.WaitForWinner()
	if winner == nil {
		t.Fatalf("expected a winner; spawnTriggers=%v statuses=%v",
			coord.spawnTriggers, coord.GetRequestStatuses())
	}

	// (a) fallback fired — the openai upstream actually received the request.
	if winner.modelID != "openai-coding" {
		t.Fatalf("winner modelID = %q, want openai-coding (fallback row)", winner.modelID)
	}
	if winner.modelType != modelTypeFallback {
		t.Errorf("winner modelType = %q, want modelTypeFallback", winner.modelType)
	}
	if got := upstream.hitCount(); got != 1 {
		t.Errorf("openai upstream hit count = %d, want 1 (fallback fired exactly once)", got)
	}
	if !primary.sawCall() {
		t.Error("primary scripted provider was never called")
	}

	captured := upstream.capturedBodyBytes()
	if len(captured) == 0 {
		t.Fatal("openai upstream received an empty body")
	}
	bodyStr := string(captured)

	// (b) NO reasoning_split top-level
	if strings.Contains(bodyStr, `"reasoning_split"`) {
		t.Errorf("non-MiniMax wire body unexpectedly carries reasoning_split (gate is provider-scoped):\n%s", bodyStr)
	}
	// (c) NO per-message reasoning_details array
	if strings.Contains(bodyStr, `"reasoning_details"`) {
		t.Errorf("non-MiniMax wire body unexpectedly carries reasoning_details (gate is provider-scoped):\n%s", bodyStr)
	}
	// (d) raw reasoning_content IS preserved — the openai wire carries
	// the original string. This is the byte-identical invariant for the
	// non-MiniMax provider class.
	if !strings.Contains(bodyStr, `"reasoning_content":"I should call the weather tool"`) {
		t.Errorf("non-MiniMax wire body lost the original reasoning_content string:\n%s", bodyStr)
	}

	// (e) request succeeded end-to-end (the wire-body round-trip through
	// the openai upstream is still valid).
	if winner.GetError() != nil {
		t.Errorf("winner has error %v (expected success)", winner.GetError())
	}
	chunks, _ := winner.GetBuffer().GetChunksFrom(0)
	if len(chunks) == 0 {
		t.Errorf("winner buffer has zero chunks — client received no streamed response")
	}
}
