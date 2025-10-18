// config/config.go
package config

import (
	"time"
)

// Config represents the complete configuration for the recording service
type Config struct {
	Service   ServiceConfig   `yaml:"service" json:"service"`
	Recording RecordingConfig `yaml:"recording" json:"recording"`
	Video     VideoConfig     `yaml:"video" json:"video"`
	Audio     AudioConfig     `yaml:"audio" json:"audio"`
	Encoder   EncoderConfig   `yaml:"encoder" json:"encoder"`
	Storage   StorageConfig   `yaml:"storage" json:"storage"`
	Motion    MotionConfig    `yaml:"motion" json:"motion"`
	Pipeline  PipelineConfig  `yaml:"pipeline" json:"pipeline"`
	Buffer    BufferConfig    `yaml:"buffer" json:"buffer"`
	API       APIConfig       `yaml:"api" json:"api"`
	Metrics   MetricsConfig   `yaml:"metrics" json:"metrics"`
	Log       LogConfig       `yaml:"log" json:"log"`
}

// ServiceConfig contains service-level configuration
type ServiceConfig struct {
	Name        string `yaml:"name" json:"name" envconfig:"SERVICE_NAME" default:"recorder"`
	Environment string `yaml:"environment" json:"environment" envconfig:"ENVIRONMENT" default:"development"`
	InstanceID  string `yaml:"instance_id" json:"instance_id" envconfig:"INSTANCE_ID"`

	// Graceful shutdown
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout" json:"shutdown_timeout" envconfig:"SHUTDOWN_TIMEOUT" default:"30s"`

	// Health check
	HealthCheckInterval time.Duration `yaml:"health_check_interval" json:"health_check_interval" envconfig:"HEALTH_CHECK_INTERVAL" default:"30s"`

	// Resource limits
	MaxCPU    int `yaml:"max_cpu" json:"max_cpu" envconfig:"MAX_CPU" default:"0"` // 0 = no limit
	MaxMemory int `yaml:"max_memory_mb" json:"max_memory_mb" envconfig:"MAX_MEMORY_MB" default:"0"`
}

// RecordingConfig contains recording-specific settings
type RecordingConfig struct {
	// Recording modes
	ContinuousEnabled bool `yaml:"continuous_enabled" json:"continuous_enabled" envconfig:"CONTINUOUS_ENABLED" default:"true"`
	EventEnabled      bool `yaml:"event_enabled" json:"event_enabled" envconfig:"EVENT_ENABLED" default:"true"`

	// Segment configuration
	SegmentDuration time.Duration `yaml:"segment_duration" json:"segment_duration" envconfig:"SEGMENT_DURATION" default:"5m"`
	MaxSegmentSize  int64         `yaml:"max_segment_size_mb" json:"max_segment_size_mb" envconfig:"MAX_SEGMENT_SIZE_MB" default:"100"`

	// Buffer configuration
	RingBufferSize   time.Duration `yaml:"ring_buffer_size" json:"ring_buffer_size" envconfig:"RING_BUFFER_SIZE" default:"60s"`
	PreMotionBuffer  time.Duration `yaml:"pre_motion_buffer" json:"pre_motion_buffer" envconfig:"PRE_MOTION_BUFFER" default:"10s"`
	PostMotionBuffer time.Duration `yaml:"post_motion_buffer" json:"post_motion_buffer" envconfig:"POST_MOTION_BUFFER" default:"30s"`

	// Event recording limits
	MinEventDuration time.Duration `yaml:"min_event_duration" json:"min_event_duration" envconfig:"MIN_EVENT_DURATION" default:"3s"`
	MaxEventDuration time.Duration `yaml:"max_event_duration" json:"max_event_duration" envconfig:"MAX_EVENT_DURATION" default:"5m"`

	// Retention
	RetentionPeriod time.Duration `yaml:"retention_period" json:"retention_period" envconfig:"RETENTION_PERIOD" default:"168h"` // 7 days

	// Storage paths
	TempDir  string `yaml:"temp_dir" json:"temp_dir" envconfig:"TEMP_DIR" default:"/tmp/recorder"`
	SpoolDir string `yaml:"spool_dir" json:"spool_dir" envconfig:"SPOOL_DIR" default:"/var/spool/recorder"`
}

