package main

import (
	"fmt"
	"log"

	"github.com/pion/mediadevices"
	_ "github.com/pion/mediadevices/pkg/driver/camera"
)

func main() {
	log.Println("Enumerating available media devices...")

	devices := mediadevices.EnumerateDevices()

	fmt.Println("\n========== AVAILABLE CAMERAS ==========")
	cameraCount := 0
	for _, device := range devices {
		if device.Kind == mediadevices.VideoInput {
			cameraCount++
			fmt.Printf("\nCamera %d:\n", cameraCount)
			fmt.Printf("  Device ID: %s\n", device.DeviceID)
			fmt.Printf("  Label:     %s\n", device.Label)
			fmt.Printf("  Kind:      %s\n", device.Kind)
		}
	}

	if cameraCount == 0 {
		fmt.Println("No cameras found!")
	} else {
		fmt.Printf("\nTotal cameras found: %d\n", cameraCount)
	}

	fmt.Println("\n========== ALL DEVICES ==========")
	for i, device := range devices {
		fmt.Printf("%d. ID: %s | Label: %s | Kind: %s\n",
			i+1, device.DeviceID, device.Label, device.Kind)
	}
}
