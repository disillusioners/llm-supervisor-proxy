package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy/translator"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
)

// ─────────────────────────────────────────────────────────────────────────────
// AnthropicAdapter - Translates between Anthropic and OpenAI formats
// ─────────────────────────────────────────────────────────────────────────────

// AnthropicAdapter handles Anthropic Messages API requests by translating
// to/from OpenAI format for upstream.
type AnthropicAdapter struct {
	extractor     ResponseExtractor
	originalModel string // model name from the incoming Anthropic request
}

// NewAnthropicAdapter creates a new Anthropic adapter.
func NewAnthropicAdapter() *AnthropicAdapter {
	return &AnthropicAdapter{
		extractor: ResponseExtractor{},
	}
}

func (a *AnthropicAdapter) Protocol() string {
	return "anthropic"
}

func (a *AnthropicAdapter) ParseRequest(r *http.Request) (map[string]interface{}, *RequestMetadata, error) {
	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read request body")
	}

	var anthropicReq translator.AnthropicRequest
	if err := json.Unmarshal(bodyBytes, &anthropicReq); err != nil {
		return nil, nil, fmt.Errorf("invalid JSON body")
	}

	// Capture original model name for response translation
	a.originalModel = anthropicReq.Model

	// Validate request
	if err := validateAnthropicAdapterRequest(&anthropicReq); err != nil {
		return nil, nil, err
	}

	// Convert to map for internal use
	var body map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		return nil, nil, fmt.Errorf("failed to parse request body")
	}

	params := make(map[string]interface{})
	params["max_tokens"] = anthropicReq.MaxTokens
	if anthropicReq.Temperature != nil {
		params["temperature"] = *anthropicReq.Temperature
	}
	if anthropicReq.TopP != nil {
		params["top_p"] = *anthropicReq.TopP
	}
	if len(anthropicReq.StopSequences) > 0 {
		params["stop_sequences"] = anthropicReq.StopSequences
	}
	params["endpoint"] = "anthropic"

	meta := &RequestMetadata{
		ClientModel:   anthropicReq.Model,
		UpstreamModel: anthropicReq.Model, // Will be mapped in ToUpstreamRequest
		IsStream:      anthropicReq.Stream,
		Parameters:    params,
	}

	return body, meta, nil
}

func (a *AnthropicAdapter) ToUpstreamRequest(body map[string]interface{}) ([]byte, error) {
	// Convert map back to AnthropicRequest for translation
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	var anthropicReq translator.AnthropicRequest
	if err := json.Unmarshal(bodyBytes, &anthropicReq); err != nil {
		return nil, fmt.Errorf("failed to parse Anthropic request: %w", err)
	}

	// Translate to OpenAI format
	openaiReq := translator.TranslateRequest(&anthropicReq, nil)

	return json.Marshal(openaiReq)
}

func (a *AnthropicAdapter) ToStoreMessages(body map[string]interface{}) []store.Message {
	// Convert map to AnthropicRequest
	bodyBytes, _ := json.Marshal(body)
	var anthropicReq translator.AnthropicRequest
	if err := json.Unmarshal(bodyBytes, &anthropicReq); err != nil {
		return nil
	}

	// Convert Anthropic messages to store format
	messages := convertAnthropicMessagesToStoreAdapter(anthropicReq.Messages)

	// Add system message if present
	if anthropicReq.System != nil {
		systemContent := translator.TranslateSystem(anthropicReq.System)
		if systemContent != "" {
			messages = append([]store.Message{{Role: "system", Content: systemContent}}, messages...)
		}
	}

	return messages
}

func (a *AnthropicAdapter) ExtractUpstreamModel(body map[string]interface{}) string {
	model, _ := body["model"].(string)
	return model
}

func (a *AnthropicAdapter) IsStream(body map[string]interface{}) bool {
	if s, ok := body["stream"].(bool); ok && s {
		return true
	}
	return false
}

// ─────────────────────────────────────────────────────────────────────────────
// ResponseWriter implementation
// ─────────────────────────────────────────────────────────────────────────────

