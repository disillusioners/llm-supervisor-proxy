# OpenAI Tool Calls in Streaming Mode - Protocol Specification

This document describes the full, practical spec of OpenAI tool calls in streaming mode based on actual protocol behavior.

## 1. Top-level Streaming Structure

Each SSE / chunk looks like:

```json
{
  "id": "chatcmpl-xxx",
  "object": "chat.completion.chunk",
  "created": 1710000000,
  "model": "gpt-4o-mini",
  "choices": [
    {
      "index": 0,
      "delta": { ... },
      "finish_reason": null
    }
  ]
}
```

## 2. Where Tool Calls Appear

Tool calls are inside:

```
choices[0].delta.tool_calls
```

**NOT** in `message` (that's only for non-streaming).

## 3. Tool Call Delta Schema

Each chunk may contain:

```json
{
  "tool_calls": [
    {
      "index": 0,
      "id": "call_xxx",        // optional (usually first chunk only)
      "type": "function",      // optional (usually first chunk only)
      "function": {
        "name": "my_func",     // optional (early chunk)
        "arguments": "{...}"   // optional (streamed progressively)
      }
    }
  ]
}
```

## 4. Field-by-field Behavior (VERY IMPORTANT)

### 🔢 index (REQUIRED for correctness)

- **Type:** Integer
- Identifies the tool call
- Stable across all chunks of the same call
- **👉 You MUST group by this.**

### 🆔 id

- Present only in the first chunk (usually)
- Example: `"call_abc123"`
- Reused across all chunks implicitly (not repeated)
- **👉 Store it when first seen.**

### 🧠 type

- Always `"function"`
- Often only appears in first chunk

### 🔤 function.name

- Usually appears once at the beginning
- May be missing in later chunks

### 📦 function.arguments

- Streamed as partial strings
- Can be:
  - Empty
  - Partial JSON
  - Split across many chunks

Example:
```
Chunk 1: "{"
Chunk 2: "\"city\":"
Chunk 3: "\"Paris\"}"
```

**👉 You must concatenate:**
```javascript
args += delta.function.arguments
```

## 5. Multiple Tool Calls (Parallel)

Yes — model can emit:

```json
tool_calls: [
  { "index": 0, ... },
  { "index": 1, ... }
]
```

And chunks may interleave like:
```
Chunk A → index 0
Chunk B → index 1
Chunk C → index 0
```

**👉 So your structure must be:**
```javascript
toolCalls[index]
```

NOT a single accumulator.

## 6. Finish Signal

Final chunk:

```json
{
  "choices": [
    {
      "delta": {},
      "finish_reason": "tool_calls"
    }
  ]
}
```

**👉 Means:**
- Model is done generating tool calls
- You should now execute them

## 7. Full Reconstruction Target

After streaming, you should reconstruct:

```json
{
  "tool_calls": [
    {
      "id": "call_xxx",
      "type": "function",
      "function": {
        "name": "my_func",
        "arguments": "{\"city\":\"Paris\"}"
      }
    }
  ]
}
```

## 8. Reference Reconstruction Algorithm

Pseudocode:

```javascript
const toolCalls = {}

for (chunk of stream) {
  const delta = chunk.choices[0].delta

  if (!delta.tool_calls) continue

  for (tc of delta.tool_calls) {
    const i = tc.index

    if (!toolCalls[i]) {
      toolCalls[i] = {
        id: null,
        type: "function",
        function: {
          name: "",
          arguments: ""
        }
      }
    }

    if (tc.id) toolCalls[i].id = tc.id
    if (tc.function?.name) toolCalls[i].function.name = tc.function.name
    if (tc.function?.arguments)
      toolCalls[i].function.arguments += tc.function.arguments
  }
}
```

## 9. Edge Cases You MUST Handle

### ❗ Missing fields

A chunk may contain ONLY:

```json
{ "index": 0 }
```

**👉 Always null-check everything.**

### ❗ Arguments not valid JSON until the end

Do NOT parse early:

```javascript
JSON.parse(arguments) // ❌ (will crash mid-stream)
```

**👉 Only parse after `finish_reason === "tool_calls"`**

### ❗ Empty deltas

You may get:

```json
{ "delta": {} }
```

Ignore safely.

### ❗ No tool_calls in some chunks

Normal — tool calls are sparse.

## 10. Differences vs Non-streaming

| Feature | Streaming | Non-streaming |
|---------|-----------|---------------|
| tool_calls | in `delta` | in `message` |
| arguments | partial strings | full JSON string |
| index | REQUIRED | NOT present |
| reconstruction | required | not needed |

## 11. Compatibility Issues (Important)

Not all "OpenAI-compatible" APIs follow this spec:

| Provider | Issue |
|----------|-------|
| Gemini (compat) | missing `index` |
| Ollama | sometimes no `id` |
| Some proxies | send full `args` in one chunk |

**👉 So production parser should:**
- Fallback if `index` missing → assume `0`
- Tolerate missing `id`

## 12. Minimal Mental Model

Think of streaming tool calls as:

> "Token streaming, but for structured function calls"

- `name` → emitted once
- `arguments` → token stream
- `index` → stream ID
