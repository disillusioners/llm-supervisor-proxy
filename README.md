# LLM Supervisor Proxy

A lightweight sidecar proxy designed to sit between your autonomous agents (e.g., OpenCode) and your LLM provider (e.g., LiteLLM, vLLM, Ollama). It detects "zombie" requests where the LLM stops generating tokens mid-stream and automatically retries them, ensuring your agents don't freeze indefinitely.

## 🚀 Features

-   **Heartbeat Monitoring**: Detects if the token stream hangs for more than `IDLE_TIMEOUT` (default: 10s).
-   **Auto-Retry**: Automatically retries failed or hung requests.
-   **Smart Resume**: When retrying, it appends the partial generation to the prompt and asks the LLM to "Continue exactly where you stopped", minimizing wasted compute and latency.
-   **Hard Deadline**: Enforces a global `MAX_GENERATION_TIME` to prevent infinite loops (default: 180s).
-   **Streaming Passthrough**: Fully supports Server-Sent Events (SSE) for real-time token streaming.

## 🛠️ Installation

### Prerequisites
-   Go 1.20+
-   Node.js 18+ (for frontend development only)

### Build & Install

```bash
git clone https://github.com/disillusioners/llm-supervisor-proxy.git
cd llm-supervisor-proxy

# Build both frontend and backend
make

# Install globally to your system path (optional)
sudo make install
```

### Makefile Targets
- `make`: Build both frontend and backend.
- `make build`: Build only the Go backend.
- `make build-frontend`: Build only the frontend UI.
- `make install`: Install the binary to `/usr/local/bin` (requires sudo).
- `make clean`: Remove build artifacts.
- `make test`: Run Go tests.

## ⚙️ Configuration

The proxy is configured entirely via environment variables:

| Variable | Default | Description |
| :--- | :--- | :--- |
| `UPSTREAM_URL` | `http://localhost:4000` | The URL of your actual LLM provider (e.g., LiteLLM). |
| `PORT` | `8080` | Port for the proxy to listen on. |
| `IDLE_TIMEOUT` | `10s` | Max time to wait between tokens before considering the stream hung. |
| `MAX_GENERATION_TIME` | `180s` | Hard limit for the entire request (including retries). |
| `MAX_RETRIES` | `1` | Number of times to retry a failed/hung request. |

## 🏃 Usage

1.  **Start your LLM Provider** (e.g., LiteLLM) on port 4000.
2.  **Start the Supervisor Proxy**:
    ```bash
    export UPSTREAM_URL="http://localhost:4000"
    llm-supervisor-proxy
    ```
3.  **Point your Agent/Client** to the Proxy (port 8080):
    ```bash
    curl -X POST http://localhost:8080/v1/chat/completions \
      -H "Content-Type: application/json" \
      -d '{
        "model": "gpt-3.5-turbo",
        "messages": [{"role": "user", "content": "Hello!"}]
      }'
    ```

## 🧠 How it Works

1.  **Interception**: The proxy accepts standard OpenAI-compatible `/v1/chat/completions` requests.
2.  **Forwarding**: It forwards the request to the `UPSTREAM_URL`.
3.  **Monitoring**: It monitors the response stream. If no token is received for `IDLE_TIMEOUT`, it kills the upstream connection.
4.  **Recovery**:
    -   It takes the tokens generated *before* the hang.
    -   It appends them to the messages with an `assistant` role.
    -   It adds a system/user instruction to "Continue exactly where you stopped".
    -   It initiates a new request to the upstream.
5.  **Streaming**: The client receives a continuous stream of tokens, unaware of the interruption and recovery (mostly).

## 🖥️ Web UI

The proxy includes a built-in monitoring dashboard built with **Preact + Vite + Tailwind CSS** (~16KB gzipped).

### Features
- **Real-time Request Monitoring**: View all requests as they flow through the proxy
- **Live Event Stream**: Server-Sent Events (SSE) for instant updates
- **Request Details**: Inspect messages, tool calls, and thinking process
- **Configuration Management**: Adjust proxy settings and model fallback chains via UI

### Access
Once the proxy is running, open http://localhost:8080 in your browser.

### Frontend Development

```bash
# Install dependencies
cd pkg/ui/frontend && npm install

# Development server (hot reload + API proxy to :8080)
npm run dev

# Production build (outputs to pkg/ui/static/)
npm run build
```

The frontend is embedded in the Go binary via `//go:embed static/*`, so no separate deployment needed.

## 🧪 Verification

To verify the proxy's resilience against hangs, you can use the included mock server:

```bash
# Terminal 1: Run the verification script
./verify.sh
```

This script builds a custom mock server that intentionally hangs on specific tokens, starts the proxy, and asserts that the final output is complete despite the hang.

## 📁 Project Structure

```
.
├── cmd/main.go              # Entry point
├── pkg/
│   ├── ui/
│   │   ├── server.go        # UI server + API handlers
│   │   ├── static/          # Built frontend (embedded)
│   │   └── frontend/        # Preact frontend source
│   ├── proxy/               # Core proxy logic
│   ├── events/              # Event bus for SSE
│   ├── models/              # Model configuration
│   └── store/               # Request storage
└── config/                  # Configuration files
```

## 🔧 Tech Stack

- **Backend**: Go 1.20+
- **Frontend**: Preact, Vite, Tailwind CSS, TypeScript
- **Real-time**: Server-Sent Events (SSE)
