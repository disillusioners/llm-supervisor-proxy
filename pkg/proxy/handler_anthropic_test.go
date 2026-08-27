package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
)

// ─────────────────────────────────────────────────────────────────────────────
// Mock OpenAI Server for Anthropic Tests
// ─────────────────────────────────────────────────────────────────────────────

// mockOpenAICreateChunk creates an OpenAI SSE chunk
func mockOpenAICreateChunk(content string) string {
	chunk := map[string]interface{}{
		"id": "chatcmpl-test",
		"choices": []interface{}{
			map[string]interface{}{
				"delta": map[string]interface{}{
					"content": content,
				},
				"index": 0,
			},
		},
	}
	b, _ := json.Marshal(chunk)
	return string(b)
}

// mockOpenAIReasoningChunk creates a reasoning_content chunk
func mockOpenAIReasoningChunk(content string) string {
	chunk := map[string]interface{}{
		"id": "chatcmpl-test",
		"choices": []interface{}{
			map[string]interface{}{
				"delta": map[string]interface{}{
					"reasoning_content": content,
				},
				"index": 0,
			},
		},
	}
	b, _ := json.Marshal(chunk)
	return string(b)
}

// mockOpenAIToolCallChunk creates a tool call chunk
func mockOpenAIToolCallChunk(id, name, args string) string {
	chunk := map[string]interface{}{
		"id": "chatcmpl-test",
		"choices": []interface{}{
			map[string]interface{}{
				"delta": map[string]interface{}{
					"tool_calls": []interface{}{
						map[string]interface{}{
							"index": 0,
							"id":    id,
							"type":  "function",
							"function": map[string]interface{}{
								"name":      name,
								"arguments": args,
							},
						},
					},
				},
				"index": 0,
			},
		},
	}
	b, _ := json.Marshal(chunk)
	return string(b)
}

// mockOpenAIHandler creates a mock OpenAI server that responds based on keywords
func mockOpenAIHandler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		reqBodyBytes, _ := io.ReadAll(r.Body)
		r.Body.Close()

		var reqBody map[string]interface{}
		json.Unmarshal(reqBodyBytes, &reqBody)

		isStream := true
		if s, ok := reqBody["stream"].(bool); ok && !s {
			isStream = false
		}

		// Extract prompt from messages
		var prompt string
		if msgs, ok := reqBody["messages"].([]interface{}); ok {
			for _, mb := range msgs {
				if msg, ok := mb.(map[string]interface{}); ok {
					if content, ok := msg["content"].(string); ok {
						prompt += content + " "
					}
				}
			}
		}

		// Handle mock-500 error BEFORE any response (for both streaming and non-streaming)
		if strings.Contains(prompt, "mock-500") {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":{"message":"Internal error","type":"server_error"}}`)
			return
		}

		// Non-streaming response
		if !isStream {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			resp := map[string]interface{}{
				"id":      "chatcmpl-test",
				"object":  "chat.completion",
				"created": time.Now().Unix(),
				"model":   "gpt-4o",
				"choices": []interface{}{
					map[string]interface{}{
						"index": 0,
						"message": map[string]interface{}{
							"role":    "assistant",
							"content": "Hello! I am a helpful assistant.",
						},
						"finish_reason": "stop",
					},
				},
				"usage": map[string]interface{}{
					"prompt_tokens":     10,
					"completion_tokens": 8,
					"total_tokens":      18,
				},
			}
			json.NewEncoder(w).Encode(resp)
			return
		}

		// Streaming response
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)

		tokens := []string{"Hello", "!", " I", " am", " a", " helpful", " assistant", "."}

		if strings.Contains(prompt, "mock-think") {
			// Send reasoning_content then content
			thinkTokens := []string{"Hmm", ", ", "thinking", "..."}
			for _, token := range thinkTokens {
				fmt.Fprintf(w, "data: %s\n\n", mockOpenAIReasoningChunk(token))
				flusher.Flush()
			}
			fmt.Fprintf(w, "data: %s\n\n", mockOpenAICreateChunk("Here is the answer."))
			flusher.Flush()
		} else if strings.Contains(prompt, "mock-tool") {
			// Send tool call
			fmt.Fprintf(w, "data: %s\n\n", mockOpenAICreateChunk("Let me check that."))
			flusher.Flush()
			fmt.Fprintf(w, "data: %s\n\n", mockOpenAIToolCallChunk("call_123", "get_weather", `{"location":"SF"}`))
			flusher.Flush()
		} else if strings.Contains(prompt, "mock-hang") {
			// Send some tokens then hang
			for i, token := range tokens {
				if i > 4 {
					<-r.Context().Done()
					return
				}
				fmt.Fprintf(w, "data: %s\n\n", mockOpenAICreateChunk(token))
				flusher.Flush()
			}
		} else {
			// Normal response
			for _, token := range tokens {
				fmt.Fprintf(w, "data: %s\n\n", mockOpenAICreateChunk(token))
				flusher.Flush()
			}
		}

		// Send final chunk with usage
		finalChunk := map[string]interface{}{
			"id": "chatcmpl-test",
			"choices": []interface{}{
				map[string]interface{}{
					"delta":         map[string]interface{}{},
					"index":         0,
					"finish_reason": "stop",
				},
			},
			"usage": map[string]interface{}{
				"prompt_tokens":     10,
				"completion_tokens": 8,
			},
		}
		b, _ := json.Marshal(finalChunk)
		fmt.Fprintf(w, "data: %s\n\n", string(b))
		flusher.Flush()

		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test Helpers
