package imggen

// imggen-groups commission — Phase 2 authz composition battery (T1–T8).
//
// P1 landed the group serving loop; this file pins the three v203 arms
// composed onto it (phase2-plan.md §4.1):
//
//   - ARM#1: the group arm resolves its OWN scope and authorizes the
//     resolved group id, quoting the CLIENT string (T1, T8 precedence).
//   - ARM#2: members are excluded on a FRESH COPY before the policy
//     builder (T2, T5, T6).
//   - ARM#3: an empty order is 403 when scope-CAUSED and 502 when the
//     group is merely unservable (T4, the all-disabled pair).
//
// A-6: every 403 path is asserted to write ZERO engine state. The
// byte-envelope pin asserts the 403 body is byte-identical to the chat /
// decision lanes (the FE parses all three identically).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/modelscache"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
)

// ---------- shared helpers for the authz battery ----------

// newAuthzGroupEnv builds P1's two-member image-gen group env (the D8
// scaffolding) with two healthy stubbed upstreams. The default token is
// the wildcard; each test narrows it with setGrants.
func newAuthzGroupEnv(t *testing.T) (*groupEnv, *groupFakeUpstream, *groupFakeUpstream) {
	t.Helper()
	body := successBody(1, 0)
	upA := newGroupUpstream(t, 200, body)
	upB := newGroupUpstream(t, 200, body)
	env := newGroupEnv(t,
		[]models.ModelConfig{
			groupMemberRow("img-a"),
			groupMemberRow("img-b"),
			groupRow("img-group", "img-a", "img-b"),
		},
		map[string]models.GroupLane{"img-group": models.GroupLaneImageGen},
		map[string][]models.ResolvedCredential{
			"img-a": {{Provider: "minimax", APIKey: "sk-a", BaseURL: upA.srv.URL, InternalModel: "image-img-a", CredentialID: "cred-a"}},
			"img-b": {{Provider: "minimax", APIKey: "sk-b", BaseURL: upB.srv.URL, InternalModel: "image-img-b", CredentialID: "cred-b"}},
		},
		map[string]*groupFakeUpstream{"img-a": upA, "img-b": upB})
	return env, upA, upB
}

// setGrants narrows the default token's two axes. Passing nil for an axis
// makes it ABSENT (the wildcard half of the conjunctive rule); passing an
// explicit []string{} makes it PRESENT-but-empty (grants nothing).
func setGrants(env *groupEnv, allowedModels, allowedGroups []string) {
	tok := env.tokenStore.tokens["tok-test"]
	tok.AllowedModels = allowedModels
	tok.AllowedGroups = allowedGroups
}

// assertImggen403Bytes pins the RAW body bytes of the 403 (not just decoded
// fields). sendModelNotAllowedError is byte-identical across chat/decision/
// imggen; json.Encoder sorts map keys so the body is deterministic. The
// envelope quotes the CLIENT string (wantClient), never the resolved id.
func assertImggen403Bytes(t *testing.T, rec *httptest.ResponseRecorder, wantClient string) {
	t.Helper()
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	want := fmt.Sprintf("{\"error\":{\"message\":\"model not allowed for this token\",\"model\":%q,\"type\":\"access_denied\"}}\n", wantClient)
	if rec.Body.String() != want {
		t.Errorf("403 body bytes = %q, want %q (byte-identical to the chat/decision lanes)", rec.Body.String(), want)
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
		t.Fatalf("decode 403 body (%s): %v", rec.Body.String(), err)
	}
	if envelope.Error.Type != "access_denied" {
		t.Errorf("error.type = %q, want access_denied (body: %s)", envelope.Error.Type, rec.Body.String())
	}
	if envelope.Error.Model != wantClient {
		t.Errorf("error.model = %q, want %q (the CLIENT string — never the canonical resolved id)", envelope.Error.Model, wantClient)
	}
	if envelope.Error.Code != "" {
		t.Errorf("error.code = %q, want empty (the 403 is the plain access_denied envelope)", envelope.Error.Code)
	}
}

