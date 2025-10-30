// Package api provides HTTP handlers for the web interface
package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/mikeyg42/webcam/internal/config"
)

// ConfigHandler handles configuration API requests
type ConfigHandler struct {
	config     *config.Config
	configFile string
}

// NewConfigHandler creates a new configuration handler
func NewConfigHandler(cfg *config.Config) *ConfigHandler {
	// Determine config file location
	configFile := os.Getenv("CONFIG_FILE")
	if configFile == "" {
		homeDir, _ := os.UserHomeDir()
		configFile = filepath.Join(homeDir, ".webcam2", "config.json")
	}

	return &ConfigHandler{
		config:     cfg,
		configFile: configFile,
	}
}

// ConfigResponse represents the configuration sent to the frontend
type ConfigResponse struct {
	// Recording settings
	Recording RecordingSettings `json:"recording"`

	// Video settings
	Video VideoSettings `json:"video"`

	// Motion detection settings
	Motion MotionSettings `json:"motion"`

	// Storage settings
	Storage StorageSettings `json:"storage"`

	// Tailscale settings
	Tailscale TailscaleSettings `json:"tailscale"`

	// Email notification settings
	Email EmailSettings `json:"email"`

	// WebRTC authentication
	WebRTC WebRTCSettings `json:"webrtc"`
}

type RecordingSettings struct {
	ContinuousEnabled bool   `json:"continuousEnabled"`
	EventEnabled      bool   `json:"eventEnabled"`
	SaveDirectory     string `json:"saveDirectory"`
	SegmentDuration   int    `json:"segmentDuration"` // minutes
	PreMotionBuffer   int    `json:"preMotionBuffer"` // seconds
	PostMotionBuffer  int    `json:"postMotionBuffer"` // seconds
	RetentionDays     int    `json:"retentionDays"`
}

type VideoSettings struct {
	Width     int `json:"width"`
	Height    int `json:"height"`
	Framerate int `json:"framerate"`
	BitRate   int `json:"bitrate"`
}

type MotionSettings struct {
	Enabled              bool    `json:"enabled"`
	Threshold            float32 `json:"threshold"`
	MinimumArea          int     `json:"minimumArea"`
	CooldownPeriod       int     `json:"cooldownPeriod"` // seconds
	NoMotionDelay        int     `json:"noMotionDelay"`  // seconds
	MinConsecutiveFrames int     `json:"minConsecutiveFrames"`
	FrameSkip            int     `json:"frameSkip"`
}

type StorageSettings struct {
	MinIO    MinIOSettings    `json:"minio"`
	Postgres PostgresSettings `json:"postgres"`
}

type MinIOSettings struct {
	Endpoint        string `json:"endpoint"`
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey"`
	Bucket          string `json:"bucket"`
	UseSSL          bool   `json:"useSSL"`
}

type PostgresSettings struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Database string `json:"database"`
	Username string `json:"username"`
	Password string `json:"password"`
	SSLMode  string `json:"sslMode"`
}

type TailscaleSettings struct {
	Enabled    bool   `json:"enabled"`
	NodeName   string `json:"nodeName"`
	AuthKey    string `json:"authKey,omitempty"` // Don't send to frontend by default
	Hostname   string `json:"hostname"`
	ListenPort int    `json:"listenPort"`
}

type EmailSettings struct {
	Method            string `json:"method"` // "mailersend", "gmail", "disabled"
	MailSendAPIToken  string `json:"mailsendApiToken,omitempty"`
	ToEmail           string `json:"toEmail"`
	FromEmail         string `json:"fromEmail"`
	GmailClientID     string `json:"gmailClientId,omitempty"`
	GmailClientSecret string `json:"gmailClientSecret,omitempty"`
}

type WebRTCSettings struct {
	Username string `json:"username"`
	Password string `json:"password,omitempty"` // Don't send to frontend by default
}

// GetConfig handles GET /api/config
func (h *ConfigHandler) GetConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Convert internal config to API response format
	response := h.configToResponse()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// UpdateConfig handles POST /api/config
