# Phase 1: Data Model & Server Bootstrap

## Objective
Create the database schema, Go data structures, store CRUD methods (with encryption), validation helpers, and the MCP server bootstrap logic (constructor, Start/Stop with its own `http.Server`, port config). This phase delivers the foundation that Phases 2 and 3 build upon.

## Coupling
- **Depends on**: None (root phase)
- **Coupling type**: —
- **Shared files with other phases**: 
  - `pkg/mcp/types.go` — used by all phases
  - `pkg/mcp/store.go` — used by Phases 2, 3, 4
  - `pkg/mcp/mcp.go` — used by Phases 2, 3, 4 (Phase 2 fills `setupRoutes()`, Phase 3 fills `RegisterAPIHandlers()`)
  - `pkg/mcp/validation.go` — used by Phases 2, 3
- **Shared APIs/interfaces**: `MCPServer` struct, `MCPStore` with CRUD methods, `GetMCPProxyPort()`, `NewServer()` (includes `tokenStore` param from day one), validation functions, `setupRoutes()` and `RegisterAPIHandlers()` empty stubs
- **Why this coupling**: Foundation data types, storage, and server lifecycle must exist before proxy logic, auth, or API handlers can be written

## Context
- Database: `pkg/store/database/` with `Store` struct (`DB *sql.DB`, `Dialect` field)
- Migrations: Embedded SQL files, registered in `migrate.go` migrations list
- Currently at migration 025, new starts at 026
- QueryBuilder pattern for dialect-aware SQL
- Encryption: `pkg/crypto/` with `Encrypt(plaintext) → (ciphertext, error)` and `Decrypt(ciphertext) → (plaintext, error)` using AES-256-GCM. Pass-through if no key configured.

## Data Model

### Database Table: `mcp_servers`

```sql
CREATE TABLE IF NOT EXISTS mcp_servers (
    id              TEXT PRIMARY KEY,           -- UUID
    name            TEXT    NOT NULL UNIQUE,    -- Human-readable name
    description     TEXT    DEFAULT '',         -- Optional description
    upstream_url    TEXT    NOT NULL,           -- Upstream MCP server URL (e.g., "http://remote:3001")
    transport_type  TEXT    NOT NULL DEFAULT 'streamable_http',  -- 'sse' | 'streamable_http'
    auth_type       TEXT    DEFAULT 'none',     -- 'none' | 'bearer' | 'basic' | 'api_key'
    auth_token      TEXT    DEFAULT '',         -- Encrypted auth token/credentials for upstream
    headers         TEXT    DEFAULT '{}',       -- JSON map of custom headers to forward
    enabled         INTEGER NOT NULL DEFAULT 1, -- Whether server is active
    created_at      TEXT    NOT NULL,           -- ISO8601 timestamp (set by Go, not DB)
    updated_at      TEXT    NOT NULL            -- ISO8601 timestamp (set by Go, not DB)
);
CREATE INDEX IF NOT EXISTS idx_mcp_servers_enabled ON mcp_servers(enabled);
```

### Go Struct: `MCPServer`

