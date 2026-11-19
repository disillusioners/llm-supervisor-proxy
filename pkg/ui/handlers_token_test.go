package ui

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/authz"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
	_ "modernc.org/sqlite"
)

// mockTokenStore implements auth.TokenStoreInterface for testing
type mockTokenStore struct {
	tokens        map[string]*auth.AuthToken
	updateErr     error
	createTokenFn func(ctx context.Context, name string, expiresAt *time.Time, createdBy string, perm auth.OptionalTokenPermission) (string, *auth.AuthToken, error)
}

func newMockTokenStore() *mockTokenStore {
	return &mockTokenStore{
		tokens: make(map[string]*auth.AuthToken),
	}
}

func (m *mockTokenStore) AddToken(token *auth.AuthToken) {
	m.tokens[token.ID] = token
}

func (m *mockTokenStore) ValidateToken(ctx context.Context, plaintext string) (*auth.AuthToken, error) {
	return nil, nil
}

func (m *mockTokenStore) CreateToken(ctx context.Context, name string, expiresAt *time.Time, createdBy string, perm auth.OptionalTokenPermission) (string, *auth.AuthToken, error) {
	if m.createTokenFn != nil {
		return m.createTokenFn(ctx, name, expiresAt, createdBy, perm)
	}
	return "", nil, nil
}

func (m *mockTokenStore) DeleteToken(ctx context.Context, id string) error {
	if _, ok := m.tokens[id]; !ok {
		return auth.ErrTokenNotFound
	}
	delete(m.tokens, id)
	return nil
}

func (m *mockTokenStore) ListTokens(ctx context.Context) ([]auth.AuthToken, error) {
	var result []auth.AuthToken
	for _, t := range m.tokens {
		result = append(result, *t)
	}
	return result, nil
}

func (m *mockTokenStore) GetTokenByID(ctx context.Context, id string) (*auth.AuthToken, error) {
	token, ok := m.tokens[id]
	if !ok {
		return nil, auth.ErrTokenNotFound
	}
	return token, nil
}

func (m *mockTokenStore) UpdateTokenPermission(ctx context.Context, id string, perm auth.OptionalTokenPermission) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	token, ok := m.tokens[id]
	if !ok {
		return auth.ErrTokenNotFound
	}
	// COLUMN-SKIP merge, mirroring auth.TokenStore.UpdateTokenPermission: a
	// Set:false axis is not written at all (a keep must not heal an INVALID
	// axis to NULL, nor clobber a retained value). A Set:true axis replaces,
	// [] included.
	if perm.AllowedModels.IsSet() {
		token.AllowedModels = perm.AllowedModels.Value()
	}
	if perm.AllowedGroups.IsSet() {
		token.AllowedGroups = perm.AllowedGroups.Value()
	}
	return nil
}

// tokenTestServer creates a Server with a mock token store for testing
type tokenTestServer struct {
	*Server
	mockStore *mockTokenStore
}

func newTokenTestServer() *tokenTestServer {
	mockStore := newMockTokenStore()

	s := &Server{
		tokenStore: mockStore,
	}

	return &tokenTestServer{
		Server:    s,
		mockStore: mockStore,
	}
}

