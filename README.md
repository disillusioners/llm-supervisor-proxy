# LLM Supervisor Proxy

A production-ready OpenAI-compatible proxy server that sits between your autonomous agents and LLM providers. It detects "zombie" requests where the LLM stops generating tokens mid-stream and automatically retries them, ensuring your agents don't freeze indefinitely.

**Supports multiple LLM providers**: OpenAI, Anthropic, Azure OpenAI, Google Gemini, Zhipu/GLM, MiniMax, and ZAI.

## 🚀 Features

### Core Reliability
- **Heartbeat Monitoring**: Detects if the token stream hangs for more than `IDLE_TIMEOUT` (default: 60s).
- **Parallel Race Retry**: When a request hangs or fails, spawns parallel requests (same model + fallback) and races them to completion. First successful response wins.
- **Smart Resume**: When retrying after a hang, it appends the partial generation to the prompt and asks the LLM to "Continue exactly where you stopped", minimizing wasted compute and latency.
- **Streaming Passthrough**: Fully supports Server-Sent Events (SSE) for real-time token streaming.
- **Stream Normalizers**: Automatically fixes common streaming issues like missing role fields, missing tool call indices, and concatenated chunks.

### Loop Detection & Recovery
- **Loop Detection**: Detects when LLMs enter repetitive patterns (identical responses, similar content, repeated tool calls, circular action workflows, stagnating progress). Optionally interrupts the stream and retries with sanitized context.
- **6 Detection Strategies**: Exact match, SimHash similarity, action patterns, oscillation detection, trigram analysis, and stagnation detection.

### Multi-Provider Support
- **Multiple LLM Providers**: Works with OpenAI, Anthropic, Azure OpenAI, Google Gemini, Zhipu/GLM, MiniMax, and ZAI out of the box.
- **Model Fallback Chains**: Automatically switches to a fallback model if the primary model fails or hangs.
- **Credential Management**: Store encrypted API keys and configure per-model credentials. Supports environment variable expansion (`${API_KEY}`) for secure secret management.

### Tool Call Handling
- **Automatic Tool Call Repair**: Repairs malformed JSON in LLM tool call arguments using multiple strategies (extraction, library repair, reasoning removal, or LLM-based fixing).
- **Tool Call Buffering**: Buffers tool call fragments until valid JSON is formed. Designed for streaming clients that cannot handle partial JSON.

### Authentication & Security
- **API Token Authentication**: Token-based authentication with `sk-` prefix tokens, expiration dates, and secure SHA-256 hashing.

### Raw Response Logging
- **Upstream Response Logging**: Save raw upstream responses to disk for debugging and auditing.
- **Configurable Storage**: Set directory and max storage limits for buffer files.

### Deployment & Monitoring
- **Web UI Dashboard**: Real-time monitoring of requests, event logs, and configuration management.
- **Kubernetes Ready**: Helm chart with OAuth2 proxy integration, PostgreSQL support, and long-running request handling (3600s timeout for streaming).

## 🛠️ Installation

### Prerequisites
- Go 1.24+
- Node.js 18+ (required for building the frontend)

### Build & Install

```bash
git clone https://github.com/disillusioners/llm-supervisor-proxy.git
cd llm-supervisor-proxy

# Build both frontend and backend
make

# Install globally to your system path
sudo make install
```

### Makefile Targets
- `make`: Build both frontend and backend.
- `make build`: Build only the Go backend.
- `make build-frontend`: Build only the frontend UI.
- `make install`: Install the binary to `/usr/local/bin`.
- `make uninstall`: Remove the binary from the system.
- `make clean`: Remove build artifacts.
- `make test`: Run Go tests.

## ⚙️ Configuration

The proxy uses a three-tier configuration system with the following precedence:
1. **Environment Variables** (Highest, when `APPLY_ENV_OVERRIDES=true`)
2. **Database Storage** (SQLite / PostgreSQL)
3. **Defaults** (Lowest)

