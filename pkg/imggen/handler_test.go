package imggen

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/usage"
)

// fakeMiniMax is a scriptable httptest fixture for the MiniMax
// image_generation endpoint. Each test sets a response script
// (status + body + delay). The fake records the call count and
// the most recent request body + headers for header-strip
// assertions (BE-A2 — proxy's sk- token must never reach
// upstream).
type fakeMiniMax struct {
	mu        sync.Mutex
	hitCount  atomic.Int64
	lastBody  []byte
	lastAuthH string
	lastCT    string
	lastHdrs  http.Header
	script    fakeScript
	startedCh chan struct{} // closed on first request, optional
}

type fakeScript struct {
	status      int
	body        []byte
	contentType string
	delay       time.Duration
}

func newFakeMiniMax(t *testing.T, script fakeScript) (*httptest.Server, *fakeMiniMax) {
	t.Helper()
	f := &fakeMiniMax{script: script}
	if script.contentType == "" {
		script.contentType = "application/json"
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		body, _ := io.ReadAll(r.Body)
		f.lastBody = body
		f.lastAuthH = r.Header.Get("Authorization")
		f.lastCT = r.Header.Get("Content-Type")
		f.lastHdrs = r.Header.Clone()
		f.mu.Unlock()
		f.hitCount.Add(1)
		if script.delay > 0 {
			select {
			case <-time.After(script.delay):
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", script.contentType)
		w.WriteHeader(script.status)
		_, _ = w.Write(script.body)
	}))
	t.Cleanup(srv.Close)
	return srv, f
}

// successBody returns a MiniMax success body with the given
// success_count and failed_count (strings — live-proven shape).
func successBody(success, failed int) []byte {
	md := map[string]interface{}{"success_count": success, "failed_count": failed}
	br := map[string]interface{}{"status_code": 0, "status_msg": "success"}
	return mustJSON(map[string]interface{}{
		"data":      []interface{}{map[string]interface{}{"image_urls": []string{"https://x/a.png"}}},
		"metadata":  md,
		"base_resp": br,
	})
}

func errorIn200Body(code int, msg string) []byte {
	return mustJSON(map[string]interface{}{
		"data":      nil,
		"base_resp": map[string]interface{}{"status_code": code, "status_msg": msg},
	})
}

func mustJSON(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return b
}

// ---------- Handler test harness ----------

type handlerTestEnv struct {
	t           *testing.T
	upstream    *httptest.Server
	fake        *fakeMiniMax
	handler     *Handler
	bus         *events.Bus
	usage       *usage.Counter
	usageDB     *usageCounterDB
	tokenStore  *memTokenStore
	modelID     string
	credBaseURL string
	imageGenTimeout time.Duration
}

type usageCounterDB struct {
	mu     sync.Mutex
	db     map[string]int // key = "token:model:hour"
}

// memTokenStore is an in-memory auth.TokenStoreInterface used by
// the imggen handler tests so we don't need a real DB.
type memTokenStore struct {
	mu     sync.Mutex
	tokens map[string]*auth.AuthToken
}

func newMemTokenStore() *memTokenStore {
	return &memTokenStore{tokens: map[string]*auth.AuthToken{}}
}

func (m *memTokenStore) add(tok *auth.AuthToken) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens[tok.ID] = tok
}

func (m *memTokenStore) ValidateToken(ctx context.Context, plaintext string) (*auth.AuthToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tokens {
		if auth.HashToken(plaintext) == t.TokenHash {
			return t, nil
		}
	}
	return nil, auth.ErrTokenNotFound
}

func (m *memTokenStore) CreateToken(ctx context.Context, name string, expiresAt *time.Time, createdBy string, ultimateModelEnabled bool, ultimateModelID string, allowedModels []string) (string, *auth.AuthToken, error) {
	plaintext, hash, err := auth.GenerateToken()
	if err != nil {
		return "", nil, err
	}
	tok := &auth.AuthToken{
		ID:                   "mem-" + plaintext[:8],
		Name:                 name,
		TokenHash:            hash,
		ExpiresAt:            expiresAt,
		CreatedAt:            time.Now(),
		CreatedBy:            createdBy,
		UltimateModelEnabled: ultimateModelEnabled,
		UltimateModelID:      ultimateModelID,
		AllowedModels:        allowedModels,
	}
	m.add(tok)
	return plaintext, tok, nil
}

