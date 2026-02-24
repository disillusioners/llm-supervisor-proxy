# LLM Supervisor Proxy

A lightweight sidecar proxy designed to sit between your autonomous agents (e.g., OpenCode) and your LLM provider (e.g., LiteLLM, vLLM, Ollama). It detects "zombie" requests where the LLM stops generating tokens mid-stream and automatically retries them, ensuring your agents don't freeze indefinitely.

## 🚀 Features

-   **Heartbeat Monitoring**: Detects if the token stream hangs for more than `IDLE_TIMEOUT` (default: 60s).
-   **Multi-Strategy Auto-Retry**:
    -   **Idle Reset**: Retries when a stream hangs mid-generation.
    -   **Upstream Recovery**: Retries on 5xx errors or connectivity issues from the provider.
    -   **Generation Guard**: Ensures requests eventually finish within `MAX_GENERATION_TIME`.
-   **Loop Detection**: Detects when LLMs enter repetitive patterns (identical responses, similar content, repeated tool calls, circular action workflows, stagnating progress). Optionally interrupts the stream and retries with sanitized context.
-   **Model Fallback Chains**: Automatically switches to a fallback model if the primary model fails or hangs.
-   **Smart Resume**: When retrying after a hang, it appends the partial generation to the prompt and asks the LLM to "Continue exactly where you stopped", minimizing wasted compute and latency.
-   **Web UI Dashboard**: Real-time monitoring of requests, event logs, and configuration management.
-   **Streaming Passthrough**: Fully supports Server-Sent Events (SSE) for real-time token streaming.
    > ⚠️ **Note**: For streaming requests, retry only occurs before headers are sent (e.g., on 5xx errors). Once streaming begins, mid-stream failures send an SSE error event to the client instead of retrying.

## 🛠️ Installation

### Prerequisites
-   Go 1.20+
-   Node.js 18+ (required for building the frontend)

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
1.  **Environment Variables** (Highest)
2.  **Database Storage** (SQLite / PostgreSQL)
3.  **Defaults** (Lowest)

### Environment Variables

| Variable | Default | Description |
| :--- | :--- | :--- |
| `UPSTREAM_URL` | `http://localhost:4001` | The URL of your actual LLM provider. |
| `PORT` | `4321` | Port for the proxy to listen on. |
| `IDLE_TIMEOUT` | `60s` | Max time to wait between tokens before considering the stream hung. |
| `MAX_GENERATION_TIME` | `300s` | Hard limit for the entire request lifecycle. |
| `MAX_UPSTREAM_ERROR_RETRIES` | `1` | Retries for 5xx/network errors. |
| `MAX_IDLE_RETRIES` | `2` | Retries for hung streams. |
| `MAX_GENERATION_RETRIES` | `1` | Retries for time-limit exceeded. |
| `LOOP_DETECTION_ENABLED` | `true` | Enable loop detection. |
| `LOOP_DETECTION_SHADOW_MODE` | `true` | Shadow mode (log only, no interruption). |
| `DATABASE_URL` | *(empty)* | PostgreSQL connection string (e.g. `postgres://user:pass@host/db`). If unset, uses SQLite. |

### Database Storage

The application uses a database for persisting configurations and fallback models:

*   **Local Development (SQLite)**: Used automatically by default. The database is created at `~/.config/llm-supervisor-proxy/config.db`.
*   **Production (PostgreSQL)**: Enabled by setting the `DATABASE_URL` environment variable.

*Note: If you are upgrading from an older version, your existing `config.json` and `models.json` files will be automatically migrated to the database.*

For full database details and rollback procedures, see [`docs/database-migration.md`](docs/database-migration.md).

## 🏃 Usage

1.  **Start your LLM Provider** (e.g., LiteLLM) on port 4001.
2.  **Start the Supervisor Proxy**:
    ```bash
    llm-supervisor-proxy
    ```
3.  **Point your Agent** to the Proxy (port 4321):
    ```bash
    curl -X POST http://localhost:4321/v1/chat/completions \
      -H "Content-Type: application/json" \
      -d '{
        "model": "gpt-4",
        "messages": [{"role": "user", "content": "Write a long story about a space pirate."}],
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
│   ├── ui/
│   │   ├── server.go        # UI server + API handlers
│   │   ├── static/          # Built frontend (embedded)
│   │   └── frontend/        # Preact frontend source
│   ├── proxy/               # Core proxy logic & retry handling
│   ├── loopdetection/       # Loop detection strategies & recovery
│   ├── events/              # Event bus for SSE updates
│   ├── models/              # Model & fallback configuration
│   ├── store/               # In-memory storage & SQLite/PostgreSQL Database
│   │   ├── database/        # DB Connection, migrations, sqlc queries
│   └── config/              # App-wide configuration management
├── docs/                    # Design docs & implementation details
└── LICENSE                  # MIT License
```

## ⚖️ License

This project is licensed under the **MIT License**. See the [LICENSE](LICENSE) file for details.
