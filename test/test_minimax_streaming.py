#!/usr/bin/env python3
"""
Test script to call MiniMax provider directly with streaming.
Logs raw responses to a log file for debugging.

Usage:
    python test/test_minimax_streaming.py

Environment:
    Load from .env-test-minimax file:
    - MINIMAX_API_KEY: MiniMax API key
    - MINIMAX_MODEL: Model to use (default: MiniMax-M2.5)
"""

import os
import sys
import json
import datetime
import requests
from pathlib import Path

# Configuration
SCRIPT_DIR = Path(__file__).parent
ROOT_DIR = SCRIPT_DIR.parent
LOG_FILE = ROOT_DIR / "logs" / "minimax_stream_test.log"

# Check both root .env-test-minimax and test/.env-test-minimax
ENV_FILE = ROOT_DIR / "test" / ".env-test-minimax"
if not ENV_FILE.exists():
    ENV_FILE = ROOT_DIR / ".env-test-minimax"

# Ensure logs directory exists
LOG_FILE.parent.mkdir(exist_ok=True)

def load_env():
    """Load environment variables from .env-test-minimax file"""
    env_vars = {}
    if ENV_FILE.exists():
        with open(ENV_FILE, 'r') as f:
            for line in f:
                line = line.strip()
                if line and not line.startswith('#'):
                    if '=' in line:
                        key, value = line.split('=', 1)
                        env_vars[key.strip()] = value.strip()
    
    # Also check for environment variables (they take precedence)
    for key in ['MINIMAX_API_KEY', 'MINIMAX_MODEL', 'MINIMAX_GROUP_ID']:
        if key in os.environ:
            env_vars[key] = os.environ[key]
    
    return env_vars

def log_message(log_fp, message: str):
    """Log message to both console and file"""
    timestamp = datetime.datetime.now().isoformat()
    formatted = f"[{timestamp}] {message}"
    print(formatted)
    log_fp.write(formatted + "\n")
    log_fp.flush()

def log_raw_data(log_fp, label: str, data: str):
    """Log raw data with label"""
    log_message(log_fp, f"\n{'='*60}")
    log_message(log_fp, f"RAW {label}:")
    log_message(log_fp, f"{'='*60}")
    log_message(log_fp, data)
    log_message(log_fp, f"{'='*60}\n")

