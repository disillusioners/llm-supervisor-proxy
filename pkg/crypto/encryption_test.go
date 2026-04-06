package crypto

import (
	"os"
	"testing"
)

// resetEncryptionState resets the encryption state for testing
func resetEncryptionState() {
	ResetEncryptionState()
}

func TestEncryptDecrypt(t *testing.T) {
	// Generate a test key
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	// Reset encryption state for test
	resetEncryptionState()

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
	resetEncryptionState()

	os.Setenv(EnvEncryptionKey, key)
	defer os.Unsetenv(EnvEncryptionKey)

	plaintext := "same-plaintext"

	ciphertext1, _ := Encrypt(plaintext)

	// Reset to allow re-initialization
	resetEncryptionState()

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
	resetEncryptionState()

	os.Setenv(EnvEncryptionKey, key)
	defer os.Unsetenv(EnvEncryptionKey)

	_, err := Decrypt("not-valid-base64!@#$")
	if err == nil {
		t.Error("expected error for invalid base64")
	}
}

func TestInitEncryptionWithEnvVar(t *testing.T) {
	key, _ := GenerateKey()

	// Reset encryption state
	resetEncryptionState()

	os.Setenv(EnvEncryptionKey, key)
	defer os.Unsetenv(EnvEncryptionKey)

	err := InitEncryption()
	if err != nil {
		t.Errorf("expected no error with valid env var, got: %v", err)
	}
}

func TestInitEncryptionGeneratesKeyFile(t *testing.T) {
	// Reset encryption state
	resetEncryptionState()

	// Ensure no env var is set
	os.Unsetenv(EnvEncryptionKey)

	// Get the expected key file path
	keyPath, err := getKeyFilePath()
	if err != nil {
		t.Fatalf("failed to get key file path: %v", err)
	}

	// Remove existing key file if present
	os.Remove(keyPath)

	err = InitEncryption()
	if err != nil {
		t.Errorf("expected no error when no key configured, got: %v", err)
	}

	// Encryption should be disabled (no key generated)
	if encryptionKey != nil {
		t.Error("expected encryption to be disabled (no auto-generation)")
	}

	// Key file should NOT be created
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Errorf("expected key file NOT to be auto-generated at %s", keyPath)
		os.Remove(keyPath)
	}
}

func TestInitEncryptionInvalidKeyLength(t *testing.T) {
	// Reset encryption state
	resetEncryptionState()

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

func TestPassthroughWhenNoKey(t *testing.T) {
	// Reset encryption state
	resetEncryptionState()

	// Ensure no env var is set
	os.Unsetenv(EnvEncryptionKey)

	// Get the key file path and remove it
	keyPath, _ := getKeyFilePath()
	os.Remove(keyPath)

	plaintext := "my-secret-api-key"

	// Encrypt should return plaintext unchanged
	encrypted, err := Encrypt(plaintext)
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}

	if encrypted != plaintext {
		t.Errorf("expected passthrough, got encrypted=%q, want %q", encrypted, plaintext)
	}

	// Decrypt should return input unchanged
	decrypted, err := Decrypt(plaintext)
	if err != nil {
		t.Fatalf("decrypt failed: %v", err)
	}

	if decrypted != plaintext {
		t.Errorf("expected passthrough, got decrypted=%q, want %q", decrypted, plaintext)
	}
}
