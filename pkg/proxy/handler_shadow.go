// ─────────────────────────────────────────────────────────────────────────────
// Shadow Retry
// ─────────────────────────────────────────────────────────────────────────────

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
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
)

// sendShadowResult sends result to shadow channel in a non-blocking manner.
// Returns (sent=true, closed=false) on success, (sent=false, closed=true) if channel is closed,
// or (sent=false, closed=false) if channel is full.
// The caller should exit immediately if closed=true to avoid unnecessary work.
func sendShadowResult(ch chan shadowResult, result shadowResult) (sent bool, closed bool) {
	// Recover from panic when sending to a closed channel
	defer func() {
		if r := recover(); r != nil {
			// Channel was closed - this is expected if main request finished
			log.Printf("[SHADOW] Channel closed, result not sent")
			sent = false
			closed = true
		}
	}()

	select {
	case ch <- result:
		return true, false
	default:
		// Channel is full (not closed, since closed channels would panic above)
		// Try a non-blocking receive to verify channel state
		select {
		case _, ok := <-ch:
			if !ok {
				// Channel is closed (shouldn't reach here due to panic, but safety check)
				return false, true
			}
			// Channel had data, we drained it. Try sending again.
			select {
			case ch <- result:
				return true, false
			default:
				// Still full after draining
				log.Printf("[SHADOW] Channel full, result not sent")
				return false, false
			}
		default:
			// Channel is empty but full (buffer full with no readers)
			log.Printf("[SHADOW] Channel full, result not sent")
			return false, false
		}
	}
}

// shouldStartShadow checks if shadow retry should be triggered
func shouldStartShadow(rc *requestContext, counters *retryCounters) bool {
	// Must be enabled in config
	if !rc.conf.ShadowRetryEnabled {
		return false
	}
	// Only on first idle timeout
	if counters.idleRetries != 0 {
		return false
	}
	// Must have a fallback model available (next in chain after current)
	shadowModelIndex := rc.currentModelIndex + 1
	if shadowModelIndex >= len(rc.modelList) {
		return false
	}
	// Shadow must not already be running
	if rc.shadow != nil {
		return false
	}
	return true
}

// startShadowRequest spawns a background goroutine to make a parallel request
// to the fallback model. If shadow completes successfully before the main request,
// its buffer will be used instead.
func (h *Handler) startShadowRequest(rc *requestContext) {
	// Get next fallback model in chain
	shadowModelIndex := rc.currentModelIndex + 1
	if shadowModelIndex >= len(rc.modelList) {
		return
	}
	shadowModel := rc.modelList[shadowModelIndex]

	// Check if shadow model is internal (direct provider call)
	var isInternalShadow bool
	var shadowModelConfig *models.ModelConfig
	if rc.conf.ModelsConfig != nil {
		if modelCfg := rc.conf.ModelsConfig.GetModel(shadowModel); modelCfg != nil && modelCfg.Internal {
			isInternalShadow = true
			shadowModelConfig = modelCfg
		}
	}

	// Create shadow state
	shadowCtx, shadowCancel := context.WithCancel(rc.baseCtx)

	rc.shadow = &shadowRequestState{
		done:       make(chan shadowResult, 1),
		cancelFunc: shadowCancel,
		started:    true,
		model:      shadowModel,
		startTime:  time.Now(),
	}

	// Track if goroutine was successfully launched
	goroutineLaunched := false

	// Ensure cleanup on failure (panic or error before goroutine launch)
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[SHADOW] Panic during start for request %s: %v", rc.reqID, r)
			// Clean up shadow state on panic
			if !goroutineLaunched && rc.shadow != nil {
				rc.shadow.Cancel()
				rc.shadow.Close()
				rc.shadow = nil
			}
		}
	}()

	h.publishEvent("shadow_retry_started", map[string]interface{}{
		"id":       rc.reqID,
		"model":    shadowModel,
		"trigger":  "idle_timeout",
		"internal": isInternalShadow,
	})

	log.Printf("[SHADOW] Starting shadow request to model %s for request %s (internal=%v)", shadowModel, rc.reqID, isInternalShadow)

	// Branch based on internal vs external
	if isInternalShadow {
		go h.executeInternalShadowRequest(rc, shadowCtx, shadowCancel, shadowModel, shadowModelConfig)
	} else {
		go h.executeExternalShadowRequest(rc, shadowCtx, shadowCancel, shadowModel)
	}

	// Mark goroutine as successfully launched
	goroutineLaunched = true
}

