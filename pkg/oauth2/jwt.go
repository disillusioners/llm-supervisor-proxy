package oauth2

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"time"
)

type Claims struct {
	Issuer          string   `json:"iss"`
	Subject         string   `json:"sub"`
	Audience        []string `json:"-"`
	ExpiresAt       int64    `json:"exp"`
	NotBefore       int64    `json:"nbf"`
	IssuedAt        int64    `json:"iat"`
	Nonce           string   `json:"nonce"`
	Email           string   `json:"email"`
	EmailVerified   *bool    `json:"email_verified"`
	Name            string   `json:"name"`
	TokenType       string   `json:"typ"`
	AuthorizedParty string   `json:"azp"`
}

type claimsWire struct {
	Issuer          string          `json:"iss"`
	Subject         string          `json:"sub"`
	Audience        json.RawMessage `json:"aud"`
	ExpiresAt       int64           `json:"exp"`
	NotBefore       int64           `json:"nbf"`
	IssuedAt        int64           `json:"iat"`
	Nonce           string          `json:"nonce"`
	Email           string          `json:"email"`
	EmailVerified   *bool           `json:"email_verified"`
	Name            string          `json:"name"`
	TokenType       string          `json:"typ"`
	AuthorizedParty string          `json:"azp"`
}

func (c *Claims) UnmarshalJSON(data []byte) error {
	var w claimsWire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	c.Issuer = w.Issuer
	c.Subject = w.Subject
	c.ExpiresAt = w.ExpiresAt
	c.NotBefore = w.NotBefore
	c.IssuedAt = w.IssuedAt
	c.Nonce = w.Nonce
	c.Email = w.Email
	c.EmailVerified = w.EmailVerified
	c.Name = w.Name
	c.TokenType = w.TokenType
	c.AuthorizedParty = w.AuthorizedParty
	if len(w.Audience) == 0 || string(w.Audience) == "null" {
		c.Audience = nil
		return nil
	}
	var s string
	if err := json.Unmarshal(w.Audience, &s); err == nil {
		c.Audience = []string{s}
		return nil
	}
	var arr []string
	if err := json.Unmarshal(w.Audience, &arr); err != nil {
		return err
	}
	c.Audience = arr
	return nil
}

func (c Claims) MarshalJSON() ([]byte, error) {
	type alias struct {
		Issuer          string      `json:"iss"`
		Subject         string      `json:"sub"`
		Audience        interface{} `json:"aud,omitempty"`
		ExpiresAt       int64       `json:"exp"`
		NotBefore       int64       `json:"nbf,omitempty"`
		IssuedAt        int64       `json:"iat,omitempty"`
		Nonce           string      `json:"nonce,omitempty"`
		Email           string      `json:"email,omitempty"`
		EmailVerified   *bool       `json:"email_verified,omitempty"`
		Name            string      `json:"name,omitempty"`
		TokenType       string      `json:"typ,omitempty"`
		AuthorizedParty string      `json:"azp,omitempty"`
	}
	a := alias{
		Issuer: c.Issuer, Subject: c.Subject, ExpiresAt: c.ExpiresAt,
		NotBefore: c.NotBefore, IssuedAt: c.IssuedAt, Nonce: c.Nonce,
		Email: c.Email, EmailVerified: c.EmailVerified, Name: c.Name,
		TokenType: c.TokenType, AuthorizedParty: c.AuthorizedParty,
	}
	if len(c.Audience) == 1 {
		a.Audience = c.Audience[0]
	} else if len(c.Audience) > 1 {
		a.Audience = c.Audience
	}
	return json.Marshal(a)
}

type joseHeader struct {
	Alg  string   `json:"alg"`
	Kid  string   `json:"kid"`
	Typ  string   `json:"typ"`
	Crit []string `json:"crit"`
	B64  *bool    `json:"b64"`
}

type Verifier struct {
	cfg   *Config
	cache *JWKSCache
}

func NewVerifier(cfg *Config, cache *JWKSCache) *Verifier {
	return &Verifier{cfg: cfg, cache: cache}
}

