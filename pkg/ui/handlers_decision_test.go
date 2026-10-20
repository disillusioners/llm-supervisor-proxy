package ui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// =============================================================================
// Decision-model TEST button — POST /fe/api/decisions/test
//
// The suite pins the four contracts the feature depends on:
//
//   (a) a successful decision relays through with the typed answers
//       (predicate + probability),
//   (b) the upstream's own error statuses (402 / 429 / 401) surface
//       as a CLASSIFIED status on the test DTO (not a raw dump),
//   (c) the canned body + a SINGLE Content-Type + the Authorization
//       header reach the upstream — and the canned body carries NO
//       safety_identifier (the typesafe arm hard-rejects it),
//   (d) the model / credential preconditions (exact row id,
//       decision-kind gate, first-credential selection).
//
// No live network: an httptest server plays the upstream.
// =============================================================================

// decisionTestCapture records what the fake upstream saw.
type decisionTestCapture struct {
	Calls         int
	Path          string
	Method        string
	ContentTypes  []string
	Authorization string
	Body          []byte
}

// newDecisionTestUpstream spins an httptest upstream answering with
// the given status + body and capturing the inbound request.
func newDecisionTestUpstream(t *testing.T, status int, respBody string) (*httptest.Server, *decisionTestCapture) {
	t.Helper()
	cap := &decisionTestCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.Calls++
		cap.Path = r.URL.Path
		cap.Method = r.Method
		cap.ContentTypes = r.Header.Values("Content-Type")
		cap.Authorization = r.Header.Get("Authorization")
		buf := make([]byte, 0)
		if r.Body != nil {
			buf = readAllForDecisionTest(r)
		}
		cap.Body = buf
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(respBody))
	}))
	return srv, cap
}

// readAllForDecisionTest reads the request body (bounded).
func readAllForDecisionTest(r *http.Request) []byte {
	defer r.Body.Close()
	buf, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	return buf
}

// newDecisionTestServer wires a server with ONE decision model whose
// first credential points at the given upstream.
func newDecisionTestServer(t *testing.T, upstreamURL string) *Server {
	t.Helper()
	mock := newMockModelsConfigForHandlers()
	if err := mock.AddCredential(models.CredentialConfig{
		ID:       "cred-dec-1",
		Provider: "openai",
		APIKey:   "sk-decision-test-key",
		BaseURL:  upstreamURL,
	}); err != nil {
		t.Fatalf("add credential: %v", err)
	}
	if err := mock.AddModel(models.ModelConfig{
		ID:                           "decision-router-v1",
		Name:                         "Decision Router v1",
		Enabled:                      true,
		Kind:                         models.KindDecision,
		Internal:                     true,
		InternalModel:                "gpt-6-luna",
		ExcludeFromUltimateSwitching: true,
		Credentials: []models.CredentialRef{
			{CredentialID: "cred-dec-1", Weight: 1, Position: 0},
		},
	}); err != nil {
		t.Fatalf("add model: %v", err)
	}
	return &Server{modelsConfig: mock}
}

// postDecisionTest drives handleDecisionTest and decodes the DTO.
func postDecisionTest(t *testing.T, s *Server, modelID string) (int, DecisionTestResponse) {
	t.Helper()
	body, err := json.Marshal(DecisionTestRequest{ModelID: modelID})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/fe/api/decisions/test", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleDecisionTest(w, req)

	var resp DecisionTestResponse
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v (body=%s)", err, w.Body.String())
		}
	}
	return w.Code, resp
}

