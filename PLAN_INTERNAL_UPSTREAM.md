# Plan: Internal Upstream Implementation (Replace LiteLLM)

## Status: Backend Complete ✅ | Frontend Pending ⏳

**Last Updated**: 2026-02-27

| Phase | Status |
|-------|--------|
| Stage 1: Core Infrastructure | ✅ Complete |
| Stage 2: OpenAI Provider | ✅ Complete |
| Stage 3: Integration | ✅ Complete |
| Stage 4: Frontend | ⏳ Pending |

**Ready for**: Manual testing with API (curl/Postman), frontend development

---

## Overview

**Goal**: Add an "internal" option to model configuration that bypasses the upstream HTTP hop and directly calls AI providers from the proxy itself.

**Current Flow**:
```
Client → Proxy → Upstream (LiteLLM) → AI Provider
```

**Target Flow** (when `internal: true`):
```
Client → Proxy → AI Provider (direct)
```

---

## Phase 1: Database & Model Schema

### 1.1 Add Migration
**File**: `pkg/store/database/migrations/002_internal_upstream.up.sql`

```sql
-- Add internal upstream fields to models table
ALTER TABLE models ADD COLUMN internal INTEGER NOT NULL DEFAULT 0;
ALTER TABLE models ADD COLUMN internal_provider TEXT NOT NULL DEFAULT '';
ALTER TABLE models ADD COLUMN internal_api_key TEXT NOT NULL DEFAULT '';
ALTER TABLE models ADD COLUMN internal_base_url TEXT NOT NULL DEFAULT '';
ALTER TABLE models ADD COLUMN internal_model TEXT NOT NULL DEFAULT '';
ALTER TABLE models ADD COLUMN internal_key_version INTEGER NOT NULL DEFAULT 1;

-- Add index for internal lookup
CREATE INDEX idx_models_internal ON models(internal);
```

**Rollback File**: `pkg/store/database/migrations/002_internal_upstream.down.sql`
```sql
DROP INDEX idx_models_internal;
ALTER TABLE models DROP COLUMN internal_key_version;
ALTER TABLE models DROP COLUMN internal_model;
ALTER TABLE models DROP COLUMN internal_base_url;
ALTER TABLE models DROP COLUMN internal_api_key;
ALTER TABLE models DROP COLUMN internal_provider;
ALTER TABLE models DROP COLUMN internal;
```

### 1.2 Update Go Structs
**File**: `pkg/models/config.go`

Add fields to `ModelConfig`:
```go
Internal          bool   `json:"internal,omitempty"`
InternalProvider  string `json:"internal_provider,omitempty"`
InternalAPIKey    string `json:"-"`  // Never expose in JSON
InternalBaseURL   string `json:"internal_base_url,omitempty"`
InternalModel     string `json:"internal_model,omitempty"`
```

**Notes:**
- `InternalAPIKey` uses `json:"-"` to never expose in API responses
- `omitempty` ensures backward compatibility with existing configs
- After migration, regenerate sqlc models: `sqlc generate`

**Model Mapping Example** (following LiteLLM convention):
```
Model ID (user-facing): glm-5
Internal Provider: zhipu
Internal Model: GLM-5.0
→ Request sent to provider: model = "GLM-5.0"
```

No provider prefix needed in `internal_model` since `internal_provider` already specifies it.

---

## Phase 2: API Token Management

### 2.1 Token Generation
**New File**: `pkg/auth/token.go`

Simple API token system (full access to all models):
- `GenerateToken(name string, expiry *time.Time) (string, error)` - Generate random 32-char token with `sk-` prefix
- `ValidateToken(token string) (bool, *AuthToken)` - Validate and return token info (checks expiry if set)
- `HashToken(token string) string` - SHA-256 hash for storage

**Note**: User identity handled by external OAuth2. API tokens are for programmatic access with full model access. Expiry is optional (NULL = no expiry).

### 2.2 Token Storage
**New Migration**: `003_auth_tokens.up.sql`

```sql
CREATE TABLE auth_tokens (
    id TEXT PRIMARY KEY,
    token_hash TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    expires_at TEXT,
    created_at TEXT NOT NULL,
    created_by TEXT NOT NULL
);

CREATE INDEX idx_auth_tokens_hash ON auth_tokens(token_hash);
```

**MVP simplification**: Skip `last_used_at` to avoid modifying hot path. Add in Phase 2 with analytics.

### 2.3 Token API Endpoints
**File**: `pkg/ui/server.go`

Add endpoints:
- `POST /fe/api/tokens` - Create new token (returns token once)
- `GET /fe/api/tokens` - List all tokens
- `DELETE /fe/api/tokens/{id}` - Revoke token

---

## Phase 3: Internal Upstream Handler

### 3.1 Provider Interface
**New File**: `pkg/providers/interface.go`

