package main

import (
	"fmt"
	"log"

	"github.com/pion/mediadevices"
	"github.com/pion/mediadevices/pkg/avfoundation"
)

func main() {
	fmt.Println("Testing AVFoundation device enumeration...")
	fmt.Println("==========================================\n")

	// Test AVFoundation directly
	fmt.Println("1. Testing AVFoundation video devices...")
	videoDevices, err := avfoundation.Devices(avfoundation.Video)
	if err != nil {
		log.Printf("ERROR: AVFoundation video devices failed: %v\n", err)
		log.Printf("This usually means:\n")
		log.Printf("  - Camera permissions not granted\n")
		log.Printf("  - No camera connected\n")
		log.Printf("  - AVFoundation framework issue\n\n")
	} else {
		fmt.Printf("SUCCESS: Found %d video devices via AVFoundation\n", len(videoDevices))
		for i, dev := range videoDevices {
			fmt.Printf("  Video Device %d: %s (UID: %s)\n", i+1, dev.Name, dev.UID)
		}
		fmt.Println()
	}

	fmt.Println("2. Testing AVFoundation audio devices...")
	audioDevices, err := avfoundation.Devices(avfoundation.Audio)
	if err != nil {
		log.Printf("ERROR: AVFoundation audio devices failed: %v\n", err)
	} else {
		fmt.Printf("SUCCESS: Found %d audio devices via AVFoundation\n", len(audioDevices))
		for i, dev := range audioDevices {
			fmt.Printf("  Audio Device %d: %s (UID: %s)\n", i+1, dev.Name, dev.UID)
		}
		fmt.Println()
	}

	// Now try mediadevices.EnumerateDevices()
	fmt.Println("3. Testing mediadevices.EnumerateDevices()...")
	devices := mediadevices.EnumerateDevices()

	if len(devices) == 0 {
		log.Println("ERROR: No devices found via mediadevices.EnumerateDevices()")
		log.Fatal("\nDiagnosis: Camera driver initialization likely panicked due to AVFoundation error")
	}

	fmt.Printf("SUCCESS: Found %d devices:\n\n", len(devices))

	for i, device := range devices {
		fmt.Printf("Device %d:\n", i+1)
		fmt.Printf("  Label:      %s\n", device.Label)
		fmt.Printf("  DeviceID:   %s\n", device.DeviceID)
		fmt.Printf("  Kind:       %v\n", device.Kind)
		fmt.Printf("  DeviceType: %v\n", device.DeviceType)

		// Check if it's camera or mic
		switch device.Kind {
		case mediadevices.VideoInput:
			fmt.Printf("  Type:       Camera\n")
		case mediadevices.AudioInput:
			fmt.Printf("  Type:       Microphone\n")
		case mediadevices.AudioOutput:
			fmt.Printf("  Type:       Audio Output\n")
		default:
			fmt.Printf("  Type:       Unknown (%v)\n", device.Kind)
		}

		fmt.Println()
	}
}
