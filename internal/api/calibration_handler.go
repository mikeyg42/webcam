package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"

	"github.com/mikeyg42/webcam/internal/calibration"
	"github.com/mikeyg42/webcam/internal/config"
	"github.com/mikeyg42/webcam/internal/framestream"
	"github.com/mikeyg42/webcam/internal/motion"
	"github.com/mikeyg42/webcam/internal/tailscale"
	"github.com/pion/mediadevices"
	_ "github.com/pion/mediadevices/pkg/driver/camera"     // Camera driver
	_ "github.com/pion/mediadevices/pkg/driver/microphone" // Microphone driver
)

// CalibrationHandler handles calibration API requests
type CalibrationHandler struct {
	calibService     *calibration.Service
	detector         *motion.Detector
	frameDistributor *framestream.FrameDistributor
	config           *config.Config
	ctx              context.Context
	tailscaleManager *tailscale.TailscaleManager
}

// NewCalibrationHandler creates a new calibration handler
func NewCalibrationHandler(ctx context.Context, calibService *calibration.Service, detector *motion.Detector,
	frameDistributor *framestream.FrameDistributor, cfg *config.Config) *CalibrationHandler {
	return &CalibrationHandler{
		ctx:              ctx,
		calibService:     calibService,
		detector:         detector,
		frameDistributor: frameDistributor,
		config:           cfg,
	}
}

// SetTailscaleManager sets the Tailscale manager for authentication
func (h *CalibrationHandler) SetTailscaleManager(tsManager *tailscale.TailscaleManager) {
	h.tailscaleManager = tsManager
}

// requireAuth checks Tailscale authentication and returns error if unauthorized
func (h *CalibrationHandler) requireAuth(w http.ResponseWriter, r *http.Request) bool {
	if h.tailscaleManager != nil {
		_, err := h.tailscaleManager.GetUserEmailFromRequest(r)
		if err != nil {
			log.Printf("[CalibrationHandler] Authentication failed: %v", err)
			http.Error(w, "Unauthorized - Tailscale authentication required", http.StatusUnauthorized)
			return false
		}
	} else {
		log.Printf("[CalibrationHandler] Warning: Tailscale disabled - unauthenticated calibration access")
	}
	return true
}

// CalibrationStatusResponse represents the current calibration status
type CalibrationStatusResponse struct {
	State      string  `json:"state"`      // idle, recording, processing, complete, error
	Progress   float64 `json:"progress"`   // 0-100
	Message    string  `json:"message"`
	Calibrated bool    `json:"calibrated"` // Whether detector is calibrated
	VideoPath  string  `json:"videoPath,omitempty"`
	Error      string  `json:"error,omitempty"`
}

// CalibrationResultResponse represents the calibration result
type CalibrationResultResponse struct {
	Baseline  float64 `json:"baseline"`
	Threshold float64 `json:"threshold"`
	Samples   int     `json:"samples"`
	Mean      float64 `json:"mean"`
	StdDev    float64 `json:"stdDev"`
}

