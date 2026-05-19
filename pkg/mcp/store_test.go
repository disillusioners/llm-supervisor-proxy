package mcp

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/crypto"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
	_ "modernc.org/sqlite"
)

// testDB wraps a SQLite connection for testing MCPStore
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

	// Run migrations to create mcp_servers table
	if err := runMCPMigrations(db); err != nil {
		db.Close()
		t.Fatalf("Failed to run migrations: %v", err)
	}

	cleanup := func() {
		db.Close()
	}

	return &testDB{DB: db}, cleanup
}

// runMCPMigrations creates the mcp_servers table for testing
func runMCPMigrations(db *sql.DB) error {
	// Create mcp_servers table (matching migration 026_add_mcp_servers)
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS mcp_servers (
			id              TEXT PRIMARY KEY,
			name            TEXT    NOT NULL UNIQUE,
			description     TEXT    DEFAULT '',
			upstream_url    TEXT    NOT NULL,
			transport_type  TEXT    NOT NULL DEFAULT 'streamable_http',
			auth_type       TEXT    DEFAULT 'none',
			auth_token      TEXT    DEFAULT '',
			headers         TEXT    DEFAULT '{}',
			enabled         INTEGER NOT NULL DEFAULT 1,
			created_at      TEXT    NOT NULL,
			updated_at      TEXT    NOT NULL
		)
	`)
	if err != nil {
		return err
	}

	// Create index on enabled
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_mcp_servers_enabled ON mcp_servers(enabled)`)
	return err
}

func TestNewMCPStore(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewMCPStore(db.DB, database.SQLite)

	if store == nil {
		t.Fatal("NewMCPStore returned nil")
	}

	if store.db == nil {
		t.Error("store.db is nil")
	}

	if store.dialect != database.SQLite {
		t.Errorf("dialect = %v, want SQLite", store.dialect)
	}
}

// =============================================================================
// ListServers Tests
// =============================================================================

func TestListServersEmpty(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewMCPStore(db.DB, database.SQLite)
	ctx := context.Background()

	servers, err := store.ListServers(ctx)
	if err != nil {
		t.Fatalf("ListServers() error = %v, want nil", err)
	}

	// Note: Store returns nil when empty (Go nil slice behavior)
	// Both nil and empty slice have len() == 0
	if len(servers) != 0 {
		t.Errorf("len(servers) = %d, want 0", len(servers))
	}
}

func TestListServersReturnsAll(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewMCPStore(db.DB, database.SQLite)
	ctx := context.Background()

	// Create multiple servers
	for i := 0; i < 3; i++ {
		req := CreateMCPServerRequest{
			Name:        "server-" + string(rune('a'+i)),
			UpstreamURL: "https://api.example.com/mcp",
			AuthType:    AuthNone,
		}
		_, err := store.CreateServer(ctx, req)
		if err != nil {
			t.Fatalf("CreateServer() error = %v", err)
		}
	}

	servers, err := store.ListServers(ctx)
	if err != nil {
		t.Fatalf("ListServers() error = %v, want nil", err)
	}

	if len(servers) != 3 {
		t.Errorf("len(servers) = %d, want 3", len(servers))
	}
}

func TestListServersOrderedByName(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewMCPStore(db.DB, database.SQLite)
	ctx := context.Background()

	// Create servers with names that would not be in order if sorted by ID
	names := []string{"zebra", "alpha", "middle"}
	for _, name := range names {
		req := CreateMCPServerRequest{
			Name:        name,
			UpstreamURL: "https://api.example.com/mcp",
			AuthType:    AuthNone,
		}
		_, err := store.CreateServer(ctx, req)
		if err != nil {
			t.Fatalf("CreateServer(%s) error = %v", name, err)
		}
	}

	servers, err := store.ListServers(ctx)
	if err != nil {
		t.Fatalf("ListServers() error = %v, want nil", err)
	}

	// Verify ordered by name
	for i := 0; i < len(servers)-1; i++ {
		if servers[i].Name > servers[i+1].Name {
			t.Errorf("ListServers not ordered by name: %s > %s", servers[i].Name, servers[i+1].Name)
		}
	}
}

// =============================================================================
// GetServer Tests
// =============================================================================

