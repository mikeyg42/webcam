#!/bin/bash

# Tailscale WebRTC Security Camera Setup Script
# This script helps configure the security camera system to use Tailscale networking

set -e

echo "🔗 Tailscale WebRTC Security Camera Setup"
echo "==========================================="

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Function to print colored output
print_status() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Check if running as root
if [[ $EUID -eq 0 ]]; then
    print_error "This script should not be run as root"
    exit 1
fi

# Check if Tailscale is installed
check_tailscale_installation() {
    print_status "Checking Tailscale installation..."
    
    if command -v tailscale &> /dev/null; then
        print_success "Tailscale is installed"
        tailscale version
        return 0
    else
        print_warning "Tailscale is not installed"
        return 1
    fi
}

# Install Tailscale
install_tailscale() {
    print_status "Installing Tailscale..."
    
    # Add Tailscale's package signing key and repository
    curl -fsSL https://pkgs.tailscale.com/stable/ubuntu/noble.noarmor.gpg | sudo tee /usr/share/keyrings/tailscale-archive-keyring.gpg >/dev/null
    curl -fsSL https://pkgs.tailscale.com/stable/ubuntu/noble.tailscale-keyring.list | sudo tee /etc/apt/sources.list.d/tailscale.list

    # Install Tailscale
    sudo apt update && sudo apt install -y tailscale
    
    print_success "Tailscale installed successfully"
}

# Configure Tailscale
configure_tailscale() {
    print_status "Configuring Tailscale..."
    
    # Check if already connected
    if tailscale status &> /dev/null; then
        print_success "Tailscale is already configured and connected"
        tailscale status
        return 0
    fi
    
    # Get node name
    read -p "Enter a name for this device (default: webcam-security): " NODE_NAME
    NODE_NAME=${NODE_NAME:-webcam-security}
    
    print_status "Starting Tailscale with hostname: $NODE_NAME"
    print_warning "You will need to authenticate via browser or provide an auth key"
    
    # Start Tailscale
    sudo tailscale up --hostname="$NODE_NAME"
    
    if [ $? -eq 0 ]; then
        print_success "Tailscale configured successfully"
        print_status "Current Tailscale status:"
        tailscale status
    else
        print_error "Failed to configure Tailscale"
        return 1
    fi
}

# Create environment configuration
create_env_config() {
    print_status "Creating environment configuration..."
    
    # Get Tailscale info
    local tailscale_ip=$(tailscale ip -4 2>/dev/null)
    local hostname=$(hostname)
    
    # Create .env file
    cat > .env << EOF
# Tailscale Configuration
TAILSCALE_ENABLED=true
TAILSCALE_NODE_NAME=$hostname-webcam
TAILSCALE_HOSTNAME=$tailscale_ip
TAILSCALE_LISTEN_PORT=41641

# Server Configuration
PORT=3000
HOST=0.0.0.0
ION_SFU_URL=ws://localhost:7000/ws

# CORS Configuration (allow Tailscale IPs)
CORS_ORIGIN=*

# WebRTC Configuration
WEBRTC_USE_TAILSCALE=true
EOF
    
    print_success "Environment configuration created in .env"
    print_status "Your Tailscale IP: $tailscale_ip"
}

# Update ion-sfu configuration for Tailscale
update_sfu_config() {
    print_status "Updating ion-sfu configuration for Tailscale..."
    
    if [ ! -f "configs/sfu.toml" ]; then
        print_warning "ion-sfu config not found, skipping SFU configuration"
        return 1
    fi
    
    # Backup original config
    cp configs/sfu.toml configs/sfu.toml.backup
    
    # Get Tailscale IP
    local tailscale_ip=$(tailscale ip -4 2>/dev/null)
    
    # Update config to use Tailscale IP
    if [ ! -z "$tailscale_ip" ]; then
        print_status "Configuring SFU to use Tailscale IP: $tailscale_ip"
        
        # Update TURN server configuration
        sed -i "s/urls = \[\"turn:.*\"\]/urls = [\"turn:$tailscale_ip:3478\"]/" configs/sfu.toml
        
        print_success "ion-sfu configuration updated"
    else
        print_warning "Could not get Tailscale IP, manual configuration may be needed"
    fi
}

