#!/usr/bin/env bash
# Security Camera System - Complete Startup Script

set -Eeuo pipefail

# ----------------------------
# Colors
# ----------------------------
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# ----------------------------
# Paths & Logs
# ----------------------------
PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$PROJECT_DIR"

LOG_DIR="$PROJECT_DIR/logs"
mkdir -p "$LOG_DIR"

echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}Security Camera System Startup${NC}"
echo -e "${BLUE}========================================${NC}"
echo ""

# ----------------------------
# Logging helpers
# ----------------------------
log_info() {  echo -e "${GREEN}[INFO]${NC} $*"; }
log_warn() {  echo -e "${YELLOW}[WARN]${NC} $*"; }
log_error(){  echo -e "${RED}[ERROR]${NC} $*"; }

# ----------------------------
# Preflight checks
# ----------------------------
need() {
  if ! command -v "$1" >/dev/null 2>&1; then
    log_error "required tool '$1' is not installed or not in PATH"
    exit 1
  fi
}

log_info "Checking required tools…"
need docker
need curl
need go
need node
need npm

# Prefer plugin 'docker compose' (new) over legacy 'docker-compose'
if ! docker compose version >/dev/null 2>&1; then
  log_error "This script requires 'docker compose' (plugin). Please install Docker Desktop (macOS) or Docker Engine + compose plugin."
  exit 1
fi

# ----------------------------
# Docker helpers
# ----------------------------
check_docker() { docker info >/dev/null 2>&1; }

start_docker() {
  log_info "Docker is not running. Attempting to start…"
  if [[ "$OSTYPE" == "darwin"* ]]; then
    open -a Docker || true
    log_info "Waiting for Docker Desktop…"
    local max_wait=120 waited=0
    while ! check_docker && (( waited < max_wait )); do
      sleep 2; waited=$((waited+2)); echo -n "."
    done
    echo ""
    check_docker || { log_error "Docker failed to start after ${max_wait}s"; exit 1; }
  elif command -v systemctl >/dev/null 2>&1; then
    sudo systemctl start docker || true
    local max_wait=60 waited=0
    while ! check_docker && (( waited < max_wait )); do
      sleep 2; waited=$((waited+2)); echo -n "."
    done
    echo ""
    check_docker || { log_error "Docker service failed to start after ${max_wait}s"; exit 1; }
  else
    log_error "Please start Docker manually."
    exit 1
  fi
  log_info "Docker is running."
}

wait_for_service() {
  local name="$1"
  local cmd="$2"
  local max_wait="${3:-60}"
  local waited=0

  log_info "Waiting for ${name}…"
  while ! bash -c "$cmd" >/dev/null 2>&1 && (( waited < max_wait )); do
    sleep 2; waited=$((waited+2)); echo -n "."
  done
  echo ""
  if (( waited >= max_wait )); then
    log_error "${name} failed to become ready after ${max_wait}s"
    return 1
  fi
  log_info "${name} is ready."
}

find_available_port() {
  local start_port="$1"
  local process_name="$2"
  local max_port=$((start_port + 10))

  local port=$start_port
  while (( port <= max_port )); do
    if ! lsof -ti:${port} >/dev/null 2>&1; then
      # Port is free
      echo "$port"
      return 0
    fi

    # Port is occupied - check what's using it
    local pids=$(lsof -ti:${port} 2>/dev/null)
    local is_our_process=false

    for pid in $pids; do
      local cmd=$(ps -p "$pid" -o command= 2>/dev/null || echo "")
      if [[ "$cmd" == *"$process_name"* ]]; then
        is_our_process=true
        log_warn "Port ${port} is occupied by our previous process (PID: ${pid}). Killing it…"
        kill -9 "$pid" 2>/dev/null || true
      fi
    done

    if $is_our_process; then
      # Wait for port to be released
      sleep 1
      if ! lsof -ti:${port} >/dev/null 2>&1; then
        echo "$port"
        return 0
      fi
    fi

    # Try next port
    log_warn "Port ${port} is in use by another process, trying ${port}+1…"
    port=$((port + 1))
  done

  log_error "Could not find available port between ${start_port}-${max_port}"
  return 1
}

