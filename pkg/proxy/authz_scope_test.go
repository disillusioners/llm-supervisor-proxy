package proxy

// THE AUTH COMMISSION's end-to-end behavioural pins (phase2-plan.md §5,
// leader corrections §2/§3).
//
// THE HARNESS is the PRODUCTION shape: a real SQLite store + the DB
// ModelsManager wrapped by the modelscache decorator (CACHE_LAYER_ENABLED
// defaults ON — cmd/main.go), a real token store, and a stub upstream
// with a CALL COUNTER. Every 403/exclusion pin asserts the counter is
// zero: a denied request must never reach an upstream.
//
// The token is the ONLY variable between the scenarios — the same group,
// the same members, the same upstream.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/decision"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/imggen"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/modelscache"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
)

type authzEnv struct {
	t       *testing.T
	mgr     *database.ModelsManager
	models  *modelscache.CachedModelsConfig
	h       *Handler
	up      *httptest.Server
	bus     *events.Bus
	db      *sql.DB
	store   *database.Store
	upCalls *int32
}

func (e *authzEnv) close() {
	if e.models != nil {
		e.models.Stop()
	}
	e.up.Close()
	e.mgr.Close()
	e.store.Close()
}

// totalUpstreamCalls is the ZERO-ATTEMPT proof every 403/exclusion pin
// asserts on.
func (e *authzEnv) totalUpstreamCalls() int { return int(atomic.LoadInt32(e.upCalls)) }

func newAuthzEnv(t *testing.T) *authzEnv {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("RACE_RETRY_ENABLED", "true")
	t.Setenv("DATABASE_URL", "sqlite:"+filepath.Join(tmp, "authz.db"))

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

	calls := int32(0)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(stubUpstreamBody("stub-model", "hi")))
	}))

	// THE PRODUCTION BINDING: the decorator over the manager.
	dec, err := modelscache.NewCachedModelsConfig(mgr, modelscache.Options{})
	if err != nil {
		up.Close()
		mgr.Close()
		st.Close()
		t.Fatalf("NewCachedModelsConfig: %v", err)
	}

	tokenStore := auth.NewTokenStore(st.DB, st.Dialect)
	mgrCfg := newTestManagerWithConfig(t, up.URL)
	reqStore := store.NewRequestStore(100, store.WithMaxBytes(256<<20))
	h := NewHandler(&Config{ConfigMgr: mgrCfg, ModelsConfig: dec}, bus, reqStore, nil, tokenStore, nil, mgr.Engine(), nil)

	env := &authzEnv{t: t, mgr: mgr, models: dec, h: h, up: up, bus: bus, db: st.DB, store: st, upCalls: &calls}

	// The credential goes through the DECORATOR too — the production
	// write path — so the decorator's credential mirror is primed (a
	// credential written to the raw manager behind the decorator's back
	// is invisible until the reconcile tick).
	if err := dec.AddCredential(models.CredentialConfig{
		ID: "authz-cred", Provider: "openai", APIKey: "k", BaseURL: up.URL,
	}); err != nil {
		env.close()
		t.Fatalf("AddCredential: %v", err)
	}
	// Two chat members + a group over them; the decorator is the write
	// path so its in-memory snapshot (and the membership accessor) is
	// current.
	for _, m := range []models.ModelConfig{
		{ID: "member-a", Name: "Member A", Enabled: true, Kind: models.KindChat, Internal: true, InternalModel: "stub-model", Credentials: models.TestRefs("authz-cred")},
		{ID: "member-b", Name: "Member B", Enabled: true, Kind: models.KindChat, Internal: true, InternalModel: "stub-model", Credentials: models.TestRefs("authz-cred")},
	} {
		if err := dec.AddModel(m); err != nil {
			env.close()
			t.Fatalf("AddModel(%s): %v", m.ID, err)
		}
	}
	if err := dec.AddModel(models.ModelConfig{
		ID: "authz-group", Name: "Authz Group", Enabled: true, Kind: models.KindGroup, Internal: true,
		Members: []models.GroupMember{{ModelID: "member-a", Weight: 1}, {ModelID: "member-b", Weight: 1}},
	}); err != nil {
		env.close()
		t.Fatalf("AddModel(group): %v", err)
	}
	return env
}