```go
// pkg/mcp/types.go
package mcp

import (
    "encoding/json"
    "fmt"
    "net/url"
    "strings"
)

type TransportType string

const (
    TransportSSE            TransportType = "sse"
    TransportStreamableHTTP TransportType = "streamable_http"
)

func (t TransportType) Valid() bool {
    return t == TransportSSE || t == TransportStreamableHTTP
}

type AuthType string

const (
    AuthNone   AuthType = "none"
    AuthBearer AuthType = "bearer"
    AuthBasic  AuthType = "basic"
    AuthAPIKey AuthType = "api_key"
)

func (a AuthType) Valid() bool {
    return a == AuthNone || a == AuthBearer || a == AuthBasic || a == AuthAPIKey
}

type MCPServer struct {
    ID            string        `json:"id"`
    Name          string        `json:"name"`
    Description   string        `json:"description"`
    UpstreamURL   string        `json:"upstream_url"`
    TransportType TransportType `json:"transport_type"`
    AuthType      AuthType      `json:"auth_type"`
    AuthToken     string        `json:"auth_token,omitempty"` // masked in API responses, encrypted in DB
    Headers       string        `json:"headers"`              // JSON map
    Enabled       bool          `json:"enabled"`
    CreatedAt     string        `json:"created_at"`
    UpdatedAt     string        `json:"updated_at"`
}

// For create requests
type CreateMCPServerRequest struct {
    Name          string        `json:"name"`
    Description   string        `json:"description"`
    UpstreamURL   string        `json:"upstream_url"`
    TransportType TransportType `json:"transport_type"`
    AuthType      AuthType      `json:"auth_type"`
    AuthToken     string        `json:"auth_token"`
    Headers       string        `json:"headers"`
    Enabled       *bool         `json:"enabled"`
}

// For update requests (all optional)
type UpdateMCPServerRequest struct {
    Name          *string        `json:"name"`
    Description   *string        `json:"description"`
    UpstreamURL   *string        `json:"upstream_url"`
    TransportType *TransportType `json:"transport_type"`
    AuthType      *AuthType      `json:"auth_type"`
    AuthToken     *string        `json:"auth_token"`
    Headers       *string        `json:"headers"`
    Enabled       *bool          `json:"enabled"`
}
```

## Tasks

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 1 | Create migration 026 (SQLite + PostgreSQL) | `mcp_servers` table with schema above | `migrations/sqlite/026_add_mcp_servers.up.sql`, `migrations/postgres/026_add_mcp_servers.up.sql` |
| 2 | Register migration 026 in migrate.go | Add `{"026", "026_add_mcp_servers.up"}` to migrations list | `pkg/store/database/migrate.go` |
| 3 | Create `pkg/mcp/types.go` | `MCPServer`, `TransportType`, `AuthType`, request structs as above. Include `Valid()` methods on enum types. | `pkg/mcp/types.go` |
| 4 | Create `pkg/mcp/validation.go` | URL validation (reject localhost, private networks, link-local, non-http(s) schemes), header blocklist (`Host`, `Content-Length`, `Transfer-Encoding`, `Connection`), JSON headers validation, enum validation, name uniqueness is enforced by DB UNIQUE constraint. | `pkg/mcp/validation.go` |
| 5 | Create `pkg/mcp/store.go` — MCPStore | CRUD methods using `database.Store` pattern: `ListServers`, `GetServer`, `CreateServer`, `UpdateServer`, `DeleteServer`. Use `QueryBuilder` for dialect-aware SQL. **Encryption**: `auth_token` encrypted with `crypto.Encrypt()` on write, decrypted with `crypto.Decrypt()` on read. **Boolean helper**: `isEnabled()` converts `INTEGER` → `bool` for SQLite compatibility. **Timestamps**: use `time.Now().UTC().Format(time.RFC3339)` in Go, not DB-level defaults. | `pkg/mcp/store.go` |
| 6 | Create `pkg/mcp/mcp.go` — Server bootstrap | `Server` struct with `*http.Server`, `MCPStore`, `auth.TokenStoreInterface`, port config. **`NewServer(port, mcpStore, bus, tokenStore)` constructor — `tokenStore` from day one** (NB1). `Start(ctx)` / `Stop(ctx)` methods. `GetMCPProxyPort()` reads `MCP_PROXY_PORT` env var, returns 0 if unset/invalid. **Create two empty route registration stubs** (B1): `setupRoutes()` — for Phase 2 to fill with transport routes (`/mcp/{id}/sse`, `/mcp/{id}/messages`, `/mcp/{id}/http`), and `RegisterAPIHandlers(mux)` — for Phase 3 to fill with management API routes (`/fe/api/mcp-servers`). Health endpoint `/healthz` on MCP port. | `pkg/mcp/mcp.go` |
| 7 | Write store and validation tests | Test CRUD operations (including encrypt/decrypt round-trip), boolean conversion, timestamp handling. Test URL validation (reject localhost, private IPs, valid URLs). Test header blocklist. Test enum validation. | `pkg/mcp/store_test.go`, `pkg/mcp/validation_test.go` |

