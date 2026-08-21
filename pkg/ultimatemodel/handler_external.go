package ultimatemodel

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy/normalizers"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy/token"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy/translator"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/toolcall"
)

// sharedHTTPClient is a module-level HTTP client with connection pooling.
// Reusing a single client prevents accumulation of orphaned connection pools
// that occur when creating a new client per request.
var sharedHTTPClient = &http.Client{
	Timeout: 0, // No timeout - use context for cancellation
	Transport: &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     300 * time.Second,
	},
}

// executeExternal handles requests to external upstream (proxy mode)
// This is a RAW PROXY - no retry, no fallback, no buffering, no loop detection
//
// interleaved is the X-Proxy-Interleaved-Thinking flag re-parsed by
// Execute (B3). upstreamProvider is the lowercase credential provider
// name when CredentialID is set on modelCfg (D3); empty otherwise.
// The gate fires iff interleaved && upstreamProvider == providers.ProviderMiniMax
// (case-insensitive). Gate lives in this function so H5 (short-circuit
// BEFORE parse/marshal) is guaranteed at the strongest no-op site.
func (h *Handler) executeExternal(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	requestBody map[string]interface{},
	requestBodyBytes []byte,
	modelCfg *models.ModelConfig,
	isStream bool,
	interleaved bool,
	upstreamProvider string,
) (*ExecuteResult, error) {
	cfg := h.config.Get()
	var _ config.Config = cfg // Ensure config package is used

	// Get upstream URL from config
	upstreamURL := cfg.UpstreamURL
	if upstreamURL == "" {
		return nil, fmt.Errorf("upstream URL not configured")
	}

	// Prepare request body with model ID override
	bodyCopy := make(map[string]interface{})
	for k, v := range requestBody {
		bodyCopy[k] = v
	}

	// Use the ultimate model ID
	if modelCfg.ID != "" {
		bodyCopy["model"] = modelCfg.ID
	}

	// Marshal bodyCopy (carries the model override). bodyBytes is what
	// gets sent upstream. Pre-existing behavior: gated-off and gated-on
	// paths both produce bodyBytes via this step; the difference is
	// whether the translator runs on bodyBytes AFTER this marshal (so the
	// model override is preserved through the parse→mutate→re-marshal
	// round-trip the translator performs).
	bodyBytes, err := json.Marshal(bodyCopy)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// D3 + H5: provider gate. When the gate fires, the translator mutates
	// the already-model-overridden bodyBytes (top-level reasoning_split +
	// per-message reasoning_details); when the gate is off, bodyBytes is
	// used unchanged. Gated-off is a pure no-op from the translator's
	// perspective.
	//
	// upstreamProvider is already lowercased in Execute (per D3). Compare
	// against the canonical ProviderMiniMax constant (providers.ProviderMiniMax
	// == "minimax"; package is imported above) for the case-insensitive
	// match per handler_anthropic.go:297 precedent.
	providerIsMiniMax := upstreamProvider != "" && upstreamProvider == strings.ToLower(string(providers.ProviderMiniMax))
	if interleaved && providerIsMiniMax {
		outBytes, terr := translator.TranslateRequestBytes(bodyBytes)
		if terr != nil {
			return nil, fmt.Errorf("ultimate-external translator: %w", terr)
		}
		bodyBytes = outBytes
	}

	// Create upstream request
	upstreamReq, err := http.NewRequestWithContext(ctx, "POST", upstreamURL+"/v1/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create upstream request: %w", err)
	}

	// Copy headers from original request
	upstreamReq.Header.Set("Content-Type", "application/json")
	for key, values := range r.Header {
		// Skip hop-by-hop headers
		if key == "Host" || key == "Content-Length" || key == "Transfer-Encoding" {
			continue
		}
		// D4: NARROW strip of exactly x-proxy-interleaved-thinking on
		// the ultimate-external path. Case-insensitive via
		// strings.EqualFold. The general x-proxy-* strip is REJECTED
		// (would change flag-absent forwarding behavior — old clients
		// never set this header so the narrow strip preserves the
		// flag-absent invariant).
		if strings.EqualFold(key, "x-proxy-interleaved-thinking") {
			continue
		}
		for _, value := range values {
			upstreamReq.Header.Add(key, value)
		}
	}

	// Send request using shared HTTP client with connection pooling
	resp, err := sharedHTTPClient.Do(upstreamReq)
	if err != nil {
		return nil, fmt.Errorf("upstream request failed: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("upstream returned %d: %s", resp.StatusCode, string(body))
	}

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	if isStream {
		// Stream response directly
		return h.streamResponse(w, resp, modelCfg.ID, requestBodyBytes, interleaved, providerIsMiniMax)
	}

	// Non-streaming: read body, extract usage, then translate + copy to response
	bodyBytes, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// P2-4 (d): ultim-ext non-stream — invoke the response
	// translator on the upstream body BEFORE writing to the client.
	// Gate is (interleaved && providerIsMiniMax) — short-circuits
	// BEFORE any parse (H5). Gated-off is a pure no-op on this
	// verbatim-byte path (strictest invariant — the gate must
	// guarantee byte-identical behavior on ultim-ext non-stream).
	if interleaved && providerIsMiniMax {
		translated, terr := translator.TranslateNonStreamResponseBytes(bodyBytes)
		if terr != nil {
			// §6.1: non-stream KEEPS error return; caller routes
			// to WriteError 502 api_error. Surface as wrapped
			// upstream error.
			return nil, fmt.Errorf("ultimate-external response translator: %w", terr)
		}
		bodyBytes = translated
	}

	// Extract usage from response
	usage := extractUsageFromResponse(bodyBytes)

	// Fallback token counting if usage is nil/zero
	if usage == nil || (usage.PromptTokens == 0 && usage.CompletionTokens == 0 && usage.TotalTokens == 0) {
		if token.FallbackEnabled() {
			tokenizer := token.GetTokenizer()
			promptTokens, err := tokenizer.CountPromptTokens(requestBodyBytes, modelCfg.ID)
			if err != nil {
				log.Printf("[DEBUG][fallback-token-count] error counting prompt tokens: %v, model=%s", err, modelCfg.ID)
			}
			completionText := token.ExtractCompletionTextFromJSON(bodyBytes)
			completionTokens, err := tokenizer.CountCompletionTokens(completionText, modelCfg.ID)
			if err != nil {
				log.Printf("[DEBUG][fallback-token-count] error counting completion tokens: %v, model=%s", err, modelCfg.ID)
			}
			usage = &store.Usage{
				PromptTokens:     promptTokens,
				CompletionTokens: completionTokens,
				TotalTokens:      promptTokens + completionTokens,
			}
			log.Printf("[DEBUG][fallback-token-count] ultimate-external: model=%s prompt=%d completion=%d total=%d",
				modelCfg.ID, promptTokens, completionTokens, promptTokens+completionTokens)
		}
	}

	// Passive capture: parse content + reasoning_content +
	// tool_calls from the post-translation response bytes (the
	// bytes that are about to be written to the client). This is
	// purely observational — bodyBytes is read-only here and
	// w.Write(bodyBytes) below is unchanged. The captured strings
	// / tool calls are returned to the outer handler via
	// ExecuteResult.Content / .Thinking / .ToolCalls so it can
	// persist a store.Message for the Web UI.
	//
	// W7: tool-call-only responses (empty Content + empty Thinking
	// + real tool_calls) previously left the UI empty because the
	// outer persistence conditional skipped them. Now that we
	// observe the tool_calls here, the outer conditional can also
	// persist when ToolCalls is non-empty.
	content, thinking := captureFromNonStreamResponse(bodyBytes)
	toolCalls := captureToolCallsFromNonStreamResponse(bodyBytes)

	// Write response
	w.WriteHeader(resp.StatusCode)
	_, err = w.Write(bodyBytes)
	return &ExecuteResult{
		Usage:     usage,
		Content:   content,
		Thinking:  thinking,
		ToolCalls: toolCalls,
	}, err
}

