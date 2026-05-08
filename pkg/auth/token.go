package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
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
	UltimateModelEnabled bool         `json:"ultimate_model_enabled"`
	UltimateModelID      string       `json:"ultimate_model"` // Empty = use global config
	AllowedModels        []string     `json:"allowed_models"` // nil/empty = all models allowed
}

// ScanAllowedModels deserializes allowed_models from a JSON string (from DB).
// If the value is NULL/empty, returns nil (all models allowed).
// If JSON is malformed, returns empty slice (fail closed - deny all models).
// Handles both string and []byte types from different DB drivers.
func ScanAllowedModels(raw interface{}) []string {
	if raw == nil {
		return nil
	}

	// Handle both string and []byte (some DB drivers return []byte for TEXT)
	var str string
	switch v := raw.(type) {
	case string:
		str = v
	case []byte:
		str = string(v)
	default:
		// Fail closed for unknown types
		return []string{}
	}

	if str == "" {
		return nil
	}

	var models []string
	if err := json.Unmarshal([]byte(str), &models); err != nil {
		// Fail closed - deny all models on malformed JSON
		log.Printf("SECURITY: malformed allowed_models JSON for token, denying all models")
		return []string{}
	}
	return models
}

// scanString deserializes a nullable string from DB.
// NULL -> empty string, non-NULL -> the value.
// Handles both string and []byte types from different DB drivers.
func scanString(raw interface{}) string {
	if raw == nil {
		return ""
	}
	switch v := raw.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return ""
	}
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

// IsModelAllowed checks if the given model ID is allowed for this token.
// Returns true if AllowedModels is nil (all models allowed) or if modelID is
// found in the list. Empty slice is treated the same as nil (all models allowed).
// Case-sensitive exact match.
func (t *AuthToken) IsModelAllowed(modelID string) bool {
	// nil or empty slice means all models are allowed
	if len(t.AllowedModels) == 0 {
		return true
	}
	for _, allowed := range t.AllowedModels {
		if allowed == modelID {
			return true
		}
	}
	return false
}
