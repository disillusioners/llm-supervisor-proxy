package imggen

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// --- InspectResponse ---

func TestNineRouter_InspectResponse_Success(t *testing.T) {
	p := NewNineRouterProvider().(*nineRouterProvider)
	body := []byte(`{"created":123,"data":[{"b64_json":"AAAA"}]}`)
	got := p.InspectResponse(200, body)
	if got.RelayStatus != 0 {
		t.Errorf("RelayStatus = %d, want 0 (PassAsIs)", got.RelayStatus)
	}
	if got.Code != CodeOK {
		t.Errorf("Code = %q, want %q", got.Code, CodeOK)
	}
}

func TestNineRouter_InspectResponse_HTTP429_Quota(t *testing.T) {
	p := NewNineRouterProvider().(*nineRouterProvider)
	inner := `{"error":{"code":429,"message":"You have exhausted your capacity on this model. Your quota will reset after 4h39m9s.","status":"RESOURCE_EXHAUSTED","details":[{"quotaResetTimeStamp":"2026-11-09T12:59:16Z"}]}}`
	outerMsg := "[antigravity/gemini-3.8-flash] [429]: " + inner
	body, _ := json.Marshal(map[string]interface{}{
		"error": map[string]interface{}{
			"message": outerMsg,
			"type":    "invalid_request_error",
		},
	})
	got := p.InspectResponse(429, body)
	if got.RelayStatus != 429 {
		t.Errorf("RelayStatus = %d, want 429 (quota must not be mangled into 502)", got.RelayStatus)
	}
	if got.Code != CodeCooldown {
		t.Errorf("Code = %q, want %q", got.Code, CodeCooldown)
	}
	if !strings.Contains(got.Message, "You have exhausted your capacity on this model") {
		t.Errorf("Message = %q, want the unwrapped quota text", got.Message)
	}
	if strings.Contains(got.Message, "RESOURCE_EXHAUSTED") {
		t.Errorf("Message must be the human-readable text, not the nested blob: %q", got.Message)
	}
}

func TestNineRouter_InspectResponse_HTTP400(t *testing.T) {
	p := NewNineRouterProvider().(*nineRouterProvider)
	body := []byte(`{"error":{"message":"bad request"}}`)
	got := p.InspectResponse(400, body)
	if got.RelayStatus != 400 {
		t.Errorf("RelayStatus = %d, want 400", got.RelayStatus)
	}
	if got.Code != CodeUpstreamHTTPError {
		t.Errorf("Code = %q, want %q", got.Code, CodeUpstreamHTTPError)
	}
}

func TestNineRouter_InspectResponse_EmptyBody(t *testing.T) {
	p := NewNineRouterProvider().(*nineRouterProvider)
	got := p.InspectResponse(200, nil)
	if got.RelayStatus != 502 {
		t.Errorf("RelayStatus = %d, want 502", got.RelayStatus)
	}
	if got.Reason != "empty body" {
		t.Errorf("Reason = %q, want %q", got.Reason, "empty body")
	}
}

func TestNineRouter_InspectResponse_UnparseableJSON(t *testing.T) {
	p := NewNineRouterProvider().(*nineRouterProvider)
	got := p.InspectResponse(200, []byte(`<html>upstream</html>`))
	if got.RelayStatus != 502 {
		t.Errorf("RelayStatus = %d, want 502", got.RelayStatus)
	}
	if got.Reason != "unparseable JSON" {
		t.Errorf("Reason = %q, want %q", got.Reason, "unparseable JSON")
	}
}

func TestNineRouter_InspectResponse_MissingData(t *testing.T) {
	p := NewNineRouterProvider().(*nineRouterProvider)
	got := p.InspectResponse(200, []byte(`{"created":1}`))
	if got.RelayStatus != 502 {
		t.Errorf("RelayStatus = %d, want 502", got.RelayStatus)
	}
	if got.Reason != "missing data key" {
		t.Errorf("Reason = %q, want %q", got.Reason, "missing data key")
	}
}

func TestNineRouter_InspectResponse_EmptyData(t *testing.T) {
	p := NewNineRouterProvider().(*nineRouterProvider)
	got := p.InspectResponse(200, []byte(`{"data":[]}`))
	if got.RelayStatus != 502 {
		t.Errorf("RelayStatus = %d, want 502", got.RelayStatus)
	}
	if got.Code != CodeEmptyImage {
		t.Errorf("Code = %q, want %q", got.Code, CodeEmptyImage)
	}
}

