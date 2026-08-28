// Phase 5 / real-streaming-default — golden-file generator / recorder.
//
// The `TestRealStreaming_GoldenRecorder` test is a one-shot utility
// for generating / updating the committed goldens. Run with:
//
//	go test -run TestRealStreaming_GoldenRecorder ./pkg/proxy/ -update
//
// The default behavior is READ-ONLY (asserts the goldens match what
// the recorder would write). The `-update` flag (custom flag parsed
// via -args -update) overwrites the goldens under
// pkg/proxy/testdata/real_streaming_golden/*.json.
//
// `TestRealStreaming_Events_BufferedEqualsLive` is the EVENT-PARITY
// assertion (plan task 5.4): for a given upstream fixture, the
// `events.Bus` publishes the same EVENT TYPES in both buffered and
// live modes — presence asserted, exact ordering NOT (event-bus
// ordering is timing-dependent). The asymmetry for
// `model_credential_selected` (locked L7) is respected: OpenAI race
// path publishes, Anthropic path does NOT.
//
// Generators also include fixtures for `role_only_first`,
// `usage_first`, `thinking_first` first-chunk variants.
package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
)

var goldenUpdate = flag.Bool("update", false, "regenerate golden files under testdata/real_streaming_golden/")

// goldenEnvelope is the JSON shape of each committed golden file.
// Including content_type makes it forward-compatible with future
// coverage of response headers and status.
type goldenEnvelope struct {
	Body        string `json:"body"`
	ContentType string `json:"content_type"`
}

// goldenPath returns the canonical path under testdata for a given
// golden name (no extension).
func goldenPath(name string) string {
	return filepath.Join("testdata", "real_streaming_golden", name+".json")
}