## Key Files
- `pkg/mcp/types.go` — Data types and enums (NEW)
- `pkg/mcp/validation.go` — URL, header, enum validation (NEW)
- `pkg/mcp/store.go` — Database CRUD with encryption (NEW)
- `pkg/mcp/mcp.go` — Server bootstrap, lifecycle, route stubs, GetMCPProxyPort() (NEW)
- `pkg/store/database/migrate.go` — Migration registration (MODIFY — add 1 line)
- `pkg/store/database/migrations/sqlite/026_add_mcp_servers.up.sql` — New
- `pkg/store/database/migrations/postgres/026_add_mcp_servers.up.sql` — New
- `pkg/mcp/store_test.go` — Store CRUD tests (NEW)
- `pkg/mcp/validation_test.go` — Validation tests (NEW)

## Constraints
- MCP port read from `MCP_PROXY_PORT` env var (or `0` = disabled)
- MCP module is completely optional — if port is 0 or unset, module is not started
- Store uses `database.Store.DB` and `database.Store.Dialect` directly (same pattern as auth, usage)
- All SQL queries must use `QueryBuilder` for dialect compatibility
- `auth_token` encrypted with `crypto.Encrypt()` on Create/Update, decrypted with `crypto.Decrypt()` on Get/List
- Timestamps generated in Go code (`time.Now().UTC()`), not DB-level defaults (W7)
- Boolean fields use `INTEGER` in SQLite; `isEnabled()` helper converts to `bool` (W3)
- URL validation rejects private networks, localhost, link-local, non-HTTP(S) schemes (W1)
- Header blocklist prevents injection of `Host`, `Content-Length`, `Transfer-Encoding`, `Connection` (W2)
- Enum validation on `TransportType` and `AuthType` (W10)
- JSON headers validated as valid JSON object on create/update (W10)

## Detailed Implementation Notes

### Encryption in Store (C4)

```go
// store.go
import "github.com/disillusioners/llm-supervisor-proxy/pkg/crypto"

func (s *MCPStore) CreateServer(ctx context.Context, req CreateMCPServerRequest) (*MCPServer, error) {
    // Encrypt auth_token before storing
    encryptedToken, err := crypto.Encrypt(req.AuthToken)
    if err != nil {
        return nil, fmt.Errorf("failed to encrypt auth token: %w", err)
    }
    // ... INSERT with encryptedToken ...
}

func (s *MCPStore) GetServer(ctx context.Context, id string) (*MCPServer, error) {
    // ... SELECT ...
    // Decrypt auth_token after reading
    if server.AuthToken != "" {
        decrypted, err := crypto.Decrypt(server.AuthToken)
        if err != nil {
            return nil, fmt.Errorf("failed to decrypt auth token: %w", err)
        }
        server.AuthToken = decrypted
    }
    return server, nil
}
```

### Boolean Helper for SQLite (W3)

```go
// store.go
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
```

### GetMCPProxyPort (C7)