// VideoConfig contains video-specific settings
type VideoConfig struct {
	Width     int     `yaml:"width" json:"width" envconfig:"VIDEO_WIDTH" default:"1920"`
	Height    int     `yaml:"height" json:"height" envconfig:"VIDEO_HEIGHT" default:"1080"`
	FrameRate float64 `yaml:"frame_rate" json:"frame_rate" envconfig:"VIDEO_FRAME_RATE" default:"30"`

	// Quality settings
	Quality string `yaml:"quality" json:"quality" envconfig:"VIDEO_QUALITY" default:"high"` // low, medium, high, ultra

	// Input settings
	InputFormat string `yaml:"input_format" json:"input_format" envconfig:"VIDEO_INPUT_FORMAT" default:"RGBA"`
	ColorSpace  string `yaml:"color_space" json:"color_space" envconfig:"VIDEO_COLOR_SPACE" default:"sRGB"`
	PixelFormat string `yaml:"pixel_format" json:"pixel_format" envconfig:"VIDEO_PIXEL_FORMAT" default:"yuv420p"`
}

// AudioConfig contains audio-specific settings
type AudioConfig struct {
	Enabled    bool   `yaml:"enabled" json:"enabled" envconfig:"AUDIO_ENABLED" default:"true"`
	SampleRate int    `yaml:"sample_rate" json:"sample_rate" envconfig:"AUDIO_SAMPLE_RATE" default:"48000"`
	Channels   int    `yaml:"channels" json:"channels" envconfig:"AUDIO_CHANNELS" default:"2"`
	BitDepth   int    `yaml:"bit_depth" json:"bit_depth" envconfig:"AUDIO_BIT_DEPTH" default:"16"`
	Codec      string `yaml:"codec" json:"codec" envconfig:"AUDIO_CODEC" default:"opus"`
	Bitrate    int    `yaml:"bitrate" json:"bitrate" envconfig:"AUDIO_BITRATE" default:"128000"`
}

// EncoderConfig contains encoder-specific settings
type EncoderConfig struct {
	// Codec selection
	Codec   string `yaml:"codec" json:"codec" envconfig:"ENCODER_CODEC" default:"h264"`
	Profile string `yaml:"profile" json:"profile" envconfig:"ENCODER_PROFILE" default:"main"`
	Preset  string `yaml:"preset" json:"preset" envconfig:"ENCODER_PRESET" default:"balanced"`

	// Bitrate control
	Bitrate    int `yaml:"bitrate" json:"bitrate" envconfig:"ENCODER_BITRATE" default:"4000000"`
	MaxBitrate int `yaml:"max_bitrate" json:"max_bitrate" envconfig:"ENCODER_MAX_BITRATE" default:"6000000"`
	BufferSize int `yaml:"buffer_size" json:"buffer_size" envconfig:"ENCODER_BUFFER_SIZE" default:"8000000"`

	// GOP settings
	KeyframeInterval int `yaml:"keyframe_interval" json:"keyframe_interval" envconfig:"KEYFRAME_INTERVAL" default:"60"`
	MaxBFrames       int `yaml:"max_b_frames" json:"max_b_frames" envconfig:"MAX_B_FRAMES" default:"2"`

	// Hardware acceleration
	HardwareAccel  bool   `yaml:"hardware_accel" json:"hardware_accel" envconfig:"HARDWARE_ACCEL" default:"true"`
	HardwareDevice string `yaml:"hardware_device" json:"hardware_device" envconfig:"HARDWARE_DEVICE" default:"auto"`

	// Threading
	Threads int `yaml:"threads" json:"threads" envconfig:"ENCODER_THREADS" default:"0"` // 0 = auto
}