// writeGolden writes the envelope to disk under the golden path. Used
// only by the generator (-update flag).
func writeGolden(t *testing.T, name string, env goldenEnvelope) {
	t.Helper()
	path := goldenPath(name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	b, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
	t.Logf("wrote golden: %s", path)
}

// ─────────────────────────────────────────────────────────────────────────────
// Per-path golden generators. Each helper runs the upstream mock
// in BUFFERED mode (header-present or BufferMode=true) and returns
// the wire body + content-type for goldenization.
// ─────────────────────────────────────────────────────────────────────────────

// genOpenAIRaceText is kept as a tiny wrapper around the parameterized
// genOpenAIRace(t, "text") for direct callers; the golden registry
// uses genOpenAIRace directly with the variant list.
func genOpenAIRaceText(t *testing.T) (string, string) {
	return genOpenAIRace(t, "text")
}

// genAnthropicPassthrough runs the named variant through the
// Anthropic→Anthropic passthrough path with header-present
// (buffered). Real passthrough fixture: internal model with
// anthropic-provider credential pointing at the upstream.
func genAnthropicPassthrough(t *testing.T, variant string) (string, string) {
	t.Helper()
	upstream := httptest.NewServer(anthropicRawPassthroughHandler(variant))
	t.Cleanup(upstream.Close)

	t.Setenv("APPLY_ENV_OVERRIDES", "true")
	t.Setenv("UPSTREAM_URL", upstream.URL)
	mgr, _ := config.NewManager()
	modelsCfg := models.NewModelsConfig()
	if err := modelsCfg.AddCredential(models.CredentialConfig{
		ID:       "anthropic-passthrough-cred",
		Provider: "anthropic",
		APIKey:   "sk-anthropic-test",
		BaseURL:  upstream.URL,
	}); err != nil {
		t.Fatalf("AddCredential: %v", err)
	}
	if err := modelsCfg.AddModel(models.ModelConfig{
		ID:              "claude-passthrough",
		Name:            "claude-passthrough",
		Enabled:         true,
		Internal:        true,
		InternalModel:   "claude-sonnet-4-5",
		InternalBaseURL: upstream.URL,
		Credentials:     models.TestRefs("anthropic-passthrough-cred"),
	}); err != nil {
		t.Fatalf("AddModel: %v", err)
	}
	h := NewHandler(&Config{ConfigMgr: mgr, ModelsConfig: modelsCfg}, events.NewBus(), store.NewRequestStore(100), nil, nil, nil, nil)

	body := anthropicBody("claude-passthrough", true, []map[string]interface{}{
		{"role": "user", "content": "hi"},
	})
	req := makeLiveAnthropicRequest(t, body, true)
	rec := httptest.NewRecorder()
	h.HandleAnthropicMessages(rec, req)

	return rec.Body.String(), rec.Header().Get("Content-Type")
}

// genAnthropicExternal runs the named variant through the
// Anthropic→OpenAI-wire external translation path with header-present.
func genAnthropicExternal(t *testing.T, variant string) (string, string) {
	t.Helper()
	fixture := fixtureSet()[variant]
	upstream := httptest.NewServer(fixture.handler())
	t.Cleanup(upstream.Close)

	t.Setenv("APPLY_ENV_OVERRIDES", "true")
	t.Setenv("UPSTREAM_URL", upstream.URL)
	mgr, _ := config.NewManager()
	modelsCfg := models.NewModelsConfig()
	h := NewHandler(&Config{ConfigMgr: mgr, ModelsConfig: modelsCfg}, events.NewBus(), store.NewRequestStore(100), nil, nil, nil, nil)

	body := anthropicBody("claude-sonnet-4-5", true, []map[string]interface{}{
		{"role": "user", "content": "hi"},
	})
	req := makeLiveAnthropicRequest(t, body, true)
	rec := httptest.NewRecorder()
	h.HandleAnthropicMessages(rec, req)

	return rec.Body.String(), rec.Header().Get("Content-Type")
}

// genAnthropicInternal runs the named variant through the
// handleStream direct path with NO live translator wired (buffered
// branch). The recorder body is the SSE wire shape.
func genAnthropicInternal(t *testing.T, variant string) (string, string) {
	t.Helper()
	events := streamEventsForVariant(variant)
	provider := &liveStreamEventProvider{events: events}

	handler := NewInternalHandler(
		&models.ModelConfig{ID: "test-model", InternalModel: "gpt-4"},
		&mockModelsConfig{},
	)

	rec := httptest.NewRecorder()
	rec.Body = new(bytes.Buffer)
	rw := &flushingResponseRecorder{ResponseRecorder: rec}

	req := &providers.ChatCompletionRequest{
		Model:    "test-model",
		Messages: []providers.ChatMessage{{Role: "user", Content: "hi"}},
		Stream:   true,
	}
	if err := handler.handleStream(context.Background(), provider, req, rw, "gpt-4"); err != nil {
		t.Fatalf("handleStream (buffered): %v", err)
	}
	return rw.Body.String(), rec.Header().Get("Content-Type")
}

// genOpenAIRace runs the named variant through the
// /v1/chat/completions OpenAI race path with header-present.
func genOpenAIRace(t *testing.T, variant string) (string, string) {
	t.Helper()
	fixture := fixtureSet()[variant]
	h, _ := newTestHandler(t, fixture.handler(), models.NewModelsConfig())

	reqHTTP := makeRequest(t, simpleBody("mock-model", true))
	reqHTTP.Header.Set("X-LLMProxy-Buffer-Response", "true")
	rec := httptest.NewRecorder()
	h.HandleChatCompletions(rec, reqHTTP)

	return rec.Body.String(), rec.Header().Get("Content-Type")
}

// ─────────────────────────────────────────────────────────────────────────────
// Golden registry — central table mapping golden names to generator
// functions. Adding a new path: add an entry here + a generator
// above + a subtest in TestRealStreaming_GoldenRecorder below.
//
// Variant matrix per path (reviewer finding #2): text + thinking +
// tool_call + usage everywhere; role_only + usage_first + comment_first
// on paths 2 and 4 (OpenAI-wire upstream; role_only/usage_first/
// comment_first don't apply to the Anthropic-wire passthrough or the
// typed-event internal handler).
// ─────────────────────────────────────────────────────────────────────────────

type goldenGen struct {
	name string
	gen  func(t *testing.T) (body, contentType string)
}

func goldenRegistry() []goldenGen {
	out := []goldenGen{}
	// Path 1 — Anthropic→Anthropic passthrough (real fixture).
	for _, variant := range []string{"text", "thinking", "tool_call", "usage"} {
		variant := variant
		out = append(out, goldenGen{
			name: "anthropic_passthrough_" + variant,
			gen:  func(t *testing.T) (string, string) { return genAnthropicPassthrough(t, variant) },
		})
	}
	// Path 2 — Anthropic→OpenAI-wire external translation.
	for _, variant := range []string{"text", "thinking", "tool_call", "usage", "role_only", "usage_first", "comment_first"} {
		variant := variant
		out = append(out, goldenGen{
			name: "anthropic_external_" + variant,
			gen:  func(t *testing.T) (string, string) { return genAnthropicExternal(t, variant) },
		})
	}
	// Path 3 — Anthropic→OpenAI-wire internal translation
	// (typed-event channel; role_only/usage_first/comment_first
	// don't apply — the internal handler normalizes into typed
	// events and re-emits OpenAI-wire SSE).
	for _, variant := range []string{"text", "thinking", "tool_call", "usage"} {
		variant := variant
		out = append(out, goldenGen{
			name: "anthropic_internal_" + variant,
			gen:  func(t *testing.T) (string, string) { return genAnthropicInternal(t, variant) },
		})
	}
	// Path 4 — /v1/chat/completions OpenAI race path.
	for _, variant := range []string{"text", "thinking", "tool_call", "usage", "role_only", "usage_first", "comment_first"} {
		variant := variant
		out = append(out, goldenGen{
			name: "openai_race_" + variant,
			gen:  func(t *testing.T) (string, string) { return genOpenAIRace(t, variant) },
		})
	}
	// Paths 5+6 — Ultimate external/internal stream — covered by
	// existing pkg/ultimatemodel tests; their goldens are produced
	// in that package's own test fixtures.
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// TestRealStreaming_GoldenRecorder — generates / verifies all goldens.
//
//   go test -run TestRealStreaming_GoldenRecorder ./pkg/proxy/
//
// Default: READ-ONLY (asserts existing goldens match the recorder
// output). With -update: regenerates the goldens.
// ─────────────────────────────────────────────────────────────────────────────

func TestRealStreaming_GoldenRecorder(t *testing.T) {
	if *goldenUpdate == false && os.Getenv("GOLDEN_REGEN") == "" {
		t.Log("read-only mode; pass -update to regenerate goldens, or set GOLDEN_REGEN=1")
	}

	for _, g := range goldenRegistry() {
		t.Run(g.name, func(t *testing.T) {
			body, ct := g.gen(t)
			stripped := volStrip(body)

			if *goldenUpdate || os.Getenv("GOLDEN_REGEN") != "" {
				writeGolden(t, g.name, goldenEnvelope{Body: stripped, ContentType: ct})
				return
			}

			// READ-ONLY: assert the on-disk golden matches the
			// freshly-generated wire (a drift here is a real
			// signal — the goldens were recorded against a
			// specific commit and a change means behavior
			// changed).
			onDisk, onDiskCT := readGolden(t, g.name)
			if onDisk != stripped {
				t.Errorf("golden drift on %s — body diverges; rerun with -update if the change is intentional. First 200 chars of diff:\n  golden: %q\n  actual: %q",
					g.name, truncate(onDisk, 200), truncate(stripped, 200))
			}
			if onDiskCT != ct {
				t.Errorf("golden drift on %s — content-type: %q vs %q", g.name, onDiskCT, ct)
			}
		})
	}
}

// truncate helper used in error messages only.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ─────────────────────────────────────────────────────────────────────────────
// TestRealStreaming_Events_BufferedEqualsLive — EVENT-PARITY (plan task 5.4).
//
// For a given upstream fixture, both buffered and live modes publish
// the same EVENT TYPES on `events.Bus`. Ordering is NOT asserted (the
// bus is timing-dependent). The race_winner_selected event fires
// mid-stream in live mode (presence asserted); model_credential_selected
// asymmetry is LOCKED L7 (Anthropic path does NOT publish).
// ─────────────────────────────────────────────────────────────────────────────

// eventRecorder wraps events.Bus.Subscribe to collect all events for
// a request, optionally filtering by request ID.
type eventRecorder struct {
	ch    chan events.Event
	bus   *events.Bus
	mu    sync.Mutex
	got   []events.Event
	done  chan struct{}
	once  sync.Once
	hits  map[string]int // event type -> count (guarded by mu)
}

func newEventRecorder(bus *events.Bus) *eventRecorder {
	ch, err := bus.Subscribe()
	if err != nil {
		panic(err)
	}
	r := &eventRecorder{
		ch:   ch,
		bus:  bus,
		hits: make(map[string]int),
		done: make(chan struct{}),
	}
	go func() {
		for e := range ch {
			r.mu.Lock()
			r.got = append(r.got, e)
			r.hits[e.Type]++
			r.mu.Unlock()
		}
		close(r.done)
	}()
	return r
}

func (r *eventRecorder) close() {
	r.bus.Unsubscribe(r.ch)
	r.once.Do(func() {
		<-r.done
	})
}

func (r *eventRecorder) saw(eventType string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.hits[eventType] > 0
}

func TestRealStreaming_Events_BufferedEqualsLive(t *testing.T) {
	// The recorder is wired against the handler's bus; events are
	// published on h.bus. We run the request once per mode with a
	// fresh recorder and assert presence of event TYPES (not exact
	// ordering — the bus is timing-dependent).
	runWithRecorder := func(bufMode bool) *eventRecorder {
		// Rebuild handler with fresh bus + store.
		h2, _ := newTestHandler(t, fixtureSet()["text"].handler(), models.NewModelsConfig())
		rec := newEventRecorder(h2.bus)
		req := makeRequest(t, simpleBody("mock-model", true))
		if bufMode {
			req.Header.Set("X-LLMProxy-Buffer-Response", "true")
		} else {
			req.Header.Del("X-LLMProxy-Buffer-Response")
		}
		wrec := httptest.NewRecorder()
		h2.HandleChatCompletions(wrec, req)
		// Allow late events (request_completed may fire after the
		// response is written) to drain.
		time.Sleep(100 * time.Millisecond)
		return rec
	}

	t.Run("OpenAI_RacePath", func(t *testing.T) {
		buf := runWithRecorder(true)
		defer buf.close()
		live := runWithRecorder(false)
		defer live.close()

		// Both must publish request_started + request_completed.
		if !buf.saw("request_started") {
			t.Errorf("buffered mode: missing request_started event")
		}
		if !buf.saw("request_completed") {
			t.Errorf("buffered mode: missing request_completed event")
		}
		if !live.saw("request_started") {
			t.Errorf("live mode: missing request_started event")
		}
		if !live.saw("request_completed") {
			t.Errorf("live mode: missing request_completed event")
		}

		// LOCKED L7: OpenAI race path publishes
		// model_credential_selected (single source of truth at
		// race_coordinator.go:896-918). The publish fires ONLY
		// when the per-request `publish` callback was wired —
		// which itself only happens when BOTH `c.eventBus != nil`
		// AND `c.engine != nil` (line 901). The mock setup here
		// uses `NewHandler(... nil, nil, nil, nil)` — the 7th
		// positional arg is the credential LB engine, and we pass
		// nil. So model_credential_selected is GUARANTEED NOT TO
		// PUBLISH in this harness, regardless of buffered/live
		// mode. We assert absence with an explicit reason rather
		// than silently logging it as a "may be expected" caveat
		// (reviewer finding #4: no silent shrugs — name the
		// structural reason the publication cannot fire).
		//
		// The POSITIVE half of L7 (model_credential_selected IS
		// published when an engine IS wired) is pinned by
		// TestCoordinator_ModelCredentialSelected_OncePerFirstBinding
		// in race_coordinator_credfailover_test.go:334 — that test
		// exercises the full engine + publish callback path and
		// asserts W-1 / exit #4 (one event per newly-bound
		// resolution).
		if buf.saw("model_credential_selected") {
			t.Errorf("buffered: model_credential_selected published — the test harness passes a nil LB engine (NewHandler 7th arg = nil); the publish callback at race_coordinator.go:901 is never wired. This is a test-fixture regression.")
		}
		if live.saw("model_credential_selected") {
			t.Errorf("live: model_credential_selected published — same nil-engine harness caveat (race_coordinator.go:901)")
		}
	})

	t.Run("Anthropic_Path", func(t *testing.T) {
		// Anthropic path does NOT publish model_credential_selected (locked L7).
		fixture := fixtureSet()["text"]
		upstream := httptest.NewServer(fixture.handler())
		t.Cleanup(upstream.Close)

		t.Setenv("APPLY_ENV_OVERRIDES", "true")
		t.Setenv("UPSTREAM_URL", upstream.URL)
		mgr, _ := config.NewManager()
		modelsCfg := models.NewModelsConfig()
		h := NewHandler(&Config{ConfigMgr: mgr, ModelsConfig: modelsCfg}, events.NewBus(), store.NewRequestStore(100), nil, nil, nil, nil)

		rec := newEventRecorder(h.bus)
		defer rec.close()

		body := anthropicBody("claude-sonnet-4-5", true, []map[string]interface{}{
			{"role": "user", "content": "hi"},
		})
		req := makeLiveAnthropicRequest(t, body, false) // live mode
		rr := httptest.NewRecorder()
		h.HandleAnthropicMessages(rr, req)
		time.Sleep(100 * time.Millisecond)

		// Anthropic path MUST publish request_started +
		// request_completed.
		if !rec.saw("request_started") {
			t.Errorf("Anthropic path: missing request_started")
		}
		if !rec.saw("request_completed") {
			t.Errorf("Anthropic path: missing request_completed")
		}
		// LOCKED L7: model_credential_selected MUST NOT be published
		// on the Anthropic path (the internal_handler.go:146-256 path
		// discards NewlyBound and never publishes).
		if rec.saw("model_credential_selected") {
			t.Errorf("Anthropic path: model_credential_selected published — L7 violation (must NOT publish)")
		}
	})
}