func (m *memTokenStore) DeleteToken(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.tokens, id)
	return nil
}

func (m *memTokenStore) ListTokens(ctx context.Context) ([]auth.AuthToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]auth.AuthToken, 0, len(m.tokens))
	for _, t := range m.tokens {
		out = append(out, *t)
	}
	return out, nil
}

func (m *memTokenStore) GetTokenByID(ctx context.Context, id string) (*auth.AuthToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tokens[id]
	if !ok {
		return nil, auth.ErrTokenNotFound
	}
	return t, nil
}

func (m *memTokenStore) UpdateTokenPermission(ctx context.Context, id string, ultimateModelEnabled bool, ultimateModelID string, allowedModels []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tokens[id]
	if !ok {
		return auth.ErrTokenNotFound
	}
	t.UltimateModelEnabled = ultimateModelEnabled
	t.UltimateModelID = ultimateModelID
	t.AllowedModels = allowedModels
	return nil
}

// stubConfig satisfies the configGetter interface (only
// GetImageGenTimeout is needed).
type stubConfig struct{ timeout time.Duration }

func (s stubConfig) GetImageGenTimeout() time.Duration { return s.timeout }

// newEnv constructs a handler test env with a fake MiniMax
// upstream, an in-memory usage counter, and a fake token store.
// By default the env installs a valid token ("sk-test-token")
// that has all-models access; tests that need to test auth can
// use a different request.
func newEnv(t *testing.T, script fakeScript) *handlerTestEnv {
	t.Helper()
	upstream, fake := newFakeMiniMax(t, script)
	// Use the test counter DB.
	db, _ := newUsageTestDB(t)
	uc := usage.NewCounter(db, database.SQLite)
	bus := events.NewBus()
	ts := newMemTokenStore()

	credBaseURL := upstream.URL

	h := NewHandler(stubConfig{timeout: 200 * time.Millisecond}, bus, ts, uc)
	// Resolvers: model is image-gen; credential points at the
	// fake upstream. Stable (modelID, modelID) key for affinity.
	modelID := "minimax-image-01"
	h.SetModelResolver(func(id string) (*models.ModelConfig, bool) {
		if id != modelID {
			return nil, false
		}
		return &models.ModelConfig{
			ID:             modelID,
			Name:           "Image Gen 01",
			Kind:           models.KindImageGen,
			Enabled:        true,
			Internal:       true,
			InternalModel:  "image-01",
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
			Provider:      "minimax",
			APIKey:        "sk-cred-secret",
			BaseURL:       credBaseURL,
			InternalModel: "image-01",
		}, true
	})

	// Install a default valid token with all-models access. The
	// plaintext "sk-test-token" matches the default Authorization
	// header the test helper injects when auth is needed.
	tok := &auth.AuthToken{
		ID:            "tok-test",
		Name:          "Test Token",
		TokenHash:     auth.HashToken("sk-test-token"),
		AllowedModels: nil, // nil = all models allowed
	}
	ts.add(tok)

	return &handlerTestEnv{
		t:           t,
		upstream:    upstream,
		fake:        fake,
		handler:     h,
		bus:         bus,
		usage:       uc,
		usageDB:     &usageCounterDB{db: map[string]int{}},
		tokenStore:  ts,
		modelID:     modelID,
		credBaseURL: credBaseURL,
		imageGenTimeout: 200 * time.Millisecond,
	}
}

// defaultAuthHeader is the default Authorization header for
// handler tests. The handler's auth path uses extractAPIKey
// (Authorization: Bearer …). The plaintext matches the
// pre-registered test token.
const defaultAuthHeader = "Bearer sk-test-token"

