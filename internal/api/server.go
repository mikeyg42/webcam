// Package api provides HTTP API server
package api

import (
	"context"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/mikeyg42/webcam/internal/calibration"
	"github.com/mikeyg42/webcam/internal/config"
	"github.com/mikeyg42/webcam/internal/database"
	"github.com/mikeyg42/webcam/internal/framestream"
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
func NewServer(ctx context.Context, cfg *config.Config, addr string, calibService *calibration.Service, detector *motion.Detector,
	frameDistributor *framestream.FrameDistributor, credDB *database.DB, tsManager *tailscale.TailscaleManager) *Server {
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
		calibrationHandler = NewCalibrationHandler(ctx, calibService, detector, frameDistributor, cfg)
		if tsManager != nil {
			calibrationHandler.SetTailscaleManager(tsManager)
		}
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

	// LiveKit token endpoint for browser subscribers
	lkTokenHandler := NewLiveKitTokenHandler(&cfg.LiveKit)
	mux.HandleFunc("/api/livekit-token", requireTailscaleAuth(tsManager, lkTokenHandler.handleToken))

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
			WriteTimeout:   5 * time.Minute,
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

// SetRecordingHealthHandler sets the recording health handler (called after recorder service is initialized)
func (s *Server) SetRecordingHealthHandler(provider RecordingHealthProvider) {
	if provider != nil {
		handler := NewRecordingHealthHandler(provider)
		handler.RegisterRoutes(s.mux)
	}
}

// SetRecordingControlHandler sets the recording control handler (called after recorder service is initialized)
func (s *Server) SetRecordingControlHandler(controller RecordingController) {
	if controller != nil {
		handler := NewRecordingControlHandler(controller)
		// Wrap with Tailscale auth
		s.mux.HandleFunc("/api/recording/start", requireTailscaleAuth(s.tailscaleManager, handler.HandleStart))
		s.mux.HandleFunc("/api/recording/stop", requireTailscaleAuth(s.tailscaleManager, handler.HandleStop))
		s.mux.HandleFunc("/api/recording/status", requireTailscaleAuth(s.tailscaleManager, handler.HandleStatus))
		log.Println("[APIServer] Recording control endpoints registered with auth")
	}
}

// SetRecordingsBrowserHandler sets the recordings browser handler for listing, playing, and deleting recordings
func (s *Server) SetRecordingsBrowserHandler(browser RecordingBrowser) {
	if browser != nil {
		handler := NewRecordingsBrowserHandler(browser)
		auth := func(hf http.HandlerFunc) http.HandlerFunc {
			return requireTailscaleAuth(s.tailscaleManager, hf)
		}
		s.mux.HandleFunc("GET /api/recordings", auth(handler.handleList))
		s.mux.HandleFunc("GET /api/recordings/{id}", auth(handler.handleGet))
		s.mux.HandleFunc("DELETE /api/recordings/{id}", auth(handler.handleDelete))
		s.mux.HandleFunc("PATCH /api/recordings/{id}", auth(handler.handleUpdate))
		s.mux.HandleFunc("GET /api/recordings/{id}/download", auth(handler.handleDownload))
		s.mux.HandleFunc("GET /api/recordings/{id}/segments/{index}/stream", auth(handler.handleSegmentStream))
		log.Println("[APIServer] Recordings browser endpoints registered with auth")
	}
}

// corsMiddleware adds CORS headers to allow cross-origin requests
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		// Allow localhost and Tailscale origins (100.x.x.x range)
		if origin != "" && isAllowedOrigin(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
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

// requireTailscaleAuth wraps an http.HandlerFunc with Tailscale authentication.
// Returns 401 if Tailscale is configured and the request isn't from an authenticated user.
func requireTailscaleAuth(tsManager *tailscale.TailscaleManager, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if tsManager != nil {
			_, err := tsManager.GetUserEmailFromRequest(r)
			if err != nil {
				http.Error(w, "Unauthorized - Tailscale authentication required", http.StatusUnauthorized)
				return
			}
		}
		next(w, r)
	}
}

// isAllowedOrigin checks if an origin is localhost or a Tailscale IP (100.64.0.0/10)
func isAllowedOrigin(origin string) bool {
	// Strip scheme (http:// or https://)
	host := origin
	for _, prefix := range []string{"https://", "http://"} {
		if len(host) > len(prefix) && host[:len(prefix)] == prefix {
			host = host[len(prefix):]
			break
		}
	}
	// Strip port
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}

	// Allow localhost
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}

	// Allow custom domain
	if host == "camera.uncannyportal.com" || strings.HasSuffix(host, ".uncannyportal.com") {
		return true
	}

	// Allow Tailscale CGNAT range (100.64.0.0/10)
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	_, tailscaleNet, _ := net.ParseCIDR("100.64.0.0/10")
	return tailscaleNet.Contains(ip)
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
