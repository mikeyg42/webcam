#!/usr/bin/env bash
set -euo pipefail

echo "Building macOS App Bundle for Security Camera..."

# Build the Go binary
echo "Building Go binary..."
go build -o security-camera ./cmd/security-camera

# Create app bundle structure
APP_NAME="SecurityCamera.app"
APP_DIR="$APP_NAME/Contents"
rm -rf "$APP_NAME"
mkdir -p "$APP_DIR/MacOS"
mkdir -p "$APP_DIR/Resources"

# Copy binary
echo "Creating app bundle..."
cp security-camera "$APP_DIR/MacOS/"

# Copy Info.plist
cp Info.plist "$APP_DIR/"

echo ""
echo "App bundle created: $APP_NAME"
echo ""
echo "To trigger camera/microphone permission dialogs, run:"
echo "  open $APP_NAME"
echo ""
echo "Or double-click $APP_NAME in Finder"
