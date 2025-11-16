package encoder

import (
	"fmt"
	"sync/atomic"
	"time"
)

// EncoderConfig contains all encoder configuration
type EncoderConfig struct {
	// Video parameters (required)
	Width            int
	Height           int
	FrameRate        int
	BitRateKbps      int
	KeyFrameInterval int

	// Codec selection
	Codec            string // "auto", "h264", "vp9", "av1"
	PreferredEncoder string // Specific encoder name
	PreferHardware   bool

	// Performance tuning
	CPUUsed     int    // VP9: 0-8
	Deadline    string // VP9: "realtime", "good", "best"
	Threads     int
	BufferSize  int

	// VP9 specific
	RowMT       bool
	TileColumns int

	// H.264 specific
	Profile string // "baseline", "main", "high"
	Preset  string // "ultrafast", "fast", "medium", "slow"
	Tune    string // "zerolatency", "film", "animation"

	// Quality settings
	MinQuantizer int
	MaxQuantizer int

	// Real-time settings
	RealTime   bool
	LowLatency bool

	// Advanced
	MaxBFrames            int
	AllowSoftwareFallback bool
}

// DefaultEncoderConfig returns sensible defaults
func DefaultEncoderConfig() EncoderConfig {
	return EncoderConfig{
		Width:            640,
		Height:           480,
		FrameRate:        15,
		BitRateKbps:      1500,
		KeyFrameInterval: 30,
		Codec:            "auto",
		Profile:          "main",
		CPUUsed:          5,
		Deadline:         "realtime",
		Threads:          4,
		BufferSize:       100,
		PreferHardware:   true,
		RowMT:            true,
		TileColumns:      2,
		MinQuantizer:     4,
		MaxQuantizer:     48,
		RealTime:         true,
		LowLatency:       true,
	}
}

// Validate checks configuration validity
func (c *EncoderConfig) Validate() error {
	if c.Width <= 0 || c.Height <= 0 {
		return fmt.Errorf("invalid resolution: %dx%d", c.Width, c.Height)
	}
	if c.FrameRate <= 0 || c.FrameRate > 120 {
		return fmt.Errorf("invalid framerate: %d", c.FrameRate)
	}
	if c.BitRateKbps <= 0 {
		return fmt.Errorf("invalid bitrate: %d kbps", c.BitRateKbps)
	}
	if c.CPUUsed < 0 || c.CPUUsed > 8 {
		return fmt.Errorf("invalid cpu-used: %d (must be 0-8)", c.CPUUsed)
	}
	if c.TileColumns < 0 || c.TileColumns > 6 {
		return fmt.Errorf("invalid tile-columns: %d (must be 0-6)", c.TileColumns)
	}
	if c.Threads < 0 {
		return fmt.Errorf("invalid threads: %d", c.Threads)
	}
	return nil
}

// EncoderStats tracks runtime statistics
type EncoderStats struct {
	framesIn      atomic.Uint64
	packetsOut    atomic.Uint64
	droppedFrames atomic.Uint64
	bytesEncoded  atomic.Uint64
	lastKeyframe  atomic.Value // stores time.Time
}

// Increment methods
func (s *EncoderStats) IncrementFramesIn()      { s.framesIn.Add(1) }
func (s *EncoderStats) IncrementPacketsOut()    { s.packetsOut.Add(1) }
func (s *EncoderStats) IncrementDroppedFrames() { s.droppedFrames.Add(1) }
func (s *EncoderStats) AddBytesEncoded(n uint64) { s.bytesEncoded.Add(n) }
func (s *EncoderStats) SetLastKeyframe(t time.Time) { s.lastKeyframe.Store(t) }

// Get methods
func (s *EncoderStats) GetFramesIn() uint64      { return s.framesIn.Load() }
func (s *EncoderStats) GetPacketsOut() uint64    { return s.packetsOut.Load() }
func (s *EncoderStats) GetDroppedFrames() uint64 { return s.droppedFrames.Load() }
func (s *EncoderStats) GetBytesEncoded() uint64  { return s.bytesEncoded.Load() }
func (s *EncoderStats) GetLastKeyframe() time.Time {
	if v := s.lastKeyframe.Load(); v != nil {
		return v.(time.Time)
	}
	return time.Time{}
}

// Reset clears all statistics
func (s *EncoderStats) Reset() {
	s.framesIn.Store(0)
	s.packetsOut.Store(0)
	s.droppedFrames.Store(0)
	s.bytesEncoded.Store(0)
	s.lastKeyframe.Store(time.Time{})
}

// Snapshot returns a copy of current stats
func (s *EncoderStats) Snapshot() EncoderStatsSnapshot {
	return EncoderStatsSnapshot{
		FramesIn:      s.GetFramesIn(),
		PacketsOut:    s.GetPacketsOut(),
		DroppedFrames: s.GetDroppedFrames(),
		BytesEncoded:  s.GetBytesEncoded(),
		LastKeyframe:  s.GetLastKeyframe(),
	}
}

// EncoderStatsSnapshot is a point-in-time copy of stats
type EncoderStatsSnapshot struct {
	FramesIn      uint64
	PacketsOut    uint64
	DroppedFrames uint64
	BytesEncoded  uint64
	LastKeyframe  time.Time
}

// CalculateFPS calculates current FPS from stats
func (s *EncoderStatsSnapshot) CalculateFPS(duration time.Duration) float64 {
	if duration <= 0 {
		return 0
	}
	return float64(s.FramesIn) / duration.Seconds()
}

// CalculateBitRate calculates current bitrate in kbps
func (s *EncoderStatsSnapshot) CalculateBitRate(duration time.Duration) float64 {
	if duration <= 0 {
		return 0
	}
	return float64(s.BytesEncoded*8) / duration.Seconds() / 1000
}

// CalculateDropRate calculates frame drop percentage
func (s *EncoderStatsSnapshot) CalculateDropRate() float64 {
	total := s.FramesIn + s.DroppedFrames
	if total == 0 {
		return 0
	}
	return float64(s.DroppedFrames) / float64(total) * 100
}

// EncoderError represents an encoder-specific error
type EncoderError struct {
	Code    int
	Message string
	Fatal   bool
}

func (e *EncoderError) Error() string {
	severity := "recoverable"
	if e.Fatal {
		severity = "fatal"
	}
	return fmt.Sprintf("[%s] encoder error %d: %s", severity, e.Code, e.Message)
}

// NewEncoderError creates a new encoder error
func NewEncoderError(code int, message string, fatal bool) *EncoderError {
	return &EncoderError{
		Code:    code,
		Message: message,
		Fatal:   fatal,
	}
}

// Common error codes
const (
	ErrCodePipelineInit = 1001 + iota
	ErrCodeEncoderNotFound
	ErrCodeInvalidConfig
	ErrCodePushBufferFailed
	ErrCodeRTPPacketFailed
	ErrCodeKeyframeTooLate
)