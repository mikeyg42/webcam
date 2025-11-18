# Security Camera System - Startup Guide

## Quick Start

### Starting All Services

Run the complete startup script:

```bash
./start-all.sh
```

This script will:
1. ✅ Check if Docker is running (starts it if needed)
2. ✅ Start Docker Compose services (PostgreSQL, MinIO, ion-sfu)
3. ✅ Wait for all services to be healthy
4. ✅ Build the Go security camera application
5. ✅ Build the React frontend
6. ✅ Start the Node.js WebSocket proxy server
7. ✅ Start the Go security camera application
8. ✅ Display service URLs and PIDs
9. ✅ Tail logs to show realtime output

### Stopping All Services

```bash
./stop-all.sh
```

Or press `Ctrl+C` in the terminal running `start-all.sh`

## Service URLs

Once started, access the following services:

- **Frontend (Camera Stream & Config)**: http://localhost:8080
- **WebSocket Proxy**: http://localhost:3000
- **MinIO Console**: http://localhost:9001
  - Username: `minioadmin`
  - Password: `minioadmin`
- **PostgreSQL**: `localhost:5432`
  - Database: `recordings`
  - Username: `recorder`
  - Password: `recorder`

## Log Files

Logs are stored in the `logs/` directory:

- `logs/node-server.log` - Node.js WebSocket proxy
- `logs/go-camera.log` - Go security camera application

View logs in real-time:

```bash
tail -f logs/node-server.log logs/go-camera.log
```

## Manual Startup (Step by Step)

If you prefer to start services manually:

### 1. Start Docker Services

```bash
docker-compose up -d
```

Wait for services to be ready (check with `docker-compose ps`)

### 2. Build Applications

```bash
# Build Go application
go build -o security-camera ./cmd/security-camera

# Build frontend
cd frontend
npm run build
cd ..
```

### 3. Start Node.js Proxy

```bash
export ION_SFU_URL=ws://localhost:7001/ws
export PORT=3000
node server.js
```

### 4. Start Go Camera Application

In a new terminal:

```bash
export WEBRTC_USERNAME=testuser
export WEBRTC_PASSWORD=testing123
./security-camera -debug
```

## Troubleshooting

### Docker Not Starting

On macOS, if Docker fails to start automatically:

```bash
open -a Docker
```

Wait ~30 seconds for Docker to fully start.

### Port Already in Use

If you get "port already in use" errors:

```bash
# Find process using port 3000 (Node.js)
lsof -i :3000

# Find process using port 8080 (Go app)
lsof -i :8080

# Kill the process
kill -9 <PID>
```

### Camera Permission Issues

If no cameras are detected:

```bash
# Reset camera permissions (macOS)
tccutil reset Camera
```

Then restart the Go application - it will prompt for camera access.

### Docker Services Won't Start

Check Docker logs:

```bash
docker-compose logs postgres
docker-compose logs minio
docker-compose logs ion-sfu
```

Restart services:

```bash
docker-compose down
docker-compose up -d
```

### Frontend Not Loading

Ensure the frontend was built:

```bash
cd frontend
npm run build
cd ..
```

The frontend files are served from `public-new/` by the Go application.

## Development Mode

For faster development iteration:

### Frontend Development Server

```bash
cd frontend
npm run dev
```

This starts Vite dev server at http://localhost:5173 with hot reload.

### Backend with Auto-Reload

Install `air` for Go hot reload:

```bash
go install github.com/cosmtrek/air@latest
air
```

## Configuration

Edit `config.yaml` to change:
- Video/audio settings
- Camera/microphone device selection
- Motion detection parameters
- Recording settings
- Storage backends

Or use the web UI at http://localhost:8080 and navigate to the Configuration page.

## Testing Camera/Microphone Selection

1. Navigate to http://localhost:8080
2. Go to Configuration page
3. Select your preferred camera from the dropdown
4. Select your preferred microphone from the dropdown
5. Save configuration
6. Restart the application for changes to take effect

The system supports:
- Built-in webcam
- Continuity Camera (iPhone/iPad as webcam)
- External USB cameras
- External USB microphones
