// Package api provides HTTP handlers for the web interface
package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pion/mediadevices"
	_ "github.com/pion/mediadevices/pkg/driver/camera"

	"github.com/mikeyg42/webcam/internal/config"
	"github.com/mikeyg42/webcam/internal/crypto"
	"github.com/mikeyg42/webcam/internal/motion"
)

// ConfigHandler handles configuration API requests
type ConfigHandler struct {
	config                *config.Config
	configFile            string
	masterKey             string // AES-256-GCM master key for password encryption
	motionDetector        *motion.Detector
	onQualityPriorityChange func(priority string) error // Callback for dynamic priority updates
}

// NewConfigHandler creates a new configuration handler
func NewConfigHandler(cfg *config.Config, motionDetector *motion.Detector) *ConfigHandler {
	// Determine config file location
	configFile := os.Getenv("CONFIG_FILE")
	if configFile == "" {
		homeDir, _ := os.UserHomeDir()
		configFile = filepath.Join(homeDir, ".webcam2", "config.json")
	}

	// Get or generate master key
	masterKey := os.Getenv("CONFIG_MASTER_KEY")
	if masterKey == "" {
		// Generate new master key
		var err error
		masterKey, err = crypto.GenerateMasterKey()
		if err != nil {
			log.Printf("[WARN] Failed to generate master key: %v. Password encryption disabled.", err)
			masterKey = ""
		} else {
			log.Println("=======================================================================")
			log.Println("[IMPORTANT] No CONFIG_MASTER_KEY found. Generated new master key:")
			log.Printf("  CONFIG_MASTER_KEY=%s", masterKey)
			log.Println("")
			log.Println("Add this to your environment variables to encrypt/decrypt passwords.")
			log.Println("Without this key, encrypted passwords cannot be decrypted!")
			log.Println("=======================================================================")
		}
	}

	return &ConfigHandler{
		config:         cfg,
		configFile:     configFile,
		masterKey:      masterKey,
		motionDetector: motionDetector,
		onQualityPriorityChange: nil,
	}
}

// SetQualityPriorityCallback registers a callback for quality priority changes
func (h *ConfigHandler) SetQualityPriorityCallback(cb func(priority string) error) {
	h.onQualityPriorityChange = cb
}

// ConfigResponse represents the configuration sent to the frontend
type ConfigResponse struct {
	// Recording settings
	Recording RecordingSettings `json:"recording"`

	// Video settings
	Video VideoSettings `json:"video"`

	// Audio settings
	Audio AudioSettings `json:"audio"`

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
	SegmentDuration   int    `json:"segmentDuration"`  // minutes
	PreMotionBuffer   int    `json:"preMotionBuffer"`  // seconds
	PostMotionBuffer  int    `json:"postMotionBuffer"` // seconds
	RetentionDays     int    `json:"retentionDays"`
}

type VideoSettings struct {
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	FrameRate int    `json:"framerate"`
	BitRate   int    `json:"bitrate"`
	DeviceID  string `json:"deviceId"`
}

type AudioSettings struct {
	Enabled    bool   `json:"enabled"`
	DeviceID   string `json:"deviceId"`
	SampleRate int    `json:"sampleRate"`
	Channels   int    `json:"channels"`
	BitRate    int    `json:"bitrate"`
}

type MotionSettings struct {
	Enabled              bool    `json:"enabled"`
	Threshold            float32 `json:"threshold"`
	MinimumArea          int     `json:"minimumArea"`
	CooldownPeriod       int     `json:"cooldownPeriod"` // seconds
	NoMotionDelay        int     `json:"noMotionDelay"`  // seconds
	MinConsecutiveFrames int     `json:"minConsecutiveFrames"`
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
	Username        string `json:"username"`
	Password        string `json:"password,omitempty"` // Don't send to frontend by default
	QualityPriority string `json:"qualityPriority"`    // maximize_quality, minimize_latency, minimize_device_strain
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

	// Return success with updated config and restart requirements
	response := h.configToResponse()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Configuration saved. Motion detection settings applied immediately. Video, audio, recording, and WebRTC changes require application restart.",
		"appliedImmediately": map[string]bool{
			"motion": true,
		},
		"requiresRestart": map[string]bool{
			"video":     true,
			"audio":     true,
			"recording": true,
			"webrtc":    true,
			"storage":   true,
			"email":     true,
		},
		"config": response,
	})
}

