package quality

import "fmt"

// GetQualityProfiles returns all available quality profiles
// Profiles are ordered from highest to lowest quality
// Bitrate ranges based on user requirements:
// - 4K @ 30fps: 4500-6000 Kbps (drop 500 Kbps for 24fps)
// - 1080p @ 30fps: 3500-5000 Kbps (drop 500 Kbps for 24fps)
// - 720p @ 30fps: 2500-4000 Kbps (drop 500 Kbps for 24fps)
func GetQualityProfiles() []QualityProfile {
	return []QualityProfile{
		// 4K Profiles (High-end devices only)
		{
			Name:         "4K@30",
			Resolution:   Resolution{Width: 3840, Height: 2160},
			FrameRate:    30,
			BitrateRange: BitrateRange{Min: 4500, Max: 6000},
			MinDeviceTier: DeviceTierHigh,
		},
		{
			Name:         "4K@24",
			Resolution:   Resolution{Width: 3840, Height: 2160},
			FrameRate:    24,
			BitrateRange: BitrateRange{Min: 4000, Max: 5500},
			MinDeviceTier: DeviceTierHigh,
		},

		// 1080p Profiles (Mid-range and up)
		{
			Name:         "1080p@30",
			Resolution:   Resolution{Width: 1920, Height: 1080},
			FrameRate:    30,
			BitrateRange: BitrateRange{Min: 3500, Max: 5000},
			MinDeviceTier: DeviceTierMedium,
		},
		{
			Name:         "1080p@24",
			Resolution:   Resolution{Width: 1920, Height: 1080},
			FrameRate:    24,
			BitrateRange: BitrateRange{Min: 3000, Max: 4500},
			MinDeviceTier: DeviceTierMedium,
		},

		// 720p Profiles (Universal compatibility)
		{
			Name:         "720p@30",
			Resolution:   Resolution{Width: 1280, Height: 720},
			FrameRate:    30,
			BitrateRange: BitrateRange{Min: 2500, Max: 4000},
			MinDeviceTier: DeviceTierLow,
		},
		{
			Name:         "720p@24",
			Resolution:   Resolution{Width: 1280, Height: 720},
			FrameRate:    24,
			BitrateRange: BitrateRange{Min: 2000, Max: 3500},
			MinDeviceTier: DeviceTierLow,
		},

		// 480p Profiles (Fallback for very poor networks)
		{
			Name:         "480p@30",
			Resolution:   Resolution{Width: 854, Height: 480},
			FrameRate:    30,
			BitrateRange: BitrateRange{Min: 1500, Max: 2500},
			MinDeviceTier: DeviceTierLow,
		},
		{
			Name:         "480p@24",
			Resolution:   Resolution{Width: 854, Height: 480},
			FrameRate:    24,
			BitrateRange: BitrateRange{Min: 1200, Max: 2000},
			MinDeviceTier: DeviceTierLow,
		},

		// Emergency fallback (critical network conditions)
		{
			Name:         "360p@20",
			Resolution:   Resolution{Width: 640, Height: 360},
			FrameRate:    20,
			BitrateRange: BitrateRange{Min: 500, Max: 1500},
			MinDeviceTier: DeviceTierLow,
		},
	}
}

// GetProfileByName finds a profile by name
func GetProfileByName(name string) *QualityProfile {
	profiles := GetQualityProfiles()
	for i := range profiles {
		if profiles[i].Name == name {
			return &profiles[i]
		}
	}
	return nil
}

// GetProfileForResolution finds the best profile for a given resolution and framerate
// This handles the "custom" resolution case by finding the closest standard profile
// and using its bitrate ranges as guidance
func GetProfileForResolution(width, height, framerate int) *QualityProfile {
	profiles := GetQualityProfiles()

	// Find exact match first
	for i := range profiles {
		p := &profiles[i]
		if p.Resolution.Width == width &&
		   p.Resolution.Height == height &&
		   p.FrameRate == framerate {
			return p
		}
	}

	// Custom resolution - find closest match by pixel count and aspect ratio
	return findClosestProfile(width, height, framerate, profiles)
}

// findClosestProfile intelligently matches custom resolutions to nearest standard profile
// Uses weighted distance metric considering:
// - Pixel count difference (primary)
// - Aspect ratio similarity (secondary)
// - Framerate difference (tertiary)
func findClosestProfile(width, height, framerate int, profiles []QualityProfile) *QualityProfile {
	var closest *QualityProfile
	minScore := float64(999999999)

	targetPixels := width * height
	targetAspect := float64(width) / float64(height)

	for i := range profiles {
		p := &profiles[i]
		profilePixels := p.Resolution.Pixels()
		profileAspect := float64(p.Resolution.Width) / float64(p.Resolution.Height)

		// Calculate pixel difference (main factor)
		pixelDiff := float64(abs(profilePixels - targetPixels))
		pixelScore := pixelDiff / float64(targetPixels) // Normalized difference

		// Calculate aspect ratio difference (secondary factor)
		aspectDiff := abs(int((targetAspect - profileAspect) * 1000))
		aspectScore := float64(aspectDiff) / 1000.0

		// Calculate framerate difference (tertiary factor)
		fpsDiff := abs(p.FrameRate - framerate)
		fpsScore := float64(fpsDiff) / 30.0 // Normalize by typical max FPS

		// Weighted composite score
		// Pixel count is most important (60%), aspect ratio secondary (25%), FPS tertiary (15%)
		compositeScore := (pixelScore * 0.60) + (aspectScore * 0.25) + (fpsScore * 0.15)

		if compositeScore < minScore {
			minScore = compositeScore
			closest = p
		}
	}

	return closest
}

