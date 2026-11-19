package oauth2

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"fmt"
	"math/big"
	"sync"
	"time"
)

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
	fetch       func(ctx context.Context) (map[string]crypto.PublicKey, error)
}

func NewJWKSCache(ttl, minRefresh, grace time.Duration, fetch func(ctx context.Context) (map[string]crypto.PublicKey, error)) *JWKSCache {
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

func (c *JWKSCache) Key(ctx context.Context, kid string, alg string) (crypto.PublicKey, error) {
	c.mu.RLock()
	pub, ok := c.keys[kid]
	fresh := time.Since(c.fetchedAt) < c.ttl
	c.mu.RUnlock()
	if ok && fresh {
		return pub, nil
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
			keys, err := c.fetch(ctx)
			if err != nil {
				return err
			}
			c.mu.Lock()
			c.keys = keys
			c.fetchedAt = time.Now()
			c.mu.Unlock()
			return nil
		})
		if err != nil {
			c.mu.RLock()
			if c.keys != nil && time.Since(c.fetchedAt) < c.grace {
				if p, ok := c.keys[kid]; ok {
					c.mu.RUnlock()
					return p, nil
				}
			}
			c.mu.RUnlock()
			return nil, err
		}
	}

	c.mu.RLock()
	pub, ok = c.keys[kid]
	c.mu.RUnlock()
	if !ok {
		return nil, ErrJWKSUnknownKid
	}
	_ = alg
	return pub, nil
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
