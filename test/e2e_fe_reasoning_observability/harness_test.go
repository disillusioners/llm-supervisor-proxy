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
	"strings"
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

	// raceIntModel is registered Internal:true with the openai credential
	// ⇒ race-internal path via the race coordinator.
	raceIntModel = "matrix-race-internal"

	// ultExtModel is the X-Force-Ultimate-Model target with Internal:false
	// ⇒ ultimate-external path.
	ultExtModel = "matrix-ultimate-external"

	// ultIntModel is the X-Force-Ultimate-Model target with Internal:true
	// ⇒ ultimate-internal path.
	ultIntModel = "matrix-ultimate-internal"

	// ultExtModelMiniMax is an ultimate-external target bound to the
	// MiniMax credential: the ultimate-external translation gate reads the
	// MODEL's CredentialID (not the global upstream credential), so the M2
	// row must use a MiniMax-credentialed ultimate model.
	ultExtModelMiniMax = "matrix-ultimate-external-minimax"

	// minimaxCredID drives the MiniMax translation gate rows (M1/M2):
	// credential provider must be "minimax" AND the request must carry
	// X-Proxy-Interleaved-Thinking.
	minimaxCredID = "matrix-minimax-cred"

	// anthropicIntModel is registered Internal:true for the anthropic-client
	// stream row (R9): HandleAnthropicMessages → doAnthropicInternalRequest.
	anthropicIntModel = "matrix-anthropic-internal"
)

// testEnv holds all wired dependencies for one test.
type testEnv struct {
	t             *testing.T
	db            *sql.DB
	dbStore       *database.Store
	tokenStore    *auth.TokenStore
	upstream      *httptest.Server
	mockUp        *mockUpstream
	handler       *proxy.Handler
	modelsConfig  *models.ModelsConfig
	ultimateToken string // plaintext token with ultimateModelEnabled=true
	plainToken    string // plaintext token (ultimateModelEnabled=false)

	// reqStore is the SAME *store.RequestStore the proxy handler writes
	// into — the store the FE API must read from.
	reqStore *store.RequestStore
	// feSrv serves the FE API (/fe/api/*) over REAL HTTP, sharing reqStore.
	feSrv *httptest.Server
}

// matrixOptions customizes setupMatrixEnv per matrix row group.
type matrixOptions struct {
	// ultimateModelID overrides ULTIMATE_MODEL_ID (ultimate rows). Empty
	// means "no ultimate model configured".
	ultimateModelID string
	// upstreamProviderMinimax binds UPSTREAM_CREDENTIAL_ID to the MiniMax
	// credential so the race-external translation gate fires (M1).
	upstreamProviderMinimax bool
}

