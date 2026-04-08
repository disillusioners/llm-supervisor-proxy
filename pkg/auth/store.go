package auth

import (
	"context"
	"database/sql"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
	"github.com/google/uuid"
)

// TokenStore manages auth tokens in the database
type TokenStore struct {
	db      *sql.DB
	dialect database.Dialect
}

// TokenStoreInterface defines the interface for token operations
// This allows for mocking in tests
type TokenStoreInterface interface {
	ValidateToken(ctx context.Context, plaintext string) (*AuthToken, error)
	CreateToken(ctx context.Context, name string, expiresAt *time.Time, createdBy string, ultimateModelEnabled bool) (string, *AuthToken, error)
	DeleteToken(ctx context.Context, id string) error
	ListTokens(ctx context.Context) ([]AuthToken, error)
	UpdateTokenPermission(ctx context.Context, id string, ultimateModelEnabled bool) error
}

// Ensure TokenStore implements TokenStoreInterface at compile time
var _ TokenStoreInterface = (*TokenStore)(nil)

// NewTokenStore creates a new token store
func NewTokenStore(db *sql.DB, dialect database.Dialect) *TokenStore {
	return &TokenStore{db: db, dialect: dialect}
}

// CreateToken creates a new API token
// Returns the plaintext token (show once) and the stored token info
func (s *TokenStore) CreateToken(ctx context.Context, name string, expiresAt *time.Time, createdBy string, ultimateModelEnabled bool) (string, *AuthToken, error) {
	plaintext, hash, err := GenerateToken()
	if err != nil {
		return "", nil, err
	}

	id := uuid.New().String()
	now := time.Now()

	var expiresAtStr interface{}
	if expiresAt != nil {
		expiresAtStr = expiresAt.Format(time.RFC3339)
	}

	var query string
	if s.dialect == database.PostgreSQL {
		query = `INSERT INTO auth_tokens (id, token_hash, name, expires_at, created_at, created_by, ultimate_model_enabled) VALUES ($1, $2, $3, $4, $5, $6, $7)`
	} else {
		query = `INSERT INTO auth_tokens (id, token_hash, name, expires_at, created_at, created_by, ultimate_model_enabled) VALUES (?, ?, ?, ?, ?, ?, ?)`
	}

	_, err = s.db.ExecContext(ctx, query, id, hash, name, expiresAtStr, now.Format(time.RFC3339), createdBy, ultimateModelEnabled)
	if err != nil {
		return "", nil, err
	}

	token := &AuthToken{
		ID:                   id,
		Name:                 name,
		TokenHash:            hash,
		ExpiresAt:            expiresAt,
		CreatedAt:            now,
		CreatedBy:            createdBy,
		UltimateModelEnabled: ultimateModelEnabled,
	}

	return plaintext, token, nil
}

// ValidateToken validates a token and returns its info
func (s *TokenStore) ValidateToken(ctx context.Context, plaintext string) (*AuthToken, error) {
	if !ValidateTokenFormat(plaintext) {
		return nil, ErrInvalidTokenFormat
	}

	hash := HashToken(plaintext)

	token := &AuthToken{}
	var expiresAtStr sql.NullString
	var createdAtStr string

	var query string
	if s.dialect == database.PostgreSQL {
		query = `SELECT id, token_hash, name, expires_at, created_at, created_by, ultimate_model_enabled FROM auth_tokens WHERE token_hash = $1`
	} else {
		query = `SELECT id, token_hash, name, expires_at, created_at, created_by, ultimate_model_enabled FROM auth_tokens WHERE token_hash = ?`
	}

	err := s.db.QueryRowContext(ctx, query, hash).Scan(&token.ID, &token.TokenHash, &token.Name, &expiresAtStr, &createdAtStr, &token.CreatedBy, &token.UltimateModelEnabled)

	if err == sql.ErrNoRows {
		return nil, ErrTokenNotFound
	}
	if err != nil {
		return nil, err
	}

	// Parse created_at string to time.Time
	if createdAtStr != "" {
		t, err := time.Parse(time.RFC3339, createdAtStr)
		if err == nil {
			token.CreatedAt = t
		}
	}

	if expiresAtStr.Valid {
		t, err := time.Parse(time.RFC3339, expiresAtStr.String)
		if err == nil {
			token.ExpiresAt = &t
		}
	}

	if token.IsExpired() {
		return nil, ErrTokenExpired
	}

	return token, nil
}

