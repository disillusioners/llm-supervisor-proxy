package auth

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
	_ "modernc.org/sqlite" // SQLite driver
)

// testDB wraps a SQLite connection for testing TokenStore
type testDB struct {
	*sql.DB
}

func newTestDB(t *testing.T) (*testDB, func()) {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Failed to open SQLite database: %v", err)
	}

	// Enable foreign keys
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		t.Fatalf("Failed to enable foreign keys: %v", err)
	}

	// Run migrations to create auth_tokens table
	if err := runTestMigrations(db); err != nil {
		db.Close()
		t.Fatalf("Failed to run migrations: %v", err)
	}

	cleanup := func() {
		db.Close()
	}

	return &testDB{DB: db}, cleanup
}

// runTestMigrations creates the auth_tokens table for testing
func runTestMigrations(db *sql.DB) error {
	// Create auth_tokens table (matching migration 004 + 020)
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS auth_tokens (
			id TEXT PRIMARY KEY,
			token_hash TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			expires_at TEXT,
			created_at TEXT NOT NULL,
			created_by TEXT NOT NULL,
			ultimate_model_enabled BOOLEAN NOT NULL DEFAULT FALSE
		)
	`)
	if err != nil {
		return err
	}

	// Create index on token_hash
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_auth_tokens_hash ON auth_tokens(token_hash)`)
	return err
}

func TestNewTokenStore(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewTokenStore(db.DB, database.SQLite)

	if store == nil {
		t.Fatal("NewTokenStore returned nil")
	}

	if store.db == nil {
		t.Error("store.db is nil")
	}

	if store.dialect != database.SQLite {
		t.Errorf("dialect = %v, want SQLite", store.dialect)
	}
}

func TestCreateToken(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewTokenStore(db.DB, database.SQLite)
	ctx := context.Background()

	plaintext, token, err := store.CreateToken(ctx, "test-token", nil, "admin", false)
	if err != nil {
		t.Fatalf("CreateToken() error = %v, want nil", err)
	}

	if plaintext == "" {
		t.Error("plaintext token is empty")
	}

	if token == nil {
		t.Fatal("token is nil")
	}

	if token.ID == "" {
		t.Error("token.ID is empty")
	}

	if token.Name != "test-token" {
		t.Errorf("token.Name = %q, want %q", token.Name, "test-token")
	}

	if token.TokenHash == "" {
		t.Error("token.TokenHash is empty")
	}

	if token.ExpiresAt != nil {
		t.Error("token.ExpiresAt should be nil")
	}

	if token.CreatedBy != "admin" {
		t.Errorf("token.CreatedBy = %q, want %q", token.CreatedBy, "admin")
	}

	if token.CreatedAt.IsZero() {
		t.Error("token.CreatedAt is zero")
	}
}

func TestCreateTokenWithExpiration(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewTokenStore(db.DB, database.SQLite)
	ctx := context.Background()

	futureTime := time.Now().Add(24 * time.Hour)
	plaintext, token, err := store.CreateToken(ctx, "expiring-token", &futureTime, "admin", false)
	if err != nil {
		t.Fatalf("CreateToken() error = %v, want nil", err)
	}

	if plaintext == "" {
		t.Error("plaintext token is empty")
	}

	if token == nil {
		t.Fatal("token is nil")
	}

	if token.ExpiresAt == nil {
		t.Error("token.ExpiresAt should not be nil")
	}

	if !token.ExpiresAt.Equal(futureTime) {
		t.Errorf("token.ExpiresAt = %v, want %v", token.ExpiresAt, futureTime)
	}
}

func TestValidateTokenValid(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewTokenStore(db.DB, database.SQLite)
	ctx := context.Background()

	// Create a token
	plaintext, _, err := store.CreateToken(ctx, "valid-test", nil, "admin", false)
	if err != nil {
		t.Fatalf("CreateToken() error = %v", err)
	}

	// Validate it
	validated, err := store.ValidateToken(ctx, plaintext)
	if err != nil {
		t.Fatalf("ValidateToken() error = %v, want nil", err)
	}

	if validated == nil {
		t.Fatal("validated token is nil")
	}

	if validated.Name != "valid-test" {
		t.Errorf("validated.Name = %q, want %q", validated.Name, "valid-test")
	}

	if validated.CreatedBy != "admin" {
		t.Errorf("validated.CreatedBy = %q, want %q", validated.CreatedBy, "admin")
	}
}

func TestValidateTokenInvalidFormat(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewTokenStore(db.DB, database.SQLite)
	ctx := context.Background()

	testCases := []struct {
		name  string
		token string
	}{
		{"empty", ""},
		{"too short", "sk-short"},
		{"wrong prefix", "tk-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
		{"invalid hex", "sk-gggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggg"},
		{"no prefix", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.ValidateToken(ctx, tc.token)
			if err != ErrInvalidTokenFormat {
				t.Errorf("ValidateToken() error = %v, want ErrInvalidTokenFormat", err)
			}
		})
	}
}

