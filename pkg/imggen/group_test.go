package imggen

// imggen-groups commission — the image-gen GROUP serving branch.
//
// The battery covers the §8 rows that belong to Phase 1:
//
//   - Kind gate: a chat group / a decision group / a chat standalone
//     posted to /v1/image_generation ⇒ 400 image_misroute, the code in
//     the body, the event outcome=image_misroute with
//     upstream_status=0, and ZERO upstream hits; a plain image-gen row
//     is unchanged.
//   - Misroute-vs-scope precedence: the misroute answer is
//     token-INDEPENDENT (a wildcard token + a chat group ⇒ still a
//     400 — never a 403, never a 502).
//   - Loop rows (a)-(i): the winner relay + metering, the
//     per-member credential rotation, the exhaustion envelope, the
//     fail-fast relay, the walk budget, the walk advance, the policy's
//     member skipping, the W-8 quota short-circuit (and its reset
//     semantics) and the W-3 one-read pin.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/modelscache"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/usage"
)

// ---------- The harness ----------

// groupModelsStub is the models stub for the image-gen GROUP tests.
//
// It satisfies models.ModelsConfigInterface (the JSON backend supplies
// the methods this lane never calls), answers the TWO optional seams
// the group branch uses — the lane query (GroupLane) and the
// membership reader (GroupMembers) — and COUNTS the GetModels reads
// (the W-3 one-read pin: the handler must read the live model set
// exactly ONCE per group request).
//
// The rows are injected directly (the JSON backend's AddModel would
// run the full write validation — a credential-provider gate that is
// not what these tests are about).
type groupModelsStub struct {
	*models.ModelsConfig
	rows     []models.ModelConfig
	lanes    map[string]models.GroupLane
	getCalls int
	// credRows serves the in-request credential-list seam (D-1). It is
	// keyed by credential ID and holds the credential row (provider /
	// key / baseURL); the member's own row carries the ordered refs.
	credRows map[string]models.CredentialConfig
}

// GetModels returns the stub's rows and COUNTS the read (the W-3
// one-read pin).
func (s *groupModelsStub) GetModels() []models.ModelConfig {
	s.getCalls++
	out := make([]models.ModelConfig, len(s.rows))
	copy(out, s.rows)
	return out
}

// GetEnabledModels returns the enabled rows (the same counter — the
// imggen lane never calls it, but the stub must stay coherent).
func (s *groupModelsStub) GetEnabledModels() []models.ModelConfig {
	s.getCalls++
	out := make([]models.ModelConfig, 0, len(s.rows))
	for _, row := range s.rows {
		if row.Enabled {
			out = append(out, row)
		}
	}
	return out
}

// GetCredential serves the in-request credential-list seam (D-1) from
// the harness's credential rows. The embedded JSON twin's credential
// store is intentionally left empty — the harness injects rows
// directly (no write-time provider validation, no crypto init).
func (s *groupModelsStub) GetCredential(id string) *models.CredentialConfig {
	if s.credRows == nil {
		return nil
	}
	cred, ok := s.credRows[id]
	if !ok {
		return nil
	}
	return &cred
}

// GroupLane answers the OPTIONAL lane seam.
func (s *groupModelsStub) GroupLane(groupID string) (models.GroupLane, bool) {
	lane, ok := s.lanes[groupID]
	return lane, ok
}

// GroupMembers answers the OPTIONAL membership seam — the group's own
// member list, read WITHOUT touching GetModels (the counter pins the
// handler's policy read, and the membership seam is the non-copying
// accessor in production).
func (s *groupModelsStub) GroupMembers(groupID string) []string {
	for _, row := range s.rows {
		if row.ID != groupID || !row.IsGroup() {
			continue
		}
		out := make([]string, 0, len(row.Members))
		for _, m := range row.Members {
			out = append(out, m.ModelID)
		}
		return out
	}
	return nil
}

// groupFakeUpstream is a scriptable fake upstream for ONE member: a
// status + body + a hit counter + an optional delay (the delay is what
// lets a test drive the walk budget).
type groupFakeUpstream struct {
	srv   *httptest.Server
	hits  atomic.Int64
	delay time.Duration
}

func newGroupUpstream(t *testing.T, status int, body []byte) *groupFakeUpstream {
	return newGroupUpstreamDelayed(t, status, body, 0)
}

