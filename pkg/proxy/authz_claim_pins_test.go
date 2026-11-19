package proxy

// THE COMMISSION-NAMED OBLIGATION PINS — the six proxy pins of the
// AUTH COMMISSION (token permission model: `allowed_groups` + reach-based
// enforcement), landed in a TEST-ONLY lane. Every name below is the FULL
// literal symbol the corpus enumerates, so the pin set is copy-pasteable
// into the gate:
//
//  1. TestChat_FallbackPositionGroup_NoPhantomPick          — PD-7 / PD-12
//     (plan.md §4.3 PD-7, §8.2a PD-12, §8.4 T1)
//  2. TestChat_ShadowClaimed_403QuotesClaimedString         — PD-9 (plan.md §2.4a, §9.1)
//  3. TestChat_ShadowClaimed_AliasOnlyToken_403             — PD-9 STRICT (Ruling 3, plan.md §2.4 / §2.4a)
//  4. TestAnthropic_ShadowClaimed_AliasOnlyToken_403        — PD-9 STRICT, anthropic lane (PD-14 parity)
//  5. TestChat_ShadowClaimed_GroupGrant_AdmitsClaimedRequest — PD-9 STRICT, the other half (ADR D1)
//  6. TestChainGroupEntry_NeverAdmitted_No502               — PD-15 / leader PD-10 (plan.md §8.2a A10, §17.3a)
//
// CORPUS NOTE (reported, not papered over): plan.md §13.1a "Reconciliation —
// speculative names from earlier passes" recorded pins 2-5 as NOT LANDED and
// pins 1/6 as EXCLUDED (covered by other names / a documented limitation).
// The leader re-commissioned all six as canonical obligation pins for this
// test-only lane; where the earlier phase2-plan text and the landed code
// disagree (pin 6's premise), the pin asserts the LANDED behaviour and says so
// in-line rather than altering production code.
//
// THE HARNESS is the PRODUCTION shape (real SQLite store → DB ModelsManager →
// modelscache decorator → real token store → real Handler), with ONE upstream
// server + credential PER MODEL so "member X was never attempted" is observable
// PER MODEL (a shared stub could not distinguish members — they all carry the
// same InternalModel on the wire). Every 403/exclusion pin asserts an EXPLICIT
// call counter, never only the status code.

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
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/authz"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/modelscache"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
)

// ─────────────────────────────────────────────────────────────────────────────
// Harness — production wiring, one upstream per model.
// ─────────────────────────────────────────────────────────────────────────────

type claimEnv struct {
	t      *testing.T
	mgr    *database.ModelsManager
	models *modelscache.CachedModelsConfig
	h      *Handler
	bus    *events.Bus
	db     *sql.DB
	st     *database.Store
	ups    map[string]*httptest.Server
	calls  map[string]*int32
}

func (e *claimEnv) close() {
	if e.models != nil {
		e.models.Stop()
	}
	for _, up := range e.ups {
		up.Close()
	}
	e.mgr.Close()
	e.st.Close()
}

// hits is the per-model upstream call counter — the zero-attempt proof.
func (e *claimEnv) hits(modelID string) int {
	c, ok := e.calls[modelID]
	if !ok {
		return 0
	}
	return int(atomic.LoadInt32(c))
}

func (e *claimEnv) totalHits() int {
	total := 0
	for id := range e.calls {
		total += e.hits(id)
	}
	return total
}