func TestNineRouter_InspectResponse_DataWithNullItem(t *testing.T) {
	p := NewNineRouterProvider().(*nineRouterProvider)
	got := p.InspectResponse(200, []byte(`{"data":[null]}`))
	if got.RelayStatus != 502 {
		t.Errorf("RelayStatus = %d, want 502", got.RelayStatus)
	}
	if got.Code != CodeEmptyImage {
		t.Errorf("Code = %q, want %q", got.Code, CodeEmptyImage)
	}
}

func TestNineRouter_InspectResponse_DataWithEmptyObject(t *testing.T) {
	p := NewNineRouterProvider().(*nineRouterProvider)
	got := p.InspectResponse(200, []byte(`{"data":[{}]}`))
	if got.RelayStatus != 502 {
		t.Errorf("RelayStatus = %d, want 502", got.RelayStatus)
	}
	if got.Code != CodeEmptyImage {
		t.Errorf("Code = %q, want %q", got.Code, CodeEmptyImage)
	}
}

// --- BuildUpstream ---

func TestNineRouter_BuildUpstream_ExplicitURLResponseFormat(t *testing.T) {
	p := NewNineRouterProvider().(*nineRouterProvider)
	cred := models.ResolvedCredential{
		Provider:      "9router",
		APIKey:        "k",
		BaseURL:       "http://host:20128/v1",
		InternalModel: "ag/gemini-3.8-flash",
	}
	url, body, err := p.BuildUpstream(cred, []byte(`{"model":"m","prompt":"a cat","aspect_ratio":"16:9","response_format":"url","n":1}`))
	if err != nil {
		t.Fatalf("BuildUpstream: %v", err)
	}
	if url != "http://host:20128/v1/images/generations" {
		t.Errorf("URL = %q, want %q", url, "http://host:20128/v1/images/generations")
	}
	var m map[string]interface{}
	if err := jsonUnmarshal(body, &m); err != nil {
		t.Fatalf("body: %v", err)
	}
	if m["model"] != "ag/gemini-3.8-flash" {
		t.Errorf("model = %v, want ag/gemini-3.8-flash", m["model"])
	}
	if _, ok := m["aspect_ratio"]; ok {
		t.Errorf("aspect_ratio should be dropped, got %v", m["aspect_ratio"])
	}
	if m["response_format"] != "b64_json" {
		t.Errorf("response_format = %v, want b64_json", m["response_format"])
	}
	if m["prompt"] != "a cat" {
		t.Errorf("prompt = %v, want a cat", m["prompt"])
	}
}

func TestNineRouter_BuildUpstream_OmittedResponseFormat(t *testing.T) {
	p := NewNineRouterProvider().(*nineRouterProvider)
	cred := models.ResolvedCredential{
		Provider:      "9router",
		APIKey:        "k",
		BaseURL:       "http://host:20128/v1",
		InternalModel: "ag/gemini-3.8-flash",
	}
	// Client completely omits response_format (OpenAI default is "url").
	// Adapter must inject response_format: "b64_json" unconditionally.
	url, body, err := p.BuildUpstream(cred, []byte(`{"model":"m","prompt":"a dog","n":1}`))
	if err != nil {
		t.Fatalf("BuildUpstream: %v", err)
	}
	if url != "http://host:20128/v1/images/generations" {
		t.Errorf("URL = %q, want %q", url, "http://host:20128/v1/images/generations")
	}
	var m map[string]interface{}
	if err := jsonUnmarshal(body, &m); err != nil {
		t.Fatalf("body: %v", err)
	}
	if m["response_format"] != "b64_json" {
		t.Errorf("response_format = %v, want b64_json when omitted by client", m["response_format"])
	}
}

// --- BillableImages ---

