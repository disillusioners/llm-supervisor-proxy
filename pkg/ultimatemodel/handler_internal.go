package ultimatemodel

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
)

// executeInternal handles requests to internal providers (bypassing upstream)
// This is a RAW PROXY - no retry, no fallback, no buffering, no loop detection
func (h *Handler) executeInternal(
	ctx context.Context,
	w http.ResponseWriter,
	requestBody map[string]interface{},
	modelCfg *models.ModelConfig,
	isStream bool,
) error {
	// Resolve internal config (including credential lookup)
	provider, apiKey, baseURL, internalModel, ok := h.modelsMgr.ResolveInternalConfig(modelCfg.ID)
	if !ok {
		return fmt.Errorf("failed to resolve internal config for model %s", modelCfg.ID)
	}

	// Create provider
	providerClient, err := providers.NewProvider(provider, apiKey, baseURL)
	if err != nil {
		return fmt.Errorf("failed to create provider: %w", err)
	}

	// Convert request
	req, err := h.convertRequest(requestBody)
	if err != nil {
		return fmt.Errorf("failed to convert request: %w", err)
	}

	// Override model with internal model name
	req.Model = internalModel

	if isStream {
		return h.handleInternalStream(ctx, providerClient, req, w, internalModel)
	}
	return h.handleInternalNonStream(ctx, providerClient, req, w, internalModel)
}

// handleInternalNonStream handles non-streaming requests for internal providers
func (h *Handler) handleInternalNonStream(
	ctx context.Context,
	provider providers.Provider,
	req *providers.ChatCompletionRequest,
	w http.ResponseWriter,
	internalModel string,
) error {
	resp, err := provider.ChatCompletion(ctx, req)
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(resp)
}

// handleInternalStream handles streaming requests for internal providers
func (h *Handler) handleInternalStream(
	ctx context.Context,
	provider providers.Provider,
	req *providers.ChatCompletionRequest,
	w http.ResponseWriter,
	internalModel string,
) error {
	eventCh, err := provider.StreamChatCompletion(ctx, req)
	if err != nil {
		return err
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("streaming not supported")
	}

	for event := range eventCh {
		switch event.Type {
		case "content":
			// Write SSE data event
			chunk := providers.ChatCompletionResponse{
				ID:      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   internalModel,
				Choices: []providers.Choice{
					{
						Index: 0,
						Delta: &providers.ChatMessage{
							Role:    "assistant",
							Content: event.Content,
						},
					},
				},
			}
			data, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()

		case "tool_call":
			// Write tool_call delta
			if len(event.ToolCalls) > 0 {
				tc := event.ToolCalls[0]
				chunk := providers.ChatCompletionResponse{
					ID:      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
					Object:  "chat.completion.chunk",
					Created: time.Now().Unix(),
					Model:   internalModel,
					Choices: []providers.Choice{
						{
							Index: 0,
							Delta: &providers.ChatMessage{
								ToolCalls: []providers.ToolCall{
									{
										ID:   tc.ID,
										Type: tc.Type,
										Function: providers.ToolCallFunction{
											Name:      tc.Function.Name,
											Arguments: tc.Function.Arguments,
										},
									},
								},
							},
						},
					},
				}
				data, _ := json.Marshal(chunk)
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
			}

		case "thinking":
			// Write thinking/reasoning content
			// Note: Some providers may support reasoning content in the future
			// For now, we treat it as regular content
			chunk := providers.ChatCompletionResponse{
				ID:      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   internalModel,
				Choices: []providers.Choice{
					{
						Index: 0,
						Delta: &providers.ChatMessage{
							Content: event.Content,
						},
					},
				},
			}
			data, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()

		case "done":
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

			// Write [DONE] marker
			fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()

		case "error":
			// Write error as SSE event
			log.Printf("[UltimateModel] Stream error from provider: %s", event.Content)
			// If headers not sent, we can return error
			// Otherwise, we need to send SSE error
			errorChunk := map[string]interface{}{
				"error": map[string]string{
					"message": event.Content,
					"type":    "ultimate_model_error",
				},
			}
			data, _ := json.Marshal(errorChunk)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
			return fmt.Errorf("provider stream error: %s", event.Content)
		}
	}

	return nil
}

// convertRequest converts map[string]interface{} to providers.ChatCompletionRequest
func (h *Handler) convertRequest(body map[string]interface{}) (*providers.ChatCompletionRequest, error) {
	req := &providers.ChatCompletionRequest{}

	if model, ok := body["model"].(string); ok {
		req.Model = model
	}

	if messages, ok := body["messages"].([]interface{}); ok {
		for _, m := range messages {
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
