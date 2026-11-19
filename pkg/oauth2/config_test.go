package oauth2

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"
	"time"
)

func setBaseEnv(t *testing.T) {
	t.Helper()
	t.Setenv(EnvEnabled, "true")
	t.Setenv(EnvIssuerURL, "https://issuer.example.com")
	t.Setenv(EnvClientID, "client123")
	t.Setenv(EnvExternalURL, "https://app.example.com")
	t.Setenv(EnvCookieSecret, "abcdefghijklmnopqrstuvwxyz012345") // 32 chars passphrase? len>=16 => sha256 mode
	t.Setenv(EnvAllowAnyEmailDomain, "true")
}

func TestConfigFromEnv_DormantByDefault(t *testing.T) {
	os.Unsetenv(EnvEnabled)
	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg != nil {
		t.Fatal("expected nil config when dormant")
	}
}

func TestConfigFromEnv_EnabledRequiresIssuerClientIDAndSecret(t *testing.T) {
	os.Unsetenv(EnvEnabled)
	t.Setenv(EnvEnabled, "true")
	// missing issuer
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("expected error missing issuer")
	}
	t.Setenv(EnvIssuerURL, "https://issuer.example.com")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("expected error missing client id")
	}
	t.Setenv(EnvClientID, "cid")
	// no cookie secret and no external url
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("expected error missing secret/external")
	}
}

func TestConfig_EnabledRequiresExternalURL(t *testing.T) {
	os.Unsetenv(EnvEnabled)
	t.Setenv(EnvEnabled, "true")
	t.Setenv(EnvIssuerURL, "https://issuer.example.com")
	t.Setenv(EnvClientID, "cid")
	t.Setenv(EnvCookieSecret, "secretsecretsecretsecret") // >=16
	t.Setenv(EnvAllowAnyEmailDomain, "true")
	t.Setenv(EnvEmailDomains, "*")
	// no external/redirect
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("expected error missing external url")
	}
	t.Setenv(EnvExternalURL, "https://app.example.com")
	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ExternalURL == "" {
		t.Fatal("external url not set")
	}
}

func TestConfigFromEnv_RejectsHS256InAllowlist(t *testing.T) {
	setBaseEnv(t)
	t.Setenv(EnvAllowedAlgs, "HS256")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("expected error HS256")
	}
	t.Setenv(EnvAllowedAlgs, "hs256")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("expected error hs256")
	}
	t.Setenv(EnvAllowedAlgs, "none")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("expected error none")
	}
	t.Setenv(EnvAllowedAlgs, "NoNe")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("expected error NoNe")
	}
}

func TestConfigFromEnv_RejectsOAuth2ProxyCookieName(t *testing.T) {
	setBaseEnv(t)
	t.Setenv(EnvCookieName, "_oauth2_proxy")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("expected error")
	}
}

func TestConfigFromEnv_EmptyEmailDomainsIsError(t *testing.T) {
	setBaseEnv(t)
	t.Setenv(EnvEmailDomains, "")
	// env var present empty
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("expected error empty email domains")
	}
}

func TestConfig_StarDomainRequiresExplicitAllow(t *testing.T) {
	setBaseEnv(t)
	t.Setenv(EnvAllowAnyEmailDomain, "")
	t.Setenv(EnvEmailDomains, "*")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("expected error * without allow")
	}
	t.Setenv(EnvAllowAnyEmailDomain, "true")
	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AllowAnyDomain {
		t.Fatal("expected allow any domain")
	}
}

func TestConfig_CookieSecureDefaultsTrue(t *testing.T) {
	setBaseEnv(t)
	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.CookieSecure {
		t.Fatal("expected secure default true")
	}
}

func TestConfig_TimeoutsAreClamped(t *testing.T) {
	setBaseEnv(t)
	t.Setenv(EnvDiscoveryTimeout, "100s")
	t.Setenv(EnvTokenExchangeTimeout, "1s")
	t.Setenv(EnvUserinfoTimeout, "1s")
	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DiscoveryTimeout != 30*time.Second {
		t.Fatalf("discovery not clamped: %v", cfg.DiscoveryTimeout)
	}
	if cfg.TokenExchangeTimeout != time.Second {
		t.Fatalf("token not clamped: %v", cfg.TokenExchangeTimeout)
	}
	if cfg.UserinfoTimeout != time.Second {
		t.Fatalf("userinfo not clamped: %v", cfg.UserinfoTimeout)
	}
}

