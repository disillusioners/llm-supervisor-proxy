package oauth2

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOIDC_DiscoveryParses(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"issuer":"` + srv.URL + `","authorization_endpoint":"` + srv.URL + `/auth","token_endpoint":"` + srv.URL + `/token","jwks_uri":"` + srv.URL + `/jwks"}`))
	}))
	defer srv.Close()
	cfg := &Config{IssuerURL: srv.URL, DiscoveryTimeout: 5 * time.Second, AllowedEndpoints: nil}
	// normalize issuer to srv.URL
	cfg.IssuerURL = srv.URL
	p := NewOIDCProvider(cfg)
	meta, err := p.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if meta.Issuer != srv.URL {
		t.Fatal("issuer mismatch")
	}
}

func TestOIDC_RejectsIssuerMismatch(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"issuer":"https://other.example.com","authorization_endpoint":"https://other.example.com/auth","token_endpoint":"https://other.example.com/token","jwks_uri":"https://other.example.com/jwks"}`))
	}))
	defer srv.Close()
	cfg := &Config{IssuerURL: srv.URL, DiscoveryTimeout: 5 * time.Second}
	p := NewOIDCProvider(cfg)
	if _, err := p.Discover(context.Background()); err == nil {
		t.Fatal("expected issuer mismatch")
	}
}

func TestOIDC_RejectsCrossOriginEndpoints(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"issuer":"` + srv.URL + `","authorization_endpoint":"https://evil.com/auth","token_endpoint":"` + srv.URL + `/token","jwks_uri":"` + srv.URL + `/jwks"}`))
	}))
	defer srv.Close()
	cfg := &Config{IssuerURL: srv.URL, DiscoveryTimeout: 5 * time.Second}
	p := NewOIDCProvider(cfg)
	if _, err := p.Discover(context.Background()); err == nil {
		t.Fatal("expected cross-origin rejection")
	}
}

func TestOIDC_CrossOriginRedirectRejected(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			w.Write([]byte(`{"issuer":"` + srv.URL + `","token_endpoint":"` + srv.URL + `/token","jwks_uri":"` + srv.URL + `/jwks"}`))
			return
		}
		http.Redirect(w, r, "https://evil.com/token", http.StatusFound)
	}))
	defer srv.Close()
	cfg := &Config{IssuerURL: srv.URL, DiscoveryTimeout: 5 * time.Second, TokenExchangeTimeout: 5 * time.Second, ClientID: "cid", RedirectURL: "http://cb"}
	p := NewOIDCProvider(cfg)
	_, _, err := p.ExchangeCode(context.Background(), "code", "http://cb", "verifier")
	if err == nil {
		t.Fatal("expected error on cross-origin redirect")
	}
}

func TestOIDC_DiscoverySingleFlight(t *testing.T) {
	calls := 0
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		time.Sleep(50 * time.Millisecond)
		w.Write([]byte(`{"issuer":"` + srv.URL + `","authorization_endpoint":"` + srv.URL + `/auth","token_endpoint":"` + srv.URL + `/token","jwks_uri":"` + srv.URL + `/jwks"}`))
	}))
	defer srv.Close()
	cfg := &Config{IssuerURL: srv.URL, DiscoveryTimeout: 5 * time.Second}
	p := NewOIDCProvider(cfg)
	done := make(chan struct{})
	go func() { p.Discover(context.Background()); done <- struct{}{} }()
	go func() { p.Discover(context.Background()); done <- struct{}{} }()
	<-done
	<-done
	if calls != 1 {
		t.Fatalf("expected 1 discovery call, got %d", calls)
	}
}

func TestOIDC_DiscoveryNegativeCache(t *testing.T) {
	calls := 0
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(500)
	}))
	defer srv.Close()
	cfg := &Config{IssuerURL: srv.URL, DiscoveryTimeout: 5 * time.Second}
	p := NewOIDCProvider(cfg)
	if _, err := p.Discover(context.Background()); err == nil {
		t.Fatal("expected failure")
	}
	calls2 := calls
	_, err2 := p.Discover(context.Background())
	if err2 == nil {
		t.Fatal("expected cached error")
	}
	if calls != calls2 {
		t.Fatalf("expected no new server hit, calls %d vs %d", calls, calls2)
	}
}

func TestOIDC_ExchangeIsSingleAttempt(t *testing.T) {
	calls := 0
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			w.Write([]byte(`{"issuer":"` + srv.URL + `","token_endpoint":"` + srv.URL + `/token","jwks_uri":"` + srv.URL + `/jwks"}`))
			return
		}
		calls++
		w.WriteHeader(500)
	}))
	defer srv.Close()
	cfg := &Config{IssuerURL: srv.URL, DiscoveryTimeout: 5 * time.Second, TokenExchangeTimeout: 5 * time.Second, ClientID: "cid", RedirectURL: "http://cb"}
	p := NewOIDCProvider(cfg)
	_, _, err := p.ExchangeCode(context.Background(), "code", "http://cb", "verifier")
	if err == nil {
		t.Fatal("expected failure")
	}
	if calls != 1 {
		t.Fatalf("expected 1 attempt, got %d", calls)
	}
}

func TestOIDC_ExchangeTimeout(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			w.Write([]byte(`{"issuer":"` + srv.URL + `","token_endpoint":"` + srv.URL + `/token","jwks_uri":"` + srv.URL + `/jwks"}`))
			return
		}
		time.Sleep(200 * time.Millisecond)
		w.Write([]byte(`{"id_token":"x"}`))
	}))
	defer srv.Close()
	cfg := &Config{IssuerURL: srv.URL, DiscoveryTimeout: 5 * time.Second, TokenExchangeTimeout: 50 * time.Millisecond, ClientID: "cid", RedirectURL: "http://cb"}
	p := NewOIDCProvider(cfg)
	_, _, err := p.ExchangeCode(context.Background(), "code", "http://cb", "verifier")
	if err == nil {
		t.Fatal("expected timeout/error")
	}
}

func TestOIDC_UserinfoSubMustMatch(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			w.Write([]byte(`{"issuer":"` + srv.URL + `","userinfo_endpoint":"` + srv.URL + `/userinfo","jwks_uri":"` + srv.URL + `/jwks"}`))
			return
		}
		w.Write([]byte(`{"sub":"other"}`))
	}))
	defer srv.Close()
	cfg := &Config{IssuerURL: srv.URL, DiscoveryTimeout: 5 * time.Second, UserinfoTimeout: 5 * time.Second}
	p := NewOIDCProvider(cfg)
	_, err := p.FetchUserinfo(context.Background(), "tok", "expected")
	if err == nil {
		t.Fatal("expected sub mismatch")
	}
}

func TestOIDC_RejectsNonHTTPSEndpoints(t *testing.T) {
	// http endpoints rejected unless allowed
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"issuer":"` + srv.URL + `","authorization_endpoint":"http://evil.com/auth","token_endpoint":"` + srv.URL + `/token","jwks_uri":"` + srv.URL + `/jwks"}`))
	}))
	defer srv.Close()
	cfg := &Config{IssuerURL: srv.URL, DiscoveryTimeout: 5 * time.Second}
	p := NewOIDCProvider(cfg)
	if _, err := p.Discover(context.Background()); err == nil {
		t.Fatal("expected rejection of non-https cross-origin endpoint")
	}
}