// configToResponse converts internal config to API response
func (h *ConfigHandler) configToResponse() ConfigResponse {
	cfg := h.config

	return ConfigResponse{
		Recording: RecordingSettings{
			ContinuousEnabled: false, // This will be read from recorder config
			EventEnabled:      true,
			SaveDirectory:     cfg.Video.OutputPath,
			SegmentDuration:   5,  // minutes, hardcoded in adapter
			PreMotionBuffer:   10, // seconds, hardcoded in adapter
			PostMotionBuffer:  30, // seconds, hardcoded in adapter
			RetentionDays:     7,  // days, hardcoded in adapter
		},
		Video: VideoSettings{
			Width:     cfg.Video.Width,
			Height:    cfg.Video.Height,
			FrameRate: cfg.Video.FrameRate,
			BitRate:   cfg.Video.BitRate,
			DeviceID:  cfg.Video.DeviceID,
		},
		Audio: AudioSettings{
			Enabled:    cfg.Audio.Enabled,
			DeviceID:   cfg.Audio.DeviceID,
			SampleRate: cfg.Audio.SampleRate,
			Channels:   cfg.Audio.Channels,
			BitRate:    cfg.Audio.BitRate,
		},
		Motion: MotionSettings{
			Enabled:              true,
			Threshold:            float32(cfg.Motion.Threshold),
			MinimumArea:          cfg.Motion.MinArea,
			CooldownPeriod:       int(cfg.Motion.CooldownPeriod.Seconds()),
			NoMotionDelay:        int(cfg.Motion.NoMotionDelay.Seconds()),
			MinConsecutiveFrames: cfg.Motion.MinConsecutiveFrames,
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
			Enabled:  cfg.Tailscale.Enabled,
			NodeName: cfg.Tailscale.NodeName,
			AuthKey:  "", // Don't send auth key
			Hostname: cfg.Tailscale.Hostname,
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
			Username:        cfg.WebRTC.Username,
			Password:        "", // Don't send password
			QualityPriority: cfg.WebRTC.QualityPriority,
		},
	}
}