// TestHandleDecisionTest_SuccessRelaysAnswersAndProbability — (a)
// A 200 decision response relays with the typed answers: the
// predicate answer keeps its name + probability.
func TestHandleDecisionTest_SuccessRelaysAnswersAndProbability(t *testing.T) {
	upstream, cap := newDecisionTestUpstream(t, http.StatusOK,
		`{"model":"gpt-6-luna","answers":[{"type":"predicate","name":"is_refund_request","probability":0.93}],"usage":{"input_tokens":42,"output_tokens":7,"total_tokens":49}}`)
	defer upstream.Close()

	s := newDecisionTestServer(t, upstream.URL)
	code, resp := postDecisionTest(t, s, "decision-router-v1")

	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if !resp.Success {
		t.Fatalf("success = false, want true (resp=%+v)", resp)
	}
	if resp.Status != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.Status)
	}
	if resp.Model != "gpt-6-luna" {
		t.Errorf("model = %q, want %q (the upstream echo)", resp.Model, "gpt-6-luna")
	}
	if resp.Provider != "openai" {
		t.Errorf("provider = %q, want %q", resp.Provider, "openai")
	}
	if resp.CredentialID != "cred-dec-1" {
		t.Errorf("credential_id = %q, want %q", resp.CredentialID, "cred-dec-1")
	}
	if len(resp.Answers) != 1 {
		t.Fatalf("answers = %d, want 1 (%+v)", len(resp.Answers), resp.Answers)
	}
	ans := resp.Answers[0]
	if ans.Type != "predicate" {
		t.Errorf("answer type = %q, want %q", ans.Type, "predicate")
	}
	if ans.Name != "is_refund_request" {
		t.Errorf("answer name = %q, want %q", ans.Name, "is_refund_request")
	}
	if ans.Probability == nil {
		t.Fatalf("answer probability = nil, want 0.93")
	}
	if *ans.Probability < 0.92 || *ans.Probability > 0.94 {
		t.Errorf("answer probability = %v, want ~0.93", *ans.Probability)
	}
	if resp.Usage == nil || resp.Usage.InputTokens != 42 {
		t.Errorf("usage = %+v, want input_tokens 42", resp.Usage)
	}
	if cap.Calls != 1 {
		t.Errorf("upstream calls = %d, want 1", cap.Calls)
	}
}

// TestHandleDecisionTest_UpstreamErrorStatusesAreClassified — (b)
// The upstream's own error status is the classification signal:
// 402 / 429 / 401 each surface as their own status on the DTO (the
// adapters promote every error envelope to 502 — that promotion is
// preserved verbatim in relay_status, but the FE must be able to
// tell "insufficient funds" from "rate limited").
func TestHandleDecisionTest_UpstreamErrorStatusesAreClassified(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		wantStatus int
		wantMsg    string
	}{
		{
			name:       "402 insufficient funds",
			status:     http.StatusPaymentRequired,
			body:       `{"error":{"message":"Insufficient credits to run this request.","type":"insufficient_quota","code":"insufficient_quota"}}`,
			wantStatus: http.StatusPaymentRequired,
			wantMsg:    "Insufficient credits",
		},
		{
			name:       "429 rate limited",
			status:     http.StatusTooManyRequests,
			body:       `{"error":{"message":"Rate limit reached for gpt-6-luna.","type":"rate_limit_error","code":"rate_limit_exceeded"}}`,
			wantStatus: http.StatusTooManyRequests,
			wantMsg:    "Rate limit reached",
		},
		{
			name:       "401 unauthorized",
			status:     http.StatusUnauthorized,
			body:       `{"error":{"message":"Incorrect API key provided.","type":"invalid_request_error","code":"invalid_api_key"}}`,
			wantStatus: http.StatusUnauthorized,
			wantMsg:    "Incorrect API key",
		},
		{
			name:       "422 endpoint unavailable",
			status:     http.StatusUnprocessableEntity,
			body:       `{"error":{"message":"Endpoint is unavailable","type":"invalid_request_error","code":"unknown_url"}}`,
			wantStatus: http.StatusUnprocessableEntity,
			wantMsg:    "Endpoint is unavailable",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstream, _ := newDecisionTestUpstream(t, tc.status, tc.body)
			defer upstream.Close()

			s := newDecisionTestServer(t, upstream.URL)
			code, resp := postDecisionTest(t, s, "decision-router-v1")

			if code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (the classified result travels in the body)", code)
			}
			if resp.Success {
				t.Fatalf("success = true, want false for upstream status %d", tc.status)
			}
			if resp.Status != tc.wantStatus {
				t.Errorf("status = %d, want %d (the upstream status is the classification signal)", resp.Status, tc.wantStatus)
			}
			if resp.UpstreamStatus != tc.wantStatus {
				t.Errorf("upstream_status = %d, want %d", resp.UpstreamStatus, tc.wantStatus)
			}
			if resp.RelayStatus != http.StatusBadGateway {
				t.Errorf("relay_status = %d, want 502 (the adapter's promotion, preserved verbatim)", resp.RelayStatus)
			}
			if !strings.Contains(resp.Error, tc.wantMsg) {
				t.Errorf("error = %q, want it to contain %q", resp.Error, tc.wantMsg)
			}
			if len(resp.Answers) != 0 {
				t.Errorf("answers = %d, want 0 on a failed test", len(resp.Answers))
			}
		})
	}
}

