package config

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"time"

	"gopkg.in/yaml.v2"
)

// Bitrate constants for WebRTC
const (
	MinBitrate = 100    // 100 kbps minimum
	MaxBitrate = 100000 // 100 Mbps maximum
)

// Config represents the complete application configuration
type Config struct {
	// Core service settings
	Service ServiceConfig `yaml:"service" json:"service"`

	// Media settings
	Video   VideoConfig   `yaml:"video" json:"video"`
	Audio   AudioConfig   `yaml:"audio" json:"audio"`
	Encoder EncoderConfig `yaml:"encoder" json:"encoder"`

	// Aliases for backward compatibility
	VideoSettings   VideoConfig   `yaml:"-" json:"-"`
	AudioSettings   AudioConfig   `yaml:"-" json:"-"`
	EncoderSettings EncoderConfig `yaml:"-" json:"-"`

	// Recording settings
	Recording RecordingConfig `yaml:"recording" json:"recording"`

	// Storage backends
	Storage StorageConfig `yaml:"storage" json:"storage"`

	// Motion detection
	Motion MotionConfig `yaml:"motion" json:"motion"`

	// WebRTC settings
	WebRTC WebRTCConfig `yaml:"webrtc" json:"webrtc"`

	// WebSocket configuration
	WebSocket WebSocketConfig `yaml:"websocket" json:"websocket"`

	// Tailscale integration
	Tailscale       TailscaleConfig `yaml:"tailscale" json:"tailscale"`
	TailscaleConfig TailscaleConfig `yaml:"-" json:"-"` // Alias for compatibility

	// Email notifications
	EmailMethod       string            `yaml:"email_method" json:"email_method" default:"disabled"`
	MailSendConfig    MailSendConfig    `yaml:"mailsend" json:"mailsend"`
	GmailOAuth2Config GmailOAuth2Config `yaml:"gmail_oauth2" json:"gmail_oauth2"`

	// Pipeline configuration
	Pipeline PipelineConfig `yaml:"pipeline" json:"pipeline"`

	// Buffer management
	Buffer BufferConfig `yaml:"buffer" json:"buffer"`

	// API server
	API APIConfig `yaml:"api" json:"api"`

	// Metrics and monitoring
	Metrics                 MetricsConfig `yaml:"metrics" json:"metrics"`
	StatsCollectionInterval time.Duration `yaml:"stats_collection_interval" json:"stats_collection_interval" default:"30s"`

	// Logging
	Log LogConfig `yaml:"log" json:"log"`
}

// ServiceConfig contains service-level configuration
type ServiceConfig struct {
	Name        string `yaml:"name" json:"name" default:"webcam-security"`
	Environment string `yaml:"environment" json:"environment" default:"production"`
	InstanceID  string `yaml:"instance_id" json:"instance_id"`

	// Graceful shutdown
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout" json:"shutdown_timeout" default:"30s"`

	// Health check
	HealthCheckInterval time.Duration `yaml:"health_check_interval" json:"health_check_interval" default:"30s"`

	// Resource limits
	MaxCPU    int `yaml:"max_cpu" json:"max_cpu" default:"0"`             // 0 = no limit
	MaxMemory int `yaml:"max_memory_mb" json:"max_memory_mb" default:"0"` // 0 = no limit
}

// VideoConfig contains video capture and processing settings
type VideoConfig struct {
	// Resolution
	Width  int `yaml:"width" json:"width" default:"640"`
	Height int `yaml:"height" json:"height" default:"480"`

	// Frame rate
	FrameRate int `yaml:"frame_rate" json:"frame_rate" default:"30"`

	// Quality settings
	Quality string `yaml:"quality" json:"quality" default:"high"` // low, medium, high, ultra

	// BitRate in bps (not kbps)
	BitRate int `yaml:"bitrate" json:"bitrate" default:"2000000"` // 2 Mbps

	// Keyframe interval in frames
	KeyFrameInterval int `yaml:"keyframe_interval" json:"keyframe_interval" default:"60"`

	// Device selection
	DeviceID string `yaml:"device_id" json:"device_id" default:""`

	// Format settings
	InputFormat string `yaml:"input_format" json:"input_format" default:"RGBA"`
	ColorSpace  string `yaml:"color_space" json:"color_space" default:"sRGB"`
	PixelFormat string `yaml:"pixel_format" json:"pixel_format" default:"yuv420p"`

	// Output path for recordings
	OutputPath string `yaml:"output_path" json:"output_path" default:"/var/recordings"`
}

