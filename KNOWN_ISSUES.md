# Known Issues

## Recording Pipeline Status: PARTIALLY WORKING

The recording system produces playable MKV files with AV1 video. First segments work correctly; subsequent segments need keyframe forcing at rotation.

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
- ✅ 5-second segment rotation
- ✅ MinIO upload (segments appear in bucket)
- ✅ First segment (segment_000.mkv) plays correctly
- ✅ Disk space checks before recording
- ✅ Segment checksums (SHA256)

### Current Problems (Verified)

#### 1. PostgreSQL Foreign Key Constraint Error
**Symptom**: `pq: insert or update on table "segments" violates foreign key constraint "fk_segments_recording"`
**Cause**: Recording metadata not saved to DB before segments are uploaded
**Impact**: Segment metadata not tracked in database (upload still works)
**File**: `internal/recorder/recorder.go` - need to save recording on continuous start

#### 2. Subsequent Segments Missing Keyframes
**Symptom**: `ffprobe` shows "Missing reference frame needed for show_existing_frame"
**Cause**: Segment rotation happens at arbitrary frame boundaries, not at keyframes
**Impact**: segment_001.mkv and later won't play standalone
**Fix needed**: Force keyframe from encoder when starting new segment

### What's NOT Implemented (Phase 2 & 3)
- ❌ Keyframe forcing at segment rotation
- ❌ Emergency buffers (fallback when primary buffer full)
- ❌ Recording watchdog (detect hangs/failures)
- ❌ Crash recovery for interrupted recordings
- ❌ Audio recording to MKV

---

## Recently Fixed (January 2025)

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
2. First segment should be larger (~100KB+), subsequent segments may be small until keyframe fix is applied
3. Check MinIO for uploads: `docker exec webcam2-minio mc ls local/recordings/ --recursive`

### Verify segment playback
```bash
# Download from MinIO
docker exec webcam2-minio mc cp local/recordings/continuous/2026-01-13/<recording-id>/segment_000.mkv /tmp/
docker cp webcam2-minio:/tmp/segment_000.mkv /tmp/test.mkv

# Test playback
ffmpeg -i /tmp/test.mkv -f null -
# Should show "frame= XXX" with no errors for segment_000
```