// assertZeroEngineStateAfter403 is the A-6 assertion. v1 writes ZERO
// cross-request health state (F-3): no marks, no cooldown entries, no
// skip-cache writes. The env exposes no health seams in v1, so the pin
// checks every OBSERVABLE proxy is still empty after a permission denial:
// no metering row, no attempt/exhaustion/success event, no upstream hit.
// When the v2 health mirror lands its seams join this assertion — a
// permission denial is never a health fact.
func assertZeroEngineStateAfter403(t *testing.T, env *groupEnv) {
	t.Helper()
	if rows := env.modelUsage(); len(rows) != 0 {
		t.Errorf("usage rows after 403 = %v, want empty — a permission denial writes no state", rows)
	}
	evts := env.events()
	for _, outcome := range []string{"member_attempted", "all_members_failed", "success"} {
		if n := countEvents(evts, outcome); n != 0 {
			t.Errorf("%s events after 403 = %d, want 0", outcome, n)
		}
	}
	for id, up := range env.upstreams {
		if got := up.hits.Load(); got != 0 {
			t.Errorf("upstream %q hits after 403 = %d, want 0", id, got)
		}
	}
}

// assertNoUpstreamHits asserts every stubbed upstream is untouched (the
// zero-attempt leg of T1/T4 — the walk accounting is asserted separately
// in each test).
func assertNoUpstreamHits(t *testing.T, env *groupEnv) {
	t.Helper()
	for id, up := range env.upstreams {
		if got := up.hits.Load(); got != 0 {
			t.Errorf("upstream %q hits = %d, want 0 — a denied request must never reach an upstream", id, got)
		}
	}
}

// ---------- T1: group NOT granted ----------

// T1 — a token that grants only an unrelated model cannot reach the
// group: 403 access_denied quoting the CLIENT string, ZERO upstream
// attempts (stub hit counters + walk accounting), the model_not_allowed
// event with upstream_status=0, and ZERO engine-state writes (A-6).
func TestImggenAuthz_GroupNotGranted_403_ZeroAttempts(t *testing.T) {
	env, _, _ := newAuthzGroupEnv(t)
	setGrants(env, []string{"some-other-model"}, nil)

	rec := env.post(`{"model":"img-group","prompt":"x"}`)

	assertImggen403Bytes(t, rec, "img-group")
	assertNoUpstreamHits(t, env)

	evts := env.events()
	if n := countEvents(evts, "model_not_allowed"); n != 1 {
		t.Fatalf("model_not_allowed events = %d, want exactly 1; events=%v", n, evts)
	}
	evt := findEvent(evts, "model_not_allowed")
	if evt["model"] != "img-group" {
		t.Errorf("event model = %v, want the CLIENT string img-group", evt["model"])
	}
	if evt["upstream_status"] != 0 {
		t.Errorf("event upstream_status = %v, want 0 (no upstream was contacted)", evt["upstream_status"])
	}
	if n := countEvents(evts, "member_attempted"); n != 0 {
		t.Errorf("member_attempted events = %d, want 0 (the walk never started)", n)
	}
	assertZeroEngineStateAfter403(t, env)
}

// ---------- T2: group granted via the GROUP axis ----------