func TestConfigFromEnv_TTLClamps(t *testing.T) {
	setBaseEnv(t)
	t.Setenv(EnvSessionTTL, "1m")
	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SessionTTL != 5*time.Minute {
		t.Fatalf("ttl not clamped min: %v", cfg.SessionTTL)
	}
	t.Setenv(EnvSessionTTL, "100h")
	cfg, err = ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SessionTTL != 24*time.Hour {
		t.Fatalf("ttl not clamped max: %v", cfg.SessionTTL)
	}
}

// S4: an OAUTH2_ALLOWED_ENDPOINTS entry that carries a path can never match
// the origin comparison and would silently never fire — it must be rejected
// at config load with a precise message.
func TestConfigFromEnv_RejectsPathBearingAllowedEndpoint(t *testing.T) {
	setBaseEnv(t)
	t.Setenv(EnvAllowedEndpoints, "https://other.example.com/path")
	_, err := ConfigFromEnv()
	if err == nil {
		t.Fatal("expected an error for a path-bearing allowed endpoint")
	}
	if !strings.Contains(err.Error(), "OAUTH2_ALLOWED_ENDPOINTS") {
		t.Fatalf("unexpected error: %v", err)
	}
	// control: an origin-only entry is accepted.
	t.Setenv(EnvAllowedEndpoints, "https://other.example.com")
	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("an origin-only entry must be accepted: %v", err)
	}
	if len(cfg.AllowedEndpoints) != 1 || cfg.AllowedEndpoints[0] != "https://other.example.com" {
		t.Fatalf("allowed endpoints: %v", cfg.AllowedEndpoints)
	}
}

func TestDeriveCookieKey_Base64AndPassphrase(t *testing.T) {
	// base64 std 32 bytes
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i + 1)
	}
	enc := base64.StdEncoding.EncodeToString(raw)
	key, mode, err := DeriveCookieKey(enc)
	if err != nil || mode != "base64-std-32" || len(key) != 32 {
		t.Fatalf("std: %v %s %d", err, mode, len(key))
	}
	// url encoding
	uenc := base64.URLEncoding.EncodeToString(raw)
	key2, mode2, err := DeriveCookieKey(uenc)
	if err != nil || len(key2) != 32 {
		t.Fatalf("url: %v %s %d", err, mode2, len(key2))
	}
	// passphrase
	key3, mode3, err := DeriveCookieKey("this-is-a-passphrase-long-enough")
	if err != nil || mode3 != "sha256-passphrase" || len(key3) != 32 {
		t.Fatalf("pass: %v %s %d", err, mode3, len(key3))
	}
	// determinism
	key4, _, _ := DeriveCookieKey("this-is-a-passphrase-long-enough")
	if string(key3) != string(key4) {
		t.Fatal("not deterministic")
	}
}

func TestDeriveCookieKey_PassphraseNeeds16Bytes(t *testing.T) {
	if _, _, err := DeriveCookieKey("short"); err == nil {
		t.Fatal("expected error short passphrase")
	}
}

func TestDeriveCookieKey_EmptyIsError(t *testing.T) {
	if _, _, err := DeriveCookieKey(""); err == nil {
		t.Fatal("expected error empty")
	}
}

func TestConfig_RedirectURLWinsOverExternalURL(t *testing.T) {
	setBaseEnv(t)
	t.Setenv(EnvRedirectURL, "https://app.example.com/oauth2/callback")
	t.Setenv(EnvExternalURL, "https://other.example.com")
	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RedirectURL != "https://app.example.com/oauth2/callback" {
		t.Fatalf("redirect not winning: %s", cfg.RedirectURL)
	}
}

