# Credential Management - Quick Start Guide

## Overview

Your webcam security system now has **automatic credential management**. Users authenticate via Tailscale, and their credentials (WebRTC, MinIO, PostgreSQL) are stored encrypted in a local SQLite database and auto-populated.

**No more repeatedly entering passwords! 🎉**

---

## Quick Start (3 Steps)

### Step 1: Generate Encryption Key

```bash
./generate-encryption-key.sh
```

This will output something like:
```
CREDENTIALS_ENCRYPTION_KEY="Ab3dEf7gHi9jKl2mNoPqRs4tUvWxYz1A2b4C6d8="
```

**Copy this key and save it securely!**

### Step 2: Set Environment Variable

Add to your shell profile (`~/.bashrc` or `~/.zshrc`):

```bash
export CREDENTIALS_ENCRYPTION_KEY="Ab3dEf7gHi9jKl2mNoPqRs4tUvWxYz1A2b4C6d8="
```

Then reload:
```bash
source ~/.zshrc  # or source ~/.bashrc
```

### Step 3: Start the System

```bash
./start-all.sh
```

The script will automatically use your encryption key. You'll see:
```
✓ Credential database initialized at ~/.webcam2/credentials.db
✓ Tailscale manager initialized for user authentication
```

---

## First-Time User Experience

### User's Perspective

1. **First Visit** (one-time setup):
   - User connects to `http://localhost:8080/config.html` via Tailscale
   - Frontend detects: "No credentials stored"
   - UI shows message: *"First-time Setup: Please enter your credentials once."*
   - User fills in credentials:
     - WebRTC username/password
     - MinIO access key/secret
     - PostgreSQL username/password
   - User clicks "Save Configuration"
   - Backend:
     - Identifies user via Tailscale WhoIs (gets email)
     - Encrypts passwords with AES-256
     - Stores in SQLite database
   - Success message: "Credentials saved securely!"

2. **Subsequent Visits** (automatic):
   - User connects to `http://localhost:8080/config.html`
   - Frontend detects: "Credentials found!"
   - UI hides credential fields
   - Shows message: *"✓ Credentials loaded from secure storage"*
   - Backend auto-populates config with decrypted credentials
   - User never sees passwords again

---

## Testing the Flow

### Test Without Tailscale (Development)

If Tailscale is not enabled, the system falls back gracefully:
- Credential management endpoints will return warnings
- Config UI will show all fields (no auto-hide)
- Users enter credentials normally
- No automatic storage/retrieval

### Test With Tailscale (Production)

1. **Enable Tailscale in config:**
   ```json
   {
     "tailscale": {
       "enabled": true,
       "hostname": "webcam-security"
     }
   }
   ```

2. **Start system:**
   ```bash
   ./start-all.sh
   ```

3. **From browser (on Tailscale network):**
   - Visit `http://<tailscale-ip>:8080/config.html`
   - First time: Enter credentials
   - Second time: Credentials auto-filled

4. **Check backend logs:**
   ```bash
   tail -f logs/go-camera.log | grep -i credential
   ```

   You should see:
   ```
   ✓ Credential database initialized at ~/.webcam2/credentials.db
   ✓ Tailscale manager initialized for user authentication
   [APIServer] Credential management endpoints registered
   ✓ Loaded credentials for user user@example.com from secure storage
   ```

---

## API Endpoints (For Testing)

### Check Credential Status
```bash
curl http://localhost:8081/api/credentials/status
```

Response:
```json
{
  "has_credentials": false,
  "user_email": "user@example.com"
}
```

### Save Credentials (First-Time)
```bash
curl -X POST http://localhost:8081/api/credentials \
  -H "Content-Type: application/json" \
  -d '{
    "webrtc_username": "myuser",
    "webrtc_password": "mypass123",
    "minio_access_key": "minioadmin",
    "minio_secret_key": "minioadmin",
    "postgres_username": "recorder",
    "postgres_password": "recorder"
  }'
```

Response:
```json
{
  "success": true,
  "message": "Credentials saved successfully"
}
```

### Delete Credentials (Reset)
```bash
curl -X DELETE http://localhost:8081/api/credentials
```

---

## File Locations

| File | Purpose |
|------|---------|
| `~/.webcam2/credentials.db` | SQLite database with encrypted credentials |
| `~/.webcam2/config.json` | Application configuration (no passwords) |
| `logs/go-camera.log` | Backend logs (includes credential system messages) |

---

## Security Notes

### Encryption
- **Algorithm**: AES-256-GCM (Galois/Counter Mode)
- **Key derivation**: Argon2id (memory-hard KDF) with per-installation salt
- **Unique nonce**: Generated per encryption (prevents replay attacks)
- **Authenticated encryption**: GCM provides both confidentiality and integrity

