#!/usr/bin/env python3
"""
Test script to analyze proxy responses and identify why clients keep sending requests.
Tests the proxy at localhost:4321 with MiniMax-M2.5 model.
"""

import http.client
import json
import time
import sys
from datetime import datetime


def print_separator(title: str = ""):
    """Print a visual separator."""
    if title:
        print(f"\n{'='*60}")
        print(f" {title}")
        print(f"{'='*60}")
    else:
        print(f"{'='*60}")


def test_non_streaming_request(host: str, port: int, model: str, messages: list, api_key: str = None):
    """Test a non-streaming request and analyze the response."""
    print_separator("NON-STREAMING REQUEST")
    
    conn = http.client.HTTPConnection(host, port)
    
    payload = {
        "model": model,
        "messages": messages,
        "stream": False
    }
    
    headers = {
        "Content-Type": "application/json",
    }
    if api_key:
        headers["Authorization"] = f"Bearer {api_key}"
    
    start_time = time.time()
    
    try:
        conn.request("POST", "/v1/chat/completions", json.dumps(payload), headers)
        response = conn.getresponse()
        
        status = response.status
        response_headers = dict(response.getheaders())
        body = response.read().decode('utf-8')
        
        elapsed = time.time() - start_time
        
        print(f"Status: {status}")
        print(f"Elapsed: {elapsed:.3f}s")
        print(f"\nResponse Headers:")
        for k, v in response_headers.items():
            print(f"  {k}: {v}")
        
        print(f"\nResponse Body (raw):")
        print(body[:2000] if len(body) > 2000 else body)
        
        if len(body) > 2000:
            print(f"\n... (truncated, total {len(body)} bytes)")
        
        # Try to parse as JSON
        try:
            data = json.loads(body)
            print(f"\nParsed JSON structure:")
            print(f"  Keys: {list(data.keys())}")
            
            if 'choices' in data:
                print(f"  Choices count: {len(data['choices'])}")
                for i, choice in enumerate(data['choices']):
                    print(f"  Choice {i}:")
                    print(f"    finish_reason: {choice.get('finish_reason')}")
                    if 'message' in choice:
                        msg = choice['message']
                        print(f"    message.role: {msg.get('role')}")
                        content = msg.get('content', '')
                        print(f"    message.content length: {len(content)}")
                        print(f"    message.content preview: {content[:200]}...")
            
            if 'usage' in data:
                print(f"  Usage: {data['usage']}")
                
        except json.JSONDecodeError as e:
            print(f"\nFailed to parse JSON: {e}")
        
        return status, response_headers, body
        
    except Exception as e:
        print(f"ERROR: {e}")
        return None, None, str(e)
    finally:
        conn.close()


def test_streaming_request(host: str, port: int, model: str, messages: list, api_key: str = None):
    """Test a streaming request and analyze the SSE chunks."""
    print_separator("STREAMING REQUEST")
    
    conn = http.client.HTTPConnection(host, port)
    
    payload = {
        "model": model,
        "messages": messages,
        "stream": True
    }
    
    headers = {
        "Content-Type": "application/json",
        "Accept": "text/event-stream",
    }
    if api_key:
        headers["Authorization"] = f"Bearer {api_key}"
    
    start_time = time.time()
    chunks = []
    chunk_times = []
    
    try:
        conn.request("POST", "/v1/chat/completions", json.dumps(payload), headers)
        response = conn.getresponse()
        
        status = response.status
        response_headers = dict(response.getheaders())
        
        print(f"Status: {status}")
        print(f"Response Headers:")
        for k, v in response_headers.items():
            print(f"  {k}: {v}")
        print()
        
        # Read SSE stream
        buffer = ""
        while True:
            chunk = response.read(1024)
            if not chunk:
                break
            
            chunk_str = chunk.decode('utf-8')
            buffer += chunk_str
            
            # Process complete lines
            while '\n' in buffer:
                line, buffer = buffer.split('\n', 1)
                line = line.strip()
                
                if line:
                    chunk_times.append(time.time() - start_time)
                    chunks.append(line)
                    print(f"[{chunk_times[-1]:.3f}s] {line}")
        
        # Process any remaining buffer
        if buffer.strip():
            chunk_times.append(time.time() - start_time)
            chunks.append(buffer.strip())
            print(f"[{chunk_times[-1]:.3f}s] {buffer.strip()}")
        
        elapsed = time.time() - start_time
        
        print_separator("STREAM ANALYSIS")
        print(f"Total chunks: {len(chunks)}")
        print(f"Total time: {elapsed:.3f}s")
        
        # Analyze chunks
        data_chunks = [c for c in chunks if c.startswith('data:')]
        print(f"Data chunks: {len(data_chunks)}")
        
        # Check for [DONE] marker
        done_chunks = [c for c in chunks if '[DONE]' in c]
        print(f"Chunks with [DONE]: {len(done_chunks)}")
        
        if done_chunks:
            print(f"[DONE] chunks:")
            for c in done_chunks:
                print(f"  {c}")
        else:
            print("WARNING: No [DONE] marker found!")
        
        # Analyze finish_reason in chunks
        finish_reasons = []
        for c in data_chunks:
            try:
                data_str = c.replace('data: ', '', 1).strip()
                if data_str and data_str != '[DONE]':
                    data = json.loads(data_str)
                    if 'choices' in data:
                        for choice in data['choices']:
                            fr = choice.get('finish_reason')
                            if fr:
                                finish_reasons.append(fr)
            except json.JSONDecodeError:
                pass
        
        print(f"\nFinish reasons found: {finish_reasons}")
        
        # Check for any error indicators
        error_chunks = [c for c in chunks if 'error' in c.lower()]
        if error_chunks:
            print(f"\nERROR chunks found: {len(error_chunks)}")
            for c in error_chunks:
                print(f"  {c}")
        
        # Check timing gaps
        if len(chunk_times) > 1:
            gaps = [chunk_times[i+1] - chunk_times[i] for i in range(len(chunk_times)-1)]
            max_gap = max(gaps)
            avg_gap = sum(gaps) / len(gaps)
            print(f"\nTiming analysis:")
            print(f"  Max gap between chunks: {max_gap:.3f}s")
            print(f"  Avg gap between chunks: {avg_gap:.3f}s")
            
            if max_gap > 5:
                print(f"  WARNING: Large gap detected (>5s), could indicate timeout")
        
        return status, chunks, finish_reasons
        
    except Exception as e:
        print(f"ERROR: {e}")
        import traceback
        traceback.print_exc()
        return None, [], []
    finally:
        conn.close()


