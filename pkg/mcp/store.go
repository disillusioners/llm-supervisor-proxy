package mcp

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/crypto"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
	"github.com/google/uuid"
)

// MCPStore handles CRUD operations for MCP servers
type MCPStore struct {
	db      *sql.DB
	dialect database.Dialect
	qb      *database.QueryBuilder
}

// NewMCPStore creates a new MCP store
func NewMCPStore(db *sql.DB, dialect database.Dialect) *MCPStore {
	return &MCPStore{
		db:      db,
		dialect: dialect,
		qb:      database.NewQueryBuilder(dialect),
	}
}

// isEnabled converts the enabled field to bool (handles both SQLite int64 and PostgreSQL bool)
func isEnabled(val interface{}) bool {
	switch v := val.(type) {
	case int64:
		return v == 1
	case bool:
		return v
	default:
		return false
	}
}

// ListServers returns all MCP servers
func (s *MCPStore) ListServers(ctx context.Context) ([]*MCPServer, error) {
	query := `SELECT id, name, description, upstream_url, transport_type, auth_type, auth_token, headers, enabled, created_at, updated_at FROM mcp_servers ORDER BY name`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var servers []*MCPServer
	for rows.Next() {
		server := &MCPServer{}
		var authToken string
		var enabled interface{}

		err := rows.Scan(
			&server.ID,
			&server.Name,
			&server.Description,
			&server.UpstreamURL,
			&server.TransportType,
			&server.AuthType,
			&authToken,
			&server.Headers,
			&enabled,
			&server.CreatedAt,
			&server.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}

		// Decrypt auth_token if present
		if authToken != "" {
			decrypted, err := crypto.Decrypt(authToken)
			if err != nil {
				return nil, fmt.Errorf("failed to decrypt auth_token: %w", err)
			}
			server.AuthToken = decrypted
		}

		server.Enabled = isEnabled(enabled)
		servers = append(servers, server)
	}

	return servers, rows.Err()
}

// GetServer returns a single MCP server by ID
func (s *MCPStore) GetServer(ctx context.Context, id string) (*MCPServer, error) {
	query := `SELECT id, name, description, upstream_url, transport_type, auth_type, auth_token, headers, enabled, created_at, updated_at FROM mcp_servers WHERE id = ` + s.qb.Placeholder(1)

	server := &MCPServer{}
	var authToken string
	var enabled interface{}

	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&server.ID,
		&server.Name,
		&server.Description,
		&server.UpstreamURL,
		&server.TransportType,
		&server.AuthType,
		&authToken,
		&server.Headers,
		&enabled,
		&server.CreatedAt,
		&server.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// Decrypt auth_token if present
	if authToken != "" {
		decrypted, err := crypto.Decrypt(authToken)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt auth_token: %w", err)
		}
		server.AuthToken = decrypted
	}

	server.Enabled = isEnabled(enabled)
	return server, nil
}

// CreateServer creates a new MCP server
func (s *MCPStore) CreateServer(ctx context.Context, req CreateMCPServerRequest) (*MCPServer, error) {
	id := uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339)

	// Encrypt auth_token if provided
	encryptedToken := ""
	if req.AuthToken != "" {
		encrypted, err := crypto.Encrypt(req.AuthToken)
		if err != nil {
			return nil, fmt.Errorf("failed to encrypt auth_token: %w", err)
		}
		encryptedToken = encrypted
	}

	// Set defaults
	if req.TransportType == "" {
		req.TransportType = "streamable_http"
	}
	if req.AuthType == "" {
		req.AuthType = "none"
	}
	if req.Headers == "" {
		req.Headers = "{}"
	}

	// Handle enabled - default to true if not provided
	enabledVal := true
	if req.Enabled != nil {
		enabledVal = *req.Enabled
	}

	query := `INSERT INTO mcp_servers (id, name, description, upstream_url, transport_type, auth_type, auth_token, headers, enabled, created_at, updated_at) VALUES (` +
		s.qb.Placeholder(1) + `, ` +
		s.qb.Placeholder(2) + `, ` +
		s.qb.Placeholder(3) + `, ` +
		s.qb.Placeholder(4) + `, ` +
		s.qb.Placeholder(5) + `, ` +
		s.qb.Placeholder(6) + `, ` +
		s.qb.Placeholder(7) + `, ` +
		s.qb.Placeholder(8) + `, ` +
		s.qb.Placeholder(9) + `, ` +
		s.qb.Placeholder(10) + `, ` +
		s.qb.Placeholder(11) + `)`

	_, err := s.db.ExecContext(ctx, query,
		id,
		req.Name,
		req.Description,
		req.UpstreamURL,
		req.TransportType,
		req.AuthType,
		encryptedToken,
		req.Headers,
		s.qb.BooleanLiteral(enabledVal),
		now,
		now,
	)
	if err != nil {
		return nil, err
	}

	// Return the created server with decrypted token
	return &MCPServer{
		ID:            id,
		Name:          req.Name,
		Description:   req.Description,
		UpstreamURL:    req.UpstreamURL,
		TransportType:  req.TransportType,
		AuthType:      req.AuthType,
		AuthToken:     req.AuthToken,
		Headers:       req.Headers,
		Enabled:       enabledVal,
		CreatedAt:     now,
		UpdatedAt:     now,
	}, nil
}

