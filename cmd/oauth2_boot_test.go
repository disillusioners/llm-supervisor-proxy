package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/identity"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/ui"
)

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

func TestMain_DormantAuthMeIs200(t *testing.T) {
	os.Unsetenv("OAUTH2_ENABLED")
	guard, err := buildOAuth2FromEnv()
	if err != nil {
		t.Fatalf("buildOAuth2FromEnv: %v", err)
	}
	var identityProvider identity.Provider
	if guard != nil {
		identityProvider = guard
	}
	s := &ui.Server{}
	s.SetIdentityProvider(identityProvider)
	mux := http.NewServeMux()
	if guard != nil {
		guard.RegisterRoutes(mux)
	}
	s.RegisterHandlers(mux)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/fe/api/auth/me", nil)
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["enabled"] != false {
		t.Fatalf("enabled = %v, want false", body["enabled"])
	}
}
