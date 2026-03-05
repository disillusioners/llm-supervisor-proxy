package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
)

const EnvEncryptionKey = "INTERNAL_ENCRYPTION_KEY"

const keyFileName = ".encryption_key"

var (
	ErrInvalidCiphertext = errors.New("invalid ciphertext")
	ErrKeyInvalidLength  = errors.New("encryption key must be 32 bytes")
)

var (
	encryptionKey     []byte
	encryptionKeyOnce sync.Once
	encryptionKeyErr  error
)

// getKeyFilePath returns the path to the encryption key file
func getKeyFilePath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user config directory: %w", err)
	}
	return filepath.Join(configDir, "llm-supervisor-proxy", keyFileName), nil
}

// loadKeyFromFile reads the encryption key from the specified file
func loadKeyFromFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// InitEncryption initializes the encryption key from environment variable or key file
// If neither exists, encryption is disabled (Encrypt/Decrypt will pass through plaintext)
func InitEncryption() error {
	encryptionKeyOnce.Do(func() {
		var key string

		// 1. Check environment variable first
		key = os.Getenv(EnvEncryptionKey)

		// 2. If not set, try key file
		if key == "" {
			keyFilePath, err := getKeyFilePath()
			if err != nil {
				log.Printf("Encryption disabled: failed to get key file path: %v", err)
				return // No key, encryption disabled
			}

			keyFromFile, err := loadKeyFromFile(keyFilePath)
			if err == nil {
				key = keyFromFile
			} else if os.IsNotExist(err) {
				log.Printf("Encryption disabled: no key configured (set %s or create key file)", EnvEncryptionKey)
				return // No key, encryption disabled
			} else {
				encryptionKeyErr = fmt.Errorf("failed to read encryption key file: %w", err)
				return
			}
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
		log.Printf("Encryption enabled")
	})
	return encryptionKeyErr
}

// Encrypt encrypts plaintext using AES-256-GCM
// If no encryption key is configured, returns plaintext unchanged
func Encrypt(plaintext string) (string, error) {
	if err := InitEncryption(); err != nil {
		return "", err
	}

	// No key configured - pass through plaintext
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

// Decrypt decrypts base64-encoded ciphertext using AES-256-GCM
// If no encryption key is configured, returns input unchanged
func Decrypt(ciphertext string) (string, error) {
	if err := InitEncryption(); err != nil {
		return "", err
	}

	// No key configured - pass through
	if encryptionKey == nil {
		return ciphertext, nil
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