func TestValidateTokenNotFound(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewTokenStore(db.DB, database.SQLite)
	ctx := context.Background()

	// Generate a valid format token that doesn't exist in DB
	plaintext, _, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	// Validate non-existent token
	_, err = store.ValidateToken(ctx, plaintext)
	if err != ErrTokenNotFound {
		t.Errorf("ValidateToken() error = %v, want ErrTokenNotFound", err)
	}
}

func TestValidateTokenExpired(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewTokenStore(db.DB, database.SQLite)
	ctx := context.Background()

	// Create token with past expiration
	pastTime := time.Now().Add(-24 * time.Hour)
	plaintext, _, err := store.CreateToken(ctx, "expired-token", &pastTime, "admin", false)
	if err != nil {
		t.Fatalf("CreateToken() error = %v", err)
	}

	// Validate expired token
	_, err = store.ValidateToken(ctx, plaintext)
	if err != ErrTokenExpired {
		t.Errorf("ValidateToken() error = %v, want ErrTokenExpired", err)
	}
}

func TestListTokensEmpty(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewTokenStore(db.DB, database.SQLite)
	ctx := context.Background()

	tokens, err := store.ListTokens(ctx)
	if err != nil {
		t.Fatalf("ListTokens() error = %v, want nil", err)
	}

	if len(tokens) != 0 {
		t.Errorf("len(tokens) = %d, want 0", len(tokens))
	}
}

func TestListTokensReturnsAll(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewTokenStore(db.DB, database.SQLite)
	ctx := context.Background()

	// Create multiple tokens
	for i := 0; i < 5; i++ {
		_, _, err := store.CreateToken(ctx, "token-"+string(rune('a'+i)), nil, "admin", false)
		if err != nil {
			t.Fatalf("CreateToken() error = %v", err)
		}
	}

	tokens, err := store.ListTokens(ctx)
	if err != nil {
		t.Fatalf("ListTokens() error = %v, want nil", err)
	}

	if len(tokens) != 5 {
		t.Errorf("len(tokens) = %d, want 5", len(tokens))
	}

	// Verify no token hash is returned in list
	for _, token := range tokens {
		if token.TokenHash != "" {
			t.Error("ListTokens should not return TokenHash")
		}
	}
}

func TestListTokensOrderedByCreatedAtDesc(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewTokenStore(db.DB, database.SQLite)
	ctx := context.Background()

	// Create tokens with slight delays to ensure different timestamps
	for i := 0; i < 3; i++ {
		_, _, err := store.CreateToken(ctx, "order-test-"+string(rune('a'+i)), nil, "admin", false)
		if err != nil {
			t.Fatalf("CreateToken() error = %v", err)
		}
		// Small delay to ensure different timestamps
		time.Sleep(time.Millisecond)
	}

	listed, err := store.ListTokens(ctx)
	if err != nil {
		t.Fatalf("ListTokens() error = %v", err)
	}

	// Should be ordered by created_at DESC (newest first)
	for i := 0; i < len(listed)-1; i++ {
		if listed[i].CreatedAt.Before(listed[i+1].CreatedAt) {
			t.Error("ListTokens should return tokens ordered by created_at DESC")
		}
	}
}

func TestDeleteToken(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewTokenStore(db.DB, database.SQLite)
	ctx := context.Background()

	// Create a token
	plaintext, token, err := store.CreateToken(ctx, "delete-test", nil, "admin", false)
	if err != nil {
		t.Fatalf("CreateToken() error = %v", err)
	}

	// Delete it
	err = store.DeleteToken(ctx, token.ID)
	if err != nil {
		t.Fatalf("DeleteToken() error = %v, want nil", err)
	}

	// Validate should fail
	_, err = store.ValidateToken(ctx, plaintext)
	if err != ErrTokenNotFound {
		t.Errorf("ValidateToken() after delete error = %v, want ErrTokenNotFound", err)
	}
}

func TestDeleteTokenNotFound(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewTokenStore(db.DB, database.SQLite)
	ctx := context.Background()

	err := store.DeleteToken(ctx, "nonexistent-id")
	if err != ErrTokenNotFound {
		t.Errorf("DeleteToken() error = %v, want ErrTokenNotFound", err)
	}
}

