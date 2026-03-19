package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy/normalizers"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/toolrepair"
)

// executeRequest performs the actual HTTP call to upstream
// and streams the response into the request's buffer.
// It checks if the model is internal and routes accordingly.
func executeRequest(ctx context.Context, cfg *ConfigSnapshot, originalReq *http.Request, rawBody []byte, req *upstreamRequest) error {
	req.MarkStarted()

	// Check if this model uses internal upstream
	// Note: ModelsConfig may be nil in tests, so check first
	if cfg.ModelsConfig != nil {
		modelConfig := cfg.ModelsConfig.GetModel(req.modelID)
		if modelConfig != nil && modelConfig.Internal {
			return executeInternalRequest(ctx, cfg, rawBody, req)
		}
	}

	// External upstream: use the configured upstream URL
	return executeExternalRequest(ctx, cfg, originalReq, rawBody, req)
}

// executeInternalRequest handles requests to internal providers (bypassing external upstream)
func executeInternalRequest(ctx context.Context, cfg *ConfigSnapshot, rawBody []byte, req *upstreamRequest) error {
	// Resolve internal config (including credential lookup)
	provider, apiKey, baseURL, internalModel, ok := cfg.ModelsConfig.ResolveInternalConfig(req.modelID)
	if !ok {
		return fmt.Errorf("failed to resolve internal config for model %s", req.modelID)
	}

	// Create provider client
	providerClient, err := providers.NewProvider(provider, apiKey, baseURL)
	if err != nil {
		return fmt.Errorf("failed to create provider: %w", err)
	}

	log.Printf("[DEBUG] Race attempt %d calling internal provider: %s (model=%s, baseURL=%s)", req.id, provider, internalModel, baseURL)

	// Parse request body
	var bodyMap map[string]interface{}
	if err := json.Unmarshal(rawBody, &bodyMap); err != nil {
		return fmt.Errorf("failed to parse request body: %w", err)
	}

	// Check if streaming
	isStream := false
	if stream, ok := bodyMap["stream"].(bool); ok {
		isStream = stream
	}

	// Convert to provider request
	providerReq, err := convertToProviderRequest(bodyMap, internalModel)
	if err != nil {
		return fmt.Errorf("failed to convert request: %w", err)
	}

	if isStream {
		// Detect provider for normalization context
		provider := normalizers.DetectProvider(cfg.ModelsConfig, req.modelID)
		normCtx := normalizers.NewContext(provider, fmt.Sprintf("%d", req.id))
		normalizers.GetRegistry().ResetAll(normCtx)
		return handleInternalStream(ctx, providerClient, providerReq, req, internalModel, normCtx, cfg.ToolRepair, cfg.StreamDeadline)
	}
	return handleInternalNonStream(ctx, providerClient, providerReq, req, internalModel)
}