func (a *AnthropicAdapter) WriteNonStreamResponse(w http.ResponseWriter, openaiResponse []byte) error {
	// Translate OpenAI response to Anthropic format
	anthropicResp, err := translator.TranslateNonStreamResponse(openaiResponse, a.originalModel)
	if err != nil {
		return fmt.Errorf("failed to translate response: %w", err)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, err = w.Write(anthropicResp)
	return err
}

func (a *AnthropicAdapter) WriteStreamEvent(w http.ResponseWriter, openaiChunk []byte) error {
	// Buffered mode: legacy path. The recorder-based handler at
	// handleAnthropicInternalStreamResponse feeds the body into
	// translator.TranslateBufferedStream, so per-chunk translation
	// is unnecessary here. The error message is preserved verbatim
	// from the legacy behavior (any caller that hits this path is
	// relying on buffered translation).
	return fmt.Errorf("streaming requires buffered translation - use BufferedStreamTranslator")
}

// WriteStreamEventLive is the live-mode counterpart to
// WriteStreamEvent. Called by the live Anthropic-streaming
// handler (handleAnthropicLiveStreamResponse at
// handler_anthropic.go) which owns the IncrementalStreamTranslator
// directly — the adapter only needs to forward each emitted
// Anthropic event to w and flush.
//
// The actual translation from OpenAI SSE → Anthropic SSE happens
// upstream (the handler drives translator.IncrementalStreamTranslator
// .ProcessChunk per line). This method is the seam where the
// adapter's WriteStreamEvent contract would otherwise return the
// "buffered translation required" error — for the live path the
// caller is the handler, not the adapter, so this method is
// unused at runtime today, but the seam is documented here for
// symmetry and future refactors.
func (a *AnthropicAdapter) WriteStreamEventLive(w http.ResponseWriter, anthropicEvent string) error {
	if _, err := fmt.Fprint(w, anthropicEvent); err != nil {
		return err
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}

func (a *AnthropicAdapter) WriteStreamDone(w http.ResponseWriter) error {
	// Anthropic uses message_stop event
	fmt.Fprintf(w, "event: message_stop\ndata: {}\n\n")
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}

// SSE headers — `Cache-Control: no-cache, no-transform` is required
// to defeat Cloudflare's response buffering on long-lived streaming
// responses (otherwise CF sits on chunks until its 100/180s read
// timeout and disconnects the client mid-stream with a 524).
func (a *AnthropicAdapter) SetStreamHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
}

// ─────────────────────────────────────────────────────────────────────────────
// ErrorWriter implementation
// ─────────────────────────────────────────────────────────────────────────────

func (a *AnthropicAdapter) WriteError(w http.ResponseWriter, errorType, message string, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	errorResp := map[string]interface{}{
		"type": "error",
		"error": map[string]interface{}{
			"type":    errorType,
			"message": message,
		},
	}
	errBytes, err := json.Marshal(errorResp)
	if err != nil {
		log.Printf("failed to marshal error response: %v", err)
		return
	}
	w.Write(errBytes)
}

func (a *AnthropicAdapter) WriteStreamError(w http.ResponseWriter, errorType, message string) {
	a.WriteStreamErrorWithCode(w, errorType, "", message)
}

func (a *AnthropicAdapter) WriteStreamErrorWithCode(w http.ResponseWriter, errorType, code, message string) {
	// Emit message_stop before error to ensure proper stream termination per Anthropic SSE spec.
	// Anthropic clients expect the stream to end with message_stop; sending an error without it
	// can leave clients in a broken state waiting for stream completion.
	fmt.Fprintf(w, "event: message_stop\ndata: {}\n\n")

	errorEvent := map[string]interface{}{
		"type": "error",
		"error": map[string]interface{}{
			"type":    errorType,
			"message": message,
		},
	}
	if code != "" {
		errorEvent["error"].(map[string]interface{})["code"] = code
	}
	errBytes, err := json.Marshal(errorEvent)
	if err != nil {
		log.Printf("failed to marshal error response: %v", err)
		return
	}
	fmt.Fprintf(w, "event: error\ndata: %s\n\n", string(errBytes))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// WriteErrorWithCode sends a non-streaming error with optional code field
func (a *AnthropicAdapter) WriteErrorWithCode(w http.ResponseWriter, errorType, code, message string, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	errorResp := map[string]interface{}{
		"type": "error",
		"error": map[string]interface{}{
			"type":    errorType,
			"message": message,
		},
	}
	if code != "" {
		errorResp["error"].(map[string]interface{})["code"] = code
	}
	errBytes, err := json.Marshal(errorResp)
	if err != nil {
		log.Printf("failed to marshal error response: %v", err)
		return
	}
	w.Write(errBytes)
}

// ─────────────────────────────────────────────────────────────────────────────
// Helper functions
// ─────────────────────────────────────────────────────────────────────────────

// validateAnthropicAdapterRequest validates an Anthropic request for the adapter
func validateAnthropicAdapterRequest(req *translator.AnthropicRequest) error {
	if req.Model == "" {
		return fmt.Errorf("model is required")
	}
	if req.MaxTokens == 0 {
		return fmt.Errorf("max_tokens is required")
	}
	if len(req.Messages) == 0 {
		return fmt.Errorf("messages is required")
	}
	for _, msg := range req.Messages {
		if msg.Role != "user" && msg.Role != "assistant" {
			return fmt.Errorf("invalid role: %s (must be 'user' or 'assistant')", msg.Role)
		}
	}
	return nil
}

// convertAnthropicMessagesToStoreAdapter converts Anthropic messages to store format
func convertAnthropicMessagesToStoreAdapter(messages []translator.AnthropicMessage) []store.Message {
	var result []store.Message
	for _, msg := range messages {
		content := ""
		switch c := msg.Content.(type) {
		case string:
			content = c
		case []interface{}:
			// Extract text from content blocks
			var sb strings.Builder
			for _, block := range c {
				if bm, ok := block.(map[string]interface{}); ok {
					if t, ok := bm["text"].(string); ok {
						sb.WriteString(t)
					}
				}
			}
			content = sb.String()
		}
		result = append(result, store.Message{
			Role:    msg.Role,
			Content: content,
		})
	}
	return result
}


