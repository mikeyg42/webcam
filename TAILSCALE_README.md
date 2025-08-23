# Tailscale WebRTC Security Camera Integration

This document describes how to use Tailscale mesh networking with your WebRTC security camera system to replace traditional TURN servers and enable secure, direct peer-to-peer connections.

## 🔗 Overview

The Tailscale integration replaces your current networking architecture with:

- **Tailscale mesh networking** instead of traditional TURN servers
- **Direct peer-to-peer connections** over the Tailscale network
- **Automatic NAT traversal** handled by Tailscale's infrastructure
- **Enhanced security** with WireGuard encryption
- **Simplified deployment** without complex port forwarding

## 📋 Architecture Changes

### Before (Traditional)
```
Camera Device ←→ TURN Server ←→ Internet ←→ Viewer
```

### After (Tailscale)
```
Camera Device ←→ Tailscale Network ←→ Viewer
```

## 🚀 Quick Start

### 1. Run the Setup Script
```bash
./setup-tailscale.sh
```

This will:
- Install Tailscale if needed
- Configure Tailscale with appropriate hostname
- Create environment configuration
- Update ion-sfu settings
- Generate startup scripts

### 2. Start the Services

**Server (Node.js proxy):**
```bash
./start-tailscale-server.sh
```

**Camera Application (Go):**
```bash
./start-tailscale-camera.sh
```

### 3. Access the Interface

- **Local access**: http://localhost:3000/index-tailscale.html
- **Remote access**: http://YOUR_TAILSCALE_IP:3000/index-tailscale.html

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

### 1. Tailscale Network Detection
- Go backend initializes `TailscaleManager` if enabled
- Node.js server queries Tailscale status via CLI
- Frontend receives network configuration from server

### 2. WebRTC Configuration Adaptation
- **With Tailscale**: Uses minimal STUN servers, relies on direct connections
- **Without Tailscale**: Falls back to traditional TURN server configuration
- ICE candidate gathering optimized for local mesh networks

### 3. Connection Flow
1. Tailscale handles NAT traversal and creates secure tunnels
2. WebRTC establishes peer connections over Tailscale network
3. Media streams flow directly between devices
4. DERP servers provide relay fallback if direct connection fails

## 🌟 Benefits

### Performance
- **Lower latency** due to direct connections
- **Higher bandwidth** without TURN relay overhead
- **Better video quality** on local networks

### Security
- **WireGuard encryption** for all traffic
- **No exposed ports** on public internet
- **Device authentication** through Tailscale

### Simplicity
- **No TURN server maintenance**
- **Automatic NAT traversal**
- **Easy multi-device setup**

### Reliability
- **Automatic reconnection** if network changes
- **DERP fallback** when direct connections fail
- **Network resilience** with multiple connection paths

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

#### 1. Tailscale Not Connected
**Symptoms**: Application falls back to traditional networking
**Solution**: 
```bash
sudo tailscale up --hostname=your-device-name
```

#### 2. Can't Reach Peers  
**Symptoms**: Connection timeouts, falling back to internet routing
**Solution**:
- Check if peers are online: `tailscale status`
- Test connectivity: `tailscale ping <peer>`
- Verify firewall settings

#### 3. Video Quality Issues
**Symptoms**: Poor video quality despite local network
**Solution**:
- Check if using Tailscale: Look for 🔗 indicator in web interface
- Verify direct connection: Check WebRTC stats in browser dev tools
- Monitor bandwidth: `tailscale web` for network dashboard

#### 4. Port Conflicts
**Symptoms**: Services fail to start
**Solution**:
- Check if ports are in use: `netstat -tulpn | grep :3000`
- Modify PORT in `.env` file
- Restart services

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

## 📝 License

This Tailscale integration maintains the same license as the main project. See LICENSE file for details.

---

*This integration transforms your security camera system into a modern, secure, mesh-networked solution. Enjoy the benefits of direct peer-to-peer connections with the security and simplicity of Tailscale!* 🎉