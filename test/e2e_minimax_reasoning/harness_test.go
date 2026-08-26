// Package e2e_minimax_reasoning is the P3-5 pre-merge E2E gate for the
// MiniMax reasoning_details feature on branch feature/minimax-reasoning-details.
//
// It drives the FULL proxy handler (proxy.Handler.HandleChatCompletions) in
// process, wired to a capturing mock upstream (external paths) or a
// MiniMax-shaped mock provider upstream served over HTTP (internal paths),
// and asserts the wire-visible feature contract on all four proxy paths:
//
//	race-external      — unknown model id + MiniMax upstream credential
//	race-internal      — registered model with Internal:true + MiniMax credential
//	ultimate-external  — X-Force-Ultimate-Model + ultimate model Internal:false
//	ultimate-internal  — X-Force-Ultimate-Model + ultimate model Internal:true
//
// The harness mirrors test/e2e_ultimate_internal_reasoning/ (real
// database.Store + auth.TokenStore + models.ModelsConfig + proxy.Handler).
package e2e_minimax_reasoning

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy/translator"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
)

// ─────────────────────────────────────────────────────────────────────────────
// Capturing mock upstream
// ─────────────────────────────────────────────────────────────────────────────

// mockUpstream wraps an httptest server that captures every incoming request
// (body + headers) before delegating to a user-supplied handler.
type mockUpstream struct {
	t                *testing.T
	handler          http.HandlerFunc
	mu               sync.Mutex
	capturedRequests []capturedRequest
}

type capturedRequest struct {
	Body       []byte
	BodyParsed map[string]interface{}
	Headers    http.Header
}

func newMockUpstream(t *testing.T, handler http.HandlerFunc) *mockUpstream {
	return &mockUpstream{t: t, handler: handler}
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

func (m *mockUpstream) snapshot() []capturedRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]capturedRequest, len(m.capturedRequests))
	copy(out, m.capturedRequests)
	return out
}

// last returns the most recently captured upstream request.
func (m *mockUpstream) last() capturedRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.capturedRequests) == 0 {
		return capturedRequest{}
	}
	return m.capturedRequests[len(m.capturedRequests)-1]
}

// ─────────────────────────────────────────────────────────────────────────────
// Test environment
// ─────────────────────────────────────────────────────────────────────────────

const (
	// minimaxCredID is the MiniMax provider credential used by all
	// positive-path models (external + internal).
	minimaxCredID = "minimax-cred"
	// openaiCredID is a non-MiniMax credential used for negative paths.
	openaiCredID = "openai-cred"

	// raceExtModel is an UNREGISTERED model id: initRequestContext leaves
	// resolvedModel nil ⇒ modelList=[raw name] ⇒ race-external path.
	raceExtModel = "unregistered-minimax-external"

	// raceIntModel is registered Internal:true with the MiniMax credential
	// ⇒ race-internal path via the race coordinator.
	raceIntModel = "race-internal-minimax"

	// ultExtModel is the X-Force-Ultimate-Model target with Internal:false
	// ⇒ ultimate-external path.
	ultExtModel = "ultimate-external-minimax"

	// ultIntModel is the X-Force-Ultimate-Model target with Internal:true
	// ⇒ ultimate-internal path.
	ultIntModel = "ultimate-internal-minimax"

	// ultExtModelOpenAI is an ultimate-external target bound to the
	// non-MiniMax credential (S9: ultimate paths gate on the MODEL's
	// CredentialID, not the global upstream credential).
	ultExtModelOpenAI = "ultimate-external-openai"

	// raceIntModelOpenAI is registered Internal:true with a non-MiniMax
	// credential (S3 negative / S9).
	raceIntModelOpenAI = "race-internal-openai"
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
	plainToken    string // plaintext token with ultimateModelEnabled=false
}

// envOptions customizes setupTestEnv per test.
type envOptions struct {
	// upstreamProvider replaces "minimax" on the EXTERNAL (UpstreamCredentialID)
	// credential. Empty means "minimax".
	upstreamProvider string
	// noUpstreamCred leaves UpstreamCredentialID unset for the external path
	// (S10: no credential resolvable ⇒ feature inert on race-external).
	noUpstreamCred bool
	// ultimateModelID overrides ULTIMATE_MODEL_ID. Empty uses ultIntModel.
	ultimateModelID string
}

