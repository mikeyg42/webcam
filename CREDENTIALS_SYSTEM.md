# Encrypted Credential Management System

## Overview

This system eliminates the need for users to repeatedly enter sensitive credentials (WebRTC, MinIO, PostgreSQL passwords) in the configuration UI. Instead:

1. **User authenticates once** via Tailscale (network-level auth)
2. **Backend identifies user** using Tailscale WhoIs API
3. **Credentials are stored encrypted** in SQLite database
4. **Backend auto-populates config** from encrypted storage
5. **Credentials never sent to browser**

## Architecture

```
┌─────────────┐
│   User      │ ← Authenticated via Tailscale
└──────┬──────┘
       │ HTTPS Request (Tailscale IP)
       ↓
┌─────────────────────────────────────┐
│  Backend (Go)                       │
│  1. Tailscale WhoIs → user email    │
│  2. Query SQLite → encrypted creds  │
│  3. Decrypt → plaintext passwords   │
│  4. Populate config automatically   │
└──────┬──────────────────────────────┘
       │
       ↓
┌─────────────────────────┐
│  SQLite Database        │
│  - user_email (PK)      │
│  - webrtc_username      │
│  - webrtc_password_enc  │ ← AES-256 encrypted
│  - minio_access_key     │
│  - minio_secret_enc     │ ← AES-256 encrypted
│  - postgres_username    │
│  - postgres_password_enc│ ← AES-256 encrypted
└─────────────────────────┘
```

## Security Features

- **AES-256-GCM encryption** for all passwords
- **Unique nonce per encryption** (prevents pattern analysis)
- **Environment-based encryption key** (`CREDENTIALS_ENCRYPTION_KEY`)
- **Network-level authentication** (Tailscale required)
- **No credentials in browser** (never sent to frontend)
- **Database-level isolation** (one row per user)

## Components Created

### 1. Database Schema (`internal/database/schema.sql`)
```sql
CREATE TABLE user_credentials (
    user_email TEXT PRIMARY KEY,
    webrtc_username TEXT NOT NULL,
    webrtc_password_encrypted BLOB NOT NULL,
    minio_access_key TEXT NOT NULL,
    minio_secret_key_encrypted BLOB NOT NULL,
    postgres_username TEXT NOT NULL,
    postgres_password_encrypted BLOB NOT NULL,
    created_at TIMESTAMP,
    updated_at TIMESTAMP
);
```

### 2. Encryption Module (`internal/database/crypto.go`)
- `Encrypt(plaintext) → []byte` - AES-256-GCM encryption
- `Decrypt(ciphertext) → string` - Decryption with integrity check
- Uses `CREDENTIALS_ENCRYPTION_KEY` env var (derived with Argon2id KDF)

### 3. Database Package (`internal/database/credentials.go`)
- `NewDB(dbPath)` - Initialize database with schema
- `GetUserCredentials(email) → *UserCredentials` - Retrieve and decrypt
- `SaveUserCredentials(creds)` - Encrypt and store/update
- `DeleteUserCredentials(email)` - Remove user's credentials

### 4. Config Enrichment (`internal/database/config_enrichment.go`)
- `EnrichConfigWithUserCredentials(cfg, db, userEmail)` - Auto-populate config
- `ValidateUserHasCredentials(db, userEmail)` - Check if creds exist

### 5. Tailscale WhoIs (`internal/tailscale/tailscale.go`)
- `GetUserEmailFromRequest(r)` - Extract user email from HTTP request
- `GetUserEmailFromIP(ipAddr)` - Call `tailscale whois --json`

### 6. API Endpoints (`internal/api/credentials_handler.go`)
- `POST /api/credentials` - Set/update credentials (first-time setup)
- `GET /api/credentials/status` - Check if user has credentials stored
- `DELETE /api/credentials` - Remove credentials

## Setup & Usage

### 1. Set Encryption Key (Production)
```bash
export CREDENTIALS_ENCRYPTION_KEY="your-secure-random-key-here"
```

**Important**: Use a strong random key in production:
```bash
openssl rand -base64 32
```

### 2. Initialize Database
The database is automatically created on first run. Default path: `~/.webcam2/credentials.db`

### 3. User First-Time Setup Flow

