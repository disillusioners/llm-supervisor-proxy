package main

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/identity"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/ui"
)

// newDormantHarness builds the REAL cmd wiring path with OAUTH2_ENABLED
// unset — the same construction sequence main() runs, with the DB-backed
// managers swapped for their in-memory twins (MockConfigManager /
// NewModelsConfig / nil DB token store; the D1 surface never touches the
// DB) and the guard nil (D1 dormant). It returns the mux (for route-set
// assertions) and the composed chain (exactly what http.Server would run).
func newDormantHarness(t *testing.T) (*http.ServeMux, http.Handler) {
	t.Helper()
	os.Unsetenv("OAUTH2_ENABLED")
	guard, err := buildOAuth2FromEnv()
	if err != nil {
		t.Fatalf("buildOAuth2FromEnv: %v", err)
	}
	if guard != nil {
		t.Fatal("expected dormant guard (nil) with OAUTH2_ENABLED unset")
	}

	cfgMgr := database.NewMockConfigManager()
	modelsConfig := models.NewModelsConfig()
	proxyConfig := &proxy.Config{ConfigMgr: cfgMgr, ModelsConfig: modelsConfig}
	bus := events.NewBus()
	reqStore := store.NewRequestStore(100)
	tokenStore := auth.NewTokenStore(nil, database.SQLite) // DB unused by the D1 surface
	proxyHandler := proxy.NewHandler(proxyConfig, bus, reqStore, nil, tokenStore, nil, nil, nil)
	uiServer := ui.NewServer(bus, cfgMgr, proxyConfig, modelsConfig, reqStore, nil, tokenStore, nil)

	// cmd/main.go identity wiring + route registration, verbatim shape:
	// dormant ⇒ identityProvider stays nil and NO /oauth2/* route is
	// registered (D1).
	var identityProvider identity.Provider
	if guard != nil {
		identityProvider = guard
	}
	uiServer.SetIdentityProvider(identityProvider)
	mux := http.NewServeMux()
	if guard != nil {
		guard.RegisterRoutes(mux)
	}
	uiServer.RegisterHandlers(mux)
	mux.HandleFunc("/v1/models", proxyHandler.HandleModels)
	mux.HandleFunc("/healthz", handleHealthz)
	return mux, buildHandlerChain(mux, guard)
}

