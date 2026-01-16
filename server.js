const express = require('express');
const http = require('http');
const WebSocket = require('ws');
const path = require('path');
const cors = require('cors');
const helmet = require('helmet');
const { v4: uuidv4 } = require('uuid'); // Add this dependency with: npm install uuid
const crypto = require('crypto');
const url = require('url'); // Added for parsing URL query parameters

// Rate-limited logger to prevent log flooding
const rateLimitedLog = (() => {
    const logHistory = new Map(); // key -> { count, lastTime, firstTime }
    const COOLDOWN_MS = 5000;     // 5 seconds between identical messages
    const BATCH_REPORT_MS = 10000; // Report suppressed count every 10s

    // Periodically report suppressed message counts
    setInterval(() => {
        const now = Date.now();
        logHistory.forEach((entry, key) => {
            if (entry.count > 1 && (now - entry.firstTime) > BATCH_REPORT_MS) {
                console.log(`[Log suppressed ${entry.count - 1}x in last ${Math.round((now - entry.firstTime) / 1000)}s]: ${key}`);
                logHistory.delete(key);
            }
        });
    }, BATCH_REPORT_MS);

    return {
        error: (key, ...args) => {
            const now = Date.now();
            const entry = logHistory.get(key);

            if (entry && (now - entry.lastTime) < COOLDOWN_MS) {
                // Suppress duplicate, increment counter
                entry.count++;
                entry.lastTime = now;
                return;
            }

            // Log the message
            console.error(...args);
            logHistory.set(key, { count: 1, lastTime: now, firstTime: now });
        },
        warn: (key, ...args) => {
            const now = Date.now();
            const entry = logHistory.get(key);

            if (entry && (now - entry.lastTime) < COOLDOWN_MS) {
                entry.count++;
                entry.lastTime = now;
                return;
            }

            console.warn(...args);
            logHistory.set(key, { count: 1, lastTime: now, firstTime: now });
        }
    };
})();

