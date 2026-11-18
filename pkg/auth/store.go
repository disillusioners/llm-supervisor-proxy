package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
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
//
// The write methods take ONE auth.OptionalTokenPermission payload — never two
// adjacent []string parameters, and one type for both methods (fix-pass 2, A4).
// A positional swap of two slices would be a silent privilege change; a field
// swap is a compile error. The per-axis Set flag is what makes "keep this
// axis" expressible without a sentinel (PD-3 column-skip).
type TokenStoreInterface interface {
	ValidateToken(ctx context.Context, plaintext string) (*AuthToken, error)
	CreateToken(ctx context.Context, name string, expiresAt *time.Time, createdBy string, perm OptionalTokenPermission) (string, *AuthToken, error)
	DeleteToken(ctx context.Context, id string) error
	ListTokens(ctx context.Context) ([]AuthToken, error)
	GetTokenByID(ctx context.Context, id string) (*AuthToken, error)
	UpdateTokenPermission(ctx context.Context, id string, perm OptionalTokenPermission) error
}

// Ensure TokenStore implements TokenStoreInterface at compile time
var _ TokenStoreInterface = (*TokenStore)(nil)

// NewTokenStore creates a new token store
func NewTokenStore(db *sql.DB, dialect database.Dialect) *TokenStore {
	return &TokenStore{db: db, dialect: dialect}
}

// marshalGrant serializes ONE grant axis for storage.
//
// The nil-vs-empty distinction is normative (plan §2.2, decisions.md
// Q7/Q13): nil -> NULL (ABSENT), non-nil (including the empty slice) ->
// a JSON array ('[]' for the empty slice, which is PRESENT-and-empty and
// grants NOTHING on that axis).
//
// The guard MUST be `axis != nil`, never `len(axis) > 0`: under the
// conjunctive wildcard rule (plan §2.3) collapsing nil and empty into NULL
// would make "clear the groups list" write NULL on BOTH axes and silently
// escalate the token to unrestricted (decisions.md R-6 / Q13).
func marshalGrant(axis []string) (interface{}, error) {
	if axis == nil {
		return nil, nil // SQL NULL
	}
	if len(axis) == 0 {
		return "[]", nil
	}
	jsonBytes, err := json.Marshal(axis)
	if err != nil {
		return nil, err
	}
	return string(jsonBytes), nil
}

