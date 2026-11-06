package proxy

// Sticky last-success race ordering — wiring tests (design §9).
//
// Harness notes:
//   - A REAL *database.ModelsManager on an in-process SQLite store (the
//     same shape as groupHarness) so the group rewrite / re-entry paths
//     run through production code, not stubs.
//   - The config manager is a database.MockConfigManager, so the sticky
//     flag can be set per test WITHOUT touching the on-disk
//     ~/.config/llm-supervisor-proxy/config.json (the documented HOME
//     hazard).
//   - The sticky engine is injected through NewHandler's 8th positional
//     arg; nil means "feature absent" and exercises the nil-safe seam.
//   - Observability without new event types: the OpenAI lane asserts the
//     existing race_started payload's "models" order; the Anthropic lane
//     asserts the upstream-attempt order (it has no race coordinator).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/credentiallb"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/stickyorder"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
)

// ─── harness ────────────────────────────────────────────────────────────────

type stickyHarness struct {
	mgr     *database.ModelsManager
	h       *Handler
	up      *httptest.Server
	bus     *events.Bus
	sticky  *stickyorder.Engine
	enabled bool
	store   *store.RequestStore // keeps the handler's store reachable for RouteModel assertions
	clean   func()

	// tokenStore / cfgMutate are optional seams for the ultimate-path
	// fixture (auth is required for X-Force-Ultimate-Model entitlement,
	// and the global ultimate model id comes from config).
	tokenStore auth.TokenStoreInterface
	cfgMutate  func(*config.Config)
}

// newStickyHarness builds the real manager + handler. stickyEnabled sets
// the config flag; withEngine=false passes a nil engine (the nil-seam
// variant).
func newStickyHarness(t *testing.T, stickyEnabled, withEngine bool) *stickyHarness {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("DATABASE_URL", "sqlite:"+filepath.Join(tmp, "sticky.db"))
	st, err := database.Initialize(context.Background())
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	bus := events.NewBus()
	_, mgr, err := database.InitializeManagers(st, bus)
	if err != nil {
		st.Close()
		t.Fatalf("InitializeManagers: %v", err)
	}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(stubUpstreamBody("stub-model", "hi")))
	}))
	if err := mgr.AddCredential(models.CredentialConfig{
		ID: p5FixtureCredID, Provider: "openai", APIKey: "test-key", BaseURL: up.URL,
	}); err != nil {
		t.Fatalf("AddCredential: %v", err)
	}

	h := &stickyHarness{mgr: mgr, up: up, bus: bus, enabled: stickyEnabled}
	h.store = store.NewRequestStore(100, store.WithMaxBytes(256<<20))
	if withEngine {
		h.sticky = stickyorder.NewEngine(time.Minute, time.Hour)
	}
	h.rebuild(t)

	h.clean = func() {
		if h.sticky != nil {
			h.sticky.Stop()
		}
		up.Close()
		mgr.Close()
		st.Close()
		_ = os.RemoveAll(tmp)
	}
	t.Cleanup(h.clean)
	return h
}

// rebuild (re)constructs the handler from the current upstream + config
// flag + engine. Uses a MockConfigManager: no disk writes, so the
// documented ~/.config hazard never fires.
func (h *stickyHarness) rebuild(t *testing.T) {
	t.Helper()
	cfg := config.Defaults
	if h.cfgMutate != nil {
		h.cfgMutate(&cfg)
	}
	cfg.StickyLastSuccessEnabled = h.enabled
	mgrCfg := database.NewMockConfigManagerWithConfig(cfg)
	handler := NewHandler(&Config{ConfigMgr: mgrCfg, ModelsConfig: h.mgr}, h.bus, h.store, nil, h.tokenStore, nil, h.mgr.Engine(), h.sticky)
	h.h = handler
}

// lastRequestLog returns the newest request log in the harness store.
func (h *stickyHarness) lastRequestLog(t *testing.T) *store.RequestLog {
	t.Helper()
	list := h.store.List()
	if len(list) == 0 {
		t.Fatal("no request log recorded")
	}
	return list[0]
}

// swapUpstream replaces the stub upstream and re-points the fixture
// credential at it (models must be seeded AFTER this call).
func (h *stickyHarness) swapUpstream(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	h.up.Close()
	h.up = httptest.NewServer(handler)
	if err := h.mgr.UpdateCredential(p5FixtureCredID, models.CredentialConfig{
		ID: p5FixtureCredID, Provider: "openai", APIKey: "test-key", BaseURL: h.up.URL,
	}); err != nil {
		t.Fatalf("UpdateCredential: %v", err)
	}
	h.rebuild(t)
}

// seedChatModel adds an internal chat model pointed at the fixture
// credential, optionally with a fallback chain.
func (h *stickyHarness) seedChatModel(t *testing.T, id string, fallback ...string) {
	t.Helper()
	if err := h.mgr.AddModel(models.ModelConfig{
		ID: id, Name: id, Enabled: true, Kind: models.KindChat,
		Internal: true, InternalModel: upstreamModelName(id),
		Credentials:   models.TestRefs(p5FixtureCredID),
		FallbackChain: fallback,
	}); err != nil {
		t.Fatalf("AddModel(%s): %v", id, err)
	}
}

// upstreamModelName is the provider-side model string a seeded row sends
// on the wire ("up-<row id>"); upstreamBodyModel strips the prefix so a
// recorded attempt maps back to the MODEL ROW id.
func upstreamModelName(rowID string) string { return "up-" + rowID }

