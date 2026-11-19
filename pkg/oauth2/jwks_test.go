package oauth2

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestJWKS_ParsesRSA(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	nB := priv.N.Bytes()
	eB := []byte{0x01, 0x00, 0x01}
	jwk := JWK{Kty: "RSA", Kid: "r1", Use: "sig", Alg: "RS256", N: base64.RawURLEncoding.EncodeToString(nB), E: base64.RawURLEncoding.EncodeToString(eB)}
	key, err := ParseJWK(jwk, map[string]struct{}{"RS256": {}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := key.(*rsa.PublicKey); !ok {
		t.Fatal("not rsa")
	}
}

func TestJWKS_ParsesEC(t *testing.T) {
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	jwk := JWK{Kty: "EC", Kid: "e1", Use: "sig", Alg: "ES256", Crv: "P-256", X: base64.RawURLEncoding.EncodeToString(priv.X.Bytes()), Y: base64.RawURLEncoding.EncodeToString(priv.Y.Bytes())}
	key, err := ParseJWK(jwk, map[string]struct{}{"ES256": {}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := key.(*ecdsa.PublicKey); !ok {
		t.Fatal("not ec")
	}
}

func TestJWKS_ParsesOKP(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	jwk := JWK{Kty: "OKP", Kid: "o1", Use: "sig", Alg: "EdDSA", Crv: "Ed25519", X: base64.RawURLEncoding.EncodeToString(pub)}
	key, err := ParseJWK(jwk, map[string]struct{}{"EdDSA": {}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := key.(ed25519.PublicKey); !ok {
		t.Fatal("not okp")
	}
}

func TestJWKS_PinsKtyAlgCrv(t *testing.T) {
	jwk := JWK{Kty: "RSA", Kid: "x", Use: "sig", Alg: "ES256", N: "AAAA", E: "AQAB"}
	if _, err := ParseJWK(jwk, map[string]struct{}{"ES256": {}, "RS256": {}}); err == nil {
		t.Fatal("expected error")
	}
}

func TestJWKS_KidMissTriggersRefresh(t *testing.T) {
	calls := 0
	cache := NewJWKSCache(time.Hour, time.Minute, time.Hour, func(ctx context.Context) (map[string]crypto.PublicKey, map[string]string, error) {
		calls++
		return map[string]crypto.PublicKey{"k2": &rsa.PublicKey{E: 65537}}, nil, nil
	})
	pub, err := cache.Key(context.Background(), "k2", "RS256")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if pub == nil {
		t.Fatal("nil key")
	}
	if calls != 1 {
		t.Fatalf("expected 1 refresh, got %d", calls)
	}
}

func TestJWKS_SingleFlightCollapsesConcurrentMisses(t *testing.T) {
	calls := 0
	var mu sync.Mutex
	cache := NewJWKSCache(time.Hour, time.Minute, time.Hour, func(ctx context.Context) (map[string]crypto.PublicKey, map[string]string, error) {
		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		calls++
		mu.Unlock()
		return map[string]crypto.PublicKey{"k1": &rsa.PublicKey{E: 65537}}, nil, nil
	})
	done := make(chan struct{})
	go func() { cache.Key(context.Background(), "k2", "RS256"); done <- struct{}{} }()
	go func() { cache.Key(context.Background(), "k2", "RS256"); done <- struct{}{} }()
	<-done
	<-done
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("expected 1 fetch, got %d", calls)
	}
}

func TestJWKS_SingleFlightRearmsAfterCompletion(t *testing.T) {
	calls := 0
	cache := NewJWKSCache(time.Hour, time.Minute, time.Hour, func(ctx context.Context) (map[string]crypto.PublicKey, map[string]string, error) {
		calls++
		return map[string]crypto.PublicKey{"k1": &rsa.PublicKey{E: 65537}}, nil, nil
	})
	// first miss triggers refresh
	cache.Key(context.Background(), "k2", "RS256")
	// advance internal refresh clock past minRefresh
	cache.mu.Lock()
	cache.lastRefresh = time.Now().Add(-2 * time.Minute)
	cache.mu.Unlock()
	// second miss triggers another refresh
	cache.Key(context.Background(), "k3", "RS256")
	if calls != 2 {
		t.Fatalf("expected 2 fetches across two cycles, got %d", calls)
	}
}

func TestJWKS_TTLAndKidMissShareOneKey(t *testing.T) {
	calls := 0
	cache := NewJWKSCache(time.Hour, time.Minute, time.Hour, func(ctx context.Context) (map[string]crypto.PublicKey, map[string]string, error) {
		calls++
		return map[string]crypto.PublicKey{"k1": &rsa.PublicKey{E: 65537}}, nil, nil
	})
	if _, err := cache.Key(context.Background(), "k1", "RS256"); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Key(context.Background(), "k1", "RS256"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("expected one shared refresh, got %d", calls)
	}
}

func TestJWKS_RefreshIsRateLimited(t *testing.T) {
	cache := NewJWKSCache(time.Hour, 10*time.Minute, time.Hour, func(ctx context.Context) (map[string]crypto.PublicKey, map[string]string, error) {
		return map[string]crypto.PublicKey{}, nil, nil
	})
	cache.lastRefresh = time.Now()
	_, err := cache.Key(context.Background(), "missing", "RS256")
	if err != ErrJWKSRateLimited {
		t.Fatalf("expected rate limit, got: %v", err)
	}
}

func TestJWKS_MissWaitersBlockOnInFlightRefresh(t *testing.T) {
	calls := 0
	cache := NewJWKSCache(time.Hour, time.Minute, time.Hour, func(ctx context.Context) (map[string]crypto.PublicKey, map[string]string, error) {
		time.Sleep(20 * time.Millisecond)
		calls++
		return map[string]crypto.PublicKey{"k1": &rsa.PublicKey{E: 65537}}, nil, nil
	})
	done := make(chan struct{}, 2)
	go func() { cache.Key(context.Background(), "k2", "RS256"); done <- struct{}{} }()
	time.Sleep(5 * time.Millisecond)
	go func() { cache.Key(context.Background(), "k2", "RS256"); done <- struct{}{} }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("deadlock")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("deadlock")
	}
	if calls < 1 {
		t.Fatalf("expected at least 1 refresh, got %d", calls)
	}
}

func TestJWKS_StaleCacheSurvivesFetchFailure(t *testing.T) {
	cache := NewJWKSCache(time.Hour, time.Minute, time.Hour, func(ctx context.Context) (map[string]crypto.PublicKey, map[string]string, error) {
		return nil, nil, context.DeadlineExceeded
	})
	cache.keys = map[string]crypto.PublicKey{"k1": &rsa.PublicKey{E: 65537}}
	cache.fetchedAt = time.Now().Add(-2 * time.Hour)
	cache.lastRefresh = time.Now().Add(-2 * time.Hour)
	// grace covers
	cache.grace = 4 * time.Hour
	_, err := cache.Key(context.Background(), "k1", "RS256")
	if err != nil {
		t.Fatalf("expected stale grace to serve, got: %v", err)
	}
}

func TestJWKS_RotatedAwayKidInvalidatesOldTokens(t *testing.T) {
	cache := NewJWKSCache(time.Hour, time.Minute, time.Hour, func(ctx context.Context) (map[string]crypto.PublicKey, map[string]string, error) {
		return map[string]crypto.PublicKey{"k2": &rsa.PublicKey{E: 65537}}, nil, nil
	})
	cache.keys = map[string]crypto.PublicKey{"k1": &rsa.PublicKey{E: 65537}}
	cache.fetchedAt = time.Now().Add(-2 * time.Hour)
	cache.lastRefresh = time.Now().Add(-2 * time.Hour)
	_, err := cache.Key(context.Background(), "k1", "RS256")
	if err == nil {
		t.Fatal("expected unknown kid after refresh")
	}
}

func TestJWKS_RejectsSmallRSAExponent(t *testing.T) {
	jwk := JWK{Kty: "RSA", Kid: "r", Use: "sig", Alg: "RS256", N: base64.RawURLEncoding.EncodeToString([]byte{1}), E: base64.RawURLEncoding.EncodeToString([]byte{2})}
	if _, err := ParseJWK(jwk, map[string]struct{}{"RS256": {}}); err == nil {
		t.Fatal("expected rejection small exponent")
	}
}

// S2: an RSA key whose modulus is below 2048 bits must be rejected — a
// 1024-bit modulus is far too weak to protect a session cookie.
func TestJWKS_RejectsSmallRSAModulus(t *testing.T) {
	// a 1024-bit modulus (128 bytes with the top bit set) — no key generation
	// is needed, ParseJWK only parses n and e.
	nBytes := make([]byte, 128)
	if _, err := rand.Read(nBytes); err != nil {
		t.Fatal(err)
	}
	nBytes[0] |= 0x80
	jwk := JWK{Kty: "RSA", Kid: "small", Use: "sig", Alg: "RS256", N: base64.RawURLEncoding.EncodeToString(nBytes), E: base64.RawURLEncoding.EncodeToString([]byte{0x01, 0x00, 0x01})}
	_, err := ParseJWK(jwk, map[string]struct{}{"RS256": {}})
	if err == nil {
		t.Fatal("expected a 1024-bit modulus to be rejected")
	}
	if !strings.Contains(err.Error(), "modulus too small") {
		t.Fatalf("unexpected error: %v", err)
	}
	// control: a 2048-bit modulus is accepted.
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ok := JWK{Kty: "RSA", Kid: "ok", Use: "sig", Alg: "RS256", N: base64.RawURLEncoding.EncodeToString(priv.N.Bytes()), E: base64.RawURLEncoding.EncodeToString([]byte{0x01, 0x00, 0x01})}
	if _, err := ParseJWK(ok, map[string]struct{}{"RS256": {}}); err != nil {
		t.Fatalf("a 2048-bit modulus must be accepted: %v", err)
	}
}

// W1: a JWKS endpoint answering with a non-200 status must fail cleanly —
// the error body (an HTML page) must never reach json.Unmarshal.
func TestJWKS_FetchRejectsNonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("<html><body>internal error</body></html>"))
	}))
	defer srv.Close()
	_, _, err := fetchJWKSSet(context.Background(), srv.Client(), srv.URL+"/jwks", map[string]struct{}{"RS256": {}})
	if err == nil {
		t.Fatal("expected an error for a non-200 jwks response")
	}
	if !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// W1: a JWKS body larger than the cap must be rejected instead of buffered
