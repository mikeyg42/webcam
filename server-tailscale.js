const express = require('express');
const http = require('http');
const WebSocket = require('ws');
const path = require('path');
const cors = require('cors');
const helmet = require('helmet');
const { v4: uuidv4 } = require('uuid');
const crypto = require('crypto');
const url = require('url');
const { exec } = require('child_process');
const { promisify } = require('util');

const execAsync = promisify(exec);

// Configuration
const config = {
    port: process.env.PORT || 3000,
    host: process.env.HOST || 'localhost',
    cors: {
        origin: process.env.CORS_ORIGIN || '*',
        methods: ['GET', 'POST']
    },
    ionSfu: {
        url: process.env.ION_SFU_URL || 'ws://localhost:7000/ws',
        reconnectInterval: 5000,
        maxReconnectAttempts: 10
    },
    websocket: {
        path: '/ws',
        keepAliveInterval: 30000
    },
    tailscale: {
        enabled: process.env.TAILSCALE_ENABLED === 'true',
        nodeName: process.env.TAILSCALE_NODE_NAME || 'webcam-proxy',
        statusCheckInterval: 60000, // Check Tailscale status every minute
    }
};

// Tailscale integration
class TailscaleIntegration {
    constructor() {
        this.isConnected = false;
        this.localIP = null;
        this.hostname = null;
        this.peers = new Map();
        this.statusCheckTimer = null;
    }

    async initialize() {
        if (!config.tailscale.enabled) {
            console.log('Tailscale integration disabled');
            return false;
        }

        try {
            const status = await this.getStatus();
            if (status.BackendState === 'Running') {
                this.isConnected = true;
                this.localIP = status.Self.TailAddr;
                this.hostname = status.Self.HostName;
                
                // Update peers list
                this.updatePeers(status.Peer);
                
                console.log(`Tailscale connected - Hostname: ${this.hostname}, IP: ${this.localIP}`);
                
                // Start periodic status checks
                this.startStatusChecking();
                return true;
            } else {
                console.log(`Tailscale not running (state: ${status.BackendState})`);
                return false;
            }
        } catch (error) {
            console.error('Failed to initialize Tailscale:', error.message);
            return false;
        }
    }

    async getStatus() {
        const { stdout } = await execAsync('tailscale status --json');
        return JSON.parse(stdout);
    }

    updatePeers(peerData) {
        this.peers.clear();
        for (const [id, peer] of Object.entries(peerData)) {
            if (peer.Online && peer.TailAddr) {
                this.peers.set(peer.HostName, {
                    id,
                    hostname: peer.HostName,
                    ip: peer.TailAddr,
                    dnsName: peer.DNSName,
                    online: peer.Online
                });
            }
        }
        console.log(`Updated ${this.peers.size} online Tailscale peers`);
    }

    startStatusChecking() {
        if (this.statusCheckTimer) {
            clearInterval(this.statusCheckTimer);
        }

        this.statusCheckTimer = setInterval(async () => {
            try {
                const status = await this.getStatus();
                this.isConnected = status.BackendState === 'Running';
                this.updatePeers(status.Peer);
            } catch (error) {
                console.error('Tailscale status check failed:', error.message);
                this.isConnected = false;
            }
        }, config.tailscale.statusCheckInterval);
    }

    stopStatusChecking() {
        if (this.statusCheckTimer) {
            clearInterval(this.statusCheckTimer);
            this.statusCheckTimer = null;
        }
    }

    getOptimizedIonSfuUrl() {
        if (!this.isConnected) {
            return config.ionSfu.url;
        }

        // If ion-sfu is running on a Tailscale peer, use its Tailscale IP
        const ionSfuPeer = Array.from(this.peers.values()).find(peer => 
            peer.hostname.includes('ion-sfu') || peer.hostname.includes('sfu')
        );

        if (ionSfuPeer) {
            const originalUrl = new URL(config.ionSfu.url);
            return `${originalUrl.protocol}//${ionSfuPeer.ip}:${originalUrl.port || '7000'}${originalUrl.pathname}`;
        }

        return config.ionSfu.url;
    }

    getPeerByHostname(hostname) {
        return this.peers.get(hostname);
    }

    getAllPeers() {
        return Array.from(this.peers.values());
    }

