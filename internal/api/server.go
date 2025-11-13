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
	"github.com/mikeyg42/webcam/internal/motion"
)

// Server is an HTTP API server
type Server struct {
	httpServer         *http.Server
	configHandler      *ConfigHandler
	calibrationHandler *CalibrationHandler
}

// NewServer creates a new API server
func NewServer(ctx context.Context, cfg *config.Config, addr string, calibService *calibration.Service, detector *motion.Detector, frameDistributor interface {
	GetMotionChannel() <-chan image.Image
	Start(width, height int) error
	Stop()
	IsRunning() bool
}) *Server {
	mux := http.NewServeMux()

	// Create configuration handler
	configHandler := NewConfigHandler(cfg)
	configHandler.RegisterRoutes(mux)

	// Create calibration handler (if provided)
	var calibrationHandler *CalibrationHandler
	if calibService != nil && detector != nil && frameDistributor != nil {
		calibrationHandler = NewCalibrationHandler(ctx, calibService, detector, frameDistributor)
		calibrationHandler.RegisterRoutes(mux)
	}

	// Health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
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
		configHandler:      configHandler,
		calibrationHandler: calibrationHandler,
	}
}

// corsMiddleware adds CORS headers to allow cross-origin requests
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Allow requests from any localhost port for development
		origin := r.Header.Get("Origin")
		if origin == "" {
			origin = "*"
		}

		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Allow-Credentials", "true")

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