// newClaimEnv builds the production chain with a claimable group, a plain
// group, and independent upstreams. Models named in failing get 500 upstreams.
//
//	pin-primary         chat  ← cred-pin-primary         (healthy)
//	pin-member          chat  ← cred-pin-member          (member of pin-shadow-group)
//	pin-alias           chat  ← cred-pin-alias           (the CLAIM TARGET of pin-shadow-group)
//	pin-shadow-group    group, shadow_model_id=pin-alias, members=[pin-member]
//	pin-chain-primary   chat  ← cred-pin-chain-primary, FallbackChain=[pin-plain-group]
//	pin-chain-member    chat  ← cred-pin-chain-member     (member of pin-plain-group)
//	pin-plain-group     group, no claim, members=[pin-chain-member]
func newClaimEnv(t *testing.T, failing ...string) *claimEnv {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("RACE_RETRY_ENABLED", "true")
	t.Setenv("DATABASE_URL", "sqlite:"+filepath.Join(tmp, "claimpins.db"))

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

	dec, err := modelscache.NewCachedModelsConfig(mgr, modelscache.Options{})
	if err != nil {
		mgr.Close()
		st.Close()
		t.Fatalf("NewCachedModelsConfig: %v", err)
	}

	failSet := make(map[string]bool, len(failing))
	for _, id := range failing {
		failSet[id] = true
	}
	env := &claimEnv{t: t, mgr: mgr, models: dec, bus: bus, db: st.DB, st: st,
		ups: map[string]*httptest.Server{}, calls: map[string]*int32{}}

	// addModel creates the model's OWN counting upstream + credential.
	addModel := func(m models.ModelConfig) {
		status := http.StatusOK
		if failSet[m.ID] {
			status = http.StatusInternalServerError
		}
		counter := int32(0)
		up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&counter, 1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			if status < 400 {
				_, _ = w.Write([]byte(stubUpstreamBody("stub-model", "hi")))
			} else {
				_, _ = w.Write([]byte(`{"error":{"message":"stub failure","type":"server_error"}}`))
			}
		}))
		credID := "cred-" + m.ID
		if err := dec.AddCredential(models.CredentialConfig{
			ID: credID, Provider: "openai", APIKey: "k", BaseURL: up.URL,
		}); err != nil {
			up.Close()
			env.close()
			t.Fatalf("AddCredential(%s): %v", credID, err)
		}
		m.Credentials = models.TestRefs(credID)
		if err := dec.AddModel(m); err != nil {
			up.Close()
			env.close()
			t.Fatalf("AddModel(%s): %v", m.ID, err)
		}
		env.ups[m.ID] = up
		env.calls[m.ID] = &counter
	}

	chat := func(id string, fallback []string) {
		addModel(models.ModelConfig{
			ID: id, Name: id, Enabled: true, Kind: models.KindChat, Internal: true,
			InternalModel: "stub-model", FallbackChain: fallback,
		})
	}
	chat("pin-primary", nil)
	chat("pin-member", nil)
	chat("pin-alias", nil)
	chat("pin-chain-primary", []string{"pin-plain-group"})
	chat("pin-chain-member", nil)

	// The shadow-claiming group: ENABLED + shadow_model_id makes the
	// claims-first resolver redirect "pin-alias" → this GROUP row
	// (modelscache/models.go GetModel claims-first; claims index rebuilt on
	// every write-through mutation).
	addModel(models.ModelConfig{
		ID: "pin-shadow-group", Name: "Pin Shadow Group", Enabled: true,
		Kind: models.KindGroup, Internal: true, ShadowModelID: "pin-alias",
		Members: []models.GroupMember{{ModelID: "pin-member", Weight: 1}},
	})
	// The plain (no-claim) group used by the chain-entry / phantom-pick pins.
	addModel(models.ModelConfig{
		ID: "pin-plain-group", Name: "Pin Plain Group", Enabled: true,
		Kind: models.KindGroup, Internal: true,
		Members: []models.GroupMember{{ModelID: "pin-chain-member", Weight: 1}},
	})

	tokenStore := auth.NewTokenStore(st.DB, st.Dialect)
	mgrCfg := newTestManagerWithConfig(t, "http://unused")
	reqStore := store.NewRequestStore(100, store.WithMaxBytes(256<<20))
	env.h = NewHandler(&Config{ConfigMgr: mgrCfg, ModelsConfig: dec}, bus, reqStore, nil, tokenStore, nil, mgr.Engine(), nil)
	return env
}