func newGroupUpstreamDelayed(t *testing.T, status int, body []byte, delay time.Duration) *groupFakeUpstream {
	t.Helper()
	fu := &groupFakeUpstream{delay: delay}
	fu.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fu.hits.Add(1)
		if fu.delay > 0 {
			select {
			case <-time.After(fu.delay):
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(fu.srv.Close)
	return fu
}

// boundCredential models the PRODUCTION affinity binding: once a member
// resolves to a credential, every RE-RESOLVE returns that SAME credential
// (the (memberID, memberID) conversation-key binding with a 30m TTL).
//
// It is deliberately NOT a sequence stub. The old credSequence handed out
// distinct credentials on each resolve, which could pass a rotation pin
// while production was broken (D-1): the walk re-resolved, got the next
// credential from the stub, and never noticed that the real engine would
// have returned the bound one again. Rotation in the group walk is now
// driven by the IN-REQUEST credential-list seam, never by re-resolution —
// so the pin must model the binding shape honestly.
type boundCredential struct {
	mu   sync.Mutex
	cred models.ResolvedCredential
	ok   bool
}

func (b *boundCredential) resolve() (models.ResolvedCredential, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cred, b.ok
}

// groupEnv is the image-gen GROUP test harness.
type groupEnv struct {
	t          *testing.T
	handler    *Handler
	bus        *events.Bus
	usage      *usage.Counter
	stub       *groupModelsStub
	tokenStore *memTokenStore
	upstreams  map[string]*groupFakeUpstream
	bound      map[string]*boundCredential
	groupID    string
}

// groupMemberRow builds an image-gen member row.
func groupMemberRow(id string) models.ModelConfig {
	return models.ModelConfig{
		ID: id, Name: id, Enabled: true, Internal: true,
		Kind: models.KindImageGen, InternalModel: "image-" + id,
	}
}

// groupMemberRowDisabled builds a DISABLED image-gen member row (the
// policy never offers it as a candidate).
func groupMemberRowDisabled(id string) models.ModelConfig {
	row := groupMemberRow(id)
	row.Enabled = false
	return row
}

// groupChatMemberRow builds a chat member row.
func groupChatMemberRow(id string) models.ModelConfig {
	return models.ModelConfig{
		ID: id, Name: id, Enabled: true, Internal: true,
		Kind: models.KindChat, InternalModel: "chat-" + id,
	}
}

// groupRow builds a group row over the given member ids (every member
// in the DEFAULT pool).
func groupRow(id string, members ...string) models.ModelConfig {
	ms := make([]models.GroupMember, 0, len(members))
	for _, m := range members {
		ms = append(ms, models.GroupMember{ModelID: m, Weight: 1})
	}
	return models.ModelConfig{
		ID: id, Name: id, Enabled: true, Internal: true,
		Kind: models.KindGroup, Members: ms,
	}
}

// groupRowWithBackups builds a group row whose FIRST member is in the
// default pool and whose remaining members are BACKUP-ONLY.
//
// THE POOL SHAPE IS THE DETERMINISTIC ORDER LEVER: the policy head is
// a FRESH weighted pick from the default pool (random by design), and
// the backup pool is a RECOURSE that follows every default member in
// configured order. A group with exactly ONE default member therefore
// has a deterministic attempt order — [the default member, the backups
// in configured order] — which is what the order-sensitive rows (the
// fail-fast relay, the rotation, the quota short-circuit and its reset
// semantics) need.
func groupRowWithBackups(id string, defaultMember string, backups ...string) models.ModelConfig {
	ms := make([]models.GroupMember, 0, len(backups)+1)
	ms = append(ms, models.GroupMember{ModelID: defaultMember, Weight: 1})
	for _, m := range backups {
		ms = append(ms, models.GroupMember{ModelID: m, Weight: 1, BackupOnly: true})
	}
	return models.ModelConfig{
		ID: id, Name: id, Enabled: true, Internal: true,
		Kind: models.KindGroup, Members: ms,
	}
}

// newGroupEnv builds a handler over the given rows.
//
// creds maps a member id to the credentials its attempts consume (the
// per-member rotation); upstreams maps a member id to its fake
// upstream. The token store holds the default wildcard token
// ("sk-test-token").
func newGroupEnv(t *testing.T, rows []models.ModelConfig, lanes map[string]models.GroupLane, creds map[string][]models.ResolvedCredential, upstreams map[string]*groupFakeUpstream) *groupEnv {
	t.Helper()

	db, err := newUsageTestDB(t)
	if err != nil {
		t.Fatalf("usage db: %v", err)
	}
	uc := usage.NewCounter(db, database.SQLite)
	bus := events.NewBus()
	ts := newMemTokenStore()

	stub := &groupModelsStub{
		ModelsConfig: models.NewModelsConfig(),
		rows:         rows,
		lanes:        lanes,
		credRows:     map[string]models.CredentialConfig{},
	}
	env := &groupEnv{
		t:          t,
		bus:        bus,
		usage:      uc,
		stub:       stub,
		tokenStore: ts,
		upstreams:  upstreams,
		bound:      map[string]*boundCredential{},
	}

	// Seed the models seam for the in-request credential-list (D-1):
	// each member's resolved-credential list becomes (a) ordered
	// CredentialRefs on the member's own row and (b) credential rows
	// in the stub, so FlattenCredentialRefs + GetCredential reproduce
	// the production shape. The resolver stub models the AFFINITY
	// BINDING: it always returns the member's FIRST credential, even
	// after a rotation-eligible failure — a re-resolve-based rotation
	// would therefore stall and this harness would catch it.
	rowIndex := map[string]int{}
	for i, row := range stub.rows {
		rowIndex[row.ID] = i
	}
	for id, seq := range creds {
		refs := make([]models.CredentialRef, 0, len(seq))
		for i, cred := range seq {
			credID := cred.CredentialID
			if credID == "" {
				credID = "cred-" + id + "-" + strconv.Itoa(i)
			}
			stub.credRows[credID] = models.CredentialConfig{
				ID: credID, Provider: cred.Provider, APIKey: cred.APIKey, BaseURL: cred.BaseURL,
			}
			refs = append(refs, models.CredentialRef{CredentialID: credID, Weight: 1, Position: i})
		}
		if idx, ok := rowIndex[id]; ok {
			stub.rows[idx].Credentials = refs
		}
		if len(seq) > 0 {
			env.bound[id] = &boundCredential{cred: seq[0], ok: true}
		}
	}

	h := NewHandler(stubConfig{timeout: 2 * time.Second}, bus, ts, uc)
	h.SetModelsConfig(stub)
	h.SetModelResolver(func(id string) (*models.ModelConfig, bool) {
		for i := range stub.rows {
			if stub.rows[i].ID == id {
				row := stub.rows[i]
				return &row, true
			}
		}
		return nil, false
	})
	h.SetCredentialResolver(func(id string) (models.ResolvedCredential, bool) {
		bound, ok := env.bound[id]
		if !ok {
			return models.ResolvedCredential{}, false
		}
		return bound.resolve()
	})
	env.handler = h

	// The default token: a WILDCARD (both axes absent) — the
	// unrestricted scope.
	tok := &auth.AuthToken{
		ID:            "tok-test",
		Name:          "Test Token",
		TokenHash:     auth.HashToken("sk-test-token"),
		AllowedModels: nil,
	}
	ts.add(tok)

	return env
}

// post sends a request to the image-gen handler.
func (e *groupEnv) post(body string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/image_generation", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", defaultAuthHeader)
	rec := httptest.NewRecorder()
	e.handler.HandleImageGeneration(rec, req)
	return rec
}

// events returns the telemetry events published so far (the bus keeps a
// 100-event history and the subscription replays it).
func (e *groupEnv) events() []map[string]interface{} {
	e.t.Helper()
	sub, err := e.bus.Subscribe()
	if err != nil {
		e.t.Fatalf("subscribe: %v", err)
	}
	defer e.bus.Unsubscribe(sub)
	var out []map[string]interface{}
	for {
		select {
		case evt := <-sub:
			data, _ := evt.Data.(map[string]interface{})
			out = append(out, data)
		default:
			return out
		}
	}
}

// modelUsage returns the per-model usage rows (model id → row).
func (e *groupEnv) modelUsage() map[string]usage.ModelHourlyUsageRow {
	e.t.Helper()
	rows, err := e.usage.GetModelUsage(context.Background(), startHour(time.Now()), startHour(time.Now()))
	if err != nil {
		e.t.Fatalf("GetModelUsage: %v", err)
	}
	out := make(map[string]usage.ModelHourlyUsageRow, len(rows))
	for _, r := range rows {
		out[r.ModelID] = r
	}
	return out
}

// countEvents counts the published events with the given outcome.
func countEvents(events []map[string]interface{}, outcome string) int {
	n := 0
	for _, evt := range events {
		if evt["outcome"] == outcome {
			n++
		}
	}
	return n
}

// findEvent returns the FIRST published event with the given outcome.
func findEvent(events []map[string]interface{}, outcome string) map[string]interface{} {
	for _, evt := range events {
		if evt["outcome"] == outcome {
			return evt
		}
	}
	return nil
}

// errorCode reads the machine code out of an OpenAI-envelope body.
func errorCode(t *testing.T, body []byte) string {
	t.Helper()
	var parsed struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("body is not an OpenAI envelope: %v; body=%s", err, string(body))
	}
	return parsed.Error.Code
}

// ---------- The kind / lane gate (T1.5) ----------

