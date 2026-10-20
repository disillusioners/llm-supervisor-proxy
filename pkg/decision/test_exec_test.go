package decision

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// =============================================================================
// ExecuteTest — the operator-facing single-shot decision probe.
//
// The suite pins the contracts the FE test button depends on:
//
//   - the typesafe pre-flight rejects a body carrying
//     safety_identifier with a 400 BEFORE any upstream call,
//   - the upstream's own error status (402 / 429 / 401 ...) is the
//     classification signal while the adapter's promoted
//     RelayStatus (502) is preserved verbatim alongside it,
//   - a success relays the typed response (answers + probability),
//   - the upstream request carries EXACTLY ONE Content-Type and the
//     credential's bearer token (the duplicate-CT bug class must not
//     come back).
//
// Same-package white-box (package decision), stdlib-only — mirrors
// the house test style of pkg/decision/handler_test.go.
// =============================================================================

// testExecCred builds a resolved credential for the openai adapter.
func testExecCred(baseURL string) models.ResolvedCredential {
	return models.ResolvedCredential{
		Provider:      OpenAIDecisionProviderName,
		APIKey:        "sk-decision-exec-key",
		BaseURL:       baseURL,
		InternalModel: "gpt-6-luna",
		CredentialID:  "cred-exec-1",
	}
}

// testExecBody builds a raw canned body + its typed twin.
func testExecBody(t *testing.T, modelID string, extra map[string]interface{}) ([]byte, *DecisionRequest) {
	t.Helper()
	body := map[string]interface{}{
		"model": modelID,
		"input": "Customer wrote: I was charged twice for order #4411 and want a refund.",
		"questions": []map[string]interface{}{
			{
				"type":         "predicate",
				"name":         "is_refund_request",
				"instructions": "Is the customer asking for a refund?",
			},
		},
	}
	for k, v := range extra {
		body[k] = v
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	var typed DecisionRequest
	if err := json.Unmarshal(raw, &typed); err != nil {
		t.Fatalf("unmarshal typed body: %v", err)
	}
	return raw, &typed
}

// TestExecuteTest_TypesafePreflightRejectsSafetyIdentifier — the
// typesafe arm hard-rejects a body carrying safety_identifier
// (validators.go:248). The probe must surface that as a 400 and
// must NOT reach the upstream.
func TestExecuteTest_TypesafePreflightRejectsSafetyIdentifier(t *testing.T) {
	srv, fake := newFakeUpstream(t, fakeScript{
		status: http.StatusOK,
		body:   []byte(`{"model":"jev-1.13","answers":[],"usage":{}}`),
	})
	defer srv.Close()

	raw, typed := testExecBody(t, "decision-router-v1", map[string]interface{}{
		"safety_identifier": "user-123",
	})
	cred := testExecCred(srv.URL)
	cred.Provider = TypesafeProviderName
	cred.InternalModel = "jev-1.13"

	res := ExecuteTest(cred, typed, raw, 5*time.Second)

	if res.Status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", res.Status)
	}
	if !strings.Contains(res.Error, "safety_identifier") {
		t.Errorf("error = %q, want it to mention safety_identifier", res.Error)
	}
	if res.Response != nil {
		t.Errorf("response = %+v, want nil", res.Response)
	}
	if got := fake.hitCount.Load(); got != 0 {
		t.Errorf("upstream hits = %d, want 0 (the pre-flight fires before the call)", got)
	}
}

// TestExecuteTest_UpstreamErrorStatusIsClassified — the upstream's
// own status is the classification signal; the adapter's promoted
// RelayStatus (502) is preserved verbatim beside it.
func TestExecuteTest_UpstreamErrorStatusIsClassified(t *testing.T) {
	for _, status := range []int{
		http.StatusPaymentRequired,
		http.StatusTooManyRequests,
		http.StatusUnauthorized,
		http.StatusUnprocessableEntity,
	} {
		srv, fake := newFakeUpstream(t, fakeScript{
			status: status,
			body:   []byte(`{"error":{"message":"upstream says no","type":"invalid_request_error"}}`),
		})

		raw, typed := testExecBody(t, "decision-router-v1", nil)
		res := ExecuteTest(testExecCred(srv.URL), typed, raw, 5*time.Second)

		if res.Status != status {
			t.Errorf("status = %d, want %d (the upstream status is the classification signal)", res.Status, status)
		}
		if res.UpstreamStatus != status {
			t.Errorf("upstream_status = %d, want %d", res.UpstreamStatus, status)
		}
		if res.RelayStatus != http.StatusBadGateway {
			t.Errorf("relay_status = %d, want 502 (the adapter's promotion, preserved verbatim)", res.RelayStatus)
		}
		if res.Response != nil {
			t.Errorf("response = %+v, want nil on an error", res.Response)
		}
		if string(res.Body) != `{"error":{"message":"upstream says no","type":"invalid_request_error"}}` {
			t.Errorf("body = %q, want the upstream body verbatim", string(res.Body))
		}
		if got := fake.hitCount.Load(); got != 1 {
			t.Errorf("upstream hits = %d, want 1", got)
		}
		srv.Close()
	}
}