// AudioConfig contains audio capture settings
type AudioConfig struct {
	Enabled      bool   `yaml:"enabled" json:"enabled" default:"true"`
	DeviceID     string `yaml:"device_id" json:"device_id" default:""`
	SampleRate   int    `yaml:"sample_rate" json:"sample_rate" default:"48000"`
	Channels     int    `yaml:"channels" json:"channels" default:"2"`
	BitDepth     int    `yaml:"bit_depth" json:"bit_depth" default:"16"`
	Codec        string `yaml:"codec" json:"codec" default:"opus"`
	BitRate      int    `yaml:"bitrate" json:"bitrate" default:"128000"` // 128 kbps
	ChannelCount int    `yaml:"channel_count" json:"channel_count" default:"2"`
}

// EncoderConfig contains video encoding settings
type EncoderConfig struct {
	// Basic video parameters (duplicated for compatibility)
	Width            int `yaml:"width" json:"width" default:"640"`
	Height           int `yaml:"height" json:"height" default:"480"`
	FrameRate        int `yaml:"frame_rate" json:"frame_rate" default:"30"`
	BitRateKbps      int `yaml:"bitrate_kbps" json:"bitrate_kbps" default:"1500"`
	KeyFrameInterval int `yaml:"keyframe_interval" json:"keyframe_interval" default:"60"`

	// Codec selection
	Codec            string `yaml:"codec" json:"codec" default:"auto"`
	PreferredCodec   string `yaml:"preferred_codec" json:"preferred_codec" default:"auto"` // auto, h264, vp9, av1
	PreferHardware   bool   `yaml:"prefer_hardware" json:"prefer_hardware" default:"true"`
	PreferredEncoder string `yaml:"preferred_encoder" json:"preferred_encoder" default:""`

	// VP9 specific settings
	CPUUsed     int    `yaml:"cpu_used" json:"cpu_used" default:"5"`         // 0-8
	Deadline    string `yaml:"deadline" json:"deadline" default:"realtime"`  // realtime, good, best
	RowMT       bool   `yaml:"row_mt" json:"row_mt" default:"true"`          // Row multithreading
	TileColumns int    `yaml:"tile_columns" json:"tile_columns" default:"2"` // 0-6

	// H.264 specific settings
	Profile string `yaml:"profile" json:"profile" default:"main"`  // baseline, main, high
	Preset  string `yaml:"preset" json:"preset" default:"fast"`    // ultrafast, fast, medium, slow
	Tune    string `yaml:"tune" json:"tune" default:"zerolatency"` // zerolatency, film, animation

	// Performance tuning
	Threads    int `yaml:"threads" json:"threads" default:"4"`
	BufferSize int `yaml:"buffer_size" json:"buffer_size" default:"100"`

	// Quality settings
	MinQuantizer int `yaml:"min_quantizer" json:"min_quantizer" default:"4"`
	MaxQuantizer int `yaml:"max_quantizer" json:"max_quantizer" default:"48"`

	// Real-time settings
	RealTime   bool `yaml:"real_time" json:"real_time" default:"true"`
	LowLatency bool `yaml:"low_latency" json:"low_latency" default:"true"`

	// Dynamic adjustment
	AdaptiveBitRate bool `yaml:"adaptive_bitrate" json:"adaptive_bitrate" default:"true"`
	MinBitRate      int  `yaml:"min_bitrate" json:"min_bitrate" default:"500"`  // kbps
	MaxBitRate      int  `yaml:"max_bitrate" json:"max_bitrate" default:"5000"` // kbps

	// Advanced settings
	MaxBFrames            int  `yaml:"max_b_frames" json:"max_b_frames" default:"0"`
	AllowSoftwareFallback bool `yaml:"allow_software_fallback" json:"allow_software_fallback" default:"true"`
}

