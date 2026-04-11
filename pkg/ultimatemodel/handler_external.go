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
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy/token"
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
func (h *Handler) executeExternal(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	requestBody map[string]interface{},
	requestBodyBytes []byte,
	modelCfg *models.ModelConfig,
	isStream bool,
) (*store.Usage, error) {
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

	// Marshal request body
	bodyBytes, err := json.Marshal(bodyCopy)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
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
		return h.streamResponse(w, resp, modelCfg.ID, requestBodyBytes)
	}

	// Non-streaming: read body, extract usage, then copy to response
	bodyBytes, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
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

	// Write response
	w.WriteHeader(resp.StatusCode)
	_, err = w.Write(bodyBytes)
	return usage, err
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
func (h *Handler) streamResponse(w http.ResponseWriter, resp *http.Response, modelID string, requestBodyBytes []byte) (*store.Usage, error) {
	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("streaming not supported")
	}

	// Create tool call buffer (same pattern as handleInternalStream)
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

	// Track the last chunk containing usage data (only need the most recent)
	var lastUsageChunk []byte

	// Accumulate raw SSE chunks for fallback token counting
	var rawChunks bytes.Buffer

	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("error reading stream: %w", err)
		}

		// Convert to []byte once and reuse (fixes double allocation)
		lineBytes := []byte(line)

		// Extract usage from data lines - only keep the most recent usage chunk
		// This replaces unbounded accumulation of all dataLines
		if bytes.HasPrefix(lineBytes, []byte("data: ")) {
			data := bytes.TrimPrefix(lineBytes, []byte("data: "))
			// Check for non-empty, non-[DONE] lines
			// Only track chunks that might contain usage (avoid checking [DONE], empty lines)
			if len(data) > 0 &&
				!bytes.HasPrefix(data, []byte("[DONE]")) &&
				!bytes.HasPrefix(data, []byte("\n")) {
				// Try to extract usage - if present, update lastUsageChunk
				if extractUsageFromChunk(data) != nil {
					lastUsageChunk = data
				}
			}
		}

		// Process through tool call buffer
		var chunksToEmit [][]byte
		if toolCallBuffer != nil {
			chunksToEmit = toolCallBuffer.ProcessChunk(lineBytes)
		} else {
			chunksToEmit = [][]byte{lineBytes}
		}

		// Write all chunks and accumulate for fallback token counting
		for _, chunk := range chunksToEmit {
			w.Write(chunk)
			rawChunks.Write(chunk)
		}
		flusher.Flush()
	}

	// Flush remaining buffered tool calls at stream end
	if toolCallBuffer != nil {
		flushChunks := toolCallBuffer.Flush()
		for _, chunk := range flushChunks {
			w.Write(chunk)
		}

		// Log repair stats if any repairs occurred
		stats := toolCallBuffer.GetRepairStats()
		if stats.Attempted > 0 {
			log.Printf("[TOOL-BUFFER] UltimateModel External: Repair stats: attempted=%d, success=%d, failed=%d",
				stats.Attempted, stats.Successful, stats.Failed)
		}
	}

	// Extract usage from the last chunk that contained usage data
	var usage *store.Usage
	if lastUsageChunk != nil {
		usage = extractUsageFromChunk(lastUsageChunk)
	}

	// Fallback token counting if usage is nil/zero
	if usage == nil || (usage.PromptTokens == 0 && usage.CompletionTokens == 0 && usage.TotalTokens == 0) {
		if token.FallbackEnabled() {
			tokenizer := token.GetTokenizer()
			promptTokens, err := tokenizer.CountPromptTokens(requestBodyBytes, modelID)
			if err != nil {
				log.Printf("[DEBUG][fallback-token-count] error counting prompt tokens: %v, model=%s", err, modelID)
			}
			completionText := token.ExtractCompletionTextFromChunks(rawChunks.Bytes())
			completionTokens, err := tokenizer.CountCompletionTokens(completionText, modelID)
			if err != nil {
				log.Printf("[DEBUG][fallback-token-count] error counting completion tokens: %v, model=%s", err, modelID)
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

	return usage, nil
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
