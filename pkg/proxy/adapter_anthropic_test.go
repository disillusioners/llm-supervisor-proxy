package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy/translator"
)

func TestAnthropicAdapter_Protocol(t *testing.T) {
	adapter := NewAnthropicAdapter()
	if adapter.Protocol() != "anthropic" {
		t.Errorf("expected protocol 'anthropic', got '%s'", adapter.Protocol())
	}
}

func TestAnthropicAdapter_IsStream(t *testing.T) {
	adapter := NewAnthropicAdapter()

	tests := []struct {
		name     string
		body     map[string]interface{}
		expected bool
	}{
		{
			name:     "stream true",
			body:     map[string]interface{}{"stream": true},
			expected: true,
		},
		{
			name:     "stream false",
			body:     map[string]interface{}{"stream": false},
			expected: false,
		},
		{
			name:     "no stream field",
			body:     map[string]interface{}{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := adapter.IsStream(tt.body); got != tt.expected {
				t.Errorf("IsStream() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestAnthropicAdapter_ParseRequest(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantErr   bool
		wantModel string
		wantMeta  bool
	}{
		{
			name:      "valid request",
			body:      `{"model":"claude-3-sonnet","max_tokens":1024,"messages":[{"role":"user","content":"Hi"}]}`,
			wantModel: "claude-3-sonnet",
			wantMeta:  true,
		},
		{
			name:      "with stream",
			body:      `{"model":"claude-3-sonnet","max_tokens":1024,"messages":[{"role":"user","content":"Hi"}],"stream":true}`,
			wantModel: "claude-3-sonnet",
			wantMeta:  true,
		},
		{
			name:      "with temperature",
			body:      `{"model":"claude-3-sonnet","max_tokens":1024,"messages":[{"role":"user","content":"Hi"}],"temperature":0.7}`,
			wantModel: "claude-3-sonnet",
			wantMeta:  true,
		},
		{
			name:    "invalid json",
			body:    `not json`,
			wantErr: true,
		},
		{
			name:    "missing model",
			body:    `{"max_tokens":1024,"messages":[{"role":"user","content":"Hi"}]}`,
			wantErr: true,
		},
		{
			name:    "missing max_tokens",
			body:    `{"model":"claude-3-sonnet","messages":[{"role":"user","content":"Hi"}]}`,
			wantErr: true,
		},
		{
			name:    "missing messages",
			body:    `{"model":"claude-3-sonnet","max_tokens":1024}`,
			wantErr: true,
		},
		{
			name:    "invalid role",
			body:    `{"model":"claude-3-sonnet","max_tokens":1024,"messages":[{"role":"system","content":"Hi"}]}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter := NewAnthropicAdapter()
			req := httptest.NewRequest("POST", "/", strings.NewReader(tt.body))

			_, meta, err := adapter.ParseRequest(req)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseRequest() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && tt.wantMeta {
				if meta.ClientModel != tt.wantModel {
					t.Errorf("expected model '%s', got '%s'", tt.wantModel, meta.ClientModel)
				}
			}
		})
	}
}

func TestAnthropicAdapter_ExtractUpstreamModel(t *testing.T) {
	adapter := NewAnthropicAdapter()

	body := map[string]interface{}{"model": "claude-3-sonnet"}
	model := adapter.ExtractUpstreamModel(body, nil)
	if model != "claude-3-sonnet" {
		t.Errorf("expected 'claude-3-sonnet', got '%s'", model)
	}
}

func TestValidateAnthropicAdapterRequest(t *testing.T) {
	tests := []struct {
		name    string
		req     map[string]interface{}
		wantErr bool
	}{
		{
			name: "valid request",
			req: map[string]interface{}{
				"model":      "claude-3-sonnet",
				"max_tokens": 1024,
				"messages": []interface{}{
					map[string]interface{}{"role": "user", "content": "Hi"},
				},
			},
			wantErr: false,
		},
		{
			name: "missing model",
			req: map[string]interface{}{
				"max_tokens": 1024,
				"messages":   []interface{}{},
			},
			wantErr: true,
		},
		{
			name: "missing max_tokens",
			req: map[string]interface{}{
				"model":    "claude-3-sonnet",
				"messages": []interface{}{},
			},
			wantErr: true,
		},
		{
			name: "missing messages",
			req: map[string]interface{}{
				"model":      "claude-3-sonnet",
				"max_tokens": 1024,
			},
			wantErr: true,
		},
		{
			name: "invalid role",
			req: map[string]interface{}{
				"model":      "claude-3-sonnet",
				"max_tokens": 1024,
				"messages": []interface{}{
					map[string]interface{}{"role": "system", "content": "Hi"},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			anthropicReq := parseAnthropicReq(tt.req)
			err := validateAnthropicAdapterRequest(anthropicReq)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateAnthropicAdapterRequest() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestConvertAnthropicMessagesToStoreAdapter(t *testing.T) {
	tests := []struct {
		name     string
		messages []translator.AnthropicMessage
		expected int
	}{
		{
			name: "simple string content",
			messages: []translator.AnthropicMessage{
				{Role: "user", Content: "Hello"},
			},
			expected: 1,
		},
		{
			name: "array content with text",
			messages: []translator.AnthropicMessage{
				{Role: "user", Content: []interface{}{
					map[string]interface{}{"type": "text", "text": "Hello"},
				}},
			},
			expected: 1,
		},
		{
			name: "array content with multiple texts",
			messages: []translator.AnthropicMessage{
				{Role: "user", Content: []interface{}{
					map[string]interface{}{"type": "text", "text": "Hello"},
					map[string]interface{}{"type": "text", "text": " World"},
				}},
			},
			expected: 1,
		},
		{
			name: "mixed content blocks",
			messages: []translator.AnthropicMessage{
				{Role: "user", Content: []interface{}{
					map[string]interface{}{"type": "text", "text": "Hello"},
					map[string]interface{}{"type": "image", "source": map[string]interface{}{"type": "base64"}},
				}},
			},
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertAnthropicMessagesToStoreAdapter(tt.messages)
			if len(result) != tt.expected {
				t.Errorf("expected %d messages, got %d", tt.expected, len(result))
			}
		})
	}
}

func parseAnthropicReq(m map[string]interface{}) *translator.AnthropicRequest {
	bodyBytes, _ := json.Marshal(m)
	var req translator.AnthropicRequest
	json.Unmarshal(bodyBytes, &req)
	return &req
}

func TestGetAnthropicModelMapping(t *testing.T) {
	mapping := getAnthropicModelMapping(nil)
	if mapping == nil {
		t.Error("expected non-nil mapping")
	}
	// Unknown models should pass through
	if mapping.GetMappedModel("unknown-model") != "unknown-model" {
		t.Error("expected unknown model to pass through")
	}
}

func TestAnthropicAdapter_WriteNonStreamResponse(t *testing.T) {
	adapter := NewAnthropicAdapter()

	openaiResp := `{"choices":[{"message":{"role":"assistant","content":"Hello"}}]}`
	rec := httptest.NewRecorder()

	err := adapter.WriteNonStreamResponse(rec, []byte(openaiResp))
	if err != nil {
		t.Fatalf("WriteNonStreamResponse() error = %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["type"] != "message" {
		t.Error("expected type 'message' in response")
	}
}

func TestAnthropicAdapter_WriteStreamEvent(t *testing.T) {
	adapter := NewAnthropicAdapter()

	rec := httptest.NewRecorder()
	chunk := []byte(`{"choices":[{"delta":{"content":"Hello"}}]}`)

	err := adapter.WriteStreamEvent(rec, chunk)
	if err == nil {
		t.Error("expected error for streaming (requires buffered translation)")
	}
}

func TestAnthropicAdapter_WriteStreamDone(t *testing.T) {
	adapter := NewAnthropicAdapter()

	rec := httptest.NewRecorder()

	err := adapter.WriteStreamDone(rec)
	if err != nil {
		t.Fatalf("WriteStreamDone() error = %v", err)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "event: message_stop") {
		t.Error("expected 'event: message_stop' in response")
	}
}

func TestAnthropicAdapter_SetStreamHeaders(t *testing.T) {
	adapter := NewAnthropicAdapter()

	rec := httptest.NewRecorder()

	adapter.SetStreamHeaders(rec)

	if rec.Header().Get("Content-Type") != "text/event-stream" {
		t.Error("expected Content-Type: text/event-stream")
	}
	if rec.Header().Get("Cache-Control") != "no-cache" {
		t.Error("expected Cache-Control: no-cache")
	}
}

func TestAnthropicAdapter_WriteError(t *testing.T) {
	adapter := NewAnthropicAdapter()

	rec := httptest.NewRecorder()

	adapter.WriteError(rec, "invalid_request_error", "Invalid request", 400)

	if rec.Code != 400 {
		t.Errorf("expected status 400, got %d", rec.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["type"] != "error" {
		t.Error("expected type 'error' in response")
	}
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok {
		t.Fatal("expected error object in response")
	}
	if errObj["type"] != "invalid_request_error" {
		t.Errorf("expected error type 'invalid_request_error', got '%v'", errObj["type"])
	}
}

func TestAnthropicAdapter_WriteStreamError(t *testing.T) {
	adapter := NewAnthropicAdapter()

	rec := httptest.NewRecorder()

	adapter.WriteStreamError(rec, "rate_limit_error", "Rate limit exceeded")

	body := rec.Body.String()
	if !strings.Contains(body, "event: error") {
		t.Error("expected 'event: error' in response")
	}

	lines := strings.Split(body, "\n")
	var dataLine string
	for _, line := range lines {
		if strings.HasPrefix(line, "data: ") {
			dataLine = strings.TrimPrefix(line, "data: ")
			break
		}
	}
	if dataLine == "" {
		t.Fatal("expected 'data: {...}' line in response")
	}

	var errResp map[string]interface{}
	if err := json.Unmarshal([]byte(dataLine), &errResp); err != nil {
		t.Fatalf("failed to parse error response JSON: %v", err)
	}

	errObj, ok := errResp["error"].(map[string]interface{})
	if !ok {
		t.Fatal("expected 'error' object in response")
	}
	if errObj["type"] != "rate_limit_error" {
		t.Errorf("expected type 'rate_limit_error', got '%v'", errObj["type"])
	}
	if errObj["message"] != "Rate limit exceeded" {
		t.Errorf("expected message 'Rate limit exceeded', got '%v'", errObj["message"])
	}
}

func TestAnthropicAdapter_WriteStreamErrorWithCode(t *testing.T) {
	adapter := NewAnthropicAdapter()

	rec := httptest.NewRecorder()

	adapter.WriteStreamErrorWithCode(rec, "invalid_request_error", "INVALID_MODEL", "Model not found")

	body := rec.Body.String()
	if !strings.Contains(body, "event: error") {
		t.Error("expected 'event: error' in response")
	}

	lines := strings.Split(body, "\n")
	var dataLine string
	for _, line := range lines {
		if strings.HasPrefix(line, "data: ") {
			dataLine = strings.TrimPrefix(line, "data: ")
			break
		}
	}
	if dataLine == "" {
		t.Fatal("expected 'data: {...}' line in response")
	}

	var errResp map[string]interface{}
	if err := json.Unmarshal([]byte(dataLine), &errResp); err != nil {
		t.Fatalf("failed to parse error response JSON: %v", err)
	}

	errObj, ok := errResp["error"].(map[string]interface{})
	if !ok {
		t.Fatal("expected 'error' object in response")
	}
	if errObj["type"] != "invalid_request_error" {
		t.Errorf("expected type 'invalid_request_error', got '%v'", errObj["type"])
	}
	if errObj["message"] != "Model not found" {
		t.Errorf("expected message 'Model not found', got '%v'", errObj["message"])
	}
	if errObj["code"] != "INVALID_MODEL" {
		t.Errorf("expected code 'INVALID_MODEL', got '%v'", errObj["code"])
	}
}

func TestAnthropicAdapter_WriteErrorWithCode(t *testing.T) {
	adapter := NewAnthropicAdapter()

	rec := httptest.NewRecorder()

	adapter.WriteErrorWithCode(rec, "rate_limit_error", "RATE_LIMIT", "Too many requests", 429)

	if rec.Code != 429 {
		t.Errorf("expected status 429, got %d", rec.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok {
		t.Fatal("expected error object in response")
	}
	if errObj["code"] != "RATE_LIMIT" {
		t.Errorf("expected code 'RATE_LIMIT', got '%v'", errObj["code"])
	}
}

func TestAnthropicAdapter_ToUpstreamRequest(t *testing.T) {
	adapter := NewAnthropicAdapter()

	body := map[string]interface{}{
		"model":      "claude-3-sonnet",
		"max_tokens": 1024,
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "Hello"},
		},
	}

	result, err := adapter.ToUpstreamRequest(body, nil)
	if err != nil {
		t.Fatalf("ToUpstreamRequest() error = %v", err)
	}

	var parsed map[string]interface{}
	json.Unmarshal(result, &parsed)

	// Should be converted to OpenAI format
	if parsed["model"] == nil {
		t.Error("expected model in converted request")
	}
}

func TestAnthropicAdapter_ToStoreMessages(t *testing.T) {
	adapter := NewAnthropicAdapter()

	body := map[string]interface{}{
		"model":      "claude-3-sonnet",
		"max_tokens": 1024,
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "Hello"},
		},
	}

	messages := adapter.ToStoreMessages(body)
	if len(messages) == 0 {
		t.Error("expected non-empty messages")
	}
}

func TestAnthropicAdapter_ToStoreMessages_WithSystem(t *testing.T) {
	adapter := NewAnthropicAdapter()

	body := map[string]interface{}{
		"model":      "claude-3-sonnet",
		"max_tokens": 1024,
		"system":     "You are helpful",
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "Hello"},
		},
	}

	messages := adapter.ToStoreMessages(body)
	if len(messages) != 2 {
		t.Errorf("expected 2 messages (system + user), got %d", len(messages))
	}
	if messages[0].Role != "system" {
		t.Errorf("expected first message role 'system', got '%s'", messages[0].Role)
	}
}
