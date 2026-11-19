package oauth2

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"crypto/hkdf"
)

const (
	EnvEnabled               = "OAUTH2_ENABLED"
	EnvIssuerURL             = "OAUTH2_ISSUER_URL"
	EnvClientID              = "OAUTH2_CLIENT_ID"
	EnvClientSecret          = "OAUTH2_CLIENT_SECRET"
	EnvRedirectURL           = "OAUTH2_REDIRECT_URL"
	EnvExternalURL           = "OAUTH2_EXTERNAL_URL"
	EnvCookieSecret          = "OAUTH2_COOKIE_SECRET"
	EnvCookieSecretFallback  = "COOKIE_SECRET"
	EnvSessionTTL            = "OAUTH2_SESSION_TTL"
	EnvCookieName            = "OAUTH2_COOKIE_NAME"
	EnvCookieSecure          = "OAUTH2_COOKIE_SECURE"
	EnvCookieSameSite        = "OAUTH2_COOKIE_SAMESITE"
	EnvEmailDomains          = "OAUTH2_EMAIL_DOMAINS"
	EnvAllowAnyEmailDomain   = "OAUTH2_ALLOW_ANY_EMAIL_DOMAIN"
	EnvRequireEmailVerified  = "OAUTH2_REQUIRE_EMAIL_VERIFIED"
	EnvAllowedAlgs           = "OAUTH2_ALLOWED_ALGS"
	EnvJWKSCacheTTL          = "OAUTH2_JWKS_CACHE_TTL"
	EnvJWKSMinRefresh        = "OAUTH2_JWKS_MIN_REFRESH_INTERVAL"
	EnvJWKSStaleGrace        = "OAUTH2_JWKS_STALE_GRACE"
	EnvDiscoveryTimeout      = "OAUTH2_DISCOVERY_TIMEOUT"
	EnvTokenExchangeTimeout  = "OAUTH2_TOKEN_EXCHANGE_TIMEOUT"
	EnvUserinfoTimeout       = "OAUTH2_USERINFO_TIMEOUT"
	EnvAllowedEndpoints      = "OAUTH2_ALLOWED_ENDPOINTS"
	EnvLogoutRedirect        = "OAUTH2_LOGOUT_REDIRECT"
	EnvStateStoreMax         = "OAUTH2_STATE_STORE_MAX"
	EnvRateLimitRPM          = "OAUTH2_RATE_LIMIT_RPM"
	EnvRateLimitBurst        = "OAUTH2_RATE_LIMIT_BURST"
	EnvCookieSecureAutoOptIn = "OAUTH2_COOKIE_SECURE_AUTO_OK"
	EnvAllowInsecureIssuer   = "OAUTH2_ALLOW_INSECURE_ISSUER"
)

type Config struct {
	Enabled              bool
	IssuerURL            string
	IssuerOrigin         string
	ClientID             string
	ClientSecret         string
	RedirectURL          string
	ExternalURL          string
	CookieName           string
	TxnCookieName        string
	CookieSecure         bool
	CookieSecureAuto     bool
	CookieSameSite       http.SameSite
	SessionTTL           time.Duration
	TxnTTL               time.Duration
	EmailDomains         []string
	AllowAnyDomain       bool
	RequireEmailVerified bool
	AllowedAlgs          map[string]struct{}
	AllowedEndpoints     []string
	LogoutRedirect       string
	JWKSCacheTTL         time.Duration
	JWKSMinRefresh       time.Duration
	JWKSStaleGrace       time.Duration
	StateStoreMax        int
	RateLimitRPM         int
	RateLimitBurst       int
	CookieSecureAutoOK   bool
	DiscoveryTimeout     time.Duration
	TokenExchangeTimeout time.Duration
	UserinfoTimeout      time.Duration
	AllowInsecure        bool
	SecretMode           string
	rootKey              []byte
	sessionKey           []byte
	txnKey               []byte
}

func envStr(key string) string { return strings.TrimSpace(os.Getenv(key)) }

func parseBool(s string, def bool) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return def
	}
	return s == "true" || s == "1" || s == "yes"
}

func parseDuration(s string, def time.Duration) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return def, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, err
	}
	return d, nil
}

func clampDuration(d, min, max time.Duration) time.Duration {
	if d < min {
		return min
	}
	if d > max {
		return max
	}
	return d
}

func clampInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func ConfigFromEnv() (*Config, error) {
	enabled := strings.EqualFold(strings.TrimSpace(os.Getenv(EnvEnabled)), "true")
	if !enabled {
		return nil, nil
	}

	cfg := &Config{
		Enabled:              true,
		TxnCookieName:        "llmproxy_oauth2_txn",
		CookieName:           "llmproxy_oauth2",
		CookieSecure:         true,
		CookieSameSite:       http.SameSiteLaxMode,
		SessionTTL:           24 * time.Hour,
		TxnTTL:               10 * time.Minute,
		RequireEmailVerified: true,
		JWKSCacheTTL:         time.Hour,
		JWKSMinRefresh:       60 * time.Second,
		JWKSStaleGrace:       2 * time.Hour,
		DiscoveryTimeout:     5 * time.Second,
		TokenExchangeTimeout: 10 * time.Second,
		UserinfoTimeout:      5 * time.Second,
		StateStoreMax:        1024,
		RateLimitRPM:         10,
		RateLimitBurst:       20,
		EmailDomains:         []string{"*"},
		AllowAnyDomain:       false,
		AllowedAlgs:          map[string]struct{}{"RS256": {}},
		AllowedEndpoints:     nil,
		LogoutRedirect:       "",
	}

	// issuer
	issuer := envStr(EnvIssuerURL)
	cfg.AllowInsecure = strings.EqualFold(envStr(EnvAllowInsecureIssuer), "true")
	if issuer == "" {
		return nil, errors.New("OAUTH2_ISSUER_URL is required when OAUTH2_ENABLED=true")
	}
	// strip trailing slash
	issuer = strings.TrimRight(issuer, "/")
	u, err := url.Parse(issuer)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("OAUTH2_ISSUER_URL must be an absolute URL: %v", err)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("OAUTH2_ISSUER_URL must not contain query or fragment")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && cfg.AllowInsecure) {
		return nil, errors.New("OAUTH2_ISSUER_URL must use https (or http with OAUTH2_ALLOW_INSECURE_ISSUER=true)")
	}
	cfg.IssuerURL = issuer
	cfg.IssuerOrigin = u.Scheme + "://" + u.Host

	// client id
	cid := envStr(EnvClientID)
	if cid == "" {
		return nil, errors.New("OAUTH2_CLIENT_ID is required when OAUTH2_ENABLED=true")
	}
	cfg.ClientID = cid

	cfg.ClientSecret = os.Getenv(EnvClientSecret) // not trimmed? keep raw-ish but trim
	cfg.ClientSecret = strings.TrimSpace(cfg.ClientSecret)

	// external/redirect url precedence
	redirectURL := envStr(EnvRedirectURL)
	externalURL := envStr(EnvExternalURL)
	if redirectURL == "" && externalURL == "" {
		return nil, errors.New("OAUTH2_EXTERNAL_URL or OAUTH2_REDIRECT_URL is required when OAUTH2_ENABLED=true")
	}
	if redirectURL != "" {
		// validate absolute
		ru, err := url.Parse(redirectURL)
		if err != nil || ru.Scheme == "" || ru.Host == "" {
			return nil, fmt.Errorf("OAUTH2_REDIRECT_URL must be an absolute URL: %v", err)
		}
		cfg.RedirectURL = redirectURL
	}
	if externalURL != "" {
		eu, err := url.Parse(externalURL)
		if err != nil || eu.Scheme == "" || eu.Host == "" {
			return nil, fmt.Errorf("OAUTH2_EXTERNAL_URL must be an absolute URL: %v", err)
		}
		externalURL = strings.TrimRight(externalURL, "/")
		cfg.ExternalURL = externalURL
	}
	if cfg.RedirectURL == "" && cfg.ExternalURL != "" {
		cfg.RedirectURL = cfg.ExternalURL + "/oauth2/callback"
	}
	// if both set, redirect wins (already set). if only external, derived.

	// cookie secret
	rawSecret := envStr(EnvCookieSecret)
	if rawSecret == "" {
		rawSecret = envStr(EnvCookieSecretFallback)
	}
	if rawSecret == "" {
		return nil, errors.New("OAUTH2_COOKIE_SECRET (or COOKIE_SECRET) is required when OAUTH2_ENABLED=true")
	}
	key, mode, err := DeriveCookieKey(rawSecret)
	if err != nil {
		return nil, err
	}
	cfg.rootKey = key
	cfg.SecretMode = mode
	sk, tk, err := deriveSubkeys(key)
	if err != nil {
		return nil, err
	}
	cfg.sessionKey = sk
	cfg.txnKey = tk

	// session ttl
	if v := envStr(EnvSessionTTL); v != "" {
		d, err := parseDuration(v, cfg.SessionTTL)
		if err != nil {
			return nil, fmt.Errorf("invalid OAUTH2_SESSION_TTL: %v", err)
		}
		cfg.SessionTTL = clampDuration(d, 5*time.Minute, 24*time.Hour)
	}

	// cookie name
	if v := envStr(EnvCookieName); v != "" {
		cfg.CookieName = v
	}
	if cfg.CookieName == "_oauth2_proxy" {
		return nil, errors.New("OAUTH2_COOKIE_NAME must not be _oauth2_proxy")
	}
	if !validCookieName(cfg.CookieName) {
		return nil, errors.New("OAUTH2_COOKIE_NAME must match ^[A-Za-z0-9_-]{1,64}$")
	}
	if cfg.CookieName == cfg.TxnCookieName {
		return nil, errors.New("OAUTH2_COOKIE_NAME must differ from the txn cookie name")
	}

	// cookie secure
	secureRaw := strings.ToLower(envStr(EnvCookieSecure))
	cfg.CookieSecureAutoOK = strings.EqualFold(envStr(EnvCookieSecureAutoOptIn), "true")
	switch secureRaw {
	case "", "true":
		cfg.CookieSecure = true
		cfg.CookieSecureAuto = false
	case "false":
		cfg.CookieSecure = false
		cfg.CookieSecureAuto = false
	case "auto":
		if !cfg.CookieSecureAutoOK {
			return nil, errors.New("OAUTH2_COOKIE_SECURE=auto requires OAUTH2_COOKIE_SECURE_AUTO_OK=true")
		}
		cfg.CookieSecure = true // resolved default; auto handled by opt-in (simplified)
		cfg.CookieSecureAuto = true
	default:
		return nil, fmt.Errorf("invalid OAUTH2_COOKIE_SECURE: %s", secureRaw)
	}
	// false with https external url => error
	if !cfg.CookieSecure && cfg.ExternalURL != "" {
		u, _ := url.Parse(cfg.ExternalURL)
		if u != nil && u.Scheme == "https" {
			return nil, errors.New("OAUTH2_COOKIE_SECURE=false is not allowed with an https external URL")
		}
	}

	// cookie samesite
	ss := envStr(EnvCookieSameSite)
	if ss == "" {
		ss = "Lax"
	}
	switch strings.ToLower(ss) {
	case "lax":
		cfg.CookieSameSite = http.SameSiteLaxMode
	case "strict":
		cfg.CookieSameSite = http.SameSiteStrictMode
	case "none":
		cfg.CookieSameSite = http.SameSiteNoneMode
	default:
		return nil, fmt.Errorf("invalid OAUTH2_COOKIE_SAMESITE: %s", ss)
	}
	if cfg.CookieSameSite == http.SameSiteNoneMode && !cfg.CookieSecure {
		return nil, errors.New("OAUTH2_COOKIE_SAMESITE=None requires Secure=true")
	}

	// email domains
	ed := os.Getenv(EnvEmailDomains)
	if strings.TrimSpace(ed) == "" {
		// distinguish explicitly empty: if env var set to empty string => error
		if _, present := os.LookupEnv(EnvEmailDomains); present {
			return nil, errors.New("OAUTH2_EMAIL_DOMAINS explicitly empty is not allowed")
		}
		ed = "*"
	}
	cfg.AllowAnyDomain = strings.EqualFold(envStr(EnvAllowAnyEmailDomain), "true")
	domains := []string{}
	for _, d := range strings.Split(ed, ",") {
		d = strings.TrimSpace(d)
		if d != "" {
			domains = append(domains, d)
		}
	}
	if len(domains) == 0 {
		return nil, errors.New("OAUTH2_EMAIL_DOMAINS explicitly empty is not allowed")
	}
	cfg.EmailDomains = domains
	// * requires allow any
	hasStar := false
	for _, d := range domains {
		if d == "*" {
			hasStar = true
		}
	}
	if hasStar && !cfg.AllowAnyDomain {
		return nil, errors.New("OAUTH2_EMAIL_DOMAINS=* requires OAUTH2_ALLOW_ANY_EMAIL_DOMAIN=true")
	}

	// require email verified
	if v := envStr(EnvRequireEmailVerified); v != "" {
		cfg.RequireEmailVerified = parseBool(v, true)
	}

	// allowed algs
	if v := envStr(EnvAllowedAlgs); v != "" {
		cfg.AllowedAlgs = map[string]struct{}{}
		for _, a := range strings.Split(v, ",") {
			a = strings.TrimSpace(a)
			if a != "" {
				cfg.AllowedAlgs[a] = struct{}{}
			}
		}
	}
	if len(cfg.AllowedAlgs) == 0 {
		cfg.AllowedAlgs = map[string]struct{}{"RS256": {}}
	}
	// reject HS*/none case-insensitive
	for a := range cfg.AllowedAlgs {
		la := strings.ToLower(a)
		if strings.HasPrefix(la, "hs") || la == "none" {
			return nil, fmt.Errorf("OAUTH2_ALLOWED_ALGS must not include %s", a)
		}
	}

	// jwks cache ttl
	if v := envStr(EnvJWKSCacheTTL); v != "" {
		d, err := parseDuration(v, cfg.JWKSCacheTTL)
		if err != nil {
			return nil, fmt.Errorf("invalid OAUTH2_JWKS_CACHE_TTL: %v", err)
		}
		cfg.JWKSCacheTTL = clampDuration(d, time.Minute, 24*time.Hour)
	}
	if v := envStr(EnvJWKSMinRefresh); v != "" {
		d, err := parseDuration(v, cfg.JWKSMinRefresh)
		if err != nil {
			return nil, fmt.Errorf("invalid OAUTH2_JWKS_MIN_REFRESH_INTERVAL: %v", err)
		}
		cfg.JWKSMinRefresh = clampDuration(d, 5*time.Second, 10*time.Minute)
	}
	if v := envStr(EnvJWKSStaleGrace); v != "" {
		d, err := parseDuration(v, cfg.JWKSStaleGrace)
		if err != nil {
			return nil, fmt.Errorf("invalid OAUTH2_JWKS_STALE_GRACE: %v", err)
		}
		cfg.JWKSStaleGrace = clampDuration(d, time.Minute, 48*time.Hour)
	} else {
		cfg.JWKSStaleGrace = 2 * cfg.JWKSCacheTTL
		if cfg.JWKSStaleGrace < time.Minute {
			cfg.JWKSStaleGrace = time.Minute
		}
		if cfg.JWKSStaleGrace > 48*time.Hour {
			cfg.JWKSStaleGrace = 48 * time.Hour
		}
	}
	// discovery timeout
	if v := envStr(EnvDiscoveryTimeout); v != "" {
		d, err := parseDuration(v, cfg.DiscoveryTimeout)
		if err != nil {
			return nil, fmt.Errorf("invalid OAUTH2_DISCOVERY_TIMEOUT: %v", err)
		}
		cfg.DiscoveryTimeout = clampDuration(d, time.Second, 30*time.Second)
	}
	if v := envStr(EnvTokenExchangeTimeout); v != "" {
		d, err := parseDuration(v, cfg.TokenExchangeTimeout)
		if err != nil {
			return nil, fmt.Errorf("invalid OAUTH2_TOKEN_EXCHANGE_TIMEOUT: %v", err)
		}
		cfg.TokenExchangeTimeout = clampDuration(d, time.Second, 60*time.Second)
	}
	if v := envStr(EnvUserinfoTimeout); v != "" {
		d, err := parseDuration(v, cfg.UserinfoTimeout)
		if err != nil {
			return nil, fmt.Errorf("invalid OAUTH2_USERINFO_TIMEOUT: %v", err)
		}
		cfg.UserinfoTimeout = clampDuration(d, time.Second, 30*time.Second)
	}

	// allowed endpoints
	if v := envStr(EnvAllowedEndpoints); v != "" {
		for _, e := range strings.Split(v, ",") {
			e = strings.TrimSpace(e)
			if e == "" {
				continue
			}
			u, err := url.Parse(e)
			if err != nil || u.Scheme == "" || u.Host == "" {
				return nil, fmt.Errorf("invalid OAUTH2_ALLOWED_ENDPOINTS entry: %s", e)
			}
			cfg.AllowedEndpoints = append(cfg.AllowedEndpoints, e)
		}
	}

	// logout redirect
	if v := envStr(EnvLogoutRedirect); v != "" {
		if !strings.HasPrefix(v, "/") {
			return nil, errors.New("OAUTH2_LOGOUT_REDIRECT must be a path starting with /")
		}
		cfg.LogoutRedirect = v
	} else {
		cfg.LogoutRedirect = "/ui/"
	}

	// state store max
	if v := envStr(EnvStateStoreMax); v != "" {
		var n int
		_, err := fmt.Sscanf(v, "%d", &n)
		if err != nil {
			return nil, fmt.Errorf("invalid OAUTH2_STATE_STORE_MAX: %v", err)
		}
		cfg.StateStoreMax = clampInt(n, 64, 65536)
	}

	// rate limit
	if v := envStr(EnvRateLimitRPM); v != "" {
		var n int
		_, err := fmt.Sscanf(v, "%d", &n)
		if err != nil {
			return nil, fmt.Errorf("invalid OAUTH2_RATE_LIMIT_RPM: %v", err)
		}
		cfg.RateLimitRPM = clampInt(n, 1, 600)
	}
	if v := envStr(EnvRateLimitBurst); v != "" {
		var n int
		_, err := fmt.Sscanf(v, "%d", &n)
		if err != nil {
			return nil, fmt.Errorf("invalid OAUTH2_RATE_LIMIT_BURST: %v", err)
		}
		cfg.RateLimitBurst = clampInt(n, 1, 100)
	}

	// default logout redirect if empty
	if cfg.LogoutRedirect == "" {
		cfg.LogoutRedirect = "/ui/"
	}

	// warn algs differs? not needed here; logging in caller.

	// sort email domains? no.

	_ = sort.Strings

	return cfg, nil
}