func rowIDFromUpstream(wire string) string {
	return strings.TrimPrefix(wire, "up-")
}

// seedGroup adds an internal group row with the given default members.
func (h *stickyHarness) seedGroup(t *testing.T, id string, members ...models.GroupMember) {
	t.Helper()
	if err := h.mgr.AddModel(models.ModelConfig{
		ID: id, Name: id, Enabled: true, Kind: models.KindGroup,
		Internal: true, ExcludeFromUltimateSwitching: true,
		Members: members,
	}); err != nil {
		t.Fatalf("AddModel(%s): %v", id, err)
	}
}

// stickyKeyFor recomputes the exact key the handler derives: the
// existing affinity key, salted by the TOP-LEVEL requested id (the group
// id for a group request), tokenID "" (no auth header in these tests).
func stickyKeyFor(topLevelModelID string, messages []interface{}) string {
	return credentiallb.ComputeConversationKey(
		topLevelModelID, "", credentiallb.ExtractLeadingUserRun(messages))
}

func stickyMessages() []interface{} {
	return []interface{}{map[string]interface{}{"role": "user", "content": "hi"}}
}

// collectStickyEvents drains the bus into a buffered channel.
func collectStickyEvents(t *testing.T, bus *events.Bus) chan events.Event {
	t.Helper()
	ch, err := bus.Subscribe()
	if err != nil {
		t.Fatalf("bus.Subscribe: %v", err)
	}
	t.Cleanup(func() { bus.Unsubscribe(ch) })
	out := make(chan events.Event, 128)
	go func() {
		for evt := range ch {
			select {
			case out <- evt:
			default:
			}
		}
	}()
	return out
}

// waitRaceStartedModels waits for the n-th (1-based) race_started event
// and returns its "models" payload as a []string.
func waitRaceStartedModels(t *testing.T, ch chan events.Event, n int, timeout time.Duration) []string {
	t.Helper()
	deadline := time.After(timeout)
	seen := 0
	for {
		select {
		case evt := <-ch:
			if evt.Type != "race_started" {
				continue
			}
			seen++
			if seen < n {
				continue
			}
			data, ok := evt.Data.(map[string]interface{})
			if !ok {
				t.Fatalf("race_started payload is %T", evt.Data)
			}
			raw, ok := data["models"].([]string)
			if !ok {
				t.Fatalf("race_started models payload is %T", data["models"])
			}
			return raw
		case <-deadline:
			t.Fatalf("timed out waiting for race_started #%d", n)
		}
	}
}

// modelRecorder records the "model" field of every upstream request body.
type modelRecorder struct {
	mu     sync.Mutex
	models []string
}

func (r *modelRecorder) add(m string) {
	r.mu.Lock()
	r.models = append(r.models, m)
	r.mu.Unlock()
}

func (r *modelRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.models...)
}

// upstreamBodyModel extracts the top-level "model" string from a request
// body.
func upstreamBodyModel(r *http.Request) string {
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(r.Body)
	var body map[string]interface{}
	_ = json.Unmarshal(buf.Bytes(), &body)
	m, _ := body["model"].(string)
	return m
}

func makeStickyChatRequest(t *testing.T) *http.Request {
	t.Helper()
	return makeChatRequest(t, map[string]interface{}{
		"model":    "m-a",
		"messages": stickyMessages(),
	})
}

func makeStickyAnthropicRequest(t *testing.T, modelID string, messages []interface{}) *http.Request {
	t.Helper()
	return makeAnthropicRequestNoAuth(t, map[string]interface{}{
		"model":      modelID,
		"max_tokens": 16,
		"messages":   messages,
	})
}

// ─── gate / nil-engine negatives ────────────────────────────────────────────

// TestStickyOrder_GateOff_NoReorderAndNoRecord pins §8.2: flag off ⇒ the
// raced order is unchanged AND nothing is recorded (the record site is
// flag-gated too).
func TestStickyOrder_GateOff_NoReorderAndNoRecord(t *testing.T) {
	h := newStickyHarness(t, false, true)
	h.seedChatModel(t, "m-a", "sticky-w")
	h.seedChatModel(t, "sticky-w")

	key := stickyKeyFor("m-a", stickyMessages())
	h.sticky.RecordSuccess(key, "sticky-w")

	evCh := collectStickyEvents(t, h.bus)
	rec := httptest.NewRecorder()
	h.h.HandleChatCompletions(rec, makeStickyChatRequest(t))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}

	got := waitRaceStartedModels(t, evCh, 1, 2*time.Second)
	if !equalStrings(got, []string{"m-a", "sticky-w"}) {
		t.Fatalf("gate off reordered the race: %v", got)
	}
	// Record site is flag-gated: the pre-seeded value survives untouched.
	if v, ok := h.sticky.Get(key); !ok || v != "sticky-w" {
		t.Fatalf("gate off recorded a winner: got (%q,%v), want (sticky-w,true)", v, ok)
	}
}

// TestStickyOrder_NilEngine_NoReorder pins §8.3: flag TRUE + nil engine ⇒
// identity at every site.
func TestStickyOrder_NilEngine_NoReorder(t *testing.T) {
	h := newStickyHarness(t, true, false)
	h.seedChatModel(t, "m-a", "sticky-w")
	h.seedChatModel(t, "sticky-w")

	evCh := collectStickyEvents(t, h.bus)
	rec := httptest.NewRecorder()
	h.h.HandleChatCompletions(rec, makeStickyChatRequest(t))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	got := waitRaceStartedModels(t, evCh, 1, 2*time.Second)
	if !equalStrings(got, []string{"m-a", "sticky-w"}) {
		t.Fatalf("nil engine reordered the race: %v", got)
	}
}

