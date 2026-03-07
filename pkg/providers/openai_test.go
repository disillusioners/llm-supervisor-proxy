package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/toolrepair"
)

func TestOpenAIProvider_Name(t *testing.T) {
	provider := NewOpenAIProvider("test-key", "")
	if provider.Name() != "openai" {
		t.Errorf("expected name 'openai', got %q", provider.Name())
	}
}

func TestOpenAIProvider_ChatCompletion_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify headers
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("expected Authorization header")
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Error("expected Content-Type header")
		}

		// Return success response
		resp := ChatCompletionResponse{
			ID:      "test-id",
			Object:  "chat.completion",
			Created: 1234567890,
			Model:   "gpt-4",
			Choices: []Choice{
				{
					Index: 0,
					Message: &ChatMessage{
						Role:    "assistant",
						Content: "Hello!",
					},
					FinishReason: "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	provider := NewOpenAIProvider("test-key", server.URL)
	req := &ChatCompletionRequest{
		Model:    "gpt-4",
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	}

	resp, err := provider.ChatCompletion(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.ID != "test-id" {
		t.Errorf("expected ID 'test-id', got %q", resp.ID)
	}
	if len(resp.Choices) != 1 {
		t.Errorf("expected 1 choice, got %d", len(resp.Choices))
	}
}

func TestOpenAIProvider_ChatCompletion_Error429(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error": {"message": "Rate limit exceeded", "type": "rate_limit"}}`))
	}))
	defer server.Close()

	provider := NewOpenAIProvider("test-key", server.URL)
	req := &ChatCompletionRequest{
		Model:    "gpt-4",
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	}

	_, err := provider.ChatCompletion(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for 429")
	}

	var providerErr *ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected ProviderError, got %T: %v", err, err)
	}

	if !providerErr.Retryable {
		t.Error("429 error should be retryable")
	}
	if providerErr.StatusCode != 429 {
		t.Errorf("expected status 429, got %d", providerErr.StatusCode)
	}
}

func TestOpenAIProvider_ChatCompletion_Error401(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error": {"message": "Invalid API key", "type": "invalid_key"}}`))
	}))
	defer server.Close()

	provider := NewOpenAIProvider("test-key", server.URL)
	req := &ChatCompletionRequest{
		Model:    "gpt-4",
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	}

	_, err := provider.ChatCompletion(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for 401")
	}

	var providerErr *ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected ProviderError, got %T: %v", err, err)
	}

	if providerErr.Retryable {
		t.Error("401 error should not be retryable")
	}
}

func TestOpenAIProvider_IsRetryable(t *testing.T) {
	provider := &OpenAIProvider{}

	retryableErr := &ProviderError{Retryable: true}
	if !provider.IsRetryable(retryableErr) {
		t.Error("retryable error should return true")
	}

	nonRetryableErr := &ProviderError{Retryable: false}
	if provider.IsRetryable(nonRetryableErr) {
		t.Error("non-retryable error should return false")
	}
}

