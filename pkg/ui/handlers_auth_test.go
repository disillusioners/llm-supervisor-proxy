package ui

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/identity"
)

// fakeIdentityProvider is a test stub implementing identity.Provider.
// It proves pkg/ui does not import pkg/oauth2.
type fakeIdentityProvider struct {
	enabled  bool
	identity *identity.Identity
	count    int
	err      error
}

func (f *fakeIdentityProvider) Enabled() bool { return f.enabled }

func (f *fakeIdentityProvider) Current(r *http.Request) (*identity.Identity, error) {
	return f.identity, f.err
}

func (f *fakeIdentityProvider) StateStoreCount() int { return f.count }

// TestHandleAuthMe_DormantBodyByteStable pins the hoisted dormant body
// (review S11, internal-oauth2 round 1): the package-level constant must
// stay byte-identical to what the legacy per-request map re-marshal
// produced — same fields, same sorted-key order, same trailing newline —
// and the handler must emit exactly those bytes in one write.
func TestHandleAuthMe_DormantBodyByteStable(t *testing.T) {
	const want = `{"authenticated":false,"enabled":false,"state_store_count":0,"user":null}` + "\n"

	// 1. The hoisted constant itself.
	if got := string(dormantAuthMeBody); got != want {
		t.Fatalf("dormantAuthMeBody = %q, want %q", got, want)
	}

	// 2. The literal must still be what the legacy encoding path produced:
	// a fresh json.Encoder writing the equivalent map must emit identical
	// bytes (this is the drift lock — if anyone changes the map shape or
	// the constant independently, one of these comparisons fails).
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(map[string]any{
		"enabled": false, "authenticated": false, "user": nil, "state_store_count": 0,
	}); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != want {
		t.Fatalf("legacy map re-marshal = %q, want %q (constant drifted from the encoder output)", got, want)
	}

	// 3. The live handler writes exactly those bytes.
	s := &Server{}
	mux := http.NewServeMux()
	mux.HandleFunc("/fe/api/auth/me", s.handleAuthMe)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/fe/api/auth/me")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(body); got != want {
		t.Fatalf("handler body = %q, want %q", got, want)
	}
}

func TestHandleAuthMe_Dormant(t *testing.T) {
	s := &Server{}
	mux := http.NewServeMux()
	mux.HandleFunc("/fe/api/auth/me", s.handleAuthMe)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/fe/api/auth/me")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	if body["enabled"] != false {
		t.Fatalf("enabled = %v", body["enabled"])
	}
	if body["authenticated"] != false {
		t.Fatalf("authenticated = %v", body["authenticated"])
	}
	if body["state_store_count"] != float64(0) {
		t.Fatalf("state_store_count = %v", body["state_store_count"])
	}
}

func TestHandleAuthMe_EnabledAuthenticated(t *testing.T) {
	s := &Server{}
	s.SetIdentityProvider(&fakeIdentityProvider{
		enabled:  true,
		identity: &identity.Identity{Sub: "sub1", Email: "a@b.com", Name: "Alice"},
		count:    7,
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/fe/api/auth/me", s.handleAuthMe)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/fe/api/auth/me")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	if body["enabled"] != true {
		t.Fatalf("enabled = %v", body["enabled"])
	}
	if body["authenticated"] != true {
		t.Fatalf("authenticated = %v", body["authenticated"])
	}
	u := body["user"].(map[string]any)
	if u["sub"] != "sub1" || u["email"] != "a@b.com" || u["name"] != "Alice" {
		t.Fatalf("user = %v", u)
	}
	if _, ok := u["preferred_username"]; ok {
		t.Fatal("preferred_username must not be present")
	}
	if body["state_store_count"] != float64(7) {
		t.Fatalf("state_store_count = %v", body["state_store_count"])
	}
}

func TestHandleAuthMe_EnabledAnonymous(t *testing.T) {
	s := &Server{}
	s.SetIdentityProvider(&fakeIdentityProvider{
		enabled:  true,
		identity: nil,
		count:    3,
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/fe/api/auth/me", s.handleAuthMe)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/fe/api/auth/me")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	if body["enabled"] != true {
		t.Fatalf("enabled = %v", body["enabled"])
	}
	if body["authenticated"] != false {
		t.Fatalf("authenticated = %v", body["authenticated"])
	}
	if body["state_store_count"] != float64(3) {
		t.Fatalf("state_store_count = %v", body["state_store_count"])
	}
}

func TestHandleAuthMe_MethodNotAllowed(t *testing.T) {
	s := &Server{}
	mux := http.NewServeMux()
	mux.HandleFunc("/fe/api/auth/me", s.handleAuthMe)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/fe/api/auth/me", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}

func TestRegisterHandlers_AuthMeRouteExists(t *testing.T) {
	s := &Server{}
	mux := http.NewServeMux()
	s.RegisterHandlers(mux)
	// invoke via mux
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/fe/api/auth/me", nil)
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
}

func TestHandleAuthMe_StateStoreCount(t *testing.T) {
	s := &Server{}
	s.SetIdentityProvider(&fakeIdentityProvider{enabled: true, count: 42})
	mux := http.NewServeMux()
	mux.HandleFunc("/fe/api/auth/me", s.handleAuthMe)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/fe/api/auth/me")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	if body["state_store_count"] != float64(42) {
		t.Fatalf("state_store_count = %v", body["state_store_count"])
	}
}
