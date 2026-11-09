package imggen

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/usage"
)

func b64JPEG() string {
	return base64.StdEncoding.EncodeToString([]byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46})
}

func newEnv9Router(t *testing.T, script fakeScript) *handlerTestEnv {
	t.Helper()
	upstream, fake := newFakeMiniMax(t, script)
	db, _ := newUsageTestDB(t)
	uc := usage.NewCounter(db, database.SQLite)
	bus := events.NewBus()
	ts := newMemTokenStore()

	credBaseURL := upstream.URL
	h := NewHandler(stubConfig{timeout: 200 * time.Millisecond}, bus, ts, uc)
	modelID := "9router-image-01"
	h.SetModelResolver(func(id string) (*models.ModelConfig, bool) {
		if id != modelID {
			return nil, false
		}
		return &models.ModelConfig{
			ID:              modelID,
			Name:            "Image Gen 9Router",
			Kind:            models.KindImageGen,
			Enabled:         true,
			Internal:        true,
			InternalModel:   "ag/gemini-3.8-flash",
			InternalBaseURL: credBaseURL,
			Credentials: []models.CredentialRef{
				{CredentialID: "cred-1", Weight: 1, Position: 0},
			},
		}, true
	})
	h.SetCredentialResolver(func(id string) (models.ResolvedCredential, bool) {
		if id != modelID {
			return models.ResolvedCredential{}, false
		}
		return models.ResolvedCredential{
			Provider:      "9router",
			APIKey:        "sk-cred-secret",
			BaseURL:       credBaseURL,
			InternalModel: "ag/gemini-3.8-flash",
		}, true
	})
	tok := &auth.AuthToken{
		ID:            "tok-test",
		Name:          "Test Token",
		TokenHash:     auth.HashToken("sk-test-token"),
		AllowedModels: nil,
	}
	ts.add(tok)
	return &handlerTestEnv{
		t:          t,
		upstream:   upstream,
		fake:       fake,
		handler:    h,
		bus:        bus,
		usage:      uc,
		tokenStore: ts,
		modelID:    modelID,
	}
}

// TestHandler_9Router_ConversionRelay — the upstream returns an
// OpenAI-images body (b64_json); the client receives the converted
// body with data[].image_urls as a data-URI, and Content-Length is removed.
//
// Mutation discrimination (M1): detects if ResponseRewriter hook is REMOVED from 9router.
// If the hook is removed, body would remain raw upstream b64_json instead of converted data-URI image_urls.
func TestHandler_9Router_ConversionRelay(t *testing.T) {
	body := map[string]interface{}{
		"created": 7,
		"data":    []interface{}{map[string]interface{}{"b64_json": b64JPEG()}},
	}
	raw, _ := json.Marshal(body)
	env := newEnv9Router(t, fakeScript{status: 200, body: raw, contentLength: true})

	rec := env.sendRequest(http.MethodPost, `{"model":"`+env.modelID+`","prompt":"a cat"}`, nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	// Verify that the rewrite hook WAS invoked (body is converted, not raw b64_json passthrough,
	// and Content-Length header was removed so HTTP stack recomputes it).
	if bytes.Equal(rec.Body.Bytes(), raw) {
		t.Fatalf("body should have been converted by rewrite hook, but remained byte-equal to raw upstream")
	}
	if cl := rec.Header().Get("Content-Length"); cl != "" {
		t.Errorf("Content-Length header should have been deleted for converted 9router response, got: %q", cl)
	}

	var out map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	data := out["data"].([]interface{})
	item := data[0].(map[string]interface{})
	urls := item["image_urls"].([]interface{})
	if len(urls) != 1 {
		t.Fatalf("image_urls len = %d, want 1", len(urls))
	}
	uri := urls[0].(string)
	if !strings.HasPrefix(uri, "data:image/jpeg;base64,") {
		t.Errorf("uri = %q, want data:image/jpeg;base64,", uri[:40])
	}
}

// TestHandler_9Router_URLShapedUpstreamResponse — if upstream returns a
// URL-shaped response instead of b64_json, conversion fails cleanly with 502
// response_conversion_failed and OpenAI error envelope.
func TestHandler_9Router_URLShapedUpstreamResponse(t *testing.T) {
	raw := []byte(`{"created":123,"data":[{"url":"https://example.com/cat.png"}]}`)
	env := newEnv9Router(t, fakeScript{status: 200, body: raw})

	rec := env.sendRequest(http.MethodPost, `{"model":"`+env.modelID+`","prompt":"a cat"}`, nil)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	errObj, ok := out["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected OpenAI error envelope, got: %s", rec.Body.String())
	}
	msg, _ := errObj["message"].(string)
	if !strings.Contains(msg, "failed to convert upstream response") || !strings.Contains(msg, "no convertible image in data") {
		t.Errorf("message = %q, want conversion failure notice", msg)
	}

	// Meter failure: image_count must be 0
	rows, err := env.usage.GetModelUsage(context.Background(), startHour(time.Now()), startHour(time.Now()))
	if err != nil {
		t.Fatalf("GetModelUsage: %v", err)
	}
	if len(rows) > 0 && rows[0].ImageCount != 0 {
		t.Errorf("ImageCount = %d, want 0 on conversion failure", rows[0].ImageCount)
	}
}

