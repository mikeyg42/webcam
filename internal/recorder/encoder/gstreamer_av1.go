// internal/recorder/encoder/gstreamer_av1.go
package encoder

import (
	"context"
	"fmt"
	"image"
	"log"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/go-gst/go-gst/gst"
	"github.com/go-gst/go-gst/gst/app"
)

// GStreamerAV1Encoder implements AV1 encoding using GStreamer
type GStreamerAV1Encoder struct {
	config  EncoderConfig
	metrics EncoderMetrics

	// GStreamer pipeline components
	pipeline *gst.Pipeline
	appSrc   *app.Source
	appSink  *app.Sink
	encoder  *gst.Element

	// Frame buffering
	encodedFrames chan []byte
	ctx           context.Context
	cancel        context.CancelFunc

	// State management
	mu           sync.RWMutex
	running      atomic.Bool
	frameCount   atomic.Uint64
	bytesEncoded atomic.Uint64
	keyFrames    atomic.Uint64
}

// NewGStreamerAV1Encoder creates a new AV1 encoder using GStreamer
func NewGStreamerAV1Encoder(cfg EncoderConfig) (*GStreamerAV1Encoder, error) {
	// Initialize GStreamer
	gst.Init(nil)

	ctx, cancel := context.WithCancel(context.Background())

	enc := &GStreamerAV1Encoder{
		config:        cfg,
		encodedFrames: make(chan []byte, 100),
		ctx:           ctx,
		cancel:        cancel,
	}

	// Build the GStreamer pipeline
	if err := enc.buildPipeline(); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to build pipeline: %w", err)
	}

	// Start the pipeline
	if err := enc.pipeline.SetState(gst.StatePlaying); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to start pipeline: %w", err)
	}

	enc.running.Store(true)
	log.Println("GStreamer AV1 encoder initialized successfully")

	return enc, nil
}

// buildPipeline constructs the GStreamer AV1 encoding pipeline
func (e *GStreamerAV1Encoder) buildPipeline() error {
	var err error

	// Create pipeline
	e.pipeline, err = gst.NewPipeline("av1-encoder")
	if err != nil {
		return fmt.Errorf("create pipeline: %w", err)
	}

	// Create appsrc (our frame input)
	srcElem, err := gst.NewElement("appsrc")
	if err != nil {
		return fmt.Errorf("create appsrc: %w", err)
	}
	e.appSrc = app.SrcFromElement(srcElem)

	// Create videoconvert
	conv, err := gst.NewElement("videoconvert")
	if err != nil {
		return fmt.Errorf("create videoconvert: %w", err)
	}

	// Create AV1 encoder - try SVT-AV1 first, fallback to others
	e.encoder, err = e.createAV1Encoder()
	if err != nil {
		return err
	}

	// Create AV1 parser
	parser, err := gst.NewElement("av1parse")
	if err != nil {
		return fmt.Errorf("create av1parse: %w", err)
	}

	// Create appsink (encoded output)
	sinkElem, err := gst.NewElement("appsink")
	if err != nil {
		return fmt.Errorf("create appsink: %w", err)
	}
	e.appSink = app.SinkFromElement(sinkElem)

	// Add all elements to pipeline
	if err := e.pipeline.AddMany(srcElem, conv, e.encoder, parser, sinkElem); err != nil {
		return fmt.Errorf("add elements: %w", err)
	}

	// Configure appsrc
	if err := e.configureAppSrc(); err != nil {
		return err
	}

	// Configure appsink
	if err := e.configureAppSink(); err != nil {
		return err
	}

	// Link elements
	if err := srcElem.Link(conv); err != nil {
		return fmt.Errorf("link src->conv: %w", err)
	}
	if err := conv.Link(e.encoder); err != nil {
		return fmt.Errorf("link conv->encoder: %w", err)
	}
	if err := e.encoder.Link(parser); err != nil {
		return fmt.Errorf("link encoder->parser: %w", err)
	}
	if err := parser.Link(sinkElem); err != nil {
		return fmt.Errorf("link parser->sink: %w", err)
	}

	return nil
}

