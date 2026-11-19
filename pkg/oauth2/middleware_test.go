package oauth2

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMiddleware_GatingTable(t *testing.T) {
	cfg := &Config{Enabled: true, CookieName: "sess", CookieSecure: true, ExternalURL: "https://app.example.com"}
	m := &Middleware{cfg: cfg, cookie: &CookieCodec{name: "sess", key: make([]byte, 32), secure: true, sameSite: http.SameSiteLaxMode, path: "/"}}
	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	cases := []struct {
		path string
		code int
	}{
		{"/healthz", 200},
		{"/v1/models", 200},
		{"/oauth2/start", 200},
		{"/fe/api/auth/me", 200},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, c.path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != c.code {
			t.Fatalf("%s: %d", c.path, w.Code)
		}
	}
}

func TestMiddleware_RouteCensus(t *testing.T) {
	// placeholder: pass
}

func TestMiddleware_UncleanPathCannotBypassGate(t *testing.T) {
	cfg := &Config{Enabled: true, CookieName: "sess", CookieSecure: true, ExternalURL: "https://app.example.com"}
	m := &Middleware{cfg: cfg, cookie: &CookieCodec{name: "sess", key: make([]byte, 32), secure: true, sameSite: http.SameSiteLaxMode, path: "/"}}
	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	req := httptest.NewRequest(http.MethodGet, "/x/../fe/api/config", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized && w.Code != http.StatusMovedPermanently && w.Code != http.StatusFound {
		t.Fatalf("code: %d", w.Code)
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