    getLocalInfo() {
        return {
            connected: this.isConnected,
            ip: this.localIP,
            hostname: this.hostname,
            peerCount: this.peers.size
        };
    }
}

// Initialize Tailscale
const tailscale = new TailscaleIntegration();

// Initialize Express app
const app = express();

// Security middleware with Tailscale-friendly CSP
const cspDirectives = {
    defaultSrc: ["'self'"],
    scriptSrc: ["'self'", "unpkg.com"],
    connectSrc: ["'self'", "ws:", "wss:", config.ionSfu.url],
    imgSrc: ["'self'", "data:"],
    styleSrc: ["'self'", "'unsafe-inline'"]
};

// Add Tailscale IPs to CSP if available
if (config.tailscale.enabled) {
    cspDirectives.connectSrc.push("100.*", "fd7a:*"); // Tailscale IP ranges
}

app.use(helmet({
    contentSecurityPolicy: {
        directives: cspDirectives
    }
}));

app.use(cors(config.cors));
app.use(express.json({ limit: '1mb' }));

// Serve static files
app.use(express.static(path.join(__dirname, 'public')));

// Tailscale status endpoint
app.get('/tailscale/status', (req, res) => {
    res.json(tailscale.getLocalInfo());
});

// Tailscale peers endpoint
app.get('/tailscale/peers', (req, res) => {
    res.json(tailscale.getAllPeers());
});

// Create HTTP server
const server = http.createServer(app);

// --- Enhanced Room and Session Management for Tailscale ---

class TailscaleAwareRoomManager extends RoomManager {
    constructor() {
        super();
    }

    getOptimizedIonUrl() {
        return tailscale.getOptimizedIonSfuUrl();
    }

    createRoomWithTailscaleInfo(roomId) {
        const room = this.getOrCreateRoom(roomId);
        room.tailscaleInfo = tailscale.getLocalInfo();
        return room;
    }
}

class TailscaleAwareRoom extends Room {
    constructor(roomId) {
        super(roomId);
        this.tailscaleInfo = null;
    }

    _connectToIonSfu() {
        if (this.ionWsState === 'connecting' || this.ionWsState === 'open') return;

        // Use optimized Ion SFU URL if Tailscale is available
        const ionUrl = tailscale.getOptimizedIonSfuUrl();
        console.log(`Connecting to ion-sfu for room: ${this.id} at ${ionUrl} (Attempt: ${this.reconnectAttempts + 1})`);
        
        this.ionWsState = 'connecting';
        this.ionWs = new WebSocket(ionUrl);

        this.ionWs.on('open', () => {
            console.log(`Connected to ion-sfu for room: ${this.id} via ${tailscale.isConnected ? 'Tailscale' : 'traditional'} networking`);
            this.ionWsState = 'open';
            this.reconnectAttempts = 0;

            // Send pending messages
            this.pendingMessages.forEach(msg => this.ionWs.send(msg));
            this.pendingMessages = [];
        });

        this.ionWs.on('message', (message) => {
            // Broadcast message from ion-sfu to all clients in this room
            this.broadcast(message);
        });

        this.ionWs.on('close', () => {
            console.log(`ion-sfu connection closed for room: ${this.id}`);
            this.ionWs = null;
            
            if (this.isEmpty()) {
                console.log(`Room ${this.id} is empty, not reconnecting ion-sfu.`);
                this.ionWsState = 'closed';
                return;
            }

            if (this.reconnectAttempts < config.ionSfu.maxReconnectAttempts) {
                this.reconnectAttempts++;
                this.ionWsState = 'reconnecting';
                console.log(`Attempting reconnect to ion-sfu for room ${this.id} in ${config.ionSfu.reconnectInterval}ms (Attempt: ${this.reconnectAttempts})`);
                setTimeout(() => this._connectToIonSfu(), config.ionSfu.reconnectInterval);
            } else {
                console.error(`Max reconnect attempts reached for ion-sfu in room: ${this.id}`);
                this.ionWsState = 'closed';
                this.broadcast(JSON.stringify({ 
                    type: 'error', 
                    message: 'Media server connection failed permanently.',
                    tailscaleEnabled: tailscale.isConnected
                }));
            }
        });

        this.ionWs.on('error', (error) => {
            console.error(`ion-sfu connection error for room ${this.id}:`, error.message);
            if (this.ionWsState !== 'reconnecting' && this.ionWsState !== 'closed') {
                this.ionWs.close();
            }
        });
    }

