package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"sync"
)

const EnvEncryptionKey = "INTERNAL_ENCRYPTION_KEY"

// DefaultEncryptionKey is a hardcoded fallback key (32 bytes base64-encoded)
// WARNING: For production, always set INTERNAL_ENCRYPTION_KEY env var
const DefaultEncryptionKey = "zxL6bzszggELp/xZ5t5Hnd4GQW27wMgTg4e7LV874uU="

var (
	ErrEncryptionKeyNotSet = errors.New("INTERNAL_ENCRYPTION_KEY environment variable not set")
	ErrInvalidCiphertext   = errors.New("invalid ciphertext")
	ErrKeyInvalidLength    = errors.New("encryption key must be 32 bytes")
)

var (
	encryptionKey     []byte
	encryptionKeyOnce sync.Once
	encryptionKeyErr  error
	usingDefaultKey   = false
)

// InitEncryption initializes the encryption key from environment variable
// Falls back to a hardcoded default key if not set
func InitEncryption() error {
	encryptionKeyOnce.Do(func() {
		key := os.Getenv(EnvEncryptionKey)
		if key == "" {
			// Use default key
			key = DefaultEncryptionKey
			usingDefaultKey = true
		}

		// Decode base64 key
		decoded, err := base64.StdEncoding.DecodeString(key)
		if err != nil {
			encryptionKeyErr = errors.New("failed to decode encryption key: " + err.Error())
			return
		}

		if len(decoded) != 32 {
			encryptionKeyErr = ErrKeyInvalidLength
			return
		}

		encryptionKey = decoded
	})
	return encryptionKeyErr
}

// UsingDefaultKey returns true if the default hardcoded key is being used
func UsingDefaultKey() bool {
	return usingDefaultKey
}

// Encrypt encrypts plaintext using AES-256-GCM
// Returns base64-encoded ciphertext
func Encrypt(plaintext string) (string, error) {
	if err := InitEncryption(); err != nil {
		return "", err
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

// Decrypt decrypts base64-encoded ciphertext using AES-256-GCM
func Decrypt(ciphertext string) (string, error) {
	if err := InitEncryption(); err != nil {
		return "", err
	}

	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
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
		return "", ErrInvalidCiphertext
	}

	nonce, cipherData := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, cipherData, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}

// GenerateKey generates a new random 32-byte key encoded as base64
// Useful for generating the INTERNAL_ENCRYPTION_KEY
func GenerateKey() (string, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(key), nil
}