```go
type Provider interface {
    Name() string
    ChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error)
    StreamChatCompletion(ctx context.Context, req *ChatCompletionRequest) (<-chan StreamEvent, error)
    IsRetryable(err error) bool  // Provider-specific retry logic
}

// Normalized streaming event (converted from provider-native format)
type StreamEvent struct {
    Type      string // "content", "done", "error"
    Content    string
    FinishReason string
    Error      error
}
```

### 3.2 Provider Implementations
**New Directory**: `pkg/providers/`

Files:
- `openai.go` - OpenAI-compatible (OpenAI, Groq, Together, etc.)
- `anthropic.go` - Anthropic Claude API
- `gemini.go` - Google Gemini API
- `factory.go` - Provider factory based on config

### 3.3 Request Routing
**File**: `pkg/proxy/handler_functions.go`

Modify `initRequestContext()`:
```go
if model.Internal {
    // Use internal provider
    provider := providers.NewProvider(model.InternalProvider, model.InternalAPIKey, model.InternalBaseURL)
    return p.handleInternalRequest(ctx, provider, req)
}
// Existing: forward to upstream
```

### 3.4 Internal Request Handler
**New File**: `pkg/proxy/internal_handler.go`

Functions:
- `handleInternalRequest()` - Route to appropriate provider
- `handleInternalStreaming()` - Handle streaming with proper SSE format
- `transformRequest()` - Convert internal format to provider-specific format
- `transformResponse()` - Convert provider response back to OpenAI format

---

## Phase 4: Frontend Changes

### 4.1 Model Config Form
**Files**: Frontend bundle (external React/Vue app)

Add to model configuration UI:
- Checkbox: "Use Internal Upstream"
- Dropdown: Provider selection (when internal checked)
- Input: API Key (masked)
- Input: Custom Base URL (optional)
- Input: Model Name (actual model ID for provider, e.g., `GLM-5.0`)

### 4.2 Token Management UI
Add page/section:
- Token list view (name, created, expires)
- Create token form (name, optional expiry)
- Copy token on creation (show once - important!)
- Revoke button

---

## Phase 5: Configuration & Security

### 5.1 Encryption
**New File**: `pkg/crypto/encryption.go`

- Encrypt API keys before storage
- Decrypt on demand
- Use environment variable for encryption key: `INTERNAL_ENCRYPTION_KEY`
- **Fail fast at startup** if key not set (no fallback to plaintext)
- Store key version with encrypted value for future rotation

**Migration update**: Add `internal_key_version INTEGER DEFAULT 1` to track encryption key version.

### 5.2 Config Validation
**File**: `pkg/models/config.go`

Add validation:
- If `internal: true`, require `internal_provider`, `internal_api_key`, and `internal_model`
- Validate provider name against allowed list
- Validate base URL format if provided
- `internal_model` cannot be empty when internal is true

---

## Implementation Status

### ✅ Completed

| Stage | Component | Files |
|-------|-----------|-------|
| 1 | Database migrations (internal + auth_tokens) | `pkg/store/database/migrate.go`, `migrations/*.sql` |
| 1 | ModelConfig struct with internal fields | `pkg/models/config.go` |
| 1 | Token generation/validation | `pkg/auth/token.go`, `pkg/auth/store.go` |
| 1 | Encryption module (AES-256-GCM) | `pkg/crypto/encryption.go` |
| 2 | Provider interface + factory | `pkg/providers/interface.go`, `factory.go` |
| 2 | OpenAI provider (streaming + non-streaming) | `pkg/providers/openai.go` |
| 3 | Internal handler routing | `pkg/proxy/internal_handler.go` |
| 3 | Proxy integration (check `internal` flag) | `pkg/proxy/handler_functions.go` |
| 3 | Token API endpoints | `pkg/ui/server.go` |
| 3 | API exposes internal fields | `pkg/ui/server.go` (Model struct) |
| 3 | Encryption at rest for API keys | `pkg/store/database/store.go` |
| 3 | Encryption init at startup | `cmd/main.go` |
| Test | Unit tests for auth, crypto, providers | `*_test.go` files |

### 📋 Pending

| Stage | Component | Notes |
|-------|-----------|-------|
| 4 | Frontend: Model config UI updates | Pre-built bundle, needs source access |
| 4 | Frontend: Token management UI | Pre-built bundle, needs source access |

---

## Implementation Order (MVP)

### Stage 1: Core Infrastructure ✅ COMPLETE
1. ✅ Database migration for internal fields
2. ✅ Update ModelConfig struct
3. ✅ Basic token generation/validation
4. ✅ Auth tokens table migration

### Stage 2: OpenAI Provider ✅ COMPLETE
1. ✅ Provider interface
2. ✅ OpenAI provider implementation
3. ✅ Internal handler routing
4. ✅ Request/response transformation

