package oauth2

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMiddleware_GatingTable(t *testing.T) {
	cfg := &Config{Enabled: true, CookieName: "sess", CookieSecure: true, ExternalURL: "https://app.example.com"}
	m := &Middleware{cfg: cfg, cookie: &CookieCodec{name: "sess", key: make([]byte, 32), secure: true, sameSite: http.SameSiteLaxMode, path: "/"}}
	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	cases := []struct {
		path   string
		method string
		code   int
	}{
		{"/healthz", http.MethodGet, 200},
		{"/v1/models", http.MethodGet, 200},
		{"/oauth2/start", http.MethodGet, 200},
		{"/fe/api/auth/me", http.MethodGet, 200},
		{"/fe/api/version", http.MethodGet, 200},
		{"/fe/api/version", http.MethodPost, 401},
		{"/fe/api/config", http.MethodGet, 401},
		{"/ui/", http.MethodGet, 302},
		{"/", http.MethodGet, 302},
		{"/some/unknown", http.MethodGet, 302},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != c.code {
			t.Fatalf("%s %s: %d (want %d)", c.method, c.path, w.Code, c.code)
		}
	}
}

// censusRow is one registered mux route plus the §5.7 classification the
// gate must apply to it.
//
// route — the EXACT registered mux pattern (the census identity). The MCP
// proxy patterns carry a {id} wildcard; probe holds the concrete path handed
// to the gate because a real request never contains braces.
//
// method — the method probed. The gate's classification is method-independent
// for classes 1, 2, 3, 5 and 6; only class 4b (/fe/api/version) is
// method-sensitive and it is probed with BOTH methods.
//
// probe — the concrete path handed to the gate.
//
// class — the §5.7 classification (1, 2, 3, 4, 4b, 5, 6).
//
// want — the EXACT status an unauthenticated request must receive.
//
// src — the registration site (file:line) the route is registered at.
//
// note — optional annotation (conditional registration, derived pattern).
type censusRow struct {
	route  string
	method string
	probe  string
	class  string
	want   int
	src    string
	note   string
}

// censusKey is the membership key of a route inside the pass allowlist /
// the gated set.
func censusKey(route, method string) string { return route + " " + method }

// censusClassIsPass reports whether a §5.7 class ADMITS the request
// (classes 1, 2, 3, 4 and 4b are the explicit pass allowlist).
func censusClassIsPass(class string) bool {
	switch class {
	case "1", "2", "3", "4", "4b":
		return true
	}
	return false
}

// censusClassStatus is the EXACT status an unauthenticated request must
// receive for a §5.7 class (plan §5.7 classification table, §6.2 rows).
func censusClassStatus(class string) int {
	switch class {
	case "1", "2", "3", "4", "4b":
		return http.StatusOK // the gate admits the request
	case "5":
		return http.StatusUnauthorized // /fe/api/* → 401 JSON
	case "6":
		return http.StatusFound // browser path → 302 → /oauth2/start
	}
	return http.StatusUnauthorized // class 7 — fail-closed
}

