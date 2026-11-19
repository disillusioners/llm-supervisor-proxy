package oauth2

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testCfg() *Config {
	return &Config{
		IssuerURL:   "https://issuer.example.com",
		ClientID:    "client123",
		AllowedAlgs: map[string]struct{}{"RS256": {}, "RS384": {}, "RS512": {}, "ES256": {}, "ES384": {}, "ES512": {}, "EdDSA": {}},
	}
}

func newCache(kid string, pub crypto.PublicKey) *JWKSCache {
	return &JWKSCache{
		keys:       map[string]crypto.PublicKey{kid: pub},
		keyAlg:     map[string]string{},
		fetchedAt:  time.Now(),
		ttl:        time.Hour,
		sf:         &singleflight{},
		grace:      time.Hour,
		minRefresh: time.Minute,
	}
}

func rsaKey(t *testing.T) *rsa.PrivateKey {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func ecKey(t *testing.T, curve elliptic.Curve) *ecdsa.PrivateKey {
	k, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func makeRSAJWT(t *testing.T, alg string, claims Claims, priv *rsa.PrivateKey, kid string) string {
	header := map[string]string{"alg": alg, "typ": "JWT", "kid": kid}
	hb, _ := json.Marshal(header)
	cb, _ := json.Marshal(claims)
	hEnc := base64.RawURLEncoding.EncodeToString(hb)
	cEnc := base64.RawURLEncoding.EncodeToString(cb)
	signing := hEnc + "." + cEnc
	var hash crypto.Hash
	switch alg {
	case "RS256":
		hash = crypto.SHA256
	case "RS384":
		hash = crypto.SHA384
	case "RS512":
		hash = crypto.SHA512
	}
	h := hash.New()
	h.Write([]byte(signing))
	digest := h.Sum(nil)
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, hash, digest)
	if err != nil {
		t.Fatal(err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func makeESJWT(t *testing.T, alg string, claims Claims, priv *ecdsa.PrivateKey, kid string) string {
	header := map[string]string{"alg": alg, "typ": "JWT", "kid": kid}
	hb, _ := json.Marshal(header)
	cb, _ := json.Marshal(claims)
	hEnc := base64.RawURLEncoding.EncodeToString(hb)
	cEnc := base64.RawURLEncoding.EncodeToString(cb)
	signing := hEnc + "." + cEnc
	var hash crypto.Hash
	var curve elliptic.Curve
	switch alg {
	case "ES256":
		hash = crypto.SHA256
		curve = elliptic.P256()
	case "ES384":
		hash = crypto.SHA384
		curve = elliptic.P384()
	case "ES512":
		hash = crypto.SHA512
		curve = elliptic.P521()
	}
	_ = curve
	h := hash.New()
	h.Write([]byte(signing))
	digest := h.Sum(nil)
	r, s, err := ecdsa.Sign(rand.Reader, priv, digest)
	if err != nil {
		t.Fatal(err)
	}
	keyBytes := (priv.Curve.Params().BitSize + 7) / 8
	rb := r.Bytes()
	sb := s.Bytes()
	// pad
	sig := make([]byte, 2*keyBytes)
	copy(sig[keyBytes-len(rb):keyBytes], rb)
	copy(sig[2*keyBytes-len(sb):], sb)
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func makeEdJWT(t *testing.T, claims Claims, priv ed25519.PrivateKey, kid string) string {
	header := map[string]string{"alg": "EdDSA", "typ": "JWT", "kid": kid}
	hb, _ := json.Marshal(header)
	cb, _ := json.Marshal(claims)
	hEnc := base64.RawURLEncoding.EncodeToString(hb)
	cEnc := base64.RawURLEncoding.EncodeToString(cb)
	signing := hEnc + "." + cEnc
	sig := ed25519.Sign(priv, []byte(signing))
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func validClaims() Claims {
	return Claims{
		Issuer:    "https://issuer.example.com",
		Subject:   "user1",
		Audience:  []string{"client123"},
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
		Nonce:     "nonce123",
	}
}

func TestJWT_RejectsHS256AlgConfusion(t *testing.T) {
	cfg := testCfg()
	// make an HS256 token signed with RSA public key as HMAC secret
	priv := rsaKey(t)
	_ = priv
	// build token with alg HS256
	header := map[string]string{"alg": "HS256", "typ": "JWT", "kid": "k1"}
	hb, _ := json.Marshal(header)
	cb, _ := json.Marshal(validClaims())
	signing := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(cb)
	// hmac with public key bytes? Use rsa pub modulus bytes as secret
	hmacSig := make([]byte, 32)
	token := signing + "." + base64.RawURLEncoding.EncodeToString(hmacSig)
	cache := newCache("k1", &priv.PublicKey)
	v := NewVerifier(cfg, cache)
	if _, err := v.Verify(context.Background(), token); err == nil {
		t.Fatal("expected rejection")
	}
}

func TestJWT_RejectsNoneAlg(t *testing.T) {
	cfg := testCfg()
	priv := rsaKey(t)
	header := map[string]string{"alg": "none", "typ": "JWT", "kid": "k1"}
	hb, _ := json.Marshal(header)
	cb, _ := json.Marshal(validClaims())
	signing := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(cb)
	token := signing + "."
	cache := newCache("k1", &priv.PublicKey)
	v := NewVerifier(cfg, cache)
	if _, err := v.Verify(context.Background(), token); err == nil {
		t.Fatal("expected rejection")
	}
}

func TestJWT_RejectsUnknownAlg(t *testing.T) {
	cfg := testCfg()
	priv := rsaKey(t)
	header := map[string]string{"alg": "XX999", "typ": "JWT", "kid": "k1"}
	hb, _ := json.Marshal(header)
	cb, _ := json.Marshal(validClaims())
	signing := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(cb)
	token := signing + ".AAAA"
	cache := newCache("k1", &priv.PublicKey)
	v := NewVerifier(cfg, cache)
	if _, err := v.Verify(context.Background(), token); err == nil {
		t.Fatal("expected rejection")
	}
}

func TestJWT_RS256RoundTrip(t *testing.T) {
	cfg := testCfg()
	priv := rsaKey(t)
	tok := makeRSAJWT(t, "RS256", validClaims(), priv, "k1")
	cache := newCache("k1", &priv.PublicKey)
	v := NewVerifier(cfg, cache)
	c, err := v.Verify(context.Background(), tok)
	if err != nil {
		t.Fatal(err)
	}
	if c.Subject != "user1" {
		t.Fatal("subject mismatch")
	}
}

func TestJWT_ES256RoundTrip(t *testing.T) {
	cfg := testCfg()
	priv := ecKey(t, elliptic.P256())
	tok := makeESJWT(t, "ES256", validClaims(), priv, "k1")
	cache := newCache("k1", &priv.PublicKey)
	v := NewVerifier(cfg, cache)
	_, err := v.Verify(context.Background(), tok)
	if err != nil {
		t.Fatal(err)
	}
}

func TestJWT_EdDSARoundTrip(t *testing.T) {
	cfg := testCfg()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	tok := makeEdJWT(t, validClaims(), priv, "k1")
	cache := newCache("k1", pub)
	v := NewVerifier(cfg, cache)
	_, err := v.Verify(context.Background(), tok)
	if err != nil {
		t.Fatal(err)
	}
}

func TestJWT_RejectsExpired(t *testing.T) {
	cfg := testCfg()
	priv := rsaKey(t)
	claims := validClaims()
	claims.ExpiresAt = time.Now().Add(-time.Hour).Unix()
	tok := makeRSAJWT(t, "RS256", claims, priv, "k1")
	cache := newCache("k1", &priv.PublicKey)
	v := NewVerifier(cfg, cache)
	if _, err := v.Verify(context.Background(), tok); err == nil {
		t.Fatal("expected expired")
	}
}

func TestJWT_RejectsWrongIssuer(t *testing.T) {
	cfg := testCfg()
	priv := rsaKey(t)
	claims := validClaims()
	claims.Issuer = "https://evil.com"
	tok := makeRSAJWT(t, "RS256", claims, priv, "k1")
	cache := newCache("k1", &priv.PublicKey)
	v := NewVerifier(cfg, cache)
	if _, err := v.Verify(context.Background(), tok); err == nil {
		t.Fatal("expected issuer mismatch")
	}
}

// Item 5: a MALFORMED issuer (one net/url cannot parse) must be rejected.
// The parse-error fallback of normalizeIssuer may only ever fail CLOSED: it
// must never accept a token whose issuer only loosely matches the configured
// one.
func TestJWT_RejectsMalformedIssuer(t *testing.T) {
	cfg := testCfg()
	priv := rsaKey(t)
	malformed := []string{
		"https://issuer.example.com/%zz",  // invalid URL escape
		"https://issuer.example.com:80x",  // invalid port
		"https://issuer.example.com/\x7f", // control character
		"https://[issuer.example.com",     // malformed host
	}
	for _, iss := range malformed {
		claims := validClaims()
		claims.Issuer = iss
		tok := makeRSAJWT(t, "RS256", claims, priv, "k1")
		cache := newCache("k1", &priv.PublicKey)
		v := NewVerifier(cfg, cache)
		_, err := v.Verify(context.Background(), tok)
		if err == nil {
			t.Fatalf("the malformed issuer %q was accepted", iss)
		}
		if !strings.Contains(err.Error(), "issuer") {
			t.Fatalf("the malformed issuer %q was rejected for the wrong reason: %v", iss, err)
		}
	}
}

func TestJWT_AudienceArrayAndString(t *testing.T) {
	cfg := testCfg()
	priv := rsaKey(t)
	// string aud
	claims := validClaims()
	claims.Audience = []string{}
	// craft raw token with aud as string
	header := map[string]string{"alg": "RS256", "typ": "JWT", "kid": "k1"}
	hb, _ := json.Marshal(header)
	// manual claims with aud string
	rawClaims := map[string]interface{}{
		"iss":   "https://issuer.example.com",
		"sub":   "user1",
		"aud":   "client123",
		"exp":   time.Now().Add(time.Hour).Unix(),
		"nonce": "nonce123",
	}
	cb, _ := json.Marshal(rawClaims)
	hEnc := base64.RawURLEncoding.EncodeToString(hb)
	cEnc := base64.RawURLEncoding.EncodeToString(cb)
	signing := hEnc + "." + cEnc
	h := crypto.SHA256.New()
	h.Write([]byte(signing))
	digest := h.Sum(nil)
	sig, _ := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, digest)
	tok := signing + "." + base64.RawURLEncoding.EncodeToString(sig)
	cache := newCache("k1", &priv.PublicKey)
	v := NewVerifier(cfg, cache)
	c, err := v.Verify(context.Background(), tok)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Audience) != 1 || c.Audience[0] != "client123" {
		t.Fatal("aud parse failed")
	}
	_ = validClaims()
}

func TestJWT_AudAzpRules(t *testing.T) {
	cfg := testCfg()
	priv := rsaKey(t)
	// multi-aud requires azp
	claims := validClaims()
	claims.Audience = []string{"client123", "other"}
	claims.AuthorizedParty = "client123"
	tok := makeRSAJWT(t, "RS256", claims, priv, "k1")
	cache := newCache("k1", &priv.PublicKey)
	v := NewVerifier(cfg, cache)
	if _, err := v.Verify(context.Background(), tok); err != nil {
		t.Fatal(err)
	}
	// azp mismatch
	claims2 := validClaims()
	claims2.Audience = []string{"client123", "other"}
	claims2.AuthorizedParty = "evil"
	tok2 := makeRSAJWT(t, "RS256", claims2, priv, "k1")
	if _, err := v.Verify(context.Background(), tok2); err == nil {
		t.Fatal("expected azp mismatch")
	}
}

func TestJWT_RejectsCritHeader(t *testing.T) {
	cfg := testCfg()
	priv := rsaKey(t)
	header := map[string]interface{}{"alg": "RS256", "typ": "JWT", "kid": "k1", "crit": []string{"exp"}}
	hb, _ := json.Marshal(header)
	cb, _ := json.Marshal(validClaims())
	hEnc := base64.RawURLEncoding.EncodeToString(hb)
	cEnc := base64.RawURLEncoding.EncodeToString(cb)
	signing := hEnc + "." + cEnc
	h := crypto.SHA256.New()
	h.Write([]byte(signing))
	digest := h.Sum(nil)
	sig, _ := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, digest)
	tok := signing + "." + base64.RawURLEncoding.EncodeToString(sig)
	cache := newCache("k1", &priv.PublicKey)
	v := NewVerifier(cfg, cache)
	if _, err := v.Verify(context.Background(), tok); err == nil {
		t.Fatal("expected crit rejection")
	}
}

func TestJWT_RejectsB64False(t *testing.T) {
	b64false := false
	cfg := testCfg()
	priv := rsaKey(t)
	header := map[string]interface{}{"alg": "RS256", "typ": "JWT", "kid": "k1", "b64": b64false}
	hb, _ := json.Marshal(header)
	cb, _ := json.Marshal(validClaims())
	hEnc := base64.RawURLEncoding.EncodeToString(hb)
	cEnc := base64.RawURLEncoding.EncodeToString(cb)
	signing := hEnc + "." + cEnc
	h := crypto.SHA256.New()
	h.Write([]byte(signing))
	digest := h.Sum(nil)
	sig, _ := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, digest)
	tok := signing + "." + base64.RawURLEncoding.EncodeToString(sig)
	cache := newCache("k1", &priv.PublicKey)
	v := NewVerifier(cfg, cache)
	if _, err := v.Verify(context.Background(), tok); err == nil {
		t.Fatal("expected b64 false rejection")
	}
}

func TestJWT_RejectsAtJwtInIdTokenPath(t *testing.T) {
	cfg := testCfg()
	priv := rsaKey(t)
	header := map[string]string{"alg": "RS256", "typ": "at+jwt", "kid": "k1"}
	hb, _ := json.Marshal(header)
	cb, _ := json.Marshal(validClaims())
	hEnc := base64.RawURLEncoding.EncodeToString(hb)
	cEnc := base64.RawURLEncoding.EncodeToString(cb)
	signing := hEnc + "." + cEnc
	h := crypto.SHA256.New()
	h.Write([]byte(signing))
	digest := h.Sum(nil)
	sig, _ := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, digest)
	tok := signing + "." + base64.RawURLEncoding.EncodeToString(sig)
	cache := newCache("k1", &priv.PublicKey)
	v := NewVerifier(cfg, cache)
	if _, err := v.Verify(context.Background(), tok); err == nil {
		t.Fatal("expected rejection")
	}
	// JWT typ accepted
	header2 := map[string]string{"alg": "RS256", "typ": "JWT", "kid": "k1"}
	hb2, _ := json.Marshal(header2)
	hEnc2 := base64.RawURLEncoding.EncodeToString(hb2)
	signing2 := hEnc2 + "." + cEnc
	h2 := crypto.SHA256.New()
	h2.Write([]byte(signing2))
	digest2 := h2.Sum(nil)
	sig2, _ := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, digest2)
	tok2 := signing2 + "." + base64.RawURLEncoding.EncodeToString(sig2)
	if _, err := v.Verify(context.Background(), tok2); err != nil {
		t.Fatal(err)
	}
}

