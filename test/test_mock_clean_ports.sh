#!/bin/bash

# Clean ports script for test_mock* tests
# Force kills all processes taking the specified ports
# Default: PROXY_PORT=4322, MOCK_PORT=4002 (for mock tests)

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Default ports for mock tests (can be overridden via arguments)
PROXY_PORT="${1:-4322}"
MOCK_PORT="${2:-4002}"

echo -e "${BLUE}======================================${NC}"
echo -e "${BLUE}     Clean Test Ports Utility        ${NC}"
echo -e "${BLUE}======================================${NC}"
echo -e ""
echo -e "${YELLOW}Proxy port: $PROXY_PORT${NC}"
echo -e "${YELLOW}Mock port: $MOCK_PORT${NC}"
echo -e ""

# Function to force kill processes on a port
kill_port() {
    local port=$1
    local pids
    
    # Get PIDs using the port
    pids=$(lsof -ti :$port 2>/dev/null)
    
    if [ -z "$pids" ]; then
        echo -e "  Port $port: ${GREEN}already free${NC}"
        return 0
    fi
    
    echo -e "  Port $port: ${YELLOW}killing PIDs: $pids${NC}"
    
    # Force kill directly (no graceful shutdown)
    echo "$pids" | xargs kill -9 2>/dev/null || true
    sleep 0.5
    
    # Verify
    pids=$(lsof -ti :$port 2>/dev/null)
    if [ ! -z "$pids" ]; then
        echo -e "  Port $port: ${RED}still in use, retrying...${NC}"
        echo "$pids" | xargs kill -9 2>/dev/null || true
    fi
    echo -e "  Port $port: ${GREEN}freed${NC}"
}

# Function to kill go processes by pattern
kill_go_processes() {
    local pattern=$1
    local name=$2
    local pids
    
    pids=$(pgrep -f "$pattern" 2>/dev/null)
    
    if [ -z "$pids" ]; then
        echo -e "  $name: ${GREEN}not running${NC}"
        return 0
    fi
    
    echo -e "  $name: ${YELLOW}killing PIDs: $pids${NC}"
    echo "$pids" | xargs kill -9 2>/dev/null || true
    echo -e "  $name: ${GREEN}killed${NC}"
}

echo -e "${YELLOW}[1/3] Killing processes on ports...${NC}"
kill_port $PROXY_PORT
kill_port $MOCK_PORT

echo -e ""
echo -e "${YELLOW}[2/3] Killing mock LLM processes...${NC}"
kill_go_processes "mock_llm.go" "mock_llm.go"
kill_go_processes "mock_llm_race.go" "mock_llm_race.go"
kill_go_processes "mock_llm_loop.go" "mock_llm_loop.go"

echo -e ""
echo -e "${YELLOW}[3/3] Killing proxy processes...${NC}"
kill_go_processes "cmd/main.go" "cmd/main.go"

# Final verification
echo -e ""
echo -e "${BLUE}======================================${NC}"
echo -e "${BLUE}           Verification              ${NC}"
echo -e "${BLUE}======================================${NC}"

all_clean=true
for port in $PROXY_PORT $MOCK_PORT; do
    if lsof -i :$port >/dev/null 2>&1; then
        echo -e "  Port $port: ${RED}STILL IN USE${NC}"
        lsof -i :$port 2>/dev/null
        all_clean=false
    else
        echo -e "  Port $port: ${GREEN}free${NC}"
    fi
done

echo -e ""
if [ "$all_clean" = true ]; then
    echo -e "${GREEN}All ports are clean!${NC}"
    exit 0
else
    echo -e "${RED}Some ports are still in use. You may need to kill processes manually.${NC}"
    exit 1
fi
