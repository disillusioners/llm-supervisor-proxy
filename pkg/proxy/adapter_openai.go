package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
)

// ─────────────────────────────────────────────────────────────────────────────
// OpenAIAdapter - Passthrough adapter for OpenAI-compatible clients
// ─────────────────────────────────────────────────────────────────────────────

// OpenAIAdapter handles OpenAI-compatible requests without translation.
// It passes requests through to upstream as-is.
type OpenAIAdapter struct {
	extractor ResponseExtractor
}

// NewOpenAIAdapter creates a new OpenAI adapter.
func NewOpenAIAdapter() *OpenAIAdapter {
	return &OpenAIAdapter{
		extractor: ResponseExtractor{},
	}
}

func (a *OpenAIAdapter) Protocol() string {
	return "openai"
}

func (a *OpenAIAdapter) ParseRequest(r *http.Request) (map[string]interface{}, *RequestMetadata, error) {
	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read request body")
	}

	var body map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		return nil, nil, fmt.Errorf("invalid JSON body")
	}

	model, _ := body["model"].(string)
	isStream := false
	if s, ok := body["stream"].(bool); ok && s {
		isStream = true
	}

	// Extract parameters (exclude standard fields)
	params := extractOpenAIParameters(body)

	meta := &RequestMetadata{
		ClientModel:   model,
		UpstreamModel: model, // No mapping needed for OpenAI
		IsStream:      isStream,
		Parameters:    params,
	}

	return body, meta, nil
}

func (a *OpenAIAdapter) ToUpstreamRequest(body map[string]interface{}, _ models.ModelsConfigInterface) ([]byte, error) {
	// OpenAI requests pass through unchanged
	return json.Marshal(body)
}

func (a *OpenAIAdapter) ToStoreMessages(body map[string]interface{}) []store.Message {
	return parseOpenAIMessages(body)
}

func (a *OpenAIAdapter) ExtractUpstreamModel(body map[string]interface{}, _ models.ModelsConfigInterface) string {
	model, _ := body["model"].(string)
	return model
}

func (a *OpenAIAdapter) IsStream(body map[string]interface{}) bool {
	if s, ok := body["stream"].(bool); ok && s {
		return true
	}
	return false
}

// ─────────────────────────────────────────────────────────────────────────────
// ResponseWriter implementation
// ─────────────────────────────────────────────────────────────────────────────

func (a *OpenAIAdapter) WriteNonStreamResponse(w http.ResponseWriter, openaiResponse []byte) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(openaiResponse)
	return err
}

func (a *OpenAIAdapter) WriteBufferedStream(w http.ResponseWriter, openaiBuffer []byte) error {
	// OpenAI format: passthrough the entire buffer as-is
	_, err := w.Write(openaiBuffer)
	if err != nil {
		return err
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}

func (a *OpenAIAdapter) SetStreamHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
}

// ─────────────────────────────────────────────────────────────────────────────
// ErrorWriter implementation
// ─────────────────────────────────────────────────────────────────────────────

func (a *OpenAIAdapter) WriteError(w http.ResponseWriter, errorType, message string, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]string{
			"type":    errorType,
			"message": message,
		},
	})
}

func (a *OpenAIAdapter) WriteStreamError(w http.ResponseWriter, errorType, message string) {
	errorResp, _ := json.Marshal(map[string]interface{}{
		"error": map[string]string{
			"type":    errorType,
			"message": message,
		},
	})
	fmt.Fprintf(w, "data: %s\n\n", string(errorResp))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}
