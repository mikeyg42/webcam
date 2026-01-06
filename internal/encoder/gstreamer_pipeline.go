package encoder

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"log"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-gst/go-gst/gst"
	"github.com/go-gst/go-gst/gst/app"
	"github.com/pion/rtp"
)

// Buffer pool for RGB conversion
var rgbBufferPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, 1280*720*3)
	},
}

// GStreamerPipeline manages the GStreamer encoding pipeline
type GStreamerPipeline struct {
	config   EncoderConfig
	pipeline *gst.Pipeline

	// Input
	appSrc *app.Source

	// Outputs
	rawSink1 *app.Sink // Motion detection
	rawSink2 *app.Sink // Recording
	rtpSink  *app.Sink // WebRTC RTP packets

	// Output channels
	motionChan chan image.Image
	recordChan chan image.Image
	rtpChan    chan *rtp.Packet

	// Statistics
	stats *EncoderStats

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Frame timing
	frameIdx       uint64
	frameDur       time.Duration
	startMonotonic time.Time

	// Keyframe monitoring
	lastKeyframeTime     time.Time
	keyframeIntervalWarn time.Duration

	// Thread safety
	mu sync.Mutex

	// Dynamic bitrate control
	encoder     *gst.Element
	encoderKind encoderKind
}

// NewGStreamerPipeline creates a new encoder pipeline
func NewGStreamerPipeline(cfg EncoderConfig) (*GStreamerPipeline, error) {
	gst.Init(nil)

	// Validate config
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	p := &GStreamerPipeline{
		config:               cfg,
		motionChan:           make(chan image.Image, 8),
		recordChan:           make(chan image.Image, 8),
		rtpChan:              make(chan *rtp.Packet, 128),
		stats:                &EncoderStats{},
		frameDur:             time.Second / time.Duration(max(1, cfg.FrameRate)),
		keyframeIntervalWarn: 5 * time.Second,
	}
	return p, nil
}

// Start builds and starts the pipeline
func (g *GStreamerPipeline) Start(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.pipeline != nil {
		return errors.New("pipeline already started")
	}

	g.ctx, g.cancel = context.WithCancel(ctx)

	// Create pipeline
	pipe, err := gst.NewPipeline("enc-pipeline")
	if err != nil {
		return fmt.Errorf("failed to create pipeline: %w", err)
	}

	// Build pipeline elements
	if err := g.buildPipeline(pipe); err != nil {
		return fmt.Errorf("failed to build pipeline: %w", err)
	}

	g.pipeline = pipe
	g.startMonotonic = time.Now()

	// Setup sink callbacks
	g.setupRawSinks()
	
	// Monitor bus
	bus := pipe.GetBus()
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		g.monitorBus(bus)
	}()

	// Start pipeline
	if err := pipe.SetState(gst.StatePlaying); err != nil {
		return fmt.Errorf("failed to set PLAYING state: %w", err)
	}

	log.Println("GStreamer pipeline started successfully")
	return nil
}

// Stop gracefully stops the pipeline
func (g *GStreamerPipeline) Stop() {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.pipeline == nil {
		return
	}

	// Cancel context
	if g.cancel != nil {
		g.cancel()
	}

	// Send EOS and wait briefly
	g.pipeline.SendEvent(gst.NewEOSEvent())
	time.Sleep(100 * time.Millisecond)

	// Stop pipeline
	g.pipeline.SetState(gst.StateNull)

	// Wait for goroutines with timeout
	done := make(chan struct{})
	go func() {
		g.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Goroutines completed successfully
	case <-time.After(5 * time.Second):
		log.Println("Warning: Pipeline stop timeout - force closing")
	}

	// Close channels
	close(g.motionChan)
	close(g.recordChan)
	close(g.rtpChan)

	g.pipeline = nil

	// Log final statistics
	log.Printf("Pipeline stopped - frames: %d, packets: %d, dropped: %d",
		g.stats.GetFramesIn(),
		g.stats.GetPacketsOut(),
		g.stats.GetDroppedFrames())
}

