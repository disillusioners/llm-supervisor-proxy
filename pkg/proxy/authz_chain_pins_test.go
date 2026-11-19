package proxy

// The AUTH COMMISSION's chain / re-entry / per-member exclusion pins
// (phase2-plan.md §5.2 P-G7 / P-G8 / P-G9 / P-G10 / P-G11).
//
// THE HARNESS gives each member its OWN upstream server and credential,
// so "member X's call counter == 0" is observable PER MEMBER — a shared
// stub could not distinguish which member an attempt reached (both
// members carry the same InternalModel on the wire).

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/authz"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/modelscache"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
)

type chainEnv struct {
	t      *testing.T
	mgr    *database.ModelsManager
	models *modelscache.CachedModelsConfig
	h      *Handler
	bus    *events.Bus
	db     *sql.DB
	st     *database.Store
	upA    *httptest.Server
	upB    *httptest.Server
	callsA *int32
	callsB *int32
}

func (e *chainEnv) close() {
	if e.models != nil {
		e.models.Stop()
	}
	e.upA.Close()
	e.upB.Close()
	e.mgr.Close()
	e.st.Close()
}

func (e *chainEnv) callsFor(side string) int {
	if side == "B" {
		return int(atomic.LoadInt32(e.callsB))
	}
	return int(atomic.LoadInt32(e.callsA))
}

// newChainEnv builds the production shape (SQLite store + DB manager +
// modelscache decorator + real token store) with TWO stub upstreams:
//
//	upstream A ← credential chain-cred-a ← model-a
//	upstream B ← credential chain-cred-b ← model-b
//
// statuses lets a test fail one side (the re-entry pins fail A).
func newChainEnv(t *testing.T, statusA, statusB int) *chainEnv {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("RACE_RETRY_ENABLED", "true")
	t.Setenv("DATABASE_URL", "sqlite:"+filepath.Join(tmp, "chain.db"))

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

	callsA, callsB := int32(0), int32(0)
	newStub := func(counter *int32, status int) *httptest.Server { //nolint:unparam
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(counter, 1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			if status < 400 {
				_, _ = w.Write([]byte(stubUpstreamBody("stub-model", "hi")))
			} else {
				_, _ = w.Write([]byte(`{"error":{"message":"stub failure","type":"server_error"}}`))
			}
		}))
	}
	upA := newStub(&callsA, statusA)
	upB := newStub(&callsB, statusB)

	dec, err := modelscache.NewCachedModelsConfig(mgr, modelscache.Options{})
	if err != nil {
		upA.Close()
		upB.Close()
		mgr.Close()
		st.Close()
		t.Fatalf("NewCachedModelsConfig: %v", err)
	}
	if err := dec.AddCredential(models.CredentialConfig{ID: "chain-cred-a", Provider: "openai", APIKey: "k-a", BaseURL: upA.URL}); err != nil {
		t.Fatalf("AddCredential(a): %v", err)
	}
	if err := dec.AddCredential(models.CredentialConfig{ID: "chain-cred-b", Provider: "openai", APIKey: "k-b", BaseURL: upB.URL}); err != nil {
		t.Fatalf("AddCredential(b): %v", err)
	}
	for _, m := range []models.ModelConfig{
		{ID: "model-a", Name: "Model A", Enabled: true, Kind: models.KindChat, Internal: true, InternalModel: "stub-model", Credentials: models.TestRefs("chain-cred-a")},
		{ID: "model-b", Name: "Model B", Enabled: true, Kind: models.KindChat, Internal: true, InternalModel: "stub-model", Credentials: models.TestRefs("chain-cred-b")},
	} {
		if err := dec.AddModel(m); err != nil {
			t.Fatalf("AddModel(%s): %v", m.ID, err)
		}
	}

	tokenStore := auth.NewTokenStore(st.DB, st.Dialect)
	mgrCfg := newTestManagerWithConfig(t, upA.URL)
	reqStore := store.NewRequestStore(100, store.WithMaxBytes(256<<20))
	h := NewHandler(&Config{ConfigMgr: mgrCfg, ModelsConfig: dec}, bus, reqStore, nil, tokenStore, nil, mgr.Engine(), nil)

	return &chainEnv{t: t, mgr: mgr, models: dec, h: h, bus: bus, db: st.DB, st: st, upA: upA, upB: upB, callsA: &callsA, callsB: &callsB}
}