### Core Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `UPSTREAM_URL` | `http://localhost:4001` | The URL of your actual LLM provider. |
| `UPSTREAM_PROTOCOL` | *(auto-detect)* | Upstream protocol: `anthropic`, `openai`, or empty for auto-detect (URL heuristic). |
| `UPSTREAM_CREDENTIAL_ID` | *(empty)* | ID of stored credential to use for upstream authentication. |
| `PORT` | `4321` | Port for the proxy to listen on. |
| `IDLE_TIMEOUT` | `60s` | Max time to wait between tokens before spawning parallel requests. |
| `STREAM_DEADLINE` | `110s` | Time limit before picking best buffer and continuing streaming. |
| `MAX_GENERATION_TIME` | `300s` | **Absolute hard timeout** for entire request lifecycle. |
| `SSE_HEARTBEAT_ENABLED` | `false` | Enable SSE heartbeat for streaming responses (keeps connections alive during buffering). |
| `DATABASE_URL` | *(empty)* | PostgreSQL connection string (e.g. `postgres://user:pass@host/db`). If unset, uses SQLite. |
| `INTERNAL_ENCRYPTION_KEY` | *(empty)* | Base64-encoded 32-byte key for encrypting stored API keys. |

### Race Retry Configuration

The proxy uses a **parallel race retry** mechanism for maximum reliability:

| Variable | Default | Description |
|----------|---------|-------------|
| `RACE_RETRY_ENABLED` | `false` | Enable parallel race retry (recommended: `true`). |
| `RACE_PARALLEL_ON_IDLE` | `true` | Spawn parallel requests when main request hits idle timeout. |
| `RACE_MAX_PARALLEL` | `3` | Max parallel requests (main + second + fallback). |
| `RACE_MAX_BUFFER_BYTES` | `5242880` | Max bytes per request buffer (5MB default). |

**How Race Retry Works:**
1. Main request starts immediately with the original model
2. If main request hits idle timeout or fails, parallel requests spawn:
   - **Second request**: Same model, fresh attempt
   - **Fallback request**: First fallback model in chain
3. First request to complete successfully **wins** the race
4. Other requests are cancelled immediately
5. Winner's buffered response streams to client

