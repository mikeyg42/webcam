package video

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"io"

	"github.com/at-wat/ebml-go/webm"
	"github.com/pion/webrtc/v4"

	"gocv.io/x/gocv"
)

type frameBuffer struct {
	data      []byte
	timestamp int64
	keyframe  bool
}
type VideoConfig struct {
	Width        int
	Height       int
	Framerate    int
	BitRate      int
	OutputPath   string
	SampleRate   int
	ChannelCount int
}

type Recorder struct {
	config         *VideoConfig
	mu             sync.Mutex
	isRecording    bool
	currentFile    string
	peerConnection *webrtc.PeerConnection
	webmWriter     webm.BlockWriteCloser
	videoTrack     *webrtc.TrackRemote
	audioTrack     *webrtc.TrackRemote
	done           chan struct{}
	stats          RecorderStats
	videoBuffer    chan frameBuffer
	audioBuffer    chan frameBuffer
	bufferSize     int
	ctx            context.Context
	cancel         context.CancelFunc
	errors         chan error
	errorHandler   func(error)
}

func NewRecorder(config *VideoConfig) *Recorder {
	ctx, cancel := context.WithCancel(context.Background())
	const defaultBufferSize = 100
	r := &Recorder{
		config:       config,
		ctx:          ctx,
		cancel:       cancel,
		done:         make(chan struct{}),
		videoBuffer:  make(chan frameBuffer, defaultBufferSize),
		audioBuffer:  make(chan frameBuffer, defaultBufferSize),
		bufferSize:   defaultBufferSize,
		errors:       make(chan error, 10),
		errorHandler: defaultErrorHandler, // Add default handler
	}

	// Start buffer processing
	go r.processBuffers()
	go r.updateStats()

	go r.handleErrors()
	return r
}

type RecorderStats struct {
	VideoFramesWritten int64
	AudioFramesWritten int64
	KeyFramesWritten   int64 // Add this field
	DroppedFrames      int64
	LastWriteTime      time.Time
	RecordingDuration  time.Duration
	FileSize           int64
}

func (r *Recorder) GetStats() RecorderStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stats
}

func (r *Recorder) processBuffers() {
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-r.done:
			return
		case vf := <-r.videoBuffer:
			r.writeVideoFrame(vf.data, vf.timestamp)
		case af := <-r.audioBuffer:
			r.writeAudioFrame(af.data, af.timestamp)
		}
	}
}

func (r *Recorder) StartRecording(frameChan <-chan gocv.Mat) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.isRecording {
		return nil
	}

	if err := os.MkdirAll(r.config.OutputPath, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %v", err)
	}

	timestamp := time.Now().Format("2006-01-02_15-04-05")
	r.currentFile = filepath.Join(r.config.OutputPath, fmt.Sprintf("recording_%s.webm", timestamp))

	file, err := os.Create(r.currentFile)
	if err != nil {
		return fmt.Errorf("failed to create file: %v", err)
	}

	// Configure WebM writer
	ws, err := webm.NewSimpleBlockWriter(file,
		[]webm.TrackEntry{
			{
				Name:            "Video",
				TrackNumber:     1,
				TrackUID:        12345,
				CodecID:         "V_VP9",
				TrackType:       1,
				DefaultDuration: uint64(time.Second/time.Duration(r.config.Framerate)) * 1000,
				Video: &webm.Video{
					PixelWidth:  uint64(r.config.Width),
					PixelHeight: uint64(r.config.Height),
				},
			},
			{
				Name:        "Audio",
				TrackNumber: 2,
				TrackUID:    67890,
				CodecID:     "A_OPUS",
				TrackType:   2,
				Audio: &webm.Audio{
					SamplingFrequency: float64(r.config.SampleRate),
					Channels:          uint64(r.config.ChannelCount),
				},
			},
		},
	)
	if err != nil {
		file.Close()
		return fmt.Errorf("failed to create WebM writer: %v", err)
	}

	r.webmWriter = ws[0]
	r.isRecording = true
	log.Printf("Started recording to: %s", r.currentFile)
	return nil
}

