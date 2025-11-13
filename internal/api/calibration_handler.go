package api

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/mikeyg42/webcam/internal/calibration"
	"github.com/mikeyg42/webcam/internal/motion"
)

// CalibrationHandler handles calibration API requests
type CalibrationHandler struct {
	calibService     *calibration.Service
	detector         *motion.Detector
	frameDistributor interface {
		GetMotionChannel() <-chan image.Image
		Start(width, height int) error
		Stop()
		IsRunning() bool
	}
	ctx context.Context
}

// NewCalibrationHandler creates a new calibration handler
func NewCalibrationHandler(ctx context.Context, calibService *calibration.Service, detector *motion.Detector, frameDistributor interface {
	GetMotionChannel() <-chan image.Image
	Start(width, height int) error
	Stop()
	IsRunning() bool
}) *CalibrationHandler {
	return &CalibrationHandler{
		ctx:              ctx,
		calibService:     calibService,
		detector:         detector,
		frameDistributor: frameDistributor,
	}
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

	// Check if detector is running
	if h.detector.IsRunning() {
		http.Error(w, "Cannot calibrate while motion detection is running. Stop detection first.", http.StatusConflict)
		return
	}

	// Start frame distributor (camera) if not already running
	if !h.frameDistributor.IsRunning() {
		log.Println("[API] Starting camera for calibration...")
		if err := h.frameDistributor.Start(1280, 720); err != nil {
			http.Error(w, fmt.Sprintf("Failed to start camera: %v", err), http.StatusInternalServerError)
			return
		}
		log.Println("[API] Camera started successfully at 1280x720")
	}

	// Start calibration with frames from motion channel
	motionChan := h.frameDistributor.GetMotionChannel()
	if err := h.calibService.StartCalibration(h.ctx, motionChan); err != nil {
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

	// Get progress from calibration service
	progress := h.calibService.GetProgress()

	// If calibration just completed, stop the camera
	if progress.State == "complete" && h.frameDistributor.IsRunning() {
		log.Println("[API] Calibration complete - stopping camera")
		h.frameDistributor.Stop()
		log.Println("[API] Camera stopped - waiting for user to click 'Start Detection'")
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

	progress := h.calibService.GetProgress()

	if progress.Result == nil {
		http.Error(w, "No calibration result available. Run calibration first.", http.StatusBadRequest)
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

// GetCalibrationVideo handles GET /api/calibration/video
func (h *CalibrationHandler) GetCalibrationVideo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
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

// StartDetection handles POST /api/detection/start
func (h *CalibrationHandler) StartDetection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
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
	mux.HandleFunc("/api/detection/start", h.StartDetection)
	mux.HandleFunc("/api/detection/stop", h.StopDetection)
	mux.HandleFunc("/api/detection/status", h.GetDetectionStatus)
}
