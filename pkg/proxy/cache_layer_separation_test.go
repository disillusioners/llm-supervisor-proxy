package proxy

// wi-4d2c8e17 — cache layer-separation acceptance tests.
//
// Covers the two rulings:
//
//	RULING 1 (semantics) — the cache must not decide the group pick;
//	  marks suppress only the ATTEMPT and the walk proceeds INTO the
//	  member's chain; a member is judged failed only after hedge +
//	  chain; chain-only (inactive) targets stay reachable; a
//	  single-model list can still hedge; a client-attributable head
//	  failure returns the upstream 400 promptly; an empty 200 does
//	  not win.
//	RULING 2 (structure) — the group pick reads only GROUP-owned
//	  rotation state; the upstream/model caches are never consulted
//	  by it (structural: store_group_rewrite.go imports neither).

import (
	"context"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/credentiallb"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/modelcache"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/skipcache"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
)

// ───────────────────────── handler-level fixtures ─────────────────────────

// sepStubState records wire models attempted and returns a per-model
// status (200 default).
type sepStubState struct {
	mu       sync.Mutex
	models   []string
	statuses map[string]int
}

func (s *sepStubState) record(model string) {
	s.mu.Lock()
	s.models = append(s.models, model)
	s.mu.Unlock()
}

func (s *sepStubState) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.models))
	copy(out, s.models)
	return out
}

func newSepStub(st *sepStubState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		model, _ := body["model"].(string)
		st.record(model)
		status := st.statuses[model]
		if status == 0 {
			status = http.StatusOK
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status >= 400 {
			_, _ = w.Write([]byte(`{"error":{"type":"server_error","message":"stub failure"}}`))
			return
		}
		_, _ = w.Write([]byte(stubUpstreamBody(model, "hi")))
	}
}

// rebuildHandlerWithCaches re-points the fixture credential at the live
// stub and rebuilds the handler with BOTH caches wired.
func rebuildHandlerWithCaches(t *testing.T, h *groupHarness, up *skipcache.Cache, mc *modelcache.Cache) {
	t.Helper()
	if h.mgr.GetCredential(p5FixtureCredID) != nil {
		if err := h.mgr.UpdateCredential(p5FixtureCredID, models.CredentialConfig{
			ID: p5FixtureCredID, Provider: "openai", APIKey: "test-key", BaseURL: h.up.URL,
		}); err != nil {
			t.Fatalf("UpdateCredential: %v", err)
		}
	}
	mgrCfg := newTestManagerWithConfig(t, h.up.URL)
	reqStore := store.NewRequestStore(100, store.WithMaxBytes(256<<20))
	*h.h = *NewHandler(&Config{
		ConfigMgr:         mgrCfg,
		ModelsConfig:      h.mgr,
		UpstreamSkipCache: up,
		ModelSkipCache:    mc,
	}, h.bus, reqStore, nil, nil, nil, h.mgr.Engine(), nil)
}

// addSepChatModel adds an internal chat model with a fallback chain.
func addSepChatModel(t *testing.T, h *groupHarness, id string, enabled bool, chain ...string) {
	t.Helper()
	if err := h.mgr.AddModel(models.ModelConfig{
		ID: id, Name: id, Enabled: enabled, Kind: models.KindChat,
		Internal: true, InternalModel: id,
		Credentials:   models.TestRefs(p5FixtureCredID),
		FallbackChain: chain,
	}); err != nil {
		t.Fatalf("AddModel(%s): %v", id, err)
	}
}

// seedSepGroup builds the incident topology:
//
//	agentic-Main — default (weight 10), chain [coding4]
//	coding4      — INACTIVE as a member, reachable only via the chain
//	backup-1/2   — backup_only pool
func seedSepGroup(t *testing.T, h *groupHarness, chainOfMain ...string) {
	t.Helper()
	addSepChatModel(t, h, "agentic-Main", true, chainOfMain...)
	if len(chainOfMain) > 0 {
		for _, c := range chainOfMain {
			addSepChatModel(t, h, c, false) // INACTIVE — chain-only by design
		}
	}
	addSepChatModel(t, h, "backup-1", true)
	addSepChatModel(t, h, "backup-2", true)
	if err := h.mgr.AddModel(models.ModelConfig{
		ID: "agentic-group", Name: "agentic-group", Enabled: true,
		Kind: models.KindGroup, Internal: true,
		Members: []models.GroupMember{
			{ModelID: "agentic-Main", Weight: 10},
			{ModelID: "backup-1", Weight: 1, BackupOnly: true},
			{ModelID: "backup-2", Weight: 1, BackupOnly: true},
		},
	}); err != nil {
		t.Fatalf("AddModel(agentic-group): %v", err)
	}
}

func countModel(attempts []string, want string) int {
	n := 0
	for _, m := range attempts {
		if m == want {
			n++
		}
	}
	return n
}

func indexOfModel(attempts []string, want string) int {
	for i, m := range attempts {
		if m == want {
			return i
		}
	}
	return -1
}

// ───────────────────────────── T-A / T-B ─────────────────────────────

// TestCacheLayer_T_A_MarkedHeadStillPicked_ChainServes: agentic-Main is
// MODEL-marked ⇒ the group head is STILL agentic-Main (pick unchanged),
// its attempt is SUPPRESSED (never reaches the stub), and the walk
// proceeds into its chain (coding4), which serves.
func TestCacheLayer_T_A_MarkedHeadStillPicked_ChainServes(t *testing.T) {
	h := newGroupHarness(t)
	defer h.close()
	seedSepGroup(t, h, "coding4")

	st := &sepStubState{statuses: map[string]int{
		"agentic-Main": http.StatusOK,
		"coding4":      http.StatusOK,
	}}
	h.up.Close()
	h.up = httptest.NewServer(newSepStub(st))

	up := skipcache.New()
	mc := modelcache.New()
	rebuildHandlerWithCaches(t, h, up, mc)
	mc.Mark("agentic-Main", 10*time.Minute)

	rec := httptest.NewRecorder()
	req := makeChatRequest(t, map[string]interface{}{
		"model":    "agentic-group",
		"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
	})
	h.h.HandleChatCompletions(rec, req)

	attempted := st.snapshot()
	if rec.Code != http.StatusOK {
		t.Fatalf("request not served (status=%d); attempted=%v", rec.Code, attempted)
	}
	if got := countModel(attempted, "agentic-Main"); got != 0 {
		t.Fatalf("marked head agentic-Main was ATTEMPTED %d time(s) — the model mark must suppress the attempt at zero cost; attempted=%v", got, attempted)
	}
	if got := countModel(attempted, "coding4"); got != 1 {
		t.Fatalf("chain target coding4 attempted %d time(s), want 1 (walk must proceed INTO the head's chain); attempted=%v", got, attempted)
	}
	if got := countModel(attempted, "backup-1") + countModel(attempted, "backup-2"); got != 0 {
		t.Fatalf("a backup was attempted %d time(s) — a mark must NOT flip the group into backup mode; attempted=%v", got, attempted)
	}
	if len(attempted) == 0 || attempted[0] != "coding4" {
		t.Fatalf("first attempt = %v, want coding4 (head suppressed, chain first)", attempted)
	}

	// D7c — assert the HEAD identity DIRECTLY from the group-pick event
	// (newlyBound on this first request), not only via "no backup
	// attempted". The mark must not change WHICH member was picked.
	foundHead := false
	for _, evt := range drainAllEvents(h.bus) {
		if evt.Type != "model_member_selected" {
			continue
		}
		d, ok := evt.Data.(map[string]interface{})
		if !ok {
			continue
		}
		if asString(d["member_id"]) == "agentic-Main" {
			foundHead = true
		}
	}
	if !foundHead {
		t.Fatal("head identity not observable as agentic-Main in model_member_selected — the mark must not change the pick (R1)")
	}
}