// setupTestEnv wires a full proxy.Handler against a capturing upstream.
//
// Registered models:
//   - raceIntModel     Internal:true  + minimax cred  (base_url = upstream)
//   - raceIntModelOpenAI Internal:true + openai cred  (base_url = upstream)
//   - ultExtModel      Internal:false + minimax cred (ultimate target, external)
//   - ultIntModel      Internal:true  + minimax cred (ultimate target, internal)
//
// The external upstream credential (config.UpstreamCredentialID) is MiniMax
// unless overridden via envOptions — race-external gate = flag && that
// credential is MiniMax.
func setupTestEnv(t *testing.T, upstreamHandler http.HandlerFunc, opts envOptions) *testEnv {
	t.Helper()

	mockUp := newMockUpstream(t, upstreamHandler)
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

	upstreamProvider := opts.upstreamProvider
	if upstreamProvider == "" {
		upstreamProvider = "minimax"
	}

	// MiniMax credential — drives the internal-path provider (provider
	// comes from the credential) and the external path when selected.
	if err := modelsConfig.AddCredential(models.CredentialConfig{
		ID:       minimaxCredID,
		Provider: "minimax",
		APIKey:   "test-key",
		BaseURL:  upstream.URL,
	}); err != nil {
		t.Fatalf("add minimax cred: %v", err)
	}

	// Non-MiniMax credential (openai) — S3/S9 negative paths.
	if err := modelsConfig.AddCredential(models.CredentialConfig{
		ID:       openaiCredID,
		Provider: "openai",
		APIKey:   "test-key",
		BaseURL:  upstream.URL,
	}); err != nil {
		t.Fatalf("add openai cred: %v", err)
	}

	addModel := func(m models.ModelConfig) {
		t.Helper()
		if err := modelsConfig.AddModel(m); err != nil {
			t.Fatalf("add model %s: %v", m.ID, err)
		}
	}

	// Race-internal model (MiniMax credential).
	addModel(models.ModelConfig{
		ID:            raceIntModel,
		Name:          "Race Internal MiniMax",
		Enabled:       true,
		Internal:      true,
		Credentials: models.TestRefs(minimaxCredID),
		InternalModel: "minimax-internal-name",
	})

	// Race-internal model (non-MiniMax credential) — S3/S9.
	addModel(models.ModelConfig{
		ID:            raceIntModelOpenAI,
		Name:          "Race Internal OpenAI",
		Enabled:       true,
		Internal:      true,
		Credentials: models.TestRefs(openaiCredID),
		InternalModel: "gpt-4o-mini",
	})

	// Ultimate models. Both are registered so X-Force-Ultimate-Model can
	// route to them; ULTIMATE_MODEL_ID env picks the active one per test.
	addModel(models.ModelConfig{
		ID:           ultExtModel,
		Name:         "Ultimate External MiniMax",
		Enabled:      true,
		Internal:     false,
		Credentials: models.TestRefs(minimaxCredID),
	})
	addModel(models.ModelConfig{
		ID:            ultIntModel,
		Name:          "Ultimate Internal MiniMax",
		Enabled:       true,
		Internal:      true,
		Credentials: models.TestRefs(minimaxCredID),
		InternalModel: "minimax-ultimate-internal-name",
	})
	// Ultimate-external target bound to the non-MiniMax credential (S9).
	addModel(models.ModelConfig{
		ID:           ultExtModelOpenAI,
		Name:         "Ultimate External OpenAI",
		Enabled:      true,
		Internal:     false,
		Credentials: models.TestRefs(openaiCredID),
	})

	// Env: ultimate model + fast deadlines. ULTIMATE_MODEL_MAX_RETRIES=0
	// makes X-Force-Ultimate-Model trigger on the first call.
	t.Setenv("APPLY_ENV_OVERRIDES", "1")
	t.Setenv("MAX_GENERATION_TIME", "10s")
	t.Setenv("ULTIMATE_MODEL_MAX_HASH", "100")
	t.Setenv("ULTIMATE_MODEL_MAX_RETRIES", "0")

	ultimateID := opts.ultimateModelID
	if ultimateID == "" {
		ultimateID = ultIntModel
	}
	t.Setenv("ULTIMATE_MODEL_ID", ultimateID)

	// External upstream URL + credential (race-external gate reads this
	// credential's provider).
	if !opts.noUpstreamCred {
		t.Setenv("UPSTREAM_URL", upstream.URL)
		t.Setenv("UPSTREAM_CREDENTIAL_ID", func() string {
			if opts.upstreamProvider != "" {
				return openaiCredID
			}
			return minimaxCredID
		}())
	} else {
		t.Setenv("UPSTREAM_URL", upstream.URL)
	}

	cfgMgr, err := config.NewManager()
	if err != nil {
		t.Fatalf("config manager: %v", err)
	}

	bus := events.NewBus()
	reqStore := store.NewRequestStore(100)
	proxyCfg := &proxy.Config{ConfigMgr: cfgMgr, ModelsConfig: modelsConfig}
	handler := proxy.NewHandler(proxyCfg, bus, reqStore, nil, tokenStore, nil, nil)

	env := &testEnv{
		t:            t,
		db:           db,
		dbStore:      dbStore,
		tokenStore:   tokenStore,
		upstream:     upstream,
		mockUp:       mockUp,
		handler:      handler,
		modelsConfig: modelsConfig,
	}

	// Two tokens: one with ultimate permission, one without.
	ctx := context.Background()
	env.ultimateToken, _, err = tokenStore.CreateToken(ctx, "ult-token", nil, "test", true, "", nil)
	if err != nil {
		t.Fatalf("create ultimate token: %v", err)
	}
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
	stream   bool
	messages []map[string]interface{}
	flag     bool // send X-Proxy-Interleaved-Thinking: true
	forceUlt bool // send X-Force-Ultimate-Model: true
	token    string
	marker   string // extra marker header value (S14 positive control)
}