// createAV1Encoder tries to create an AV1 encoder, preferring SVT-AV1
func (e *GStreamerAV1Encoder) createAV1Encoder() (*gst.Element, error) {
	// Try SVT-AV1 encoder first (highest quality)
	if enc, err := gst.NewElement("svtav1enc"); err == nil {
		e.configureSVTAV1(enc)
		log.Println("Using SVT-AV1 encoder for recording")
		return enc, nil
	}

	// Try rav1e encoder (alternative)
	if enc, err := gst.NewElement("rav1e"); err == nil {
		e.configureRAV1E(enc)
		log.Println("Using rav1e encoder for recording")
		return enc, nil
	}

	// Try AOM AV1 encoder (reference implementation)
	if enc, err := gst.NewElement("av1enc"); err == nil {
		e.configureAOMEncoder(enc)
		log.Println("Using AOM AV1 encoder for recording")
		return enc, nil
	}

	return nil, fmt.Errorf("no AV1 encoder available (tried: svtav1enc, rav1e, av1enc)")
}

// configureSVTAV1 configures the SVT-AV1 encoder for maximum quality
func (e *GStreamerAV1Encoder) configureSVTAV1(enc *gst.Element) {
	// GOP size (keyframe interval)
	gopSize := e.config.KeyframeInterval
	if gopSize == 0 {
		gopSize = 60 // Default to 2 seconds at 30fps
	}
	_ = enc.SetProperty("gop-size", int(gopSize))

	// Speed preset: 0=slowest/highest quality, 12=fastest/lowest quality
	// For recording, use preset 4-6 for good quality/speed balance
	_ = enc.SetProperty("preset", uint(5))

	// Use CRF mode for consistent quality
	_ = enc.SetProperty("rc-mode", uint(0)) // 0 = CRF mode
	_ = enc.SetProperty("crf", uint(28))    // 28 = good quality (lower is better, 0-63 range)

	// Tile configuration for parallel encoding
	_ = enc.SetProperty("tile-columns", uint(2))
	_ = enc.SetProperty("tile-rows", uint(2))

	// Enable scene detection for better keyframe placement
	_ = enc.SetProperty("enable-scene-detection", true)
}

// configureRAV1E configures the rav1e encoder
func (e *GStreamerAV1Encoder) configureRAV1E(enc *gst.Element) {
	bitrate := e.config.Bitrate
	if bitrate == 0 {
		bitrate = 10000000
	}
	_ = enc.SetProperty("bitrate", uint(bitrate/1000)) // rav1e uses kbps

	gopSize := e.config.KeyframeInterval
	if gopSize == 0 {
		gopSize = 60
	}
	_ = enc.SetProperty("keyframe-interval", uint(gopSize))

	// Speed preset: 0=slowest, 10=fastest
	_ = enc.SetProperty("speed-preset", uint(5))

	// Quantizer: 0-255, lower is better quality
	_ = enc.SetProperty("quantizer", uint(80))
}

// configureAOMEncoder configures the AOM reference encoder
func (e *GStreamerAV1Encoder) configureAOMEncoder(enc *gst.Element) {
	bitrate := e.config.Bitrate
	if bitrate == 0 {
		bitrate = 10000000
	}
	_ = enc.SetProperty("target-bitrate", uint(bitrate/1000)) // kbps

	gopSize := e.config.KeyframeInterval
	if gopSize == 0 {
		gopSize = 60
	}
	_ = enc.SetProperty("keyframe-max-dist", uint(gopSize))

	// CPU speed: 0=slowest, 8=fastest
	_ = enc.SetProperty("cpu-used", int(4))

	// End usage mode: vbr, cbr, cq
	_ = enc.SetProperty("end-usage", "vbr")
}

// configureAppSrc sets up the input source
func (e *GStreamerAV1Encoder) configureAppSrc() error {
	// Set caps for RGB input
	capsStr := fmt.Sprintf(
		"video/x-raw,format=RGB,width=%d,height=%d,framerate=%d/1",
		e.config.Width,
		e.config.Height,
		int(e.config.FrameRate),
	)

	caps := gst.NewCapsFromString(capsStr)
	e.appSrc.SetCaps(caps)
	e.appSrc.SetStreamType(app.AppStreamTypeStream)
	e.appSrc.SetLatency(0, uint64(2*time.Second))
	e.appSrc.SetProperty("format", gst.FormatTime)
	e.appSrc.SetProperty("is-live", false) // Recording mode, not live
	e.appSrc.SetProperty("block", false)

	return nil
}

