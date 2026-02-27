package auth

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// TokenStore manages auth tokens in the database
type TokenStore struct {
	db *sql.DB
}

// NewTokenStore creates a new token store
func NewTokenStore(db *sql.DB) *TokenStore {
	return &TokenStore{db: db}
}

// CreateToken creates a new API token
// Returns the plaintext token (show once) and the stored token info
func (s *TokenStore) CreateToken(ctx context.Context, name string, expiresAt *time.Time, createdBy string) (string, *AuthToken, error) {
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

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO auth_tokens (id, token_hash, name, expires_at, created_at, created_by)
		VALUES (?, ?, ?, ?, ?, ?)`,
		id, hash, name, expiresAtStr, now.Format(time.RFC3339), createdBy,
	)
	if err != nil {
		return "", nil, err
	}

	token := &AuthToken{
		ID:        id,
		Name:      name,
		TokenHash: hash,
		ExpiresAt: expiresAt,
		CreatedAt: now,
		CreatedBy: createdBy,
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

	err := s.db.QueryRowContext(ctx, `
		SELECT id, token_hash, name, expires_at, created_at, created_by
		FROM auth_tokens WHERE token_hash = ?`,
		hash,
	).Scan(&token.ID, &token.TokenHash, &token.Name, &expiresAtStr, &token.CreatedAt, &token.CreatedBy)

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

	if token.IsExpired() {
		return nil, ErrTokenExpired
	}

	return token, nil
}

// ListTokens returns all tokens (without hashes)
func (s *TokenStore) ListTokens(ctx context.Context) ([]AuthToken, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, expires_at, created_at, created_by
		FROM auth_tokens ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tokens []AuthToken
	for rows.Next() {
		token := AuthToken{}
		var expiresAtStr sql.NullString
		var createdAtStr string

		err := rows.Scan(&token.ID, &token.Name, &expiresAtStr, &createdAtStr, &token.CreatedBy)
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
	result, err := s.db.ExecContext(ctx, `DELETE FROM auth_tokens WHERE id = ?`, id)
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

	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, expires_at, created_at, created_by
		FROM auth_tokens WHERE id = ?`,
		id,
	).Scan(&token.ID, &token.Name, &expiresAtStr, &createdAtStr, &token.CreatedBy)

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