// executeInternalShadowRequest handles shadow requests for internal models (direct provider calls)
func (h *Handler) executeInternalShadowRequest(rc *requestContext, shadowCtx context.Context, shadowCancel context.CancelFunc, shadowModel string, shadowModelConfig *models.ModelConfig) {
	defer rc.shadow.Cancel()
	defer rc.shadow.Close()

	// Resolve internal config for shadow model (uses shadow model's credentials, not main request's)
	provider, apiKey, baseURL, internalModel, ok := rc.conf.ModelsConfig.ResolveInternalConfig(shadowModel)
	if !ok {
		if _, closed := sendShadowResult(rc.shadow.done, shadowResult{err: fmt.Errorf("failed to resolve internal config for shadow model %s", shadowModel)}); closed {
			return
		}
		return
	}

	// Create provider client
	providerClient, err := providers.NewProvider(provider, apiKey, baseURL)
	if err != nil {
		if _, closed := sendShadowResult(rc.shadow.done, shadowResult{err: fmt.Errorf("failed to create shadow provider: %w", err)}); closed {
			return
		}
		return
	}

	// Build request body with shadow model
	shadowBody := make(map[string]interface{})
	for k, v := range rc.requestBody {
		shadowBody[k] = v
	}
	shadowBody["model"] = internalModel // Use internal model ID for provider

	// Apply TruncateParams for shadow model
	if rc.conf.ModelsConfig != nil {
		if toStrip := rc.conf.ModelsConfig.GetTruncateParams(shadowModel); len(toStrip) > 0 {
			for _, param := range toStrip {
				delete(shadowBody, param)
			}
		}
	}

	// Make streaming request to internal provider
	req := &providers.ChatCompletionRequest{}
	if model, ok := shadowBody["model"].(string); ok {
		req.Model = model
	}
	if msgs, ok := shadowBody["messages"].([]interface{}); ok {
		req.Messages = make([]providers.ChatMessage, len(msgs))
		for i, m := range msgs {
			if mm, ok := m.(map[string]interface{}); ok {
				msg := providers.ChatMessage{}

				// Role (required)
				if role, ok := mm["role"].(string); ok {
					msg.Role = role
				}

				// Name (optional, for tool/function messages)
				if name, ok := mm["name"].(string); ok {
					msg.Name = name
				}

				// ToolCallID (optional, required for tool role messages)
				if toolCallID, ok := mm["tool_call_id"].(string); ok {
					msg.ToolCallID = toolCallID
				}

				// Content (string OR array for multimodal)
				if content, ok := mm["content"].(string); ok {
					msg.Content = content
				} else if contentArray, ok := mm["content"].([]interface{}); ok {
					// Handle multimodal content array
					parts := make([]providers.ContentPart, 0, len(contentArray))
					for _, part := range contentArray {
						if partMap, ok := part.(map[string]interface{}); ok {
							cp := providers.ContentPart{}
							if t, ok := partMap["type"].(string); ok {
								cp.Type = t
							}
							if text, ok := partMap["text"].(string); ok {
								cp.Text = text
							}
							if imgURL, ok := partMap["image_url"].(map[string]interface{}); ok {
								cp.ImageURL = &providers.ImageURL{}
								if url, ok := imgURL["url"].(string); ok {
									cp.ImageURL.URL = url
								}
								if detail, ok := imgURL["detail"].(string); ok {
									cp.ImageURL.Detail = detail
								}
							}
							parts = append(parts, cp)
						}
					}
					msg.Content = parts
				}

				// Tool calls (optional)
				if toolCalls, ok := mm["tool_calls"].([]interface{}); ok {
					msg.ToolCalls = make([]providers.ToolCall, 0, len(toolCalls))
					for _, tc := range toolCalls {
						if tcMap, ok := tc.(map[string]interface{}); ok {
							toolCall := providers.ToolCall{}
							if idx, ok := tcMap["index"].(float64); ok {
								toolCall.Index = int(idx)
							}
							if id, ok := tcMap["id"].(string); ok {
								toolCall.ID = id
							}
							if t, ok := tcMap["type"].(string); ok {
								toolCall.Type = t
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
							msg.ToolCalls = append(msg.ToolCalls, toolCall)
						}
					}
				}

				req.Messages[i] = msg
			}
		}
	}
	req.Stream = true

	// Stream response to buffer
	eventCh, err := providerClient.StreamChatCompletion(shadowCtx, req)
	if err != nil {
		if _, closed := sendShadowResult(rc.shadow.done, shadowResult{err: err}); closed {
			return
		}
		return
	}

	buffer := &bytes.Buffer{}
	completed := false

	for event := range eventCh {
		// Convert event to SSE format (matching external upstream format)
		switch event.Type {
		case "content":
			chunk := providers.ChatCompletionResponse{
				ID:      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   shadowModel, // Keep original model ID for client compatibility
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
			fmt.Fprintf(buffer, "data: %s\n", data)

		case "tool_call":
			chunk := providers.ChatCompletionResponse{
				ID:      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   shadowModel,
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
			fmt.Fprintf(buffer, "data: %s\n", data)

		case "done":
			finishReason := event.FinishReason
			if finishReason == "" {
				finishReason = "stop"
			}
			chunk := providers.ChatCompletionResponse{
				ID:      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   shadowModel,
				Choices: []providers.Choice{
					{
						Index:        0,
						Delta:        &providers.ChatMessage{},
						FinishReason: finishReason,
					},
				},
			}
			data, _ := json.Marshal(chunk)
			fmt.Fprintf(buffer, "data: %s\n", data)
			fmt.Fprintf(buffer, "data: [DONE]\n")
			completed = true

		case "error":
			if _, closed := sendShadowResult(rc.shadow.done, shadowResult{err: event.Error}); closed {
				return
			}
			return
		}
	}

	if _, closed := sendShadowResult(rc.shadow.done, shadowResult{
		buffer:    buffer,
		completed: completed,
	}); closed {
		return
	}
}

// executeExternalShadowRequest handles shadow requests via HTTP upstream
func (h *Handler) executeExternalShadowRequest(rc *requestContext, shadowCtx context.Context, shadowCancel context.CancelFunc, shadowModel string) {
	defer rc.shadow.Cancel()
	defer rc.shadow.Close()

	// Build request body with shadow model
	shadowBody := make(map[string]interface{})
	for k, v := range rc.requestBody {
		shadowBody[k] = v
	}
	shadowBody["model"] = shadowModel

	// Apply TruncateParams for shadow model (strip unsupported params)
	if rc.conf.ModelsConfig != nil {
		if toStrip := rc.conf.ModelsConfig.GetTruncateParams(shadowModel); len(toStrip) > 0 {
			for _, param := range toStrip {
				delete(shadowBody, param)
			}
		}
	}

	// Clone body bytes
	newBodyBytes, _ := json.Marshal(shadowBody)

	// Create request with shadow context
	proxyReq, err := http.NewRequestWithContext(shadowCtx, rc.method, rc.targetURL, bytes.NewBuffer(newBodyBytes))
	if err != nil {
		if _, closed := sendShadowResult(rc.shadow.done, shadowResult{err: err}); closed {
			return
		}
		return
	}

	copyHeaders(proxyReq, rc.originalHeaders)

	// Set auth if configured (EXTERNAL UPSTREAM CREDENTIALS)
	if rc.conf.UpstreamCredentialID != "" {
		proxyReq.Header.Del("Authorization")
		proxyReq.Header.Del("X-API-Key")
		proxyReq.Header.Del("x-api-key")
		proxyReq.Header.Del("api-key")
		cred := rc.conf.ModelsConfig.GetCredential(rc.conf.UpstreamCredentialID)
		if cred != nil {
			if apiKey := cred.ResolveAPIKey(); apiKey != "" {
				proxyReq.Header.Set("Authorization", "Bearer "+apiKey)
			}
		}
	}

	// Make request
	resp, err := h.client.Do(proxyReq)
	if err != nil {
		// Per Go's http.Client.Do docs: even on error, resp.Body may be non-nil and must be closed
		if resp != nil {
			resp.Body.Close()
		}
		if _, closed := sendShadowResult(rc.shadow.done, shadowResult{err: err}); closed {
			return
		}
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		if _, closed := sendShadowResult(rc.shadow.done, shadowResult{err: fmt.Errorf("shadow request failed with status %d: %s", resp.StatusCode, string(bodyBytes))}); closed {
			return
		}
		return
	}

	// Stream response to buffer
	buffer := &bytes.Buffer{}
	scanner := bufio.NewScanner(resp.Body)
	// Use smaller buffer (256KB) to reduce memory footprint
	scanner.Buffer(make([]byte, 0, 256*1024), 256*1024)

	completed := false
	for scanner.Scan() {
		line := scanner.Bytes()
		buffer.Write(line)
		buffer.Write([]byte("\n"))

		// Check for [DONE] marker
		if bytes.HasPrefix(line, []byte("data: [DONE]")) {
			completed = true
			break
		}
	}

	if err := scanner.Err(); err != nil {
		if _, closed := sendShadowResult(rc.shadow.done, shadowResult{err: err}); closed {
			return
		}
		return
	}

	if _, closed := sendShadowResult(rc.shadow.done, shadowResult{
		buffer:    buffer,
		completed: completed,
	}); closed {
		return
	}
}
