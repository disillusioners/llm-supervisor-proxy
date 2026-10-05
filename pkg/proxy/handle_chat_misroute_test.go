package proxy

// ImgGen Models commission / T1.3.3 + T1.7.3 — chat-misroute
// early-reject test. A chat completion request naming an
// image-gen model is rejected with OpenAI-envelope 400 BEFORE
// any upstream call, auth-race, or metering work. The fake
// upstream's hit counter must stay at 0; the chat-path's request
// store (if any) is never written for the misroute.
//
// CRITICAL — unskippable (Leader Ruling L1 + Architect Amendment
// 4): without this guard a chat request to an image-gen model
// would burn real upstream spend (race coordinator → MiniMax
// with model=image-01 → upstream fail) AND pollute
// model_hourly_usage.request_count.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// TestHandleChatCompletions_RejectsImageGenMisroute — chat
// request naming an image-gen model ⇒ 400 + zero upstream calls.
func TestHandleChatCompletions_RejectsImageGenMisroute(t *testing.T) {
	stub := &models.ModelsConfig{}
	credCfg := models.CredentialConfig{ID: "minimax-cred", Provider: "minimax", APIKey: "k"}
	if err := stub.AddCredential(credCfg); err != nil {
		t.Fatalf("AddCredential: %v", err)
	}
	cred := []models.CredentialRef{{CredentialID: "minimax-cred", Weight: 1, Position: 0}}
	if err := stub.AddModel(models.ModelConfig{
		ID: "img-1", Name: "Image 1", Enabled: true, Internal: true,
		Kind: models.KindImageGen, InternalModel: "image-01",
		Credentials: cred,
	}); err != nil {
		t.Fatalf("AddModel: %v", err)
	}

	var upstreamHits int
	h, _ := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		upstreamHits++
		// This should NEVER run for the misroute case.
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"x"}}]}`))
	}, stub)

	rec := httptest.NewRecorder()
	body := `{"model":"img-1","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.HandleChatCompletions(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (OpenAI envelope)", rec.Code)
	}
	if upstreamHits != 0 {
		t.Errorf("upstream was called for a chat→image-gen misroute; hit count = %d", upstreamHits)
	}
	// Verify the body is the OpenAI envelope.
	var parsed map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("body not JSON: %v; body=%s", err, rec.Body.String())
	}
	errObj, ok := parsed["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("body has no 'error' object: %s", rec.Body.String())
	}
	msg, _ := errObj["message"].(string)
	if !strings.Contains(msg, "/v1/image_generation") {
		t.Errorf("error message = %q, want it to mention /v1/image_generation", msg)
	}
}

// TestHandleChatCompletions_ChatModelUnaffected — sanity: a
// chat model on the chat path still passes the early-reject
// (the guard only fires on IsImageGen, not on every model).
func TestHandleChatCompletions_ChatModelUnaffected(t *testing.T) {
	stub := &models.ModelsConfig{}
	credCfg := models.CredentialConfig{ID: "openai-cred", Provider: "openai", APIKey: "k"}
	if err := stub.AddCredential(credCfg); err != nil {
		t.Fatalf("AddCredential: %v", err)
	}
	cred := []models.CredentialRef{{CredentialID: "openai-cred", Weight: 1, Position: 0}}
	if err := stub.AddModel(models.ModelConfig{
		ID: "chat-1", Name: "Chat 1", Enabled: true, Internal: true,
		InternalModel: "gpt-4", Credentials: cred,
	}); err != nil {
		t.Fatalf("AddModel: %v", err)
	}

	var upstreamHits int
	h, _ := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		upstreamHits++
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"x"}}]}`))
	}, stub)

	rec := httptest.NewRecorder()
	body := `{"model":"chat-1","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.HandleChatCompletions(rec, req)

	// We don't assert the status code here (the chat path
	// has many sub-paths; the test only asserts the
	// early-reject does NOT fire for chat models — the
	// upstream is reached).
	if rec.Code == http.StatusBadRequest {
		body := rec.Body.String()
		if strings.Contains(body, "/v1/image_generation") {
			t.Errorf("chat model triggered image-gen early-reject; body=%s", body)
		}
	}
}

