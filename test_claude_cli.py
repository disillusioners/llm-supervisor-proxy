#!/usr/bin/env python3
"""
Test script to simulate Claude CLI behavior
"""
import os
import sys
from anthropic import Anthropic

# Configuration
PROXY_URL = os.environ.get("PROXY_URL", "http://localhost:4321")
API_KEY = os.environ.get("API_KEY", "sk-test")
MODEL = os.environ.get("MODEL", "glm-5")

def test_claude_cli():
    print("=== Simulating Claude CLI behavior ===")
    print(f"Proxy URL: {PROXY_URL}")
    print(f"Model: {MODEL}")
    
    client = Anthropic(
        api_key=API_KEY,
        base_url=PROXY_URL,
    )
    
    # Test 1: Simple request with system message (like Claude CLI sends)
    print("\n=== Test 1: Simple request with system ===")
    
    try:
        response = client.messages.create(
            model=MODEL,
            max_tokens=100,
            system=[
                {
                    "type": "text",
                    "text": "You are a helpful AI assistant. Be concise."
                }
            ],
            messages=[
                {"role": "user", "content": "What is the main language of this project?"}
            ]
        )
        
        print(f"\nResponse ID: {response.id}")
        print(f"Model: {response.model}")
        print(f"Content: {response.content}")
        print(f"Usage: {response.usage}")
        print("Test 1: PASSED")
        
    except Exception as e:
        print(f"\nTest 1: FAILED: {e}")
        import traceback
        traceback.print_exc()
        sys.exit(1)
    
    # Test 2: Multi-turn conversation (simulates conversation history)
    print("\n=== Test 2: Multi-turn conversation ===")
    
    try:
        response = client.messages.create(
            model=MODEL,
            max_tokens=100,
            system=[
                {
                    "type": "text",
                    "text": "You are a helpful AI assistant. Be concise."
                }
            ],
            messages=[
                {"role": "user", "content": "What is the main language of this project?"},
                {"role": "assistant", "content": "The main language is Go."},
                {"role": "user", "content": "What features does it have?"}
            ]
        )
        
        print(f"\nResponse ID: {response.id}")
        print(f"Model: {response.model}")
        print(f"Content: {response.content}")
        print(f"Usage: {response.usage}")
        print("Test 2: PASSED")
        
    except Exception as e:
        print(f"\nTest 2: FAILED: {e}")
        import traceback
        traceback.print_exc()
        sys.exit(1)
    
    # Test 3: Streaming request
    print("\n=== Test 3: Streaming request ===")
    
    try:
        with client.messages.stream(
            model=MODEL,
            max_tokens=100,
            system=[
                {
                    "type": "text",
                    "text": "You are a helpful AI assistant. Be concise."
                }
            ],
            messages=[
                {"role": "user", "content": "What is the main language of this project?"}
            ]
        ) as stream:
            full_text = ""
            for text in stream.text_stream:
                full_text += text
        
        print(f"\nFull response: {full_text[:200]}...")
        print(f"\nFinal message ID: {stream.get_final_message().id}")
        print("Test 3: PASSED")
        
    except Exception as e:
        print(f"\nTest 3: FAILED: {e}")
        import traceback
        traceback.print_exc()
        sys.exit(1)
    
    print("\n=== ALL TESTS PASSED ===")

if __name__ == "__main__":
    test_claude_cli()
