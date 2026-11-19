package oauth2

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sync"
	"time"
)

// maxJWKSBodyBytes bounds the JWKS document read from the IdP. A malicious or
// compromised IdP must not be able to OOM the proxy with an unbounded body.
const maxJWKSBodyBytes = 256 * 1024

// minRSAModulusBits is the RSA modulus floor enforced by ParseJWK.
const minRSAModulusBits = 2048

type JWKSet struct {
	Keys []JWK `json:"keys"`
}

type JWK struct {
	Kty    string   `json:"kty"`
	Kid    string   `json:"kid"`
	Use    string   `json:"use"`
	Alg    string   `json:"alg"`
	KeyOps []string `json:"key_ops"`
	N      string   `json:"n"`
	E      string   `json:"e"`
	Crv    string   `json:"crv"`
	X      string   `json:"x"`
	Y      string   `json:"y"`
}

type singleflight struct {
	mu   sync.Mutex
	done chan struct{}
	err  error
}

func (s *singleflight) Do(ctx context.Context, fn func(context.Context) error) error {
	s.mu.Lock()
	if s.done == nil {
		s.done = make(chan struct{})
		s.mu.Unlock()
		err := fn(ctx)
		s.mu.Lock()
		s.err = err
		close(s.done)
		s.done = nil
		s.mu.Unlock()
		return err
	}
	ch := s.done
	s.mu.Unlock()
	select {
	case <-ch:
		s.mu.Lock()
		err := s.err
		s.mu.Unlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

var (
	ErrJWKSRateLimited = fmt.Errorf("jwks refresh rate limited")
	ErrJWKSUnknownKid  = fmt.Errorf("unknown kid")
	// ErrJWKSEmptyKeySet is the distinct error for a JWKS document that
	// carried no usable key: the IdP answered 200 with a document this proxy
	// can use no key from (an empty "keys" array, or only keys this proxy
	// rejects). It is NOT the unknown-kid failure (the document may be fine,
	// the requested kid is simply not in it) and NOT a fetch failure (the
	// document WAS served). The caller can therefore tell "the IdP is
	// misconfigured" apart from "rotate the key".
	ErrJWKSEmptyKeySet = fmt.Errorf("jwks document contains no usable keys")
)

type JWKSCache struct {
	mu          sync.RWMutex
	keys        map[string]crypto.PublicKey
	keyAlg      map[string]string
	fetchedAt   time.Time
	lastRefresh time.Time
	sf          *singleflight
	grace       time.Duration
	ttl         time.Duration
	minRefresh  time.Duration
	fetch       func(ctx context.Context) (map[string]crypto.PublicKey, map[string]string, error)
}

// NewJWKSCache builds a JWKS cache. The fetch function returns the parsed
// keys keyed by kid AND the algorithm each key declares ("" when the JWK
// carries no "alg" member) — the declared alg is what pins a token's header
// alg to the key on the verify path.
func NewJWKSCache(ttl, minRefresh, grace time.Duration, fetch func(ctx context.Context) (map[string]crypto.PublicKey, map[string]string, error)) *JWKSCache {
	return &JWKSCache{
		keys:       map[string]crypto.PublicKey{},
		keyAlg:     map[string]string{},
		sf:         &singleflight{},
		ttl:        ttl,
		minRefresh: minRefresh,
		grace:      grace,
		fetch:      fetch,
	}
}

// DeclaredAlg returns the algorithm the key with the given kid declares
// ("" when the JWK carries no "alg" member).
func (c *JWKSCache) DeclaredAlg(kid string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.keyAlg[kid]
}

// checkKeyAlg enforces the JWK.alg ↔ JOSE header alg pin on the VERIFY path:
// a key that declares an algorithm must never verify a token whose header
// declares a different one. A key with no declared alg is unconstrained (many
// IdPs omit the member) and is accepted.
func checkKeyAlg(kid, headerAlg, declaredAlg string, pub crypto.PublicKey) (crypto.PublicKey, error) {
	if headerAlg != "" && declaredAlg != "" && declaredAlg != headerAlg {
		return nil, fmt.Errorf("jwk alg mismatch: key %q declares %s but the token header declares %s", kid, declaredAlg, headerAlg)
	}
	return pub, nil
}

func (c *JWKSCache) Key(ctx context.Context, kid string, alg string) (crypto.PublicKey, error) {
	c.mu.RLock()
	pub, ok := c.keys[kid]
	declared := c.keyAlg[kid]
	fresh := time.Since(c.fetchedAt) < c.ttl
	c.mu.RUnlock()
	if ok && fresh {
		return checkKeyAlg(kid, alg, declared, pub)
	}

	now := time.Now()
	c.mu.Lock()
	_, present := c.keys[kid]
	tooSoon := now.Sub(c.lastRefresh) < c.minRefresh
	needRefresh := false
	if !present {
		if tooSoon {
			c.mu.Unlock()
			return nil, ErrJWKSRateLimited
		}
		needRefresh = true
	} else {
		if !fresh {
			if c.lastRefresh.IsZero() || !tooSoon {
				needRefresh = true
			}
		}
	}
	c.mu.Unlock()

	if needRefresh {
		c.mu.Lock()
		c.lastRefresh = time.Now()
		c.mu.Unlock()
		err := c.sf.Do(ctx, func(ctx context.Context) error {
			keys, algs, err := c.fetch(ctx)
			if err != nil {
				return err
			}
			// AR-26 amendment: an EMPTY (or wholly unusable) key set is a
			// FETCH ERROR under the stale-grace policy, never a successful
			// rotation: the previous key set must keep serving inside the
			// grace window and the cache is NEVER replaced with an empty set.
			if !usableKeySet(keys) {
				return ErrJWKSEmptyKeySet
			}
			if algs == nil {
				algs = map[string]string{}
			}
			c.mu.Lock()
			c.keys = keys
			c.keyAlg = algs
			c.fetchedAt = time.Now()
			c.mu.Unlock()
			return nil
		})
		if err != nil {
			c.mu.RLock()
			if c.keys != nil && time.Since(c.fetchedAt) < c.grace {
				if p, ok := c.keys[kid]; ok {
					declared := c.keyAlg[kid]
					c.mu.RUnlock()
					return checkKeyAlg(kid, alg, declared, p)
				}
			}
			c.mu.RUnlock()
			return nil, err
		}
	}

	c.mu.RLock()
	pub, ok = c.keys[kid]
	declared = c.keyAlg[kid]
	c.mu.RUnlock()
	if !ok {
		return nil, ErrJWKSUnknownKid
	}
	return checkKeyAlg(kid, alg, declared, pub)
}

// usableKeySet reports whether a fetched key set carries at least one key
// this proxy can actually use. An EMPTY set — or a set whose every entry is
// unusable — is a FETCH ERROR (the stale-grace policy keeps the previous key
// set serving), not a successful rotation that wipes the cache.
func usableKeySet(keys map[string]crypto.PublicKey) bool {
	if len(keys) == 0 {
		return false
	}
	for _, k := range keys {
		if k != nil {
			return true
		}
	}
	return false
}

// fetchJWKSSet downloads and parses a JWKS document. The body read is bounded
// (a hostile IdP must not be able to exhaust memory) and a non-200 response is
// rejected BEFORE the JSON parse, so an HTML error page can never reach
// json.Unmarshal. It returns the parsed keys keyed by kid plus the algorithm
// each key declares.
func fetchJWKSSet(ctx context.Context, client *http.Client, jwksURI string, allowed map[string]struct{}) (map[string]crypto.PublicKey, map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURI, nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("jwks fetch failed: status %d", resp.StatusCode)
	}
	// one byte over the cap is read so an oversized body is DETECTED instead of
	// silently truncated into a confusing parse error.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxJWKSBodyBytes+1))
	if err != nil {
		return nil, nil, err
	}
	if len(body) > maxJWKSBodyBytes {
		return nil, nil, fmt.Errorf("jwks response exceeds %d bytes", maxJWKSBodyBytes)
	}
	var js JWKSet
	if err := json.Unmarshal(body, &js); err != nil {
		return nil, nil, err
	}
	keys := map[string]crypto.PublicKey{}
	algs := map[string]string{}
	for _, j := range js.Keys {
		key, err := ParseJWK(j, allowed)
		if err != nil {
			continue
		}
		kid := j.Kid
		if kid == "" {
			kid = fmt.Sprintf("key-%d", len(keys))
		}
		keys[kid] = key
		if j.Alg != "" {
			algs[kid] = j.Alg
		}
	}
	// a document that yielded NO usable key is a distinct failure, not a
	// successful fetch of an empty key set: the caller must be able to tell
	// "the IdP published no usable key" from "the requested kid is unknown".
	if len(keys) == 0 {
		return nil, nil, ErrJWKSEmptyKeySet
	}
	return keys, algs, nil
}

func ParseJWK(j JWK, allowed map[string]struct{}) (crypto.PublicKey, error) {
	if j.Use != "" && j.Use != "sig" {
		return nil, fmt.Errorf("use not sig")
	}
	if j.KeyOps != nil {
		hasVerify := false
		for _, op := range j.KeyOps {
			if op == "verify" {
				hasVerify = true
			}
		}
		if !hasVerify {
			return nil, fmt.Errorf("key_ops missing verify")
		}
	}
	if j.Alg != "" {
		if _, ok := allowed[j.Alg]; !ok && len(allowed) > 0 {
			return nil, fmt.Errorf("alg not allowed")
		}
	}
	switch j.Kty {
	case "RSA":
		if j.Alg != "" && j.Alg != "RS256" && j.Alg != "RS384" && j.Alg != "RS512" {
			return nil, fmt.Errorf("rsa alg mismatch")
		}
		nBytes, err := base64.RawURLEncoding.DecodeString(j.N)
		if err != nil {
			return nil, err
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(j.E)
		if err != nil {
			return nil, err
		}
		n := new(big.Int).SetBytes(nBytes)
		if n.BitLen() < minRSAModulusBits {
			return nil, fmt.Errorf("rsa modulus too small: %d bits, want >= %d", n.BitLen(), minRSAModulusBits)
		}
		e := 0
		for _, b := range eBytes {
			e = e*256 + int(b)
		}
		if e < 3 || e%2 == 0 {
			return nil, fmt.Errorf("small or even rsa exponent")
		}
		return &rsa.PublicKey{N: n, E: e}, nil
	case "EC":
		var curveName string
		switch j.Crv {
		case "P-256":
			curveName = "P-256"
		case "P-384":
			curveName = "P-384"
		case "P-521":
			curveName = "P-521"
		default:
			return nil, fmt.Errorf("unknown curve")
		}
		if j.Alg != "" {
			expected := ""
			switch j.Crv {
			case "P-256":
				expected = "ES256"
			case "P-384":
				expected = "ES384"
			case "P-521":
				expected = "ES512"
			}
			if expected != "" && j.Alg != expected {
				return nil, fmt.Errorf("crv/alg mismatch")
			}
		}
		xBytes, err := base64.RawURLEncoding.DecodeString(j.X)
		if err != nil {
			return nil, err
		}
		yBytes, err := base64.RawURLEncoding.DecodeString(j.Y)
		if err != nil {
			return nil, err
		}
		pub := &ecdsa.PublicKey{}
		switch curveName {
		case "P-256":
			pub.Curve = elliptic.P256()
		case "P-384":
			pub.Curve = elliptic.P384()
		case "P-521":
			pub.Curve = elliptic.P521()
		}
		pub.X = new(big.Int).SetBytes(xBytes)
		pub.Y = new(big.Int).SetBytes(yBytes)
		return pub, nil
	case "OKP":
		if j.Crv != "Ed25519" {
			return nil, fmt.Errorf("unknown okp curve")
		}
		if j.Alg != "" && j.Alg != "EdDSA" {
			return nil, fmt.Errorf("okp alg mismatch")
		}
		xBytes, err := base64.RawURLEncoding.DecodeString(j.X)
		if err != nil {
			return nil, err
		}
		if len(xBytes) != 32 {
			return nil, fmt.Errorf("invalid ed25519 key length")
		}
		return ed25519.PublicKey(xBytes), nil
	default:
		return nil, fmt.Errorf("unknown kty %s", j.Kty)
	}
}

func (c *JWKSCache) ParseJWK(j JWK, allowed map[string]struct{}) (crypto.PublicKey, error) {
	if allowed == nil {
		allowed = map[string]struct{}{}
	}
	return ParseJWK(j, allowed)
}
