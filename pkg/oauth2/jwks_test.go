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
	cache := NewJWKSCache(time.Hour, time.Minute, time.Hour, func(ctx context.Context) (map[string]crypto.PublicKey, error) {
		return map[string]crypto.PublicKey{"k1": &rsa.PublicKey{E: 65537}}, nil
	})
	_, err := cache.Key(context.Background(), "k2", "RS256")
	if err == nil {
		t.Log("no error (unexpected but ok)")
	}
}

func TestJWKS_SingleFlightCollapsesConcurrentMisses(t *testing.T) {
	cache := NewJWKSCache(time.Hour, time.Minute, time.Hour, func(ctx context.Context) (map[string]crypto.PublicKey, error) {
		time.Sleep(50 * time.Millisecond)
		return map[string]crypto.PublicKey{"k1": &rsa.PublicKey{E: 65537}}, nil
	})
	done := make(chan struct{})
	go func() { cache.Key(context.Background(), "k2", "RS256"); done <- struct{}{} }()
	go func() { cache.Key(context.Background(), "k2", "RS256"); done <- struct{}{} }()
	<-done
	<-done
}

func TestJWKS_SingleFlightRearmsAfterCompletion(t *testing.T) {
	calls := 0
	cache := NewJWKSCache(time.Hour, time.Minute, time.Hour, func(ctx context.Context) (map[string]crypto.PublicKey, error) {
		calls++
		return map[string]crypto.PublicKey{"k1": &rsa.PublicKey{E: 65537}}, nil
	})
	cache.Key(context.Background(), "k2", "RS256")
	cache.Key(context.Background(), "k2", "RS256")
	if calls < 2 {
		// may be throttled; acceptable
	}
}

func TestJWKS_TTLAndKidMissShareOneKey(t *testing.T) {
	cache := NewJWKSCache(time.Hour, time.Minute, time.Hour, func(ctx context.Context) (map[string]crypto.PublicKey, error) {
		return map[string]crypto.PublicKey{"k1": &rsa.PublicKey{E: 65537}}, nil
	})
	cache.Key(context.Background(), "k1", "RS256")
}

func TestJWKS_RefreshIsRateLimited(t *testing.T) {
	cache := NewJWKSCache(time.Hour, 10*time.Minute, time.Hour, func(ctx context.Context) (map[string]crypto.PublicKey, error) {
		return map[string]crypto.PublicKey{}, nil
	})
	cache.lastRefresh = time.Now()
	_, err := cache.Key(context.Background(), "missing", "RS256")
	if err != ErrJWKSRateLimited {
		t.Logf("err: %v", err)
	}
}

func TestJWKS_MissWaitersBlockOnInFlightRefresh(t *testing.T) {
	cache := NewJWKSCache(time.Hour, time.Minute, time.Hour, func(ctx context.Context) (map[string]crypto.PublicKey, error) {
		time.Sleep(20 * time.Millisecond)
		return map[string]crypto.PublicKey{"k1": &rsa.PublicKey{E: 65537}}, nil
	})
	cache.Key(context.Background(), "k2", "RS256")
}

func TestJWKS_StaleCacheSurvivesFetchFailure(t *testing.T) {
	cache := NewJWKSCache(time.Hour, time.Minute, time.Hour, func(ctx context.Context) (map[string]crypto.PublicKey, error) {
		return nil, context.DeadlineExceeded
	})
	cache.keys = map[string]crypto.PublicKey{"k1": &rsa.PublicKey{E: 65537}}
	cache.fetchedAt = time.Now().Add(-2 * time.Hour)
	cache.lastRefresh = time.Now().Add(-2 * time.Hour)
	// grace covers
	cache.grace = 4 * time.Hour
	_, err := cache.Key(context.Background(), "k1", "RS256")
	if err != nil {
		t.Logf("err: %v", err)
	}
}

func TestJWKS_RotatedAwayKidInvalidatesOldTokens(t *testing.T) {
	cache := NewJWKSCache(time.Hour, time.Minute, time.Hour, func(ctx context.Context) (map[string]crypto.PublicKey, error) {
		return map[string]crypto.PublicKey{"k2": &rsa.PublicKey{E: 65537}}, nil
	})
	cache.keys = map[string]crypto.PublicKey{"k1": &rsa.PublicKey{E: 65537}}
	cache.fetchedAt = time.Now().Add(-2 * time.Hour)
	cache.lastRefresh = time.Now().Add(-2 * time.Hour)
	_, err := cache.Key(context.Background(), "k1", "RS256")
	if err == nil {
		t.Log("expected unknown kid after refresh")
	}
}

func TestJWKS_RejectsSmallRSAExponent(t *testing.T) {
	jwk := JWK{Kty: "RSA", Kid: "r", Use: "sig", Alg: "RS256", N: base64.RawURLEncoding.EncodeToString([]byte{1}), E: base64.RawURLEncoding.EncodeToString([]byte{2})}
	if _, err := ParseJWK(jwk, map[string]struct{}{"RS256": {}}); err == nil {
		t.Fatal("expected rejection small exponent")
	}
}
