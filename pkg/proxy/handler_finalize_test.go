package proxy

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
)

// newMockRequestContext creates a mock requestContext for testing finalize functions.
func newMockRequestContext(t *testing.T) *requestContext {
	t.Helper()

	return &requestContext{
		reqID:     "test-req-" + strings.ReplaceAll(t.Name(), "/", "_"),
		startTime: time.Now().Add(-1 * time.Second),
		reqLog: &store.RequestLog{
			ID:        "test-req-" + strings.ReplaceAll(t.Name(), "/", "_"),
			Status:    "running",
			Model:     "test-model",
			StartTime: time.Now().Add(-1 * time.Second),
			Messages:  []store.Message{},
		},
		modelList: []string{"primary-model", "fallback-model-1", "fallback-model-2"},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// finalizeSuccess tests
// ─────────────────────────────────────────────────────────────────────────────

func TestFinalizeSuccess_SetsStatusToCompleted(t *testing.T) {
	h, upstream := newTestHandler(t, mockLLMHandler(t), models.NewModelsConfig())
	defer upstream.Close()
	rc := newMockRequestContext(t)

	// Verify initial status is "running"
	if rc.reqLog.Status != "running" {
		t.Fatalf("expected initial status 'running', got '%s'", rc.reqLog.Status)
	}

	// Call finalizeSuccess
	h.finalizeSuccess(rc)

	// Verify status is set to "completed"
	if rc.reqLog.Status != "completed" {
		t.Errorf("expected status 'completed', got '%s'", rc.reqLog.Status)
	}
}

func TestFinalizeSuccess_AppendsAssistantMessageWithContent(t *testing.T) {
	h, upstream := newTestHandler(t, mockLLMHandler(t), models.NewModelsConfig())
	defer upstream.Close()
	rc := newMockRequestContext(t)

	// Add some content to accumulated response
	rc.accumulatedResponse.WriteString("Hello, world!")
	rc.accumulatedThinking.WriteString("") // No thinking

	// Record initial message count
	initialMsgCount := len(rc.reqLog.Messages)

	// Call finalizeSuccess
	h.finalizeSuccess(rc)

	// Verify assistant message was appended
	if len(rc.reqLog.Messages) != initialMsgCount+1 {
		t.Errorf("expected %d messages, got %d", initialMsgCount+1, len(rc.reqLog.Messages))
	}

	// Verify the last message is an assistant message with correct content
	lastMsg := rc.reqLog.Messages[len(rc.reqLog.Messages)-1]
	if lastMsg.Role != "assistant" {
		t.Errorf("expected role 'assistant', got '%s'", lastMsg.Role)
	}
	if lastMsg.Content != "Hello, world!" {
		t.Errorf("expected content 'Hello, world!', got '%s'", lastMsg.Content)
	}
}

func TestFinalizeSuccess_AppendsAssistantMessageWithThinking(t *testing.T) {
	h, upstream := newTestHandler(t, mockLLMHandler(t), models.NewModelsConfig())
	defer upstream.Close()
	rc := newMockRequestContext(t)

	// Add content and thinking
	rc.accumulatedResponse.WriteString("Here's the answer.")
	rc.accumulatedThinking.WriteString("Let me think about this carefully.")

	// Call finalizeSuccess
	h.finalizeSuccess(rc)

	// Verify the assistant message has thinking content
	lastMsg := rc.reqLog.Messages[len(rc.reqLog.Messages)-1]
	if lastMsg.Role != "assistant" {
		t.Errorf("expected role 'assistant', got '%s'", lastMsg.Role)
	}
	if lastMsg.Content != "Here's the answer." {
		t.Errorf("expected content 'Here's the answer.', got '%s'", lastMsg.Content)
	}
	if lastMsg.Thinking != "Let me think about this carefully." {
		t.Errorf("expected thinking 'Let me think about this carefully.', got '%s'", lastMsg.Thinking)
	}
}

func TestFinalizeSuccess_AppendsAssistantMessageWithToolCalls(t *testing.T) {
	h, upstream := newTestHandler(t, mockLLMHandler(t), models.NewModelsConfig())
	defer upstream.Close()
	rc := newMockRequestContext(t)

	// Add response content
	rc.accumulatedResponse.WriteString("Let me check the weather.")

	// Add tool call
	tc := store.ToolCall{
		ID:   "call_123",
		Type: "function",
		Function: store.Function{
			Name:      "get_weather",
			Arguments: "", // Will be set from builder
		},
	}
	rc.accumulatedToolCalls = append(rc.accumulatedToolCalls, tc)

	// Add argument builder
	argBuilder := &strings.Builder{}
	argBuilder.WriteString(`{"location": "San Francisco"}`)
	rc.toolCallArgBuilders = append(rc.toolCallArgBuilders, argBuilder)

	// Call finalizeSuccess
	h.finalizeSuccess(rc)

	// Verify the assistant message has tool calls
	lastMsg := rc.reqLog.Messages[len(rc.reqLog.Messages)-1]
	if lastMsg.Role != "assistant" {
		t.Errorf("expected role 'assistant', got '%s'", lastMsg.Role)
	}
	if len(lastMsg.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(lastMsg.ToolCalls))
	}
	if lastMsg.ToolCalls[0].Function.Name != "get_weather" {
		t.Errorf("expected tool name 'get_weather', got '%s'", lastMsg.ToolCalls[0].Function.Name)
	}
	if lastMsg.ToolCalls[0].Function.Arguments != `{"location": "San Francisco"}` {
		t.Errorf("expected arguments '{\"location\": \"San Francisco\"}', got '%s'", lastMsg.ToolCalls[0].Function.Arguments)
	}
}

func TestFinalizeSuccess_SetsEndTimeAndDuration(t *testing.T) {
	h, upstream := newTestHandler(t, mockLLMHandler(t), models.NewModelsConfig())
	defer upstream.Close()
	rc := newMockRequestContext(t)

	// Verify EndTime is zero before finalize
	if !rc.reqLog.EndTime.IsZero() {
		t.Error("expected EndTime to be zero before finalize")
	}

	beforeCall := time.Now()

	// Call finalizeSuccess
	h.finalizeSuccess(rc)

	// Verify EndTime is set
	if rc.reqLog.EndTime.IsZero() {
		t.Error("expected EndTime to be set after finalize")
	}
	if rc.reqLog.EndTime.Before(beforeCall) {
		t.Error("expected EndTime to be >= time.Now() at call")
	}

	// Verify Duration is set (not empty)
	if rc.reqLog.Duration == "" {
		t.Error("expected Duration to be set after finalize")
	}
}

func TestFinalizeSuccess_CallsStoreAdd(t *testing.T) {
	h, upstream := newTestHandler(t, mockLLMHandler(t), models.NewModelsConfig())
	defer upstream.Close()
	rc := newMockRequestContext(t)

	// Verify request is not in store before finalize
	reqID := rc.reqLog.ID
	if h.store.Get(reqID) != nil {
		t.Error("expected request not in store before finalize")
	}

	// Call finalizeSuccess
	h.finalizeSuccess(rc)

	// Verify request is now in store
	storedReq := h.store.Get(reqID)
	if storedReq == nil {
		t.Fatal("expected request to be in store after finalize")
	}
	if storedReq.Status != "completed" {
		t.Errorf("expected stored request status 'completed', got '%s'", storedReq.Status)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// handleModelFailure tests
// ─────────────────────────────────────────────────────────────────────────────

func TestHandleModelFailure_SetsStatusToFailed(t *testing.T) {
	h, upstream := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {}, models.NewModelsConfig())
	defer upstream.Close()
	rc := newMockRequestContext(t)

	// Call handleModelFailure
	h.handleModelFailure(rc, 0, "test-model")

	// Verify status is set to "failed"
	if rc.reqLog.Status != "failed" {
		t.Errorf("expected status 'failed', got '%s'", rc.reqLog.Status)
	}
}

func TestHandleModelFailure_SetsErrorMessage(t *testing.T) {
	h, upstream := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {}, models.NewModelsConfig())
	defer upstream.Close()
	rc := newMockRequestContext(t)

	// Call handleModelFailure
	h.handleModelFailure(rc, 0, "test-model")

	// Verify error is set
	if rc.reqLog.Error != "Model failed" {
		t.Errorf("expected error 'Model failed', got '%s'", rc.reqLog.Error)
	}
}

func TestHandleModelFailure_SetsCurrentFallbackWhenMoreModelsAvailable(t *testing.T) {
	h, upstream := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {}, models.NewModelsConfig())
	defer upstream.Close()
	rc := newMockRequestContext(t)

	// rc.modelList = ["primary-model", "fallback-model-1", "fallback-model-2"]
	// When modelIndex=0 fails, CurrentFallback should be set to "fallback-model-1"

	// Verify CurrentFallback is empty before
	if rc.reqLog.CurrentFallback != "" {
		t.Error("expected CurrentFallback to be empty before handleModelFailure")
	}

	// Call handleModelFailure with modelIndex=0
	h.handleModelFailure(rc, 0, "primary-model")

	// Verify CurrentFallback is set to the next model
	if rc.reqLog.CurrentFallback != "fallback-model-1" {
		t.Errorf("expected CurrentFallback 'fallback-model-1', got '%s'", rc.reqLog.CurrentFallback)
	}
}

