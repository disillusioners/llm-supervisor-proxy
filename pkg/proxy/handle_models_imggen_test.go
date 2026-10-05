package proxy

// ImgGen Models commission / T1.3.1 + T1.7.3 — /v1/models
// contract test: image-gen models are excluded from the
// OpenAI-compatible list returned by HandleModels (Architect
// Amendment 19 matrix). Uses a tiny stub for ModelsConfig that
// returns a prebuilt list of models — the standard
// models.ModelsConfig.AddModel path runs full validation (which
// requires the credential IDs to exist in the configured
// credentials set; a unit-test ModelsConfig has none), so the
// test stub short-circuits GetModels / GetEnabledModels to
// return the test models directly.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// stubModelsConfig is a minimal models.ModelsConfigInterface
// stub: GetModels + GetEnabledModels return a prebuilt list;
// the other methods are no-ops (the /v1/models handler only
// touches GetEnabledModels).
type stubModelsConfig struct {
	enabled []models.ModelConfig
	all     []models.ModelConfig
}

func (s *stubModelsConfig) GetModels() []models.ModelConfig {
	out := make([]models.ModelConfig, len(s.all))
	copy(out, s.all)
	return out
}
func (s *stubModelsConfig) GetEnabledModels() []models.ModelConfig {
	out := make([]models.ModelConfig, len(s.enabled))
	copy(out, s.enabled)
	return out
}
func (s *stubModelsConfig) GetModel(string) *models.ModelConfig   { return nil }
func (s *stubModelsConfig) GetModelByName(string) *models.ModelConfig {
	return nil
}
func (s *stubModelsConfig) GetTruncateParams(string) []string  { return nil }
func (s *stubModelsConfig) GetFallbackChain(string) []string    { return nil }
func (s *stubModelsConfig) AddModel(models.ModelConfig) error   { return nil }
func (s *stubModelsConfig) UpdateModel(string, models.ModelConfig) error {
	return nil
}
func (s *stubModelsConfig) RemoveModel(string) error { return nil }
func (s *stubModelsConfig) Save() error              { return nil }
func (s *stubModelsConfig) Validate() error          { return nil }
func (s *stubModelsConfig) GetCredential(string) *models.CredentialConfig {
	return nil
}
func (s *stubModelsConfig) GetCredentials() []models.CredentialConfig { return nil }
func (s *stubModelsConfig) AddCredential(models.CredentialConfig) error {
	return nil
}
func (s *stubModelsConfig) UpdateCredential(string, models.CredentialConfig) error {
	return nil
}
func (s *stubModelsConfig) RemoveCredential(string) error  { return nil }
func (s *stubModelsConfig) ResolveInternalConfig(string) (string, string, string, string, bool) {
	return "", "", "", "", false
}
func (s *stubModelsConfig) ResolveInternalConfigWithAffinity(string, string) (models.ResolvedCredential, bool) {
	return models.ResolvedCredential{}, false
}

// newImgGenFilterHarness constructs a handler + a stub config
// that returns the given enabled models from GetEnabledModels.
func newImgGenFilterHarness(t *testing.T, enabled []models.ModelConfig) (*Handler, *httptest.Server) {
	t.Helper()
	all := make([]models.ModelConfig, len(enabled))
	copy(all, enabled)
	stub := &stubModelsConfig{enabled: enabled, all: all}
	// upstreamHandler is unused — HandleModels doesn't talk
	// upstream. newTestHandler still needs a non-nil handler.
	h, upstream := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {}, stub)
	return h, upstream
}

func TestHandleModels_FilterImageGen(t *testing.T) {
	h, _ := newImgGenFilterHarness(t, []models.ModelConfig{
		{ID: "chat-1", Name: "Chat 1", Enabled: true, Internal: true, InternalModel: "x"},
		{ID: "img-1", Name: "Image 1", Enabled: true, Internal: true, Kind: models.KindImageGen, InternalModel: "image-01"},
		{ID: "chat-2", Name: "Chat 2", Enabled: true, Internal: true, InternalModel: "y"},
		{ID: "img-2", Name: "Image 2", Enabled: false, Internal: true, Kind: models.KindImageGen, InternalModel: "image-02"},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	h.HandleModels(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Object string                   `json:"object"`
		Data   []map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rec.Body.String())
	}

	got := make(map[string]bool)
	for _, m := range body.Data {
		if id, ok := m["id"].(string); ok {
			got[id] = true
		}
	}
	// (a) enabled image + enabled chat ⇒ only chat listed.
	if !got["chat-1"] || !got["chat-2"] {
		t.Errorf("expected chat-1 and chat-2 in /v1/models; got=%v", got)
	}
	if got["img-1"] {
		t.Errorf("img-1 (enabled image-gen) leaked into /v1/models; got=%v", got)
	}
	if got["img-2"] {
		t.Errorf("img-2 (disabled image-gen) leaked into /v1/models; got=%v", got)
	}
}

// TestHandleModels_OnlyImageGen_EmptyData — when EVERY enabled
// model is image-gen, /v1/models returns data:[] (200, not 404).
func TestHandleModels_OnlyImageGen_EmptyData(t *testing.T) {
	h, _ := newImgGenFilterHarness(t, []models.ModelConfig{
		{ID: "img-1", Name: "Image 1", Enabled: true, Internal: true, Kind: models.KindImageGen, InternalModel: "image-01"},
		{ID: "img-2", Name: "Image 2", Enabled: true, Internal: true, Kind: models.KindImageGen, InternalModel: "image-02"},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	h.HandleModels(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (empty data, not 404)", rec.Code)
	}
	var body struct {
		Object string                   `json:"object"`
		Data   []map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rec.Body.String())
	}
	if len(body.Data) != 0 {
		t.Errorf("data should be empty when only image-gen is enabled, got=%v", body.Data)
	}
}

// TestHandleModels_MethodNotAllowed — POST to /v1/models is
// rejected with 405 (house style).
func TestHandleModels_MethodNotAllowed(t *testing.T) {
	h, _ := newImgGenFilterHarness(t, []models.ModelConfig{
		{ID: "chat-1", Name: "Chat 1", Enabled: true, Internal: true, InternalModel: "x"},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/models", nil)
	h.HandleModels(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}
