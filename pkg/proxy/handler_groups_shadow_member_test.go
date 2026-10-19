package proxy

// P0 shadowfix — "group member id shadowed by its own group".
//
// THE LIVE SHAPE (llm.ensem.dev, 2026-10-19):
//
//	coding-group: kind=group, shadow_model_id="coding",
//	members=[{coding,w10},{coding2,w3},{coding3,w1},{coding4,w10}]
//
// The group's shadow claim makes the CLIENT-FACING resolution of
// "coding" land on the GROUP row (claims-first — correct), while
// "coding" is ALSO the ID of a member row. Every EXECUTION-
// resolution read of "coding" must therefore resolve by ROW IDENTITY
// (Am22 L1: "member refs resolve by direct ID; claims apply to
// client-facing resolution only") — otherwise the per-attempt
// machinery sees the GROUP row (no credentials, no credential list)
// instead of the MEMBER row.
//
// TRIGGER (repro-confirmed, 2026-10-19): a static seed does NOT
// reproduce the failure — a cold boot primes the cache with the member
// row and serves fine. The failure appears after a RUNTIME group write
// (PUT/POST /fe/api/models): the decorator's claim-eviction hook
// (modelscache/models.go:260 evictClaimEntriesLocked) DELETES the
// member's cache entry, and the claims-first fill
// (modelscache/models.go:456 GetModel → :641) can no longer restore
// it (the redirect returns the GROUP row before the strict fill).
//
// HARNESSES — the tests below do NOT all drive a runtime group write
// through the decorator; this file uses TWO harnesses, and each pins a
// different seam:
//
// (a) newShadowFixHarness (this file) — the PRODUCTION wiring: a real
//     *database.ModelsManager wrapped by the modelscache decorator
//     (cmd/main.go:143, CACHE_LAYER_ENABLED defaults ON). The runtime
//     group write goes THROUGH the decorator, so the claim-eviction
//     hook fires and the member row is MISSING from the cache — the
//     exact post-write live state. Used by the tests that pin the
//     RESOLUTION seam:
//       (a) TestShadowFix_SelfShadowMemberServes_LiveShape
//       (b) TestShadowFix_SelfShadowMemberServes_Day1Shape
//       (c) TestShadowFix_PlainMemberUnaffected
//
// (b) newGroupHarness (handler_groups_test.go) — the RAW
//     *database.ModelsManager, NO modelscache decorator (the Handler's
//     ModelsConfig IS that raw manager). Used by:
//       (d) TestShadowFix_SelfShadowMemberCredentialFailover
//     That test needs no decorator: it pins the COORDINATOR's
//     credential-failover ELIGIBILITY gate (race_coordinator.go:1523) —
//     a gate on the attempt's OWN model row. The shadow claim it
//     exercises lives in the MANAGER itself (GetModel is claims-first
//     — store.go:988; GetModelNative bypasses claims — store.go:1010),
//     so the raw manager reproduces the defect (a claims-first read
//     returns the credential-less GROUP row) with no cache in the
//     picture. The claim-eviction trigger is irrelevant to that gate:
//     the READ is what is under test, not the cache state.
//
// ASSERTIONS: the member carries NO fallback chain, and the tests
// assert on the RACE OUTCOME (the member's own attempt must reach the
// upstream, and no race_request_failed event may be published) —
// never on the HTTP status alone (a 200 can be served by a fallback).
//
// The four shapes the P0 brief demands:
//   (a) live shape  — group shadows "coding" AND lists "coding" as a member
//   (b) day-1 shape — the legal equivalent of "group id == shadow id ==
//       member id" (models.id is a shared TEXT primary key, so a group
//       row and a chat row CANNOT share an id: the group id is
//       "coding-group", the shadow id is "coding", the member id is
//       "coding")
//   (c) a plain non-shadowed member still works (pinning — no behavior change)
//   (d) the credential-failover path for a self-shadow member

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/modelscache"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
)

// shadowFixCapture is one recorded upstream wire.
type shadowFixCapture struct {
	Model string // the "model" field of the upstream REQUEST body
	Auth  string
}

// shadowFixHarness wires the PRODUCTION model-config shape: a real
// *database.ModelsManager wrapped by the modelscache decorator
// (cmd/main.go:143), a stub upstream that records every wire, a real
// usage counter and the proxy handler.
//
// The reconciler is pinned to an hour: the test asserts the
// POST-EVICTION cache state, and a reconciler tick would restore the
// member row and mask the bug.
type shadowFixHarness struct {
	mgr   *database.ModelsManager
	cache *modelscache.CachedModelsConfig
	h     *Handler
	up    *httptest.Server
	bus   *events.Bus
	clean func()

	mu   sync.Mutex
	caps []shadowFixCapture
}

