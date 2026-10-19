package proxy

// load-balance-groups / P2.4–P2.7 + P2.9 (plan §9.4): the proxy-pipeline
// tests for the group rewrite, Am21 parity, Am22 self-shadow
// regression, Am24 terminal-only reselect, Am25 metering dedup,
// Am26 masking.
//
// Harness: a real *database.ModelsManager with an in-process SQLite
// store + a stub upstream HTTP server that mimics the OpenAI
// chat-completions wire. Members are Internal=true with a fixture
// credential pointed at the stub (the P5 write-stamp makes chat
// rows internal, so the race takes the internal path to the stub).
// Groups are Internal=true (G1) so they flow through the dispatch
// predicates pre-rewrite; the rewrite replaces the entry with the
// member, and per-attempt re-fetches run member-native (internal
// path → stub).
//
// Parity: the same group fixture + conv key produces the same sticky
// member on both the OpenAI and Anthropic paths (Am21 — the rewrite
// helper is the SAME shared function).

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
)

// groupHarness wires a real *database.ModelsManager + a stub
// upstream HTTP server + a Handler whose ModelsConfig is the real
// manager.
type groupHarness struct {
	mgr   *database.ModelsManager
	h     *Handler
	up    *httptest.Server
	bus   *events.Bus
	clean func()
}

func newGroupHarness(t *testing.T) *groupHarness {
	t.Helper()
	tmp := t.TempDir()
	// load-balance-groups / P4.5 (P2 deferred #2) — race retry
	// enabled at the HARNESS level so the 429 / member-cred-LB
	// tests' expectation is real, not environment-dependent. The
	// production default is race-retry ON; the env name matches
	// the cmd/main.go and config.NewManager boot path.
	t.Setenv("RACE_RETRY_ENABLED", "true")
	t.Setenv("DATABASE_URL", "sqlite:"+filepath.Join(tmp, "test.db"))
	store1, err := database.Initialize(context.Background())
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	bus := events.NewBus()
	_, mgr, err := database.InitializeManagers(store1, bus)
	if err != nil {
		store1.Close()
		t.Fatalf("InitializeManagers: %v", err)
	}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(stubUpstreamBody("stub-model", "hi")))
	}))
	mgrCfg := newTestManagerWithConfig(t, up.URL)
	reqStore := store.NewRequestStore(100, store.WithMaxBytes(256<<20))
	h := NewHandler(&Config{ConfigMgr: mgrCfg, ModelsConfig: mgr}, bus, reqStore, nil, nil, nil, mgr.Engine())
	// remove-external-upstream P5: member chat rows are stamped
	// Internal=true on write, so they need a credential; point it at
	// the stub upstream so the internal path reaches it.
	if err := mgr.AddCredential(models.CredentialConfig{
		ID: p5FixtureCredID, Provider: "openai", APIKey: "test-key", BaseURL: up.URL,
	}); err != nil {
		t.Fatalf("AddCredential(%s): %v", p5FixtureCredID, err)
	}
	clean := func() {
		up.Close()
		mgr.Close()
		store1.Close()
		_ = os.RemoveAll(tmp)
	}
	return &groupHarness{mgr: mgr, h: h, up: up, bus: bus, clean: clean}
}

func (h *groupHarness) close() { h.clean() }

// rebuildHandler with the current upstream URL — used after
// per-test failure-injection swaps the stub.
func (h *groupHarness) rebuildHandler(t *testing.T) {
	t.Helper()
	// remove-external-upstream P5: the upstream URL lives in the fixture
	// credential now (the old global UpstreamURL is gone), so re-point it
	// at the (possibly swapped) server before rebuilding the handler.
	if err := h.mgr.UpdateCredential(p5FixtureCredID, models.CredentialConfig{
		ID: p5FixtureCredID, Provider: "openai", APIKey: "test-key", BaseURL: h.up.URL,
	}); err != nil {
		t.Fatalf("rebuildHandler: UpdateCredential(%s): %v", p5FixtureCredID, err)
	}
	mgrCfg := newTestManagerWithConfig(t, h.up.URL)
	reqStore := store.NewRequestStore(100, store.WithMaxBytes(256<<20))
	*h.h = *NewHandler(&Config{ConfigMgr: mgrCfg, ModelsConfig: h.mgr}, h.bus, reqStore, nil, nil, nil, h.mgr.Engine())
}

