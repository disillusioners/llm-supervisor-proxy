# AGENTS.md - Coding Agent Guidelines

This document provides essential information for AI coding agents working in this repository.

## Project Overview

**LLM Supervisor Proxy** - An OpenAI-compatible proxy server with Anthropic Messages API support, featuring retry logic, loop detection, and a web UI.

| Component | Technology |
|-----------|------------|
| Backend | Go 1.24 |
| Frontend | TypeScript + Preact + Vite + Tailwind CSS |
| Database | SQLite (dev) / PostgreSQL (prod) |
| Code Gen | sqlc for database queries |

---

## Build & Test Commands

### Go Backend

```bash
# Build everything (frontend + backend)
make all

# Build backend only (auto-increments VERSION)
make build

# Build frontend only
make build-frontend

# Run all tests
make test
# or
go test ./...

# Run a single test
go test -run TestName ./pkg/path/
# Example:
go test -run TestHandlerInitialize ./pkg/proxy/
go test -run TestDuration_MarshalJSON ./pkg/config/

# Run tests with verbose output
go test -v ./...

# Run tests in a specific package
go test ./pkg/config/...

# Build and run locally
make run
```

### Frontend

```bash
cd pkg/ui/frontend

# Install dependencies
npm install

# Development server (hot reload)
npm run dev

# Production build
npm run build

# Preview production build
npm run preview
```

### Database (sqlc)

```bash
# Generate database code from SQL queries
sqlc generate

# SQL queries location: pkg/store/database/sqlc/queries.sql
# Generated code location: pkg/store/database/db/
# Migrations location: pkg/store/database/migrations/
```

---

## Environment Variables

Key configuration via environment variables (highest precedence):

| Variable | Default | Description |
|----------|---------|-------------|
| `UPSTREAM_URL` | `http://localhost:4001` | LLM provider URL |
| `PORT` | `4321` | Proxy listening port |
| `IDLE_TIMEOUT` | `60s` | Max wait between tokens before spawning parallel requests |
| `STREAM_DEADLINE` | `110s` | Time limit before picking best buffer and continuing streaming |
| `MAX_GENERATION_TIME` | `300s` | **Absolute hard timeout** for entire request lifecycle |
| `LOOP_DETECTION_ENABLED` | `true` | Enable loop detection |
| `LOOP_DETECTION_SHADOW_MODE` | `true` | Shadow mode (log only) |
| `DATABASE_URL` | *(empty)* | PostgreSQL connection string |

### Race Retry Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `RACE_RETRY_ENABLED` | `false` | Enable parallel race retry |
| `RACE_PARALLEL_ON_IDLE` | `true` | Spawn parallel requests on idle timeout |
| `RACE_MAX_PARALLEL` | `3` | Max parallel requests (main + second + fallback) |
| `RACE_MAX_BUFFER_BYTES` | `5242880` | Max bytes per request buffer (5MB) |

---

## Code Style Guidelines

### Go

#### Imports
- Standard library first, separated by blank line
- External packages second, separated by blank line  
- Local packages last
- Use absolute imports with full module path

```go
import (
    "context"
    "encoding/json"
    "fmt"

    "github.com/some/external/pkg"

    "github.com/disillusioners/llm-supervisor-proxy/pkg/config"
)
```

#### Naming Conventions
- **Files**: `snake_case.go` (e.g., `config_manager.go`)
- **Packages**: lowercase single word (e.g., `config`, `proxy`, `auth`)
- **Variables/Functions**: `camelCase` (exported: `PascalCase`)
- **Interfaces**: `PascalCase` + `Interface` suffix or `-er` suffix (e.g., `ManagerInterface`, `Reader`)
- **Constants**: `PascalCase` for exported, `camelCase` for private

#### Structs & Types
- Add comments for exported types
- Use JSON tags with `snake_case`
- Group related fields with blank lines

```go
// Config holds all application configuration
type Config struct {
    Version     string `json:"version"`
    UpstreamURL string `json:"upstream_url"`
    Port        int    `json:"port"`

    // Timeouts
    IdleTimeout       Duration `json:"idle_timeout"`
    MaxGenerationTime Duration `json:"max_generation_time"`
}
```

#### Error Handling
- Return errors, don't panic
- Wrap errors with context using `fmt.Errorf("context: %w", err)`
- Validate inputs early with clear error messages

```go
if err != nil {
    return fmt.Errorf("failed to parse config: %w", err)
}
```

#### Concurrency
- Use `sync.RWMutex` for read-heavy workloads
- Always `defer m.mu.RUnlock()` or `defer m.mu.Unlock()` immediately after locking
- Prefer channels for communication, mutexes for state

#### Testing
- Test files: `*_test.go` in same package
- Test functions: `func TestFeatureName(t *testing.T)`
- Use table-driven tests for multiple cases
- Use `httptest` for HTTP handler tests

```go
func TestConfigValidation(t *testing.T) {
    tests := []struct {
        name    string
        config  Config
        wantErr bool
    }{
        // test cases...
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := tt.config.Validate()
            if (err != nil) != tt.wantErr {
                t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
            }
        })
    }
}
```

---

### TypeScript / Preact

#### Imports
- React/Preact hooks first
- Components second
- Utilities/hooks last
- Use relative imports with `./` prefix

