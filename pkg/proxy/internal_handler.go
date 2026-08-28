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
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
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
	// "thinking" reasoning text. INVARIANT (Phase 3 / task 3.9 dual-
	// mode wording): thinking bytes must NEVER be written to the
	// ResponseWriter `w` as raw OpenAI chunks. Buffered mode: the
	// recorder body is consumed downstream by
	// translator.TranslateBufferedStream, which would convert any
	// reasoning_content delta written there into Anthropic thinking
	// blocks leaked onto the client wire — so the recorder path
	// keeps the sink capture-only contract. Live mode: the recorder
	// is gone (handleStream drives the typed-event translator
	// instead); thinking events are captured into the sink AND
	// ADDITIONALLY emitted as Anthropic thinking_delta blocks via
	// the translator on the wire — that is the deliberate,
	// documented wire-shape change for the internal non-Anthropic
	// variant in live mode (real-streaming-default plan, Phase 3 /
	// Q2 mechanics). When nil, thinking events are silently not
	// captured (the documented base behaviour).
	thinkingSink *strings.Builder

	// liveTranslator is the OPTIONAL per-request
	// translator.IncrementalStreamTranslator installed ONLY in live
	// mode (bufferMode==false at the call site). When set, the case
	// "content"/"thinking"/"tool_call" arms in handleStream drive
	// ProcessEvent from the typed event instead of writing raw
	// OpenAI SSE chunks to w — the translator's emitted Anthropic
	// events REPLACE the raw-OpenAI write (M5: REPLACE, never
	// "also"). The recorder-based buffered mode never installs
	// this. Installed via SetLiveTranslator mirroring SetThinkingSink
	// (nil-safe; nil means legacy buffered behavior).
	liveTranslator *translator.IncrementalStreamTranslator

	// liveArc is the per-request anthropicRequestContext installed
	// by SetLiveArc. Used by emitLivePreamble to set headersSent
	// lazily (preserving pre-first-byte fallback semantics — see
	// the emitLivePreamble comment for the full rationale). nil in
	// buffered mode (the buffered path owns its own headersSent
	// gate at handleAnthropicInternalStreamResponse :735-749).
	liveArc *anthropicRequestContext

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

// SetLiveTranslator (Phase 3 / task 3.4) installs the per-request
// IncrementalStreamTranslator used by the live-mode Anthropic path
// (doAnthropicInternalRequest). When set, handleStream's content /
// thinking / tool_call arms drive ProcessEvent instead of writing raw
// OpenAI chunks to w. nil-safe: a nil install returns nil and leaves
// the previous state alone (the documented buffered-mode behavior
// when no translator was ever installed).
//
// Double-set is a programming error: a second call without an
// intervening ResetLiveTranslator returns an error and leaves the
// existing translator installed.
func (h *InternalHandler) SetLiveTranslator(t *translator.IncrementalStreamTranslator) error {
	if h.liveTranslator != nil && t != nil {
		return fmt.Errorf("SetLiveTranslator: translator already set; call ResetLiveTranslator before installing a new one")
	}
	h.liveTranslator = t
	return nil
}

// ResetLiveTranslator removes the installed translator. After
// ResetLiveTranslator, the internal handler returns to the buffered-
// mode default (raw OpenAI chunks written to w).
func (h *InternalHandler) ResetLiveTranslator() {
	h.liveTranslator = nil
	h.liveArc = nil
}

