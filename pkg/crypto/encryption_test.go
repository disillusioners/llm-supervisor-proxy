package crypto

import (
	"os"
	"sync"
	"testing"
)

// resetEncryptionState resets the encryption state for testing
func resetEncryptionState() {
	encryptionKey = nil
	encryptionKeyOnce = sync.Once{}
}

func TestEncryptDecrypt(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	resetEncryptionState()

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

func TestEncryptionDisabled(t *testing.T) {
	// Reset encryption state
	resetEncryptionState()

	// Ensure no env var is set
	os.Unsetenv(EnvEncryptionKey)

	plaintext := "plaintext-should-be-unchanged"

	ciphertext, err := Encrypt(plaintext)
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}

	// Verify plaintext is returned unchanged
	if ciphertext != plaintext {
		t.Errorf("encrypt should return plaintext unchanged when encryption is disabled, got: %q", ciphertext)
	}

	decrypted, err := Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("decrypt failed: %v", err)
	}

	// Verify decryption returns plaintext unchanged
	if decrypted != plaintext {
		t.Errorf("decrypt should return plaintext unchanged when encryption is disabled, got: %q", decrypted)
	}
}
