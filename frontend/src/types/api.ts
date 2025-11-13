// API types matching the Go backend

export interface ConfigResponse {
  recording: RecordingSettings;
  video: VideoSettings;
  motion: MotionSettings;
  storage: StorageSettings;
  tailscale: TailscaleSettings;
  email: EmailSettings;
  webrtc: WebRTCSettings;
}

export interface RecordingSettings {
  continuousEnabled: boolean;
  eventEnabled: boolean;
  saveDirectory: string;
  segmentDuration: number; // minutes
  preMotionBuffer: number; // seconds
  postMotionBuffer: number; // seconds
  retentionDays: number;
}

export interface VideoSettings {
  width: number;
  height: number;
  framerate: number;
  bitRate: number; // Match Go backend capitalization
}

export interface MotionSettings {
  enabled: boolean;
  threshold: number;
  minimumArea: number;
  cooldownPeriod: number; // seconds
  noMotionDelay: number; // seconds
  minConsecutiveFrames: number;
  frameSkip: number;
}

export interface StorageSettings {
  minio: MinIOSettings;
  postgres: PostgresSettings;
}

export interface MinIOSettings {
  endpoint: string;
  accessKeyId: string;
  secretAccessKey: string;
  bucket: string;
  useSSL: boolean;
}

export interface PostgresSettings {
  host: string;
  port: number;
  database: string;
  username: string;
  password: string;
  sslMode: string;
}

export interface TailscaleSettings {
  enabled: boolean;
  nodeName: string;
  authKey?: string;
  hostname: string;
  listenPort: number;
}

export interface EmailSettings {
  method: 'mailersend' | 'gmail' | 'disabled';
  mailsendApiToken?: string;
  toEmail: string;
  fromEmail: string;
  gmailClientId?: string;
  gmailClientSecret?: string;
}

export interface WebRTCSettings {
  username: string;
  password?: string;
}

// Calibration types
export interface CalibrationStatus {
  state: 'idle' | 'recording' | 'processing' | 'complete' | 'error';
  progress: number; // 0-100
  message: string;
  calibrated: boolean;
  videoPath?: string;
  error?: string;
}

export interface CalibrationResult {
  baseline: number;
  threshold: number;
  samples: number;
  mean: number;
  stdDev: number;
}

// API response wrapper
export interface ApiResponse<T = any> {
  success: boolean;
  message?: string;
  data?: T;
  config?: ConfigResponse;
}

// Error response
export interface ApiError {
  message: string;
  status: number;
}
