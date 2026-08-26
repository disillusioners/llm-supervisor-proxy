// Package e2e_ultimate_internal_reasoning contains an end-to-end reproduction
// for the reasoning_content ultimate-internal bug fixed by commit 83814b0.
//
// Scenario under test (original bug):
//   - Client sends a chat-completion request whose `messages` carry a
//     `reasoning_content` field on an assistant message (DeepSeek R1-style).
//   - The proxy rewrites the request to the ultimate model (forced via
//     `X-Force-Ultimate-Model: true` — fail-closed admin bypass wired at
//     pkg/proxy/handler.go:465).
//   - The ultimate model is configured as `internal:true` with an
//     `internal_model` and a `base_url` pointing at our capturing mock upstream.
//   - pkg/ultimatemodel/handler_internal.go convertRequest must preserve
//     `reasoning_content` (the 4-line fix at lines 453-456).
//   - The mock upstream receives the rewritten body; we assert the captured
//     upstream body preserves `reasoning_content` at the correct slot.
//
// Trigger mechanism used: `X-Force-Ultimate-Model: true` admin header
//   (see pkg/proxy/handler.go:464-469 — bypasses hash-duplicate trigger).
//
// Negative control: revert ONLY the 4-line fix in
// pkg/ultimatemodel/handler_internal.go; the test MUST FAIL (slot-1
// reasoning_content missing in captured upstream body). After restoring
// the fix, the test MUST PASS again.
package e2e_ultimate_internal_reasoning

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
	"time"

	_ "modernc.org/sqlite" // SQLite driver

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
)

// ─────────────────────────────────────────────────────────────────────────────
// Capturing mock upstream (adapted from test/e2e_reasoning_content/)
// ─────────────────────────────────────────────────────────────────────────────

// mockUpstream wraps an httptest server that captures every incoming request
// body (parsed into BodyParsed) before delegating to a user-supplied handler.
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

// startCapturingServer starts an httptest.NewServer that records every
// request body and then forwards to the user handler. Cleanup is auto-registered.
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

		// Restore the body so the actual handler can read it
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

// ─────────────────────────────────────────────────────────────────────────────
// Test environment: real database.Store + proxy.Handler wired to mock upstream
// ─────────────────────────────────────────────────────────────────────────────

type testEnv struct {
	db           *sql.DB
	dbStore      *database.Store
	tokenStore   *auth.TokenStore
	upstream     *httptest.Server
	mockUp       *mockUpstream
	handler      *proxy.Handler
	modelsConfig *models.ModelsConfig
	// ultimateModelID is the model ID registered with internal:true; also set
	// as the ULTIMATE_MODEL_ID env value so the proxy routes to it.
	ultimateModelID string
}

