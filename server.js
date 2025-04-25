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
        url: process.env.ION_SFU_URL || 'ws://localhost:7000/ws',
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
app.use(helmet({
    contentSecurityPolicy: {
        directives: {
            defaultSrc: ["'self'"],
            scriptSrc: ["'self'", "unpkg.com"],
            connectSrc: ["'self'", "ws:", "wss:", config.ionSfu.url], // Allow connection to SFU
            imgSrc: ["'self'", "data:"],
            styleSrc: ["'self'", "'unsafe-inline'"]
        }
    }
}));
app.use(cors(config.cors));
app.use(express.json({ limit: '1mb' }));

// Serve static files
app.use(express.static(path.join(__dirname, 'public')));

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
            // Broadcast message from ion-sfu to all clients in this room
            this.broadcast(message);
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