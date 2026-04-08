# CLAUDE.md

## Core Purpose

**Persist all motion-triggered events (or all events per user config) with full-quality recordings.**

WebRTC streaming latency up to 20 seconds is acceptable. Dropped frames or reduced resolution in persisted recordings is NOT acceptable - this is a security camera system where evidence capture is paramount.

## Project Summary

WebRTC security camera: Go backend captures video/audio, publishes to LiveKit SFU via livekit server-sdk-go, React frontend subscribes via livekit-client. Uses Tailscale for networking (NO TURN servers). Recording pipeline uses separate AV1 path (not WebRTC).

## Quick Start

```bash
./start-all.sh   # Starts everything: Docker (postgres, minio) + livekit-server + Node proxy + Go app
./stop-all.sh    # Clean shutdown
```

## Frontend Workflow

Three-tab wizard flow with progressive unlocking:

1. **Configuration** (always available)
   - Video: resolution, framerate, camera selection
   - Audio: enable/disable, microphone selection, sample rate
   - Motion: sensitivity, cooldown, detection zones
   - Recording: format, storage location, retention
   - Storage: MinIO/PostgreSQL settings
   - Tailscale: node configuration

2. **Calibration** (unlocks after config saved)
   - 10-second recording of "empty scene"
   - Computes motion baseline (mean + stddev)
   - Sets threshold for motion detection
   - Must complete before live view

3. **Camera** (unlocks after calibration)
   - Live WebRTC stream view
   - Recording controls (start/stop)
   - Motion detection status indicator

## Architecture

### Data Flow
```
Camera → FrameProducer → FrameDistributor → motionChannel → Motion Detector → triggers recording
                                          → recordChannel → RecordingService → AV1 encoder → MKV segments → MinIO
                                          → GStreamer H.264 → RTP → LiveKit Publisher → livekit-server → Browser
```

### Key Components
- **Motion Detector** (`internal/motion/detector.go`): Background subtraction via OpenCV/gocv. Configurable sensitivity and cooldown.
- **Recording Service** (`internal/recorder/`): AV1 encoding via GStreamer SVT-AV1, MKV muxing via ebml-go. First segments playable; keyframe forcing at rotation still needed.
- **Audio** (`internal/` + frontend): Microphone capture supported, configurable in GUI. Opus audio muxed into MKV when enabled.

### Key Files
| Purpose | File |
|---------|------|
| App entry | `cmd/security-camera/main.go` |
| LiveKit publisher | `internal/livekitPublisher/publisher.go` |
| Frame distribution | `internal/integration/pipeline.go` |
| Recording | `internal/recorder/recorder.go` |
| Motion detection | `internal/motion/detector.go` |
| Calibration | `internal/calibration/service.go` |
| Node proxy | `server.js` |
| Frontend | `frontend/src/App.tsx` |

### Configuration Files
| Purpose | Location |
|---------|----------|
| Runtime config | `~/.webcam2/config.json` |
| Credentials DB | `~/.webcam2/credentials.db` |
| Docker services | `docker-compose.yml` |
| LiveKit config | `configs/livekit.yaml` |

## Constraints (Non-Negotiable)

1. **Tailscale ONLY** - No TURN/STUN. All WebRTC uses Tailscale mesh.
2. **LiveKit SFU** - livekit-server (pion v4 native), Go SDK for publishing, JS SDK for subscribing
3. **macOS primary** - VideoToolbox encoding, AVFoundation permissions
4. **Recording quality** - Full resolution, no dropped frames in persisted files
5. **gocv/OpenCV** - Required for motion detection

## Decisions Already Made

- LiveKit SFU (replaced ion-sfu due to pion v3/v4 incompatibility; not Janus, Mediasoup)
- Tailscale (all TURN code removed in commit b3cbadd)
- H.264 via GStreamer VideoToolbox for WebRTC streaming
- AV1 via GStreamer SVT-AV1 for recordings (high quality, smaller files)
- MKV container via ebml-go for recordings
- React frontend (not vanilla JS)
- PostgreSQL + MinIO for storage
- AES-256-GCM for credential encryption

## Agent & Skill Routing

When spawning subagents for tasks involving **WebRTC debugging**, **GStreamer pipelines**, or **AV1 encoding**, use `general-purpose` agent type (not `backend-developer`) so the agent has access to the project's specialized skills:

- `/webrtc-debug` - ICE failures, media quality, LiveKit issues
- `/gstreamer-debug` - Pipeline errors, caps negotiation, state changes
- `/av1-encoding` - Encoder settings, MKV muxing, quality tradeoffs
- `/web-interface-guidelines` - Accessibility, forms, animation, performance audits

The `backend-developer` agent has a limited toolset that excludes the Skill tool. For general Go/API work that doesn't need these skills, `backend-developer` is fine.

### Deploy-Time Web Accessibility Check

When the user mentions deploying or hosting the frontend (phrases like "let's deploy", "push to production", "see how it looks live", "deploy and test"):

1. **Run `/web-interface-guidelines` in the background** on `frontend/src/**/*.tsx` files
2. **Do NOT interrupt** the user's deploy flow - proceed with their request
3. **If issues are found**, silently add tasks via TaskCreate with subject prefixed `[urgent-web-accessibility]`
4. **After deploy**, briefly mention: "Added X web accessibility items to your task list" (only if items were added)

## Status & Issues

- **Current status**: `FUNCTIONALITY_STATUS.md`
- **Known issues & fixes needed**: `KNOWN_ISSUES.md`
- **Detailed recording fix plan**: `todo_goals.md`

### KNOWN_ISSUES.md Rules

This file is a **reference list only**. When editing it:
- **DO**: Add/remove issues with brief but thorough description
- **DO**: Mark items as FIXED with a short summary of the changes made
- **DO NOT**: Add implementation proposals, rationale, or "why this matters"
- **DO NOT**: Add "Next Priority" sections or recommendations

If asked to identify priorities or propose implementations, respond to the user directly - do not write that content into KNOWN_ISSUES.md.

## Development

```bash
# Build and run with debug
go build ./cmd/security-camera && ./security-camera -debug

# Headless testing mode (bypasses Tailscale auth)
WEBRTC_PASSWORD=testing123 WEBRTC_USERNAME=testuser ./security-camera -debug -headless -testing
```

## Code Style

- Comments explain HOW, not change history
- Error handling: return early, wrap with context
- Use context.Context for cancellation throughout

### Logging Standards

**All logs MUST be structured and written to stdout.** Never use `fmt.Println` or `fmt.Printf` for logging.

**Format:**
- Use `log.Printf()` with component prefixes: `[Pipeline]`, `[WebRTC]`, `[Motion]`, `[Recorder]`, `[Config]`, `[API]`
- Keep messages concise: `log.Printf("[Component] action: key=value")`
- Error logs include context: `log.Printf("[Component] operation failed: %v", err)`

**Debug guards:** Verbose logs should be behind `if debugMode { }`:
- SDP/codec negotiation details
- Periodic stats (RTCP, packet counts)
- Track/stream metadata dumps
- Individual NACK/PLI/FIR events
- Bandwidth/resolution calculations

**Never guard (production-critical):**
- Connection state changes (Connected, Disconnected, Failed)
- Errors and failures
- ICE/DTLS failures and restarts
- Panic recovery
- Shutdown/cleanup

```go
// Production log (always)
log.Printf("[WebRTC] ICE connection failed, initiating restart")

// Debug log (guarded)
if m.debugMode {
    log.Printf("[WebRTC] Negotiated video SSRC=%d, PT=%d", ssrc, pt)
}

// Bad - never use fmt for logging
fmt.Println("something happened")
```