// FeedFrame sends a frame to the encoder
func (g *GStreamerPipeline) FeedFrame(img image.Image) error {
	g.mu.Lock()
	pipeline := g.pipeline
	appSrc := g.appSrc
	g.mu.Unlock()

	if pipeline == nil || appSrc == nil {
		return errors.New("pipeline not started")
	}

	// Convert to RGB
	rgba := imageToRGBA(img)
	expectedSize := rgba.Bounds().Dx() * rgba.Bounds().Dy() * 3
	
	// Get buffer from pool
	rgbBuf := rgbBufferPool.Get().([]byte)
	if cap(rgbBuf) < expectedSize {
		rgbBuf = make([]byte, expectedSize)
	} else {
		rgbBuf = rgbBuf[:expectedSize]
	}
	defer rgbBufferPool.Put(rgbBuf)

	// Convert RGBA to RGB
	rgbaToRGBBuffer(rgba, rgbBuf)

	// Create GStreamer buffer
	buf := gst.NewBufferFromBytes(rgbBuf)
	pts := gst.ClockTime(time.Duration(atomic.LoadUint64(&g.frameIdx)) * g.frameDur)
	buf.SetPresentationTimestamp(pts)
	buf.SetDuration(gst.ClockTime(g.frameDur))
	atomic.AddUint64(&g.frameIdx, 1)

	// Push to pipeline
	if ret := appSrc.PushBuffer(buf); ret != gst.FlowOK {
		g.stats.IncrementDroppedFrames()
		return fmt.Errorf("push buffer failed: %s", ret.String())
	}

	g.stats.IncrementFramesIn()
	return nil
}

// GetMotionChannel returns the motion detection frame channel
func (g *GStreamerPipeline) GetMotionChannel() <-chan image.Image {
	return g.motionChan
}

// GetRecordingChannel returns the recording frame channel
func (g *GStreamerPipeline) GetRecordingChannel() <-chan image.Image {
	return g.recordChan
}

// GetRTPChannel returns the RTP packet channel
func (g *GStreamerPipeline) GetRTPChannel() <-chan *rtp.Packet {
	return g.rtpChan
}

// GetStats returns current statistics
func (g *GStreamerPipeline) GetStats() EncoderStats {
	// Return current stats (values are already atomic)
	return *g.stats
}

// buildPipeline constructs the GStreamer pipeline
func (g *GStreamerPipeline) buildPipeline(pipe *gst.Pipeline) error {
	// Create elements
	srcElem, err := gst.NewElement("appsrc")
	if err != nil {
		return fmt.Errorf("create appsrc: %w", err)
	}
	g.appSrc = app.SrcFromElement(srcElem)

	conv, err := gst.NewElement("videoconvert")
	if err != nil {
		return fmt.Errorf("create videoconvert: %w", err)
	}

	tee, err := gst.NewElement("tee")
	if err != nil {
		return fmt.Errorf("create tee: %w", err)
	}

	// Create queues for each branch
	qA, err := g.createQueue("queue-motion", 10)
	if err != nil {
		return err
	}

	qB, err := g.createQueue("queue-record", 10)
	if err != nil {
		return err
	}

	qC, err := g.createQueue("queue-encode", 30)
	if err != nil {
		return err
	}

	// Create videoconvert for encoder branch (encoder needs NV12/I420, not RGB)
	convEnc, err := gst.NewElement("videoconvert")
	if err != nil {
		return fmt.Errorf("create encoder videoconvert: %w", err)
	}

	// Create sinks
	rawSinkA, err := gst.NewElement("appsink")
	if err != nil {
		return fmt.Errorf("create appsink A: %w", err)
	}
	g.rawSink1 = app.SinkFromElement(rawSinkA)

	rawSinkB, err := gst.NewElement("appsink")
	if err != nil {
		return fmt.Errorf("create appsink B: %w", err)
	}
	g.rawSink2 = app.SinkFromElement(rawSinkB)

	// Create encoder
	enc, encKind := g.chooseEncoder()
	if enc == nil {
		return fmt.Errorf("no usable encoder found")
	}

	// Store encoder reference for dynamic bitrate control
	g.encoder = enc
	g.encoderKind = encKind

	// Create RTP payloader
	rtpPay, caps, err := g.makeRTPPay(encKind)
	if err != nil {
		return err
	}

	rtpOut, err := gst.NewElement("appsink")
	if err != nil {
		return fmt.Errorf("create RTP appsink: %w", err)
	}
	g.rtpSink = app.SinkFromElement(rtpOut)

	// Add all elements to pipeline
	if err := pipe.AddMany(srcElem, conv, tee, qA, rawSinkA, qB, rawSinkB, qC, convEnc, enc, rtpPay, rtpOut); err != nil {
		return fmt.Errorf("add elements: %w", err)
	}

	// Configure appsrc
	if err := g.configureAppSrc(); err != nil {
		return err
	}

	// Link main path
	if err := srcElem.Link(conv); err != nil {
		return fmt.Errorf("link src->conv: %w", err)
	}
	if err := conv.Link(tee); err != nil {
		return fmt.Errorf("link conv->tee: %w", err)
	}

	// Link branches
	if err := g.linkBranches(tee, qA, rawSinkA, qB, rawSinkB, qC, convEnc, enc, rtpPay, rtpOut); err != nil {
		return err
	}

	// Setup RTP sink with encoder info
	if err := g.setupRTPSink(caps, encKind); err != nil {
		return err
	}

	return nil
}