func (cr chatRequest) build(t *testing.T) (*http.Request, *httptest.ResponseRecorder) {
	t.Helper()
	body := map[string]interface{}{
		"model":    cr.model,
		"stream":   cr.stream,
		"messages": cr.messages,
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
	if cr.marker != "" {
		req.Header.Set("X-Test-Marker", cr.marker)
	}
	return req, httptest.NewRecorder()
}

// run executes one request through the full handler.
func (e *testEnv) run(cr chatRequest) *httptest.ResponseRecorder {
	req, rr := cr.build(e.t)
	e.handler.HandleChatCompletions(rr, req)
	return rr
}

// reasoningMessages is the standard 3-message conversation: assistant
// reasoning_content at slot 1 only.
var reasoningMessages = []map[string]interface{}{
	{"role": "user", "content": "What is 2+2?"},
	{"role": "assistant", "content": "The answer is 4.", "reasoning_content": "think-A"},
	{"role": "user", "content": "Are you sure?"},
}

// twoReasoningMessages has reasoning_content on slots 0 and 1 (monotonic
// id counter coverage).
var twoReasoningMessages = []map[string]interface{}{
	{"role": "assistant", "content": "answer", "reasoning_content": "think-1"},
	{"role": "assistant", "content": "answer2", "reasoning_content": "think-2"},
}

// ─────────────────────────────────────────────────────────────────────────────
// Assertion helpers
// ─────────────────────────────────────────────────────────────────────────────

// msg extracts the messages array from a parsed body.
func msg(t *testing.T, body map[string]interface{}, where string) []interface{} {
	t.Helper()
	msgs, ok := body["messages"].([]interface{})
	if !ok {
		t.Fatalf("%s: no messages array; body=%s", where, mustJSON(body))
	}
	return msgs
}

// mustJSON pretty-prints for failure messages.
func mustJSON(v interface{}) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("<<marshal error: %v>>", err)
	}
	return string(b)
}

// assertDetailsEntry verifies the MiniMax wire shape of one
// reasoning_details entry: type/id/format/text exact; index is 0-or-absent
// (the ReasoningDetail struct carries `json:"index,omitempty"` — the
// translator's own unit tests document absent-index ≡ 0, minimax_test.go).
func assertDetailsEntry(t *testing.T, entry interface{}, wantID, wantText, where string) {
	t.Helper()
	e, ok := entry.(map[string]interface{})
	if !ok {
		t.Fatalf("%s: details entry is %T not object: %v", where, entry, entry)
	}
	if got := e["type"]; got != "reasoning.text" {
		t.Errorf("%s: type=%v want reasoning.text", where, got)
	}
	if got := e["id"]; got != wantID {
		t.Errorf("%s: id=%v want %s", where, got, wantID)
	}
	if got := e["format"]; got != "MiniMax-response-v1" {
		t.Errorf("%s: format=%v want MiniMax-response-v1", where, got)
	}
	// index: omitempty ⇒ absent (nil) or explicit 0 are both the wire
	// encoding of Index=0. Anything else is a contract violation.
	if idx, present := e["index"]; present && idx != float64(0) {
		t.Errorf("%s: index=%v want 0 (or absent via omitempty)", where, idx)
	}
	if got := e["text"]; got != wantText {
		t.Errorf("%s: text=%v want %s", where, got, wantText)
	}
	for _, k := range []string{"type", "id", "format", "text"} {
		if _, ok := e[k]; !ok {
			t.Errorf("%s: entry missing required field %q: %s", where, k, mustJSON(e))
		}
	}
}