// TestCacheLayer_T_B_MarkDoesNotFlipToBackup: a mark on the sole
// default member must NOT make the group switch to a backup member —
// even when every backup is healthy and would serve.
func TestCacheLayer_T_B_MarkDoesNotFlipToBackup(t *testing.T) {
	h := newGroupHarness(t)
	defer h.close()
	seedSepGroup(t, h, "coding4")

	st := &sepStubState{statuses: map[string]int{
		"agentic-Main": http.StatusOK,
		"coding4":      http.StatusOK,
		"backup-1":     http.StatusOK,
		"backup-2":     http.StatusOK,
	}}
	h.up.Close()
	h.up = httptest.NewServer(newSepStub(st))

	up := skipcache.New()
	mc := modelcache.New()
	rebuildHandlerWithCaches(t, h, up, mc)
	mc.Mark("agentic-Main", 10*time.Minute)

	rec := httptest.NewRecorder()
	req := makeChatRequest(t, map[string]interface{}{
		"model":    "agentic-group",
		"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
	})
	h.h.HandleChatCompletions(rec, req)

	attempted := st.snapshot()
	if rec.Code != http.StatusOK {
		t.Fatalf("request not served (status=%d)", rec.Code)
	}
	if got := countModel(attempted, "backup-1") + countModel(attempted, "backup-2"); got != 0 {
		t.Fatalf("backup member(s) attempted %d time(s) — the mark flipped the group pick into backup mode (R1 violation); attempted=%v", got, attempted)
	}
	if countModel(attempted, "coding4") != 1 {
		t.Fatalf("chain target coding4 not attempted exactly once; attempted=%v", attempted)
	}
}

// ───────────────────────────── T-C ─────────────────────────────

// TestCacheLayer_T_C_ChainBeforeMemberAdvance: a member is judged
// failed only after its chain is consumed, then the next group member
// (backup) is attempted.
func TestCacheLayer_T_C_ChainBeforeMemberAdvance(t *testing.T) {
	h := newGroupHarness(t)
	defer h.close()
	// m1 (default) → chain [m2]; m3 is a backup.
	addSepChatModel(t, h, "m1", true, "m2")
	addSepChatModel(t, h, "m2", true)
	addSepChatModel(t, h, "m3", true)
	if err := h.mgr.AddModel(models.ModelConfig{
		ID: "chain-group", Name: "chain-group", Enabled: true,
		Kind: models.KindGroup, Internal: true,
		Members: []models.GroupMember{
			{ModelID: "m1", Weight: 10},
			{ModelID: "m3", Weight: 1, BackupOnly: true},
		},
	}); err != nil {
		t.Fatalf("AddModel(chain-group): %v", err)
	}

	st := &sepStubState{statuses: map[string]int{
		"m1": http.StatusServiceUnavailable,
		"m2": http.StatusServiceUnavailable,
		"m3": http.StatusOK,
	}}
	h.up.Close()
	h.up = httptest.NewServer(newSepStub(st))
	rebuildHandlerWithCaches(t, h, skipcache.New(), modelcache.New())

	rec := httptest.NewRecorder()
	req := makeChatRequest(t, map[string]interface{}{
		"model":    "chain-group",
		"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
	})
	h.h.HandleChatCompletions(rec, req)

	attempted := st.snapshot()
	if rec.Code != http.StatusOK {
		t.Fatalf("request not served (status=%d); attempted=%v", rec.Code, attempted)
	}
	i1, i2, i3 := indexOfModel(attempted, "m1"), indexOfModel(attempted, "m2"), indexOfModel(attempted, "m3")
	if i1 < 0 || i2 < 0 || i3 < 0 {
		t.Fatalf("missing an attempt: m1@%d m2@%d m3@%d attempted=%v", i1, i2, i3, attempted)
	}
	if !(i1 < i2 && i2 < i3) {
		t.Fatalf("order = m1@%d m2@%d m3@%d — want m1 then its chain m2 before the next member m3", i1, i2, i3)
	}
}

// ───────────────────────────── T-D ─────────────────────────────

// TestCacheLayer_T_D_InactiveChainTargetServable: a model created
// INACTIVE (not a group member, reachable only as a chain target) is
// valid config and servable at execution.
func TestCacheLayer_T_D_InactiveChainTargetServable(t *testing.T) {
	h := newGroupHarness(t)
	defer h.close()

	// Validation must ALLOW a disabled chain target (R4).
	if err := h.mgr.AddModel(models.ModelConfig{
		ID: "chain-only", Name: "chain-only", Enabled: false, Kind: models.KindChat,
		Internal: true, InternalModel: "chain-only",
		Credentials: models.TestRefs(p5FixtureCredID),
	}); err != nil {
		t.Fatalf("AddModel(disabled chain target) rejected by validation: %v", err)
	}
	addSepChatModel(t, h, "head", true, "chain-only")
	if err := h.mgr.AddModel(models.ModelConfig{
		ID: "td-group", Name: "td-group", Enabled: true,
		Kind: models.KindGroup, Internal: true,
		Members: []models.GroupMember{{ModelID: "head", Weight: 1}},
	}); err != nil {
		t.Fatalf("AddModel(td-group): %v", err)
	}

	st := &sepStubState{statuses: map[string]int{
		"head":       http.StatusServiceUnavailable,
		"chain-only": http.StatusOK,
	}}
	h.up.Close()
	h.up = httptest.NewServer(newSepStub(st))
	rebuildHandlerWithCaches(t, h, skipcache.New(), modelcache.New())

	rec := httptest.NewRecorder()
	req := makeChatRequest(t, map[string]interface{}{
		"model":    "td-group",
		"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
	})
	h.h.HandleChatCompletions(rec, req)

	attempted := st.snapshot()
	if countModel(attempted, "chain-only") != 1 {
		t.Fatalf("inactive chain-only target not attempted exactly once; attempted=%v", attempted)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("request not served by the inactive chain target (status=%d); attempted=%v", rec.Code, attempted)
	}
}

// ───────────────────────── coordinator fixtures ─────────────────────────

type sepModel struct {
	id       string
	creds    int
	chain    []string
	disabled bool
}

