package database

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// UserCredentials represents a user's stored credentials
type UserCredentials struct {
	UserEmail string

	// WebRTC
	WebRTCUsername string
	WebRTCPassword string

	// MinIO
	MinIOAccessKey string
	MinIOSecretKey string

	// PostgreSQL
	PostgresUsername string
	PostgresPassword string

	CreatedAt time.Time
	UpdatedAt time.Time
}

// DB wraps the SQLite database connection
type DB struct {
	conn *sql.DB
}

// NewDB creates a new database connection and initializes schema
func NewDB(dbPath string) (*DB, error) {
	conn, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}

	db := &DB{conn: conn}

	if err := db.initSchema(); err != nil {
		return nil, fmt.Errorf("init schema: %w", err)
	}

	return db, nil
}

// initSchema creates the credentials table if it doesn't exist
func (db *DB) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS user_credentials (
		user_email TEXT PRIMARY KEY,

		webrtc_username TEXT NOT NULL,
		webrtc_password_encrypted BLOB NOT NULL,

		minio_access_key TEXT NOT NULL,
		minio_secret_key_encrypted BLOB NOT NULL,

		postgres_username TEXT NOT NULL,
		postgres_password_encrypted BLOB NOT NULL,

		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_user_email ON user_credentials(user_email);
	`

	_, err := db.conn.Exec(schema)
	return err
}

// GetUserCredentials retrieves and decrypts credentials for a user
func (db *DB) GetUserCredentials(userEmail string) (*UserCredentials, error) {
	var creds UserCredentials
	var webrtcPasswordEnc, minioSecretKeyEnc, postgresPasswordEnc []byte

	query := `
		SELECT user_email, webrtc_username, webrtc_password_encrypted,
		       minio_access_key, minio_secret_key_encrypted,
		       postgres_username, postgres_password_encrypted,
		       created_at, updated_at
		FROM user_credentials
		WHERE user_email = ?
	`

	err := db.conn.QueryRow(query, userEmail).Scan(
		&creds.UserEmail,
		&creds.WebRTCUsername, &webrtcPasswordEnc,
		&creds.MinIOAccessKey, &minioSecretKeyEnc,
		&creds.PostgresUsername, &postgresPasswordEnc,
		&creds.CreatedAt, &creds.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil // No credentials found (not an error)
	}
	if err != nil {
		return nil, fmt.Errorf("query credentials: %w", err)
	}

	// Decrypt passwords
	creds.WebRTCPassword, err = Decrypt(webrtcPasswordEnc)
	if err != nil {
		return nil, fmt.Errorf("decrypt webrtc password: %w", err)
	}

	creds.MinIOSecretKey, err = Decrypt(minioSecretKeyEnc)
	if err != nil {
		// Zero out already decrypted password before returning error
		secureZero(&creds.WebRTCPassword)
		return nil, fmt.Errorf("decrypt minio secret: %w", err)
	}

	creds.PostgresPassword, err = Decrypt(postgresPasswordEnc)
	if err != nil {
		// Zero out already decrypted passwords before returning error
		secureZero(&creds.WebRTCPassword)
		secureZero(&creds.MinIOSecretKey)
		return nil, fmt.Errorf("decrypt postgres password: %w", err)
	}

	return &creds, nil
}

// secureZero overwrites a string's memory with zeros
// This helps prevent credentials from lingering in memory
func secureZero(s *string) {
	if s == nil || *s == "" {
		return
	}
	// Convert to byte slice and zero it
	b := []byte(*s)
	for i := range b {
		b[i] = 0
	}
	*s = ""
}

// SaveUserCredentials stores or updates encrypted credentials for a user
func (db *DB) SaveUserCredentials(creds *UserCredentials) error {
	// Encrypt passwords
	webrtcPasswordEnc, err := Encrypt(creds.WebRTCPassword)
	if err != nil {
		return fmt.Errorf("encrypt webrtc password: %w", err)
	}

	minioSecretKeyEnc, err := Encrypt(creds.MinIOSecretKey)
	if err != nil {
		return fmt.Errorf("encrypt minio secret: %w", err)
	}

	postgresPasswordEnc, err := Encrypt(creds.PostgresPassword)
	if err != nil {
		return fmt.Errorf("encrypt postgres password: %w", err)
	}

	// Upsert (insert or update)
	query := `
		INSERT INTO user_credentials (
			user_email, webrtc_username, webrtc_password_encrypted,
			minio_access_key, minio_secret_key_encrypted,
			postgres_username, postgres_password_encrypted,
			updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(user_email) DO UPDATE SET
			webrtc_username = excluded.webrtc_username,
			webrtc_password_encrypted = excluded.webrtc_password_encrypted,
			minio_access_key = excluded.minio_access_key,
			minio_secret_key_encrypted = excluded.minio_secret_key_encrypted,
			postgres_username = excluded.postgres_username,
			postgres_password_encrypted = excluded.postgres_password_encrypted,
			updated_at = CURRENT_TIMESTAMP
	`

	_, err = db.conn.Exec(query,
		creds.UserEmail,
		creds.WebRTCUsername, webrtcPasswordEnc,
		creds.MinIOAccessKey, minioSecretKeyEnc,
		creds.PostgresUsername, postgresPasswordEnc,
	)

	if err != nil {
		return fmt.Errorf("save credentials: %w", err)
	}

	return nil
}

// DeleteUserCredentials removes credentials for a user
func (db *DB) DeleteUserCredentials(userEmail string) error {
	_, err := db.conn.Exec("DELETE FROM user_credentials WHERE user_email = ?", userEmail)
	if err != nil {
		return fmt.Errorf("delete credentials: %w", err)
	}
	return nil
}

// Close closes the database connection
func (db *DB) Close() error {
	return db.conn.Close()
}
