package decision

// decision-groups commission — the decision lane's GROUP branch.
//
// The lane's contract under test:
//   - the PICK is policy (pools, weights, order) and never a mark;
//   - a mark SUPPRESSES an attempt (zero upstream cost) and is
//     recorded as a skip, never a failure;
//   - a member's own credentials are consumed BEFORE the walk
//     advances (the failure-class policy);
//   - exactly ONE client write, at the winner;
//   - exhaustion ⇒ 502 + code=decision_all_members_failed, no usage
//     row, one event.
//
// (The import-guard rule of this package: the banned subtrees are
// never named — not even in a comment.)

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/credentiallb"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/modelscache"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/skipcache"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/usage"
)

// groupMemberFixture describes one member of a decision group in the
// tests.
type groupMemberFixture struct {
	ID         string
	Weight     float64
	BackupOnly bool
	Enabled    bool
	Kind       string
	// Status + Body are the fake upstream's scripted answer.
	Status int
	Body   []byte
	// CredentialID is the member's credential (the credential resolver
	// returns it).
	CredentialID string
	// InternalModel is the member's wire model (the mark keys are
	// (credential|provider, wire-model) — distinct wire models keep
	// the members' marks distinct).
	InternalModel string
	// ExtraCredentialIDs are ADDITIONAL credential refs on the member
	// row (the member's own credential multiplicity — ADR-6 / AC-R4).
	// The credential resolver keeps answering the PRIMARY ref; the
	// extras exist so a fixture can express "this member has another
	// credential" (e.g. a member whose only marked credential must NOT
	// be in the D-C state).
	ExtraCredentialIDs []string
}

// groupEnv is the decision-lane group test environment: one fake
// upstream PER MEMBER (so the attempt order is observable), the group
// row, the member rows and the models seam.
type groupEnv struct {
	handlerTestEnv
	group      *models.ModelConfig
	members    []models.ModelConfig
	upstreams  map[string]*httptest.Server
	fakes      map[string]*fakeUpstream
	modelsSeam *stubGroupModels
	events     []events.Event
	sub        chan events.Event
}

// stubGroupModels is the models seam for the group tests: the live
// model set + the group-lane query (the OPTIONAL seam the lane
// asserts structurally).
type stubGroupModels struct {
	rows  []models.ModelConfig
	lanes map[string]models.GroupLane
}

func (s *stubGroupModels) GetModels() []models.ModelConfig {
	out := make([]models.ModelConfig, len(s.rows))
	copy(out, s.rows)
	return out
}
func (s *stubGroupModels) GetEnabledModels() []models.ModelConfig { return s.GetModels() }
func (s *stubGroupModels) GetModel(id string) *models.ModelConfig {
	for i := range s.rows {
		if s.rows[i].ID == id {
			row := s.rows[i]
			return &row
		}
	}
	return nil
}
func (s *stubGroupModels) GetModelByName(string) *models.ModelConfig { return nil }
func (s *stubGroupModels) GetTruncateParams(string) []string         { return nil }
func (s *stubGroupModels) GetFallbackChain(string) []string          { return nil }
func (s *stubGroupModels) AddModel(models.ModelConfig) error         { return nil }
func (s *stubGroupModels) UpdateModel(string, models.ModelConfig) error {
	return nil
}
func (s *stubGroupModels) RemoveModel(string) error { return nil }
func (s *stubGroupModels) Save() error              { return nil }
func (s *stubGroupModels) Validate() error          { return nil }
func (s *stubGroupModels) GetCredential(string) *models.CredentialConfig {
	return nil
}
func (s *stubGroupModels) GetCredentials() []models.CredentialConfig { return nil }
func (s *stubGroupModels) AddCredential(models.CredentialConfig) error {
	return nil
}
func (s *stubGroupModels) UpdateCredential(string, models.CredentialConfig) error {
	return nil
}
func (s *stubGroupModels) RemoveCredential(string) error { return nil }
func (s *stubGroupModels) ResolveInternalConfig(string) (string, string, string, string, bool) {
	return "", "", "", "", false
}
func (s *stubGroupModels) ResolveInternalConfigWithAffinity(string, string) (models.ResolvedCredential, bool) {
	return models.ResolvedCredential{}, false
}

// GroupLane is the OPTIONAL lane seam (the decision-groups
// commission's one new manager method).
func (s *stubGroupModels) GroupLane(groupID string) (models.GroupLane, bool) {
	lane, ok := s.lanes[groupID]
	return lane, ok
}

// The credential-group surface of models.ModelsConfigInterface.
//
// The credential-groups commission (v195, origin/latest) GREW that
// interface with nine credential-group methods, so this seam no longer
// satisfied it and the package's TEST build broke on the merge:
//
//	*stubGroupModels does not implement models.ModelsConfigInterface
//	(missing method AddCredentialGroup)
//
// These nine methods are the MECHANICAL adapter for that growth and
// mirror the no-op pattern upstream used for every other stub in the
// tree (pkg/ui/handlers_models_test.go, pkg/proxy/internal_handler_test.go,
// pkg/proxy/handle_models_imggen_test.go, pkg/proxy/normalizers/...):
// the stub carries no credential groups, so every accessor returns the
// empty value.
//
// ADDITIONS ONLY — no pre-existing stub method was removed, renamed or
// re-signed (the Tier-2 fence), and no assertion-bearing line was
// touched.
func (s *stubGroupModels) GetCredentialGroup(string) *models.CredentialGroupConfig { return nil }
func (s *stubGroupModels) GetCredentialGroups() []models.CredentialGroupConfig     { return nil }
func (s *stubGroupModels) AddCredentialGroup(models.CredentialGroupConfig) error   { return nil }
func (s *stubGroupModels) UpdateCredentialGroup(string, models.CredentialGroupConfig) error {
	return nil
}
func (s *stubGroupModels) RemoveCredentialGroup(string) error         { return nil }
func (s *stubGroupModels) ModelsUsingCredentialGroup(string) []string { return nil }
func (s *stubGroupModels) ModelsUsingCredentialGroupsAll() map[string][]string {
	return nil
}
func (s *stubGroupModels) GroupsContainingCredential(string) []string { return nil }
func (s *stubGroupModels) FlattenCredentialRefs(*models.ModelConfig) []models.CredentialRef {
	return nil
}

// newGroupEnv builds the group test env.
func newGroupEnv(t *testing.T, fixtures []groupMemberFixture) *groupEnv {
	t.Helper()
	bus := events.NewBus()
	db, dbCleanup := newDecisionUsageTestDB(t)
	uc := usage.NewCounter(db, database.SQLite)
	ts := newMemTokenStore()
	h := NewHandler(stubConfig{timeout: 5 * time.Second}, bus, ts, uc)
	ts.add(&auth.AuthToken{
		ID:            "tok-test",
		Name:          "Test Token",
		TokenHash:     auth.HashToken("sk-test-token"),
		AllowedModels: nil,
	})

	env := &groupEnv{
		handlerTestEnv: handlerTestEnv{
			t:          t,
			handler:    h,
			bus:        bus,
			usage:      uc,
			tokenStore: ts,
			dbCleanup:  dbCleanup,
		},
		upstreams:  map[string]*httptest.Server{},
		fakes:      map[string]*fakeUpstream{},
		modelsSeam: &stubGroupModels{lanes: map[string]models.GroupLane{}},
	}

	// One fake upstream per member (the attempt order is observable).
	members := make([]models.ModelConfig, 0, len(fixtures))
	groupMembers := make([]models.GroupMember, 0, len(fixtures))
	for _, f := range fixtures {
		if f.Enabled == false && f.Kind == "" {
			// An explicitly disabled member (the caller sets Enabled
			// false on an otherwise-valid fixture).
		}
		srv, fake := newFakeUpstream(t, fakeScript{status: f.Status, body: f.Body})
		env.upstreams[f.ID] = srv
		env.fakes[f.ID] = fake
		credID := f.CredentialID
		if credID == "" {
			credID = "cred-" + f.ID
		}
		kind := f.Kind
		if kind == "" {
			kind = models.KindDecision
		}
		wireModel := f.InternalModel
		if wireModel == "" {
			wireModel = "gpt-6-luna"
		}
		refs := []models.CredentialRef{{CredentialID: credID, Weight: 1, Position: 0}}
		for i, extra := range f.ExtraCredentialIDs {
			refs = append(refs, models.CredentialRef{CredentialID: extra, Weight: 1, Position: i + 1})
		}
		members = append(members, models.ModelConfig{
			ID:            f.ID,
			Name:          f.ID,
			Kind:          kind,
			Enabled:       f.Enabled,
			Internal:      true,
			InternalModel: wireModel,
			Credentials:   refs,
		})
		groupMembers = append(groupMembers, models.GroupMember{ModelID: f.ID, Weight: f.Weight, BackupOnly: f.BackupOnly})
	}
	group := &models.ModelConfig{
		ID:       "decision-group",
		Name:     "Decision Group",
		Kind:     models.KindGroup,
		Enabled:  true,
		Internal: true,
		Members:  groupMembers,
	}
	env.group = group
	env.members = members

	rows := append([]models.ModelConfig{}, members...)
	rows = append(rows, *group)
	env.modelsSeam.rows = rows
	env.modelsSeam.lanes[group.ID] = models.GroupLaneDecision

	// The model resolver: the group row for the group id, the member
	// rows for the member ids.
	h.SetModelResolver(func(id string) (*models.ModelConfig, bool) {
		if id == group.ID {
			row := *group
			return &row, true
		}
		for i := range members {
			if members[i].ID == id {
				row := members[i]
				return &row, true
			}
		}
		return nil, false
	})
	// The credential resolver: the member's own credential, by row
	// identity (the member id keys the resolution).
	h.SetCredentialResolver(func(id string) (models.ResolvedCredential, bool) {
		for _, f := range fixtures {
			if f.ID != id {
				continue
			}
			credID := f.CredentialID
			if credID == "" {
				credID = "cred-" + f.ID
			}
			wireModel := f.InternalModel
			if wireModel == "" {
				wireModel = "gpt-6-luna"
			}
			return models.ResolvedCredential{
				Provider: "openai",
				// The bearer carries the credential id — a test can
				// assert WHICH credential an attempt used.
				APIKey:        "sk-" + credID,
				BaseURL:       env.upstreams[id].URL,
				InternalModel: wireModel,
				CredentialID:  credID,
			}, true
		}
		return models.ResolvedCredential{}, false
	})
	h.SetModelsConfig(env.modelsSeam)

	// Capture the events for the telemetry assertions.
	sub, err := bus.Subscribe()
	if err != nil {
		t.Fatalf("bus.Subscribe: %v", err)
	}
	env.sub = sub
	t.Cleanup(func() { bus.Unsubscribe(sub) })

	return env
}

// send posts a decision request for the group id.
func (e *groupEnv) send(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	return e.sendRequest(http.MethodPost, standardRequestBody(e.group.ID), nil)
}

// hitsFor returns the upstream hit count of one member.
func (e *groupEnv) hitsFor(id string) int64 {
	if f, ok := e.fakes[id]; ok {
		return f.hitCount.Load()
	}
	return 0
}

// totalHits returns the summed upstream hit count.
func (e *groupEnv) totalHits() int64 {
	var total int64
	for _, f := range e.fakes {
		total += f.hitCount.Load()
	}
	return total
}

// decisionUsageRows counts the usage rows the meter wrote (the
// exhaustion path must write none).
func decisionUsageRows(t *testing.T, env *groupEnv) int {
	t.Helper()
	hourBucket := time.Now().UTC().Format("2006-01-02T15")
	rows, err := env.usage.GetModelUsage(context.Background(), hourBucket, hourBucket)
	if err != nil {
		t.Fatalf("GetModelUsage: %v", err)
	}
	total := 0
	for _, r := range rows {
		total += r.RequestCount
	}
	return total
}

// eventData normalizes an event's payload to a map.
func eventData(ev events.Event) map[string]interface{} {
	if m, ok := ev.Data.(map[string]interface{}); ok {
		return m
	}
	return nil
}

// drainEvents moves the published events into the local slice (the
// bus publishes synchronously into the subscriber channel).
func (e *groupEnv) drainEvents() {
	for {
		select {
		case ev := <-e.sub:
			e.events = append(e.events, ev)
		default:
			return
		}
	}
}

// outcomes returns the decision events' outcome values in order.
func (e *groupEnv) outcomes() []string {
	e.drainEvents()
	out := []string{}
	for _, ev := range e.events {
		if ev.Type != eventTypeDecision {
			continue
		}
		if outcome, ok := eventData(ev)["outcome"].(string); ok {
			out = append(out, outcome)
		}
	}
	return out
}

// hasOutcome reports whether an outcome was published.
func (e *groupEnv) hasOutcome(name string) bool {
	for _, o := range e.outcomes() {
		if o == name {
			return true
		}
	}
	return false
}

