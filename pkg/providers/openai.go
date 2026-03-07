package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/bufferstore"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/toolrepair"
)

// OpenAIProvider implements Provider for OpenAI-compatible APIs
type OpenAIProvider struct {
	apiKey      string
	baseURL     string
	client      *http.Client
	bufferStore *bufferstore.BufferStore // Optional: for saving debug info
	requestID   string                   // Optional: request ID for buffer naming
	repairer    *toolrepair.Repairer     // Optional: for repairing tool call JSON
}

// NewOpenAIProvider creates a new OpenAI provider
func NewOpenAIProvider(apiKey, baseURL string) *OpenAIProvider {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return &OpenAIProvider{
		apiKey:  apiKey,
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}
}

// SetDebugContext sets the buffer store and request ID for debug file saving
func (p *OpenAIProvider) SetDebugContext(bufferStore *bufferstore.BufferStore, requestID string) {
	p.bufferStore = bufferStore
	p.requestID = requestID
}

// SetRepairer sets the tool call repairer
func (p *OpenAIProvider) SetRepairer(repairer *toolrepair.Repairer) {
	p.repairer = repairer
}

// Name returns the provider name
func (p *OpenAIProvider) Name() string {
	return "openai"
}

// ChatCompletion sends a non-streaming chat completion request
func (p *OpenAIProvider) ChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	req.Stream = false

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	p.setHeaders(httpReq)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, &ProviderError{
			Provider:  p.Name(),
			Message:   err.Error(),
			Retryable: isNetworkError(err),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		// Write request to file (message, toolcall) and provide a link in frontend for debugging
		return nil, p.handleError(resp, req)
	}

	var result ChatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Repair tool calls if repairer is configured
	if p.repairer != nil {
		for i := range result.Choices {
			if result.Choices[i].Message == nil {
				continue
			}

			// Convert tool calls to repair data
			toolCallsData := make([]toolrepair.ToolCallData, len(result.Choices[i].Message.ToolCalls))
			for j, tc := range result.Choices[i].Message.ToolCalls {
				toolCallsData[j] = toolrepair.ToolCallData{
					ID:        tc.ID,
					Type:      tc.Type,
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				}
			}

			// Repair tool calls
			repairedCalls, stats := p.repairer.RepairToolCallsData(toolCallsData)
			if stats.Repaired > 0 || stats.Failed > 0 {
				log.Printf("[TOOL-REPAIR] total=%d repaired=%d failed=%d duration=%v",
					stats.TotalToolCalls, stats.Repaired, stats.Failed, stats.Duration)
			}

			// Update with repaired data
			for j, rc := range repairedCalls {
				result.Choices[i].Message.ToolCalls[j].Function.Arguments = rc.Arguments
			}
		}
	}

	return &result, nil
}

// StreamChatCompletion sends a streaming chat completion request
func (p *OpenAIProvider) StreamChatCompletion(ctx context.Context, req *ChatCompletionRequest) (<-chan StreamEvent, error) {
	req.Stream = true

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	p.setHeaders(httpReq)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, &ProviderError{
			Provider:  p.Name(),
			Message:   err.Error(),
			Retryable: isNetworkError(err),
		}
	}

	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		return nil, p.handleError(resp, req)
	}

	eventCh := make(chan StreamEvent, 100)

	go func() {
		defer close(eventCh)
		defer resp.Body.Close()

		p.processStream(resp.Body, eventCh)
	}()

	return eventCh, nil
}

// IsRetryable checks if an error should trigger a retry
func (p *OpenAIProvider) IsRetryable(err error) bool {
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		return providerErr.Retryable
	}
	return false
}

// setHeaders sets common headers for OpenAI requests
func (p *OpenAIProvider) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
}

