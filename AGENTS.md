# AGENTS.md - Coding Agent Guidelines

This document provides essential information for AI coding agents working in this repository.

## Project Overview

**LLM Supervisor Proxy** - An OpenAI-compatible proxy server with Anthropic Messages API support, featuring retry logic, loop detection, and a web UI.

| Component | Technology |
|-----------|------------|
| Backend | Go 1.24 |
| Frontend | TypeScript + Preact + Vite |
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

# Development server
npm run dev

# Production build
npm run build
```

### Database (sqlc)

```bash
# Generate database code from SQL queries
sqlc generate
```

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

---

## Project Structure

```
llm-supervisor-proxy/
├── cmd/main.go              # Entry point
├── pkg/
│   ├── proxy/               # Core proxy logic & handlers
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
- Migrations in `pkg/store/database/migrations/`
- Queries defined in `pkg/store/database/sqlc/queries.sql`
