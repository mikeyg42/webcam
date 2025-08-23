# 🚀 WebRTC Security Camera Setup Guide

This guide will help you get the security camera system running quickly and securely.

## 📋 Prerequisites

- **Go 1.19+** - [Download here](https://golang.org/dl/)
- **Node.js 16+** - [Download here](https://nodejs.org/)
- **Docker** - [Download here](https://www.docker.com/get-started)
- **OpenCV** (for Go bindings) - See [installation guide](#opencv-installation)

## ⚡ Quick Start

### 1. Clone and Setup Environment

```bash
git clone <your-repo-url>
cd webcam
cp .env.example .env
```

### 2. Configure Your Settings

Edit the `.env` file with your preferences. **For basic local testing, you only need to:**

```bash
# Basic setup - just change these:
WEBRTC_USERNAME=camera_user
WEBRTC_PASSWORD=your-secure-password-here

# Optional - for email notifications:
MAILSLURP_API_KEY=your-actual-mailslurp-key
NOTIFICATION_EMAIL=your-email@gmail.com
```

### 3. Install Dependencies

```bash
# Node.js dependencies
npm install

# Go dependencies (automatically downloaded)
go mod tidy
```

### 4. Start the System

```bash
# Terminal 1: Start the media server and proxy
./startServers.sh

# Terminal 2: Start the camera application  
go run cmd/security-camera/main.go
```

### 5. Access the Interface

Open your browser to: **http://localhost:3000**

## 🔧 Detailed Configuration

### Required Configuration

| Variable | Required | Description | Example |
|----------|----------|-------------|---------|
| `WEBRTC_USERNAME` | ✅ | Username for WebRTC auth (you choose this) | `camera_user` |
| `WEBRTC_PASSWORD` | ✅ | Password for WebRTC auth (you choose this) | `mySecurePassword123` |

### Optional Configuration

| Variable | Optional | Description | Default |
|----------|----------|-------------|---------|
| `MAILSLURP_API_KEY` | ⚠️ | API key for email notifications | `your-mailslurp-api-key-here` |
| `NOTIFICATION_EMAIL` | ⚠️ | Email to receive motion alerts | `your-email@example.com` |
| `PORT` | ✅ | Web server port | `3000` |
| `HOST` | ✅ | Server bind address | `localhost` |

### Advanced Configuration (Tailscale)

| Variable | Advanced | Description | Default |
|----------|----------|-------------|---------|
| `TAILSCALE_ENABLED` | ✅ | Enable mesh networking | `false` |
| `TAILSCALE_AUTH_KEY` | ⚠️ | Tailscale authentication key | `""` |

## 🛠️ Common Configurations

### 1. Local Testing (Default)
```bash
PORT=3000
HOST=localhost
TAILSCALE_ENABLED=false
```
**Access**: http://localhost:3000

### 2. Network Access (Same WiFi)
```bash
PORT=3000
HOST=0.0.0.0
TAILSCALE_ENABLED=false
```
**Access**: http://YOUR_IP_ADDRESS:3000

### 3. Tailscale Mesh Network (Advanced)
```bash
PORT=3000
HOST=0.0.0.0
TAILSCALE_ENABLED=true
```
**Access**: http://TAILSCALE_IP:3000 (from any Tailscale device)

## 📧 Email Notifications Setup

### Option 1: MailSlurp (Recommended)

1. Sign up at [MailSlurp](https://app.mailslurp.com/dashboard/)
2. Get your API key from the dashboard
3. Create an inbox and get the inbox ID
4. Add to `.env`:
```bash
MAILSLURP_API_KEY=your-actual-api-key-here
MAILSLURP_INBOX_ID=your-inbox-id-here
NOTIFICATION_EMAIL=your-email@gmail.com
```

### Option 2: Disable Notifications
Leave the MailSlurp settings as placeholders - the system will work without email notifications.

## 🔗 Tailscale Setup (Advanced)

Tailscale provides secure, direct connections without port forwarding.

### 1. Install Tailscale
```bash
curl -fsSL https://tailscale.com/install.sh | sh
```

### 2. Automated Setup
```bash
./setup-tailscale.sh
```

### 3. Manual Setup
```bash
sudo tailscale up --hostname=webcam-security
```

### 4. Enable in Configuration
```bash
# In .env file:
TAILSCALE_ENABLED=true
```

## 🐳 Docker Deployment

### Build and Run
```bash
docker-compose up -d
```

### Custom Configuration
```bash
# Copy environment file
cp .env.example .env
# Edit .env with your settings
docker-compose up -d
```

## 🔍 Troubleshooting

### Common Issues

#### 1. "Permission denied" errors
```bash
# Make scripts executable
chmod +x startServers.sh
chmod +x setup-tailscale.sh
```

#### 2. OpenCV not found
See [OpenCV Installation](#opencv-installation) below.

#### 3. Port already in use
```bash
# Change port in .env
PORT=3001
```

#### 4. Can't access from other devices
```bash
# Change host binding in .env
HOST=0.0.0.0
```

#### 5. WebRTC connection fails
- Check firewall settings
- Verify TURN server is running
- Consider using Tailscale for better NAT traversal

### Debug Mode
```bash
# Enable verbose logging
DEBUG=true

# Run with debug flag
go run cmd/security-camera/main.go -debug
```

## 📦 OpenCV Installation

### Ubuntu/Debian
```bash
sudo apt update
sudo apt install libopencv-dev pkg-config
```

### macOS
```bash
brew install opencv pkg-config
```

### Windows
1. Download OpenCV from [opencv.org](https://opencv.org/releases/)
2. Extract and add to PATH
3. Set `CGO_CPPFLAGS` and `CGO_LDFLAGS` environment variables

## 🔒 Security Best Practices

### 1. Strong Passwords
```bash
# Use strong WebRTC credentials
WEBRTC_PASSWORD=GenerateAStrongPasswordHere123!
```

### 2. Network Security
```bash
# For internet access, restrict CORS
CORS_ORIGIN=https://yourdomain.com
```

### 3. Environment Variables
- Never commit `.env` to git
- Use different credentials for production
- Rotate API keys regularly

### 4. Tailscale Security
- Use Tailscale ACLs to restrict access
- Enable MFA on Tailscale account
- Use short-lived auth keys

## 📁 Project Structure

```
webcam/
├── .env                 # Your configuration (not in git)
├── .env.example         # Template configuration
├── SETUP.md            # This file
├── cmd/
│   └── security-camera/ # Main Go application
├── internal/           # Go packages
├── public/            # Web interface
├── server.js          # Node.js proxy server
├── configs/           # ion-sfu configuration
└── startServers.sh    # Server startup script
```

## 🚀 Production Deployment

### 1. Environment Setup
```bash
# Production environment variables
PORT=80
HOST=0.0.0.0
DEBUG=false
CORS_ORIGIN=https://yourdomain.com
```

### 2. Process Management
```bash
# Using PM2 for Node.js
npm install -g pm2
pm2 start server.js --name webcam-server

# Using systemd for Go app
# Create service file in /etc/systemd/system/
```

### 3. SSL/TLS
- Use Let's Encrypt for HTTPS
- Configure reverse proxy (nginx/Apache)
- Update WebRTC configuration for HTTPS

### 4. Monitoring
- Set up log aggregation
- Monitor system resources  
- Configure health checks

## 🆘 Support

### Getting Help
1. Check this setup guide
2. Review the [Troubleshooting](#troubleshooting) section
3. Check [TAILSCALE_README.md](TAILSCALE_README.md) for Tailscale-specific issues
4. Open an issue on GitHub

### Common Questions
- **Q**: Do I need Tailscale?
  **A**: No, it's optional. The system works great with traditional networking.

- **Q**: Can I use this without email notifications?
  **A**: Yes, just leave the MailSlurp settings as placeholders.

- **Q**: How do I access from my phone?
  **A**: Set `HOST=0.0.0.0` and access via http://YOUR_COMPUTER_IP:3000

---

🎉 **You're all set!** Your WebRTC security camera system should now be running. Enjoy secure, high-quality video streaming!