**Benefits over sequential retry:**
- Faster response (don't wait for stuck requests to timeout)
- Better success rate (multiple attempts running simultaneously)
- Preserved progress (main request continues even after idle timeout)

### Buffer Storage Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `BUFFER_STORAGE_DIR` | *(empty)* | Directory for buffer content files (defaults to user config dir). |
| `BUFFER_MAX_STORAGE_MB` | `100` | Max total storage for buffers in MB. |

### Tool Call Buffer Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `TOOL_CALL_BUFFER_DISABLED` | `false` | Disable tool call buffering (for clients that handle partial JSON). |
| `TOOL_CALL_BUFFER_MAX_SIZE` | `1048576` | Max bytes per tool call buffer (1MB default). |

### Raw Upstream Response Logging

| Variable | Default | Description |
|----------|---------|-------------|
| `LOG_RAW_UPSTREAM_RESPONSE` | `false` | Log successful upstream responses. |
| `LOG_RAW_UPSTREAM_ON_ERROR` | `false` | Log failed/error upstream responses. |
| `LOG_RAW_UPSTREAM_MAX_KB` | `1024` | Max KB per response to log. |

### Ultimate Model Configuration

For handling duplicate requests with a designated high-priority model:

| Variable | Default | Description |
|----------|---------|-------------|
| `ULTIMATE_MODEL_ID` | *(empty)* | Model ID for duplicate request handling. |
| `ULTIMATE_MODEL_MAX_HASH` | `100` | Max hashes in circular buffer. |
| `ULTIMATE_MODEL_MAX_RETRIES` | `2` | Max ultimate model retries per hash. |

### Loop Detection Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `LOOP_DETECTION_ENABLED` | `true` | Enable loop detection. |
| `LOOP_DETECTION_SHADOW_MODE` | `true` | Shadow mode (log only, no interruption). |

Advanced loop detection parameters (via Web UI or database config):

| Parameter | Default | Description |
|-----------|---------|-------------|
| `MessageWindow` | `10` | Sliding window size for message analysis. |
| `ActionWindow` | `15` | Action window size for tool call analysis. |
| `ExactMatchCount` | `3` | Identical messages to trigger detection. |
| `SimilarityThreshold` | `0.85` | SimHash similarity threshold (0.0-1.0). |
| `MinTokensForSimHash` | `15` | Min tokens before SimHash applies. |
| `ActionRepeatCount` | `3` | Consecutive identical actions to trigger. |
| `OscillationCount` | `4` | A→B→A→B cycles to trigger. |
| `ThinkingMinTokens` | `100` | Min thinking tokens before trigram analysis. |
| `TrigramThreshold` | `0.3` | Trigram repetition ratio threshold. |
| `ReasoningModelPatterns` | `["o1", "o3", "deepseek-r1"]` | Patterns for reasoning models. |
| `ReasoningTrigramThreshold` | `0.15` | More forgiving threshold for reasoning models. |

### Tool Repair Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `TOOL_REPAIR_ENABLED` | `true` | Enable tool repair. |
| `TOOL_REPAIR_STRATEGIES` | *(multi)* | Repair strategies to use. |
| `TOOL_REPAIR_MAX_ARGUMENTS_SIZE` | `100KB` | Max tool arguments size. |
| `TOOL_REPAIR_MAX_TOOL_CALLS` | `10` | Max tool calls per response. |
| `TOOL_REPAIR_FIXER_MODEL` | *(empty)* | LLM model for fixing (optional). |
| `TOOL_REPAIR_FIXER_TIMEOUT` | `25s` | Fixer request timeout. |

### Database Storage

The application uses a database for persisting configurations and fallback models:

- **Local Development (SQLite)**: Used automatically by default. The database is created at `~/.config/llm-supervisor-proxy/config.db`.
- **Production (PostgreSQL)**: Enabled by setting the `DATABASE_URL` environment variable.

*Note: If you are upgrading from an older version, your existing `config.json` and `models.json` files will be automatically migrated to the database.*

For full database details and rollback procedures, see [`docs/database-migration.md`](docs/database-migration.md).

## 🏃 Usage

1. **Start your LLM Provider** (e.g., LiteLLM) on port 4001.
2. **Start the Supervisor Proxy**:
   ```bash
   llm-supervisor-proxy
   ```
3. **Point your Agent** to the Proxy (port 4321):

   **OpenAI-compatible clients:**
   ```bash
   curl -X POST http://localhost:4321/v1/chat/completions \
     -H "Content-Type: application/json" \
     -d '{
       "model": "gpt-4",
       "messages": [{"role": "user", "content": "Write a long story about a space pirate."}],
       "stream": true
     }'
   ```

   **Anthropic-compatible clients (Claude Code):**
   ```bash
   curl -X POST http://localhost:4321/v1/messages \
     -H "x-api-key: <your-key>" \
     -H "anthropic-version: 2023-06-01" \
     -H "Content-Type: application/json" \
     -d '{
       "model": "claude-sonnet-4-20250514",
       "max_tokens": 1024,
       "messages": [{"role": "user", "content": "Hello!"}],
       "stream": true
     }'
   ```

## 🖥️ Web UI

The proxy includes a built-in monitoring dashboard accessible at `http://localhost:4321`.

### Features
- **Per-Request Logging**: All events (retries, fallbacks, token counts) are grouped by request.
- **Inspect Payloads**: View full message history, tool calls, and model responses.
- **Live Configuration**: Change timeouts and retry limits on the fly without restarting.
- **Fallback Management**: Configure model-to-model fallback chains.
- **Loop Detection Config**: Tune detection thresholds and toggle shadow mode.
- **Token Management**: Create, list, and revoke API tokens.
- **Credential Management**: Store and manage encrypted API keys for direct provider access.

## 🔌 Multi-Provider Support

The proxy supports multiple LLM providers out of the box:

| Provider | Default Base URL | Protocol | Notes |
|----------|-----------------|----------|-------|
| **OpenAI** | `https://api.openai.com/v1` | OpenAI | Standard OpenAI API |
| **Anthropic** | `https://api.anthropic.com/v1` | Anthropic | Anthropic Messages API |
| **Azure OpenAI** | — | OpenAI | Requires `AZURE_API_KEY` and deployment URL |
| **Google Gemini** | — | OpenAI | Google AI Studio / Vertex AI |
| **Zhipu/GLM** | `https://open.bigmodel.cn/api/paas/v4` | OpenAI | OpenAI-compatible |
| **MiniMax** | `https://api.minimax.io/v1` | OpenAI | OpenAI-compatible |
| **ZAI** | `https://api.z.ai/api/coding/paas/v4` | OpenAI | OpenAI-compatible |

Configure per-model credentials and base URLs via the Web UI or database.

### Dual Client Protocol Support

The proxy accepts requests in both **OpenAI** and **Anthropic** formats:

| Client Protocol | Endpoint | Upstream Protocol |
|----------------|----------|-------------------|
| OpenAI (`/v1/chat/completions`) | ChatGPT, LangChain, LiteLLM | Any OpenAI-compatible provider |
| Anthropic (`/v1/messages`) | Claude Code, Anthropic SDK | Auto-detected or configured |

### Upstream Protocol Detection

When clients send Anthropic-format requests via `/v1/messages`, the proxy needs to know what protocol the upstream speaks:

1. **Explicit config** — Set `UPSTREAM_PROTOCOL=anthropic` or `UPSTREAM_PROTOCOL=openai` (most reliable)
2. **Environment variable** — `UPSTREAM_PROTOCOL` env var
3. **Auto-detection** — If upstream URL path contains "anthropic", uses passthrough mode

| Upstream | Protocol Mode | What Happens |
|----------|--------------|--------------|
| Anthropic API or Anthropic-compatible | `anthropic` | **Passthrough** — request forwarded as-is, no translation |
| OpenAI or OpenAI-compatible | `openai` or auto | **Translation** — Anthropic→OpenAI on request, OpenAI→Anthropic on response |

### Claude Code Setup

Point Claude Code at the proxy:

```bash
export ANTHROPIC_BASE_URL=http://localhost:4321
export ANTHROPIC_AUTH_TOKEN=<your-api-key>
claude "hello world"
```

Or in Claude Code settings (`~/.claude/settings.json`):

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "http://localhost:4321",
    "ANTHROPIC_AUTH_TOKEN": "<your-api-key>"
  }
}
```

If your upstream is Anthropic-compatible, also set:

```bash
UPSTREAM_PROTOCOL=anthropic UPSTREAM_URL=https://your-anthropic-provider.com llm-supervisor-proxy
```

## 🔐 API Token Authentication

Enable token-based authentication for your proxy:

```bash
# Create a token (via API)
curl -X POST http://localhost:4321/api/tokens \
  -H "Content-Type: application/json" \
  -d '{"name": "my-agent", "expires_at": "2025-12-31T23:59:59Z"}'