// StorageConfig contains storage configuration
type StorageConfig struct {
	Type     string         `yaml:"type" json:"type" envconfig:"STORAGE_TYPE" default:"minio"` // minio, s3, local
	MinIO    MinIOConfig    `yaml:"minio" json:"minio"`
	Postgres PostgresConfig `yaml:"postgres" json:"postgres"`
	Local    LocalConfig    `yaml:"local" json:"local"`

	// Upload settings
	UploadConcurrency int           `yaml:"upload_concurrency" json:"upload_concurrency" envconfig:"UPLOAD_CONCURRENCY" default:"3"`
	UploadRetries     int           `yaml:"upload_retries" json:"upload_retries" envconfig:"UPLOAD_RETRIES" default:"3"`
	UploadTimeout     time.Duration `yaml:"upload_timeout" json:"upload_timeout" envconfig:"UPLOAD_TIMEOUT" default:"5m"`

	// Cleanup
	CleanupInterval  time.Duration `yaml:"cleanup_interval" json:"cleanup_interval" envconfig:"CLEANUP_INTERVAL" default:"1h"`
	CleanupBatchSize int           `yaml:"cleanup_batch_size" json:"cleanup_batch_size" envconfig:"CLEANUP_BATCH_SIZE" default:"100"`
}

// MinIOConfig contains MinIO-specific configuration
type MinIOConfig struct {
	Endpoint        string `yaml:"endpoint" json:"endpoint" envconfig:"MINIO_ENDPOINT" default:"localhost:9000"`
	AccessKeyID     string `yaml:"access_key_id" json:"access_key_id" envconfig:"MINIO_ACCESS_KEY_ID"`
	SecretAccessKey string `yaml:"secret_access_key" json:"secret_access_key" envconfig:"MINIO_SECRET_ACCESS_KEY"`
	UseSSL          bool   `yaml:"use_ssl" json:"use_ssl" envconfig:"MINIO_USE_SSL" default:"false"`
	Bucket          string `yaml:"bucket" json:"bucket" envconfig:"MINIO_BUCKET" default:"recordings"`
	Region          string `yaml:"region" json:"region" envconfig:"MINIO_REGION" default:"us-east-1"`

	// Connection settings
	MaxUploads     int           `yaml:"max_uploads" json:"max_uploads" envconfig:"MINIO_MAX_UPLOADS" default:"10"`
	MaxDownloads   int           `yaml:"max_downloads" json:"max_downloads" envconfig:"MINIO_MAX_DOWNLOADS" default:"20"`
	ConnectTimeout time.Duration `yaml:"connect_timeout" json:"connect_timeout" envconfig:"MINIO_CONNECT_TIMEOUT" default:"30s"`
	RequestTimeout time.Duration `yaml:"request_timeout" json:"request_timeout" envconfig:"MINIO_REQUEST_TIMEOUT" default:"5m"`

	// Multipart settings
	PartSize    int64 `yaml:"part_size_mb" json:"part_size_mb" envconfig:"MINIO_PART_SIZE_MB" default:"64"`
	Concurrency int   `yaml:"concurrency" json:"concurrency" envconfig:"MINIO_CONCURRENCY" default:"4"`
}

// PostgresConfig contains PostgreSQL configuration
type PostgresConfig struct {
	Host     string `yaml:"host" json:"host" envconfig:"POSTGRES_HOST" default:"localhost"`
	Port     int    `yaml:"port" json:"port" envconfig:"POSTGRES_PORT" default:"5432"`
	Database string `yaml:"database" json:"database" envconfig:"POSTGRES_DATABASE" default:"recorder"`
	Username string `yaml:"username" json:"username" envconfig:"POSTGRES_USERNAME"`
	Password string `yaml:"password" json:"password" envconfig:"POSTGRES_PASSWORD"`
	SSLMode  string `yaml:"ssl_mode" json:"ssl_mode" envconfig:"POSTGRES_SSL_MODE" default:"require"`

	// Connection pool
	MaxConnections  int           `yaml:"max_connections" json:"max_connections" envconfig:"POSTGRES_MAX_CONNECTIONS" default:"25"`
	MaxIdleConns    int           `yaml:"max_idle_conns" json:"max_idle_conns" envconfig:"POSTGRES_MAX_IDLE_CONNS" default:"5"`
	ConnMaxLifetime time.Duration `yaml:"conn_max_lifetime" json:"conn_max_lifetime" envconfig:"POSTGRES_CONN_MAX_LIFETIME" default:"5m"`
	ConnMaxIdleTime time.Duration `yaml:"conn_max_idle_time" json:"conn_max_idle_time" envconfig:"POSTGRES_CONN_MAX_IDLE_TIME" default:"1m"`

	// Schema
	AutoMigrate bool   `yaml:"auto_migrate" json:"auto_migrate" envconfig:"POSTGRES_AUTO_MIGRATE" default:"true"`
	SchemaName  string `yaml:"schema_name" json:"schema_name" envconfig:"POSTGRES_SCHEMA_NAME" default:"public"`
}

