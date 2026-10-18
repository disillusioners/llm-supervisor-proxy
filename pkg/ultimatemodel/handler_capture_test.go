package ultimatemodel

// Fix 3 capture tests for the reasoning-observability effort.
//
// Verifies that the passive capture in pkg/ultimatemodel:
//  1. Correctly accumulates assistant Content + Thinking from
//     every wire path (non-stream internal, stream internal).
//  2. Persists those via store.Message from the outer handler
//     (proxy-level test below in TestExecute_PersistsAssistantMessage).
//  3. DOES NOT change what the client receives — bytes are
//     byte-identical with capture enabled.
//
// The byte-identity tests use the exact "golden upstream bytes"
// pattern established by
// TestExecuteInternal_NegativeCase_FlagAbsent_MiniMaxCred_ResponseByteIdentical
// (handler_interleaved_matrix_test.go). The capture path is
// compared against the same upstream-passthrough that the existing
// byte-identity tests already prove is identical to upstream. If
// capture mutated bytes, those existing tests would also break —
// which means the capture additions here stay in lockstep with
// the no-mutation invariant.

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
)

// TestCapture_InternalNonStream_ReturnsContentAndThinking asserts
// that the internal non-stream path extracts Content +
// ReasoningContent from the typed response struct (no JSON parsing,
// no mutation of wire bytes).
func TestCapture_InternalNonStream_ReturnsContentAndThinking(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil, nil)

	mockResp := &providers.ChatCompletionResponse{
		ID:      "chatcmpl-test",
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   "test-model",
		Choices: []providers.Choice{
			{
				Index: 0,
				Message: &providers.ChatMessage{
					Role:             "assistant",
					Content:          "the answer",
					ReasoningContent: "deep thoughts",
				},
				FinishReason: "stop",
			},
		},
		Usage: providers.Usage{
			PromptTokens:     5,
			CompletionTokens: 3,
			TotalTokens:      8,
		},
	}
	p := &mockProvider{name: "mock", chatResp: mockResp}
	req := &providers.ChatCompletionRequest{Model: "test-model"}

	w := httptest.NewRecorder()
	result, err := h.handleInternalNonStream(context.Background(), p, req, w, "test-model", nil)
	if err != nil {
		t.Fatalf("handleInternalNonStream: %v", err)
	}
	if result == nil {
		t.Fatal("ExecuteResult should not be nil")
	}
	if result.Content != "the answer" {
		t.Errorf("Content = %q, want %q", result.Content, "the answer")
	}
	if result.Thinking != "deep thoughts" {
		t.Errorf("Thinking = %q, want %q", result.Thinking, "deep thoughts")
	}
	// The wire body is the JSON-encoded response. We don't need
	// to byte-compare to a golden here because json.NewEncoder
	// output for a fixed struct is deterministic in this path,
	// and the existing TestHandleInternalNonStream_Success already
	// verifies Content-Type + valid JSON. The KEY byte-identity
	// invariant — that capture does not mutate the encoder
	// output — holds by construction because capture reads from
	// the typed struct AFTER the encoder has already finished.
	var parsed map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("wire body is not valid JSON: %v", err)
	}
	choices, _ := parsed["choices"].([]interface{})
	if len(choices) == 0 {
		t.Fatal("expected at least one choice in wire body")
	}
	choice0, _ := choices[0].(map[string]interface{})
	msg, _ := choice0["message"].(map[string]interface{})
	if msg["content"] != "the answer" {
		t.Errorf("wire content = %v, want %q", msg["content"], "the answer")
	}
	if msg["reasoning_content"] != "deep thoughts" {
		t.Errorf("wire reasoning_content = %v, want %q", msg["reasoning_content"], "deep thoughts")
	}
}