// CreateToken creates a new API token
// Returns the plaintext token (show once) and the stored token info
func (s *TokenStore) CreateToken(ctx context.Context, name string, expiresAt *time.Time, createdBy string, perm OptionalTokenPermission) (string, *AuthToken, error) {
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

	// CreateToken is a FULL write: both axes are Set by construction
	// (FullPermission), so both columns are written. The value keeps the
	// nil-vs-empty distinction (nil -> NULL/ABSENT, [] -> '[]'/PRESENT-empty).
	allowedModelsJSON, err := marshalGrant(perm.AllowedModels.Value())
	if err != nil {
		return "", nil, err
	}
	allowedGroupsJSON, err := marshalGrant(perm.AllowedGroups.Value())
	if err != nil {
		return "", nil, err
	}

	var query string
	if s.dialect == database.PostgreSQL {
		query = `INSERT INTO auth_tokens (id, token_hash, name, expires_at, created_at, created_by, allowed_models, allowed_groups) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	} else {
		query = `INSERT INTO auth_tokens (id, token_hash, name, expires_at, created_at, created_by, allowed_models, allowed_groups) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	}

	_, err = s.db.ExecContext(ctx, query, id, hash, name, expiresAtStr, now.Format(time.RFC3339), createdBy, allowedModelsJSON, allowedGroupsJSON)
	if err != nil {
		return "", nil, err
	}

	token := &AuthToken{
		ID:            id,
		Name:          name,
		TokenHash:     hash,
		ExpiresAt:     expiresAt,
		CreatedAt:     now,
		CreatedBy:     createdBy,
		AllowedModels: perm.AllowedModels.Value(),
		AllowedGroups: perm.AllowedGroups.Value(),
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
	var allowedModelsRaw, allowedGroupsRaw interface{}

	var query string
	if s.dialect == database.PostgreSQL {
		query = `SELECT id, token_hash, name, expires_at, created_at, created_by, allowed_models, allowed_groups FROM auth_tokens WHERE token_hash = $1`
	} else {
		query = `SELECT id, token_hash, name, expires_at, created_at, created_by, allowed_models, allowed_groups FROM auth_tokens WHERE token_hash = ?`
	}

	err := s.db.QueryRowContext(ctx, query, hash).Scan(&token.ID, &token.TokenHash, &token.Name, &expiresAtStr, &createdAtStr, &token.CreatedBy, &allowedModelsRaw, &allowedGroupsRaw)

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

	// Deserialize both grant axes into their tri-state (plan §2.2).
	scanGrantAxes(token, allowedModelsRaw, allowedGroupsRaw)

	if token.IsExpired() {
		return nil, ErrTokenExpired
	}

	return token, nil
}

// scanGrantAxes fills the two grant axes + their INVALID flags from the raw
// column values. A single place so the three read sites cannot drift.
func scanGrantAxes(token *AuthToken, allowedModelsRaw, allowedGroupsRaw interface{}) {
	models, modelsPresent, modelsInvalid := scanGrant(allowedModelsRaw, "allowed_models")
	if modelsPresent {
		token.AllowedModels = models
	}
	token.ModelsInvalid = modelsInvalid

	groups, groupsPresent, groupsInvalid := scanGrant(allowedGroupsRaw, "allowed_groups")
	if groupsPresent {
		token.AllowedGroups = groups
	}
	token.GroupsInvalid = groupsInvalid
}

// ListTokens returns all tokens (without hashes)
func (s *TokenStore) ListTokens(ctx context.Context) ([]AuthToken, error) {
	query := `SELECT id, name, expires_at, created_at, created_by, allowed_models, allowed_groups FROM auth_tokens ORDER BY created_at DESC`

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
		var allowedModelsRaw, allowedGroupsRaw interface{}

		err := rows.Scan(&token.ID, &token.Name, &expiresAtStr, &createdAtStr, &token.CreatedBy, &allowedModelsRaw, &allowedGroupsRaw)
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

		scanGrantAxes(&token, allowedModelsRaw, allowedGroupsRaw)

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
	var allowedModelsRaw, allowedGroupsRaw interface{}

	var query string
	if s.dialect == database.PostgreSQL {
		query = `SELECT id, name, expires_at, created_at, created_by, allowed_models, allowed_groups FROM auth_tokens WHERE id = $1`
	} else {
		query = `SELECT id, name, expires_at, created_at, created_by, allowed_models, allowed_groups FROM auth_tokens WHERE id = ?`
	}

	err := s.db.QueryRowContext(ctx, query, id).Scan(&token.ID, &token.Name, &expiresAtStr, &createdAtStr, &token.CreatedBy, &allowedModelsRaw, &allowedGroupsRaw)

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

	scanGrantAxes(token, allowedModelsRaw, allowedGroupsRaw)

	return token, nil
}

// UpdateTokenPermission updates the grant axes for a token.
//
// COLUMN-SKIP MERGE (plan §6.1 PD-3, ADR §4.2 + §7, D10 — the fail-closed
// preservation rule): a Set:false axis is NOT written at all. It skips that
// SQL column, so the stored bytes are untouched.
//
// That is what keeps DENY-ALL sticky across an omitting merge: a token whose
// allowed_models column holds unreadable bytes is DENY-ALL; a PATCH that
// omits that axis must not "repair" it to NULL (which the conjunctive
// wildcard rule would read as ABSENT, i.e. full access). The operator's
// repair path is to submit a valid array for that axis explicitly.
//
// A Set:true axis IS written: [] -> '[]' (PRESENT-empty, grants nothing),
// ids -> JSON array, nil -> NULL (store-level reset; the PATCH wire cannot
// express it). Never `len(x) > 0` (decisions.md R-6: that would turn "clear
// the groups" into NULL-on-both-axes ⇒ wildcard escalation).
//
// NOTE: this means an already-PRESENT axis can never be returned to ABSENT
// through the PATCH wire — the frozen contract (plan §2.8, Ruling 2) defines
// omitted/explicit-null = KEEP, [] = REPLACE WITH EMPTY. Only creation can
// leave an axis ABSENT (and only a store-level caller can reset one).
func (s *TokenStore) UpdateTokenPermission(ctx context.Context, id string, perm OptionalTokenPermission) error {
	var sets []string
	var args []interface{}

	addSet := func(column string, axis []string) error {
		value, err := marshalGrant(axis)
		if err != nil {
			return err
		}
		if s.dialect == database.PostgreSQL {
			sets = append(sets, fmt.Sprintf("%s = $%d", column, len(args)+1))
		} else {
			sets = append(sets, fmt.Sprintf("%s = ?", column))
		}
		args = append(args, value)
		return nil
	}

	// PD-3: assemble the statement from the SET axes ONLY. A Set:false axis is
	// omitted from the SQL entirely — value and column alike — so untouched
	// raw bytes stay untouched. That is what keeps a DENY-ALL (unreadable) row
	// un-healable by an unrelated edit.
	if perm.AllowedModels.Set {
		if err := addSet("allowed_models", perm.AllowedModels.Value()); err != nil {
			return err
		}
	}
	if perm.AllowedGroups.Set {
		if err := addSet("allowed_groups", perm.AllowedGroups.Value()); err != nil {
			return err
		}
	}

	// Both axes Set:false: NO column is written, so no UPDATE is executed at all.
	// This is a SUCCESS (a no-op PATCH), never ErrTokenNotFound — the 404
	// contract belongs to the API layer, which keeps its GetTokenByID
	// pre-check (pkg/ui/server.go updateTokenPermission). Pinned by
	// TestUpdateTokenPermission_OmitsNilColumn + TestTokenAPI_PatchNonexistent_404
	// so a future refactor cannot delete the pre-check believing the store
	// covers it.
	if len(sets) == 0 {
		return nil
	}

	var query string
	if s.dialect == database.PostgreSQL {
		query = fmt.Sprintf(`UPDATE auth_tokens SET %s WHERE id = $%d`, strings.Join(sets, ", "), len(args)+1)
	} else {
		query = fmt.Sprintf(`UPDATE auth_tokens SET %s WHERE id = ?`, strings.Join(sets, ", "))
	}
	args = append(args, id)

	result, err := s.db.ExecContext(ctx, query, args...)
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
