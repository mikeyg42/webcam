const express = require('express');
const http = require('http');
const WebSocket = require('ws');
const path = require('path');
const cors = require('cors');
const helmet = require('helmet');
const { v4: uuidv4 } = require('uuid'); // Add this dependency with: npm install uuid
const crypto = require('crypto');
const url = require('url'); // Added for parsing URL query parameters

// Configuration
const config = {
    port: process.env.PORT || 3000,
    host: process.env.HOST || 'localhost',
    cors: {
        origin: process.env.CORS_ORIGIN || '*', // Allow configuring origin via env var
        methods: ['GET', 'POST']
    },
    ionSfu: {
        url: process.env.ION_SFU_URL || 'ws://localhost:7001/ws',
        reconnectInterval: 5000,
        maxReconnectAttempts: 10
    },
    websocket: {
        path: '/ws',
        keepAliveInterval: 30000 // 30 seconds
    }
};

// Initialize Express app
const app = express();

// Security middleware
const ionSfuUrl = process.env.ION_SFU_URL || 'ws://localhost:7001/ws';
const ionSfuWssUrl = ionSfuUrl.replace('ws://', 'wss://');

app.use(helmet({
    contentSecurityPolicy: {
        directives: {
            defaultSrc: ["'self'"],
            scriptSrc: ["'self'", "https://unpkg.com", "'unsafe-eval'"],
            connectSrc: ["'self'", "ws:", "wss:", ionSfuUrl, ionSfuWssUrl],
            mediaSrc: ["'self'", "blob:", "data:"],
            workerSrc: ["'self'", "blob:"],
            imgSrc: ["'self'", "data:", "blob:"],
            styleSrc: ["'self'", "'unsafe-inline'"],
            objectSrc: ["'none'"],
            frameAncestors: ["'none'"]
        }
    }
}));
app.use(cors(config.cors));
app.use(express.json({ limit: '1mb' }));

// Tailscale authentication middleware (for when TAILSCALE_ENABLED=true)
function tailscaleAuthMiddleware(req, res, next) {
    const tailscaleEnabled = process.env.TAILSCALE_ENABLED === 'true';

    if (!tailscaleEnabled) {
        // If Tailscale is not enabled, allow all requests (traditional setup)
        return next();
    }

    const clientIP = req.ip || req.connection.remoteAddress || req.headers['x-forwarded-for'];

    // Check if the request is coming from a Tailscale IP range
    const isTailscaleIP = (ip) => {
        // Tailscale IP ranges: 100.64.0.0/10 and fd7a:115c:a1e0::/48
        if (ip.startsWith('100.')) {
            const parts = ip.split('.');
            const secondOctet = parseInt(parts[1], 10);
            return secondOctet >= 64 && secondOctet <= 127;
        }
        if (ip.startsWith('fd7a:115c:a1e0:')) {
            return true;
        }
        // Also allow localhost for development
        return ip === '127.0.0.1' || ip === '::1' || ip === 'localhost';
    };

    if (!isTailscaleIP(clientIP)) {
        console.warn(`Access denied for non-Tailscale IP: ${clientIP}`);
        return res.status(403).json({
            error: 'Access denied',
            message: 'This camera system is only accessible via Tailscale network'
        });
    }

    console.log(`Tailscale access granted for IP: ${clientIP}`);
    next();
}

// Apply Tailscale authentication when enabled
if (process.env.TAILSCALE_ENABLED === 'true') {
    app.use(tailscaleAuthMiddleware);
}

// Serve static files from the new React build (or fallback to old public if not built yet)
const publicDir = path.join(__dirname, 'public-new');
const fallbackDir = path.join(__dirname, 'public');
const fs = require('fs');

if (fs.existsSync(publicDir)) {
    console.log('Serving React app from public-new/');
    app.use(express.static(publicDir));
} else {
    console.log('React build not found, serving from public/ (old interface)');
    app.use(express.static(fallbackDir));
}

// Create HTTP server
const server = http.createServer(app);

// --- Room and Session Management ---

class RoomManager {
    constructor() {
        // Map<roomId, Room>
        this.rooms = new Map();
    }

