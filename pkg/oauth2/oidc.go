package oauth2

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type ProviderMetadata struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
	UserinfoEndpoint      string `json:"userinfo_endpoint"`
	EndSessionEndpoint    string `json:"end_session_endpoint"`
}

type OIDCProvider struct {
	cfg         *Config
	client      *http.Client
	mu          sync.RWMutex
	meta        *ProviderMetadata
	fetchedAt   time.Time
	fetchErr    error
	lastAttempt time.Time
	sf          *singleflight
}

func NewOIDCProvider(cfg *Config) *OIDCProvider {
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment, MaxIdleConns: 10, IdleConnTimeout: 30 * time.Second}
	return &OIDCProvider{
		cfg:    cfg,
		client: &http.Client{Transport: transport},
		sf:     &singleflight{},
	}
}

func (p *OIDCProvider) Discover(ctx context.Context) (*ProviderMetadata, error) {
	// negative cache check
	p.mu.RLock()
	if p.meta != nil {
		if time.Since(p.fetchedAt) < p.cfg.JWKSCacheTTL { // reuse ttl? use discovery timeout? We'll use JWKSCacheTTL as cache lifetime
			m := p.meta
			p.mu.RUnlock()
			return m, nil
		}
	}
	if p.fetchErr != nil && time.Since(p.lastAttempt) < 30*time.Second {
		err := p.fetchErr
		p.mu.RUnlock()
		return nil, err
	}
	p.mu.RUnlock()

	err := p.sf.Do(ctx, func(ctx context.Context) error {
		p.mu.Lock()
		p.lastAttempt = time.Now()
		p.mu.Unlock()

		ctx2, cancel := context.WithTimeout(ctx, p.cfg.DiscoveryTimeout)
		defer cancel()

		u := strings.TrimSuffix(p.cfg.IssuerURL, "/") + "/.well-known/openid-configuration"
		req, err := http.NewRequestWithContext(ctx2, http.MethodGet, u, nil)
		if err != nil {
			p.setFetchErr(err)
			return err
		}
		resp, err := p.client.Do(req)
		if err != nil {
			p.setFetchErr(err)
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			err := fmt.Errorf("discovery returned %d", resp.StatusCode)
			p.setFetchErr(err)
			return err
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
		if err != nil {
			p.setFetchErr(err)
			return err
		}
		var meta ProviderMetadata
		if err := json.Unmarshal(body, &meta); err != nil {
			p.setFetchErr(err)
			return err
		}
		// issuer match
		if normalizeIssuer(meta.Issuer) != normalizeIssuer(p.cfg.IssuerURL) {
			err := fmt.Errorf("issuer mismatch: got %s want %s", meta.Issuer, p.cfg.IssuerURL)
			p.setFetchErr(err)
			return err
		}
		// endpoint origin pinning
		endpoints := []struct {
			name string
			val  string
		}{
			{"authorization_endpoint", meta.AuthorizationEndpoint},
			{"token_endpoint", meta.TokenEndpoint},
			{"jwks_uri", meta.JWKSURI},
			{"userinfo_endpoint", meta.UserinfoEndpoint},
			{"end_session_endpoint", meta.EndSessionEndpoint},
		}
		for _, e := range endpoints {
			if e.val == "" {
				continue
			}
			if !sameOriginOrAllowed(p.cfg, e.val) {
				err := fmt.Errorf("%s is not same-origin with issuer: %s", e.name, e.val)
				p.setFetchErr(err)
				return err
			}
		}
		p.mu.Lock()
		p.meta = &meta
		p.fetchedAt = time.Now()
		p.fetchErr = nil
		p.mu.Unlock()
		return nil
	})
	if err != nil {
		return nil, err
	}
	p.mu.RLock()
	m := p.meta
	p.mu.RUnlock()
	if m == nil {
		return nil, fmt.Errorf("discovery failed")
	}
	return m, nil
}

func (p *OIDCProvider) setFetchErr(err error) {
	p.mu.Lock()
	p.fetchErr = err
	p.mu.Unlock()
}

// FetchedAt returns the time the discovery metadata was last successfully
// fetched (zero value if never). Used by AR-23 to gate the logout
// end-session hop on metadata freshness.
func (p *OIDCProvider) FetchedAt() time.Time {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.fetchedAt
}

// CachedMetadata returns the currently cached discovery metadata without
// triggering a network fetch. Used by AR-23 so logout can decide whether
// the cached metadata is fresh without forcing a refresh.
func (p *OIDCProvider) CachedMetadata() *ProviderMetadata {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.meta == nil {
		return nil
	}
	m := *p.meta
	return &m
}

func sameOriginOrAllowed(cfg *Config, endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	iu, err := url.Parse(cfg.IssuerURL)
	if err != nil {
		return false
	}
	origin := u.Scheme + "://" + u.Host
	issuerOrigin := iu.Scheme + "://" + iu.Host
	if origin == issuerOrigin {
		return true
	}
	for _, a := range cfg.AllowedEndpoints {
		if a == origin {
			return true
		}
	}
	return false
}

func (p *OIDCProvider) ExchangeCode(ctx context.Context, code, redirectURI, codeVerifier string) (string, string, error) {
	meta, err := p.Discover(ctx)
	if err != nil {
		return "", "", err
	}
	ctx2, cancel := context.WithTimeout(ctx, p.cfg.TokenExchangeTimeout)
	defer cancel()

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", p.cfg.ClientID)
	form.Set("code_verifier", codeVerifier)
	if p.cfg.ClientSecret != "" {
		form.Set("client_secret", p.cfg.ClientSecret)
	}

	req, err := http.NewRequestWithContext(ctx2, http.MethodPost, meta.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// CheckRedirect: reject cross-origin
	p.client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 0 && req.URL.Host != via[0].URL.Host {
			return http.ErrUseLastResponse
		}
		if len(via) >= 10 {
			return fmt.Errorf("too many redirects")
		}
		return nil
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("token_exchange_failed: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return "", "", fmt.Errorf("token_exchange_failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("token_exchange_failed: status %d", resp.StatusCode)
	}
	var tr struct {
		IDToken     string `json:"id_token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", "", fmt.Errorf("token_exchange_failed: %v", err)
	}
	if tr.IDToken == "" {
		return "", "", fmt.Errorf("token_exchange_failed: missing id_token")
	}
	return tr.IDToken, tr.AccessToken, nil
}

func (p *OIDCProvider) FetchUserinfo(ctx context.Context, accessToken, idTokenSub string) (map[string]any, error) {
	meta, err := p.Discover(ctx)
	if err != nil {
		return nil, err
	}
	if meta.UserinfoEndpoint == "" {
		return nil, fmt.Errorf("no userinfo endpoint")
	}
	ctx2, cancel := context.WithTimeout(ctx, p.cfg.UserinfoTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx2, http.MethodGet, meta.UserinfoEndpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	if sub, _ := m["sub"].(string); sub != "" && sub != idTokenSub {
		return nil, fmt.Errorf("userinfo sub mismatch")
	}
	return m, nil
}