func TestHandleModelFailure_DoesNotSetCurrentFallbackWhenNoMoreModels(t *testing.T) {
	h, upstream := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {}, models.NewModelsConfig())
	defer upstream.Close()
	rc := newMockRequestContext(t)

	// When modelIndex=2 (last model) fails, CurrentFallback should NOT be set
	// rc.modelList = ["primary-model", "fallback-model-1", "fallback-model-2"]

	// Call handleModelFailure with modelIndex=2 (last model)
	h.handleModelFailure(rc, 2, "fallback-model-2")

	// Verify CurrentFallback is NOT set (no more fallback models available)
	if rc.reqLog.CurrentFallback != "" {
		t.Errorf("expected CurrentFallback to be empty when no more models, got '%s'", rc.reqLog.CurrentFallback)
	}
}

func TestHandleModelFailure_AddsToFallbackUsedWhenModelIndexGreaterThanZero(t *testing.T) {
	h, upstream := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {}, models.NewModelsConfig())
	defer upstream.Close()
	rc := newMockRequestContext(t)

	// Verify FallbackUsed is empty before
	if len(rc.reqLog.FallbackUsed) != 0 {
		t.Error("expected FallbackUsed to be empty before handleModelFailure")
	}

	// Call handleModelFailure with modelIndex=1 (first fallback)
	h.handleModelFailure(rc, 1, "fallback-model-1")

	// Verify FallbackUsed contains the failed fallback model
	if len(rc.reqLog.FallbackUsed) != 1 {
		t.Fatalf("expected 1 fallback model in FallbackUsed, got %d", len(rc.reqLog.FallbackUsed))
	}
	if rc.reqLog.FallbackUsed[0] != "fallback-model-1" {
		t.Errorf("expected FallbackUsed[0] to be 'fallback-model-1', got '%s'", rc.reqLog.FallbackUsed[0])
	}
}