func (v *Verifier) Verify(ctx context.Context, token string) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("invalid token format")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, err
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, err
	}
	var header joseHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, err
	}
	// alg allowlist
	la := strings.ToLower(header.Alg)
	if strings.HasPrefix(la, "hs") || la == "none" {
		return nil, errors.New("disallowed alg")
	}
	if _, ok := v.cfg.AllowedAlgs[header.Alg]; !ok {
		return nil, errors.New("alg not allowed")
	}
	// crit
	if len(header.Crit) > 0 {
		return nil, errors.New("crit header not supported")
	}
	// b64 false
	if header.B64 != nil && !*header.B64 {
		return nil, errors.New("b64 false not supported")
	}
	// typ
	if header.Typ != "" {
		if strings.EqualFold(header.Typ, "JWT") {
			// ok
		} else if strings.EqualFold(header.Typ, "at+jwt") {
			return nil, errors.New("at+jwt not accepted on id-token path")
		} else {
			return nil, errors.New("invalid typ")
		}
	}

	pub, err := v.cache.Key(ctx, header.Kid, header.Alg)
	if err != nil {
		return nil, err
	}
	// W2: the JWK.alg ↔ JOSE header alg pin, enforced on the VERIFY path. A
	// key that declares an algorithm must never verify a token whose header
	// declares a different one (the same predicate the key resolution applies).
	if _, err := checkKeyAlg(header.Kid, header.Alg, v.cache.DeclaredAlg(header.Kid), pub); err != nil {
		return nil, err
	}

	signingInput := parts[0] + "." + parts[1]

	switch {
	case header.Alg == "RS256" || header.Alg == "RS384" || header.Alg == "RS512":
		rsaPub, ok := pub.(*rsa.PublicKey)
		if !ok {
			return nil, errors.New("key type mismatch")
		}
		h := hashFor(header.Alg).New()
		h.Write([]byte(signingInput))
		digest := h.Sum(nil)
		if err := rsa.VerifyPKCS1v15(rsaPub, hashFor(header.Alg), digest, sig); err != nil {
			return nil, fmt.Errorf("bad signature: %w", err)
		}
	case header.Alg == "ES256" || header.Alg == "ES384" || header.Alg == "ES512":
		ecPub, ok := pub.(*ecdsa.PublicKey)
		if !ok {
			return nil, errors.New("key type mismatch")
		}
		keyBytes := (ecPub.Curve.Params().BitSize + 7) / 8
		if len(sig) != 2*keyBytes {
			return nil, errors.New("invalid es signature length")
		}
		n := len(sig) / 2
		r := new(big.Int).SetBytes(sig[:n])
		sv := new(big.Int).SetBytes(sig[n:])
		h := hashFor(header.Alg).New()
		h.Write([]byte(signingInput))
		digest := h.Sum(nil)
		if !ecdsa.Verify(ecPub, digest, r, sv) {
			return nil, errors.New("bad signature")
		}
	case header.Alg == "EdDSA":
		edPub, ok := pub.(ed25519.PublicKey)
		if !ok {
			// maybe pointer
			if p, ok2 := pub.(*ed25519.PublicKey); ok2 {
				edPub = *p
			} else {
				return nil, errors.New("key type mismatch")
			}
		}
		if !ed25519.Verify(edPub, []byte(signingInput), sig) {
			return nil, errors.New("bad signature")
		}
	default:
		return nil, errors.New("unsupported alg")
	}

	// the JWK alg mismatch is rejected above, when the key is resolved for
	// verification (the JWK's declared alg is only known there); key_ops is
	// rejected at parse time.

	var claims Claims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return nil, err
	}

	now := time.Now().Unix()
	if claims.ExpiresAt == 0 {
		return nil, errors.New("missing exp")
	}
	leeway := int64(60)
	if now > claims.ExpiresAt+leeway {
		return nil, errors.New("token expired")
	}
	if claims.NotBefore > 0 && now+leeway < claims.NotBefore {
		return nil, errors.New("token not yet valid")
	}
	if normalizeIssuer(claims.Issuer) != normalizeIssuer(v.cfg.IssuerURL) {
		return nil, errors.New("issuer mismatch")
	}
	if err := validateAudAzp(claims, v.cfg.ClientID); err != nil {
		return nil, err
	}
	return &claims, nil
}

func hashFor(alg string) crypto.Hash {
	switch alg {
	case "RS256", "ES256":
		return crypto.SHA256
	case "RS384", "ES384":
		return crypto.SHA384
	case "RS512", "ES512":
		return crypto.SHA512
	}
	return crypto.SHA256
}

func validateAudAzp(c Claims, clientID string) error {
	if len(c.Audience) == 0 {
		return errors.New("missing aud")
	}
	if len(c.Audience) == 1 {
		if c.Audience[0] != clientID {
			return errors.New("aud mismatch")
		}
	} else {
		if c.AuthorizedParty == "" {
			return errors.New("azp required for multiple aud")
		}
		if c.AuthorizedParty != clientID {
			return errors.New("azp mismatch")
		}
		found := false
		for _, a := range c.Audience {
			if a == clientID {
				found = true
			}
		}
		if !found {
			return errors.New("client not in aud")
		}
	}
	if c.AuthorizedParty != "" && c.AuthorizedParty != clientID {
		return errors.New("azp mismatch")
	}
	return nil
}

func normalizeIssuer(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimRight(s, "/")
	u, err := url.Parse(s)
	if err != nil {
		return strings.ToLower(s)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	host := u.Host
	if (u.Scheme == "https" && len(host) > 4 && host[len(host)-4:] == ":443") ||
		(u.Scheme == "http" && len(host) > 3 && host[len(host)-3:] == ":80") {
		if u.Scheme == "https" {
			host = host[:len(host)-4]
		} else {
			host = host[:len(host)-3]
		}
	}
	u.Host = host
	return strings.TrimRight(u.String(), "/")
}
