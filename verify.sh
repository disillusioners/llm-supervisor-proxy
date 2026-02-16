#!/bin/bash
set -e

# Cleanup on exit
trap 'kill $(jobs -p)' EXIT

echo "Building proxy..."
go build -o bin/proxy cmd/main.go

echo "Building mock..."
go build -o bin/mock test/mock_llm.go

echo "Starting mock server..."
./bin/mock &
MOCK_PID=$!
sleep 2

echo "Starting proxy server..."
export UPSTREAM_URL="http://localhost:4000"
export IDLE_TIMEOUT="5s" # Short timeout for testing (mock hangs for 15s)
export MAX_RETRIES="1"
./bin/proxy &
PROXY_PID=$!
sleep 2

echo "Sending request to proxy..."
# We expect the mock to hang on " skipped", trigger timeout in proxy, retry, 
# and potentially hang again if the mock is stateless and deterministic.
# Wait, if the mock is deterministic, it will hang again on retry!
# My mock logic: if msg == " skipped" { hang }
# So retry will also hang.
# I should update the mock to be smarter or less deterministic?
# Or update the mock to only hang once?

# Let's update the mock first to only hang on the first request?
# Or use a header to control it?

curl -v -N -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-3.5-turbo",
    "messages": [{"role": "user", "content": "Hello"}]
  }' > output.txt 2>&1

echo "Request finished. Checking output..."
cat output.txt