func setupTestEnv(t *testing.T, upstreamHandler http.HandlerFunc) *testEnv {
	t.Helper()

	// 1. Capturing mock upstream
	mockUp := newMockUpstream(t, upstreamHandler)
	upstream := mockUp.startCapturingServer(t)

	// 2. Temp SQLite DB
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Failed to open SQLite database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("Failed to enable foreign keys: %v", err)
	}

	dbStore := &database.Store{DB: db, Dialect: database.SQLite}
	t.Cleanup(func() { dbStore.Close() })

	if err := dbStore.RunMigrations(context.Background()); err != nil {
		t.Fatalf("Failed to run migrations: %v", err)
	}

	tokenStore := auth.NewTokenStore(db, database.SQLite)

	// 3. Models config: credential + ULTIMATE model (internal:true) + a normal user-facing model
	modelsConfig := models.NewModelsConfig()

	if err := modelsConfig.AddCredential(models.CredentialConfig{
		ID:       "deepseek-cred",
		Provider: "openai",
		APIKey:   "test-key",
		BaseURL:  upstream.URL,
	}); err != nil {
		t.Fatalf("Failed to add credential: %v", err)
	}

	// User-facing model (the proxy sees requests targeting this ID)
	if err := modelsConfig.AddModel(models.ModelConfig{
		ID:            "deepseek-r1",
		Name:          "DeepSeek R1",
		Enabled:       true,
		Internal:      true,
		Credentials: models.TestRefs("deepseek-cred"),
		InternalModel: "deepseek-reasoner",
	}); err != nil {
		t.Fatalf("Failed to add user model: %v", err)
	}

	// Ultimate model (Internal:true, internal_model set, credential points at mock upstream)
	const ultimateModelID = "ultimate-internal-model"
	if err := modelsConfig.AddModel(models.ModelConfig{
		ID:            ultimateModelID,
		Name:          "Ultimate Internal Model",
		Enabled:       true,
		Internal:      true,
		Credentials: models.TestRefs("deepseek-cred"),
		InternalModel: "ultimate-internal-name",
	}); err != nil {
		t.Fatalf("Failed to add ultimate model: %v", err)
	}

	// 4. Env: point ultimate model config at our registered ID.
	// X-Force-Ultimate-Model triggers the ultimate path on the FIRST
	// call unconditionally under the fixed schedule.
	t.Setenv("APPLY_ENV_OVERRIDES", "1")
	t.Setenv("MAX_GENERATION_TIME", "10s")
	t.Setenv("ULTIMATE_MODEL_ID", ultimateModelID)
	t.Setenv("ULTIMATE_MODEL_MAX_HASH", "100")

	cfgMgr, err := config.NewManager()
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	// 5. Event bus + request store
	bus := events.NewBus()
	reqStore := store.NewRequestStore(100)

	proxyCfg := &proxy.Config{
		ConfigMgr:    cfgMgr,
		ModelsConfig: modelsConfig,
	}
	handler := proxy.NewHandler(proxyCfg, bus, reqStore, nil, tokenStore, nil, nil)

	return &testEnv{
		db:              db,
		dbStore:         dbStore,
		tokenStore:      tokenStore,
		upstream:        upstream,
		mockUp:          mockUp,
		handler:         handler,
		modelsConfig:    modelsConfig,
		ultimateModelID: ultimateModelID,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test: Reasoning Content preserved on ultimate-internal path (83814b0 fix)
// ─────────────────────────────────────────────────────────────────────────────

// TestE2E_UltimateInternalReasoningContent_Reproduction is the slot-precise
// reproduction of the bug: when the proxy rewrites a chat-completion request
// to the ultimate INTERNAL model, convertRequest must preserve the assistant
// message's reasoning_content. With the fix present (HEAD 83814b0), the
// captured upstream body contains reasoning_content at messages[1]. With the
// fix reverted, the field is missing.
func TestE2E_UltimateInternalReasoningContent_Reproduction(t *testing.T) {
	const wantReasoning = "Let me reason deeply about whether 2+2=4..."

	// Mock upstream returns a benign OpenAI-shaped response; capturing the body
	// is what matters for the assertion.
	upstreamHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id":      "chatcmpl-ultimate",
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   "ultimate-internal-name",
			"choices": []map[string]interface{}{
				{
					"index": 0,
					"message": map[string]interface{}{
						"role":    "assistant",
						"content": "ok",
					},
					"finish_reason": "stop",
				},
			},
		})
	})

	env := setupTestEnv(t, upstreamHandler)
	ctx := context.Background()

	// Token MUST have UltimateModelEnabled=true so the X-Force header takes effect.
	plaintext, _, err := env.tokenStore.CreateToken(
		ctx,
		"ultimate-internal-test-token",
		nil,
		"test",
		true, // ultimateModelEnabled
		"",
		nil,
	)
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	// Build the request: 3 messages, assistant message at index 1 carries reasoning_content.
	body := map[string]interface{}{
		"model":  "deepseek-r1", // any user-facing model
		"stream": false,
		"messages": []map[string]interface{}{
			{"role": "user", "content": "What is 2+2?"},
			{"role": "assistant", "content": "The answer is 4.", "reasoning_content": wantReasoning},
			{"role": "user", "content": "Are you sure?"},
		},
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+plaintext)
	req.Header.Set("X-Force-Ultimate-Model", "true")

	rr := httptest.NewRecorder()
	env.handler.HandleChatCompletions(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK from ultimate-internal handler, got %d: %s", rr.Code, rr.Body.String())
	}

	// ── ASSERTION (slot-precise) ────────────────────────────────────────────
	// Find the request the proxy forwarded to our capturing upstream. With
	// X-Force-Ultimate-Model + ultimate-internal config, exactly one request
	// should land on the upstream for this scenario.
	captured := env.mockUp.snapshot()
	if len(captured) == 0 {
		t.Fatalf("FAIL: no upstream request was captured. Proxy response: %s", rr.Body.String())
	}
	last := captured[len(captured)-1]

	messages, ok := last.BodyParsed["messages"].([]interface{})
	if !ok {
		t.Fatalf("FAIL: upstream body has no `messages` array. Body: %s", string(last.Body))
	}
	if len(messages) != 3 {
		t.Fatalf("FAIL: expected 3 messages in upstream body, got %d. messages=%s", len(messages), debugMessagesJSON(messages))
	}

	msg1, ok := messages[1].(map[string]interface{})
	if !ok {
		t.Fatalf("FAIL: messages[1] is not an object: %s", debugMessagesJSON(messages))
	}

	// Slot-precise check: messages[1].reasoning_content must equal the sent value.
	gotReasoning, hasReasoning := msg1["reasoning_content"].(string)
	if !hasReasoning || gotReasoning != wantReasoning {
		t.Fatalf(
			"FAIL: messages[1].reasoning_content mismatch on ultimate-internal path.\n"+
				"  has-field=%v\n"+
				"  got=%q\n"+
				"  want=%q\n"+
				"  captured upstream messages=%s",
			hasReasoning, gotReasoning, wantReasoning, debugMessagesJSON(messages),
		)
	}

	// Sanity: content fields must be intact (slot 0, 1, 2 all have their content)
	if c, _ := msg1["content"].(string); c != "The answer is 4." {
		t.Fatalf("FAIL: messages[1].content was corrupted on ultimate-internal path. got=%q, messages=%s", c, debugMessagesJSON(messages))
	}
	if c, _ := messages[0].(map[string]interface{})["content"].(string); c != "What is 2+2?" {
		t.Fatalf("FAIL: messages[0].content was corrupted. got=%q, messages=%s", c, debugMessagesJSON(messages))
	}
	if c, _ := messages[2].(map[string]interface{})["content"].(string); c != "Are you sure?" {
		t.Fatalf("FAIL: messages[2].content was corrupted. got=%q, messages=%s", c, debugMessagesJSON(messages))
	}

	// Sanity: model field should reflect the internal model name (set in convertRequest at line ~48).
	if model, _ := last.BodyParsed["model"].(string); !strings.Contains(model, "ultimate-internal") {
		t.Fatalf("FAIL: upstream body model field unexpected: got=%q", model)
	}

	// Print the captured upstream messages JSON for the run report.
	pretty, _ := json.MarshalIndent(last.BodyParsed, "", "  ")
	t.Logf("CAPTURED UPSTREAM BODY:\n%s", string(pretty))

	t.Logf("PASS: reasoning_content preserved at messages[1] on ultimate-internal path")
}

// debugMessagesJSON is used by Fatalf on failure to dump the captured upstream
// messages array for human inspection.
func debugMessagesJSON(messages []interface{}) string {
	pretty, err := json.MarshalIndent(messages, "  ", "  ")
	if err != nil {
		return fmt.Sprintf("<<marshal error: %v>>", err)
	}
	return string(pretty)
}