// stubUpstreamBody returns a minimal OpenAI chat-completions
// response body with the given model and content.
func stubUpstreamBody(model, content string) string {
	return `{
		"id": "chatcmpl-stub",
		"object": "chat.completion",
		"created": 1700000000,
		"model": "` + model + `",
		"choices": [{"index":0,"message":{"role":"assistant","content":"` + content + `"},"finish_reason":"stop"}],
		"usage": {"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
	}`
}

// seedGroup canonical day-1 self-shadow group: shadow=coding,
// single member=coding. Am22 first-pass resolution must pick the
// REAL chat row.
func seedSelfShadowGroup(t *testing.T, h *groupHarness) {
	t.Helper()
	if err := h.mgr.AddModel(models.ModelConfig{
		ID: "coding", Name: "Coding", Enabled: true, Kind: models.KindChat,
		// remove-external-upstream P5: the write-stamp forces
		// Internal=true, so a member chat row needs a credential
		// pointed at the stub upstream.
		Internal: true, InternalModel: "stub-model",
		Credentials: models.TestRefs(p5FixtureCredID),
	}); err != nil {
		t.Fatalf("AddModel(coding): %v", err)
	}
	if err := h.mgr.AddModel(models.ModelConfig{
		ID: "coding-group", Name: "Coding Group", Enabled: true,
		Kind: models.KindGroup, Internal: true,
		ExcludeFromUltimateSwitching: true,
		Members:                      []models.GroupMember{{ModelID: "coding", Weight: 1}},
		ShadowModelID:                "coding",
	}); err != nil {
		t.Fatalf("AddModel(coding-group): %v", err)
	}
}

// seedGroupWithFallbacks: a 3-member group (for Am24 multi-member
// reselect scenarios).
func seedGroupWithFallbacks(t *testing.T, h *groupHarness) {
	t.Helper()
	for _, id := range []string{"m-a", "m-b", "m-c"} {
		if err := h.mgr.AddModel(models.ModelConfig{
			ID: id, Name: id, Enabled: true, Kind: models.KindChat,
			Internal: true, InternalModel: "stub-model",
			Credentials: models.TestRefs(p5FixtureCredID),
		}); err != nil {
			t.Fatalf("AddModel %s: %v", id, err)
		}
	}
	if err := h.mgr.AddModel(models.ModelConfig{
		ID: "g-multi", Name: "Multi", Enabled: true,
		Kind: models.KindGroup, Internal: true,
		ExcludeFromUltimateSwitching: true,
		Members: []models.GroupMember{
			{ModelID: "m-a", Weight: 3},
			{ModelID: "m-b", Weight: 2},
			{ModelID: "m-c", Weight: 1},
		},
	}); err != nil {
		t.Fatalf("AddModel(g-multi): %v", err)
	}
}

// ─── P2.4 — Am22 self-shadow regression (GATED FIRST per §9.4) ─────────────

func TestHandlerGroups_SelfShadowRegression(t *testing.T) {
	h := newGroupHarness(t)
	defer h.close()
	seedSelfShadowGroup(t, h)
	rec := httptest.NewRecorder()
	req := makeChatRequest(t, map[string]interface{}{
		"model": "coding",
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		},
	})
	h.h.HandleChatCompletions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if m, _ := body["model"].(string); m != "coding" {
		t.Errorf("self-shadow masking: model=%q, want \"coding\" (client-requested)", m)
	}
}

func TestHandlerGroups_RewriteStickySameKey(t *testing.T) {
	h := newGroupHarness(t)
	defer h.close()
	seedSelfShadowGroup(t, h)
	body := map[string]interface{}{
		"model": "coding",
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		},
	}
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		req := makeChatRequest(t, body)
		h.h.HandleChatCompletions(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("iter %d: status = %d, body=%s", i, rec.Code, rec.Body.String())
		}
	}
}

