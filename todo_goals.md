# Webcam Security System - Todo & Goals

## 🔴 CRITICAL PRIORITY: Recording System Reliability Fixes

**Status:** Recording system has critical failures that prevent reliable security footage capture.
**Risk Level:** HIGH - Current system unsuitable for production use
**Overall Reliability Score:** 4.2/10 (POOR)

### Critical Issues Identified (2025-09-30 Code Audit)
- ❌ Recording channel frames are discarded (100% data loss from recordChannel)
- ❌ No disk space checks before recording
- ❌ Race conditions in recording state management
- ❌ Buffer overflow causes silent frame drops (up to 6.7 seconds lost)
- ❌ No frame write verification (silent data corruption)
- ❌ Zero redundancy mechanisms

### Architecture Change: Recording Must Be Independent of WebRTC

**Current (Broken):** Recording depends on WebRTC track handlers → fragile, network-dependent
**Target:** Recording directly from FrameDistributor → robust, network-independent

```
Camera → FrameDistributor → recordChannel → VP9 Encoder → WebM (Direct to disk)
                          → vp9Channel → WebRTC (Live streaming only)
                          → motionChannel → Motion Detection
```

---

## Phase 1: CRITICAL FIXES (Fix Immediately) - 15 hours

### 1.1 Implement Direct Frame-to-Disk Recording ⏱️ 6 hours
- [ ] **Modify `internal/integration/pipeline.go:254-288`**
  - [ ] Add VP9 encoder initialization in `consumeRecordingFrames()`
  - [ ] Encode frames from recordChannel directly to VP9
  - [ ] Write encoded frames to WebM file
  - [ ] Add proper frame timestamp management
  - [ ] Remove WebRTC dependency from recording path
- **Impact:** Fixes 100% frame loss from recordChannel
- **Files:** `internal/integration/pipeline.go`

### 1.2 Remove WebRTC Dependency from Recorder ⏱️ 4 hours
- [ ] **Refactor `internal/video/recorder.go`**
  - [ ] Remove `HandleTrack()` method (lines 205-258)
  - [ ] Delete `videoBuffer` and `audioBuffer` channels
  - [ ] Remove `processBuffers()` goroutine (lines 299-361)
  - [ ] Delete VP8 frame assembler code
  - [ ] Simplify to pure file lifecycle manager (start/stop/rotate)
- **Impact:** Cleaner architecture, recording independent of WebRTC
- **Files:** `internal/video/recorder.go`

### 1.3 Add Disk Space Checks ⏱️ 2 hours
- [ ] **Add preemptive validation in `recorder.go:111-168`**
  - [ ] Implement `checkDiskSpace()` method (require 5GB minimum)
  - [ ] Add `verifyDirectoryWritable()` method
  - [ ] Call both checks before `StartRecording()`
  - [ ] Return clear error messages on failure
- **Impact:** Prevents recording failures due to disk full
- **Files:** `internal/video/recorder.go`

### 1.4 Fix Race Conditions in Recording State ⏱️ 3 hours
- [ ] **Thread-safe state management in `recorder.go`**
  - [ ] Replace `isRecording bool` with `atomic.Bool`
  - [ ] Add separate mutexes for state vs file operations
  - [ ] Ensure atomic state transitions
  - [ ] Add state validation in all public methods
- **Impact:** Prevents file corruption from concurrent access
- **Files:** `internal/video/recorder.go`

---

## Phase 2: HIGH PRIORITY FIXES (Fix This Week) - 20 hours

### 2.5 Implement Emergency Frame Buffer ⏱️ 6 hours
- [ ] **Add overflow protection in `pipeline.go`**
  - [ ] Create circular emergency buffer (300 frames = 20 seconds)
  - [ ] Implement backpressure when buffer >90% full
  - [ ] Add metrics for buffer utilization
  - [ ] Log warnings when emergency buffer engaged
- **Impact:** Prevents frame loss during encoder slowdowns
- **Files:** `internal/integration/pipeline.go`

### 2.6 Add Frame Write Verification ⏱️ 4 hours
- [ ] **Track frame sequence numbers**
  - [ ] Implement FrameTracker with sequence validation
  - [ ] Detect missing frames (sequence gaps)
  - [ ] Log all frame drops with timestamps
  - [ ] Add statistics for frame write success rate
- **Impact:** Detect silent data loss
- **Files:** `internal/integration/pipeline.go`

