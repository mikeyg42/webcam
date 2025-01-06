package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// ServerManager handles the lifecycle of our servers
type ServerManager struct {
	ctx    context.Context
	cancel context.CancelFunc
	cmd    *exec.Cmd
}

// NewServerManager creates a new server manager
func NewServerManager() *ServerManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &ServerManager{
		ctx:    ctx,
		cancel: cancel,
	}
}

func (sm *ServerManager) startServers() error {
	// Get the absolute path to the script
	scriptPath, err := filepath.Abs("./startServers.sh")
	if err != nil {
		return fmt.Errorf("failed to get script path: %v", err)
	}

	// Create the command
	sm.cmd = exec.CommandContext(sm.ctx, "bash", scriptPath)

	// Connect standard output and error
	sm.cmd.Stdout = os.Stdout
	sm.cmd.Stderr = os.Stderr

	// Start the script
	log.Println("Starting servers...")
	if err := sm.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start servers: %v", err)
	}

	// Monitor the process
	go func() {
		if err := sm.cmd.Wait(); err != nil {
			if sm.ctx.Err() == nil { // Only log if not due to context cancellation
				log.Printf("Server process ended unexpectedly: %v", err)
			}
		}
	}()

	return nil
}

func (sm *ServerManager) waitForServers() error {
	// Create an HTTP client with timeout
	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	// Check for ion-sfu
	checkIon := func() error {
		resp, err := client.Get("http://localhost:7000")
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return nil
	}

	// Check for Node server
	checkNode := func() error {
		resp, err := client.Get("http://localhost:3000")
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return nil
	}

	// Try for up to 30 seconds
	timeout := time.After(30 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	log.Println("Waiting for servers to be ready...")
	for {
		select {
		case <-sm.ctx.Done():
			return fmt.Errorf("context cancelled while waiting for servers")
		case <-timeout:
			return fmt.Errorf("timeout waiting for servers")
		case <-ticker.C:
			ionErr := checkIon()
			nodeErr := checkNode()
			if ionErr == nil && nodeErr == nil {
				log.Println("All servers are ready!")
				return nil
			}
		}
	}
}

func (sm *ServerManager) Cleanup() {
	sm.cancel() // Cancel context

	if sm.cmd != nil && sm.cmd.Process != nil {
		// Try graceful shutdown first
		if err := sm.cmd.Process.Signal(os.Interrupt); err != nil {
			log.Printf("Failed to send interrupt signal: %v", err)
			// Force kill if interrupt fails
			if err := sm.cmd.Process.Kill(); err != nil {
				log.Printf("Failed to kill process: %v", err)
			}
		}

		// Wait for the process to exit
		timeout := time.After(5 * time.Second)
		done := make(chan error, 1)
		go func() {
			_, err := sm.cmd.Process.Wait()
			done <- err
		}()

		select {
		case err := <-done:
			if err != nil {
				log.Printf("Error waiting for process to exit: %v", err)
			}
		case <-timeout:
			log.Printf("Timeout waiting for process to exit")
		}
	}
}

// InitializeServers starts and monitors the servers
func InitializeServers() (*ServerManager, error) {
	sm := NewServerManager()

	if err := sm.startServers(); err != nil {
		sm.Cleanup()
		return nil, fmt.Errorf("failed to start servers: %v", err)
	}

	if err := sm.waitForServers(); err != nil {
		sm.Cleanup()
		return nil, fmt.Errorf("servers failed to start: %v", err)
	}

	return sm, nil
}
