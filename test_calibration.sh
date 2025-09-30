#!/bin/bash
# Test script for interactive calibration

echo "Starting security camera with interactive calibration..."
echo ""
echo "INSTRUCTIONS:"
echo "1. You will see a prompt asking you to press ENTER to start calibration"
echo "2. Make sure camera view is EMPTY (no movement)"
echo "3. Press ENTER"
echo "4. Wait 10 seconds for calibration to complete"
echo "5. You'll see a second prompt to press ENTER to begin motion detection"
echo "6. Press ENTER again"
echo "7. Motion detection will now be active!"
echo ""
echo "Press ENTER to continue..."
read

# Run the security camera
WEBRTC_PASSWORD=testing123 WEBRTC_USERNAME=testuser ./security-camera -debug -testing