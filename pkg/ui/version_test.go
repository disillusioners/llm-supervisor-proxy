package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHandleVersionServesSetVersionValue proves the version plumbing
// end to end: whatever main() passes to SetVersion is exactly what
// /fe/api/version serves and the UI badge renders. The handler reads
// only the package-level version var and touches no Server fields, so
// a zero-value Server is enough (no DB, no bus, no stores).
func TestHandleVersionServesSetVersionValue(t *testing.T) {
	orig := version
	t.Cleanup(func() { version = orig })

	SetVersion("151")
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/fe/api/version", nil)
	(&Server{}).handleVersion(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not a JSON object: %v (body: %q)", err, rr.Body.String())
	}
	if body["version"] != "151" {
		t.Fatalf("served version = %q, want %q", body["version"], "151")
	}
}