// #10: a malformed NUMERIC env value must fail loudly, naming the variable —
// it must not silently take the default (fmt.Sscanf accepted "12abc" as 12).
func TestConfigFromEnv_RejectsMalformedNumericLimits(t *testing.T) {
	// every limit knob is set in every case: exactly ONE is malformed, the
	// others carry a valid value, so the error must name the malformed one.
	limits := []string{EnvStateStoreMax, EnvRateLimitRPM, EnvRateLimitBurst}
	cases := []struct {
		name string
		env  string
		val  string
	}{
		{"state store max", EnvStateStoreMax, "12abc"},
		{"rate limit rpm", EnvRateLimitRPM, "12abc"},
		{"rate limit burst", EnvRateLimitBurst, "12abc"},
		{"state store max (not a number)", EnvStateStoreMax, "abc"},
		{"rate limit rpm (fraction)", EnvRateLimitRPM, "1.5"},
	}
	for _, tc := range cases {
		setBaseEnv(t)
		for _, l := range limits {
			t.Setenv(l, "128")
		}
		t.Setenv(tc.env, tc.val)
		_, err := ConfigFromEnv()
		if err == nil {
			t.Fatalf("%s: expected an error for %q", tc.name, tc.val)
		}
		if !strings.Contains(err.Error(), tc.env) {
			t.Fatalf("%s: the error must name the variable: %v", tc.name, err)
		}
	}
}

// #10 (control): a valid numeric value is still applied.
func TestConfigFromEnv_AcceptsValidNumericLimits(t *testing.T) {
	setBaseEnv(t)
	t.Setenv(EnvStateStoreMax, "128")
	t.Setenv(EnvRateLimitRPM, "7")
	t.Setenv(EnvRateLimitBurst, "9")
	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StateStoreMax != 128 {
		t.Fatalf("state store max: %d", cfg.StateStoreMax)
	}
	if cfg.RateLimitRPM != 7 {
		t.Fatalf("rate limit rpm: %d", cfg.RateLimitRPM)
	}
	if cfg.RateLimitBurst != 9 {
		t.Fatalf("rate limit burst: %d", cfg.RateLimitBurst)
	}
}

// #10: a malformed BOOLEAN env value must fail loudly, naming the variable —
// not silently take the default.
func TestConfigFromEnv_RejectsMalformedBoolean(t *testing.T) {
	for _, env := range []string{EnvRequireEmailVerified, EnvAllowAnyEmailDomain, EnvCookieSecureAutoOptIn} {
		setBaseEnv(t)
		t.Setenv(env, "maybe")
		_, err := ConfigFromEnv()
		if err == nil {
			t.Fatalf("%s: expected an error for a malformed boolean", env)
		}
		if !strings.Contains(err.Error(), env) {
			t.Fatalf("%s: the error must name the variable: %v", env, err)
		}
	}
}

// #10 (control): the documented boolean vocabulary keeps its meaning.
func TestConfigFromEnv_AcceptsValidBoolean(t *testing.T) {
	setBaseEnv(t)
	t.Setenv(EnvRequireEmailVerified, "false")
	t.Setenv(EnvAllowAnyEmailDomain, "true")
	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RequireEmailVerified {
		t.Fatal("OAUTH2_REQUIRE_EMAIL_VERIFIED=false was not applied")
	}
	if !cfg.AllowAnyDomain {
		t.Fatal("OAUTH2_ALLOW_ANY_EMAIL_DOMAIN=true was not applied")
	}
}

// #10: a malformed DURATION env value fails loudly with the variable named
// (the durations were already loud — this pins the message).
func TestConfigFromEnv_RejectsMalformedDuration(t *testing.T) {
	durations := []string{EnvSessionTTL, EnvJWKSCacheTTL, EnvJWKSMinRefresh, EnvJWKSStaleGrace, EnvDiscoveryTimeout, EnvTokenExchangeTimeout, EnvUserinfoTimeout}
	for _, env := range durations {
		setBaseEnv(t)
		for _, d := range durations {
			t.Setenv(d, "10s")
		}
		t.Setenv(env, "10 seconds")
		_, err := ConfigFromEnv()
		if err == nil {
			t.Fatalf("%s: expected an error for a malformed duration", env)
		}
		if !strings.Contains(err.Error(), env) {
			t.Fatalf("%s: the error must name the variable: %v", env, err)
		}
	}
}