func TestOpenAIProvider_StreamChatCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")

		// Send SSE events
		w.Write([]byte("data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-4\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Hello\"}}]}\n\n"))
		w.Write([]byte("data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-4\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\" world\"}}]}\n\n"))
		w.Write([]byte("data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-4\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	provider := NewOpenAIProvider("test-key", server.URL)
	req := &ChatCompletionRequest{
		Model:    "gpt-4",
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
		Stream:   true,
	}

	eventCh, err := provider.StreamChatCompletion(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var events []StreamEvent
	for event := range eventCh {
		events = append(events, event)
	}

	// Should have at least 2 content events and 1 done event
	if len(events) < 3 {
		t.Errorf("expected at least 3 events, got %d", len(events))
	}

	// Check first event is content
	if events[0].Type != "content" {
		t.Errorf("expected first event type 'content', got %q", events[0].Type)
	}
	if events[0].Content != "Hello" {
		t.Errorf("expected content 'Hello', got %q", events[0].Content)
	}

	// Check last event is done
	lastEvent := events[len(events)-1]
	if lastEvent.Type != "done" {
		t.Errorf("expected last event type 'done', got %q", lastEvent.Type)
	}
}

// =============================================================================
// Tool Repair Integration Tests with Mock LLM
// =============================================================================

type mockToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// mockToolCallResponse creates a non-streaming response with tool calls
func mockToolCallResponse(toolCalls []mockToolCall) ChatCompletionResponse {
	tc := make([]ToolCall, len(toolCalls))
	for i, call := range toolCalls {
		tc[i] = ToolCall{
			ID:   call.ID,
			Type: "function",
			Function: ToolCallFunction{
				Name:      call.Name,
				Arguments: call.Arguments,
			},
		}
	}
	return ChatCompletionResponse{
		ID:      "test-id",
		Object:  "chat.completion",
		Created: 1234567890,
		Model:   "gpt-4",
		Choices: []Choice{
			{
				Index: 0,
				Message: &ChatMessage{
					Role:      "assistant",
					Content:   "",
					ToolCalls: tc,
				},
				FinishReason: "tool_calls",
			},
		},
	}
}

// TestOpenAIProvider_ToolRepair_ValidJSON tests that valid JSON passes through unchanged
func TestOpenAIProvider_ToolRepair_ValidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := mockToolCallResponse([]mockToolCall{
			{ID: "call_1", Name: "get_weather", Arguments: `{"location":"San Francisco","unit":"celsius"}`},
		})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	provider := NewOpenAIProvider("test-key", server.URL)
	provider.SetRepairer(toolrepair.NewRepairer(toolrepair.DefaultConfig()))

	req := &ChatCompletionRequest{
		Model:    "gpt-4",
		Messages: []ChatMessage{{Role: "user", Content: "What's the weather?"}},
	}

	resp, err := provider.ChatCompletion(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Arguments should be unchanged
	args := resp.Choices[0].Message.ToolCalls[0].Function.Arguments
	expected := `{"location":"San Francisco","unit":"celsius"}`
	if args != expected {
		t.Errorf("expected arguments %q, got %q", expected, args)
	}
}

// TestOpenAIProvider_ToolRepair_MalformedJSON_ExtractFromText tests extracting JSON from surrounding text
func TestOpenAIProvider_ToolRepair_MalformedJSON_ExtractFromText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// LLM wraps JSON in explanatory text
		resp := mockToolCallResponse([]mockToolCall{
			{
				ID:        "call_1",
				Name:      "get_weather",
				Arguments: `Here is the data: {"location":"Tokyo","unit":"fahrenheit"} end.`,
			},
		})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	provider := NewOpenAIProvider("test-key", server.URL)
	provider.SetRepairer(toolrepair.NewRepairer(toolrepair.DefaultConfig()))

	req := &ChatCompletionRequest{
		Model:    "gpt-4",
		Messages: []ChatMessage{{Role: "user", Content: "Weather?"}},
	}

	resp, err := provider.ChatCompletion(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Arguments should be repaired to valid JSON
	args := resp.Choices[0].Message.ToolCalls[0].Function.Arguments

	// Verify it's valid JSON
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(args), &parsed); err != nil {
		t.Errorf("repaired arguments should be valid JSON, got: %q, error: %v", args, err)
	}

	// Check values are preserved
	if parsed["location"] != "Tokyo" {
		t.Errorf("expected location 'Tokyo', got %v", parsed["location"])
	}
}

// TestOpenAIProvider_ToolRepair_MalformedJSON_UnclosedBrackets tests library repair for unclosed brackets
func TestOpenAIProvider_ToolRepair_MalformedJSON_UnclosedBrackets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// LLM sends JSON with missing closing brace
		resp := mockToolCallResponse([]mockToolCall{
			{
				ID:        "call_1",
				Name:      "search",
				Arguments: `{"query":"test query","limit":10`,
			},
		})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Use config with library_repair strategy (handles unclosed brackets)
	config := &toolrepair.Config{
		Enabled:    true,
		Strategies: []string{"library_repair"},
	}
	provider := NewOpenAIProvider("test-key", server.URL)
	provider.SetRepairer(toolrepair.NewRepairer(config))

	req := &ChatCompletionRequest{
		Model:    "gpt-4",
		Messages: []ChatMessage{{Role: "user", Content: "Search"}},
	}

	resp, err := provider.ChatCompletion(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	args := resp.Choices[0].Message.ToolCalls[0].Function.Arguments

	// Verify it's valid JSON
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(args), &parsed); err != nil {
		t.Errorf("repaired arguments should be valid JSON, got: %q, error: %v", args, err)
	}
}

