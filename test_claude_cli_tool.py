#!/bin/bash

# Test with Claude CLI
echo "Testing with Claude CLI..."
CLAUDE_CLI_cmd <<'EOF'
sleep 5

# Check result
if [ $? -eq 0 ]; then
    echo "✅ Claude CLI works with MiniMax-M2.5!"
else
    echo "❌ Claude CLI failed"
    echo "Output:"
    cat /tmp/claude_cli_output.log
    exit $?
fi
