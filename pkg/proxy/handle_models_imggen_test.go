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
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
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
func (s *stubModelsConfig) GetModel(string) *models.ModelConfig { return nil }
func (s *stubModelsConfig) GetModelByName(string) *models.ModelConfig {
	return nil
}
func (s *stubModelsConfig) GetTruncateParams(string) []string { return nil }
func (s *stubModelsConfig) GetFallbackChain(string) []string  { return nil }
func (s *stubModelsConfig) AddModel(models.ModelConfig) error { return nil }
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
func (s *stubModelsConfig) RemoveCredential(string) error { return nil }
func (s *stubModelsConfig) ResolveInternalConfig(string) (string, string, string, string, bool) {
	return "", "", "", "", false
}
func (s *stubModelsConfig) ResolveInternalConfigWithAffinity(string, string) (models.ResolvedCredential, bool) {
	return models.ResolvedCredential{}, false
}

func (s *stubModelsConfig) GetCredentialGroup(string) *models.CredentialGroupConfig { return nil }
func (s *stubModelsConfig) GetCredentialGroups() []models.CredentialGroupConfig     { return nil }
func (s *stubModelsConfig) AddCredentialGroup(models.CredentialGroupConfig) error   { return nil }
func (s *stubModelsConfig) UpdateCredentialGroup(string, models.CredentialGroupConfig) error {
	return nil
}
func (s *stubModelsConfig) RemoveCredentialGroup(string) error         { return nil }
func (s *stubModelsConfig) ModelsUsingCredentialGroup(string) []string { return nil }
func (s *stubModelsConfig) ModelsUsingCredentialGroupsAll() map[string][]string {
	return nil
}
func (s *stubModelsConfig) GroupsContainingCredential(string) []string { return nil }
func (s *stubModelsConfig) FlattenCredentialRefs(model *models.ModelConfig) []models.CredentialRef {
	if model == nil {
		return nil
	}
	return model.Credentials
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

// TestHandleModels_FilterDecision — Decision Models / §3.1
// site 6: decision models are excluded from /v1/models (served
// by /v1/decisions instead). The exclusion clause
// `|| m.IsDecision()` in HandleModels (pkg/proxy/handler.go)
// had no test coverage — a refactor dropping it would silently
// leak decision models into the OpenAI-compatible chat pickers.
// Sibling of TestHandleModels_FilterImageGen (same harness);
// the image-gen assertions above remain intact there.
func TestHandleModels_FilterDecision(t *testing.T) {
	h, _ := newImgGenFilterHarness(t, []models.ModelConfig{
		{ID: "chat-1", Name: "Chat 1", Enabled: true, Internal: true, InternalModel: "x"},
		{ID: "decision-1", Name: "Decision 1", Enabled: true, Kind: models.KindDecision, Internal: true, InternalModel: "decider-01"},
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
	if !got["chat-1"] {
		t.Errorf("expected chat-1 in /v1/models; got=%v", got)
	}
	if got["decision-1"] {
		t.Errorf("decision-1 (enabled decision model) leaked into /v1/models — the m.IsDecision() exclusion clause (site 6) has been dropped; got=%v", got)
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

// ─────────────────────────────────────────────────────────────────────────────
// decision-groups commission — the /v1/models filter: a decision-lane
// GROUP is excluded from the OpenAI-compatible listing (it is served
// by /v1/decisions only). A chat group stays listed (today's
// behaviour).
// ─────────────────────────────────────────────────────────────────────────────

// TestHandleModels_FilterDecisionGroup — a decision group is ABSENT
// from /v1/models; a chat group is still listed.
func TestHandleModels_FilterDecisionGroup(t *testing.T) {
	stub := newDecisionGroupStub(t)
	if err := stub.AddModel(models.ModelConfig{
		ID: "chat-1", Name: "Chat 1", Enabled: true,
		Kind: models.KindChat, Internal: true, InternalModel: "glm-5",
		Credentials: []models.CredentialRef{{CredentialID: "openai-cred", Weight: 1, Position: 0}},
	}); err != nil {
		t.Fatalf("AddModel chat: %v", err)
	}
	if err := stub.AddModel(models.ModelConfig{
		ID: "chat-group", Name: "Chat Group", Enabled: true,
		Kind: models.KindGroup, Internal: true,
		Members: []models.GroupMember{{ModelID: "chat-1", Weight: 1}},
	}); err != nil {
		t.Fatalf("AddModel chat group: %v", err)
	}
	stub.lanes["chat-group"] = models.GroupLaneChat

	h, _ := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {}, stub)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	h.HandleModels(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, `"id":"decision-group"`) {
		t.Errorf("the decision group must be ABSENT from /v1/models; body=%s", body)
	}
	if !strings.Contains(body, `"id":"chat-group"`) {
		t.Errorf("the chat group must stay listed; body=%s", body)
	}
}

// TestHandleModels_FilterDecisionGroup_ThroughCachedConfig — the same
// exclusion through the PRODUCTION models binding, WITH the
// all-models fold: a group with a DISABLED decision member is still a
// decision group and must still be excluded.
func TestHandleModels_FilterDecisionGroup_ThroughCachedConfig(t *testing.T) {
	cached, cleanup := newCachedProxyModels(t, func(mgr *database.ModelsManager) {
		if err := mgr.AddCredential(models.CredentialConfig{ID: "openai-cred", Provider: "openai", APIKey: "k"}); err != nil {
			t.Fatalf("AddCredential: %v", err)
		}
		if err := mgr.AddModel(models.ModelConfig{
			ID: "dec-1", Name: "Decision 1", Enabled: true,
			Kind: models.KindDecision, Internal: true, InternalModel: "gpt-6-luna",
			Credentials: []models.CredentialRef{{CredentialID: "openai-cred", Weight: 1, Position: 0}},
		}); err != nil {
			t.Fatalf("AddModel member: %v", err)
		}
		// A DISABLED decision member: the lane derivation counts it
		// (the manager's inference counts disabled members), so the
		// group is still a decision group.
		if err := mgr.AddModel(models.ModelConfig{
			ID: "dec-off", Name: "Decision Off", Enabled: false,
			Kind: models.KindDecision, Internal: true, InternalModel: "gpt-6-luna",
			Credentials: []models.CredentialRef{{CredentialID: "openai-cred", Weight: 1, Position: 0}},
		}); err != nil {
			t.Fatalf("AddModel disabled member: %v", err)
		}
		if err := mgr.AddModel(models.ModelConfig{
			ID: "decision-group", Name: "Decision Group", Enabled: true,
			Kind: models.KindGroup, Internal: true,
			Members: []models.GroupMember{{ModelID: "dec-1", Weight: 1}, {ModelID: "dec-off", Weight: 1}},
		}); err != nil {
			t.Fatalf("AddModel group: %v", err)
		}
	})
	defer cleanup()

	h, _ := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {}, cached)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	h.HandleModels(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), `"id":"decision-group"`) {
		t.Errorf("the decision group must be ABSENT from /v1/models (the all-models fold counts the disabled decision member); body=%s", rec.Body.String())
	}
}
