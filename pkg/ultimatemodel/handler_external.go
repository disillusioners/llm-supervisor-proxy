package ultimatemodel

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
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
) error {
	cfg := h.config.Get()
	var _ config.Config = cfg // Ensure config package is used

	// Get upstream URL from config
	upstreamURL := cfg.UpstreamURL
	if upstreamURL == "" {
		return fmt.Errorf("upstream URL not configured")
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
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create upstream request
	upstreamReq, err := http.NewRequestWithContext(ctx, "POST", upstreamURL+"/v1/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to create upstream request: %w", err)
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
		return fmt.Errorf("upstream request failed: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upstream returned %d: %s", resp.StatusCode, string(body))
	}

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	if isStream {
		// Stream response directly
		return h.streamResponse(w, resp)
	}

	// Non-streaming: copy body directly
	w.WriteHeader(resp.StatusCode)
	_, err = io.Copy(w, resp.Body)
	return err
}

// streamResponse streams the upstream response directly to client
func (h *Handler) streamResponse(w http.ResponseWriter, resp *http.Response) error {
	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("streaming not supported")
	}

	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("error reading stream: %w", err)
		}

		// Write line directly
		fmt.Fprint(w, line)
		flusher.Flush()
	}

	return nil
}