// ─── site (a): OpenAI race promotion ────────────────────────────────────────

func TestStickyOrder_OpenAIRace_PromotesRecordedWinner(t *testing.T) {
	h := newStickyHarness(t, true, true)
	h.seedChatModel(t, "m-a", "sticky-w")
	h.seedChatModel(t, "sticky-w")

	key := stickyKeyFor("m-a", stickyMessages())
	h.sticky.RecordSuccess(key, "sticky-w")

	evCh := collectStickyEvents(t, h.bus)
	rec := httptest.NewRecorder()
	h.h.HandleChatCompletions(rec, makeStickyChatRequest(t))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	got := waitRaceStartedModels(t, evCh, 1, 2*time.Second)
	if !equalStrings(got, []string{"sticky-w", "m-a"}) {
		t.Fatalf("race order = %v, want [sticky-w m-a]", got)
	}
}

// TestStickyOrder_UnknownWinner_NoReorder pins §8.4: a recorded winner
// absent from the current list is a no-op.
func TestStickyOrder_UnknownWinner_NoReorder(t *testing.T) {
	h := newStickyHarness(t, true, true)
	h.seedChatModel(t, "m-a", "m-b")
	h.seedChatModel(t, "m-b")

	key := stickyKeyFor("m-a", stickyMessages())
	h.sticky.RecordSuccess(key, "gone-model")

	evCh := collectStickyEvents(t, h.bus)
	rec := httptest.NewRecorder()
	h.h.HandleChatCompletions(rec, makeStickyChatRequest(t))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	got := waitRaceStartedModels(t, evCh, 1, 2*time.Second)
	if !equalStrings(got, []string{"m-a", "m-b"}) {
		t.Fatalf("unknown winner reordered the race: %v", got)
	}
}

// ─── delivered-success recording ────────────────────────────────────────────

func TestStickyOrder_Record_NonStream(t *testing.T) {
	h := newStickyHarness(t, true, true)
	h.seedChatModel(t, "m-a")

	key := stickyKeyFor("m-a", stickyMessages())
	rec := httptest.NewRecorder()
	h.h.HandleChatCompletions(rec, makeStickyChatRequest(t))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if v, ok := h.sticky.Get(key); !ok || v != "m-a" {
		t.Fatalf("non-stream delivered success not recorded: (%q,%v)", v, ok)
	}
}

func TestStickyOrder_Record_Stream(t *testing.T) {
	h := newStickyHarness(t, true, true)
	h.swapUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: %s\n\n", makeOpenAITextChunk("hello"))
		fmt.Fprint(w, "data: [DONE]\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	})
	h.seedChatModel(t, "m-a")

	key := stickyKeyFor("m-a", stickyMessages())
	req := makeChatRequest(t, map[string]interface{}{
		"model":    "m-a",
		"stream":   true,
		"messages": stickyMessages(),
	})
	rec := httptest.NewRecorder()
	h.h.HandleChatCompletions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if v, ok := h.sticky.Get(key); !ok || v != "m-a" {
		t.Fatalf("stream delivered success not recorded: (%q,%v)", v, ok)
	}
}

// TestStickyOrder_FirstByteThenDie_NoRecord pins §4.2 exclusion: a winner
// picked on first byte whose upstream then dies records NOTHING (the
// buffer-error arm returns before the completed publish).
func TestStickyOrder_FirstByteThenDie_NoRecord(t *testing.T) {
	h := newStickyHarness(t, true, true)
	h.swapUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: %s\n\n", makeOpenAITextChunk("hello"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// Kill the connection without [DONE]: the buffer closes with an
		// error, so the delivered-success publish is never reached.
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close()
				return
			}
		}
	})
	h.seedChatModel(t, "m-a")

	key := stickyKeyFor("m-a", stickyMessages())
	req := makeChatRequest(t, map[string]interface{}{
		"model":    "m-a",
		"stream":   true,
		"messages": stickyMessages(),
	})
	rec := httptest.NewRecorder()
	h.h.HandleChatCompletions(rec, req)

	if v, ok := h.sticky.Get(key); ok {
		t.Fatalf("first-byte-then-die recorded a winner: %q", v)
	}
}