// StartCalibration handles POST /api/calibration/start
func (h *CalibrationHandler) StartCalibration(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Require Tailscale authentication
	if !h.requireAuth(w, r) {
		return
	}

	// Check if calibration is already in progress (prevents double-click issues)
	progress := h.calibService.GetProgress()
	if progress.State == calibration.StateRecording || progress.State == calibration.StateProcessing {
		log.Printf("[API] Calibration already in progress (state: %s), ignoring duplicate request", progress.State)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Calibration already in progress.",
		})
		return
	}

	// Check if detector is running
	if h.detector.IsRunning() {
		http.Error(w, "Cannot calibrate while motion detection is running. Stop detection first.", http.StatusConflict)
		return
	}

	// Stop frame distributor if running (it may be at wrong resolution or wrong device)
	if h.frameDistributor.IsRunning() {
		log.Println("[API] Stopping camera to apply new device settings...")
		h.frameDistributor.Stop()
	}

	// Update devices from current config (in case user changed camera/mic in settings)
	camera, microphone, audioEnabled, err := h.lookupDevicesFromConfig()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to find configured devices: %v", err), http.StatusInternalServerError)
		return
	}

	if err := h.frameDistributor.UpdateDevices(camera, microphone, audioEnabled); err != nil {
		http.Error(w, fmt.Sprintf("Failed to update devices: %v", err), http.StatusInternalServerError)
		return
	}

	// Subscribe to motion frames BEFORE starting camera
	// This ensures frames are captured even if distributor restarts during resolution change
	motionSub := h.frameDistributor.SubscribeMotion()

	log.Println("[API] Starting camera for calibration at 1280x720...")
	if err := h.frameDistributor.Start(1280, 720); err != nil {
		motionSub.Close() // Clean up subscription on error
		http.Error(w, fmt.Sprintf("Failed to start camera: %v", err), http.StatusInternalServerError)
		return
	}
	log.Println("[API] Camera started successfully at 1280x720")

	// Start calibration with frames from motion subscription
	// The subscription survives distributor restarts, solving the "0 samples" issue
	// Pass cleanup function to close subscription when calibration completes
	if err := h.calibService.StartCalibration(h.ctx, motionSub.Frames(), motionSub.Close); err != nil {
		motionSub.Close()
		http.Error(w, fmt.Sprintf("Failed to start calibration: %v", err), http.StatusInternalServerError)
		return
	}

	log.Println("[API] Calibration started via API")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Calibration started. Use /api/calibration/status to monitor progress.",
	})
}

// GetCalibrationStatus handles GET /api/calibration/status
func (h *CalibrationHandler) GetCalibrationStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Require Tailscale authentication
	if !h.requireAuth(w, r) {
		return
	}

	// Get progress from calibration service
	progress := h.calibService.GetProgress()

	// Keep camera running after calibration for WebRTC streaming
	// (Previously we stopped it, but WebRTC needs continuous video feed)
	if progress.State == "complete" && h.frameDistributor.IsRunning() {
		log.Println("[API] Calibration complete - camera staying active for WebRTC streaming")
	}

	// Check if detector is calibrated
	isCalibrated := h.detector.IsCalibrated()

	response := CalibrationStatusResponse{
		State:      string(progress.State),
		Progress:   progress.Progress,
		Message:    progress.Message,
		Calibrated: isCalibrated,
	}

	if progress.VideoPath != "" {
		// Return relative path for frontend
		response.VideoPath = filepath.Base(progress.VideoPath)
	}

	if progress.Error != nil {
		response.Error = progress.Error.Error()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetCalibrationResult handles GET /api/calibration/result
func (h *CalibrationHandler) GetCalibrationResult(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Require Tailscale authentication
	if !h.requireAuth(w, r) {
		return
	}

	progress := h.calibService.GetProgress()

	if progress.Result == nil {
		http.Error(w, "No calibration result available. Run calibration first.", http.StatusNotFound)
		return
	}

	response := CalibrationResultResponse{
		Baseline:  progress.Result.Baseline,
		Threshold: progress.Result.Threshold,
		Samples:   progress.Result.Samples,
		Mean:      progress.Result.Mean,
		StdDev:    progress.Result.StdDev,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// ApplyCalibration handles POST /api/calibration/apply
func (h *CalibrationHandler) ApplyCalibration(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Require Tailscale authentication
	if !h.requireAuth(w, r) {
		return
	}

	progress := h.calibService.GetProgress()

	if progress.Result == nil {
		http.Error(w, "No calibration result available. Run calibration first.", http.StatusBadRequest)
		return
	}

	// Validate calibration result before applying
	if err := validateCalibrationResult(progress.Result); err != nil {
		http.Error(w, fmt.Sprintf("Invalid calibration result: %v", err), http.StatusBadRequest)
		return
	}

	// Apply calibration to detector
	h.detector.SetCalibration(progress.Result.Baseline, progress.Result.Threshold)

	log.Println("[API] Calibration applied - camera will remain OFF until 'Start Detection' is clicked")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Calibration applied - Baseline: %.4f%%, Threshold: %.4f%%",
			progress.Result.Baseline, progress.Result.Threshold),
	})
}