# Create startup scripts
create_startup_scripts() {
    print_status "Creating Tailscale-enabled startup scripts..."
    
    # Create enhanced server startup script
    cat > start-tailscale-server.sh << 'EOF'
#!/bin/bash

echo "🔗 Starting Tailscale-enhanced WebRTC Security Camera System"

# Load environment variables
if [ -f .env ]; then
    export $(grep -v '^#' .env | xargs)
fi

# Check Tailscale status
if ! tailscale status &> /dev/null; then
    echo "⚠️  Tailscale not connected. Please run: sudo tailscale up"
    exit 1
fi

echo "✅ Tailscale is connected"
tailscale ip -4

# Start services
echo "📡 Starting ion-sfu..."
docker run -d --name ion-sfu \
    -p 7000:7000 \
    -p 5000-5200:5000-5200/udp \
    -p 3478:3478/udp \
    -p 49152-65535:49152-65535/udp \
    -v $(pwd)/configs:/configs \
    pionwebrtc/ion-sfu:latest-jsonrpc \
    -c /configs/sfu.toml || echo "ion-sfu already running or failed to start"

echo "🖥️  Starting Tailscale-enhanced Node.js server..."
node server-tailscale.js
EOF

    chmod +x start-tailscale-server.sh
    
    # Create Go app startup script
    cat > start-tailscale-camera.sh << 'EOF'
#!/bin/bash

echo "📹 Starting Tailscale-enhanced Go Security Camera"

# Load environment variables
if [ -f .env ]; then
    export $(grep -v '^#' .env | xargs)
fi

# Check Tailscale status
if ! tailscale status &> /dev/null; then
    echo "⚠️  Tailscale not connected. Please run: sudo tailscale up"
    exit 1
fi

echo "✅ Tailscale is connected - IP: $(tailscale ip -4)"

# Start Go application
go run cmd/security-camera/main.go -addr localhost:3000
EOF

    chmod +x start-tailscale-camera.sh
    
    print_success "Startup scripts created"
    print_status "Use ./start-tailscale-server.sh to start the server"
    print_status "Use ./start-tailscale-camera.sh to start the camera app"
}

# Test Tailscale connectivity
test_connectivity() {
    print_status "Testing Tailscale connectivity..."
    
    local tailscale_ip=$(tailscale ip -4 2>/dev/null)
    
    if [ ! -z "$tailscale_ip" ]; then
        print_success "Local Tailscale IP: $tailscale_ip"
        
        # Test connectivity to other nodes
        print_status "Testing connectivity to Tailscale peers..."
        tailscale status | grep -E "^[0-9]" | while read line; do
            peer_ip=$(echo $line | awk '{print $1}')
            peer_name=$(echo $line | awk '{print $2}')
            
            if ping -c 1 -W 2 $peer_ip &> /dev/null; then
                print_success "✅ Can reach $peer_name ($peer_ip)"
            else
                print_warning "⚠️  Cannot reach $peer_name ($peer_ip)"
            fi
        done
    else
        print_error "Could not get Tailscale IP"
        return 1
    fi
}

# Display usage information
show_usage() {
    cat << EOF

📖 Usage Instructions:
=====================

1. Make sure all devices are connected to the same Tailscale network
2. Start the services:
   - Server: ./start-tailscale-server.sh
   - Camera: ./start-tailscale-camera.sh

3. Access the web interface:
   - Local: http://localhost:3000/index-tailscale.html
   - Remote: http://YOUR_TAILSCALE_IP:3000/index-tailscale.html

4. Configuration files:
   - Environment: .env
   - SFU Config: configs/sfu.toml
   - Server: server-tailscale.js
   - Frontend: public/index-tailscale.html

🔧 Troubleshooting:
==================
- Check Tailscale status: tailscale status
- View logs: sudo journalctl -u tailscaled
- Test connectivity: tailscale ping <peer-hostname>
- Restart Tailscale: sudo systemctl restart tailscaled

EOF
}

# Main execution
main() {
    echo "Starting Tailscale setup for WebRTC Security Camera..."
    echo
    
    # Check if Tailscale is installed
    if ! check_tailscale_installation; then
        read -p "Would you like to install Tailscale? (y/N): " install_choice
        if [[ $install_choice =~ ^[Yy]$ ]]; then
            install_tailscale
        else
            print_error "Tailscale is required for this setup"
            exit 1
        fi
    fi
    
    # Configure Tailscale
    configure_tailscale
    
    # Create configuration files
    create_env_config
    
    # Update SFU config
    update_sfu_config
    
    # Create startup scripts
    create_startup_scripts
    
    # Test connectivity
    test_connectivity
    
    # Show usage
    show_usage
    
    print_success "🎉 Tailscale setup completed successfully!"
}

# Run main function
main "$@"