func TestNineRouter_BillableImages(t *testing.T) {
	p := NewNineRouterProvider().(*nineRouterProvider)
	// Fully convertible upstream body (2 items)
	body := []byte(`{"data":[{"b64_json":"A"},{"b64_json":"B"}]}`)
	n, err := p.BillableImages(body)
	if err != nil {
		t.Fatalf("BillableImages: %v", err)
	}
	if n != 2 {
		t.Errorf("BillableImages = %d, want 2", n)
	}

	// Partially convertible upstream body (1 valid, 1 empty map): bills only 1
	partialBody := []byte(`{"data":[{"b64_json":"A"},{}]}`)
	nPartial, err := p.BillableImages(partialBody)
	if err != nil {
		t.Fatalf("BillableImages: %v", err)
	}
	if nPartial != 1 {
		t.Errorf("BillableImages = %d, want 1 for partial body", nPartial)
	}

	// Converted body with metadata.success_count: "1"
	convertedBody := []byte(`{"data":[{"image_urls":["data:..."]}],"metadata":{"success_count":"1","failed_count":"1"}}`)
	nConv, err := p.BillableImages(convertedBody)
	if err != nil {
		t.Fatalf("BillableImages: %v", err)
	}
	if nConv != 1 {
		t.Errorf("BillableImages = %d, want 1 for converted body", nConv)
	}
}

// --- RewriteResponse (conversion) ---

func TestNineRouter_RewriteResponse_JPEG(t *testing.T) {
	p := NewNineRouterProvider().(*nineRouterProvider)
	// JPEG magic bytes FF D8 FF
	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46}
	b64 := base64.StdEncoding.EncodeToString(jpeg)
	body := []byte(`{"created":5,"data":[{"b64_json":"` + b64 + `"}]}`)
	out, err := p.RewriteResponse(body)
	if err != nil {
		t.Fatalf("RewriteResponse: %v", err)
	}
	var m map[string]interface{}
	if err := jsonUnmarshal(out, &m); err != nil {
		t.Fatalf("out: %v", err)
	}
	data := m["data"].([]interface{})
	item := data[0].(map[string]interface{})
	urls := item["image_urls"].([]interface{})
	if len(urls) != 1 {
		t.Fatalf("image_urls len = %d, want 1", len(urls))
	}
	uri := urls[0].(string)
	if !strings.HasPrefix(uri, "data:image/jpeg;base64,") {
		t.Errorf("uri prefix = %q, want data:image/jpeg;base64,", uri[:30])
	}
	if m["created"].(float64) != 5 {
		t.Errorf("created = %v, want 5", m["created"])
	}
	md := m["metadata"].(map[string]interface{})
	if md["success_count"] != "1" {
		t.Errorf("success_count = %v, want 1", md["success_count"])
	}
	br := m["base_resp"].(map[string]interface{})
	if br["status_code"].(float64) != 0 {
		t.Errorf("base_resp.status_code = %v, want 0", br["status_code"])
	}
}

func TestNineRouter_RewriteResponse_PNG(t *testing.T) {
	p := NewNineRouterProvider().(*nineRouterProvider)
	png := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00}
	b64 := base64.StdEncoding.EncodeToString(png)
	body := []byte(`{"data":[{"b64_json":"` + b64 + `"}]}`)
	out, err := p.RewriteResponse(body)
	if err != nil {
		t.Fatalf("RewriteResponse: %v", err)
	}
	if !strings.Contains(string(out), "data:image/png;base64,") {
		t.Errorf("expected image/png data-URI, got: %s", out)
	}
}

func TestNineRouter_RewriteResponse_UnknownMagicFallback(t *testing.T) {
	p := NewNineRouterProvider().(*nineRouterProvider)
	// Unknown magic bytes — truthful fallback to application/octet-stream.
	unknown := []byte{0x00, 0x01, 0x02, 0x03, 0x04}
	b64 := base64.StdEncoding.EncodeToString(unknown)
	body := []byte(`{"data":[{"b64_json":"` + b64 + `"}]}`)
	out, err := p.RewriteResponse(body)
	if err != nil {
		t.Fatalf("RewriteResponse: %v", err)
	}
	if !strings.Contains(string(out), "data:application/octet-stream;base64,") {
		t.Errorf("expected fallback application/octet-stream data-URI, got: %s", out)
	}
}

