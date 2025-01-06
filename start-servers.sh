!/bin/bash

# Function to cleanup background processes on exit
cleanup() {
    echo "Cleaning up..."
    docker stop ion-sfu 2>/dev/null
    kill $(jobs -p)
    exit
}

# Set up cleanup on script exit
trap cleanup EXIT INT TERM

# Start ion-sfu
echo "Starting ion-sfu server..."
docker run --name ion-sfu -d \
  -p 7000:7000 -p 5000-5200:5000-5200/udp \
  -v $(pwd)/configs/sfu.toml:/configs/sfu.toml \
  pionwebrtc/ion-sfu:latest-jsonrpc

# Wait for ion-sfu to be ready
echo "Waiting for ion-sfu to be ready..."
until curl -s http://localhost:7000 > /dev/null; do
    sleep 1
done

# Start Node.js server with nodemon for development
echo "Starting Node.js server..."
if command -v nodemon &> /dev/null; then
    nodemon server.js
else
    node server.js
fi