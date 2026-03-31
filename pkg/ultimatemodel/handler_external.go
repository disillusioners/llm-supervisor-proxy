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
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/toolcall"
)

// executeExternal handles requests to external upstream (proxy mode)
// This is a RAW PROXY - no retry, no fallback, no buffering, no loop detection
func (h *Handler) executeExternal(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	requestBody map[string]interface{},
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

	// Send request
	client := &http.Client{
		Timeout: 0, // No timeout - use context for cancellation
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 100,
			IdleConnTimeout:     300 * time.Second,
		},
	}

	resp, err := client.Do(upstreamReq)
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
		return h.streamResponse(w, resp, modelCfg.ID)
	}

	// Non-streaming: read body, extract usage, then copy to response
	bodyBytes, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Extract usage from response
	usage := extractUsageFromResponse(bodyBytes)

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
func (h *Handler) streamResponse(w http.ResponseWriter, resp *http.Response, modelID string) (*store.Usage, error) {
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

	// Collect all data lines with JSON for reverse-scan usage extraction
	var dataLines [][]byte

	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("error reading stream: %w", err)
		}

		// Collect data lines for reverse-scan usage extraction
		if bytes.HasPrefix([]byte(line), []byte("data: ")) {
			data := bytes.TrimPrefix([]byte(line), []byte("data: "))
			// Only collect non-empty, non-[DONE] lines
			dataStr := string(data)
			if dataStr != "[DONE]\n" && dataStr != "[DONE]" && dataStr != "\n" && dataStr != "" {
				dataLines = append(dataLines, data)
			}
		}

		// Process through tool call buffer
		var chunksToEmit [][]byte
		if toolCallBuffer != nil {
			chunksToEmit = toolCallBuffer.ProcessChunk([]byte(line))
		} else {
			chunksToEmit = [][]byte{[]byte(line)}
		}

		// Write all chunks
		for _, chunk := range chunksToEmit {
			w.Write(chunk)
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

	// Extract usage by reverse-scanning collected data lines (same pattern as handler.go's streamResult)
	var usage *store.Usage
	for i := len(dataLines) - 1; i >= 0; i-- {
		if usage = extractUsageFromChunk(dataLines[i]); usage != nil {
			break
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