// TestMiddleware_RouteCensus is the ANTI-DRIFT gate of plan §6.2 / BLK-4:
// it enumerates the FULL mux registration set of the production binary and
// asserts, for EVERY entry, that the route is classified exactly as §5.7
// requires — an explicit pass-admission or an explicit rejection.
//
// The census is NOT a "a response exists" smoke test: every row pins the
// EXACT status the gate must answer with, so a registered route that leaks
// into the pass set (public) instead of being gated FAILS the test.
//
// pkg/oauth2 may NOT import pkg/ui, pkg/mcp, cmd or pkg/proxy (the plan's
// import guard, §9.4), so the table below is hard-coded WITH the
// registration source line of every entry — an independent gate lane greps
// the real registrations and compares them against this table.
func TestMiddleware_RouteCensus(t *testing.T) {
	cfg := &Config{Enabled: true, CookieName: "sess", CookieSecure: true, ExternalURL: "https://app.example.com"}
	m := &Middleware{cfg: cfg, cookie: &CookieCodec{name: "sess", key: make([]byte, 32), secure: true, sameSite: http.SameSiteLaxMode, path: "/"}}
	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))

	// ── The census: the full registered-route set ───────────────────────────
	// pkg/ui/server.go RegisterHandlers, pkg/mcp/mcp.go RegisterProxyHandlers
	// + RegisterAPIHandlers, cmd/main.go, pkg/oauth2/handler.go RegisterRoutes.
	rows := []censusRow{
		// ── pkg/ui/server.go ──────────────────────────────────────────────
		{route: "/ui/", method: http.MethodGet, probe: "/ui/", class: "6", want: http.StatusFound, src: "pkg/ui/server.go:181"},
		{route: "/ui", method: http.MethodGet, probe: "/ui", class: "6", want: http.StatusFound, src: "pkg/ui/server.go:181", note: "DERIVED: the /ui/ subtree pattern also serves the exact /ui after the mux trailing-slash redirect (plan §6.2)"},
		{route: "/", method: http.MethodGet, probe: "/", class: "6", want: http.StatusFound, src: "pkg/ui/server.go:201"},
		{route: "/fe/api/version", method: http.MethodGet, probe: "/fe/api/version", class: "4b", want: http.StatusOK, src: "pkg/ui/server.go:210", note: "class 4b is GET-ONLY (the house deploy gate G4 probes this path)"},
		{route: "/fe/api/version", method: http.MethodPost, probe: "/fe/api/version", class: "5", want: http.StatusUnauthorized, src: "pkg/ui/server.go:210", note: "a NON-GET /fe/api/version is class 5 → 401"},
		{route: "/fe/api/auth/me", method: http.MethodGet, probe: "/fe/api/auth/me", class: "4", want: http.StatusOK, src: "pkg/ui/server.go:213", note: "class 4 EXEMPT — the gate admits it; the handler itself answers 401 when enabled+anonymous"},
		{route: "/fe/api/ram", method: http.MethodGet, probe: "/fe/api/ram", class: "5", want: http.StatusUnauthorized, src: "pkg/ui/server.go:214"},
		{route: "/fe/api/config", method: http.MethodGet, probe: "/fe/api/config", class: "5", want: http.StatusUnauthorized, src: "pkg/ui/server.go:215"},
		{route: "/fe/api/models", method: http.MethodGet, probe: "/fe/api/models", class: "5", want: http.StatusUnauthorized, src: "pkg/ui/server.go:216"},
		{route: "/fe/api/models/", method: http.MethodGet, probe: "/fe/api/models/", class: "5", want: http.StatusUnauthorized, src: "pkg/ui/server.go:217"},
		{route: "/fe/api/models/validate", method: http.MethodGet, probe: "/fe/api/models/validate", class: "5", want: http.StatusUnauthorized, src: "pkg/ui/server.go:218"},
		{route: "/fe/api/models/test", method: http.MethodGet, probe: "/fe/api/models/test", class: "5", want: http.StatusUnauthorized, src: "pkg/ui/server.go:219"},
		{route: "/fe/api/decisions/test", method: http.MethodGet, probe: "/fe/api/decisions/test", class: "5", want: http.StatusUnauthorized, src: "pkg/ui/server.go:225"},
		{route: "/fe/api/image_generation/test", method: http.MethodGet, probe: "/fe/api/image_generation/test", class: "5", want: http.StatusUnauthorized, src: "pkg/ui/server.go:231"},
		{route: "/fe/api/events", method: http.MethodGet, probe: "/fe/api/events", class: "5", want: http.StatusUnauthorized, src: "pkg/ui/server.go:232", note: "SSE — the gate answers BEFORE the handler sets its headers"},
		{route: "/fe/api/requests", method: http.MethodGet, probe: "/fe/api/requests", class: "5", want: http.StatusUnauthorized, src: "pkg/ui/server.go:233"},
		{route: "/fe/api/requests/", method: http.MethodGet, probe: "/fe/api/requests/", class: "5", want: http.StatusUnauthorized, src: "pkg/ui/server.go:243"},
		{route: "/fe/api/app-tags", method: http.MethodGet, probe: "/fe/api/app-tags", class: "5", want: http.StatusUnauthorized, src: "pkg/ui/server.go:244"},
		{route: "/fe/api/buffers/", method: http.MethodGet, probe: "/fe/api/buffers/", class: "5", want: http.StatusUnauthorized, src: "pkg/ui/server.go:245"},
		{route: "/fe/api/tokens", method: http.MethodGet, probe: "/fe/api/tokens", class: "5", want: http.StatusUnauthorized, src: "pkg/ui/server.go:247"},
		{route: "/fe/api/tokens/", method: http.MethodGet, probe: "/fe/api/tokens/", class: "5", want: http.StatusUnauthorized, src: "pkg/ui/server.go:248"},
		{route: "/fe/api/credentials", method: http.MethodGet, probe: "/fe/api/credentials", class: "5", want: http.StatusUnauthorized, src: "pkg/ui/server.go:250"},
		{route: "/fe/api/credentials/", method: http.MethodGet, probe: "/fe/api/credentials/", class: "5", want: http.StatusUnauthorized, src: "pkg/ui/server.go:251"},
		{route: "/fe/api/credential-groups", method: http.MethodGet, probe: "/fe/api/credential-groups", class: "5", want: http.StatusUnauthorized, src: "pkg/ui/server.go:258"},
		{route: "/fe/api/credential-groups/", method: http.MethodGet, probe: "/fe/api/credential-groups/", class: "5", want: http.StatusUnauthorized, src: "pkg/ui/server.go:259"},
		{route: "/fe/api/providers", method: http.MethodGet, probe: "/fe/api/providers", class: "5", want: http.StatusUnauthorized, src: "pkg/ui/server.go:261"},
		{route: "/fe/api/usage", method: http.MethodGet, probe: "/fe/api/usage", class: "5", want: http.StatusUnauthorized, src: "pkg/ui/server.go:263"},
		{route: "/fe/api/usage/tokens", method: http.MethodGet, probe: "/fe/api/usage/tokens", class: "5", want: http.StatusUnauthorized, src: "pkg/ui/server.go:264"},
		{route: "/fe/api/usage/summary", method: http.MethodGet, probe: "/fe/api/usage/summary", class: "5", want: http.StatusUnauthorized, src: "pkg/ui/server.go:265"},
		{route: "/fe/api/usage/models", method: http.MethodGet, probe: "/fe/api/usage/models", class: "5", want: http.StatusUnauthorized, src: "pkg/ui/server.go:266"},

		// ── pkg/mcp/mcp.go — RegisterProxyHandlers ────────────────────────
		// CONDITIONAL: registered only when pkg/mcp/mcp.go:33 tokenStore != nil.
		// The census asserts the CLASSIFICATION for the registered case; the
		// unregistered case is NOT asserted here (a route that does not exist
		// cannot be probed for a 401 — plan §6.2).
		{route: "/v1/mcp/{id}/sse", method: http.MethodGet, probe: "/v1/mcp/probe-server-id/sse", class: "2", want: http.StatusOK, src: "pkg/mcp/mcp.go:40", note: "CONDITIONAL (pkg/mcp/mcp.go:33)"},
		{route: "/v1/mcp/{id}/messages", method: http.MethodGet, probe: "/v1/mcp/probe-server-id/messages", class: "2", want: http.StatusOK, src: "pkg/mcp/mcp.go:41", note: "CONDITIONAL (pkg/mcp/mcp.go:33)"},
		{route: "/v1/mcp/{id}/", method: http.MethodGet, probe: "/v1/mcp/probe-server-id/", class: "2", want: http.StatusOK, src: "pkg/mcp/mcp.go:44", note: "CONDITIONAL (pkg/mcp/mcp.go:33)"},
		{route: "/v1/mcp/{id}", method: http.MethodGet, probe: "/v1/mcp/probe-server-id", class: "2", want: http.StatusOK, src: "pkg/mcp/mcp.go:45", note: "CONDITIONAL (pkg/mcp/mcp.go:33)"},

		// ── pkg/mcp/mcp.go — RegisterAPIHandlers ──────────────────────────
		// CONDITIONAL: registered only when MCP_ENABLED=true (cmd/main.go:422)
		// AND pkg/mcp/mcp.go:50 store != nil. Otherwise the path is 404, not
		// 401 — the census therefore pins the classification for the
		// REGISTERED case only (plan §6.2).
		{route: "/fe/api/mcp-servers/status", method: http.MethodGet, probe: "/fe/api/mcp-servers/status", class: "5", want: http.StatusUnauthorized, src: "pkg/mcp/mcp.go:55", note: "CONDITIONAL (MCP_ENABLED=true + pkg/mcp/mcp.go:50)"},
		{route: "/fe/api/mcp-servers/test-connection", method: http.MethodGet, probe: "/fe/api/mcp-servers/test-connection", class: "5", want: http.StatusUnauthorized, src: "pkg/mcp/mcp.go:59", note: "CONDITIONAL (MCP_ENABLED=true + pkg/mcp/mcp.go:50)"},
		{route: "/fe/api/mcp-servers/", method: http.MethodGet, probe: "/fe/api/mcp-servers/", class: "5", want: http.StatusUnauthorized, src: "pkg/mcp/mcp.go:62", note: "CONDITIONAL (MCP_ENABLED=true + pkg/mcp/mcp.go:50)"},

		// ── cmd/main.go ──────────────────────────────────────────────────
		{route: "/v1/chat/completions", method: http.MethodGet, probe: "/v1/chat/completions", class: "2", want: http.StatusOK, src: "cmd/main.go:451"},
		{route: "/v1/messages", method: http.MethodGet, probe: "/v1/messages", class: "2", want: http.StatusOK, src: "cmd/main.go:452"},
		{route: "/v1/models", method: http.MethodGet, probe: "/v1/models", class: "2", want: http.StatusOK, src: "cmd/main.go:453"},
		{route: "/v1/image_generation", method: http.MethodGet, probe: "/v1/image_generation", class: "2", want: http.StatusOK, src: "cmd/main.go:460"},
		{route: "/v1/decisions", method: http.MethodGet, probe: "/v1/decisions", class: "2", want: http.StatusOK, src: "cmd/main.go:469"},
		{route: "/healthz", method: http.MethodGet, probe: "/healthz", class: "1", want: http.StatusOK, src: "cmd/main.go:472"},

		// ── pkg/oauth2/handler.go — RegisterRoutes ───────────────────────
		// Registered only when the internal OAuth2 guard exists (enabled mode).
		{route: "/oauth2/start", method: http.MethodGet, probe: "/oauth2/start", class: "3", want: http.StatusOK, src: "pkg/oauth2/handler.go:208"},
		{route: "/oauth2/callback", method: http.MethodGet, probe: "/oauth2/callback", class: "3", want: http.StatusOK, src: "pkg/oauth2/handler.go:209"},
		{route: "/oauth2/logout", method: http.MethodPost, probe: "/oauth2/logout", class: "3", want: http.StatusOK, src: "pkg/oauth2/handler.go:210", note: "POST-only handler — GET answers 405 (pkg/oauth2/handler.go:477)"},
	}

	// ── The gate's SPEC, authored from §5.7 — INDEPENDENT of the census ───
	// The pass allowlist (classes 1, 2, 3, 4, 4b) and the gated set
	// (classes 5, 6) are written from the §5.7 classification table, NOT
	// derived from the census rows above — that is what gives the coverage
	// check below its teeth.
	passAllowlist := map[string]bool{
		// class 1 — health probe
		"/healthz GET": true,
		// class 2 — the client API keeps its own Bearer auth
		"/v1/chat/completions GET":  true,
		"/v1/messages GET":          true,
		"/v1/models GET":            true,
		"/v1/image_generation GET":  true,
		"/v1/decisions GET":         true,
		"/v1/mcp/{id}/sse GET":      true,
		"/v1/mcp/{id}/messages GET": true,
		"/v1/mcp/{id}/ GET":         true,
		"/v1/mcp/{id} GET":          true,
		// class 3 — the flow itself
		"/oauth2/start GET":    true,
		"/oauth2/callback GET": true,
		"/oauth2/logout POST":  true,
		// class 4 — the identity probe (must answer when unauthenticated)
		"/fe/api/auth/me GET": true,
		// class 4b — GET /fe/api/version (the house deploy gate G4 probes it)
		"/fe/api/version GET": true,
	}
	gatedSet := map[string]bool{
		// class 6 — browser paths → 302
		"/ GET":    true,
		"/ui GET":  true,
		"/ui/ GET": true,
		// class 5 — /fe/api/* → 401 JSON
		"/fe/api/version POST":                    true,
		"/fe/api/ram GET":                         true,
		"/fe/api/config GET":                      true,
		"/fe/api/models GET":                      true,
		"/fe/api/models/ GET":                     true,
		"/fe/api/models/validate GET":             true,
		"/fe/api/models/test GET":                 true,
		"/fe/api/decisions/test GET":              true,
		"/fe/api/image_generation/test GET":       true,
		"/fe/api/events GET":                      true,
		"/fe/api/requests GET":                    true,
		"/fe/api/requests/ GET":                   true,
		"/fe/api/app-tags GET":                    true,
		"/fe/api/buffers/ GET":                    true,
		"/fe/api/tokens GET":                      true,
		"/fe/api/tokens/ GET":                     true,
		"/fe/api/credentials GET":                 true,
		"/fe/api/credentials/ GET":                true,
		"/fe/api/credential-groups GET":           true,
		"/fe/api/credential-groups/ GET":          true,
		"/fe/api/providers GET":                   true,
		"/fe/api/usage GET":                       true,
		"/fe/api/usage/tokens GET":                true,
		"/fe/api/usage/summary GET":               true,
		"/fe/api/usage/models GET":                true,
		"/fe/api/mcp-servers/status GET":          true,
		"/fe/api/mcp-servers/test-connection GET": true,
		"/fe/api/mcp-servers/ GET":                true,
	}

	// A row silently removed from the census is a route silently outside it
	// (BLK-4) — the count pins the table's size.
	const wantRowCount = 46
	if len(rows) != wantRowCount {
		t.Fatalf("census has %d rows, want %d — a route may not silently leave the census (BLK-4)", len(rows), wantRowCount)
	}

	for _, row := range rows {
		key := censusKey(row.route, row.method)
		inPass := passAllowlist[key]
		inGated := gatedSet[key]
		switch {
		case inPass && inGated:
			t.Fatalf("%s: route is in BOTH the pass allowlist and the gated set", key)
		case !inPass && !inGated:
			t.Errorf("BLK-4: registered route %s (%s) appears in NEITHER the pass allowlist nor the gated set", key, row.src)
		}
		if censusClassIsPass(row.class) && !inPass {
			t.Errorf("%s: class %s is a pass class but the route is NOT in the pass allowlist", key, row.class)
		}
		if !censusClassIsPass(row.class) && !inGated {
			t.Errorf("%s: class %s is a gated class but the route is NOT in the gated set", key, row.class)
		}
		if inGated && row.want == http.StatusOK {
			t.Errorf("%s: the route is in the GATED set but the expected status is 200 (a pass status) — a pass route misclassified as gated would leak publicly", key)
		}
		if inPass && row.want != http.StatusOK {
			t.Errorf("%s: the route is in the PASS allowlist but the expected status is %d (not 200)", key, row.want)
		}
		if want := censusClassStatus(row.class); row.want != want {
			t.Errorf("%s: want %d contradicts class %s (expect %d)", key, row.want, row.class, want)
		}

		req := httptest.NewRequest(row.method, row.probe, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != row.want {
			t.Errorf("%s (%s): status %d, want %d — the route is classified wrong", key, row.src, w.Code, row.want)
			continue
		}
		if row.want == http.StatusUnauthorized {
			if ct := w.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("%s: 401 Content-Type %q, want application/json", key, ct)
			}
		}
		if row.class == "6" {
			if loc := w.Header().Get("Location"); !strings.HasPrefix(loc, "/oauth2/start?rd=") {
				t.Errorf("%s: 302 Location %q, want prefix /oauth2/start?rd=", key, loc)
			}
		}
	}

	// §5.7 class 7 (fail-closed): an UNREGISTERED path must never pass
	// through as 200. Browser-shaped ⇒ 302, API-shaped ⇒ 401 JSON.
	req := httptest.NewRequest(http.MethodGet, "/totally/unknown/xyz", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code == http.StatusOK {
		t.Fatal("unknown path passed through (fail-open)")
	}
	if w.Code != http.StatusFound {
		t.Errorf("unknown browser-shaped path: status %d, want 302", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/totally/unknown/xyz", nil)
	req.Header.Set("Accept", "application/json")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("unknown API-shaped path (Accept: application/json): status %d, want 401", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("unknown API-shaped path: 401 Content-Type %q, want application/json", ct)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/unknown/xyz", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("unknown /api/ path: status %d, want 401", w.Code)
	}
}

func TestMiddleware_UncleanPathCannotBypassGate(t *testing.T) {
	cfg := &Config{Enabled: true, CookieName: "sess", CookieSecure: true, ExternalURL: "https://app.example.com"}
	m := &Middleware{cfg: cfg, cookie: &CookieCodec{name: "sess", key: make([]byte, 32), secure: true, sameSite: http.SameSiteLaxMode, path: "/"}}
	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	req := httptest.NewRequest(http.MethodGet, "/x/../fe/api/config", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("code: %d", w.Code)
	}
	// //fe/api/config case
	req2 := httptest.NewRequest(http.MethodGet, "//fe/api/config", nil)
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("code2: %d", w2.Code)
	}
}

func TestMiddleware_API401IsJSON(t *testing.T) {
	cfg := &Config{Enabled: true, CookieName: "sess", CookieSecure: true, ExternalURL: "https://app.example.com"}
	m := &Middleware{cfg: cfg, cookie: &CookieCodec{name: "sess", key: make([]byte, 32), secure: true, sameSite: http.SameSiteLaxMode, path: "/"}}
	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	req := httptest.NewRequest(http.MethodGet, "/fe/api/config", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("code: %d", w.Code)
	}
	if w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("ct: %s", w.Header().Get("Content-Type"))
	}
}