// executeExternalRequest handles requests to external upstream (LiteLLM, etc.)
func executeExternalRequest(ctx context.Context, cfg *ConfigSnapshot, originalReq *http.Request, rawBody []byte, req *upstreamRequest) error {
	// 1. Prepare upstream request
	// Check for test upstream header (for testing with mock servers)
	upstreamURL := cfg.UpstreamURL

	// Set the target URL to upstream
	u, err := url.Parse(upstreamURL)
	if err != nil {
		return fmt.Errorf("invalid upstream URL: %w", err)
	}
	u.Path, _ = url.JoinPath(u.Path, "/v1/chat/completions")

	// 1.5 Modify body to use current model ID
	var bodyMap map[string]interface{}
	finalBody := rawBody
	if err := json.Unmarshal(rawBody, &bodyMap); err == nil {
		bodyMap["model"] = req.modelID
		if b, err := json.Marshal(bodyMap); err == nil {
			finalBody = b
		}
	}
	// Create fresh request with context and body
	upstreamReq, err := http.NewRequestWithContext(ctx, "POST", u.String(), bytes.NewReader(finalBody))
	if err != nil {
		return fmt.Errorf("failed to create upstream request: %w", err)
	}

	// Copy headers from original request
	for k, v := range originalReq.Header {
		// Skip standard proxy-unsafe headers
		if strings.EqualFold(k, "Content-Length") || strings.EqualFold(k, "Host") || strings.HasPrefix(strings.ToLower(k), "x-llmproxy-") {
			continue
		}
		upstreamReq.Header[k] = v
	}
	upstreamReq.Host = u.Host

	// If UpstreamCredentialID is configured, resolve the credential and set auth header
	// This allows the proxy to authenticate with external upstream providers
	// using a different token than what the client provided
	if cfg.UpstreamCredentialID != "" && cfg.ModelsConfig != nil {
		// Remove all auth headers first to avoid conflicts
		upstreamReq.Header.Del("Authorization")
		upstreamReq.Header.Del("X-API-Key")
		upstreamReq.Header.Del("x-api-key")
		upstreamReq.Header.Del("api-key")

		// Resolve credential
		cred := cfg.ModelsConfig.GetCredential(cfg.UpstreamCredentialID)
		if cred != nil {
			apiKey := cred.ResolveAPIKey()
			if apiKey != "" {
				upstreamReq.Header.Set("Authorization", "Bearer "+apiKey)
				log.Printf("[DEBUG] Race attempt %d: using upstream credential %s for authentication", req.id, cfg.UpstreamCredentialID)
			}
		} else {
			log.Printf("[WARN] Race attempt %d: upstream credential %s not found", req.id, cfg.UpstreamCredentialID)
		}
	}

	log.Printf("[DEBUG] Race attempt %d calling: %s (Host: %s)", req.id, upstreamReq.URL.String(), upstreamReq.Host)

	client := &http.Client{
		Timeout: 0, // Timeout is handled by context
	}

	// 2. Perform request
	resp, err := client.Do(upstreamReq)
	if err != nil {
		return fmt.Errorf("upstream request failed: %w", err)
	}
	defer resp.Body.Close()

	req.resp = resp

	// 3. Check for immediate error
	if resp.StatusCode >= 400 {
		return fmt.Errorf("upstream returned error: %s", resp.Status)
	}

	// 4. Check if this is a streaming or non-streaming response
	contentType := resp.Header.Get("Content-Type")
	isStreaming := strings.Contains(contentType, "text/event-stream")

	if !isStreaming {
		// Non-streaming response: read entire body as single chunk
		return handleNonStreamingResponse(ctx, cfg, resp, req)
	}

	// Streaming response
	req.MarkStreaming()
	// Detect provider for normalization
	provider := normalizers.DetectProvider(cfg.ModelsConfig, req.modelID)
	return handleStreamingResponse(ctx, cfg, resp, req, provider)
}

// handleInternalNonStream handles non-streaming requests for internal providers
func handleInternalNonStream(ctx context.Context, provider providers.Provider, req *providers.ChatCompletionRequest, upstreamReq *upstreamRequest, internalModel string) error {
	resp, err := provider.ChatCompletion(ctx, req)
	if err != nil {
		return err
	}

	// Marshal response to JSON
	data, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("failed to marshal response: %w", err)
	}

	// Add as single chunk
	if !upstreamReq.buffer.Add(data) {
		return fmt.Errorf("buffer limit exceeded")
	}

	return nil
}