func newShadowFixHarness(t *testing.T) *shadowFixHarness {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("RACE_RETRY_ENABLED", "true")
	t.Setenv("DATABASE_URL", "sqlite:"+tmp+"/test.db")
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
	hh := &shadowFixHarness{}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var decoded map[string]interface{}
		_ = json.Unmarshal(body, &decoded)
		m, _ := decoded["model"].(string)
		hh.mu.Lock()
		hh.caps = append(hh.caps, shadowFixCapture{Model: m, Auth: r.Header.Get("Authorization")})
		hh.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(stubUpstreamBody(m, "hi")))
	}))
	// The production decorator (CACHE_LAYER_ENABLED defaults ON).
	cache, err := modelscache.NewCachedModelsConfig(mgr, modelscache.Options{ReconcileInterval: time.Hour})
	if err != nil {
		t.Fatalf("NewCachedModelsConfig: %v", err)
	}
	if err := cache.AddCredential(models.CredentialConfig{
		ID: p5FixtureCredID, Provider: "openai", APIKey: "test-key", BaseURL: up.URL,
	}); err != nil {
		t.Fatalf("AddCredential: %v", err)
	}
	mgrCfg := newTestManagerWithConfig(t, up.URL)
	reqStore := store.NewRequestStore(100, store.WithMaxBytes(256<<20))
	counter := newAm29Counter(t)
	h := NewHandler(&Config{ConfigMgr: mgrCfg, ModelsConfig: cache}, bus, reqStore, nil, nil, counter, mgr.Engine())
	hh.mgr = mgr
	hh.cache = cache
	hh.h = h
	hh.up = up
	hh.bus = bus
	hh.clean = func() {
		cache.Stop()
		up.Close()
		mgr.Close()
		store1.Close()
	}
	return hh
}

func (h *shadowFixHarness) close() { h.clean() }

// recorded returns the upstream wires seen so far.
func (h *shadowFixHarness) recorded() []shadowFixCapture {
	h.mu.Lock()
	defer h.mu.Unlock()
	cp := make([]shadowFixCapture, len(h.caps))
	copy(cp, h.caps)
	return cp
}

// servedModels returns the distinct upstream "model" fields seen.
func (h *shadowFixHarness) servedModels() []string {
	seen := map[string]bool{}
	out := []string{}
	for _, c := range h.recorded() {
		if c.Model != "" && !seen[c.Model] {
			seen[c.Model] = true
			out = append(out, c.Model)
		}
	}
	return out
}

// shadowFixAddMember seeds an internal chat model with a DISTINCT
// internal_model (the wire identity of the member) and NO fallback
// chain (a fallback would mask the failure behind a 200).
func shadowFixAddMember(t *testing.T, h *shadowFixHarness, id, upstream string) {
	t.Helper()
	if err := h.cache.AddModel(models.ModelConfig{
		ID: id, Name: id, Enabled: true, Kind: models.KindChat,
		Internal: true, InternalModel: upstream,
		Credentials: models.TestRefs(p5FixtureCredID),
	}); err != nil {
		t.Fatalf("AddModel(%s): %v", id, err)
	}
}

// (a) LIVE shape — the group claims "coding" AND "coding" is one of
// its members. The client asks for "coding" (the shadow): the
// client-facing resolution lands on the group, the group picks a
// member, and the SELF-SHADOW member ("coding") must be able to
// SERVE.
//
// Each request carries a distinct leading-user run ⇒ a distinct
// conversation key ⇒ an independent weighted group pick, so the
// self-shadow member is exercised within a bounded number of requests
// (weight 10 of 24 ⇒ ~42% per pick).
func TestShadowFix_SelfShadowMemberServes_LiveShape(t *testing.T) {
	h := newShadowFixHarness(t)
	defer h.close()
	rec := am29SubscribeAll(t, h.bus)

	for _, m := range []struct{ id, upstream string }{
		{"coding", "up-coding"},
		{"coding2", "up-coding2"},
		{"coding3", "up-coding3"},
		{"coding4", "up-coding4"},
	} {
		shadowFixAddMember(t, h, m.id, m.upstream)
	}
	// The RUNTIME group write — the trigger: the claim-eviction hook
	// drops the member's cache entry.
	if err := h.cache.AddModel(models.ModelConfig{
		ID: "coding-group", Name: "Coding Group", Enabled: true,
		Kind: models.KindGroup, Internal: true,
		ExcludeFromUltimateSwitching: true,
		Members: []models.GroupMember{
			{ModelID: "coding", Weight: 10},
			{ModelID: "coding2", Weight: 3},
			{ModelID: "coding3", Weight: 1},
			{ModelID: "coding4", Weight: 10},
		},
		ShadowModelID: "coding",
	}); err != nil {
		t.Fatalf("AddModel(coding-group): %v", err)
	}

	const attempts = 40
	servedSelfShadow := false
	failures := 0
	for i := 0; i < attempts && !servedSelfShadow; i++ {
		r := httptest.NewRecorder()
		h.h.HandleChatCompletions(r, makeChatRequest(t, map[string]interface{}{
			"model":    "coding",
			"messages": []interface{}{map[string]interface{}{"role": "user", "content": fmt.Sprintf("hi-%d", i)}},
		}))
		if r.Code != http.StatusOK {
			failures++
		}
		for _, m := range h.servedModels() {
			if m == "up-coding" {
				servedSelfShadow = true
			}
		}
	}
	if !servedSelfShadow {
		t.Fatalf("self-shadow member %q never served across %d requests (%d non-200) — the group picked it but its attempt could not resolve it (the group row was read instead of the member row); upstream models seen=%v", "coding", attempts, failures, h.servedModels())
	}
	// Outcome assertion: no attempt may have failed.
	shadowFixAssertNoRaceFailure(t, rec)
}

