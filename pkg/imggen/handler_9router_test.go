package imggen

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/usage"
)

func b64JPEG() string {
	return base64.StdEncoding.EncodeToString([]byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46})
}

func newEnv9Router(t *testing.T, script fakeScript) *handlerTestEnv {
	t.Helper()
	upstream, fake := newFakeMiniMax(t, script)
	db, _ := newUsageTestDB(t)
	uc := usage.NewCounter(db, database.SQLite)
	bus := events.NewBus()
	ts := newMemTokenStore()

	credBaseURL := upstream.URL
	h := NewHandler(stubConfig{timeout: 200 * time.Millisecond}, bus, ts, uc)
	modelID := "9router-image-01"
	h.SetModelResolver(func(id string) (*models.ModelConfig, bool) {
		if id != modelID {
			return nil, false
		}
		return &models.ModelConfig{
			ID:              modelID,
			Name:            "Image Gen 9Router",
			Kind:            models.KindImageGen,
			Enabled:         true,
			Internal:        true,
			InternalModel:   "ag/gemini-3.8-flash",
			InternalBaseURL: credBaseURL,
			Credentials: []models.CredentialRef{
				{CredentialID: "cred-1", Weight: 1, Position: 0},
			},
		}, true
	})
	h.SetCredentialResolver(func(id string) (models.ResolvedCredential, bool) {
		if id != modelID {
			return models.ResolvedCredential{}, false
		}
		return models.ResolvedCredential{
			Provider:      "9router",
			APIKey:        "sk-cred-secret",
			BaseURL:       credBaseURL,
			InternalModel: "ag/gemini-3.8-flash",
		}, true
	})
	tok := &auth.AuthToken{
		ID:            "tok-test",
		Name:          "Test Token",
		TokenHash:     auth.HashToken("sk-test-token"),
		AllowedModels: nil,
	}
	ts.add(tok)
	return &handlerTestEnv{
		t:          t,
		upstream:   upstream,
		fake:       fake,
		handler:    h,
		bus:        bus,
		usage:      uc,
		tokenStore: ts,
		modelID:    modelID,
	}
}

// TestHandler_9Router_ConversionRelay — the upstream returns an
// OpenAI-images body (b64_json); the client receives the converted
// body with data[].image_urls as a data-URI.
func TestHandler_9Router_ConversionRelay(t *testing.T) {
	body := map[string]interface{}{
		"created": 7,
		"data":    []interface{}{map[string]interface{}{"b64_json": b64JPEG()}},
	}
	raw, _ := json.Marshal(body)
	env := newEnv9Router(t, fakeScript{status: 200, body: raw})

	rec := env.sendRequest(http.MethodPost, `{"model":"`+env.modelID+`","prompt":"a cat"}`, nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	data := out["data"].([]interface{})
	item := data[0].(map[string]interface{})
	urls := item["image_urls"].([]interface{})
	if len(urls) != 1 {
		t.Fatalf("image_urls len = %d, want 1", len(urls))
	}
	uri := urls[0].(string)
	if !strings.HasPrefix(uri, "data:image/jpeg;base64,") {
		t.Errorf("uri = %q, want data:image/jpeg;base64,", uri[:40])
	}
	_ = bytes.Equal
}

// TestHandler_9Router_429Quota — upstream returns 429 with a nested
// JSON message; the client sees 429 (not 502) and the normalized body.
func TestHandler_9Router_429Quota(t *testing.T) {
	inner := `{"error":{"code":429,"message":"You have exhausted your capacity on this model. Your quota will reset after 4h39m9s.","status":"RESOURCE_EXHAUSTED"}}`
	outerMsg := "[antigravity/gemini-3.8-flash] [429]: " + inner
	body, _ := json.Marshal(map[string]interface{}{
		"error": map[string]interface{}{
			"message": outerMsg,
			"type":    "invalid_request_error",
		},
	})
	env := newEnv9Router(t, fakeScript{status: 429, body: body})

	rec := env.sendRequest(http.MethodPost, `{"model":"`+env.modelID+`","prompt":"x"}`, nil)
	if rec.Code != 429 {
		t.Fatalf("status = %d, want 429; body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	em := out["error"].(map[string]interface{})
	if em["message"] != "You have exhausted your capacity on this model. Your quota will reset after 4h39m9s." {
		t.Errorf("message = %v, want unwrapped quota text", em["message"])
	}
}

// TestHandler_Minimax_ByteIdentical — minimax path is untouched: a
// success body is relayed byte-for-byte (no hook runs).
func TestHandler_Minimax_ByteIdenticalStill(t *testing.T) {
	body := successBody(1, 0)
	env := newEnv(t, fakeScript{status: 200, body: body})
	rec := env.sendRequest(http.MethodPost, `{"model":"`+env.modelID+`","prompt":"a cat"}`, nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !bytes.Equal(rec.Body.Bytes(), body) {
		t.Errorf("minimax body not byte-identical")
	}
}