// configureAppSink sets up the encoded output sink
func (e *GStreamerAV1Encoder) configureAppSink() error {
	e.appSink.SetProperty("emit-signals", true)
	e.appSink.SetProperty("sync", false)
	e.appSink.SetProperty("max-buffers", uint(100))
	e.appSink.SetProperty("drop", false)

	// Set caps for AV1 output
	caps := gst.NewCapsFromString("video/x-av1")
	e.appSink.SetCaps(caps)

	// Set callback for encoded samples
	e.appSink.SetCallbacks(&app.SinkCallbacks{
		NewSampleFunc: func(sink *app.Sink) gst.FlowReturn {
			sample := sink.PullSample()
			if sample == nil {
				return gst.FlowEOS
			}

			buffer := sample.GetBuffer()
			if buffer == nil {
				return gst.FlowError
			}

			// Extract encoded data
			data := buffer.Bytes()
			if len(data) > 0 {
				// Make a copy of the data
				encoded := make([]byte, len(data))
				copy(encoded, data)

				// Update metrics
				e.bytesEncoded.Add(uint64(len(encoded)))

				// Send to channel (non-blocking)
				select {
				case e.encodedFrames <- encoded:
				default:
					log.Println("Warning: encoded frame buffer full, dropping frame")
				}
			}

			return gst.FlowOK
		},
	})

	return nil
}

// Encode encodes a single frame
func (e *GStreamerAV1Encoder) Encode(frame image.Image, pts time.Duration) ([]byte, error) {
	if !e.running.Load() {
		return nil, fmt.Errorf("encoder not running")
	}

	e.frameCount.Add(1)

	// Convert image.Image to raw bytes
	bounds := frame.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	if width != e.config.Width || height != e.config.Height {
		return nil, fmt.Errorf("frame size mismatch: got %dx%d, expected %dx%d",
			width, height, e.config.Width, e.config.Height)
	}

	// Allocate buffer for RGB data
	dataSize := width * height * 3
	data := make([]byte, dataSize)

	// Convert image to RGB bytes
	idx := 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := frame.At(x, y).RGBA()
			data[idx] = byte(r >> 8)
			data[idx+1] = byte(g >> 8)
			data[idx+2] = byte(b >> 8)
			idx += 3
		}
	}

	// Create GStreamer buffer
	buffer := gst.NewBufferFromBytes(data)
	buffer.SetPresentationTimestamp(gst.ClockTime(pts.Nanoseconds()))
	buffer.SetDuration(gst.ClockTime(time.Second.Nanoseconds() / int64(e.config.FrameRate)))

	// Push to pipeline
	if ret := e.appSrc.PushBuffer(buffer); ret != gst.FlowOK {
		return nil, fmt.Errorf("failed to push buffer: %v", ret)
	}

	// Try to get encoded frame (non-blocking)
	select {
	case encoded := <-e.encodedFrames:
		return encoded, nil
	case <-time.After(100 * time.Millisecond):
		// No encoded frame yet, return nil (frame is being processed)
		return nil, nil
	}
}

// Flush flushes any remaining encoded frames
func (e *GStreamerAV1Encoder) Flush() ([][]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Send EOS to pipeline
	e.appSrc.EndStream()

	// Collect all remaining frames
	var frames [][]byte
	timeout := time.After(5 * time.Second)

	for {
		select {
		case frame := <-e.encodedFrames:
			frames = append(frames, frame)
		case <-timeout:
			log.Printf("Flush timeout, collected %d frames", len(frames))
			return frames, nil
		case <-time.After(100 * time.Millisecond):
			// No more frames
			return frames, nil
		}
	}
}

// Close closes the encoder and releases resources
func (e *GStreamerAV1Encoder) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.running.Load() {
		return nil
	}

	e.running.Store(false)

	// Stop pipeline
	if e.pipeline != nil {
		e.pipeline.SetState(gst.StateNull)
		e.pipeline.Unref()
	}

	// Cancel context
	e.cancel()

	// Close channel
	close(e.encodedFrames)

	log.Println("GStreamer AV1 encoder closed")
	return nil
}

// GetMetrics returns current encoder metrics
func (e *GStreamerAV1Encoder) GetMetrics() *EncoderMetrics {
	e.mu.RLock()
	defer e.mu.RUnlock()

	return &EncoderMetrics{
		FramesEncoded:       e.frameCount.Load(),
		BytesEncoded:        e.bytesEncoded.Load(),
		KeyFrames:           e.keyFrames.Load(),
		HardwareAccelerated: false, // AV1 is software encoding
		CurrentBitrate:      float64(e.bytesEncoded.Load() * 8 / (e.frameCount.Load() / uint64(e.config.FrameRate))),
	}
}

// Ensure GStreamerAV1Encoder implements Encoder interface
var _ Encoder = (*GStreamerAV1Encoder)(nil)

// Helper to suppress unused import warning
var _ = unsafe.Pointer(nil)