// TestHandleDecisionTest_CannedBodyReachesUpstreamCleanly — (c)
// The canned body is the validated one: the model token is the
// upstream model name (the same rewrite /v1/decisions performs), the
// input + question match the canned body verbatim, there is NO
// safety_identifier (the typesafe arm hard-rejects it), the
// Content-Type is present EXACTLY ONCE (the duplicate-CT bug class)
// and the Authorization header carries the credential's API key.
func TestHandleDecisionTest_CannedBodyReachesUpstreamCleanly(t *testing.T) {
	upstream, cap := newDecisionTestUpstream(t, http.StatusOK,
		`{"model":"gpt-6-luna","answers":[{"type":"predicate","name":"is_refund_request","probability":0.5}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
	defer upstream.Close()

	s := newDecisionTestServer(t, upstream.URL)
	if code, _ := postDecisionTest(t, s, "decision-router-v1"); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}

	if cap.Calls != 1 {
		t.Fatalf("upstream calls = %d, want 1", cap.Calls)
	}
	if cap.Method != http.MethodPost {
		t.Errorf("method = %q, want POST", cap.Method)
	}
	if cap.Path != "/v1/decisions" {
		t.Errorf("path = %q, want /v1/decisions", cap.Path)
	}
	if len(cap.ContentTypes) != 1 {
		t.Fatalf("Content-Type headers = %v, want EXACTLY ONE (the duplicate-CT bug class)", cap.ContentTypes)
	}
	if cap.ContentTypes[0] != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", cap.ContentTypes[0])
	}
	if want := "Bearer sk-decision-test-key"; cap.Authorization != want {
		t.Errorf("Authorization = %q, want %q", cap.Authorization, want)
	}

	var sent map[string]interface{}
	if err := json.Unmarshal(cap.Body, &sent); err != nil {
		t.Fatalf("decode upstream body: %v (body=%s)", err, string(cap.Body))
	}
	if got := sent["model"]; got != "gpt-6-luna" {
		t.Errorf("model = %v, want %q (the upstream model name — the same rewrite /v1/decisions performs)", got, "gpt-6-luna")
	}
	if got := sent["input"]; got != "Customer wrote: I was charged twice for order #4411 and want a refund." {
		t.Errorf("input = %v, want the canned input verbatim", got)
	}
	if _, present := sent["safety_identifier"]; present {
		t.Errorf("safety_identifier present in the canned body — the typesafe arm hard-rejects it (400)")
	}
	questions, ok := sent["questions"].([]interface{})
	if !ok || len(questions) != 1 {
		t.Fatalf("questions = %v, want exactly one question", sent["questions"])
	}
	q, ok := questions[0].(map[string]interface{})
	if !ok {
		t.Fatalf("question = %v, want an object", questions[0])
	}
	if q["type"] != "predicate" {
		t.Errorf("question type = %v, want predicate", q["type"])
	}
	if q["name"] != "is_refund_request" {
		t.Errorf("question name = %v, want is_refund_request", q["name"])
	}
	if q["instructions"] != "Is the customer asking for a refund?" {
		t.Errorf("question instructions = %v, want the canned instructions", q["instructions"])
	}
}

// TestHandleDecisionTest_RejectsNonDecisionModel — (d) the
// decision-kind gate: a chat row posted to the decision test route
// is refused (the /v1/decisions handler rejects it the same way).
func TestHandleDecisionTest_RejectsNonDecisionModel(t *testing.T) {
	upstream, cap := newDecisionTestUpstream(t, http.StatusOK, `{}`)
	defer upstream.Close()

	mock := newMockModelsConfigForHandlers()
	if err := mock.AddCredential(models.CredentialConfig{
		ID: "cred-chat-1", Provider: "openai", APIKey: "k", BaseURL: upstream.URL,
	}); err != nil {
		t.Fatalf("add credential: %v", err)
	}
	if err := mock.AddModel(models.ModelConfig{
		ID: "chat-model-1", Name: "Chat", Enabled: true, Internal: true, InternalModel: "gpt-4o",
		Credentials: []models.CredentialRef{{CredentialID: "cred-chat-1", Weight: 1, Position: 0}},
	}); err != nil {
		t.Fatalf("add model: %v", err)
	}
	s := &Server{modelsConfig: mock}

	code, _ := postDecisionTest(t, s, "chat-model-1")
	if code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (a chat row is not a decision model)", code)
	}
	if cap.Calls != 0 {
		t.Errorf("upstream calls = %d, want 0 (the gate fires before any upstream call)", cap.Calls)
	}
}

// TestHandleDecisionTest_UnknownModelIsNotFound — (d) an unknown
// row id is a 404 and never reaches the upstream.
func TestHandleDecisionTest_UnknownModelIsNotFound(t *testing.T) {
	upstream, cap := newDecisionTestUpstream(t, http.StatusOK, `{}`)
	defer upstream.Close()

	s := newDecisionTestServer(t, upstream.URL)
	code, _ := postDecisionTest(t, s, "no-such-decision-model")
	if code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", code)
	}
	if cap.Calls != 0 {
		t.Errorf("upstream calls = %d, want 0", cap.Calls)
	}
}

// TestHandleDecisionTest_UsesFirstCredentialByExactID — (d) the
// test-button credential convention: the model's FIRST credential
// is selected via an exact-ID lookup (not the LB engine's pick).
// The second credential is a different provider — if the engine
// picked it, the provider would not be "openai".
func TestHandleDecisionTest_UsesFirstCredentialByExactID(t *testing.T) {
	upstream, cap := newDecisionTestUpstream(t, http.StatusOK,
		`{"model":"gpt-6-luna","answers":[{"type":"predicate","name":"is_refund_request","probability":0.1}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
	defer upstream.Close()

	mock := newMockModelsConfigForHandlers()
	// The FIRST credential (the one the convention selects).
	if err := mock.AddCredential(models.CredentialConfig{
		ID: "cred-first", Provider: "openai", APIKey: "sk-first", BaseURL: upstream.URL,
	}); err != nil {
		t.Fatalf("add credential: %v", err)
	}
	// A second credential with a DIFFERENT provider — a pick of it
	// would change the provider (and the upstream URL).
	if err := mock.AddCredential(models.CredentialConfig{
		ID: "cred-second", Provider: "typesafe", APIKey: "sk-second", BaseURL: "https://example.invalid",
	}); err != nil {
		t.Fatalf("add credential: %v", err)
	}
	if err := mock.AddModel(models.ModelConfig{
		ID: "decision-router-v1", Name: "Decision Router v1", Enabled: true,
		Kind: models.KindDecision, Internal: true, InternalModel: "gpt-6-luna",
		ExcludeFromUltimateSwitching: true,
		Credentials: []models.CredentialRef{
			{CredentialID: "cred-first", Weight: 1, Position: 0},
			{CredentialID: "cred-second", Weight: 1, Position: 1},
		},
	}); err != nil {
		t.Fatalf("add model: %v", err)
	}
	s := &Server{modelsConfig: mock}

	_, resp := postDecisionTest(t, s, "decision-router-v1")
	if !resp.Success {
		t.Fatalf("success = false, want true (resp=%+v)", resp)
	}
	if resp.CredentialID != "cred-first" {
		t.Errorf("credential_id = %q, want %q (the FIRST credential)", resp.CredentialID, "cred-first")
	}
	if resp.Provider != "openai" {
		t.Errorf("provider = %q, want openai", resp.Provider)
	}
	if cap.Calls != 1 {
		t.Fatalf("upstream calls = %d, want 1", cap.Calls)
	}
	if want := "Bearer sk-first"; cap.Authorization != want {
		t.Errorf("Authorization = %q, want %q (the first credential's key)", cap.Authorization, want)
	}
}

// TestHandleDecisionTest_NoCredentialsIsRejected — (d) a decision
// row without credentials cannot be probed.
func TestHandleDecisionTest_NoCredentialsIsRejected(t *testing.T) {
	mock := newMockModelsConfigForHandlers()
	if err := mock.AddModel(models.ModelConfig{
		ID: "decision-router-v1", Name: "Decision Router v1", Enabled: true,
		Kind: models.KindDecision, Internal: true, InternalModel: "gpt-6-luna",
		ExcludeFromUltimateSwitching: true,
	}); err != nil {
		t.Fatalf("add model: %v", err)
	}
	s := &Server{modelsConfig: mock}

	code, _ := postDecisionTest(t, s, "decision-router-v1")
	if code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (the model has no credentials)", code)
	}
}

// TestHandleDecisionTest_TypesafeArmReachesSystemOne — the zen /
// typesafe arm: the canned body passes the typesafe pre-flight (it
// carries NO safety_identifier), is translated to the System One
// shape, and the predicate answer comes back with its probability.
func TestHandleDecisionTest_TypesafeArmReachesSystemOne(t *testing.T) {
	upstream, cap := newDecisionTestUpstream(t, http.StatusOK,
		`{"model":"jev-1.13","answers":{"is_refund_request":{"noul":0.88}},"usage":{"input_tokens":11,"output_tokens":3}}`)
	defer upstream.Close()

	mock := newMockModelsConfigForHandlers()
	if err := mock.AddCredential(models.CredentialConfig{
		ID: "cred-ts", Provider: "typesafe", APIKey: "sk-typesafe", BaseURL: upstream.URL,
	}); err != nil {
		t.Fatalf("add credential: %v", err)
	}
	if err := mock.AddModel(models.ModelConfig{
		ID: "decision-router-v1", Name: "Decision Router v1", Enabled: true,
		Kind: models.KindDecision, Internal: true, InternalModel: "jev-1.13",
		ExcludeFromUltimateSwitching: true,
		Credentials:                  []models.CredentialRef{{CredentialID: "cred-ts", Weight: 1, Position: 0}},
	}); err != nil {
		t.Fatalf("add model: %v", err)
	}
	s := &Server{modelsConfig: mock}

	code, resp := postDecisionTest(t, s, "decision-router-v1")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if !resp.Success {
		t.Fatalf("success = false, want true (resp=%+v)", resp)
	}
	if resp.Provider != "typesafe" {
		t.Errorf("provider = %q, want typesafe", resp.Provider)
	}
	if len(resp.Answers) != 1 {
		t.Fatalf("answers = %d, want 1", len(resp.Answers))
	}
	if resp.Answers[0].Probability == nil || *resp.Answers[0].Probability < 0.87 || *resp.Answers[0].Probability > 0.89 {
		t.Errorf("answer probability = %v, want ~0.88", resp.Answers[0].Probability)
	}

	if cap.Path != "/v1/systemone" {
		t.Errorf("path = %q, want /v1/systemone", cap.Path)
	}
	var sent map[string]interface{}
	if err := json.Unmarshal(cap.Body, &sent); err != nil {
		t.Fatalf("decode upstream body: %v (body=%s)", err, string(cap.Body))
	}
	if got := sent["model"]; got != "jev-1.13" {
		t.Errorf("model = %v, want jev-1.13 (the upstream model name)", got)
	}
	if got := sent["state"]; got != "Customer wrote: I was charged twice for order #4411 and want a refund." {
		t.Errorf("state = %v, want the canned input translated to the System One state", got)
	}
	if _, present := sent["safety_identifier"]; present {
		t.Errorf("safety_identifier present in the upstream body — the typesafe arm hard-rejects it")
	}
	questions, ok := sent["questions"].(map[string]interface{})
	if !ok {
		t.Fatalf("questions = %v, want a map keyed by the question name", sent["questions"])
	}
	q, ok := questions["is_refund_request"].(map[string]interface{})
	if !ok {
		t.Fatalf("questions[is_refund_request] = %v, want an object", questions["is_refund_request"])
	}
	if q["type"] != "noul" {
		t.Errorf("question type = %v, want noul (the System One predicate discriminator)", q["type"])
	}
}

// TestHandleDecisionTest_MissingModelIDIsBadRequest — the FE must
// send the row id.
func TestHandleDecisionTest_MissingModelIDIsBadRequest(t *testing.T) {
	s := &Server{modelsConfig: newMockModelsConfigForHandlers()}
	req := httptest.NewRequest(http.MethodPost, "/fe/api/decisions/test", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	s.handleDecisionTest(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

// TestHandleDecisionTest_RejectsNonPost — house method guard.
func TestHandleDecisionTest_RejectsNonPost(t *testing.T) {
	s := &Server{modelsConfig: newMockModelsConfigForHandlers()}
	req := httptest.NewRequest(http.MethodGet, "/fe/api/decisions/test", nil)
	w := httptest.NewRecorder()
	s.handleDecisionTest(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
}

// TestHandleDecisionTest_TransportFailureCarriesTheCause — the
// LOCAL failure mode: the upstream is unreachable (the fake server
// was closed), so the probe never produced a response.
//
// The DTO must still be the STRUCTURED CLASSIFIED SHAPE (a 200 + the
// classification status) AND its error field must carry the CAUSE.
// Before the fix the error field was EMPTY (the route read only the
// upstream body — empty here — and then the adapter's reason — also
// empty here, because a transport failure never reaches the
// adapter), so the FE rendered a bare "Upstream error (HTTP 502)"
// and the [decision-test] log line carried no cause either.
func TestHandleDecisionTest_TransportFailureCarriesTheCause(t *testing.T) {
	// Spin an upstream up, take its URL, then CLOSE it — the port is
	// closed, so the probe's upstream call fails locally (connection
	// refused) instead of getting an upstream answer.
	dead, _ := newDecisionTestUpstream(t, http.StatusOK, `{}`)
	deadURL := dead.URL
	dead.Close()

	s := newDecisionTestServer(t, deadURL)
	code, resp := postDecisionTest(t, s, "decision-router-v1")

	// The classified result still travels in a 200 (the FE routes
	// this call through apiFetch, which throws on a non-2xx status).
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the classified result travels in the body)", code)
	}
	if resp.Success {
		t.Fatalf("success = true, want false for an unreachable upstream (resp=%+v)", resp)
	}
	if resp.UpstreamStatus != 0 {
		t.Errorf("upstream_status = %d, want 0 (the upstream never answered)", resp.UpstreamStatus)
	}
	if resp.Status != http.StatusBadGateway && resp.Status != http.StatusGatewayTimeout {
		t.Errorf("status = %d, want 502 (bad gateway) or 504 (timeout)", resp.Status)
	}
	// THE regression assertion: the error field carries the cause.
	if strings.TrimSpace(resp.Error) == "" {
		t.Fatalf("error = %q, want it to carry the local failure cause (resp=%+v)", resp.Error, resp)
	}
	if !strings.Contains(strings.ToLower(resp.Error), "upstream") {
		t.Errorf("error = %q, want it to mention the upstream failure", resp.Error)
	}
	// The cause came from result.Error, NOT from the adapter's reason
	// (a transport failure never produces an adapter outcome).
	if resp.Reason != "" {
		t.Errorf("reason = %q, want empty (a transport failure never reaches the adapter)", resp.Reason)
	}
}