func TestJWT_RejectsJWKAlgMismatch(t *testing.T) {
	jwk := JWK{Kty: "RSA", Kid: "k", Use: "sig", Alg: "RS384", N: "AQAB", E: "AQAB"}
	_, err := ParseJWK(jwk, map[string]struct{}{"RS256": {}})
	if err == nil {
		t.Fatal("expected rejection for RS384 when only RS256 allowed")
	}
}

func TestJWT_RejectsKeyOpsWithoutVerify(t *testing.T) {
	jwk := JWK{Kty: "RSA", Kid: "k", Use: "sig", Alg: "RS256", N: "AQAB", E: "AQAB", KeyOps: []string{"sign"}}
	_, err := ParseJWK(jwk, map[string]struct{}{"RS256": {}})
	if err == nil {
		t.Fatal("expected key_ops rejection")
	}
}

func TestJWT_RejectsBadSignature(t *testing.T) {
	cfg := testCfg()
	priv := rsaKey(t)
	tok := makeRSAJWT(t, "RS256", validClaims(), priv, "k1")
	// corrupt signature
	parts := splitToken(tok)
	parts[2] = parts[2] + "AAAA"
	cache := newCache("k1", &priv.PublicKey)
	v := NewVerifier(cfg, cache)
	if _, err := v.Verify(context.Background(), joinToken(parts)); err == nil {
		t.Fatal("expected bad signature")
	}
}