// ─────────────────────────────────────────────────────────────────────────────

// newAnthropicTestHandler creates a handler configured for Anthropic endpoint tests
func newAnthropicTestHandler(t *testing.T, upstreamHandler http.HandlerFunc) (*Handler, *httptest.Server) {
	t.Helper()
	upstream := httptest.NewServer(upstreamHandler)

	t.Setenv("APPLY_ENV_OVERRIDES", "true")
	t.Setenv("UPSTREAM_URL", upstream.URL)

	mgr, err := config.NewManager()
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	cfg := &Config{
		ConfigMgr:    mgr,
		ModelsConfig: models.NewModelsConfig(),
	}

	bus := events.NewBus()
	reqStore := store.NewRequestStore(100)
	h := NewHandler(cfg, bus, reqStore, nil, nil, nil, nil)

	t.Cleanup(func() { upstream.Close() })
	return h, upstream
}

// makeAnthropicRequest creates an Anthropic-style request
func makeAnthropicRequest(t *testing.T, body map[string]interface{}) *http.Request {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "test-key")
	req.Header.Set("anthropic-version", "2023-06-01")
	return req
}

// anthropicBody creates an Anthropic request body
func anthropicBody(model string, stream bool, messages []map[string]interface{}) map[string]interface{} {
	msgs := make([]interface{}, len(messages))
	for i, m := range messages {
		msgs[i] = m
	}
	return map[string]interface{}{
		"model":      model,
		"max_tokens": 1024,
		"stream":     stream,
		"messages":   msgs,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Integration Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestAnthropic_NonStreaming(t *testing.T) {
	h, _ := newAnthropicTestHandler(t, mockOpenAIHandler(t))

	body := anthropicBody("claude-sonnet-4-5", false, []map[string]interface{}{
		{"role": "user", "content": "Hello"},
	})
	req := makeAnthropicRequest(t, body)
	rr := httptest.NewRecorder()

	h.HandleAnthropicMessages(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Verify Anthropic response format
	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	if resp["type"] != "message" {
		t.Errorf("expected type 'message', got %v", resp["type"])
	}
	if resp["role"] != "assistant" {
		t.Errorf("expected role 'assistant', got %v", resp["role"])
	}

	content, ok := resp["content"].([]interface{})
	if !ok {
		t.Fatalf("expected content array, got %T", resp["content"])
	}
	if len(content) == 0 {
		t.Error("expected at least one content block")
	}
}

func TestAnthropic_Streaming(t *testing.T) {
	h, _ := newAnthropicTestHandler(t, mockOpenAIHandler(t))

	body := anthropicBody("claude-sonnet-4-5", true, []map[string]interface{}{
		{"role": "user", "content": "Hello"},
	})
	req := makeAnthropicRequest(t, body)
	rr := httptest.NewRecorder()

	h.HandleAnthropicMessages(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	respBody := rr.Body.String()

	// Verify Anthropic SSE event format
	if !strings.Contains(respBody, "event: message_start") {
		t.Error("expected message_start event")
	}
	if !strings.Contains(respBody, "event: content_block_start") {
		t.Error("expected content_block_start event")
	}
	if !strings.Contains(respBody, "event: content_block_delta") {
		t.Error("expected content_block_delta event")
	}
	if !strings.Contains(respBody, "event: message_stop") {
		t.Error("expected message_stop event")
	}
}

func TestAnthropic_WithSystem(t *testing.T) {
	h, _ := newAnthropicTestHandler(t, mockOpenAIHandler(t))

	body := map[string]interface{}{
		"model":      "claude-sonnet-4-5",
		"max_tokens": 1024,
		"system":     "You are a pirate",
		"stream":     false,
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "Hello"},
		},
	}
	req := makeAnthropicRequest(t, body)
	rr := httptest.NewRecorder()

	h.HandleAnthropicMessages(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Verify request completed successfully
	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["type"] != "message" {
		t.Errorf("expected type 'message', got %v", resp["type"])
	}
}

func TestAnthropic_ToolCall(t *testing.T) {
	h, _ := newAnthropicTestHandler(t, mockOpenAIHandler(t))

	body := anthropicBody("claude-sonnet-4-5", true, []map[string]interface{}{
		{"role": "user", "content": "mock-tool call"},
	})
	req := makeAnthropicRequest(t, body)
	rr := httptest.NewRecorder()

	h.HandleAnthropicMessages(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	respBody := rr.Body.String()

	// Verify tool_use content block in response
	if !strings.Contains(respBody, `"type":"tool_use"`) {
		t.Error("expected tool_use content block in response")
	}
}

func TestAnthropic_ThinkingStream(t *testing.T) {
	h, _ := newAnthropicTestHandler(t, mockOpenAIHandler(t))

	body := anthropicBody("claude-sonnet-4-5", true, []map[string]interface{}{
		{"role": "user", "content": "mock-think please"},
	})
	req := makeAnthropicRequest(t, body)
	rr := httptest.NewRecorder()

	h.HandleAnthropicMessages(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	respBody := rr.Body.String()

	// In streaming mode, thinking is sent as thinking_delta inside a text block
	// The content_block_start has type "text", and thinking is sent via thinking_delta events
	if !strings.Contains(respBody, `"type":"thinking_delta"`) {
		t.Error("expected thinking_delta in response")
	}
}

func TestAnthropic_Error500(t *testing.T) {
	h, _ := newAnthropicTestHandler(t, mockOpenAIHandler(t))

	body := anthropicBody("claude-sonnet-4-5", false, []map[string]interface{}{
		{"role": "user", "content": "mock-500 trigger"},
	})
	req := makeAnthropicRequest(t, body)
	rr := httptest.NewRecorder()

	h.HandleAnthropicMessages(rr, req)

	// Should return error after retries exhausted
	if rr.Code != http.StatusInternalServerError && rr.Code != http.StatusBadGateway {
		t.Logf("Warning: expected 500 or 502, got %d", rr.Code)
	}

	// Verify Anthropic error format
	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	if resp["type"] != "error" {
		t.Errorf("expected type 'error', got %v", resp["type"])
	}
}

func TestAnthropic_InvalidRequest(t *testing.T) {
	h, _ := newAnthropicTestHandler(t, mockOpenAIHandler(t))

	// Missing required fields
	body := map[string]interface{}{
		"model": "claude-sonnet-4-5",
		// missing max_tokens
		// missing messages
	}
	req := makeAnthropicRequest(t, body)
	rr := httptest.NewRecorder()

	h.HandleAnthropicMessages(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}

	// Verify error format
	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	if resp["type"] != "error" {
		t.Errorf("expected type 'error', got %v", resp["type"])
	}
}

func TestAnthropic_AuthWithXAPIKey(t *testing.T) {
	h, _ := newAnthropicTestHandler(t, mockOpenAIHandler(t))

	body := anthropicBody("claude-sonnet-4-5", false, []map[string]interface{}{
		{"role": "user", "content": "Hello"},
	})

	// Use lowercase x-api-key header
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "test-key") // lowercase
	rr := httptest.NewRecorder()

	h.HandleAnthropicMessages(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 with lowercase x-api-key, got %d", rr.Code)
	}
}

func TestAnthropic_MethodNotAllowed(t *testing.T) {
	h, _ := newAnthropicTestHandler(t, mockOpenAIHandler(t))

	req := httptest.NewRequest(http.MethodGet, "/v1/messages", nil)
	rr := httptest.NewRecorder()

	h.HandleAnthropicMessages(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

func TestAnthropic_InvalidJSON(t *testing.T) {
	h, _ := newAnthropicTestHandler(t, mockOpenAIHandler(t))

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.HandleAnthropicMessages(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestAnthropic_Multimodal(t *testing.T) {
	h, _ := newAnthropicTestHandler(t, mockOpenAIHandler(t))

	body := map[string]interface{}{
		"model":      "claude-sonnet-4-5",
		"max_tokens": 1024,
		"stream":     false,
		"messages": []interface{}{
			map[string]interface{}{
				"role": "user",
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": "What's in this?"},
					map[string]interface{}{
						"type": "image",
						"source": map[string]interface{}{
							"type":       "base64",
							"media_type": "image/png",
							"data":       "base64data",
						},
					},
				},
			},
		},
	}
	req := makeAnthropicRequest(t, body)
	rr := httptest.NewRecorder()

	h.HandleAnthropicMessages(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Unit Tests for extractOpenAIResponseContentFromSSE
// ─────────────────────────────────────────────────────────────────────────────

func TestExtractOpenAIResponseContentFromSSE_BasicText(t *testing.T) {
	sseBuffer := `data: {"choices":[{"delta":{"content":"Hello"},"index":0}]}
data: {"choices":[{"delta":{"content":" World"},"index":0}]}
data: [DONE]
`

	content, thinking, toolCalls := extractOpenAIResponseContentFromSSE([]byte(sseBuffer))

	if content != "Hello World" {
		t.Errorf("expected 'Hello World', got %q", content)
	}
	if thinking != "" {
		t.Errorf("expected empty thinking, got %q", thinking)
	}
	if len(toolCalls) != 0 {
		t.Errorf("expected no tool calls, got %d", len(toolCalls))
	}
}

func TestExtractOpenAIResponseContentFromSSE_Thinking(t *testing.T) {
	sseBuffer := `data: {"choices":[{"delta":{"reasoning_content":"Hmm,"},"index":0}]}
data: {"choices":[{"delta":{"reasoning_content":" thinking..."},"index":0}]}
data: [DONE]
`

	content, thinking, toolCalls := extractOpenAIResponseContentFromSSE([]byte(sseBuffer))

	if content != "" {
		t.Errorf("expected empty content, got %q", content)
	}
	if thinking != "Hmm, thinking..." {
		t.Errorf("expected 'Hmm, thinking...', got %q", thinking)
	}
	if len(toolCalls) != 0 {
		t.Errorf("expected no tool calls, got %d", len(toolCalls))
	}
}

func TestExtractOpenAIResponseContentFromSSE_BothThinkingAndText(t *testing.T) {
	sseBuffer := `data: {"choices":[{"delta":{"reasoning_content":"Let me think"},"index":0}]}
data: {"choices":[{"delta":{"content":"Here's the answer."},"index":0}]}
data: [DONE]
`

	content, thinking, toolCalls := extractOpenAIResponseContentFromSSE([]byte(sseBuffer))

	if content != "Here's the answer." {
		t.Errorf("expected 'Here's the answer.', got %q", content)
	}
	if thinking != "Let me think" {
		t.Errorf("expected 'Let me think', got %q", thinking)
	}
	if len(toolCalls) != 0 {
		t.Errorf("expected no tool calls, got %d", len(toolCalls))
	}
}

func TestExtractOpenAIResponseContentFromSSE_ToolCalls(t *testing.T) {
	sseBuffer := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_abc","type":"function","function":{"name":"get_weather","arguments":"{\"location\":"}}]},"index":0}]}
data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"San Francisco\"}"}}]},"index":0}]}
data: [DONE]
`

	content, thinking, toolCalls := extractOpenAIResponseContentFromSSE([]byte(sseBuffer))

	if content != "" {
		t.Errorf("expected empty content, got %q", content)
	}
	if thinking != "" {
		t.Errorf("expected empty thinking, got %q", thinking)
	}
	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(toolCalls))
	}
	if toolCalls[0].ID != "call_abc" {
		t.Errorf("expected tool call ID 'call_abc', got %q", toolCalls[0].ID)
	}
	if toolCalls[0].Function.Name != "get_weather" {
		t.Errorf("expected function name 'get_weather', got %q", toolCalls[0].Function.Name)
	}
	if toolCalls[0].Function.Arguments != `{"location":"San Francisco"}` {
		t.Errorf("expected arguments '{\"location\":\"San Francisco\"}', got %q", toolCalls[0].Function.Arguments)
	}
}

func TestExtractOpenAIResponseContentFromSSE_DoneMarker(t *testing.T) {
	sseBuffer := `data: {"choices":[{"delta":{"content":"Hello"},"index":0}]}
