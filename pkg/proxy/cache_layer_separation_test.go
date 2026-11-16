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
	"net/http"
	"net/http/httptest"
	"os"
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
}

func (p *sepStatusProvider) Name() string { return "sep-status" }

func (p *sepStatusProvider) ChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (*providers.ChatCompletionResponse, error) {
	p.mu.Lock()
	p.calls = append(p.calls, req.Model)
	status := p.statuses[req.Model]
	p.mu.Unlock()
	if status == 0 {
		status = http.StatusOK
	}
	if status >= 400 {
		return nil, &providers.ProviderError{Provider: "sep-status", StatusCode: status, Message: "stub failure"}
	}
	return &providers.ChatCompletionResponse{
		ID: "cc", Object: "chat.completion", Created: time.Now().Unix(), Model: req.Model,
		Choices: []providers.Choice{{Index: 0, Message: &providers.ChatMessage{Role: "assistant", Content: "ok"}, FinishReason: "stop"}},
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
	hasSecond := false
	for _, row := range spawnOrderSnapshot(coord) {
		if strings.HasPrefix(row, "second:") {
			hasSecond = true
		}
	}
	if !hasSecond {
		t.Fatalf("no second (hedge) row spawned for a single-model list; spawns=%v", spawnOrderSnapshot(coord))
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
	h.mgr.SetSkipCache(up)
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

// TestCacheLayer_Structural_NoCacheImportsInGroupPick pins the
// compile-time isolation: the group-pick source imports neither cache
// package, so cross-layer reads are impossible by construction.
func TestCacheLayer_Structural_NoCacheImportsInGroupPick(t *testing.T) {
	src, err := os.ReadFile("../store/database/store_group_rewrite.go")
	if err != nil {
		t.Fatalf("read group-rewrite source: %v", err)
	}
	body := string(src)
	for _, forbidden := range []string{
		`"github.com/disillusioners/llm-supervisor-proxy/pkg/skipcache"`,
		`"github.com/disillusioners/llm-supervisor-proxy/pkg/modelcache"`,
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("store_group_rewrite.go imports %q — the group pick must not be able to read another layer's cache", forbidden)
		}
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
	h.clearWinnerUpstreamSkip(conf, nil, "cred-1", "openai", "wire-m", "row-m")

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
