package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/mikeyg42/webcam/internal/database"
	"github.com/mikeyg42/webcam/internal/tailscale"
)

// CredentialsRequest represents the JSON payload for setting credentials
type CredentialsRequest struct {
	WebRTCUsername   string `json:"webrtc_username"`
	WebRTCPassword   string `json:"webrtc_password"`
	MinIOAccessKey   string `json:"minio_access_key"`
	MinIOSecretKey   string `json:"minio_secret_key"`
	PostgresUsername string `json:"postgres_username"`
	PostgresPassword string `json:"postgres_password"`
}

// CredentialsResponse is returned after saving credentials
type CredentialsResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// CredentialsStatusResponse indicates if user has credentials stored
type CredentialsStatusResponse struct {
	HasCredentials bool   `json:"has_credentials"`
	UserEmail      string `json:"user_email"`
}

// CredentialsHandler handles credential management endpoints
type CredentialsHandler struct {
	db               *database.DB
	tailscaleManager *tailscale.TailscaleManager
}

// NewCredentialsHandler creates a new credentials handler
func NewCredentialsHandler(db *database.DB, tsManager *tailscale.TailscaleManager) *CredentialsHandler {
	return &CredentialsHandler{
		db:               db,
		tailscaleManager: tsManager,
	}
}

// getUserEmail extracts user email from request using Tailscale WhoIs
func (h *CredentialsHandler) getUserEmail(r *http.Request) (string, error) {
	if h.tailscaleManager == nil {
		return "", fmt.Errorf("tailscale not enabled - cannot identify user")
	}

	userEmail, err := h.tailscaleManager.GetUserEmailFromRequest(r)
	if err != nil {
		return "", fmt.Errorf("get user email: %w", err)
	}

	return userEmail, nil
}

// HandleSetCredentials handles POST /api/credentials - saves user credentials
func (h *CredentialsHandler) HandleSetCredentials(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Limit request body size to 100KB to prevent DoS attacks
	r.Body = http.MaxBytesReader(w, r.Body, 100*1024)

	// Get user email from Tailscale
	userEmail, err := h.getUserEmail(r)
	if err != nil {
		log.Printf("Error getting user email: %v", err)
		http.Error(w, "Unauthorized - Tailscale authentication required", http.StatusUnauthorized)
		return
	}

	// Parse request body
	var req CredentialsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("JSON decode error for %s: %v", userEmail, err)
		http.Error(w, "Invalid request format", http.StatusBadRequest)
		return
	}

	// Support partial updates: fetch existing credentials first
	existingCreds, err := h.db.GetUserCredentials(userEmail)
	var creds *database.UserCredentials

	if err != nil {
		// No existing credentials - this is a new user
		creds = &database.UserCredentials{
			UserEmail:        userEmail,
			WebRTCUsername:   req.WebRTCUsername,
			WebRTCPassword:   req.WebRTCPassword,
			MinIOAccessKey:   req.MinIOAccessKey,
			MinIOSecretKey:   req.MinIOSecretKey,
			PostgresUsername: req.PostgresUsername,
			PostgresPassword: req.PostgresPassword,
		}
	} else {
		// Existing credentials - merge with provided values (only update non-empty fields)
		creds = existingCreds
		if req.WebRTCUsername != "" {
			creds.WebRTCUsername = req.WebRTCUsername
		}
		if req.WebRTCPassword != "" {
			creds.WebRTCPassword = req.WebRTCPassword
		}
		if req.MinIOAccessKey != "" {
			creds.MinIOAccessKey = req.MinIOAccessKey
		}
		if req.MinIOSecretKey != "" {
			creds.MinIOSecretKey = req.MinIOSecretKey
		}
		if req.PostgresUsername != "" {
			creds.PostgresUsername = req.PostgresUsername
		}
		if req.PostgresPassword != "" {
			creds.PostgresPassword = req.PostgresPassword
		}
	}

	// Validate credentials
	if err := database.ValidateUserCredentials(creds); err != nil {
		log.Printf("Validation error for %s: %v", userEmail, err)
		http.Error(w, "Invalid credentials: please check field requirements", http.StatusBadRequest)
		return
	}

	// Save credentials
	if err := h.db.SaveUserCredentials(creds); err != nil {
		log.Printf("Error saving credentials for %s: %v", userEmail, err)
		database.AuditLog(database.AuditActionCreate, userEmail, r.RemoteAddr, database.AuditResultFailure, err.Error())
		http.Error(w, "Failed to save credentials", http.StatusInternalServerError)
		return
	}

	log.Printf("✓ Saved credentials for user %s", userEmail)
	database.AuditLog(database.AuditActionCreate, userEmail, r.RemoteAddr, database.AuditResultSuccess, "credentials saved")

	// Return success
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(CredentialsResponse{
		Success: true,
		Message: "Credentials saved successfully",
	})
}

// HandleGetCredentialsStatus handles GET /api/credentials/status - checks if user has credentials
func (h *CredentialsHandler) HandleGetCredentialsStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get user email from Tailscale
	userEmail, err := h.getUserEmail(r)
	if err != nil {
		log.Printf("Error getting user email: %v", err)
		http.Error(w, "Unauthorized - Tailscale authentication required", http.StatusUnauthorized)
		return
	}

	// Check if credentials exist
	hasCredentials, err := database.ValidateUserHasCredentials(h.db, userEmail)
	if err != nil {
		log.Printf("Error checking credentials for %s: %v", userEmail, err)
		database.AuditLog(database.AuditActionValidate, userEmail, r.RemoteAddr, database.AuditResultFailure, err.Error())
		http.Error(w, "Failed to check credentials", http.StatusInternalServerError)
		return
	}

	database.AuditLog(database.AuditActionValidate, userEmail, r.RemoteAddr, database.AuditResultSuccess, fmt.Sprintf("has_credentials=%t", hasCredentials))

	// Return status
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(CredentialsStatusResponse{
		HasCredentials: hasCredentials,
		UserEmail:      userEmail,
	})
}

// HandleDeleteCredentials handles DELETE /api/credentials - removes user credentials
func (h *CredentialsHandler) HandleDeleteCredentials(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get user email from Tailscale
	userEmail, err := h.getUserEmail(r)
	if err != nil {
		log.Printf("Error getting user email: %v", err)
		http.Error(w, "Unauthorized - Tailscale authentication required", http.StatusUnauthorized)
		return
	}

	// Delete credentials
	if err := h.db.DeleteUserCredentials(userEmail); err != nil {
		log.Printf("Error deleting credentials for %s: %v", userEmail, err)
		database.AuditLog(database.AuditActionDelete, userEmail, r.RemoteAddr, database.AuditResultFailure, err.Error())
		http.Error(w, "Failed to delete credentials", http.StatusInternalServerError)
		return
	}

	log.Printf("✓ Deleted credentials for user %s", userEmail)
	database.AuditLog(database.AuditActionDelete, userEmail, r.RemoteAddr, database.AuditResultSuccess, "credentials deleted")

	// Return success
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(CredentialsResponse{
		Success: true,
		Message: "Credentials deleted successfully",
	})
}
