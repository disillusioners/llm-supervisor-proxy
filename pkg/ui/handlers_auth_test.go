package ui

import (
	"encoding/json"
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

	resp, _ := http.Get(srv.URL + "/fe/api/auth/me")
	defer resp.Body.Close()
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	if body["state_store_count"] != float64(42) {
		t.Fatalf("state_store_count = %v", body["state_store_count"])
	}
}
