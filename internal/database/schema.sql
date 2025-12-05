-- User credentials table - stores encrypted credentials per user
CREATE TABLE IF NOT EXISTS user_credentials (
    user_email TEXT PRIMARY KEY,

    -- WebRTC credentials
    webrtc_username TEXT NOT NULL,
    webrtc_password_encrypted BLOB NOT NULL,

    -- MinIO credentials
    minio_access_key TEXT NOT NULL,
    minio_secret_key_encrypted BLOB NOT NULL,

    -- PostgreSQL credentials
    postgres_username TEXT NOT NULL,
    postgres_password_encrypted BLOB NOT NULL,

    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Index for quick lookups by email
CREATE INDEX IF NOT EXISTS idx_user_email ON user_credentials(user_email);
