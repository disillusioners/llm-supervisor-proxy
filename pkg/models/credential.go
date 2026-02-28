package models

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/crypto"
)

// CredentialConfig represents a reusable credential for internal upstream models.
// Credentials are stored separately from models and referenced by ID.
type CredentialConfig struct {
	ID       string `json:"id"`                 // Unique identifier for the credential
	Provider string `json:"provider"`           // Provider: openai, anthropic, gemini, zhipu, azure, zai, minimax
	APIKey   string `json:"api_key"`            // API key (supports ${ENV_VAR} expansion)
	BaseURL  string `json:"base_url,omitempty"` // Custom base URL (optional)
}

// CredentialConfigInterface defines methods for credential management
type CredentialConfigInterface interface {
	GetCredential(id string) *CredentialConfig
	GetCredentials() []CredentialConfig
	AddCredential(cred CredentialConfig) error
	UpdateCredential(id string, cred CredentialConfig) error
	RemoveCredential(id string) error
}

// expandEnvVars expands environment variable references in the format ${VAR} or ${VAR:-default}
func expandEnvVars(s string) string {
	if s == "" {
		return s
	}

	// Handle ${VAR:-default} syntax
	if strings.Contains(s, ":-") {
		// Pattern: ${VAR:-default}
		start := strings.Index(s, "${")
		for start != -1 {
			end := strings.Index(s[start:], "}")
			if end == -1 {
				break
			}
			end += start

			// Extract the content between ${ and }
			content := s[start+2 : end]

			// Check for :- separator
			if sepIdx := strings.Index(content, ":-"); sepIdx != -1 {
				envVar := content[:sepIdx]
				defaultVal := content[sepIdx+2:]
				envVal := os.Getenv(envVar)
				if envVal == "" {
					envVal = defaultVal
				}
				s = s[:start] + envVal + s[end+1:]
			} else {
				// No default, just expand
				envVal := os.Getenv(content)
				s = s[:start] + envVal + s[end+1:]
			}

			start = strings.Index(s, "${")
		}
		return s
	}

	// Simple ${VAR} expansion
	return os.Expand(s, func(key string) string {
		// Return empty string for undefined vars (not the literal ${key})
		return os.Getenv(key)
	})
}

// ResolveAPIKey returns the resolved API key with environment variables expanded
func (c *CredentialConfig) ResolveAPIKey() string {
	return expandEnvVars(c.APIKey)
}

// Validate validates the credential configuration
func (c *CredentialConfig) Validate() error {
	if c.ID == "" {
		return ErrInvalidCredentialID
	}
	if c.Provider == "" {
		return fmt.Errorf("credential %s: provider is required", c.ID)
	}

	// Resolve env vars before validating
	resolvedKey := c.ResolveAPIKey()
	if resolvedKey == "" {
		return fmt.Errorf("credential %s: api_key is required (after env var expansion)", c.ID)
	}

	if !isValidProvider(c.Provider) {
		return fmt.Errorf("credential %s: invalid provider: %s", c.ID, c.Provider)
	}
	return nil
}

// Credential errors
var (
	ErrInvalidCredentialID      = &ConfigError{"invalid credential ID: cannot be empty"}
	ErrDuplicateCredentialID    = &ConfigError{"duplicate credential ID"}
	ErrCredentialNotFound       = &ConfigError{"credential not found"}
	ErrCredentialInUse          = &ConfigError{"credential is in use by one or more models"}
	ErrCannotChangeCredentialID = &ConfigError{"cannot change credential ID"}
)

// CredentialsConfig manages the collection of credential configurations.
type CredentialsConfig struct {
	mu          sync.RWMutex
	credentials map[string]CredentialConfig
}

// NewCredentialsConfig creates a new empty CredentialsConfig.
func NewCredentialsConfig() *CredentialsConfig {
	return &CredentialsConfig{
		credentials: make(map[string]CredentialConfig),
	}
}