**Step 1**: User visits configuration page (authenticated via Tailscale)

**Step 2**: Frontend checks credential status:
```javascript
fetch('/api/credentials/status')
  .then(res => res.json())
  .then(data => {
    if (!data.has_credentials) {
      // Show one-time credential setup form
    } else {
      // Hide credential fields, they're auto-populated
    }
  });
```

**Step 3**: User submits credentials once:
```javascript
fetch('/api/credentials', {
  method: 'POST',
  headers: {'Content-Type': 'application/json'},
  body: JSON.stringify({
    webrtc_username: 'user1',
    webrtc_password: 'secure_pass',
    minio_access_key: 'minioadmin',
    minio_secret_key: 'minioadmin',
    postgres_username: 'recorder',
    postgres_password: 'recorder'
  })
});
```

**Step 4**: Backend:
- Gets user email from Tailscale WhoIs
- Encrypts passwords
- Stores in database
- Returns success

**Step 5**: On subsequent config loads:
- Backend identifies user via Tailscale
- Retrieves encrypted credentials from database
- Decrypts and populates config automatically
- User never sees credential fields again

### 4. Integration with Main Application

In `cmd/security-camera/main.go`:

```go
import (
    "github.com/mikeyg42/webcam/internal/database"
    "github.com/mikeyg42/webcam/internal/tailscale"
)

func main() {
    // ... existing setup ...

    // Initialize credential database
    dbPath := filepath.Join(homeDir, ".webcam2", "credentials.db")
    credDB, err := database.NewDB(dbPath)
    if err != nil {
        log.Fatalf("Failed to initialize credentials database: %v", err)
    }
    defer credDB.Close()

    // Initialize Tailscale (if enabled)
    var tsManager *tailscale.TailscaleManager
    if cfg.Tailscale.Enabled {
        tsManager, err = tailscale.NewTailscaleManager(ctx, &cfg.Tailscale)
        if err != nil {
            log.Fatalf("Failed to initialize Tailscale: %v", err)
        }

        // Get user email from Tailscale
        // (in HTTP handler context, would use GetUserEmailFromRequest)
        // For now, this would be done per-request in API handlers
    }

    // Setup API server with credential handler
    credHandler := api.NewCredentialsHandler(credDB, tsManager)

    // Register routes
    http.HandleFunc("/api/credentials", credHandler.HandleSetCredentials)
    http.HandleFunc("/api/credentials/status", credHandler.HandleGetCredentialsStatus)
    http.HandleFunc("/api/credentials", credHandler.HandleDeleteCredentials)
}
```

### 5. Config Handler Integration

Modify config handler to auto-populate credentials:

```go
func (h *ConfigHandler) HandleGetConfig(w http.ResponseWriter, r *http.Request) {
    cfg := h.config // Base config

    // Get user email from Tailscale
    if h.tailscaleManager != nil {
        userEmail, err := h.tailscaleManager.GetUserEmailFromRequest(r)
        if err == nil && userEmail != "" {
            // Auto-populate credentials from database
            if err := database.EnrichConfigWithUserCredentials(cfg, h.credDB, userEmail); err != nil {
                log.Printf("Warning: Could not enrich config with credentials: %v", err)
            }
        }
    }

    // Return config (with credentials populated)
    json.NewEncoder(w).Encode(cfg)
}
```

## Frontend Changes (TODO)

The config UI (`public/config.html`) should be updated to:

1. **Check credential status on load**:
   ```javascript
   async function checkCredentials() {
       const response = await fetch('/api/credentials/status');
       const data = await response.json();
       return data.has_credentials;
   }
   ```

2. **Conditionally show/hide credential fields**:
   ```javascript
   if (await checkCredentials()) {
       // Hide WebRTC, MinIO, PostgreSQL credential inputs
       document.getElementById('credentialSection').style.display = 'none';
       showMessage('Credentials loaded from secure storage');
   } else {
       // Show one-time setup form
       showMessage('Please set your credentials (one-time setup)');
   }
   ```

3. **Add credential management UI** (optional):
   - Button to update stored credentials
   - Button to delete credentials (requires re-setup)

## API Reference

### POST /api/credentials
**Description**: Save/update user credentials

**Authentication**: Tailscale (automatic via network)

