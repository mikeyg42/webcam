package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"

	"github.com/mikeyg42/webcam/internal/api"
	"github.com/mikeyg42/webcam/internal/config"
)

func main() {
	log.Println("Testing device enumeration API endpoints...")

	// Create a config handler
	cfg := config.DefaultConfig()
	handler := api.NewConfigHandler(cfg)

	// Test /api/cameras endpoint
	fmt.Println("\n========== Testing /api/cameras ==========")
	req1 := httptest.NewRequest(http.MethodGet, "/api/cameras", nil)
	w1 := httptest.NewRecorder()
	handler.ListCameras(w1, req1)

	resp1 := w1.Result()
	fmt.Printf("Status Code: %d\n", resp1.StatusCode)

	var cameraResult map[string]interface{}
	if err := json.NewDecoder(resp1.Body).Decode(&cameraResult); err != nil {
		log.Fatalf("Failed to decode camera response: %v", err)
	}

	prettyJSON1, _ := json.MarshalIndent(cameraResult, "", "  ")
	fmt.Printf("Response:\n%s\n", string(prettyJSON1))

	// Test /api/microphones endpoint
	fmt.Println("\n========== Testing /api/microphones ==========")
	req2 := httptest.NewRequest(http.MethodGet, "/api/microphones", nil)
	w2 := httptest.NewRecorder()
	handler.ListMicrophones(w2, req2)

	resp2 := w2.Result()
	fmt.Printf("Status Code: %d\n", resp2.StatusCode)

	var micResult map[string]interface{}
	if err := json.NewDecoder(resp2.Body).Decode(&micResult); err != nil {
		log.Fatalf("Failed to decode microphone response: %v", err)
	}

	prettyJSON2, _ := json.MarshalIndent(micResult, "", "  ")
	fmt.Printf("Response:\n%s\n", string(prettyJSON2))

	fmt.Println("\n========== Tests Complete ==========")
}