// WebSocketConfig contains WebSocket server configuration
type WebSocketConfig struct {
	Enabled         bool          `yaml:"enabled" json:"enabled" default:"true"`
	ListenAddr      string        `yaml:"listen_addr" json:"listen_addr" default:":8080"`
	Path            string        `yaml:"path" json:"path" default:"/ws"`
	ReadBufferSize  int           `yaml:"read_buffer_size" json:"read_buffer_size" default:"1024"`
	WriteBufferSize int           `yaml:"write_buffer_size" json:"write_buffer_size" default:"1024"`
	PingInterval    time.Duration `yaml:"ping_interval" json:"ping_interval" default:"30s"`
	PongTimeout     time.Duration `yaml:"pong_timeout" json:"pong_timeout" default:"60s"`
	MaxMessageSize  int64         `yaml:"max_message_size" json:"max_message_size" default:"512000"`

	// Authentication
	RequireAuth bool   `yaml:"require_auth" json:"require_auth" default:"false"`
	AuthToken   string `yaml:"auth_token" json:"auth_token"`

	// TLS
	UseTLS   bool   `yaml:"use_tls" json:"use_tls" default:"false"`
	CertFile string `yaml:"cert_file" json:"cert_file"`
	KeyFile  string `yaml:"key_file" json:"key_file"`
}

// MailSendConfig contains MailerSend email service configuration
type MailSendConfig struct {
	APIToken  string `yaml:"api_token" json:"api_token"`
	ToEmail   string `yaml:"to_email" json:"to_email"`
	FromEmail string `yaml:"from_email" json:"from_email"`
	FromName  string `yaml:"from_name" json:"from_name" default:"WebCam Security"`
	Debug     bool   `yaml:"debug" json:"debug" default:"false"`

	// Advanced settings
	MaxRetries int           `yaml:"max_retries" json:"max_retries" default:"3"`
	Timeout    time.Duration `yaml:"timeout" json:"timeout" default:"30s"`
	RateLimit  int           `yaml:"rate_limit" json:"rate_limit" default:"10"` // emails per minute
}

// GmailOAuth2Config contains Gmail OAuth2 configuration
type GmailOAuth2Config struct {
	ClientID           string `yaml:"client_id" json:"client_id"`
	ClientSecret       string `yaml:"client_secret" json:"client_secret"`
	ToEmail            string `yaml:"to_email" json:"to_email"`
	FromEmail          string `yaml:"from_email" json:"from_email"`
	FromName           string `yaml:"from_name" json:"from_name" default:"WebCam Security"`
	RedirectURL        string `yaml:"redirect_url" json:"redirect_url" default:"http://localhost:8080/oauth2callback"`
	TokenStorePath     string `yaml:"token_store_path" json:"token_store_path" default:"~/.webcam/gmail_token.json"`
	TokenEncryptionKey string `yaml:"token_encryption_key" json:"token_encryption_key"`
	Debug              bool   `yaml:"debug" json:"debug" default:"false"`

	// OAuth2 scopes
	Scopes []string `yaml:"scopes" json:"scopes"`
}

// RecordingConfig contains recording-specific settings
type RecordingConfig struct {
	Enabled bool `yaml:"enabled" json:"enabled" default:"true"`

	// Recording modes
	ContinuousEnabled bool `yaml:"continuous_enabled" json:"continuous_enabled" default:"true"`
	EventEnabled      bool `yaml:"event_enabled" json:"event_enabled" default:"true"`

	// Output settings
	OutputDir  string `yaml:"output_dir" json:"output_dir" default:"/var/recordings"`
	FileFormat string `yaml:"file_format" json:"file_format" default:"mp4"` // mp4, webm, mkv

	// Segment configuration
	SegmentDuration time.Duration `yaml:"segment_duration" json:"segment_duration" default:"5m"`
	MaxSegmentSize  int64         `yaml:"max_segment_size_mb" json:"max_segment_size_mb" default:"100"`

	// Buffer configuration
	RingBufferSize   time.Duration `yaml:"ring_buffer_size" json:"ring_buffer_size" default:"60s"`
	PreMotionBuffer  time.Duration `yaml:"pre_motion_buffer" json:"pre_motion_buffer" default:"10s"`
	PostMotionBuffer time.Duration `yaml:"post_motion_buffer" json:"post_motion_buffer" default:"30s"`

	// Retention
	RetentionDays int `yaml:"retention_days" json:"retention_days" default:"30"`
	MaxStorageGB  int `yaml:"max_storage_gb" json:"max_storage_gb" default:"100"`

	// Temporary storage
	TempDir  string `yaml:"temp_dir" json:"temp_dir" default:"/tmp/recorder"`
	SpoolDir string `yaml:"spool_dir" json:"spool_dir" default:"/var/spool/recorder"`
}

