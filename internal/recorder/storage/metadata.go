// storage/metadata.go
package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	_ "github.com/lib/pq" // PostgreSQL driver
	
	"github.com/mikeyg42/webcam/internal/recorder/recorderlog"
)

// MetadataStore defines the interface for recording metadata operations
type MetadataStore interface {
	// Recording operations
	SaveRecording(ctx context.Context, recording *Recording) error
	UpdateRecording(ctx context.Context, id string, updates map[string]interface{}) error
	GetRecording(ctx context.Context, id string) (*Recording, error)
	QueryRecordings(ctx context.Context, query RecordingQuery) ([]*Recording, error)
	DeleteRecording(ctx context.Context, id string) error
	
	// Segment operations
	SaveSegment(ctx context.Context, segment *Segment) error
	GetSegment(ctx context.Context, recordingID string, index int) (*Segment, error)
	GetSegments(ctx context.Context, recordingID string) ([]*Segment, error)
	UpdateSegmentStatus(ctx context.Context, segmentID string, status SegmentStatus) error
	DeleteOldSegments(ctx context.Context, olderThan time.Time) (int64, error)
	
	// Event operations
	SaveMotionEvent(ctx context.Context, event *MotionEvent) error
	GetMotionEvents(ctx context.Context, recordingID string) ([]*MotionEvent, error)
	
	// Analytics
	GetStorageStats(ctx context.Context) (*StorageStats, error)
	GetRecordingStats(ctx context.Context, timeRange TimeRange) (*RecordingStats, error)
	
	// Maintenance
	Vacuum(ctx context.Context) error
	HealthCheck(ctx context.Context) error
}

// PostgresStore implements MetadataStore using PostgreSQL
type PostgresStore struct {
	db     *sqlx.DB
	logger recorderlog.Logger
	config PostgresConfig
}

// PostgresConfig contains PostgreSQL configuration
type PostgresConfig struct {
	Host            string
	Port            int
	Database        string
	Username        string
	Password        string
	SSLMode         string // disable, require, verify-ca, verify-full
	MaxConnections  int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// NewPostgresStore creates a new PostgreSQL metadata store
func NewPostgresStore(config PostgresConfig) (*PostgresStore, error) {
	// Set defaults
	if config.Port == 0 {
		config.Port = 5432
	}
	if config.SSLMode == "" {
		config.SSLMode = "require"
	}
	if config.MaxConnections == 0 {
		config.MaxConnections = 25
	}
	if config.MaxIdleConns == 0 {
		config.MaxIdleConns = 5
	}
	if config.ConnMaxLifetime == 0 {
		config.ConnMaxLifetime = 5 * time.Minute
	}
	
	// Build connection string
	dsn := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		config.Host, config.Port, config.Username, config.Password, config.Database, config.SSLMode,
	)
	
	// Connect to database
	db, err := sqlx.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	
	// Configure connection pool
	db.SetMaxOpenConns(config.MaxConnections)
	db.SetMaxIdleConns(config.MaxIdleConns)
	db.SetConnMaxLifetime(config.ConnMaxLifetime)
	if config.ConnMaxIdleTime > 0 {
		db.SetConnMaxIdleTime(config.ConnMaxIdleTime)
	}
	
	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}
	
	store := &PostgresStore{
		db:     db,
		logger: recorderlog.L().Named("postgres-store"),
		config: config,
	}
	
	// Initialize schema
	if err := store.initSchema(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}
	
	return store, nil
}

