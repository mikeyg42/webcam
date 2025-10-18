// storage/manifest.go
package storage

import (
	"database/sql"
	"encoding/json"
	"time"
	"fmt"
)

// Recording represents a recording with all its metadata
type Recording struct {
	// Core identifiers
	ID         string `json:"id" db:"external_id"`
	Type       string `json:"type" db:"type"` // "continuous" or "event"
	Status     string `json:"status" db:"status"` // "recording", "processing", "completed", "failed"
	
	// Timestamps
	StartedAt time.Time     `json:"started_at" db:"started_at"`
	EndedAt   sql.NullTime `json:"ended_at,omitempty" db:"ended_at"`
	Duration  float64       `json:"duration_seconds,omitempty" db:"duration_seconds"`
	
	// Storage information
	Bucket       string `json:"bucket" db:"bucket"`
	BaseKey      string `json:"base_key" db:"base_key"`
	SegmentCount int    `json:"segment_count" db:"segment_count"`
	TotalSize    int64  `json:"total_size_bytes" db:"total_size_bytes"`
	
	// Video properties
	Resolution sql.NullString `json:"resolution,omitempty" db:"resolution"`
	FPS        sql.NullInt32  `json:"fps,omitempty" db:"fps"`
	Codec      sql.NullString `json:"codec,omitempty" db:"codec"`
	Bitrate    sql.NullInt32  `json:"bitrate,omitempty" db:"bitrate"`
	
	// Motion event specific
	MotionConfidence  sql.NullFloat64 `json:"motion_confidence,omitempty" db:"motion_confidence"`
	PreBufferSeconds  sql.NullInt32   `json:"pre_buffer_seconds,omitempty" db:"pre_buffer_seconds"`
	PostBufferSeconds sql.NullInt32   `json:"post_buffer_seconds,omitempty" db:"post_buffer_seconds"`
	TriggerTime       sql.NullTime    `json:"trigger_time,omitempty" db:"trigger_time"`
	
	// Additional metadata
	Metadata map[string]interface{} `json:"metadata,omitempty"`
	Tags     []string               `json:"tags,omitempty"`
	
	// Audit fields
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
	
	// Runtime fields (not stored in DB)
	Segments      []*Segment      `json:"segments,omitempty" db:"-"`
	MotionEvents  []*MotionEvent  `json:"motion_events,omitempty" db:"-"`
	StreamURL     string          `json:"stream_url,omitempty" db:"-"`
	ThumbnailURL  string          `json:"thumbnail_url,omitempty" db:"-"`
}

// Segment represents a recording segment
type Segment struct {
	ID          string    `json:"id" db:"id"`
	RecordingID string    `json:"recording_id" db:"recording_id"`
	Index       int       `json:"index" db:"segment_index"`
	
	// Time information
	StartTime time.Time     `json:"start_time" db:"start_time"`
	EndTime   time.Time     `json:"end_time" db:"end_time"`
	Duration  time.Duration `json:"duration" db:"-"`
	
	// Storage information
	StorageKey string        `json:"storage_key" db:"storage_key"`
	Size       int64         `json:"size_bytes" db:"size_bytes"`
	FrameCount int64         `json:"frame_count" db:"frame_count"`
	Checksum   string        `json:"checksum,omitempty" db:"checksum"`
	
	// Status tracking
	Status         SegmentStatus  `json:"status" db:"status"`
	UploadAttempts int            `json:"upload_attempts,omitempty" db:"upload_attempts"`
	LastError      sql.NullString `json:"last_error,omitempty" db:"last_error"`
	
	// Timestamps
	CreatedAt  time.Time     `json:"created_at" db:"created_at"`
	UploadedAt sql.NullTime `json:"uploaded_at,omitempty" db:"uploaded_at"`
	
	// Runtime fields
	FilePath     string `json:"-" db:"-"`
	TempPath     string `json:"-" db:"-"`
	PresignedURL string `json:"url,omitempty" db:"-"`
}

// SegmentStatus represents the status of a segment
type SegmentStatus string

