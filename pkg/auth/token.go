package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
)

// Token prefix for easy identification
const TokenPrefix = "sk-"

// Token length (excluding prefix) in bytes - results in 64 hex chars
const TokenLength = 32

var (
	ErrInvalidTokenFormat = errors.New("invalid token format")
	ErrTokenExpired       = errors.New("token has expired")
	ErrTokenNotFound      = errors.New("token not found")
)

// AuthToken represents a stored API token
type AuthToken struct {
	ID                   string
	Name                 string
	TokenHash            string
	ExpiresAt            *time.Time
	CreatedAt            time.Time
	CreatedBy            string
	UltimateModelEnabled bool `json:"ultimate_model_enabled"`
}

// GenerateToken generates a new random token with sk- prefix
// Returns the plaintext token (show once) and its hash for storage
func GenerateToken() (plaintext string, hash string, err error) {
	bytes := make([]byte, TokenLength)
	if _, err := rand.Read(bytes); err != nil {
		return "", "", err
	}

	plaintext = TokenPrefix + hex.EncodeToString(bytes)
	hash = HashToken(plaintext)
	return plaintext, hash, nil
}

// HashToken creates a SHA-256 hash of the token for storage
func HashToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

// ValidateTokenFormat checks if token has correct format (sk- prefix + 64 hex chars)
func ValidateTokenFormat(token string) bool {
	if len(token) != len(TokenPrefix)+64 {
		return false
	}
	if token[:len(TokenPrefix)] != TokenPrefix {
		return false
	}
	// Check if rest is valid hex
	_, err := hex.DecodeString(token[len(TokenPrefix):])
	return err == nil
}

// IsExpired checks if the token has expired
func (t *AuthToken) IsExpired() bool {
	if t.ExpiresAt == nil {
		return false
	}
	return time.Now().After(*t.ExpiresAt)
}
