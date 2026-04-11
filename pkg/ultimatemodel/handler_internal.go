package ultimatemodel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy/token"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/toolcall"
)

// executeInternal handles requests to internal providers (bypassing upstream)
// This is a RAW PROXY - no retry, no fallback, no buffering, no loop detection
func (h *Handler) executeInternal(
	ctx context.Context,
	w http.ResponseWriter,
	requestBody map[string]interface{},
	requestBodyBytes []byte,
	modelCfg *models.ModelConfig,
	isStream bool,
) (*store.Usage, error) {
	// Resolve internal config (including credential lookup)
	provider, apiKey, baseURL, internalModel, ok := h.modelsMgr.ResolveInternalConfig(modelCfg.ID)
	if !ok {
		return nil, fmt.Errorf("failed to resolve internal config for model %s", modelCfg.ID)
	}

	// Create provider
	providerClient, err := providers.NewProvider(provider, apiKey, baseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to create provider: %w", err)
	}

	// Convert request
	req, err := h.convertRequest(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to convert request: %w", err)
	}

	// Override model with internal model name
	req.Model = internalModel

	if isStream {
		return h.handleInternalStream(ctx, providerClient, req, w, internalModel, requestBodyBytes)
	}
	return h.handleInternalNonStream(ctx, providerClient, req, w, internalModel, requestBodyBytes)
}

// handleInternalNonStream handles non-streaming requests for internal providers
func (h *Handler) handleInternalNonStream(
	ctx context.Context,
	provider providers.Provider,
	req *providers.ChatCompletionRequest,
	w http.ResponseWriter,
	internalModel string,
	requestBodyBytes []byte,
) (*store.Usage, error) {
	resp, err := provider.ChatCompletion(ctx, req)
	if err != nil {
		return nil, err
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		return nil, err
	}

	// Extract usage from response
	usage := &store.Usage{
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		TotalTokens:      resp.Usage.TotalTokens,
	}

	// Fallback token counting if usage is nil/zero
	if usage == nil || (usage.PromptTokens == 0 && usage.CompletionTokens == 0 && usage.TotalTokens == 0) {
		if token.FallbackEnabled() {
			tokenizer := token.GetTokenizer()
			promptTokens, err := tokenizer.CountPromptTokens(requestBodyBytes, internalModel)
			if err != nil {
				log.Printf("[fallback-token-count] error counting prompt tokens: %v, model=%s", err, internalModel)
			}
			respBytes, _ := json.Marshal(resp)
			completionText := token.ExtractCompletionTextFromJSON(respBytes)
			completionTokens, err := tokenizer.CountCompletionTokens(completionText, internalModel)
			if err != nil {
				log.Printf("[fallback-token-count] error counting completion tokens: %v, model=%s", err, internalModel)
			}
			usage = &store.Usage{
				PromptTokens:     promptTokens,
				CompletionTokens: completionTokens,
				TotalTokens:      promptTokens + completionTokens,
			}
			log.Printf("[fallback-token-count] ultimate-internal: model=%s prompt=%d completion=%d total=%d",
				internalModel, promptTokens, completionTokens, promptTokens+completionTokens)
		}
	}

	return usage, nil
}

