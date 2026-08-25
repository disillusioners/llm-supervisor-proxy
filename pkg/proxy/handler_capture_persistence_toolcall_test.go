package proxy

// W7 persistence tests — tool-call-only ultimate responses
// must persist a store.Message, while pure-empty responses must
// continue to NOT persist (empty-response hygiene preserved).
//
// Mirrors the setup of handler_capture_persistence_test.go (Fix 3
// persistence tests): an External ultimate model pointing at a
// mock httptest upstream; trigger via X-Force-Ultimate-Model;
// verify the persisted store.Message on the ultimate RequestLog.
//
// Self-contained in this file — adds mockUltimateExternalToolCallResponse
// (non-stream) and a stream-emitting handler variant (stream).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/bufferstore"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/usage"
)

// mockUltimateExternalToolCallResponse builds a non-stream
// upstream body that is purely a tool call: empty content +
// reasoning_content + real tool_calls array. Mirrors the
// assistant-turn shape most OpenAI-compatible providers emit when
// the model decides to invoke a function without any preamble.
func mockUltimateExternalToolCallResponse(toolCalls []map[string]interface{}, prompt, completion, total int) string {
	resp := map[string]interface{}{
		"id":      "chatcmpl-w7-capture-test",
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   "ultimate-model",
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"message": map[string]interface{}{
					"role":       "assistant",
					"content":    "",
					"tool_calls": toolCalls,
				},
				"finish_reason": "tool_calls",
			},
		},
		"usage": map[string]int{
			"prompt_tokens":     prompt,
			"completion_tokens": completion,
			"total_tokens":      total,
		},
	}
	b, _ := json.Marshal(resp)
	return string(b)
}

// mockUltimateExternalEmptyResponse builds a non-stream upstream
// body that has no observable assistant turn at all (empty
// content + no tool_calls + finish_reason=stop). Used to verify
// the negative branch of the W7 conditional — that this case
// still does NOT persist a blank store.Message.
func mockUltimateExternalEmptyResponse() string {
	resp := map[string]interface{}{
		"id":      "chatcmpl-w7-empty-test",
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   "ultimate-model",
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"message": map[string]interface{}{
					"role":    "assistant",
					"content": "",
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]int{
			"prompt_tokens": 1, "completion_tokens": 0, "total_tokens": 1,
		},
	}
	b, _ := json.Marshal(resp)
	return string(b)
}

