package oauth2

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
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

// parseBoolStrict parses a boolean env value. An EMPTY value takes the
// default; an UNRECOGNISED value is an ERROR: a typo in a boolean knob must
// fail loudly (naming the variable) instead of silently taking the default.
//
// The accepted vocabulary is exactly "true"/"false" (case-insensitive,
// trimmed) — the one the plan documents. Every documented value keeps its
// current meaning; anything else is a misconfiguration the operator must
// see. OAUTH2_ENABLED is deliberately NOT parsed through here — the plan
// pins it as "enabled iff exactly true; any other value => dormant" (D1).
func parseBoolStrict(name, raw string, def bool) (bool, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return def, nil
	}
	switch strings.ToLower(s) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return def, fmt.Errorf("invalid %s: %q (want true/false)", name, s)
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

// ConfigFromEnv builds the OAuth2 configuration from the environment.
//
// The function is split into ordered phases (parse -> validate -> clamp) so
// that each phase owns one concern. The ORDER of the phases IS the fail-fast
// order: the first misconfiguration wins, so the operator sees exactly one
// precise message. The fail-fast SET and the message prefixes are the ones
// the plan pins (phase1-plan.md §"startup fail-fast").
func ConfigFromEnv() (*Config, error) {
	// OAUTH2_ENABLED is the master toggle and the ONE knob whose "not true"
	// means "off": the plan pins "enabled iff the value is exactly true
	// (case-insensitive after TrimSpace); any other value => dormant" (D1).
	// Every OTHER boolean knob below is parsed STRICTLY: a typo there fails
	// the boot instead of silently taking a default.
	if !strings.EqualFold(strings.TrimSpace(os.Getenv(EnvEnabled)), "true") {
		return nil, nil
	}

	cfg := defaultConfig()
	for _, phase := range []func() error{
		cfg.applyIssuer,
		cfg.applyClient,
		cfg.applyURLs,
		cfg.applyCookieSecret,
		cfg.applySessionTTL,
		cfg.applyCookieName,
		cfg.applyCookieSecure,
		cfg.applyCookieSameSite,
		cfg.applyEmailDomains,
		cfg.applyRequireEmailVerified,
		cfg.applyAllowedAlgs,
		cfg.applyJWKSDurations,
		cfg.applyTimeouts,
		cfg.applyAllowedEndpoints,
		cfg.applyLogoutRedirect,
		cfg.applyLimits,
	} {
		if err := phase(); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

// defaultConfig is the baseline every env knob overrides.
func defaultConfig() *Config {
	return &Config{
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
}

// applyIssuer validates the issuer URL and pins its origin.
func (c *Config) applyIssuer() error {
	issuer := envStr(EnvIssuerURL)
	c.AllowInsecure = strings.EqualFold(envStr(EnvAllowInsecureIssuer), "true")
	if issuer == "" {
		return errors.New("OAUTH2_ISSUER_URL is required when OAUTH2_ENABLED=true")
	}
	// strip trailing slash
	issuer = strings.TrimRight(issuer, "/")
	u, err := url.Parse(issuer)
	if err != nil {
		return fmt.Errorf("OAUTH2_ISSUER_URL must be an absolute URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return errors.New("OAUTH2_ISSUER_URL must be an absolute URL")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return errors.New("OAUTH2_ISSUER_URL must not contain query or fragment")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && c.AllowInsecure) {
		return errors.New("OAUTH2_ISSUER_URL must use https (or http with OAUTH2_ALLOW_INSECURE_ISSUER=true)")
	}
	c.IssuerURL = issuer
	c.IssuerOrigin = u.Scheme + "://" + u.Host
	return nil
}

// applyClient pins the client id and the client secret.
func (c *Config) applyClient() error {
	cid := envStr(EnvClientID)
	if cid == "" {
		return errors.New("OAUTH2_CLIENT_ID is required when OAUTH2_ENABLED=true")
	}
	c.ClientID = cid
	// the secret is trimmed: a trailing newline from a mounted secret file
	// must never be sent to the IdP.
	c.ClientSecret = strings.TrimSpace(os.Getenv(EnvClientSecret))
	return nil
}

// applyURLs resolves the redirect/external URL pair. The redirect wins when
// both are set; the callback is derived from the external URL when only that
// one is set.
func (c *Config) applyURLs() error {
	redirectURL := envStr(EnvRedirectURL)
	externalURL := envStr(EnvExternalURL)
	if redirectURL == "" && externalURL == "" {
		return errors.New("OAUTH2_EXTERNAL_URL or OAUTH2_REDIRECT_URL is required when OAUTH2_ENABLED=true")
	}
	if redirectURL != "" {
		ru, err := url.Parse(redirectURL)
		if err != nil {
			return fmt.Errorf("OAUTH2_REDIRECT_URL must be an absolute URL: %w", err)
		}
		if ru.Scheme == "" || ru.Host == "" {
			return errors.New("OAUTH2_REDIRECT_URL must be an absolute URL")
		}
		c.RedirectURL = redirectURL
	}
	if externalURL != "" {
		eu, err := url.Parse(externalURL)
		if err != nil {
			return fmt.Errorf("OAUTH2_EXTERNAL_URL must be an absolute URL: %w", err)
		}
		if eu.Scheme == "" || eu.Host == "" {
			return errors.New("OAUTH2_EXTERNAL_URL must be an absolute URL")
		}
		externalURL = strings.TrimRight(externalURL, "/")
		c.ExternalURL = externalURL
	}
	if c.RedirectURL == "" && c.ExternalURL != "" {
		c.RedirectURL = c.ExternalURL + "/oauth2/callback"
	}
	return nil
}

// applyCookieSecret derives the root key and its two subkeys.
func (c *Config) applyCookieSecret() error {
	rawSecret := envStr(EnvCookieSecret)
	if rawSecret == "" {
		rawSecret = envStr(EnvCookieSecretFallback)
	}
	if rawSecret == "" {
		return errors.New("OAUTH2_COOKIE_SECRET (or COOKIE_SECRET) is required when OAUTH2_ENABLED=true")
	}
	key, mode, err := DeriveCookieKey(rawSecret)
	if err != nil {
		return err
	}
	c.rootKey = key
	c.SecretMode = mode
	return c.deriveSubkeys()
}

// applySessionTTL reads the session TTL and clamps it.
func (c *Config) applySessionTTL() error {
	v := envStr(EnvSessionTTL)
	if v == "" {
		return nil
	}
	d, err := parseDuration(v, c.SessionTTL)
	if err != nil {
		return fmt.Errorf("invalid OAUTH2_SESSION_TTL: %w", err)
	}
	c.SessionTTL = clampDuration(d, 5*time.Minute, 24*time.Hour)
	return nil
}

// applyCookieName validates the session cookie name.
func (c *Config) applyCookieName() error {
	if v := envStr(EnvCookieName); v != "" {
		c.CookieName = v
	}
	if c.CookieName == "_oauth2_proxy" {
		return errors.New("OAUTH2_COOKIE_NAME must not be _oauth2_proxy")
	}
	if !validCookieName(c.CookieName) {
		return errors.New("OAUTH2_COOKIE_NAME must match ^[A-Za-z0-9_-]{1,64}$")
	}
	if c.CookieName == c.TxnCookieName {
		return errors.New("OAUTH2_COOKIE_NAME must differ from the txn cookie name")
	}
	return nil
}

// applyCookieSecure resolves the Secure attribute and the "auto" opt-in.
func (c *Config) applyCookieSecure() error {
	secureRaw := strings.ToLower(envStr(EnvCookieSecure))
	autoOK, err := parseBoolStrict(EnvCookieSecureAutoOptIn, envStr(EnvCookieSecureAutoOptIn), false)
	if err != nil {
		return err
	}
	c.CookieSecureAutoOK = autoOK
	switch secureRaw {
	case "", "true":
		c.CookieSecure = true
		c.CookieSecureAuto = false
	case "false":
		c.CookieSecure = false
		c.CookieSecureAuto = false
	case "auto":
		if !c.CookieSecureAutoOK {
			return errors.New("OAUTH2_COOKIE_SECURE=auto requires OAUTH2_COOKIE_SECURE_AUTO_OK=true")
		}
		c.CookieSecure = true // resolved default; auto handled by opt-in (simplified)
		c.CookieSecureAuto = true
	default:
		return fmt.Errorf("invalid OAUTH2_COOKIE_SECURE: %s", secureRaw)
	}
	// false with https external url => error
	if !c.CookieSecure && c.ExternalURL != "" {
		u, _ := url.Parse(c.ExternalURL)
		if u != nil && u.Scheme == "https" {
			return errors.New("OAUTH2_COOKIE_SECURE=false is not allowed with an https external URL")
		}
	}
	return nil
}

// applyCookieSameSite resolves the SameSite attribute.
func (c *Config) applyCookieSameSite() error {
	ss := envStr(EnvCookieSameSite)
	if ss == "" {
		ss = "Lax"
	}
	switch strings.ToLower(ss) {
	case "lax":
		c.CookieSameSite = http.SameSiteLaxMode
	case "strict":
		c.CookieSameSite = http.SameSiteStrictMode
	case "none":
		c.CookieSameSite = http.SameSiteNoneMode
	default:
		return fmt.Errorf("invalid OAUTH2_COOKIE_SAMESITE: %s", ss)
	}
	if c.CookieSameSite == http.SameSiteNoneMode && !c.CookieSecure {
		return errors.New("OAUTH2_COOKIE_SAMESITE=None requires Secure=true")
	}
	return nil
}

// applyEmailDomains parses the domain allowlist and the "any domain" opt-in.
func (c *Config) applyEmailDomains() error {
	ed := os.Getenv(EnvEmailDomains)
	if strings.TrimSpace(ed) == "" {
		// distinguish explicitly empty: if env var set to empty string => error
		if _, present := os.LookupEnv(EnvEmailDomains); present {
			return errors.New("OAUTH2_EMAIL_DOMAINS explicitly empty is not allowed")
		}
		ed = "*"
	}
	allowAny, err := parseBoolStrict(EnvAllowAnyEmailDomain, envStr(EnvAllowAnyEmailDomain), false)
	if err != nil {
		return err
	}
	c.AllowAnyDomain = allowAny
	domains := []string{}
	for _, d := range strings.Split(ed, ",") {
		d = strings.TrimSpace(d)
		if d != "" {
			domains = append(domains, d)
		}
	}
	if len(domains) == 0 {
		return errors.New("OAUTH2_EMAIL_DOMAINS explicitly empty is not allowed")
	}
	c.EmailDomains = domains
	// * requires allow any
	hasStar := false
	for _, d := range domains {
		if d == "*" {
			hasStar = true
		}
	}
	if hasStar && !c.AllowAnyDomain {
		return errors.New("OAUTH2_EMAIL_DOMAINS=* requires OAUTH2_ALLOW_ANY_EMAIL_DOMAIN=true")
	}
	return nil
}

// applyRequireEmailVerified reads the email_verified strictness flag.
func (c *Config) applyRequireEmailVerified() error {
	v := envStr(EnvRequireEmailVerified)
	if v == "" {
		return nil
	}
	b, err := parseBoolStrict(EnvRequireEmailVerified, v, true)
	if err != nil {
		return err
	}
	c.RequireEmailVerified = b
	return nil
}

// applyAllowedAlgs parses the JWS algorithm allowlist.
func (c *Config) applyAllowedAlgs() error {
	if v := envStr(EnvAllowedAlgs); v != "" {
		c.AllowedAlgs = map[string]struct{}{}
		for _, a := range strings.Split(v, ",") {
			a = strings.TrimSpace(a)
			if a != "" {
				c.AllowedAlgs[a] = struct{}{}
			}
		}
	}
	if len(c.AllowedAlgs) == 0 {
		c.AllowedAlgs = map[string]struct{}{"RS256": {}}
	}
	// reject HS*/none case-insensitive
	for a := range c.AllowedAlgs {
		la := strings.ToLower(a)
		if strings.HasPrefix(la, "hs") || la == "none" {
			return fmt.Errorf("OAUTH2_ALLOWED_ALGS must not include %s", a)
		}
	}
	return nil
}

// applyJWKSDurations reads the JWKS cache/refresh/grace durations.
func (c *Config) applyJWKSDurations() error {
	if v := envStr(EnvJWKSCacheTTL); v != "" {
		d, err := parseDuration(v, c.JWKSCacheTTL)
		if err != nil {
			return fmt.Errorf("invalid OAUTH2_JWKS_CACHE_TTL: %w", err)
		}
		c.JWKSCacheTTL = clampDuration(d, time.Minute, 24*time.Hour)
	}
	if v := envStr(EnvJWKSMinRefresh); v != "" {
		d, err := parseDuration(v, c.JWKSMinRefresh)
		if err != nil {
			return fmt.Errorf("invalid OAUTH2_JWKS_MIN_REFRESH_INTERVAL: %w", err)
		}
		c.JWKSMinRefresh = clampDuration(d, 5*time.Second, 10*time.Minute)
	}
	if v := envStr(EnvJWKSStaleGrace); v != "" {
		d, err := parseDuration(v, c.JWKSStaleGrace)
		if err != nil {
			return fmt.Errorf("invalid OAUTH2_JWKS_STALE_GRACE: %w", err)
		}
		c.JWKSStaleGrace = clampDuration(d, time.Minute, 48*time.Hour)
	} else {
		c.JWKSStaleGrace = 2 * c.JWKSCacheTTL
		if c.JWKSStaleGrace < time.Minute {
			c.JWKSStaleGrace = time.Minute
		}
		if c.JWKSStaleGrace > 48*time.Hour {
			c.JWKSStaleGrace = 48 * time.Hour
		}
	}
	return nil
}

// applyTimeouts reads the discovery/token-exchange/userinfo timeouts.
func (c *Config) applyTimeouts() error {
	if v := envStr(EnvDiscoveryTimeout); v != "" {
		d, err := parseDuration(v, c.DiscoveryTimeout)
		if err != nil {
			return fmt.Errorf("invalid OAUTH2_DISCOVERY_TIMEOUT: %w", err)
		}
		c.DiscoveryTimeout = clampDuration(d, time.Second, 30*time.Second)
	}
	if v := envStr(EnvTokenExchangeTimeout); v != "" {
		d, err := parseDuration(v, c.TokenExchangeTimeout)
		if err != nil {
			return fmt.Errorf("invalid OAUTH2_TOKEN_EXCHANGE_TIMEOUT: %w", err)
		}
		c.TokenExchangeTimeout = clampDuration(d, time.Second, 60*time.Second)
	}
	if v := envStr(EnvUserinfoTimeout); v != "" {
		d, err := parseDuration(v, c.UserinfoTimeout)
		if err != nil {
			return fmt.Errorf("invalid OAUTH2_USERINFO_TIMEOUT: %w", err)
		}
		c.UserinfoTimeout = clampDuration(d, time.Second, 30*time.Second)
	}
	return nil
}

// applyAllowedEndpoints parses the cross-origin endpoint allowlist.
func (c *Config) applyAllowedEndpoints() error {
	v := envStr(EnvAllowedEndpoints)
	if v == "" {
		return nil
	}
	for _, e := range strings.Split(v, ",") {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		u, err := url.Parse(e)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("invalid OAUTH2_ALLOWED_ENDPOINTS entry: %s", e)
		}
		// The allowlist is compared by ORIGIN (scheme://host). An entry
		// carrying a path, a query or a fragment can never match that
		// comparison and would silently never fire — reject it loudly
		// instead of accepting a rule that does nothing.
		if u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("OAUTH2_ALLOWED_ENDPOINTS entry must be an origin without path/query/fragment: %s", e)
		}
		c.AllowedEndpoints = append(c.AllowedEndpoints, e)
	}
	return nil
}

// applyLogoutRedirect pins the post-logout redirect target.
func (c *Config) applyLogoutRedirect() error {
	v := envStr(EnvLogoutRedirect)
	if v == "" {
		c.LogoutRedirect = "/ui/"
		return nil
	}
	if !strings.HasPrefix(v, "/") {
		return errors.New("OAUTH2_LOGOUT_REDIRECT must be a path starting with /")
	}
	c.LogoutRedirect = v
	return nil
}

// applyLimits reads the state-store and rate-limit limits.
func (c *Config) applyLimits() error {
	if v := envStr(EnvStateStoreMax); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("invalid OAUTH2_STATE_STORE_MAX: %w", err)
		}
		c.StateStoreMax = clampInt(n, 64, 65536)
	}
	if v := envStr(EnvRateLimitRPM); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("invalid OAUTH2_RATE_LIMIT_RPM: %w", err)
		}
		c.RateLimitRPM = clampInt(n, 1, 600)
	}
	if v := envStr(EnvRateLimitBurst); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("invalid OAUTH2_RATE_LIMIT_BURST: %w", err)
		}
		c.RateLimitBurst = clampInt(n, 1, 100)
	}
	return nil
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

// deriveSubkeys derives the session and transaction subkeys from the root key
// and stores them on the Config. The Config is the ONLY holder of the derived
// keys: every consumer (the guard, the cookie codecs) READS them from here —
// nothing re-derives them on the side.
func (c *Config) deriveSubkeys() error {
	session, txn, err := deriveSubkeys(c.rootKey)
	if err != nil {
		return err
	}
	c.sessionKey = session
	c.txnKey = txn
	return nil
}

// ensureSubkeys derives the subkeys once. A Config built by ConfigFromEnv
// already carries them; a hand-built one (tests) does not.
func (c *Config) ensureSubkeys() error {
	if len(c.sessionKey) == 32 && len(c.txnKey) == 32 {
		return nil
	}
	return c.deriveSubkeys()
}

// Accessors for internal keys (used by cookie/guard)
func (c *Config) sessionKeyBytes() []byte { return c.sessionKey }
func (c *Config) txnKeyBytes() []byte     { return c.txnKey }
func (c *Config) rootKeyBytes() []byte    { return c.rootKey }