# Use the token
curl http://localhost:4321/v1/chat/completions \
  -H "Authorization: Bearer sk_xxx..." \
  -H "Content-Type: application/json" \
  -d '{"model": "gpt-4", "messages": [...]}'
```

Tokens use the `sk-` prefix with 64 hex characters and support optional expiration dates.

## 🔧 Tool Call Repair

When LLMs output malformed JSON in tool call arguments, the proxy can automatically repair them:

| Strategy | Description |
|----------|-------------|
| `extract_json` | Extracts valid JSON from mixed content |
| `library_repair` | Uses jsonrepair library for common issues |
| `remove_reasoning` | Strips reasoning patterns (e.g., "Let me...", "Summary:") |
| `fixer_model` | Uses a separate LLM to repair malformed JSON |

Configure via the Web UI under Tool Repair settings.

## 🛡️ Tool Call Buffering

For streaming clients that cannot handle partial JSON in tool call arguments:

- **Enabled by default** - Buffers fragments until valid JSON is formed
- **Emits complete tool calls** - Only when arguments are fully parsed
- **Configurable max size** - Default 1MB per request

Disable if your client handles partial JSON:
```bash
export TOOL_CALL_BUFFER_DISABLED=true
```

## 📊 Stream Normalizers

Automatically fixes common streaming issues:

| Normalizer | Fixes |
|------------|-------|
| `FixEmptyRoleNormalizer` | Missing role field in streaming chunks |
| `FixMissingToolCallIndexNormalizer` | Missing tool call indices |
| `SplitConcatenatedChunksNormalizer` | Merged SSE chunks |

## 🔑 Credential Management

Store encrypted API keys and call providers directly (bypassing LiteLLM):

1. **Set an encryption key**:
   ```bash
   export INTERNAL_ENCRYPTION_KEY=$(openssl rand -base64 32)
   ```

2. **Create a credential** (via Web UI or API):
   ```json
   {
     "name": "openai-prod",
     "api_key": "${OPENAI_API_KEY}",
     "base_url": "https://api.openai.com/v1"
   }
   ```

3. **Use in model config**:
   ```json
   {
     "model": "gpt-4",
     "credential_id": "cred_xxx"
   }
   ```

Supports environment variable expansion: `${VAR}` or `${VAR:-default}`.

## ☸️ Kubernetes Deployment

Deploy to Kubernetes using the included Helm chart:

```bash
# Install with Helm
helm install llm-supervisor-proxy ./k8s

