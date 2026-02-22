# Database Storage Migration

This document describes the migration from JSON config files to SQLite/PostgreSQL database storage.

## Overview

The application now uses a database for configuration storage instead of JSON files:

| Environment | Database | Location |
|-------------|----------|----------|
| **Local Development** | SQLite | `~/.config/llm-supervisor-proxy/config.db` |
| **Production** | PostgreSQL | Configured via `DATABASE_URL` |

The database type is determined automatically by the presence of the `DATABASE_URL` environment variable.

## Usage

### Local Development (SQLite)

```bash
# No configuration needed - SQLite is used by default
./llm-supervisor-proxy
```

The SQLite database is automatically created at `~/.config/llm-supervisor-proxy/config.db`.

### Production (PostgreSQL)

```bash
# Set DATABASE_URL to use PostgreSQL
export DATABASE_URL="postgres://user:password@hostname:5432/database_name?sslmode=require"
./llm-supervisor-proxy
```

## Migration from JSON

On first run with database storage, existing JSON configuration files are automatically migrated:

1. `~/.config/llm-supervisor-proxy/config.json` → `configs` table
2. `~/.config/llm-supervisor-proxy/models.json` → `models` table

Config and models are migrated independently - if you have models but no custom config, only models will be migrated.

After successful migration, JSON files are renamed to `.migrated` (e.g., `config.json.migrated`).

## Database Schema

### configs table (SQLite)

```sql
CREATE TABLE configs (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    version TEXT NOT NULL DEFAULT '1.0',
    upstream_url TEXT NOT NULL DEFAULT 'http://localhost:4001',
    port INTEGER NOT NULL DEFAULT 4321,
    idle_timeout_ms INTEGER NOT NULL DEFAULT 60000,
    max_generation_time_ms INTEGER NOT NULL DEFAULT 300000,
    max_upstream_error_retries INTEGER NOT NULL DEFAULT 1,
    max_idle_retries INTEGER NOT NULL DEFAULT 2,
    max_generation_retries INTEGER NOT NULL DEFAULT 1,
    loop_detection_json TEXT NOT NULL DEFAULT '{}',
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
```

### models table (SQLite)

```sql
CREATE TABLE models (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    fallback_chain_json TEXT NOT NULL DEFAULT '[]',
    truncate_params_json TEXT NOT NULL DEFAULT '[]',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_models_enabled ON models(enabled);
```

### PostgreSQL Schema Differences

When using PostgreSQL, the schema uses native types:

- `enabled BOOLEAN NOT NULL DEFAULT true` instead of `INTEGER`
- `TIMESTAMPTZ` instead of `TEXT` for timestamps
- `JSONB` instead of `TEXT` for JSON columns
- `NOW()` instead of `datetime('now')`

## Architecture

### Dialect-Aware Query System

To support both SQLite and PostgreSQL, the implementation uses a `QueryBuilder` that generates dialect-specific SQL:

- **Placeholders**: SQLite uses `?`, PostgreSQL uses `$1, $2, ...`
- **Boolean handling**: SQLite uses `0/1`, PostgreSQL uses `true/false`
- **Timestamps**: SQLite uses `datetime('now')`, PostgreSQL uses `NOW()`

```go
qb := NewQueryBuilder(store.Dialect)
query := qb.UpdateConfig() // Returns dialect-appropriate query
```

### Package Structure

```
pkg/store/database/
├── connection.go      # Database connection with dialect detection
├── migrate.go         # Schema migrations (SQLite + PostgreSQL)
├── json_migrate.go    # One-time JSON → DB migration
├── querybuilder.go    # Dialect-aware SQL query generation
├── store.go           # ConfigManager & ModelsManager implementations
├── init.go            # Initialization helper functions
├── database_test.go   # Comprehensive test suite
└── migrations/
    ├── 001_initial.up.sql
    └── 001_initial.down.sql
```

### Interfaces

Two new interfaces enable abstraction over storage backends:

**config.ManagerInterface** (`pkg/config/config.go`):
```go
type ManagerInterface interface {
    Get() Config
    GetUpstreamURL() string
    GetPort() int
    GetIdleTimeout() time.Duration
    GetMaxGenerationTime() time.Duration
    GetMaxUpstreamErrorRetries() int
    GetMaxIdleRetries() int
    GetMaxGenerationRetries() int
    GetLoopDetection() LoopDetectionConfig
    Save(Config) (*SaveResult, error)
    IsReadOnly() bool
}
```

**models.ModelsConfigInterface** (`pkg/models/config.go`):
```go
type ModelsConfigInterface interface {
    GetModels() []ModelConfig
    GetEnabledModels() []ModelConfig
    GetTruncateParams(modelID string) []string
    GetFallbackChain(modelID string) []string
    AddModel(model ModelConfig) error
    UpdateModel(modelID string, model ModelConfig) error
    RemoveModel(modelID string) error
    Save() error
    Validate() error
}
```