// StorageConfig contains storage backend configuration
type StorageConfig struct {
	Type     string         `yaml:"type" json:"type" default:"local"` // local, minio, s3
	Local    LocalStorage   `yaml:"local" json:"local"`
	MinIO    MinIOConfig    `yaml:"minio" json:"minio"`
	S3       S3Config       `yaml:"s3" json:"s3"`
	Postgres PostgresConfig `yaml:"postgres" json:"postgres"`

	// Upload settings
	UploadConcurrency int           `yaml:"upload_concurrency" json:"upload_concurrency" default:"3"`
	UploadRetries     int           `yaml:"upload_retries" json:"upload_retries" default:"3"`
	UploadTimeout     time.Duration `yaml:"upload_timeout" json:"upload_timeout" default:"5m"`

	// Cleanup settings
	CleanupInterval  time.Duration `yaml:"cleanup_interval" json:"cleanup_interval" default:"1h"`
	CleanupBatchSize int           `yaml:"cleanup_batch_size" json:"cleanup_batch_size" default:"100"`
}

// LocalStorage configuration
type LocalStorage struct {
	BasePath         string `yaml:"base_path" json:"base_path" default:"/var/lib/recorder"`
	MaxSize          int64  `yaml:"max_size_gb" json:"max_size_gb" default:"100"`
	UseDateHierarchy bool   `yaml:"use_date_hierarchy" json:"use_date_hierarchy" default:"true"`
	Permissions      string `yaml:"permissions" json:"permissions" default:"0755"`
}

// MinIOConfig contains MinIO-specific settings
type MinIOConfig struct {
	Endpoint        string `yaml:"endpoint" json:"endpoint" default:"localhost:9000"`
	AccessKeyID     string `yaml:"access_key_id" json:"access_key_id"`
	SecretAccessKey string `yaml:"secret_access_key" json:"secret_access_key"`
	UseSSL          bool   `yaml:"use_ssl" json:"use_ssl" default:"false"`
	Bucket          string `yaml:"bucket" json:"bucket" default:"recordings"`
	Region          string `yaml:"region" json:"region" default:"us-east-1"`

	// Connection pooling
	MaxUploads   int `yaml:"max_uploads" json:"max_uploads" default:"10"`
	MaxDownloads int `yaml:"max_downloads" json:"max_downloads" default:"20"`

	// Timeouts
	ConnectTimeout time.Duration `yaml:"connect_timeout" json:"connect_timeout" default:"30s"`
	RequestTimeout time.Duration `yaml:"request_timeout" json:"request_timeout" default:"5m"`

	// Multipart upload settings
	PartSize    int64 `yaml:"part_size_mb" json:"part_size_mb" default:"64"` // MB
	Concurrency int   `yaml:"concurrency" json:"concurrency" default:"4"`
}

// S3Config contains AWS S3 settings
type S3Config struct {
	Region          string `yaml:"region" json:"region" default:"us-east-1"`
	Bucket          string `yaml:"bucket" json:"bucket"`
	AccessKeyID     string `yaml:"access_key_id" json:"access_key_id"`
	SecretAccessKey string `yaml:"secret_access_key" json:"secret_access_key"`
	Endpoint        string `yaml:"endpoint" json:"endpoint"` // For S3-compatible services
}

// PostgresConfig contains PostgreSQL database settings for metadata storage
type PostgresConfig struct {
	Host            string        `yaml:"host" json:"host" default:"localhost"`
	Port            int           `yaml:"port" json:"port" default:"5432"`
	Database        string        `yaml:"database" json:"database" default:"recordings"`
	Username        string        `yaml:"username" json:"username" default:"recorder"`
	Password        string        `yaml:"password" json:"password" default:""`
	SSLMode         string        `yaml:"ssl_mode" json:"ssl_mode" default:"disable"`
	MaxConnections  int           `yaml:"max_connections" json:"max_connections" default:"25"`
	MaxIdleConns    int           `yaml:"max_idle_conns" json:"max_idle_conns" default:"5"`
	ConnMaxLifetime time.Duration `yaml:"conn_max_lifetime" json:"conn_max_lifetime" default:"5m"`
	ConnMaxIdleTime time.Duration `yaml:"conn_max_idle_time" json:"conn_max_idle_time" default:"5m"`
}