func (r *Recorder) StopRecording() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.isRecording {
		return nil
	}

	if r.webmWriter != nil {
		if err := r.webmWriter.Close(); err != nil {
			return fmt.Errorf("failed to close WebM writer: %v", err)
		}
		r.webmWriter = nil
	}

	// Verify the recording file exists and has content
	if r.currentFile != "" {
		fileInfo, err := os.Stat(r.currentFile)
		if err != nil {
			return fmt.Errorf("failed to verify recording file: %v", err)
		}

		if fileInfo.Size() == 0 {
			os.Remove(r.currentFile)
			return fmt.Errorf("recording failed: output file is empty")
		}

		log.Printf("Successfully saved recording (%d bytes): %s",
			fileInfo.Size(), r.currentFile)
	}

	r.isRecording = false
	return nil
}

func (r *Recorder) HandleTrack(track *webrtc.TrackRemote) error {
	switch track.Kind() {
	case webrtc.RTPCodecTypeVideo:
		return r.handleVideoTrack(track)
	case webrtc.RTPCodecTypeAudio:
		return r.handleAudioTrack(track)
	default:
		return fmt.Errorf("unsupported track kind: %s", track.Kind())
	}
}

func (r *Recorder) handleVideoTrack(track *webrtc.TrackRemote) error {
	r.mu.Lock()
	if r.videoTrack != nil {
		r.mu.Unlock()
		return fmt.Errorf("video track already exists")
	}
	r.videoTrack = track
	r.mu.Unlock()

	buffer := make([]byte, 1500)
	frameAssembler := newVP8Assembler()

	defer func() {
		r.mu.Lock()
		r.videoTrack = nil
		r.mu.Unlock()
	}()

	for {
		select {
		case <-r.done:
			return nil
		default:
			n, _, err := track.Read(buffer)
			if err != nil {
				if err == io.EOF {
					return nil
				}
				return fmt.Errorf("error reading video track: %v", err)
			}

			frame, timestamp, complete := frameAssembler.addPacket(buffer[:n])
			if complete {
				// Get keyframe information from assembler
				select {
				case r.videoBuffer <- frameBuffer{
					data:      frame,
					timestamp: timestamp,
					keyframe:  frameAssembler.keyframe,
				}:
					r.mu.Lock()
					r.stats.VideoFramesWritten++
					r.stats.LastWriteTime = time.Now()
					r.mu.Unlock()
				default:
					r.mu.Lock()
					r.stats.DroppedFrames++
					r.mu.Unlock()
					log.Printf("Video buffer full, dropping frame")
				}
			}

		}
	}
}
func (r *Recorder) handleAudioTrack(track *webrtc.TrackRemote) error {
	r.mu.Lock()
	if r.audioTrack != nil {
		r.mu.Unlock()
		return fmt.Errorf("audio track already exists")
	}
	r.audioTrack = track
	r.mu.Unlock()

	buffer := make([]byte, 1500)

	defer func() {
		r.mu.Lock()
		r.audioTrack = nil
		r.mu.Unlock()
	}()

	for {
		select {
		case <-r.done:
			return nil
		default:
			n, _, err := track.Read(buffer)
			if err != nil {
				if err == io.EOF {
					return nil
				}
				return fmt.Errorf("error reading audio track: %v", err)
			}

			timestamp := int64(binary.BigEndian.Uint32(buffer[4:8]))
			select {
			case r.audioBuffer <- frameBuffer{
				data:      buffer[12:n],
				timestamp: timestamp,
			}:
				r.mu.Lock()
				r.stats.AudioFramesWritten++
				r.stats.LastWriteTime = time.Now()
				r.mu.Unlock()
			default:
				r.mu.Lock()
				r.stats.DroppedFrames++
				r.mu.Unlock()
				log.Printf("Audio buffer full, dropping frame")
			}
		}
	}
}

type vp8Assembler struct {
	currentFrame []byte
	timestamp    int64
	keyframe     bool
	started      bool
}

func newVP8Assembler() *vp8Assembler {
	return &vp8Assembler{
		currentFrame: make([]byte, 0, 1500),
	}
}

func (v *vp8Assembler) addPacket(packet []byte) ([]byte, int64, bool) {
	if len(packet) < 13 {
		return nil, 0, false
	}

	vp8Desc := packet[12]

	// Start of VP8 partition
	if vp8Desc&0x10 != 0 {
		if v.started {
			frame := make([]byte, len(v.currentFrame))
			copy(frame, v.currentFrame)
			timestamp := v.timestamp

			v.currentFrame = v.currentFrame[:0]
			v.started = false

			return frame, timestamp, true
		}

		v.started = true
		v.timestamp = int64(binary.BigEndian.Uint32(packet[4:8]))
		v.keyframe = packet[13]&0x01 == 0
	}

	if v.started {
		v.currentFrame = append(v.currentFrame, packet[13:]...)
	}

	return nil, 0, false
}

