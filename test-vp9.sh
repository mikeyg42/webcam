#!/bin/bash

# Quick VP9 testing script with automatic cleanup
set -e

echo "🔧 VP9 Test Script - Automatic Cleanup & Testing"

# Function to cleanup processes
cleanup() {
    echo "🧹 Cleaning up..."
    pkill -f "node server.js" || true
    pkill -f "./security-camera" || true
    docker stop ion-sfu-instance || true
    docker rm ion-sfu-instance || true
    sleep 2
    echo "✅ Cleanup complete"
}

# Trap cleanup on exit
trap cleanup EXIT

# Initial cleanup
cleanup

# Build latest version
echo "🔨 Building latest security-camera..."
go build ./cmd/security-camera

# Start servers
echo "🚀 Starting servers..."
./startServers.sh &
SERVER_PID=$!

# Wait for servers to be ready
echo "⏳ Waiting for servers to start..."
sleep 10

# Test VP9 implementation
echo "🎥 Testing VP9 streaming..."
if command -v gtimeout > /dev/null; then
    gtimeout 30s ./security-camera -debug || echo "🔍 Test completed (timeout expected)"
elif command -v timeout > /dev/null; then
    timeout 30s ./security-camera -debug || echo "🔍 Test completed (timeout expected)"
else
    ./security-camera -debug &
    CAMERA_PID=$!
    sleep 30
    kill $CAMERA_PID || true
    wait $CAMERA_PID 2>/dev/null || true
    echo "🔍 Test completed (timeout expected)"
fi

echo "✅ VP9 test cycle complete!"