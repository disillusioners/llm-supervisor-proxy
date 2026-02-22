# Loop Detection - Manual Testing Guide

This guide explains how to manually test all 6 loop detection strategies using the included mock LLM server. 

## Prerequisites

You need three terminal windows to run the test environment.

### 1. Start the Mock Loop LLM Server
This server listens on port `:4002` and generates infinite loops of various types based on trigger keywords in the prompt.
```bash
go run test/mock_llm_loop.go
```

### 2. Start the Frontend Dev Server
If you want to observe the events in the Web UI:
```bash
cd pkg/ui/frontend
npm run dev
```

### 3. Start the Supervisor Proxy (Active Mode)
We need to point the proxy to our mock loop server and disable `shadow_mode` so it actively interrupts the loops.
```bash
go build -o supervisor-proxy ./cmd/main.go

UPSTREAM_URL=http://localhost:4002 \
PORT=4321 \
LOOP_DETECTION_SHADOW_MODE=false \
./supervisor-proxy
```

---

## Running the Tests

You can trigger the 6 different loop scenarios by sending specific keywords in the `content` field. Run these commands in a 4th terminal window.

> **Tip**: Keep the Web UI open at `http://localhost:4321` to watch the events populate in real-time.

### Scenario 1: Exact Match Loop
The LLM gets stuck repeatedly sending the *exact same string* across multiple completion chunks.

```bash
curl -N -X POST http://localhost:4321/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4",
    "messages": [{"role": "user", "content": "Trigger loop-exact"}],
    "stream": true
  }'
```
**Expected Result:** Proxy interrupts after 3 identical responses. You should see a `⚡ Loop interrupted: exact_match` event in the UI.

### Scenario 2: Similarity Loop
The LLM sends *slightly different* messages that mean the exact same thing (minor word variations).

```bash
curl -N -X POST http://localhost:4321/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4",
    "messages": [{"role": "user", "content": "Trigger loop-similar"}],
    "stream": true
  }'
```
**Expected Result:** Proxy interrupts once it detects 3 messages with >85% SimHash similarity. Event: `loop_interrupted: similarity`.

### Scenario 3: Action Pattern (Repeated Tool Calls)
The LLM repeatedly calls the exact same tool with the exact same arguments without making progress.

```bash
curl -N -X POST http://localhost:4321/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4",
    "messages": [{"role": "user", "content": "Trigger loop-action"}],
    "stream": true
  }'
```
**Expected Result:** Proxy interrupts after the same tool is called 3 consecutive times. Event: `loop_interrupted: action_pattern`.

### Scenario 4: Action Oscillation (A ↔ B)
The LLM gets stuck alternating between two actions (e.g., `read_file` -> `write_file` -> `read_file`...).

```bash
curl -N -X POST http://localhost:4321/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4",
    "messages": [{"role": "user", "content": "Trigger loop-oscillate"}],
    "stream": true
  }'
```
**Expected Result:** Proxy interrupts after identifying the A↔B oscillation pattern repeating. Event: `loop_interrupted: action_pattern`.

### Scenario 5: Cycle Loop (A → B → C)
The LLM gets stuck in a multi-step circular workflow.

```bash
curl -N -X POST http://localhost:4321/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4",
    "messages": [{"role": "user", "content": "Trigger loop-cycle"}],
    "stream": true
  }'
```
**Expected Result:** Proxy identifies a repeating subsequence (length > 2) and interrupts. Event: `loop_interrupted: cycle`.

### Scenario 6: Stagnation Loop
The LLM produces hundreds of tokens of output that superficially looks unique but has very high cumulative similarity, failing to advance the conversation.

```bash
curl -N -X POST http://localhost:4321/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4",
    "messages": [{"role": "user", "content": "Trigger loop-stagnate"}],
    "stream": true
  }'
```
**Expected Result:** Proxy calculates the average pairwise similarity of the recent window and interrupts when it exceeds 85%. Event: `loop_interrupted: stagnation`.

### Scenario 7: Thinking Loop (Repetitive Reasoning)
The LLM generates reasoning tokens (`reasoning_content`) that go in circles, evaluated via N-gram analysis.

```bash
curl -N -X POST http://localhost:4321/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4",
    "messages": [{"role": "user", "content": "Trigger loop-thinking"}],
    "stream": true
  }'
```
**Expected Result:** Proxy calculates the unique trigram ratio and interrupts when the ratio falls below 0.30 (highly repetitive). Event: `loop_interrupted: thinking`.

---

## Observing the Recovery Flow (Context Sanitization)

When a loop is successfully detected and interrupted (`LOOP_DETECTION_SHADOW_MODE=false`), you should observe the following sequence:

1. **Proxy Output Logs**:
   - `[LOOP-DETECTION][INTERRUPT] Stopping stream — <strategy>: <evidence>`
   - `[LOOP-DETECTION] Context sanitized: 3 → 2 messages`
   - `Retrying request (attempt 1)...`
2. **Web UI Event Log**:
   - 🟡 `LOOP_DETECTED` (Warning that a loop is forming)
   - 🔴 `LOOP_INTERRUPTED` (The moment the proxy aggressively cuts the stream)
   - 🟣 `RETRY_ATTEMPT` (The proxy firing the request again with the sanitized context block)
3. **Web UI Messages Tab**:
   - Look at the `messages` payload for the Retry attempt. You will see a newly injected `system` message instructing the model to stop looping:
     > *"System: Your previous 3 responses were identical. Please take a completely different approach. Do NOT repeat what you just tried."*
