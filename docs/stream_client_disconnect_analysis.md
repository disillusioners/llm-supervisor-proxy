# Stream Client Disconnect Analysis

This document outlines the investigation into why some clients drop their connection when the LLM Supervisor Proxy attempts a stream retry or model fallback during an interrupted Server-Sent Events (SSE) stream.

## Background Context
Initially, logs indicated `Client disconnected` occurring at the exact same time as `Retrying request (attempt 1)...`. It was found out that the client drop often happens **before** the retry begins, not because of the retry mechanism itself. However, even when trying to handle fallbacks seamlessly, strict HTTP clients drop the connection while more forgiving clients survive.

## The Core Issues

Based on a deeper dive into how the `llm-supervisor-proxy` manages the stream data, there are **three distinct root causes** as to why clients drop connections instead of transparently using the fallback stream.

### 1. The "Incomplete JSON Chunk" Vulnerability
When LiteLLM or an upstream server crashes/disconnects mid-generation, the TCP socket is closed abruptly. The proxy reads the stream using `bufio.Scanner`. When it hits the abrupt EOF, the scanner yields whatever partial bytes it had in its buffer as the "final line".

Because the proxy currently forwards `v` (via `w.Write(line)`) before validating it, an incomplete, corrupted chunk is given to the client:
```text
data: {"id": "123", "choices": [{"delta": {"con        <-- (Upstream dies here)
: retrying-attempt-1
data: {"id": "fallback", "choices": ...}
```
* **Forgiving Clients** (like custom JS using `try-catch` with `JSON.parse`) ignore the mangled `{"con` line and continue reading to catch the fallback chunks.
* **Strict Clients** (like the official OpenAI SDK, Langchain frameworks) attempt to decode it, throw a `JSONDecodeError` or `APIError`, and actively **close and drop the TCP connection**.

### 2. Stream Metadata Mismatch (`id` and `role` changing mid-stream)
When a fallback model kicks in, it effectively creates a brand new generation on the upstream side. The new stream chunks forwarded by the proxy will usually contain a brand new message `id` and re-announce `"role": "assistant"`.
```text
... (End of interrupted stream)
data: {"id": "chatcmpl-ORIGINAL", "model": "gpt-4", "choices": [{"delta": {"content": "world"}}]}
... (Fallback starts)
data: {"id": "chatcmpl-FALLBACK_NEW", "model": "gpt-3.5", "choices": [{"delta": {"role": "assistant"}}]}
```
* **Forgiving Clients** look only at the `content` delta, ignoring everything else entirely.
* **Strict Clients** (most SDKs) enforce that the message `id` must not change mid-stream. They also expect the `"role": "assistant"` attribute to only be present in chunk `index: 0`. Encountering a sudden new `id` or repeated `role` mid-stream causes their parser to panic, prompting them to drop the connection.

### 3. Read Timeout During the Fallback "Silence Gap"
When `prepareRetry` runs, the proxy sends a single keep-alive comment (`: retrying-attempt-n\n`). It then executes `h.client.Do()` to ping the fallback model. If the fallback model has long inference times and requires 10–30 seconds to yield its first token (Time-To-First-Token), the proxy sends absolutely nothing across the wire during this waiting window.
* **Forgiving Clients** (such as the standard browser `fetch` API) usually have no enforced socket-level `read_timeout` and wait indefinitely.
* **Strict Clients** (Python's `requests` or `httpx` under the hood) use rigorous read timeouts (often `timeout=10` or `15` seconds). If they don't receive any bytes during the long pause while the fallback model is "thinking", they assume the connection is dead and close it.

## Proposed Strategy to Make Proxy Failovers 100% Transparent

To repair these issues without breaking client connections during stream failovers, the following blueprint should be implemented:

1. **Verify JSON before Writing:**
   In `handleStreamResponse`, intercept the line before calling `w.Write(line)`. If it is a `data: ` line, run `json.Valid()` on the payload. If the JSON is malformed/incomplete, drop the line instead of sending it down the wire.
2. **Normalize Stream Metadata:**
   The proxy must track the `id` from the very first chunk the user receives and cache it inside the `requestContext`. For all subsequent chunks (including those from retries and fallbacks), the proxy should rewrite the chunk's JSON `id` to match the cached original. Additionally, it should strip out the `"role": "assistant"` delta from fallback fallback chunks.
3. **Continuous Keep-Alive:**
   Instead of sending one single `: retrying` comment at the start of a retry, spin up a lightweight goroutine (`time.Ticker`) during the retry setup that blasts an SSE comment (e.g., `:\n`) down the stream every 3-5 seconds. This guarantees that strict HTTP clients never trip their idle read timeout during the fallback Time-To-First-Token delay.
