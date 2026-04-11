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
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy/normalizers"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy/token"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/toolcall"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/toolrepair"
)

// executeRequest performs the actual HTTP call to upstream
// and streams the response into the request's buffer.
// It checks if the model is internal and routes accordingly.
func executeRequest(ctx context.Context, cfg *ConfigSnapshot, originalReq *http.Request, rawBody []byte, req *upstreamRequest) error {
	req.MarkStarted()

	log.Printf("[PEAK-DBG] executeRequest ENTRY: req.modelID=%q, req.modelType=%v", req.modelID, req.modelType)

	// Check if this model uses internal upstream
	// Note: ModelsConfig may be nil in tests, so check first
	if cfg.ModelsConfig != nil {
		modelConfig := cfg.ModelsConfig.GetModel(req.modelID)

		log.Printf("[PEAK-DBG] executeRequest: modelConfig found, Internal=%v, modelID=%q", modelConfig != nil && modelConfig.Internal, req.modelID)
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

	log.Printf("[PEAK-DBG] executeInternalRequest: req.modelID=%q -> ResolveInternalConfig returned internalModel=%q, ok=%v", req.modelID, internalModel, ok)
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
		return handleInternalStream(ctx, providerClient, providerReq, req, internalModel, normCtx, cfg.ToolRepair, cfg.StreamDeadline, rawBody)
	}
	return handleInternalNonStream(ctx, providerClient, providerReq, req, internalModel, rawBody)
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

	// Deferred function to close response body only if req.resp hasn't been
	// cleared by cleanup() (which happens when Cancel() is called).
	// This prevents double-close when both Cancel() and this defer execute.
	defer func() {
		req.mu.Lock()
		if req.resp != nil && req.resp.Body != nil {
			req.resp.Body.Close()
			req.resp = nil
		}
		req.mu.Unlock()
	}()

	req.resp = resp

	// Track HTTP status code for error type detection
	req.SetHTTPStatus(resp.StatusCode)

	// 3. Check for immediate error
	if resp.StatusCode >= 400 {
		// Read response body for error details
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		bodyStr := string(bodyBytes)

		// Check if the response body contains context overflow patterns
		if models.IsContextOverflowError(fmt.Errorf("%s", bodyStr)) {
			return fmt.Errorf("upstream returned error: %s - %s", resp.Status, bodyStr)
		}

		return fmt.Errorf("upstream returned error: %s", resp.Status)
	}

	// 4. Check if this is a streaming or non-streaming response
	contentType := resp.Header.Get("Content-Type")
	isStreaming := strings.Contains(contentType, "text/event-stream")

	if !isStreaming {
		// Non-streaming response: read entire body as single chunk
		return handleNonStreamingResponse(ctx, cfg, resp, req, finalBody)
	}

	// Streaming response
	req.MarkStreaming()
	// Detect provider for normalization
	provider := normalizers.DetectProvider(cfg.ModelsConfig, req.modelID)
	return handleStreamingResponse(ctx, cfg, resp, req, provider, finalBody)
}

// handleInternalNonStream handles non-streaming requests for internal providers
func handleInternalNonStream(ctx context.Context, provider providers.Provider, req *providers.ChatCompletionRequest, upstreamReq *upstreamRequest, internalModel string, rawBody []byte) error {
	resp, err := provider.ChatCompletion(ctx, req)
	if err != nil {
		// Extract HTTP status from ProviderError if available
		if providerErr, ok := err.(*providers.ProviderError); ok && providerErr.StatusCode > 0 {
			upstreamReq.SetHTTPStatus(providerErr.StatusCode)
		}
		return err
	}

	// Extract usage from response and store it
	if resp.Usage.PromptTokens > 0 || resp.Usage.CompletionTokens > 0 || resp.Usage.TotalTokens > 0 {
		upstreamReq.SetUsage(&TokenUsage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		})
	}

	// Marshal response to JSON
	data, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("failed to marshal response: %w", err)
	}

	// If provider didn't return usage, use fallback token counting
	if upstreamReq.usage == nil || (upstreamReq.usage.PromptTokens == 0 && upstreamReq.usage.CompletionTokens == 0 && upstreamReq.usage.TotalTokens == 0) {
		if token.FallbackEnabled() {
			tokenizer := token.GetTokenizer()
			// Convert bodyMap back to rawBody for fallback counting
			reqBody, _ := json.Marshal(req)
			promptTokens, err := tokenizer.CountPromptTokens(reqBody, internalModel)
			if err != nil {
				log.Printf("[fallback-token-count] error counting prompt tokens: %v, model=%s", err, internalModel)
			}
			// Extract completion text from the response we already have
			var respMap map[string]interface{}
			json.Unmarshal(data, &respMap)
			var completionText string
			if choices, ok := respMap["choices"].([]interface{}); ok && len(choices) > 0 {
				if choice, ok := choices[0].(map[string]interface{}); ok {
					if msg, ok := choice["message"].(map[string]interface{}); ok {
						if content, ok := msg["content"].(string); ok {
							completionText = content
						}
					}
				}
			}
			completionTokens, err := tokenizer.CountCompletionTokens(completionText, internalModel)
			if err != nil {
				log.Printf("[fallback-token-count] error counting completion tokens: %v, model=%s", err, internalModel)
			}
			upstreamReq.SetUsage(&TokenUsage{
				PromptTokens:     promptTokens,
				CompletionTokens: completionTokens,
				TotalTokens:      promptTokens + completionTokens,
			})
			log.Printf("[fallback-token-count] internal non-streaming: model=%s prompt=%d completion=%d total=%d",
				internalModel, promptTokens, completionTokens, promptTokens+completionTokens)
		}
	}

	// Add as single chunk
	if !upstreamReq.buffer.Add(data) {
		return fmt.Errorf("buffer limit exceeded: non-streaming response for model %s", internalModel)
	}

	return nil
}