### Stage 3: Integration ✅ COMPLETE
1. ✅ Update proxy handler to check `internal` flag
2. ✅ Streaming support
3. ✅ Error handling and retries
4. ✅ Token API endpoints
5. ✅ Encryption at rest for API keys
6. ✅ Encryption init at startup
7. ✅ API exposes internal model fields

### Stage 4: Frontend ⏳ PENDING
1. ⏳ Model config UI updates (requires frontend source)
2. ⏳ Token management UI (requires frontend source)
3. ⏳ Testing and polish

---

## API Changes Summary

### New Endpoints

| Method | Path | Description |
|--------|------|-------------|
| POST | `/fe/api/tokens` | Create API token |
| GET | `/fe/api/tokens` | List tokens |
| DELETE | `/fe/api/tokens/{id}` | Revoke token |

### Modified Endpoints

| Method | Path | Changes |
|--------|------|---------|
| POST | `/fe/api/models` | Accept `internal`, `internal_provider`, `internal_api_key`, `internal_base_url`, `internal_model` |
| PUT | `/fe/api/models/{id}` | Same as above |
| GET | `/fe/api/models` | Return new fields |

---

## Database Schema Changes

### Model Mapping Example

| id (alias) | internal | internal_provider | internal_model | Request sends |
|------------|----------|-------------------|----------------|---------------|
| glm-5 | 1 | zhipu | GLM-5.0 | `{"model": "GLM-5.0", ...}` |
| gpt-4o | 1 | openai | gpt-4o-2024-08-06 | `{"model": "gpt-4o-2024-08-06", ...}` |
| claude-3 | 0 | - | - | (uses upstream) |

### models table (new columns)
| Column | Type | Default | Description |
|--------|------|---------|-------------|
| internal | INTEGER | 0 | 1 = use internal upstream |
| internal_provider | TEXT | '' | Provider: openai, anthropic, gemini, zhipu |
| internal_api_key | TEXT | '' | Encrypted API key |
| internal_base_url | TEXT | '' | Custom base URL |
| internal_model | TEXT | '' | Actual model name for provider |
| internal_key_version | INTEGER | 1 | Encryption key version (for rotation) |

### auth_tokens table (new)
| Column | Type | Description |
|--------|------|-------------|
| id | TEXT | Primary key |
| token_hash | TEXT | SHA-256 hash (unique) |
| name | TEXT | Human-readable name |
| expires_at | TEXT | ISO timestamp or NULL |
| created_at | TEXT | Creation timestamp |
| created_by | TEXT | OAuth user who created it |

**Note**: `last_used_at` skipped for MVP (adds hot-path overhead).

---

## Security Considerations

1. **API Key Storage**: Encrypt at rest using AES-256-GCM
2. **Token Display**: Show token only once on creation (cannot retrieve later)
3. **Token Hashing**: SHA-256 for storage (no need for bcrypt - tokens are random high-entropy)
4. **Token Format**: `sk-` prefix + 32 random chars (easy to identify)

---

## Testing Strategy

### Unit Tests ✅ COMPLETE
- ✅ Token generation/validation (`pkg/auth/token_test.go`)
- ✅ Provider implementations (`pkg/providers/*_test.go`)
- ✅ Request/response transformation (`pkg/proxy/internal_handler_test.go`)
- ✅ Encryption/decryption (`pkg/crypto/encryption_test.go`)

### Integration Tests
- ✅ Database migrations with internal fields
- ⏳ End-to-end request flow (internal vs upstream) - requires running server
- ⏳ Token authentication flow - requires running server
- ⏳ Fallback chain with mixed internal/upstream models

### Manual Testing
- ⏳ Create model with internal upstream
- ⏳ Verify direct provider calls
- ⏳ Compare response format with upstream path
- ⏳ Test streaming responses

---

## Risks & Mitigations

| Risk | Mitigation |
|------|------------|
| Breaking existing behavior | Feature flag, default `internal: false` |
| API key exposure | Encrypt at rest, mask in UI, audit logs |
| Provider API changes | Isolate provider logic, version interfaces |
| Token leakage | Show once, hash storage, short expiry option |

---

## Decisions Made

| Question | Decision |
|----------|----------|
| User identity | External OAuth2 handles this; API tokens are for programmatic access |
| Token scopes | Full access to all models (no granular scopes for MVP) |
| Key rotation | No - single API key per model, keep simple |
| Token expiry | Optional (no default, user chooses or leaves empty for no expiry) |

---

## Success Criteria (MVP)

### Backend ✅ COMPLETE
- [x] Can create model with `internal: true` via API
- [x] Model mapping works: `id` (alias) → `internal_model` (actual)
- [x] Can generate and validate API tokens
- [x] Requests to internal models go directly to AI provider
- [x] Streaming works for internal requests
- [x] Existing upstream behavior unchanged
- [x] API keys encrypted at rest
- [x] Encryption fails fast if key not configured
- [x] Unit tests pass for all new modules

