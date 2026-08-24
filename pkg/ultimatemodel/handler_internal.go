package ultimatemodel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy/token"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy/translator"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/toolcall"
)

// newProviderClient is a package-level variable that creates a provider.
// Tests can override it to inject a mock provider; production callers use
// providers.NewProvider.
var newProviderClient = providers.NewProvider

// executeInternal handles requests to internal providers (bypassing upstream)
// This is a RAW PROXY - no retry, no fallback, no buffering, no loop detection
//
// interleaved is the X-Proxy-Interleaved-Thinking flag re-parsed by
// Execute (B3). When true AND the credential provider is MiniMax, the
// caller sets the typed ReasoningSplit *bool = ptr(true) on the hydrated
// *ChatCompletionRequest after convertRequest returns, and convertRequest
// populates ChatMessage.ReasoningDetails from the input map. The gate
// (provider + flag) belongs to the caller, not here — this function
// remains pure (W6).
func (h *Handler) executeInternal(
	ctx context.Context,
	w http.ResponseWriter,
	requestBody map[string]interface{},
	requestBodyBytes []byte,
	modelCfg *models.ModelConfig,
	isStream bool,
	interleaved bool,
) (*ExecuteResult, error) {
	// Resolve internal config (including credential lookup)
	provider, apiKey, baseURL, internalModel, ok := h.modelsMgr.ResolveInternalConfig(modelCfg.ID)
	if !ok {
		return nil, fmt.Errorf("failed to resolve internal config for model %s", modelCfg.ID)
	}

	// Create provider
	providerClient, err := newProviderClient(provider, apiKey, baseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to create provider: %w", err)
	}

	// Convert request
	req, err := h.convertRequest(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to convert request: %w", err)
	}

	// P1-8(d) / W5: twin B — typed-field setter AND
	// message-level reasoning_content→reasoning_details
	// translation. When the gate fires (flag AND credential is
	// MiniMax), the typed openai.go marshaler emits the wire
	// field AND each per-message `reasoning_content` is
	// translated to a `reasoning_details` array. The
	// reasoning_content→reasoning_details translation is the
	// W5 mirror of the race-internal twin A behavior — without
	// it, the typed path carried reasoning_content on the
	// wire (the SDK does not auto-translate), which leaked
	// OpenAI semantics to MiniMax and produced the
	// reasoning_content silent drop reported in
	// c3a4b35/83814b0. The translation is done on the typed
	// struct directly (not via translator.TranslateRequestBytes)
	// because the input is already a typed request after
	// convertRequest; using the bytes wrapper would require a
	// parse→mutate→re-marshal round-trip we already paid
	// once. The result is the same: each
	// ChatMessage.ReasoningContent that is non-empty becomes
	// a one-entry ChatMessage.ReasoningDetails array, and
	// ReasoningContent is cleared (strip-and-replace).
	// Provider compare is case-insensitive per
	// handler_anthropic.go:297 precedent. The provider string
	// comes from ResolveInternalConfig (already a normalized
	// canonical value from the credential).
	if interleaved && strings.ToLower(provider) == strings.ToLower(string(providers.ProviderMiniMax)) {
		t := true
		req.ReasoningSplit = &t
		// W5 — mirror the race-internal twin A
		// translation on the typed struct.
		//
		// The id counter is MONOTONIC over reasoning-carrying
		// messages only (starting at 1), matching the
		// translator's canonical TranslateMessagesReasoning
		// (pkg/proxy/translator/minimax.go): messages with
		// empty reasoning_content are skipped and consume NO
		// counter slot. Using the message index here (the
		// pre-fix scheme) made [user, assistant w/ rc, user]
		// emit reasoning-text-2 on this path while every other
		// path emitted reasoning-text-1 — the S3 cross-path
		// divergence caught by test/e2e_minimax_reasoning.
		n := 0
		for i := range req.Messages {
			msg := &req.Messages[i]
			if msg.ReasoningContent == "" {
				continue
			}
			n++
			// Strip-and-replace: build the new
			// details entry from the existing content,
			// then clear the content string. The
			// wire-literal identifiers
			// (translator.ReasoningTextType /
			// .ReasoningTextFormat /
			// .ReasoningTextIDPrefix) are imported
			// here for the constant-not-literal
			// invariant — single source of truth
			// for the MiniMax wire vocabulary
			// lives in pkg/proxy/translator
			// (R3 carve-out — providers/ultimatemodel
			// may import these constants; see
			// ExtractEntryText for the established
			// helper-only precedent).
			msg.ReasoningDetails = []providers.ReasoningDetailEntry{{
				Type:   translator.ReasoningTextType,
				ID:     fmt.Sprintf("%s%d", translator.ReasoningTextIDPrefix, n),
				Format: translator.ReasoningTextFormat,
				Index:  0,
				Text:   msg.ReasoningContent,
			}}
			msg.ReasoningContent = ""
		}
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
) (*ExecuteResult, error) {
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

	// Passive capture: pull content + reasoning_content + tool_calls
	// from the typed response. The wire bytes have ALREADY been
	// written above (json.NewEncoder(w).Encode(resp)) — this is pure
	// read-only access to the in-memory response struct, so the
	// client output is unchanged.
	//
	// ChatMessage.Content is typed as interface{} in the provider
	// because it can be a string OR []ContentPart (multimodal);
	// the shared coerceContentString helper (S1) yields "" for the
	// non-string forms, which is safe here because the upstream
	// non-stream OpenAI response is always string-typed for text
	// completions (the []ContentPart form is request-only).
	//
	// W7: tool calls come back fully assembled on the typed
	// non-stream response (no streaming-accumulation needed). The
	// providers.ToolCall carries an extra "index" field compared
	// to store.ToolCall; we copy the persistence-relevant fields
	// (id, type, function name + arguments) explicitly so the
	// store.Message.ToolCalls shape is exactly what the Web UI
	// reads (pkg/ui/frontend/src/components/RequestDetail.tsx).
	var capturedContent, capturedThinking string
	var capturedToolCalls []store.ToolCall
	if len(resp.Choices) > 0 && resp.Choices[0].Message != nil {
		capturedContent = coerceContentString(resp.Choices[0].Message.Content)
		capturedThinking = resp.Choices[0].Message.ReasoningContent
		if len(resp.Choices[0].Message.ToolCalls) > 0 {
			capturedToolCalls = make([]store.ToolCall, len(resp.Choices[0].Message.ToolCalls))
			for i, tc := range resp.Choices[0].Message.ToolCalls {
				capturedToolCalls[i] = store.ToolCall{
					ID:   tc.ID,
					Type: tc.Type,
					Function: store.Function{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				}
			}
		}
	}

	return &ExecuteResult{
		Usage:     usage,
		Content:   capturedContent,
		Thinking:  capturedThinking,
		ToolCalls: capturedToolCalls,
	}, nil
}

// handleInternalStream handles streaming requests for internal providers
func (h *Handler) handleInternalStream(
	ctx context.Context,
	provider providers.Provider,
	req *providers.ChatCompletionRequest,
	w http.ResponseWriter,
	internalModel string,
	requestBodyBytes []byte,
) (*ExecuteResult, error) {
	eventCh, err := provider.StreamChatCompletion(ctx, req)
	if err != nil {
		return nil, err
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
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

	// Capture-side only: accumulate Content + Thinking +
	// ToolCalls from typed provider events as we write them out.
	// Builders stay local; nothing here is sent to the client. The
	// string-builder pattern avoids the += memory trap across
	// many stream chunks.
	var capturedContent strings.Builder
	var capturedThinking strings.Builder

	// W7: tool calls arrive incrementally across "tool_call"
	// events as partial providers.ToolCall deltas (index, ID,
	// type, function name appear once; arguments fragments
	// concatenate). Accumulate by index using the same
	// shape-pattern as pkg/proxy/handler_helpers.go's
	// extractStreamChunkContent — by-index Builder slice for
	// arguments, parallel store.ToolCall slice for fixed fields
	// (ID/Type/Name). finalise collapses arg builders into
	// Function.Arguments at end-of-stream.
	var capturedToolCalls []store.ToolCall
	var capturedToolCallArgBuilders []*strings.Builder

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
			// Capture-side only: accumulate content delta. The
			// typed event.Content field is the same string the
			// proxy just wrote into the chunk above, so
			// capturing here is exactly equivalent to parsing
			// the wire bytes — without an extra JSON pass.
			if event.Content != "" {
				capturedContent.WriteString(event.Content)
			}

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

					// W7: capture-side only. Mirror the same
					// per-index accumulation pattern that
					// pkg/proxy/handler_helpers.go's
					// extractStreamChunkContent uses so the
					// outer persistence layer sees the
					// fully-assembled ToolCalls. The typed
					// event.ToolCalls slice is the SAME data
					// the proxy just wrote into `toolCalls`
					// above (delta fields), so capturing here
					// is observationally equivalent to parsing
					// the wire bytes — without an extra JSON
					// pass. The `index` here is the
					// proxy-internal reassigned index (seenToolCallIDs)
					// that the wire observer will see.
					for len(capturedToolCalls) <= index {
						capturedToolCalls = append(capturedToolCalls, store.ToolCall{})
						b := &strings.Builder{}
						b.Grow(1024)
						capturedToolCallArgBuilders = append(capturedToolCallArgBuilders, b)
					}
					ctc := &capturedToolCalls[index]
					if tc.ID != "" {
						ctc.ID = tc.ID
					}
					if tc.Type != "" {
						ctc.Type = tc.Type
					}
					if tc.Function.Name != "" {
						ctc.Function.Name = tc.Function.Name
					}
					if tc.Function.Arguments != "" {
						capturedToolCallArgBuilders[index].WriteString(tc.Function.Arguments)
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
			// Capture-side only: accumulate thinking delta. The
			// typed event.ReasoningContent field is the same
			// string the proxy just wrote into the chunk above
			// (mirroring the d7300cb internal-path reasoning
			// emission).
			if event.ReasoningContent != "" {
				capturedThinking.WriteString(event.ReasoningContent)
			}

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

			return &ExecuteResult{
				Usage:     extractedUsage,
				Content:   capturedContent.String(),
				Thinking:  capturedThinking.String(),
				ToolCalls: finalizeAccumulatedToolCalls(capturedToolCalls, capturedToolCallArgBuilders),
			}, nil

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
				// Handle reasoning_content for DeepSeek R1-style thinking models
				if reasoningContent, ok := msg["reasoning_content"].(string); ok {
					chatMsg.ReasoningContent = reasoningContent
				}
				// D1 carry: hydrate MiniMax per-message reasoning_details into
				// ChatMessage.ReasoningDetails. The field exists so the typed
				// openai.go marshaler emits reasoning_details on the wire
				// (P1-8 d). R3 — pkg/providers does not import pkg/proxy/translator;
				// the translator's ReasoningDetail is kept separate from this
				// local ReasoningDetailEntry shape. convertRequest delegates to
				// providers.HydrateReasoningDetails (M4 dedup — twin with the
				// race-internal hydration in pkg/proxy/race_executor.go).
				chatMsg.ReasoningDetails = providers.HydrateReasoningDetails(msg)
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