// into memory (a hostile IdP must not be able to exhaust the proxy).
func TestJWKS_FetchRejectsOversizedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"keys":[`))
		w.Write(bytes.Repeat([]byte("x"), maxJWKSBodyBytes+4096))
		w.Write([]byte(`]}`))
	}))
	defer srv.Close()
	_, _, err := fetchJWKSSet(context.Background(), srv.Client(), srv.URL+"/jwks", map[string]struct{}{"RS256": {}})
	if err == nil {
		t.Fatal("expected an error for an oversized jwks response")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// overflowBody yields data and then fails — it models an endless/massive JWKS
// body. A bounded read must stop at the cap and never reach the end of the
// stream.
type overflowBody struct {
	remaining int
	err       error
}

func (b *overflowBody) Read(p []byte) (int, error) {
	if b.remaining <= 0 {
		return 0, b.err
	}
	n := len(p)
	if n > b.remaining {
		n = b.remaining
	}
	for i := 0; i < n; i++ {
		p[i] = 'x'
	}
	b.remaining -= n
	return n, nil
}

func (b *overflowBody) Close() error { return nil }

// staticTripper serves one canned response.
type staticTripper struct{ resp *http.Response }

func (t *staticTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	t.resp.Request = r
	return t.resp, nil
}

// W1: the JWKS body read is BOUNDED — the proxy must stop reading at the cap
// instead of buffering whatever a hostile IdP sends.
func TestJWKS_FetchStopsReadingAtTheBodyCap(t *testing.T) {
	body := &overflowBody{remaining: 8 * maxJWKSBodyBytes, err: errors.New("stream ended")}
	client := &http.Client{Transport: &staticTripper{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       body,
	}}}
	_, _, err := fetchJWKSSet(context.Background(), client, "https://idp.example.com/jwks", map[string]struct{}{"RS256": {}})
	if err == nil {
		t.Fatal("expected an error for an oversized jwks response")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("the body read was not bounded at the cap: %v", err)
	}
}

// W1 (end to end): the guard's JWKS fetch refuses a non-200 response and the
// verify path reports it — no panic, no unbounded read.
func TestJWKS_GuardFetchFailsCleanlyOnIdPError(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Write([]byte(`{"issuer":"` + srv.URL + `","authorization_endpoint":"` + srv.URL + `/auth","token_endpoint":"` + srv.URL + `/token","jwks_uri":"` + srv.URL + `/jwks"}`))
		case "/jwks":
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("<html>boom</html>"))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	cfg := &Config{
		Enabled:          true,
		IssuerURL:        srv.URL,
		ClientID:         "cid",
		ExternalURL:      "https://app.example.com",
		RedirectURL:      srv.URL + "/oauth2/callback",
		CookieName:       "sess",
		TxnCookieName:    "txn",
		CookieSecure:     true,
		CookieSameSite:   http.SameSiteLaxMode,
		SessionTTL:       time.Hour,
		EmailDomains:     []string{"*"},
		AllowAnyDomain:   true,
		AllowedAlgs:      map[string]struct{}{"RS256": {}},
		StateStoreMax:    4,
		JWKSCacheTTL:     time.Hour,
		DiscoveryTimeout: 5 * time.Second,
	}
	root := make([]byte, 32)
	for i := range root {
		root[i] = byte(i + 1)
	}
	cfg.rootKey = root
	g, err := NewGuard(cfg)
	if err != nil {
		t.Fatal(err)
	}
	hb, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "missing-kid", "typ": "JWT"})
	tok := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString([]byte("{}")) + ".AAAA"
	_, err = g.handler.verifier.Verify(context.Background(), tok)
	if err == nil {
		t.Fatal("expected the jwks fetch to fail")
	}
	if !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// #9: a JWKS document that yields NO usable key is a DISTINCT, non-nil,
// stable error — not a successful fetch of an empty key set. The caller must
// be able to tell "the IdP published no usable key" from "the requested kid
// is unknown".
func TestJWKS_FetchRejectsEmptyKeySet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"keys":[]}`))
	}))
	defer srv.Close()
	keys, algs, err := fetchJWKSSet(context.Background(), srv.Client(), srv.URL+"/jwks", map[string]struct{}{"RS256": {}})
	if err == nil {
		t.Fatal("an empty key set must be an error, not a successful fetch")
	}
	if !errors.Is(err, ErrJWKSEmptyKeySet) {
		t.Fatalf("expected ErrJWKSEmptyKeySet, got: %v", err)
	}
	if keys != nil || algs != nil {
		t.Fatal("the empty key set must not return keys")
	}
}

