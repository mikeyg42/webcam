# Tailscale-Only WebRTC Security Camera

This document describes the **Tailscale-only** networking architecture for the WebRTC security camera system.

## ⚠️ Breaking Change Notice

**As of commit `b3cbadd`, TURN servers have been completely removed.**

- **No fallback** to traditional networking
- **Tailscale required** - application will fail to start without it
- **External provisioning** - you must run `tailscale up` before starting the app
- **ICE restricted** - only Tailscale interfaces used for WebRTC

If you were using TURN servers before, see the [Migration Guide](#-migrating-from-turn) below.

## 🔗 Overview

**CRITICAL: This system REQUIRES Tailscale. There is no fallback to traditional TURN servers.**

The Tailscale-only architecture provides:

- **Tailscale mesh networking** ONLY (no TURN server option)
- **Direct peer-to-peer connections** over the Tailscale network
- **Automatic NAT traversal** handled by Tailscale's infrastructure
- **Enhanced security** with WireGuard encryption
- **Fail-fast** if Tailscale is not connected

## 📋 Architecture

### Tailscale-Only (Current)
```
Camera Device ←→ Tailscale Network ←→ Viewer
```

**Key Points:**
- No TURN server involved
- ICE candidates restricted to Tailscale interface only
- Host candidates rewritten to use Tailscale IP
- Application fails immediately if Tailscale not connected

## 🚀 Quick Start

### Prerequisites

**IMPORTANT: You must provision Tailscale externally before starting the application.**

1. **Install Tailscale:**
   ```bash
   # macOS
   brew install tailscale

   # Linux
   curl -fsSL https://tailscale.com/install.sh | sh
   ```

2. **Connect to your tailnet:**
   ```bash
   sudo tailscale up --hostname=webcam-security
   ```

3. **Verify connection:**
   ```bash
   tailscale status
   tailscale ip -4  # Should show your Tailscale IP
   ```

### Starting the Application

**With infrastructure services (PostgreSQL, MinIO, ion-sfu):**
```bash
# Start storage infrastructure
./scripts/setup-storage.sh

# Start servers
./startServers.sh
```

**Camera Application (Go):**
```bash
go run cmd/security-camera/main.go
```

If Tailscale is not connected, the application will exit with an error:
```
tailscale is not running (state: <state>) - please start Tailscale externally
```

### Access the Interface

- **Via Tailscale**: http://YOUR_TAILSCALE_IP:3000/index-tailscale.html
- **Local only**: http://localhost:3000/index-tailscale.html

## 📁 File Structure

### New Files Created
- `internal/tailscale/tailscale.go` - Tailscale integration for Go backend
- `internal/rtcManager/manager_tailscale.go` - WebRTC manager with Tailscale support
- `server-tailscale.js` - Enhanced Node.js server with Tailscale integration
- `public/index-tailscale.html` - Enhanced frontend HTML
- `public/index-tailscale.js` - Enhanced frontend JavaScript
- `setup-tailscale.sh` - Automated setup script
- `TAILSCALE_README.md` - This documentation

### Modified Files
- `internal/config/config.go` - Added Tailscale configuration options
- `cmd/security-camera/main.go` - Added Tailscale manager selection

## ⚙️ Configuration

### Environment Variables

Create a `.env` file or set these environment variables:

```bash
# Tailscale Configuration
TAILSCALE_ENABLED=true
TAILSCALE_NODE_NAME=webcam-security
TAILSCALE_AUTH_KEY=your_auth_key_here  # Optional for unattended setup
TAILSCALE_HOSTNAME=your.tailscale.hostname
TAILSCALE_LISTEN_PORT=41641

# Server Configuration  
PORT=3000
HOST=0.0.0.0
ION_SFU_URL=ws://localhost:7000/ws

# Enable Tailscale features
WEBRTC_USE_TAILSCALE=true
```

### Go Application Configuration

The Go application automatically detects Tailscale configuration through the `TailscaleConfig` struct:

```go
type TailscaleConfig struct {
    Enabled    bool   // Enable/disable Tailscale integration
    NodeName   string // Hostname for this device
    AuthKey    string // Optional auth key for unattended setup
    DERPMap    string // Custom DERP server map URL
    Hostname   string // Tailscale hostname
    ListenPort int    // UDP port for Tailscale
}
```

## 🔧 How It Works

### 1. Tailscale Requirement Enforcement
- Go backend requires `TailscaleConfig.Enabled = true`
- Validates Tailscale configuration on startup
- Fails immediately if Tailscale not running or not connected
- Queries Tailscale status (read-only, no `tailscale up` execution)

### 2. WebRTC ICE Configuration
- **SetInterfaceFilter**: Forces ICE gathering on tailscale0/utun* interfaces only
- **SetNAT1To1IPs**: Rewrites host candidates to use Tailscale IP address
- **Network restrictions**: UDP4/UDP6 only on Tailscale network
- **Optional STUN**: stun.l.google.com for initial discovery (though not required with Tailscale)

### 3. Connection Flow
1. Application verifies Tailscale connection has a valid IP address
2. WebRTC ICE gathering restricted to Tailscale interface
3. Host candidates advertise Tailscale IP only
4. Peer-to-peer connections established over Tailscale network
5. Media streams flow directly between devices via WireGuard tunnel
6. Tailscale DERP servers provide relay fallback if needed

## 🌟 Benefits

### Performance
- **Direct connections** only - no relay overhead
- **Lower latency** within Tailscale mesh network
- **Higher bandwidth** without intermediary servers
- **Consistent video quality** over secure tunnels

### Security
- **WireGuard encryption** for all traffic end-to-end
- **No exposed ports** on public internet
- **Device authentication** through Tailscale identity
- **Network isolation** - only tailnet members can connect

### Simplicity
- **No TURN server** to configure, run, or maintain
- **No firewall rules** or port forwarding needed
- **Automatic NAT traversal** handled by Tailscale
- **External provisioning** - Tailscale managed outside application

### Reliability
- **Fail-fast** with clear error messages
- **Status monitoring** via Tailscale CLI
- **DERP relay fallback** provided by Tailscale infrastructure
- **Resilient** to network changes (Tailscale handles reconnection)

## 🔍 Monitoring and Debugging

### Check Tailscale Status
```bash
tailscale status
tailscale ip -4  # Get your Tailscale IP
tailscale ping <peer-hostname>  # Test connectivity
```

### View Logs
```bash
# Tailscale logs
sudo journalctl -u tailscaled -f

# Application logs (check terminal output)
```

### Network Diagnostics
The web interface shows:
- Current network type (Tailscale/Traditional)
- Connected Tailscale peers
- Connection quality indicators
- Real-time debug messages

### Debug URLs
- `/tailscale/status` - JSON status of Tailscale integration
- `/tailscale/peers` - List of available Tailscale peers

## 🚨 Troubleshooting

### Common Issues

#### 1. Application Won't Start - Tailscale Not Connected
**Symptoms**: Application exits with error "tailscale is not running"
**Solution**:
```bash
# Check Tailscale status
tailscale status

# If not running, start it
sudo tailscale up --hostname=your-device-name

# Verify you have an IP
tailscale ip -4
```

**Note**: The application will NOT start without Tailscale. This is by design.

#### 2. Application Won't Start - No Tailnet IP
**Symptoms**: Error "tailscale not connected (no tailnet IP)"
**Solution**:
- Ensure Tailscale is fully connected: `tailscale status` should show "Running"
- Check your account is authenticated
- Verify you're connected to a tailnet

#### 3. Can't Reach Peers
**Symptoms**: WebRTC connection timeouts
**Solution**:
- Check if peers are online: `tailscale status`
- Test connectivity: `tailscale ping <peer-hostname>`
- Verify both devices are on same tailnet
- Check Tailscale ACLs don't block traffic

#### 4. Video Quality Issues
**Symptoms**: Poor video quality or stuttering
**Solution**:
- Verify direct connection: Check WebRTC stats in browser (chrome://webrtc-internals/)
- Look for "host" candidates with Tailscale IPs (100.x.x.x)
- Monitor Tailscale network: `tailscale web` for dashboard
- Check for DERP relay usage (indicates no direct path)

#### 5. Front-End Shows "Tailscale Not Detected"
**Symptoms**: Red error message in web interface
**Solution**:
- This is expected behavior when Tailscale not connected
- Connect to Tailscale and reload the page
- There is no traditional networking fallback

### Advanced Debugging

#### Enable Debug Mode
```bash
# Go application
go run cmd/security-camera/main.go -debug -addr localhost:3000

# Node.js server  
DEBUG=* node server-tailscale.js
```

#### WebRTC Statistics
Access WebRTC stats in browser:
1. Open browser dev tools
2. Go to `chrome://webrtc-internals/` (Chrome) or `about:webrtc` (Firefox)
3. Look for connection type and candidate pairs

## 🔒 Security Considerations

### Network Isolation
- Tailscale creates isolated overlay network
- Devices only accessible to authorized Tailscale users
- No exposure to public internet required

### Access Control
- Use Tailscale ACLs to restrict device access
- Configure user/device permissions in Tailscale admin console
- Consider using Tailscale SSH for secure device management

### Key Management
- Store Tailscale auth keys securely
- Use short-lived auth keys for deployment
- Regularly rotate authentication credentials

## 🚀 Deployment Options

### Single Device Setup
1. Install Tailscale on camera device
2. Run setup script
3. Access via Tailscale IP from any connected device

### Multi-Device Setup  
1. Install Tailscale on all devices (cameras, viewers)
2. Run setup on each camera device
3. Use room-based separation for multiple cameras

### Cloud Integration
- Deploy on cloud instances with Tailscale
- Use Tailscale subnet routing for hybrid setups
- Integrate with existing Tailscale infrastructure

## 📊 Performance Optimization

### Network Settings
```bash
# Optimize for real-time media
echo 'net.core.rmem_max = 134217728' | sudo tee -a /etc/sysctl.conf
echo 'net.core.wmem_max = 134217728' | sudo tee -a /etc/sysctl.conf
sudo sysctl -p
```

### Application Tuning
- Increase ICE candidate pool size for faster connections
- Adjust media constraints based on network type
- Use hardware acceleration where available

### Monitoring
- Set up Tailscale monitoring with Prometheus/Grafana
- Monitor WebRTC statistics
- Track connection success rates

## 🛠️ Development

### Building Custom Features
The Tailscale integration is designed to be extensible:

```go
// Add custom Tailscale features
type CustomTailscaleManager struct {
    *tailscale.TailscaleManager
    // Add custom fields
}

func (ctm *CustomTailscaleManager) CustomFeature() {
    // Implement custom functionality
}
```

### Testing
```bash
# Test Go components
go test ./internal/tailscale/...

# Test Node.js components  
npm test

# Integration testing
./test-tailscale-integration.sh
```

## 📞 Support

### Resources
- [Tailscale Documentation](https://tailscale.com/kb/)
- [WebRTC Troubleshooting](https://webrtc.org/getting-started/troubleshooting)
- [ion-sfu Documentation](https://github.com/pion/ion-sfu)

### Community
- [Tailscale Slack](https://tailscale.com/contact/support/)
- [WebRTC Discord](https://discord.gg/webrtc)

---

## 🔄 Migrating from TURN

If you were using the previous TURN-based implementation, follow these steps:

### 1. Install and Configure Tailscale
```bash
# Install Tailscale
brew install tailscale  # macOS
# or
curl -fsSL https://tailscale.com/install.sh | sh  # Linux

# Connect to your tailnet
sudo tailscale up --hostname=webcam-security

# Verify connection
tailscale status
tailscale ip -4
```

### 2. Update Configuration

In your config file, ensure:
```yaml
TailscaleConfig:
  Enabled: true
  NodeName: "webcam-security"
  # AuthKey optional for production
```

### 3. Remove TURN References

The following have been removed and are no longer needed:
- **stun_server.go** - Deleted completely
- **TURN server environment variables** - No longer read
- **WebRTC TURN credentials** - Not used
- **Port 3478** - TURN port no longer needed

### 4. Update SFU Configuration

If using custom ion-sfu config, ensure:
```toml
[turn]
enabled = false  # MUST be false
```

### 5. Test Connection

1. Start the application
2. Verify it detects your Tailscale IP in logs
3. Access web interface via Tailscale IP
4. Check browser console shows Tailscale networking active

### Common Migration Issues

**Old config still has `TailscaleConfig.Enabled = false`**
- Update to `true` - this is now required

**Application tries to start TURN server**
- Pull latest code - TURN code has been deleted

**Front-end shows traditional networking**
- Clear browser cache and reload
- Ensure using `index-tailscale.html`

---

## 📝 License

This Tailscale integration maintains the same license as the main project. See LICENSE file for details.

---

## 📚 Additional Resources

- **TAILSCALE_INTEGRATION_TODO.md** - Detailed implementation plan (reference)
- **configs/sfu.toml** - ion-sfu configuration with TURN disabled
- **internal/tailscale/tailscale.go** - Tailscale manager implementation
- **internal/rtcManager/manager.go** - WebRTC manager with Tailscale-only setup

---

*This system enforces Tailscale-only networking for maximum security and simplicity. All WebRTC traffic flows exclusively over your private tailnet with WireGuard encryption.* 🔒