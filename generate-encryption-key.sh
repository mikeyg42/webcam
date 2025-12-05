#!/usr/bin/env bash
# Generate a secure encryption key for credential storage

echo "========================================"
echo "Credential Encryption Key Generator"
echo "========================================"
echo ""

# Generate a secure random key using openssl
KEY=$(openssl rand -base64 32)

echo "Generated encryption key:"
echo ""
echo "  CREDENTIALS_ENCRYPTION_KEY=\"$KEY\""
echo ""
echo "========================================"
echo ""
echo "IMPORTANT: Save this key securely!"
echo ""
echo "Add this to your environment:"
echo ""
echo "  export CREDENTIALS_ENCRYPTION_KEY=\"$KEY\""
echo ""
echo "Or add to your ~/.bashrc / ~/.zshrc:"
echo ""
echo "  echo 'export CREDENTIALS_ENCRYPTION_KEY=\"$KEY\"' >> ~/.zshrc"
echo ""
echo "For production (systemd service):"
echo ""
echo "  1. Create file: /etc/webcam2/credentials.env"
echo "  2. Add line: CREDENTIALS_ENCRYPTION_KEY=\"$KEY\""
echo "  3. Set permissions: chmod 600 /etc/webcam2/credentials.env"
echo "  4. Reference in systemd: EnvironmentFile=/etc/webcam2/credentials.env"
echo ""
echo "========================================"
