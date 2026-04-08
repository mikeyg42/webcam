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

### What's NOT Implemented
- ✅ Emergency buffers - IMPLEMENTED with full safety measures:
  - 20-second ring buffer fallback when primary channel full
  - 90% capacity rejection threshold to prevent memory exhaustion
  - Capacity check before read to prevent frame reordering
  - Adaptive drain rate (5/15/30 frames per tick based on fill level)
  - Proper shutdown drain with timeout handling

### Crash Resilience (IMPLEMENTED)
- ✅ Recording watchdog (detect hangs/failures)
- ✅ Crash recovery for interrupted recordings (orphaned files recovered on startup)
- ✅ Fsync durability (periodic fsync during recording, file + directory fsync before rename)
- ✅ Disk space monitoring (emergency/critical/warning thresholds)
- ✅ Panic recovery (best-effort finalization after panics)
- ✅ Monotonic segment index (indices never reused, prevents object store key collisions)
- ✅ Atomic segment creation (race condition between pre-motion buffer and live frames fixed)
- ✅ MKV validation on recovery (checks EBML magic, Segment element, and Cluster presence)

---

## Recently Fixed (January 2025)

### Segment Index Reuse Data Loss (CRITICAL - FIXED)
**Problem**: `getNextSegmentIndex()` counted files on disk, but after upload files were deleted, causing index 0, 1, 2 to repeat.
**Impact**: Object store key collisions, DB unique constraint violations, data loss.
**Fix**: Replaced with monotonic per-recording counter in `segmentIndex` map. Indices never reused.

### Segment Creation Race Condition (HIGH - FIXED)
**Problem**: `WriteFrame()` unlocked between checking for nil segment and calling `NewSegment()`, allowing two goroutines to create competing segments.
**Fix**: Added `newSegmentLocked()` - atomic check-and-create under single lock hold.

### Pending Queue Memory Leak (MEDIUM - FIXED)
**Problem**: `s.pending` grew forever since `GetPendingSegments()` was never called.
**Fix**: Removed unused pending queue. Uploads handled directly by recorder.

### DB Schema/Status Alignment (MEDIUM - FIXED)
**Problem**: CHECK constraints didn't include new statuses (`crashed`, `panic_recovered`, `finalizing`, `uploaded`). FK confusion between `recordings.id` vs `external_id`.
**Fix**: Updated CHECK constraints, changed segments/motion_events FK to reference `external_id` directly.

### Directory Fsync for Durability (MEDIUM - FIXED)
**Problem**: Only file data was fsynced, not directory entries. Power loss could lose rename.
**Fix**: Added `fsyncDir()` - fsyncs both temp and output directories around rename.

### Segment Index Not Crash-Persistent (MEDIUM - FIXED)
**Problem**: Monotonic `segmentIndex` map was in-memory only; after restart, indices would start from 0 again if segments were already uploaded.
**Fix**: Added `GetNextSegmentIndex()` to query DB for max segment index on recording start. `SetInitialSegmentIndex()` called before first segment.

### segmentIndex Map Memory Leak (MEDIUM - FIXED)
**Problem**: `segmentIndex` map entries accumulated forever; never cleaned up when recordings finished.
**Fix**: Added `ClearRecordingState()` to remove all state for finished recordings, called in `finalizeRecording()`.

### SaveSegment Stats Inflation (MEDIUM - FIXED)
**Problem**: `segment_count` incremented on every `SaveSegment()` call, including upserts (ON CONFLICT DO UPDATE).
**Fix**: Use `(xmax = 0) AS inserted` in RETURNING clause to detect actual inserts vs updates; only increment stats on inserts.

### MKV Recovery Deletes Uncertain Files (LOW - FIXED)
**Problem**: Files failing validation were deleted; potentially recoverable data lost.
**Fix**: Quarantine files to `.quarantine.mkv` instead of deleting. Increased scan size to 256KB for better Cluster detection.

### emergencyFinalize Deadlock Risk (MEDIUM - FIXED)
**Problem**: `emergencyFinalize()` used blocking `Lock()` which could deadlock if another goroutine held the mutex during panic.
**Fix**: Use `TryLock()` (non-blocking); log and return if lock unavailable. Also persist `panic_recovered` status to DB.

---

## Previously Fixed (January 2025)

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

## Resolved Issues

### Audio Recording to MKV (FIXED)
**Fix**: Audio pipeline implemented - Opus frames muxed into MKV via `WriteAudioFrame()`

### MinIO Multipart Upload Placeholders (REMOVED)
**Fix**: Removed unused placeholder functions. MinIO client handles multipart internally.

### Frame Buffer Full Errors (FIXED)
**Fix**: Increased buffer from 1s to 3s in `internal/recorder/recorder.go:123`

### JSON-RPC Health Check Failures (FIXED — REMOVED)
**Fix**: rtcManager was removed during LiveKit migration. No longer applicable.

### Config JSON Parsing (FIXED)
**Problem**: Duration values parsed incorrectly (5 minutes instead of 5 seconds)
**Cause**: YAML unmarshaler used for JSON files
**Fix**: Check file extension and use appropriate parser in `config.go`

---

## Troubleshooting

### WebRTC not connecting
1. `tailscale status` - Tailscale must be running
2. Verify livekit-server is running: `curl http://localhost:7880`
3. `curl http://localhost:3000/api/health` - Node proxy + Go backend must respond
4. `curl http://localhost:8081/api/livekit-token` - Token endpoint must return JWT

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
