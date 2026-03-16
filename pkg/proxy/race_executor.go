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
)

// executeRequest performs the actual HTTP call to upstream
// and streams the response into the request's buffer.
func executeRequest(ctx context.Context, cfg *ConfigSnapshot, originalReq *http.Request, rawBody []byte, req *upstreamRequest) error {
	req.MarkStarted()

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
	return handleStreamingResponse(ctx, cfg, resp, req)
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

// handleStreamingResponse handles SSE streaming responses
func handleStreamingResponse(ctx context.Context, cfg *ConfigSnapshot, resp *http.Response, req *upstreamRequest) error {
	// MEMORY TRAP FIX: Use bufio.Reader with increased buffer instead of bufio.Scanner
	// to avoid issues with long SSE lines and memory retention.
	reader := bufio.NewReaderSize(resp.Body, 64*1024) // 64KB buffer

	// Create ticker outside the loop
	idleTimer := time.NewTimer(time.Duration(cfg.IdleTimeout))
	defer idleTimer.Stop()

	sawDone := false

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

			// Add chunk to buffer
			if !req.buffer.Add(line) {
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