// createQueue creates a queue with proper settings
func (g *GStreamerPipeline) createQueue(name string, maxBuffers uint) (*gst.Element, error) {
	q, err := gst.NewElement("queue")
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", name, err)
	}
	
	_ = q.SetProperty("max-size-buffers", maxBuffers)
	_ = q.SetProperty("max-size-bytes", uint(0))
	_ = q.SetProperty("max-size-time", uint64(0))
	_ = q.SetProperty("leaky", 2) // Drop old buffers if full
	
	return q, nil
}

// linkBranches links all pipeline branches
func (g *GStreamerPipeline) linkBranches(tee, qA, sinkA, qB, sinkB, qC, convEnc, enc, pay, rtpOut *gst.Element) error {
	// Link motion branch
	if err := linkTeeToQueue(tee, qA); err != nil {
		return fmt.Errorf("link tee->qA: %w", err)
	}
	if err := qA.Link(sinkA); err != nil {
		return fmt.Errorf("link qA->sinkA: %w", err)
	}

	// Link recording branch
	if err := linkTeeToQueue(tee, qB); err != nil {
		return fmt.Errorf("link tee->qB: %w", err)
	}
	if err := qB.Link(sinkB); err != nil {
		return fmt.Errorf("link qB->sinkB: %w", err)
	}

	// Link encode branch (queue → videoconvert → encoder → payloader → sink)
	if err := linkTeeToQueue(tee, qC); err != nil {
		return fmt.Errorf("link tee->qC: %w", err)
	}
	if err := qC.Link(convEnc); err != nil {
		return fmt.Errorf("link qC->convEnc: %w", err)
	}
	if err := convEnc.Link(enc); err != nil {
		return fmt.Errorf("link convEnc->enc: %w", err)
	}
	if err := enc.Link(pay); err != nil {
		return fmt.Errorf("link enc->pay: %w", err)
	}
	if err := pay.Link(rtpOut); err != nil {
		return fmt.Errorf("link pay->rtpOut: %w", err)
	}

	return nil
}

// configureAppSrc sets up the appsrc element
func (g *GStreamerPipeline) configureAppSrc() error {
	capsStr := fmt.Sprintf(
		"video/x-raw,format=RGB,width=%d,height=%d,framerate=%d/1",
		g.config.Width, g.config.Height, max(1, g.config.FrameRate),
	)

	caps := gst.NewCapsFromString(capsStr)

	g.appSrc.SetCaps(caps)
	g.appSrc.SetStreamType(app.AppStreamTypeStream)
	g.appSrc.SetLatency(0, uint64(2*time.Second))
	g.appSrc.SetProperty("format", gst.FormatTime)
	g.appSrc.SetProperty("is-live", true)
	g.appSrc.SetProperty("block", false)
	
	return nil
}