// TestStickyOrder_DeadlinePick_NoRecord pins §4.2's "never record on
// race_deadline_pick": buffered mode (no first-byte gate) with a stream
// that stalls past StreamDeadline and then completes — the deadline-picked
// winner is a best-partial artifact and must not be recorded.
func TestStickyOrder_DeadlinePick_NoRecord(t *testing.T) {
	h := newStickyHarness(t, true, true)
	h.swapUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: %s\n\n", makeOpenAITextChunk("partial"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(600 * time.Millisecond) // past StreamDeadline (200ms)
		fmt.Fprint(w, "data: [DONE]\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	})
	h.seedChatModel(t, "m-a")

	// Short stream deadline, generous hard deadline — and buffered mode
	// so the first-byte gate does not pre-empt the deadline pick.
	h.h.config.ConfigMgr = database.NewMockConfigManagerWithConfig(deadlineCfg(200*time.Millisecond, 10*time.Second))

	key := stickyKeyFor("m-a", stickyMessages())
	req := makeChatRequest(t, map[string]interface{}{
		"model":    "m-a",
		"stream":   true,
		"messages": stickyMessages(),
	})
	req.Header.Set("X-LLMProxy-Buffer-Response", "true")
	rec := httptest.NewRecorder()
	h.h.HandleChatCompletions(rec, req)

	if v, ok := h.sticky.Get(key); ok {
		t.Fatalf("deadline pick recorded a winner: %q", v)
	}
}

func deadlineCfg(streamDeadline, maxGen time.Duration) config.Config {
	cfg := config.Defaults
	cfg.StickyLastSuccessEnabled = true
	cfg.StreamDeadline = config.Duration(streamDeadline)
	cfg.MaxGenerationTime = config.Duration(maxGen)
	cfg.IdleTimeout = config.Duration(60 * time.Second)
	return cfg
}

// ─── invalidation ───────────────────────────────────────────────────────────

func TestStickyOrder_Invalidate_OnRecordedWinnerFailure(t *testing.T) {
	h := newStickyHarness(t, true, true)
	h.swapUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"type":"server_error","message":"boom"}}`))
	})
	h.seedChatModel(t, "m-a")

	key := stickyKeyFor("m-a", stickyMessages())
	h.sticky.RecordSuccess(key, "m-a")

	rec := httptest.NewRecorder()
	h.h.HandleChatCompletions(rec, makeStickyChatRequest(t))

	if v, ok := h.sticky.Get(key); ok {
		t.Fatalf("failed attempt of the recorded winner left the pin: %q", v)
	}
}

func TestStickyOrder_Invalidate_NonWinnerFailureKeepsPin(t *testing.T) {
	h := newStickyHarness(t, true, true)
	h.swapUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"type":"server_error","message":"boom"}}`))
	})
	h.seedChatModel(t, "m-a")

	key := stickyKeyFor("m-a", stickyMessages())
	h.sticky.RecordSuccess(key, "m-z") // not in the raced list

	rec := httptest.NewRecorder()
	h.h.HandleChatCompletions(rec, makeStickyChatRequest(t))

	if v, ok := h.sticky.Get(key); !ok || v != "m-z" {
		t.Fatalf("non-winner failure disturbed the pin: (%q,%v)", v, ok)
	}
}

// TestStickyOrder_Invalidate_AfterCredFailoverExhaustion pins R8: every
// failover attempt flows through the same failure funnel, so an exhausted
// rotation of the recorded winner still unpins it.
func TestStickyOrder_Invalidate_AfterCredFailoverExhaustion(t *testing.T) {
	h := newStickyHarness(t, true, true)
	var calls int32
	h.swapUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if strings.Contains(r.Header.Get("Authorization"), "key-A") {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"type":"rate_limit_error","message":"429"}}`))
			return
		}
		_ = n
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"type":"server_error","message":"500"}}`))
	})
	for _, c := range []struct{ id, key string }{{"cred-a", "key-A"}, {"cred-b", "key-B"}} {
		if err := h.mgr.AddCredential(models.CredentialConfig{ID: c.id, Provider: "openai", APIKey: c.key, BaseURL: h.up.URL}); err != nil {
			t.Fatalf("AddCredential(%s): %v", c.id, err)
		}
	}
	if err := h.mgr.AddModel(models.ModelConfig{
		ID: "m-c", Name: "m-c", Enabled: true, Kind: models.KindChat,
		Internal: true, InternalModel: "stub-model",
		Credentials: models.TestRefs("cred-a", "cred-b"),
	}); err != nil {
		t.Fatalf("AddModel(m-c): %v", err)
	}

	key := stickyKeyFor("m-c", stickyMessages())
	h.sticky.RecordSuccess(key, "m-c")

	rec := httptest.NewRecorder()
	h.h.HandleChatCompletions(rec, makeChatRequest(t, map[string]interface{}{
		"model":    "m-c",
		"messages": stickyMessages(),
	}))
	if rec.Code == http.StatusOK {
		t.Fatalf("expected failure, got 200")
	}
	if v, ok := h.sticky.Get(key); ok {
		t.Fatalf("cred-failover exhaustion left the pin: %q", v)
	}
}

// ─── cancelled loser (§8.12) ────────────────────────────────────────────────