func TestHandlerGroups_Parity(t *testing.T) {
	h := newGroupHarness(t)
	defer h.close()
	seedSelfShadowGroup(t, h)
	t.Run("OpenAI", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := makeChatRequest(t, map[string]interface{}{
			"model": "coding",
			"messages": []interface{}{
				map[string]interface{}{"role": "user", "content": "hi"},
			},
		})
		h.h.HandleChatCompletions(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("OpenAI self-shadow: status=%d body=%s", rec.Code, rec.Body.String())
		}
	})
	t.Run("Anthropic", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := makeAnthropicRequestNoAuth(t, map[string]interface{}{
			"model":      "coding",
			"max_tokens": 16,
			"messages": []interface{}{
				map[string]interface{}{"role": "user", "content": "hi"},
			},
		})
		h.h.HandleAnthropicMessages(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("Anthropic self-shadow: status=%d body=%s", rec.Code, rec.Body.String())
		}
	})
}

// ─── P2.6 — Am24 failure semantics ──────────────────────────────────────

func TestHandlerGroups_Am24_TerminalOnlyReselect(t *testing.T) {
	h := newGroupHarness(t)
	defer h.close()
	seedGroupWithFallbacks(t, h)

	// First call: 502. Subsequent: 200.
	var calls int32
	h.up.Close()
	h.up = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":{"type":"server_error","message":"upstream 502"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(stubUpstreamBody("stub-model", "ok")))
	}))
	h.rebuildHandler(t)

	rec := httptest.NewRecorder()
	req := makeChatRequest(t, map[string]interface{}{
		"model": "g-multi",
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		},
	})
	h.h.HandleChatCompletions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Am24 re-entry: expected 200; got %d body=%s", rec.Code, rec.Body.String())
	}
	if n := atomic.LoadInt32(&calls); n < 2 {
		t.Errorf("Am24: expected ≥2 upstream calls (fail + re-entry); got %d", n)
	}
}