// Encoder types
type encoderKind int

const (
	encH264NVENC encoderKind = iota
	encH264VAAPI
	encH264VideoToolbox
	encH264X264
	encH265NVENC
	encH265VAAPI
	encH265VideoToolbox
	encH265X265
	encAV1SVT
	encAV1RAV1E
)

// IsHardwareEncoder returns true if the encoder uses hardware acceleration
func (g *GStreamerPipeline) IsHardwareEncoder() bool {
	switch g.encoderKind {
	case encH264NVENC, encH264VAAPI, encH264VideoToolbox,
		encH265NVENC, encH265VAAPI, encH265VideoToolbox:
		return true
	default:
		return false
	}
}

// GetCodecMimeType returns the WebRTC MIME type for the current encoder
func (g *GStreamerPipeline) GetCodecMimeType() string {
	switch g.encoderKind {
	case encH265NVENC, encH265VAAPI, encH265VideoToolbox, encH265X265:
		return "video/H265"
	case encH264NVENC, encH264VAAPI, encH264VideoToolbox, encH264X264:
		return "video/H264"
	case encAV1SVT, encAV1RAV1E:
		return "video/AV1"
	default:
		return "video/H264" // Default fallback
	}
}

// chooseEncoder selects the best available encoder based on resolution
func (g *GStreamerPipeline) chooseEncoder() (*gst.Element, encoderKind) {
	// Check if resolution is 4K or higher
	is4K := g.config.Width*g.config.Height >= 3840*2160

	// For 4K+ resolutions, prefer H.265/HEVC
	if is4K {
		log.Printf("4K+ resolution detected (%dx%d), trying H.265 encoders", g.config.Width, g.config.Height)

		// On macOS, prefer H265 VideoToolbox
		if runtime.GOOS == "darwin" {
			if e, err := gst.NewElement("vtenc_h265_hw"); err == nil && e != nil {
				g.configureVideoToolboxH265(e)
				log.Println("Using VideoToolbox H.265 hardware encoder")
				return e, encH265VideoToolbox
			}
		}

		// Try hardware H.265 encoders (non-macOS)
		if g.config.PreferHardware {
			// NVIDIA NVENC H.265
			if e, err := gst.NewElement("nvh265enc"); err == nil && e != nil {
				g.configureNVENCH265(e)
				log.Println("Using NVENC H.265 hardware encoder")
				return e, encH265NVENC
			}

			// Intel/AMD VAAPI H.265
			if e, err := gst.NewElement("vah265enc"); err == nil && e != nil {
				g.configureVAAPIH265(e)
				log.Println("Using VA-API H.265 hardware encoder")
				return e, encH265VAAPI
			}
		}

		// Software H.265 fallback for 4K
		if e, err := gst.NewElement("x265enc"); err == nil && e != nil {
			g.configureX265(e)
			log.Println("Using x265 software encoder for 4K")
			return e, encH265X265
		}

		log.Println("H.265 encoders not available, falling back to H.264 for 4K")
	}

	// For sub-4K resolutions or H.265 fallback, use H.264
	log.Printf("Using H.264 encoder for %dx%d resolution", g.config.Width, g.config.Height)

	// On macOS, prefer H264 VideoToolbox
	if runtime.GOOS == "darwin" {
		if e, err := gst.NewElement("vtenc_h264_hw"); err == nil && e != nil {
			g.configureVideoToolbox(e)
			log.Println("Using VideoToolbox H.264 hardware encoder")
			return e, encH264VideoToolbox
		}
	}

	// Try hardware H.264 encoders (non-macOS)
	if g.config.PreferHardware {
		// NVIDIA NVENC H.264
		if e, err := gst.NewElement("nvh264enc"); err == nil && e != nil {
			g.configureNVENC(e)
			log.Println("Using NVENC H.264 hardware encoder")
			return e, encH264NVENC
		}

		// Intel/AMD VAAPI H.264
		if e, err := gst.NewElement("vah264enc"); err == nil && e != nil {
			g.configureVAAPI(e)
			log.Println("Using VA-API H.264 hardware encoder")
			return e, encH264VAAPI
		}
	}

	// Software H.264 fallback
	if e, err := gst.NewElement("x264enc"); err == nil && e != nil {
		g.configureX264(e)
		log.Println("Using x264 software encoder")
		return e, encH264X264
	}

	return nil, encH264X264
}