// TestStickyOrder_CancelledLoser_Invalidated: a mid-attempt cancel of the
// recorded winner surfaces as context.Canceled through the race failure
// funnel and unpins the slot (§8.12 / R22) — the shape cancelAllExcept
// produces on a client-write failure.
func TestStickyOrder_CancelledLoser_Invalidated(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Stall until the coordinator cancels the attempt (or the test ends).
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release) // LIFO: release the handler BEFORE srv.Close blocks

	mc := models.NewModelsConfig()
	if err := mc.AddCredential(models.CredentialConfig{ID: "cred-x", Provider: "openai", APIKey: "k", BaseURL: srv.URL}); err != nil {
		t.Fatalf("AddCredential: %v", err)
	}
	if err := mc.AddModel(models.ModelConfig{
		ID: "m-slow", Name: "m-slow", Enabled: true, Internal: true, InternalModel: "stub",
		Credentials: models.TestRefs("cred-x"),
	}); err != nil {
		t.Fatalf("AddModel: %v", err)
	}
	cfg := newTestConfigSnapshot("m-slow")
	cfg.ModelsConfig = mc
	cfg.StickyLastSuccessEnabled = true
	cfg.RaceParallelOnIdle = false // no idle spawn: the main attempt stays alone
	cfg.StreamDeadline = 30 * time.Second
	cfg.MaxGenerationTime = 30 * time.Second

	eng := stickyorder.NewEngine(time.Minute, time.Hour)
	defer eng.Stop()
	convKey := "conv-cancelled-loser"
	eng.RecordSuccess(convKey, "m-slow")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"m-slow"}`))
	coord := newRaceCoordinatorWithEvents(ctx, cfg, req, []byte(`{"model":"m-slow"}`),
		[]string{"m-slow"}, nil, "cancelled-loser", false, nil, convKey, false, eng)
	coord.Start()

	// Let the attempt reach the upstream, then cancel it mid-flight.
	time.Sleep(100 * time.Millisecond)
	coord.cancelAll()

	deadline := time.After(5 * time.Second)
	for {
		if _, ok := eng.Get(convKey); !ok {
			return // invalidated, as designed
		}
		select {
		case <-deadline:
			t.Fatal("cancelled recorded winner was not invalidated")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// ─── site (b): Anthropic walk promotion ─────────────────────────────────────

func TestStickyOrder_AnthropicWalk_PromotesRecordedWinner(t *testing.T) {
	h := newStickyHarness(t, true, true)
	rec := &modelRecorder{}
	h.swapUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		rec.add(rowIDFromUpstream(upstreamBodyModel(r)))
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"type":"server_error","message":"boom"}}`))
	})
	h.seedChatModel(t, "m-a", "sticky-w")
	h.seedChatModel(t, "sticky-w")

	key := stickyKeyFor("m-a", stickyMessages())
	h.sticky.RecordSuccess(key, "sticky-w")

	recorder := httptest.NewRecorder()
	h.h.HandleAnthropicMessages(recorder, makeStickyAnthropicRequest(t, "m-a", stickyMessages()))

	models := rec.snapshot()
	if len(models) == 0 {
		t.Fatal("no upstream attempts recorded")
	}
	if models[0] != "sticky-w" {
		t.Fatalf("Anthropic walk did not promote the recorded winner: %v", models)
	}
}

// TestStickyOrder_AnthropicWalk_RecordsServedModel pins §4.4: servedModel
// is set BEFORE the attempt, so finalize records the serving model (a
// post-success set would record nothing).
func TestStickyOrder_AnthropicWalk_RecordsServedModel(t *testing.T) {
	h := newStickyHarness(t, true, true)
	h.seedChatModel(t, "m-a")

	key := stickyKeyFor("m-a", stickyMessages())
	recorder := httptest.NewRecorder()
	h.h.HandleAnthropicMessages(recorder, makeStickyAnthropicRequest(t, "m-a", stickyMessages()))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if v, ok := h.sticky.Get(key); !ok || v != "m-a" {
		t.Fatalf("servedModel not recorded (ordering trap): (%q,%v)", v, ok)
	}
}

// ─── sites (c)/(d): group re-entry ──────────────────────────────────────────

// seedReentryGroup builds a 2-member group whose FIRST member (the
// deterministic default-pool pick) has NO chain and always fails, and
// whose replacement member carries the sticky target in its OWN fallback
// chain — so the rebuilt list is [replacement, sticky-w] regardless of
// which member the reselect lands on.
// seedReentryGroup builds a 2-default-member group and BINDS it to m-a
// (no chain) for the returned message set: the group rewrite is
// weighted-random on a fresh conversation, so the test pins the sticky
// binding through the production picker (RewriteGroupEntries binds per
// conversation key) instead of hoping for a coin flip.
//
// m-b — the only remaining member after m-a is excluded — carries the
// sticky target in ITS OWN fallback chain, so the rebuilt list is
// [m-b, sticky-w]: iteration 1 never touches sticky-w (so the record
// survives to iteration 2) and the re-entry is where the reorder must
// land.
func seedReentryGroup(t *testing.T, h *stickyHarness) []interface{} {
	t.Helper()
	h.seedChatModel(t, "m-a")             // bound first pick, no chain
	h.seedChatModel(t, "m-b", "sticky-w") // reselect target + chain
	h.seedChatModel(t, "sticky-w")        // the recorded sticky winner
	h.seedGroup(t, "g-two",
		models.GroupMember{ModelID: "m-a", Weight: 1},
		models.GroupMember{ModelID: "m-b", Weight: 1},
	)
	return bindGroupTo(t, h, "g-two", "m-a")
}

// bindGroupTo probes message sets until the group's production picker
// binds this conversation to wantMember, then returns those messages.
func bindGroupTo(t *testing.T, h *stickyHarness, groupID, want string) []interface{} {
	t.Helper()
	for i := 0; i < 100; i++ {
		msgs := []interface{}{map[string]interface{}{"role": "user", "content": fmt.Sprintf("bind-%d", i)}}
		key := stickyKeyFor(groupID, msgs)
		if res := h.mgr.RewriteGroupEntries([]string{groupID}, key); res.MemberID == want {
			return msgs
		}
	}
	t.Fatalf("could not bind group %s to member %s", groupID, want)
	return nil
}