// handleInternalStream handles streaming requests for internal providers
func handleInternalStream(ctx context.Context, provider providers.Provider, req *providers.ChatCompletionRequest, upstreamReq *upstreamRequest, internalModel string, normCtx *normalizers.NormalizeContext, toolRepairConfig toolrepair.Config, streamDeadline time.Duration) error {
	eventCh, err := provider.StreamChatCompletion(ctx, req)
	if err != nil {
		return err
	}

	upstreamReq.MarkStreaming()

	// Track state for proper streaming format
	firstChunk := true
	nextToolCallIndex := 0
	seenToolCallIDs := make(map[string]int)

	// NEW: Create accumulator for tool call arguments
	// Tool call arguments are streamed incrementally, so we must accumulate
	// before repairing (which requires complete JSON)
	accumulator := NewToolCallAccumulator()
	streamStartTime := time.Now()

	// NOTE: Tool call buffering is NOT applied to internal streams because:
	// 1. Internal providers (Anthropic, etc.) generate well-formed streaming output
	// 2. The tool call buffer is specifically for weak external upstream clients
	// 3. Internal path converts provider-specific format to OpenAI format directly

	for event := range eventCh {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		switch event.Type {
		case "content":
			// Write SSE data event
			// OpenAI streaming format: role is only present in FIRST chunk
			// Use map to control exactly what gets serialized (avoid zero-value string issue)
			var data []byte
			if firstChunk {
				// First chunk includes role
				chunk := map[string]interface{}{
					"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
					"object":  "chat.completion.chunk",
					"created": time.Now().Unix(),
					"model":   internalModel,
					"choices": []map[string]interface{}{
						{
							"index": 0,
							"delta": map[string]interface{}{
								"role":    "assistant",
								"content": event.Content,
							},
						},
					},
				}
				data, _ = json.Marshal(chunk)
			} else {
				// Subsequent chunks: NO role field at all (not even empty string)
				chunk := map[string]interface{}{
					"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
					"object":  "chat.completion.chunk",
					"created": time.Now().Unix(),
					"model":   internalModel,
					"choices": []map[string]interface{}{
						{
							"index": 0,
							"delta": map[string]interface{}{
								"content": event.Content,
							},
						},
					},
				}
				data, _ = json.Marshal(chunk)
			}
			line := fmt.Sprintf("data: %s\n", data)
			// Apply normalization to ensure consistent format
			normalizedLine, modified, normalizerName := normalizers.NormalizeWithContextAndName([]byte(line), normCtx)
			if modified {
				log.Printf("[DEBUG] Race attempt %d (internal): normalized chunk by %s", upstreamReq.id, normalizerName)
			}
			if !upstreamReq.buffer.Add(normalizedLine) {
				return fmt.Errorf("buffer limit exceeded")
			}
			firstChunk = false

		case "tool_call":
			// Write tool_call delta
			// Must include index field for each tool call (required for streaming)
			// Use map to control exactly what gets serialized
			if len(event.ToolCalls) > 0 {
				toolCalls := make([]map[string]interface{}, len(event.ToolCalls))
				for i, tc := range event.ToolCalls {
					// Assign index based on tool call ID if seen before, otherwise use next available
					var index int
					if tc.ID != "" {
						if idx, seen := seenToolCallIDs[tc.ID]; seen {
							index = idx
						} else {
							index = nextToolCallIndex
							seenToolCallIDs[tc.ID] = index
							nextToolCallIndex++
						}
					} else {
						// No ID, use position-based index
						index = i
					}
					toolCalls[i] = map[string]interface{}{
						"index": index,
						"id":    tc.ID,
						"type":  tc.Type,
						"function": map[string]interface{}{
							"name":      tc.Function.Name,
							"arguments": tc.Function.Arguments,
						},
					}
				}
				chunk := map[string]interface{}{
					"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
					"object":  "chat.completion.chunk",
					"created": time.Now().Unix(),
					"model":   internalModel,
					"choices": []map[string]interface{}{
						{
							"index": 0,
							"delta": map[string]interface{}{
								"tool_calls": toolCalls,
							},
						},
					},
				}
				data, _ := json.Marshal(chunk)
				line := fmt.Sprintf("data: %s\n", data)

				// NEW: Accumulate tool call arguments before normalization
				// This is needed because tool call args are streamed incrementally
				if err := accumulator.ProcessChunk([]byte(line)); err != nil {
					log.Printf("[DEBUG] Race attempt %d (internal): failed to accumulate chunk: %v", upstreamReq.id, err)
				}

				// Apply normalization to ensure consistent format
				normalizedLine, modified, normalizerName := normalizers.NormalizeWithContextAndName([]byte(line), normCtx)
				if modified {
					log.Printf("[DEBUG] Race attempt %d (internal): normalized chunk by %s", upstreamReq.id, normalizerName)
				}

				// REMOVED: Per-chunk tool repair (was here)
				// Tool repair now happens post-stream (after accumulation is complete)

				if !upstreamReq.buffer.Add(normalizedLine) {
					return fmt.Errorf("buffer limit exceeded")
				}
			}

		case "thinking":
			// Write thinking/reasoning content (DeepSeek-style reasoning_content field)
			// Use map to control exactly what gets serialized
			chunk := map[string]interface{}{
				"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
				"object":  "chat.completion.chunk",
				"created": time.Now().Unix(),
				"model":   internalModel,
				"choices": []map[string]interface{}{
					{
						"index": 0,
						"delta": map[string]interface{}{
							"reasoning_content": event.ReasoningContent,
						},
					},
				},
			}
			data, _ := json.Marshal(chunk)
			line := fmt.Sprintf("data: %s\n", data)
			// Apply normalization to ensure consistent format
			normalizedLine, modified, normalizerName := normalizers.NormalizeWithContextAndName([]byte(line), normCtx)
			if modified {
				log.Printf("[DEBUG] Race attempt %d (internal): normalized chunk by %s", upstreamReq.id, normalizerName)
			}
			if !upstreamReq.buffer.Add(normalizedLine) {
				return fmt.Errorf("buffer limit exceeded")
			}

		case "done":
			// Write final chunk with finish_reason before [DONE]
			// This is required by OpenAI streaming format - clients expect finish_reason in the last chunk
			// Use the finish_reason from the event (e.g., "tool_calls" for tool calls, "stop" for normal completion)
			finishReason := event.FinishReason
			if finishReason == "" {
				finishReason = "stop"
			}

			// Validate finish_reason
			validReasons := map[string]bool{"stop": true, "tool_calls": true, "length": true, "content_filter": true}
			if !validReasons[finishReason] {
				log.Printf("[WARN] Invalid finish_reason: %s, defaulting to 'stop'", finishReason)
				finishReason = "stop"
			}
			finalChunk := map[string]interface{}{
				"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
				"object":  "chat.completion.chunk",
				"created": time.Now().Unix(),
				"model":   internalModel,
				"choices": []map[string]interface{}{
					{
						"index":         0,
						"delta":         map[string]interface{}{},
						"finish_reason": finishReason,
					},
				},
			}
			finalData, _ := json.Marshal(finalChunk)
			finalLine := fmt.Sprintf("data: %s\n", finalData)
			if !upstreamReq.buffer.Add([]byte(finalLine)) {
				return fmt.Errorf("buffer limit exceeded")
			}

			// Write [DONE] marker
			if !upstreamReq.buffer.Add([]byte("data: [DONE]\n")) {
				return fmt.Errorf("buffer limit exceeded")
			}

			// NEW: Post-stream tool call repair
			// Only attempt repair if:
			// 1. We have accumulated tool calls
			// 2. Repair is enabled in config
			// 3. We're still within the stream deadline (for latency reasons)
			if accumulator.HasToolCalls() && toolRepairConfig.Enabled {
				streamElapsed := time.Since(streamStartTime)
				if streamElapsed < streamDeadline {
					// Repair accumulated arguments
					repairedArgs := repairAccumulatedArgs(accumulator.GetAccumulatedArgs(), toolRepairConfig)
					if len(repairedArgs) > 0 {
						// Rewrite buffer with repaired arguments
						upstreamReq.buffer = rewriteBufferWithRepairedArgs(upstreamReq.buffer, repairedArgs)
						log.Printf("[TOOL-REPAIR] Race attempt %d (internal): repaired %d tool_call arguments after stream completion",
							upstreamReq.id, len(repairedArgs))
					}
				} else {
					log.Printf("[TOOL-REPAIR] Race attempt %d (internal): stream completed after deadline (%v > %v), skipping repair for latency",
						upstreamReq.id, streamElapsed, streamDeadline)
				}
			}

			return nil

		case "error":
			errMsg := "unknown error"
			if event.Error != nil {
				errMsg = event.Error.Error()
			}
			log.Printf("[RACE] Internal provider stream error: %s", errMsg)
			return fmt.Errorf("provider stream error: %s", errMsg)
		}
	}

	// If we get here without "done", the stream ended unexpectedly
	return fmt.Errorf("stream ended without done signal")
}