// TestHandler_9Router_OutboundRequestBody_ResponseFormatB64Json — wire-level proof
// asserting the ACTUAL OUTBOUND request body received by upstream (env.fake.lastBody).
// Verifies response_format="b64_json" in BOTH cases: when client omits response_format
// and when client explicitly passes response_format="url".
func TestHandler_9Router_OutboundRequestBody_ResponseFormatB64Json(t *testing.T) {
	// Case 1: Client omits response_format (OpenAI default is "url").
	body1 := map[string]interface{}{
		"created": 1,
		"data":    []interface{}{map[string]interface{}{"b64_json": b64JPEG()}},
	}
	raw1, _ := json.Marshal(body1)
	env1 := newEnv9Router(t, fakeScript{status: 200, body: raw1})
	rec1 := env1.sendRequest(http.MethodPost, `{"model":"`+env1.modelID+`","prompt":"a cat"}`, nil)
	if rec1.Code != 200 {
		t.Fatalf("omitted case: status = %d, want 200; body=%s", rec1.Code, rec1.Body.String())
	}
	var outbound1 map[string]interface{}
	if err := json.Unmarshal(env1.fake.lastBody, &outbound1); err != nil {
		t.Fatalf("omitted case: parse upstream lastBody: %v", err)
	}
	if outbound1["response_format"] != "b64_json" {
		t.Errorf("omitted case: outbound response_format = %v, want b64_json", outbound1["response_format"])
	}

	// Case 2: Client explicitly passes response_format="url".
	body2 := map[string]interface{}{
		"created": 2,
		"data":    []interface{}{map[string]interface{}{"b64_json": b64JPEG()}},
	}
	raw2, _ := json.Marshal(body2)
	env2 := newEnv9Router(t, fakeScript{status: 200, body: raw2})
	rec2 := env2.sendRequest(http.MethodPost, `{"model":"`+env2.modelID+`","prompt":"a dog","response_format":"url"}`, nil)
	if rec2.Code != 200 {
		t.Fatalf("url case: status = %d, want 200; body=%s", rec2.Code, rec2.Body.String())
	}
	var outbound2 map[string]interface{}
	if err := json.Unmarshal(env2.fake.lastBody, &outbound2); err != nil {
		t.Fatalf("url case: parse upstream lastBody: %v", err)
	}
	if outbound2["response_format"] != "b64_json" {
		t.Errorf("url case: outbound response_format = %v, want b64_json", outbound2["response_format"])
	}
}

