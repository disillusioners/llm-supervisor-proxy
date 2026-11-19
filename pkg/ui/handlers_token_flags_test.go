package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

func TestTokenAPI_InvalidFlagsExposed(t *testing.T) {
	ts := newTokenTestServer()

	// Healthy row: no invalid flags.
	ts.mockStore.AddToken(&auth.AuthToken{
		ID:            "healthy",
		Name:          "healthy",
		CreatedAt:     time.Now(),
		CreatedBy:     "api",
		AllowedModels: []string{"m1"},
		AllowedGroups: []string{},
		ModelsInvalid: false,
		GroupsInvalid: false,
	})
	// INVALID row: unreadable axes => fail-closed DENY-ALL, flags must surface.
	ts.mockStore.AddToken(&auth.AuthToken{
		ID:            "invalid",
		Name:          "invalid",
		CreatedAt:     time.Now(),
		CreatedBy:     "api",
		AllowedModels: nil,
		AllowedGroups: nil,
		ModelsInvalid: true,
		GroupsInvalid: true,
	})

	// GET /fe/api/tokens — listTokens path.
	req := httptest.NewRequest(http.MethodGet, "/fe/api/tokens", nil)
	req = req.WithContext(context.Background())
	w := httptest.NewRecorder()
	ts.handleTokens(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET status = %d: %s", w.Code, w.Body.String())
	}

	var list []struct {
		ID                   string `json:"id"`
		AllowedModelsInvalid bool   `json:"allowed_models_invalid"`
		AllowedGroupsInvalid bool   `json:"allowed_groups_invalid"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v (%s)", err, w.Body.String())
	}
	if len(list) != 2 {
		t.Fatalf("list len = %d, want 2", len(list))
	}
	byID := map[string]struct {
		M bool
		G bool
	}{}
	for _, r := range list {
		byID[r.ID] = struct {
			M bool
			G bool
		}{r.AllowedModelsInvalid, r.AllowedGroupsInvalid}
	}
	if !byID["invalid"].M || !byID["invalid"].G {
		t.Errorf("invalid row flags = (models=%v, groups=%v), want (true,true)", byID["invalid"].M, byID["invalid"].G)
	}
	if byID["healthy"].M || byID["healthy"].G {
		t.Errorf("healthy row flags = (models=%v, groups=%v), want (false,false)", byID["healthy"].M, byID["healthy"].G)
	}

	// POST /fe/api/tokens — createToken path echoes the flags.
	ts.mockStore.createTokenFn = func(ctx context.Context, name string, expiresAt *time.Time, createdBy string, perm auth.OptionalTokenPermission) (string, *auth.AuthToken, error) {
		return "sk-test", &auth.AuthToken{
			ID:            "created-invalid",
			Name:          name,
			CreatedAt:     time.Now(),
			CreatedBy:     createdBy,
			AllowedModels: nil,
			AllowedGroups: nil,
			ModelsInvalid: true,
			GroupsInvalid: false,
		}, nil
	}
	// body required
	postReq := httptest.NewRequest(http.MethodPost, "/fe/api/tokens", strings.NewReader(`{"name":"x"}`))
	postReq.Header.Set("Content-Type", "application/json")
	postReq = postReq.WithContext(context.Background())
	pw := httptest.NewRecorder()
	ts.handleTokens(pw, postReq)
	if pw.Code != http.StatusCreated {
		t.Fatalf("POST status = %d: %s", pw.Code, pw.Body.String())
	}
	var created struct {
		ID                   string `json:"id"`
		AllowedModelsInvalid bool   `json:"allowed_models_invalid"`
		AllowedGroupsInvalid bool   `json:"allowed_groups_invalid"`
	}
	if err := json.Unmarshal(pw.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v (%s)", err, pw.Body.String())
	}
	if !created.AllowedModelsInvalid {
		t.Errorf("created token allowed_models_invalid = %v, want true", created.AllowedModelsInvalid)
	}
	if created.AllowedGroupsInvalid {
		t.Errorf("created token allowed_groups_invalid = %v, want false", created.AllowedGroupsInvalid)
	}
}

// staleClaimModelsConfig simulates a backend whose claim-aware GetModel
// REDIRECTS a stale alias to a different row, while the claim-blind
// GetModelNative lookup misses (the alias no longer exists as a row id).
type staleClaimModelsConfig struct {
	*mockModelsConfig
	alias   string
	realRow models.ModelConfig
}

func (s *staleClaimModelsConfig) GetModelNative(modelID string) *models.ModelConfig {
	return nil
}

func (s *staleClaimModelsConfig) GetModel(modelID string) *models.ModelConfig {
	if modelID == s.alias {
		row := s.realRow
		return &row
	}
	return s.mockModelsConfig.GetModel(modelID)
}

func TestTokenAPI_StaleClaimAlias_Rejected(t *testing.T) {
	ts := newTokenTestServer()
	stale := &staleClaimModelsConfig{
		mockModelsConfig: newMockModelsConfigForHandlers(),
		alias:            "stale-alias",
		realRow:          models.ModelConfig{ID: "real-group", Name: "real-group", Kind: "group", Enabled: true},
	}
	ts.Server.modelsConfig = stale

	// POST: a stale claim in allowed_groups must NOT satisfy existence
	// validation (the redirect row's ID differs from the requested id).
	req := httptest.NewRequest(http.MethodPost, "/fe/api/tokens", strings.NewReader(`{"name":"x","allowed_groups":["stale-alias"]}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.Background())
	w := httptest.NewRecorder()
	ts.handleTokens(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("POST stale-claim alias: status = %d, want 400: %s", w.Code, w.Body.String())
	}

	// PATCH: same rule on the update path.
	ts.mockStore.AddToken(&auth.AuthToken{ID: "tok-stale", Name: "n", AllowedGroups: []string{}})
	preq := httptest.NewRequest(http.MethodPatch, "/fe/api/tokens/tok-stale", strings.NewReader(`{"allowed_groups":["stale-alias"]}`))
	preq.Header.Set("Content-Type", "application/json")
	preq = preq.WithContext(context.Background())
	pw := httptest.NewRecorder()
	ts.handleTokenDetail(pw, preq)
	if pw.Code != http.StatusBadRequest {
		t.Fatalf("PATCH stale-claim alias: status = %d, want 400: %s", pw.Code, pw.Body.String())
	}
}
