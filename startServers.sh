#!/bin/bash

# Function to cleanup background processes on exit
cleanup() {
    echo "Cleaning up..."
    # Check if docker is running before trying docker commands
    if docker info >/dev/null 2>&1; then
        # Stop and remove the container if it exists
        docker ps -q --filter ancestor=pionwebrtc/ion-sfu:latest-jsonrpc --filter name=ion-sfu-instance | xargs -r docker stop
        docker ps -a -q --filter ancestor=pionwebrtc/ion-sfu:latest-jsonrpc --filter name=ion-sfu-instance | xargs -r docker rm
    else
        echo "Docker daemon not running, skipping Docker cleanup."
    fi
    # Attempt to kill background jobs gracefully
    kill $(jobs -p) 2>/dev/null || true
    exit
}

# Set up cleanup on script exit
trap cleanup EXIT INT TERM

# Check if docker daemon is running
if ! docker info >/dev/null 2>&1; then
    echo "Docker isn't running. Please start Docker Desktop first."
    exit 1
fi

# Clean up any existing containers specifically named 'ion-sfu-instance'
echo "Checking for existing ion-sfu container..."
if docker ps -a --filter name=ion-sfu-instance | grep -q 'ion-sfu-instance'; then
    echo "Cleaning up existing ion-sfu container..."
    docker rm -f ion-sfu-instance
fi

# Start ion-sfu with error handling and a specific container name
echo "Starting ion-sfu server..."
# Note: --platform=linux/amd64 is often needed on ARM-based Macs (Apple Silicon)
# to run x86-64 Docker images like ion-sfu using Rosetta 2 emulation.
if ! docker run --platform=linux/amd64 \
    --name ion-sfu-instance \
    -d --add-host=host.docker.internal:host-gateway \
    -p 7001:7000 -p 5000-5200:5000-5200/udp \
    -p 3480:3478/udp \
    -v $(pwd)/configs/sfu.toml:/configs/sfu.toml \
    pionwebrtc/ion-sfu:latest-jsonrpc; then
    echo "Failed to start ion-sfu container. Please check docker logs (docker logs ion-sfu-instance) for details."
    echo "You may need to run 'docker system prune' to clean up docker resources."
    exit 1
fi

# Wait for ion-sfu JSON-RPC endpoint to be ready
echo "Waiting for ion-sfu JSON-RPC (ws://localhost:7001/ws) to be ready..."
timeout=30
elapsed=0
# We can't directly curl a WebSocket, so we'll check the HTTP endpoint if available
# or just wait a fixed time as a fallback. ion-sfu doesn't have a simple HTTP health check by default.
# We'll rely on the Node.js server's ability to connect/reconnect.
# A simple sleep is less reliable but avoids adding complex checks here.
sleep 5 # Give it a few seconds to initialize

# Check if the container is still running after the wait
if ! docker ps --filter name=ion-sfu-instance --filter status=running | grep -q 'ion-sfu-instance'; then
    echo "Error: ion-sfu container failed to stay running."
    docker logs ion-sfu-instance | tail -n 20 # Show recent logs
    exit 1
fi
echo "ion-sfu container started. Node.js server will attempt to connect."


# Start Node.js server with nodemon for development
echo "Starting Node.js server..."
export ION_SFU_URL=ws://localhost:7001/ws
if command -v nodemon &> /dev/null; then
    nodemon server.js
else
    node server.js
fi