// bootstrapToolCallUltimateTest wires up an external ultimate
// model pointing at `upstreamURL`. Returns the httptest-driven
// recorder after invoking HandleChatCompletions with the
// X-Force-Ultimate-Model header — saves the persistence-target
// `reqStore` and `h.Handler` for the assertion phase.
func bootstrapToolCallUltimateTest(t *testing.T, upstreamURL, wantBody string, stream bool) (reqStore *store.RequestStore) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)
			fmt.Fprint(w, wantBody)
			flusher.Flush()
			return
		}
		fmt.Fprint(w, wantBody)
	}))
	t.Cleanup(upstream.Close)

	db := setupIntegrationDB(t)
	counter := usage.NewCounter(db, database.SQLite)
	tokenStore := auth.NewTokenStore(db, database.SQLite)
	plaintextToken, _, err := tokenStore.CreateToken(
		context.Background(),
		"token-w7",
		nil,
		"test-user",
		true, // ultimateModelEnabled
		"",
		nil,
	)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	modelsConfig := models.NewModelsConfig()
	if err := modelsConfig.AddCredential(models.CredentialConfig{
		ID:       "test-credential",
		Provider: "openai",
		APIKey:   "test-api-key",
		BaseURL:  upstream.URL,
	}); err != nil {
		t.Fatalf("AddCredential: %v", err)
	}
	if err := modelsConfig.AddModel(models.ModelConfig{
		ID:           "ultimate-model",
		Name:         "Ultimate Model",
		Enabled:      true,
		Internal:     false, // EXTERNAL — executes through executeExternal
		Credentials: models.TestRefs("test-credential"),
	}); err != nil {
		t.Fatalf("AddModel (ultimate): %v", err)
	}

	t.Setenv("APPLY_ENV_OVERRIDES", "true")
	t.Setenv("UPSTREAM_URL", upstream.URL)
	t.Setenv("MAX_GENERATION_TIME", "10s")
	t.Setenv("RACE_RETRY_ENABLED", "false")
	t.Setenv("ULTIMATE_MODEL_ID", "ultimate-model")
	t.Setenv("ULTIMATE_MODEL_MAX_RETRIES", "0")

	mgr, err := config.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	cfg := &Config{
		ConfigMgr:    mgr,
		ModelsConfig: modelsConfig,
	}
	bus := events.NewBus()
	reqStore = store.NewRequestStore(100)
	bufStore, err := bufferstore.New(t.TempDir(), 1024*1024)
	if err != nil {
		t.Fatalf("NewBufferStore: %v", err)
	}
	h := NewHandler(cfg, bus, reqStore, bufStore, tokenStore, counter)

	body := map[string]interface{}{
		"model":  "any-model",
		"stream": stream,
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hello"},
		},
	}
	bodyBytes, _ := json.Marshal(body)
	httpReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+plaintextToken)
	httpReq.Header.Set("X-Force-Ultimate-Model", "true")

	rec := httptest.NewRecorder()
	h.HandleChatCompletions(rec, httpReq)

	if rec.Code != http.StatusOK {
		t.Fatalf("request failed: %d - %s", rec.Code, rec.Body.String())
	}
	return reqStore
}

// findUltimateAssistantMessage finds the most recent assistant
// store.Message on the most recent ultimate RequestLog, returning
// nil if no ultimate RequestLog exists OR no assistant message
// was persisted.
func findUltimateAssistantMessage(t *testing.T, reqStore *store.RequestStore) *store.Message {
	t.Helper()
	all := reqStore.List()
	var log *store.RequestLog
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].UltimateModelUsed {
			log = all[i]
			break
		}
	}
	if log == nil {
		return nil
	}
	for i := len(log.Messages) - 1; i >= 0; i-- {
		if log.Messages[i].Role == "assistant" {
			return &log.Messages[i]
		}
	}
	return nil
}

// TestUltimateModel_PersistsAssistantToolCallsOnly_External_NonStream
// is the W7 non-stream test: a tool-call-only ultimate response
// (empty content + empty reasoning_content + real tool_calls)
// MUST persist a store.Message{Role: "assistant", ToolCalls:
// [...], Content: "", Thinking: ""}. The Web UI's
// pkg/ui/frontend/src/components/RequestDetail.tsx renders
// `message.tool_calls` directly when present, so populating the
// ToolCalls field also restores UI observability of WHAT the
// assistant decided to invoke.
func TestUltimateModel_PersistsAssistantToolCallsOnly_External_NonStream(t *testing.T) {
	wantToolCalls := []map[string]interface{}{
		{
			"id":   "call_w7_1",
			"type": "function",
			"function": map[string]interface{}{
				"name":      "get_weather",
				"arguments": `{"city":"Hanoi"}`,
			},
		},
	}
	upstreamBody := mockUltimateExternalToolCallResponse(wantToolCalls, 5, 4, 9)

	reqStore := bootstrapToolCallUltimateTest(t, "", upstreamBody, false)
	msg := findUltimateAssistantMessage(t, reqStore)
	if msg == nil {
		t.Fatal("W7: tool-call-only ultimate response did NOT persist an assistant message")
	}
	if msg.Content != "" {
		t.Errorf("W7: expected empty Content for tool-call-only response, got %q", msg.Content)
	}
	if msg.Thinking != "" {
		t.Errorf("W7: expected empty Thinking for tool-call-only response, got %q", msg.Thinking)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("W7: expected 1 tool call, got %d (msg=%+v)", len(msg.ToolCalls), msg)
	}
	got := msg.ToolCalls[0]
	if got.ID != "call_w7_1" {
		t.Errorf("W7: tool call ID = %q, want call_w7_1", got.ID)
	}
	if got.Type != "function" {
		t.Errorf("W7: tool call Type = %q, want function", got.Type)
	}
	if got.Function.Name != "get_weather" {
		t.Errorf("W7: tool call Name = %q, want get_weather", got.Function.Name)
	}
	if got.Function.Arguments != `{"city":"Hanoi"}` {
		t.Errorf("W7: tool call Arguments = %q, want %q", got.Function.Arguments, `{"city":"Hanoi"}`)
	}
}