func TestGetTokenByID(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewTokenStore(db.DB, database.SQLite)
	ctx := context.Background()

	// Create a token
	_, created, err := store.CreateToken(ctx, "get-by-id-test", nil, "creator", false)
	if err != nil {
		t.Fatalf("CreateToken() error = %v", err)
	}

	// Get by ID
	retrieved, err := store.GetTokenByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetTokenByID() error = %v, want nil", err)
	}

	if retrieved == nil {
		t.Fatal("retrieved token is nil")
	}

	if retrieved.ID != created.ID {
		t.Errorf("retrieved.ID = %q, want %q", retrieved.ID, created.ID)
	}

	if retrieved.Name != "get-by-id-test" {
		t.Errorf("retrieved.Name = %q, want %q", retrieved.Name, "get-by-id-test")
	}

	if retrieved.CreatedBy != "creator" {
		t.Errorf("retrieved.CreatedBy = %q, want %q", retrieved.CreatedBy, "creator")
	}

	// TokenHash should not be returned
	if retrieved.TokenHash != "" {
		t.Error("GetTokenByID should not return TokenHash")
	}
}

func TestGetTokenByIDNotFound(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewTokenStore(db.DB, database.SQLite)
	ctx := context.Background()

	_, err := store.GetTokenByID(ctx, "nonexistent-id")
	if err != ErrTokenNotFound {
		t.Errorf("GetTokenByID() error = %v, want ErrTokenNotFound", err)
	}
}

func TestTokenStoreMultipleOperations(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewTokenStore(db.DB, database.SQLite)
	ctx := context.Background()

	// Create multiple tokens
	token1, _, err := store.CreateToken(ctx, "multi-1", nil, "admin", false)
	if err != nil {
		t.Fatalf("CreateToken(1) error = %v", err)
	}

	token2, _, err := store.CreateToken(ctx, "multi-2", nil, "admin", false)
	if err != nil {
		t.Fatalf("CreateToken(2) error = %v", err)
	}

	// Validate both
	_, err = store.ValidateToken(ctx, token1)
	if err != nil {
		t.Errorf("ValidateToken(1) error = %v", err)
	}

	_, err = store.ValidateToken(ctx, token2)
	if err != nil {
		t.Errorf("ValidateToken(2) error = %v", err)
	}

	// List all
	tokens, err := store.ListTokens(ctx)
	if err != nil {
		t.Fatalf("ListTokens() error = %v", err)
	}
	if len(tokens) != 2 {
		t.Errorf("len(tokens) = %d, want 2", len(tokens))
	}

	// Delete one
	_, deleteToken, _ := store.CreateToken(ctx, "to-delete", nil, "admin", false)
	store.DeleteToken(ctx, deleteToken.ID)

	tokens, err = store.ListTokens(ctx)
	if err != nil {
		t.Fatalf("ListTokens() after delete error = %v", err)
	}
	if len(tokens) != 2 {
		t.Errorf("len(tokens) after delete = %d, want 2", len(tokens))
	}
}

func TestTokenStoreCreateTokenWithNoExpiration(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewTokenStore(db.DB, database.SQLite)
	ctx := context.Background()

	// Create token with nil expiration
	_, token, err := store.CreateToken(ctx, "no-expire", nil, "admin", false)
	if err != nil {
		t.Fatalf("CreateToken() error = %v", err)
	}

	if token.ExpiresAt != nil {
		t.Error("token.ExpiresAt should be nil for no-expiration token")
	}

	// Should validate successfully (not expired)
	plaintext, _, _ := store.CreateToken(ctx, "for-plaintext", nil, "admin", false)
	validated, err := store.ValidateToken(ctx, plaintext)
	if err != nil {
		t.Fatalf("ValidateToken() error = %v", err)
	}
	if validated.ExpiresAt != nil {
		t.Error("validated token should have nil ExpiresAt")
	}
}

func TestTokenStoreInterface(t *testing.T) {
	var _ TokenStoreInterface = (*TokenStore)(nil)
}

// ─────────────────────────────────────────────────────────────────────────────
// Ultimate Model Enabled Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestCreateTokenWithUltimateModelEnabled(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewTokenStore(db.DB, database.SQLite)
	ctx := context.Background()

	// Create token with ultimate_model_enabled = true
	plaintext, token, err := store.CreateToken(ctx, "ultimate-enabled", nil, "admin", true)
	if err != nil {
		t.Fatalf("CreateToken() error = %v, want nil", err)
	}

	if plaintext == "" {
		t.Error("plaintext token is empty")
	}

	if token == nil {
		t.Fatal("token is nil")
	}

	if !token.UltimateModelEnabled {
		t.Error("token.UltimateModelEnabled should be true")
	}

	// Validate it returns the correct value
	validated, err := store.ValidateToken(ctx, plaintext)
	if err != nil {
		t.Fatalf("ValidateToken() error = %v, want nil", err)
	}

	if !validated.UltimateModelEnabled {
		t.Error("validated.UltimateModelEnabled should be true")
	}
}

