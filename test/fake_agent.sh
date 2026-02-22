#!/bin/bash

# Talk to the proxy, mimicking an agent
echo "Starting fake agent loop-action test..."

# 1. Initial request
curl -s -X POST http://localhost:4321/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4",
    "messages": [
      {"role": "user", "content": "Please check loop-action"}
    ],
    "stream": true
  }' > /dev/null

echo "Sent request 1"
sleep 1

# If it is a real agent it would send previous history back.
# We don't have a real agent, so we'll just send 3 requests with the same user prompt.
# Since the proxy doesn't link separate HTTP requests, it won't detect loops *across* distinct HTTP requests unless the client sends the entire history!
# Oh right! The LLM proxy analyzes the streaming response. BUT wait... the proxy creates a NEW loop detector for each REQUEST.
# So if a client sends 3 distinct HTTP requests, the proxy creates 3 distinct loop detectors.
# Loop detection in this proxy ONLY works if the client sends the full conversation history in the JSON payload!
# BUT wait! handler_functions.go DOES NOT load existing history from the request body into the detector!!