func (h *ConfigHandler) UpdateConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ConfigResponse
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	// Validate the configuration
	if err := h.validateConfigRequest(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid configuration: %v", err), http.StatusBadRequest)
		return
	}

	// Update internal config
	h.updateInternalConfig(&req)

	// Save to file
	if err := h.saveConfig(); err != nil {
		log.Printf("Warning: Failed to save config to file: %v", err)
		// Don't fail the request, just log the warning
	}

	// Return success with updated config
	response := h.configToResponse()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Configuration updated successfully. Restart the application for all changes to take effect.",
		"config":  response,
	})
}

// configToResponse converts internal config to API response
func (h *ConfigHandler) configToResponse() ConfigResponse {
	cfg := h.config

	return ConfigResponse{
		Recording: RecordingSettings{
			ContinuousEnabled: false, // This will be read from recorder config
			EventEnabled:      true,
			SaveDirectory:     cfg.VideoConfig.OutputPath,
			SegmentDuration:   5,  // minutes, hardcoded in adapter
			PreMotionBuffer:   10, // seconds, hardcoded in adapter
			PostMotionBuffer:  30, // seconds, hardcoded in adapter
			RetentionDays:     7,  // days, hardcoded in adapter
		},
		Video: VideoSettings{
			Width:     cfg.VideoConfig.Width,
			Height:    cfg.VideoConfig.Height,
			Framerate: cfg.VideoConfig.Framerate,
			BitRate:   cfg.VideoConfig.BitRate,
		},
		Motion: MotionSettings{
			Enabled:              true,
			Threshold:            cfg.MotionConfig.Threshold,
			MinimumArea:          cfg.MotionConfig.MinimumArea,
			CooldownPeriod:       int(cfg.MotionConfig.CooldownPeriod.Seconds()),
			NoMotionDelay:        int(cfg.MotionConfig.NoMotionDelay.Seconds()),
			MinConsecutiveFrames: cfg.MotionConfig.MinConsecutiveFrames,
			FrameSkip:            cfg.MotionConfig.FrameSkip,
		},
		Storage: StorageSettings{
			MinIO: MinIOSettings{
				Endpoint:        "localhost:9000",
				AccessKeyID:     "minioadmin",
				SecretAccessKey: "", // Don't send secret by default
				Bucket:          "recordings",
				UseSSL:          false,
			},
			Postgres: PostgresSettings{
				Host:     "localhost",
				Port:     5432,
				Database: "recordings",
				Username: "recorder",
				Password: "", // Don't send password by default
				SSLMode:  "disable",
			},
		},
		Tailscale: TailscaleSettings{
			Enabled:    cfg.TailscaleConfig.Enabled,
			NodeName:   cfg.TailscaleConfig.NodeName,
			AuthKey:    "", // Don't send auth key
			Hostname:   cfg.TailscaleConfig.Hostname,
			ListenPort: cfg.TailscaleConfig.ListenPort,
		},
		Email: EmailSettings{
			Method:            cfg.EmailMethod,
			MailSendAPIToken:  "", // Don't send API token
			ToEmail:           cfg.MailSendConfig.ToEmail,
			FromEmail:         cfg.MailSendConfig.FromEmail,
			GmailClientID:     "", // Don't send OAuth credentials
			GmailClientSecret: "",
		},
		WebRTC: WebRTCSettings{
			Username: cfg.WebrtcAuth.Username,
			Password: "", // Don't send password
		},
	}
}