// (b) DAY-1 shape — the legal equivalent of "group id == shadow id ==
// member id" (a group row and a chat row cannot share models.id —
// the primary key is shared TEXT): group "coding-group", shadow
// "coding", single member "coding". The pick is deterministic ⇒ the
// self-shadow member MUST serve the request.
func TestShadowFix_SelfShadowMemberServes_Day1Shape(t *testing.T) {
	h := newShadowFixHarness(t)
	defer h.close()
	rec := am29SubscribeAll(t, h.bus)

	shadowFixAddMember(t, h, "coding", "up-coding")
	// The RUNTIME group write (the eviction trigger).
	if err := h.cache.AddModel(models.ModelConfig{
		ID: "coding-group", Name: "Coding Group", Enabled: true,
		Kind: models.KindGroup, Internal: true,
		ExcludeFromUltimateSwitching: true,
		Members:                      []models.GroupMember{{ModelID: "coding", Weight: 1}},
		ShadowModelID:                "coding",
	}); err != nil {
		t.Fatalf("AddModel(coding-group): %v", err)
	}

	r := httptest.NewRecorder()
	h.h.HandleChatCompletions(r, makeChatRequest(t, map[string]interface{}{
		"model":    "coding",
		"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
	}))
	// OUTCOME assertion (not the status code): the member's own
	// attempt must have reached the upstream.
	served := h.servedModels()
	if len(served) == 0 {
		t.Fatalf("day-1 self-shadow: the upstream was never reached (status=%d body=%s) — the member's attempt failed before I/O", r.Code, r.Body.String())
	}
	if served[0] != "up-coding" {
		t.Fatalf("day-1 self-shadow: upstream model=%q, want %q (the self-shadow member must serve)", served[0], "up-coding")
	}
	shadowFixAssertNoRaceFailure(t, rec)
}