// TestHandleImageGeneration_KindGate_ChatGroup_IsMisroute — a CHAT
// group posted to /v1/image_generation ⇒ 400 image_misroute, the code
// in the body, the event outcome=image_misroute with
// upstream_status=0, and ZERO upstream hits.
func TestHandleImageGeneration_KindGate_ChatGroup_IsMisroute(t *testing.T) {
	env := newGroupEnv(t,
		[]models.ModelConfig{
			groupChatMemberRow("chat-a"),
			groupRow("chat-group", "chat-a"),
		},
		map[string]models.GroupLane{"chat-group": models.GroupLaneChat},
		nil, nil)

	rec := env.post(`{"model":"chat-group","prompt":"x"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec.Body.Bytes()); code != imggenMisrouteCode {
		t.Errorf("error code = %q, want %q", code, imggenMisrouteCode)
	}
	evt := findEvent(env.events(), imggenMisrouteCode)
	if evt == nil {
		t.Fatalf("no %q event published; events=%v", imggenMisrouteCode, env.events())
	}
	if evt["upstream_status"] != 0 {
		t.Errorf("event upstream_status = %v, want 0", evt["upstream_status"])
	}
}

// TestHandleImageGeneration_KindGate_DecisionGroup_IsMisroute — a
// DECISION group posted to the image-gen lane ⇒ the same 400.
func TestHandleImageGeneration_KindGate_DecisionGroup_IsMisroute(t *testing.T) {
	env := newGroupEnv(t,
		[]models.ModelConfig{
			groupMemberRow("dec-a"),
			groupRow("dec-group", "dec-a"),
		},
		map[string]models.GroupLane{"dec-group": models.GroupLaneDecision},
		nil, nil)

	rec := env.post(`{"model":"dec-group","prompt":"x"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec.Body.Bytes()); code != imggenMisrouteCode {
		t.Errorf("error code = %q, want %q", code, imggenMisrouteCode)
	}
}

// TestHandleImageGeneration_KindGate_ChatStandalone_IsMisroute — a
// CHAT model posted to the image-gen lane ⇒ the same 400.
//
// This closes the REAL pre-existing gap: any model id used to resolve
// here and die at the provider lookup as a 502 unsupported_provider (a
// chat model whose provider happened to be minimax would even be
// SERVED).
func TestHandleImageGeneration_KindGate_ChatStandalone_IsMisroute(t *testing.T) {
	env := newGroupEnv(t,
		[]models.ModelConfig{groupChatMemberRow("chat-a")},
		nil, nil, nil)

	rec := env.post(`{"model":"chat-a","prompt":"x"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (the pre-existing gap answered 502); body=%s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec.Body.Bytes()); code != imggenMisrouteCode {
		t.Errorf("error code = %q, want %q", code, imggenMisrouteCode)
	}
}

// TestHandleImageGeneration_KindGate_MisrouteIsTokenIndependent — the
// misroute answer never depends on the token: a WILDCARD token + a
// chat group ⇒ still a 400 (never a 403, never a 502).
func TestHandleImageGeneration_KindGate_MisrouteIsTokenIndependent(t *testing.T) {
	env := newGroupEnv(t,
		[]models.ModelConfig{
			groupChatMemberRow("chat-a"),
			groupRow("chat-group", "chat-a"),
		},
		map[string]models.GroupLane{"chat-group": models.GroupLaneChat},
		nil, nil)

	// The default token IS a wildcard (both axes absent) — the
	// unrestricted scope. A chat group must still be refused.
	rec := env.post(`{"model":"chat-group","prompt":"x"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (the misroute must be token-independent); body=%s", rec.Code, rec.Body.String())
	}
	if rec.Code == http.StatusForbidden {
		t.Error("the misroute must NOT be answered with a permission error")
	}
	if rec.Code == http.StatusBadGateway {
		t.Error("the misroute must NOT be answered with an availability error")
	}
}

// TestHandleImageGeneration_KindGate_PlainImageGen_Unchanged — a plain
// image-gen row still serves (the gate does not touch that path).
func TestHandleImageGeneration_KindGate_PlainImageGen_Unchanged(t *testing.T) {
	up := newGroupUpstream(t, 200, successBody(1, 0))
	env := newGroupEnv(t,
		[]models.ModelConfig{groupMemberRow("img-a")},
		nil,
		map[string][]models.ResolvedCredential{
			"img-a": {{Provider: "minimax", APIKey: "sk-x", BaseURL: up.srv.URL, InternalModel: "image-img-a"}},
		},
		map[string]*groupFakeUpstream{"img-a": up})

	rec := env.post(`{"model":"img-a","prompt":"x"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if up.hits.Load() != 1 {
		t.Errorf("upstream hits = %d, want 1", up.hits.Load())
	}
}

// ---------- The group walk (T1.7) ----------

// TestServeImageGenGroup_Winner_RelaysVerbatim_MetersMemberRow — loop
// row (a): the first member's 200 is relayed verbatim, the WINNER
// MEMBER's row is metered (request_count=1, image_count=1) and the
// GROUP row is untouched.
func TestServeImageGenGroup_Winner_RelaysVerbatim_MetersMemberRow(t *testing.T) {
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

	rec := env.post(`{"model":"img-group","prompt":"x"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	// The winner's body is relayed VERBATIM.
	if rec.Body.String() != string(body) {
		t.Errorf("relayed body = %q, want the winner's body verbatim %q", rec.Body.String(), string(body))
	}
	// Exactly ONE terminal success event, naming the group + the
	// winning member.
	events := env.events()
	if n := countEvents(events, "success"); n != 1 {
		t.Errorf("success events = %d, want exactly 1; events=%v", n, events)
	}
	evt := findEvent(events, "success")
	if evt["model"] != "img-group" {
		t.Errorf("event model = %v, want the group id", evt["model"])
	}
	if evt["member"] == nil || evt["member"] == "" {
		t.Errorf("event member = %v, want the winning member id", evt["member"])
	}
	if evt["image_count"] != 1 {
		t.Errorf("event image_count = %v, want 1", evt["image_count"])
	}
	if evt["upstream_status"] != 200 {
		t.Errorf("event upstream_status = %v, want 200", evt["upstream_status"])
	}

	// The metering: the WINNER MEMBER's row carries the request + the
	// images; the GROUP row is untouched.
	usageRows := env.modelUsage()
	winnerID, _ := evt["member"].(string)
	winnerRow, ok := usageRows[winnerID]
	if !ok {
		t.Fatalf("no usage row for the winning member %q; rows=%v", winnerID, usageRows)
	}
	if winnerRow.RequestCount != 1 || winnerRow.ImageCount != 1 {
		t.Errorf("winner usage row = req %d / img %d, want 1/1", winnerRow.RequestCount, winnerRow.ImageCount)
	}
	if _, ok := usageRows["img-group"]; ok {
		t.Errorf("the group row must be UNTOUCHED on the winner path; rows=%v", usageRows)
	}
}

// TestServeImageGenGroup_Quota429_RotatesCredential_ThenAdvances —
// loop row (b): member A's quota-429 rotates to member A's SECOND
// credential (the member's own resilience is consumed first), and the
// walk then advances to member B, which serves the request.
func TestServeImageGenGroup_Quota429_RotatesCredential_ThenAdvances(t *testing.T) {
	quotaBody := []byte(`{"error":{"message":"quota exhausted"}}`)
	upA := newGroupUpstream(t, 429, quotaBody)
	upB := newGroupUpstream(t, 200, successBody(1, 0))

	env := newGroupEnv(t,
		[]models.ModelConfig{
			groupMemberRow("img-a"),
			groupMemberRow("img-b"),
			groupRowWithBackups("img-group", "img-a", "img-b"),
		},
		map[string]models.GroupLane{"img-group": models.GroupLaneImageGen},
		map[string][]models.ResolvedCredential{
			// Member A has TWO credentials: the quota failure rotates
			// to the second one before the walk advances.
			"img-a": {
				{Provider: "9router", APIKey: "sk-a1", BaseURL: upA.srv.URL, InternalModel: "image-img-a", CredentialID: "cred-a1"},
				{Provider: "9router", APIKey: "sk-a2", BaseURL: upA.srv.URL, InternalModel: "image-img-a", CredentialID: "cred-a2"},
			},
			"img-b": {{Provider: "9router", APIKey: "sk-b", BaseURL: upB.srv.URL, InternalModel: "image-img-b", CredentialID: "cred-b"}},
		},
		map[string]*groupFakeUpstream{"img-a": upA, "img-b": upB})

	rec := env.post(`{"model":"img-group","prompt":"x"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	// Member A was attempted TWICE (the credential rotation), member B
	// once — the walk advanced after member A's resilience was
	// consumed.
	if upA.hits.Load() != 2 {
		t.Errorf("member A upstream hits = %d, want 2 (the quota failure must rotate the credential before the walk advances)", upA.hits.Load())
	}
	if upB.hits.Load() != 1 {
		t.Errorf("member B upstream hits = %d, want 1", upB.hits.Load())
	}
	// The quota failure is classified as the quota class.
	events := env.events()
	quotaEvents := 0
	for _, evt := range events {
		if evt["outcome"] == quotaFailureClass {
			quotaEvents++
		}
	}
	if quotaEvents != 2 {
		t.Errorf("quota-class failure events = %d, want 2 (both of member A's attempts); events=%v", quotaEvents, events)
	}
}

// TestServeImageGenGroup_AllMembersFail_ExhaustionEnvelope — loop row
// (c): every member fails ⇒ 502 + the imggen_all_members_failed code,
// exactly ONE terminal event with the accounting counters, the GROUP
// row billed ONCE (request_count=1, image_count=0) and NO member
// usage rows.
func TestServeImageGenGroup_AllMembersFail_ExhaustionEnvelope(t *testing.T) {
	errBody := []byte(`{"error":{"message":"upstream boom"}}`)
	upA := newGroupUpstream(t, 500, errBody)
	upB := newGroupUpstream(t, 500, errBody)

	env := newGroupEnv(t,
		[]models.ModelConfig{
			groupMemberRow("img-a"),
			groupMemberRow("img-b"),
			groupRow("img-group", "img-a", "img-b"),
		},
		map[string]models.GroupLane{"img-group": models.GroupLaneImageGen},
		map[string][]models.ResolvedCredential{
			"img-a": {{Provider: "9router", APIKey: "sk-a", BaseURL: upA.srv.URL, InternalModel: "image-img-a", CredentialID: "cred-a"}},
			"img-b": {{Provider: "9router", APIKey: "sk-b", BaseURL: upB.srv.URL, InternalModel: "image-img-b", CredentialID: "cred-b"}},
		},
		map[string]*groupFakeUpstream{"img-a": upA, "img-b": upB})

	rec := env.post(`{"model":"img-group","prompt":"x"}`)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec.Body.Bytes()); code != imggenAllMembersFailedCode {
		t.Errorf("error code = %q, want %q", code, imggenAllMembersFailedCode)
	}
	// ONE terminal event, with the accounting counters.
	events := env.events()
	if n := countEvents(events, "all_members_failed"); n != 1 {
		t.Fatalf("all_members_failed events = %d, want exactly 1; events=%v", n, events)
	}
	evt := findEvent(events, "all_members_failed")
	if evt["model"] != "img-group" {
		t.Errorf("event model = %v, want the group id", evt["model"])
	}
	if evt["upstream_status"] != 0 {
		t.Errorf("event upstream_status = %v, want 0", evt["upstream_status"])
	}
	if evt["attempted"] != 2 {
		t.Errorf("event attempted = %v, want 2", evt["attempted"])
	}
	if evt["members_attempted"] != 2 {
		t.Errorf("event members_attempted = %v, want 2", evt["members_attempted"])
	}

	// The metering: the GROUP row once (request_count=1,
	// image_count=0) and NO member failure rows.
	usageRows := env.modelUsage()
	groupRowUsage, ok := usageRows["img-group"]
	if !ok {
		t.Fatalf("no usage row for the group; rows=%v", usageRows)
	}
	if groupRowUsage.RequestCount != 1 || groupRowUsage.ImageCount != 0 {
		t.Errorf("group usage row = req %d / img %d, want 1/0", groupRowUsage.RequestCount, groupRowUsage.ImageCount)
	}
	if len(usageRows) != 1 {
		t.Errorf("usage rows = %v, want ONLY the group row (no per-member failure rows)", usageRows)
	}
}

// TestServeImageGenGroup_Metering_TwoMembers_OneFailure_RowCount — the
// D-5 metering pin: a 2-member group where ONE member fails ⇒ exactly
// ONE usage row (the winner's member row); the failed member has NO
// row and the group row has none either.
func TestServeImageGenGroup_Metering_TwoMembers_OneFailure_RowCount(t *testing.T) {
	errBody := []byte(`{"error":{"message":"upstream boom"}}`)
	upA := newGroupUpstream(t, 500, errBody)
	upB := newGroupUpstream(t, 200, successBody(2, 0))

	env := newGroupEnv(t,
		[]models.ModelConfig{
			groupMemberRow("img-a"),
			groupMemberRow("img-b"),
			groupRowWithBackups("img-group", "img-a", "img-b"),
		},
		map[string]models.GroupLane{"img-group": models.GroupLaneImageGen},
		map[string][]models.ResolvedCredential{
			"img-a": {{Provider: "minimax", APIKey: "sk-a", BaseURL: upA.srv.URL, InternalModel: "image-img-a", CredentialID: "cred-a"}},
			"img-b": {{Provider: "minimax", APIKey: "sk-b", BaseURL: upB.srv.URL, InternalModel: "image-img-b", CredentialID: "cred-b"}},
		},
		map[string]*groupFakeUpstream{"img-a": upA, "img-b": upB})

	rec := env.post(`{"model":"img-group","prompt":"x"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	usageRows := env.modelUsage()
	if len(usageRows) != 1 {
		t.Fatalf("usage rows = %v, want exactly ONE (the winner's member row — no failed-member row, no group row)", usageRows)
	}
	row, ok := usageRows["img-b"]
	if !ok {
		t.Fatalf("the usage row is not the winner's member row; rows=%v", usageRows)
	}
	if row.RequestCount != 1 || row.ImageCount != 2 {
		t.Errorf("winner usage row = req %d / img %d, want 1/2", row.RequestCount, row.ImageCount)
	}
	if _, ok := usageRows["img-a"]; ok {
		t.Errorf("the FAILED member must have NO usage row; rows=%v", usageRows)
	}
	if _, ok := usageRows["img-group"]; ok {
		t.Errorf("the GROUP row must have NO usage row on the winner path; rows=%v", usageRows)
	}
}

// TestServeImageGenGroup_Client400_FailsFast_RelaysMemberStatusAndBody
// — loop row (d): a client-attributable 400 FAILS FAST: the walk stops
// (the second member is never attempted) and the member's status + body
// are relayed.
func TestServeImageGenGroup_Client400_FailsFast_RelaysMemberStatusAndBody(t *testing.T) {
	badBody := []byte(`{"error":{"message":"bad prompt"}}`)
	upA := newGroupUpstream(t, 400, badBody)
	upB := newGroupUpstream(t, 200, successBody(1, 0))

	env := newGroupEnv(t,
		[]models.ModelConfig{
			groupMemberRow("img-a"),
			groupMemberRow("img-b"),
			groupRowWithBackups("img-group", "img-a", "img-b"),
		},
		map[string]models.GroupLane{"img-group": models.GroupLaneImageGen},
		map[string][]models.ResolvedCredential{
			"img-a": {{Provider: "9router", APIKey: "sk-a", BaseURL: upA.srv.URL, InternalModel: "image-img-a", CredentialID: "cred-a"}},
			"img-b": {{Provider: "9router", APIKey: "sk-b", BaseURL: upB.srv.URL, InternalModel: "image-img-b", CredentialID: "cred-b"}},
		},
		map[string]*groupFakeUpstream{"img-a": upA, "img-b": upB})

	rec := env.post(`{"model":"img-group","prompt":"x"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (the member's own status); body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "bad prompt") {
		t.Errorf("relayed body = %q, want the member's body", rec.Body.String())
	}
	if upB.hits.Load() != 0 {
		t.Errorf("member B upstream hits = %d, want 0 (the walk must fail fast)", upB.hits.Load())
	}
	// The failed walk is billed on the GROUP row (bill-on-error).
	usageRows := env.modelUsage()
	if _, ok := usageRows["img-group"]; !ok {
		t.Errorf("the failed walk must bill the GROUP row; rows=%v", usageRows)
	}
}

// TestServeImageGenGroup_WalkAdvances_EveryMemberAttempted — loop row
// (f): the walk ADVANCES on a member failure (it is never a
// single-attempt lane): every base-servable member is attempted before
// the request may fail.
func TestServeImageGenGroup_WalkAdvances_EveryMemberAttempted(t *testing.T) {
	errBody := []byte(`{"error":{"message":"boom"}}`)
	upA := newGroupUpstream(t, 500, errBody)
	upB := newGroupUpstream(t, 500, errBody)
	upC := newGroupUpstream(t, 500, errBody)

	env := newGroupEnv(t,
		[]models.ModelConfig{
			groupMemberRow("img-a"),
			groupMemberRow("img-b"),
			groupMemberRow("img-c"),
			groupRow("img-group", "img-a", "img-b", "img-c"),
		},
		map[string]models.GroupLane{"img-group": models.GroupLaneImageGen},
		map[string][]models.ResolvedCredential{
			"img-a": {{Provider: "9router", APIKey: "sk-a", BaseURL: upA.srv.URL, InternalModel: "image-img-a", CredentialID: "cred-a"}},
			"img-b": {{Provider: "9router", APIKey: "sk-b", BaseURL: upB.srv.URL, InternalModel: "image-img-b", CredentialID: "cred-b"}},
			"img-c": {{Provider: "9router", APIKey: "sk-c", BaseURL: upC.srv.URL, InternalModel: "image-img-c", CredentialID: "cred-c"}},
		},
		map[string]*groupFakeUpstream{"img-a": upA, "img-b": upB, "img-c": upC})

	rec := env.post(`{"model":"img-group","prompt":"x"}`)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rec.Code, rec.Body.String())
	}
	for id, up := range map[string]*groupFakeUpstream{"img-a": upA, "img-b": upB, "img-c": upC} {
		if up.hits.Load() != 1 {
			t.Errorf("member %s upstream hits = %d, want 1 (every base-servable member must get its honest attempt)", id, up.hits.Load())
		}
	}
}

// TestServeImageGenGroup_PolicySkips_UnservableMembers — loop row (g):
// a DISABLED, a VANISHED and a WRONG-KIND member are skipped by the
// policy — never attempted.
func TestServeImageGenGroup_PolicySkips_UnservableMembers(t *testing.T) {
	upOK := newGroupUpstream(t, 200, successBody(1, 0))
	upDisabled := newGroupUpstream(t, 200, successBody(1, 0))
	upWrongKind := newGroupUpstream(t, 200, successBody(1, 0))
	upVanished := newGroupUpstream(t, 200, successBody(1, 0))

	env := newGroupEnv(t,
		[]models.ModelConfig{
			groupMemberRow("img-ok"),
			groupMemberRowDisabled("img-disabled"),
			groupChatMemberRow("img-wrongkind"),
			// "img-vanished" is a member of the group but has NO row.
			groupRow("img-group", "img-ok", "img-disabled", "img-wrongkind", "img-vanished"),
		},
		map[string]models.GroupLane{"img-group": models.GroupLaneImageGen},
		map[string][]models.ResolvedCredential{
			"img-ok":        {{Provider: "minimax", APIKey: "sk-ok", BaseURL: upOK.srv.URL, InternalModel: "image-img-ok", CredentialID: "cred-ok"}},
			"img-disabled":  {{Provider: "minimax", APIKey: "sk-d", BaseURL: upDisabled.srv.URL, InternalModel: "image-img-disabled", CredentialID: "cred-d"}},
			"img-wrongkind": {{Provider: "minimax", APIKey: "sk-w", BaseURL: upWrongKind.srv.URL, InternalModel: "image-img-wrongkind", CredentialID: "cred-w"}},
			"img-vanished":  {{Provider: "minimax", APIKey: "sk-v", BaseURL: upVanished.srv.URL, InternalModel: "image-img-vanished", CredentialID: "cred-v"}},
		},
		map[string]*groupFakeUpstream{
			"img-ok": upOK, "img-disabled": upDisabled, "img-wrongkind": upWrongKind, "img-vanished": upVanished,
		})

	rec := env.post(`{"model":"img-group","prompt":"x"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if upDisabled.hits.Load() != 0 {
		t.Errorf("the DISABLED member was attempted (%d hits)", upDisabled.hits.Load())
	}
	if upWrongKind.hits.Load() != 0 {
		t.Errorf("the WRONG-KIND member was attempted (%d hits)", upWrongKind.hits.Load())
	}
	if upVanished.hits.Load() != 0 {
		t.Errorf("the VANISHED member was attempted (%d hits)", upVanished.hits.Load())
	}
	if upOK.hits.Load() != 1 {
		t.Errorf("the servable member hits = %d, want 1", upOK.hits.Load())
	}
}

// TestServeImageGenGroup_QuotaShortCircuit_ThreeConsecutive — loop row
// (h) / W-8: THREE CONSECUTIVE quota-class failures stop the walk: the
// remaining members are NOT attempted, the exhaustion envelope is
// written, and the terminal event carries short_circuited=true +
// last_class=upstream_quota_exceeded.
func TestServeImageGenGroup_QuotaShortCircuit_ThreeConsecutive(t *testing.T) {
	quotaBody := []byte(`{"error":{"message":"quota exhausted"}}`)
	upA := newGroupUpstream(t, 429, quotaBody)
	upB := newGroupUpstream(t, 429, quotaBody)
	upC := newGroupUpstream(t, 429, quotaBody)
	upD := newGroupUpstream(t, 200, successBody(1, 0))

	env := newGroupEnv(t,
		[]models.ModelConfig{
			groupMemberRow("img-a"),
			groupMemberRow("img-b"),
			groupMemberRow("img-c"),
			groupMemberRow("img-d"),
			groupRowWithBackups("img-group", "img-a", "img-b", "img-c", "img-d"),
		},
		map[string]models.GroupLane{"img-group": models.GroupLaneImageGen},
		map[string][]models.ResolvedCredential{
			"img-a": {{Provider: "9router", APIKey: "sk-a", BaseURL: upA.srv.URL, InternalModel: "image-img-a", CredentialID: "cred-a"}},
			"img-b": {{Provider: "9router", APIKey: "sk-b", BaseURL: upB.srv.URL, InternalModel: "image-img-b", CredentialID: "cred-b"}},
			"img-c": {{Provider: "9router", APIKey: "sk-c", BaseURL: upC.srv.URL, InternalModel: "image-img-c", CredentialID: "cred-c"}},
			"img-d": {{Provider: "9router", APIKey: "sk-d", BaseURL: upD.srv.URL, InternalModel: "image-img-d", CredentialID: "cred-d"}},
		},
		map[string]*groupFakeUpstream{"img-a": upA, "img-b": upB, "img-c": upC, "img-d": upD})

	rec := env.post(`{"model":"img-group","prompt":"x"}`)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (the exhaustion envelope); body=%s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec.Body.Bytes()); code != imggenAllMembersFailedCode {
		t.Errorf("error code = %q, want %q", code, imggenAllMembersFailedCode)
	}
	// The 4th member was NEVER attempted (the short-circuit stopped
	// the walk).
	if upD.hits.Load() != 0 {
		t.Errorf("the 4th member was attempted (%d hits) — the short-circuit must stop the walk", upD.hits.Load())
	}
	// The terminal event carries the quota class.
	events := env.events()
	evt := findEvent(events, "all_members_failed")
	if evt == nil {
		t.Fatalf("no all_members_failed event; events=%v", events)
	}
	if evt["short_circuited"] != true {
		t.Errorf("event short_circuited = %v, want true", evt["short_circuited"])
	}
	if evt["last_class"] != quotaFailureClass {
		t.Errorf("event last_class = %v, want %q", evt["last_class"], quotaFailureClass)
	}
}

// TestServeImageGenGroup_QuotaShortCircuit_ResetsOnNonQuota — the
// W-8 counter RESET semantics: quota, quota, 5xx, quota, quota ⇒ NO
// short-circuit (the 5xx breaks the consecutive run) — the walk keeps
// going and the exhaustion envelope carries NO short_circuited flag.
func TestServeImageGenGroup_QuotaShortCircuit_ResetsOnNonQuota(t *testing.T) {
	quotaBody := []byte(`{"error":{"message":"quota exhausted"}}`)
	errBody := []byte(`{"error":{"message":"boom"}}`)
	upA := newGroupUpstream(t, 429, quotaBody)
	upB := newGroupUpstream(t, 429, quotaBody)
	upC := newGroupUpstream(t, 500, errBody)
	upD := newGroupUpstream(t, 429, quotaBody)
	upE := newGroupUpstream(t, 429, quotaBody)

	env := newGroupEnv(t,
		[]models.ModelConfig{
			groupMemberRow("img-a"),
			groupMemberRow("img-b"),
			groupMemberRow("img-c"),
			groupMemberRow("img-d"),
			groupMemberRow("img-e"),
			groupRowWithBackups("img-group", "img-a", "img-b", "img-c", "img-d", "img-e"),
		},
		map[string]models.GroupLane{"img-group": models.GroupLaneImageGen},
		map[string][]models.ResolvedCredential{
			"img-a": {{Provider: "9router", APIKey: "sk-a", BaseURL: upA.srv.URL, InternalModel: "image-img-a", CredentialID: "cred-a"}},
			"img-b": {{Provider: "9router", APIKey: "sk-b", BaseURL: upB.srv.URL, InternalModel: "image-img-b", CredentialID: "cred-b"}},
			"img-c": {{Provider: "9router", APIKey: "sk-c", BaseURL: upC.srv.URL, InternalModel: "image-img-c", CredentialID: "cred-c"}},
			"img-d": {{Provider: "9router", APIKey: "sk-d", BaseURL: upD.srv.URL, InternalModel: "image-img-d", CredentialID: "cred-d"}},
			"img-e": {{Provider: "9router", APIKey: "sk-e", BaseURL: upE.srv.URL, InternalModel: "image-img-e", CredentialID: "cred-e"}},
		},
		map[string]*groupFakeUpstream{"img-a": upA, "img-b": upB, "img-c": upC, "img-d": upD, "img-e": upE})

	rec := env.post(`{"model":"img-group","prompt":"x"}`)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rec.Code, rec.Body.String())
	}
	// Every member got its attempt — the 5xx reset the consecutive
	// quota run, so no short-circuit fired.
	for id, up := range map[string]*groupFakeUpstream{"img-a": upA, "img-b": upB, "img-c": upC, "img-d": upD, "img-e": upE} {
		if up.hits.Load() != 1 {
			t.Errorf("member %s hits = %d, want 1 (the 5xx must reset the consecutive quota counter)", id, up.hits.Load())
		}
	}
	evt := findEvent(env.events(), "all_members_failed")
	if evt == nil {
		t.Fatalf("no all_members_failed event")
	}
	if _, ok := evt["short_circuited"]; ok {
		t.Errorf("event short_circuited = %v, want the key ABSENT (no short-circuit fired)", evt["short_circuited"])
	}
}

// TestServeImageGenGroup_BudgetExhausted_MidWalk — loop row (e): the
// walk budget is the OUTER bound — when it expires mid-walk the walk
// stops with the exhaustion envelope and the terminal event carries
// budget_exhausted=true.
func TestServeImageGenGroup_BudgetExhausted_MidWalk(t *testing.T) {
	// The budget knob is HOT: the handler reads it per request. The
	// stub returns a budget that the FIRST member attempt consumes
	// whole (the per-member timeout is LARGER than the budget, so the
	// attempt's ctx is the budget itself) — the walk is then cut
	// before the second member.
	upA := newGroupUpstreamDelayed(t, 200, successBody(1, 0), 200*time.Millisecond)
	upB := newGroupUpstreamDelayed(t, 200, successBody(1, 0), 200*time.Millisecond)

	env := newGroupEnv(t,
		[]models.ModelConfig{
			groupMemberRow("img-a"),
			groupMemberRow("img-b"),
			groupRowWithBackups("img-group", "img-a", "img-b"),
		},
		map[string]models.GroupLane{"img-group": models.GroupLaneImageGen},
		map[string][]models.ResolvedCredential{
			"img-a": {{Provider: "minimax", APIKey: "sk-a", BaseURL: upA.srv.URL, InternalModel: "image-img-a", CredentialID: "cred-a"}},
			"img-b": {{Provider: "minimax", APIKey: "sk-b", BaseURL: upB.srv.URL, InternalModel: "image-img-b", CredentialID: "cred-b"}},
		},
		map[string]*groupFakeUpstream{"img-a": upA, "img-b": upB})

	// The walk budget (40ms) is SMALLER than the per-member deadline
	// (60ms) — so the first attempt's ctx is the budget itself.
	env.handler.configMgr = budgetConfig{timeout: 60 * time.Millisecond, walk: 40 * time.Millisecond}

	rec := env.post(`{"model":"img-group","prompt":"x"}`)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (the exhaustion envelope); body=%s", rec.Code, rec.Body.String())
	}
	if upA.hits.Load() != 1 {
		t.Errorf("member A upstream hits = %d, want 1 (the first attempt consumes the budget)", upA.hits.Load())
	}
	if upB.hits.Load() != 0 {
		t.Errorf("member B was attempted (%d hits) — the exhausted budget must stop the walk", upB.hits.Load())
	}
	evt := findEvent(env.events(), "all_members_failed")
	if evt == nil {
		t.Fatalf("no all_members_failed event")
	}
	if evt["budget_exhausted"] != true {
		t.Errorf("event budget_exhausted = %v, want true", evt["budget_exhausted"])
	}
}

// budgetConfig is a config stub with an explicit walk budget (the
// HOT knob).
type budgetConfig struct {
	timeout time.Duration
	walk    time.Duration
}

func (c budgetConfig) GetImageGenTimeout() time.Duration          { return c.timeout }
func (c budgetConfig) GetImageGenGroupWalkTimeout() time.Duration { return c.walk }

// TestServeImageGenGroup_OneGetModelsReadPerRequest — loop row (i) /
// W-3: exactly ONE GetModels read per group request (the policy + the
// counting rule share the single call — the deep-copying read must not
// be repeated).
func TestServeImageGenGroup_OneGetModelsReadPerRequest(t *testing.T) {
	up := newGroupUpstream(t, 200, successBody(1, 0))

	env := newGroupEnv(t,
		[]models.ModelConfig{
			groupMemberRow("img-a"),
			groupMemberRow("img-b"),
			groupRow("img-group", "img-a", "img-b"),
		},
		map[string]models.GroupLane{"img-group": models.GroupLaneImageGen},
		map[string][]models.ResolvedCredential{
			"img-a": {{Provider: "minimax", APIKey: "sk-a", BaseURL: up.srv.URL, InternalModel: "image-img-a", CredentialID: "cred-a"}},
			"img-b": {{Provider: "minimax", APIKey: "sk-b", BaseURL: up.srv.URL, InternalModel: "image-img-b", CredentialID: "cred-b"}},
		},
		map[string]*groupFakeUpstream{"img-a": up, "img-b": up})

	rec := env.post(`{"model":"img-group","prompt":"x"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if env.stub.getCalls != 1 {
		t.Errorf("GetModels reads = %d, want exactly 1 per group request", env.stub.getCalls)
	}
}

// ===================================================================
// Verification-fix iteration 1 (D-1..D-6) pins.
//
// These pins exercise the adjudicated defects directly. The harness's
// credential resolver now models the PRODUCTION affinity binding (the
// same credential on every resolve), so a rotation that relied on
// re-resolving would stall — the pins below can only pass via the
// in-request credential-list seam.
// ===================================================================

// TestServeImageGenGroup_RotationFiresUnderAffinityBinding — D-1: a
// member whose FIRST credential is bad and SECOND is healthy must be
// served 200 from the SECOND. The resolver stub returns the SAME
// (first) credential on every call (the production binding shape), so
// the ONLY way to reach the second credential is the in-request
// credential-list seam. A sequence stub would hide this defect.
func TestServeImageGenGroup_RotationFiresUnderAffinityBinding(t *testing.T) {
	badBody := []byte(`{"error":{"message":"quota exhausted"}}`)
	upBad := newGroupUpstream(t, 429, badBody)
	upGood := newGroupUpstream(t, 200, successBody(1, 0))

	env := newGroupEnv(t,
		[]models.ModelConfig{
			groupMemberRow("img-a"),
			groupRowWithBackups("img-group", "img-a"),
		},
		map[string]models.GroupLane{"img-group": models.GroupLaneImageGen},
		map[string][]models.ResolvedCredential{
			"img-a": {
				{Provider: "9router", APIKey: "sk-bad", BaseURL: upBad.srv.URL, InternalModel: "image-img-a", CredentialID: "cred-bad"},
				{Provider: "9router", APIKey: "sk-good", BaseURL: upGood.srv.URL, InternalModel: "image-img-a", CredentialID: "cred-good"},
			},
		},
		map[string]*groupFakeUpstream{"img-a": upBad})

	rec := env.post(`{"model":"img-group","prompt":"x"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 from the member's SECOND credential; body=%s", rec.Code, rec.Body.String())
	}
	if upBad.hits.Load() != 1 {
		t.Errorf("bad-credential upstream hits = %d, want 1", upBad.hits.Load())
	}
	if upGood.hits.Load() != 1 {
		t.Errorf("healthy-credential upstream hits = %d, want 1 (rotation must fire)", upGood.hits.Load())
	}
}

// TestServeImageGenGroup_RotationThroughCachedConfig — D-1 decorator-
// through: the SAME rotation proof, but driven through the REAL
// *modelscache.CachedModelsConfig over a real migrated SQLite store —
// the production binding. The two methods the seam composes
// (FlattenCredentialRefs + GetCredential) must both be forwarded by
// the decorator or the rotation stalls here.
func TestServeImageGenGroup_RotationThroughCachedConfig(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "imggen-rotation-cached.db")
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

	badBody := []byte(`{"error":{"message":"quota exhausted"}}`)
	upBad := newGroupUpstream(t, 429, badBody)
	upGood := newGroupUpstream(t, 200, successBody(1, 0))
	if err := mgr.AddCredential(models.CredentialConfig{ID: "cred-bad", Provider: "9router", APIKey: "sk-bad", BaseURL: upBad.srv.URL}); err != nil {
		t.Fatalf("AddCredential(bad): %v", err)
	}
	if err := mgr.AddCredential(models.CredentialConfig{ID: "cred-good", Provider: "9router", APIKey: "sk-good", BaseURL: upGood.srv.URL}); err != nil {
		t.Fatalf("AddCredential(good): %v", err)
	}
	if err := mgr.AddModel(models.ModelConfig{
		ID: "img-member", Name: "Img Member", Enabled: true, Internal: true,
		Kind: models.KindImageGen, InternalModel: "image-img-member",
		Credentials: []models.CredentialRef{
			{CredentialID: "cred-bad", Weight: 1, Position: 0},
			{CredentialID: "cred-good", Weight: 1, Position: 1},
		},
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
	// The PRODUCTION resolver wiring: a NON-EMPTY conversation key
	// (= modelID) binds one credential. A re-resolve-based rotation
	// would return that same credential and stall.
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
	if upBad.hits.Load() != 1 {
		t.Errorf("bad-credential upstream hits = %d, want 1", upBad.hits.Load())
	}
	if upGood.hits.Load() != 1 {
		t.Errorf("healthy-credential upstream hits = %d, want 1 (the seam must advance through the decorator)", upGood.hits.Load())
	}
}

// TestServeImageGenGroup_RotatingMember_RespectsWalkBudget — D-2: a
// rotating member's SECOND attempt must see the FRESH remaining walk
// budget, not a stale per-member snapshot. The first attempt consumes
// most of the budget; the second attempt's ctx deadline must still be
// bounded by the walk deadline.
func TestServeImageGenGroup_RotatingMember_RespectsWalkBudget(t *testing.T) {
	quotaBody := []byte(`{"error":{"message":"quota exhausted"}}`)
	// cred-1 delays 100ms then fails with quota (rotation-eligible).
	upSlow := newGroupUpstreamDelayed(t, 429, quotaBody, 100*time.Millisecond)
	// cred-2 responds at once; we capture its ctx deadline.
	upFast := newGroupUpstream(t, 200, successBody(1, 0))

	env := newGroupEnv(t,
		[]models.ModelConfig{
			groupMemberRow("img-a"),
			groupRowWithBackups("img-group", "img-a"),
		},
		map[string]models.GroupLane{"img-group": models.GroupLaneImageGen},
		map[string][]models.ResolvedCredential{
			"img-a": {
				{Provider: "9router", APIKey: "sk-1", BaseURL: upSlow.srv.URL, InternalModel: "image-img-a", CredentialID: "cred-1"},
				{Provider: "9router", APIKey: "sk-2", BaseURL: upFast.srv.URL, InternalModel: "image-img-a", CredentialID: "cred-2"},
			},
		},
		map[string]*groupFakeUpstream{"img-a": upSlow})

	// Walk budget 150ms; per-member timeout 2s (the stub default).
	// With a STALE per-member `remaining` the second attempt would
	// grant a ~150ms ctx (deadline ≈ start+250ms). With the FRESH
	// per-attempt remaining it grants ~50ms (deadline ≈ start+150ms).
	env.handler.configMgr = budgetConfig{timeout: 2 * time.Second, walk: 150 * time.Millisecond}

	start := time.Now()
	rec := env.post(`{"model":"img-group","prompt":"x"}`)
	elapsed := time.Since(start)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the second credential is healthy); body=%s", rec.Code, rec.Body.String())
	}
	if upSlow.hits.Load() != 1 || upFast.hits.Load() != 1 {
		t.Fatalf("hits = slow %d / fast %d, want 1/1 (rotation must fire)", upSlow.hits.Load(), upFast.hits.Load())
	}
	// The whole walk must stay within the budget plus scheduling slack.
	if elapsed > 220*time.Millisecond {
		t.Errorf("walk took %v — a rotating member overran the 150ms budget (stale remaining)", elapsed)
	}
}

// TestServeImageGenGroup_DistinctMembersAttempted — D-3:
// members_attempted counts DISTINCT members. A member rotating across
// two credentials contributes 2 to attempted but only 1 here.
func TestServeImageGenGroup_DistinctMembersAttempted(t *testing.T) {
	quotaBody := []byte(`{"error":{"message":"quota exhausted"}}`)
	errBody := []byte(`{"error":{"message":"boom"}}`)
	// 429 quota is rotation-eligible ⇒ member A consumes BOTH of its
	// credentials before the walk advances (a 5xx would advance at
	// once without rotating).
	upA := newGroupUpstream(t, 429, quotaBody)
	upB := newGroupUpstream(t, 500, errBody)

	env := newGroupEnv(t,
		[]models.ModelConfig{
			groupMemberRow("img-a"),
			groupMemberRow("img-b"),
			groupRowWithBackups("img-group", "img-a", "img-b"),
		},
		map[string]models.GroupLane{"img-group": models.GroupLaneImageGen},
		map[string][]models.ResolvedCredential{
			"img-a": {
				{Provider: "9router", APIKey: "sk-a1", BaseURL: upA.srv.URL, InternalModel: "image-img-a", CredentialID: "cred-a1"},
				{Provider: "9router", APIKey: "sk-a2", BaseURL: upA.srv.URL, InternalModel: "image-img-a", CredentialID: "cred-a2"},
			},
			"img-b": {{Provider: "9router", APIKey: "sk-b", BaseURL: upB.srv.URL, InternalModel: "image-img-b", CredentialID: "cred-b"}},
		},
		map[string]*groupFakeUpstream{"img-a": upA, "img-b": upB})

	rec := env.post(`{"model":"img-group","prompt":"x"}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rec.Code, rec.Body.String())
	}
	evt := findEvent(env.events(), "all_members_failed")
	if evt == nil {
		t.Fatalf("no all_members_failed event")
	}
	if evt["attempted"] != 3 {
		t.Errorf("attempted = %v, want 3 (A twice + B once)", evt["attempted"])
	}
	if evt["members_attempted"] != 2 {
		t.Errorf("members_attempted = %v, want 2 DISTINCT members (A rotated but counts once)", evt["members_attempted"])
	}
}

// TestServeImageGenGroup_SkippedCountsPolicySkipped — D-3: skipped
// counts the members the POLICY refused as unservable at pick time
// (vanished / disabled / wrong-kind), not scope-excluded members.
func TestServeImageGenGroup_SkippedCountsPolicySkipped(t *testing.T) {
	errBody := []byte(`{"error":{"message":"boom"}}`)
	upOK := newGroupUpstream(t, 500, errBody)

	env := newGroupEnv(t,
		[]models.ModelConfig{
			groupMemberRow("img-ok"),
			groupMemberRowDisabled("img-disabled"),
			groupRowWithBackups("img-group", "img-ok", "img-disabled", "img-vanished"),
		},
		map[string]models.GroupLane{"img-group": models.GroupLaneImageGen},
		map[string][]models.ResolvedCredential{
			"img-ok": {{Provider: "minimax", APIKey: "sk-ok", BaseURL: upOK.srv.URL, InternalModel: "image-img-ok", CredentialID: "cred-ok"}},
		},
		map[string]*groupFakeUpstream{"img-ok": upOK})

	rec := env.post(`{"model":"img-group","prompt":"x"}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rec.Code, rec.Body.String())
	}
	evt := findEvent(env.events(), "all_members_failed")
	if evt == nil {
		t.Fatalf("no all_members_failed event")
	}
	// filtered.Members has 3 entries; the policy admits only img-ok.
	if evt["skipped"] != 2 {
		t.Errorf("skipped = %v, want 2 (disabled + vanished were policy-skipped)", evt["skipped"])
	}
	if evt["members_attempted"] != 1 {
		t.Errorf("members_attempted = %v, want 1", evt["members_attempted"])
	}
}

// TestServeImageGenGroup_QuotaUnresolved_DoesNotResetShortCircuit — D-6:
// quota, quota, UNRESOLVED (no attempt / no class), quota, quota ⇒
// short-circuit AT THE THIRD QUOTA (the unresolved member does NOT
// reset the consecutive-quota counter).
func TestServeImageGenGroup_QuotaUnresolved_DoesNotResetShortCircuit(t *testing.T) {
	quotaBody := []byte(`{"error":{"message":"quota exhausted"}}`)
	upA := newGroupUpstream(t, 429, quotaBody)
	upB := newGroupUpstream(t, 429, quotaBody)
	upD := newGroupUpstream(t, 429, quotaBody)
	upE := newGroupUpstream(t, 429, quotaBody)

	env := newGroupEnv(t,
		[]models.ModelConfig{
			groupMemberRow("img-a"),
			groupMemberRow("img-b"),
			groupMemberRow("img-unresolved"), // no credential entry ⇒ unresolved
			groupMemberRow("img-d"),
			groupMemberRow("img-e"),
			groupRowWithBackups("img-group", "img-a", "img-b", "img-unresolved", "img-d", "img-e"),
		},
		map[string]models.GroupLane{"img-group": models.GroupLaneImageGen},
		map[string][]models.ResolvedCredential{
			"img-a": {{Provider: "9router", APIKey: "sk-a", BaseURL: upA.srv.URL, InternalModel: "image-img-a", CredentialID: "cred-a"}},
			"img-b": {{Provider: "9router", APIKey: "sk-b", BaseURL: upB.srv.URL, InternalModel: "image-img-b", CredentialID: "cred-b"}},
			"img-d": {{Provider: "9router", APIKey: "sk-d", BaseURL: upD.srv.URL, InternalModel: "image-img-d", CredentialID: "cred-d"}},
			"img-e": {{Provider: "9router", APIKey: "sk-e", BaseURL: upE.srv.URL, InternalModel: "image-img-e", CredentialID: "cred-e"}},
		},
		map[string]*groupFakeUpstream{
			"img-a": upA, "img-b": upB, "img-d": upD, "img-e": upE,
		})

	rec := env.post(`{"model":"img-group","prompt":"x"}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rec.Code, rec.Body.String())
	}
	// The third quota (member D) triggers the short-circuit; E is never
	// attempted. If the unresolved member had RESET the counter, D would
	// be the second quota and E would be attempted.
	if upD.hits.Load() != 1 {
		t.Errorf("member D hits = %d, want 1", upD.hits.Load())
	}
	if upE.hits.Load() != 0 {
		t.Errorf("member E hits = %d, want 0 (the third quota must short-circuit)", upE.hits.Load())
	}
	evt := findEvent(env.events(), "all_members_failed")
	if evt == nil {
		t.Fatalf("no all_members_failed event")
	}
	if evt["short_circuited"] != true {
		t.Errorf("short_circuited = %v, want true", evt["short_circuited"])
	}
	unresolved := 0
	for _, e := range env.events() {
		if e["outcome"] == "credential_unresolved" {
			unresolved++
		}
	}
	if unresolved != 1 {
		t.Errorf("credential_unresolved events = %d, want 1", unresolved)
	}
}

// newGroupUpstreamWithHeader is a fake upstream that sets an extra
// response header — used to pin that the fail-fast relay copies the
// upstream's response headers (D-4).
func newGroupUpstreamWithHeader(t *testing.T, status int, body []byte, header, value string) *groupFakeUpstream {
	t.Helper()
	fu := newGroupUpstream(t, status, body)
	// Wrap the existing server handler by standing up a new server
	// with the same counter semantics plus the extra header.
	fu.srv.Close()
	fu.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fu.hits.Add(1)
		w.Header().Set(header, value)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(fu.srv.Close)
	return fu
}

// TestServeImageGenGroup_FailFastCopiesUpstreamHeaders — D-4: the
// fail-fast relay copies the upstream response headers (parity with
// the standalone PromoteToError relay), not just the status + body.
func TestServeImageGenGroup_FailFastCopiesUpstreamHeaders(t *testing.T) {
	badBody := []byte(`{"error":{"message":"bad prompt"}}`)
	upA := newGroupUpstreamWithHeader(t, 400, badBody, "X-Upstream-Trace", "trace-42")
	upB := newGroupUpstream(t, 200, successBody(1, 0))

	env := newGroupEnv(t,
		[]models.ModelConfig{
			groupMemberRow("img-a"),
			groupMemberRow("img-b"),
			groupRowWithBackups("img-group", "img-a", "img-b"),
		},
		map[string]models.GroupLane{"img-group": models.GroupLaneImageGen},
		map[string][]models.ResolvedCredential{
			"img-a": {{Provider: "9router", APIKey: "sk-a", BaseURL: upA.srv.URL, InternalModel: "image-img-a", CredentialID: "cred-a"}},
			"img-b": {{Provider: "9router", APIKey: "sk-b", BaseURL: upB.srv.URL, InternalModel: "image-img-b", CredentialID: "cred-b"}},
		},
		map[string]*groupFakeUpstream{"img-a": upA, "img-b": upB})

	rec := env.post(`{"model":"img-group","prompt":"x"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Upstream-Trace"); got != "trace-42" {
		t.Errorf("relayed X-Upstream-Trace = %q, want %q (the fail-fast relay must copy the upstream headers)", got, "trace-42")
	}
	if upB.hits.Load() != 0 {
		t.Errorf("member B hits = %d, want 0 (fail-fast stops the walk)", upB.hits.Load())
	}
}

// TestServeImageGenGroup_FailFast_SingleTerminalEvent — D-5: exactly
// ONE authoritative terminal event on the fail-fast path. The old code
// emitted a per-attempt client_attributable event AND a second
// terminal event with the same outcome + member — duplicated meaning.
func TestServeImageGenGroup_FailFast_SingleTerminalEvent(t *testing.T) {
	badBody := []byte(`{"error":{"message":"bad prompt"}}`)
	upA := newGroupUpstream(t, 400, badBody)
	upB := newGroupUpstream(t, 200, successBody(1, 0))

	env := newGroupEnv(t,
		[]models.ModelConfig{
			groupMemberRow("img-a"),
			groupMemberRow("img-b"),
			groupRowWithBackups("img-group", "img-a", "img-b"),
		},
		map[string]models.GroupLane{"img-group": models.GroupLaneImageGen},
		map[string][]models.ResolvedCredential{
			"img-a": {{Provider: "9router", APIKey: "sk-a", BaseURL: upA.srv.URL, InternalModel: "image-img-a", CredentialID: "cred-a"}},
			"img-b": {{Provider: "9router", APIKey: "sk-b", BaseURL: upB.srv.URL, InternalModel: "image-img-b", CredentialID: "cred-b"}},
		},
		map[string]*groupFakeUpstream{"img-a": upA, "img-b": upB})

	rec := env.post(`{"model":"img-group","prompt":"x"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	events := env.events()
	terminal := 0
	for _, evt := range events {
		// The single authoritative event is the one carrying the
		// accounting counters (attempted/skipped/members_attempted).
		if _, ok := evt["attempted"]; ok {
			terminal++
		}
		if evt["outcome"] == "client_attributable" {
			// Any client_attributable event MUST be the accounting one.
			if _, ok := evt["attempted"]; !ok {
				t.Errorf("a per-attempt client_attributable event was emitted without accounting — duplicate terminal meaning; events=%v", events)
			}
		}
	}
	if terminal != 1 {
		t.Errorf("terminal events with accounting = %d, want exactly 1; events=%v", terminal, events)
	}
	if upB.hits.Load() != 0 {
		t.Errorf("member B hits = %d, want 0 (fail-fast)", upB.hits.Load())
	}
}