// initSchema creates the database schema if it doesn't exist
func (s *PostgresStore) initSchema(ctx context.Context) error {
	schema := `
	-- Enable UUID extension
	CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
	
	-- Recordings table
	CREATE TABLE IF NOT EXISTS recordings (
		id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
		external_id VARCHAR(255) UNIQUE NOT NULL,
		type VARCHAR(20) NOT NULL CHECK (type IN ('continuous', 'event')),
		status VARCHAR(20) NOT NULL CHECK (status IN ('recording', 'processing', 'completed', 'failed')),
		
		started_at TIMESTAMPTZ NOT NULL,
		ended_at TIMESTAMPTZ,
		duration_seconds FLOAT,
		
		-- Storage references
		bucket VARCHAR(255) NOT NULL,
		base_key VARCHAR(500) NOT NULL,
		segment_count INTEGER DEFAULT 0,
		total_size_bytes BIGINT DEFAULT 0,
		
		-- Video metadata
		resolution VARCHAR(20),
		fps INTEGER,
		codec VARCHAR(20),
		bitrate INTEGER,
		
		-- Motion event specific
		motion_confidence FLOAT,
		pre_buffer_seconds INTEGER,
		post_buffer_seconds INTEGER,
		trigger_time TIMESTAMPTZ,
		
		-- Additional metadata as JSONB
		metadata JSONB DEFAULT '{}',
		tags TEXT[] DEFAULT '{}',
		
		-- Audit fields
		created_at TIMESTAMPTZ DEFAULT NOW(),
		updated_at TIMESTAMPTZ DEFAULT NOW()
	);
	
	-- Segments table
	CREATE TABLE IF NOT EXISTS segments (
		id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
		recording_id UUID REFERENCES recordings(id) ON DELETE CASCADE,
		segment_index INTEGER NOT NULL,
		
		start_time TIMESTAMPTZ NOT NULL,
		end_time TIMESTAMPTZ NOT NULL,
		duration_seconds FLOAT NOT NULL,
		
		storage_key VARCHAR(500) NOT NULL,
		size_bytes BIGINT NOT NULL,
		frame_count BIGINT DEFAULT 0,
		checksum VARCHAR(64),
		
		status VARCHAR(20) NOT NULL CHECK (status IN ('recording', 'uploading', 'completed', 'verified', 'failed')),
		upload_attempts INTEGER DEFAULT 0,
		last_error TEXT,
		
		created_at TIMESTAMPTZ DEFAULT NOW(),
		uploaded_at TIMESTAMPTZ,
		
		UNIQUE(recording_id, segment_index)
	);
	
	-- Motion events table
	CREATE TABLE IF NOT EXISTS motion_events (
		id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
		recording_id UUID REFERENCES recordings(id) ON DELETE CASCADE,
		
		event_time TIMESTAMPTZ NOT NULL,
		confidence FLOAT NOT NULL,
		duration_seconds FLOAT,
		
		regions JSONB, -- Array of detected regions
		peak_confidence FLOAT,
		total_motion_frames INTEGER,
		
		metadata JSONB DEFAULT '{}',
		
		created_at TIMESTAMPTZ DEFAULT NOW()
	);
	
	-- Indexes for performance
	CREATE INDEX IF NOT EXISTS idx_recordings_type_status ON recordings(type, status);
	CREATE INDEX IF NOT EXISTS idx_recordings_started_at ON recordings(started_at DESC);
	CREATE INDEX IF NOT EXISTS idx_recordings_external_id ON recordings(external_id);
	CREATE INDEX IF NOT EXISTS idx_recordings_metadata ON recordings USING GIN(metadata);
	CREATE INDEX IF NOT EXISTS idx_recordings_tags ON recordings USING GIN(tags);
	
	CREATE INDEX IF NOT EXISTS idx_segments_recording_id ON segments(recording_id);
	CREATE INDEX IF NOT EXISTS idx_segments_status ON segments(status);
	CREATE INDEX IF NOT EXISTS idx_segments_created_at ON segments(created_at);
	
	CREATE INDEX IF NOT EXISTS idx_motion_events_recording_id ON motion_events(recording_id);
	CREATE INDEX IF NOT EXISTS idx_motion_events_event_time ON motion_events(event_time DESC);
	
	-- Trigger for updated_at
	CREATE OR REPLACE FUNCTION update_updated_at_column()
	RETURNS TRIGGER AS $$
	BEGIN
		NEW.updated_at = NOW();
		RETURN NEW;
	END;
	$$ language 'plpgsql';
	
	DROP TRIGGER IF EXISTS update_recordings_updated_at ON recordings;
	CREATE TRIGGER update_recordings_updated_at BEFORE UPDATE ON recordings
		FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
	`
	
	_, err := s.db.ExecContext(ctx, schema)
	return err
}

