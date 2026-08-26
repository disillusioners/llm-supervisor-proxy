package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/bufferstore"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/credentiallb"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/logger"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy/translator"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/toolcall"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/toolrepair"
)

// InternalHandler handles requests to internal providers (bypassing upstream)
type InternalHandler struct {
	config        *models.ModelConfig
	resolver      models.ModelsConfigInterface   // Resolver for credentials
	bufferStore   *bufferstore.BufferStore       // Optional: for saving debug info
	requestID     string                         // Optional: request ID for buffer naming
	repairer      *toolrepair.Repairer           // Optional: for repairing tool call JSON
	eventCallback toolrepair.RepairEventCallback // Optional: callback for repair events
	// thinkingSink is an OPTIONAL, capture-only side channel for case
	// "thinking" reasoning text. INVARIANT: thinking bytes must NEVER be
	// written to the ResponseWriter `w` — the recorder body is consumed
	// downstream by translator.TranslateBufferedStream, which would convert
	// any reasoning_content delta written here into Anthropic thinking
	// blocks leaked onto the client wire. Base fea5874 DROPPED thinking
	// events entirely; the sink exists only so persistence
	// (store.Message.Thinking) can still observe them. When nil, thinking
	// events are silently not captured (the documented base behaviour).
	thinkingSink *strings.Builder

	// Tool call buffer configuration
	toolCallBufferMaxSize  int64              // Max size for tool call buffer
	toolCallBufferDisabled bool               // Disable tool call buffering
	toolRepairConfig       *toolrepair.Config // Tool repair config for buffer

	// credEngine + eventBus (Phase 3 / Task 22 — Round 3 rate-limit
	// credential failover at the InternalHandler seam). Both optional
	// (nil-safe): unset on legacy/test constructions — the hook is
	// skipped entirely. Installed via SetCredentialFailover by the
	// /v1/messages caller that owns the engine (constructor-only
	// injection per W-3 holds — NewInternalHandler's signature is
	// unchanged per the Files table row).
	credEngine *credentiallb.Engine
	eventBus   *events.Bus
}

// SetCredentialFailover (Phase 3 / Task 22) installs the engine + bus
// used by HandleRequest's rate-limit failover hook. Optional; when not
// called, HandleRequest behaves exactly as before (single attempt).
func (h *InternalHandler) SetCredentialFailover(engine *credentiallb.Engine, bus *events.Bus) {
	h.credEngine = engine
	h.eventBus = bus
}

// NewInternalHandler creates a new internal handler for a model
// The resolver is used to resolve credentials from the model's credential_id
func NewInternalHandler(config *models.ModelConfig, resolver models.ModelsConfigInterface) *InternalHandler {
	return &InternalHandler{config: config, resolver: resolver}
}

// SetDebugContext sets the buffer store and request ID for debug file saving
func (h *InternalHandler) SetDebugContext(bufferStore *bufferstore.BufferStore, requestID string) {
	h.bufferStore = bufferStore
	h.requestID = requestID
}

// SetRepairer sets the tool call repairer and optional event callback
func (h *InternalHandler) SetRepairer(repairer *toolrepair.Repairer, callback toolrepair.RepairEventCallback) {
	h.repairer = repairer
	h.eventCallback = callback
}

// SetToolCallBufferConfig sets the tool call buffer configuration
func (h *InternalHandler) SetToolCallBufferConfig(maxSize int64, disabled bool, repairConfig *toolrepair.Config) {
	h.toolCallBufferMaxSize = maxSize
	h.toolCallBufferDisabled = disabled
	h.toolRepairConfig = repairConfig
}

