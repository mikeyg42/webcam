// SPDX-FileCopyright...

package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/3d0c/gmf"
	"github.com/pion/interceptor"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"gocv.io/x/gocv"
)

func main() {
	port := flag.Int("port", 8080, "http server port")
	flag.Parse()

	sdpChan := httpSDPServer(*port)

	// Everything below is the Pion WebRTC API, thanks for using it ❤️.
	offer := webrtc.SessionDescription{}
	decode(<-sdpChan, &offer)
	fmt.Println("")

	peerConnectionConfig := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	}

	m := &webrtc.MediaEngine{}

	// Register VP9 Codec
	vp9Codec := webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:     webrtc.MimeTypeVP9,
			ClockRate:    90000,
			Channels:     0,
			SDPFmtpLine:  "",
			RTCPFeedback: nil,
		},
		PayloadType: 100, // Dynamic payload type
	}
	if err := m.RegisterCodec(vp9Codec, webrtc.RTPCodecTypeVideo); err != nil {
		panic(err)
	}

	// Create an InterceptorRegistry
	i := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(m, i); err != nil {
		panic(err)
	}

	// Create a new RTCPeerConnection
	peerConnection, err := webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithInterceptorRegistry(i)).NewPeerConnection(peerConnectionConfig)
	if err != nil {
		panic(err)
	}
	defer func() {
		if cErr := peerConnection.Close(); cErr != nil {
			fmt.Printf("cannot close peerConnection: %v\n", cErr)
		}
	}()

	// Initialize GoCV video capture
	webcam, err := gocv.VideoCaptureDevice(0)
	if err != nil {
		panic(err)
	}
	defer webcam.Close()

	// Create a new track to send video to peers
	videoTrack, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{
		MimeType:  webrtc.MimeTypeVP9,
		ClockRate: 90000,
	}, "video", "pion")
	if err != nil {
		panic(err)
	}

	// Start capturing and encoding video frames
	go captureAndEncodeVideo(webcam, videoTrack)

	// Add the track to the peer connection
	_, err = peerConnection.AddTrack(videoTrack)
	if err != nil {
		panic(err)
	}

	// Set the remote SessionDescription
	err = peerConnection.SetRemoteDescription(offer)
	if err != nil {
		panic(err)
	}

	// Create answer
	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		panic(err)
	}

	// Create channel that is blocked until ICE Gathering is complete
	gatherComplete := webrtc.GatheringCompletePromise(peerConnection)

	// Sets the LocalDescription, and starts our UDP listeners
	err = peerConnection.SetLocalDescription(answer)
	if err != nil {
		panic(err)
	}

	// Block until ICE Gathering is complete, disabling trickle ICE
	<-gatherComplete

	// Get the LocalDescription and take it to base64 so we can paste in browser
	fmt.Println(encode(peerConnection.LocalDescription()))

	// Wait indefinitely
	select {}
}

// Implement the captureAndEncodeVideo function
func captureAndEncodeVideo(webcam *gocv.VideoCapture, videoTrack *webrtc.TrackLocalStaticSample) {
	// Initialize GMF encoder for VP9
	codec, err := gmf.FindEncoder("libvpx-vp9")
	if err != nil {
		panic(err)
	}

	codecCtx := gmf.NewCodecCtx(codec)
	defer gmf.Release(codecCtx)

	// Set codec parameters
	codecCtx.SetTimeBase(gmf.AVR{Num: 1, Den: 90000})
	codecCtx.SetPixFmt(gmf.AV_PIX_FMT_YUV420P)
	codecCtx.SetWidth(640)
	codecCtx.SetHeight(480)
	codecCtx.SetBitRate(500000) // Adjust bitrate as needed

	if err := codecCtx.Open(nil); err != nil {
		panic(err)
	}

	// Frame variables
	img := gocv.NewMat()
	defer img.Close()

	swsCtx := gmf.NewSwsCtx(
		gmf.AV_PIX_FMT_BGR24, // Input pixel format from GoCV
		codecCtx.Width(),
		codecCtx.Height(),
		codecCtx.PixFmt(), // Output pixel format for VP9 encoder
		codecCtx.Width(),
		codecCtx.Height(),
		gmf.SWS_BILINEAR,
	)
	defer swsCtx.Free()

	for {
		if ok := webcam.Read(&img); !ok || img.Empty() {
			continue
		}

		// Convert GoCV Mat to GMF frame
		frame, err := imgToFrame(img, codecCtx, swsCtx)
		if err != nil {
			panic(err)
		}
		defer frame.Free()

		// Encode frame
		packets, err := codecCtx.Encode(frame, -1)
		if err != nil {
			panic(err)
		}

		for _, packet := range packets {
			data := packet.Data()
			packet.Free()

			// Create a Sample
			sample := media.Sample{
				Data:     data,
				Duration: time.Second / 30, // Assuming 30 FPS
			}

			// Write the sample to the video track
			if writeErr := videoTrack.WriteSample(sample); writeErr != nil {
				if errors.Is(writeErr, io.ErrClosedPipe) {
					return
				}
				panic(writeErr)
			}
		}
	}
}

// Helper function to convert GoCV Mat to GMF Frame
func imgToFrame(img gocv.Mat, codecCtx *gmf.CodecCtx, swsCtx *gmf.SwsCtx) (*gmf.Frame, error) {
	// Create input frame from GoCV Mat
	srcFrame := gmf.NewFrame().
		SetWidth(codecCtx.Width()).
		SetHeight(codecCtx.Height()).
		SetFormat(gmf.AV_PIX_FMT_BGR24)
	defer srcFrame.Free()

	// Copy data from GoCV Mat to GMF Frame
	srcFrame.SetLine("data", img.ToBytes())

	// Create destination frame with the encoder's pixel format
	dstFrame := gmf.NewFrame().
		SetWidth(codecCtx.Width()).
		SetHeight(codecCtx.Height()).
		SetFormat(codecCtx.PixFmt())
	if err := dstFrame.ImgAlloc(); err != nil {
		return nil, err
	}

	// Convert the pixel format from BGR24 to YUV420P
	if _, err := swsCtx.Scale(srcFrame, dstFrame); err != nil {
		dstFrame.Free()
		return nil, err
	}

	dstFrame.SetPts(time.Now().UnixNano() / int64(time.Millisecond))
	return dstFrame, nil
}

// JSON encode + base64 a SessionDescription
func encode(obj *webrtc.SessionDescription) string {
	b, err := json.Marshal(obj)
	if err != nil {
		panic(err)
	}

	return base64.StdEncoding.EncodeToString(b)
}

// Decode a base64 and unmarshal JSON into a SessionDescription
func decode(in string, obj *webrtc.SessionDescription) {
	b, err := base64.StdEncoding.DecodeString(in)
	if err != nil {
		panic(err)
	}

	if err = json.Unmarshal(b, obj); err != nil {
		panic(err)
	}
}

// httpSDPServer starts a HTTP Server that consumes SDPs
func httpSDPServer(port int) chan string {
	sdpChan := make(chan string)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fmt.Fprintf(w, "done")
		sdpChan <- string(body)
	})

	go func() {
		panic(http.ListenAndServe(":"+strconv.Itoa(port), nil))
	}()

	return sdpChan
}