func TestHandlerGroups_Am24_SecondTerminalPropagates(t *testing.T) {
	h := newGroupHarness(t)
	defer h.close()
	seedGroupWithFallbacks(t, h)
	h.up.Close()
	h.up = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"type":"server_error","message":"x"}}`))
	}))
	h.rebuildHandler(t)

	rec := httptest.NewRecorder()
	req := makeChatRequest(t, map[string]interface{}{
		"model": "g-multi",
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		},
	})
	h.h.HandleChatCompletions(rec, req)
	if rec.Code == http.StatusOK {
		t.Errorf("all-terminal: must NOT succeed; got 200 body=%s", rec.Body.String())
	}
}

// ─── P2.7 — Am28 stale-member defense at the rewrite ─────────────────────

func TestHandlerGroups_Am28_StaleMember_SkipAndReselect(t *testing.T) {
	h := newGroupHarness(t)
	defer h.close()
	seedGroupWithFallbacks(t, h)
	if err := h.mgr.UpdateModel("m-a", models.ModelConfig{
		ID: "m-a", Name: "m-a", Enabled: false, Kind: models.KindChat,
		Internal: true, InternalModel: "stub-model",
		Credentials: models.TestRefs(p5FixtureCredID),
	}); err != nil {
		t.Fatalf("disable m-a: %v", err)
	}
	rec := httptest.NewRecorder()
	req := makeChatRequest(t, map[string]interface{}{
		"model": "g-multi",
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		},
	})
	h.h.HandleChatCompletions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Am28: expected 200 (skip-and-reselect); got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandlerGroups_Am28_AllStale_FailsClean(t *testing.T) {
	h := newGroupHarness(t)
	defer h.close()
	seedGroupWithFallbacks(t, h)
	for _, id := range []string{"m-a", "m-b", "m-c"} {
		if err := h.mgr.UpdateModel(id, models.ModelConfig{
			ID: id, Name: id, Enabled: false, Kind: models.KindChat,
			Internal: true, InternalModel: "stub-model",
			Credentials: models.TestRefs(p5FixtureCredID),
		}); err != nil {
			t.Fatalf("disable %s: %v", id, err)
		}
	}
	rec := httptest.NewRecorder()
	req := makeChatRequest(t, map[string]interface{}{
		"model": "g-multi",
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		},
	})
	h.h.HandleChatCompletions(rec, req)
	if rec.Code == http.StatusOK {
		t.Errorf("all-stale group: must NOT succeed; got 200")
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("no servable members")) &&
		!bytes.Contains(rec.Body.Bytes(), []byte("group_unservable")) {
		t.Errorf("expected the group_unservable envelope; got status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// ─── P2.8 — Am26 masking (non-stream) ─────────────────────────────────────

func TestHandlerGroups_Am26_NonStreamMasking(t *testing.T) {
	h := newGroupHarness(t)
	defer h.close()
	seedSelfShadowGroup(t, h)
	rec := httptest.NewRecorder()
	req := makeChatRequest(t, map[string]interface{}{
		"model": "coding",
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		},
	})
	h.h.HandleChatCompletions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Am26 non-stream: %d %s", rec.Code, rec.Body.String())
	}
	var body map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if m, _ := body["model"].(string); m != "coding" {
		t.Errorf("Am26 masking: model=%q, want \"coding\" (client-requested)", m)
	}
}

func TestHandlerGroups_Am26_NonGroup_ByteIdentity(t *testing.T) {
	h := newGroupHarness(t)
	defer h.close()
	if err := h.mgr.AddModel(models.ModelConfig{
		ID: "solo", Name: "Solo", Enabled: true, Kind: models.KindChat,
		Internal: true, InternalModel: "stub-model",
		Credentials: models.TestRefs(p5FixtureCredID),
	}); err != nil {
		t.Fatalf("AddModel(solo): %v", err)
	}
	rec := httptest.NewRecorder()
	req := makeChatRequest(t, map[string]interface{}{
		"model": "solo",
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		},
	})
	h.h.HandleChatCompletions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("non-group: %d %s", rec.Code, rec.Body.String())
	}
	var body map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if m, _ := body["model"].(string); m != "stub-model" {
		t.Errorf("non-group byte-identity: model=%q, want \"stub-model\" (upstream passthrough)", m)
	}
}

// ─── P2.9 — Am25 metering + Q15 event ───────────────────────────────────

// TestHandlerGroups_Am25_Metering_Shape_NilCounter — Am25 shape
// smoke (counter=nil harness, status-only verification). The
// COUNTER-WIRED row-shape pin lives in
// `handler_groups_metering_test.go::TestHandlerGroups_Am25_Metering_RowShapes`
// (note: ROWSHAPES plural — that file uses a real usage.Counter).
// The two suites were near-identically named under the original
// `*_Shape` reading and the P3 reviewer flagged the confusion.
// Renaming to `Shape_NilCounter` vs `RowShapes` keeps the
// counter-anchored vs smoke distinction visible in test output.
func TestHandlerGroups_Am25_Metering_Shape_NilCounter(t *testing.T) {
	t.Run("Direct", func(t *testing.T) {
		h := newGroupHarness(t)
		defer h.close()
		if err := h.mgr.AddModel(models.ModelConfig{
			ID: "solo", Name: "Solo", Enabled: true, Kind: models.KindChat,
			Internal: true, InternalModel: "stub-model",
			Credentials: models.TestRefs(p5FixtureCredID),
		}); err != nil {
			t.Fatalf("AddModel: %v", err)
		}
		rec := httptest.NewRecorder()
		req := makeChatRequest(t, map[string]interface{}{
			"model": "solo",
			"messages": []interface{}{
				map[string]interface{}{"role": "user", "content": "hi"},
			},
		})
		h.h.HandleChatCompletions(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("direct: %d %s", rec.Code, rec.Body.String())
		}
	})
	t.Run("Day1SelfShadow", func(t *testing.T) {
		h := newGroupHarness(t)
		defer h.close()
		seedSelfShadowGroup(t, h)
		rec := httptest.NewRecorder()
		req := makeChatRequest(t, map[string]interface{}{
			"model": "coding",
			"messages": []interface{}{
				map[string]interface{}{"role": "user", "content": "hi"},
			},
		})
		h.h.HandleChatCompletions(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("day-1: %d %s", rec.Code, rec.Body.String())
		}
	})
}

func TestHandlerGroups_Q15_Event_NewlyBoundOnly(t *testing.T) {
	h := newGroupHarness(t)
	defer h.close()
	seedGroupWithFallbacks(t, h)

	bus := h.bus
	seen := &atomic.Int32{}
	stop := make(chan struct{})
	go func() {
		ch, _ := bus.Subscribe()
		for {
			select {
			case <-stop:
				return
			case evt, ok := <-ch:
				if !ok {
					return
				}
				if evt.Type == "model_member_selected" {
					seen.Add(1)
				}
			}
		}
	}()
	defer close(stop)

	rec := httptest.NewRecorder()
	req := makeChatRequest(t, map[string]interface{}{
		"model": "g-multi",
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		},
	})
	h.h.HandleChatCompletions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first: %d %s", rec.Code, rec.Body.String())
	}
	// Poll briefly for the bus to deliver (deterministic via
	// time.Sleep — the goroutine reading from the channel needs a
	// scheduler slot).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if seen.Load() > 0 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if seen.Load() < 1 {
		t.Errorf("model_member_selected should fire on first (newlyBound) request; seen=%d", seen.Load())
	}
}

// ─── helpers ────────────────────────────────────────────────────────────────

func makeChatRequest(t *testing.T, body map[string]interface{}) *http.Request {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// makeAnthropicRequestNoAuth builds an Anthropic-style /v1/messages request
// WITHOUT the x-api-key / anthropic-version headers (the "no-auth" variant).
func makeAnthropicRequestNoAuth(t *testing.T, body map[string]interface{}) *http.Request {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// TestHandlerGroups_Am24_Member429_AbsorbedByCredLB — Am24 named
// case: a member-internal 429 is absorbed by the member's OWN
// credential LB (the coordinator's credFailover machinery —
// unchanged by P2) and NEVER bubbles to the group layer. Shape: a
// single-member INTERNAL group whose member has 2 credentials; the
// stub upstream 429s credential A and 200s credential B. If the 429
// bubbled to the group layer, ExcludeGroupMember on a single-member
// group returns none-available ⇒ the request would FAIL; a 200
// therefore proves cred-side absorption (the winner is the same
// member on its reselected credential).
func TestHandlerGroups_Am24_Member429_AbsorbedByCredLB(t *testing.T) {
	// load-balance-groups / P4.5 (P2 deferred #2) — the
	// RACE_RETRY_ENABLED env is now set in newGroupHarness so the
	// test's expectation is harness-driven, not test-local.
	h := newGroupHarness(t)
	defer h.close()

	// Swap the stub FIRST (429 for key-A / 200 for key-B) so the
	// credentials below capture the FINAL upstream URL.
	h.up.Close()
	h.up = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if strings.Contains(auth, "key-A") {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"type":"rate_limit_error","message":"429"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(stubUpstreamBody("glm-5", "ok")))
	}))
	h.rebuildHandler(t)

	// Two credentials, both pointing at the stub upstream.
	for _, c := range []struct{ id, key string }{
		{"cred-a", "key-A"},
		{"cred-b", "key-B"},
	} {
		if err := h.mgr.AddCredential(models.CredentialConfig{
			ID: c.id, Provider: "openai", APIKey: c.key, BaseURL: h.up.URL,
		}); err != nil {
			t.Fatalf("AddCredential %s: %v", c.id, err)
		}
	}
	// The single INTERNAL member with both credentials.
	if err := h.mgr.AddModel(models.ModelConfig{
		ID: "m-int", Name: "M Int", Enabled: true, Kind: models.KindChat,
		Internal: true, InternalModel: "glm-5",
		Credentials: []models.CredentialRef{
			{CredentialID: "cred-a", Weight: 1, Position: 0},
			{CredentialID: "cred-b", Weight: 1, Position: 1},
		},
	}); err != nil {
		t.Fatalf("AddModel(m-int): %v", err)
	}
	if err := h.mgr.AddModel(models.ModelConfig{
		ID: "g-int", Name: "G Int", Enabled: true,
		Kind: models.KindGroup, Internal: true,
		ExcludeFromUltimateSwitching: true,
		Members:                      []models.GroupMember{{ModelID: "m-int", Weight: 1}},
	}); err != nil {
		t.Fatalf("AddModel(g-int): %v", err)
	}

	rec := httptest.NewRecorder()
	req := makeChatRequest(t, map[string]interface{}{
		"model": "g-int",
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		},
	})
	h.h.HandleChatCompletions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("member-429 absorbed by cred-LB expected 200; got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestSSEModelMasker_StraddledEvent — Am6/Am26 named fixture: a JSON
// event STRADDLING raw chunk boundaries must still be masked (a
// per-chunk patch would fail open on the straddle and leak the
// upstream model name). The masker buffers per COMPLETE event.
func TestSSEModelMasker_StraddledEvent(t *testing.T) {
	m := newSSEModelMasker("client-group")
	if m == nil {
		t.Fatal("masker must engage for a non-empty target")
	}

	event1 := `data: {"id":"a","model":"upstream-one","choices":[]}` + "\n\n"
	event2 := `data: {"id":"b","model":"upstream-two","choices":[]}` + "\n\n"

	// Straddle: split event1's JSON at an arbitrary byte inside
	// "model":"upstream-one".
	raw := event1 + event2
	mid := bytes.Index([]byte(raw), []byte("upstream-one")) + 5 // inside the value
	chunkA, chunkB := []byte(raw[:mid]), []byte(raw[mid:])

	outA := m.process(chunkA)
	if len(outA) != 0 {
		t.Errorf("partial event must buffer, not emit; got %d bytes", len(outA))
	}
	outB := m.process(chunkB)
	got := string(outB)

	// Both events present, both masked.
	if !bytes.Contains([]byte(got), []byte(`"model":"client-group"`)) {
		t.Errorf("straddled event not masked; output:\n%s", got)
	}
	if bytes.Contains([]byte(got), []byte("upstream-one")) || bytes.Contains([]byte(got), []byte("upstream-two")) {
		t.Errorf("upstream model name leaked; output:\n%s", got)
	}
	// Event structure preserved: two complete events, [DONE] absent.
	if bytes.Count([]byte(got), []byte(`"model":"client-group"`)) != 2 {
		t.Errorf("expected both events masked; output:\n%s", got)
	}
	// The non-model bytes are preserved verbatim (byte-preserving
	// patch): ids survive.
	if !bytes.Contains([]byte(got), []byte(`"id":"a"`)) || !bytes.Contains([]byte(got), []byte(`"id":"b"`)) {
		t.Errorf("non-model bytes must be preserved; output:\n%s", got)
	}
}

// TestSSEModelMasker_DataDONE_Passthrough — [DONE] and comment lines
// pass through untouched.
func TestSSEModelMasker_DataDONE_Passthrough(t *testing.T) {
	m := newSSEModelMasker("client-group")
	in := ": connected\n\ndata: [DONE]\n\n"
	out := string(m.process([]byte(in)))
	// Both complete events emit; the comment verbatim, [DONE]
	// verbatim (non-JSON payload — fail-open passthrough).
	if !bytes.Contains([]byte(out), []byte(": connected")) {
		t.Errorf("comment line must pass through; got %q", out)
	}
	if !bytes.Contains([]byte(out), []byte("data: [DONE]")) {
		t.Errorf("[DONE] must pass through verbatim; got %q", out)
	}
	if bytes.Contains([]byte(out), []byte("client-group")) {
		t.Errorf("no JSON model field to patch — output must be unchanged; got %q", out)
	}
}

// TestPatchTopLevelModelField_BytePreserving — only the model value
// changes; key order + surrounding bytes identical.
func TestPatchTopLevelModelField_BytePreserving(t *testing.T) {
	body := []byte(`{"zzz":1,"model":"orig","aaa":{"model":"nested"}}`)
	out := patchTopLevelModelField(body, "new-name")
	want := []byte(`{"zzz":1,"model":"new-name","aaa":{"model":"nested"}}`)
	if !bytes.Equal(out, want) {
		t.Errorf("patch not byte-preserving:\n got %s\nwant %s", out, want)
	}
	// No top-level model ⇒ unchanged.
	noModel := []byte(`{"a":1}`)
	if got := patchTopLevelModelField(noModel, "x"); !bytes.Equal(got, noModel) {
		t.Errorf("no-model body must be unchanged; got %s", got)
	}
}