// countOutcome counts the published outcomes with a name.
func (e *groupEnv) countOutcome(name string) int {
	n := 0
	for _, o := range e.outcomes() {
		if o == name {
			n++
		}
	}
	return n
}

// decisionBody is a canonical OpenAI Decision success body.
func decisionBody() []byte {
	return mustJSON(map[string]interface{}{
		"model":   "gpt-6-luna",
		"answers": []interface{}{map[string]interface{}{"type": "predicate", "name": "is_x", "probability": 0.9}},
		"usage": map[string]interface{}{
			"input_tokens": 10, "output_tokens": 5, "total_tokens": 15,
		},
	})
}

// decisionErrorBody is the OpenAI Decision error envelope.
func decisionErrorBody(code, errType, msg string) []byte {
	return mustJSON(map[string]interface{}{
		"error": map[string]interface{}{"message": msg, "type": errType, "code": code},
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// The group branch's behaviour.
// ─────────────────────────────────────────────────────────────────────────────

// TestDecision_Group_ResolvesToDecisionMember — a group id on
// /v1/decisions resolves to a member and returns that member's typed
// answer.
func TestDecision_Group_ResolvesToDecisionMember(t *testing.T) {
	env := newGroupEnv(t, []groupMemberFixture{
		{ID: "dec-a", Weight: 1, Enabled: true, Status: 200, Body: decisionBody()},
	})
	rec := env.send(t)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var parsed struct {
		Model   string `json:"model"`
		Answers []any  `json:"answers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if parsed.Model != "gpt-6-luna" || len(parsed.Answers) != 1 {
		t.Fatalf("the member's typed answer must be relayed, got %s", rec.Body.String())
	}
	if env.hitsFor("dec-a") != 1 {
		t.Fatalf("the member must be attempted exactly once, got %d hits", env.hitsFor("dec-a"))
	}
}

// TestDecision_Group_DefaultPoolFirst_BackupPoolAfter — the default
// member serves first; with the default member unavailable, the BACKUP
// member serves.
func TestDecision_Group_DefaultPoolFirst_BackupPoolAfter(t *testing.T) {
	env := newGroupEnv(t, []groupMemberFixture{
		{ID: "dec-default", Weight: 1, Enabled: true, Status: 200, Body: decisionBody()},
		{ID: "dec-backup", Weight: 1, BackupOnly: true, Enabled: true, Status: 200, Body: decisionBody()},
	})
	rec := env.send(t)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if env.hitsFor("dec-default") != 1 {
		t.Fatalf("the default member must serve first, got %d hits", env.hitsFor("dec-default"))
	}
	if env.hitsFor("dec-backup") != 0 {
		t.Fatalf("the backup member must not be attempted while the default serves, got %d hits", env.hitsFor("dec-backup"))
	}

	// The default member fails ⇒ the backup member serves.
	env2 := newGroupEnv(t, []groupMemberFixture{
		{ID: "dec-default", Weight: 1, Enabled: true, Status: 500, Body: decisionErrorBody("boom", "server_error", "boom")},
		{ID: "dec-backup", Weight: 1, BackupOnly: true, Enabled: true, Status: 200, Body: decisionBody()},
	})
	rec2 := env2.send(t)
	if rec2.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the backup member serves); body=%s", rec2.Code, rec2.Body.String())
	}
	if env2.hitsFor("dec-default") != 1 || env2.hitsFor("dec-backup") != 1 {
		t.Fatalf("both members must be attempted once: default=%d backup=%d", env2.hitsFor("dec-default"), env2.hitsFor("dec-backup"))
	}
}

// TestDecision_Group_WeightsAffectPick — over N fresh requests the
// heavier member is picked more often (the weighted policy pick; no
// binding — every request draws afresh).
func TestDecision_Group_WeightsAffectPick(t *testing.T) {
	env := newGroupEnv(t, []groupMemberFixture{
		{ID: "dec-heavy", Weight: 9, Enabled: true, Status: 200, Body: decisionBody()},
		{ID: "dec-light", Weight: 1, Enabled: true, Status: 200, Body: decisionBody()},
	})
	for i := 0; i < 40; i++ {
		env.send(t)
	}
	heavy := env.hitsFor("dec-heavy")
	light := env.hitsFor("dec-light")
	if heavy <= light {
		t.Fatalf("the heavier member must be picked more often: heavy=%d light=%d", heavy, light)
	}
}

// TestDecision_Group_AllMembersFail_502DecisionAllMembersFailed — every
// member was attempted before the failure; the response is 502 with
// code=decision_all_members_failed; NO usage row was written; exactly
// ONE all_members_failed event.
func TestDecision_Group_AllMembersFail_502DecisionAllMembersFailed(t *testing.T) {
	env := newGroupEnv(t, []groupMemberFixture{
		{ID: "dec-a", Weight: 1, Enabled: true, Status: 500, Body: decisionErrorBody("boom", "server_error", "boom")},
		{ID: "dec-b", Weight: 1, Enabled: true, Status: 500, Body: decisionErrorBody("boom", "server_error", "boom")},
	})
	rec := env.send(t)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rec.Code, rec.Body.String())
	}
	_, _, code := decodeOpenAIError(t, rec.Body.Bytes())
	if code != "decision_all_members_failed" {
		t.Fatalf("code = %q, want decision_all_members_failed", code)
	}
	if !strings.Contains(rec.Body.String(), "2 attempted") {
		t.Errorf("the message must carry the attempted count, got %s", rec.Body.String())
	}
	if env.hitsFor("dec-a") != 1 || env.hitsFor("dec-b") != 1 {
		t.Fatalf("every member must be attempted exactly once: a=%d b=%d", env.hitsFor("dec-a"), env.hitsFor("dec-b"))
	}
	if n := env.countOutcome("all_members_failed"); n != 1 {
		t.Fatalf("all_members_failed events = %d, want exactly 1", n)
	}
	// No usage row was written (the failure path meters nothing).
	if rows := decisionUsageRows(t, env); rows != 0 {
		t.Fatalf("the exhaustion path must write NO usage row, got %d", rows)
	}
}

// TestDecision_Group_FirstFailsSecondSucceeds_ExactlyOneCommit — member
// A fails, member B succeeds ⇒ 200 with B's answer; the winner's member
// id is recorded in the event.
func TestDecision_Group_FirstFailsSecondSucceeds_ExactlyOneCommit(t *testing.T) {
	env := newGroupEnv(t, []groupMemberFixture{
		{ID: "dec-a", Weight: 1, Enabled: true, Status: 500, Body: decisionErrorBody("boom", "server_error", "boom")},
		{ID: "dec-b", Weight: 1, BackupOnly: true, Enabled: true, Status: 200, Body: decisionBody()},
	})
	rec := env.send(t)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	found := false
	env.drainEvents()
	for _, ev := range env.events {
		if ev.Type != eventTypeDecision {
			continue
		}
		data := eventData(ev)
		if member, ok := data["member"].(string); ok && member == "dec-b" {
			if model, ok := data["model"].(string); ok && model == "decision-group" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("the winner's member id must be recorded in the event (with the client-facing group id)")
	}
}

// TestDecision_Group_NoWriteBeforeWinner — the response writer records
// ZERO WriteHeader calls before the winner's attempt and exactly one
// after.
func TestDecision_Group_NoWriteBeforeWinner(t *testing.T) {
	env := newGroupEnv(t, []groupMemberFixture{
		{ID: "dec-a", Weight: 1, Enabled: true, Status: 500, Body: decisionErrorBody("boom", "server_error", "boom")},
		{ID: "dec-b", Weight: 1, BackupOnly: true, Enabled: true, Status: 200, Body: decisionBody()},
	})
	rec := env.send(t)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	// The recorder's Result().StatusCode is the single committed
	// status; a second write would have panicked the recorder (httptest
	// logs "superfluous WriteHeader call"), and the group path never
	// writes before the winner (the single-commit contract).
	if rec.Result().StatusCode != http.StatusOK {
		t.Fatalf("the committed status = %d, want 200", rec.Result().StatusCode)
	}
	if env.hitsFor("dec-a") != 1 || env.hitsFor("dec-b") != 1 {
		t.Fatalf("both members must be attempted: a=%d b=%d", env.hitsFor("dec-a"), env.hitsFor("dec-b"))
	}
}

// TestDecision_Group_Exhaustion_DoesNotReattemptTriedMember — the
// tried-set holds: each member is attempted at most once per request.
func TestDecision_Group_Exhaustion_DoesNotReattemptTriedMember(t *testing.T) {
	env := newGroupEnv(t, []groupMemberFixture{
		{ID: "dec-a", Weight: 1, Enabled: true, Status: 500, Body: decisionErrorBody("boom", "server_error", "boom")},
		{ID: "dec-b", Weight: 1, Enabled: true, Status: 500, Body: decisionErrorBody("boom", "server_error", "boom")},
	})
	env.send(t)
	if env.hitsFor("dec-a") != 1 || env.hitsFor("dec-b") != 1 {
		t.Fatalf("each member must be attempted at most once: a=%d b=%d", env.hitsFor("dec-a"), env.hitsFor("dec-b"))
	}
}

// TestDecision_Group_NonGroupRequest_ByteIdentical — a non-group
// decision request is byte-identical to the pre-change path: the group
// seams change nothing for a plain decision model.
func TestDecision_Group_NonGroupRequest_ByteIdentical(t *testing.T) {
	env := newGroupEnv(t, []groupMemberFixture{
		{ID: "dec-a", Weight: 1, Enabled: true, Status: 200, Body: decisionBody()},
	})
	// The plain (non-group) path: the model resolver answers the
	// member's own id.
	env.handler.SetModelResolver(func(id string) (*models.ModelConfig, bool) {
		if id != "dec-a" {
			return nil, false
		}
		row := env.members[0]
		return &row, true
	})
	rec := env.sendRequest(http.MethodPost, standardRequestBody("dec-a"), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if parsed["model"] != "gpt-6-luna" {
		t.Fatalf("the non-group path must relay the typed answer unchanged, got %v", parsed)
	}
	if len(parsed["answers"].([]interface{})) != 1 {
		t.Fatalf("the non-group path's body shape changed: %v", parsed)
	}
}

// TestDecision_Group_LaneSeamAbsent_FailsClosed400 — with the lane
// seam absent the request fails closed with 400 decision_misroute.
func TestDecision_Group_LaneSeamAbsent_FailsClosed400(t *testing.T) {
	env := newGroupEnv(t, []groupMemberFixture{
		{ID: "dec-a", Weight: 1, Enabled: true, Status: 200, Body: decisionBody()},
	})
	// A models seam WITHOUT the lane query (the optional seam is
	// absent).
	env.handler.SetModelsConfig(&stubGroupModels{rows: env.modelsSeam.rows, lanes: map[string]models.GroupLane{}})
	rec := env.send(t)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	_, _, code := decodeOpenAIError(t, rec.Body.Bytes())
	if code != "decision_misroute" {
		t.Fatalf("code = %q, want decision_misroute", code)
	}
	if env.totalHits() != 0 {
		t.Fatalf("no upstream may be called without a resolved lane, got %d hits", env.totalHits())
	}
}

// TestDecision_Group_InactiveMember_NotOfferedAsCandidate — an
// inactive model is not offered as a group candidate.
func TestDecision_Group_InactiveMember_NotOfferedAsCandidate(t *testing.T) {
	env := newGroupEnv(t, []groupMemberFixture{
		{ID: "dec-off", Weight: 1, Enabled: false, Status: 200, Body: decisionBody()},
		{ID: "dec-on", Weight: 1, Enabled: true, Status: 200, Body: decisionBody()},
	})
	rec := env.send(t)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if env.hitsFor("dec-off") != 0 {
		t.Fatalf("an inactive member must never be attempted, got %d hits", env.hitsFor("dec-off"))
	}
	if env.hitsFor("dec-on") != 1 {
		t.Fatalf("the active member must serve, got %d hits", env.hitsFor("dec-on"))
	}
}

// TestDecision_Group_MemberNeverAutoEnabled — adding a member to a
// group never flips enabled.
func TestDecision_Group_MemberNeverAutoEnabled(t *testing.T) {
	env := newGroupEnv(t, []groupMemberFixture{
		{ID: "dec-off", Weight: 1, Enabled: false, Status: 200, Body: decisionBody()},
	})
	for _, row := range env.modelsSeam.rows {
		if row.ID == "dec-off" && row.Enabled {
			t.Fatal("adding a member to a group must never auto-enable it")
		}
	}
}

// TestDecision_Group_ChainTargetNotConflatedWithMember — chain
// targets and group members are TWO populations: the chain clause is
// vacuous on this lane and is never consulted.
func TestDecision_Group_ChainTargetNotConflatedWithMember(t *testing.T) {
	env := newGroupEnv(t, []groupMemberFixture{
		{ID: "dec-a", Weight: 1, Enabled: true, Status: 200, Body: decisionBody()},
	})
	// A chain target on the member row is NOT consulted by the lane
	// (the chain clause is vacuous for decision models).
	env.modelsSeam.rows[0].FallbackChain = []string{"some-chain-target"}
	rec := env.send(t)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the chain clause must not be consulted); body=%s", rec.Code, rec.Body.String())
	}
	if env.hitsFor("dec-a") != 1 {
		t.Fatalf("the member must be attempted exactly once, got %d hits", env.hitsFor("dec-a"))
	}
}

// TestDecision_Group_SingleMemberGroup_FullyConsumable — a
// single-member, single-credential group is fully valid and fully
// consumable: one attempt, then exhaustion (no hedge engine).
func TestDecision_Group_SingleMemberGroup_FullyConsumable(t *testing.T) {
	env := newGroupEnv(t, []groupMemberFixture{
		{ID: "dec-only", Weight: 1, Enabled: true, Status: 500, Body: decisionErrorBody("boom", "server_error", "boom")},
	})
	rec := env.send(t)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rec.Code, rec.Body.String())
	}
	if env.hitsFor("dec-only") != 1 {
		t.Fatalf("the single member must be attempted exactly once, got %d hits", env.hitsFor("dec-only"))
	}
	_, _, code := decodeOpenAIError(t, rec.Body.Bytes())
	if code != "decision_all_members_failed" {
		t.Fatalf("code = %q, want decision_all_members_failed", code)
	}
}

// TestDecision_Group_Exhaustion_5xxMemberCountsAsGenuinelyFailed — a
// 5xx'd member counts as genuinely failed for this request.
func TestDecision_Group_Exhaustion_5xxMemberCountsAsGenuinelyFailed(t *testing.T) {
	env := newGroupEnv(t, []groupMemberFixture{
		{ID: "dec-a", Weight: 1, Enabled: true, Status: 503, Body: decisionErrorBody("boom", "server_error", "boom")},
	})
	rec := env.send(t)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rec.Code, rec.Body.String())
	}
	_, _, code := decodeOpenAIError(t, rec.Body.Bytes())
	if code != "decision_all_members_failed" {
		t.Fatalf("code = %q, want decision_all_members_failed", code)
	}
	if !strings.Contains(rec.Body.String(), "1 attempted") {
		t.Errorf("the 5xx'd member must count as a genuine attempt, got %s", rec.Body.String())
	}
}

// TestDecision_Group_5xx_AdvancesWithoutRotationOrMark — a 5xx
// advances to the next member immediately: no within-member rotation,
// no upstream-layer mark, no cooldown write.
func TestDecision_Group_5xx_AdvancesWithoutRotationOrMark(t *testing.T) {
	cache := skipcache.New()
	env := newGroupEnv(t, []groupMemberFixture{
		{ID: "dec-a", Weight: 1, Enabled: true, Status: 503, Body: decisionErrorBody("boom", "server_error", "boom")},
		{ID: "dec-b", Weight: 1, BackupOnly: true, Enabled: true, Status: 200, Body: decisionBody()},
	})
	env.handler.SetUpstreamSkipCache(cache)
	rec := env.send(t)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the 5xx advances to the next member); body=%s", rec.Code, rec.Body.String())
	}
	if env.hitsFor("dec-a") != 1 || env.hitsFor("dec-b") != 1 {
		t.Fatalf("both members must be attempted once: a=%d b=%d", env.hitsFor("dec-a"), env.hitsFor("dec-b"))
	}
	// No mark was written for the 5xx'd member.
	if cache.Marked(skipcache.ScopeCredential, "cred-dec-a", "gpt-6-luna") {
		t.Error("a 5xx must write NO upstream-layer mark")
	}
	if cache.Marked(skipcache.ScopeProvider, "openai", "gpt-6-luna") {
		t.Error("a 5xx must write NO provider-scope mark")
	}
}

// TestDecision_Group_StampedStatusDrivesClassification — the stamped
// upstream status drives the classification: a 429 produces a
// limit-class demotion (the marks ARE written), while a 400 writes NO
// mark (and fails fast).
func TestDecision_Group_StampedStatusDrivesClassification(t *testing.T) {
	cache := skipcache.New()
	env := newGroupEnv(t, []groupMemberFixture{
		{ID: "dec-a", Weight: 1, Enabled: true, InternalModel: "gpt-6-luna", Status: 429, Body: decisionErrorBody("rate_limit", "rate_limit_error", "slow down")},
		{ID: "dec-b", Weight: 1, BackupOnly: true, Enabled: true, InternalModel: "gpt-6-luna-b", Status: 200, Body: decisionBody()},
	})
	env.handler.SetUpstreamSkipCache(cache)
	rec := env.send(t)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !cache.Marked(skipcache.ScopeCredential, "cred-dec-a", "gpt-6-luna") {
		t.Error("a 429 must write the credential-scope mark")
	}
	if !cache.Marked(skipcache.ScopeProvider, "openai", "gpt-6-luna") {
		t.Error("a 429 must write the provider-scope mark")
	}

	// A 400 writes NO mark (the client-attributable class).
	cache2 := skipcache.New()
	env2 := newGroupEnv(t, []groupMemberFixture{
		{ID: "dec-a", Weight: 1, Enabled: true, Status: 400, Body: decisionErrorBody("bad_request", "invalid_request_error", "bad")},
	})
	env2.handler.SetUpstreamSkipCache(cache2)
	env2.send(t)
	if cache2.Marked(skipcache.ScopeCredential, "cred-dec-a", "gpt-6-luna") {
		t.Error("a 400 must write NO mark")
	}
}

// TestDecision_Group_400_413_415_422_FailFastAdvanceBlocked — the
// client-attributable classes fail fast (advancing would be thrash).
func TestDecision_Group_400_413_415_422_FailFastAdvanceBlocked(t *testing.T) {
	for _, status := range []int{400, 413, 415, 422} {
		env := newGroupEnv(t, []groupMemberFixture{
			{ID: "dec-a", Weight: 1, Enabled: true, Status: status, Body: decisionErrorBody("bad", "invalid_request_error", "bad")},
			{ID: "dec-b", Weight: 1, BackupOnly: true, Enabled: true, Status: 200, Body: decisionBody()},
		})
		rec := env.send(t)
		if rec.Code == http.StatusOK {
			t.Fatalf("status %d must NOT advance to the next member (fail fast); body=%s", status, rec.Body.String())
		}
		if env.hitsFor("dec-b") != 0 {
			t.Fatalf("status %d: the next member must not be attempted, got %d hits", status, env.hitsFor("dec-b"))
		}
	}
}

// TestDecision_Group_401_RotatesWithinMember — an upstream 401 is
// rotation-eligible: the member's next credential is attempted.
func TestDecision_Group_401_RotatesWithinMember(t *testing.T) {
	t.Run("single_credential", func(t *testing.T) {
		env := newGroupEnv(t, []groupMemberFixture{
			{ID: "dec-a", Weight: 1, Enabled: true, Status: 401, Body: decisionErrorBody("invalid_api_key", "authentication_error", "bad key")},
			{ID: "dec-b", Weight: 1, BackupOnly: true, Enabled: true, Status: 200, Body: decisionBody()},
		})
		rec := env.send(t)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		// The 401 is rotation-eligible but the member has ONE credential
		// ⇒ the walk advances to the next member.
		if env.hitsFor("dec-b") != 1 {
			t.Fatalf("the walk must advance after the member's resilience is consumed, got %d hits", env.hitsFor("dec-b"))
		}
	})

	t.Run("rotates_to_second_credential", func(t *testing.T) {
		cache := skipcache.New()
		engine := newStubCredEngine()
		env := newGroupEnv(t, []groupMemberFixture{
			{ID: "dec-a", Weight: 1, Enabled: true, Status: 401, Body: decisionErrorBody("invalid_api_key", "authentication_error", "bad key"), ExtraCredentialIDs: []string{"cred-dec-a-2"}},
			{ID: "dec-b", Weight: 1, BackupOnly: true, Enabled: true, Status: 200, Body: decisionBody()},
		})
		env.handler.SetUpstreamSkipCache(cache)
		env.handler.SetCredentialEngine(engine)

		rotated := false
		env.handler.SetCredentialResolver(func(id string) (models.ResolvedCredential, bool) {
			if id != "dec-a" && id != "dec-b" {
				return models.ResolvedCredential{}, false
			}
			credID := "cred-" + id
			if id == "dec-a" && rotated {
				credID = "cred-dec-a-2"
			}
			wireModel := "gpt-6-luna"
			return models.ResolvedCredential{
				Provider:      "openai",
				APIKey:        "sk-" + credID,
				BaseURL:       env.upstreams[id].URL,
				InternalModel: wireModel,
				CredentialID:  credID,
			}, true
		})
		engine.onExclude = func(modelID, credID string) {
			if modelID == "dec-a" {
				rotated = true
			}
		}

		rec := env.send(t)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		// The 401 rotates within the member: the first credential is
		// excluded and the second is attempted.
		if !engine.excludedPair("dec-a", "cred-dec-a") {
			t.Fatalf("the first credential must be excluded (rotated) on a 401")
		}
		if env.hitsFor("dec-a") != 2 {
			t.Fatalf("both credentials must be attempted (rotation), got %d hits", env.hitsFor("dec-a"))
		}
	})
}

// TestDecision_Group_PickAttemptSkipEventOutcomes — member_picked /
// member_attempted / member_suppressed are DISTINCT outcomes.
func TestDecision_Group_PickAttemptSkipEventOutcomes(t *testing.T) {
	cache := skipcache.New()
	env := newGroupEnv(t, []groupMemberFixture{
		{ID: "dec-a", Weight: 1, Enabled: true, ExtraCredentialIDs: []string{"cred-dec-a-2"}, Status: 200, Body: decisionBody()},
	})
	env.handler.SetUpstreamSkipCache(cache)
	// Mark the member's credential ⇒ the attempt is suppressed. (The
	// member carries a SECOND, available credential ⇒ it is not in the
	// D-C state — the escape applies only when EVERY credential of the
	// member is unavailable.)
	cache.Mark(skipcache.ScopeCredential, "cred-dec-a", "gpt-6-luna", time.Minute)
	env.send(t)
	if !env.hasOutcome("member_suppressed") {
		t.Fatalf("the suppression must publish member_suppressed; outcomes=%v", env.outcomes())
	}
	if env.hasOutcome("member_attempted") {
		t.Fatalf("a suppressed attempt must NOT publish member_attempted; outcomes=%v", env.outcomes())
	}
}

// stubCredEngine is the credential-engine stub for the rotation tests:
// it records the exclusions and reports the cooling state.
type stubCredEngine struct {
	mu       sync.Mutex
	excluded map[string]bool
	// onExclude is called on every exclusion (the rotation hook).
	onExclude func(modelID, credID string)
}

func newStubCredEngine() *stubCredEngine {
	return &stubCredEngine{excluded: map[string]bool{}}
}

func (e *stubCredEngine) ExcludeAndReselectForClass(modelID, conversationKey, excludedCredID string, class credentiallb.FailureClass, retryAfter time.Duration) (string, credentiallb.ReselectMode) {
	e.mu.Lock()
	e.excluded[modelID+"|"+excludedCredID] = true
	hook := e.onExclude
	e.mu.Unlock()
	if hook != nil {
		hook(modelID, excludedCredID)
	}
	return "", credentiallb.ReselectNone
}

func (e *stubCredEngine) CooldownUntil(modelID, credID string) (time.Time, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.excluded[modelID+"|"+credID] {
		return time.Now().Add(time.Minute), true
	}
	return time.Time{}, false
}

// excluded reports whether a (model, credential) pair was excluded.
func (e *stubCredEngine) excludedPair(modelID, credID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.excluded[modelID+"|"+credID]
}

// TestDecision_Group_PolicyPick_IgnoresMarks — a mark on the head does
// NOT change which member is the head: the head reported in telemetry
// is the POLICY head (the pick reads no failure state).
func TestDecision_Group_PolicyPick_IgnoresMarks(t *testing.T) {
	cache := skipcache.New()
	env := newGroupEnv(t, []groupMemberFixture{
		{ID: "dec-a", Weight: 1, Enabled: true, ExtraCredentialIDs: []string{"cred-dec-a-2"}, Status: 200, Body: decisionBody()},
		{ID: "dec-b", Weight: 1, BackupOnly: true, Enabled: true, Status: 200, Body: decisionBody()},
	})
	env.handler.SetUpstreamSkipCache(cache)
	// Mark the default member BEFORE the request. (Its second
	// credential is available ⇒ the member is not in the D-C state.)
	cache.Mark(skipcache.ScopeCredential, "cred-dec-a", "gpt-6-luna", time.Minute)

	// The pick is policy: the head is still dec-a (the default pool),
	// so the suppressed attempt is dec-a's — and the backup member is
	// NOT promoted into the head.
	rec := env.send(t)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	env.drainEvents()
	sawSuppressedA := false
	for _, ev := range env.events {
		data := eventData(ev)
		if data["outcome"] == "member_suppressed" && data["member"] == "dec-a" {
			sawSuppressedA = true
		}
	}
	if !sawSuppressedA {
		t.Fatalf("the marked member must be the suppressed one; outcomes=%v", env.outcomes())
	}
}

// TestDecision_Group_MarkedHead_ZeroUpstreamCallSkip — a marked
// member's attempt is suppressed: ZERO upstream hits, the event shows
// member_suppressed, and the next candidate is attempted.
func TestDecision_Group_MarkedHead_ZeroUpstreamCallSkip(t *testing.T) {
	cache := skipcache.New()
	env := newGroupEnv(t, []groupMemberFixture{
		{ID: "dec-a", Weight: 1, Enabled: true, ExtraCredentialIDs: []string{"cred-dec-a-2"}, Status: 200, Body: decisionBody()},
		{ID: "dec-b", Weight: 1, BackupOnly: true, Enabled: true, Status: 200, Body: decisionBody()},
	})
	env.handler.SetUpstreamSkipCache(cache)
	cache.Mark(skipcache.ScopeCredential, "cred-dec-a", "gpt-6-luna", time.Minute)

	rec := env.send(t)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if env.hitsFor("dec-a") != 0 {
		t.Fatalf("the suppressed member must NOT reach the upstream, got %d hits", env.hitsFor("dec-a"))
	}
	if env.hitsFor("dec-b") != 1 {
		t.Fatalf("the next candidate must be attempted, got %d hits", env.hitsFor("dec-b"))
	}
	if !env.hasOutcome("member_suppressed") {
		t.Fatalf("the suppression must publish member_suppressed; outcomes=%v", env.outcomes())
	}
}

// TestDecision_Group_AllMarked_PolicyHeadHonestAttempt — with every
// candidate marked, the POLICY HEAD is attempted exactly once (the
// suppression never removes the last attempt).
func TestDecision_Group_AllMarked_PolicyHeadHonestAttempt(t *testing.T) {
	cache := skipcache.New()
	env := newGroupEnv(t, []groupMemberFixture{
		{ID: "dec-a", Weight: 1, Enabled: true, Status: 200, Body: decisionBody()},
		{ID: "dec-b", Weight: 1, BackupOnly: true, Enabled: true, Status: 200, Body: decisionBody()},
	})
	env.handler.SetUpstreamSkipCache(cache)
	cache.Mark(skipcache.ScopeCredential, "cred-dec-a", "gpt-6-luna", time.Minute)
	cache.Mark(skipcache.ScopeCredential, "cred-dec-b", "gpt-6-luna", time.Minute)

	rec := env.send(t)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the policy head gets its honest attempt); body=%s", rec.Code, rec.Body.String())
	}
	if total := env.totalHits(); total != 1 {
		t.Fatalf("exactly ONE honest attempt was expected, got %d upstream hits", total)
	}
	if env.hitsFor("dec-a") != 1 {
		t.Fatalf("the POLICY HEAD (the default-pool member) must be the honest attempt, got %d hits on dec-a", env.hitsFor("dec-a"))
	}
}

// TestDecision_Group_AttemptOrder_PolicyOrderMinusMarked — the attempt
// sequence is the policy order MINUS the marked candidates — no
// reordering.
func TestDecision_Group_AttemptOrder_PolicyOrderMinusMarked(t *testing.T) {
	cache := skipcache.New()
	env := newGroupEnv(t, []groupMemberFixture{
		{ID: "dec-a", Weight: 1, Enabled: true, ExtraCredentialIDs: []string{"cred-dec-a-2"}, Status: 500, Body: decisionErrorBody("boom", "server_error", "boom")},
		{ID: "dec-b", Weight: 1, BackupOnly: true, Enabled: true, Status: 500, Body: decisionErrorBody("boom", "server_error", "boom")},
		{ID: "dec-c", Weight: 1, BackupOnly: true, Enabled: true, Status: 200, Body: decisionBody()},
	})
	env.handler.SetUpstreamSkipCache(cache)
	// dec-a is marked ⇒ it is skipped; the order of the REST is the
	// policy order (dec-b, then dec-c).
	cache.Mark(skipcache.ScopeCredential, "cred-dec-a", "gpt-6-luna", time.Minute)

	rec := env.send(t)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if env.hitsFor("dec-a") != 0 {
		t.Fatalf("the marked member must not be attempted, got %d hits", env.hitsFor("dec-a"))
	}
	if env.hitsFor("dec-b") != 1 {
		t.Fatalf("the next policy member must be attempted, got %d hits", env.hitsFor("dec-b"))
	}
	if env.hitsFor("dec-c") != 1 {
		t.Fatalf("the last policy member must be attempted, got %d hits", env.hitsFor("dec-c"))
	}
}

// TestDecision_Group_Exhaustion_ExcludesSuppressedAttempts — the
// exhaustion event's attempted counts GENUINE attempts only; the
// skipped ones are reported separately.
func TestDecision_Group_Exhaustion_ExcludesSuppressedAttempts(t *testing.T) {
	cache := skipcache.New()
	env := newGroupEnv(t, []groupMemberFixture{
		{ID: "dec-a", Weight: 1, Enabled: true, Status: 500, Body: decisionErrorBody("boom", "server_error", "boom")},
		{ID: "dec-b", Weight: 1, BackupOnly: true, Enabled: true, Status: 500, Body: decisionErrorBody("boom", "server_error", "boom"), ExtraCredentialIDs: []string{"cred-dec-b-2"}},
	})
	env.handler.SetUpstreamSkipCache(cache)
	// Mark ONLY cred-dec-b (not cred-dec-b-2) so dec-b is genuinely
	// suppressed but NOT in the D-C state (it still has a usable
	// credential).
	cache.Mark(skipcache.ScopeCredential, "cred-dec-b", "gpt-6-luna", time.Minute)

	rec := env.send(t)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (genuine failure); body=%s", rec.Code, rec.Body.String())
	}
	env.drainEvents()
	foundEnvelope := false
	attemptedEvents := 0
	suppressedEvents := 0
	var failFastAttempted, failFastSkipped, failFastMembersAttempted int
	foundFailFast := false
	for _, ev := range env.events {
		data := eventData(ev)
		if data["outcome"] == "all_members_failed" {
			foundEnvelope = true
		}
		if data["outcome"] == "member_attempted" {
			attemptedEvents++
		}
		if data["outcome"] == "member_suppressed" {
			suppressedEvents++
		}
		// The fail-fast event carries the accounting counters.
		if v, ok := data["attempted"]; ok {
			if ai, ok := v.(int); ok {
				failFastAttempted = ai
				foundFailFast = true
			}
		}
		if v, ok := data["skipped"]; ok {
			if si, ok := v.(int); ok {
				failFastSkipped = si
			}
		}
		if v, ok := data["members_attempted"]; ok {
			if mi, ok := v.(int); ok {
				failFastMembersAttempted = mi
			}
		}
	}
	// With B1 fixed, a suppressed-only member prevents the exhaustion
	// envelope from firing (the request fails with the genuine failure).
	if foundEnvelope {
		t.Fatalf("the exhaustion envelope must NOT fire when a member was only suppressed (never attempted)")
	}
	// Exact event-count accounting (R3): exactly one genuine attempt
	// and exactly one suppressed attempt.
	if attemptedEvents != 1 {
		t.Fatalf("member_attempted events = %d, want 1 (the suppressed member must not be counted as attempted)", attemptedEvents)
	}
	if suppressedEvents != 1 {
		t.Fatalf("member_suppressed events = %d, want 1", suppressedEvents)
	}
	// The fail-fast event carries the accounting counters (R4).
	if !foundFailFast {
		t.Fatalf("no fail-fast event carried the accounting counters")
	}
	if failFastAttempted != 1 {
		t.Fatalf("fail-fast attempted = %d, want 1", failFastAttempted)
	}
	if failFastSkipped != 1 {
		t.Fatalf("fail-fast skipped = %d, want 1", failFastSkipped)
	}
	if failFastMembersAttempted != 1 {
		t.Fatalf("fail-fast members_attempted = %d, want 1", failFastMembersAttempted)
	}

	// R-4: the EXHAUSTION ENVELOPE's payload carries the same
	// accounting counters (the shared builder — the two payloads can
	// never drift).
	t.Run("exhaustion_envelope_counters", func(t *testing.T) {
		cache := skipcache.New()
		env := newGroupEnv(t, []groupMemberFixture{
			{ID: "dec-a", Weight: 1, Enabled: true, Status: 500, Body: decisionErrorBody("boom", "server_error", "boom")},
			{ID: "dec-b", Weight: 1, BackupOnly: true, Enabled: true, Status: 500, Body: decisionErrorBody("boom", "server_error", "boom")},
		})
		env.handler.SetUpstreamSkipCache(cache)

		rec := env.send(t)
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("status = %d, want 502 (the exhaustion envelope); body=%s", rec.Code, rec.Body.String())
		}
		env.drainEvents()
		found := false
		var attempted, skipped, membersAttempted int
		for _, ev := range env.events {
			data := eventData(ev)
			if data["outcome"] != "all_members_failed" {
				continue
			}
			found = true
			if v, ok := data["attempted"].(int); ok {
				attempted = v
			}
			if v, ok := data["skipped"].(int); ok {
				skipped = v
			}
			if v, ok := data["members_attempted"].(int); ok {
				membersAttempted = v
			}
		}
		if !found {
			t.Fatalf("the exhaustion envelope event was not published")
		}
		if attempted != 2 {
			t.Fatalf("exhaustion attempted = %d, want 2", attempted)
		}
		if skipped != 0 {
			t.Fatalf("exhaustion skipped = %d, want 0", skipped)
		}
		if membersAttempted != 2 {
			t.Fatalf("exhaustion members_attempted = %d, want 2", membersAttempted)
		}
	})

	// R-5: the CLIENT-ATTRIBUTABLE fail-fast path reports the honest
	// attempt it made — the accounting is hoisted BEFORE the attempt,
	// so the fail-fast can never under-report it.
	t.Run("client_attributable_counters", func(t *testing.T) {
		cache := skipcache.New()
		env := newGroupEnv(t, []groupMemberFixture{
			{ID: "dec-a", Weight: 1, Enabled: true, Status: 400, Body: decisionErrorBody("bad_request", "invalid_request_error", "bad request"), ExtraCredentialIDs: []string{"cred-dec-a-2"}},
		})
		env.handler.SetUpstreamSkipCache(cache)
		// Mark ONLY cred-dec-a: the member is suppressed (it still has
		// a usable credential), so the OQ-1 lift grants the head its
		// ONE honest attempt — which the upstream answers with a
		// client-attributable 400.
		cache.Mark(skipcache.ScopeCredential, "cred-dec-a", "gpt-6-luna", time.Minute)

		rec := env.send(t)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (the client-attributable fail-fast); body=%s", rec.Code, rec.Body.String())
		}
		env.drainEvents()
		var attempted, skipped, membersAttempted int
		found := false
		for _, ev := range env.events {
			data := eventData(ev)
			if v, ok := data["attempted"]; ok {
				if ai, ok := v.(int); ok {
					attempted = ai
					found = true
				}
			}
			if v, ok := data["skipped"]; ok {
				if si, ok := v.(int); ok {
					skipped = si
				}
			}
			if v, ok := data["members_attempted"]; ok {
				if mi, ok := v.(int); ok {
					membersAttempted = mi
				}
			}
		}
		if !found {
			t.Fatalf("the fail-fast event did not carry the accounting counters")
		}
		if attempted != 1 {
			t.Fatalf("fail-fast attempted = %d, want 1 (the honest attempt is a real attempt)", attempted)
		}
		if skipped != 1 {
			t.Fatalf("fail-fast skipped = %d, want 1", skipped)
		}
		if membersAttempted != 1 {
			t.Fatalf("fail-fast members_attempted = %d, want 1", membersAttempted)
		}
	})
}

// TestDecision_Group_WinClear_ClearsWinnerNotFailedHead — the WINNER's
// marks are cleared; the FAILED head's marks survive.
func TestDecision_Group_WinClear_ClearsWinnerNotFailedHead(t *testing.T) {
	// (a) The WINNER's marks are cleared: with every candidate marked,
	// the policy head gets its honest attempt — and the win clears the
	// head's marks.
	cache := skipcache.New()
	env := newGroupEnv(t, []groupMemberFixture{
		{ID: "dec-a", Weight: 1, Enabled: true, InternalModel: "model-a", Status: 200, Body: decisionBody()},
		{ID: "dec-b", Weight: 1, BackupOnly: true, Enabled: true, InternalModel: "model-b", Status: 200, Body: decisionBody()},
	})
	env.handler.SetUpstreamSkipCache(cache)
	cache.Mark(skipcache.ScopeCredential, "cred-dec-a", "model-a", time.Minute)
	cache.Mark(skipcache.ScopeCredential, "cred-dec-b", "model-b", time.Minute)

	rec := env.send(t)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if cache.Marked(skipcache.ScopeCredential, "cred-dec-a", "model-a") {
		t.Error("the WINNER's mark must be cleared by the win")
	}

	// (b) The FAILED head's marks SURVIVE the win: a limit-class
	// failure writes the head's mark, and the next member's win must
	// not clear it.
	cache2 := skipcache.New()
	env2 := newGroupEnv(t, []groupMemberFixture{
		{ID: "dec-a", Weight: 1, Enabled: true, InternalModel: "model-a", Status: 429, Body: decisionErrorBody("rate_limit", "rate_limit_error", "slow down")},
		{ID: "dec-b", Weight: 1, BackupOnly: true, Enabled: true, InternalModel: "model-b", Status: 200, Body: decisionBody()},
	})
	env2.handler.SetUpstreamSkipCache(cache2)
	rec2 := env2.send(t)
	if rec2.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec2.Code, rec2.Body.String())
	}
	if !cache2.Marked(skipcache.ScopeCredential, "cred-dec-a", "model-a") {
		t.Error("the FAILED head's mark must survive the win")
	}
}

// TestDecision_Group_NoCooldownWriteOnSuppressedPick — a SUPPRESSED
// pick writes no cooldown and no engine state.
func TestDecision_Group_NoCooldownWriteOnSuppressedPick(t *testing.T) {
	cache := skipcache.New()
	engine := newStubCredEngine()
	env := newGroupEnv(t, []groupMemberFixture{
		{ID: "dec-a", Weight: 1, Enabled: true, Status: 500, Body: decisionErrorBody("boom", "server_error", "boom")},
		{ID: "dec-b", Weight: 1, BackupOnly: true, Enabled: true, Status: 200, Body: decisionBody()},
	})
	env.handler.SetUpstreamSkipCache(cache)
	env.handler.SetCredentialEngine(engine)
	cache.Mark(skipcache.ScopeCredential, "cred-dec-a", "gpt-6-luna", time.Minute)

	rec := env.send(t)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if engine.excludedPair("dec-a", "cred-dec-a") {
		t.Error("a suppressed pick must write NO credential cooldown")
	}
}

// TestDecision_Group_AllCredentialsCooling_PolicyFirstCredentialHonestAttempt
// — with EVERY credential of a member cooling, the member still gets
// exactly ONE honest attempt (the cache is never a failure oracle).
func TestDecision_Group_AllCredentialsCooling_PolicyFirstCredentialHonestAttempt(t *testing.T) {
	// The D-C escape (ADR-15): when EVERY credential of a member is
	// UNAVAILABLE — cooling (the credential engine's rotation state)
	// OR marked in the UPSTREAM layer (the shared, process-wide skip
	// cache) — the member still gets EXACTLY ONE honest attempt on its
	// POLICY-FIRST credential. The cache is never a failure oracle at
	// any layer: a mark/cooling record cannot, by itself, remove the
	// member's last attempt.
	//
	// BOTH unavailability kinds are covered here (subtests — the
	// top-level name count stays at 74).
	runCase := func(t *testing.T, seed func(engine *stubCredEngine, cache *skipcache.Cache)) {
		t.Helper()
		cache := skipcache.New()
		engine := newStubCredEngine()
		env := newGroupEnv(t, []groupMemberFixture{
			// The D-C member: TWO credential refs — BOTH are made
			// unavailable by the subtest's seed.
			{ID: "dec-a", Weight: 1, Enabled: true, ExtraCredentialIDs: []string{"cred-dec-a-2"}, Status: 503, Body: decisionErrorBody("boom", "server_error", "boom")},
			// The next member (the backup pool) — it serves after the
			// D-C member's genuine failure.
			{ID: "dec-b", Weight: 1, BackupOnly: true, Enabled: true, Status: 200, Body: decisionBody()},
		})
		env.handler.SetUpstreamSkipCache(cache)
		env.handler.SetCredentialEngine(engine)
		seed(engine, cache)

		rec := env.send(t)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (the D-C member's honest attempt failed ⇒ the member failed ⇒ the loop advanced); body=%s", rec.Code, rec.Body.String())
		}
		// EXACTLY ONE upstream call: the honest attempt happened (and
		// the 5xx advances immediately — no within-member rotation).
		if env.hitsFor("dec-a") != 1 {
			t.Fatalf("the D-C member must get exactly ONE honest attempt, got %d upstream hits", env.hitsFor("dec-a"))
		}
		// ON THE POLICY-FIRST CREDENTIAL: the upstream saw that
		// credential's bearer (the first ref by position).
		if got := env.fakes["dec-a"].lastAuthH; got != "Bearer sk-cred-dec-a" {
			t.Fatalf("the honest attempt must use the member's POLICY-FIRST credential, got %q", got)
		}
		// The genuine per-AM-4 failure ⇒ the member failed and the
		// loop advanced to the next member.
		if env.hitsFor("dec-b") != 1 {
			t.Fatalf("the loop must advance to the next member after the D-C member's genuine failure, got %d hits", env.hitsFor("dec-b"))
		}
	}

	t.Run("cooling", func(t *testing.T) {
		// EVERY credential of the member is COOLING (the credential
		// engine's own rotation state).
		runCase(t, func(engine *stubCredEngine, cache *skipcache.Cache) {
			engine.excluded["dec-a|cred-dec-a"] = true
			engine.excluded["dec-a|cred-dec-a-2"] = true
		})
	})

	t.Run("marked", func(t *testing.T) {
		// EVERY credential of the member is MARKED in the UPSTREAM
		// layer (the shared, process-wide skip cache — the
		// credential-scope mark).
		runCase(t, func(engine *stubCredEngine, cache *skipcache.Cache) {
			cache.Mark(skipcache.ScopeCredential, "cred-dec-a", "gpt-6-luna", time.Minute)
			cache.Mark(skipcache.ScopeCredential, "cred-dec-a-2", "gpt-6-luna", time.Minute)
		})
	})

	// ─────────────────────────────────────────────────────────────────
	// The mark-KEY chain — the WRITE side and the READ side must use
	// ONE key, by construction:
	//
	//	row.InternalModel  ->  memberID
	//
	// This asserts it MECHANICALLY (the shared cache is the
	// observation point), not by inspection:
	//
	//	(1) the WRITE path's ACTUAL key — the credential mark the lane
	//	    stored after a real limit-class failure — is the chain key
	//	    (and is NOT any other key), and
	//	(2) the READ path — the D-C precondition's own reader — FINDS
	//	    the mark the WRITE path stored (with the member's rotation
	//	    state cleared, so the mark alone decides) ⇒ the two keys
	//	    are equal.
	//
	// Two row shapes are covered: the validated row (an internal model
	// set) and the degenerate row (an empty internal model — reachable
	// only outside the validated path, which is precisely why the
	// chain must not depend on it).
	// ─────────────────────────────────────────────────────────────────
	runKeyCase := func(t *testing.T, memberID, rowInternalModel, wantKey, otherKey string) {
		t.Helper()
		cache := skipcache.New()
		engine := newStubCredEngine()
		env := newGroupEnv(t, []groupMemberFixture{
			// ONE credential (so "every credential unavailable" is
			// decidable) and a LIMIT-CLASS answer (429) so the write
			// path really runs and really stores a mark.
			{ID: memberID, Weight: 1, Enabled: true, Status: 429, Body: decisionErrorBody("rate_limit", "rate_limit_error", "slow down")},
			{ID: "dec-next", Weight: 1, BackupOnly: true, Enabled: true, Status: 200, Body: decisionBody()},
		})
		env.handler.SetUpstreamSkipCache(cache)
		env.handler.SetCredentialEngine(engine)

		// The member ROW's internal model — the chain's head. The
		// degenerate case blanks it (a legacy hand-edited row); the
		// credential's internal model stays as the harness resolved
		// it, so the two sides COULD disagree.
		for i := range env.modelsSeam.rows {
			if env.modelsSeam.rows[i].ID == memberID {
				env.modelsSeam.rows[i].InternalModel = rowInternalModel
			}
		}

		env.send(t)

		credID := "cred-" + memberID
		// (1) The WRITE path's ACTUAL key.
		if !cache.Marked(skipcache.ScopeCredential, credID, wantKey) {
			t.Fatalf("the write path must store the credential mark under the chain key %q", wantKey)
		}
		if cache.Marked(skipcache.ScopeCredential, credID, otherKey) {
			t.Fatalf("the write path must not store the credential mark under %q — the chain is a SINGLE key", otherKey)
		}
		// (2) The READ path finds the mark the WRITE path stored.
		// Clear the member's rotation state first: the D-C
		// precondition then depends on the MARK ALONE, so the read
		// path's key is what decides the outcome.
		engine.mu.Lock()
		engine.excluded = map[string]bool{}
		engine.mu.Unlock()
		if !env.handler.memberFullyUnavailable(memberID) {
			t.Fatalf("the read path must find the mark the write path stored (key equality) for member %q", memberID)
		}
	}

	t.Run("mark_key_equality", func(t *testing.T) {
		t.Run("validated_row", func(t *testing.T) {
			// The row's internal model is set ⇒ the chain's head is the
			// internal model; the member id is NOT used.
			runKeyCase(t, "dec-a", "gpt-6-luna", "gpt-6-luna", "dec-a")
		})
		t.Run("degenerate_row", func(t *testing.T) {
			// The row's internal model is EMPTY ⇒ the chain falls back
			// to the member id. The credential's internal model (the
			// OLD write key) must NOT be used — the write key and the
			// read key would then disagree.
			runKeyCase(t, "dec-a", "", "dec-a", "gpt-6-luna")
		})
	})

	// RC-1: the COOLING-ONLY shape — the member's credentials are
	// unavailable through the credential ENGINE's cooldown (zero
	// marks). The D-C escape must apply here too (the cooling shape
	// is the routinely-reachable one: a quota cooldown is 15m against
	// a 60s mark TTL, and a 401/403 rotation cools without writing
	// any mark).
	t.Run("cooling_only_latch", func(t *testing.T) {
		cache := skipcache.New()
		engine := newStubCredEngine()
		env := newGroupEnv(t, []groupMemberFixture{
			{ID: "dec-a", Weight: 1, Enabled: true, Status: 429, Body: decisionErrorBody("rate_limit", "rate_limit_error", "slow down"), InternalModel: "model-a", ExtraCredentialIDs: []string{"cred-dec-a-2"}},
			{ID: "dec-b", Weight: 1, BackupOnly: true, Enabled: true, Status: 200, Body: decisionBody(), InternalModel: "model-b"},
		})
		env.handler.SetUpstreamSkipCache(cache)
		env.handler.SetCredentialEngine(engine)
		// BOTH credentials COOLING (engine cooldown) — ZERO marks.
		engine.excluded["dec-a|cred-dec-a"] = true
		engine.excluded["dec-a|cred-dec-a-2"] = true

		rotated := false
		env.handler.SetCredentialResolver(func(id string) (models.ResolvedCredential, bool) {
			if id != "dec-a" && id != "dec-b" {
				return models.ResolvedCredential{}, false
			}
			credID := "cred-" + id
			if id == "dec-a" && rotated {
				credID = "cred-dec-a-2"
			}
			wireModel := "model-a"
			if id == "dec-b" {
				wireModel = "model-b"
			}
			return models.ResolvedCredential{
				Provider:      "openai",
				APIKey:        "sk-" + credID,
				BaseURL:       env.upstreams[id].URL,
				InternalModel: wireModel,
				CredentialID:  credID,
			}, true
		})
		engine.onExclude = func(modelID, credID string) {
			if modelID == "dec-a" {
				rotated = true
			}
		}

		rec := env.send(t)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		// EXACTLY ONE upstream hit: the cooling shape gets ONE honest
		// attempt — the AM-4 rotation must NOT re-fire the escape (a
		// second call on a cooling credential is a billed call).
		if env.hitsFor("dec-a") != 1 {
			t.Fatalf("the D-C escape must grant exactly ONE honest attempt for the cooling shape, got %d hits", env.hitsFor("dec-a"))
		}
		// On the POLICY-FIRST credential.
		if got := env.fakes["dec-a"].lastAuthH; got != "Bearer sk-cred-dec-a" {
			t.Fatalf("the honest attempt must use the member's POLICY-FIRST credential, got %q", got)
		}
	})

	// R-2(b): the PER-MEMBER latch alone — the SAME member must not
	// get a second honest attempt across its rotation.
	t.Run("per_member_latch", func(t *testing.T) {
		cache := skipcache.New()
		engine := newStubCredEngine()
		env := newGroupEnv(t, []groupMemberFixture{
			{ID: "dec-a", Weight: 1, Enabled: true, Status: 429, Body: decisionErrorBody("rate_limit", "rate_limit_error", "slow down"), InternalModel: "model-a", ExtraCredentialIDs: []string{"cred-dec-a-2"}},
			{ID: "dec-b", Weight: 1, BackupOnly: true, Enabled: true, Status: 200, Body: decisionBody(), InternalModel: "model-b"},
		})
		env.handler.SetUpstreamSkipCache(cache)
		env.handler.SetCredentialEngine(engine)
		engine.excluded["dec-a|cred-dec-a"] = true
		engine.excluded["dec-a|cred-dec-a-2"] = true

		rotated := false
		env.handler.SetCredentialResolver(func(id string) (models.ResolvedCredential, bool) {
			if id != "dec-a" && id != "dec-b" {
				return models.ResolvedCredential{}, false
			}
			credID := "cred-" + id
			if id == "dec-a" && rotated {
				credID = "cred-dec-a-2"
			}
			wireModel := "model-a"
			if id == "dec-b" {
				wireModel = "model-b"
			}
			return models.ResolvedCredential{
				Provider:      "openai",
				APIKey:        "sk-" + credID,
				BaseURL:       env.upstreams[id].URL,
				InternalModel: wireModel,
				CredentialID:  credID,
			}, true
		})
		engine.onExclude = func(modelID, credID string) {
			if modelID == "dec-a" {
				rotated = true
			}
		}

		rec := env.send(t)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		// The rotation DID re-offer the member's second credential …
		if !rotated {
			t.Fatalf("the limit-class failure must rotate the member's credential (the latch must be what stops the second attempt)")
		}
		// … and the latch denied the SECOND honest attempt on it.
		if env.hitsFor("dec-a") != 1 {
			t.Fatalf("the per-member latch must deny a second honest attempt, got %d hits", env.hitsFor("dec-a"))
		}
	})

	// R-2(a) + R-6: the PER-REQUEST cap alone — a LATER fully-unavailable
	// member is SKIPPED with ZERO upstream calls once a real attempt is
	// in flight (the cache can no longer be the sole cause of failure).
	t.Run("per_request_cap_zero_cost_skip", func(t *testing.T) {
		cache := skipcache.New()
		engine := newStubCredEngine()
		env := newGroupEnv(t, []groupMemberFixture{
			{ID: "dec-a", Weight: 1, Enabled: true, Status: 429, Body: decisionErrorBody("rate_limit", "rate_limit_error", "slow down"), InternalModel: "model-a", ExtraCredentialIDs: []string{"cred-dec-a-2"}},
			{ID: "dec-b", Weight: 1, BackupOnly: true, Enabled: true, Status: 429, Body: decisionErrorBody("rate_limit", "rate_limit_error", "slow down"), InternalModel: "model-b", ExtraCredentialIDs: []string{"cred-dec-b-2"}},
			{ID: "dec-c", Weight: 1, BackupOnly: true, Enabled: true, Status: 200, Body: decisionBody(), InternalModel: "model-c"},
		})
		env.handler.SetUpstreamSkipCache(cache)
		env.handler.SetCredentialEngine(engine)
		// BOTH members fully unavailable (all their credentials cooling).
		engine.excluded["dec-a|cred-dec-a"] = true
		engine.excluded["dec-a|cred-dec-a-2"] = true
		engine.excluded["dec-b|cred-dec-b"] = true
		engine.excluded["dec-b|cred-dec-b-2"] = true

		rec := env.send(t)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		// The FIRST fully-unavailable member (policy order) got the one
		// honest attempt …
		if env.hitsFor("dec-a") != 1 {
			t.Fatalf("the first fully-unavailable member must get exactly ONE honest attempt, got %d hits", env.hitsFor("dec-a"))
		}
		// … and the LATER fully-unavailable member was skipped with
		// ZERO upstream calls (the per-request cap).
		if env.hitsFor("dec-b") != 0 {
			t.Fatalf("a later fully-unavailable member must be skipped with ZERO upstream calls, got %d hits", env.hitsFor("dec-b"))
		}
		// The zero-cost skip is OBSERVABLE in the event stream.
		env.drainEvents()
		suppressedLater := false
		for _, ev := range env.events {
			data := eventData(ev)
			if data["outcome"] == "member_suppressed" {
				if member, ok := data["member"].(string); ok && member == "dec-b" {
					suppressedLater = true
				}
			}
		}
		if !suppressedLater {
			t.Fatalf("the later fully-unavailable member must publish a member_suppressed event")
		}
		// The winner's own call is the only other upstream call.
		if env.hitsFor("dec-c") != 1 {
			t.Fatalf("the healthy member must serve, got %d hits", env.hitsFor("dec-c"))
		}
		if env.totalHits() != 2 {
			t.Fatalf("only the one honest attempt and the winner may reach an upstream, got %d total hits", env.totalHits())
		}
	})

	t.Run("limit_class_latch", func(t *testing.T) {
		// B2: a LIMIT-CLASS failure on the D-C escape's single honest
		// attempt must END the member (no rotation, no second escape).
		cache := skipcache.New()
		engine := newStubCredEngine()
		env := newGroupEnv(t, []groupMemberFixture{
			{ID: "dec-a", Weight: 1, Enabled: true, Status: 429, Body: decisionErrorBody("rate_limit", "rate_limit_error", "slow down"), InternalModel: "model-a", ExtraCredentialIDs: []string{"cred-dec-a-2"}},
			{ID: "dec-b", Weight: 1, BackupOnly: true, Enabled: true, Status: 200, Body: decisionBody(), InternalModel: "model-b"},
		})
		env.handler.SetUpstreamSkipCache(cache)
		env.handler.SetCredentialEngine(engine)
		// Mark both credentials in the UPSTREAM layer so the member is
		// fully-unavailable via marks (not cooling).
		cache.Mark(skipcache.ScopeCredential, "cred-dec-a", "model-a", time.Minute)
		cache.Mark(skipcache.ScopeCredential, "cred-dec-a-2", "model-a", time.Minute)

		rotated := false
		env.handler.SetCredentialResolver(func(id string) (models.ResolvedCredential, bool) {
			if id != "dec-a" && id != "dec-b" {
				return models.ResolvedCredential{}, false
			}
			credID := "cred-" + id
			if id == "dec-a" && rotated {
				credID = "cred-dec-a-2"
			}
			wireModel := "model-a"
			if id == "dec-b" {
				wireModel = "model-b"
			}
			return models.ResolvedCredential{
				Provider:      "openai",
				APIKey:        "sk-" + credID,
				BaseURL:       env.upstreams[id].URL,
				InternalModel: wireModel,
				CredentialID:  credID,
			}, true
		})
		engine.onExclude = func(modelID, credID string) {
			if modelID == "dec-a" {
				rotated = true
			}
		}

		rec := env.send(t)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		if env.hitsFor("dec-a") != 1 {
			t.Fatalf("the D-C escape must grant exactly ONE honest attempt, got %d hits", env.hitsFor("dec-a"))
		}
		if env.hitsFor("dec-b") != 1 {
			t.Fatalf("the loop must advance after the honest attempt fails, got %d hits", env.hitsFor("dec-b"))
		}
	})
}

// TestDecision_Group_LayerSeparation_CredentialMarkCannotChangePick —
// a UPSTREAM-layer (credential) mark provably cannot alter the group
// pick: the pick reads no failure state. The MODEL layer's CHAIN
// reader is recorded ABSENT on this lane.
func TestDecision_Group_LayerSeparation_CredentialMarkCannotChangePick(t *testing.T) {
	cache := skipcache.New()
	env := newGroupEnv(t, []groupMemberFixture{
		{ID: "dec-a", Weight: 9, Enabled: true, ExtraCredentialIDs: []string{"cred-dec-a-2"}, Status: 200, Body: decisionBody()},
		{ID: "dec-b", Weight: 1, BackupOnly: true, Enabled: true, Status: 200, Body: decisionBody()},
	})
	env.handler.SetUpstreamSkipCache(cache)
	// Mark the heavy member: the pick (the policy head) must still be
	// that member — the mark cannot change WHICH member is the head.
	cache.Mark(skipcache.ScopeCredential, "cred-dec-a", "gpt-6-luna", time.Minute)
	env.send(t)
	env.drainEvents()
	head := ""
	for _, ev := range env.events {
		data := eventData(ev)
		if data["outcome"] == "member_suppressed" {
			if member, ok := data["member"].(string); ok {
				head = member
			}
		}
	}
	if head != "dec-a" {
		t.Fatalf("the policy head must be the heavy member (the mark cannot move it), got %q", head)
	}
}

// TestDecision_Group_CacheLayerSeparation_SharedUpstreamInstanceOneAnswer
// — the decision lane's UPSTREAM read is the SAME process-wide
// instance the text lane writes (one answer about one credential).
func TestDecision_Group_CacheLayerSeparation_SharedUpstreamInstanceOneAnswer(t *testing.T) {
	cache := skipcache.New()
	env := newGroupEnv(t, []groupMemberFixture{
		{ID: "dec-a", Weight: 1, Enabled: true, Status: 200, Body: decisionBody()},
	})
	env.handler.SetUpstreamSkipCache(cache)
	// A mark written by the OTHER lane (the same process-wide
	// instance) is visible to this lane — one answer.
	cache.Mark(skipcache.ScopeCredential, "cred-dec-a", "gpt-6-luna", time.Minute)
	rec := env.send(t)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the OQ-1 lift gives the head its honest attempt); body=%s", rec.Code, rec.Body.String())
	}
	if env.hitsFor("dec-a") != 1 {
		t.Fatalf("the honest attempt must happen exactly once, got %d hits", env.hitsFor("dec-a"))
	}
}

// TestDecision_Group_ChatInferredWithDecisionMember_NeverSilentlyServes
// — a group inferred as CHAT that contains a decision member never
// silently serves: the lane gate refuses it (400 decision_misroute).
func TestDecision_Group_ChatInferredWithDecisionMember_NeverSilentlyServes(t *testing.T) {
	env := newGroupEnv(t, []groupMemberFixture{
		{ID: "dec-a", Weight: 1, Enabled: true, Status: 200, Body: decisionBody()},
	})
	// The lane seam says CHAT (the mixed shape) ⇒ the group branch is
	// never entered (the fail-closed path).
	env.modelsSeam.lanes[env.group.ID] = models.GroupLaneChat
	rec := env.send(t)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (a chat-inferred group must never silently serve); body=%s", rec.Code, rec.Body.String())
	}
	_, _, code := decodeOpenAIError(t, rec.Body.Bytes())
	if code != "decision_misroute" {
		t.Fatalf("code = %q, want decision_misroute", code)
	}
	if env.totalHits() != 0 {
		t.Fatalf("no upstream may be called for a chat-inferred group, got %d hits", env.totalHits())
	}
}

// TestDecision_Group_MemberWithTwoCredentials_LimitClassRotatesBeforeAdvancing
// — a LIMIT-CLASS failure on credential #1 rotates to credential #2
// BEFORE the loop advances to the next member.
func TestDecision_Group_MemberWithTwoCredentials_LimitClassRotatesBeforeAdvancing(t *testing.T) {
	cache := skipcache.New()
	engine := newStubCredEngine()
	env := newGroupEnv(t, []groupMemberFixture{
		{ID: "dec-a", Weight: 1, Enabled: true, InternalModel: "model-a", Status: 429, Body: decisionErrorBody("rate_limit", "rate_limit_error", "slow down")},
		{ID: "dec-b", Weight: 1, BackupOnly: true, Enabled: true, InternalModel: "model-b", Status: 200, Body: decisionBody()},
	})
	env.handler.SetUpstreamSkipCache(cache)
	env.handler.SetCredentialEngine(engine)
	// The member's second credential becomes the pick after the
	// rotation (the engine's exclusion drives the re-resolve).
	rotated := false
	env.handler.SetCredentialResolver(func(id string) (models.ResolvedCredential, bool) {
		if id != "dec-a" && id != "dec-b" {
			return models.ResolvedCredential{}, false
		}
		credID := "cred-" + id
		if id == "dec-a" && rotated {
			credID = "cred-dec-a-2"
		}
		wireModel := "gpt-6-luna"
		if id == "dec-a" {
			wireModel = "model-a"
		} else if id == "dec-b" {
			wireModel = "model-b"
		}
		return models.ResolvedCredential{
			Provider:      "openai",
			APIKey:        "sk-cred",
			BaseURL:       env.upstreams[id].URL,
			InternalModel: wireModel,
			CredentialID:  credID,
		}, true
	})
	// The exclusion flips the pick to the member's second credential.
	engine.onExclude = func(modelID, credID string) {
		if modelID == "dec-a" {
			rotated = true
		}
	}

	rec := env.send(t)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !rotated {
		t.Fatal("the limit-class failure must rotate the member's credential")
	}
	if env.hitsFor("dec-a") != 2 {
		t.Fatalf("the member must be attempted on both credentials before advancing, got %d hits", env.hitsFor("dec-a"))
	}
	// The member's own resilience was consumed FIRST: both of its
	// credentials were attempted before the walk advanced (the next
	// member is attempted only after the member's resilience is
	// exhausted).
	if env.hitsFor("dec-b") != 1 {
		t.Fatalf("the walk advances only after the member's resilience is consumed, got %d hits on the next member", env.hitsFor("dec-b"))
	}
}

// TestDecision_Group_MemberFailedPredicate_OwnResilienceExhausted — the
// member-failed predicate is true only when the member's own
// resilience (its credential attempts) is exhausted.
func TestDecision_Group_MemberFailedPredicate_OwnResilienceExhausted(t *testing.T) {
	cache := skipcache.New()
	engine := newStubCredEngine()
	env := newGroupEnv(t, []groupMemberFixture{
		{ID: "dec-a", Weight: 1, Enabled: true, Status: 500, Body: decisionErrorBody("boom", "server_error", "boom")},
		{ID: "dec-b", Weight: 1, BackupOnly: true, Enabled: true, Status: 200, Body: decisionBody()},
	})
	env.handler.SetUpstreamSkipCache(cache)
	env.handler.SetCredentialEngine(engine)
	rec := env.send(t)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	// A 5xx does NOT rotate (no exclusion) — the member's resilience
	// is consumed by the single attempt and the walk advances.
	if engine.excludedPair("dec-a", "cred-dec-a") {
		t.Error("a 5xx must NOT rotate the member's credential")
	}
	if env.hitsFor("dec-b") != 1 {
		t.Fatalf("the walk must advance once the member failed, got %d hits", env.hitsFor("dec-b"))
	}
}

// TestDecision_DoctrineLine_PinnedVerbatimAtSeam — the extended
// doctrine line is present VERBATIM in the suppression seam's source.
func TestDecision_DoctrineLine_PinnedVerbatimAtSeam(t *testing.T) {
	const doctrine = "The cache is an optimization, not the truth: it may reorder candidates, never exclude them — and it\n//\tmay suppress an ATTEMPT, never a PICK or a CHAIN."
	src, err := os.ReadFile("suppression.go")
	if err != nil {
		t.Fatalf("read suppression.go: %v", err)
	}
	if !strings.Contains(string(src), doctrine) {
		t.Fatalf("the doctrine line must be present VERBATIM in suppression.go — the seam that decides attempt-vs-skip")
	}

	t.Run("policy_go_copy", func(t *testing.T) {
		// The doctrine's second verbatim copy (policy.go) must match.
		src2, err := os.ReadFile("policy.go")
		if err != nil {
			t.Fatalf("read policy.go: %v", err)
		}
		if !strings.Contains(string(src2), doctrine) {
			t.Fatalf("the doctrine line must be present VERBATIM in policy.go (the second production copy)")
		}
	})
}

// TestDecision_Group_ThroughCachedConfig — the PRODUCTION binding (a
// *modelscache.CachedModelsConfig) enters the group branch: the
// end-to-end decorator proof on the decision lane.
func TestDecision_Group_ThroughCachedConfig(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "group-cached.db")
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

	upstream, fake := newFakeUpstream(t, fakeScript{status: 200, body: decisionBody()})
	if err := mgr.AddCredential(models.CredentialConfig{ID: "cred-1", Provider: "openai", APIKey: "sk", BaseURL: upstream.URL}); err != nil {
		t.Fatalf("AddCredential: %v", err)
	}
	if err := mgr.AddModel(models.ModelConfig{
		ID: "dec-a", Name: "Decision A", Enabled: true, Kind: models.KindDecision,
		Internal: true, InternalModel: "gpt-6-luna",
		Credentials: []models.CredentialRef{{CredentialID: "cred-1", Weight: 1, Position: 0}},
	}); err != nil {
		t.Fatalf("seed decision member: %v", err)
	}
	if err := mgr.AddModel(models.ModelConfig{
		ID: "decision-group", Name: "Decision Group", Enabled: true, Kind: models.KindGroup, Internal: true,
		Members: []models.GroupMember{{ModelID: "dec-a", Weight: 1}},
	}); err != nil {
		t.Fatalf("seed decision group: %v", err)
	}

	cached, err := modelscache.NewCachedModelsConfig(mgr, modelscache.Options{ReconcileInterval: time.Hour})
	if err != nil {
		t.Fatalf("NewCachedModelsConfig: %v", err)
	}
	t.Cleanup(cached.Stop)

	// The handler is wired EXACTLY as production wires it.
	bus := events.NewBus()
	ts := newMemTokenStore()
	ts.add(&auth.AuthToken{ID: "tok-test", Name: "Test Token", TokenHash: auth.HashToken("sk-test-token")})
	h := NewHandler(stubConfig{timeout: 5 * time.Second}, bus, ts, nil)
	h.SetModelResolver(NewModelResolverFromConfig(func(modelID string) *models.ModelConfig {
		return cached.GetModel(modelID)
	}))
	h.SetCredentialResolver(NewCredentialResolverFromConfig(func(modelID string) (models.ResolvedCredential, bool) {
		return cached.ResolveInternalConfigWithAffinity(modelID, modelID)
	}))
	h.SetModelsConfig(cached)

	req := httptest.NewRequest(http.MethodPost, "/v1/decisions", strings.NewReader(standardRequestBody("decision-group")))
	req.Header.Set("Authorization", "Bearer sk-test-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.HandleDecision(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if fake.hitCount.Load() != 1 {
		t.Fatalf("the member must be attempted exactly once through the cached config, got %d hits", fake.hitCount.Load())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// PD-11 — disabled vs out-of-scope: NO LEAK (AUTH commission, T2.13)
// ─────────────────────────────────────────────────────────────────────────────

// TestDecision_DisabledVsOutOfScope_NoLeak pins the property the
// precedence order exists to satisfy (phase2-plan.md T2.13, pin table
// row `TestDecision_DisabledVsOutOfScope_NoLeak`): a request that is
// BOTH disabled and out of scope must produce ONE answer that does not
// reveal which condition applied.
//
// THE CORPUS PREMISES, as frozen (plan.md §9 + phase2-plan.md
// T2.13 ~532-536), and what LANDED code does with each:
//
//  1. "this codebase has no disabled-model rejection on any lane —
//     GetModel/GetModelNative return disabled rows for non-group ids
//     (pkg/store/database/store.go; pkg/proxy/handler_functions.go:174
//     'GetModel never filters enabled for non-group rows')". ⇒ VERIFIED
//     on this lane: a DISABLED standalone decision model is SERVED
//     (200) when in reach, so the disabled case produces no error to
//     leak. Sub-test B.
//  2. "a disabled MEMBER is invisible to buildGroupPolicy
//     (policy.go:76 `if !row.Enabled { continue }`)". ⇒ VERIFIED: the
//     member is never a candidate, so its counter is 0 and its state
//     never reaches the response. Sub-test A.
//  3. "all-disabled ⇒ 502; all-out-of-scope ⇒ 403 — distinguishable
//     BY DESIGN (different failures, different operator remedies)".
//     ⇒ VERIFIED, and the discriminator is `baseServable` counted on the
//     UNFILTERED row (group.go:179/:199): when the group has ZERO
//     base-servable members the empty order is AVAILABILITY (502) even
//     when the scope also excluded members. Sub-test C.
//  4. CORPUS DISCREPANCY — plan/phase2-plan assert "a disabled GROUP
//     is invisible to the claims index — so a disabled group resolves
//     as *unknown* (404)". ⇒ FALSE for a group named by its OWN id:
//     the claims index only governs SHADOWED ids, so a direct name
//     always resolves the row it names, and the handler never filters
//     `enabled` on the group arm ⇒ a disabled group named directly is
//     SERVED (verified against the production binding: sqlite store +
//     modelscache decorator + the resolver chain cmd/main.go wires).
//     Sub-test D pins the LANDED behaviour and asserts the part of the
//     premise that IS true (the disabled group's SHADOW CLAIM is
//     inactive). Reported as a behaviour/corpus finding, not silently
//     harmonised away.
func TestDecision_DisabledVsOutOfScope_NoLeak(t *testing.T) {
	// ── A: a disabled+out-of-scope MEMBER never leaks (healthy group) ────
	t.Run("member_both_disabled_and_out_of_scope_never_attempted_never_mentioned", func(t *testing.T) {
		// dec-both is disabled AND out of reach; dec-healthy is enabled and
		// granted. Both are BackupOnly=false EXCEPT dec-both, which is
		// BackupOnly so the healthy head is deterministic (the policy would
		// skip it anyway on `!row.Enabled`).
		env := newGroupEnv(t, []groupMemberFixture{
			{ID: "dec-healthy", Weight: 1, Enabled: true, Status: 200, Body: decisionBody()},
			{ID: "dec-both", Weight: 1, BackupOnly: true, Enabled: false, Status: 200, Body: decisionBody()},
		})
		env.tokenStore.tokens["tok-test"].AllowedModels = []string{env.group.ID, "dec-healthy"}
		rec := env.send(t)
		if rec.Code != http.StatusOK {
			t.Fatalf("disabled+out-of-scope member next to a healthy one: status = %d, want 200; body=%s",
				rec.Code, rec.Body.String())
		}
		if got := env.hitsFor("dec-healthy"); got != 1 {
			t.Errorf("the healthy in-reach member must serve exactly once, got %d hits", got)
		}
		if got := env.hitsFor("dec-both"); got != 0 {
			t.Errorf("the disabled+out-of-scope member must never be attempted, got %d hits — neither condition may leak as an attempt", got)
		}
		for _, forbidden := range []string{"dec-both", "access_denied", "decision_all_members_failed"} {
			if strings.Contains(rec.Body.String(), forbidden) {
				t.Errorf("the 200 body names %q (%s) — neither the disabled state nor the scope exclusion may appear in the response",
					forbidden, rec.Body.String())
			}
		}

		// THE CONTROL: swap the disabled member for an ENABLED, equally
		// out-of-scope one. The two answers must be byte-identical — that
		// is the "no distinguishing detail" property.
		control := newGroupEnv(t, []groupMemberFixture{
			{ID: "dec-healthy", Weight: 1, Enabled: true, Status: 200, Body: decisionBody()},
			{ID: "dec-other", Weight: 1, BackupOnly: true, Enabled: true, Status: 200, Body: decisionBody()},
		})
		control.tokenStore.tokens["tok-test"].AllowedModels = []string{control.group.ID, "dec-healthy"}
		crec := control.send(t)
		if crec.Code != http.StatusOK || crec.Body.String() != rec.Body.String() {
			t.Fatalf("the disabled member's state is DISTINGUISHABLE: disabled=%d/%s enabled-control=%d/%s",
				rec.Code, rec.Body.String(), crec.Code, crec.Body.String())
		}
		if got := control.hitsFor("dec-other"); got != 0 {
			t.Errorf("control: the out-of-scope member must never be attempted, got %d hits", got)
		}
	})

	// ── B: the MODEL arm — a disabled standalone model is SERVED ──────────
	t.Run("model_arm_disabled_standalone_is_served_so_no_error_can_leak", func(t *testing.T) {
		// Corpus premise 1 (plan.md §9 / T2.13): there is no disabled-model
		// rejection on this lane. A disabled model that IS in reach is
		// served — so on the model arm the disabled case produces no error
		// at all, and therefore nothing to leak.
		env := newEnvOpenAI(t, fakeScript{status: 200, body: openAIDecisionSuccessBody(10, 0)})
		env.handler.SetModelResolver(func(id string) (*models.ModelConfig, bool) {
			if id != "dec-disabled" {
				return nil, false
			}
			return &models.ModelConfig{ID: "dec-disabled", Name: "Disabled", Kind: models.KindDecision,
				Enabled: false, Internal: true, InternalModel: "gpt-6-luna",
				Credentials: models.TestRefs("cred-1")}, true
		})
		env.handler.SetCredentialResolver(func(id string) (models.ResolvedCredential, bool) {
			if id != "dec-disabled" {
				return models.ResolvedCredential{}, false
			}
			return models.ResolvedCredential{Provider: "openai", APIKey: "sk", BaseURL: env.upstream.URL,
				InternalModel: "gpt-6-luna", CredentialID: "cred-1"}, true
		})
		rec := env.sendRequest(http.MethodPost, standardRequestBody("dec-disabled"), nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("disabled standalone model (wildcard, in reach): status = %d, want 200 — "+
				"GetModel never filters enabled for non-group rows, so the disabled case is SERVED (T2.13 premise 1); body=%s",
				rec.Code, rec.Body.String())
		}
		if got := env.fake.hitCount.Load(); got != 1 {
			t.Errorf("disabled standalone model: upstream hits = %d, want 1 (it is served, not rejected)", got)
		}

		// THE LEAK TEST: restricted, the disabled model's answer must be
		// BYTE-IDENTICAL to an enabled out-of-scope model's answer — the
		// disabled flag is invisible behind the 403.
		disabledEnv := newEnvOpenAI(t, fakeScript{status: 200, body: openAIDecisionSuccessBody(10, 0)})
		disabledEnv.tokenStore.tokens["tok-test"].AllowedModels = []string{"some-other-model"}
		disabledEnv.handler.SetModelResolver(func(id string) (*models.ModelConfig, bool) {
			if id != "dec-x" {
				return nil, false
			}
			return &models.ModelConfig{ID: "dec-x", Name: "X", Kind: models.KindDecision, Enabled: false,
				Internal: true, InternalModel: "gpt-6-luna", Credentials: models.TestRefs("cred-1")}, true
		})
		drec := disabledEnv.sendRequest(http.MethodPost, standardRequestBody("dec-x"), nil)

		enabledEnv := newEnvOpenAI(t, fakeScript{status: 200, body: openAIDecisionSuccessBody(10, 0)})
		enabledEnv.tokenStore.tokens["tok-test"].AllowedModels = []string{"some-other-model"}
		enabledEnv.handler.SetModelResolver(func(id string) (*models.ModelConfig, bool) {
			if id != "dec-x" {
				return nil, false
			}
			return &models.ModelConfig{ID: "dec-x", Name: "X", Kind: models.KindDecision, Enabled: true,
				Internal: true, InternalModel: "gpt-6-luna", Credentials: models.TestRefs("cred-1")}, true
		})
		erec := enabledEnv.sendRequest(http.MethodPost, standardRequestBody("dec-x"), nil)

		if drec.Code != http.StatusForbidden {
			t.Fatalf("disabled + out-of-scope model: status = %d, want 403; body=%s", drec.Code, drec.Body.String())
		}
		if erec.Code != http.StatusForbidden {
			t.Fatalf("enabled + out-of-scope model: status = %d, want 403; body=%s", erec.Code, erec.Body.String())
		}
		if drec.Body.String() != erec.Body.String() {
			t.Fatalf("the disabled flag LEAKS through the 403: disabled=%s enabled=%s",
				drec.Body.String(), erec.Body.String())
		}
		if got := disabledEnv.fake.hitCount.Load(); got != 0 {
			t.Errorf("disabled + out-of-scope model: upstream hits = %d, want 0", got)
		}
		if got := enabledEnv.fake.hitCount.Load(); got != 0 {
			t.Errorf("enabled + out-of-scope model: upstream hits = %d, want 0", got)
		}
	})

	// ── C: all-disabled ⇒ 502 · all-out-of-scope ⇒ 403 (by design) ────────
	t.Run("group_arm_all_disabled_502_vs_all_out_of_scope_403", func(t *testing.T) {
		// All members DISABLED, group in reach (wildcard) ⇒ 502
		// decision_all_members_failed, ZERO attempts (unservability).
		offEnv := newGroupEnv(t, []groupMemberFixture{
			{ID: "dec-off-1", Weight: 1, Enabled: false, Status: 200, Body: decisionBody()},
			{ID: "dec-off-2", Weight: 1, BackupOnly: true, Enabled: false, Status: 200, Body: decisionBody()},
		})
		offRec := offEnv.send(t)
		if offRec.Code != http.StatusBadGateway {
			t.Fatalf("all-disabled group: status = %d, want 502; body=%s", offRec.Code, offRec.Body.String())
		}
		_, _, offCode := decodeOpenAIError(t, offRec.Body.Bytes())
		if offCode != "decision_all_members_failed" {
			t.Fatalf("all-disabled group: code = %q, want decision_all_members_failed", offCode)
		}
		if got := offEnv.totalHits(); got != 0 {
			t.Errorf("all-disabled group: upstream hits = %d, want 0 (no member is a candidate)", got)
		}

		// Every member ENABLED but out of reach (the group target itself is
		// granted — the D4 entry-only spelling) ⇒ 403, ZERO attempts.
		scopeEnv := newGroupEnv(t, []groupMemberFixture{
			{ID: "dec-on-1", Weight: 1, Enabled: true, Status: 200, Body: decisionBody()},
			{ID: "dec-on-2", Weight: 1, BackupOnly: true, Enabled: true, Status: 200, Body: decisionBody()},
		})
		scopeEnv.tokenStore.tokens["tok-test"].AllowedModels = []string{scopeEnv.group.ID}
		scopeRec := scopeEnv.send(t)
		assertAccessDenied403(t, scopeRec, scopeEnv.group.ID)
		if got := scopeEnv.totalHits(); got != 0 {
			t.Errorf("all-out-of-scope group: upstream hits = %d, want 0", got)
		}

		// THE TWO ARE DISTINGUISHABLE BY DESIGN — and each envelope carries
		// ONLY its own vocabulary (an availability failure never wears the
		// permission shape and vice versa).
		if strings.Contains(offRec.Body.String(), "access_denied") {
			t.Errorf("the 502 body carries access_denied (%s) — an availability failure must not wear the permission shape", offRec.Body.String())
		}
		if strings.Contains(scopeRec.Body.String(), "decision_all_members_failed") {
			t.Errorf("the 403 body carries decision_all_members_failed (%s) — a permission denial must not wear the availability shape", scopeRec.Body.String())
		}

		// THE CROSSOVER: members that are BOTH disabled and out of scope ⇒
		// 502, not 403. group.go:199 requires `baseServable > 0` for the
		// scope branch: a group with ZERO base-servable members is
		// UNSERVABLE even though the scope also excluded members
		// (availability, not permission).
		bothEnv := newGroupEnv(t, []groupMemberFixture{
			{ID: "dec-both-1", Weight: 1, Enabled: false, Status: 200, Body: decisionBody()},
		})
		bothEnv.tokenStore.tokens["tok-test"].AllowedModels = []string{bothEnv.group.ID}
		bothRec := bothEnv.send(t)
		if bothRec.Code != http.StatusBadGateway {
			t.Fatalf("disabled AND out-of-scope group: status = %d, want 502 (baseServable == 0 wins: availability, not permission); body=%s",
				bothRec.Code, bothRec.Body.String())
		}
		_, _, bothCode := decodeOpenAIError(t, bothRec.Body.Bytes())
		if bothCode != "decision_all_members_failed" {
			t.Fatalf("disabled AND out-of-scope group: code = %q, want decision_all_members_failed", bothCode)
		}
		if got := bothEnv.totalHits(); got != 0 {
			t.Errorf("disabled AND out-of-scope group: upstream hits = %d, want 0", got)
		}
	})

	// ── D: the group ROW's own disabled flag — LANDED behaviour, pinned ───
	t.Run("group_row_disabled_does_not_leak_either", func(t *testing.T) {
		// CORPUS DISCREPANCY (phase2-plan.md T2.13 ~534): the frozen text
		// says "a disabled group is invisible to the claims index — so a
		// disabled group resolves as *unknown* (404)". That is true only of
		// SHADOWED ids: a group named by its OWN id always resolves the row
		// it names, and neither the resolve nor the group arm reads
		// `enabled`. The landed behaviour — verified against the production
		// binding (sqlite store → modelscache decorator → the resolver chain
		// cmd/main.go wires) — is: a disabled group named directly is
		// SERVED, and its SHADOW CLAIM is inactive. This sub-test pins the
		// LANDED behaviour (reported as a corpus finding, never harmonised).
		dbPath := filepath.Join(t.TempDir(), "disabled-group.db")
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

		upstream, fake := newFakeUpstream(t, fakeScript{status: 200, body: decisionBody()})
		if err := mgr.AddCredential(models.CredentialConfig{ID: "cred-dg", Provider: "openai", APIKey: "sk", BaseURL: upstream.URL}); err != nil {
			t.Fatalf("AddCredential: %v", err)
		}
		if err := mgr.AddModel(models.ModelConfig{
			ID: "dec-claimed", Name: "Claimed", Enabled: true, Kind: models.KindDecision,
			Internal: true, InternalModel: "gpt-6-luna", Credentials: models.TestRefs("cred-dg"),
		}); err != nil {
			t.Fatalf("seed decision member: %v", err)
		}
		// A DISABLED group that SHADOW-CLAIMS the member's id.
		if err := mgr.AddModel(models.ModelConfig{
			ID: "dec-group-off", Name: "Disabled Group", Enabled: false, Kind: models.KindGroup,
			Internal: true, ShadowModelID: "dec-claimed",
			Members: []models.GroupMember{{ModelID: "dec-claimed", Weight: 1}},
		}); err != nil {
			t.Fatalf("seed disabled group: %v", err)
		}

		cached, err := modelscache.NewCachedModelsConfig(mgr, modelscache.Options{ReconcileInterval: time.Hour})
		if err != nil {
			t.Fatalf("NewCachedModelsConfig: %v", err)
		}
		t.Cleanup(cached.Stop)

		// THE HALF OF THE CORPUS PREMISE THAT IS TRUE: a disabled group's
		// shadow claim is INACTIVE (the claimed id resolves to itself, never
		// to the disabled group) — store.go's claim lookup requires
		// `g.Enabled`, the decorator's mirror requires `g.Enabled`.
		if got := cached.GetModel("dec-claimed"); got == nil || got.ID != "dec-claimed" {
			t.Fatalf("the disabled group's shadow claim is ACTIVE: GetModel(dec-claimed) = %v, want the row itself", got)
		}

		bus := events.NewBus()
		ts := newMemTokenStore()
		ts.add(&auth.AuthToken{ID: "tok-dg", Name: "DG", TokenHash: auth.HashToken("sk-dg-token")})
		// active is the decorator the handler reads through — swapped after
		// the group is enabled (the decorator's snapshot is rebuilt from the
		// manager, so the write becomes visible without a reconcile tick).
		active := cached
		h := NewHandler(stubConfig{timeout: 5 * time.Second}, bus, ts, nil)
		h.SetModelResolver(NewModelResolverFromConfig(func(modelID string) *models.ModelConfig {
			return active.GetModel(modelID)
		}))
		h.SetCredentialResolver(NewCredentialResolverFromConfig(func(modelID string) (models.ResolvedCredential, bool) {
			return active.ResolveInternalConfigWithAffinity(modelID, modelID)
		}))
		h.SetModelsConfig(active)

		post := func(model, token string) *httptest.ResponseRecorder {
			r := httptest.NewRequest(http.MethodPost, "/v1/decisions", strings.NewReader(standardRequestBody(model)))
			if token != "" {
				r.Header.Set("Authorization", "Bearer "+token)
			}
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.HandleDecision(w, r)
			return w
		}

		// LANDED: a disabled group named by its own id is SERVED (the
		// handler never consults `enabled` on the group arm).
		before := fake.hitCount.Load()
		rec := post("dec-group-off", "sk-dg-token")
		if rec.Code != http.StatusOK {
			t.Fatalf("disabled group named directly (wildcard token): status = %d, want 200 per LANDED behaviour; body=%s",
				rec.Code, rec.Body.String())
		}
		if got := fake.hitCount.Load() - before; got != 1 {
			t.Errorf("disabled group named directly: upstream hits = %d, want 1 (its enabled member serves)", got)
		}

		// THE NO-LEAK HALF THAT DOES HOLD: once the token cannot reach it,
		// the disabled group's answer is BYTE-IDENTICAL to an ENABLED
		// out-of-scope group's answer — the flag never surfaces.
		ts.add(&auth.AuthToken{
			ID: "tok-restricted", Name: "Restricted",
			TokenHash:     auth.HashToken("sk-restricted-token"),
			AllowedModels: []string{"some-other-model"},
		})
		drec := post("dec-group-off", "sk-restricted-token")
		assertAccessDenied403(t, drec, "dec-group-off")
		before = fake.hitCount.Load()

		// The control: ENABLE the group, keep the same restricted token.
		row := mgr.GetModelNative("dec-group-off")
		if row == nil {
			t.Fatal("disabled group row missing")
		}
		enabled := *row
		enabled.Enabled = true
		if err := mgr.UpdateModel("dec-group-off", enabled); err != nil {
			t.Fatalf("UpdateModel: %v", err)
		}
		// The decorator's write path is not used here, so refresh its
		// snapshot by recreating the decorator over the same manager.
		cached2, err := modelscache.NewCachedModelsConfig(mgr, modelscache.Options{ReconcileInterval: time.Hour})
		if err != nil {
			t.Fatalf("NewCachedModelsConfig(2): %v", err)
		}
		t.Cleanup(cached2.Stop)
		active = cached2
		h.SetModelsConfig(active)
		erec := post("dec-group-off", "sk-restricted-token")
		if erec.Code != http.StatusForbidden {
			t.Fatalf("enabled-but-out-of-scope group: status = %d, want 403; body=%s", erec.Code, erec.Body.String())
		}
		if erec.Body.String() != drec.Body.String() {
			t.Fatalf("the group's disabled flag LEAKS through the 403: disabled=%s enabled=%s",
				drec.Body.String(), erec.Body.String())
		}
		if got := fake.hitCount.Load() - before; got != 0 {
			t.Errorf("enabled-but-out-of-scope group: upstream hits = %d, want 0", got)
		}
	})
}