// convertToProviderRequest converts map[string]interface{} to providers.ChatCompletionRequest
func convertToProviderRequest(body map[string]interface{}, model string) (*providers.ChatCompletionRequest, error) {
	req := &providers.ChatCompletionRequest{}
	req.Model = model

	if messages, ok := body["messages"].([]interface{}); ok {
		for msgIdx, m := range messages {
			if msg, ok := m.(map[string]interface{}); ok {
				chatMsg := providers.ChatMessage{}
				if role, ok := msg["role"].(string); ok {
					chatMsg.Role = role
				}
				if content, ok := msg["content"]; ok {
					switch c := content.(type) {
					case string:
						chatMsg.Content = content
					case []interface{}:
						// Multimodal content - handle each part
						contentParts := make([]providers.ContentPart, len(c))
						for i, part := range c {
							if partMap, ok := part.(map[string]interface{}); ok {
								cp := providers.ContentPart{}
								if partType, ok := partMap["type"].(string); ok {
									cp.Type = partType
								}
								if text, ok := partMap["text"].(string); ok {
									cp.Text = text
								}
								if imageURL, ok := partMap["image_url"].(map[string]interface{}); ok {
									if url, ok := imageURL["url"].(string); ok {
										cp.ImageURL = &providers.ImageURL{
											URL: url,
										}
									}
								}
								contentParts[i] = cp
							}
						}
						chatMsg.Content = contentParts
					}
				}
				if toolCalls, ok := msg["tool_calls"].([]interface{}); ok {
					chatMsg.ToolCalls = make([]providers.ToolCall, len(toolCalls))
					for i, tc := range toolCalls {
						if tcMap, ok := tc.(map[string]interface{}); ok {
							toolCall := providers.ToolCall{}
							if id, ok := tcMap["id"].(string); ok {
								toolCall.ID = id
							}
							if tcType, ok := tcMap["type"].(string); ok {
								toolCall.Type = tcType
							}
							if fn, ok := tcMap["function"].(map[string]interface{}); ok {
								toolCall.Function = providers.ToolCallFunction{}
								if name, ok := fn["name"].(string); ok {
									toolCall.Function.Name = name
								}
								if args, ok := fn["arguments"].(string); ok {
									toolCall.Function.Arguments = args
								}
							}
							chatMsg.ToolCalls[i] = toolCall
						}
					}
				}
				// Handle tool_call_id for tool role messages (required by MiniMax and other providers)
				if toolCallID, ok := msg["tool_call_id"].(string); ok {
					chatMsg.ToolCallID = toolCallID
				}
				// Debug log for tool role messages to diagnose MiniMax compatibility issues
				if chatMsg.Role == "tool" {
					if chatMsg.ToolCallID == "" {
						log.Printf("[WARN] Message[%d] has role='tool' but missing tool_call_id - this may cause MiniMax API error", msgIdx)
					} else {
						log.Printf("[DEBUG] Message[%d] has role='tool' with tool_call_id=%s", msgIdx, chatMsg.ToolCallID)
					}
				}
				req.Messages = append(req.Messages, chatMsg)
			}
		}
	}

	if temperature, ok := body["temperature"].(float64); ok {
		req.Temperature = &temperature
	}

	if maxTokens, ok := body["max_tokens"].(float64); ok {
		maxTokensInt := int(maxTokens)
		req.MaxTokens = &maxTokensInt
	}

	if stream, ok := body["stream"].(bool); ok {
		req.Stream = stream
	}

	if tools, ok := body["tools"].([]interface{}); ok {
		req.Tools = make([]providers.Tool, len(tools))
		for i, t := range tools {
			if tMap, ok := t.(map[string]interface{}); ok {
				tool := providers.Tool{}
				if toolType, ok := tMap["type"].(string); ok {
					tool.Type = toolType
				}
				if fn, ok := tMap["function"].(map[string]interface{}); ok {
					tool.Function = providers.ToolFunction{}
					if name, ok := fn["name"].(string); ok {
						tool.Function.Name = name
					}
					if desc, ok := fn["description"].(string); ok {
						tool.Function.Description = desc
					}
					if params, ok := fn["parameters"].(map[string]interface{}); ok {
						tool.Function.Parameters = params
					}
				}
				req.Tools[i] = tool
			}
		}
	}

	if toolChoice, exists := body["tool_choice"]; exists {
		req.ToolChoice = toolChoice
	}

	if extra, ok := body["extra"].(map[string]interface{}); ok {
		req.Extra = extra
	}

	return req, nil
}

