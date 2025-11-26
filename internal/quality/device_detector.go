// Package quality provides device capability detection
package quality

import (
	"log"
	"runtime"
)

// DetectDeviceCapability attempts to detect device tier based on available information
func DetectDeviceCapability(maxWidth, maxHeight, maxFPS int, hasHWEncoder bool) DeviceCapability {
	tier := detectTier(maxWidth, maxHeight, maxFPS, hasHWEncoder)

	return DeviceCapability{
		Tier:                   tier,
		HardwareEncoder:        hasHWEncoder,
		MaxResolution:          Resolution{Width: maxWidth, Height: maxHeight},
		MaxFrameRate:           maxFPS,
		SupportsDynamicBitrate: true, // GStreamer supports dynamic bitrate
	}
}

// detectTier determines device tier based on resolution capability and hardware
func detectTier(maxWidth, maxHeight, maxFPS int, hasHWEncoder bool) DeviceTier {
	pixels := maxWidth * maxHeight

	// High tier: 4K capable with hardware encoder
	if pixels >= 3840*2160 && maxFPS >= 30 && hasHWEncoder {
		log.Printf("[DeviceDetector] Detected HIGH tier: %dx%d@%d with HW encoder", maxWidth, maxHeight, maxFPS)
		return DeviceTierHigh
	}

	// Medium tier: 1080p+ capable or has hardware encoder
	if pixels >= 1920*1080 && (maxFPS >= 30 || hasHWEncoder) {
		log.Printf("[DeviceDetector] Detected MEDIUM tier: %dx%d@%d (HW encoder: %v)", maxWidth, maxHeight, maxFPS, hasHWEncoder)
		return DeviceTierMedium
	}

	// Low tier: Everything else
	log.Printf("[DeviceDetector] Detected LOW tier: %dx%d@%d (HW encoder: %v)", maxWidth, maxHeight, maxFPS, hasHWEncoder)
	return DeviceTierLow
}

// DetectHardwareEncoder checks if hardware encoder is likely available
func DetectHardwareEncoder() bool {
	// Simple heuristic based on OS
	switch runtime.GOOS {
	case "darwin":
		// macOS has VideoToolbox
		return true
	case "linux":
		// Linux might have VAAPI or NVENC (harder to detect without probing)
		// Conservative: assume software encoding unless we can verify
		return false
	case "windows":
		// Windows might have NVENC or QuickSync
		// Conservative: assume software encoding
		return false
	default:
		return false
	}
}
