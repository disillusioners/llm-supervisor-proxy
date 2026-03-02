#!/usr/bin/env python3
import subprocess
import sys

# Simple test - just check if anthropic endpoint works
try:
    # Set environment variable
    os.environ['ANTHROPIC_BASE_URL'] = 'http://localhost:4321'
    
    # Run claude with a simple prompt
    result = subprocess.run(
        ['claude'],
        capture_output=True,
        text=True
    )
    
    print("✅ Claude CLI works!")
    print(result.stdout)
except Exception as e:
    print(f"Error: {e}")
    sys.exit(1)