// newSepCoord builds a coordinator over an internal model chain using a
// global provider stub.
func newSepCoord(t *testing.T, chain []sepModel, provider providers.Provider, stream, liveGate bool, convKey string, tune func(*ConfigSnapshot)) (*raceCoordinator, *events.Bus) {
	t.Helper()
	mock := &mockModelsConfig{}
	engine := credentiallb.NewEngine(time.Hour, time.Hour, 7, 60*time.Second)
	t.Cleanup(engine.Stop)

	modelIDs := make([]string, 0, len(chain))
	for _, m := range chain {
		creds := m.creds
		if creds <= 0 {
			creds = 1
		}
		refs := make([]string, 0, creds)
		for i := 0; i < creds; i++ {
			id := fmt.Sprintf("cred-%s-%d", m.id, i)
			mock.AddCredential(models.CredentialConfig{ID: id, Provider: "openai", APIKey: "k-" + id, BaseURL: "http://up-" + id})
			refs = append(refs, id)
		}
		mock.AddModel(models.ModelConfig{
			ID: m.id, Name: m.id, Enabled: !m.disabled, Internal: true,
			InternalModel: m.id, Credentials: models.TestRefs(refs...),
			FallbackChain: m.chain,
		})
		engine.RebindFromStore(m.id, models.TestRefs(refs...))
		modelIDs = append(modelIDs, m.id)
	}
	resolver := &engineBackedResolver{mockModelsConfig: mock, engine: engine}

	origNew := newProviderClient
	newProviderClient = func(providerType, apiKey, baseURL string) (providers.Provider, error) {
		return provider, nil
	}
	t.Cleanup(func() { newProviderClient = origNew })

	bus := events.NewBus()
	cfg := newTestConfigSnapshot(modelIDs[0])
	cfg.ModelsConfig = resolver
	cfg.StreamDeadline = 5 * time.Second
	cfg.MaxGenerationTime = 8 * time.Second
	if tune != nil {
		tune(cfg)
	}
	rawBody := []byte(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hello"}],"stream":%v}`, modelIDs[0], stream))
	coord := newRaceCoordinatorWithEvents(context.Background(), cfg, newTestRequest(), rawBody, modelIDs, bus, "sep-req", false, engine, convKey, liveGate, nil)
	return coord, bus
}

// sepStatusProvider returns a per-model status (non-stream).
type sepStatusProvider struct {
	mu       sync.Mutex
	calls    []string
	statuses map[string]int
	empty    map[string]bool   // models that answer 200 with NO usable content
	toolOnly map[string]bool   // models that answer 200 with ONLY tool_calls (no text)
	finish   map[string]string // models that answer with an explicit finish_reason
}

func (p *sepStatusProvider) Name() string { return "sep-status" }

func (p *sepStatusProvider) ChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (*providers.ChatCompletionResponse, error) {
	p.mu.Lock()
	p.calls = append(p.calls, req.Model)
	status := p.statuses[req.Model]
	empty := p.empty[req.Model]
	toolOnly := p.toolOnly[req.Model]
	finish := p.finish[req.Model]
	p.mu.Unlock()
	if status == 0 {
		status = http.StatusOK
	}
	if status >= 400 {
		return nil, &providers.ProviderError{Provider: "sep-status", StatusCode: status, Message: "stub failure"}
	}
	if finish != "" {
		return &providers.ChatCompletionResponse{
			ID: "cc", Object: "chat.completion", Created: time.Now().Unix(), Model: req.Model,
			Choices: []providers.Choice{{Index: 0, Message: &providers.ChatMessage{Role: "assistant", Content: ""}, FinishReason: finish}},
			Usage:   providers.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
		}, nil
	}
	if toolOnly {
		return &providers.ChatCompletionResponse{
			ID: "cc", Object: "chat.completion", Created: time.Now().Unix(), Model: req.Model,
			Choices: []providers.Choice{{
				Index: 0,
				Message: &providers.ChatMessage{
					Role: "assistant",
					ToolCalls: []providers.ToolCall{{
						ID: "call_1", Type: "function",
						Function: providers.ToolCallFunction{Name: "get_weather", Arguments: `{"city":"x"}`},
					}},
				},
				FinishReason: "tool_calls",
			}},
			Usage: providers.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
		}, nil
	}
	content := "ok"
	if empty {
		content = ""
	}
	return &providers.ChatCompletionResponse{
		ID: "cc", Object: "chat.completion", Created: time.Now().Unix(), Model: req.Model,
		Choices: []providers.Choice{{Index: 0, Message: &providers.ChatMessage{Role: "assistant", Content: content}, FinishReason: "stop"}},
		Usage:   providers.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
	}, nil
}

func (p *sepStatusProvider) StreamChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (<-chan providers.StreamEvent, error) {
	return nil, &providers.ProviderError{StatusCode: 500, Message: "not stream"}
}

func (p *sepStatusProvider) IsRetryable(err error) bool { return true }

func (p *sepStatusProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

// sepSlowProvider sleeps then succeeds — drives the idle hedge.
type sepSlowProvider struct {
	mu    sync.Mutex
	calls int
	delay time.Duration
}

func (p *sepSlowProvider) Name() string { return "sep-slow" }

func (p *sepSlowProvider) ChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (*providers.ChatCompletionResponse, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	select {
	case <-time.After(p.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &providers.ChatCompletionResponse{
		ID: "cc", Object: "chat.completion", Created: time.Now().Unix(), Model: req.Model,
		Choices: []providers.Choice{{Index: 0, Message: &providers.ChatMessage{Role: "assistant", Content: "ok"}, FinishReason: "stop"}},
	}, nil
}

func (p *sepSlowProvider) StreamChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (<-chan providers.StreamEvent, error) {
	return nil, &providers.ProviderError{StatusCode: 500, Message: "not stream"}
}

func (p *sepSlowProvider) IsRetryable(err error) bool { return true }

func (p *sepSlowProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// sepStreamProvider emits content for every model except `empty`,
// which emits ONLY a done event (a content-less 200).
type sepStreamProvider struct {
	mu    sync.Mutex
	calls []string
	empty map[string]bool
}

func (p *sepStreamProvider) Name() string { return "sep-stream" }

func (p *sepStreamProvider) ChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (*providers.ChatCompletionResponse, error) {
	return nil, &providers.ProviderError{StatusCode: 500, Message: "not non-stream"}
}

func (p *sepStreamProvider) StreamChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (<-chan providers.StreamEvent, error) {
	p.mu.Lock()
	p.calls = append(p.calls, req.Model)
	p.mu.Unlock()
	ch := make(chan providers.StreamEvent, 4)
	if p.empty[req.Model] {
		ch <- providers.StreamEvent{Type: "done", FinishReason: "stop"}
		close(ch)
		return ch, nil
	}
	ch <- providers.StreamEvent{Type: "content", Content: "hi"}
	ch <- providers.StreamEvent{Type: "done", FinishReason: "stop"}
	close(ch)
	return ch, nil
}

func (p *sepStreamProvider) IsRetryable(err error) bool { return true }

func (p *sepStreamProvider) callsFor(model string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, m := range p.calls {
		if m == model {
			n++
		}
	}
	return n
}

// ───────────────────────────── T-E ─────────────────────────────

// TestCacheLayer_T_E_SingleModelGetsHedge: a single-model list must
// still get its credential-level hedge (R5/F1) — an idle main spawns a
// second attempt.
func TestCacheLayer_T_E_SingleModelGetsHedge(t *testing.T) {
	provider := &sepSlowProvider{delay: 150 * time.Millisecond}
	coord, _ := newSepCoord(t,
		[]sepModel{{id: "solo", creds: 2}},
		provider, false, false, "conv-hedge",
		func(cfg *ConfigSnapshot) {
			cfg.IdleTimeout = 40 * time.Millisecond
			cfg.RaceParallelOnIdle = true
			cfg.RaceMaxParallel = 3
			cfg.MaxGenerationTime = 5 * time.Second
		})
	coord.Start()
	if winner := coord.WaitForWinner(); winner == nil {
		t.Fatalf("no winner on the slow single-model list (calls=%d)", provider.callCount())
	}
	if got := provider.callCount(); got < 2 {
		t.Fatalf("upstream calls = %d, want >= 2 (a single-model list must still hedge)", got)
	}
	secondRows := 0
	for _, row := range spawnOrderSnapshot(coord) {
		if strings.HasPrefix(row, "second:") {
			secondRows++
		}
	}
	// D7b: the hedge is BOUNDED — exactly one second row, never more.
	if secondRows != 1 {
		t.Fatalf("second (hedge) rows = %d, want exactly 1; spawns=%v", secondRows, spawnOrderSnapshot(coord))
	}
}

// ───────────────────────────── T-F ─────────────────────────────

// TestCacheLayer_T_F_Client400_ReturnsPromptly: a client-attributable
// head failure with a chain must return the upstream 400 PROMPTLY —
// never spin to the hard deadline.
func TestCacheLayer_T_F_Client400_ReturnsPromptly(t *testing.T) {
	provider := &sepStatusProvider{statuses: map[string]int{
		"m1": http.StatusBadRequest,
		"m2": http.StatusOK, // must NEVER be called (client-attributable fail-fast)
	}}
	coord, _ := newSepCoord(t,
		[]sepModel{{id: "m1", chain: []string{"m2"}}, {id: "m2"}},
		provider, false, false, "conv-400",
		func(cfg *ConfigSnapshot) {
			cfg.MaxGenerationTime = 1500 * time.Millisecond
			cfg.StreamDeadline = 1500 * time.Millisecond
		})
	start := time.Now()
	coord.Start()
	winner := coord.WaitForWinner()
	elapsed := time.Since(start)

	if winner != nil {
		t.Fatalf("expected no winner on a client-attributable 400, got %s", winner.modelID)
	}
	if provider.callCount() != 1 {
		t.Fatalf("upstream calls = %d, want 1 (400 must not advance the chain)", provider.callCount())
	}
	if elapsed > 900*time.Millisecond {
		t.Fatalf("resolution took %v — the 400 was not returned promptly (spun toward the 1.5s deadline)", elapsed)
	}
	if info := coord.GetFinalErrorInfo(); info.HTTPStatus != http.StatusBadRequest {
		t.Fatalf("final HTTP status = %d, want 400 (upstream client-attributable status must surface, not 502/timeout)", info.HTTPStatus)
	}
}

// ───────────────────────────── T-G ─────────────────────────────

// TestCacheLayer_T_G_Empty200DoesNotWin: a content-less 200 (only
// finish + [DONE]) must not be declared the winner; the chain advances
// to a model that actually produces content.
func TestCacheLayer_T_G_Empty200DoesNotWin(t *testing.T) {
	provider := &sepStreamProvider{empty: map[string]bool{"m1": true}}
	coord, _ := newSepCoord(t,
		[]sepModel{{id: "m1", chain: []string{"m2"}}, {id: "m2"}},
		provider, true /*stream*/, true /*liveGate*/, "conv-empty",
		func(cfg *ConfigSnapshot) { cfg.MaxGenerationTime = 5 * time.Second })
	coord.Start()
	winner := coord.WaitForWinner()
	if winner == nil {
		t.Fatalf("no winner — the empty 200 hung the race instead of advancing (m1 calls=%d, m2 calls=%d)", provider.callsFor("m1"), provider.callsFor("m2"))
	}
	if winner.modelID != "m2" {
		t.Fatalf("winner = %s, want m2 (the content-less m1 must not win)", winner.modelID)
	}
	if provider.callsFor("m2") != 1 {
		t.Fatalf("m2 calls = %d, want 1 (chain advanced after the empty 200)", provider.callsFor("m2"))
	}
}

// ───────────────────────── isolation pins (RULING 2) ─────────────────────────

// TestCacheLayer_GroupPick_StableUnderUpstreamMarks pins the structural
// invariant: upstream-layer marks never change WHICH member is picked.
func TestCacheLayer_GroupPick_StableUnderUpstreamMarks(t *testing.T) {
	h := newGroupHarness(t)
	defer h.close()
	addSepChatModel(t, h, "pick-me", true)
	addSepChatModel(t, h, "backup-b", true)
	if err := h.mgr.AddModel(models.ModelConfig{
		ID: "iso-group", Name: "iso-group", Enabled: true,
		Kind: models.KindGroup, Internal: true,
		Members: []models.GroupMember{
			{ModelID: "pick-me", Weight: 10},
			{ModelID: "backup-b", Weight: 1, BackupOnly: true},
		},
	}); err != nil {
		t.Fatalf("AddModel: %v", err)
	}

	up := skipcache.New()
	// A storm of upstream marks on the DEFAULT member's identity.
	up.Mark(skipcache.ScopeCredential, p5FixtureCredID, "pick-me", time.Hour)
	up.Mark(skipcache.ScopeProvider, "openai", "pick-me", time.Hour)

	for i := 0; i < 5; i++ {
		res := h.mgr.RewriteGroupEntries([]string{"iso-group"}, "")
		if res.FailedGroupID != "" {
			t.Fatalf("group failed to resolve")
		}
		if res.MemberID != "pick-me" {
			t.Fatalf("group pick flipped to %q under upstream marks — R1 violation (mark must never decide the pick)", res.MemberID)
		}
	}
}

// parsedImportPaths returns the unquoted import paths of a Go file
// (import block only, via go/parser ImportsOnly). Comments are ignored,
// so a comment that merely writes a fully-qualified path never matches.
func parsedImportPaths(path string) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(file.Imports))
	for _, spec := range file.Imports {
		if imp, uerr := strconv.Unquote(spec.Path.Value); uerr == nil {
			out = append(out, imp)
		}
	}
	return out, nil
}

// TestCacheLayer_Structural_PackageLevelNoCacheImports pins the
// isolation at PACKAGE level (D2): NO non-test file in
// pkg/store/database, pkg/grouplb or pkg/modelscache imports either
// cache package. This is stronger than a file-level grep on the group
// pick — a new file in those packages could otherwise add a
// `m.skipCache.Marked(...)` call with no import change, defeating the
// "impossible by construction" claim.
//
// The check PARSES the import block (go/parser, ImportsOnly) rather than
// grepping raw text: a comment that merely writes the fully-qualified
// path can no longer false-fail the pin, while a real import is still
// caught (see TestCacheLayer_Structural_ImportParserIsCommentAware).
func TestCacheLayer_Structural_PackageLevelNoCacheImports(t *testing.T) {
	forbidden := map[string]bool{
		"github.com/disillusioners/llm-supervisor-proxy/pkg/skipcache":  true,
		"github.com/disillusioners/llm-supervisor-proxy/pkg/modelcache": true,
	}
	for _, dir := range []string{"../store/database", "../grouplb", "../modelscache"} {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				return nil
			}
			name := d.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				return nil
			}
			imports, perr := parsedImportPaths(path)
			if perr != nil {
				return fmt.Errorf("parse %s: %w", path, perr)
			}
			for _, imp := range imports {
				if forbidden[imp] {
					t.Errorf("%s imports %q — a cross-layer cache read must not be compilable in the group/model-data packages", path, imp)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
}

// TestCacheLayer_Structural_ImportParserIsCommentAware proves the pin's
// two required properties: a quoted forbidden path inside a COMMENT is
// ignored, and a REAL import of the same path is detected.
func TestCacheLayer_Structural_ImportParserIsCommentAware(t *testing.T) {
	dir := t.TempDir()
	forbiddenPath := "github.com/disillusioners/llm-supervisor-proxy/pkg/skipcache"

	commentOnly := filepath.Join(dir, "comment_only.go")
	body := "package x\n\n// note: this package must not import \"" + forbiddenPath + "\" ever\nimport \"fmt\"\n"
	if err := os.WriteFile(commentOnly, []byte(body), 0o644); err != nil {
		t.Fatalf("write comment-only fixture: %v", err)
	}
	imps, err := parsedImportPaths(commentOnly)
	if err != nil {
		t.Fatalf("parse comment-only: %v", err)
	}
	for _, imp := range imps {
		if imp == forbiddenPath {
			t.Fatalf("comment text was treated as an import — the pin is comment-unaware")
		}
	}

	realImport := filepath.Join(dir, "real_import.go")
	body2 := "package x\nimport _ \"" + forbiddenPath + "\"\n"
	if err := os.WriteFile(realImport, []byte(body2), 0o644); err != nil {
		t.Fatalf("write real-import fixture: %v", err)
	}
	imps2, err := parsedImportPaths(realImport)
	if err != nil {
		t.Fatalf("parse real-import: %v", err)
	}
	found := false
	for _, imp := range imps2 {
		if imp == forbiddenPath {
			found = true
		}
	}
	if !found {
		t.Fatal("a real import of the forbidden path was NOT detected — the pin cannot catch a cross-layer read")
	}
}

// TestCacheLayer_WinClear_PerLayer pins the per-layer win-clear:
// a proven WIN clears the MODEL row for the winning model AND the
// UPSTREAM rows for the winning credential/provider.
func TestCacheLayer_WinClear_PerLayer(t *testing.T) {
	up := skipcache.New()
	mc := modelcache.New()
	up.Mark(skipcache.ScopeCredential, "cred-1", "wire-m", time.Minute)
	up.Mark(skipcache.ScopeProvider, "openai", "wire-m", time.Minute)
	mc.Mark("row-m", time.Minute)

	conf := &ConfigSnapshot{UpstreamSkipCache: up, ModelSkipCache: mc}
	h := &Handler{}
	h.clearWinnerLayerState(conf, nil, "cred-1", "openai", "wire-m", "row-m")

	if up.Marked(skipcache.ScopeCredential, "cred-1", "wire-m") {
		t.Fatal("upstream credential mark survived the win")
	}
	if up.Marked(skipcache.ScopeProvider, "openai", "wire-m") {
		t.Fatal("upstream provider mark survived the win")
	}
	if mc.Marked("row-m") {
		t.Fatal("model-layer mark survived the winner's win")
	}
}

// TestCacheLayer_Structural_ModelSuppressionKeyedByRow ensures the
// model-layer suppression path exists in both lanes (source pin).
func TestCacheLayer_Structural_ModelSuppressionKeyedByRow(t *testing.T) {
	for _, tc := range []struct{ file, needle string }{
		{"race_executor.go", "ModelSkipCache.Marked(req.modelID)"},
		{"handler_anthropic.go", "ModelSkipCache.Marked(currentModel)"},
	} {
		src, err := os.ReadFile(tc.file)
		if err != nil {
			t.Fatalf("read %s: %v", tc.file, err)
		}
		if !strings.Contains(string(src), tc.needle) {
			t.Fatalf("%s missing the model-layer suppression guard %q", tc.file, tc.needle)
		}
	}
}

// ─────────────────── D1: upstream-layer reader (credential reorder) ───────────────────

// newTwoCredCfg builds a ConfigSnapshot over a 2-credential internal
// model for the upstream-reorder unit tests.
func newTwoCredCfg(t *testing.T) (*ConfigSnapshot, *mockModelsConfig) {
	t.Helper()
	mock := &mockModelsConfig{}
	mock.AddCredential(models.CredentialConfig{ID: "cred-A", Provider: "openai", APIKey: "kA", BaseURL: "http://a"})
	mock.AddCredential(models.CredentialConfig{ID: "cred-B", Provider: "openai", APIKey: "kB", BaseURL: "http://b"})
	mock.AddModel(models.ModelConfig{
		ID: "m", Name: "m", Enabled: true, Internal: true, InternalModel: "m",
		Credentials: models.TestRefs("cred-A", "cred-B"),
	})
	cfg := newTestConfigSnapshot("m")
	cfg.ModelsConfig = mock
	cfg.UpstreamSkipCache = skipcache.New()
	return cfg, mock
}

// TestCacheLayer_D1_UpstreamMarkReordersCredential: a mark on the
// selected credential makes the attempt prefer an unmarked sibling; an
// all-marked model still attempts, preferring the soonest-expiring mark.
func TestCacheLayer_D1_UpstreamMarkReordersCredential(t *testing.T) {
	cfg, _ := newTwoCredCfg(t)
	cur := models.ResolvedCredential{CredentialID: "cred-A", Provider: "openai", InternalModel: "m"}

	// A marked (credential scope only) ⇒ reorder to B.
	cfg.UpstreamSkipCache.Mark(skipcache.ScopeCredential, "cred-A", "m", 10*time.Minute)
	got := preferUnmarkedUpstreamCredential(cfg, nil, "m", cur)
	if got.CredentialID != "cred-B" {
		t.Fatalf("selected credential = %q, want cred-B (unmarked sibling must be preferred while cred-A is marked)", got.CredentialID)
	}

	// All marked ⇒ still attempt; prefer the soonest-expiring mark.
	cfg2, _ := newTwoCredCfg(t)
	cfg2.UpstreamSkipCache.Mark(skipcache.ScopeCredential, "cred-A", "m", 10*time.Minute)
	cfg2.UpstreamSkipCache.Mark(skipcache.ScopeCredential, "cred-B", "m", time.Minute)
	got2 := preferUnmarkedUpstreamCredential(cfg2, nil, "m", models.ResolvedCredential{CredentialID: "cred-A", Provider: "openai", InternalModel: "m"})
	if got2.CredentialID != "cred-B" {
		t.Fatalf("all-marked selection = %q, want cred-B (soonest-expiring mark; the attempt is NOT excluded)", got2.CredentialID)
	}
	if got2.CredentialID == "" {
		t.Fatal("all-marked must still resolve an attempt")
	}
}

// TestCacheLayer_D7a_SuppressedAttemptWritesNoUpstreamMark: a
// MODEL-layer suppression makes no upstream call, so it must write no
// UPSTREAM mark (the marks come from observed failures only).
func TestCacheLayer_D7a_SuppressedAttemptWritesNoUpstreamMark(t *testing.T) {
	up := skipcache.New()
	mc := modelcache.New()
	provider := &sepStatusProvider{statuses: map[string]int{"m1": http.StatusOK, "m2": http.StatusOK}}
	coord, _ := newSepCoord(t,
		[]sepModel{{id: "m1", chain: []string{"m2"}}, {id: "m2"}},
		provider, false, false, "conv-nomark",
		func(cfg *ConfigSnapshot) {
			cfg.UpstreamSkipCache = up
			cfg.ModelSkipCache = mc
		})
	mc.Mark("m1", 10*time.Minute)

	coord.Start()
	if winner := coord.WaitForWinner(); winner == nil {
		t.Fatal("no winner (m2 should serve)")
	}
	if up.Marked(skipcache.ScopeCredential, "cred-m1-0", "m1") {
		t.Fatal("suppressed attempt wrote an upstream credential mark — suppression must not fabricate an upstream failure")
	}
	if up.Marked(skipcache.ScopeProvider, "openai", "m1") {
		t.Fatal("suppressed attempt wrote an upstream provider mark")
	}
}

// ─────────────────── D5: non-stream empty 200 does not win ───────────────────

// TestCacheLayer_D5_NonStreamEmpty200DoesNotWin: a non-stream 200 whose
// message has no usable content (no text, tool_calls or reasoning) must
// not win; the chain advances to a model that answers with content.
func TestCacheLayer_D5_NonStreamEmpty200DoesNotWin(t *testing.T) {
	provider := &sepStatusProvider{
		statuses: map[string]int{"m1": http.StatusOK, "m2": http.StatusOK},
		empty:    map[string]bool{"m1": true},
	}
	coord, _ := newSepCoord(t,
		[]sepModel{{id: "m1", chain: []string{"m2"}}, {id: "m2"}},
		provider, false /*non-stream*/, false, "conv-ns-empty",
		func(cfg *ConfigSnapshot) { cfg.MaxGenerationTime = 5 * time.Second })
	coord.Start()
	winner := coord.WaitForWinner()
	if winner == nil {
		t.Fatalf("no winner — non-stream empty 200 hung instead of advancing")
	}
	if winner.modelID != "m2" {
		t.Fatalf("winner = %s, want m2 (empty non-stream 200 must not win)", winner.modelID)
	}
}

// ─────────────────── D6: synthetic 429 does not mask co-failure status ───────────────────

// TestCacheLayer_D6_SuppressedHeadDoesNotMask500: suppressed head
// (fabricated 429) + a real chain 500 must surface 500, not 502 from a
// status-agreement mismatch.
func TestCacheLayer_D6_SuppressedHeadDoesNotMask500(t *testing.T) {
	provider := &sepStatusProvider{statuses: map[string]int{"m1": http.StatusOK, "m2": http.StatusInternalServerError}}
	mc := modelcache.New()
	coord, _ := newSepCoord(t,
		[]sepModel{{id: "m1", chain: []string{"m2"}}, {id: "m2"}},
		provider, false, false, "conv-mask500",
		func(cfg *ConfigSnapshot) {
			cfg.ModelSkipCache = mc
		})
	mc.Mark("m1", 10*time.Minute)
	coord.Start()
	if winner := coord.WaitForWinner(); winner != nil {
		t.Fatalf("expected no winner, got %s", winner.modelID)
	}
	if info := coord.GetFinalErrorInfo(); info.HTTPStatus != http.StatusInternalServerError {
		t.Fatalf("final status = %d, want 500 (the synthetic suppression 429 must not mask the real chain failure)", info.HTTPStatus)
	}
}

// ─────────────── D1 wiring pin: the attempt path consults the reader ───────────────

// keyRecordingProvider answers every attempt with usable content; the
// API key each attempt used is captured by the newProviderClient closure
// (a credential's APIKey is the only per-credential observable there).
type keyRecordingProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *keyRecordingProvider) Name() string { return "key-rec" }

func (p *keyRecordingProvider) ChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (*providers.ChatCompletionResponse, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	return &providers.ChatCompletionResponse{
		Model:   req.Model,
		Choices: []providers.Choice{{Message: &providers.ChatMessage{Role: "assistant", Content: "ok"}, FinishReason: "stop"}},
	}, nil
}

func (p *keyRecordingProvider) StreamChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (<-chan providers.StreamEvent, error) {
	return nil, &providers.ProviderError{StatusCode: 500, Message: "not stream"}
}

func (p *keyRecordingProvider) IsRetryable(err error) bool { return true }

// TestCacheLayer_D1_Wiring_AttemptPrefersUnmarkedCredential pins that
// the OpenAI attempt path actually CALLS the upstream reorder reader
// (race_executor.go:520): the engine binding points at cred-A, cred-A is
// upstream-marked, and the attempt must reach the upstream on cred-B's
// key.
func TestCacheLayer_D1_Wiring_AttemptPrefersUnmarkedCredential(t *testing.T) {
	mock := &mockModelsConfig{}
	mock.AddCredential(models.CredentialConfig{ID: "cred-A", Provider: "openai", APIKey: "api-key-A", BaseURL: "http://a"})
	mock.AddCredential(models.CredentialConfig{ID: "cred-B", Provider: "openai", APIKey: "api-key-B", BaseURL: "http://b"})
	mock.AddModel(models.ModelConfig{
		ID: "m1", Name: "m1", Enabled: true, Internal: true, InternalModel: "m1",
		Credentials: models.TestRefs("cred-A", "cred-B"),
	})
	engine := credentiallb.NewEngine(time.Hour, time.Hour, 11, 60*time.Second)
	t.Cleanup(engine.Stop)
	engine.RebindFromStore("m1", models.TestRefs("cred-A", "cred-B"))
	// Force the engine to bind cred-A: cool cred-B so the healthy pick is A.
	engine.InjectAllCoolingForTest("m1", "cred-A")
	resolver := &engineBackedResolver{mockModelsConfig: mock, engine: engine}

	provider := &keyRecordingProvider{}
	var seenKeys []string
	orig := newProviderClient
	newProviderClient = func(providerType, apiKey, baseURL string) (providers.Provider, error) {
		seenKeys = append(seenKeys, apiKey)
		return provider, nil
	}
	t.Cleanup(func() { newProviderClient = orig })

	up := skipcache.New()
	up.Mark(skipcache.ScopeCredential, "cred-A", "m1", 10*time.Minute)

	cfg := newTestConfigSnapshot("m1")
	cfg.ModelsConfig = resolver
	cfg.UpstreamSkipCache = up
	cfg.MaxGenerationTime = 5 * time.Second
	cfg.StreamDeadline = 5 * time.Second
	raw := []byte(`{"model":"m1","messages":[{"role":"user","content":"hi"}],"stream":false}`)
	coord := newRaceCoordinatorWithEvents(context.Background(), cfg, newTestRequest(), raw, []string{"m1"}, events.NewBus(), "d1-wire", false, engine, "conv-d1-wire", false, nil)

	coord.Start()
	if w := coord.WaitForWinner(); w == nil {
		t.Fatal("no winner on the D1 wiring request")
	}
	if len(seenKeys) == 0 {
		t.Fatal("provider client was never constructed — no attempt ran")
	}
	if seenKeys[0] != "api-key-B" {
		t.Fatalf("attempt used api key %q, want api-key-B — the attempt path did NOT consult the upstream reorder reader", seenKeys[0])
	}
}

// ─────────────── D5 pin: tool-calls-only non-stream response WINS ───────────────

// TestCacheLayer_D5_ToolCallsOnlyWins: a non-stream 200 with ONLY
// tool_calls (no text) is a legitimate usable win — it must NOT be
// treated as "empty" and advance. Pins responseHasUsableContent against
// a future "require text" regression that would break agentic traffic.
func TestCacheLayer_D5_ToolCallsOnlyWins(t *testing.T) {
	provider := &sepStatusProvider{toolOnly: map[string]bool{"m1": true}}
	coord, _ := newSepCoord(t,
		[]sepModel{{id: "m1", chain: []string{"m2"}}, {id: "m2"}},
		provider, false, false, "conv-tool-only",
		func(cfg *ConfigSnapshot) { cfg.MaxGenerationTime = 5 * time.Second })
	coord.Start()
	winner := coord.WaitForWinner()
	if winner == nil {
		t.Fatal("no winner — a tool-calls-only non-stream response must win")
	}
	if winner.modelID != "m1" {
		t.Fatalf("winner = %s, want m1 (tool-calls-only is usable content; must not advance)", winner.modelID)
	}
}

// ─────────────── D6 pin: all-suppressed ⇒ 429 ───────────────

// TestCacheLayer_D6_AllSuppressedReturns429: when EVERY attempt is
// model-suppressed (no real upstream failure), the final status is the
// suppression semantic 429 — never 0/502.
func TestCacheLayer_D6_AllSuppressedReturns429(t *testing.T) {
	provider := &sepStatusProvider{}
	mc := modelcache.New()
	coord, _ := newSepCoord(t,
		[]sepModel{{id: "m1"}},
		provider, false, false, "conv-all-suppressed",
		func(cfg *ConfigSnapshot) {
			cfg.ModelSkipCache = mc
			cfg.MaxGenerationTime = 5 * time.Second
		})
	mc.Mark("m1", 10*time.Minute)
	coord.Start()
	if w := coord.WaitForWinner(); w != nil {
		t.Fatalf("expected no winner, got %s", w.modelID)
	}
	if got := provider.callCount(); got != 0 {
		t.Fatalf("upstream calls = %d, want 0 (every attempt suppressed)", got)
	}
	if info := coord.GetFinalErrorInfo(); info.HTTPStatus != http.StatusTooManyRequests {
		t.Fatalf("final status = %d, want 429 (all-suppressed)", info.HTTPStatus)
	}
}

// ─────────── fix round #1 — review items #1/#2/#3 ───────────

// anthEmptyStub serves per-model OpenAI JSON bodies (the internal
// Anthropic lane talks OpenAI to the upstream).
type anthEmptyStub struct {
	mu     sync.Mutex
	models []string
	bodies map[string]string
}

func (s *anthEmptyStub) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		model, _ := body["model"].(string)
		s.mu.Lock()
		s.models = append(s.models, model)
		b := s.bodies[model]
		s.mu.Unlock()
		if b == "" {
			b = stubUpstreamBody(model, "hi")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(b))
	}
}

func (s *anthEmptyStub) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.models))
	copy(out, s.models)
	return out
}