// SaveRecording saves a new recording
func (s *PostgresStore) SaveRecording(ctx context.Context, recording *Recording) error {
	query := `
		INSERT INTO recordings (
			external_id, type, status, started_at, ended_at, duration_seconds,
			bucket, base_key, segment_count, total_size_bytes,
			resolution, fps, codec, bitrate,
			motion_confidence, pre_buffer_seconds, post_buffer_seconds, trigger_time,
			metadata, tags
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20
		)
		ON CONFLICT (external_id) DO UPDATE SET
			status = EXCLUDED.status,
			ended_at = EXCLUDED.ended_at,
			duration_seconds = EXCLUDED.duration_seconds,
			segment_count = EXCLUDED.segment_count,
			total_size_bytes = EXCLUDED.total_size_bytes,
			updated_at = NOW()
		RETURNING id, created_at, updated_at
	`
	
	metadataJSON, err := json.Marshal(recording.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}
	
	var id, createdAt, updatedAt sql.NullTime
	err = s.db.QueryRowContext(
		ctx, query,
		recording.ID, recording.Type, recording.Status,
		recording.StartedAt, recording.EndedAt, recording.Duration,
		recording.Bucket, recording.BaseKey, recording.SegmentCount, recording.TotalSize,
		recording.Resolution, recording.FPS, recording.Codec, recording.Bitrate,
		recording.MotionConfidence, recording.PreBufferSeconds, recording.PostBufferSeconds, recording.TriggerTime,
		metadataJSON, pq.Array(recording.Tags),
	).Scan(&id, &createdAt, &updatedAt)
	
	if err != nil {
		return fmt.Errorf("failed to save recording: %w", err)
	}
	
	s.logger.Info("Recording saved",
		recorderlog.String("id", recording.ID),
		recorderlog.String("type", recording.Type))
	
	return nil
}

// UpdateRecording updates recording fields
func (s *PostgresStore) UpdateRecording(ctx context.Context, id string, updates map[string]interface{}) error {
	if len(updates) == 0 {
		return nil
	}
	
	// Build dynamic UPDATE query
	setClauses := make([]string, 0, len(updates))
	args := make([]interface{}, 0, len(updates)+1)
	argIdx := 1
	
	for field, value := range updates {
		// Validate field names to prevent SQL injection
		if !isValidFieldName(field) {
			return fmt.Errorf("invalid field name: %s", field)
		}
		
		setClauses = append(setClauses, fmt.Sprintf("%s = $%d", field, argIdx))
		args = append(args, value)
		argIdx++
	}
	
	args = append(args, id)
	query := fmt.Sprintf(
		"UPDATE recordings SET %s, updated_at = NOW() WHERE external_id = $%d",
		strings.Join(setClauses, ", "),
		argIdx,
	)
	
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to update recording: %w", err)
	}
	
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	
	if rows == 0 {
		return fmt.Errorf("recording not found: %s", id)
	}
	
	return nil
}

// GetRecording retrieves a recording by ID
func (s *PostgresStore) GetRecording(ctx context.Context, id string) (*Recording, error) {
	query := `
		SELECT 
			external_id, type, status, started_at, ended_at, duration_seconds,
			bucket, base_key, segment_count, total_size_bytes,
			resolution, fps, codec, bitrate,
			motion_confidence, pre_buffer_seconds, post_buffer_seconds, trigger_time,
			metadata, tags, created_at, updated_at
		FROM recordings
		WHERE external_id = $1
	`
	
	var rec Recording
	var metadataJSON []byte
	var tags pq.StringArray
	
	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&rec.ID, &rec.Type, &rec.Status,
		&rec.StartedAt, &rec.EndedAt, &rec.Duration,
		&rec.Bucket, &rec.BaseKey, &rec.SegmentCount, &rec.TotalSize,
		&rec.Resolution, &rec.FPS, &rec.Codec, &rec.Bitrate,
		&rec.MotionConfidence, &rec.PreBufferSeconds, &rec.PostBufferSeconds, &rec.TriggerTime,
		&metadataJSON, &tags, &rec.CreatedAt, &rec.UpdatedAt,
	)
	
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("recording not found: %s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get recording: %w", err)
	}
	
	if len(metadataJSON) > 0 {
		if err := json.Unmarshal(metadataJSON, &rec.Metadata); err != nil {
			return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
		}
	}
	
	rec.Tags = []string(tags)
	
	return &rec, nil
}