func (r *Recorder) writeVideoFrame(frame []byte, timestamp int64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.isRecording || r.webmWriter == nil {
		return
	}

	// Check if file needs rotation before writing
	if err := r.checkFileRotation(); err != nil {
		log.Printf("Error checking file rotation: %v", err)
		select {
		case r.errors <- fmt.Errorf("file rotation check failed: %v", err):
		default:
		}
	}

	writer, ok := r.webmWriter.(webm.BlockWriter)
	if !ok {
		log.Printf("WebM writer does not support BlockWriter")
		return
	}

	tc := timestamp / 90 // VP8 timestamp rate is 90000

	// VP8 payload descriptor is at least 1 byte
	if len(frame) < 1 {
		log.Printf("Frame too short")
		return
	}

	// Check if this is a keyframe by examining VP8 payload header
	// In VP8, the first byte of the payload after descriptor contains frame type
	// Bit 0: 0 = key frame, 1 = interframe
	isKeyFrame := false
	if len(frame) > 1 { // Ensure we have at least one byte after descriptor
		isKeyFrame = frame[1]&0x01 == 0
	}

	retries := 3
	for i := 0; i < retries; i++ {
		_, err := writer.Write(isKeyFrame, tc, frame)
		if err == nil {
			// Update stats for keyframes
			if isKeyFrame {
				r.stats.KeyFramesWritten++
			}
			break
		}
		if i == retries-1 {
			log.Printf("Failed to write video frame after %d attempts: %v", retries, err)
			select {
			case r.errors <- fmt.Errorf("failed to write video frame: %v", err):
			default:
			}
		}
		time.Sleep(time.Millisecond * 10)
	}
}

func (r *Recorder) writeAudioFrame(frame []byte, timestamp int64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.isRecording || r.webmWriter == nil {
		return
	}

	writer, ok := r.webmWriter.(webm.BlockWriter)
	if !ok {
		log.Printf("WebM writer does not support BlockWriter")
		return
	}

	tc := timestamp / 48 // Opus timestamp rate is 48000

	retries := 3
	for i := 0; i < retries; i++ {
		_, err := writer.Write(false, tc, frame)
		if err == nil {
			break
		}
		if i == retries-1 {
			log.Printf("Failed to write audio frame after %d attempts: %v", retries, err)
		}
		time.Sleep(time.Millisecond * 10)
	}
}

func (r *Recorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	select {
	case <-r.done:
		return nil
	default:
		close(r.done)
	}

	if err := r.StopRecording(); err != nil {
		return fmt.Errorf("error stopping recording: %v", err)
	}

	// Wait for any pending writes to complete
	time.Sleep(time.Millisecond * 100)

	return nil
}

func (r *Recorder) Cleanup() error {
	r.cancel()
	close(r.done)

	// Drain buffers with timeout
	timeout := time.After(5 * time.Second)
	for {
		select {
		case <-r.videoBuffer:
		case <-r.audioBuffer:
		case <-timeout:
			goto done
		default:
			goto done
		}
	}
done:
	if err := r.StopRecording(); err != nil {
		return fmt.Errorf("error stopping recording: %v", err)
	}

	// Close error channel
	close(r.errors)
	return nil
}

func (r *Recorder) rotateFile() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Close current file
	if r.webmWriter != nil {
		if err := r.webmWriter.Close(); err != nil {
			return fmt.Errorf("failed to close current file: %v", err)
		}

		// Verify integrity of the file we're closing
		if err := r.verifyFileIntegrity(r.currentFile); err != nil {
			log.Printf("Warning: File integrity check failed for %s: %v", r.currentFile, err)
			// Optionally handle corrupt file (e.g., move to corrupt folder)
			if err := r.handleCorruptFile(r.currentFile); err != nil {
				log.Printf("Error handling corrupt file: %v", err)
			}
		}
	}

	// Create new file
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	newFile := filepath.Join(r.config.OutputPath, fmt.Sprintf("recording_%s.webm", timestamp))

	file, err := os.Create(newFile)
	if err != nil {
		return fmt.Errorf("failed to create new file: %v", err)
	}

	ws, err := webm.NewSimpleBlockWriter(file,
		[]webm.TrackEntry{
			{
				Name:            "Video",
				TrackNumber:     1,
				TrackUID:        12345,
				CodecID:         "V_VP8",
				TrackType:       1,
				DefaultDuration: uint64(time.Second/time.Duration(r.config.Framerate)) * 1000,
				Video: &webm.Video{
					PixelWidth:  uint64(r.config.Width),
					PixelHeight: uint64(r.config.Height),
				},
			},
			{
				Name:        "Audio",
				TrackNumber: 2,
				TrackUID:    67890,
				CodecID:     "A_OPUS",
				TrackType:   2,
				Audio: &webm.Audio{
					SamplingFrequency: float64(r.config.SampleRate),
					Channels:          uint64(r.config.ChannelCount),
				},
			},
		})
	if err != nil {
		file.Close()
		return fmt.Errorf("failed to create WebM writer: %v", err)
	}

	// Reset recording duration for the new file
	r.stats.RecordingDuration = 0

	r.currentFile = newFile
	r.webmWriter = ws[0]
	return nil
}

