package rtcManager

import (
	"context"
	"fmt"
	"log"
	"net"
	"regexp"
	"runtime/debug"
	"sync"
	"syscall"

	"time"

	"github.com/pion/turn/v4"
	"golang.org/x/sys/unix"

	"github.com/mikeyg42/webcam/internal/config"
)

type TURNServer struct {
	Server    *turn.Server
	Realm     string
	PublicIP  string
	Port      int
	mu        sync.RWMutex
	ctx       context.Context
	cancel    context.CancelFunc
	isRunning bool
	done      chan struct{} // Missing channel for cleanup signaling
	configs   *config.TURNConfigs
	startTime time.Time
}

type TURNStats struct {
	ActiveAllocations int
	Uptime            time.Duration
	CurrentState      string
}

func (t *TURNServer) NewTURNServer(ctx context.Context) error {
	TURNConfigs := t.configs

	addr, err := net.ResolveUDPAddr("udp", "0.0.0.0:"+fmt.Sprintf("%d", TURNConfigs.Port))
	if err != nil {
		return fmt.Errorf("failed to parse server address: %s", err)
	}

	//Cache -users flag for easy lookup later
	usersMap := map[string][]byte{}
	for _, kv := range regexp.MustCompile(`(\w+)=(\w+)`).FindAllStringSubmatch(TURNConfigs.Users, -1) {
		usersMap[kv[1]] = turn.GenerateAuthKey(kv[1], TURNConfigs.Realm, kv[2])
	}
	// Create `numThreads` UDP listeners to pass into pion/turn
	// UDP listeners share the same local address:port with setting SO_REUSEPORT and the kernel
	// will load-balance received packets per the IP 5-tuple
	listenerConfig := &net.ListenConfig{
		Control: func(network, address string, conn syscall.RawConn) error {
			var operr error
			if err = conn.Control(func(fd uintptr) {
				operr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, unix.SO_REUSEPORT, 1)
			}); err != nil {
				return err
			}

			return operr
		},
	}

	relayAddressGenerator := &turn.RelayAddressGeneratorPortRange{
		RelayAddress: net.ParseIP(TURNConfigs.PublicIP), // Claim that we are listening on IP passed by user
		Address:      "0.0.0.0",                         // But actually be listening on every interface
		MinPort:      49152,
		MaxPort:      65535,
	}

	if err := relayAddressGenerator.Validate(); err != nil {
		log.Fatalf("Failed to validate relay address generator: %s", err)
	}

	packetConnConfigs := make([]turn.PacketConnConfig, TURNConfigs.ThreadNum)
	for i := 0; i < TURNConfigs.ThreadNum; i++ {
		conn, listErr := listenerConfig.ListenPacket(ctx, addr.Network(), addr.String())
		if listErr != nil {
			log.Fatalf("Failed to allocate UDP listener at %s:%s", addr.Network(), addr.String())
		}

		packetConnConfigs[i] = turn.PacketConnConfig{
			PacketConn:            conn,
			RelayAddressGenerator: relayAddressGenerator,
		}

		log.Printf("Server %d listening on %s\n", i, conn.LocalAddr().String())
	}

	s, err := turn.NewServer(turn.ServerConfig{
		Realm: TURNConfigs.Realm,
		// Set AuthHandler callback,, called every time a user tries to authenticate
		// Return the key for that user, or false when no user is found
		AuthHandler: func(username string, realm string, srcAddr net.Addr) ([]byte, bool) {
			if key, ok := usersMap[username]; ok {
				return key, true
			}
			return nil, false
		},

		// PacketConnConfigs is a list of UDP Listeners and the configuration around them
		PacketConnConfigs: packetConnConfigs,
	})
	if err != nil {
		return fmt.Errorf("failed to create TURN server: %s", err)
	}

	t.Server = s
	return nil
}

func CreateTURNServer(ctx context.Context) (t *TURNServer) {
	TURNctx, cancel := context.WithCancel(ctx)
	TURNConfigs := config.GetTurnConfigs()

	t = &TURNServer{
		isRunning: false,
		Port:      TURNConfigs.Port,
		Realm:     TURNConfigs.Realm,
		PublicIP:  TURNConfigs.PublicIP,
		ctx:       TURNctx,
		cancel:    cancel,
		mu:        sync.RWMutex{},
		done:      make(chan struct{}), // Initialize the done channel
		configs:   TURNConfigs,
		startTime: time.Time{},
	}

	return t
}
func (t *TURNServer) Start() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.isRunning {
		return fmt.Errorf("TURN server is already running")
	}

	if err := t.NewTURNServer(t.ctx); err != nil {
		return fmt.Errorf("failed to initialize TURN server: %v", err)
	}

	t.startTime = time.Now()
	// Start the server in a goroutine
	go func() {
		defer close(t.done) // Signal completion when the goroutine exits
		t.serve()
	}()

	t.isRunning = true
	log.Printf("TURN server started on port %d", t.Port)
	return nil
}

func (t *TURNServer) Stop() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.isRunning {
		return nil
	}

	log.Println("Stopping TURN server...")

	// Cancel the context first to stop accepting new connections
	t.cancel()

	// Close the server
	if t.Server != nil {
		if err := t.Server.Close(); err != nil {
			return fmt.Errorf("failed to close TURN server: %v", err)
		}
	}

	t.isRunning = false

	// Wait for serve goroutine to complete with timeout
	select {
	case <-t.done:
		log.Println("TURN server stopped successfully")
	case <-time.After(10 * time.Second):
		return fmt.Errorf("timeout waiting for TURN server to stop")
	}

	return nil
}

func (t *TURNServer) serve() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Recovered from panic in TURN server: %v", r)
			debug.PrintStack()
		}
	}()

	// Create channel for monitoring server health
	healthCheck := time.NewTicker(30 * time.Second)
	defer healthCheck.Stop()

	for {
		select {
		case <-t.ctx.Done():
			log.Println("TURN server context cancelled, shutting down...")
			return

		case <-healthCheck.C:
			t.mu.RLock()
			if !t.isRunning {
				t.mu.RUnlock()
				continue
			}

			// Check ports
			if t.Server != nil {
				// Check for any blocked ports
				if err := t.checkPorts(); err != nil {
					log.Printf("Port check failed: %v", err)
				}
			}
			t.mu.RUnlock()
		}
	}
}

// Helper method to check if required ports are accessible
func (t *TURNServer) checkPorts() error {
	// Check UDP port
	udpAddr := fmt.Sprintf(":%d", t.Port)
	conn, err := net.ListenPacket("udp", udpAddr)
	if err != nil {
		return fmt.Errorf("UDP port %d is not available: %v", t.Port, err)
	}
	conn.Close()

	return nil
}

// Check if the server is currently running
func (t *TURNServer) IsRunning() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.isRunning
}

// GetState returns the current state of the TURN server
func (t *TURNServer) GetState() string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.Server == nil {
		return "uninitialized"
	}

	if !t.isRunning {
		return "stopped"
	}

	// Check if server is actually responding
	if err := t.checkPorts(); err != nil {
		return "degraded"
	}

	// Check allocation count to ensure server is accepting connections
	if t.Server.AllocationCount() > 0 {
		return "active"
	}

	return "idle"
}

// GetStats returns detailed statistics about the TURN server
func (t *TURNServer) GetStats() TURNStats {

	stats := TURNStats{
		ActiveAllocations: 0,
		Uptime:            time.Since(t.startTime),
		CurrentState:      t.GetState(),
	}

	if t.Server != nil {
		stats.ActiveAllocations = t.Server.AllocationCount()
	}

	return stats
}
