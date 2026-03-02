package proxy

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

// InternalHandler handles requests to internal providers (bypassing upstream)
type InternalHandler struct {
	config   *models.ModelConfig
	resolver models.ModelsConfigInterface // Resolver for credentials
}

// NewInternalHandler creates a new internal handler for a model
// The resolver is used to resolve credentials from the model's credential_id
func NewInternalHandler(config *models.ModelConfig, resolver models.ModelsConfigInterface) *InternalHandler {
	return &InternalHandler{config: config, resolver: resolver}
}

// CanHandleInternal checks if a model should use internal upstream
func CanHandleInternal(modelConfig *models.ModelConfig) bool {
	return modelConfig != nil && modelConfig.Internal
}

// HandleRequest handles a request using internal provider
func (h *InternalHandler) HandleRequest(ctx context.Context, requestBody map[string]interface{}, w http.ResponseWriter, isStream bool) error {
	// Resolve internal config (including credential lookup)
	provider, apiKey, baseURL, internalModel, ok := h.resolver.ResolveInternalConfig(h.config.ID)
	if !ok {
		return fmt.Errorf("failed to resolve internal config for model %s", h.config.ID)
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
		return h.handleStream(ctx, providerClient, req, w, internalModel)
	}
	return h.handleNonStream(ctx, providerClient, req, w, internalModel)
}

// handleNonStream handles non-streaming requests
func (h *InternalHandler) handleNonStream(ctx context.Context, provider providers.Provider, req *providers.ChatCompletionRequest, w http.ResponseWriter, internalModel string) error {
	resp, err := provider.ChatCompletion(ctx, req)
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(resp)
}

// handleStream handles streaming requests
func (h *InternalHandler) handleStream(ctx context.Context, provider providers.Provider, req *providers.ChatCompletionRequest, w http.ResponseWriter, internalModel string) error {
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
		log.Printf("[DEBUG INTERNAL] Received event: type=%s, content=%.100s", event.Type, event.Content)
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
			log.Printf("[DEBUG INTERNAL] Writing chunk: %s", string(data))
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()

		case "tool_call":
			// Write tool_call delta
			chunk := providers.ChatCompletionResponse{
				ID:      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   internalModel,
				Choices: []providers.Choice{
					{
						Index: 0,
						Delta: &providers.ChatMessage{
							Role:      "assistant",
							ToolCalls: event.ToolCalls,
						},
					},
				},
			}
			data, _ := json.Marshal(chunk)
			log.Printf("[DEBUG INTERNAL] Writing tool_call chunk: %s", string(data))
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()

		case "done":
			// Write finish chunk
			finishReason := event.FinishReason
			if finishReason == "" {
				finishReason = "stop"
			}
			chunk := providers.ChatCompletionResponse{
				ID:      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   internalModel,
				Choices: []providers.Choice{
					{
						Index:        0,
						Delta:        &providers.ChatMessage{},
						FinishReason: finishReason,
					},
				},
			}
			data, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", data)

			// Write [DONE]
			fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
			return nil

		case "error":
			log.Printf("Stream error: %v", event.Error)
			return event.Error
		}
	}

	return nil
}

