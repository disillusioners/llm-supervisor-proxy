# LLM Supervisor Proxy - Project Exploration Report

**Date:** 2026-04-08
**Purpose:** Planning feature to restrict ultimate model access to specific tokens
**Status:** Complete

---

## 1. Project Structure

### Top-Level Layout
```
llm-supervisor-proxy/
├── cmd/                    # Application entry point (main.go)
├── pkg/                    # All Go source code (20+ packages)
├── config/                 # Configuration templates
├── docs/                   # Documentation
├── k8s/                    # Kubernetes manifests
├── plans/                  # Design documents
├── specs/                  # Specifications
├── test/                   # Test utilities and mock LLMs
├── go.mod / go.sum         # Go dependencies
├── Makefile                # Build automation
└── Dockerfile              # Container build
```

### Key Packages

| Package | Purpose |
|---------|---------|
| `pkg/proxy/` | Core proxy logic, HTTP handlers, race retry (~4000+ lines) |
| `pkg/store/database/` | SQLite/PostgreSQL layer with sqlc |
| `pkg/ui/frontend/` | Preact + TypeScript + Tailwind web UI |
| `pkg/ultimatemodel/` | Ultimate model bypass handler (~400 lines) |
| `pkg/auth/` | API token authentication |
| `pkg/config/` | Configuration management (50+ fields) |
| `pkg/loopdetection/` | AI loop detection (6 strategies) |
| `pkg/loopdetection/` | AI loop detection (6 strategies) |
| `pkg/toolrepair/` | Tool call JSON repair |
| `pkg/models/` | Model configuration, fallback chains, credentials |
| `pkg/translator/` | OpenAI ↔ Anthropic protocol translation |
| `pkg/toolcall/` | Streaming JSON fragment buffer |
| `pkg/usage/` | Token usage tracking |

### Entry Point (`cmd/main.go`)
- Initializes Event Bus, Request Store, Database, Encryption, Buffer Store
- Sets up HTTP routes for `/v1/chat/completions`, `/v1/messages`, `/v1/models`
- Web UI on `/` and `/api/*`

---

## 2. Token Management System

### Token Model (`pkg/auth/token.go:24-31`)
```go
type AuthToken struct {
    ID        string     // UUID
    Name      string     // Human-readable label
    TokenHash string     // SHA-256 hash (never plaintext)
    ExpiresAt *time.Time // Optional expiration
    CreatedAt time.Time
    CreatedBy string     // Creator identifier
}
```

### Token Storage
- **Format:** `sk-` + 64 hex chars (32 random bytes)
- **Only hash stored** - plaintext shown once at creation
- **Table:** `auth_tokens` (id, token_hash, name, expires_at, created_at, created_by)
- **Usage table:** `token_hourly_usage` (token_id, hour_bucket, request_count, prompt_tokens, completion_tokens, total_tokens)

### Authentication Flow (`pkg/proxy/handler.go:229-273`)
1. Extract API key from: `Authorization: Bearer` → `X-API-Key` → `x-api-key`
2. Hash and validate against DB
3. Check expiration
4. Store token metadata in request context

### TokenStore Interface (`pkg/auth/store.go:20-25`)
```go
type TokenStoreInterface interface {
    ValidateToken(ctx, plaintext) (*AuthToken, error)
    CreateToken(ctx, name, expiresAt, createdBy) (plaintext, *AuthToken, error)
    DeleteToken(ctx, id) error
    ListTokens(ctx) ([]AuthToken, error)
}
```

### API Endpoints
| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/fe/api/tokens` | List all tokens |
| POST | `/fe/api/tokens` | Create new token |
| DELETE | `/fe/api/tokens/{id}` | Delete token |
| GET | `/fe/api/usage/tokens` | Token usage stats |

### ⚠️ CRITICAL FINDING: No Permissions/Roles System
**Tokens are binary - valid or invalid. No role, scope, or permission system exists.**

| Gap | Impact |
|-----|--------|
| No permission model | All tokens have equal access to all features |
| No update endpoint | Can't modify token properties after creation |
| No token rotation | Compromised tokens require full recreate |
| No per-token config | Can't associate features with specific tokens |

---

## 3. Ultimate Model Feature

### What is Ultimate Model?
**Duplicate request deduplication with retry logic.** When a request with identical message content has failed before, it bypasses normal race-retry and routes to a designated "ultimate model" for a fresh answer.

### Architecture

```
pkg/ultimatemodel/
├── hash_cache.go       # Circular buffer + retry counter (thread-safe)
├── handler.go          # Main interface: ShouldTrigger(), MarkFailed(), Execute()
├── handler_internal.go  # Direct provider API (uses DB credentials)
└── handler_external.go  # Proxy to upstream URL
```

### Key Components

#### HashCache (`pkg/ultimatemodel/hash_cache.go`)
```go
type HashCache struct {
    mu           sync.RWMutex
    hashes       []string        // circular buffer
    size         int             // max capacity
    retryCounter map[string]int  // hash → retry count
}
```
- Stores SHA256 hash of messages (role + content per message)
- Tracks retry count per hash
- Max retries configurable (default: 5)

#### Handler Interface (`pkg/ultimatemodel/handler.go`)
```go
type Handler struct {
    config    config.ManagerInterface
    modelsMgr models.ModelsConfigInterface
    hashCache *HashCache
    eventBus  *events.Bus
}