// setupMatrixEnv wires the path-matrix environment: everything setupTestEnv
// builds, plus the registered models the R/N/M rows route through and BOTH
// tokens (plain + ultimate-enabled).
func setupMatrixEnv(t *testing.T, upstreamHandler http.HandlerFunc, opts matrixOptions) *testEnv {
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

	// Generic openai-shaped external credential.
	if err := modelsConfig.AddCredential(models.CredentialConfig{
		ID:       externalCredID,
		Provider: "openai",
		APIKey:   "test-key",
		BaseURL:  upstream.URL,
	}); err != nil {
		t.Fatalf("add external cred: %v", err)
	}

	// MiniMax credential — drives the translation-gate rows (M1/M2) and
	// internal model wiring for those rows.
	if err := modelsConfig.AddCredential(models.CredentialConfig{
		ID:       minimaxCredID,
		Provider: "minimax",
		APIKey:   "test-key",
		BaseURL:  upstream.URL,
	}); err != nil {
		t.Fatalf("add minimax cred: %v", err)
	}

	addModel := func(m models.ModelConfig) {
		t.Helper()
		if err := modelsConfig.AddModel(m); err != nil {
			t.Fatalf("add model %s: %v", m.ID, err)
		}
	}

	// Race-internal model (openai credential ⇒ internal OpenAI handler).
	addModel(models.ModelConfig{
		ID:            raceIntModel,
		Name:          "Matrix Race Internal",
		Enabled:       true,
		Internal:      true,
		Credentials: models.TestRefs(externalCredID),
		InternalModel: "matrix-race-internal-upstream",
	})

	// Ultimate-external model (Internal:false ⇒ executeExternal).
	addModel(models.ModelConfig{
		ID:           ultExtModel,
		Name:         "Matrix Ultimate External",
		Enabled:      true,
		Internal:     false,
		Credentials: models.TestRefs(externalCredID),
	})

	// Ultimate-internal model (Internal:true ⇒ executeInternal).
	addModel(models.ModelConfig{
		ID:            ultIntModel,
		Name:          "Matrix Ultimate Internal",
		Enabled:       true,
		Internal:      true,
		Credentials: models.TestRefs(externalCredID),
		InternalModel: "matrix-ult-internal-upstream",
	})

	// Ultimate-external model bound to the MiniMax credential — the M2
	// translation-gate row. The ultimate-external gate resolves
	// upstreamProvider from THIS model's CredentialID.
	addModel(models.ModelConfig{
		ID:           ultExtModelMiniMax,
		Name:         "Matrix Ultimate External MiniMax",
		Enabled:      true,
		Internal:     false,
		Credentials: models.TestRefs(minimaxCredID),
	})

	// Anthropic-client internal model (openai provider ⇒ translation mode,
	// NOT anthropic passthrough — R9/R10 translate OpenAI→Anthropic).
	addModel(models.ModelConfig{
		ID:            anthropicIntModel,
		Name:          "Matrix Anthropic Internal",
		Enabled:       true,
		Internal:      true,
		Credentials: models.TestRefs(externalCredID),
		InternalModel: "matrix-anthropic-internal-upstream",
	})

	// Env: fast deadlines + ultimate trigger-on-first-call (MaxRetries=0).
	t.Setenv("APPLY_ENV_OVERRIDES", "1")
	t.Setenv("MAX_GENERATION_TIME", "10s")
	t.Setenv("ULTIMATE_MODEL_MAX_HASH", "100")
	t.Setenv("ULTIMATE_MODEL_MAX_RETRIES", "0")
	if opts.ultimateModelID != "" {
		t.Setenv("ULTIMATE_MODEL_ID", opts.ultimateModelID)
	}

	// External upstream URL + credential. M rows bind the MiniMax
	// credential so the race-external translation gate fires.
	credID := externalCredID
	if opts.upstreamProviderMinimax {
		credID = minimaxCredID
	}
	t.Setenv("UPSTREAM_URL", upstream.URL)
	t.Setenv("UPSTREAM_CREDENTIAL_ID", credID)

	cfgMgr, err := config.NewManager()
	if err != nil {
		t.Fatalf("config manager: %v", err)
	}

	bus := events.NewBus()
	reqStore := store.NewRequestStore(100)
	proxyCfg := &proxy.Config{ConfigMgr: cfgMgr, ModelsConfig: modelsConfig}
	handler := proxy.NewHandler(proxyCfg, bus, reqStore, nil, tokenStore, nil, nil)

	// ── FE API over real HTTP, sharing reqStore ─────────────────────────
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
	env.ultimateToken, _, err = tokenStore.CreateToken(ctx, "ult-token", nil, "test", true, "", nil)
	if err != nil {
		t.Fatalf("create ultimate token: %v", err)
	}

	return env
}

// setupTestEnv wires the minimal closure-gate environment (Scenarios A–D):
// no registered internal models, glmModel stays unregistered for the
// race-external path.
func setupTestEnv(t *testing.T, upstreamHandler http.HandlerFunc) *testEnv {
	t.Helper()
	return setupMatrixEnv(t, upstreamHandler, matrixOptions{})
}

// ─────────────────────────────────────────────────────────────────────────────
// Request helpers
// ─────────────────────────────────────────────────────────────────────────────

// chatRequest describes one client request; build() renders it.
type chatRequest struct {
	model    string
	stream   *bool // nil ⇒ omit the "stream" field entirely (original bug shape)
	messages []map[string]interface{}
	flag     bool   // send X-Proxy-Interleaved-Thinking: true
	forceUlt bool   // send X-Force-Ultimate-Model: true
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
	if cr.flag {
		req.Header.Set("X-Proxy-Interleaved-Thinking", "true")
	}
	if cr.forceUlt {
		req.Header.Set("X-Force-Ultimate-Model", "true")
	}
	return req, httptest.NewRecorder()
}

// run executes one request through the full proxy handler.
func (e *testEnv) run(cr chatRequest) *httptest.ResponseRecorder {
	req, rr := cr.build(e.t)
	e.handler.HandleChatCompletions(rr, req)
	return rr
}

// anthropicRequest describes one /v1/messages client request.
type anthropicRequest struct {
	model    string
	stream   bool
	token    string
	messages []map[string]interface{}
}

