package api

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/livekit/protocol/auth"
	"github.com/mikeyg42/webcam/internal/config"
)

// LiveKitTokenHandler issues JWT tokens for browser clients to subscribe to the camera room.
type LiveKitTokenHandler struct {
	cfg *config.LiveKitConfig
}

func NewLiveKitTokenHandler(cfg *config.LiveKitConfig) *LiveKitTokenHandler {
	return &LiveKitTokenHandler{cfg: cfg}
}

func (h *LiveKitTokenHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/livekit-token", h.handleToken)
}

func (h *LiveKitTokenHandler) handleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	at := auth.NewAccessToken(h.cfg.APIKey, h.cfg.APISecret)
	at.SetIdentity("browser-viewer").
		SetName("Browser Viewer").
		SetValidFor(24 * time.Hour).
		SetVideoGrant(&auth.VideoGrant{
			RoomJoin: true,
			Room:     h.cfg.RoomName,
		})

	token, err := at.ToJWT()
	if err != nil {
		log.Printf("[API] Failed to generate LiveKit token: %v", err)
		http.Error(w, "Failed to generate token", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"token":      token,
		"url":        h.cfg.URL(),
		"roomName":   h.cfg.RoomName,
	})
}