func TestGetServerExisting(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewMCPStore(db.DB, database.SQLite)
	ctx := context.Background()

	// Create a server
	created, err := store.CreateServer(ctx, CreateMCPServerRequest{
		Name:        "get-test-server",
		Description: "A test server",
		UpstreamURL: "https://api.example.com/mcp",
		AuthType:    AuthBearer,
		AuthToken:   "secret-token",
		Headers:     `{"X-Custom": "value"}`,
	})
	if err != nil {
		t.Fatalf("CreateServer() error = %v", err)
	}

	// Get the server
	retrieved, err := store.GetServer(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetServer() error = %v, want nil", err)
	}

	if retrieved == nil {
		t.Fatal("GetServer() returned nil")
	}

	if retrieved.ID != created.ID {
		t.Errorf("retrieved.ID = %q, want %q", retrieved.ID, created.ID)
	}

	if retrieved.Name != "get-test-server" {
		t.Errorf("retrieved.Name = %q, want %q", retrieved.Name, "get-test-server")
	}

	if retrieved.Description != "A test server" {
		t.Errorf("retrieved.Description = %q, want %q", retrieved.Description, "A test server")
	}

	if retrieved.UpstreamURL != "https://api.example.com/mcp" {
		t.Errorf("retrieved.UpstreamURL = %q, want %q", retrieved.UpstreamURL, "https://api.example.com/mcp")
	}

	if retrieved.AuthType != AuthBearer {
		t.Errorf("retrieved.AuthType = %v, want %v", retrieved.AuthType, AuthBearer)
	}

	// Auth token should be decrypted
	if retrieved.AuthToken != "secret-token" {
		t.Errorf("retrieved.AuthToken = %q, want %q", retrieved.AuthToken, "secret-token")
	}

	if retrieved.Headers != `{"X-Custom": "value"}` {
		t.Errorf("retrieved.Headers = %q, want %q", retrieved.Headers, `{"X-Custom": "value"}`)
	}
}

func TestGetServerNonExistent(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewMCPStore(db.DB, database.SQLite)
	ctx := context.Background()

	server, err := store.GetServer(ctx, "nonexistent-id")
	if err != nil {
		t.Fatalf("GetServer() error = %v, want nil", err)
	}

	if server != nil {
		t.Errorf("GetServer() returned server for non-existent ID, want nil")
	}
}

// =============================================================================
// CreateServer Tests
// =============================================================================

func TestCreateServer(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewMCPStore(db.DB, database.SQLite)
	ctx := context.Background()

	created, err := store.CreateServer(ctx, CreateMCPServerRequest{
		Name:        "create-test-server",
		Description: "Test description",
		UpstreamURL: "https://api.example.com/mcp",
		TransportType: TransportStreamableHTTP,
		AuthType:    AuthBearer,
		AuthToken:   "my-secret-token",
		Headers:     `{"X-Header": "value"}`,
	})
	if err != nil {
		t.Fatalf("CreateServer() error = %v, want nil", err)
	}

	// UUID is generated
	if created.ID == "" {
		t.Error("created.ID is empty")
	}

	// Timestamps are set
	if created.CreatedAt == "" {
		t.Error("created.CreatedAt is empty")
	}

	if created.UpdatedAt == "" {
		t.Error("created.UpdatedAt is empty")
	}

	// Verify timestamp format is RFC3339
	_, err = time.Parse(time.RFC3339, created.CreatedAt)
	if err != nil {
		t.Errorf("created.CreatedAt is not valid RFC3339: %v", err)
	}

	_, err = time.Parse(time.RFC3339, created.UpdatedAt)
	if err != nil {
		t.Errorf("created.UpdatedAt is not valid RFC3339: %v", err)
	}

	// Verify fields are set correctly
	if created.Name != "create-test-server" {
		t.Errorf("created.Name = %q, want %q", created.Name, "create-test-server")
	}

	if created.Description != "Test description" {
		t.Errorf("created.Description = %q, want %q", created.Description, "Test description")
	}

	if created.UpstreamURL != "https://api.example.com/mcp" {
		t.Errorf("created.UpstreamURL = %q, want %q", created.UpstreamURL, "https://api.example.com/mcp")
	}

	if created.TransportType != TransportStreamableHTTP {
		t.Errorf("created.TransportType = %v, want %v", created.TransportType, TransportStreamableHTTP)
	}

	if created.AuthType != AuthBearer {
		t.Errorf("created.AuthType = %v, want %v", created.AuthType, AuthBearer)
	}

	if created.AuthToken != "my-secret-token" {
		t.Errorf("created.AuthToken = %q, want %q", created.AuthToken, "my-secret-token")
	}

	if created.Headers != `{"X-Header": "value"}` {
		t.Errorf("created.Headers = %q, want %q", created.Headers, `{"X-Header": "value"}`)
	}
}

