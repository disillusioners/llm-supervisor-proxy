#!/bin/bash

# Clean ports script for test_mock* tests
# Force kills all processes taking the specified ports
# Can be called directly or sourced by other scripts
#
# Usage:
#   ./test_mock_clean_ports.sh [PROXY_PORT] [MOCK_PORT]
#   source ./test_mock_clean_ports.sh [PROXY_PORT] [MOCK_PORT]
#
# Default: PROXY_PORT=4322, MOCK_PORT=4002

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

# Colors for output (only when called directly)
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    RED='\033[0;31m'
    GREEN='\033[0;32m'
    YELLOW='\033[1;33m'
    BLUE='\033[0;34m'
    NC='\033[0m'
else
    RED=''
    GREEN=''
    YELLOW=''
    BLUE=''
    NC=''
fi

# Default ports for mock tests (can be overridden via arguments)
CLEAN_PROXY_PORT="${1:-4322}"
CLEAN_MOCK_PORT="${2:-4001}"

# Function to force kill processes on a port
kill_port() {
    local port=$1
    local pids
    
    pids=$(lsof -ti :$port 2>/dev/null)
    
    if [ -z "$pids" ]; then
        echo -e "  Port $port: ${GREEN}already free${NC}"
        return 0
    fi
    
    echo -e "  Port $port: ${YELLOW}killing PIDs: $pids${NC}"
    echo "$pids" | xargs kill -9 2>/dev/null || true
    sleep 0.3
    
    # Verify and retry if needed
    pids=$(lsof -ti :$port 2>/dev/null)
    if [ ! -z "$pids" ]; then
        echo -e "  Port $port: ${RED}still in use, retrying...${NC}"
        echo "$pids" | xargs kill -9 2>/dev/null || true
        sleep 0.2
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
}

# Main cleanup function
clean_ports() {
    local proxy_port="${1:-$CLEAN_PROXY_PORT}"
    local mock_port="${2:-$CLEAN_MOCK_PORT}"
    
    echo -e "${BLUE}Cleaning ports (proxy=$proxy_port, mock=$mock_port)...${NC}"
    
    # Kill processes on ports
    kill_port $proxy_port
    kill_port $mock_port
    
    # Kill mock LLM processes
    kill_go_processes "mock_llm.go" "mock_llm.go"
    kill_go_processes "mock_llm_race.go" "mock_llm_race.go"
    kill_go_processes "mock_llm_loop.go" "mock_llm_loop.go"
    kill_go_processes "mock_llm_openai.go" "mock_llm_openai.go"
    
    # Kill proxy processes
    kill_go_processes "cmd/main.go" "cmd/main.go"
    
    echo -e "${GREEN}Port cleanup complete${NC}"
}

# If called directly (not sourced), run the cleanup
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    echo -e "${BLUE}======================================${NC}"
    echo -e "${BLUE}     Clean Test Ports Utility        ${NC}"
    echo -e "${BLUE}======================================${NC}"
    echo -e ""
    echo -e "${YELLOW}Proxy port: $CLEAN_PROXY_PORT${NC}"
    echo -e "${YELLOW}Mock port: $CLEAN_MOCK_PORT${NC}"
    echo -e ""
    
    clean_ports "$CLEAN_PROXY_PORT" "$CLEAN_MOCK_PORT"
    
    # Final verification
    echo -e ""
    echo -e "${BLUE}======================================${NC}"
    echo -e "${BLUE}           Verification              ${NC}"
    echo -e "${BLUE}======================================${NC}"
    
    all_clean=true
    for port in $CLEAN_PROXY_PORT $CLEAN_MOCK_PORT; do
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
        echo -e "${RED}Some ports are still in use.${NC}"
        exit 1
    fi
fi