data: [DONE]
`

	content, _, _ := extractOpenAIResponseContentFromSSE([]byte(sseBuffer))

	if content != "Hello" {
		t.Errorf("expected 'Hello', got %q", content)
	}
	// Verify [DONE] doesn't cause a panic or error - function simply skips it
}

func TestExtractOpenAIResponseContentFromSSE_EmptyBuffer(t *testing.T) {
	content, thinking, toolCalls := extractOpenAIResponseContentFromSSE([]byte{})

	if content != "" {
		t.Errorf("expected empty content, got %q", content)
	}
	if thinking != "" {
		t.Errorf("expected empty thinking, got %q", thinking)
	}
	if toolCalls != nil {
		t.Errorf("expected nil tool calls, got %v", toolCalls)
	}
}

func TestExtractOpenAIResponseContentFromSSE_MalformedJSON(t *testing.T) {
	sseBuffer := `data: {"choices":[{"delta":{"content":"Hello"},"index":0}]}
data: not json at all
data: {"choices":[{"delta":{"content":" World"},"index":0}]}
data: [DONE]
`

	content, thinking, _ := extractOpenAIResponseContentFromSSE([]byte(sseBuffer))

	if content != "Hello World" {
		t.Errorf("expected 'Hello World', got %q", content)
	}
	if thinking != "" {
		t.Errorf("expected empty thinking, got %q", thinking)
	}
}

func TestExtractOpenAIResponseContentFromSSE_LargeLine(t *testing.T) {
	largeContent := strings.Repeat("x", 100*1024)
	sseBuffer := fmt.Sprintf(`data: {"choices":[{"delta":{"content":"%s"},"index":0}]}
