// Package e2e_fe_reasoning_observability is the CLOSURE GATE E2E suite for
// the reasoning-observability fix on branch fix/ui-reasoning-observability.
//
// It reproduces the ORIGINAL user bug end-to-end: a glm-5.3 NON-STREAMING
// response carrying choices[0].message.reasoning_content was visible in raw
// server JSON but the FE API GET /fe/api/requests/{id} returned ZERO
// occurrences of "thinking" and the UI 🧠 block never rendered.
//
// The fix populates store.Message.Thinking on paths that previously never
// captured it. This suite proves the full chain in ONE process:
//
//	client → proxy.Handler.HandleChatCompletions
//	       → *store.RequestStore (in-memory ring, shared instance)
//	       → ui.Server FE API mounted on a REAL httptest.Server
//	       → payload shape the 🧠 block renders (messages[i].thinking)
//
// The harness mirrors test/e2e_minimax_reasoning/ (real database.Store +
// auth.TokenStore + models.ModelsConfig + proxy.Handler) and additionally
// exposes reqStore and mounts the FE API over real HTTP via
// ui.NewServer(...).RegisterHandlers — no auth middleware on /fe/api.
package e2e_fe_reasoning_observability

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	_ "modernc.org/sqlite" // SQLite driver

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/ui"
)

// ─────────────────────────────────────────────────────────────────────────────
// Capturing mock upstream
// ─────────────────────────────────────────────────────────────────────────────

// mockUpstream wraps an httptest server that captures every incoming request
// (body + headers) before delegating to a user-supplied handler.
type mockUpstream struct {
	handler          http.HandlerFunc
	mu               sync.Mutex
	capturedRequests []capturedRequest
}

type capturedRequest struct {
	Body       []byte
	BodyParsed map[string]interface{}
	Headers    http.Header
}

func newMockUpstream(handler http.HandlerFunc) *mockUpstream {
	return &mockUpstream{handler: handler}
}

func (m *mockUpstream) startCapturingServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var bodyParsed map[string]interface{}
		_ = json.Unmarshal(body, &bodyParsed)

		m.mu.Lock()
		m.capturedRequests = append(m.capturedRequests, capturedRequest{
			Body:       body,
			BodyParsed: bodyParsed,
			Headers:    r.Header.Clone(),
		})
		m.mu.Unlock()

		r.Body = io.NopCloser(bytes.NewReader(body))
		m.handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ─────────────────────────────────────────────────────────────────────────────
// Test environment
// ─────────────────────────────────────────────────────────────────────────────

const (
	// glmModel is an UNREGISTERED model id: initRequestContext leaves
	// resolvedModel nil ⇒ modelList=[raw name] ⇒ race-external path.
	// This mirrors the original bug scenario exactly: a real-world model
	// name (glm-5.3) hitting the external upstream credential.
	glmModel = "glm-5.3"

	// externalCredID is the credential bound to UPSTREAM_CREDENTIAL_ID so
	// the race-external path resolves it for upstream auth/base URL.
	externalCredID = "external-cred"
)

// testEnv holds all wired dependencies for one test.
type testEnv struct {
	t            *testing.T
	db           *sql.DB
	dbStore      *database.Store
	tokenStore   *auth.TokenStore
	upstream     *httptest.Server
	mockUp       *mockUpstream
	handler      *proxy.Handler
	modelsConfig *models.ModelsConfig
	plainToken   string // plaintext token (ultimateModelEnabled=false)

	// reqStore is the SAME *store.RequestStore the proxy handler writes
	// into — the store the FE API must read from.
	reqStore *store.RequestStore
	// feSrv serves the FE API (/fe/api/*) over REAL HTTP, sharing reqStore.
	feSrv *httptest.Server
}

// setupTestEnv wires a full proxy.Handler against a capturing upstream and
// mounts the FE API on a real httptest.Server sharing the same reqStore.
//
// Registered models: none with Internal semantics relevant here — glm-5.3 is
// deliberately UNREGISTERED to take the race-external path.
func setupTestEnv(t *testing.T, upstreamHandler http.HandlerFunc) *testEnv {
	t.Helper()

	mockUp := newMockUpstream(upstreamHandler)
	upstream := mockUp.startCapturingServer(t)

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("pragma: %v", err)
	}

	dbStore := &database.Store{DB: db, Dialect: database.SQLite}
	t.Cleanup(func() { dbStore.Close() })
	if err := dbStore.RunMigrations(context.Background()); err != nil {
		t.Fatalf("migrations: %v", err)
	}

	tokenStore := auth.NewTokenStore(db, database.SQLite)

	modelsConfig := models.NewModelsConfig()

	// External credential (provider irrelevant to the bug — a generic
	// OpenAI-shaped upstream) bound as the global upstream credential.
	if err := modelsConfig.AddCredential(models.CredentialConfig{
		ID:       externalCredID,
		Provider: "openai",
		APIKey:   "test-key",
		BaseURL:  upstream.URL,
	}); err != nil {
		t.Fatalf("add external cred: %v", err)
	}

	// Env: fast deadlines, external upstream URL + credential.
	t.Setenv("APPLY_ENV_OVERRIDES", "1")
	t.Setenv("MAX_GENERATION_TIME", "10s")
	t.Setenv("ULTIMATE_MODEL_MAX_HASH", "100")
	t.Setenv("ULTIMATE_MODEL_MAX_RETRIES", "0")
	t.Setenv("UPSTREAM_URL", upstream.URL)
	t.Setenv("UPSTREAM_CREDENTIAL_ID", externalCredID)

	cfgMgr, err := config.NewManager()
	if err != nil {
		t.Fatalf("config manager: %v", err)
	}

	bus := events.NewBus()
	reqStore := store.NewRequestStore(100)
	proxyCfg := &proxy.Config{ConfigMgr: cfgMgr, ModelsConfig: modelsConfig}
	handler := proxy.NewHandler(proxyCfg, bus, reqStore, nil, tokenStore, nil)

	// ── FE API over real HTTP, sharing reqStore ─────────────────────────
	// ui.NewServer(bus, configMgr, proxyConfig, modelsConfig, store,
	// bufferStore, tokenStore, dbStore) — pkg/ui/server.go:75. The /fe/api
	// routes have NO auth middleware (server.go:130-151), so a plain GET
	// works. RegisterHandlers mounts both /ui/ static and /fe/api/*.
	uiSrv := ui.NewServer(bus, cfgMgr, proxyCfg, modelsConfig, reqStore, nil, tokenStore, dbStore)
	feMux := http.NewServeMux()
	uiSrv.RegisterHandlers(feMux)
	feHTTP := httptest.NewServer(feMux)
	t.Cleanup(feHTTP.Close)

	env := &testEnv{
		t:            t,
		db:           db,
		dbStore:      dbStore,
		tokenStore:   tokenStore,
		upstream:     upstream,
		mockUp:       mockUp,
		handler:      handler,
		modelsConfig: modelsConfig,
		reqStore:     reqStore,
		feSrv:        feHTTP,
	}

	ctx := context.Background()
	env.plainToken, _, err = tokenStore.CreateToken(ctx, "plain-token", nil, "test", false, "", nil)
	if err != nil {
		t.Fatalf("create plain token: %v", err)
	}

	return env
}