func TestJWT_RejectsES256WithDERSignature(t *testing.T) {
	cfg := testCfg()
	priv := ecKey(t, elliptic.P256())
	// build a DER signature
	header := map[string]string{"alg": "ES256", "typ": "JWT", "kid": "k1"}
	hb, _ := json.Marshal(header)
	cb, _ := json.Marshal(validClaims())
	hEnc := base64.RawURLEncoding.EncodeToString(hb)
	cEnc := base64.RawURLEncoding.EncodeToString(cb)
	signing := hEnc + "." + cEnc
	h := crypto.SHA256.New()
	h.Write([]byte(signing))
	digest := h.Sum(nil)
	derSig, _ := ecdsa.SignASN1(rand.Reader, priv, digest)
	tok := signing + "." + base64.RawURLEncoding.EncodeToString(derSig)
	cache := newCache("k1", &priv.PublicKey)
	v := NewVerifier(cfg, cache)
	if _, err := v.Verify(context.Background(), tok); err == nil {
		t.Fatal("expected rejection of DER")
	}
}

// W2: a JWK whose declared alg differs from the token header alg must be
// rejected on the VERIFY path — the pin is enforced when the key is used to
// verify, not only when the key set is parsed.
func TestJWT_VerifyRejectsJWKAlgMismatchAgainstTokenHeader(t *testing.T) {
	cfg := testCfg()
	priv := rsaKey(t)
	// the key set declares RS512 …
	srv512 := jwksServerFor(t, priv, "k1", "RS512")
	defer srv512.Close()
	// … while the token header declares RS256. The signature itself is valid
	// for RS256, so ONLY the alg pin can reject this token.
	tok := makeRSAJWT(t, "RS256", validClaims(), priv, "k1")
	cache512 := NewJWKSCache(time.Hour, time.Minute, time.Hour, func(ctx context.Context) (map[string]crypto.PublicKey, map[string]string, error) {
		return fetchJWKSSet(ctx, srv512.Client(), srv512.URL+"/jwks", cfg.AllowedAlgs)
	})
	v512 := NewVerifier(cfg, cache512)
	_, err := v512.Verify(context.Background(), tok)
	if err == nil {
		t.Fatal("expected the verify path to reject the jwk alg mismatch")
	}
	if !strings.Contains(err.Error(), "alg mismatch") {
		t.Fatalf("unexpected error: %v", err)
	}
	// control: the same token verifies when the key declares RS256.
	srv256 := jwksServerFor(t, priv, "k1", "RS256")
	defer srv256.Close()
	cache256 := NewJWKSCache(time.Hour, time.Minute, time.Hour, func(ctx context.Context) (map[string]crypto.PublicKey, map[string]string, error) {
		return fetchJWKSSet(ctx, srv256.Client(), srv256.URL+"/jwks", cfg.AllowedAlgs)
	})
	v256 := NewVerifier(cfg, cache256)
	if _, err := v256.Verify(context.Background(), tok); err != nil {
		t.Fatalf("a matching alg must verify: %v", err)
	}
}