func (e *chainEnv) mintToken(name string, models, groups []string) string {
	e.t.Helper()
	plaintext, _, err := auth.NewTokenStore(e.db, database.SQLite).CreateToken(
		context.Background(), name, nil, "chain-test", auth.FullPermission(models, groups))
	if err != nil {
		e.t.Fatalf("CreateToken(%s): %v", name, err)
	}
	return plaintext
}

func (e *chainEnv) postChat(token, model string) *httptest.ResponseRecorder {
	e.t.Helper()
	body, _ := json.Marshal(map[string]interface{}{
		"model":    model,
		"messages": []map[string]string{{"role": "user", "content": "hello"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.h.HandleChatCompletions(rec, req)
	return rec
}

func (e *chainEnv) postAnthropic(token, model string) *httptest.ResponseRecorder {
	e.t.Helper()
	body, _ := json.Marshal(map[string]interface{}{
		"model":      model,
		"max_tokens": 16,
		"messages":   []map[string]string{{"role": "user", "content": "hello"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.h.HandleAnthropicMessages(rec, req)
	return rec
}

// chainReachScope resolves the scope a token with the given grants would
// carry, using the SAME resolver the lanes use and the SAME in-memory
// membership accessor — so the list-level assertions below are the
// production predicate, not a test twin.
func (e *chainEnv) chainReachScope(modelsAxis, groupsAxis []string) *authz.Scope {
	return authz.Resolve(&auth.AuthToken{AllowedModels: modelsAxis, AllowedGroups: groupsAxis}, e.membersOf())
}

func (e *chainEnv) membersOf() func(string) []string {
	return func(groupID string) []string {
		return e.models.GroupMembers(groupID)
	}
}

// assertExecutionListExcludes drives the production reach filter over the
// execution list the lane would build from resolved.ID + FallbackChain,
// and asserts every dropped id is gone.
func (e *chainEnv) assertExecutionListExcludes(scope *authz.Scope, head string, chain []string, mustDrop ...string) {
	e.t.Helper()
	list := append([]string{head}, chain...)
	filtered := e.h.applyReachFilter(scope, list)
	for _, dropped := range mustDrop {
		for _, id := range filtered {
			if id == dropped {
				e.t.Errorf("the execution list still carries %q after the reach filter (list=%v) — an out-of-reach candidate reached construction", dropped, filtered)
			}
		}
	}
	if len(filtered) == 0 {
		e.t.Fatalf("the filtered list is empty (%v → %v) — the head must be in reach for this pin to be meaningful", list, filtered)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// P-G8 — chat: an out-of-scope CHAIN entry is never spawned.
// ─────────────────────────────────────────────────────────────────────────────

// TestChat_ChainEntry_OutOfScope_NeverSpawned pins D5 at the chat lane:
// model A carries FallbackChain=[B]; the token grants ONLY A, so B (a
// chain target) is NOT granted. The request must be served by A while
// B's upstream counter stays at ZERO — a chain target is not inherited
// from the granted model.
func TestChat_ChainEntry_OutOfScope_NeverSpawned(t *testing.T) {
	env := newChainEnv(t, http.StatusOK, http.StatusOK)
	defer env.close()

	if err := env.models.UpdateModel("model-a", models.ModelConfig{
		ID: "model-a", Name: "Model A", Enabled: true, Kind: models.KindChat, Internal: true,
		InternalModel: "stub-model", Credentials: models.TestRefs("chain-cred-a"),
		FallbackChain: []string{"model-b"},
	}); err != nil {
		t.Fatalf("wire the chain: %v", err)
	}

	// D5: the chain targets of a granted model are NOT granted.
	tok := env.mintToken("chain-models-only", []string{"model-a"}, nil)
	rec := env.postChat(tok, "model-a")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — the GRANTED head must still serve (body: %s)", rec.Code, rec.Body.String())
	}

	// ZERO-UPSTREAM PROOF: B was never attempted.
	if got := env.callsFor("B"); got != 0 {
		t.Errorf("model-b upstream calls = %d, want 0 — an out-of-scope chain entry must never be spawned (P-G8)", got)
	}
	if got := env.callsFor("A"); got < 1 {
		t.Errorf("model-a upstream calls = %d, want >= 1 — the pin is vacuous unless the granted head served", got)
	}

	// LIST-LEVEL PROOF: the production reach filter drops B from the
	// execution list the lane builds ([resolved.ID] + FallbackChain).
	scope := env.chainReachScope([]string{"model-a"}, nil)
	env.assertExecutionListExcludes(scope, "model-a", []string{"model-b"}, "model-b")
}

// ─────────────────────────────────────────────────────────────────────────────
// P-G9 — anthropic: the same on the sequential lane.
// ─────────────────────────────────────────────────────────────────────────────

// TestAnthropic_ChainEntry_OutOfScope_NeverAttempted pins D5 on the
// Anthropic lane: the walk's list is reach-filtered at C2, so B is never
// attempted.
func TestAnthropic_ChainEntry_OutOfScope_NeverAttempted(t *testing.T) {
	env := newChainEnv(t, http.StatusOK, http.StatusOK)
	defer env.close()

	if err := env.models.UpdateModel("model-a", models.ModelConfig{
		ID: "model-a", Name: "Model A", Enabled: true, Kind: models.KindChat, Internal: true,
		InternalModel: "stub-model", Credentials: models.TestRefs("chain-cred-a"),
		FallbackChain: []string{"model-b"},
	}); err != nil {
		t.Fatalf("wire the chain: %v", err)
	}

	tok := env.mintToken("anthropic-chain-models-only", []string{"model-a"}, nil)
	rec := env.postAnthropic(tok, "model-a")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	// ZERO-UPSTREAM PROOF: B was never attempted on the sequential walk.
	if got := env.callsFor("B"); got != 0 {
		t.Errorf("model-b upstream calls = %d, want 0 — an out-of-scope chain entry must never be attempted (P-G9)", got)
	}
	if got := env.callsFor("A"); got < 1 {
		t.Errorf("model-a upstream calls = %d, want >= 1 — vacuous unless the head served", got)
	}

	scope := env.chainReachScope([]string{"model-a"}, nil)
	env.assertExecutionListExcludes(scope, "model-a", []string{"model-b"}, "model-b")
}

// ─────────────────────────────────────────────────────────────────────────────
// P-G7 / P-G9 per-member shape — one in-scope sibling serves, the
// excluded member is never picked.
// ─────────────────────────────────────────────────────────────────────────────

// TestChat_GroupMember_OutOfScope_NeverPicked_SiblingServes is the
// END-TO-END form of the per-member exclusion: a group of TWO members
// where the token reaches the group and exactly ONE member. The in-scope
// sibling serves 200; the excluded member's upstream counter stays at
// ZERO — the pick is scope-aware, not merely post-filtered.
func TestChat_GroupMember_OutOfScope_NeverPicked_SiblingServes(t *testing.T) {
	env := newChainEnv(t, http.StatusOK, http.StatusOK)
	defer env.close()

	if err := env.models.AddModel(models.ModelConfig{
		ID: "chain-group", Name: "Chain Group", Enabled: true, Kind: models.KindGroup, Internal: true,
		Members: []models.GroupMember{
			{ModelID: "model-a", Weight: 1},
			{ModelID: "model-b", Weight: 1},
		},
	}); err != nil {
		t.Fatalf("AddModel(group): %v", err)
	}

	// D4/D5: the group id grants ENTRY; only model-a is granted as a
	// member. model-b is therefore out of reach.
	tok := env.mintToken("sibling-serves", []string{"chain-group", "model-a"}, nil)
	rec := env.postChat(tok, "chain-group")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — the in-scope sibling must serve (body: %s)", rec.Code, rec.Body.String())
	}

	// ZERO-UPSTREAM PROOF: the excluded member was never picked.
	if got := env.callsFor("B"); got != 0 {
		t.Errorf("model-b upstream calls = %d, want 0 — an out-of-scope member must never be picked while an in-scope sibling serves (P-G7)", got)
	}
	if got := env.callsFor("A"); got < 1 {
		t.Errorf("model-a upstream calls = %d, want >= 1 — vacuous unless the sibling served", got)
	}

	// The scoped rewrite's pick and list agree and stay inside reach.
	scope := env.chainReachScope([]string{"chain-group", "model-a"}, nil)
	res := env.models.RewriteGroupEntriesScoped([]string{"chain-group"}, "sibling-serves-conv", scope)
	if res.MemberID != "" && res.MemberID != "model-a" {
		t.Errorf("the scoped pick = %q, want the only in-reach member %q", res.MemberID, "model-a")
	}
	for _, id := range res.ModelList {
		if id == "model-b" {
			t.Errorf("the execution list carries the out-of-scope member %q", id)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// P-G11 — re-entry: the re-offer / rebuild never yields an out-of-reach
// member.
// ─────────────────────────────────────────────────────────────────────────────

// seedReentryGroup wires the group {model-a, model-b} used by the
// re-entry pins (A fails terminally, B is out of reach).
func (e *chainEnv) seedReentryGroup(t *testing.T) {
	t.Helper()
	if err := e.models.AddModel(models.ModelConfig{
		ID: "chain-group", Name: "Chain Group", Enabled: true, Kind: models.KindGroup, Internal: true,
		Members: []models.GroupMember{
			{ModelID: "model-a", Weight: 1},
			{ModelID: "model-b", Weight: 1},
		},
	}); err != nil {
		t.Fatalf("AddModel(group): %v", err)
	}
}

// TestChat_Reentry_OutOfScopeTarget_NeverSpawned pins P-G11 on the chat
// lane: the in-reach member fails TERMINALLY, so the group re-entry runs
// — and the re-offer / rebuild must never yield the out-of-reach member.
// B's upstream counter stays at ZERO, and because an upstream attempt DID
// occur, the terminal verdict is the AVAILABILITY envelope (502), never a
// 403 (the attempt-count-keyed rule).
func TestChat_Reentry_OutOfScopeTarget_NeverSpawned(t *testing.T) {
	env := newChainEnv(t, http.StatusInternalServerError, http.StatusOK)
	defer env.close()
	env.seedReentryGroup(t)

	tok := env.mintToken("chat-reentry", []string{"chain-group", "model-a"}, nil)
	rec := env.postChat(tok, "chain-group")

	// An attempt happened (A), so the terminal path is AVAILABILITY: the
	// verdict is the availability envelope (this fixture's upstream 500 is
	// propagated as the response status), NEVER a scope denial.
	body := rec.Body.String()
	if rec.Code == http.StatusForbidden || strings.Contains(body, "access_denied") {
		t.Fatalf("status = %d body = %s — a re-entry that exhausted after a REAL attempt is an availability failure, never a 403 (the attempt-count-keyed rule)", rec.Code, body)
	}

	// ZERO-UPSTREAM PROOF: the out-of-reach member was never spawned by
	// the re-offer or the rebuild.
	if got := env.callsFor("B"); got != 0 {
		t.Errorf("model-b upstream calls = %d, want 0 — the re-entry must never yield an out-of-reach member (P-G11)", got)
	}
	if got := env.callsFor("A"); got < 1 {
		t.Errorf("model-a upstream calls = %d, want >= 1 — vacuous unless the terminal failure actually happened", got)
	}
}

// TestAnthropic_Reentry_OutOfScopeTarget_NeverAttempted pins P-G11 on
// the Anthropic lane: same shape, sequential walk.
func TestAnthropic_Reentry_OutOfScopeTarget_NeverAttempted(t *testing.T) {
	env := newChainEnv(t, http.StatusInternalServerError, http.StatusOK)
	defer env.close()
	env.seedReentryGroup(t)

	tok := env.mintToken("anthropic-reentry", []string{"chain-group", "model-a"}, nil)
	rec := env.postAnthropic(tok, "chain-group")

	// Same attempt-count-keyed rule on this lane: a real attempt happened,
	// so the verdict is the availability envelope, never a scope denial.
	body := rec.Body.String()
	if rec.Code == http.StatusForbidden || strings.Contains(body, "access_denied") {
		t.Fatalf("status = %d body = %s — a re-entry that exhausted after a REAL attempt is an availability failure, never a 403", rec.Code, body)
	}

	// ZERO-UPSTREAM PROOF.
	if got := env.callsFor("B"); got != 0 {
		t.Errorf("model-b upstream calls = %d, want 0 — the re-entry must never attempt an out-of-reach member (P-G11)", got)
	}
	if got := env.callsFor("A"); got < 1 {
		t.Errorf("model-a upstream calls = %d, want >= 1 — vacuous unless the terminal failure actually happened", got)
	}
}