func TestStickyOrder_OpenAIReEntry_Reorder(t *testing.T) {
	h := newStickyHarness(t, true, true)
	h.swapUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"type":"server_error","message":"boom"}}`))
	})
	msgs := seedReentryGroup(t, h)

	key := stickyKeyFor("g-two", msgs)
	h.sticky.RecordSuccess(key, "sticky-w")

	evCh := collectStickyEvents(t, h.bus)
	rec := httptest.NewRecorder()
	h.h.HandleChatCompletions(rec, makeChatRequest(t, map[string]interface{}{
		"model":    "g-two",
		"messages": msgs,
	}))

	// Iteration 1 races [m-a] (no chain ⇒ sticky no-op, record survives),
	// then the group re-entry rebuilds [m-b, sticky-w]; the loop-top
	// re-application must reorder the REBUILT list.
	got := waitRaceStartedModels(t, evCh, 2, 5*time.Second)
	if !equalStrings(got, []string{"sticky-w", "m-b"}) {
		t.Fatalf("re-entry race order = %v, want [sticky-w m-b]", got)
	}
}

func TestStickyOrder_AnthropicReEntry_Reorder(t *testing.T) {
	h := newStickyHarness(t, true, true)
	rec := &modelRecorder{}
	h.swapUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		rec.add(rowIDFromUpstream(upstreamBodyModel(r)))
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"type":"server_error","message":"boom"}}`))
	})
	msgs := seedReentryGroup(t, h)

	key := stickyKeyFor("g-two", msgs)
	h.sticky.RecordSuccess(key, "sticky-w")

	recorder := httptest.NewRecorder()
	h.h.HandleAnthropicMessages(recorder, makeStickyAnthropicRequest(t, "g-two", msgs))

	// Walk: m-a fails → re-entry rebuilds [m-b, sticky-w] → mandatory
	// explicit site (d) reorders to [sticky-w, m-b].
	got := rec.snapshot()
	if !equalStrings(got, []string{"m-a", "sticky-w", "m-b"}) {
		t.Fatalf("re-entry walk order = %v, want [m-a sticky-w m-b]", got)
	}
}