// updateInternalConfig updates the internal config from API request
func (h *ConfigHandler) updateInternalConfig(req *ConfigResponse) {
	cfg := h.config

	// Update video settings
	cfg.VideoConfig.Width = req.Video.Width
	cfg.VideoConfig.Height = req.Video.Height
	cfg.VideoConfig.Framerate = req.Video.Framerate
	cfg.VideoConfig.BitRate = req.Video.BitRate
	cfg.VideoConfig.OutputPath = req.Recording.SaveDirectory

	// Update motion settings
	cfg.MotionConfig.Threshold = req.Motion.Threshold
	cfg.MotionConfig.MinimumArea = req.Motion.MinimumArea
	cfg.MotionConfig.CooldownPeriod = time.Duration(req.Motion.CooldownPeriod) * time.Second
	cfg.MotionConfig.NoMotionDelay = time.Duration(req.Motion.NoMotionDelay) * time.Second
	cfg.MotionConfig.MinConsecutiveFrames = req.Motion.MinConsecutiveFrames
	cfg.MotionConfig.FrameSkip = req.Motion.FrameSkip

	// Update Tailscale settings
	cfg.TailscaleConfig.Enabled = req.Tailscale.Enabled
	cfg.TailscaleConfig.NodeName = req.Tailscale.NodeName
	cfg.TailscaleConfig.Hostname = req.Tailscale.Hostname
	cfg.TailscaleConfig.ListenPort = req.Tailscale.ListenPort
	if req.Tailscale.AuthKey != "" {
		cfg.TailscaleConfig.AuthKey = req.Tailscale.AuthKey
	}

	// Update email settings
	cfg.EmailMethod = req.Email.Method
	cfg.MailSendConfig.ToEmail = req.Email.ToEmail
	cfg.MailSendConfig.FromEmail = req.Email.FromEmail
	if req.Email.MailSendAPIToken != "" {
		cfg.MailSendConfig.APIToken = req.Email.MailSendAPIToken
	}

	// Update Gmail OAuth2 settings
	cfg.GmailOAuth2Config.ToEmail = req.Email.ToEmail
	cfg.GmailOAuth2Config.FromEmail = req.Email.FromEmail
	if req.Email.GmailClientID != "" {
		cfg.GmailOAuth2Config.ClientID = req.Email.GmailClientID
	}
	if req.Email.GmailClientSecret != "" {
		cfg.GmailOAuth2Config.ClientSecret = req.Email.GmailClientSecret
	}

	// Update WebRTC auth
	cfg.WebrtcAuth.Username = req.WebRTC.Username
	if req.WebRTC.Password != "" {
		cfg.WebrtcAuth.Password = req.WebRTC.Password
	}
}

// validateConfigRequest validates the configuration request
func (h *ConfigHandler) validateConfigRequest(req *ConfigResponse) error {
	// Validate video settings
	if req.Video.Width <= 0 || req.Video.Height <= 0 {
		return fmt.Errorf("invalid video dimensions: %dx%d", req.Video.Width, req.Video.Height)
	}
	if req.Video.Framerate <= 0 || req.Video.Framerate > 60 {
		return fmt.Errorf("invalid framerate: %d (must be 1-60)", req.Video.Framerate)
	}
	if req.Video.BitRate < 100000 || req.Video.BitRate > 10000000 {
		return fmt.Errorf("invalid bitrate: %d (must be 100000-10000000)", req.Video.BitRate)
	}

	// Validate motion settings
	if req.Motion.Threshold < 0 || req.Motion.Threshold > 255 {
		return fmt.Errorf("invalid motion threshold: %.2f (must be 0-255)", req.Motion.Threshold)
	}
	if req.Motion.MinimumArea < 0 {
		return fmt.Errorf("invalid minimum area: %d", req.Motion.MinimumArea)
	}

	// Validate recording settings
	if req.Recording.SaveDirectory == "" {
		return fmt.Errorf("save directory cannot be empty")
	}

	return nil
}

// saveConfig saves the current configuration to a file
func (h *ConfigHandler) saveConfig() error {
	// Ensure config directory exists
	configDir := filepath.Dir(h.configFile)
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Convert config to JSON
	data, err := json.MarshalIndent(h.configToResponse(), "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Write to file
	if err := os.WriteFile(h.configFile, data, 0600); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	log.Printf("Configuration saved to %s", h.configFile)
	return nil
}

// TestNotification handles POST /api/test-notification
func (h *ConfigHandler) TestNotification(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// This endpoint needs to be handled by the main application
	// For now, return a message indicating the feature needs implementation
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": false,
		"message": "Notification test requires application restart with updated configuration. Save your settings and restart the camera application to enable notifications.",
	})
}

// RegisterRoutes registers HTTP routes for configuration API
func (h *ConfigHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			h.GetConfig(w, r)
		case http.MethodPost:
			h.UpdateConfig(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/test-notification", h.TestNotification)
}
