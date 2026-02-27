package crypto

import (
	"os"
	"sync"
	"testing"
)

func TestEncryptDecrypt(t *testing.T) {
	// Generate a test key
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	// Reset encryption state for test
	encryptionKey = nil
	encryptionKeyOnce = sync.Once{}
	encryptionKeyErr = nil
	usingDefaultKey = false

	// Set the key
	os.Setenv(EnvEncryptionKey, key)
	defer os.Unsetenv(EnvEncryptionKey)

	plaintext := "my-secret-api-key"

	ciphertext, err := Encrypt(plaintext)
	if err != nil {
		t.Fatalf("failed to encrypt: %v", err)
	}

	if ciphertext == "" {
		t.Error("ciphertext should not be empty")
	}

	if ciphertext == plaintext {
		t.Error("ciphertext should not equal plaintext")
	}

	decrypted, err := Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("failed to decrypt: %v", err)
	}

	if decrypted != plaintext {
		t.Errorf("decrypted = %q, want %q", decrypted, plaintext)
	}
}

func TestEncryptProducesDifferentCiphertext(t *testing.T) {
	key, _ := GenerateKey()

	// Reset encryption state
	encryptionKey = nil
	encryptionKeyOnce = sync.Once{}
	encryptionKeyErr = nil
	usingDefaultKey = false

	os.Setenv(EnvEncryptionKey, key)
	defer os.Unsetenv(EnvEncryptionKey)

	plaintext := "same-plaintext"

	ciphertext1, _ := Encrypt(plaintext)

	// Reset to allow re-initialization
	encryptionKey = nil
	encryptionKeyOnce = sync.Once{}

	ciphertext2, _ := Encrypt(plaintext)

	// Different nonces should produce different ciphertext
	if ciphertext1 == ciphertext2 {
		t.Error("same plaintext should produce different ciphertext due to nonce")
	}

	// But both should decrypt to same value
	decrypted1, _ := Decrypt(ciphertext1)
	decrypted2, _ := Decrypt(ciphertext2)

	if decrypted1 != plaintext || decrypted2 != plaintext {
		t.Error("both ciphertexts should decrypt to original plaintext")
	}
}

func TestDecryptInvalidCiphertext(t *testing.T) {
	key, _ := GenerateKey()

	// Reset encryption state
	encryptionKey = nil
	encryptionKeyOnce = sync.Once{}
	encryptionKeyErr = nil
	usingDefaultKey = false

	os.Setenv(EnvEncryptionKey, key)
	defer os.Unsetenv(EnvEncryptionKey)

	_, err := Decrypt("not-valid-base64!@#$")
	if err == nil {
		t.Error("expected error for invalid base64")
	}
}

func TestInitEncryptionMissingKey(t *testing.T) {
	// Reset encryption state
	encryptionKey = nil
	encryptionKeyOnce = sync.Once{}
	encryptionKeyErr = nil
	usingDefaultKey = false

	os.Unsetenv(EnvEncryptionKey)

	err := InitEncryption()
	if err != nil {
		t.Errorf("expected no error when using default key, got: %v", err)
	}
	if !usingDefaultKey {
		t.Error("expected UsingDefaultKey() to return true")
	}
}

func TestInitEncryptionInvalidKeyLength(t *testing.T) {
	// Reset encryption state
	encryptionKey = nil
	encryptionKeyOnce = sync.Once{}
	encryptionKeyErr = nil
	usingDefaultKey = false

	os.Setenv(EnvEncryptionKey, "dG9vc2hvcnQ=") // "tooshort" in base64, but less than 32 bytes
	defer os.Unsetenv(EnvEncryptionKey)

	err := InitEncryption()
	if err == nil {
		t.Error("expected error for invalid key length")
	}
}

func TestGenerateKey(t *testing.T) {
	key1, err := GenerateKey()
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	key2, err := GenerateKey()
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	// Keys should be different
	if key1 == key2 {
		t.Error("generated keys should be unique")
	}

	// Key should not be empty
	if key1 == "" {
		t.Error("generated key should not be empty")
	}
}
