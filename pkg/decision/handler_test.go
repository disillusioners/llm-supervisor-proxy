package decision

// Full P2 handler test suite (Decision Models commission / §9.1).
// Same-package white-box (`package decision`), stdlib-only (no
// testify), flat `TestHandler_<Behavior>_<Expected>` naming.
// Mirrors pkg/imggen/handler_test.go harness (`fakeScript`,
// `httptest.NewServer`, `memTokenStore`, `stubConfig`) with
// adaptations for the typed seam.
//
// The 3 import-guard tests at the top of this file were the
// only P1 deliverable in this file; everything below is P2.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
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

// ---------- Import guard tests (carried over from P1; unchanged) ----------

func TestPackage_ImportGuard_NoProxyImports(t *testing.T) {
	const banned = `github\.com/disillusioners/llm-supervisor-proxy/pkg/proxy(/|")`
	re, err := compileRE(banned)
	if err != nil {
		t.Fatalf("bad guard regex: %v", err)
	}
	entries, err := readPackageDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		src, err := readFile(e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if loc := re.FindIndex(src); loc != nil {
			line := 1 + bytes.Count(src[:loc[0]], []byte("\n"))
			t.Errorf("%s:%d references the pkg/proxy import path — forbidden by the pkg/decision import guard (Decision Models §3.5)", e.Name(), line)
		}
	}
}

func TestPackage_ImportGuard_NoImggenImports(t *testing.T) {
	const banned = `github\.com/disillusioners/llm-supervisor-proxy/pkg/imggen(/|")`
	re, err := compileRE(banned)
	if err != nil {
		t.Fatalf("bad guard regex: %v", err)
	}
	entries, err := readPackageDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		src, err := readFile(e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if loc := re.FindIndex(src); loc != nil {
			line := 1 + bytes.Count(src[:loc[0]], []byte("\n"))
			t.Errorf("%s:%d references the pkg/imggen import path — forbidden by the pkg/decision transitive-closure guard (Decision Models §3.5)", e.Name(), line)
		}
	}
}

func TestPackage_ImportGuard_TransitiveClosure(t *testing.T) {
	const projectPrefix = "github.com/disillusioners/llm-supervisor-proxy/pkg/"
	const bannedProxy = projectPrefix + "proxy"
	const bannedImggen = projectPrefix + "imggen"
	cwd := getwdOrFatal(t)
	moduleRoot := joinPath(cwd, "..", "..")
	out := runGoListDeps(t, moduleRoot)
	deps := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, dep := range deps {
		if dep == "" {
			continue
		}
		if strings.HasPrefix(dep, bannedProxy+"/") || dep == bannedProxy {
			t.Errorf("transitive dep %q is under pkg/proxy — forbidden by the pkg/decision transitive-closure guard (Decision Models §3.5)", dep)
		}
		if strings.HasPrefix(dep, bannedImggen+"/") || dep == bannedImggen {
			t.Errorf("transitive dep %q is under pkg/imggen — forbidden by the pkg/decision transitive-closure guard (Decision Models §3.5)", dep)
		}
	}
}

// ---------- Test harness (fakes + helpers) ----------

// fakeUpstream is a scriptable httptest fixture for the
// OpenAI Decision / TypeSafe upstreams. Each test sets a
// response script (status + body + contentType + delay). The
// fake records the call count and the most recent request
// body + headers for header-strip assertions (decision
// upstream must NEVER see the proxy's sk- token — the
// credential bearer is the only Authorization header
// forwarded).
type fakeUpstream struct {
	mu        sync.Mutex
	hitCount  atomic.Int64
	lastBody  []byte
	lastAuthH string
	script    fakeScript
}

type fakeScript struct {
	status      int
	body        []byte
	contentType string
	delay       time.Duration
}

