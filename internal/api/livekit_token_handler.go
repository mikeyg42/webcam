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

	// Derive the LiveKit URL based on how the client reached us:
	// - Via custom domain (HTTPS/Caddy): use wss://domain/livekit-ws (proxied through Caddy)
	// - Via localhost: use ws://localhost:7880 (direct)
	// - Via Tailscale IP: use ws://100.x.x.x:7880 (direct)
	host := r.Host
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}

	var livekitURL string
	if host == "localhost" || host == "127.0.0.1" {
		livekitURL = fmt.Sprintf("ws://%s:%d", host, h.cfg.Port)
	} else if ip := net.ParseIP(host); ip != nil {
		_, tsNet, _ := net.ParseCIDR("100.64.0.0/10")
		if tsNet.Contains(ip) {
			livekitURL = fmt.Sprintf("ws://%s:%d", host, h.cfg.Port)
		} else {
			livekitURL = fmt.Sprintf("ws://%s:%d", h.cfg.Host, h.cfg.Port)
		}
	} else {
		// Custom domain (e.g., camera.uncannyportal.com) — route through Caddy's
		// reverse proxy so the browser uses the same TLS connection
		scheme := "wss"
		if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" {
			scheme = "ws"
		}
		livekitURL = fmt.Sprintf("%s://%s/livekit-ws", scheme, r.Host)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"token":    token,
		"url":      livekitURL,
		"roomName": h.cfg.RoomName,
	})
}
