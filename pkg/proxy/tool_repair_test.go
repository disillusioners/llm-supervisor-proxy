package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/toolrepair"
)

// TestToolRepair_StreamingIntegration tests that tool repair is applied
// when streaming responses contain malformed tool call JSON
func TestToolRepair_StreamingIntegration(t *testing.T) {
	tests := []struct {
		name         string
		config       toolrepair.Config
		toolCallArgs string // Malformed JSON from LLM
		expectedArgs string // Expected repaired JSON
		shouldRepair bool
	}{
		{
			name:         "disabled_repair_returns_original",
			config:       toolrepair.Config{Enabled: false},
			toolCallArgs: `{key: "value"}`,
			expectedArgs: `{key: "value"}`, // Unchanged
			shouldRepair: false,
		},
		{
			name: "enabled_repair_fixes_malformed_json",
			config: toolrepair.Config{
				Enabled:    true,
				Strategies: []string{"extract_json", "library_repair", "remove_reasoning"},
			},
			toolCallArgs: `{key: "value"}`,
			expectedArgs: `{"key": "value"}`, // Repaired
			shouldRepair: true,
		},
		{
			name: "repair_extracts_json_from_text",
			config: toolrepair.Config{
				Enabled:    true,
				Strategies: []string{"extract_json", "library_repair"},
			},
			toolCallArgs: `Here is the result: {"location":"Tokyo"} end.`,
			expectedArgs: `{"location":"Tokyo"}`, // Extracted
			shouldRepair: true,
		},
		{
			name: "repair_fixes_trailing_comma",
			config: toolrepair.Config{
				Enabled:    true,
				Strategies: []string{"library_repair"},
			},
			toolCallArgs: `{"command":"ls","args":["-la",]}`,
			expectedArgs: `{"command":"ls","args":["-la"]}`, // Fixed
			shouldRepair: true,
		},
		{
			name: "repair_fixes_single_quotes",
			config: toolrepair.Config{
				Enabled:    true,
				Strategies: []string{"library_repair"},
			},
			toolCallArgs: `{'query':'test','limit':10}`,
			expectedArgs: `{"query":"test","limit":10}`, // Fixed
			shouldRepair: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock upstream that returns malformed tool call JSON
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)

				// Send malformed tool call in streaming response
				toolCallChunk := map[string]interface{}{
					"id":      "chatcmpl-test",
					"object":  "chat.completion.chunk",
					"created": time.Now().Unix(),
					"model":   "test-model",
					"choices": []map[string]interface{}{
						{
							"index": 0,
							"delta": map[string]interface{}{
								"tool_calls": []map[string]interface{}{
									{
										"index": 0,
										"id":    "call_123",
										"type":  "function",
										"function": map[string]interface{}{
											"name":      "test_tool",
											"arguments": tt.toolCallArgs,
										},
									},
								},
							},
						},
					},
				}
				data, _ := json.Marshal(toolCallChunk)
				w.Write([]byte("data: " + string(data) + "\n\n"))

				// Send finish chunk
				finishChunk := map[string]interface{}{
					"id":      "chatcmpl-test",
					"object":  "chat.completion.chunk",
					"created": time.Now().Unix(),
					"model":   "test-model",
					"choices": []map[string]interface{}{
						{
							"index":         0,
							"delta":         map[string]interface{}{},
							"finish_reason": "tool_calls",
						},
					},
				}
				finishData, _ := json.Marshal(finishChunk)
				w.Write([]byte("data: " + string(finishData) + "\n\n"))
				w.Write([]byte("data: [DONE]\n\n"))
			}))
			defer server.Close()

			cfg := &ConfigSnapshot{
				UpstreamURL:        server.URL,
				RaceRetryEnabled:   false,
				RaceMaxBufferBytes: 1024 * 1024,
				IdleTimeout:        30 * time.Second,
				StreamDeadline:     10 * time.Second,
				MaxGenerationTime:  15 * time.Second,
				ModelID:            "test-model",
				ToolRepair:         tt.config,
			}

			ctx := context.Background()

			req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"test-model","stream":true}`))
			rawBody := []byte(`{"model":"test-model","stream":true}`)
			models := []string{"test-model"}

			coordinator := newRaceCoordinator(ctx, cfg, req, rawBody, models)
			coordinator.Start()

			winner := coordinator.WaitForWinner()
			if winner == nil {
				t.Fatalf("No winner selected")
			}

			// Wait for streaming to complete
			select {
			case <-winner.buffer.Done():
				// Success
			case <-time.After(3 * time.Second):
				t.Fatalf("Timeout waiting for buffer done")
			}

			// Get all chunks and check for tool call arguments
			chunks, _ := winner.buffer.GetChunksFrom(0)

			var foundToolCall bool
			var actualArgs string

			for _, chunk := range chunks {
				chunkStr := string(chunk)
				if strings.Contains(chunkStr, `"tool_calls"`) {
					foundToolCall = true
					// Parse the chunk to extract arguments
					var parsed map[string]interface{}
					dataStr := strings.TrimPrefix(chunkStr, "data: ")
					dataStr = strings.TrimSpace(dataStr)
					if err := json.Unmarshal([]byte(dataStr), &parsed); err == nil {
						if choices, ok := parsed["choices"].([]interface{}); ok && len(choices) > 0 {
							if choice, ok := choices[0].(map[string]interface{}); ok {
								if delta, ok := choice["delta"].(map[string]interface{}); ok {
									if toolCalls, ok := delta["tool_calls"].([]interface{}); ok && len(toolCalls) > 0 {
										if tc, ok := toolCalls[0].(map[string]interface{}); ok {
											if fn, ok := tc["function"].(map[string]interface{}); ok {
												if args, ok := fn["arguments"].(string); ok {
													actualArgs = args
												}
											}
										}
									}
								}
							}
						}
					}
				}
			}

			if !foundToolCall {
				t.Fatalf("No tool call found in response chunks")
			}

			// Check if arguments match expected
			if tt.shouldRepair {
				// Validate that the repaired JSON is valid
				var parsed map[string]interface{}
				if err := json.Unmarshal([]byte(actualArgs), &parsed); err != nil {
					t.Errorf("Expected repaired JSON to be valid, got: %q, error: %v", actualArgs, err)
				}

				// Check if it matches expected (for exact matches)
				if tt.expectedArgs != "" && actualArgs != tt.expectedArgs {
					t.Errorf("Expected arguments %q, got %q", tt.expectedArgs, actualArgs)
				}
			} else {
				// When repair is disabled, arguments should be unchanged
				if actualArgs != tt.expectedArgs {
					t.Errorf("Expected arguments %q, got %q", tt.expectedArgs, actualArgs)
				}
			}
		})
	}
}

// TestToolRepair_NonStreamingIntegration tests tool repair in non-streaming responses
func TestToolRepair_NonStreamingIntegration(t *testing.T) {
	tests := []struct {
		name         string
		config       toolrepair.Config
		toolCallArgs string
		expectedArgs string
		shouldRepair bool
	}{
		{
			name:         "disabled_no_repair",
			config:       toolrepair.Config{Enabled: false},
			toolCallArgs: `{key: "value"}`,
			expectedArgs: `{key: "value"}`,
			shouldRepair: false,
		},
		{
			name: "enabled_repairs_malformed",
			config: toolrepair.Config{
				Enabled:    true,
				Strategies: []string{"library_repair"},
			},
			toolCallArgs: `{key: "value"}`,
			expectedArgs: `{"key": "value"}`,
			shouldRepair: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock upstream that returns non-streaming response with malformed tool call
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Check if this is a streaming request
				var body map[string]interface{}
				json.NewDecoder(r.Body).Decode(&body)
				isStream, _ := body["stream"].(bool)

				if isStream {
					// For streaming, use SSE
					w.Header().Set("Content-Type", "text/event-stream")
					w.WriteHeader(http.StatusOK)
					// ... streaming logic
					return
				}

				// Non-streaming response
				w.Header().Set("Content-Type", "application/json")
				resp := map[string]interface{}{
					"id":      "chatcmpl-test",
					"object":  "chat.completion",
					"created": time.Now().Unix(),
					"model":   "test-model",
					"choices": []map[string]interface{}{
						{
							"index": 0,
							"message": map[string]interface{}{
								"role": "assistant",
								"tool_calls": []map[string]interface{}{
									{
										"id":   "call_123",
										"type": "function",
										"function": map[string]interface{}{
											"name":      "test_tool",
											"arguments": tt.toolCallArgs,
										},
									},
								},
							},
							"finish_reason": "tool_calls",
						},
					},
				}
				json.NewEncoder(w).Encode(resp)
			}))
			defer server.Close()

			cfg := &ConfigSnapshot{
				UpstreamURL:        server.URL,
				RaceRetryEnabled:   false,
				RaceMaxBufferBytes: 1024 * 1024,
				IdleTimeout:        30 * time.Second,
				StreamDeadline:     10 * time.Second,
				MaxGenerationTime:  15 * time.Second,
				ModelID:            "test-model",
				ToolRepair:         tt.config,
			}

			ctx := context.Background()

			req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"test-model","stream":false}`))
			rawBody := []byte(`{"model":"test-model","stream":false}`)
			models := []string{"test-model"}

			coordinator := newRaceCoordinator(ctx, cfg, req, rawBody, models)
			coordinator.Start()

			winner := coordinator.WaitForWinner()
			if winner == nil {
				t.Fatalf("No winner selected")
			}

			// Wait for completion
			select {
			case <-winner.buffer.Done():
			case <-time.After(3 * time.Second):
				t.Fatalf("Timeout waiting for buffer done")
			}

			// Get response
			chunks, _ := winner.buffer.GetChunksFrom(0)
			if len(chunks) == 0 {
				t.Fatalf("No response chunks")
			}

			// Parse response
			var resp map[string]interface{}
			respData := strings.TrimPrefix(string(chunks[0]), "data: ")
			if err := json.Unmarshal([]byte(respData), &resp); err != nil {
				t.Fatalf("Failed to parse response: %v", err)
			}

			// Extract tool call arguments
			choices, _ := resp["choices"].([]interface{})
			if len(choices) == 0 {
				t.Fatalf("No choices in response")
			}
			choice, _ := choices[0].(map[string]interface{})
			message, _ := choice["message"].(map[string]interface{})
			toolCalls, _ := message["tool_calls"].([]interface{})
			if len(toolCalls) == 0 {
				t.Fatalf("No tool calls in response")
			}
			tc, _ := toolCalls[0].(map[string]interface{})
			fn, _ := tc["function"].(map[string]interface{})
			actualArgs, _ := fn["arguments"].(string)

			// Verify repair behavior
			if tt.shouldRepair {
				var parsed map[string]interface{}
				if err := json.Unmarshal([]byte(actualArgs), &parsed); err != nil {
					t.Errorf("Expected repaired JSON to be valid, got: %q, error: %v", actualArgs, err)
				}
			} else {
				if actualArgs != tt.expectedArgs {
					t.Errorf("Expected arguments %q, got %q", tt.expectedArgs, actualArgs)
				}
			}
		})
	}
}

// TestToolRepair_ConfigPropagation tests that ToolRepair config is properly propagated
func TestToolRepair_ConfigPropagation(t *testing.T) {
	// This test verifies that the ToolRepair config is accessible in ConfigSnapshot
	cfg := &ConfigSnapshot{
		ToolRepair: toolrepair.Config{
			Enabled:          true,
			Strategies:       []string{"extract_json", "library_repair"},
			MaxArgumentsSize: 10240,
		},
	}

	if !cfg.ToolRepair.Enabled {
		t.Error("ToolRepair should be enabled in config snapshot")
	}

	if len(cfg.ToolRepair.Strategies) != 2 {
		t.Errorf("Expected 2 strategies, got %d", len(cfg.ToolRepair.Strategies))
	}

	if cfg.ToolRepair.MaxArgumentsSize != 10240 {
		t.Errorf("Expected MaxArgumentsSize 10240, got %d", cfg.ToolRepair.MaxArgumentsSize)
	}
}
