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

### Build
```bash
git clone https://github.com/disillusioners/llm-supervisor-proxy.git
cd llm-supervisor-proxy
go build -o supervisor-proxy cmd/main.go
```

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
    ./supervisor-proxy
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

## 🧪 Verification

To verify the proxy's resilience against hangs, you can use the included mock server:

```bash
# Terminal 1: Run the verification script
./verify.sh
```

This script builds a custom mock server that intentionally hangs on specific tokens, starts the proxy, and asserts that the final output is complete despite the hang.