func TestCreateServerDefaultsEnabled(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewMCPStore(db.DB, database.SQLite)
	ctx := context.Background()

	// Create server without specifying enabled
	created, err := store.CreateServer(ctx, CreateMCPServerRequest{
		Name:        "default-enabled-test",
		UpstreamURL: "https://api.example.com/mcp",
		AuthType:    AuthNone,
	})
	if err != nil {
		t.Fatalf("CreateServer() error = %v", err)
	}

	if !created.Enabled {
		t.Error("created.Enabled should default to true")
	}
}

func TestCreateServerWithEnabledFalse(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewMCPStore(db.DB, database.SQLite)
	ctx := context.Background()

	enabled := false
	created, err := store.CreateServer(ctx, CreateMCPServerRequest{
		Name:        "disabled-server-test",
		UpstreamURL: "https://api.example.com/mcp",
		AuthType:    AuthNone,
		Enabled:     &enabled,
	})
	if err != nil {
		t.Fatalf("CreateServer() error = %v", err)
	}

	if created.Enabled {
		t.Error("created.Enabled should be false when explicitly set")
	}
}

func TestCreateServerAuthTokenEncrypted(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewMCPStore(db.DB, database.SQLite)
	ctx := context.Background()

	// Create server with auth token
	created, err := store.CreateServer(ctx, CreateMCPServerRequest{
		Name:       "token-encryption-test",
		UpstreamURL: "https://api.example.com/mcp",
		AuthType:   AuthBearer,
		AuthToken:  "super-secret-token",
	})
	if err != nil {
		t.Fatalf("CreateServer() error = %v", err)
	}

	// Verify the token is returned decrypted
	if created.AuthToken != "super-secret-token" {
		t.Errorf("created.AuthToken = %q, want %q (decrypted)", created.AuthToken, "super-secret-token")
	}

	// Also verify by getting from DB
	retrieved, err := store.GetServer(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetServer() error = %v", err)
	}

	if retrieved.AuthToken != "super-secret-token" {
		t.Errorf("retrieved.AuthToken = %q, want %q (decrypted on read)", retrieved.AuthToken, "super-secret-token")
	}

	// Verify that encryption pass-through works (no key configured = plaintext)
	// If a key were configured, the stored value would be different from plaintext
	// This test confirms the token round-trips correctly
	_ = crypto.ResetEncryptionState // For documentation purposes - tests run without encryption key
}

func TestCreateServerNameUniqueness(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewMCPStore(db.DB, database.SQLite)
	ctx := context.Background()

	// Create first server
	_, err := store.CreateServer(ctx, CreateMCPServerRequest{
		Name:        "unique-name-test",
		UpstreamURL: "https://api.example.com/mcp",
		AuthType:    AuthNone,
	})
	if err != nil {
		t.Fatalf("First CreateServer() error = %v", err)
	}

	// Try to create second server with same name
	_, err = store.CreateServer(ctx, CreateMCPServerRequest{
		Name:        "unique-name-test",
		UpstreamURL: "https://other.example.com/mcp",
		AuthType:    AuthNone,
	})

	// Should fail due to UNIQUE constraint
	if err == nil {
		t.Error("Second CreateServer() with duplicate name should fail, got nil error")
	}
}

func TestCreateServerDefaultsTransportType(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewMCPStore(db.DB, database.SQLite)
	ctx := context.Background()

	// Create server without specifying transport type
	created, err := store.CreateServer(ctx, CreateMCPServerRequest{
		Name:        "default-transport-test",
		UpstreamURL: "https://api.example.com/mcp",
		AuthType:    AuthNone,
	})
	if err != nil {
		t.Fatalf("CreateServer() error = %v", err)
	}

	if created.TransportType != TransportStreamableHTTP {
		t.Errorf("created.TransportType = %v, want %v (default)", created.TransportType, TransportStreamableHTTP)
	}
}