// ─────────────────────────────────────────────────────────────────────────────
// Request helpers
// ─────────────────────────────────────────────────────────────────────────────

// chatRequest describes one client request; build() renders it.
type chatRequest struct {
	model    string
	stream   *bool // nil ⇒ omit the "stream" field entirely (original bug shape)
	messages []map[string]interface{}
	token    string
}

func (cr chatRequest) build(t *testing.T) (*http.Request, *httptest.ResponseRecorder) {
	t.Helper()
	body := map[string]interface{}{
		"model":    cr.model,
		"messages": cr.messages,
	}
	if cr.stream != nil {
		body["stream"] = *cr.stream
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cr.token)
	return req, httptest.NewRecorder()
}

// run executes one request through the full proxy handler.
func (e *testEnv) run(cr chatRequest) *httptest.ResponseRecorder {
	req, rr := cr.build(e.t)
	e.handler.HandleChatCompletions(rr, req)
	return rr
}

// ─────────────────────────────────────────────────────────────────────────────
// FE API helpers (real HTTP)
// ─────────────────────────────────────────────────────────────────────────────

// feListRequests GETs /fe/api/requests and returns the newest-first list.
func (e *testEnv) feListRequests(t *testing.T) []map[string]interface{} {
	t.Helper()
	resp, err := http.Get(e.feSrv.URL + "/fe/api/requests")
	if err != nil {
		t.Fatalf("FE list: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("FE list status=%d body=%s", resp.StatusCode, string(raw))
	}
	var list []map[string]interface{}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("FE list body not JSON array: %v — %s", err, string(raw))
	}
	return list
}

// feGetRequest GETs /fe/api/requests/{id} and returns status + raw body.
func (e *testEnv) feGetRequest(t *testing.T, id string) (int, []byte) {
	t.Helper()
	resp, err := http.Get(e.feSrv.URL + "/fe/api/requests/" + id)
	if err != nil {
		t.Fatalf("FE detail GET %s: %v", id, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

// latestRequestID returns the id of the newest request via the FE list
// endpoint (newest-first ⇒ index 0).
func (e *testEnv) latestRequestID(t *testing.T) string {
	t.Helper()
	list := e.feListRequests(t)
	if len(list) == 0 {
		t.Fatalf("FE list empty — no requests recorded in store")
	}
	id, _ := list[0]["id"].(string)
	if id == "" {
		t.Fatalf("FE list[0] has no id: %v", list[0])
	}
	return id
}

// mustJSON pretty-prints for failure messages.
func mustJSON(v interface{}) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "<<marshal error>>"
	}
	return string(b)
}

// ─────────────────────────────────────────────────────────────────────────────
// Mock upstream response builders
// ─────────────────────────────────────────────────────────────────────────────

// reasoningNonStreamHandler emits a non-stream OpenAI response whose
// choices[0].message carries reasoning_content — the glm-5.3 shape from the
// original bug report.
func reasoningNonStreamHandler(reasoning string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id": "chatcmpl-mock-glm", "object": "chat.completion", "created": 1700000000, "model": "glm-5.3",
			"choices": []map[string]interface{}{
				{
					"index": 0,
					"message": map[string]interface{}{
						"role":              "assistant",
						"content":           "The answer is Paris.",
						"reasoning_content": reasoning,
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]interface{}{"prompt_tokens": 11, "completion_tokens": 7, "total_tokens": 18},
		})
	}
}

// plainNonStreamHandler emits a plain completion WITHOUT any reasoning.
func plainNonStreamHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id": "chatcmpl-mock", "object": "chat.completion", "created": 1700000000, "model": "mock",
			"choices": []map[string]interface{}{
				{"index": 0, "message": map[string]interface{}{"role": "assistant", "content": "ok"}, "finish_reason": "stop"},
			},
		})
	}
}