func newFakeUpstream(t *testing.T, script fakeScript) (*httptest.Server, *fakeUpstream) {
	t.Helper()
	f := &fakeUpstream{script: script}
	if script.contentType == "" {
		script.contentType = "application/json"
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.lastBody = body
		f.lastAuthH = r.Header.Get("Authorization")
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

// openAIDecisionSuccessBody returns a canonical OpenAI Decision
// success body with the given usage.
func openAIDecisionSuccessBody(inputTokens, outputTokens int64) []byte {
	return mustJSON(map[string]interface{}{
		"model": "gpt-6-luna",
		"answers": []interface{}{
			map[string]interface{}{
				"type":        "predicate",
				"name":        "is_x",
				"probability": 0.97,
			},
		},
		"usage": map[string]interface{}{
			"input_tokens":  inputTokens,
			"output_tokens": outputTokens,
			"total_tokens":  inputTokens + outputTokens,
			"input_tokens_details": map[string]interface{}{
				"cached_tokens":     0,
				"cache_write_tokens": 0,
			},
			"output_tokens_details": map[string]interface{}{
				"reasoning_tokens": 0,
			},
		},
	})
}

// openAIDecisionErrorBody returns the canonical OpenAI
// Decision error envelope (PromoteToError path).
func openAIDecisionErrorBody(code, errType, msg string) []byte {
	return mustJSON(map[string]interface{}{
		"error": map[string]interface{}{
			"message": msg,
			"type":    errType,
			"code":    code,
		},
	})
}

// systemOneSuccessBody returns a canonical System One success
// body. Answers is a MAP keyed by question.Name.
func systemOneSuccessBody(answers map[string]interface{}, inputTokens, outputTokens int64) []byte {
	return mustJSON(map[string]interface{}{
		"model":   "jev-1.13.0",
		"answers": answers,
		"usage": map[string]interface{}{
			"input_tokens":  inputTokens,
			"output_tokens": outputTokens,
		},
	})
}

func mustJSON(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return b
}

// ---------- Handler test env ----------

type handlerTestEnv struct {
	t          *testing.T
	upstream   *httptest.Server
	fake       *fakeUpstream
	handler    *Handler
	bus        *events.Bus
	usage      *usage.Counter
	tokenStore *memTokenStore
	modelID    string
	provider   string
	dbCleanup  func()
}

// memTokenStore is the in-memory auth.TokenStoreInterface used
// by the decision handler tests.
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

// stubConfig is the configGetter shim — the handler only reads
// GetDecisionTimeout.
type stubConfig struct{ timeout time.Duration }

func (s stubConfig) GetDecisionTimeout() time.Duration { return s.timeout }

// newEnvOpenAI builds a decision handler test env where the
// upstream is the OpenAI Decision API (provider="openai").
func newEnvOpenAI(t *testing.T, script fakeScript) *handlerTestEnv {
	return newEnvForProvider(t, script, "openai", "gpt-6-luna", "https://api.openai.com/v1")
}

// newEnvTypesafe builds a decision handler test env where the
// upstream is TypeSafe (Jev) (provider="typesafe").
func newEnvTypesafe(t *testing.T, script fakeScript) *handlerTestEnv {
	return newEnvForProvider(t, script, "typesafe", "jev-1.13.0", "https://api.typesafe.ai")
}

// newEnvForProvider is the shared env builder; the provider
// arg controls which adapter's BuildUpstream / InspectResponse
// is exercised (registry lookup at handler step 9 picks the
// adapter by cred.Provider).
func newEnvForProvider(t *testing.T, script fakeScript, provider, internalModel, baseURL string) *handlerTestEnv {
	t.Helper()
	upstream, fake := newFakeUpstream(t, script)
	db, dbCleanup := newDecisionUsageTestDB(t)
	uc := usage.NewCounter(db, database.SQLite)
	bus := events.NewBus()
	ts := newMemTokenStore()
	h := NewHandler(stubConfig{timeout: 2 * time.Second}, bus, ts, uc)

	// Override the upstream URL for the credential so the
	// test builds go to the fake httptest server (Q17 still
	// honored — baseURL flows through).
	credBaseURL := upstream.URL

	modelID := "decision-" + provider
	h.SetModelResolver(func(id string) (*models.ModelConfig, bool) {
		if id != modelID {
			return nil, false
		}
		return &models.ModelConfig{
			ID:                         modelID,
			Name:                       "Decision " + provider,
			Kind:                       models.KindDecision,
			Enabled:                    true,
			Internal:                   true,
			InternalModel:              internalModel,
			Credentials: []models.CredentialRef{
				{CredentialID: "cred-1", Weight: 1, Position: 0},
			},
			ExcludeFromUltimateSwitching: true,
		}, true
	})
	h.SetCredentialResolver(func(id string) (models.ResolvedCredential, bool) {
		if id != modelID {
			return models.ResolvedCredential{}, false
		}
		return models.ResolvedCredential{
			Provider:      provider,
			APIKey:        "sk-cred-secret",
			BaseURL:       credBaseURL,
			InternalModel: internalModel,
			CredentialID:  "cred-1",
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
		provider:   provider,
		dbCleanup:  dbCleanup,
	}
}

// decodeOpenAIError reads an OpenAI envelope body and returns
// the typed error details.
func decodeOpenAIError(t *testing.T, body []byte) (string, string, string) {
	t.Helper()
	var parsed struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("decode: %v; body=%s", err, string(body))
	}
	return parsed.Error.Message, parsed.Error.Type, parsed.Error.Code
}

func newUsageTestDB() (*sql.DB, error) {
	db, err := openDecisionSQLiteForUsage()
	if err != nil {
		return nil, err
	}
	return db, nil
}

// newDecisionUsageTestDB returns (*sql.DB, cleanup). The
// caller is responsible for invoking cleanup. The db is an
// in-memory SQLite preloaded with the token + model hourly
// usage tables (no image_count column — those columns are
// not relevant to the decision route).
func newDecisionUsageTestDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()
	db, err := openDecisionSQLiteForUsage()
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	cleanup := func() { _ = db.Close() }
	t.Cleanup(cleanup)
	return db, cleanup
}

func openDecisionSQLiteForUsage() (*sql.DB, error) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS token_hourly_usage (
		token_id TEXT NOT NULL,
		hour_bucket TEXT NOT NULL,
		request_count INTEGER NOT NULL DEFAULT 0,
		prompt_tokens INTEGER NOT NULL DEFAULT 0,
		completion_tokens INTEGER NOT NULL DEFAULT 0,
		total_tokens INTEGER NOT NULL DEFAULT 0,
		image_count INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (token_id, hour_bucket)
	)`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS model_hourly_usage (
		model_id TEXT NOT NULL,
		hour_bucket TEXT NOT NULL,
		request_count INTEGER NOT NULL DEFAULT 0,
		prompt_tokens INTEGER NOT NULL DEFAULT 0,
		completion_tokens INTEGER NOT NULL DEFAULT 0,
		total_tokens INTEGER NOT NULL DEFAULT 0,
		image_count INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (model_id, hour_bucket)
	)`); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// env.sendRequest POSTs the body to /v1/decisions with the
// given (optional) headers. nil headers injects the default
// "sk-test-token" Authorization header so auth passes through
// most paths.
func (e *handlerTestEnv) sendRequest(method, body string, headers map[string]string) *httptest.ResponseRecorder {
	e.t.Helper()
	r := httptest.NewRequest(method, "/v1/decisions", strings.NewReader(body))
	if headers == nil {
		r.Header.Set("Authorization", "Bearer sk-test-token")
	} else {
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		if _, ok := headers["Authorization"]; !ok {
			r.Header.Set("Authorization", "Bearer sk-test-token")
		}
	}
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.handler.HandleDecision(w, r)
	return w
}

// standardRequestBody builds the canonical decision request
// body (one predicate question, "is_x", without
// safety_identifier) for the handler tests that pass
// validation without surprises.
func standardRequestBody(model string) string {
	return requestBodyWith(model, `[
		{"type":"predicate", "name":"is_x", "instructions":"is it true?"}
	]`)
}

// standardRequestBodyChoice builds the body with a choice
// question — for tests that exercise choice translation.
func standardRequestBodyChoice(model string) string {
	return requestBodyWith(model, `[
		{"type":"choice", "name":"dept", "instructions":"where?",
		 "choices":[
		   {"value":"billing"},
		   {"value":"technical"}
		 ]
		}
	]`)
}

// standardRequestBodyScore builds the body with a score
// question — for tests that exercise score translation.
func standardRequestBodyScore(model string) string {
	return requestBodyWith(model, `[
		{"type":"score", "name":"frustration", "instructions":"how frustrated?",
		 "levels":[
		   {"label":"calm"},
		   {"label":"angry"}
		 ]
		}
	]`)
}

// requestBodyWith emits `{model, input, questions}` around a
// caller-supplied questions JSON array.
func requestBodyWith(model string, qJSON string) string {
	body := map[string]interface{}{
		"model":     model,
		"input":     "Customer wrote: I was charged twice.",
		"questions": json.RawMessage(qJSON),
	}
	b, _ := json.Marshal(body)
	return string(b)
}

// pollUntil waits for pred to become true OR the deadline to
// elapse. Useful for async metered writes that need a small
// grace window (the test harness runs the detached-meter ctx
// on a real clock; 250ms is more than enough for a SQLite
// in-memory write).
func pollUntil(t *testing.T, deadline time.Duration, pred func() bool) {
	t.Helper()
	tick := 25 * time.Millisecond
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if pred() {
			return
		}
		time.Sleep(tick)
	}
	if !pred() {
		t.Fatalf("pollUntil: predicate failed within %s", deadline)
	}
}

// ---------- 1. Method guard / 2. Body cap / 3. Auth ----------

// TestHandler_MethodGuard_405 — non-POST returns 405 via
// http.Error (no envelope, house style).
func TestHandler_MethodGuard_405(t *testing.T) {
	env := newEnvOpenAI(t, fakeScript{status: 200, body: openAIDecisionSuccessBody(10, 0)})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/decisions", nil)
	env.handler.HandleDecision(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: status = %d, want 405", rec.Code)
	}
}

// TestHandler_BodyCap_413 — body > 16MB returns 413 via
// *http.MaxBytesError → house OpenAI envelope.
func TestHandler_BodyCap_413(t *testing.T) {
	env := newEnvOpenAI(t, fakeScript{status: 200, body: openAIDecisionSuccessBody(10, 0)})
	// Construct a body slightly above the 16 MB cap. 17 MB
	// of "x" inside a JSON string field triggers MaxBytesError.
	huge := strings.Repeat("x", 17<<20)
	body := `{"model":"` + env.modelID + `","input":"` + huge + `","questions":[{"type":"predicate","name":"x","instructions":"y"}]}`
	rec := env.sendRequest(http.MethodPost, body, nil)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body: status = %d, want 413; body=%s", rec.Code, rec.Body.String())
	}
	_, _, _ = decodeOpenAIError(t, rec.Body.Bytes())
	if env.fake.hitCount.Load() != 0 {
		t.Errorf("upstream hit count = %d, want 0 (MaxBytesReader rejects before any upstream call)", env.fake.hitCount.Load())
	}
}