### Technology Stack

| Component | Library | Purpose |
|-----------|---------|---------|
| **SQLite Driver** | `modernc.org/sqlite` | Pure Go, no CGO required |
| **PostgreSQL Driver** | `github.com/jackc/pgx/v5/stdlib` | High-performance PG driver |
| **Query Builder** | Custom `QueryBuilder` | Dialect-aware SQL generation |

## Configuration

### Environment Variables

| Variable | Description | Example |
|----------|-------------|---------|
| `DATABASE_URL` | PostgreSQL connection string (enables PG) | `postgres://user:pass@host:5432/db` |

If `DATABASE_URL` is not set or starts with `sqlite:`, SQLite is used.

### Connection String Formats

**PostgreSQL:**
```
postgres://username:password@hostname:5432/database_name?sslmode=require
```

**SQLite (explicit):**
```
sqlite:/path/to/database.db
```

## Testing

Run the database package tests:

```bash
go test ./pkg/store/database/... -v
```

### Test Coverage

- `TestSQLiteConnection` - Basic SQLite connection and migrations
- `TestConfigManager` - Config CRUD operations
- `TestModelsManager` - Model CRUD operations
- `TestJSONMigration` - JSON file migration (Linux only)
- `TestInitializeAll` - Full initialization flow
- `TestConfigDurationConversion` - Duration serialization

### PostgreSQL Testing

> **Note**: PostgreSQL integration tests require a running PostgreSQL instance. These tests are not included in the default test suite. To test PostgreSQL functionality manually:
>
> ```bash
> # Start PostgreSQL (example with Docker)
> docker run -d --name test-postgres -e POSTGRES_PASSWORD=test -p 5432:5432 postgres
>
> # Run with DATABASE_URL
> DATABASE_URL="postgres://postgres:test@localhost:5432/postgres?sslmode=disable" ./llm-supervisor-proxy
> ```

## Migration Guide

### For Developers

1. **No code changes required** - The interfaces abstract storage details
2. **Use the interfaces** - Accept `config.ManagerInterface` and `models.ModelsConfigInterface` instead of concrete types
3. **Initialization** - Use `database.InitializeAll()` to create store and managers

```go
import "github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"

func main() {
    ctx := context.Background()
    bus := events.NewBus()
    
    // Initialize database and managers
    store, configMgr, modelsMgr, err := database.InitializeAll(ctx, bus)
    if err != nil {
        log.Fatal(err)
    }
    defer store.Close()
    
    // Use managers as before
    cfg := configMgr.Get()
    models := modelsMgr.GetModels()
}
```

### For Deployment

1. **Set `DATABASE_URL`** environment variable for PostgreSQL
2. **Run migrations** - Automatic on startup
3. **Verify migration** - Check logs for "Migrated config.json to database"

## Known Limitations

### Schema Migrations

The current implementation uses `CREATE TABLE IF NOT EXISTS` for schema initialization. Future schema changes will need to be handled manually or through a migration system. A `schema_migrations` tracking table is not yet implemented.

### Boolean Type Handling

The `dbModelRow.isEnabled()` helper method handles the difference between SQLite (`int64`) and PostgreSQL (`bool`) boolean representations automatically.

## Rollback

To revert to JSON-based configuration:

1. Stop the application
2. Rename `.migrated` files back:
   ```bash
   mv ~/.config/llm-supervisor-proxy/config.json.migrated ~/.config/llm-supervisor-proxy/config.json
   mv ~/.config/llm-supervisor-proxy/models.json.migrated ~/.config/llm-supervisor-proxy/models.json
   ```
3. Remove or comment out database initialization code in `main.go`
4. Revert to `config.NewManager()` and `models.NewModelsConfig()`

## Troubleshooting

### Database locked (SQLite)

SQLite may return "database is locked" under high concurrency. Solutions:
- Use PostgreSQL for production workloads
- Increase SQLite timeout: `?_busy_timeout=5000`

### Migration fails

1. Check file permissions on `~/.config/llm-supervisor-proxy/`
2. Verify JSON files are valid
3. Check logs for specific error messages

### PostgreSQL connection errors

1. Verify `DATABASE_URL` format
2. Check network connectivity
3. Verify SSL settings match server requirements

### PostgreSQL syntax errors

If you encounter SQL syntax errors with PostgreSQL:
1. Ensure you're using the latest version (dialect-aware queries)
2. Check that the `QueryBuilder` is being used for all queries
3. Report issues with the specific query and error message