// handleInternalStream handles streaming requests for internal providers
func handleInternalStream(ctx context.Context, provider providers.Provider, req *providers.ChatCompletionRequest, upstreamReq *upstreamRequest, internalModel string, normCtx *normalizers.NormalizeContext, toolRepairConfig toolrepair.Config, streamDeadline time.Duration, rawBody []byte) error {
	eventCh, err := provider.StreamChatCompletion(ctx, req)
	if err != nil {
		// Extract HTTP status from ProviderError if available
		if providerErr, ok := err.(*providers.ProviderError); ok && providerErr.StatusCode > 0 {
			upstreamReq.SetHTTPStatus(providerErr.StatusCode)
		}
		return err
	}

	upstreamReq.MarkStreaming()

	// Track state for proper streaming format
	firstChunk := true
	nextToolCallIndex := 0
	seenToolCallIDs := make(map[string]int)

	// Create tool call buffer with integrated repair
	// This replaces the separate accumulator + post-stream repair pattern
	// Repair happens during streaming when tool calls are emitted
	var toolCallBuffer *toolcall.ToolCallBuffer
	if toolRepairConfig.Enabled {
		toolCallBuffer = toolcall.NewToolCallBufferWithRepair(
			5*1024*1024, // 5MB default
			internalModel,
			fmt.Sprintf("%d", upstreamReq.id),
			&toolRepairConfig,
		)
	} else {
		// Buffer without repair (repair disabled)
		// This is still needed to accumulate chunked tool call arguments
		toolCallBuffer = toolcall.NewToolCallBuffer(
			5*1024*1024, // 5MB default
			internalModel,
			fmt.Sprintf("%d", upstreamReq.id),
		)
	}

	for event := range eventCh {
		// Check for context cancellation or explicit cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Check if the request was cancelled to exit promptly
		// This ensures the goroutine exits immediately when Cancel() is called,
		// even if context cancellation hasn't propagated yet
		if upstreamReq.IsCancelled() {
			return context.Canceled
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
				return fmt.Errorf("buffer limit exceeded: content chunk for model %s", internalModel)
			}
			firstChunk = false

		case "tool_call":
			// Write tool_call delta
			// Must include index field for each tool call (required for streaming)
			// Use map to control exactly what gets serialized
			if len(event.ToolCalls) > 0 {
				toolCalls := make([]map[string]interface{}, len(event.ToolCalls))
				for i, tc := range event.ToolCalls {
					// Use the index from the tool call delta directly.
					// The provider (OpenAI) already assigns correct indices based on the upstream response.
					// Reassigning indices here causes mismatches when arguments chunks don't have IDs.
					index := tc.Index
					if index == 0 && tc.ID == "" && tc.Function.Name == "" {
						// Fallback: if index is 0 but this is actually position-based (no ID, no name),
						// use position-based index as a last resort
						index = i
					}

					// Track seen IDs for debugging/logging purposes only
					if tc.ID != "" {
						if _, seen := seenToolCallIDs[tc.ID]; !seen {
							seenToolCallIDs[tc.ID] = index
							if index >= nextToolCallIndex {
								nextToolCallIndex = index + 1
							}
						}
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

				// Apply normalization to ensure consistent format
				normalizedLine, modified, normalizerName := normalizers.NormalizeWithContextAndName([]byte(line), normCtx)
				if modified {
					log.Printf("[DEBUG] Race attempt %d (internal): normalized chunk by %s", upstreamReq.id, normalizerName)
				}

				// Process through tool call buffer with integrated repair
				// The buffer accumulates fragments and repairs when complete
				var chunksToEmit [][]byte
				if toolCallBuffer != nil {
					chunksToEmit = toolCallBuffer.ProcessChunk(normalizedLine)
				} else {
					chunksToEmit = [][]byte{normalizedLine}
				}

				// Add all chunks to buffer
				for _, chunk := range chunksToEmit {
					if !upstreamReq.buffer.Add(chunk) {
						return fmt.Errorf("buffer limit exceeded: tool_call chunk for model %s", internalModel)
					}
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
				return fmt.Errorf("buffer limit exceeded: thinking chunk for model %s", internalModel)
			}

		case "done":
			// Flush any remaining buffered tool calls with repair
			if toolCallBuffer != nil {
				flushChunks := toolCallBuffer.Flush()
				for _, chunk := range flushChunks {
					if !upstreamReq.buffer.Add(chunk) {
						log.Printf("[WARN] Race attempt %d (internal): failed to flush tool call chunk", upstreamReq.id)
					}
				}

				// Log repair stats if any repairs occurred
				stats := toolCallBuffer.GetRepairStats()
				if stats.Attempted > 0 {
					log.Printf("[TOOL-BUFFER] Race attempt %d (internal): Repair stats: attempted=%d, success=%d, failed=%d",
						upstreamReq.id, stats.Attempted, stats.Successful, stats.Failed)
				}
			}

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

			// Build final chunk - include usage if available from the done event
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

			// Inject usage from the full response if available
			// This is critical for clients to track token usage for streaming responses
			if event.Response != nil && (event.Response.Usage.PromptTokens > 0 || event.Response.Usage.CompletionTokens > 0 || event.Response.Usage.TotalTokens > 0) {
				finalChunk["usage"] = map[string]int{
					"prompt_tokens":     event.Response.Usage.PromptTokens,
					"completion_tokens": event.Response.Usage.CompletionTokens,
					"total_tokens":      event.Response.Usage.TotalTokens,
				}

				// Also store usage for retrieval after race completes
				upstreamReq.SetUsage(&TokenUsage{
					PromptTokens:     event.Response.Usage.PromptTokens,
					CompletionTokens: event.Response.Usage.CompletionTokens,
					TotalTokens:      event.Response.Usage.TotalTokens,
				})
			}

			finalData, _ := json.Marshal(finalChunk)
			finalLine := fmt.Sprintf("data: %s\n", finalData)
			if !upstreamReq.buffer.Add([]byte(finalLine)) {
				return fmt.Errorf("buffer limit exceeded: final chunk for model %s", internalModel)
			}

			// Write [DONE] marker
			if !upstreamReq.buffer.Add([]byte("data: [DONE]\n")) {
				return fmt.Errorf("buffer limit exceeded: done marker for model %s", internalModel)
			}

			// If provider didn't return usage in the done event, use fallback
			if upstreamReq.usage == nil || (upstreamReq.usage.PromptTokens == 0 && upstreamReq.usage.CompletionTokens == 0 && upstreamReq.usage.TotalTokens == 0) {
				if token.FallbackEnabled() {
					tokenizer := token.GetTokenizer()
					promptTokens, err := tokenizer.CountPromptTokens(rawBody, internalModel)
					if err != nil {
						log.Printf("[fallback-token-count] error counting prompt tokens: %v, model=%s", err, internalModel)
					}
					rawBytes := upstreamReq.buffer.GetAllRawBytesOnce()
					completionText := token.ExtractCompletionTextFromChunks(rawBytes)
					completionTokens, err := tokenizer.CountCompletionTokens(completionText, internalModel)
					if err != nil {
						log.Printf("[fallback-token-count] error counting completion tokens: %v, model=%s", err, internalModel)
					}
					upstreamReq.SetUsage(&TokenUsage{
						PromptTokens:     promptTokens,
						CompletionTokens: completionTokens,
						TotalTokens:      promptTokens + completionTokens,
					})
					log.Printf("[fallback-token-count] internal streaming: model=%s prompt=%d completion=%d total=%d",
						internalModel, promptTokens, completionTokens, promptTokens+completionTokens)
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
func handleNonStreamingResponse(ctx context.Context, cfg *ConfigSnapshot, resp *http.Response, req *upstreamRequest, rawBody []byte) error {
	// Limit body size to 10MB to prevent unbounded memory consumption
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
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

	// Extract usage from response and store it
	var respMap map[string]interface{}
	if err := json.Unmarshal(body, &respMap); err == nil {
		if usageMap, ok := respMap["usage"].(map[string]interface{}); ok {
			promptTokens := intValue(usageMap["prompt_tokens"])
			completionTokens := intValue(usageMap["completion_tokens"])
			totalTokens := intValue(usageMap["total_tokens"])
			if promptTokens > 0 || completionTokens > 0 || totalTokens > 0 {
				req.SetUsage(&TokenUsage{
					PromptTokens:     promptTokens,
					CompletionTokens: completionTokens,
					TotalTokens:      totalTokens,
				})
			}
		}
	}

	// If provider didn't return usage, use fallback token counting
	if req.usage == nil || (req.usage.PromptTokens == 0 && req.usage.CompletionTokens == 0 && req.usage.TotalTokens == 0) {
		if token.FallbackEnabled() {
			tokenizer := token.GetTokenizer()
			promptTokens, err := tokenizer.CountPromptTokens(rawBody, req.modelID)
			if err != nil {
				log.Printf("[fallback-token-count] error counting prompt tokens: %v, model=%s", err, req.modelID)
			}
			completionText := token.ExtractCompletionTextFromJSON(body)
			completionTokens, err := tokenizer.CountCompletionTokens(completionText, req.modelID)
			if err != nil {
				log.Printf("[fallback-token-count] error counting completion tokens: %v, model=%s", err, req.modelID)
			}
			req.SetUsage(&TokenUsage{
				PromptTokens:     promptTokens,
				CompletionTokens: completionTokens,
				TotalTokens:      promptTokens + completionTokens,
			})
			log.Printf("[fallback-token-count] non-streaming: model=%s prompt=%d completion=%d total=%d",
				req.modelID, promptTokens, completionTokens, promptTokens+completionTokens)
		}
	}

	// Add as single chunk (the non-streaming JSON response)
	if !req.buffer.Add(body) {
		return fmt.Errorf("buffer limit exceeded: non-streaming response for model %s", req.modelID)
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

// intValue safely converts an interface{} to int
func intValue(v interface{}) int {
	switch val := v.(type) {
	case float64:
		return int(val)
	case int:
		return val
	case int64:
		return int(val)
	default:
		return 0
	}
}

// extractUsageFromSSEChunk extracts usage data from an SSE chunk if present
// The chunk is expected to be in the format: "data: {...json...}\n"
func extractUsageFromSSEChunk(req *upstreamRequest, line []byte) {
	// Check if this is a data line
	const dataPrefix = "data: "
	if len(line) <= len(dataPrefix) {
		return
	}
	if !bytes.HasPrefix(line, []byte(dataPrefix)) {
		return
	}

	// Extract JSON part (skip "data: " prefix)
	jsonPart := line[len(dataPrefix):]

	// Quick filter: skip unmarshaling if chunk likely has no usage data
	// Most chunks don't contain usage/choices fields, so avoid expensive JSON parse
	if !bytes.Contains(jsonPart, []byte(`"usage"`)) && !bytes.Contains(jsonPart, []byte(`"choices"`)) {
		return
	}

	// Try to parse as JSON
	var chunk map[string]interface{}
	if err := json.Unmarshal(jsonPart, &chunk); err != nil {
		return
	}

	// Look for usage field
	usageMap, ok := chunk["usage"].(map[string]interface{})
	if !ok {
		return
	}

	promptTokens := intValue(usageMap["prompt_tokens"])
	completionTokens := intValue(usageMap["completion_tokens"])
	totalTokens := intValue(usageMap["total_tokens"])

	// Only set if we have meaningful usage data
	if promptTokens > 0 || completionTokens > 0 || totalTokens > 0 {
		req.SetUsage(&TokenUsage{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      totalTokens,
		})
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

// handleStreamingResponse handles SSE streaming responses
// IMPORTANT: This function does NOT return error on idle timeout.
// Per the unified race retry design, the main request should continue streaming
// even after idle timeout - the coordinator will spawn parallel requests.
// Idle timeout detection is handled by the coordinator via TrackActivity().
func handleStreamingResponse(ctx context.Context, cfg *ConfigSnapshot, resp *http.Response, req *upstreamRequest, provider string, rawBody []byte) error {
	// MEMORY TRAP FIX: Use bufio.Reader with increased buffer instead of bufio.Scanner
	// to avoid issues with long SSE lines and memory retention.
	reader := bufio.NewReaderSize(resp.Body, 64*1024) // 64KB buffer

	sawDone := false

	// Create normalization context for this stream
	normCtx := normalizers.NewContext(provider, fmt.Sprintf("%d", req.id))

	// Reset normalizer state for this new stream to avoid state leakage
	normalizers.GetRegistry().ResetAll(normCtx)

	// Create tool call buffer with integrated repair
	// This replaces the separate accumulator + post-stream repair pattern
	// Repair happens during streaming when tool calls are emitted
	var toolCallBuffer *toolcall.ToolCallBuffer
	if !cfg.ToolCallBufferDisabled && cfg.ToolRepair.Enabled {
		toolCallBuffer = toolcall.NewToolCallBufferWithRepair(
			cfg.ToolCallBufferMaxSize,
			req.modelID,
			fmt.Sprintf("%d", req.id),
			&cfg.ToolRepair,
		)
	} else if !cfg.ToolCallBufferDisabled {
		// Buffer without repair (repair disabled)
		toolCallBuffer = toolcall.NewToolCallBuffer(
			cfg.ToolCallBufferMaxSize,
			req.modelID,
			fmt.Sprintf("%d", req.id),
		)
	}

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
			// Check if the request was cancelled to exit promptly
			// This ensures the goroutine exits immediately when Cancel() is called,
			// even if context cancellation hasn't propagated yet
			if req.IsCancelled() {
				return context.Canceled
			}
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

			// IMPORTANT: Apply normalization FIRST
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

			// Process through tool call buffer (if enabled)
			// The buffer accumulates tool call fragments, repairs when complete, and emits
			// Non-tool-call chunks pass through immediately
			var chunksToEmit [][]byte
			if toolCallBuffer != nil {
				chunksToEmit = toolCallBuffer.ProcessChunk(normalizedLine)
			} else {
				chunksToEmit = [][]byte{normalizedLine}
			}

			// Add all chunks to buffer
			for _, chunk := range chunksToEmit {
				if !req.buffer.Add(chunk) {
					return fmt.Errorf("buffer limit exceeded: streaming tool_call chunk for model %s", req.modelID)
				}
			}

			// Extract usage from SSE chunk if present
			extractUsageFromSSEChunk(req, normalizedLine)

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

	// Flush any remaining buffered tool calls
	// This ensures even incomplete tool calls are emitted at stream end
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

		// Log repair stats if any repairs occurred
		stats := toolCallBuffer.GetRepairStats()
		if stats.Attempted > 0 {
			log.Printf("[TOOL-BUFFER] Race attempt %d: Repair stats: attempted=%d, success=%d, failed=%d",
				req.id, stats.Attempted, stats.Successful, stats.Failed)

			// Publish event to frontend if event bus is available
			if cfg.EventBus != nil {
				cfg.EventBus.Publish(events.Event{
					Type:      "tool_repair",
					Timestamp: time.Now().Unix(),
					Data: map[string]interface{}{
						"id":          fmt.Sprintf("%d", req.id),
						"provider":    provider,
						"description": fmt.Sprintf("Repaired %d malformed JSON in tool_call arguments (during streaming)", stats.Successful),
					},
				})
			}
		}
	}

	// If no usage was found during streaming, use fallback
	if req.usage == nil || (req.usage.PromptTokens == 0 && req.usage.CompletionTokens == 0 && req.usage.TotalTokens == 0) {
		if token.FallbackEnabled() {
			tokenizer := token.GetTokenizer()
			promptTokens, err := tokenizer.CountPromptTokens(rawBody, req.modelID)
			if err != nil {
				log.Printf("[fallback-token-count] error counting prompt tokens: %v, model=%s", err, req.modelID)
			}
			rawBytes := req.buffer.GetAllRawBytesOnce()
			completionText := token.ExtractCompletionTextFromChunks(rawBytes)
			completionTokens, err := tokenizer.CountCompletionTokens(completionText, req.modelID)
			if err != nil {
				log.Printf("[fallback-token-count] error counting completion tokens: %v, model=%s", err, req.modelID)
			}
			req.SetUsage(&TokenUsage{
				PromptTokens:     promptTokens,
				CompletionTokens: completionTokens,
				TotalTokens:      promptTokens + completionTokens,
			})
			log.Printf("[fallback-token-count] streaming: model=%s prompt=%d completion=%d total=%d",
				req.modelID, promptTokens, completionTokens, promptTokens+completionTokens)
		}
	}

	return nil
}