type ShouldTriggerResult struct {
    Triggered      bool
    Hash           string
    RetryExhausted bool
    CurrentRetry   int
    MaxRetries     int
}
```

### Trigger Flow (`pkg/proxy/handler.go:380-511`)
```
1. Generate hash = SHA256(role|content per message)
2. Contains(hash)?
   ├─ NO → First request, continue to normal flow
   └─ YES → Duplicate detected
       ├─ IncrementAndCheckRetry(hash, maxRetries)
       ├─ Exhausted? → SendRetryExhaustedError, RETURN
       └─ Not exhausted → Execute via ultimate model
```

### Internal vs External Handlers
| Aspect | Internal | External |
|--------|----------|----------|
| **Upstream** | Direct provider API | Configured `UpstreamURL` |
| **Auth** | Credential from DB | Upstream handles auth |
| **Model Resolution** | From DATABASE | Model ID passed directly |

### ⚠️ CRITICAL FINDING: No Token Consideration in Ultimate Model
The ultimate model feature does NOT check the requesting token at all. It's purely hash-based.

---

## 4. Frontend Structure

### Tech Stack
| Layer | Technology |
|-------|------------|
| Framework | Preact 10.19.3 (lightweight React) |
| Build | Vite 5.1.0 |
| Styling | Tailwind CSS 3.4.1 |
| Language | TypeScript 5.3.3 (strict mode) |

### Component Organization
```
pkg/ui/frontend/src/
├── App.tsx                    # Router: /ui (Dashboard), /ui/settings
├── Header.tsx                 # Top nav bar
├── RequestList.tsx            # Left panel request list
├── RequestDetail.tsx          # Right panel request details
├── SettingsPage.tsx           # Settings container with tabs
├── Toast.tsx                  # Notification system
├── types.ts                   # Shared TypeScript types
├── hooks/
│   ├── useRequests.ts         # Request list
│   ├── useConfig.ts           # App config
│   ├── useModels.ts           # Models
│   ├── useTokens.ts           # Tokens
│   ├── useCredentials.ts      # Credentials
│   ├── useUsage.ts            # Usage stats
│   └── useEvents.ts           # SSE real-time events
├── components/
│   ├── config/                # Proxy, Models, Credentials, Loop, ToolRepair
│   ├── tokens/                # TokenList, TokenForm
│   └── usage/                 # UsageTab, UsageSummaryCards, UsageTable
```

### Existing Tabs (7 total in SettingsPage.tsx)
```
'proxy' | 'models' | 'credentials' | 'loop_detection' | 'tool_repair' | 'tokens' | 'usage'
```

### Tab Registration Pattern (SettingsPage.tsx)
To add a new tab, 3 changes needed:
1. **Add type** (line 28): `type TabType = '...' | 'new_tab';`
2. **Add button** (lines 284-352): `<button onClick={() => setActiveTab('new_tab')}>New Tab</button>`
3. **Add render** (lines 356-461): `{activeTab === 'new_tab' && <NewComponent />}`

### Token UI Components
- `TokenList` - Displays tokens with name, prefix, created, expires, last used
- `TokenForm` - Create new token with name + optional expiry
- `useTokens` hook - `createToken()`, `deleteToken()`, `refetch()`

---

## 5. Database Layer

### Database Engines
| Environment | Driver | Default Behavior |
|-------------|--------|------------------|
| **SQLite** | `modernc.org/sqlite` (pure Go) | Default when `DATABASE_URL` empty |
| **PostgreSQL** | `jackc/pgx/v5/stdlib` | When `DATABASE_URL` set (non-sqlite) |

### Store Package Structure
```
pkg/store/database/
├── store.go         # ConfigManager, ModelsManager (business logic)
├── connection.go    # Store struct, NewConnection(), dialect detection
├── migrate.go       # Migration runner with embedded SQL files
├── init.go          # Initialize(), InitializeManagers(), InitializeAll()
├── querybuilder.go  # Dialect-aware SQL query generation
├── db/              # sqlc-generated code
│   ├── db.go        # DBTX interface, Queries struct
│   ├── models.go    # Generated model structs
│   └── queries.sql.go # Generated query methods
└── migrations/
    ├── sqlite/      # 19 migration files (001-019)
    └── postgres/    # 19 migration files (001-019)
