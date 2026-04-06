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
	// Create auth_tokens table (matching migration 004)
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS auth_tokens (
			id TEXT PRIMARY KEY,
			token_hash TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			expires_at TEXT,
			created_at TEXT NOT NULL,
			created_by TEXT NOT NULL
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

	plaintext, token, err := store.CreateToken(ctx, "test-token", nil, "admin")
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
	plaintext, token, err := store.CreateToken(ctx, "expiring-token", &futureTime, "admin")
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
	plaintext, _, err := store.CreateToken(ctx, "valid-test", nil, "admin")
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
	plaintext, _, err := store.CreateToken(ctx, "expired-token", &pastTime, "admin")
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
		_, _, err := store.CreateToken(ctx, "token-"+string(rune('a'+i)), nil, "admin")
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
		_, _, err := store.CreateToken(ctx, "order-test-"+string(rune('a'+i)), nil, "admin")
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
	plaintext, token, err := store.CreateToken(ctx, "delete-test", nil, "admin")
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
	_, created, err := store.CreateToken(ctx, "get-by-id-test", nil, "creator")
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
	token1, _, err := store.CreateToken(ctx, "multi-1", nil, "admin")
	if err != nil {
		t.Fatalf("CreateToken(1) error = %v", err)
	}

	token2, _, err := store.CreateToken(ctx, "multi-2", nil, "admin")
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
	_, deleteToken, _ := store.CreateToken(ctx, "to-delete", nil, "admin")
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
	_, token, err := store.CreateToken(ctx, "no-expire", nil, "admin")
	if err != nil {
		t.Fatalf("CreateToken() error = %v", err)
	}

	if token.ExpiresAt != nil {
		t.Error("token.ExpiresAt should be nil for no-expiration token")
	}

	// Should validate successfully (not expired)
	plaintext, _, _ := store.CreateToken(ctx, "for-plaintext", nil, "admin")
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