func TestCreateTokenWithUltimateModelDisabled(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewTokenStore(db.DB, database.SQLite)
	ctx := context.Background()

	// Create token with ultimate_model_enabled = false (default)
	_, token, err := store.CreateToken(ctx, "ultimate-disabled", nil, "admin", false)
	if err != nil {
		t.Fatalf("CreateToken() error = %v, want nil", err)
	}

	if token.UltimateModelEnabled {
		t.Error("token.UltimateModelEnabled should be false")
	}
}

func TestListTokensReturnsUltimateModelEnabled(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewTokenStore(db.DB, database.SQLite)
	ctx := context.Background()

	// Create tokens with different ultimate_model_enabled values
	_, _, err := store.CreateToken(ctx, "ultimate-true", nil, "admin", true)
	if err != nil {
		t.Fatalf("CreateToken() error = %v", err)
	}

	_, _, err = store.CreateToken(ctx, "ultimate-false", nil, "admin", false)
	if err != nil {
		t.Fatalf("CreateToken() error = %v", err)
	}

	tokens, err := store.ListTokens(ctx)
	if err != nil {
		t.Fatalf("ListTokens() error = %v", err)
	}

	if len(tokens) != 2 {
		t.Fatalf("len(tokens) = %d, want 2", len(tokens))
	}

	// Find each token and verify its ultimate_model_enabled value
	foundTrue := false
	foundFalse := false
	for _, token := range tokens {
		if token.Name == "ultimate-true" {
			if !token.UltimateModelEnabled {
				t.Error("Token 'ultimate-true' should have UltimateModelEnabled = true")
			}
			foundTrue = true
		} else if token.Name == "ultimate-false" {
			if token.UltimateModelEnabled {
				t.Error("Token 'ultimate-false' should have UltimateModelEnabled = false")
			}
			foundFalse = true
		}
	}

	if !foundTrue {
		t.Error("Token 'ultimate-true' not found in list")
	}
	if !foundFalse {
		t.Error("Token 'ultimate-false' not found in list")
	}
}

func TestGetTokenByIDReturnsUltimateModelEnabled(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewTokenStore(db.DB, database.SQLite)
	ctx := context.Background()

	// Create token with ultimate_model_enabled = true
	_, created, err := store.CreateToken(ctx, "get-ultimate-test", nil, "admin", true)
	if err != nil {
		t.Fatalf("CreateToken() error = %v", err)
	}

	// Get by ID
	retrieved, err := store.GetTokenByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetTokenByID() error = %v, want nil", err)
	}

	if !retrieved.UltimateModelEnabled {
		t.Error("retrieved.UltimateModelEnabled should be true")
	}
}

func TestUpdateTokenPermission(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewTokenStore(db.DB, database.SQLite)
	ctx := context.Background()

	// Create token with ultimate_model_enabled = false
	_, created, err := store.CreateToken(ctx, "update-test", nil, "admin", false)
	if err != nil {
		t.Fatalf("CreateToken() error = %v", err)
	}

	// Verify initial state
	if created.UltimateModelEnabled {
		t.Error("Initial token should have UltimateModelEnabled = false")
	}

	// Enable ultimate model
	err = store.UpdateTokenPermission(ctx, created.ID, true)
	if err != nil {
		t.Fatalf("UpdateTokenPermission(enable) error = %v, want nil", err)
	}

	// Verify enabled
	retrieved, _ := store.GetTokenByID(ctx, created.ID)
	if !retrieved.UltimateModelEnabled {
		t.Error("After enable, token should have UltimateModelEnabled = true")
	}

	// Disable ultimate model
	err = store.UpdateTokenPermission(ctx, created.ID, false)
	if err != nil {
		t.Fatalf("UpdateTokenPermission(disable) error = %v, want nil", err)
	}

	// Verify disabled
	retrieved, _ = store.GetTokenByID(ctx, created.ID)
	if retrieved.UltimateModelEnabled {
		t.Error("After disable, token should have UltimateModelEnabled = false")
	}
}

func TestUpdateTokenPermissionNotFound(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewTokenStore(db.DB, database.SQLite)
	ctx := context.Background()

	err := store.UpdateTokenPermission(ctx, "nonexistent-id", true)
	if err != ErrTokenNotFound {
		t.Errorf("UpdateTokenPermission() error = %v, want ErrTokenNotFound", err)
	}
}

func TestValidateTokenReturnsUltimateModelEnabled(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewTokenStore(db.DB, database.SQLite)
	ctx := context.Background()

	// Create token with ultimate_model_enabled = true
	plaintext, _, err := store.CreateToken(ctx, "validate-ultimate", nil, "admin", true)
	if err != nil {
		t.Fatalf("CreateToken() error = %v", err)
	}

	// Validate it
	validated, err := store.ValidateToken(ctx, plaintext)
	if err != nil {
		t.Fatalf("ValidateToken() error = %v, want nil", err)
	}

	if !validated.UltimateModelEnabled {
		t.Error("validated.UltimateModelEnabled should be true")
	}
}
