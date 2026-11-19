package oauth2

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"unicode/utf8"
)

const MaxCookieBytes = 4096

// maxCookieBytes is the runtime threshold used by Seal/Open; defaults to
// MaxCookieBytes and is overridable in tests to prove the budget check.
var maxCookieBytes = MaxCookieBytes

const (
	MaxSubBytes   = 64
	MaxEmailBytes = 254
	MaxNameBytes  = 128
)

type Session struct {
	Sub       string `json:"sub"`
	Email     string `json:"email,omitempty"`
	Name      string `json:"name,omitempty"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
}

type CookieCodec struct {
	name     string
	key      []byte
	secure   bool
	sameSite http.SameSite
	path     string
	maxAge   int
}

func (c *CookieCodec) Seal(s *Session) (string, error) {
	if s == nil {
		return "", errors.New("nil session")
	}
	if len(s.Sub) > MaxSubBytes {
		return "", fmt.Errorf("sub exceeds %d bytes", MaxSubBytes)
	}
	if len(s.Email) > MaxEmailBytes {
		return "", fmt.Errorf("email exceeds %d bytes", MaxEmailBytes)
	}
	if len(s.Name) > MaxNameBytes {
		s.Name = truncateToBytes(s.Name, MaxNameBytes)
	}
	if s.ExpiresAt == 0 {
		// default? leave as is
	}
	b, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCMWithNonceSize(block, 16)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nil, nonce, b, []byte(c.name))
	out := append(nonce, ct...)
	enc := base64.RawURLEncoding.EncodeToString(out)
	if len(enc) > maxCookieBytes {
		return "", fmt.Errorf("sealed cookie exceeds %d bytes", MaxCookieBytes)
	}
	return enc, nil
}

func (c *CookieCodec) Open(value string) (*Session, error) {
	if value == "" {
		return nil, errors.New("empty cookie")
	}
	if len(value) > maxCookieBytes {
		return nil, errors.New("cookie too large")
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		// try standard url encoding
		raw, err = base64.URLEncoding.DecodeString(value)
		if err != nil {
			return nil, err
		}
	}
	if len(raw) < 16+1 {
		return nil, errors.New("cookie too short")
	}
	nonce := raw[:16]
	ct := raw[16:]
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCMWithNonceSize(block, 16)
	if err != nil {
		return nil, err
	}
	pt, err := gcm.Open(nil, nonce, ct, []byte(c.name))
	if err != nil {
		return nil, err
	}
	var s Session
	if err := json.Unmarshal(pt, &s); err != nil {
		return nil, err
	}
	if len(s.Sub) > MaxSubBytes {
		return nil, errors.New("sub exceeds cap")
	}
	if len(s.Email) > MaxEmailBytes {
		return nil, errors.New("email exceeds cap")
	}
	if len(s.Name) > MaxNameBytes {
		return nil, errors.New("name exceeds cap")
	}
	if s.ExpiresAt > 0 && time.Now().Unix() > s.ExpiresAt {
		return nil, errors.New("cookie expired")
	}
	return &s, nil
}

func (c *CookieCodec) SetCookie(w http.ResponseWriter, s *Session) {
	val, err := c.Seal(s)
	if err != nil {
		return
	}
	maxAge := c.maxAge
	if maxAge == 0 && s != nil {
		maxAge = int(s.ExpiresAt - time.Now().Unix())
		if maxAge < 0 {
			maxAge = 0
		}
	}
	path := c.path
	if path == "" {
		path = "/"
	}
	http.SetCookie(w, &http.Cookie{
		Name:     c.name,
		Value:    val,
		Path:     path,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   c.secure,
		SameSite: c.sameSite,
	})
}

func (c *CookieCodec) ClearCookie(w http.ResponseWriter) {
	path := c.path
	if path == "" {
		path = "/"
	}
	http.SetCookie(w, &http.Cookie{
		Name:     c.name,
		Value:    "",
		Path:     path,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   c.secure,
		SameSite: c.sameSite,
	})
}

func truncateToBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	b := []byte(s)
	b = b[:n]
	// trim trailing incomplete UTF-8 rune
	for len(b) > 0 {
		r, size := utf8.DecodeLastRune(b)
		if r == utf8.RuneError && size == 1 {
			b = b[:len(b)-1]
			continue
		}
		break
	}
	return string(b)
}
