#!/bin/bash

# Security Camera System - Shutdown Script
# This script stops all running services

# Color output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Project root directory
PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$PROJECT_DIR"

echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}Security Camera System Shutdown${NC}"
echo -e "${BLUE}========================================${NC}"
echo ""

# Function to print status messages
log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

# Step 1: Stop Node.js server
log_info "Stopping Node.js WebSocket proxy server..."
pkill -f "node server.js" && log_info "Node.js server stopped" || log_warn "Node.js server not running"

# Step 2: Stop Go camera application
log_info "Stopping Go security camera application..."
pkill -f "./security-camera" && log_info "Go camera application stopped" || log_warn "Go camera application not running"

# Step 3: Stop Docker Compose services
log_info "Stopping Docker Compose services..."
if [ -f "docker-compose.yml" ]; then
    docker-compose down
    log_info "Docker services stopped"
else
    log_warn "docker-compose.yml not found"
fi

echo ""
echo -e "${GREEN}All services stopped${NC}"
echo ""