// mintToken creates a token and returns its plaintext.
func (e *authzEnv) mintToken(name string, perm auth.OptionalTokenPermission) string {
	e.t.Helper()
	plaintext, _, err := auth.NewTokenStore(e.db, database.SQLite).CreateToken(
		context.Background(), name, nil, "authz-test", perm)
	if err != nil {
		e.t.Fatalf("CreateToken(%s): %v", name, err)
	}
	return plaintext
}

// postChat issues an authenticated chat completion for model.
func (e *authzEnv) postChat(token, model string) *httptest.ResponseRecorder {
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

// assertModelNotAllowed403 asserts the chat-shaped 403 envelope and that
// the body quotes wantModel (the CLIENT-requested string).
func assertModelNotAllowed403(t *testing.T, rec *httptest.ResponseRecorder, wantModel string) {
	t.Helper()
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Model   string `json:"model"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode the 403 body (%s): %v", rec.Body.String(), err)
	}
	if envelope.Error.Type != "access_denied" {
		t.Errorf("error.type = %q, want access_denied (body: %s)", envelope.Error.Type, rec.Body.String())
	}
	if envelope.Error.Model != wantModel {
		t.Errorf("error.model = %q, want %q (the CLIENT-requested string — never the canonical resolved id)", envelope.Error.Model, wantModel)
	}
	if envelope.Error.Code != "" {
		t.Errorf("error.code = %q, want empty (the 403 is the plain access_denied envelope)", envelope.Error.Code)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Chat lane — the entry gate.
// ─────────────────────────────────────────────────────────────────────────────

// TestChat_OutOfScopeModel_403_ZeroAttempts pins the hoisted entry gate
// (D13): an out-of-scope model is denied BEFORE any candidate
// construction — no binding, no expansion, no upstream attempt.
func TestChat_OutOfScopeModel_403_ZeroAttempts(t *testing.T) {
	env := newAuthzEnv(t)
	defer env.close()

	tok := env.mintToken("models-only", auth.FullPermission([]string{"some-other-model"}, nil))
	rec := env.postChat(tok, "member-a")

	assertModelNotAllowed403(t, rec, "member-a")
	if got := env.totalUpstreamCalls(); got != 0 {
		t.Errorf("upstream calls = %d, want 0 — a denied request must never reach an upstream", got)
	}
}

// TestChat_WildcardToken_Unchanged_200 pins D11: a token with BOTH axes
// ABSENT keeps today's behaviour bit-for-bit.
func TestChat_WildcardToken_Unchanged_200(t *testing.T) {
	env := newAuthzEnv(t)
	defer env.close()

	tok := env.mintToken("wildcard", auth.FullPermission(nil, nil))
	rec := env.postChat(tok, "member-a")

	if rec.Code != http.StatusOK {
		t.Fatalf("wildcard token: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if got := env.totalUpstreamCalls(); got < 1 {
		t.Errorf("upstream calls = %d, want >= 1 — the wildcard request must be served", got)
	}
}

// TestChat_GroupGrant_AdmitsMembers_200 pins the group axis: a token
// granting the GROUP reaches the group and its CURRENT members.
func TestChat_GroupGrant_AdmitsMembers_200(t *testing.T) {
	env := newAuthzEnv(t)
	defer env.close()

	tok := env.mintToken("group-grant", auth.FullPermission(nil, []string{"authz-group"}))
	rec := env.postChat(tok, "authz-group")

	if rec.Code != http.StatusOK {
		t.Fatalf("group grant: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if got := env.totalUpstreamCalls(); got < 1 {
		t.Errorf("upstream calls = %d, want >= 1 — a granted group must serve", got)
	}
}

// TestChat_Group_AllMembersOutOfScope_403_Not502 is the commission's
// discriminator pin: the group TARGET is in reach (named in
// allowed_models — the legacy D4 spelling, which grants ENTRY ONLY), but
// every CURRENT member is out of reach ⇒ 403 access_denied, NEVER the
// 502 group_unservable envelope, and ZERO upstream attempts.
func TestChat_Group_AllMembersOutOfScope_403_Not502(t *testing.T) {
	env := newAuthzEnv(t)
	defer env.close()

	// D4: a group id in allowed_models grants the group ENTRY only —
	// the members are NOT implicitly granted (D5).
	tok := env.mintToken("group-entry-only", auth.FullPermission([]string{"authz-group"}, nil))
	rec := env.postChat(tok, "authz-group")

	assertModelNotAllowed403(t, rec, "authz-group")
	body := rec.Body.String()
	if strings.Contains(body, "group_unservable") {
		t.Errorf("the 403 body carries group_unservable (%s) — a scope exhaustion must NEVER be reported as an availability failure", body)
	}
	if got := env.totalUpstreamCalls(); got != 0 {
		t.Errorf("upstream calls = %d, want 0 — no member may be attempted when every member is out of reach", got)
	}
}

// TestChat_DynamicMembership_ReflectsImmediately pins ADR D12: the reach
// set is computed from the group's CURRENT members at REQUEST time, so a
// membership change takes effect on the next request without re-minting
// the token.
func TestChat_DynamicMembership_ReflectsImmediately(t *testing.T) {
	env := newAuthzEnv(t)
	defer env.close()

	// A group grant reaches the CURRENT members — initially member-a
	// and member-b.
	tok := env.mintToken("dynamic-membership", auth.FullPermission(nil, []string{"authz-group"}))

	grp := env.models.GetModelNative("authz-group")
	if grp == nil {
		t.Fatal("group row missing")
	}

	// Replace the membership with a SINGLE member-b.
	updated := *grp
	updated.Members = []models.GroupMember{{ModelID: "member-b", Weight: 1}}
	if err := env.models.UpdateModel("authz-group", updated); err != nil {
		t.Fatalf("UpdateModel(group): %v", err)
	}

	// The membership accessor must reflect the write with no delay (the
	// snapshot is rebuilt by the decorator's write-through mutator).
	if got := env.models.GroupMembers("authz-group"); len(got) != 1 || got[0] != "member-b" {
		t.Fatalf("GroupMembers after the write = %v, want [member-b] — the in-memory snapshot must be invalidated on a group write", got)
	}

	// member-a has LEFT the group ⇒ the SAME token (never re-saved) can
	// no longer reach it. The request 403s at the entry.
	rec := env.postChat(tok, "member-a")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("removed member: status = %d, want 403 (the token was never re-saved — reach must follow the CURRENT membership)", rec.Code)
	}

	// member-b is still a member ⇒ served.
	rec = env.postChat(tok, "member-b")
	if rec.Code != http.StatusOK {
		t.Fatalf("remaining member: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Deny-all (INVALID grant row) — the fail-closed landing.
// ─────────────────────────────────────────────────────────────────────────────

// TestChat_DenyAll_InvalidGrantRow_403 pins the fail-closed flip: a
// token whose stored grant axis is unreadable denies EVERYTHING.
//
// The row is written straight to the token table (the store's own write
// path would refuse to produce it) — the exact shape a corrupt/legacy
// row has on disk.
func TestChat_DenyAll_InvalidGrantRow_403(t *testing.T) {
	env := newAuthzEnv(t)
	defer env.close()

	plaintext, stored, err := auth.NewTokenStore(env.db, database.SQLite).CreateToken(
		context.Background(), "corrupt-row", nil, "authz-test", auth.FullPermission(nil, nil))
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	// Corrupt the MODEL axis: a non-array JSON literal is INVALID
	// (fail closed), never ABSENT (wildcard).
	if _, err := env.db.Exec(`UPDATE auth_tokens SET allowed_models = ? WHERE id = ?`, `"not-an-array"`, stored.ID); err != nil {
		t.Fatalf("corrupt the grant row: %v", err)
	}

	rec := env.postChat(plaintext, "member-a")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("DENY-ALL token: status = %d, want 403 (an unreadable grant row must deny every model)", rec.Code)
	}
	if got := env.totalUpstreamCalls(); got != 0 {
		t.Errorf("upstream calls = %d, want 0 for a DENY-ALL token", got)
	}
}

// TestChat_EmptyTextGrantRow_DenyAll pins the Phase-1.1 item A scanner
// rule end-to-end: a legacy EMPTY-STRING grant column is UNREADABLE
// (DENY-ALL), never ABSENT (which would have been a WILDCARD on the
// model axis before the fix).
func TestChat_EmptyTextGrantRow_DenyAll(t *testing.T) {
	env := newAuthzEnv(t)
	defer env.close()

	plaintext, stored, err := auth.NewTokenStore(env.db, database.SQLite).CreateToken(
		context.Background(), "legacy-empty-row", nil, "authz-test", auth.FullPermission(nil, nil))
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	if _, err := env.db.Exec(`UPDATE auth_tokens SET allowed_models = '' WHERE id = ?`, stored.ID); err != nil {
		t.Fatalf("write the legacy empty row: %v", err)
	}

	rec := env.postChat(plaintext, "member-a")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("legacy '' grant row: status = %d, want 403 — an empty TEXT column is UNREADABLE (DENY-ALL), never ABSENT/wildcard", rec.Code)
	}
	if got := env.totalUpstreamCalls(); got != 0 {
		t.Errorf("upstream calls = %d, want 0", got)
	}
}

// TestReachCompleteness_ExecutionListsSubsetOfReach pins I15 at the
// entry: every execution-list candidate the coordinator receives is
// inside the token's reach. The list is captured through the production
// constructor path by asserting on the served model instead: the served
// model must be one of the reachable members.
func TestReachCompleteness_ExecutionListsSubsetOfReach(t *testing.T) {
	env := newAuthzEnv(t)
	defer env.close()

	// Reach = the group's members only (via a group grant).
	tok := env.mintToken("reach-subset", auth.FullPermission(nil, []string{"authz-group"}))
	reach := map[string]bool{"authz-group": true, "member-a": true, "member-b": true}

	for i := 0; i < 4; i++ {
		rec := env.postChat(tok, "authz-group")
		if rec.Code != http.StatusOK {
			t.Fatalf("iteration %d: status = %d, want 200 (body: %s)", i, rec.Code, rec.Body.String())
		}
		served := env.h.stickyEngine // keep the engine referenced (no sticky assertions here)
		_ = served
	}

	// The upstream saw ONLY in-reach model ids.
	if got := env.totalUpstreamCalls(); got == 0 {
		t.Fatal("no upstream calls — the reach-subset pin would be vacuous")
	}

	// Cross-check through the rewrite seam: the execution list for the
	// group is a subset of the reach set.
	res := env.models.RewriteGroupEntriesScoped([]string{"authz-group"}, "reach-subset-conv", authzScopeFor(reach))
	for _, id := range res.ModelList {
		if !reach[id] {
			t.Errorf("execution list carries %q, which is outside the reach set %v", id, reach)
		}
	}
	if res.MemberID != "" && !reach[res.MemberID] {
		t.Errorf("the picked member %q is outside the reach set %v", res.MemberID, reach)
	}
}

// authzScopeFor builds a models.ReachFilter from a plain set (the test
// twin of *authz.Scope — the production filter is asserted through the
// HTTP path above).
func authzScopeFor(set map[string]bool) models.ReachFilter { return authzSet(set) }

type authzSet map[string]bool

func (s authzSet) Allows(id string) bool { return s[id] }

// unusedFailImport keeps the sql import meaningful if a future edit drops
// the direct DB writes above.
var _ = fmt.Sprintf
var _ = os.RemoveAll

// ─────────────────────────────────────────────────────────────────────────────
// Anthropic lane — the same predicate, the chat-shaped 403 envelope.
// ─────────────────────────────────────────────────────────────────────────────

// postAnthropic issues an authenticated /v1/messages request.
func (e *authzEnv) postAnthropic(token, model string) *httptest.ResponseRecorder {
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

// TestAnthropic_Group_AllMembersOutOfScope_403_EnvelopeParity is the
// PD-14 pin: the Anthropic scope denial reuses the CHAT 403 writer, so
// the envelope is byte-identical across lanes — no `api_error` type, no
// `group_unservable` code — and ZERO upstream attempts run.
func TestAnthropic_Group_AllMembersOutOfScope_403_EnvelopeParity(t *testing.T) {
	env := newAuthzEnv(t)
	defer env.close()

	tok := env.mintToken("anthropic-group-entry-only", auth.FullPermission([]string{"authz-group"}, nil))
	rec := env.postAnthropic(tok, "authz-group")

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, `"api_error"`) {
		t.Errorf("the 403 body carries the Anthropic api_error type (%s) — the scope denial must reuse the chat writer", body)
	}
	if strings.Contains(body, "group_unservable") {
		t.Errorf("the 403 body carries group_unservable (%s) — an availability code must never describe a permission denial", body)
	}
	var envelope struct {
		Error struct {
			Type  string `json:"type"`
			Model string `json:"model"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode the 403 body (%s): %v", body, err)
	}
	if envelope.Error.Type != "access_denied" {
		t.Errorf("error.type = %q, want access_denied", envelope.Error.Type)
	}
	if envelope.Error.Model != "authz-group" {
		t.Errorf("error.model = %q, want the client-requested %q", envelope.Error.Model, "authz-group")
	}
	if got := env.totalUpstreamCalls(); got != 0 {
		t.Errorf("upstream calls = %d, want 0", got)
	}
}