# With OAuth2 proxy for authentication
helm install llm-supervisor-proxy ./k8s \
  --set oauth2Proxy.enabled=true \
  --set postgresql.enabled=true
```

Features:
- **OAuth2 Proxy integration** for OIDC authentication
- **PostgreSQL support** for production workloads
- **Long-running request support** (3600s timeout for streaming)
- **Secret management** for database and OAuth credentials

## 🔄 Loop Detection

The proxy monitors LLM responses for repetitive patterns using 6 heuristic strategies (no additional LLM required):

| Strategy | Detects |
|----------|---------|
| **Exact Match** | Identical consecutive messages |
| **Similarity** | Near-identical messages via SimHash fingerprints |
| **Action Pattern** | Repeated tool calls or A↔B oscillations |
| **Cycle** | Circular action workflows (A→B→C→A→B→C) |
| **Thinking** | Repetitive reasoning patterns via trigram analysis |
| **Stagnation** | No meaningful progress despite continued output |

### Shadow Mode (Default)

By default, loop detection runs in **shadow mode** — loops are logged but the stream is not interrupted. This lets you observe and tune thresholds before enabling active intervention.

### Active Interruption

When `shadow_mode` is `false`, critical-severity loops will:
1. Stop the streaming response
2. Sanitize the context window (remove repetitive messages)
3. Inject a strategy-specific system prompt to break the loop
4. Retry with sanitized context (or fallback to the next model)

For full details, see [`docs/loop-detection-implementation.md`](docs/loop-detection-implementation.md).

## 📁 Project Structure

```
.
├── cmd/main.go              # Entry point
├── pkg/
│   ├── auth/                # API token authentication
│   ├── bufferstore/         # Persistent buffer storage for raw response logging
│   ├── config/              # Configuration management
│   ├── crypto/              # Encryption utilities
│   ├── events/              # Event bus for SSE updates
│   ├── handlers/            # HTTP handlers
│   ├── internal/            # Internal implementation details
│   ├── logger/               # Logging utilities
│   ├── loopdetection/       # Loop detection strategies & recovery
│   ├── models/              # Model, fallback & credential management
│   ├── providers/           # LLM provider adapters (OpenAI, Anthropic)
│   ├── proxy/               # Core proxy logic & race retry
│   │   ├── normalizers/     # Stream normalization
│   │   ├── race_coordinator.go  # Race retry coordinator
│   │   ├── race_executor.go      # Request execution
│   │   ├── race_request.go      # Request state
│   │   └── stream_buffer.go     # Thread-safe buffer
│   ├── store/               # In-memory storage & SQLite/PostgreSQL Database
│   │   └── database/        # DB Connection, migrations, sqlc queries
│   │       └── sqlc/        # sqlc query definitions
│   ├── supervisor/          # Idle timeout monitoring
│   ├── toolcall/            # Tool call buffering
│   ├── toolrepair/          # Automatic tool call JSON repair
│   ├── ultimatemodel/       # Ultimate model for duplicate requests
│   └── ui/                  # Web UI
│       └── frontend/        # Preact frontend source
├── k8s/                     # Kubernetes Helm chart & manifests
│   ├── templates/           # Helm templates
│   └── values.yaml          # Default values
├── docs/                    # Design docs & implementation details
├── plans/                   # Design proposals & reviews
└── LICENSE                  # MIT License
```

## ⚖️ License

This project is licensed under the **MIT License**. See the [LICENSE](LICENSE) file for details.