// TestOpenAIProvider_ToolRepair_MalformedJSON_TrailingComma tests library repair for trailing commas
func TestOpenAIProvider_ToolRepair_MalformedJSON_TrailingComma(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// LLM sends JSON with trailing comma
		resp := mockToolCallResponse([]mockToolCall{
			{
				ID:        "call_1",
				Name:      "execute",
				Arguments: `{"command":"ls","args":["-la",]}`,
			},
		})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	provider := NewOpenAIProvider("test-key", server.URL)
	provider.SetRepairer(toolrepair.NewRepairer(toolrepair.DefaultConfig()))

	req := &ChatCompletionRequest{
		Model:    "gpt-4",
		Messages: []ChatMessage{{Role: "user", Content: "Execute"}},
	}

	resp, err := provider.ChatCompletion(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	args := resp.Choices[0].Message.ToolCalls[0].Function.Arguments

	// Verify it's valid JSON
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(args), &parsed); err != nil {
		t.Errorf("repaired arguments should be valid JSON, got: %q, error: %v", args, err)
	}
}

// TestOpenAIProvider_ToolRepair_MultipleToolCalls tests repairing multiple tool calls with mixed validity
func TestOpenAIProvider_ToolRepair_MultipleToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := mockToolCallResponse([]mockToolCall{
			{ID: "call_1", Name: "tool_a", Arguments: `{"valid":"json"}`},                       // Valid
			{ID: "call_2", Name: "tool_b", Arguments: `{"broken":"missing_close"`},              // Invalid - missing }
			{ID: "call_3", Name: "tool_c", Arguments: `Text before {"key":"value"} text after`}, // Invalid - embedded
		})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	provider := NewOpenAIProvider("test-key", server.URL)
	provider.SetRepairer(toolrepair.NewRepairer(toolrepair.DefaultConfig()))

	req := &ChatCompletionRequest{
		Model:    "gpt-4",
		Messages: []ChatMessage{{Role: "user", Content: "Multi"}},
	}

	resp, err := provider.ChatCompletion(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All arguments should be valid JSON after repair
	for i, tc := range resp.Choices[0].Message.ToolCalls {
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &parsed); err != nil {
			t.Errorf("tool call %d arguments should be valid JSON, got: %q, error: %v", i, tc.Function.Arguments, err)
		}
	}
}

// TestOpenAIProvider_ToolRepair_Disabled tests that repair can be disabled
func TestOpenAIProvider_ToolRepair_Disabled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := mockToolCallResponse([]mockToolCall{
			{ID: "call_1", Name: "tool", Arguments: `{"broken":`}, // Invalid JSON
		})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	provider := NewOpenAIProvider("test-key", server.URL)
	provider.SetRepairer(toolrepair.NewRepairer(toolrepair.DisabledConfig())) // Disabled

	req := &ChatCompletionRequest{
		Model:    "gpt-4",
		Messages: []ChatMessage{{Role: "user", Content: "Test"}},
	}

	resp, err := provider.ChatCompletion(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Arguments should be unchanged (still invalid)
	args := resp.Choices[0].Message.ToolCalls[0].Function.Arguments
	if args != `{"broken":` {
		t.Errorf("expected unchanged invalid JSON, got: %q", args)
	}
}