// TestStickyOrder_BackupPoolReEntry_Reorder pins §8.8: the backup-pool
// reselect rebuild lands on the same choke point.
func TestStickyOrder_BackupPoolReEntry_Reorder(t *testing.T) {
	h := newStickyHarness(t, true, true)
	h.swapUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"type":"server_error","message":"boom"}}`))
	})
	h.seedChatModel(t, "m-a")             // only default member — fails
	h.seedChatModel(t, "m-b", "sticky-w") // backup-only member
	h.seedChatModel(t, "sticky-w")
	if err := h.mgr.AddModel(models.ModelConfig{
		ID: "g-pool", Name: "g-pool", Enabled: true, Kind: models.KindGroup,
		Internal: true, ExcludeFromUltimateSwitching: true,
		Members: []models.GroupMember{
			{ModelID: "m-a", Weight: 1},
			{ModelID: "m-b", Weight: 1, BackupOnly: true},
		},
	}); err != nil {
		t.Fatalf("AddModel(g-pool): %v", err)
	}

	key := stickyKeyFor("g-pool", stickyMessages())
	h.sticky.RecordSuccess(key, "sticky-w")

	evCh := collectStickyEvents(t, h.bus)
	rec := httptest.NewRecorder()
	h.h.HandleChatCompletions(rec, makeChatRequest(t, map[string]interface{}{
		"model":    "g-pool",
		"messages": stickyMessages(),
	}))

	got := waitRaceStartedModels(t, evCh, 2, 5*time.Second)
	if !equalStrings(got, []string{"sticky-w", "m-b"}) {
		t.Fatalf("backup-pool re-entry race order = %v, want [sticky-w m-b]", got)
	}
}

// ─── applyStickyOrder unit gates + logs ─────────────────────────────────────

func TestApplyStickyOrder_GatesAndLog(t *testing.T) {
	eng := stickyorder.NewEngine(time.Minute, time.Hour)
	defer eng.Stop()
	h := &Handler{stickyEngine: eng}
	on := &ConfigSnapshot{StickyLastSuccessEnabled: true}
	off := &ConfigSnapshot{StickyLastSuccessEnabled: false}
	list := []string{"a", "b", "c"}

	eng.RecordSuccess("key-1", "c")

	if got := h.applyStickyOrder(off, list, "key-1", "r"); !equalStrings(got, list) {
		t.Fatalf("gate off: %v", got)
	}
	if got := h.applyStickyOrder(on, list, "", "r"); !equalStrings(got, list) {
		t.Fatalf("empty key: %v", got)
	}
	if got := h.applyStickyOrder(nil, list, "key-1", "r"); !equalStrings(got, list) {
		t.Fatalf("nil conf: %v", got)
	}
	if got := (&Handler{}).applyStickyOrder(on, list, "key-1", "r"); !equalStrings(got, list) {
		t.Fatalf("nil engine: %v", got)
	}
	if got := h.applyStickyOrder(on, list, "unknown-key", "r"); !equalStrings(got, list) {
		t.Fatalf("no record: %v", got)
	}

	out := captureStickyLog(t, func() {
		if got := h.applyStickyOrder(on, list, "key-1", "req-log"); !equalStrings(got, []string{"c", "a", "b"}) {
			t.Errorf("promotion: %v", got)
		}
	})
	if !strings.Contains(out, "[STICKY] request req-log: promoted last-success model=c to index 0") {
		t.Fatalf("promotion log line missing: %q", out)
	}

	// Idempotent: already front ⇒ no log line.
	out = captureStickyLog(t, func() {
		_ = h.applyStickyOrder(on, []string{"c", "a", "b"}, "key-1", "req-log")
	})
	if out != "" {
		t.Fatalf("already-front promotion logged: %q", out)
	}
}

// TestApplyStickyOrder_NoSTICKYLogWhenGatedOff pins the zero-log negative.
func TestApplyStickyOrder_NoSTICKYLogWhenGatedOff(t *testing.T) {
	eng := stickyorder.NewEngine(time.Minute, time.Hour)
	defer eng.Stop()
	eng.RecordSuccess("key-1", "c")
	h := &Handler{stickyEngine: eng}

	out := captureStickyLog(t, func() {
		_ = h.applyStickyOrder(&ConfigSnapshot{StickyLastSuccessEnabled: false}, []string{"a", "b", "c"}, "key-1", "r")
		_ = h.applyStickyOrder(&ConfigSnapshot{StickyLastSuccessEnabled: true}, []string{"a", "b", "c"}, "", "r")
		_ = h.applyStickyOrder(&ConfigSnapshot{StickyLastSuccessEnabled: true}, []string{"a", "b", "c"}, "no-record", "r")
	})
	if strings.Contains(out, "[STICKY]") {
		t.Fatalf("gated-off paths logged [STICKY]: %q", out)
	}
}

func captureStickyLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prevOut := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	}()
	fn()
	return buf.String()
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestStickyOrder_SnapshotCarriesFlag pins R13: the per-request snapshot
// (Config.Clone) carries the flag to every choke point (rc.conf /
// arc.conf).
func TestStickyOrder_SnapshotCarriesFlag(t *testing.T) {
	cfg := config.Defaults
	cfg.StickyLastSuccessEnabled = true
	h := &Handler{config: &Config{ConfigMgr: database.NewMockConfigManagerWithConfig(cfg)}}

	snap := h.config.Clone()
	if !snap.StickyLastSuccessEnabled {
		t.Fatal("Config.Clone dropped StickyLastSuccessEnabled")
	}

	cfg.StickyLastSuccessEnabled = false
	h.config.ConfigMgr = database.NewMockConfigManagerWithConfig(cfg)
	if h.config.Clone().StickyLastSuccessEnabled {
		t.Fatal("Config.Clone must follow the live config value")
	}
}

// ─── Scope pin: ORDER only — group pick / RouteModel untouched ──────────────

// TestStickyOrder_PromotionDoesNotTouchRouteModel pins the ORDER-only
// contract (the no-grouplb-coupling ruling): a sticky promotion may serve
// a model that is NOT the group pick, but arc.reqLog.RouteModel must keep
// reporting the GROUP-served member and the group's binding is not
// re-pointed by the reorder.
func TestStickyOrder_PromotionDoesNotTouchRouteModel(t *testing.T) {
	h := newStickyHarness(t, true, true)
	rec := &modelRecorder{}
	h.swapUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		rec.add(rowIDFromUpstream(upstreamBodyModel(r)))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(stubUpstreamBody("stub", "ok")))
	})
	// Single-default group: the group pick is deterministic (m-a). m-a's
	// own fallback chain carries the recorded sticky winner, so the
	// promoted list is [sticky-w, m-a] and sticky-w SERVES — while the
	// group pick stays m-a.
	h.seedChatModel(t, "m-a", "sticky-w")
	h.seedChatModel(t, "sticky-w")
	h.seedGroup(t, "g-one", models.GroupMember{ModelID: "m-a", Weight: 1})

	msgs := stickyMessages()
	key := stickyKeyFor("g-one", msgs)
	h.sticky.RecordSuccess(key, "sticky-w")

	recorder := httptest.NewRecorder()
	h.h.HandleAnthropicMessages(recorder, makeStickyAnthropicRequest(t, "g-one", msgs))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}

	// The promotion really happened: sticky-w was attempted first.
	if got := rec.snapshot(); len(got) == 0 || got[0] != "sticky-w" {
		t.Fatalf("sticky promotion did not run: attempts=%v", got)
	}
	// ...and the delivered success re-recorded it.
	if v, ok := h.sticky.Get(key); !ok || v != "sticky-w" {
		t.Fatalf("recorded winner = (%q,%v), want sticky-w", v, ok)
	}
	// ORDER only: the metering/identity stamp still names the GROUP pick.
	rl := h.lastRequestLog(t)
	if rl.RouteModel != "m-a" {
		t.Fatalf("RouteModel = %q, want the group pick %q (promotion must not touch grouplb identity)", rl.RouteModel, "m-a")
	}
	if rl.Model != "g-one" {
		t.Fatalf("RequestLog.Model = %q, want the client-requested group id", rl.Model)
	}
}

// ─── §9 fixture: ultimate-path completion records NOTHING ───────────────────

// TestStickyOrder_UltimatePath_NoRecord pins §4.2 exclusion 1: the
// ultimate-model completion is not a race delivered success, so it must
// neither record nor disturb an existing record.
func TestStickyOrder_UltimatePath_NoRecord(t *testing.T) {
	h := newStickyHarness(t, true, true)

	// Auth is required for the X-Force-Ultimate-Model entitlement.
	db := setupIntegrationDB(t)
	t.Cleanup(func() { _ = db.Close() })
	h.tokenStore = auth.NewTokenStore(db, database.SQLite)
	plaintext, token, err := h.tokenStore.CreateToken(context.Background(), "sticky-ultimate", nil, "test-user", true, "", nil)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	h.seedChatModel(t, "m-a")
	h.seedChatModel(t, "ultimate-model")
	h.cfgMutate = func(c *config.Config) { c.UltimateModel.ModelID = "ultimate-model" }
	h.rebuild(t)

	msgs := stickyMessages()
	key := stickyKeyFor("m-a", msgs) // tokenID "" — see the token-salted variant below
	body := map[string]interface{}{
		"model":    "m-a",
		"messages": msgs,
	}
	req := makeChatRequest(t, body)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	req.Header.Set("X-Force-Ultimate-Model", "true")

	rec := httptest.NewRecorder()
	h.h.HandleChatCompletions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ultimate path did not execute: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Sanity: the ULTIMATE path (not the race) served this request.
	rl := h.lastRequestLog(t)
	if !rl.UltimateModelUsed {
		t.Fatalf("expected the ultimate path to run (UltimateModelUsed=false, model=%q)", rl.Model)
	}

	// The token-salted key for this authenticated request + the unsalted
	// key must BOTH be empty: nothing in the engine at all.
	tokenKey := credentiallb.ComputeConversationKey("m-a", token.ID, credentiallb.ExtractLeadingUserRun(msgs))
	if v, ok := h.sticky.Get(tokenKey); ok {
		t.Fatalf("ultimate-path completion recorded a winner: key=%s model=%q", "token", v)
	}
	if v, ok := h.sticky.Get(key); ok {
		t.Fatalf("ultimate-path completion recorded a winner: key=%s model=%q", "unsalted", v)
	}
}

// ─── §9 fixture: client-write-fail records NOTHING (BOTH lanes) ─────────────

// failWriteResponseWriter fails ONLY body writes — headers behave
// normally, so the request runs its full success course and the write
// error is the single difference from a healthy client.
type failWriteResponseWriter struct {
	header    http.Header
	writeErrs int
}

func (w *failWriteResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}
func (w *failWriteResponseWriter) WriteHeader(int) {}
func (w *failWriteResponseWriter) Write([]byte) (int, error) {
	w.writeErrs++
	return 0, errors.New("write: broken pipe")
}

// TestStickyOrder_ClientWriteFail_NoRecord pins §4.2 exclusion 2 on both
// lanes: a response the client never received is not a delivered
// success. The non-stream lane captures w.Write's error (the asymmetry
// fix); the stream lane returns from the relay before its record site.
func TestStickyOrder_ClientWriteFail_NoRecord(t *testing.T) {
	t.Run("non-stream", func(t *testing.T) {
		h := newStickyHarness(t, true, true)
		h.seedChatModel(t, "m-a")

		key := stickyKeyFor("m-a", stickyMessages())
		w := &failWriteResponseWriter{}
		h.h.HandleChatCompletions(w, makeStickyChatRequest(t))

		if w.writeErrs == 0 {
			t.Fatal("fixture did not exercise a failing body write")
		}
		if v, ok := h.sticky.Get(key); ok {
			t.Fatalf("non-stream client-write failure recorded a winner: %q", v)
		}
		// Non-vacuous: the request itself still completed normally.
		if rl := h.lastRequestLog(t); rl.Status != "completed" {
			t.Fatalf("request log status = %q, want completed (the write failure must gate ONLY the sticky record)", rl.Status)
		}
	})

	t.Run("stream", func(t *testing.T) {
		h := newStickyHarness(t, true, true)
		h.swapUpstream(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "data: %s\n\n", makeOpenAITextChunk("hello"))
			fmt.Fprint(w, "data: [DONE]\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		})
		h.seedChatModel(t, "m-a")

		key := stickyKeyFor("m-a", stickyMessages())
		w := &failWriteResponseWriter{}
		req := makeChatRequest(t, map[string]interface{}{
			"model":    "m-a",
			"stream":   true,
			"messages": stickyMessages(),
		})
		h.h.HandleChatCompletions(w, req)

		if w.writeErrs == 0 {
			t.Fatal("fixture did not exercise a failing stream write")
		}
		if v, ok := h.sticky.Get(key); ok {
			t.Fatalf("stream client-write failure recorded a winner: %q", v)
		}
	})
}

// ─── §9 fixture: Anthropic BACKUP-POOL re-entry variant ─────────────────────

// TestStickyOrder_AnthropicBackupPoolReEntry_Reorder pins §8.8 on the
// Anthropic lane: the ExcludeGroupMember backup-pool reselect rebuild
// (handler_groups.go:437 → :459) lands on the mandatory site (d), so the
// rebuilt list is re-promoted exactly as on the OpenAI lane.
func TestStickyOrder_AnthropicBackupPoolReEntry_Reorder(t *testing.T) {
	h := newStickyHarness(t, true, true)
	rec := &modelRecorder{}
	h.swapUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		rec.add(rowIDFromUpstream(upstreamBodyModel(r)))
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"type":"server_error","message":"boom"}}`))
	})
	h.seedChatModel(t, "m-a")             // only default member — fails
	h.seedChatModel(t, "m-b", "sticky-w") // backup-only member
	h.seedChatModel(t, "sticky-w")
	if err := h.mgr.AddModel(models.ModelConfig{
		ID: "g-pool", Name: "g-pool", Enabled: true, Kind: models.KindGroup,
		Internal: true, ExcludeFromUltimateSwitching: true,
		Members: []models.GroupMember{
			{ModelID: "m-a", Weight: 1},
			{ModelID: "m-b", Weight: 1, BackupOnly: true},
		},
	}); err != nil {
		t.Fatalf("AddModel(g-pool): %v", err)
	}

	msgs := stickyMessages()
	key := stickyKeyFor("g-pool", msgs)
	h.sticky.RecordSuccess(key, "sticky-w")

	recorder := httptest.NewRecorder()
	h.h.HandleAnthropicMessages(recorder, makeStickyAnthropicRequest(t, "g-pool", msgs))

	// m-a (default) fails → backup reselect m-b → rebuild [m-b, sticky-w]
	// → site (d) reorders to [sticky-w, m-b].
	got := rec.snapshot()
	if !equalStrings(got, []string{"m-a", "sticky-w", "m-b"}) {
		t.Fatalf("backup-pool re-entry walk order = %v, want [m-a sticky-w m-b]", got)
	}
}