### 2.7 Implement Buffer Flush on Stop ⏱️ 4 hours
- [ ] **Ensure clean recording shutdown**
  - [ ] Wait for all buffers to drain before closing file
  - [ ] Add 15-second timeout for buffer flush
  - [ ] Log number of frames flushed vs lost
  - [ ] Verify file integrity after flush
- **Impact:** Prevents loss of last 6.7 seconds of recording
- **Files:** `internal/integration/pipeline.go`

### 2.8 Enhance File Integrity Verification ⏱️ 3 hours
- [ ] **Comprehensive validation in `recorder.go:637-656`**
  - [ ] Check file size > 1KB minimum
  - [ ] Verify WebM structure is valid
  - [ ] Validate duration metadata exists
  - [ ] Confirm at least one keyframe present
  - [ ] Check timestamp continuity
- **Impact:** Detect corrupted recordings early
- **Files:** `internal/video/recorder.go`

### 2.9 Fix Frame Distributor Drop Rate ⏱️ 3 hours
- [ ] **Reduce drops in `distributor.go:260-289`**
  - [ ] Increase recordChannel buffer: 30 → 150 frames
  - [ ] Implement adaptive backpressure mechanism
  - [ ] Log every frame drop (not just every 100th)
  - [ ] Add metrics dashboard for drop rates
- **Impact:** Reduces 50%+ drop rates under load
- **Files:** `internal/framestream/distributor.go`

---

## Phase 3: MEDIUM PRIORITY (Fix This Month) - 30 hours

### 3.10 Implement Recording Watchdog ⏱️ 5 hours
- [ ] Monitor file size increases every 5 seconds
- [ ] Alert if recording stalls (no growth)
- [ ] Auto-restart recording on failure
- [ ] Send notifications for recording failures

### 3.11 Add Crash Recovery ⏱️ 8 hours
- [ ] Use `.tmp` extension during active recording
- [ ] Rename to `.webm` only on successful close
- [ ] Scan for `.tmp` files on startup
- [ ] Attempt to salvage partial recordings
- [ ] Move corrupted files to `corrupted/` subdirectory

### 3.12 Fix Resolution/Framerate Config ⏱️ 2 hours
- [ ] Align all components to 1280x720 @ 15fps
- [ ] Single source of truth for video parameters
- [ ] Remove conflicting config values (640x480, 25fps)

### 3.13 Reduce Memory Allocations ⏱️ 10 hours
- [ ] Implement `sync.Pool` for image buffers
- [ ] Pre-allocate encoder frame buffers
- [ ] Reuse buffers instead of cloning where safe
- [ ] Profile and optimize hot paths
- [ ] Target: <20 GB/hour allocations (currently ~200 GB/hour)

### 3.14 Implement Frame Checksums ⏱️ 5 hours
- [ ] Add CRC32 checksum to each frame
- [ ] Store checksums in `.checksum` sidecar file
- [ ] Verify on playback/export
- [ ] Detect silent corruption

---

## Testing Requirements

### Critical Tests (Must Pass Before Production)
- [ ] **Disk Full Test:** Fill disk to 95%, verify graceful failure
- [ ] **I/O Stall Test:** Throttle disk to 1MB/s, verify no frame loss
- [ ] **Memory Pressure Test:** Limit to 1GB RAM, verify no OOM
- [ ] **Concurrent Recording Test:** Multiple start/stop cycles, verify no race conditions
- [ ] **File Rotation Test:** Rotate under load, verify no frame loss
- [ ] **Crash Recovery Test:** Kill process during recording, verify file salvage
- [ ] **24-Hour Stress Test:** Record continuously, verify no memory leaks
- [ ] **Frame Verification Test:** Compare captured vs recorded frame count

### Performance Benchmarks
- [ ] Frame clone: <5ms @ 1280x720
- [ ] Frame encode: <30ms @ 1280x720
- [ ] Frame write: <10ms average
- [ ] Drop rate: <1% under normal load
- [ ] Memory: <2GB steady-state
- [ ] Disk I/O: <50 MB/s sustained

---

## Success Metrics

**Current State:**
- Reliability: 4.2/10
- Frame loss: Unknown (not tracked)
- Redundancy: 0/10
- Production ready: ❌ NO

**Target State (Phase 1 Complete):**
- Reliability: 7/10
- Frame loss: <1% tracked
- Redundancy: 3/10
- Production ready: ⚠️ MAYBE (basic use only)

**Target State (Phase 2 Complete):**
- Reliability: 9/10
- Frame loss: <0.1% tracked
- Redundancy: 7/10
- Production ready: ✅ YES (suitable for real security use)