    getOrCreateRoom(roomId) {
        if (!this.rooms.has(roomId)) {
            console.log(`Creating new room: ${roomId}`);
            this.rooms.set(roomId, new Room(roomId));
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
        // Map<clientId, WebSocket>
        this.clients = new Map();
        this.ionWs = null;
        this.ionWsState = 'closed'; // 'connecting', 'open', 'closed', 'reconnecting'
        this.reconnectAttempts = 0;
        this.pendingMessages = []; // Messages from clients waiting for ion connection
    }

    addClient(client) {
        this.clients.set(client.id, client);
        client.roomId = this.id;
        client.isAlive = true; // For keepalive

        // Ensure ion connection is established for this room
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
            // Optionally skip broadcasting back to the sender
            if (client !== senderClient && client.readyState === WebSocket.OPEN) {
                try {
                    // Ensure message is sent as text
                    const messageText = typeof message === 'string' ? message : String(message);
                    client.send(messageText);
                } catch (error) {
                    console.error(`Error broadcasting to client ${client.id} in room ${this.id}:`, error);
                }
            }
        });
    }

    forwardToIon(message) {
         if (this.ionWs && this.ionWsState === 'open') {
            // Ensure message is sent as text to ion-sfu
           // const messageText = Buffer.isBuffer(message) ? message.toString('utf8') :
                //               typeof message === 'string' ? message : String(message);
           // this.ionWs.send(messageText);
           this.ionWs.send(message);
        } else {
            // Store as text for pending messages
            //const messageText = Buffer.isBuffer(message) ? message.toString('utf8') :
           //                   typeof message === 'string' ? message : String(message);
            //this.pendingMessages.push(messageText);
            this.pendingMessages.push(message)
             // Attempt to connect if not already trying
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
        if (this.ionWsState === 'connecting' || this.ionWsState === 'open') return; // Already connecting or open

        console.log(`Connecting to ion-sfu for room: ${this.id} (Attempt: ${this.reconnectAttempts + 1})`);
        this.ionWsState = 'connecting';
        this.ionWs = new WebSocket(config.ionSfu.url);

        this.ionWs.on('open', () => {
            console.log(`Connected to ion-sfu for room: ${this.id}`);
            this.ionWsState = 'open';
            this.reconnectAttempts = 0;

            // Send pending messages
            this.pendingMessages.forEach(msg => this.ionWs.send(msg));
            this.pendingMessages = [];
        });

        this.ionWs.on('message', (message) => {
            // Ensure message is sent as text to browser clients
            let messageText;
            if (Buffer.isBuffer(message)) {
                messageText = message.toString('utf8');
            } else if (typeof message === 'string') {
                messageText = message;
            } else {
                messageText = String(message);
            }
            // Broadcast message from ion-sfu to all clients in this room
            this.broadcast(messageText);
        });

        this.ionWs.on('close', () => {
            console.log(`ion-sfu connection closed for room: ${this.id}`);
            this.ionWs = null; // Clear the closed socket
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
                // Notify clients in the room about the failure?
                 this.broadcast(JSON.stringify({ type: 'error', message: 'Media server connection failed permanently.' }));
            }
        });

        this.ionWs.on('error', (error) => {
            console.error(`ion-sfu connection error for room ${this.id}:`, error.message);
             // Close event will likely follow, triggering reconnect logic
             if (this.ionWsState !== 'reconnecting' && this.ionWsState !== 'closed') {
                this.ionWs.close(); // Ensure close is triggered if error doesn't auto-close
             }
        });
    }

    closeIonConnection() {
        if (this.ionWs) {
            console.log(`Closing ion-sfu connection explicitly for room: ${this.id}`);
             // Prevent automatic reconnection by setting state first
            this.ionWsState = 'closed';
            this.ionWs.close();
            this.ionWs = null;
        }
    }
}

// --- WebSocket Server Setup ---

const roomManager = new RoomManager();

const wss = new WebSocket.Server({
    server,
    path: config.websocket.path,
    verifyClient: (info, cb) => {
        // Extract roomId from query parameters
        const { query } = url.parse(info.req.url, true);
        const roomId = query.roomId;

        if (!roomId) {
            console.warn('Client connection rejected: Missing roomId query parameter.');
            cb(false, 400, 'Room ID is required');
        } else {
            // Attach roomId to the request for later use
            info.req.roomId = roomId;
            cb(true);
        }
    }
});