// #9: a document whose keys are ALL unusable (an unknown key type) is the
// same distinct failure — the IdP served a document this proxy can use no
// key from.
func TestJWKS_FetchRejectsUnusableKeySet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"keys":[{"kty":"XYZ","kid":"k1","use":"sig","alg":"RS256"}]}`))
	}))
	defer srv.Close()
	_, _, err := fetchJWKSSet(context.Background(), srv.Client(), srv.URL+"/jwks", map[string]struct{}{"RS256": {}})
	if !errors.Is(err, ErrJWKSEmptyKeySet) {
		t.Fatalf("expected ErrJWKSEmptyKeySet, got: %v", err)
	}
}

// #9: the sentinel must be distinct from the other two JWKS failures — an
// empty key set is neither an unknown kid nor a rate limit.
func TestJWKS_EmptyKeySetSentinelIsDistinct(t *testing.T) {
	if errors.Is(ErrJWKSEmptyKeySet, ErrJWKSUnknownKid) {
		t.Fatal("the empty-keyset sentinel must not match the unknown-kid failure")
	}
	if errors.Is(ErrJWKSEmptyKeySet, ErrJWKSRateLimited) {
		t.Fatal("the empty-keyset sentinel must not match the rate-limit failure")
	}
	if ErrJWKSEmptyKeySet == nil || ErrJWKSEmptyKeySet.Error() == "" {
		t.Fatal("the empty-keyset sentinel must be a non-nil, stable error")
	}
}
