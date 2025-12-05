// Package api provides HTTP API server
package api

import (
	"context"
	"image"
	"log"
	"net/http"
	"time"

	"github.com/mikeyg42/webcam/internal/calibration"
	"github.com/mikeyg42/webcam/internal/config"
	"github.com/mikeyg42/webcam/internal/database"
	"github.com/mikeyg42/webcam/internal/motion"
	"github.com/mikeyg42/webcam/internal/tailscale"
)

// Server is an HTTP API server
type Server struct {
	httpServer          *http.Server
	mux                 *http.ServeMux // Store mux for dynamic route registration
	configHandler       *ConfigHandler
	calibrationHandler  *CalibrationHandler
	qualityHandler      *QualityHandler
	credentialsHandler  *CredentialsHandler
	credDB              *database.DB
	tailscaleManager    *tailscale.TailscaleManager
}

// NewServer creates a new API server
func NewServer(ctx context.Context, cfg *config.Config, addr string, calibService *calibration.Service, detector *motion.Detector, frameDistributor interface {
	GetMotionChannel() <-chan image.Image
	Start(width, height int) error
	Stop()
	IsRunning() bool
}, credDB *database.DB, tsManager *tailscale.TailscaleManager) *Server {
	mux := http.NewServeMux()

	// Create configuration handler with motion detector for runtime updates
	configHandler := NewConfigHandler(cfg, detector)
	// Set credential database and Tailscale manager for auto-population
	if credDB != nil {
		configHandler.SetCredentialDatabase(credDB)
	}
	if tsManager != nil {
		configHandler.SetTailscaleManager(tsManager)
	}
	configHandler.RegisterRoutes(mux)

	// Create calibration handler (if provided)
	var calibrationHandler *CalibrationHandler
	if calibService != nil && detector != nil && frameDistributor != nil {
		calibrationHandler = NewCalibrationHandler(ctx, calibService, detector, frameDistributor)
		calibrationHandler.RegisterRoutes(mux)
	}

	// Create credentials handler (if database and Tailscale are available)
	var credentialsHandler *CredentialsHandler
	if credDB != nil && tsManager != nil {
		credentialsHandler = NewCredentialsHandler(credDB, tsManager)

		// Create rate limiter: 10 requests per minute per IP
		credentialRateLimiter := NewRateLimiter(10, time.Minute)

		// Register credential management routes with rate limiting
		mux.HandleFunc("/api/credentials", credentialRateLimiter.Middleware(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodPost:
				credentialsHandler.HandleSetCredentials(w, r)
			case http.MethodDelete:
				credentialsHandler.HandleDeleteCredentials(w, r)
			default:
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			}
		}))
		mux.HandleFunc("/api/credentials/status", credentialRateLimiter.Middleware(credentialsHandler.HandleGetCredentialsStatus))

		log.Println("[APIServer] Credential management endpoints registered with rate limiting")
	} else {
		log.Println("[APIServer] Warning: Credential management disabled (database or Tailscale not available)")
	}

	// Health check endpoint
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	// Wrap mux with CORS middleware
	handler := corsMiddleware(mux)

	return &Server{
		httpServer: &http.Server{
			Addr:           addr,
			Handler:        handler,
			ReadTimeout:    10 * time.Second,
			WriteTimeout:   10 * time.Second,
			MaxHeaderBytes: 1 << 20, // 1 MB
		},
		mux:                 mux,
		configHandler:       configHandler,
		calibrationHandler:  calibrationHandler,
		credentialsHandler:  credentialsHandler,
		credDB:              credDB,
		tailscaleManager:    tsManager,
	}
}

// GetConfigHandler returns the config handler for external access
func (s *Server) GetConfigHandler() *ConfigHandler {
	return s.configHandler
}

// SetQualityHandler sets the quality handler (called after WebRTC manager is initialized)
func (s *Server) SetQualityHandler(provider QualityManagerProvider) {
	if s.qualityHandler == nil && provider != nil {
		s.qualityHandler = NewQualityHandler(provider)
		s.qualityHandler.RegisterRoutes(s.mux)
		log.Println("[APIServer] Quality metrics endpoint registered at /api/quality/metrics")
	}
}

// corsMiddleware adds CORS headers to allow cross-origin requests
func corsMiddleware(next http.Handler) http.Handler {
	// Whitelist of allowed origins
	allowedOrigins := map[string]bool{
		"http://localhost:8080":  true,
		"http://localhost:8081":  true,
		"http://localhost:3000":  true,
		"http://127.0.0.1:8080":  true,
		"http://127.0.0.1:8081":  true,
		"http://127.0.0.1:3000":  true,
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		// Only set CORS headers for whitelisted origins
		if origin != "" && allowedOrigins[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}

		// Handle preflight requests
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Start starts the API server
func (s *Server) Start() error {
	log.Printf("Starting API server on %s", s.httpServer.Addr)
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown(ctx context.Context) error {
	log.Println("Shutting down API server...")
	return s.httpServer.Shutdown(ctx)
}

// StartInBackground starts the server in a goroutine
func (s *Server) StartInBackground() {
	go func() {
		if err := s.Start(); err != nil && err != http.ErrServerClosed {
			log.Printf("API server error: %v", err)
		}
	}()
	log.Printf("API server started in background on %s", s.httpServer.Addr)
}