// handleNonStreamingResponse reads a non-streaming JSON response
func handleNonStreamingResponse(ctx context.Context, cfg *ConfigSnapshot, resp *http.Response, req *upstreamRequest) error {
	// Read entire body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read error: %w", err)
	}

	// Apply tool repair to non-streaming JSON response if enabled
	if cfg.ToolRepair.Enabled {
		repairedBody, repaired := repairToolCallArgumentsInNonStreamingResponse(body, cfg.ToolRepair)
		if repaired {
			body = repairedBody
			log.Printf("[TOOL-REPAIR] Race attempt %d: repaired malformed tool_call arguments in non-streaming response", req.id)
		}
	}

	// Add as single chunk (the non-streaming JSON response)
	if !req.buffer.Add(body) {
		return fmt.Errorf("buffer limit exceeded")
	}

	return nil
}

// getNormalizerDescription returns a human-readable description of what a normalizer fixes
func getNormalizerDescription(normalizerName string) string {
	switch normalizerName {
	case "fix_empty_role":
		return "Fixed empty role field in delta (changed to 'assistant')"
	case "fix_tool_call_index":
		return "Added missing index field to tool_calls"
	case "tool_call_arguments_repair":
		return "Repaired malformed JSON in tool_call arguments"
	default:
		return "Normalized stream chunk"
	}
}

