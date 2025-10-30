# Configuration GUI Documentation

## Overview

The security camera system now includes a modern, web-based configuration interface that allows you to configure all aspects of the system without editing configuration files or environment variables.

## Accessing the Configuration GUI

1. **Start the application** (which starts the API server on port 8081):
   ```bash
   go run cmd/security-camera/main.go
   ```

2. **Start the Node.js proxy server** (port 3000):
   ```bash
   ./startServers.sh
   # or
   node server.js
   ```

3. **Open the configuration interface** in your browser:
   ```
   http://localhost:3000/config.html
   ```

## Configuration Sections

### 📹 Recording Tab

Configure how and when recordings are saved:

- **Recording Mode**:
  - **Event-Based** (default): Records only when motion is detected
  - **Continuous**: Records all the time
  - **Both**: Records continuously and flags motion events

- **Storage Paths**:
  - **Save Directory**: Local path where recordings are temporarily stored

- **Recording Settings**:
  - **Segment Duration**: Break recordings into manageable segments (1-60 minutes)
  - **Pre-Motion Buffer**: Capture video before motion detected (0-60 seconds)
  - **Post-Motion Buffer**: Continue recording after motion stops (0-120 seconds)
  - **Retention Period**: Auto-delete old recordings after N days

### 🎬 Video Tab

Configure video quality and resolution:

- **Video Quality Settings**:
  - Width and Height (320x240 to 1920x1080)
  - Frame Rate (1-60 fps)
  - Bitrate (100kbps to 10Mbps)

- **Quick Presets**:
  - **Low Quality**: 320x240, 15fps, 200kbps (saves bandwidth and storage)
  - **Medium Quality**: 640x480, 25fps, 500kbps (balanced)
  - **High Quality**: 1280x720, 30fps, 2Mbps (best quality)

### 🏃 Motion Detection Tab

Fine-tune motion detection sensitivity:

- **Detection Settings**:
  - **Sensitivity** (0-255): Lower = more sensitive to movement
  - **Minimum Area**: Ignore movements smaller than N pixels
  - **Cooldown Period**: Minimum time between motion alerts
  - **No Motion Delay**: How long to wait before declaring motion has stopped

- **Advanced Settings**:
  - **Min Consecutive Frames**: Frames needed to trigger detection
  - **Frame Skip**: Process every Nth frame (improves performance)

### 💾 Storage Tab

Configure object storage and database:

- **MinIO Object Storage**:
  - Endpoint (e.g., localhost:9000)
  - Bucket name
  - Access credentials
  - SSL/TLS option

- **PostgreSQL Database**:
  - Host and port
  - Database name
  - Username and password
  - Connection settings

**Note**: Requires Docker infrastructure to be running:
```bash
./scripts/setup-storage.sh
```

### 🌐 Network Tab

Configure Tailscale and WebRTC:

- **Tailscale Configuration**:
  - Enable/disable Tailscale networking
  - Node name and hostname
  - Listen port
  - Authentication key (optional)

  **Important**: Tailscale is REQUIRED for WebRTC connections. The system will not work without it.

- **WebRTC Authentication**:
  - Username for camera access
  - Password (leave blank to keep current)

### 📧 Notifications Tab

Configure email alerts for motion events:

- **Notification Method**:
  - **Disabled**: No email notifications
  - **MailerSend**: Use MailerSend API
  - **Gmail OAuth2**: Use Gmail with OAuth2 authentication

- **Email Settings**:
  - To/From email addresses
  - API tokens or OAuth credentials

## API Endpoints

The configuration system exposes two HTTP API endpoints:

### GET /api/config

Returns the current configuration in JSON format.

**Example Response**:
```json
{
  "recording": {
    "continuousEnabled": false,
    "eventEnabled": true,
    "saveDirectory": "/Users/user/.webcam2/recordings",
    "segmentDuration": 5,
    "preMotionBuffer": 10,
    "postMotionBuffer": 30,
    "retentionDays": 7
  },
  "video": {
    "width": 640,
    "height": 480,
    "framerate": 25,
    "bitrate": 500000
  },
  "motion": { ... },
  "storage": { ... },
  "tailscale": { ... },
  "email": { ... },
  "webrtc": { ... }
}
```

### POST /api/config

Updates the configuration. Send a JSON body with the same structure as GET response.

**Example Request**:
```bash
curl -X POST http://localhost:8081/api/config \
  -H "Content-Type: application/json" \
  -d '{
    "video": {
      "width": 1280,
      "height": 720,
      "framerate": 30,
      "bitrate": 2000000
    }
  }'
```

