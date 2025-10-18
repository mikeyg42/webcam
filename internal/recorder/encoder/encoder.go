// encoder/encoder.go
package encoder

import (
	"fmt"
	"image"
	"time"

	//"github.com/mikeyg42/webcam/internal/recorder/recorderlog"
)

// Encoder defines the interface for video encoding
type Encoder interface {
	Encode(frame image.Image, pts time.Duration) ([]byte, error)
	Flush() ([][]byte, error)
	Close() error
	GetMetrics() *EncoderMetrics
}

// EncoderMetrics provides runtime statistics
type EncoderMetrics struct {
	FramesEncoded      uint64
	BytesEncoded       uint64
	EncodingTime       time.Duration
	AverageFrameTime   time.Duration
	CurrentBitrate     float64
	DroppedFrames      uint64
	HardwareAccelerated bool
	LastFrameSize      int
	KeyFrames          uint64
	// Memory stats
	MemoryAllocated    uint64
	MemoryReleased     uint64
	ActiveBuffers      int
}

// EncoderConfig contains encoding parameters
type EncoderConfig struct {
	Width            int
	Height           int
	FrameRate        float64
	Bitrate          int
	KeyframeInterval int
	Codec            string // "h264" or "h265"
	Profile          string // "baseline", "main", "high"
	RealTime         bool
	// Hardware specific
	AllowSoftwareFallback bool
	MaxBFrames           int
}

// Error types for better error handling
type EncoderError struct {
	Code    int
	Message string
	Fatal   bool
}

func (e *EncoderError) Error() string {
	return fmt.Sprintf("encoder error %d: %s (fatal: %v)", e.Code, e.Message, e.Fatal)
}