// TestCacheLayer_R1_AnthropicEmpty200ContinuesWalk (#1): F3 parity — a
// content-less 200 in the Anthropic walk must NOT end the walk; the
// chain target serves.
func TestCacheLayer_R1_AnthropicEmpty200ContinuesWalk(t *testing.T) {
	h := newGroupHarness(t)
	defer h.close()

	if err := h.mgr.AddModel(models.ModelConfig{
		ID: "a1", Name: "a1", Enabled: true, Kind: models.KindChat,
		Internal: true, InternalModel: "a1",
		Credentials:   models.TestRefs(p5FixtureCredID),
		FallbackChain: []string{"a2"},
	}); err != nil {
		t.Fatalf("AddModel(a1): %v", err)
	}
	if err := h.mgr.AddModel(models.ModelConfig{
		ID: "a2", Name: "a2", Enabled: true, Kind: models.KindChat,
		Internal: true, InternalModel: "a2",
		Credentials: models.TestRefs(p5FixtureCredID),
	}); err != nil {
		t.Fatalf("AddModel(a2): %v", err)
	}

	st := &anthEmptyStub{bodies: map[string]string{
		"a1": `{"id":"cc","object":"chat.completion","model":"a1","choices":[{"index":0,"message":{"role":"assistant","content":""},"finish_reason":"stop"}]}`,
	}}
	h.up.Close()
	h.up = httptest.NewServer(st.handler())
	h.rebuildHandler(t) // re-points the fixture credential at the live stub

	rec := httptest.NewRecorder()
	req := makeAnthropicRequestNoAuth(t, map[string]interface{}{
		"model":      "a1",
		"max_tokens": 16,
		"messages":   []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
	})
	h.h.HandleAnthropicMessages(rec, req)

	attempted := st.snapshot()
	sawA2 := false
	for _, m := range attempted {
		if m == "a2" {
			sawA2 = true
		}
	}
	if !sawA2 {
		t.Fatalf("content-less 200 on a1 ended the walk (attempted=%v) — the chain target a2 was never attempted", attempted)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("request not served (status=%d)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "hi") {
		t.Fatalf("client did not receive a2's content (body=%q)", rec.Body.String())
	}
}

// TestCacheLayer_R2_ReorderRespectsCredentialCooldown (#2): a quota-
// COOLING credential is skipped in the reorder preference (a healthy
// sibling wins), and when every candidate is marked-or-cooling the
// attempt still fires on the soonest-recovering one.
func TestCacheLayer_R2_ReorderRespectsCredentialCooldown(t *testing.T) {
	mock := &mockModelsConfig{}
	for _, id := range []string{"cred-A", "cred-B", "cred-C"} {
		mock.AddCredential(models.CredentialConfig{ID: id, Provider: "openai", APIKey: "k-" + id, BaseURL: "http://" + id})
	}
	mock.AddModel(models.ModelConfig{
		ID: "m", Name: "m", Enabled: true, Internal: true, InternalModel: "m",
		Credentials: models.TestRefs("cred-A", "cred-B", "cred-C"),
	})
	engine := credentiallb.NewEngine(time.Hour, time.Hour, 5, 60*time.Second)
	t.Cleanup(engine.Stop)
	engine.RebindFromStore("m", models.TestRefs("cred-A", "cred-B", "cred-C"))
	// cred-B is quota-COOLING but UNMARKED in the skip cache.
	engine.InjectAllCoolingForTest("m", "cred-A", "cred-C")

	cfg := newTestConfigSnapshot("m")
	cfg.ModelsConfig = mock
	cfg.UpstreamSkipCache = skipcache.New()
	cfg.UpstreamSkipCache.Mark(skipcache.ScopeCredential, "cred-A", "m", 10*time.Minute)
	cur := models.ResolvedCredential{CredentialID: "cred-A", Provider: "openai", InternalModel: "m"}

	got := preferUnmarkedUpstreamCredential(cfg, engine, "m", cur)
	if got.CredentialID != "cred-C" {
		t.Fatalf("reorder preferred %q — a COOLING credential (cred-B) must be skipped in favour of the healthy cred-C", got.CredentialID)
	}

	// All candidates marked-or-cooling: still attempts, preferring the
	// soonest-recovering (cred-B/cred-C's ~60s cooldown beats cred-A's
	// 10m mark). A second engine cools BOTH siblings so no candidate is
	// clean.
	engine2 := credentiallb.NewEngine(time.Hour, time.Hour, 6, 60*time.Second)
	t.Cleanup(engine2.Stop)
	engine2.RebindFromStore("m", models.TestRefs("cred-A", "cred-B", "cred-C"))
	engine2.InjectAllCoolingForTest("m", "cred-A") // cools cred-B AND cred-C
	cfg2 := newTestConfigSnapshot("m")
	cfg2.ModelsConfig = mock
	cfg2.UpstreamSkipCache = skipcache.New()
	cfg2.UpstreamSkipCache.Mark(skipcache.ScopeCredential, "cred-A", "m", 10*time.Minute)
	got2 := preferUnmarkedUpstreamCredential(cfg2, engine2, "m", cur)
	if got2.CredentialID != "cred-B" && got2.CredentialID != "cred-C" {
		t.Fatalf("all-cooling selection = %q, want cred-B or cred-C (a cooling candidate is still attempted — never excluded)", got2.CredentialID)
	}
	if got2.CredentialID == "" {
		t.Fatal("all-cooling must still resolve an attempt")
	}
}

// TestCacheLayer_R3_FinishReasonWins (#3): a `content_filter` (or
// `length`) completion with EMPTY content is the upstream's terminal
// answer — it must WIN, not trade the client a 502.
func TestCacheLayer_R3_FinishReasonWins(t *testing.T) {
	for _, finish := range []string{"content_filter", "length"} {
		fin := finish
		t.Run(fin, func(t *testing.T) {
			provider := &sepStatusProvider{finish: map[string]string{"m1": fin}}
			coord, _ := newSepCoord(t,
				[]sepModel{{id: "m1", chain: []string{"m2"}}, {id: "m2"}},
				provider, false, false, "conv-finish-"+fin,
				func(cfg *ConfigSnapshot) { cfg.MaxGenerationTime = 5 * time.Second })
			coord.Start()
			winner := coord.WaitForWinner()
			if winner == nil {
				t.Fatal("no winner — the filtered/length completion must win")
			}
			if winner.modelID != "m1" {
				t.Fatalf("winner = %s, want m1 (a %s completion with empty content is the answer to forward)", winner.modelID, fin)
			}
		})
	}
}

// ─────────── fix round #2 — D-A double-write, D-B terminal finish ───────────

// anthSSEStub serves per-model SSE (stream) or JSON (non-stream) bodies
// and records the attempted wire models.
type anthSSEStub struct {
	mu     sync.Mutex
	models []string
	sse    map[string]string
	json   map[string]string
	status map[string]int
}

func (s *anthSSEStub) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		model, _ := body["model"].(string)
		stream, _ := body["stream"].(bool)
		s.mu.Lock()
		s.models = append(s.models, model)
		st := s.status[model]
		var sseB, jsonB string
		if stream {
			sseB = s.sse[model]
		} else {
			jsonB = s.json[model]
		}
		s.mu.Unlock()
		if st == 0 {
			st = http.StatusOK
		}
		if st >= 400 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(st)
			_, _ = w.Write([]byte(`{"error":{"type":"server_error","message":"stub failure"}}`))
			return
		}
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(sseB))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(jsonB))
	}
}

