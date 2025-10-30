-- PostgreSQL schema for webcam2 recording metadata
-- Version: 1.0

-- Enable UUID extension
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- ============================================================================
-- RECORDINGS TABLE - Main recording metadata
-- ============================================================================
CREATE TABLE IF NOT EXISTS recordings (
    id SERIAL PRIMARY KEY,
    external_id VARCHAR(255) UNIQUE NOT NULL,
    type VARCHAR(50) NOT NULL CHECK (type IN ('continuous', 'event')),
    status VARCHAR(50) NOT NULL CHECK (status IN ('recording', 'processing', 'completed', 'failed')),

    -- Timestamps
    started_at TIMESTAMP NOT NULL DEFAULT NOW(),
    ended_at TIMESTAMP,
    duration_seconds DECIMAL(10, 2),

    -- Storage information
    bucket VARCHAR(255) NOT NULL,
    base_key VARCHAR(1024) NOT NULL,
    segment_count INTEGER DEFAULT 0,
    total_size_bytes BIGINT DEFAULT 0,

    -- Video properties
    resolution VARCHAR(50),
    fps INTEGER,
    codec VARCHAR(50),
    bitrate INTEGER,

    -- Motion event specific fields
    motion_confidence DECIMAL(5, 4),
    pre_buffer_seconds INTEGER,
    post_buffer_seconds INTEGER,
    trigger_time TIMESTAMP,

    -- Audit fields
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),

    -- Indexes
    CONSTRAINT recordings_external_id_key UNIQUE (external_id)
);

-- Index on status for filtering active recordings
CREATE INDEX IF NOT EXISTS idx_recordings_status ON recordings(status);

-- Index on started_at for time-based queries
CREATE INDEX IF NOT EXISTS idx_recordings_started_at ON recordings(started_at DESC);

-- Index on type for filtering by recording type
CREATE INDEX IF NOT EXISTS idx_recordings_type ON recordings(type);

-- Compound index for common query patterns
CREATE INDEX IF NOT EXISTS idx_recordings_type_status ON recordings(type, status);


-- ============================================================================
-- SEGMENTS TABLE - Recording segments
-- ============================================================================
CREATE TABLE IF NOT EXISTS segments (
    id SERIAL PRIMARY KEY,
    recording_id VARCHAR(255) NOT NULL,
    segment_index INTEGER NOT NULL,

    -- Time information
    start_time TIMESTAMP NOT NULL,
    end_time TIMESTAMP NOT NULL,
    duration_seconds DECIMAL(10, 2),

    -- Storage information
    storage_key VARCHAR(1024) NOT NULL,
    size_bytes BIGINT NOT NULL DEFAULT 0,
    frame_count BIGINT NOT NULL DEFAULT 0,
    checksum VARCHAR(128),

    -- Status tracking
    status VARCHAR(50) NOT NULL DEFAULT 'recording' CHECK (status IN ('recording', 'uploading', 'completed', 'verified', 'failed')),
    upload_attempts INTEGER DEFAULT 0,
    last_error TEXT,

    -- Timestamps
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    uploaded_at TIMESTAMP,

    -- Unique constraint
    CONSTRAINT segments_recording_index_key UNIQUE (recording_id, segment_index),

    -- Foreign key to recordings table
    CONSTRAINT fk_segments_recording
        FOREIGN KEY (recording_id)
        REFERENCES recordings(external_id)
        ON DELETE CASCADE
);

-- Index on recording_id for quick segment lookup
CREATE INDEX IF NOT EXISTS idx_segments_recording_id ON segments(recording_id);

-- Index on status for filtering segments by status
CREATE INDEX IF NOT EXISTS idx_segments_status ON segments(status);

-- Index on start_time for time-based queries
CREATE INDEX IF NOT EXISTS idx_segments_start_time ON segments(start_time DESC);


-- ============================================================================
-- MOTION_EVENTS TABLE - Motion detection events
-- ============================================================================
CREATE TABLE IF NOT EXISTS motion_events (
    id SERIAL PRIMARY KEY,
    recording_id VARCHAR(255) NOT NULL,

    -- Event details
    event_time TIMESTAMP NOT NULL,
    confidence DECIMAL(5, 4) NOT NULL,
    duration_seconds DECIMAL(10, 2),
    peak_confidence DECIMAL(5, 4),
    total_motion_frames INTEGER,

    -- Timestamps
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),

    -- Foreign key to recordings table
    CONSTRAINT fk_motion_events_recording
        FOREIGN KEY (recording_id)
        REFERENCES recordings(external_id)
        ON DELETE CASCADE
);

-- Index on recording_id for quick event lookup
CREATE INDEX IF NOT EXISTS idx_motion_events_recording_id ON motion_events(recording_id);

-- Index on event_time for time-based queries
CREATE INDEX IF NOT EXISTS idx_motion_events_event_time ON motion_events(event_time DESC);

-- Index on confidence for filtering high-confidence events
CREATE INDEX IF NOT EXISTS idx_motion_events_confidence ON motion_events(confidence DESC);


-- ============================================================================
-- TRIGGERS - Auto-update timestamps
-- ============================================================================

-- Function to update updated_at timestamp
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Trigger for recordings table
DROP TRIGGER IF EXISTS update_recordings_updated_at ON recordings;
CREATE TRIGGER update_recordings_updated_at
    BEFORE UPDATE ON recordings
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();


-- ============================================================================
-- VIEWS - Useful queries
-- ============================================================================

-- View: Recent recordings with segment counts
CREATE OR REPLACE VIEW v_recent_recordings AS
SELECT
    r.id,
    r.external_id,
    r.type,
    r.status,
    r.started_at,
    r.ended_at,
    r.duration_seconds,
    r.segment_count,
    r.total_size_bytes,
    COUNT(DISTINCT s.id) AS actual_segment_count,
    SUM(s.size_bytes) AS actual_total_size,
    r.motion_confidence
FROM recordings r
LEFT JOIN segments s ON r.external_id = s.recording_id
GROUP BY r.id
ORDER BY r.started_at DESC;

-- View: Active recordings (currently recording)
CREATE OR REPLACE VIEW v_active_recordings AS
SELECT *
FROM recordings
WHERE status = 'recording'
ORDER BY started_at DESC;

-- View: Failed recordings for troubleshooting
CREATE OR REPLACE VIEW v_failed_recordings AS
SELECT
    r.*,
    COUNT(s.id) AS completed_segments
FROM recordings r
LEFT JOIN segments s ON r.external_id = s.recording_id AND s.status = 'completed'
WHERE r.status = 'failed'
GROUP BY r.id
ORDER BY r.started_at DESC;


-- ============================================================================
-- INITIAL DATA / PERMISSIONS
-- ============================================================================

-- Grant permissions to recorder user (already owner, but explicit is good)
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO recorder;
GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO recorder;

-- Success message
DO $$
BEGIN
    RAISE NOTICE 'Database schema initialized successfully';
    RAISE NOTICE 'Tables: recordings, segments, motion_events';
    RAISE NOTICE 'Views: v_recent_recordings, v_active_recordings, v_failed_recordings';
END $$;