const (
	SegmentStatusRecording SegmentStatus = "recording"
	SegmentStatusUploading SegmentStatus = "uploading"
	SegmentStatusCompleted SegmentStatus = "completed"
	SegmentStatusVerified  SegmentStatus = "verified"
	SegmentStatusFailed    SegmentStatus = "failed"
)

// MotionEvent represents a motion detection event
type MotionEvent struct {
	ID          string    `json:"id" db:"id"`
	RecordingID string    `json:"recording_id" db:"recording_id"`
	
	// Event details
	EventTime         time.Time     `json:"event_time" db:"event_time"`
	Confidence        float64       `json:"confidence" db:"confidence"`
	Duration          time.Duration `json:"duration" db:"-"`
	PeakConfidence    float64       `json:"peak_confidence" db:"peak_confidence"`
	TotalMotionFrames int           `json:"total_motion_frames" db:"total_motion_frames"`
	
	// Motion regions
	Regions []MotionRegion `json:"regions,omitempty"`
	
	// Additional metadata
	Metadata map[string]interface{} `json:"metadata,omitempty"`
	
	// Timestamps
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

// MotionRegion represents a region where motion was detected
type MotionRegion struct {
	X          int       `json:"x"`
	Y          int       `json:"y"`
	Width      int       `json:"width"`
	Height     int       `json:"height"`
	Confidence float64   `json:"confidence"`
	Timestamp  time.Time `json:"timestamp"`
}

// RecordingQuery defines search criteria for recordings
type RecordingQuery struct {
	Type      string    `json:"type,omitempty"`
	Status    string    `json:"status,omitempty"`
	StartTime time.Time `json:"start_time,omitempty"`
	EndTime   time.Time `json:"end_time,omitempty"`
	Tags      []string  `json:"tags,omitempty"`
	
	// Pagination
	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`
	
	// Sorting
	OrderBy   string `json:"order_by,omitempty"`
	OrderDesc bool   `json:"order_desc,omitempty"`
	
	// Filters
	MinDuration      time.Duration `json:"min_duration,omitempty"`
	MaxDuration      time.Duration `json:"max_duration,omitempty"`
	MinMotionConfidence float64    `json:"min_motion_confidence,omitempty"`
	
	// Include related data
	IncludeSegments     bool `json:"include_segments,omitempty"`
	IncludeMotionEvents bool `json:"include_motion_events,omitempty"`
	IncludeURLs         bool `json:"include_urls,omitempty"`
}

// StorageStats represents storage statistics
type StorageStats struct {
	TotalRecordings int64                `json:"total_recordings"`
	TotalSize       int64                `json:"total_size_bytes"`
	TotalSegments   int64                `json:"total_segments"`
	AverageDuration time.Duration        `json:"average_duration"`
	ByType          map[string]TypeStats `json:"by_type"`
}

// TypeStats represents statistics for a recording type
type TypeStats struct {
	Count     int64 `json:"count"`
	TotalSize int64 `json:"total_size_bytes"`
}

// RecordingStats represents recording statistics for a time range
type RecordingStats struct {
	TimeRange               TimeRange          `json:"time_range"`
	TotalRecordings         int64              `json:"total_recordings"`
	TotalDuration           time.Duration      `json:"total_duration"`
	TotalSize               int64              `json:"total_size_bytes"`
	AverageMotionConfidence float64            `json:"average_motion_confidence"`
	RecordingsByHour        map[time.Time]int  `json:"recordings_by_hour"`
	TopMotionEvents         []*MotionEvent     `json:"top_motion_events,omitempty"`
}

// TimeRange represents a time range for queries
type TimeRange struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// RecordingManifest represents the complete manifest for a recording
type RecordingManifest struct {
	Recording     *Recording       `json:"recording"`
	Segments      []*Segment       `json:"segments"`
	MotionEvents  []*MotionEvent   `json:"motion_events,omitempty"`
	StreamInfo    *StreamInfo      `json:"stream_info,omitempty"`
}

// StreamInfo contains streaming information for a recording
type StreamInfo struct {
	Protocol      string            `json:"protocol"` // "hls", "dash", "webrtc"
	PlaylistURL   string            `json:"playlist_url,omitempty"`
	SegmentURLs   []string          `json:"segment_urls,omitempty"`
	Duration      time.Duration     `json:"duration"`
	Resolution    string            `json:"resolution"`
	Bitrate       int               `json:"bitrate"`
	Codecs        []string          `json:"codecs"`
	Subtitles     []SubtitleTrack   `json:"subtitles,omitempty"`
}

// SubtitleTrack represents a subtitle/metadata track
type SubtitleTrack struct {
	Language string `json:"language"`
	Label    string `json:"label"`
	URL      string `json:"url"`
	Default  bool   `json:"default"`
}

// MarshalJSON customizes JSON marshaling for Recording
func (r *Recording) MarshalJSON() ([]byte, error) {
	type Alias Recording
	
	// Convert SQL null types to regular values
	aux := struct {
		*Alias
		EndedAt           *time.Time  `json:"ended_at,omitempty"`
		Resolution        *string     `json:"resolution,omitempty"`
		FPS               *int32      `json:"fps,omitempty"`
		Codec             *string     `json:"codec,omitempty"`
		Bitrate           *int32      `json:"bitrate,omitempty"`
		MotionConfidence  *float64    `json:"motion_confidence,omitempty"`
		PreBufferSeconds  *int32      `json:"pre_buffer_seconds,omitempty"`
		PostBufferSeconds *int32      `json:"post_buffer_seconds,omitempty"`
		TriggerTime       *time.Time  `json:"trigger_time,omitempty"`
	}{
		Alias: (*Alias)(r),
	}
	
	if r.EndedAt.Valid {
		aux.EndedAt = &r.EndedAt.Time
	}
	if r.Resolution.Valid {
		aux.Resolution = &r.Resolution.String
	}
	if r.FPS.Valid {
		aux.FPS = &r.FPS.Int32
	}
	if r.Codec.Valid {
		aux.Codec = &r.Codec.String
	}
	if r.Bitrate.Valid {
		aux.Bitrate = &r.Bitrate.Int32
	}
	if r.MotionConfidence.Valid {
		aux.MotionConfidence = &r.MotionConfidence.Float64
	}
	if r.PreBufferSeconds.Valid {
		aux.PreBufferSeconds = &r.PreBufferSeconds.Int32
	}
	if r.PostBufferSeconds.Valid {
		aux.PostBufferSeconds = &r.PostBufferSeconds.Int32
	}
	if r.TriggerTime.Valid {
		aux.TriggerTime = &r.TriggerTime.Time
	}
	
	return json.Marshal(aux)
}

// IsComplete returns true if the recording is completed
func (r *Recording) IsComplete() bool {
	return r.Status == "completed"
}

// IsMotionEvent returns true if this is a motion-triggered recording
func (r *Recording) IsMotionEvent() bool {
	return r.Type == "event"
}

// GetDuration returns the recording duration as a time.Duration
func (r *Recording) GetDuration() time.Duration {
	return time.Duration(r.Duration * float64(time.Second))
}

// Validate checks if the recording data is valid
func (r *Recording) Validate() error {
	if r.ID == "" {
		return fmt.Errorf("recording ID is required")
	}
	if r.Type != "continuous" && r.Type != "event" {
		return fmt.Errorf("invalid recording type: %s", r.Type)
	}
	if r.StartedAt.IsZero() {
		return fmt.Errorf("start time is required")
	}
	return nil
}

// IsUploaded returns true if the segment has been uploaded
func (s *Segment) IsUploaded() bool {
	return s.Status == SegmentStatusCompleted || s.Status == SegmentStatusVerified
}

// NeedsRetry returns true if the segment upload should be retried
func (s *Segment) NeedsRetry() bool {
	return s.Status == SegmentStatusFailed && s.UploadAttempts < 3
}

// GetError returns the last error if any
func (s *Segment) GetError() string {
	if s.LastError.Valid {
		return s.LastError.String
	}
	return ""
}