    addClientWithTailscaleInfo(client) {
        this.addClient(client);
        
        // Send Tailscale info to the client
        if (tailscale.isConnected) {
            try {
                client.send(JSON.stringify({
                    type: 'tailscale_info',
                    data: {
                        enabled: true,
                        localIP: tailscale.localIP,
                        hostname: tailscale.hostname,
                        peers: tailscale.getAllPeers().map(peer => ({
                            hostname: peer.hostname,
                            ip: peer.ip
                        }))
                    }
                }));
            } catch (error) {
                console.error(`Failed to send Tailscale info to client ${client.id}:`, error);
            }
        }
    }
}

// Copy RoomManager and Room classes from original server.js but use Tailscale-aware versions
class RoomManager {
    constructor() {
        this.rooms = new Map();
    }

    getOrCreateRoom(roomId) {
        if (!this.rooms.has(roomId)) {
            console.log(`Creating new room: ${roomId}`);
            this.rooms.set(roomId, new TailscaleAwareRoom(roomId));
        }
        return this.rooms.get(roomId);
    }

    removeClient(client) {
        if (client.roomId && this.rooms.has(client.roomId)) {
            const room = this.rooms.get(client.roomId);
            room.removeClient(client);
            if (room.isEmpty()) {
                console.log(`Room empty, closing ion-sfu connection and removing room: ${client.roomId}`);
                room.closeIonConnection();
                this.rooms.delete(client.roomId);
            }
        }
    }
}

class Room {
    constructor(roomId) {
        this.id = roomId;
        this.clients = new Map();
        this.ionWs = null;
        this.ionWsState = 'closed';
        this.reconnectAttempts = 0;
        this.pendingMessages = [];
    }

    addClient(client) {
        this.clients.set(client.id, client);
        client.roomId = this.id;
        client.isAlive = true;
        this.ensureIonConnection();
    }

    removeClient(client) {
        this.clients.delete(client.id);
    }

    isEmpty() {
        return this.clients.size === 0;
    }

    broadcast(message, senderClient = null) {
        this.clients.forEach(client => {
            if (client !== senderClient && client.readyState === WebSocket.OPEN) {
                try {
                    client.send(message);
                } catch (error) {
                    console.error(`Error broadcasting to client ${client.id} in room ${this.id}:`, error);
                }
            }
        });
    }

    forwardToIon(message) {
        if (this.ionWs && this.ionWsState === 'open') {
            this.ionWs.send(message);
        } else {
            this.pendingMessages.push(message);
            if (this.ionWsState === 'closed') {
                this.ensureIonConnection();
            }
        }
    }

    ensureIonConnection() {
        if (this.ionWsState === 'closed' || this.ionWsState === 'reconnecting') {
            this._connectToIonSfu();
        }
    }

    _connectToIonSfu() {
        // This will be overridden by TailscaleAwareRoom
        if (this.ionWsState === 'connecting' || this.ionWsState === 'open') return;

        console.log(`Connecting to ion-sfu for room: ${this.id} (Attempt: ${this.reconnectAttempts + 1})`);
        this.ionWsState = 'connecting';
        this.ionWs = new WebSocket(config.ionSfu.url);

        // ... rest of the connection logic (same as original)
    }

    closeIonConnection() {
        if (this.ionWs) {
            console.log(`Closing ion-sfu connection explicitly for room: ${this.id}`);
            this.ionWsState = 'closed';
            this.ionWs.close();
            this.ionWs = null;
        }
    }
}

// --- WebSocket Server Setup with Tailscale Integration ---

const roomManager = new RoomManager();

const wss = new WebSocket.Server({
    server,
    path: config.websocket.path,
    verifyClient: (info, cb) => {
        const { query } = url.parse(info.req.url, true);
        const roomId = query.roomId;

        if (!roomId) {
            console.warn('Client connection rejected: Missing roomId query parameter.');
            cb(false, 400, 'Room ID is required');
        } else {
            info.req.roomId = roomId;
            cb(true);
        }
    }
});

