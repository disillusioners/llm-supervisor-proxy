#!/bin/bash

echo "Starting mock_llm.go..."
go run mock_llm.go &
MOCK_PID=$!

# Wait for server to be ready
sleep 2

URL="http://localhost:4001/v1/chat/completions"

echo -e "\n--- Test 1: Normal Streaming ---"
curl -N -s -X POST $URL \
    -H "Content-Type: application/json" \
    -d '{
        "stream": true,
        "messages": [{"role": "user", "content": "hello"}]
    }'

echo -e "\n--- Test 2: Non-Streaming ---"
curl -s -X POST $URL \
    -H "Content-Type: application/json" \
    -d '{
        "stream": false,
        "messages": [{"role": "user", "content": "hello"}]
    }'

echo -e "\n\n--- Test 3: mock-think ---"
curl -N -s -X POST $URL \
    -H "Content-Type: application/json" \
    -d '{
        "stream": true,
        "messages": [{"role": "user", "content": "mock-think sequence"}]
    }'

echo -e "\n--- Test 4: mock-tool ---"
curl -N -s -X POST $URL \
    -H "Content-Type: application/json" \
    -d '{
        "stream": true,
        "messages": [{"role": "user", "content": "trigger mock-tool"}]
    }'

echo -e "\n--- Test 5: mock-hang (timeout after 2s) ---"
curl -N -s --max-time 2 -X POST $URL \
    -H "Content-Type: application/json" \
    -d '{
        "stream": true,
        "messages": [{"role": "user", "content": "trigger mock-hang"}]
    }'

echo -e "\n\n--- Test 6: mock-long (head 20 lines) ---"
curl -N -s -X POST $URL \
    -H "Content-Type: application/json" \
    -d '{
        "stream": true,
        "messages": [{"role": "user", "content": "trigger mock-long"}]
    }' | head -n 20

echo -e "\n\nStopping mock_llm..."
kill $MOCK_PID
wait $MOCK_PID 2>/dev/null
echo "Done!"