// T2 — a token granting the GROUP on allowed_groups reaches the group
// AND its current members (the reach expansion in authz.Resolve). 200
// with >= 1 upstream hit.
func TestImggenAuthz_GroupGrantedViaAllowedGroups_200(t *testing.T) {
	env, upA, upB := newAuthzGroupEnv(t)
	setGrants(env, nil, []string{"img-group"})

	rec := env.post(`{"model":"img-group","prompt":"x"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (a group grant expands to its members); body=%s", rec.Code, rec.Body.String())
	}
	if got := upA.hits.Load() + upB.hits.Load(); got < 1 {
		t.Errorf("upstream hits = %d, want >= 1 — the granted group must serve a member", got)
	}
}

// ---------- T3: wildcard token ----------

// T3 — a token with BOTH axes absent keeps today's behaviour bit-for-bit:
// the conjunctive wildcard allows the group and its members, 200.
func TestImggenAuthz_WildcardToken_200(t *testing.T) {
	env, upA, upB := newAuthzGroupEnv(t)
	// The default token is the wildcard — no setGrants call.

	rec := env.post(`{"model":"img-group","prompt":"x"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("wildcard token: status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := upA.hits.Load() + upB.hits.Load(); got < 1 {
		t.Errorf("upstream hits = %d, want >= 1", got)
	}
}

// ---------- T4: group granted by the MODEL axis only ----------

// T4 — allowed_models=[g] with NO group axis ⇒ reach = {g} with ZERO
// member expansion (the group target is reachable, its members are not).
// Every base-servable member is out of scope ⇒ ARM#3 403 access_denied,
// NEVER the 502 availability envelope, ZERO upstream attempts.
func TestImggenAuthz_GroupEntryOnly_ModelAxis_403_Not502(t *testing.T) {
	env, _, _ := newAuthzGroupEnv(t)
	setGrants(env, []string{"img-group"}, nil)

	rec := env.post(`{"model":"img-group","prompt":"x"}`)

	assertImggen403Bytes(t, rec, "img-group")
	if strings.Contains(rec.Body.String(), imggenAllMembersFailedCode) {
		t.Errorf("the 403 body carries %q (%s) — a scope exhaustion must NEVER be reported as availability", imggenAllMembersFailedCode, rec.Body.String())
	}
	assertNoUpstreamHits(t, env)
	evts := env.events()
	if n := countEvents(evts, "model_not_allowed"); n != 1 {
		t.Errorf("model_not_allowed events = %d, want 1; events=%v", n, evts)
	}
	if n := countEvents(evts, "all_members_failed"); n != 0 {
		t.Errorf("all_members_failed events = %d, want 0 — the 403 is not an exhaustion", n)
	}
	assertZeroEngineStateAfter403(t, env)
}

// ---------- T5: one member excluded, one admitted ----------

// T5 — granting the group id plus ONE member on the model axis keeps the
// group in reach (ARM#1 passes) but excludes the other member (ARM#2).
// The excluded member is NEVER attempted and NEVER named; the admitted
// member serves; scopeExcluded>0 does not leak a 403 on a non-empty order.
func TestImggenAuthz_OneMemberExcluded_OneAdmitted(t *testing.T) {
	env, upA, upB := newAuthzGroupEnv(t)
	setGrants(env, []string{"img-group", "img-b"}, nil)

	rec := env.post(`{"model":"img-group","prompt":"x"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the admitted member serves); body=%s", rec.Code, rec.Body.String())
	}
	if upB.hits.Load() < 1 {
		t.Errorf("admitted member img-b hits = %d, want >= 1", upB.hits.Load())
	}
	if upA.hits.Load() != 0 {
		t.Errorf("excluded member img-a hits = %d, want 0 — it must never be attempted", upA.hits.Load())
	}
	if strings.Contains(rec.Body.String(), "img-a") {
		t.Errorf("the 200 body names the excluded member img-a (%s)", rec.Body.String())
	}
	if n := countEvents(env.events(), "model_not_allowed"); n != 0 {
		t.Errorf("model_not_allowed events = %d, want 0 (a non-empty order is not a permission failure)", n)
	}
}

// ---------- T6: disabled-vs-out-of-scope byte-identical (no leak) ----------

// T6 — swapping a DISABLED member for an ENABLED but out-of-scope one
// must produce byte-identical 200 answers: neither the disabled state nor
// the scope exclusion may appear in the response (no existence/enablement
// oracle). Mirrors pkg/decision/handler_group_test.go:2004-2050 including
// the forbidden-substring sweep on the 200 path.
func TestImggenAuthz_DisabledVsOutOfScope_NoLeak(t *testing.T) {
	body := successBody(1, 0)
	build := func(second models.ModelConfig) *groupEnv {
		upHealthy := newGroupUpstream(t, 200, body)
		upSecond := newGroupUpstream(t, 200, body)
		env := newGroupEnv(t,
			[]models.ModelConfig{
				groupMemberRow("img-healthy"),
				second,
				groupRowWithBackups("img-group", "img-healthy", second.ID),
			},
			map[string]models.GroupLane{"img-group": models.GroupLaneImageGen},
			map[string][]models.ResolvedCredential{
				"img-healthy": {{Provider: "minimax", APIKey: "sk-h", BaseURL: upHealthy.srv.URL, InternalModel: "image-img-healthy", CredentialID: "cred-h"}},
				second.ID:     {{Provider: "minimax", APIKey: "sk-s", BaseURL: upSecond.srv.URL, InternalModel: "image-" + second.ID, CredentialID: "cred-s"}},
			},
			map[string]*groupFakeUpstream{"img-healthy": upHealthy, second.ID: upSecond})
		setGrants(env, []string{"img-group", "img-healthy"}, nil)
		return env
	}

	disabled := build(groupMemberRowDisabled("img-disabled"))
	recDisabled := disabled.post(`{"model":"img-group","prompt":"x"}`)
	enabled := build(groupMemberRow("img-other"))
	recEnabled := enabled.post(`{"model":"img-group","prompt":"x"}`)

	if recDisabled.Code != http.StatusOK || recEnabled.Code != http.StatusOK {
		t.Fatalf("status disabled=%d enabled-control=%d, want 200/200; bodies=%s / %s",
			recDisabled.Code, recEnabled.Code, recDisabled.Body.String(), recEnabled.Body.String())
	}
	if recDisabled.Body.String() != recEnabled.Body.String() {
		t.Fatalf("the disabled member's state is DISTINGUISHABLE: disabled=%s enabled-control=%s",
			recDisabled.Body.String(), recEnabled.Body.String())
	}
	for _, forbidden := range []string{"img-disabled", "img-other", "access_denied", imggenAllMembersFailedCode} {
		if strings.Contains(recDisabled.Body.String(), forbidden) {
			t.Errorf("the 200 body names %q (%s) — neither the disabled state nor the scope exclusion may leak",
				forbidden, recDisabled.Body.String())
		}
	}
	if got := disabled.upstreams["img-disabled"].hits.Load(); got != 0 {
		t.Errorf("disabled+out-of-scope member hits = %d, want 0", got)
	}
	if got := enabled.upstreams["img-other"].hits.Load(); got != 0 {
		t.Errorf("enabled+out-of-scope member hits = %d, want 0", got)
	}
}

// ---------- T7: served through the production decorator ----------

// T7 — the group is served through the REAL *CachedModelsConfig over a
// migrated SQLite stack (the decorator-forward-trap end-to-end proof).
// Without the GroupLane/GroupMembers forwards the lane gate fails closed
// and this pin goes red. Mirrors pkg/decision/handler_group_test.go:1894.
func TestImggenAuthz_GroupThroughCachedConfig(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "imggen-group-cached.db")
	t.Setenv("DATABASE_URL", "sqlite:"+dbPath)
	dbStore, err := database.NewConnection(context.Background())
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { dbStore.Close() })
	if err := dbStore.RunMigrations(context.Background()); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	mgr, err := database.NewModelsManager(dbStore, nil)
	if err != nil {
		t.Fatalf("NewModelsManager: %v", err)
	}
	t.Cleanup(mgr.Close)

	up := newGroupUpstream(t, 200, successBody(1, 0))
	if err := mgr.AddCredential(models.CredentialConfig{ID: "cred-1", Provider: "minimax", APIKey: "sk", BaseURL: up.srv.URL}); err != nil {
		t.Fatalf("AddCredential: %v", err)
	}
	if err := mgr.AddModel(models.ModelConfig{
		ID: "img-member", Name: "Img Member", Enabled: true, Internal: true,
		Kind: models.KindImageGen, InternalModel: "image-img-member",
		Credentials: []models.CredentialRef{{CredentialID: "cred-1", Weight: 1, Position: 0}},
	}); err != nil {
		t.Fatalf("seed image-gen member: %v", err)
	}
	if err := mgr.AddModel(models.ModelConfig{
		ID: "img-group", Name: "Img Group", Enabled: true, Internal: true,
		Kind: models.KindGroup, Members: []models.GroupMember{{ModelID: "img-member", Weight: 1}},
	}); err != nil {
		t.Fatalf("seed image-gen group: %v", err)
	}

	cached, err := modelscache.NewCachedModelsConfig(mgr, modelscache.Options{ReconcileInterval: time.Hour})
	if err != nil {
		t.Fatalf("NewCachedModelsConfig: %v", err)
	}
	t.Cleanup(cached.Stop)

	bus := events.NewBus()
	ts := newMemTokenStore()
	ts.add(&auth.AuthToken{ID: "tok-test", Name: "Test Token", TokenHash: auth.HashToken("sk-test-token")})
	h := NewHandler(stubConfig{timeout: 5 * time.Second}, bus, ts, nil)
	h.SetModelResolver(func(id string) (*models.ModelConfig, bool) {
		m := cached.GetModel(id)
		if m == nil {
			return nil, false
		}
		return m, true
	})
	h.SetCredentialResolver(func(id string) (models.ResolvedCredential, bool) {
		return cached.ResolveInternalConfigWithAffinity(id, id)
	})
	h.SetModelsConfig(cached)

	req := httptest.NewRequest(http.MethodPost, "/v1/image_generation", strings.NewReader(`{"model":"img-group","prompt":"x"}`))
	req.Header.Set("Authorization", "Bearer sk-test-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.HandleImageGeneration(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 through the decorator; body=%s", rec.Code, rec.Body.String())
	}
	if got := up.hits.Load(); got != 1 {
		t.Fatalf("the member must be attempted exactly once through the cached config, got %d hits", got)
	}
}

// ---------- T8: misroute precedence is token-independent ----------

// T8 — a chat / decision GROUP posted to /v1/image_generation is a 400
// image_misroute, NEVER a 403 or 502, INCLUDING with a wildcard token.
// The kind/lane gate runs before any scope work (T8 precedence law).
func TestImggenAuthz_MisroutePrecedence_WildcardToken_400(t *testing.T) {
	cases := []struct {
		name  string
		rows  []models.ModelConfig
		lanes map[string]models.GroupLane
	}{
		{
			name: "chat_group",
			rows: []models.ModelConfig{
				groupChatMemberRow("chat-a"),
				groupRow("chat-group", "chat-a"),
			},
			lanes: map[string]models.GroupLane{"chat-group": models.GroupLaneChat},
		},
		{
			name: "decision_group",
			rows: []models.ModelConfig{
				{ID: "dec-a", Name: "dec-a", Enabled: true, Internal: true, Kind: models.KindDecision, InternalModel: "gpt-x"},
				groupRow("decision-group", "dec-a"),
			},
			lanes: map[string]models.GroupLane{"decision-group": models.GroupLaneDecision},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newGroupEnv(t, tc.rows, tc.lanes, nil, nil)
			// The default token is the wildcard — the misroute must still fire.
			groupID := tc.rows[len(tc.rows)-1].ID
			rec := env.post(fmt.Sprintf(`{"model":%q,"prompt":"x"}`, groupID))

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 image_misroute (token-independent); body=%s", rec.Code, rec.Body.String())
			}
			if got := errorCode(t, rec.Body.Bytes()); got != imggenMisrouteCode {
				t.Errorf("error.code = %q, want %q", got, imggenMisrouteCode)
			}
			if n := countEvents(env.events(), "model_not_allowed"); n != 0 {
				t.Errorf("model_not_allowed events = %d, want 0 (a misroute is never a permission answer)", n)
			}
		})
	}
}

// ---------- the exhaustion-vs-scope pair (ARM#3 discriminator) ----------

// TestImggenAuthz_AllDisabled_ScopeExcluded_502 pins the OTHER fork of
// ARM#3: every member DISABLED (baseServable == 0) with scopeExcluded > 0
// is genuine unservability ⇒ 502 imggen_all_members_failed, NOT 403.
func TestImggenAuthz_AllDisabled_ScopeExcluded_502(t *testing.T) {
	env := newGroupEnv(t,
		[]models.ModelConfig{
			groupMemberRowDisabled("img-d1"),
			groupMemberRowDisabled("img-d2"),
			groupRow("img-group", "img-d1", "img-d2"),
		},
		map[string]models.GroupLane{"img-group": models.GroupLaneImageGen},
		nil, nil)
	setGrants(env, []string{"img-group"}, nil) // group in reach, members out of reach

	rec := env.post(`{"model":"img-group","prompt":"x"}`)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (all-disabled is availability, not permission); body=%s", rec.Code, rec.Body.String())
	}
	if got := errorCode(t, rec.Body.Bytes()); got != imggenAllMembersFailedCode {
		t.Errorf("error.code = %q, want %q", got, imggenAllMembersFailedCode)
	}
	if strings.Contains(rec.Body.String(), "access_denied") {
		t.Errorf("the 502 body carries access_denied (%s) — all-disabled is availability", rec.Body.String())
	}
}

// ---------- 404-vs-403 precedence (re-pin beside the new arms) ----------

// TestImggenAuthz_UnknownID_404_RegardlessOfGrants re-pins the existing
// precedence: an unknown id is a 404 regardless of the token's grants —
// the new arms never turn a missing model into a permission answer.
func TestImggenAuthz_UnknownID_404_RegardlessOfGrants(t *testing.T) {
	env := newGroupEnv(t, []models.ModelConfig{groupMemberRow("img-a")}, nil, nil, nil)
	setGrants(env, []string{"img-a"}, nil) // a restricted token

	rec := env.post(`{"model":"does-not-exist","prompt":"x"}`)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (unknown id, regardless of grants); body=%s", rec.Code, rec.Body.String())
	}
}

// ---------- PD-9: the 403 quotes the CLIENT string, not the resolved id ----------

// TestImggenAuthz_ClaimedID_403QuotesClientString is the PD-9 mirror pin
// that T1 alone cannot make: T1's client string equals the group id, so
// it cannot distinguish the two. Here a CLAIMED id ("claim-alias") is
// resolved claims-first INTO the group arm (a custom resolver maps it to
// the canonical group row), while the token grants only an unrelated
// model. ARM#1 authorizes the RESOLVED group id (img-group) but the 403
// must quote the CLIENT string (claim-alias) — the canonical id never
// appears in the body. Mirrors pkg/decision/group.go:130-138 / T2.16.
func TestImggenAuthz_ClaimedID_403QuotesClientString(t *testing.T) {
	env, _, _ := newAuthzGroupEnv(t)
	setGrants(env, []string{"some-other-model"}, nil)

	// Simulate the claims-first resolve: the client string resolves to
	// the canonical group row (which is what the arm authorizes).
	env.handler.SetModelResolver(func(id string) (*models.ModelConfig, bool) {
		if id != "claim-alias" {
			return nil, false
		}
		for i := range env.stub.rows {
			if env.stub.rows[i].ID == "img-group" {
				row := env.stub.rows[i]
				return &row, true
			}
		}
		return nil, false
	})

	rec := env.post(`{"model":"claim-alias","prompt":"x"}`)

	assertImggen403Bytes(t, rec, "claim-alias") // the CLIENT string
	if strings.Contains(rec.Body.String(), "img-group") {
		t.Errorf("the 403 body leaks the canonical resolved id img-group (%s) — only the client string may appear", rec.Body.String())
	}
	evt := findEvent(env.events(), "model_not_allowed")
	if evt == nil || evt["model"] != "claim-alias" {
		t.Errorf("event model = %v, want the CLIENT string claim-alias", evt["model"])
	}
	assertZeroEngineStateAfter403(t, env)
}