func TestCreateServerDefaultsAuthType(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewMCPStore(db.DB, database.SQLite)
	ctx := context.Background()

	// Create server without specifying auth type
	created, err := store.CreateServer(ctx, CreateMCPServerRequest{
		Name:        "default-auth-test",
		UpstreamURL: "https://api.example.com/mcp",
	})
	if err != nil {
		t.Fatalf("CreateServer() error = %v", err)
	}

	if created.AuthType != AuthNone {
		t.Errorf("created.AuthType = %v, want %v (default)", created.AuthType, AuthNone)
	}
}

func TestCreateServerDefaultsHeaders(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewMCPStore(db.DB, database.SQLite)
	ctx := context.Background()

	// Create server without specifying headers
	created, err := store.CreateServer(ctx, CreateMCPServerRequest{
		Name:        "default-headers-test",
		UpstreamURL: "https://api.example.com/mcp",
		AuthType:    AuthNone,
	})
	if err != nil {
		t.Fatalf("CreateServer() error = %v", err)
	}

	if created.Headers != "{}" {
		t.Errorf("created.Headers = %q, want %q (default)", created.Headers, "{}")
	}
}

// =============================================================================
// UpdateServer Tests
// =============================================================================

func TestUpdateServer(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewMCPStore(db.DB, database.SQLite)
	ctx := context.Background()

	// Create a server
	created, err := store.CreateServer(ctx, CreateMCPServerRequest{
		Name:        "update-test-server",
		Description: "Original description",
		UpstreamURL: "https://original.example.com/mcp",
		AuthType:    AuthBearer,
		AuthToken:   "original-token",
	})
	if err != nil {
		t.Fatalf("CreateServer() error = %v", err)
	}

	// Update individual fields
	newName := "updated-name"
	newDescription := "Updated description"
	newURL := "https://updated.example.com/mcp"

	updated, err := store.UpdateServer(ctx, created.ID, UpdateMCPServerRequest{
		Name:        &newName,
		Description: &newDescription,
		UpstreamURL: &newURL,
	})
	if err != nil {
		t.Fatalf("UpdateServer() error = %v", err)
	}

	if updated.Name != "updated-name" {
		t.Errorf("updated.Name = %q, want %q", updated.Name, "updated-name")
	}

	if updated.Description != "Updated description" {
		t.Errorf("updated.Description = %q, want %q", updated.Description, "Updated description")
	}

	if updated.UpstreamURL != "https://updated.example.com/mcp" {
		t.Errorf("updated.UpstreamURL = %q, want %q", updated.UpstreamURL, "https://updated.example.com/mcp")
	}
}

func TestUpdateServerAuthToken(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewMCPStore(db.DB, database.SQLite)
	ctx := context.Background()

	// Create a server without auth token
	created, err := store.CreateServer(ctx, CreateMCPServerRequest{
		Name:       "update-token-test",
		UpstreamURL: "https://api.example.com/mcp",
		AuthType:   AuthBearer,
		AuthToken:  "original-token",
	})
	if err != nil {
		t.Fatalf("CreateServer() error = %v", err)
	}

	// Update auth token
	newToken := "new-secret-token"
	updated, err := store.UpdateServer(ctx, created.ID, UpdateMCPServerRequest{
		AuthToken: &newToken,
	})
	if err != nil {
		t.Fatalf("UpdateServer() error = %v", err)
	}

	// Verify new token is returned
	if updated.AuthToken != "new-secret-token" {
		t.Errorf("updated.AuthToken = %q, want %q", updated.AuthToken, "new-secret-token")
	}

	// Verify by retrieving from DB
	retrieved, err := store.GetServer(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetServer() error = %v", err)
	}

	if retrieved.AuthToken != "new-secret-token" {
		t.Errorf("retrieved.AuthToken = %q, want %q (decrypted on read)", retrieved.AuthToken, "new-secret-token")
	}
}

func TestUpdateServerEnabled(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewMCPStore(db.DB, database.SQLite)
	ctx := context.Background()

	// Create a server (enabled = true by default)
	created, err := store.CreateServer(ctx, CreateMCPServerRequest{
		Name:        "update-enabled-test",
		UpstreamURL: "https://api.example.com/mcp",
		AuthType:    AuthNone,
	})
	if err != nil {
		t.Fatalf("CreateServer() error = %v", err)
	}

	if !created.Enabled {
		t.Error("Initial server should be enabled")
	}

	// Update enabled to false
	disabled := false
	updated, err := store.UpdateServer(ctx, created.ID, UpdateMCPServerRequest{
		Enabled: &disabled,
	})
	if err != nil {
		t.Fatalf("UpdateServer() error = %v", err)
	}

	if updated.Enabled {
		t.Error("updated.Enabled should be false")
	}

	// Verify by retrieving from DB
	retrieved, err := store.GetServer(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetServer() error = %v", err)
	}

	if retrieved.Enabled {
		t.Error("retrieved.Enabled should be false after update")
	}
}

