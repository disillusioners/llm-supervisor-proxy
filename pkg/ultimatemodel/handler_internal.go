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
				log.Printf("[DEBUG][fallback-token-count] error counting prompt tokens: %v, model=%s", err, internalModel)
			}
			respBytes, _ := json.Marshal(resp)
			completionText := token.ExtractCompletionTextFromJSON(respBytes)
			completionTokens, err := tokenizer.CountCompletionTokens(completionText, internalModel)
			if err != nil {
				log.Printf("[DEBUG][fallback-token-count] error counting completion tokens: %v, model=%s", err, internalModel)
			}
			usage = &store.Usage{
				PromptTokens:     promptTokens,
				CompletionTokens: completionTokens,
				TotalTokens:      promptTokens + completionTokens,
			}
			log.Printf("[DEBUG][fallback-token-count] ultimate-internal: model=%s prompt=%d completion=%d total=%d",
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
	var toolCallBuffer *toolcall.ToolCallBuffer
	if !h.toolCallBufferDisabled && h.toolRepairConfig != nil && h.toolRepairConfig.Enabled {
		toolCallBuffer = toolcall.NewToolCallBufferWithRepair(
			h.toolCallBufferMaxSize,
			internalModel,
			"ultimate",
			h.toolRepairConfig,
		)
	} else if !h.toolCallBufferDisabled {
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

	// Create a simple buffer to batch writes like the race executor does
	var buf bytes.Buffer

	for event := range eventCh {
		switch event.Type {
		case "content":
			// Write SSE data event
			var data []byte
			if firstChunk {
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
			buf.WriteString("data: ")
			buf.Write(data)
			buf.WriteString("\n\n")
			firstChunk = false

		case "tool_call":
			if len(event.ToolCalls) > 0 {
				toolCalls := make([]map[string]interface{}, len(event.ToolCalls))
				for i, tc := range event.ToolCalls {
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
				line := "data: " + string(data) + "\n\n"

				var chunksToEmit [][]byte
				if toolCallBuffer != nil {
					chunksToEmit = toolCallBuffer.ProcessChunk([]byte(line))
				} else {
					chunksToEmit = [][]byte{[]byte(line)}
				}

				for _, chunk := range chunksToEmit {
					buf.Write(chunk)
				}
			}

		case "thinking":
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
			buf.WriteString("data: ")
			buf.Write(data)
			buf.WriteString("\n\n")

		case "done":
			// Flush remaining tool calls
			if toolCallBuffer != nil {
				flushChunks := toolCallBuffer.Flush()
				for _, chunk := range flushChunks {
					buf.Write(chunk)
				}
				stats := toolCallBuffer.GetRepairStats()
				if stats.Attempted > 0 {
					log.Printf("[TOOL-BUFFER] UltimateModel Internal: Repair stats: attempted=%d, success=%d, failed=%d",
						stats.Attempted, stats.Successful, stats.Failed)
				}
			}

			// Extract usage
			if event.Response != nil {
				extractedUsage = &store.Usage{
					PromptTokens:     event.Response.Usage.PromptTokens,
					CompletionTokens: event.Response.Usage.CompletionTokens,
					TotalTokens:      event.Response.Usage.TotalTokens,
				}
			}

			// Write finish chunk
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
			buf.WriteString("data: ")
			buf.Write(finalData)
			buf.WriteString("\n\n")
			buf.WriteString("data: [DONE]\n\n")

			// Fallback token counting
			if extractedUsage == nil || (extractedUsage.PromptTokens == 0 && extractedUsage.CompletionTokens == 0 && extractedUsage.TotalTokens == 0) {
				if token.FallbackEnabled() {
					tokenizer := token.GetTokenizer()
					promptTokens, err := tokenizer.CountPromptTokens(requestBodyBytes, internalModel)
					if err != nil {
						log.Printf("[DEBUG][fallback-token-count] error counting prompt tokens: %v", err)
					}
					completionText := token.ExtractCompletionTextFromChunks(buf.Bytes())
					completionTokens, err := tokenizer.CountCompletionTokens(completionText, internalModel)
					if err != nil {
						log.Printf("[DEBUG][fallback-token-count] error counting completion tokens: %v", err)
					}
					extractedUsage = &store.Usage{
						PromptTokens:     promptTokens,
						CompletionTokens: completionTokens,
						TotalTokens:      promptTokens + completionTokens,
					}
					log.Printf("[DEBUG][fallback-token-count] ultimate-internal: model=%s prompt=%d completion=%d total=%d",
						internalModel, promptTokens, completionTokens, promptTokens+completionTokens)
				}
			}

			// Flush everything to client
			rawChunks.Write(buf.Bytes())
			w.Write(buf.Bytes())
			flusher.Flush()

			return extractedUsage, nil

		case "error":
			errMsg := ""
			if event.Error != nil {
				errMsg = event.Error.Error()
			}
			errResp := models.NewOpenAIError(models.ErrorTypeServerError, "", errMsg)
			data, _ := json.Marshal(errResp)
			buf.WriteString("data: ")
			buf.Write(data)
			buf.WriteString("\n\n")
			rawChunks.Write(buf.Bytes())
			w.Write(buf.Bytes())
			flusher.Flush()
			return nil, fmt.Errorf("provider error: %s", errMsg)
		}
	}

	return nil, fmt.Errorf("event channel closed unexpectedly")
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