// SetLiveArc installs the per-request anthropicRequestContext used
// by emitLivePreamble to set headersSent lazily. Required in live
// mode; the InternalHandler cannot read arc directly because it
// lives in the proxy package.
func (h *InternalHandler) SetLiveArc(arc *anthropicRequestContext) {
	h.liveArc = arc
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
//
// Phase 3 S3 fix (real-streaming-default merge-gate blocker): in LIVE
// mode (liveArc != nil), the live branch of doAnthropicInternalRequest
// drives the real ResponseWriter here — there is NO recorder round-trip,
// so the buffered path's extractOpenAIResponseContentFromJSON-based
// capture never runs. We capture content/reasoning_content/tool_calls
// directly from the typed *providers.ChatCompletionResponse here,
// matching the buffered path's extraction shape (same fields, same
// source shape — OpenAI ChatCompletionResponse.Choices[0].Message).
// Without this, every internal-Anthropic non-stream request in live
// mode (the new default) persisted empty Thinking + Content + ToolCalls
// while the wire response was correct (see
// .agents/tester/LESSONS/2026-08-28-rsd-s3-nonstream-persistence-bug.md).
//
// Phase 3 E-fix (M2-E non-stream wire parity): in LIVE mode the wire
// body must also be translated to Anthropic-shape — same as the
// buffered path emits (handler_anthropic.go:842 calls
// translator.TranslateNonStreamResponse on the recorder body and
// writes the result). Live mode historically wrote OpenAI-shape JSON
// (json.NewEncoder(w).Encode(resp)), breaking locked decision #3
// "stream=false unaffected in both modes" — Anthropic clients
// receiving stream:false got OpenAI bytes. The fix routes the live
// non-stream wire through the SAME translator the buffered path uses,
// via a small marshal-then-translate round-trip on the typed resp
// (the typed resp marshals to the SAME JSON shape the buffered path's
// recorder captures). Capture above remains intact; this only changes
// the wire bytes, not the persistence fields. Buffered mode is
// untouched (liveArc == nil → recorder path → its own translation
// stays authoritative).
//
// Call-site survey: InternalHandler.HandleRequest (the only caller of
// handleNonStream) is reachable from exactly two sites — both inside
// doAnthropicInternalRequest in handler_anthropic.go (the Anthropic-
// internal path). The OpenAI race path uses a DIFFERENT function
// (`handleNonStreamingResponse` in race_executor.go:1116) and is not
// affected. So liveArc != nil is a precise live-mode signal here.
func (h *InternalHandler) handleNonStream(ctx context.Context, provider providers.Provider, req *providers.ChatCompletionRequest, w http.ResponseWriter, internalModel string) error {
	resp, err := provider.ChatCompletion(ctx, req)
	if err != nil {
		return err
	}

	// Live-mode capture (parity with buffered extractOpenAIResponseContentFromJSON
	// at handler_anthropic.go:1440 — same fields, same source shape).
	// Runs BEFORE wire translation so the arc mirror is independent
	// of the translation outcome (S3 fix 64da4ae contract).
	if h.liveArc != nil && len(resp.Choices) > 0 {
		msg := resp.Choices[0].Message
		if msg != nil {
			if s, ok := msg.Content.(string); ok {
				h.liveArc.accumulatedResponse.WriteString(s)
			}
			h.liveArc.accumulatedThinking.WriteString(msg.ReasoningContent)
			for _, tc := range msg.ToolCalls {
				h.liveArc.accumulatedToolCalls = append(h.liveArc.accumulatedToolCalls, store.ToolCall{
					ID:   tc.ID,
					Type: tc.Type,
					Function: store.Function{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				})
			}
		}
	}

	// Wire-shape routing. Live mode → Anthropic-shape (mirrors the
	// buffered path's handleAnthropicInternalNonStreamResponse at
	// handler_anthropic.go:842). Buffered mode → keep OpenAI-shape
	// bytes (the recorder path needs them so its own
	// extractOpenAIResponseContentFromJSON + TranslateNonStreamResponse
	// pass can run on the recorder body).
	if h.liveArc != nil {
		openaiBytes, terr := json.Marshal(resp)
		if terr != nil {
			log.Printf("[DEBUG INTERNAL] live non-stream marshal failed: %v", terr)
			return terr
		}
		anthropicResp, terr := translator.TranslateNonStreamResponse(openaiBytes, h.liveArc.originalModel)
		if terr != nil {
			log.Printf("[DEBUG INTERNAL] live non-stream translate failed: %v", terr)
			return terr
		}
		w.Header().Set("Content-Type", "application/json")
		_, werr := w.Write(anthropicResp)
		return werr
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
			if h.liveTranslator != nil {
				// Live mode: drive the translator via the typed-event
				// entry. The emitted Anthropic events REPLACE the raw
				// OpenAI-chunk write to w (M5: REPLACE, never "also")
				// — no double-write, no OpenAI-wire bytes on the
				// Anthropic wire. Per-event accumulation into the
				// arc's accumulatedResponse is the caller's job (the
				// translator's StreamState is the source of truth,
				// mirrored in doAnthropicInternalRequest's success
				// path).
				toolCallsForEv := convertProvidersToolCallsToMapList(event.ToolCalls)
				events, terr := h.liveTranslator.ProcessEvent(event.Content, "", "", toolCallsForEv)
				if terr != nil {
					logger.Debugf("[DEBUG INTERNAL] live translator ProcessEvent (content): %v", terr)
					break
				}
				if len(events) > 0 {
					h.emitLivePreamble(w, h.liveArc)
				}
				for _, ev := range events {
					if _, werr := w.Write([]byte(ev)); werr != nil {
						return werr
					}
					if f, ok := w.(http.Flusher); ok {
						f.Flush()
					}
				}
				break
			}
			// Buffered mode (legacy): write raw OpenAI SSE chunk to w.
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
			// Capture reasoning/thinking text via side channel ONLY
			// in buffered mode. In live mode the side channel ALSO
			// captures (invariant dual-mode wording — see the
			// thinkingSink field comment), and the translator
			// ADDITIONALLY emits an Anthropic thinking_delta event on
			// the wire. The recorder-based buffered path suppresses
			// the wire write to keep the recorder clean (which is
			// what TranslateBufferedStream downstream expects).
			if h.thinkingSink != nil {
				h.thinkingSink.WriteString(event.ReasoningContent)
			}
			logger.Debugf("[DEBUG INTERNAL] Captured thinking (side channel): %s", event.ReasoningContent)

			if h.liveTranslator != nil {
				// Live mode wire emission: drive ProcessEvent to get
				// the Anthropic thinking_delta event(s), then write
				// to w (replaces the legacy "do not write" rule —
				// that rule was recorder-specific).
				events, terr := h.liveTranslator.ProcessEvent("", event.ReasoningContent, "", nil)
				if terr != nil {
					logger.Debugf("[DEBUG INTERNAL] live translator ProcessEvent (thinking): %v", terr)
					break
				}
				if len(events) > 0 {
					h.emitLivePreamble(w, h.liveArc)
				}
				for _, ev := range events {
					if _, werr := w.Write([]byte(ev)); werr != nil {
						return werr
					}
					if f, ok := w.(http.Flusher); ok {
						f.Flush()
					}
				}
			}

		case "tool_call":
			if h.liveTranslator != nil {
				// LIVE MODE with per-call hold + repair active (Phase 3
				// follow-up ruling on plan decision #5 — tool-call
				// buffering must be preserved for streaming clients,
				// mirroring the Phase 2 OpenAI race path). The
				// pipeline SHAPE here mirrors the buffered arm below
				// (~:552-585):
				//   1. Build a raw OpenAI SSE "data: ..." line from
				//      the typed event's ToolCalls (same construction
				//      as the buffered arm — see ~:551-569).
				//   2. Feed it through toolCallBuffer.ProcessChunk —
				//      per-call hold: the buffer accumulates fragments
				//      per index and emits an OpenAI-shape chunk ONLY
				//      when that tool call's arguments form valid JSON
				//      (buffer.go:220-223). If JSON is malformed, the
				//      toolrepair chain (pkg/toolrepair) repairs it
				//      before emission (buffer.go:268-286).
				//   3. For each complete emission, parse back to a
				//      providers.ToolCall list and drive the
				//      liveTranslator via ProcessEvent. The
				//      translator's emitToolDelta opens the
				//      content_block_start on first id+name arrival
				//      and emits input_json_delta(complete args) — no
				//      unbounded hold, only per-call latency.
				//   4. Write the emitted Anthropic events to w.
				// Non-tool arms (case "content" / "thinking") stay
				// UNTOUCHED — immediate pass-through, zero added
				// latency for text/thinking deltas. Only tool-call
				// fragments are subject to the per-call hold.
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
				logger.Debugf("[DEBUG INTERNAL] Live tool_call chunk: %s", string(data))

				// Per-call hold + repair (same shape as the buffered
				// arm's toolCallBuffer call below). The buffer may
				// return zero chunks when no tool call is yet complete
				// (in-flight fragments held until JSON parses) — that
				// IS the per-call hold guarantee.
				var chunksToEmit [][]byte
				if toolCallBuffer != nil {
					chunksToEmit = toolCallBuffer.ProcessChunk([]byte(line))
				} else {
					// No buffer configured (disabled): preserve the
					// raw-passthrough behavior so a misconfigured test
					// path doesn't drop tool calls silently. This
					// matches the buffered arm's else-branch behavior.
					chunksToEmit = [][]byte{[]byte(line)}
				}

				// Drive the liveTranslator with each complete emission.
				// Each emission has COMPLETE args (the buffer's per-
				// call hold guarantee) so the translator's
				// content_block_start + input_json_delta(complete)
				// emission shape is correct.
				for _, emitted := range chunksToEmit {
					tcs := parseBufferEmittedToolCalls(emitted)
					if len(tcs) == 0 {
						continue
					}
					toolCallsForEv := convertProvidersToolCallsToMapList(tcs)
					events, terr := h.liveTranslator.ProcessEvent("", "", "", toolCallsForEv)
					if terr != nil {
						logger.Debugf("[DEBUG INTERNAL] live translator ProcessEvent (tool_call): %v", terr)
						break
					}
					if len(events) > 0 {
						h.emitLivePreamble(w, h.liveArc)
					}
					for _, ev := range events {
						if _, werr := w.Write([]byte(ev)); werr != nil {
							return werr
						}
						if f, ok := w.(http.Flusher); ok {
							f.Flush()
						}
					}
				}
				break
			}
			// Buffered mode (legacy): write tool_call delta as raw OpenAI SSE.
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
			if h.liveTranslator != nil {
				// Live mode: the plan pins the ordering constraint
				// that translator.Finalize() MUST run AFTER
				// toolCallBuffer.Flush() completes (internal_handler
				// toolcall-buffer ordering constraint). With per-call
				// hold active (Phase 3 follow-up), Flush() may now
				// emit any HELD tool calls (those that never reached
				// valid JSON during streaming — truncated stream,
				// upstream error, etc.). Those emissions MUST be fed
				// through the liveTranslator BEFORE Finalize() runs;
				// otherwise the held calls would vanish from the wire
				// AND from the arc mirror, breaking the persistence
				// contract. Ordering is load-bearing — see also the
				// phase-3 follow-up commit's regression test pair.
				if toolCallBuffer != nil {
					// Flush-before-Finalize — see helper doc for the
					// full rationale (held calls would otherwise
					// vanish from the wire AND from the arc mirror).
					if ferr := driveBufferFlushIntoLiveTranslator(w, h, toolCallBuffer); ferr != nil {
						return ferr
					}
				}

				// Now Finalize — emits content_block_stops +
				// message_delta + message_stop on the wire.
				finalEvents, ferr := h.liveTranslator.Finalize()
				if ferr != nil {
					logger.Debugf("[DEBUG INTERNAL] live translator Finalize: %v", ferr)
				} else {
					if len(finalEvents) > 0 {
						h.emitLivePreamble(w, h.liveArc)
					}
					for _, ev := range finalEvents {
						if _, werr := w.Write([]byte(ev)); werr != nil {
							return werr
						}
						if f, ok := w.(http.Flusher); ok {
							f.Flush()
						}
					}
				}
				return nil
			}
			// Buffered mode (legacy): flush the tool-call buffer's
			// remaining chunks, write the finish chunk + [DONE].
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
			// Mid-stream error (NB2): the live translator's Finalize
			// MUST run at this call site BEFORE we propagate the
			// error to the client wire — sequence pinned by the
			// plan. After Finalize, we write a well-formed Anthropic
			// error event and return.
			//
			// Phase 3 final follow-up: held tool calls must also be
			// flushed BEFORE Finalize on this path. A truncated
			// fragment that never reached valid JSON would otherwise
			// vanish — same root cause as the done site (a8b97f3).
			// Wire-write errors here are IGNORED (the error event
			// MUST still be written regardless — the helper returns
			// the werr which we deliberately discard here).
			log.Printf("Stream error: %v", event.Error)
			if h.liveTranslator != nil {
				_ = driveBufferFlushIntoLiveTranslator(w, h, toolCallBuffer)

				if fEvents, ferr := h.liveTranslator.Finalize(); ferr == nil {
					if len(fEvents) > 0 {
						h.emitLivePreamble(w, h.liveArc)
					}
					for _, ev := range fEvents {
						w.Write([]byte(ev))
					}
					if f, ok := w.(http.Flusher); ok {
						f.Flush()
					}
				}
			}
			// Emit a well-formed Anthropic error event on the wire
			// (mirrors sendAnthropicSSEError shape at
			// handler_anthropic.go:1129).
			errorEvent := map[string]interface{}{
				"type": "error",
				"error": map[string]interface{}{
					"type":    "api_error",
					"message": event.Error.Error(),
				},
			}
			errBytes, _ := json.Marshal(errorEvent)
			fmt.Fprintf(w, "event: error\ndata: %s\n\n", string(errBytes))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			return event.Error
		}
	}

	// Channel closed without an explicit "done" event — Finalize the
	// translator if live mode is active.
	//
	// Phase 3 final follow-up: same Flush-before-Finalize as the done
	// site and case "error" path — held tool calls land on the wire
	// before the close events. Wire-write errors are ignored (no
	// further writes follow this path).
	if h.liveTranslator != nil {
		_ = driveBufferFlushIntoLiveTranslator(w, h, toolCallBuffer)

		if fEvents, ferr := h.liveTranslator.Finalize(); ferr == nil {
			if len(fEvents) > 0 {
				h.emitLivePreamble(w, h.liveArc)
			}
			for _, ev := range fEvents {
				w.Write([]byte(ev))
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}

	return nil
}

// emitLivePreamble (Phase 3 / task 3.3 / NB8) is the lazy SSE
// preamble emitter. Called once on the FIRST wire-writing arm
// (case "content"/"thinking"/"tool_call") of handleStream in live
// mode. The lazy-emit property is critical: setting headersSent
// before any provider event would trip the fallback-loop guard at
// handler_anthropic.go:256-264 (which breaks on
// `arc.headersSent && !arc.bufferMode`) and block pre-first-byte
// fallback for a provider that fails before sending any event.
// Mirrors the buffered path's recorder-based semantics where
// headersSent is set only AFTER the recorder has captured all
// chunks (handleAnthropicInternalStreamResponse :735-749).
//
// arcPtr is the calling request's anthropicRequestContext so we can
// set headersSent exactly once. w is the live ResponseWriter.
// Returns true when the preamble was emitted (caller should NOT
// emit it again), false when it was already emitted.
//
// Review-fix #8 (Phase 3 review): nil-guard the arc pointer. Live
// tests that construct an InternalHandler with SetLiveArc(nil) (e.g.
// when asserting wire output without persistence) previously
// panicked on the headersSent write. Treat nil arc as "preamble
// already sent / not relevant" — never panic, never set headersSent.
func (h *InternalHandler) emitLivePreamble(w http.ResponseWriter, arc *anthropicRequestContext) bool {
	if arc == nil || arc.headersSent {
		return false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	arc.headersSent = true
	fmt.Fprint(w, ": connected\n\n")
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	return true
}

// parseBufferEmittedToolCalls parses one `data: {...}\n\n` byte slice
// emitted by toolcall.ToolCallBuffer (ProcessChunk or Flush) and
// returns the providers.ToolCall list it carries. Returns nil for
// non-tool chunks, malformed JSON, or empty tool_calls. The buffer
// emits at most one tool call per chunk in its current shape
// (buffer.go:267-316), but this parses any count for forward-
// compatibility.
//
// Used by the live tool_call arm and the Flush-before-Finalize done
// site to feed the liveTranslator with complete tool-call emissions.
func parseBufferEmittedToolCalls(line []byte) []providers.ToolCall {
	s := strings.TrimSpace(string(line))
	if !strings.HasPrefix(s, "data: ") {
		return nil
	}
	data := strings.TrimPrefix(s, "data: ")
	var chunk map[string]interface{}
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return nil
	}
	choices, ok := chunk["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return nil
	}
	choice, ok := choices[0].(map[string]interface{})
	if !ok {
		return nil
	}
	delta, ok := choice["delta"].(map[string]interface{})
	if !ok {
		return nil
	}
	toolCallsRaw, ok := delta["tool_calls"].([]interface{})
	if !ok || len(toolCallsRaw) == 0 {
		return nil
	}
	var out []providers.ToolCall
	for _, tc := range toolCallsRaw {
		tcMap, ok := tc.(map[string]interface{})
		if !ok {
			continue
		}
		ptc := providers.ToolCall{}
		if id, ok := tcMap["id"].(string); ok {
			ptc.ID = id
		}
		if typ, ok := tcMap["type"].(string); ok {
			ptc.Type = typ
		}
		if fn, ok := tcMap["function"].(map[string]interface{}); ok {
			if name, ok := fn["name"].(string); ok {
				ptc.Function.Name = name
			}
			if args, ok := fn["arguments"].(string); ok {
				ptc.Function.Arguments = args
			}
		}
		out = append(out, ptc)
	}
	return out
}

// driveBufferFlushIntoLiveTranslator (live mode only) drains any HELD
// tool calls from the toolCallBuffer via Flush(), parses each emitted
// complete chunk back to providers.ToolCall, drives the liveTranslator
// so the held calls land on the Anthropic wire, and writes the
// emitted Anthropic events to w.
//
// Used by every end-of-stream path in live mode:
//   - case "done"            — see a8b97f3 done-site
//   - case "error"           — Phase 3 final follow-up
//   - channel-closed-without-done — Phase 3 final follow-up
//
// Flush-before-Finalize ordering is LOAD-BEARING for held calls:
// without it, a tool call whose arguments never reached valid JSON
// during streaming (truncated stream, upstream error, channel close)
// would vanish from the wire AND from the arc mirror, breaking the
// persistence contract. The repair chain (pkg/toolrepair) runs in
// emitToolCall when JSON is invalid, so a held but repairable
// fragment lands on the wire as REPAIRED args.
//
// Returns the FIRST wire-write error (if any). Callers decide:
//   - done path propagates (returns the werr from handleStream).
//   - error / channel-closed paths ignore (continue to write the
//     well-formed Anthropic error event, or simply end-of-stream).
//
// No-op if toolCallBuffer is nil.
func driveBufferFlushIntoLiveTranslator(w http.ResponseWriter, h *InternalHandler, toolCallBuffer *toolcall.ToolCallBuffer) error {
	if toolCallBuffer == nil {
		return nil
	}
	flushChunks := toolCallBuffer.Flush()
	for _, emitted := range flushChunks {
		tcs := parseBufferEmittedToolCalls(emitted)
		if len(tcs) == 0 {
			continue
		}
		toolCallsForEv := convertProvidersToolCallsToMapList(tcs)
		fEvents, terr := h.liveTranslator.ProcessEvent("", "", "", toolCallsForEv)
		if terr != nil {
			logger.Debugf("[DEBUG INTERNAL] live translator ProcessEvent (flush tool_call): %v", terr)
			continue
		}
		if len(fEvents) > 0 {
			h.emitLivePreamble(w, h.liveArc)
		}
		for _, ev := range fEvents {
			if _, werr := w.Write([]byte(ev)); werr != nil {
				return werr
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}
	stats := toolCallBuffer.GetRepairStats()
	if stats.Attempted > 0 {
		log.Printf("[TOOL-BUFFER] InternalHandler (live): Repair stats: attempted=%d, success=%d, failed=%d",
			stats.Attempted, stats.Successful, stats.Failed)
	}
	return nil
}

// convertProvidersToolCallsToMapList converts providers.ToolCall into
// the []interface{} of OpenAI-shape {index,id,type,function{...}} maps
// that translator.IncrementalStreamTranslator.ProcessEvent expects.
func convertProvidersToolCallsToMapList(tcs []providers.ToolCall) []interface{} {
	out := make([]interface{}, 0, len(tcs))
	for i, tc := range tcs {
		out = append(out, map[string]interface{}{
			"index": i,
			"id":    tc.ID,
			"type":  tc.Type,
			"function": map[string]interface{}{
				"name":      tc.Function.Name,
				"arguments": tc.Function.Arguments,
			},
		})
	}
	return out
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