// runAnthropic drives HandleAnthropicMessages directly (same in-process
// pattern as run(), but on the Anthropic Messages endpoint).
func (e *testEnv) runAnthropic(ar anthropicRequest) *httptest.ResponseRecorder {
	e.t.Helper()
	body := map[string]interface{}{
		"model":      ar.model,
		"max_tokens": 1024,
		"stream":     ar.stream,
		"messages":   ar.messages,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		e.t.Fatalf("marshal anthropic: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", ar.token)
	req.Header.Set("anthropic-version", "2023-06-01")
	rr := httptest.NewRecorder()
	e.handler.HandleAnthropicMessages(rr, req)
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

// ─────────────────────────────────────────────────────────────────────────────
// SSE stream mock builders (path matrix)
// ─────────────────────────────────────────────────────────────────────────────

// matrixSSEStream frames each line as an SSE data event.
func matrixSSEStream(lines ...string) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString("data: " + l + "\n\n")
	}
	return b.String()
}

// reasoningDeltaChunk emits a stream chunk carrying delta.reasoning_content.
func reasoningDeltaChunk(text string) string {
	return `{"id":"1","object":"chat.completion.chunk","created":1,"model":"mock","choices":[{"index":0,"delta":{"reasoning_content":"` + text + `"}}]}`
}

// streamContentChunk emits a stream chunk carrying delta.content.
func streamContentChunk(text string) string {
	return `{"id":"1","object":"chat.completion.chunk","created":1,"model":"mock","choices":[{"index":0,"delta":{"content":"` + text + `"}}]}`
}

// matrixFinishChunk is the finish_reason carrier.
var matrixFinishChunk = `{"id":"1","object":"chat.completion.chunk","created":1,"model":"mock","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`

// reasoningStreamHandler serves an SSE stream with reasoning_content deltas
// (split across two chunks so accumulation is exercised) + content + finish.
func reasoningStreamHandler(reasoningPart1, reasoningPart2 string) http.HandlerFunc {
	up := matrixSSEStream(
		reasoningDeltaChunk(reasoningPart1),
		reasoningDeltaChunk(reasoningPart2),
		streamContentChunk("final"),
		matrixFinishChunk,
		"[DONE]",
	)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(up))
	}
}

// plainStreamHandler serves an SSE stream with NO reasoning anywhere.
func plainStreamHandler() http.HandlerFunc {
	up := matrixSSEStream(
		streamContentChunk("ok"),
		matrixFinishChunk,
		"[DONE]",
	)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(up))
	}
}

// minimaxDetailsEntry is the MiniMax reasoning_details entry fixture.
func minimaxDetailsEntry(id, text string) string {
	return `{"type":"reasoning.text","id":"` + id + `","format":"MiniMax-response-v1","index":0,"text":"` + text + `"}`
}

// minimaxDetailsStreamChunk emits a stream chunk with delta.reasoning_details.
func minimaxDetailsStreamChunk(details string) string {
	return `{"id":"1","object":"chat.completion.chunk","created":1,"model":"mock","choices":[{"index":0,"delta":{"reasoning_details":[` + details + `]}}]}`
}

// minimaxDetailsStreamHandler serves an SSE stream whose reasoning arrives as
// reasoning_details (the MiniMax shape the translator converts to
// reasoning_content deltas for the client).
func minimaxDetailsStreamHandler(entry1ID, entry1Text, entry2ID, entry2Text string) http.HandlerFunc {
	up := matrixSSEStream(
		minimaxDetailsStreamChunk(minimaxDetailsEntry(entry1ID, entry1Text)),
		minimaxDetailsStreamChunk(minimaxDetailsEntry(entry2ID, entry2Text)),
		streamContentChunk("final"),
		matrixFinishChunk,
		"[DONE]",
	)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(up))
	}
}

// minimaxDetailsNonStreamHandler serves a non-stream response carrying
// message.reasoning_details (translated to reasoning_content for the client).
func minimaxDetailsNonStreamHandler(entry1Text, entry2Text string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id": "chatcmpl-mock-mm", "object": "chat.completion", "created": 1700000000, "model": "mock",
			"choices": []map[string]interface{}{
				{
					"index": 0,
					"message": map[string]interface{}{
						"role":    "assistant",
						"content": "final",
						"reasoning_details": []map[string]interface{}{
							{"type": "reasoning.text", "id": "reasoning-text-1", "format": "MiniMax-response-v1", "index": 0, "text": entry1Text},
							{"type": "reasoning.text", "id": "reasoning-text-2", "format": "MiniMax-response-v1", "index": 0, "text": entry2Text},
						},
					},
					"finish_reason": "stop",
				},
			},
		})
	}
}
