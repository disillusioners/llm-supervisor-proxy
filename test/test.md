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