func (r *Recorder) handleCorruptFile(filelocation string) error {
	// Create corrupt files directory if it doesn't exist
	corruptDir := filepath.Join(r.config.OutputPath, "corrupt")
	if err := os.MkdirAll(corruptDir, 0755); err != nil {
		return fmt.Errorf("failed to create corrupt files directory: %v", err)
	}

	// Move corrupt file to corrupt directory
	filename := filepath.Base(filelocation)
	newPath := filepath.Join(corruptDir, filename)
	if err := os.Rename(filelocation, newPath); err != nil {
		return fmt.Errorf("failed to move corrupt file: %v", err)
	}

	log.Printf("Moved corrupt file to: %s", newPath)
	return nil
}

const (
	maxFileSize     = 2 * 1024 * 1024 * 1024 // 2GB
	maxFileDuration = 1 * time.Hour          // 1 hour
)

func (r *Recorder) checkFileRotation() error {
	if r.currentFile == "" {
		return nil
	}

	info, err := os.Stat(r.currentFile)
	if err != nil {
		return fmt.Errorf("failed to stat current file: %v", err)
	}

	r.mu.Lock()
	r.stats.FileSize = info.Size()
	fileDuration := r.stats.RecordingDuration
	r.mu.Unlock()

	// Rotate file if either size or duration limit is reached
	if info.Size() >= maxFileSize || fileDuration >= maxFileDuration {
		if err := r.rotateFile(); err != nil {
			return fmt.Errorf("failed to rotate file: %v", err)
		}
	}
	return nil
}

func (r *Recorder) updateStats() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	startTime := time.Now()

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			r.mu.Lock()
			if r.isRecording {
				r.stats.RecordingDuration = time.Since(startTime)
			}
			r.mu.Unlock()
		}
	}
}

func (r *Recorder) verifyFileIntegrity(filepath string) error {
	file, err := os.Open(filepath)
	if err != nil {
		return fmt.Errorf("failed to open file for verification: %v", err)
	}
	defer file.Close()

	// Read first few bytes to verify WebM header
	header := make([]byte, 4)
	if _, err := file.Read(header); err != nil {
		return fmt.Errorf("failed to read file header: %v", err)
	}

	// WebM files start with 0x1A 0x45 0xDF 0xA3
	if header[0] != 0x1A || header[1] != 0x45 || header[2] != 0xDF || header[3] != 0xA3 {
		return fmt.Errorf("invalid WebM file format")
	}

	return nil
}

// ERROR HANDLING

func (r *Recorder) handleErrors() {
	for {
		select {
		case <-r.ctx.Done():
			return
		case err := <-r.errors:
			if r.errorHandler != nil {
				r.errorHandler(err)
			}

			// Handle critical errors
			if isCriticalError(err) {
				r.handleCriticalError(err)
			}
		}
	}
}

func isCriticalError(err error) bool {
	// Define what constitutes a critical error
	if err == nil {
		return false
	}

	// Check for specific error types
	return strings.Contains(err.Error(), "disk full") ||
		strings.Contains(err.Error(), "permission denied") ||
		strings.Contains(err.Error(), "device not found")
}

func (r *Recorder) handleCriticalError(err error) {
	log.Printf("Critical error encountered: %v", err)

	// Attempt recovery
	if err := r.rotateFile(); err != nil {
		log.Printf("Failed to rotate file after critical error: %v", err)
		// If recovery fails, stop recording
		r.StopRecording()
	}
}
func (r *Recorder) SetErrorHandler(handler func(error)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errorHandler = handler
}

// Default error handler
func defaultErrorHandler(err error) {
	log.Printf("Recorder error: %v", err)
}