// getKeys returns the keys of a map as a slice
func getKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// repairToolCallArgumentsInNonStreamingResponse repairs malformed JSON in tool_call arguments
// within a non-streaming JSON response. Returns the (potentially modified) body and whether it was modified.
func repairToolCallArgumentsInNonStreamingResponse(body []byte, config toolrepair.Config) ([]byte, bool) {
	if !config.Enabled {
		return body, false
	}

	repairer := toolrepair.NewRepairer(&config)

	// Try to parse as JSON
	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		return body, false
	}

	// Navigate to choices[].message.tool_calls
	choices, ok := resp["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return body, false
	}

	modified := false

	for _, choice := range choices {
		choiceMap, ok := choice.(map[string]interface{})
		if !ok {
			continue
		}

		message, ok := choiceMap["message"].(map[string]interface{})
		if !ok {
			continue
		}

		toolCalls, ok := message["tool_calls"].([]interface{})
		if !ok || len(toolCalls) == 0 {
			continue
		}

		// Process each tool call
		for _, tc := range toolCalls {
			tcMap, ok := tc.(map[string]interface{})
			if !ok {
				continue
			}

			// Get function object
			fn, ok := tcMap["function"].(map[string]interface{})
			if !ok {
				continue
			}

			// Get arguments string
			args, ok := fn["arguments"].(string)
			if !ok || args == "" {
				continue
			}

			// Check if arguments are already valid JSON
			var js interface{}
			if json.Unmarshal([]byte(args), &js) == nil {
				continue
			}

			// Attempt repair
			result := repairer.RepairArguments(args, "")
			if result.Success && result.Repaired != args {
				fn["arguments"] = result.Repaired
				modified = true
			}
		}
	}

	if !modified {
		return body, false
	}

	// Marshal back to JSON
	repaired, err := json.Marshal(resp)
	if err != nil {
		return body, false
	}

	return repaired, true
}

// repairToolCallArgumentsInChunk repairs malformed JSON in tool_call arguments within an SSE chunk.
// Returns the (potentially modified) chunk and whether it was modified.
//
// DEPRECATED: This function is broken for streaming responses because tool call arguments
// are incrementally streamed across multiple chunks. Per-chunk repair cannot work because
// each chunk contains partial JSON that cannot be meaningfully repaired in isolation.
//
// Use repairAccumulatedArgs() instead, which repairs tool call arguments AFTER the stream
// completes (when all chunks have been accumulated into complete JSON).
//
// This function is kept for backward compatibility but is no longer used in the streaming path.
func repairToolCallArgumentsInChunk(line []byte, config toolrepair.Config) ([]byte, bool) {
	if !config.Enabled {
		return line, false
	}

	repairer := toolrepair.NewRepairer(&config)

	// Skip non-data lines
	lineStr := strings.TrimSpace(string(line))
	if lineStr == "" || lineStr == "data: [DONE]" || lineStr == "[DONE]" {
		return line, false
	}

	// Strip "data: " prefix if present
	data := line
	hasDataPrefix := strings.HasPrefix(lineStr, "data: ")
	if hasDataPrefix {
		data = []byte(strings.TrimPrefix(lineStr, "data: "))
	}

	// Try to parse as JSON
	var chunk map[string]interface{}
	if err := json.Unmarshal(data, &chunk); err != nil {
		return line, false
	}

	// Navigate to choices[0].delta.tool_calls
	choices, ok := chunk["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return line, false
	}

	choice, ok := choices[0].(map[string]interface{})
	if !ok {
		return line, false
	}

	delta, ok := choice["delta"].(map[string]interface{})
	if !ok {
		return line, false
	}

	toolCalls, ok := delta["tool_calls"].([]interface{})
	if !ok || len(toolCalls) == 0 {
		return line, false
	}

	modified := false

	// Process each tool call
	for _, tc := range toolCalls {
		tcMap, ok := tc.(map[string]interface{})
		if !ok {
			continue
		}

		// Get function object
		fn, ok := tcMap["function"].(map[string]interface{})
		if !ok {
			continue
		}

		// Get arguments string
		args, ok := fn["arguments"].(string)
		if !ok || args == "" {
			continue
		}

		// Check if arguments are already valid JSON
		var js interface{}
		if json.Unmarshal([]byte(args), &js) == nil {
			continue
		}

		// Attempt repair
		result := repairer.RepairArguments(args, "")
		if result.Success && result.Repaired != args {
			fn["arguments"] = result.Repaired
			modified = true
		}
	}

	if !modified {
		return line, false
	}

	// Marshal back to JSON
	normalized, err := json.Marshal(chunk)
	if err != nil {
		return line, false
	}

	// Re-add "data: " prefix if it was present
	if hasDataPrefix {
		return []byte("data: " + string(normalized)), true
	}

	return normalized, true
}