// QueryRecordings queries recordings based on criteria
func (s *PostgresStore) QueryRecordings(ctx context.Context, query RecordingQuery) ([]*Recording, error) {
	// Build WHERE clauses
	whereClauses := []string{}
	args := []interface{}{}
	argIdx := 1
	
	if query.Type != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("type = $%d", argIdx))
		args = append(args, query.Type)
		argIdx++
	}
	
	if query.Status != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("status = $%d", argIdx))
		args = append(args, query.Status)
		argIdx++
	}
	
	if !query.StartTime.IsZero() {
		whereClauses = append(whereClauses, fmt.Sprintf("started_at >= $%d", argIdx))
		args = append(args, query.StartTime)
		argIdx++
	}
	
	if !query.EndTime.IsZero() {
		whereClauses = append(whereClauses, fmt.Sprintf("started_at <= $%d", argIdx))
		args = append(args, query.EndTime)
		argIdx++
	}
	
	if len(query.Tags) > 0 {
		whereClauses = append(whereClauses, fmt.Sprintf("tags && $%d", argIdx))
		args = append(args, pq.Array(query.Tags))
		argIdx++
	}
	
	// Build final query
	sqlQuery := `
		SELECT 
			external_id, type, status, started_at, ended_at, duration_seconds,
			bucket, base_key, segment_count, total_size_bytes,
			resolution, fps, codec, bitrate,
			motion_confidence, pre_buffer_seconds, post_buffer_seconds, trigger_time,
			metadata, tags, created_at, updated_at
		FROM recordings
	`
	
	if len(whereClauses) > 0 {
		sqlQuery += " WHERE " + strings.Join(whereClauses, " AND ")
	}
	
	// Add sorting
	if query.OrderBy != "" {
		if !isValidFieldName(query.OrderBy) {
			return nil, fmt.Errorf("invalid order by field: %s", query.OrderBy)
		}
		sqlQuery += fmt.Sprintf(" ORDER BY %s", query.OrderBy)
		if query.OrderDesc {
			sqlQuery += " DESC"
		}
	} else {
		sqlQuery += " ORDER BY started_at DESC"
	}
	
	// Add limit and offset
	if query.Limit > 0 {
		sqlQuery += fmt.Sprintf(" LIMIT %d", query.Limit)
	}
	if query.Offset > 0 {
		sqlQuery += fmt.Sprintf(" OFFSET %d", query.Offset)
	}
	
	rows, err := s.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query recordings: %w", err)
	}
	defer rows.Close()
	
	recordings := make([]*Recording, 0)
	for rows.Next() {
		var rec Recording
		var metadataJSON []byte
		var tags pq.StringArray
		
		err := rows.Scan(
			&rec.ID, &rec.Type, &rec.Status,
			&rec.StartedAt, &rec.EndedAt, &rec.Duration,
			&rec.Bucket, &rec.BaseKey, &rec.SegmentCount, &rec.TotalSize,
			&rec.Resolution, &rec.FPS, &rec.Codec, &rec.Bitrate,
			&rec.MotionConfidence, &rec.PreBufferSeconds, &rec.PostBufferSeconds, &rec.TriggerTime,
			&metadataJSON, &tags, &rec.CreatedAt, &rec.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		
		if len(metadataJSON) > 0 {
			if err := json.Unmarshal(metadataJSON, &rec.Metadata); err != nil {
				return nil, err
			}
		}
		
		rec.Tags = []string(tags)
		recordings = append(recordings, &rec)
	}
	
	return recordings, nil
}

// DeleteRecording deletes a recording and all its segments
func (s *PostgresStore) DeleteRecording(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM recordings WHERE external_id = $1", id)
	if err != nil {
		return fmt.Errorf("failed to delete recording: %w", err)
	}
	
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	
	if rows == 0 {
		return fmt.Errorf("recording not found: %s", id)
	}
	
	s.logger.Info("Recording deleted", recorderlog.String("id", id))
	return nil
}