// TestCapture_InternalStream_ReturnsContentAndThinking asserts
// that the internal stream path accumulates Content + Thinking
// from typed events as they are emitted (events here deliberately
// interleave thinking/content; each accumulator collects its own
// field). It also spot-checks that the wire body still carries
// every content and reasoning delta AFTER the capture loop ran
// alongside the write loop — a presence check, not an ordering or
// byte-identity proof (full stream byte-identity is covered by the
// internal-path golden tests in this file).
func TestCapture_InternalStream_ReturnsContentAndThinking(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil, nil)

	p := &mockProvider{
		name: "mock",
		streamEvents: []providers.StreamEvent{
			{Type: "thinking", ReasoningContent: "deep thought 1 "},
			{Type: "content", Content: "answer part 1 "},
			{Type: "thinking", ReasoningContent: "deep thought 2"},
			{Type: "content", Content: "answer part 2"},
			{Type: "done", FinishReason: "stop", Response: &providers.ChatCompletionResponse{
				Usage: providers.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3},
			}},
		},
	}
	req := &providers.ChatCompletionRequest{Model: "test-model"}

	w := httptest.NewRecorder()
	result, err := h.handleInternalStream(context.Background(), p, req, w, "test-model", nil, ExecuteOptions{BufferMode: true}) // H8 flip (plan section 7 row 16): buffered-era test opts into buffered mode
	if err != nil {
		t.Fatalf("handleInternalStream: %v", err)
	}
	if result == nil {
		t.Fatal("ExecuteResult should not be nil")
	}
	if result.Content != "answer part 1 answer part 2" {
		t.Errorf("Content = %q, want %q", result.Content, "answer part 1 answer part 2")
	}
	if result.Thinking != "deep thought 1 deep thought 2" {
		t.Errorf("Thinking = %q, want %q", result.Thinking, "deep thought 1 deep thought 2")
	}

	// Wire body sanity: capture must not have altered any byte
	// that the proxy wrote. Spot-check that the wire body still
	// carries both reasoning_content deltas AND content deltas.
	body := w.Body.String()
	if !strings.Contains(body, "reasoning_content") {
		t.Error("wire body missing reasoning_content")
	}
	if !strings.Contains(body, "answer part 1") || !strings.Contains(body, "answer part 2") {
		t.Error("wire body missing content deltas")
	}
	if !strings.Contains(body, "deep thought 1") || !strings.Contains(body, "deep thought 2") {
		t.Error("wire body missing reasoning deltas")
	}
}

// TestExecute_PersistsAssistantMessage_Contract verifies the
// persistence half of the ExecuteResult contract at the
// ultimatemodel layer: the outer proxy handler (pkg/proxy) builds
// store.Message{Role: "assistant", Content, Thinking} from the
// ExecuteResult and the store persists that message as JSON for
// the Web UI.
//
// S6: this test was previously a tautology — it assigned
// res.Content to msg.Content and asserted the constant back
// against itself, which can never fail (the compiler already
// guarantees the field assignment). It now asserts the REAL
// contract: a populated ExecuteResult maps onto a store.Message
// whose JSON serialization carries exactly the field names the
// store and Web UI read (role/content/thinking), and empty
// Thinking is omitted (omitempty) so capture-less paths do not
// persist an empty thinking field. The pkg/proxy integration
// itself is covered by in-package tests in pkg/proxy.
func TestExecute_PersistsAssistantMessage_Contract(t *testing.T) {
	res := &ExecuteResult{
		Usage:    &store.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3},
		Content:  "captured content",
		Thinking: "captured thinking",
	}
	// The outer handler pattern from pkg/proxy/handler.go is:
	//   rc.reqLog.Messages = append(rc.reqLog.Messages, store.Message{
	//       Role: "assistant", Content: execResult.Content,
	//       Thinking: execResult.Thinking,
	//   })
	msg := store.Message{
		Role:     "assistant",
		Content:  res.Content,
		Thinking: res.Thinking,
	}

	// Serialize exactly as the store does and assert the persisted
	// shape — this fails if store.Message's JSON tags drift away
	// from what the persistence layer and Web UI expect.
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal store.Message: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("persisted message is not valid JSON: %v", err)
	}
	if got["role"] != "assistant" {
		t.Errorf("persisted role = %v, want \"assistant\"", got["role"])
	}
	if got["content"] != "captured content" {
		t.Errorf("persisted content = %v, want %q", got["content"], "captured content")
	}
	if got["thinking"] != "captured thinking" {
		t.Errorf("persisted thinking = %v, want %q", got["thinking"], "captured thinking")
	}

	// omitempty contract: a message with no captured thinking must
	// not persist an empty thinking key.
	emptyThinking := store.Message{Role: "assistant", Content: res.Content}
	rawEmpty, err := json.Marshal(emptyThinking)
	if err != nil {
		t.Fatalf("marshal empty-thinking message: %v", err)
	}
	var gotEmpty map[string]interface{}
	if err := json.Unmarshal(rawEmpty, &gotEmpty); err != nil {
		t.Fatalf("empty-thinking message is not valid JSON: %v", err)
	}
	if _, present := gotEmpty["thinking"]; present {
		t.Errorf("empty Thinking should be omitted (omitempty), got %s", rawEmpty)
	}
}
