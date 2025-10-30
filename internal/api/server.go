// Package api provides HTTP API server
package api

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/mikeyg42/webcam/internal/config"
)

// Server is an HTTP API server
type Server struct {
	httpServer    *http.Server
	configHandler *ConfigHandler
}

// NewServer creates a new API server
func NewServer(cfg *config.Config, addr string) *Server {
	mux := http.NewServeMux()

	// Create configuration handler
	configHandler := NewConfigHandler(cfg)
	configHandler.RegisterRoutes(mux)

	// Health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	return &Server{
		httpServer: &http.Server{
			Addr:           addr,
			Handler:        mux,
			ReadTimeout:    10 * time.Second,
			WriteTimeout:   10 * time.Second,
			MaxHeaderBytes: 1 << 20, // 1 MB
		},
		configHandler: configHandler,
	}
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