// Configure encoder functions
func (g *GStreamerPipeline) configureNVENC(e *gst.Element) {
	_ = e.SetProperty("rc-mode", uint32(0))
	_ = e.SetProperty("bitrate", uint(g.config.BitRateKbps))
	_ = e.SetProperty("gop-size", int(g.config.KeyFrameInterval))
}

func (g *GStreamerPipeline) configureVAAPI(e *gst.Element) {
	_ = e.SetProperty("bitrate", uint(g.config.BitRateKbps))
}

func (g *GStreamerPipeline) configureVideoToolbox(e *gst.Element) {
	_ = e.SetProperty("bitrate", uint(g.config.BitRateKbps))
	_ = e.SetProperty("max-keyframe-interval", uint(g.config.KeyFrameInterval))
	_ = e.SetProperty("allow-frame-reordering", false)
}

func (g *GStreamerPipeline) configureX264(e *gst.Element) {
	_ = e.SetProperty("bitrate", uint(g.config.BitRateKbps))
	_ = e.SetProperty("key-int-max", uint(g.config.KeyFrameInterval))
	_ = e.SetProperty("speed-preset", "medium")
}

// H.265/HEVC encoder configurations
func (g *GStreamerPipeline) configureNVENCH265(e *gst.Element) {
	_ = e.SetProperty("rc-mode", uint32(0))
	_ = e.SetProperty("bitrate", uint(g.config.BitRateKbps))
	_ = e.SetProperty("gop-size", int(g.config.KeyFrameInterval))
}

func (g *GStreamerPipeline) configureVAAPIH265(e *gst.Element) {
	_ = e.SetProperty("bitrate", uint(g.config.BitRateKbps))
}

func (g *GStreamerPipeline) configureVideoToolboxH265(e *gst.Element) {
	_ = e.SetProperty("bitrate", uint(g.config.BitRateKbps))
	_ = e.SetProperty("max-keyframe-interval", uint(g.config.KeyFrameInterval))
	_ = e.SetProperty("allow-frame-reordering", false)
}

func (g *GStreamerPipeline) configureX265(e *gst.Element) {
	_ = e.SetProperty("bitrate", uint(g.config.BitRateKbps))
	_ = e.SetProperty("key-int-max", uint(g.config.KeyFrameInterval))
	_ = e.SetProperty("speed-preset", "medium")
}

// AV1 encoder configurations
func (g *GStreamerPipeline) configureSVTAV1(e *gst.Element) {
	_ = e.SetProperty("target-bitrate", uint(g.config.BitRateKbps*1000))
	_ = e.SetProperty("gop-size", int(g.config.KeyFrameInterval))
	_ = e.SetProperty("preset", uint(7))
}

func (g *GStreamerPipeline) configureRAV1E(e *gst.Element) {
	_ = e.SetProperty("bitrate", uint(g.config.BitRateKbps))
	_ = e.SetProperty("keyframe-interval", uint(g.config.KeyFrameInterval))
	_ = e.SetProperty("speed-preset", uint(7))
}