func TestMain_OAuth2DisabledByDefault(t *testing.T) {
	os.Unsetenv("OAUTH2_ENABLED")
	guard, err := buildOAuth2FromEnv()
	if err != nil {
		t.Fatalf("buildOAuth2FromEnv: %v", err)
	}
	if guard != nil {
		t.Fatal("expected dormant guard")
	}
	mux := http.NewServeMux()
	if guard != nil {
		guard.RegisterRoutes(mux)
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/oauth2/start", nil)
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestMain_OAuth2EnabledRequiresConfig(t *testing.T) {
	t.Setenv("OAUTH2_ENABLED", "true")
	_, err := buildOAuth2FromEnv()
	if err == nil {
		t.Fatal("expected error for misconfigured enabled deployment")
	}
}

// TestMain_DormantAuthMeIs200 (plan §3 D1, B4) — the dormant auth/me probe
// answers 200 with the constant body, no 500/panic. Extended (review W4)
// to pin the EXACT bytes so the S11 hoisted constant cannot drift from the
// legacy map re-marshal output.
func TestMain_DormantAuthMeIs200(t *testing.T) {
	_, chain := newDormantHarness(t)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/fe/api/auth/me", nil)
	chain.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}
	// Semantic shape (plan D1 wording).
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["enabled"] != false {
		t.Fatalf("enabled = %v, want false", body["enabled"])
	}
	if body["authenticated"] != false {
		t.Fatalf("authenticated = %v, want false", body["authenticated"])
	}
	if body["user"] != nil {
		t.Fatalf("user = %v, want null", body["user"])
	}
	if body["state_store_count"] != float64(0) {
		t.Fatalf("state_store_count = %v, want 0", body["state_store_count"])
	}
	// Byte-exact (S11): no per-request re-marshal drift, sorted-key order,
	// trailing newline — the bytes the pre-hoist encoder produced.
	if got := rr.Body.String(); got != ui.DormantAuthMeBody {
		t.Fatalf("body = %q, want byte-identical %q", got, ui.DormantAuthMeBody)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	if cc := rr.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", cc)
	}
}

// TestMain_DormantD1Surface (plan §3 D1, extended per review W4) — with
// OAUTH2_ENABLED unset, EVERY request on EVERY probed route behaves exactly
// as pre-feature: /oauth2/* unregistered, /fe/api/* NOT gated, /v1/models
// public, /healthz alive.
func TestMain_DormantD1Surface(t *testing.T) {
	_, chain := newDormantHarness(t)

	probe := func(target string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		rr := httptest.NewRecorder()
		chain.ServeHTTP(rr, req)
		return rr
	}

	// 1. No /oauth2/* route is registered in dormant mode.
	for _, p := range []string{"/oauth2/start", "/oauth2/callback", "/oauth2/logout"} {
		rr := probe(p)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("GET %s: status = %d, want 404 (D1: no /oauth2/* route registered dormant)", p, rr.Code)
		}
	}

	// 2. /fe/api/auth/me — the ONLY dormant-mode addition.
	rr := probe("/fe/api/auth/me")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /fe/api/auth/me: status = %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}
	if got := rr.Body.String(); got != ui.DormantAuthMeBody {
		t.Fatalf("GET /fe/api/auth/me: body = %q, want byte-identical %q", got, ui.DormantAuthMeBody)
	}

	// 3. /fe/api/models reachable with NO credentials — the /fe/api/*
	// surface is NOT gated in dormant mode. This is the invariant an
	// over-eager gate (e.g. a blanket 401 on /fe/api/*) would break.
	rr = probe("/fe/api/models")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /fe/api/models with no credentials: status = %d, want 200 (D1: /fe/api/* ungated dormant; body: %s)", rr.Code, rr.Body.String())
	}
	var list []json.RawMessage
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatalf("GET /fe/api/models: body is not a JSON list: %v (%q)", err, rr.Body.String())
	}

	// 4. /v1/models public by design — 200 without any auth material.
	rr = probe("/v1/models")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /v1/models: status = %d, want 200 (public by design; body: %s)", rr.Code, rr.Body.String())
	}
	var v1 struct {
		Object string            `json:"object"`
		Data   []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &v1); err != nil {
		t.Fatalf("GET /v1/models: body is not a JSON object: %v (%q)", err, rr.Body.String())
	}
	if v1.Object != "list" {
		t.Fatalf("GET /v1/models: object = %q, want %q", v1.Object, "list")
	}

	// 5. /healthz alive.
	rr = probe("/healthz")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /healthz: status = %d, want 200", rr.Code)
	}
	if got := rr.Body.String(); got != "ok" {
		t.Fatalf("GET /healthz: body = %q, want %q", got, "ok")
	}
}

// TestMain_DormantChainHasNoOAuth2Layer (review W4 — chain identity).
//
// WHAT THIS ASSERTION PROVES:
//
// The dormant handler chain contains NO OAuth2 layer. The proof is a
// discrimination pair over the two request classes the enabled gate alters:
//
//   - `/fe/api/models` — the enabled gate answers 401 BEFORE the mux for an
//     unauthenticated caller (pkg/oauth2 Middleware.Wrap class 5).
//   - `/` — the enabled gate answers 302 to /oauth2/start?rd=… (class 6).
//
// The same requests are served by two chains built through the SAME
// construction path main() uses (buildHandlerChain) over the SAME harness
// mux: guard == nil (dormant) vs a real guard built by buildOAuth2FromEnv
// from a fully-populated env (enabled). The dormant chain serves the
// sentinel routes' OWN responses (200 with the handler's body; the mux's
// own 307 → /ui/ on `/`) while the enabled chain answers 401/302 without
// reaching those handlers. If any OAuth2 layer were present in the dormant
// chain — a Wrap call, a fallback shim, an accidentally-shared middleware —
// those same probes would be intercepted and the dormant-side assertions
// would fail with 401/302.
//
// The construction-path half: buildHandlerChain is the SINGLE site where
// http.Server's Handler is composed in cmd/main.go (`Handler:
// buildHandlerChain(mux, oauth2Guard)`, with oauth2Guard nil exactly when
// buildOAuth2FromEnv returned nil), so what this test proves about
// buildHandlerChain(mux, nil) is what the running server executes in
// dormant mode. A second subtest proves the restructured chain still
// carries the gzip response layer (the S9 refactor's blast radius).
func TestMain_DormantChainHasNoOAuth2Layer(t *testing.T) {
	mux, dormantChain := newDormantHarness(t)

	// The REAL enabled guard, from the production env→config→guard path.
	t.Setenv("OAUTH2_ENABLED", "true")
	t.Setenv("OAUTH2_ISSUER_URL", "https://dormant-chain-probe.invalid")
	t.Setenv("OAUTH2_CLIENT_ID", "dormant-chain-probe")
	t.Setenv("OAUTH2_EXTERNAL_URL", "https://dormant-chain-probe.invalid")
	t.Setenv("OAUTH2_EMAIL_DOMAINS", "example.invalid")
	t.Setenv("OAUTH2_COOKIE_SECRET", "dormant-chain-probe-cookie-secret-0123456789abcdef")
	enabledGuard, err := buildOAuth2FromEnv()
	if err != nil {
		t.Fatalf("buildOAuth2FromEnv (enabled): %v", err)
	}
	if enabledGuard == nil || !enabledGuard.Enabled() {
		t.Fatalf("expected a live enabled guard, got %v", enabledGuard)
	}
	// The discriminator chain: same harness mux, same routes, only the
	// chain differs (the /oauth2/* routes stay unregistered — the 302
	// Location is asserted, never followed).
	enabledChain := buildHandlerChain(mux, enabledGuard)

	t.Run("fe api class gated 401 vs dormant pass", func(t *testing.T) {
		// Dormant: reaches the real /fe/api/models handler.
		rr := httptest.NewRecorder()
		dormantChain.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/fe/api/models", nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("dormant chain GET /fe/api/models: status = %d, want 200 (handler's own response — no OAuth2 layer may intercept)", rr.Code)
		}
		// Discriminator: the identical request through the enabled chain is
		// answered 401 by the gate — proving this probe is of a class the
		// gate WOULD intercept.
		rr2 := httptest.NewRecorder()
		enabledChain.ServeHTTP(rr2, httptest.NewRequest(http.MethodGet, "/fe/api/models", nil))
		if rr2.Code != http.StatusUnauthorized {
			t.Fatalf("enabled chain GET /fe/api/models: status = %d, want 401 (discriminator must prove the gate intercepts this class)", rr2.Code)
		}
	})

	t.Run("ui root class gated 302 vs dormant pass", func(t *testing.T) {
		// Dormant: the mux's OWN "/" handler runs (307 → /ui/), not a gate
		// redirect.
		rr := httptest.NewRecorder()
		dormantChain.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
		if rr.Code != http.StatusTemporaryRedirect {
			t.Fatalf("dormant chain GET /: status = %d, want 307 (mux's own redirect — no OAuth2 layer may intercept)", rr.Code)
		}
		if loc := rr.Header().Get("Location"); loc != "/ui/" {
			t.Fatalf("dormant chain GET /: Location = %q, want /ui/ (mux's own target, not /oauth2/start)", loc)
		}
		// Discriminator: the identical request through the enabled chain is
		// answered 302 → /oauth2/start?rd=… by the gate.
		rr2 := httptest.NewRecorder()
		enabledChain.ServeHTTP(rr2, httptest.NewRequest(http.MethodGet, "/", nil))
		if rr2.Code != http.StatusFound {
			t.Fatalf("enabled chain GET /: status = %d, want 302 (discriminator must prove the gate redirects this class)", rr2.Code)
		}
		if loc := rr2.Header().Get("Location"); !strings.HasPrefix(loc, "/oauth2/start?rd=") {
			t.Fatalf("enabled chain GET /: Location = %q, want prefix /oauth2/start?rd=", loc)
		}
	})

	t.Run("dormant chain still compresses fe api responses", func(t *testing.T) {
		// The S9 restructure must not drop the gzip response layer: an
		// /fe/api/* response above the 1 KiB threshold with Accept-Encoding:
		// gzip must arrive gzipped through the dormant chain.
		const body = `{"sentinel":` + `"` + "x" + `"}`
		big := strings.Repeat(body, 1024) // well above MinResponseSize (1024)
		probeMux := http.NewServeMux()
		probeMux.HandleFunc("/fe/api/models", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(big))
		})
		chain := buildHandlerChain(probeMux, nil)
		req := httptest.NewRequest(http.MethodGet, "/fe/api/models", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rr := httptest.NewRecorder()
		chain.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
		if enc := rr.Header().Get("Content-Encoding"); enc != "gzip" {
			t.Fatalf("dormant chain dropped the gzip response layer: Content-Encoding = %q, want gzip", enc)
		}
		zr, err := gzip.NewReader(rr.Body)
		if err != nil {
			t.Fatalf("gzip.NewReader: %v", err)
		}
		defer zr.Close()
		got, err := io.ReadAll(zr)
		if err != nil {
			t.Fatalf("gzip body read: %v", err)
		}
		if string(got) != big {
			t.Fatalf("decompressed body length = %d, want %d", len(got), len(big))
		}
	})
}