// SaveSegment saves a segment
func (s *PostgresStore) SaveSegment(ctx context.Context, segment *Segment) error {
	// First get the internal recording ID
	var recordingID string
	err := s.db.QueryRowContext(ctx, 
		"SELECT id FROM recordings WHERE external_id = $1", 
		segment.RecordingID,
	).Scan(&recordingID)
	
	if err != nil {
		return fmt.Errorf("failed to find recording: %w", err)
	}
	
	query := `
		INSERT INTO segments (
			recording_id, segment_index, start_time, end_time, duration_seconds,
			storage_key, size_bytes, frame_count, checksum, status
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10
		)
		ON CONFLICT (recording_id, segment_index) DO UPDATE SET
			end_time = EXCLUDED.end_time,
			duration_seconds = EXCLUDED.duration_seconds,
			size_bytes = EXCLUDED.size_bytes,
			frame_count = EXCLUDED.frame_count,
			checksum = EXCLUDED.checksum,
			status = EXCLUDED.status,
			uploaded_at = CASE 
				WHEN EXCLUDED.status = 'completed' THEN NOW() 
				ELSE segments.uploaded_at 
			END
		RETURNING id
	`
	
	var segmentID string
	err = s.db.QueryRowContext(
		ctx, query,
		recordingID, segment.Index, segment.StartTime, segment.EndTime, segment.Duration.Seconds(),
		segment.StorageKey, segment.Size, segment.FrameCount, segment.Checksum, segment.Status,
	).Scan(&segmentID)
	
	if err != nil {
		return fmt.Errorf("failed to save segment: %w", err)
	}
	
	segment.ID = segmentID
	
	// Update recording stats
	_, err = s.db.ExecContext(ctx, `
		UPDATE recordings 
		SET 
			segment_count = segment_count + 1,
			total_size_bytes = total_size_bytes + $1
		WHERE external_id = $2
	`, segment.Size, segment.RecordingID)
	
	return err
}

// GetSegment retrieves a specific segment
func (s *PostgresStore) GetSegment(ctx context.Context, recordingID string, index int) (*Segment, error) {
	query := `
		SELECT 
			s.id, s.segment_index, s.start_time, s.end_time, s.duration_seconds,
			s.storage_key, s.size_bytes, s.frame_count, s.checksum, s.status,
			s.created_at, s.uploaded_at
		FROM segments s
		JOIN recordings r ON s.recording_id = r.id
		WHERE r.external_id = $1 AND s.segment_index = $2
	`
	
	var seg Segment
	var duration float64
	
	err := s.db.QueryRowContext(ctx, query, recordingID, index).Scan(
		&seg.ID, &seg.Index, &seg.StartTime, &seg.EndTime, &duration,
		&seg.StorageKey, &seg.Size, &seg.FrameCount, &seg.Checksum, &seg.Status,
		&seg.CreatedAt, &seg.UploadedAt,
	)
	
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("segment not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get segment: %w", err)
	}
	
	seg.RecordingID = recordingID
	seg.Duration = time.Duration(duration * float64(time.Second))
	
	return &seg, nil
}

// GetSegments retrieves all segments for a recording
func (s *PostgresStore) GetSegments(ctx context.Context, recordingID string) ([]*Segment, error) {
	query := `
		SELECT 
			s.id, s.segment_index, s.start_time, s.end_time, s.duration_seconds,
			s.storage_key, s.size_bytes, s.frame_count, s.checksum, s.status,
			s.created_at, s.uploaded_at
		FROM segments s
		JOIN recordings r ON s.recording_id = r.id
		WHERE r.external_id = $1
		ORDER BY s.segment_index ASC
	`
	
	rows, err := s.db.QueryContext(ctx, query, recordingID)
	if err != nil {
		return nil, fmt.Errorf("failed to get segments: %w", err)
	}
	defer rows.Close()
	
	segments := make([]*Segment, 0)
	for rows.Next() {
		var seg Segment
		var duration float64
		
		err := rows.Scan(
			&seg.ID, &seg.Index, &seg.StartTime, &seg.EndTime, &duration,
			&seg.StorageKey, &seg.Size, &seg.FrameCount, &seg.Checksum, &seg.Status,
			&seg.CreatedAt, &seg.UploadedAt,
		)
		if err != nil {
			return nil, err
		}
		
		seg.RecordingID = recordingID
		seg.Duration = time.Duration(duration * float64(time.Second))
		segments = append(segments, &seg)
	}
	
	return segments, nil
}