// TestOpenAIProvider_ToolRepair_NoRepairer tests behavior without repairer configured
func TestOpenAIProvider_ToolRepair_NoRepairer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := mockToolCallResponse([]mockToolCall{
			{ID: "call_1", Name: "tool", Arguments: `{"broken":`},
		})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	provider := NewOpenAIProvider("test-key", server.URL)
	// No repairer configured

	req := &ChatCompletionRequest{
		Model:    "gpt-4",
		Messages: []ChatMessage{{Role: "user", Content: "Test"}},
	}

	resp, err := provider.ChatCompletion(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Arguments should be unchanged
	args := resp.Choices[0].Message.ToolCalls[0].Function.Arguments
	if args != `{"broken":` {
		t.Errorf("expected unchanged invalid JSON, got: %q", args)
	}
}

// TestOpenAIProvider_ToolRepair_SingleQuotes tests repair of single-quoted JSON
func TestOpenAIProvider_ToolRepair_SingleQuotes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// LLM uses single quotes instead of double quotes
		resp := mockToolCallResponse([]mockToolCall{
			{
				ID:        "call_1",
				Name:      "search",
				Arguments: `{'query':'test','limit':10}`,
			},
		})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	provider := NewOpenAIProvider("test-key", server.URL)
	provider.SetRepairer(toolrepair.NewRepairer(toolrepair.DefaultConfig()))

	req := &ChatCompletionRequest{
		Model:    "gpt-4",
		Messages: []ChatMessage{{Role: "user", Content: "Search"}},
	}

	resp, err := provider.ChatCompletion(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	args := resp.Choices[0].Message.ToolCalls[0].Function.Arguments

	// Verify it's valid JSON
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(args), &parsed); err != nil {
		t.Errorf("repaired arguments should be valid JSON, got: %q, error: %v", args, err)
	}

	if parsed["query"] != "test" {
		t.Errorf("expected query 'test', got %v", parsed["query"])
	}
}

// TestOpenAIProvider_ToolRepair_EmptyArguments tests handling empty arguments
func TestOpenAIProvider_ToolRepair_EmptyArguments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := mockToolCallResponse([]mockToolCall{
			{ID: "call_1", Name: "no_args_tool", Arguments: `{}`},
		})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	provider := NewOpenAIProvider("test-key", server.URL)
	provider.SetRepairer(toolrepair.NewRepairer(toolrepair.DefaultConfig()))

	req := &ChatCompletionRequest{
		Model:    "gpt-4",
		Messages: []ChatMessage{{Role: "user", Content: "No args"}},
	}

	resp, err := provider.ChatCompletion(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	args := resp.Choices[0].Message.ToolCalls[0].Function.Arguments
	if args != `{}` {
		t.Errorf("expected empty object, got: %q", args)
	}
}

// TestOpenAIProvider_ToolRepair_SizeLimit tests size limit enforcement
func TestOpenAIProvider_ToolRepair_SizeLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Large arguments
		largeArgs := fmt.Sprintf(`{"data":"%s"}`, strings.Repeat("x", 20*1024))
		resp := mockToolCallResponse([]mockToolCall{
			{ID: "call_1", Name: "large_tool", Arguments: largeArgs},
		})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Config with 10KB limit
	config := &toolrepair.Config{
		Enabled:          true,
		MaxArgumentsSize: 10 * 1024, // 10KB
		Strategies:       []string{"library_repair"},
	}
	provider := NewOpenAIProvider("test-key", server.URL)
	provider.SetRepairer(toolrepair.NewRepairer(config))

	req := &ChatCompletionRequest{
		Model:    "gpt-4",
		Messages: []ChatMessage{{Role: "user", Content: "Large"}},
	}

	resp, err := provider.ChatCompletion(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Arguments should be unchanged (too large to repair)
	args := resp.Choices[0].Message.ToolCalls[0].Function.Arguments
	if !strings.Contains(args, "xxxxx") {
		t.Error("expected large arguments to be passed through unchanged")
	}
}