// TestHandleTokenDetail_Patch_NotFound tests PATCH with non-existent token ID
func TestHandleTokenDetail_Patch_NotFound(t *testing.T) {
	ts := newTokenTestServer()
	ctx := context.Background()

	// PATCH non-existent token
	body := `{"allowed_models": ["model-a"]}`
	req := httptest.NewRequest(http.MethodPatch, "/fe/api/tokens/nonexistent-id", strings.NewReader(body))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	ts.handleTokenDetail(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleTokenDetail_Patch_InvalidJSON tests PATCH with malformed JSON body
func TestHandleTokenDetail_Patch_InvalidJSON(t *testing.T) {
	ts := newTokenTestServer()
	ctx := context.Background()

	// Create a token in the mock store
	token := &auth.AuthToken{
		ID:   "test-token-id",
		Name: "Test Token",
	}
	ts.mockStore.AddToken(token)

	// PATCH with invalid JSON
	body := `{invalid json}`
	req := httptest.NewRequest(http.MethodPatch, "/fe/api/tokens/test-token-id", strings.NewReader(body))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	ts.handleTokenDetail(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleTokenDetail_Patch_EmptyBody tests PATCH with empty body
func TestHandleTokenDetail_Patch_EmptyBody(t *testing.T) {
	ts := newTokenTestServer()
	ctx := context.Background()

	// Create a token in the mock store
	token := &auth.AuthToken{
		ID:   "test-token-id",
		Name: "Test Token",
	}
	ts.mockStore.AddToken(token)

	// PATCH with empty body
	req := httptest.NewRequest(http.MethodPatch, "/fe/api/tokens/test-token-id", strings.NewReader(""))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	ts.handleTokenDetail(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleTokenDetail_Patch_ContentType tests PATCH response has correct content type
func TestHandleTokenDetail_Patch_ContentType(t *testing.T) {
	ts := newTokenTestServer()
	ctx := context.Background()

	// Create a token in the mock store
	token := &auth.AuthToken{
		ID:   "test-token-id",
		Name: "Test Token",
	}
	ts.mockStore.AddToken(token)

	body := `{"allowed_models": ["model-a"]}`
	req := httptest.NewRequest(http.MethodPatch, "/fe/api/tokens/test-token-id", strings.NewReader(body))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	ts.handleTokenDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("expected Content-Type = 'application/json', got %s", contentType)
	}
}

// TestHandleTokenDetail_Patch_MissingID tests PATCH with missing token ID
func TestHandleTokenDetail_Patch_MissingID(t *testing.T) {
	ts := newTokenTestServer()
	ctx := context.Background()

	// PATCH with empty ID path
	req := httptest.NewRequest(http.MethodPatch, "/fe/api/tokens/", strings.NewReader(`{"allowed_models": ["model-a"]}`))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	ts.handleTokenDetail(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleTokenDetail_Patch_TokenStoreNotConfigured tests PATCH when token store is nil
func TestHandleTokenDetail_Patch_TokenStoreNotConfigured(t *testing.T) {
	s := &Server{tokenStore: nil}
	ctx := context.Background()

	body := `{"allowed_models": ["model-a"]}`
	req := httptest.NewRequest(http.MethodPatch, "/fe/api/tokens/test-token-id", strings.NewReader(body))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleTokenDetail(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

// TestHandleTokenDetail_Patch_MethodNotAllowed tests that only PATCH and DELETE are allowed
func TestHandleTokenDetail_Patch_MethodNotAllowed(t *testing.T) {
	ts := newTokenTestServer()

	methods := []string{http.MethodGet, http.MethodPost, http.MethodPut}
	for _, method := range methods {
		req := httptest.NewRequest(method, "/fe/api/tokens/test-token-id", nil)
		w := httptest.NewRecorder()

		ts.handleTokenDetail(w, req)

		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("method %s: expected status 405, got %d", method, w.Code)
		}
	}
}

// TestHandleTokenDetail_Delete_Success tests successful DELETE
func TestHandleTokenDetail_Delete_Success(t *testing.T) {
	ts := newTokenTestServer()
	ctx := context.Background()

	// Create a token in the mock store
	token := &auth.AuthToken{
		ID:   "test-token-id",
		Name: "Test Token",
	}
	ts.mockStore.AddToken(token)

	req := httptest.NewRequest(http.MethodDelete, "/fe/api/tokens/test-token-id", nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	ts.handleTokenDetail(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("expected status 204, got %d: %s", w.Code, w.Body.String())
	}

	// Verify token was deleted
	if _, ok := ts.mockStore.tokens["test-token-id"]; ok {
		t.Error("expected token to be deleted")
	}
}

// TestHandleTokenDetail_Delete_NotFound tests DELETE with non-existent token ID
func TestHandleTokenDetail_Delete_NotFound(t *testing.T) {
	ts := newTokenTestServer()
	ctx := context.Background()

	req := httptest.NewRequest(http.MethodDelete, "/fe/api/tokens/nonexistent-id", nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	ts.handleTokenDetail(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d: %s", w.Code, w.Body.String())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Ultimate Model ID API Tests
// ─────────────────────────────────────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────────────────────
// Token grant axes — admin API contract (token-authz plan T1.11)
// ─────────────────────────────────────────────────────────────────────────────

// grantRecordingServer builds a token test server whose mock store records the
// TokenPermission it was handed by CreateToken / UpdateTokenPermission, so the
// wire semantics can be asserted at the API boundary rather than inferred from
// the store.
type grantRecordingServer struct {
	*tokenTestServer
	created  *auth.OptionalTokenPermission
	updated  *auth.OptionalTokenPermission
	updateID string
	models   *mockModelsConfig
}

func newGrantRecordingServer() *grantRecordingServer {
	ts := newTokenTestServer()
	g := &grantRecordingServer{tokenTestServer: ts, models: newMockModelsConfigForHandlers()}
	ts.Server.modelsConfig = g.models
	ts.mockStore.createTokenFn = func(ctx context.Context, name string, expiresAt *time.Time, createdBy string, perm auth.OptionalTokenPermission) (string, *auth.AuthToken, error) {
		p := perm
		g.created = &p
		tok := &auth.AuthToken{
			ID:            "created-id",
			Name:          name,
			ExpiresAt:     expiresAt,
			CreatedAt:     time.Now(),
			CreatedBy:     createdBy,
			AllowedModels: perm.AllowedModels.Value(),
			AllowedGroups: perm.AllowedGroups.Value(),
		}
		return "sk-" + strings.Repeat("a", 64), tok, nil
	}
	return g
}

func (g *grantRecordingServer) addModel(id, kind string) {
	g.models.models = append(g.models.models, models.ModelConfig{ID: id, Name: id, Kind: kind, Enabled: true})
}

// postToken drives POST /fe/api/tokens and returns the recorder.
func (g *grantRecordingServer) postToken(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/fe/api/tokens", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	g.handleTokens(w, req)
	return w
}

// patchToken drives PATCH /fe/api/tokens/{id} and returns the recorder.
func (g *grantRecordingServer) patchToken(t *testing.T, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/fe/api/tokens/"+id, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	g.handleTokenDetail(w, req)
	return w
}

// TestTokenAPI_CreateEchoesGroups — POST with both axes echoes both on the 201
// response and hands the store the exact tri-state (nil stays nil).
func TestTokenAPI_CreateEchoesGroups(t *testing.T) {
	g := newGrantRecordingServer()
	g.addModel("m1", "chat")
	g.addModel("g1", "group")

	w := g.postToken(t, `{"name":"svc","allowed_models":["m1"],"allowed_groups":["g1"]}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201: %s", w.Code, w.Body.String())
	}

	if g.created == nil {
		t.Fatal("CreateToken was not called on the mock store")
	}
	createdModels := g.created.AllowedModels.Value()
	if len(createdModels) != 1 || createdModels[0] != "m1" {
		t.Errorf("store got AllowedModels = %v, want [m1]", createdModels)
	}
	createdGroups := g.created.AllowedGroups.Value()
	if len(createdGroups) != 1 || createdGroups[0] != "g1" {
		t.Errorf("store got AllowedGroups = %v, want [g1]", createdGroups)
	}

	var resp struct {
		AllowedModels *[]string `json:"allowed_models"`
		AllowedGroups *[]string `json:"allowed_groups"`
		Token         string    `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (%s)", err, w.Body.String())
	}
	if resp.AllowedGroups == nil || len(*resp.AllowedGroups) != 1 || (*resp.AllowedGroups)[0] != "g1" {
		t.Errorf("response allowed_groups = %v, want [g1]", resp.AllowedGroups)
	}
	if resp.AllowedModels == nil || len(*resp.AllowedModels) != 1 {
		t.Errorf("response allowed_models = %v, want [m1]", resp.AllowedModels)
	}
	if resp.Token == "" {
		t.Error("the plaintext token must be echoed once on POST 201")
	}

	// An omitted axis stays ABSENT (nil) all the way through — it must not be
	// coerced into an empty list.
	w = g.postToken(t, `{"name":"svc2","allowed_groups":["g1"]}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST (groups only) status = %d, want 201: %s", w.Code, w.Body.String())
	}
	// CREATE is a full write: BOTH axes are Set, and the VALUE is nil (ABSENT)
	// for the omitted axis — nil, not [] (the escalation-trap distinction).
	if !g.created.AllowedModels.IsSet() {
		t.Error("create must Set the model axis even when the value is nil")
	}
	if g.created.AllowedModels.Value() != nil {
		t.Errorf("store got AllowedModels = %v, want nil (omitted axis is ABSENT)", g.created.AllowedModels.Value())
	}
}

// TestTokenAPI_PatchMergeSemantics — nil = keep, [] = explicit-empty, ids =
// replace, per axis.
func TestTokenAPI_PatchMergeSemantics(t *testing.T) {
	g := newGrantRecordingServer()
	g.addModel("m1", "chat")
	g.addModel("m2", "chat")
	g.addModel("g1", "group")
	g.addModel("g2", "group")

	g.mockStore.AddToken(&auth.AuthToken{
		ID:            "tok-1",
		Name:          "existing",
		AllowedModels: []string{"m1"},
		AllowedGroups: []string{"g1"},
	})

	// (a) Omitting an axis keeps it: only the group axis is supplied here, and
	// the store is handed a nil model axis so it SKIPS that column.
	w := g.patchToken(t, "tok-1", `{"allowed_groups":["g2"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, want 200: %s", w.Code, w.Body.String())
	}
	stored := g.mockStore.tokens["tok-1"]
	if len(stored.AllowedGroups) != 1 || stored.AllowedGroups[0] != "g2" {
		t.Errorf("stored AllowedGroups = %v, want [g2]", stored.AllowedGroups)
	}
	if len(stored.AllowedModels) != 1 || stored.AllowedModels[0] != "m1" {
		t.Errorf("stored AllowedModels = %v, want [m1] (omitted axis kept)", stored.AllowedModels)
	}

	// (b) Explicit empty replaces with PRESENT-empty.
	w = g.patchToken(t, "tok-1", `{"allowed_groups":[]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH (empty) status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if stored.AllowedGroups == nil {
		t.Fatal("stored AllowedGroups is nil after an explicit [] — must be PRESENT-empty, never ABSENT")
	}
	if len(stored.AllowedGroups) != 0 {
		t.Errorf("stored AllowedGroups = %v, want empty", stored.AllowedGroups)
	}

	// (c) Ids replace.
	w = g.patchToken(t, "tok-1", `{"allowed_models":["m2"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH (replace) status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if len(stored.AllowedModels) != 1 || stored.AllowedModels[0] != "m2" {
		t.Errorf("stored AllowedModels = %v, want [m2]", stored.AllowedModels)
	}
}

// TestTokenAPI_PatchClearDoesNotEscalate — the Q13/R-6 trap asserted at the API
// boundary: a token with models ABSENT + groups [g1], PATCHed with
// {"allowed_groups":[]}, must end up with a NON-NIL empty group axis (never
// nil) and an untouched nil model axis ⇒ the token stays RESTRICTED.
func TestTokenAPI_PatchClearDoesNotEscalate(t *testing.T) {
	g := newGrantRecordingServer()
	g.addModel("g1", "group")

	g.mockStore.AddToken(&auth.AuthToken{
		ID:            "narrow-1",
		Name:          "narrow",
		AllowedModels: nil, // ABSENT — the post-034 narrow shape
		AllowedGroups: []string{"g1"},
	})

	body := `{"allowed_groups":[]}`
	w := g.patchToken(t, "narrow-1", body)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, want 200: %s", w.Code, w.Body.String())
	}

	stored := g.mockStore.tokens["narrow-1"]
	if stored.AllowedGroups == nil {
		t.Fatal("stored AllowedGroups is nil after an explicit clear — clearing a grant axis must produce PRESENT-empty, never ABSENT (that would escalate the token to wildcard)")
	}
	if len(stored.AllowedGroups) != 0 {
		t.Errorf("stored AllowedGroups = %v, want empty", stored.AllowedGroups)
	}
	if stored.AllowedModels != nil {
		t.Errorf("stored AllowedModels = %v, want nil (the omitted axis must not be written)", stored.AllowedModels)
	}

	// Prove the resulting state is NOT a wildcard under the real resolver.
	scope := authz.Resolve(stored, nil)
	if scope == nil {
		t.Fatal("the cleared token must NOT resolve to a wildcard scope")
	}
	if scope.Allows("any-model") {
		t.Error("the cleared token must still deny an unlisted model")
	}
}

// TestTokenAPI_Validation — the write-time rules of plan §6.3, including the
// anti-wedge inbound-only rule.
func TestTokenAPI_Validation(t *testing.T) {
	g := newGrantRecordingServer()
	g.addModel("m1", "chat")
	g.addModel("g1", "group")

	// (a) allowed_groups: unknown id ⇒ 400.
	if w := g.postToken(t, `{"name":"x","allowed_groups":["ghost"]}`); w.Code != http.StatusBadRequest {
		t.Errorf("unknown group id: status = %d, want 400: %s", w.Code, w.Body.String())
	}
	// (b) allowed_groups: an existing NON-group row ⇒ 400.
	if w := g.postToken(t, `{"name":"x","allowed_groups":["m1"]}`); w.Code != http.StatusBadRequest {
		t.Errorf("non-group id in allowed_groups: status = %d, want 400: %s", w.Code, w.Body.String())
	}
	// (c) allowed_models: unknown id ⇒ 400.
	if w := g.postToken(t, `{"name":"x","allowed_models":["ghost"]}`); w.Code != http.StatusBadRequest {
		t.Errorf("unknown model id: status = %d, want 400: %s", w.Code, w.Body.String())
	}
	// (d) a group id in allowed_models is LEGAL (target-only grant, plan §2.4).
	if w := g.postToken(t, `{"name":"x","allowed_models":["g1"]}`); w.Code != http.StatusCreated {
		t.Errorf("group id in allowed_models: status = %d, want 201: %s", w.Code, w.Body.String())
	}

	// (e) ANTI-WEDGE: a stale id on the OMITTED axis must NOT 400. The token
	// below points at a model that no longer exists; PATCHing a different axis
	// must succeed so the operator can still edit it.
	g.mockStore.AddToken(&auth.AuthToken{
		ID:            "stale-1",
		Name:          "stale",
		AllowedModels: []string{"deleted-model"}, // not in the models config
		AllowedGroups: []string{"g1"},
	})
	if w := g.patchToken(t, "stale-1", `{"allowed_groups":["g1"]}`); w.Code != http.StatusOK {
		t.Errorf("omitted stale axis: status = %d, want 200 (inbound-only validation): %s", w.Code, w.Body.String())
	}
	// …but actively re-submitting the stale id IS a 400.
	if w := g.patchToken(t, "stale-1", `{"allowed_models":["deleted-model"]}`); w.Code != http.StatusBadRequest {
		t.Errorf("re-submitted stale id: status = %d, want 400: %s", w.Code, w.Body.String())
	}
}

// TestTokenAPI_PatchNonexistent_404 pins the PD-3 companion contract: the 404
// for an unknown id comes from the API's GetTokenByID PRE-CHECK, not from the
// store's RowsAffected. That matters because the store's both-nil arm executes
// no UPDATE at all (a deliberate success), so a future refactor that deleted
// the pre-check "because the store covers it" would silently turn a missing
// token into a `200 {"success":true}`.
func TestTokenAPI_PatchNonexistent_404(t *testing.T) {
	g := newGrantRecordingServer()
	g.addModel("g1", "group")

	// (a) an axis-bearing PATCH on an unknown id.
	if w := g.patchToken(t, "does-not-exist", `{"allowed_groups":["g1"]}`); w.Code != http.StatusNotFound {
		t.Errorf("PATCH unknown id: status = %d, want 404: %s", w.Code, w.Body.String())
	}

	// (b) the both-axes-nil PATCH — the case where the store is a no-op and
	// would report success if the pre-check were ever removed.
	if w := g.patchToken(t, "does-not-exist", `{}`); w.Code != http.StatusNotFound {
		t.Errorf("PATCH unknown id with an empty body: status = %d, want 404 (the pre-check is the 404 contract): %s", w.Code, w.Body.String())
	}
}

// TestTokenAPI_Validation_MalformedShape pins PD-8 (a): a non-array value for
// either axis is a 400, and the EXISTING typed decode (json.Decoder into
// *[]string) is the mechanism — no second shape-checker was added.
func TestTokenAPI_Validation_MalformedShape(t *testing.T) {
	g := newGrantRecordingServer()
	g.addModel("g1", "group")
	g.addModel("m1", "chat")

	cases := []struct{ name, body string }{
		{"allowed_groups as a string", `{"name":"x","allowed_groups":"g1"}`},
		{"allowed_groups as a number", `{"name":"x","allowed_groups":5}`},
		{"allowed_models as a number", `{"name":"x","allowed_models":5}`},
		{"allowed_models as an object", `{"name":"x","allowed_models":{"a":"b"}}`},
		{"allowed_groups as an array of numbers", `{"name":"x","allowed_groups":[1,2]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if w := g.postToken(t, tc.body); w.Code != http.StatusBadRequest {
				t.Errorf("POST %s: status = %d, want 400 (typed decode rejects the shape): %s", tc.body, w.Code, w.Body.String())
			}
		})
	}

	// The PATCH route uses the same typed decode; pin it there too, because a
	// divergent second shape-checker must not appear on only one route.
	g.mockStore.AddToken(&auth.AuthToken{ID: "shape-1", Name: "s", AllowedGroups: []string{"g1"}})
	if w := g.patchToken(t, "shape-1", `{"allowed_groups":"g1"}`); w.Code != http.StatusBadRequest {
		t.Errorf("PATCH malformed shape: status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if len(g.mockStore.tokens["shape-1"].AllowedGroups) != 1 {
		t.Error("a rejected request must not be applied")
	}
}

// TestTokenAPI_PatchNilAxis_DoesNotHealDenyAll is the PD-3 head-line pin at the
// API BOUNDARY, and it pins the keep-contract ruling: an explicit JSON `null`
// is a KEEP — identical to omitting the key (plan §2.8 corrected wording;
// there is NO reset-to-absent wire state on update, NULL/ABSENT is
// CREATE-ONLY).
//
// Properties:
//  1. an omitted axis and an explicit-null axis produce the SAME store payload
//     (both nil ⇒ the column is skipped), so `null` can never write NULL;
//  2. a token whose model axis is unreadable (ModelsInvalid ⇒ DENY-ALL) is
//     PATCHed on its OTHER axis and the response is 200 — never 400 — with the
//     DENY-ALL axis left exactly as it was (never healed into a grant list,
//     never reset to absent).
func TestTokenAPI_PatchNilAxis_DoesNotHealDenyAll(t *testing.T) {
	g := newGrantRecordingServer()
	g.addModel("g1", "group")
	g.addModel("g2", "group")

	// ── property 1: explicit null ≡ omitted ─────────────────────────────────
	g.mockStore.AddToken(&auth.AuthToken{
		ID:            "null-eq-omit",
		Name:          "n",
		AllowedModels: []string{"m-kept"},
		AllowedGroups: []string{"g1"},
	})

	if w := g.patchToken(t, "null-eq-omit", `{"allowed_groups":null}`); w.Code != http.StatusOK {
		t.Fatalf(`PATCH with explicit null: status = %d, want 200: %s`, w.Code, w.Body.String())
	}
	afterNull := g.mockStore.tokens["null-eq-omit"]
	if len(afterNull.AllowedGroups) != 1 || afterNull.AllowedGroups[0] != "g1" {
		t.Errorf("explicit null changed the axis to %v, want [g1] untouched (null == keep)", afterNull.AllowedGroups)
	}
	if len(afterNull.AllowedModels) != 1 || afterNull.AllowedModels[0] != "m-kept" {
		t.Errorf("omitted model axis = %v, want [m-kept] untouched", afterNull.AllowedModels)
	}

	// Control: the omitted-key form must land on the same state.
	g.mockStore.AddToken(&auth.AuthToken{
		ID:            "omit-form",
		Name:          "n",
		AllowedModels: []string{"m-kept"},
		AllowedGroups: []string{"g1"},
	})
	if w := g.patchToken(t, "omit-form", `{}`); w.Code != http.StatusOK {
		t.Fatalf("PATCH with an empty object: status = %d, want 200: %s", w.Code, w.Body.String())
	}
	afterOmit := g.mockStore.tokens["omit-form"]
	if len(afterOmit.AllowedGroups) != len(afterNull.AllowedGroups) || len(afterOmit.AllowedModels) != len(afterNull.AllowedModels) {
		t.Errorf("explicit null and omitted key diverged: null=%+v omit=%+v — null MUST mean keep", afterNull, afterOmit)
	}

	// ── property 2: an INVALID axis survives an update of the other axis ────
	g.mockStore.AddToken(&auth.AuthToken{
		ID:            "deny-all-api",
		Name:          "deny",
		AllowedModels: nil, // unreadable row ⇒ no ids
		ModelsInvalid: true,
		AllowedGroups: []string{"g1"},
	})

	w := g.patchToken(t, "deny-all-api", `{"allowed_groups":["g2"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH on a token with an INVALID model axis: status = %d, want 200 (never 400): %s", w.Code, w.Body.String())
	}
	stored := g.mockStore.tokens["deny-all-api"]
	if !stored.ModelsInvalid {
		t.Error("ModelsInvalid was cleared by an update that did not mention the model axis — DENY-ALL must survive")
	}
	if stored.AllowedModels != nil {
		t.Errorf("model axis = %v, want nil (unchanged, still unreadable)", stored.AllowedModels)
	}
	if len(stored.AllowedGroups) != 1 || stored.AllowedGroups[0] != "g2" {
		t.Errorf("group axis = %v, want [g2] (the supplied axis DID land)", stored.AllowedGroups)
	}

	// And the token still denies everything under the real resolver.
	if scope := authz.Resolve(stored, nil); scope == nil || !scope.IsDenyAll() {
		t.Fatalf("the token no longer resolves to DENY-ALL: %+v", scope)
	}
}

// TestTokenAPI_PatchOmittedInvalidAxis_RealStore_ByteIdentical is the P-G26
// end-to-end form of the PD-3 fail-closed preservation rule, run against a REAL
// auth.TokenStore over a real SQLite row rather than the mock (the corpus asks
// for a store-level assertion that the generated UPDATE carries no
// `allowed_models` fragment and that the persisted bytes are unchanged).
//
// It is the executable proof of ADR :234/:320 + D10: an operator editing the
// GROUPS of a token whose model axis is corrupt must get a 200, must not be
// told 400, and must leave the corrupt bytes exactly as they were — so the
// token stays DENY-ALL instead of silently becoming a wildcard.
func TestTokenAPI_PatchOmittedInvalidAxis_RealStore_ByteIdentical(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "tok.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS auth_tokens (
		id TEXT PRIMARY KEY,
		token_hash TEXT NOT NULL UNIQUE,
		name TEXT NOT NULL,
		expires_at TEXT,
		created_at TEXT NOT NULL,
		created_by TEXT NOT NULL,
		allowed_models TEXT DEFAULT NULL,
		allowed_groups TEXT DEFAULT NULL
	)`); err != nil {
		t.Fatalf("create auth_tokens: %v", err)
	}

	// Seed a DENY-ALL row: bin_models holds unreadable bytes.
	const corrupt = `[broken`
	if _, err := db.Exec(
		`INSERT INTO auth_tokens (id, token_hash, name, expires_at, created_at, created_by, allowed_models, allowed_groups) VALUES ('deny-real', 'h', 'deny', NULL, '2026-01-01T00:00:00Z', 'seed', ?, '["g1"]')`,
		corrupt,
	); err != nil {
		t.Fatalf("seed row: %v", err)
	}

	realStore := auth.NewTokenStore(db, database.SQLite)

	modelsCfg := newMockModelsConfigForHandlers()
	modelsCfg.models = append(modelsCfg.models,
		models.ModelConfig{ID: "g1", Name: "g1", Kind: "group", Enabled: true},
		models.ModelConfig{ID: "g2", Name: "g2", Kind: "group", Enabled: true},
	)
	srv := &Server{tokenStore: realStore, modelsConfig: modelsCfg}

	readRaw := func() (string, string) {
		t.Helper()
		var m, g interface{}
		if err := db.QueryRow(`SELECT allowed_models, allowed_groups FROM auth_tokens WHERE id = 'deny-real'`).Scan(&m, &g); err != nil {
			t.Fatalf("read raw: %v", err)
		}
		str := func(v interface{}) string {
			switch x := v.(type) {
			case nil:
				return "<NULL>"
			case string:
				return x
			case []byte:
				return string(x)
			}
			return "<?>"
		}
		return str(m), str(g)
	}

	beforeModels, beforeGroups := readRaw()
	if beforeModels != corrupt {
		t.Fatalf("precondition: allowed_models = %q, want %q", beforeModels, corrupt)
	}

	// PATCH the GROUP axis only — the model axis is omitted.
	req := httptest.NewRequest(http.MethodPatch, "/fe/api/tokens/deny-real", strings.NewReader(`{"allowed_groups":["g2"]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleTokenDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, want 200 (never 400) on a token with an unreadable axis: %s", w.Code, w.Body.String())
	}

	afterModels, afterGroups := readRaw()
	if afterModels != corrupt {
		t.Errorf("allowed_models = %q after the PATCH, want the original corrupt bytes %q — the column must be BYTE-IDENTICAL (the UPDATE must not mention it)", afterModels, corrupt)
	}
	if beforeGroups == afterGroups {
		t.Errorf("allowed_groups = %q unchanged — the supplied axis should have been written", afterGroups)
	}

	// The row still reads as DENY-ALL through the real store.
	readBack, err := realStore.GetTokenByID(context.Background(), "deny-real")
	if err != nil {
		t.Fatalf("GetTokenByID: %v", err)
	}
	if !readBack.ModelsInvalid {
		t.Error("ModelsInvalid = false — the DENY-ALL state did not survive the PATCH")
	}
	if readBack.AllowedModels != nil {
		t.Errorf("AllowedModels = %v, want nil", readBack.AllowedModels)
	}
	if scope := authz.Resolve(readBack, nil); scope == nil || !scope.IsDenyAll() {
		t.Fatalf("the token no longer resolves to DENY-ALL: %+v", scope)
	}
}