// mint creates a token with a FULL write of both axes (nil slice = ABSENT).
func (e *claimEnv) mint(name string, modelsAxis, groupsAxis []string) string {
	e.t.Helper()
	plaintext, _, err := auth.NewTokenStore(e.db, database.SQLite).CreateToken(
		context.Background(), name, nil, "claim-pin-test", auth.FullPermission(modelsAxis, groupsAxis))
	if err != nil {
		e.t.Fatalf("CreateToken(%s): %v", name, err)
	}
	return plaintext
}

func (e *claimEnv) postChat(token, model string) *httptest.ResponseRecorder {
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

func (e *claimEnv) postAnthropic(token, model string) *httptest.ResponseRecorder {
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

// scopeOf resolves the scope a token with the given grants would carry,
// through the SAME resolver the lanes use and the SAME in-memory membership
// accessor — so list-level assertions exercise the production predicate.
func (e *claimEnv) scopeOf(modelsAxis, groupsAxis []string) *authz.Scope {
	return authz.Resolve(&auth.AuthToken{AllowedModels: modelsAxis, AllowedGroups: groupsAxis},
		func(groupID string) []string { return e.models.GroupMembers(groupID) })
}

// claimChatLog returns the request log the handler stored for the served model.
func (e *claimEnv) claimChatLog(model string) *store.RequestLog {
	e.t.Helper()
	for _, log := range e.h.store.List() {
		if log.Model == model {
			return log
		}
	}
	return nil
}

// decodeError403 decodes the house 403 envelope.
type error403Body struct {
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
		Model   string `json:"model"`
		Code    string `json:"code"`
	} `json:"error"`
}

// assertClaimDenied403 asserts the 403 access_denied envelope quotes wantModel
// (the CLIENT-REQUESTED claimed string), and that the body leaks NO occurrence
// of forbiddenSubstr (the canonical group id — PD-9: a body carrying the
// canonical id would disclose the claim topology).
func assertClaimDenied403(t *testing.T, rec *httptest.ResponseRecorder, wantModel, forbiddenSubstr string) {
	t.Helper()
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	var body error403Body
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode 403 body (%s): %v", rec.Body.String(), err)
	}
	if body.Error.Type != "access_denied" {
		t.Errorf("error.type = %q, want %q (body: %s)", body.Error.Type, "access_denied", rec.Body.String())
	}
	if body.Error.Model != wantModel {
		t.Errorf("error.model = %q, want %q — the CLIENT-REQUESTED claimed string, never the canonical resolved id (PD-9)", body.Error.Model, wantModel)
	}
	if body.Error.Code != "" {
		t.Errorf("error.code = %q, want empty — the 403 carries no code field (plan §9.3)", body.Error.Code)
	}
	if !strings.Contains(body.Error.Message, "model not allowed for this token") {
		t.Errorf("error.message = %q, want the house wording \"model not allowed for this token\" (pkg/proxy/handler.go:526-536 sendModelNotAllowedError)", body.Error.Message)
	}
	if forbiddenSubstr != "" && strings.Contains(rec.Body.String(), forbiddenSubstr) {
		t.Errorf("the 403 body leaks the canonical id %q (%s) — the claim topology must never be disclosed (PD-9)", forbiddenSubstr, rec.Body.String())
	}
}

// emptyIDGroupEvents returns every group-family event whose group_id is empty.
// The phantom pick (PD-7/PD-12) used to emit exactly these.
func emptyIDGroupEvents(evs []events.Event) []events.Event {
	out := make([]events.Event, 0)
	for _, ev := range evs {
		if !strings.HasPrefix(ev.Type, "group_") &&
			ev.Type != "model_member_selected" &&
			ev.Type != "model_group_binding_dropped" &&
			ev.Type != "model.group.members.changed" {
			continue
		}
		data, ok := ev.Data.(map[string]interface{})
		if !ok {
			continue
		}
		if gid, ok2 := data["group_id"].(string); ok2 && gid == "" {
			out = append(out, ev)
		}
	}
	return out
}

