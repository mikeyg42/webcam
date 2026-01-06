#!/usr/bin/env bash
# Security Camera System - Fast Backend Restart
# Only restarts the Go camera application to reload configuration

set -Eeuo pipefail

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Project root
PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$PROJECT_DIR"

LOG_DIR="$PROJECT_DIR/logs"
mkdir -p "$LOG_DIR"

log_info() {  echo -e "${GREEN}[INFO]${NC} $*"; }
log_warn() {  echo -e "${YELLOW}[WARN]${NC} $*"; }
log_error(){  echo -e "${RED}[ERROR]${NC} $*"; }

echo -e "${BLUE}=======================================${NC}"
echo -e "${BLUE}Fast Backend Restart (Config Reload)${NC}"
echo -e "${BLUE}=======================================${NC}"
echo ""

# Step 1: Stop Go camera application gracefully
log_info "Stopping Go security camera application..."
if pkill -f "./security-camera"; then
    log_info "Sent shutdown signal to Go camera application"
    # Wait for graceful shutdown (max 5 seconds)
    for i in {1..5}; do
        if ! pgrep -f "./security-camera" > /dev/null; then
            log_info "Go camera application stopped gracefully"
            break
        fi
        sleep 1
    done

    # Force kill if still running
    if pgrep -f "./security-camera" > /dev/null; then
        log_warn "Forcing shutdown..."
        pkill -9 -f "./security-camera" || true
        sleep 1
    fi

    # Additional wait for WebSocket cleanup in ion-sfu
    log_info "Waiting for WebSocket connection cleanup..."
    sleep 3
else
    log_warn "Go camera application was not running"
fi

# Step 2: Verify Docker dependencies are running
log_info "Checking Docker container dependencies..."

check_container() {
    local name="$1"
    local port="$2"
    local check_cmd="$3"

    if ! docker ps --format '{{.Names}}' | grep -q "$name"; then
        log_error "Docker container '$name' is not running!"
        log_error "Run './start-all.sh' first, or 'docker compose up -d'"
        return 1
    fi

    # Optional port check
    if [ -n "$check_cmd" ]; then
        if ! eval "$check_cmd" >/dev/null 2>&1; then
            log_warn "Container '$name' is running but service not ready on port $port"
        fi
    fi
    return 0
}

DOCKER_OK=true

# Check PostgreSQL
if ! check_container "webcam2-postgres" "5432" "docker exec webcam2-postgres pg_isready -U recorder -d recordings"; then
    DOCKER_OK=false
fi

# Check MinIO
if ! check_container "webcam2-minio" "9000" "curl -fsS http://localhost:9000/minio/health/live"; then
    DOCKER_OK=false
fi

# Check ion-sfu
if ! check_container "webcam2-ion-sfu" "7001" ""; then
    DOCKER_OK=false
fi

if [ "$DOCKER_OK" = false ]; then
    log_error "Docker dependencies not ready. Start them with: docker compose up -d"
    exit 1
fi
log_info "✓ Docker containers are running"

# Step 3a: Ensure ports are free (8080 for frontend, 8081 for API)
for port in 8080 8081; do
    if lsof -ti:$port >/dev/null 2>&1; then
        log_warn "Port $port still occupied, clearing..."
        kill -9 $(lsof -ti:$port) 2>/dev/null || true
        sleep 1
    fi
done

# Step 3b: Ensure Node.js WebSocket proxy is running
NODE_PORT="${PORT:-3000}"
log_info "Checking Node.js WebSocket proxy on port ${NODE_PORT}..."

if ! curl -fsS "http://localhost:${NODE_PORT}" >/dev/null 2>&1; then
    log_warn "Node.js proxy not responding on port ${NODE_PORT}, starting it..."

    # Kill any zombie processes on the port
    if lsof -ti:${NODE_PORT} >/dev/null 2>&1; then
        log_warn "Clearing port ${NODE_PORT}..."
        kill -9 $(lsof -ti:${NODE_PORT}) 2>/dev/null || true
        sleep 1
    fi

    # Start Node.js server
    export ION_SFU_URL="${ION_SFU_URL:-ws://localhost:7001/ws}"
    export PORT="${NODE_PORT}"
    node server.js > "$LOG_DIR/node-server.log" 2>&1 &
    NODE_PID=$!
    log_info "Node.js server started with PID: ${NODE_PID}"

    # Wait for Node.js to be ready (max 15 seconds)
    for i in {1..15}; do
        if curl -fsS "http://localhost:${NODE_PORT}" >/dev/null 2>&1; then
            log_info "✓ Node.js proxy is ready"
            break
        fi
        sleep 1
        if [ $i -eq 15 ]; then
            log_error "Node.js proxy failed to start"
            tail -20 "$LOG_DIR/node-server.log" 2>/dev/null || echo "No logs available"
            exit 1
        fi
    done
else
    log_info "✓ Node.js proxy already running"
fi

# Step 4: Restart Go camera application
log_info "Starting Go security camera application..."

# Preserve environment variables if they were set
export WEBRTC_USERNAME="${WEBRTC_USERNAME:-testuser}"
export WEBRTC_PASSWORD="${WEBRTC_PASSWORD:-testing123}"

# Start the application in background
./security-camera -debug > "$LOG_DIR/go-camera.log" 2>&1 &
GO_PID=$!

log_info "Go camera started with PID: ${GO_PID}"

# Step 5: Verify it's running
sleep 2
if kill -0 "$GO_PID" 2>/dev/null; then
    log_info "✓ Go camera application restarted successfully"
    echo ""
    echo -e "${GREEN}Backend restart complete!${NC}"
    echo -e "New configuration loaded successfully."
    echo ""
    exit 0
else
    log_error "✗ Go camera application failed to start"
    echo ""
    echo -e "${RED}Recent logs:${NC}"
    tail -20 "$LOG_DIR/go-camera.log" 2>/dev/null || echo "No logs available"
    echo ""
    exit 1
fi
