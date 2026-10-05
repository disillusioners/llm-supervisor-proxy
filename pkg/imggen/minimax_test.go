package imggen

import (
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// TestMiniMax_InspectResponse_Success — base_resp.status_code == 0 ⇒
// PassAsIs (RelayStatus: 0). The upstream HTTP status is relayed
// verbatim by the handler (single-classifier seam, Amendment 3).
func TestMiniMax_InspectResponse_Success(t *testing.T) {
	p := NewMiniMaxProvider()
	body := []byte(`{"data":null,"metadata":{"success_count":"1","failed_count":"0"},"base_resp":{"status_code":0,"status_msg":"success"}}`)
	got := p.InspectResponse(200, body)
	if got.RelayStatus != 0 {
		t.Errorf("RelayStatus = %d, want 0 (PassAsIs)", got.RelayStatus)
	}
	if got.Reason != "" {
		t.Errorf("Reason = %q, want empty", got.Reason)
	}
}

// TestMiniMax_InspectResponse_ErrorIn200 — live-captured 2013
// shape (data:null, status_code:2013). 502 with body preserved
// verbatim.
func TestMiniMax_InspectResponse_ErrorIn200(t *testing.T) {
	p := NewMiniMaxProvider()
	body := []byte(`{"data":null,"base_resp":{"status_code":2013,"status_msg":"invalid params"}}`)
	got := p.InspectResponse(200, body)
	if got.RelayStatus != 502 {
		t.Errorf("RelayStatus = %d, want 502", got.RelayStatus)
	}
	if got.Reason == "" {
		t.Error("Reason = empty, want non-empty diagnostic")
	}
}

// TestMiniMax_InspectResponse_HTTP500 — upstream HTTP 500
// relayed verbatim (PassAsIs). The handler reads resp.StatusCode
// ONLY from the upstream response when the seam returned
// PassAsIs; the seam itself doesn't classify on the HTTP
// status (Amendment 3).
func TestMiniMax_InspectResponse_HTTP500(t *testing.T) {
	p := NewMiniMaxProvider()
	body := []byte(`{"data":null,"base_resp":{"status_code":0,"status_msg":"x"}}`)
	got := p.InspectResponse(500, body)
	if got.RelayStatus != 0 {
		t.Errorf("RelayStatus = %d, want 0 (PassAsIs — HTTP 500 relayed verbatim)", got.RelayStatus)
	}
}

// TestMiniMax_InspectResponse_HTTP400 — upstream HTTP 400 also
// PassAsIs (the body would carry the upstream's error shape).
func TestMiniMax_InspectResponse_HTTP400(t *testing.T) {
	p := NewMiniMaxProvider()
	body := []byte(`{"data":null,"base_resp":{"status_code":0,"status_msg":"x"}}`)
	got := p.InspectResponse(400, body)
	if got.RelayStatus != 0 {
		t.Errorf("RelayStatus = %d, want 0 (PassAsIs — HTTP 400 relayed verbatim)", got.RelayStatus)
	}
}

// TestMiniMax_InspectResponse_PartialSuccess — status_code==0 +
// failed_count>0 (the live partial-success shape) is a 200
// success (RelayStatus: 0). MiniMax-native semantics.
func TestMiniMax_InspectResponse_PartialSuccess(t *testing.T) {
	p := NewMiniMaxProvider()
	body := []byte(`{"data":null,"metadata":{"success_count":"1","failed_count":"1"},"base_resp":{"status_code":0,"status_msg":"partial"}}`)
	got := p.InspectResponse(200, body)
	if got.RelayStatus != 0 {
		t.Errorf("RelayStatus = %d, want 0 (partial success is a 200 success)", got.RelayStatus)
	}
}

// TestMiniMax_InspectResponse_MissingBaseResp — API-drift
// detection. Amendment 11: distinct log line ("missing base_resp
// key") for this case.
func TestMiniMax_InspectResponse_MissingBaseResp(t *testing.T) {
	p := NewMiniMaxProvider()
	body := []byte(`{"data":null}`)
	got := p.InspectResponse(200, body)
	if got.RelayStatus != 502 {
		t.Errorf("RelayStatus = %d, want 502", got.RelayStatus)
	}
	if got.Reason != "missing base_resp key" {
		t.Errorf("Reason = %q, want %q", got.Reason, "missing base_resp key")
	}
}

// TestMiniMax_InspectResponse_UnparseableJSON — HTML intermediary
// page, truncated stream, etc. 502 with the body preserved
// verbatim.
func TestMiniMax_InspectResponse_UnparseableJSON(t *testing.T) {
	p := NewMiniMaxProvider()
	body := []byte(`<html>upstream HTML page</html>`)
	got := p.InspectResponse(200, body)
	if got.RelayStatus != 502 {
		t.Errorf("RelayStatus = %d, want 502", got.RelayStatus)
	}
	if got.Reason != "unparseable JSON" {
		t.Errorf("Reason = %q, want %q", got.Reason, "unparseable JSON")
	}
}

// TestMiniMax_InspectResponse_EmptyBody — Amendment 11 distinct
// log line for the empty-body case.
func TestMiniMax_InspectResponse_EmptyBody(t *testing.T) {
	p := NewMiniMaxProvider()
	got := p.InspectResponse(200, nil)
	if got.RelayStatus != 502 {
		t.Errorf("RelayStatus = %d, want 502", got.RelayStatus)
	}
	if got.Reason != "empty body" {
		t.Errorf("Reason = %q, want %q", got.Reason, "empty body")
	}
}

// TestMiniMax_InspectResponse_StringStatusCode — tolerate the
// string form of base_resp.status_code (defensive — the live
// form is a JSON number; the string form is not observed but
// is harmless to support).
func TestMiniMax_InspectResponse_StringStatusCode(t *testing.T) {
	p := NewMiniMaxProvider()
	body := []byte(`{"base_resp":{"status_code":"0","status_msg":"x"}}`)
	got := p.InspectResponse(200, body)
	if got.RelayStatus != 0 {
		t.Errorf("RelayStatus = %d, want 0 (string \"0\" is success)", got.RelayStatus)
	}
}

// TestMiniMax_BillableImages — single-point string→int coercion
// for metadata.success_count. Live: "1" (string), 0 on
// missing.
func TestMiniMax_BillableImages(t *testing.T) {
	p := NewMiniMaxProvider()
	tests := []struct {
		name string
		body string
		want int
	}{
		{"string success_count", `{"metadata":{"success_count":"1"}}`, 1},
		{"string success_count 2", `{"metadata":{"success_count":"2"}}`, 2},
		{"numeric success_count", `{"metadata":{"success_count":3}}`, 3},
		{"missing metadata", `{"data":null}`, 0},
		{"missing success_count", `{"metadata":{"failed_count":"0"}}`, 0},
		{"empty body", ``, 0},
		{"unparseable JSON", `<html>`, 0},
		{"partial-success: success_count:1, failed_count:1", `{"metadata":{"success_count":"1","failed_count":"1"}}`, 1},
		{"error body (data:null, no metadata)", `{"data":null,"base_resp":{"status_code":2013}}`, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := p.BillableImages([]byte(tc.body))
			if got != tc.want {
				t.Errorf("BillableImages = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestMiniMax_BuildUpstream — URL = baseURL + /image_generation;
// body model = cred.InternalModel; all other body keys
// preserved.
func TestMiniMax_BuildUpstream(t *testing.T) {
	p := NewMiniMaxProvider()
	cred := models.ResolvedCredential{
		Provider:      "minimax",
		BaseURL:       "https://api.minimax.io/v1",
		InternalModel: "image-01",
		APIKey:        "sk-test",
	}
	body := []byte(`{"model":"minimax-image-01","prompt":"a cat","n":1,"style":"foo"}`)
	url, ub, err := p.BuildUpstream(cred, body)
	if err != nil {
		t.Fatalf("BuildUpstream error: %v", err)
	}
	if url != "https://api.minimax.io/v1/image_generation" {
		t.Errorf("upstreamURL = %q, want %q", url, "https://api.minimax.io/v1/image_generation")
	}
	// Verify body model rewrite + other keys preserved.
	ubStr := string(ub)
	if !contains(ubStr, `"model":"image-01"`) {
		t.Errorf("upstream body missing model rewrite: %s", ubStr)
	}
	if !contains(ubStr, `"prompt":"a cat"`) {
		t.Errorf("upstream body missing prompt: %s", ubStr)
	}
	if !contains(ubStr, `"n":1`) {
		t.Errorf("upstream body missing n: %s", ubStr)
	}
	if !contains(ubStr, `"style":"foo"`) {
		t.Errorf("upstream body missing style (CN param must pass through): %s", ubStr)
	}
	if contains(ubStr, `"model":"minimax-image-01"`) {
		t.Errorf("upstream body still carries client-facing model (should be rewritten): %s", ubStr)
	}
}

// TestMiniMax_BuildUpstream_TrailingSlash — TrimSuffix on the
// baseURL.
func TestMiniMax_BuildUpstream_TrailingSlash(t *testing.T) {
	p := NewMiniMaxProvider()
	cred := models.ResolvedCredential{
		Provider:      "minimax",
		BaseURL:       "https://api.minimax.io/v1/",
		InternalModel: "image-01",
	}
	_, ub, err := p.BuildUpstream(cred, []byte(`{"model":"client","prompt":"x"}`))
	if err != nil {
		t.Fatalf("BuildUpstream error: %v", err)
	}
	if !contains(string(ub), `"model":"image-01"`) {
		t.Errorf("upstream body should rewrite model to image-01: %s", string(ub))
	}
}

// TestMiniMax_BuildUpstream_InvalidJSON — invalid body surfaces
// as a build error.
func TestMiniMax_BuildUpstream_InvalidJSON(t *testing.T) {
	p := NewMiniMaxProvider()
	cred := models.ResolvedCredential{
		Provider:      "minimax",
		BaseURL:       "https://api.minimax.io/v1",
		InternalModel: "image-01",
	}
	_, _, err := p.BuildUpstream(cred, []byte(`not-json`))
	if err == nil {
		t.Error("BuildUpstream should error on invalid JSON")
	}
}

// TestRegistry_MiniMaxRegistered — package init() registers
// minimax.
func TestRegistry_MiniMaxRegistered(t *testing.T) {
	p, ok := lookup(MiniMaxProviderName)
	if !ok {
		t.Fatalf("registry missing %q", MiniMaxProviderName)
	}
	if p == nil {
		t.Fatal("registry returned nil for minimax")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