wss.on('connection', (ws, req) => {
    const clientIp = req.socket.remoteAddress;
    const clientId = uuidv4();
    const roomId = req.roomId; // Get roomId attached by verifyClient

    // Enhance the WebSocket object with client info
    ws.id = clientId;
    ws.roomId = roomId;
    ws.isAlive = true; // For keepalive checks

    console.log(`Client ${clientId} connected from ${clientIp} to room ${roomId}`);

    // Add client to the room
    const room = roomManager.getOrCreateRoom(roomId);
    room.addClient(ws);

    // Setup ping/pong for keepalive
    ws.on('pong', () => {
        ws.isAlive = true;
    });

    // Handle messages from this client
    ws.on('message', (message) => {
         // Ensure message is Buffer or string before forwarding
        if (Buffer.isBuffer(message) || typeof message === 'string') {
             room.forwardToIon(message);
        } else {
             console.warn(`Received non-forwardable message type from ${clientId}: ${typeof message}`);
        }
    });

    // Handle client disconnection
    ws.on('close', () => {
        console.log(`Client ${clientId} disconnected from room ${roomId}`);
        roomManager.removeClient(ws);
    });

    // Handle errors
    ws.on('error', (error) => {
        console.error(`WebSocket error for client ${clientId} in room ${roomId}:`, error);
        // Trigger cleanup on error as well
        roomManager.removeClient(ws);
        ws.terminate(); // Force close on error
    });
});

// --- Keepalive Interval ---
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

// Proxy configuration API to Go backend
const goBackendUrl = process.env.GO_BACKEND_URL || 'http://localhost:8081';

app.get('/api/config', async (req, res) => {
    try {
        const response = await fetch(`${goBackendUrl}/api/config`);
        const data = await response.json();
        res.json(data);
    } catch (error) {
        console.error('Error fetching config from Go backend:', error);
        res.status(500).json({ error: 'Failed to fetch configuration' });
    }
});

app.post('/api/config', async (req, res) => {
    try {
        const response = await fetch(`${goBackendUrl}/api/config`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(req.body)
        });
        const data = await response.json();
        res.json(data);
    } catch (error) {
        console.error('Error updating config:', error);
        res.status(500).json({ error: 'Failed to update configuration' });
    }
});

app.post('/api/test-notification', async (req, res) => {
    try {
        const response = await fetch(`${goBackendUrl}/api/test-notification`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(req.body)
        });
        const data = await response.json();
        res.json(data);
    } catch (error) {
        console.error('Error testing notification:', error);
        res.status(500).json({ error: 'Failed to test notification' });
    }
});

// API Routes
// Authentication status endpoint
app.get('/api/auth/status', (req, res) => {
    const clientIP = req.ip || req.connection.remoteAddress || req.headers['x-forwarded-for'];
    const tailscaleEnabled = process.env.TAILSCALE_ENABLED === 'true';

    if (!tailscaleEnabled) {
        return res.json({
            authenticated: true,
            method: 'none',
            message: 'Traditional setup - no additional authentication required',
            clientIP: clientIP
        });
    }

    res.json({
        authenticated: true,
        method: 'tailscale',
        message: 'Access granted via Tailscale network',
        clientIP: clientIP
    });
});

// Endpoint to provide WebRTC configuration (Tailscale-only)
app.get('/api/webrtc-config', (req, res) => {
    // Tailscale-only configuration - no TURN server needed
    const webrtcConfig = {
        codec: 'vp9',
        iceServers: [
            // STUN for initial discovery (optional with Tailscale)
            { urls: "stun:stun.l.google.com:19302" }
        ],
        iceTransportPolicy: 'all' // Tailscale handles NAT traversal
    };

    res.json(webrtcConfig);
});

