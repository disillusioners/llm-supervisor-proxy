#!/usr/bin/env python3
"""
Test script for Anthropic endpoint using the official Anthropic Python SDK.
Tests the /v1/messages endpoint with a high-complexity task.
"""

import os
import sys
import json
from anthropic import Anthropic

# Configuration
PROXY_URL = os.environ.get("PROXY_URL", "http://localhost:4321")
API_KEY = os.environ.get("API_KEY", "sk-a96c6864f2d86ee77ee33c6a2612d934ccb56236e2cc16edbe93bdbf350e3e26")
# Use configured internal models (GLM-5 or MiniMax-M2.5)
MODEL = os.environ.get("MODEL", "glm-5")

# The complex task: Review the project features
PROJECT_CONTEXT = """
# LLM Supervisor Proxy Project

A lightweight sidecar proxy designed to sit between autonomous agents and LLM providers. 
It detects "zombie" requests where the LLM stops generating tokens mid-stream and automatically retries them.

## Key Features:
1. **Heartbeat Monitoring**: Detects if the token stream hangs for more than IDLE_TIMEOUT (default: 60s).
2. **Multi-Strategy Auto-Retry**:
   - Idle Reset: Retries when a stream hangs mid-generation.
   - Upstream Recovery: Retries on 5xx errors or connectivity issues.
   - Generation Guard: Ensures requests finish within MAX_GENERATION_TIME.
3. **Loop Detection**: Detects repetitive patterns (identical responses, similar content, repeated tool calls, circular workflows, stagnating progress).
4. **Model Fallback Chains**: Automatically switches to fallback model if primary fails.
5. **Smart Resume**: When retrying, appends partial generation to prompt asking LLM to continue.
6. **Web UI Dashboard**: Real-time monitoring of requests, event logs, and configuration.
7. **Streaming Passthrough**: Fully supports Server-Sent Events (SSE) for real-time token streaming.

## Loop Detection Strategies:
- Exact Match: Identical consecutive messages
- Similarity: Near-identical messages via SimHash fingerprints
- Action Pattern: Repeated tool calls or A↔B oscillations
- Cycle: Circular action workflows (A→B→C→A→B→C)
- Thinking: Repetitive reasoning patterns via trigram analysis
- Stagnation: No meaningful progress despite continued output

## Architecture:
- Go backend with Preact frontend
- SQLite (dev) or PostgreSQL (production) for persistence
- Translates Anthropic API format to OpenAI format for upstream
"""

def test_non_streaming():
    """Test non-streaming request with complex task."""
    print("=" * 60)
    print("TEST 1: Non-streaming Anthropic request (high complexity)")
    print("=" * 60)
    print(f"Proxy URL: {PROXY_URL}")
    print(f"Model: {MODEL}")
    print()
    
    client = Anthropic(
        api_key=API_KEY,
        base_url=PROXY_URL,
    )
    
    try:
        message = client.messages.create(
            model=MODEL,
            max_tokens=4096,
            messages=[
                {
                    "role": "user",
                    "content": f"""Please provide a comprehensive review and analysis of this LLM Supervisor Proxy project. 

{PROJECT_CONTEXT}

Your review should include:
1. **Architecture Assessment**: Evaluate the overall design decisions
2. **Feature Analysis**: Deep dive into each major feature and its implementation approach
3. **Strengths**: What does this project do particularly well?
4. **Potential Improvements**: What could be enhanced or added?
5. **Use Case Fit**: What scenarios is this best suited for?
6. **Technical Recommendations**: Any architectural or implementation suggestions?

Be thorough and specific in your analysis."""
                }
            ]
        )
        
        print("Response received successfully!")
        print("-" * 40)
        print(f"ID: {message.id}")
        print(f"Model: {message.model}")
        print(f"Role: {message.role}")
        print(f"Stop Reason: {message.stop_reason}")
        print(f"Usage: {message.usage}")
        print("-" * 40)
        print("Content:")
        print()
        
        for block in message.content:
            if hasattr(block, 'text'):
                print(block.text)
        
        print()
        print("=" * 60)
        print("TEST 1: PASSED")
        print("=" * 60)
        return True
        
    except Exception as e:
        print(f"ERROR: {type(e).__name__}: {e}")
        print("=" * 60)
        print("TEST 1: FAILED")
        print("=" * 60)
        return False


def test_streaming():
    """Test streaming request with complex task."""
    print()
    print("=" * 60)
    print("TEST 2: Streaming Anthropic request (high complexity)")
    print("=" * 60)
    print(f"Proxy URL: {PROXY_URL}")
    print(f"Model: {MODEL}")
    print()
    
    client = Anthropic(
        api_key=API_KEY,
        base_url=PROXY_URL,
    )
    
    try:
        with client.messages.stream(
            model=MODEL,
            max_tokens=4096,
            messages=[
                {
                    "role": "user",
                    "content": f"""Provide a detailed technical review of the LLM Supervisor Proxy project.

{PROJECT_CONTEXT}

Focus on:
1. The retry mechanism and its robustness
2. Loop detection algorithm effectiveness
3. Streaming implementation quality
4. Error handling approaches

Be specific and technical."""
                }
            ]
        ) as stream:
            print("Stream started successfully!")
            print("-" * 40)
            print("Streaming content:")
            print()
            
            for text in stream.text_stream:
                print(text, end="", flush=True)
            
            print()
            print("-" * 40)
            
            # Get final message
            final_message = stream.get_final_message()
            print(f"ID: {final_message.id}")
            print(f"Stop Reason: {final_message.stop_reason}")
            print(f"Usage: {final_message.usage}")
        
        print()
        print("=" * 60)
        print("TEST 2: PASSED")
        print("=" * 60)
        return True
        
    except Exception as e:
        print(f"ERROR: {type(e).__name__}: {e}")
        import traceback
        traceback.print_exc()
        print("=" * 60)
        print("TEST 2: FAILED")
        print("=" * 60)
        return False


def test_simple_request():
    """Test simple request first to verify connectivity."""
    print()
    print("=" * 60)
    print("TEST 0: Simple connectivity test")
    print("=" * 60)
    
    client = Anthropic(
        api_key=API_KEY,
        base_url=PROXY_URL,
    )
    
    try:
        message = client.messages.create(
            model=MODEL,
            max_tokens=100,
            messages=[
                {
                    "role": "user",
                    "content": "Say 'Hello, the connection works!' in exactly those words."
                }
            ]
        )
        
        print(f"Response: {[b.text for b in message.content if hasattr(b, 'text')]}")
        print("TEST 0: PASSED")
        return True
        
    except Exception as e:
        print(f"ERROR: {type(e).__name__}: {e}")
        print("TEST 0: FAILED")
        return False


if __name__ == "__main__":
    print("Anthropic Endpoint Test Suite")
    print("=" * 60)
    
    results = []
    
    # Run simple test first
    results.append(("Connectivity", test_simple_request()))
    
    if results[0][1]:  # Only continue if connectivity works
        results.append(("Non-streaming", test_non_streaming()))
        results.append(("Streaming", test_streaming()))
    
    # Summary
    print()
    print("=" * 60)
    print("SUMMARY")
    print("=" * 60)
    for name, passed in results:
        status = "PASSED" if passed else "FAILED"
        print(f"  {name}: {status}")
    
    all_passed = all(r[1] for r in results)
    print()
    print(f"Overall: {'ALL TESTS PASSED' if all_passed else 'SOME TESTS FAILED'}")
    
    sys.exit(0 if all_passed else 1)
