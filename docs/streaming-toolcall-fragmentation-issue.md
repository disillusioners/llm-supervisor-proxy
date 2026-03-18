# Streaming Tool-Call Fragmentation Issue

## Problem Description

When streaming tool calls from LLM providers (especially non-OpenAI providers like MiniMax), the JSON arguments for tool calls can be split across multiple SSE chunks. This causes `AI_JSONParseError` when the parser attempts to parse each chunk as a complete JSON object.

### Error Example

```
AI_JSONParseError: JSON parsing failed: Text: {"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"{\"include\": \"*.go\", \"pattern\": \"event.*log|EventLog|event_log\"}","name":"grep"},"id":"call_function_fby4618py6wv_2","index":1,"type":"function"}]},"index":0}],"created":1773862864,"id":"chatcmpl-1773862864966387000","model":"MiniMax-M2.5","object":"chat.completion.chunk"}
{"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"\"}","name":""},"id":"","index":0,"type":""}]},"index":0}],"created":1773862865,"id":"chatcmpl-1773862865645769000","model":"MiniMax-M2.5","object":"chat.completion.chunk"}.
Error message: JSON Parse error: Unable to parse JSON string
```

## Root Cause Analysis

### Chunk Breakdown

The error occurs because the tool call arguments are split across two separate streaming chunks:

**Chunk 1:**
```json
{
  "choices": [
    {
      "delta": {
        "tool_calls": [
          {
            "function": {
              "arguments": "{\"include\": \"*.go\", \"pattern\": \"event.*log|EventLog|event_log\"}",
              "name": "grep"
            },
            "id": "call_function_fby4618py6wv_2",
            "index": 1,
            "type": "function"
          }
        ]
      },
      "index": 0
    }
  ]
}
```

**Chunk 2:**
```json
{
  "choices": [
    {
      "delta": {
        "tool_calls": [
          {
            "function": {
              "arguments": "\"}",
              "name": ""
            },
            "id": "",
            "index": 0,
            "type": ""
          }
        ]
      },
      "index": 0
    }
  ]
}
```

### Issues Identified

#### 1. Arguments Split Across Chunks

- **Chunk 1:** `"{\"include\": \"*.go\", \"pattern\": \"event.*log|EventLog|event_log\"}"`
- **Chunk 2:** `"\"}"`
- **Combined:** `"{\"include\": \"*.go\", \"pattern\": \"event.*log|EventLog|event_log\"}\"}"`

The combined result is invalid JSON due to the extra `\"}` at the end.

#### 2. Tool Call Metadata Fragmentation

Chunk 2 contains empty fields:
```json
{
  "name": "",
  "id": "",
  "type": ""
}
```

This is **not** a new tool call — it's a continuation of the previous one. Empty fields should not overwrite existing values.

## The Key Rule

> **Streaming `tool_calls` must be reconstructed by `index`, not parsed per chunk.**

## Correct Implementation

### Step 1: Group by `tool_calls[index]`

```typescript
toolCalls[index] = {
  id,
  type,
  function: {
    name,
    arguments: "" // accumulated string
  }
}
```

### Step 2: Append Arguments Incrementally

```typescript
toolCall.function.arguments += delta.function.arguments || ""
```

### Step 3: Ignore Empty Fields in Later Chunks

If you receive:
```json
{
  "name": "",
  "id": ""
}
```

**DO NOT** overwrite existing values with empty strings.

## Example Fix (Pseudo Code)

```typescript
const toolCalls: Record<number, ToolCall> = {};

for (const chunk of stream) {
  const deltas = chunk.choices[0]?.delta?.tool_calls || [];

  for (const deltaCall of deltas) {
    const i = deltaCall.index;

    // Initialize tool call slot if needed
    if (!toolCalls[i]) {
      toolCalls[i] = {
        id: deltaCall.id || "",
        type: deltaCall.type || "function",
        function: {
          name: deltaCall.function?.name || "",
          arguments: ""
        }
      };
    }

    // Only update if non-empty
    if (deltaCall.id) toolCalls[i].id = deltaCall.id;
    if (deltaCall.type) toolCalls[i].type = deltaCall.type;
    if (deltaCall.function?.name) {
      toolCalls[i].function.name = deltaCall.function.name;
    }

    // Accumulate arguments
    if (deltaCall.function?.arguments) {
      toolCalls[i].function.arguments += deltaCall.function.arguments;
    }
  }
}
```

## Edge Case: Double-Closed JSON String

In the example above, the stream produced:

```
...event_log\"}"
+ "\"}"
```

This means the model double-closed the JSON string. After full accumulation, you may need to clean the arguments:

```typescript
function cleanArguments(args: string): string {
  // Trim trailing garbage
  let cleaned = args.trim();
  
  // Detect and remove duplicated closing "}
  // This is provider-specific behavior
  if (cleaned.endsWith('\"}"') && cleaned.indexOf('\"}') !== cleaned.lastIndexOf('\"}')) {
    cleaned = cleaned.slice(0, -2);
  }
  
  return cleaned;
}

// Usage
try {
  const parsed = JSON.parse(cleanArguments(toolCall.function.arguments));
} catch (e) {
  // Handle parse error - possibly wait for more chunks
}
```

## Safer Strategy (Recommended)

Instead of trusting raw concatenation:

1. **Accumulate string** across all chunks
2. **Try `JSON.parse()`** when stream completes
3. **If parse fails:**
   - Wait for more chunks (if stream not complete)
   - OR attempt repair (last resort)

```typescript
function parseToolCallArguments(args: string): unknown | null {
  try {
    return JSON.parse(args);
  } catch (e) {
    // Attempt repair
    const repaired = attemptRepair(args);
    if (repaired) {
      try {
        return JSON.parse(repaired);
      } catch (e2) {
        return null;
      }
    }
    return null;
  }
}

function attemptRepair(args: string): string | null {
  // Common repair strategies:
  // 1. Remove trailing garbage
  // 2. Balance brackets
  // 3. Fix escaped quotes
  return null; // Return repaired string or null if unrepairable
}
```

## Provider-Specific Considerations

Different providers (like MiniMax) are less strict than OpenAI and may:

| Issue | Description |
|-------|-------------|
| Emit broken JSON | Invalid JSON structure |
| Emit partial tokens incorrectly | Incomplete tokens split incorrectly |
| Reset fields mid-stream | Empty fields in continuation chunks |

**Your parser must be:**
- **Stateful** — Track accumulated state across chunks
- **Tolerant** — Handle malformed input gracefully
- **Defensive** — Don't overwrite valid data with empty values

## Summary

| Issue | Cause | Fix |
|-------|-------|-----|
| Parse chunks individually | Each chunk treated as complete | Reconstruct by index |
| Arguments split across chunks | Streaming fragmentation | Append arguments incrementally |
| Later chunk corrupts JSON | Empty fields overwrite valid data | Ignore empty overwrites |
| Premature parsing | Parse before stream complete | Parse only after completion |

## Related Documentation

- [`docs/openai-streaming-tool-calls-spec.md`](openai-streaming-tool-calls-spec.md) — OpenAI streaming specification
- [`docs/streaming-toolcall-implementation-issues.md`](streaming-toolcall-implementation-issues.md) — Implementation issues
- [`plans/streaming-toolcall-repair-fix.md`](../plans/streaming-toolcall-repair-fix.md) — Repair implementation plan
