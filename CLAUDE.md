# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a WebRTC-based security camera system with dual language architecture:
- **Go backend** (`cmd/security-camera/main.go`): Handles camera capture, motion detection, video recording, and WebRTC signaling
- **Node.js proxy server** (`server.js`): WebSocket proxy that manages rooms and forwards signaling to ion-sfu
- **Web frontend** (`public/`): Browser-based video streaming interface using ion-sdk-js

## Architecture

### Key Components

**Go Application (`cmd/security-camera/main.go`)**:
- `Application` struct: Main application coordinator with lifecycle management
- `FrameProducer`: Captures video frames using OpenCV (gocv)
- `RecordingManager`: Handles motion-triggered recording with cooldown periods
- `rtcManager.Manager` (`internal/rtcManager/manager.go`): WebRTC peer connection management and signaling

**Node.js Proxy (`server.js`)**:
- `RoomManager`: Manages WebSocket rooms for multiple concurrent sessions
- `Room` class: Handles ion-sfu connections per room with automatic reconnection
- WebSocket proxy that forwards signaling between clients and ion-sfu

**Internal Packages**:
- `internal/config/`: Configuration management with environment variable overrides
- `internal/motion/`: OpenCV-based motion detection using background subtraction
- `internal/video/`: Video recording functionality
- `internal/notification/`: Email notifications (currently disabled)

### Data Flow
1. Go app captures camera frames and detects motion
2. WebRTC streams are established through ion-sfu JSON-RPC signaling
3. Node.js server proxies WebSocket connections between browser clients and ion-sfu
4. Motion triggers video recording and notifications

## Development Commands

### Starting the System
```bash
# Start ion-sfu (Docker) and Node.js proxy server
./startServers.sh

# In separate terminal, start Go security camera
go run cmd/security-camera/main.go
```

### Go Development
```bash
# Build Go application
go build ./cmd/security-camera

# Run with debug mode
go run cmd/security-camera/main.go -debug

# Run with custom WebSocket address
go run cmd/security-camera/main.go -addr localhost:3000
```

### Node.js Development
```bash
# Install dependencies
npm install

# Start Node.js server directly
npm start
# or
node server.js

# Development with auto-reload (if nodemon installed)
nodemon server.js
```

### Dependencies
- **Go**: Requires OpenCV (gocv), WebRTC (Pion), WebSocket (Gorilla)
- **Docker**: ion-sfu runs in container (`pionwebrtc/ion-sfu:latest-jsonrpc`)
- **Node.js**: Express server with WebSocket proxy functionality

## Configuration

### Key Configuration Files
- `configs/sfu.toml`: ion-sfu configuration (TURN server, port ranges, timeouts)
- `internal/config/config.go`: Go application defaults with environment variable overrides

### Environment Variables
- `MAILSLURP_API_KEY`: Email notification API key
- `NOTIFICATION_EMAIL`: Email recipient for motion alerts
- `PORT`: Node.js server port (default: 3000)
- `ION_SFU_URL`: ion-sfu WebSocket URL (default: ws://localhost:7000/ws)

### TURN Server Configuration
- Embedded TURN server in ion-sfu container
- Default credentials: `pion=ion, pion2=ion2`
- UDP port range: 5000-5200 for WebRTC, 49152-65535 for TURN relay

## Testing and Development Notes

- Motion detection uses background subtraction with configurable thresholds
- Video recording has cooldown periods to prevent excessive file creation
- WebRTC connection includes automatic reconnection and health monitoring
- System supports multiple concurrent room-based video sessions
- All components use context-based cancellation for clean shutdown