func TestUpdateServerNonExistent(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewMCPStore(db.DB, database.SQLite)
	ctx := context.Background()

	newName := "some-name"
	updated, err := store.UpdateServer(ctx, "nonexistent-id", UpdateMCPServerRequest{
		Name: &newName,
	})

	if err != nil {
		t.Fatalf("UpdateServer() error = %v, want nil", err)
	}

	if updated != nil {
		t.Errorf("UpdateServer() returned server for non-existent ID, want nil")
	}
}

func TestUpdateServerPartialUpdate(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewMCPStore(db.DB, database.SQLite)
	ctx := context.Background()

	// Create a server with all fields set
	created, err := store.CreateServer(ctx, CreateMCPServerRequest{
		Name:          "partial-update-test",
		Description:   "Original description",
		UpstreamURL:   "https://original.example.com/mcp",
		TransportType: TransportSSE,
		AuthType:      AuthBasic,
		AuthToken:     "original-token",
		Headers:       `{"X-Original": "value"}`,
	})
	if err != nil {
		t.Fatalf("CreateServer() error = %v", err)
	}

	// Only update the name
	newName := "only-name-updated"
	updated, err := store.UpdateServer(ctx, created.ID, UpdateMCPServerRequest{
		Name: &newName,
	})
	if err != nil {
		t.Fatalf("UpdateServer() error = %v", err)
	}

	// Name should be updated
	if updated.Name != "only-name-updated" {
		t.Errorf("updated.Name = %q, want %q", updated.Name, "only-name-updated")
	}

	// Other fields should be preserved
	if updated.Description != "Original description" {
		t.Errorf("updated.Description = %q, want %q (preserved)", updated.Description, "Original description")
	}

	if updated.UpstreamURL != "https://original.example.com/mcp" {
		t.Errorf("updated.UpstreamURL = %q, want %q (preserved)", updated.UpstreamURL, "https://original.example.com/mcp")
	}

	if updated.TransportType != TransportSSE {
		t.Errorf("updated.TransportType = %v, want %v (preserved)", updated.TransportType, TransportSSE)
	}

	if updated.AuthType != AuthBasic {
		t.Errorf("updated.AuthType = %v, want %v (preserved)", updated.AuthType, AuthBasic)
	}

	if updated.AuthToken != "original-token" {
		t.Errorf("updated.AuthToken = %q, want %q (preserved)", updated.AuthToken, "original-token")
	}

	if updated.Headers != `{"X-Original": "value"}` {
		t.Errorf("updated.Headers = %q, want %q (preserved)", updated.Headers, `{"X-Original": "value"}`)
	}
}

// =============================================================================
// DeleteServer Tests
// =============================================================================

func TestDeleteServer(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewMCPStore(db.DB, database.SQLite)
	ctx := context.Background()

	// Create a server
	created, err := store.CreateServer(ctx, CreateMCPServerRequest{
		Name:        "delete-test-server",
		UpstreamURL: "https://api.example.com/mcp",
		AuthType:    AuthNone,
	})
	if err != nil {
		t.Fatalf("CreateServer() error = %v", err)
	}

	// Delete the server
	err = store.DeleteServer(ctx, created.ID)
	if err != nil {
		t.Fatalf("DeleteServer() error = %v, want nil", err)
	}

	// Verify it's deleted
	retrieved, err := store.GetServer(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetServer() after delete error = %v, want nil", err)
	}

	if retrieved != nil {
		t.Error("GetServer() should return nil after delete")
	}
}

func TestDeleteServerNotFound(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewMCPStore(db.DB, database.SQLite)
	ctx := context.Background()

	err := store.DeleteServer(ctx, "nonexistent-id")
	if err != sql.ErrNoRows {
		t.Errorf("DeleteServer() error = %v, want sql.ErrNoRows", err)
	}
}

