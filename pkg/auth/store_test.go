package auth

import (
	"context"
	"database/sql"
	"fmt"
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
	// Create auth_tokens table (matching migration 004 + 020 + 022 + 023)
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS auth_tokens (
			id TEXT PRIMARY KEY,
			token_hash TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			expires_at TEXT,
			created_at TEXT NOT NULL,
			created_by TEXT NOT NULL,
			allowed_models TEXT,
			allowed_groups TEXT DEFAULT NULL
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

	plaintext, token, err := store.CreateToken(ctx, "test-token", nil, "admin", FullPermission(nil, nil))
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
	plaintext, token, err := store.CreateToken(ctx, "expiring-token", &futureTime, "admin", FullPermission(nil, nil))
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
	plaintext, _, err := store.CreateToken(ctx, "valid-test", nil, "admin", FullPermission(nil, nil))
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
	plaintext, _, err := store.CreateToken(ctx, "expired-token", &pastTime, "admin", FullPermission(nil, nil))
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
		_, _, err := store.CreateToken(ctx, "token-"+string(rune('a'+i)), nil, "admin", FullPermission(nil, nil))
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
		_, _, err := store.CreateToken(ctx, "order-test-"+string(rune('a'+i)), nil, "admin", FullPermission(nil, nil))
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
	plaintext, token, err := store.CreateToken(ctx, "delete-test", nil, "admin", FullPermission(nil, nil))
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
	_, created, err := store.CreateToken(ctx, "get-by-id-test", nil, "creator", FullPermission(nil, nil))
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
	token1, _, err := store.CreateToken(ctx, "multi-1", nil, "admin", FullPermission(nil, nil))
	if err != nil {
		t.Fatalf("CreateToken(1) error = %v", err)
	}

	token2, _, err := store.CreateToken(ctx, "multi-2", nil, "admin", FullPermission(nil, nil))
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
	_, deleteToken, _ := store.CreateToken(ctx, "to-delete", nil, "admin", FullPermission(nil, nil))
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
	_, token, err := store.CreateToken(ctx, "no-expire", nil, "admin", FullPermission(nil, nil))
	if err != nil {
		t.Fatalf("CreateToken() error = %v", err)
	}

	if token.ExpiresAt != nil {
		t.Error("token.ExpiresAt should be nil for no-expiration token")
	}

	// Should validate successfully (not expired)
	plaintext, _, _ := store.CreateToken(ctx, "for-plaintext", nil, "admin", FullPermission(nil, nil))
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

func TestUpdateTokenPermission(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewTokenStore(db.DB, database.SQLite)
	ctx := context.Background()

	// Create token with no allowed_models restriction (all allowed)
	_, created, err := store.CreateToken(ctx, "update-test", nil, "admin", FullPermission(nil, nil))
	if err != nil {
		t.Fatalf("CreateToken() error = %v", err)
	}

	// Restrict to a model list
	models := []string{"model-a", "model-b"}
	err = store.UpdateTokenPermission(ctx, created.ID, GrantModels(models))
	if err != nil {
		t.Fatalf("UpdateTokenPermission(restrict) error = %v, want nil", err)
	}
	retrieved, _ := store.GetTokenByID(ctx, created.ID)
	if len(retrieved.AllowedModels) != 2 || retrieved.AllowedModels[0] != "model-a" {
		t.Errorf("AllowedModels = %v, want [model-a model-b]", retrieved.AllowedModels)
	}

	// An OMITTING update (both axes nil) writes NO column — it keeps the
	// stored value (column-skip merge, plan §6.1 PD-3). Under the frozen wire
	// contract an axis can only return to ABSENT via creation; a PATCH that
	// says nothing about a grant axis must never widen it.
	err = store.UpdateTokenPermission(ctx, created.ID, OptionalTokenPermission{})
	if err != nil {
		t.Fatalf("UpdateTokenPermission(omit both) error = %v, want nil", err)
	}
	retrieved, _ = store.GetTokenByID(ctx, created.ID)
	if len(retrieved.AllowedModels) != 2 || retrieved.AllowedModels[0] != "model-a" {
		t.Errorf("AllowedModels after an omitting update = %v, want [model-a model-b] (unchanged)", retrieved.AllowedModels)
	}
}

func TestUpdateTokenPermissionNotFound(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewTokenStore(db.DB, database.SQLite)
	ctx := context.Background()

	// A REAL update (at least one non-nil axis) against a missing id is a
	// 404-class error.
	err := store.UpdateTokenPermission(ctx, "nonexistent-id", GrantGroups([]string{"g1"}))
	if err != ErrTokenNotFound {
		t.Errorf("UpdateTokenPermission(real update) error = %v, want ErrTokenNotFound", err)
	}

	// The NO-OP arm (both axes nil) executes no UPDATE at all, so it cannot
	// observe row existence: it is a SUCCESS. The 404 contract lives at the
	// API layer's GetTokenByID pre-check (pinned by
	// TestTokenAPI_PatchNonexistent_404), never on RowsAffected.
	if err := store.UpdateTokenPermission(ctx, "nonexistent-id", OptionalTokenPermission{}); err != nil {
		t.Errorf("UpdateTokenPermission(no-op arm) error = %v, want nil (no UPDATE is executed; 404 belongs to the API pre-check)", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Allowed Models Integration Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestCreateTokenWithAllowedModels(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewTokenStore(db.DB, database.SQLite)
	ctx := context.Background()

	allowedModels := []string{"gpt-4", "claude-3"}
	_, token, err := store.CreateToken(ctx, "allowed-models-test", nil, "admin", FullPermission(allowedModels, nil))
	if err != nil {
		t.Fatalf("CreateToken() error = %v, want nil", err)
	}

	if token == nil {
		t.Fatal("token is nil")
	}

	if len(token.AllowedModels) != 2 {
		t.Errorf("token.AllowedModels length = %d, want 2", len(token.AllowedModels))
	}

	if token.AllowedModels[0] != "gpt-4" || token.AllowedModels[1] != "claude-3" {
		t.Errorf("token.AllowedModels = %v, want [gpt-4, claude-3]", token.AllowedModels)
	}
}

func TestValidateTokenReturnsAllowedModels(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewTokenStore(db.DB, database.SQLite)
	ctx := context.Background()

	allowedModels := []string{"gpt-4", "gemini-pro"}
	plaintext, _, err := store.CreateToken(ctx, "validate-allowed", nil, "admin", FullPermission(allowedModels, nil))
	if err != nil {
		t.Fatalf("CreateToken() error = %v, want nil", err)
	}

	// Validate and check AllowedModels is returned
	validated, err := store.ValidateToken(ctx, plaintext)
	if err != nil {
		t.Fatalf("ValidateToken() error = %v, want nil", err)
	}

	if len(validated.AllowedModels) != 2 {
		t.Errorf("validated.AllowedModels length = %d, want 2", len(validated.AllowedModels))
	}

	if validated.AllowedModels[0] != "gpt-4" || validated.AllowedModels[1] != "gemini-pro" {
		t.Errorf("validated.AllowedModels = %v, want [gpt-4, gemini-pro]", validated.AllowedModels)
	}
}

func TestListTokensReturnsAllowedModels(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewTokenStore(db.DB, database.SQLite)
	ctx := context.Background()

	// Create tokens with different allowed_models
	_, _, err := store.CreateToken(ctx, "list-allowed-1", nil, "admin", FullPermission([]string{"gpt-4"}, nil))
	if err != nil {
		t.Fatalf("CreateToken(1) error = %v", err)
	}

	_, _, err = store.CreateToken(ctx, "list-allowed-2", nil, "admin", FullPermission([]string{"claude-3", "gemini-pro"}, nil))
	if err != nil {
		t.Fatalf("CreateToken(2) error = %v", err)
	}

	_, _, err = store.CreateToken(ctx, "list-allowed-nil", nil, "admin", FullPermission(nil, nil))
	if err != nil {
		t.Fatalf("CreateToken(3) error = %v", err)
	}

	tokens, err := store.ListTokens(ctx)
	if err != nil {
		t.Fatalf("ListTokens() error = %v", err)
	}

	if len(tokens) != 3 {
		t.Fatalf("len(tokens) = %d, want 3", len(tokens))
	}

	// Find and verify each token's AllowedModels
	found := make(map[string][]string)
	for _, token := range tokens {
		found[token.Name] = token.AllowedModels
	}

	// Token with single model
	if len(found["list-allowed-1"]) != 1 || found["list-allowed-1"][0] != "gpt-4" {
		t.Errorf("list-allowed-1.AllowedModels = %v, want [gpt-4]", found["list-allowed-1"])
	}

	// Token with multiple models
	if len(found["list-allowed-2"]) != 2 {
		t.Errorf("list-allowed-2.AllowedModels length = %d, want 2", len(found["list-allowed-2"]))
	}

	// Token with nil models
	if found["list-allowed-nil"] != nil {
		t.Errorf("list-allowed-nil.AllowedModels = %v, want nil", found["list-allowed-nil"])
	}
}

func TestGetTokenByIDReturnsAllowedModels(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewTokenStore(db.DB, database.SQLite)
	ctx := context.Background()

	allowedModels := []string{"o3-mini", "o1", "claude-sonnet"}
	_, created, err := store.CreateToken(ctx, "get-allowed-test", nil, "admin", FullPermission(allowedModels, nil))
	if err != nil {
		t.Fatalf("CreateToken() error = %v", err)
	}

	// Get by ID
	retrieved, err := store.GetTokenByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetTokenByID() error = %v, want nil", err)
	}

	if len(retrieved.AllowedModels) != 3 {
		t.Errorf("retrieved.AllowedModels length = %d, want 3", len(retrieved.AllowedModels))
	}

	if retrieved.AllowedModels[0] != "o3-mini" || retrieved.AllowedModels[1] != "o1" || retrieved.AllowedModels[2] != "claude-sonnet" {
		t.Errorf("retrieved.AllowedModels = %v, want [o3-mini, o1, claude-sonnet]", retrieved.AllowedModels)
	}
}

func TestUpdateTokenPermissionWithAllowedModels(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewTokenStore(db.DB, database.SQLite)
	ctx := context.Background()

	// Create token with nil allowed_models
	_, created, err := store.CreateToken(ctx, "update-allowed-test", nil, "admin", FullPermission(nil, nil))
	if err != nil {
		t.Fatalf("CreateToken() error = %v", err)
	}

	// Verify initial state
	retrieved, _ := store.GetTokenByID(ctx, created.ID)
	if retrieved.AllowedModels != nil {
		t.Errorf("Initial AllowedModels = %v, want nil", retrieved.AllowedModels)
	}

	// Update with allowed_models
	newAllowedModels := []string{"gpt-4-turbo", "claude-3-5-sonnet"}
	err = store.UpdateTokenPermission(ctx, created.ID, GrantModels(newAllowedModels))
	if err != nil {
		t.Fatalf("UpdateTokenPermission() error = %v, want nil", err)
	}

	// Verify update
	retrieved, _ = store.GetTokenByID(ctx, created.ID)
	if len(retrieved.AllowedModels) != 2 {
		t.Errorf("After update AllowedModels length = %d, want 2", len(retrieved.AllowedModels))
	}
	if retrieved.AllowedModels[0] != "gpt-4-turbo" || retrieved.AllowedModels[1] != "claude-3-5-sonnet" {
		t.Errorf("After update AllowedModels = %v, want [gpt-4-turbo, claude-3-5-sonnet]", retrieved.AllowedModels)
	}
}

func TestUpdateTokenPermissionClearAllowedModels(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewTokenStore(db.DB, database.SQLite)
	ctx := context.Background()

	// Create token with allowed_models
	_, created, err := store.CreateToken(ctx, "clear-allowed-test", nil, "admin", FullPermission([]string{"gpt-4"}, nil))
	if err != nil {
		t.Fatalf("CreateToken() error = %v", err)
	}

	// Verify initial state
	retrieved, _ := store.GetTokenByID(ctx, created.ID)
	if len(retrieved.AllowedModels) != 1 {
		t.Errorf("Initial AllowedModels length = %d, want 1", len(retrieved.AllowedModels))
	}

	// Clear with an EXPLICIT EMPTY slice: under the explicit-empty encoding
	// (plan §2.8 / decisions.md Q13) this writes '[]' — PRESENT-and-empty,
	// which grants NOTHING. It is deliberately NOT nil: under the conjunctive
	// wildcard rule nil+ABSENT-everywhere means UNRESTRICTED, so collapsing a
	// clear into NULL would escalate the token (R-6).
	err = store.UpdateTokenPermission(ctx, created.ID, GrantModels([]string{}))
	if err != nil {
		t.Fatalf("UpdateTokenPermission() error = %v, want nil", err)
	}

	// Verify cleared-to-explicit-empty (never nil).
	retrieved, _ = store.GetTokenByID(ctx, created.ID)
	if retrieved.AllowedModels == nil {
		t.Fatalf("After clear AllowedModels is nil — an explicit-empty clear must never become ABSENT (wildcard escalation)")
	}
	if len(retrieved.AllowedModels) != 0 {
		t.Errorf("After clear AllowedModels = %v, want empty (explicit-empty)", retrieved.AllowedModels)
	}
}

func TestCreateTokenWithEmptyAllowedModels(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewTokenStore(db.DB, database.SQLite)
	ctx := context.Background()

	// Create token with empty slice
	_, token, err := store.CreateToken(ctx, "empty-allowed-test", nil, "admin", FullPermission([]string{}, nil))
	if err != nil {
		t.Fatalf("CreateToken() error = %v, want nil", err)
	}

	// Empty slice serializes to '[]' (PRESENT-and-empty), NOT NULL.
	// After retrieval it is a non-nil empty slice — the fail-open class this
	// commission kills is precisely "empty reads back as nil => wildcard".
	plaintext, _, _ := store.CreateToken(ctx, "for-validate", nil, "admin", FullPermission([]string{}, nil))
	validated, err := store.ValidateToken(ctx, plaintext)
	if err != nil {
		t.Fatalf("ValidateToken() error = %v", err)
	}

	// Empty slice stored returns non-nil-empty (PRESENT, grants nothing).
	if validated.AllowedModels == nil {
		t.Fatalf("Empty []string stored returned nil — '[]' must read back as PRESENT-empty, never ABSENT")
	}
	if len(validated.AllowedModels) != 0 {
		t.Errorf("Empty []string stored returns %v, want empty", validated.AllowedModels)
	}

	// Original token object has empty slice (before DB round-trip)
	if token.AllowedModels == nil {
		t.Errorf("token.AllowedModels should be empty slice, got nil")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Ultimate Model ID Tests
// ─────────────────────────────────────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────────────────────
// Token grant axes — tri-state persistence (token-authz plan T1.11)
// ─────────────────────────────────────────────────────────────────────────────

// seedToken inserts an auth_tokens row with the given raw grant column values
// (nil ⇒ SQL NULL), so a test can pin exact stored bytes.
func seedToken(t *testing.T, db *sql.DB, id string, allowedModels, allowedGroups interface{}) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO auth_tokens (id, token_hash, name, expires_at, created_at, created_by, allowed_models, allowed_groups) VALUES (?, ?, ?, NULL, ?, 'seed', ?, ?)`,
		id, "hash-"+id, id, time.Now().Format(time.RFC3339), allowedModels, allowedGroups,
	); err != nil {
		t.Fatalf("insert auth_tokens row %s: %v", id, err)
	}
}

// rawGrantColumns reads the two grant columns verbatim (as the driver returns
// them) so a test can assert NULL vs '[]' vs a JSON array rather than a
// round-tripped Go value.
func rawGrantColumns(t *testing.T, db *sql.DB, id string) (models, groups interface{}) {
	t.Helper()
	if err := db.QueryRow(`SELECT allowed_models, allowed_groups FROM auth_tokens WHERE id = ?`, id).
		Scan(&models, &groups); err != nil {
		t.Fatalf("read raw grant columns for %s: %v", id, err)
	}
	return models, groups
}

// rawString renders a driver raw value for assertions.
func rawString(v interface{}) string {
	switch x := v.(type) {
	case nil:
		return "<NULL>"
	case string:
		return x
	case []byte:
		return string(x)
	default:
		return fmt.Sprintf("<unsupported %T>", v)
	}
}

// TestCreateToken_GrantAxesRoundTrip — a group-only grant stores NULL on the
// model axis and a JSON array on the group axis; reading it back yields the
// nil-vs-PRESENT distinction intact.
func TestCreateToken_GrantAxesRoundTrip(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewTokenStore(db.DB, database.SQLite)
	ctx := context.Background()

	_, token, err := store.CreateToken(ctx, "group-only", nil, "admin", FullPermission(nil, []string{"g1"}))
	if err != nil {
		t.Fatalf("CreateToken() error = %v", err)
	}

	rawModels, rawGroups := rawGrantColumns(t, db.DB, token.ID)
	if got := rawString(rawModels); got != "<NULL>" {
		t.Errorf("allowed_models column = %q, want NULL (ABSENT axis)", got)
	}
	if got := rawString(rawGroups); got != `["g1"]` {
		t.Errorf("allowed_groups column = %q, want [\"g1\"]", got)
	}

	read, err := store.GetTokenByID(ctx, token.ID)
	if err != nil {
		t.Fatalf("GetTokenByID() error = %v", err)
	}
	if read.AllowedModels != nil {
		t.Errorf("AllowedModels = %v, want nil (ABSENT)", read.AllowedModels)
	}
	if len(read.AllowedGroups) != 1 || read.AllowedGroups[0] != "g1" {
		t.Errorf("AllowedGroups = %v, want [g1]", read.AllowedGroups)
	}
	if read.ModelsInvalid || read.GroupsInvalid {
		t.Errorf("clean row flagged invalid: ModelsInvalid=%v GroupsInvalid=%v", read.ModelsInvalid, read.GroupsInvalid)
	}
}

// TestCreateToken_ExplicitEmptyIsNotNull — the escalation trap (R-6/Q13): an
// explicit empty list must persist as '[]' (PRESENT-and-empty, grants nothing),
// NEVER as NULL (ABSENT — which under the conjunctive wildcard rule could mean
// unrestricted when the other axis is absent too).
func TestCreateToken_ExplicitEmptyIsNotNull(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewTokenStore(db.DB, database.SQLite)
	ctx := context.Background()

	_, token, err := store.CreateToken(ctx, "explicit-empty", nil, "admin", FullPermission([]string{}, nil))
	if err != nil {
		t.Fatalf("CreateToken() error = %v", err)
	}

	rawModels, rawGroups := rawGrantColumns(t, db.DB, token.ID)
	if got := rawString(rawModels); got != "[]" {
		t.Fatalf("allowed_models column = %q, want '[]' — an explicit-empty clear must never collapse to NULL (wildcard escalation)", got)
	}
	if got := rawString(rawGroups); got != "<NULL>" {
		t.Errorf("allowed_groups column = %q, want NULL (omitted axis)", got)
	}

	read, err := store.GetTokenByID(ctx, token.ID)
	if err != nil {
		t.Fatalf("GetTokenByID() error = %v", err)
	}
	if read.AllowedModels == nil {
		t.Fatal("read-back AllowedModels is nil — '[]' must read back as PRESENT-empty")
	}
	if len(read.AllowedModels) != 0 {
		t.Errorf("read-back AllowedModels = %v, want empty", read.AllowedModels)
	}
}

// TestValidateToken_SurfacesInvalidFlags — an unreadable stored row (malformed
// JSON touching BOTH parse branches) yields the INVALID flags and never a
// grant list. This is the fail-closed landing the whole phase exists for.
func TestValidateToken_SurfacesInvalidFlags(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewTokenStore(db.DB, database.SQLite)
	ctx := context.Background()

	// Insert a token row by hand with corrupt grant bytes.
	plaintext, hash, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if _, err := db.DB.Exec(
		`INSERT INTO auth_tokens (id, token_hash, name, expires_at, created_at, created_by, allowed_models, allowed_groups) VALUES (?, ?, ?, NULL, ?, ?, ?, ?)`,
		"corrupt-1", hash, "corrupt", time.Now().Format(time.RFC3339), "admin", `not-json`, `{"not":"an array"}`,
	); err != nil {
		t.Fatalf("insert corrupt row: %v", err)
	}

	tok, err := store.ValidateToken(ctx, plaintext)
	if err != nil {
		t.Fatalf("ValidateToken() error = %v", err)
	}
	if !tok.ModelsInvalid {
		t.Error("ModelsInvalid = false, want true for a malformed allowed_models row")
	}
	if !tok.GroupsInvalid {
		t.Error("GroupsInvalid = false, want true for a non-array allowed_groups row")
	}
	if tok.AllowedModels != nil {
		t.Errorf("AllowedModels = %v, want nil (an INVALID axis carries no ids)", tok.AllowedModels)
	}
	if tok.AllowedGroups != nil {
		t.Errorf("AllowedGroups = %v, want nil (an INVALID axis carries no ids)", tok.AllowedGroups)
	}

	// The same row through the DDL-free reader (GetTokenByID) must agree.
	byID, err := store.GetTokenByID(ctx, "corrupt-1")
	if err != nil {
		t.Fatalf("GetTokenByID() error = %v", err)
	}
	if !byID.ModelsInvalid || !byID.GroupsInvalid {
		t.Errorf("GetTokenByID lost the INVALID flags: %+v", byID)
	}

	// And through ListTokens.
	listed, err := store.ListTokens(ctx)
	if err != nil {
		t.Fatalf("ListTokens() error = %v", err)
	}
	if len(listed) != 1 || !listed[0].ModelsInvalid || !listed[0].GroupsInvalid {
		t.Errorf("ListTokens lost the INVALID flags: %+v", listed)
	}
}

// TestUpdateTokenPermission_BothAxes — each axis is written independently; a
// nil axis is SKIPPED (column-skip merge), which is what makes an omitting
// PATCH preserve a DENY-ALL axis instead of healing it to NULL.
func TestUpdateTokenPermission_BothAxes(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewTokenStore(db.DB, database.SQLite)
	ctx := context.Background()

	_, token, err := store.CreateToken(ctx, "both-axes", nil, "admin", FullPermission([]string{"m1"}, []string{"g1"}))
	if err != nil {
		t.Fatalf("CreateToken() error = %v", err)
	}

	// Rewrite ONLY the group axis.
	if err := store.UpdateTokenPermission(ctx, token.ID, GrantGroups([]string{"g2", "g3"})); err != nil {
		t.Fatalf("UpdateTokenPermission(groups only) error = %v", err)
	}
	read, err := store.GetTokenByID(ctx, token.ID)
	if err != nil {
		t.Fatalf("GetTokenByID() error = %v", err)
	}
	if len(read.AllowedModels) != 1 || read.AllowedModels[0] != "m1" {
		t.Errorf("AllowedModels = %v, want [m1] (axis untouched by an omitting update)", read.AllowedModels)
	}
	if len(read.AllowedGroups) != 2 || read.AllowedGroups[0] != "g2" || read.AllowedGroups[1] != "g3" {
		t.Errorf("AllowedGroups = %v, want [g2 g3]", read.AllowedGroups)
	}

	// Rewrite ONLY the model axis, explicitly empty.
	if err := store.UpdateTokenPermission(ctx, token.ID, GrantModels([]string{})); err != nil {
		t.Fatalf("UpdateTokenPermission(models empty) error = %v", err)
	}
	read, err = store.GetTokenByID(ctx, token.ID)
	if err != nil {
		t.Fatalf("GetTokenByID() error = %v", err)
	}
	if read.AllowedModels == nil || len(read.AllowedModels) != 0 {
		t.Errorf("AllowedModels = %v, want non-nil empty", read.AllowedModels)
	}
	if len(read.AllowedGroups) != 2 {
		t.Errorf("AllowedGroups = %v, want [g2 g3] (untouched)", read.AllowedGroups)
	}
}

// TestUpdateTokenPermission_OmitsNilColumn is the PD-3 store-level pin.
//
// It asserts all three halves of the column-skip contract:
//  1. an omitted axis leaves the OTHER column's raw bytes byte-identical;
//  2. a perm with BOTH axes nil executes no UPDATE at all and returns nil
//     (a no-op is a success — the 404 contract lives at the API pre-check);
//  3. a malformed (DENY-ALL) allowed_models row is NEVER healed by a
//     groups-only update — the corrupt bytes stay corrupt, so the token keeps
//     resolving to deny-all instead of silently becoming a wildcard.
func TestUpdateTokenPermission_OmitsNilColumn(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewTokenStore(db.DB, database.SQLite)
	ctx := context.Background()

	// ── half 1: an omitted axis leaves the other column byte-identical ───────
	seedToken(t, db.DB, "byte-identical", `["m-keep"]`, `["g-keep"]`)
	beforeModels, _ := rawGrantColumns(t, db.DB, "byte-identical")
	if err := store.UpdateTokenPermission(ctx, "byte-identical", GrantGroups([]string{"g-new"})); err != nil {
		t.Fatalf("UpdateTokenPermission(groups only) error = %v", err)
	}
	afterModels, afterGroups := rawGrantColumns(t, db.DB, "byte-identical")
	if rawString(afterModels) != rawString(beforeModels) {
		t.Errorf("allowed_models = %q, want %q (the omitted axis must not be written at all)",
			rawString(afterModels), rawString(beforeModels))
	}
	if rawString(afterGroups) != `["g-new"]` {
		t.Errorf("allowed_groups = %q, want [\"g-new\"] (the supplied axis DID land)", rawString(afterGroups))
	}

	// ── half 2: both axes nil ⇒ no UPDATE, nil error, nothing changes ────────
	if err := store.UpdateTokenPermission(ctx, "byte-identical", OptionalTokenPermission{}); err != nil {
		t.Fatalf("UpdateTokenPermission(no-op) error = %v, want nil (a no-op is success, not ErrTokenNotFound)", err)
	}
	finalModels, finalGroups := rawGrantColumns(t, db.DB, "byte-identical")
	if rawString(finalModels) != rawString(afterModels) || rawString(finalGroups) != rawString(afterGroups) {
		t.Errorf("a both-nil perm changed the row: models %q→%q, groups %q→%q",
			rawString(afterModels), rawString(finalModels), rawString(afterGroups), rawString(finalGroups))
	}

	// ── half 3: a malformed DENY-ALL row is never healed ────────────────────
	if _, err := db.DB.Exec(
		`INSERT INTO auth_tokens (id, token_hash, name, expires_at, created_at, created_by, allowed_models, allowed_groups) VALUES (?, ?, ?, NULL, ?, ?, ?, NULL)`,
		"deny-all-1", "hash-deny-all-1", "deny", time.Now().Format(time.RFC3339), "admin", `[broken`,
	); err != nil {
		t.Fatalf("insert corrupt row: %v", err)
	}

	before, _ := rawGrantColumns(t, db.DB, "deny-all-1")
	if got := rawString(before); got != "[broken" {
		t.Fatalf("precondition: allowed_models = %q, want the corrupt bytes", got)
	}

	// PATCH that touches ONLY the group axis.
	if err := store.UpdateTokenPermission(ctx, "deny-all-1", GrantGroups([]string{"g1"})); err != nil {
		t.Fatalf("UpdateTokenPermission() error = %v", err)
	}

	after, rawGroups := rawGrantColumns(t, db.DB, "deny-all-1")
	if got := rawString(after); got != "[broken" {
		t.Fatalf("allowed_models = %q after an omitting update — the corrupt (DENY-ALL) bytes were healed; a keep must never overwrite them", got)
	}
	if got := rawString(rawGroups); got != `["g1"]` {
		t.Errorf("allowed_groups = %q, want [\"g1\"] (the supplied axis DID land)", got)
	}

	// The row still reads as DENY-ALL.
	read, err := store.GetTokenByID(ctx, "deny-all-1")
	if err != nil {
		t.Fatalf("GetTokenByID() error = %v", err)
	}
	if !read.ModelsInvalid {
		t.Error("ModelsInvalid = false after an omitting update — DENY-ALL must survive")
	}

	// The operator's explicit repair path still works.
	if err := store.UpdateTokenPermission(ctx, "deny-all-1", GrantModels([]string{"m1"})); err != nil {
		t.Fatalf("UpdateTokenPermission(repair) error = %v", err)
	}
	read, err = store.GetTokenByID(ctx, "deny-all-1")
	if err != nil {
		t.Fatalf("GetTokenByID() error = %v", err)
	}
	if read.ModelsInvalid {
		t.Error("ModelsInvalid still true after an explicit valid array — the repair path is broken")
	}
	if len(read.AllowedModels) != 1 || read.AllowedModels[0] != "m1" {
		t.Errorf("AllowedModels = %v, want [m1] after repair", read.AllowedModels)
	}
}

// TestUpdateTokenPermission_RoundTrip is the fix-pass-2 TWO-STATE pin (leader
// Ruling 2 / Q13(a)) — every reachable update state, asserted on the raw
// stored bytes:
//
//	Set:false (wire: key omitted OR explicit null)  => column BYTE-IDENTICAL
//	Set:true, Values:&[]string{}                    => '[]'  (PRESENT-empty)
//	Set:true, Values:&ids                           => the JSON array
//
// and the malformed DENY-ALL row survives an untouched axis unchanged, so a
// PATCH that says nothing about an axis can never heal it into NULL/wildcard.
func TestUpdateTokenPermission_RoundTrip(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewTokenStore(db.DB, database.SQLite)
	ctx := context.Background()

	// ── state 1: omitted axis (Set:false) is byte-identical ─────────────────
	seedToken(t, db.DB, "rt-1", `["m-keep"]`, `["g-keep"]`)

	// Wire-level "explicit null" and "key omitted" BOTH arrive as Set:false —
	// the store cannot and must not distinguish them (Go pointer fields decode
	// both to nil), so this single assertion covers both.
	if err := store.UpdateTokenPermission(ctx, "rt-1", GrantGroups([]string{"g-new"})); err != nil {
		t.Fatalf("omitted model axis: %v", err)
	}
	rawModels, rawGroups := rawGrantColumns(t, db.DB, "rt-1")
	if rawString(rawModels) != `["m-keep"]` {
		t.Errorf("allowed_models = %q after an omitted axis, want the original bytes [\"m-keep\"]", rawString(rawModels))
	}
	if rawString(rawGroups) != `["g-new"]` {
		t.Errorf("allowed_groups = %q, want [\"g-new\"]", rawString(rawGroups))
	}

	// ── state 2: explicit [] => '[]' (PRESENT-empty, never NULL) ────────────
	if err := store.UpdateTokenPermission(ctx, "rt-1", GrantGroups([]string{})); err != nil {
		t.Fatalf("explicit empty: %v", err)
	}
	_, rawGroups = rawGrantColumns(t, db.DB, "rt-1")
	if rawString(rawGroups) != "[]" {
		t.Errorf("allowed_groups = %q after an explicit [] — want '[]' (PRESENT-empty); NULL here would be an escalation", rawString(rawGroups))
	}

	// ── state 3: ids replace ────────────────────────────────────────────────
	if err := store.UpdateTokenPermission(ctx, "rt-1", GrantModels([]string{"m-a", "m-b"})); err != nil {
		t.Fatalf("ids: %v", err)
	}
	rawModels, _ = rawGrantColumns(t, db.DB, "rt-1")
	if rawString(rawModels) != `["m-a","m-b"]` {
		t.Errorf("allowed_models = %q, want [\"m-a\",\"m-b\"]", rawString(rawModels))
	}

	// ── state 4: a malformed (DENY-ALL) axis survives an unrelated update ───
	seedToken(t, db.DB, "rt-deny", `[broken`, nil)
	if err := store.UpdateTokenPermission(ctx, "rt-deny", GrantGroups([]string{"g1"})); err != nil {
		t.Fatalf("groups-only on a corrupt row: %v", err)
	}
	rawModels, _ = rawGrantColumns(t, db.DB, "rt-deny")
	if rawString(rawModels) != "[broken" {
		t.Fatalf("allowed_models = %q, want the corrupt bytes untouched — an omitted axis must never heal DENY-ALL", rawString(rawModels))
	}
	denied, err := store.GetTokenByID(ctx, "rt-deny")
	if err != nil {
		t.Fatalf("GetTokenByID: %v", err)
	}
	if !denied.ModelsInvalid {
		t.Error("ModelsInvalid = false after an unrelated update — DENY-ALL must survive the round trip")
	}

	// ── state 5: both axes Set:false => no UPDATE at all, nil error ─────────
	if err := store.UpdateTokenPermission(ctx, "rt-1", OptionalTokenPermission{}); err != nil {
		t.Fatalf("both-omitted: %v (a no-op is a success)", err)
	}
	finalModels, finalGroups := rawGrantColumns(t, db.DB, "rt-1")
	if rawString(finalModels) != `["m-a","m-b"]` || rawString(finalGroups) != "[]" {
		t.Errorf("a both-omitted perm changed the row: models=%q groups=%q", rawString(finalModels), rawString(finalGroups))
	}
}
