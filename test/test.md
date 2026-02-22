# Mock

curl --location 'http://localhost:8089/v1/chat/completions' \
--header 'Content-Type: application/json' \
--header 'Authorization: Bearer sk-7MrDwc0Iw4VUqrKbYdsnTg' \
--data '{
    "model": "glm-5",
    "messages": [
        {
            "role": "user",
            "content": "Hello?"
        }
    ],
    "stream": false
}'

curl --location 'http://localhost:4321/v1/chat/completions' \
--header 'Content-Type: application/json' \
--header 'Authorization: Bearer sk-7MrDwc0Iw4VUqrKbYdsnTg' \
--data '{
    "model": "glm-5",
    "messages": [
        {
            "role": "user",
            "content": "Hello?"
        }
    ],
    "stream": false
}'


curl --location 'http://localhost:4321/v1/chat/completions' \
--header 'Content-Type: application/json' \
--header 'Authorization: Bearer sk-7MrDwc0Iw4VUqrKbYdsnTg' \
--data '{
    "model": "MiniMax-M2.5",
    "messages": [
        {
            "role": "user",
            "content": "Hello?"
        }
    ],
    "stream": true
}'

curl --location 'http://localhost:4321/v1/chat/completions' \
--header 'Content-Type: application/json' \
--header 'Authorization: Bearer sk-7MrDwc0Iw4VUqrKbYdsnTg' \
--data '{
    "model": "glm-test",
    "messages": [
        {
            "role": "user",
            "content": "Hello?"
        }
    ],
    "stream": true
}'


curl --location 'http://litellm-service.litellm.svc.cluster.local:4321/v1/chat/completions' \
--header 'Content-Type: application/json' \
--header 'Authorization: Bearer sk-7MrDwc0Iw4VUqrKbYdsnTg' \
--data '{
    "model": "glm-4.5-air",
    "messages": [
        {
            "role": "user",
            "content": "Hello?"
        }
    ],
    "stream": true
}'


<!-- mock-500, change it to mock-timeout or mock-hang -->


curl --location 'http://localhost:4321/v1/chat/completions' \
--header 'Content-Type: application/json' \
--header 'Authorization: Bearer mock-test' \
--data '{
    "model": "glm-test",
    "messages": [
        {
            "role": "user",
            "content": "mock-hang"
        }
    ],
    "stream": true
}'


curl --location 'http://localhost:4321/v1/chat/completions' \
--header 'Content-Type: application/json' \
--header 'Authorization: Bearer mock-test' \
--data '{
    "model": "glm-test",
    "messages": [
        {
            "role": "user",
            "content": "mock-timeout"
        }
    ],
    "stream": true
}'

---

# Loop Detection Testing

## Start the loop mock LLM (port 4002)

```bash
cd test && go run mock_llm_loop.go
```

## Start the proxy pointing to the loop mock

```bash
UPSTREAM_URL=http://localhost:4002 go run cmd/main.go
```

## Trigger keywords (use as the prompt content)

| Keyword | Scenario | Expected Detection |
|---------|----------|--------------------|
| `loop-exact` | Identical response every time | ExactMatchStrategy (critical) |
| `loop-similar` | Near-identical with minor word swaps | SimilarityStrategy (warning→critical) |
| `loop-action` | Same tool call repeated | ActionPatternStrategy (critical) |
| `loop-oscillate` | read→write→read→write cycle | ActionPatternStrategy oscillation (warning) |
| `loop-thinking` | Repetitive reasoning content | SimHash similarity on thinking |
| *(any other)* | Normal response | No detection |

## Example curl commands

<!-- loop-exact: sends the same response every time -->
curl -N --location 'http://localhost:4321/v1/chat/completions' \
--header 'Content-Type: application/json' \
--data '{
    "model": "mock-model",
    "messages": [{"role": "user", "content": "loop-exact"}],
    "stream": true
}'

<!-- loop-similar: near-identical responses -->
curl -N --location 'http://localhost:4321/v1/chat/completions' \
--header 'Content-Type: application/json' \
--data '{
    "model": "mock-model",
    "messages": [{"role": "user", "content": "loop-similar"}],
    "stream": true
}'

<!-- loop-action: same tool call repeated -->
curl -N --location 'http://localhost:4321/v1/chat/completions' \
--header 'Content-Type: application/json' \
--data '{
    "model": "mock-model",
    "messages": [{"role": "user", "content": "loop-action"}],
    "stream": true
}'

## Run the full test script

```bash
cd test && bash test_loop_detection.sh
```

Check the **proxy logs** for `[LOOP-DETECTION][SHADOW]` entries.