// ListTokens returns all tokens (without hashes)
func (s *TokenStore) ListTokens(ctx context.Context) ([]AuthToken, error) {
	var query string
	if s.dialect == database.PostgreSQL {
		query = `SELECT id, name, expires_at, created_at, created_by, ultimate_model_enabled FROM auth_tokens ORDER BY created_at DESC`
	} else {
		query = `SELECT id, name, expires_at, created_at, created_by, ultimate_model_enabled FROM auth_tokens ORDER BY created_at DESC`
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tokens []AuthToken
	for rows.Next() {
		token := AuthToken{}
		var expiresAtStr sql.NullString
		var createdAtStr string

		err := rows.Scan(&token.ID, &token.Name, &expiresAtStr, &createdAtStr, &token.CreatedBy, &token.UltimateModelEnabled)
		if err != nil {
			return nil, err
		}

		if expiresAtStr.Valid {
			t, err := time.Parse(time.RFC3339, expiresAtStr.String)
			if err == nil {
				token.ExpiresAt = &t
			}
		}

		t, _ := time.Parse(time.RFC3339, createdAtStr)
		token.CreatedAt = t

		tokens = append(tokens, token)
	}

	return tokens, rows.Err()
}

// DeleteToken removes a token by ID
func (s *TokenStore) DeleteToken(ctx context.Context, id string) error {
	var query string
	if s.dialect == database.PostgreSQL {
		query = `DELETE FROM auth_tokens WHERE id = $1`
	} else {
		query = `DELETE FROM auth_tokens WHERE id = ?`
	}

	result, err := s.db.ExecContext(ctx, query, id)
	if err != nil {
		return err
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrTokenNotFound
	}

	return nil
}

// GetTokenByID returns a token by ID (without hash)
func (s *TokenStore) GetTokenByID(ctx context.Context, id string) (*AuthToken, error) {
	token := &AuthToken{}
	var expiresAtStr sql.NullString
	var createdAtStr string

	var query string
	if s.dialect == database.PostgreSQL {
		query = `SELECT id, name, expires_at, created_at, created_by, ultimate_model_enabled FROM auth_tokens WHERE id = $1`
	} else {
		query = `SELECT id, name, expires_at, created_at, created_by, ultimate_model_enabled FROM auth_tokens WHERE id = ?`
	}

	err := s.db.QueryRowContext(ctx, query, id).Scan(&token.ID, &token.Name, &expiresAtStr, &createdAtStr, &token.CreatedBy, &token.UltimateModelEnabled)

	if err == sql.ErrNoRows {
		return nil, ErrTokenNotFound
	}
	if err != nil {
		return nil, err
	}

	if expiresAtStr.Valid {
		t, err := time.Parse(time.RFC3339, expiresAtStr.String)
		if err == nil {
			token.ExpiresAt = &t
		}
	}

	t, _ := time.Parse(time.RFC3339, createdAtStr)
	token.CreatedAt = t

	return token, nil
}

// UpdateTokenPermission updates the ultimate_model_enabled flag for a token
func (s *TokenStore) UpdateTokenPermission(ctx context.Context, id string, ultimateModelEnabled bool) error {
	var query string
	if s.dialect == database.PostgreSQL {
		query = `UPDATE auth_tokens SET ultimate_model_enabled = $1 WHERE id = $2`
	} else {
		query = `UPDATE auth_tokens SET ultimate_model_enabled = ? WHERE id = ?`
	}

	result, err := s.db.ExecContext(ctx, query, ultimateModelEnabled, id)
	if err != nil {
		return err
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrTokenNotFound
	}

	return nil
}