### What's Encrypted
- WebRTC password ✓
- MinIO secret key ✓
- PostgreSQL password ✓

### What's NOT Encrypted (Usernames)
- WebRTC username
- MinIO access key
- PostgreSQL username

*Usernames are not considered sensitive (they're not authentication secrets)*

### Key Management
⚠️ **CRITICAL**: If you lose `CREDENTIALS_ENCRYPTION_KEY`, you cannot decrypt stored credentials!

**Production Recommendations:**
1. Store key in secure secrets manager (HashiCorp Vault, AWS Secrets Manager, etc.)
2. Add to systemd EnvironmentFile with restricted permissions (`chmod 600`)
3. Rotate key periodically (requires re-encrypting all user credentials)
4. Never commit key to git (add to `.gitignore`)

---

## Troubleshooting

### Problem: "Tailscale not enabled - cannot identify user"

**Solution**: Enable Tailscale in config:
```json
{
  "tailscale": {
    "enabled": true
  }
}
```

And ensure Tailscale is running:
```bash
tailscale status
```

### Problem: "Ciphertext too short" or decryption errors

**Causes**:
1. Encryption key changed
2. Database corrupted

**Solution**: Delete and recreate credentials:
```bash
rm ~/.webcam2/credentials.db
# Re-enter credentials in UI
```

### Problem: Credentials not auto-populating

**Debug steps**:
```bash
# 1. Check database exists
ls -la ~/.webcam2/credentials.db

# 2. Check Tailscale can identify user
tailscale whois $(tailscale ip -4)

# 3. Check backend logs
tail -f logs/go-camera.log | grep -i credential
```

### Problem: Frontend still shows credential fields

**Possible causes**:
1. No credentials stored yet (first visit)
2. Tailscale not enabled
3. JavaScript not loaded

**Solution**:
1. Open browser console (F12)
2. Look for errors in Console tab
3. Check Network tab for failed requests to `/api/credentials/status`

---

## Production Deployment

### Using systemd (Linux)

1. **Create environment file:**
   ```bash
   sudo mkdir -p /etc/webcam2
   sudo bash -c 'cat > /etc/webcam2/credentials.env << EOF
   CREDENTIALS_ENCRYPTION_KEY="your-generated-key-here"
   EOF'
   sudo chmod 600 /etc/webcam2/credentials.env
   ```

2. **Create systemd service:**
   ```ini
   [Unit]
   Description=Webcam Security Camera
   After=network.target docker.service tailscaled.service

   [Service]
   Type=simple
   User=webcam
   WorkingDirectory=/opt/webcam2
   EnvironmentFile=/etc/webcam2/credentials.env
   ExecStart=/opt/webcam2/security-camera
   Restart=on-failure
   RestartSec=10

   [Install]
   WantedBy=multi-user.target
   ```

3. **Enable and start:**
   ```bash
   sudo systemctl daemon-reload
   sudo systemctl enable webcam-security
   sudo systemctl start webcam-security
   ```

### Using Docker Compose

Add to your `docker-compose.yml`:

```yaml
services:
  webcam-backend:
    image: webcam-security:latest
    environment:
      - CREDENTIALS_ENCRYPTION_KEY=${CREDENTIALS_ENCRYPTION_KEY}
    env_file:
      - /etc/webcam2/credentials.env  # Alternatively
    volumes:
      - ~/.webcam2:/root/.webcam2
```

Then run:
```bash
export CREDENTIALS_ENCRYPTION_KEY="your-key"
docker compose up -d
```

---

## Multiple Users

The system supports multiple users automatically:

| User | Email | Stored Credentials |
|------|-------|-------------------|
| Alice | alice@company.com | Her WebRTC/MinIO/PostgreSQL creds |
| Bob | bob@company.com | His WebRTC/MinIO/PostgreSQL creds |
| Carol | carol@company.com | Her WebRTC/MinIO/PostgreSQL creds |

**How it works:**
1. Each user is identified by their Tailscale email
2. Each user has a separate row in the database
3. Backend auto-loads the correct credentials based on who's making the request
4. Users never see each other's credentials

---

## Need Help?

- **Documentation**: See `CREDENTIALS_SYSTEM.md` for full technical details
- **Logs**: Check `logs/go-camera.log` for backend errors
- **Database**: Inspect with `sqlite3 ~/.webcam2/credentials.db`

---

## Summary

✅ **One-time setup**: Generate key, set environment variable
✅ **User experience**: Enter credentials once, auto-filled forever
✅ **Security**: AES-256 encrypted, Tailscale-authenticated
✅ **Multi-user**: Each user has their own encrypted credentials
✅ **Zero maintenance**: Automatic load/save, no manual management

**You're done! 🎉**