func validCookieName(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func (c *Config) RateLimit() (int, int)     { return c.RateLimitRPM, c.RateLimitBurst }
func (c *Config) StateStoreMaxRecords() int { return c.StateStoreMax }
func (c *Config) ExternalBaseURL() string   { return c.ExternalURL }
func (c *Config) CallbackURL() string       { return c.RedirectURL }

func DeriveCookieKey(raw string) ([]byte, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, "", errors.New("cookie secret is empty")
	}
	// rung 1-4: base64 decode with various encodings, len 32
	decoders := []struct {
		dec  *base64.Encoding
		mode string
	}{
		{base64.StdEncoding, "base64-std-32"},
		{base64.RawStdEncoding, "base64-std-32"},
		{base64.URLEncoding, "base64-url-32"},
		{base64.RawURLEncoding, "base64-url-32"},
	}
	for _, d := range decoders {
		b, err := d.dec.DecodeString(raw)
		if err == nil && len(b) == 32 {
			return b, d.mode, nil
		}
	}
	// passphrase branch
	if len(raw) >= 16 {
		h := sha256.Sum256([]byte(raw))
		return h[:], "sha256-passphrase", nil
	}
	return nil, "", errors.New("cookie secret passphrase must be at least 16 bytes")
}

func deriveSubkeys(root []byte) ([]byte, []byte, error) {
	if len(root) != 32 {
		return nil, nil, errors.New("root key must be 32 bytes")
	}
	session, err := hkdf.Key(sha256.New, root, nil, "session", 32)
	if err != nil {
		return nil, nil, err
	}
	txn, err := hkdf.Key(sha256.New, root, nil, "txn", 32)
	if err != nil {
		return nil, nil, err
	}
	return session, txn, nil
}

// Accessors for internal keys (used by cookie/guard)
func (c *Config) sessionKeyBytes() []byte { return c.sessionKey }
func (c *Config) txnKeyBytes() []byte     { return c.txnKey }
func (c *Config) rootKeyBytes() []byte    { return c.rootKey }