// MotionConfig contains motion detection settings
type MotionConfig struct {
	Enabled       bool    `yaml:"enabled" json:"enabled" default:"true"`
	Sensitivity   float64 `yaml:"sensitivity" json:"sensitivity" default:"0.3"`
	MinConfidence float64 `yaml:"min_confidence" json:"min_confidence" default:"0.5"`

	NoMotionDelay        time.Duration `yaml:"no_motion_delay" json:"no_motion_delay" default:"2s"`
	MinConsecutiveFrames int           `yaml:"min_consecutive_frames" json:"min_consecutive_frames" default:"3"`
	MaxConsecutiveFrames int           `yaml:"max_consecutive_frames" json:"max_consecutive_frames" default:"10"`

	// Detection parameters
	MinArea        int     `yaml:"min_area" json:"min_area" default:"100"`
	MaxArea        int     `yaml:"max_area" json:"max_area" default:"0"` // 0 = no limit
	MinAspectRatio float64 `yaml:"min_aspect_ratio" json:"min_aspect_ratio" default:"0.5"`
	MaxAspectRatio float64 `yaml:"max_aspect_ratio" json:"max_aspect_ratio" default:"2.0"`

	// Background subtraction parameters
	LearningRate float64 `yaml:"learning_rate" json:"learning_rate" default:"0.01"`
	Threshold    int     `yaml:"threshold" json:"threshold" default:"25"`

	// Processing
	BlurSize       int `yaml:"blur_size" json:"blur_size" default:"21"`
	DilationSize   int `yaml:"dilation_size" json:"dilation_size" default:"5"`
	MorphologySize int `yaml:"morphology_size" json:"morphology_size" default:"5"`

	// Cooldown
	CooldownPeriod time.Duration `yaml:"cooldown_period" json:"cooldown_period" default:"5s"`
}

// WebRTCConfig contains WebRTC settings
type WebRTCConfig struct {
	// ICE servers
	ICEServers []ICEServer `yaml:"ice_servers" json:"ice_servers"`

	// Authentication
	Username string `yaml:"username" json:"username" default:""`
	Password string `yaml:"password" json:"password" default:""`

	// Connection policies
	ICETransportPolicy   string `yaml:"ice_transport_policy" json:"ice_transport_policy" default:"all"`
	BundlePolicy         string `yaml:"bundle_policy" json:"bundle_policy" default:"max-bundle"`
	RTCPMuxPolicy        string `yaml:"rtcp_mux_policy" json:"rtcp_mux_policy" default:"require"`
	ICECandidatePoolSize int    `yaml:"ice_candidate_pool_size" json:"ice_candidate_pool_size" default:"3"`

	// Timeouts
	ConnectionTimeout   time.Duration `yaml:"connection_timeout" json:"connection_timeout" default:"30s"`
	DisconnectedTimeout time.Duration `yaml:"disconnected_timeout" json:"disconnected_timeout" default:"5s"`
	FailedTimeout       time.Duration `yaml:"failed_timeout" json:"failed_timeout" default:"5s"`
	KeepaliveInterval   time.Duration `yaml:"keepalive_interval" json:"keepalive_interval" default:"10s"`
}

// WebRTCAuth is an alias for WebRTCConfig for backward compatibility
type WebRTCAuth = WebRTCConfig

// ICEServer configuration
type ICEServer struct {
	URLs       []string `yaml:"urls" json:"urls"`
	Username   string   `yaml:"username,omitempty" json:"username,omitempty"`
	Credential string   `yaml:"credential,omitempty" json:"credential,omitempty"`
}

// TailscaleConfig contains Tailscale VPN settings
type TailscaleConfig struct {
	Enabled         bool   `yaml:"enabled" json:"enabled" default:"false"`
	AuthKey         string `yaml:"auth_key" json:"auth_key"`
	ControlURL      string `yaml:"control_url" json:"control_url" default:""`
	Hostname        string `yaml:"hostname" json:"hostname"`
	NodeName        string `yaml:"node_name" json:"node_name" default:""`
	StateDir        string `yaml:"state_dir" json:"state_dir" default:"/var/lib/tailscale"`
	AcceptRoutes    bool   `yaml:"accept_routes" json:"accept_routes" default:"false"`
	ShieldsUp       bool   `yaml:"shields_up" json:"shields_up" default:"false"`
	ExitNode        string `yaml:"exit_node" json:"exit_node"`
	AdvertiseRoutes string `yaml:"advertise_routes" json:"advertise_routes"`
}