func (s *anthSSEStub) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.models))
	copy(out, s.models)
	return out
}

// TestCacheLayer_DA_LiveEmptyStreamSingleWrite: an empty LIVE stream on
// the head (finish-only) must NOT be followed by a second Anthropic
// stream from the chain target — the finalize frames commit the client
// to the first stream and the completion stays served.
func TestCacheLayer_DA_LiveEmptyStreamSingleWrite(t *testing.T) {
	h := newGroupHarness(t)
	defer h.close()
	if err := h.mgr.AddModel(models.ModelConfig{
		ID: "a1", Name: "a1", Enabled: true, Kind: models.KindChat,
		Internal: true, InternalModel: "a1",
		Credentials:   models.TestRefs(p5FixtureCredID),
		FallbackChain: []string{"a2"},
	}); err != nil {
		t.Fatalf("AddModel(a1): %v", err)
	}
	if err := h.mgr.AddModel(models.ModelConfig{
		ID: "a2", Name: "a2", Enabled: true, Kind: models.KindChat,
		Internal: true, InternalModel: "a2",
		Credentials: models.TestRefs(p5FixtureCredID),
	}); err != nil {
		t.Fatalf("AddModel(a2): %v", err)
	}

	st := &anthSSEStub{
		sse: map[string]string{
			// a1: no content chunks — only a finish_reason chunk + DONE.
			"a1": "data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
				"data: [DONE]\n\n",
			// a2: a real content stream (must never be reached).
			"a2": "data: {\"id\":\"y\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hi\"}}]}\n\n" +
				"data: {\"id\":\"y\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
				"data: [DONE]\n\n",
		},
	}
	h.up.Close()
	h.up = httptest.NewServer(st.handler())
	h.rebuildHandler(t)

	rec := httptest.NewRecorder()
	req := makeAnthropicRequestNoAuth(t, map[string]interface{}{
		"model":      "a1",
		"max_tokens": 16,
		"stream":     true,
		"messages":   []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
	})
	h.h.HandleAnthropicMessages(rec, req)

	body := rec.Body.String()
	starts := strings.Count(body, "event: message_start")
	stops := strings.Count(body, "event: message_stop")
	if stops != 1 || starts > 1 {
		t.Fatalf("client saw %d message_start / %d message_stop events, want ≤1/1 (a single Anthropic stream) — double write; attempted=%v body=%q", starts, stops, st.snapshot(), body)
	}
	for _, m := range st.snapshot() {
		if m == "a2" {
			t.Fatalf("chain target a2 attempted after the committed empty stream (%v) — the switch window must be closed", st.snapshot())
		}
	}
}