// repairAccumulatedArgs repairs accumulated tool call arguments after the stream completes.
// Returns a map of index -> repaired arguments (only includes indices that were repaired).
// This is the correct approach for streaming because tool call arguments are incrementally
// streamed across multiple chunks, so repair can only happen on complete JSON.
func repairAccumulatedArgs(accumulated map[int]string, config toolrepair.Config) map[int]string {
	if !config.Enabled {
		return nil
	}

	repairer := toolrepair.NewRepairer(&config)
	repaired := make(map[int]string)

	for idx, args := range accumulated {
		// Skip empty arguments
		if args == "" {
			continue
		}

		// Check if already valid JSON - no repair needed
		var js interface{}
		if json.Unmarshal([]byte(args), &js) == nil {
			continue // Already valid
		}

		// Attempt repair
		result := repairer.RepairArguments(args, "")
		if result.Success && result.Repaired != args {
			repaired[idx] = result.Repaired
			log.Printf("[TOOL-REPAIR] Repaired tool_call[%d] arguments: %d -> %d bytes",
				idx, len(args), len(result.Repaired))
		} else if !result.Success {
			log.Printf("[WARN] Tool repair failed for tool_call[%d], using original args", idx)
		}
	}

	return repaired
}

// handleStreamingResponse handles SSE streaming responses
// IMPORTANT: This function does NOT return error on idle timeout.
// Per the unified race retry design, the main request should continue streaming
// even after idle timeout - the coordinator will spawn parallel requests.
// Idle timeout detection is handled by the coordinator via TrackActivity().
func handleStreamingResponse(ctx context.Context, cfg *ConfigSnapshot, resp *http.Response, req *upstreamRequest, provider string) error {
	// MEMORY TRAP FIX: Use bufio.Reader with increased buffer instead of bufio.Scanner
	// to avoid issues with long SSE lines and memory retention.
	reader := bufio.NewReaderSize(resp.Body, 64*1024) // 64KB buffer

	sawDone := false

	// Create normalization context for this stream
	normCtx := normalizers.NewContext(provider, fmt.Sprintf("%d", req.id))

	// Reset normalizer state for this new stream to avoid state leakage
	normalizers.GetRegistry().ResetAll(normCtx)

	// Create tool call buffer for weak streaming clients (enabled by default)
	// This buffers tool call fragments until they form valid JSON before emitting
	var toolCallBuffer *ToolCallBuffer
	if !cfg.ToolCallBufferDisabled {
		toolCallBuffer = NewToolCallBuffer(cfg.ToolCallBufferMaxSize, req.modelID, fmt.Sprintf("%d", req.id))
	}

	// Create accumulator for tool call arguments
	// Tool call arguments are streamed incrementally, so we must accumulate
	// before repairing (which requires complete JSON)
	accumulator := NewToolCallAccumulator()
	streamStartTime := time.Now()

	for {
		// Set idle timeout for reading
		var line []byte
		var readErr error

		// Setup idle timeout wrapper with configurable timeout
		// Use a longer read timeout to allow the coordinator to detect idle
		readTimeout := time.Duration(cfg.IdleTimeout) * 2 // Double the idle timeout for read
		if readTimeout < 30*time.Second {
			readTimeout = 30 * time.Second // Minimum 30s
		}

		readDone := make(chan struct{})
		go func() {
			line, readErr = reader.ReadBytes('\n')
			close(readDone)
		}()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-readDone:
			// Track activity for coordinator's idle detection
			req.TrackActivity()
			// Continuous processing
		case <-time.After(readTimeout):
			// Read timeout - but DON'T return error!
			// The coordinator will detect idle and spawn parallel requests.
			// We continue waiting for the read to complete.
			// This prevents cancelling the main request prematurely.
			log.Printf("[RACE] Request %d: read timeout after %v, continuing to wait...", req.id, readTimeout)
			// Wait for the read to eventually complete or context cancellation
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-readDone:
				req.TrackActivity()
				// Continue processing
			}
		}

		if len(line) > 0 {
			// Remove trailing newline for consistency with scanner
			if line[len(line)-1] == '\n' {
				line = line[:len(line)-1]
			}
			// and \r if present
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}

			// IMPORTANT: Apply normalization FIRST before accumulation
			// This fixes issues like concatenated JSON chunks from providers like MiniMax
			// Example malformed input:  data: {...} {...}
			// Fixed output:             data: {...}\ndata: {...}
			normalizedLine, modified, normalizerName := normalizers.NormalizeWithContextAndName(line, normCtx)
			if modified {
				log.Printf("[DEBUG] Race attempt %d: normalized malformed stream chunk by %s", req.id, normalizerName)

				// Publish event to frontend if event bus is available
				if cfg.EventBus != nil {
					description := getNormalizerDescription(normalizerName)
					cfg.EventBus.Publish(events.Event{
						Type:      "stream_normalize",
						Timestamp: time.Now().Unix(),
						Data: map[string]interface{}{
							"id":          fmt.Sprintf("%d", req.id),
							"normalizer":  normalizerName,
							"provider":    provider,
							"description": description,
						},
					})
				}
			}

			// After normalization, we may have multiple lines (if concatenated chunks were split)
			// Process each line separately for accumulation
			normalizedStr := string(normalizedLine)
			var linesToProcess []string
			if strings.Contains(normalizedStr, "\n") {
				// Multiple lines from split - process each one
				linesToProcess = strings.Split(normalizedStr, "\n")
			} else {
				// Single line
				linesToProcess = []string{normalizedStr}
			}

			// Accumulate tool call arguments from each normalized line
			for _, lineToProcess := range linesToProcess {
				if lineToProcess == "" {
					continue
				}
				if err := accumulator.ProcessChunk([]byte(lineToProcess)); err != nil {
					log.Printf("[DEBUG] Race attempt %d: failed to accumulate chunk: %v", req.id, err)
				}
			}

			// REMOVED: Per-chunk tool repair (was here)
			// Tool repair now happens post-stream (after accumulation is complete)
			// This is because tool call arguments are incrementally streamed
			// and repair requires complete JSON

			// Process through tool call buffer (if enabled)
			// This buffers tool call fragments until they form valid JSON before emitting
			var chunksToEmit [][]byte
			if toolCallBuffer != nil {
				chunksToEmit = toolCallBuffer.ProcessChunk(normalizedLine)
			} else {
				chunksToEmit = [][]byte{normalizedLine}
			}

			// Add all chunks to buffer
			for _, chunk := range chunksToEmit {
				if !req.buffer.Add(chunk) {
					return fmt.Errorf("buffer limit exceeded")
				}
			}

			// Check for stream error chunk (e.g., from LiteLLM)
			if isStreamErrorChunk(line) != "" {
				return fmt.Errorf("upstream streamed error chunk: %s", isStreamErrorChunk(line))
			}

			// Check for [DONE]
			if string(line) == "data: [DONE]" {
				sawDone = true
				break
			}
		}

		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return fmt.Errorf("read error: %w", readErr)
		}
	}

	if !sawDone {
		return fmt.Errorf("upstream closed connection prematurely")
	}

	// Flush any remaining buffered tool calls (if tool call buffer is enabled)
	// This ensures complete tool call chunks are emitted before post-stream processing
	if toolCallBuffer != nil {
		flushChunks := toolCallBuffer.Flush()
		for _, chunk := range flushChunks {
			if !req.buffer.Add(chunk) {
				log.Printf("[WARN] Race attempt %d: failed to add flushed tool call chunk (buffer limit exceeded)", req.id)
				break
			}
		}
		if len(flushChunks) > 0 {
			log.Printf("[DEBUG] Race attempt %d: flushed %d buffered tool call chunks at stream end", req.id, len(flushChunks))
		}
	}

	// NEW: Post-stream tool call repair
	// Only attempt repair if:
	// 1. We have accumulated tool calls
	// 2. Repair is enabled in config
	// 3. We're still within the stream deadline (for latency reasons)
	if accumulator.HasToolCalls() && cfg.ToolRepair.Enabled {
		streamElapsed := time.Since(streamStartTime)
		if streamElapsed < cfg.StreamDeadline {
			// Repair accumulated arguments
			repairedArgs := repairAccumulatedArgs(accumulator.GetAccumulatedArgs(), cfg.ToolRepair)
			if len(repairedArgs) > 0 {
				// Rewrite buffer with repaired arguments
				req.buffer = rewriteBufferWithRepairedArgs(req.buffer, repairedArgs)
				log.Printf("[TOOL-REPAIR] Race attempt %d: repaired %d tool_call arguments after stream completion",
					req.id, len(repairedArgs))

				// Publish event to frontend if event bus is available
				if cfg.EventBus != nil {
					cfg.EventBus.Publish(events.Event{
						Type:      "tool_repair",
						Timestamp: time.Now().Unix(),
						Data: map[string]interface{}{
							"id":          fmt.Sprintf("%d", req.id),
							"provider":    provider,
							"description": fmt.Sprintf("Repaired %d malformed JSON in tool_call arguments (post-stream)", len(repairedArgs)),
						},
					})
				}
			}
		} else {
			log.Printf("[TOOL-REPAIR] Race attempt %d: stream completed after deadline (%v > %v), skipping repair for latency",
				req.id, streamElapsed, cfg.StreamDeadline)
		}
	}

	return nil
}