// claimPinsSettle polls the bus deterministically until the terminal event for
// the request appears (or a 5s deadline expires), so asynchronous race-goroutine
// event publishes have landed before the caller drains. Match is on event type
// plus a request-specific predicate (model id) so concurrent tests cannot
// satisfy each other's wait. The caller still runs the vacuous-empty guard.
func claimPinsSettle(bus *events.Bus, terminalType string, match func(events.Event) bool) {
	deadline := time.Now().Add(5 * time.Second)
	for {
		evs := drainAllEvents(bus)
		for _, ev := range evs {
			if ev.Type == terminalType && match(ev) {
				return
			}
		}
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Pin 1 — TestChat_FallbackPositionGroup_NoPhantomPick
// ─────────────────────────────────────────────────────────────────────────────

// TestChat_FallbackPositionGroup_NoPhantomPick pins PD-7 / PD-12
// (plan.md §4.3 PD-7; §8.2a PD-12; §8.4 T1) at the chat lane:
//
//	A fallback-position group that is dropped for SCOPE must yield NO
//	phantom pick — an empty-group-id pick would trip the group re-entry
//	path, write groupTriedMembers[""], publish empty-id group_* events
//	and blank RouteModel on a perfectly healthy non-group request.
//
// Shape: the request names a plain, healthy primary whose FallbackChain
// contains a group whose members are ALL outside the token's reach.
// The rewrite drops the fallback entry (idx != 0), so the outcome carries
// pick == nil and no failed-group id (a dropped fallback is not the
// request's failure); the healthy primary still serves 200.
func TestChat_FallbackPositionGroup_NoPhantomPick(t *testing.T) {
	env := newClaimEnv(t)
	defer env.close()

	// The token grants ONLY the primary ⇒ the fallback group's member
	// (pin-chain-member) is outside the reach.
	tok := env.mint("phantom-pick", []string{"pin-primary"}, nil)

	// SEAM ASSERTION (the corpus contract: "outcome has pick == nil and
	// GroupID == ''"): drive the production helper directly with the exact
	// list the chat lane builds ([resolved.ID] + FallbackChain).
	conf := &ConfigSnapshot{ModelsConfig: env.models}
	scope := env.scopeOf([]string{"pin-primary"}, nil)
	outcome := env.h.applyGroupRewrite(conf, []string{"pin-primary", "pin-plain-group"}, "phantom-conv", scope)
	if outcome.pick != nil {
		t.Fatalf("applyGroupRewrite minted a pick %+v for a scope-dropped FALLBACK-position group — PD-7/PD-12 require pick == nil (GroupID must not exist)", *outcome.pick)
	}
	if outcome.failGroupID != "" {
		t.Errorf("outcome.failGroupID = %q, want \"\" — a fallback the token cannot use is not the request's failure (PD-12 idx!=0 rule)", outcome.failGroupID)
	}
	if len(outcome.list) != 1 || outcome.list[0] != "pin-primary" {
		t.Errorf("outcome.list = %v, want [pin-primary] — the dropped fallback entry must not survive into the execution list", outcome.list)
	}

	// END-TO-END: the healthy primary serves 200.
	rec := env.postChat(tok, "pin-primary")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — the healthy primary must still serve (body: %s)", rec.Code, rec.Body.String())
	}
	if got := env.hits("pin-primary"); got < 1 {
		t.Errorf("pin-primary upstream calls = %d, want >= 1 — the pin is vacuous unless the primary served", got)
	}
	// ZERO-ATTEMPT PROOF: the dropped group's member was never attempted.
	if got := env.hits("pin-chain-member"); got != 0 {
		t.Errorf("pin-chain-member upstream calls = %d, want 0 — an out-of-reach fallback member must never be attempted", got)
	}

	// NO EMPTY-ID GROUP EVENT (the phantom pick's signature).
	claimPinsSettle(env.bus, "request_completed", func(ev events.Event) bool {
		data, _ := ev.Data.(map[string]interface{})
		m, _ := data["model"].(string)
		return m == "pin-primary"
	})
	evs := drainAllEvents(env.bus)
	if len(evs) == 0 {
		t.Fatal("no events captured — the empty-id event assertion would be vacuous")
	}
	if bad := emptyIDGroupEvents(evs); len(bad) > 0 {
		for _, ev := range bad {
			t.Errorf("a %q event carries an EMPTY group id %v — a phantom pick emitted it (PD-7/PD-12)", ev.Type, ev.Data)
		}
	}

	// ROUTEMODEL NOT BLANKED: no group member was stamped onto this plain
	// primary serve (the phantom pick would have assigned RouteModel =
	// pick.MemberID == "" and opened the re-entry path that blanks it).
	log := env.claimChatLog("pin-primary")
	if log == nil {
		t.Fatal("no stored request log for pin-primary")
	}
	if log.RouteModel != "" {
		t.Errorf("request log RouteModel = %q, want \"\" — a healthy non-group serve must not carry a group-member route stamp", log.RouteModel)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Pin 2 — TestChat_ShadowClaimed_403QuotesClaimedString
// ─────────────────────────────────────────────────────────────────────────────

// TestChat_ShadowClaimed_403QuotesClaimedString pins PD-9 (plan.md §2.4a,
// §9.1): a claimed/alias id resolves CLAIMS-FIRST to the claiming group
// row; a token granting neither the claimed id nor the group is denied at
// the entry with 403 access_denied whose "model" is the CLIENT-REQUESTED
// claimed string — never the canonical group id — and ZERO upstream
// attempts occur.
func TestChat_ShadowClaimed_403QuotesClaimedString(t *testing.T) {
	env := newClaimEnv(t)
	defer env.close()

	// Grants a MEMBER of the claiming group but neither the claim alias
	// nor the group itself: the group's reach is not implied by a member
	// grant (D4/D5).
	tok := env.mint("claimed-denied", []string{"pin-member"}, nil)
	rec := env.postChat(tok, "pin-alias")

	assertClaimDenied403(t, rec, "pin-alias", "pin-shadow-group")
	if got := env.totalHits(); got != 0 {
		t.Errorf("upstream calls = %d, want 0 — a denied request must never reach an upstream", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Pin 3 — TestChat_ShadowClaimed_AliasOnlyToken_403
// ─────────────────────────────────────────────────────────────────────────────

// TestChat_ShadowClaimed_AliasOnlyToken_403 is the anti-union pin (Ruling 3;
// plan.md §2.4 / §2.4a; PD-9 STRICT): a token whose grants contain ONLY the
// claimed alias string — not the canonical group row id — does NOT pass the
// resolved-only gate. The request resolves claims-first to the GROUP row, so
// the gate authorizes the RESOLVED target ("pin-shadow-group"), which the
// alias-only token does not grant ⇒ 403 quoting the alias, ZERO upstream
// attempts. A union gate would have admitted it via Allows(alias) and served
// the group's members — implicit reach.
func TestChat_ShadowClaimed_AliasOnlyToken_403(t *testing.T) {
	env := newClaimEnv(t)
	defer env.close()

	// ONLY the alias is granted (the alias row exists as a real chat model,
	// but the claim makes the request resolve to the group).
	tok := env.mint("alias-only", []string{"pin-alias"}, nil)
	rec := env.postChat(tok, "pin-alias")

	assertClaimDenied403(t, rec, "pin-alias", "pin-shadow-group")
	if got := env.totalHits(); got != 0 {
		t.Errorf("upstream calls = %d, want 0 — the alias-only grant must not admit any member attempt", got)
	}

	// No empty-id group event either (the denial precedes all group work).
	claimPinsSettle(env.bus, "model_not_allowed", func(ev events.Event) bool {
		data, _ := ev.Data.(map[string]interface{})
		m, _ := data["model"].(string)
		return m == "pin-alias"
	})
	if bad := emptyIDGroupEvents(drainAllEvents(env.bus)); len(bad) > 0 {
		for _, ev := range bad {
			t.Errorf("a %q event carries an EMPTY group id %v", ev.Type, ev.Data)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Pin 4 — TestAnthropic_ShadowClaimed_AliasOnlyToken_403
// ─────────────────────────────────────────────────────────────────────────────

// TestAnthropic_ShadowClaimed_AliasOnlyToken_403 is the anthropic-lane twin
// of pin 3 (handler_anthropic.go's hoisted scope gate; PD-14 parity): a token
// granting ONLY the claimed alias string is denied at the entry with the
// chat-shaped 403 (byte-identical envelope), quoting the alias, zero upstream
// attempts.
func TestAnthropic_ShadowClaimed_AliasOnlyToken_403(t *testing.T) {
	env := newClaimEnv(t)
	defer env.close()

	tok := env.mint("anthropic-alias-only", []string{"pin-alias"}, nil)
	rec := env.postAnthropic(tok, "pin-alias")

	assertClaimDenied403(t, rec, "pin-alias", "pin-shadow-group")
	if got := env.totalHits(); got != 0 {
		t.Errorf("upstream calls = %d, want 0 — the alias-only grant must not admit any member attempt", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Pin 5 — TestChat_ShadowClaimed_GroupGrant_AdmitsClaimedRequest
// ─────────────────────────────────────────────────────────────────────────────

// TestChat_ShadowClaimed_GroupGrant_AdmitsClaimedRequest pins the OTHER half
// of PD-9 (ADR D1): the resolved target is the authorization unit — so a
// token that grants the canonical group (allowed_groups: [<group_id>]) IS
// admitted for a request naming that group's claimed alias, and the group
// serves one of its members (mock upstream, 200).
func TestChat_ShadowClaimed_GroupGrant_AdmitsClaimedRequest(t *testing.T) {
	env := newClaimEnv(t)
	defer env.close()

	tok := env.mint("group-grant", nil, []string{"pin-shadow-group"})
	rec := env.postChat(tok, "pin-alias")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — a group grant must admit the request naming the group's claimed alias (body: %s)", rec.Code, rec.Body.String())
	}
	// CLAIMS-FIRST REDIRECT SEAM (F3): the production resolver through the
	// decorator must redirect the claimed alias to the claiming group row.
	if gm := env.models.GetModel("pin-alias"); gm == nil || gm.ID != "pin-shadow-group" {
		t.Errorf("GetModel(\"pin-alias\") = %+v, want group row ID == \"pin-shadow-group\" — claims-first redirect seam regressed", gm)
	}
	// THE SERVED PATH: the group's member is what actually ran upstream.
	if got := env.hits("pin-member"); got < 1 {
		t.Errorf("pin-member upstream calls = %d, want >= 1 — the admitted request must be served by a member of the granted group", got)
	}
	if got := env.hits("pin-alias"); got != 0 {
		t.Errorf("pin-alias upstream calls = %d, want 0 — the claim target row must never be attempted (the claim redirects to the group)", got)
	}
	// Metering attribution: the served member differs from the client string.
	log := env.claimChatLog("pin-alias")
	if log == nil {
		t.Fatal("no stored request log for pin-alias")
	}
	if log.RouteModel != "pin-member" {
		t.Errorf("request log RouteModel = %q, want %q — the served group member must be the metered route", log.RouteModel, "pin-member")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Pin 6 — TestChainGroupEntry_NeverAdmitted_No502
// ─────────────────────────────────────────────────────────────────────────────

// TestChainGroupEntry_NeverAdmitted_No502 pins PD-15 / the leader's PD-10
// ruling (plan.md §8.2a A10; §17.3a): a group id inside a model's
// FallbackChain is NEVER admitted as a raw execution candidate.
//
// LANDED BEHAVIOUR (verified, and the reason this pin is green): the entry
// model's chain entries are part of the modelList handed to the single-pass
// rewrite, so a group-kind entry is EXPANDED into its picked member
// (store_group_rewrite.go rewriteGroupEntries) — the group ROW itself never
// reaches credential resolution (a credential-less group row would resolve
// ok=false and turn the request into a runtime 502). PD-10's documented
// limitation concerns a group id appended INSIDE a group member's own chain
// at rewrite time (appended to the output list and never re-scanned) — that
// shape is a known pre-existing wart and is deliberately NOT pinned here.
//
// Shape: primary's FallbackChain = ["pin-plain-group"]; the token's reach
// contains the group (allowed_groups). The primary fails (500) so the chain
// actually advances: the served candidate is the group's MEMBER, and the
// request ends 200 — never the 502 a raw group-row attempt would produce.
func TestChainGroupEntry_NeverAdmitted_No502(t *testing.T) {
	env := newClaimEnv(t, "pin-chain-primary")
	defer env.close()

	tok := env.mint("chain-group-entry", []string{"pin-chain-primary"}, []string{"pin-plain-group"})

	// SEAM PROOF: the rewrite replaces the group ENTRY with its member —
	// the raw group id is never an execution candidate.
	scope := env.scopeOf([]string{"pin-chain-primary"}, []string{"pin-plain-group"})
	res := env.models.RewriteGroupEntriesScoped(
		[]string{"pin-chain-primary", "pin-plain-group"}, "chain-group-conv", scope)
	for _, id := range res.ModelList {
		if id == "pin-plain-group" {
			t.Errorf("the execution list carries the raw group id %q (%v) — a group-kind chain entry must never be admitted as a candidate", id, res.ModelList)
		}
		if id == "" {
			t.Errorf("the execution list carries an empty id (%v)", res.ModelList)
		}
	}
	if res.GroupID != "pin-plain-group" || res.MemberID != "pin-chain-member" {
		t.Fatalf("rewrite pick = (%q, %q), want (pin-plain-group, pin-chain-member) — the chain group must expand to its member", res.GroupID, res.MemberID)
	}

	// END-TO-END: the failing primary advances the chain, the expanded member
	// serves, and the request does NOT 502.
	rec := env.postChat(tok, "pin-chain-primary")
	if rec.Code == http.StatusBadGateway {
		t.Fatalf("status = 502 — a group-kind chain entry was admitted as a raw candidate and reached credential resolution (the PD-15 wart this pin forbids); body: %s", rec.Body.String())
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 from the expanded chain member (body: %s)", rec.Code, rec.Body.String())
	}
	if got := env.hits("pin-chain-primary"); got < 1 {
		t.Errorf("pin-chain-primary upstream calls = %d, want >= 1 — the primary must have been attempted for the chain to advance", got)
	}
	if got := env.hits("pin-chain-member"); got < 1 {
		t.Errorf("pin-chain-member upstream calls = %d, want >= 1 — the expanded group member must be the fallback candidate that served", got)
	}

	// The execution list the coordinator actually raced never contained the
	// raw group id (observed through the production race_started event).
	claimPinsSettle(env.bus, "request_completed", func(ev events.Event) bool {
		data, _ := ev.Data.(map[string]interface{})
		m, _ := data["model"].(string)
		return m == "pin-chain-member"
	})
	var sawRaceStarted bool
	for _, ev := range drainAllEvents(env.bus) {
		if ev.Type != "race_started" {
			continue
		}
		sawRaceStarted = true
		data, ok := ev.Data.(map[string]interface{})
		if !ok {
			continue
		}
		modelsList, _ := data["models"].([]string)
		for _, id := range modelsList {
			if id == "pin-plain-group" {
				t.Errorf("race_started models list carries the raw group id %q (%v) — the group entry must be expanded, never admitted", id, modelsList)
			}
		}
	}
	if !sawRaceStarted {
		t.Error("no race_started event captured — the execution-list assertion would be vacuous")
	}
}