cleanup() {
  log_warn "Shutting down app processes…"
  [[ -n "${NODE_PID:-}" ]] && kill "${NODE_PID}" 2>/dev/null || true
  [[ -n "${GO_PID:-}" ]] && kill "${GO_PID}" 2>/dev/null || true

  # Uncomment if you want containers to stop when the script exits:
  # log_warn "Stopping docker compose services…"
  # docker compose down -v
}
trap cleanup EXIT INT TERM

# ----------------------------
# Step 1: Ensure Docker daemon
# ----------------------------
log_info "Checking Docker status…"
check_docker || start_docker

# ----------------------------
# Step 2: Start compose services
# ----------------------------
# Support common compose filenames
COMPOSE_FILE=""
for f in docker-compose.yml docker-compose.yaml compose.yml compose.yaml; do
  if [[ -f "$f" ]]; then COMPOSE_FILE="$f"; break; fi
done
if [[ -z "$COMPOSE_FILE" ]]; then
  log_error "No compose file found (docker-compose.yml / compose.yml)."
  exit 1
fi

log_info "Starting Docker Compose services (file: $COMPOSE_FILE)…"
docker compose -f "$COMPOSE_FILE" up -d

# Resolve container IDs by service name if available; fallback to known names.
postgres_cid="$(docker compose -f "$COMPOSE_FILE" ps -q postgres || true)"
minio_cid="$(docker compose -f "$COMPOSE_FILE" ps -q minio || true)"
ionsfu_cid="$(docker compose -f "$COMPOSE_FILE" ps -q ion-sfu || true)"

# If service names differ in your compose file, these fallbacks may help:
[[ -z "$postgres_cid" ]] && postgres_cid="webcam2-postgres"
[[ -z "$ionsfu_cid"   ]] && ionsfu_cid="webcam2-ion-sfu"

# PostgreSQL readiness (pg_isready in container)
wait_for_service "PostgreSQL" "docker exec $postgres_cid pg_isready -U recorder -d recordings" 60

# MinIO readiness (HTTP liveness)
wait_for_service "MinIO" "curl -fsS http://localhost:9000/minio/health/live" 60

# Ion SFU readiness: check if container is running and healthy
# ion-sfu doesn't have a traditional health endpoint, so we just verify the container is running
# and give it a few extra seconds to initialize its WebSocket server on port 7001
log_info "Waiting for ion-sfu container to be running…"
sleep 5
if docker ps --format '{{.Names}}' | grep -q "webcam2-ion-sfu"; then
  log_info "ion-sfu is ready."
else
  log_error "ion-sfu container is not running. Check: docker compose logs ion-sfu"
  exit 1
fi

log_info "All Docker services are running."

# ----------------------------
# Step 3: Build Go app
# ----------------------------
log_info "Building Go application…"
GOOS=${GOOS:-} GOARCH=${GOARCH:-} go build -o security-camera ./cmd/security-camera
log_info "Go application built."

# ----------------------------
# Step 4: Build frontend
# ----------------------------
log_info "Building frontend…"
pushd frontend >/dev/null
# Fast path if node_modules exists; run install otherwise
if [[ ! -d node_modules ]]; then
  npm ci
fi
npm run build
popd >/dev/null
log_info "Frontend built."

# ----------------------------
# Step 5: Start Node.js WS proxy
# ----------------------------
log_info "Starting Node.js WebSocket proxy server…"
export ION_SFU_URL="${ION_SFU_URL:-ws://localhost:7001/ws}"

# Find available port for Node.js server
PORT=$(find_available_port 3000 "node server.js")
if [[ -z "$PORT" ]]; then
  log_error "Failed to find available port for Node.js server"
  exit 1