// validateCalibrationResult checks that calibration values are sane
func validateCalibrationResult(result *calibration.CalibrationResult) error {
	if result == nil {
		return fmt.Errorf("calibration result is nil")
	}

	if result.Samples < 10 {
		return fmt.Errorf("insufficient samples: got %d, need at least 10", result.Samples)
	}

	// Check for NaN/Inf values which indicate calculation errors
	if math.IsNaN(result.Baseline) || math.IsInf(result.Baseline, 0) {
		return fmt.Errorf("invalid baseline value (NaN or Inf)")
	}
	if math.IsNaN(result.Threshold) || math.IsInf(result.Threshold, 0) {
		return fmt.Errorf("invalid threshold value (NaN or Inf)")
	}
	if math.IsNaN(result.StdDev) || math.IsInf(result.StdDev, 0) {
		return fmt.Errorf("invalid standard deviation value (NaN or Inf)")
	}

	if result.Baseline < 0 {
		return fmt.Errorf("invalid baseline: %.4f (must be >= 0)", result.Baseline)
	}

	if result.Threshold <= result.Baseline {
		return fmt.Errorf("threshold (%.4f) must be greater than baseline (%.4f)", result.Threshold, result.Baseline)
	}

	if result.Threshold > 50 {
		return fmt.Errorf("threshold too high: %.4f%% (max 50%%). Scene may have too much motion for calibration", result.Threshold)
	}

	if result.StdDev < 0 {
		return fmt.Errorf("invalid standard deviation: %.4f (must be >= 0)", result.StdDev)
	}

	return nil
}

// GetCalibrationVideo handles GET /api/calibration/video
func (h *CalibrationHandler) GetCalibrationVideo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Require Tailscale authentication
	if !h.requireAuth(w, r) {
		return
	}

	progress := h.calibService.GetProgress()

	if progress.VideoPath == "" {
		http.Error(w, "No calibration video available", http.StatusNotFound)
		return
	}

	// Check if file exists
	if _, err := os.Stat(progress.VideoPath); os.IsNotExist(err) {
		http.Error(w, "Calibration video file not found", http.StatusNotFound)
		return
	}

	// Serve the video file
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=\"%s\"", filepath.Base(progress.VideoPath)))
	http.ServeFile(w, r, progress.VideoPath)
}

// ResetCalibration handles POST /api/calibration/reset
func (h *CalibrationHandler) ResetCalibration(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Require Tailscale authentication
	if !h.requireAuth(w, r) {
		return
	}

	h.calibService.Reset()

	log.Println("[API] Calibration state reset via API")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Calibration state reset. Ready to retry.",
	})
}

// StartDetection handles POST /api/detection/start
func (h *CalibrationHandler) StartDetection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Require Tailscale authentication
	if !h.requireAuth(w, r) {
		return
	}

	// Check if calibrated
	if !h.detector.IsCalibrated() {
		http.Error(w, "Cannot start detection without calibration. Run calibration first.", http.StatusBadRequest)
		return
	}

	// Check if already running
	if h.detector.IsRunning() {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Motion detection is already running",
		})
		return
	}

	// Start camera if not running
	if !h.frameDistributor.IsRunning() {
		log.Println("[API] Starting camera for motion detection...")
		if err := h.frameDistributor.Start(1280, 720); err != nil {
			http.Error(w, fmt.Sprintf("Failed to start camera: %v", err), http.StatusInternalServerError)
			return
		}
		log.Println("[API] Camera started successfully")
	}

	// Start detection
	if err := h.detector.Start(); err != nil {
		http.Error(w, fmt.Sprintf("Failed to start detection: %v", err), http.StatusInternalServerError)
		return
	}

	log.Println("[API] Motion detection started via API")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Motion detection started successfully",
	})
}