// LocalConfig contains local storage configuration
type LocalConfig struct {
	BasePath string `yaml:"base_path" json:"base_path" envconfig:"LOCAL_BASE_PATH" default:"/var/lib/recorder"`
	MaxSize  int64  `yaml:"max_size_gb" json:"max_size_gb" envconfig:"LOCAL_MAX_SIZE_GB" default:"100"`

	// Directory structure
	UseDateHierarchy bool   `yaml:"use_date_hierarchy" json:"use_date_hierarchy" envconfig:"LOCAL_USE_DATE_HIERARCHY" default:"true"`
	Permissions      string `yaml:"permissions" json:"permissions" envconfig:"LOCAL_PERMISSIONS" default:"0755"`
}

// MotionConfig contains motion detection configuration
type MotionConfig struct {
	Enabled       bool    `yaml:"enabled" json:"enabled" envconfig:"MOTION_ENABLED" default:"true"`
	Sensitivity   float64 `yaml:"sensitivity" json:"sensitivity" envconfig:"MOTION_SENSITIVITY" default:"0.3"`
	MinConfidence float64 `yaml:"min_confidence" json:"min_confidence" envconfig:"MOTION_MIN_CONFIDENCE" default:"0.5"`

	// Detection parameters
	MinArea        int     `yaml:"min_area" json:"min_area" envconfig:"MOTION_MIN_AREA" default:"100"`
	MaxArea        int     `yaml:"max_area" json:"max_area" envconfig:"MOTION_MAX_AREA" default:"0"` // 0 = no limit
	MinAspectRatio float64 `yaml:"min_aspect_ratio" json:"min_aspect_ratio" envconfig:"MOTION_MIN_ASPECT_RATIO" default:"0.5"`
	MaxAspectRatio float64 `yaml:"max_aspect_ratio" json:"max_aspect_ratio" envconfig:"MOTION_MAX_ASPECT_RATIO" default:"2.0"`

	// Noise reduction
	BlurSize       int `yaml:"blur_size" json:"blur_size" envconfig:"MOTION_BLUR_SIZE" default:"21"`
	MorphologySize int `yaml:"morphology_size" json:"morphology_size" envconfig:"MOTION_MORPHOLOGY_SIZE" default:"5"`

	// Zones
	EnableZones bool   `yaml:"enable_zones" json:"enable_zones" envconfig:"MOTION_ENABLE_ZONES" default:"false"`
	ZonesConfig string `yaml:"zones_config" json:"zones_config" envconfig:"MOTION_ZONES_CONFIG"`

	// Cooldown
	CooldownPeriod time.Duration `yaml:"cooldown_period" json:"cooldown_period" envconfig:"MOTION_COOLDOWN_PERIOD" default:"5s"`
}

// PipelineConfig contains pipeline configuration
type PipelineConfig struct {
	// Frame processing
	FrameQueueSize int `yaml:"frame_queue_size" json:"frame_queue_size" envconfig:"FRAME_QUEUE_SIZE" default:"30"`

	// Worker pools
	EncoderWorkers  int `yaml:"encoder_workers" json:"encoder_workers" envconfig:"ENCODER_WORKERS" default:"2"`
	UploaderWorkers int `yaml:"uploader_workers" json:"uploader_workers" envconfig:"UPLOADER_WORKERS" default:"3"`

	// Priorities
	MotionPriority     int `yaml:"motion_priority" json:"motion_priority" envconfig:"MOTION_PRIORITY" default:"10"`
	ContinuousPriority int `yaml:"continuous_priority" json:"continuous_priority" envconfig:"CONTINUOUS_PRIORITY" default:"5"`

	// Backpressure
	EnableBackpressure    bool    `yaml:"enable_backpressure" json:"enable_backpressure" envconfig:"ENABLE_BACKPRESSURE" default:"true"`
	BackpressureThreshold float64 `yaml:"backpressure_threshold" json:"backpressure_threshold" envconfig:"BACKPRESSURE_THRESHOLD" default:"0.8"`
}

