# Known Issues

## Recording Pipeline Status: WORKING

The recording system produces playable MKV files with AV1 video. All segments are independently playable.

### Architecture: ✅ VERIFIED WORKING
```
Camera → FrameDistributor → recordChannel → Pipeline.consumeRecordingFrames()
                                          → RecordingService.HandleFrame()
                                          → GStreamer SVT-AV1 encoder
                                          → MKV muxer (ebml-go)
                                          → Segments → MinIO upload
```
Recording is **network-independent** - frames flow directly from FrameDistributor, not WebRTC.

### What's Working
- ✅ Direct frame path from FrameDistributor
- ✅ AV1 encoding via GStreamer SVT-AV1
- ✅ MKV container muxing via ebml-go/webm
- ✅ 5-second segment rotation with keyframe forcing
- ✅ MinIO upload (segments appear in bucket)
- ✅ All segments play correctly (keyframe at each segment start)
- ✅ Disk space checks before recording
- ✅ Segment checksums (SHA256)
- ✅ Segment metadata saved to PostgreSQL

### What's NOT Implemented (Phase 2 & 3)
- ❌ Emergency buffers (fallback when primary buffer full)
- ❌ Recording watchdog (detect hangs/failures)
- ❌ Crash recovery for interrupted recordings
- ❌ Audio recording to MKV

---

## Recently Fixed (January 2025)

### PostgreSQL FK Constraint Error (HIGH - FIXED)
**Problem**: `pq: insert or update on table "segments" violates foreign key constraint "fk_segments_recording"`
**Cause**: SaveSegment was using incorrect column - FK references `recordings(external_id)` not `recordings(id)`
**Fix**: Use `segment.RecordingID` (external_id) directly in `metadata.go:SaveSegment()`

### Keyframe at Segment Rotation (HIGH - FIXED)
**Problem**: Subsequent segments (001+) wouldn't play - "Missing reference frame needed for show_existing_frame"
**Cause**: Segment rotation happened at arbitrary frame boundaries, not at keyframes
**Fix**: Added `ForceKeyframe()` to Encoder interface, called before `NewSegment()` in `recorder.go:checkSegmentRotation()`

### WriteFrame Race Condition (CRITICAL - FIXED)
**Problem**: ShouldRotate() saw 0 segments while WriteFrame() was creating one
**Cause**: RLock released before NewSegment() call, creating visibility window
**Fix**: Use write lock for atomic segment lookup/creation in `pipeline.go:219-243`

### OBU Parsing Bounds Check (HIGH - FIXED)
**Problem**: Potential integer overflow with malformed AV1 data
**Fix**: Added bounds validation before advancing offset in `isAV1Keyframe()` and `extractSequenceHeader()`

### Segment Cleanup Status (MEDIUM - FIXED)
**Problem**: "cannot cleanup segment in status completed" errors
**Fix**: Allow cleanup of `completed` and `failed` segments, not just `uploaded`

### Empty Sequence Header Validation (MEDIUM - FIXED)
**Problem**: Empty byte slice could be cached as sequence header
**Fix**: Changed `seqHdr != nil` to `len(seqHdr) > 0`

### Debug Printf Removal (LOW - FIXED)
**Problem**: Production code had debug print statements
**Fix**: Removed `fmt.Printf` calls, replaced with structured logging

---

## Medium: Email Notifications

**Status**: Intentionally disabled
**Config**: `"email": {"method": "disabled"}`
**Reason**: Not needed for current use case

---

## Low: Audio Recording

**Status**: Configurable but not integrated into MKV
**Notes**: Frontend has audio settings UI, backend supports microphone capture. Audio muxing into MKV not yet implemented.

---

## Resolved Issues

### Frame Buffer Full Errors (FIXED)
**Fix**: Increased buffer from 1s to 3s in `internal/recorder/recorder.go:123`

### JSON-RPC Health Check Failures (FIXED)
**Fix**: Disabled unsupported ping RPC in `internal/rtcManager/connectionHealth.go:665`
ion-sfu doesn't implement `ping` method; WebSocket ping/pong is sufficient.

### Config JSON Parsing (FIXED)
**Problem**: Duration values parsed incorrectly (5 minutes instead of 5 seconds)
**Cause**: YAML unmarshaler used for JSON files
**Fix**: Check file extension and use appropriate parser in `config.go`

---

## Troubleshooting

### WebRTC not connecting
1. `tailscale status` - Tailscale must be running
2. `docker ps | grep ion-sfu` - SFU container must be up
3. `curl http://localhost:3000/health` - Node proxy must respond

### Camera not found
macOS permissions issue. Grant in System Preferences > Privacy & Security > Camera. Restart app after granting.

### Recording produces small/empty segments
1. Check if continuous recording is enabled: `cat ~/.webcam2/config.json | jq '.recording.continuousEnabled'`
2. All segments should be playable with similar sizes
3. Check MinIO for uploads: `docker exec webcam2-minio mc ls local/recordings/ --recursive`

### Verify segment playback
```bash
# Download from MinIO
docker exec webcam2-minio mc cp local/recordings/continuous/2026-01-16/<recording-id>/segment_001.mkv /tmp/
docker cp webcam2-minio:/tmp/segment_001.mkv /tmp/test.mkv

# Test playback - all segments should decode without errors
ffmpeg -i /tmp/test.mkv -f null -
# Should show "frame= XXX" with no errors
```
