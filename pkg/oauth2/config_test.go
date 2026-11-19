package oauth2

import (
	"encoding/base64"
	"os"
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