// BufferConfig contains buffer configuration
type BufferConfig struct {
	// Ring buffer
	RingBufferFrames int `yaml:"ring_buffer_frames" json:"ring_buffer_frames" envconfig:"RING_BUFFER_FRAMES" default:"1800"` // 60s @ 30fps

	// Frame pool
	FramePoolSize int `yaml:"frame_pool_size" json:"frame_pool_size" envconfig:"FRAME_POOL_SIZE" default:"100"`
	ImagePoolSize int `yaml:"image_pool_size" json:"image_pool_size" envconfig:"IMAGE_POOL_SIZE" default:"50"`

	// WAL (Write-Ahead Log)
	EnableWAL         bool          `yaml:"enable_wal" json:"enable_wal" envconfig:"ENABLE_WAL" default:"true"`
	WALDirectory      string        `yaml:"wal_directory" json:"wal_directory" envconfig:"WAL_DIRECTORY" default:"/var/lib/recorder/wal"`
	WALMaxSize        int64         `yaml:"wal_max_size_mb" json:"wal_max_size_mb" envconfig:"WAL_MAX_SIZE_MB" default:"100"`
	WALRotateInterval time.Duration `yaml:"wal_rotate_interval" json:"wal_rotate_interval" envconfig:"WAL_ROTATE_INTERVAL" default:"1h"`
	WALRetention      time.Duration `yaml:"wal_retention" json:"wal_retention" envconfig:"WAL_RETENTION" default:"24h"`

	// Memory management
	MaxMemoryMB int           `yaml:"max_memory_mb" json:"max_memory_mb" envconfig:"BUFFER_MAX_MEMORY_MB" default:"500"`
	GCInterval  time.Duration `yaml:"gc_interval" json:"gc_interval" envconfig:"BUFFER_GC_INTERVAL" default:"1m"`
}

// APIConfig contains API server configuration
type APIConfig struct {
	Enabled    bool   `yaml:"enabled" json:"enabled" envconfig:"API_ENABLED" default:"true"`
	ListenAddr string `yaml:"listen_addr" json:"listen_addr" envconfig:"API_LISTEN_ADDR" default:":8080"`

	// TLS
	TLSEnabled  bool   `yaml:"tls_enabled" json:"tls_enabled" envconfig:"API_TLS_ENABLED" default:"false"`
	TLSCertFile string `yaml:"tls_cert_file" json:"tls_cert_file" envconfig:"API_TLS_CERT_FILE"`
	TLSKeyFile  string `yaml:"tls_key_file" json:"tls_key_file" envconfig:"API_TLS_KEY_FILE"`

	// CORS
	CORSEnabled bool     `yaml:"cors_enabled" json:"cors_enabled" envconfig:"API_CORS_ENABLED" default:"true"`
	CORSOrigins []string `yaml:"cors_origins" json:"cors_origins" envconfig:"API_CORS_ORIGINS"`

	// Rate limiting
	RateLimitEnabled bool `yaml:"rate_limit_enabled" json:"rate_limit_enabled" envconfig:"API_RATE_LIMIT_ENABLED" default:"true"`
	RateLimitRPS     int  `yaml:"rate_limit_rps" json:"rate_limit_rps" envconfig:"API_RATE_LIMIT_RPS" default:"100"`

	// Authentication
	AuthEnabled bool   `yaml:"auth_enabled" json:"auth_enabled" envconfig:"API_AUTH_ENABLED" default:"false"`
	AuthType    string `yaml:"auth_type" json:"auth_type" envconfig:"API_AUTH_TYPE" default:"bearer"` // bearer, basic, jwt
	AuthSecret  string `yaml:"auth_secret" json:"auth_secret" envconfig:"API_AUTH_SECRET"`

	// Timeouts
	ReadTimeout  time.Duration `yaml:"read_timeout" json:"read_timeout" envconfig:"API_READ_TIMEOUT" default:"30s"`
	WriteTimeout time.Duration `yaml:"write_timeout" json:"write_timeout" envconfig:"API_WRITE_TIMEOUT" default:"30s"`
	IdleTimeout  time.Duration `yaml:"idle_timeout" json:"idle_timeout" envconfig:"API_IDLE_TIMEOUT" default:"120s"`
}