### Frontend ⏳ PENDING
- [ ] Frontend can toggle internal option
- [ ] Token management UI

---

## Future Enhancements (Post-MVP)

- Additional providers (Anthropic, Gemini, Azure OpenAI, Bedrock, Vertex AI)
- Token usage analytics + `last_used_at` tracking
- Cost tracking per model/token
- Per-token rate limiting
- Encryption key rotation support

---

## Manual Testing Guide

### 1. Generate Encryption Key
```bash
# Generate a 32-byte key encoded as base64
openssl rand -base64 32
# Or run:
go run -e 'package main; import ("crypto/rand"; "encoding/base64"; "fmt"); func main() { b := make([]byte, 32); rand.Read(b); fmt.Println(base64.StdEncoding.EncodeToString(b)) }'
```

### 2. Start Server
```bash
export INTERNAL_ENCRYPTION_KEY="<your-base64-key>"
go run ./cmd/main.go
```

### 3. Create Internal Model
```bash
curl -X POST http://localhost:4321/fe/api/models \
  -H "Content-Type: application/json" \
  -d '{
    "id": "gpt-4o-internal",
    "name": "GPT-4o (Direct)",
    "enabled": true,
    "internal": true,
    "internal_provider": "openai",
    "internal_api_key": "sk-your-openai-key",
    "internal_model": "gpt-4o"
  }'
```

### 4. Test Request
```bash
curl -X POST http://localhost:4321/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o-internal",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

### 5. Create API Token
```bash
curl -X POST http://localhost:4321/fe/api/tokens \
  -H "Content-Type: application/json" \
  -d '{"name": "test-token"}'
# Save the returned token - it's shown only once!
```

### 6. List Tokens
```bash
curl http://localhost:4321/fe/api/tokens
```

---

## Architectural Review Notes

Incorporated from oracle review:

| Recommendation | Status |
|----------------|--------|
| Add `IsRetryable(error) bool` to Provider interface | ✅ Done |
| Fail fast if encryption key missing | ✅ Done |
| Add key version column for future rotation | ✅ Done |
| Create down migration | ✅ Done |
| Add JSON tags with `omitempty` | ✅ Done |
| Skip `last_used_at` for MVP | ✅ Done |
| Define normalized `StreamEvent` type | ✅ Done |
| Encrypt API keys before database storage | ✅ Done |
| Expose internal fields in API Model struct | ✅ Done |
| Use `IsRetryable()` for internal errors | ✅ Done |
| Fix `finish_reason` handling in streaming | ✅ Done |
| Remove dead code `copyStreamToResponse` | ✅ Done |
| Create `UpstreamClient` interface | 📋 Skipped (unnecessary complexity for MVP) |

---

## Files Created/Modified

### New Files
| File | Purpose |
|------|---------|
| `pkg/auth/token.go` | Token generation, hashing, validation |
| `pkg/auth/store.go` | Token CRUD operations |
| `pkg/crypto/encryption.go` | AES-256-GCM encryption |
| `pkg/providers/interface.go` | Provider interface + types |
| `pkg/providers/openai.go` | OpenAI-compatible provider |
| `pkg/providers/factory.go` | Provider factory |
| `pkg/proxy/internal_handler.go` | Internal request handling |
| `pkg/store/database/migrations/002_internal_upstream.up.sql` | Migration |
| `pkg/store/database/migrations/002_internal_upstream.down.sql` | Rollback |
| `pkg/store/database/migrations/003_auth_tokens.up.sql` | Migration |
| `pkg/store/database/migrations/003_auth_tokens.down.sql` | Rollback |

### Modified Files
| File | Changes |
|------|---------|
| `cmd/main.go` | Encryption init, token store creation |
| `pkg/models/config.go` | Internal fields, `GetModel()` method, validation |
| `pkg/store/database/store.go` | Encrypt/decrypt API keys, internal fields in CRUD |
| `pkg/store/database/querybuilder.go` | SQL queries include internal fields |
| `pkg/store/database/migrate.go` | Migrations 003, 004 added |
| `pkg/proxy/handler_functions.go` | Internal routing, `IsRetryable` check |
| `pkg/ui/server.go` | Token API endpoints, internal fields in Model struct |

### Test Files
| File | Tests |
|------|-------|
| `pkg/auth/token_test.go` | 4 tests |
| `pkg/crypto/encryption_test.go` | 6 tests |
| `pkg/providers/factory_test.go` | 4 tests |
| `pkg/providers/openai_test.go` | 7 tests |
| `pkg/proxy/internal_handler_test.go` | 3 tests |