// StopDetection handles POST /api/detection/stop
func (h *CalibrationHandler) StopDetection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Require Tailscale authentication
	if !h.requireAuth(w, r) {
		return
	}

	// Check if running
	if !h.detector.IsRunning() {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Motion detection is not running",
		})
		return
	}

	// Stop detection
	if err := h.detector.Stop(); err != nil {
		http.Error(w, fmt.Sprintf("Failed to stop detection: %v", err), http.StatusInternalServerError)
		return
	}

	log.Println("[API] Motion detection stopped via API")

	// Stop camera as well
	if h.frameDistributor.IsRunning() {
		log.Println("[API] Stopping camera...")
		h.frameDistributor.Stop()
		log.Println("[API] Camera stopped")
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Motion detection and camera stopped successfully",
	})
}

// GetDetectionStatus handles GET /api/detection/status
func (h *CalibrationHandler) GetDetectionStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Require Tailscale authentication
	if !h.requireAuth(w, r) {
		return
	}

	baseline, threshold, calibrated := h.detector.GetCalibration()
	isRunning := h.detector.IsRunning()
	stats := h.detector.GetStats()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"running":    isRunning,
		"calibrated": calibrated,
		"baseline":   baseline,
		"threshold":  threshold,
		"stats": map[string]interface{}{
			"framesProcessed": stats.FramesProcessed,
			"motionEvents":    stats.MotionEvents,
			"lastMotionTime":  stats.LastMotionTime,
		},
	})
}

// RegisterRoutes registers HTTP routes for calibration API
func (h *CalibrationHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/calibration/start", h.StartCalibration)
	mux.HandleFunc("/api/calibration/status", h.GetCalibrationStatus)
	mux.HandleFunc("/api/calibration/result", h.GetCalibrationResult)
	mux.HandleFunc("/api/calibration/apply", h.ApplyCalibration)
	mux.HandleFunc("/api/calibration/video", h.GetCalibrationVideo)
	mux.HandleFunc("/api/calibration/reset", h.ResetCalibration)
	mux.HandleFunc("/api/detection/start", h.StartDetection)
	mux.HandleFunc("/api/detection/stop", h.StopDetection)
	mux.HandleFunc("/api/detection/status", h.GetDetectionStatus)
}

// lookupDevicesFromConfig finds camera and microphone devices based on current config
func (h *CalibrationHandler) lookupDevicesFromConfig() (mediadevices.MediaDeviceInfo, mediadevices.MediaDeviceInfo, bool, error) {
	var camera, microphone mediadevices.MediaDeviceInfo
	audioEnabled := h.config.Audio.Enabled

	// Get all available devices
	devices := mediadevices.EnumerateDevices()

	// Find camera by device ID from config
	cameraID := h.config.Video.DeviceID
	for _, d := range devices {
		if d.Kind == mediadevices.VideoInput && d.DeviceID == cameraID {
			camera = d
			log.Printf("[API] Found camera: %s (ID: %s)", d.Label, d.DeviceID)
			break
		}
	}

	if camera.DeviceID == "" {
		// Fallback: use first available camera
		for _, d := range devices {
			if d.Kind == mediadevices.VideoInput {
				camera = d
				log.Printf("[API] Using fallback camera: %s (ID: %s)", d.Label, d.DeviceID)
				break
			}
		}
	}

	if camera.DeviceID == "" {
		return camera, microphone, false, fmt.Errorf("no camera found")
	}

	// Find microphone by device ID from config (if audio enabled)
	if audioEnabled {
		micID := h.config.Audio.DeviceID
		for _, d := range devices {
			if d.Kind == mediadevices.AudioInput && d.DeviceID == micID {
				microphone = d
				log.Printf("[API] Found microphone: %s (ID: %s)", d.Label, d.DeviceID)
				break
			}
		}

		if microphone.DeviceID == "" {
			// Fallback: use first available microphone
			for _, d := range devices {
				if d.Kind == mediadevices.AudioInput {
					microphone = d
					log.Printf("[API] Using fallback microphone: %s (ID: %s)", d.Label, d.DeviceID)
					break
				}
			}
		}

		if microphone.DeviceID == "" {
			log.Printf("[API] Warning: No microphone found, disabling audio")
			audioEnabled = false
		}
	}

	return camera, microphone, audioEnabled, nil
}