func TestDeleteServerAfterDeleteGetServerReturnsNil(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewMCPStore(db.DB, database.SQLite)
	ctx := context.Background()

	// Create a server
	created, err := store.CreateServer(ctx, CreateMCPServerRequest{
		Name:        "delete-verify-test",
		UpstreamURL: "https://api.example.com/mcp",
		AuthType:    AuthNone,
	})
	if err != nil {
		t.Fatalf("CreateServer() error = %v", err)
	}

	// Delete it
	err = store.DeleteServer(ctx, created.ID)
	if err != nil {
		t.Fatalf("DeleteServer() error = %v", err)
	}

	// After delete, GetServer returns nil
	retrieved, err := store.GetServer(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetServer() error after delete = %v, want nil", err)
	}

	if retrieved != nil {
		t.Error("After delete, GetServer should return nil, nil")
	}
}

// =============================================================================
// isEnabled Helper Tests
// =============================================================================

func TestIsEnabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		val  interface{}
		want bool
	}{
		{"int64(1) returns true", int64(1), true},
		{"int64(0) returns false", int64(0), false},
		{"int64(2) returns false", int64(2), false},
		{"bool true returns true", true, true},
		{"bool false returns false", false, false},
		{"nil returns false", nil, false},
		{"string returns false", "not a bool", false},
		{"float64 returns false", 1.0, false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isEnabled(tt.val); got != tt.want {
				t.Errorf("isEnabled(%v) = %v, want %v", tt.val, got, tt.want)
			}
		})
	}
}

// =============================================================================
// Full CRUD Integration Test
// =============================================================================

func TestMCPStoreFullCRUD(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	store := NewMCPStore(db.DB, database.SQLite)
	ctx := context.Background()

	// CREATE
	server1, err := store.CreateServer(ctx, CreateMCPServerRequest{
		Name:        "crud-server-1",
		Description: "First server",
		UpstreamURL: "https://server1.example.com/mcp",
		AuthType:    AuthNone,
	})
	if err != nil {
		t.Fatalf("CreateServer(1) error = %v", err)
	}

	_, err = store.CreateServer(ctx, CreateMCPServerRequest{
		Name:        "crud-server-2",
		Description: "Second server",
		UpstreamURL: "https://server2.example.com/mcp",
		AuthType:    AuthBearer,
		AuthToken:   "secret",
	})
	if err != nil {
		t.Fatalf("CreateServer(2) error = %v", err)
	}

	// LIST
	servers, err := store.ListServers(ctx)
	if err != nil {
		t.Fatalf("ListServers() error = %v", err)
	}
	if len(servers) != 2 {
		t.Errorf("len(servers) = %d, want 2", len(servers))
	}

	// GET
	retrieved, err := store.GetServer(ctx, server1.ID)
	if err != nil {
		t.Fatalf("GetServer() error = %v", err)
	}
	if retrieved.Name != "crud-server-1" {
		t.Errorf("retrieved.Name = %q, want %q", retrieved.Name, "crud-server-1")
	}

	// UPDATE
	newDesc := "Updated description"
	updated, err := store.UpdateServer(ctx, server1.ID, UpdateMCPServerRequest{
		Description: &newDesc,
	})
	if err != nil {
		t.Fatalf("UpdateServer() error = %v", err)
	}
	if updated.Description != "Updated description" {
		t.Errorf("updated.Description = %q, want %q", updated.Description, "Updated description")
	}

	// Verify update persisted
	retrieved, err = store.GetServer(ctx, server1.ID)
	if err != nil {
		t.Fatalf("GetServer() after update error = %v", err)
	}
	if retrieved.Description != "Updated description" {
		t.Errorf("retrieved.Description = %q, want %q", retrieved.Description, "Updated description")
	}

	// DELETE
	err = store.DeleteServer(ctx, server1.ID)
	if err != nil {
		t.Fatalf("DeleteServer() error = %v", err)
	}

	// Verify deleted
	retrieved, err = store.GetServer(ctx, server1.ID)
	if err != nil {
		t.Fatalf("GetServer() after delete error = %v", err)
	}
	if retrieved != nil {
		t.Error("After delete, GetServer should return nil")
	}

	// Verify other server still exists
	servers, err = store.ListServers(ctx)
	if err != nil {
		t.Fatalf("ListServers() after delete error = %v", err)
	}
	if len(servers) != 1 {
		t.Errorf("len(servers) after delete = %d, want 1", len(servers))
	}
}
