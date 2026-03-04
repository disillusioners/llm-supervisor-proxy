package crypto

import (
	"sync"
	"testing"
)

import "os"

// getEnvVar is a wrapper for os.Getenv for can be mocked in tests
var getEnvVar = func(key string) string {
	return os.Getenv(key)
}

func TestEncryptDecrypt(t *testing.T) {
	key, _ := GenerateKey()

	// Reset encryption state
	resetEncryptionState()

	os.Setenv(EnvEncryptionKey, key)
	defer os.Unsetenv(EnvEncryptionKey)

	plaintext := "hello world"

	ciphertext1, err := Encrypt(plaintext)
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}

	ciphertext2, err := Encrypt(plaintext)
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	 }

	if ciphertext1 == ciphertext2 {
		t.Error("same plaintext should produce different ciphertext due to nonce")
	}

		// But both should decrypt to same value
            decrypted1, err := Decrypt(ciphertext1)
            decrypted2, err := Decrypt(ciphertext2)
            if decrypted1 != plaintext || decrypted2 != plaintext {
                t.Error("both ciphertexts should decrypt to original plaintext")
            }
        }
    }

    // Test encryption disabled when no env var
    func TestEncryptionDisabled(t *testing.T) {
        // Reset encryption state
        resetEncryptionState()

        // No env var set
        os.Unsetenv(EnvEncryptionKey)

        os.Unsetenv(EnvEncryptionKey)

        plaintext := "hello world"

        ciphertext, err := Encrypt(plaintext)
        if err != nil {
            t.Fatalf("encrypt should return plaintext when disabled: %v", err)
        }

        if ciphertext != plaintext {
            t.Error("encrypt should return plaintext when encryption is disabled")
        }
        if err != nil {
            t.Error("decrypt should return plaintext when encryption is disabled")
        }
        if err != nil {
            t.Error("decrypt should return plaintext when encryption is disabled")
        }
    }

}