// assertTranslatedUpstreamRequest is the shared S1/S2/S3 request-translation
// assertion: top-level reasoning_split:true, slot-correct per-message
// details with monotonic ids, reasoning_content stripped everywhere.
func assertTranslatedUpstreamRequest(t *testing.T, capBody map[string]interface{}, messages []map[string]interface{}, where string) {
	t.Helper()
	if got, ok := capBody["reasoning_split"]; !ok || got != true {
		t.Errorf("%s: top-level reasoning_split=%v (%T), want true — body=%s", where, got, got, mustJSON(capBody))
	}
	upMsgs := msg(t, capBody, where)
	if len(upMsgs) != len(messages) {
		t.Fatalf("%s: %d upstream messages, want %d — %s", where, len(upMsgs), len(messages), mustJSON(upMsgs))
	}
	counter := 0
	for i, orig := range messages {
		up, ok := upMsgs[i].(map[string]interface{})
		if !ok {
			t.Fatalf("%s: upstream messages[%d] not an object: %s", where, i, mustJSON(upMsgs))
		}
		origRC, _ := orig["reasoning_content"].(string)
		if origRC != "" {
			counter++
			details, ok := up["reasoning_details"].([]interface{})
			if !ok || len(details) != 1 {
				t.Fatalf("%s: messages[%d] reasoning_details=%v, want 1 entry — %s", where, i, up["reasoning_details"], mustJSON(up))
			}
			assertDetailsEntry(t, details[0], fmt.Sprintf("reasoning-text-%d", counter), origRC, fmt.Sprintf("%s messages[%d]", where, i))
			if _, still := up["reasoning_content"]; still {
				t.Errorf("%s: messages[%d] still carries reasoning_content after translation — %s", where, i, mustJSON(up))
			}
		} else {
			if _, has := up["reasoning_details"]; has {
				t.Errorf("%s: messages[%d] has no reasoning_content input but carries reasoning_details — %s", where, i, mustJSON(up))
			}
		}
		// role/content survive
		if got := up["role"]; got != orig["role"] {
			t.Errorf("%s: messages[%d].role=%v want %v", where, i, got, orig["role"])
		}
		if got := up["content"]; got != orig["content"] {
			t.Errorf("%s: messages[%d].content=%v want %v", where, i, got, orig["content"])
		}
	}
}

// assertUntouchedUpstreamRequest is the shared S8/S9/S10 negative assertion:
// reasoning_content preserved verbatim in the same slot, no reasoning_split,
// no reasoning_details on the wire.
func assertUntouchedUpstreamRequest(t *testing.T, capBody map[string]interface{}, messages []map[string]interface{}, where string) {
	t.Helper()
	if _, has := capBody["reasoning_split"]; has {
		t.Errorf("%s: reasoning_split present on wire but feature must be inert — body=%s", where, mustJSON(capBody))
	}
	upMsgs := msg(t, capBody, where)
	if len(upMsgs) != len(messages) {
		t.Fatalf("%s: %d upstream messages, want %d — %s", where, len(upMsgs), len(messages), mustJSON(upMsgs))
	}
	for i, orig := range messages {
		up, ok := upMsgs[i].(map[string]interface{})
		if !ok {
			t.Fatalf("%s: upstream messages[%d] not an object", where, i)
		}
		if _, has := up["reasoning_details"]; has {
			t.Errorf("%s: messages[%d] carries reasoning_details but translator must not run — %s", where, i, mustJSON(up))
		}
		origRC, _ := orig["reasoning_content"].(string)
		if origRC != "" {
			got, has := up["reasoning_content"].(string)
			if !has || got != origRC {
				t.Errorf("%s: messages[%d].reasoning_content=%q (has=%v), want verbatim %q — %s", where, i, got, has, origRC, mustJSON(up))
			}
		}
	}
}

// assertHeaderAbsentFolded fails if any captured upstream header key
// case-insensitively equals the given name (S14).
func assertHeaderAbsentFolded(t *testing.T, h http.Header, name, where string) {
	t.Helper()
	for k := range h {
		if strings.EqualFold(k, name) {
			t.Errorf("%s: header %q (any-case variant of %s) leaked upstream; captured headers=%v", where, k, name, h)
		}
	}
}

// parseJSONBody parses rr.Body; fatal on error.
func parseJSONBody(t *testing.T, rr *httptest.ResponseRecorder, where string) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatalf("%s: client body not JSON (status=%d): %s", where, rr.Code, rr.Body.String())
	}
	return m
}

// ─────────────────────────────────────────────────────────────────────────────
// Mock upstream response builders
// ─────────────────────────────────────────────────────────────────────────────

