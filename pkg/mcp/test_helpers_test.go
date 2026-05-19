package mcp

import (
	"context"
	"database/sql"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
	_ "modernc.org/sqlite"
)

// setupTestEnv creates a test Server with real SQLite-backed stores for testing.
// Returns the server, MCPStore, valid auth token, and a cleanup function.
func setupTestEnv(t *testing.T) (*Server, *MCPStore, string, func()) {
	t.Helper()

	// Create in-memory SQLite for mcp_servers
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory database: %v", err)
	}

	// Run migrations
	if err := runMCPMigrations(db); err != nil {
		db.Close()
		t.Fatalf("failed to run migrations: %v", err)
	}

	// Create MCPStore
	mcpStore := NewMCPStore(db, database.SQLite)

	// Create in-memory SQLite for auth_tokens
	authDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		db.Close()
		t.Fatalf("failed to open auth database: %v", err)
	}

	// Create auth_tokens table
	createTable := `
		CREATE TABLE auth_tokens (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			token_hash TEXT NOT NULL UNIQUE,
			expires_at TEXT,
			created_at TEXT NOT NULL,
			created_by TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			ultimate_model_enabled INTEGER NOT NULL DEFAULT 0,
			ultimate_model TEXT,
			allowed_models TEXT
		)
	`
	if _, err := authDB.Exec(createTable); err != nil {
		db.Close()
		authDB.Close()
		t.Fatalf("failed to create auth_tokens table: %v", err)
	}

	tokenStore := auth.NewTokenStore(authDB, database.SQLite)

	// Create a test token
	plaintext, _, err := tokenStore.CreateToken(context.Background(), "test-token", nil, "test", false, "", nil)
	if err != nil {
		db.Close()
		authDB.Close()
		t.Fatalf("failed to create test token: %v", err)
	}

	bus := events.NewBus()

	server := &Server{
		store:      mcpStore,
		bus:        bus,
		tokenStore: tokenStore,
		connMgr:    NewConnectionManager(),
	}

	cleanup := func() {
		db.Close()
		authDB.Close()
	}

	return server, mcpStore, plaintext, cleanup
}