fi
export PORT
log_info "Node.js server will use port: ${PORT}"

node server.js > "$LOG_DIR/node-server.log" 2>&1 &
NODE_PID=$!
log_info "Node.js server PID: ${NODE_PID}"

wait_for_service "Node.js server" "curl -fsS http://localhost:${PORT}" 30

# ----------------------------
# Step 6: Start Go camera app
# ----------------------------
log_info "Starting Go security camera application…"
export WEBRTC_USERNAME="${WEBRTC_USERNAME:-testuser}"
export WEBRTC_PASSWORD="${WEBRTC_PASSWORD:-testing123}"

# Check port 8080 for Go app (note: port is hardcoded in the app)
# If it's occupied by our previous security-camera process, kill it
# Otherwise, warn and exit (since we can't change the port easily)
if lsof -ti:8080 >/dev/null 2>&1; then
  local pids=$(lsof -ti:8080 2>/dev/null)
  local killed=false

  for pid in $pids; do
    local cmd=$(ps -p "$pid" -o command= 2>/dev/null || echo "")
    if [[ "$cmd" == *"security-camera"* ]]; then
      log_warn "Port 8080 occupied by previous security-camera (PID: ${pid}). Killing it…"
      kill -9 "$pid" 2>/dev/null || true
      killed=true
    fi
  done

  if $killed; then
    sleep 1
  else
    log_error "Port 8080 is in use by another process. The Go app requires port 8080."
    log_error "Please free port 8080 or modify the Go app to use a different port."
    exit 1
  fi
fi

./security-camera -debug > "$LOG_DIR/go-camera.log" 2>&1 &
GO_PID=$!
log_info "Go camera PID: ${GO_PID}"

sleep 2
kill -0 "$NODE_PID" >/dev/null 2>&1 || { log_error "Node server exited. See $LOG_DIR/node-server.log"; exit 1; }
kill -0 "$GO_PID"   >/dev/null 2>&1 || { log_error "Go app exited. See $LOG_DIR/go-camera.log"; tail -20 "$LOG_DIR/go-camera.log" || true; exit 1; }

# ----------------------------
# Step 7: Summary
# ----------------------------
echo ""
echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}All services started successfully!${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""
echo -e "${BLUE}Service URLs (for external access):${NC}"
echo -e "  ${GREEN}Frontend:          http://localhost:8080${NC}"
echo -e "  ${GREEN}WebSocket Proxy:   http://localhost:${PORT}${NC}  ${YELLOW}<-- Node.js proxy${NC}"
echo -e "  ${GREEN}MinIO Console:     http://localhost:9001${NC}"
echo -e "  ${GREEN}PostgreSQL:        localhost:5432${NC}"
echo ""
echo -e "${YELLOW}NOTE: If connecting from another device, use these ports:${NC}"
echo -e "  ${YELLOW}Frontend:   8080${NC}"
echo -e "  ${YELLOW}WS Proxy:   ${PORT}${NC}"
echo ""
echo -e "${BLUE}Docker Services:${NC}"
docker compose -f "$COMPOSE_FILE" ps
echo ""
echo -e "${BLUE}Application PIDs:${NC}"
echo -e "  Node.js Server:    ${GREEN}$NODE_PID${NC}"
echo -e "  Go Camera App:     ${GREEN}$GO_PID${NC}"
echo ""
echo -e "${BLUE}Log Files:${NC}"
echo -e "  Node.js:           $LOG_DIR/node-server.log"
echo -e "  Go Camera:         $LOG_DIR/go-camera.log"
echo ""
echo -e "${YELLOW}Press Ctrl+C to stop app processes (containers will remain running).${NC}"
echo ""

# Keep printing the app logs
tail -f "$LOG_DIR/node-server.log" "$LOG_DIR/go-camera.log"