func TestMiddleware_BrowserIs302(t *testing.T) {
	cfg := &Config{Enabled: true, CookieName: "sess", CookieSecure: true, ExternalURL: "https://app.example.com"}
	m := &Middleware{cfg: cfg, cookie: &CookieCodec{name: "sess", key: make([]byte, 32), secure: true, sameSite: http.SameSiteLaxMode, path: "/"}}
	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	req := httptest.NewRequest(http.MethodGet, "/ui/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("code: %d", w.Code)
	}
}

func TestMiddleware_V1IsUntouched(t *testing.T) {
	cfg := &Config{Enabled: true, CookieName: "sess", CookieSecure: true, ExternalURL: "https://app.example.com"}
	m := &Middleware{cfg: cfg, cookie: &CookieCodec{name: "sess", key: make([]byte, 32), secure: true, sameSite: http.SameSiteLaxMode, path: "/"}}
	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("code: %d", w.Code)
	}
}

func TestMiddleware_HealthzIsUntouched(t *testing.T) {
	cfg := &Config{Enabled: true, CookieName: "sess", CookieSecure: true, ExternalURL: "https://app.example.com"}
	m := &Middleware{cfg: cfg, cookie: &CookieCodec{name: "sess", key: make([]byte, 32), secure: true, sameSite: http.SameSiteLaxMode, path: "/"}}
	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("code: %d", w.Code)
	}
}

