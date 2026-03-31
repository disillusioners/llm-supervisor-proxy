# Tester Documentation: llm-supervisor-proxy

## Project Overview
Go-based proxy server for supervising and managing LLM API requests. Uses SQLite/PostgreSQL for storage, Preact frontend for UI.

## Technology Stack
- **Backend**: Go (net/http, custom migration framework)
- **Frontend**: Preact + Vite + TypeScript + Tailwind CSS
- **Database**: SQLite (default), PostgreSQL (supported)
- **Build**: `go build`, `npm run build` (frontend)

## Test Commands
- **Unit tests**: `go test ./... -v`
- **Unit tests (with race)**: `go test ./... -v -race`
- **Go vet**: `go vet ./...`
- **Frontend build**: `cd pkg/ui/frontend && npm run build`
- **Full build**: `go build ./...`

## Key Test Files
- `pkg/models/peak_hours_test.go` — Peak hour unit tests
- `pkg/store/database/config_peak_hour_test.go` — Peak hour config integration tests
- `pkg/store/database/database_test.go` — Database layer tests
- `pkg/proxy/authenticate_test.go` — Token identity / authenticate() function tests
- `pkg/proxy/counting_hooks_test.go` — Counting hooks at handler success points
- `pkg/ultimatemodel/usage_test.go` — Usage extraction from streaming/non-streaming responses
- `pkg/usage/counter_test.go` — Token hourly usage counter (Increment + GetTokenUsage)

## Testing Conventions
- Standard Go testing with `testing` package
- Table-driven tests for parameterized scenarios
- No external test frameworks required
- In-memory SQLite for database-layer tests
- Interfaces used for mockability (e.g., `auth.TokenStoreInterface`)