// SetThinkingSink installs a side-channel sink for reasoning/thinking text
// captured during streaming. When set, case "thinking" events are written to
// the sink ONLY — no SSE chunk is emitted on the wire (w). Callers use the
// sink to persist store.Message.Thinking without leaking thinking deltas
// into the OpenAI stream that gets translated to Anthropic.
//
// INVARIANT: the sink is capture-only. Thinking bytes must NEVER be written
// to the ResponseWriter `w`, because the recorder body feeds
// translator.TranslateBufferedStream and any thinking delta written there
// leaks to the client wire as Anthropic thinking blocks. Base fea5874
// DROPPED thinking events entirely; a nil sink preserves that documented
// base behaviour (thinking is silently not captured).
//
// Double-set is a programming error: a second call without an intervening
// ResetThinkingSink returns an error and leaves the existing sink installed.
// This guards against two callers racing to capture the same stream into
// different builders (which would split the thinking text).
func (h *InternalHandler) SetThinkingSink(sink *strings.Builder) error {
	if h.thinkingSink != nil {
		return fmt.Errorf("SetThinkingSink: sink already set; call ResetThinkingSink before installing a new one (double-set is a programming error)")
	}
	h.thinkingSink = sink
	return nil
}

// ResetThinkingSink removes the installed sink, returning thinking capture
// to the documented base behaviour (silently not captured). It is the only
// sanctioned way to make a subsequent SetThinkingSink succeed.
func (h *InternalHandler) ResetThinkingSink() {
	h.thinkingSink = nil
}

// CanHandleInternal checks if a model should use internal upstream
func CanHandleInternal(modelConfig *models.ModelConfig) bool {
	return modelConfig != nil && modelConfig.Internal
}