**Request Body**:
```json
{
  "webrtc_username": "user1",
  "webrtc_password": "secure_pass",
  "minio_access_key": "minioadmin",
  "minio_secret_key": "secret123",
  "postgres_username": "recorder",
  "postgres_password": "db_pass"
}
```

**Response**:
```json
{
  "success": true,
  "message": "Credentials saved successfully"
}
```

### GET /api/credentials/status
**Description**: Check if user has credentials stored

**Response**:
```json
{
  "has_credentials": true,
  "user_email": "user@example.com"
}
```

### DELETE /api/credentials
**Description**: Delete user's stored credentials

**Response**:
```json
{
  "success": true,
  "message": "Credentials deleted successfully"
}
```

## Testing

### Manual Testing

1. **Start backend with Tailscale enabled**:
   ```bash
   export CREDENTIALS_ENCRYPTION_KEY="test-key-123"
   go run cmd/security-camera/main.go
   ```

2. **Save credentials** (replace IP with your Tailscale IP):
   ```bash
   curl -X POST http://100.x.x.x:8080/api/credentials \
     -H "Content-Type: application/json" \
     -d '{
       "webrtc_username": "testuser",
       "webrtc_password": "testpass123",
       "minio_access_key": "minioadmin",
       "minio_secret_key": "minioadmin",
       "postgres_username": "recorder",
       "postgres_password": "recorder"
     }'
   ```

3. **Check status**:
   ```bash
   curl http://100.x.x.x:8080/api/credentials/status
   ```

4. **Load config** (should auto-populate credentials):
   ```bash
   curl http://100.x.x.x:8080/api/config
   ```

### Unit Testing

Create tests in `internal/database/credentials_test.go`:
```go
func TestEncryptDecrypt(t *testing.T) {
    plaintext := "my-secret-password"
    encrypted, err := Encrypt(plaintext)
    if err != nil {
        t.Fatalf("Encrypt failed: %v", err)
    }

    decrypted, err := Decrypt(encrypted)
    if err != nil {
        t.Fatalf("Decrypt failed: %v", err)
    }

    if decrypted != plaintext {
        t.Errorf("Expected %s, got %s", plaintext, decrypted)
    }
}
```

## Security Considerations

1. **Encryption Key Management**:
   - Store `CREDENTIALS_ENCRYPTION_KEY` in environment (not in code)
   - Use systemd EnvironmentFile or docker secrets in production
   - Rotate keys periodically (requires re-encrypting all credentials)

2. **Database Security**:
   - SQLite file should have restricted permissions (`chmod 600`)
   - Database path should be in secure directory (e.g., `~/.webcam2/`)
   - Consider database-level encryption for additional protection

3. **Network Security**:
   - Tailscale provides encrypted network layer
   - All requests must come from Tailscale network
   - No public internet access to credential endpoints

4. **Credential Rotation**:
   - Users can update credentials via POST /api/credentials
   - Old credentials are immediately overwritten (no history)

5. **Audit Logging** (future enhancement):
   - Log credential access/updates (without logging passwords)
   - Monitor for suspicious patterns

## Troubleshooting

### "Tailscale not enabled - cannot identify user"
- Ensure `cfg.Tailscale.Enabled = true`
- Verify Tailscale is running: `tailscale status`
- Check backend has Tailscale network access

### "Ciphertext too short" or decryption errors
- Encryption key might have changed
- Database might be corrupted
- Re-save credentials to fix

### Credentials not auto-populating
- Check backend logs for "Loaded credentials for user X"
- Verify Tailscale WhoIs returns correct email
- Test with: `tailscale whois YOUR_TAILSCALE_IP`

## Next Steps

- [ ] Integrate credential handler into API server
- [ ] Update frontend config UI to hide/show credential fields
- [ ] Add credential update button in frontend
- [ ] Test with multiple users
- [ ] Add admin endpoint to list all users with credentials (optional)
- [ ] Implement credential export/backup feature (encrypted)
- [ ] Add systemd service file with EnvironmentFile for production

## References

- Tailscale WhoIs API: https://tailscale.com/kb/1080/cli
- AES-GCM in Go: https://pkg.go.dev/crypto/cipher
- SQLite in Go: https://github.com/mattn/go-sqlite3