// handleInternalStream handles streaming requests for internal providers
func (h *Handler) handleInternalStream(
	ctx context.Context,
	provider providers.Provider,
	req *providers.ChatCompletionRequest,
	w http.ResponseWriter,
	internalModel string,
	requestBodyBytes []byte,
) (*store.Usage, error) {
	eventCh, err := provider.StreamChatCompletion(ctx, req)
	if err != nil {
		return nil, err
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("streaming not supported")
	}

	// Create tool call buffer with integrated repair
	// This replaces the separate accumulator + post-stream repair pattern
	// Repair happens during streaming when tool calls are emitted
	var toolCallBuffer *toolcall.ToolCallBuffer
	if !h.toolCallBufferDisabled && h.toolRepairConfig != nil && h.toolRepairConfig.Enabled {
		toolCallBuffer = toolcall.NewToolCallBufferWithRepair(
			h.toolCallBufferMaxSize,
			internalModel,
			"ultimate",
			h.toolRepairConfig,
		)
	} else if !h.toolCallBufferDisabled {
		// Buffer without repair (repair disabled)
		toolCallBuffer = toolcall.NewToolCallBuffer(
			h.toolCallBufferMaxSize,
			internalModel,
			"ultimate",
		)
	}

	// Track state for proper streaming format
	firstChunk := true
	nextToolCallIndex := 0
	seenToolCallIDs := make(map[string]int)

	// Track usage from done event
	var extractedUsage *store.Usage

	// Accumulate raw SSE chunks for fallback token counting
	var rawChunks bytes.Buffer

	for event := range eventCh {
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
			fmt.Fprintf(w, "data: %s\n\n", data)
			rawChunks.Write(data)
			rawChunks.Write([]byte("\n\n"))
			flusher.Flush()
			firstChunk = false

		case "tool_call":
			// Write tool_call delta
			// Must include index field for each tool call (required for streaming)
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
				line := fmt.Sprintf("data: %s\n\n", data)

				// Process through tool call buffer (if enabled)
				// The buffer accumulates tool call fragments, repairs when complete, and emits
				// Non-tool-call chunks pass through immediately
				var chunksToEmit [][]byte
				if toolCallBuffer != nil {
					chunksToEmit = toolCallBuffer.ProcessChunk([]byte(line))
				} else {
					chunksToEmit = [][]byte{[]byte(line)}
				}

				// Write all chunks to client
				for _, chunk := range chunksToEmit {
					w.Write(chunk)
					rawChunks.Write(chunk)
				}
				flusher.Flush()
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
			fmt.Fprintf(w, "data: %s\n\n", data)
			rawChunks.Write(data)
			rawChunks.Write([]byte("\n\n"))
			flusher.Flush()

		case "done":
			// Flush any remaining buffered tool calls with repair
			if toolCallBuffer != nil {
				flushChunks := toolCallBuffer.Flush()
				for _, chunk := range flushChunks {
					w.Write(chunk)
				}

				// Log repair stats if any repairs occurred
				stats := toolCallBuffer.GetRepairStats()
				if stats.Attempted > 0 {
					log.Printf("[TOOL-BUFFER] UltimateModel: Repair stats: attempted=%d, success=%d, failed=%d",
						stats.Attempted, stats.Successful, stats.Failed)
				}
			}

			// Extract usage from the done event's full response
			if event.Response != nil {
				extractedUsage = &store.Usage{
					PromptTokens:     event.Response.Usage.PromptTokens,
					CompletionTokens: event.Response.Usage.CompletionTokens,
					TotalTokens:      event.Response.Usage.TotalTokens,
				}
			}

			// Write finish chunk with finish_reason before [DONE]
			// This is required by OpenAI streaming format - clients expect finish_reason in the last chunk
			// Use the finish_reason from the event (e.g., "tool_calls" for tool calls, "stop" for normal completion)
			finishReason := event.FinishReason
			if finishReason == "" {
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
			fmt.Fprintf(w, "data: %s\n\n", finalData)
			rawChunks.Write(finalData)
			rawChunks.Write([]byte("\n\n"))

			// Write [DONE] marker
			fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()

			// Fallback token counting if usage is nil/zero
			if extractedUsage == nil || (extractedUsage.PromptTokens == 0 && extractedUsage.CompletionTokens == 0 && extractedUsage.TotalTokens == 0) {
				if token.FallbackEnabled() {
					tokenizer := token.GetTokenizer()
					promptTokens, err := tokenizer.CountPromptTokens(requestBodyBytes, internalModel)
					if err != nil {
						log.Printf("[fallback-token-count] error counting prompt tokens: %v, model=%s", err, internalModel)
					}
					completionText := token.ExtractCompletionTextFromChunks(rawChunks.Bytes())
					completionTokens, err := tokenizer.CountCompletionTokens(completionText, internalModel)
					if err != nil {
						log.Printf("[fallback-token-count] error counting completion tokens: %v, model=%s", err, internalModel)
					}
					extractedUsage = &store.Usage{
						PromptTokens:     promptTokens,
						CompletionTokens: completionTokens,
						TotalTokens:      promptTokens + completionTokens,
					}
					log.Printf("[fallback-token-count] ultimate-internal: model=%s prompt=%d completion=%d total=%d",
						internalModel, promptTokens, completionTokens, promptTokens+completionTokens)
				}
			}

		case "error":
			// Write error as SSE event
			errMsg := "unknown error"
			if event.Error != nil {
				errMsg = event.Error.Error()
			}
			log.Printf("[UltimateModel] Stream error from provider: %s", errMsg)
			// If headers not sent, we can return error
			// Otherwise, we need to send SSE error
			errorChunk := map[string]interface{}{
				"error": map[string]string{
					"message": errMsg,
					"type":    "ultimate_model_error",
				},
			}
			data, _ := json.Marshal(errorChunk)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
			return nil, fmt.Errorf("provider stream error: %s", errMsg)
		}
	}

	return extractedUsage, nil
}

// convertRequest converts map[string]interface{} to providers.ChatCompletionRequest
func (h *Handler) convertRequest(body map[string]interface{}) (*providers.ChatCompletionRequest, error) {
	req := &providers.ChatCompletionRequest{}

	if model, ok := body["model"].(string); ok {
		req.Model = model
	}

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
						log.Printf("[WARN] UltimateModel: Message[%d] has role='tool' but missing tool_call_id - this may cause MiniMax API error", msgIdx)
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
