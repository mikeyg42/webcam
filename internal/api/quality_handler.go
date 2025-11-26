// Package api provides HTTP API handlers
package api

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/mikeyg42/webcam/internal/quality"
)

// QualityManagerProvider interface for accessing QualityManager
type QualityManagerProvider interface {
	GetQualityManager() *quality.QualityManager
}

// QualityHandler handles quality management API endpoints
type QualityHandler struct {
	provider QualityManagerProvider
}

// NewQualityHandler creates a new quality handler
func NewQualityHandler(provider QualityManagerProvider) *QualityHandler {
	return &QualityHandler{
		provider: provider,
	}
}

// RegisterRoutes registers quality API routes
func (h *QualityHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/quality/metrics", h.handleGetMetrics)
}

// handleGetMetrics returns current quality management metrics
func (h *QualityHandler) handleGetMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	qm := h.provider.GetQualityManager()
	if qm == nil {
		http.Error(w, "Quality manager not available", http.StatusServiceUnavailable)
		return
	}

	metrics := qm.GetMetrics()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(metrics); err != nil {
		log.Printf("[QualityHandler] Failed to encode metrics: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}