// PipelineConfig contains media pipeline settings
type PipelineConfig struct {
	// Frame processing
	FrameQueueSize int `yaml:"frame_queue_size" json:"frame_queue_size" default:"30"`

	// Worker pools
	EncoderWorkers  int `yaml:"encoder_workers" json:"encoder_workers" default:"2"`
	UploaderWorkers int `yaml:"uploader_workers" json:"uploader_workers" default:"3"`

	// Priorities
	MotionPriority     int `yaml:"motion_priority" json:"motion_priority" default:"10"`
	ContinuousPriority int `yaml:"continuous_priority" json:"continuous_priority" default:"5"`

	// Backpressure
	EnableBackpressure    bool    `yaml:"enable_backpressure" json:"enable_backpressure" default:"true"`
	BackpressureThreshold float64 `yaml:"backpressure_threshold" json:"backpressure_threshold" default:"0.8"`
}

// BufferConfig contains buffer management settings
type BufferConfig struct {
	// Ring buffer
	RingBufferFrames int `yaml:"ring_buffer_frames" json:"ring_buffer_frames" default:"1800"` // 60s @ 30fps

	// Frame pool
	FramePoolSize int `yaml:"frame_pool_size" json:"frame_pool_size" default:"100"`
	ImagePoolSize int `yaml:"image_pool_size" json:"image_pool_size" default:"50"`

	// Memory management
	MaxMemoryMB int           `yaml:"max_memory_mb" json:"max_memory_mb" default:"500"`
	GCInterval  time.Duration `yaml:"gc_interval" json:"gc_interval" default:"1m"`
}

// APIConfig contains API server settings
type APIConfig struct {
	Enabled    bool   `yaml:"enabled" json:"enabled" default:"true"`
	ListenAddr string `yaml:"listen_addr" json:"listen_addr" default:":8080"`

	// TLS
	TLSEnabled  bool   `yaml:"tls_enabled" json:"tls_enabled" default:"false"`
	TLSCertFile string `yaml:"tls_cert_file" json:"tls_cert_file"`
	TLSKeyFile  string `yaml:"tls_key_file" json:"tls_key_file"`

	// CORS
	CORSEnabled bool     `yaml:"cors_enabled" json:"cors_enabled" default:"true"`
	CORSOrigins []string `yaml:"cors_origins" json:"cors_origins"`

	// Rate limiting
	RateLimitEnabled bool `yaml:"rate_limit_enabled" json:"rate_limit_enabled" default:"true"`
	RateLimitRPS     int  `yaml:"rate_limit_rps" json:"rate_limit_rps" default:"100"`

	// Authentication
	AuthEnabled bool   `yaml:"auth_enabled" json:"auth_enabled" default:"false"`
	AuthType    string `yaml:"auth_type" json:"auth_type" default:"bearer"`
	AuthSecret  string `yaml:"auth_secret" json:"auth_secret"`

	// Timeouts
	ReadTimeout  time.Duration `yaml:"read_timeout" json:"read_timeout" default:"30s"`
	WriteTimeout time.Duration `yaml:"write_timeout" json:"write_timeout" default:"30s"`
	IdleTimeout  time.Duration `yaml:"idle_timeout" json:"idle_timeout" default:"120s"`
}

// MetricsConfig contains monitoring settings
type MetricsConfig struct {
	Enabled    bool   `yaml:"enabled" json:"enabled" default:"true"`
	ListenAddr string `yaml:"listen_addr" json:"listen_addr" default:":9090"`
	Path       string `yaml:"path" json:"path" default:"/metrics"`

	// Prometheus
	PrometheusEnabled bool `yaml:"prometheus_enabled" json:"prometheus_enabled" default:"true"`

	// Collection intervals
	CollectInterval time.Duration `yaml:"collect_interval" json:"collect_interval" default:"10s"`

	// Detailed metrics
	DetailedMetrics bool `yaml:"detailed_metrics" json:"detailed_metrics" default:"false"`
}

// LogConfig contains logging settings
type LogConfig struct {
	Level  string `yaml:"level" json:"level" default:"info"`
	Format string `yaml:"format" json:"format" default:"json"` // json, console

	// Output paths
	OutputPath      string `yaml:"output_path" json:"output_path" default:"stdout"`
	ErrorOutputPath string `yaml:"error_output_path" json:"error_output_path" default:"stderr"`

	// File rotation
	RotateEnabled    bool `yaml:"rotate_enabled" json:"rotate_enabled" default:"true"`
	RotateMaxSize    int  `yaml:"rotate_max_size_mb" json:"rotate_max_size_mb" default:"100"`
	RotateMaxAge     int  `yaml:"rotate_max_age_days" json:"rotate_max_age_days" default:"7"`
	RotateMaxBackups int  `yaml:"rotate_max_backups" json:"rotate_max_backups" default:"3"`
}