```typescript
import { useState, useCallback } from 'preact/hooks';
import { Header, RequestList } from './components';
import { useRequests, useConfig } from './hooks';
```

#### Naming Conventions
- **Files**: `PascalCase.tsx` for components, `camelCase.ts` for utilities
- **Components**: `PascalCase` function components
- **Variables/Functions**: `camelCase`
- **Types/Interfaces**: `PascalCase`
- **Constants**: `UPPER_SNAKE_CASE` or `PascalCase`

#### Component Structure
- Use functional components with hooks
- Destructure props in function signature
- Group hooks at top, then handlers, then JSX

```typescript
export function RequestList({ requests, onSelect, loading }: RequestListProps) {
  // Hooks
  const [selectedId, setSelectedId] = useState<string | null>(null);
  
  // Handlers
  const handleClick = useCallback((id: string) => {
    setSelectedId(id);
    onSelect(id);
  }, [onSelect]);

  // Render
  return (
    <div class="request-list">
      {/* JSX */}
    </div>
  );
}
```

#### TypeScript Configuration
- Strict mode enabled
- `noUnusedLocals` and `noUnusedParameters` enabled
- JSX: `react-jsx` with `preact` import source

#### Styling (Tailwind CSS)
- Use Tailwind utility classes for styling
- Class names use `class` (not `className`) in Preact
- Responsive design with Tailwind breakpoints
- Example: `class="bg-gray-900 border-r border-gray-700"`

---

## Project Structure

```
llm-supervisor-proxy/
├── cmd/main.go              # Entry point
├── pkg/
│   ├── proxy/               # Core proxy logic & handlers
│   │   ├── handler.go           # Main HTTP handler with race retry integration
│   │   ├── race_coordinator.go  # Parallel race retry coordinator
│   │   ├── race_executor.go     # Upstream request execution
│   │   ├── race_request.go      # Request state structure
│   │   └── stream_buffer.go     # Thread-safe SSE chunk buffer
│   ├── config/              # Configuration management
│   ├── auth/                # Token authentication
│   ├── models/              # Data models
│   ├── providers/           # LLM provider adapters (OpenAI, Anthropic)
│   ├── loopdetection/       # AI loop detection
│   ├── events/              # Event bus for real-time updates
│   ├── store/database/      # Database layer (sqlc generated)
│   └── ui/frontend/         # Preact frontend
├── k8s/                     # Kubernetes manifests
└── docs/                    # Documentation
```

---

## Race Retry Architecture

The proxy uses a **parallel race retry** mechanism for maximum reliability:

```
Client Request
     │
     ├─► UltimateModel.ShouldTrigger() ──► YES ──► Execute ultimate model (no retry)
     │
     └─► NO ──► Race Coordinator
                     │
                     ├─ MAIN REQUEST (original model, starts immediately)
                     │       │
                     │       ├─ ERROR ──► Spawn parallel requests immediately
                     │       │
                     │       └─ IDLE TIMEOUT ──► Spawn parallel requests
                     │
                     ├─ SECOND REQUEST (same model, spawned on idle/error)
                     │
                     └─ FALLBACK REQUEST (first fallback model)
                             │
                             ▼
                     FIRST TO COMPLETE WINS
                     (others cancelled, winner streams to client)
```

**Key Components:**

| File | Purpose |
|------|---------|
| `race_coordinator.go` | Manages parallel requests, selects winner |
| `race_executor.go` | Executes HTTP requests to upstream |
| `race_request.go` | Request state with atomic status tracking |
| `stream_buffer.go` | Thread-safe chunk buffer with notification pattern |

**Thread Safety:**
- Uses notification channel pattern (not shared buffer)
- Atomic status transitions
- Mutex-protected chunk storage with GC-friendly pruning

For full design details, see [`plans/unified-race-retry-design.md`](plans/unified-race-retry-design.md).

---

## Key Patterns

### Configuration
- Precedence: Environment variables > Config file > Defaults
- Validate before saving
- Use atomic writes with temp files
- Backup before overwriting

### HTTP Handlers
- Use interfaces for testability
- Support both streaming and non-streaming responses
- Implement proper timeout handling with retries

### Database
- Use sqlc for type-safe SQL queries
- Queries defined in `pkg/store/database/sqlc/queries.sql`
- Generated code in `pkg/store/database/db/`
- After modifying queries, run `sqlc generate`

### Database Migrations
- Uses `embed.FS` to embed SQL files at compile time
- Tracked via `schema_migrations` table (version, applied_at)
- Dialect-specific directories: `migrations/sqlite/` and `migrations/postgres/`
- Naming convention: `NNN_description.up.sql` (e.g., `007_add_field.up.sql`)

**Adding a new migration:**
1. Create SQL files in both dialect directories:
   ```bash
   # SQLite: pkg/store/database/migrations/sqlite/007_add_field.up.sql
   # PostgreSQL: pkg/store/database/migrations/postgres/007_add_field.up.sql
   ```
2. Register in `pkg/store/database/migrate.go`:
   ```go
   var migrations = []migration{
       // ... existing
       {"007", "007_add_field.up"},
   }
   ```
3. Run `go build ./...` to embed new files
