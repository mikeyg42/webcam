// Package api provides HTTP API server
package api

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/mikeyg42/webcam/internal/recorder"
)

// RecordingHealthProvider is the interface required from the recorder service
type RecordingHealthProvider interface {
	GetHealthStatus() map[string]*recorder.RecordingHealthInfo
	GetMetrics() *recorder.MetricsSnapshot
}

// RecordingHealthHandler handles recording health API requests
type RecordingHealthHandler struct {
	provider RecordingHealthProvider
}

// NewRecordingHealthHandler creates a new recording health handler
func NewRecordingHealthHandler(provider RecordingHealthProvider) *RecordingHealthHandler {
	return &RecordingHealthHandler{
		provider: provider,
	}
}

// HealthResponse is the JSON response for the health endpoint
type HealthResponse struct {
	Status     string                  `json:"status"`
	Timestamp  time.Time               `json:"timestamp"`
	Recordings []RecordingHealthDetail `json:"recordings"`
	Metrics    MetricsDetail           `json:"metrics"`
}

// RecordingHealthDetail provides detailed health info for a single recording
type RecordingHealthDetail struct {
	RecordingID       string `json:"recording_id"`
	Type              string `json:"type"`
	Status            string `json:"status"`
	LastFrameWrite    string `json:"last_frame_write,omitempty"`
	StaleDuration     string `json:"stale_duration,omitempty"`
	FrameCount        uint64 `json:"frame_count"`
	ConsecutiveStalls uint32 `json:"consecutive_stalls"`
	RestartAttempts   int    `json:"restart_attempts"`
}

// MetricsDetail provides metrics info for the response
type MetricsDetail struct {
	FramesReceived    uint64 `json:"frames_received"`
	FramesDropped     uint64 `json:"frames_dropped"`
	StallsDetected    uint64 `json:"stalls_detected"`
	EncoderRestarts   uint64 `json:"encoder_restarts"`
}

// HandleHealth handles GET /api/health/recording
func (h *RecordingHealthHandler) HandleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	healthStatus := h.provider.GetHealthStatus()
	metrics := h.provider.GetMetrics()

	// Determine overall status
	overallStatus := "ok"
	var recordings []RecordingHealthDetail

	for _, info := range healthStatus {
		detail := RecordingHealthDetail{
			RecordingID:       info.RecordingID,
			Type:              info.Type,
			Status:            string(info.Status),
			FrameCount:        info.FrameCount,
			ConsecutiveStalls: info.ConsecutiveStalls,
			RestartAttempts:   info.RestartAttempts,
		}

		if !info.LastFrameWrite.IsZero() {
			detail.LastFrameWrite = info.LastFrameWrite.Format(time.RFC3339)
			detail.StaleDuration = info.StaleDuration.Round(time.Millisecond).String()
		}

		recordings = append(recordings, detail)

		// Update overall status based on individual recording status
		switch info.Status {
		case recorder.HealthStatusWarning:
			if overallStatus == "ok" {
				overallStatus = "degraded"
			}
		case recorder.HealthStatusCritical:
			overallStatus = "degraded"
		case recorder.HealthStatusFailed:
			overallStatus = "unhealthy"
		}
	}

	response := HealthResponse{
		Status:     overallStatus,
		Timestamp:  time.Now(),
		Recordings: recordings,
		Metrics: MetricsDetail{
			FramesReceived:  metrics.FramesReceived,
			FramesDropped:   metrics.FramesDropped,
			StallsDetected:  metrics.StallsDetected,
			EncoderRestarts: metrics.EncoderRestarts,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	if overallStatus == "unhealthy" {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else if overallStatus == "degraded" {
		w.WriteHeader(http.StatusOK) // Still OK, but degraded
	} else {
		w.WriteHeader(http.StatusOK)
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("[RecordingHealth] Failed to encode response: %v", err)
	}
}

// RegisterRoutes registers the health endpoint on the mux
func (h *RecordingHealthHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/health/recording", h.HandleHealth)
	log.Println("[APIServer] Recording health endpoint registered at /api/health/recording")
}