// extractUsageFromResponse parses usage data from a non-streaming response body.
func extractUsageFromResponse(body []byte) *store.Usage {
	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil
	}

	usageData, ok := resp["usage"].(map[string]interface{})
	if !ok {
		return nil
	}

	var promptTokens, completionTokens, totalTokens int
	if v, ok := usageData["prompt_tokens"].(float64); ok {
		promptTokens = int(v)
	}
	if v, ok := usageData["completion_tokens"].(float64); ok {
		completionTokens = int(v)
	}
	if v, ok := usageData["total_tokens"].(float64); ok {
		totalTokens = int(v)
	}
	return &store.Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      totalTokens,
	}
}

// streamResponse streams the upstream response directly to client
//
// interleaved is the X-Proxy-Interleaved-Thinking flag (re-parsed
// from r.Header in Execute, threaded through executeExternal).
// providerIsMiniMax is the credential-derived provider gate. Both
// must be true to construct a StreamTranslator instance; gated-off
// is a pure no-op (H5 — strictest invariant on this verbatim-byte
// path; gate short-circuits BEFORE any parse).
func (h *Handler) streamResponse(w http.ResponseWriter, resp *http.Response, modelID string, requestBodyBytes []byte, interleaved bool, providerIsMiniMax bool) (*ExecuteResult, error) {
	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("streaming not supported")
	}

	// Create normalizer context for this stream
	normCtx := normalizers.NewContext("openai", "ultimate-external")
	normalizers.GetRegistry().ResetAll(normCtx)

	// Create tool call buffer
	var toolCallBuffer *toolcall.ToolCallBuffer
	if !h.toolCallBufferDisabled && h.toolRepairConfig != nil && h.toolRepairConfig.Enabled {
		toolCallBuffer = toolcall.NewToolCallBufferWithRepair(
			h.toolCallBufferMaxSize,
			modelID,
			"ultimate-external",
			h.toolRepairConfig,
		)
	} else if !h.toolCallBufferDisabled {
		toolCallBuffer = toolcall.NewToolCallBuffer(
			h.toolCallBufferMaxSize,
			modelID,
			"ultimate-external",
		)
	}

	// P2-5 (b): ultim-ext stream — construct the StreamTranslator
	// per-stream instance ONLY when the gate fires. Gated-off ⇒ no
	// instance ⇒ no per-chunk work ⇒ byte-identical passthrough.
	// The instance's lifetime is the loop scope (P2-3 / §3.3).
	var streamTranslator *translator.StreamTranslator
	if interleaved && providerIsMiniMax {
		streamTranslator = translator.NewStreamTranslator()
	}

	// Track the last chunk containing usage data
	var lastUsageChunk []byte

	// Capture-side only: accumulate Content + Thinking +
	// ToolCalls from each chunk that gets written to the client.
	// The builders / accumulators below are populated passively
	// alongside the existing write-to-buf loop; nothing in the
	// wire path is touched.
	var capturedContent strings.Builder
	var capturedThinking strings.Builder

	// W7: tool calls arrive as delta.tool_calls arrays in SSE
	// chunks. Accumulate by index (mirrors
	// pkg/proxy/handler_helpers.go's extractStreamChunkContent)
	// so the outer persistence layer sees fully-assembled
	// tool calls when streaming completes.
	var capturedToolCalls []store.ToolCall
	var capturedToolCallArgBuilders []*strings.Builder

	// Create buffer to batch all writes like race executor does
	var buf bytes.Buffer

	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("error reading stream: %w", err)
		}

		lineBytes := []byte(line)

		// Apply normalization to fix malformed chunks
		normalizedLine, modified, normalizerName := normalizers.NormalizeWithContextAndName(lineBytes, normCtx)
		if modified {
			log.Printf("[DEBUG] UltimateModel: normalized stream chunk by %s", normalizerName)
			lineBytes = normalizedLine
		}

		// Extract usage from data lines (BEFORE the translator
		// strips reasoning_details — usage is in a different
		// location and is not affected by the strip).
		if bytes.HasPrefix(lineBytes, []byte("data: ")) {
			data := bytes.TrimPrefix(lineBytes, []byte("data: "))
			if len(data) > 0 &&
				!bytes.HasPrefix(data, []byte("[DONE]")) &&
				!bytes.HasPrefix(data, []byte("\n")) {
				if extractUsageFromChunk(data) != nil {
					lastUsageChunk = data
				}
			}
		}

		// P2-5 (b) / W3: apply the response-side stream translator
		// BEFORE the tool call buffer. The translator returns
		// (stripped, emitted) — the caller writes stripped first,
		// then each emitted[i] in order, before the next flush
		// boundary. The translator is error-free by construction
		// (§6.1); err is always nil. ultim-ext has no mid-stream
		// flush (H8) — all bytes accumulate in buf and are written
		// in a single flush at end-of-stream.
		translatorLine := lineBytes
		var translatorEmitted [][]byte
		if streamTranslator != nil {
			var terr error
			translatorLine, translatorEmitted, terr = streamTranslator.ChunkBytes(lineBytes)
			if terr != nil {
				// §6.1: should be impossible. Log + continue
				// with the original line.
				log.Printf("[WARN] ultimate-external stream translator: %v", terr)
				translatorLine = lineBytes
				translatorEmitted = nil
			}
		}

		// Process through tool call buffer (translatorLine carries
		// reasoning_details stripped, if any — see W3 ordering).
		var chunksToEmit [][]byte
		if toolCallBuffer != nil {
			chunksToEmit = toolCallBuffer.ProcessChunk(translatorLine)
		} else {
			chunksToEmit = [][]byte{translatorLine}
		}

		// Prepend the translator's emitted chunks (if any) so the
		// client sees reasoning_content deltas before the stripped
		// upstream chunk on each loop iteration. C1
		// uniform-framing contract: ChunkBytes frames both
		// `stripped` and each `emitted[i]` on the mutated path,
		// and returns the ORIGINAL line VERBATIM (already framed)
		// on the unchanged path. The call site is therefore
		// responsible for ZERO framing here — just concatenate in
		// order. ultim-ext has no mid-stream flush (H8) — all
		// bytes accumulate in buf and are written in a single
		// flush at end-of-stream.
		if len(translatorEmitted) > 0 {
			full := make([][]byte, 0, len(translatorEmitted)+len(chunksToEmit))
			full = append(full, translatorEmitted...)
			full = append(full, chunksToEmit...)
			chunksToEmit = full
		}

		// Capture-side only: observe the chunks that are about to
		// be buffered to the client and accumulate any content /
		// reasoning_content / tool_calls deltas.
		// captureFromSSEChunkBytes is best-effort and never
		// mutates the chunk bytes — it just parses them. Wire
		// bytes to buf are unchanged.
		for _, chunk := range chunksToEmit {
			captureFromSSEChunkBytes(chunk, &capturedContent, &capturedThinking)
			// W7: same shape of observation for tool calls —
			// the wire bytes the client sees are the SAME
			// bytes we are parsing here. captureFromSSEChunk*
			// helpers below are read-only and best-effort.
			captureToolCallsFromSSEChunk(chunk, &capturedToolCalls, &capturedToolCallArgBuilders)
			buf.Write(chunk)
		}
	}

	// Flush remaining buffered tool calls at stream end
	if toolCallBuffer != nil {
		flushChunks := toolCallBuffer.Flush()
		for _, chunk := range flushChunks {
			// Capture-side only: also observe the tool-buffer's
			// post-flush chunks (typically the tool_call delta
			// + finish chunk). captureFromSSEChunkBytes silently
			// ignores chunks without delta.content /
			// delta.reasoning_content, so this is safe.
			captureFromSSEChunkBytes(chunk, &capturedContent, &capturedThinking)
			// W7: same shape for tool calls — the
			// tool-buffer's flush chunks ARE the bytes
			// the client sees; observing them here is
			// equivalent to parsing the wire output and
			// cannot diverge from what capture-from-raw
			// would see.
			captureToolCallsFromSSEChunk(chunk, &capturedToolCalls, &capturedToolCallArgBuilders)
			buf.Write(chunk)
		}

		stats := toolCallBuffer.GetRepairStats()
		if stats.Attempted > 0 {
			log.Printf("[TOOL-BUFFER] UltimateModel External: Repair stats: attempted=%d, success=%d, failed=%d",
				stats.Attempted, stats.Successful, stats.Failed)
		}
	}

	// Extract usage from the last chunk
	var usage *store.Usage
	if lastUsageChunk != nil {
		usage = extractUsageFromChunk(lastUsageChunk)
	}

	// Fallback token counting
	if usage == nil || (usage.PromptTokens == 0 && usage.CompletionTokens == 0 && usage.TotalTokens == 0) {
		if token.FallbackEnabled() {
			tokenizer := token.GetTokenizer()
			promptTokens, err := tokenizer.CountPromptTokens(requestBodyBytes, modelID)
			if err != nil {
				log.Printf("[DEBUG][fallback-token-count] error counting prompt tokens: %v", err)
			}
			completionText := token.ExtractCompletionTextFromChunks(buf.Bytes())
			completionTokens, err := tokenizer.CountCompletionTokens(completionText, modelID)
			if err != nil {
				log.Printf("[DEBUG][fallback-token-count] error counting completion tokens: %v", err)
			}
			usage = &store.Usage{
				PromptTokens:     promptTokens,
				CompletionTokens: completionTokens,
				TotalTokens:      promptTokens + completionTokens,
			}
			log.Printf("[DEBUG][fallback-token-count] ultimate-external: model=%s prompt=%d completion=%d total=%d",
				modelID, promptTokens, completionTokens, promptTokens+completionTokens)
		}
	}

	// Write all buffered data to client in one flush
	w.Write(buf.Bytes())
	flusher.Flush()

	return &ExecuteResult{
		Usage:     usage,
		Content:   capturedContent.String(),
		Thinking:  capturedThinking.String(),
		ToolCalls: finalizeAccumulatedToolCalls(capturedToolCalls, capturedToolCallArgBuilders),
	}, nil
}

// extractUsageFromChunk parses usage data from an SSE chunk JSON payload.
func extractUsageFromChunk(data []byte) *store.Usage {
	var chunk map[string]interface{}
	if err := json.Unmarshal(data, &chunk); err != nil {
		return nil
	}
	usageData, ok := chunk["usage"].(map[string]interface{})
	if !ok {
		return nil
	}
	var promptTokens, completionTokens, totalTokens int
	if v, ok := usageData["prompt_tokens"].(float64); ok {
		promptTokens = int(v)
	}
	if v, ok := usageData["completion_tokens"].(float64); ok {
		completionTokens = int(v)
	}
	if v, ok := usageData["total_tokens"].(float64); ok {
		totalTokens = int(v)
	}
	return &store.Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      totalTokens,
	}
}