// TestCacheLayer_DB_AnthropicContentFilterForwarded: a content_filter
// 200 with EMPTY content on the Anthropic internal non-stream lane is
// the upstream's terminal answer — it must be FORWARDED (win), not
// failed onto the chain.
func TestCacheLayer_DB_AnthropicContentFilterForwarded(t *testing.T) {
	h := newGroupHarness(t)
	defer h.close()
	if err := h.mgr.AddModel(models.ModelConfig{
		ID: "a1", Name: "a1", Enabled: true, Kind: models.KindChat,
		Internal: true, InternalModel: "a1",
		Credentials:   models.TestRefs(p5FixtureCredID),
		FallbackChain: []string{"a2"},
	}); err != nil {
		t.Fatalf("AddModel(a1): %v", err)
	}
	if err := h.mgr.AddModel(models.ModelConfig{
		ID: "a2", Name: "a2", Enabled: true, Kind: models.KindChat,
		Internal: true, InternalModel: "a2",
		Credentials: models.TestRefs(p5FixtureCredID),
	}); err != nil {
		t.Fatalf("AddModel(a2): %v", err)
	}

	st := &anthSSEStub{
		json: map[string]string{
			"a1": `{"id":"cc","object":"chat.completion","model":"a1","choices":[{"index":0,"message":{"role":"assistant","content":""},"finish_reason":"content_filter"}]}`,
		},
		status: map[string]int{"a2": http.StatusInternalServerError},
	}
	h.up.Close()
	h.up = httptest.NewServer(st.handler())
	h.rebuildHandler(t)

	rec := httptest.NewRecorder()
	req := makeAnthropicRequestNoAuth(t, map[string]interface{}{
		"model":      "a1",
		"max_tokens": 16,
		"messages":   []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
	})
	req.Header.Set("X-LLMProxy-Buffer-Response", "true") // buffered → the internal non-stream translation site
	h.h.HandleAnthropicMessages(rec, req)

	for _, m := range st.snapshot() {
		if m == "a2" {
			t.Fatalf("chain target a2 attempted (%v) — the content_filter completion must be the win", st.snapshot())
		}
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("content_filter completion not forwarded (status=%d body=%q)", rec.Code, rec.Body.String())
	}
}