```

### Table Schemas

**`configs`** (single-row table)
```sql
CREATE TABLE configs (
    id INTEGER PRIMARY KEY CHECK (id = 1),  -- Always row ID = 1
    config_json TEXT NOT NULL DEFAULT '{}',  -- All config as JSON
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

**`models`**
```sql
CREATE TABLE models (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    fallback_chain_json TEXT DEFAULT '[]',
    truncate_params_json TEXT DEFAULT '[]',
    internal INTEGER DEFAULT 0,
    credential_id TEXT,
    internal_base_url TEXT,
    internal_model TEXT,
    release_stream_chunk_deadline INTEGER DEFAULT 0,
    peak_hour_enabled INTEGER DEFAULT 0,
    peak_hour_start TEXT DEFAULT '',
    peak_hour_end TEXT DEFAULT '',
    peak_hour_timezone TEXT DEFAULT '',
    peak_hour_model TEXT DEFAULT '',
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);
```

**`auth_tokens`**
```sql
CREATE TABLE auth_tokens (
    id TEXT PRIMARY KEY,
    token_hash TEXT NOT NULL,
    name TEXT NOT NULL,
    expires_at TEXT,
    created_at TEXT,
    created_by TEXT NOT NULL
);
```

**`credentials`**
```sql
CREATE TABLE credentials (
    id TEXT PRIMARY KEY,
    provider TEXT NOT NULL,
    api_key TEXT NOT NULL,  -- Encrypted
    base_url TEXT NOT NULL,
    created_at TEXT,
    updated_at TEXT
);
```

**`token_hourly_usage`** (Migration 019)
```sql
CREATE TABLE token_hourly_usage (
    token_id TEXT NOT NULL,
    hour_bucket TEXT NOT NULL,          -- Format: "2026-04-08T14:00"
    request_count INTEGER DEFAULT 0,
    prompt_tokens INTEGER DEFAULT 0,
    completion_tokens INTEGER DEFAULT 0,
    total_tokens INTEGER DEFAULT 0,
    PRIMARY KEY (token_id, hour_bucket)
);
```

### Migration Approach
- **Embedded FS pattern** - SQL files compiled into binary via `//go:embed`
- **Migration table:** `schema_migrations(version TEXT, applied_at TEXT/TIMESTAMPTZ)`
- **Execution:** Check applied versions → execute unapplied in order → record

### Query Patterns
1. **sqlc-generated** - Static queries in `queries.sql`, type-safe `Queries` struct
2. **QueryBuilder** - Dialect-aware dynamic queries in `store.go`

### Configuration
**Precedence:** `Database JSON` → `Environment Variables (if APPLY_ENV_OVERRIDES=true)` → `Defaults`

---

## 6. Feature Implementation Requirements

### For "Restrict Ultimate Model Access to Specific Tokens"

#### Backend Changes Needed

1. **Database Schema**
   - Add `allowed_tokens` field to ultimate model config (JSON array of token IDs)
   - OR create new `ultimate_model_token_allowlist` table

2. **Token Model Enhancement**
   - Add `ultimate_model_enabled` boolean field to `auth_tokens` table
   - Alternative: `allowed_ultimate_models` field (JSON array of ultimate model IDs)

3. **Config Layer**
   - Add `AllowedTokens` or `TokenAllowlist` to `UltimateModelConfig` struct
   - Add getter/setter methods

4. **Handler Layer** (`pkg/proxy/handler.go`)
   - After authentication, check if token is in ultimate model allowlist
   - If NOT allowed: skip ultimate model check, fall through to normal flow

5. **Ultimate Model Handler** (`pkg/ultimatemodel/handler.go`)
   - Accept token context in `ShouldTrigger()`
   - Only trigger if token is in allowlist (or allowlist is empty = all allowed)

#### Frontend Changes Needed

1. **Types** (`types.ts`)
   - Add `ultimate_model_allowed` or similar to `ApiToken` interface

2. **Token Management UI**
   - Add checkbox/toggle in `TokenForm` for ultimate model access
   - Add indicator in `TokenList`

3. **Settings UI** (new tab or existing)
   - Add ultimate model token allowlist configuration
   - OR enable/disable per-token in token management

4. **API Endpoints**
   - Update token create/update to include ultimate model flag
   - Add endpoint to configure ultimate model allowlist

#### Key Files to Modify
- `pkg/store/database/migrations/` - Add migration for new field(s)
- `pkg/auth/token.go` - Add field to AuthToken struct
- `pkg/config/config.go` - Add ultimate model allowlist config
- `pkg/proxy/handler.go` - Add token allowlist check in trigger flow
- `pkg/ultimatemodel/handler.go` - Accept token context
- `pkg/ui/frontend/src/types.ts` - Add frontend type
- `pkg/ui/frontend/src/components/tokens/` - Update UI
- `pkg/ui/frontend/src/components/config/` - Update ultimate model settings
