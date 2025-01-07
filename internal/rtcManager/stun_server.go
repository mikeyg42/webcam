package rtcManager

import (
	"fmt"
	"log"
	"net"
	"sync"

	"github.com/pion/stun/v3"
)

type STUNServer struct {
	listener  net.PacketConn
	port      int
	publicIP  string
	mu        sync.RWMutex
	isRunning bool
	done      chan struct{}
}

func NewSTUNServer(port int) (*STUNServer, error) {
	// Get public IP (you might want to implement a more robust method)
	publicIP, err := getPublicIP()
	if err != nil {
		return nil, fmt.Errorf("failed to get public IP: %v", err)
	}

	return &STUNServer{
		port:     port,
		publicIP: publicIP,
		done:     make(chan struct{}),
	}, nil
}

func (s *STUNServer) Start() error {
	s.mu.Lock()
	if s.isRunning {
		s.mu.Unlock()
		return fmt.Errorf("STUN server is already running")
	}

	addr := fmt.Sprintf(":%d", s.port)
	listener, err := net.ListenPacket("udp4", addr)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("failed to start STUN server: %v", err)
	}

	s.listener = listener
	s.isRunning = true
	s.mu.Unlock()

	log.Printf("STUN server listening on %s", addr)

	go s.serve()
	return nil
}

func (s *STUNServer) serve() {
	buffer := make([]byte, 1500)
	for {
		select {
		case <-s.done:
			return
		default:
			n, addr, err := s.listener.ReadFrom(buffer)
			if err != nil {
				log.Printf("Error reading from connection: %v", err)
				continue
			}

			message := buffer[:n]
			if !stun.IsMessage(message) {
				continue
			}

			var msg stun.Message
			if err := stun.Decode(message, &msg); err != nil {
				log.Printf("Failed to decode STUN message: %v", err)
				continue
			}

			if msg.Type != stun.BindingRequest {
				continue
			}

			// Create STUN response
			resp := &stun.Message{
				Type: stun.BindingSuccess,
			}

			// Add XOR-MAPPED-ADDRESS attribute
			clientIP, clientPort, err := net.SplitHostPort(addr.String())
			if err != nil {
				log.Printf("Failed to parse client address: %v", err)
				continue
			}

			port, _ := net.LookupPort("udp", clientPort)
			xorAddr := &stun.XORMappedAddress{
				IP:   net.ParseIP(clientIP),
				Port: port,
			}

			if err := resp.Build(
				stun.BindingSuccess,
				stun.NewTransactionIDSetter(msg.TransactionID),
				xorAddr,
				stun.NewSoftware("pion-stun"),
				stun.Fingerprint,
			); err != nil {
				log.Printf("Failed to build STUN response: %v", err)
				continue
			}

			if _, err := s.listener.WriteTo(resp.Raw, addr); err != nil {
				log.Printf("Failed to send STUN response: %v", err)
			}
		}
	}
}

func (s *STUNServer) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.isRunning {
		return nil
	}

	close(s.done)
	if s.listener != nil {
		if err := s.listener.Close(); err != nil {
			return fmt.Errorf("failed to close listener: %v", err)
		}
	}

	s.isRunning = false
	return nil
}

// Helper function to get public IP
func getPublicIP() (string, error) {
	// You might want to use a more reliable service or method
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "", err
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String(), nil
}
