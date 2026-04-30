package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
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

func (a *AnthropicAdapter) ToUpstreamRequest(body map[string]interface{}, modelMapping models.ModelsConfigInterface) ([]byte, error) {
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
	mapping := getAnthropicModelMapping(modelMapping)
	openaiReq := translator.TranslateRequest(&anthropicReq, mapping)

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

func (a *AnthropicAdapter) ExtractUpstreamModel(body map[string]interface{}, modelMapping models.ModelsConfigInterface) string {
	model, _ := body["model"].(string)
	mapping := getAnthropicModelMapping(modelMapping)
	return mapping.GetMappedModel(model)
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
	// For streaming, we need to translate each OpenAI chunk to Anthropic format
	// This is more complex and handled by the stream translator
	// For now, we'll use the buffered approach
	return fmt.Errorf("streaming requires buffered translation - use BufferedStreamTranslator")
}

func (a *AnthropicAdapter) WriteStreamDone(w http.ResponseWriter) error {
	// Anthropic uses message_stop event
	fmt.Fprintf(w, "event: message_stop\ndata: {}\n\n")
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}

func (a *AnthropicAdapter) SetStreamHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
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

// getAnthropicModelMapping extracts model mapping from config
func getAnthropicModelMapping(modelsConfig models.ModelsConfigInterface) *translator.ModelMappingConfig {
	mapping := make(map[string]string)
	if modelsConfig != nil {
		for _, model := range modelsConfig.GetModels() {
			if model.ID != "" && model.Name != "" && model.Name != model.ID {
				mapping[model.Name] = model.ID
			}
		}
	}
	// Return mapping config without default - unknown models pass through unchanged
	return &translator.ModelMappingConfig{
		Mapping: mapping,
	}
}