// (c) PINNING — a plain (non-shadowed) member is unaffected, and the
// client-facing shadow semantics are unchanged: a client asking for
// the shadow id still goes through the GROUP (member rotation), never
// straight to the chat row.
func TestShadowFix_PlainMemberUnaffected(t *testing.T) {
	h := newShadowFixHarness(t)
	defer h.close()

	shadowFixAddMember(t, h, "coding", "up-coding")
	shadowFixAddMember(t, h, "coding2", "up-coding2")
	shadowFixAddMember(t, h, "coding3", "up-coding3")
	if err := h.cache.AddModel(models.ModelConfig{
		ID: "coding-group", Name: "Coding Group", Enabled: true,
		Kind: models.KindGroup, Internal: true,
		ExcludeFromUltimateSwitching: true,
		Members: []models.GroupMember{
			{ModelID: "coding", Weight: 1},
			{ModelID: "coding2", Weight: 1},
			{ModelID: "coding3", Weight: 1},
		},
		ShadowModelID: "coding",
	}); err != nil {
		t.Fatalf("AddModel(coding-group): %v", err)
	}

	// Plain member, requested directly (it is NOT the shadow) — the
	// request must serve that member.
	r := httptest.NewRecorder()
	h.h.HandleChatCompletions(r, makeChatRequest(t, map[string]interface{}{
		"model":    "coding2",
		"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
	}))
	if r.Code != http.StatusOK {
		t.Fatalf("plain member: status=%d body=%s", r.Code, r.Body.String())
	}
	served := h.servedModels()
	if len(served) == 0 {
		t.Fatalf("plain member: the upstream was never reached")
	}
	if served[0] != "up-coding2" {
		t.Fatalf("plain member: upstream model=%q, want %q", served[0], "up-coding2")
	}

	// The shadow id still routes through the GROUP (the client-facing
	// claims-first semantics are untouched by the execution-resolution
	// fix): the response is masked to the client string, and the
	// upstream saw a MEMBER's internal model — never the group id.
	r2 := httptest.NewRecorder()
	h.h.HandleChatCompletions(r2, makeChatRequest(t, map[string]interface{}{
		"model":    "coding",
		"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
	}))
	if r2.Code != http.StatusOK {
		t.Fatalf("shadow id: status=%d body=%s", r2.Code, r2.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(r2.Body.Bytes(), &body); err != nil {
		t.Fatalf("shadow id: response unmarshal: %v", err)
	}
	if m, _ := body["model"].(string); m != "coding" {
		t.Errorf("shadow id: response model=%q, want %q (the Am26 mask rewrites the response to the client string)", m, "coding")
	}
	for _, m := range h.servedModels() {
		if strings.Contains(m, "group") {
			t.Errorf("shadow id: upstream model=%q — the group id must never reach the upstream", m)
		}
	}
}

// (d) CREDENTIAL-FAILOVER path for a self-shadow member. The member
// has TWO credentials; the stub 429s credential A. The coordinator's
// credential-failover admission gate reads the attempt's model row to
// count the credentials — for a self-shadow member that read must
// land on the MEMBER row (2 credentials ⇒ failover allowed), not on
// the GROUP row (0 credentials ⇒ failover silently disabled ⇒ the
// 429 bubbles to the group layer, which has no alternative member ⇒
// the request fails).
func TestShadowFix_SelfShadowMemberCredentialFailover(t *testing.T) {
	h := newGroupHarness(t)
	defer h.close()

	// Swap the stub: credential A rate-limits, credential B serves.
	// keyAHits counts the rate-limited calls — the test asserts the
	// 429 path was ACTUALLY exercised (a 50/50 credential pick would
	// let the test pass trivially).
	var keyAHits int32
	h.up.Close()
	h.up = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if strings.Contains(auth, "key-A") {
			atomic.AddInt32(&keyAHits, 1)
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"type":"rate_limit_error","message":"429"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(stubUpstreamBody("glm-5", "ok")))
	}))
	// The harness cleanup closes the server captured at CONSTRUCTION
	// (the stub closed just above — httptest.Server.Close is
	// idempotent), so the REPLACEMENT would leak. Close whichever
	// server is installed at cleanup time: h.up is read lazily, so
	// this closes the replacement.
	t.Cleanup(func() { h.up.Close() })
	h.rebuildHandler(t)

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
	// The self-shadow member: its ID is ALSO the group's shadow id.
	// cred-a is weighted so heavily that the engine's fresh pick lands
	// on it — the rate-limited credential MUST be the one exercised.
	if err := h.mgr.AddModel(models.ModelConfig{
		ID: "m-int", Name: "M Int", Enabled: true, Kind: models.KindChat,
		Internal: true, InternalModel: "glm-5",
		Credentials: []models.CredentialRef{
			{CredentialID: "cred-a", Weight: 1000, Position: 0},
			{CredentialID: "cred-b", Weight: 1, Position: 1},
		},
	}); err != nil {
		t.Fatalf("AddModel(m-int): %v", err)
	}
	if err := h.mgr.AddModel(models.ModelConfig{
		ID: "g-shadow", Name: "G Shadow", Enabled: true,
		Kind: models.KindGroup, Internal: true,
		ExcludeFromUltimateSwitching: true,
		Members:                      []models.GroupMember{{ModelID: "m-int", Weight: 1}},
		ShadowModelID:                "m-int",
	}); err != nil {
		t.Fatalf("AddModel(g-shadow): %v", err)
	}

	r := httptest.NewRecorder()
	h.h.HandleChatCompletions(r, makeChatRequest(t, map[string]interface{}{
		"model":    "m-int",
		"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
	}))
	if r.Code != http.StatusOK {
		t.Fatalf("self-shadow credential failover: expected 200 (the member's own cred-LB must absorb the 429); got %d body=%s", r.Code, r.Body.String())
	}
	if atomic.LoadInt32(&keyAHits) == 0 {
		t.Fatalf("self-shadow credential failover: the rate-limited credential was never exercised (keyAHits=0) — the test would pass trivially")
	}
}

// shadowFixAssertNoRaceFailure asserts that no attempt failed during
// the request — the race-outcome assertion (the HTTP status alone can
// be masked by a fallback serving the request).
func shadowFixAssertNoRaceFailure(t *testing.T, rec *am29EventRecorder) {
	t.Helper()
	// The bus delivers asynchronously — give the recorder a bounded
	// settle window before asserting the ABSENCE of a failure event.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range rec.snapshot() {
			if e == "race_request_failed" {
				t.Fatalf("a race attempt failed (race_request_failed) — the member's own attempt did not serve")
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
}