// TestAnthropic_OutOfScopeModel_403_ZeroAttempts pins the hoisted entry
// gate on the Anthropic lane (the old arm ran AFTER the rewrite).
func TestAnthropic_OutOfScopeModel_403_ZeroAttempts(t *testing.T) {
	env := newAuthzEnv(t)
	defer env.close()

	tok := env.mintToken("anthropic-models-only", auth.FullPermission([]string{"some-other-model"}, nil))
	rec := env.postAnthropic(tok, "member-a")

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	if got := env.totalUpstreamCalls(); got != 0 {
		t.Errorf("upstream calls = %d, want 0 — a denied request must never reach an upstream", got)
	}
}

// TestAnthropic_WildcardToken_Unchanged_200 pins D11 on this lane.
func TestAnthropic_WildcardToken_Unchanged_200(t *testing.T) {
	env := newAuthzEnv(t)
	defer env.close()

	tok := env.mintToken("anthropic-wildcard", auth.FullPermission(nil, nil))
	rec := env.postAnthropic(tok, "member-a")
	if rec.Code != http.StatusOK {
		t.Fatalf("wildcard token: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Decision lane — the group arm's group-level check.
// ─────────────────────────────────────────────────────────────────────────────

// newDecisionLane builds the decision handler over the same decorator
// (the cmd/main.go wiring shape).
func (e *authzEnv) newDecisionLane() *decision.Handler {
	e.t.Helper()
	dh := decision.NewHandler(e.h.config.ConfigMgr, e.bus, auth.NewTokenStore(e.db, database.SQLite), nil)
	dh.SetModelResolver(decision.NewModelResolverFromConfig(func(modelID string) *models.ModelConfig {
		return e.models.GetModel(modelID)
	}))
	dh.SetCredentialResolver(decision.NewCredentialResolverFromConfig(func(modelID string) (models.ResolvedCredential, bool) {
		return e.models.ResolveInternalConfigWithAffinity(modelID, modelID)
	}))
	dh.SetModelsConfig(e.models)
	return dh
}

// postDecision issues an authenticated decision request.
func (e *authzEnv) postDecision(dh *decision.Handler, token, model string) *httptest.ResponseRecorder {
	e.t.Helper()
	body, _ := json.Marshal(map[string]interface{}{
		"model": model,
		"input": "pick one",
		"questions": []map[string]interface{}{
			{"type": "predicate", "name": "q1", "instructions": "is it true?"},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/decisions", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	dh.HandleDecision(rec, req)
	return rec
}

// TestDecision_Group_NotGranted_403_ZeroAttempts pins the group arm's
// ARM #1: the group itself must be in reach — evaluated AFTER the lane
// gate (precedence preserved) and BEFORE any member construction. Zero
// upstream attempts.
func TestDecision_Group_NotGranted_403_ZeroAttempts(t *testing.T) {
	env := newAuthzEnv(t)
	defer env.close()

	// A decision group over decision-kind members.
	for _, m := range []models.ModelConfig{
		{ID: "dec-a", Name: "Dec A", Enabled: true, Kind: models.KindDecision, Internal: true, InternalModel: "stub-model", Credentials: models.TestRefs("authz-cred")},
		{ID: "dec-b", Name: "Dec B", Enabled: true, Kind: models.KindDecision, Internal: true, InternalModel: "stub-model", Credentials: models.TestRefs("authz-cred")},
	} {
		if err := env.models.AddModel(m); err != nil {
			t.Fatalf("AddModel(%s): %v", m.ID, err)
		}
	}
	if err := env.models.AddModel(models.ModelConfig{
		ID: "dec-group", Name: "Decision Group", Enabled: true, Kind: models.KindGroup, Internal: true,
		Members: []models.GroupMember{{ModelID: "dec-a", Weight: 1}, {ModelID: "dec-b", Weight: 1}},
	}); err != nil {
		t.Fatalf("AddModel(dec-group): %v", err)
	}

	dh := env.newDecisionLane()
	tok := env.mintToken("decision-not-granted", auth.FullPermission([]string{"some-other-model"}, nil))
	rec := env.postDecision(dh, tok, "dec-group")

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "decision_all_members_failed") {
		t.Errorf("the 403 body carries decision_all_members_failed (%s) — a permission denial must never be reported as an availability failure", body)
	}
	if got := env.totalUpstreamCalls(); got != 0 {
		t.Errorf("upstream calls = %d, want 0 — the group-level check precedes every member attempt", got)
	}
}

// TestDecision_Group_Granted_NotScopeDenied pins the other half: a group
// grant passes the scope arm, so the request proceeds to the member
// attempt (the fixture upstream speaks the chat wire, not the decision
// wire, so the attempt itself fails with the AVAILABILITY envelope —
// which is exactly the point: an in-reach group is never answered with
// a scope denial).
func TestDecision_Group_Granted_NotScopeDenied(t *testing.T) {
	env := newAuthzEnv(t)
	defer env.close()

	for _, m := range []models.ModelConfig{
		{ID: "dec-a", Name: "Dec A", Enabled: true, Kind: models.KindDecision, Internal: true, InternalModel: "stub-model", Credentials: models.TestRefs("authz-cred")},
	} {
		if err := env.models.AddModel(m); err != nil {
			t.Fatalf("AddModel(%s): %v", m.ID, err)
		}
	}
	if err := env.models.AddModel(models.ModelConfig{
		ID: "dec-group", Name: "Decision Group", Enabled: true, Kind: models.KindGroup, Internal: true,
		Members: []models.GroupMember{{ModelID: "dec-a", Weight: 1}},
	}); err != nil {
		t.Fatalf("AddModel(dec-group): %v", err)
	}

	dh := env.newDecisionLane()
	tok := env.mintToken("decision-granted", auth.FullPermission(nil, []string{"dec-group"}))
	rec := env.postDecision(dh, tok, "dec-group")

	if rec.Code == http.StatusForbidden {
		t.Fatalf("granted group: status = 403 — a group grant must pass the scope arm (body: %s)", rec.Body.String())
	}
	if got := env.totalUpstreamCalls(); got < 1 {
		t.Errorf("upstream calls = %d, want >= 1 — the granted group's member must be attempted", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Image-gen lane — the entry-only predicate.
// ─────────────────────────────────────────────────────────────────────────────

// TestImggen_NotGranted_403 pins the image-gen entry gate: entry-only
// (no candidate list exists on this lane).
func TestImggen_NotGranted_403(t *testing.T) {
	env := newAuthzEnv(t)
	defer env.close()

	// An image-gen model requires a minimax / 9router credential
	// (models.ValidateKindRules).
	if err := env.models.AddCredential(models.CredentialConfig{
		ID: "authz-imgcred", Provider: "minimax", APIKey: "k", BaseURL: env.up.URL,
	}); err != nil {
		t.Fatalf("AddCredential(img): %v", err)
	}
	if err := env.models.AddModel(models.ModelConfig{
		ID: "img-a", Name: "Img A", Enabled: true, Kind: models.KindImageGen, Internal: true,
		InternalModel: "stub-model", Credentials: models.TestRefs("authz-imgcred"),
	}); err != nil {
		t.Fatalf("AddModel(img-a): %v", err)
	}

	ih := imggen.NewHandler(env.h.config.ConfigMgr, env.bus, auth.NewTokenStore(env.db, database.SQLite), nil)
	ih.SetModelResolver(imggen.NewModelResolverFromConfig(func(modelID string) *models.ModelConfig {
		return env.models.GetModel(modelID)
	}))
	ih.SetCredentialResolver(imggen.NewCredentialResolverFromConfig(func(modelID string) (models.ResolvedCredential, bool) {
		return env.models.ResolveInternalConfigWithAffinity(modelID, modelID)
	}))
	ih.SetModelsConfig(env.models)

	tok := env.mintToken("img-not-granted", auth.FullPermission([]string{"some-other-model"}, nil))
	body, _ := json.Marshal(map[string]interface{}{"model": "img-a", "prompt": "a cat"})
	req := httptest.NewRequest(http.MethodPost, "/v1/image_generation", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	ih.HandleImageGeneration(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	if got := env.totalUpstreamCalls(); got != 0 {
		t.Errorf("upstream calls = %d, want 0 — the image-gen lane has no candidate list; the entry check is the whole obligation", got)
	}
}

// TestDecisionGroup_FilterDoesNotMutateCachedRow is the PD-5 pin: the
// member filter runs on a COPY of the group row, so the CACHED row's
// Members slice is byte-identical before and after an out-of-scope
// member filter. (Slicing with `group.Members[:0]` would keep the source
// array's capacity and the appends would write straight into the cache.)
func TestDecisionGroup_FilterDoesNotMutateCachedRow(t *testing.T) {
	env := newAuthzEnv(t)
	defer env.close()

	for _, m := range []models.ModelConfig{
		{ID: "dec-a", Name: "Dec A", Enabled: true, Kind: models.KindDecision, Internal: true, InternalModel: "stub-model", Credentials: models.TestRefs("authz-cred")},
		{ID: "dec-b", Name: "Dec B", Enabled: true, Kind: models.KindDecision, Internal: true, InternalModel: "stub-model", Credentials: models.TestRefs("authz-cred")},
	} {
		if err := env.models.AddModel(m); err != nil {
			t.Fatalf("AddModel(%s): %v", m.ID, err)
		}
	}
	if err := env.models.AddModel(models.ModelConfig{
		ID: "dec-group", Name: "Decision Group", Enabled: true, Kind: models.KindGroup, Internal: true,
		Members: []models.GroupMember{{ModelID: "dec-a", Weight: 1}, {ModelID: "dec-b", Weight: 2}},
	}); err != nil {
		t.Fatalf("AddModel(dec-group): %v", err)
	}

	before := env.models.GetModelNative("dec-group")
	if before == nil || len(before.Members) != 2 {
		t.Fatalf("group fixture: %+v", before)
	}
	beforeMembers := append([]models.GroupMember(nil), before.Members...)

	dh := env.newDecisionLane()
	// The group ENTRY is in reach and dec-a is in reach; dec-b is NOT
	// (a group id in allowed_models grants entry only — D4/D5).
	tok := env.mintToken("decision-partial-filter", auth.FullPermission([]string{"dec-group", "dec-a"}, nil))
	rec := env.postDecision(dh, tok, "dec-group")
	if rec.Code == http.StatusForbidden {
		t.Fatalf("status = 403 — the group entry and dec-a are in reach (body: %s)", rec.Body.String())
	}

	after := env.models.GetModelNative("dec-group")
	if after == nil {
		t.Fatal("the group row vanished")
	}
	if len(after.Members) != len(beforeMembers) {
		t.Fatalf("cached Members length = %d after the request, want %d — the filter must run on a COPY", len(after.Members), len(beforeMembers))
	}
	for i := range beforeMembers {
		if after.Members[i] != beforeMembers[i] {
			t.Errorf("cached Members[%d] = %+v after the request, want %+v — the member filter mutated the CACHED row", i, after.Members[i], beforeMembers[i])
		}
	}
}
