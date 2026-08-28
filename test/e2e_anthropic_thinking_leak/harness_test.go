// Package e2e_anthropic_thinking_leak is the Task-4 spot-check of the
// byte-identity constraint on branch feature/real-streaming-default:
// anthropic-format client streaming with thinking events. The pack is
// re-based against the real-streaming-default D8 wire-shape contract
// (docs/real-streaming-default.md §D8, lines 181-199):
//
//	Buffered mode (X-LLMProxy-Buffer-Response: true) — no thinking bytes on
//	  wire; sink captures concatenated reasoning into the persisted assistant
//	  message (the legacy leak spot-check invariant).
//	Live mode (header absent) — Anthropic thinking content_block +
//	  thinking_delta events ARE emitted on the wire (deliberate wire-shape
//	  change for live mode); sink capture ALSO preserved.
//
// It drives the FULL proxy handler (proxy.Handler.HandleAnthropicMessages)
// in process — the same pattern as test/e2e_minimax_reasoning — wired to a
// capturing mock OpenAI upstream, a temp SQLite database with real
// migrations, a real auth.TokenStore, and the real models config. The
// httptest recorder body IS the client wire.
//
// Scenarios:
//
//	S1A — INTERNAL path, stream, BUFFERED mode: the legacy leak spot-check.
//	     Sends X-LLMProxy-Buffer-Response: true; asserts zero thinking bytes
//	     on wire AND persisted Thinking == concatenated reasoning.
//	S1B — INTERNAL path, stream, LIVE mode: the D8 positive mirror. No
//	     header ⇒ live mode. Asserts thinking_delta + content_block_start
//	     type:thinking ARE emitted on wire, reasoning text chunks appear
//	     INSIDE thinking_delta payloads, reasoning_content stays absent
//	     (no cross-protocol leak), AND persisted Thinking still equals the
//	     concatenated reasoning (sink invariant preserved in live mode).
//	S2  — EXTERNAL path, stream: byte-identity reference. reasoning_content
//	     IS translated to thinking_delta on the wire by design (the
//	     TestAnthropic_ThinkingStream contract) — guards against the sink
//	     over-swallowing.
//	S3  — INTERNAL path, non-stream: persisted thinking == reasoning value;
//	     wire classified against the pre-fix base (fea5874) differential
//	     since translator/response.go is unchanged since base.
//	     (Currently failing for an UNRELATED reason on the non-stream
//	     persistence path — under separate investigation; this pack does
//	     NOT touch S3.)
package e2e_anthropic_thinking_leak

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	_ "modernc.org/sqlite" // SQLite driver

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
)

// ─────────────────────────────────────────────────────────────────────────────
// Constants: reasoning/content strings and model ids
// ─────────────────────────────────────────────────────────────────────────────

// The reasoning text is deliberately split across >= 2 upstream SSE chunks
// (spec requirement) with distinctive, wire-greppable fragments. Each
// fragment is a distinct substring asserted ABSENT from the S1 wire.
const (
	reasoningChunk1 = "Hmm, internal"
	reasoningChunk2 = " deliberation over the leak constraint"
	// reasoningFull is the concatenation the sink must persist in S1.
	reasoningFull = reasoningChunk1 + reasoningChunk2
	// wireContent is the visible answer that must survive translation.
	wireContent = "Visible answer for the anthropic client."
)

const (
	// intModel is registered Internal:true with an openai-provider
	// credential pointed at the mock upstream ⇒ doAnthropicInternalRequest
	// ⇒ InternalHandler + thinking sink (the path the leak lived on).
	intModel = "anthropic-leak-internal"
	// intModelUpstreamName is the InternalModel rewrite target; the
	// captured upstream body's model field must equal it (proof the
	// request actually routed through the internal path).
	intModelUpstreamName = "gpt-internal-leak-test"
	// extModel is UNREGISTERED: resolvedModel=nil ⇒ modelList=[raw name]
	// ⇒ external path via doAnthropicRequest (UPSTREAM_URL/credential).
	extModel = "unregistered-anthropic-external"

	// openaiCredID backs the internal model (provider=openai).
	openaiCredID = "openai-cred"
	// extCredID is the UPSTREAM_CREDENTIAL_ID for the external path.
	extCredID = "ext-openai-cred"
)

// ─────────────────────────────────────────────────────────────────────────────
// Capturing mock upstream
// ─────────────────────────────────────────────────────────────────────────────