data: [DONE]
`, largeContent)

	content, thinking, _ := extractOpenAIResponseContentFromSSE([]byte(sseBuffer))

	if content != largeContent {
		t.Errorf("expected large content of length %d, got %d", len(largeContent), len(content))
	}
	if thinking != "" {
		t.Errorf("expected empty thinking, got %q", thinking)
	}
}

func TestExtractOpenAIResponseContentFromSSE_ThinkingField(t *testing.T) {
	sseBuffer := `data: {"choices":[{"delta":{"thinking":"Internal thought"},"index":0}]}
data: [DONE]
`

	content, thinking, toolCalls := extractOpenAIResponseContentFromSSE([]byte(sseBuffer))

	if content != "" {
		t.Errorf("expected empty content, got %q", content)
	}
	if thinking != "Internal thought" {
		t.Errorf("expected 'Internal thought', got %q", thinking)
	}
	if len(toolCalls) != 0 {
		t.Errorf("expected no tool calls, got %d", len(toolCalls))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Unit Tests for extractOpenAIResponseContentFromJSON
// ─────────────────────────────────────────────────────────────────────────────

func TestExtractOpenAIResponseContentFromJSON_Basic(t *testing.T) {
	openaiBody := `{
		"choices": [{
			"message": {
				"role": "assistant",
				"content": "Hello, world!"
			}
		}]
	}`

	content, thinking, toolCalls := extractOpenAIResponseContentFromJSON([]byte(openaiBody))

	if content != "Hello, world!" {
		t.Errorf("expected 'Hello, world!', got %q", content)
	}
	if thinking != "" {
		t.Errorf("expected empty thinking, got %q", thinking)
	}
	if len(toolCalls) != 0 {
		t.Errorf("expected no tool calls, got %d", len(toolCalls))
	}
}

func TestExtractOpenAIResponseContentFromJSON_WithThinking(t *testing.T) {
	openaiBody := `{
		"choices": [{
			"message": {
				"role": "assistant",
				"content": "Here's the answer.",
				"reasoning_content": "Let me think about this..."
			}
		}]
	}`

	content, thinking, toolCalls := extractOpenAIResponseContentFromJSON([]byte(openaiBody))

	if content != "Here's the answer." {
		t.Errorf("expected 'Here's the answer.', got %q", content)
	}
	if thinking != "Let me think about this..." {
		t.Errorf("expected 'Let me think about this...', got %q", thinking)
	}
	if len(toolCalls) != 0 {
		t.Errorf("expected no tool calls, got %d", len(toolCalls))
	}
}

func TestExtractOpenAIResponseContentFromJSON_WithToolCalls(t *testing.T) {
	openaiBody := `{
		"choices": [{
			"message": {
				"role": "assistant",
				"content": "Let me check the weather.",
				"tool_calls": [
					{
						"id": "call_123",
						"type": "function",
						"function": {
							"name": "get_weather",
							"arguments": "{\"location\": \"San Francisco\"}"
						}
					}
				]
			}
		}]
	}`

	content, thinking, toolCalls := extractOpenAIResponseContentFromJSON([]byte(openaiBody))

	if content != "Let me check the weather." {
		t.Errorf("expected 'Let me check the weather.', got %q", content)
	}
	if thinking != "" {
		t.Errorf("expected empty thinking, got %q", thinking)
	}
	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(toolCalls))
	}
	if toolCalls[0].ID != "call_123" {
		t.Errorf("expected ID 'call_123', got %q", toolCalls[0].ID)
	}
	if toolCalls[0].Type != "function" {
		t.Errorf("expected type 'function', got %q", toolCalls[0].Type)
	}
	if toolCalls[0].Function.Name != "get_weather" {
		t.Errorf("expected function name 'get_weather', got %q", toolCalls[0].Function.Name)
	}
	if toolCalls[0].Function.Arguments != `{"location": "San Francisco"}` {
		t.Errorf("expected arguments '{\"location\": \"San Francisco\"}', got %q", toolCalls[0].Function.Arguments)
	}
}

func TestExtractOpenAIResponseContentFromJSON_EmptyChoices(t *testing.T) {
	openaiBody := `{"choices": []}`

	content, thinking, toolCalls := extractOpenAIResponseContentFromJSON([]byte(openaiBody))

	if content != "" {
		t.Errorf("expected empty content, got %q", content)
	}
	if thinking != "" {
		t.Errorf("expected empty thinking, got %q", thinking)
	}
	if toolCalls != nil {
		t.Errorf("expected nil tool calls, got %v", toolCalls)
	}
}

func TestExtractOpenAIResponseContentFromJSON_InvalidJSON(t *testing.T) {
	openaiBody := `not valid json at all {`

	content, thinking, toolCalls := extractOpenAIResponseContentFromJSON([]byte(openaiBody))

	if content != "" {
		t.Errorf("expected empty content, got %q", content)
	}
	if thinking != "" {
		t.Errorf("expected empty thinking, got %q", thinking)
	}
	if toolCalls != nil {
		t.Errorf("expected nil tool calls, got %v", toolCalls)
	}
}

func TestExtractOpenAIResponseContentFromJSON_NoMessage(t *testing.T) {
	openaiBody := `{"choices": [{}]}`

	content, thinking, toolCalls := extractOpenAIResponseContentFromJSON([]byte(openaiBody))

	if content != "" {
		t.Errorf("expected empty content, got %q", content)
	}
	if thinking != "" {
		t.Errorf("expected empty thinking, got %q", thinking)
	}
	if toolCalls != nil {
		t.Errorf("expected nil tool calls, got %v", toolCalls)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// W4: Fallback thinking-isolation tests
// ─────────────────────────────────────────────────────────────────────────────

// newInternalFallbackTestHandler builds a Handler whose ModelsConfig maps
// model "internal-primary" (internal, credential provider=openai pointed at
// internalUpstream) with fallback chain ["external-fallback"], and whose
// external upstream is externalUpstream. Used by the W4 isolation tests to
// drive the real HandleAnthropicMessages flow: internal attempt first, then
// external fallback.
func newInternalFallbackTestHandler(t *testing.T, internalUpstream, externalUpstream http.HandlerFunc) *Handler {
	t.Helper()
	internalSrv := httptest.NewServer(internalUpstream)
	externalSrv := httptest.NewServer(externalUpstream)
	t.Cleanup(func() { internalSrv.Close(); externalSrv.Close() })

	t.Setenv("APPLY_ENV_OVERRIDES", "true")
	t.Setenv("UPSTREAM_URL", externalSrv.URL)

	mgr, err := config.NewManager()
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	modelsCfg := models.NewModelsConfig()
	if err := modelsCfg.AddCredential(models.CredentialConfig{
		ID:       "test-openai-cred",
		Provider: "openai",
		APIKey:   "sk-test",
		BaseURL:  internalSrv.URL,
	}); err != nil {
		t.Fatalf("add credential: %v", err)
	}
	if err := modelsCfg.AddModel(models.ModelConfig{
		ID:            "internal-primary",
		Name:          "Internal Primary",
		Enabled:       true,
		Internal:      true,
		Credentials: models.TestRefs("test-openai-cred"),
		InternalModel: "gpt-4o-internal",
		FallbackChain: []string{"external-fallback"},
	}); err != nil {
		t.Fatalf("add model: %v", err)
	}

	cfg := &Config{
		ConfigMgr:    mgr,
		ModelsConfig: modelsCfg,
	}
	bus := events.NewBus()
	reqStore := store.NewRequestStore(100)
	return NewHandler(cfg, bus, reqStore, nil, nil, nil, nil)
}

// anthropicTestRequestStore retrieves the request store behind the handler
// so tests can inspect the persisted RequestLog (assistant message + thinking).
func anthropicTestRequestStore(h *Handler) *store.RequestStore {
	return h.store
}

// TestAnthropic_FailedInternalFallbackThinkingIsolation exercises the W4
// sequence: the internal (OpenAI-provider) attempt streams SOME thinking
// deltas, then dies mid-stream WITHOUT [DONE]/finish_reason (the provider
// layer surfaces "stream ended without [DONE] marker"), so the internal
// attempt fails AFTER partial thinking was already captured into the sink.
// The external fallback then succeeds and streams its own thinking + content.
//
// The persisted store.Message.Thinking must contain ONLY the fallback's
// thinking — the internal partial thinking must not appear (neither
// concatenated, nor duplicated, nor polluting).
func TestAnthropic_FailedInternalFallbackThinkingIsolation(t *testing.T) {
	// NOTE: this test exercises the W4 thinking-isolation property on
	// the BUFFERED-mode path (real-streaming-default plan, Phase 3,
	// task 3.8 — buffered mode is byte-for-byte legacy). The plan
	// explicitly notes that live mode's preamble emission commits the
	// stream once a byte hits the wire (handler_anthropic.go
	// :256-264 fallback guard fires on arc.headersSent), so a
	// mid-stream failure after the first byte CANNOT fall back —
	// which is the correct live-mode behavior (per plan: "Pre-first-
	// byte fallback unchanged"). The internal upstream below emits
	// ONE thinking chunk before dying, which IS a byte — so in live
	// mode the fallback wouldn't fire. Opting into buffered mode
	// (X-LLMProxy-Buffer-Response: true) keeps the legacy recorder-
	// based semantics that this test was originally written for.
	const internalPartialThinking = "INTERNAL PARTIAL THINKING TOKEN"
	const fallbackThinking = "fallback reasoning"
	const fallbackContent = "fallback answer"

	internalUpstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		// One reasoning delta is consumed and captured by the sink...
		fmt.Fprintf(w, "data: %s\n\n", mockOpenAIReasoningChunk(internalPartialThinking))
		flusher.Flush()
		// ...then the connection dies mid-stream: no further chunks, no
		// finish_reason, no [DONE]. The provider layer turns this into a
		// terminal error ("stream ended without [DONE] marker"), failing the
		// internal attempt AFTER the partial thinking was captured.
		return
	})

	externalUpstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		fmt.Fprintf(w, "data: %s\n\n", mockOpenAIReasoningChunk(fallbackThinking))
		flusher.Flush()
		fmt.Fprintf(w, "data: %s\n\n", mockOpenAICreateChunk(fallbackContent))
		flusher.Flush()
		finalChunk := map[string]interface{}{
			"id": "chatcmpl-test",
			"choices": []interface{}{
				map[string]interface{}{
					"delta":         map[string]interface{}{},
					"index":         0,
					"finish_reason": "stop",
				},
			},
		}
		b, _ := json.Marshal(finalChunk)
		fmt.Fprintf(w, "data: %s\n\n", string(b))
		flusher.Flush()
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	})

	h := newInternalFallbackTestHandler(t, internalUpstream, externalUpstream)

	body := anthropicBody("internal-primary", true, []map[string]interface{}{
		{"role": "user", "content": "Hello"},
	})
	req := makeAnthropicRequest(t, body)
	// Opt into buffered mode — see test comment above for why live
	// mode cannot exercise this fallback path (commit-on-first-byte
	// semantics block model switches after any byte hits the wire).
	req.Header.Set("X-LLMProxy-Buffer-Response", "true")
	rr := httptest.NewRecorder()

	h.HandleAnthropicMessages(rr, req)

	// Sanity: the fallback succeeded and produced a valid Anthropic SSE body.
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 from fallback, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), fallbackContent) {
		t.Errorf("expected fallback content on the wire; body=%q", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "thinking") {
		t.Errorf("expected fallback thinking on the wire (fallback is external; its reasoning is translated normally); body=%q", rr.Body.String())
	}

	// Core W4 assertion: the persisted assistant message must carry ONLY the
	// fallback's thinking.
	reqStore := anthropicTestRequestStore(h)
	logs := reqStore.List()
	if len(logs) == 0 {
		t.Fatal("expected a persisted request log")
	}
	var assistant *store.Message
	for i := range logs[len(logs)-1].Messages {
		if logs[len(logs)-1].Messages[i].Role == "assistant" {
			assistant = &logs[len(logs)-1].Messages[i]
		}
	}
	if assistant == nil {
		t.Fatal("expected a persisted assistant message after fallback success")
	}

	if got := assistant.Thinking; got != fallbackThinking {
		t.Errorf("persisted thinking must be EXACTLY the fallback's thinking %q; got %q (internal partial=%q)",
			fallbackThinking, got, internalPartialThinking)
	}
	if strings.Contains(assistant.Thinking, internalPartialThinking) {
		t.Errorf("internal partial thinking must NOT appear in persisted thinking; got %q", assistant.Thinking)
	}
	if !strings.Contains(assistant.Content, fallbackContent) {
		t.Errorf("persisted content must carry fallback content; got %q", assistant.Content)
	}
}