```go
// mcp.go
import (
    "os"
    "strconv"
)

func GetMCPProxyPort() int {
    val := os.Getenv("MCP_PROXY_PORT")
    if val == "" {
        return 0
    }
    port, err := strconv.Atoi(val)
    if err != nil || port < 1 || port > 65535 {
        return 0
    }
    return port
}

// Server struct — tokenStore included from day one (NB1)
type Server struct {
    port       int
    store      *MCPStore
    bus        *events.Bus
    tokenStore auth.TokenStoreInterface
    connMgr    *ConnectionManager
    httpServer *http.Server
}

// NewServer — tokenStore is a required parameter (NB1)
func NewServer(port int, store *MCPStore, bus *events.Bus, tokenStore auth.TokenStoreInterface) *Server {
    return &Server{
        port:       port,
        store:      store,
        bus:        bus,
        tokenStore: tokenStore,
    }
}

// setupRoutes — empty stub, filled by Phase 2 with transport routes (B1)
func (s *Server) setupRoutes(mux *http.ServeMux) {
    mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
        w.Write([]byte("ok"))
    })
    // Phase 2 will add: /mcp/{id}/sse, /mcp/{id}/messages, /mcp/{id}/http
}

// RegisterAPIHandlers — empty stub, filled by Phase 3 with management API routes (B1)
func (s *Server) RegisterAPIHandlers(mux *http.ServeMux) {
    // Phase 3 will add: /fe/api/mcp-servers, /fe/api/mcp-servers/, /fe/api/mcp-servers/status
}

func (s *Server) Start(ctx context.Context) error {
    mux := http.NewServeMux()
    s.setupRoutes(mux)
    s.httpServer = &http.Server{Addr: ":" + strconv.Itoa(s.port), Handler: mux}
    return s.httpServer.ListenAndServe()
}

func (s *Server) Stop(ctx context.Context) error {
    if s.httpServer != nil {
        return s.httpServer.Shutdown(ctx)
    }
    return nil
}
```

### URL Validation (W1)

```go
// validation.go
import (
    "net"
    "net/url"
    "strings"
)

var blockedHosts = []string{"localhost", "127.0.0.1", "0.0.0.0", "::1"}

func ValidateUpstreamURL(rawURL string) error {
    u, err := url.Parse(rawURL)
    if err != nil {
        return fmt.Errorf("invalid URL: %w", err)
    }
    if u.Scheme != "http" && u.Scheme != "https" {
        return fmt.Errorf("only http and https schemes allowed")
    }
    if u.Host == "" {
        return fmt.Errorf("host is required")
    }
    host := u.Hostname()
    for _, blocked := range blockedHosts {
        if host == blocked {
            return fmt.Errorf("localhost URLs are not allowed")
        }
    }
    ip := net.ParseIP(host)
    if ip != nil {
        if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
            return fmt.Errorf("private/local network URLs are not allowed")
        }
    }
    return nil
}
```

### Header Blocklist (W2)

```go
// validation.go
var blockedHeaders = map[string]bool{
    "host":              true,
    "content-length":    true,
    "transfer-encoding": true,
    "connection":        true,
}

func ValidateCustomHeaders(headersJSON string) error {
    if headersJSON == "" || headersJSON == "{}" {
        return nil
    }
    var headers map[string]string
    if err := json.Unmarshal([]byte(headersJSON), &headers); err != nil {
        return fmt.Errorf("invalid JSON: %w", err)
    }
    for key := range headers {
        if blockedHeaders[strings.ToLower(key)] {
            return fmt.Errorf("header '%s' is not allowed", key)
        }
    }
    return nil
}
```

## Deliverables
- [ ] Migration 026 creates `mcp_servers` table in both SQLite and PostgreSQL
- [ ] `MCPServer` Go struct with JSON tags and enum validation
- [ ] `MCPStore` with full CRUD (List, Get, Create, Update, Delete) — encrypt/decrypt auth_token
- [ ] `mcp.Server` with constructor `NewServer(port, store, bus, tokenStore)` from day one (NB1), Start, Stop, GetMCPProxyPort()
- [ ] Two empty route stubs: `setupRoutes()` and `RegisterAPIHandlers(mux)` for Phases 2 and 3 to fill independently (B1)
- [ ] Validation: URL (SSRF prevention), header blocklist, enum, JSON headers
- [ ] Boolean helper `isEnabled()` for SQLite compatibility
- [ ] Go-level timestamps (`time.Now().UTC()`)
- [ ] MCP module only starts when `MCP_PROXY_PORT` is set and non-zero
- [ ] Unit tests for store CRUD (including encrypt/decrypt), validation, boolean conversion