// capturedRequest records one upstream request for evidence.
type capturedRequest struct {
	Body       []byte
	BodyParsed map[string]interface{}
	Headers    http.Header
}

type mockUpstream struct {
	mu      sync.Mutex
	history []capturedRequest
}

func (m *mockUpstream) capture(r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body))
	var parsed map[string]interface{}
	_ = json.Unmarshal(body, &parsed)
	m.mu.Lock()
	m.history = append(m.history, capturedRequest{Body: body, BodyParsed: parsed, Headers: r.Header.Clone()})
	m.mu.Unlock()
}

func (m *mockUpstream) last() capturedRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.history) == 0 {
		return capturedRequest{}
	}
	return m.history[len(m.history)-1]
}

// ─────────────────────────────────────────────────────────────────────────────
// Mock upstream handlers (OpenAI SSE / JSON shapes)
// ─────────────────────────────────────────────────────────────────────────────

// sseChunk renders one OpenAI SSE chunk. delta == nil renders a finish-only
// chunk (finish_reason set, no delta key).
func sseChunk(delta map[string]interface{}, finishReason string) string {
	choice := map[string]interface{}{"index": 0}
	if finishReason != "" {
		choice["finish_reason"] = finishReason
	} else {
		choice["delta"] = delta
	}
	chunk := map[string]interface{}{
		"id":      "chatcmpl-mock",
		"object":  "chat.completion.chunk",
		"created": 1755700000,
		"model":   "mock-upstream-model",
		"choices": []interface{}{choice},
	}
	b, _ := json.Marshal(chunk)
	return string(b)
}

// reasoningSSEHandler streams reasoning_content chunks (>=2, per spec) then
// content, a finish chunk, and [DONE] — the exact upstream shape that
// triggered the pre-fix leak.
func reasoningSSEHandler(mock *mockUpstream) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mock.capture(r)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)

		fmt.Fprintf(w, "data: %s\n\n", sseChunk(map[string]interface{}{"reasoning_content": reasoningChunk1}, ""))
		flusher.Flush()
		fmt.Fprintf(w, "data: %s\n\n", sseChunk(map[string]interface{}{"reasoning_content": reasoningChunk2}, ""))
		flusher.Flush()
		fmt.Fprintf(w, "data: %s\n\n", sseChunk(map[string]interface{}{"content": wireContent}, ""))
		flusher.Flush()
		fmt.Fprintf(w, "data: %s\n\n", sseChunk(nil, "stop"))
		flusher.Flush()
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}
}