// Configuration
const config = {
    port: process.env.PORT || 3000,
    host: process.env.HOST || '0.0.0.0',  // Listen on all interfaces (localhost, LAN, Tailscale)
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

// Trust proxy - needed to get real client IP from X-Forwarded-For when behind a proxy
// Since frontend connects to Node.js and Node.js connects to Go backend,
// we need the frontend's IP to reach the Go backend
app.set('trust proxy', true);

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
            styleSrc: ["'self'", "'unsafe-inline'", "https://fonts.googleapis.com"],
            fontSrc: ["'self'", "https://fonts.gstatic.com"],
            objectSrc: ["'none'"],
            frameAncestors: ["'none'"],
            upgradeInsecureRequests: null  // Disable HTTPS upgrade for development (no SSL certs)
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

// Track recent disconnections for reconnect detection
const recentDisconnections = new Map(); // key: `${ip}-${roomId}` -> { time, clientId }
const RECONNECT_WINDOW_MS = 30000; // 30 seconds

// Clean up old entries periodically
setInterval(() => {
    const now = Date.now();
    recentDisconnections.forEach((entry, key) => {
        if (now - entry.time > RECONNECT_WINDOW_MS * 2) {
            recentDisconnections.delete(key);
        }
    });
}, 60000); // Clean every minute

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
        // Map<clientId, { ws: WebSocket, ionWs: WebSocket, ionWsState: string }>
        this.clients = new Map();
    }

    addClient(clientWs) {
        const clientId = clientWs.id;

        // Create a dedicated ion-sfu connection for this client
        const clientData = {
            ws: clientWs,
            ionWs: null,
            ionWsState: 'closed',
            reconnectAttempts: 0,
            pendingMessages: []
        };

        this.clients.set(clientId, clientData);
        clientWs.roomId = this.id;
        clientWs.isAlive = true;

        // Create ion-sfu connection for this specific client
        this._connectClientToIonSfu(clientId);
    }

    removeClient(clientWs) {
        const clientData = this.clients.get(clientWs.id);
        if (clientData) {
            // Close the client's dedicated ion-sfu connection
            if (clientData.ionWs) {
                clientData.ionWsState = 'closed';
                clientData.ionWs.close();
            }
            this.clients.delete(clientWs.id);
        }
    }

    isEmpty() {
        return this.clients.size === 0;
    }

    forwardToIon(message, clientId) {
        const clientData = this.clients.get(clientId);
        if (!clientData) return;

        // Debug logging
        try {
            const msgStr = Buffer.isBuffer(message) ? message.toString('utf8') : String(message);
            const parsed = JSON.parse(msgStr);
            if (parsed.method) {
                console.log(`[client ${clientId.slice(0,8)} -> ion-sfu] Method: ${parsed.method}`);
                // Log full answer payload for debugging SDPType issue
                if (parsed.method === 'answer') {
                    console.log(`[DEBUG] Answer params type: ${typeof parsed.params}`);
                    console.log(`[DEBUG] Answer params.type: ${parsed.params?.type} (type: ${typeof parsed.params?.type})`);
                    console.log(`[DEBUG] Full answer JSON: ${JSON.stringify(parsed)}`);
                }
            }
        } catch (e) {}

        if (clientData.ionWs && clientData.ionWsState === 'open') {
            clientData.ionWs.send(message);
        } else {
            clientData.pendingMessages.push(message);
            if (clientData.ionWsState === 'closed') {
                this._connectClientToIonSfu(clientId);
            }
        }
    }

    _connectClientToIonSfu(clientId) {
        const clientData = this.clients.get(clientId);
        if (!clientData) return;
        if (clientData.ionWsState === 'connecting' || clientData.ionWsState === 'open') return;

        console.log(`Connecting client ${clientId.slice(0,8)} to ion-sfu for room: ${this.id}`);
        clientData.ionWsState = 'connecting';
        clientData.ionWs = new WebSocket(config.ionSfu.url);

        clientData.ionWs.on('open', () => {
            console.log(`Client ${clientId.slice(0,8)} connected to ion-sfu for room: ${this.id}`);
            clientData.ionWsState = 'open';
            clientData.reconnectAttempts = 0;

            // Send pending messages
            clientData.pendingMessages.forEach(msg => clientData.ionWs.send(msg));
            clientData.pendingMessages = [];
        });

        clientData.ionWs.on('message', (message) => {
            // Route all messages from ion-sfu directly to this client
            let messageText;
            if (Buffer.isBuffer(message)) {
                messageText = message.toString('utf8');
            } else if (typeof message === 'string') {
                messageText = message;
            } else {
                messageText = String(message);
            }

            // Debug logging
            try {
                const parsed = JSON.parse(messageText);
                if (parsed.method) {
                    console.log(`[ion-sfu -> client ${clientId.slice(0,8)}] Method: ${parsed.method}`);
                } else if (parsed.result !== undefined || parsed.error !== undefined) {
                    console.log(`[ion-sfu -> client ${clientId.slice(0,8)}] Response`);
                }
            } catch (e) {}

            // Send directly to this client only
            if (clientData.ws && clientData.ws.readyState === WebSocket.OPEN) {
                clientData.ws.send(messageText);
            }
        });

        clientData.ionWs.on('close', () => {
            console.log(`ion-sfu connection closed for client ${clientId.slice(0,8)} in room: ${this.id}`);
            clientData.ionWs = null;

            // Don't reconnect if client is gone
            if (!this.clients.has(clientId)) return;

            if (clientData.reconnectAttempts < config.ionSfu.maxReconnectAttempts) {
                clientData.reconnectAttempts++;
                clientData.ionWsState = 'reconnecting';
                console.log(`Reconnecting client ${clientId.slice(0,8)} to ion-sfu (Attempt: ${clientData.reconnectAttempts})`);
                setTimeout(() => this._connectClientToIonSfu(clientId), config.ionSfu.reconnectInterval);
            } else {
                console.error(`Max reconnect attempts for client ${clientId.slice(0,8)} in room: ${this.id}`);
                clientData.ionWsState = 'closed';
                if (clientData.ws && clientData.ws.readyState === WebSocket.OPEN) {
                    clientData.ws.send(JSON.stringify({ type: 'error', message: 'Media server connection failed.' }));
                }
            }
        });

        clientData.ionWs.on('error', (error) => {
            rateLimitedLog.error(`ion-sfu-${clientId.slice(0,8)}`, `ion-sfu error for client ${clientId.slice(0,8)}:`, error.message);
            if (clientData.ionWsState !== 'reconnecting' && clientData.ionWsState !== 'closed') {
                clientData.ionWs.close();
            }
        });
    }

    closeIonConnection() {
        // Close all client ion-sfu connections
        this.clients.forEach((clientData, clientId) => {
            if (clientData.ionWs) {
                console.log(`Closing ion-sfu connection for client ${clientId.slice(0,8)}`);
                clientData.ionWsState = 'closed';
                clientData.ionWs.close();
            }
        });
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
    ws.clientIp = clientIp; // Store for disconnect tracking
    ws.isAlive = true; // For keepalive checks

    // Check if this is a reconnection
    const disconnectKey = `${clientIp}-${roomId}`;
    const recentDisconnect = recentDisconnections.get(disconnectKey);
    if (recentDisconnect) {
        const timeSinceDisconnect = Date.now() - recentDisconnect.time;
        console.log(`[Reconnect] Client reconnecting from ${clientIp} to room ${roomId} (was ${recentDisconnect.clientId}, disconnected ${Math.round(timeSinceDisconnect / 1000)}s ago)`);
        recentDisconnections.delete(disconnectKey);
    } else {
        console.log(`Client ${clientId} connected from ${clientIp} to room ${roomId}`);
    }

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
             room.forwardToIon(message, clientId);
        } else {
             console.warn(`Received non-forwardable message type from ${clientId}: ${typeof message}`);
        }
    });

    // Handle client disconnection
    ws.on('close', (code, reason) => {
        const disconnectKey = `${ws.clientIp}-${roomId}`;
        recentDisconnections.set(disconnectKey, { time: Date.now(), clientId });

        // Log with close code for debugging
        const reasonStr = reason ? reason.toString() : 'none';
        console.log(`Client ${clientId} disconnected from room ${roomId} (code: ${code}, reason: ${reasonStr})`);
        roomManager.removeClient(ws);
    });

    // Handle errors
    ws.on('error', (error) => {
        rateLimitedLog.error(`ws-error-${roomId}`, `WebSocket error for client in room ${roomId}:`, error.message);
        // Trigger cleanup on error as well
        roomManager.removeClient(ws);
        ws.terminate(); // Force close on error
    });
});