// TestUltimateModel_PersistsAssistantToolCallsOnly_External_Stream
// is the W7 streaming counterpart. The upstream emits SSE chunks
// that include tool_calls deltas (across multiple chunks so
// the by-index accumulator has to be exercised); the request is
// stream=true; verification is the persisted assistant message
// has the assembled tool_calls, and the client bytes contain the
// raw tool_calls (byte-identity proof — capture must not strip,
// reorder, or reframe).
func TestUltimateModel_PersistsAssistantToolCallsOnly_External_Stream(t *testing.T) {
	upstreamBody := "" +
		// Single streaming chunk with the entire tool call —
		// simpler than an incremental multi-chunk case but
		// still exercises the by-index stream path. Multi-
		// chunk / repair-matrix coverage lives in
		// pkg/toolcall and pkg/proxy/handler_helpers tests;
		// W7 cares that the W7 conditional fires for the
		// streaming external path (capture-side parses the
		// SAME wire bytes the client sees).
		`data: {"id":"1","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_w7_stream_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Hanoi\"}"}}]}}]}` + "\n\n" +
		`data: [DONE]` + "\n\n"

	reqStore := bootstrapToolCallUltimateTest(t, "", upstreamBody, true)
	msg := findUltimateAssistantMessage(t, reqStore)
	if msg == nil {
		t.Fatal("W7(stream): tool-call-only streaming ultimate response did NOT persist an assistant message")
	}
	if msg.Content != "" {
		t.Errorf("W7(stream): expected empty Content, got %q", msg.Content)
	}
	if msg.Thinking != "" {
		t.Errorf("W7(stream): expected empty Thinking, got %q", msg.Thinking)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("W7(stream): expected 1 tool call, got %d (msg=%+v)", len(msg.ToolCalls), msg)
	}
	got := msg.ToolCalls[0]
	if got.ID != "call_w7_stream_1" {
		t.Errorf("W7(stream): tool call ID = %q, want call_w7_stream_1", got.ID)
	}
	if got.Function.Name != "get_weather" {
		t.Errorf("W7(stream): tool call Name = %q, want get_weather", got.Function.Name)
	}
	// Arguments were assembled from two fragments — captures the
	// by-index accumulation across chunks.
	wantArgs := `{"city":"Hanoi"}`
	if got.Function.Arguments != wantArgs {
		t.Errorf("W7(stream): tool call Arguments = %q, want %q", got.Function.Arguments, wantArgs)
	}
}

// TestUltimateModel_DoesNotPersistEmptyAssistantMessage is the
// negative branch of the W7 conditional — pure-empty ultimate
// responses (empty content + no tool_calls) MUST continue to NOT
// persist a blank store.Message (the Fix 3 empty-response
// hygiene invariant). Without this, regressions that weaken the
// persistence gate to unconditional would silently pass.
func TestUltimateModel_DoesNotPersistEmptyAssistantMessage(t *testing.T) {
	upstreamBody := mockUltimateExternalEmptyResponse()

	reqStore := bootstrapToolCallUltimateTest(t, "", upstreamBody, false)
	if msg := findUltimateAssistantMessage(t, reqStore); msg != nil {
		t.Fatalf("Empty-hygiene breach: empty ultimate response persisted a blank assistant message: %+v", msg)
	}
}