// TestOpenAIProvider_ToolRepair_Streaming tests tool repair with streaming responses
// NOTE: This test verifies that streaming works correctly. Tool call repair in streaming
// mode requires the accumulated tool calls to be in Message.ToolCalls, which happens
// when the caller accumulates deltas. The final chunk with finish_reason only has Delta.
func TestOpenAIProvider_ToolRepair_Streaming(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")

		// Send streaming content chunks
		w.Write([]byte(`data: {"id":"stream-1","object":"chat.completion.chunk","created":1,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"}}]}` + "\n\n"))
		w.Write([]byte(`data: {"id":"stream-1","object":"chat.completion.chunk","created":1,"model":"gpt-4","choices":[{"index":0,"delta":{"content":" world"}}]}` + "\n\n"))
		w.Write([]byte(`data: {"id":"stream-1","object":"chat.completion.chunk","created":1,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	provider := NewOpenAIProvider("test-key", server.URL)
	provider.SetRepairer(toolrepair.NewRepairer(toolrepair.DefaultConfig()))

	req := &ChatCompletionRequest{
		Model:    "gpt-4",
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
		Stream:   true,
	}

	eventCh, err := provider.StreamChatCompletion(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var events []StreamEvent
	for event := range eventCh {
		events = append(events, event)
	}

	// Should have content events and done event
	if len(events) < 2 {
		t.Errorf("expected at least 2 events, got %d", len(events))
	}

	// Check last event is done
	lastEvent := events[len(events)-1]
	if lastEvent.Type != "done" {
		t.Errorf("expected last event type 'done', got %q", lastEvent.Type)
	}
}

// TestOpenAIProvider_ToolRepair_StreamingToolCalls tests that tool call deltas are sent as events
func TestOpenAIProvider_ToolRepair_StreamingToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")

		// Send streaming tool call chunks with malformed JSON (missing closing brace)
		escapedArgs := strings.ReplaceAll(`{"location":"Paris","unit":"celsius"`, `"`, `\"`)
		w.Write([]byte(fmt.Sprintf(`data: {"id":"stream-1","object":"chat.completion.chunk","created":1,"model":"gpt-4","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"%s"}}]}}]}`, escapedArgs) + "\n\n"))
		w.Write([]byte(`data: {"id":"stream-1","object":"chat.completion.chunk","created":1,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	provider := NewOpenAIProvider("test-key", server.URL)
	provider.SetRepairer(toolrepair.NewRepairer(toolrepair.DefaultConfig()))

	req := &ChatCompletionRequest{
		Model:    "gpt-4",
		Messages: []ChatMessage{{Role: "user", Content: "Weather?"}},
		Stream:   true,
	}

	eventCh, err := provider.StreamChatCompletion(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var toolCallEvents []StreamEvent
	for event := range eventCh {
		if event.Type == "tool_call" {
			toolCallEvents = append(toolCallEvents, event)
		}
	}

	// Should have received tool_call events
	if len(toolCallEvents) == 0 {
		t.Error("expected tool_call events")
	}

	// Check that tool call delta has the expected data
	if len(toolCallEvents) > 0 && len(toolCallEvents[0].ToolCalls) > 0 {
		tc := toolCallEvents[0].ToolCalls[0]
		if tc.Function.Name != "get_weather" {
			t.Errorf("expected function name 'get_weather', got %q", tc.Function.Name)
		}
		// Arguments should contain the malformed JSON (repair happens at accumulation time)
		if !strings.Contains(tc.Function.Arguments, "Paris") {
			t.Errorf("expected arguments to contain 'Paris', got %q", tc.Function.Arguments)
		}
	}
}