def main():
    # Load environment
    env = load_env()
    api_key = env.get('MINIMAX_API_KEY', '')
    model = env.get('MINIMAX_MODEL', 'MiniMax-M2.1')
    
    if not api_key:
        print(f"ERROR: MINIMAX_API_KEY not found in {ENV_FILE}")
        print(f"Please create {ENV_FILE} with:")
        print(f"  MINIMAX_API_KEY=your_api_key_here")
        print(f"  MINIMAX_MODEL=MiniMax-M2.1")
        sys.exit(1)
    
    # Open log file
    with open(LOG_FILE, 'w') as log_fp:
        log_message(log_fp, "="*60)
        log_message(log_fp, "MiniMax Streaming Test")
        log_message(log_fp, f"Model: {model}")
        log_message(log_fp, f"API Key: {api_key[:20]}..." if len(api_key) > 20 else api_key)
        log_message(log_fp, "="*60)
        
        # MiniMax API endpoint
        # Note: MiniMax uses OpenAI-compatible API at api.minimax.io/v1
        # The GroupId should be passed as a query parameter
        group_id = env.get('MINIMAX_GROUP_ID', '')
        
        base_url = "https://api.minimax.io/v1"
        if group_id:
            endpoint = f"/chat/completions?GroupId={group_id}"
        else:
            endpoint = "/chat/completions"
        url = f"{base_url}{endpoint}"
        
        log_message(log_fp, f"\nRequest URL: {url}")
        
        # Define tools - at least 3 tools
        tools = [
            {
                "type": "function",
                "function": {
                    "name": "get_weather",
                    "description": "Get current weather for a location",
                    "parameters": {
                        "type": "object",
                        "properties": {
                            "location": {
                                "type": "string",
                                "description": "City name"
                            },
                            "unit": {
                                "type": "string",
                                "enum": ["celsius", "fahrenheit"],
                                "description": "Temperature unit"
                            }
                        },
                        "required": ["location"]
                    }
                }
            },
            {
                "type": "function",
                "function": {
                    "name": "search_code",
                    "description": "Search for code in a repository",
                    "parameters": {
                        "type": "object",
                        "properties": {
                            "query": {
                                "type": "string",
                                "description": "Search query"
                            },
                            "language": {
                                "type": "string",
                                "description": "Programming language"
                            }
                        },
                        "required": ["query"]
                    }
                }
            },
            {
                "type": "function",
                "function": {
                    "name": "calculate",
                    "description": "Perform a mathematical calculation",
                    "parameters": {
                        "type": "object",
                        "properties": {
                            "expression": {
                                "type": "string",
                                "description": "Mathematical expression to evaluate"
                            }
                        },
                        "required": ["expression"]
                    }
                }
            }
        ]
        
        # Request payload
        payload = {
            "model": model,
            "messages": [
                {
                    "role": "user",
                    "content": "Please do the following:\n1. Get weather for Tokyo\n2. Search for code about authentication in Python\n3. Calculate 123 * 456"
                }
            ],
            "tools": tools,
            "stream": True
        }
        
        # Log request
        log_message(log_fp, "\n--- REQUEST ---")
        log_message(log_fp, json.dumps(payload, indent=2))
        
        # Headers
        headers = {
            "Authorization": f"Bearer {api_key}",
            "Content-Type": "application/json"
        }
        
        log_message(log_fp, "\n--- HEADERS ---")
        log_message(log_fp, json.dumps({k: v for k, v in headers.items() if k != "Authorization"}, indent=2))
        log_message(log_fp, "Authorization: Bearer ***")
        
        # Make streaming request
        log_message(log_fp, "\n--- STARTING STREAMING REQUEST ---")
        
        try:
            response = requests.post(
                url,
                headers=headers,
                json=payload,
                stream=True,
                timeout=60
            )
            
            log_message(log_fp, f"\n--- RESPONSE STATUS ---")
            log_message(log_fp, f"Status Code: {response.status_code}")
            log_message(log_fp, f"Status Text: {response.reason}")
            log_message(log_fp, f"Headers: {dict(response.headers)}")
            
            # Log raw response headers
            log_raw_data(log_fp, "RESPONSE HEADERS", json.dumps(dict(response.headers), indent=2))
            
            if response.status_code != 200:
                error_text = response.text
                log_raw_data(log_fp, "ERROR RESPONSE", error_text)
                log_message(log_fp, f"\nERROR: Status {response.status_code}")
                log_message(log_fp, error_text)
                return
            
            # Process streaming response
            log_message(log_fp, "\n--- STREAMING RESPONSE ---")
            
            chunk_count = 0
            full_response = ""
            
            for line in response.iter_lines():
                if line:
                    line_str = line.decode('utf-8')
                    full_response += line_str + "\n"
                    chunk_count += 1
                    
                    # Log each chunk
                    log_message(log_fp, f"\n--- CHUNK {chunk_count} ---")
                    log_message(log_fp, line_str)
                    
                    # Also log parsed JSON if valid
                    if line_str.startswith('data: '):
                        data_str = line_str[6:]  # Remove 'data: ' prefix
                        if data_str.strip() and data_str.strip() != '[DONE]':
                            try:
                                parsed = json.loads(data_str)
                                log_message(log_fp, f"\n--- CHUNK {chunk_count} (PARSED) ---")
                                log_message(log_fp, json.dumps(parsed, indent=2))
                            except json.JSONDecodeError as e:
                                log_message(log_fp, f"Failed to parse JSON: {e}")
            
            # Log complete raw response
            log_raw_data(log_fp, "COMPLETE RESPONSE", full_response)
            
            log_message(log_fp, f"\n--- SUMMARY ---")
            log_message(log_fp, f"Total chunks received: {chunk_count}")
            log_message(log_fp, f"Log file: {LOG_FILE}")
            
        except requests.exceptions.Timeout:
            log_message(log_fp, "ERROR: Request timed out")
        except requests.exceptions.RequestException as e:
            log_message(log_fp, f"ERROR: Request failed: {e}")
            if hasattr(e, 'response') and e.response:
                log_raw_data(log_fp, "ERROR RESPONSE", e.response.text)
        except Exception as e:
            log_message(log_fp, f"ERROR: Unexpected error: {e}")
            import traceback
            log_message(log_fp, traceback.format_exc())
    
    print(f"\nLog file saved to: {LOG_FILE}")

if __name__ == "__main__":
    main()
