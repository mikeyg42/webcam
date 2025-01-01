package video

import (
	"encoding/binary"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/at-wat/ebml-go/webm"
	"github.com/pion/webrtc/v4"

	"gocv.io/x/gocv"
)

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
}

func NewRecorder(config *VideoConfig) *Recorder {
	return &Recorder{
		config: config,
		done:   make(chan struct{}),
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

func (r *Recorder) Close() error {
	close(r.done)
	return r.StopRecording()
}

func (r *Recorder) HandleTrack(track *webrtc.TrackRemote) error {
	r.mu.Lock()
	// var trackNum uint64
	switch track.Kind() {
	case webrtc.RTPCodecTypeVideo:
		r.videoTrack = track
		// trackNum = 1
	case webrtc.RTPCodecTypeAudio:
		r.audioTrack = track
		// trackNum = 2
	}
	r.mu.Unlock()

	buffer := make([]byte, 1500) // Standard MTU size
	var frame []byte
	var keyframe bool
	var tcRawLast int64 = -1
	var tcRawBase int64

	writer, ok := r.webmWriter.(webm.BlockWriter)
	if !ok {
		log.Printf("WebM writer does not support BlockWriter")
		return fmt.Errorf("WebM writer does not support BlockWriter")
	}

	for {
		select {
		case <-r.done:
			return nil
		default:
			n, _, err := track.Read(buffer)
			if err != nil {
				log.Printf("Error reading from track: %v", err)
				return err
			}

			if n < 14 {
				log.Print("RTP packet size is too small")
				continue
			}

			r.mu.Lock()
			if r.isRecording && writer != nil {
				if track.Kind() == webrtc.RTPCodecTypeVideo {
					// RTP header 12 bytes, VP8 payload descriptor 1 byte
					vp8Desc := buffer[12]

					// Check for extended control bits
					if vp8Desc&0x80 != 0 {
						log.Printf("Warning: Extended control bits not supported")
						r.mu.Unlock()
						continue
					}

					// Start of VP8 partition
					if vp8Desc&0x10 != 0 {
						if len(frame) > 0 {
							tcRaw := int64(binary.BigEndian.Uint32(buffer[4:8]))
							if tcRawLast == -1 {
								tcRawLast = tcRaw
							}
							if tcRaw < 0x10000 && tcRawLast > 0xFFFFFFFF-0x10000 {
								// counter overflow
								tcRawBase += 0x100000000
							} else if tcRawLast < 0x10000 && tcRaw > 0xFFFFFFFF-0x10000 {
								// counter underflow
								tcRawBase -= 0x100000000
							}
							tcRawLast = tcRaw

							tc := (tcRaw + tcRawBase) / 90 // VP8 timestamp rate is 90000

							_, err := writer.Write(
								keyframe,
								tc,
								frame,
							)
							if err != nil {
								log.Printf("Error writing video frame...not closing though -- persisting! see: %v", err)
							}
						}
						frame = []byte{}
						keyframe = false

						// Check for keyframe
						vp8Header := buffer[13]
						if vp8Header&0x01 == 0 {
							keyframe = true
						}
					}
					frame = append(frame, buffer[13:n]...)
				} else {
					// Audio handling
					tcRaw := int64(binary.BigEndian.Uint32(buffer[4:8]))
					tc := tcRaw / 48 // Opus timestamp rate is 48000

					_, err := writer.Write(
						false, // Audio frames don't have keyframes
						tc,
						buffer[12:n], // Skip RTP header
					)
					if err != nil {
						log.Printf("Error writing audio frame...not closing though -- persisting! see: %v", err)

					}
				}
			}
			r.mu.Unlock()
		}
	}
}