// HandleRequest handles a request using internal provider
//
// conversationKey (Phase 3 / Task 16) is the per-request affinity key
// threaded from the Anthropic-path post-auth wiring site
// (handler_anthropic.go HandleAnthropicMessages, after the resolved
// model + auth-equivalent site). Empty key is graceful (W-2 + C2 — no
// binding stored, fresh pick per request).
//
// Phase 3 / Task 22 (Round 3 rate-limit credential failover at the
// InternalHandler seam): when the engine + bus are installed
// (SetCredentialFailover) and the initial provider call fails with a
// rate-limited *ProviderError, the hook calls engine.ExcludeAndReselect
// and retries ONCE with the reselected credential. Pre-first-byte
// guard: the initial-call *ProviderError assertion (initial-call
// errors return before any write to `w`; the /v1/messages client-side
// first-byte marker is arc.headersSent at handler_anthropic.go:648-654,
// which cannot have fired while the recorder is still empty).
func (h *InternalHandler) HandleRequest(ctx context.Context, requestBody map[string]interface{}, w http.ResponseWriter, isStream bool, conversationKey string) error {
	// Resolve internal config via the affinity seam (Phase 3 / Task 16,
	// #3 struct form). The Anthropic path does NOT publish
	// model_credential_selected itself (single source of truth in
	// pkg/proxy/race_executor.go:executeRequest); discarding NewlyBound
	// here is intentional.
	resolved, ok := h.resolver.ResolveInternalConfigWithAffinity(h.config.ID, conversationKey)
	if !ok {
		return fmt.Errorf("failed to resolve internal config for model %s", h.config.ID)
	}

	err := h.executeWithResolved(ctx, requestBody, w, isStream, resolved)
	if err == nil || h.credEngine == nil {
		return err
	}

	// ── Task 22 hook guards ──
	// (1) initial-call *ProviderError (pre-first-byte guard: ChatCompletion/
	//     StreamChatCompletion errors return before any write to `w`);
	// (2) rate-limit classification (Task 17 vocabulary);
	// (3) multi-credential model (single-cred ⇒ ReselectNone anyway);
	// (4) budget: this hook retries at most ONCE — structurally within
	//     the R3-5 budget (≤ len(credentials)−1) given (3).
	var providerErr *providers.ProviderError
	if !errors.As(err, &providerErr) {
		return err
	}
	if !providers.IsRateLimitError(providerErr) {
		return err
	}
	if len(h.config.Credentials) <= 1 {
		return err
	}

	reselected, mode := h.credEngine.ExcludeAndReselect(
		h.config.ID,
		conversationKey,
		resolved.CredentialID,
		providerErr.RetryAfter,
	)

	switch mode {
	case credentiallb.ReselectNone:
		// C2 narrowing: no credential available. Propagate.
		log.Printf("[LB-FAILOVER] /v1/messages internal: model=%s no reselectable credential (mode=%v); propagating failure", h.config.ID, mode)
		return err
	case credentiallb.ReselectSoonestExpiry:
		// 0-of-N healthy: single attempt with the soonest-expiring
		// credential, then propagate (F.4 single-attempt-then-fall-through).
		log.Printf("[WARN] [LB-FAILOVER] /v1/messages internal: model=%s all credentials cooling; single attempt with soonest-expiring cred=%s", h.config.ID, reselected)
	default:
		// ReselectHealthy (incl. B2 no-op + empty-key fresh pick per C2).
		log.Printf("[LB-FAILOVER] /v1/messages internal: model=%s rate-limited on cred=%s; failing over to cred=%s", h.config.ID, resolved.CredentialID, reselected)
	}

	if reselected == "" || reselected == resolved.CredentialID {
		// Nothing new to try — propagate (idempotent no-op guard).
		return err
	}

	// Resolve the SPECIFIC reselected credential — NOT a fresh
	// GetOrSelect (an empty-key fresh pick would re-roll and could
	// re-select the just-cooled credential). Same field derivation as
	// store.resolveWithCredential.
	newCred := h.resolver.GetCredential(reselected)
	if newCred == nil {
		return err
	}
	newBaseURL := h.config.InternalBaseURL
	if newBaseURL == "" {
		newBaseURL = newCred.BaseURL
	}
	newInternalModel := h.config.InternalModel
	if peakModel := h.config.ResolvePeakHourModel(time.Now()); peakModel != "" {
		newInternalModel = peakModel
	}
	retryResolved := models.ResolvedCredential{
		Provider:      newCred.Provider,
		APIKey:        newCred.APIKey,
		BaseURL:       newBaseURL,
		InternalModel: newInternalModel,
		CredentialID:  reselected,
		NewlyBound:    false, // reselection ≠ first binding (W-1) — never publishes model_credential_selected
	}

	// Task 20 observability: model_credential_failover event
	// ({model_id, from_credential_id, to_credential_id, reason,
	// retry_after_ms, cooldown_ms, attempt_index}).
	if h.eventBus != nil {
		cooldown := credentiallb.DefaultCooldown
		if providerErr.RetryAfter > 0 {
			cooldown = providerErr.RetryAfter
		}
		data := map[string]interface{}{
			"model_id":           h.config.ID,
			"from_credential_id": resolved.CredentialID,
			"to_credential_id":   reselected,
			"reason":             "rate_limit",
			"retry_after_ms":     providerErr.RetryAfter.Milliseconds(),
			"cooldown_ms":        cooldown.Milliseconds(),
			"attempt_index":      1,
		}
		h.eventBus.Publish(events.Event{
			Type:      credentiallb.EventCredentialFailover,
			Timestamp: time.Now().Unix(),
			Data:      data,
		})
	}

	// Single retry through the SAME execution path.
	return h.executeWithResolved(ctx, requestBody, w, isStream, retryResolved)
}