// GetCredential returns the credential configuration for a given ID.
// Returns nil if the credential is not found.
// The API key is decrypted before returning.
func (cc *CredentialsConfig) GetCredential(id string) *CredentialConfig {
	cc.mu.RLock()
	defer cc.mu.RUnlock()

	if cred, ok := cc.credentials[id]; ok {
		copy := cred
		// Decrypt API key before returning
		if copy.APIKey != "" {
			decrypted, err := crypto.Decrypt(copy.APIKey)
			if err != nil {
				log.Printf("Warning: failed to decrypt API key for credential %s: %v", id, err)
				// Return with encrypted key rather than failing
			} else {
				copy.APIKey = decrypted
			}
		}
		return &copy
	}
	return nil
}

// GetCredentials returns all credential configurations.
// API keys are decrypted before returning.
func (cc *CredentialsConfig) GetCredentials() []CredentialConfig {
	cc.mu.RLock()
	defer cc.mu.RUnlock()

	result := make([]CredentialConfig, 0, len(cc.credentials))
	for _, cred := range cc.credentials {
		copy := cred
		// Decrypt API key before returning
		if copy.APIKey != "" {
			decrypted, err := crypto.Decrypt(copy.APIKey)
			if err != nil {
				log.Printf("Warning: failed to decrypt API key for credential %s: %v", copy.ID, err)
			} else {
				copy.APIKey = decrypted
			}
		}
		result = append(result, copy)
	}
	return result
}

// AddCredential adds a new credential configuration after validation.
// The API key is encrypted before storing.
func (cc *CredentialsConfig) AddCredential(cred CredentialConfig) error {
	if err := cred.Validate(); err != nil {
		return err
	}

	// Encrypt API key before storing
	if cred.APIKey != "" {
		encrypted, err := crypto.Encrypt(cred.APIKey)
		if err != nil {
			return fmt.Errorf("failed to encrypt API key: %w", err)
		}
		cred.APIKey = encrypted
	}

	cc.mu.Lock()
	defer cc.mu.Unlock()

	if _, exists := cc.credentials[cred.ID]; exists {
		return ErrDuplicateCredentialID
	}

	cc.credentials[cred.ID] = cred
	return nil
}

// UpdateCredential updates an existing credential configuration after validation.
// The API key is encrypted before storing.
func (cc *CredentialsConfig) UpdateCredential(id string, cred CredentialConfig) error {
	if err := cred.Validate(); err != nil {
		return err
	}

	// Encrypt API key before storing
	if cred.APIKey != "" {
		encrypted, err := crypto.Encrypt(cred.APIKey)
		if err != nil {
			return fmt.Errorf("failed to encrypt API key: %w", err)
		}
		cred.APIKey = encrypted
	}

	cc.mu.Lock()
	defer cc.mu.Unlock()

	if _, exists := cc.credentials[id]; !exists {
		return ErrCredentialNotFound
	}

	// Ensure the ID doesn't change
	if cred.ID != id {
		return ErrCannotChangeCredentialID
	}

	cc.credentials[id] = cred
	return nil
}

// RemoveCredential removes a credential configuration by ID.
// It does NOT check if models are using this credential - that's handled by ModelsConfig.Validate()
func (cc *CredentialsConfig) RemoveCredential(id string) error {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	if _, exists := cc.credentials[id]; !exists {
		return ErrCredentialNotFound
	}

	delete(cc.credentials, id)
	return nil
}

// SetCredentials replaces all credentials (used during loading)
func (cc *CredentialsConfig) SetCredentials(creds []CredentialConfig) {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	cc.credentials = make(map[string]CredentialConfig)
	for _, cred := range creds {
		cc.credentials[cred.ID] = cred
	}
}

// ToSlice converts credentials map to slice for JSON serialization.
// API keys are kept encrypted (as stored) for serialization.
func (cc *CredentialsConfig) ToSlice() []CredentialConfig {
	cc.mu.RLock()
	defer cc.mu.RUnlock()

	result := make([]CredentialConfig, 0, len(cc.credentials))
	for _, cred := range cc.credentials {
		result = append(result, cred)
	}
	return result
}