func TestHandleModelFailure_DoesNotAddToFallbackUsedForMainModel(t *testing.T) {
	h, upstream := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {}, models.NewModelsConfig())
	defer upstream.Close()
	rc := newMockRequestContext(t)

	// Call handleModelFailure with modelIndex=0 (main model)
	h.handleModelFailure(rc, 0, "primary-model")

	// Verify FallbackUsed is still empty (main model failure is not recorded as "fallback used")
	if len(rc.reqLog.FallbackUsed) != 0 {
		t.Errorf("expected FallbackUsed to be empty for main model failure, got %d items", len(rc.reqLog.FallbackUsed))
	}
}

func TestHandleModelFailure_AddsMultipleFallbacksToFallbackUsed(t *testing.T) {
	h, upstream := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {}, models.NewModelsConfig())
	defer upstream.Close()
	rc := newMockRequestContext(t)

	// Simulate multiple fallback failures
	// First fallback fails
	h.handleModelFailure(rc, 1, "fallback-model-1")

	// Second fallback fails
	h.handleModelFailure(rc, 2, "fallback-model-2")

	// Verify both fallbacks are recorded
	if len(rc.reqLog.FallbackUsed) != 2 {
		t.Fatalf("expected 2 fallback models in FallbackUsed, got %d", len(rc.reqLog.FallbackUsed))
	}
	if rc.reqLog.FallbackUsed[0] != "fallback-model-1" {
		t.Errorf("expected FallbackUsed[0] 'fallback-model-1', got '%s'", rc.reqLog.FallbackUsed[0])
	}
	if rc.reqLog.FallbackUsed[1] != "fallback-model-2" {
		t.Errorf("expected FallbackUsed[1] 'fallback-model-2', got '%s'", rc.reqLog.FallbackUsed[1])
	}
}

func TestHandleModelFailure_SetsEndTimeAndDuration(t *testing.T) {
	h, upstream := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {}, models.NewModelsConfig())
	defer upstream.Close()
	rc := newMockRequestContext(t)

	// Verify EndTime is zero before
	if !rc.reqLog.EndTime.IsZero() {
		t.Error("expected EndTime to be zero before handleModelFailure")
	}

	beforeCall := time.Now()

	// Call handleModelFailure
	h.handleModelFailure(rc, 0, "test-model")

	// Verify EndTime is set
	if rc.reqLog.EndTime.IsZero() {
		t.Error("expected EndTime to be set after handleModelFailure")
	}
	if rc.reqLog.EndTime.Before(beforeCall) {
		t.Error("expected EndTime to be >= time.Now() at call")
	}

	// Verify Duration is set (not empty)
	if rc.reqLog.Duration == "" {
		t.Error("expected Duration to be set after handleModelFailure")
	}
}

func TestHandleModelFailure_CallsStoreAdd(t *testing.T) {
	h, upstream := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {}, models.NewModelsConfig())
	defer upstream.Close()
	rc := newMockRequestContext(t)

	reqID := rc.reqLog.ID

	// Verify request is not in store before
	if h.store.Get(reqID) != nil {
		t.Error("expected request not in store before handleModelFailure")
	}

	// Call handleModelFailure
	h.handleModelFailure(rc, 0, "test-model")

	// Verify request is now in store
	storedReq := h.store.Get(reqID)
	if storedReq == nil {
		t.Fatal("expected request to be in store after handleModelFailure")
	}
	if storedReq.Status != "failed" {
		t.Errorf("expected stored request status 'failed', got '%s'", storedReq.Status)
	}
}