// jwksServerFor serves a one-key JWKS document with the given declared alg.
func jwksServerFor(t *testing.T, priv *rsa.PrivateKey, kid, alg string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := priv.N.Bytes()
		e := []byte{0x01, 0x00, 0x01}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]string{{
				"kty": "RSA",
				"kid": kid,
				"use": "sig",
				"alg": alg,
				"n":   base64.RawURLEncoding.EncodeToString(n),
				"e":   base64.RawURLEncoding.EncodeToString(e),
			}},
		})
	}))
}

// S1: a token whose nbf lies in the future must be rejected.
func TestJWT_RejectsFutureNotBefore(t *testing.T) {
	cfg := testCfg()
	priv := rsaKey(t)
	claims := validClaims()
	claims.NotBefore = time.Now().Add(2 * time.Hour).Unix()
	tok := makeRSAJWT(t, "RS256", claims, priv, "k1")
	cache := newCache("k1", &priv.PublicKey)
	v := NewVerifier(cfg, cache)
	_, err := v.Verify(context.Background(), tok)
	if err == nil {
		t.Fatal("expected a future nbf to be rejected")
	}
	if !strings.Contains(err.Error(), "not yet valid") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// S1: a single-element aud that is not the client_id must be rejected.
func TestJWT_RejectsSingleElementAudNotClientID(t *testing.T) {
	cfg := testCfg()
	priv := rsaKey(t)
	claims := validClaims()
	claims.Audience = []string{"another-client"}
	tok := makeRSAJWT(t, "RS256", claims, priv, "k1")
	cache := newCache("k1", &priv.PublicKey)
	v := NewVerifier(cfg, cache)
	_, err := v.Verify(context.Background(), tok)
	if err == nil {
		t.Fatal("expected an aud mismatch to be rejected")
	}
	if !strings.Contains(err.Error(), "aud mismatch") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// helpers
func splitToken(tok string) []string { return split(tok, ".") }
func split(s, sep string) []string {
	var out []string
	cur := ""
	for _, c := range s {
		if string(c) == sep {
			out = append(out, cur)
			cur = ""
		} else {
			cur += string(c)
		}
	}
	out = append(out, cur)
	return out
}
func joinToken(parts []string) string {
	return parts[0] + "." + parts[1] + "." + parts[2]
}
