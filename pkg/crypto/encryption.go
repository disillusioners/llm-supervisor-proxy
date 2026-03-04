package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"sync"
)

const EnvEncryptionKey = "INTERNAL_ENCRYPTION_KEY"

var (
	ErrInvalidCiphertext = errors.New("invalid ciphertext")
	ErrKeyInvalidLength  = errors.New("encryption key must be 32 bytes")
)

var (
	encryptionKey     []byte
	encryptionKeyOnce sync.Once
)

// InitEncryption initializes the encryption key from INTERNAL_ENCRYPTION_KEY env var.
// If the env var is not set, encryption is disabled and data will be stored in plaintext.
func InitEncryption() error {
	encryptionKeyOnce.Do(func() {
		key := getEnvVar(EnvEncryptionKey)
		if key == "" {
			// No encryption key set - encryption disabled, use plaintext
			return
		}

		// Decode base64 key
		decoded, err := base64.StdEncoding.DecodeString(key)
		if err != nil {
			return // Invalid key format - encryption disabled
		}

		if len(decoded) != 32 {
			return // Invalid key length - encryption disabled
		}

		encryptionKey = decoded
	})
	return nil
}

// IsEncryptionEnabled returns true if INTERNAL_ENCRYPTION_KEY is set and valid.
func IsEncryptionEnabled() bool {
	_ = InitEncryption()
	return encryptionKey != nil
}

// Encrypt encrypts plaintext using AES-256-GCM if encryption is enabled.
// Returns plaintext unchanged if INTERNAL_ENCRYPTION_KEY is not set.
func Encrypt(plaintext string) (string, error) {
	if err := InitEncryption(); err != nil {
		return "", err
	}

	// No encryption key - return plaintext
	if encryptionKey == nil {
		return plaintext, nil
	}

	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decrypts base64-encoded ciphertext using AES-256-GCM if encryption is enabled.
// Returns input unchanged if INTERNAL_ENCRYPTION_KEY is not set.
// Returns input unchanged if the input is not valid encrypted data (assumes plaintext).
func Decrypt(ciphertext string) (string, error) {
	if err := InitEncryption(); err != nil {
		return "", err
	}

	// No encryption key - return input as-is (plaintext mode)
	if encryptionKey == nil {
		return ciphertext, nil
	}

	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		// Not valid base64 - assume it's plaintext, return as-is
		return ciphertext, nil
	}

	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		// Too short to be valid ciphertext - assume plaintext
		return ciphertext, nil
	}

	nonce, cipherData := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, cipherData, nil)
	if err != nil {
		// Decryption failed - assume it's plaintext, return as-is
		return ciphertext, nil
	}

	return string(plaintext), nil
}

// GenerateKey generates a new random 32-byte key encoded as base64.
// Useful for generating the INTERNAL_ENCRYPTION_KEY.
func GenerateKey() (string, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(key), nil
}

// getEnvVar is a wrapper for os.Getenv that can be mocked in tests
var getEnvVar = func(key string) string {
	import "os"
	return os.Getenv(key)
}