// TestHandler_9Router_PartialConversion_CountsAndBilling — upstream returns n=2
// with 1 convertible item and 1 unconvertible item. Asserts success_count=1,
// failed_count=1, and meter delta=1 (never overcharging for unconvertible items).
func TestHandler_9Router_PartialConversion_CountsAndBilling(t *testing.T) {
	body := map[string]interface{}{
		"created": 10,
		"data": []interface{}{
			map[string]interface{}{"b64_json": b64JPEG()},
			map[string]interface{}{}, // unconvertible item
		},
	}
	raw, _ := json.Marshal(body)
	env := newEnv9Router(t, fakeScript{status: 200, body: raw})

	rec := env.sendRequest(http.MethodPost, `{"model":"`+env.modelID+`","prompt":"two cats","n":2}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var out map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	data, ok := out["data"].([]interface{})
	if !ok || len(data) != 1 {
		t.Fatalf("len(data) = %d, want 1", len(data))
	}
	md, ok := out["metadata"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing metadata in response")
	}
	if md["success_count"] != "1" {
		t.Errorf("success_count = %v, want 1", md["success_count"])
	}
	if md["failed_count"] != "1" {
		t.Errorf("failed_count = %v, want 1", md["failed_count"])
	}

	// Meter assertion: usage counter billed exactly 1 image (delivered images), not 2
	rows, err := env.usage.GetModelUsage(context.Background(), startHour(time.Now()), startHour(time.Now()))
	if err != nil {
		t.Fatalf("GetModelUsage: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 model usage row, got %d", len(rows))
	}
	if rows[0].RequestCount != 1 {
		t.Errorf("request_count = %d, want 1", rows[0].RequestCount)
	}
	if rows[0].ImageCount != 1 {
		t.Errorf("image_count = %d, want 1 (must bill only delivered images)", rows[0].ImageCount)
	}
}

// TestHandler_9Router_429Quota — upstream returns 429 with a nested
// JSON message; the client sees 429 (not 502) and the normalized body.
func TestHandler_9Router_429Quota(t *testing.T) {
	inner := `{"error":{"code":429,"message":"You have exhausted your capacity on this model. Your quota will reset after 4h39m9s.","status":"RESOURCE_EXHAUSTED"}}`
	outerMsg := "[antigravity/gemini-3.8-flash] [429]: " + inner
	body, _ := json.Marshal(map[string]interface{}{
		"error": map[string]interface{}{
			"message": outerMsg,
			"type":    "invalid_request_error",
		},
	})
	env := newEnv9Router(t, fakeScript{status: 429, body: body})

	rec := env.sendRequest(http.MethodPost, `{"model":"`+env.modelID+`","prompt":"x"}`, nil)
	if rec.Code != 429 {
		t.Fatalf("status = %d, want 429; body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	em := out["error"].(map[string]interface{})
	if em["message"] != "You have exhausted your capacity on this model. Your quota will reset after 4h39m9s." {
		t.Errorf("message = %v, want unwrapped quota text", em["message"])
	}
}

// TestHandler_Minimax_ByteIdenticalStill — minimax path is untouched: a
// success body is relayed byte-for-byte (no hook runs), and header behavior
// strictly matches base (Content-Length stripped via isHopByHopHeader).
//
// Mutation discrimination:
//   - M2: detects if ResponseRewriter hook is inappropriately APPLIED to minimax.
//     Minimax does not implement ResponseRewriter, so body must remain byte-for-byte
//     identical to upstream.
//   - M4: base copyResponseHeaders drops Content-Length via isHopByHopHeader; minimax
//     preserves that exact base contract (Content-Length is stripped, body bytes identical).
//     Preserving Content-Length would change minimax's client-visible headers vs base,
//     violating the forbidden "Touch the minimax path's semantics" constraint.
func TestHandler_Minimax_ByteIdenticalStill(t *testing.T) {
	body := successBody(1, 0)
	env := newEnv(t, fakeScript{status: 200, body: body, contentLength: true})
	rec := env.sendRequest(http.MethodPost, `{"model":"`+env.modelID+`","prompt":"a cat"}`, nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	// Byte-for-byte body equality:
	if !bytes.Equal(rec.Body.Bytes(), body) {
		t.Errorf("minimax body not byte-identical")
	}
	// Base header behavior: Content-Length is considered hop-by-hop by isHopByHopHeader
	// and stripped by copyResponseHeaders, matching base behavior.
	if cl := rec.Header().Get("Content-Length"); cl != "" {
		t.Errorf("Content-Length = %q, want empty (base copyResponseHeaders strips Content-Length)", cl)
	}
}