---

## Current Status: Email Notification System Optimization & Modularization

### Phase 1: MailerSend Service Optimization
- [ ] **Research MailerSend optimal configuration**
  - [ ] Determine if API vs SMTP relay is better for our use case
  - [ ] Minimize required MailerSend dashboard configuration
  - [ ] Ensure "clone and run" simplicity (minimal external setup)
  - [ ] Document recommended MailerSend token permissions (minimize scope)
  - [ ] Evaluate domain verification requirements
  - [ ] Consider sender identity configuration

### Phase 2: Gmail OAuth2 Integration & Code Modularization
- [ ] **Review and integrate Gmail notifier implementation**
  - [ ] Validate Gmail OAuth2 implementation security and best practices
  - [ ] Add missing dependencies to go.mod (oauth2, gmail API)
  - [ ] Create shared email template system for code reuse
  - [ ] Extract common retry logic into shared utilities
  - [ ] Standardize error handling patterns across both notifiers

- [ ] **Modularize notification package**
  - [ ] Extract shared email template functionality
  - [ ] Create common retry mechanism (used by both Gmail & MailerSend)
  - [ ] Unify configuration patterns between Gmail and MailerSend
  - [ ] Create shared email data preparation functions
  - [ ] Ensure consistent interface implementations
  - [ ] Add unified debug/testing capabilities

### Phase 3: Email Configuration GUI (NEW PRIORITY)
- [ ] **Research and choose GUI framework for Go**
  - [ ] Evaluate Fyne vs Gio vs embedded web server approach
  - [ ] Consider cross-platform compatibility (Windows/Mac/Linux)
  - [ ] Test basic GUI window creation and user interactions

- [ ] **Create Email Setup Dialog**
  - [ ] Design main configuration window with 3 clear options
  - [ ] Add MailerSend token input field with validation
  - [ ] Add Gmail OAuth2 button with progress indication
  - [ ] Add "Skip Email" option that continues without notifications
  - [ ] Implement window styling and responsive layout

- [ ] **Implement Email Method Testing**
  - [ ] Create unified test email function for both methods
  - [ ] Add real-time validation feedback (spinner, success/error states)
  - [ ] Implement retry logic with specific error messages
  - [ ] Add method switching without restarting dialog

- [ ] **Error Handling & Recovery System**
  - [ ] Design error display with actionable messages
  - [ ] Implement persistent dialog (stays open until valid choice)
  - [ ] Add method fallback options (try different method on failure)
  - [ ] Create configuration persistence for successful setups

### Phase 4: Integration with Main Application
- [ ] **Update main application startup flow**
  - [ ] Show GUI before camera initialization
  - [ ] Wait for email method selection/configuration
  - [ ] Initialize appropriate notifier based on GUI choice
  - [ ] Add headless mode flag for server deployments

- [ ] **Documentation & Setup**
  - [ ] Update .env.example with Gmail OAuth2 options
  - [ ] Create Gmail OAuth2 setup guide (Google Cloud Console steps)
  - [ ] Document MailerSend vs Gmail trade-offs
  - [ ] Add troubleshooting guide for both email methods
  - [ ] Update SETUP.md with new email configuration options

### Phase 4: Testing & Validation
- [ ] **Test both email notification systems**
  - [ ] Verify MailerSend integration works with optimized configuration
  - [ ] Test Gmail OAuth2 flow end-to-end
  - [ ] Validate encrypted token storage and retrieval
  - [ ] Test email template rendering for both systems
  - [ ] Verify retry logic and error handling
  - [ ] Test "clone and run" experience for both email methods

### Design Goals
1. **Maximum Code Reuse**: Share templates, retry logic, and utilities between Gmail & MailerSend
2. **Consistent Interface**: Both notifiers should implement identical interfaces
3. **"Clone and Run" Friendly**: Minimal external service configuration required
4. **Security First**: Encrypted token storage, minimal OAuth scopes, secure defaults
5. **Graceful Degradation**: Core motion detection works even if email fails
6. **Developer Experience**: Clear configuration, good error messages, easy debugging

### Future Considerations
- [ ] Consider adding SMS notifications (Twilio integration)
- [ ] Webhook notification support for external systems
- [ ] Push notification support (Firebase/Apple Push)
- [ ] Multiple recipient support
- [ ] HTML email templates with embedded images
- [ ] **Support for headless browsers (use the device code OAuth flow)** - Very low priority for server deployments without GUI support