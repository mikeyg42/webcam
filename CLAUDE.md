# CLAUDE.md

## Core Purpose

**Persist all motion-triggered events (or all events per user config) with full-quality recordings.**

WebRTC streaming latency up to 20 seconds is acceptable. Dropped frames or reduced resolution in persisted recordings is NOT acceptable - this is a security camera system where evidence capture is paramount.

## Project Summary

WebRTC security camera: Go backend captures video/audio, streams via ion-sfu, React frontend displays. Uses Tailscale for networking (NO TURN servers). Live streaming works; recording pipeline partially working (first segments playable, keyframe rotation WIP).

## Quick Start

```bash
./start-all.sh   # Starts everything: Docker (ion-sfu, postgres, minio) + Node proxy + Go app
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
                                          → WebRTC Track → ion-sfu → Browser
```

### Key Components
- **Motion Detector** (`internal/motion/detector.go`): Background subtraction via OpenCV/gocv. Configurable sensitivity and cooldown.
- **Recording Service** (`internal/recorder/`): AV1 encoding via GStreamer SVT-AV1, MKV muxing via ebml-go. First segments playable; keyframe forcing at rotation still needed.
- **Audio** (`internal/` + frontend): Microphone capture supported, configurable in GUI. Not yet muxed into MKV recordings.

### Key Files
| Purpose | File |
|---------|------|
| App entry | `cmd/security-camera/main.go` |
| WebRTC management | `internal/rtcManager/manager.go` |
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
| ion-sfu config | `configs/sfu.toml` |

## Constraints (Non-Negotiable)

1. **Tailscale ONLY** - No TURN/STUN. All WebRTC uses Tailscale mesh.
2. **ion-sfu** - SFU already integrated and working
3. **macOS primary** - VideoToolbox encoding, AVFoundation permissions
4. **Recording quality** - Full resolution, no dropped frames in persisted files
5. **gocv/OpenCV** - Required for motion detection

## Decisions Already Made

- ion-sfu (not Janus, Mediasoup)
- Tailscale (all TURN code removed in commit b3cbadd)
- H.264 via GStreamer VideoToolbox for WebRTC streaming
- AV1 via GStreamer SVT-AV1 for recordings (high quality, smaller files)
- MKV container via ebml-go for recordings
- React frontend (not vanilla JS)
- PostgreSQL + MinIO for storage
- AES-256-GCM for credential encryption

## Status & Issues

- **Current status**: `FUNCTIONALITY_STATUS.md`
- **Known issues & fixes needed**: `KNOWN_ISSUES.md`
- **Detailed recording fix plan**: `todo_goals.md`

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
- Logging: structured with component prefixes `[Pipeline]`, `[WebRTC]`, `[Motion]`
- Use context.Context for cancellation throughout