// UpdateSegmentStatus updates a segment's status
func (s *PostgresStore) UpdateSegmentStatus(ctx context.Context, segmentID string, status SegmentStatus) error {
	query := "UPDATE segments SET status = $1 WHERE id = $2"
	
	result, err := s.db.ExecContext(ctx, query, status, segmentID)
	if err != nil {
		return fmt.Errorf("failed to update segment status: %w", err)
	}
	
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	
	if rows == 0 {
		return fmt.Errorf("segment not found: %s", segmentID)
	}
	
	return nil
}

// DeleteOldSegments deletes segments older than the specified time
func (s *PostgresStore) DeleteOldSegments(ctx context.Context, olderThan time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		"DELETE FROM segments WHERE created_at < $1",
		olderThan,
	)
	
	if err != nil {
		return 0, fmt.Errorf("failed to delete old segments: %w", err)
	}
	
	rows, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	
	if rows > 0 {
		s.logger.Info("Deleted old segments",
			recorderlog.Int64("count", rows),
			recorderlog.Time("older_than", olderThan))
	}
	
	return rows, nil
}

// SaveMotionEvent saves a motion event
func (s *PostgresStore) SaveMotionEvent(ctx context.Context, event *MotionEvent) error {
	// Get internal recording ID
	var recordingID string
	err := s.db.QueryRowContext(ctx,
		"SELECT id FROM recordings WHERE external_id = $1",
		event.RecordingID,
	).Scan(&recordingID)
	
	if err != nil {
		return fmt.Errorf("failed to find recording: %w", err)
	}
	
	regionsJSON, err := json.Marshal(event.Regions)
	if err != nil {
		return fmt.Errorf("failed to marshal regions: %w", err)
	}
	
	metadataJSON, err := json.Marshal(event.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}
	
	query := `
		INSERT INTO motion_events (
			recording_id, event_time, confidence, duration_seconds,
			regions, peak_confidence, total_motion_frames, metadata
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8
		) RETURNING id
	`
	
	err = s.db.QueryRowContext(
		ctx, query,
		recordingID, event.EventTime, event.Confidence, event.Duration.Seconds(),
		regionsJSON, event.PeakConfidence, event.TotalMotionFrames, metadataJSON,
	).Scan(&event.ID)
	
	return err
}