**Example Response**:
```json
{
  "success": true,
  "message": "Configuration updated successfully. Restart the application for all changes to take effect.",
  "config": { ... }
}
```

## Configuration Persistence

The configuration is saved to a JSON file at:
```
~/.webcam2/config.json
```

This file is automatically created when you save changes through the GUI.

**Important**: Most configuration changes require restarting the application to take effect.

## Security Considerations

### Sensitive Data

The following fields are **not sent to the frontend** for security:

- Storage passwords (MinIO SecretAccessKey, PostgreSQL password)
- Email API tokens
- Tailscale auth keys
- WebRTC passwords

These fields will appear blank in the GUI. To change them, enter a new value. To keep the existing value, leave the field blank.

### Access Control

- The API server runs on `localhost:8081` by default
- The Node.js proxy forwards requests from port 3000
- If Tailscale is enabled, access is restricted to Tailscale network IPs
- For production deployment, consider adding authentication to the configuration interface

## Environment Variables

You can still use environment variables to set configuration. The precedence is:

1. GUI-saved configuration (~/.webcam2/config.json)
2. Environment variables
3. Default values in code

To force environment variables to override GUI settings, remove the config file:
```bash
rm ~/.webcam2/config.json
```

## Troubleshooting

### Configuration not loading

**Problem**: GUI shows "Failed to load configuration"

**Solutions**:
- Ensure Go application is running (API server on port 8081)
- Check browser console for errors
- Verify Node.js proxy is running and forwarding requests

### Changes not taking effect

**Problem**: Configuration saved but behavior unchanged

**Solutions**:
- **Restart the application** - most settings require restart
- Check logs for configuration errors
- Verify the config file was saved: `cat ~/.webcam2/config.json`

### API connection errors

**Problem**: Cannot connect to Go backend

**Solutions**:
- Ensure GO_BACKEND_URL is correct (default: http://localhost:8081)
- Check if port 8081 is available: `lsof -i :8081`
- Verify no firewall blocking the connection

### Storage configuration not working

**Problem**: MinIO or PostgreSQL connection fails

**Solutions**:
- Verify Docker containers are running: `docker compose ps`
- Check database/MinIO credentials match docker-compose.yml
- Test connection manually:
  ```bash
  psql -h localhost -U recorder -d recordings
  mc alias set myminio http://localhost:9000 minioadmin minioadmin
  ```

## Development

### Adding New Configuration Fields

1. **Add to config struct** (`internal/api/config_handler.go`):
   ```go
   type MyNewSettings struct {
       Field1 string `json:"field1"`
       Field2 int    `json:"field2"`
   }
   ```

2. **Update API methods**:
   - Add to `ConfigResponse` struct
   - Update `configToResponse()` to populate new fields
   - Update `updateInternalConfig()` to apply changes
   - Update `validateConfigRequest()` if validation needed

3. **Add to GUI** (`public/config.html`):
   ```html
   <div class="form-group">
       <label>My New Setting</label>
       <input type="text" id="myNewField">
   </div>
   ```

4. **Update JavaScript** (`public/config.js`):
   - Add to `saveConfig()` to collect field value
   - Add to `populateForm()` to display field value

### API Server Configuration

The API server configuration can be customized:

```go
// In cmd/security-camera/main.go
apiServer := api.NewServer(cfg, ":8081") // Change port here
```

Set environment variable in Node.js server:
```bash
GO_BACKEND_URL=http://localhost:8082 node server.js
```

## Testing

### Manual Testing Checklist

- [ ] Load configuration successfully
- [ ] Save each tab's configuration
- [ ] Verify changes persist after reload
- [ ] Test validation (invalid values rejected)
- [ ] Verify sensitive fields not displayed
- [ ] Test quick presets work
- [ ] Verify conditional field visibility (Tailscale, Email)
- [ ] Test navigation between viewer and config page

### Automated Testing

Currently no automated tests. Future improvements:

- Unit tests for config validation
- Integration tests for API endpoints
- E2E tests for GUI interactions

## Future Enhancements

Potential improvements for future versions:

1. **Live Configuration Updates**: Apply changes without restart
2. **Configuration Profiles**: Save and load preset configurations
3. **Import/Export**: Backup and restore configuration
4. **Validation Feedback**: Real-time validation with inline errors
5. **Advanced Mode**: Show/hide advanced settings
6. **Configuration History**: Track and revert configuration changes
7. **Multi-User Support**: User-based configurations and permissions
8. **Camera Preview**: Live preview while configuring video settings

---

**Version**: 1.0
**Last Updated**: 2025-10-30
**Author**: Claude Code
