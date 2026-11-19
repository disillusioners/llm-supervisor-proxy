package oauth2

import (
	"context"
	"crypto"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/identity"
)

type Middleware struct {
	cookie *CookieCodec
	cfg    *Config
}

func (m *Middleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := path.Clean(r.URL.Path)
		// pass classes
		if p == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(p, "/v1/") {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(p, "/oauth2/") {
			next.ServeHTTP(w, r)
			return
		}
		if p == "/fe/api/auth/me" {
			next.ServeHTTP(w, r)
			return
		}
		if p == "/fe/api/version" && r.Method == http.MethodGet {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(p, "/fe/api/") {
			if id, ok := m.isAuthenticated(r); ok {
				ctx := WithIdentity(r.Context(), id)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			write401(w)
			return
		}
		if p == "/ui" || strings.HasPrefix(p, "/ui/") || p == "/" {
			if id, ok := m.isAuthenticated(r); ok {
				ctx := WithIdentity(r.Context(), id)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			rd := r.URL.Path
			if r.URL.RawQuery != "" {
				rd = rd + "?" + r.URL.RawQuery
			}
			loc := "/oauth2/start?rd=" + urlQueryEscape(rd)
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, loc, http.StatusFound)
			return
		}
		// class 7: gated
		// authenticated?
		id, ok := m.isAuthenticated(r)
		if ok {
			ctx := WithIdentity(r.Context(), id)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		// decide 401 vs 302
		if isAPIShaped(p, r) {
			write401(w)
			return
		}
		rd := r.URL.Path
		if r.URL.RawQuery != "" {
			rd = rd + "?" + r.URL.RawQuery
		}
		loc := "/oauth2/start?rd=" + urlQueryEscape(rd)
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, loc, http.StatusFound)
	})
}

func (m *Middleware) isAuthenticated(r *http.Request) (*identity.Identity, bool) {
	val := cookieValue(r, m.cfg.CookieName)
	if val == "" {
		return nil, false
	}
	s, err := m.cookie.Open(val)
	if err != nil {
		return nil, false
	}
	return &identity.Identity{Sub: s.Sub, Email: s.Email, Name: s.Name}, true
}

type Guard struct {
	cfg        *Config
	handler    *Handler
	middleware *Middleware
}

func NewGuard(cfg *Config) (*Guard, error) {
	if cfg == nil {
		return nil, nil
	}
	provider := NewOIDCProvider(cfg)
	fetch := func(ctx context.Context) (map[string]crypto.PublicKey, error) {
		meta, err := provider.Discover(ctx)
		if err != nil {
			return nil, err
		}
		ctx2, cancel := context.WithTimeout(ctx, cfg.DiscoveryTimeout)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx2, http.MethodGet, meta.JWKSURI, nil)
		if err != nil {
			return nil, err
		}
		resp, err := provider.client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		var js JWKSet
		if err := json.Unmarshal(body, &js); err != nil {
			return nil, err
		}
		out := map[string]crypto.PublicKey{}
		for _, j := range js.Keys {
			key, err := ParseJWK(j, cfg.AllowedAlgs)
			if err != nil {
				continue
			}
			kid := j.Kid
			if kid == "" {
				kid = fmt.Sprintf("key-%d", len(out))
			}
			out[kid] = key
		}
		return out, nil
	}
	cache := NewJWKSCache(cfg.JWKSCacheTTL, cfg.JWKSMinRefresh, cfg.JWKSStaleGrace, fetch)
	verifier := NewVerifier(cfg, cache)
	sessionKey, txnKey, err := deriveSubkeys(cfg.rootKeyBytes())
	if err != nil {
		return nil, err
	}
	cookie := &CookieCodec{name: cfg.CookieName, key: sessionKey, secure: cfg.CookieSecure, sameSite: cfg.CookieSameSite, path: "/"}
	txn := &CookieCodec{name: cfg.TxnCookieName, key: txnKey, secure: cfg.CookieSecure, sameSite: http.SameSiteLaxMode, path: "/oauth2", maxAge: 600}
	handler := NewHandler(cfg, provider, verifier, cookie, txn)
	mw := &Middleware{cookie: cookie, cfg: cfg}
	return &Guard{cfg: cfg, handler: handler, middleware: mw}, nil
}

func (g *Guard) Enabled() bool {
	return g != nil && g.cfg != nil && g.cfg.Enabled
}

func (g *Guard) Current(r *http.Request) (*identity.Identity, error) {
	if g == nil || g.handler == nil {
		return nil, fmt.Errorf("guard not initialized")
	}
	return g.handler.Current(r)
}

func (g *Guard) StateStoreCount() int {
	if g == nil || g.handler == nil {
		return 0
	}
	return g.handler.StateStoreCount()
}

func (g *Guard) Wrap(next http.Handler) http.Handler {
	if g == nil || g.middleware == nil {
		return next
	}
	return g.middleware.Wrap(next)
}

func (g *Guard) RegisterRoutes(mux *http.ServeMux) {
	if g == nil || g.handler == nil {
		return
	}
	g.handler.RegisterRoutes(mux)
}

func (g *Guard) Handler() *Handler { return g.handler }
func (g *Guard) Config() *Config   { return g.cfg }

// context helpers
type identityContextKey struct{}

func WithIdentity(ctx context.Context, id *identity.Identity) context.Context {
	return context.WithValue(ctx, identityContextKey{}, id)
}

func IdentityFromContext(ctx context.Context) (*identity.Identity, bool) {
	v := ctx.Value(identityContextKey{})
	if v == nil {
		return nil, false
	}
	id, ok := v.(*identity.Identity)
	return id, ok
}

func write401(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(map[string]string{"error": "unauthenticated"})
}

func isAPIShaped(p string, r *http.Request) bool {
	if strings.HasPrefix(p, "/fe/") || strings.HasPrefix(p, "/api/") {
		return true
	}
	accept := r.Header.Get("Accept")
	if strings.Contains(accept, "application/json") {
		return true
	}
	return false
}

func urlQueryEscape(s string) string {
	return url.QueryEscape(s)
}