// makeRTPPay creates the RTP payloader
func (g *GStreamerPipeline) makeRTPPay(kind encoderKind) (*gst.Element, *gst.Caps, error) {
	switch kind {
	case encH265NVENC, encH265VAAPI, encH265VideoToolbox, encH265X265:
		pay, err := gst.NewElement("rtph265pay")
		if err != nil {
			return nil, nil, fmt.Errorf("create rtph265pay: %w", err)
		}
		_ = pay.SetProperty("config-interval", 1)
		c := gst.NewCapsFromString("application/x-rtp,media=video,encoding-name=H265,clock-rate=90000")
		return pay, c, nil

	default: // H.264 encoders
		pay, err := gst.NewElement("rtph264pay")
		if err != nil {
			return nil, nil, fmt.Errorf("create rtph264pay: %w", err)
		}
		_ = pay.SetProperty("config-interval", 1)
		c := gst.NewCapsFromString("application/x-rtp,media=video,encoding-name=H264,clock-rate=90000,packetization-mode=1,profile-level-id=42e01f")
		return pay, c, nil
	}
}

// setupRawSinks configures raw frame output sinks
func (g *GStreamerPipeline) setupRawSinks() {
	// Motion detection sink
	g.rawSink1.SetEmitSignals(true)
	g.rawSink1.SetProperty("max-buffers", uint(2))
	g.rawSink1.SetProperty("drop", true)
	g.rawSink1.SetCallbacks(&app.SinkCallbacks{
		NewSampleFunc: g.handleMotionSample,
	})

	// Recording sink
	g.rawSink2.SetEmitSignals(true)
	g.rawSink2.SetProperty("max-buffers", uint(2))
	g.rawSink2.SetProperty("drop", true)
	g.rawSink2.SetCallbacks(&app.SinkCallbacks{
		NewSampleFunc: g.handleRecordingSample,
	})
}

// setupRTPSink configures the RTP output sink
func (g *GStreamerPipeline) setupRTPSink(caps *gst.Caps, kind encoderKind) error {
	g.rtpSink.SetEmitSignals(true)
	g.rtpSink.SetCaps(caps)
	g.rtpSink.SetProperty("max-buffers", uint(50))
	g.rtpSink.SetProperty("drop", false)
	
	g.rtpSink.SetCallbacks(&app.SinkCallbacks{
		NewSampleFunc: func(s *app.Sink) gst.FlowReturn {
			return g.handleRTPSample(s, kind)
		},
	})
	
	return nil
}

// Sample handlers
func (g *GStreamerPipeline) handleMotionSample(s *app.Sink) gst.FlowReturn {
	return g.handleRawSample(s, g.motionChan)
}

func (g *GStreamerPipeline) handleRecordingSample(s *app.Sink) gst.FlowReturn {
	return g.handleRawSample(s, g.recordChan)
}

func (g *GStreamerPipeline) handleRawSample(s *app.Sink, outChan chan image.Image) gst.FlowReturn {
	sample := s.PullSample()
	if sample == nil {
		return gst.FlowOK
	}
	defer sample.Unref()

	buf := sample.GetBuffer()
	if buf == nil {
		return gst.FlowOK
	}

	mapping := buf.Map(gst.MapRead)
	if mapping != nil {
		defer buf.Unmap()

		img := rgbBytesToRGBA(mapping.Bytes(), g.config.Width, g.config.Height)

		select {
		case outChan <- img:
		default:
			g.stats.IncrementDroppedFrames()
		}
	}
	
	return gst.FlowOK
}

func (g *GStreamerPipeline) handleRTPSample(s *app.Sink, kind encoderKind) gst.FlowReturn {
	sample := s.PullSample()
	if sample == nil {
		return gst.FlowOK
	}
	defer sample.Unref()

	buf := sample.GetBuffer()
	if buf == nil {
		return gst.FlowOK
	}

	mapping := buf.Map(gst.MapRead); 
	defer buf.Unmap()
	
	var pkt rtp.Packet
	if err := pkt.Unmarshal(mapping.Bytes()); err == nil {
		// Check for keyframe
		if g.isKeyframe(&pkt, kind) {
			now := time.Now()
			if !g.lastKeyframeTime.IsZero() {
				interval := now.Sub(g.lastKeyframeTime)
				if interval > g.keyframeIntervalWarn {
					log.Printf("Warning: keyframe interval too long: %v", interval)
				}
			}
			g.lastKeyframeTime = now
			g.stats.SetLastKeyframe(now)
		}
		
		select {
		case g.rtpChan <- &pkt:
			g.stats.IncrementPacketsOut()
			g.stats.AddBytesEncoded(uint64(len(mapping.Bytes())))
		default:
			g.stats.IncrementDroppedFrames()
		}
	}
	
	return gst.FlowOK
}

