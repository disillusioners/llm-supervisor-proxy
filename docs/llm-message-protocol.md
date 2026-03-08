# LLM Message Protocol Overview

## OpenAI Chat Format and Emerging Thinking Extensions (Z.ai)

## 1. Purpose

This document describes the **LLM message protocol structure**, primarily focusing on the **OpenAI chat/message format**, and how newer models introduce **reasoning-aware extensions** such as **Preserved Thinking** and **Interleaved Thinking** (e.g., Z.ai thinking mode).

The goal is to clarify:

* Message structures used by modern LLM APIs
* Tool calling message flow
* Streaming delta formats
* New reasoning-aware message types
* Implications for LLM proxy / agent runtimes

---

# 2. Core Chat Message Protocol (OpenAI)

Most modern LLM APIs are based on a **role-based message protocol**.

A conversation is a sequence of messages.

```
messages = [
  {role: "...", content: ...}
]
```

## 2.1 Message Roles

| Role        | Description                       |
| ----------- | --------------------------------- |
| `system`    | System instructions for the model |
| `user`      | End-user input                    |
| `assistant` | Model response                    |
| `tool`      | Tool execution result             |

Example:

```json
[
  {"role": "system", "content": "You are a helpful assistant"},
  {"role": "user", "content": "What is the weather in Paris?"}
]
```

---

# 3. Assistant Message Structures

Assistant messages are **not always simple text**.

There are multiple valid formats.

---

# 3.1 Simple Text Response

Legacy / simplest format.

```json
{
  "role": "assistant",
  "content": "The weather in Paris is currently 18°C."
}
```

---

# 3.2 Structured Content Parts

Modern APIs allow `content` to be **an array of parts**.

```json
{
  "role": "assistant",
  "content": [
    {
      "type": "text",
      "text": "The weather in Paris is currently 18°C."
    }
  ]
}
```

Content parts allow **multimodal outputs**.

Common types:

| Type          | Description                  |
| ------------- | ---------------------------- |
| `text`        | textual output               |
| `image_url`   | image reference              |
| `input_text`  | text input (multimodal APIs) |
| `input_image` | image input                  |

Example:

```json
{
  "role": "assistant",
  "content": [
    {"type": "text", "text": "Here is the diagram"},
    {"type": "image_url", "image_url": {"url": "https://example.com/img.png"}}
  ]
}
```

---

# 4. Tool Calling Protocol

Tool calling allows the model to request external actions.

## 4.1 Assistant Tool Call

```json
{
  "role": "assistant",
  "tool_calls": [
    {
      "id": "call_123",
      "type": "function",
      "function": {
        "name": "get_weather",
        "arguments": "{\"city\":\"Paris\"}"
      }
    }
  ]
}
```

Key fields:

| Field                | Description                 |
| -------------------- | --------------------------- |
| `id`                 | unique tool call identifier |
| `type`               | usually `function`          |
| `function.name`      | tool name                   |
| `function.arguments` | JSON arguments string       |

---

## 4.2 Tool Result Message

Client executes tool and sends result back.

```json
{
  "role": "tool",
  "tool_call_id": "call_123",
  "content": "18°C and sunny"
}
```

The conversation continues with another assistant message.

---

# 5. Mixed Assistant Messages

Assistant messages may contain **both content and tool calls**.

Example:

```json
{
  "role": "assistant",
  "content": [
    {"type": "text", "text": "Let me check the weather."}
  ],
  "tool_calls": [
    {
      "id": "call_abc",
      "type": "function",
      "function": {
        "name": "get_weather",
        "arguments": "{\"city\":\"Paris\"}"
      }
    }
  ]
}
```

Possible assistant shapes:

| Case              | Valid |
| ----------------- | ----- |
| Text only         | ✓     |
| Tool calls only   | ✓     |
| Text + tool calls | ✓     |
| Empty content     | ✓     |

---

# 6. Streaming Protocol

In streaming mode the response arrives as **delta updates**.

Example:

```json
{
  "choices": [
    {
      "delta": {
        "content": "Hello"
      }
    }
  ]
}
```

Tool calls may stream incrementally:

```json
{
  "choices": [
    {
      "delta": {
        "tool_calls": [
          {
            "index": 0,
            "function": {
              "arguments": "{\"city\":\""
            }
          }
        ]
      }
    }
  ]
}
```

Arguments arrive **chunk by chunk** until complete.

A proxy or client must **assemble the full message**.

---