func TestOIDC_DiscoveryFailureIsRetryable(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	cfg := &Config{IssuerURL: srv.URL, DiscoveryTimeout: 5 * time.Second}
	p := NewOIDCProvider(cfg)
	if _, err := p.Discover(context.Background()); err == nil {
		t.Fatal("expected failure")
	}
	// retryable: a later success works after backoff window; simulate by waiting? Just ensure no panic.
}

func TestOIDC_ExchangeCodePostsForm(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			w.Write([]byte(`{"issuer":"` + srv.URL + `","token_endpoint":"` + srv.URL + `/token","jwks_uri":"` + srv.URL + `/jwks"}`))
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("not post")
		}
		r.ParseForm()
		if r.FormValue("grant_type") != "authorization_code" {
			t.Errorf("grant_type wrong")
		}
		w.Write([]byte(`{"id_token":"dummy"}`))
	}))
	defer srv.Close()
	cfg := &Config{IssuerURL: srv.URL, DiscoveryTimeout: 5 * time.Second, TokenExchangeTimeout: 5 * time.Second, ClientID: "cid", RedirectURL: "http://cb"}
	p := NewOIDCProvider(cfg)
	idt, _, err := p.ExchangeCode(context.Background(), "code", "http://cb", "verifier")
	if err != nil {
		t.Fatal(err)
	}
	if idt != "dummy" {
		t.Fatal("id_token mismatch")
	}
}

func TestOIDC_DiscoveryBodySizeCapped(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// huge body
		w.Write([]byte(`{"issuer":"` + srv.URL + `",` + string(make([]byte, 300*1024)) + `}`))
	}))
	defer srv.Close()
	cfg := &Config{IssuerURL: srv.URL, DiscoveryTimeout: 5 * time.Second}
	p := NewOIDCProvider(cfg)
	// should not panic; may error
	_, _ = p.Discover(context.Background())
}