// isKeyframe detects keyframes in RTP packets
func (g *GStreamerPipeline) isKeyframe(pkt *rtp.Packet, kind encoderKind) bool {
	if len(pkt.Payload) < 2 {
		return false
	}

	switch kind {
	case encH264NVENC, encH264VAAPI, encH264VideoToolbox, encH264X264:
		// H.264: NAL unit type 5 = IDR
		nalType := pkt.Payload[0] & 0x1F
		return nalType == 5

	case encH265NVENC, encH265VAAPI, encH265VideoToolbox, encH265X265:
		// H.265: NAL unit types for keyframes
		// Extract NAL unit type from first two bytes
		nalType := (pkt.Payload[0] >> 1) & 0x3F
		// IDR_W_RADL = 19, IDR_N_LP = 20, CRA_NUT = 21
		return nalType == 19 || nalType == 20 || nalType == 21

	default:
		return false
	}
}

// monitorBus monitors GStreamer bus messages
func (g *GStreamerPipeline) monitorBus(bus *gst.Bus) {
	for {
		msg := bus.TimedPop(gst.ClockTime(100 * time.Millisecond))
		if msg == nil {
			select {
			case <-g.ctx.Done():
				return
			default:
				continue
			}
		}

		// Get source element name for better diagnostics
		source := msg.Source()
		sourceName := "unknown"
		if source != "" {
			sourceName = source
		}

		switch msg.Type() {
		case gst.MessageEOS:
			log.Printf("[H.264 Pipeline] EOS received from element: %s", sourceName)
			return

		case gst.MessageError:
			err := msg.ParseError()
			log.Printf("[H.264 Pipeline] ERROR from element '%s': %v", sourceName, err)

		case gst.MessageWarning:
			err := msg.ParseWarning()
			log.Printf("[H.264 Pipeline] WARNING from element '%s': %v", sourceName, err)

		case gst.MessageStateChanged:
			// Only log state changes for the pipeline itself to reduce noise
			if sourceName == "enc-pipeline" {
				oldState, newState := msg.ParseStateChanged()
				log.Printf("[H.264 Pipeline] State: %s -> %s", oldState.String(), newState.String())
			}
		}
	}
}

// Helper functions
func linkTeeToQueue(tee, queue *gst.Element) error {
	srcPad := tee.GetRequestPad("src_%u")
	if srcPad == nil {
		return errors.New("tee request pad failed")
	}
	
	sinkPad := queue.GetStaticPad("sink")
	if sinkPad == nil {
		return errors.New("queue sink pad missing")
	}
	
	if st := srcPad.Link(sinkPad); st != gst.PadLinkOK {
		return fmt.Errorf("pad link failed: %s", st.String())
	}
	
	return nil
}

func imageToRGBA(img image.Image) *image.RGBA {
	if m, ok := img.(*image.RGBA); ok {
		return m
	}
	b := img.Bounds()
	r := image.NewRGBA(b)
	draw.Draw(r, b, img, b.Min, draw.Src)
	return r
}

func rgbaToRGBBuffer(rgba *image.RGBA, rgb []byte) {
	w, h := rgba.Bounds().Dx(), rgba.Bounds().Dy()
	pi := 0
	for y := 0; y < h; y++ {
		row := rgba.Pix[y*rgba.Stride : y*rgba.Stride+w*4]
		for x := 0; x < w; x++ {
			rgb[pi+0] = row[x*4+0]
			rgb[pi+1] = row[x*4+1]
			rgb[pi+2] = row[x*4+2]
			pi += 3
		}
	}
}