// updateInternalConfig updates the internal config from API request
func (h *ConfigHandler) updateInternalConfig(req *ConfigResponse) {
	cfg := h.config

	// Update video settings
	cfg.Video.Width = req.Video.Width
	cfg.Video.Height = req.Video.Height
	cfg.Video.FrameRate = req.Video.FrameRate
	cfg.Video.BitRate = req.Video.BitRate
	cfg.Video.DeviceID = req.Video.DeviceID
	cfg.Video.OutputPath = req.Recording.SaveDirectory

	// Update audio settings
	cfg.Audio.Enabled = req.Audio.Enabled
	cfg.Audio.DeviceID = req.Audio.DeviceID
	cfg.Audio.SampleRate = req.Audio.SampleRate
	cfg.Audio.Channels = req.Audio.Channels
	cfg.Audio.BitRate = req.Audio.BitRate

	// Update motion settings
	cfg.Motion.Threshold = int(req.Motion.Threshold)
	cfg.Motion.MinArea = req.Motion.MinimumArea
	cfg.Motion.CooldownPeriod = time.Duration(req.Motion.CooldownPeriod) * time.Second
	cfg.Motion.NoMotionDelay = time.Duration(req.Motion.NoMotionDelay) * time.Second
	cfg.Motion.MinConsecutiveFrames = req.Motion.MinConsecutiveFrames

	// Update Tailscale settings
	cfg.Tailscale.Enabled = req.Tailscale.Enabled
	cfg.Tailscale.NodeName = req.Tailscale.NodeName
	cfg.Tailscale.Hostname = req.Tailscale.Hostname
	if req.Tailscale.AuthKey != "" {
		cfg.Tailscale.AuthKey = req.Tailscale.AuthKey
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

	// Update WebRTC auth and quality settings
	cfg.WebRTC.Username = req.WebRTC.Username
	if req.WebRTC.Password != "" {
		cfg.WebRTC.Password = req.WebRTC.Password
	}
	if req.WebRTC.QualityPriority != "" && req.WebRTC.QualityPriority != cfg.WebRTC.QualityPriority {
		oldPriority := cfg.WebRTC.QualityPriority
		cfg.WebRTC.QualityPriority = req.WebRTC.QualityPriority

		// Apply priority change to running quality manager
		if h.onQualityPriorityChange != nil {
			if err := h.onQualityPriorityChange(req.WebRTC.QualityPriority); err != nil {
				log.Printf("[ConfigHandler] Failed to apply quality priority change: %v", err)
			} else {
				log.Printf("[ConfigHandler] Quality priority updated: %s -> %s", oldPriority, req.WebRTC.QualityPriority)
			}
		}
	}

	// Apply motion config updates to running detector
	if h.motionDetector != nil {
		h.motionDetector.UpdateConfig(&cfg.Motion)
	}
}

// validateConfigRequest validates the configuration request
func (h *ConfigHandler) validateConfigRequest(req *ConfigResponse) error {
	// Validate video settings
	if req.Video.Width <= 0 || req.Video.Height <= 0 {
		return fmt.Errorf("invalid video dimensions: %dx%d", req.Video.Width, req.Video.Height)
	}
	if req.Video.FrameRate <= 0 || req.Video.FrameRate > 60 {
		return fmt.Errorf("invalid framerate: %d (must be 1-60)", req.Video.FrameRate)
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

// saveConfig saves the current configuration to a file with encrypted passwords
func (h *ConfigHandler) saveConfig() error {
	// Ensure config directory exists
	configDir := filepath.Dir(h.configFile)
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Get config response (which doesn't include passwords for security)
	configResp := h.configToResponse()

	// Add encrypted passwords if master key is available
	if h.masterKey != "" {
		// Encrypt WebRTC password
		if h.config.WebRTC.Password != "" {
			encrypted, err := crypto.EncryptPassword(h.config.WebRTC.Password, h.masterKey)
			if err != nil {
				log.Printf("[WARN] Failed to encrypt WebRTC password: %v", err)
			} else {
				configResp.WebRTC.Password = encrypted
			}
		}

		// Note: MinIO and PostgreSQL passwords are not stored in config struct
		// They come from the frontend request directly and should be encrypted there
	}

	// Convert config to JSON
	data, err := json.MarshalIndent(configResp, "", "  ")
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

// CameraDevice represents an available camera
type CameraDevice struct {
	DeviceID  string `json:"deviceId"`
	Label     string `json:"label"`
	IsDefault bool   `json:"isDefault"`
}

// MicrophoneDevice represents an available microphone
type MicrophoneDevice struct {
	DeviceID  string `json:"deviceId"`
	Label     string `json:"label"`
	IsDefault bool   `json:"isDefault"`
}

// ListCameras handles GET /api/cameras - returns list of available cameras
func (h *ConfigHandler) ListCameras(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	devices := mediadevices.EnumerateDevices()

	cameras := []CameraDevice{}
	cameraIndex := 0
	for _, device := range devices {
		if device.Kind == mediadevices.VideoInput {
			// Create a more user-friendly label
			label := device.Label
			if label == "" || strings.HasPrefix(label, "0x") {
				// If no label or hex address, generate a friendly name
				label = fmt.Sprintf("Camera %d", cameraIndex+1)
			}

			cameras = append(cameras, CameraDevice{
				DeviceID:  device.DeviceID,
				Label:     label,
				IsDefault: cameraIndex == 0, // First camera is default
			})
			cameraIndex++
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"cameras": cameras,
	})
}

// ListMicrophones handles GET /api/microphones - returns list of available microphones
func (h *ConfigHandler) ListMicrophones(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	devices := mediadevices.EnumerateDevices()

	microphones := []MicrophoneDevice{}
	micIndex := 0
	for _, device := range devices {
		if device.Kind == mediadevices.AudioInput {
			// Create a more user-friendly label
			label := device.Label
			if label == "" || strings.HasPrefix(label, "0x") {
				// If no label or hex address, generate a friendly name
				label = fmt.Sprintf("Microphone %d", micIndex+1)
			}

			microphones = append(microphones, MicrophoneDevice{
				DeviceID:  device.DeviceID,
				Label:     label,
				IsDefault: micIndex == 0, // First microphone is default
			})
			micIndex++
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"microphones": microphones,
	})
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

// RestartBackend handles POST /api/restart - triggers backend restart to reload config
func (h *ConfigHandler) RestartBackend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Respond immediately that restart is starting
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Backend restart initiated. Please wait while configuration changes are applied...",
	})

	// Trigger restart script in background after response is sent
	go func() {
		time.Sleep(500 * time.Millisecond) // Brief delay to ensure response is delivered

		// Determine project root (where restart script is located)
		projectRoot := os.Getenv("PROJECT_ROOT")
		if projectRoot == "" {
			// Try to find it relative to config file
			configDir := filepath.Dir(h.configFile)
			projectRoot = filepath.Join(configDir, "..", "projects", "webcam2")
			// This is a fallback; ideally PROJECT_ROOT should be set
		}

		scriptPath := filepath.Join(projectRoot, "restart-backend-fast.sh")
		log.Printf("[Restart] Executing: %s", scriptPath)

		// Execute restart script (this will kill the current process)
		cmd := exec.Command("/bin/bash", scriptPath)
		cmd.Dir = projectRoot
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			log.Printf("[Restart] Failed to execute restart script: %v", err)
		}
	}()
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

	mux.HandleFunc("/api/cameras", h.ListCameras)
	mux.HandleFunc("/api/microphones", h.ListMicrophones)
	mux.HandleFunc("/api/test-notification", h.TestNotification)
	mux.HandleFunc("/api/restart", h.RestartBackend)
}