// CreateCustomProfile generates a profile for non-standard resolutions
// Uses the closest standard profile as a template and scales bitrate appropriately
func CreateCustomProfile(width, height, framerate int) *QualityProfile {
	// Find closest standard profile
	baseProfile := GetProfileForResolution(width, height, framerate)
	if baseProfile == nil {
		// Fallback to 720p@30
		baseProfile = GetProfileByName("720p@30")
	}

	customPixels := width * height
	basePixels := baseProfile.Resolution.Pixels()

	// Scale bitrate based on pixel count ratio
	// More pixels = proportionally higher bitrate needed
	pixelRatio := float64(customPixels) / float64(basePixels)

	// Apply sub-linear scaling (sqrt) - bitrate doesn't need to scale 1:1 with pixels
	// because encoding efficiency improves at higher resolutions
	bitrateScale := pixelRatio * 0.7 // 70% weight on linear scaling
	if pixelRatio > 1.0 {
		bitrateScale = 1.0 + ((pixelRatio - 1.0) * 0.6) // Diminishing returns for upscaling
	}

	// Adjust for framerate difference
	fpsRatio := float64(framerate) / float64(baseProfile.FrameRate)
	bitrateScale *= fpsRatio

	// Calculate scaled bitrate range
	scaledMin := int(float64(baseProfile.BitrateRange.Min) * bitrateScale)
	scaledMax := int(float64(baseProfile.BitrateRange.Max) * bitrateScale)

	// Clamp to reasonable bounds
	if scaledMin < 500 {
		scaledMin = 500
	}
	if scaledMax > 10000 {
		scaledMax = 10000
	}

	customProfile := &QualityProfile{
		Name: fmt.Sprintf("Custom %dx%d@%d", width, height, framerate),
		Resolution: Resolution{
			Width:  width,
			Height: height,
		},
		FrameRate: framerate,
		BitrateRange: BitrateRange{
			Min: scaledMin,
			Max: scaledMax,
		},
		MinDeviceTier: baseProfile.MinDeviceTier, // Inherit device tier from base
	}

	return customProfile
}

// GetNextLowerProfile returns the next profile down in quality
// Used for graceful degradation
func GetNextLowerProfile(current *QualityProfile) *QualityProfile {
	profiles := GetQualityProfiles()

	// Handle custom profiles - find where they would fit in the hierarchy
	if !isStandardProfile(current) {
		// Find the standard profile just below this custom resolution
		for i := range profiles {
			if profiles[i].Resolution.Pixels() < current.Resolution.Pixels() {
				return &profiles[i]
			}
		}
		// Already at or below lowest standard
		return &profiles[len(profiles)-1]
	}

	// Standard profile - return next in list
	for i := range profiles {
		if profiles[i].Name == current.Name {
			if i < len(profiles)-1 {
				return &profiles[i+1]
			}
			return current // Already at lowest
		}
	}

	return current
}

// GetNextHigherProfile returns the next profile up in quality
// Used for quality upgrades when network improves
func GetNextHigherProfile(current *QualityProfile, maxDeviceTier DeviceTier) *QualityProfile {
	profiles := GetQualityProfiles()

	// Handle custom profiles
	if !isStandardProfile(current) {
		// Find the standard profile just above this custom resolution
		for i := len(profiles) - 1; i >= 0; i-- {
			if profiles[i].Resolution.Pixels() > current.Resolution.Pixels() &&
			   profiles[i].MinDeviceTier <= maxDeviceTier {
				return &profiles[i]
			}
		}
		return current // Already at or above highest compatible
	}

	// Standard profile
	for i := range profiles {
		if profiles[i].Name == current.Name {
			if i > 0 {
				candidate := &profiles[i-1]
				if candidate.MinDeviceTier <= maxDeviceTier {
					return candidate
				}
			}
			return current // Already at highest or device can't handle higher
		}
	}

	return current
}

// isStandardProfile checks if a profile is a standard defined profile (not custom)
func isStandardProfile(profile *QualityProfile) bool {
	profiles := GetQualityProfiles()
	for i := range profiles {
		if profiles[i].Name == profile.Name {
			return true
		}
	}
	return false
}

// ValidateCustomResolution checks if custom resolution is reasonable
func ValidateCustomResolution(width, height, framerate int) error {
	// Minimum bounds
	if width < 320 || height < 240 {
		return fmt.Errorf("resolution too small: minimum 320x240")
	}

	// Maximum bounds (8K)
	if width > 7680 || height > 4320 {
		return fmt.Errorf("resolution too large: maximum 7680x4320")
	}

	// Framerate bounds
	if framerate < 1 || framerate > 60 {
		return fmt.Errorf("framerate out of range: must be 1-60 fps")
	}

	// Check for reasonable aspect ratios (prevent extreme values)
	aspectRatio := float64(width) / float64(height)
	if aspectRatio < 0.5 || aspectRatio > 3.0 {
		return fmt.Errorf("unusual aspect ratio: %.2f (expected 0.5-3.0)", aspectRatio)
	}

	return nil
}