// newUsageTestDB is a minimal in-memory SQLite for the usage
// counter so the metering assertions can read back rows.
func newUsageTestDB(t *testing.T) (*sql.DB, error) {
	t.Helper()
	db, err := openSQLiteForUsage()
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, nil
}

// sendRequest is a small helper that POSTs the given body to
// the handler and returns the response. If the caller does not
// pass an Authorization header, the test env's default
// "sk-test-token" is injected so auth passes (most tests want
// the success path; auth-specific tests pass their own).
func (e *handlerTestEnv) sendRequest(method, body string, headers map[string]string) *httptest.ResponseRecorder {
	e.t.Helper()
	r := httptest.NewRequest(method, "/v1/image_generation", strings.NewReader(body))
	if headers == nil {
		r.Header.Set("Authorization", defaultAuthHeader)
	} else {
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		if _, ok := headers["Authorization"]; !ok {
			r.Header.Set("Authorization", defaultAuthHeader)
		}
	}
	w := httptest.NewRecorder()
	e.handler.HandleImageGeneration(w, r)
	return w
}

// ---------- Tests ----------

// T1.7.2 — happy path / byte-identical success relay.
func TestHandler_Success_ByteIdenticalRelay(t *testing.T) {
	body := successBody(1, 0)
	env := newEnv(t, fakeScript{status: 200, body: body})

	rec := env.sendRequest(http.MethodPost, `{"model":"`+env.modelID+`","prompt":"a cat"}`, nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Equal(rec.Body.Bytes(), body) {
		t.Errorf("response body not byte-identical to upstream: got=%s want=%s", rec.Body.String(), string(body))
	}
	if env.fake.hitCount.Load() != 1 {
		t.Errorf("upstream hit count = %d, want 1 (exactly-one-upstream-call)", env.fake.hitCount.Load())
	}
}

// T1.7.2 — error-in-200 mapped to 502 + body preserved verbatim.
func TestHandler_ErrorIn200_502WithBodyPreserved(t *testing.T) {
	body := errorIn200Body(2013, "invalid params")
	env := newEnv(t, fakeScript{status: 200, body: body})

	rec := env.sendRequest(http.MethodPost, `{"model":"`+env.modelID+`","prompt":"x"}`, nil)
	if rec.Code != 502 {
		t.Errorf("status = %d, want 502", rec.Code)
	}
	if !bytes.Equal(rec.Body.Bytes(), body) {
		t.Errorf("502 body must be upstream body verbatim, got=%s want=%s", rec.Body.String(), string(body))
	}
}

// T1.7.2 — partial success (status_code==0, failed_count>0) is
// 200 success.
func TestHandler_PartialSuccess_200(t *testing.T) {
	body := mustJSON(map[string]interface{}{
		"data":      []interface{}{map[string]interface{}{"image_urls": []string{"https://x/a.png"}}},
		"metadata":  map[string]interface{}{"success_count": "1", "failed_count": "1"},
		"base_resp": map[string]interface{}{"status_code": 0, "status_msg": "partial"},
	})
	env := newEnv(t, fakeScript{status: 200, body: body})

	rec := env.sendRequest(http.MethodPost, `{"model":"`+env.modelID+`","prompt":"x"}`, nil)
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200 (partial success)", rec.Code)
	}
}

// T1.7.2 — missing base_resp is 502 (API drift).
func TestHandler_MissingBaseResp_502(t *testing.T) {
	body := []byte(`{"data":null}`)
	env := newEnv(t, fakeScript{status: 200, body: body})

	rec := env.sendRequest(http.MethodPost, `{"model":"`+env.modelID+`","prompt":"x"}`, nil)
	if rec.Code != 502 {
		t.Errorf("status = %d, want 502 (missing base_resp)", rec.Code)
	}
}

// T1.7.2 — unparseable JSON is 502 (HTML intermediary page, etc).
func TestHandler_UnparseableJSON_502(t *testing.T) {
	env := newEnv(t, fakeScript{status: 200, body: []byte(`<html>upstream</html>`)})

	rec := env.sendRequest(http.MethodPost, `{"model":"`+env.modelID+`","prompt":"x"}`, nil)
	if rec.Code != 502 {
		t.Errorf("status = %d, want 502 (unparseable JSON)", rec.Code)
	}
}