func TestMiddleware_AuthMeIsExempt(t *testing.T) {
	cfg := &Config{Enabled: true, CookieName: "sess", CookieSecure: true, ExternalURL: "https://app.example.com"}
	m := &Middleware{cfg: cfg, cookie: &CookieCodec{name: "sess", key: make([]byte, 32), secure: true, sameSite: http.SameSiteLaxMode, path: "/"}}
	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	req := httptest.NewRequest(http.MethodGet, "/fe/api/auth/me", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("code: %d", w.Code)
	}
}

func TestMiddleware_VersionIsExempt(t *testing.T) {
	cfg := &Config{Enabled: true, CookieName: "sess", CookieSecure: true, ExternalURL: "https://app.example.com"}
	m := &Middleware{cfg: cfg, cookie: &CookieCodec{name: "sess", key: make([]byte, 32), secure: true, sameSite: http.SameSiteLaxMode, path: "/"}}
	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	req := httptest.NewRequest(http.MethodGet, "/fe/api/version", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("code: %d", w.Code)
	}
}

func TestMiddleware_UnknownPathIsGated(t *testing.T) {
	cfg := &Config{Enabled: true, CookieName: "sess", CookieSecure: true, ExternalURL: "https://app.example.com"}
	m := &Middleware{cfg: cfg, cookie: &CookieCodec{name: "sess", key: make([]byte, 32), secure: true, sameSite: http.SameSiteLaxMode, path: "/"}}
	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	req := httptest.NewRequest(http.MethodGet, "/some/unknown/path", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusFound && w.Code != http.StatusUnauthorized {
		t.Fatalf("code: %d", w.Code)
	}
}

func TestMiddleware_AuthenticatedPassesWithIdentity(t *testing.T) {
	key := make([]byte, 32)
	cfg := &Config{Enabled: true, CookieName: "sess", CookieSecure: true, ExternalURL: "https://app.example.com"}
	c := &CookieCodec{name: "sess", key: key, secure: true, sameSite: http.SameSiteLaxMode, path: "/"}
	m := &Middleware{cfg: cfg, cookie: c}
	s := &Session{Sub: "u", ExpiresAt: futureUnix()}
	v, _ := c.Seal(s)
	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := IdentityFromContext(r.Context())
		if !ok || id.Sub != "u" {
			t.Fatal("no identity")
		}
		w.WriteHeader(200)
	}))
	req := httptest.NewRequest(http.MethodGet, "/fe/api/config", nil)
	req.AddCookie(&http.Cookie{Name: "sess", Value: v})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("code: %d", w.Code)
	}
}

func TestMiddleware_InvalidCookieIsAnonymous(t *testing.T) {
	cfg := &Config{Enabled: true, CookieName: "sess", CookieSecure: true, ExternalURL: "https://app.example.com"}
	m := &Middleware{cfg: cfg, cookie: &CookieCodec{name: "sess", key: make([]byte, 32), secure: true, sameSite: http.SameSiteLaxMode, path: "/"}}
	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	req := httptest.NewRequest(http.MethodGet, "/fe/api/config", nil)
	req.AddCookie(&http.Cookie{Name: "sess", Value: "garbage"})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("code: %d", w.Code)
	}
}

func futureUnix() int64 { return time.Now().Unix() + 3600 }