# 7. Limitations of Traditional Chat Protocol

The standard chat protocol assumes a simple pattern:

```
user → assistant → final answer
```

However **agent workflows** require more complex behavior:

```
user
 ↓
think
 ↓
tool call
 ↓
think
 ↓
tool call
 ↓
final answer
```

Traditional APIs hide the **thinking step**, causing:

* repeated reasoning
* tool misuse
* inefficient token usage
* poor multi-step planning

---

# 8. Thinking-Aware Extensions

New LLM providers are introducing **explicit reasoning protocols**.

Examples:

| Provider  | Feature                          |
| --------- | -------------------------------- |
| OpenAI    | reasoning tokens                 |
| Anthropic | extended thinking                |
| DeepSeek  | reasoning models                 |
| Google    | thought tokens                   |
| Z.ai      | preserved + interleaved thinking |

These expose **reasoning as a structured artifact**.

---

# 9. Z.ai Thinking Mode

Z.ai introduces two key capabilities.

## 9.1 Interleaved Thinking

The model can **reason between actions**.

Traditional workflow:

```
think → answer
```

Interleaved workflow:

```
think
 ↓
tool call
 ↓
think
 ↓
tool call
 ↓
think
 ↓
final answer
```

Example conversation:

Assistant:

```json
{
  "role": "assistant",
  "thinking": "Need weather data",
  "tool_calls": [
    {
      "name": "get_weather",
      "arguments": {"city": "Paris"}
    }
  ]
}
```

Tool:

```json
{
  "role": "tool",
  "content": "18°C"
}
```

Assistant continues reasoning:

```json
{
  "role": "assistant",
  "thinking": "Weather is mild, recommend walking",
  "content": "It's 18°C in Paris. Good weather for walking."
}
```

Key idea:

**Reasoning occurs between tool calls.**

---

## 9.2 Preserved Thinking

Normally LLM reasoning is **discarded after each turn**.

Preserved thinking stores reasoning so the model can reuse it later.

Standard conversation:

```
Turn 1:
  think → answer

Turn 2:
  think again
```

Preserved thinking:

```
Turn 1:
  reasoning block A

Turn 2:
  reuse A + new reasoning B
```

Example:

Turn 1 assistant:

```json
{
  "role": "assistant",
  "reasoning": [
    "Project uses Go",
    "Deployment uses Docker"
  ],
  "content": "Use container build pipeline"
}
```

Turn 2 assistant:

```json
{
  "role": "assistant",
  "reasoning": [
    "Reuse previous reasoning",
    "Environment now uses Kubernetes"
  ],
  "content": "Switch to Kubernetes deployment strategy"
}
```

Benefits:

* avoids recomputing reasoning
* improves long agent workflows
* reduces token cost
* improves consistency

---

# 10. Protocol Evolution

The LLM message protocol is evolving from a **chat format** to an **agent execution protocol**.

Traditional:

```
user → assistant(text)
```

Agent-oriented:

```
user
 ↓
thinking
 ↓
tool_call
 ↓
tool_result
 ↓
thinking
 ↓
final_response
```

Future APIs may include explicit fields such as:

```
thinking
reasoning
planning
reflection
tool_calls
content
```

---

# 11. Implications for LLM Proxy Design

If implementing a **multi-provider proxy**, the protocol must support:

### Multiple assistant output types

Possible fields:

```
content
tool_calls
thinking
reasoning
reasoning_content
```

### Multiple assistant steps per response

```
assistant(thinking)
assistant(tool_call)
tool(result)
assistant(thinking)
assistant(final)
```

### Streaming assembly

Proxy must reconstruct:

```
tool_call arguments
content text
reasoning blocks
```

### Provider-specific extensions

Different providers may use different reasoning fields:

| Provider  | Field                |
| --------- | -------------------- |
| OpenAI    | reasoning tokens     |
| Anthropic | thinking             |
| Z.ai      | reasoning / thinking |
| DeepSeek  | reasoning            |

A proxy may normalize these internally.

---

# 12. Key Takeaways

1. **OpenAI chat protocol** is the foundation for most LLM APIs.
2. Assistant messages support **multiple formats**.
3. Tool calling introduces **structured execution steps**.
4. Streaming responses require **delta assembly**.
5. New models introduce **explicit reasoning protocols**.
6. Z.ai introduces:

   * **Interleaved Thinking**
   * **Preserved Thinking**
7. LLM APIs are evolving toward a **full agent runtime protocol**.

---