func TestNineRouter_RewriteResponse_PartialConversionCounts(t *testing.T) {
	p := NewNineRouterProvider().(*nineRouterProvider)
	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46}
	b64 := base64.StdEncoding.EncodeToString(jpeg)
	// 2 items: 1 valid b64_json, 1 empty map (unconvertible)
	body := []byte(`{"data":[{"b64_json":"` + b64 + `"},{}]}`)
	out, err := p.RewriteResponse(body)
	if err != nil {
		t.Fatalf("RewriteResponse: %v", err)
	}
	var m map[string]interface{}
	if err := jsonUnmarshal(out, &m); err != nil {
		t.Fatalf("out: %v", err)
	}
	data := m["data"].([]interface{})
	if len(data) != 1 {
		t.Fatalf("len(data) = %d, want 1", len(data))
	}
	md := m["metadata"].(map[string]interface{})
	if md["success_count"] != "1" {
		t.Errorf("success_count = %v, want 1", md["success_count"])
	}
	if md["failed_count"] != "1" {
		t.Errorf("failed_count = %v, want 1", md["failed_count"])
	}
}

// --- Error message extraction ---

func TestNineRouter_ErrorMessage_Nested(t *testing.T) {
	inner := `{"error":{"code":429,"message":"You have exhausted your capacity on this model. Your quota will reset after 4h39m9s.","status":"RESOURCE_EXHAUSTED","details":[{"quotaResetTimeStamp":"2026-11-09T12:59:16Z"}]}}`
	outerMsg := "[antigravity/gemini-3.8-flash] [429]: " + inner
	body, _ := json.Marshal(map[string]interface{}{
		"error": map[string]interface{}{
			"message": outerMsg,
			"type":    "invalid_request_error",
		},
	})
	got := nineRouterErrorMessage(body)
	if got != "You have exhausted your capacity on this model. Your quota will reset after 4h39m9s." {
		t.Errorf("got %q, want the unwrapped quota message", got)
	}
}

func TestNineRouter_ErrorMessage_Plain(t *testing.T) {
	body := []byte(`{"error":{"message":"plain error"}}`)
	got := nineRouterErrorMessage(body)
	if got != "plain error" {
		t.Errorf("got %q, want %q", got, "plain error")
	}
}

func TestNineRouter_ErrorMessage_ParseFailure(t *testing.T) {
	body := []byte(`not json`)
	got := nineRouterErrorMessage(body)
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// --- NormalizeErrorBody ---

func TestNineRouter_NormalizeErrorBody(t *testing.T) {
	inner := `{"error":{"code":429,"message":"quota exhausted","status":"RESOURCE_EXHAUSTED"}}`
	outerMsg := "[antigravity/gemini-3.8-flash] [429]: " + inner
	body, _ := json.Marshal(map[string]interface{}{
		"error": map[string]interface{}{
			"message": outerMsg,
			"type":    "invalid_request_error",
		},
	})
	out := NewNineRouterProvider().(*nineRouterProvider).NormalizeErrorBody(body)
	var m map[string]interface{}
	if err := jsonUnmarshal(out, &m); err != nil {
		t.Fatalf("out: %v", err)
	}
	em := m["error"].(map[string]interface{})
	if em["message"] != "quota exhausted" {
		t.Errorf("message = %v, want %q", em["message"], "quota exhausted")
	}
	if em["type"] != "invalid_request_error" {
		t.Errorf("type = %v, want invalid_request_error", em["type"])
	}
}

func TestNineRouter_NormalizeErrorBody_VerbatimOnNoMessage(t *testing.T) {
	body := []byte(`{"error":{"code":500}}`)
	out := NewNineRouterProvider().(*nineRouterProvider).NormalizeErrorBody(body)
	if string(out) != string(body) {
		t.Errorf("expected verbatim, got %s", out)
	}
}

// --- Minimax byte-identity control: minimax has no hooks ---

func TestMinimax_HasNoResponseRewriter(t *testing.T) {
	var p UpstreamProvider = NewMiniMaxProvider()
	if _, ok := p.(ResponseRewriter); ok {
		t.Errorf("minimax must NOT implement ResponseRewriter (byte-identity)")
	}
	if _, ok := p.(ErrorBodyNormalizer); ok {
		t.Errorf("minimax must NOT implement ErrorBodyNormalizer (byte-identity)")
	}
}

// helper for tests (avoids import cycle with encoding/json naming)
func jsonUnmarshal(b []byte, v interface{}) error {
	return json.Unmarshal(b, v)
}
