package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
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

	// Derive the LiveKit URL from the request's Host header so it works both
	// locally (localhost) and remotely (Tailscale IP / hostname).
	// Only trust the hostname if it's localhost or a Tailscale IP to prevent
	// host header injection pointing browsers to attacker-controlled servers.
	host := r.Host
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}

	// Validate: only allow localhost or Tailscale CGNAT range
	hostname := h.cfg.Host // fallback to config default
	if host == "localhost" || host == "127.0.0.1" {
		hostname = host
	} else if ip := net.ParseIP(host); ip != nil {
		_, tsNet, _ := net.ParseCIDR("100.64.0.0/10")
		if tsNet.Contains(ip) {
			hostname = host
		}
	}
	livekitURL := fmt.Sprintf("ws://%s:%d", hostname, h.cfg.Port)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"token":    token,
		"url":      livekitURL,
		"roomName": h.cfg.RoomName,
	})
}
