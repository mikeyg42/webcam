//go:build darwin
// +build darwin

package permissions

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AVFoundation -framework Foundation
#include "avfoundation_permissions.h"
*/
import "C"
import (
	"fmt"
	"log"
)

// AuthorizationStatus represents the current permission state
type AuthorizationStatus int

const (
	NotDetermined AuthorizationStatus = 0
	Restricted    AuthorizationStatus = 1
	Denied        AuthorizationStatus = 2
	Authorized    AuthorizationStatus = 3
)

func (s AuthorizationStatus) String() string {
	switch s {
	case NotDetermined:
		return "Not Determined"
	case Restricted:
		return "Restricted"
	case Denied:
		return "Denied"
	case Authorized:
		return "Authorized"
	default:
		return fmt.Sprintf("Unknown(%d)", s)
	}
}

// CheckCameraAuthorization returns the current camera permission status
func CheckCameraAuthorization() AuthorizationStatus {
	status := C.CheckCameraAuthorizationStatus()
	return AuthorizationStatus(status)
}

// CheckMicrophoneAuthorization returns the current microphone permission status
func CheckMicrophoneAuthorization() AuthorizationStatus {
	status := C.CheckMicrophoneAuthorizationStatus()
	return AuthorizationStatus(status)
}

// RequestCameraPermission requests camera access and waits for user response
// Returns true if granted, false if denied, error on timeout
func RequestCameraPermission() (bool, error) {
	log.Println("Requesting camera permission...")
	result := C.RequestCameraAccess()
	
	switch result {
	case 1:
		log.Println("Camera permission granted")
		return true, nil
	case 0:
		log.Println("Camera permission denied")
		return false, nil
	case -1:
		return false, fmt.Errorf("camera permission request timed out")
	default:
		return false, fmt.Errorf("unexpected result from camera permission request: %d", result)
	}
}

// RequestMicrophonePermission requests microphone access and waits for user response
// Returns true if granted, false if denied, error on timeout
func RequestMicrophonePermission() (bool, error) {
	log.Println("Requesting microphone permission...")
	result := C.RequestMicrophoneAccess()
	
	switch result {
	case 1:
		log.Println("Microphone permission granted")
		return true, nil
	case 0:
		log.Println("Microphone permission denied")
		return false, nil
	case -1:
		return false, fmt.Errorf("microphone permission request timed out")
	default:
		return false, fmt.Errorf("unexpected result from microphone permission request: %d", result)
	}
}

// EnsureMediaPermissions checks and requests camera/microphone permissions
// Returns an error if permissions are denied or cannot be obtained
func EnsureMediaPermissions() error {
	// Check camera permission
	cameraStatus := CheckCameraAuthorization()
	log.Printf("Camera authorization status: %s", cameraStatus)
	
	if cameraStatus == NotDetermined {
		granted, err := RequestCameraPermission()
		if err != nil {
			return fmt.Errorf("failed to request camera permission: %w", err)
		}
		if !granted {
			return fmt.Errorf("camera permission denied by user")
		}
	} else if cameraStatus != Authorized {
		return fmt.Errorf("camera access %s - please grant permission in System Settings > Privacy & Security > Camera", cameraStatus)
	}
	
	// Check microphone permission
	micStatus := CheckMicrophoneAuthorization()
	log.Printf("Microphone authorization status: %s", micStatus)
	
	if micStatus == NotDetermined {
		granted, err := RequestMicrophonePermission()
		if err != nil {
			return fmt.Errorf("failed to request microphone permission: %w", err)
		}
		if !granted {
			return fmt.Errorf("microphone permission denied by user")
		}
	} else if micStatus != Authorized {
		return fmt.Errorf("microphone access %s - please grant permission in System Settings > Privacy & Security > Microphone", micStatus)
	}
	
	log.Println("All media permissions granted")
	return nil
}