// MetricsConfig contains metrics configuration
type MetricsConfig struct {
	Enabled    bool   `yaml:"enabled" json:"enabled" envconfig:"METRICS_ENABLED" default:"true"`
	ListenAddr string `yaml:"listen_addr" json:"listen_addr" envconfig:"METRICS_LISTEN_ADDR" default:":9090"`
	Path       string `yaml:"path" json:"path" envconfig:"METRICS_PATH" default:"/metrics"`

	// Prometheus
	PrometheusEnabled bool `yaml:"prometheus_enabled" json:"prometheus_enabled" envconfig:"PROMETHEUS_ENABLED" default:"true"`

	// StatsD
	StatsDEnabled bool   `yaml:"statsd_enabled" json:"statsd_enabled" envconfig:"STATSD_ENABLED" default:"false"`
	StatsDAddr    string `yaml:"statsd_addr" json:"statsd_addr" envconfig:"STATSD_ADDR"`
	StatsDPrefix  string `yaml:"statsd_prefix" json:"statsd_prefix" envconfig:"STATSD_PREFIX" default:"recorder"`

	// Collection intervals
	CollectInterval time.Duration `yaml:"collect_interval" json:"collect_interval" envconfig:"METRICS_COLLECT_INTERVAL" default:"10s"`

	// Detailed metrics
	DetailedMetrics bool `yaml:"detailed_metrics" json:"detailed_metrics" envconfig:"DETAILED_METRICS" default:"false"`
}

// LogConfig contains logging configuration
type LogConfig struct {
	Level            string   `yaml:"level" json:"level" envconfig:"LOG_LEVEL" default:"info"`
	Format           string   `yaml:"format" json:"format" envconfig:"LOG_FORMAT" default:"json"` // json, console
	OutputPaths      []string `yaml:"output_paths" json:"output_paths" envconfig:"LOG_OUTPUT_PATHS"`
	ErrorOutputPaths []string `yaml:"error_output_paths" json:"error_output_paths" envconfig:"LOG_ERROR_OUTPUT_PATHS"`

	// Sampling
	EnableSampling  bool `yaml:"enable_sampling" json:"enable_sampling" envconfig:"LOG_ENABLE_SAMPLING" default:"false"`
	SamplingInitial int  `yaml:"sampling_initial" json:"sampling_initial" envconfig:"LOG_SAMPLING_INITIAL" default:"100"`
	SamplingAfter   int  `yaml:"sampling_after" json:"sampling_after" envconfig:"LOG_SAMPLING_AFTER" default:"100"`

	// File rotation (if logging to file)
	RotateEnabled    bool `yaml:"rotate_enabled" json:"rotate_enabled" envconfig:"LOG_ROTATE_ENABLED" default:"true"`
	RotateMaxSize    int  `yaml:"rotate_max_size_mb" json:"rotate_max_size_mb" envconfig:"LOG_ROTATE_MAX_SIZE_MB" default:"100"`
	RotateMaxAge     int  `yaml:"rotate_max_age_days" json:"rotate_max_age_days" envconfig:"LOG_ROTATE_MAX_AGE_DAYS" default:"7"`
	RotateMaxBackups int  `yaml:"rotate_max_backups" json:"rotate_max_backups" envconfig:"LOG_ROTATE_MAX_BACKUPS" default:"3"`
}