// executeWithResolved (Phase 3 / Task 22 refactor) runs the internal
// request against an ALREADY-RESOLVED credential. Split out of
// HandleRequest so the failover hook can retry with the reselected
// credential through the identical provider-construction + dispatch
// path (no duplicated logic).
func (h *InternalHandler) executeWithResolved(ctx context.Context, requestBody map[string]interface{}, w http.ResponseWriter, isStream bool, resolved models.ResolvedCredential) error {
	provider := resolved.Provider
	apiKey := resolved.APIKey
	baseURL := resolved.BaseURL
	internalModel := resolved.InternalModel

	// Create provider
	providerClient, err := providers.NewProvider(provider, apiKey, baseURL)
	if err != nil {
		return fmt.Errorf("failed to create provider: %w", err)
	}

	// Set debug context on provider if available (for OpenAIProvider)
	if h.bufferStore != nil && h.requestID != "" {
		if openaiProvider, ok := providerClient.(*providers.OpenAIProvider); ok {
			openaiProvider.SetDebugContext(h.bufferStore, h.requestID)
		}
	}

	// Set repairer on provider if available (for OpenAIProvider)
	if h.repairer != nil {
		if openaiProvider, ok := providerClient.(*providers.OpenAIProvider); ok {
			openaiProvider.SetRepairer(h.repairer, h.eventCallback)
		}
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
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("streaming not supported")
	}

	// Create tool call buffer with integrated repair
	// This replaces the separate accumulator + post-stream repair pattern
	// Repair happens during streaming when tool calls are emitted
	var toolCallBuffer *toolcall.ToolCallBuffer
	if !h.toolCallBufferDisabled && h.toolRepairConfig != nil && h.toolRepairConfig.Enabled {
		toolCallBuffer = toolcall.NewToolCallBufferWithRepair(
			h.toolCallBufferMaxSize,
			internalModel,
			h.requestID,
			h.toolRepairConfig,
		)
	} else if !h.toolCallBufferDisabled {
		// Buffer without repair (repair disabled)
		toolCallBuffer = toolcall.NewToolCallBuffer(
			h.toolCallBufferMaxSize,
			internalModel,
			h.requestID,
		)
	}

	for event := range eventCh {
		logger.Debugf("[DEBUG INTERNAL] Received event: type=%s, content=%.100s", event.Type, event.Content)
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
			logger.Debugf("[DEBUG INTERNAL] Writing chunk: %s", string(data))
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()

		case "thinking":
			// Capture reasoning/thinking text via side channel ONLY.
			// The recorder (`w`) is consumed downstream by
			// translator.TranslateBufferedStream, which would convert any
			// reasoning_content delta we wrote here into Anthropic thinking
			// blocks the client would receive. That violates the byte-identity
			// contract with the pre-fix behaviour, so we deliberately stay
			// silent on the wire and accumulate event.ReasoningContent into
			// the optional thinkingSink for persistence instead.
			//
			// NIL-SINK INVARIANT (W1): with no sink installed this arm is a
			// no-op — thinking is silently not captured, exactly the
			// documented base behaviour of fea5874 (which dropped thinking
			// events entirely). No panic, no wire write, ever.
			if h.thinkingSink != nil {
				h.thinkingSink.WriteString(event.ReasoningContent)
			}
			logger.Debugf("[DEBUG INTERNAL] Captured thinking (side channel): %s", event.ReasoningContent)

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
			line := fmt.Sprintf("data: %s\n\n", data)
			logger.Debugf("[DEBUG INTERNAL] Writing tool_call chunk: %s", string(data))

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
			}
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
					log.Printf("[TOOL-BUFFER] InternalHandler: Repair stats: attempted=%d, success=%d, failed=%d",
						stats.Attempted, stats.Successful, stats.Failed)
				}
			}

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
						var sb strings.Builder
						sb.Grow(len(c) * 64) // Pre-allocate reasonable capacity
						for _, part := range c {
							if partMap, ok := part.(map[string]interface{}); ok {
								if text, ok := partMap["text"].(string); ok {
									sb.WriteString(text)
								}
							}
						}
						msg.Content = sb.String()
					default:
						// Unsupported content type, skip or handle as needed
						logger.Debugf("Unsupported content type: %T", content)
						msg.Content = ""
					}

				}
				if name, ok := msgMap["name"].(string); ok {
					msg.Name = name
				}
				// Handle tool_call_id for tool role messages
				if toolCallID, ok := msgMap["tool_call_id"].(string); ok {
					msg.ToolCallID = toolCallID
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

	// Handle tool_choice - translate from OpenAI to Anthropic format
	if toolChoice, ok := body["tool_choice"]; ok {
		req.ToolChoice = translator.TranslateOpenAIToolChoiceToAnthropic(toolChoice)
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
