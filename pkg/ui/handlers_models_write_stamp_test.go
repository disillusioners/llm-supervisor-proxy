package ui

// remove-external-upstream P5 / D2 — FE-API write-stamp pin.
//
// The store-level stamp is already pinned at the modelscache level by
// commit 1ccbbfa (CachedModelsConfig.AddModel/UpdateModel assert the
// GetModel read-back shows Internal==true after an Internal-omitted
// fixture). THIS test adds the FE-API level: a POST /fe/api/models
// whose JSON payload omits the `internal` key entirely (it decodes as
// false — the shape any non-P3 client, curl, or API consumer would
// send) must be ACCEPTED with HTTP 201 and the persisted row must
// READ BACK internal:true through GET /fe/api/models.
//
// The stamp that fires is the DB ModelsManager's own write-stamp
// (pkg/store/database AddModel: model.Internal = true BEFORE
// validateBeforeWrite — "task 7, layer ii/iii"); production wraps this
// same manager in the modelscache decorator ("layer i/iii"), so the
// guarantee holds on both wirings. The POST's own response body echoes
// the wire DTO (unstamped) — the read-back is the stamped truth, which
// is exactly what this test asserts.
//
// Wiring follows the house pattern (handlers_models_put_probe_test.go):
// the real handleModels chain against a real SQLite-backed
// ModelsManager with seeded credentials.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

func TestHandleModels_POST_InternalOmitted_StampReadsBackTrue(t *testing.T) {
	ts := newRealPUTTestServer(t)
	defer ts.cleanup()

	payload := map[string]interface{}{
		"id":      "p5-d2-stamp",
		"name":    "P5 D2 Write-Stamp",
		"enabled": true,
		"kind":    models.KindChat,
		// NOTE: "internal" is deliberately ABSENT from this payload —
		// the whole point of D2. The write-stamp must supply it.
		"internal_model": "stamp-test-model",
		"credentials": []map[string]interface{}{
			{"credential_id": "openai-cred", "weight": 1, "position": 0},
		},
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	resp, err := http.Post(ts.server.URL+"/fe/api/models", "application/json", bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("POST /fe/api/models: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		var out bytes.Buffer
		out.ReadFrom(resp.Body)
		t.Fatalf("POST status = %d, want %d (body: %s)", resp.StatusCode, http.StatusCreated, out.String())
	}

	_, got := doGet(t, ts.server, "p5-d2-stamp")
	if got == nil {
		t.Fatal("GET /fe/api/models does not list the created model p5-d2-stamp")
	}
	internal, ok := got["internal"].(bool)
	if !ok || !internal {
		t.Errorf("read-back internal = %v (%T), want true — the internal=true write-stamp did not surface at the FE-API boundary", got["internal"], got["internal"])
	}
}
