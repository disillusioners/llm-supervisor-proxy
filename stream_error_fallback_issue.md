# Streaming Error & Fallback Issue Analysis

## The Problem
A client received a `500` error wrapped inside a stream from LiteLLM: `"litellm.APIError: Error building chunks for logging/streaming usage calculation"`. Even though the proxy is configured with retry and model fallback logic, the fallback failed to trigger.

## Event Log Trace
```
[17:52:08] [REQUEST_STARTED] Processing new request...
[17:52:14] [UNEXPECTED_EOF] Stream ended unexpectedly without [DONE]
[17:52:14] [RETRY_ATTEMPT] Retrying request (Attempt 1)
```
*Note: The model did not fallback after Attempt 1.*

## Root Cause Analysis
This issue stems from a combination of how HTTP Server-Sent Events (SSE) streams work, and a specific logic flaw in how our proxy handles retries when the HTTP headers have *already* been sent to the client.

### 1. Why the error passed through to the caller
When a client requests a streamed response, the upstream server (LiteLLM) initially accepts the request and replies with an HTTP `200 OK` status, beginning the stream.
1. Our proxy sees the `200 OK` and immediately writes these headers to the client. This sets the internal flag: `rc.headersSent = true`.
2. As the proxy reads the stream chunk-by-chunk in `handleStreamResponse`, it utilizes a "pass-through" approach. Every line it reads is immediately written and flushed to the client:
   ```go
   line := scanner.Bytes()
   w.Write(line) // Flushed instantly to the client
   w.Write([]byte("\n"))
   f.Flush()
   ```
3. Midway through the stream, LiteLLM crashed with the `litellm.APIError`. Instead of sending the expected `data: [DONE]` signal, LiteLLM dumped the raw JSON error string directly into the active connection and dropped the stream.
4. Because our proxy is blindly passing through data line-by-line, that raw error text is immediately flushed to the caller. The caller sees the error text, breaks, and throws an error on their end.

### 2. Why retry occurred but the model did NOT fallback
Right after LiteLLM abruptly closed the stream, the proxy realized it never received the `[DONE]` signal.
1. It accurately logged `[UNEXPECTED_EOF] Stream ended unexpectedly without [DONE]` and launched a retry: `[RETRY_ATTEMPT] Retrying request (Attempt 1)`.
2. However, when Attempt 1 ran, LiteLLM was likely fully broken and responded with a hard HTTP `500 Internal Server Error` status code (instead of starting another `200 OK` stream).
3. The proxy sees the 500 status and calls `handleNonOKStatus`. Inside this function, we have the following condition:
   ```go
   if !rc.headersSent {
       // ... retry logic and fallback logic resides here ...
   }

   // Headers already sent, can't send a different status
   resp.Body.Close()
   return attemptReturnImmediately
   ```
4. **The Catch-22 Logic Flaw:** Because the *very first* attempt successfully sent the `200 OK` headers to the client before failing, `rc.headersSent` is still `true`.
5. The function immediately returns `attemptReturnImmediately`. The parent function (`attemptModel`) sees this result and **returns `true`**.
6. Because `attemptModel` returns `true`, the main router loop in `HandleChatCompletions` thinks the model request was handled completely/successfully and `return`s outright. **It completely skips the fallback loop!**

## Visual Flow
![Proxy Stream Error Flow](proxy_stream_error_flow_1771783300473.png)

## Next Steps for Fixing
To fix this, we need to carefully reconsider how we handle stream interrupts:
1.  **Filter/Buffer Error Chunks:** If the chunk is an error JSON and not a valid SSE message (e.g., doesn't start with `data:`), we shouldn't blindly flush it to the client. We should parse it and trigger a retry/fallback internally.
2.  **Rethink the `rc.headersSent` Trap:** If the stream has broken midway, we cannot transparently fallback in the middle of a stream to another model *and append to the HTTP stream* as if nothing happened, because the client might get confused by the disjointed text. However, our current logic of sending `Previous response was interrupted. Continue exactly where you stopped` is designed to do exactly this. 
3.  **Fixing `attemptModel` logic:** We need to change the logic in `handleNonOKStatus`. If `rc.headersSent` is true, and we are in a stream retry, and the *retry attempt* returns a 500, we still want to trigger the fallback logic (which would try the fallback model with the "continue continuing" prompt), rather than returning `attemptReturnImmediately`.
