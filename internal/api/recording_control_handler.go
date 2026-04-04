package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	"github.com/mikeyg42/webcam/internal/recorder"
)

// RecordingController is the interface required from the recorder service for control operations
type RecordingController interface {
	StartManualRecording(ctx context.Context) (string, error)
	StopManualRecording() error
	GetRecordingState() recorder.RecordingState
	GetMetrics() *recorder.MetricsSnapshot
}

// RecordingControlHandler handles recording control API requests
type RecordingControlHandler struct {
	controller RecordingController
}

// NewRecordingControlHandler creates a new recording control handler
func NewRecordingControlHandler(controller RecordingController) *RecordingControlHandler {
	return &RecordingControlHandler{controller: controller}
}

// HandleStart handles POST /api/recording/start
func (h *RecordingControlHandler) HandleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	recordingID, err := h.controller.StartManualRecording(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":       "started",
		"recording_id": recordingID,
	})
}

// HandleStop handles POST /api/recording/stop
func (h *RecordingControlHandler) HandleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := h.controller.StopManualRecording(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "stopped",
	})
}

// HandleStatus handles GET /api/recording/status
func (h *RecordingControlHandler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	state := h.controller.GetRecordingState()
	metrics := h.controller.GetMetrics()

	response := map[string]interface{}{
		"state":                state,
		"frames_received":     metrics.FramesReceived,
		"frames_dropped":      metrics.FramesDropped,
		"segments_created":    metrics.SegmentsCreated,
		"emergency_buffer":    metrics.EmergencyBufferSize,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// RegisterRoutes registers the recording control endpoints on the mux
func (h *RecordingControlHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/recording/start", h.HandleStart)
	mux.HandleFunc("/api/recording/stop", h.HandleStop)
	mux.HandleFunc("/api/recording/status", h.HandleStatus)
	log.Println("[APIServer] Recording control endpoints registered at /api/recording/{start,stop,status}")
}