// NewDefaultConfig returns a complete default configuration
// This is an alias for DefaultConfig to match expected naming
func NewDefaultConfig() *Config {
	return DefaultConfig()
}

// DefaultConfig returns a complete default configuration
func DefaultConfig() *Config {
	cfg := &Config{
		Service: ServiceConfig{
			Name:                "webcam-security",
			Environment:         "production",
			ShutdownTimeout:     30 * time.Second,
			HealthCheckInterval: 30 * time.Second,
		},
		Video: VideoConfig{
			Width:            640,
			Height:           480,
			FrameRate:        30,
			Quality:          "high",
			BitRate:          2000,  // Adjusted to be within MaxBitRate: 5000 range
			KeyFrameInterval: 60,
			OutputPath:       "/tmp/recordings",  // Changed: use /tmp for local testing
		},
		Audio: AudioConfig{
			Enabled:      true,
			SampleRate:   48000,
			Channels:     2,
			BitDepth:     16,
			Codec:        "opus",
			BitRate:      128000,
			ChannelCount: 2,
		},
		Encoder: EncoderConfig{
			Width:                 640,
			Height:                480,
			FrameRate:             30,
			BitRateKbps:           1500,
			KeyFrameInterval:      60,
			Codec:                 "auto",
			PreferredCodec:        "auto",
			PreferHardware:        true,
			CPUUsed:               5,
			Deadline:              "realtime",
			RowMT:                 true,
			TileColumns:           2,
			Profile:               "main",
			Preset:                "fast",
			Tune:                  "zerolatency",
			Threads:               4,
			BufferSize:            100,
			MinQuantizer:          4,
			MaxQuantizer:          48,
			RealTime:              true,
			LowLatency:            true,
			AdaptiveBitRate:       true,
			MinBitRate:            500,
			MaxBitRate:            5000,
			AllowSoftwareFallback: true,
		},
		WebSocket: WebSocketConfig{
			Enabled:         true,
			ListenAddr:      ":8080",
			Path:            "/ws",
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			PingInterval:    30 * time.Second,
			PongTimeout:     60 * time.Second,
			MaxMessageSize:  512000,
		},
		EmailMethod: "disabled",
		MailSendConfig: MailSendConfig{
			FromName:   "WebCam Security",
			Debug:      false,
			MaxRetries: 3,
			Timeout:    30 * time.Second,
			RateLimit:  10,
		},
		GmailOAuth2Config: GmailOAuth2Config{
			FromName:       "WebCam Security",
			RedirectURL:    "http://localhost:8080/oauth2callback",
			TokenStorePath: "~/.webcam/gmail_token.json",
			Debug:          false,
			Scopes:         []string{"https://www.googleapis.com/auth/gmail.send"},
		},
		WebRTC: WebRTCConfig{
			ICEServers: []ICEServer{
				{URLs: []string{"stun:stun.l.google.com:19302"}},
			},
			ICETransportPolicy:   "all",
			BundlePolicy:         "max-bundle",
			RTCPMuxPolicy:        "require",
			ICECandidatePoolSize: 3,
			ConnectionTimeout:    30 * time.Second,
			DisconnectedTimeout:  5 * time.Second,
			FailedTimeout:        5 * time.Second,
			KeepaliveInterval:    10 * time.Second,
			Username:             "defaultuser",           // Added: must be 3-64 chars, alphanumeric
			Password:             "defaultpassword123",    // Added: must be 8-128 chars
		},
		Tailscale: TailscaleConfig{
			Enabled:   false,
			StateDir:  "/var/lib/tailscale",
			ShieldsUp: false,
		},
		Recording: RecordingConfig{
			Enabled:           true,
			ContinuousEnabled: true,
			EventEnabled:      true,
			OutputDir:         "/tmp/recordings",           // Changed: use /tmp for local testing
			FileFormat:        "mp4",
			SegmentDuration:   5 * time.Minute,
			MaxSegmentSize:    100,
			RingBufferSize:    60 * time.Second,           // Added: must be positive
			PreMotionBuffer:   10 * time.Second,
			PostMotionBuffer:  30 * time.Second,
			RetentionDays:     30,
			MaxStorageGB:      100,
			TempDir:           "/tmp/recorder",
			SpoolDir:          "/tmp/recorder/spool",       // Changed: use /tmp for local testing
		},
		Storage: StorageConfig{
			Type: "local",
			Local: LocalStorage{
				BasePath:         "/tmp/recorder/storage",  // Changed: use /tmp for local testing
				MaxSize:          100,
				UseDateHierarchy: true,
				Permissions:      "0755",
			},
			MinIO: MinIOConfig{
				Endpoint:        "localhost:9000",
				AccessKeyID:     "minioadmin",
				SecretAccessKey: "minioadmin",
				UseSSL:          false,
				Bucket:          "recordings",
				Region:          "us-east-1",
				MaxUploads:      10,
				MaxDownloads:    20,
				ConnectTimeout:  30 * time.Second,
				RequestTimeout:  5 * time.Minute,
				PartSize:        64,
				Concurrency:     4,
			},
			Postgres: PostgresConfig{
				Host:            "localhost",
				Port:            5432,
				Database:        "recordings",
				Username:        "recorder",
				Password:        "",
				SSLMode:         "disable",
				MaxConnections:  25,
				MaxIdleConns:    5,
				ConnMaxLifetime: 5 * time.Minute,
				ConnMaxIdleTime: 5 * time.Minute,
			},
			UploadConcurrency: 3,
			UploadRetries:     3,
			UploadTimeout:     5 * time.Minute,
		},
		Motion: MotionConfig{
			Enabled:              true,
			Sensitivity:          0.3,
			MinConsecutiveFrames: 6,
			MaxConsecutiveFrames: 30,         // Added: must be >= MinConsecutiveFrames
			NoMotionDelay:        2 * time.Second,
			MinConfidence:        0.5,
			MinArea:              100,
			MaxArea:              0,
			MinAspectRatio:       0.5,
			MaxAspectRatio:       2.0,
			BlurSize:             21,
			DilationSize:         5,           // Added: must be positive
			MorphologySize:       5,
			Threshold:            25,          // Added: must be 0-255
			LearningRate:         0.001,       // Added: must be 0-1
			CooldownPeriod:       5 * time.Second,
		},
		Pipeline: PipelineConfig{
			FrameQueueSize:        30,
			EncoderWorkers:        2,
			UploaderWorkers:       3,
			MotionPriority:        10,
			ContinuousPriority:    5,
			EnableBackpressure:    true,
			BackpressureThreshold: 0.8,
		},
		Buffer: BufferConfig{
			RingBufferFrames: 1800,
			FramePoolSize:    100,
			ImagePoolSize:    50,
			MaxMemoryMB:      500,
			GCInterval:       time.Minute,
		},
		API: APIConfig{
			Enabled:          true,
			ListenAddr:       ":8080",
			CORSEnabled:      true,
			RateLimitEnabled: true,
			RateLimitRPS:     100,
			ReadTimeout:      30 * time.Second,
			WriteTimeout:     30 * time.Second,
			IdleTimeout:      120 * time.Second,
		},
		Metrics: MetricsConfig{
			Enabled:           true,
			ListenAddr:        ":9090",
			Path:              "/metrics",
			PrometheusEnabled: true,
			CollectInterval:   10 * time.Second,
			DetailedMetrics:   false,
		},
		StatsCollectionInterval: 10 * time.Second,  // Added: must be >= 1s and <= 5m
		Log: LogConfig{
			Level:            "info",
			Format:           "json",
			OutputPath:       "stdout",
			ErrorOutputPath:  "stderr",
			RotateEnabled:    true,
			RotateMaxSize:    100,
			RotateMaxAge:     7,
			RotateMaxBackups: 3,
		},
	}

	// Set up aliases for backward compatibility
	cfg.VideoSettings = cfg.Video
	cfg.AudioSettings = cfg.Audio
	cfg.EncoderSettings = cfg.Encoder
	cfg.TailscaleConfig = cfg.Tailscale

	return cfg
}

// LoadConfig loads configuration from file
func LoadConfig(path string) (*Config, error) {
	config := DefaultConfig()

	data, err := ioutil.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist, use defaults
			return config, nil
		}
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Try to parse as YAML first, then JSON
	if err := yaml.Unmarshal(data, config); err != nil {
		if err := json.Unmarshal(data, config); err != nil {
			return nil, fmt.Errorf("failed to parse config file: %w", err)
		}
	}

	// Set up aliases after loading
	config.VideoSettings = config.Video
	config.AudioSettings = config.Audio
	config.EncoderSettings = config.Encoder
	config.TailscaleConfig = config.Tailscale

	return config, nil
}

// SaveConfig saves configuration to file
func SaveConfig(config *Config, path string) error {
	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := ioutil.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}