func rgbBytesToRGBA(b []byte, width, height int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	i := 0
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if i+2 >= len(b) {
				break
			}
			img.SetRGBA(x, y, color.RGBA{R: b[i], G: b[i+1], B: b[i+2], A: 0xff})
			i += 3
		}
	}
	return img
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// SetBitrate dynamically adjusts encoder bitrate at runtime
// This is called by QualityManager for adaptive quality control
func (g *GStreamerPipeline) SetBitrate(bitrateKbps int) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.encoder == nil {
		return errors.New("encoder not initialized")
	}

	if g.pipeline == nil {
		return errors.New("pipeline not initialized")
	}

	// Sanity check bitrate bounds
	const minBitrate = 100   // 100 Kbps absolute minimum
	const maxBitrate = 15000 // 15 Mbps absolute maximum
	if bitrateKbps < minBitrate || bitrateKbps > maxBitrate {
		return fmt.Errorf("bitrate %d Kbps out of safe range [%d, %d]", bitrateKbps, minBitrate, maxBitrate)
	}

	// Check if pipeline is playing
	currentState := g.pipeline.GetCurrentState()
	if currentState != gst.StatePlaying {
		return errors.New("pipeline not running")
	}

	log.Printf("[GStreamerPipeline] Adjusting encoder bitrate: %d -> %d Kbps", g.config.BitRateKbps, bitrateKbps)

	// Update encoder based on type
	var err error
	switch g.encoderKind {
	case encH264NVENC, encH265NVENC:
		// NVIDIA NVENC supports runtime bitrate changes
		err = g.encoder.SetProperty("bitrate", uint(bitrateKbps))
		if err != nil {
			return fmt.Errorf("failed to set NVENC bitrate: %w", err)
		}

	case encH264VAAPI, encH265VAAPI:
		// VA-API supports runtime bitrate changes
		err = g.encoder.SetProperty("bitrate", uint(bitrateKbps))
		if err != nil {
			return fmt.Errorf("failed to set VAAPI bitrate: %w", err)
		}

	case encH264VideoToolbox, encH265VideoToolbox:
		// VideoToolbox supports runtime bitrate changes
		err = g.encoder.SetProperty("bitrate", uint(bitrateKbps))
		if err != nil {
			return fmt.Errorf("failed to set VideoToolbox bitrate: %w", err)
		}

	case encH264X264, encH265X265:
		// Software encoders (x264/x265) support runtime bitrate changes
		err = g.encoder.SetProperty("bitrate", uint(bitrateKbps))
		if err != nil {
			return fmt.Errorf("failed to set x264/x265 bitrate: %w", err)
		}

	case encAV1SVT, encAV1RAV1E:
		// AV1 encoders - check support
		err = g.encoder.SetProperty("bitrate", uint(bitrateKbps))
		if err != nil {
			log.Printf("[GStreamerPipeline] Warning: AV1 encoder may not support runtime bitrate changes: %v", err)
			return fmt.Errorf("failed to set AV1 bitrate: %w", err)
		}

	default:
		return fmt.Errorf("unsupported encoder type for runtime bitrate adjustment: %d", g.encoderKind)
	}

	// Update config to reflect new bitrate
	g.config.BitRateKbps = bitrateKbps
	log.Printf("[GStreamerPipeline] Bitrate adjusted successfully to %d Kbps", bitrateKbps)

	return nil
}

// GetCurrentBitrate returns the current configured bitrate
func (g *GStreamerPipeline) GetCurrentBitrate() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.config.BitRateKbps
}

// SupportsDynamicBitrate checks if the current encoder supports runtime bitrate changes
func (g *GStreamerPipeline) SupportsDynamicBitrate() bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	switch g.encoderKind {
	case encH264NVENC, encH265NVENC,
		encH264VAAPI, encH265VAAPI,
		encH264VideoToolbox, encH265VideoToolbox,
		encH264X264, encH265X265:
		return true
	default:
		return false
	}
}