// reasoningNonStreamHandler returns a 200 JSON body whose assistant message
// carries reasoning_content + content (the S3 non-stream shape).
func reasoningNonStreamHandler(mock *mockUpstream) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mock.capture(r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{
			"id": "chatcmpl-mock-ns",
			"object": "chat.completion",
			"created": 1755700000,
			"model": "mock-upstream-model",
			"choices": [{
				"index": 0,
				"message": {
					"role": "assistant",
					"content": %q,
					"reasoning_content": %q
				},
				"finish_reason": "stop"
			}],
			"usage": {"prompt_tokens": 10, "completion_tokens": 8, "total_tokens": 18}
		}`, wireContent, reasoningFull)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test environment
// ─────────────────────────────────────────────────────────────────────────────

type testEnv struct {
	t          *testing.T
	db         *sql.DB
	tokenStore *auth.TokenStore
	handler    *proxy.Handler
	reqStore   *store.RequestStore
	mockUp     *mockUpstream
	token      string
}

// setupTestEnv wires a full proxy.Handler for the anthropic endpoint.
//
// handlerFactory receives the capturing mock so scenario handlers can record
// upstream requests. One httptest upstream (ephemeral port) serves BOTH
// paths: the internal credential base_url and the external UPSTREAM_URL
// point at it.
func setupTestEnv(t *testing.T, handlerFactory func(*mockUpstream) http.HandlerFunc) *testEnv {
	t.Helper()

	mock := &mockUpstream{}
	upstream := httptest.NewServer(handlerFactory(mock))
	t.Cleanup(upstream.Close)

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("pragma: %v", err)
	}

	dbStore := &database.Store{DB: db, Dialect: database.SQLite}
	t.Cleanup(func() { dbStore.Close() })
	if err := dbStore.RunMigrations(context.Background()); err != nil {
		t.Fatalf("migrations: %v", err)
	}

	tokenStore := auth.NewTokenStore(db, database.SQLite)

	modelsConfig := models.NewModelsConfig()

	// Internal-path credential (openai provider) — provider comes from the
	// credential, so "openai" routes through the InternalHandler code path
	// with the thinking sink.
	if err := modelsConfig.AddCredential(models.CredentialConfig{
		ID:       openaiCredID,
		Provider: "openai",
		APIKey:   "test-key-internal",
		BaseURL:  upstream.URL,
	}); err != nil {
		t.Fatalf("add openai cred: %v", err)
	}

	// External-path credential (UPSTREAM_CREDENTIAL_ID).
	if err := modelsConfig.AddCredential(models.CredentialConfig{
		ID:       extCredID,
		Provider: "openai",
		APIKey:   "test-key-external",
		BaseURL:  upstream.URL,
	}); err != nil {
		t.Fatalf("add ext cred: %v", err)
	}

	if err := modelsConfig.AddModel(models.ModelConfig{
		ID:            intModel,
		Name:          "Anthropic Leak Internal",
		Enabled:       true,
		Internal:      true,
		Credentials: models.TestRefs(openaiCredID),
		InternalModel: intModelUpstreamName,
	}); err != nil {
		t.Fatalf("add model %s: %v", intModel, err)
	}

	// Fast deadlines; APPLY_ENV_OVERRIDES=1 so they take effect.
	t.Setenv("APPLY_ENV_OVERRIDES", "1")
	t.Setenv("MAX_GENERATION_TIME", "10s")
	t.Setenv("UPSTREAM_URL", upstream.URL)
	t.Setenv("UPSTREAM_CREDENTIAL_ID", extCredID)

	cfgMgr, err := config.NewManager()
	if err != nil {
		t.Fatalf("config manager: %v", err)
	}

	bus := events.NewBus()
	reqStore := store.NewRequestStore(100)
	proxyCfg := &proxy.Config{ConfigMgr: cfgMgr, ModelsConfig: modelsConfig}
	handler := proxy.NewHandler(proxyCfg, bus, reqStore, nil, tokenStore, nil, nil)

	// Real tokenStore parity with the T1 harness (the anthropic endpoint
	// itself does not gate on it, but the store wiring must be realistic).
	ctx := context.Background()
	token, _, err := tokenStore.CreateToken(ctx, "leak-test-token", nil, "test", false, "", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	return &testEnv{
		t:          t,
		db:         db,
		tokenStore: tokenStore,
		handler:    handler,
		reqStore:   reqStore,
		mockUp:     mock,
		token:      token,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Anthropic request driver
// ─────────────────────────────────────────────────────────────────────────────

// anthropicRequest describes one anthropic-format client request.
// extraHeaders is an optional map of additional headers applied in build();
// used by S1A to opt into buffered mode via X-LLMProxy-Buffer-Response:true
// (real-streaming-default D8 wire-shape contract — see docs/real-streaming-default.md).
type anthropicRequest struct {
	model        string
	stream       bool
	extraHeaders map[string]string
}

func (ar anthropicRequest) build(t *testing.T) (*http.Request, *httptest.ResponseRecorder) {
	t.Helper()
	body := map[string]interface{}{
		"model":      ar.model,
		"max_tokens": 1024,
		"stream":     ar.stream,
		"messages":   []interface{}{map[string]interface{}{"role": "user", "content": "Trigger the reasoning path."}},
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "leak-spot-check-key")
	req.Header.Set("anthropic-version", "2023-06-01")
	for k, v := range ar.extraHeaders {
		req.Header.Set(k, v)
	}
	return req, httptest.NewRecorder()
}

// run drives one request through the full anthropic handler. The recorder
// body is the client wire.
func (e *testEnv) run(ar anthropicRequest) *httptest.ResponseRecorder {
	req, rr := ar.build(e.t)
	e.handler.HandleAnthropicMessages(rr, req)
	return rr
}

// lastAssistant returns the persisted assistant message from the in-memory
// request store (the store finalizeAnthropicSuccess writes to).
func (e *testEnv) lastAssistant() (assistant store.Message, log *store.RequestLog, ok bool) {
	logs := e.reqStore.List()
	if len(logs) == 0 {
		return store.Message{}, nil, false
	}
	log = logs[0]
	for i := range log.Messages {
		if log.Messages[i].Role == "assistant" {
			return log.Messages[i], log, true
		}
	}
	return store.Message{}, log, false
}