// convertRequest converts map[string]interface{} to ChatCompletionRequest
func (h *InternalHandler) convertRequest(body map[string]interface{}) (*providers.ChatCompletionRequest, error) {
	req := &providers.ChatCompletionRequest{
		Extra: make(map[string]interface{}),
	}

	// Model
	if v, ok := body["model"].(string); ok {
		req.Model = v
	}

	// Messages
	if msgs, ok := body["messages"].([]interface{}); ok {
		for _, m := range msgs {
			if msgMap, ok := m.(map[string]interface{}); ok {
				msg := providers.ChatMessage{}
				if role, ok := msgMap["role"].(string); ok {
					msg.Role = role
				}
				// Handle content as string or array
				// Flatten array to string for provider compatibility
				if content, ok := msgMap["content"]; ok {
					switch c := content.(type) {
					case string:
						msg.Content = c
					case []interface{}:
						// Flatten array content to string for provider compatibility
						var contentStr string
						for _, part := range c {
							if partMap, ok := part.(map[string]interface{}); ok {
								if text, ok := partMap["text"].(string); ok {
									contentStr += text
								}
							}
						}
						msg.Content = contentStr
					default:
						// Unsupported content type, skip or handle as needed
						log.Printf("Unsupported content type: %T", content)
						msg.Content = ""
					}

				}
				if name, ok := msgMap["name"].(string); ok {
					msg.Name = name
				}
				// Handle tool_calls in messages
				if toolCalls, ok := msgMap["tool_calls"].([]interface{}); ok {
					msg.ToolCalls = make([]providers.ToolCall, 0, len(toolCalls))
					for _, tc := range toolCalls {
						if tcMap, ok := tc.(map[string]interface{}); ok {
							toolCall := providers.ToolCall{}
							if id, ok := tcMap["id"].(string); ok {
								toolCall.ID = id
							}
							if t, ok := tcMap["type"].(string); ok {
								toolCall.Type = t
							}
							if fn, ok := tcMap["function"].(map[string]interface{}); ok {
								if name, ok := fn["name"].(string); ok {
									toolCall.Function.Name = name
								}
								if args, ok := fn["arguments"].(string); ok {
									toolCall.Function.Arguments = args
								}
							}
							msg.ToolCalls = append(msg.ToolCalls, toolCall)
						}
					}
				}
				req.Messages = append(req.Messages, msg)
			}
		}
	}

	// Optional parameters
	if v, ok := body["max_tokens"].(float64); ok {
		vi := int(v)
		req.MaxTokens = &vi
	}
	if v, ok := body["temperature"].(float64); ok {
		req.Temperature = &v
	}
	if v, ok := body["top_p"].(float64); ok {
		req.TopP = &v
	}
	if v, ok := body["n"].(float64); ok {
		vi := int(v)
		req.N = &vi
	}
	if v, ok := body["stream"].(bool); ok {
		req.Stream = v
	}
	if v, ok := body["stop"]; ok {
		req.Stop = v
	}
	if v, ok := body["presence_penalty"].(float64); ok {
		req.PresencePenalty = &v
	}
	if v, ok := body["frequency_penalty"].(float64); ok {
		req.FrequencyPenalty = &v
	}
	if v, ok := body["logit_bias"].(map[string]interface{}); ok {
		req.LogitBias = make(map[string]float64)
		for k, val := range v {
			if f, ok := val.(float64); ok {
				req.LogitBias[k] = f
			}
		}
	}
	if v, ok := body["user"].(string); ok {
		req.User = v
	}

	// Handle tools
	if tools, ok := body["tools"].([]interface{}); ok {
		req.Tools = make([]providers.Tool, 0, len(tools))
		for _, t := range tools {
			if toolMap, ok := t.(map[string]interface{}); ok {
				tool := providers.Tool{}
				if typ, ok := toolMap["type"].(string); ok {
					tool.Type = typ
				}
				if fn, ok := toolMap["function"].(map[string]interface{}); ok {
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
				req.Tools = append(req.Tools, tool)
			}
		}
	}

	// Handle tool_choice
	if toolChoice, ok := body["tool_choice"]; ok {
		req.ToolChoice = toolChoice
	}

	// Store any extra fields not handled above
	knownFields := map[string]bool{
		"model": true, "messages": true, "max_tokens": true, "temperature": true,
		"top_p": true, "n": true, "stream": true, "stop": true,
		"presence_penalty": true, "frequency_penalty": true, "logit_bias": true, "user": true,
		"tools": true, "tool_choice": true,
	}
	for k, v := range body {
		if !knownFields[k] {
			req.Extra[k] = v
		}
	}

	return req, nil
}
