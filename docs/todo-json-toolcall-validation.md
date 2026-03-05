# Proxy Feature Plan: Automatic Tool Call JSON Repair (OpenAI-Compatible)

## Goal

Reduce client failures caused by malformed tool call arguments returned by upstream LLM providers.

The proxy will detect, repair, and retry invalid tool call JSON before forwarding responses to the client.

Scope: **OpenAI-compatible APIs only**.

---

# Problem

OpenAI tool calls return arguments as a **JSON string**, which frequently becomes malformed due to model output errors.

Common issues:

* Invalid JSON syntax
* Unescaped quotes
* Partial JSON due to streaming
* Extra reasoning text inside arguments
* Duplicate keys
* Very large argument payloads

Example broken output:

```
{
  "tool_calls": [
    {
      "function": {
        "name": "edit",
        "arguments": "{\"filePath\":\"/app/main.go\",\"newString\":\"let header = {"Authorization": "Bearer " + apiKey}\"}"
      }
    }
  ]
}
```

This causes downstream JSON parsing failures in clients.

---

# Solution

Introduce a **Tool Call Validation + Repair Layer** inside the proxy.

The proxy will:

1. Detect tool calls in upstream responses
2. Validate `function.arguments`
3. Attempt automatic JSON repair if parsing fails
4. Retry upstream if repair fails
5. Return valid tool call responses to clients

---

# Architecture

```
Client
  │
  ▼
Proxy
  │
  ├── Upstream Request
  │
  ├── Response Interceptor
  │       │
  │       ├── Tool Call Detector
  │       ├── JSON Validator
  │       ├── JSON Repair
  │       └── Retry Engine
  │
  ▼
Client Response
```

---

# Detection

Intercept responses containing:

```
choices[].message.tool_calls
```

For each tool call:

```
tool_call.function.arguments
```

This field must contain valid JSON.

---

# Validation

Attempt JSON parsing:

```
json.Unmarshal(arguments)
```

If parsing succeeds → forward response unchanged.

If parsing fails → start repair pipeline.

---

# Repair Pipeline

Repair attempts executed sequentially.

### Step 1 — Extract JSON Block

Remove surrounding text if model included explanation.

Example:

```
Here is the tool call:

{ ... }
```

Extract only the JSON object.

---

### Step 2 — Tolerant JSON Repair

Use tolerant parsing or repair library to fix common issues:

* Missing quotes around keys
* Trailing commas
* Unescaped quotes
* Invalid escape sequences

Example fix:

```
{filePath:"main.go"}
→
{"filePath":"main.go"}
```

---

### Step 3 — Escape Dangerous Quotes

Repair unescaped quotes inside strings:

```
"Authorization": "Bearer " + apiKey
```

→

```
\"Authorization\": \"Bearer \" + apiKey
```

---

### Step 4 — Remove Reasoning Leakage

Remove known reasoning patterns accidentally inserted into arguments:

* "Summary:"
* "Approach:"
* "Recommended:"
* "Let me"

These should never appear in tool arguments.

---

# Retry Strategy

If repair fails:

1 retry upstream request.

Retry prompt injection:

```
The previous tool call arguments were invalid JSON.
Return only valid JSON matching the tool schema.
```

Retry limit:

```
max_tool_repair_retries = 1
```

---

# Streaming Handling

When streaming responses:

* Collect tool call argument chunks
* Reconstruct final `arguments` string
* Validate JSON after stream completes

If invalid:

Apply repair pipeline before emitting final event.

---

# Safety Limits

Prevent pathological tool calls.

Limits:

```
max_tool_arguments_size = 10KB
max_tool_calls_per_response = 8
```

If exceeded:

Return proxy error:

```
tool_arguments_too_large
```

---

# Metrics

Expose Prometheus metrics.

```
proxy_tool_json_invalid_total
proxy_tool_json_repaired_total
proxy_tool_repair_failed_total
proxy_tool_retry_total
```

These metrics help track upstream reliability.

---

# Logging

Structured logs for debugging:

```
event=tool_json_invalid
tool_name=edit
provider=openai
repair_attempt=1
```

Include optional debug logging of original arguments (truncated).

---

# Configuration

```
tool_repair_enabled: true
tool_repair_retry_limit: 1
max_tool_arguments_size: 10kb
```

Feature should be configurable per deployment.

---

# Implementation Steps

1. Add response interceptor for OpenAI responses
2. Detect `tool_calls`
3. Implement JSON validator
4. Add repair pipeline
5. Add retry mechanism
6. Add streaming reconstruction
7. Add metrics + logging
8. Add configuration flags

---

# Expected Impact

Estimated reduction in client tool-call failures:

```
60–90%
```

Benefits:

* Clients receive valid JSON tool arguments
* Reduced agent crashes
* Improved compatibility with LLM providers

---

# Future Extensions

Possible future improvements:

* Support Anthropic tool format
* Auto-convert large edits to patch/diff format
* Tool schema enforcement
* Partial JSON recovery during streaming
* Tool argument size compression

---