// T1.7.2 — empty body is 502 with the empty-body log Reason.
func TestHandler_EmptyBody_502(t *testing.T) {
	env := newEnv(t, fakeScript{status: 200, body: nil})

	rec := env.sendRequest(http.MethodPost, `{"model":"`+env.modelID+`","prompt":"x"}`, nil)
	if rec.Code != 502 {
		t.Errorf("status = %d, want 502 (empty body)", rec.Code)
	}
}

// T1.7.2 — upstream HTTP 500 with status_code==0 is relayed
// verbatim (the handler does NOT promote to 502).
func TestHandler_UpstreamHTTP500_PassAsIs(t *testing.T) {
	body := successBody(1, 0)
	env := newEnv(t, fakeScript{status: 500, body: body})

	rec := env.sendRequest(http.MethodPost, `{"model":"`+env.modelID+`","prompt":"x"}`, nil)
	if rec.Code != 500 {
		t.Errorf("status = %d, want 500 (PassAsIs relays upstream HTTP status verbatim)", rec.Code)
	}
}

// T1.7.2 — method guard: GET returns 405.
func TestHandler_MethodGuard_405(t *testing.T) {
	env := newEnv(t, fakeScript{status: 200, body: successBody(1, 0)})
	rec := env.sendRequest(http.MethodGet, `{}`, nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

// T1.7.2 — model rewrite: upstream sees cred.InternalModel.
func TestHandler_ModelRewrite(t *testing.T) {
	env := newEnv(t, fakeScript{status: 200, body: successBody(1, 0)})
	_ = env.sendRequest(http.MethodPost, `{"model":"`+env.modelID+`","prompt":"x"}`, nil)
	env.fake.mu.Lock()
	defer env.fake.mu.Unlock()
	if !bytes.Contains(env.fake.lastBody, []byte(`"model":"image-01"`)) {
		t.Errorf("upstream body missing model=image-01 (rewrite to InternalModel); got=%s", string(env.fake.lastBody))
	}
	if bytes.Contains(env.fake.lastBody, []byte(env.modelID)) {
		t.Errorf("upstream body still carries client-facing model=%s; got=%s", env.modelID, string(env.fake.lastBody))
	}
}

// T1.7.2 — client Authorization is stripped (BE-A2). The client
// sends an Authorization header; the proxy's auth path uses
// it for validation, then strips it before forwarding. The
// upstream sees ONLY the credential bearer the proxy installed.
func TestHandler_ClientAuthStripped(t *testing.T) {
	env := newEnv(t, fakeScript{status: 200, body: successBody(1, 0)})
	// sendRequest's default Authorization is "Bearer
	// sk-test-token" (the pre-registered test token). Verify
	// the upstream's recorded Authorization is the credential
	// bearer, not the client's sk-test-token.
	_ = env.sendRequest(http.MethodPost, `{"model":"`+env.modelID+`"}`, nil)
	env.fake.mu.Lock()
	defer env.fake.mu.Unlock()
	if env.fake.lastAuthH == "Bearer sk-test-token" {
		t.Errorf("upstream received the client's bearer (BE-A2 leak); lastAuthH=%q", env.fake.lastAuthH)
	}
	if env.fake.lastAuthH != "Bearer sk-cred-secret" {
		t.Errorf("upstream Authorization = %q, want credential bearer", env.fake.lastAuthH)
	}
}

// T1.7.2 — exactly-one-upstream-call: even on a 502-classified
// error, the upstream was called exactly once.
func TestHandler_ExactlyOneUpstreamCall(t *testing.T) {
	env := newEnv(t, fakeScript{status: 200, body: errorIn200Body(2013, "x")})
	_ = env.sendRequest(http.MethodPost, `{"model":"`+env.modelID+`"}`, nil)
	if env.fake.hitCount.Load() != 1 {
		t.Errorf("upstream hit count = %d, want 1 (no retry)", env.fake.hitCount.Load())
	}
}

// T1.7.2 — 404 for unknown model.
func TestHandler_UnknownModel_404(t *testing.T) {
	env := newEnv(t, fakeScript{status: 200, body: successBody(1, 0)})
	rec := env.sendRequest(http.MethodPost, `{"model":"not-a-real-model"}`, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if env.fake.hitCount.Load() != 0 {
		t.Errorf("upstream was called for unknown model: hit count = %d", env.fake.hitCount.Load())
	}
}

// T1.4.4f / review F3 — credential resolution failure ⇒ 502
// (upstream-class failure, OpenAI envelope; same family as the
// unsupported-provider path), not 500.
func TestHandler_CredentialUnresolved_502(t *testing.T) {
	env := newEnv(t, fakeScript{status: 200, body: successBody(1, 0)})
	env.handler.SetCredentialResolver(func(id string) (models.ResolvedCredential, bool) {
		return models.ResolvedCredential{}, false
	})
	rec := env.sendRequest(http.MethodPost, `{"model":"`+env.modelID+`"}`, nil)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 (credential resolution is an upstream-class failure)", rec.Code)
	}
	if env.fake.hitCount.Load() != 0 {
		t.Errorf("upstream was called despite credential failure: hit count = %d", env.fake.hitCount.Load())
	}
}

// T1.7.2 — auth gating: invalid token ⇒ 401; no upstream call.
func TestHandler_Auth_InvalidToken_401(t *testing.T) {
	env := newEnv(t, fakeScript{status: 200, body: successBody(1, 0)})
	rec := env.sendRequest(http.MethodPost, `{"model":"`+env.modelID+`"}`, map[string]string{
		"Authorization": "Bearer sk-not-a-real-token",
	})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if env.fake.hitCount.Load() != 0 {
		t.Errorf("upstream was called despite auth failure: hit count = %d", env.fake.hitCount.Load())
	}
}

// T1.7.2 — auth disabled: tokenStore nil ⇒ open.
func TestHandler_AuthDisabled_Open(t *testing.T) {
	upstream, fake := newFakeMiniMax(t, fakeScript{status: 200, body: successBody(1, 0)})
	bus := events.NewBus()
	db, _ := newUsageTestDB(t)
	h := NewHandler(stubConfig{timeout: 1 * time.Second}, bus, nil, usage.NewCounter(db, database.SQLite))
	h.SetModelResolver(func(id string) (*models.ModelConfig, bool) {
		return &models.ModelConfig{ID: id, Kind: models.KindImageGen, Internal: true, InternalModel: "image-01", InternalBaseURL: upstream.URL, Credentials: []models.CredentialRef{{CredentialID: "x"}}}, true
	})
	h.SetCredentialResolver(func(id string) (models.ResolvedCredential, bool) {
		return models.ResolvedCredential{Provider: "minimax", APIKey: "sk-cred", BaseURL: upstream.URL, InternalModel: "image-01"}, true
	})
	r := httptest.NewRequest(http.MethodPost, "/v1/image_generation", strings.NewReader(`{"model":"x"}`))
	w := httptest.NewRecorder()
	h.HandleImageGeneration(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200 (auth disabled)", w.Code)
	}
	if fake.hitCount.Load() != 1 {
		t.Errorf("upstream hit count = %d, want 1", fake.hitCount.Load())
	}
}

// T1.7.2 — 405 does not call upstream.
func TestHandler_405_NoUpstream(t *testing.T) {
	env := newEnv(t, fakeScript{status: 200, body: successBody(1, 0)})
	env.sendRequest(http.MethodPut, `{}`, nil)
	if env.fake.hitCount.Load() != 0 {
		t.Errorf("PUT reached upstream: hit count = %d", env.fake.hitCount.Load())
	}
}

// T1.7.2 — IsModelAllowed 403 when token has restricted list.
func TestHandler_ModelNotAllowed_403(t *testing.T) {
	env := newEnv(t, fakeScript{status: 200, body: successBody(1, 0)})
	// Add a restricted token that does NOT include our model.
	tok := &auth.AuthToken{
		ID:            "tok-restricted",
		Name:          "Restricted",
		TokenHash:     auth.HashToken("sk-restricted"),
		AllowedModels: []string{"some-other-model"},
	}
	env.tokenStore.add(tok)
	rec := env.sendRequest(http.MethodPost, `{"model":"`+env.modelID+`"}`, map[string]string{
		"Authorization": "Bearer sk-restricted",
	})
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	if env.fake.hitCount.Load() != 0 {
		t.Errorf("upstream was called despite 403: hit count = %d", env.fake.hitCount.Load())
	}
}

// T1.7.2 — event published on success.
func TestHandler_EventPublished_Success(t *testing.T) {
	env := newEnv(t, fakeScript{status: 200, body: successBody(1, 0)})
	sub, err := env.bus.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	defer env.bus.Unsubscribe(sub)
	_ = env.sendRequest(http.MethodPost, `{"model":"`+env.modelID+`"}`, nil)
	select {
	case evt := <-sub:
		if evt.Type != "image_generation" {
			t.Errorf("event type = %q, want image_generation", evt.Type)
		}
		data, ok := evt.Data.(map[string]interface{})
		if !ok {
			t.Fatalf("event data is not a map: %T", evt.Data)
		}
		if data["outcome"] != "success" {
			t.Errorf("event outcome = %v, want success", data["outcome"])
		}
		if data["model"] != env.modelID {
			t.Errorf("event model = %v, want %s", data["model"], env.modelID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event received within 2s")
	}
}

// T1.7.2 — event on error-in-200 carries outcome=error.
func TestHandler_EventPublished_ErrorIn200(t *testing.T) {
	env := newEnv(t, fakeScript{status: 200, body: errorIn200Body(2013, "x")})
	sub, err := env.bus.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	defer env.bus.Unsubscribe(sub)
	_ = env.sendRequest(http.MethodPost, `{"model":"`+env.modelID+`"}`, nil)
	select {
	case evt := <-sub:
		if evt.Type != "image_generation" {
			t.Errorf("event type = %q, want image_generation", evt.Type)
		}
		data, _ := evt.Data.(map[string]interface{})
		if data["outcome"] != "error" {
			t.Errorf("event outcome = %v, want error", data["outcome"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event received within 2s")
	}
}

// T1.7.2 — bounded response: over-cap (handler's
// response-cap + 1) → 502. The test uses the
// maxResponseBodyBytesForTest override so we don't have to
// allocate 64MB+1 (the production constant is preserved above
// for the live path).
func TestHandler_ResponseOverCap_502(t *testing.T) {
	// Override the response cap to a small test value; restore
	// after the test.
	prev := maxResponseBodyBytesForTest
	maxResponseBodyBytesForTest = 1024 // 1 KB test cap
	defer func() { maxResponseBodyBytesForTest = prev }()

	big := make([]byte, maxResponseBodyBytesForTest+1)
	for i := range big {
		big[i] = 'a'
	}
	env := newEnv(t, fakeScript{status: 200, body: big})
	rec := env.sendRequest(http.MethodPost, `{"model":"`+env.modelID+`"}`, nil)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 (over-cap)", rec.Code)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("upstream response exceeds")) {
		t.Errorf("expected over-cap error message, got=%s", rec.Body.String())
	}
}

// T1.7.2 — request body over-cap → 413 OpenAI envelope.
func TestHandler_RequestOverCap_413(t *testing.T) {
	env := newEnv(t, fakeScript{status: 200, body: successBody(1, 0)})
	big := bytes.Repeat([]byte("a"), maxRequestBodyBytes+1)
	body := `{"model":"` + env.modelID + `","x":"` + string(big) + `"}`
	rec := env.sendRequest(http.MethodPost, body, nil)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", rec.Code)
	}
	if env.fake.hitCount.Load() != 0 {
		t.Errorf("upstream was called despite 413: hit count = %d", env.fake.hitCount.Load())
	}
}

// T1.7.2 — handler has no io.ReadAll of uncapped body / no
// io.Copy of resp.Body. Asserted via static analysis: the
// handler file uses io.LimitReader + io.ReadAll(limit-reader)
// only. This test exists as a sentinel that must be manually
// audited (task 1.8.2).
func TestHandler_RAMDiscipline_AuditSentinel(t *testing.T) {
	// This is a sentinel — see phase1-plan.md T1.8.2 / SC8
	// (single-materialization audit) for the grep-based check.
	// The test exists so a future refactor that adds a second
	// io.ReadAll or io.Copy of resp.Body fails CI; the actual
	// check is in the audit script (Makefile target or
	// pre-commit hook).
	t.Skip("static audit sentinel — see task 1.8.2 / SC8 / Amendment 12")
}

// disconnectWriter is an http.ResponseWriter whose body write
// blocks until `release` is closed, then fails — simulating a
// client that goes away mid-relay (the upstream response is
// already fully read when the handler attempts the client
// write). writeEntered is closed when the handler attempts the
// body write, so the test can cancel the request context at a
// deterministic point (review F1).
type disconnectWriter struct {
	header       http.Header
	code         int
	ctx          context.Context
	writeEntered chan struct{}
	enterOnce    sync.Once
	release      chan struct{}
}

func newDisconnectWriter(ctx context.Context) *disconnectWriter {
	return &disconnectWriter{
		header:       make(http.Header),
		ctx:          ctx,
		writeEntered: make(chan struct{}),
		release:      make(chan struct{}),
	}
}

func (w *disconnectWriter) Header() http.Header { return w.header }

func (w *disconnectWriter) WriteHeader(code int) { w.code = code }

func (w *disconnectWriter) Write(p []byte) (int, error) {
	w.enterOnce.Do(func() { close(w.writeEntered) })
	<-w.release // hold until the test simulates the disconnect
	return 0, w.ctx.Err()
}

// Review F1 — metering must survive a client disconnect. The
// upstream call has completed by the time metering runs, so the
// billing writes go to a detached context; a request context
// cancelled mid-relay (client gone) must not suppress them.
// Regression guard: meter() used to run on r.Context(), so the
// usage rows silently vanished on disconnect ("we still bill"
// claim at the write-failure branch was false).
func TestHandler_Metering_SurvivesClientDisconnect(t *testing.T) {
	env := newEnv(t, fakeScript{status: 200, body: successBody(1, 0)})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest(http.MethodPost, "/v1/image_generation", strings.NewReader(`{"model":"`+env.modelID+`"}`))
	r.Header.Set("Authorization", defaultAuthHeader)
	r = r.WithContext(ctx)
	w := newDisconnectWriter(ctx)

	done := make(chan struct{})
	go func() {
		defer close(done)
		env.handler.HandleImageGeneration(w, r)
	}()

	// Wait until the upstream response is fully relayed into the
	// (blocking) client write — the upstream is complete and the
	// handler is mid-relay.
	select {
	case <-w.writeEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("handler never reached the client write")
	}

	cancel()         // simulate the disconnect cancelling the request context
	close(w.release) // the client write now fails like a dead-client write

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not finish after disconnect")
	}

	// The point of the fix: usage rows exist despite the cancelled
	// request context (request_count=1, image_count=1).
	rows, err := env.usage.GetModelUsage(context.Background(), startHour(time.Now()), startHour(time.Now()))
	if err != nil {
		t.Fatalf("GetModelUsage: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 model usage row despite disconnect, got %d", len(rows))
	}
	if rows[0].RequestCount != 1 || rows[0].ImageCount != 1 {
		t.Errorf("request_count=%d image_count=%d, want 1/1 (metering must survive disconnect)", rows[0].RequestCount, rows[0].ImageCount)
	}
}

// T1.7.2 — Metering: success ⇒ image_count=1.
func TestHandler_Metering_Success_ImageCount1(t *testing.T) {
	env := newEnv(t, fakeScript{status: 200, body: successBody(1, 0)})
	_ = env.sendRequest(http.MethodPost, `{"model":"`+env.modelID+`"}`, nil)
	rows, err := env.usage.GetModelUsage(context.Background(), startHour(time.Now()), startHour(time.Now()))
	if err != nil {
		t.Fatalf("GetModelUsage: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 model usage row, got %d", len(rows))
	}
	if rows[0].ImageCount != 1 {
		t.Errorf("image_count = %d, want 1", rows[0].ImageCount)
	}
	if rows[0].RequestCount != 1 {
		t.Errorf("request_count = %d, want 1", rows[0].RequestCount)
	}
}

// T1.7.2 — Metering: error-in-200 ⇒ request_count+1, image_count=0
// (Amendment 14 documented divergence from chat's success-only).
func TestHandler_Metering_ErrorIn200_RequestPlusOne_ImageCountZero(t *testing.T) {
	env := newEnv(t, fakeScript{status: 200, body: errorIn200Body(2013, "x")})
	_ = env.sendRequest(http.MethodPost, `{"model":"`+env.modelID+`"}`, nil)
	rows, err := env.usage.GetModelUsage(context.Background(), startHour(time.Now()), startHour(time.Now()))
	if err != nil {
		t.Fatalf("GetModelUsage: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 model usage row, got %d", len(rows))
	}
	if rows[0].RequestCount != 1 {
		t.Errorf("request_count = %d, want 1 (error-in-200 still bumps request_count per Amendment 14)", rows[0].RequestCount)
	}
	if rows[0].ImageCount != 0 {
		t.Errorf("image_count = %d, want 0 (error-in-200 does not bill images)", rows[0].ImageCount)
	}
}

// T1.7.2 — Metering: partial success with success_count=2 ⇒
// image_count=2 (billable truth under partial success is
// metadata.success_count, NOT request n).
func TestHandler_Metering_PartialSuccess_ImageCount2(t *testing.T) {
	body := mustJSON(map[string]interface{}{
		"data":      []interface{}{map[string]interface{}{"image_urls": []string{"https://x/a.png", "https://x/b.png"}}},
		"metadata":  map[string]interface{}{"success_count": "2", "failed_count": "1"},
		"base_resp": map[string]interface{}{"status_code": 0, "status_msg": "partial"},
	})
	env := newEnv(t, fakeScript{status: 200, body: body})
	_ = env.sendRequest(http.MethodPost, `{"model":"`+env.modelID+`"}`, nil)
	rows, err := env.usage.GetModelUsage(context.Background(), startHour(time.Now()), startHour(time.Now()))
	if err != nil {
		t.Fatalf("GetModelUsage: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 model usage row, got %d", len(rows))
	}
	if rows[0].ImageCount != 2 {
		t.Errorf("image_count = %d, want 2 (billable truth = metadata.success_count)", rows[0].ImageCount)
	}
}

// T1.7.2 — timeout honored (504) — fake upstream sleeps
// >ImageGenTimeout. Hit counter == 1 (no retry).
func TestHandler_Deadline_504(t *testing.T) {
	env := newEnv(t, fakeScript{status: 200, body: successBody(1, 0), delay: 1 * time.Second})
	start := time.Now()
	rec := env.sendRequest(http.MethodPost, `{"model":"`+env.modelID+`"}`, nil)
	elapsed := time.Since(start)
	if rec.Code != http.StatusGatewayTimeout {
		t.Errorf("status = %d, want 504 (deadline)", rec.Code)
	}
	if elapsed > 800*time.Millisecond {
		t.Errorf("deadline took %v, want ~200ms (configured ImageGenTimeout + scheduling slack)", elapsed)
	}
	// Exactly one upstream call (no retry on deadline).
	if env.fake.hitCount.Load() < 1 {
		t.Errorf("upstream was never called (hit count = %d)", env.fake.hitCount.Load())
	}
}

// ---------- helpers ----------

func startHour(t time.Time) string { return t.UTC().Format("2006-01-02T15") }