// handleError converts HTTP error response to ProviderError
// If bufferStore is configured, saves the request to a file for debugging
func (p *OpenAIProvider) handleError(resp *http.Response, req *ChatCompletionRequest) *ProviderError {
	body, _ := io.ReadAll(resp.Body)

	var apiErr struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	json.Unmarshal(body, &apiErr)

	msg := apiErr.Error.Message
	if msg == "" {
		msg = string(body)
	}

	// Determine if retryable based on status code
	retryable := false
	switch resp.StatusCode {
	case 429: // Rate limit
		retryable = true
	case 500, 502, 503, 504: // Server errors
		retryable = true
	}

	providerErr := &ProviderError{
		Provider:   p.Name(),
		StatusCode: resp.StatusCode,
		Message:    msg,
		Retryable:  retryable,
	}

	// Save request to file for debugging
	if p.bufferStore != nil && p.requestID != "" && req != nil {
		if requestJSON, err := json.MarshalIndent(req, "", "  "); err == nil {
			bufferID := fmt.Sprintf("%s_provider_request", p.requestID)
			if saveErr := p.bufferStore.Save(bufferID, requestJSON); saveErr == nil {
				providerErr.BufferID = bufferID
			}
		}
	}

	return providerErr
}

// processStream processes SSE stream and sends normalized events
func (p *OpenAIProvider) processStream(reader io.Reader, eventCh chan<- StreamEvent) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024) // 10MB max

	var lastResponse *ChatCompletionResponse

	for scanner.Scan() {
		line := scanner.Text()

		// Skip empty lines
		if line == "" {
			continue
		}

		// Only process data lines
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")

		// Check for stream end
		if data == "[DONE]" {
			if lastResponse != nil {
				// Repair tool calls in the final response
				if p.repairer != nil {
					for i := range lastResponse.Choices {
						if lastResponse.Choices[i].Message == nil {
							continue
						}

						// Convert tool calls to repair data
						toolCallsData := make([]toolrepair.ToolCallData, len(lastResponse.Choices[i].Message.ToolCalls))
						for j, tc := range lastResponse.Choices[i].Message.ToolCalls {
							toolCallsData[j] = toolrepair.ToolCallData{
								ID:        tc.ID,
								Type:      tc.Type,
								Name:      tc.Function.Name,
								Arguments: tc.Function.Arguments,
							}
						}

						// Repair tool calls
						repairedCalls, stats := p.repairer.RepairToolCallsData(toolCallsData)
						if stats.Repaired > 0 || stats.Failed > 0 {
							log.Printf("[TOOL-REPAIR] total=%d repaired=%d failed=%d duration=%v",
								stats.TotalToolCalls, stats.Repaired, stats.Failed, stats.Duration)
						}

						// Update with repaired data
						for j, rc := range repairedCalls {
							lastResponse.Choices[i].Message.ToolCalls[j].Function.Arguments = rc.Arguments
						}
					}
				}

				eventCh <- StreamEvent{
					Type:     "done",
					Response: lastResponse,
				}
			} else {
				eventCh <- StreamEvent{
					Type: "done",
				}
			}
			return
		}

		var chunk ChatCompletionResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			eventCh <- StreamEvent{
				Type:  "error",
				Error: fmt.Errorf("failed to parse chunk: %w", err),
			}
			continue
		}

		// Extract content delta
		if len(chunk.Choices) > 0 {
			choice := chunk.Choices[0]
			if choice.Delta != nil {
				// Content can be string or nil during streaming
				if contentStr, ok := choice.Delta.Content.(string); ok && contentStr != "" {
					eventCh <- StreamEvent{
						Type:    "content",
						Content: contentStr,
					}
				}
				// Handle tool_calls in streaming
				if len(choice.Delta.ToolCalls) > 0 {
					eventCh <- StreamEvent{
						Type:      "tool_call",
						ToolCalls: choice.Delta.ToolCalls,
					}
				}
			}
			if choice.FinishReason != "" {
				lastResponse = &chunk
			}
		}
	}

	if err := scanner.Err(); err != nil {
		eventCh <- StreamEvent{
			Type:  "error",
			Error: err,
		}
	}
}

// isNetworkError checks if the error is a network-level error
func isNetworkError(err error) bool {
	if err == nil {
		return false
	}
	// Network errors are generally retryable
	return true
}

// parseRetryAfter parses the Retry-After header
func parseRetryAfter(header string) time.Duration {
	if header == "" {
		return 0
	}

	// Try parsing as seconds
	if secs, err := strconv.Atoi(header); err == nil {
		return time.Duration(secs) * time.Second
	}

	// Try parsing as date
	if t, err := time.Parse(time.RFC1123, header); err == nil {
		return time.Until(t)
	}

	return 0
}