// --- Keepalive Interval ---
const heartbeatInterval = setInterval(() => {
    roomManager.rooms.forEach(room => {
        room.clients.forEach((clientData, clientId) => {
            const ws = clientData.ws;
            if (ws.isAlive === false) {
                console.warn(`Keepalive failed for client ${clientId.slice(0,8)} in room ${ws.roomId}. Terminating.`);
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

// Helper to get client IP and create headers for proxying
function getProxyHeaders(req) {
    const clientIP = req.ip || req.connection.remoteAddress || req.socket.remoteAddress;
    return {
        'Content-Type': 'application/json',
        'X-Forwarded-For': clientIP,  // Forward real client IP to Go backend
        'X-Forwarded-Proto': req.protocol,
        'X-Forwarded-Host': req.get('host')
    };
}

app.get('/api/config', async (req, res) => {
    try {
        const response = await fetch(`${goBackendUrl}/api/config`, {
            headers: getProxyHeaders(req)
        });

        // Check content type to handle both JSON and text responses
        const contentType = response.headers.get('content-type');
        if (contentType && contentType.includes('application/json')) {
            const data = await response.json();
            res.status(response.status).json(data);
        } else {
            const text = await response.text();
            res.status(response.status).json({ error: text });
        }
    } catch (error) {
        rateLimitedLog.error('api-config-get', 'Error fetching config from Go backend:', error.message);
        res.status(500).json({ error: 'Failed to fetch configuration' });
    }
});

app.post('/api/config', async (req, res) => {
    try {
        const response = await fetch(`${goBackendUrl}/api/config`, {
            method: 'POST',
            headers: getProxyHeaders(req),
            body: JSON.stringify(req.body)
        });

        // Check content type to handle both JSON and text responses
        const contentType = response.headers.get('content-type');
        if (contentType && contentType.includes('application/json')) {
            const data = await response.json();
            res.status(response.status).json(data);
        } else {
            // Handle text/plain error responses
            const text = await response.text();
            res.status(response.status).json({ error: text });
        }
    } catch (error) {
        rateLimitedLog.error('api-config-post', 'Error updating config:', error.message);
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

        // Check content type to handle both JSON and text responses
        const contentType = response.headers.get('content-type');
        if (contentType && contentType.includes('application/json')) {
            const data = await response.json();
            res.status(response.status).json(data);
        } else {
            const text = await response.text();
            res.status(response.status).json({ error: text });
        }
    } catch (error) {
        rateLimitedLog.error('api-notification', 'Error testing notification:', error.message);
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

        // Check content type to handle both JSON and text responses
        // Go's http.Error() returns text/plain which would crash response.json()
        const contentType = response.headers.get('content-type');
        if (contentType && contentType.includes('application/json')) {
            const data = await response.json();
            res.status(response.status).json(data);
        } else {
            // Handle text/plain error responses from Go's http.Error()
            const text = await response.text();
            res.status(response.status).json({ error: text });
        }
    } catch (error) {
        rateLimitedLog.error(`api-proxy-${req.method}`, `Error proxying ${req.method} /api${req.url} to Go backend:`, error.message);
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

// Start server with dynamic port selection
function startServerWithDynamicPort(port, maxAttempts = 10) {
    const tryPort = (currentPort, attempts) => {
        if (attempts > maxAttempts) {
            console.error(`Failed to find available port after ${maxAttempts} attempts`);
            process.exit(1);
        }

        server.listen(currentPort, config.host)
            .on('listening', () => {
                const actualPort = server.address().port;
                console.log(`Server running at http://${config.host}:${actualPort}`);
                console.log(`WebSocket server available at ws://${config.host}:${actualPort}${config.websocket.path}?roomId=<yourRoomId>`);
                console.log(`Proxying connections to ion-sfu at ${config.ionSfu.url}`);

                if (actualPort !== config.port) {
                    console.log(`Note: Using port ${actualPort} instead of configured port ${config.port}`);
                }
            })
            .on('error', (err) => {
                if (err.code === 'EADDRINUSE') {
                    console.log(`Port ${currentPort} is in use, trying port ${currentPort + 1}...`);
                    tryPort(currentPort + 1, attempts + 1);
                } else {
                    console.error('Server error:', err);
                    process.exit(1);
                }
            });
    };

    tryPort(port, 1);
}

startServerWithDynamicPort(config.port);

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
// Only exit on truly fatal errors - transient network failures should not crash the server
process.on('uncaughtException', (err, origin) => {
    console.error('Uncaught Exception:', err, 'Origin:', origin);

    // Check if this is a recoverable error
    const isRecoverable = (
        err.code === 'ECONNREFUSED' ||
        err.code === 'ECONNRESET' ||
        err.code === 'ETIMEDOUT' ||
        err.code === 'ENOTFOUND' ||
        err.message?.includes('fetch failed') ||
        err.message?.includes('socket hang up')
    );

    if (isRecoverable) {
        console.warn('Recoverable error detected, continuing operation...');
        return;
    }

    // For truly fatal errors, exit
    console.error('Fatal error - exiting');
    process.exit(1);
});

// Handle unhandled promise rejections
// Log them but don't crash - many are transient network errors
process.on('unhandledRejection', (reason, promise) => {
    console.error('Unhandled Rejection at:', promise, 'reason:', reason);

    // Check if this is a recoverable rejection
    const isRecoverable = (
        reason?.code === 'ECONNREFUSED' ||
        reason?.code === 'ECONNRESET' ||
        reason?.code === 'ETIMEDOUT' ||
        reason?.code === 'ENOTFOUND' ||
        reason?.message?.includes('fetch failed') ||
        reason?.cause?.code === 'ECONNREFUSED'
    );

    if (isRecoverable) {
        console.warn('Recoverable rejection detected, continuing operation...');
        return;
    }

    // For non-recoverable rejections, log but don't crash
    console.error('Non-fatal unhandled rejection - continuing operation...');
});

module.exports = server; // Export for testing