// okNonStreamHandler returns a handler emitting a plain OpenAI response.
func okNonStreamHandler() http.HandlerFunc {
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

// detailsNonStreamHandler emits a non-stream response carrying
// reasoning_details (2 entries) plus optional extra fields and usage.
func detailsNonStreamHandler(extra map[string]interface{}, messageExtra map[string]interface{}, usage map[string]interface{}) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		message := map[string]interface{}{
			"role": "assistant", "content": "final",
			"reasoning_details": []map[string]interface{}{
				{"type": "reasoning.text", "id": "reasoning-text-1", "format": "MiniMax-response-v1", "index": 0, "text": "think-A"},
				{"type": "reasoning.text", "id": "reasoning-text-2", "format": "MiniMax-response-v1", "index": 0, "text": "think-B"},
			},
		}
		for k, v := range messageExtra {
			message[k] = v
		}
		body := map[string]interface{}{
			"id": "chatcmpl-mock", "object": "chat.completion", "created": 1700000000, "model": "mock",
			"choices": []map[string]interface{}{
				{"index": 0, "message": message, "finish_reason": "stop"},
			},
		}
		for k, v := range extra {
			body[k] = v
		}
		if usage != nil {
			body["usage"] = usage
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}
}

// rawHandler writes the given body/status verbatim.
func rawHandler(status int, body []byte, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		w.Write(body)
	}
}

// rawStringHandler is rawHandler for string literals.
func rawStringHandler(status int, body string, contentType string) http.HandlerFunc {
	return rawHandler(status, []byte(body), contentType)
}

// ─────────────────────────────────────────────────────────────────────────────
// SSE helpers
// ─────────────────────────────────────────────────────────────────────────────

// sseDataLines parses an SSE byte stream and returns each `data: ` payload
// (without the prefix), preserving order. Non-data lines (comments like
// `: connected` / `: heartbeat`) are skipped.
func sseDataLines(t *testing.T, raw []byte) []string {
	t.Helper()
	var out []string
	for _, ln := range strings.Split(string(raw), "\n") {
		ln = strings.TrimRight(ln, "\r")
		if strings.HasPrefix(ln, "data: ") {
			out = append(out, strings.TrimPrefix(ln, "data: "))
		}
	}
	return out
}

// sseAssertWellFormed verifies every non-empty line is a comment or a
// well-formed `data: {json}` / `data: [DONE]` event, and the stream ends
// with [DONE].
func sseAssertWellFormed(t *testing.T, raw []byte, where string) {
	t.Helper()
	lines := strings.Split(string(raw), "\n")
	sawDone := false
	for _, ln := range lines {
		ln = strings.TrimRight(ln, "\r")
		if ln == "" {
			continue
		}
		if strings.HasPrefix(ln, ":") {
			continue // SSE comment (connected / heartbeat)
		}
		if !strings.HasPrefix(ln, "data: ") {
			t.Errorf("%s: malformed SSE line %q (no data: prefix)", where, ln)
			continue
		}
		payload := strings.TrimPrefix(ln, "data: ")
		if payload == "[DONE]" {
			sawDone = true
			continue
		}
		var probe map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &probe); err != nil {
			t.Errorf("%s: data payload not JSON: %q (%v)", where, payload, err)
		}
	}
	if !sawDone {
		t.Errorf("%s: stream does not terminate with data: [DONE] — raw=%q", where, raw)
	}
}

// reasoningDeltasFromClientStream extracts, in order, every
// delta.reasoning_content value from an SSE client stream.
func reasoningDeltasFromClientStream(t *testing.T, raw []byte) []string {
	t.Helper()
	var out []string
	for _, payload := range sseDataLines(t, raw) {
		if payload == "[DONE]" {
			continue
		}
		var chunk map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		choices, ok := chunk["choices"].([]interface{})
		if !ok || len(choices) == 0 {
			continue
		}
		choice, ok := choices[0].(map[string]interface{})
		if !ok {
			continue
		}
		delta, ok := choice["delta"].(map[string]interface{})
		if !ok {
			continue
		}
		if rc, ok := delta["reasoning_content"].(string); ok {
			out = append(out, rc)
		}
	}
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// Suite-level drift counter (S13)
// ─────────────────────────────────────────────────────────────────────────────

var driftBefore uint64

// TestMain captures the process-wide drift counter before the suite runs;
// TestS13_DriftCounter asserts delta==0 after everything else (Go runs
// tests in file order within a package; S13 is last in the test file).
func TestMain(m *testing.M) {
	driftBefore = translator.FormatDriftCount()
	m.Run()
}