// TestExecuteTest_SuccessReturnsTypedResponse — the happy path
// relays the typed response (the answers + probability survive).
func TestExecuteTest_SuccessReturnsTypedResponse(t *testing.T) {
	srv, fake := newFakeUpstream(t, fakeScript{
		status: http.StatusOK,
		body:   []byte(`{"model":"gpt-6-luna","answers":[{"type":"predicate","name":"is_refund_request","probability":0.87}],"usage":{"input_tokens":11,"output_tokens":3,"total_tokens":14}}`),
	})
	defer srv.Close()

	raw, typed := testExecBody(t, "decision-router-v1", nil)
	res := ExecuteTest(testExecCred(srv.URL), typed, raw, 5*time.Second)

	if res.Status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (error=%q reason=%q)", res.Status, res.Error, res.Reason)
	}
	if res.RelayStatus != 0 {
		t.Errorf("relay_status = %d, want 0 (PassAsIs)", res.RelayStatus)
	}
	if res.UpstreamStatus != http.StatusOK {
		t.Errorf("upstream_status = %d, want 200", res.UpstreamStatus)
	}
	if res.Response == nil {
		t.Fatalf("response = nil, want the typed response")
	}
	if res.Response.Model != "gpt-6-luna" {
		t.Errorf("response model = %q, want gpt-6-luna", res.Response.Model)
	}
	if len(res.Response.Answers) != 1 {
		t.Fatalf("answers = %d, want 1", len(res.Response.Answers))
	}
	ans := res.Response.Answers[0]
	if ans.Type != QuestionTypePredicate {
		t.Errorf("answer type = %q, want predicate", ans.Type)
	}
	if ans.Probability == nil || *ans.Probability < 0.86 || *ans.Probability > 0.88 {
		t.Errorf("answer probability = %v, want ~0.87", ans.Probability)
	}
	if res.Response.Usage.InputTokens != 11 {
		t.Errorf("usage.input_tokens = %d, want 11", res.Response.Usage.InputTokens)
	}

	// The upstream request is clean: exactly ONE Content-Type, the
	// credential's bearer token, and the model token rewritten to
	// the upstream model name (the byte-faithful openai rewrite).
	if got := len(fake.lastCT); got != 1 {
		t.Errorf("Content-Type count = %d, want exactly 1 (values=%v)", got, fake.lastCT)
	}
	if fake.lastAuthH != "Bearer sk-decision-exec-key" {
		t.Errorf("Authorization = %q, want the credential bearer", fake.lastAuthH)
	}
	if !strings.Contains(string(fake.lastBody), `"model":"gpt-6-luna"`) {
		t.Errorf("upstream body = %s, want the model token rewritten to the upstream model name", string(fake.lastBody))
	}
	if strings.Contains(string(fake.lastBody), "safety_identifier") {
		t.Errorf("upstream body = %s, want no safety_identifier", string(fake.lastBody))
	}
}

// TestExecuteTest_UnsupportedProviderIsBadGateway — a provider with
// no decision adapter is a clean 502 (no upstream call).
func TestExecuteTest_UnsupportedProviderIsBadGateway(t *testing.T) {
	srv, fake := newFakeUpstream(t, fakeScript{status: http.StatusOK, body: []byte(`{}`)})
	defer srv.Close()

	raw, typed := testExecBody(t, "decision-router-v1", nil)
	cred := testExecCred(srv.URL)
	cred.Provider = "anthropic"

	res := ExecuteTest(cred, typed, raw, 5*time.Second)

	if res.Status != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", res.Status)
	}
	if !strings.Contains(res.Error, "no decision adapter") {
		t.Errorf("error = %q, want the 'no decision adapter' message", res.Error)
	}
	if got := fake.hitCount.Load(); got != 0 {
		t.Errorf("upstream hits = %d, want 0", got)
	}
}

// TestExecuteTest_TransportErrorIsBadGateway — a dead upstream is a
// 502 with a readable message (never a raw dump).
func TestExecuteTest_TransportErrorIsBadGateway(t *testing.T) {
	srv, _ := newFakeUpstream(t, fakeScript{status: http.StatusOK, body: []byte(`{}`)})
	baseURL := srv.URL
	srv.Close() // the upstream is gone — the call must fail cleanly

	raw, typed := testExecBody(t, "decision-router-v1", nil)
	res := ExecuteTest(testExecCred(baseURL), typed, raw, 2*time.Second)

	if res.Status != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", res.Status)
	}
	if res.Error == "" {
		t.Errorf("error = %q, want a readable failure message", res.Error)
	}
	if res.Response != nil {
		t.Errorf("response = %+v, want nil", res.Response)
	}
}