// GetMotionEvents retrieves motion events for a recording
func (s *PostgresStore) GetMotionEvents(ctx context.Context, recordingID string) ([]*MotionEvent, error) {
	query := `
		SELECT 
			m.id, m.event_time, m.confidence, m.duration_seconds,
			m.regions, m.peak_confidence, m.total_motion_frames, m.metadata,
			m.created_at
		FROM motion_events m
		JOIN recordings r ON m.recording_id = r.id
		WHERE r.external_id = $1
		ORDER BY m.event_time DESC
	`
	
	rows, err := s.db.QueryContext(ctx, query, recordingID)
	if err != nil {
		return nil, fmt.Errorf("failed to get motion events: %w", err)
	}
	defer rows.Close()
	
	events := make([]*MotionEvent, 0)
	for rows.Next() {
		var event MotionEvent
		var duration float64
		var regionsJSON, metadataJSON []byte
		
		err := rows.Scan(
			&event.ID, &event.EventTime, &event.Confidence, &duration,
			&regionsJSON, &event.PeakConfidence, &event.TotalMotionFrames, &metadataJSON,
			&event.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		
		event.RecordingID = recordingID
		event.Duration = time.Duration(duration * float64(time.Second))
		
		if len(regionsJSON) > 0 {
			if err := json.Unmarshal(regionsJSON, &event.Regions); err != nil {
				return nil, err
			}
		}
		
		if len(metadataJSON) > 0 {
			if err := json.Unmarshal(metadataJSON, &event.Metadata); err != nil {
				return nil, err
			}
		}
		
		events = append(events, &event)
	}
	
	return events, nil
}

// GetStorageStats returns storage statistics
func (s *PostgresStore) GetStorageStats(ctx context.Context) (*StorageStats, error) {
	var stats StorageStats
	
	query := `
		SELECT 
			COUNT(*) as total_recordings,
			COALESCE(SUM(total_size_bytes), 0) as total_size,
			COALESCE(SUM(segment_count), 0) as total_segments,
			COALESCE(AVG(duration_seconds), 0) as avg_duration
		FROM recordings
	`
	
	var avgDuration float64
	err := s.db.QueryRowContext(ctx, query).Scan(
		&stats.TotalRecordings,
		&stats.TotalSize,
		&stats.TotalSegments,
		&avgDuration,
	)
	
	if err != nil {
		return nil, fmt.Errorf("failed to get storage stats: %w", err)
	}
	
	stats.AverageDuration = time.Duration(avgDuration * float64(time.Second))
	
	// Get type breakdown
	typeQuery := `
		SELECT type, COUNT(*), COALESCE(SUM(total_size_bytes), 0)
		FROM recordings
		GROUP BY type
	`
	
	rows, err := s.db.QueryContext(ctx, typeQuery)
	if err != nil {
		return &stats, nil // Return partial stats
	}
	defer rows.Close()
	
	stats.ByType = make(map[string]TypeStats)
	for rows.Next() {
		var recordingType string
		var ts TypeStats
		
		err := rows.Scan(&recordingType, &ts.Count, &ts.TotalSize)
		if err != nil {
			continue
		}
		
		stats.ByType[recordingType] = ts
	}
	
	return &stats, nil
}

// GetRecordingStats returns recording statistics for a time range
func (s *PostgresStore) GetRecordingStats(ctx context.Context, timeRange TimeRange) (*RecordingStats, error) {
	var stats RecordingStats
	
	query := `
		SELECT 
			COUNT(*) as count,
			COALESCE(SUM(duration_seconds), 0) as total_duration,
			COALESCE(SUM(total_size_bytes), 0) as total_size,
			COALESCE(AVG(motion_confidence), 0) as avg_confidence
		FROM recordings
		WHERE started_at >= $1 AND started_at <= $2
	`
	
	var totalDuration float64
	err := s.db.QueryRowContext(ctx, query, timeRange.Start, timeRange.End).Scan(
		&stats.TotalRecordings,
		&totalDuration,
		&stats.TotalSize,
		&stats.AverageMotionConfidence,
	)
	
	if err != nil {
		return nil, fmt.Errorf("failed to get recording stats: %w", err)
	}
	
	stats.TotalDuration = time.Duration(totalDuration * float64(time.Second))
	stats.TimeRange = timeRange
	
	// Get hourly distribution
	hourQuery := `
		SELECT 
			DATE_TRUNC('hour', started_at) as hour,
			COUNT(*) as count
		FROM recordings
		WHERE started_at >= $1 AND started_at <= $2
		GROUP BY hour
		ORDER BY hour
	`
	
	rows, err := s.db.QueryContext(ctx, hourQuery, timeRange.Start, timeRange.End)
	if err != nil {
		return &stats, nil // Return partial stats
	}
	defer rows.Close()
	
	stats.RecordingsByHour = make(map[time.Time]int)
	for rows.Next() {
		var hour time.Time
		var count int
		
		if err := rows.Scan(&hour, &count); err != nil {
			continue
		}
		
		stats.RecordingsByHour[hour] = count
	}
	
	return &stats, nil
}

// Vacuum runs VACUUM on the database to reclaim space
func (s *PostgresStore) Vacuum(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "VACUUM ANALYZE")
	if err != nil {
		return fmt.Errorf("vacuum failed: %w", err)
	}
	
	s.logger.Info("Database vacuum completed")
	return nil
}

// HealthCheck verifies database connectivity
func (s *PostgresStore) HealthCheck(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// Close closes the database connection
func (s *PostgresStore) Close() error {
	return s.db.Close()
}

// Helper function to validate field names (prevent SQL injection)
func isValidFieldName(field string) bool {
	validFields := map[string]bool{
		"status": true, "ended_at": true, "duration_seconds": true,
		"segment_count": true, "total_size_bytes": true,
		"resolution": true, "fps": true, "codec": true, "bitrate": true,
		"metadata": true, "tags": true, "started_at": true,
	}
	return validFields[field]
}