wss.on('connection', (ws, req) => {
    const clientIp = req.socket.remoteAddress;
    const clientId = uuidv4();
    const roomId = req.roomId;

    ws.id = clientId;
    ws.roomId = roomId;
    ws.isAlive = true;

    console.log(`Client ${clientId} connected from ${clientIp} to room ${roomId} ${tailscale.isConnected ? '(Tailscale available)' : '(Traditional networking)'}`);

    // Add client to the room with Tailscale info
    const room = roomManager.getOrCreateRoom(roomId);
    if (room.addClientWithTailscaleInfo) {
        room.addClientWithTailscaleInfo(ws);
    } else {
        room.addClient(ws);
    }

    ws.on('pong', () => {
        ws.isAlive = true;
    });

    ws.on('message', (message) => {
        if (Buffer.isBuffer(message) || typeof message === 'string') {
            room.forwardToIon(message);
        } else {
            console.warn(`Received non-forwardable message type from ${clientId}: ${typeof message}`);
        }
    });

    ws.on('close', () => {
        console.log(`Client ${clientId} disconnected from room ${roomId}`);
        roomManager.removeClient(ws);
    });

    ws.on('error', (error) => {
        console.error(`WebSocket error for client ${clientId} in room ${roomId}:`, error);
        roomManager.removeClient(ws);
        ws.terminate();
    });
});

// Keepalive interval (same as original)
const heartbeatInterval = setInterval(() => {
    roomManager.rooms.forEach(room => {
        room.clients.forEach(ws => {
            if (ws.isAlive === false) {
                console.warn(`Keepalive failed for client ${ws.id} in room ${ws.roomId}. Terminating.`);
                roomManager.removeClient(ws);
                return ws.terminate();
            }
            ws.isAlive = false;
            ws.ping();
        });
    });
}, config.websocket.keepAliveInterval);

wss.on('close', () => {
    clearInterval(heartbeatInterval);
});

// Error handling middleware
app.use((err, req, res, next) => {
    console.error(err.stack);
    res.status(500).send('Something broke!');
});

// Initialize and start server
async function startServer() {
    console.log('Starting server with Tailscale integration...');
    
    // Initialize Tailscale
    const tailscaleInitialized = await tailscale.initialize();
    
    if (tailscaleInitialized) {
        console.log('✅ Tailscale integration active');
        console.log(`🔗 Local Tailscale IP: ${tailscale.localIP}`);
        console.log(`🏠 Hostname: ${tailscale.hostname}`);
        console.log(`👥 Connected peers: ${tailscale.peers.size}`);
    } else {
        console.log('⚠️  Tailscale integration failed - using traditional networking');
    }

    // Start HTTP server
    server.listen(config.port, config.host, () => {
        const networkType = tailscaleInitialized ? 'Tailscale + Traditional' : 'Traditional';
        console.log(`🚀 Server running at http://${config.host}:${config.port} (${networkType} networking)`);
        console.log(`🔌 WebSocket server available at ws://${config.host}:${config.port}${config.websocket.path}?roomId=<yourRoomId>`);
        
        const ionUrl = tailscale.getOptimizedIonSfuUrl();
        console.log(`📡 Proxying connections to ion-sfu at ${ionUrl}`);
        
        if (tailscaleInitialized && tailscale.peers.size > 0) {
            console.log('📍 Available Tailscale peers:');
            tailscale.getAllPeers().forEach(peer => {
                console.log(`   - ${peer.hostname} (${peer.ip})`);
            });
        }
    });
}

// Graceful shutdown
process.on('SIGTERM', async () => {
    console.log('SIGTERM signal received: closing server...');

    // Stop Tailscale status checking
    tailscale.stopStatusChecking();

    // Close all room ion connections
    roomManager.rooms.forEach(room => {
        room.closeIonConnection();
    });

    // Close all client WebSocket connections
    wss.clients.forEach(client => {
        client.terminate();
    });

    // Close HTTP server
    server.close(() => {
        console.log('HTTP server closed');
        process.exit(0);
    });

    setTimeout(() => {
        console.error("Graceful shutdown timed out. Forcing exit.");
        process.exit(1);
    }, 10000);
});

// Handle uncaught exceptions
process.on('uncaughtException', (err, origin) => {
    console.error('Uncaught Exception:', err, 'Origin:', origin);
    process.exit(1);
});

process.on('unhandledRejection', (reason, promise) => {
    console.error('Unhandled Rejection at:', promise, 'reason:', reason);
    process.exit(1);
});

// Start the server
startServer().catch(error => {
    console.error('Failed to start server:', error);
    process.exit(1);
});

module.exports = server;