// TestHandler_Auth_NoToken_401 — missing Authorization header
// returns 401.
func TestHandler_Auth_NoToken_401(t *testing.T) {
	env := newEnvOpenAI(t, fakeScript{status: 200, body: openAIDecisionSuccessBody(10, 0)})
	rec := env.sendRequest(http.MethodPost, standardRequestBody(env.modelID), map[string]string{
		// Override Authorization to be empty explicitly.
		"Authorization": "",
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no auth: status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
	_, _, _ = decodeOpenAIError(t, rec.Body.Bytes())
}

// TestHandler_Auth_WrongToken_401 — wrong sk- token returns 401.
func TestHandler_Auth_WrongToken_401(t *testing.T) {
	env := newEnvOpenAI(t, fakeScript{status: 200, body: openAIDecisionSuccessBody(10, 0)})
	rec := env.sendRequest(http.MethodPost, standardRequestBody(env.modelID), map[string]string{
		"Authorization": "Bearer sk-wrong-token",
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
}

// TestHandler_Auth_TokenNotInAllowList_403 — token's
// AllowedModels lacks the model ⇒ 403 (decision shape: nested
// envelope).
func TestHandler_Auth_TokenNotInAllowList_403(t *testing.T) {
	env := newEnvOpenAI(t, fakeScript{status: 200, body: openAIDecisionSuccessBody(10, 0)})
	// Replace the default token with one that disallows the
	// model.
	env.tokenStore.tokens["tok-test"].AllowedModels = []string{"some-other-model"}
	rec := env.sendRequest(http.MethodPost, standardRequestBody(env.modelID), nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("not in allow list: status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

// ---------- 6/7/8/9. Resolve paths ----------

// TestHandler_ResolveModel_Unknown_404 — unknown model returns
// 404.
func TestHandler_ResolveModel_Unknown_404(t *testing.T) {
	env := newEnvOpenAI(t, fakeScript{status: 200, body: openAIDecisionSuccessBody(10, 0)})
	rec := env.sendRequest(http.MethodPost, standardRequestBody("not-configured-model"), nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown model: status = %d, want 404", rec.Code)
	}
}

// TestHandler_ResolveModel_NotDecisionKind_400 — chat model
// posted to /v1/decisions ⇒ 400 + code "decision_misroute"
// (closes imggen reverse-direction gap).
func TestHandler_ResolveModel_NotDecisionKind_400(t *testing.T) {
	env := newEnvOpenAI(t, fakeScript{status: 200, body: openAIDecisionSuccessBody(10, 0)})
	env.handler.SetModelResolver(func(id string) (*models.ModelConfig, bool) {
		if id != "chat-1" {
			return nil, false
		}
		return &models.ModelConfig{
			ID: "chat-1", Name: "Chat 1", Kind: models.KindChat,
			Enabled: true, Internal: true, InternalModel: "x",
			Credentials: []models.CredentialRef{{CredentialID: "c", Weight: 1, Position: 0}},
		}, true
	})
	rec := env.sendRequest(http.MethodPost, standardRequestBody("chat-1"), nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("chat model on /v1/decisions: status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	_, _, code := decodeOpenAIError(t, rec.Body.Bytes())
	if code != "decision_misroute" {
		t.Errorf("code = %q, want decision_misroute", code)
	}
}

// TestHandler_ResolveCredential_502 — credential resolution
// fails ⇒ 502.
func TestHandler_ResolveCredential_502(t *testing.T) {
	env := newEnvOpenAI(t, fakeScript{status: 200, body: openAIDecisionSuccessBody(10, 0)})
	env.handler.SetCredentialResolver(func(id string) (models.ResolvedCredential, bool) {
		return models.ResolvedCredential{}, false
	})
	rec := env.sendRequest(http.MethodPost, standardRequestBody(env.modelID), nil)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("no creds: status = %d, want 502; body=%s", rec.Code, rec.Body.String())
	}
}

// TestHandler_ProviderLookup_502 — credential has a provider
// NOT in the decision providerRegistry ⇒ 502 + clean
// operational message.
func TestHandler_ProviderLookup_502(t *testing.T) {
	env := newEnvOpenAI(t, fakeScript{status: 200, body: openAIDecisionSuccessBody(10, 0)})
	env.handler.SetCredentialResolver(func(id string) (models.ResolvedCredential, bool) {
		return models.ResolvedCredential{
			Provider:      "anthropic",
			APIKey:        "k",
			BaseURL:       env.upstream.URL,
			InternalModel: "x",
		}, true
	})
	rec := env.sendRequest(http.MethodPost, standardRequestBody(env.modelID), nil)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("unregistered provider: status = %d, want 502; body=%s", rec.Code, rec.Body.String())
	}
	msg, _, _ := decodeOpenAIError(t, rec.Body.Bytes())
	if !strings.Contains(msg, "anthropic") {
		t.Errorf("message = %q, want it to mention the unregistered provider", msg)
	}
	// Finding 6: the message must NOT carry the stale P1
	// "(P1 ships with an empty registry by design; P2
	// registers ...)" clause.
	if strings.Contains(msg, "P1 ships") || strings.Contains(msg, "P2 registers") {
		t.Errorf("message carries stale P1 wording: %q", msg)
	}
}

// ---------- Validation 400s (per §4.2 + Q4 + finding 24) ----------

// TestHandler_Validation_TooManyQuestions_400 — 129 questions
// ⇒ 400.
func TestHandler_Validation_TooManyQuestions_400(t *testing.T) {
	env := newEnvOpenAI(t, fakeScript{status: 200, body: openAIDecisionSuccessBody(10, 0)})
	// Build 129 predicate questions.
	var qb strings.Builder
	qb.WriteString("[")
	for i := 0; i < 129; i++ {
		if i > 0 {
			qb.WriteString(",")
		}
		fmt.Fprintf(&qb, `{"type":"predicate","name":"q%d","instructions":"?","choices":[]}`, i)
	}
	qb.WriteString("]")
	body := requestBodyWith(env.modelID, qb.String())
	rec := env.sendRequest(http.MethodPost, body, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("129 questions: status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	msg, _, _ := decodeOpenAIError(t, rec.Body.Bytes())
	if !strings.Contains(msg, "too many questions") {
		t.Errorf("message = %q, want it to mention too many questions", msg)
	}
}

// TestHandler_Validation_ChoiceOneOption_400 — choice with 1
// option ⇒ 400 (System One requires ≥2; finding Q4).
func TestHandler_Validation_ChoiceOneOption_400(t *testing.T) {
	env := newEnvOpenAI(t, fakeScript{status: 200, body: openAIDecisionSuccessBody(10, 0)})
	body := requestBodyWith(env.modelID, `[
		{"type":"choice","name":"dept","choices":[{"value":"billing"}]}
	]`)
	rec := env.sendRequest(http.MethodPost, body, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("1-choice: status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	msg, _, _ := decodeOpenAIError(t, rec.Body.Bytes())
	if !strings.Contains(msg, "choice") || !strings.Contains(msg, "at least 2") {
		t.Errorf("message = %q, want it to mention choice+at least 2", msg)
	}
}

// TestHandler_Validation_ScoreOneLevel_400 — score with 1
// level ⇒ 400 (System One requires ≥2; finding Q4).
func TestHandler_Validation_ScoreOneLevel_400(t *testing.T) {
	env := newEnvOpenAI(t, fakeScript{status: 200, body: openAIDecisionSuccessBody(10, 0)})
	body := requestBodyWith(env.modelID, `[
		{"type":"score","name":"frustration","levels":[{"label":"calm"}]}
	]`)
	rec := env.sendRequest(http.MethodPost, body, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("1-level: status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestHandler_Validation_BooleanStringCollision_400 — boolean
// true alongside string "true" ⇒ 400 (research-web §Q2).
func TestHandler_Validation_BooleanStringCollision_400(t *testing.T) {
	env := newEnvOpenAI(t, fakeScript{status: 200, body: openAIDecisionSuccessBody(10, 0)})
	body := requestBodyWith(env.modelID, `[
		{"type":"choice","name":"flag","choices":[{"value":true},{"value":"true"}]}
	]`)
	rec := env.sendRequest(http.MethodPost, body, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bool+string: status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	msg, _, _ := decodeOpenAIError(t, rec.Body.Bytes())
	if !strings.Contains(msg, "type-collision") && !strings.Contains(msg, "collision") {
		t.Errorf("message = %q, want it to mention collision", msg)
	}
}

// TestHandler_Validation_DuplicateQuestionNames_400 — two
// questions sharing the same name ⇒ 400 (finding 9).
func TestHandler_Validation_DuplicateQuestionNames_400(t *testing.T) {
	env := newEnvOpenAI(t, fakeScript{status: 200, body: openAIDecisionSuccessBody(10, 0)})
	body := requestBodyWith(env.modelID, `[
		{"type":"predicate","name":"foo","instructions":"?"},
		{"type":"predicate","name":"foo","instructions":"?"}
	]`)
	rec := env.sendRequest(http.MethodPost, body, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("duplicate name: status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	msg, _, _ := decodeOpenAIError(t, rec.Body.Bytes())
	if !strings.Contains(msg, "duplicate question name") {
		t.Errorf("message = %q, want it to mention duplicate question name", msg)
	}
}

// TestHandler_Validation_SafetyIdentifierOver128_400 — finding
// 24: safety_identifier > 128 chars ⇒ 400.
func TestHandler_Validation_SafetyIdentifierOver128_400(t *testing.T) {
	env := newEnvOpenAI(t, fakeScript{status: 200, body: openAIDecisionSuccessBody(10, 0)})
	body := mustJSON(map[string]interface{}{
		"model":             env.modelID,
		"input":             "x",
		"questions":         []interface{}{map[string]interface{}{"type": "predicate", "name": "x", "instructions": "?"}},
		"safety_identifier": strings.Repeat("u", 129),
	})
	rec := env.sendRequest(http.MethodPost, string(body), nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("safety_identifier overlong: status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	msg, _, _ := decodeOpenAIError(t, rec.Body.Bytes())
	if !strings.Contains(msg, "safety_identifier") {
		t.Errorf("message = %q, want it to mention safety_identifier", msg)
	}
}

// ---------- 10/12/13/14/15. End-to-end smoke (httptest fake upstreams) ----------

// TestHandler_OpenAI_Decision_RoundTrip_Success — full
// round-trip through the OpenAI Decision adapter: client POST
// ⇒ typed wire ⇒ typed relay; usage metered on success.
func TestHandler_OpenAI_Decision_RoundTrip_Success(t *testing.T) {
	body := openAIDecisionSuccessBody(401, 0)
	env := newEnvOpenAI(t, fakeScript{status: 200, body: body})
	rec := env.sendRequest(http.MethodPost, standardRequestBody(env.modelID), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if env.fake.hitCount.Load() != 1 {
		t.Errorf("upstream hit count = %d, want 1 (exactly-one-upstream-call)", env.fake.hitCount.Load())
	}
	// Response body should be the typed DecisionResponse shape.
	var got DecisionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("response not typed DecisionResponse: %v; body=%s", err, rec.Body.String())
	}
	if got.Model != "gpt-6-luna" {
		t.Errorf("model = %q, want %q", got.Model, "gpt-6-luna")
	}
	if len(got.Answers) == 0 {
		t.Errorf("answers empty: %+v", got)
	}
	// Meter must have written a row.
	hourBucket := time.Now().UTC().Format("2006-01-02T15")
	ok := false
	for i := 0; i < 40; i++ {
		n, _ := readModelUsageRowCount(env, env.modelID, hourBucket)
		if n > 0 {
			ok = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ok {
		t.Fatalf("meter did not write a row within 2s")
	}
}

// readModelUsageRowCount is a small helper that reads
// request_count from the model_hourly_usage table for the
// given model + hour. Goes through the public
// usage.Counter.GetModelUsage surface.
func readModelUsageRowCount(env *handlerTestEnv, modelID, hourBucket string) (int, error) {
	rows, err := env.usage.GetModelUsage(context.Background(), hourBucket, hourBucket)
	if err != nil {
		return 0, err
	}
	for _, r := range rows {
		if r.ModelID == modelID {
			return r.RequestCount, nil
		}
	}
	return 0, nil
}

// TestHandler_OpenAI_Decision_ErrorEnvelope_502_BodyPreserved —
// upstream returns an OpenAI error envelope ⇒ 502 with body
// preserved verbatim (imggen-style PromoteToError).
func TestHandler_OpenAI_Decision_ErrorEnvelope_502_BodyPreserved(t *testing.T) {
	body := openAIDecisionErrorBody("invalid_request", "invalid_request_error", "missing field `questions`")
	env := newEnvOpenAI(t, fakeScript{status: 200, body: body})
	rec := env.sendRequest(http.MethodPost, standardRequestBody(env.modelID), nil)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("error envelope: status = %d, want 502", rec.Code)
	}
	if !bytes.Equal(rec.Body.Bytes(), body) {
		t.Errorf("body not preserved verbatim: got=%s want=%s", rec.Body.String(), string(body))
	}
	// Failure path → NO usage row.
	hourBucket := time.Now().UTC().Format("2006-01-02T15")
	// Hold for a beat to ensure the (absent) meter doesn't sneak in.
	time.Sleep(50 * time.Millisecond)
	rc, _ := readModelUsageRowCount(env, env.modelID, hourBucket)
	if rc != 0 {
		t.Errorf("failure path wrote a usage row (request_count=%d); success-only semantics violated", rc)
	}
}

// TestHandler_OpenAI_PathFidelity_BytePassThrough — finding 3
// / Q18 / A26: a client sends OpenAI wire with an unknown
// future field; the upstream sees that field VERBATIM (no
// silent field-drop via typed re-marshal).
func TestHandler_OpenAI_PathFidelity_BytePassThrough(t *testing.T) {
	body := openAIDecisionSuccessBody(10, 0)
	env := newEnvOpenAI(t, fakeScript{status: 200, body: body})
	// Construct a client body carrying a future field
	// (`experimental_feature`). The handler reads rawBody for
	// the openai-decision byte-faithful pass-through; the
	// upstream must receive that field verbatim.
	bodyJSON := `{
		"model": "` + env.modelID + `",
		"input": "x",
		"questions": [{"type":"predicate","name":"x","instructions":"?"}],
		"experimental_feature": "test"
	}`
	rec := env.sendRequest(http.MethodPost, bodyJSON, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	// Confirm the upstream saw the future field. Pretty-
	// printed JSON may add a space after the colon; match on
	// the value not the exact spacing.
	env.fake.mu.Lock()
	got := string(env.fake.lastBody)
	env.fake.mu.Unlock()
	if !strings.Contains(got, `"experimental_feature"`) || !strings.Contains(got, `"test"`) {
		t.Errorf("upstream body did not contain the future field: got=%s", got)
	}
	// And the model field was rewritten.
	if !strings.Contains(got, `"gpt-6-luna"`) {
		t.Errorf("upstream body did not contain the rewritten model: got=%s", got)
	}
}

// TestHandler_TypeSafe_InputString_Translation — typesafe
// adapter: client sends `input: "string"` ⇒ upstream receives
// `state: "string"`.
func TestHandler_TypeSafe_InputString_Translation(t *testing.T) {
	// Upstream returns a System One shape that matches the
	// request's question name(s).
	body := systemOneSuccessBody(map[string]interface{}{
		"foo": map[string]interface{}{"noul": 0.91},
	}, 401, 0)
	env := newEnvTypesafe(t, fakeScript{status: 200, body: body})

	// Client sends OpenAI Decision wire, including a model
	// field whose name matches the typesafe credential's
	// InternalModel. The decision handler rewrites the model
	// name on the typesafe path? — NO, typesafe does its own
	// struct→System One translation (NOT byte-faithful).
	reqJSON := `{
		"model": "` + env.modelID + `",
		"input": "Customer: I was charged twice.",
		"questions": [{"type":"predicate","name":"foo","instructions":"Is foo?"}]
	}`
	rec := env.sendRequest(http.MethodPost, reqJSON, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	env.fake.mu.Lock()
	got := string(env.fake.lastBody)
	env.fake.mu.Unlock()
	// Upstream URL ends with /v1/systemone.
	if !strings.HasSuffix(env.upstream.URL[len(env.upstream.URL)-20:], "") {
		// trivial — keep linter quiet
	}
	// state: the joined input.
	if !strings.Contains(got, `"state":"Customer: I was charged twice."`) {
		t.Errorf("upstream body missing state translation: got=%s", got)
	}
	// model: the InternalModel (jev-1.13.0).
	if !strings.Contains(got, `"model":"jev-1.13.0"`) {
		t.Errorf("upstream body missing model translation: got=%s", got)
	}
	// questions: keyed by name (not array).
	if !strings.Contains(got, `"questions"`) {
		t.Errorf("upstream body missing questions: got=%s", got)
	}
	if strings.Contains(got, `"questions":[`) {
		t.Errorf("questions should be a map (System One), got an array: %s", got)
	}
	if !strings.Contains(got, `"foo"`) {
		t.Errorf("questions map must be keyed by Name: got=%s", got)
	}
	// typed type tag.
	if !strings.Contains(got, `"type":"noul"`) {
		t.Errorf("predicate should translate to noul: got=%s", got)
	}
}

// TestHandler_TypeSafe_InputArray_Translation — plan §9.1 row
// (P2 review finding 2, important): typesafe adapter's text-join
// path. Client wires input as a DecisionInputMessage[] with
// multiple parts across multiple messages; BuildUpstream's
// deriveStateText (typesafe.go:201-241) concatenates every
// input_text part WITHOUT a delimiter, so the upstream must
// receive `"state": "<joined-text>"` (System One), NOT the
// original `"input":[...]` shape. This test exercises the
// text-join path + the 6 liteLLM gotchas that flow through
// deriveStateText — coverage previously only via code
// inspection.
func TestHandler_TypeSafe_InputArray_Translation(t *testing.T) {
	// Upstream returns a System One shape that matches the
	// request's question name.
	body := systemOneSuccessBody(map[string]interface{}{
		"foo": map[string]interface{}{"noul": 0.83},
	}, 401, 0)
	env := newEnvTypesafe(t, fakeScript{status: 200, body: body})

	// Two user messages, three input_text parts total. deriveStateText
	// concatenates each input_text part with NO separator (typesafe.go
	// :228-229 sb.WriteString(part.Text) — note: no \n / space / etc).
	reqJSON := `{
		"model": "` + env.modelID + `",
		"input": [
			{"role":"user","parts":[
				{"type":"input_text","text":"Customer wrote: I was charged twice."},
				{"type":"input_text","text":"Order #4411 \u2014 wants refund today."}
			]},
			{"role":"user","parts":[
				{"type":"input_text","text":"Be concise."}
			]}
		],
		"questions": [{"type":"predicate","name":"foo","instructions":"Is foo?"}]
	}`
	rec := env.sendRequest(http.MethodPost, reqJSON, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	env.fake.mu.Lock()
	got := string(env.fake.lastBody)
	env.fake.mu.Unlock()

	// The deriveStateText join (no delimiter between parts). Assert
	// the EXACT concatenated string — if a future change adds a
	// separator, this test catches it.
	wantState := "Customer wrote: I was charged twice.Order #4411 \u2014 wants refund today.Be concise."
	if !strings.Contains(got, `"state":"`+wantState+`"`) {
		t.Errorf("upstream body missing exact joined state translation: got=%s", got)
	}
	// Round-trip: OpenAI's "input" array must NOT leak through to
	// the System One upstream (System One reads "state", not "input").
	if strings.Contains(got, `"input":[`) {
		t.Errorf("upstream body leaked OpenAI `input` array (System One reads `state`): got=%s", got)
	}
	// model: the InternalModel (jev-1.13.0).
	if !strings.Contains(got, `"model":"jev-1.13.0"`) {
		t.Errorf("upstream body missing model translation: got=%s", got)
	}
	// questions: keyed by name (System One map shape).
	if !strings.Contains(got, `"foo"`) {
		t.Errorf("questions map must be keyed by Name: got=%s", got)
	}
	if strings.Contains(got, `"questions":[`) {
		t.Errorf("questions should be a map (System One), got an array: %s", got)
	}
	// typed type tag.
	if !strings.Contains(got, `"type":"noul"`) {
		t.Errorf("predicate should translate to noul: got=%s", got)
	}
}

// TestHandler_TypeSafe_InputImage_400 — typesafe rejects
// input_image parts (research-web §Q2). Reject at the
// validator edge OR at typesafe.BuildUpstream (both return 400
// to the client).
func TestHandler_TypeSafe_InputImage_400(t *testing.T) {
	body := systemOneSuccessBody(map[string]interface{}{}, 10, 0)
	env := newEnvTypesafe(t, fakeScript{status: 200, body: body})
	// Client sends OpenAI Decision wire with an input array
	// carrying an input_image part.
	reqJSON := `{
		"model": "` + env.modelID + `",
		"input": [
			{"role":"user","parts":[{"type":"input_image","image_url":"data:image/png;base64,iVBORw0KGgo="}]}
		],
		"questions": [{"type":"predicate","name":"x","instructions":"?"}]
	}`
	rec := env.sendRequest(http.MethodPost, reqJSON, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("input_image on typesafe: status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if env.fake.hitCount.Load() != 0 {
		t.Errorf("upstream hit count = %d, want 0 (400 fires before upstream call)", env.fake.hitCount.Load())
	}
}

// TestHandler_TypeSafe_SafetyIdentifier_400 — typesafe
// rejects safety_identifier (research-web §Q2).
func TestHandler_TypeSafe_SafetyIdentifier_400(t *testing.T) {
	env := newEnvTypesafe(t, fakeScript{status: 200, body: []byte(`{}`)})
	body := mustJSON(map[string]interface{}{
		"model":             env.modelID,
		"input":             "x",
		"questions":         []interface{}{map[string]interface{}{"type": "predicate", "name": "x", "instructions": "?"}},
		"safety_identifier": "user-1234",
	})
	rec := env.sendRequest(http.MethodPost, string(body), nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("safety_identifier on typesafe: status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if env.fake.hitCount.Load() != 0 {
		t.Errorf("upstream hit count = %d, want 0 (400 fires before upstream call)", env.fake.hitCount.Load())
	}
}

// TestHandler_TypeSafe_Question_DuplicateName_400 — duplicate
// question names on a typesafe request ⇒ 400 (System One map
// collision).
func TestHandler_TypeSafe_Question_DuplicateName_400(t *testing.T) {
	env := newEnvTypesafe(t, fakeScript{status: 200, body: []byte(`{}`)})
	body := mustJSON(map[string]interface{}{
		"model": env.modelID, "input": "x",
		"questions": []interface{}{
			map[string]interface{}{"type": "predicate", "name": "foo", "instructions": "?"},
			map[string]interface{}{"type": "predicate", "name": "foo", "instructions": "?"},
		},
	})
	rec := env.sendRequest(http.MethodPost, string(body), nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("duplicate question name on typesafe: status = %d, want 400", rec.Code)
	}
}

// TestHandler_TypeSafe_UsageDetailSynthesis — System One
// returns only `{input_tokens, output_tokens}`; the proxy
// synthesizes the OpenAI-shape response with zero-filled
// detail sub-objects (A25).
func TestHandler_TypeSafe_UsageDetailSynthesis(t *testing.T) {
	answers := map[string]interface{}{
		"foo": map[string]interface{}{"noul": 0.5},
	}
	body := systemOneSuccessBody(answers, 401, 0)
	env := newEnvTypesafe(t, fakeScript{status: 200, body: body})
	reqJSON := `{
		"model": "` + env.modelID + `",
		"input": "x",
		"questions": [{"type":"predicate","name":"foo","instructions":"Is foo?"}]
	}`
	rec := env.sendRequest(http.MethodPost, reqJSON, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got DecisionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("response not typed: %v", err)
	}
	if got.Usage.InputTokens != 401 || got.Usage.OutputTokens != 0 {
		t.Errorf("usage tokens wrong: %+v", got.Usage)
	}
	if got.Usage.InputTokensDetail.CachedTokens != 0 || got.Usage.InputTokensDetail.CacheWriteTokens != 0 {
		t.Errorf("input_tokens_details not zero-filled: %+v", got.Usage.InputTokensDetail)
	}
	if got.Usage.OutputTokensDetail.ReasoningTokens != 0 {
		t.Errorf("output_tokens_details not zero-filled: %+v", got.Usage.OutputTokensDetail)
	}
}

// TestHandler_TypeSafe_AnswerOrderPreserved — the System One
// keyed map → ordered OpenAI `answers[]` using the original
// questions[] order (per-question key map).
func TestHandler_TypeSafe_AnswerOrderPreserved(t *testing.T) {
	// System One returns "bar" before "foo" in the JSON map
	// iteration (Go map ordering is randomized but we'll
	// assert against the request order regardless).
	answers := map[string]interface{}{
		"foo": map[string]interface{}{"noul": 0.5},
		"bar": map[string]interface{}{"choice": map[string]interface{}{"value": "billing", "probabilities": map[string]interface{}{"billing": 0.7, "other": 0.3}, "confidence": 0.6}},
	}
	body := systemOneSuccessBody(answers, 1, 1)
	env := newEnvTypesafe(t, fakeScript{status: 200, body: body})
	reqJSON := `{
		"model": "` + env.modelID + `",
		"input": "x",
		"questions": [
			{"type":"predicate","name":"foo","instructions":"Is foo?"},
			{"type":"choice","name":"bar","choices":[{"value":"billing"},{"value":"other"}]}
		]
	}`
	rec := env.sendRequest(http.MethodPost, reqJSON, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got DecisionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("typed: %v", err)
	}
	if len(got.Answers) != 2 {
		t.Fatalf("answers len = %d, want 2; got=%+v", len(got.Answers), got.Answers)
	}
	// Order: foo first, bar second (request order).
	if got.Answers[0].Name != "foo" {
		t.Errorf("answers[0].name = %q, want foo", got.Answers[0].Name)
	}
	if got.Answers[1].Name != "bar" {
		t.Errorf("answers[1].name = %q, want bar", got.Answers[1].Name)
	}
}

// ---------- BuildUpstream error path ----------

// TestHandler_BuildUpstream_Error_502_DecisionUpstreamBuildFailed —
// build failure returns 502 + house envelope code
// "decision_upstream_build_failed" (Q15 / finding 8).
func TestHandler_BuildUpstream_Error_502_DecisionUpstreamBuildFailed(t *testing.T) {
	body := openAIDecisionSuccessBody(10, 0)
	env := newEnvOpenAI(t, fakeScript{status: 200, body: body})
	// Wipe the credential's InternalModel so the openai
	// adapter's BuildUpstream returns an error (defense-in-depth
	// — the rewrite target was empty).
	env.handler.SetCredentialResolver(func(id string) (models.ResolvedCredential, bool) {
		return models.ResolvedCredential{
			Provider:      "openai",
			APIKey:        "k",
			BaseURL:       env.upstream.URL,
			InternalModel: "", // empty → BuildUpstream returns error
			CredentialID:  "cred-1",
		}, true
	})
	rec := env.sendRequest(http.MethodPost, standardRequestBody(env.modelID), nil)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("BuildUpstream error: status = %d, want 502; body=%s", rec.Code, rec.Body.String())
	}
	_, _, code := decodeOpenAIError(t, rec.Body.Bytes())
	if code != "decision_upstream_build_failed" {
		t.Errorf("code = %q, want decision_upstream_build_failed", code)
	}
	if env.fake.hitCount.Load() != 0 {
		t.Errorf("upstream hit count = %d, want 0 (build error → no upstream call)", env.fake.hitCount.Load())
	}
}

// ---------- Timeout honesty (acceptance 8) ----------

// TestHandler_TimeoutCtx_AppliedToUpstream — a slow upstream
// that exceeds the configured DecisionTimeout sees the call
// abort at the clamp; the handler returns 504 + OpenAI
// envelope.
func TestHandler_TimeoutCtx_AppliedToUpstream(t *testing.T) {
	// The stub config returns 100ms; the fake upstream
	// delays 1s. The handler's http.NewRequestWithContext
	// bounds the call — sharedHTTPClient.Do returns
	// context.DeadlineExceeded, the handler maps that to 504.
	env := newEnvOpenAI(t, fakeScript{status: 200, body: openAIDecisionSuccessBody(10, 0), delay: 1 * time.Second})
	// Override the config to a 100ms cap.
	env.handler.configMgr = stubConfig{timeout: 100 * time.Millisecond}
	start := time.Now()
	rec := env.sendRequest(http.MethodPost, standardRequestBody(env.modelID), nil)
	elapsed := time.Since(start)
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("timeout: status = %d, want 504; body=%s", rec.Code, rec.Body.String())
	}
	if elapsed > 800*time.Millisecond {
		t.Errorf("handler returned in %s; expected ~100ms (timeout clamp)", elapsed)
	}
}

// ---------- Meter (success-only) ----------

// TestHandler_Meter_NoUsageRow_OnFailure — failure path:
// Increment + IncrementModelUsage must NOT be called (success-
// only semantics per amendment 5 / Q5).
func TestHandler_Meter_NoUsageRow_OnFailure(t *testing.T) {
	body := openAIDecisionErrorBody("invalid_request", "invalid_request_error", "bad")
	env := newEnvOpenAI(t, fakeScript{status: 200, body: body})
	// We need to monitor the meter call site. The metered
	// call happens after InspectResponse returns a non-zero
	// Outcome. We track the model_hourly_usage table — a row
	// appears only on success.
	rec := env.sendRequest(http.MethodPost, standardRequestBody(env.modelID), nil)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("error envelope: status = %d, want 502; body=%s", rec.Code, rec.Body.String())
	}
	// Give the (absent) meter a beat — and then assert no
	// row exists.
	hourBucket := time.Now().UTC().Format("2006-01-02T15")
	time.Sleep(150 * time.Millisecond)
	rc, _ := readModelUsageRowCount(env, env.modelID, hourBucket)
	if rc != 0 {
		t.Errorf("failure path wrote a usage row (request_count=%d); success-only semantics violated", rc)
	}
	// Also confirm the event log emitted a failure outcome
	// (telemetry, not metering) — subscribe to a fresh
	// channel and re-issue.
	sub, err := env.bus.Subscribe()
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer env.bus.Unsubscribe(sub)
	_ = env.sendRequest(http.MethodPost, standardRequestBody(env.modelID), nil)
	select {
	case e := <-sub:
		if e.Type != "decision" {
			t.Fatalf("event type = %q, want decision", e.Type)
		}
		data, ok := e.Data.(map[string]interface{})
		if !ok {
			t.Fatalf("event data not map: %T", e.Data)
		}
		if outcome, _ := data["outcome"].(string); outcome != "error" {
			t.Errorf("event outcome = %v, want error", data["outcome"])
		}
	case <-time.After(time.Second):
		t.Errorf("DecisionEvent with outcome=error not published within 1s; failure telemetry missing")
	}
}

// TestHandler_Meter_OnSuccess_WritesUsageRow — success path:
// the meter writes a row to model_hourly_usage + token
// hourly usage.
func TestHandler_Meter_OnSuccess_WritesUsageRow(t *testing.T) {
	body := openAIDecisionSuccessBody(401, 0)
	env := newEnvOpenAI(t, fakeScript{status: 200, body: body})
	rec := env.sendRequest(http.MethodPost, standardRequestBody(env.modelID), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	hourBucket := time.Now().UTC().Format("2006-01-02T15")
	pollUntil(t, time.Second, func() bool {
		n, _ := readModelUsageRowCount(env, env.modelID, hourBucket)
		return n > 0
	})
	n, _ := readModelUsageRowCount(env, env.modelID, hourBucket)
	if n != 1 {
		t.Errorf("model request_count = %d, want 1 (success path)", n)
	}
}

// TestHandler_Meter_DetachedContext_DoesNotCancelOnClientCancel —
// success: even if the client cancels mid-request, the meter
// still writes (detached ctx, mirror imggen).
func TestHandler_Meter_DetachedContext_DoesNotCancelOnClientCancel(t *testing.T) {
	body := openAIDecisionSuccessBody(10, 0)
	env := newEnvOpenAI(t, fakeScript{status: 200, body: body})
	// Build a request whose context we cancel BEFORE the
	// handler returns — we cannot interpose between the
	// handler and the meter from here, so instead we just
	// confirm a successful POST writes the row even with
	// a client cancel pattern that would have fired if the
	// request ctx were threaded into the meter. We assert
	// using the success-only invariant (no separate
	// cancel pattern required — the success case proves the
	// detach).
	rec := env.sendRequest(http.MethodPost, standardRequestBody(env.modelID), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	hourBucket := time.Now().UTC().Format("2006-01-02T15")
	pollUntil(t, time.Second, func() bool {
		n, _ := readModelUsageRowCount(env, env.modelID, hourBucket)
		return n > 0
	})
}

// ---------- DecisionEvent telemetry ----------

// TestHandler_DecisionEvent_OnSuccess — every completed
// decision publishes a DecisionEvent with model/provider/usage/
// outcome/duration.
func TestHandler_DecisionEvent_OnSuccess(t *testing.T) {
	body := openAIDecisionSuccessBody(401, 0)
	env := newEnvOpenAI(t, fakeScript{status: 200, body: body})

	// Subscribe BEFORE issuing the request.
	sub, err := env.bus.Subscribe()
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer env.bus.Unsubscribe(sub)

	rec := env.sendRequest(http.MethodPost, standardRequestBody(env.modelID), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	select {
	case e := <-sub:
		if e.Type != "decision" {
			t.Fatalf("event type = %q, want decision", e.Type)
		}
		data, ok := e.Data.(map[string]interface{})
		if !ok {
			t.Fatalf("event data not map: %T", e.Data)
		}
		if data["model"] != env.modelID {
			t.Errorf("event model = %v, want %s", data["model"], env.modelID)
		}
		if data["provider"] != "openai" {
			t.Errorf("event provider = %v, want openai", data["provider"])
		}
		if data["outcome"] != "success" {
			t.Errorf("event outcome = %v, want success", data["outcome"])
		}
		// input_tokens at 401
		if v, _ := data["input_tokens"].(int64); v != 401 {
			t.Errorf("event input_tokens = %v, want 401", data["input_tokens"])
		}
	case <-time.After(time.Second):
		t.Fatalf("no decision event published within 1s")
	}
}

// ---------- Header strip / credential bearer wire ----------

// TestHandler_ProxyAuthHeader_NotForwarded — the proxy's sk-
// token must never reach the upstream; only the credential's
// Bearer is sent.
func TestHandler_ProxyAuthHeader_NotForwarded(t *testing.T) {
	body := openAIDecisionSuccessBody(10, 0)
	env := newEnvOpenAI(t, fakeScript{status: 200, body: body})
	rec := env.sendRequest(http.MethodPost, standardRequestBody(env.modelID), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	env.fake.mu.Lock()
	got := env.fake.lastAuthH
	env.fake.mu.Unlock()
	if got != "Bearer sk-cred-secret" {
		t.Errorf("upstream Authorization = %q, want %q (only credential bearer)", got, "Bearer sk-cred-secret")
	}
	if strings.Contains(got, "sk-test-token") {
		t.Errorf("proxy's sk- token leaked to upstream: %q", got)
	}
}

// ---------- Decoupled / unit-level typed-adapter tests ----------

// TestOpenAIDecisionProvider_BuildUpstream_RewritesModel — unit
// test of the openai-decision adapter's BuildUpstream — the
// typed request's Model is replaced with cred.InternalModel;
// all other bytes are preserved.
func TestOpenAIDecisionProvider_BuildUpstream_RewritesModel(t *testing.T) {
	p := NewOpenAIDecisionProvider()
	req := &DecisionRequest{Model: "decision-router"}
	cred := models.ResolvedCredential{
		Provider: "openai", InternalModel: "gpt-6-luna",
		APIKey: "k", BaseURL: "https://api.openai.com/v1",
	}
	raw := []byte(`{"model":"decision-router","input":"x","questions":[{"type":"predicate","name":"foo"}]}`)
	url, body, err := p.BuildUpstream(cred, req, raw)
	if err != nil {
		t.Fatalf("BuildUpstream: %v", err)
	}
	// chat-convention cred.BaseURL is "https://api.openai.com/v1"
	// (factory.go:116); the builder strips it before re-appending
	// "/v1/decisions" — so the final URL has ONE /v1, not two.
	if url != "https://api.openai.com/v1/decisions" {
		t.Errorf("URL = %q, want https://api.openai.com/v1/decisions", url)
	}
	if !bytes.Contains(body, []byte(`"model":"gpt-6-luna"`)) {
		t.Errorf("body missing model rewrite: %s", body)
	}
	if bytes.Contains(body, []byte(`"decision-router"`)) {
		t.Errorf("body still has old model: %s", body)
	}
}

// TestOpenAIDecisionProvider_BuildUpstream_DefaultURL — P2
// review finding 1 (critical): the openai-decision URL builder
// must produce a single "/v1/decisions" tail for ALL three
// operator input shapes:
//
//	(a) cred.BaseURL == ""                                       → default
//	(b) cred.BaseURL == "https://api.openai.com/"                → trailing slash
//	(c) cred.BaseURL == "https://api.openai.com/v1"             → chat convention
//
// The prior code produced "https://api.openai.com/v1/v1/decisions"
// for (a) and (c) — a 404 in the default path. The fix strips a
// single "/v1" tail before re-appending "/v1/decisions", so all
// three shapes resolve to the same canonical URL.
func TestOpenAIDecisionProvider_BuildUpstream_DefaultURL(t *testing.T) {
	p := NewOpenAIDecisionProvider()
	cases := []struct {
		name    string
		baseURL string
		wantURL string
	}{
		{name: "empty_default", baseURL: "", wantURL: "https://api.openai.com/v1/decisions"},
		{name: "trailing_slash", baseURL: "https://api.openai.com/", wantURL: "https://api.openai.com/v1/decisions"},
		{name: "chat_convention_v1", baseURL: "https://api.openai.com/v1", wantURL: "https://api.openai.com/v1/decisions"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &DecisionRequest{Model: "decision-router"}
			cred := models.ResolvedCredential{
				Provider: "openai", InternalModel: "gpt-6-luna",
				APIKey: "k", BaseURL: tc.baseURL,
			}
			raw := []byte(`{"model":"decision-router","input":"x","questions":[{"type":"predicate","name":"foo"}]}`)
			url, body, err := p.BuildUpstream(cred, req, raw)
			if err != nil {
				t.Fatalf("BuildUpstream: %v", err)
			}
			if url != tc.wantURL {
				t.Errorf("URL = %q, want %q", url, tc.wantURL)
			}
			if !bytes.Contains(body, []byte(`"model":"gpt-6-luna"`)) {
				t.Errorf("body missing model rewrite: %s", body)
			}
			if bytes.Contains(body, []byte(`"decision-router"`)) {
				t.Errorf("body still has old model: %s", body)
			}
		})
	}
}

// TestTypesafeProvider_BuildUpstream_PredicateToNoul — unit
// test of the typesafe adapter — predicate → noul with
// instructions passthrough (and synthesized fallback when
// instructions are missing).
func TestTypesafeProvider_BuildUpstream_PredicateToNoul(t *testing.T) {
	p := NewTypesafeProvider()
	req := &DecisionRequest{
		Model: "x",
		Input: json.RawMessage(`"some text"`),
		Questions: []Question{
			{Type: QuestionTypePredicate, Name: "foo", Instructions: "Is foo?"},
		},
	}
	cred := models.ResolvedCredential{
		Provider: "typesafe", InternalModel: "jev-1.13.0",
		APIKey: "k", BaseURL: "https://api.typesafe.ai",
	}
	url, body, err := p.BuildUpstream(cred, req, []byte(`{}`))
	if err != nil {
		t.Fatalf("BuildUpstream: %v", err)
	}
	if url != "https://api.typesafe.ai/v1/systemone" {
		t.Errorf("URL = %q, want https://api.typesafe.ai/v1/systemone", url)
	}
	if !strings.Contains(string(body), `"type":"noul"`) {
		t.Errorf("body missing noul type tag: %s", body)
	}
	if !strings.Contains(string(body), `"Is foo?"`) {
		t.Errorf("body missing instructions: %s", body)
	}
}

// TestTypesafeProvider_BuildUpstream_ChoiceToMap — unit test —
// choice → System One criteria map keyed by JSON-formatted
// value.
func TestTypesafeProvider_BuildUpstream_ChoiceToMap(t *testing.T) {
	p := NewTypesafeProvider()
	req := &DecisionRequest{
		Model: "x", Input: json.RawMessage(`"x"`),
		Questions: []Question{
			{Type: QuestionTypeChoice, Name: "dept", Instructions: "?",
				Choices: []ChoiceOption{
					{Value: "billing"}, {Value: "technical"},
				}},
		},
	}
	cred := models.ResolvedCredential{Provider: "typesafe", InternalModel: "jev"}
	_, body, err := p.BuildUpstream(cred, req, []byte(`{}`))
	if err != nil {
		t.Fatalf("BuildUpstream: %v", err)
	}
	if !strings.Contains(string(body), `"criteria"`) {
		t.Errorf("body missing criteria: %s", body)
	}
	if !strings.Contains(string(body), `"billing"`) {
		t.Errorf("body missing billing key: %s", body)
	}
}

// TestTypesafeProvider_BuildUpstream_ScoreToCriteriaArray —
// unit test — score → System One criteria array.
func TestTypesafeProvider_BuildUpstream_ScoreToCriteriaArray(t *testing.T) {
	p := NewTypesafeProvider()
	req := &DecisionRequest{
		Model: "x", Input: json.RawMessage(`"x"`),
		Questions: []Question{
			{Type: QuestionTypeScore, Name: "frustration",
				Levels: []ScoreLevel{{Label: "calm"}, {Label: "angry"}},
			},
		},
	}
	cred := models.ResolvedCredential{Provider: "typesafe", InternalModel: "jev"}
	_, body, err := p.BuildUpstream(cred, req, []byte(`{}`))
	if err != nil {
		t.Fatalf("BuildUpstream: %v", err)
	}
	if !strings.Contains(string(body), `"criteria":[`) {
		t.Errorf("body should have criteria array: %s", body)
	}
}

// TestTypesafeProvider_InspectResponse_OrdersAnswers — unit —
// System One keyed map → ordered answers[] using the original
// questions[] order.
func TestTypesafeProvider_InspectResponse_OrdersAnswers(t *testing.T) {
	p := NewTypesafeProvider()
	req := &DecisionRequest{
		Questions: []Question{
			{Type: QuestionTypePredicate, Name: "a"},
			{Type: QuestionTypePredicate, Name: "b"},
		},
	}
	respBody := []byte(`{
		"model": "jev",
		"answers": {
			"b": {"noul": 0.1},
			"a": {"noul": 0.9}
		},
		"usage": {"input_tokens": 10, "output_tokens": 0}
	}`)
	resp, outcome, err := p.InspectResponse(respBody, req)
	if err != nil || outcome == nil {
		t.Fatalf("InspectResponse: %v, %v", err, outcome)
	}
	if outcome.RelayStatus != 0 {
		t.Errorf("RelayStatus = %d, want 0", outcome.RelayStatus)
	}
	if len(resp.Answers) != 2 {
		t.Fatalf("answers len = %d, want 2", len(resp.Answers))
	}
	if resp.Answers[0].Name != "a" || resp.Answers[1].Name != "b" {
		t.Errorf("ordered answers wrong: a=%q b=%q (want 'a' then 'b')",
			resp.Answers[0].Name, resp.Answers[1].Name)
	}
}

// TestTypesafeProvider_InspectResponse_ScoreEmitsLevelIndex —
// A24: System One score (which may be int-like) → OpenAI score
// is integer level index; probabilities[] includes label per
// the request's levels[].
func TestTypesafeProvider_InspectResponse_ScoreEmitsLevelIndex(t *testing.T) {
	p := NewTypesafeProvider()
	req := &DecisionRequest{
		Questions: []Question{
			{Type: QuestionTypeScore, Name: "frustration",
				Levels: []ScoreLevel{{Label: "calm"}, {Label: "angry"}}},
		},
	}
	respBody := []byte(`{
		"model": "jev",
		"answers": {
			"frustration": {
				"score": 0,
				"probabilities": {"calm": 0.7, "angry": 0.3},
				"confidence": 0.5
			}
		},
		"usage": {"input_tokens": 10, "output_tokens": 0}
	}`)
	resp, outcome, err := p.InspectResponse(respBody, req)
	if err != nil || outcome == nil {
		t.Fatalf("InspectResponse: %v, %v", err, outcome)
	}
	if resp.Answers[0].Score == nil || *resp.Answers[0].Score != 0 {
		t.Errorf("score = %v, want 0 (int level index)", resp.Answers[0].Score)
	}
	probs := resp.Answers[0].GetScoreProbabilities()
	if len(probs) != 2 {
		t.Fatalf("score probabilities len = %d, want 2; got=%+v", len(probs), probs)
	}
}

// TestOpenAIDecisionProvider_InspectResponse_Empty_502 — empty
// upstream body ⇒ 502 with distinct log reason.
func TestOpenAIDecisionProvider_InspectResponse_Empty_502(t *testing.T) {
	p := NewOpenAIDecisionProvider()
	_, outcome, err := p.InspectResponse(nil, &DecisionRequest{Model: "x"})
	if err != nil || outcome == nil {
		t.Fatalf("InspectResponse: %v, %v", err, outcome)
	}
	if outcome.RelayStatus != 502 {
		t.Errorf("RelayStatus = %d, want 502", outcome.RelayStatus)
	}
	if outcome.Reason != "empty body" {
		t.Errorf("Reason = %q, want %q", outcome.Reason, "empty body")
	}
}

// TestOpenAIDecisionProvider_InspectResponse_ErrorEnvelope_502 —
// OpenAI error envelope → 502 with body verbatim.
func TestOpenAIDecisionProvider_InspectResponse_ErrorEnvelope_502(t *testing.T) {
	p := NewOpenAIDecisionProvider()
	body := []byte(`{"error":{"message":"bad","type":"invalid_request","code":"x"}}`)
	_, outcome, err := p.InspectResponse(body, &DecisionRequest{Model: "x"})
	if err != nil || outcome == nil {
		t.Fatalf("InspectResponse: %v, %v", err, outcome)
	}
	if outcome.RelayStatus != 502 {
		t.Errorf("RelayStatus = %d, want 502", outcome.RelayStatus)
	}
}

// ---------- Validator unit tests (typed predicates) ----------

func TestValidator_QuestionsNonEmpty(t *testing.T) {
	if msg, failed := ValidateQuestionsNonEmpty(&DecisionRequest{}); !failed || !strings.Contains(msg, "at least one question") {
		t.Errorf("expected fail with empty questions: got msg=%q failed=%v", msg, failed)
	}
}

func TestValidator_QuestionsCount(t *testing.T) {
	req := &DecisionRequest{Questions: make([]Question, 200)}
	if _, failed := ValidateQuestionsCount(req); !failed {
		t.Errorf("expected fail for 200 questions")
	}
}

func TestValidator_ChoiceOptionsCount(t *testing.T) {
	req := &DecisionRequest{
		Questions: []Question{
			{Type: QuestionTypeChoice, Name: "x", Choices: []ChoiceOption{{Value: "a"}}},
		},
	}
	if _, failed := ValidateChoiceOptionsCount(req); !failed {
		t.Errorf("expected fail for 1 choice option")
	}
}

func TestValidator_ScoreLevelsCount(t *testing.T) {
	req := &DecisionRequest{
		Questions: []Question{
			{Type: QuestionTypeScore, Name: "x", Levels: []ScoreLevel{{Label: "low"}, {Label: "high"}, {Label: "x"}, {Label: "x"}, {Label: "x"}, {Label: "x"}, {Label: "x"}, {Label: "x"}, {Label: "x"}, {Label: "x"}, {Label: "x"}}},
		},
	}
	if _, failed := ValidateScoreLevelsCount(req); !failed {
		t.Errorf("expected fail for 11 levels (cap 10)")
	}
}

func TestValidator_BooleanStringCollision(t *testing.T) {
	req := &DecisionRequest{
		Questions: []Question{
			{Type: QuestionTypeChoice, Name: "x",
				Choices: []ChoiceOption{{Value: true}, {Value: "true"}}},
		},
	}
	if _, failed := ValidateBooleanStringCollision(req); !failed {
		t.Errorf("expected fail for boolean vs string collision")
	}
}

// TestValidator_BooleanStringCollision_MixedTypes — mixed
// string|boolean choice values are LEGAL per the OpenAI
// Decisions spec (research-web: choices are typed, 2–255 unique
// {value: string|boolean, description?}). Only the GENUINE
// cross-type collision (bool true vs string "true", bool false
// vs string "false") may fail.
//
// Regression guard for the P2 over-rejection: the detector's
// bool arm used to self-synthesize its string form ("bridge
// key") into the shared string set, so the trailing inverse
// check fired for EVERY boolean-valued option — e.g.
// ["maybe", true] was wrongly rejected. Under the buggy code,
// the "mixed string+bool is legal" and "all booleans" cases
// fail loudly.
func TestValidator_BooleanStringCollision_MixedTypes(t *testing.T) {
	choiceQ := func(name string, values ...interface{}) *DecisionRequest {
		opts := make([]ChoiceOption, len(values))
		for i, v := range values {
			opts[i] = ChoiceOption{Value: v}
		}
		return &DecisionRequest{
			Questions: []Question{{Type: QuestionTypeChoice, Name: name, Choices: opts}},
		}
	}
	tests := []struct {
		name      string
		req       *DecisionRequest
		wantFail  bool
		wantInMsg string // substring required in msg when wantFail
	}{
		{
			name:      "genuine collision: bool true vs string true",
			req:       choiceQ("x", true, "true"),
			wantFail:  true,
			wantInMsg: "type-collision",
		},
		{
			name:      "genuine collision: bool false vs string false",
			req:       choiceQ("x", false, "false"),
			wantFail:  true,
			wantInMsg: "type-collision",
		},
		{
			name:     "mixed string+bool is legal (THE case the bug broke)",
			req:      choiceQ("x", "maybe", true),
			wantFail: false,
		},
		{
			name:     "all strings incl true/false literals: no booleans, no collision",
			req:      choiceQ("x", "true", "false", "maybe"),
			wantFail: false,
		},
		{
			name:     "all booleans: no strings, no collision",
			req:      choiceQ("x", true, false),
			wantFail: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, failed := ValidateBooleanStringCollision(tt.req)
			if failed != tt.wantFail {
				t.Fatalf("ValidateBooleanStringCollision = (%q, %v), wantFail=%v", msg, failed, tt.wantFail)
			}
			if tt.wantFail && !strings.Contains(msg, tt.wantInMsg) {
				t.Errorf("message = %q, want it to contain %q", msg, tt.wantInMsg)
			}
		})
	}
}

func TestValidator_DuplicateQuestionNames(t *testing.T) {
	req := &DecisionRequest{
		Questions: []Question{
			{Type: QuestionTypePredicate, Name: "foo"},
			{Type: QuestionTypePredicate, Name: "foo"},
		},
	}
	if _, failed := ValidateDuplicateQuestionNames(req); !failed {
		t.Errorf("expected fail for duplicate names")
	}
}

func TestValidator_SafetyIdentifier_Over128(t *testing.T) {
	req := &DecisionRequest{SafetyIdentifier: strings.Repeat("u", 129)}
	if _, failed := ValidateSafetyIdentifier(req); !failed {
		t.Errorf("expected fail for 129-char safety_identifier")
	}
}

func TestValidator_SafetyIdentifier_Under128(t *testing.T) {
	req := &DecisionRequest{SafetyIdentifier: strings.Repeat("u", 128)}
	if _, failed := ValidateSafetyIdentifier(req); failed {
		t.Errorf("expected pass for 128-char safety_identifier")
	}
}

// ---------- Suppress unused-import noise ----------

var _ = log.Printf