def test_multiple_requests(host: str, port: int, model: str, count: int = 3, delay: float = 1.0, api_key: str = None):
    """Send multiple requests to see if there's a pattern."""
    print_separator(f"MULTIPLE REQUESTS TEST ({count} requests)")
    
    messages = [
        {"role": "user", "content": "Say 'hello' and nothing else."}
    ]
    
    results = []
    for i in range(count):
        print(f"\n--- Request {i+1}/{count} ---")
        status, headers, body = test_non_streaming_request(host, port, model, messages, api_key)
        results.append({
            'request_num': i + 1,
            'status': status,
            'body_length': len(body) if body else 0,
        })
        
        if i < count - 1:
            print(f"\nWaiting {delay}s before next request...")
            time.sleep(delay)
    
    print_separator("RESULTS SUMMARY")
    for r in results:
        print(f"Request {r['request_num']}: status={r['status']}, body_length={r['body_length']}")


def analyze_response_completeness(host: str, port: int, model: str, api_key: str = None):
    """Analyze if responses are complete and well-formed."""
    print_separator("RESPONSE COMPLETENESS ANALYSIS")
    
    messages = [
        {"role": "user", "content": "Count from 1 to 5, one number per line."}
    ]
    
    # Test streaming
    print("\n=== Testing Streaming ===")
    status, chunks, finish_reasons = test_streaming_request(host, port, model, messages, api_key)
    
    issues = []
    
    # Check for issues
    if not any('[DONE]' in c for c in chunks):
        issues.append("Missing [DONE] marker in stream")
    
    if not finish_reasons:
        issues.append("No finish_reason found in any chunk")
    elif 'stop' not in finish_reasons:
        issues.append(f"Expected finish_reason='stop', got: {finish_reasons}")
    
    # Test non-streaming
    print("\n=== Testing Non-Streaming ===")
    status, headers, body = test_non_streaming_request(host, port, model, messages, api_key)
    
    try:
        data = json.loads(body)
        if 'choices' not in data:
            issues.append("Missing 'choices' in response")
        elif not data['choices']:
            issues.append("Empty 'choices' array")
        else:
            choice = data['choices'][0]
            if choice.get('finish_reason') != 'stop':
                issues.append(f"Non-streaming finish_reason: {choice.get('finish_reason')}")
    except json.JSONDecodeError:
        issues.append("Response is not valid JSON")
    
    print_separator("ISSUES FOUND")
    if issues:
        for issue in issues:
            print(f"  - {issue}")
    else:
        print("  No issues found - responses appear complete")
    
    return issues


def main():
    import os
    
    host = "localhost"
    port = 4321
    model = "MiniMax-M2.5"
    api_key = os.environ.get("API_KEY", "sk-a96c6864f2d86ee77ee33c6a2612d934ccb56236e2cc16edbe93bdbf350e3e26")
    
    # Parse command line args
    if len(sys.argv) > 1:
        if sys.argv[1] == "--stream":
            messages = [
                {"role": "user", "content": "Say 'hello world' and nothing else."}
            ]
            test_streaming_request(host, port, model, messages, api_key)
            return
        elif sys.argv[1] == "--multi":
            count = int(sys.argv[2]) if len(sys.argv) > 2 else 3
            test_multiple_requests(host, port, model, count, api_key=api_key)
            return
        elif sys.argv[1] == "--analyze":
            analyze_response_completeness(host, port, model, api_key)
            return
        elif sys.argv[1] == "--help":
            print("Usage: python test_proxy_requests.py [option]")
            print("Options:")
            print("  --stream    Test streaming request only")
            print("  --multi N   Send N requests (default 3)")
            print("  --analyze   Analyze response completeness")
            print("  --help      Show this help")
            print("\nDefault: Run all tests")
            print("\nEnvironment variables:")
            print("  API_KEY     API key for authentication")
            return
    
    # Default: run all tests
    messages = [
        {"role": "user", "content": "Say 'hello world' and nothing else."}
    ]
    
    print(f"Testing proxy at {host}:{port}")
    print(f"Model: {model}")
    print(f"Time: {datetime.now().isoformat()}")
    
    # Test non-streaming first
    test_non_streaming_request(host, port, model, messages, api_key)
    
    # Wait a bit
    time.sleep(1)
    
    # Test streaming
    test_streaming_request(host, port, model, messages, api_key)
    
    # Analyze completeness
    analyze_response_completeness(host, port, model, api_key)


if __name__ == "__main__":
    main()
