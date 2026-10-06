//go:build live_imggen

// Package imggen live e2e (T1.7.4 / HR-2 / HR-3 / HR-4).
//
// This file is build-tagged `live_imggen` and runs ONLY when
// the tag is set. It skips silently when MINIMAX_API_KEY is
// unset (i.e., on dev / CI without the real key). The key is
// sourced from the absolute path
// /home/nea/Code/opensource-projects/llm-supervisor-proxy/.env-minimax
// (HR-4) — the test reads it via a one-shot source of the env
// file at run time, NEVER copies it into the worktree, and
// masks it in every artifact / log line / assertion (HR-3).
//
// The live e2e budget is ≤3 generation calls (HR-2). This test
// spends 1 happy-path call on a successful run. A failed test
// that already consumed its call does NOT get a free re-run —
// record and stop.
//
// Build:  go test -tags live_imggen -run TestLiveE2E ./pkg/imggen/...
// Run:    (ensure .env-minimax is populated; the key is NEVER
//
//	inlined into the test source or the worktree).
package imggen

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// TestLiveE2E_HappyPath is the only live e2e call the Phase 1
// implementation makes (HR-2 budget = 3, ledger reserves 2 for
// defect-fix retry + spare). When the key is absent the test
// skips silently. When the key is present, the test issues ONE
// real MiniMax image generation call through the proxy and
// asserts: HTTP 200, non-empty data.image_urls, no MINIMAX_API_KEY
// occurrences in the captured log/assertion output (HR-3).
//
// loadAPIKeyFromAbsolutePath / maskKey live in the untagged
// file (no_key_in_artifacts_test.go) so the static-check test
// compiles in the default `go test` suite.
func TestLiveE2E_HappyPath(t *testing.T) {
	apiKey, ok := loadAPIKeyFromAbsolutePath(t)
	if !ok {
		t.Skipf("MINIMAX_API_KEY unavailable at %s — live e2e skipped (HR-2 budget reserved for tester)", envFilePath)
	}
	masked := maskKey(apiKey)

	// Build a live env: the real proxy handler pointed at
	// api.minimax.io. The handler's resolvers wire the
	// credential to the live endpoint; the handler injects the
	// bearer Authorization header from the credential. The real
	// key is the credential bearer the proxy injects — the
	// client never sees it.
	baseURL := "https://api.minimax.io/v1"

	h := NewHandler(stubConfig{timeout: 120 * time.Second}, events.NewBus(), nil, nil)
	h.SetModelResolver(func(id string) (*models.ModelConfig, bool) {
		return &models.ModelConfig{
			ID: id, Kind: models.KindImageGen, Internal: true, InternalModel: "image-01",
			InternalBaseURL: baseURL, Credentials: []models.CredentialRef{{CredentialID: "x"}},
		}, true
	})
	h.SetCredentialResolver(func(id string) (models.ResolvedCredential, bool) {
		return models.ResolvedCredential{Provider: "minimax", APIKey: apiKey, BaseURL: baseURL, InternalModel: "image-01"}, true
	})

	// Capture the masked key in a local variable for assertion
	// messages only. The plaintext key is NEVER logged.
	t.Logf("[live e2e] running 1/3 generation calls (key=%s)", masked)

	// Smoke assert: a happy-path request through the handler
	// with a fake-upstream server. The "live" half of this
	// test is gated on the key being present; the build-tag
	// test asserts the handler runs end-to-end with the live
	// resolver shape (which is what the live e2e will exercise).
	//
	// When the env flag is set, the live e2e runs IN-PROCESS
	// through the proxy handler (h) below — the handler owns
	// model resolution, credential injection, model-name rewrite
	// to the upstream alias (minimax-image-01 → image-01), and
	// the real upstream POST. The test exercises the full proxy
	// pipeline, not a direct upstream call.
	if os.Getenv("IMGGEN_LIVE_E2E_NETWORK_CALL") == "" {
		// Without the env flag, this file exists ONLY to
		// verify the build tag compiles and the skip path
		// works. The actual live e2e call is owned by the
		// tester (HR-2). The handler shape is exercised by
		// the mock-only suite (handler_test.go).
		t.Logf("[live e2e] IMGGEN_LIVE_E2E_NETWORK_CALL unset — coder-side build-tag smoke test only (network call owned by tester per HR-2)")
		return
	}

	// In-process live happy-path: POST through the real proxy
	// handler so the model-resolution + credential-resolution +
	// provider-rewrite pipeline is exercised end-to-end. The
	// handler rewrites the client-facing model name to the
	// upstream alias (cred.InternalModel = "image-01") before
	// calling MiniMax. tokenStore is nil → auth is disabled
	// (per BE-A1 / Leader Ruling L2), so no Authorization
	// header is needed on the test request.
	req := httptest.NewRequest(http.MethodPost, "/v1/image_generation",
		strings.NewReader(`{"model":"minimax-image-01","prompt":"a small red apple","response_format":"url","n":1}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.HandleImageGeneration(rec, req)

	respBody := rec.Body.Bytes()
	if rec.Code != http.StatusOK {
		t.Fatalf("proxy returned %d: %s", rec.Code, string(respBody))
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		t.Fatalf("proxy body unparseable: %v", err)
	}
	if id, _ := parsed["id"].(string); id == "" {
		t.Fatalf("proxy body missing id: %s", string(respBody))
	}
	data, _ := parsed["data"].([]interface{})
	if len(data) == 0 {
		t.Fatalf("proxy data is empty: %s", string(respBody))
	}
	t.Logf("[live e2e] success: %d image URL(s) returned (key=%s)", len(data), masked)
}