// UpdateServer updates an existing MCP server
func (s *MCPStore) UpdateServer(ctx context.Context, id string, req UpdateMCPServerRequest) (*MCPServer, error) {
	// First, get the existing server to verify it exists and get current values
	existing, err := s.GetServer(ctx, id)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, nil
	}

	// Build dynamic UPDATE query with only provided fields
	var setClauses []string
	var args []interface{}
	argIndex := 1

	if req.Name != nil {
		setClauses = append(setClauses, "name = "+s.qb.Placeholder(argIndex))
		args = append(args, *req.Name)
		argIndex++
	}
	if req.Description != nil {
		setClauses = append(setClauses, "description = "+s.qb.Placeholder(argIndex))
		args = append(args, *req.Description)
		argIndex++
	}
	if req.UpstreamURL != nil {
		setClauses = append(setClauses, "upstream_url = "+s.qb.Placeholder(argIndex))
		args = append(args, *req.UpstreamURL)
		argIndex++
	}
	if req.TransportType != nil {
		setClauses = append(setClauses, "transport_type = "+s.qb.Placeholder(argIndex))
		args = append(args, *req.TransportType)
		argIndex++
	}
	if req.AuthType != nil {
		setClauses = append(setClauses, "auth_type = "+s.qb.Placeholder(argIndex))
		args = append(args, *req.AuthType)
		argIndex++
	}
	if req.AuthToken != nil && *req.AuthToken != "" {
		encrypted, err := crypto.Encrypt(*req.AuthToken)
		if err != nil {
			return nil, fmt.Errorf("failed to encrypt auth_token: %w", err)
		}
		setClauses = append(setClauses, "auth_token = "+s.qb.Placeholder(argIndex))
		args = append(args, encrypted)
		argIndex++
	}
	if req.Headers != nil {
		setClauses = append(setClauses, "headers = "+s.qb.Placeholder(argIndex))
		args = append(args, *req.Headers)
		argIndex++
	}
	if req.Enabled != nil {
		setClauses = append(setClauses, "enabled = "+s.qb.Placeholder(argIndex))
		args = append(args, s.qb.BooleanLiteral(*req.Enabled))
		argIndex++
	}

	// Always update updated_at
	now := time.Now().UTC().Format(time.RFC3339)
	setClauses = append(setClauses, "updated_at = "+s.qb.Placeholder(argIndex))
	args = append(args, now)
	argIndex++

	// Add WHERE clause argument
	args = append(args, id)

	query := "UPDATE mcp_servers SET " + strings.Join(setClauses, ", ") + " WHERE id = " + s.qb.Placeholder(argIndex)

	_, err = s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}

	// Build updated server response
	updated := &MCPServer{
		ID:         existing.ID,
		CreatedAt:  existing.CreatedAt,
		UpdatedAt:  now,
	}

	// Apply updates
	if req.Name != nil {
		updated.Name = *req.Name
	} else {
		updated.Name = existing.Name
	}
	if req.Description != nil {
		updated.Description = *req.Description
	} else {
		updated.Description = existing.Description
	}
	if req.UpstreamURL != nil {
		updated.UpstreamURL = *req.UpstreamURL
	} else {
		updated.UpstreamURL = existing.UpstreamURL
	}
	if req.TransportType != nil {
		updated.TransportType = *req.TransportType
	} else {
		updated.TransportType = existing.TransportType
	}
	if req.AuthType != nil {
		updated.AuthType = *req.AuthType
	} else {
		updated.AuthType = existing.AuthType
	}
	if req.AuthToken != nil && *req.AuthToken != "" {
		updated.AuthToken = *req.AuthToken
	} else {
		updated.AuthToken = existing.AuthToken
	}
	if req.Headers != nil {
		updated.Headers = *req.Headers
	} else {
		updated.Headers = existing.Headers
	}
	if req.Enabled != nil {
		updated.Enabled = *req.Enabled
	} else {
		updated.Enabled = existing.Enabled
	}

	return updated, nil
}

// DeleteServer removes an MCP server by ID
func (s *MCPStore) DeleteServer(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM mcp_servers WHERE id = "+s.qb.Placeholder(1),
		id,
	)
	if err != nil {
		return fmt.Errorf("failed to delete server: %w", err)
	}
	// Return nil even if no rows affected (consistent with GetServer returning nil, nil)
	return nil
}
