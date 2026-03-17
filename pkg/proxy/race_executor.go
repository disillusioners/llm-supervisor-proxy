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
		return handleInternalStream(ctx, providerClient, providerReq, req, internalModel)
	}
	return handleInternalNonStream(ctx, providerClient, providerReq, req, internalModel)
}

// executeExternalRequest handles requests to external upstream (LiteLLM, etc.)
func executeExternalRequest(ctx context.Context, cfg *ConfigSnapshot, originalReq *http.Request, rawBody []byte, req *upstreamRequest) error {
	// 1. Prepare upstream request
	// Set the target URL to upstream
	u, err := url.Parse(cfg.UpstreamURL)
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
func handleInternalStream(ctx context.Context, provider providers.Provider, req *providers.ChatCompletionRequest, upstreamReq *upstreamRequest, internalModel string) error {
	eventCh, err := provider.StreamChatCompletion(ctx, req)
	if err != nil {
		return err
	}

	upstreamReq.MarkStreaming()

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
			line := fmt.Sprintf("data: %s\n", data)
			if !upstreamReq.buffer.Add([]byte(line)) {
				return fmt.Errorf("buffer limit exceeded")
			}

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
				line := fmt.Sprintf("data: %s\n", data)
				if !upstreamReq.buffer.Add([]byte(line)) {
					return fmt.Errorf("buffer limit exceeded")
				}
			}

		case "thinking":
			// Write thinking/reasoning content
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
			line := fmt.Sprintf("data: %s\n", data)
			if !upstreamReq.buffer.Add([]byte(line)) {
				return fmt.Errorf("buffer limit exceeded")
			}

		case "done":
			// Write [DONE] marker
			if !upstreamReq.buffer.Add([]byte("data: [DONE]\n")) {
				return fmt.Errorf("buffer limit exceeded")
			}
			return nil

		case "error":
			log.Printf("[RACE] Internal provider stream error: %s", event.Content)
			return fmt.Errorf("provider stream error: %s", event.Content)
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

// handleNonStreamingResponse reads a non-streaming JSON response
func handleNonStreamingResponse(ctx context.Context, cfg *ConfigSnapshot, resp *http.Response, req *upstreamRequest) error {
	// Read entire body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read error: %w", err)
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
	default:
		return "Normalized stream chunk"
	}
}

// handleStreamingResponse handles SSE streaming responses
func handleStreamingResponse(ctx context.Context, cfg *ConfigSnapshot, resp *http.Response, req *upstreamRequest, provider string) error {
	// MEMORY TRAP FIX: Use bufio.Reader with increased buffer instead of bufio.Scanner
	// to avoid issues with long SSE lines and memory retention.
	reader := bufio.NewReaderSize(resp.Body, 64*1024) // 64KB buffer

	// Create ticker outside the loop
	idleTimer := time.NewTimer(time.Duration(cfg.IdleTimeout))
	defer idleTimer.Stop()

	sawDone := false

	// Create normalization context for this stream
	normCtx := normalizers.NewContext(provider, fmt.Sprintf("%d", req.id))

	// Reset normalizer state for this new stream to avoid state leakage
	normalizers.GetRegistry().ResetAll(normCtx)

	for {
		// Set idle timeout for reading
		var line []byte
		var readErr error

		// Setup idle timeout wrapper
		readDone := make(chan struct{})
		go func() {
			line, readErr = reader.ReadBytes('\n')
			close(readDone)
		}()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-readDone:
			// Reset idle timer after successful read
			if !idleTimer.Stop() {
				select {
				case <-idleTimer.C:
				default:
				}
			}
			idleTimer.Reset(time.Duration(cfg.IdleTimeout))
			// Continuous processing
		case <-idleTimer.C:
			return fmt.Errorf("idle timeout exceeded")
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

			// Apply normalization to fix malformed chunks
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

			// Add chunk to buffer
			if !req.buffer.Add(normalizedLine) {
				return fmt.Errorf("buffer limit exceeded")
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
	return nil
}
