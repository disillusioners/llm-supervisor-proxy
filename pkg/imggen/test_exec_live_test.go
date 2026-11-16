//go:build live_imggen

// Live e2e for the image-gen TEST path — the 9router leg.
//
// ONE real image generation THROUGH THE FIXED TEST PATH (ProbeBody →
// ExecuteTest → the 9router adapter → the real upstream), verifying
// the operator-visible contract end-to-end: HTTP 200, the ok code,
// and the b64_json image delivered as a data-URI.
//
// TRIPLE-GATED so suites stay hermetic (default-skip):
//  1. the `live_imggen` build tag (same exclusion as live_e2e_test.go
//     — `go test ./...` never compiles this file),
//  2. IMGGEN_LIVE_E2E_NETWORK_CALL set in the environment (the same
//     flag live_e2e_test.go gates on — the coder-side run without it
//     is a build/skip smoke only),
//  3. the credential present: env NINEROUTER_API_KEY, or the stored
//     credential file /tmp/9router.key (mode 600, the 2026-11-09
//     verification gate's location). The key is read at run time,
//     NEVER copied into the worktree, and masked in every log line.
//
// Report discipline: HTTP status, latency, and the response KIND are
// logged; the raw upstream body is NEVER printed (on success it is
// half a megabyte of base64; on failure it may embed provider
// internals). The upstream URL is the EXTERNAL verify endpoint
// https://9router.ensem.dev/v1 (override: NINEROUTER_BASE_URL); the
// internal model is ag/gemini-3.8-flash (override: NINEROUTER_MODEL).
//
// Build: go test -tags live_imggen -run TestLiveE2E_NineRouterProbe ./pkg/imggen/
//
//	Run:   IMGGEN_LIVE_E2E_NETWORK_CALL=1 go test -tags live_imggen \
//	       -run TestLiveE2E_NineRouterProbe ./pkg/imggen/ -v
package imggen

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// nineRouterKeyPath is the stored 9router credential (the 2026-11-09
// verification gate's key file, mode 600). Read-only, never echoed.
const nineRouterKeyPath = "/tmp/9router.key"

// loadNineRouterKey sources the credential: the env first, then the
// stored key file. Returns ok=false when neither exists (the caller
// SKIPS — a missing credential must never fail a hermetic run).
func loadNineRouterKey(t *testing.T) (string, bool) {
	t.Helper()
	if k := strings.TrimSpace(os.Getenv("NINEROUTER_API_KEY")); k != "" {
		return k, true
	}
	raw, err := os.ReadFile(nineRouterKeyPath)
	if err != nil {
		return "", false
	}
	k := strings.TrimSpace(string(raw))
	if k == "" {
		return "", false
	}
	return k, true
}

// TestLiveE2E_NineRouterProbe spends ONE real billable generation
// through the fixed test path and asserts the classified success:
// status 200, the ok code, ≥1 image, the image delivered as a
// data-URI (the b64_json→data-URI conversion the FE's green panel
// counts). A quota 429 surfaces as status 429 + the cooldown code —
// the retry budget is NOT automatic inside this test (HR-2 style:
// one call per run; the runner decides on a cooldown retry).
func TestLiveE2E_NineRouterProbe(t *testing.T) {
	if os.Getenv("IMGGEN_LIVE_E2E_NETWORK_CALL") == "" {
		t.Skip("IMGGEN_LIVE_E2E_NETWORK_CALL unset — live 9router probe skipped (hermetic default)")
	}
	apiKey, ok := loadNineRouterKey(t)
	if !ok {
		t.Skipf("no 9router credential (env NINEROUTER_API_KEY / %s) — live probe skipped", nineRouterKeyPath)
	}
	masked := maskKey(apiKey)

	baseURL := os.Getenv("NINEROUTER_BASE_URL")
	if baseURL == "" {
		baseURL = "https://9router.ensem.dev/v1"
	}
	internalModel := os.Getenv("NINEROUTER_MODEL")
	if internalModel == "" {
		internalModel = "ag/gemini-3.8-flash"
	}

	cred := models.ResolvedCredential{
		Provider:      NineRouterProviderName,
		APIKey:        apiKey,
		BaseURL:       baseURL,
		InternalModel: internalModel,
		CredentialID:  "live-e2e-9router",
	}
	// The FIXED test path: the adapter-owned canned body for a
	// 9router row, run through the SAME seam the FE button drives.
	rawBody := ProbeBody(NineRouterProviderName, "live-e2e-row")

	start := time.Now()
	res := ExecuteTest(cred, rawBody, 120*time.Second)
	latency := time.Since(start)

	// Defensive leak guard: the classified error is upstream-derived;
	// it must never carry the credential.
	if strings.Contains(res.Error, apiKey) || strings.Contains(res.Reason, apiKey) {
		t.Fatalf("credential leaked into the classified result (key=%s) — refusing to continue", masked)
	}

	t.Logf("[live 9router probe] url=%s/images/generations model=%s key=%s", baseURL, internalModel, masked)
	t.Logf("[live 9router probe] status=%d upstream_status=%d code=%q relay_status=%d images=%d latency=%s",
		res.Status, res.UpstreamStatus, res.Code, res.RelayStatus, res.ImageCount, latency)
	if res.Error != "" {
		t.Logf("[live 9router probe] error=%q reason=%q", res.Error, res.Reason)
	}

	if res.Status != 200 {
		t.Fatalf("probe classified %d (code=%q error=%q) — want 200", res.Status, res.Code, res.Error)
	}
	if res.Code != CodeOK {
		t.Fatalf("code = %q, want %q", res.Code, CodeOK)
	}
	if res.ImageCount < 1 || len(res.ImageURLs) == 0 {
		t.Fatalf("image_count = %d, want ≥1", res.ImageCount)
	}
	if !strings.HasPrefix(res.ImageURLs[0], "data:image/") {
		t.Fatalf("first image = %.64q..., want a data:image/ URI (the b64_json conversion)", res.ImageURLs[0])
	}
}