// Debug endpoints (only in development)
if (process.env.NODE_ENV === 'development') {
    app.get('/debug/csp', (req, res) => {
        const cspDirectives = {
            defaultSrc: ["'self'"],
            scriptSrc: ["'self'", "https://unpkg.com", "'unsafe-eval'"],
            connectSrc: ["'self'", "ws:", "wss:", ionSfuUrl, ionSfuWssUrl],
            mediaSrc: ["'self'", "blob:", "data:"],
            workerSrc: ["'self'", "blob:"],
            imgSrc: ["'self'", "data:", "blob:"],
            styleSrc: ["'self'", "'unsafe-inline'"],
            objectSrc: ["'none'"],
            frameAncestors: ["'none'"]
        };
        res.json({
            csp: cspDirectives,
            tailscale: { enabled: false },
            peers: [],
            clientIP: req.ip || req.connection.remoteAddress || req.headers['x-forwarded-for']
        });
    });

    app.get('/debug/network', (req, res) => {
        res.json({
            ionSfuUrl: config.ionSfu.url,
            optimizedIonSfuUrl: config.ionSfu.url,
            tailscaleStatus: { enabled: false },
            peers: [],
            clientIP: req.ip || req.connection.remoteAddress || req.headers['x-forwarded-for'],
            config: {
                tailscaleEnabled: false,
                port: config.port,
                host: config.host
            }
        });
    });

    app.get('/debug/rooms', (req, res) => {
        const roomsInfo = [];
        roomManager.rooms.forEach((room, roomId) => {
            roomsInfo.push({
                id: roomId,
                clientCount: room.clients.size,
                ionWsState: room.ionWsState,
                reconnectAttempts: room.reconnectAttempts,
                pendingMessages: room.pendingMessages.length
            });
        });
        res.json({
            rooms: roomsInfo,
            totalRooms: roomManager.rooms.size
        });
    });
}

// General API proxy - forward all other /api/* requests to Go backend
app.use('/api', async (req, res) => {
    try {
        // req.url here has /api stripped by Express, so we need to add it back
        const url = `${goBackendUrl}/api${req.url}`;
        const options = {
            method: req.method,
            headers: { 'Content-Type': 'application/json' },
        };

        if (req.method !== 'GET' && req.method !== 'HEAD') {
            options.body = JSON.stringify(req.body);
        }

        const response = await fetch(url, options);
        const data = await response.json();
        res.status(response.status).json(data);
    } catch (error) {
        console.error(`Error proxying ${req.method} /api${req.url} to Go backend:`, error);
        res.status(500).json({ error: 'Failed to proxy request to backend' });
    }
});

// SPA catch-all route - serve index.html for all non-API routes
app.get('*', (req, res) => {
    const indexPath = fs.existsSync(publicDir)
        ? path.join(publicDir, 'index.html')
        : path.join(fallbackDir, 'index.html');
    res.sendFile(indexPath);
});

// Error handling middleware
app.use((err, req, res, next) => {
    console.error(err.stack);
    res.status(500).send('Something broke!');
});

// Start server
server.listen(config.port, config.host, () => {
    console.log(`Server running at http://${config.host}:${config.port}`);
    console.log(`WebSocket server available at ws://${config.host}:${config.port}${config.websocket.path}?roomId=<yourRoomId>`);
    console.log(`Proxying connections to ion-sfu at ${config.ionSfu.url}`);
});

// Graceful shutdown
process.on('SIGTERM', () => {
    console.log('SIGTERM signal received: closing server...');

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

     // Force exit after timeout if server doesn't close gracefully
    setTimeout(() => {
        console.error("Graceful shutdown timed out. Forcing exit.");
        process.exit(1);
    }, 10000); // 10 seconds timeout
});

// Handle uncaught exceptions
process.on('uncaughtException', (err, origin) => {
    console.error('Uncaught Exception:', err, 'Origin:', origin);
    // Implement more sophisticated error handling/logging here if needed
    process.exit(1); // Exit uncleanly on uncaught exception
});

// Handle unhandled promise rejections
process.on('unhandledRejection', (reason, promise) => {
    console.error('Unhandled Rejection at:', promise, 'reason:', reason);
    // Implement more sophisticated error handling/logging here if needed
    process.exit(1); // Exit uncleanly on unhandled rejection
});

module.exports = server; // Export for testing